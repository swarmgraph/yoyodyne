package backend

// Which providers this project may name, and what each of them can be asked to
// do.
//
// The capability model has to stay honest as it grows. Codex is already
// documented as not matching every Claude Code feature, and an unsupported role
// fails validation before any work is assigned; that has to hold for a provider
// nobody here has seen, which means the answer cannot be a switch statement over
// the backends this build happens to know. It is a registry: the built-ins are
// data, a user-supplied plugin is the same data read out of configuration, and
// the question "may this agent run on this backend" is asked of both the same
// way.
//
// The registry is constructed rather than global. Registration by package
// initializer would make what a project may name depend on which packages
// happened to be linked in, and configuration that validates differently
// depending on who imported what is configuration nobody can reason about.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Posture is the access a role needs from its provider. Developers may edit a
// worktree; other roles may inspect evidence without modifying it. Adapters may
// enforce read-only access more narrowly, including by disabling tools entirely.
type Posture string

const (
	// PostureReadOnly permits inspection without filesystem changes, network
	// access from tools, or permission escalation. It does not promise filesystem
	// read isolation: a native read-only sandbox may read outside the repository.
	PostureReadOnly Posture = "read-only"
	// PostureWorktreeWrite is an agent whose work is editing a worktree. It
	// requires a backend that can scope writes to that worktree.
	PostureWorktreeWrite Posture = "worktree-write"
)

// Postures is every posture a backend may declare, for a refusal that shows the
// choices.
var Postures = []Posture{PostureReadOnly, PostureWorktreeWrite}

func (p Posture) Valid() bool {
	for _, known := range Postures {
		if p == known {
			return true
		}
	}
	return false
}

// PostureFor is what a role needs of whatever backend runs it. The developer is
// the only role whose work is editing a worktree; each of the others decides
// something the harness then carries out on its behalf, so their tools cannot
// perform those mutations. Roles are listed rather than inverted, so a role nobody has
// decided a posture for answers with no posture instead of inheriting the
// developer's.
func PostureFor(role domain.AgentRole) Posture {
	switch role {
	case domain.RoleDeveloper:
		return PostureWorktreeWrite
	case domain.RoleReviewer, domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleProgramManager:
		return PostureReadOnly
	default:
		return ""
	}
}

// Descriptor is everything the harness knows about one provider without running
// it: which roles it serves, which postures it can hold them to, what it can do,
// and how to read what it says.
type Descriptor struct {
	ID domain.Backend
	// Adapter is the backend whose compiled adapter launches this provider, and
	// is empty for a provider nothing in this build can launch. A built-in that
	// ships an adapter names itself, and a declared provider names the built-in
	// whose adapter runs it.
	//
	// It exists because a dialect that nothing can attach to is a plugin that
	// loads and can never fire. A declaration says which compiled adapter starts
	// the process and reads the stream, and supplies the dialect that adapter
	// reads the stream *with*; a declaration naming an adapter this build does
	// not ship is refused where it is written.
	Adapter domain.Backend
	// Binary is the executable the adapter runs, and is empty for the adapter's
	// own default. It is what makes a fork or a proxy of a provider this build
	// already speaks reachable without a second adapter.
	Binary string
	// AdapterVersion is the version of the compiled adapter named above, and is
	// empty for a provider nothing in this build can launch. It is what an
	// endpoint carries and what a record says read the provider's stream, so a
	// declared provider inherits the version of the adapter it runs on rather
	// than stating one of its own: the declaration supplies a dialect, and the
	// code that reads the stream with it is this build's.
	AdapterVersion string
	Capabilities   Capabilities
	Roles          []domain.AgentRole
	Postures       []Posture
	// Dialect is how this provider's operational vocabulary is read. It is set
	// for a user-supplied provider, whose dialect is data, and the adapter that
	// runs the provider is handed it in place of its own. A built-in leaves it
	// nil and carries its dialect as code beside its adapter, which is the trade
	// the two delivery mechanisms make: code can read a shape data cannot
	// describe, and data needs no fork.
	Dialect Dialect
	// BuiltIn distinguishes a provider this build ships from one a project
	// declared, which is what a refusal has to say to be actionable: one of them
	// is a typo and the other is a plugin that was never written.
	BuiltIn bool
	// EffortLevels are the effort levels this provider's adapter accepts, in the
	// provider's own order from least to most, and nil for a provider that
	// accepts none. A declared provider inherits its adapter's, because the level
	// is passed on the command line the adapter builds. See effort.go.
	EffortLevels  []string
	DefaultEffort string
}

// Runnable reports a provider something in this build can actually launch.
func (d Descriptor) Runnable() bool { return d.Adapter != "" }

func (d Descriptor) SupportsRole(role domain.AgentRole) bool {
	if !role.Valid() {
		return false
	}
	for _, served := range d.Roles {
		if served == role {
			return true
		}
	}
	return false
}

func (d Descriptor) SupportsPosture(posture Posture) bool {
	for _, held := range d.Postures {
		if held == posture {
			return true
		}
	}
	return false
}

// BuiltInDescriptors describes the providers this build can launch. Both serve
// every role; their adapters enforce each role's required access independently.
func BuiltInDescriptors() []Descriptor {
	return []Descriptor{
		{
			ID:             domain.BackendClaudeCode,
			Adapter:        domain.BackendClaudeCode,
			AdapterVersion: ClaudeCodeAdapterVersion,
			Capabilities: Capabilities{
				StructuredEvents:  true,
				SessionResumption: true,
				StructuredOutput:  true,
				ToolControl:       true,
				LocalAuth:         true,
			},
			Roles:        domain.Roles(),
			Postures:     Postures,
			BuiltIn:      true,
			EffortLevels: claudeCodeEffortLevels,
		},
		{
			ID:             domain.BackendCodex,
			Adapter:        domain.BackendCodex,
			AdapterVersion: CodexAdapterVersion,
			// Structured output is absent because Codex enforces no schema on what
			// an agent finally says: the adapter reads the last message and nothing
			// makes it the shape anybody asked for. Tool control is present in the
			// sense the harness needs — the sandbox decides what the agent may do —
			// and not in the sense of naming individual tools, which Codex has no
			// way to do.
			Capabilities: Capabilities{
				StructuredEvents:  true,
				SessionResumption: true,
				ToolControl:       true,
				LocalAuth:         true,
			},
			Roles:        domain.Roles(),
			Postures:     Postures,
			BuiltIn:      true,
			EffortLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
		},
	}
}

// RunnableAdapters names the backends this build ships an adapter for, which is
// what a declared provider may name as the thing that launches it.
func RunnableAdapters() []domain.Backend {
	var runnable []domain.Backend
	for _, descriptor := range BuiltInDescriptors() {
		if descriptor.Runnable() {
			runnable = append(runnable, descriptor.ID)
		}
	}
	return runnable
}

func describeRunnableAdapters() string {
	named := make([]string, 0, 2)
	for _, id := range RunnableAdapters() {
		named = append(named, string(id))
	}
	return strings.Join(named, ", ")
}

// BuiltInDescriptor is what this build knows about one of the providers it
// ships. An adapter reads its own description from here rather than restating
// it, so what the harness validates a configuration against and what the adapter
// reports about itself cannot drift apart.
func BuiltInDescriptor(id domain.Backend) (Descriptor, bool) {
	for _, descriptor := range BuiltInDescriptors() {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// ProviderPlugin is a provider a project declares for itself: which roles it
// serves, which postures it can hold them to, what it can do, and the dialect
// that reads what it says. It is the whole of a user-supplied provider's
// description, and it holds nothing that decides anything.
type ProviderPlugin struct {
	// Adapter is the backend this build ships an adapter for whose adapter
	// launches this provider and reads its stream. It is required, because a
	// dialect nothing can attach to is a plugin that loads and can never fire:
	// naming the adapter is what gives the declared rules an invocation to
	// observe.
	Adapter domain.Backend `yaml:"adapter" json:"adapter"`
	// Binary is the executable that adapter runs, and is empty for the adapter's
	// own default. A fork or a proxy of a provider this build already speaks is
	// reached by naming its binary here rather than by writing a second adapter.
	Binary       string             `yaml:"binary,omitempty" json:"binary,omitempty"`
	Roles        []domain.AgentRole `yaml:"roles" json:"roles"`
	Postures     []Posture          `yaml:"postures" json:"postures"`
	Capabilities Capabilities       `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Dialect      DialectSpec        `yaml:"dialect" json:"dialect"`
}

// Registry is the set of providers a project may name.
type Registry struct {
	descriptors map[domain.Backend]Descriptor
	order       []domain.Backend
}

// NewRegistry builds the registry for a project: the built-ins, plus whatever
// providers the project declared. Everything wrong with the declarations is
// reported at once, and none of it is reported as a run that fails later --
// which is the whole point of validating a provider before work is assigned to
// an agent that names it.
func NewRegistry(plugins map[domain.Backend]ProviderPlugin) (*Registry, error) {
	registry := &Registry{descriptors: map[domain.Backend]Descriptor{}}
	for _, descriptor := range BuiltInDescriptors() {
		registry.add(descriptor)
	}

	names := make([]string, 0, len(plugins))
	for id := range plugins {
		names = append(names, string(id))
	}
	sort.Strings(names)

	var problems []string
	for _, name := range names {
		id := domain.Backend(name)
		if existing, taken := registry.descriptors[id]; taken && existing.BuiltIn {
			problems = append(problems, fmt.Sprintf("provider %q is a backend this build ships, so declaring one would replace it", name))
			continue
		}
		descriptor, err := DescriptorFor(id, plugins[id])
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		registry.add(descriptor)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return registry, nil
}

// DescriptorFor turns one declared provider into a descriptor, or reports
// everything wrong with it. A provider that serves no role, holds no posture, or
// cannot read anything its provider says is refused here: each of those is a
// plugin that loads and then does nothing on the day it matters.
func DescriptorFor(id domain.Backend, plugin ProviderPlugin) (Descriptor, error) {
	var problems []string
	if err := domain.ValidateIdentifier("identifier", string(id)); err != nil {
		problems = append(problems, fmt.Sprintf("is named something an agent could not name: %v", err))
	}
	// A declaration that names no adapter, or one this build does not ship, is
	// refused here rather than loaded: it would be a set of rules with no
	// invocation to observe, which is a plugin that validates and can never fire.
	adapter, adapterKnown := BuiltInDescriptor(plugin.Adapter)
	switch {
	case strings.TrimSpace(string(plugin.Adapter)) == "":
		problems = append(problems, fmt.Sprintf("names no adapter, so nothing in this build could launch it; adapter is one of %s",
			describeRunnableAdapters()))
	case !adapterKnown || !adapter.Runnable():
		problems = append(problems, fmt.Sprintf("runs on adapter %q, which this build ships no adapter for; adapter is one of %s",
			plugin.Adapter, describeRunnableAdapters()))
	}
	if len(plugin.Roles) == 0 {
		problems = append(problems, "serves no role, so no agent could ever name it")
	}
	for _, role := range plugin.Roles {
		if !role.Valid() {
			problems = append(problems, fmt.Sprintf("serves role %q, which is not one of the harness's roles", role))
		}
	}
	if len(plugin.Postures) == 0 {
		problems = append(problems, fmt.Sprintf("holds no tool access; the tool access a provider can hold is %s", describePostures()))
	}
	for _, posture := range plugin.Postures {
		if !posture.Valid() {
			problems = append(problems, fmt.Sprintf("holds tool access %q, which is not one of %s", posture, describePostures()))
		}
	}
	dialect, err := NewDeclarativeDialect(string(id), plugin.Dialect)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return Descriptor{}, fmt.Errorf("provider %q %s", id, strings.Join(problems, "; "))
	}
	return Descriptor{
		ID:      id,
		Adapter: plugin.Adapter,
		// The adapter that runs this provider is the one whose version its
		// endpoints carry: what a record has to be able to tell apart is two
		// harness builds reading one provider differently, and that is this
		// build's adapter rather than the declaration.
		AdapterVersion: adapter.AdapterVersion,
		Binary:         strings.TrimSpace(plugin.Binary),
		Capabilities:   plugin.Capabilities,
		Roles:          append([]domain.AgentRole(nil), plugin.Roles...),
		Postures:       append([]Posture(nil), plugin.Postures...),
		Dialect:        dialect,
		EffortLevels:   append([]string(nil), adapter.EffortLevels...),
		DefaultEffort:  adapter.DefaultEffort,
	}, nil
}

func (r *Registry) add(descriptor Descriptor) {
	if _, known := r.descriptors[descriptor.ID]; !known {
		r.order = append(r.order, descriptor.ID)
	}
	r.descriptors[descriptor.ID] = descriptor
}

// Lookup is the provider named, and whether this project has one by that name at
// all.
func (r *Registry) Lookup(id domain.Backend) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	descriptor, known := r.descriptors[id]
	return descriptor, known
}

// Backends is every provider this project may name, built-ins first and in the
// order they were added, so a refusal can list them.
func (r *Registry) Backends() []domain.Backend {
	if r == nil {
		return nil
	}
	return append([]domain.Backend(nil), r.order...)
}

func describePostures() string {
	named := make([]string, 0, len(Postures))
	for _, posture := range Postures {
		named = append(named, string(posture))
	}
	return strings.Join(named, ", ")
}
