package orchestrator

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A stopped document publication has no work item, so nothing writes a blocker
// for it, and an older copy whose pull request is still open is put to the
// development manager carrying no failure either. Its reason is why its
// document stopped, and the docket takes that as the reason rather than
// refusing the entry and failing the build over it.
func TestAStoppedDocumentPublicationWithNoWorkItemIsDocketedOnWhyItsDocumentStopped(t *testing.T) {
	_, docketer, _, runs, last := stoppedPublication(t)
	older := runByID(t, runs, last.Document.RunIDFor(0))
	older.Blocker = ""
	older.Failure = ""
	older.PullRequest = &runstate.PullRequest{Remote: "origin", Branch: older.Branch, Number: 1170, URL: "https://github.example/yoyodyne/pull/1170", HeadCommit: older.BaseCommit, State: "OPEN"}
	if err := runs.Save(older); err != nil {
		t.Fatal(err)
	}

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v, want the stopped publication docketed", err)
	}
	if len(built.NotDocketed) != 0 {
		t.Fatalf("not docketed = %#v, want every run docketed", built.NotDocketed)
	}
	var entry *triage.Entry
	for index := range built.Entries {
		if built.Entries[index].RunID == older.RunID {
			entry = &built.Entries[index]
		}
	}
	if entry == nil {
		t.Fatalf("docket = %+v, want the older copy's run on it", built.Entries)
	}
	if entry.Class != triage.ClassStoppedRun || entry.Blocker != "" || entry.Failure != "" || entry.StopUnrecorded != "" {
		t.Fatalf("entry = %+v, want a stopped run carrying its document's reason and nothing else", entry)
	}
	if entry.Document == nil || strings.TrimSpace(entry.Document.Stopped) == "" || entry.Document.PullRequest != 1170 {
		t.Fatalf("document = %+v, want why it stopped and its pull request", entry.Document)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "Why it stopped") || !strings.Contains(rendered, "#1170") {
		t.Fatalf("entry is missing why it stopped:\n%s", rendered)
	}
}

// A stopped run whose record carries no reason the docket accepts is listed
// saying so in plain words, and the build names it, rather than refusing it.
// A blocker of nothing but spaces is the shape the scan can meet: it reads as
// a blocker to the scan and as none to the entry.
func TestAStoppedRunWithNoRecordedReasonIsListedSayingSo(t *testing.T) {
	t.Parallel()

	state := stoppedState()
	state.Blocker = "   "
	state.Failure = ""
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{state}, docket).Build()
	if err != nil || len(built.NotDocketed) != 0 {
		t.Fatalf("Build() = %v, not docketed %#v; want the run listed", err, built.NotDocketed)
	}
	if len(built.Entries) != 1 || built.Entries[0].RunID != state.RunID {
		t.Fatalf("docket = %+v, want the run on it", built.Entries)
	}
	if len(built.Unrecorded) != 1 || built.Unrecorded[0] != state.RunID {
		t.Fatalf("unrecorded = %v, want the run named", built.Unrecorded)
	}
	entry := built.Entries[0]
	if !strings.Contains(entry.StopUnrecorded, "ended failed at its reviewing step") {
		t.Fatalf("stop_unrecorded = %q, want how the record says it ended", entry.StopUnrecorded)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "No reason for this stop was recorded") {
		t.Fatalf("entry does not say no reason was recorded:\n%s", rendered)
	}
}

// One build meeting every shape at once finishes: the stoppage with no
// recorded reason is listed saying so, the run whose entry cannot be written is
// named, and the ordinary stoppage beside them is docketed as it always was.
func TestADocketBuildFinishesPastARunWithNoReasonAndARunItCannotWrite(t *testing.T) {
	t.Parallel()

	ordinary := stoppedState()
	unexplained := stoppedState()
	unexplained.RunID = "run-11111111111111111111111111111111"
	unexplained.Blocker = " "
	unwritable := publishedState(3 * docketedTriage.StuckMergeAge.Duration())
	unwritable.RunID = "run-22222222222222222222222222222222"
	docket := &memoryDocket{failOn: string(triage.ClassPublication)}

	built, err := docketerOver([]runstate.State{ordinary, unexplained, unwritable}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v, want it to finish past both", err)
	}
	if built.Added != 2 || len(built.Entries) != 2 {
		t.Fatalf("build = %+v, want both stoppages docketed", built)
	}
	if len(built.Unrecorded) != 1 || built.Unrecorded[0] != unexplained.RunID {
		t.Fatalf("unrecorded = %v, want the run with no reason named", built.Unrecorded)
	}
	if len(built.NotDocketed) != 1 || built.NotDocketed[0].RunID != unwritable.RunID {
		t.Fatalf("not docketed = %#v, want the unwritable run named", built.NotDocketed)
	}
}
