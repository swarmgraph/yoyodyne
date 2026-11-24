package epicscope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// lockWait bounds the wait for the lock. What it waits on is one small write by
// another process, so a longer wait is a holder that is not coming back.
const lockWait = 5 * time.Second

// Operator makes the operator's own acts on the record. Each act reads the file
// and replaces it whole while holding the lock, so two acts at once lose
// neither, and an act that is refused writes nothing.
type Operator struct {
	stateRoot string
	productID domain.ProductID
}

// ForOperator opens the record for the operator's acts. Only a command the
// operator runs may call it (onlywriter_test.go).
func ForOperator(stateRoot string, product domain.ProductID) (*Operator, error) {
	if !filepath.IsAbs(stateRoot) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(product)); err != nil {
		return nil, err
	}
	return &Operator{stateRoot: filepath.Clean(stateRoot), productID: product}, nil
}

// Naming is what the operator names an epic with.
type Naming struct {
	Epic        string
	From        string // where the operator named it
	Words       string // what the operator said, if anything
	Description string // the epic's description as it reads now
	Members     []string
}

// Start turns epic mode on where it is off and names the epic, with exactly the
// members given. An epic that was named before, open or ended, is refused: it
// has its one entry, and naming it again would rewrite what that entry says.
func (o *Operator) Start(naming Naming, at time.Time) (Record, error) {
	return o.change(func(record *Record) error {
		if epic, named := record.Named(naming.Epic); named {
			if !epic.Open() {
				return fmt.Errorf("epic %s was named before and has ended", naming.Epic)
			}
			return fmt.Errorf("epic %s is already named", naming.Epic)
		}
		epic := Epic{
			WorkItemID:  naming.Epic,
			NamedAt:     at.UTC(),
			NamedFrom:   strings.TrimSpace(naming.From),
			Words:       strings.TrimSpace(naming.Words),
			Description: naming.Description,
			Members:     []Member{},
		}
		for _, item := range naming.Members {
			if err := record.free(item); err != nil {
				return err
			}
			epic.Members = append(epic.Members, Member{WorkItemID: item, AddedAt: at.UTC(), Act: ActStart})
		}
		record.Mode = ModeEpic
		record.Epics = append(record.Epics, epic)
		return nil
	})
}

// Add adds an item to an open epic's members. An item the epic already has is
// left as it was, with when it was first added.
func (o *Operator) Add(epic, item string, at time.Time) (Record, error) {
	return o.change(func(record *Record) error {
		index, err := record.open(epic)
		if err != nil {
			return err
		}
		if record.Epics[index].Has(item) {
			return errUnchanged
		}
		if err := record.free(item); err != nil {
			return err
		}
		record.Epics[index].Members = append(record.Epics[index].Members, Member{WorkItemID: item, AddedAt: at.UTC(), Act: ActAdd})
		return nil
	})
}

// End ends an open epic and keeps its entry. Epic mode stays on, the last epic
// included, so ending one starts nothing unrelated.
func (o *Operator) End(epic, words string, at time.Time) (Record, error) {
	return o.change(func(record *Record) error {
		index, err := record.open(epic)
		if err != nil {
			return err
		}
		record.Epics[index].Ended = &Ending{At: at.UTC(), Words: strings.TrimSpace(words)}
		return nil
	})
}

// TurnOff turns epic mode off and changes no epic's entry. Where it is already
// off nothing is written.
func (o *Operator) TurnOff() (Record, error) {
	return o.change(func(record *Record) error {
		if !record.On() {
			return errUnchanged
		}
		record.Mode = ModeGoal
		return nil
	})
}

// open finds the entry of an epic that was named and has not ended.
func (r Record) open(epic string) (int, error) {
	index := r.index(epic)
	if index < 0 {
		return 0, fmt.Errorf("epic %s has not been named", epic)
	}
	if !r.Epics[index].Open() {
		return 0, fmt.Errorf("epic %s has ended", epic)
	}
	return index, nil
}

// free refuses an item another open epic has. It is asked under the lock, so two
// epics named at once cannot both take the item.
func (r Record) free(item string) error {
	if holder, held := r.Holding(item); held {
		return fmt.Errorf("work item %s is already in epic %s, which has not ended", item, holder.WorkItemID)
	}
	return nil
}

// errUnchanged is an act that asks for what the record already says.
var errUnchanged = errors.New("nothing to change")

func (o *Operator) change(apply func(*Record) error) (Record, error) {
	root, err := runstate.PinStateRoot(o.stateRoot)
	if err != nil {
		return Record{}, fmt.Errorf("pin the state root: %w", err)
	}
	defer root.Close()
	directory := filepath.FromSlash(home.ProductDirectoryWithin(root.Path(), string(o.productID)))
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return Record{}, fmt.Errorf("create the product's state directory: %w", err)
	}
	product, err := root.OpenDirectory(directory)
	if err != nil {
		return Record{}, fmt.Errorf("open the product's state directory: %w", err)
	}
	defer product.Close()
	lock, err := product.OpenLock(lockName, 0o600)
	if err != nil {
		return Record{}, fmt.Errorf("open the lock on the record of named epics: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	release, err := runstate.LockFile(ctx, lock)
	if err != nil {
		return Record{}, fmt.Errorf("lock the record of named epics: %w", err)
	}
	defer release()

	record, err := Read(root.Path(), o.productID)
	if err != nil {
		return Record{}, err
	}
	if err := apply(&record); errors.Is(err, errUnchanged) {
		return record, nil
	} else if err != nil {
		return Record{}, err
	}
	if err := o.write(product, record); err != nil {
		return Record{}, err
	}
	return record, nil
}

// write replaces the file whole: the confined writer puts the content in a
// temporary file beside it and renames that over it, so a reader sees the old
// record or the new one. The bytes are read back as a record first, so nothing
// is written that Read would refuse.
func (o *Operator) write(product *repowrite.PinnedRoot, record Record) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("encode the record of named epics: %w", err)
	}
	if _, err := decode(encoded.Bytes(), o.productID); err != nil {
		return fmt.Errorf("refused record of named epics: %w", err)
	}
	// A directory moved out of the state root is refused on both sides of the
	// change (repository-writes-are-physically-confined).
	if err := product.Unchanged(); err != nil {
		return err
	}
	if err := product.WriteFile(fileName, encoded.Bytes(), 0o600, false); err != nil {
		return fmt.Errorf("write the record of named epics: %w", err)
	}
	return errors.Join(product.Unchanged(), product.Sync())
}
