package backend

// How hard an agent's provider is asked to think on each invocation.
//
// Claude Code takes an effort level on its command line, and a session that is
// given none runs at whatever the provider resolves on its own: an environment
// variable, a level saved in the machine's settings, or the model's default,
// which differs by model. None of those is written down anywhere the harness
// reads, so a role that names no level runs at a level nobody chose and no
// record says. The configuration therefore names one beside the model, the
// adapter passes it on every invocation, and the record says what was asked.
//
// Accepted levels and defaults belong to the adapter descriptor, so validation,
// launch arguments, and durable records use the same policy. Codex's model
// catalog is established locally from codex-cli 0.159.2, without a provider call.

import (
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"strings"
)

// claudeCodeEffortLevels are the levels Claude Code's --effort flag accepts, as
// its own help states them: "Effort level for the current session (low, medium,
// high, xhigh, max)".
var claudeCodeEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// AcceptsEffort reports whether this provider accepts the named level.
func (d Descriptor) AcceptsEffort(level string) bool {
	level = strings.TrimSpace(level)
	for _, accepted := range d.EffortLevels {
		if accepted == level {
			return true
		}
	}
	return false
}

// DescribeEffortLevels names the levels this provider accepts the way a refusal
// says them: "low, medium, high, xhigh, or max", or that it accepts none.
func (d Descriptor) DescribeEffortLevels() string {
	switch len(d.EffortLevels) {
	case 0:
		return "no effort level"
	case 1:
		return d.EffortLevels[0]
	}
	return strings.Join(d.EffortLevels[:len(d.EffortLevels)-1], ", ") + ", or " + d.EffortLevels[len(d.EffortLevels)-1]
}

// ModelEffort is the provider's advertised levels and default for one model.
type ModelEffort struct {
	Default string
	Levels  []string
}

// codexModelEfforts was read from `codex debug models --bundled` on 0.159.2.
// Unlike Claude's levels, Codex effort strings are model-defined. The CLI's
// configuration parser accepts any nonempty string; the catalog is what proves
// which values the configured model supports.
var codexModelEfforts = map[string]ModelEffort{
	"gpt-6-astra":              {Default: "low", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-6.1-sol":              {Default: "low", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-6-sol":                {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-6-luna":               {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh", "max"}},
	"gpt-5.6-sol":              {Default: "low", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-5.6-terra":            {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-5.6-luna":             {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh", "max"}},
	"gpt-daybreak-blue-latest": {Default: "low", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-daybreak-red-latest":  {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
	"gpt-5.5":                  {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh"}},
	"codex-auto-review":        {Default: "medium", Levels: []string{"low", "medium", "high", "xhigh", "max"}},
}

// ForModel narrows the descriptor to the levels its model advertises. An
// unverified Codex selector has no accepted levels or established default.
func (d Descriptor) ForModel(model string) Descriptor {
	if d.Adapter != domain.BackendCodex {
		return d
	}
	if policy, ok := codexModelEfforts[strings.TrimSpace(model)]; ok {
		d.EffortLevels, d.DefaultEffort = policy.Levels, policy.Default
	} else {
		d.EffortLevels, d.DefaultEffort = nil, ""
	}
	return d
}

// InvocationEffort resolves an omitted effort explicitly where the provider
// has an established default. Claude's existing empty-effort behavior is kept.
func (d Descriptor) InvocationEffort(model, effort string) string {
	if level := strings.TrimSpace(effort); level != "" {
		return level
	}
	return d.ForModel(model).DefaultEffort
}
