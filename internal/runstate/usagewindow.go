package runstate

// The provider's latest word on each of an account's usage windows: how much is
// used and when it resets.
//
// Claude Code reports its windows on nearly every invocation, and until this
// the event log was the only place that went — once per invocation, in a log
// per run or conversation, where nobody read it. What the operator asked for is
// the week's percentage and the time it resets, on the dashboard, without
// reading a log. So each reading is written here as it arrives, one entry per
// account and window, and a later reading of the same window replaces the
// earlier. It is one small document rewritten in place, like the served-capacity
// record beside it, because what is wanted is the latest reading and never the
// history of them; the history is still in the event logs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// UsageWindowSchemaVersion is 1 and has never changed.
const UsageWindowSchemaVersion = 1

// UsageWindowRecord is the whole document: the latest reading of every window
// of every account the provider has reported one for.
type UsageWindowRecord struct {
	SchemaVersion int                          `json:"schema_version"`
	ProductID     domain.ProductID             `json:"product_id"`
	Readings      []backend.UsageWindowReading `json:"readings"`
}

// UsageWindowStore is where the product's usage-window readings are kept, one
// file under the product beside the usage-limit log.
type UsageWindowStore struct {
	root      string
	productID domain.ProductID
}

var _ backend.UsageWindowRecorder = (*UsageWindowStore)(nil)

func NewUsageWindowStore(root string, productID domain.ProductID) (*UsageWindowStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &UsageWindowStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *UsageWindowStore) Root() string { return s.root }

// Path names the document, so a failure can say where it is.
func (s *UsageWindowStore) Path() string { return filepath.Join(s.root, "usage-windows.json") }

func validateUsageWindowReading(reading backend.UsageWindowReading) error {
	var problems []error
	if strings.TrimSpace(reading.Window) == "" {
		problems = append(problems, errors.New("window is required: a reading that names no window is about nothing"))
	}
	if reading.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	return errors.Join(problems...)
}

// RecordUsageWindows writes one event's readings, each replacing the reading of
// the same account and window where it is later. An earlier reading than the
// one recorded writes nothing for that window: two invocations hearing the same
// window at nearly the same moment agree about it, and the later says the most.
func (s *UsageWindowStore) RecordUsageWindows(readings []backend.UsageWindowReading) error {
	if len(readings) == 0 {
		return nil
	}
	for index := range readings {
		readings[index].Account = strings.TrimSpace(readings[index].Account)
		readings[index].Window = strings.TrimSpace(readings[index].Window)
		readings[index].At = readings[index].At.UTC()
		if err := validateUsageWindowReading(readings[index]); err != nil {
			return err
		}
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
	changed := false
	for _, reading := range readings {
		replaced := false
		for index, existing := range recorded {
			if existing.Key() != reading.Key() {
				continue
			}
			replaced = true
			if reading.At.After(existing.At) {
				recorded[index] = reading
				changed = true
			}
		}
		if !replaced {
			recorded = append(recorded, reading)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	sort.Slice(recorded, func(i, j int) bool { return recorded[i].Key() < recorded[j].Key() })
	return s.write(UsageWindowRecord{
		SchemaVersion: UsageWindowSchemaVersion,
		ProductID:     s.productID,
		Readings:      recorded,
	})
}

// List returns the latest reading of every window. No document is the ordinary
// answer on a product no invocation has heard a window on, and is an empty list
// rather than a failure to look. A field this build does not know is stepped
// over rather than refused, because what reads this is the read model, and a
// newer build's record must not blank the reading.
func (s *UsageWindowStore) List() ([]backend.UsageWindowReading, error) {
	return s.read(false)
}

// read is both doors onto the document: strict for a write, which rewrites what
// it read and would drop a field it stepped over, and tolerant for List.
func (s *UsageWindowStore) read(strict bool) ([]backend.UsageWindowReading, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open usage limit record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return nil, fmt.Errorf("read usage limit record: %w", err)
	}
	var record UsageWindowRecord
	if strict {
		err = decodeStrictly(encoded, &record)
	} else {
		_, err = decodeTolerating(encoded, &record)
	}
	if err != nil {
		return nil, fmt.Errorf("decode usage limit record: %w", err)
	}
	if record.SchemaVersion != UsageWindowSchemaVersion {
		return nil, fmt.Errorf("usage limit schema version %d is not supported", record.SchemaVersion)
	}
	if record.ProductID != s.productID {
		return nil, fmt.Errorf("usage limit record belongs to product %q, not %q", record.ProductID, s.productID)
	}
	for _, reading := range record.Readings {
		if err := validateUsageWindowReading(reading); err != nil {
			return nil, fmt.Errorf("usage limit record: %w", err)
		}
	}
	return record.Readings, nil
}

func (s *UsageWindowStore) write(record UsageWindowRecord) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".usage-windows-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary usage limit record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary usage limit record: %w", err)
	}
	if err := writeJSONFile(temporary, "usage limit record", record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary usage limit record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("replace usage limit record: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the writers: every process an invocation runs in writes here,
// and a read-modify-write with nothing between two of them loses one window.
func (s *UsageWindowStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create product state directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open usage limit lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), intakeLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the usage limit record: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
