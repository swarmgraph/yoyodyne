package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

// MaxRoleDefinitionBytes bounds a role definition before YAML is decoded.
const MaxRoleDefinitionBytes = 32 << 10

// roleDefinitionsDirectory is where definitions sit beside a configuration.
const roleDefinitionsDirectory = "roles"

// OriginRoleDefinition prefixes the origin of an agent's capability set and
// definition where a role definition supplied them; the definition's name
// follows it.
const OriginRoleDefinition = "role-definition:"

// RoleDefinition is a validated, inert definition of a bundle. It records what
// a file says, never what an agent may do: operator activation and an agent
// binding are required before it can supply authority.
type RoleDefinition struct {
	Name    string           `json:"name"`
	Source  string           `json:"source"`
	Digest  string           `json:"digest"`
	Extends domain.AgentRole `json:"extends"`
	Tools   RoleTools        `json:"tools"`
}

// RoleTools lists changes to the shipped bundle named by Extends. The vocabulary
// is the capability registry's; a file can select primitives and invents none.
type RoleTools struct {
	Add    []capability.Capability `yaml:"add" json:"add,omitempty"`
	Remove []capability.Capability `yaml:"remove" json:"remove,omitempty"`
}

type roleDefinitionDocument struct {
	Extends domain.AgentRole `yaml:"extends"`
	Tools   RoleTools        `yaml:"tools"`
}

// LoadRoleDefinitions reads roles/<name>.yaml beside the project's configuration,
// using the same directory order as personas and workflows. The first file for a
// name wins; a bad file refuses the load rather than falling back. No directory
// is required, and loading changes neither the shipped registry nor any agent.
func LoadRoleDefinitions(configPath string) (map[string]RoleDefinition, error) {
	registry, err := rolecapability.Default()
	if err != nil {
		return nil, err
	}
	definitions := make(map[string]RoleDefinition)
	for _, directory := range ConfigurationDirectories(configPath) {
		if err := loadRoleDirectory(directory, registry, definitions); err != nil {
			return nil, err
		}
	}
	return definitions, nil
}

func loadRoleDirectory(directory string, registry rolecapability.Registry, definitions map[string]RoleDefinition) error {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open role definitions directory %s: %w", directory, err)
	}
	defer root.Close()
	// Lstat tells an absent directory from a dangling link. The latter is a
	// broken instruction rather than a project that has defined no roles.
	if _, err := root.Lstat(roleDefinitionsDirectory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect role definitions in %s: %w", directory, err)
	}
	roles, err := root.OpenRoot(roleDefinitionsDirectory)
	if err != nil {
		return fmt.Errorf("open role definitions in %s: %w", directory, err)
	}
	defer roles.Close()
	entries, err := fs.ReadDir(roles.FS(), ".")
	if err != nil {
		return fmt.Errorf("list role definitions in %s: %w", directory, err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		source := filepath.Join(directory, roleDefinitionsDirectory, entry.Name())
		if err := domain.ValidateIdentifier("role definition name", name); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if _, loaded := definitions[name]; loaded {
			continue
		}
		definition, err := readRoleDefinition(roles, entry.Name(), registry)
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		definition.Name, definition.Source = name, source
		definitions[name] = definition
	}
	return nil
}

func readRoleDefinition(root *os.Root, name string, registry rolecapability.Registry) (RoleDefinition, error) {
	// Refuse special files before opening them: opening a named pipe could wait
	// forever, before the byte bound has any chance to apply.
	info, err := root.Stat(name)
	if err != nil {
		return RoleDefinition{}, fmt.Errorf("inspect role definition: %w", err)
	}
	if !info.Mode().IsRegular() {
		return RoleDefinition{}, errors.New("role definition is not a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return RoleDefinition{}, fmt.Errorf("read role definition: %w", err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return RoleDefinition{}, err
	}
	if !info.Mode().IsRegular() {
		return RoleDefinition{}, errors.New("role definition is not a regular file")
	}
	source, err := io.ReadAll(io.LimitReader(file, MaxRoleDefinitionBytes+1))
	if err != nil {
		return RoleDefinition{}, fmt.Errorf("read role definition: %w", err)
	}
	if len(source) > MaxRoleDefinitionBytes {
		return RoleDefinition{}, fmt.Errorf("role definition exceeds the limit of %d bytes", MaxRoleDefinitionBytes)
	}
	return decodeRoleDefinition(source, registry)
}

func decodeRoleDefinition(source []byte, registry rolecapability.Registry) (RoleDefinition, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	var document roleDefinitionDocument
	if err := decoder.Decode(&document); err != nil {
		return RoleDefinition{}, fmt.Errorf("decode role definition: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return RoleDefinition{}, errors.New("role definition must contain exactly one YAML document")
		}
		return RoleDefinition{}, fmt.Errorf("decode role definition: %w", err)
	}
	var problems []string
	if _, known := registry.Bundle(document.Extends); !known {
		problems = append(problems, fmt.Sprintf("extends must name exactly one shipped role (%s), got %q", describeRoles(), document.Extends))
	}
	added := make(map[capability.Capability]bool)
	for _, tool := range document.Tools.Add {
		if problem := roleToolAdditionProblem(tool, registry); problem != "" {
			problems = append(problems, fmt.Sprintf("tools.add names %q: %s", tool, problem))
		}
		if added[tool] {
			problems = append(problems, fmt.Sprintf("tools.add names %q more than once", tool))
		}
		added[tool] = true
	}
	removed := make(map[capability.Capability]bool)
	for _, tool := range document.Tools.Remove {
		if !tool.Known() {
			problems = append(problems, fmt.Sprintf("tools.remove names %q, which the capability registry does not declare", tool))
		} else if document.Extends.Valid() && !registry.Holds(document.Extends, tool) {
			problems = append(problems, fmt.Sprintf("tools.remove names %q, which the shipped %s does not hold", tool, document.Extends))
		}
		if removed[tool] {
			problems = append(problems, fmt.Sprintf("tools.remove names %q more than once", tool))
		}
		if added[tool] {
			problems = append(problems, fmt.Sprintf("%q is both added and removed", tool))
		}
		removed[tool] = true
	}
	if len(problems) > 0 {
		return RoleDefinition{}, ValidationError{Problems: problems}
	}
	digest := sha256.Sum256(source)
	return RoleDefinition{Extends: document.Extends, Tools: document.Tools, Digest: hex.EncodeToString(digest[:])}, nil
}

func roleToolAdditionProblem(tool capability.Capability, registry rolecapability.Registry) string {
	if !tool.Known() {
		return "the capability registry does not declare this primitive"
	}
	if held, harness := registry.HarnessHolds(tool); harness {
		return held.Reason
	}
	// These are run operations, not conversation tools. Inherited shipped
	// authority stays the shipped role's; a definition cannot confer the gates,
	// their evidence, execution, or publication on another role.
	switch tool {
	case capability.ChecksExecute, capability.ReviewVerdict:
		return "checks and review evidence belong to the workflow runtime and cannot be added as tools"
	case capability.ForgePublish:
		return "publication belongs to the harness and cannot be added as a tool"
	case capability.WorktreeMutate, capability.ProviderInvoke, capability.RunStateMutate:
		return "this run operation is not a conversation tool"
	}
	return ""
}

// Holds is the definition's tool set: the shipped role's bundle, plus the
// additions, minus the removals, in the order the vocabulary declares them. The
// loader has already refused every addition no definition may make, so this set
// never holds what the shipped role may not do.
func (d RoleDefinition) Holds(registry rolecapability.Registry) []capability.Capability {
	bundle, _ := registry.Bundle(d.Extends)
	held := make(map[capability.Capability]bool, len(bundle.Holds)+len(d.Tools.Add))
	for _, tool := range bundle.Holds {
		held[tool] = true
	}
	for _, tool := range d.Tools.Add {
		held[tool] = true
	}
	for _, tool := range d.Tools.Remove {
		delete(held, tool)
	}
	ordered := make([]capability.Capability, 0, len(held))
	for _, declared := range capability.All() {
		if held[declared] {
			ordered = append(ordered, declared)
		}
	}
	return ordered
}

// bindRoleDefinitions resolves every agent whose role names a loaded role
// definition rather than a shipped role. The agent takes the shipped role the
// definition extends as its role, the definition's tool set as its
// capabilities, and the definition's name, digest, and file as the record of
// what it fills. A role that names neither is left for Validate, which refuses
// it naming both kinds of name.
//
// Binding and activation are one step. Nothing is bound until the activation
// record has been read and says the definition is activated as it stands, and
// with no record to read every agent naming a definition is refused: a
// definition supplies authority only through a person's activation of its
// exact digest from its current file.
func (r *Resolved) bindRoleDefinitions(activations RoleActivationReader) error {
	named := make([]string, 0)
	for _, name := range sortedConfiguredAgents(r.Config.Agents) {
		role := r.Config.Agents[name].Role
		if _, found := r.RoleDefinitions[string(role)]; found && !role.Valid() {
			named = append(named, name)
		}
	}
	if len(named) == 0 {
		return nil
	}
	if activations == nil {
		problems := make([]string, 0, len(named))
		for _, name := range named {
			problems = append(problems, fmt.Sprintf("agent %q fills role definition %q, and this reader of the configuration cannot check the definition's activation", name, r.Config.Agents[name].Role))
		}
		return ValidationError{Problems: problems}
	}
	activated, err := activations(*r)
	if err != nil {
		return fmt.Errorf("read the role activations the configuration's agents depend on: %w", err)
	}
	registry, err := rolecapability.Default()
	if err != nil {
		return err
	}
	var problems []string
	for _, name := range named {
		agent := r.Config.Agents[name]
		definition := r.RoleDefinitions[string(agent.Role)]
		if err := activated(definition); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q fills role definition %q: %v", name, definition.Name, err))
			continue
		}
		agent.Role = definition.Extends
		agent.Capabilities = definition.Holds(registry)
		agent.Definition = &AgentDefinition{Name: definition.Name, Digest: definition.Digest, Source: definition.Source}
		r.Config.Agents[name] = agent
		if r.Origins == nil {
			r.Origins = map[string]string{}
		}
		r.Origins["agents."+name+".capabilities"] = OriginRoleDefinition + definition.Name
		r.Origins["agents."+name+".definition"] = OriginRoleDefinition + definition.Name
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

// BoundDefinitions is every role definition an agent fills, by agent name.
func (c Config) BoundDefinitions() map[string]AgentDefinition {
	bound := make(map[string]AgentDefinition)
	for name, agent := range c.Agents {
		if agent.Definition != nil {
			bound[name] = *agent.Definition
		}
	}
	return bound
}

func sortedConfiguredAgents(agents map[string]AgentConfig) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
