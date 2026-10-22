package config

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Sub-agents let a developer run plan on its own model and hand bounded pieces
// of coding to sub-agents on a cheaper one, through the provider's own
// sub-agent tool (docs/designs/developer-sub-agents.md). They are off unless a
// project turns them on, and off is a developer invoked exactly as before.
//
// No value here grants anything. Which tools a sub-agent holds, where it runs,
// and whether a provider can give one at all are the adapter's to decide
// (internal/backend/subagents.go); the configuration chooses only whether a
// run uses them and which model and effort they run on.

// DefaultSubagentModel is the model sub-agents run on where sub-agents are on
// and nothing names one.
const DefaultSubagentModel = "sonnet"

// Subagents is an agent's sub-agent setting.
type Subagents struct {
	// Enabled is false, and absent from the effective configuration, unless a
	// layer turned sub-agents on.
	Enabled bool `yaml:"enabled" json:"enabled,omitempty"`
	// Model is the model sub-agents run on, on the same provider and account
	// as the endpoint serving the run. Empty is DefaultSubagentModel.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Effort is the effort sub-agents are asked for. Empty leaves the
	// provider's own default; one the provider cannot apply to a sub-agent is
	// refused when the configuration loads.
	Effort string `yaml:"effort,omitempty" json:"effort,omitempty"`
}

// Stated reports a block that says anything at all.
func (s Subagents) Stated() bool {
	return s != (Subagents{})
}

type subagentsDocument struct {
	Enabled *bool   `yaml:"enabled"`
	Model   *string `yaml:"model"`
	Effort  *string `yaml:"effort"`
}

// SubagentSetting is the sub-agent setting a developer run starts with,
// resolved from its slot and its agent, and recorded on the run before its
// first launch so a reload changes it only for runs that start after.
type SubagentSetting struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	// Origin says which configuration decided it: the slot, the agent, or
	// neither, which is off.
	Origin string `json:"origin"`
}

// DeveloperSubagents is the sub-agent setting for a developer run of the named
// agent in a slot, zero for a run in no slot. The slot's setting wins where it
// states one, then the agent's, then off. The model follows the same order,
// with DefaultSubagentModel where neither names one; the effort is the
// agent's.
func (c Config) DeveloperSubagents(name string, slot int) SubagentSetting {
	agent := c.Agents[name]
	setting := SubagentSetting{
		Enabled: agent.Subagents.Enabled,
		Model:   strings.TrimSpace(agent.Subagents.Model),
		Effort:  strings.TrimSpace(agent.Subagents.Effort),
		Origin:  "off: nothing configures sub-agents",
	}
	if agent.Subagents.Stated() {
		setting.Origin = fmt.Sprintf("agents.%s.subagents", name)
	}
	if slot >= 1 && slot <= len(c.Execution.DeveloperSlots) {
		if override := c.Execution.DeveloperSlots[slot-1].Subagents; override != nil {
			if override.Enabled != nil {
				setting.Enabled = *override.Enabled
				setting.Origin = fmt.Sprintf("execution.developer_slots slot %d.subagents", slot)
			}
			if model := strings.TrimSpace(override.Model); model != "" {
				setting.Model = model
			}
		}
	}
	if setting.Model == "" {
		setting.Model = DefaultSubagentModel
	}
	if !setting.Enabled {
		return SubagentSetting{Origin: setting.Origin}
	}
	return setting
}

// subagentProblems are what refuses an agent's or a slot's sub-agent setting
// when the configuration loads: sub-agents on any role but the developer, a
// model that is not a selector, an effort a provider that would be asked for
// it cannot apply to a sub-agent, and sub-agents turned on where no endpoint
// the run could use offers them, so the setting could never take effect.
func (c Config) subagentProblems(providers *backend.Registry) []string {
	var problems []string
	for _, name := range sortedNames(c.Agents) {
		agent := c.Agents[name]
		if !agent.Subagents.Stated() {
			continue
		}
		path := fmt.Sprintf("agents.%s.subagents", name)
		if agent.Role != domain.RoleDeveloper {
			problems = append(problems, fmt.Sprintf("%s: sub-agents are a developer's, and agent %q is a %s", path, name, agent.Role))
			continue
		}
		if model := strings.TrimSpace(agent.Subagents.Model); model != "" {
			if err := validateModelSelector(model); err != nil {
				problems = append(problems, fmt.Sprintf("%s.model: %s", path, err))
			}
		}
		if effort := strings.TrimSpace(agent.Subagents.Effort); effort != "" {
			for _, provider := range c.everySubagentProvider(providers, name) {
				descriptor, _ := providers.Lookup(provider)
				if descriptor.Subagents.Eligible() && !descriptor.Subagents.AcceptsEffort(effort) {
					problems = append(problems, fmt.Sprintf("%s.effort: %q is not an effort %s can apply to a sub-agent; it accepts %s",
						path, effort, provider, describeLevels(descriptor.Subagents.EffortLevels)))
				}
			}
		}
		if agent.Subagents.Enabled && !c.offersSubagents(providers, name, 0) {
			problems = append(problems, fmt.Sprintf("%s: sub-agents are on, and no provider this agent could run on offers them (%s), so the setting could never take effect",
				path, describeUnavailable(providers, c.subagentProviders(providers, name, 0))))
		}
	}
	name := c.developerAgentName()
	for index, slot := range c.Execution.DeveloperSlots {
		if slot.Subagents == nil {
			continue
		}
		path := fmt.Sprintf("execution.developer_slots slot %d.subagents", index+1)
		if model := strings.TrimSpace(slot.Subagents.Model); model != "" {
			if err := validateModelSelector(model); err != nil {
				problems = append(problems, fmt.Sprintf("%s.model: %s", path, err))
			}
		}
		if slot.Subagents.Enabled != nil && *slot.Subagents.Enabled && name != "" && !c.offersSubagents(providers, name, index+1) {
			problems = append(problems, fmt.Sprintf("%s: sub-agents are on, and no provider this slot could run on offers them (%s), so the setting could never take effect",
				path, describeUnavailable(providers, c.subagentProviders(providers, name, index+1))))
		}
	}
	return problems
}

// SubagentNotices say, once, every provider a developer with sub-agents on
// could run on that will serve its runs without them. They are not refusals:
// a slot pair that mixes providers keeps its fallback, and the run's record
// says it ran without sub-agents.
func (c Config) SubagentNotices() []string {
	providers, err := c.ProviderRegistry()
	if err != nil {
		return nil
	}
	name := c.developerAgentName()
	if name == "" {
		return nil
	}
	var notices []string
	for slot := 0; slot <= len(c.Execution.DeveloperSlots); slot++ {
		if !c.DeveloperSubagents(name, slot).Enabled {
			continue
		}
		where := fmt.Sprintf("developer agent %q", name)
		if slot > 0 {
			where = fmt.Sprintf("developer slot %d", slot)
		}
		for _, provider := range c.subagentProviders(providers, name, slot) {
			descriptor, _ := providers.Lookup(provider)
			if reason := descriptor.SubagentsUnavailable(); reason != "" {
				notices = append(notices, fmt.Sprintf("%s has sub-agents on, and its runs on %s go without them: %s", where, provider, reason))
			}
		}
	}
	return dedupe(notices)
}

// everySubagentProvider is every provider a developer run of the agent could
// be served on, in any slot or none.
func (c Config) everySubagentProvider(providers *backend.Registry, name string) []domain.Backend {
	seen := map[domain.Backend]bool{}
	var named []domain.Backend
	for slot := 0; slot <= len(c.Execution.DeveloperSlots); slot++ {
		for _, provider := range c.subagentProviders(providers, name, slot) {
			if !seen[provider] {
				seen[provider] = true
				named = append(named, provider)
			}
		}
	}
	return named
}

// offersSubagents reports whether any provider a run of the agent in the slot
// could use can give it sub-agents.
func (c Config) offersSubagents(providers *backend.Registry, name string, slot int) bool {
	for _, provider := range c.subagentProviders(providers, name, slot) {
		if descriptor, known := providers.Lookup(provider); known && descriptor.Subagents.Eligible() {
			return true
		}
	}
	return false
}

// subagentProviders are the providers a developer run of the agent in the
// slot could be served on: the slot's pair where it has one, and otherwise the
// agent's own and its failover's.
func (c Config) subagentProviders(providers *backend.Registry, name string, slot int) []domain.Backend {
	agent := c.Agents[name]
	seen := map[domain.Backend]bool{}
	var named []domain.Backend
	add := func(provider domain.Backend) {
		if provider = domain.Backend(strings.TrimSpace(string(provider))); provider != "" && !seen[provider] {
			seen[provider] = true
			named = append(named, provider)
		}
	}
	if slot >= 1 && slot <= len(c.Execution.DeveloperSlots) {
		if pair := c.Execution.DeveloperSlots[slot-1].Routing; pair != nil {
			for _, spec := range []*domain.EndpointSpec{pair.Primary, pair.Alternate} {
				if spec == nil {
					continue
				}
				if spec.Provider != "" {
					add(spec.Provider)
				} else {
					add(agent.Backend)
				}
			}
			return named
		}
	}
	add(agent.Backend)
	add(agent.Failover.Provider)
	return named
}

func (c Config) developerAgentName() string {
	for _, name := range sortedNames(c.Agents) {
		if c.Agents[name].Role == domain.RoleDeveloper {
			return name
		}
	}
	return ""
}

func describeUnavailable(providers *backend.Registry, named []domain.Backend) string {
	reasons := make([]string, 0, len(named))
	for _, provider := range named {
		reason := fmt.Sprintf("%s is not a provider this project has", provider)
		if descriptor, known := providers.Lookup(provider); known {
			reason = descriptor.SubagentsUnavailable()
		}
		reasons = append(reasons, reason)
	}
	return strings.Join(reasons, "; ")
}

func describeLevels(levels []string) string {
	if len(levels) == 0 {
		return "no level"
	}
	return strings.Join(levels, ", ")
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	kept := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			kept = append(kept, value)
		}
	}
	return kept
}
