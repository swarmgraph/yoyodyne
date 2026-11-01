package runstate

import (
	"strings"
	"testing"
	"time"
)

// A run waiting to try an install step again says which step, why, and the
// local time of the next try with its zone, and a wait the harness would never
// write is refused when the record is saved.
func TestAnInstallWaitNamesTheStepAndTheLocalTimeOfTheNextTry(t *testing.T) {
	t.Parallel()

	next := time.Date(2026, 10, 31, 21, 5, 0, 0, time.UTC)
	wait := InstallWait{Name: "npm packages", Retry: 2, NextAt: next}
	if err := wait.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	said := wait.Describe()
	for _, want := range []string{`"npm packages"`, "network or the package registry", "trying again at " + next.Local().Format("15:04 MST"), "try 2 of 3"} {
		if !strings.Contains(said, want) {
			t.Fatalf("Describe() = %q, want it to say %q", said, want)
		}
	}
	for _, refused := range []InstallWait{{Retry: 1, NextAt: next}, {Name: "npm packages", Retry: 0, NextAt: next}, {Name: "npm packages", Retry: InstallRetries + 1, NextAt: next}, {Name: "npm packages", Retry: 1}} {
		if err := refused.Validate(); err == nil {
			t.Fatalf("Validate(%#v) = nil, want it refused", refused)
		}
	}
}
