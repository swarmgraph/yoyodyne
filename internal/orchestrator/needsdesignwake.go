package orchestrator

// Waking the architect when an item is labelled needs-design.
//
// "Waking the architect" in docs/designs/needs-design-gate.md is what this
// builds: setting the label wakes her conversation with the item named, as
// admissions and stoppages wake a program manager, and a pass once a day is the
// backstop for a wake that was lost. Nothing here wakes her more often than
// that on a schedule, because her pass opens on a large context and every
// pass is paid for whether or not anything was waiting.
//
// # What wakes her is the label, not who set it
//
// The wake reads which open items carry the label, as the tracker holds it, at
// every pull. So every path that adds the label wakes her alike — a
// conversation's label action, an item created carrying it, and a developer's
// raise that the harness labels on the run's behalf — and none of them has to
// remember to. An item she has been woken for is recorded once the pass that
// named it completes (runstate.NeedsDesignWakes), so an item labelled twice,
// or by two roles, wakes her once; an item that loses the label leaves the
// record, so labelling it again later wakes her again.
//
// # Queued while she is busy, never refused
//
// The wake is a firing of her conversation like any recurring pass, and is
// taken on the same path: a pass of hers already in flight in this session
// keeps it waiting for the next pull, and a turn another process is taking in
// her conversation is queued behind when the wake opens it. Until a pass that
// named an item completes, the item is still unwoken, so a wake that waited, or
// failed, is taken again rather than lost.
//
// # The backstop
//
// Once a day since her last pass of this kind, if any item still carries the
// label, she is woken with every one of them listed. A wake that was lost —
// the record written and the turn never reaching her, or a label added and
// removed between two reads — is caught there, and an item nobody has
// designed in a day is put in front of her again. Where no item carries the
// label the backstop costs nothing: no pass is taken.
//
// It is the needs-design wake's own cadence, under the name
// NeedsDesignWakeTask, and is independent of any recurring task the project
// configures for her.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// NeedsDesignWakeTask is the name the architect's needs-design passes are
// claimed, paced, and recorded under, which is what `yoyo sweeps --task` finds
// them by. Configuration reserves it, so no recurring task shares its cadence.
const NeedsDesignWakeTask = config.NeedsDesignWakeTask

// NeedsDesignBackstop is how long after her last needs-design pass the
// architect is woken again for every item still carrying the label.
const NeedsDesignBackstop = 24 * time.Hour

// NeedsDesignItem is one open work item carrying the needs-design label.
type NeedsDesignItem struct {
	ID    string
	Title string
	// Priority is the item's priority, and nil where the tracker does not say.
	Priority *int
}

// NeedsDesignItems reads the open work items carrying the needs-design label.
// It is satisfied in the CLI over the tracker's export.
type NeedsDesignItems interface {
	Labelled(ctx context.Context) ([]NeedsDesignItem, error)
}

// NeedsDesignWakeRecord is which labelled items the architect has been woken
// for. It is satisfied by *runstate.NeedsDesignWakeStore.
type NeedsDesignWakeRecord interface {
	Load() (runstate.NeedsDesignWakes, bool, error)
	Record(ctx context.Context, woken, labelled []string, at time.Time) (runstate.NeedsDesignWakes, error)
}

// needsDesignWakeWired reports a trigger that wakes the architect on the label.
func (t Trigger) needsDesignWakeWired() bool {
	return t.NeedsDesign != nil && t.NeedsDesignWakes != nil
}

// needsDesignWake considers the architect's needs-design pass at this pull, and
// finds it due where an item carries the label she has not been woken for, or
// where the backstop is due and any item carries it. An error is something that
// stopped the wake being considered, said beside the pull.
func (t Trigger) needsDesignWake(ctx context.Context, outage runstate.ProviderOutage, away bool) (passDue, error) {
	labelled, err := t.NeedsDesign.Labelled(ctx)
	if err != nil {
		return passDue{}, fmt.Errorf("read which work items carry the %s label, so the architect is not woken for them until it can be: %w", domain.NeedsDesignLabel, err)
	}
	record, _, err := t.NeedsDesignWakes.Load()
	if err != nil {
		return passDue{}, fmt.Errorf("read which %s items the architect has been woken for, so she is not woken until it can be: %w", domain.NeedsDesignLabel, err)
	}
	ids := make([]string, 0, len(labelled))
	for _, item := range labelled {
		ids = append(ids, item.ID)
	}
	now := t.now()
	var problems []error
	// An item that lost the label leaves the record now rather than at the next
	// pass, so one labelled again before then wakes her again.
	if stale := record.Stale(ids); len(stale) > 0 {
		write, stopWriting := recordContext(ctx)
		pruned, err := t.NeedsDesignWakes.Record(write, nil, ids, now)
		stopWriting()
		if err != nil {
			problems = append(problems, fmt.Errorf("drop %s from the architect's %s wakes now the label is off them: %w", strings.Join(stale, ", "), domain.NeedsDesignLabel, err))
		} else {
			record = pruned
		}
	}
	if len(labelled) == 0 {
		return passDue{}, errors.Join(problems...)
	}
	var fresh []NeedsDesignItem
	for _, item := range labelled {
		if !record.Has(item.ID) {
			fresh = append(fresh, item)
		}
	}

	// A provider answering nobody leaves the wake untaken, as it leaves a program
	// manager's: a wake made into the outage asks nothing, and the backstop's
	// claim is what records the wait.
	start := maystart.Pass(maystart.PromptedPass, domain.RoleArchitect)
	trigger := runstate.PassTriggerSchedule
	if len(fresh) > 0 && !away {
		last, fired, err := t.Claims.Find(NeedsDesignWakeTask)
		if err != nil {
			return passDue{}, errors.Join(append(problems, fmt.Errorf("read when the architect was last woken for the %s label: %w", domain.NeedsDesignLabel, err))...)
		}
		// Not sooner than the recurring minimum after her last such pass, for the
		// reason that minimum exists: every pass is a conversation turn, and a wake
		// that failed would otherwise be retried every pull.
		if !fired || !now.Before(last.FiredAt.Add(config.MinRecurringInterval)) {
			trigger = runstate.PassTriggerEvents
			due, err := t.labelDue(fresh)
			if err != nil {
				problems = append(problems, err)
			}
			start = start.Due("label", due)
		}
	}
	if trigger == runstate.PassTriggerSchedule {
		fell, owed, err := t.scheduleDue(NeedsDesignWakeTask, NeedsDesignBackstop)
		if err != nil {
			return passDue{}, errors.Join(append(problems, fmt.Errorf("claim the architect's daily pass over the %s label: %w", domain.NeedsDesignLabel, err))...)
		}
		if !owed {
			return passDue{}, errors.Join(problems...)
		}
		start = start.Due(scheduleSource(NeedsDesignWakeTask), fell)
	}
	return passDue{start: start, unasked: away, claim: func(ctx context.Context) (claimedFiring, bool, error) {
		return t.claimNeedsDesignWake(ctx, trigger, fresh, labelled, outage, away, now)
	}}, errors.Join(problems...)
}

// labelDue names the label that has waited longest: its item, and the change
// that added the label, known by when the item was first seen carrying it.
func (t Trigger) labelDue(fresh []NeedsDesignItem) (string, error) {
	var since map[string]time.Time
	var problem error
	if reader, readable := t.NeedsDesignLabelled.(NeedsDesignLabelledReader); readable {
		record, _, err := reader.Load()
		if err != nil {
			problem = fmt.Errorf("read since when each work item has carried the %s label: %w", domain.NeedsDesignLabel, err)
		} else {
			since = make(map[string]time.Time, len(fresh))
			for _, item := range fresh {
				since[item.ID], _ = record.Since(item.ID)
			}
		}
	}
	oldest := fresh[0].ID
	for _, item := range fresh[1:] {
		if at, first := since[item.ID], since[oldest]; at.Before(first) || (at.Equal(first) && item.ID < oldest) {
			oldest = item.ID
		}
	}
	if at := since[oldest]; !at.IsZero() {
		return oldest + "@" + at.UTC().Format(time.RFC3339Nano), problem
	}
	return oldest, problem
}

// claimNeedsDesignWake claims the pass found due, by the label or by the daily
// backstop.
func (t Trigger) claimNeedsDesignWake(ctx context.Context, trigger runstate.PassTrigger, fresh, labelled []NeedsDesignItem, outage runstate.ProviderOutage, away bool, now time.Time) (claimedFiring, bool, error) {
	var claimed runstate.SweepClaim
	var err error
	if trigger == runstate.PassTriggerEvents {
		if claimed, err = t.Claims.Summon(ctx, NeedsDesignWakeTask, now); err != nil {
			return claimedFiring{}, false, fmt.Errorf("claim the architect's pass for the %s label: %w", domain.NeedsDesignLabel, err)
		}
	} else {
		claimed, err = t.Claims.Claim(ctx, NeedsDesignWakeTask, NeedsDesignBackstop, now)
		if errors.Is(err, runstate.ErrSweepNotDue) {
			return claimedFiring{}, false, nil
		}
		if err != nil {
			return claimedFiring{}, false, fmt.Errorf("claim the architect's daily pass over the %s label: %w", domain.NeedsDesignLabel, err)
		}
	}
	ids := make([]string, 0, len(labelled))
	for _, item := range labelled {
		ids = append(ids, item.ID)
	}

	task := config.RecurringTask{
		Role:    domain.RoleArchitect,
		Every:   config.Duration(NeedsDesignBackstop),
		Enabled: true,
	}
	if away {
		fired := t.refuse(ctx, NeedsDesignWakeTask, task, outage)
		return claimedFiring{settled: &fired}, true, nil
	}
	return claimedFiring{take: func(ctx context.Context) Fired {
		addressed := t.addressedTo(task)
		return t.run(ctx, firing{
			name:      NeedsDesignWakeTask,
			pass:      passName(claimed),
			task:      task,
			trigger:   trigger,
			message:   needsDesignMessage(trigger, fresh, labelled, addressed),
			addressed: addressed,
			finish: func(answered bool) string {
				if !answered {
					return fmt.Sprintf("the pass did not complete, so the %d item(s) it named are not recorded as woken for, and the architect is woken for the unwoken ones again", len(labelled))
				}
				write, stopWriting := recordContext(ctx)
				defer stopWriting()
				if _, err := t.NeedsDesignWakes.Record(write, ids, ids, now); err != nil {
					return fmt.Sprintf("the items this pass named could not be recorded as woken for, so the architect is woken for them again: %v", err)
				}
				return ""
			},
		})
	}}, true, nil
}

// needsDesignMessage is what the harness says when it wakes the architect over
// the needs-design label: why she was woken, the items, newest to her first,
// and what lifting the label takes.
//
// A report addressed to her that names one of the items is shown under that
// item as well as in the section of reports addressed to her, because the item
// is where she decides: a report listed only at the foot of the message was
// read past by passes that each decided the item it warned about.
func needsDesignMessage(trigger runstate.PassTrigger, fresh, labelled []NeedsDesignItem, addressed *addressedReports) string {
	why := fmt.Sprintf("The harness woke you because the %s label was added to work items you have not been woken for.", domain.NeedsDesignLabel)
	if trigger != runstate.PassTriggerEvents {
		why = fmt.Sprintf("The harness woke you for your daily pass over the work items carrying the %s label, which catches any wake that was lost.", domain.NeedsDesignLabel)
	}
	lines := []string{
		why + " Nobody is waiting at a terminal for this: what you produce is recorded and read later.",
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra, and nothing about being woken by a label widens what you may decide or change.",
		"",
	}
	isFresh := make(map[string]bool, len(fresh))
	for _, item := range fresh {
		isFresh[item.ID] = true
	}
	var listed []NeedsDesignItem
	if len(fresh) > 0 {
		lines = append(lines, fmt.Sprintf("Newly labelled (%d):", len(fresh)))
		listed = append(listed, fresh...)
		lines = append(lines, needsDesignLines(fresh, maxPassEventsListed, addressed)...)
	}
	var waiting []NeedsDesignItem
	for _, item := range labelled {
		if !isFresh[item.ID] {
			waiting = append(waiting, item)
		}
	}
	if len(waiting) > 0 {
		if len(listed) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprintf("Still carrying the label from before (%d):", len(waiting)))
		lines = append(lines, needsDesignLines(waiting, maxPassEventsListed-min(len(listed), maxPassEventsListed), addressed)...)
	}
	lines = append(lines,
		"",
		"Each of these is held from every start the harness chooses until a document in docs/designs naming the item among the items it answers lands on the target branch (docs/designs/needs-design-gate.md). You cannot change the item, and nothing asks you to: the development manager removes the label, for now, once that document has landed. A ruling only in the item's notes, or a document written and not yet landed, leaves it on.",
		"Read each item in full, with the question its notes say the design must answer, and the documents it rests on. Take them by priority first, then age, and settle as many as this pass can do properly rather than touching all of them. For each, say plainly what you decided, and write the design as a document of yours in docs/designs, new or revised, that names the item in the write's `answers` list, which is what counts; naming it only in the reason does not.",
	)
	lines = append(lines, addressedLines(addressed)...)
	lines = append(lines, "", sweep.Contract())
	return strings.Join(lines, "\n")
}

// needsDesignLines lists items one a line, up to a bound, and says how many
// the bound left out. Each listed item is followed by the reports addressed to
// the role that name it.
func needsDesignLines(items []NeedsDesignItem, bound int, addressed *addressedReports) []string {
	var lines []string
	for index, item := range items {
		if index >= bound {
			lines = append(lines, fmt.Sprintf("- and %d more not listed here", len(items)-index))
			break
		}
		line := "- " + item.ID
		if item.Priority != nil {
			line += fmt.Sprintf(" (P%d)", *item.Priority)
		}
		if title := strings.Join(strings.Fields(item.Title), " "); title != "" {
			line += ": " + title
		}
		lines = append(lines, truncateLine(line, maxPassEventLineBytes))
		lines = append(lines, addressed.beside(item.ID)...)
	}
	return lines
}
