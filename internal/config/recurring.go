package config

// Work the harness does on a cadence rather than because something happened.
//
// Everything else the harness starts is reactive: an item is admitted, a run
// stops, an operator asks. A recurring task is the other shape — a role woken
// every so often to look at its own domain and deal with what it finds — and it
// is configuration because what runs and how often is a project's judgement,
// changed by editing a file rather than by a release.
//
// What configuration decides here is which role is woken, when, and — where the
// task names one — which model its turns ask for. It cannot decide what that
// role may then do. There is no key for a
// capability, a tool, an account, or an authority of any kind, and that is not
// an omission to be filled in later: a role woken by a task acts under the
// authority its role already holds, resolved from the harness's own registry the
// same way it is resolved for a conversation an operator opens by hand. A
// configuration file that could widen it would make every project file an
// escalation path, which is the constraint the whole configurable-workflows
// epic is held to. The loader is strict about unknown fields, so a key invented
// in a project file fails the configuration rather than being ignored — and a
// test in this package asserts exactly that for the authority-shaped names
// somebody would reach for first.
//
// The prompt is the one thing here that is prose, and it is the task rather than
// a persona: the role's own persona is read on this turn exactly as it is on
// every other, because a scheduled wakeup that read a different personality
// would be a second version of a role nobody configured.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DefaultMissingReportsBeforeFreshConversation is the bound used when a project
// names none, or supplies zero.
const DefaultMissingReportsBeforeFreshConversation = 3

// MissingReportLimit applies the default to configurations assembled in code too.
func (e Execution) MissingReportLimit() int {
	if e.MissingReportsBeforeFreshConversation <= 0 {
		return DefaultMissingReportsBeforeFreshConversation
	}
	return e.MissingReportsBeforeFreshConversation
}

// The bounds a recurring task is held to.
//
// The minimum cadence is what keeps a typo from becoming a provider bill: every
// firing is a conversation turn, and "1m" in the place "1h" was meant is sixty
// times the spend with nothing to warn about it. Five minutes is well below any
// cadence a sweep of a domain is worth running at and well above the accident.
//
// The turn bound is the other half of the same discipline. A pass with more to
// do than one turn holds says so and is given another, up to this many, so a
// heavy pass iterates instead of trying to fit a morning's work inside bounds
// that will not hold it — and a task whose role says "more" forever stops at a
// number the operator can see rather than spending the session.
const (
	MinRecurringInterval  = 5 * time.Minute
	DefaultRecurringTurns = 3
	MaxRecurringTurns     = 10
	MaxRecurringPromptLen = 16 << 10
)

// RecurringTask is one thing the harness does on a cadence: a role, an interval,
// and what to say to it.
type RecurringTask struct {
	// Role is who is woken. It must be a role this project configures an agent
	// for, because a task naming a role nobody fills is one that can never fire
	// and would otherwise be discovered as silence.
	Role domain.AgentRole `yaml:"role" json:"role"`
	// Every is how often the task fires. It is measured from the last firing
	// rather than from a wall-clock grid: a harness that was not running at the
	// top of the hour fires once when it comes back rather than firing for every
	// hour it missed.
	Every Duration `yaml:"every" json:"every"`
	// Enabled is the switch. It is explicit rather than implied by the task's
	// presence so a project can keep a task's prompt and cadence written down
	// while it is turned off, which is what makes turning one off for a week a
	// one-word edit rather than a deletion somebody has to reconstruct.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Prompt is what the role is told when it is woken. It is the task and not a
	// persona: what the role is is read from its persona on this turn as on every
	// other.
	Prompt string `yaml:"prompt" json:"prompt"`
	// MaxTurns bounds how many turns one firing may take. Zero means
	// DefaultRecurringTurns, so a task that does not think about it still
	// iterates rather than truncating.
	MaxTurns int `yaml:"max_turns,omitempty" json:"max_turns,omitempty"`
	// Model is the model selector this task's turns ask for, where the task
	// names one. Empty is the role's own configured model, which is what every
	// firing asked for before the key existed.
	//
	// It is spend following the work rather than the role: a routine pass over a
	// domain and a decision in the same conversation are both the role's turns,
	// and only the decision needs the role's model. It covers this task's turns
	// and nothing else — a message the operator sends, a docket decision, and a
	// directive in the same conversation still ask for the role's model — and it
	// selects a model and nothing more. The account the turn is served under, the
	// failover alternate, and what the role may do are the agent's, unchanged.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

// ModelSelector is the model this task's turns ask for, or empty where the task
// names none and the role's configured model applies.
func (t RecurringTask) ModelSelector() string {
	return strings.TrimSpace(t.Model)
}

// Turns is the turn bound in force for this task, with the default applied.
func (t RecurringTask) Turns() int {
	if t.MaxTurns <= 0 {
		return DefaultRecurringTurns
	}
	return t.MaxTurns
}

// RecurringTaskNames lists the configured tasks in a stable order, so what the
// harness fires first is decided by the name rather than by map iteration.
func (c Config) RecurringTaskNames() []string {
	names := make([]string, 0, len(c.RecurringTasks))
	for name := range c.RecurringTasks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// problems reports every contract violation in one task at once, named by the
// key it was written under.
func (t RecurringTask) problems(name string, agents map[string]AgentConfig) []string {
	var problems []string
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		problems = append(problems, err.Error())
	}
	// The maintenance pass records its passes in the sweep log under its own
	// name and claims its cadence there, so a task written under that name
	// would fire on the pass's claim and the pass on the task's.
	if name == MaintenanceTaskName {
		problems = append(problems, fmt.Sprintf("recurring task %q is named for the product's maintenance pass, which records its passes under that name; give the task another", name))
	}
	if !t.Role.Valid() {
		problems = append(problems, fmt.Sprintf("recurring task %q role %q must be one of %s", name, t.Role, describeRoles()))
	} else if !roleIsConfigured(agents, t.Role) {
		problems = append(problems, fmt.Sprintf("recurring task %q wakes the %s, and no %s agent is configured, so it could never fire", name, t.Role, t.Role))
	}
	if t.Every.Duration() < MinRecurringInterval {
		problems = append(problems, fmt.Sprintf("recurring task %q every is %s, and the shortest cadence is %s: every firing is a conversation turn, so a cadence below that is a bill rather than a schedule",
			name, t.Every, Duration(MinRecurringInterval)))
	}
	switch prompt := strings.TrimSpace(t.Prompt); {
	case prompt == "":
		problems = append(problems, fmt.Sprintf("recurring task %q prompt is required: a task with nothing to say wakes a role for nothing", name))
	case len(prompt) > MaxRecurringPromptLen:
		problems = append(problems, fmt.Sprintf("recurring task %q prompt is %d bytes, limit is %d", name, len(prompt), MaxRecurringPromptLen))
	}
	if t.MaxTurns < 0 {
		problems = append(problems, fmt.Sprintf("recurring task %q max_turns cannot be negative", name))
	}
	if t.MaxTurns > MaxRecurringTurns {
		problems = append(problems, fmt.Sprintf("recurring task %q max_turns is %d, limit is %d", name, t.MaxTurns, MaxRecurringTurns))
	}
	// Held to the rule an agent's own model is held to, so a selector refused
	// there is refused here with the same reason. Leaving it out is the role's
	// model; writing it empty is the same thing and is not refused.
	if t.Model != "" {
		if err := validateModelSelector(t.Model); err != nil {
			problems = append(problems, fmt.Sprintf("recurring task %q %s", name, err))
		}
	}
	return problems
}

func roleIsConfigured(agents map[string]AgentConfig, role domain.AgentRole) bool {
	for _, agent := range agents {
		if agent.Role == role {
			return true
		}
	}
	return false
}

// validateRecurringTasks checks every configured task, in name order so a file
// with two broken tasks reports them the same way twice.
func validateRecurringTasks(c Config) []string {
	var problems []string
	for _, name := range c.RecurringTaskNames() {
		problems = append(problems, c.RecurringTasks[name].problems(name, c.Agents)...)
	}
	return problems
}

// ErrNoRecurringTask names a task nobody configured, so a caller asking for one
// by name can tell it apart from a configuration that would not load.
var ErrNoRecurringTask = errors.New("no recurring task by that name is configured")

// RecurringTaskNamed is one configured task, looked up by the name it was
// written under.
func (c Config) RecurringTaskNamed(name string) (RecurringTask, error) {
	task, configured := c.RecurringTasks[strings.TrimSpace(name)]
	if !configured {
		return RecurringTask{}, fmt.Errorf("%w: %q; the configured tasks are %s", ErrNoRecurringTask, name, namedRecurringTasks(c))
	}
	return task, nil
}

func namedRecurringTasks(c Config) string {
	names := c.RecurringTaskNames()
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
