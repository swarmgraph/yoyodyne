package orchestrator

// The audit that gives back a claim with nothing alive behind it, and the pass
// that runs it. The four nights it exists for are the regression cases in
// readmodel; what is here is what the harness does about one — the tracker write,
// the record that keeps it to once, and the placement in the loop that decides
// whether a full machine ever reaches it at all.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// auditMoment is when every audit below is made.
var auditMoment = time.Date(2026, 9, 4, 7, 30, 0, 0, time.UTC)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// claimHarness stands in for the tracker, the run store, and the release log.
type claimHarness struct {
	orchestratortest.ClaimTracker
	mu   sync.Mutex
	runs []runstate.State
	log  []runstate.ReleasedClaim
	// saved is every run record the audit ended, by run, which is the half of a
	// release that actually frees the developer slot.
	saved map[string]runstate.State
	// held names the runs whose lease a live process owns, which is the only
	// answer about liveness that is not a guess.
	held map[string]bool
	// adopted names the runs whose lease this audit took, so a test can say that a
	// claim was settled under one rather than written to from outside.
	adopted []string
	// parkOnAdopt replaces what a run's record says once its lease is taken, which
	// is how a run that parked between the reading and the lease is driven.
	parkOnAdopt map[string]runstate.State
	// failAppend, failRuns, failAdopt, and failSave stand in for a log that
	// cannot be written and a run store that will not answer or be written.
	failAppend error
	failRuns   error
	failAdopt  error
	failSave   error
}

func newClaimHarness(runs ...runstate.State) *claimHarness {
	harness := &claimHarness{
		ClaimTracker: orchestratortest.ClaimTracker{Released: map[string]string{}},
		runs:         runs,
		saved:        map[string]runstate.State{},
		held:         map[string]bool{},
		parkOnAdopt:  map[string]runstate.State{},
	}

	harness.ClaimTracker.Mu = &harness.mu
	return harness
}

// AdoptRun stands in for taking a run's lease. The nil lease is what a released
// one is: runstate.Lease.Release tolerates it, so a fake needs nothing more.
//
// A terminal run is refused outright, which the real store does not do — the
// point is the opposite of imitation. How runstate.Store answers for a record
// nobody holds is a question the audit must not depend on, so this fake makes
// asking it a failure rather than something that quietly works here and is
// decided by the store in production.
func (h *claimHarness) AdoptRun(_ context.Context, runID string) (runstate.State, *runstate.Lease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failAdopt != nil {
		return runstate.State{}, nil, h.failAdopt
	}
	if h.held[runID] {
		return runstate.State{}, nil, runstate.ErrRunHeld
	}
	for _, run := range h.runs {
		if run.RunID == runID && run.Status.Terminal() {
			return runstate.State{}, nil, fmt.Errorf("run %s already ended and must not be taken up to be settled", runID)
		}
	}
	h.adopted = append(h.adopted, runID)
	if parked, changed := h.parkOnAdopt[runID]; changed {
		return parked, nil, nil
	}
	for _, run := range h.runs {
		if run.RunID == runID {
			return run, nil, nil
		}
	}
	return runstate.State{}, nil, errors.New("no such run")
}

func (h *claimHarness) Save(state runstate.State) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failSave != nil {
		return h.failSave
	}
	h.saved[state.RunID] = state
	for index, run := range h.runs {
		if run.RunID == state.RunID {
			h.runs[index] = state
		}
	}
	return nil
}

func (h *claimHarness) Recorded() ([]runstate.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failRuns != nil {
		return nil, h.failRuns
	}
	return h.runs, nil
}

func (h *claimHarness) Append(released runstate.ReleasedClaim) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failAppend != nil {
		return h.failAppend
	}
	h.log = append(h.log, released)
	return nil
}

func (h *claimHarness) List() ([]runstate.ReleasedClaim, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.log, nil
}

func (h *claimHarness) auditor() ClaimAuditor {
	return ClaimAuditor{
		Tracker:   h,
		Runs:      h,
		Releases:  h,
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}
}

// deadRun is the record a killed process leaves: in flight, and nothing written
// to it since the night before.
func deadRun(runID, workItemID string) runstate.State {
	moment := auditMoment.Add(-9 * time.Hour)
	return runstate.State{
		RunID:      runID,
		WorkItemID: workItemID,
		Status:     runstate.StatusRunning,
		Phase:      runstate.PhaseDeveloping,
		StartedAt:  moment.Add(-time.Hour),
		UpdatedAt:  moment,
	}
}

func claimedItem(id, title string) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: title, Status: "in_progress", Priority: 2}
}

// The whole of it in one pass: a claimed item whose run died is given back to the
// queue, the reason reaches the item's own notes, and the release is on the
// durable log for a surface to say once.
func TestADeadClaimIsGivenBackAndRecorded(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-264", "yoyodyne-ifd.264"))
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.264", "Recoverable failures retry with backoff")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 1 {
		t.Fatalf("released = %+v, want the dead claim given back", sweep.Released)
	}
	if len(sweep.Problems) != 0 {
		t.Fatalf("problems = %v, want none", sweep.Problems)
	}
	note, gave := harness.Released["yoyodyne-ifd.264"]
	if !gave {
		t.Fatal("the tracker was never asked to give the claim back")
	}
	if !strings.Contains(note, "gave this item back") {
		t.Fatalf("the note written onto the item is %q, want it to say the harness gave the item back", note)
	}
	recorded := sweep.Released[0]
	if recorded.WorkItemID != "yoyodyne-ifd.264" || recorded.RunID != "run-264" {
		t.Fatalf("recorded = %+v, want the item and the run that left it", recorded)
	}
	// The other half of freeing the item: the run's own record is ended, so it
	// stops filling a developer slot and the scheduler stops passing the item over
	// as already running. It is ended under the run's lease rather than written to
	// from outside.
	if len(harness.adopted) != 1 || harness.adopted[0] != "run-264" {
		t.Fatalf("adopted = %v, want the dead run taken up under its own lease", harness.adopted)
	}
	settled, ended := harness.saved["run-264"]
	if !ended {
		t.Fatal("the dead run's record was left in flight, so the slot it holds is still taken and nothing will pull the item")
	}
	if settled.Status != runstate.StatusCancelled {
		t.Fatalf("the settled run reads %q, want a killed process recorded as cancelled rather than judged", settled.Status)
	}
	if settled.CompletedAt == nil || !settled.CompletedAt.Equal(auditMoment) {
		t.Fatalf("the settled run completed at %v, want the moment of the audit", settled.CompletedAt)
	}
	// The audit's end is when it noticed, so the record keeps when the run last
	// moved: that is what the stall reading dates a dead line from.
	if settled.SettledQuietSince == nil || !settled.SettledQuietSince.Equal(auditMoment.Add(-9*time.Hour)) {
		t.Fatalf("the settled run was quiet since %v, want %s, when its record last moved", settled.SettledQuietSince, auditMoment.Add(-9*time.Hour))
	}
	if settled.Status.Terminal() != true || settled.Outcome() != runstate.OutcomeCancelled {
		t.Fatalf("the settled run reads as %q, want cancelled", settled.Outcome())
	}
	if !strings.Contains(settled.Failure, "stopped writing to its record") {
		t.Fatalf("the settled run says %q, want it to say the process stopped existing", settled.Failure)
	}
	if recorded.WorkItemTitle != "Recoverable failures retry with backoff" {
		t.Fatalf("title = %q, want the item's own title carried onto the record", recorded.WorkItemTitle)
	}
	if !recorded.ReleasedAt.Equal(auditMoment) {
		t.Fatalf("released at %s, want the moment of the audit", recorded.ReleasedAt)
	}
	if len(harness.log) != 1 {
		t.Fatalf("the release log holds %d record(s), want the release written down", len(harness.log))
	}
}

// A claim left by a run that ended in its own process needs no settling: its slot
// is already free, so the audit gives the claim back and writes nothing to the
// run.
func TestAClaimLeftByARunThatAlreadyEndedIsNotWrittenTo(t *testing.T) {
	t.Parallel()

	over := deadRun("run-over", "yoyodyne-ifd.9")
	completed := auditMoment.Add(-9 * time.Hour)
	over.Status = runstate.StatusFailed
	over.CompletedAt = &completed
	harness := newClaimHarness(over)
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Ended in its own process")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 1 {
		t.Fatalf("released = %+v, want the claim given back", sweep.Released)
	}
	if len(harness.saved) != 0 {
		t.Fatalf("saved = %+v, want a run that already ended left exactly as it is", harness.saved)
	}
	// And it is never taken up. A record that says a run ended cannot go back to
	// running, so it is decided from the reading rather than from a lease — which
	// is what keeps this whole case off however the store answers for a run nobody
	// holds.
	if len(harness.adopted) != 0 {
		t.Fatalf("adopted = %v, want a run that already ended never taken up at all", harness.adopted)
	}
}

// Both settlements against the real run store rather than a stand-in for it,
// because what each of them rests on is how that store actually behaves: one on
// a lease being takeable for a process that is gone, and the other on a record
// that ended needing no lease at all. A fake can be made to agree with either
// answer, which is exactly why neither is proven by one.
func TestBothDeathsAreSettledAgainstTheRealRunStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The killed process: a record still saying it is in flight, filling a slot,
	// with nothing written to it since the night before.
	killed := recordedRun(t, "yoyodyne-ifd.209.7", runstate.StatusRunning, auditMoment.Add(-9*time.Hour))
	// And the run that recorded its own ending and left the claim standing.
	ended := recordedRun(t, "yoyodyne-ifd.264", runstate.StatusFailed, auditMoment.Add(-9*time.Hour))
	for _, state := range []runstate.State{killed, ended} {
		if err := store.Create(state); err != nil {
			t.Fatalf("Create(%s) error = %v", state.RunID, err)
		}
	}
	// Both slots read as taken before the audit: the killed run because its record
	// says so, and that is the whole of what keeps the item from being pulled.
	if incomplete, err := store.Incomplete(); err != nil || len(incomplete) != 1 {
		t.Fatalf("Incomplete() = %d run(s), %v, want the killed run holding a slot", len(incomplete), err)
	}

	tracker := newClaimHarness()
	auditor := ClaimAuditor{
		Tracker:   tracker,
		Runs:      store,
		Releases:  &claimLog{},
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}
	sweep, err := auditor.Audit(context.Background(), []beads.WorkItem{
		claimedItem("yoyodyne-ifd.209.7", "Killed on the network"),
		claimedItem("yoyodyne-ifd.264", "Ended in its own process"),
	})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Problems) != 0 {
		t.Fatalf("problems = %v, want both claims settled against the real store", sweep.Problems)
	}
	if len(sweep.Released) != 2 {
		t.Fatalf("released = %+v, want both claims given back", sweep.Released)
	}
	// The killed run is ended, so its slot is free and the scheduler will pull the
	// item again.
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("Incomplete() = %+v, want the killed run settled and its slot free", incomplete)
	}
	settled, err := store.Load(killed.RunID)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", killed.RunID, err)
	}
	if settled.Outcome() != runstate.OutcomeCancelled {
		t.Fatalf("the killed run reads as %q, want cancelled", settled.Outcome())
	}
	// And the run that ended in its own process is left exactly as it was, ending
	// and all: there was nothing to settle, and rewriting it would replace the
	// account it made of itself.
	untouched, err := store.Load(ended.RunID)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", ended.RunID, err)
	}
	if untouched.Status != runstate.StatusFailed || !untouched.UpdatedAt.Equal(ended.UpdatedAt) {
		t.Fatalf("the ended run reads %+v, want the record it wrote for itself", untouched)
	}
}

// A run a session stopped for its redeploy, that no later session carried on,
// is settled like any other dead claim. The stop is a promise that the run will
// be continued, and the store refuses a terminal record that still carries one,
// so an ending saved with the stop on it was refused at every audit: the record
// went on saying the run was going, it kept its developer slot, and every item
// sharing a file with it was held back behind it for a week.
func TestARunStoppedForARedeployThatNobodyContinuedIsSettled(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	stopped := recordedRun(t, "yoyodyne-ifd.428.37", runstate.StatusRunning, auditMoment.Add(-9*time.Hour))
	stopped.Phase = runstate.PhaseDeveloping
	stopped.RedeployStop = &runstate.RedeployStop{
		At: stopped.UpdatedAt, Phase: runstate.PhaseDeveloping, BoundSeconds: 900, SessionID: "watch-before",
	}
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	auditor := ClaimAuditor{
		Tracker:   newClaimHarness(),
		Runs:      store,
		Releases:  &claimLog{},
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}
	sweep, err := auditor.Audit(context.Background(), []beads.WorkItem{
		claimedItem("yoyodyne-ifd.428.37", "Stopped for a redeploy and never continued"),
	})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Problems) != 0 || len(sweep.Released) != 1 {
		t.Fatalf("problems = %v, released = %+v, want the claim settled and given back", sweep.Problems, sweep.Released)
	}
	if incomplete, err := store.Incomplete(); err != nil || len(incomplete) != 0 {
		t.Fatalf("Incomplete() = %+v, %v, want the run settled and its slot free", incomplete, err)
	}
	settled, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Outcome() != runstate.OutcomeCancelled || settled.RedeployStop != nil {
		t.Fatalf("the run reads as %q with redeploy stop %+v, want it cancelled with no promise to continue it",
			settled.Outcome(), settled.RedeployStop)
	}
}

// recordedRun is a run the store will accept, quiet since the moment given.
func recordedRun(t *testing.T, workItemID string, status runstate.Status, last time.Time) runstate.State {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		StartedAt:     last.Add(-time.Hour),
		UpdatedAt:     last,
	}
	if status.Terminal() {
		completed := last
		state.CompletedAt = &completed
	}
	return state
}

// The lease is the authority on liveness, and the reading before it is a
// snapshot. A run a live process still holds keeps its claim and is said nothing
// about, because the timestamps that called it dead were wrong.
func TestAClaimWhoseRunIsStillHeldIsLeftToTheProcessHoldingIt(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-alive", "yoyodyne-ifd.9"))
	harness.held["run-alive"] = true
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Quiet but alive")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 || len(sweep.Problems) != 0 {
		t.Fatalf("sweep = %+v, want a held run left alone and nothing said", sweep)
	}
	if len(harness.Released) != 0 || len(harness.saved) != 0 {
		t.Fatal("a run a live process holds was written to or had its claim taken")
	}
}

// And a run that parked between the reading and the lease is owed what its record
// now says, not what the snapshot said. It is re-read under the lease for exactly
// this.
func TestARunThatParkedUnderTheLeaseKeepsItsClaim(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-parking", "yoyodyne-ifd.9"))
	parked := deadRun("run-parking", "yoyodyne-ifd.9")
	resets := auditMoment.Add(time.Hour)
	parked.UsageLimitResetsAt = &resets
	harness.parkOnAdopt["run-parking"] = parked
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Parked while we looked")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 || len(sweep.Problems) != 0 {
		t.Fatalf("sweep = %+v, want the parked run left alone", sweep)
	}
	if len(harness.saved) != 0 || len(harness.Released) != 0 {
		t.Fatal("a run that parked under the lease was ended or had its claim taken")
	}
}

// A run whose ending could not be recorded leaves the slot taken, so the claim is
// not given back either: an item back in the queue that nothing can pull is a fix
// that reads as one. The failure is named instead.
func TestARunThatCouldNotBeSettledLeavesTheClaimAloneAndIsNamed(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-stuck", "yoyodyne-ifd.9"))
	harness.failSave = errors.New("the run store is read-only")
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Unsettleable")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 {
		t.Fatalf("released = %+v, want no release over a slot that is still taken", sweep.Released)
	}
	if len(sweep.Problems) != 1 || !strings.Contains(sweep.Problems[0], "nothing will pull the item") {
		t.Fatalf("problems = %v, want the unsettled run named with what it costs", sweep.Problems)
	}
	if len(harness.Released) != 0 {
		t.Fatal("the claim was given back over a run whose slot is still held")
	}
}

// Once means once. The second audit sees an item the tracker still calls claimed
// — a tracker that has not caught up, or a release that came straight back — and
// says nothing, because a second record is a second message about one stuck item.
func TestAClaimIsGivenBackOnceEvenIfTheTrackerStillCallsItClaimed(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-211", "yoyodyne-ifd.211"))
	auditor := harness.auditor()
	claimed := []beads.WorkItem{claimedItem("yoyodyne-ifd.211", "The two-day one")}
	if _, err := auditor.Audit(context.Background(), claimed); err != nil {
		t.Fatalf("first Audit() error = %v", err)
	}
	sweep, err := auditor.Audit(context.Background(), claimed)
	if err != nil {
		t.Fatalf("second Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 {
		t.Fatalf("released = %+v, want the second audit to say nothing", sweep.Released)
	}
	if len(harness.Order) != 1 {
		t.Fatalf("the tracker was asked %d time(s), want the claim given back once", len(harness.Order))
	}
	if len(harness.log) != 1 {
		t.Fatalf("the release log holds %d record(s), want one", len(harness.log))
	}
}

// A fresh run that dies in its turn is a fresh thing to say. The dedup is against
// the run's own last word rather than against the item, so an item released in
// the morning and stuck again by the evening is reported both times.
func TestASecondDeathOfTheSameItemIsSaidAgain(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-first", "yoyodyne-ifd.9"))
	auditor := harness.auditor()
	claimed := []beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Twice unlucky")}
	if _, err := auditor.Audit(context.Background(), claimed); err != nil {
		t.Fatalf("first Audit() error = %v", err)
	}
	// A second run, started after the release and dead in its turn.
	second := deadRun("run-second", "yoyodyne-ifd.9")
	second.StartedAt = auditMoment.Add(-2 * time.Hour)
	second.UpdatedAt = auditMoment.Add(-90 * time.Minute)
	harness.runs = append(harness.runs, second)
	sweep, err := auditor.Audit(context.Background(), claimed)
	if err != nil {
		t.Fatalf("second Audit() error = %v", err)
	}
	if len(sweep.Released) != 1 || sweep.Released[0].RunID != "run-second" {
		t.Fatalf("released = %+v, want the second death said as its own", sweep.Released)
	}
}

// A claim the tracker will not give back is an item nothing will ever pull, which
// is exactly what must not go unsaid — and it must not take the rest of the sweep
// down with it, because the pass this matters in is the one where several claims
// died at once.
func TestAClaimTheTrackerRefusesIsNamedAndDoesNotStopTheSweep(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(
		deadRun("run-209-7", "yoyodyne-ifd.209.7"),
		deadRun("run-264", "yoyodyne-ifd.264"),
	)
	harness.FailRelease = errors.New("the tracker is locked")
	sweep, err := harness.auditor().Audit(context.Background(), []beads.WorkItem{
		claimedItem("yoyodyne-ifd.209.7", "The first of the pair"),
		claimedItem("yoyodyne-ifd.264", "The second of the pair"),
	})
	if err != nil {
		t.Fatalf("Audit() error = %v, want one refused claim reported rather than raised", err)
	}
	if len(sweep.Problems) != 2 {
		t.Fatalf("problems = %v, want both claims named", sweep.Problems)
	}
	for _, problem := range sweep.Problems {
		if !strings.Contains(problem, "nothing will pull it") {
			t.Fatalf("problem = %q, want it to say what the failure costs", problem)
		}
	}
	if len(harness.log) != 0 {
		t.Fatalf("the release log holds %d record(s), want nothing recorded for a release that did not happen", len(harness.log))
	}
}

// A release the tracker took and the log would not record is reported rather than
// swallowed: the item is back in the queue and nobody will be told why, which is
// a second run for it with nothing accounting for the first.
func TestAReleaseNobodyCouldRecordIsReported(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-9", "yoyodyne-ifd.9"))
	harness.failAppend = errors.New("the log is read-only")
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Unrecorded")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 {
		t.Fatalf("released = %+v, want a release nobody recorded reported rather than claimed", sweep.Released)
	}
	if len(sweep.Problems) != 1 || !strings.Contains(sweep.Problems[0], "nobody will be told") {
		t.Fatalf("problems = %v, want the unrecorded release named", sweep.Problems)
	}
}

// Nothing claimed is answered without reading anything at all, which is the
// ordinary case on every pull of a quiet product.
func TestAnAuditWithNothingClaimedReadsNothing(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness()
	harness.failRuns = errors.New("the run store would have been read")
	sweep, err := harness.auditor().Audit(context.Background(), nil)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 || len(sweep.Problems) != 0 {
		t.Fatalf("sweep = %+v, want nothing at all", sweep)
	}
}

// An auditor missing any of its three parts refuses rather than reporting a sweep
// it never made.
func TestAnAuditorWithoutItsPartsRefuses(t *testing.T) {
	t.Parallel()

	if _, err := (ClaimAuditor{}).Audit(context.Background(), []beads.WorkItem{claimedItem("yoyodyne-ifd.9", "x")}); err == nil {
		t.Fatal("Audit() error = nil, want a refusal from an auditor with nothing wired")
	}
}

// claimLog is the release log as a scheduler test needs it. It is its own type
// rather than another method on the harness because the harness already answers
// List for the tracker, and one type cannot answer it two ways.
type claimLog struct {
	mu      sync.Mutex
	records []runstate.ReleasedClaim
}

func (l *claimLog) Append(released runstate.ReleasedClaim) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, released)
	return nil
}

func (l *claimLog) List() ([]runstate.ReleasedClaim, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]runstate.ReleasedClaim(nil), l.records...), nil
}

// Recorded is every run this harness holds, in flight or finished.
func (h *scheduleHarness) Recorded() ([]runstate.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := make([]runstate.State, 0, len(h.inFlight)+len(h.finished))
	for _, state := range h.inFlight {
		states = append(states, state)
	}
	return append(states, h.finished...), nil
}

// AdoptRun hands over a run the way the store does, with the nil lease a released
// one is.
func (h *scheduleHarness) AdoptRun(_ context.Context, runID string) (runstate.State, *runstate.Lease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, state := range h.inFlight {
		if state.RunID == runID {
			return state, nil, nil
		}
	}
	return runstate.State{}, nil, runstate.ErrRunHeld
}

// Save records a run's ending. A terminal record leaves the in-flight set, which
// is the whole of what frees the developer slot it was filling.
func (h *scheduleHarness) Save(state runstate.State) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if state.Status.Terminal() {
		delete(h.inFlight, state.WorkItemID)
		h.finished = append(h.finished, state)
		return nil
	}
	h.inFlight[state.WorkItemID] = state
	return nil
}

// The whole of the fix in one pass, against the real audit rather than a stand-in
// for it: both developer slots are held by runs that died, the machine reads as
// full, and the item is claimed so nothing would pull it anyway. Nobody types
// anything. The pass audits the claims before it consults its own capacity,
// settles the dead runs, gives the items back, and starts one of them.
//
// This is the placement and the completeness together, because they only mean
// anything together: an audit the pass never reaches fixes nothing, and a release
// that leaves the run record in flight frees no slot.
func TestDeadRunsFillingTheMachineAreSettledAndTheirItemsPulledAgain(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 2
	harness.now = auditMoment
	harness.inFlight["yoyodyne-ifd.209.7"] = deadRun("run-209-7", "yoyodyne-ifd.209.7")
	harness.inFlight["yoyodyne-ifd.264"] = deadRun("run-264", "yoyodyne-ifd.264")
	harness.Items = []beads.WorkItem{
		claimedItem("yoyodyne-ifd.209.7", "The first of the pair"),
		claimedItem("yoyodyne-ifd.264", "The second of the pair"),
	}
	log := &claimLog{}
	harness.claims = ClaimAuditor{
		Tracker:   harness,
		Runs:      harness,
		Releases:  log,
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}

	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.ReleasedClaims) != 2 {
		t.Fatalf("released = %+v, want both dead claims given back", schedule.ReleasedClaims)
	}
	if schedule.ClaimProblem != "" {
		t.Fatalf("claim problem = %q, want none", schedule.ClaimProblem)
	}
	// The runs that were filling the machine are ended, so the slots are free.
	for _, state := range harness.finished {
		if state.Status != runstate.StatusCancelled {
			t.Fatalf("run %s ended %q, want a killed process recorded as cancelled", state.RunID, state.Status)
		}
	}
	if len(harness.inFlight) != 0 {
		t.Fatalf("%d run(s) are still in flight, want the dead ones settled", len(harness.inFlight))
	}
	// And the items are pulled again in the same pass, with nobody having typed
	// anything: this is what "released so the scheduler can retry it" has to mean.
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s) (%s), want both released items pulled again", len(schedule.Started), schedule.Render())
	}
	pulled := map[string]bool{}
	for _, started := range schedule.Started {
		pulled[started.WorkItemID] = true
	}
	for _, id := range []string{"yoyodyne-ifd.209.7", "yoyodyne-ifd.264"} {
		if !pulled[id] {
			t.Fatalf("%s was never pulled again: %s", id, schedule.Render())
		}
	}
	if len(log.records) != 2 {
		t.Fatalf("the release log holds %d record(s), want one per item given back", len(log.records))
	}
	if !strings.Contains(schedule.Render(), "given back to the queue") {
		t.Fatalf("the rendered pass does not say what it gave back:\n%s", schedule.Render())
	}
}

// The audit is asked before the intake hold short-circuits the pass, and that
// placement is deliberate: a held queue is often this session's own brake, placed
// exactly when runs are failing one after another, which is the pass a dead claim
// is most likely to have just appeared on. Giving a claim back chooses nothing
// and starts nothing, so a hold has no business suppressing it.
func TestAHeldIntakeStillAuditsTheClaims(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.held = &runstate.IntakeHold{HeldAt: auditMoment.Add(-time.Hour), Reason: "runs kept blocking"}
	harness.inFlight["yoyodyne-ifd.264"] = deadRun("run-264", "yoyodyne-ifd.264")
	harness.Items = []beads.WorkItem{claimedItem("yoyodyne-ifd.264", "Stuck while intake is held")}
	log := &claimLog{}
	harness.claims = ClaimAuditor{
		Tracker:   harness,
		Runs:      harness,
		Releases:  log,
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}

	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to have stopped the choosing", schedule.Stopped)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %d run(s), want a held intake to still choose nothing", len(schedule.Started))
	}
	if len(schedule.ReleasedClaims) != 1 {
		t.Fatalf("released = %+v, want the dead claim given back under a held intake", schedule.ReleasedClaims)
	}
}

// The audit is asked before the provider-outage wait short-circuits the pass
// too, and for the same reason. A login that expired is a wait this session sits
// in for as long as the login takes to renew, and a run killed before it expired
// is exactly as dead through it: the runs that are waiting hold their claims by
// their own pause, so the audit reads nothing wrong in them, and the one that
// was killed is given back rather than sitting under the wait until morning.
func TestAProviderAnsweringNobodyStillAuditsTheClaims(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	outages := newOutageStore(t)
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Waiting: "an earlier dispatch"}); err != nil {
		t.Fatal(err)
	}
	harness.outages, harness.provider = outages, &orchestratortest.LoginProbe{}
	harness.inFlight["yoyodyne-ifd.264"] = deadRun("run-264", "yoyodyne-ifd.264")
	harness.Items = []beads.WorkItem{claimedItem("yoyodyne-ifd.264", "Stuck while the provider is away")}
	log := &claimLog{}
	harness.claims = ClaimAuditor{
		Tracker:   harness,
		Runs:      harness,
		Releases:  log,
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}

	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleProviderAway {
		t.Fatalf("stopped = %q, want the outage to have stopped the choosing", schedule.Stopped)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %d run(s), want nothing dispatched into a provider answering nobody", len(schedule.Started))
	}
	if len(schedule.ReleasedClaims) != 1 {
		t.Fatalf("released = %+v, want the dead claim given back while the provider is away", schedule.ReleasedClaims)
	}
}

// The audit's own tracker reading is the one in this loop that is reported rather
// than retried, and a held intake is why. A hold is answered from a switch and
// needs no tracker at all, so a pass that routed the audit's read through the
// retry would stop answering "intake is held" on a machine whose tracker is down
// — which is a command an operator runs precisely when things are going wrong.
func TestATrackerThatWillNotAnswerDoesNotStopTheHoldBeingReported(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-alpha")...)
	harness.held = &runstate.IntakeHold{HeldAt: auditMoment.Add(-time.Hour), Reason: "the queue needs reordering first"}
	harness.failList = func(*scheduleHarness, int) error { return errors.New("no beads database found") }
	harness.claims = ClaimAuditor{
		Tracker:   harness,
		Runs:      harness,
		Releases:  &claimLog{},
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}

	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v, want the hold reported rather than the reading raised", err)
	}
	if schedule.Stopped != ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to be why the choosing stopped", schedule.Stopped)
	}
	if schedule.IntakeHeld == nil {
		t.Fatal("the hold was not named, so an operator reading this is told nothing they can act on")
	}
	if !strings.Contains(schedule.ClaimProblem, "could not be read") {
		t.Fatalf("claim problem = %q, want the unread claims said beside the hold", schedule.ClaimProblem)
	}
}

// An audit that fails costs the pass nothing it was doing. The line goes on
// choosing work and the failure is reported beside it, because a session that
// stopped pulling because it could not read a claim would be a worse failure than
// the stuck item it was looking for.
func TestAnAuditThatFailsDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-alpha")...)
	harness.audit = func(*scheduleHarness, []beads.WorkItem) (ClaimSweep, error) {
		return ClaimSweep{}, errors.New("the run store could not be read")
	}
	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d run(s), want the pass to have carried on choosing work", len(schedule.Started))
	}
	if !strings.Contains(schedule.ClaimProblem, "could not be audited") {
		t.Fatalf("claim problem = %q, want the failed audit reported beside the pass", schedule.ClaimProblem)
	}
}

// The default clock is the real one, so an auditor a caller wired without one
// still stamps its records rather than writing the zero time.
func TestAnAuditorWithoutAClockUsesTheWallClock(t *testing.T) {
	t.Parallel()

	auditor := ClaimAuditor{Tracker: &claimHarness{}, Runs: &claimHarness{}, Releases: &claimHarness{}, ProductID: "yoyodyne"}
	if _, ok := auditor.clock().(execution.RealClock); !ok {
		t.Fatalf("clock() = %T, want the wall clock", auditor.clock())
	}
}
