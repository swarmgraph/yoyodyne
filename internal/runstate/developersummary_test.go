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
