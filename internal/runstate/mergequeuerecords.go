package runstate

// What the merge queue keeps beside its entries for the parts of the harness
// that work every queue at once and for the people reading about them: which
// queues exist, an entry moved explicitly to the other queue mode, why a queue
// last refused to admit a change, what its worker's last pass found, and its
// recent passes.
// docs/designs/integration-through-a-merge-queue.md is the design; mergequeue.go
// is the record these sit beside.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

const (
	mergeQueueRefusalName = "refusal.json"
	mergeQueuePassName    = "pass.json"
	// mergeQueuePassHistoryName is the queue's recent passes (recordPassHistory).
	mergeQueuePassHistoryName = "pass-history.json"
)

// Keys reports every queue the product has a record for, by repository and
// target branch. A directory holding no record yet — a lock taken before
// anything was admitted — is no queue. A record that cannot be read or does
// not decode is named in the error, which is returned beside every queue that
// could be read, so one torn record hides no other queue and is not dropped
// in silence.
func (s *MergeQueueStore) Keys() ([]MergeQueueKey, error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the merge queue state root: %w", err)
	}
	defer root.Close()
	directory := filepath.ToSlash(s.directory())
	listed, err := root.ReadDirectory(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the merge queues: %w", err)
	}
	var keys []MergeQueueKey
	var problems []error
	for _, item := range listed {
		if !item.IsDir() {
			continue
		}
		queueRoot, err := root.OpenDirectory(path.Join(directory, item.Name()))
		if err != nil {
			problems = append(problems, fmt.Errorf("open the merge queue %s: %w", item.Name(), err))
			continue
		}
		encoded, err := readQueue(queueRoot)
		queueRoot.Close()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("read the merge queue %s: %w", item.Name(), err))
			continue
		}
		var named MergeQueue
		if _, err := decodeTolerating(encoded, &named); err != nil {
			problems = append(problems, fmt.Errorf("decode the merge queue %s: %w", item.Name(), err))
			continue
		}
		key := MergeQueueKey{Repository: named.Repository, TargetBranch: named.TargetBranch}
		if key.validate() != nil || key.directory() != item.Name() {
			problems = append(problems, fmt.Errorf("the merge queue record in %s is not the queue that directory is for", item.Name()))
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Repository != keys[j].Repository {
			return keys[i].Repository < keys[j].Repository
		}
		return keys[i].TargetBranch < keys[j].TargetBranch
	})
	return keys, errors.Join(problems...)
}

// MergeQueueTransferError is a transfer the queue refuses: the entry is not
// one released to move to that mode, or it has already moved somewhere else.
type MergeQueueTransferError struct {
	EntryID string
	Reason  string
}

func (e MergeQueueTransferError) Error() string {
	return fmt.Sprintf("merge queue entry %s cannot be moved: %s", e.EntryID, e.Reason)
}

// Transfer admits the change of a released entry again in the other queue
// mode, in the place the released entry had, so moving a change between modes
// moves it nowhere in the queue. The entry it supersedes keeps its
// generations, its landing record and the release itself, and the new entry
// has none of them: its candidate is built and verified from nothing.
//
// Only an entry released for exactly this move may be transferred: its queued
// merge was confirmed withdrawn before it was released (MergeQueueHandback's
// TransferTo), so nothing the old mode was asked for can still land it. A
// transfer already made is reported again rather than made twice, which is
// what makes a transfer interrupted after this write safe to repeat.
func (s *MergeQueueStore) Transfer(ctx context.Context, key MergeQueueKey, entryID string, mode MergeQueueMode, evidence MergeQueueModeEvidence, at time.Time) (MergeQueueEntry, bool, error) {
	if at.IsZero() {
		at = time.Now()
	}
	evidence.ObservedAt = evidence.ObservedAt.UTC()
	evidence.Unmet = append([]string(nil), evidence.Unmet...)
	if err := errors.Join(key.validate(), errors.Join(evidence.validate(mode)...)); err != nil {
		return MergeQueueEntry{}, false, fmt.Errorf("merge queue transfer: %w", err)
	}
	if !mode.valid() {
		return MergeQueueEntry{}, false, fmt.Errorf("merge queue transfer: mode %q is neither %q nor %q", mode, MergeQueueHarness, MergeQueueForge)
	}
	queueRoot, unlock, err := s.lockQueue(ctx, key)
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	defer unlock()
	queue, err := s.load(queueRoot, key, true)
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	refuse := func(format string, args ...any) (MergeQueueEntry, bool, error) {
		return MergeQueueEntry{}, false, MergeQueueTransferError{EntryID: entryID, Reason: fmt.Sprintf(format, args...)}
	}
	var released MergeQueueEntry
	found := false
	for _, entry := range queue.Entries {
		switch {
		case entry.EntryID == entryID:
			released, found = entry, true
		case found && entry.Supersedes == entryID:
			if entry.Mode != mode {
				return refuse("it was already admitted again as entry %d in the %s queue", entry.Order, entry.Mode)
			}
			return entry, false, nil
		}
	}
	if !found {
		return refuse("the merge queue for %s admits no such entry", key.TargetBranch)
	}
	if released.Mode == mode {
		return refuse("it is already in the %s queue", mode)
	}
	landing, landed, err := s.loadLanding(queueRoot, key, entryID, false)
	if err != nil {
		return MergeQueueEntry{}, false, err
	}
	switch {
	case !landed || landing.Handback == nil:
		return refuse("it has not been withdrawn and released from the %s queue", released.Mode)
	case landing.Handback.Continuation != MergeQueueReleased || landing.Handback.TransferTo != mode:
		return refuse("it left the queue for something other than a move to the %s queue (%s)", mode, landing.Handback.Continuation)
	}
	moved := released
	moved.EntryID, moved.Order, moved.PredecessorOrder = "", 0, 0
	moved.Mode, moved.ModeEvidence, moved.AdmittedAt = mode, evidence, at.UTC()
	moved.Supersedes, moved.Place = released.EntryID, released.WorkPlace()
	return s.append(queueRoot, key, queue, moved)
}

// MergeQueueRefusal is the last time a queue refused to admit an approved
// change because neither queue mode could land into its target as it is
// protected: the target requires the forge's own merge queue, or its
// protection could not be read. It is kept so a reader can see that the queue
// is switched on and not in use, which change it refused, and what would let
// it be used. What becomes of the refused change is the admission's to decide
// and record, not this record's.
type MergeQueueRefusal struct {
	At          time.Time `json:"at"`
	WorkItemID  string    `json:"work_item_id"`
	RunID       string    `json:"run_id"`
	Requirement string    `json:"requirement"`
	// Step is what would let the queue be used, and PersonOnly says it is a
	// repository setting only a person can change.
	Step       string `json:"step"`
	PersonOnly bool   `json:"person_only,omitempty"`
}

func (r MergeQueueRefusal) validate() error {
	var problems []error
	if r.At.IsZero() {
		problems = append(problems, errors.New("a refusal records when it was made"))
	}
	for field, value := range map[string]string{"work item id": r.WorkItemID, "requirement": r.Requirement, "step": r.Step} {
		if err := mergeQueueText(field, value, true); err != nil {
			problems = append(problems, err)
		}
	}
	if !ValidRunID(r.RunID) {
		problems = append(problems, fmt.Errorf("run id %q is not a run id", r.RunID))
	}
	return errors.Join(problems...)
}

// RecordRefusal writes a queue's latest refusal, replacing the one before it.
func (s *MergeQueueStore) RecordRefusal(ctx context.Context, key MergeQueueKey, refusal MergeQueueRefusal) error {
	refusal.At = refusal.At.UTC()
	if err := errors.Join(key.validate(), refusal.validate()); err != nil {
		return fmt.Errorf("merge queue refusal: %w", err)
	}
	return s.writeSide(ctx, key, mergeQueueRefusalName, refusal)
}

// ClearRefusal removes a queue's refusal once a change has been admitted to
// it, because the refusal no longer describes the queue.
func (s *MergeQueueStore) ClearRefusal(ctx context.Context, key MergeQueueKey) error {
	return s.removeSide(ctx, key, mergeQueueRefusalName)
}

// Refusal reports a queue's latest refusal, and false where it has none.
func (s *MergeQueueStore) Refusal(key MergeQueueKey) (MergeQueueRefusal, bool, error) {
	var refusal MergeQueueRefusal
	found, err := s.readSide(key, mergeQueueRefusalName, &refusal)
	return refusal, found, err
}

// MergeQueuePassOutcome is what one pass of a queue's worker came to, in a
// closed vocabulary a surface can count by.
type MergeQueuePassOutcome string

const (
	// MergeQueuePassIdle is a pass that found nothing waiting.
	MergeQueuePassIdle MergeQueuePassOutcome = "idle"
	// MergeQueuePassWaiting is a pass that stopped short of a stage or a
	// landing for a reason that is nobody's failure: the operator's pause, a
	// provider that cannot serve the review, a merge the forge still holds.
	MergeQueuePassWaiting MergeQueuePassOutcome = "waiting"
	// MergeQueuePassLanded is a pass that confirmed at least one landing.
	MergeQueuePassLanded MergeQueuePassOutcome = "landed"
	// MergeQueuePassHandedBack is a pass that handed a change back.
	MergeQueuePassHandedBack MergeQueuePassOutcome = "handed-back"
	// MergeQueuePassStopped is a pass that stopped on something somebody has
	// to look at: a refusal, an outcome nothing could establish, or an error.
	MergeQueuePassStopped MergeQueuePassOutcome = "stopped"
)

func (o MergeQueuePassOutcome) valid() bool {
	switch o {
	case MergeQueuePassIdle, MergeQueuePassWaiting, MergeQueuePassLanded, MergeQueuePassHandedBack, MergeQueuePassStopped:
		return true
	}
	return false
}

// MergeQueuePass is what a queue's worker last found, for a reader that wants
// to know why an entry is not moving. It replaces the pass before it.
type MergeQueuePass struct {
	At      time.Time             `json:"at"`
	Outcome MergeQueuePassOutcome `json:"outcome"`
	// EntryID and WorkItemID name the entry the pass ended on, where it ended
	// on one.
	EntryID    string `json:"entry_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
	// Says is what the pass ended on, in ordinary words.
	Says string `json:"says,omitempty"`
	// Moved is when the pass first got the queue moving: it started a
	// candidate's checks or its review, landed an entry or handed one back, or
	// found nothing in the queue to move. A pass that only waited — on the
	// forge, on another process, on a stage an earlier start may still hold —
	// has none. It is what says when the queue was working again after a move
	// onto a new build (BuildMoves).
	Moved *time.Time `json:"moved,omitempty"`
	// ForRestart marks a pass that ended because its watch session was
	// restarting onto a new build: cut off part-way, or left a check stage or a
	// review for the session that comes back to start.
	ForRestart bool `json:"for_restart,omitempty"`
}

// mergeQueuePassHistory is how many of a queue's passes its history keeps,
// newest last: about twelve hours of a queue going round once a minute while
// it waits, which reaches back past the longest a move onto a new build has
// held a queue. Each is bounded (historySaysBytes), so the record stays far
// inside the bound on one state record and costs little to write again at
// every pass.
const mergeQueuePassHistory = 720

// historySaysBytes bounds what one pass in the history says.
const historySaysBytes = 200

// RecordPass writes what a queue's worker last found.
func (s *MergeQueueStore) RecordPass(ctx context.Context, key MergeQueueKey, pass MergeQueuePass) error {
	pass.At = pass.At.UTC()
	pass.Says = BoundedMergeQueueText(strings.Join(strings.Fields(pass.Says), " "))
	if err := key.validate(); err != nil {
		return fmt.Errorf("merge queue pass: %w", err)
	}
	if pass.At.IsZero() || !pass.Outcome.valid() {
		return fmt.Errorf("merge queue pass: a pass records when it ended and one of the outcomes a pass can have, not %q", pass.Outcome)
	}
	if pass.Moved != nil {
		moved := pass.Moved.UTC()
		pass.Moved = &moved
	}
	if err := s.writeSide(ctx, key, mergeQueuePassName, pass); err != nil {
		return err
	}
	return s.recordPassHistory(ctx, key, pass)
}

// recordPassHistory adds a pass to the queue's history, keeping the newest
// mergeQueuePassHistory. The latest pass alone says what the queue is doing
// now; the history is what says, after the fact, what it was waiting on
// across an hour, such as the time after a restart before it was working
// again.
func (s *MergeQueueStore) recordPassHistory(ctx context.Context, key MergeQueueKey, pass MergeQueuePass) error {
	queueRoot, unlock, err := s.lockQueue(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	var history []MergeQueuePass
	encoded, err := queueRoot.ReadFile(mergeQueuePassHistoryName)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read the merge queue's pass history for %s: %w", key.TargetBranch, err)
	default:
		// A history that cannot be read is begun again rather than left to
		// refuse every pass that follows: it is an account, not a gate.
		_ = json.Unmarshal(encoded, &history)
	}
	pass.Says = oneline.Bound(pass.Says, historySaysBytes)
	history = append(history, pass)
	keep := s.passHistory
	if keep <= 0 {
		keep = mergeQueuePassHistory
	}
	if len(history) > keep {
		history = history[len(history)-keep:]
	}
	written, err := encodeRecord("merge queue pass history", history)
	if err != nil {
		return err
	}
	if err := queueRoot.WriteFile(mergeQueuePassHistoryName, written, 0o600, false); err != nil {
		return fmt.Errorf("write the merge queue's pass history for %s: %w", key.TargetBranch, err)
	}
	return queueRoot.Sync()
}

// PassHistory is a queue's recent passes, oldest first, and nothing where no
// pass has been recorded since the history began to be kept.
func (s *MergeQueueStore) PassHistory(key MergeQueueKey) ([]MergeQueuePass, error) {
	var history []MergeQueuePass
	if _, err := s.readSide(key, mergeQueuePassHistoryName, &history); err != nil {
		return nil, err
	}
	return history, nil
}

// LastPass reports what a queue's worker last found, and false where no pass
// has been recorded.
func (s *MergeQueueStore) LastPass(key MergeQueueKey) (MergeQueuePass, bool, error) {
	var pass MergeQueuePass
	found, err := s.readSide(key, mergeQueuePassName, &pass)
	return pass, found, err
}

// writeSide replaces one of the records kept beside a queue's own, under the
// queue's record lock so it is never written beside an admission's readback.
func (s *MergeQueueStore) writeSide(ctx context.Context, key MergeQueueKey, name string, record any) error {
	encoded, err := encodeRecord("merge queue "+strings.TrimSuffix(name, ".json"), record)
	if err != nil {
		return err
	}
	queueRoot, unlock, err := s.lockQueue(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	if err := queueRoot.WriteFile(name, encoded, 0o600, false); err != nil {
		return fmt.Errorf("write the merge queue's %s for %s: %w", name, key.TargetBranch, err)
	}
	return queueRoot.Sync()
}

func (s *MergeQueueStore) removeSide(ctx context.Context, key MergeQueueKey, name string) error {
	if err := key.validate(); err != nil {
		return fmt.Errorf("merge queue: %w", err)
	}
	queueRoot, unlock, err := s.lockQueue(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	if err := queueRoot.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the merge queue's %s for %s: %w", name, key.TargetBranch, err)
	}
	return queueRoot.Sync()
}

func (s *MergeQueueStore) readSide(key MergeQueueKey, name string, into any) (bool, error) {
	if err := key.validate(); err != nil {
		return false, fmt.Errorf("merge queue: %w", err)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return false, fmt.Errorf("pin the merge queue state root: %w", err)
	}
	defer root.Close()
	queueRoot, err := root.OpenDirectory(path.Join(filepath.ToSlash(s.directory()), key.directory()))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open the merge queue for %s: %w", key.TargetBranch, err)
	}
	defer queueRoot.Close()
	encoded, err := queueRoot.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the merge queue's %s for %s: %w", name, key.TargetBranch, err)
	}
	if len(encoded) > maxEncodedStateBytes {
		return false, fmt.Errorf("the merge queue's %s for %s is %d bytes, which exceeds the %d byte bound", name, key.TargetBranch, len(encoded), maxEncodedStateBytes)
	}
	unknown, err := decodeTolerating(encoded, into)
	noteUnknownFields("merge queue "+name, unknown)
	if err != nil {
		return false, fmt.Errorf("decode the merge queue's %s for %s: %w", name, key.TargetBranch, err)
	}
	return true, nil
}
