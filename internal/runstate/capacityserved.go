package runstate

// The provider serving an account and a model, recorded as the evidence that a
// window it refused earlier has lifted.
//
// A refusal carries the reset the provider quoted, and until this nothing but
// that clock ended one. A quoted reset is a claim, and it goes stale the way
// every claim here does: on 2026-09-24 the operator added capacity a day into a
// seven-day window, every turn after that was served, and the dashboard went on
// showing two conversations blocked — one of them retired — until the quoted
// reset on 09-27. A turn the provider served on the model it had refused is the
// provider saying the window is open, and it says so more recently than the
// refusal did.
//
// So each served invocation writes down when it was served, one entry per
// account and model, and every reading of what the provider is refusing reads a
// refusal recorded before that moment, on that account and model, as lifted. It
// is one small document rewritten in place rather than a log: what is needed is
// the latest served moment per endpoint, never the history of them, and a log
// written on every served turn would grow with every turn the harness takes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// CapacityServedSchemaVersion is 1 and has never changed.
const CapacityServedSchemaVersion = 1

// maxCapacityServedWhatBytes bounds what one entry says was served. It is a
// phrase somebody reads beside a cleared block, not a record they study.
const maxCapacityServedWhatBytes = 1 << 10

// CapacityServed is the latest moment the provider served one account and
// model.
type CapacityServed struct {
	// AccountAlias is the configured account the invocation ran under, and empty
	// where the process that served it did not know — in which case it lifts the
	// model's refusals on every account, as a refusal that names no account is
	// lifted by a served turn on any.
	AccountAlias string `json:"account,omitempty"`
	// Model is the model selector the served invocation asked for. It is required:
	// a window is a fact about one model, and a served turn that cannot say which
	// model served it is evidence about nothing in particular.
	Model string `json:"model"`
	// At is when the invocation was served.
	At time.Time `json:"at"`
	// What says what was served, in words — a turn of a conversation, a developer
	// attempt of a run — so a block that cleared early can be read back to the
	// evidence that cleared it.
	What string `json:"what,omitempty"`
}

func (c CapacityServed) key() string {
	return strings.TrimSpace(c.AccountAlias) + "\x00" + strings.TrimSpace(c.Model)
}

func (c CapacityServed) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Model) == "" {
		problems = append(problems, errors.New("model is required: a served invocation that names no model lifts no window"))
	}
	if c.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if len(c.What) > maxCapacityServedWhatBytes {
		problems = append(problems, fmt.Errorf("what was served is %d bytes, which exceeds the %d byte bound", len(c.What), maxCapacityServedWhatBytes))
	}
	return errors.Join(problems...)
}

// Lifts reports whether this served invocation is evidence that the refusal's
// window has lifted: it was served after the refusal was recorded, on the model
// the refusal names and on the account the refusal names.
//
// A refusal that names no model was written before refusals carried one —
// every one of them before 2026-09-13 — and cannot be matched to a model, so any
// served invocation after it lifts it: the harness serving anything at all after
// an unattributable refusal is the only evidence that record can be read
// against. A refusal that names no account is every one written before
// refusals carried the account, and is lifted by the model being served on any
// account, for the same reason; so is any refusal where the served invocation
// could not name its own account.
func (c CapacityServed) Lifts(refusal UsageLimitExhaustion) bool {
	if !c.At.After(refusal.At) {
		return false
	}
	if model := strings.TrimSpace(refusal.Model); model != "" && model != strings.TrimSpace(c.Model) {
		return false
	}
	account, served := strings.TrimSpace(refusal.AccountAlias), strings.TrimSpace(c.AccountAlias)
	return account == "" || served == "" || account == served
}

// CapacityServedRecord is the whole document: the latest served moment for
// each account and model the harness has been served on.
type CapacityServedRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Served        []CapacityServed `json:"served"`
}

// CapacityServedStore is where the product's served moments are kept, one file
// under the product beside the usage-limit log it is read against.
type CapacityServedStore struct {
	root      string
	productID domain.ProductID
}

func NewCapacityServedStore(root string, productID domain.ProductID) (*CapacityServedStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &CapacityServedStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *CapacityServedStore) Root() string { return s.root }

// Path names the document, so a failure can say where it is.
func (s *CapacityServedStore) Path() string { return filepath.Join(s.root, "capacity-served.json") }

// Record writes down that the provider served an account and model, replacing
// the entry for that pair where this is later than what it holds. An earlier
// moment than the one recorded is not an error and writes nothing: two
// processes served at nearly the same moment agree about the window, and the
// later of them is the one that says the most.
func (s *CapacityServedStore) Record(served CapacityServed) error {
	served.AccountAlias = strings.TrimSpace(served.AccountAlias)
	served.Model = strings.TrimSpace(served.Model)
	served.At = served.At.UTC()
	if err := served.Validate(); err != nil {
		return err
	}
	release, err := s.lock()
	if err != nil {
		return err
	}
	defer release()
	recorded, err := s.read(true)
	if err != nil {
		return err
	}
	replaced := false
	for index, existing := range recorded {
		if existing.key() != served.key() {
			continue
		}
		replaced = true
		if !served.At.After(existing.At) {
			return nil
		}
		recorded[index] = served
	}
	if !replaced {
		recorded = append(recorded, served)
	}
	sort.Slice(recorded, func(i, j int) bool { return recorded[i].key() < recorded[j].key() })
	return s.write(CapacityServedRecord{
		SchemaVersion: CapacityServedSchemaVersion,
		ProductID:     s.productID,
		Served:        recorded,
	})
}

// List returns the latest served moment for every account and model. No
// document is the ordinary answer on a product nothing has been served on since
// this was recorded, and is an empty list rather than a failure to look. A
// document that cannot be read is an error: a block cleared on evidence nobody
// could read would be a guess. A field this build does not know is stepped over
// rather than refused, because what reads this is the read model and the sink,
// and a newer build's record must not blank their capacity reading.
func (s *CapacityServedStore) List() ([]CapacityServed, error) {
	return s.read(false)
}

// read is both doors onto the document: strict for Record, which rewrites what
// it read and would drop a field it stepped over, and tolerant for List.
func (s *CapacityServedStore) read(strict bool) ([]CapacityServed, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open capacity served record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return nil, fmt.Errorf("read capacity served record: %w", err)
	}
	var record CapacityServedRecord
	if strict {
		err = decodeStrictly(encoded, &record)
	} else {
		_, err = decodeTolerating(encoded, &record)
	}
	if err != nil {
		return nil, fmt.Errorf("decode capacity served record: %w", err)
	}
	if record.SchemaVersion != CapacityServedSchemaVersion {
		return nil, fmt.Errorf("capacity served schema version %d is not supported", record.SchemaVersion)
	}
	if record.ProductID != s.productID {
		return nil, fmt.Errorf("capacity served record belongs to product %q, not %q", record.ProductID, s.productID)
	}
	for _, served := range record.Served {
		if err := served.Validate(); err != nil {
			return nil, fmt.Errorf("capacity served record: %w", err)
		}
	}
	return record.Served, nil
}

func (s *CapacityServedStore) write(record CapacityServedRecord) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".capacity-served-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary capacity served record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary capacity served record: %w", err)
	}
	if err := writeJSONFile(temporary, "capacity served record", record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary capacity served record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("replace capacity served record: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the writers. Every process the provider serves writes here —
// runs, conversations, reviews — and a read-modify-write with nothing between
// two of them loses one endpoint's moment.
func (s *CapacityServedStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open capacity served lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), intakeLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the capacity served record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
