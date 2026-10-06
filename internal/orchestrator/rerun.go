package orchestrator

// Running a docketed stoppage again, on the development manager's decision.
//
// This is the one triage decision the harness carries out. A re-run is what a
// correct change whose ground moved needs: nothing about the work was wrong, so
// there is nothing to repair and nothing to escalate — what it needs is the same
// item attempted again against the repository as it now stands.
//
// The decision is still not this package's. What reaches here is the run the
// docket entry names and nothing else: the decision and the reasoning it was
// made on are read from the item's durable triage record, where the development
// manager's own conversation wrote them. Everything here is the harness acting
// on that — reading whether it may start work at all, proving the stoppage is
// really over, reading that the item is one a run may start on, claiming the one
// re-run that stoppage gets, and starting the run.
//
// # Why the reasoning is read and not given
//
// This verb used to take the reasoning as a flag and record it as the fresh
// run's selection reason, attributed to the development manager. Nothing checked
// those words against anything, because triage decisions were prose in the item's
// notes and prose is not a record. So a run started by hand could carry a
// development-manager attribution nobody in that role wrote — which is precisely
// the thing `selected-work-passes-intake-and-records-why` exists to make
// impossible, weakened by the verb that was meant to satisfy it. The decision is
// durable now, and this reads it: a stoppage with no recorded decision is refused
// naming the missing record, and the attribution the run carries cites the
// decision it was built from.
//
// # Why everything that refuses is asked before the claim
//
// The budget a re-run spends is one per docketed stoppage, and it is spent by
// claiming it rather than by running anything. So a condition that would refuse
// the fresh run and is asked after the claim spends the very budget it refuses:
// that is what a re-run of a blocked item did, where the pipeline refused the
// item's status past the claim and the next attempt was then refused by the
// once-only guard, for a run that had never happened.
//
// The item's own state is the one such condition the harness does not hold in
// its own records, so it is read here from the tracker, and what refuses it is
// the pipeline's own condition rather than a second rendering of it. Everything
// past the claim keeps the opposite order deliberately: a claim taken before the
// run means a process that dies between the two has spent a re-run nobody took
// rather than taken one nobody recorded.
//
// # Why a human gate refuses here and not in the pipeline
//
// A step only a person can take, declared on the item and not yet recorded, is
// asked here as well, and it is the one condition here the pipeline does not ask.
// The pipeline is also the route `yoyo run <id>` takes, and an item the operator
// names is exempt from every hold on the harness's own choosing — naming it is
// them deciding it is the exception, and the gate is their own step to take or to
// waive. A re-run is not that: docs/configuration.md classes it as the harness
// choosing the work, which is why the intake hold applies, and the same
// classification is why the gate does. The scheduler refuses a gated item at the
// pull; this refuses it before the claim. What is left is the operator's own
// route, which is left open on purpose.
//
// # Why a full harness is a state rather than a refusal
//
// Developer capacity is the one condition here that says nothing at all about
// the decision: two developers happening to be busy at this second is not an
// argument against running the item again, and it stops being true on its own.
// So a carry-out that meets it neither fails nor spends anything — it reports
// what it is waiting on and leaves the authorization standing, to be carried out
// by asking again once a slot frees. The item is meanwhile in the open pool the
// scheduler pulls from, which is the other way the same work reaches a
// developer; the two agree because nothing was claimed here.
//
// That is also one of the two things withdrawn past the claim. The slot can go
// to another run between the reading and the reservation, and a claim taken for a
// run the reservation then refused is a re-run spent on a run that provably
// never existed — the reservation refuses before the run's record, the item's
// claim and any agent. So it is given back, and the carry-out reports the same
// waiting state it would have reported a moment earlier.
//
// # Why a fresh run that never existed gives the claim back
//
// The other is everything else the pipeline answers with before it reserves
// anything: a pause — the operator's hold on all activity, an unresolved
// directive, work the item waits on, a held intake — and any refusal at all. Each
// of them comes back as an outcome carrying no run: no run record, no claimed
// item, no agent. That is the same nothing a refused reservation leaves, and it
// has to cost the same — a claim settled against an empty run identifier is a
// stoppage whose next carry-out is refused as already re-run, for a run nobody
// made, which is the contradiction the first exercised re-run met on
// yoyodyne-ifd.125.1. So the claim is given back and what the run met is reported
// in place of it, and asking again once that no longer holds carries out the same
// decision.
//
// The outcome's own run identifier is what says which side of the reservation the
// pipeline stopped on, rather than a list here of the conditions it might have
// stopped for. A refusal that nonetheless names a run is a run that existed and
// did something, so its claim stands and is settled like any other.
//
// Some of those conditions are read here before the claim as well, and that is
// not the same question asked twice: the reading before the claim keeps a harness
// that is already held, or an item no run may start on, from spending anything,
// and this is what covers what arrives while the claim is being taken.
//
// # Why the hold applies
//
// A re-run is the harness choosing work. The development manager naming the item
// is not the operator naming it, and `selected-work-passes-intake-and-records-why`
// draws exactly that line: the exemption belongs to the operator, because naming
// an item is them deciding it is the exception. So the intake hold is read before
// anything is claimed or spent, and a held intake starts nothing — the claim is
// not taken, so the stoppage keeps its one re-run for after the hold is lifted.
// The pipeline reads the hold again where it would start the run, which is the
// enforcement; this reading is what keeps a held harness from spending the
// stoppage's only claim on a run that would then decline to start.
//
// The reason the run records is the development manager's decision and its
// reasoning, which is the other half of the same invariant. A re-run nobody can
// account for looks exactly like work happening behind somebody's back.
//
// # What the developer is given
//
// Nothing here assembles the developer's context. The guidance a development
// manager records for a re-run — what the preserved branch holds, what is worth
// cherry-picking — is written into the work item's notes when the decision is
// recorded, and the item's notes are part of every run's context bundle already.
// That route is deliberate and worth stating so nobody improves it: notes are not
// evidence for a protected-path grant, so guidance travelling this way can never
// widen what the re-run may touch. Carrying it any other way would put the two
// back in the same channel.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// RerunDocket is the docket a re-run is decided against. It is read and never
// written: a re-run settles nothing about the entry, which stands as the record
// that the work stopped however many times it is run again.
type RerunDocket interface {
	List() ([]triage.Entry, error)
}

// RerunRuns is the durable run state this action reads, and — once a retirement
// has actually removed something — writes. The two reads are the architect's
// condition rather than convenience: the stopped run's own record is what proves
// the stoppage is terminal, and what is in flight is what proves the item has no
// live run to collide with.
//
// The write is the other side of the same fact. Everything in the harness that
// asks whether a stopped run's branch and worktree are still there reads the
// flags on its record rather than looking on disk, so a retirement that is not
// written back leaves every one of those readers naming artifacts that are gone.
// It is taken under the stopped run's own lease, which is what AdoptRun exists
// for: a run that is terminal and still owes cleanup is not in flight, and still
// must have exactly one owner while anybody acts on it.
type RerunRuns interface {
	Load(runID string) (runstate.State, error)
	Incomplete() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
}

// RerunDecisions is the harness's own durable record of what triage has decided
// about one work item. It is what proves a re-run was actually decided and what
// says what was decided: the development manager's decision is written there
// with its reasoning and spends the item's re-run budget in the same write, so an
// item whose record holds no re-run decision about this stoppage is an item
// nobody decided this about.
//
// It also takes back the finding a refused carry-out left about the stoppage,
// the moment the decision is carried out. The clearing lives here rather than in
// the pass that fires decisions because this action is the one thing every
// carry-out goes through — the pass and the typed verb alike — and a finding
// cleared only by one of them is a finding the other leaves standing over a run
// that is happening, which reads exactly like the silence the finding exists to
// end.
//
// It is satisfied by runstate.TriageStore.
type RerunDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
	ClearCarryOut(ctx context.Context, workItemID, runID string, at time.Time) (runstate.TriageCounters, error)
}

// RerunRecords is where the one re-run a docketed stoppage gets is claimed and
// settled, and where what has already been carried out for a work item is
// read back. What has been claimed is the other half of the decision gate: a
// claim is what says a decision has been acted on, so counting them is what stops
// one decision authorizing a re-run of every stoppage an item ever has.
//
// It is satisfied by runstate.RerunStore.
type RerunRecords interface {
	Claim(ctx context.Context, rerun runstate.Rerun) (runstate.Rerun, error)
	Settle(ctx context.Context, docketKey, runID string, preserved runstate.PreservedArtifacts) (runstate.Rerun, error)
	Claimed(workItemID string) ([]runstate.Rerun, error)
	// Withdraw gives the claim back, for the case where the run it was taken for
	// provably never existed: the pipeline answered before it reserved anything,
	// whether with a refusal, a pause, or a full harness. See
	// runstate.RerunStore.Withdraw for why that case and no other.
	Withdraw(ctx context.Context, docketKey string) error
}

// RerunGates is the human gates a person has recorded passing, read from the
// harness's own store rather than from the tracker, which cannot answer it: the
// only completion the tracker records is an item being closed, and closure
// passing a step somebody reserved is the failure the gate exists to end. It is
// required for the reason the scheduler requires it — a carry-out that cannot
// read the acts cannot tell a gate somebody passed from one nobody has, and would
// either refuse every gated re-run forever or start past all of them.
//
// It is satisfied by *runstate.Store.
type RerunGates interface {
	DischargedGates() (map[string][]string, error)
}

// RerunItems is the work item the stoppage is about. It is read, and written in
// exactly one case: what becomes of an item is the fresh run's to record, and a
// re-run that reopened what it wanted to run would be deciding the thing it is
// here to carry out somebody else's decision about.
//
// It is the one condition a re-run asks outside the harness's own records, and
// it is asked because a fresh run starts on the item itself. The pipeline reads
// the same tracker where it would claim the item, which is the enforcement; this
// reading is what keeps an item the pipeline would refuse from spending the
// stoppage's only claim on a run that would then decline to start.
//
// The one write is Release, and it is the in-progress counterpart of what the
// pipeline's claim does for a stale blocked status: a claim the stopped run left
// on the item, with that run terminal and nothing of the item in flight, is a
// status nothing is working behind, and it is given back with a note saying so
// rather than left to refuse the decision. See supersedeStaleClaim.
//
// It is satisfied by beads.Client.
type RerunItems interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	Release(ctx context.Context, id, reason string) (beads.WorkItem, error)
}

// PreservedRetirer retires what the stopped run left behind, once the fresh run
// has integrated and what it held has stopped being worth keeping. It is
// optional: an action wired without one starts exactly the same run and records
// the preserved artifacts as kept, which is what they are.
//
// The remote branch is the third artifact and is retired the same way and at the
// same moment, which is why it is on the same interface: what the stopped run
// published is as superseded as what it kept locally.
//
// It is satisfied by gitworktree.Manager.
type PreservedRetirer interface {
	RetirePreserved(ctx context.Context, worktree gitworktree.Worktree, targetBranch string) (gitworktree.Retirement, error)
	SupersededBranches
}

// Rerunner starts a fresh run of an item triage decided to run again. It reads
// the work item and writes to it only to give back a claim the stopped run left
// behind (see RerunItems), it has no forge access, and it decides
// nothing about the work: what it does is check that a decision somebody else
// made may be carried out, and then carry it out.
type Rerunner struct {
	Docket RerunDocket
	Runs   RerunRuns
	Intake IntakeHolds
	Reruns RerunRecords
	// Decisions is the item's durable triage record, which is what says the
	// development manager decided this at all. Required: a re-run carried out on
	// nobody's decision would record the development manager as having chosen
	// work it never looked at.
	Decisions RerunDecisions
	// Items is the work item the fresh run would start on. Required: the pipeline
	// asks the same question past the claim, so a re-run that could not ask it
	// here would go on spending the stoppage's one re-run to find out.
	Items RerunItems
	// Gates is the human gates a person has passed. Required; see RerunGates for
	// why a re-run without one is refused rather than run.
	Gates RerunGates
	// Capacity is execution.max_concurrent_developers as this carry-out read it,
	// which is the same number the reservation enforces. Required: a carry-out
	// that could not tell a full harness from a free one would go on spending the
	// stoppage's re-run to find out there was no slot for it.
	Capacity int
	// Preserved retires the stopped run's branch and worktree once the fresh run
	// integrates. Optional; see PreservedRetirer.
	Preserved PreservedRetirer
	// Remains asks the repository what the stopped run still holds.
	Remains readmodel.Remains
	// Publications closes the pull request the stopped run left open, at the same
	// moment and for the same reason: the fresh run has integrated, so that
	// request carries work that has landed by another vehicle and will never
	// merge. Optional — a project that does not publish has none, and an action
	// wired without one leaves the request to the convergence sweep, which finds
	// it either way. It is wired because the sweep is not continuous, and the
	// close-by-hand practice this replaced missed four such requests inside six
	// hours of heavy triage.
	Publications SupersededPublications
	Start        Starter
	Clock        execution.Clock
}

// RerunRequest is one decision to carry out: the run the docket entry names, and
// nothing else. The reasoning is not asked for and cannot be given — it is read
// from the decision the development manager recorded, because a run that took it
// from whoever typed the command would carry an attribution to a role that never
// wrote those words.
type RerunRequest struct {
	Run string
	// AheadOf is a sentence the pass that fired this adds to the run's reason
	// where it put the decision ahead of higher-priority ready work, naming that
	// work. Empty for every other re-run.
	AheadOf string
}

// RerunResult is what the action did. It reports the run it started and what
// became of it, and it reports just as carefully when it started nothing: an
// intake hold and a refusal are different things for an operator to do something
// about, and neither is a run that failed.
type RerunResult struct {
	WorkItemID string `json:"work_item_id"`
	PriorRunID string `json:"prior_run_id"`
	DocketKey  string `json:"docket_key"`
	// Reason is what the fresh run recorded as why it exists, which is the
	// development manager's decision and its reasoning rather than this package's
	// account of either.
	Reason  string `json:"reason"`
	Started bool   `json:"started"`
	// IntakeHeld is the operator's hold, when one is what stopped this. Nothing
	// was claimed and the stoppage keeps its re-run.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// CapacityFull is every developer slot being occupied, when that is what this
	// is waiting on. It is not a refusal and not a failure: nothing was claimed,
	// the decision stands until it is carried out or the development manager
	// withdraws it, and asking again once a slot frees carries out the same one.
	CapacityFull *runstate.CapacityError `json:"capacity_full,omitempty"`
	// PausedBeforeStarting is the pause the fresh run met where it would have
	// started, when one is what stopped this: the operator's hold on all activity,
	// an unresolved directive, work the item waits on, or a held intake. It is the
	// pipeline's own outcome, so it names which of them it was. Nothing was
	// reserved and no run exists, so the claim taken for it was given back and the
	// stoppage keeps its re-run.
	PausedBeforeStarting *Outcome `json:"paused_before_starting,omitempty"`
	// ClaimGivenBack says the fresh run was reserved and then refused by the
	// environment before any agent of it ran, so the claim taken for it was given
	// back and the stoppage keeps its re-run. The run itself happened and is
	// reported below; what did not happen is anything the claim was spent on.
	ClaimGivenBack bool `json:"claim_given_back,omitempty"`
	// SupersededClaim is the note the item was given when the claim the stopped
	// run left on it was released so the fresh run could start, and empty where
	// the item was not left claimed.
	SupersededClaim string  `json:"superseded_claim,omitempty"`
	Outcome         Outcome `json:"outcome"`
	// Preserved is what the stopped run left behind and what became of it.
	Preserved runstate.PreservedArtifacts `json:"preserved"`
	// Publication is the pull request the stopped run left open and what became
	// of it, present only where there was one to retire. It is beside Preserved
	// rather than part of it because it is the one artifact that is not in this
	// repository: a reader acting on it goes to the forge.
	Publication *PublicationRetirement `json:"publication,omitempty"`
	// RecordProblem names a durable record this action could not update after the
	// run. The run happened either way, so it is reported beside the result rather
	// than in place of it — but a disposition nobody wrote down is exactly the
	// orphan this record exists to prevent, so it is never left unsaid.
	RecordProblem string `json:"record_problem,omitempty"`
}

// Rerun carries out one re-run decision.
//
// The order is the order the guarantees need. Everything that can refuse is
// asked before anything is claimed or started, so a refused re-run costs the
// stoppage nothing; the claim is taken before the run is started, so a process
// that dies between the two has spent a re-run nobody took rather than taken one
// nobody recorded; and what became of the preserved artifacts is recorded after
// the run, because until it ends there is nothing to decide about them.
func (r Rerunner) Rerun(ctx context.Context, request RerunRequest) (RerunResult, error) {
	if err := r.validate(); err != nil {
		return RerunResult{}, err
	}
	priorRunID := strings.TrimSpace(request.Run)
	if !runstate.ValidRunID(priorRunID) {
		return RerunResult{}, fmt.Errorf("re-run %q is not a run identifier; a triage decision names the run the docket entry is about", request.Run)
	}

	entry, err := r.entry(priorRunID)
	var missing NoDocketedStoppageError
	undocketed := errors.As(err, &missing)
	if undocketed {
		entry, err = r.undocketedEntry(priorRunID, missing)
	}
	if err != nil {
		return RerunResult{}, err
	}
	result := RerunResult{
		WorkItemID: entry.WorkItemID,
		PriorRunID: entry.RunID,
		DocketKey:  entry.Key,
	}
	prior, err := r.Runs.Load(entry.RunID)
	if err != nil {
		return result, fmt.Errorf("read the run the docket entry is about: %w", err)
	}
	// The entry and the removal flags describe earlier readings. Ask the
	// repository what remains now before deciding what this re-run can carry.
	found := readmodel.LookFor(ctx, r.Remains, prior)
	result.Preserved = preservedOf(prior, found)
	// The docket says what was true when the entry was made. What decides whether
	// this stoppage may be run again is what is true now, so the run's own record
	// is asked rather than the entry that describes it. Both this and the run in
	// flight below describe something that is still moving, so both stop being
	// true on their own — which is what a reader of either refusal has to act on,
	// and why each of them says the stoppage kept its re-run.
	// A run the docket never held is re-run on the development manager's decision
	// alone: it ended without anything the stoppage rule counts, which is why it
	// was never docketed, so asking that rule of it could only refuse what she
	// decided. It still has to have ended.
	if undocketed {
		if !prior.Status.Terminal() {
			return result, unspentRefusal(fmt.Errorf("run %s is recorded as %s rather than ended, so it is owed a continuation rather than a fresh run; a re-run is refused while anything of it is resumable",
				prior.RunID, prior.Status))
		}
	} else if err := rerunnable(prior, found); err != nil {
		return result, unspentRefusal(err)
	}
	if err := r.noRunInFlight(entry.WorkItemID); err != nil {
		return result, unspentRefusal(err)
	}
	// That the development manager decided this is read rather than taken on
	// trust, and so is what they decided it on. The reason this run records names
	// that role, so both the decision and the words attributed to it come from the
	// record the role wrote rather than from whoever asked for the carry-out.
	decided, taken, decision, err := r.decided(entry)
	if err != nil {
		return result, err
	}
	result.Reason = rerunReason(entry, decided, taken, decision)
	result.Reason = withAheadOf(result.Reason, request.AheadOf)
	// The item is read before the claim, for the reason the hold below is: a fresh
	// run starts on the item itself, so an item the pipeline would refuse must not
	// spend the stoppage's one re-run on finding that out.
	item, err := r.itemCanBeRun(ctx, entry.WorkItemID)
	if err != nil {
		return result, err
	}
	// A raise is re-run only once the item's owner has amended it and released
	// the parking the raise placed: the raise said the item could not be met as
	// it stood, so running it again as it stands would be the run the raise was
	// raised to save. The release is the owner's act and the one the tracker
	// records, so it is what is read.
	if prior.Escalated() {
		if err := raiseReleased(item, prior); err != nil {
			return result, unspentRefusal(err)
		}
	}
	// The hold is read before the claim, so a held harness leaves the stoppage its
	// one re-run rather than spending it on a run that would decline to start.
	hold, held, err := r.Intake.Held()
	if err != nil {
		return result, fmt.Errorf("read whether intake is held: %w", err)
	}
	if held {
		result.IntakeHeld = &hold
		return result, nil
	}
	// Capacity is read last of everything asked before the claim, because it is
	// the condition most likely to have changed while the rest were being asked
	// and the one a moment's wait can settle. A full harness is reported rather
	// than refused: the decision stands, and nothing was claimed to carry it out
	// with.
	full, free, err := r.slotIsFree()
	if err != nil {
		return result, err
	}
	if !free {
		result.CapacityFull = &full
		return result, nil
	}
	// A claim the stopped run left on the item is given back last of everything
	// before the re-run's own claim, because it is the one write this carry-out
	// makes to the item and everything that could refuse has already been asked.
	// The fresh run claims the item again as it starts.
	if item.Status == claimedItemStatus {
		note, err := r.supersedeStaleClaim(ctx, entry, prior, decision)
		if err != nil {
			return result, unspentRefusal(err)
		}
		result.SupersededClaim = note
	}

	claimed, err := r.Reruns.Claim(ctx, runstate.Rerun{
		DocketKey:  entry.Key,
		PriorRunID: entry.RunID,
		WorkItemID: entry.WorkItemID,
		Reason:     result.Reason,
		ClaimedAt:  r.now(),
		Preserved:  result.Preserved,
	})
	if err != nil {
		// A claim refused after the stale one was given back — a concurrent
		// carry-out of the same stoppage taking it first, say — leaves the item open
		// with a note promising a fresh run this carry-out will not start. Open is a
		// state a run may start on and the note says what moved it, so nothing is
		// lost, but the refusal says so rather than leaving the promise unexplained.
		if result.SupersededClaim != "" {
			return result, fmt.Errorf("%w; the claim run %s left on %s had already been released, so the item now reads open with a note saying a fresh run claims it, and no fresh run was started by this carry-out",
				err, entry.RunID, entry.WorkItemID)
		}
		return result, err
	}
	result.Preserved = claimed.Preserved

	result.Started = true
	// The finding a refused carry-out left is taken back here, at the claim rather
	// than after the run: a finding that stood for the length of the run would have
	// the docket say the decision is not happening while it runs. A refusal past
	// this point writes a fresh finding of its own.
	result.note(clearCarryOutFinding(ctx, r.Decisions, entry.WorkItemID, entry.RunID, r.now()))
	outcome, runErr := r.Start(ctx, entry.WorkItemID, runstate.Selection{
		By:     runstate.SelectedByDevelopmentManager,
		Reason: result.Reason,
		At:     r.now(),
		Lift:   liftOf(prior, result.Preserved),
	})
	// The last free slot going to another run between the reading above and the
	// reservation is the same waiting state read a moment too late, and it has to
	// cost the same: the claim taken for a run that never existed is given back,
	// and there is nothing to settle behind it.
	if capacity, refused := capacityRefused(outcome, runErr); refused {
		return r.withdraw(ctx, entry, capacity, result), nil
	}
	// A pause met where the fresh run would have started is the same nothing: the
	// hold, the directive, the dependency and the intake hold all stop the pipeline
	// before it reserves a run, claims the item, or invokes an agent. The claim
	// taken for it is given back and the pause is reported in place of the run, so
	// the next carry-out of the same decision meets the pause rather than the
	// once-only guard.
	if pausedBeforeStarting(outcome) {
		return r.withdrawPaused(ctx, entry, outcome, result), runErr
	}
	// And so is every other refusal made before the reservation, which is the case
	// the two above are particular kinds of: the pipeline asks the item's state,
	// the repository's, and the provider's before it reserves anything, and any of
	// them can have changed since this action asked its own questions. Nothing was
	// reserved, so the claim goes back and the refusal is reported saying so.
	if refusedBeforeStarting(outcome, runErr) {
		return r.withdrawRefused(ctx, entry, runErr, result)
	}
	// A fresh run that was reserved and then refused by the environment before any
	// agent of it ran is the same nothing one step later: its record exists and
	// says what stopped it, and no developer was invoked and no change delivered.
	// The claim was spent on the machine having been too busy rather than on the
	// work, so it goes back and the stoppage keeps the re-run its decision
	// authorized. A run the provider's usage window stopped is the same nothing
	// for the same reason: whatever it produced, nothing judged it. Everything
	// else is settled, whatever the run came to.
	if refusedBeforeAnythingRan(outcome) || stoppedByUsageWindow(outcome.Environmental) {
		return r.withdrawEnvironmental(ctx, entry, outcome, result), runErr
	}
	result.Outcome = outcome
	// A publication nothing ever asked the forge to merge is handed back by this
	// re-run, whatever the fresh run came to: the decision has been carried out,
	// so the request stops being something anybody waits on or decides.
	if prior.PublicationUnasked() {
		result.note(r.handBackPublication(ctx, entry, prior.RunID, result.Reason))
	}
	result.Preserved = r.settle(ctx, entry, prior, outcome, &result)
	return result, runErr
}

// handBackPublication records on the prior run that its publication — a request
// nothing ever asked the forge to merge — was handed back for a fresh run, and
// reports what it could not record.
//
// It is written onto the run's own record, under that run's lease, because that
// record is what every reading of the publication starts from: the docket, the
// status line's attention entry, and the heartbeat's count of what awaits the
// forge all stop naming it once it is marked. The request itself is left open on
// the forge until the fresh run lands, which is when settle retires it as
// superseded; until then what keeps it from being mistaken for work still
// waiting is the mark.
func (r Rerunner) handBackPublication(ctx context.Context, entry triage.Entry, runID, reason string) string {
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	state, lease, err := r.Runs.AdoptRun(write, runID)
	if err != nil {
		return fmt.Sprintf("the publication of run %s was handed back for a fresh run and its record could not be taken to say so, so it may still be named as waiting on a decision: %v", runID, err)
	}
	defer func() { _ = lease.Release() }()
	if !state.PublicationUnasked() {
		return ""
	}
	published := *state.PullRequest
	published.HandedBack = &runstate.PublicationHandBack{At: r.now(), DocketKey: entry.Key, Reason: reason}
	state.PullRequest = &published
	state.UpdatedAt = r.now()
	if err := r.Runs.Save(state); err != nil {
		return fmt.Sprintf("the publication of run %s was handed back for a fresh run and its record could not be saved saying so, so it may still be named as waiting on a decision: %v", runID, err)
	}
	return ""
}

// entry finds the docketed stoppage a decision is about. A run that is not on
// the docket is refused rather than run: the docket entry is what a re-run is
// counted against, so a re-run of something nothing docketed would be a re-run
// nothing bounds.
//
// A publication nothing ever asked the forge to merge is the one other entry a
// re-run answers. It stopped nothing and so is not docketed as a stoppage, and
// it is the development manager's to decide all the same: arming it is one
// answer, and handing the change back for a fresh run is the other. Whether the
// run's record still says nothing was asked is stoppageIsOver's to check, off
// the record rather than the entry.
//
// A re-run is the one carry-out that answers a raise as well as a stoppage: an
// item a role raised as unmeetable is docketed as that raise rather than as a
// stopped run, because the run that raised it succeeded at what it was for, and
// running it again once its owner has amended it is one of the two decisions
// that answer one. A stopped run's entry is taken first where a run has both.
func (r Rerunner) entry(priorRunID string) (triage.Entry, error) {
	entry, err := docketedStoppage(r.Docket, priorRunID, "run again")
	if err == nil {
		return entry, nil
	}
	raise, found, raiseErr := docketedRaise(r.Docket, priorRunID)
	if raiseErr != nil {
		return triage.Entry{}, raiseErr
	}
	if found {
		return raise, nil
	}
	entries, listErr := r.Docket.List()
	if listErr != nil {
		return triage.Entry{}, err
	}
	for _, candidate := range entries {
		if candidate.Class == triage.ClassPublication && candidate.RunID == priorRunID {
			return candidate, nil
		}
	}
	return triage.Entry{}, err
}

// undocketedEntry is the entry a re-run of a run the docket never held is
// claimed under: the stopped-run key the docket would have given it, so the
// once-per-stoppage claim holds for it exactly as for a docketed one. A run the
// harness cancelled on its way out is never docketed, and a decision about one
// used to be refused at every pass for want of an entry — yoyodyne-ifd.187's
// re-run of run-04e578ce, thirty-nine times — although what the development
// manager decided is plain: start the item again (yoyodyne-ifd.428.52). A run
// the harness holds no record of, or one of no work item, is still refused.
func (r Rerunner) undocketedEntry(priorRunID string, missing NoDocketedStoppageError) (triage.Entry, error) {
	prior, err := r.Runs.Load(priorRunID)
	if err != nil || strings.TrimSpace(prior.WorkItemID) == "" {
		return triage.Entry{}, missing
	}
	recorded := prior.StartedAt
	if prior.CompletedAt != nil {
		recorded = *prior.CompletedAt
	}
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, prior.RunID),
		Class:         triage.ClassStoppedRun,
		ProductID:     prior.ProductID,
		RunID:         prior.RunID,
		WorkItemID:    prior.WorkItemID,
		WorkItemTitle: prior.WorkItemTitle,
		RecordedAt:    recorded,
	}, nil
}

// docketedRaise finds the raise one run put on the docket, and whether there is
// one at all.
func docketedRaise(docket RerunDocket, priorRunID string) (triage.Entry, bool, error) {
	entries, err := docket.List()
	if err != nil {
		return triage.Entry{}, false, fmt.Errorf("read the triage docket: %w", err)
	}
	for _, candidate := range entries {
		if candidate.Class == triage.ClassEscalation && candidate.RunID == priorRunID {
			return candidate, true, nil
		}
	}
	return triage.Entry{}, false, nil
}

// rerunnable is stoppageIsOver widened by the one run a re-run answers that did
// not stop: a run that ended by raising its item as unmeetable. Such a run
// succeeded — raising is what it was for — and carries no blocker, so the
// stoppage rule refuses it; what makes it something a person decides about is
// the raise itself, read from the run's own record.
func rerunnable(prior runstate.State, found triage.Found) error {
	if prior.Status.Terminal() && prior.Escalated() {
		return nil
	}
	return stoppageIsOver(prior, found)
}

// raiseReleased reports the item a raise parked having been released by its
// owner, which is the owner saying the item has been amended so that it can be
// met. A parking somebody else placed since holds the re-run just the same:
// whatever it waits on, the item is not to be started until it is released.
func raiseReleased(item beads.WorkItem, prior runstate.State) error {
	if !item.Parking.Parked() {
		return nil
	}
	if raisedBy, raised := runstate.RaisedBy(item.Parking.Reason()); raised && raisedBy == prior.RunID {
		return fmt.Errorf("run %s raised %s as one that cannot be met as it stands, and the item is still parked by that raise; a re-run of a raise waits for the item's owner to amend it and release the parking",
			prior.RunID, item.ID)
	}
	return fmt.Errorf("%s is parked (%s), and a re-run of the raise run %s made is not started until the item is released",
		item.ID, singleLine(item.Parking.Reason(), 300), prior.RunID)
}

// liftOf is the preserved change a re-run of a raise starts from, where one
// stands: the branch the raising run's record says is still there. A re-run of a
// stopped run starts from the target as it always has — the ground moved under
// that change, which is why it is being run again — and so does a raise whose
// branch is gone.
func liftOf(prior runstate.State, preserved runstate.PreservedArtifacts) *runstate.Lift {
	if !prior.Escalated() || strings.TrimSpace(preserved.Branch) == "" {
		return nil
	}
	return &runstate.Lift{RunID: prior.RunID, Branch: preserved.Branch}
}

// docketedStoppage finds the docketed stoppage of one run, for whichever action
// is about to act on it. act names what the caller would do, so a refusal reads
// as the thing that was refused rather than as a lookup that came back empty.
func docketedStoppage(docket RerunDocket, priorRunID, act string) (triage.Entry, error) {
	entries, err := docket.List()
	if err != nil {
		return triage.Entry{}, fmt.Errorf("read the triage docket: %w", err)
	}
	for _, candidate := range entries {
		if candidate.Class == triage.ClassStoppedRun && candidate.RunID == priorRunID {
			return candidate, nil
		}
	}
	return triage.Entry{}, NoDocketedStoppageError{RunID: priorRunID, Act: act}
}

// NoDocketedStoppageError is the refusal of an action asked to act on a run the
// docket holds no stoppage of. It is typed because what the carry-out writes
// onto the item about it is more than the refusal: a decision recorded against
// such a run can never be carried out as it stands, so the finding has to name
// the decision that would apply instead (yoyodyne-ifd.428.52).
type NoDocketedStoppageError struct {
	RunID string
	Act   string
}

func (e NoDocketedStoppageError) Error() string {
	return fmt.Sprintf("no stopped run of %s is on the triage docket, so there is no stoppage to %s", e.RunID, e.Act)
}

// stoppageIsOver reports the run's own record proving the stoppage is terminal
// and still standing. Both halves are the condition: a run still in flight is
// owed the rest of its own step, and one that ended with nothing anybody has to
// decide about is not a stoppage at all.
//
// The docket and the decision establish which stoppage this action may carry
// out. A blocker can survive removal of the change, and a change can survive a
// run that recorded no blocker. Ask the repository for that second fact rather
// than excluding terminal statuses whose flags or failure text happen to differ.
func stoppageIsOver(prior runstate.State, found triage.Found) error {
	if !prior.Status.Terminal() {
		return fmt.Errorf("run %s is recorded as %s rather than ended, so it is owed a continuation rather than a fresh run; a re-run is refused while anything of it is resumable",
			prior.RunID, prior.Status)
	}
	if strings.TrimSpace(prior.Blocker) == "" && !found.Holds() && !prior.PublicationUnasked() {
		return fmt.Errorf("run %s ended carrying no durable blocker and left no change behind, so nothing about it stopped for a person to decide: %s", prior.RunID, found.Describe())
	}
	return nil
}

// decided reports a decision of the development manager's that this re-run may
// carry out, and refuses where there is none. It is two questions and the second
// is what makes the first mean anything.
//
// The item's re-run counter is the decision's own footprint: the development
// manager spends it as the decision is recorded and before anything acts on it,
// so an item carrying none is an item nobody decided this about. But the counter
// is a total and is never cleared, so on its own it cannot tell a decision that
// is waiting to be carried out from one that was carried out last month — and
// reading it that way would let a second stoppage of an already re-run item start
// a whole second run on the strength of the first decision, past the one-per-item
// bound and under an attribution that describes a decision about a different
// stoppage.
//
// So each decision authorizes exactly one re-run, and what has been claimed for
// the item is read back against what has been decided. An item whose decisions
// are all spent is refused: a further stoppage of it needs a further decision,
// which past the cap is an escalation rather than a larger budget — which is
// exactly what the development manager's own workflow says.
//
// The count is read before the claim is taken, so two processes carrying out one
// decision at the same instant could both pass it. What that costs is bounded by
// the reservation rather than by this: the second run of one item is refused
// where it is reserved, so the loser spends a claim rather than putting a second
// developer on the work.
//
// What it returns is the whole record rather than the one counter, because the
// reason this run records has to be able to say what the decision stands on: an
// item whose caps an operator crossed was re-run past a bound the harness would
// otherwise have refused, and the run's own selection reason is where that
// survives the conversation it was decided in.
//
// The decision itself is the first thing asked, and it is what the run's
// attribution is built from. A spent counter says somebody decided something
// about this item; the decision says it was this stoppage, this role, this
// conversation and these words. An item whose budget was spent on a decision
// about some other run of it is refused here, and so is one whose standing
// decision is a wait or an escalation rather than a re-run — a re-run started on
// either would be attributed to a decision nobody made.
func (r Rerunner) decided(entry triage.Entry) (decided runstate.TriageCounters, taken int, decision runstate.TriageDecision, err error) {
	workItemID := entry.WorkItemID
	counters, err := r.Decisions.Counters(workItemID)
	if err != nil {
		return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)
	}
	recorded, found := counters.DecisionOf(entry.RunID)
	if !found {
		// An item whose re-run was decided before decisions were durable is the one
		// case where the budget says decided and the record says nothing, and it is
		// worth naming: the decision has to be recorded again, which spends a
		// further re-run, and past the cap that is an operator's override to permit.
		return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, permanentCarryOut(triage.CarryOutDecisionMissing, fmt.Errorf(
			"the development manager has recorded no triage decision about the stoppage of run %s, so there is nothing here to carry out: a re-run carries the decision the record holds rather than words given to this command, and the decision is recorded where it is made, in the development manager's own conversation. Recording it there spends a further re-run of %s, which the cap may refuse — `yoyo triage override` is what permits that",
			entry.RunID, workItemID))
	}
	if recorded.Decision != runstate.TriageDecisionRerun {
		return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, permanentCarryOut(triage.CarryOutDecisionSuperseded, fmt.Errorf(
			"the decision standing about the stoppage of run %s is %q rather than a re-run, %s: carrying this out would attribute a fresh run to a decision nobody made",
			entry.RunID, recorded.Decision, recorded.Cite()))
	}
	if counters.Reruns < 1 {
		return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, fmt.Errorf(
			"a re-run of %s is recorded as decided and the item's re-run budget shows none spent, so its durable record disagrees with itself and nothing here is safe to carry out: the decision spends the budget as it is recorded, in one write",
			workItemID)
	}
	claimed, err := r.Reruns.Claimed(workItemID)
	if err != nil {
		return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, fmt.Errorf("read the re-runs already taken of %s: %w", workItemID, err)
	}
	// This stoppage having been re-run already is the more particular answer, and
	// the one whose refusal can say what became of it, so it is given rather than
	// the arithmetic below. The claim itself refuses this too; asking here is what
	// makes the refusal name the run the first re-run started.
	for _, existing := range claimed {
		if existing.DocketKey == entry.Key {
			return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, runstate.RerunTakenError{Existing: existing}
		}
	}
	if len(claimed) >= counters.Reruns {
		return runstate.TriageCounters{}, 0, runstate.TriageDecision{}, fmt.Errorf(
			"triage has decided %d re-run(s) of %s and the harness has carried out %d, so this stoppage has no decision of its own to act on: a further stoppage of an item that has already been run again needs a further decision, which past the cap is an escalation rather than a larger budget",
			counters.Reruns, entry.WorkItemID, len(claimed))
	}
	return counters, len(claimed), recorded, nil
}

// unspentRefusal says what a refusal made before the claim cost the stoppage,
// which is nothing. It is worth saying rather than leaving implied: what the
// refusals before the claim have in common is that the condition can clear, and a
// reader who cannot tell whether asking again is worth anything has to go and
// read the record to find out. The conditions these refusals are about are the
// shared ones, so the sentence is added where the re-run asks them rather than
// inside a helper the repair action asks the same question through.
func unspentRefusal(err error) error {
	return fmt.Errorf("%w; nothing was claimed, so the stoppage keeps its re-run — asking again once that is no longer so carries out the same decision", err)
}

// ErrItemNotStartable is what a triage carry-out refused for the work item's own
// state unwraps to, so a caller can tell "somebody has to put the item back" from
// a tracker that would not answer without matching on the words of either. It is
// shared by both carry-outs because it is one condition: a blocked item is
// neither one a fresh run may start on nor one a stopped run may be resumed on.
var ErrItemNotStartable = errors.New("the work item is not in a state a run may start or resume on")

// itemCanBeRun reports the work item being in a state a fresh run may start on,
// which for a docketed stoppage ordinarily means somebody has put it back: a run
// that stopped on a durable blocker blocked its item, and a blocked item is not
// one the pipeline starts work on.
//
// What it asks is the pipeline's own condition rather than a second rendering of
// it, so this refusal and the one the fresh run would make can never drift apart.
// The refusal says what has to become true, because that is the whole of what
// this being free is worth: the stoppage keeps its re-run, so the same decision
// is carried out by asking again once the item has been put back.
//
// A claim is the one status read more widely here than the pipeline reads it,
// and only because this action gives it back before the fresh run starts. By
// the time this is asked, the stopped run has been proved terminal and nothing
// of the item is in flight, so an item still reading in_progress is holding a
// claim nothing is working behind — the in-progress twin of a stale blocked
// status, which the pipeline's claim already corrects. Refusing it was what
// stood yoyodyne-ifd.429.3's recorded re-run off four times over run-7dda71fb: the
// claim audit leaves a claim standing while the run's branch survives, this
// refused an item that was claimed, and nothing else in the harness moves the
// status. Everything else the pipeline refuses an item for is still refused.
//
// What it asks after the pipeline's condition is the item's human gates, which
// the pipeline does not ask and the scheduler does; the package comment says why
// the line falls there. An undischarged gate refuses in the same words the queue
// holds the item with, so the development manager reads the same sentence here
// as on the backlog, and the stoppage keeps its re-run for after the act is
// recorded.
func (r Rerunner) itemCanBeRun(ctx context.Context, workItemID string) (beads.WorkItem, error) {
	item, err := r.Items.Show(ctx, workItemID)
	if err != nil {
		return beads.WorkItem{}, fmt.Errorf("read the work item the stoppage is about: %w", err)
	}
	statuses := startableStatuses
	if item.Status == claimedItemStatus {
		statuses = []string{claimedItemStatus}
	}
	if err := validateWorkItem(item, workItemID, statuses...); err != nil {
		return beads.WorkItem{}, fmt.Errorf("%w: %w, which is what a fresh run of it would start from; nothing was claimed, so the stoppage keeps its re-run — put the item back in a state a run may start on and ask again to carry out the same decision",
			ErrItemNotStartable, err)
	}
	discharged, err := r.Gates.DischargedGates()
	if err != nil {
		return beads.WorkItem{}, fmt.Errorf("read the human gates a person has passed: %w", err)
	}
	if gates := humangate.Of(item).Pending(discharged[item.ID]); gates.Holds() {
		return beads.WorkItem{}, fmt.Errorf("work item %s is %s; nothing was claimed, so the stoppage keeps its re-run — a re-run is the harness choosing work, and no decision of the development manager's passes a step reserved for a person",
			item.ID, gates.Describe(item.ID))
	}
	return item, nil
}

// claimedItemStatus is the tracker status of work a run has claimed.
const claimedItemStatus = "in_progress"

// supersedeStaleClaim gives back the claim the stopped run left on the item, so
// the fresh run the decision authorizes can claim it, and reports the note the
// item was given saying what moved it and why.
//
// What makes the claim stale is asked again here rather than carried from the
// readings before it, because this is the write that acts on it: the stopped
// run is terminal, which nothing takes back, and no run of the item is in
// flight. A run in flight is the claim's live holder, and giving its claim back
// would put two developers on one piece of work, so that is refused exactly as
// the reading before it refuses. Between this reading and the release nothing
// can start a run of the item either, because a claimed item is not one the
// pipeline starts on.
//
// Release verifies the status it wrote, so an item that did not come back open
// is reported rather than handed to a fresh run the pipeline would refuse.
func (r Rerunner) supersedeStaleClaim(ctx context.Context, entry triage.Entry, prior runstate.State, decision runstate.TriageDecision) (string, error) {
	if !prior.Status.Terminal() {
		return "", fmt.Errorf("%s is claimed and its stopped run %s is recorded as %s rather than ended, so the claim may be that run's and is not given back",
			entry.WorkItemID, prior.RunID, prior.Status)
	}
	if err := noRunInFlight(r.Runs, entry.WorkItemID); err != nil {
		return "", fmt.Errorf("%s is claimed by a run in flight, so its claim is not given back: %w", entry.WorkItemID, err)
	}
	note := fmt.Sprintf(
		"The harness released this item's in_progress claim to carry out the development manager's re-run of the stoppage of run %s (%s): that run ended as %s and no run of this item is in flight, so the claim was left over from it rather than held by anything working on the item. The fresh run claims the item again as it starts.",
		prior.RunID, decision.Cite(), prior.Status)
	if _, err := r.Items.Release(ctx, entry.WorkItemID, note); err != nil {
		return "", fmt.Errorf("give back the claim run %s left on %s, which a fresh run has to take: %w", prior.RunID, entry.WorkItemID, err)
	}
	return note, nil
}

// noRunInFlight refuses a re-run of an item something is already running. The
// reservation refuses a second run of one item anyway; this is the same rule
// asked before anything is claimed, so a collision costs the stoppage's re-run
// nothing.
func (r Rerunner) noRunInFlight(workItemID string) error {
	return noRunInFlight(r.Runs, workItemID)
}

// noRunInFlight is the same rule for every triage action that would put a
// developer on an item: an item something is already running is not work that
// has stopped, whatever the docket entry said when it was written.
func noRunInFlight(runs RerunRuns, workItemID string) error {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return fmt.Errorf("read what is already in flight: %w", err)
	}
	for _, state := range incomplete {
		if state.WorkItemID == workItemID {
			return fmt.Errorf("%s already has run %s in flight in status %s, so it is not work that has stopped",
				workItemID, state.RunID, state.Status)
		}
	}
	return nil
}

// slotIsFree reports a developer slot being free for the fresh run, and reports
// a full harness as a state rather than as a refusal.
//
// Capacity is the one condition here that is about the moment rather than about
// the work: two developers being busy at this second says nothing about whether
// the item should be run again, and it stops being true without anybody doing
// anything. A carry-out that failed on it would make a recorded decision of the
// development manager's weaker than an ordinary scheduler poll, which simply
// comes back — so this waits instead, and waiting costs the stoppage nothing
// because it is asked before the claim.
//
// The same number the reservation enforces is counted the same way, from the
// runs in flight, so what is read here and what would refuse the fresh run are
// one fact rather than two.
func (r Rerunner) slotIsFree() (runstate.CapacityError, bool, error) {
	return slotIsFree(r.Runs, r.Capacity)
}

// slotIsFree counts the runs in flight against the configured limit, the same
// way and from the same records the reservation does, so what a triage action
// reads before it spends anything and what would refuse the run it starts are
// one fact rather than two.
func slotIsFree(runs RerunRuns, capacity int) (runstate.CapacityError, bool, error) {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return runstate.CapacityError{}, false, fmt.Errorf("read what is already in flight: %w", err)
	}
	if holding := runstate.HoldingDeveloperSlots(incomplete); holding >= capacity {
		return runstate.CapacityError{Limit: capacity, Active: holding}, false, nil
	}
	return runstate.CapacityError{}, true, nil
}

// capacityRefused reports the fresh run having been refused a developer slot and
// nothing else having happened. Both halves are the condition: the reservation
// is what refuses for capacity and it is taken before the run's record exists,
// before the work item is claimed and before any agent runs, so a refusal that
// nonetheless names a run is not this and is left to be settled like any other
// run that ended badly.
func capacityRefused(outcome Outcome, err error) (runstate.CapacityError, bool) {
	var capacity runstate.CapacityError
	if !errors.As(err, &capacity) || outcome.RunID != "" {
		return runstate.CapacityError{}, false
	}
	return capacity, true
}

// pausedBeforeStarting reports the fresh run having met a pause where it would
// have started, and nothing else having happened. The missing run identifier is
// what makes it that rather than a run that paused: the pauses that stop work
// before it is claimed carry no run, and a paused outcome naming one describes a
// run in flight that this carry-out neither started nor may give a claim back for.
func pausedBeforeStarting(outcome Outcome) bool {
	return outcome.Paused && outcome.RunID == ""
}

// refusedBeforeStarting reports the fresh run having been refused before it was
// reserved. The missing run identifier is the whole of the condition, and it is
// proof rather than a guess: the pipeline reserves the run and its record in one
// step, everything it refuses before that returns an empty outcome, and every
// failure after it is reported on the run it failed. So a refusal carrying no run
// is one that claimed no work item, cut no worktree and invoked no agent, and the
// claim taken for it is a claim nothing was done on.
func refusedBeforeStarting(outcome Outcome, err error) bool {
	return err != nil && outcome.RunID == ""
}

// refusedBeforeAnythingRan reports the fresh run having been refused by the
// environment before any agent of it was invoked. Both halves are the condition
// and the run's own record carries both: the refusing site writes "nothing ran"
// because it is the only thing that can know, and the settle marks the round
// refused only once it has proved the round delivered nothing. A run refused
// after an agent had been asked something is settled like any other, and so is
// one whose round the settle could not classify — which is the direction this
// has to fail in, since a claim given back twice is one decision starting two
// runs.
func refusedBeforeAnythingRan(outcome Outcome) bool {
	return outcome.Environmental != nil && outcome.Environmental.NothingRan && outcome.Environmental.Refused
}

// withdrawEnvironmental gives back the claim taken for a fresh run the
// environment refused before any agent of it ran, and reports the run all the
// same. The run existed and its record says what stopped it; what did not
// happen is anything the claim bought, so the stoppage keeps its re-run and
// asking again once the machine is not what stops it carries out the same
// decision.
//
// A claim that could not be given back leaves the flag false, because the
// sentence it prints is an accounting claim: a stoppage told it kept its re-run
// when the record still holds the claim is exactly the disagreement the
// give-back exists to prevent. What stopped it is reported beside the run.
//
// A fresh run the provider's usage window stopped is given back the same way.
// An agent may have been asked something before the window closed, but nothing
// the run produced was judged, so what the claim bought is still nothing.
func (r Rerunner) withdrawEnvironmental(ctx context.Context, entry triage.Entry, outcome Outcome, result RerunResult) RerunResult {
	result.Outcome = outcome
	met := fmt.Sprintf("the fresh run of %s was refused by the environment before any agent of it ran", entry.WorkItemID)
	if stoppedByUsageWindow(outcome.Environmental) {
		met = fmt.Sprintf("the fresh run of %s was stopped by the provider's usage window before anything it produced was judged", entry.WorkItemID)
	}
	result.ClaimGivenBack = r.giveBack(ctx, entry, met, &result) == ""
	return result
}

// withdraw gives back the claim taken for a fresh run a full harness refused to
// reserve, and reports the same waiting state a carry-out that met capacity
// before the claim reports.
func (r Rerunner) withdraw(ctx context.Context, entry triage.Entry, capacity runstate.CapacityError, result RerunResult) RerunResult {
	result.Started = false
	result.CapacityFull = &capacity
	r.giveBack(ctx, entry, fmt.Sprintf(
		"the last free developer slot went to another run before this re-run of %s was reserved", entry.WorkItemID), &result)
	return result
}

// withdrawPaused gives back the claim taken for a fresh run that met a pause
// where it would have started, and reports the pause in place of the run it
// stopped from existing.
func (r Rerunner) withdrawPaused(ctx context.Context, entry triage.Entry, paused Outcome, result RerunResult) RerunResult {
	result.Started = false
	result.PausedBeforeStarting = &paused
	r.giveBack(ctx, entry, fmt.Sprintf(
		"the re-run of %s met %s where the fresh run would have started", entry.WorkItemID, pauseMet(paused)), &result)
	return result
}

// withdrawRefused gives back the claim taken for a fresh run the pipeline
// refused before it reserved anything, and reports the refusal with the
// accounting the asker needs: nothing ran and nothing was spent, so the same
// decision is carried out by asking again once the refusal no longer holds.
//
// The accounting goes into the error rather than beside it, because a refused
// carry-out is reported as its refusal — a result that says only "not started"
// is the one thing this path never returns.
func (r Rerunner) withdrawRefused(ctx context.Context, entry triage.Entry, refusal error, result RerunResult) (RerunResult, error) {
	result.Started = false
	problem := r.giveBack(ctx, entry, fmt.Sprintf(
		"the fresh run of %s was refused where it would have started", entry.WorkItemID), &result)
	if problem != "" {
		return result, fmt.Errorf("%w; %s", refusal, problem)
	}
	return result, fmt.Errorf("%w; nothing was reserved, so the claim taken for it was given back and the stoppage keeps its re-run — asking again once that no longer refuses carries out the same decision", refusal)
}

// giveBack returns the claim taken for a fresh run that provably never existed,
// and reports what stopped it where it could not. met names what the run met
// instead, so a claim that could not be given back reads as the thing that
// stopped it rather than as a record failure with no cause.
//
// That failure is said out loud rather than swallowed: the stoppage has then paid
// for a refusal that was meant to be free, and the decision it authorized needs a
// person to look at it, because nothing else here will notice.
func (r Rerunner) giveBack(ctx context.Context, entry triage.Entry, met string, result *RerunResult) string {
	err := r.Reruns.Withdraw(ctx, entry.Key)
	if err == nil {
		return ""
	}
	problem := fmt.Sprintf(
		"%s, and the claim taken for it could not be given back, so the stoppage has spent its re-run on a run that never started: %v",
		met, err)
	result.note(problem)
	return problem
}

// pauseMet names the pause a fresh run met, for a report that has to say what
// stopped it rather than only that something did. Each of them is lifted by a
// different person doing a different thing, so which one it was is the whole of
// what a reader can act on.
func pauseMet(outcome Outcome) string {
	switch {
	case outcome.PausedByOperator != nil:
		return "the operator's hold on all harness activity"
	case outcome.PausedByIntake != nil:
		return "the operator's hold on what the harness chooses"
	case outcome.PausedByDirective != nil:
		return "the unresolved directive " + outcome.PausedByDirective.ID
	case outcome.PausedByDependency != nil:
		return "unfinished work its item waits on: " + outcome.PausedByDependency.Summary()
	case outcome.PausedByTracker != nil:
		return "a tracker that would not answer: " + outcome.PausedByTracker.Summary()
	default:
		return "a pause"
	}
}

// settle records what became of the stopped run's artifacts and reports the
// disposition it recorded. They are kept until the fresh run integrates, which
// is the moment what the stopped run holds stops being what somebody might
// cherry-pick from; then they are retired explicitly, and what could not be
// retired is kept with the reason, because an artifact nobody records is an
// orphan nobody discovers.
func (r Rerunner) settle(ctx context.Context, entry triage.Entry, prior runstate.State, outcome Outcome, result *RerunResult) runstate.PreservedArtifacts {
	preserved := preservedOf(prior, readmodel.LookFor(ctx, r.Remains, prior))
	// The fresh run having integrated is what retires everything the stopped run
	// left: what it kept in this repository, and the pull request it published.
	// So the vehicle the work landed by is read from the same outcome that
	// decides there is anything to retire at all.
	by := supersessionOfOutcome(outcome)
	// A publication this re-run has just handed back is read here from the prior
	// record, which predates the mark, so it is asked for in its own words.
	if by.Landed() && (preserved.Disposition == runstate.PreservedKept || retirablePublication(prior) || prior.PublicationUnasked()) {
		var problem string
		preserved, problem = r.retire(ctx, prior, by, preserved, result)
		result.note(problem)
	}
	settled, err := r.Reruns.Settle(ctx, entry.Key, outcome.RunID, preserved)
	if err != nil {
		result.note(fmt.Sprintf(
			"the re-run of %s ran, and what became of the branch and worktree run %s preserved could not be recorded: %v",
			entry.WorkItemID, entry.RunID, err))
		return preserved
	}
	return settled.Preserved
}

// note adds one record this action could not update. They accumulate rather than
// replacing each other: the stopped run's record and the re-run's own are two
// different readers' accounts of the same artifacts, and a caller told about one
// failure would go looking in the wrong place for the other.
func (result *RerunResult) note(problem string) {
	switch {
	case problem == "":
	case result.RecordProblem == "":
		result.RecordProblem = problem
	default:
		result.RecordProblem += "; " + problem
	}
}

// clearCarryOutFinding takes back the finding a refused carry-out left about one
// stoppage, now that its decision is being carried out, and reports what it could
// not do. A finding standing over a decision that has been acted on is the worst
// kind: it reads exactly like the condition the finding exists to report. The
// write is made under a context detached from the action's own, for the reason
// every record written as a run starts is: a shutdown that lands here cancels the
// very context the action ran under.
func clearCarryOutFinding(ctx context.Context, decisions RerunDecisions, workItemID, runID string, at time.Time) string {
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if _, err := decisions.ClearCarryOut(write, workItemID, runID, at); err != nil {
		return fmt.Sprintf(
			"the decision is being carried out and the finding a previous attempt left on %s's triage record could not be cleared, so the docket still says this decision is not happening: %v",
			workItemID, err)
	}
	return ""
}

// retire removes what the stopped run left behind, now that the fresh run has
// integrated the work: the worktree and branch it kept in this repository, and
// the pull request it published that nothing will ever merge. It reports the
// disposition of the local artifacts beside any record it could not update.
// Nothing here fails the re-run: the work landed, and an artifact that has to be
// looked at by hand is a fact to record rather than a reason to report a
// successful run as a failure.
//
// It is done under the stopped run's own lease, taken and released here. That is
// what makes the removals and the record of them one act: the state the flags
// are written onto is the state read under the lease, so a sweep settling the
// same run beside this cannot lose any of it.
func (r Rerunner) retire(ctx context.Context, prior runstate.State, by Supersession, preserved runstate.PreservedArtifacts, result *RerunResult) (runstate.PreservedArtifacts, string) {
	// Nothing is wired to remove anything, and this is reachable for a run that
	// preserved nothing locally and still holds an open publication, so what is
	// reported has to name what actually survived rather than only the local
	// artifacts. A publication left open here is said out loud for the reason one
	// left open below is: the convergence sweep will close it, and until it does
	// the forge shows work as pending that is not.
	if r.Preserved == nil {
		if preserved.Disposition == runstate.PreservedKept {
			preserved.Problem = "nothing is wired to retire what the stopped run preserved, so it is still there"
		}
		r.notePublicationLeftOpen(prior, by, "nothing is wired to delete the branch it published", result)
		return preserved, ""
	}
	stopped, lease, err := r.Runs.AdoptRun(ctx, prior.RunID)
	if err != nil {
		// Somebody else owns the stopped run, or its record could not be read.
		// Either way nothing is removed: an artifact retired without its record
		// being writable is exactly the stale record this took the lease to avoid.
		if preserved.Disposition == runstate.PreservedKept {
			preserved.Problem = fmt.Sprintf("what run %s preserved was left where it is, because its record could not be taken to write the removal onto: %v", prior.RunID, err)
		}
		r.notePublicationLeftOpen(prior, by, fmt.Sprintf("its record could not be taken to write the closure onto: %v", err), result)
		return preserved, ""
	}
	defer lease.Release()

	// The publication goes first, and independently of what is on disk: a pull
	// request nothing will ever merge is left open by the same omission whether
	// or not the branch beneath it turns out to be removable.
	retiredPublication := r.retirePublication(ctx, &stopped, by, result)
	// Nothing local survives to retire, so the record of the publication is the
	// whole of what this had to write.
	if preserved.Disposition != runstate.PreservedKept {
		return preserved, r.recordRemoval(stopped, by.RunID, gitworktree.Retirement{}, retiredPublication)
	}

	retirement, retireErr := r.Preserved.RetirePreserved(ctx, worktreeOf(stopped), stopped.TargetBranch)
	// What was removed is written onto the stopped run before anything else is
	// decided, because that record is what every other reader in the harness asks
	// whether these artifacts still exist.
	recorded := r.recordRemoval(stopped, by.RunID, retirement, retiredPublication)
	if retireErr == nil && retirement.Retired() {
		retired := r.now()
		preserved.Disposition = runstate.PreservedRetired
		preserved.RetiredAt = &retired
		return preserved, recorded
	}
	// Something survived, so the record still says kept — and it names only what
	// actually survived. A retirement that took one of the two and then failed is
	// the case that makes this matter: a reader sent after an artifact that is not
	// there is exactly what these fields exist to prevent.
	if retirement.Worktree.Removed {
		preserved.WorktreePath = ""
	}
	if retirement.Branch.Removed {
		preserved.Branch = ""
	}
	if retireErr != nil {
		preserved.Problem = fmt.Sprintf("retiring what run %s preserved failed: %v", prior.RunID, retireErr)
		return preserved, recorded
	}
	preserved.Problem = retirement.Kept()
	return preserved, recorded
}

// retirePublication closes the pull request the stopped run left open and
// deletes the branch it published, and reports whether the run's record now has
// to say so. What it did goes on the result rather than into the preserved
// artifacts, because this is the one artifact that is not in this repository: a
// reader acting on it goes to the forge.
//
// It mutates the adopted state rather than saving it, so the close and the note
// of it reach disk in the same write as the local removals beside them.
func (r Rerunner) retirePublication(ctx context.Context, stopped *runstate.State, by Supersession, result *RerunResult) bool {
	if !retirablePublication(*stopped) {
		return false
	}
	if r.Publications == nil {
		r.notePublicationLeftOpen(*stopped, by, "nothing is wired to close it", result)
		return false
	}
	retirement := retirePublication(ctx, r.Publications, r.Preserved, *stopped, by)
	result.Publication = &retirement
	if retirement.Failure != "" {
		result.note(retirement.Failure)
		return false
	}
	published := *stopped.PullRequest
	published.Superseded = by.Vehicle()
	published.State = "CLOSED"
	stopped.PullRequest = &published
	return true
}

// notePublicationLeftOpen says that the stopped run's pull request is still
// open and why, for each of the three ways retiring it can be skipped before
// anything is attempted. It is one sentence in one place because the three are
// the same fact to whoever reads the result: the forge is still showing this
// run's work as pending, and the convergence sweep is what will fix it.
//
// A run that published nothing, or whose publication is already retired, says
// nothing at all — there is no request to leave open.
func (r Rerunner) notePublicationLeftOpen(stopped runstate.State, by Supersession, because string, result *RerunResult) {
	if !retirablePublication(stopped) {
		return
	}
	result.note(fmt.Sprintf(
		"pull request %d, which run %s left open and run %s superseded, is still open because %s; it stays open until a convergence sweep closes it",
		stopped.PullRequest.Number, stopped.RunID, by.RunID, because))
}

// recordRemoval marks the stopped run's own record with what this retirement
// removed, and reports what stopped it where it could not.
//
// It is the record the rest of the harness reads: `yoyo status`, a docket entry
// built later, and reconciliation all take these flags as the answer to whether
// the branch, the worktree and the publication are still there. A removal that is
// not written back leaves every one of them sending somebody after an artifact
// that is gone — or, for the publication, asking the forge about a request that
// is already closed on every later sweep.
//
// A retirement that removed nothing writes nothing: this is called on every
// retirement, including the ones that kept everything.
func (r Rerunner) recordRemoval(stopped runstate.State, freshRunID string, retirement gitworktree.Retirement, publicationRetired bool) string {
	worktreeGone := retirement.Worktree.Removed && !stopped.WorktreeRemoved
	branchGone := retirement.Branch.Removed && !stopped.BranchRemoved
	if !worktreeGone && !branchGone && !publicationRetired {
		return ""
	}
	stopped.WorktreeRemoved = stopped.WorktreeRemoved || worktreeGone
	stopped.BranchRemoved = stopped.BranchRemoved || branchGone
	// A stopped run promoted nothing, so what earns the removal is the run that
	// superseded it. Naming it is what keeps the record evidence rather than an
	// assertion, and it is what the run state requires of a removal with no
	// integration behind it. A retired publication is deliberately not one of
	// those removals: nothing in this repository was removed for it, and the
	// vehicle that earned it is recorded on the publication itself.
	if worktreeGone || branchGone {
		stopped.ArtifactsRetiredBy = freshRunID
	}
	// When the run ended is what dates it; this dates the last thing the harness
	// did to what it left behind, which is what UpdatedAt has always meant.
	stopped.UpdatedAt = r.now()
	if err := r.Runs.Save(stopped); err != nil {
		return fmt.Sprintf(
			"what run %s left behind was retired, and its own record still says otherwise, so anything reading that run will name artifacts that are gone: %v",
			stopped.RunID, err)
	}
	return ""
}

// preservedOf is what the repository holds of the stopped run. A
// run whose artifacts the harness already removed has nothing to keep or retire,
// and says so rather than describing what is gone as kept.
func preservedOf(prior runstate.State, found triage.Found) runstate.PreservedArtifacts {
	preserved := runstate.PreservedArtifacts{Disposition: runstate.PreservedKept}
	if found.BranchThere || found.Unknown {
		preserved.Branch = prior.Branch
	}
	if found.WorktreeThere || found.Unknown {
		preserved.WorktreePath = prior.WorktreePath
	}
	if preserved.Branch == "" && preserved.WorktreePath == "" {
		preserved.Disposition = runstate.PreservedGone
	}
	return preserved
}

// rerunReason is what the fresh run records as why it exists: the decision the
// harness read, where that decision is recorded, the stoppage it settles, and
// the reasoning the decision was recorded with.
//
// All of it is read from the durable record, which is what makes the whole
// sentence evidence rather than a claim. It cites the record it came from —
// whose decision, which conversation, which turn — so a reader who doubts the
// attribution can go and find the turn it was written on. Nothing here can be
// supplied by whoever asked for the carry-out, which is the point: the sentence
// names the development manager, and until the record existed the words after
// that name were whatever a command line carried.
func rerunReason(entry triage.Entry, decided runstate.TriageCounters, taken int, decision runstate.TriageDecision) string {
	reason := fmt.Sprintf(
		"the development manager's triage decided a re-run of %s, %s — %d recorded against the item's durable triage budget, %d of them already carried out — and the harness started this run to carry out the one left, on the stopped work of run %s.%s The reasoning that decision was recorded with: ",
		entry.WorkItemID, decision.Cite(), decided.Reruns, taken, entry.RunID, crossedCaps(decided))
	reasoning := strings.TrimSpace(decision.Reason)
	// The reasoning is folded to what the run's recorded selection will hold,
	// rather than refused: losing the end of a long argument is better than
	// refusing to carry out a decision because of its length. The room is never
	// negative, so a prefix that filled the record on its own truncates the
	// argument to nothing rather than indexing past the end of it.
	room := runstate.MaxSelectionReasonBytes - len(reason)
	if room < 0 {
		room = 0
	}
	// The assembled reason is folded to the same bound, and that is the guarantee
	// rather than the arithmetic above. The prefix is no longer a fixed sentence:
	// it carries the citation of the decision it was read from, and an attribution
	// clause per crossed cap built from a name an operator chose, so measuring
	// room and then not enforcing the bound on the result is how an over-length
	// reason reaches the record. It is folded here
	// anyway rather than left to the record's own fold, because cutting the
	// reasoning and keeping the whole of the verified decision is a better cut
	// than the record can make from the assembled sentence alone.
	return singleLine(reason+singleLine(reasoning, room), runstate.MaxSelectionReasonBytes)
}

// crossedCaps names the operator overrides this decision stands on, and says
// nothing on the ordinary item.
//
// A re-run recorded past a cap exists because a person crossed that cap, and the
// run's selection reason is where that has to survive: the record it was read
// from is a live one that the next override rewrites, and the conversation it was
// argued in is gone. It names the two budgets a re-run is refused against and
// only those, because the reason is a sentence somebody reads rather than a dump
// of the item's record — and it names who and when rather than why, because the
// argument for crossing the cap is on the override itself, at whatever length the
// operator wrote it.
func crossedCaps(counters runstate.TriageCounters) string {
	var crossed []string
	for _, budget := range []string{runstate.TriageRerunBudget, runstate.TriageReviewRoundBudget} {
		override, found := counters.OverrideOf(budget)
		if !found {
			continue
		}
		crossed = append(crossed, fmt.Sprintf("the %s cap, by %s at %s",
			budget, singleLine(override.DecidedBy, maxCrossedCapAttributionBytes), override.DecidedAt.UTC().Format(time.RFC3339)))
	}
	if len(crossed) == 0 {
		return ""
	}
	return " It stands on a recorded operator override of " + strings.Join(crossed, ", and of ") + "."
}

// maxCrossedCapAttributionBytes bounds how much of an operator's name the run's
// reason carries. The record keeps whatever they gave; this is a clause inside a
// sentence, and a bounded one is what keeps the argument the reason exists for
// from being crowded out by the attribution.
const maxCrossedCapAttributionBytes = 64

func (r Rerunner) validate() error {
	var problems []error
	if r.Docket == nil {
		problems = append(problems, errors.New("a re-run requires the triage docket the decision was made against"))
	}
	if r.Runs == nil {
		problems = append(problems, errors.New("a re-run requires the durable run state"))
	}
	if r.Intake == nil {
		problems = append(problems, errors.New("a re-run requires the intake hold, because a re-run is the harness choosing work"))
	}
	if r.Reruns == nil {
		problems = append(problems, errors.New("a re-run requires the record that bounds it to one per docketed stoppage"))
	}
	if r.Decisions == nil {
		problems = append(problems, errors.New("a re-run requires the item's triage record, which is what says the development manager decided one and what the reasoning it records is read from"))
	}
	if r.Items == nil {
		problems = append(problems, errors.New("a re-run requires the work item, because a fresh run starts on it and a re-run that cannot read it would spend the stoppage's claim to find that out"))
	}
	if r.Gates == nil {
		problems = append(problems, errors.New("a re-run requires the human gates a person has passed, because a re-run is the harness choosing work and a gated item is not work it may choose"))
	}
	if r.Start == nil {
		problems = append(problems, errors.New("a re-run requires a way to start a run"))
	}
	if r.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which starts nothing; a re-run reads the same limit the reservation enforces, so that a full harness costs the stoppage nothing", r.Capacity))
	}
	return errors.Join(problems...)
}

func (r Rerunner) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result RerunResult) Render() string {
	var rendered strings.Builder
	if result.IntakeHeld != nil {
		// Who is holding it comes off the record rather than out of this sentence:
		// the same switch is placed by the operator and by the harness's own
		// failure-storm brake, and they are different things to do something about.
		fmt.Fprintf(&rendered, "INTAKE HELD since %s: %s\n",
			result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339), result.IntakeHeld.Says())
		fmt.Fprintf(&rendered, "nothing was started for %s, and the stoppage of run %s keeps its one re-run; `yoyo release` lifts the hold, and asking again carries out the same decision\n",
			result.WorkItemID, result.PriorRunID)
		return rendered.String()
	}
	if result.CapacityFull != nil {
		fmt.Fprintf(&rendered, "WAITING FOR A DEVELOPER: nothing was started for %s, %d active run(s), limit %d\n",
			result.WorkItemID, result.CapacityFull.Active, result.CapacityFull.Limit)
		fmt.Fprintf(&rendered, "the stoppage of run %s keeps its one re-run and the decision still stands, so asking again once a slot frees carries out the same one\n", result.PriorRunID)
		fmt.Fprintf(&rendered, "%s is meanwhile open work the scheduler pulls from, so it can reach a developer without this being asked again\n", result.WorkItemID)
		if result.RecordProblem != "" {
			fmt.Fprintln(&rendered, result.RecordProblem)
		}
		return rendered.String()
	}
	if result.PausedBeforeStarting != nil {
		// What lifts the pause is said by the pause's own report rather than here,
		// because it is the same thing that lifts it for any other work. What this
		// owes a reader is the accounting: nothing ran, and nothing was spent.
		fmt.Fprintf(&rendered, "NOT STARTED: the fresh run of %s met %s\n",
			result.WorkItemID, pauseMet(*result.PausedBeforeStarting))
		fmt.Fprintf(&rendered, "the stoppage of run %s keeps its one re-run and the decision still stands, so asking again once the pause lifts carries out the same one\n", result.PriorRunID)
		if result.RecordProblem != "" {
			fmt.Fprintln(&rendered, result.RecordProblem)
		}
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "re-ran %s on the stopped work of run %s\n", result.WorkItemID, result.PriorRunID)
	fmt.Fprintf(&rendered, "chosen because %s\n", result.Reason)
	if result.SupersededClaim != "" {
		fmt.Fprintf(&rendered, "the claim run %s left on %s was released first; the item's notes say why\n", result.PriorRunID, result.WorkItemID)
	}
	if result.Outcome.RunID != "" {
		fmt.Fprintf(&rendered, "fresh run: %s\n", result.Outcome.RunID)
	}
	if result.ClaimGivenBack {
		fmt.Fprintf(&rendered, "the environment refused that run before any agent of it ran, so the claim was given back and the stoppage of run %s keeps its one re-run; asking again once the machine is not what stops it carries out the same decision\n",
			result.PriorRunID)
	}
	rendered.WriteString(result.Preserved.Render())
	// A publication is reported only where there was one to retire, and only
	// what became of it: a line on every re-run of a project that does not
	// publish would say nothing.
	if result.Publication != nil && result.Publication.Closed {
		fmt.Fprintf(&rendered, "closed pull request #%d as superseded, and deleted the branch it published\n", result.Publication.Number)
	}
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
