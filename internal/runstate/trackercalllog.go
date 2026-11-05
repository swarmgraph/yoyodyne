package runstate

// Every tracker call the harness's own processes make, kept for two days.
//
// bd lets one process into the store at a time, so which of the harness's
// processes keeps it busiest is answered by counting every call all of them
// made (internal/beads, calllog.go). Each process appends its calls to a file
// of its own for each hour, named by the hour and the process number, so no
// two processes ever write one file and nothing here needs a lock between
// them. A process that starts a new hour's file removes the files older than
// trackerCallsKept. A line a process was still writing when it was read, or
// one a killed process left half written, is counted as unreadable rather
// than failing the read.

import (
	"bufio"
	"bytes"
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
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// TrackerCallSchemaVersion is 1 and has never changed.
const TrackerCallSchemaVersion = 1

const (
	// trackerCallsKept is how long a process's calls are kept.
	trackerCallsKept = 48 * time.Hour
	// trackerCallHourLayout is the hour a file's name begins with.
	trackerCallHourLayout = "20060102T15"
	// maxCallersSaid is how many callers a count names in words.
	maxCallersSaid = 8
	// maxTrackerCallFileBytes bounds what of one process's hour is read, far
	// above the few thousand calls a busy process makes in one.
	maxTrackerCallFileBytes = 64 << 20
)

// TrackerCallRecord is one call as the log keeps it: the call, and the process
// that made it.
type TrackerCallRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Process is what the process was started as, such as "yoyo watch", and PID
	// its process number.
	Process string `json:"process"`
	PID     int    `json:"pid"`
	beads.LoggedCall
}

// Caller is who the call is counted against: who it was for, where it named
// somebody, and the process that made it otherwise (CallerGroup).
func (r TrackerCallRecord) Caller() string { return CallerGroup(r.For, r.Process) }

// TrackerCallLogStore is where one process's tracker calls are kept, in the
// product's directory of them, and where every process's are read back.
type TrackerCallLogStore struct {
	root, anchor string
	productID    domain.ProductID
	process      string
	pid          int

	mu sync.Mutex
	// hour is the hour this process last wrote a file for, so the old files are
	// looked for once an hour rather than at every call.
	hour string
	// failed is the last write that failed, said once by whoever asks.
	failed error
}

func NewTrackerCallLogStore(root string, productID domain.ProductID, process string) (*TrackerCallLogStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &TrackerCallLogStore{
		root:   filepath.Join(home.ProductDirectory(root, string(productID)), "tracker-calls"),
		anchor: anchor, productID: productID,
		process: oneline.Fold(strings.TrimSpace(process), maxTrackerSlowCallTextBytes),
		pid:     os.Getpid(),
	}, nil
}

// Path names the directory, so a failure can say where it is.
func (s *TrackerCallLogStore) Path() string { return s.root }

// LogCall appends one call to this process's file for the hour it started in.
// A write that fails is kept for Failed and changes nothing about the call.
func (s *TrackerCallLogStore) LogCall(call beads.LoggedCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.append(call); err != nil {
		s.failed = err
	}
}

// Failed is the last write that failed, where one has.
func (s *TrackerCallLogStore) Failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func (s *TrackerCallLogStore) append(call beads.LoggedCall) error {
	if call.At.IsZero() {
		return errors.New("a logged tracker call's start time is required")
	}
	call.At = call.At.UTC()
	call.For = oneline.Fold(call.For, maxTrackerSlowCallTextBytes)
	encoded, err := json.Marshal(TrackerCallRecord{SchemaVersion: TrackerCallSchemaVersion, ProductID: s.productID, Process: s.process, PID: s.pid, LoggedCall: call})
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(s.anchor, s.root)
	if err != nil {
		return err
	}
	root, err := repowrite.NewRoot(s.anchor)
	if err != nil {
		return err
	}
	hour := call.At.Format(trackerCallHourLayout)
	file, err := root.OpenAppend(path.Join(filepath.ToSlash(relative), hour+"-"+strconv.Itoa(s.pid)+".jsonl"), 0o600, 0o700)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if hour != s.hour {
		s.hour = hour
		s.removeOld(root, filepath.ToSlash(relative), call.At)
	}
	return nil
}

var trackerCallFile = regexp.MustCompile(`^(\d{8}T\d{2})-\d+\.jsonl$`)

// removeOld removes every process's files for hours older than
// trackerCallsKept before now. A removal that fails is tried again next hour.
func (s *TrackerCallLogStore) removeOld(root repowrite.Root, relative string, now time.Time) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	oldest := now.Add(-trackerCallsKept).Format(trackerCallHourLayout)
	for _, entry := range entries {
		match := trackerCallFile.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() || match[1] >= oldest {
			continue
		}
		_, _ = root.RemoveFile(path.Join(relative, entry.Name()))
	}
}

// Read returns every process's calls that started at or after since and before
// until, oldest first, and how many lines could not be read. No directory is a
// product nothing has logged a call for.
func (s *TrackerCallLogStore) Read(since, until time.Time) ([]TrackerCallRecord, int, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read the tracker calls in %s: %w", s.root, err)
	}
	first, last := since.UTC().Format(trackerCallHourLayout), until.UTC().Format(trackerCallHourLayout)
	var records []TrackerCallRecord
	unreadable := 0
	for _, entry := range entries {
		match := trackerCallFile.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() || match[1] < first || match[1] > last {
			continue
		}
		data, err := readTrackerCallFile(filepath.Join(s.root, entry.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			// Removed as old by another process between the listing and the read.
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read the tracker calls %s: %w", entry.Name(), err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var record TrackerCallRecord
			if _, err := decodeTolerating(line, &record); err != nil || record.SchemaVersion != TrackerCallSchemaVersion || record.ProductID != s.productID {
				unreadable++
				continue
			}
			if record.At.Before(since) || !record.At.Before(until) {
				continue
			}
			records = append(records, record)
		}
		if scanner.Err() != nil {
			unreadable++
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].At.Before(records[j].At) })
	return records, unreadable, nil
}

func readTrackerCallFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxTrackerCallFileBytes))
}

// TrackerCallerCount is one caller's calls over a window.
type TrackerCallerCount struct {
	Caller string `json:"caller"`
	Calls  int    `json:"calls"`
	// Took is how long its calls ran in all, and Longest the longest of them.
	Took    time.Duration `json:"took"`
	Longest time.Duration `json:"longest"`
	// Stopped is how many were stopped at their limit without an answer.
	Stopped int `json:"stopped,omitempty"`
}

// TrackerCallCount is the harness's own tracker calls over a window, counted
// by caller.
type TrackerCallCount struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// Calls is every call, and Took how long they ran in all.
	Calls int           `json:"calls"`
	Took  time.Duration `json:"took"`
	// Callers is each caller, most calls first.
	Callers []TrackerCallerCount `json:"callers,omitempty"`
	// Unreadable is how many logged lines could not be read, and Failed why the
	// log could not be read at all.
	Unreadable int    `json:"unreadable,omitempty"`
	Failed     string `json:"failed,omitempty"`
}

// CountTrackerCalls counts records by caller over the window they were read
// for.
func CountTrackerCalls(records []TrackerCallRecord, since, until time.Time, unreadable int) TrackerCallCount {
	count := TrackerCallCount{Since: since.UTC(), Until: until.UTC(), Unreadable: unreadable}
	callers := map[string]*TrackerCallerCount{}
	for _, record := range records {
		caller := record.Caller()
		if callers[caller] == nil {
			callers[caller] = &TrackerCallerCount{Caller: caller}
		}
		counted := callers[caller]
		counted.Calls++
		counted.Took += record.Took
		counted.Longest = max(counted.Longest, record.Took)
		if record.StoppedAtLimit {
			counted.Stopped++
		}
		count.Calls++
		count.Took += record.Took
	}
	for _, counted := range callers {
		count.Callers = append(count.Callers, *counted)
	}
	sort.Slice(count.Callers, func(i, j int) bool {
		if count.Callers[i].Calls != count.Callers[j].Calls {
			return count.Callers[i].Calls > count.Callers[j].Calls
		}
		return count.Callers[i].Caller < count.Callers[j].Caller
	})
	return count
}

// Says is the count in a sentence: how many calls over the window, and the
// callers that made most of them.
func (c TrackerCallCount) Says() string {
	window := fmt.Sprintf("from %s to %s", c.Since.Local().Format("2006-01-02 15:04"), c.Until.Local().Format("15:04 MST"))
	if c.Failed != "" {
		return "the harness's own tracker calls " + window + " could not be counted: " + c.Failed
	}
	if c.Calls == 0 {
		said := "the harness's own processes made no tracker call " + window
		if c.Unreadable > 0 {
			said += fmt.Sprintf(" that could be read (%d logged lines could not be)", c.Unreadable)
		}
		return said
	}
	said := fmt.Sprintf("the harness's own processes asked the tracker %d times %s, which took %s in all: ", c.Calls, window, sayTook(c.Took))
	parts := make([]string, 0, min(len(c.Callers), maxCallersSaid)+1)
	for index, caller := range c.Callers {
		if index == maxCallersSaid {
			parts = append(parts, fmt.Sprintf("and %d other callers", len(c.Callers)-maxCallersSaid))
			break
		}
		part := fmt.Sprintf("%s %d (%s in all, the longest %s", caller.Caller, caller.Calls, sayTook(caller.Took), sayTook(caller.Longest))
		if caller.Stopped > 0 {
			part += fmt.Sprintf(", %d stopped at their limit", caller.Stopped)
		}
		parts = append(parts, part+")")
	}
	said += strings.Join(parts, "; ")
	if c.Unreadable > 0 {
		said += fmt.Sprintf("; %d logged lines could not be read", c.Unreadable)
	}
	return said
}

var (
	firingNumber  = regexp.MustCompile(`#\d+\b`)
	developerRuns = regexp.MustCompile(`^(the developer run for |developer run \S+ for )`)
)

// CallerGroup is who a call is counted against: who it was for, with what makes
// one firing or run of the same caller different from the next taken out — a
// recurring pass's firing number, a developer run's own id and item — so the
// calls of every firing are counted together; and where the call named nobody,
// the process that made it.
func CallerGroup(who, process string) string {
	who = strings.TrimSpace(who)
	if who == "" {
		if process = strings.TrimSpace(process); process != "" {
			return process
		}
		return "a call that named no caller"
	}
	if developerRuns.MatchString(who) {
		return "developer runs"
	}
	return strings.TrimSpace(firingNumber.ReplaceAllString(who, ""))
}
