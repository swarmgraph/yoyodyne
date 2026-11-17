package config

import (
	"strings"
	"testing"
	"time"
)

// The checks run on the branch's head are off until a project lists them, and
// run every four hours unless it says otherwise.
func TestMainChecksAreOffByDefaultAndRunEveryFourHours(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.MainChecks; len(got) != 0 {
		t.Fatalf("inherited main_checks = %v, want none", got)
	}
	if got := inherited.Config.Execution.MainCheckEvery(); got != 4*time.Hour {
		t.Fatalf("inherited main_check_interval = %s, want 4h", got)
	}
	if got := (Execution{}).MainCheckEvery(); got != 4*time.Hour {
		t.Fatalf("a configuration built in code reads main_check_interval as %s, want 4h", got)
	}

	overridden := loadProject(t, minimalProjectConfig+`main_checks:
  - make race
execution:
  main_check_interval: 2h
  heavy_checks:
    make race: 1
`, nil)
	if got := overridden.Config.MainChecks; len(got) != 1 || got[0] != "make race" {
		t.Fatalf("main_checks = %v, want make race", got)
	}
	if got := overridden.Config.Execution.MainCheckEvery(); got != 2*time.Hour {
		t.Fatalf("main_check_interval = %s, want 2h", got)
	}
	if origin := overridden.Origins["main_checks"]; origin == "" || origin == BuiltinV1 {
		t.Fatalf("main_checks origin = %q, want the project file", origin)
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.MainCheckInterval; got != Duration(4*time.Hour) {
		t.Fatalf("generated main_check_interval = %s, want 4h", got)
	}
}

func TestMainChecksRefuseAnEmptyCommandAndAShortInterval(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		"main_checks:\n  - \"  \"\n":              "main check 0 cannot be empty",
		"execution:\n  main_check_interval: 5m\n": "execution.main_check_interval must be at least 30m0s",
	} {
		_, err := loadProjectError(t, minimalProjectConfig+body, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("LoadResolved() error = %v, want one saying %q", err, want)
		}
	}
}

func TestADevelopmentManagerMayApplyTheMainChecksAndTheirInterval(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"main_checks", "execution.main_check_interval"} {
		if err := RoleMayApply(key); err != nil {
			t.Fatalf("RoleMayApply(%q) = %v, want it applicable", key, err)
		}
	}
}
