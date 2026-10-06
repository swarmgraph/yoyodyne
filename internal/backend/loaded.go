package backend

import (
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Skills, plugins, and instruction files are what a coding CLI puts in front of
// a model beside the prompt it was handed. The CLI finds them in the account's
// own home, so an invocation made under somebody's login is otherwise also made
// under that person's skills and instructions: on 2026-10-03 to 05, 143 of 258
// harness Codex sessions read the operator's personal code-review skill, which
// no role contract or persona named and nobody here had reviewed.
//
// So what an invocation is given beyond its prompt is decided by the project and
// recorded on the run, the way its model and effort are. A project names what it
// wants in its configuration; everything else is kept out where the adapter can
// keep it out, and what was put in is said rather than inferred afterwards.

// ContextFile is one skill or instruction file a project names for its agents.
// Path is the file or, for a skill, the directory holding its SKILL.md; a
// relative path is read from the repository the invocation works in, and a
// leading "~/" is the home directory of whoever runs the harness. Roles narrows
// it to some roles; naming none gives it to every role.
type ContextFile struct {
	Path  string             `yaml:"path" json:"path"`
	Roles []domain.AgentRole `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// AppliesTo reports whether role is given this file.
func (f ContextFile) AppliesTo(role domain.AgentRole) bool {
	if len(f.Roles) == 0 {
		return true
	}
	for _, named := range f.Roles {
		if named == role {
			return true
		}
	}
	return false
}

// NamedContext is every skill and instruction file a project names. Plugins are
// not nameable: an adapter keeps them all out.
type NamedContext struct {
	Skills       []ContextFile `yaml:"skills,omitempty" json:"skills,omitempty"`
	Instructions []ContextFile `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

// LoadedItem is one thing an invocation was given beside its prompt: its name,
// and where it came from — the project configuration, the repository, or the
// provider home — with the path it was read from.
type LoadedItem struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

// The sources a loaded item can come from.
const (
	LoadedFromProjectConfiguration = "project configuration"
	LoadedFromRepository           = "repository"
)

// Loaded is what one invocation was given beside its prompt. Reported is false
// for an adapter that does not account for it, which is not the same as having
// loaded nothing: Summary says "not reported" there and "none" for an empty kind
// of an adapter that does.
//
// Settings sources and connectors are kinds only some providers have: Claude
// Code reads settings files and connects the account's claude.ai connectors,
// and Codex has neither by those names. SettingsAndConnectors says the adapter
// accounts for both, and only then does the summary name them, so each
// provider's line lists the kinds it has and says "none" for an empty one.
type Loaded struct {
	Reported              bool         `json:"reported"`
	SettingsAndConnectors bool         `json:"settings_and_connectors,omitempty"`
	SettingsSources       []LoadedItem `json:"settings_sources,omitempty"`
	Skills                []LoadedItem `json:"skills,omitempty"`
	Plugins               []LoadedItem `json:"plugins,omitempty"`
	Connectors            []LoadedItem `json:"connectors,omitempty"`
	Instructions          []LoadedItem `json:"instructions,omitempty"`
	Summary               string       `json:"summary,omitempty"`
}

// NewLoaded is an adapter's account of what it loaded, with its summary settled.
func NewLoaded(skills, plugins, instructions []LoadedItem) Loaded {
	loaded := Loaded{Reported: true, Skills: skills, Plugins: plugins, Instructions: instructions}
	loaded.Summary = loaded.describe()
	return loaded
}

// WithSettingsAndConnectors is the account with the settings sources the
// invocation read and the connectors it was given added, for an adapter whose
// provider has both.
func (l Loaded) WithSettingsAndConnectors(settings, connectors []LoadedItem) Loaded {
	l.SettingsAndConnectors, l.SettingsSources, l.Connectors = true, settings, connectors
	l.Summary = l.describe()
	return l
}

// Recorded is the account a record keeps: a copy with its summary settled,
// which for an adapter that did not account for it says so.
func (l Loaded) Recorded() *Loaded {
	l.Summary = l.describe()
	return &l
}

// String is the one-line account a record and an event carry.
func (l Loaded) String() string {
	if l.Summary != "" {
		return l.Summary
	}
	return l.describe()
}

func (l Loaded) describe() string {
	if !l.Reported {
		return "skills, plugins, and instruction files: not reported"
	}
	if l.SettingsAndConnectors {
		return "settings sources: " + describeLoaded(l.SettingsSources) +
			"; skills: " + describeLoaded(l.Skills) +
			"; plugins: " + describeLoaded(l.Plugins) +
			"; connectors: " + describeLoaded(l.Connectors) +
			"; instruction files: " + describeLoaded(l.Instructions)
	}
	return "skills: " + describeLoaded(l.Skills) +
		"; plugins: " + describeLoaded(l.Plugins) +
		"; instruction files: " + describeLoaded(l.Instructions)
}

func describeLoaded(items []LoadedItem) string {
	if len(items) == 0 {
		return "none"
	}
	described := make([]string, 0, len(items))
	for _, item := range items {
		entry := item.Name + " (" + item.Source
		if item.Path != "" {
			entry += ", " + item.Path
		}
		described = append(described, entry+")")
	}
	return strings.Join(described, ", ")
}

// Anchored is the named context with every relative path made absolute under
// root, the checkout the harness itself reads. A path starting "~" or already
// absolute is left as written. A root that is not absolute anchors nothing,
// and the adapter then refuses a relative path for any role that inspects a
// repository rather than read it from the one it is inspecting.
func (n NamedContext) Anchored(root string) NamedContext {
	if !filepath.IsAbs(root) {
		return n
	}
	anchor := func(files []ContextFile) []ContextFile {
		if files == nil {
			return nil
		}
		anchored := make([]ContextFile, len(files))
		for index, file := range files {
			path := strings.TrimSpace(file.Path)
			if path != "" && !strings.HasPrefix(path, "~") && !filepath.IsAbs(path) {
				file.Path = filepath.Join(root, filepath.FromSlash(path))
			}
			anchored[index] = file
		}
		return anchored
	}
	return NamedContext{Skills: anchor(n.Skills), Instructions: anchor(n.Instructions)}
}
