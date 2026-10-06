package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestRoleActivationRecordsTheDigestAndListsAnEditedDefinition(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "")
	stateRoot := t.TempDir()
	t.Setenv(runstate.StateHomeVariable, stateRoot)
	t.Setenv("USER", "Grace")
	configPath := writeConfig(t, validConfig)
	source := filepath.Join(filepath.Dir(configPath), config.DirectoryName, "roles", "specialist.yaml")
	body := "extends: architect\ntools:\n  add: [backlog.order]\n"
	writeArtifact(t, filepath.Dir(configPath), config.DirectoryName+"/roles/specialist.yaml", body)
	before, err := config.LoadResolved(configPath)
	if err != nil {
		t.Fatal(err)
	}
	listed := roleCLIOutput(t, "list", configPath)
	if len(listed.Roles) != 1 || listed.Roles[0].Activated || listed.Roles[0].AmendedSince || listed.Roles[0].Activation != nil {
		t.Fatalf("before activation = %#v", listed)
	}
	if entries, err := os.ReadDir(stateRoot); err != nil || len(entries) != 0 {
		t.Fatalf("list created state: %v, %v", entries, err)
	}
	activation := roleCLIOutput(t, "activate", configPath, "specialist", "--by", "Ada").Recorded
	digest := sha256.Sum256([]byte(body))
	if activation == nil || activation.Person != "Ada" || activation.Source != source || activation.ActivatedAt.IsZero() || activation.Digest != hex.EncodeToString(digest[:]) {
		t.Fatalf("activation = %#v", activation)
	}
	listed = roleCLIOutput(t, "list", configPath)
	if !listed.Roles[0].Activated || listed.Roles[0].AmendedSince || !reflect.DeepEqual(listed.Roles[0].Activation, activation) {
		t.Fatalf("activated definition = %#v", listed)
	}
	if err := os.WriteFile(source, []byte(body+"# changed after activation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed = roleCLIOutput(t, "list", configPath)
	if listed.Roles[0].Activated || !listed.Roles[0].AmendedSince || listed.Roles[0].Activation.Digest != activation.Digest {
		t.Fatalf("amended definition = %#v", listed)
	}
	stdout, stderr, code := runCLI(t, "role", "list", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "amended since activation") || !strings.Contains(stdout, "Ada") {
		t.Fatalf("list = %d, %q, %q", code, stdout, stderr)
	}
	second := roleCLIOutput(t, "activate", configPath, "specialist").Recorded
	if second.Person != "Grace" || second.Digest == activation.Digest || second.ID == activation.ID {
		t.Fatalf("second activation = %#v", second)
	}
	listed = roleCLIOutput(t, "list", configPath)
	if !listed.Roles[0].Activated || listed.Roles[0].AmendedSince || listed.Roles[0].Activation.ID != second.ID {
		t.Fatalf("reactivated definition = %#v", listed)
	}
	after, err := config.LoadResolved(configPath)
	if err != nil || !reflect.DeepEqual(before.Config, after.Config) {
		t.Fatalf("activation changed effective agent authority: %v", err)
	}
	if err := os.WriteFile(source, []byte("extends: unknown-role\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if history := roleCLIOutput(t, "history", configPath).Activations; len(history) != 2 {
		t.Fatalf("a malformed definition hid its history: %#v", history)
	}
	// Removing a definition loses neither its activation nor its audit history.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	history := roleCLIOutput(t, "history", configPath).Activations
	if len(history) != 2 || !reflect.DeepEqual(history[0], *second) || !reflect.DeepEqual(history[1], *activation) {
		t.Fatalf("history = %#v", history)
	}
	stdout, stderr, code = runCLI(t, "role", "history", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, activation.Digest) || !strings.Contains(stdout, second.Digest) {
		t.Fatalf("history = %d, %q, %q", code, stdout, stderr)
	}
}

func TestRoleActivationCommandsValidateArgumentsAndReportEmptyState(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(runstate.StateHomeVariable, t.TempDir())
	t.Setenv("USER", "")
	configPath := writeConfig(t, validConfig)
	for _, args := range [][]string{
		{"role", "unknown"}, {"role", "activate"}, {"role", "activate", "one", "two"},
		{"role", "list", "one"}, {"role", "history", "one"},
	} {
		if stdout, stderr, code := runCLI(t, args...); code != 2 {
			t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
		}
	}
	for _, verb := range []string{"list", "history"} {
		stdout, stderr, code := runCLI(t, "role", verb, "--config", configPath)
		if code != 0 || !strings.Contains(stdout, "no role definitions") {
			t.Fatalf("empty %s = %d, %q, %q", verb, code, stdout, stderr)
		}
	}
	writeArtifact(t, filepath.Dir(configPath), config.DirectoryName+"/roles/specialist.yaml", "extends: architect\n")
	stdout, stderr, code := runCLI(t, "role", "activate", "specialist", "--config", configPath)
	if code != 2 || !strings.Contains(stderr, "requires --by") {
		t.Fatalf("missing person = %d, %q, %q", code, stdout, stderr)
	}
}

func TestRoleActivationRefusesInvalidOrMissingDefinitionsWithoutWriting(t *testing.T) {
	for _, body := range []string{"", "extends: unknown-role\n", "extends: architect\nextra: true\n"} {
		t.Run(body, func(t *testing.T) {
			t.Setenv(execution.AgentRoleVariable, "")
			stateRoot := t.TempDir()
			t.Setenv(runstate.StateHomeVariable, stateRoot)
			configPath := writeConfig(t, validConfig)
			if body != "" {
				writeArtifact(t, filepath.Dir(configPath), config.DirectoryName+"/roles/specialist.yaml", body)
			}
			stdout, stderr, code := runCLI(t, "role", "activate", "specialist", "--by", "Ada", "--config", configPath, "--json")
			var output roleOutput
			if err := json.Unmarshal([]byte(stdout), &output); err != nil || code != 1 || output.Error == "" {
				t.Fatalf("activate = %d, %q, %q, %v", code, stdout, stderr, err)
			}
			if body == "" {
				if !strings.Contains(output.Error, "no role definition") {
					t.Fatalf("missing definition error = %q", output.Error)
				}
			} else if !strings.Contains(output.Error, "specialist.yaml") || strings.Contains(output.Error, "no role definition") {
				t.Fatalf("invalid definition was not loaded and refused: %q", output.Error)
			}
			if entries, err := os.ReadDir(stateRoot); err != nil || len(entries) != 0 {
				t.Fatalf("refused activation created state: %v, %v", entries, err)
			}
		})
	}
}

func TestRoleActivationRefusesAnAgentBeforeLoadingAnything(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "reviewer")
	stateRoot := t.TempDir()
	t.Setenv(runstate.StateHomeVariable, stateRoot)
	for _, jsonOutput := range []bool{false, true} {
		args := []string{"role", "activate", "specialist", "--by", "Ada", "--config", filepath.Join(t.TempDir(), "absent.yaml")}
		if jsonOutput {
			args = append(args, "--json")
		}
		stdout, stderr, code := runCLI(t, args...)
		message := stderr
		if jsonOutput {
			var output roleOutput
			if err := json.Unmarshal([]byte(stdout), &output); err != nil {
				t.Fatal(err)
			}
			message = output.Error
		}
		want := "yoyo role activate is refused from a process the harness launched for the reviewer: a person activates a role definition, and an agent's process is not one"
		if code != 1 || strings.TrimSpace(message) != want {
			t.Fatalf("agent activation = %d, %q, want %q", code, message, want)
		}
	}
	if entries, err := os.ReadDir(stateRoot); err != nil || len(entries) != 0 {
		t.Fatalf("refused activation created state: %v, %v", entries, err)
	}
}

func TestRoleActivationCommandsAgreeWithConfiguredRepositoryStateRoot(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv("YOYODYNE_CONFIG_HOME", t.TempDir())
	first := t.TempDir()
	t.Setenv(runstate.StateHomeVariable, first)
	project, configPath := stateRootProject(t)
	repository := filepath.Join(project, "checkout")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "init", "-b", "main")
	if err := os.WriteFile(configPath, []byte(strings.Replace(validConfig, "repository: .", "repository: checkout", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, project, config.DirectoryName+"/roles/specialist.yaml", "extends: architect\n")
	activation := roleCLIOutput(t, "activate", configPath, "specialist", "--by", "Ada").Recorded
	marker := filepath.Join(repository, ".git", "yoyodyne", "state-root")
	if content, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(content)) != first {
		t.Fatalf("configured repository marker = %q, %v; want %q", content, err, first)
	}
	if history := roleCLIOutput(t, "history", configPath).Activations; len(history) != 1 || history[0].ID != activation.ID {
		t.Fatalf("history on the recorded root = %#v", history)
	}

	second := t.TempDir()
	t.Setenv(runstate.StateHomeVariable, second)
	for _, verb := range []string{"activate", "list", "history"} {
		for _, jsonOutput := range []bool{false, true} {
			args := []string{"role", verb, "--config", configPath}
			if verb == "activate" {
				args = append(args, "specialist", "--by", "Ada")
			}
			if jsonOutput {
				args = append(args, "--json")
			}
			stdout, stderr, code := runCLI(t, args...)
			message := stderr
			if jsonOutput {
				var output roleOutput
				if err := json.Unmarshal([]byte(stdout), &output); err != nil {
					t.Fatal(err)
				}
				message = output.Error
			}
			if code != 1 {
				t.Fatalf("%v on a second root = %d, %q, %q; want refusal", args, code, stdout, stderr)
			}
			for _, want := range []string{first, second, marker, runstate.RootOriginEnvironment} {
				if !strings.Contains(message, want) {
					t.Errorf("%v refusal %q does not name %q", args, message, want)
				}
			}
		}
	}
	if entries, err := os.ReadDir(second); err != nil || len(entries) != 0 {
		t.Fatalf("refused role commands wrote under the second root: %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "yoyodyne", "state-root")); !os.IsNotExist(err) {
		t.Fatalf("role commands opened the unconfigured repository's marker: %v", err)
	}
}

func roleCLIOutput(t *testing.T, verb, configPath string, args ...string) roleOutput {
	t.Helper()
	command := append([]string{"role", verb, "--config", configPath, "--json"}, args...)
	stdout, stderr, code := runCLI(t, command...)
	var output roleOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil || code != 0 {
		t.Fatalf("%v = %d, %q, %q, %v", command, code, stdout, stderr, err)
	}
	return output
}

// definitionAgentsConfig configures two agents on role definitions: an
// architect that gains the read model and loses the ask channel, and a developer
// that loses its worktree write.
const definitionAgentsConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  architect:
    role: specialist
    backend: claude-code
    model: opus
  developer:
    role: narrow
    backend: claude-code
    model: opus
`

// An agent's role: may name a role definition, and every reader of what the
// agent may do reads the definition's tool set once a person has activated it:
// configuration validation, the conversation authority table, the tool access
// the backend is held to, and `yoyo config show`'s capabilities.
func TestAnAgentOnAnActivatedRoleDefinitionIsReadByEveryAuthorityReader(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(runstate.StateHomeVariable, t.TempDir())
	project := t.TempDir()
	configPath := filepath.Join(project, config.FileName)
	if err := os.WriteFile(configPath, []byte(definitionAgentsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, project, config.DirectoryName+"/roles/specialist.yaml",
		"extends: architect\ntools:\n  add: [readmodel.read]\n  remove: [exchange.ask]\n")
	writeArtifact(t, project, config.DirectoryName+"/roles/narrow.yaml",
		"extends: developer\ntools:\n  remove: [worktree.mutate]\n")

	// Before activation, every command that reads or runs agents refuses the
	// configuration, naming the definitions and how a person activates them.
	stdout, stderr, code := runCLI(t, "config", "show", "--config", configPath)
	if code == 0 || !strings.Contains(stderr, `role definition "specialist": nobody has activated it`) || !strings.Contains(stderr, "yoyo role activate narrow") {
		t.Fatalf("config show before activation = %d, %q, %q", code, stdout, stderr)
	}
	if _, err := loadConfiguration(configPath); err == nil {
		t.Fatal("loadConfiguration() accepted an agent on a definition nobody activated")
	}
	// The role commands are how a definition is activated, so they still load.
	if listed := roleCLIOutput(t, "list", configPath); len(listed.Roles) != 2 {
		t.Fatalf("role list = %#v", listed)
	}
	roleCLIOutput(t, "activate", configPath, "specialist", "--by", "Ada")
	roleCLIOutput(t, "activate", configPath, "narrow", "--by", "Ada")

	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatalf("loadConfiguration() after activation = %v", err)
	}
	architect := resolved.Config.Agents["architect"]
	developer := resolved.Config.Agents["developer"]
	if architect.Role != domain.RoleArchitect || architect.Definition == nil || architect.Definition.Name != "specialist" {
		t.Fatalf("architect = %+v, want the architect role filled by specialist", architect)
	}

	// yoyo config show reports the definition's tool set.
	stdout, stderr, code = runCLI(t, "config", "show", "--json", "--config", configPath)
	var shown struct {
		Effective config.Config `json:"effective"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil || code != 0 {
		t.Fatalf("config show = %d, %q, %q, %v", code, stdout, stderr, err)
	}
	shownArchitect := shown.Effective.Agents["architect"]
	if !slices.Contains(shownArchitect.Capabilities, capability.ReadModelRead) || slices.Contains(shownArchitect.Capabilities, capability.ExchangeAsk) ||
		shownArchitect.Definition == nil || shownArchitect.Definition.Name != "specialist" {
		t.Fatalf("config show architect = %+v, want the definition's tool set and name", shownArchitect)
	}
	if slices.Contains(shown.Effective.Agents["developer"].Capabilities, capability.WorktreeMutate) {
		t.Fatal("config show reports the worktree write the developer's definition removed")
	}

	// The conversation authority table reads the definition's set: the
	// shipped architect asks other roles, this one does not.
	if shipped, _ := chat.AuthorityFor(domain.RoleArchitect); !shipped.Asks {
		t.Fatal("the shipped architect does not ask, so this test asserts nothing")
	}
	authority := conversationAuthority(architect.Role, architect)
	if authority.Asks || !authority.Answers || !authority.RepositoryReads || authority.Contract == "" {
		t.Fatalf("architect authority = %+v, want the shipped contract without the ask", authority)
	}
	if conversationExchanges(components{}, authority, nil, nil) != nil {
		t.Fatal("an agent whose definition removed the ask was wired to the ask channel")
	}

	// The tool access the backend is held to, and capability validation of it.
	if developer.Posture() != backend.PostureReadOnly || architect.Posture() != backend.PostureReadOnly {
		t.Fatalf("postures = %q, %q", developer.Posture(), architect.Posture())
	}
	providers, err := resolved.Config.ProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.ServesAt(developer.Backend, developer.Role, developer.Posture()); err != nil {
		t.Fatalf("ServesAt() = %v", err)
	}
	if _, err := backend.RequestPosture(backend.RunRequest{Role: developer.Role, Posture: developer.Posture()}); err != nil {
		t.Fatalf("RequestPosture() = %v", err)
	}

	// A definition amended after activation stops binding at the next read —
	// the read a watching session makes at every pull through buildComponents.
	specialistPath := filepath.Join(project, config.DirectoryName, "roles", "specialist.yaml")
	original, err := os.ReadFile(specialistPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specialistPath, append(append([]byte{}, original...), []byte("# widened by hand\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfiguration(configPath); err == nil || !strings.Contains(err.Error(), `role definition "specialist": its file has changed since Ada activated`) {
		t.Fatalf("loadConfiguration() after an amendment = %v, want the amended definition refused", err)
	}
	if err := os.WriteFile(specialistPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	// A file that moved since activation is not activated where it now stands,
	// whatever its content.
	moved := filepath.Join(project, "roles", "narrow.yaml")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(project, config.DirectoryName, "roles", "narrow.yaml"), moved); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfiguration(configPath); err == nil || !strings.Contains(err.Error(), `role definition "narrow": it now sits at `+moved) {
		t.Fatalf("loadConfiguration() after the move = %v, want the moved definition refused", err)
	}
	stdout, stderr, code = runCLI(t, "role", "list", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "moved since activation") {
		t.Fatalf("role list after the move = %d, %q, %q", code, stdout, stderr)
	}
}

// The Slack process reads the configuration the way every other command does:
// a project whose agent fills a role definition is refused until a person
// activates it, and accepted once they have — by `yoyo slack ensure` and by the
// sink's reading of what the template has improved alike.
func TestTheSlackProcessReadsAnAgentOnAnActivatedRoleDefinition(t *testing.T) {
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(runstate.StateHomeVariable, t.TempDir())
	project := t.TempDir()
	configPath := filepath.Join(project, config.FileName)
	if err := os.WriteFile(configPath, []byte(definitionAgentsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, project, config.DirectoryName+"/roles/specialist.yaml",
		"extends: architect\ntools:\n  add: [readmodel.read]\n  remove: [exchange.ask]\n")
	writeArtifact(t, project, config.DirectoryName+"/roles/narrow.yaml",
		"extends: developer\ntools:\n  remove: [worktree.mutate]\n")

	stdout, stderr, code := runCLI(t, "slack", "ensure", "--config", configPath)
	if code == 0 || !strings.Contains(stderr, "nobody has activated it") {
		t.Fatalf("slack ensure before activation = %d, %q, %q, want the unactivated definition refused", code, stdout, stderr)
	}
	roleCLIOutput(t, "activate", configPath, "specialist", "--by", "Ada")
	roleCLIOutput(t, "activate", configPath, "narrow", "--by", "Ada")

	stdout, stderr, code = runCLI(t, "slack", "ensure", "--config", configPath)
	if code != 0 {
		t.Fatalf("slack ensure after activation = %d, %q, %q, want the project accepted", code, stdout, stderr)
	}
	if _, err := (configImprovements{path: configPath}).Offered(context.Background()); err != nil {
		t.Fatalf("the sink's configuration read refused an activated definition: %v", err)
	}
}
