package orchestrator

// A critical report is the severity that means somebody has to act, and until
// this existed nothing held anybody to it. The pile's delivery put a critical
// ahead of the walk — but only into whichever turn of the Lead Product
// Manager's came next, and a turn that was shown one could end its pass
// complete without deciding anything about it. On 2026-09-27 a program
// manager's critical, "Nothing is landing on the protected main", was shown at
// the head of the 03:40 sweep's slice and passed over while the sweep handled
// others; it waited more than eight hours.
//
// Three things close that, and all three are here because all three are the
// harness holding a role to what it was shown rather than the role's own
// judgement:
//
//   - A critical nobody has put in front of her is delivered as a turn of its
//     own on the next pull, as a firing of her report task out of its cadence,
//     ahead of anything her own cadence has due. The pull is the harness's one
//     place for invoking a role, so "the moment it is filed" is the next pull
//     after it — a minute on a watching session — rather than her next pass.
//     It takes her conversation and no other role's: a missed pass reported at
//     critical once woke her into the one firing the starved roles were
//     waiting for, so the report of a starved pass starved them again.
//   - A pass whose account says complete while a critical it was shown stands
//     unhandled is refused as complete: it is marked as having more to do, and
//     the next turn is asked for those reports by name.
//   - A warning or note from a program manager that has stood unhandled through
//     two of her passes is named on the next pass's message as overdue. A
//     program manager is the role whose reports are about a whole lane, and it
//     was its reports that sat.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// RecurringPile is the collected reports and what became of them, as a firing
// reads them. It is satisfied by *runstate.ReportStore.
type RecurringPile interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
}

// maxCriticalsPerFiring and maxCriticalMessageBytes bound one delivery. The
// message a firing sends is held to the bound on what a person may type, and a
// report's message can be four kilobytes, so what fits is decided by size as
// well as by count; the first always goes, and the rest are delivered on the
// next pull.
const (
	maxCriticalsPerFiring   = 5
	maxCriticalMessageBytes = 12 << 10
)

// overduePasses is how many of her passes a program manager's warning or note
// may stand unhandled through before the next one names it as overdue, and
// maxOverdueListed how many one message names.
const (
	overduePasses    = 2
	maxOverdueListed = 10
	overdueTextBytes = 240
)

// productManagerTask is the first enabled task, in name order, that wakes the
// Lead Product Manager: the one a critical report is delivered through.
func (t Trigger) productManagerTask() (string, config.RecurringTask, bool) {
	for _, name := range t.names() {
		task := t.Tasks[name]
		if task.Enabled && task.Role == domain.RoleProductManager {
			return name, task, true
		}
	}
	return "", config.RecurringTask{}, false
}

// criticalsWaiting is the Lead Product Manager's task a critical report is
// delivered through, and every critical nobody has yet put in front of her,
// oldest first. It claims nothing: whether and when the delivery is made is the
// caller's, which orders it against the other firings due by how long each has
// waited.
func (t Trigger) criticalsWaiting() (string, config.RecurringTask, []report.Report, error) {
	if t.Pile == nil {
		return "", config.RecurringTask{}, nil, nil
	}
	name, task, found := t.productManagerTask()
	if !found {
		return "", config.RecurringTask{}, nil, nil
	}
	pending, err := t.undeliveredCriticals()
	if err != nil {
		return "", config.RecurringTask{}, nil, err
	}
	return name, task, pending, nil
}

// deliverCriticals claims a firing of the Lead Product Manager's task now, with
// the critical reports nobody has yet put in front of her as a turn of its own,
// and hands back the turns to take. It is a firing of her conversation and of
// nobody else's, so it never takes a due firing from another role: firings of
// different roles are taken side by side, and where the bound on them is reached
// the delivery waits its turn by the time its oldest report was filed, like any
// other firing.
//
// What has been delivered is read from the sweep log, where each such firing
// records the reports it carried, for the reason the forge's requests are: the
// record of what was said is what decides what has been. A firing whose turn
// then failed has still delivered, so a provider that refuses the turn is not
// asked again every pull; what keeps the critical in front of her from then on
// is her next pass carrying it and refusing to end complete over it.
func (t Trigger) deliverCriticals(ctx context.Context, name string, task config.RecurringTask, pending []report.Report, due time.Time) (claimedFiring, error) {
	carried := fitCriticals(pending)
	claimed, err := t.Claims.Summon(ctx, name, t.now())
	if err != nil {
		return claimedFiring{}, fmt.Errorf("claim the firing of the recurring task %s for a critical report: %w", name, err)
	}
	ids := make([]string, 0, len(carried))
	for _, reported := range carried {
		ids = append(ids, reported.ID)
	}
	return claimedFiring{take: func(ctx context.Context) Fired {
		return t.run(ctx, firing{
			name:      name,
			pass:      passName(claimed),
			task:      task,
			trigger:   runstate.PassTriggerSummons,
			message:   criticalMessage(name, task, carried, len(pending)-len(carried), t.overdueFor(task)),
			summoned:  boundedProblem([]string{"a critical report, " + strings.Join(ids, ", ")}),
			criticals: ids,
			due:       due,
		})
	}}, nil
}

// undeliveredCriticals is every unhandled critical report no firing has yet
// carried, oldest first. The Lead Product Manager's own are left out: she filed
// them in the conversation this would deliver them into.
func (t Trigger) undeliveredCriticals() ([]report.Report, error) {
	reports, err := t.Pile.List()
	if err != nil {
		return nil, fmt.Errorf("read the collected reports to deliver the critical ones: %w", err)
	}
	var criticals []report.Report
	for _, reported := range reports {
		if reported.Severity == report.SeverityCritical && reported.Role != domain.RoleProductManager {
			criticals = append(criticals, reported)
		}
	}
	if len(criticals) == 0 {
		return nil, nil
	}
	handlings, err := t.Pile.Handlings()
	if err != nil {
		return nil, fmt.Errorf("read what became of the collected reports to deliver the critical ones: %w", err)
	}
	recorded, _, err := t.Reports.List()
	if err != nil {
		// Without the earlier firings there is no saying which criticals were
		// already delivered, and delivering them all again every pull is the thing
		// this must not do; the delivery waits for a pull that can read the log.
		return nil, fmt.Errorf("the critical reports were not delivered because the earlier passes' records could not be read: %w", err)
	}
	delivered := map[string]bool{}
	for _, entry := range recorded {
		for _, id := range entry.Criticals {
			delivered[id] = true
		}
	}
	var pending []report.Report
	for _, reported := range report.ByFiling(report.Unhandled(criticals, handlings)) {
		if !delivered[reported.ID] {
			pending = append(pending, reported)
		}
	}
	return pending, nil
}

// fitCriticals is what of the pending criticals one message carries.
func fitCriticals(pending []report.Report) []report.Report {
	var carried []report.Report
	size := 0
	for _, reported := range pending {
		if len(carried) == maxCriticalsPerFiring {
			break
		}
		rendered := len(reported.Render())
		if len(carried) > 0 && size+rendered > maxCriticalMessageBytes {
			break
		}
		carried = append(carried, reported)
		size += rendered
	}
	return carried
}

// standingCriticals is which of the critical reports a pass was shown still
// stand unhandled, sorted, and what stopped the reading where something did. A
// pile that cannot be read takes the pass's account as given and says so:
// refusing a pass over a file nobody could open would hold the role to nothing
// it could see.
func (t Trigger) standingCriticals(shown map[string]bool) ([]string, string) {
	if len(shown) == 0 || t.Pile == nil {
		return nil, ""
	}
	handlings, err := t.Pile.Handlings()
	if err != nil {
		return nil, fmt.Sprintf("whether the critical reports the pass was shown are handled could not be read, so its account was taken as given: %v", err)
	}
	handled := report.Handled(handlings)
	var standing []string
	for id := range shown {
		if _, done := handled[id]; !done {
			standing = append(standing, id)
		}
	}
	sort.Strings(standing)
	return standing, ""
}

// criticalMessage is what the harness says when it wakes the Lead Product
// Manager for a critical report. It is her ordinary pass with the reports in
// front of it, for the reason a summons is the development manager's ordinary
// pass with the trip in front: what she has to look at arrives with the
// message, and the rest of her pass is the one she already knows.
func criticalMessage(name string, task config.RecurringTask, carried []report.Report, behind int, overdue string) string {
	lines := []string{
		fmt.Sprintf("A critical report was filed, and the harness woke you for it now, ahead of the cadence of %q and ahead of the rest of the pile. Nobody is waiting at a terminal for this: what you produce is recorded and read later.", name),
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra.",
		"A critical report is something already wrong that will cost somebody if nobody looks at it. Decide what becomes of each one below on this pass and record it with the \"handle\" action: work to admit, a proposal to make, a concern to raise, the operator's hand where only a person can act, or that it needs nothing because it has already resolved. This pass is not accepted as complete while any of them stands unhandled.",
		"Before you file anything, check it against the work already admitted, and check whether a fix has landed since the build that filed it.",
		"",
		"The critical report(s):",
		"",
	}
	for _, reported := range carried {
		lines = append(lines, strings.TrimRight(reported.Render(), "\n"))
	}
	if behind > 0 {
		lines = append(lines, "", fmt.Sprintf("%d further critical report(s) are waiting behind these and are delivered on the harness's next pass over the schedule.", behind))
	}
	lines = append(lines, overdueLines(overdue)...)
	lines = append(lines,
		"",
		strings.TrimSpace(task.Prompt),
		"",
		sweep.Contract(),
	)
	return strings.Join(lines, "\n")
}

// criticalsOutstandingMessage is what a pass refused as complete is given next.
// It names the reports, because they are the whole of why the pass goes on.
func criticalsOutstandingMessage(name string, standing []string) string {
	return strings.Join([]string{
		fmt.Sprintf("You said the pass of %q was complete, and the harness refused that: the critical report(s) %s you were shown on this pass stand unhandled.", name, strings.Join(standing, ", ")),
		"Decide what becomes of each and record it with the \"handle\" action. A critical that needs nothing — already resolved, or already covered — is handled by saying so; one only a person can act on is handled with \"needs\": \"operator\".",
		"Report only what this turn found: what you already reported is kept, and repeating it would be counted twice.",
		"",
		sweep.Contract(),
	}, "\n")
}

// overdueFor is the program managers' warnings and notes that have stood
// unhandled through at least overduePasses of the Lead Product Manager's
// passes, rendered for her next one, and nothing for any other role or where
// nothing is overdue. A pile or a log that cannot be read is said in the
// section rather than left out, because a pass told nothing is overdue when
// nothing could be read would conclude nothing was.
func (t Trigger) overdueFor(task config.RecurringTask) string {
	if t.Pile == nil || task.Role != domain.RoleProductManager {
		return ""
	}
	overdue, passes, err := t.overdueReports()
	if err != nil {
		return "Whether any program manager's report is overdue could not be read on this pass: " + err.Error()
	}
	if len(overdue) == 0 {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "These warnings and notes from a program manager have stood unhandled through %d or more of your passes. Decide about each on this pass: a report that asks for nothing is handled by saying so.\n\n", overduePasses)
	fmt.Fprintf(&rendered, "Each preview is limited to %d bytes after whitespace is joined. A cut names the full message's size; read the whole report, or one it cites, with {\"action\":\"read\",\"report\":\"report-id\"} in your yoyodyne-tracker block.\n\n", overdueTextBytes)
	listed := overdue
	if len(listed) > maxOverdueListed {
		listed = listed[:maxOverdueListed]
	}
	for _, reported := range listed {
		reporter := string(reported.Role)
		if reported.Agent != "" {
			reporter = reported.Agent
		}
		on := ""
		if reported.WorkItemID != "" {
			on = " on " + reported.WorkItemID
		}
		text := strings.Join(strings.Fields(reported.Message), " ")
		preview := clip(text, overdueTextBytes)
		if len(text) > overdueTextBytes {
			preview += fmt.Sprintf(" [message cut; full message is %d bytes]", len(reported.Message))
		}
		fmt.Fprintf(&rendered, "- %s [%s] from %s%s, filed %s, unhandled through %d of your passes: %s\n",
			reported.ID, reported.Severity, reporter, on, reported.RecordedAt.UTC().Format(time.RFC3339),
			passes[reported.ID], preview)
	}
	if len(overdue) > len(listed) {
		fmt.Fprintf(&rendered, "\n%d further overdue report(s) from a program manager are not listed here.\n", len(overdue)-len(listed))
	}
	return rendered.String()
}

// overdueReports is every unhandled warning or note a program manager filed
// that at least overduePasses of the Lead Product Manager's passes started after,
// oldest first, with how many each has stood through. A pass counts where it
// took a turn: one that asked her nothing did not pass over anything.
func (t Trigger) overdueReports() ([]report.Report, map[string]int, error) {
	reports, err := t.Pile.List()
	if err != nil {
		return nil, nil, fmt.Errorf("read the collected reports: %w", err)
	}
	var candidates []report.Report
	for _, reported := range reports {
		if reported.Role == domain.RoleProgramManager && reported.Severity != report.SeverityCritical {
			candidates = append(candidates, reported)
		}
	}
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	handlings, err := t.Pile.Handlings()
	if err != nil {
		return nil, nil, fmt.Errorf("read what became of the collected reports: %w", err)
	}
	recorded, _, err := t.Reports.List()
	if err != nil {
		return nil, nil, fmt.Errorf("read the earlier passes: %w", err)
	}
	var starts []time.Time
	for _, entry := range recorded {
		if entry.Role == domain.RoleProductManager && entry.Turns > 0 && !entry.IsMiss() {
			starts = append(starts, entry.StartedAt)
		}
	}
	var overdue []report.Report
	passes := map[string]int{}
	for _, reported := range report.ByFiling(report.Unhandled(candidates, handlings)) {
		through := 0
		for _, started := range starts {
			if started.After(reported.RecordedAt) {
				through++
			}
		}
		if through >= overduePasses {
			overdue = append(overdue, reported)
			passes[reported.ID] = through
		}
	}
	return overdue, passes, nil
}

// overdueLines is the overdue section a pass carries, introduced as what it
// is. Nothing is added where there is none.
func overdueLines(overdue string) []string {
	overdue = strings.TrimSpace(overdue)
	if overdue == "" {
		return nil
	}
	return []string{"", "## Overdue reports", "", overdue}
}

// clip cuts text to a byte bound on a rune boundary, marking the cut.
func clip(text string, bound int) string {
	if len(text) <= bound {
		return text
	}
	cut := bound
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + " […]"
}
