package runstate

// The index of run records.
//
// The routine questions asked of the run store — which runs are in flight,
// which still owe a step, which runs one work item has had — used to be
// answered by decoding every record the product has ever kept. With thousands
// of runs that is hundreds of megabytes read on every scheduler pass, every
// reservation, and every status reading, and the reservation read them while
// holding the lock every fresh start waits on, so one record nobody could
// decode refused every reservation behind it.
//
// The index answers those questions instead. It is one small file beside the
// records holding, for each run, the few fields the questions are asked of. The
// store writes it in the same act as the record, under the index's own lock
// (writeIndexed), so it is never rebuilt on a read; it is rebuilt from the
// records only when it is missing or written in a format this build does not
// read, and a record file it does not name — one created by something that
// went round the store — is read and added when a reader notices it.
//
// The index only chooses which records to read. Every answer still comes from
// the records themselves, read again and held to the same predicate, so the
// index can say too much without anybody acting on it: what it must never do is
// say too little. That is why a write that widens what the index has to say of a
// run — a run becoming in flight, or owing a step — is written to the index
// before the record, and one that narrows it after: a crash between the two
// leaves the index naming a run the record no longer qualifies, never the other
// way round.
//
// A record that will not decode when the index is built is kept in the index as
// unreadable and reported (UnreadableRuns). Where what is left of it still says
// the run ended, it is an old record and is left out of every answer, so one
// old record nobody can read costs that record rather than every reservation.
// Where it does not — the status says in flight, or cannot be made out at all —
// nobody can say the run is not going, so a listing that would have to count it
// refuses rather than steps over it: a run counted as not there is a run a
// second start can duplicate. History is never removed: the index names every
// run whose record is kept.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// runIndexVersion is the index format this build reads and writes. An index
	// in any other format is rebuilt from the records rather than trusted.
	runIndexVersion = 1
	runIndexName    = ".run-index.json"
	runIndexLock    = ".run-index.lock"
	// maxEncodedRunIndexBytes bounds what reading the index will take in. An
	// entry is about two hundred bytes, so this is room for some hundreds of
	// thousands of runs; an index past it is rebuilt rather than read whole.
	maxEncodedRunIndexBytes = 256 << 20
)

// runIndexEntry is what the index holds for one run: the fields the routine
// questions are asked of, and nothing an answer is read from.
type runIndexEntry struct {
	WorkItemID string    `json:"item"`
	Status     Status    `json:"status"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	// InFlight and Outstanding are kept as flags rather than worked out from
	// the status, because during a write they may be wider than the record —
	// see the file's opening comment — and an answer reads the record again to
	// narrow them.
	InFlight     bool   `json:"in_flight,omitempty"`
	Outstanding  bool   `json:"outstanding,omitempty"`
	AccountAlias string `json:"account,omitempty"`
}

func runIndexEntryOf(state State) runIndexEntry {
	return runIndexEntry{
		WorkItemID:   state.WorkItemID,
		Status:       state.Status,
		StartedAt:    state.StartedAt.UTC(),
		InFlight:     state.Status.InFlight(),
		Outstanding:  state.Outstanding(),
		AccountAlias: state.AccountAlias,
	}
}

// covers reports an entry that already names its run for every question next
// would, so writing next's record first loses nothing a crash could expose.
func (e runIndexEntry) covers(next runIndexEntry) bool {
	return e.WorkItemID == next.WorkItemID &&
		e.StartedAt.Equal(next.StartedAt) &&
		e.AccountAlias == next.AccountAlias &&
		(e.InFlight || !next.InFlight) &&
		(e.Outstanding || !next.Outstanding)
}

// widened is next, still naming the run for every question e did.
func (e runIndexEntry) widened(next runIndexEntry) runIndexEntry {
	next.InFlight = next.InFlight || e.InFlight
	next.Outstanding = next.Outstanding || e.Outstanding
	return next
}

type runIndex struct {
	Version int                      `json:"version"`
	Runs    map[string]runIndexEntry `json:"runs"`
	// Unreadable is each record the index could not read, with what refused
	// it. Such a run is in no answer until a write replaces its record.
	Unreadable map[string]unreadableRun `json:"unreadable,omitempty"`
}

// unreadableRun is what is known of a record that would not read: what refused
// it, and what could still be made out of it without the rest.
type unreadableRun struct {
	Reason     string `json:"reason"`
	WorkItemID string `json:"item,omitempty"`
	Status     Status `json:"status,omitempty"`
}

// ended reports a record whose status, as far as it can be made out, says the
// run is over. Only such a record is safe to leave out of a listing of what
// is going.
func (u unreadableRun) ended() bool { return u.Status.Terminal() }

// mayBelongTo reports a record that could be one of a work item's runs: its item
// is that one, or could not be made out.
func (u unreadableRun) mayBelongTo(workItemID string) bool {
	return u.WorkItemID == "" || u.WorkItemID == workItemID
}

func newRunIndex() runIndex {
	return runIndex{Version: runIndexVersion, Runs: map[string]runIndexEntry{}, Unreadable: map[string]unreadableRun{}}
}

func (x runIndex) clone() runIndex {
	copied := runIndex{Version: x.Version, Runs: make(map[string]runIndexEntry, len(x.Runs)), Unreadable: make(map[string]unreadableRun, len(x.Unreadable))}
	for id, entry := range x.Runs {
		copied.Runs[id] = entry
	}
	for id, unreadable := range x.Unreadable {
		copied.Unreadable[id] = unreadable
	}
	return copied
}

// runIndexCache keeps the last index each process read, by path, so a reader
// that finds the file unchanged decodes nothing. The file is only ever
// replaced by a rename, so its identity, size, and modification time together
// say whether it is the one already decoded.
var runIndexCache = struct {
	sync.Mutex
	by map[string]cachedRunIndex
}{by: map[string]cachedRunIndex{}}

type cachedRunIndex struct {
	info  os.FileInfo
	index runIndex
}

func (s *Store) runIndexPath() string { return filepath.Join(s.root, runIndexName) }

// readRunIndex reads the index file as it stands, reporting false where there
// is none this build can use. It takes no lock: the file is replaced whole by
// a rename, so a reader sees one version or the next and never a mixture.
func (s *Store) readRunIndex() (runIndex, bool, error) {
	path := s.runIndexPath()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return runIndex{}, false, nil
	}
	if err != nil {
		return runIndex{}, false, fmt.Errorf("open the run index: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return runIndex{}, false, fmt.Errorf("inspect the run index: %w", err)
	}
	runIndexCache.Lock()
	cached, seen := runIndexCache.by[path]
	runIndexCache.Unlock()
	if seen && os.SameFile(cached.info, info) && cached.info.Size() == info.Size() && cached.info.ModTime().Equal(info.ModTime()) {
		return cached.index.clone(), true, nil
	}
	if info.Size() > maxEncodedRunIndexBytes {
		return runIndex{}, false, nil
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedRunIndexBytes))
	if err != nil {
		return runIndex{}, false, fmt.Errorf("read the run index: %w", err)
	}
	var index runIndex
	// An index that will not decode, or is in another format, is not an
	// answer; it is rebuilt from the records, which are.
	if err := json.Unmarshal(encoded, &index); err != nil || index.Version != runIndexVersion || index.Runs == nil {
		return runIndex{}, false, nil
	}
	if index.Unreadable == nil {
		index.Unreadable = map[string]unreadableRun{}
	}
	runIndexCache.Lock()
	runIndexCache.by[path] = cachedRunIndex{info: info, index: index.clone()}
	runIndexCache.Unlock()
	return index, true, nil
}

// writeRunIndex replaces the index file. Its callers hold the index's lock.
func (s *Store) writeRunIndex(index runIndex) error {
	return replaceIndexFile(s.root, s.runIndexPath(), "run index", index, maxEncodedRunIndexBytes, true)
}

// replaceIndexFile replaces one index file whole, by a rename, as
// replaceJSONFile replaces a record. It is apart from that door for two
// reasons. An index names every run or stream the product keeps, so it is
// bounded by its own limit rather than by one record's: written through the
// record's bound, an index past a megabyte would refuse every write that keeps
// it in step. And it is written compact, since nobody reads it by eye.
//
// durable says whether the file and its directory are synced before this
// returns. The run index is, because an answer chooses records by it; the
// kept spend is not, because losing it costs only a log read again.
func replaceIndexFile(directory, path, label string, value any, limit int, durable bool) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	if len(encoded) > limit {
		return fmt.Errorf("encoded %s is %d bytes, limit is %d", label, len(encoded), limit)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create the directory for %s: %w", label, err)
	}
	temporary, err := os.CreateTemp(directory, ".index-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", label, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(encoded); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", label, err), temporary.Close())
	}
	if durable {
		if err := temporary.Sync(); err != nil {
			return errors.Join(fmt.Errorf("sync %s: %w", label, err), temporary.Close())
		}
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", label, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", label, err)
	}
	if !durable {
		return nil
	}
	return syncDirectory(directory)
}

// lockRunIndex serializes every change to the index across processes. It is
// only ever taken innermost — under a run's write lock or the reservation lock,
// never around either — so it cannot be half of a deadlock.
func (s *Store) lockRunIndex() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create run state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.root, runIndexLock), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the run index lock: %w", err)
	}
	if err := lockStateFile(context.Background(), lock); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock the run index: %w", err)
	}
	return func() { _ = releaseStateFile(lock) }, nil
}

// heldRunIndex is the index under its lock, built from the records first where
// there is none this build can use.
func (s *Store) heldRunIndex() (runIndex, error) {
	index, ok, err := s.readRunIndex()
	if err != nil || ok {
		return index, err
	}
	index, err = s.buildRunIndex()
	if err != nil {
		return runIndex{}, err
	}
	if err := s.writeRunIndex(index); err != nil {
		return runIndex{}, err
	}
	return index, nil
}

// buildRunIndex reads every record the store keeps into a new index. It is the
// one routine path that decodes every record, and it runs only when there is
// no index to read. A record that will not decode is named as unreadable and
// reported rather than refusing the index.
func (s *Store) buildRunIndex() (runIndex, error) {
	names, err := s.recordNames()
	if err != nil {
		return runIndex{}, err
	}
	index := newRunIndex()
	for _, runID := range names {
		s.indexRecord(&index, runID)
	}
	return index, nil
}

// indexRecord reads one record into the index, or names it unreadable. A
// record gone between the listing and the read is left out: it is no longer a
// run the store keeps.
func (s *Store) indexRecord(index *runIndex, runID string) {
	state, err := s.Read(runID)
	if errors.Is(err, os.ErrNotExist) {
		delete(index.Runs, runID)
		delete(index.Unreadable, runID)
		return
	}
	if err != nil {
		delete(index.Runs, runID)
		index.Unreadable[runID] = s.whatIsLeft(runID, err)
		noteUnreadableRun(runID, err)
		return
	}
	delete(index.Unreadable, runID)
	index.Runs[runID] = runIndexEntryOf(state)
}

// whatIsLeft is what can still be made out of a record that would not read:
// its work item and status, decoded on their own where the file is JSON at all.
func (s *Store) whatIsLeft(runID string, refused error) unreadableRun {
	left := unreadableRun{Reason: refused.Error()}
	path, err := s.statePath(runID)
	if err != nil {
		return left
	}
	encoded, err := os.ReadFile(path)
	if err != nil || len(encoded) > maxEncodedStateBytes {
		return left
	}
	var read struct {
		WorkItemID string `json:"work_item_id"`
		Status     Status `json:"status"`
	}
	if json.Unmarshal(encoded, &read) != nil {
		return left
	}
	left.WorkItemID = read.WorkItemID
	if read.Status.Terminal() || read.Status.InFlight() {
		left.Status = read.Status
	}
	return left
}

// recordNames lists the run identifiers that have a record file, sorted. It
// reads the directory's names only, which is what lets a reader notice a
// record the index does not name without decoding the ones it does.
func (s *Store) recordNames() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run state directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		if runIDPattern.MatchString(runID) {
			names = append(names, runID)
		}
	}
	sort.Strings(names)
	return names, nil
}

// runIndex is the index a reader chooses records by: the file as it stands
// where it names exactly the records the directory holds, and otherwise the
// file brought up to date under its lock — built where there is none, the
// records it does not name read and added, and the runs whose records are gone
// dropped.
func (s *Store) runIndex() (runIndex, error) {
	names, err := s.recordNames()
	if err != nil {
		return runIndex{}, err
	}
	index, ok, err := s.readRunIndex()
	if err != nil {
		return runIndex{}, err
	}
	if ok && index.names(names) {
		return index, nil
	}
	unlock, err := s.lockRunIndex()
	if err != nil {
		return runIndex{}, err
	}
	defer unlock()
	if index, err = s.heldRunIndex(); err != nil {
		return runIndex{}, err
	}
	if index.names(names) {
		return index, nil
	}
	present := make(map[string]bool, len(names))
	for _, runID := range names {
		present[runID] = true
		if _, known := index.Runs[runID]; known {
			continue
		}
		if _, known := index.Unreadable[runID]; known {
			continue
		}
		s.indexRecord(&index, runID)
	}
	for runID := range index.Runs {
		if !present[runID] {
			delete(index.Runs, runID)
		}
	}
	for runID := range index.Unreadable {
		if !present[runID] {
			delete(index.Unreadable, runID)
		}
	}
	if err := s.writeRunIndex(index); err != nil {
		return runIndex{}, err
	}
	return index, nil
}

// names reports an index that names every record in names and nothing else.
func (x runIndex) names(names []string) bool {
	if len(x.Runs)+len(x.Unreadable) != len(names) {
		return false
	}
	for _, runID := range names {
		if _, known := x.Runs[runID]; known {
			continue
		}
		if _, known := x.Unreadable[runID]; !known {
			return false
		}
	}
	return true
}

// writeIndexed writes one run's record with write, keeping the index in step in
// the same act: under the index's lock, widened before the record is written
// where the record would otherwise be named for less than it is, and made exact
// after. A write that changes nothing the index holds does not touch it.
func (s *Store) writeIndexed(state State, write func() error) error {
	s.forgetReading()
	unlock, err := s.lockRunIndex()
	if err != nil {
		return err
	}
	defer unlock()
	index, err := s.heldRunIndex()
	if err != nil {
		return err
	}
	next := runIndexEntryOf(state)
	held, known := index.Runs[state.RunID]
	_, unreadable := index.Unreadable[state.RunID]
	if !known || unreadable || !held.covers(next) {
		widened := next
		if known {
			widened = held.widened(next)
		}
		index.Runs[state.RunID] = widened
		delete(index.Unreadable, state.RunID)
		if err := s.writeRunIndex(index); err != nil {
			return err
		}
	}
	if err := write(); err != nil {
		return err
	}
	if index.Runs[state.RunID] == next {
		return nil
	}
	index.Runs[state.RunID] = next
	return s.writeRunIndex(index)
}

// RebuildRunIndex builds the index again from every record, whatever the one
// on disk says. It is maintenance's, for an index somebody has reason to doubt;
// nothing routine calls it.
func (s *Store) RebuildRunIndex() error {
	unlock, err := s.lockRunIndex()
	if err != nil {
		return err
	}
	defer unlock()
	index, err := s.buildRunIndex()
	if err != nil {
		return err
	}
	return s.writeRunIndex(index)
}

// UnreadableRuns is every kept record the index could not read, and what
// refused each. Those runs are in no answer the index gives, so this is where
// they are seen.
func (s *Store) UnreadableRuns() ([]Unreadable, error) {
	index, err := s.runIndex()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(index.Unreadable))
	for runID := range index.Unreadable {
		ids = append(ids, runID)
	}
	sort.Strings(ids)
	unreadable := make([]Unreadable, 0, len(ids))
	for _, runID := range ids {
		unreadable = append(unreadable, Unreadable{Record: runID, Err: errors.New(index.Unreadable[runID].Reason)})
	}
	return unreadable, nil
}

// indexed reads the records the index chooses with choose, in run order, and
// keeps the ones keep holds of the record itself. A record gone since the index
// named it is no longer kept and is left out; one that will not read refuses
// the answer as the listing always has, because a run the index chose is not an
// old record and nobody can say it is safe to step over. An unreadable record
// the index already knows is refused the same way where uncertain says it
// could be one the answer has to hold.
func (s *Store) indexed(label string, choose func(runIndexEntry) bool, uncertain func(unreadableRun) bool, keep func(State) bool) ([]State, error) {
	index, err := s.runIndex()
	if err != nil {
		return nil, err
	}
	unreadable := make([]string, 0)
	for runID, left := range index.Unreadable {
		if uncertain(left) {
			unreadable = append(unreadable, runID)
		}
	}
	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		return nil, fmt.Errorf("discover %s runs: the run record %s could not be read and may be one of them: %s", label, unreadable[0], index.Unreadable[unreadable[0]].Reason)
	}
	ids := make([]string, 0)
	for runID, entry := range index.Runs {
		if choose(entry) {
			ids = append(ids, runID)
		}
	}
	sort.Strings(ids)
	states := make([]State, 0, len(ids))
	for _, runID := range ids {
		state, err := s.Read(runID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("discover %s runs: %w", label, err)
		}
		if keep(state) {
			states = append(states, state)
		}
	}
	return states, nil
}

// unreadableRunNotices says each unreadable record once per process, as
// noteUnknownFields does, so a listing read every minute does not repeat it.
var unreadableRunNotices = struct {
	sync.Mutex
	said map[string]bool
}{said: map[string]bool{}}

func noteUnreadableRun(runID string, err error) {
	unreadableRunNotices.Lock()
	defer unreadableRunNotices.Unlock()
	if unreadableRunNotices.said[runID] {
		return
	}
	unreadableRunNotices.said[runID] = true
	unknownFieldNotices.Lock()
	to := unknownFieldNotices.to
	unknownFieldNotices.Unlock()
	fmt.Fprintf(to, "the run record %s could not be read and is left out of the runs the harness counts and lists; it is kept as it is: %v\n", runID, err)
}
