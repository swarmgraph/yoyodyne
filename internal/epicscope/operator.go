package epicscope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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
	joined    JoinedReader
}

// JoinedReader reads the pieces that joined, beside a reading of the record
// (epicprogress.Holding). An act asks it under the record's lock.
type JoinedReader func(Record) (Joins, error)

// Joins is which open epics an item is in, the one it joined included, and why
// an epic's progress cannot be read, where it cannot.
type Joins struct {
	Holders    func(item string) []Epic
	Unreadable func(Epic) string
}

// Knowing is the same acts with the pieces that joined an epic known, so that
// one is refused as a member of another.
func (o *Operator) Knowing(joined JoinedReader) *Operator {
	knowing := *o
	knowing.joined = joined
	return &knowing
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
	// Texts is each member's text as it reads now, copied beside it. A member with
	// none is kept without one.
	Texts map[string]Text
}

// Started is what Start did.
type Started struct {
	Record Record
	// Epic is the epic's open entry, the new one or the one it already had.
	Epic Epic
	// Named says the epic was given a new entry, and TurnedOn that epic mode was
	// off before. At least one holds.
	Named    bool
	TurnedOn bool
}

// Start turns epic mode on where it is off and names the epic, with exactly the
// members given. An epic that ended gets a new entry and its ended one is left
// as it is. An epic still open keeps its entry unchanged, whatever the naming
// says, so starting it only turns epic mode on and is refused where it is on.
func (o *Operator) Start(naming Naming, at time.Time) (Started, error) {
	var started Started
	record, err := o.change(func(record *Record) error {
		started.TurnedOn = !record.On()
		record.Mode = ModeEpic
		if index := record.index(naming.Epic); index >= 0 && record.Epics[index].Open() {
			if !started.TurnedOn {
				return fmt.Errorf("epic %s is already open; `yoyo epic add %s <item>` adds an item to it", naming.Epic, naming.Epic)
			}
			started.Epic = record.Epics[index]
			return nil
		}
		if err := o.free(*record, "", append([]string{naming.Epic}, naming.Members...)...); err != nil {
			return err
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
			member := Member{WorkItemID: item, AddedAt: at.UTC(), Act: ActStart}
			if text, copied := naming.Texts[item]; copied {
				member.Text = &text
			}
			epic.Members = append(epic.Members, member)
		}
		record.Epics = append(record.Epics, epic)
		started.Epic, started.Named = epic, true
		return nil
	})
	if err != nil {
		return Started{}, err
	}
	started.Record = record
	return started, nil
}

// Add adds an item to an open epic's members. An item the epic already has is
// left as it was, with when it was first added.
func (o *Operator) Add(epic, item string, at time.Time) (Record, error) {
	return o.AddCopying(epic, item, nil, at)
}

// AddCopying is Add with the item's text as it reads now copied beside it, which
// is what the operator's command does.
func (o *Operator) AddCopying(epic, item string, text *Text, at time.Time) (Record, error) {
	return o.change(func(record *Record) error {
		index, err := record.open(epic)
		if err != nil {
			return fmt.Errorf("%w; `yoyo epic start %s` names it", err, epic)
		}
		if record.Epics[index].Has(item) {
			return errUnchanged
		}
		if err := o.free(*record, epic, item); err != nil {
			return err
		}
		record.Epics[index].Members = append(record.Epics[index].Members, Member{WorkItemID: item, AddedAt: at.UTC(), Act: ActAdd, Text: text})
		return nil
	})
}

// Remove takes one item out of an open epic, a member or a piece that joined it,
// with the operator's words. What joined is known only where the epic's progress
// can be read; a member of the record is taken out either way.
func (o *Operator) Remove(epic, item, words string, at time.Time) (Record, error) {
	return o.change(func(record *Record) error {
		index, err := record.open(epic)
		if err != nil {
			return err
		}
		entry := &record.Epics[index]
		removal := Removal{WorkItemID: item, RemovedAt: at.UTC(), Words: strings.TrimSpace(words)}
		if removal.Words == "" {
			return fmt.Errorf("taking work item %s out of epic %s needs your words, because the check of the whole epic is given them", item, epic)
		}
		if member := slices.IndexFunc(entry.Members, func(member Member) bool { return member.WorkItemID == item }); member >= 0 {
			removal.AddedAt, removal.Act = &entry.Members[member].AddedAt, entry.Members[member].Act
			entry.Members = slices.Concat(entry.Members[:member], entry.Members[member+1:])
			if entry.Members == nil {
				entry.Members = []Member{}
			}
		} else if err := o.joinedIt(*record, *entry, item); err != nil {
			return err
		}
		entry.Removed = append(entry.Removed, removal)
		return nil
	})
}

// joinedIt refuses an item that did not join the epic, or of which that cannot
// be known.
func (o *Operator) joinedIt(record Record, epic Epic, item string) error {
	if o.joined != nil {
		joins, err := o.joined(record)
		if err != nil {
			return fmt.Errorf("whether work item %s joined epic %s cannot be known: %w", item, epic.WorkItemID, err)
		}
		if why := joins.Unreadable(epic); why != "" {
			return fmt.Errorf("whether work item %s joined epic %s cannot be known, because the epic's progress cannot be read: %s", item, epic.WorkItemID, why)
		}
		if slices.ContainsFunc(joins.Holders(item), func(holder Epic) bool { return holder.WorkItemID == epic.WorkItemID }) {
			return nil
		}
	}
	return fmt.Errorf("work item %s is not in epic %s", item, epic.WorkItemID)
}

// SetBudget sets the limit on what an open epic may spend before it starts
// nothing new, replacing any it had. It is the only way a budget is written.
func (o *Operator) SetBudget(epic string, limitUSD float64, at time.Time) (Record, error) {
	if err := ValidBudget(limitUSD); err != nil {
		return Record{}, err
	}
	return o.change(func(record *Record) error {
		index, err := record.open(epic)
		if err != nil {
			return err
		}
		record.Epics[index].Budget = &Budget{LimitUSD: limitUSD, SetAt: at.UTC()}
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
		return 0, fmt.Errorf("epic %s was never named", epic)
	}
	if !r.Epics[index].Open() {
		return 0, fmt.Errorf("epic %s has already ended", epic)
	}
	return index, nil
}

// free refuses an item another open epic has, as a member or as a piece that
// joined. It is asked under the lock, so two epics named at once cannot both take
// the item, and nor can a join.
func (o *Operator) free(record Record, epic string, items ...string) error {
	joined := func(string) []Epic { return nil }
	if o.joined != nil {
		joins, err := o.joined(record)
		if err != nil {
			return err
		}
		joined = joins.Holders
	}
	for _, item := range items {
		holder, held := record.Holding(item)
		for _, other := range joined(item) {
			if !held && other.WorkItemID != epic {
				holder, held = other, true
			}
		}
		if held {
			return fmt.Errorf("work item %s is already in epic %s, which has not ended", item, holder.WorkItemID)
		}
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

// UnderLock holds the lock every act takes and gives a reading of the record, so
// that no act of the operator comes between what is read and what is done. It
// writes nothing, because what it makes is never a change.
func UnderLock(stateRoot string, product domain.ProductID, do func(Record) error) error {
	reader, err := ForOperator(stateRoot, product)
	if err != nil {
		return err
	}
	_, err = reader.change(func(record *Record) error {
		if err := do(*record); err != nil {
			return err
		}
		return errUnchanged
	})
	return err
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
