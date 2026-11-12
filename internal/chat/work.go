package chat

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxSurveyItems bounds how many items of one kind a survey lists. What an
// operator needs from a survey is what to decide about next, not an export of
// the tracker, so the list is cut while the count stays exact.
const maxSurveyItems = 15

// maxSurveyTitleBytes keeps one tracker-supplied title to one line of a survey.
const maxSurveyTitleBytes = 120

// The two reasons the harness stops a provider invocation on time, named here
// rather than imported so a conversation stays independent of how a run is
// executed. They read differently to an operator: a stall is worth looking into,
// an exhausted budget is simply work that needs another pass.
const (
	ProviderStopStalled         = "stalled"
	ProviderStopBudgetExhausted = "budget_exhausted"
)

// The two ways a provider refuses an attempt without judging the work, named
// here for the same reason. They read differently to an operator too: an
// exhausted limit is the account's own capacity and may need a decision, while
// an overloaded server is weather and needs nothing.
const (
	PauseUsageLimit     = "usage_limit"
	PauseServerOverload = "server_overload"
)

// defaultStopGrace bounds how long stopping waits for a cancelled run to give
// up. It is generous because a cancelled run still has to kill its provider and
// check processes, and it is bounded because an operator who asked to stop must
// get an answer either way rather than a conversation that hangs.
const defaultStopGrace = 2 * time.Minute

// Work is the harness's own hand in a conversation: what an operator sees and
// steers development with without leaving it. Every method here runs because
// the operator asked for it in their own words. The product manager cannot reach
// any of it: it manages what the queue says, through the Tracker, and running,
// redirecting, and stopping the work itself stays the operator's decision and
// the harness's action.
type Work interface {
	// Survey reports development as the harness sees it now: the runs it has in
	// flight and the tracker's own view of what is blocked, claimed, available,
	// and done.
	Survey(ctx context.Context) (Survey, error)
	// Backlog reports the admitted work in the product manager's order, which is
	// the order a development manager pulls in. It is the same queue read from
	// the same tracker rather than a second account of it, so what the operator
	// sees here is what is actually pulled from.
	Backlog(ctx context.Context) (backlog.Queue, error)
	// Run carries one work item through the harness pipeline: worktree,
	// developer, checks, review, and integration under the project's configured
	// policy. It returns what the run reported even when it failed, because a
	// failed run's branch, worktree, and findings are what the operator acts on.
	// The selection travels with it and is recorded on the run, so what shows up
	// in a survey afterwards says why it was started rather than only that it was.
	Run(ctx context.Context, workItemID string, selection Selection) (RunReport, error)
	// RequestStop asks one run to stop, whichever process is working on it. It is
	// a request rather than an act for the reason the whole design turns on: the
	// process holding a run is the only thing entitled to end it, so this records
	// the operator's decision where that process reads it and returns. What became
	// of the run is then read like anything else, through Progress.
	RequestStop(ctx context.Context, request StopRequest) error
	// Progress reports where the harness's most recent run of one work item has
	// got to, read from the durable run record. It is how a conversation learns
	// that a run it started has crossed a phase without knowing anything about
	// how a run is executed: the pipeline writes down where it is as it goes,
	// and this reads what it wrote. A work item with no recorded run is a
	// failure like any other — a run that has not written anything down yet is
	// simply a question this cannot answer, and a conversation asking it says
	// nothing rather than guessing.
	Progress(ctx context.Context, workItemID string) (RunProgress, error)
	// Changes reports what the harness's most recent run of one work item
	// changed, read from the durable run record rather than from a worktree. That
	// is the whole point of it: the worktree a run wrote in is removed when the
	// run is cleaned up, and the branch with it, so a change nobody recorded is a
	// change nobody can be shown afterwards.
	Changes(ctx context.Context, workItemID string) (RunChanges, error)
	// Price reports what one work item has cost, broken down by the runs it
	// took, read from the durable run records. It is the same evidence the
	// tracker's own price was summed from, so a breakdown and the total beside an
	// item in a survey can never be two different accounts of the spending; and
	// because it is read rather than stored, it prices an item whose runs
	// happened before anything was recording prices at all.
	Price(ctx context.Context, workItemID string) (ItemPrice, error)
	// Direct records durable operator direction on a work item, where the next
	// attempt at it reads it. It never changes the item's status: redirecting
	// work says what to do differently, it does not decide the work is done.
	Direct(ctx context.Context, workItemID, note string) error
	// Settle settles the runs nothing is working on any more, the way an
	// interrupted process's runs are settled: artifacts preserved, integrated
	// work finished, and anything a person has to decide recorded as a blocker.
	Settle(ctx context.Context) ([]Settlement, error)
}

// WorkItemSummary is one tracked item as a conversation reports it.
type WorkItemSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	// Cost is what the runs made for this item have cost, as the tracker carries
	// it. It is absent from an item nothing has priced yet, which is not the same
	// as an item that cost nothing.
	Cost *ItemCost `json:"cost,omitempty"`
	// Parked reports work somebody has deliberately taken out of reach. It is
	// here because a survey is what the queue's owner orders from, and the
	// priority beside it is exactly what parking must not be read off: an item
	// listed at the bottom of the order looks like the next-but-last thing to be
	// pulled, and a parked one is not going to be pulled at all.
	Parked bool `json:"parked,omitempty"`
}

// ItemCost is the provider-reported price of one work item, summed over every
// run made for it. UnknownRuns is what keeps it honest: a run whose record is
// gone cannot be priced, and while any are unpriced the total is a floor on what
// the item cost rather than what it cost.
type ItemCost struct {
	TotalUSD    float64 `json:"total_usd"`
	Runs        int     `json:"runs"`
	UnknownRuns int     `json:"unknown_runs,omitempty"`
}

// Render is the price as a survey shows it: money, and a mark saying the number
// is a floor when some of the work behind it could not be priced.
func (c ItemCost) Render() string {
	if c.UnknownRuns > 0 {
		return fmt.Sprintf("≥ $%.2f", c.TotalUSD)
	}
	return fmt.Sprintf("$%.2f", c.TotalUSD)
}

// ItemPrice is what one work item cost, broken down by the runs it took. It is
// read from the durable run records rather than from the tracker, so it prices
// an item whose price nobody has recorded yet and says which attempt spent what.
type ItemPrice struct {
	WorkItemID string     `json:"work_item_id"`
	Runs       []RunPrice `json:"runs,omitempty"`
	TotalUSD   float64    `json:"total_usd"`
	// UnknownRuns counts the runs whose recorded evidence is gone, and is what
	// makes the total a floor rather than a price.
	UnknownRuns int `json:"unknown_runs,omitempty"`
}

// RunPrice is what one run of a work item cost: which attempt it was, how it
// went, and what the provider charged for the invocations inside it.
type RunPrice struct {
	RunID string `json:"run_id"`
	// Status is the durable status, which is what says whether the attempt has
	// stopped spending. Outcome is what became of the work, in the fixed
	// vocabulary the run records derive and every listing says: a conversation
	// that named the status here would tell an operator "failed" about a run
	// whose change is preserved and whose item is back with a person, which is
	// the misreading that vocabulary exists to stop. It is empty on a survey
	// assembled before the vocabulary existed, and the status stands in.
	Status    string    `json:"status"`
	Outcome   string    `json:"outcome,omitempty"`
	Phase     string    `json:"phase,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// CompletedAt is when the run ended, and nil on one still going or one whose
	// record names no end.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Integrated reports the attempt that promoted its work, which is what
	// separates the run that finished the item from the ones that did not.
	Integrated bool `json:"integrated,omitempty"`
	// Remains is what the run's record says survives of its change, in the
	// phrase the run records derive: "work preserved", "work removed", or "no
	// artifacts recorded". It is empty on a price assembled before that was
	// carried, and a reader says so rather than guessing.
	Remains string `json:"remains,omitempty"`
	// Invocations counts the provider invocations behind the cost: the
	// developer's, the reviewer's, and one more for every repair attempt.
	Invocations int     `json:"invocations,omitempty"`
	CostUSD     float64 `json:"cost_usd"`
	// Unknown says why this run could not be priced. A run carrying one is not a
	// run that was free, and its cost is deliberately left out of the total
	// rather than added to it as a zero.
	Unknown string `json:"unknown,omitempty"`
}

// The two things that choose work, as a conversation names them. They are the
// same words the durable record uses, kept here so a conversation does not have
// to reach into how a run is stored to say who picked something.
const (
	OperatorSelected           = "operator"
	DevelopmentManagerSelected = "development manager"
)

// Selection is why the harness is running a work item: who chose it and on what
// grounds. It travels with the request to run and is recorded on the run, so a
// survey afterwards can answer the question that has no answer at all once
// something other than the operator does the choosing.
type Selection struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// StopRequest is the operator asking one run to stop. It names the run rather
// than only the item because that is what a stop is about: an item can have had
// several runs, and the one being stopped is the one in flight now.
type StopRequest struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Reason     string `json:"reason,omitempty"`
}

// RunSnapshot is one run the harness has recorded and not finished. It is read
// from durable run state rather than from this process, so a run another
// process is working on is just as visible as one started here.
type RunSnapshot struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	Status     string    `json:"status"`
	Phase      string    `json:"phase,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	// SelectedBy and SelectedBecause are who chose this work and why, as the run
	// recorded it when it started. Both empty is a run nothing accounted for, and
	// a survey says exactly that rather than leaving the line out: work proceeding
	// for no visible reason is the thing an operator most needs to see.
	SelectedBy      string `json:"selected_by,omitempty"`
	SelectedBecause string `json:"selected_because,omitempty"`
	// StopRequested reports that the operator has asked this run to stop and it
	// has not stopped yet, which is a fact about the run rather than about the
	// request: a run stays in flight until it reaches a boundary and ends itself.
	StopRequested bool `json:"stop_requested,omitempty"`
	// Detail names anything about the run that its status does not say, such as
	// the usage limit it is waiting out.
	Detail string `json:"detail,omitempty"`
}

// Survey is development as the harness sees it now.
type Survey struct {
	InFlight  []RunSnapshot     `json:"in_flight,omitempty"`
	Claimed   []WorkItemSummary `json:"claimed,omitempty"`
	Blocked   []WorkItemSummary `json:"blocked,omitempty"`
	Available []WorkItemSummary `json:"available,omitempty"`
	Completed []WorkItemSummary `json:"completed,omitempty"`
	// Unavailable names the parts of the survey that could not be read. A part
	// that could not be read is reported as unknown rather than rendered as
	// empty, and it never hides the parts that were read.
	Unavailable []Unread `json:"unavailable,omitempty"`
}

// Unread is one part of a survey that could not be read, named by the group it
// would have filled so the report can say "unknown" exactly where it belongs.
type Unread struct {
	Group  string `json:"group"`
	Reason string `json:"reason"`
}

// RunReport is what one run of the harness pipeline produced, in the terms an
// operator steers by. It is a projection rather than the pipeline's own
// outcome, so a conversation stays independent of how a run is executed.
type RunReport struct {
	RunID        string `json:"run_id,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	Status       string `json:"status,omitempty"`
	Branch       string `json:"branch,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	// Integrated, TargetBranch, and Commit describe a promotion that actually
	// happened. A run that succeeded without integrating says so instead.
	Integrated     bool   `json:"integrated,omitempty"`
	TargetBranch   string `json:"target_branch,omitempty"`
	Commit         string `json:"commit,omitempty"`
	WorkItemClosed bool   `json:"work_item_closed,omitempty"`
	// Undischarged reports an integrated change that does not discharge its item —
	// its developer claimed as much, or its reviewer approved it as evidence
	// rather than as the work — with the account whichever of them said so gave,
	// and where the item was left. A reader told only that the change was
	// integrated would read it as done — which is the reading this whole
	// distinction exists to stop — and one told only that the item is open would go
	// looking for it in a queue nothing is going to offer it from.
	//
	// The disposition is the harness's own derivation rather than this surface's
	// reading of the two fields beside it: a parking waits for a person and a
	// dependency releases itself, and a surface that worded that difference for
	// itself is one that could word it differently from the next.
	Undischarged            bool   `json:"undischarged,omitempty"`
	UndischargedReason      string `json:"undischarged_reason,omitempty"`
	UndischargedDisposition string `json:"undischarged_disposition,omitempty"`
	RepairAttempts          int    `json:"repair_attempts,omitempty"`
	// TransientRelaunches counts the provider invocations the run reissued after
	// one died without judging the work. It is reported beside the repair
	// attempts and means something different: the provider's weather rather than
	// anything the change had to be corrected for.
	TransientRelaunches int `json:"transient_relaunches,omitempty"`
	// Blocked reports that the run recorded a durable blocker on its item, and
	// Paused reports a run that stopped short of finishing and is owed a
	// continuation rather than having failed.
	Blocked            bool       `json:"blocked,omitempty"`
	Paused             bool       `json:"paused,omitempty"`
	UsageLimitKind     string     `json:"usage_limit_kind,omitempty"`
	UsageLimitResetsAt *time.Time `json:"usage_limit_resets_at,omitempty"`
	// PauseCause is which refusal the run is waiting out, one of the Pause
	// constants above. Both refusals park a run on a deadline, so the deadline
	// alone cannot say which of them a conversation is reporting.
	PauseCause string `json:"pause_cause,omitempty"`
	// ProviderStop is set instead of the usage-limit fields when what paused the
	// run was the harness stopping a provider invocation on time. Such a run is
	// continuable straight away rather than waiting on a deadline.
	ProviderStop string `json:"provider_stop,omitempty"`
	// DirectivePause is set instead of either when what paused the work was an
	// unresolved user directive. It names the directive and what is unresolved,
	// because settling that is what makes the work continuable — nothing about it
	// is a matter of waiting. It is the one pause that can appear without a run
	// behind it, on work a directive stopped before it was ever claimed.
	DirectivePause string `json:"directive_pause,omitempty"`
	// DependencyPause is set instead of any of the others when what paused the
	// work was unfinished work the item was made to wait on. It names that work,
	// because closing it or unlinking it is what makes this continuable — nothing
	// about it is a matter of waiting on a provider. Like a directive pause it can
	// appear without a run behind it, on work a dependency stopped before it was
	// ever claimed.
	DependencyPause string `json:"dependency_pause,omitempty"`
	// TrackerPause is set instead of any of the others when what parked the run
	// was the tracker not answering the read a gate boundary makes, for the whole
	// of that boundary's recovery window. It names the read and what the window
	// was spent on, because what makes this continuable is the store answering
	// again rather than anything about the work. Unlike the two pauses above it
	// never appears without a run behind it: it is a run that reached a gate and
	// could not find out whether it may take the next step.
	TrackerPause string `json:"tracker_pause,omitempty"`
	// IntakeHold is set instead of any of the others when the work was never
	// started because something is holding what the harness chooses for itself.
	// It never appears on a run: nothing was claimed and nothing developed, so
	// there is no worktree, no branch, and nothing to continue — only an item
	// that was not picked up and says who stopped it and why. The hold is carried
	// whole rather than as the parts a headline happens to want, because who
	// placed it is a fact about the record and not one a report may infer.
	IntakeHold *runstate.IntakeHold `json:"intake_hold,omitempty"`
	// OperatorHeldSince is set instead of any of the others when what stopped the
	// work was the operator pausing all harness activity. Like a directive pause
	// it is lifted by a decision rather than by a clock, and it can appear with no
	// run behind it, on work a paused harness declined to start at all.
	OperatorHeldSince *time.Time `json:"operator_held_since,omitempty"`
	Failure           string     `json:"failure,omitempty"`
	// Reported counts what this run's agents reported while their work carried
	// on, and ReportProblem names one the run could not keep. The reports
	// themselves are in the collected pile that /reports shows; the count is
	// here because a run finishing is when the operator finds out there is
	// something new in it.
	Reported int `json:"reported,omitempty"`
	// ReportedWorst is the severity of the most attention-seeking of them. A
	// count on its own reads the same for three notes and for something already
	// costing somebody, which is how a critical report goes unread by an operator
	// who was told about it. It is empty on a run that reported nothing, and on a
	// record written before this was carried, which is why the line below falls
	// back to the count alone rather than claiming a severity it does not have.
	ReportedWorst report.Severity `json:"reported_worst,omitempty"`
	ReportProblem string          `json:"report_problem,omitempty"`
}

// RunChanges is what one recorded run changed, as the durable record holds it.
// Every field comes from that record rather than from the repository, so a run
// whose worktree and branch are long gone still says what it did and still
// points at the pull request it published. What it cannot say is what has
// happened since: this describes the change the run made, not the state of the
// repository now.
type RunChanges struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Status     string `json:"status"`
	Phase      string `json:"phase,omitempty"`
	// StartedAt and CompletedAt say when the run this describes happened, so an
	// account of a change is never mistaken for an account of the latest one.
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Branch      string     `json:"branch,omitempty"`
	// WorktreePath is where the change was written, and Preserved reports that
	// it is still there to be looked at. A cleaned-up run says so rather than
	// naming a directory that no longer exists.
	WorktreePath string `json:"worktree_path,omitempty"`
	Preserved    bool   `json:"preserved,omitempty"`
	// Files and DiffStat are the recorded account of the change itself: which
	// files the run touched and how much of each.
	Files    string `json:"files,omitempty"`
	DiffStat string `json:"diff_stat,omitempty"`
	// Integrated, TargetBranch, and Commit describe a promotion that actually
	// happened, and are read from the recorded integration rather than inferred
	// from a run having succeeded.
	Integrated   bool   `json:"integrated,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	Commit       string `json:"commit,omitempty"`
	// PullRequest is where the work was published, when the project publishes.
	// It outlives the branch, which is exactly why it is worth pointing at.
	PullRequest *PublishedChange `json:"pull_request,omitempty"`
	Failure     string           `json:"failure,omitempty"`
}

// PublishedChange is the pull request a run published its work through, as the
// run's record holds it.
type PublishedChange struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state,omitempty"`
	Merged bool   `json:"merged,omitempty"`
}

// Settlement is what settling did with one run.
type Settlement struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Action     string `json:"action"`
	Detail     string `json:"detail,omitempty"`
	Failure    string `json:"failure,omitempty"`
}

// FinishedRun is a run this conversation started, collected after it ended.
// Err is whatever the harness reported; the report beside it still describes
// what the run left behind, because a failed run is not an empty one.
type FinishedRun struct {
	WorkItemID string
	Report     RunReport
	Err        error
}

// Stopped is what stopping a run achieved. Finished reports whether the run
// actually gave up before the grace ran out: a run still winding down is
// reported as such rather than described as stopped.
type Stopped struct {
	WorkItemID string
	Finished   bool
	// AlreadyFinished reports a run that had reached its own conclusion before
	// the cancellation reached it, whether or not that conclusion integrated
	// anything. Nothing was stopped, and nothing pretends it was.
	AlreadyFinished bool
	// RunID names the run the stop was aimed at, and Requested reports a stop
	// this conversation asked another process to carry out rather than performing
	// itself. The two go together: a run this conversation started is cancelled
	// here and there is nothing to identify, while a run somewhere else is asked,
	// and what became of it is read from its record afterwards. Requested is what
	// keeps the report honest about which of those happened.
	RunID       string
	Requested   bool
	Report      RunReport
	RunErr      error
	Settlements []Settlement
}

// Render describes the survey for an operator. Every group is named even when
// it is empty: "blocked: none" is an answer, and a missing heading is not.
//
// The theme decides how much of it may be dressed. Each group is coloured for
// the state it reports — running, blocked, and done have the same colour here
// as anywhere else — and the identifiers are aligned so a column of them can be
// read down rather than picked out of ragged prose. Both are additions: every
// group says its state in words, so a survey read with the colour stripped, or
// read where colour was never permitted, loses nothing.
func (s Survey) Render(theme console.Theme) string {
	var rendered strings.Builder
	if reason, unread := s.unread(inFlightGroup); unread {
		rendered.WriteString(renderUnread(theme, inFlightGroup, reason))
	} else {
		rendered.WriteString(renderRunSnapshots(theme, s.InFlight))
	}
	for _, group := range []struct {
		label string
		state console.State
		items []WorkItemSummary
	}{
		// Available work is in no state of its own: nothing is running it,
		// nothing is holding it, and it is not done. It is left undressed rather
		// than given a colour that would mean something it does not.
		{"claimed", console.StateRunning, s.Claimed},
		{"blocked", console.StateBlocked, s.Blocked},
		{"available", "", s.Available},
		{"completed", console.StateDone, s.Completed},
	} {
		if reason, unread := s.unread(group.label); unread {
			rendered.WriteString(renderUnread(theme, group.label, reason))
			continue
		}
		rendered.WriteString(renderWorkItemGroup(theme, group.label, group.state, group.items))
		// Finished work is what somebody asks the price of, so the completed group
		// is the one place worth saying that some of it carries none.
		if group.label == "completed" {
			rendered.WriteString(renderUnpricedNote(group.items))
		}
	}
	// Anything unread that does not belong to a group above is still reported;
	// a part nobody could read is never dropped for not fitting the listing.
	for _, unavailable := range s.Unavailable {
		if !knownSurveyGroup(unavailable.Group) {
			rendered.WriteString(renderUnread(theme, unavailable.Group, unavailable.Reason))
		}
	}
	return rendered.String()
}

// inFlightGroup is the survey group runs fill, named so the report can say the
// runs are unknown in the same place it would have listed them.
const inFlightGroup = "in flight"

// InFlightGroup names that group for whoever assembles a survey, so the name a
// failure is filed under is the name the report renders it at.
func InFlightGroup() string { return inFlightGroup }

func knownSurveyGroup(group string) bool {
	switch group {
	case inFlightGroup, "claimed", "blocked", "available", "completed":
		return true
	default:
		return false
	}
}

func (s Survey) unread(group string) (string, bool) {
	for _, unavailable := range s.Unavailable {
		if unavailable.Group == group {
			return unavailable.Reason, true
		}
	}
	return "", false
}

// renderUnread reports a part of the survey nobody could read. It wears the
// failed colour because that is what it is: not an empty group, but a question
// the harness could not answer.
func renderUnread(theme console.Theme, group, reason string) string {
	return theme.State(console.StateFailed, fmt.Sprintf("%s: could not be read, so treat it as unknown rather than empty: %s\n",
		group, singleLine(reason, maxSurveyTitleBytes*2)))
}

func renderRunSnapshots(theme console.Theme, runs []RunSnapshot) string {
	if len(runs) == 0 {
		return "in flight: none\n"
	}
	var rendered strings.Builder
	fmt.Fprint(&rendered, theme.State(console.StateRunning, fmt.Sprintf("in flight (%d):\n", len(runs))))
	width := runIDWidth(runs)
	for _, run := range runs {
		// Each run is coloured for what it is doing rather than for the group it
		// is in: a run that failed or is waiting is in flight as far as the
		// record is concerned, and reads nothing like one that is working.
		fmt.Fprintf(&rendered, "  %s on %s [%s]\n",
			theme.State(run.dressing(), pad(run.RunID, width)), run.WorkItemID, run.state())
		if run.Branch != "" {
			fmt.Fprintf(&rendered, "    branch %s, started %s\n", run.Branch, run.StartedAt.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(&rendered, "    started %s\n", run.StartedAt.UTC().Format(time.RFC3339))
		}
		// Why the item was chosen is said for every run, including the ones nothing
		// accounted for. It is the line that makes the difference between watching
		// the harness work and watching it do something unexplained, so it is never
		// the line that is quietly left out.
		fmt.Fprintf(&rendered, "    %s\n", run.chosen())
		if run.StopRequested {
			fmt.Fprint(&rendered, theme.State(console.StateFailed,
				"    you have asked this run to stop; it ends itself at its next step, no later than ten minutes after you asked, and nothing has been thrown away yet; a change whose checks already passed is kept for its review instead\n"))
		}
		if run.Detail != "" {
			fmt.Fprintf(&rendered, "    %s\n", singleLine(run.Detail, maxSurveyTitleBytes*2))
		}
	}
	return rendered.String()
}

// chosen says why the harness is running this item. A run that recorded nothing
// says so plainly rather than showing a blank: "nobody recorded why" is an
// answer an operator can act on, and an omitted line is not.
func (r RunSnapshot) chosen() string {
	by := strings.TrimSpace(r.SelectedBy)
	because := strings.TrimSpace(r.SelectedBecause)
	switch {
	case by == "" && because == "":
		return "chosen by: nothing recorded why this run was started"
	case because == "":
		return "chosen by " + by + ", with no reason recorded"
	case by == "":
		return "chosen, by nobody recorded: " + singleLine(because, maxSurveyTitleBytes*2)
	default:
		return "chosen by the " + by + ": " + singleLine(because, maxSurveyTitleBytes*2)
	}
}

// state names where a run is: its status, and the phase it reached when the
// record has one, because "running" alone does not say what it is doing.
func (r RunSnapshot) state() string {
	if r.Phase == "" {
		return r.Status
	}
	return r.Status + ", " + r.Phase
}

// dressing is the state this run is coloured for. A status the harness does not
// recognize is left undressed rather than guessed at.
func (r RunSnapshot) dressing() console.State {
	switch r.Status {
	case "running", "pending":
		return console.StateRunning
	case "succeeded":
		return console.StateDone
	case "failed", "timed_out", "cancelled":
		return console.StateFailed
	default:
		return ""
	}
}

func renderWorkItemGroup(theme console.Theme, label string, state console.State, items []WorkItemSummary) string {
	if len(items) == 0 {
		return label + ": none\n"
	}
	listed := items
	if len(listed) > maxSurveyItems {
		listed = listed[:maxSurveyItems]
	}
	var rendered strings.Builder
	fmt.Fprint(&rendered, theme.State(state, fmt.Sprintf("%s (%d):\n", label, len(items))))
	// The identifiers are padded to the widest in the group, so the priorities
	// and titles beside them line up in columns rather than starting wherever
	// the tracker's identifiers happen to end. The prices get a column of their
	// own, right-aligned, so what the work cost can be read down the group
	// instead of hunted for at the end of each title.
	width := itemIDWidth(listed)
	prices := itemCostWidth(listed)
	floor := false
	for _, item := range listed {
		identifier := theme.State(state, pad("["+item.ID+"]", width))
		title := singleLine(item.Title, maxSurveyTitleBytes)
		// Parked work is marked against its priority, because the priority is what
		// it would otherwise be mistaken for. The item says why; what this column
		// has to carry is that the number beside it decides nothing here.
		if item.Parked {
			title = "(parked) " + title
		}
		if prices == 0 {
			fmt.Fprintf(&rendered, "  %s p%d %s\n", identifier, item.Priority, title)
			continue
		}
		price := ""
		if item.Cost != nil {
			price = item.Cost.Render()
			floor = floor || item.Cost.UnknownRuns > 0
		}
		// An item nothing has priced is left blank rather than shown as nothing:
		// a zero there would read as work that was free.
		fmt.Fprintf(&rendered, "  %s p%d %s %s\n", identifier, item.Priority, padLeft(price, prices), title)
	}
	if floor {
		rendered.WriteString("  ≥ marks a floor: some runs of that item have no surviving record and could not be priced.\n")
	}
	if len(items) > len(listed) {
		fmt.Fprintf(&rendered, "  %d further %s item(s) are not listed here.\n", len(items)-len(listed), label)
	}
	return rendered.String()
}

// renderUnpricedNote says how much finished work carries no price, and what the
// two reasons for that are. It is the one place an operator notices the gap, so
// it is also where the way to close it is named: an item the harness ran has a
// price waiting in its run records whether or not anybody has recorded it yet,
// and an item the harness never ran has none to find.
func renderUnpricedNote(items []WorkItemSummary) string {
	unpriced := 0
	for _, item := range items {
		if item.Cost == nil {
			unpriced++
		}
	}
	if unpriced == 0 {
		return ""
	}
	return fmt.Sprintf("  %d completed item(s) carry no price: work the harness did not run has none, and work it did is priced by `yoyo cost --record`.\n", unpriced)
}

// Render breaks one work item's price down by the runs it took, which is the
// question a single total invites: an item that cost twenty-eight dollars across
// a rejected attempt and a successful one is a different story from one that
// cost twenty-eight in a single pass.
func (p ItemPrice) Render() string {
	var rendered strings.Builder
	if len(p.Runs) == 0 {
		fmt.Fprintf(&rendered, "cost: the harness has no recorded run of %s, so it has no price rather than a price of nothing.\n", p.WorkItemID)
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "cost: %s across %d run(s)\n", p.total(), len(p.Runs))
	width := runPriceIDWidth(p.Runs)
	for _, run := range p.Runs {
		fmt.Fprintf(&rendered, "  %s started %s [%s] %s\n",
			pad(run.RunID, width), run.StartedAt.UTC().Format(time.RFC3339), run.outcome(), run.price())
	}
	if p.UnknownRuns > 0 {
		fmt.Fprintf(&rendered, "  %d of those run(s) left no record to price, so the total is a floor rather than the price.\n", p.UnknownRuns)
	}
	// The gap is stated rather than closed. A conversation that discussed five
	// items cannot be attributed to one of them without a judgement nobody asked
	// this to make, so what is priced here is runs and says so.
	rendered.WriteString("  This is what the runs cost; the conversations that steered them are recorded but not priced against any item.\n")
	return rendered.String()
}

// total says whether the number is the price or the least it can have been.
func (p ItemPrice) total() string {
	if p.UnknownRuns > 0 {
		return fmt.Sprintf("at least $%.2f", p.TotalUSD)
	}
	return fmt.Sprintf("$%.2f", p.TotalUSD)
}

// outcome names how the attempt went, the way a survey names a run: what became
// of it, the phase it reached, and whether it was the attempt that promoted the
// work.
//
// A survey assembled before the vocabulary existed says so rather than falling
// back to the status it carries. The status is the word that reads "failed" over
// a preserved, blocked run, which is the sentence the vocabulary was introduced
// to stop — substituting it here would put that sentence back in exactly the
// records nobody can re-derive it for.
func (r RunPrice) outcome() string {
	outcome := r.Outcome
	if outcome == "" {
		outcome = "outcome not recorded"
	}
	if r.Phase != "" {
		outcome += ", " + r.Phase
	}
	if r.Integrated {
		outcome += ", integrated"
	}
	return outcome
}

// price says what the attempt cost, or plainly that nothing survives to say. An
// unpriced run never reads as a free one, and a run still going says its figure
// is what it has spent so far rather than what it will have cost.
func (r RunPrice) price() string {
	if r.Unknown != "" {
		return "unknown: " + singleLine(r.Unknown, maxSurveyTitleBytes*2)
	}
	if !r.finished() {
		return fmt.Sprintf("$%.2f so far from %d invocation(s)", r.CostUSD, r.Invocations)
	}
	return fmt.Sprintf("$%.2f from %d invocation(s)", r.CostUSD, r.Invocations)
}

// finished reports a run that has stopped spending. The terminal statuses are
// named here rather than imported, as the provider-stop reasons above are, so a
// conversation stays independent of how a run is executed. Everything else is
// treated as still spending — a run in flight, one paused and owed a
// continuation, and any status this does not recognize — because a figure that
// is still growing reads as final if this guesses the wrong way.
func (r RunPrice) finished() bool {
	switch r.Status {
	case "succeeded", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}

func runPriceIDWidth(runs []RunPrice) int {
	width := 0
	for _, run := range runs {
		if measured := utf8.RuneCountInString(run.RunID); measured > width {
			width = measured
		}
	}
	return width
}

// itemCostWidth measures the price column a group needs, and reports zero when
// nothing in the group is priced, which is what leaves an unpriced survey
// looking exactly as it did before prices existed.
func itemCostWidth(items []WorkItemSummary) int {
	width := 0
	for _, item := range items {
		if item.Cost == nil {
			continue
		}
		if measured := utf8.RuneCountInString(item.Cost.Render()); measured > width {
			width = measured
		}
	}
	return width
}

func itemIDWidth(items []WorkItemSummary) int {
	width := 0
	for _, item := range items {
		if measured := utf8.RuneCountInString(item.ID) + 2; measured > width {
			width = measured
		}
	}
	return width
}

func runIDWidth(runs []RunSnapshot) int {
	width := 0
	for _, run := range runs {
		if measured := utf8.RuneCountInString(run.RunID); measured > width {
			width = measured
		}
	}
	return width
}

// pad widens one column entry to the width its group needs. It counts runes
// rather than bytes: a tracker identifier is ASCII in practice, and a column
// measured in bytes would still be a column that does not line up when it is
// not.
func pad(value string, width int) string {
	if missing := width - utf8.RuneCountInString(value); missing > 0 {
		return value + strings.Repeat(" ", missing)
	}
	return value
}

// padLeft is pad for a column of money, which reads down the page only when the
// figures end in the same place rather than starting in it.
func padLeft(value string, width int) string {
	if missing := width - utf8.RuneCountInString(value); missing > 0 {
		return strings.Repeat(" ", missing) + value
	}
	return value
}

// Render describes a finished run to the operator. It reports the artifacts a
// failed run preserved as carefully as it reports a successful promotion:
// either way they are what the operator's next decision is about.
//
// One line in it is dressed: what the run's agents reported, by the worst of
// what they reported. A finished run scrolls past in the middle of a
// conversation the operator is holding about something else, and that is the
// line they have to be able to catch without stopping to read the rest.
func (r RunReport) Render(theme console.Theme) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s\n", r.Headline())
	if r.RunID != "" {
		fmt.Fprintf(&rendered, "  run: %s\n", r.RunID)
	}
	if r.Branch != "" {
		fmt.Fprintf(&rendered, "  branch: %s\n", r.Branch)
	}
	if r.WorktreePath != "" && !r.Integrated {
		fmt.Fprintf(&rendered, "  worktree: %s\n", r.WorktreePath)
	}
	if r.RepairAttempts > 0 {
		fmt.Fprintf(&rendered, "  repair attempts: %d\n", r.RepairAttempts)
	}
	if r.TransientRelaunches > 0 {
		fmt.Fprintf(&rendered, "  relaunches after a provider death: %d\n", r.TransientRelaunches)
	}
	if r.Reported > 0 {
		rendered.WriteString(theme.Severity(console.Severity(r.ReportedWorst),
			fmt.Sprintf("  %sreported %d thing(s) without stopping%s; /reports shows them\n",
				r.ReportedWorst.Prefix(), r.Reported, worstReported(r.ReportedWorst))))
	}
	if r.ReportProblem != "" {
		fmt.Fprintf(&rendered, "  %s\n", singleLine(r.ReportProblem, MaxOperatorMessageBytes))
	}
	if r.Failure != "" {
		fmt.Fprintf(&rendered, "  failure: %s\n", singleLine(r.Failure, MaxOperatorMessageBytes))
	}
	return rendered.String()
}

// worstReported names the worst severity a run reported anything at, and says
// nothing where the record does not carry one — a run from before this was
// recorded, which must read as a count rather than as a pile of notes.
func worstReported(worst report.Severity) string {
	if worst == "" {
		return ""
	}
	return ", the worst of them " + string(worst)
}

// Headline states in one line what became of a run. It is what the operator
// reads first and what the product manager is told, so it never describes work
// as integrated on the strength of anything but a recorded promotion.
func (r RunReport) Headline() string {
	item := r.WorkItemID
	if item == "" {
		item = "the work item"
	}
	switch {
	case r.Paused && r.IntakeHold != nil:
		// Intake is held, so the harness declined to choose this item. It is the
		// one pause an operator can walk straight past: nothing is running, nothing
		// is claimed, and naming the item themselves runs it anyway — which is the
		// distinction between holding what the harness picks and pausing everything.
		//
		// Who is holding it comes off the record. The operator is the ordinary
		// answer and was once the only one this said, which was wrong the moment
		// the failure-storm brake placed a hold and this told them they had.
		held := fmt.Sprintf("%s was not started because intake is held, since %s: %s",
			item, r.IntakeHold.HeldAt.UTC().Format(time.RFC3339),
			singleLine(r.IntakeHold.Says(), maxSurveyTitleBytes*2))
		return held + fmt.Sprintf("; nothing is running it and nothing was claimed, /release lets the harness choose work again, and /work %s runs it now regardless", item)
	case r.Paused && r.OperatorHeldSince != nil:
		// The operator paused everything rather than this, so this says what they
		// did and what undoes it. It never claims a run is in flight: a paused
		// harness declines to start work as readily as it parks work under way.
		return fmt.Sprintf("%s is paused because you paused all harness activity at %s; nothing reaches the provider until `yoyo resume` lifts it, and /work %s carries on after that",
			item, r.OperatorHeldSince.UTC().Format(time.RFC3339), item)
	case r.Paused && r.DirectivePause != "":
		// A directive pause is lifted by a decision rather than by time, so this
		// says what to settle rather than what to wait for. It never claims a run
		// is in flight: a directive can stop work before anything was claimed.
		return fmt.Sprintf("%s is paused for an unresolved directive and nothing is working on it: %s; /resolve releases it and /work %s carries on",
			item, r.DirectivePause, item)
	case r.Paused && r.DependencyPause != "":
		// A dependency pause is lifted by other work finishing rather than by time,
		// so this says what to close rather than what to wait for. It never claims a
		// run is in flight: a dependency can stop work before anything was claimed.
		return fmt.Sprintf("%s is paused waiting on unfinished work it depends on: %s; closing that work, or unlinking it, is what releases %s and /work %s carries on after that",
			item, r.DependencyPause, item, item)
	case r.Paused && r.TrackerPause != "":
		// A tracker park is lifted by the store answering rather than by a decision
		// or a clock, so this says what went unanswered and that the run is waiting
		// where it stopped. Nothing about the change was judged.
		return fmt.Sprintf("%s is parked because %s; nothing about its change was judged, its claim, branch, worktree, and developer session are all preserved, and /work %s continues the same run once the store answers",
			item, r.TrackerPause, item)
	case r.Paused && r.ProviderStop != "":
		stopped := "the AI session running it produced no output for longer than the harness allows, so the harness stopped it; the cause was outside the work"
		if r.ProviderStop == ProviderStopBudgetExhausted {
			stopped = "its provider was still working when its total budget ran out"
		}
		return fmt.Sprintf("%s is still in flight: %s, and it reported no failure; /work %s continues the same run from where it stopped", item, stopped, item)
	case r.Paused:
		refusal := "a transient provider server overload"
		if r.PauseCause != PauseServerOverload {
			limit := r.UsageLimitKind
			if strings.TrimSpace(limit) == "" {
				limit = "provider"
			}
			refusal = "an exhausted " + limit + " usage limit"
		}
		asks := "an unstated time"
		if r.UsageLimitResetsAt != nil {
			asks = r.UsageLimitResetsAt.UTC().Format(time.RFC3339)
		}
		return fmt.Sprintf("%s is paused for %s and is still in flight; it asks again by %s at the latest, sooner at its probe interval, and /work %s continues the same run", item, refusal, asks, item)
	case r.Integrated:
		closed := ""
		if r.WorkItemClosed {
			closed = " and the item is closed"
		}
		// An item the run deliberately left open is said so here rather than left
		// to be inferred from the closure not being mentioned. The two read
		// identically otherwise, and only one of them is work still to do — and
		// where the item was left decides who has to do something about it next.
		if r.Undischarged {
			closed = " and the item " + r.UndischargedDisposition + ": " + r.UndischargedReason
		}
		return fmt.Sprintf("%s was integrated into %s at %s%s", item, r.TargetBranch, r.Commit, closed)
	case r.Blocked:
		return fmt.Sprintf("%s stopped and now carries a durable blocker", item)
	case r.Failure != "":
		return fmt.Sprintf("%s did not finish", item)
	case r.Status != "":
		return fmt.Sprintf("%s finished with status %s and nothing integrated", item, r.Status)
	default:
		return item + " finished with nothing integrated"
	}
}

// Render describes what a run changed. It says which run it is describing
// first, because the answer to "what did that change" is only worth anything
// once you know which attempt it is about, and it says plainly when the record
// holds no account of a change rather than printing an empty listing that reads
// like one.
func (c RunChanges) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s on %s [%s], started %s\n", c.RunID, c.WorkItemID, c.state(), c.StartedAt.UTC().Format(time.RFC3339))
	if c.Branch != "" {
		fmt.Fprintf(&rendered, "  branch: %s\n", c.Branch)
	}
	if c.WorktreePath != "" {
		if c.Preserved {
			fmt.Fprintf(&rendered, "  worktree: %s\n", c.WorktreePath)
		} else {
			// Which removal it was is not said, because what the reader needs is
			// that the directory is not there: cleanup after a promotion, a re-run
			// retiring what this one preserved, and the convergence sweep retiring
			// an old empty checkout all end the same way.
			fmt.Fprintf(&rendered, "  worktree: %s, removed\n", c.WorktreePath)
		}
	}
	if c.Integrated {
		fmt.Fprintf(&rendered, "  integrated into %s at %s\n", c.TargetBranch, c.Commit)
	}
	if c.PullRequest != nil {
		fmt.Fprintf(&rendered, "  pull request: #%d %s%s\n", c.PullRequest.Number, c.PullRequest.URL, c.PullRequest.describe())
	}
	if c.Failure != "" {
		fmt.Fprintf(&rendered, "  failure: %s\n", singleLine(c.Failure, MaxOperatorMessageBytes))
	}
	if c.Files == "" && c.DiffStat == "" {
		rendered.WriteString("  nothing was recorded about what this run changed; it may not have got as far as changing anything.\n")
		return rendered.String()
	}
	for _, section := range []struct {
		label string
		text  string
	}{{"files", c.Files}, {"diff stat", c.DiffStat}} {
		if strings.TrimSpace(section.text) == "" {
			continue
		}
		fmt.Fprintf(&rendered, "  %s:\n", section.label)
		for _, line := range strings.Split(strings.TrimRight(section.text, "\n"), "\n") {
			fmt.Fprintf(&rendered, "    %s\n", strings.TrimRight(line, " \t"))
		}
	}
	return rendered.String()
}

// state names where the run is, the way a survey names it: its status, and the
// phase it reached when the record has one.
func (c RunChanges) state() string {
	if c.Phase == "" {
		return c.Status
	}
	return c.Status + ", " + c.Phase
}

// describe says what the forge last said about a published request, when it
// said anything at all.
func (p PublishedChange) describe() string {
	switch {
	case p.Merged:
		return " (merged)"
	case p.State != "":
		return " (" + p.State + ")"
	default:
		return ""
	}
}

// Render describes what stopping achieved, including the parts of it that did
// not: a run still winding down and a settlement that failed are both things
// the operator has to know about.
func (s Stopped) Render() string {
	var rendered strings.Builder
	if s.WorkItemID == "" {
		// Nothing was stopped, so there is nothing to describe. The reason for
		// that is the caller's error to report, not something to narrate here.
		return ""
	}
	if !s.Finished {
		if s.Requested {
			// The request is durable and the run is somewhere else, so this is not a
			// failure and there is nothing to type again: the run stops at its next
			// step, within runstate.StopTakesEffectWithin, and what it kept until
			// then it still has.
			fmt.Fprintf(&rendered, "asked %s to stop; it had not given up by the time this stopped waiting, so it is still in flight and stops at its next step, no later than ten minutes after it was asked. If its checks have already passed, its change is kept for its review instead.\n", s.WorkItemID)
			rendered.WriteString(indent("/status says whether it has, and what it left behind is settled the next time anything settles."))
			return rendered.String()
		}
		fmt.Fprintf(&rendered, "%s was asked to stop but has not given up yet; it is left in flight and can be settled once it does.\n", s.WorkItemID)
		return rendered.String()
	}
	if s.AlreadyFinished {
		fmt.Fprintf(&rendered, "%s had already finished before the stop reached it, so nothing was stopped.\n", s.WorkItemID)
		rendered.WriteString(indent(s.Report.Headline()))
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "stopped work on %s.\n", s.WorkItemID)
	rendered.WriteString(indent(s.Report.Headline()))
	if s.Report.Paused {
		// A paused run reported no failure but is not finished either: it is
		// waiting to be continued, and only saying so keeps "stopped" from
		// reading as "over".
		rendered.WriteString(indent("it had paused itself before the stop reached it, so nothing is working on it now and it continues only if you start it again."))
	}
	if s.RunErr != nil {
		rendered.WriteString(indent("the stopped run reported: " + singleLine(s.RunErr.Error(), MaxOperatorMessageBytes)))
	}
	rendered.WriteString(renderSettlements(s.Settlements))
	return rendered.String()
}

func renderSettlements(settlements []Settlement) string {
	if len(settlements) == 0 {
		return indent("nothing was left outstanding to settle.")
	}
	var rendered strings.Builder
	for _, settlement := range settlements {
		fmt.Fprintf(&rendered, "    %s on %s: %s\n", settlement.RunID, settlement.WorkItemID, settlement.Action)
		if settlement.Detail != "" {
			fmt.Fprintf(&rendered, "        %s\n", singleLine(settlement.Detail, maxSurveyTitleBytes*2))
		}
		if settlement.Failure != "" {
			fmt.Fprintf(&rendered, "        could not be settled: %s\n", singleLine(settlement.Failure, maxSurveyTitleBytes*2))
		}
	}
	return rendered.String()
}

// singleLine folds a value into one bounded line, so tracker prose and provider
// failures stay a list entry whatever they contain. It is cut on a rune
// boundary: a line truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	return oneline.Fold(value, limit)
}
