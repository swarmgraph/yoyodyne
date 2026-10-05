package config

// Naming how hard an agent's provider is asked to think, beside the model it is
// asked to think with.
//
// Before this key existed no agent set a level, the harness passed none, and
// every role ran at whatever its provider resolved for itself -- an environment
// variable, a level saved in the machine's settings, or the model's own default,
// which differs by model. None of that is written anywhere the harness reads, so
// nobody could say what level a role had run at. An agent now names one, the
// level is validated here against what the agent's provider accepts, and every
// invocation of the agent passes it and records it.
//
// The level is the agent's and not the model's. A failover alternate, a version
// fallback, a model execution.developer_models maps an item to, and a model a
// recurring task names are all served at the agent's level, because each of
// them moves which model answers and none of them is a decision about how hard
// it is asked to think. The one exception is a failover crossing onto a provider
// that would not accept the level: that turn is asked with none, and its record
// says so, rather than the crossing failing on a flag the provider refuses --
// which would cost the turn the failover exists to save.
//
// Omitted Codex effort is resolved to the model's advertised default and passed
// explicitly; Claude's existing resolution of an omitted effort is preserved.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// effortProblems reports an effort level the agent's configured model or
// version fallback would not accept. A provider the project
// does not name is reported elsewhere, and nothing is said about its levels.
func effortProblems(providers *backend.Registry, name string, agent AgentConfig) []string {
	level := strings.TrimSpace(agent.Effort)

	var problems []string
	if descriptor, known := providers.Lookup(agent.Backend); known {
		model := agent.Model
		if agent.ModelVersion != "" {
			model = agent.ModelVersion
		}
		models := []string{model}
		if descriptor.Adapter == domain.BackendCodex && agent.ModelVersion != "" && agent.Model != model {
			models = append(models, agent.Model)
		}
		// Version fallback keeps the initial invocation's effort, including an
		// explicit default resolved for the version rather than the family.
		level = descriptor.InvocationEffort(model, level)
		for _, selector := range models {
			narrowed := descriptor.ForModel(selector)
			if descriptor.Adapter == domain.BackendCodex && len(narrowed.EffortLevels) == 0 {
				problems = append(problems, fmt.Sprintf("agent %q uses Codex model %q whose effort levels and default are not established by this build; use a model in the codex-cli 0.159.2 bundled catalog", name, selector))
				if level != "" && !descriptor.AcceptsEffort(level) {
					problems = append(problems, effortRefusal(name, level, agent.Backend, descriptor))
				}
				continue
			}
			if level != "" && !narrowed.AcceptsEffort(level) {
				problem := effortRefusal(name, level, agent.Backend, narrowed)
				if len(models) > 1 {
					problem += fmt.Sprintf(" (model %q)", selector)
				}
				problems = append(problems, problem)
			}
		}
	}
	return problems
}

func effortRefusal(name, level string, provider domain.Backend, descriptor backend.Descriptor) string {
	if len(descriptor.EffortLevels) == 0 {
		return fmt.Sprintf("agent %q names effort %q, and provider %q accepts no effort level from this harness; leave effort out for this agent",
			name, level, provider)
	}
	return fmt.Sprintf("agent %q names effort %q, which provider %q does not accept; effort is one of %s",
		name, level, provider, descriptor.DescribeEffortLevels())
}

// AgentEffort is the effort level one configured agent's invocations ask for,
// including the explicit Codex default when its configuration names none.
func (c Config) AgentEffort(name string) string {
	agent := c.Agents[strings.TrimSpace(name)]
	return c.InvocationEffort(agent, agent.Model)
}

// InvocationEffort resolves the level before constructing or recording an
// invocation, including one whose task overrides the configured model.
func (c Config) InvocationEffort(agent AgentConfig, model string) string {
	if model == agent.Model && agent.ModelVersion != "" {
		model = agent.ModelVersion
	}
	providers, err := c.ProviderRegistry()
	if err == nil {
		if descriptor, ok := providers.Lookup(agent.Backend); ok {
			return descriptor.InvocationEffort(model, agent.Effort)
		}
	}
	return strings.TrimSpace(agent.Effort)
}

// Every configured Codex selector is checked before any role can be invoked,
// including models a mapping or a recurring task supplies to that role.
func (c Config) otherEffortProblems(providers *backend.Registry, name string, agent AgentConfig) []string {
	var problems []string
	check := func(label string, target AgentConfig) {
		if descriptor, ok := providers.Lookup(target.Backend); ok && descriptor.Adapter == domain.BackendCodex {
			problems = append(problems, effortProblems(providers, name+" ("+label+")", target)...)
		}
	}
	if agent.Failover.Enabled || agent.Failover.Effort != nil {
		alternate := agent
		alternate.Model, alternate.ModelVersion = agent.Failover.Model, ""
		if agent.Failover.Provider != "" {
			alternate.Backend = agent.Failover.Provider
		}
		if agent.Failover.Effort != nil {
			alternate.Effort = *agent.Failover.Effort
			problems = append(problems, effortProblems(providers, name+" (failover)", alternate)...)
		} else {
			check("failover", alternate)
		}
	}
	if agent.Role == domain.RoleDeveloper {
		for _, rule := range c.Execution.DeveloperModels {
			mapped := agent
			mapped.Model, mapped.ModelVersion = rule.Model, ""
			check("developer model mapping", mapped)
		}
	}
	for _, taskName := range c.RecurringTaskNames() {
		task := c.RecurringTasks[taskName]
		if task.Role == agent.Role && task.ModelSelector() != "" {
			mapped := agent
			mapped.Model, mapped.ModelVersion = task.ModelSelector(), ""
			check("recurring task "+taskName, mapped)
		}
	}
	return problems
}
