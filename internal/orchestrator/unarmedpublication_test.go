package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// checksStub is the forge's reading of a request's checks, which gates arming a
// request nothing ever asked the forge to merge.
type checksStub struct {
	reading publish.CheckReading
	err     error
	asked   int
}

func (c *checksStub) Checks(context.Context, int, string) (publish.CheckReading, error) {
	c.asked++
	return c.reading, c.err
}

// unarmedPublicationRun is a finished, approved run whose promotion holds its pull
// request and whose record says nothing ever asked the forge to merge it: no
// merge queued, none dropped, no method recorded, and no account of anything
// having gone wrong. It ended a minute before the docket is built, well inside
// the stuck-merge age, because what puts it on the docket is not its age.
func unarmedPublicationRun() runstate.State {
	state := droppedPublication()
	completed := docketedNow.Add(-time.Minute)
	state.Status = runstate.StatusSucceeded
	state.UpdatedAt = completed
	state.CompletedAt = &completed
	state.PublishFailure = ""
	state.Blocker = ""
	state.MergeDrop = nil
	state.PullRequest.MergeMethod = ""
	return state
}

// newUnarmedHarness records one unarmed publication and builds the docket over
// it the way every sweep does, rather than writing the entry by hand: the docket
// finding it is half of what is under test.
func newUnarmedHarness(t *testing.T) (*rearmHarness, *checksStub, DocketBuild) {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := unarmedPublicationRun()
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !state.PublicationUnarmed() {
		t.Fatalf("the fixture is not a publication nothing asked the forge to merge: %+v", state)
	}
	docket := &memoryDocket{}
	build, err := docketerOverStore(docket, runs, rearmConfig()).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	harness := &rearmHarness{
		docket: docket,
		runs:   runs,
		leases: &leasedRuns{Store: runs},
		forge: &orchestratortest.RearmForge{
			Observed: publish.PullRequest{Number: state.PullRequest.Number, URL: state.PullRequest.URL, State: "OPEN", HeadCommit: rearmedCommit},
			Status:   "CLEAN",
			Result:   publish.MergeResult{Queued: true},
		},
		worktrees: &orchestratortest.RemoteTarget{},
		state:     state,
	}
	checks := &checksStub{reading: publish.CheckReading{HeadCommit: rearmedCommit, Passing: 4}}
	return harness, checks, build
}

func (h *rearmHarness) armer(checks RearmChecks) Rearmer {
	rearmer := h.rearmer()
	rearmer.Checks = checks
	return rearmer
}

// A publication nothing ever asked the forge to merge is put to the development
// manager at once, and her re-arm decision is carried out by the harness as the
// merge request the run's own merge would have made — the same method, pinned to
// the promoted commit, after the same pre-merge check, under the target branch's
// promotion lease — and nothing about it is left for a hand merge on the forge.
func TestAPublicationNothingAskedTheForgeToMergeIsDocketedAndArmedByTheHarness(t *testing.T) {
	t.Parallel()

	harness, checks, build := newUnarmedHarness(t)

	if len(build.Entries) != 1 {
		t.Fatalf("docket = %+v, want the one unarmed publication on it", build.Entries)
	}
	entry := build.Entries[0]
	if entry.Class != triage.ClassPublication || entry.Key != harness.publication() || entry.RunID != harness.state.RunID {
		t.Fatalf("entry = %+v, want the publication keyed to run %s and pull request 92", entry, harness.state.RunID)
	}
	if entry.Publication == nil || !strings.Contains(entry.Publication.Message, "nothing ever asked the forge to merge pull request 92 into main") ||
		!strings.Contains(entry.Publication.Message, "a re-run hands the change back for a fresh run") {
		t.Fatalf("entry publication = %+v, want the account naming the two decisions", entry.Publication)
	}

	harness.decide(t)
	result, err := harness.armer(checks).Rearm(context.Background(), RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
	if err != nil {
		t.Fatalf("Rearm() error = %v", err)
	}
	want := publish.MergeRequest{Number: 92, HeadCommit: rearmedCommit, Method: mergeMethod}
	if len(harness.forge.Requested) != 1 || harness.forge.Requested[0] != want {
		t.Fatalf("merge requests = %#v, want the run's own merge request %#v", harness.forge.Requested, want)
	}
	if checks.asked != 1 || len(harness.worktrees.Verified) != 1 || len(harness.leases.promoted) != 1 {
		t.Fatalf("checks read %d, remote target verified %d, leases %v; want each once before the merge", checks.asked, len(harness.worktrees.Verified), harness.leases.promoted)
	}
	if !result.FirstArm || !result.Rearmed || result.Method != string(mergeMethod) {
		t.Fatalf("result = %+v, want a first arming by the run's own method", result)
	}
	for _, said := range []string{result.Reason, result.Render()} {
		if !strings.Contains(said, "nothing had") {
			t.Fatalf("account %q does not say nothing had asked the forge before", said)
		}
	}
	armed := harness.reload(t)
	if !armed.PullRequest.MergeQueued || armed.PullRequest.MergeMethod != string(mergeMethod) || armed.PullRequest.MergeRearms != 1 {
		t.Fatalf("recorded publication = %+v, want the merge queued, its method recorded, and the decision spent", armed.PullRequest)
	}
	if armed.PublicationUnarmed() {
		t.Fatal("the armed publication still reads as one nothing asked the forge to merge")
	}
}

// The arming is refused where the request's head is behind its target or its
// checks fail, naming the gate, and refused before anything is spent.
func TestArmingAnUnaskedPublicationIsRefusedAtTheLandingGates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		reading publish.CheckReading
		want    string
	}{
		{name: "head behind target", reading: publish.CheckReading{HeadCommit: rearmedCommit, BehindBy: 3}, want: "head-behind-target gate: pull request 92's head is 3 commit(s) behind main"},
		{name: "failing check", reading: publish.CheckReading{HeadCommit: rearmedCommit, Failing: []publish.FailedCheck{{Name: "make race"}}}, want: "checks gate: pull request 92 has 1 failing check(s) on its head (make race)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness, checks, _ := newUnarmedHarness(t)
			checks.reading = test.reading
			harness.decide(t)
			_, err := harness.armer(checks).Rearm(context.Background(), RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Rearm() error = %v, want it refused naming %q", err, test.want)
			}
			if len(harness.forge.Requested) != 0 {
				t.Fatalf("a refused arming asked the forge for %#v", harness.forge.Requested)
			}
			if left := harness.reload(t); left.PullRequest.MergeRearms != 0 || !left.PublicationUnarmed() {
				t.Fatalf("a refused arming changed the record: %+v", left.PullRequest)
			}
		})
	}

	// A harness that cannot read the checks does not arm unchecked.
	harness, _, _ := newUnarmedHarness(t)
	harness.decide(t)
	if _, err := harness.armer(nil).Rearm(context.Background(), RearmRequest{Run: harness.state.RunID, Reason: rearmReasoning}); err == nil ||
		!strings.Contains(err.Error(), "armed only on a reading of them") {
		t.Fatalf("Rearm() without a checks reading error = %v, want it refused", err)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("an unchecked arming asked the forge for %#v", harness.forge.Requested)
	}
}

// The other decision is a re-run, which hands the change back for a fresh run.
// The watch carries it out off the publication's own entry, and once it has, the
// publication stops standing: its record says it was handed back, a docket built
// afresh over the records puts it to nobody, the status line no longer names it
// as waiting on her decision, and the heartbeat no longer counts it as awaiting
// the forge.
func TestTheWatchHandsAnUnaskedPublicationBackForAFreshRun(t *testing.T) {
	t.Parallel()

	armed, _, _ := newUnarmedHarness(t)
	root := t.TempDir()
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	recordRerunDecision(t, armed.runs, armed.state.WorkItemID, armed.state.RunID)
	rerun := &rerunHarness{
		docket:   armed.docket,
		runs:     armed.runs,
		intake:   intake,
		reruns:   armed.runs.Reruns(),
		item:     beads.WorkItem{ID: armed.state.WorkItemID, Title: armed.state.WorkItemTitle, Status: "open"},
		capacity: 2,
		outcome:  Outcome{RunID: "run-fedcba9876543210fedcba9876543210", WorkItemID: armed.state.WorkItemID, Status: runstate.StatusSucceeded},
	}
	watch := CarryOut{
		Docket:    armed.docket,
		Decisions: armed.runs.Triage(),
		Reruns:    armed.runs.Reruns(),
		Runs:      armed.runs,
		Rerunner:  rerun.rerunner(),
		Clock:     docketClock{},
	}

	tasks, err := watch.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].RunID != armed.state.RunID || tasks[0].Decision != runstate.TriageDecisionRerun || tasks[0].DocketKey != armed.publication() {
		t.Fatalf("outstanding = %+v, want the re-run offered off the publication's entry", tasks)
	}
	carried, _, err := watch.Carry(context.Background(), tasks[0])
	if err != nil || !carried.Carried {
		t.Fatalf("Carry() = %+v, %v; want the re-run carried out", carried, err)
	}
	if len(rerun.started) != 1 || rerun.started[0].workItemID != armed.state.WorkItemID {
		t.Fatalf("started = %#v, want one fresh run of the item", rerun.started)
	}

	handedBack := armed.reload(t)
	if handedBack.PullRequest.HandedBack == nil || handedBack.PullRequest.HandedBack.DocketKey != armed.publication() {
		t.Fatalf("recorded publication = %+v, want it marked handed back against its entry", handedBack.PullRequest)
	}
	if handedBack.PublicationUnasked() || handedBack.AwaitingForge() {
		t.Fatal("the handed-back publication still reads as unasked or as awaiting the forge")
	}
	if stuckPublication(handedBack, docketedNow.Add(30*24*time.Hour), docketedTriage.StuckMergeAge.Duration()) {
		t.Fatal("the handed-back publication would be docketed again on its age")
	}
	rebuilt, err := docketerOverStore(&memoryDocket{}, armed.runs, rearmConfig()).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(rebuilt.Entries) != 0 {
		t.Fatalf("a docket built afresh = %+v, want the handed-back publication put to nobody", rebuilt.Entries)
	}
	if awaiting := readmodel.AwaitingForge([]runstate.State{handedBack}); len(awaiting) != 0 {
		t.Fatalf("awaiting the forge = %+v, want the handed-back publication off the needs-a-human line", awaiting)
	}
	if again, err := watch.Outstanding(); err != nil || len(again) != 0 {
		t.Fatalf("a later pull offers %+v (%v), want nothing left to carry out", again, err)
	}
}

// A request the forge has closed has nothing left to arm: it is still put to the
// development manager, for a re-run, and neither the watch nor the verb arms it.
func TestAClosedUnaskedPublicationIsOfferedOnlyTheRerun(t *testing.T) {
	t.Parallel()

	closed := unarmedPublicationRun()
	closed.PullRequest.State = "CLOSED"
	if closed.PublicationUnarmed() || !closed.PublicationUnasked() {
		t.Fatalf("a closed request reads unarmed %v, unasked %v; want only the second", closed.PublicationUnarmed(), closed.PublicationUnasked())
	}
	if !stuckPublication(closed, docketedNow, docketedTriage.StuckMergeAge.Duration()) {
		t.Fatal("a closed request nothing asked the forge to merge is not docketed")
	}
	if message := publicationMessage(closed); !strings.Contains(message, "closed it unmerged") || strings.Contains(message, "re-arm") {
		t.Fatalf("docket message = %q, want the re-run alone offered", message)
	}
	if err := stoppageIsOver(closed, triage.Found{}); err != nil {
		t.Fatalf("stoppageIsOver() = %v, want a closed request admitted to a re-run", err)
	}
	if _, _, err := rearmablePublication(closed); err == nil {
		t.Fatal("a closed request with no merge method is rearmable")
	}
}

// carryOut is the watch's carry-out over the harness's records, with the same
// Rearmer the verb makes the request through.
func (h *rearmHarness) carryOut(checks RearmChecks) CarryOut {
	return CarryOut{
		Docket:    h.docket,
		Decisions: h.runs.Triage(),
		Reruns:    h.runs.Reruns(),
		Runs:      h.runs,
		Rearmer:   h.armer(checks),
		Clock:     docketClock{},
	}
}

// The re-arm she records is carried out by the watch on its own pull, with
// nobody typing `yoyo triage rearm`: the merge request is made once, and a pull
// after it finds nothing left to carry out. Under the intake hold nothing is
// asked of the forge, and the item says the hold is what the decision waits on.
func TestTheWatchArmsAnUnaskedPublicationItsDecisionNames(t *testing.T) {
	t.Parallel()

	harness, checks, _ := newUnarmedHarness(t)
	harness.decide(t)
	watch := harness.carryOut(checks)

	held, err := watch.CarryRearms(context.Background(), true)
	if err != nil || len(held) != 1 || held[0].Carried || held[0].Gate != runstate.TriageGateIntakeHold || len(harness.forge.Requested) != 0 {
		t.Fatalf("CarryRearms() under the intake hold = %+v, %v with requests %#v; want nothing asked of the forge and the hold named", held, err, harness.forge.Requested)
	}

	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil {
		t.Fatalf("CarryRearms() error = %v", err)
	}
	if len(carried) != 1 || !carried[0].Carried || carried[0].RunID != harness.state.RunID || carried[0].Decision != runstate.TriageDecisionRearm {
		t.Fatalf("carried = %+v, want the one re-arm carried out", carried)
	}
	want := publish.MergeRequest{Number: 92, HeadCommit: rearmedCommit, Method: mergeMethod}
	if len(harness.forge.Requested) != 1 || harness.forge.Requested[0] != want {
		t.Fatalf("merge requests = %#v, want the run's own merge request %#v", harness.forge.Requested, want)
	}
	if armed := harness.reload(t); !armed.PullRequest.MergeQueued || armed.PullRequest.MergeRearms != 1 {
		t.Fatalf("recorded publication = %+v, want the merge queued and the decision spent", armed.PullRequest)
	}

	again, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(again) != 0 || len(harness.forge.Requested) != 1 {
		t.Fatalf("a second pull carried %+v (%v) with requests %#v; want nothing left to carry out", again, err, harness.forge.Requested)
	}
}

// A re-arm the landing gates refuse is written onto the item where the
// development manager reads it, naming the gate, and is not asked again on the
// very next pull.
func TestTheWatchRecordsARefusedArmingOnTheItem(t *testing.T) {
	t.Parallel()

	harness, checks, _ := newUnarmedHarness(t)
	checks.reading = publish.CheckReading{HeadCommit: rearmedCommit, BehindBy: 2}
	harness.decide(t)
	watch := harness.carryOut(checks)

	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil {
		t.Fatalf("CarryRearms() error = %v", err)
	}
	if len(carried) != 1 || carried[0].Carried || !strings.Contains(carried[0].Problem, "head-behind-target gate") {
		t.Fatalf("carried = %+v, want the arming refused naming the gate", carried)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("a refused arming asked the forge for %#v", harness.forge.Requested)
	}
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	finding, found := counters.CarryOutOf(harness.state.RunID)
	if !found || finding.Decision != runstate.TriageDecisionRearm || !strings.Contains(finding.Refusal, "head-behind-target gate") {
		t.Fatalf("finding = %+v (found %v), want the refusal on the item's triage record", finding, found)
	}
	if again, _ := watch.CarryRearms(context.Background(), false); len(again) != 0 || checks.asked != 1 {
		t.Fatalf("the next pull attempted %+v (checks read %d times); want the refusal left to cool", again, checks.asked)
	}
}

// Handing the change back leaves the old request open only until the fresh run
// lands. Once it has, that request carries work that reached the target branch
// by another vehicle, so the re-run closes it in that vehicle's name and records
// the supersession beside the hand-back — the case whose request nothing closed
// before yoyodyne-ifd.69.
func TestARerunClosesTheHandedBackPublicationOnceTheFreshRunLands(t *testing.T) {
	t.Parallel()

	armed, _, _ := newUnarmedHarness(t)
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	recordRerunDecision(t, armed.runs, armed.state.WorkItemID, armed.state.RunID)
	rerun := &rerunHarness{
		docket:   armed.docket,
		runs:     armed.runs,
		intake:   intake,
		reruns:   armed.runs.Reruns(),
		item:     beads.WorkItem{ID: armed.state.WorkItemID, Title: armed.state.WorkItemTitle, Status: "open"},
		capacity: 2,
		outcome:  Outcome{RunID: "run-fedcba9876543210fedcba9876543210", WorkItemID: armed.state.WorkItemID, Status: runstate.StatusSucceeded},
	}
	rerun.integrated()
	rerun.merged(445)

	result, err := rerun.rerunner().Rerun(context.Background(), RerunRequest{Run: armed.state.RunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if len(rerun.closed) != 1 || rerun.closed[0].Number != armed.state.PullRequest.Number {
		t.Fatalf("closed = %#v, want the handed-back request %d closed once", rerun.closed, armed.state.PullRequest.Number)
	}
	if !strings.Contains(rerun.closed[0].Comment, "#445") || !strings.Contains(rerun.closed[0].Comment, "handed back for a fresh run") {
		t.Errorf("close comment = %q, want the fresh run's pull request named and the hand-back said", rerun.closed[0].Comment)
	}
	if result.Publication == nil || !result.Publication.Closed {
		t.Errorf("publication = %#v, want the close reported", result.Publication)
	}
	retired := armed.reload(t)
	if retired.PullRequest.HandedBack == nil || !strings.Contains(retired.PullRequest.Superseded, "#445") {
		t.Fatalf("recorded publication = %+v, want it handed back and superseded by pull request 445", retired.PullRequest)
	}
	if retirablePublication(retired) {
		t.Error("the retired publication still reads as one a sweep should close")
	}
}
