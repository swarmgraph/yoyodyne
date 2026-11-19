package config

import (
	"strings"
	"testing"
	"time"
)

// The least time between two moves onto a new build is three hours unless the
// project says otherwise.
func TestTheBuildMoveIntervalIsThreeHoursUnlessTheProjectSetsIt(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.Execution.BuildMoveEvery(); got != 3*time.Hour {
		t.Fatalf("inherited build_move_interval = %s, want 3h", got)
	}
	if origin := inherited.Origins["execution.build_move_interval"]; origin != BuiltinV1 {
		t.Fatalf("build_move_interval origin = %q, want %q", origin, BuiltinV1)
	}
	if got := (Execution{}).BuildMoveEvery(); got != 3*time.Hour {
		t.Fatalf("a configuration built in code reads build_move_interval as %s, want 3h", got)
	}

	for body, want := range map[string]time.Duration{
		"execution:\n  build_move_interval: 1h\n":  time.Hour,
		"execution:\n  build_move_interval: 6h\n":  6 * time.Hour,
		"execution:\n  build_move_interval: 24h\n": 24 * time.Hour,
	} {
		overridden := loadProject(t, minimalProjectConfig+body, nil)
		if got := overridden.Config.Execution.BuildMoveEvery(); got != want {
			t.Fatalf("%q: build_move_interval = %s, want %s", body, got, want)
		}
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.BuildMoveInterval; got != Duration(3*time.Hour) {
		t.Fatalf("generated build_move_interval = %s, want 3h", got)
	}
}

// A value under one hour or over twenty-four hours is refused when the
// configuration is read, saying what the limits are.
func TestABuildMoveIntervalOutsideOneToTwentyFourHoursIsRefused(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"59m", "30m", "24h1m", "48h", "-1h"} {
		_, err := loadProjectError(t, minimalProjectConfig+"execution:\n  build_move_interval: "+value+"\n", nil)
		if err == nil || !strings.Contains(err.Error(), "must be at least one hour and at most twenty-four hours") {
			t.Fatalf("build_move_interval %s: LoadResolved() error = %v, want it refused naming the limits", value, err)
		}
	}
}
