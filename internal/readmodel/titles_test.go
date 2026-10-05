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

// titledItems is a tracker holding the two items the operator was handed as
// bare numbers on 2026-09-26, and one more.
func titledItems() []beads.WorkItem {
	return []beads.WorkItem{
		{ID: "yoyodyne-ifd.434.9", Title: "Price a resumed session at what it moved by"},
		{ID: "yoyodyne-ifd.434.3", Title: "Say the provider's reset in local time"},
		{ID: "yoyodyne-ifd.12", Title: "Pause on a provider usage limit"},
	}
}

func TestEveryShapeOfIdentifierIsShownBesideItsTitle(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(titledItems())
	for _, test := range []struct {
		name, text, want string
	}{
		{"the bare numbers a lane report used", "Blocked on 434.9 and 434.3.",
			"Blocked on (P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9) and (P0) Say the provider's reset in local time (yoyodyne-ifd.434.3)."},
		{"the full identifier", "yoyodyne-ifd.12 is next",
			"(P0) Pause on a provider usage limit (yoyodyne-ifd.12) is next"},
		{"the identifier without the product", "see ifd.434.9",
			"see (P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)"},
		{"a full identifier the tracker does not hold", "admitted as yoyodyne-ifd.999.1",
			"admitted as title unavailable (yoyodyne-ifd.999.1)"},
		{"the form without the product the tracker does not hold", "see ifd.999",
			"see title unavailable (yoyodyne-ifd.999)"},
	} {
		if got := titles.Cite(test.text); got != test.want {
			t.Errorf("%s: Cite(%q) = %q, want %q", test.name, test.text, got, test.want)
		}
	}
}

// Text that already says what an item is, or that only looks like it names one,
// is left exactly as it was written.
func TestTextThatIsNotABareIdentifierIsLeftAlone(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(append(titledItems(),
		beads.WorkItem{ID: "yoyodyne-ifd.3.5", Title: "Something numbered like a duration"},
		beads.WorkItem{ID: "yoyodyne-ifd.27.93", Title: "Something numbered like a price"},
		beads.WorkItem{ID: "yoyodyne-ifd.1.22", Title: "Something numbered like a Go version"},
	))
	for _, text := range []string{
		"waited 3.5 hours for the provider",
		"Go 1.22 and Python 3.5; version 27.93",
		"it cost $27.93 across 3 runs",
		"cached 27.93% of input",
		"release v0.3.0 and 10.0.0.1 and 2026.09.26",
		"the branch yoyodyne/yoyodyne-ifd-432-18/e6775b59 and run-e6775b59",
		"docs/diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md",
		"run `yoyo cost yoyodyne-ifd.12` to see it",
		"a yoyodyne-watch session and the yoyodyne-ifd tracker",
		"https://forge.example/yoyodyne-ifd.12",
		"5.5 is not an item and 99.99 is not one either",
	} {
		if got := titles.Cite(text); got != text {
			t.Errorf("Cite(%q) = %q, want it unchanged", text, got)
		}
	}
}

func TestEveryMentionCarriesTheCompleteCitation(t *testing.T) {
	t.Parallel()

	titles := NewWorkItemTitles(titledItems())
	once := titles.Cite("434.9 first, then yoyodyne-ifd.434.9 again, and 999.1 never")
	want := "(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9) first, then (P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9) again, and 999.1 never"
	if once != want {
		t.Fatalf("Cite() = %q, want %q", once, want)
	}
	if twice := titles.Cite(once); twice != once {
		t.Errorf("Cite(Cite()) = %q, want it unchanged from %q", twice, once)
	}
	unknown := titles.Cite("yoyodyne-ifd.999.1")
	if again := titles.Cite(unknown); again != unknown {
		t.Errorf("an unknown identifier was said to be unknown twice: %q", again)
	}
	if after := titles.CiteAfter(once, "434.9 is still open"); after != "(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9) is still open" {
		t.Errorf("CiteAfter() = %q, want every mention expanded", after)
	}
}

func TestAnUnreadableTrackerKeepsTheIdentifierAndMarksTheTitleUnavailable(t *testing.T) {
	t.Parallel()

	var titles *WorkItemTitles
	if got := titles.Cite("yoyodyne-ifd.999.1 and 434.9"); got != "title unavailable (yoyodyne-ifd.999.1) and 434.9" {
		t.Errorf("nil Cite() = %q, want the identifier retained with its title unavailable", got)
	}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{fail: context.DeadlineExceeded}}
	standing := ReadStanding(context.Background(), sources)
	if standing.Titles != nil {
		t.Errorf("Titles = %+v, want none from a tracker that failed", standing.Titles)
	}
}

func titledSources() Sources {
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"": titledItems()}}}
	return sources
}

// The needs-a-human line is what the operator reads in a terminal and in the
// channel, and the dashboard reads the same entry's sentences from JSON.
func TestTheNeedsAHumanLineShowsEveryItemBesideItsTitle(t *testing.T) {
	t.Parallel()

	sources := titledSources()
	sources.Directives = fakeDirectives{recorded: []directive.Directive{{
		ID: "directive-4f2c", Kind: directive.KindAmbiguous, Unresolved: "should 434.9 land before 434.3?", ReceivedAt: moment.Add(-time.Hour),
	}}}
	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	for _, want := range []string{
		"(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)",
		"(P0) Say the provider's reset in local time (yoyodyne-ifd.434.3)",
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
	if said, _ := wire["said_what"].(string); !strings.Contains(said, "(P0) Price a resumed session") {
		t.Errorf("said_what = %q, want the sentence the dashboard shows with the title in it", said)
	}
	if what, _ := wire["what"].(string); strings.Contains(what, "Price a resumed") {
		t.Errorf("what = %q, want the derived sentence left as the fields say it", what)
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Errorf("an entry carrying its titled sentences does not read back: %v", err)
	}
}

func TestTheNotStartableLineShowsEachItemBesideItsTitle(t *testing.T) {
	t.Parallel()

	standing := Standing{
		NotStartable: []Refused{{WorkItemID: "yoyodyne-ifd.12", Reason: "waiting on 434.9"}},
		Admitted:     1,
		Titles:       NewWorkItemTitles(titledItems()),
	}
	rendered := standing.RenderLines()
	for _, want := range []string{
		"(P0) Pause on a provider usage limit (yoyodyne-ifd.12)",
		"(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderLines() = %q, want it to carry %q", rendered, want)
		}
	}
}

// The lane report is the surface the defect was found on, and the card the
// dashboard opens on it reads this query.
func TestALaneReportsCardShowsEveryItemBesideItsTitle(t *testing.T) {
	t.Parallel()

	current := blockedBy("factory-pgm", "restart-0123456789abcdef")
	current.Report.Summary = "Moving on 434.9 and 434.3."
	current.Report.Remaining = []string{"yoyodyne-ifd.12", "yoyodyne-ifd.999.1"}
	current.Report.Blockers[0].What = "434.9 needs a restart"
	sources := programManagerSources()
	sources.Tracker = titledSources().Tracker
	sources.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "factory-pgm")}}
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": current}}

	answer, err := ReadProgramManagerReport(sources, "factory-pgm")
	if err != nil || answer.Report == nil {
		t.Fatalf("ReadProgramManagerReport() = %+v, %v", answer, err)
	}
	if want := "Moving on (P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9) and (P0) Say the provider's reset in local time (yoyodyne-ifd.434.3)."; answer.Report.Summary != want {
		t.Errorf("Summary = %q, want %q", answer.Report.Summary, want)
	}
	if got := strings.Join(answer.Report.Remaining, "|"); got != "(P0) Pause on a provider usage limit (yoyodyne-ifd.12)|title unavailable (yoyodyne-ifd.999.1)" {
		t.Errorf("Remaining = %q, want each item titled and the unknown one said to be unknown", got)
	}
	if len(answer.Instance.Blockers) != 1 || !strings.Contains(answer.Instance.Blockers[0].What, "(P0) Price a resumed session") {
		t.Errorf("Blockers = %+v, want the blocker's item titled", answer.Instance.Blockers)
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.ProgramManagers) == 0 || !strings.Contains(standing.ProgramManagers[0].Blockers[0].What, "(P0) Price a resumed session") {
		t.Errorf("the standing's instance = %+v, want its blocker titled as the card's is", standing.ProgramManagers)
	}
}

func TestCitationIncludesCurrentPriorityLabelsAndTheWholeTitle(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{ID: "yoyodyne-ifd.432.28", Priority: 1, Labels: []string{"reliability", "dashboard"}, Title: strings.Repeat("A long title ", 12)}
	titles := NewWorkItemTitles([]beads.WorkItem{item})
	want := "(P1, reliability, dashboard) " + strings.TrimSpace(item.Title) + " (yoyodyne-ifd.432.28)"
	if got := titles.Cite("blocked on " + item.ID); got != "blocked on "+want {
		t.Fatalf("Cite() = %q, want %q", got, "blocked on "+want)
	}
	item.Priority = 3
	item.Labels = nil
	item.Title = "New title"
	updated := NewWorkItemTitles([]beads.WorkItem{item})
	if got := updated.Cite(want); got != "(P3) New title (yoyodyne-ifd.432.28)" {
		t.Fatalf("refreshed citation = %q", got)
	}
}

func TestExistingAdjacentTitlesBecomeCompleteCitations(t *testing.T) {
	t.Parallel()
	titles := NewWorkItemTitles(titledItems())
	for _, text := range []string{
		"yoyodyne-ifd.12 (Pause on a provider usage limit)",
		"Pause on a provider usage limit (yoyodyne-ifd.12)",
		"[yoyodyne-ifd.12] Pause on a provider usage limit",
	} {
		if got := titles.Cite(text); got != "(P0) Pause on a provider usage limit (yoyodyne-ifd.12)" {
			t.Errorf("Cite(%q) = %q", text, got)
		}
	}
}

func TestCodeBlocksDoNotReceiveWorkItemCitations(t *testing.T) {
	t.Parallel()
	text := "```sh\nyoyo cost yoyodyne-ifd.12\n```\n~~~text\nyoyodyne-ifd.12\n~~~"
	if got := NewWorkItemTitles(titledItems()).Cite(text); got != text {
		t.Fatalf("code changed to %q", got)
	}
}

func TestStandingNamesComeFromTheCurrentTrackerRatherThanTheClaim(t *testing.T) {
	t.Parallel()
	sources := titledSources()
	sources.Runs = fakeRuns{incomplete: []runstate.State{{
		RunID: "run-old-title", WorkItemID: "yoyodyne-ifd.434.9", WorkItemTitle: "Old title", WorkItemLabels: []string{"old-label"},
		Status: runstate.StatusRunning, StartedAt: moment,
	}}}
	standing := ReadStanding(context.Background(), sources)
	if got := standing.WorkItemNames["yoyodyne-ifd.434.9"]; got != "(P0) Price a resumed session at what it moved by (yoyodyne-ifd.434.9)" {
		t.Fatalf("current name = %q", got)
	}
}

func TestAParenthesizedNumberInATitleDoesNotChangeTheCitedItem(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{ID: "yoyodyne-ifd.432.28", Title: "Extend the earlier work (434.9, yoyodyne-ifd.434.9) with current labels", Priority: 1}
	titles := NewWorkItemTitles(append(titledItems(), item))
	citation := "(P1) Extend the earlier work (434.9, yoyodyne-ifd.434.9) with current labels (yoyodyne-ifd.432.28)"
	if got := titles.Cite(citation); got != citation {
		t.Fatalf("Cite() = %q, want %q", got, citation)
	}
}

func TestCardProseUsesTheSameResolverWithoutChangingTheRecord(t *testing.T) {
	t.Parallel()
	titles := NewWorkItemTitles(titledItems())
	record := struct {
		Reason string `json:"reason"`
	}{Reason: "Waiting on yoyodyne-ifd.12"}
	shown := titles.CitedText(record)
	if got := shown[record.Reason]; got != "Waiting on (P0) Pause on a provider usage limit (yoyodyne-ifd.12)" {
		t.Fatalf("shown reason = %q", got)
	}
	if record.Reason != "Waiting on yoyodyne-ifd.12" {
		t.Fatal("the author record changed")
	}
}

func TestALaneClaimAndRestartReasonExpandWorkWithoutChangingItsReferences(t *testing.T) {
	t.Parallel()
	titles := NewWorkItemTitles(titledItems())
	instance := ProgramManager{
		Claims:          []ProgramManagerClaim{{What: "Waiting on yoyodyne-ifd.12", Cites: "yoyodyne-ifd.12", Reason: "yoyodyne-ifd.434.9 is unfinished"}},
		RestartRequests: []runstate.RestartRequest{{Reason: "needed for yoyodyne-ifd.12"}},
	}
	shown := citeProgramManager(instance, titles)
	claim := shown.Claims[0]
	if claim.Cites != "yoyodyne-ifd.12" || claim.SaidCites != titles.Name(claim.Cites) {
		t.Fatalf("claim reference = %+v", claim)
	}
	if claim.Reason != titles.Name("yoyodyne-ifd.434.9")+" is unfinished" || shown.RestartRequests[0].Reason != "needed for "+titles.Name("yoyodyne-ifd.12") {
		t.Fatalf("shown lane prose = %+v", shown)
	}
	if instance.Claims[0].Reason != "yoyodyne-ifd.434.9 is unfinished" || instance.RestartRequests[0].Reason != "needed for yoyodyne-ifd.12" {
		t.Fatal("the recorded lane prose changed")
	}
}

func TestUnreadableRootWorkItemReferencesRemainVisible(t *testing.T) {
	t.Parallel()
	for _, titles := range []*WorkItemTitles{nil, NewWorkItemTitles(nil)} {
		for _, id := range []string{"yoyodyne-ifd", "calc-wja"} {
			text := "Waiting on work item " + id + "."
			want := "Waiting on work item title unavailable (" + id + ")."
			if got := titles.Cite(text); got != want {
				t.Errorf("Cite(%q) = %q, want %q", text, got, want)
			}
		}
		text := "A dry-run checks read-only behavior and follow-up work. The recurring task report-triage has failed."
		if got := titles.Cite(text); got != text {
			t.Errorf("ordinary hyphenated words changed: %q", got)
		}
	}
}

func TestRefreshingACitationKeepsItsOuterIdentifierWhenFieldsChange(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{ID: "yoyodyne-ifd.432.28", Title: "Extend work (yoyodyne-ifd.12) with labels", Priority: 1}
	old := NewWorkItemTitles(append(titledItems(), item)).Name(item.ID)
	item.Priority = 3
	item.Labels = []string{"reliability"}
	item.Title = "Updated work (yoyodyne-ifd.12)"
	current := NewWorkItemTitles(append(titledItems(), item))
	for _, titles := range []*WorkItemTitles{current, nil, NewWorkItemTitles(nil)} {
		want := "Waiting on " + titles.Name(item.ID) + "; then " + titles.Name("yoyodyne-ifd.12") + "."
		text := "Waiting on " + old + "; then (P0) Pause on a provider usage limit (yoyodyne-ifd.12)."
		if got := titles.Cite(text); got != want {
			t.Errorf("Cite(%q) = %q, want %q", text, got, want)
		}
		if got := titles.Cite(want); got != want {
			t.Errorf("second rendering = %q, want %q", got, want)
		}
		text = old + ". See yoyodyne-ifd.12."
		want = titles.Name(item.ID) + ". See " + titles.Name("yoyodyne-ifd.12") + "."
		if got := titles.Cite(text); got != want {
			t.Errorf("a later reference changed the citation: %q, want %q", got, want)
		}
		for _, punctuation := range []string{";", ".", ":"} {
			text = "(P1) Extend work (yoyodyne-ifd.12)" + punctuation + " with labels (yoyodyne-ifd.432.28)"
			if got := titles.Cite(text); got != titles.Name(item.ID) {
				t.Errorf("punctuation in a title changed the item: %q", got)
			}
		}
	}
}

func TestRefreshingACitationPreservesALaterParenthesizedReference(t *testing.T) {
	t.Parallel()
	text := "(P1) First task (calc-abc). Compare (calc-def)."
	titles := NewWorkItemTitles([]beads.WorkItem{
		{ID: "calc-abc", Title: "Updated first task", Priority: 3},
		{ID: "calc-def", Title: "Comparison task", Priority: 2},
	})
	want := "(P3) Updated first task (calc-abc). Compare ((P2) Comparison task (calc-def))."
	if got := titles.Cite(text); got != want {
		t.Fatalf("Cite() = %q, want %q", got, want)
	}
	var unreadable *WorkItemTitles
	want = "title unavailable (calc-abc). Compare (calc-def)."
	if got := unreadable.Cite(text); got != want {
		t.Fatalf("unreadable Cite() = %q, want surrounding prose preserved as %q", got, want)
	}
}

func TestUnreadableRootWorkItemsAreNamedOnTheStatusLines(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{fail: context.DeadlineExceeded}}
	sources.Runs = fakeRuns{incomplete: []runstate.State{{
		RunID: "run-root", WorkItemID: "calc-wja", Status: runstate.StatusRunning,
		Phase: runstate.PhaseDeveloping, StartedAt: moment,
	}}}
	standing := ReadStanding(context.Background(), sources)
	if standing.Titles != nil {
		t.Fatal("want an unreadable tracker")
	}
	if got := standing.RenderLines(); !strings.Contains(got, "  title unavailable (calc-wja) — developing") {
		t.Fatalf("status leaves its known root item bare:\n%s", got)
	}
}
