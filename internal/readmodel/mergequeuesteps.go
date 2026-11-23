package readmodel

// What each merge queue entry is doing now, since when, and whether it has
// done it for too long.
//
// An entry's state (mergequeues.go) says where it stands; its step says what
// the queue is doing with it at this moment — building its candidate, running
// one named check, waiting for its review, landing, waiting on GitHub — and the
// moment that step began, read off the same records: the generation's check,
// launch, and review times, and the landing's mutations. A step whose record
// carries no time is said without one rather than given a guessed one.
//
// An entry the queue is working, or the entry at the head of the queue waiting
// for its candidate to be built, whose step has run longer than
// execution.merge_queue_step_overdue_after is a problem: every change behind it
// waits on it, and nothing in the queue's own flow says so. So is a merge
// GitHub holds whose required checks all ended and at least one did not pass —
// failed, or cancelled with nothing failed — because GitHub will not make that
// merge by itself however long the queue waits. Either is said on the attention
// line as the development manager's to move: finding what holds the head of the
// queue, and taking it up again or handing it back, is a judgement about run
// reliability, and both are commands she has.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultMergeQueueStepOverdueAfter is how long one step of a queue entry may
// run before it is a problem, where nothing configured a bound. It matches
// execution.merge_queue_step_overdue_after's default: a code candidate takes
// about seventy minutes from build to landing, so two hours on one step is
// well past an ordinary one.
const DefaultMergeQueueStepOverdueAfter = 2 * time.Hour

// QueueEntryOverdue is one queue entry the queue has made no progress with
// past the bound, or whose merge GitHub will not make by itself.
type QueueEntryOverdue struct {
	TargetBranch string `json:"target_branch"`
	EntryID      string `json:"entry_id"`
	Order        uint64 `json:"order"`
	Place        uint64 `json:"place"`
	WorkItemID   string `json:"work_item_id"`
	Title        string `json:"title"`
	// Step is the step it has been at, and Since when that step began.
	Step  string    `json:"step"`
	Since time.Time `json:"since"`
	// BoundSeconds is the bound the step ran past, and zero where the entry is
	// a problem for a reason that waits on no bound.
	BoundSeconds int64 `json:"bound_seconds,omitempty"`
	// WaitingOn is what the entry is waiting on, in ordinary words.
	WaitingOn string `json:"waiting_on"`
}

// Says is the entry as a sentence: which entry, its step and since when, why
// it is a problem, and what it waits on.
func (o QueueEntryOverdue) Says() string {
	why := "GitHub will not make this merge by itself"
	if o.BoundSeconds > 0 {
		why = "past the " + boundWords(time.Duration(o.BoundSeconds)*time.Second) + " a step may take before it is a problem"
	}
	return fmt.Sprintf("merge queue entry %d for %s (place %d, %s %s) has been %s since %s, %s; every change behind it waits; it is waiting on %s",
		o.Order, o.TargetBranch, o.Place, o.WorkItemID, o.Title, o.Step, localMoment(o.Since), why, o.WaitingOn)
}

// boundWords is a bound in hours or minutes, as a person states one.
func boundWords(bound time.Duration) string {
	if bound >= time.Hour && bound%time.Hour == 0 {
		return count(int(bound/time.Hour), "hour")
	}
	return count(int(bound.Round(time.Minute)/time.Minute), "minute")
}

// queueOverdueAttention is an overdue entry as the attention line carries it.
// The entry is the ID and its work item the WorkItemID; the registry names the
// development manager as its mover.
func queueOverdueAttention(overdue QueueEntryOverdue) Attention {
	return resolved(Attention{
		Kind:         AttentionMergeQueueOverdue,
		ID:           overdue.EntryID,
		WorkItemID:   overdue.WorkItemID,
		QueueOverdue: &overdue,
	})
}

// MergeQueueOverdue is every overdue entry of every queue, as the attention
// line carries them, in the order the queues show them.
func MergeQueueOverdue(queues []MergeQueueStanding) []Attention {
	var entries []Attention
	for _, queue := range queues {
		for _, entry := range queue.Entries {
			if entry.Overdue != nil {
				entries = append(entries, queueOverdueAttention(*entry.Overdue))
			}
		}
	}
	return entries
}

// setStep names what the queue is doing with an entry now and since when,
// from the records its state was read from. Generations is nil where the
// landing record placed the entry, and reading is GitHub's latest account of
// the merge it holds, where one was read.
func setStep(read *QueueEntryStanding, entry runstate.MergeQueueEntry, landing runstate.MergeQueueLanding, generations []runstate.MergeQueueGeneration, reading *runstate.MergeQueueForgeReading) {
	at := func(moment time.Time) {
		if !moment.IsZero() {
			moment = moment.UTC()
			read.StepSince = &moment
		}
	}
	attempt, attempted := landing.Current()
	switch read.State {
	case QueueEntryLanded:
		if completion := landing.Completion; completion != nil {
			read.left = completion.QueueAt
		} else if handback := landing.Handback; handback != nil {
			read.left = handback.At
		}
		return
	case QueueEntryReleased, QueueEntrySetAside:
		if handback := landing.Handback; handback != nil {
			read.left = handback.At
		}
		return
	case QueueEntryHandedBack:
		read.left = landing.Handback.At
		read.Step = handbackWords(landing.Handback.Continuation)
		at(landing.Handback.At)
		return
	case QueueEntryRecording:
		read.Step = "recording its landing on the run and the work item"
		switch {
		case attempted && attempt.Landed != nil:
			at(attempt.Landed.ConfirmedAt)
		case landing.Completion != nil:
			at(landing.Completion.QueueAt)
		case landing.Handback != nil:
			at(landing.Handback.At)
		}
		return
	case QueueEntryUncertain:
		if !attempted {
			return
		}
		if record, unsettled := attempt.Unsettled(); unsettled {
			read.Step = "waiting to learn what became of " + stepWords(record.Mutation, landing.TargetBranch, attempt.PullRequest)
			at(record.RequestedAt)
		} else if withdrawal := attempt.Withdrawal; withdrawal != nil && withdrawal.Settled == nil {
			read.Step = fmt.Sprintf("taking back the merge of pull request %d", withdrawal.PullRequest)
			at(withdrawal.IntendedAt)
		}
		return
	case QueueEntryForgeHolds:
		read.Step = fmt.Sprintf("waiting for GitHub to merge pull request %d", attempt.PullRequest)
		if requested, asked := attempt.Mutation(runstate.MergeQueueRequestMerge); asked && requested.Settled != nil {
			at(requested.Settled.At)
		}
		if reading != nil && reading.PullRequest == attempt.PullRequest && reading.Unread == "" && len(reading.FailedChecks) > 0 && len(reading.PendingChecks) == 0 {
			read.forgeWontMerge = fmt.Sprintf("GitHub's required %s %s ended without passing (failed, or cancelled with nothing failed) at the reading of %s, and no required check is still running, so GitHub will not merge pull request %d until somebody runs the check again, takes the entry up again, or hands it back",
				plural(len(reading.FailedChecks), "check", "checks"), strings.Join(reading.FailedChecks, ", "), localMoment(reading.At), attempt.PullRequest)
		}
		return
	case QueueEntryLanding:
		read.Step = fmt.Sprintf("landing, attempt %d, %s", attempt.Number, pathWords(attempt.Path, landing.TargetBranch))
		since := attempt.IntendedAt
		if n := len(attempt.Mutations); n > 0 {
			last := attempt.Mutations[n-1]
			read.Step = fmt.Sprintf("landing, attempt %d: %s", attempt.Number, stepWords(last.Mutation, landing.TargetBranch, attempt.PullRequest))
			since = last.RequestedAt
			if last.Settled != nil {
				since = last.Settled.At
			}
		}
		at(since)
		return
	}
	if len(generations) == 0 {
		if read.State == QueueEntryWaiting {
			read.Step = "waiting for its candidate to be built"
			at(entry.AdmittedAt)
		}
		return
	}
	latest := generations[len(generations)-1]
	read.built = latest.CreatedAt
	read.selection = latest.Selection
	stageTries := func(stage runstate.MergeQueueStage) string {
		var set []runstate.MergeQueueInterruption
		for _, interruption := range latest.Interruptions {
			if interruption.Stage == stage {
				set = append(set, interruption)
			}
		}
		if len(set) == 0 {
			return ""
		}
		last := set[len(set)-1]
		said := fmt.Sprintf(", try %d after %d set aside", len(set)+1, len(set))
		if last.Problem != "" {
			said += " (the last because " + singleLine(last.Problem, maxRefusalBytes) + ")"
		}
		return said
	}
	run, review := latest.CheckRun, latest.Review
	switch read.State {
	case QueueEntryWaiting:
		// A candidate that stopped being one that could land is built again.
		read.Step = "waiting for its candidate to be built again on the target as it stands"
		if latest.Invalidated != nil {
			at(latest.Invalidated.At)
		}
	case QueueEntryTargetRed:
		read.Step = "waiting on " + latest.TargetBranch + " being made to pass " + latest.BaseCheck.Command
		if latest.BaseCheck.FinishedAt != nil {
			at(*latest.BaseCheck.FinishedAt)
		}
	case QueueEntryVerified:
		read.Step = "waiting to begin landing"
		if review != nil && review.FinishedAt != nil {
			at(*review.FinishedAt)
		}
	case QueueEntryFailed:
		if base := latest.BaseCheck; base != nil && base.FinishedAt == nil {
			read.Step = "running " + base.Command + " on " + latest.TargetBranch + " at the candidate's base, to learn whether the failure is the change's"
			at(base.StartedAt)
			return
		}
		read.Step = "waiting for the queue to hand it back after it did not pass"
		switch {
		case review != nil && review.FinishedAt != nil:
			at(*review.FinishedAt)
		case run != nil && run.FinishedAt != nil:
			at(*run.FinishedAt)
		case latest.Paths != nil:
			at(latest.Paths.CheckedAt)
		}
	case QueueEntryVerifying:
		switch {
		case run == nil && latest.ForgePush != nil:
			read.Step = "waiting for GitHub's required checks on the candidate"
			if latest.ForgePush.PullRequest > 0 {
				read.Step = fmt.Sprintf("waiting for GitHub's required checks on the candidate's pull request %d", latest.ForgePush.PullRequest)
			}
			at(latest.ForgePush.PushedAt)
		case run == nil:
			// A start set aside is started again from when it was found.
			read.Step = "starting its checks" + stageTries(runstate.MergeQueueStageChecks)
			since := latest.CreatedAt
			for _, interruption := range latest.Interruptions {
				if interruption.Stage == runstate.MergeQueueStageChecks && interruption.FoundAt.After(since) {
					since = interruption.FoundAt
				}
			}
			at(since)
		case run.FinishedAt == nil:
			index, commands := len(run.Results), latest.Checks.Commands
			if index >= len(commands) {
				read.Step = "finishing its checks" + stageTries(runstate.MergeQueueStageChecks)
				at(run.StartedAt)
				return
			}
			command := commands[index]
			since := run.StartedAt
			launched := index == 0
			for _, launch := range latest.Launches {
				if launch.Stage == runstate.MergeQueueStageChecks && launch.Command == command && !launch.StartedAt.Before(run.StartedAt) {
					since, launched = launch.StartedAt, true
				}
			}
			if launched {
				read.Step = fmt.Sprintf("running %s, check %d of %d", command, index+1, len(commands))
			} else {
				// No launch of this check was recorded, so the time said is when
				// the checks began rather than when this one did.
				read.Step = fmt.Sprintf("running its checks, now %s, check %d of %d", command, index+1, len(commands))
			}
			read.Step += stageTries(runstate.MergeQueueStageChecks)
			at(since)
		case review == nil:
			read.Step = "waiting for its independent review to start" + stageTries(runstate.MergeQueueStageReview)
			at(*run.FinishedAt)
		case review.FinishedAt == nil:
			read.Step = "under independent review" + stageTries(runstate.MergeQueueStageReview)
			if routing := latest.ReviewRouting; routing != nil && routing.Waiting != "" {
				read.Step += "; " + singleLine(routing.Waiting, maxRefusalBytes)
			}
			at(review.StartedAt)
		default:
			read.Step = "verifying it again"
			at(*review.FinishedAt)
		}
	}
}

// placeTheHead finishes the steps that depend on the entries around one: the
// head of the queue, waiting for its candidate, has waited since the later of
// its admission and the last entry leaving the queue; and an entry waiting its
// turn that was admitted before the entry the queue chose first was passed
// over, for the rule that chose it.
func placeTheHead(open []QueueEntryStanding, left time.Time, standing MergeQueueStanding) {
	if len(open) == 0 {
		return
	}
	head := &open[0]
	if head.State == QueueEntryWaiting && head.Generation == 0 && !left.IsZero() && (head.StepSince == nil || left.After(*head.StepSince)) {
		moment := left.UTC()
		head.StepSince = &moment
	}
	rule, why := standing.FirstRule, standing.FirstWhy
	if head.selection != nil {
		rule, why = head.selection.Rule, head.selection.Why
	}
	if rule == "" || head.Generation == 0 {
		return
	}
	for index := 1; index < len(open); index++ {
		entry := &open[index]
		if entry.State != QueueEntryWaiting || entry.Generation != 0 || entry.Order >= head.Order {
			continue
		}
		entry.Step = fmt.Sprintf("passed over: the queue's rule (%s) chose %s first, because %s", rule, head.WorkItemID, why)
		entry.StepSince = nil
		if !head.built.IsZero() {
			moment := head.built.UTC()
			entry.StepSince = &moment
		}
	}
}

// overdueSteps marks every entry that is a problem: one the queue is working,
// or the head waiting for its candidate, whose step has run past the bound,
// and a merge GitHub will not make by itself. An entry handed back is its
// run's, on the development manager's list of stopped runs already, and an
// entry passed over waits on the one ahead of it, which is the one marked.
func overdueSteps(queue *MergeQueueStanding, open []QueueEntryStanding, now time.Time, bound time.Duration) {
	if now.IsZero() {
		return
	}
	if bound <= 0 {
		bound = DefaultMergeQueueStepOverdueAfter
	}
	for index := range open {
		entry := &open[index]
		mark := func(boundSeconds int64, waitingOn string) {
			overdue := QueueEntryOverdue{
				TargetBranch: queue.TargetBranch, EntryID: entry.EntryID, Order: entry.Order, Place: entry.Place,
				WorkItemID: entry.WorkItemID, Title: entry.Title, Step: entry.Step, BoundSeconds: boundSeconds, WaitingOn: waitingOn,
			}
			if entry.StepSince != nil {
				overdue.Since = *entry.StepSince
			}
			entry.Overdue = &overdue
		}
		if entry.forgeWontMerge != "" {
			mark(0, entry.forgeWontMerge)
			continue
		}
		if entry.StepSince == nil || entry.Step == "" || now.Sub(*entry.StepSince) <= bound {
			continue
		}
		switch entry.State {
		case QueueEntryHandedBack, QueueEntryLanded, QueueEntryReleased, QueueEntrySetAside:
			continue
		case QueueEntryWaiting:
			if index != 0 || strings.HasPrefix(entry.Step, "passed over") {
				continue
			}
		}
		mark(int64(bound/time.Second), waitingOnWords(*entry))
	}
}

// waitingOnWords is what an entry past the bound is waiting on.
func waitingOnWords(entry QueueEntryStanding) string {
	switch entry.State {
	case QueueEntryWaiting:
		return "the queue's worker building its candidate, which it has not done; the queue's last pass says why, if it recorded a reason"
	case QueueEntryVerifying:
		return "its step to finish: " + entry.Why
	case QueueEntryFailed:
		return "the queue to hand it back or settle whose failure it is: " + entry.Why
	case QueueEntryTargetRed:
		return entry.Why
	case QueueEntryVerified:
		return "the queue's worker beginning to land it"
	case QueueEntryForgeHolds:
		return "GitHub merging it: " + entry.Why
	case QueueEntryUncertain, QueueEntryLanding, QueueEntryRecording:
		return entry.Why
	}
	return entry.Why
}
