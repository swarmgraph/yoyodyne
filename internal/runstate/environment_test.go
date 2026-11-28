package runstate

import (
	"strings"
	"testing"
)

// Each environment's check is held to the shapes the harness writes, and its
// output to the 16 KiB a record keeps.
func TestAnEnvironmentCheckIsHeldToTheShapesTheHarnessWrites(t *testing.T) {
	t.Parallel()
	good := EnvironmentCheck{Name: "web ui", Result: EnvironmentTimedOut, Where: "harness", Criteria: []int{1}}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a good record accepted", err)
	}
	for name, bad := range map[string]EnvironmentCheck{
		"no name":      {Result: EnvironmentPassed, Where: "developer"},
		"no result":    {Name: "web ui", Where: "developer"},
		"no place":     {Name: "web ui", Result: EnvironmentFailed, Where: "elsewhere"},
		"long output":  {Name: "web ui", Result: EnvironmentFailed, Where: "developer", Output: strings.Repeat("x", MaxInstallOutputBytes+1)},
		"negative run": {Name: "web ui", Result: EnvironmentFailed, Where: "developer", DurationMilliseconds: -1},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted %#v", name, bad)
		}
	}
	state := State{Environments: []EnvironmentCheck{{Name: "x", Result: "unknown", Where: "developer"}}}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "environments[0]") {
		t.Fatalf("State.Validate() = %v, want the bad environment named", err)
	}
}

// Only the environments the latest round found not usable are still to be
// checked, and a round where every one passes leaves none.
func TestOnlyTheLatestRoundsFailuresAreStillToBeChecked(t *testing.T) {
	t.Parallel()
	state := State{RunID: "run-a"}
	changed, err := EnvironmentsChecked{Checks: []EnvironmentCheck{
		{Name: "android client", Result: EnvironmentPassed, Where: "harness", Criteria: []int{1, 2}},
		{Name: "ios client", Result: EnvironmentFailed, Where: "harness", Criteria: []int{3}},
	}}.apply(&state)
	if err != nil || !changed {
		t.Fatalf("apply() = %v, %v", changed, err)
	}
	if pending := state.EnvironmentsStillToCheck(); len(pending) != 1 || pending[0].Name != "ios client" {
		t.Fatalf("still to check = %#v", pending)
	}
	if says := state.EnvironmentsStillToCheckSays(); !strings.Contains(says, `"ios client", which criterion 3 blocks`) {
		t.Fatalf("says %q", says)
	}
	if _, err := (EnvironmentsChecked{Checks: []EnvironmentCheck{
		{Name: "android client", Result: EnvironmentPassed, Where: "harness"},
		{Name: "ios client", Result: EnvironmentPassed, Where: "harness"},
	}}).apply(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.EnvironmentsStillToCheck()) != 0 || state.EnvironmentsStillToCheckSays() != "" {
		t.Fatalf("still to check after every one passed = %#v", state.EnvironmentsStillToCheck())
	}
	if state.EnvironmentsMissingSays() != "" {
		t.Fatal("a run not held for its environments says it is")
	}
}

// A machine environment's instruction is quoted for a person, and said to be
// one the harness never runs.
func TestAMissingMachineEnvironmentQuotesItsInstruction(t *testing.T) {
	t.Parallel()
	said := EnvironmentCheck{Name: "ios client", Result: EnvironmentFailed, Where: "harness", Criteria: []int{2, 4}, Machine: true, HowToInstall: "xcodebuild -downloadPlatform iOS"}.Missing()
	for _, want := range []string{`"ios client"`, "criteria 2, 4", `"xcodebuild -downloadPlatform iOS"`, "a person has to install on this machine", "the harness never runs that"} {
		if !strings.Contains(said, want) {
			t.Fatalf("missing says %q, want %q", said, want)
		}
	}
}
