package runstate

// A run record has one writer at a time, and no change to it is lost: guarantee
// 1 of docs/designs/one-owner-for-each-guarantee.md, as the architect ruled it
// for the run record (yoyodyne-ifd.428.116).
//
// Two ways in, and nothing else:
//
//   - A narrow update (Store.Update) changes only its own fields, on the record
//     as it is stored at that moment, under the run's write lock. It is how a
//     writer that does not hold the run — the merge queue, a transfer,
//     reconciliation's cleanup — changes a run, so a field some other writer
//     changed in between is kept rather than overwritten. Every one is declared
//     here, by name, because only this package declares a RunUpdate.
//   - A whole save (Store.Save) carries the revision its copy was read at. Each
//     narrow update moves the stored revision on, so a save from a copy read
//     before one is refused with StaleRunError naming the run and the fields
//     that differ, and nothing is written. The writer reads the run again and
//     makes its change again.
//
// A whole save does not move the revision: it is the run's own holder writing
// its own copy, and moving the revision there would refuse that holder's next
// save of the very copy it just wrote. Routing keeps a generation of its own
// that Save already compares (checkRoutingWrite), so a routing change does not
// move the revision either. The writers outside this package that still save a
// whole record are listed in runrecordwriters_audit_test.go, and the list only
// shrinks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// RunUpdate is one named change to a run record. apply makes the change to the
// record as stored and reports whether it changed anything; an error refuses
// the change and nothing is written. It replaces whatever it changes behind a
// pointer rather than writing through it, so the copy it was handed is left as
// it was.
type RunUpdate interface {
	apply(state *State) (bool, error)
}

// Update applies one narrow change to the run as it is stored, under the run's
// write lock, and returns the run as written. A change that changes nothing
// writes nothing and returns the stored run, so making it again after a lost
// reply is safe.
func (s *Store) Update(ctx context.Context, runID string, update RunUpdate) (State, error) {
	unlock, err := s.lockRunWrites(ctx, runID)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	stored, err := s.Load(runID)
	if err != nil {
		return State{}, err
	}
	next, changed, err := ApplyUpdate(stored, update)
	if err != nil || !changed {
		return stored, err
	}
	if !stored.Status.InFlight() && next.HoldsDeveloperSlot() {
		return State{}, RefusedStateError{Problem: fmt.Errorf("%w: run %s is recorded %s and this update would make it %s", ErrMadeLiveWithoutASlot, runID, stored.Status, next.Status)}
	}
	next.Worker = s.workerToWrite(runID, stored.Worker)
	next = next.withStopRecorded(stopHistoryOf(stored))
	if next, err = next.withRecordedTextsBounded(); err != nil {
		return State{}, err
	}
	if err := s.validateState(next); err != nil {
		return State{}, err
	}
	if err := s.writeState(next); err != nil {
		return State{}, err
	}
	return next, nil
}

// ApplyUpdate is what Update does to the stored record, made on a copy held in
// memory: the change, and the revision moved on where it changed anything. It
// writes nothing; it is for a stand-in for the store, which has no file to lock.
func ApplyUpdate(state State, update RunUpdate) (State, bool, error) {
	next := state
	changed, err := update.apply(&next)
	if err != nil || !changed {
		return state, false, err
	}
	next.Revision = state.Revision + 1
	return next, true, nil
}

// StaleRunError is a whole save made from a copy of the run read before a
// narrow update changed it. Nothing is written; the writer reads the run again
// and makes its change again. Fields names what the copy would have
// overwritten.
type StaleRunError struct {
	RunID  string
	Held   uint64
	Stored uint64
	Fields []string
}

func (e StaleRunError) Error() string {
	differs := "nothing this build can name"
	if len(e.Fields) > 0 {
		differs = strings.Join(e.Fields, ", ")
	}
	return fmt.Sprintf("run %s changed since this copy was read (it is at revision %d and the copy at %d), so saving the copy would overwrite %s; read the run again and make the change again", e.RunID, e.Stored, e.Held, differs)
}

// checkRevision refuses a whole save of a copy read at a revision other than
// the stored one. It is wrapped as a RefusedStateError because no later save of
// the same copy closes the gap: a run that gets it back ends over the record
// that is actually stored (orchestrator's recordEndingAfterRefusedSave).
func (s *Store) checkRevision(state State, stored uint64) error {
	if state.Revision == stored {
		return nil
	}
	stale := StaleRunError{RunID: state.RunID, Held: state.Revision, Stored: stored}
	if current, err := s.Read(state.RunID); err == nil {
		stale.Fields = differingFields(state, current)
	}
	return RefusedStateError{Problem: stale}
}

// differingFields names the record's fields, as they are written, whose values
// differ between two copies of one run. What every write changes is left out.
func differingFields(held, stored State) []string {
	encode := func(state State) map[string]json.RawMessage {
		fields := map[string]json.RawMessage{}
		if encoded, err := json.Marshal(state); err == nil {
			_ = json.Unmarshal(encoded, &fields)
		}
		return fields
	}
	a, b := encode(held), encode(stored)
	var names []string
	for name := range a {
		if _, also := b[name]; !also {
			b[name] = nil
		}
	}
	for name, value := range b {
		switch name {
		case "revision", "updated_at", "worker":
			continue
		}
		if string(a[name]) != string(value) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// PullRequestSuperseded records that the run's pull request was closed because
// its change landed by Vehicle, as the merge queue's retirement of it does. It
// changes nothing where the run's request is no longer Number, has merged, or
// already names what superseded it.
type PullRequestSuperseded struct {
	Number  int
	Vehicle string
	State   string
	At      time.Time
	// Wrote is the forge's identifier for the comment the close left, in the
	// form PullRequest.Wrote keeps, where the forge named one.
	Wrote string
}

func (u PullRequestSuperseded) apply(state *State) (bool, error) {
	published := state.PullRequest
	if published == nil || published.Number != u.Number || published.Merged || strings.TrimSpace(published.Superseded) != "" {
		return false, nil
	}
	superseded := *published
	superseded.Superseded = u.Vehicle
	superseded.State = u.State
	if u.Wrote != "" {
		superseded.RecordWrite(u.Wrote)
	}
	state.PullRequest = &superseded
	state.UpdatedAt = u.At
	return true, nil
}

// MergeQueueAdmitted records the merge queue entry that holds the run's
// change. Readmission is a transfer naming the entry the change moved to, which
// is recorded only on a run that already records an admission.
type MergeQueueAdmitted struct {
	Record      MergeQueueAdmissionRecord
	Readmission bool
	At          time.Time
}

func (u MergeQueueAdmitted) apply(state *State) (bool, error) {
	if u.Readmission && state.MergeQueue == nil {
		return false, nil
	}
	if state.MergeQueue != nil && *state.MergeQueue == u.Record {
		return false, nil
	}
	record := u.Record
	state.MergeQueue = &record
	state.UpdatedAt = u.At
	return true, nil
}

// QueuedRunCompleted ends a run whose change the merge queue admitted: it
// succeeds, owing the cleanup its landing will earn, and every pause it was
// carrying is over. It is refused on a run that records no admission, or that
// has already ended some other way.
type QueuedRunCompleted struct {
	At time.Time
}

func (u QueuedRunCompleted) apply(state *State) (bool, error) {
	if state.MergeQueue == nil {
		return false, fmt.Errorf("run %s records no merge queue admission, so it does not end as queued", state.RunID)
	}
	if state.Status == StatusSucceeded && state.Phase == PhaseCleaningUp {
		return false, nil
	}
	if state.Status.Terminal() {
		return false, fmt.Errorf("run %s has already ended %s, so it does not end again as queued", state.RunID, state.Status)
	}
	state.ProviderStop = ""
	state.RedeployStop = nil
	state.OperatorHeldSince = nil
	state.Status = StatusSucceeded
	// Cleaning up is what is owed once the queue lands the change and records
	// the landing as the run's integration; until then the run owes nothing,
	// and its branch and worktree are what a handback for repair continues.
	state.Phase = PhaseCleaningUp
	state.UpdatedAt = u.At
	completed := u.At
	state.CompletedAt = &completed
	return true, nil
}

// RaisedForDesignCompleted ends a run whose developer raised that its work item
// waits on the architect's design, once the item is back in the backlog
// labelled needs-design: it succeeds at what it was for, complete, with nothing
// integrated, and every pause it was carrying is over. Divergence is what the
// run's workflow observation has to say about ending there, kept only where the
// run carries none already. It is refused on a run that has already ended some
// other way.
type RaisedForDesignCompleted struct {
	Divergence string
	At         time.Time
}

func (u RaisedForDesignCompleted) apply(state *State) (bool, error) {
	if state.Status == StatusSucceeded && state.Phase == PhaseComplete && state.StopClass == StopNeedsDesign {
		return false, nil
	}
	if state.Status.Terminal() {
		return false, fmt.Errorf("run %s has already ended %s, so it does not end again as raised for design", state.RunID, state.Status)
	}
	state.ProviderStop = ""
	state.RedeployStop = nil
	state.OperatorHeldSince = nil
	state.StopClass = StopNeedsDesign
	state.Status = StatusSucceeded
	state.Phase = PhaseComplete
	if u.Divergence != "" && state.WorkflowDivergence == "" {
		state.WorkflowDivergence = u.Divergence
	}
	state.UpdatedAt = u.At
	completed := u.At
	state.CompletedAt = &completed
	return true, nil
}

// UnchangedRunCompleted ends a run whose reviewer approved a change with no
// file differences from its base, once its item is settled: it succeeds,
// complete, with nothing queued or integrated, and every pause it was carrying
// is over. LandingBlockedBy and LandingImpedimentProblem are where the
// settlement left an item the run did not discharge, which a tracker refusing
// the dependency moves; Divergence is what the run's workflow observation has
// to say about ending there, kept only where the run carries none already. It
// is refused on a run that has already ended some other way.
type UnchangedRunCompleted struct {
	LandingBlockedBy         string
	LandingImpedimentProblem string
	Divergence               string
	At                       time.Time
}

func (u UnchangedRunCompleted) apply(state *State) (bool, error) {
	if state.Status == StatusSucceeded && state.Phase == PhaseComplete && state.ApprovedWithNoChanges {
		return false, nil
	}
	if state.Status.Terminal() {
		return false, fmt.Errorf("run %s has already ended %s, so it does not end again as approved with no file changes", state.RunID, state.Status)
	}
	state.ApprovedWithNoChanges = true
	state.LandingBlockedBy = u.LandingBlockedBy
	state.LandingImpedimentProblem = u.LandingImpedimentProblem
	state.ProviderStop = ""
	state.RedeployStop = nil
	state.OperatorHeldSince = nil
	state.Status = StatusSucceeded
	state.Phase = PhaseComplete
	if u.Divergence != "" && state.WorkflowDivergence == "" {
		state.WorkflowDivergence = u.Divergence
	}
	state.UpdatedAt = u.At
	completed := u.At
	state.CompletedAt = &completed
	return true, nil
}

// MergeQueueEntryLost stops a run that ended waiting on the merge queue and
// whose entry the queue no longer holds, with nothing landed and nothing handed
// back to the run: it fails with the reason and the blocker the item was given,
// as the queue's own handback fails one, and keeps its branch and worktree. It
// changes nothing on a run that is not waiting on the queue — one whose change
// landed, or that already ended some other way.
type MergeQueueEntryLost struct {
	Blocker string
	Failure string
	At      time.Time
}

func (u MergeQueueEntryLost) apply(state *State) (bool, error) {
	if !state.AwaitingMergeQueue() {
		return false, nil
	}
	if strings.TrimSpace(u.Blocker) == "" || strings.TrimSpace(u.Failure) == "" {
		return false, fmt.Errorf("run %s is stopped for a lost merge queue entry only with a blocker and a reason", state.RunID)
	}
	completed := u.At
	state.Status = StatusFailed
	state.StopClass = StopIntegration
	state.Blocker = u.Blocker
	state.Failure = u.Failure
	state.CompletedAt = &completed
	state.UpdatedAt = u.At
	return true, nil
}

// RetryRecorded records one waited-out failure at a boundary, which the run's
// own process writes before it waits. It is the narrow form for a process
// whose copy may have gone out of date while another writer recorded on the
// run: an admitted change waiting on the tracker, while the merge queue lands
// it and closes its pull request.
type RetryRecorded struct {
	Retry Retry
}

func (u RetryRecorded) apply(state *State) (bool, error) {
	retries := make([]Retry, 0, len(state.Retries)+1)
	state.Retries = append(append(retries, state.Retries...), u.Retry)
	state.UpdatedAt = u.Retry.At
	return true, nil
}

// MergeQueueNoteRecorded sets or clears the note a run still owes its item
// about its change's admission to the merge queue (MergeQueueNoteOwed). A nil
// Owed clears it, which is what writing the note does.
type MergeQueueNoteRecorded struct {
	Owed *MergeQueueNoteOwed
	At   time.Time
}

func (u MergeQueueNoteRecorded) apply(state *State) (bool, error) {
	if u.Owed == nil && state.MergeQueueNoteOwed == nil {
		return false, nil
	}
	if u.Owed != nil {
		if state.MergeQueue == nil {
			return false, fmt.Errorf("run %s records no merge queue admission, so it owes no note about one", state.RunID)
		}
		owed := *u.Owed
		state.MergeQueueNoteOwed = &owed
	} else {
		state.MergeQueueNoteOwed = nil
	}
	state.UpdatedAt = u.At
	return true, nil
}

// InstallRecorded adds one run of one of the project's declared install steps
// to the run's record of them (InstallRun).
type InstallRecorded struct {
	Install InstallRun
	At      time.Time
}

func (u InstallRecorded) apply(state *State) (bool, error) {
	installs := make([]InstallRun, 0, len(state.Install)+1)
	state.Install = append(append(installs, state.Install...), u.Install)
	state.UpdatedAt = u.At
	return true, nil
}

// UntouchedRerunRecorded writes one rerun of a failure confined to untouched
// tests onto the run (untouchedrerun.go): added before it starts, and replaced
// by the same rerun, matched by stage, commit and start, once it has ended.
type UntouchedRerunRecorded struct {
	Rerun UntouchedRerun
	At    time.Time
}

func (u UntouchedRerunRecorded) apply(state *State) (bool, error) {
	reruns := append([]UntouchedRerun(nil), state.UntouchedReruns...)
	for index, rerun := range reruns {
		if rerun.Stage == u.Rerun.Stage && rerun.Commit == u.Rerun.Commit && rerun.StartedAt.Equal(u.Rerun.StartedAt) {
			reruns[index] = u.Rerun
			state.UntouchedReruns = reruns
			state.UpdatedAt = u.At
			return true, nil
		}
	}
	probe := State{UntouchedReruns: reruns}
	probe.BeginUntouchedRerun(u.Rerun)
	state.UntouchedReruns = probe.UntouchedReruns
	state.UpdatedAt = u.At
	return true, nil
}

// InstallWaitRecorded puts a wait to try an install step again on the run, or
// takes it off where Wait is nil (InstallWait).
type InstallWaitRecorded struct {
	Wait *InstallWait
	At   time.Time
}

func (u InstallWaitRecorded) apply(state *State) (bool, error) {
	if u.Wait == nil && state.InstallWait == nil {
		return false, nil
	}
	if u.Wait != nil {
		wait := *u.Wait
		state.InstallWait = &wait
	} else {
		state.InstallWait = nil
	}
	state.UpdatedAt = u.At
	return true, nil
}

// SpendWaitRecorded puts a wait for the shared remote to take a review round on
// the run, or takes it off where Wait is nil (SpendWait).
type SpendWaitRecorded struct {
	Wait *SpendWait
	At   time.Time
}

func (u SpendWaitRecorded) apply(state *State) (bool, error) {
	if u.Wait == nil && state.SpendWait == nil {
		return false, nil
	}
	if u.Wait != nil {
		wait := *u.Wait
		state.SpendWait = &wait
	} else {
		state.SpendWait = nil
	}
	state.UpdatedAt = u.At
	return true, nil
}

// LeftProcessesFound records what the harness found still running for the run
// the last time it stopped what the run started (State.LeftProcesses),
// replacing what an earlier stop found. The same finding again changes nothing.
//
// An entry that counts no passes and names no report takes both from the entry
// for the same process it replaces, so a stop made as a session or the run
// ends, which counts nothing, neither starts a process's three passes again nor
// lets it be reported a second time.
type LeftProcessesFound struct {
	Left []execution.LeftProcess
	At   time.Time
}

func (u LeftProcessesFound) apply(state *State) (bool, error) {
	if len(u.Left) == 0 && len(state.LeftProcesses) == 0 {
		return false, nil
	}
	u.Left = append([]execution.LeftProcess(nil), u.Left...)
	for index := range u.Left {
		found := &u.Left[index]
		if found.Passes != 0 || found.Reported != "" {
			continue
		}
		for _, before := range state.LeftProcesses {
			if SameLeftProcess(before, *found) {
				found.Passes, found.Reported = before.Passes, before.Reported
				break
			}
		}
	}
	if encoded, err := json.Marshal(u.Left); err == nil {
		if before, err := json.Marshal(state.LeftProcesses); err == nil && string(before) == string(encoded) {
			return false, nil
		}
	}
	state.LeftProcesses = u.Left
	state.UpdatedAt = u.At
	return true, nil
}

// SameLeftProcess reports two findings of one process: the same number and
// command, found the same way. A number the system handed to another program
// is another process.
func SameLeftProcess(a, b execution.LeftProcess) bool {
	return a.PID > 0 && a.PID == b.PID && a.Command == b.Command && a.Unmarked == b.Unmarked
}

// LateStopRecorded records a stop that reached the run only after its checks
// had passed (LateStop). The same stop recorded again changes nothing.
type LateStopRecorded struct {
	Stop LateStop
	At   time.Time
}

func (u LateStopRecorded) apply(state *State) (bool, error) {
	if err := u.Stop.Validate(); err != nil {
		return false, fmt.Errorf("late stop: %w", err)
	}
	if state.LateStop != nil && *state.LateStop == u.Stop {
		return false, nil
	}
	stop := u.Stop
	state.LateStop = &stop
	state.UpdatedAt = u.At
	return true, nil
}

// ForgeWaitRecorded puts a wait at the merge step for GitHub to be reachable
// again on the run (ForgeWait), together with the run's landing as its process
// holds it at that moment (Integration, nil where nothing is landed): a landing
// through the pull request taken back to be made again from the top, or a local
// promotion already made that the record has not been told of yet. The commit
// the landing made is kept as the harness's (HarnessCommit, where it names one).
type ForgeWaitRecorded struct {
	Wait          ForgeWait
	Integration   *Integration
	HarnessCommit string
	At            time.Time
}

func (u ForgeWaitRecorded) apply(state *State) (bool, error) {
	if err := u.Wait.Validate(); err != nil {
		return false, fmt.Errorf("forge wait: %w", err)
	}
	if !state.Status.InFlight() {
		return false, fmt.Errorf("a wait for GitHub requires a run in flight, and run %s is %s", state.RunID, state.Status)
	}
	wait := u.Wait
	state.ForgeWait = &wait
	state.Integration = nil
	if u.Integration != nil {
		integration := *u.Integration
		state.Integration = &integration
		// A landing that reached its promotion passed its checks, so it is no
		// longer waiting to be judged, as integrate records it.
		state.ReplayUnjudged = false
	}
	if strings.TrimSpace(u.HarnessCommit) != "" {
		state.HarnessCommit = u.HarnessCommit
	}
	state.UpdatedAt = u.At
	return true, nil
}

// IntegrationRecorded records the run's integration: the merge queue's landing
// of its change, which the queue records as a run's own promotion would be. It
// changes nothing where the run already records the same landing.
//
// WholeBy, where set, is the queue run that checked the change over the whole
// module in place of a run that left that to the queue
// (ChecksPassed.WholeLeftToQueue), and is written onto that run's evidence. A
// run that checked its change whole itself is given none.
type IntegrationRecorded struct {
	Integration Integration
	WholeBy     string
	// CandidateReview is the merge queue's review that approved the landed
	// candidate, given only for an entry admitted on the approval of the head
	// its resolution resolves (confinedresolution.go). That resolution had no
	// review of its own, so the run records this one as its approval.
	CandidateReview *MergeQueueReviewEvidence
	At              time.Time
}

func (u IntegrationRecorded) apply(state *State) (bool, error) {
	changed := false
	if review := u.CandidateReview; review != nil && state.ReviewDecision == "" && state.ConflictResolution != nil {
		if review.Decision != ReviewApprove || strings.TrimSpace(review.SessionID) == "" {
			return false, errors.New("the candidate review given for a resolution is not an approval by a recorded session")
		}
		state.ReviewDecision, state.ReviewSessionID, state.ReviewModel, state.ReviewResolvedModel = ReviewApprove, review.SessionID, review.Model, review.ResolvedModel
		state.ReviewSummary = review.Summary
		// What the change is for is unchanged by resolving its conflict, so what
		// the earlier approval said it approves still says whether it discharges
		// the item.
		state.ReviewApproves = state.ConflictResolution.Approved.Approves
		changed = true
	}
	if passed := state.ChecksPassed; u.WholeBy != "" && passed != nil && passed.WholeLeftToQueue && passed.WholeBy != u.WholeBy {
		passed.WholeBy = u.WholeBy
		changed = true
	}
	if state.Integration == nil || *state.Integration != u.Integration {
		integration := u.Integration
		state.Integration = &integration
		changed = true
	}
	// The queue lands only a candidate its review approved, so a resolution
	// nothing reviewed before it rejoined was approved by that review
	// (conflictmodel.go).
	if state.NoteConflictModelQueueReview(string(ReviewApprove)) {
		changed = true
	}
	if changed {
		state.UpdatedAt = u.At
	}
	return changed, nil
}

// CleanupRecorded records how far cleaning up an integrated run got: which of
// its artifacts are gone, and Failure where some are left, which keeps the run
// owing the rest. With nothing left it completes the run. It is refused on a
// run that is not cleaning up, because something moved it on while its
// artifacts were being removed.
type CleanupRecorded struct {
	WorktreeRemoved bool
	BranchRemoved   bool
	Failure         string
	// Kept is the account of a checkout the harness will not delete. It
	// completes the run like a finished cleanup does, leaving the checkout.
	Kept string
	At   time.Time
}

func (u CleanupRecorded) apply(state *State) (bool, error) {
	if state.Phase != PhaseCleaningUp {
		if state.Phase == PhaseComplete && u.Failure == "" {
			return false, nil
		}
		return false, fmt.Errorf("run %s is %s rather than cleaning up, so its cleanup is not recorded on it", state.RunID, nonEmptyPhase(state.Phase))
	}
	state.WorktreeRemoved = u.WorktreeRemoved
	state.BranchRemoved = u.BranchRemoved
	state.UpdatedAt = u.At
	if u.Failure != "" {
		state.CleanupFailure = u.Failure
		return true, nil
	}
	// Nothing is left to remove, so the run is complete and an outstanding
	// cleanup marker is no longer true.
	state.CleanupFailure = ""
	state.KeptCheckout = u.Kept
	if state.StopClass == StopCleanup {
		state.StopClass = ""
	}
	state.Phase = PhaseComplete
	return true, nil
}

func nonEmptyPhase(phase Phase) string {
	if phase == "" {
		return "at no recorded step"
	}
	return "at " + string(phase)
}

// MergeDropFound records a merge the forge dropped, as the sweep that settles
// queued merges finds it and before the work item and the triage docket are
// told: the pull request as the forge left it, the publication failure, and the
// drop marked unsettled (MergeDrop.Unsettled). Written first, it is what keeps a
// tracker that does not answer from leaving the run recorded as still queued.
// It changes nothing on a run whose pull request is another one or has merged.
type MergeDropFound struct {
	PullRequest    PullRequest
	PublishFailure string
	Drop           MergeDrop
	At             time.Time
}

func (u MergeDropFound) apply(state *State) (bool, error) {
	published := state.PullRequest
	if published == nil || published.Number != u.PullRequest.Number || published.Merged {
		return false, nil
	}
	drop := u.Drop
	drop.Unsettled = true
	pullRequest := u.PullRequest
	state.PullRequest = &pullRequest
	state.PublishFailure = u.PublishFailure
	state.MergeDrop = &drop
	state.UpdatedAt = u.At
	return true, nil
}

// HarnessResumeWaited records on an integration stop the harness resumes by
// itself what its latest try found it still waiting on, and when
// (State.NoteHarnessResumeWait). It changes nothing on a run the harness no
// longer resumes by itself: one going again, settled, or handed to the
// development manager.
type HarnessResumeWaited struct {
	Why string
	At  time.Time
}

func (u HarnessResumeWaited) apply(state *State) (bool, error) {
	if !state.HarnessResumesIntegration() || !state.NoteHarnessResumeWait(u.Why, u.At) {
		return false, nil
	}
	state.UpdatedAt = u.At
	return true, nil
}

// HarnessResumeRefused records on an integration stop that the harness's own
// try met something other than a service not answering, which ends its tries
// (State.NoteHarnessResumeRefused). It changes nothing on a run with no stop,
// or whose stop already records one.
type HarnessResumeRefused struct {
	Why string
	At  time.Time
}

func (u HarnessResumeRefused) apply(state *State) (bool, error) {
	if !state.NoteHarnessResumeRefused(u.Why, u.At) {
		return false, nil
	}
	state.UpdatedAt = u.At
	return true, nil
}

// CIWaitDecided records the forge's decision on the commit a run waits for
// (CIWait.Decided), as the scheduler reads it without a developer slot. It
// changes nothing on a run no longer waiting, waiting on another commit, or
// whose wait already records a decision: the first decision read is the one
// kept, and the run's own reading when it is continued is what ends the wait.
//
// The record's updated time is left alone. Among written work at one priority
// a waiting run is continued longest wait first by that time
// (nextContinuations), and recording what GitHub said must not send it to the
// back.
type CIWaitDecided struct {
	Commit   string
	Decision CIWaitDecision
}

func (u CIWaitDecided) apply(state *State) (bool, error) {
	wait := state.CIWait
	if wait == nil || wait.Commit != u.Commit || wait.Decided != nil || !state.Status.InFlight() {
		return false, nil
	}
	if u.Decision.At.IsZero() || strings.TrimSpace(u.Decision.Says) == "" {
		return false, fmt.Errorf("the forge's decision on run %s's commit is recorded with when it was read and what it says", state.RunID)
	}
	decided := *wait
	decision := u.Decision
	decision.At = decision.At.UTC()
	decided.Decided = &decision
	state.CIWait = &decided
	return true, nil
}

// CIWaitSlotGivenBack puts a run that took its developer slot back to settle its
// wait for the forge's checks on Commit back to waiting, holding no slot, and
// records why on the run (State.ContinuationWait): the continuation was refused
// before it went on (orchestrator's giveBackCIWaitSlot). It changes nothing on a
// run that is not running, has no wait on that commit, or has not taken its slot.
type CIWaitSlotGivenBack struct {
	Commit string
	Why    string
	At     time.Time
}

func (u CIWaitSlotGivenBack) apply(state *State) (bool, error) {
	wait := state.CIWait
	if wait == nil || wait.Commit != u.Commit || !wait.SlotTaken || state.Status != StatusRunning {
		return false, nil
	}
	why := strings.TrimSpace(u.Why)
	if why == "" || u.At.IsZero() {
		return false, fmt.Errorf("a developer slot given back by run %s is recorded with why and when", state.RunID)
	}
	waiting := *wait
	waiting.SlotTaken = false
	state.CIWait = &waiting
	state.ContinuationWait = &ContinuationWait{Why: why, Since: u.At.UTC()}
	state.UpdatedAt = u.At
	return true, nil
}

// RecoveryContinuationWaited records on a run's outstanding continuation of
// this kind what the work session last found it waiting on
// (State.WaitRecoveryContinuation), for a writer that does not hold the run:
// the scheduler, when the development manager's request could not start. It
// changes nothing where no such continuation is outstanding or the same wait is
// already written, so a pull every minute through a long wait writes once.
type RecoveryContinuationWaited struct {
	Action RecoveryAction
	Why    string
	At     time.Time
}

func (u RecoveryContinuationWaited) apply(state *State) (bool, error) {
	return state.WaitRecoveryContinuation(u.Action, u.Why, u.At), nil
}

// QueueRerunRecorded records the one further run of GitHub's checks the sweep
// asks for after GitHub's own merge queue dropped a request only for tests the
// change does not touch (PullRequest.QueueRerun): first before the merge is
// queued again, then with GitHub's answer, and, where GitHub took it, the merge
// queued again from QueuedAt. A head is given one, so a record for the same
// head may only be answered, never replaced. It changes nothing on a run whose
// pull request is another one, carries another head, or has merged.
type QueueRerunRecorded struct {
	Number   int
	Rerun    ForgeChecksRerun
	QueuedAt *time.Time
	At       time.Time
}

func (u QueueRerunRecorded) apply(state *State) (bool, error) {
	published := state.PullRequest
	if published == nil || published.Number != u.Number || published.Merged || published.HeadCommit != u.Rerun.HeadCommit {
		return false, nil
	}
	if prior := published.QueueRerun; prior != nil && prior.HeadCommit == u.Rerun.HeadCommit {
		if refusal := prior.Extends(&u.Rerun); refusal != "" {
			return false, fmt.Errorf("record the further run of the checks of pull request %d: %s", u.Number, refusal)
		}
	}
	changed := *published
	rerun := u.Rerun
	changed.QueueRerun = &rerun
	if u.QueuedAt != nil {
		queuedAt := *u.QueuedAt
		changed.MergeQueued = true
		changed.QueuedAt = &queuedAt
	}
	state.PullRequest = &changed
	state.UpdatedAt = u.At
	return true, nil
}

// WorktreeRebuilt records that the checkout the maintenance sweep removed from a
// run was put back at the path the run records, from its branch at the head the
// run recorded, so the record no longer says it is gone. It changes nothing on a
// run whose record already says the checkout is there, and is refused on a run
// whose branch is recorded gone, since nothing could have been rebuilt from it.
type WorktreeRebuilt struct {
	At time.Time
}

func (u WorktreeRebuilt) apply(state *State) (bool, error) {
	if !state.WorktreeRemoved && state.WorktreeSweptAt == nil {
		return false, nil
	}
	if state.BranchRemoved {
		return false, fmt.Errorf("run %s records its branch %s as removed, so no checkout was rebuilt from it", state.RunID, state.Branch)
	}
	state.WorktreeRemoved = false
	state.WorktreeSweptAt = nil
	state.UpdatedAt = u.At
	return true, nil
}

// HostForcedStopRecorded records on a run the stop its hosting watch session
// could not record for itself because the supervisor forced that session to
// stop (orchestrator.PreserveRunsOfForcedHost). It is the stop a drain records
// as it stops a run for a redeploy, so the session that comes back continues
// the run from it exactly as it continues one the drain stopped. It changes
// nothing on a run that is no longer running, already carries a stop, or whose
// worker is not the process that was forced, so a run some other process took
// up in between is left as that process has it.
type HostForcedStopRecorded struct {
	// Host is the forced process: its number and the start time the operating
	// system reported for it. A run is changed only where its worker names both.
	Host WorkerIdentity
	Stop RedeployStop
}

func (u HostForcedStopRecorded) apply(state *State) (bool, error) {
	if state.Status != StatusRunning || state.RedeployStop != nil || !forcedHostWorked(state.Worker, u.Host) {
		return false, nil
	}
	// The stop is at whatever phase the run had reached, as a drain's is.
	stop := u.Stop
	stop.Phase = state.Phase
	if err := stop.Validate(); err != nil {
		return false, fmt.Errorf("stop recorded for a forced host: %w", err)
	}
	state.RedeployStop = &stop
	if u.Stop.At.After(state.UpdatedAt) {
		state.UpdatedAt = u.Stop.At
	}
	return true, nil
}

// forcedHostWorked reports a run's worker being the forced process: the same
// number, and the same start time where both are known, so a later process that
// took the number is never mistaken for it.
func forcedHostWorked(worker *WorkerIdentity, host WorkerIdentity) bool {
	if worker == nil || host.PID <= 0 || worker.PID != host.PID {
		return false
	}
	if worker.StartedAt.IsZero() || host.StartedAt.IsZero() {
		return true
	}
	return sameStart(worker.StartedAt, host.StartedAt)
}
