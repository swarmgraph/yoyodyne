package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A re-arm the development manager records about a merge the forge dropped is
// carried out by the watch's own pull, exactly as the verb would make it: the
// pull request the verdict authorized, by the method its own merge recorded,
// pinned to the promoted commit. Nobody types `yoyo triage rearm`, and the pull
// after it finds nothing left to carry out. yoyodyne-ifd.428.46.
func TestTheWatchRearmsADroppedMergeItsDecisionNames(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.decide(t)
	watch := harness.carryOut(nil)

	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil {
		t.Fatalf("CarryRearms() error = %v", err)
	}
	if len(carried) != 1 || !carried[0].Carried || carried[0].RunID != harness.state.RunID || carried[0].Decision != runstate.TriageDecisionRearm {
		t.Fatalf("carried = %+v, want the re-arm of the dropped merge carried out", carried)
	}
	want := publish.MergeRequest{Number: 92, HeadCommit: rearmedCommit, Method: publish.MergeMethod(rearmedMethod)}
	if len(harness.forge.Requested) != 1 || harness.forge.Requested[0] != want {
		t.Fatalf("merge requests = %#v, want the dropped request repeated as %#v", harness.forge.Requested, want)
	}
	if len(harness.leases.promoted) != 1 || harness.leases.promoted[0] != "main" {
		t.Fatalf("promotion leases = %#v, want the re-arm made under main's", harness.leases.promoted)
	}
	rearmed := harness.reload(t)
	if !rearmed.PullRequest.MergeQueued || rearmed.PullRequest.MergeRearms != 1 || rearmed.PublishFailure != "" {
		t.Fatalf("recorded publication = %+v (failure %q), want the merge queued again and the decision spent", rearmed.PullRequest, rearmed.PublishFailure)
	}

	again, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(again) != 0 || len(harness.forge.Requested) != 1 {
		t.Fatalf("a second pull carried %+v (%v) with requests %#v; want nothing left to carry out", again, err, harness.forge.Requested)
	}
}

// A re-arm of a dropped merge the forge still holds on a requirement only a
// person can meet is refused by the action's own gate, and the refusal is written
// onto the item naming it, then left to cool rather than asked every poll.
func TestTheWatchRecordsARefusedRearmOfADroppedMergeOnTheItem(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.forge.Status = "BLOCKED"
	harness.decide(t)
	watch := harness.carryOut(nil)

	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil {
		t.Fatalf("CarryRearms() error = %v", err)
	}
	if len(carried) != 1 || carried[0].Carried || !strings.Contains(carried[0].Problem, "only a person can satisfy") {
		t.Fatalf("carried = %+v, want the re-arm refused naming what holds the merge", carried)
	}
	if len(harness.forge.Requested) != 0 {
		t.Fatalf("a refused re-arm asked the forge to merge %#v", harness.forge.Requested)
	}
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	finding, found := counters.CarryOutOf(harness.state.RunID)
	if !found || finding.Decision != runstate.TriageDecisionRearm || finding.Waiting || !strings.Contains(finding.Refusal, "only a person can satisfy") {
		t.Fatalf("finding = %+v (found %v), want the refusal on the item's triage record", finding, found)
	}
	if again, _ := watch.CarryRearms(context.Background(), false); len(again) != 0 {
		t.Fatalf("the next pull attempted %+v; want the refusal left to cool", again)
	}
}

// The intake hold and the operator's pause stop a re-arm as they stop a repair or
// a re-run: nothing is asked of the forge, the item says once what the decision is
// waiting on rather than on every poll, and the first pull after the switch opens
// carries the decision out.
func TestASwitchShutForEverythingIsWrittenOnceOnARearmAndTheFirstPullAfterFiresIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		intakeHeld bool
		paused     bool
		gate       string
	}{
		{name: "intake hold", intakeHeld: true, gate: runstate.TriageGateIntakeHold},
		{name: "operator pause", paused: true, gate: runstate.TriageGateSpendingPause},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newRearmHarness(t)
			harness.decide(t)
			watch := harness.carryOut(nil)
			watch.Holds = pausedHolds{held: test.paused, hold: runstate.OperatorHold{HeldAt: docketedNow}}

			held, err := watch.CarryRearms(context.Background(), test.intakeHeld)
			if err != nil {
				t.Fatalf("CarryRearms() under the switch error = %v", err)
			}
			if len(held) != 1 || held[0].Carried || !held[0].Waiting || held[0].Gate != test.gate {
				t.Fatalf("held = %+v, want the re-arm said to wait on %s", held, test.gate)
			}
			if len(harness.forge.Requested) != 0 {
				t.Fatalf("a re-arm under %s asked the forge for %#v", test.gate, harness.forge.Requested)
			}
			counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			finding, found := counters.CarryOutOf(harness.state.RunID)
			if !found || !finding.Waiting || finding.Gate != test.gate || finding.Attempts != 1 {
				t.Fatalf("finding = %+v (found %v), want one waiting record naming %s", finding, found, test.gate)
			}

			if again, _ := watch.CarryRearms(context.Background(), test.intakeHeld); len(again) != 0 {
				t.Fatalf("a second pull under the same switch attempted %+v; want the record left standing", again)
			}

			watch.Holds = pausedHolds{}
			carried, err := watch.CarryRearms(context.Background(), false)
			if err != nil {
				t.Fatalf("CarryRearms() after the switch opened error = %v", err)
			}
			if len(carried) != 1 || !carried[0].Carried || len(harness.forge.Requested) != 1 {
				t.Fatalf("carried = %+v with requests %#v, want the re-arm made on the first pull after the switch opened", carried, harness.forge.Requested)
			}
		})
	}
}

// A re-arm recorded about a run that stopped before it promoted — its target had
// diverged — against a publication whose docket entry an earlier wait closed is
// refused aloud at the next pull rather than passed over: the refusal is on the
// item's triage record, names the missing promotion and the re-run that applies,
// and the entry comes back onto the docket the development manager reads. That
// is the re-arm of the supervisor's periodic pass (yoyodyne-ifd.413), which no
// pull attempted for a day and nothing recorded (yoyodyne-edi).
func TestARearmOfAPublicationItsRunNeverPromotedIsRefusedAloudAtTheNextPull(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	unpromoted := harness.state
	published := *unpromoted.PullRequest
	published.MergeMethod = ""
	unpromoted.PullRequest = &published
	unpromoted.Integration = nil
	unpromoted.PublishFailure = ""
	unpromoted.Blocker = "Yoyodyne stopped this item: its target branch and the one on the remote have diverged, so the change was never promoted."
	if err := harness.runs.Save(unpromoted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	harness.docket.waitOn(harness.publication(), docketedNow, docketedNow.Add(time.Hour))
	harness.decide(t)
	harness.docket.close(harness.publication(), runstate.TriageDecisionRearm, docketedNow)
	watch := harness.carryOut(nil)
	watch.Clock = laterClock{after: time.Minute}

	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil {
		t.Fatalf("CarryRearms() error = %v", err)
	}
	if len(carried) != 1 || carried[0].Carried || carried[0].Decision != runstate.TriageDecisionRearm ||
		!strings.Contains(carried[0].Problem, "recorded no promotion") || !strings.Contains(carried[0].Problem, "re-run") {
		t.Fatalf("carried = %+v, want the re-arm refused naming the missing promotion and the re-run that applies", carried)
	}
	if len(harness.forge.Requested) != 0 || len(harness.leases.promoted) != 0 {
		t.Fatalf("a refused re-arm asked the forge for %#v under leases %#v", harness.forge.Requested, harness.leases.promoted)
	}
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	finding, found := counters.CarryOutOf(harness.state.RunID)
	if !found || finding.Waiting || finding.Gate != runstate.TriageGateHarness || !strings.Contains(finding.Refusal, "recorded no promotion") {
		t.Fatalf("finding = %+v (found %v), want the refusal on the item's triage record", finding, found)
	}

	docketed := refusedOnTheDocket(t, harness)
	if !docketed.Critical() || !strings.Contains(docketed.CarryOut.Refusal, "recorded no promotion") {
		t.Fatalf("docketed = %+v, want the refused re-arm urgent on the docket", docketed)
	}
}

// Once the harness has refused a re-arm it cannot make, nothing anybody reads
// about the item says the harness is carrying it out. The decision was recorded
// a day before the pull that refuses it, against a run that never promoted —
// the supervisor's periodic pass (yoyodyne-ifd.413), whose re-arm was refused
// onto its record from 2026-09-29 while its held line and its docket entry both
// went on naming the harness, so nobody was placed to record the re-run it
// needed (yoyodyne-8ff). The refusal is on the item, the docket entry is live
// and names her as the next mover, and the held line carries the refusal and is
// hers rather than the harness's.
func TestARefusedRearmIsHeldAsTheDevelopmentManagersMoveWithTheRefusalSaid(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	unpromoted := harness.state
	published := *unpromoted.PullRequest
	published.MergeMethod = ""
	unpromoted.PullRequest = &published
	unpromoted.Integration = nil
	unpromoted.PublishFailure = ""
	unpromoted.Blocker = "Yoyodyne stopped this item: its target branch and the one on the remote have diverged, so the change was never promoted."
	if err := harness.runs.Save(unpromoted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	harness.decide(t)
	harness.docket.close(harness.publication(), runstate.TriageDecisionRearm, docketedNow)

	// Before any pull, the decision is the harness's to carry out, and every
	// reading says so.
	before, err := readmodel.HeldForAPerson(context.Background(), harness.runs, harness.runs.Triage(), nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() before the pull error = %v", err)
	}
	if !before.Decided(harness.state.WorkItemID) {
		reason, _ := before.Reason(harness.state.WorkItemID)
		t.Fatalf("before any pull the hold reads %q, want it the harness's carry-out", reason)
	}

	watch := harness.carryOut(nil)
	watch.Clock = laterClock{after: 24 * time.Hour}
	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(carried) != 1 || carried[0].Carried || !strings.Contains(carried[0].Problem, "recorded no promotion") {
		t.Fatalf("CarryRearms() = %+v, %v; want the day-old re-arm refused for the missing promotion", carried, err)
	}
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if _, refused := counters.RefusedCarryOut(harness.state.RunID); !refused || counters.AwaitingCarryOut(harness.state.RunID) {
		t.Fatalf("carry-outs = %+v; want the refusal standing and nothing left for the harness to carry out", counters.CarryOuts)
	}

	docketed := refusedOnTheDocket(t, harness)
	rendered := docketed.Render()
	if docketed.Counters.AwaitingCarryOut() || !docketed.Counters.Standing.Refused ||
		!strings.Contains(rendered, "Next mover: you") || strings.Contains(rendered, "Next mover: the harness") {
		t.Fatalf("docketed entry awaiting=%v renders:\n%s\nwant it on the docket naming the development manager", docketed.Counters.AwaitingCarryOut(), rendered)
	}

	held, err := readmodel.HeldForAPerson(context.Background(), harness.runs, harness.runs.Triage(), nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	reason, holding := held.Reason(harness.state.WorkItemID)
	if !holding || held.Decided(harness.state.WorkItemID) {
		t.Fatalf("the hold reads %q (holding %v, decided %v), want it held as the development manager's move", reason, holding, held.Decided(harness.state.WorkItemID))
	}
	if !strings.Contains(reason, "recorded no promotion") || !strings.Contains(reason, "re-run") ||
		!strings.Contains(reason, "hers to move rather than the harness's") || strings.Contains(reason, "outstanding is the harness carrying that decision out") {
		t.Fatalf("the hold reads %q, want the refusal said and the development manager named", reason)
	}
}

// A re-arm the forge refuses — the maintenance-duties item's merge (yoyodyne-ifd.434.10)
// conflicting with its base — is refused onto the item, and the refusal reaches
// the docket the development manager reads rather than being dropped with the
// entry the decision settled. Once a later pull makes the merge request, the
// finding is taken back and the entry leaves the docket again.
func TestARefusedRearmReachesTheDocketAndLeavesItOnceTheMergeIsMade(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	harness.forge.Status = "DIRTY"
	harness.decide(t)
	harness.docket.close(harness.publication(), runstate.TriageDecisionRearm, docketedNow)
	watch := harness.carryOut(nil)
	watch.Clock = laterClock{after: time.Minute}

	refused, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(refused) != 1 || refused[0].Carried || len(harness.forge.Requested) != 0 {
		t.Fatalf("CarryRearms() = %+v, %v with requests %#v; want the re-arm refused and nothing asked", refused, err, harness.forge.Requested)
	}
	if docketed := refusedOnTheDocket(t, harness); !strings.Contains(docketed.CarryOut.Refusal, "only a person can satisfy") {
		t.Fatalf("docketed = %+v, want the forge's refusal on the entry", docketed.CarryOut)
	}

	harness.forge.Status = "CLEAN"
	watch.Clock = laterClock{after: time.Minute + runstate.TriageCarryOutRetryDelay + time.Second}
	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(carried) != 1 || !carried[0].Carried || carried[0].RecordProblem != "" || len(harness.forge.Requested) != 1 {
		t.Fatalf("CarryRearms() once the refusal cooled = %+v, %v with requests %#v; want the merge made and the record clean", carried, err, harness.forge.Requested)
	}
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if finding, found := counters.CarryOutOf(harness.state.RunID); found {
		t.Fatalf("finding = %+v still stands over a re-arm that was made", finding)
	}
	build, _ := docketerOverStore(harness.docket, harness.runs, rearmConfig()).Build()
	for _, entry := range build.Entries {
		if entry.RunID == harness.state.RunID && entry.CarryOut != nil {
			t.Fatalf("entry %s still carries %+v after the re-arm was made", entry.Key, entry.CarryOut)
		}
	}
}

// refusedOnTheDocket is the entry the docket build shows for the harness's run
// carrying a refused carry-out, failing the test where there is none.
func refusedOnTheDocket(t *testing.T, harness *rearmHarness) triage.Entry {
	t.Helper()
	docketer := docketerOverStore(harness.docket, harness.runs, rearmConfig())
	docketer.Clock = laterClock{after: 2 * time.Minute}
	build, err := docketer.Build()
	for _, entry := range build.Entries {
		for _, candidate := range append([]triage.Entry{entry}, entry.Earlier...) {
			if candidate.RunID == harness.state.RunID && candidate.CarryOut != nil {
				return candidate
			}
		}
	}
	t.Fatalf("the docket build (%v) shows %d entries and none carries the refused re-arm of run %s", err, len(build.Entries), harness.state.RunID)
	return triage.Entry{}
}

// A re-arm held back because a run of its item is in flight is attempted by no
// pass, so it is written onto the item as unattempted once it has stood a poll
// interval, naming the run it waits on.
func TestARearmHeldBehindARunInFlightIsRecordedAsUnattempted(t *testing.T) {
	t.Parallel()

	harness := newRearmHarness(t)
	live := droppedPublication()
	live.RunID = "run-aaaabbbbccccddddeeeeffff00001111"
	live.Status = runstate.StatusRunning
	live.CompletedAt = nil
	live.Blocker = ""
	live.PublishFailure = ""
	live.Integration = nil
	live.PullRequest = nil
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	harness.decide(t)
	watch := harness.carryOut(nil)

	carried, err := watch.CarryRearms(context.Background(), false)
	if err != nil || len(carried) != 0 || len(harness.forge.Requested) != 0 {
		t.Fatalf("CarryRearms() = %+v, %v with requests %#v; want nothing attempted beside a run in flight", carried, err, harness.forge.Requested)
	}

	watch.Clock = laterClock{after: 2 * time.Minute}
	written, err := watch.RecordUnattempted(context.Background(), time.Minute, nil)
	if err != nil {
		t.Fatalf("RecordUnattempted() error = %v", err)
	}
	if len(written) != 1 || written[0].Decision != runstate.TriageDecisionRearm || !strings.Contains(written[0].Problem, live.RunID) {
		t.Fatalf("written = %+v, want the re-arm recorded as unattempted naming run %s", written, live.RunID)
	}
	counters, err := harness.runs.Triage().Counters(harness.state.WorkItemID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	finding, found := counters.CarryOutOf(harness.state.RunID)
	if !found || !finding.Unattempted || finding.Gate != runstate.TriageGateWorkItem {
		t.Fatalf("finding = %+v (found %v), want the unattempted re-arm on the item's triage record", finding, found)
	}
}
