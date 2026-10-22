package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// exampleItems is the tracker the design's examples are written against
// (docs/designs/observability-and-dashboard.md, "Work item references"): one
// item with a label, one without, and one whose title carries another id.
func exampleItems() []beads.WorkItem {
	return []beads.WorkItem{
		{ID: "ex-7", Title: "Retry the merge", Priority: 1, Labels: []string{"reliability"}},
		{ID: "ex-7.2", Title: "Name the stop", Priority: 2},
		{ID: "ex-12", Title: "Fix ex-7's timeout", Priority: 0, Labels: []string{"bug"}},
	}
}

// titledItems is a tracker holding the two items the operator was handed as
// bare numbers on 2026-09-26, and one more.
func titledItems() []beads.WorkItem {
	return []beads.WorkItem{
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by", Priority: 1, Labels: []string{"reliability"}},
		{ID: "yoyodyne-ifd.434.3", Title: "Say the provider's reset in local time", Priority: 2},
		{ID: "yoyodyne-ifd.12", Title: "Pause on a provider usage limit", Priority: 0, Labels: []string{"reliability", "cost"}},
	}
}

func TestAListNamesEachItemInTheOperatorsForm(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(titledItems())
	for id, want := range map[string]string{
		// Labels are shown in order, whatever order the tracker lists them in.
		"yoyodyne-ifd.12": "(P0, cost, reliability) Pause on a provider usage limit (yoyodyne-ifd.12)",
		// An item with no label shows only its priority.
		"yoyodyne-ifd.434.3": "(P2) Say the provider's reset in local time (yoyodyne-ifd.434.3)",
		"yoyodyne-ifd.999.1": "yoyodyne-ifd.999.1 (not in the tracker)",
	} {
		if got := titles.Name(id); got != want {
			t.Errorf("Name(%s) = %q, want %q", id, got, want)
		}
	}
}

// Each row is one of the design's examples, rendered from the stored text.
func TestTheDesignsExamplesRenderAsTheDesignSays(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(exampleItems())
	for _, test := range []struct{ name, stored, want string }{
		{"semicolon and full stop", "Blocked by ex-7; see ex-7.2.",
			"Blocked by ex-7 (P1, reliability: Retry the merge); see ex-7.2 (P2: Name the stop)."},
		{"lowercase letters running on", "ex-7.2s are done, then ex-7 lands",
			"ex-7.2s (not in the tracker) are done, then ex-7 (P1, reliability: Retry the merge) lands"},
		{"an id inside a title", "Waiting on ex-12.",
			"Waiting on ex-12 (P0, bug: Fix ex-7's timeout)."},
		{"several references and a role's own title", "Retry the merge (ex-7), then ex-7.2 and ex-12.",
			"Retry the merge (ex-7 (P1, reliability: Retry the merge)), then ex-7.2 (P2: Name the stop) and ex-12 (P0, bug: Fix ex-7's timeout)."},
		{"not in the tracker", "See ex-9.", "See ex-9 (not in the tracker)."},
		{"a later parenthesized reference on the same line", "(P1) First task (ex-7). Compare (ex-12).",
			"(P1) First task (ex-7 (P1, reliability: Retry the merge)). Compare (ex-12 (P0, bug: Fix ex-7's timeout))."},
	} {
		got := titles.Cite(test.stored)
		if got != test.want {
			t.Errorf("%s: Cite(%q) = %q, want %q", test.name, test.stored, got, test.want)
		}
		// Rendering again with the same tracker answer gives the same bytes,
		// and never a second set of parentheses or a reference found inside
		// a title.
		if again := titles.Cite(got); again != got {
			t.Errorf("%s: rendering again = %q, want %q unchanged", test.name, again, got)
		}
	}
}

func TestARenamedItemChangesOnlyTheParenthesesAfterIt(t *testing.T) {
	t.Parallel()

	stored := "Retrying ex-7 now."
	before := NewWorkItemTitles(exampleItems()).Cite(stored)
	if want := "Retrying ex-7 (P1, reliability: Retry the merge) now."; before != want {
		t.Fatalf("Cite() = %q, want %q", before, want)
	}
	renamed := exampleItems()
	renamed[0].Title, renamed[0].Labels = "Retry the merge after a refusal", []string{"reliability", "bug"}
	after := NewWorkItemTitles(renamed).Cite(stored)
	if want := "Retrying ex-7 (P1, bug, reliability: Retry the merge after a refusal) now."; after != want {
		t.Fatalf("Cite() after the rename = %q, want %q", after, want)
	}
}

// Words a role wrote in parentheses after an id are its own, whatever they look
// like: they are kept as written, with the item's state put in before them.
func TestARolesOwnParenthesesAfterAnIdSurvive(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(exampleItems())
	for stored, want := range map[string]string{
		"ex-7 (P1: retry after the forge answers)":  "ex-7 (P1, reliability: Retry the merge) (P1: retry after the forge answers)",
		"ex-7 (not in the tracker, I think) failed": "ex-7 (P1, reliability: Retry the merge) (not in the tracker, I think) failed",
	} {
		if got := titles.Cite(stored); got != want {
			t.Errorf("Cite(%q) = %q, want %q", stored, got, want)
		}
	}
}

// A tracker that could not be read marks every reference with the time of the
// reading, and drops none of them nor any of the text around them.
func TestAnUnreadableTrackerMarksEveryReference(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 30, 16, 40, 0, 0, time.UTC)
	titles := UnreadableWorkItemTitles(at, "ex", "yoyodyne")
	said := "tracker unreadable at " + at.Local().Format("15:04 MST")
	if got, want := titles.Cite("Blocked by ex-7."), "Blocked by ex-7 ("+said+")."; got != want {
		t.Errorf("Cite() = %q, want %q", got, want)
	}
	if got, want := titles.Name("ex-7"), "ex-7 ("+said+")"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	// A root id has no shape that says it is an item, so a surface that knows
	// it is one names it, and it is marked all the same.
	if got, want := titles.CiteKnowing("  yoyodyne-wja — developing", "yoyodyne-wja"), "  yoyodyne-wja ("+said+") — developing"; got != want {
		t.Errorf("CiteKnowing() = %q, want %q", got, want)
	}
	if got, want := titles.Name("yoyodyne-wja"), "yoyodyne-wja ("+said+")"; got != want {
		t.Errorf("Name(root) = %q, want %q", got, want)
	}
	// Nothing wired at all is not an unreadable tracker, and leaves the text
	// as it was.
	var none *WorkItemTitles
	if got := none.Cite("ex-7 and 434.9"); got != "ex-7 and 434.9" {
		t.Errorf("nil Cite() = %q, want the text unchanged", got)
	}
}

// A short id is resolved only when the text is recorded; recorded with its
// references, it is shown as the item it named then. The same words recorded
// before references were kept are left as written and counted.
func TestAShortIdIsResolvedOnlyWhenItsTextIsRecorded(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(titledItems())
	written := "Blocked on 434.9 and ifd.434.3; Go 1.22 is fine."
	references, unresolved := titles.References(written)
	if len(references) != 2 || references[0].ID != "yoyodyne-ifd.434.9" || references[1].ID != "yoyodyne-ifd.434.3" {
		t.Fatalf("References() = %+v, want both short ids resolved to their items", references)
	}
	if unresolved != 1 {
		t.Errorf("References() unresolved = %d, want the version number counted as a short id that names nothing", unresolved)
	}
	want := "Blocked on yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session at what it moved by) and " +
		"yoyodyne-ifd.434.3 (P2: Say the provider's reset in local time); Go 1.22 is fine."
	if got := titles.CiteRecorded(written, references); got != want {
		t.Errorf("CiteRecorded() = %q, want %q", got, want)
	}
	if got := titles.Cite(written); got != written {
		t.Errorf("Cite(older text) = %q, want the short ids left as written", got)
	}
	if got := titles.Unresolved(written); got != 2 {
		t.Errorf("Unresolved(older text) = %d, want the two short ids an item ends in", got)
	}
	// References whose bytes no longer cover their id are not trusted.
	if got := titles.CiteRecorded("Blocked on 999.9 and more", references); got != "Blocked on 999.9 and more" {
		t.Errorf("CiteRecorded(changed text) = %q, want it shown as written", got)
	}
}

// Text that only looks like it names an item is left exactly as written.
func TestTextThatIsNotAReferenceIsLeftAlone(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(append(titledItems(),
		beads.WorkItem{ID: "yoyodyne-ifd.1.22", Title: "Something numbered like a version"},
	))
	for _, text := range []string{
		"built with Go 1.22 and 434.9",
		"waited 3.5 hours for the provider",
		"the branch yoyodyne/yoyodyne-ifd-432-18/e6775b59 and run-e6775b59",
		"docs/diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md",
		"run `yoyo cost yoyodyne-ifd.12` to see it",
		"a yoyodyne-watch session and yoyodyne-specific wording",
		"https://forge.example/yoyodyne-ifd.12",
	} {
		if got := titles.Cite(text); got != text {
			t.Errorf("Cite(%q) = %q, want it unchanged", text, got)
		}
	}
}

func titledSources() Sources {
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"": titledItems()}}}
	return sources
}

// The needs-a-human line is what the operator reads in a terminal and in the
// channel, and the dashboard reads the same entry's sentences from JSON.
func TestTheNeedsAHumanLineShowsEveryItemWithItsState(t *testing.T) {
	t.Parallel()

	sources := titledSources()
	sources.Directives = fakeDirectives{recorded: []directive.Directive{{
		ID: "directive-4f2c", Kind: directive.KindAmbiguous, Unresolved: "should yoyodyne-ifd.434.9 land before yoyodyne-ifd.434.3?", ReceivedAt: moment.Add(-time.Hour),
	}}}
	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	for _, want := range []string{
		"yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session at what it moved by)",
		"yoyodyne-ifd.434.3 (P2: Say the provider's reset in local time)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("Render() = %q, want it to carry %q", rendered, want)
		}
	}

	var entry Attention
	for _, waiting := range standing.NeedsHuman {
		if waiting.Kind == AttentionDirective {
			entry = waiting
		}
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if said, _ := wire["said_what"].(string); !strings.Contains(said, "yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session") {
		t.Errorf("said_what = %q, want the sentence the dashboard shows with the item's state in it", said)
	}
	if what, _ := wire["what"].(string); strings.Contains(what, "Price a resumed") {
		t.Errorf("what = %q, want the derived sentence left as the fields say it", what)
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Errorf("an entry carrying its rendered sentences does not read back: %v", err)
	}
}

func TestTheStatusLinesNameEachItemInTheOperatorsForm(t *testing.T) {
	t.Parallel()

	standing := Standing{
		Running:      []RunningRun{{RunID: "run-1", WorkItemID: "yoyodyne-ifd.434.3", Phase: runstate.PhaseDeveloping}},
		NotStartable: []Refused{{WorkItemID: "yoyodyne-ifd.12", Reason: "waiting on yoyodyne-ifd.434.9"}},
		Admitted:     1,
		Titles:       NewWorkItemTitles(titledItems()),
	}
	rendered := standing.RenderLines()
	for _, want := range []string{
		"  (P2) Say the provider's reset in local time (yoyodyne-ifd.434.3) — ",
		"  (P0, cost, reliability) Pause on a provider usage limit (yoyodyne-ifd.12) — ",
		"yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session at what it moved by)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderLines() = %q, want it to carry %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "(yoyodyne-ifd.434.3 (P") {
		t.Errorf("RenderLines() = %q, gives a listed item a second state", rendered)
	}
}

// A run in flight whose item the tracker could not be read for is still named,
// and marked: the running line knows it is a work item whatever its id's shape.
func TestTheRunningLineMarksAnItemTheTrackerCouldNotBeReadFor(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.TrackerPrefixes = []string{"calc"}
	sources.Tracker = statusTracker{fakeTracker{fail: context.DeadlineExceeded}}
	standing := ReadStanding(context.Background(), sources)
	if standing.Titles == nil || standing.Titles.Readable() {
		t.Fatalf("Titles = %+v, want the unreadable reading from a tracker that failed", standing.Titles)
	}
	standing.Running = []RunningRun{{RunID: "run-1", WorkItemID: "calc-wja", Phase: runstate.PhaseDeveloping}}
	rendered := standing.RenderLines()
	if !strings.Contains(rendered, "  calc-wja (tracker unreadable at ") {
		t.Errorf("RenderLines() = %q, want the run's item marked as unreadable", rendered)
	}
}

// The lane report is the surface the defect was found on, and the card the
// dashboard opens on it reads this query.
func TestALaneReportsCardShowsEveryItemItsReferencesName(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(titledItems())
	current := blockedBy("factory-pgm", "restart-0123456789abcdef")
	current.Report.Summary = "Moving on 434.9 and 434.3."
	current.Report.SummaryReferences, _ = titles.References(current.Report.Summary)
	current.Report.ReferencesKept = true
	current.Report.Remaining = []string{"yoyodyne-ifd.12", "yoyodyne-ifd.999.1", "older 434.9"}
	current.Report.Blockers[0].What = "yoyodyne-ifd.434.9 needs a restart"
	sources := programManagerSources()
	sources.Tracker = titledSources().Tracker
	sources.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "factory-pgm")}}
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": current}}

	answer, err := ReadProgramManagerReport(sources, "factory-pgm")
	if err != nil || answer.Report == nil {
		t.Fatalf("ReadProgramManagerReport() = %+v, %v", answer, err)
	}
	if answer.Report.FoundWhenRead {
		t.Errorf("FoundWhenRead = true on a report written with its references kept")
	}
	older := current
	older.Report.ReferencesKept, older.Report.SummaryReferences = false, nil
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": older}}
	if read, err := ReadProgramManagerReport(sources, "factory-pgm"); err != nil || read.Report == nil || !read.Report.FoundWhenRead {
		t.Errorf("a report written before references were kept = %+v, %v; want it marked found_when_read", read.Report, err)
	}
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": current}}
	if want := "Moving on yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session at what it moved by) and yoyodyne-ifd.434.3 (P2: Say the provider's reset in local time)."; answer.Report.Summary != want {
		t.Errorf("Summary = %q, want %q", answer.Report.Summary, want)
	}
	if got := strings.Join(answer.Report.Remaining, "|"); got != "yoyodyne-ifd.12 (P0, cost, reliability: Pause on a provider usage limit)|yoyodyne-ifd.999.1 (not in the tracker)|older 434.9" {
		t.Errorf("Remaining = %q, want each full id with its state and the short id kept from before references as written", got)
	}
	if len(answer.Instance.Blockers) != 1 || !strings.Contains(answer.Instance.Blockers[0].What, "yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session") {
		t.Errorf("Blockers = %+v, want the blocker's item with its state", answer.Instance.Blockers)
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.ProgramManagers) == 0 || !strings.Contains(standing.ProgramManagers[0].Blockers[0].What, "yoyodyne-ifd.434.9 (P1, reliability: Price a resumed session") {
		t.Errorf("the standing's instance = %+v, want its blocker shown as the card's is", standing.ProgramManagers)
	}
}

// The dashboard lists items from the fields, and reads their names from here.
func TestTheStandingNamesEveryItemItCarriesAField(t *testing.T) {
	t.Parallel()

	sources := titledSources()
	sources.Directives = fakeDirectives{}
	standing := ReadStanding(context.Background(), sources)
	standing.Running = append(standing.Running, RunningRun{WorkItemID: "yoyodyne-ifd.12"})
	names := standing.Titles.Names(standing.namedItems()...)
	if got, want := names["yoyodyne-ifd.12"], "(P0, cost, reliability) Pause on a provider usage limit (yoyodyne-ifd.12)"; got != want {
		t.Errorf("WorkItemNames[yoyodyne-ifd.12] = %q, want %q", got, want)
	}
}

// A name in the operator's form is never searched again: the id inside its
// title is the title's, so the running line and the not-startable line show it
// as the tracker holds it.
func TestATitleNamingAnotherItemIsNotSearchedInAList(t *testing.T) {
	t.Parallel()

	standing := Standing{
		Running:      []RunningRun{{RunID: "run-1", WorkItemID: "ex-12", Phase: runstate.PhaseDeveloping}},
		NotStartable: []Refused{{WorkItemID: "ex-12", Reason: "waiting on ex-7"}},
		Admitted:     1,
		Titles:       NewWorkItemTitles(exampleItems()),
	}
	rendered := standing.RenderLines()
	if !strings.Contains(rendered, "  (P0, bug) Fix ex-7's timeout (ex-12) — developing") {
		t.Errorf("RenderLines() = %q, want the running item named with its title untouched", rendered)
	}
	if !strings.Contains(rendered, "  (P0, bug) Fix ex-7's timeout (ex-12) — waiting on ex-7 (P1, reliability: Retry the merge)") {
		t.Errorf("RenderLines() = %q, want the refused item's title untouched and the reason's reference shown with its state", rendered)
	}
	if strings.Contains(rendered, "Fix ex-7 (") {
		t.Errorf("RenderLines() = %q, searched a title read from the tracker", rendered)
	}
	titles := NewWorkItemTitles(exampleItems())
	if named := titles.Name("ex-12"); titles.Cite("*"+named+"*") != "*"+named+"*" {
		t.Errorf("Cite(Name()) = %q, want the name unchanged", titles.Cite("*"+named+"*"))
	}
}

// Text in a fenced block is something to be read or typed exactly as written.
func TestAFencedBlockIsLeftAsWritten(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(exampleItems())
	text := "Run this:\n```\nyoyo status ex-7\n```\nthen ex-7 lands."
	want := "Run this:\n```\nyoyo status ex-7\n```\nthen ex-7 (P1, reliability: Retry the merge) lands."
	if got := titles.Cite(text); got != want {
		t.Errorf("Cite() = %q, want %q", got, want)
	}
}
