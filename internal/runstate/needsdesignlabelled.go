package runstate

// Since when each open item has carried the needs-design label, and whether
// the Lead Product Manager has been told about a P0 one waiting more than a
// day.
//
// "Showing the wait" in docs/designs/needs-design-gate.md asks for every item
// waiting on the architect's design to be shown with the time it began waiting
// and its age. The tracker keeps no time for a label being added, and the label
// is added by several paths — a conversation's action, an item created carrying
// it, a developer's raise — so the time is read the way the architect's wake
// reads the label: from the tracker as it stands, at every pull of a watching
// session. An item is recorded the first time a pull sees it labelled and keeps
// that time while it carries the label; one that loses the label leaves the
// record, so labelling it again starts a new wait. The time is therefore when
// the harness first saw the label, which is within one pull of its being set
// while a session is watching, and later than that by however long none was.
//
// It lives under the state root beside the wake record, in a file of its own
// so a build that knows only the wake record reads that record unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// NeedsDesignLabelledSchemaVersion is the shape of the stored record.
const NeedsDesignLabelledSchemaVersion = 1

const (
	needsDesignLabelledFile     = "needs-design-labelled.json"
	needsDesignLabelledLockFile = ".needs-design-labelled.lock"
)

// NeedsDesignWait is one item's wait on the architect's design.
type NeedsDesignWait struct {
	// Since is when a pull first saw the item carrying the label.
	Since time.Time `json:"since"`
	// ReportedAt is when the harness told the Lead Product Manager that this
	// item, at priority 0, had waited more than a day; absent until it has.
	ReportedAt *time.Time `json:"reported_at,omitempty"`
}

// NeedsDesignLabelled is every open item carrying the label as the last pull
// saw them, with since when.
type NeedsDesignLabelled struct {
	SchemaVersion int                        `json:"schema_version"`
	ProductID     domain.ProductID           `json:"product_id"`
	Items         map[string]NeedsDesignWait `json:"items,omitempty"`
	UpdatedAt     time.Time                  `json:"updated_at"`
}

// Since is when the item was first seen carrying the label, and false where it
// is not recorded as carrying it.
func (l NeedsDesignLabelled) Since(item string) (time.Time, bool) {
	wait, ok := l.Items[item]
	return wait.Since, ok
}

// Reported says the Lead Product Manager has been told about the item's wait.
func (l NeedsDesignLabelled) Reported(item string) bool {
	wait, ok := l.Items[item]
	return ok && wait.ReportedAt != nil
}

// Validate reports every contract violation in the record at once.
func (l NeedsDesignLabelled) Validate() error {
	var problems []error
	if l.SchemaVersion != NeedsDesignLabelledSchemaVersion {
		problems = append(problems, fmt.Errorf("needs-design labelled record schema version %d is not supported", l.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(l.ProductID)); err != nil {
		problems = append(problems, err)
	}
	for item, wait := range l.Items {
		if strings.TrimSpace(item) == "" {
			problems = append(problems, errors.New("an item is recorded as labelled with no id"))
		}
		if wait.Since.IsZero() {
			problems = append(problems, fmt.Errorf("item %q is recorded as labelled since no time", item))
		}
		if wait.ReportedAt != nil && wait.ReportedAt.IsZero() {
			problems = append(problems, fmt.Errorf("item %q is recorded as reported at no time", item))
		}
	}
	if l.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid needs-design labelled record: %w", err)
	}
	return nil
}

// NeedsDesignLabelledStore keeps the record for one product.
type NeedsDesignLabelledStore struct {
	root      string
	productID domain.ProductID
}

// NewNeedsDesignLabelledStore opens the record kept under a state root.
func NewNeedsDesignLabelledStore(root string, productID domain.ProductID) (*NeedsDesignLabelledStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &NeedsDesignLabelledStore{root: home.ProductDirectory(root, string(productID)), productID: productID}, nil
}

// NeedsDesignLabelled is the record for this run store's product.
func (s *Store) NeedsDesignLabelled() *NeedsDesignLabelledStore {
	return &NeedsDesignLabelledStore{root: filepath.Dir(s.root), productID: s.productID}
}

// Path names the record, so a failure can say where it is.
func (s *NeedsDesignLabelledStore) Path() string {
	return filepath.Join(s.root, needsDesignWakesDirectory, needsDesignLabelledFile)
}

// Load reads the record for a surface that only says what it holds. It reads
// past a field it does not know, so a newer build's record never stops the
// dashboard or the status listing; the writer below reads strictly instead.
// No record is the ordinary answer before a pull has seen the label on
// anything.
func (s *NeedsDesignLabelledStore) Load() (NeedsDesignLabelled, bool, error) {
	return s.load(false)
}

// load reads the record, refusing a field it does not know where strict is set.
func (s *NeedsDesignLabelledStore) load(strict bool) (NeedsDesignLabelled, bool, error) {
	stored, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return NeedsDesignLabelled{}, false, nil
	}
	if err != nil {
		return NeedsDesignLabelled{}, false, fmt.Errorf("read the needs-design labelled record: %w", err)
	}
	var record NeedsDesignLabelled
	if strict {
		err = decodeStrictly(bytes.TrimSpace(stored), &record)
	} else {
		err = json.Unmarshal(bytes.TrimSpace(stored), &record)
	}
	if err != nil {
		return NeedsDesignLabelled{}, false, fmt.Errorf("decode the needs-design labelled record at %s: %w", s.Path(), err)
	}
	if err := record.Validate(); err != nil {
		return NeedsDesignLabelled{}, false, err
	}
	if record.ProductID != s.productID {
		return NeedsDesignLabelled{}, false, fmt.Errorf("the needs-design labelled record at %s belongs to product %q, not %q", s.Path(), record.ProductID, s.productID)
	}
	return record, true, nil
}

// Observe records the items carrying the label as a pull read them at the
// moment given: an item not yet recorded is recorded as labelled since then,
// one already recorded keeps its time, and one no longer labelled is dropped.
// Nothing is written where nothing changed.
func (s *NeedsDesignLabelledStore) Observe(ctx context.Context, labelled []string, at time.Time) (NeedsDesignLabelled, error) {
	at = labelledMoment(at)
	return s.update(ctx, at, func(record *NeedsDesignLabelled) (bool, error) {
		current := make(map[string]bool, len(labelled))
		changed := false
		for _, item := range labelled {
			item = strings.TrimSpace(item)
			if item == "" {
				return false, errors.New("an item seen labelled has no id")
			}
			current[item] = true
			if _, kept := record.Items[item]; !kept {
				record.Items[item] = NeedsDesignWait{Since: at}
				changed = true
			}
		}
		for item := range record.Items {
			if !current[item] {
				delete(record.Items, item)
				changed = true
			}
		}
		return changed, nil
	})
}

// MarkReported records that the Lead Product Manager was told about an item's
// wait at the moment given. An item no longer recorded as labelled is left
// out: its wait is over.
func (s *NeedsDesignLabelledStore) MarkReported(ctx context.Context, item string, at time.Time) (NeedsDesignLabelled, error) {
	at = labelledMoment(at)
	return s.update(ctx, at, func(record *NeedsDesignLabelled) (bool, error) {
		wait, ok := record.Items[item]
		if !ok || wait.ReportedAt != nil {
			return false, nil
		}
		reported := at
		wait.ReportedAt = &reported
		record.Items[item] = wait
		return true, nil
	})
}

// labelledMoment is the moment a change is recorded at: the one given, in UTC,
// or now where none was.
func labelledMoment(at time.Time) time.Time {
	if at.IsZero() {
		at = time.Now()
	}
	return at.UTC()
}

// update is the locked read, change, and write every change to the record
// takes.
func (s *NeedsDesignLabelledStore) update(ctx context.Context, at time.Time, change func(*NeedsDesignLabelled) (bool, error)) (NeedsDesignLabelled, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return NeedsDesignLabelled{}, err
	}
	defer release()

	// Strictly, because the record is written back: a field this build does
	// not know would otherwise be dropped from it.
	record, _, err := s.load(true)
	if err != nil {
		return NeedsDesignLabelled{}, err
	}
	if record.Items == nil {
		record.Items = map[string]NeedsDesignWait{}
	}
	changed, err := change(&record)
	if err != nil {
		return NeedsDesignLabelled{}, err
	}
	if len(record.Items) == 0 {
		record.Items = nil
	}
	if !changed {
		return record, nil
	}
	record.SchemaVersion = NeedsDesignLabelledSchemaVersion
	record.ProductID = s.productID
	record.UpdatedAt = at
	if err := record.Validate(); err != nil {
		return NeedsDesignLabelled{}, err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return NeedsDesignLabelled{}, fmt.Errorf("encode the needs-design labelled record: %w", err)
	}
	confined, err := repowrite.NewRoot(s.root)
	if err != nil {
		return NeedsDesignLabelled{}, fmt.Errorf("resolve the product's state directory: %w", err)
	}
	if _, err := confined.WriteFile(filepath.Join(needsDesignWakesDirectory, needsDesignLabelledFile), append(encoded, '\n')); err != nil {
		return NeedsDesignLabelled{}, fmt.Errorf("record the needs-design labelled items: %w", err)
	}
	return record, nil
}

// lock serializes the read-modify-write of the record across every Yoyodyne
// process.
func (s *NeedsDesignLabelledStore) lock(ctx context.Context) (func(), error) {
	directory := filepath.Join(s.root, needsDesignWakesDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the needs-design labelled record's directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, needsDesignLabelledLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the needs-design labelled record's lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the needs-design labelled record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
