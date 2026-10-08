package runstate

// A round the environment handed nothing, and what that costs the item: nothing.
//
// A run's budgets exist to bound the work's own failures. They were being spent
// by the harness's: a round dispatched into a worktree holding none of its
// change delivers an empty diff, and an empty diff spends a review round against
// the item's cap and consumes the repair grant that bought the round. Three
// items advanced toward escalation in one night that way, on rounds that
// delivered exactly what a dead bug handed them, and an escalation produced like
// that reads afterwards as an item nobody could finish.
//
// So the class is durable and it is named. A round is environmental when its
// diff is empty and its run recorded one of the causes below, and the
// conjunction is the whole of the definition. A cause on its own excuses
// nothing: a round that recorded one and still delivered a change spends exactly
// as any other round does. And an empty delivery on its own excuses nothing
// either — with no cause recorded it spends, which is what keeps laziness out of
// the class, and the evidence that tells the two apart is the run's own record.
//
// What the class is worth is decided where a run settles, because that is the
// first point both halves are known. What is recorded here is only the cause,
// written where the harness refuses; the settle reads it, asks the worktree
// whether anything was delivered, and gives back what an environmental round
// must not have spent.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// MaxEnvironmentalDetailBytes bounds the harness's own account of what the
// environment did. It is the harness's words rather than a provider's, so it is
// held to a line rather than to the blocker's bound: what a reader needs is
// which cause it was, and the blocker beside it already carries the long form.
const MaxEnvironmentalDetailBytes = 1 << 10

// MaxEnvironmentalProblemBytes bounds a return the settle decided on and could
// not write. It is a harness failure said in one line, for the same reason.
const MaxEnvironmentalProblemBytes = 1 << 10

// EnvironmentalCause is what the environment did in place of handing a round its
// work. Every one of them is the harness's own failure rather than the work's,
// which is what makes the class a refusal rather than a verdict — the round
// delivered nothing because there was nothing there to deliver from.
//
// The set is closed and small on purpose. An open vocabulary is one every future
// stoppage can be squeezed into, and a class anything can join is a class that
// stops meaning "the harness is answerable for this".
type EnvironmentalCause string

const (
	// CauseHandbackMissingChange is a run picked up again to continue a change,
	// in a worktree that holds none of it. The developer is handed a failure
	// about work that is not there, and delivers an empty change or reinvents
	// one; either way the round is about the seeding rather than about the work.
	CauseHandbackMissingChange EnvironmentalCause = "handback-missing-change"
	// CauseDirtyPrimary is the primary checkout carrying uncommitted state the
	// harness does not own, so the worktree a round needed could not be made from
	// it. What the round was given is not the repository the work is against.
	CauseDirtyPrimary EnvironmentalCause = "dirty-primary"
	// CauseWorktreeCheckoutKilled is a run whose worktree could not be cut
	// because the harness's own budget ended `git worktree add` while it was
	// still writing the tree out. Nothing of the work was reached: no worktree
	// exists, the item was claimed and given straight back, and no agent was ever
	// invoked — so what the round delivered is the machine having been too busy
	// for a local Git command rather than anything about the change.
	//
	// It killed three runs of yoyodyne-ifd.441 in three hours on 2026-09-22, one
	// of them spending a recorded re-run on a run no developer ever saw, and each
	// of them holding a developer slot until the claim audit gave the item back
	// half an hour later. The budget is sized to the tree now (see
	// gitworktree.checkoutFileBudget), so this is the class for a creation that
	// dies anyway rather than the ordinary way one ends.
	CauseWorktreeCheckoutKilled EnvironmentalCause = "worktree-checkout-killed"
	// CauseSandboxSpawnFailure is a provider invocation that never ran: the
	// sandbox the agent is confined to could not be entered, so no agent was ever
	// asked the question the round exists to ask.
	CauseSandboxSpawnFailure EnvironmentalCause = "sandbox-spawn-failure"
	// CauseStaleBinaryDispatch is a round dispatched by a build of the harness
	// older than the one the decision was made against, so the gates the decision
	// relied on were not in the binary that carried it out. It is the cause that
	// spent the three rounds this class was built for, and the one that is hardest
	// to see: a stale build does not refuse, it proceeds, and everything
	// downstream of it looks perfectly valid.
	//
	// Nothing writes it yet, and that is a gap rather than an oversight. There is
	// no refusal site to hang it on, because the failure is precisely the absence
	// of one; recognizing it needs the harness to record which build reserved a run
	// and which build carried out each triage decision, and to compare the two at
	// dispatch. Until that exists a round refused this way is caught by whichever
	// of the causes above its symptom trips — which is how the field cases reached
	// this class, as handback-missing-change.
	CauseStaleBinaryDispatch EnvironmentalCause = "stale-binary-dispatch"
	// CauseTransportFailure is something the harness speaks to having not
	// answered: a tracker read that timed out or was killed under load, a forge or
	// a network that reset, refused, or went away. It is the class the recovery
	// package waits out and asks again at the boundaries that have a window, and
	// the same class where it reaches a step that has none. Today only the
	// integration stop records it — an approved change the environment stopped
	// between its approval and its promotion — because that is the one place a
	// transport failure has been found spending an item's budgets on a verdict
	// nobody rendered (yoyodyne-ifd.394).
	CauseTransportFailure EnvironmentalCause = "transport-failure"
	// CauseProcessVanished is a run recorded as running with no live process
	// behind it and no ending ever recorded: the harness stopped its provider on
	// time — a stream that went silent, or a total budget that ran out — and left
	// the run in flight to be continued, and nothing continued it. The record
	// went on saying "running" while nothing was, so the run held a developer
	// slot and the in-flight guard refused everything beside it, and the
	// development manager's decision about it could not be carried out because
	// the run never recorded a stoppage for the docket to carry. Two runs did
	// that for a day and a half on 2026-09-20 (yoyodyne-ifd.428.4).
	//
	// It is recorded by the reconciling sweep rather than by the run, because the
	// run is exactly what is not there to record it. The sweep names what it
	// observed — no process, no ending, and the last moment the record moved — so
	// nobody has to edit a run record by hand to end one of these.
	CauseProcessVanished EnvironmentalCause = "process-vanished"
	// CauseUsageWindow is a run the provider refused on an exhausted usage limit
	// whose reset lies past what the run may still wait under
	// execution.usage_limit_max_pause. The harness will not take that wait, and
	// that is a decision about the wait rather than a verdict on anything: the
	// window lifts on the provider's clock, and the run ends with its claim given
	// back so the item is pulled again once it has.
	//
	// Twelve runs ended failed that way between 06:50 and 09:35 UTC on
	// 2026-09-23 on one seven-day window resetting four days out, three of them
	// tripped the intake brake, and every one blocked its item for a person who
	// could do nothing about it (yoyodyne-ifd.428.17).
	//
	// It is the one cause settled without asking the worktree. The refusal ends
	// the round before any check or reviewer has read what it holds, so whatever
	// the refused invocation left there is preserved on the branch rather than
	// delivered, and the round spends nothing whatever the worktree says.
	CauseUsageWindow EnvironmentalCause = "usage-window"
	// CauseReplayKilled is an approved change whose replay onto a moved target
	// the harness ended before Git finished it — a budget that ran out, a context
	// that was cancelled, a process that went silent — and which was put back on
	// its branch afterwards. A killed rebase leaves the state directory a
	// conflicted one does, and was being reported as a conflict: terminal, and
	// telling an operator to settle one that did not exist (run b82c1c5a, under
	// load against the local Git budget; yoyodyne-ifd.406). Only the integration
	// stop records it, because a replay happens only there.
	CauseReplayKilled EnvironmentalCause = "replay-killed"
	// CauseDivergedTarget is an approved change whose target branch the harness
	// would not bring onto the remote's before promoting: the two histories have
	// gone different ways, or something in the primary checkout held the
	// fast-forward. Which history is right is a person's to say, but it is a
	// question about the branches and not about the change, and once it is
	// answered the approval still stands. Only the integration stop records it.
	//
	// Three approved changes — yoyodyne-ifd.428.16, 429.3, and 384 — each cost a
	// re-run for this or for CauseRemoteAuthRefused before either was in the
	// class, which is the shape that cost yoyodyne-ifd.309 four overrides.
	CauseDivergedTarget EnvironmentalCause = "diverged-target"
	// CauseRemoteAuthRefused is an approved change a remote stopped by refusing
	// the harness's credential: an SSH key the server would not take ("Permission
	// denied (publickey)"), or a forge login over HTTPS that was refused or
	// missing. It is never waited out, because asking again earns the same answer
	// until somebody loads the key or renews the login. Only the integration stop
	// records it.
	CauseRemoteAuthRefused EnvironmentalCause = "remote-auth-refused"
	// CauseQueuedHeadBehind is an approved change whose merge the forge had
	// queued, whose head then fell behind the target branch, and whose checks
	// failed on files the change does not touch — so the failure is one it met on
	// a target that has moved on, not one it brought. The reconciling sweep
	// withdraws the queued merge and puts the run back at its promotion, where the
	// promotion finds the target moved and replays the change onto it with the
	// gate re-earned, exactly as a promotion that lost its race does. Pull request
	// 713 sat queued that way from the evening of 2026-09-24, 31 commits behind
	// main and failing two tests its change never touched (yoyodyne-ifd.429.16).
	// Only the sweep records it, on the resumption it makes.
	CauseQueuedHeadBehind EnvironmentalCause = "queued-head-behind"
	// CauseCheckStageBound is a check stage stopped at its bound — the
	// configured execution.check_stage_timeout already scaled for the machine's
	// load the way a local Git command's budget is — before the checks reached a
	// verdict. No check failed and nothing was handed back to the developer: what
	// ran out was the machine, as on 2026-09-26 when make race was stopped at a
	// load average of 40 to 55 on 16 cores with the change already passing
	// (yoyodyne-ifd.429.26). The run keeps its branch, worktree, and developer
	// session, and the harness continues it at its checks itself.
	//
	// Like the usage window it ends the round before anything judged it, so it
	// is settled without asking the worktree: the change the attempt left is on
	// the branch for the continuation to check, not a delivery the stop spent.
	CauseCheckStageBound EnvironmentalCause = "check-stage-bound"
)

// Valid reports a cause this harness recognizes. A record naming anything else
// is refused rather than honored: the class returns budget, so a cause nothing
// declared is a budget nothing accounted for.
func EnvironmentalCauses() []EnvironmentalCause {
	return []EnvironmentalCause{
		CauseHandbackMissingChange, CauseDirtyPrimary, CauseWorktreeCheckoutKilled,
		CauseSandboxSpawnFailure, CauseStaleBinaryDispatch, CauseTransportFailure,
		CauseProcessVanished, CauseUsageWindow, CauseReplayKilled, CauseDivergedTarget,
		CauseRemoteAuthRefused, CauseQueuedHeadBehind, CauseCheckStageBound,
	}
}

func (c EnvironmentalCause) Valid() bool {
	return slices.Contains(EnvironmentalCauses(), c)
}

// ClearedBy says what has to happen before a stop of this cause can be resumed,
// for the causes a person clears rather than ones that pass by themselves. It
// is empty for the rest. The resumption refuses in these words while the cause
// still stands, so what a reader is told to do is the same whether they read
// the refusal or the stop.
func (c EnvironmentalCause) ClearedBy() string {
	switch c {
	case CauseDirtyPrimary:
		return "commit, stash, or remove what is uncommitted in the primary checkout"
	case CauseDivergedTarget:
		return "settle the local target branch and the remote's as \"Unwedging a target branch that diverged from the forge\" in docs/operations.md says, or clear whatever in the primary checkout held the catch-up, so the local branch can be fast-forwarded onto the remote's"
	case CauseRemoteAuthRefused:
		return "make the credential the harness pushes with acceptable to the remote again: load the SSH key into the agent the harness runs under (`ssh-add`, then `ssh -T git@github.com` to check), or renew the forge login (`gh auth login`)"
	default:
		return ""
	}
}

// NeedsAPerson reports a cause somebody has to clear on the machine before
// anything can move: the causes ClearedBy names a remedy for. Every other cause
// passes by itself or is the harness's own to retry, which is what decides how
// loudly a stoppage on it is said — the operator is needed only for this set.
func (c EnvironmentalCause) NeedsAPerson() bool {
	return c.ClearedBy() != ""
}

// EndsTheRoundUnjudged reports a cause that ends the round before anything
// could judge what it holds, so the round is refused without the worktree
// being asked whether it delivered: the provider's usage window, and the check
// stage stopped at its bound. See CauseUsageWindow and CauseCheckStageBound.
func (c EnvironmentalCause) EndsTheRoundUnjudged() bool {
	return c == CauseUsageWindow || c == CauseCheckStageBound
}

// Title says what a cause is, the way somebody reading a docket entry or a
// thread reads it. The identifier is what the record keys on and is not a
// sentence anybody should have to decode.
func (c EnvironmentalCause) Title() string {
	switch c {
	case CauseHandbackMissingChange:
		return "the worktree it was handed held none of the change it was to continue"
	case CauseDirtyPrimary:
		return "the primary checkout carried state the harness does not own"
	case CauseWorktreeCheckoutKilled:
		return "the checkout of the run's worktree was ended by the budget the harness gave it"
	case CauseSandboxSpawnFailure:
		return "the sandbox the agent runs in could not be entered"
	case CauseStaleBinaryDispatch:
		return "the build that dispatched it was older than the decision it carried out"
	case CauseTransportFailure:
		return "the tracker, the forge, or the network did not answer"
	case CauseProcessVanished:
		return "the process carrying the run was gone and no ending was ever recorded"
	case CauseUsageWindow:
		return "the provider's usage limit refused it and resets past the maximum pause the harness will wait"
	case CauseReplayKilled:
		return "the replay onto the moved target was ended by the harness before it finished"
	case CauseDivergedTarget:
		return "the target branch could not be fast-forwarded onto the remote's before promoting, so the harness would not catch it up"
	case CauseRemoteAuthRefused:
		return "the remote refused the credential the harness presented"
	case CauseQueuedHeadBehind:
		return "its queued merge's head fell behind the target and failed checks on files the change does not touch"
	case CauseCheckStageBound:
		return "the check stage reached its bound, already scaled for the machine's load, before the checks judged the change"
	default:
		return string(c)
	}
}

// EnvironmentalRefusal is one round's account of the environment refusing it,
// and of what the settle then gave back.
//
// The cause is written where the refusal is decided, because that is the only
// place that knows which one it was. Everything after it is written at settle,
// which is the first point the other half of the definition — an empty
// delivery — can be asked. Settled is what says the round got that far at all,
// and Problem is what says a settle that got there could not finish: those are
// three different states, and a reader shown one as another decides an
// escalation against a figure the harness knows is wrong.
type EnvironmentalRefusal struct {
	Cause EnvironmentalCause `json:"cause"`
	// Detail is the harness's own account of what it found, folded to a line. It
	// is evidence rather than the blocker: the blocker says what a person has to
	// do about the stoppage, and this says which environmental failure produced
	// it.
	Detail     string    `json:"detail,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	// NothingRan says the refusal happened before anything this round would have
	// delivered could exist: no agent of this round was ever invoked, or the
	// invocation was never started by the machine. It is written by the site that
	// refuses, because that is the only place that knows.
	//
	// It is what makes the emptiness question answerable on a continued run. A
	// round of a repair grant runs in a worktree that already holds the change
	// earlier rounds left, so asking whether that worktree differs from the base
	// commit answers "did this run ever deliver anything" rather than "did this
	// round". On a round nothing ran, the second answer is no whatever the first
	// is — and the harness knows it without reading anything, because there was
	// nothing to read it from.
	//
	// It is deliberately not derived from the cause. The same cause arrives at
	// different points: a checkout the harness does not own refuses a worktree
	// before a round starts and refuses a promotion long after one delivered, and
	// a process the machine will not start can be the developer this round is made
	// of or a check run against work that developer already wrote. Only the site
	// can tell those apart, so only the site sets this.
	NothingRan bool `json:"nothing_ran,omitempty"`
	// ResetsAt is when the provider's usage window lifts, on a refusal by it: the
	// provider's own reset where it named one, and the harness's next probe where
	// it named none, which ResetUnknown then says. It is what the item waits out
	// before it is pulled again, so every surface names it. Nothing else records
	// one, because no other cause is a wait with an end.
	ResetsAt     *time.Time `json:"resets_at,omitempty"`
	ResetUnknown bool       `json:"reset_unknown,omitempty"`
	// ProviderStop is why the harness had stopped the provider, on a run the
	// sweep settled as vanished while it was parked on that stop: stalled for a
	// silent stream, or budget_exhausted. The settlement clears the park itself,
	// because a terminal record carries none, so this is what says afterwards that
	// the stoppage was the harness's own and which kind — a first silent-stream
	// stall is one the harness continues itself (see stallcontinue.go).
	ProviderStop string `json:"provider_stop,omitempty"`
	// Settled says the round this cause belongs to has ended and the class was
	// decided on it. It is what makes the settle one-shot: a cause recorded on a
	// round the harness turned away without charging it is settled there and then,
	// so a later round of the same run that happens to deliver nothing is judged on
	// its own evidence instead of inheriting this one's.
	Settled bool `json:"settled,omitempty"`
	// Refused says the settle found both halves of the definition and classified
	// the round environmental. It is false on a round that recorded a cause and
	// delivered a change anyway, which spends exactly as any other round does —
	// and stays false on one whose settle could not tell, which is the direction
	// that costs an item a round it should have kept rather than one it should
	// have spent.
	Refused bool `json:"refused,omitempty"`
	// RoundReturned and GrantReturned are what the settle actually gave back: the
	// review round the item was charged against its cap, and the granted repair
	// round the continuation consumed. Either can be false on a refused round
	// that never reached the thing it would have spent — a handback refused
	// before any reviewer was asked has no round to return.
	RoundReturned bool `json:"round_returned,omitempty"`
	GrantReturned bool `json:"grant_returned,omitempty"`
	// RoundLeftSpent says the settle asked for the review round back and was
	// refused because this process is not the one that charged it. It is the run
	// re-entered at the review: the round at the head of the item's record was
	// spent by the process before this one, on a verdict the item really got, so
	// it stays spent.
	//
	// It is not the same as having reached nothing that spends, and it is recorded
	// rather than left to read as one. A refusal that returned nothing because
	// there was nothing to return leaves the item where it stood; this one leaves
	// it one round further on, and a reader shown the first as the second decides
	// how much budget the item has left against a figure that is wrong.
	RoundLeftSpent bool `json:"round_left_spent,omitempty"`
	// RoundChargedBy names the process holding that round, where the record names
	// one. A round charged before rounds carried the process that charged them
	// names nobody and is left spent all the same.
	RoundChargedBy string `json:"round_charged_by,omitempty"`
	// Problem is a return the settle decided on and could not write. It is never
	// left unsaid: a round classified environmental whose budget was not actually
	// returned is an item walking toward its cap with a record that says it is
	// not, which is the exact failure this class exists to end.
	Problem string `json:"problem,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (r EnvironmentalRefusal) Validate() error {
	var problems []error
	if !r.Cause.Valid() {
		problems = append(problems, fmt.Errorf("cause %q is not one this harness records for a run ended from outside the work", r.Cause))
	}
	if len(r.Detail) > MaxEnvironmentalDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(r.Detail), MaxEnvironmentalDetailBytes))
	}
	if len(r.Problem) > MaxEnvironmentalProblemBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, which exceeds the %d byte bound", len(r.Problem), MaxEnvironmentalProblemBytes))
	}
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if r.ResetsAt != nil && r.Cause != CauseUsageWindow {
		problems = append(problems, fmt.Errorf("a reset is recorded only on a %s refusal, not on %q", CauseUsageWindow, r.Cause))
	}
	if r.ResetUnknown && r.ResetsAt == nil {
		problems = append(problems, errors.New("a reset the provider did not name requires the probe recorded in its place"))
	}
	if r.ProviderStop != "" {
		if r.ProviderStop != ProviderStopStalled && r.ProviderStop != ProviderStopBudgetExhausted {
			problems = append(problems, fmt.Errorf("provider_stop %q is not a way the harness stops a provider", r.ProviderStop))
		}
		if r.Cause != CauseProcessVanished {
			problems = append(problems, fmt.Errorf("a provider stop is recorded only on a %s refusal, not on %q", CauseProcessVanished, r.Cause))
		}
	}
	// Something given back is something that was classified, and a classification
	// is something a settle made. A record the other way round could not have been
	// written by the settle, which is the only thing that writes any of the three.
	if (r.RoundReturned || r.GrantReturned) && !r.Refused {
		problems = append(problems, errors.New("a round or grant returned requires the refusal that returned it"))
	}
	if r.Refused && !r.Settled {
		problems = append(problems, errors.New("a refused round requires the settle that classified it"))
	}
	// A round left spent is a return the settle asked for and was refused, so it
	// is written by the same step and cannot stand beside the return it excludes.
	if r.RoundLeftSpent && (!r.Refused || r.RoundReturned) {
		problems = append(problems, errors.New("a round left spent requires the refusal that asked for it back and excludes the return of it"))
	}
	if r.RoundChargedBy != "" && !r.RoundLeftSpent {
		problems = append(problems, errors.New("a process credited with the round requires the return that was refused for it"))
	}
	return errors.Join(problems...)
}

// Describe says what one refusal came to: which environmental failure it was,
// and what the item was therefore charged or not charged for the round.
//
// It is the one derivation of that, and every surface phrases around it rather
// than reading the flags again. The states are close enough to be got wrong
// separately — and the one that matters most is the one a drift would silently
// drop, because a refusal whose return could not be written is the single case
// where the item's counters really are higher than what the round cost it. Three
// copies of this would be three places for that case to go missing.
//
// It never says "spent nothing" on a figure it did not actually give back. A
// refusal that returned nothing says so, because "ended by something outside the work" with
// no accounting after it is exactly the sentence a reader takes on trust.
//
// It carries neither the detail nor the problem text. Those are evidence, and a
// surface that wants them renders them beside this rather than inside it: a
// docket entry has room for a block and a thread line does not.
func (r EnvironmentalRefusal) Describe() string {
	named := fmt.Sprintf("%s (%s)", r.Cause, r.Cause.Title())
	if said := r.ResetSays(); said != "" {
		named = fmt.Sprintf("%s (%s; %s)", r.Cause, r.Cause.Title(), said)
	}
	switch {
	case !r.Settled:
		return fmt.Sprintf("a cause outside the work was recorded: %s; the round it belongs to has not settled, so nothing has been decided about what it cost", named)
	case !r.Refused && strings.TrimSpace(r.Problem) != "":
		return fmt.Sprintf("a cause outside the work was recorded: %s, and whether the round delivered anything could not be read, so it spent as any round does", named)
	case !r.Refused:
		return fmt.Sprintf("a cause outside the work was recorded: %s, and the round delivered a change all the same, so it spent as any round does", named)
	case strings.TrimSpace(r.Problem) != "":
		return fmt.Sprintf("ended by something outside the work: %s, and what it should have been given back could not be written, so this item's counters are higher than the round cost it", named)
	case r.RoundReturned && r.GrantReturned:
		return fmt.Sprintf("ended by something outside the work: %s, so the review round it was charged and the granted repair round it consumed were both returned, and this item stands where it did before the round", named)
	case r.RoundReturned:
		return fmt.Sprintf("ended by something outside the work: %s, so the review round it was charged was returned and no repair grant had been consumed, and this item stands where it did before the round", named)
	case r.RoundLeftSpent && r.GrantReturned:
		return fmt.Sprintf("ended by something outside the work: %s, so the granted repair round it consumed was returned, and the review round at the head of this item's record was left spent because %s charged it rather than this one", named, r.roundHolder())
	case r.RoundLeftSpent:
		return fmt.Sprintf("ended by something outside the work: %s, and the review round at the head of this item's record was left spent because %s charged it rather than this one, so the item stands one round further on than before it", named, r.roundHolder())
	case r.GrantReturned:
		return fmt.Sprintf("ended by something outside the work: %s, so the granted repair round it consumed was returned and no review round had been charged, and this item stands where it did before the round", named)
	default:
		return fmt.Sprintf("ended by something outside the work: %s, and it reached nothing that spends, so there was nothing to give back and this item stands where it did before the round", named)
	}
}

// ResetSays names when a usage window lifts, in the words every surface uses
// for it, and is empty on a refusal that recorded no reset.
func (r EnvironmentalRefusal) ResetSays() string {
	if r.ResetsAt == nil {
		return ""
	}
	at := r.ResetsAt.UTC().Format(time.RFC3339)
	if r.ResetUnknown {
		return "the provider named no reset, so the harness asks again at " + at
	}
	return "the window resets at " + at
}

// roundHolder is the process credited with a round a settle was refused, in the
// words a sentence about it needs. A round charged before rounds carried the
// process that charged them names nobody, and saying so is better than a
// sentence that trails off where the identity should be.
func (r EnvironmentalRefusal) roundHolder() string {
	if strings.TrimSpace(r.RoundChargedBy) == "" {
		return "a process the record does not name"
	}
	return r.RoundChargedBy
}
