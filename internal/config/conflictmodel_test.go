package config

import (
	"strings"
	"testing"
)

// execution.conflict_resolution_model: left out by default, the bounds on which
// attempts it reaches, and the refusals at load.

func TestAProjectStatingNothingNamesNoModelForAConflict(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	if kind, named := cfg.Execution.ConflictModelKind(); named {
		t.Fatalf("ConflictModelKind() = %q, want none named by default", kind)
	}
	priority, lines := 2, 10
	if got := cfg.Execution.ConflictModelEligible(nil, &priority, ConflictModelConflict{Files: 1, Lines: &lines}); got.Eligible {
		t.Fatalf("an attempt was eligible with the setting left out: %+v", got)
	}
}

func TestTheConflictModelSettingLoadsWithItsOrigin(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  conflict_resolution_model: cheaper
`, nil)
	if kind, named := resolved.Config.Execution.ConflictModelKind(); !named || kind != ModelKindCheaper {
		t.Fatalf("ConflictModelKind() = %q, %v; want cheaper", kind, named)
	}
	if resolved.Origins["execution.conflict_resolution_model"] == "" {
		t.Fatal("no origin recorded for execution.conflict_resolution_model")
	}
}

func TestTheConflictModelReachesOnlyASmallConflictOnALowRiskItem(t *testing.T) {
	t.Parallel()

	execution := Execution{ConflictResolutionModel: ModelKindCheaper, HighRiskLabelsAdded: []string{"billing"}}
	one, zero := 1, 0
	small, edge, large := 40, ConflictModelMaxLines, ConflictModelMaxLines+1
	for _, tc := range []struct {
		name     string
		labels   []string
		priority *int
		conflict ConflictModelConflict
		eligible bool
		reason   string
	}{
		{"small and low risk", nil, &one, ConflictModelConflict{Files: 2, Lines: &small}, true, "its conflicts lie in 2 file(s) and 40 line(s)"},
		{"at the bounds", nil, &one, ConflictModelConflict{Files: ConflictModelMaxFiles, Lines: &edge}, true, "3 file(s) and 200 line(s)"},
		{"too many files", nil, &one, ConflictModelConflict{Files: 3, Omitted: 1, Lines: &small}, false, "the conflicts lie in 4 files, more than 3"},
		{"too many lines", nil, &one, ConflictModelConflict{Files: 1, Lines: &large}, false, "the conflicts are 201 lines, more than 200"},
		{"lines not counted", nil, &one, ConflictModelConflict{Files: 1}, false, "could not be counted"},
		{"no file named", nil, &one, ConflictModelConflict{Lines: &small}, false, "Git named no conflicting file"},
		{"priority 0", nil, &zero, ConflictModelConflict{Files: 1, Lines: &small}, false, "the item is priority 0"},
		{"priority unknown", nil, nil, ConflictModelConflict{Files: 1, Lines: &small}, false, "treated as priority 0"},
		{"fixed high risk label", []string{"security"}, &one, ConflictModelConflict{Files: 1, Lines: &small}, false, `high risk label "security"`},
		{"added high risk label", []string{"billing"}, &one, ConflictModelConflict{Files: 1, Lines: &small}, false, `high risk label "billing"`},
	} {
		got := execution.ConflictModelEligible(tc.labels, tc.priority, tc.conflict)
		if got.Eligible != tc.eligible || !strings.Contains(got.Reason, tc.reason) {
			t.Fatalf("%s: ConflictModelEligible() = %+v, want eligible=%v saying %q", tc.name, got, tc.eligible, tc.reason)
		}
	}
}

func TestConflictModelValuesOutsideTheirBoundsAreRefusedAtLoad(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"lightest", "opus"} {
		_, err := loadProjectError(t, minimalProjectConfig+`execution:
  conflict_resolution_model: `+value+`
`, nil)
		want := `execution.conflict_resolution_model is "` + value + `"; it must be "cheaper" or "stronger", or left out`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("conflict_resolution_model: %s loaded with %v, want it refused saying %q", value, err, want)
		}
	}
}

func TestTheCheaperConflictModelIsRefusedForADeveloperWhoseProviderNamesNone(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents:    map[string]AgentConfig{"developer": {Role: "developer", Backend: "codex"}},
		Execution: Execution{ConflictResolutionModel: ModelKindCheaper},
	}
	problems := conflictModelProblems(cfg)
	if len(problems) != 1 || !strings.Contains(problems[0], `provider codex, which developer agent "developer" runs on, names no cheaper model`) {
		t.Fatalf("conflictModelProblems() = %v, want the codex developer refused", problems)
	}
	cfg.Execution.ConflictResolutionModel = ModelKindStronger
	if problems := conflictModelProblems(cfg); len(problems) != 0 {
		t.Fatalf("the stronger kind was refused: %v", problems)
	}
}
