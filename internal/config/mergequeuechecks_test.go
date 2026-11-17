package config

import (
	"strings"
	"testing"
)

func TestAProjectDeclaresWhichConfiguredChecksItsMergeQueueRuns(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.DeclaresMergeQueueChecks() || inherited.Config.MergeQueueRaceCheck != "" {
		t.Fatalf("inherited queue checks = %v and race check %q, want none: every configured check runs whole", inherited.Config.MergeQueueChecks, inherited.Config.MergeQueueRaceCheck)
	}
	declared := loadProject(t, minimalProjectConfig+`checks:
  - make test
  - make race
merge_queue_checks:
  - make test
merge_queue_race_check: make race
`, nil)
	if got := declared.Config.MergeQueueChecks; len(got) != 1 || got[0] != "make test" || !declared.Config.DeclaresMergeQueueChecks() {
		t.Fatalf("merge_queue_checks = %v, want make test", got)
	}
	if got := declared.Config.MergeQueueRaceCheck; got != "make race" {
		t.Fatalf("merge_queue_race_check = %q, want make race", got)
	}
	if origin := declared.Origins["merge_queue_checks"]; origin == "" || origin == BuiltinV1 {
		t.Fatalf("merge_queue_checks origin = %q, want the project file", origin)
	}
}

func TestTheMergeQueueChecksMustBeConfiguredChecks(t *testing.T) {
	t.Parallel()

	const checks = "checks:\n  - make test\n  - make race\n"
	for body, want := range map[string]string{
		"merge_queue_checks:\n  - make lint\n":                                                   `merge_queue_checks names "make lint", which is no configured check`,
		"merge_queue_checks:\n  - \"  \"\n":                                                      "merge queue check 0 cannot be empty",
		"merge_queue_checks:\n  - make test\n  - make test\n":                                    `names "make test" more than once`,
		"merge_queue_race_check: make race\n":                                                    "needs merge_queue_checks",
		"merge_queue_checks:\n  - make test\nmerge_queue_race_check: make lint\n":                `merge_queue_race_check names "make lint", which is no configured check`,
		"merge_queue_checks:\n  - make test\n  - make race\nmerge_queue_race_check: make race\n": "leave it off that list",
	} {
		_, err := loadProjectError(t, minimalProjectConfig+checks+body, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("LoadResolved() with %q error = %v, want one saying %q", body, err, want)
		}
	}
}
