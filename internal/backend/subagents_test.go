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

// A usage limit is named in the words a person reads, never by the provider's
// key; one the harness does not recognize says so first and gives the key
// after.
func TestALimitIsNamedInOrdinaryWords(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ kind, named string }{
		{"seven_day_sonnet", "Sonnet weekly limit"},
		{"seven_day_opus", "Opus weekly limit"},
		{"seven_day", "weekly usage limit"},
		{"five_hour", "five-hour usage limit"},
		{"seven_day_overage_included", "weekly limit with paid usage beyond the plan counted in"},
		{"overage", "limit on paid usage beyond the plan"},
		{"", "usage limit the provider did not name"},
		{"monthly_sonnet", `unrecognized usage limit (named "monthly_sonnet")`},
		{"daily_tokens", `unrecognized usage limit (named "daily_tokens")`},
	} {
		if got := LimitName(test.kind); got != test.named {
			t.Fatalf("LimitName(%q) = %q, want %q", test.kind, got, test.named)
		}
	}
}
