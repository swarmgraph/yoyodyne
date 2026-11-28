package epicwake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	fileName     = "epic-wakes.json"
	lockName     = fileName + ".lock"
	maxFileBytes = 4 << 20
	lockWait     = 5 * time.Second
)

// Store writes the file, under its own lock, whole. Only the harness opens one:
// no operator's command and no role's block reaches it.
type Store struct {
	stateRoot string
	product   domain.ProductID
	now       func() time.Time
}

// Open opens the file for the harness's writes.
func Open(stateRoot string, product domain.ProductID, now func() time.Time) (*Store, error) {
	if !filepath.IsAbs(stateRoot) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(product)); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Store{stateRoot: filepath.Clean(stateRoot), product: product, now: now}, nil
}

// Owed takes in the events owed now, and answers each role whose pass is due.
// Nothing is written where nothing changed.
func (s *Store) Owed(owed []Event) (map[domain.AgentRole]Due, error) {
	var due map[domain.AgentRole]Due
	err := s.change(func(record *Record, now time.Time) (bool, error) {
		changed := record.settle(owed, now)
		due = record.Due(now)
		return changed, nil
	})
	return due, err
}

// Woken records, before its first turn, that a pass of role was started for the
// events, so that it counts toward moving them up and is charged to their epics.
func (s *Store) Woken(role domain.AgentRole, pass string, events []Kept) error {
	keys := make([]string, 0, len(events))
	for _, event := range events {
		keys = append(keys, event.Key)
	}
	return s.change(func(record *Record, now time.Time) (bool, error) {
		return true, record.woken(role, pass, keys, now)
	})
}

// Ended records what a pass cost once its turns are over.
func (s *Store) Ended(pass string, costUSD float64, known bool) error {
	return s.change(func(record *Record, now time.Time) (bool, error) {
		return true, record.ended(pass, Ended{At: now, CostUSD: costUSD, Known: known})
	})
}

// Read reads the file whole, and answers an empty record where there is none.
func Read(stateRoot string, product domain.ProductID) (Record, error) {
	empty := Record{SchemaVersion: SchemaVersion, ProductID: product}
	path := filepath.Join(home.ProductDirectory(stateRoot, string(product)), fileName)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return Record{}, fmt.Errorf("look for the record of epic events: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return Record{}, fmt.Errorf("the record of epic events, %s, is not a regular file of at most %d bytes", path, maxFileBytes)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("read the record of epic events: %w", err)
	}
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&record)
	if _, after := decoder.Token(); err == nil && !errors.Is(after, io.EOF) {
		err = errors.New("something follows the record")
	}
	if err == nil {
		err = record.validate(product)
	}
	if err != nil {
		return Record{}, fmt.Errorf("the record of epic events, %s, cannot be read whole: %w", path, err)
	}
	return record, nil
}

// change applies one change under the lock and writes the file whole where the
// change says it changed something.
func (s *Store) change(apply func(*Record, time.Time) (bool, error)) error {
	root, err := runstate.PinStateRoot(s.stateRoot)
	if err != nil {
		return fmt.Errorf("pin the state root: %w", err)
	}
	defer root.Close()
	directory := filepath.FromSlash(home.ProductDirectoryWithin(root.Path(), string(s.product)))
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the product's state directory: %w", err)
	}
	product, err := root.OpenDirectory(directory)
	if err != nil {
		return fmt.Errorf("open the product's state directory: %w", err)
	}
	defer product.Close()
	lock, err := product.OpenLock(lockName, 0o600)
	if err != nil {
		return fmt.Errorf("open the lock on the record of epic events: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	release, err := runstate.LockFile(ctx, lock)
	if err != nil {
		return fmt.Errorf("lock the record of epic events: %w", err)
	}
	defer release()
	record, err := Read(root.Path(), s.product)
	if err != nil {
		return err
	}
	changed, err := apply(&record, s.now().UTC())
	if err != nil || !changed {
		return err
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("encode the record of epic events: %w", err)
	}
	if encoded.Len() > maxFileBytes {
		return fmt.Errorf("refused the record of epic events: it is larger than %d bytes", maxFileBytes)
	}
	if err := record.validate(s.product); err != nil {
		return fmt.Errorf("refused the record of epic events: %w", err)
	}
	// Both sides of the change, as repository-writes-are-physically-confined asks.
	if err := product.Unchanged(); err != nil {
		return err
	}
	if err := product.WriteFile(fileName, encoded.Bytes(), 0o600, false); err != nil {
		return fmt.Errorf("write the record of epic events: %w", err)
	}
	return errors.Join(product.Unchanged(), product.Sync())
}
