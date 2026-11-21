// Package intake runs the scheduled queries a project declares and keeps the
// one record of every message they return
// (docs/designs/scheduled-message-intake.md, sections 1, 2, 4 and 9).
//
// A query run reads one time window of its source at a time through the one
// MCP call path, as a recurring task holding the query's one read tool. Each
// message in an answer that fits is written down here before anything else
// happens to it, and only then does the time the query has read up to move.
// A cut answer narrows the window; a minute still too large is recorded as
// skipped. A message from a sender the query does not list is recorded as
// ignored, with its key and sender and nothing of its text.
//
// Nothing here reads a message's text with a model, starts work, or asks
// anybody anything: those are later pieces of the same design. What this
// package settles is that every message a query returns is written down once,
// and that the query keeps moving forward whatever its source does.
package intake

// The intake record (record.go). Everything is one stream of lines, appended
// through the confined writer to a file of this process's own for the day
// under the product's state directory, in intake/. Nothing is ever rewritten
// or removed: what a query has read up to, which messages it has recorded, and
// how its runs ended are all read back by folding the lines in order.
//
// The record stays on this machine. It holds each message's text up to the
// query's text_bytes, and it is the only copy of any message: the MCP call
// record keeps no result for a query's calls (mcpcall.IntakeQueryCaller).

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// RecordSchemaVersion is 1 and has never changed.
const RecordSchemaVersion = 1

// RecordDirectoryName is the directory under the product's state directory the
// record is kept in.
const RecordDirectoryName = "intake"

// maxRecordFileBytes and maxRecordLineBytes bound what is read back.
const (
	maxRecordFileBytes = 256 << 20
	maxRecordLineBytes = 1 << 20
)

// Kind is what one line of the record says.
type Kind string

const (
	// KindSwitchedOn is the moment a query was first run: what it reads starts
	// there, so switching a query on never reads old messages.
	KindSwitchedOn Kind = "switched_on"
	// KindRun is one run of a query: a line when it starts, written before
	// anything is spent, and a line when it ends.
	KindRun Kind = "run"
	// KindMessage is a message recorded for the first time.
	KindMessage Kind = "message"
	// KindReturned is a message already recorded that a query returned again:
	// another query, or the same one with its text changed since.
	KindReturned Kind = "returned"
	// KindWindow is a time window read whole, or skipped: the time the query has
	// read up to moves to its end.
	KindWindow Kind = "window"
	// KindNarrowed is an answer that was cut, halving the next window.
	KindNarrowed Kind = "narrowed"
)

// Phase is which of a run's two lines a run line is.
type Phase string

const (
	PhaseStarted Phase = "started"
	PhaseEnded   Phase = "ended"
)

// RunOutcome is how a run ended.
type RunOutcome string

const (
	// RunSwitchedOn is a query's first run, which reads nothing and marks the
	// moment its reading starts from.
	RunSwitchedOn RunOutcome = "switched_on"
	// RunRead is a run that made calls and settled every answer it had: read a
	// window whole, narrowed one, or skipped a minute.
	RunRead RunOutcome = "read"
	// RunNothingDue is a run that found no time old enough to read yet.
	RunNothingDue RunOutcome = "nothing_due"
	// RunPaused is a run the operator's pause stopped before it spent anything.
	RunPaused RunOutcome = "paused"
	// RunFailed is a run whose call failed or whose answer had the wrong shape;
	// Failure says which.
	RunFailed RunOutcome = "failed"
)

// Failure is why a failed run failed, which decides who is told after three.
type Failure string

const (
	// FailureCall is a call that failed after being tried once more, or that
	// could not be made or recorded.
	FailureCall Failure = "call"
	// FailureShape is an answer that does not hold the messages where the
	// query's configuration says they are.
	FailureShape Failure = "shape"
)

// Ending is how a message's handling ended, where it has.
type Ending string

const (
	// EndingIgnored is a message that will not be read.
	EndingIgnored Ending = "ignored"
)

// ReasonSenderNotListed is why a message from a sender the query does not list
// is ignored.
const ReasonSenderNotListed = "sender not listed"

// Line is one line of the record. Which fields are set depends on its kind.
type Line struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Kind          Kind             `json:"kind"`
	Query         string           `json:"query"`
	At            time.Time        `json:"at"`
	PID           int              `json:"pid"`

	// A run line.
	Run      string     `json:"run,omitempty"`
	Slot     time.Time  `json:"slot,omitzero"`
	Phase    Phase      `json:"phase,omitempty"`
	Outcome  RunOutcome `json:"outcome,omitempty"`
	Failure  Failure    `json:"failure,omitempty"`
	Calls    int        `json:"calls,omitempty"`
	Messages int        `json:"messages,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	// Warned says this run's ending filed the one warning for three failed runs
	// in a row, so no other is filed until a run succeeds.
	Warned bool `json:"warned,omitempty"`

	// A window or narrowed line: the window asked for, and the size the next
	// window starts at. Skipped says the window was one minute still too large
	// to read, and its messages are not read.
	From       time.Time `json:"from,omitzero"`
	Until      time.Time `json:"until,omitzero"`
	NextWindow Seconds   `json:"next_window,omitempty"`
	Skipped    bool      `json:"skipped,omitempty"`

	// A message line holds the message; a returned line holds its key, its
	// number, and the digest of its text where that changed.
	Message *Message `json:"message,omitempty"`
}

// Seconds is a duration kept as whole seconds.
type Seconds int64

// Duration is the seconds as a duration.
func (s Seconds) Duration() time.Duration { return time.Duration(s) * time.Second }

// Message is one message as the record keeps it.
type Message struct {
	// Server and SourceID are the message's key: the MCP server it came from
	// and the source's own identifier for it. The key names no query, so one
	// message two queries return is one record.
	Server   string `json:"server"`
	SourceID string `json:"source_id"`
	// Number is what leaves the record in place of the key: a short digest of
	// it, because a source's values may carry words a sender chose.
	Number string `json:"number"`
	// Query is the query that recorded it first, whose rules handle it.
	Query string `json:"query,omitempty"`
	// Sender, SentAt, Link and Text are the message's parts; Text is kept up to
	// the query's text_bytes, Size and Digest are of the whole of it, and
	// TextCut says only part was kept. A message from a sender not listed keeps
	// no text, no link and no digest.
	Sender  string    `json:"sender,omitempty"`
	SentAt  time.Time `json:"sent_at,omitzero"`
	Link    string    `json:"link,omitempty"`
	Text    string    `json:"text,omitempty"`
	Size    int       `json:"size,omitempty"`
	Digest  string    `json:"digest,omitempty"`
	TextCut bool      `json:"text_cut,omitempty"`
	// Ending and EndingReason say how its handling ended, where it has.
	Ending       Ending `json:"ending,omitempty"`
	EndingReason string `json:"ending_reason,omitempty"`
}

// Key is the message's key as one string.
func (m Message) Key() string { return messageKey(m.Server, m.SourceID) }

func messageKey(server, sourceID string) string { return server + "\x00" + sourceID }

// MessageNumber is the short number made from a message's key.
func MessageNumber(server, sourceID string) string {
	sum := sha256.Sum256([]byte(messageKey(server, sourceID)))
	return hex.EncodeToString(sum[:])[:numberLength]
}

// numberLength is how many hex characters of the key's digest make a number:
// forty bits, so two messages sharing one is not a thing a project meets.
const numberLength = 10

// Recorded is one message as the record reads back: its first line, every
// query that returned it, and the digest it was last returned with.
type Recorded struct {
	Message
	// RecordedAt is when it was first written down.
	RecordedAt time.Time
	// Queries is every query that returned it, the first one first.
	Queries []string
	// LatestDigest is the digest of its text the last time it was returned,
	// which differs from Digest when it was edited after it was recorded. It is
	// noted and nothing more: an edited message is not read again.
	LatestDigest string
}

// QueryState is what one query's lines come to.
type QueryState struct {
	// SwitchedOn is the moment the query was first run; zero before then.
	SwitchedOn time.Time
	// ReadUpTo is the time the query has read every message up to.
	ReadUpTo time.Time
	// NextWindow is the size the next window starts at; zero is the query's
	// own schedule interval.
	NextWindow time.Duration
	// LastSlot is the latest schedule window a run of the query started in.
	LastSlot time.Time
	// FailedInARow is how many runs in a row ended failed, and Warned whether
	// the warning about them has been filed.
	FailedInARow int
	Warned       bool
	// LastFailure and LastFailureKind are what the latest failed run said.
	LastFailure     string
	LastFailureKind Failure
	// Skipped is every minute the query skipped as too large to read.
	Skipped []Window
	// Messages is how many messages the query recorded first, and Ignored how
	// many of those were ignored because their sender is not listed.
	Messages int
	Ignored  int
}

// Window is a stretch of time a query asked for.
type Window struct {
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
}

// State is everything the record holds, folded.
type State struct {
	// Messages is every message, by key.
	Messages map[string]*Recorded
	// Numbers is every message's key, by number.
	Numbers map[string]string
	// Queries is every query the record has a line for, by name.
	Queries map[string]*QueryState
	// Unreadable is how many lines could not be read.
	Unreadable int
}

// Query is a query's state, empty where the record holds nothing of it.
func (s State) Query(name string) QueryState {
	if query, ok := s.Queries[name]; ok {
		return *query
	}
	return QueryState{}
}

// ByNumber is the message with a number.
func (s State) ByNumber(number string) (Recorded, bool) {
	key, ok := s.Numbers[number]
	if !ok {
		return Recorded{}, false
	}
	return *s.Messages[key], true
}

// apply folds one line into the state.
func (s *State) apply(line Line) {
	query := s.Queries[line.Query]
	if query == nil {
		query = &QueryState{}
		s.Queries[line.Query] = query
	}
	switch line.Kind {
	case KindSwitchedOn:
		if query.SwitchedOn.IsZero() {
			query.SwitchedOn = line.At
			if query.ReadUpTo.IsZero() {
				query.ReadUpTo = line.At
			}
		}
	case KindRun:
		switch line.Phase {
		case PhaseStarted:
			if line.Slot.After(query.LastSlot) {
				query.LastSlot = line.Slot
			}
		case PhaseEnded:
			switch line.Outcome {
			case RunFailed:
				query.FailedInARow++
				query.LastFailure, query.LastFailureKind = line.Reason, line.Failure
				query.Warned = query.Warned || line.Warned
			case RunPaused:
				// A pause says nothing about the query or its source.
			default:
				query.FailedInARow, query.Warned = 0, false
				query.LastFailure, query.LastFailureKind = "", ""
			}
		}
	case KindWindow:
		if line.Until.After(query.ReadUpTo) {
			query.ReadUpTo = line.Until
		}
		query.NextWindow = line.NextWindow.Duration()
		if line.Skipped {
			query.Skipped = append(query.Skipped, Window{From: line.From, Until: line.Until})
		}
	case KindNarrowed:
		query.NextWindow = line.NextWindow.Duration()
	case KindMessage:
		if line.Message == nil {
			return
		}
		key := line.Message.Key()
		if existing, ok := s.Messages[key]; ok {
			// Two processes recorded one message; the first line stands, and the
			// second reads as the query returning it again.
			existing.addQuery(line.Query)
			return
		}
		s.Messages[key] = &Recorded{Message: *line.Message, RecordedAt: line.At, Queries: []string{line.Query}, LatestDigest: line.Message.Digest}
		s.Numbers[line.Message.Number] = key
		query.Messages++
		if line.Message.Ending == EndingIgnored && line.Message.EndingReason == ReasonSenderNotListed {
			query.Ignored++
		}
	case KindReturned:
		if line.Message == nil {
			return
		}
		existing, ok := s.Messages[line.Message.Key()]
		if !ok {
			return
		}
		existing.addQuery(line.Query)
		if line.Message.Digest != "" {
			existing.LatestDigest = line.Message.Digest
		}
	}
}

func (r *Recorded) addQuery(query string) {
	for _, known := range r.Queries {
		if known == query {
			return
		}
	}
	r.Queries = append(r.Queries, query)
}

// Records is where one product's intake record is kept and read back.
type Records struct {
	stateRoot string
	productID domain.ProductID
	pid       int
	now       func() time.Time

	mu sync.Mutex
	// beforeAppend, when set, is asked before each line is written, and a line
	// it refuses is not written; a test stops a run part-way with it.
	beforeAppend func(Line) error
}

// NewRecords keeps the record for one product under a state root, which must
// be absolute.
func NewRecords(stateRoot string, productID domain.ProductID) (*Records, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(stateRoot) {
		return nil, errors.New("the state root the intake record is kept under must be an absolute path")
	}
	return &Records{stateRoot: filepath.Clean(stateRoot), productID: productID, pid: os.Getpid(), now: time.Now}, nil
}

// Directory is where the record is kept, so a failure can say where.
func (r *Records) Directory() string {
	return filepath.Join(home.ProductDirectory(r.stateRoot, string(r.productID)), RecordDirectoryName)
}

// lockKey names this record for the in-process lock on each query's runs.
func (r *Records) lockKey(query string) string { return r.Directory() + "\x00" + query }

func (r *Records) append(line Line) error {
	line.SchemaVersion = RecordSchemaVersion
	line.ProductID = r.productID
	line.PID = r.pid
	if line.At.IsZero() {
		line.At = r.now().UTC()
	}
	if r.beforeAppend != nil {
		if err := r.beforeAppend(line); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		return err
	}
	root, err := repowrite.NewRoot(r.stateRoot)
	if err != nil {
		return fmt.Errorf("the state directory for the intake record: %w", err)
	}
	name := line.At.UTC().Format("20060102") + "-" + strconv.Itoa(r.pid) + ".jsonl"
	relative := path.Join(home.ProductDirectoryWithin(r.stateRoot, string(r.productID)), RecordDirectoryName, name)
	r.mu.Lock()
	defer r.mu.Unlock()
	file, err := root.OpenAppend(relative, 0o600, 0o700)
	if err != nil {
		return fmt.Errorf("open the intake record: %w", err)
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return fmt.Errorf("write the intake record: %w", err)
	}
	return nil
}

var recordFile = regexp.MustCompile(`^\d{8}-\d+\.jsonl$`)

// Read folds every line of the record, in the order the lines were written.
func (r *Records) Read() (State, error) {
	state := State{Messages: map[string]*Recorded{}, Numbers: map[string]string{}, Queries: map[string]*QueryState{}}
	directory := r.Directory()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read the intake record in %s: %w", directory, err)
	}
	type ordered struct {
		line  Line
		order int
	}
	var lines []ordered
	for _, entry := range entries {
		if !recordFile.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		data, err := readBounded(filepath.Join(directory, entry.Name()))
		if err != nil {
			return State{}, fmt.Errorf("read the intake record %s: %w", entry.Name(), err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64<<10), maxRecordLineBytes)
		for scanner.Scan() {
			text := bytes.TrimSpace(scanner.Bytes())
			if len(text) == 0 {
				continue
			}
			var line Line
			if err := json.Unmarshal(text, &line); err != nil || line.SchemaVersion != RecordSchemaVersion || line.ProductID != r.productID || line.Query == "" {
				// A line a killed process left half written, or one written by a
				// schema this build does not know.
				state.Unreadable++
				continue
			}
			lines = append(lines, ordered{line: line, order: len(lines)})
		}
		if scanner.Err() != nil {
			state.Unreadable++
		}
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if !lines[i].line.At.Equal(lines[j].line.At) {
			return lines[i].line.At.Before(lines[j].line.At)
		}
		return lines[i].order < lines[j].order
	})
	for _, line := range lines {
		state.apply(line.line)
	}
	return state, nil
}

func readBounded(file string) ([]byte, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return io.ReadAll(io.LimitReader(handle, maxRecordFileBytes))
}
