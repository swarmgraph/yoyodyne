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
//
// So is an entry the queue's worker has stopped on the same way on its last
// three passes or more: the same entry, the same words, and nothing else
// recorded between them. A stopped pass is the worker meeting something it
// cannot get past, and every pass already tries again, so the same stop three
// times over is the trying again having changed nothing; no move the harness
// has left will change it either. It reads off the queue's pass history
// (runstate's recordPassHistory) and clears at the first pass that ends any
// other way. It is the operator's instead only where the stopped pass recorded,
// from the type of the error it stopped on, an act only a person can take, and
// then for that reason (ownership's mergeQueueOverdueRule).

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
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
	// StoppedPasses is how many passes in a row the queue's worker stopped on
	// the entry for the same reason, where that is why it is a problem, and
	// zero otherwise; Since is then when the first of them ended, and
	// StoppedBecause the reason, in the words the worker recorded it in.
	StoppedPasses  int    `json:"stopped_passes,omitempty"`
	StoppedBecause string `json:"stopped_because,omitempty"`
	// PersonOnly is the act only a person can take that the stop waits on,
	// where the stopped pass recorded one, which makes the entry the
	// operator's for that reason.
	PersonOnly ownership.PersonOnlyReason `json:"person_only,omitempty"`
	// Mover is whose the entry is to move, and OwnerReason why it is the
	// operator's, in words and empty where it is not his: the ownership
	// registry's answer on the entry, carried for a surface that shows the
	// entry in its queue, where no attention entry is beside it.
	Mover       Mover  `json:"mover"`
	OwnerReason string `json:"owner_reason,omitempty"`
}

// Says is the entry as a sentence: which entry, its step and since when, why
// it is a problem, and what it waits on.
func (o QueueEntryOverdue) Says() string {
	if o.StoppedPasses > 0 {
		return fmt.Sprintf("merge queue entry %d for %s (place %d, %s %s) has stopped the queue the same way on %d passes in a row since %s, and the harness trying again has not changed it; every change behind it waits; it stops because %s",
			o.Order, o.TargetBranch, o.Place, o.WorkItemID, o.Title, o.StoppedPasses, localMoment(o.Since), o.StoppedBecause)
	}
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
// The entry is the ID and its work item the WorkItemID; the registry names its
// mover.
func queueOverdueAttention(overdue QueueEntryOverdue) Attention {
	return resolved(Attention{
		Kind:         AttentionMergeQueueOverdue,
		ID:           overdue.EntryID,
		WorkItemID:   overdue.WorkItemID,
		QueueOverdue: &overdue,
	})
}

// sayOwner records the registry's answer on the entry.
func (o *QueueEntryOverdue) sayOwner() {
	answer := queueOverdueAttention(*o)
	o.Mover, o.OwnerReason = answer.Mover, answer.Ownership.ReasonSays()
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
// the merge it holds or is not ready to make, where one was read.
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
	case QueueEntryForgeHolds, QueueEntryAwaitsForge:
		since := runstate.MergeQueueRequestMerge
		read.Step = fmt.Sprintf("waiting for GitHub to merge pull request %d", attempt.PullRequest)
		if read.State == QueueEntryAwaitsForge {
			since = runstate.MergeQueueOpenPullRequest
			read.Step = fmt.Sprintf("waiting for GitHub to be ready to merge pull request %d", attempt.PullRequest)
		}
		if settled, recorded := attempt.Mutation(since); recorded && settled.Settled != nil {
			at(settled.Settled.At)
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
	// A stop that repeats is a problem whatever the time, and is said ahead of
	// anything else about the entry, because it is why the entry is not moving.
	for index := range open {
		entry := &open[index]
		if stop := entry.repeatedStop; stop != nil {
			waitingOn := "somebody finding and clearing what stops the queue's worker on it, or handing it back"
			if stop.personOnly != "" {
				waitingOn = "the step only a person can take that the stop names; the next pass then goes on by itself"
			}
			entry.Overdue = &QueueEntryOverdue{
				TargetBranch: queue.TargetBranch, EntryID: entry.EntryID, Order: entry.Order, Place: entry.Place,
				WorkItemID: entry.WorkItemID, Title: entry.Title, Step: entry.Step, Since: stop.since,
				WaitingOn:     waitingOn,
				StoppedPasses: stop.passes, StoppedBecause: stop.says, PersonOnly: stop.personOnly,
			}
			entry.Overdue.sayOwner()
		}
	}
	if now.IsZero() {
		return
	}
	if bound <= 0 {
		bound = DefaultMergeQueueStepOverdueAfter
	}
	for index := range open {
		entry := &open[index]
		if entry.Overdue != nil {
			continue
		}
		mark := func(boundSeconds int64, waitingOn string) {
			overdue := QueueEntryOverdue{
				TargetBranch: queue.TargetBranch, EntryID: entry.EntryID, Order: entry.Order, Place: entry.Place,
				WorkItemID: entry.WorkItemID, Title: entry.Title, Step: entry.Step, BoundSeconds: boundSeconds, WaitingOn: waitingOn,
			}
			if entry.StepSince != nil {
				overdue.Since = *entry.StepSince
			}
			overdue.sayOwner()
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
	case QueueEntryAwaitsForge:
		return "GitHub being ready to merge it: " + entry.Why
	case QueueEntryUncertain, QueueEntryLanding, QueueEntryRecording:
		return entry.Why
	}
	return entry.Why
}

// repeatedStopsBeforeProblem is how many passes in a row have to stop on one
// entry for one reason before it is said as a problem: the first stop may be
// passing, and the passes after it are the harness trying again.
const repeatedStopsBeforeProblem = 3

// queueRepeatedStop is the queue's worker stopping on one entry for one reason
// on its latest passes: how many, when the first of them ended, and why.
type queueRepeatedStop struct {
	entryID    string
	passes     int
	since      time.Time
	says       string
	personOnly ownership.PersonOnlyReason
}

// repeatedStopOf is the stop the queue's latest passes all ended on, where at
// least repeatedStopsBeforeProblem of them in a row stopped on the same entry
// for the same reason, and nil otherwise. Any other pass breaks the run, so a
// pass that gets past the stop, or stops on something else, clears it. The
// reason is said in the latest pass's own words where that pass is the newest
// in the history, because the history keeps each pass's words shortened.
func repeatedStopOf(history []runstate.MergeQueuePass, latest *runstate.MergeQueuePass) *queueRepeatedStop {
	if len(history) == 0 {
		return nil
	}
	newest := history[len(history)-1]
	if newest.Outcome != runstate.MergeQueuePassStopped || newest.EntryID == "" {
		return nil
	}
	first := len(history) - 1
	for first > 0 {
		before := history[first-1]
		if before.Outcome != newest.Outcome || before.EntryID != newest.EntryID || before.Says != newest.Says || before.PersonOnly != newest.PersonOnly {
			break
		}
		first--
	}
	passes := len(history) - first
	if passes < repeatedStopsBeforeProblem {
		return nil
	}
	says := newest.Says
	if latest != nil && latest.At.Equal(newest.At) && latest.EntryID == newest.EntryID && latest.Says != "" {
		says = latest.Says
	}
	if strings.TrimSpace(says) == "" {
		says = "of something its pass recorded no words for"
	}
	return &queueRepeatedStop{entryID: newest.EntryID, passes: passes, since: history[first].At.UTC(), says: says, personOnly: newest.PersonOnly}
}
