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
}

// Defined reports whether the block says anything at all.
func (t Triggers) Defined() bool {
	return t.Every != 0 || len(t.On) > 0
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
	if a.Triggers.Defined() {
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

// ProgramManagerPasses is every program manager instance its triggers wake,
// keyed by the agent's name: the instances a pull considers for a pass. An
// instance whose block says nothing is left out, because nothing wakes it — it
// reads, asks, and reports when somebody opens its conversation, and not
// otherwise.
func (c Config) ProgramManagerPasses() map[string]AgentConfig {
	passes := make(map[string]AgentConfig)
	for name, agent := range c.Agents {
		if agent.Role == domain.RoleProgramManager && agent.Triggers.Defined() {
			passes[name] = agent
		}
	}
	return passes
}

// passNameProblems refuses a recurring task named for a program manager
// instance its triggers wake. An instance's passes are recorded and paced under
// the agent's own name — it is what `yoyo sweeps --task` finds them by — so a
// task written under the same name would share one cadence and one record with
// it, and each would fire the other's schedule.
func passNameProblems(c Config) []string {
	var problems []string
	for _, name := range c.RecurringTaskNames() {
		agent, isAgent := c.Agents[name]
		if isAgent && agent.Role == domain.RoleProgramManager && agent.Triggers.Defined() {
			problems = append(problems, fmt.Sprintf("recurring task %q has the name of the program manager instance whose triggers wake it, and an instance's passes are recorded and paced under that name; name the task something else",
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
