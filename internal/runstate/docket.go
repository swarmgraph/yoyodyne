package runstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// maxEncodedDocketEntryBytes bounds one encoded entry, including the trailing
// newline. The writer and the reader share it, so an entry that was written is
// always one that can be read back.
const maxEncodedDocketEntryBytes = 128 << 10

// closureReasonCutNote says a decision's reasoning was cut to what the record
// carries, and where the whole of it is. The decision was recorded on the work
// item before the entry was closed, so there is somewhere to send a reader.
const closureReasonCutNote = "\n[cut; the work item's own notes carry the whole of this reasoning]"

// DocketStore is where the work that has stopped moving is collected, in the
// same operating-system state root as the runs and the reports and beside them
// rather than among them. It is append-only per product, for the reason the
// report log is: an entry outlives the run that produced it, and a run whose
// state is settled and whose artifacts are cleaned up leaves a stoppage that is
// still the development manager's to decide.
//
// There are two logs and not one. The first is what stopped, written where it
// stopped; the second is what was decided about it, written where somebody
// decided. Keeping them apart is what lets a decision take an entry off the
// docket without rewriting the record of the stoppage — which is a record every
// scan re-derives, so an edited one would either be undone by the next build or
// have to be defended against it.
type DocketStore struct {
	root      string
	productID domain.ProductID
}

func NewDocketStore(root string, productID domain.ProductID) (*DocketStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &DocketStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *DocketStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the docket actually is.
func (s *DocketStore) Path() string { return filepath.Join(s.root, "docket.jsonl") }

// ClosurePath names the log of decisions that settled entries. It is a second
// append-only log beside the first rather than a rewrite of it, for the reason
// the first is append-only: an entry is written once as the work stops, and a
// record that is never revised is one no interrupted process can leave
// half-written. What a closure changes is what a reader is shown, and that is a
// join rather than an edit.
func (s *DocketStore) ClosurePath() string { return filepath.Join(s.root, "docket-closed.jsonl") }

// WindowPath names where the development manager's docket window last stopped.
func (s *DocketStore) WindowPath() string { return filepath.Join(s.root, "docket-window.json") }

// docketWindow is the one record at WindowPath.
type docketWindow struct {
	Position triage.WindowPosition `json:"position"`
}

// WindowPosition reads where the last docket window stopped, which is where the
// next one resumes. A product whose window has never been recorded stands at the
// oldest end of the docket, which is not a failure to read.
//
// It is kept per product beside the docket rather than on a conversation,
// because the docket is per product and so is the role that decides it: a fresh
// conversation for that role resuming the walk is the point, since a window that
// restarted at every `--new` would show the same oldest few forever.
func (s *DocketStore) WindowPosition() (triage.WindowPosition, error) {
	data, err := os.ReadFile(s.WindowPath())
	if errors.Is(err, os.ErrNotExist) {
		return triage.WindowPosition{}, nil
	}
	if err != nil {
		return triage.WindowPosition{}, fmt.Errorf("read the docket window: %w", err)
	}
	var recorded docketWindow
	if err := json.Unmarshal(data, &recorded); err != nil {
		return triage.WindowPosition{}, fmt.Errorf("decode the docket window: %w", err)
	}
	return recorded.Position, nil
}

// RecordWindowPosition records where a docket window stopped. It replaces the
// record whole, since only the latest position means anything, and it is written
// the way every replaced record here is — whole, synced, and named by a rename —
// so an interrupted write leaves the last position rather than half of one.
func (s *DocketStore) RecordWindowPosition(position triage.WindowPosition) error {
	if len(position.Key) > triage.MaxKeyBytes {
		return fmt.Errorf("docket window key is %d bytes, limit is %d", len(position.Key), triage.MaxKeyBytes)
	}
	return replaceJSONFile(s.root, s.WindowPath(), "docket window", docketWindow{Position: position})
}

// RecordOnce records one entry and reports whether this call is what created
// it. Docketing is idempotent because the thing it describes is one event: a
// run stops once, and the process that stopped it and the sweep that settles it
// afterwards must between them produce one entry rather than two. So the entry's
// key decides, and an entry whose key is already on the docket changes nothing
// and is not an error — asking twice about one stoppage means the same thing the
// second time.
//
// A key whose entry a decision settled is different, and is recorded again. What
// the key names is a run or a publication rather than one moment of it, so the
// same key can carry a second stoppage: a repair continues the run that stopped,
// under the run's own identifier, and a run that dies again after being repaired
// derives exactly the key its settled entry carries. Refusing that would make a
// live blocker on live work invisible — the entry the reader is shown is the
// settled one, and nothing else would ever mention the new death. So a stoppage
// recorded after the decision that settled the last one opens the key again, and
// one recorded before it is that same settled stoppage arriving twice.
//
// Recording it is not the same as deciding it is new: this compares the moment
// the entry was made, which every caller stamps as now, so a caller re-deriving
// an old stoppage from a durable record must ask whether the stoppage itself is
// newer than the decision. Docketer.Build is where that question is asked; the
// callers that record a death as it happens are new by construction.
//
// Two processes appending at the same instant can still both see an absent key
// and both write, which is why List collapses duplicates rather than trusting
// this check alone. Taking a lock here instead would put the docket behind a
// lock every conversation open and every sweep contends for, to prevent a
// duplicate that costs a reader one repeated paragraph.
func (s *DocketStore) RecordOnce(entry triage.Entry) (bool, error) {
	if err := s.validate(entry); err != nil {
		return false, err
	}
	listed, err := s.List()
	if err != nil {
		return false, err
	}
	for _, existing := range listed {
		if existing.Key != entry.Key {
			continue
		}
		// An entry nobody has decided about is this stoppage already docketed.
		if existing.Closed == nil {
			return false, nil
		}
		// A settled one is too, unless this stoppage was recorded after the
		// decision that settled it.
		if !entry.RecordedAt.After(existing.Closed.ClosedAt) {
			return false, nil
		}
	}
	encoded, err := encodeDocketEntry(entry)
	if err != nil {
		return false, err
	}
	if err := s.append(s.Path(), "docket entry", encoded); err != nil {
		return false, err
	}
	return true, nil
}

// Close records the decision that settled one docketed stoppage and reports
// whether this call is what closed it. Closing is idempotent for the reason
// docketing is: an entry is settled once, and a decision recorded twice about
// one stoppage means the same thing the second time. The first decision is the
// one that stands, because it is what took the entry off the docket and every
// later reader was reading a settled entry.
//
// A decision that no longer holds is not that. Waiting takes an entry off the
// docket until a moment it names, and the entry is a question again after it, so
// a decision recorded once that moment has passed is a second decision about a
// stoppage nobody has settled — it is recorded, and it is what stands from then
// on.
//
// The entry has to be on this docket. A closure naming nothing is a record about
// a stoppage nobody can look at, and it would be indistinguishable from one whose
// key was mistyped — which is a settled entry still on the docket and a closure
// nothing ever joins. So is a decision made before the stoppage it claims to
// settle was docketed: nothing would ever join that either.
func (s *DocketStore) Close(closure triage.Closure) (bool, error) {
	// The reasoning is a role's own prose and nothing bounds what a role writes,
	// so it is cut to what a record may carry rather than refused. A settled
	// stoppage left on the docket because the decision behind it was wordy is the
	// one failure this must not have, and the whole of the reasoning is on the work
	// item, where the decision itself was recorded.
	closure.Reason = boundRecordedText(closure.Reason, triage.MaxMessageBytes, closureReasonCutNote)
	if err := s.validateClosure(closure); err != nil {
		return false, err
	}
	listed, err := s.List()
	if err != nil {
		return false, err
	}
	for _, entry := range listed {
		if entry.Key != closure.Key {
			continue
		}
		if closure.ClosedAt.Before(entry.RecordedAt) {
			return false, fmt.Errorf("the stoppage keyed %s was docketed at %s, after this decision was made at %s, so the decision is about some earlier stoppage",
				closure.Key, entry.RecordedAt.UTC().Format(time.RFC3339), closure.ClosedAt.UTC().Format(time.RFC3339))
		}
		// A decision still holding over this entry has already settled it. One that
		// has lapsed has not, and what is being recorded is the next decision.
		if entry.Closed != nil && entry.Closed.Holds(closure.ClosedAt) {
			return false, nil
		}
		encoded, err := encodeDocketClosure(closure)
		if err != nil {
			return false, err
		}
		if err := s.append(s.ClosurePath(), "docket closure", encoded); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("no docket entry keyed %s is recorded, so there is no stoppage to close", closure.Key)
}

// List returns what the docket stands at: one entry per key, in the order those
// entries were recorded, each carrying the decision recorded about it where
// there is one.
//
// One per key, because a key names one event. Two entries under one key are
// either the same stoppage written twice by processes that raced — and the first
// of them is the one that describes when it was noticed — or the same run or
// publication having stopped again after a decision settled the last one, and
// then the later stoppage is what the key stands at. What tells those apart is
// the decision between them: an entry recorded after it is a stoppage that
// decision was not about.
//
// The join happens here rather than in each reader because there is more than
// one — the docket the development manager reads, the sweep that delivers a
// stoppage to her, and the two verbs that carry a decision out — and a reader
// that asked only the entry log would show a settled stoppage as though nobody
// had looked at it.
//
// A docket that does not exist yet is a product where nothing has stopped,
// which is not a failure to read.
func (s *DocketStore) List() ([]triage.Entry, error) {
	recorded, err := s.records()
	if err != nil {
		return nil, err
	}
	decisions, err := s.Closures()
	if err != nil {
		return nil, err
	}
	// Which record each key stands at. A decision between two stoppages of one key
	// is what separates them: the later one is a stoppage that decision was not
	// about, and it is what the key stands at from then on. Two with no decision
	// between them are one stoppage written twice by processes that raced, and the
	// first of those is the one that says when it was noticed.
	standing := make(map[string]int, len(recorded))
	for index, entry := range recorded {
		held, found := standing[entry.Key]
		if !found {
			standing[entry.Key] = index
			continue
		}
		if decidedBetween(decisions[entry.Key], recorded[held].RecordedAt, entry.RecordedAt) {
			standing[entry.Key] = index
		}
	}
	listed := make([]triage.Entry, 0, len(standing))
	for index, entry := range recorded {
		if standing[entry.Key] != index {
			continue
		}
		if settled, decided := decisionOver(decisions[entry.Key], entry.RecordedAt); decided {
			entry.Closed = &settled
		}
		listed = append(listed, entry)
	}
	if len(listed) == 0 {
		return nil, nil
	}
	return listed, nil
}

// decidedBetween reports a decision made about the stoppage a key stood at
// before another was recorded under it, which is what makes the later one a
// stoppage of its own rather than the first arriving twice.
func decidedBetween(decisions []triage.Closure, held, recorded time.Time) bool {
	for _, decision := range decisions {
		if !held.After(decision.ClosedAt) && recorded.After(decision.ClosedAt) {
			return true
		}
	}
	return false
}

// decisionOver is the decision recorded about the stoppage a key stands at: the
// latest one made since it was docketed. A decision older than the entry belongs
// to a stoppage this one replaced and says nothing about it.
func decisionOver(decisions []triage.Closure, recorded time.Time) (triage.Closure, bool) {
	var latest triage.Closure
	found := false
	for _, decision := range decisions {
		if decision.ClosedAt.Before(recorded) {
			continue
		}
		if !found || decision.ClosedAt.After(latest.ClosedAt) {
			latest = decision
			found = true
		}
	}
	return latest, found
}

// Closures reports the decisions recorded about each key, in the order they were
// made. There is more than one where a decision lapsed and the entry it held was
// decided again, and where a key carries a stoppage that happened after an
// earlier one was settled; which of them stands over the entry a reader is shown
// is List's question.
func (s *DocketStore) Closures() (map[string][]triage.Closure, error) {
	closed := make(map[string][]triage.Closure)
	err := s.eachLine(s.ClosurePath(), "docket closures", func(line []byte) error {
		decoded, err := decodeDocketClosure(line)
		if err != nil {
			return err
		}
		if err := s.validateClosure(decoded); err != nil {
			return err
		}
		closed[decoded.Key] = append(closed[decoded.Key], decoded)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return closed, nil
}

// records is the log alone, every line of it, with nothing joined on and nothing
// collapsed. What one key stands at is decided in List, where the decisions that
// settled entries can be read beside them.
func (s *DocketStore) records() ([]triage.Entry, error) {
	var entries []triage.Entry
	err := s.eachLine(s.Path(), "docket", func(line []byte) error {
		decoded, err := decodeDocketEntry(line)
		if err != nil {
			return err
		}
		if err := s.validate(decoded); err != nil {
			return err
		}
		entries = append(entries, decoded)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// eachLine reads one of the two logs a record at a time. A log that does not
// exist yet is a product nothing has been recorded in, which is not a failure to
// read; anything else is, because a log nobody can read must never be read as
// empty.
func (s *DocketStore) eachLine(path, what string, record func([]byte) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", what, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedDocketEntryBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := record([]byte(line)); err != nil {
			return fmt.Errorf("decode %s: %w", what, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", what, err)
	}
	return nil
}

// append adds one record to one of the two logs, durably: the bound the reader
// shares is checked first, so a record that was written is always one that can be
// read back, and the directory is synced where the log itself is new.
func (s *DocketStore) append(path, what string, encoded []byte) error {
	if len(encoded) > maxEncodedDocketEntryBytes {
		return fmt.Errorf("encoded %s is %d bytes, limit is %d", what, len(encoded), maxEncodedDocketEntryBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create docket directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect %s log: %w", what, statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s log: %w", what, err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append %s: %w", what, err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append %s: %w", what, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync %s log: %w", what, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s log: %w", what, err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

func decodeDocketEntry(data []byte) (triage.Entry, error) {
	var decoded triage.Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		return triage.Entry{}, err
	}
	if decoded.StopClass == "" && (decoded.Class == triage.ClassStoppedRun || decoded.Class == triage.ClassUnstartedRun || decoded.Class == triage.ClassEscalation) {
		decoded.StopClass = StopUnknown.Name()
	}
	if decoded.StopClass != "" && !StopClass(decoded.StopClass).Valid() {
		return triage.Entry{}, errors.New("stop_class is invalid")
	}
	if err := decoded.Validate(); err != nil {
		return triage.Entry{}, err
	}
	if err := validateResumesAt(decoded); err != nil {
		return triage.Entry{}, err
	}
	return decoded, nil
}

// validateResumesAt holds the step a resumable stall is continued at to the
// phases StallResumeStep accepts. It is asked here rather than by the entry,
// because the entry cannot name a phase and this package owns them.
func validateResumesAt(entry triage.Entry) error {
	if entry.ResumesAt == "" {
		return nil
	}
	if _, ok := StallResumeStep(entry.ResumesAt); !ok {
		return fmt.Errorf("invalid triage docket entry: resumes_at: %q is not a step a stall is continued at past its developer attempt; only %q and %q are",
			entry.ResumesAt, PhaseChecking, PhaseReviewing)
	}
	return nil
}

func encodeDocketEntry(entry triage.Entry) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entry); err != nil {
		return nil, fmt.Errorf("encode docket entry: %w", err)
	}
	return buffer.Bytes(), nil
}

func decodeDocketClosure(data []byte) (triage.Closure, error) {
	var decoded triage.Closure
	if err := json.Unmarshal(data, &decoded); err != nil {
		return triage.Closure{}, err
	}
	if err := decoded.Validate(); err != nil {
		return triage.Closure{}, err
	}
	return decoded, nil
}

func encodeDocketClosure(closure triage.Closure) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(closure); err != nil {
		return nil, fmt.Errorf("encode docket closure: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *DocketStore) validate(entry triage.Entry) error {
	if entry.StopClass != "" && !StopClass(entry.StopClass).Valid() {
		return errors.New("stop_class is invalid")
	}
	if entry.ProductID != s.productID {
		return fmt.Errorf("docket entry product %q does not match store product %q", entry.ProductID, s.productID)
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	return validateResumesAt(entry)
}

func (s *DocketStore) validateClosure(closure triage.Closure) error {
	if closure.ProductID != s.productID {
		return fmt.Errorf("docket closure product %q does not match store product %q", closure.ProductID, s.productID)
	}
	return closure.Validate()
}
