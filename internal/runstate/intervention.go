package runstate

// The operator's hand steps, one event each (see internal/intervention).
//
// It is one append-only log per product, beside the stall logs, because an
// event is written once and never revised: it says a step was taken, which
// stays true whatever became of the step. Appends are made under a lock so two
// processes recording at once write two whole lines rather than one torn one.

import (
	"bufio"
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
	"github.com/mason-bryant/yoyodyne/internal/intervention"
)

// maxEncodedInterventionBytes bounds one encoded event, including the trailing
// newline: what was done at its limit, a directive's worth of items, and every
// one-line field at its own.
const maxEncodedInterventionBytes = 32 << 10

// interventionLockWait bounds the wait for the log's lock. An append takes
// milliseconds, so a wait this long is a holder that is stuck.
const interventionLockWait = 10 * time.Second

// InterventionStore is one product's log of the operator's hand steps.
type InterventionStore struct {
	// state is the state root every write this store makes is confined to.
	state     stateRoot
	root      string
	productID domain.ProductID
}

func NewInterventionStore(root string, productID domain.ProductID) (*InterventionStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	state, err := newStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &InterventionStore{
		state:     state,
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

// Path names the log.
func (s *InterventionStore) Path() string { return filepath.Join(s.root, "interventions.jsonl") }

// Record appends one event. It refuses an event for another product and an
// event that does not validate, and writes nothing in either case.
func (s *InterventionStore) Record(event intervention.Event) error {
	if err := s.validate(event); err != nil {
		return err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return fmt.Errorf("encode intervention: %w", err)
	}
	encoded := buffer.Bytes()
	if len(encoded) > maxEncodedInterventionBytes {
		return fmt.Errorf("encoded intervention is %d bytes, limit is %d", len(encoded), maxEncodedInterventionBytes)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	path := s.Path()
	// Appended in checked batches through the confined writer, which syncs the
	// directory when it creates the log.
	if err := s.state.appendTo(path, encoded); err != nil {
		return fmt.Errorf("append intervention: %w", err)
	}
	return nil
}

// List is every recorded event, in the order the steps were taken. A log that
// does not exist is a product where nobody has recorded a hand step, which is an
// answer rather than a failure to look.
//
// It reads tolerantly, because nothing read here is written back: an event a
// newer build recorded is read without the fields this build does not know.
func (s *InterventionStore) List() ([]intervention.Event, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open intervention log: %w", err)
	}
	defer file.Close()
	var events []intervention.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedInterventionBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var decoded intervention.Event
		unknown, err := decodeTolerating([]byte(line), &decoded)
		if err != nil {
			return nil, fmt.Errorf("decode intervention log: %w", err)
		}
		noteUnknownFields("intervention record", unknown)
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode intervention log: %w", err)
		}
		events = append(events, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read intervention log: %w", err)
	}
	intervention.Sort(events)
	return events, nil
}

func (s *InterventionStore) lock() (func(), error) {
	file, err := s.state.openLock(s.Path() + ".lock")
	if err != nil {
		return nil, fmt.Errorf("open intervention log lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), interventionLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the intervention log: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

func (s *InterventionStore) validate(event intervention.Event) error {
	if event.ProductID != s.productID {
		return fmt.Errorf("intervention product %q does not match store product %q", event.ProductID, s.productID)
	}
	return event.Validate()
}
