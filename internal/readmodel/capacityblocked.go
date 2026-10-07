package readmodel

// What is parked or held on provider capacity, one run and one conversation at
// a time.
//
// The hold beside this says the one sentence a whole-machine stoppage deserves:
// every role refused at once. It is deliberately silent about anything smaller,
// and so was everything else. A run past its wait budget stops with a generic
// blocker on its item, and the record it leaves reads like every other
// stoppage until somebody opens it. A run asleep on a reset the provider named
// is in flight and is counted as running, which is true and is not what an
// operator asking why nothing is landing wants told. A conversation the
// provider refused fails at whoever's terminal asked for it and is said once
// in the channel, and a second reading of the log finds each refusal but no
// state.
//
// So this is the capacity-blocked state the observability design asks the
// read model for: which runs and which conversations are parked or held on
// provider capacity, since when, until when where the provider said, and what
// a person can do about each. It is derived once, here, from the durable run
// records and the refusal log, for every surface that will say it — the
// machine-readable status first, and the capacity panel after that — so a
// dashboard and a script cannot come to count the parked runs differently.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CapacityState is where one run or conversation stands against provider
// capacity, in the vocabulary the observability design fixes for the capacity
// query. Only the two states this reading can name are here: a run or a
// conversation nothing has refused is not listed rather than listed as healthy,
// because the list is what is parked or held and an entry saying "nothing is
// wrong with this one" would be the residual bucket the four lines refuse.
//
// It is owned here, by the read model, so that no surface redeclares it: the
// panel and the command read these words rather than their own.
type CapacityState string

const (
	// CapacityStateWaiting is a run asleep on a recorded deadline. It is still in
	// flight, keeps its claim, its branch, and its worktree, and asks the
	// provider again by itself; nothing about it is waiting on a person.
	CapacityStateWaiting CapacityState = "waiting"
	// CapacityStateBlocked is a run the provider refused and the harness would not
	// wait for, and a conversation whose turn the provider refused with nothing
	// serving it while that refusal still stands. A run stopped by a usage window
	// resetting past the configured maximum pause gave its item back to the queue
	// and is pulled again once the window resets; one refused on a reset that
	// was not in the future, or on an overload that outlasted the budget, handed
	// its item to a person. None of them carries on by itself.
	CapacityStateBlocked CapacityState = "capacity-blocked"
)

// CapacityBlockedRun is one run parked or held on provider capacity.
type CapacityBlockedRun struct {
	RunID      string         `json:"run_id"`
	WorkItemID string         `json:"work_item_id"`
	Phase      runstate.Phase `json:"phase,omitempty"`
	State      CapacityState  `json:"state"`
	// RefusedBy is what the provider refused the run with, in the words a paused
	// run's cause is written in everywhere else: an exhausted usage limit, named
	// by the provider's own name for it where it gave one, or a transient
	// server overload.
	RefusedBy string `json:"refused_by"`
	// Since is when the run started waiting. For a blocked run that is the moment
	// it stopped. For a waiting run it is when its pause began where the record
	// kept that, and otherwise — every record written before the start was
	// carried — the start of the probe it is sleeping, because each probe
	// re-records the wait; WaitedSeconds below is how long the run has spent
	// waiting in total, which is the figure the maximum pause is measured
	// against.
	Since time.Time `json:"since"`
	// ResetsAt is the deadline the run is waiting out, and nil where there is
	// none: a run that stopped rather than waited recorded no deadline, and one
	// the provider gave no reset time waits the configured probe interval
	// instead. Nil is a different fact from a reset that has passed.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// WaitedSeconds is how much of the run's pause budget has been committed
	// across every wait it has taken, in the whole seconds the record keeps it
	// in and with the unit in the key: a script reading this shape gets a
	// number it can compare with execution.usage_limit_max_pause rather than a
	// bare integer of nanoseconds.
	WaitedSeconds int64 `json:"waited_seconds"`
	// Preserved reports the run's change surviving: a branch or a worktree the
	// harness has not recorded as removed. A waiting run holds everything it has
	// by definition; on a blocked run it is the difference between work somebody
	// can pick up and work that has to be done again.
	Preserved bool `json:"preserved"`
	// Remedy is what a person can do about it, or that nothing needs doing.
	Remedy string `json:"remedy"`
}

// CapacityBlockedConversation is one conversation the provider has refused
// and is still refusing, as far as the log can say.
type CapacityBlockedConversation struct {
	ConversationID string        `json:"conversation_id"`
	State          CapacityState `json:"state"`
	// Waiting is the log's own account of what was stopped — the role and the
	// conversation, in a sentence — rather than a paraphrase of it.
	Waiting string `json:"waiting"`
	// RefusedBy is what the provider refused the turn with, in the same words a
	// run's cause above is written in.
	RefusedBy string `json:"refused_by"`
	// Model is the model the latest standing refusal names, and empty where the
	// process that recorded it did not say — which is every refusal recorded
	// before the model was carried.
	Model string `json:"model,omitempty"`
	// Since is the earliest standing refusal of this conversation, which is when
	// the provider started refusing it as far as the log can say.
	Since time.Time `json:"since"`
	// ResetsAt is the latest reset any standing refusal named, and nil where the
	// provider named none: the refusal then stands for the configured probe
	// interval, and the next turn is what finds out whether it has lifted.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// Refusals is how many turns of this conversation the provider has stopped
	// and is still refusing. A turn an alternate served through is not one.
	Refusals int `json:"refusals"`
	// Remedy is what a person can do about it.
	Remedy string `json:"remedy"`
}

// CapacityBlocked is the capacity-blocked state: everything parked or held on
// provider capacity at the moment asked about. Both lists are always present
// and empty rather than absent when nothing is held, because a script reading
// this has to tell "nothing is held" from "the list was left out"; a source
// that could not be read says so in its problem instead of reporting an empty
// list, which is the rule every line of the standing status keeps.
type CapacityBlocked struct {
	Runs        []CapacityBlockedRun `json:"runs"`
	RunsProblem string               `json:"runs_problem,omitempty"`

	Conversations        []CapacityBlockedConversation `json:"conversations"`
	ConversationsProblem string                        `json:"conversations_problem,omitempty"`
}

// Held reports anything parked or held at all.
func (c CapacityBlocked) Held() bool {
	return len(c.Runs) > 0 || len(c.Conversations) > 0
}

// The remedies, each written once so every surface says the same words about
// what a person can do. The waiting run's is the one that says nothing needs
// doing, and says so in words: a list of things parked on capacity is read by
// somebody looking for what to fix, and an entry with no remedy reads as an
// entry nobody wrote one for.
const (
	waitingRunRemedy  = "nothing needs doing: the run asks the provider again by itself at its next probe and carries on once it is served; `yoyo resume` with the work item named asks now instead of at the probe"
	blockedRunRemedy  = "the run stopped and its item is back with the development manager; what lets it run again is more provider capacity, a longer execution.usage_limit_max_pause, or a replan"
	windowRunRemedy   = "nothing needs doing: the run stopped without anything being judged and gave its item back to the queue, and a watching session pulls it again once the window resets; more provider capacity or a longer execution.usage_limit_max_pause is what would have let it wait instead"
	refusedTurnRemedy = "nothing waits on it: the turn failed where it was asked for, and asking again once the window lifts is what serves it; enabling failover on the agent is what would move the next turn onto another model before then"
)

// ReadCapacityBlocked derives the capacity-blocked state from records already
// read: every run the product holds, and every refusal the provider has
// recorded outside a run.
//
// A run is waiting when it is in flight and carries a recorded deadline for an
// exhausted usage limit or a server overload. A run waiting on a provider
// outage shares the deadline field and is not here: a login nobody renewed is
// not capacity, and the outage reading says it. A run is blocked when it ended
// with a durable blocker while still recording one of those two causes, which
// is what the pipeline leaves when it refuses a wait: a run's ending clears
// every pause but keeps that cause on a run stopped as a usage-limit stop, so
// the record says which refusal stopped it. The cause is cleared with the
// deadline on every run that resumed and on every other ending, so a run that
// paused once and later stopped on something else records no cause and is not
// here either. A
// run the provider's usage window stopped is blocked too, read from the
// environmental refusal it ended on, which is what carries its reset.
//
// One run per work item, and only the item's latest: a run a later run has
// superseded is history whatever it stopped on. What the run records cannot
// say is whether the item is still admitted, so a blocked run whose item was
// since closed is listed until a later run of that item replaces it.
//
// A conversation is held while a refusal of it stands, on the same reading of
// standing that failover and the whole-machine hold take of the same log: the
// provider's own reset where it named a usable one, and the configured probe
// interval where it did not. A turn an alternate served through is not a
// refusal of the conversation, since the work carried on; an availability
// substitution names no window at all. Refusals that name no conversation — a
// branch review at somebody's terminal — are not conversations and are not
// listed. One entry per conversation, however many turns of it were stopped.
//
// Both are read against the evidence that a window lifted before its quoted
// reset. A refusal recorded before the provider served the same account and
// model is not standing, and neither is a refusal of a conversation its role
// has since replaced — nothing will happen in that one again — so neither lists
// a conversation. A run the provider stopped is not listed once the provider
// has served its account and model since it stopped. A run asleep on its
// deadline is listed whatever the evidence says, because it is still asleep: it
// asks again at its next probe, and `yoyo resume` asks now.
func ReadCapacityBlocked(runs []runstate.State, refusals []runstate.UsageLimitExhaustion, now time.Time, unknownResetPause time.Duration, evidence CapacityEvidence, looks ...Look) CapacityBlocked {
	blocked := CapacityBlocked{
		Runs:          []CapacityBlockedRun{},
		Conversations: []CapacityBlockedConversation{},
	}
	for _, run := range latestRunPerItem(runs) {
		entry, held := capacityBlockedRun(run)
		if !held {
			continue
		}
		if len(looks) > 0 && looks[0] != nil {
			entry.Preserved = looks[0](run).Holds()
		}
		if refusal, named := stoppedRunRefusal(run, entry); named && entry.State == CapacityStateBlocked && evidence.Lifted(refusal) {
			continue
		}
		blocked.Runs = append(blocked.Runs, entry)
	}
	// Sorted by item so two readings of one store list the runs in one order,
	// whatever order the store scanned them in.
	sort.Slice(blocked.Runs, func(i, j int) bool {
		return blocked.Runs[i].WorkItemID < blocked.Runs[j].WorkItemID
	})

	held := map[string]*CapacityBlockedConversation{}
	latest := map[string]time.Time{}
	for _, refusal := range evidence.Standing(refusals) {
		if strings.TrimSpace(refusal.ConversationID) == "" || refusal.Substituted() {
			continue
		}
		if !refusal.WindowClosed(now, unknownResetPause) {
			continue
		}
		entry, seen := held[refusal.ConversationID]
		if !seen {
			entry = &CapacityBlockedConversation{
				ConversationID: refusal.ConversationID,
				State:          CapacityStateBlocked,
				Since:          refusal.At.UTC(),
				Remedy:         refusedTurnRemedy,
			}
			held[refusal.ConversationID] = entry
		}
		entry.Refusals++
		if refusal.At.Before(entry.Since) {
			entry.Since = refusal.At.UTC()
		}
		if refusal.ResetsAt != nil && (entry.ResetsAt == nil || refusal.ResetsAt.After(*entry.ResetsAt)) {
			resetsAt := refusal.ResetsAt.UTC()
			entry.ResetsAt = &resetsAt
		}
		// The words are the latest refusal's, because that is the limit the
		// provider is quoting now and the model it quoted it against.
		if !refusal.At.Before(latest[refusal.ConversationID]) {
			latest[refusal.ConversationID] = refusal.At
			entry.Waiting = strings.TrimSpace(refusal.Waiting)
			entry.RefusedBy = runstate.DescribePause(runstate.PauseUsageLimit, refusal.Kind)
			entry.Model = strings.TrimSpace(refusal.Model)
		}
	}
	for _, entry := range held {
		blocked.Conversations = append(blocked.Conversations, *entry)
	}
	sort.Slice(blocked.Conversations, func(i, j int) bool {
		return blocked.Conversations[i].ConversationID < blocked.Conversations[j].ConversationID
	})
	return blocked
}

// capacityBlockedRun is one run as this reading names it, and whether it is
// parked or held on capacity at all.
func capacityBlockedRun(run runstate.State) (CapacityBlockedRun, bool) {
	if refused := run.Environmental; run.Status.Terminal() && refused != nil && refused.Cause == runstate.CauseUsageWindow {
		return usageWindowRun(run, *refused), true
	}
	if !capacityPause(run.PauseCause, run.UsageLimitResetsAt != nil) {
		return CapacityBlockedRun{}, false
	}
	entry := CapacityBlockedRun{
		RunID:         run.RunID,
		WorkItemID:    run.WorkItemID,
		Phase:         run.Phase,
		RefusedBy:     runstate.DescribePause(run.PauseCause, run.UsageLimitKind),
		Since:         run.UpdatedAt.UTC(),
		WaitedSeconds: run.UsageLimitPausedSeconds,
		Preserved:     run.Artifacts().Preserved(),
	}
	if run.UsageLimitResetsAt != nil {
		resetsAt := run.UsageLimitResetsAt.UTC()
		entry.ResetsAt = &resetsAt
	}
	switch {
	case run.Status.InFlight() && run.UsageLimitResetsAt != nil:
		entry.State = CapacityStateWaiting
		entry.Remedy = waitingRunRemedy
		if run.UsageLimitPausedSince != nil {
			entry.Since = run.UsageLimitPausedSince.UTC()
		}
	case run.Status.Terminal() && strings.TrimSpace(run.Blocker) != "":
		entry.State = CapacityStateBlocked
		entry.Remedy = blockedRunRemedy
		if run.CompletedAt != nil {
			entry.Since = run.CompletedAt.UTC()
		}
	default:
		// A cause with no deadline on a run still going is a record mid-write, and
		// a cause on a run that ended without a blocker is one something other
		// than the refusal ended — a cancellation mid-wait, say. Neither is parked
		// or held on capacity now.
		return CapacityBlockedRun{}, false
	}
	return entry, true
}

// usageWindowRun is a run the provider's usage window stopped, as this reading
// names it. It is read from the refusal the run recorded rather than from its
// pause, because the run ended rather than waited and its ending cleared the
// pause; the refusal is what kept the reset.
func usageWindowRun(run runstate.State, refused runstate.EnvironmentalRefusal) CapacityBlockedRun {
	entry := CapacityBlockedRun{
		RunID:         run.RunID,
		WorkItemID:    run.WorkItemID,
		Phase:         run.Phase,
		State:         CapacityStateBlocked,
		RefusedBy:     runstate.DescribePause(runstate.PauseUsageLimit, run.UsageLimitKind),
		Since:         run.UpdatedAt.UTC(),
		WaitedSeconds: run.UsageLimitPausedSeconds,
		Preserved:     run.Artifacts().Preserved(),
		Remedy:        windowRunRemedy,
	}
	if run.CompletedAt != nil {
		entry.Since = run.CompletedAt.UTC()
	}
	if refused.ResetsAt != nil {
		resetsAt := refused.ResetsAt.UTC()
		entry.ResetsAt = &resetsAt
	}
	return entry
}

// stoppedRunRefusal is a stopped run as the refusal that stopped it, for the
// evidence to be read against: the moment it stopped, the model it recorded as
// refused — or, on a record written before that was carried, the developer's
// model where it stopped developing — and the account it ran under. A run that
// can name no model is not one, and is left listed: a stopped review recorded
// no model at all, and reading it as lifted by whatever was served next would
// clear it on a turn of some other model.
func stoppedRunRefusal(run runstate.State, entry CapacityBlockedRun) (runstate.UsageLimitExhaustion, bool) {
	model := strings.TrimSpace(run.UsageLimitModel)
	if model == "" && run.Phase == runstate.PhaseDeveloping {
		model = strings.TrimSpace(run.ProviderModel)
	}
	if model == "" {
		return runstate.UsageLimitExhaustion{}, false
	}
	return runstate.UsageLimitExhaustion{
		At:           entry.Since,
		WorkItemID:   run.WorkItemID,
		Model:        model,
		AccountAlias: strings.TrimSpace(run.AccountAlias),
	}, true
}

// capacityPause reports a pause cause that is the provider's capacity rather
// than its availability or the operator's hold. The empty cause reads as an
// exhausted usage limit only beside a recorded deadline, which is the reading
// the cause's own record gives it: every deadline written before the cause was
// carried was a usage limit's. Beside no deadline the empty cause is every run
// that never paused, or resumed and had its cause cleared, and a review
// stoppage among them is not the provider's capacity.
func capacityPause(cause string, deadline bool) bool {
	switch cause {
	case runstate.PauseUsageLimit, runstate.PauseServerOverload:
		return true
	case "":
		return deadline
	default:
		return false
	}
}

// latestRunPerItem is each work item's most recent run, whatever became of it,
// so that a run a later one superseded describes nothing. Runs that belong to
// no item are left out: there is no item for them to be parked on.
func latestRunPerItem(runs []runstate.State) map[string]runstate.State {
	latest := make(map[string]runstate.State)
	for _, run := range runs {
		if run.WorkItemID == "" {
			continue
		}
		if previous, seen := latest[run.WorkItemID]; seen && previous.StartedAt.After(run.StartedAt) {
			continue
		}
		latest[run.WorkItemID] = run
	}
	return latest
}

// CapacityBlockedOf reads the capacity-blocked state from a set of sources.
// Each half says why it could not be read where it could not, and a source
// nobody wired says so too: "nothing is held" and "nothing was wired to read
// what is held" are opposite answers.
func CapacityBlockedOf(sources Sources, now time.Time) CapacityBlocked {
	var runs []runstate.State
	var runsProblem string
	switch {
	case sources.Runs == nil:
		runsProblem = "nothing was wired to read the recorded runs"
	default:
		recorded, err := sources.Runs.Recorded()
		if err != nil {
			runsProblem = fmt.Sprintf("the recorded runs could not be read: %v", err)
		}
		runs = recorded
	}
	var refusals []runstate.UsageLimitExhaustion
	var refusalsProblem string
	switch {
	case sources.UsageLimits == nil:
		refusalsProblem = "nothing was wired to read what the provider has refused"
	default:
		listed, err := sources.UsageLimits.List()
		if err != nil {
			refusalsProblem = fmt.Sprintf("what the provider has refused could not be read: %v", err)
		}
		refusals = listed
	}
	evidence, evidenceProblem := CapacityEvidenceOf(sources)
	blocked := ReadCapacityBlocked(runs, refusals, now, sources.UnknownResetPause, evidence, Looking(context.Background(), sources.Remains, func() time.Time { return now }))
	// The evidence clears stopped runs as well as conversations, so a failure to
	// read it is said on both halves: each is a list that may be longer than it
	// would have been.
	blocked.RunsProblem = joinProblems(runsProblem, evidenceProblem)
	blocked.ConversationsProblem = joinProblems(refusalsProblem, evidenceProblem)
	return blocked
}
