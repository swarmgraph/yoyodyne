package backend

import "testing"

func TestOnlyALimitOnAFamilyTheSubagentsAloneUseIsTheirs(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		kind, model, subagent string
		theirs                bool
	}{
		{"seven_day_sonnet", "opus", "sonnet", true},
		{"seven_day_sonnet", "claude-opus-5-5", "claude-sonnet-5-5", true},
		{"seven_day_opus", "opus", "sonnet", false},
		{"seven_day_sonnet", "sonnet", "sonnet", false},
		{"five_hour", "opus", "sonnet", false},
		{"seven_day", "opus", "sonnet", false},
		{"seven_day_sonnet", "", "sonnet", false},
		{"seven_day_sonnet", "opus", "", false},
	} {
		if got := SubagentsOnlyLimit(test.kind, test.model, test.subagent); got != test.theirs {
			t.Fatalf("SubagentsOnlyLimit(%q, %q, %q) = %v, want %v", test.kind, test.model, test.subagent, got, test.theirs)
		}
	}
}
