package readmodel

// What needs the operator's own hand, derived once from the records that say so.
//
// A finding only the operator can act on used to reach him by accident. Six
// developer reports asking for a change a person had to make sat in the pile
// from 2026-08-17 until a sweep reached them on 2026-09-14, and the finding
// that sweep produced landed on the Lead Product Manager's own checklist, which he
// sees when he asks. He asked why it took a month.
//
// So the class is derived here, from the durable records and nowhere else, and
// every surface reads this rather than deciding for itself what needs him: the
// attention line names each finding, and the channel says each one to him once.
// Two of those surfaces working it out separately is the disagreement one read
// model exists to prevent.
//
// Several things make a finding. One is read from the report pile: a handling
// that says the report needs the operator is one until a later handling of the
// same report says otherwise. A report itself never is, whatever its severity:
// a critical report nobody has handled is with the role it was addressed to, or
// with the Lead Product Manager who handles the pile, and is listed as that
// role's in addressedreports.go. Only a handling records that a person must
// act. The second is a run stopping on a
// condition only a person can clear, which the harness records as the
// development manager's escalation of that stoppage to the operator: her
// decision is the one typed record that says a stopped run needs a person
// rather than a repair, a re-run, or a wait, and it stands while it is the
// decision on the item's latest stopped run. The brake's hold is not one of
// them at all: once it is escalated it is the Lead Product Manager's, with the
// operator told, and it is read from the intake hold beside the switches and
// named on the attention line there, as the held intake it is.
//
// The third is read from the amendment log and the recurring passes: an owning
// role's batch of recommendations on the changes proposed to its documents, one
// finding per pass, standing while any proposal in it is undecided. It is
// derived in amendments.go and joins the others at the read model's reading and
// at the channel's.
//
// A repeatedly failing product pass with a validated person-only remedy is
// another finding. passfailures.go supplies its direct notification from the
// same reading that keeps its single pass entry on the attention line; the
// affected pass succeeding clears that entry.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// OperatorAction is one finding only the operator can act on: what is needed,
// where it is recorded, and since when.
type OperatorAction struct {
	// Key names the finding durably, so a surface that says each one once can
	// remember having said it. A report makes one finding however many times it
	// is handled, so the key is the report's; a stoppage makes one however many
	// times it is decided, so the key is the run's.
	Key string `json:"key"`
	// Subject is what the finding is named by where it is listed: the report,
	// or the work item whose run stopped.
	Subject string `json:"subject"`
	// ReportID is the report the finding came from, and RunID the run whose
	// stoppage it came from; each finding has one of the two. WorkItemID is the
	// item either was about, where it was about one.
	ReportID   string `json:"report_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
	// Needs is what the operator has to do, in the words of whoever found it: the
	// handling's reason, the critical report's own message, or the development
	// manager's reason for escalating.
	Needs string `json:"needs"`
	// Reason is the reason from the operator's closed list the finding was
	// raised for: the handling's, the escalation's, or the one its shape
	// stands for. A finding recorded with none, as every one recorded before
	// the list was required is, is not his; the ownership registry gives it back
	// to the role that raised it.
	Reason ownership.OperatorReason `json:"reason,omitempty"`
	// RecordedIn says where the finding is recorded, so the operator can go and
	// read the whole of it.
	RecordedIn string `json:"recorded_in"`
	// FoundBy is who found it and how, and Ends is what ends it, both worded once
	// here: every surface that names the finding says the same thing about who
	// found it and what it is waiting for.
	FoundBy string    `json:"found_by"`
	Ends    string    `json:"ends"`
	Since   time.Time `json:"since"`
}

// operatorActionKey names a finding by the report it came from, and
// escalationKey by the run whose stoppage was escalated.
func operatorActionKey(reportID string) string { return "report:" + reportID }

func escalationKey(runID string) string { return "run:" + runID }

// The endings, one per shape of finding. A report-derived finding ends with a
// handling; an escalated stoppage ends when the development manager decides the
// run again, the item is run again, parked, or leaves the backlog, or the run's
// branch and worktree are both gone.
const (
	reportFindingEnds     = "a later handling of the report records it done"
	escalationFindingEnds = "a later triage decision on the run, the item being run again, parked, retired, or closed, or the run's branch and worktree both being gone ends it"
)

// OperatorActions is every finding that needs the operator, read from the pile
// and what became of it, oldest first: each report a handling recorded as
// needing his hand. It is the one derivation of the report-derived shape;
// EscalatedOperatorActions is the stoppages', and readOperatorActions puts them
// together for the attention line.
func OperatorActions(reports []report.Report, handlings []report.Handling) []OperatorAction {
	handled := report.Handled(handlings)
	var actions []OperatorAction
	for _, reported := range report.ByFiling(reports) {
		// Sweep-derived findings remain one status entry even with a person-only
		// remedy. PassFailureOperatorActions supplies their direct notifications.
		if reported.PassFailureTask != "" {
			continue
		}
		handling, done := handled[reported.ID]
		if done && handling.NeedsOperator {
			actions = append(actions, OperatorAction{
				Key:        operatorActionKey(reported.ID),
				Subject:    reported.ID,
				ReportID:   reported.ID,
				WorkItemID: reported.WorkItemID,
				Needs:      strings.Join(strings.Fields(handling.Reason), " "),
				Reason:     handling.OperatorReason,
				RecordedIn: fmt.Sprintf("the handling of %s recorded in %s, over the %s's report from %s",
					reported.ID, handling.RunID, reported.Role.Title(), reported.RunID),
				FoundBy: fmt.Sprintf("the %s, handling the report", handling.Role.Title()),
				Ends:    reportFindingEnds,
				Since:   handling.RecordedAt,
			})
		}
	}
	return actions
}

// EscalatedOperatorActions is every stopped run the development manager
// escalated to the operator, as findings, oldest first: a run stopping on a
// condition only a person can clear is recorded as her escalation of it, since
// she is the role that judges a stoppage and escalating is the one decision
// that hands it to a person.
//
// A finding stands while the escalation is the decision standing on the item's
// latest run that stopped with a durable blocker, and nothing has since settled
// what it handed the operator. A later decision on that run supersedes it and a
// later run makes it history. The rest end it as settled rather than decided,
// and each is named in EndedEscalation so the item can be told which:
//
//   - the reconcile sweep having recorded the ending on the run, which is the
//     one ending a surface that can ask neither the tracker nor the repository
//     can read;
//   - the item having left the backlog — retired, or closed after the operator
//     did what was asked — or having been parked, where the caller can say how
//     the item stands; a caller that cannot passes nil;
//   - the run's branch and worktree both gone, where the caller can look in the
//     repository; a caller that cannot passes nil, and a look that did not reach
//     the repository ends nothing, since a record is not a look;
//   - the escalation giving no reason from the operator's closed list, which
//     every escalation recorded before the list was enforced does: it goes back
//     to the development manager (escalationEnding).
//
// On 2026-09-28 the escalation of run-95b34031 still named the operator two
// days after the Lead Product Manager parked its item, with the run's branch
// and worktree long gone: nothing about either ended it, and a line naming the
// operator as the mover of something that needs no person is the false claim
// the operator's rule of 2026-09-26 forbids. A caller that knows neither reads every
// escalation without a recorded ending as standing, which is the direction that
// says a finding once rather than never.
//
// A triage record that cannot be read costs that item's finding and is said in
// the problem rather than read as no escalation: a stoppage the operator was
// handed must not vanish from the line because one file would not open. A
// reading wired without decisions at all names none and says nothing, which is
// the answer the held-work derivation gives the same absence: every surface the
// harness builds wires them, and a fixture that does not is asking about
// something else.
func EscalatedOperatorActions(runs []runstate.State, decisions Decisions, items EscalatedItems, look Look) ([]OperatorAction, string) {
	standing, _, problem := Escalations(runs, decisions, items, look)
	return standing, problem
}

// EscalatedItem is what one reading knows of an escalated item's place in the
// backlog: whether the queue still admits it, and why it is parked where it is.
type EscalatedItem struct {
	Admitted bool
	Parked   string
}

// EscalatedItems answers EscalatedItem for one work item.
type EscalatedItems func(workItemID string) EscalatedItem

// EndedEscalation is one escalation to the operator that has stopped being one,
// and what ended it. Recorded says the reconcile sweep has already told the
// item, so the sweep does not tell it again.
type EndedEscalation struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Why        string `json:"why"`
	Recorded   bool   `json:"recorded,omitempty"`
	// DecidedAt is when the escalation that ended was decided, which is what
	// tells the ending recorded on the run apart from a later escalation of it.
	DecidedAt time.Time `json:"decided_at"`
	// ReturnedTo is the role the escalation went back to, on one that named the
	// operator without a reason from his closed list: the development manager,
	// whose docket the stoppage is put back on. It is empty on every other
	// ending, which leaves nothing for anybody to decide.
	ReturnedTo Mover `json:"returned_to,omitempty"`
}

// Escalations is every escalation to the operator the development manager
// decided, split into the ones still standing, as findings oldest first, and
// the ones something has since ended, with what ended each. See
// EscalatedOperatorActions for the rule.
func Escalations(runs []runstate.State, decisions Decisions, items EscalatedItems, look Look) ([]OperatorAction, []EndedEscalation, string) {
	if decisions == nil {
		return nil, nil, ""
	}
	stopped := latestPerItem(runs, func(run runstate.State) bool {
		return run.WorkItemID != "" && run.Status.Terminal() && strings.TrimSpace(run.Blocker) != ""
	})
	var actions []OperatorAction
	var ended []EndedEscalation
	var problems []string
	for workItemID, run := range stopped {
		counters, err := decisions.Counters(workItemID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("what triage decided about %s could not be read, so an escalation of it cannot be named: %v", workItemID, err))
			continue
		}
		decision, decided := counters.DecisionOf(run.RunID)
		// An escalation to a role is that role's, and reaches it through the report
		// addressed to it; only one to the operator is a finding for him.
		if !decided || !decision.EscalatesToOperator() {
			continue
		}
		if why, recorded, returnedTo := escalationEnding(run, decision, items, look); why != "" {
			ended = append(ended, EndedEscalation{RunID: run.RunID, WorkItemID: workItemID, Why: why, Recorded: recorded, DecidedAt: decision.DecidedAt, ReturnedTo: returnedTo})
			continue
		}
		actions = append(actions, OperatorAction{
			Key:        escalationKey(run.RunID),
			Subject:    workItemID,
			RunID:      run.RunID,
			WorkItemID: workItemID,
			Needs:      strings.Join(strings.Fields(decision.Reason), " "),
			Reason:     decision.OperatorReason,
			RecordedIn: fmt.Sprintf("the development manager's escalation of run %s, recorded in %s at turn %d, and the blocker on %s",
				run.RunID, decision.Conversation, decision.Turn, workItemID),
			FoundBy: "the development manager, escalating the stopped run to the operator",
			Ends:    escalationFindingEnds,
			Since:   decision.DecidedAt,
		})
	}
	sortOperatorActions(actions)
	sort.Slice(ended, func(i, j int) bool { return ended[i].RunID < ended[j].RunID })
	return actions, ended, strings.Join(problems, "; ")
}

// escalationEnding is what has ended one escalated run's escalation, whether
// that is already recorded on the run, and the role it went back to where it
// went back to one; empty where nothing has ended it.
//
// An escalation that names the operator and gives no reason from his closed
// list was recorded before the list was enforced, and is not his: it goes back
// down the ladder to the development manager, whose decision about the stopped
// run it always was. Every other ending is asked first, because each says more
// about where the work now stands than that.
func escalationEnding(run runstate.State, decision runstate.TriageDecision, items EscalatedItems, look Look) (string, bool, Mover) {
	// An ending recorded before this escalation was decided ended an earlier one
	// of the same run, and says nothing about this one.
	if ending := run.EscalationEnded; ending != nil && strings.TrimSpace(ending.Why) != "" && !ending.At.Before(decision.DecidedAt) {
		return ending.Why, true, ""
	}
	if items != nil {
		item := items(run.WorkItemID)
		switch {
		case !item.Admitted:
			return fmt.Sprintf("%s is no longer in the backlog: it was closed or retired", run.WorkItemID), false, ""
		case strings.TrimSpace(item.Parked) != "":
			return runstate.BoundEscalationEnding(fmt.Sprintf("%s was parked, so nothing about it waits on the operator: %s", run.WorkItemID, item.Parked)), false, ""
		}
	}
	if look != nil {
		if found := look(run); found.Recorded() && found.Looked() && !found.Holds() {
			return runstate.BoundEscalationEnding(fmt.Sprintf("run %s's branch and worktree are both gone, so there is no change of it left for anybody to act on: %s", run.RunID, found.Describe())), false, ""
		}
	}
	if !decision.OperatorReason.Valid() {
		return unreasonedEscalationEnding(run.RunID), false, MoverDevelopmentManager
	}
	return "", false, ""
}

// unreasonedEscalationEnding is what an item is told when its escalation to the
// operator goes back to the development manager for giving no closed-list
// reason: who owns it now, and why.
func unreasonedEscalationEnding(runID string) string {
	reasons := make([]string, 0, len(ownership.OperatorReasons()))
	for _, reason := range ownership.OperatorReasons() {
		reasons = append(reasons, reason.Says())
	}
	return runstate.BoundEscalationEnding(fmt.Sprintf(
		"it was recorded before an escalation to the operator had to give a reason, and it gives none only he can act on — %s — so it is not his; what becomes of run %s is the development manager's decision, and the run is listed under her as a stoppage waiting on that decision",
		strings.Join(reasons, "; "), runID))
}

// sortOperatorActions orders findings oldest first, the key breaking a tie, so
// a reading is the same twice over rather than whatever order a map handed back.
func sortOperatorActions(actions []OperatorAction) {
	sort.SliceStable(actions, func(i, j int) bool {
		if !actions[i].Since.Equal(actions[j].Since) {
			return actions[i].Since.Before(actions[j].Since)
		}
		return actions[i].Key < actions[j].Key
	})
}

// Whose is whose move a finding is and what ends it, in the one clause the
// message that reaches the operator carries as the move that follows. It is
// the attention line's own Whose for the finding, so the two cannot disagree.
func (a OperatorAction) Whose() string {
	return operatorActionAttention(a).Whose()
}

// Says is the finding as the attention line names it: what it is about, what
// is needed, who found it, and where it is recorded.
func (a OperatorAction) Says() string {
	what := fmt.Sprintf("%s needs your hand: %s (found by %s; recorded in %s",
		a.Subject, singleLine(a.Needs, maxRefusalBytes), a.FoundBy, a.RecordedIn)
	if item := strings.TrimSpace(a.WorkItemID); item != "" && item != a.Subject {
		what += ", about " + item
	}
	return what + ")"
}

// readOperatorActions is every finding as one reading has it: the report-derived
// ones from the pile, and the escalated stoppages from the runs and what triage
// decided about them, less the ones something has ended: the item parked or out
// of the queue, as items says, or the run's change gone, as look finds. A pile
// that could not be read lists none of the first kind, and the line says so
// through the pile's own problem, which the attention line already carries; a
// triage record that could not be read is said here.
func readOperatorActions(reports []report.Report, handlings []report.Handling, pileProblem string, sources Sources, items EscalatedItems, look Look) ([]Attention, string) {
	var actions []OperatorAction
	if pileProblem == "" {
		actions = OperatorActions(reports, handlings)
	}
	var problem string
	if sources.Stoppages == nil || sources.Decisions == nil {
		// Read as the held-work derivation reads the same absence: nothing named,
		// nothing said. Every surface the harness builds wires both.
	} else if runs, err := sources.Stoppages.Recorded(); err != nil {
		problem = fmt.Sprintf("the recorded runs could not be read, so no escalated stoppage can be named: %v", err)
	} else {
		escalated, escalatedProblem := EscalatedOperatorActions(runs, sources.Decisions, items, look)
		actions = append(actions, escalated...)
		problem = escalatedProblem
	}
	attention := make([]Attention, 0, len(actions))
	for _, action := range actions {
		attention = append(attention, operatorActionAttention(action))
	}
	return attention, problem
}
