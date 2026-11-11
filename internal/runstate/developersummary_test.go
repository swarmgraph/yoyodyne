package runstate

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDeveloperSummaryRequiresAnAccountAndItsBinding(t *testing.T) {
	t.Parallel()
	for _, summary := range []DeveloperSummary{
		{Content: "content", Attempt: 0},
		{Text: "implemented", Attempt: 0},
		{Text: "implemented", Content: "content", Attempt: -1},
		{Text: strings.Repeat("x", MaxDeveloperSummaryBytes+1), Content: "content"},
		{Text: "implemented", Content: "content", WholeBytes: -1},
	} {
		if err := summary.Validate(); err == nil {
			t.Errorf("Validate() accepted an incomplete summary: %#v", summary)
		}
	}
	summary := DeveloperSummary{Text: "implemented and verified", Content: "content", Attempt: 0}
	if err := summary.Validate(); err != nil {
		t.Fatalf("Validate() rejected a bound account: %v", err)
	}
}

// A summary past the 4 KiB bound summaries were once kept to is recorded whole,
// because that bound cut ordinary summaries before their answers to the item's
// done-conditions.
func TestADeveloperSummaryPastTheOldBoundIsRecordedWhole(t *testing.T) {
	t.Parallel()
	text := "Condition 1 is met. " + strings.Repeat("Condition detail. ", 600) + "Condition 6 is met."
	if len(text) <= 4<<10 || len(text) > MaxDeveloperSummaryBytes {
		t.Fatalf("test summary is %d bytes, want it between the old bound and the new one", len(text))
	}
	summary := RecordDeveloperSummary(text, "content", 2)
	if summary.Text != text || summary.WholeBytes != 0 || summary.CutByHarness() || summary.CutStatement() != "" {
		t.Fatalf("a summary within the bound was not recorded whole: cut %v, whole %d, %d of %d bytes", summary.CutByHarness(), summary.WholeBytes, len(summary.Text), len(text))
	}
	if summary.Content != "content" || summary.Attempt != 2 {
		t.Fatalf("summary lost its binding: %#v", summary)
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("Validate() rejected a summary within the bound: %v", err)
	}
	if got := RecordDeveloperSummary("implemented", "content", 0); got.Text != "implemented" || got.CutByHarness() {
		t.Fatalf("a short account changed: %#v", got)
	}
}

func TestADeveloperSummaryPastTheBoundIsCutWithTheCutDeclared(t *testing.T) {
	t.Parallel()
	text := "Implemented and checked compatibility. " + strings.Repeat("é", MaxDeveloperSummaryBytes)
	summary := RecordDeveloperSummary(text, "content", 0)
	if len(summary.Text) > MaxDeveloperSummaryBytes || !utf8.ValidString(summary.Text) ||
		!strings.HasPrefix(summary.Text, "Implemented and checked compatibility.") || !strings.HasSuffix(summary.Text, developerSummaryCutNote) {
		t.Fatalf("a bounded summary lost its head or did not declare its cut: %q", summary.Text[len(summary.Text)-200:])
	}
	if summary.WholeBytes != len(text) || !summary.CutByHarness() {
		t.Fatalf("whole = %d, cut = %v; want the whole size %d recorded", summary.WholeBytes, summary.CutByHarness(), len(text))
	}
	statement := summary.CutStatement()
	for _, want := range []string{"The harness cut this summary", fmt.Sprintf("%d bytes", len(text)), fmt.Sprintf("at most %d", MaxDeveloperSummaryBytes), "not the developer's"} {
		if !strings.Contains(statement, want) {
			t.Errorf("cut statement %q does not say %q", statement, want)
		}
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("Validate() rejected a cut summary: %v", err)
	}
}

// A summary recorded under the old 4 KiB bound ended on the review summary's
// note and carried no size; it is still declared as the harness's cut.
func TestASummaryCutUnderTheOldBoundIsStillDeclaredAsTheHarnesss(t *testing.T) {
	t.Parallel()
	old := DeveloperSummary{Text: "Condition 1 is met." + reviewSummaryCutNote, Content: "content"}
	if !old.CutByHarness() || !strings.Contains(old.CutStatement(), "was not recorded") {
		t.Fatalf("an old cut summary was not declared as cut: %q", old.CutStatement())
	}
}

func TestSavingADeveloperSummaryKeepsEachEarlierAttemptsAccount(t *testing.T) {
	t.Parallel()
	var state State
	state.SaveDeveloperSummary(DeveloperSummary{Text: "first reply", Content: "c0", Attempt: 0})
	state.SaveDeveloperSummary(DeveloperSummary{Text: "attempt 0 re-asked", Content: "c0", Attempt: 0})
	if len(state.EarlierDeveloperSummaries) != 0 || state.DeveloperSummary.Text != "attempt 0 re-asked" {
		t.Fatalf("a second account of one attempt did not replace the first: %#v", state)
	}
	state.SaveDeveloperSummary(DeveloperSummary{Text: "attempt 1", Content: "c1", Attempt: 1})
	if len(state.EarlierDeveloperSummaries) != 1 || state.EarlierDeveloperSummaries[0].Text != "attempt 0 re-asked" || state.DeveloperSummary.Attempt != 1 {
		t.Fatalf("a repair's account did not keep the earlier attempt's: %#v", state)
	}
	for attempt := 2; attempt <= MaxEarlierDeveloperSummaries+3; attempt++ {
		state.SaveDeveloperSummary(DeveloperSummary{Text: "attempt", Content: "c", Attempt: attempt})
	}
	if len(state.EarlierDeveloperSummaries) != MaxEarlierDeveloperSummaries || state.EarlierDeveloperSummariesDropped != 3 {
		t.Fatalf("earlier summaries = %d, dropped = %d, want the bound kept and the rest counted", len(state.EarlierDeveloperSummaries), state.EarlierDeveloperSummariesDropped)
	}
	if got := state.EarlierDeveloperSummaries[0].Attempt; got != 3 {
		t.Fatalf("oldest kept attempt = %d, want the oldest ones let go first", got)
	}
	state.EarlierDeveloperSummaries = append(state.EarlierDeveloperSummaries, DeveloperSummary{Text: "x", Content: "c"})
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "earlier developer summaries") {
		t.Fatalf("Validate() accepted more earlier summaries than the bound: %v", err)
	}
}
