package backend

import (
	"strings"
	"testing"
)

// A run's durable failure is what somebody reads weeks later, and the provider's
// name for the ending is a category rather than a diagnosis: every transient
// overload, refused request, and exhausted context that ended a run arrived as
// `api_error`, and the records made before this carry nothing else. The
// provider's own message is what tells them apart, so it is kept beside the
// category.
func TestDescribeFailureKeepsTheProvidersOwnWordsBesideItsCategory(t *testing.T) {
	t.Parallel()

	overloaded := "API Error: 529 Overloaded. This is a server-side issue, usually temporary."
	described := RunResult{StopReason: "api_error", FinalText: overloaded}.DescribeFailure()
	if described != "api_error: "+overloaded {
		t.Fatalf("described = %q", described)
	}

	// A provider that only names the category must not be reported as having
	// said it twice, and one that says nothing at all still leaves a reason.
	for _, testCase := range []struct {
		name   string
		result RunResult
		want   string
	}{
		{name: "category only", result: RunResult{StopReason: "api_error"}, want: "api_error"},
		{name: "same words", result: RunResult{StopReason: "api_error", FinalText: "api_error"}, want: "api_error"},
		{
			name:   "message repeats the category",
			result: RunResult{StopReason: "api_error", FinalText: "the provider reported api_error"},
			want:   "the provider reported api_error",
		},
		{name: "message only", result: RunResult{FinalText: "it went wrong"}, want: "it went wrong"},
		{name: "nothing", result: RunResult{}, want: "unknown provider failure"},
		// Whitespace is not a message, and neither is a category of it: a result
		// carrying only blanks has said nothing rather than said something empty.
		{name: "blanks", result: RunResult{StopReason: "  ", FinalText: "\n\t"}, want: "unknown provider failure"},
	} {
		if described := testCase.result.DescribeFailure(); described != testCase.want {
			t.Fatalf("%s: described = %q, want %q", testCase.name, described, testCase.want)
		}
	}

	// A final reply that runs to pages is a reason nobody can read and a work
	// item note nobody wants, so what is kept is bounded and says it was cut.
	long := RunResult{
		StopReason: "api_error",
		FinalText:  strings.Repeat("a", maxFailureDetailBytes*2),
	}.DescribeFailure()
	if len(long) > maxFailureDetailBytes+64 || !strings.HasSuffix(long, "...") {
		t.Fatalf("described %d bytes ending %q", len(long), long[len(long)-8:])
	}
}

// The refusal for a provider that is not there names where it was looked for
// when the adapter said, and is the refusal it always was when it did not.
func TestNotInstalledNamesThePathItWasLookedForOn(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")

	if got, want := NotFound("claude").NotInstalled("claude-code"), "the claude-code backend cannot run in this environment: claude was not found on PATH /usr/bin:/bin"; got != want {
		t.Fatalf("NotInstalled() = %q, want %q", got, want)
	}
	if got, want := (Availability{}).NotInstalled("codex"), "the codex backend is not installed"; got != want {
		t.Fatalf("NotInstalled() = %q, want %q", got, want)
	}
}
