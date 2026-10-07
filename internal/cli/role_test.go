package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
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
	t.Setenv("HOME", t.TempDir())
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
