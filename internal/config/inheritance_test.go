package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// minimalProjectConfig is everything a project outside the Yoyodyne source tree
// has to write down: its own identity, and the bundle it inherits everything
// else from.
const minimalProjectConfig = `version: 1
extends: builtin:v1
product:
  id: example
  repository: .
`

func TestBuiltinBundleSuppliesCompleteDefaultAgents(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	wantRoles := map[string]domain.AgentRole{
		"product-manager":     domain.RoleProductManager,
		"architect":           domain.RoleArchitect,
		"development-manager": domain.RoleDevelopmentManager,
		"developer":           domain.RoleDeveloper,
		"reviewer":            domain.RoleReviewer,
	}
	if len(cfg.Agents) != len(wantRoles) {
		t.Fatalf("agents = %d, want %d", len(cfg.Agents), len(wantRoles))
	}
	for name, wantRole := range wantRoles {
		agent, ok := cfg.Agents[name]
		if !ok {
			t.Fatalf("agent %q is missing from the built-in bundle", name)
		}
		if agent.Role != wantRole {
			t.Errorf("agent %q role = %q, want %q", name, agent.Role, wantRole)
		}
		if agent.Backend != domain.BackendClaudeCode {
			t.Errorf("agent %q backend = %q, want %q", name, agent.Backend, domain.BackendClaudeCode)
		}
		if err := ValidateModelSelector(agent.Model); err != nil {
			t.Errorf("agent %q model: %v", name, err)
		}
		if agent.Instances < 1 {
			t.Errorf("agent %q instances = %d, want at least 1", name, agent.Instances)
		}
		if agent.Persona.Version == "" || agent.Persona.Path == "" || strings.TrimSpace(agent.Persona.Text) == "" {
			t.Errorf("agent %q persona = %+v, want a versioned nonempty persona", name, agent.Persona)
		}
		if !strings.HasPrefix(agent.Persona.Source, BuiltinV1) {
			t.Errorf("agent %q persona source = %q, want a %s source", name, agent.Persona.Source, BuiltinV1)
		}
	}
	if cfg.Product.RepositoryID != "example" {
		t.Errorf("repository id = %q, want the derived product id", cfg.Product.RepositoryID)
	}
}

func TestProjectOverridesOneFieldWithoutCopyingDefaults(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`agents:
  developer:
    model: claude-opus-5-20260514
`, nil)
	developer := resolved.Config.Agents["developer"]
	if developer.Model != "claude-opus-5-20260514" {
		t.Fatalf("developer model = %q, want the project override", developer.Model)
	}
	// Everything the override did not mention still comes from the bundle.
	if developer.Role != domain.RoleDeveloper || developer.Backend != domain.BackendClaudeCode || developer.Instances != 1 {
		t.Fatalf("developer inherited fields = %+v", developer)
	}
	if strings.TrimSpace(developer.Persona.Text) == "" {
		t.Fatal("developer persona was lost by a model-only override")
	}
	if got := resolved.Origins["agents.developer.model"]; got != resolved.Path {
		t.Errorf("model origin = %q, want %q", got, resolved.Path)
	}
	for key, want := range map[string]string{
		"agents.developer.role":      BuiltinV1,
		"agents.developer.backend":   BuiltinV1,
		"agents.developer.persona":   BuiltinV1,
		"agents.developer.instances": BuiltinV1,
		"approvals.integration":      BuiltinV1,
		"product.id":                 resolved.Path,
		"product.repository_id":      OriginDerived,
	} {
		if got := resolved.Origins[key]; got != want {
			t.Errorf("origin[%q] = %q, want %q", key, got, want)
		}
	}
	if len(resolved.Sources) != 2 || resolved.Sources[0] != BuiltinV1 || resolved.Sources[1] != resolved.Path {
		t.Errorf("sources = %v, want the bundle then the project file", resolved.Sources)
	}
}

func TestProjectPersonaOverrideReplacesTheInheritedPersona(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`agents:
  reviewer:
    persona:
      version: project-1
      path: personas/reviewer.md
`, map[string]string{"personas/reviewer.md": "# House reviewer\n\nCheck the migration path first.\n"})
	reviewer := resolved.Config.Agents["reviewer"]
	if reviewer.Persona.Version != "project-1" || !strings.Contains(reviewer.Persona.Text, "House reviewer") {
		t.Fatalf("reviewer persona = %+v", reviewer.Persona)
	}
	if strings.Contains(reviewer.Persona.Text, "Reviewer persona") {
		t.Fatal("project persona was merged into the inherited one instead of replacing it")
	}
	if reviewer.Model == "" {
		t.Fatal("a persona override dropped the inherited model selector")
	}
	if got := resolved.Origins["agents.reviewer.persona"]; got != resolved.Path {
		t.Errorf("persona origin = %q, want %q", got, resolved.Path)
	}
	// The developer keeps the built-in persona nobody overrode.
	if !strings.HasPrefix(resolved.Config.Agents["developer"].Persona.Source, BuiltinV1) {
		t.Errorf("developer persona source = %q", resolved.Config.Agents["developer"].Persona.Source)
	}
}

func TestDisabledAgentIsRemovedButRequiredRolesStillValidate(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  architect:
    disabled: true
`, nil).Config
	if _, present := cfg.Agents["architect"]; present {
		t.Fatal("architect survived disabled: true")
	}
	if _, present := cfg.Agents["developer"]; !present {
		t.Fatal("disabling one agent removed an unrelated one")
	}

	// The roles the invoked workflow executes cannot be disabled away.
	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    disabled: true
`, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one developer agent is required") {
		t.Fatalf("disabling the developer error = %v, want a validation failure", err)
	}

	_, err = loadProjectError(t, minimalProjectConfig+`approvals:
  integration: automatic
checks:
  - go test ./...
agents:
  reviewer:
    disabled: true
`, nil)
	if err == nil || !strings.Contains(err.Error(), "automatic integration requires at least one reviewer agent") {
		t.Fatalf("disabling the reviewer error = %v, want a validation failure", err)
	}
}

func TestInvalidInheritanceFailsClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		config  string
		files   map[string]string
		problem string
	}{
		{
			name:    "unknown bundle",
			config:  strings.Replace(minimalProjectConfig, "builtin:v1", "builtin:v9", 1),
			problem: `unknown configuration bundle "builtin:v9"`,
		},
		{
			name:    "unknown agent key",
			config:  minimalProjectConfig + "agents:\n  developer:\n    modle: opus\n",
			problem: "field modle not found",
		},
		{
			name:    "unknown top-level key",
			config:  minimalProjectConfig + "personas: yes\n",
			problem: "field personas not found",
		},
		{
			name:    "disabled agent that is also configured",
			config:  minimalProjectConfig + "agents:\n  architect:\n    disabled: true\n    model: opus\n",
			problem: "is disabled and also configured",
		},
		{
			name:    "disabled agent that was never inherited",
			config:  minimalProjectConfig + "agents:\n  auditor:\n    disabled: true\n",
			problem: "no inherited agent by that name exists",
		},
		{
			name:    "partial persona override",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      path: personas/developer.md\n",
			problem: "must declare both version and path",
		},
		{
			name:    "inherited model unverified by backend",
			config:  minimalProjectConfig + "agents:\n  architect:\n    backend: codex\n",
			problem: `whose effort levels and default are not established`,
		},
		{
			// A typo in an agents block is the way an unknown role actually
			// arrives, and the effective configuration is where it has to be
			// caught: the overlay says only the role, and the backend and model
			// it would run under come from the bundle underneath.
			name:    "typoed role on an inherited agent",
			config:  minimalProjectConfig + "agents:\n  developer:\n    role: developor\n",
			problem: `agent "developer" has unknown role "developor"`,
		},
		{
			name:    "unknown role on a new agent",
			config:  minimalProjectConfig + "agents:\n  auditor:\n    role: security-reviewer\n    backend: claude-code\n    model: opus\n",
			problem: `agent "auditor" has unknown role "security-reviewer"`,
		},
		{
			name:    "invalid effective configuration",
			config:  minimalProjectConfig + "execution:\n  max_concurrent_developers: 4\n",
			problem: "max_concurrent_developers cannot exceed configured developer instances",
		},
		{
			name:    "absolute persona path",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: /etc/persona.md\n",
			problem: "must be relative to the project .yoyodyne directory",
		},
		{
			name:    "persona path traversal",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: ../../secrets.md\n",
			problem: "must not traverse outside the project .yoyodyne directory",
		},
		{
			name:    "persona that is not Markdown",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/developer.txt\n",
			files:   map[string]string{"personas/developer.txt": "text"},
			problem: "must be a Markdown file",
		},
		{
			name:    "missing persona file",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/absent.md\n",
			problem: `resolve persona "personas/absent.md"`,
		},
		{
			name:    "empty persona file",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/blank.md\n",
			files:   map[string]string{"personas/blank.md": "   \n"},
			problem: "is empty",
		},
		{
			name:    "oversized persona file",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/huge.md\n",
			files:   map[string]string{"personas/huge.md": strings.Repeat("g", MaxPersonaBytes+1)},
			problem: "limit is",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, test.config, test.files)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("LoadResolved() error = %v, want %q", err, test.problem)
			}
		})
	}
}

func TestPersonaSymlinkEscapeFailsClosed(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	outside := filepath.Join(project, "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside the project configuration\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	writeProject(t, project, minimalProjectConfig+`agents:
  developer:
    persona:
      version: p1
      path: personas/escape.md
`, nil)
	personas := filepath.Join(project, DirectoryName, "personas")
	if err := os.MkdirAll(personas, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(personas, "escape.md")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("LoadResolved() error = %v, want a symlink escape failure", err)
	}
}

// A project states its own schema version even when the bundle it extends
// declares one. Inheriting the version would let a file written against a
// different schema load as whatever the bundle happened to say.
func TestProjectConfigurationMustDeclareItsOwnVersion(t *testing.T) {
	t.Parallel()

	withoutVersion := strings.TrimPrefix(minimalProjectConfig, "version: 1\n")
	if strings.Contains(withoutVersion, "version:") {
		t.Fatalf("test setup left a version key in %q", withoutVersion)
	}

	_, err := loadProjectError(t, withoutVersion, nil)
	var validationErr ValidationError
	if !errors.As(err, &validationErr) || !strings.Contains(err.Error(), "version must be 1 and is required") {
		t.Fatalf("LoadResolved() error = %v, want a missing-version validation failure", err)
	}

	// A standalone configuration has no bundle to inherit from, and the stream
	// path is held to the same rule as a file.
	_, err = DecodeResolved(strings.NewReader(strings.TrimPrefix(validBootstrapConfig, "version: 1\n")))
	if err == nil || !strings.Contains(err.Error(), "version must be 1 and is required") {
		t.Fatalf("DecodeResolved() error = %v, want a missing-version validation failure", err)
	}

	// A version the harness does not implement still fails, as its own problem
	// rather than as a missing key.
	_, err = loadProjectError(t, strings.Replace(minimalProjectConfig, "version: 1", "version: 2", 1), nil)
	if err == nil || !strings.Contains(err.Error(), "version must be 1") || strings.Contains(err.Error(), "is required") {
		t.Fatalf("LoadResolved() error = %v, want an unsupported-version failure", err)
	}
}

// A configuration that does not extend a bundle is a complete standalone file
// and keeps working exactly as it did before bundles existed.
func TestLegacyCompleteConfigurationStillLoads(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	path := filepath.Join(project, LegacyFileName)
	if err := os.WriteFile(path, []byte(validBootstrapConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Extends != "" {
		t.Fatalf("Extends = %q, want empty for a standalone configuration", resolved.Config.Extends)
	}
	if len(resolved.Config.Agents) != 1 {
		t.Fatalf("agents = %d, want only the ones the file declares", len(resolved.Config.Agents))
	}
	if resolved.Config.Agents["developers"].Persona.Defined() {
		t.Fatal("a standalone configuration inherited a persona it never declared")
	}
	if len(resolved.Sources) != 1 || resolved.Sources[0] != resolved.Path {
		t.Fatalf("sources = %v, want only the file itself", resolved.Sources)
	}
}

// A value nobody configured is reported as a harness default rather than
// attributed to a layer that never mentioned it.
func TestHarnessDefaultsAreRecordedAsSuch(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(`version: 1
product:
  id: example
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
`))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	for _, key := range []string{
		"execution.max_concurrent_developers",
		"execution.repair_attempts_before_replan",
		"execution.worktree_root",
		"product.specifications",
		"agents.developer.instances",
	} {
		if got := resolved.Origins[key]; got != OriginDefault {
			t.Errorf("origin[%q] = %q, want %q", key, got, OriginDefault)
		}
	}
	if got := resolved.Origins["agents.developer.model"]; got != OriginInput {
		t.Errorf("model origin = %q, want %q", got, OriginInput)
	}
}

func loadProject(t *testing.T, contents string, personas map[string]string) Resolved {
	t.Helper()
	resolved, err := loadProjectError(t, contents, personas)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	return resolved
}

func loadProjectError(t *testing.T, contents string, personas map[string]string) (Resolved, error) {
	t.Helper()
	project := t.TempDir()
	writeProject(t, project, contents, personas)
	return LoadResolved(filepath.Join(project, DirectoryName, FileName))
}

func writeProject(t *testing.T, project, contents string, personas map[string]string) {
	t.Helper()
	directory := filepath.Join(project, DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, FileName), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	for name, body := range personas {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
}

// The bundle supplies both usage-limit pause bounds, so a project inherits a
// harness that waits out a five-hour limit and refuses to sleep through a
// seven-day one without having to write either number down.
func TestBuiltinBundleSuppliesUsageLimitPauseBounds(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if got := resolved.Config.Execution.UsageLimitMaxPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_max_pause = %s, want 6h", got)
	}
	if got := resolved.Config.Execution.UsageLimitInProcessPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_in_process_pause = %s, want 6h", got)
	}
	for _, key := range []string{"execution.usage_limit_max_pause", "execution.usage_limit_in_process_pause"} {
		if origin := resolved.Origins[key]; origin != BuiltinV1 {
			t.Fatalf("%s origin = %q, want %q", key, origin, BuiltinV1)
		}
	}
}

// A project that cares about the pause overrides only the bound it means, in the
// duration syntax it reads it in, and keeps everything else it inherited.
func TestProjectOverridesOneUsageLimitPauseBound(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  usage_limit_in_process_pause: 15m
`, nil)
	if got := resolved.Config.Execution.UsageLimitInProcessPause; got != Duration(15*time.Minute) {
		t.Fatalf("usage_limit_in_process_pause = %s, want 15m", got)
	}
	if got := resolved.Config.Execution.UsageLimitMaxPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_max_pause = %s, want the inherited 6h", got)
	}
	if origin := resolved.Origins["execution.usage_limit_max_pause"]; origin != BuiltinV1 {
		t.Fatalf("an untouched bound changed origin to %q", origin)
	}
}

// The overload pause is configurable the same way, so the whole path from the
// key an operator writes to the value a run waits is exercised: the yaml tag on
// the document, the overlay that applies it, and the harness default underneath
// them both. Setting the field on the struct in a pipeline test proves none of
// that, and the documentation promises operators the key works.
func TestServerOverloadPauseResolvesFromEveryLayer(t *testing.T) {
	t.Parallel()

	// The bundle supplies it, so a project that writes nothing down still waits a
	// short interval on an overloaded provider rather than the half hour an
	// exhausted limit gets.
	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.Execution.ServerOverloadPause; got != Duration(90*time.Second) {
		t.Fatalf("inherited server_overload_pause = %s, want 90s", got)
	}
	if origin := inherited.Origins["execution.server_overload_pause"]; origin != BuiltinV1 {
		t.Fatalf("server_overload_pause origin = %q, want %q", origin, BuiltinV1)
	}

	// A project that overrides it gets what it wrote, and its neighbours keep what
	// they inherited.
	overridden := loadProject(t, minimalProjectConfig+`execution:
  server_overload_pause: 45s
`, nil)
	if got := overridden.Config.Execution.ServerOverloadPause; got != Duration(45*time.Second) {
		t.Fatalf("overridden server_overload_pause = %s, want 45s", got)
	}
	if origin := overridden.Origins["execution.server_overload_pause"]; origin == BuiltinV1 {
		t.Fatalf("an overridden key kept the bundle's origin %q", origin)
	}
	if got := overridden.Config.Execution.UsageLimitMaxPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_max_pause = %s, want the inherited 6h", got)
	}

	// A generated project inherits no bundle and writes every value down itself,
	// so it is where a misplaced argument in the scaffold template would show up:
	// both intervals are asserted, because swapping two adjacent durations leaves
	// a file that still parses and still validates.
	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.ServerOverloadPause; got != Duration(90*time.Second) {
		t.Fatalf("generated server_overload_pause = %s, want 90s", got)
	}
	if got := generated.Execution.UsageLimitUnknownResetPause; got != Duration(30*time.Minute) {
		t.Fatalf("generated usage_limit_unknown_reset_pause = %s, want 30m", got)
	}
}

// The check budget is configurable through the same whole path, and for a
// reason the pauses do not share: it is the one bound whose right value depends
// on the project's own suite and on how many runs share the machine with it, so
// a project that outgrows the default has to be able to say so.
func TestCheckTimeoutResolvesFromEveryLayer(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.Execution.CheckTimeout; got != Duration(30*time.Minute) {
		t.Fatalf("inherited check_timeout = %s, want 30m", got)
	}
	if origin := inherited.Origins["execution.check_timeout"]; origin != BuiltinV1 {
		t.Fatalf("check_timeout origin = %q, want %q", origin, BuiltinV1)
	}

	// A project whose suite has grown, or that runs several developers at once,
	// raises it and keeps everything else it inherited.
	overridden := loadProject(t, minimalProjectConfig+`execution:
  check_timeout: 90m
`, nil)
	if got := overridden.Config.Execution.CheckTimeout; got != Duration(90*time.Minute) {
		t.Fatalf("overridden check_timeout = %s, want 90m", got)
	}
	if origin := overridden.Origins["execution.check_timeout"]; origin == BuiltinV1 {
		t.Fatalf("an overridden key kept the bundle's origin %q", origin)
	}
	if got := overridden.Config.Execution.ServerOverloadPause; got != Duration(90*time.Second) {
		t.Fatalf("server_overload_pause = %s, want the inherited 90s", got)
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.CheckTimeout; got != Duration(30*time.Minute) {
		t.Fatalf("generated check_timeout = %s, want 30m", got)
	}
}

// A check with no budget at all is refused rather than treated as unbounded: it
// would hold a worktree, a claim, and a run open for as long as it kept running,
// and nothing else bounds a check.
func TestCheckTimeoutMustBePositive(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"execution:\n  check_timeout: 0s\n", "execution:\n  check_timeout: -10m\n"} {
		_, err := loadProjectError(t, minimalProjectConfig+body, nil)
		if err == nil || !strings.Contains(err.Error(), "execution.check_timeout must be positive") {
			t.Fatalf("LoadResolved() error = %v, want one refusing %q", err, strings.TrimSpace(body))
		}
	}
}

// The watch settings resolve like every other execution key, and both of them
// have to: the interval is how responsive a session is to a queue being
// steered, and the brake is a safety bound a project sets against its own pace.
func TestTheWatchSettingsResolveFromEveryLayer(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.Execution.WorkPoll; got != Duration(60*time.Second) {
		t.Fatalf("inherited work_poll = %s, want 60s", got)
	}
	if got := inherited.Config.Execution.BlockedRunsBeforeIntakeHold; got != 3 {
		t.Fatalf("inherited blocked_runs_before_intake_hold = %d, want 3", got)
	}

	overridden := loadProject(t, minimalProjectConfig+`execution:
  work_poll: 5m
  blocked_runs_before_intake_hold: 5
`, nil)
	if got := overridden.Config.Execution.WorkPoll; got != Duration(5*time.Minute) {
		t.Fatalf("overridden work_poll = %s, want 5m", got)
	}
	if got := overridden.Config.Execution.BlockedRunsBeforeIntakeHold; got != 5 {
		t.Fatalf("overridden blocked_runs_before_intake_hold = %d, want 5", got)
	}
	if origin := overridden.Origins["execution.work_poll"]; origin == BuiltinV1 {
		t.Fatalf("an overridden key kept the bundle's origin %q", origin)
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.WorkPoll; got != Duration(60*time.Second) {
		t.Fatalf("generated work_poll = %s, want 60s", got)
	}
	if got := generated.Execution.BlockedRunsBeforeIntakeHold; got != 3 {
		t.Fatalf("generated blocked_runs_before_intake_hold = %d, want 3", got)
	}
}

// The drain bound resolves like the other watch settings, and its default is
// minutes rather than hours: a deploy over a session hosting a long check suite
// has to be taken up within the hour it landed in.
func TestTheRedeployDrainLimitResolvesFromEveryLayer(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.Execution.RedeployDrainLimit; got != Duration(15*time.Minute) {
		t.Fatalf("inherited redeploy_drain_limit = %s, want 15m", got)
	}
	if origin := inherited.Origins["execution.redeploy_drain_limit"]; origin != BuiltinV1 {
		t.Fatalf("redeploy_drain_limit origin = %q, want %q", origin, BuiltinV1)
	}

	overridden := loadProject(t, minimalProjectConfig+"execution:\n  redeploy_drain_limit: 45m\n", nil)
	if got := overridden.Config.Execution.RedeployDrainLimit; got != Duration(45*time.Minute) {
		t.Fatalf("overridden redeploy_drain_limit = %s, want 45m", got)
	}
	if origin := overridden.Origins["execution.redeploy_drain_limit"]; origin == BuiltinV1 {
		t.Fatalf("an overridden key kept the bundle's origin %q", origin)
	}

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.RedeployDrainLimit; got != Duration(15*time.Minute) {
		t.Fatalf("generated redeploy_drain_limit = %s, want 15m", got)
	}

	// A drain with no bound is the two-hour wait this bound exists to end.
	for _, body := range []string{"execution:\n  redeploy_drain_limit: 0s\n", "execution:\n  redeploy_drain_limit: -5m\n"} {
		_, err := loadProjectError(t, minimalProjectConfig+body, nil)
		if err == nil || !strings.Contains(err.Error(), "execution.redeploy_drain_limit must be positive") {
			t.Fatalf("LoadResolved() error = %v, want one refusing %q", err, strings.TrimSpace(body))
		}
	}
}

// An interval of nothing is a session reading the queue as fast as the machine
// allows, which is a spin rather than a watch. A brake of zero is a choice —
// never brake, leave the operator as the only thing that holds intake — and a
// negative one describes no run anybody could count.
func TestTheWatchSettingsRefuseWhatWouldNotBeAWatch(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"execution:\n  work_poll: 0s\n", "execution:\n  work_poll: -1m\n"} {
		_, err := loadProjectError(t, minimalProjectConfig+body, nil)
		if err == nil || !strings.Contains(err.Error(), "execution.work_poll must be positive") {
			t.Fatalf("LoadResolved() error = %v, want one refusing %q", err, strings.TrimSpace(body))
		}
	}
	_, err := loadProjectError(t, minimalProjectConfig+"execution:\n  blocked_runs_before_intake_hold: -1\n", nil)
	if err == nil || !strings.Contains(err.Error(), "blocked_runs_before_intake_hold cannot be negative") {
		t.Fatalf("LoadResolved() error = %v, want a negative brake refused", err)
	}
	turnedOff := loadProject(t, minimalProjectConfig+"execution:\n  blocked_runs_before_intake_hold: 0\n", nil)
	if got := turnedOff.Config.Execution.BlockedRunsBeforeIntakeHold; got != 0 {
		t.Fatalf("blocked_runs_before_intake_hold = %d, want the brake turned off as asked", got)
	}
}

// A non-positive interval is refused, because the whole of an overload's wait is
// this interval: there is no reset time underneath it to fall back on.
func TestServerOverloadPauseMustBePositive(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"execution:\n  server_overload_pause: 0s\n", "execution:\n  server_overload_pause: -30s\n"} {
		_, err := loadProjectError(t, minimalProjectConfig+body, nil)
		if err == nil || !strings.Contains(err.Error(), "execution.server_overload_pause must be positive") {
			t.Fatalf("LoadResolved() error = %v, want one refusing %q", err, strings.TrimSpace(body))
		}
	}
}

// A configured duration is round-tripped in the form it was written in, in both
// renderings of the effective configuration. `config show` is where an operator
// finds out how long a run is allowed to wait, and a nanosecond count is not an
// answer to that question.
func TestConfiguredDurationsRenderAsDurations(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	asYAML, err := yaml.Marshal(cfg.Execution)
	if err != nil {
		t.Fatalf("Marshal() yaml error = %v", err)
	}
	if !strings.Contains(string(asYAML), "usage_limit_max_pause: 6h0m0s") {
		t.Fatalf("effective YAML did not render the pause as a duration:\n%s", asYAML)
	}
	asJSON, err := json.Marshal(cfg.Execution)
	if err != nil {
		t.Fatalf("Marshal() json error = %v", err)
	}
	if !strings.Contains(string(asJSON), `"usage_limit_max_pause":"6h0m0s"`) {
		t.Fatalf("effective JSON did not render the pause as a duration:\n%s", asJSON)
	}
}

func TestUsageLimitPauseBoundsFailClosed(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "not a duration",
			body:    "execution:\n  usage_limit_max_pause: soon\n",
			wantErr: "parse duration",
		},
		{
			// A zero bound is a deliberate "never wait"; only a negative one
			// describes a wait nobody could take.
			name:    "negative",
			body:    "execution:\n  usage_limit_max_pause: -1h\n",
			wantErr: "usage_limit_max_pause cannot be negative",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			project := t.TempDir()
			writeProject(t, project, minimalProjectConfig+testCase.body, nil)
			_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("LoadResolved() error = %v, want one mentioning %q", err, testCase.wantErr)
			}
		})
	}
}

// The relaunch budget is inherited from the bundle and overridable like every
// other bound, including down to the pre-ifd.101 behavior of failing on the
// first provider death. Only a negative bound, which describes no relaunch
// anybody could take, is refused.
func TestTransientRelaunchBudgetIsInheritedAndOverridable(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Execution.TransientRelaunchesBeforeBlocking != 2 {
		t.Fatalf("inherited relaunch budget = %d, want the bundle's 2", inherited.Config.Execution.TransientRelaunchesBeforeBlocking)
	}
	if origin := inherited.Origins["execution.transient_relaunches_before_blocking"]; origin != "builtin:v1" {
		t.Fatalf("relaunch budget origin = %q, want the bundle it came from", origin)
	}

	never := loadProject(t, minimalProjectConfig+"execution:\n  transient_relaunches_before_blocking: 0\n", nil)
	if never.Config.Execution.TransientRelaunchesBeforeBlocking != 0 {
		t.Fatalf("overridden relaunch budget = %d, want the explicit zero to survive", never.Config.Execution.TransientRelaunchesBeforeBlocking)
	}

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+"execution:\n  transient_relaunches_before_blocking: -1\n", nil)
	_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
	if err == nil || !strings.Contains(err.Error(), "transient_relaunches_before_blocking cannot be negative") {
		t.Fatalf("LoadResolved() error = %v, want one refusing a negative relaunch budget", err)
	}
}

// Publishing is opted in to the way integration is, and a project that says
// nothing about it publishes nothing. The remote is inherited rather than
// written down, so opting in is one line.
func TestPublishingIsOptedIntoBesideIntegration(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Approvals.Publishing != domain.ApprovalHuman {
		t.Fatalf("inherited publishing = %q, want %q", inherited.Config.Approvals.Publishing, domain.ApprovalHuman)
	}
	if inherited.Config.Execution.Remote != "origin" {
		t.Fatalf("inherited remote = %q, want origin", inherited.Config.Execution.Remote)
	}
	if origin := inherited.Origins["approvals.publishing"]; origin != BuiltinV1 {
		t.Errorf("publishing origin = %q, want %q", origin, BuiltinV1)
	}

	opted := loadProject(t, minimalProjectConfig+`approvals:
  publishing: automatic
execution:
  remote: upstream
`, nil)
	if opted.Config.Approvals.Publishing != domain.ApprovalAutomatic {
		t.Fatalf("publishing = %q, want %q", opted.Config.Approvals.Publishing, domain.ApprovalAutomatic)
	}
	if opted.Config.Execution.Remote != "upstream" {
		t.Fatalf("remote = %q, want the project override", opted.Config.Execution.Remote)
	}
	// A sparse approvals override must not lose the approvals it does not name.
	if opted.Config.Approvals.Integration != domain.ApprovalHuman {
		t.Errorf("integration = %q, want the inherited value", opted.Config.Approvals.Integration)
	}
}

// A configuration written before publishing existed still loads and still means
// what it meant: the harness publishes nothing.
func TestConfigurationWithoutPublishingKeepsItsBehavior(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(`version: 1
product:
  id: example
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
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
`))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if resolved.Config.Approvals.Publishing != domain.ApprovalHuman {
		t.Fatalf("publishing = %q, want %q", resolved.Config.Approvals.Publishing, domain.ApprovalHuman)
	}
	if origin := resolved.Origins["approvals.publishing"]; origin != OriginDefault {
		t.Errorf("publishing origin = %q, want %q", origin, OriginDefault)
	}
}

// The specifications directory is the whole of what the product manager is
// shown about the product, so a project that follows the recommended layout
// inherits it and a project that does not names its own.
func TestSpecificationsDirectoryDefaultsAndIsOverridable(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Product.Specifications != DefaultSpecifications {
		t.Fatalf("specifications = %q, want %q", inherited.Config.Product.Specifications, DefaultSpecifications)
	}
	if origin := inherited.Origins["product.specifications"]; origin != OriginDefault {
		t.Errorf("specifications origin = %q, want %q", origin, OriginDefault)
	}

	overridden := loadProject(t, minimalProjectConfig+"  specifications: docs/specs\n", nil)
	if overridden.Config.Product.Specifications != "docs/specs" {
		t.Fatalf("specifications = %q, want the project override", overridden.Config.Product.Specifications)
	}
	if origin := overridden.Origins["product.specifications"]; origin == OriginDefault {
		t.Errorf("specifications origin = %q, want the project file", origin)
	}
}

// The product manager reads whatever this names, so a path that leaves the
// repository is refused before anything reads it.
func TestSpecificationsDirectoryIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	for _, directory := range []string{"\"\"", "\"   \"", "..", "../elsewhere", "/etc", "docs/../../elsewhere"} {
		_, err := loadProjectError(t, minimalProjectConfig+"  specifications: "+directory+"\n", nil)
		if err == nil || !strings.Contains(err.Error(), "specifications") {
			t.Errorf("LoadResolved() specifications %q error = %v", directory, err)
		}
	}
}

// What the product ships is the project's own answer and nothing is filled in
// for it, because the alternative is a set of generic paths applied to whatever
// repository the harness runs in — an adopting project's unrelated docs/work.md
// arriving at its product manager labeled as what its product ships.
func TestShippedDocumentationIsTheProjectsOwnAndHasNoDefault(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil).Config
	if len(inherited.Product.ShippedDocumentation) != 0 {
		t.Fatalf("shipped documentation = %v, want nothing supplied for a project that named none", inherited.Product.ShippedDocumentation)
	}

	named := loadProject(t, minimalProjectConfig+"  shipped_documentation:\n    - docs/handbook.md\n    - docs/operating.md\n", nil)
	want := []string{"docs/handbook.md", "docs/operating.md"}
	if got := named.Config.Product.ShippedDocumentation; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("shipped documentation = %v, want %v", got, want)
	}
	if origin := named.Origins["product.shipped_documentation"]; origin == OriginDefault {
		t.Errorf("shipped documentation origin = %q, want the project file", origin)
	}
}

// Every entry is read into the product manager's context, so a path that leaves
// the repository, or one the reader could not read as a document, is refused
// before anything opens a conversation against it.
func TestShippedDocumentationIsConfinedAndMarkdown(t *testing.T) {
	t.Parallel()

	for _, documentPath := range []string{"\"\"", "\"   \"", "..", "../elsewhere.md", "/etc/passwd.md", "docs/../../elsewhere.md", "docs/handbook.txt"} {
		_, err := loadProjectError(t, minimalProjectConfig+"  shipped_documentation:\n    - "+documentPath+"\n", nil)
		if err == nil || !strings.Contains(err.Error(), "shipped documentation") {
			t.Errorf("LoadResolved() shipped_documentation %q error = %v", documentPath, err)
		}
	}
}

// A remote name reaches a Git command line, so anything that could read as an
// option is refused before it gets there.
func TestRemoteMustBeAPlainRemoteName(t *testing.T) {
	t.Parallel()

	for _, remote := range []string{"--upstream", "", "with space", "/etc/passwd"} {
		_, err := loadProjectError(t, minimalProjectConfig+"execution:\n  remote: \""+remote+"\"\n", nil)
		if err == nil || !strings.Contains(err.Error(), "remote") {
			t.Errorf("LoadResolved() remote %q error = %v", remote, err)
		}
	}
}

// A fork is named the same way the publishing remote is, and it reaches the
// same command lines — but leaving it out is the ordinary arrangement rather
// than an omission, so an empty one is the default rather than a refusal.
func TestPushRemoteIsOptionalAndMustBeAPlainRemoteName(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Execution.PushRemote != "" {
		t.Fatalf("inherited push_remote = %q, want run branches on the publishing remote", inherited.Config.Execution.PushRemote)
	}

	forked := loadProject(t, minimalProjectConfig+`execution:
  remote: upstream
  push_remote: fork
`, nil)
	if forked.Config.Execution.Remote != "upstream" || forked.Config.Execution.PushRemote != "fork" {
		t.Fatalf("execution = %+v, want the fork arrangement the project named", forked.Config.Execution)
	}
	if origin := forked.Origins["execution.push_remote"]; origin == "" {
		t.Error("execution.push_remote has no recorded origin")
	}

	for _, pushRemote := range []string{"--upstream", "with space", "/etc/passwd"} {
		_, err := loadProjectError(t, minimalProjectConfig+"execution:\n  push_remote: \""+pushRemote+"\"\n", nil)
		if err == nil || !strings.Contains(err.Error(), "push_remote") {
			t.Errorf("LoadResolved() push_remote %q error = %v", pushRemote, err)
		}
	}
}

// Admitting work without asking is opted in to rather than inherited, the way
// integration and publishing are. The bundle states the same value the harness
// default holds, so a project that extends the bundle acquires no autonomy by
// doing so and none arrives when the executable is upgraded underneath it. That
// is the whole of the upgrade story, and it is checked rather than left to be
// discovered as a queue filling itself in a repository nobody asked.
func TestAdmittingWorkWithoutAskingIsOptedIntoRatherThanInherited(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Approvals.WorkItems != domain.ApprovalHuman {
		t.Fatalf("inherited work_items = %q, want %q", inherited.Config.Approvals.WorkItems, domain.ApprovalHuman)
	}
	// Stated by the bundle rather than left to the harness default, so what a
	// new project gets is a decision somebody wrote down and `yoyo init` copies
	// into a file the operator can read.
	if origin := inherited.Origins["approvals.work_items"]; origin != BuiltinV1 {
		t.Errorf("work_items origin = %q, want the bundle that states it", origin)
	}

	opted := loadProject(t, minimalProjectConfig+"approvals:\n  work_items: automatic\n", nil)
	if opted.Config.Approvals.WorkItems != domain.ApprovalAutomatic {
		t.Fatalf("work_items = %q, want the project override", opted.Config.Approvals.WorkItems)
	}
	// A sparse override must not lose the approvals it does not name.
	if opted.Config.Approvals.Goals != domain.ApprovalHuman || opted.Config.Approvals.Integration != domain.ApprovalHuman {
		t.Fatalf("approvals = %#v, want the inherited values kept", opted.Config.Approvals)
	}
}

// A configuration written before work items became a policy still loads and
// still means what it meant: the operator is asked about every item.
func TestConfigurationWithoutWorkItemsKeepsThePerItemGate(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(`version: 1
product:
  id: example
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
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
`))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if resolved.Config.Approvals.WorkItems != domain.ApprovalHuman {
		t.Fatalf("work_items = %q, want %q", resolved.Config.Approvals.WorkItems, domain.ApprovalHuman)
	}
	if origin := resolved.Origins["approvals.work_items"]; origin != OriginDefault {
		t.Errorf("work_items origin = %q, want %q", origin, OriginDefault)
	}
}

// Admitting work without asking rests on the operator's approval of the goal it
// serves, so a project that records no goal approvals has nothing for it to rest
// on. The setting would read as autonomy and mean nothing, which is refused here
// rather than discovered as a queue that never fills.
func TestAutomaticWorkItemsRequiresTheOperatorToBeApprovingGoals(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+"approvals:\n  goals: automatic\n  work_items: automatic\n", nil)
	_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
	if err == nil || !strings.Contains(err.Error(), "automatic work_items requires approvals.goals") {
		t.Fatalf("LoadResolved() error = %v, want one refusing autonomy that rests on nothing", err)
	}
	// Per-item approval is unaffected: a project that approves no goals and asks
	// about every item is coherent, if conservative.
	perItem := loadProject(t, minimalProjectConfig+"approvals:\n  goals: automatic\n  work_items: human\n", nil)
	if perItem.Config.Approvals.WorkItems != domain.ApprovalHuman {
		t.Fatalf("work_items = %q", perItem.Config.Approvals.WorkItems)
	}

	// And the refusal never fires on a value nobody wrote. A project that says
	// only `goals: automatic` while inheriting work_items from the bundle was
	// valid before this key existed and still loads: a cross-field rule that
	// refused a file over a key its author never wrote, naming a value that
	// arrived by inheritance, would break working projects on an upgrade.
	inheriting := loadProject(t, minimalProjectConfig+"approvals:\n  goals: automatic\n", nil)
	if inheriting.Config.Approvals.WorkItems != domain.ApprovalHuman {
		t.Fatalf("inherited work_items = %q, want the bundle's %q", inheriting.Config.Approvals.WorkItems, domain.ApprovalHuman)
	}
}
