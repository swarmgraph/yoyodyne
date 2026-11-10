package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func landingFixture(t *testing.T) (*runstate.LandingHoldStore, string) {
	t.Helper()
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewLandingHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewLandingHoldStore() error = %v", err)
	}
	return store, configPath
}

func TestLandingHoldNeedsADuration(t *testing.T) {
	store, configPath := landingFixture(t)
	_, stderr, code := runCLI(t, "landing", "hold", "staging", "--reason", "release", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "needs a duration") {
		t.Fatalf("code = %d, stderr = %q, want 1 and the plain message", code, stderr)
	}
	_, stderr, code = runCLI(t, "landing", "hold", "staging", "--for", "5h", "--reason", "release", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "at most 4 hours") {
		t.Fatalf("code = %d, stderr = %q, want 1 and the four-hour message", code, stderr)
	}
	_, stderr, code = runCLI(t, "landing", "hold", "staging", "--for", "soon", "--reason", "release", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, `"soon" is not one`) {
		t.Fatalf("code = %d, stderr = %q, want 1 and the plain message", code, stderr)
	}
	if holds, err := store.List(time.Now()); err != nil || len(holds) != 0 {
		t.Fatalf("List() = %v, %v, want nothing recorded", holds, err)
	}
}

func TestLandingHoldNeedsAReason(t *testing.T) {
	store, configPath := landingFixture(t)
	_, stderr, code := runCLI(t, "landing", "hold", "staging", "--for", "1h", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "needs a reason") {
		t.Fatalf("code = %d, stderr = %q, want 1 and the plain message", code, stderr)
	}
	if holds, _ := store.List(time.Now()); len(holds) != 0 {
		t.Fatalf("List() = %v, want nothing recorded", holds)
	}
}

func TestLandingHoldAndReleaseRoundTrip(t *testing.T) {
	store, configPath := landingFixture(t)
	stdout, stderr, code := runCLI(t, "landing", "hold", "staging", "--for", "4h", "--reason", "cutting release", "--config", configPath)
	if code != 0 {
		t.Fatalf("hold code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"landing on staging is held until", "cutting release", "carry on", "evidence kept", "`yoyo pause`", "this machine only", "`yoyo landing release staging`"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("hold stdout = %q, want it to mention %q", stdout, want)
		}
	}
	holds, err := store.List(time.Now())
	if err != nil || len(holds) != 1 || holds[0].TargetBranch != "staging" || holds[0].Reason != "cutting release" {
		t.Fatalf("List() = %v, %v, want the staging hold", holds, err)
	}

	stdout, stderr, code = runCLI(t, "landing", "release", "staging", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "released the hold on landing on staging") {
		t.Fatalf("release code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if holds, _ := store.List(time.Now()); len(holds) != 0 {
		t.Fatalf("List() after release = %v, want none", holds)
	}
	stdout, _, code = runCLI(t, "landing", "release", "staging", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "was not held") {
		t.Fatalf("second release code = %d, stdout = %q, want it to say nothing was held", code, stdout)
	}
}

func TestLandingUsageErrorsExitTwo(t *testing.T) {
	_, configPath := landingFixture(t)
	for _, args := range [][]string{
		{"landing"},
		{"landing", "hold", "--for", "1h", "--reason", "x"},
		{"landing", "hold", "a", "b", "--for", "1h", "--reason", "x"},
		{"landing", "release"},
		{"landing", "release", "a", "b"},
		{"landing", "hold", "staging", "--nonsense"},
		{"landing", "mend"},
	} {
		if _, _, code := runCLI(t, append(args, "--config", configPath)...); code != 2 {
			t.Fatalf("%v code = %d, want 2", args, code)
		}
	}
}

func TestLandingHoldRefusesAnAgentProcess(t *testing.T) {
	store, configPath := landingFixture(t)
	t.Setenv("YOYODYNE_AGENT_ROLE", "developer")
	_, stderr, code := runCLI(t, "landing", "hold", "staging", "--for", "1h", "--reason", "x", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "is refused") {
		t.Fatalf("code = %d, stderr = %q, want a refusal", code, stderr)
	}
	if holds, _ := store.List(time.Now()); len(holds) != 0 {
		t.Fatalf("List() = %v, want nothing recorded", holds)
	}
}
