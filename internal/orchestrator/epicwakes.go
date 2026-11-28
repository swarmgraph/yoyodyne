package orchestrator

// While epic mode is on, the Lead Product Manager's, the development manager's
// and the architect's passes are started by events of the named epics, and not
// by the clock (docs/designs/epic-mode-second-part.md, "When each role runs:
// piece 13"). The one decision refuses their scheduled passes then; this is the
// pass an event starts in their place, as a firing of the role's own task out of
// its cadence. internal/epicwake paces the events, and the read model says which
// are owed (readmodel.EpicWakeEvents).

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicwake"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// EpicWakes is what starts a role's pass from the events of the named epics. The
// harness wires it (cli's epicWakes).
type EpicWakes interface {
	// Owed reads the events owed now, and answers each role whose pass is due.
	Owed(ctx context.Context) (EpicWakeDue, error)
	// Woken records the pass before its first turn, against the events it is for.
	Woken(role domain.AgentRole, pass string, events []epicwake.Kept) error
	// Ended records what the pass cost once its turns are over.
	Ended(pass string, fired Fired) error
}

// EpicWakeDue is what the events owe. On is whether epic mode is on; nothing is
// due while it is off.
type EpicWakeDue struct {
	On  bool
	Due map[domain.AgentRole]epicwake.Due
}

// epicWakeSource names the events among the things that can start a pass.
const epicWakeSource = "epic-events"

// maxEpicEventsListed and maxEpicEventBytes bound what one pass is told.
const (
	maxEpicEventsListed = 20
	maxEpicEventBytes   = 400
)

// epicWakeCandidates is a firing for each role an event owes a pass, and whether
// epic mode is on. A role with no enabled task of its own has no conversation a
// pass could be taken in, so the event waits and is said.
func (t Trigger) epicWakeCandidates(ctx context.Context, away bool) ([]candidate, bool, error) {
	if t.EpicWakes == nil {
		return nil, false, nil
	}
	owed, err := t.EpicWakes.Owed(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("read which events of the named epics owe a role's pass: %w", err)
	}
	var candidates []candidate
	var problems []string
	for _, role := range epicwake.Roles {
		due, owes := owed.Due[role]
		if !owes || len(due.Events) == 0 {
			continue
		}
		name, task, found := t.taskOf(role)
		if !found {
			problems = append(problems, fmt.Sprintf("an event of a named epic owes the %s a pass, and no enabled recurring task wakes that role", role.Title()))
			continue
		}
		candidates = append(candidates, candidate{
			name: name, role: role, conversation: roleConversationKey(role), kind: firingTask,
			due: due.At, known: true,
			find: func(context.Context) (passDue, error) {
				// A provider answering nobody leaves the events owed, and nothing is
				// claimed, so the pass goes the first pull it answers.
				if away {
					return passDue{}, nil
				}
				return passDue{
					start: maystart.Pass(maystart.PromptedPass, role).Due(epicWakeSource, due.Events[0].Key),
					claim: func(ctx context.Context) (claimedFiring, bool, error) {
						return t.wakeForEpics(ctx, name, task, due)
					},
				}, nil
			},
		})
	}
	if len(problems) > 0 {
		return candidates, owed.On, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return candidates, owed.On, nil
}

// taskOf is the first enabled task, in name order, that wakes the role.
func (t Trigger) taskOf(role domain.AgentRole) (string, config.RecurringTask, bool) {
	for _, name := range t.names() {
		if task := t.Tasks[name]; task.Enabled && task.Role == role {
			return name, task, true
		}
	}
	return "", config.RecurringTask{}, false
}

// wakeForEpics claims the role's task out of its cadence, records the pass
// against its events before its first turn, and hands back the turns.
func (t Trigger) wakeForEpics(ctx context.Context, name string, task config.RecurringTask, due epicwake.Due) (claimedFiring, bool, error) {
	claimed, err := t.Claims.Summon(ctx, name, t.now())
	if err != nil {
		return claimedFiring{}, false, fmt.Errorf("claim the firing of the recurring task %s for an event of a named epic: %w", name, err)
	}
	pass := passName(claimed)
	if err := t.EpicWakes.Woken(task.Role, pass, due.Events); err != nil {
		return claimedFiring{}, false, fmt.Errorf("the pass of %s for an event of a named epic was not started, because it could not be written down first: %w", name, err)
	}
	return claimedFiring{take: func(ctx context.Context) Fired {
		addressed := t.addressedTo(task)
		batch := t.amendmentBatch(task)
		fired := t.run(ctx, firing{
			name: name, pass: pass, task: task, trigger: runstate.PassTriggerSummons,
			message:  epicWakeMessage(name, task, due.Events, t.overdueFor(task), addressed, batch),
			summoned: boundedProblem([]string{fmt.Sprintf("%d event(s) of a named epic, first %s", len(due.Events), due.Events[0].Key)}),
			batch:    batch, addressed: addressed, due: due.At,
		})
		if err := t.EpicWakes.Ended(pass, fired); err != nil {
			fired.Problem = appendProblem(fired.Problem, fmt.Sprintf("what the pass cost could not be written against its epics: %v", err))
		}
		return fired
	}}, true, nil
}

// epicWakeMessage is what a pass an event started is told: which events, and that
// it is not on a schedule.
func epicWakeMessage(name string, task config.RecurringTask, events []epicwake.Kept, overdue string, addressed *addressedReports, batch amendmentBatch) string {
	lines := []string{
		fmt.Sprintf("The harness woke you for the recurring task %q because something in an epic the operator named waits on your role. While epic mode is on your passes start this way and not by the clock. Nobody is waiting at a terminal for this: what you produce is recorded and read later.", name),
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra.",
		"",
		"What waits on you:",
	}
	for i, event := range events {
		if i == maxEpicEventsListed {
			lines = append(lines, fmt.Sprintf("- and %d more, which status names", len(events)-i))
			break
		}
		said := singleLine(event.Says, maxEpicEventBytes)
		if event.From != "" {
			said += fmt.Sprintf(" (it waited on the %s through %d passes that did not settle it, so it is yours now)", event.From.Title(), epicwake.PassesBeforeMoving)
		} else if len(event.Woken) > 0 {
			said += fmt.Sprintf(" (a pass of yours at %s ended without settling it)", runstate.LocalStamp(event.Woken[len(event.Woken)-1]))
		}
		lines = append(lines, "- "+said)
	}
	lines = append(lines,
		"",
		fmt.Sprintf("Settle what you can: each comes back to you an hour after a pass that leaves it as it is, and after %d such passes it goes to the next role up, or from the Lead Product Manager to status as a problem.", epicwake.PassesBeforeMoving),
	)
	lines = append(lines, overdueLines(overdue)...)
	lines = append(lines, addressedLines(addressed)...)
	lines = append(lines,
		"",
		batch.section(task.Role)+strings.TrimSpace(task.Prompt),
		"",
		sweep.Contract()+batch.contract(),
	)
	return strings.Join(lines, "\n")
}

// leftToTheirRoles drops, while epic mode is on, each critical report that waits
// on the development manager or the architect: an event of its own starts that
// role's pass, so delivering it to the Lead Product Manager as well would start a
// second.
func leftToTheirRoles(pending []report.Report, on bool) []report.Report {
	if !on {
		return pending
	}
	var kept []report.Report
	for _, reported := range pending {
		if addressee := reported.Addressee(); addressee == domain.RoleDevelopmentManager || addressee == domain.RoleArchitect {
			continue
		}
		kept = append(kept, reported)
	}
	return kept
}

// EpicWakeKnown is whether what a pass cost is known: a pass that took no turn
// cost nothing, and one that did and reported no cost is not known.
func EpicWakeKnown(fired Fired) bool { return fired.Turns == 0 || fired.CostUSD > 0 }
