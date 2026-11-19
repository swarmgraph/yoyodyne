package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A document's publication run stands as its own work item, so the tracker
// holds nothing under its identifier. A re-run the development manager decided
// about such a run, and then made pointless by retiring or retrying the
// document, was offered by every pass: the check that a recovery no longer
// applies asked the tracker, learned nothing, and the scheduler took a
// developer slot to carry the re-run out, which the action refused on the same
// missing item. These cover that run being set aside on its own record, its
// slot left free, and a re-run that still has work to do still offered.

// noSuchItem is the tracker as it answers about a document's publication run:
// it holds no item of that name. It counts every question so a test can show
// none was asked.
type noSuchItem struct {
	shown, noted int
}

func (n *noSuchItem) Show(_ context.Context, id string) (beads.WorkItem, error) {
	n.shown++
	return beads.WorkItem{}, errors.New(`bd show failed with status failed and exit code 1: Error fetching ` + id + `: no issue found matching "` + id + `"`)
}

func (n *noSuchItem) RecordOutcome(context.Context, string, string) (beads.WorkItem, error) {
	n.noted++
	return beads.WorkItem{}, errors.New("no issue found")
}

// countingRerunner is a re-run action that records being asked.
type countingRerunner struct{ asked []string }

func (r *countingRerunner) Rerun(_ context.Context, request RerunRequest) (RerunResult, error) {
	r.asked = append(r.asked, request.Run)
	return RerunResult{}, nil
}

// documentRecoveryFixture is a stopped document publication on the
// development manager's docket with a re-run of it decided, the shape the
// artifact contract's publication was left in on October 12.
type documentRecoveryFixture struct {
	runs     *runstate.Store
	docket   CarryOutDocket
	last     runstate.State
	decider  DocumentDecider
	tracker  *noSuchItem
	rerunner *countingRerunner
}

func newDocumentRecoveryFixture(t *testing.T, rerunAt time.Time) documentRecoveryFixture {
	t.Helper()
	_, docketer, forge, runs, last := stoppedPublication(t)
	if last.WorkItemID != last.RunID {
		t.Fatalf("the publication run stands for item %q, want its own run %q", last.WorkItemID, last.RunID)
	}
	if _, err := docketer.Build(); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Triage().RecordRerun(context.Background(), last.WorkItemID, triageDecided(runstate.TriageDecisionRerun, last.RunID), rerunAt, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	return documentRecoveryFixture{
		runs: runs, docket: docketer.Docket, last: last, decider: decider(runs, forge),
		tracker: &noSuchItem{}, rerunner: &countingRerunner{},
	}
}

func (f documentRecoveryFixture) carryOut() CarryOut {
	return CarryOut{
		Docket:    f.docket,
		Decisions: f.runs.Triage(),
		Notes:     f.tracker,
		Reruns:    f.runs.Reruns(),
		Runs:      f.runs,
		Rerunner:  f.rerunner,
	}
}

// offered is what the scheduler chooses from the carry-out's offer on a pull
// with both developer slots free and nothing in flight. The pull counts a
// developer slot taken for each decision chosen here and for nothing else, so
// a run left out of it holds no slot.
func offered(t *testing.T, carrying CarryOut) []CarryOutTask {
	t.Helper()
	chosen, _, _ := Scheduler{}.nextCarryOuts(&Schedule{}, Pull{CarryOut: carrying, Capacity: 2}, map[string]runstate.State{}, nil, nil, closedGates{}, 2, 0, nil)
	return chosen
}

// The case reported from November 16 on: the re-run was decided, then the
// document was retired. Before the retirement the re-run is offered and takes
// a slot, which is the decision working as meant. After it the run is skipped,
// no slot is counted for it, the tracker is never asked about an item it does
// not hold, and the finding is written once on the run's triage record so later
// passes skip it without reading anything. A task the scheduler chose before
// the retirement is refused as no longer applying when it is carried out.
func TestARetiredDocumentPublicationsRerunIsSkippedAndHoldsNoSlot(t *testing.T) {
	t.Parallel()

	f := newDocumentRecoveryFixture(t, docketedNow)
	before := offered(t, f.carryOut())
	if len(before) != 1 || before[0].RunID != f.last.RunID || before[0].Decision != runstate.TriageDecisionRerun {
		t.Fatalf("chosen = %#v, want the decided re-run chosen into a slot while the document is undecided", before)
	}

	if _, err := f.decider.RetireDocument(context.Background(), f.last.RunID, developmentManagersDecision); err != nil {
		t.Fatalf("RetireDocument() error = %v", err)
	}
	for pass := 0; pass < 2; pass++ {
		if chosen := offered(t, f.carryOut()); len(chosen) != 0 {
			t.Fatalf("pass %d: chosen = %#v; want the retired publication skipped and every slot left free", pass, chosen)
		}
		if _, err := f.carryOut().RecordUnattempted(context.Background(), time.Minute, nil); err != nil {
			t.Fatalf("pass %d: RecordUnattempted() error = %v", pass, err)
		}
	}
	counters, err := f.runs.Triage().Counters(f.last.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	finding, found := counters.NoLongerApplies(f.last.RunID)
	if !found || !strings.Contains(finding.Refusal, "retired the document") || !strings.Contains(finding.Refusal, f.last.Document.WriteID) {
		t.Fatalf("finding = %#v, %v; want one saying the document was retired", finding, found)
	}
	if counters.AwaitingCarryOut(f.last.RunID) {
		t.Fatal("the re-run still reads as awaiting the harness")
	}
	if f.tracker.shown != 0 || f.tracker.noted != 0 {
		t.Fatalf("the tracker was asked %d time(s) and noted %d time(s) about an item it does not hold", f.tracker.shown, f.tracker.noted)
	}

	carried, outcome, err := f.carryOut().Carry(context.Background(), before[0])
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if carried.Carried || carried.Cause != triage.CarryOutNoLongerApplies || outcome.RunID != "" || len(f.rerunner.asked) != 0 {
		t.Fatalf("carried = %#v, outcome = %#v, asked = %v; want nothing started", carried, outcome, f.rerunner.asked)
	}
}

// A retry is the document's publication handed to a run of its own, so a
// re-run of the stopped run decided before it is skipped the same way.
func TestARetriedDocumentPublicationsEarlierRerunIsSkipped(t *testing.T) {
	t.Parallel()

	f := newDocumentRecoveryFixture(t, docketedNow)
	if _, err := f.decider.RetryDocument(context.Background(), f.last.RunID, developmentManagersDecision); err != nil {
		t.Fatalf("RetryDocument() error = %v", err)
	}
	if chosen := offered(t, f.carryOut()); len(chosen) != 0 {
		t.Fatalf("chosen = %#v; want the retried publication's old re-run skipped", chosen)
	}
}

// A re-run decided after the document's decision is somebody deciding past it,
// so it is left to the action exactly as before.
func TestARerunDecidedAfterTheDocumentsDecisionIsStillOffered(t *testing.T) {
	t.Parallel()

	f := newDocumentRecoveryFixture(t, time.Now().Add(time.Hour))
	if _, err := f.decider.RetireDocument(context.Background(), f.last.RunID, developmentManagersDecision); err != nil {
		t.Fatalf("RetireDocument() error = %v", err)
	}
	if chosen := offered(t, f.carryOut()); len(chosen) != 1 || chosen[0].RunID != f.last.RunID {
		t.Fatalf("chosen = %#v, want the re-run decided after the retirement offered", chosen)
	}
}
