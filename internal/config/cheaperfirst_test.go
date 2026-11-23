package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Models by kind, developers starting on the cheaper model, and the high risk
// labels that keep work off it: the defaults a project stating nothing gets,
// the bounds held in code, and the refusals at load.

func TestAProjectStatingNothingGetsTheShippedModelKindsAndCheaperFirstOff(t *testing.T) {
	t.Parallel()

	execution := loadProject(t, minimalProjectConfig, nil).Config.Execution
	if execution.DeveloperCheaperFirst.Enabled {
		t.Fatal("developer_cheaper_first is on for a project that stated nothing")
	}
	for kind, want := range map[ModelKind]string{ModelKindStronger: "opus", ModelKindCheaper: "sonnet", ModelKindLightest: "haiku"} {
		if got, named := execution.ModelOfKind(domain.BackendClaudeCode, kind); !named || got != want {
			t.Fatalf("Claude Code's %s model = %q (%v), want %q", kind, got, named, want)
		}
		if got, named := execution.ModelOfKind(domain.BackendCodex, kind); named {
			t.Fatalf("Codex names %q as its %s model by default, want none", got, kind)
		}
	}
	if got := execution.HighRiskLabels(); !reflect.DeepEqual(got, FixedHighRiskLabels) {
		t.Fatalf("high risk labels = %v, want the four fixed ones", got)
	}
	if got := execution.DeveloperCheaperFirst.TrialTarget(); got != 40 {
		t.Fatalf("trial target = %d, want 40", got)
	}
	// Turned off, nothing is eligible.
	two := 2
	if execution.CheaperFirstEligible(nil, &two).Eligible {
		t.Fatal("an item was eligible with developer_cheaper_first off")
	}
}

func TestTheCheaperFirstSettingsLoadAndAProviderNamesItsOwnKinds(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  model_kinds:
    codex:
      stronger: gpt-5
      cheaper: gpt-5-mini
  developer_cheaper_first:
    enabled: true
    labels: [docs, tests]
    priorities: [2, 3]
    trial_runs: 60
  high_risk_labels: [billing]
`, nil)
	execution := resolved.Config.Execution
	if got, named := execution.ModelOfKind(domain.BackendCodex, ModelKindCheaper); !named || got != "gpt-5-mini" {
		t.Fatalf("Codex's cheaper model = %q (%v), want gpt-5-mini", got, named)
	}
	if _, named := execution.ModelOfKind(domain.BackendCodex, ModelKindLightest); named {
		t.Fatal("Codex names a lightest model nobody named")
	}
	if got, _ := execution.ModelOfKind(domain.BackendClaudeCode, ModelKindCheaper); got != "sonnet" {
		t.Fatalf("Claude Code's cheaper model = %q, want the default sonnet kept", got)
	}
	want := DeveloperCheaperFirst{Enabled: true, Labels: []string{"docs", "tests"}, Priorities: []int{2, 3}, TrialRuns: 60}
	if !reflect.DeepEqual(execution.DeveloperCheaperFirst, want) {
		t.Fatalf("developer_cheaper_first = %+v, want %+v", execution.DeveloperCheaperFirst, want)
	}
	if got := execution.HighRiskLabels(); !reflect.DeepEqual(got, append(append([]string(nil), FixedHighRiskLabels...), "billing")) {
		t.Fatalf("high risk labels = %v, want the four fixed ones and billing", got)
	}
	if origin := resolved.Origins["execution.developer_cheaper_first.enabled"]; !strings.Contains(origin, "config.yaml") {
		t.Fatalf("origin = %q, want the project file", origin)
	}

	two, one, three := 2, 1, 3
	for _, replay := range []struct {
		name     string
		labels   []string
		priority *int
		eligible bool
		reason   string
	}{
		{name: "eligible", labels: []string{"docs"}, priority: &two, eligible: true, reason: `carries "docs"`},
		{name: "priority not named", labels: []string{"docs"}, priority: &one, reason: "priorities does not name"},
		{name: "no eligible label", labels: []string{"reliability"}, priority: &three, reason: "carries no label"},
		{name: "priority unknown", labels: []string{"docs"}, reason: "not recorded"},
		{name: "added high risk", labels: []string{"docs", "billing"}, priority: &two, reason: `high risk label "billing"`},
		{name: "fixed high risk", labels: []string{"docs", "security"}, priority: &two, reason: `high risk label "security"`},
	} {
		got := execution.CheaperFirstEligible(replay.labels, replay.priority)
		if got.Eligible != replay.eligible || !strings.Contains(got.Reason, replay.reason) {
			t.Fatalf("%s: eligibility = %+v, want eligible %v saying %q", replay.name, got, replay.eligible, replay.reason)
		}
	}
}

func TestPriorityZeroIsNeverEligibleWhateverTheConfigurationSays(t *testing.T) {
	t.Parallel()

	execution := Execution{DeveloperCheaperFirst: DeveloperCheaperFirst{Enabled: true, Priorities: []int{0, 1}}}
	zero := 0
	if got := execution.CheaperFirstEligible(nil, &zero); got.Eligible {
		t.Fatalf("a priority 0 item was eligible: %+v", got)
	}
	// And a high risk label stays high risk even where the project's added
	// list leaves it out: the list adds and never removes.
	execution.HighRiskLabelsAdded = []string{"billing"}
	two := 2
	for _, label := range FixedHighRiskLabels {
		if got := execution.CheaperFirstEligible([]string{label}, &two); got.Eligible {
			t.Fatalf("an item labelled %q was eligible: %+v", label, got)
		}
	}
}

func TestCheaperFirstValuesOutsideTheirBoundsAreRefusedAtLoad(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  model_kinds:
    nowhere:
      cheaper: small
    claude-code:
      cheaper: "--yes"
  developer_cheaper_first:
    enabled: true
    labels: [docs, security, docs]
    priorities: [0, 5, 2, 2]
    trial_runs: 10
  high_risk_labels: ["not a label"]
`, nil)
	if err == nil {
		t.Fatal("a configuration with every value out of bounds loaded")
	}
	for _, want := range []string{
		`execution.model_kinds names provider "nowhere"`,
		`execution.model_kinds.claude-code.cheaper: model selector "--yes" must be a single model name`,
		"names priority 0, which is never eligible",
		"names priority 5; the tracker's priorities run from 0 to 4",
		"names priority 2 twice",
		"trial_runs is 10; it must be between 40 and 1000",
		`execution.developer_cheaper_first.labels names "security", which is a high risk label`,
		`execution.developer_cheaper_first.labels names "docs" twice`,
		"execution.high_risk_labels entry 1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v\nwant it to say %q", err, want)
		}
	}
}

func TestCheaperFirstOnForADeveloperWhoseProviderNamesNoCheaperModelIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    role: developer
    backend: codex
    model: gpt-5
execution:
  developer_cheaper_first:
    enabled: true
`, nil)
	if err == nil {
		t.Fatal("cheaper-first loaded for a developer on a provider naming no cheaper model")
	}
	want := `execution.developer_cheaper_first is on, but provider codex, which developer agent "developer" runs on, names no cheaper model`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v\nwant it to say %q", err, want)
	}
}

func TestTheDevelopmentManagerMayApplyTheCheaperFirstSettings(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"execution.model_kinds.claude-code.cheaper", "execution.developer_cheaper_first.enabled", "execution.high_risk_labels"} {
		if err := RoleMayApply(key); err != nil {
			t.Fatalf("RoleMayApply(%q) = %v, want it applicable", key, err)
		}
	}
}
