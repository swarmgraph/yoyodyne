package runstate

import (
	"strings"
	"testing"
	"time"
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

// A run that leaves an environment still to be checked does not discharge its
// item, and its account names the environment, its criteria and the commit the
// change landed at.
func TestARunLeavingAnEnvironmentToCheckDoesNotDischargeItsItem(t *testing.T) {
	t.Parallel()
	landed := strings.Repeat("f", 40)
	state := State{RunID: "run-a", Status: StatusSucceeded, Integration: &Integration{TargetCommit: landed}, Environments: []EnvironmentCheck{
		{Name: "android client", Result: EnvironmentPassed, Where: "harness", Criteria: []int{1}},
		{Name: "ios client", Result: EnvironmentFailed, Where: "harness", Criteria: []int{3}},
	}}
	if state.Discharges() || !state.LeavesEnvironmentsToCheck() || !state.Parks() {
		t.Fatalf("discharges %v, leaves %v, parks %v; want the item parked open", state.Discharges(), state.LeavesEnvironmentsToCheck(), state.Parks())
	}
	says := state.EnvironmentsLeftAtLandingSays()
	for _, want := range []string{"run-a", `"ios client"`, "criterion 3", "commit " + landed, "development manager"} {
		if !strings.Contains(says, want) {
			t.Errorf("says %q, want %q", says, want)
		}
	}
	state.Environments[1].Result = EnvironmentPassed
	if !state.Discharges() || state.EnvironmentsLeftAtLandingSays() != "" || state.LandedEnvironmentsRecheck() != nil {
		t.Fatal("a run whose every environment passed was left open")
	}
}

// What a re-run is given to check names a run and at least one environment,
// and the run it ends with no developer turn succeeds complete, once.
func TestAnEnvironmentsRecheckIsHeldToItsShapeAndEndsOnce(t *testing.T) {
	t.Parallel()
	for name, bad := range map[string]EnvironmentsRecheck{
		"no run":         {Names: []string{"ios client"}},
		"no environment": {RunID: "run-0123456789abcdef0123456789abcdef"},
		"bad commit":     {RunID: "run-0123456789abcdef0123456789abcdef", Names: []string{"ios client"}, Landed: "main"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted %#v", name, bad)
		}
	}
	state := State{RunID: "run-b", Status: StatusRunning, Phase: PhaseDeveloping}
	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if changed, err := (EnvironmentsConfirmedCompleted{At: at}).apply(&state); err != nil || !changed {
		t.Fatalf("apply() = %v, %v", changed, err)
	}
	if !state.EnvironmentsConfirmed || state.Status != StatusSucceeded || state.Phase != PhaseComplete || state.CompletedAt == nil || !state.CompletedAt.Equal(at) {
		t.Fatalf("state = %#v", state)
	}
	if changed, err := (EnvironmentsConfirmedCompleted{At: at}).apply(&state); err != nil || changed {
		t.Fatalf("second apply() = %v, %v; want nothing changed", changed, err)
	}
	failed := State{RunID: "run-c", Status: StatusFailed}
	if _, err := (EnvironmentsConfirmedCompleted{At: at}).apply(&failed); err == nil {
		t.Fatal("a run that already failed was ended again")
	}
}
