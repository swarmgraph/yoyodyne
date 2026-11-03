package runstate

// The harness asking a provider again whether a window it refused with no reset
// time has reopened, recorded so the asking is paced.
//
// A refusal that names when it lifts is waited out to that moment. One that
// names nothing is not a wait of known length, and the only way to learn it has
// lifted is to ask: one bounded invocation — a developer dispatch, a role's
// recurring turn — sent into the window to find out. Unpaced, that asking was
// every pull, because nothing remembered that the last pull had already asked.
// So each such invocation writes down when it was sent, per account and model,
// and every reader of the refusals reads the next one as due no sooner than one
// probe interval after the later of the refusal and the last probe. The record
// is what survives a restart: a session started a minute after the last probe
// waits out the rest of the interval rather than asking again at once.
//
// It is one small document rewritten in place rather than a log, for the
// reason the served record is: what is needed is the latest probe per
// endpoint, never the history of them.

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

// CapacityProbeSchemaVersion is 1 and has never changed.
const CapacityProbeSchemaVersion = 1

// maxCapacityProbeWhatBytes bounds what one entry says was sent. It is a phrase
// somebody reads beside a waiting item, not a record they study.
const maxCapacityProbeWhatBytes = 1 << 10

// CapacityProbe is the latest moment the harness sent one invocation into a
// window the provider refused with no reset time, on one account and model.
type CapacityProbe struct {
	// AccountAlias is the configured account the probe was sent under, and empty
	// where the sender did not know it — in which case it paces the model's
	// probes on every account, as a refusal that names no account is read as
	// one on every account.
	AccountAlias string `json:"account,omitempty"`
	// Model is the model selector the window closes. Required.
	Model string `json:"model"`
	// At is when the probe was sent.
	At time.Time `json:"at"`
	// What says what was sent, in words — a developer run of a work item, a
	// role's recurring turn — so the next probe time can be read back to the
	// invocation that set it.
	What string `json:"what,omitempty"`
}

func (c CapacityProbe) key() string {
	return strings.TrimSpace(c.AccountAlias) + "\x00" + strings.TrimSpace(c.Model)
}

func (c CapacityProbe) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Model) == "" {
		problems = append(problems, errors.New("model is required: a probe that names no model paces nothing"))
	}
	if c.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if len(c.What) > maxCapacityProbeWhatBytes {
		problems = append(problems, fmt.Errorf("what was sent is %d bytes, which exceeds the %d byte bound", len(c.What), maxCapacityProbeWhatBytes))
	}
	return errors.Join(problems...)
}

// Paces reports whether this probe is one a refusal of model on account is
// paced by: the same model, and the same account where both name one.
func (c CapacityProbe) Paces(accountAlias, model string) bool {
	if strings.TrimSpace(c.Model) != strings.TrimSpace(model) {
		return false
	}
	account, probed := strings.TrimSpace(accountAlias), strings.TrimSpace(c.AccountAlias)
	return account == "" || probed == "" || account == probed
}

// CapacityProbeRecord is the whole document: the latest probe for each account
// and model the harness has probed.
type CapacityProbeRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Probes        []CapacityProbe  `json:"probes"`
}

// CapacityProbeStore is where the product's probes are kept, one file under the
// product beside the usage-limit log and the served record they are read with.
type CapacityProbeStore struct {
	root      string
	productID domain.ProductID
}

func NewCapacityProbeStore(root string, productID domain.ProductID) (*CapacityProbeStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &CapacityProbeStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

// Path names the document, so a failure can say where it is.
func (s *CapacityProbeStore) Path() string { return filepath.Join(s.root, "capacity-probes.json") }

// Record writes down that a probe was sent, replacing the entry for its account
// and model where this is later than what it holds. An earlier moment writes
// nothing: two sessions probing at nearly the same moment agree, and the later
// of them sets the next probe.
func (s *CapacityProbeStore) Record(probe CapacityProbe) error {
	probe.AccountAlias = strings.TrimSpace(probe.AccountAlias)
	probe.Model = strings.TrimSpace(probe.Model)
	probe.At = probe.At.UTC()
	if err := probe.Validate(); err != nil {
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
		if existing.key() != probe.key() {
			continue
		}
		replaced = true
		if !probe.At.After(existing.At) {
			return nil
		}
		recorded[index] = probe
	}
	if !replaced {
		recorded = append(recorded, probe)
	}
	sort.Slice(recorded, func(i, j int) bool { return recorded[i].key() < recorded[j].key() })
	return s.write(CapacityProbeRecord{
		SchemaVersion: CapacityProbeSchemaVersion,
		ProductID:     s.productID,
		Probes:        recorded,
	})
}

// List returns the latest probe for every account and model. No document is the
// ordinary answer on a product that has never had to probe, and is an empty
// list. A field this build does not know is stepped over rather than refused,
// so a newer build's record does not blank an older reader's pacing.
func (s *CapacityProbeStore) List() ([]CapacityProbe, error) {
	return s.read(false)
}

func (s *CapacityProbeStore) read(strict bool) ([]CapacityProbe, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open capacity probe record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return nil, fmt.Errorf("read capacity probe record: %w", err)
	}
	var record CapacityProbeRecord
	if strict {
		err = decodeStrictly(encoded, &record)
	} else {
		_, err = decodeTolerating(encoded, &record)
	}
	if err != nil {
		return nil, fmt.Errorf("decode capacity probe record: %w", err)
	}
	if record.SchemaVersion != CapacityProbeSchemaVersion {
		return nil, fmt.Errorf("capacity probe schema version %d is not supported", record.SchemaVersion)
	}
	if record.ProductID != s.productID {
		return nil, fmt.Errorf("capacity probe record belongs to product %q, not %q", record.ProductID, s.productID)
	}
	for _, probe := range record.Probes {
		if err := probe.Validate(); err != nil {
			return nil, fmt.Errorf("capacity probe record: %w", err)
		}
	}
	return record.Probes, nil
}

func (s *CapacityProbeStore) write(record CapacityProbeRecord) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".capacity-probes-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary capacity probe record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary capacity probe record: %w", err)
	}
	if err := writeJSONFile(temporary, "capacity probe record", record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary capacity probe record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("replace capacity probe record: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the writers: every watching session and every firing that
// probes writes here.
func (s *CapacityProbeStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open capacity probe lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), intakeLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the capacity probe record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
