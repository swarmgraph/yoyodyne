package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const validBootstrapConfig = `version: 1
product:
  id: yoyodyne
  repository: .
execution:
  max_concurrent_developers: 1
  repair_attempts_before_replan: 2
  worktree_root: auto
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  developers:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
`

func TestDecodeValidBootstrapConfig(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Product.RepositoryID != "yoyodyne" {
		t.Fatalf("RepositoryID = %q, want yoyodyne", cfg.Product.RepositoryID)
	}
	if cfg.Agents["developers"].Backend != domain.BackendClaudeCode {
		t.Fatalf("developer backend = %q", cfg.Agents["developers"].Backend)
	}
}

func TestDecodeAppliesDefaults(t *testing.T) {
	t.Parallel()

	input := `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Execution.MaxConcurrentDevelopers != 1 {
		t.Fatalf("MaxConcurrentDevelopers = %d, want 1", cfg.Execution.MaxConcurrentDevelopers)
	}
	if cfg.Execution.RepairAttemptsBeforeReplan != 2 {
		t.Fatalf("RepairAttemptsBeforeReplan = %d, want 2", cfg.Execution.RepairAttemptsBeforeReplan)
	}
	if cfg.Execution.TransientRelaunchesBeforeBlocking != 2 {
		t.Fatalf("TransientRelaunchesBeforeBlocking = %d, want 2", cfg.Execution.TransientRelaunchesBeforeBlocking)
	}
	if cfg.Execution.WorktreeRoot != "auto" {
		t.Fatalf("WorktreeRoot = %q, want auto", cfg.Execution.WorktreeRoot)
	}
	if cfg.Agents["developer"].Instances != 1 {
		t.Fatalf("Instances = %d, want 1", cfg.Agents["developer"].Instances)
	}
}

func TestDecodePreservesExplicitZeroRepairAttempts(t *testing.T) {
	t.Parallel()

	input := strings.Replace(validBootstrapConfig, "repair_attempts_before_replan: 2", "repair_attempts_before_replan: 0", 1)
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Execution.RepairAttemptsBeforeReplan != 0 {
		t.Fatalf("RepairAttemptsBeforeReplan = %d, want 0", cfg.Execution.RepairAttemptsBeforeReplan)
	}
}

func TestDecodeRejectsExplicitZeroConcurrencyAndInstances(t *testing.T) {
	t.Parallel()

	input := strings.Replace(validBootstrapConfig, "max_concurrent_developers: 1", "max_concurrent_developers: 0", 1)
	input = strings.Replace(input, "instances: 1", "instances: 0", 1)
	_, err := Decode(strings.NewReader(input))
	if err == nil {
		t.Fatal("Decode() error = nil, want validation error")
	}
	for _, expected := range []string{"max_concurrent_developers must be at least 1", "instances must be at least 1"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Decode() error %q does not contain %q", err, expected)
		}
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	input := validBootstrapConfig + "unexpected: true\n"
	_, err := Decode(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Decode() error = %v, want unknown field error", err)
	}
}

func TestDecodeRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()

	_, err := Decode(strings.NewReader(validBootstrapConfig + "---\n{}\n"))
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("Decode() error = %v, want multiple document error", err)
	}
}

func TestValidateCollectsInvalidInvariants(t *testing.T) {
	t.Parallel()

	input := `version: 2
product:
  id: Invalid_ID
  repository: ""
execution:
  max_concurrent_developers: -1
  repair_attempts_before_replan: -1
approvals:
  brief: maybe
  goals: human
  designs: automatic
  integration: automatic
agents:
  product:
    role: product-manager
    backend: codex
`
	_, err := Decode(strings.NewReader(input))
	if err == nil {
		t.Fatal("Decode() error = nil, want validation error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Decode() error type = %T, want ValidationError", err)
	}
	for _, expected := range []string{
		"version must be 1",
		"product id",
		"product repository is required",
		"max_concurrent_developers",
		"repair_attempts_before_replan",
		"approval brief",
		"model selector is required",
		"at least one developer",
		"automatic integration requires at least one check",
		"automatic integration requires at least one reviewer",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Decode() error %q does not contain %q", err, expected)
		}
	}
}

func TestValidateRequiresAModelSelectorForEveryAgent(t *testing.T) {
	t.Parallel()

	// There is no implicit harness default: an agent that names no model is a
	// run whose evidence could never say what produced it.
	missing := strings.Replace(validBootstrapConfig, "    model: opus\n", "", 1)
	if _, err := Decode(strings.NewReader(missing)); err == nil || !strings.Contains(err.Error(), `agent "developers" model selector is required`) {
		t.Fatalf("Decode() missing model error = %v", err)
	}

	for _, test := range []struct {
		name     string
		selector string
		problem  string
	}{
		{name: "floating family alias", selector: "opus"},
		{name: "pinned identifier", selector: "claude-opus-5-20260514"},
		{name: "blank", selector: `"   "`, problem: "model selector is required"},
		{name: "argument smuggling", selector: `"--dangerously-skip-permissions"`, problem: "must be a single model name"},
		{name: "multiple words", selector: `"opus sonnet"`, problem: "must be a single model name"},
		{name: "oversized", selector: strings.Repeat("m", MaxModelSelectorBytes+1), problem: "limit is"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validBootstrapConfig, "model: opus", "model: "+test.selector, 1)
			cfg, err := Decode(strings.NewReader(input))
			if test.problem != "" {
				if err == nil || !strings.Contains(err.Error(), test.problem) {
					t.Fatalf("Decode() error = %v, want %q", err, test.problem)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if cfg.Agents["developers"].Model != test.selector {
				t.Fatalf("developer model = %q, want %q", cfg.Agents["developers"].Model, test.selector)
			}
		})
	}
}

func TestValidateRefusesAnUnknownRole(t *testing.T) {
	t.Parallel()

	// The set of roles is fixed in the harness, so a name outside it is refused
	// while the configuration is being loaded — before any work is claimed —
	// rather than when the provider is finally invoked with it.
	for _, test := range []struct {
		name string
		role string
	}{
		{name: "typo", role: "developor"},
		{name: "wrong case", role: "Developer"},
		{name: "role from somewhere else", role: "security-reviewer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validBootstrapConfig, "role: developer", "role: "+test.role, 1)
			_, err := Decode(strings.NewReader(input))
			if err == nil {
				t.Fatal("Decode() error = nil, want an unknown role failure")
			}
			var validationErr ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Decode() error type = %T, want ValidationError", err)
			}
			// The refusal names the agent, what it said, and what it could have
			// said: an operator who mistyped a role fixes it from the message.
			for _, expected := range []string{`agent "developers" has unknown role`, test.role, `"developer"`, `"reviewer"`} {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("Decode() error %q does not contain %q", err, expected)
				}
			}
			// An unknown role is not a backend that fails to serve it. Reporting
			// both would send the operator to the backend line for a problem on
			// the role line.
			if strings.Contains(err.Error(), "does not support role") {
				t.Errorf("Decode() error %q blames the backend for an unknown role", err)
			}
			// The role also stops counting toward the roles a run needs, so the
			// configuration is short a developer rather than quietly having one.
			if !strings.Contains(err.Error(), "at least one developer agent is required") {
				t.Errorf("Decode() error %q counts an unknown role as a developer", err)
			}
		})
	}
}

func TestValidateAcceptsEveryHarnessRole(t *testing.T) {
	t.Parallel()

	// The refusal is of names outside the set, not of names the bootstrap
	// configuration happens not to use.
	for _, role := range domain.Roles() {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validBootstrapConfig, "role: developer", "role: "+string(role), 1)
			_, err := Decode(strings.NewReader(input))
			if role == domain.RoleDeveloper {
				if err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				return
			}
			// Every other role leaves the configuration with no developer, which
			// is a different problem and the only one it should report.
			if err == nil || !strings.Contains(err.Error(), "at least one developer agent is required") {
				t.Fatalf("Decode() error = %v, want only the missing developer", err)
			}
			if strings.Contains(err.Error(), "unknown role") {
				t.Fatalf("Decode() error %q refuses role %q", err, role)
			}
		})
	}
}

func TestValidateAllowsCodexForEveryRole(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		input := strings.Replace(validBootstrapConfig, "backend: claude-code", "backend: codex", 1)
		input = strings.Replace(input, "model: opus", "model: gpt-6.1-sol", 1)
		if role != domain.RoleDeveloper {
			input += "\n  codex-role:\n    role: " + string(role) + "\n    backend: codex\n    model: gpt-6.1-sol\n"
		}
		if _, err := Decode(strings.NewReader(input)); err != nil {
			t.Errorf("Decode() %q Codex error = %v", role, err)
		}
	}
}

// A project may say which classes of work its per-item gate is not asking
// about. Nothing is exempt until it does: an exemption is the operator handing
// over a decision, so it is written down rather than inherited or defaulted.
func TestWorkItemExemptionsAreConfiguredAndOtherwiseEmpty(t *testing.T) {
	t.Parallel()

	silent, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(silent.Approvals.WorkItemExemptions) != 0 {
		t.Fatalf("exemptions = %#v, want a project that carved out nothing to have carved out nothing", silent.Approvals.WorkItemExemptions)
	}

	exempting, err := Decode(strings.NewReader(strings.Replace(validBootstrapConfig,
		"  integration: human\n",
		"  integration: human\n  work_item_exemptions:\n    - diagnosis\n", 1)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(exempting.Approvals.WorkItemExemptions) != 1 || exempting.Approvals.WorkItemExemptions[0] != domain.WorkItemClassDiagnosis {
		t.Fatalf("exemptions = %#v, want the diagnosis class", exempting.Approvals.WorkItemExemptions)
	}
}

// A class the harness does not recognize exempts nothing, so the file is
// refused rather than loaded with a policy its author believes is in force.
func TestAnUnknownWorkItemExemptionIsRefused(t *testing.T) {
	t.Parallel()

	_, err := Decode(strings.NewReader(strings.Replace(validBootstrapConfig,
		"  integration: human\n",
		"  integration: human\n  work_item_exemptions:\n    - housekeeping\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "housekeeping") {
		t.Fatalf("Decode() error = %v, want the unknown class named", err)
	}
	if err != nil && !strings.Contains(err.Error(), `"diagnosis"`) {
		t.Fatalf("Decode() error = %v, want it to name the classes there are", err)
	}
}

func TestSchedulingWaitProblemTime(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"", time.Hour}, {"  scheduling_wait_problem_after: 2h\n", 2 * time.Hour},
	} {
		input := strings.Replace(validBootstrapConfig, "execution:\n", "execution:\n"+test.value, 1)
		cfg, err := Decode(strings.NewReader(input))
		if err != nil || cfg.Execution.SchedulingWaitProblemAfter.Duration() != test.want {
			t.Fatalf("value %q: got %s, %v", test.value, cfg.Execution.SchedulingWaitProblemAfter.Duration(), err)
		}
	}
	input := strings.Replace(validBootstrapConfig, "execution:\n", "execution:\n  scheduling_wait_problem_after: -1h\n", 1)
	if _, err := Decode(strings.NewReader(input)); err == nil {
		t.Fatal("negative duration accepted")
	}
}
