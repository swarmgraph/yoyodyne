package readmodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicFixture is records as they are kept on disk: one open epic with a member
// of each kind, one that has ended, and what was refused.
func epicFixture(t *testing.T) (string, Sources) {
	t.Helper()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	for _, naming := range []epicscope.Naming{
		{Epic: "yoyodyne-old", Members: []string{"yoyodyne-old.1"}},
		{Epic: "yoyodyne-ep", Members: []string{"yoyodyne-ep.1", "yoyodyne-ep.2", "yoyodyne-ep.3", "yoyodyne-ep.4"}},
	} {
		naming.From = "terminal"
		if _, err := operator.Start(naming, moment); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := operator.End("yoyodyne-old", "", moment); err != nil {
		t.Fatal(err)
	}
	for _, start := range []maystart.Start{
		maystart.Run(maystart.ReadyWork, "yoyodyne-out"),
		maystart.Run(maystart.DecidedRepair, "yoyodyne-fin"),
		maystart.Run(maystart.ContinueRun, "yoyodyne-stop"),
		maystart.Pass(maystart.RecurringPass, domain.RoleProgramManager),
		maystart.Pass(maystart.PromptedPass, domain.RoleProgramManager),
	} {
		if err := maystart.Note(root, "yoyodyne", start, maystart.Decide(root, "yoyodyne", start), moment); err != nil {
			t.Fatal(err)
		}
	}
	item := func(id, title string) beads.WorkItem { return beads.WorkItem{ID: id, Title: title} }
	waits := item("yoyodyne-ep.4", "Say the mode")
	waits.Status = "open"
	waits.Dependencies = []beads.Dependency{
		{ID: "yoyodyne-dep", Type: beads.BlocksDependency},
		{ID: "yoyodyne-ep.3", Type: beads.BlocksDependency},
		{ID: "yoyodyne-fin", Type: beads.BlocksDependency},
		{ID: "yoyodyne-ep", Type: "parent-child"},
	}
	open := []beads.WorkItem{item("yoyodyne-ep.3", "Ask the decision"), item("yoyodyne-out", "Tidy the log"), item("yoyodyne-out2", "Sort the list"), waits, item("yoyodyne-dep", "Read the file")}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		ready: open[:3],
		byStatus: map[string][]beads.WorkItem{
			"open":        open,
			"closed":      {item("yoyodyne-ep.1", "Keep the record"), item("yoyodyne-fin", "Finish this")},
			"in_progress": {item("yoyodyne-ep.2", "Wire the runs"), item("yoyodyne-stop", "Rename the flag"), item("yoyodyne-ep", "Epic mode")},
		},
	}}
	run := func(id, item string) runstate.State {
		return runstate.State{RunID: id, WorkItemID: item, Status: runstate.StatusRunning, StartedAt: moment.Add(-time.Hour)}
	}
	stopped := run("run-2", "yoyodyne-stop")
	stopped.EpicWait = &runstate.EpicWait{Since: moment}
	sources.Runs = runsOfItems{fakeRuns: fakeRuns{incomplete: []runstate.State{run("run-1", "yoyodyne-ep.2"), stopped}}}
	sources.Epics = NewEpicRecords(root, "yoyodyne")
	return root, sources
}

func operatorOf(t *testing.T, root string) *epicscope.Operator {
	t.Helper()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return operator
}

func TestStatusSaysTheModeTheEpicsAndEachProblem(t *testing.T) {
	t.Parallel()
	_, sources := epicFixture(t)
	want := fmt.Sprintf(`Mode: epic mode; only work in the epics you named starts
  Open epic: Epic mode (yoyodyne-ep) — 4 members: 1 done, 1 running, 1 ready, 1 waiting
    Working
    Waiting on the Lead Product Manager: Keep the record (yoyodyne-ep.1) was closed without its change having landed: admit it again, have it replaced, or ask the operator to take it out (yoyo epic remove yoyodyne-ep yoyodyne-ep.1 --words <text>)
    Say the mode (yoyodyne-ep.4) waits for Read the file (yoyodyne-dep), which is not in this epic.
  Ended epic: not in the tracker (yoyodyne-old), as it stood when you ended it
    Cancelled: you ended it before it was delivered
  Ready items held out: 2, since %[1]s
  Items suggested during an open epic, which start nothing: 0
  Role passes not started: 2, since %[1]s
  The review of unmet goals: may not start; the review of unmet goals does not start while the factory works only on the epics the operator named
  Reading outside messages for work: may not start; outside messages are not read for work while the factory works only on the epics the operator named
  Rename the flag (yoyodyne-stop) has stopped before its next step because it is not in an epic you named. Its work is kept.
  Say the mode (yoyodyne-ep.4) waits for Read the file (yoyodyne-dep), which is not in an epic you named. To add it: yoyo epic add yoyodyne-ep yoyodyne-dep
  Not started: Tidy the log (yoyodyne-out), by the scheduler pulling ready work (refused since %[1]s, last at %[1]s: it is not in an open epic the operator named). To add it: yoyo epic add yoyodyne-ep yoyodyne-out
`, localMoment(moment))
	if got := ReadStanding(context.Background(), sources).RenderEpics(); got != want {
		t.Fatalf("RenderEpics() =\n%s\nwant\n%s", got, want)
	}
	// Ready is the same where something stops every start now.
	sources.IntakeHolds = fakeIntakeHolds{held: true, hold: runstate.IntakeHold{Reason: "held", HeldAt: moment}}
	if got := ReadStanding(context.Background(), sources).RenderEpics(); got != want {
		t.Fatalf("with new work held, RenderEpics() =\n%s\nwant\n%s", got, want)
	}
}

func TestAnItemAddedIsNoLongerAProblem(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	for _, id := range []string{"yoyodyne-out", "yoyodyne-dep", "yoyodyne-stop"} {
		if _, err := operatorOf(t, root).Add("yoyodyne-ep", id, moment); err != nil {
			t.Fatal(err)
		}
	}
	epics := ReadStanding(context.Background(), sources).Epics
	if len(epics.Problems) != 0 || epics.ItemsHeldOut != 1 || epics.ItemsHeldOutSince != nil {
		t.Fatalf("Epics = %+v, want no problem and one item held out that was never refused", epics)
	}
	if open := epics.Open[0]; open.Done != 1 || open.Running != 1 || open.Ready != 2 || open.Waiting != 3 {
		t.Fatalf("counts = %+v", open)
	}
}

func TestTheCommandThatAddsAnItemNamesTheEpicItIsBelow(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	if _, err := operatorOf(t, root).Start(epicscope.Naming{Epic: "yoyodyne-two", From: "terminal"}, moment); err != nil {
		t.Fatal(err)
	}
	says := ReadStanding(context.Background(), sources).RenderEpics()
	if !strings.Contains(says, "To add it: yoyo epic add yoyodyne-ep yoyodyne-out, or yoyo epic add yoyodyne-two yoyodyne-out\n") {
		t.Fatalf("with two epics it may go in, status says\n%s", says)
	}
	tracker := sources.Tracker.(statusTracker)
	tracker.byStatus["open"][1].Parent = "yoyodyne-two"
	if says = ReadStanding(context.Background(), sources).RenderEpics(); !strings.Contains(says, "To add it: yoyo epic add yoyodyne-two yoyodyne-out\n") {
		t.Fatalf("below one of them, status says\n%s", says)
	}
	for _, epic := range []string{"yoyodyne-ep", "yoyodyne-two"} {
		if _, err := operatorOf(t, root).End(epic, "", moment); err != nil {
			t.Fatal(err)
		}
	}
	says = ReadStanding(context.Background(), sources).RenderEpics()
	if !strings.Contains(says, "Every epic you named has ended.") || !strings.Contains(says, "it is not in an open epic the operator named).\n") {
		t.Fatalf("with every epic ended, status says\n%s", says)
	}
}

func TestEpicModeOffSaysOnlyTheMode(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	if _, err := operatorOf(t, root).TurnOff(); err != nil {
		t.Fatal(err)
	}
	if got := ReadStanding(context.Background(), sources).RenderEpics(); got != "Mode: goal mode; epic mode is off\n" {
		t.Fatalf("RenderEpics() = %q", got)
	}
}

func TestARecordOfEpicsThatCannotBeReadIsAProblem(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	path := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "epic-scope.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	says := ReadStanding(context.Background(), sources).RenderEpics()
	if !strings.HasPrefix(says, "Mode: could not be read\n  Every start that asks about the epics you named is refused: the record of them could not be read (the record of named epics, "+path) ||
		!strings.Contains(says, "unexpected EOF).\n  Not started: Tidy the log (yoyodyne-out), by ") {
		t.Fatalf("RenderEpics() = %q", says)
	}
	// What is refused for that is listed with that as its reason.
	start := maystart.Run(maystart.NamedRun, "yoyodyne-dep")
	if maystart.Ask(root, "yoyodyne", start, moment).MayStart() {
		t.Fatal("a start went ahead on a record that could not be read")
	}
	if says = ReadStanding(context.Background(), sources).RenderEpics(); !strings.Contains(says, "Not started: Read the file (yoyodyne-dep), by a run the operator names (") || strings.Count(says, "unexpected EOF") != 2 {
		t.Fatalf("RenderEpics() = %q", says)
	}
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(root, "yoyodyne"), "not-started.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if says = ReadStanding(context.Background(), sources).RenderEpics(); !strings.Contains(says, "what was not started could not be read: ") {
		t.Fatalf("RenderEpics() = %q", says)
	}
}

// Each way an item was refused is listed, with its reason and times, on the one
// line for the item and in the JSON.
func TestStatusListsEachWayAnItemWasRefused(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	later := moment.Add(time.Hour)
	for _, path := range []maystart.Path{maystart.ReadyWork, maystart.BringInFile} {
		maystart.Ask(root, "yoyodyne", maystart.Run(path, "yoyodyne-out"), later)
	}
	standing := ReadStanding(context.Background(), sources)
	want := fmt.Sprintf("Not started: Tidy the log (yoyodyne-out), by the scheduler pulling ready work (refused since %[1]s, last at %[2]s: it is not in an open epic the operator named); the run that brings in a pinned outside file (refused since %[2]s, last at %[2]s: it is not in an open epic the operator named). To add it:",
		localMoment(moment), localMoment(later))
	if says := standing.RenderEpics(); strings.Count(says, "Not started: ") != 1 || !strings.Contains(says, want) {
		t.Fatalf("RenderEpics() = %q, want %q", says, want)
	}
	if refused := standing.Epics.Refused; len(refused) != 2 || refused[0].Path != maystart.ReadyWork || refused[1].Path != maystart.BringInFile || refused[1].Why == "" {
		t.Fatalf("Refused = %+v", refused)
	}
	if since := standing.Epics.ItemsHeldOutSince; since == nil || !since.Equal(moment) {
		t.Fatalf("held out since %v, want the first refusal", since)
	}
}

// A refused document is said once on status, whatever it answers, and not again
// among the documents waiting for a landing. An item it answers being finished
// does not take it away.
func TestStatusSaysEachRefusedDocumentOnce(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	runs := documentWaitRuns{fakeRuns: sources.Runs.(runsOfItems).fakeRuns}
	refuse := func(id, title, write string, answers ...string) {
		maystart.Ask(root, "yoyodyne", maystart.DocumentRun(maystart.PublishDocument, answers...).Of(maystart.Document{ID: id, Title: title, WriteID: write}), moment)
		runs.waits = append(runs.waits, runstate.DocumentWait{Since: moment, Document: runstate.DocumentPublication{WriteID: write, Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: id, Title: title}}}})
	}
	refuse("d-none", "Answers nothing", "document-1.1")
	refuse("d-one", "Answers one", "document-1.2", "yoyodyne-fin")
	refuse("d-two", "Answers two", "document-1.3", "yoyodyne-out", "yoyodyne-out2")
	refuse("d-member", "Answers a member", "document-1.4", "yoyodyne-ep.3")
	sources.Runs = runs
	standing := ReadStanding(context.Background(), sources)
	says := standing.Render() + standing.RenderEpics()
	refused := fmt.Sprintf("by publishing a document (refused since %[1]s, last at %[1]s: ", localMoment(moment))
	outside := "the document answers no open epic the operator named and no work item in one)."
	for _, want := range []string{
		"Not started: Answers nothing (d-none, document-1.1), " + refused + "the document answers no work item, so it waits until epic mode is turned off or it is written again naming a member of an open epic).\n",
		"Not started: Answers one (d-one, document-1.2), " + refused + outside + " It answers Finish this (yoyodyne-fin). To let it start: yoyo epic add yoyodyne-ep yoyodyne-fin\n",
		"Not started: Answers two (d-two, document-1.3), " + refused + outside + " It answers Tidy the log (yoyodyne-out), Sort the list (yoyodyne-out2). To let it start: yoyo epic add yoyodyne-ep yoyodyne-out, or yoyo epic add yoyodyne-ep yoyodyne-out2\n",
	} {
		if !strings.Contains(says, want) {
			t.Errorf("status does not say %q:\n%s", want, says)
		}
	}
	for _, write := range []string{"document-1.1", "document-1.2", "document-1.3", "document-1.4"} {
		if strings.Count(says, write) != 1 {
			t.Errorf("%s is said %d times, want once:\n%s", write, strings.Count(says, write), says)
		}
	}
	// The dashboard reads the waiting documents, and says why there.
	if line := standing.DocumentsWaitingForSlot[0].Line; !strings.Contains(line, "the document answers no work item") {
		t.Errorf("the waiting document says %q", line)
	}
}

func TestNothingIsSaidOfEpicsWhereNoneWasNamed(t *testing.T) {
	t.Parallel()
	for name, records := range map[string]EpicRecords{"nothing wired": nil, "no file": NewEpicRecords(t.TempDir(), "yoyodyne")} {
		sources := quietSources()
		sources.Epics = records
		standing := ReadStanding(context.Background(), sources)
		if standing.Epics != nil || standing.RenderEpics() != "" {
			t.Errorf("%s: Epics = %+v, rendered %q", name, standing.Epics, standing.RenderEpics())
		}
	}
}

func TestWhatTheEpicLinesCouldNotReadIsSaid(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	sources.Tracker = statusTracker{fakeTracker{fail: errors.New("bd is away")}}
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(root, "yoyodyne"), "not-started.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	says := ReadStanding(context.Background(), sources).RenderEpics()
	for _, want := range []string{"Open epic: its title could not be read (yoyodyne-ep)\n", "could not be counted: ", "bd is away", "what was not started could not be read: "} {
		if !strings.Contains(says, want) {
			t.Errorf("RenderEpics() = %q, want it to say %q", says, want)
		}
	}
	if strings.Contains(says, "held out: 0") || strings.Contains(says, "not started: 0") {
		t.Errorf("RenderEpics() = %q, which says none of what was not read", says)
	}
}

// A refused update is one line for its target branch, until an open epic lets it
// start.
func TestARefusedUpdateFromTheTeamsBranchIsAProblem(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	if _, err := operatorOf(t, root).End("yoyodyne-ep", "", moment); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if maystart.Ask(root, "yoyodyne", maystart.UpdateFromTeam("staging"), moment).MayStart() {
			t.Fatal("the update started with every epic ended")
		}
	}
	want := fmt.Sprintf("  Not started: bringing the target branch up to date with the team's branch, for staging (refused since %[1]s, last at %[1]s: every epic the operator named has ended).\n", localMoment(moment))
	standing := ReadStanding(context.Background(), sources)
	if says := standing.RenderEpics(); strings.Count(says, want) != 1 || len(standing.Epics.RefusedUpdates) != 1 {
		t.Fatalf("RenderEpics() = %q and %+v, want %q once", says, standing.Epics.RefusedUpdates, want)
	}
	if _, err := operatorOf(t, root).Start(epicscope.Naming{Epic: "yoyodyne-two", From: "terminal"}, moment); err != nil {
		t.Fatal(err)
	}
	if says := ReadStanding(context.Background(), sources).RenderEpics(); strings.Contains(says, "for staging") {
		t.Fatalf("RenderEpics() = %q, want no refused update once it may start", says)
	}
}
