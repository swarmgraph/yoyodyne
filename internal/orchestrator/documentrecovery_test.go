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
// holds nothing under its identifier, and the re-run and repair actions both
// read the run's item from the tracker before they start anything. A re-run the
// development manager decided about an ended publication can therefore never
// start. Before this, the check that a recovery no longer applies asked the
// tracker, learned nothing, and the scheduler took a developer slot on every
// pull to carry the re-run out, which the action then refused. These cover
// such a re-run being set aside on the run's own record, whenever it was
// decided and whether or not the document was retried or retired, and its slot
// left free.

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
	// readAt is when the carry-out reads the records: after every decision a
	// test records, as any pass that meets them is.
	readAt time.Time
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
		tracker: &noSuchItem{}, rerunner: &countingRerunner{}, readAt: time.Now().Add(3 * time.Hour),
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
		Clock:     docketClockAt{at: f.readAt},
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

// rerunTask is the re-run the development manager decided about the
// publication run, as the carry-out would offer it.
func (f documentRecoveryFixture) rerunTask(decidedAt time.Time) CarryOutTask {
	return CarryOutTask{
		WorkItemID: f.last.WorkItemID, RunID: f.last.RunID, DocketKey: triage.Key(triage.ClassStoppedRun, f.last.RunID),
		Decision: runstate.TriageDecisionRerun, Reason: rerunReasoning, DecidedAt: decidedAt,
	}
}

// requireSkipped shows the publication run's re-run left out of two pulls in
// a row with both developer slots free, the finding written on the run's triage
// record and naming want, the re-run no longer read as waiting on the harness,
// and the tracker never asked about an item it does not hold. Carrying out the
// re-run anyway, as a pull that chose it before the record changed would,
// starts nothing.
func requireSkipped(t *testing.T, f documentRecoveryFixture, task CarryOutTask, want string) {
	t.Helper()
	for pass := 0; pass < 2; pass++ {
		if chosen := offered(t, f.carryOut()); len(chosen) != 0 {
			t.Fatalf("pass %d: chosen = %#v; want the publication run's re-run skipped and every slot left free", pass, chosen)
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
	if !found || !strings.Contains(finding.Refusal, want) || !strings.Contains(finding.Refusal, f.last.Document.WriteID) ||
		!strings.Contains(finding.Clears, "retries it") || !strings.Contains(finding.Clears, "retires it") {
		t.Fatalf("finding = %#v, %v; want one saying %q and pointing to the document retry or retirement", finding, found, want)
	}
	if counters.AwaitingCarryOut(f.last.RunID) {
		t.Fatal("the re-run still reads as awaiting the harness")
	}
	if f.tracker.shown != 0 || f.tracker.noted != 0 {
		t.Fatalf("the tracker was asked %d time(s) and noted %d time(s) about an item it does not hold", f.tracker.shown, f.tracker.noted)
	}
	carried, outcome, err := f.carryOut().Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if carried.Carried || carried.Cause != triage.CarryOutNoLongerApplies || outcome.RunID != "" || len(f.rerunner.asked) != 0 {
		t.Fatalf("carried = %#v, outcome = %#v, asked = %v; want nothing started", carried, outcome, f.rerunner.asked)
	}
}

// A failed publication with a re-run decided and no document decision: the
// re-run action reads the run's work item from the tracker before it starts,
// and the tracker holds none, so the re-run can never start. Before this it was
// chosen into a developer slot on every pull and refused there.
func TestAFailedDocumentPublicationsRerunIsSkippedAndHoldsNoSlot(t *testing.T) {
	t.Parallel()

	f := newDocumentRecoveryFixture(t, docketedNow)
	if f.last.Status != runstate.StatusFailed || f.last.DocumentDecision != nil {
		t.Fatalf("run = %s with document decision %+v, want a failed publication nobody has decided", f.last.Status, f.last.DocumentDecision)
	}
	requireSkipped(t, f, f.rerunTask(docketedNow), "cannot be carried out on it")
}

// The case reported from November 16 on: the re-run was decided on October
// 12, and the document was retired on October 13.
func TestARetiredDocumentPublicationsRerunIsSkippedAndHoldsNoSlot(t *testing.T) {
	t.Parallel()

	f := newDocumentRecoveryFixture(t, docketedNow)
	if _, err := f.decider.RetireDocument(context.Background(), f.last.RunID, developmentManagersDecision); err != nil {
		t.Fatalf("RetireDocument() error = %v", err)
	}
	requireSkipped(t, f, f.rerunTask(docketedNow), "retired the document")
}

// A re-run decided after the retirement cannot start either, so it is skipped
// the same way rather than holding a slot on every pull.
func TestARerunDecidedAfterTheDocumentsRetirementIsSkipped(t *testing.T) {
	t.Parallel()

	later := time.Now().Add(time.Hour)
	f := newDocumentRecoveryFixture(t, later)
	if _, err := f.decider.RetireDocument(context.Background(), f.last.RunID, developmentManagersDecision); err != nil {
		t.Fatalf("RetireDocument() error = %v", err)
	}
	requireSkipped(t, f, f.rerunTask(later), "retired the document")
}

// A retry is the document's publication handed to a run of its own, so a
// re-run of the stopped run is skipped the same way.
func TestARetriedDocumentPublicationsRerunIsSkipped(t *testing.T) {
	t.Parallel()

	f := newDocumentRecoveryFixture(t, docketedNow)
	if _, err := f.decider.RetryDocument(context.Background(), f.last.RunID, developmentManagersDecision); err != nil {
		t.Fatalf("RetryDocument() error = %v", err)
	}
	requireSkipped(t, f, f.rerunTask(docketedNow), "retried the document")
}
