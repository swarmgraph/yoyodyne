package runstate

// The factory having stopped, written down so it is said once.
//
// A factory stall is the whole product pulling no work and completing no
// recurring pass for longer than its limit (see readmodel.FactoryStallOf). The
// supervisor reads it every minute, and what it says about it — a critical
// report when it begins and a note when it clears — has to be said once per
// stall rather than once per reading. That is a property of the record rather
// than of the process, so it is kept here: one append-only log per product,
// each stall opened once and closed once, and the read and the append made
// under one lock so two readers cannot each open the same stall.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// FactoryStallSchemaVersion is 1 and has never changed.
const FactoryStallSchemaVersion = 1

var factoryStallIDPattern = regexp.MustCompile(`^factory-stall-[a-f0-9]{32}$`)

// FactoryStallEvent is one stretch of the factory doing nothing. The open and
// the close are the same record written twice, the close carrying everything
// the open did plus the ending, so a reader takes the latest entry per event.
type FactoryStallEvent struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	EventID       string           `json:"event_id"`
	// Since is when the factory last pulled work or completed a pass, and
	// OpenedAt when the supervisor noticed it had done neither for too long.
	Since    time.Time `json:"since"`
	OpenedAt time.Time `json:"opened_at"`
	// Says is the stall as it was noticed: how long, the last success, and what
	// each pass failed on.
	Says string `json:"says"`
	// ClosedAt is when a pull or a successful pass ended it, absent while it
	// stands, and Cleared what the reading that closed it saw.
	ClosedAt *time.Time `json:"closed_at,omitempty"`
	Cleared  string     `json:"cleared,omitempty"`
}

// Open reports a stall still standing.
func (e FactoryStallEvent) Open() bool { return e.ClosedAt == nil }

func (e FactoryStallEvent) Validate() error {
	var problems []error
	if e.SchemaVersion != FactoryStallSchemaVersion {
		problems = append(problems, fmt.Errorf("factory stall schema version %d is not supported", e.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !factoryStallIDPattern.MatchString(e.EventID) {
		problems = append(problems, errors.New("event_id is invalid"))
	}
	if e.Since.IsZero() || e.OpenedAt.IsZero() {
		problems = append(problems, errors.New("since and opened_at are required"))
	}
	if len(e.Says) > MaxStallDetailBytes || len(e.Cleared) > MaxStallDetailBytes {
		problems = append(problems, fmt.Errorf("a factory stall's sentences are bounded to %d bytes", MaxStallDetailBytes))
	}
	if e.ClosedAt != nil && e.ClosedAt.IsZero() {
		problems = append(problems, errors.New("closed_at is present and unset"))
	}
	if e.ClosedAt == nil && strings.TrimSpace(e.Cleared) != "" {
		problems = append(problems, errors.New("what cleared a factory stall requires the close that recorded it"))
	}
	return errors.Join(problems...)
}

// FactoryStallObservation is one reading, in the record's terms.
type FactoryStallObservation struct {
	Stalled bool
	// Since and Says are read where a stall is being opened, Cleared where one is
	// being closed.
	Since   time.Time
	Says    string
	Cleared string
	At      time.Time
}

// FactoryStallReconciliation is what one reading did to the record: the stall
// it opened, the stall it closed, and the stall standing after it.
type FactoryStallReconciliation struct {
	Opened   *FactoryStallEvent
	Closed   *FactoryStallEvent
	Standing *FactoryStallEvent
}

// FactoryStallStore is one product's log of factory stalls, beside its other
// stall log.
type FactoryStallStore struct {
	root      string
	productID domain.ProductID
}

func NewFactoryStallStore(root string, productID domain.ProductID) (*FactoryStallStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &FactoryStallStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

// Path names the log.
func (s *FactoryStallStore) Path() string { return filepath.Join(s.root, "factory-stalls.jsonl") }

// Reconcile records what one reading came to, and is the whole of the dedup: a
// stall standing while later readings agree with it writes nothing.
func (s *FactoryStallStore) Reconcile(observation FactoryStallObservation) (FactoryStallReconciliation, error) {
	unlock, err := s.lock()
	if err != nil {
		return FactoryStallReconciliation{}, err
	}
	defer unlock()
	standing, open, err := s.Standing()
	if err != nil {
		return FactoryStallReconciliation{}, err
	}
	switch {
	case observation.Stalled && open:
		return FactoryStallReconciliation{Standing: &standing}, nil
	case observation.Stalled:
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return FactoryStallReconciliation{}, fmt.Errorf("generate factory stall id: %w", err)
		}
		opened := FactoryStallEvent{
			SchemaVersion: FactoryStallSchemaVersion,
			ProductID:     s.productID,
			EventID:       "factory-stall-" + hex.EncodeToString(raw),
			Since:         observation.Since,
			OpenedAt:      observation.At,
			Says:          boundedStallDetail(observation.Says, MaxStallDetailBytes),
		}
		if err := s.record(opened); err != nil {
			return FactoryStallReconciliation{}, err
		}
		return FactoryStallReconciliation{Opened: &opened, Standing: &opened}, nil
	case open:
		closedAt := observation.At
		closed := standing
		closed.ClosedAt = &closedAt
		closed.Cleared = boundedStallDetail(observation.Cleared, MaxStallDetailBytes)
		if err := s.record(closed); err != nil {
			return FactoryStallReconciliation{}, err
		}
		return FactoryStallReconciliation{Closed: &closed}, nil
	default:
		return FactoryStallReconciliation{}, nil
	}
}

// List is every recorded factory stall, folded to one entry each, in the order
// each was opened. A log that does not exist is a product that has never
// stalled.
func (s *FactoryStallStore) List() ([]FactoryStallEvent, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open factory stall log: %w", err)
	}
	defer file.Close()
	folded := map[string]FactoryStallEvent{}
	var order []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedStallBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var decoded FactoryStallEvent
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			return nil, fmt.Errorf("decode factory stall log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode factory stall log: %w", err)
		}
		if _, seen := folded[decoded.EventID]; !seen {
			order = append(order, decoded.EventID)
		}
		folded[decoded.EventID] = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read factory stall log: %w", err)
	}
	events := make([]FactoryStallEvent, 0, len(order))
	for _, eventID := range order {
		events = append(events, folded[eventID])
	}
	return events, nil
}

// Standing is the factory stall that is open, if one is; the newest where a
// hand-written log holds two.
func (s *FactoryStallStore) Standing() (FactoryStallEvent, bool, error) {
	events, err := s.List()
	if err != nil {
		return FactoryStallEvent{}, false, err
	}
	var standing FactoryStallEvent
	found := false
	for _, event := range events {
		if event.Open() {
			standing, found = event, true
		}
	}
	return standing, found, nil
}

func (s *FactoryStallStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create factory stall directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open factory stall log lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), stallLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the factory stall log: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// record appends one entry, as the other stall log does.
func (s *FactoryStallStore) record(event FactoryStallEvent) error {
	if err := s.validate(event); err != nil {
		return err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return fmt.Errorf("encode factory stall: %w", err)
	}
	encoded := buffer.Bytes()
	if len(encoded) > maxEncodedStallBytes {
		return fmt.Errorf("encoded factory stall is %d bytes, limit is %d", len(encoded), maxEncodedStallBytes)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect factory stall log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open factory stall log: %w", err)
	}
	written, err := file.Write(encoded)
	if err == nil && written != len(encoded) {
		err = io.ErrShortWrite
	}
	if err != nil {
		file.Close()
		return fmt.Errorf("append factory stall: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync factory stall log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close factory stall log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

func (s *FactoryStallStore) validate(event FactoryStallEvent) error {
	if event.ProductID != s.productID {
		return fmt.Errorf("factory stall product %q does not match store product %q", event.ProductID, s.productID)
	}
	return event.Validate()
}
