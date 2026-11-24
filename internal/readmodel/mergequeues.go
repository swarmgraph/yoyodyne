package readmodel

// The merge queues, as every surface shows them: each queue by its target
// branch, each entry in the order it is worked, named by its work item's title
// and identifier, with the mode it was admitted in, where it stands, and why.
// The order is the one the queue's rules last chose in, as its worker's last
// pass recorded it, with the rule that put the first entry first; a queue whose
// last pass recorded no choice shows its entries in admission order.
//
// Where an entry stands is read off the queue's own records and nothing else:
// its generations, its landing record, and its handback. A change is never
// shown as landed because it was approved, or because its candidate passed its
// gate — only a landing the queue confirmed on the target is "landed" — and a
// merge asked of the forge whose outcome nothing has established is shown as
// uncertain rather than as either.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueues is the queue records the read model projects. It is satisfied
// by *runstate.MergeQueueStore.
type MergeQueues interface {
	Keys() ([]runstate.MergeQueueKey, error)
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	Generations(key runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error)
	Landing(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error)
	Refusal(key runstate.MergeQueueKey) (runstate.MergeQueueRefusal, bool, error)
	LastPass(key runstate.MergeQueueKey) (runstate.MergeQueuePass, bool, error)
	// ForgeReading is the latest reading of what GitHub said the entry's merge
	// was waiting on (runstate's mergequeueforgereading.go).
	ForgeReading(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueForgeReading, bool, error)
}

// QueueEntryState is where one queued change stands, from a closed vocabulary
// a surface can count by.
type QueueEntryState string

const (
	// QueueEntryWaiting is a change with no candidate under verification:
	// waiting its turn, or about to be built afresh.
	QueueEntryWaiting QueueEntryState = "waiting"
	// QueueEntryVerifying is a candidate whose checks or review have not ended.
	QueueEntryVerifying QueueEntryState = "verifying"
	// QueueEntryFailed is a candidate that did not earn its gate, not yet
	// read for whose failure it is.
	QueueEntryFailed QueueEntryState = "failed"
	// QueueEntryTargetRed is a candidate whose failing check fails on the
	// target at its base too: it waits on the target being made to pass.
	QueueEntryTargetRed QueueEntryState = "waiting-on-target"
	// QueueEntryVerified is a candidate that earned its gate and has not begun
	// to land.
	QueueEntryVerified QueueEntryState = "verified"
	// QueueEntryLanding is a promotion under way.
	QueueEntryLanding QueueEntryState = "landing"
	// QueueEntryForgeHolds is a merge the forge accepted and holds.
	QueueEntryForgeHolds QueueEntryState = "forge-holds"
	// QueueEntryAwaitsForge is a candidate whose pull request is open and whose
	// merge is asked for once GitHub is ready to make it.
	QueueEntryAwaitsForge QueueEntryState = "awaits-forge"
	// QueueEntryUncertain is something asked of the target or the forge whose
	// outcome nothing has established yet.
	QueueEntryUncertain QueueEntryState = "uncertain"
	// QueueEntryLanded is a landing the queue confirmed on the target, with its
	// record on the run and the work item made.
	QueueEntryLanded QueueEntryState = "landed"
	// QueueEntryRecording is a confirmed landing whose record on the run or the
	// work item is still to be made.
	QueueEntryRecording QueueEntryState = "landed-recording"
	// QueueEntryHandedBack is a change handed back: to its run for repair, or
	// to the development manager.
	QueueEntryHandedBack QueueEntryState = "handed-back"
	// QueueEntryReleased is an entry that left the queue after its merge was
	// withdrawn for something other than a defect, such as a move to the other
	// queue mode.
	QueueEntryReleased QueueEntryState = "released"
	// QueueEntrySetAside is an entry somebody set aside by a decision: it left
	// the queue with nothing written to its run's record, and its run is on the
	// development manager's list of stopped runs.
	QueueEntrySetAside QueueEntryState = "set-aside"
)

// Label is the state in ordinary words, as `yoyo status` and the dashboard
// both print it. Only a landing the queue confirmed reads "landed"; an
// approved or verified change that has not landed never does.
func (s QueueEntryState) Label() string {
	switch s {
	case QueueEntryWaiting:
		return "waiting"
	case QueueEntryVerifying:
		return "being verified"
	case QueueEntryFailed:
		return "did not pass"
	case QueueEntryTargetRed:
		return "waiting on the target"
	case QueueEntryVerified:
		return "verified, not landed yet"
	case QueueEntryLanding:
		return "landing, not confirmed yet"
	case QueueEntryForgeHolds:
		return "held by the forge, not landed yet"
	case QueueEntryAwaitsForge:
		return "waiting for GitHub to be ready to merge it, not landed yet"
	case QueueEntryUncertain:
		return "uncertain whether it landed"
	case QueueEntryLanded:
		return "landed"
	case QueueEntryRecording:
		return "landed, record not finished"
	case QueueEntryHandedBack:
		return "handed back"
	case QueueEntryReleased:
		return "left the queue"
	case QueueEntrySetAside:
		return "set aside"
	}
	return string(s)
}

// stepWords is one step of a landing in ordinary words.
func stepWords(step runstate.MergeQueueMutation, target string, pullRequest int) string {
	switch step {
	case runstate.MergeQueueMoveTarget:
		return "moving " + target + " onto the candidate"
	case runstate.MergeQueuePushCandidate:
		return "pushing the candidate to the forge"
	case runstate.MergeQueueOpenPullRequest:
		return "opening the pull request that carries the candidate"
	case runstate.MergeQueueRequestMerge:
		if pullRequest > 0 {
			return fmt.Sprintf("asking the forge to merge pull request %d", pullRequest)
		}
		return "asking the forge to merge the candidate's pull request"
	case runstate.MergeQueueFollowTarget:
		return "bringing the local " + target + " up to the forge's"
	}
	return "a step this build does not know"
}

// pathWords is how a landing attempt lands, in ordinary words.
func pathWords(path runstate.MergeQueueLandingPath, target string) string {
	switch path {
	case runstate.MergeQueueLandLocally:
		return "moving " + target + " here"
	case runstate.MergeQueueLandLocallyThenPullRequest:
		return "moving " + target + " here and then merging its pull request"
	case runstate.MergeQueueLandThroughPullRequest:
		return "through a pull request the forge merges"
	case runstate.MergeQueueLandThroughForgeQueue:
		return "through the forge's own merge queue"
	}
	return "by a way this build does not know"
}

// handbackWords is what became of a change handed back, in ordinary words.
func handbackWords(continuation runstate.MergeQueueContinuation) string {
	switch continuation {
	case runstate.MergeQueueContinueRepair:
		return "sent back to its run for repair"
	case runstate.MergeQueueBudgetExhausted:
		return "sent back with its repair attempts used up"
	case runstate.MergeQueueMissingPrerequisite:
		return "sent back, but its run no longer has what a repair starts from"
	case runstate.MergeQueueUnattributed:
		return "sent back, but no single change can be named as the cause"
	}
	return "sent back"
}

// modeWords is a queue mode in ordinary words.
func modeWords(mode runstate.MergeQueueMode) string {
	switch mode {
	case runstate.MergeQueueHarness:
		return "the harness's own queue"
	case runstate.MergeQueueForge:
		return "the forge's merge queue"
	}
	return "a queue this build does not know"
}

// passLabel is what a pass came to as every surface labels it: a pass waiting
// because a step could not reach GitHub says so, and when the queue next tries
// is in what the pass says (runstate.MergeQueueGitHubWait.Says).
func passLabel(pass runstate.MergeQueuePass) string {
	if pass.Outcome == runstate.MergeQueuePassWaiting && pass.GitHubWait != nil {
		return "waiting for GitHub to be reachable again"
	}
	return passWords(pass.Outcome)
}

// passWords is what a pass of the queue's worker came to, in ordinary words.
func passWords(outcome runstate.MergeQueuePassOutcome) string {
	switch outcome {
	case runstate.MergeQueuePassIdle:
		return "found nothing waiting"
	case runstate.MergeQueuePassWaiting:
		return "waiting"
	case runstate.MergeQueuePassLanded:
		return "landed a change"
	case runstate.MergeQueuePassHandedBack:
		return "handed a change back"
	case runstate.MergeQueuePassStopped:
		return "stopped on something somebody has to look at"
	}
	return "an outcome this build does not know"
}

// QueueEntryStanding is one queued change.
type QueueEntryStanding struct {
	EntryID    string                  `json:"entry_id"`
	Order      uint64                  `json:"order"`
	Place      uint64                  `json:"place"`
	WorkItemID string                  `json:"work_item_id"`
	Title      string                  `json:"title"`
	RunID      string                  `json:"run_id"`
	Mode       runstate.MergeQueueMode `json:"mode"`
	// ModeLabel is Mode in the words every surface prints for it.
	ModeLabel string `json:"mode_label"`
	// ModeWhy is why the entry is in its mode, as recorded when it was admitted.
	ModeWhy string          `json:"mode_why,omitempty"`
	State   QueueEntryState `json:"state"`
	// Label is State in the words every surface prints for it.
	Label string `json:"label"`
	// Why says where the change stands, in ordinary words.
	Why string `json:"why"`
	// Generation is the candidate the entry stands on, where it has one.
	Generation uint64 `json:"generation,omitempty"`
	// Landed is the commit a confirmed landing put on the target.
	Landed     string `json:"landed,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`
	// SetAsideBy and SetAsideWhy are who set the entry aside and why, for an
	// entry somebody set aside by a decision.
	SetAsideBy  string `json:"set_aside_by,omitempty"`
	SetAsideWhy string `json:"set_aside_why,omitempty"`
	// Step is what the queue is doing with the entry now, in ordinary words,
	// and StepSince when that step began; both are empty for an entry the
	// queue is done with, and StepSince where no record says when it began
	// (mergequeuesteps.go).
	Step      string     `json:"step,omitempty"`
	StepSince *time.Time `json:"step_since,omitempty"`
	// Overdue is set where the entry is a problem: its step has run past the
	// bound, or GitHub will not make its merge by itself.
	Overdue *QueueEntryOverdue `json:"overdue,omitempty"`

	// What the steps around this entry are read from, and never shown: when
	// it left the queue, when its newest candidate was built, and the rule that
	// chose that candidate; and why GitHub will not make its merge, where it
	// will not.
	left           time.Time
	built          time.Time
	selection      *runstate.MergeQueueSelection
	forgeWontMerge string
}

// StepSays is the entry's step and since when, as every surface prints it,
// and empty for an entry with no step.
func (e QueueEntryStanding) StepSays() string {
	switch {
	case e.Step == "":
		return ""
	case e.StepSince == nil:
		return e.Step + ", since a moment no record keeps"
	}
	return e.Step + ", since " + localMoment(*e.StepSince)
}

// Finished reports an entry the queue is done with.
func (e QueueEntryStanding) Finished() bool {
	switch e.State {
	case QueueEntryLanded, QueueEntryReleased, QueueEntrySetAside:
		return true
	}
	return false
}

// MergeQueueStanding is one queue: a repository's target branch.
type MergeQueueStanding struct {
	Repository   string `json:"repository"`
	TargetBranch string `json:"target_branch"`
	// Entries are the entries the queue has not finished with, in the order
	// they are worked, and the few it finished most recently after them.
	Entries []QueueEntryStanding `json:"entries"`
	// Open counts the entries the queue has not finished with, and Finished
	// every entry it is done with, shown or not.
	Open     int `json:"open"`
	Finished int `json:"finished"`
	// Refusal is the queue's last refusal to admit a change, where it has one
	// standing: the switch is on and the queue is not in use.
	Refusal *runstate.MergeQueueRefusal `json:"refusal,omitempty"`
	// LastPass is what the queue's worker last found.
	LastPass *runstate.MergeQueuePass `json:"last_pass,omitempty"`
	// LastPassLabel is the last pass's outcome in the words every surface
	// prints for it.
	LastPassLabel string `json:"last_pass_label,omitempty"`
	// FirstRule and FirstWhy are the rule that put the first waiting entry
	// first, and why in ordinary words, as the queue's last choice recorded
	// them; FirstEntry is that entry. All are empty where no choice is recorded.
	FirstEntry string `json:"first_entry,omitempty"`
	FirstRule  string `json:"first_rule,omitempty"`
	FirstWhy   string `json:"first_why,omitempty"`
}

// recentlyFinishedShown bounds the finished entries a queue shows.
const recentlyFinishedShown = 3

// ReadMergeQueues is every queue the product has, and what could not be read,
// with no entry judged against the bound on its step.
func ReadMergeQueues(queues MergeQueues) ([]MergeQueueStanding, string) {
	return ReadMergeQueuesAt(queues, time.Time{}, 0)
}

// ReadMergeQueuesAt is every queue the product has as of now, with each entry
// whose step has run past overdueAfter, or whose merge GitHub will not make by
// itself, marked as a problem. A zero overdueAfter takes
// DefaultMergeQueueStepOverdueAfter, and a zero now marks nothing.
func ReadMergeQueuesAt(queues MergeQueues, now time.Time, overdueAfter time.Duration) ([]MergeQueueStanding, string) {
	if queues == nil {
		return nil, ""
	}
	// A queue whose record cannot be read is named in the error beside every
	// queue that could be, and is said rather than left out.
	keys, err := queues.Keys()
	var standings []MergeQueueStanding
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the merge queues could not all be listed: %v", err))
	}
	for _, key := range keys {
		standing, problem := readMergeQueue(queues, key, now, overdueAfter)
		standings = append(standings, standing)
		if problem != "" {
			problems = append(problems, problem)
		}
	}
	return standings, strings.Join(problems, "; ")
}

func readMergeQueue(queues MergeQueues, key runstate.MergeQueueKey, now time.Time, overdueAfter time.Duration) (MergeQueueStanding, string) {
	standing := MergeQueueStanding{Repository: key.Repository, TargetBranch: key.TargetBranch}
	var problems []string
	if refusal, found, err := queues.Refusal(key); err != nil {
		problems = append(problems, fmt.Sprintf("why the queue for %s refused admission could not be read: %v", key.TargetBranch, err))
	} else if found {
		standing.Refusal = &refusal
	}
	if pass, found, err := queues.LastPass(key); err != nil {
		problems = append(problems, fmt.Sprintf("the last pass of the queue for %s could not be read: %v", key.TargetBranch, err))
	} else if found {
		standing.LastPass, standing.LastPassLabel = &pass, passLabel(pass)
	}
	entries, err := queues.Entries(key)
	if err != nil {
		problems = append(problems, fmt.Sprintf("the queue for %s could not be read: %v", key.TargetBranch, err))
		return standing, strings.Join(problems, "; ")
	}
	var open, finished []QueueEntryStanding
	var left time.Time
	for _, entry := range runstate.MergeQueueWorkOrder(entries) {
		read, problem := readQueueEntry(queues, key, entry)
		read.Label, read.ModeLabel = read.State.Label(), modeWords(read.Mode)
		if problem != "" {
			problems = append(problems, problem)
		}
		if read.left.After(left) {
			left = read.left
		}
		if read.Finished() {
			finished = append(finished, read)
			continue
		}
		open = append(open, read)
	}
	if pass := standing.LastPass; pass != nil && pass.Order != nil {
		open = inChosenOrder(open, pass.Order.Entries)
		if len(open) > 0 && len(pass.Order.Entries) > 0 && open[0].EntryID == pass.Order.Entries[0] {
			standing.FirstEntry, standing.FirstRule, standing.FirstWhy = open[0].EntryID, pass.Order.Rule, pass.Order.Why
		}
	}
	placeTheHead(open, left, standing)
	overdueSteps(&standing, open, now, overdueAfter)
	standing.Open, standing.Finished = len(open), len(finished)
	if len(finished) > recentlyFinishedShown {
		finished = finished[len(finished)-recentlyFinishedShown:]
	}
	standing.Entries = append(open, finished...)
	return standing, strings.Join(problems, "; ")
}

// inChosenOrder is the open entries in the order the queue's rules last chose,
// and after them, in the order they are worked, any the choice did not rank,
// such as one that joined since.
func inChosenOrder(open []QueueEntryStanding, order []string) []QueueEntryStanding {
	place := make(map[string]int, len(order))
	for index, id := range order {
		if _, seen := place[id]; !seen {
			place[id] = index
		}
	}
	ordered := append([]QueueEntryStanding(nil), open...)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, iRanked := place[ordered[i].EntryID]
		pj, jRanked := place[ordered[j].EntryID]
		switch {
		case iRanked && jRanked:
			return pi < pj
		default:
			return iRanked && !jRanked
		}
	})
	return ordered
}

func readQueueEntry(queues MergeQueues, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry) (QueueEntryStanding, string) {
	read := QueueEntryStanding{
		EntryID: entry.EntryID, Order: entry.Order, Place: entry.WorkPlace(), WorkItemID: entry.WorkItemID,
		Title: entry.WorkItemTitle, RunID: entry.RunID, Mode: entry.Mode, ModeWhy: entry.ModeEvidence.Explanation,
		Supersedes: entry.Supersedes, State: QueueEntryWaiting,
	}
	landing, landed, err := queues.Landing(key, entry.EntryID)
	if err != nil {
		read.State, read.Why = QueueEntryUncertain, "its landing record could not be read, so nothing is said about whether it landed"
		return read, fmt.Sprintf("the landing record of %s could not be read: %v", entry.EntryID, err)
	}
	if landed && placedByLanding(&read, landing) {
		if read.State != QueueEntryForgeHolds && read.State != QueueEntryAwaitsForge {
			setStep(&read, entry, landing, nil, nil)
			return read, ""
		}
		// What GitHub last said the merge is waiting on, whether it holds it or is
		// not ready for it to be asked for, or that it
		// could not be asked, is said beside it.
		reading, found, err := queues.ForgeReading(key, entry.EntryID)
		if err != nil {
			setStep(&read, entry, landing, nil, nil)
			return read, fmt.Sprintf("what GitHub last said about %s could not be read: %v", entry.EntryID, err)
		}
		if attempt, _ := landing.Current(); found && reading.PullRequest == attempt.PullRequest {
			read.Why += "; " + reading.Says()
			setStep(&read, entry, landing, nil, &reading)
			return read, ""
		}
		setStep(&read, entry, landing, nil, nil)
		return read, ""
	}
	generations, err := queues.Generations(key, entry.EntryID)
	if err != nil {
		read.State, read.Why = QueueEntryUncertain, "its candidate's record could not be read"
		return read, fmt.Sprintf("the candidates of %s could not be read: %v", entry.EntryID, err)
	}
	placeByCandidate(&read, generations)
	setStep(&read, entry, landing, generations, nil)
	return read, ""
}

// placedByLanding places an entry from its landing record, and reports false
// where the record says nothing that places it.
func placedByLanding(read *QueueEntryStanding, landing runstate.MergeQueueLanding) bool {
	if completion := landing.Completion; completion != nil {
		read.Landed, read.Generation = completion.Landed, completion.Generation
		if completion.Whole() {
			read.State, read.Why = QueueEntryLanded, fmt.Sprintf("landed on %s as %s, confirmed on the target", landing.TargetBranch, shortened(completion.Landed))
		} else {
			read.State, read.Why = QueueEntryRecording, fmt.Sprintf("landed on %s as %s; recording the landing on the run and the work item is not finished", landing.TargetBranch, shortened(completion.Landed))
		}
		return true
	}
	if handback := landing.Handback; handback != nil {
		read.Generation = handback.Generation
		if handback.Continuation == runstate.MergeQueueLandedOutside {
			read.Landed = handback.TargetBase
			read.State, read.Why = QueueEntryLanded, handback.Reason
			if handback.HandedBackAt == nil {
				read.State, read.Why = QueueEntryRecording, handback.Reason+"; recording the landing on the run and the work item is not finished"
			}
			return true
		}
		if handback.DecidedSetAside() {
			read.State, read.SetAsideBy, read.SetAsideWhy = QueueEntrySetAside, handback.SetAsideBy, handback.SetAside
			listed := "is on the development manager's list of stopped runs"
			if handback.DocketedAt == nil {
				listed = "is not on the development manager's list of stopped runs yet; the queue's next pass puts it there"
			}
			read.Why = fmt.Sprintf("set aside by %s: %s; nothing was written to run %s, which keeps its approved commit %s and its approval, and %s",
				handback.SetAsideBy, handback.SetAside, read.RunID, shortened(handback.ApprovedHead), listed)
			return true
		}
		defer func() {
			if handback.IsSetAside() {
				read.Why = fmt.Sprintf("%s; set aside: %s", read.Why, handback.SetAside)
			}
		}()
		if handback.Continuation == runstate.MergeQueueReleased {
			read.State, read.Why = QueueEntryReleased, handback.Reason
			if handback.TransferTo != "" {
				read.Why = fmt.Sprintf("moved to %s; %s", modeWords(handback.TransferTo), handback.Reason)
			}
			return true
		}
		read.State, read.Why = QueueEntryHandedBack, fmt.Sprintf("%s; the next move is %s: %s", handbackWords(handback.Continuation), handback.Mover.Possessive(), handback.Reason)
		return true
	}
	attempt, attempted := landing.Current()
	if !attempted || attempt.SetAside != nil {
		return false
	}
	read.Generation = attempt.Generation
	if record, unsettled := attempt.Unsettled(); unsettled {
		read.State, read.Why = QueueEntryUncertain, fmt.Sprintf("landing attempt %d asked for %s, and nothing has established what became of it yet", attempt.Number, stepWords(record.Mutation, landing.TargetBranch, attempt.PullRequest))
		return true
	}
	if withdrawal := attempt.Withdrawal; withdrawal != nil && withdrawal.Settled == nil {
		read.State, read.Why = QueueEntryUncertain, fmt.Sprintf("taking back the merge of pull request %d is not confirmed, so the forge may still land it", withdrawal.PullRequest)
		return true
	}
	if attempt.Landed != nil {
		read.State, read.Landed, read.Why = QueueEntryRecording, attempt.Landed.Commit, fmt.Sprintf("landed as %s; recording the landing is not finished", shortened(attempt.Landed.Commit))
		return true
	}
	if requested, asked := attempt.Mutation(runstate.MergeQueueRequestMerge); asked && requested.Settled != nil && requested.Settled.Result == runstate.MergeQueueMutationQueued {
		read.State, read.Why = QueueEntryForgeHolds, fmt.Sprintf("the forge holds the merge of pull request %d until its own requirements are met; nothing has landed yet", attempt.PullRequest)
		return true
	}
	if n := len(attempt.Mutations); n > 0 && attempt.Next() == runstate.MergeQueueRequestMerge && attempt.PullRequest > 0 &&
		attempt.Mutations[n-1].Settled != nil && attempt.Mutations[n-1].Settled.Result == runstate.MergeQueueMutationDone {
		read.State, read.Why = QueueEntryAwaitsForge, fmt.Sprintf("pull request %d is open, and its merge is asked for once GitHub is ready to make it and %s is still at the candidate's base; nothing has landed yet", attempt.PullRequest, landing.TargetBranch)
		return true
	}
	read.State, read.Why = QueueEntryLanding, fmt.Sprintf("landing attempt %d is under way, %s", attempt.Number, pathWords(attempt.Path, landing.TargetBranch))
	return true
}

// placeByCandidate places an entry nothing is landing from its newest
// candidate.
func placeByCandidate(read *QueueEntryStanding, generations []runstate.MergeQueueGeneration) {
	if len(generations) == 0 {
		read.Why = "waiting for its turn; no candidate has been built for it yet"
		return
	}
	latest := generations[len(generations)-1]
	read.Generation = latest.Number
	configured := latest.Checks
	switch {
	case latest.Invalidated != nil:
		read.Why = fmt.Sprintf("candidate %d stopped being one that could land (%s); it is built again on the target as it stands", latest.Number, latest.Invalidated.Reason)
	case latest.WaitsOnTarget():
		base := latest.BaseCheck
		read.State = QueueEntryTargetRed
		read.Why = fmt.Sprintf("%s fails on %s at %s as well as on candidate %d, so it waits on the target being made to pass it", base.Command, latest.TargetBranch, shortened(latest.TargetBase), latest.Number)
		if base.RedItem != "" {
			read.Why += " (" + base.RedItem + ")"
		}
	case latest.Gate(configured) == nil:
		read.State, read.Why = QueueEntryVerified, fmt.Sprintf("candidate %d passed its checks and its independent review and has not begun to land", latest.Number)
	case latest.Paths != nil && len(latest.Paths.Refused) > 0:
		read.State, read.Why = QueueEntryFailed, fmt.Sprintf("candidate %d changes protected paths its work item does not grant (%s)", latest.Number, strings.Join(latest.Paths.Refused, ", "))
	case latest.CheckRun == nil || latest.CheckRun.FinishedAt == nil:
		read.State, read.Why = QueueEntryVerifying, fmt.Sprintf("candidate %d's checks have not finished", latest.Number)
	case latest.CheckRun.Problem == "" && !candidateChecksPassed(latest):
		read.State, read.Why = QueueEntryFailed, fmt.Sprintf("candidate %d did not pass its checks: %s", latest.Number, latest.Gate(configured))
	case latest.Review == nil || latest.Review.FinishedAt == nil:
		read.State, read.Why = QueueEntryVerifying, fmt.Sprintf("candidate %d passed its checks and its review has not finished", latest.Number)
	case latest.Review.Problem == "" && latest.Review.Decision != "approve":
		read.State, read.Why = QueueEntryFailed, fmt.Sprintf("candidate %d's reviewer decided %q", latest.Number, latest.Review.Decision)
	default:
		read.State, read.Why = QueueEntryVerifying, fmt.Sprintf("candidate %d is being verified again: %s", latest.Number, latest.Gate(configured))
	}
}

func candidateChecksPassed(generation runstate.MergeQueueGeneration) bool {
	run := generation.CheckRun
	if run == nil || len(run.Results) != len(generation.Checks.Commands) {
		return false
	}
	for _, result := range run.Results {
		if !result.Passed || result.CouldNotRun != "" {
			return false
		}
	}
	return true
}

func shortened(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// RenderMergeQueues is one block per queue, printed under the four lines: the
// queue's last refusal where one stands, what its worker last found, and one
// line per entry naming the work by identifier and title.
func (s Standing) RenderMergeQueues() string {
	if len(s.MergeQueues) == 0 && s.MergeQueuesProblem == "" {
		return ""
	}
	var rendered strings.Builder
	for _, queue := range s.MergeQueues {
		fmt.Fprintf(&rendered, "Merge queue for %s: %d waiting to land, %d finished\n", queue.TargetBranch, queue.Open, queue.Finished)
		if refusal := queue.Refusal; refusal != nil {
			fmt.Fprintf(&rendered, "  not in use: %s What would let it be used: %s.\n", refusal.Requirement, refusal.Step)
		}
		if pass := queue.LastPass; pass != nil && pass.Says != "" {
			fmt.Fprintf(&rendered, "  last pass: %s — %s\n", queue.LastPassLabel, pass.Says)
		}
		if queue.FirstRule != "" {
			fmt.Fprintf(&rendered, "  first in the queue's order (%s): %s\n", queue.FirstRule, queue.FirstWhy)
		}
		for _, entry := range queue.Entries {
			fmt.Fprintf(&rendered, "  %d. %s %s — %s, in %s — %s\n", entry.Place, entry.WorkItemID, entry.Title, entry.Label, entry.ModeLabel, entry.Why)
			if step := entry.StepSays(); step != "" {
				fmt.Fprintf(&rendered, "     now: %s\n", step)
			}
			if entry.Overdue != nil {
				fmt.Fprintf(&rendered, "     PROBLEM, the development manager's to move: %s\n", entry.Overdue.Says())
			}
		}
	}
	if s.MergeQueuesProblem != "" {
		rendered.WriteString(partialRead + s.MergeQueuesProblem + "\n")
	}
	return s.wording.Render(rendered.String())
}
