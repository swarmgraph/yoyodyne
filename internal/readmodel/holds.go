package readmodel

// What the harness is holding for a person, and why a status field cannot say
// it.
//
// A work item's status is written when the work stops and never rewritten when
// what stopped it clears. So it answers two questions at once and maintains
// neither: it says an item is blocked whether the block was a dependency that
// has since landed or a stoppage somebody still has to decide about, and it goes
// on saying it after the dependency closes. On 2026-09-04 that hid two-thirds of
// this backlog, two p0 items among it, and the line sat idle for a morning
// because every one of those items read as unpullable and nothing said why.
//
// The two questions are separated here by asking the records instead. What an
// item waits on is the tracker's dependency graph, which the backlog reads for
// itself and which clears on its own as the work lands. What somebody still has
// to release is this: the harness's own durable account of work it stopped and
// has not been told what to do with, and of work it finished whose publication
// it could not. Neither is a field anybody has to remember to update, which is
// the whole of the difference.
//
// Nothing here releases anything, and that direction is deliberate. A hold is
// lifted by a person deciding — triage picking the preserved change up, or an
// escalation being answered — and the effect of the decision is that the records
// this reads stop saying the item is held. An item is released by the facts
// changing rather than by anything written back over them.
//
// # A preserved change is looked for, not read off a flag
//
// Whether a stopped run's change is still there is answered by the repository
// rather than by the run's record. The record's removal flags are what a sweep
// or a cleanup remembered to write, and a flag is exactly the kind of field this
// derivation exists to stop trusting: on 2026-09-19 the product manager's
// stale-state repair cleared yoyodyne-ifd.372's blocked status as "no longer
// held behind a preserved run" on the strength of the record, while the item's
// own notes still said the run's branch and worktree were checked and there. So
// a stopped run is held where its branch or its worktree exists, checked as the
// hold is read; where the check could not be made it is held as if they did,
// with the reason saying so; and a stopped run about which a triage decision
// stands that the harness has still to carry out — a repair continuation first
// among them — is held whatever became of its artifacts, because what that
// decision continues is the run, and a fresh pull would start over beside it.
//
// # Two holds, two movers
//
// A decision being recorded is not the decision being carried out, and until
// the harness acts the item is held either way. Reporting both as one thing —
// "held for a person" — is what cost the operator days on 2026-09-07:
// thirty-three items read as work the development manager owed a decision on
// while she had decided every one of them, and the gap was the carry-out. So
// each hold says which of the two it is, read from the item's own durable triage
// record: a decision standing about the stoppage that holds it is the harness's
// move, and no decision standing is hers.
//
// It lives with the read model rather than beside either caller because the
// scheduler and every operator surface have to give one answer. A surface that
// showed an item as pullable while the scheduler held it would be a
// disagreement only the operator could adjudicate, which is the thing one
// derivation exists to prevent.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Stoppages is the durable record of the work the harness has stopped: every run
// it has recorded, and every stoppage it has put in front of the development
// manager. Both are needed and neither is enough — a stoppage whose change is
// still on a branch is held whether or not anybody was ever asked about it, and
// one nobody has answered is held whether or not its branch survived.
//
// It is satisfied by *runstate.Store.
type Stoppages interface {
	Recorded() ([]runstate.State, error)
	Escalated() ([]runstate.Escalation, error)
}

// Decisions is the durable per-item record of what triage has decided, which is
// what says whether a held item is waiting on a decision or on the carrying out
// of one. It is the record the triage guards spend and refuse against, read
// rather than re-derived from the runs: a second reading of what has been
// decided is a second answer, and this one decides which role a surface sends
// the operator to.
//
// It is satisfied by *runstate.TriageStore.
type Decisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// Remains is what the repository actually holds of a stopped run's change: its
// branch, and its checkout. It is asked rather than the run's record read,
// because the record's removal flags are what something remembered to write and
// the repository is what is there. It is satisfied by *gitworktree.Manager.
type Remains interface {
	Survives(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Survival, error)
}

// HeldForAPerson is the admitted work somebody has to release before anything
// pulls it, with what each item is waiting for and whose move that is.
//
// A reading that fails is an error rather than an empty answer. The zero Holds
// already means "not read" and holds everything blocked, so a caller that
// reports the failure and carries on with it loses no safety; what it must not
// do is treat a failure as "nothing is held", which would release exactly the
// work this exists to hold.
//
// decisions may be nil, and an item's record may fail to open. Neither costs the
// hold: the item is held exactly as it was and is reported as one nobody has
// decided about, with the reason saying that this reading could not tell. That
// is the conservative direction — it points at the role that would have to
// decide, which is where the answer went before the two were separated — and it
// keeps one unreadable file from making a whole queue unpullable.
//
// remains may be nil too, and a run's artifacts may fail to be looked for. A
// reading wired without it falls back to what each run's record says survived,
// and says in the reason that nothing looked; a look that failed holds the run
// as if its change were there, for the reason an unread hold holds everything.
func HeldForAPerson(ctx context.Context, stoppages Stoppages, decisions Decisions, remains Remains) (backlog.Holds, error) {
	runs, err := stoppages.Recorded()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	escalated, err := stoppages.Escalated()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the escalated stoppages: %w", err)
	}
	decided := standingDecisions(decisions)
	return heldStopping(runs, escalated, decided, standingStops(decided), Looking(ctx, remains, nil)), nil
}

// latestStop is the stop an item's triage record stands at, where the decision
// recorded last about any of its runs is a stop.
type latestStop func(workItemID string) (runstate.TriageDecision, bool)

// standingStops asks the same reading for the stop an item stands at.
// A record that cannot be read holds nothing here: which item a stop
// superseded it with is unknown, and every other hold on the item is still read
// and still says what it could not.
func standingStops(decided standing) latestStop {
	if decided == nil {
		return nil
	}
	return func(workItemID string) (runstate.TriageDecision, bool) {
		counters, problem := decided(workItemID)
		if problem != "" {
			return runstate.TriageDecision{}, false
		}
		latest, found := counters.LatestDecision()
		if !found || latest.Decision != runstate.TriageDecisionStop {
			return runstate.TriageDecision{}, false
		}
		return latest, true
	}
}

// standing reads the decisions about one item's runs and what stopped that
// reading finding out. The hold selects a run from these references before
// asking whether that run's decision is still outstanding.
type standing func(workItemID string) (runstate.TriageCounters, string)

func (read standing) of(workItemID string, run runstate.State) decidedStanding {
	counters, problem := read(workItemID)
	if problem == "" && counters.WorkItemID != "" && counters.WorkItemID != workItemID {
		problem = fmt.Sprintf("the triage record belongs to %s rather than %s", counters.WorkItemID, workItemID)
	}
	// The docket asks the same shared rule of the same run and ledger.
	reading := decidedStanding{carryOut: counters.AwaitingCarryOutOf(run), problem: problem}
	if refused, found := counters.RefusedCarryOut(run.RunID); found {
		reading.refused = &refused
	}
	if decision, found := counters.DecisionOf(run.RunID); found && problem == "" {
		reading.rerun = decision.Decision == runstate.TriageDecisionRerun
	}
	return reading
}

// decidedStanding is one reading of a stoppage's triage record. carryOut is a
// decision the harness has still to act on; refused is a decision it tried to
// act on and a gate refused, which is the development manager's again and is
// held as hers; rerun says the decision standing about the run is a re-run;
// problem is what stopped the reading.
type decidedStanding struct {
	carryOut bool
	refused  *runstate.TriageCarryOut
	rerun    bool
	problem  string
}

// releasesRerun reports a re-run decision that no longer holds its item: the
// harness was refused carrying it out by a gate that will not clear, and the
// run it was about left no branch or worktree. A re-run of such a run starts
// the item from the target branch, which is what an ordinary pull does, so
// there is nothing a fresh pull would start beside. Holding the item for a
// decision that will never be carried out would hold it for good, because
// nothing the development manager records clears a refusal of a decision about
// a run that has already ended (yoyodyne-ifd.428.87). The carry-out writes the
// refusal and the release onto the item when it records the refusal.
func (reading decidedStanding) releasesRerun(found triage.Found) bool {
	return reading.rerun && reading.refused != nil && reading.refused.Cause != "" && !found.Holds()
}

// standingDecisions reads each item's triage record once, however many of its
// runs are held: one item's stoppages share one record, and a queue of stopped
// runs would otherwise open the same file for each of them.
func standingDecisions(decisions Decisions) standing {
	if decisions == nil {
		return func(string) (runstate.TriageCounters, string) {
			return runstate.TriageCounters{}, "nothing was wired to read what triage has decided about it"
		}
	}
	read := make(map[string]runstate.TriageCounters)
	failed := make(map[string]string)
	return func(workItemID string) (runstate.TriageCounters, string) {
		if problem, known := failed[workItemID]; known {
			return runstate.TriageCounters{}, problem
		}
		counters, seen := read[workItemID]
		if !seen {
			opened, err := decisions.Counters(workItemID)
			if err != nil {
				problem := fmt.Sprintf("what triage has decided about it could not be read: %v", err)
				failed[workItemID] = problem
				return runstate.TriageCounters{}, problem
			}
			read[workItemID], counters = opened, opened
		}
		return counters, ""
	}
}

// The clause each hold closes on: whose move follows it. They are two sentences
// rather than one because they are two people, and they are written once here so
// that every surface saying a hold says the same words about who releases it.
const (
	awaitingDecisionClause = "the development manager decides what happens to it, and nothing pulls it until she has"
	awaitingCarryOutClause = "the development manager has already decided what happens to it, so what is outstanding is the harness carrying that decision out rather than a decision"
	// harnessContinuesStallClause closes the hold of a first silent-stream stall,
	// which nobody decides: the harness continues it itself, once.
	harnessContinuesStallClause = "the harness stopped its provider for a silent stream and nothing was judged, so the harness continues the run itself, in its own session and at the phase it stalled in, at the next pull with a developer slot free — it waits on the harness rather than on a decision"
)

// heldFor is one item's hold: the account of what stopped it, closed by whose
// move follows. A reading that could not say which of the two it is says so in
// the reason and reports the decision as unmade, which is where the answer went
// before the two were told apart.
//
// since is when the item came to be held, which is the run's stop for every
// hold this closes.
//
// A decision the harness was refused carrying out is held as the development
// manager's, with the refusal said: it is not the harness's to act on, and a line
// naming the harness over it is one nobody acts on (yoyodyne-8ff).
func heldFor(runID, account string, reading decidedStanding, since time.Time) backlog.Hold {
	switch {
	case reading.problem != "":
		return backlog.Hold{Reason: account + "; " + reading.problem + ", so this is stated as a stoppage nobody has decided about", Since: since, RunID: runID}
	case reading.refused != nil:
		return backlog.Hold{Reason: account + "; " + refusedCarryOut(*reading.refused), Since: since, RunID: runID}
	case reading.carryOut:
		return backlog.Hold{Reason: account + "; " + awaitingCarryOutClause, Decided: true, Since: since, RunID: runID}
	default:
		return backlog.Hold{Reason: account + "; " + awaitingDecisionClause, Since: since, RunID: runID}
	}
}

// refusedCarryOut closes a hold whose decision the harness was refused carrying
// out: what was refused, why, what clears it, and that the move is the
// development manager's.
func refusedCarryOut(refused runstate.TriageCarryOut) string {
	return fmt.Sprintf("the harness tried to carry out the %q the development manager decided and was refused by %s (%s, last at %s), so it is hers to move rather than the harness's: what clears it is %s",
		refused.Decision, refused.Gate, strings.TrimSpace(refused.Refusal), refused.RefusedAt.UTC().Format(time.RFC3339), strings.TrimSpace(refused.Clears))
}

// stoppedAt is when a run stopped, which is when the item it was carrying came
// to be held: the moment its record says it completed, or the moment the record
// last moved where it says none.
func stoppedAt(run runstate.State) time.Time {
	if run.CompletedAt != nil && !run.CompletedAt.IsZero() {
		return *run.CompletedAt
	}
	return run.UpdatedAt
}

// raisedAt is when a stoppage was put on the development manager's docket,
// which is when an item nobody has decided about came to be held: the
// docketing where the record kept it, and the first attempt to put it in front
// of her where it did not.
func raisedAt(escalation runstate.Escalation) time.Time {
	if !escalation.DocketedAt.IsZero() {
		return escalation.DocketedAt
	}
	return escalation.FirstAttemptedAt
}

// heldForAPerson is the derivation itself, over records already read. It is
// separate so the rule can be tested against run and escalation records without
// a store behind them.
func heldForAPerson(runs []runstate.State, escalated []runstate.Escalation, decided standing, look Look) backlog.Holds {
	return heldStopping(runs, escalated, decided, nil, look)
}

// heldStopping is heldForAPerson with the stops the development manager decided
// read as well; stopped may be nil, which reads none.
func heldStopping(runs []runstate.State, escalated []runstate.Escalation, decided standing, stopped latestStop, look Look) backlog.Holds {
	reasons := make(map[string]backlog.Hold)
	// The escalations first, so that an item that is both — a stoppage nobody
	// answered whose change is also still preserved — reads as the preserved one.
	// Both are true and either would hold it; the preserved change is the one that
	// says why starting the item over is the wrong move, which is what a reader
	// about to release it needs to know.
	for _, escalation := range escalated {
		if escalation.WorkItemID == "" || strings.TrimSpace(escalation.Decision) != "" {
			continue
		}
		// A raise is held by the parking it placed, which is the owner's to release,
		// and the release is what ends it. Holding it here as well is what kept an
		// amended, released raise out of every pull whenever its delivery was
		// answered without a decision recorded against it — a development manager
		// who read the raise and waited for the owner's amendment left exactly
		// that. What she may still decide about the raising run's change is
		// answered below, where a decided re-run holds the item until it starts.
		if escalation.DocketKey == triage.Key(triage.ClassEscalation, escalation.RunID) {
			continue
		}
		// Never a carry-out: an escalation with nothing recorded against it is by
		// construction one nobody has decided, so what it waits on is the decision
		// itself however much triage has decided about the item's other stoppages.
		reasons[escalation.WorkItemID] = backlog.Hold{Reason: undecidedStoppage(escalation), Since: raisedAt(escalation), RunID: escalation.RunID}
	}
	// A publication the forge never merged next. It holds the item for the same
	// reason the merged one below does — the work is on the target branch and a
	// run against it would redo it — but it is answered before the preserved
	// change rather than after, because it says nothing about where the run's own
	// branch went and the preserved-change reason does: an item that is both has a
	// branch somebody has to decide about, and that is what its reader needs.
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool {
		return outstandingPublication(run) && !mergeConfirmed(run)
	}) {
		// A merge withdrawn for its target's red check waits on the items filed
		// for that check, and the harness takes it up once they close: nobody has
		// anything to decide, so it is the harness's move.
		if run.WaitingOnRedTarget() {
			reasons[workItemID] = backlog.Hold{Reason: redTargetPublication(run), Decided: true, Since: run.PullRequest.TargetRed.At}
			continue
		}
		reasons[workItemID] = heldFor(run.RunID, unmergedPublication(run), decided.of(workItemID, run), stoppedAt(run))
	}
	// The stoppages, each looked at rather than read: a run whose change the
	// repository still holds, a run whose change nothing could look for, and a run
	// about which a decision stands that the harness has still to carry out. The
	// third is held with nothing of it surviving, because what the decision
	// continues is the run itself — a repair grant re-enters its preserved
	// session — and a fresh pull would start over beside it.
	looked := make(map[string]triage.Found)
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool {
		if !stoppage(run) {
			return false
		}
		found := look(run)
		looked[run.RunID] = found
		if found.Holds() {
			return true
		}
		// A decision the harness was refused carrying out still stands, so it holds
		// the item as surely as one it has still to carry out — except a re-run
		// refused for good over a run that left nothing, which releasesRerun lets go.
		reading := decided.of(run.WorkItemID, run)
		if reading.releasesRerun(found) {
			return false
		}
		return reading.carryOut || reading.refused != nil
	}) {
		found := looked[run.RunID]
		reasons[workItemID] = stoppageHold(run, decided, found)
	}
	// A raise whose re-run the development manager has decided and the harness
	// has still to carry out. The run that raised the item succeeded, so none of
	// the stoppage rules above holds it, and once the item's owner has released
	// the raise's parking nothing else does either: a pull would start the item
	// from the target beside the re-run that is to start it from the raise's
	// preserved change. It is the harness's move, as every decided carry-out is.
	//
	// It asks of the item's latest run rather than of its latest raise, because
	// the decision stands on the record after it is carried out: once the re-run
	// has started, the item's latest run is that re-run and nothing is held here.
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool { return run.WorkItemID != "" }) {
		if _, held := reasons[workItemID]; held || !run.Status.Terminal() || !run.Escalated() {
			continue
		}
		if reading := decided.of(workItemID, run); (reading.carryOut || reading.refused != nil) && !reading.releasesRerun(look(run)) {
			reasons[workItemID] = heldFor(run.RunID, raiseRerun(run), reading, stoppedAt(run))
		}
	}
	// The merged publications last. Only these know the change reached everywhere
	// it was going, so only these may say there is nothing left to do about it —
	// which is worth saying over the preserved-change reason, since a branch left
	// behind a confirmed merge is debris rather than work to pick up.
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool {
		return outstandingPublication(run) && mergeConfirmed(run)
	}) {
		reasons[workItemID] = heldFor(run.RunID, mergedPublication(run), decided.of(workItemID, run), stoppedAt(run))
	}
	// A stop the development manager decided, last, because it is only ever about
	// the item's latest run and says the most about what to do with it. Such a run
	// ends cancelled with no blocker, which is a shape none of the rules above
	// hold, so without this the item went back to the queue the moment its run
	// stopped: superseded work, pulled again beside the change she had it stop.
	// It holds while that change is preserved, and it holds only where the stop is
	// what the item's record stands at — a decision she records after it, about
	// that run or any other, is what she has decided since.
	for workItemID, hold := range supersededHolds(runs, stopped, look) {
		reasons[workItemID] = hold
	}
	holdDecisions(reasons, runs, escalated, decided, func(run runstate.State) triage.Found {
		if found, seen := looked[run.RunID]; seen {
			return found
		}
		found := look(run)
		looked[run.RunID] = found
		return found
	})
	return backlog.ReadHolds(reasons).OnUnlandedParents(unlandedChanges(runs))
}

// stoppageHold describes one run, regardless of which of an item's runs a hold
// selects. An approved change stopped at promotion is still the harness's to
// resume while its branch survives, without a triage decision being needed.
func stoppageHold(run runstate.State, decided standing, found triage.Found) backlog.Hold {
	preserved := found.Holds()
	if run.IntegrationStop != nil && StoppageMover(run, &found, false) == MoverHarness {
		return backlog.Hold{Reason: stoppedIntegration(run, found, preserved), Decided: true, Since: stoppedAt(run), RunID: run.RunID}
	}
	reading := decided.of(run.WorkItemID, run)
	if run.IntegrationStop != nil {
		return heldFor(run.RunID, triage.IntegrationGoneSays(run.RunID, found.Describe()), reading, stoppedAt(run))
	}
	if preserved && !reading.carryOut && reading.refused == nil && reading.problem == "" && run.HarnessContinuesStall() {
		return backlog.Hold{Reason: preservedChange(run, found) + "; " + harnessContinuesStallClause, Decided: true, Since: stoppedAt(run), RunID: run.RunID}
	}
	if !preserved && reading.rerun && reading.carryOut && reading.problem == "" {
		return backlog.Hold{Reason: freshRerun(run), Decided: true, Since: stoppedAt(run), RunID: run.RunID}
	}
	if !preserved {
		return heldFor(run.RunID, continuedStoppage(run), reading, stoppedAt(run))
	}
	return heldFor(run.RunID, preservedChange(run, found), reading, stoppedAt(run))
}

// holdDecisions names the run an outstanding decision concerns, even when a
// different run has preserved work or an unfinished publication. Maintenance
// updates and later independent runs do not settle a repair of that run. A
// missing or inconsistent reference holds the item and says what could not be
// established, retaining any account of preserved work already found.
func holdDecisions(held map[string]backlog.Hold, runs []runstate.State, escalated []runstate.Escalation, decided standing, look Look) {
	byRun := make(map[string]runstate.State)
	items := make(map[string]struct{})
	for _, run := range runs {
		byRun[run.RunID] = run
		if run.WorkItemID != "" {
			items[run.WorkItemID] = struct{}{}
		}
	}
	for _, escalation := range escalated {
		if escalation.WorkItemID != "" {
			items[escalation.WorkItemID] = struct{}{}
		}
	}
	latest := latestPerItem(runs, func(run runstate.State) bool { return run.WorkItemID != "" })
	for workItemID := range items {
		counters, problem := decided(workItemID)
		if problem != "" {
			continue
		}
		var selected runstate.TriageDecision
		var hold backlog.Hold
		var referenceProblem string
		found := false
		for _, decision := range counters.Decisions {
			candidate, problem := decisionHold(workItemID, decision, counters, byRun, latest[workItemID], decided, look)
			if candidate.Reason == "" || (found && (decision.DecidedAt.Before(selected.DecidedAt) ||
				(decision.DecidedAt.Equal(selected.DecidedAt) && decision.RunID >= selected.RunID))) {
				continue
			}
			selected, hold, referenceProblem, found = decision, candidate, problem, true
		}
		if !found {
			continue
		}
		if referenceProblem != "" {
			if prior, preserved := held[workItemID]; preserved {
				hold.Reason += "; " + prior.Reason
			}
		}
		held[workItemID] = hold
	}
}

func decisionHold(workItemID string, decision runstate.TriageDecision, counters runstate.TriageCounters, byRun map[string]runstate.State, latest runstate.State, decided standing, look Look) (backlog.Hold, string) {
	// An earlier decision about this run may still be in the record. It cannot
	// hold the item after another decision supersedes it, even if the run is
	// missing and its reference would otherwise be reported as unresolved.
	if current, found := counters.DecisionOf(decision.RunID); found && current != decision {
		return backlog.Hold{}, ""
	}
	_, refused := counters.RefusedCarryOut(decision.RunID)
	if !decision.Spends() || (decision.Decision == runstate.TriageDecisionRepair && !counters.GrantOutstanding() && !refused) {
		return backlog.Hold{}, ""
	}
	run, recorded := byRun[decision.RunID]
	var problem string
	switch {
	case strings.TrimSpace(decision.RunID) == "":
		problem = "the decision names no run"
	case counters.WorkItemID != "" && counters.WorkItemID != workItemID:
		problem = fmt.Sprintf("the triage record belongs to %s rather than %s", counters.WorkItemID, workItemID)
	case !recorded:
		problem = fmt.Sprintf("run %s is missing from the recorded runs", decision.RunID)
	case run.WorkItemID != workItemID:
		problem = fmt.Sprintf("run %s belongs to %s rather than %s", decision.RunID, run.WorkItemID, workItemID)
	default:
		// A re-run is carried out by starting a newer run; a repair continues
		// its named run, and a re-arm settles that run's publication instead.
		if !run.Status.Terminal() || (!counters.AwaitingCarryOutOf(run) && !refused) ||
			(decision.Decision == runstate.TriageDecisionRerun && latest.RunID != run.RunID && !latest.StartedAt.Before(decision.DecidedAt)) ||
			(decision.Decision == runstate.TriageDecisionRearm && mergeConfirmed(run)) {
			return backlog.Hold{}, ""
		}
		if !stoppage(run) && !outstandingPublication(run) && !run.Escalated() {
			problem = fmt.Sprintf("run %s records neither a stoppage nor an unfinished publication nor a raised item", decision.RunID)
		}
	}
	if problem != "" {
		return backlog.Hold{
			Reason: fmt.Sprintf("the %q decision's run reference could not be reconciled: %s; the item remains held until that reference is reconciled", decision.Decision, problem),
			RunID:  decision.RunID, Since: decision.DecidedAt,
		}, problem
	}
	reading := decided.of(workItemID, run)
	if (stoppage(run) || run.Escalated()) && !outstandingPublication(run) && reading.releasesRerun(look(run)) {
		return backlog.Hold{}, ""
	}
	switch {
	case outstandingPublication(run) && mergeConfirmed(run):
		return heldFor(run.RunID, mergedPublication(run), reading, stoppedAt(run)), ""
	case stoppage(run):
		return stoppageHold(run, decided, look(run)), ""
	case outstandingPublication(run):
		if run.WaitingOnRedTarget() {
			return backlog.Hold{Reason: redTargetPublication(run), Decided: true, Since: run.PullRequest.TargetRed.At, RunID: run.RunID}, ""
		}
		return heldFor(run.RunID, unmergedPublication(run), reading, stoppedAt(run)), ""
	default:
		return heldFor(run.RunID, raiseRerun(run), reading, stoppedAt(run)), ""
	}
}

// supersededHolds is every item whose latest run the development manager
// stopped in flight, whose record still stands at that stop, and whose run's
// change the repository still holds.
//
// A run that passed its last boundary before the stop was read and stopped for
// another reason is not one of these, whatever the record says: it ended on a
// blocker or a death rather than cancelled, and the stoppage rules above hold it
// as the undecided stoppage it is.
func supersededHolds(runs []runstate.State, stopped latestStop, look Look) map[string]backlog.Hold {
	if stopped == nil {
		return nil
	}
	held := make(map[string]backlog.Hold)
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool { return run.WorkItemID != "" }) {
		if run.Status != runstate.StatusCancelled || strings.TrimSpace(run.Blocker) != "" {
			continue
		}
		decision, found := stopped(workItemID)
		if !found || decision.RunID != run.RunID {
			continue
		}
		preserved := look(run)
		if !preserved.Holds() {
			continue
		}
		held[workItemID] = backlog.Hold{Reason: supersededStop(run, decision, preserved), Since: decision.DecidedAt, RunID: run.RunID}
	}
	return held
}

// supersededStop says why an item the development manager stopped a run of is
// not something to pull: the item is superseded, by what where she named it, and
// the change the stopped run made is still there.
func supersededStop(run runstate.State, decision runstate.TriageDecision, found triage.Found) string {
	superseded := "she named no item doing its work instead"
	if by := strings.TrimSpace(decision.SupersededBy); by != "" {
		superseded = "it is superseded by " + by
	}
	what := whatWasFound(found)
	if found.Unknown {
		what = uncheckable(found)
	}
	return fmt.Sprintf(
		"run %s was stopped in flight by the development manager's decision, %s, and %s; its change is preserved (%s), so it is not pulled while that change stands — closing or retiring the item, or her deciding about the run again, releases it",
		run.RunID, decision.Cite(), superseded, what)
}

// unlandedChanges is every item whose own change the harness recorded and never
// saw reach the integration target, with where that change is, walked the way a
// creation under one of them walks it (runstate.Unlanded). It holds nothing
// itself: it is what a child that says it builds on one of these items is held
// for, until the change lands.
func unlandedChanges(runs []runstate.State) map[string]string {
	perItem := make(map[string][]runstate.State)
	for _, run := range runs {
		if run.WorkItemID != "" {
			perItem[run.WorkItemID] = append(perItem[run.WorkItemID], run)
		}
	}
	unlanded := make(map[string]string)
	for workItemID, itemRuns := range perItem {
		runstate.NewestFirst(itemRuns)
		if run, found := runstate.Unlanded(itemRuns); found {
			unlanded[workItemID] = UnlandedAccount(run)
		}
	}
	return unlanded
}

// UnlandedAccount says where one item's unlanded change is: the run that made it,
// the branch and commit it is on, and the pull request that published it, where
// each was recorded. It is the account the queue gives for a child held on the
// change and the guidance a creation under the item records on the child, so the
// two name the same branch in the same words.
func UnlandedAccount(run runstate.State) string {
	target := strings.TrimSpace(run.TargetBranch)
	if target == "" {
		target = "the target branch"
	}
	account := fmt.Sprintf("the change run %s made for %s never reached %s", run.RunID, run.WorkItemID, target)
	if branch := strings.TrimSpace(run.Branch); branch != "" {
		account += ", and is on " + branch
	}
	if commit := strings.TrimSpace(run.HarnessCommit); commit != "" {
		account += " at commit " + commit
	}
	if run.PullRequest != nil && run.PullRequest.Number > 0 {
		account += fmt.Sprintf(", published as pull request #%d", run.PullRequest.Number)
	}
	return account
}

// StoppageMover is who moves next on one stopped run: the harness where it is
// an approved change the environment stopped whose branch is still there
// (triage.IntegrationResumable), or where a triage decision about it stands
// that the harness has still to carry out; and the development manager
// otherwise, because a stoppage nobody has decided about is hers. It is the
// reading the hold above takes and the docket's next mover says
// (triage.Entry.renderNextMover), as a token rather than a sentence, so a
// surface that has to weigh a stoppage by who moves next — the channel's
// severity among them — asks it here instead of deriving it again.
//
// found is what the repository held of the run's change, and nil where nothing
// looked, which answers from the run's own removal flag.
func StoppageMover(run runstate.State, found *triage.Found, awaitingCarryOut bool) Mover {
	if run.Document != nil {
		return MoverOf(run.Document.Owner)
	}
	if run.IntegrationStop != nil && triage.IntegrationResumable(found, run.BranchRemoved) {
		return MoverHarness
	}
	if awaitingCarryOut {
		return MoverHarness
	}
	// A check stage its bound stopped is continued by the harness at its checks
	// until its continuations are spent, with nobody deciding anything.
	if run.HarnessContinuesCheckStage() {
		return MoverHarness
	}
	// A first silent-stream stall is continued by the harness itself, once, with
	// nobody deciding anything; a second is hers.
	if run.HarnessContinuesStall() {
		return MoverHarness
	}
	return MoverDevelopmentManager
}

// latestPerItem is the runs a rule matches, one per work item. One item can have
// stopped, or published, more than once; the most recent run is the one that
// describes where the work actually is. Use the store's start-time ordering:
// maintenance of an older record must not make it the latest run.
func latestPerItem(runs []runstate.State, matches func(runstate.State) bool) map[string]runstate.State {
	latest := make(map[string]runstate.State)
	ordered := append([]runstate.State(nil), runs...)
	runstate.NewestFirst(ordered)
	for _, run := range ordered {
		if !matches(run) {
			continue
		}
		if _, seen := latest[run.WorkItemID]; seen {
			continue
		}
		latest[run.WorkItemID] = run
	}
	return latest
}

// stoppage reports a run that stopped on this item and left it for somebody.
// Whether its change survived is deliberately not asked here: that is the
// repository's answer rather than the record's, and the derivation asks for it.
//
// Two endings are one, and reading only the first is what cost yoyodyne-ifd.436.4
// a second run of work that was already approved. A durable blocker is the
// harness having handed the item to somebody, and it is the obvious one. The
// other is a run that died inside its own process: it hands nobody a blocker, on
// purpose, because the harness may yet resume it — so its record ends `failed`
// rather than `stopped` while its change sits on a branch exactly as a
// stoppage's does. run-b0b6d18d ended that way on an approved change the
// environment stopped, and read to the pull as an item with nothing holding it.
// To a reader the two are the same fact, and the hold covers them the same way.
// A run whose check stage its bound stopped is the same fact a third time: it
// ends `timed out` with its finished change on the branch, and the harness
// continues it at its checks rather than anybody starting it over.
func stoppage(run runstate.State) bool {
	if run.WorkItemID == "" || !run.Status.Terminal() {
		return false
	}
	return strings.TrimSpace(run.Blocker) != "" || run.DiedInItsOwnProcess() || run.StoppedAtStageBound()
}

// raiseRerun says why an item a role raised as unmeetable is held once the
// development manager has decided to run it again: the re-run starts from what
// the raising run left, and a pull would start from nothing beside it.
func raiseRerun(run runstate.State) string {
	return fmt.Sprintf(
		"run %s raised it as one that cannot be met as it stands, and a re-run of it that starts from that run's preserved change is decided; a fresh pull would start over beside it",
		run.RunID)
}

// preservedChange says why an item with work still on a branch is not something
// to pull. It names the run because that is what somebody has to go and look at:
// the decision is whether to pick the change up, re-run it, or retire it, and
// none of those is a fresh run started underneath it. It names what was found,
// and how, because that is what a reader about to release the item checks: a
// branch and a worktree checked and there are a different claim from a record
// that says so, and a look that failed is a third.
//
// It stops short of saying whose move that is, as the two publication accounts
// below do, because heldFor closes every one of them with the answer the item's
// own triage record gives.
func preservedChange(run runstate.State, found triage.Found) string {
	if found.Unknown {
		return fmt.Sprintf(
			"run %s stopped on it and %s, so it is held as preserved: a fresh run would start over on top of work that may still be there",
			run.RunID, uncheckable(found))
	}
	return fmt.Sprintf(
		"run %s stopped on it and its change is preserved (%s), so a fresh run would start over on top of work that is still there",
		run.RunID, whatWasFound(found))
}

// whatWasFound is what the look came to, in the words every account of a
// preserved change says it in: what is there, and how that was established. The
// two are said together because they are different claims — a branch checked and
// there is not a branch a record says nothing removed — and a reader about to
// release the item acts on the difference.
func whatWasFound(found triage.Found) string {
	var there []string
	if found.BranchThere {
		there = append(there, "branch")
	}
	if found.WorktreeThere {
		there = append(there, "worktree")
	}
	checked := "checked and there"
	if !found.Looked() {
		checked = "as its record says, nothing having been wired to look"
	}
	return strings.Join(there, " and ") + " " + checked
}

// uncheckable says of a look that failed what it could not establish and why.
func uncheckable(found triage.Found) string {
	return fmt.Sprintf("whether its branch or its worktree is still there could not be checked: %s", found.Unchecked)
}

// stoppedIntegration says why an item whose approved change the environment
// stopped short of its promotion is not something to pull, and who finishes it.
//
// It is its own account rather than the preserved-change one closed by a clause,
// and the ordering is the point: the scheduler cuts a hold's reason to a line
// when it says why an item was passed over, so the fact that decides what
// anybody does — the approval stands, the environment is what stopped it, and
// `yoyo triage resume` is the verb — has to come before the evidence rather than
// after it. Said the other way round, the verb was the part that fell off.
//
// What was found of the change is still said, because it is what a reader about
// to release the item checks; it is last because it is the part that can be cut
// without leaving somebody unable to act.
func stoppedIntegration(run runstate.State, found triage.Found, preserved bool) string {
	account := fmt.Sprintf(
		"run %s stopped on it with its change approved, and the environment is what stopped the promotion, so `yoyo triage resume` finishes it rather than a decision or a fresh run",
		run.RunID)
	switch {
	case !preserved:
		return account
	case found.Unknown:
		return account + " (" + uncheckable(found) + ")"
	default:
		return account + " (" + whatWasFound(found) + ")"
	}
}

// continuedStoppage says why an item whose stopped run left nothing behind is
// still not something to pull: a decision about that stoppage stands recorded
// and the harness has yet to act on it. What a repair grant acts on is the run,
// re-entering the session it preserved, so a fresh pull meanwhile would be a
// second run on the same work, and the first thing the carried-out decision met
// would be the fresh run's claim. A re-run awaiting the harness is said by
// freshRerun instead.
func continuedStoppage(run runstate.State) string {
	return fmt.Sprintf(
		"run %s stopped on it and a decision about that stoppage is recorded and not yet carried out, so a fresh run would start beside the continuation that decision buys",
		run.RunID)
}

// freshRerun says why an item is held whose stopped run left no branch or
// worktree and whose recorded decision is a re-run. Nothing is left for anybody
// to decide: the harness starts the item again from the target branch at the
// next pull with a developer slot free, and holding it until then is what keeps
// an ordinary pull from starting a second run beside that one.
func freshRerun(run runstate.State) string {
	return fmt.Sprintf(
		"run %s stopped on it and no branch or worktree of it remains, and the development manager decided a re-run, so the harness starts it again from the target branch at the next pull with a developer slot free — it waits on the harness rather than on a decision",
		run.RunID)
}

// outstandingPublication reports a run that promoted its item's change and could
// not finish publishing it. All three halves matter, and together they describe
// the one item shape a developer run can do nothing at all with: the promotion
// put the work on the target branch, which is the authoritative one, so there is
// nothing left to implement, and what is unfinished is a publication that only a
// person or a later sweep settles.
//
// A run still in flight owns its own publication and is not this. What is
// deliberately not asked is whether the run's artifacts survived: an integrated
// run cleans its own up, which is exactly why the preserved-change rule above
// misses this and why yoyodyne-ifd.295 was pulled three times, once per
// developer run that then re-derived that the change had already landed.
//
// It says nothing about whether the forge merged anything, which is a separate
// question with three answers and is asked by mergeConfirmed below.
func outstandingPublication(run runstate.State) bool {
	return run.Retirement == nil && run.WorkItemID != "" &&
		run.Status.Terminal() &&
		run.Integration != nil &&
		strings.TrimSpace(run.PublishFailure) != ""
}

// mergeConfirmed reports a publication the forge performed and the harness saw
// it perform. It is what separates a leftover from an unfinished merge, and it
// is asked of the pull request rather than of the promotion, because the
// promotion is local and says nothing about the forge: a merge the forge dropped
// leaves a recorded promotion exactly like a merged one does, and reading that
// as a change the remote carries is the false statement this exists to refuse.
func mergeConfirmed(run runstate.State) bool {
	return run.PullRequest != nil && run.PullRequest.Merged
}

// mergedPublication says why an item whose change is merged everywhere it was
// going is not something to pull. Nothing about the work is unfinished, so the
// only thing a fresh run could do is find that out again.
func mergedPublication(run runstate.State) string {
	return fmt.Sprintf(
		"run %s integrated its change into %s and the forge merged it, so only the publication is unfinished and there is nothing here to implement",
		run.RunID, run.Integration.TargetBranch)
}

// unmergedPublication says why an item the forge has not merged is not something
// to pull either. The work is on the local target branch, which is the
// authoritative one, so a run against it would redo work that has landed; what
// is undecided is the merge, and re-arming one the forge dropped is a bounded
// triage decision rather than a developer's.
func unmergedPublication(run runstate.State) string {
	return fmt.Sprintf(
		"run %s integrated its change into %s and the forge has not merged it, so what is outstanding is the publication rather than the work",
		run.RunID, run.Integration.TargetBranch)
}

// redTargetPublication says why an item whose merge waits on its target's red
// check is not something to pull: the change is reviewed on its kept branch, and
// what it waits on is the target being fixed, which is somebody else's item.
func redTargetPublication(run runstate.State) string {
	return fmt.Sprintf(
		"run %s's change is reviewed and its pull request #%d %s; the harness takes the merge up again once that closes, so nothing here needs a decision",
		run.RunID, run.PullRequest.Number, run.PullRequest.TargetRed.Describe())
}

// undecidedStoppage says why an item whose stoppage nobody has answered is not
// something to pull. The three cases are three different people to go to, which
// is why they are not one sentence: one is waiting on the development manager,
// one is waiting on the harness to finish asking her, and one has run out of
// ways to ask and is waiting on whoever reads this.
func undecidedStoppage(escalation runstate.Escalation) string {
	switch {
	case escalation.Delivered():
		return "its stoppage is in front of the development manager and nothing has been decided about it yet"
	case escalation.Attempts >= runstate.MaxEscalationAttempts:
		return fmt.Sprintf(
			"its stoppage could not be put in front of the development manager after %d attempt(s), so the harness has stopped asking her and it stays undecided on her docket until she decides it",
			escalation.Attempts)
	default:
		return "its stoppage has not reached the development manager yet, so nobody has decided anything about it"
	}
}
