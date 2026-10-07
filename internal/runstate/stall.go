package runstate

// The harness having gone quiet, written down so that it survives the process
// that noticed it.
//
// Every other record here is written by the thing it is about: a run records its
// own phases, a session records its own transitions, a hold records the moment
// somebody placed it. A stall has nobody to write it — it is exactly the state in
// which the process that would have said something is dead or wedged — so it is
// recorded by whatever notices, and it has to be a record rather than a message
// for two reasons. It is the dedup: one open stall at a time means a checker that
// runs every fifteen seconds says something once and not four times a minute.
// And it is the history: the seven and a half hours nobody saw on 2026-09-01
// left nothing behind at all, so there was no answer afterwards to "how long was
// it dead, and what was the last thing it said".
//
// It is one append-only log per product, folded by event. A stall is opened once
// and closed once, and the close is appended rather than written over the open,
// because two processes may be reading this while a third writes it and a
// rewrite is where that stops being safe.

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
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// StallSchemaVersion is 1 and has never changed.
const StallSchemaVersion = 1

// MaxStallDetailBytes bounds each of the two sentences a stall record keeps:
// what the thing that chooses work last said, and what eventually cleared the
// stall. Both are the harness's own words about itself rather than a provider's,
// so they are held to the bound a watch transition's reason takes.
const MaxStallDetailBytes = 4 << 10

// maxEncodedStallBytes bounds one encoded record, including the trailing
// newline. The writer and the reader share it, so a record that was written is
// always one that can be read back.
const maxEncodedStallBytes = 16 << 10

var stallEventIDPattern = regexp.MustCompile(`^stall-[a-f0-9]{32}$`)

// StallEvent is one stretch of the harness having started nothing while the
// tracker reported work ready to start.
//
// The open and the close are the same record written twice, which is what makes
// the log foldable: the second entry carries everything the first did plus the
// ending. A reader takes the latest entry per event and has the whole of it.
type StallEvent struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// EventID names one stall, so a second stall a week later is a second thing to
	// say rather than the same one said again.
	EventID string `json:"event_id"`
	// OpenedAt is when a checker first noticed, which is later than Since by at
	// most the threshold and by however long the checker was itself away.
	OpenedAt time.Time `json:"opened_at"`
	// Since is when anything last held a developer slot, by starting or ending a
	// run, which is what the age of a stall is measured from. It is kept apart from OpenedAt because they answer
	// different questions: one is how long nothing happened, and the other is how
	// long it took anybody to notice.
	Since time.Time `json:"since"`
	// Ready is how much admitted work the tracker called ready when the stall was
	// noticed. It is the fact that makes the stall a stall rather than a rest.
	Ready int `json:"ready,omitempty"`
	// Chooser is what the record last said about the thing that chooses work. It
	// is the one field that tells a dead scheduler from a wedged one, which is the
	// first thing whoever reads this has to decide between.
	Chooser string `json:"chooser,omitempty"`
	// ClosedAt is when the stall stopped being true, absent while it stands.
	ClosedAt *time.Time `json:"closed_at,omitempty"`
	// Cleared is what accounted for it by the time it closed — a run starting, a
	// hold going on, the queue draining. A stall that simply stopped being
	// reported would leave a reader deciding for themselves whether it was fixed
	// or merely stopped being looked for.
	Cleared string `json:"cleared,omitempty"`
}

// Open reports a stall that is still standing.
func (e StallEvent) Open() bool { return e.ClosedAt == nil }

// For is how long the stall had been true when it was noticed, or how long it
// ran in all once it closed.
func (e StallEvent) For() time.Duration {
	if e.Since.IsZero() {
		return 0
	}
	if e.ClosedAt != nil {
		return e.ClosedAt.Sub(e.Since)
	}
	return e.OpenedAt.Sub(e.Since)
}

func (e StallEvent) Validate() error {
	var problems []error
	if e.SchemaVersion != StallSchemaVersion {
		problems = append(problems, fmt.Errorf("stall event schema version %d is not supported", e.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !stallEventIDPattern.MatchString(e.EventID) {
		problems = append(problems, errors.New("event_id is invalid"))
	}
	if e.OpenedAt.IsZero() {
		problems = append(problems, errors.New("opened_at is required"))
	}
	// A stall with no moment to measure from is a stall nobody can say the length
	// of, and the length is the whole of what makes it worth reporting.
	if e.Since.IsZero() {
		problems = append(problems, errors.New("since is required"))
	}
	if e.Ready < 0 {
		problems = append(problems, fmt.Errorf("stall event reports %d ready item(s), which is not a count", e.Ready))
	}
	if len(e.Chooser) > MaxStallDetailBytes {
		problems = append(problems, fmt.Errorf("chooser is %d bytes, which exceeds the %d byte bound", len(e.Chooser), MaxStallDetailBytes))
	}
	if len(e.Cleared) > MaxStallDetailBytes {
		problems = append(problems, fmt.Errorf("cleared is %d bytes, which exceeds the %d byte bound", len(e.Cleared), MaxStallDetailBytes))
	}
	// An ending recorded without the moment it happened is a stall that reads as
	// standing and says what stopped it, which is two answers to one question.
	if e.ClosedAt != nil && e.ClosedAt.IsZero() {
		problems = append(problems, errors.New("closed_at is present and unset"))
	}
	if e.ClosedAt == nil && strings.TrimSpace(e.Cleared) != "" {
		problems = append(problems, errors.New("what cleared a stall requires the close that recorded it"))
	}
	return errors.Join(problems...)
}

// NewStallEventID names one stall.
func NewStallEventID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate stall event id: %w", err)
	}
	return "stall-" + hex.EncodeToString(raw), nil
}

// StallObservation is one check of whether the harness has gone quiet, in the
// terms the record is kept in. It is what a checker hands the store; the store
// decides what, if anything, that changes.
type StallObservation struct {
	// Stalled is the reading itself.
	Stalled bool
	// Since is when anything last held a developer slot, and Ready is how much work
	// was waiting. Both are only read where a stall is being opened.
	Since time.Time
	Ready int
	// Chooser is what the record last said about the thing that chooses work,
	// read where a stall is being opened.
	Chooser string
	// Explains is what accounts for nothing having started, read where a stall is
	// being closed. It is what the close records as having cleared it.
	Explains string
	// At is when the check was made.
	At time.Time
}

// StallReconciliation is what one check came to: the stall it opened, the stall
// it closed, and the stall that stands after it.
//
// All three are reported because a caller does different things with each. What
// was opened is what a surface says out loud once; what stands is what a surface
// checks its own memory against; and what was closed is neither, because the
// thing that cleared a stall has already said so itself.
type StallReconciliation struct {
	Opened *StallEvent
	Closed *StallEvent
	// Standing is the stall that is open after this check, which is the one just
	// opened where one was, and an already-open one where the check changed
	// nothing. It is nil where nothing is standing.
	Standing *StallEvent
}

// StallStore is where the stalls of one product are collected, in the same
// operating-system state root as the runs and the watch log and beside them.
type StallStore struct {
	root      string
	productID domain.ProductID
}

func NewStallStore(root string, productID domain.ProductID) (*StallStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &StallStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *StallStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the account of the
// stalls actually is.
func (s *StallStore) Path() string { return filepath.Join(s.root, "stalls.jsonl") }

// Reconcile records what one check of the harness's silence came to.
//
// It is the whole of the dedup, and it is here rather than in whatever is doing
// the checking for exactly that reason: a checker runs on a poll loop, and
// "notice once" is a property of the record rather than of any one process's
// memory of what it has already seen. A stall standing while a second, third and
// four-thousandth check agree with it changes nothing at all.
//
// Two processes take this reading: the watching session and `yoyo reconcile`.
// The read and the append are therefore made under one lock across processes.
// Without it, two readings made at the same moment over no open stall would
// each find nothing open, and each would open a stall of its own.
func (s *StallStore) Reconcile(observation StallObservation) (StallReconciliation, error) {
	unlock, err := s.lock()
	if err != nil {
		return StallReconciliation{}, err
	}
	defer unlock()
	standing, open, err := s.Standing()
	if err != nil {
		return StallReconciliation{}, err
	}
	switch {
	case observation.Stalled && open:
		// The same stall, still true. This is the ordinary case by a very long way,
		// and it writes nothing.
		return StallReconciliation{Standing: &standing}, nil
	case observation.Stalled:
		eventID, err := NewStallEventID()
		if err != nil {
			return StallReconciliation{}, err
		}
		opened := StallEvent{
			SchemaVersion: StallSchemaVersion,
			ProductID:     s.productID,
			EventID:       eventID,
			OpenedAt:      observation.At,
			Since:         observation.Since,
			Ready:         observation.Ready,
			Chooser:       boundedStallDetail(observation.Chooser, MaxStallDetailBytes),
		}
		if err := s.Record(opened); err != nil {
			return StallReconciliation{}, err
		}
		return StallReconciliation{Opened: &opened, Standing: &opened}, nil
	case open:
		closedAt := observation.At
		closed := standing
		closed.ClosedAt = &closedAt
		closed.Cleared = boundedStallDetail(observation.Explains, MaxStallDetailBytes)
		if err := s.Record(closed); err != nil {
			return StallReconciliation{}, err
		}
		return StallReconciliation{Closed: &closed}, nil
	default:
		return StallReconciliation{}, nil
	}
}

// lock serializes Reconcile across processes, as the intake hold's lock does
// for its own read-modify-write.
func (s *StallStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create stall directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open stall log lock: %w", err)
	}
	// Bounded, because what this waits on is another process's one small append.
	// A wait longer than that is a lock somebody died holding, and a check that
	// cannot record should fail and be asked again rather than hang a poll loop.
	ctx, cancel := context.WithTimeout(context.Background(), stallLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the stall log: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// stallLockWait bounds the wait for the stall log's lock.
const stallLockWait = 5 * time.Second

// Record appends one entry. Opening a stall and closing it are both appends, so
// two readers of this log never see a half-written record and a crash between
// them leaves a stall that is open rather than one that is neither.
func (s *StallStore) Record(event StallEvent) error {
	if err := s.validate(event); err != nil {
		return err
	}
	encoded, err := encodeStallEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStallBytes {
		return fmt.Errorf("encoded stall event is %d bytes, limit is %d", len(encoded), maxEncodedStallBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create stall directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect stall log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stall log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append stall event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append stall event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync stall log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close stall log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded stall, folded to one entry each and oldest first.
// A log that does not exist yet is a product that has never gone quiet, which is
// not a failure to read.
func (s *StallStore) List() ([]StallEvent, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open stall log: %w", err)
	}
	defer file.Close()

	folded := map[string]StallEvent{}
	var order []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedStallBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeStallEvent([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode stall log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode stall log: %w", err)
		}
		if _, seen := folded[decoded.EventID]; !seen {
			order = append(order, decoded.EventID)
		}
		// The last entry for an event is the whole of it: a close carries
		// everything its open did, plus the ending.
		folded[decoded.EventID] = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stall log: %w", err)
	}
	events := make([]StallEvent, 0, len(order))
	for _, eventID := range order {
		events = append(events, folded[eventID])
	}
	sort.SliceStable(events, func(first, second int) bool {
		if !events[first].OpenedAt.Equal(events[second].OpenedAt) {
			return events[first].OpenedAt.Before(events[second].OpenedAt)
		}
		return events[first].EventID < events[second].EventID
	})
	return events, nil
}

// Standing is the stall that is open, if one is. Two open at once is a record
// something other than Reconcile wrote, and the newest is taken rather than the
// question being refused: a surface that stopped reporting stalls over a
// malformed log would be silence produced by the thing that exists to end it.
func (s *StallStore) Standing() (StallEvent, bool, error) {
	events, err := s.List()
	if err != nil {
		return StallEvent{}, false, err
	}
	var standing StallEvent
	found := false
	for _, event := range events {
		if event.Open() {
			standing, found = event, true
		}
	}
	return standing, found, nil
}

func decodeStallEvent(data []byte) (StallEvent, error) {
	var decoded StallEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		return StallEvent{}, err
	}
	if err := decoded.Validate(); err != nil {
		return StallEvent{}, err
	}
	return decoded, nil
}

func encodeStallEvent(event StallEvent) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return nil, fmt.Errorf("encode stall event: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *StallStore) validate(event StallEvent) error {
	if event.ProductID != s.productID {
		return fmt.Errorf("stall event product %q does not match store product %q", event.ProductID, s.productID)
	}
	return event.Validate()
}

// boundedStallDetail folds one of the harness's own sentences to a line and cuts
// it to the bound, so a record that was assembled is always one that can be
// written. It cuts on a rune boundary: a sentence truncated mid-rune is not
// text.
func boundedStallDetail(text string, limit int) string {
	return oneline.Bound(text, limit)
}
