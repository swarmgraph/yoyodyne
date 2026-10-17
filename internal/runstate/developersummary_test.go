package runstate

import (
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
		{Text: strings.Repeat("x", MaxRecordedTextBytes+1), Content: "content"},
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

func TestDeveloperSummaryIsBoundedWithAVisibleCut(t *testing.T) {
	t.Parallel()
	text := "Implemented and checked compatibility. " + strings.Repeat("é", MaxRecordedTextBytes)
	kept := RecordDeveloperSummary(text)
	if len(kept) > MaxRecordedTextBytes || !utf8.ValidString(kept) ||
		!strings.HasPrefix(kept, "Implemented and checked compatibility.") || !strings.HasSuffix(kept, reviewSummaryCutNote) {
		t.Fatalf("a bounded summary lost its head or did not declare its cut: %q", kept)
	}
	if got := RecordDeveloperSummary("implemented"); got != "implemented" {
		t.Fatalf("an account within the bound changed: %q", got)
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
