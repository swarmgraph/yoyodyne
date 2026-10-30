package runstate

// What each finished event stream spent, added up once and kept.
//
// The spend report prices a stream by reading its whole log, and it used to do
// that for every stream the product has ever recorded on every report — every
// run's and every conversation's log, hundreds of megabytes, read again each
// time the dashboard refreshed its spend box. A stream that has finished never
// changes again, so what it spent is read once, when a report first finds it
// finished, and kept here: a header for every stream, small enough to read
// whole, saying when its spend began and ended, and beside it one file per
// stream holding its priced calls. A report reads the header, and opens a
// stream's own file only where some of that stream's spend falls inside the
// window it was asked for.
//
// Nothing here is a second account of the money. What is kept is exactly the
// priced calls scanStreamLog reads out of the log, and the rows are made from
// them by the same code that makes them from a log read just now, so a figure
// made from what is kept is the figure the log gives. An entry is used only
// while the log is the same size and has the same modification time it had
// when the entry was made, and only while the stream still reads as finished:
// a log appended to since, or a run made live again, is read whole as before
// and kept again once it has finished. A stream still going is never kept.
//
// What is kept can be lost without losing anything: an entry missing, or one
// that will not read, is the log read again. Two processes writing the header
// at once can each drop the other's new entries, which costs the next report
// one more read of those logs.
//
// Side conversations are not kept: their rows take their role and their
// conversation from the side stream's own record, which a report reads anyway.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	streamSpendIndexVersion = 1
	streamSpendDirectory    = "spend-index"
	streamSpendHeaderName   = "streams.json"
	// maxEncodedStreamSpendBytes bounds what reading the header or one stream's
	// file will take in; past it, the entry is read from the log instead.
	maxEncodedStreamSpendBytes = 64 << 20
)

// keptStream is what the header says of one finished stream.
type keptStream struct {
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Events   int       `json:"events"`
	// StartedAt is the stream's opening moment as the report gives it: the
	// run's own for a run, the first event's for the others.
	StartedAt time.Time `json:"started_at,omitzero"`
	Reviewed  bool      `json:"reviewed,omitempty"`
	// Earliest and Latest bound the moments of its dated calls, and Undated
	// says whether any call had no moment, which no window leaves out.
	Earliest time.Time `json:"earliest,omitzero"`
	Latest   time.Time `json:"latest,omitzero"`
	Undated  bool      `json:"undated,omitempty"`
}

type keptStreams struct {
	Version int                   `json:"version"`
	Streams map[string]keptStream `json:"streams"`
}

// keptStreamCalls is one stream's own file: its priced calls, as scanned.
type keptStreamCalls struct {
	Invocations []Invocation `json:"invocations"`
}

func keptStreamKey(kind StreamKind, id string) string { return string(kind) + "/" + id }

func (s *StreamStore) streamSpendRoot() string {
	return filepath.Join(s.Root(), streamSpendDirectory)
}

func (s *StreamStore) keptCallsPath(kind StreamKind, id string) string {
	return filepath.Join(s.streamSpendRoot(), string(kind), id+".json")
}

// keptSpend reads the header. One that is missing, will not read, or is in
// another format is an empty one: every stream is then read from its log.
func (s *StreamStore) keptSpend() keptStreams {
	kept := keptStreams{Version: streamSpendIndexVersion, Streams: map[string]keptStream{}}
	var read keptStreams
	if readBoundedJSON(filepath.Join(s.streamSpendRoot(), streamSpendHeaderName), &read) != nil ||
		read.Version != streamSpendIndexVersion || read.Streams == nil {
		return kept
	}
	return read
}

func (s *StreamStore) writeKeptSpend(kept keptStreams) error {
	return replaceIndexFile(s.streamSpendRoot(), filepath.Join(s.streamSpendRoot(), streamSpendHeaderName), "spend index", kept, maxEncodedStreamSpendBytes, false)
}

// keptCalls reads one finished stream's priced calls.
func (s *StreamStore) keptCalls(entry streamEntry) ([]Invocation, error) {
	var calls keptStreamCalls
	if err := readBoundedJSON(s.keptCallsPath(entry.kind, entry.id), &calls); err != nil {
		return nil, err
	}
	return calls.Invocations, nil
}

// keep writes one finished stream's calls and returns what the header says of
// it. The calls are written before the header names them, so a header never
// names a file that is not there.
func (s *StreamStore) keep(entry streamEntry, stream Stream, scanned streamScan) (keptStream, error) {
	header := keptStream{Size: entry.size, Modified: entry.updated, Events: scanned.events, StartedAt: stream.StartedAt, Reviewed: scanned.reviewed}
	for _, invocation := range scanned.invocations {
		if invocation.At.IsZero() {
			header.Undated = true
			continue
		}
		if header.Earliest.IsZero() || invocation.At.Before(header.Earliest) {
			header.Earliest = invocation.At
		}
		if invocation.At.After(header.Latest) {
			header.Latest = invocation.At
		}
	}
	directory := filepath.Dir(s.keptCallsPath(entry.kind, entry.id))
	if err := replaceIndexFile(directory, s.keptCallsPath(entry.kind, entry.id), "kept stream spend", keptStreamCalls{Invocations: scanned.invocations}, maxEncodedStreamSpendBytes, false); err != nil {
		return keptStream{}, err
	}
	return header, nil
}

// unchanged reports a kept entry made from the log exactly as it stands now.
func (k keptStream) unchanged(entry streamEntry) bool {
	return k.Size == entry.size && k.Modified.Equal(entry.updated)
}

// spendWindow is the earliest moment a report has to see calls from: the
// start of its oldest local day, or its rolling moment, whichever is earlier.
// It is zero where the report covers every day, or names one stream, and then
// every kept stream's calls are read.
type spendWindow struct {
	oldestDay string
	since     time.Time
	whole     bool
}

func windowOf(query SpendQuery, report SpendReport) spendWindow {
	return spendWindow{oldestDay: report.Oldest, since: query.Since, whole: report.Oldest == "" || query.Match != ""}
}

// outside reports a kept stream with nothing to give a report over the window:
// every call it made is dated, before the oldest day the report keeps rows for,
// and before the rolling moment where there is one.
func (w spendWindow) outside(k keptStream) bool {
	if w.whole || k.Undated {
		return false
	}
	if k.Latest.IsZero() {
		return true
	}
	if LocalDay(k.Latest) >= w.oldestDay {
		return false
	}
	return w.since.IsZero() || k.Latest.Before(w.since)
}

// finished is the status of a stream a report can take from what is kept, and
// whether it reads as finished now: a run by the status the run index holds for
// it, a conversation by no longer being any role's, and a review by its kept
// verdict. Anything else is read from its log.
func (s *StreamStore) finished(entry streamEntry, current map[string]ConversationIdentity, runs runIndex, kept keptStream, known bool) (string, bool) {
	switch entry.kind {
	case StreamRun:
		indexed, ok := runs.Runs[entry.id]
		if !ok || !indexed.Status.Terminal() || indexed.InFlight {
			return "", false
		}
		return string(indexed.Status), true
	case StreamConversation:
		if _, held := current[entry.id]; held {
			return "", false
		}
		return ConversationEnded, true
	case StreamReview:
		if !known || !kept.Reviewed {
			return "", false
		}
		return ReviewFinished, true
	}
	return "", false
}

// keepable reports a stream just read from its log that has finished, so what
// it spent can be kept.
func keepable(stream Stream) bool {
	switch stream.Kind {
	case StreamRun:
		return Status(stream.Status).Terminal()
	case StreamConversation:
		return stream.Status == ConversationEnded
	case StreamReview:
		return stream.Status == ReviewFinished
	}
	return false
}

// readBoundedJSON decodes one kept file, refusing one past the bound.
func readBoundedJSON(path string, into any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxEncodedStreamSpendBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", path, info.Size(), maxEncodedStreamSpendBytes)
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStreamSpendBytes))
	if err != nil {
		return err
	}
	if len(encoded) == 0 {
		return errors.New("empty")
	}
	return json.Unmarshal(encoded, into)
}
