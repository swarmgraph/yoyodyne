package config

// A program manager instance: the lane it owns, the remit that says what the
// lane is for, and what wakes it.
//
// An instance is an agent filling the program-manager role, and the three keys
// here are what make one instance different from another — see "The role, and
// its instances" in docs/designs/program-manager.md. They are carried by that
// role alone: every other role's scope is its role, and a lane on a developer
// would be a scope nothing reads.
//
// None of them grants anything. The lane names the tracker label an instance's
// work is confined to, which narrows what the role holds rather than widening
// it; the remit is prose placed after the persona, under the contract, on every
// turn; and the triggers say when a pass is taken, not what it may do. That is
// the invariant configuration-never-grants-authority, and it is why there is no
// key here naming a capability.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// MaxRemitBytes bounds a remit at the bound a persona is held to: it is lane
// guidance, not a document to paste into every prompt.
const MaxRemitBytes = MaxPersonaBytes

// TriggerEvent is one kind of event that wakes a program manager instance for a
// pass over its lane. The set is closed: a pass is something the harness
// schedules, and an event it has no way to observe would be a trigger that
// never fires and reads as though it does.
type TriggerEvent string

const (
	// TriggerLandings wakes an instance when work in its lane lands.
	TriggerLandings TriggerEvent = "landings"
	// TriggerAdmissions wakes an instance when work is admitted to its lane.
	TriggerAdmissions TriggerEvent = "admissions"
	// TriggerStoppages wakes an instance when a run over its lane's work stops.
	TriggerStoppages TriggerEvent = "stoppages"
)

// TriggerEvents is the closed set, in the order a refusal names them.
var TriggerEvents = []TriggerEvent{TriggerLandings, TriggerAdmissions, TriggerStoppages}

// Valid reports whether the event is one of the closed set.
func (e TriggerEvent) Valid() bool {
	for _, known := range TriggerEvents {
		if e == known {
			return true
		}
	}
	return false
}

// Triggers is what wakes a program manager instance: a cadence, the events, or
// both. Reading them and taking the pass is the pass machinery's; this is the
// configuration it reads.
type Triggers struct {
	// Every is the cadence of the scheduled pass, floored at the recurring-task
	// minimum for the reason a recurring task is: every pass is a conversation
	// turn. Zero is no scheduled pass, which leaves the events alone to wake it.
	Every Duration `yaml:"every,omitempty" json:"every,omitempty"`
	// On is the events that wake the instance between scheduled passes.
	On []TriggerEvent `yaml:"on,omitempty" json:"on,omitempty"`
	// SkipWhenUnchanged and FullPassEvery are a recurring task's keys of the
	// same names (config.PassSkipping), applied to the instance's scheduled
	// pass. A pass its events wake is never skipped.
	SkipWhenUnchanged *bool    `yaml:"skip_when_unchanged,omitempty" json:"skip_when_unchanged,omitempty"`
	FullPassEvery     Duration `yaml:"full_pass_every,omitempty" json:"full_pass_every,omitempty"`
	// Model is the model selector the passes these triggers start ask for, where
	// the block names one: the scheduled pass and a pass its events wake. Empty
	// is the agent's own model. It names a model, or one of the kinds
	// execution.model_kinds names for the agent's provider ("stronger",
	// "cheaper", "lightest"), which ProgramManagerPasses resolves to that
	// provider's model. It is a recurring task's model key (RecurringTask.Model)
	// applied to the instance, and covers nothing else: a
	// pass somebody asked for, a message the operator sends, and every other
	// turn in the instance's conversation ask for the agent's own model. It
	// selects a model and nothing more; the account, the failover alternate, and
	// what the role may do are the agent's.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

// Defined reports whether the block says when a pass is taken. The two
// skipping keys and the model alone wake nothing.
func (t Triggers) Defined() bool {
	return t.Every != 0 || len(t.On) > 0
}

// ModelSelector is the model the passes these triggers start ask for, or empty
// where the block names none and the agent's own model applies.
func (t Triggers) ModelSelector() string {
	return strings.TrimSpace(t.Model)
}

// Skipping is whether the instance's scheduled passes are skipped when nothing
// is new, and its floor, with the defaults applied.
func (t Triggers) Skipping() PassSkipping {
	return passSkipping(t.SkipWhenUnchanged, t.FullPassEvery)
}

// AgentLane is the lane one configured agent owns, or empty for an agent that
// owns none.
func (c Config) AgentLane(name string) string {
	return strings.TrimSpace(c.Agents[name].Lane)
}

// instanceProblems reports what makes an agent's instance keys unusable: a key
// on an agent of a role that does not carry it, a lane the tracker would not
// carry, a remit that resolved to nothing or too much, and triggers outside the
// closed set or under the floor.
func instanceProblems(name string, agent AgentConfig) []string {
	var problems []string
	if agent.Role != domain.RoleProgramManager {
		// Named key by key, because each is its own mistake to take out, and the
		// role is named because it is what the operator would change instead.
		for _, key := range agent.instanceKeys() {
			problems = append(problems, fmt.Sprintf("agent %q sets %s, which only an agent of the %s role carries; its role is %q",
				name, key, domain.RoleProgramManager, agent.Role))
		}
		return problems
	}
	if agent.Lane != "" {
		if err := domain.ValidateLabel(agent.Lane); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q lane: %v", name, err))
		}
	}
	problems = append(problems, agent.Remit.problemsAs(name, "remit", MaxRemitBytes)...)
	problems = append(problems, agent.Triggers.problems(name)...)
	return problems
}

// instanceKeys names the instance keys an agent sets, in the order the design
// lists them.
func (a AgentConfig) instanceKeys() []string {
	var keys []string
	if strings.TrimSpace(a.Lane) != "" {
		keys = append(keys, "lane")
	}
	if a.Remit.Defined() {
		keys = append(keys, "remit")
	}
	if a.Triggers.Defined() || a.Triggers.SkipWhenUnchanged != nil || a.Triggers.FullPassEvery != 0 || a.Triggers.Model != "" {
		keys = append(keys, "triggers")
	}
	return keys
}

func (t Triggers) problems(name string) []string {
	var problems []string
	if t.Every != 0 && t.Every.Duration() < MinRecurringInterval {
		problems = append(problems, fmt.Sprintf("agent %q triggers.every is %s, and the shortest interval allowed is %s: every pass is a conversation turn, so a shorter interval is a bill rather than a schedule",
			name, t.Every, Duration(MinRecurringInterval)))
	}
	problems = append(problems, fullPassProblems(fmt.Sprintf("agent %q", name), "triggers.full_pass_every", t.FullPassEvery)...)
	// Held to the rule the agent's own model is held to. Whether the agent's
	// provider can serve it is triggerModelProblems's question.
	if t.Model != "" {
		if err := validateModelSelector(t.Model); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q triggers.model: %s", name, err))
		}
	}
	seen := make(map[TriggerEvent]struct{}, len(t.On))
	for _, event := range t.On {
		if !event.Valid() {
			problems = append(problems, fmt.Sprintf("agent %q triggers.on names %q, which is not an event the harness wakes an instance on; the events are %s",
				name, event, describeTriggerEvents()))
			continue
		}
		if _, repeated := seen[event]; repeated {
			problems = append(problems, fmt.Sprintf("agent %q triggers.on names %q twice", name, event))
		}
		seen[event] = struct{}{}
	}
	return problems
}

// triggerModel is the model a program manager's triggers ask for, with a kind
// resolved to the model the agent's provider names for it, and a refusal where
// the provider names none. A selector that is not a kind is the model itself.
func (c Config) triggerModel(name string, agent AgentConfig) (string, string) {
	selector := agent.Triggers.ModelSelector()
	switch kind := ModelKind(selector); kind {
	case ModelKindStronger, ModelKindCheaper, ModelKindLightest:
		model, named := c.Execution.ModelOfKind(agent.Backend, kind)
		if !named {
			return "", fmt.Sprintf("agent %q triggers.model is %q, and provider %s, which the agent runs on, names no %s model in execution.model_kinds",
				name, selector, agent.Backend, kind)
		}
		return model, ""
	}
	return selector, ""
}

// triggerModelProblems refuses a trigger model the agent's provider cannot
// serve: a kind the provider names no model for, and on Codex a model whose
// effort levels this build does not know or that would not accept the agent's
// effort — the check every other model a Codex agent may be asked on is held to
// (otherEffortProblems). A selector that is no model name at all is refused by
// Triggers.problems and not repeated here.
func (c Config) triggerModelProblems(providers *backend.Registry, name string, agent AgentConfig) []string {
	if agent.Role != domain.RoleProgramManager || agent.Triggers.Model == "" || validateModelSelector(agent.Triggers.Model) != nil {
		return nil
	}
	model, problem := c.triggerModel(name, agent)
	if problem != "" {
		return []string{problem}
	}
	descriptor, known := providers.Lookup(agent.Backend)
	if !known || descriptor.Adapter != domain.BackendCodex {
		return nil
	}
	mapped := agent
	mapped.Model, mapped.ModelVersion = model, ""
	return effortProblems(providers, name+" (triggers.model)", mapped)
}

// laneProblems refuses a lane two agents name. A lane has one owner or it is
// not a lane: two instances confined to one label would each be answerable for
// work the other changes. Both agents are named, because either is the one to
// move.
func laneProblems(agents map[string]AgentConfig) []string {
	owners := make(map[string][]string)
	for _, name := range sortedNames(agents) {
		agent := agents[name]
		lane := strings.TrimSpace(agent.Lane)
		if lane == "" || agent.Role != domain.RoleProgramManager {
			continue
		}
		owners[lane] = append(owners[lane], name)
	}
	lanes := make([]string, 0, len(owners))
	for lane := range owners {
		lanes = append(lanes, lane)
	}
	sort.Strings(lanes)
	var problems []string
	for _, lane := range lanes {
		if names := owners[lane]; len(names) > 1 {
			quoted := make([]string, 0, len(names))
			for _, name := range names {
				quoted = append(quoted, fmt.Sprintf("%q", name))
			}
			problems = append(problems, fmt.Sprintf("agents %s all name lane %q; a lane has one owner, so each program manager instance names a lane of its own",
				strings.Join(quoted, " and "), lane))
		}
	}
	return problems
}

// ProgramManagerPasses is every program manager instance, keyed by the agent's
// name: the instances a pull considers for a pass. One whose triggers say
// something is woken by them; every one, those included, may be asked for a
// pass by the operator or the Lead Product Manager, and one whose block says
// nothing is woken by that request and by nothing else ("Passes on request" in
// docs/designs/program-manager.md).
//
// A trigger model naming a kind is resolved here to the model the agent's
// provider names for it, so the passes ask for a model rather than a kind. A
// configuration that loaded has none left unresolved.
func (c Config) ProgramManagerPasses() map[string]AgentConfig {
	passes := make(map[string]AgentConfig)
	for name, agent := range c.Agents {
		if agent.Role == domain.RoleProgramManager {
			if model, problem := c.triggerModel(name, agent); problem == "" {
				agent.Triggers.Model = model
			}
			passes[name] = agent
		}
	}
	return passes
}

// NeedsDesignWakeTask is the name the architect's passes over the needs-design
// label are claimed, paced, and recorded under (internal/orchestrator,
// needsdesignwake.go). It is reserved here, where recurring task names are
// validated, so no configured task can share its cadence.
const NeedsDesignWakeTask = "architect-needs-design"

// passNameProblems refuses a recurring task named for a program manager
// instance. An instance's passes — on its triggers or on request — are recorded
// and paced under the agent's own name — it is what `yoyo sweeps --task` finds
// them by — so a task written under the same name would share one cadence and
// one record with it, and each would fire the other's passes.
//
// It refuses a task named NeedsDesignWakeTask for the same reason: the
// architect's wake over the needs-design label is paced and recorded under that
// name.
func passNameProblems(c Config) []string {
	var problems []string
	for _, name := range c.RecurringTaskNames() {
		if name == NeedsDesignWakeTask {
			problems = append(problems, fmt.Sprintf("recurring task %q has the name the architect's wake over the needs-design label is recorded and paced under; name the task something else",
				name))
		}
		agent, isAgent := c.Agents[name]
		if isAgent && agent.Role == domain.RoleProgramManager {
			problems = append(problems, fmt.Sprintf("recurring task %q has the name of a program manager instance, and an instance's passes are recorded and paced under that name; name the task something else",
				name))
		}
	}
	return problems
}

func describeTriggerEvents() string {
	names := make([]string, 0, len(TriggerEvents))
	for _, event := range TriggerEvents {
		names = append(names, fmt.Sprintf("%q", event))
	}
	return strings.Join(names, ", ")
}
