package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

func rolecapabilityDefault(t *testing.T) rolecapability.Registry {
	t.Helper()
	registry, err := rolecapability.Default()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestRoleDefinitionsLoadWithoutChangingAuthority(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	configPath := filepath.Join(project, DirectoryName, FileName)
	before, err := LoadResolved(configPath)
	if err != nil {
		t.Fatal(err)
	}
	body := "extends: architect\ntools:\n  add: [backlog.order]\n  remove: [repository.list]\n"
	source := writeRoleDefinition(t, filepath.Dir(configPath), "specialist", body)
	after, err := LoadResolved(configPath)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := after.RoleDefinitions["specialist"]
	if !found || definition.Name != "specialist" || definition.Source != source || definition.Extends != domain.RoleArchitect {
		t.Fatalf("loaded role definition = %+v, found = %t", definition, found)
	}
	if !slices.Equal(definition.Tools.Add, []capability.Capability{capability.BacklogOrder}) ||
		!slices.Equal(definition.Tools.Remove, []capability.Capability{capability.RepositoryList}) {
		t.Fatalf("loaded tools = %+v", definition.Tools)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte(body))); definition.Digest != want {
		t.Fatalf("digest = %q, want %q", definition.Digest, want)
	}
	if !reflect.DeepEqual(before.Config, after.Config) || before.Config.Revision() != after.Config.Revision() ||
		!reflect.DeepEqual(before.Origins, after.Origins) {
		t.Fatal("an unactivated role definition changed effective configuration or authority")
	}
	// Editing an inert file changes its digest, and still no effective authority.
	writeRoleDefinition(t, filepath.Dir(configPath), "specialist", body+"# edited\n")
	edited, err := LoadResolved(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if edited.RoleDefinitions["specialist"].Digest == definition.Digest || !reflect.DeepEqual(after.Config, edited.Config) {
		t.Fatal("editing a definition must change its digest and leave authority alone")
	}
}

func TestRoleDefinitionsRefuseInvalidFiles(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, want string
	}{
		{"missing base", "tools: {}\n", "extends must name exactly one shipped role"},
		{"empty file", "", "decode role definition"},
		{"null file", "null\n", "extends must name exactly one shipped role"},
		{"multiple bases", "extends: [architect, developer]\n", "cannot unmarshal"},
		{"unknown base", "extends: observer\n", `got "observer"`},
		{"custom base", "extends: another-definition\n", `got "another-definition"`},
		{"unknown addition", "extends: architect\ntools:\n  add: [repository.invented]\n", `"repository.invented"`},
		{"unknown removal", "extends: architect\ntools:\n  remove: [repository.invented]\n", `"repository.invented"`},
		{"gate evidence", "extends: architect\ntools:\n  add: [gate-evidence.mint]\n", `"gate-evidence.mint"`},
		{"human gate", "extends: architect\ntools:\n  add: [human-gate.record]\n", `"human-gate.record"`},
		{"unknown key", "extends: architect\ncapabilities: []\n", "field capabilities not found"},
		{"unknown tool key", "extends: architect\ntools:\n  grant: []\n", "field grant not found"},
		{"duplicate key", "extends: architect\nextends: developer\n", "already defined"},
		{"duplicate addition", "extends: architect\ntools:\n  add: [backlog.order, backlog.order]\n", `"backlog.order" more than once`},
		{"duplicate removal", "extends: architect\ntools:\n  remove: [repository.read, repository.read]\n", `"repository.read" more than once`},
		{"conflicting lists", "extends: architect\ntools:\n  add: [repository.read]\n  remove: [repository.read]\n", `"repository.read" is both added and removed`},
		{"unheld removal", "extends: architect\ntools:\n  remove: [backlog.order]\n", "the shipped architect does not hold"},
		{"multiple documents", "extends: architect\n---\nextends: developer\n", "exactly one YAML document"},
		{"trailing malformed document", "extends: architect\n---\n[\n", "decode role definition"},
		{"oversized", "extends: architect\n#" + strings.Repeat("x", MaxRoleDefinitionBytes), "exceeds the limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			writeProject(t, project, minimalProjectConfig, nil)
			source := writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist", test.body)
			resolved, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), source) {
				t.Fatalf("LoadResolved() error = %v, want %q with source %s", err, test.want, source)
			}
			if resolved.RoleDefinitions != nil || resolved.Config.Version != 0 {
				t.Fatal("a refused definition returned a partially loaded configuration")
			}
		})
	}
}

func TestRoleDefinitionsCannotAddRunOperations(t *testing.T) {
	t.Parallel()
	for _, primitive := range []capability.Capability{
		capability.ChecksExecute, capability.ReviewVerdict, capability.ForgePublish,
		capability.TargetBranchMutate, capability.PromotionLease, capability.WorktreeMutate,
		capability.ProviderInvoke, capability.RunStateMutate,
	} {
		t.Run(string(primitive), func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			writeProject(t, project, minimalProjectConfig, nil)
			writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist",
				fmt.Sprintf("extends: architect\ntools:\n  add: [%s]\n", primitive))
			_, err := Load(filepath.Join(project, DirectoryName, FileName))
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("tools.add names %q", primitive)) {
				t.Fatalf("Load() error = %v, want the forbidden primitive named", err)
			}
		})
	}
}

func TestRoleDefinitionsExtendEachShippedRole(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			writeProject(t, project, minimalProjectConfig, nil)
			writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist", "extends: "+string(role)+"\n")
			definitions, err := LoadRoleDefinitions(filepath.Join(project, DirectoryName, FileName))
			if err != nil || definitions["specialist"].Extends != role {
				t.Fatalf("LoadRoleDefinitions() = %v, %v", definitions, err)
			}
		})
	}
}

func TestRoleDefinitionsCanRemoveInheritedRunAuthority(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist",
		"extends: developer\ntools:\n  remove: [checks.execute, forge.publish]\n")
	definitions, err := LoadRoleDefinitions(filepath.Join(project, DirectoryName, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(definitions["specialist"].Tools.Remove, []capability.Capability{capability.ChecksExecute, capability.ForgePublish}) {
		t.Fatal("the prohibition on additions also refused removal of inherited authority")
	}
}

func TestAnAgentRoleNamingADefinitionHoldsTheDefinitionsToolSet(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+"agents:\n  architect:\n    role: specialist\n  developer:\n    role: narrow\n", nil)
	directory := filepath.Join(project, DirectoryName)
	specialist := writeRoleDefinition(t, directory, "specialist",
		"extends: architect\ntools:\n  add: [readmodel.read]\n  remove: [exchange.ask]\n")
	writeRoleDefinition(t, directory, "narrow", "extends: developer\ntools:\n  remove: [worktree.mutate]\n")
	path := filepath.Join(directory, FileName)
	// The plain loader cannot read the activation record, so it binds nothing.
	if _, err := LoadResolved(path); err == nil || !strings.Contains(err.Error(), `agent "architect" fills role definition "specialist"`) || !strings.Contains(err.Error(), "cannot check the definition's activation") {
		t.Fatalf("LoadResolved() error = %v, want an agent on a definition refused without the activation record", err)
	}
	// A definition the record does not call activated refuses the whole
	// configuration, naming the agent, and binds nobody.
	refusing := func(Resolved) (RoleActivationCheck, error) {
		return func(definition RoleDefinition) error {
			if definition.Name == "narrow" {
				return fmt.Errorf("nobody has activated it")
			}
			return nil
		}, nil
	}
	if _, err := LoadResolvedActivated(path, refusing); err == nil || !strings.Contains(err.Error(), `agent "developer" fills role definition "narrow": nobody has activated it`) {
		t.Fatalf("LoadResolvedActivated() with narrow unactivated = %v", err)
	}
	unreadable := func(Resolved) (RoleActivationCheck, error) { return nil, fmt.Errorf("record unreadable") }
	if _, err := LoadResolvedActivated(path, unreadable); err == nil || !strings.Contains(err.Error(), "record unreadable") {
		t.Fatalf("LoadResolvedActivated() with an unreadable record = %v, want a refusal", err)
	}
	if _, err := LoadResolvedActivated(path, nil); err == nil {
		t.Fatal("LoadResolvedActivated() with no record bound an agent")
	}
	activated := func(Resolved) (RoleActivationCheck, error) {
		return func(RoleDefinition) error { return nil }, nil
	}
	resolved, err := LoadResolvedActivated(path, activated)
	if err != nil {
		t.Fatalf("LoadResolvedActivated() error = %v, want an agent's role to name an activated role definition", err)
	}

	architect := resolved.Config.Agents["architect"]
	if architect.Role != domain.RoleArchitect {
		t.Fatalf("architect role = %q, want the shipped role the definition extends", architect.Role)
	}
	source, err := filepath.EvalSymlinks(specialist)
	if err != nil {
		t.Fatal(err)
	}
	definition := resolved.RoleDefinitions["specialist"]
	if architect.Definition == nil || architect.Definition.Name != "specialist" || architect.Definition.Digest != definition.Digest || architect.Definition.Source != definition.Source {
		t.Fatalf("architect definition = %+v, want specialist pinned to %s from %s", architect.Definition, definition.Digest, source)
	}
	shipped, _ := rolecapabilityDefault(t).Bundle(domain.RoleArchitect)
	var want []capability.Capability
	for _, declared := range capability.All() {
		if declared == capability.ReadModelRead || (declared != capability.ExchangeAsk && slices.Contains(shipped.Holds, declared)) {
			want = append(want, declared)
		}
	}
	if !slices.Equal(architect.Capabilities, want) {
		t.Fatalf("architect capabilities = %v, want the shipped set plus the addition minus the removal %v", architect.Capabilities, want)
	}
	if !architect.Holds(capability.ReadModelRead) || architect.Holds(capability.ExchangeAsk) {
		t.Fatal("Holds() did not read the definition's set")
	}
	if got := resolved.Origins["agents.architect.capabilities"]; got != OriginRoleDefinition+"specialist" {
		t.Fatalf("capabilities origin = %q, want the definition named", got)
	}
	if architect.Posture() != backend.PostureReadOnly {
		t.Fatalf("architect posture = %q", architect.Posture())
	}

	developer := resolved.Config.Agents["developer"]
	if developer.Role != domain.RoleDeveloper || developer.Holds(capability.WorktreeMutate) {
		t.Fatalf("developer = %+v, want the developer role without the worktree write", developer)
	}
	if developer.Posture() != backend.PostureReadOnly {
		t.Fatalf("developer posture = %q, want a definition that removed the worktree write held read-only", developer.Posture())
	}
	if shippedDeveloper := (AgentConfig{Role: domain.RoleDeveloper}); shippedDeveloper.Posture() != backend.PostureWorktreeWrite || !shippedDeveloper.Holds(capability.WorktreeMutate) {
		t.Fatal("an agent on the shipped developer role lost its worktree write")
	}
	if bound := resolved.Config.BoundDefinitions(); len(bound) != 2 || bound["architect"].Name != "specialist" || bound["developer"].Name != "narrow" {
		t.Fatalf("BoundDefinitions() = %v", bound)
	}
}

func TestAnAgentRoleNamingNoDefinitionIsStillRefused(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+"agents:\n  architect:\n    role: specialist\n", nil)
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "other", "extends: architect\n")
	_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
	if err == nil || !strings.Contains(err.Error(), `unknown role "specialist"`) || !strings.Contains(err.Error(), "role definition") {
		t.Fatalf("LoadResolved() error = %v, want the unknown name refused naming both kinds of role", err)
	}
}

func TestRoleHistoryReadsTheProductWhateverTheAgentsName(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+"agents:\n  architect:\n    role: specialist\n", nil)
	// A definition that no longer loads must not hide who activated it.
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist", "extends: observer\n")
	product, err := RoleHistoryProduct(filepath.Join(project, DirectoryName, FileName))
	if err != nil || product.ID != "example" {
		t.Fatalf("RoleHistoryProduct() = %+v, %v", product, err)
	}
	// The role commands list and activate definitions while an agent names one
	// nobody has activated; that read binds no agent.
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist", "extends: architect\n")
	beside, err := LoadRoleDefinitionsBeside(filepath.Join(project, DirectoryName, FileName))
	if err != nil || len(beside.RoleDefinitions) != 1 || len(beside.Config.Agents) != 0 {
		t.Fatalf("LoadRoleDefinitionsBeside() = %+v, %v", beside, err)
	}
}

func TestRoleDefinitionsUseConfigurationDirectoryOrder(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	if err := os.WriteFile(filepath.Join(project, FileName), []byte(minimalProjectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRoleDefinition(t, project, "specialist", "extends: architect\n")
	// The lower-priority copy must not supply a base or hide a primary refusal.
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist", "extends: observer\n")
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "fallback", "extends: developer\n")
	resolved, err := LoadResolved(filepath.Join(project, FileName))
	if err != nil || len(resolved.RoleDefinitions) != 2 || resolved.RoleDefinitions["specialist"].Extends != domain.RoleArchitect {
		t.Fatalf("LoadResolved() definitions = %v, error = %v", resolved.RoleDefinitions, err)
	}
	writeRoleDefinition(t, project, "specialist", "extends: observer\n")
	writeRoleDefinition(t, filepath.Join(project, DirectoryName), "specialist", "extends: architect\n")
	if _, err := LoadResolved(filepath.Join(project, FileName)); err == nil {
		t.Fatal("an invalid primary definition fell back to a valid secondary copy")
	}
	// Legacy configuration keeps definitions under .yoyodyne alone.
	definitions, err := LoadRoleDefinitions(filepath.Join(project, LegacyFileName))
	if err != nil || definitions["specialist"].Extends != domain.RoleArchitect {
		t.Fatalf("legacy definitions = %v, error = %v", definitions, err)
	}
}

func TestRoleDefinitionsRefuseBrokenPaths(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{"directory escape", "file escape", "dangling directory", "dangling file", "directory file", "invalid name"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			writeProject(t, project, minimalProjectConfig, nil)
			root := filepath.Join(project, DirectoryName)
			outside := t.TempDir()
			source := writeRoleDefinition(t, outside, "specialist", "extends: architect\n")
			roles := filepath.Join(root, "roles")
			var err error
			switch shape {
			case "directory escape":
				err = os.Symlink(filepath.Dir(source), roles)
			case "dangling directory":
				err = os.Symlink(filepath.Join(outside, "missing"), roles)
			default:
				err = os.MkdirAll(roles, 0o700)
				if err == nil {
					switch shape {
					case "file escape":
						err = os.Symlink(source, filepath.Join(roles, "specialist.yaml"))
					case "dangling file":
						err = os.Symlink("missing", filepath.Join(roles, "specialist.yaml"))
					case "directory file":
						err = os.Mkdir(filepath.Join(roles, "specialist.yaml"), 0o700)
					case "invalid name":
						writeRoleDefinition(t, root, "Specialist", "extends: architect\n")
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LoadResolved(filepath.Join(root, FileName)); err == nil || !strings.Contains(err.Error(), root) {
				t.Fatalf("LoadResolved() error = %v, want broken role path named", err)
			}
		})
	}
}

func writeRoleDefinition(t *testing.T, directory, name, body string) string {
	t.Helper()
	roles := filepath.Join(directory, "roles")
	if err := os.MkdirAll(roles, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(roles, name+".yaml")
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}
