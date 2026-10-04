package orchestrator

// Repeating a merge request the forge dropped, on the development manager's
// decision.
//
// A queued merge the forge gives up on leaves an integrated change published and
// unmerged. Most of those are somebody's work — a conflict with the base branch,
// a protection rule the request does not satisfy — and the harness never merges
// past one, not with administrator privileges and not by asking again. Some are
// not: a required check that never finished, an auto-merge race lost to another
// request that landed first. Nothing about the change is wrong in those, and what
// the publication needs is the same merge request made again.
//
// So this repeats it, and repeats exactly it. The pull request is the one the
// reviewer's verdict authorized, the method is the one that verdict's own merge
// was made by, read off the run's record rather than chosen here, and the head
// commit is pinned to the commit that was integrated. Nothing is overridden to
// get there: the request goes back through the forge's whole requirement
// machinery, which is why repeating it is not merging past a requirement.
//
// # What refuses, and why each is asked before anything is spent
//
// The order is the order the guarantees need, and it is the re-run's order for
// the re-run's reason: the publication gets one re-arm, spent by recording it, so
// a condition asked after the record spends the very budget it refuses.
//
//   - The run that made the publication has to be terminally recorded. That is
//     the same precondition the re-run's carry-out asks, and it is here because of
//     what happened without it: an agent re-armed PR #92 while the run that owned
//     it was still alive, the forge merged an earlier approved promotion into the
//     branch mid-run, the run's own republish then failed against a request it
//     could no longer publish into, and a 117-line amendment was stranded on the
//     preserved branch for somebody to recover by hand. A re-arm against a live
//     run's publication is indistinguishable from that mistake.
//   - The forge's own merge state has to name nothing only a person can supply.
//     publish.AwaitsOnlyAPerson draws that line, and a state that could not be
//     read refuses: this is a gate, and the safe answer for a gate is no.
//   - The request has to be unchanged. Its head is still the commit the run
//     integrated, and the remote target still passes the same pre-merge content
//     check the original gate ran — the identical check publishIntegration makes,
//     rather than a second rendering of it.
//   - The publication has to have a re-arm decision of the development manager's
//     that nothing has carried out. The decision is recorded on the work item and
//     keyed to this publication; what says it has been acted on is the count on
//     the publication's own record.
//
// # Why the promotion lease
//
// A re-arm is an integration retry against the target branch, and
// `one-promotion-per-target-branch` binds it exactly as it binds the promotion
// the run made: this asks a forge to move the remote target, so it queues behind
// whatever is promoting into that branch now. The lease is the harness's own and
// this is the harness's own process — no agent acquires it and no agent performs
// the merge, which is the same boundary that keeps the roles that authorize a
// promotion from being able to make one. It is taken after the run's own lease,
// which is the order everything else that holds both takes them in; repeat says
// why that order and not the other.
//
// It is held across the forge reads and the pre-merge check as well as the merge,
// rather than around the merge alone. The check on the remote target is the
// evidence that authorizes the repeated request, and a promotion admitted between
// that check and the merge invalidates it — which is the whole of what the lease
// is for.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// RearmRuns is the durable run state this action reads, writes, and serializes
// against. The publication it repeats lives on the run's own record, so the
// count of what has been repeated is written there too, under that run's lease
// and under the target branch's promotion lease.
type RearmRuns interface {
	Load(runID string) (runstate.State, error)
	Incomplete() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
	LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error)
}

// RearmForge is the forge access a re-arm needs: what the request looks like
// now, what the forge says is unmet on it, and the merge request itself. It is
// satisfied by publish.GitHub.
type RearmForge interface {
	State(ctx context.Context, head string) (publish.PullRequest, error)
	MergeState(ctx context.Context, number int) (string, error)
	Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error)
}

// RearmChecks is the forge's reading of a request's checks: which failed, and
// how far the target has moved on without the head. It is what gates arming a
// request nothing ever asked the forge to merge, and it is satisfied by
// publish.GitHub.
type RearmChecks interface {
	Checks(ctx context.Context, number int, base string) (publish.CheckReading, error)
}

// RearmWorktrees is the repository access a re-arm needs, and it is one read:
// the pre-merge check on the remote target that the original gate ran. Nothing
// here moves a ref.
type RearmWorktrees interface {
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
}

// RearmDecisions is the harness's own durable record of what triage has decided
// about one work item, which is what proves a re-arm was decided at all: the
// development manager's decision spends the publication's re-arm budget as it is
// recorded, so a publication carrying none is a publication nobody decided this
// about.
//
// It is satisfied by runstate.TriageStore.
type RearmDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// Rearmer repeats one merge request the forge dropped. It decides nothing about
// the work: what it does is check that a decision somebody else made may be
// carried out, and then make the identical request the reviewer's verdict
// already authorized.
type Rearmer struct {
	Docket    RerunDocket
	Runs      RearmRuns
	Forge     RearmForge
	Worktrees RearmWorktrees
	// Decisions is the item's durable triage record, which is what says the
	// development manager decided this at all. Required: a re-arm made on nobody's
	// decision would ask a forge for a merge that no recorded decision stands
	// behind.
	//
	// No cap is wired beside it, and none belongs here. The publication's re-arm
	// ceiling is spent where the decision is recorded, which is the direction every
	// triage budget is written in; what this action asks of the record is the
	// narrower question of whether a decision remains uncarried. A cap read again
	// here would be a second guard over a budget already spent, and the two could
	// disagree.
	Decisions RearmDecisions
	// Checks is the reading that gates arming a request nothing ever asked the
	// forge to merge: a head behind its target, or a failing check, refuses the
	// arming and names which. A re-arm of a dropped merge does not read it — the
	// forge's merge state is its gate, as it always was. Optional, and a Rearmer
	// wired without it refuses every first arming rather than arming unchecked.
	Checks RearmChecks
	// Items reads whether the items a publication waiting on its target's red
	// check waits on are closed, which is what authorizes the harness to arm it
	// with nobody deciding anything. Optional: a Rearmer wired without it
	// refuses that arming, and a decision of the development manager's is
	// carried out as ever.
	Items RearmItems
	Clock execution.Clock
}

// RearmItems reads a work item. It is satisfied by the tracker client.
type RearmItems interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
}

// RearmRequest is one decision to carry out: the run whose publication the
// docket entry names, and the reasoning the development manager recorded for
// deciding a re-arm of it.
type RearmRequest struct {
	Run    string
	Reason string
}

// RearmResult is what the action did. It reports the request it repeated and
// what the forge answered, and it says which of the two answers it was: a merge
// the forge queued leaves the run outstanding for reconciliation to settle, and
// one it performed on the spot is a publication reconciliation finishes on its
// next sweep.
type RearmResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Number     int    `json:"number"`
	URL        string `json:"url,omitempty"`
	// Method is what the repeated request was made by, which is the method the
	// run's own merge recorded rather than anything chosen here.
	Method string `json:"method,omitempty"`
	// HeadCommit is the commit the repeated request was pinned to, which is the
	// commit the run integrated.
	HeadCommit string `json:"head_commit,omitempty"`
	// Reason is the harness's account of why the request was repeated: the
	// decision it verified, and the reasoning it was given. It is reported to
	// whoever asked for the re-arm rather than written to durable state, and
	// nothing here is the only copy of it — the development manager's decision and
	// its reasoning are on the work item, put there by the conversation that
	// recorded them, which is where every triage decision's account of itself
	// lives. What this run's own records keep is the count.
	Reason string `json:"reason,omitempty"`
	// FirstArm reports a publication nothing had ever asked the forge to merge:
	// what this made is the merge request the run's own merge would have made,
	// rather than a repeat of one the forge dropped.
	FirstArm bool `json:"first_arm,omitempty"`
	// TargetRed reports a publication whose merge the harness withdrew for its
	// target's red check, armed again on the harness's own authority once every
	// item it waited on closed: nobody decided it, and it spends no re-arm.
	TargetRed bool `json:"target_red,omitempty"`
	// Rearmed reports the request having actually been repeated, and Queued the
	// forge having accepted it to perform later rather than performing it now.
	Rearmed bool `json:"rearmed"`
	Queued  bool `json:"queued"`
	// Rearms is what this publication's durable counter stands at afterwards.
	Rearms int `json:"rearms,omitempty"`
	// RecordProblem names a durable record this action could not update after the
	// request was made. The request happened either way, so it is reported beside
	// the result rather than in place of it.
	RecordProblem string `json:"record_problem,omitempty"`
}

// Rearm carries out one re-arm decision.
//
// Everything that can refuse is asked before the counter is spent, so a refused
// re-arm costs the publication nothing; the counter is written before the
// request is made, so a process that dies between the two has recorded a re-arm
// it did not make rather than made one it did not record; and what the forge
// answered is recorded after it answers.
func (r Rearmer) Rearm(ctx context.Context, request RearmRequest) (RearmResult, error) {
	if err := r.validate(); err != nil {
		return RearmResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	reasoning := strings.TrimSpace(request.Reason)
	if !runstate.ValidRunID(runID) {
		return RearmResult{}, fmt.Errorf("re-arm %q is not a run identifier; a triage decision names the run the docket entry is about", request.Run)
	}
	if reasoning == "" {
		return RearmResult{}, errors.New("a re-arm reports the development manager's reasoning as why the request was repeated, and none was given")
	}

	prior, err := r.Runs.Load(runID)
	if err != nil {
		return RearmResult{}, fmt.Errorf("read the run the docket entry is about: %w", err)
	}
	published, integration, err := rearmablePublication(prior)
	if err != nil {
		return RearmResult{}, err
	}
	result := RearmResult{
		WorkItemID: prior.WorkItemID,
		RunID:      prior.RunID,
		DocketKey:  triage.PublicationKey(prior.RunID, published.Number),
		Number:     published.Number,
		URL:        published.URL,
		Method:     published.MergeMethod,
		HeadCommit: published.HeadCommit,
		FirstArm:   prior.PublicationUnarmed(),
		TargetRed:  prior.WaitingOnRedTarget(),
	}
	// The publication has to be on the docket, for the reason a stoppage does: the
	// entry is what the development manager decided against, and a re-arm of
	// something nothing docketed is a re-arm nothing bounds.
	if err := r.docketed(result.DocketKey, prior.RunID); err != nil {
		return result, err
	}
	// The run's own record, not the entry that describes it, is what says the
	// publication is free to be acted on. Both halves are the same condition
	// stated at the two scales a mistake has actually happened at.
	if err := publicationIsSettled(prior, published); err != nil {
		return result, err
	}
	if err := noRearmRunInFlight(r.Runs, prior.WorkItemID); err != nil {
		return result, err
	}
	// A publication waiting on its target's red check is the harness's to arm, and
	// what authorizes it is every item it waits on having closed rather than a
	// decision; it is read before anything is asked of the forge.
	if result.TargetRed {
		if err := r.targetRedAnswered(ctx, *published.TargetRed); err != nil {
			return result, err
		}
		result.Reason = targetRedRearmReason(prior, published, reasoning)
		return r.repeat(ctx, prior.RunID, integration.TargetBranch, published.MergeRearms, result)
	}
	// That the development manager decided this, and that nothing has carried the
	// decision out, are read before anything is asked of the forge.
	decided, decision, err := r.decided(prior, published, result.DocketKey)
	if err != nil {
		return result, err
	}
	result.Reason = rearmReason(prior, published, decided, decision, result.DocketKey, reasoning, result.FirstArm)
	// Everything above is read from the harness's own records and refuses without
	// taking a lease, so the ordinary refusal holds up no promotion. What is left —
	// what the forge says, whether the remote target still passes, and the request
	// itself — is asked under the leases, because those are the questions a
	// concurrent promotion can invalidate between the asking and the merge.
	return r.repeat(ctx, prior.RunID, integration.TargetBranch, published.MergeRearms, result)
}

// repeat asks the forge what it says about the request, checks the remote target,
// spends the publication's re-arm, and makes the request — all under the run's own
// lease so that the counter and the answer are written onto the record they were
// read from, and under the target branch's promotion lease so that the merge and
// the evidence authorizing it are serialized against every promotion into that
// branch.
//
// The two are taken in that order, which is the order every other holder of both
// takes them: the pipeline holds its run's lease across the promotion, and
// reconciliation adopts a run before it catches its target branch up. Taking the
// promotion lease first would have this waiting on a branch while a sweep waits
// on the run this holds. Only the second wait blocks — a run somebody else holds
// is refused here rather than queued for.
//
// # Why the checks are inside the lease rather than in front of it
//
// The pre-merge check on the remote target is the evidence that authorizes the
// repeated merge, and it is worth exactly as long as the branch stands still. A
// check made before the lease is a check a promotion admitted in the meantime has
// invalidated: the target moves, and the forge is then asked to merge into a
// branch nobody in this decision saw. That is the same reasoning publishIntegration
// makes the identical check inside the merge's own retry for, rather than in front
// of it, and this is the identical check.
//
// Everything cheaper than that refuses before either lease is taken, so an
// ordinary refusal — nothing docketed, a live run, no decision to carry out —
// holds up no promotion at all.
//
// The record is re-read under the lease rather than reused, for the reason every
// other adoption here re-reads: what is rewritten has to be what is on disk now,
// and a run something settled in the meantime has had its publication written by
// whatever settled it.
func (r Rearmer) repeat(ctx context.Context, runID, targetBranch string, checked int, result RearmResult) (RearmResult, error) {
	state, lease, err := r.Runs.AdoptRun(ctx, runID)
	if err != nil {
		return result, fmt.Errorf("take run %s to record the re-arm of its publication: %w; nothing was spent, so the publication keeps its re-arm", runID, err)
	}
	defer func() { _ = lease.Release() }()

	promotion, err := r.Runs.LeasePromotion(ctx, targetBranch)
	if err != nil {
		return result, fmt.Errorf("wait for a turn to move %s: %w; nothing was spent, so the publication keeps its re-arm", targetBranch, err)
	}
	defer func() { _ = promotion.Release() }()

	published, integration, err := rearmablePublication(state)
	if err != nil {
		return result, fmt.Errorf("run %s was settled while its publication was being checked: %w", runID, err)
	}
	if published.Number != result.Number || published.MergeRearms != checked || state.PublicationUnarmed() != result.FirstArm || state.WaitingOnRedTarget() != result.TargetRed {
		return result, fmt.Errorf("run %s was settled while its publication was being checked, so what would be repeated is no longer what was checked", runID)
	}
	// What the forge says decides whether this is a drop worth repeating at all,
	// and it is asked before the local check because it is the cheaper of the two
	// and the one that refuses most re-arms.
	if err := r.forgeWouldTakeItBack(ctx, state, published, integration); err != nil {
		return result, err
	}
	// A merge withdrawn for its target's red check is armed only on a head level
	// with its target whose checks pass now, which is the gate a first arming
	// passes: what it arms is a merge whose checks last failed.
	if result.FirstArm || result.TargetRed {
		if err := r.landingChecksPass(ctx, published, integration); err != nil {
			if result.TargetRed {
				return result, fmt.Errorf("%w; this merge was withdrawn for its target's red check, and the reconcile sweep brings a head the fix left behind up to date from its kept branch rather than arming it", err)
			}
			return result, err
		}
	}
	if err := r.Worktrees.VerifyRemoteTarget(ctx, gitworktree.Integration{
		Branch:               state.Branch,
		TargetBranch:         integration.TargetBranch,
		SourceCommit:         integration.SourceCommit,
		TargetCommit:         integration.TargetCommit,
		PreviousTargetCommit: integration.PreviousTargetCommit,
	}); err != nil {
		return result, fmt.Errorf("check the remote target branch before repeating the merge request: %w; nothing was spent, so the publication keeps its re-arm", err)
	}
	// The counter is written before the request is made, which is the direction
	// every triage counter fails in. A process that dies here has recorded a
	// re-arm it did not make, and the publication has spent the one it had. An
	// arming on the harness's own authority, after the target's red check was
	// answered, spends nothing: nobody decided it, and the publication keeps the
	// re-arm triage may decide if the forge drops it later.
	if !result.TargetRed {
		published.MergeRearms++
		state.PullRequest = &published
		state.UpdatedAt = r.now()
		if err := r.Runs.Save(state); err != nil {
			return result, fmt.Errorf("record the re-arm of pull request %d before repeating it: %w; nothing was asked of the forge", published.Number, err)
		}
	}
	result.Rearms = published.MergeRearms

	merge, mergeErr := r.Forge.Merge(ctx, publish.MergeRequest{
		Number:     published.Number,
		HeadCommit: published.HeadCommit,
		Method:     publish.MergeMethod(published.MergeMethod),
	})
	if mergeErr != nil {
		return result, fmt.Errorf("repeat the merge request for pull request %d: %w; the re-arm is spent and recorded, so a further drop is an escalation rather than another re-arm",
			published.Number, mergeErr)
	}
	result.Rearmed = true
	result.Queued = merge.Queued
	// The run goes back where reconciliation settles a merge, which is exactly
	// where its original merge left it, and it goes there on either answer. A
	// queued merge is the state's own case. A merge the forge performed on the
	// spot is the same thing one step further on: this action has no worktree to
	// finish a publication with — confirming the remote target, recording the
	// merge commit, deleting the consumed branch, catching the local target up —
	// and reconciliation does all four for a merge it finds landed. Recording it
	// as settled here would leave a publication half-finished that nothing would
	// come back to.
	//
	// The publication's earlier failure is cleared with it, because what it said
	// was that the forge had dropped the merge and the forge has taken one again.
	// So is the run's blocker, which said the same thing: left standing, `yoyo
	// status` and a fresh docket would describe a publication that needs a person
	// while the forge is holding the merge again. The work item's own blocker is
	// reconciliation's to settle, on what the forge does next — a second drop
	// writes a fresh one there, and a merge closes the item.
	published.MergeQueued = true
	published.TargetRed = nil
	state.PullRequest = &published
	state.PublishFailure = ""
	if state.StopClass == runstate.StopPublish {
		state.StopClass = ""
	}
	state.Blocker = ""
	state.UpdatedAt = r.now()
	if err := r.Runs.Save(state); err != nil {
		result.RecordProblem = fmt.Sprintf(
			"the merge request for pull request %d was repeated and the run's record still says the forge dropped it, so reconciliation will not settle what the forge does next: %v",
			published.Number, err)
	}
	return result, nil
}

// targetRedAnswered refuses the harness's own arming of a merge withdrawn for its
// target's red check while any item it waits on is unfinished, or while that
// cannot be read: the items closing is the whole of its authority.
func (r Rearmer) targetRedAnswered(ctx context.Context, waiting runstate.TargetRed) error {
	if r.Items == nil {
		return errors.New("nothing is wired to this harness to read whether the items this merge waits on are closed, and the harness arms a merge withdrawn for its target's red check only once they are; nothing was spent")
	}
	for _, id := range waiting.WaitingOn() {
		item, err := r.Items.Show(ctx, id)
		if err != nil {
			return fmt.Errorf("read whether %s, filed for a red check on %s, is closed: %w; nothing was spent", id, waiting.TargetBranch, err)
		}
		if !strings.EqualFold(item.Status, "closed") {
			return fmt.Errorf("refused at the red-target gate: this merge waits on %s, filed for a red check on %s, which is %s; the harness arms it once that closes, and nothing was spent",
				id, waiting.TargetBranch, nonEmpty(item.Status, "not closed"))
		}
	}
	return nil
}

// targetRedRearmReason is the harness's account of arming a merge it withdrew
// for its target's red check: nobody decided it, so what it cites is the items
// that closed rather than a decision.
func targetRedRearmReason(state runstate.State, published runstate.PullRequest, reasoning string) string {
	reason := fmt.Sprintf("the harness withdrew the merge of pull request %d for %s's red check (%s), every item it waited on is closed, and its head is level with the target with its checks passing, so the harness armed it again by the %s method on the promotion run %s made, on its own authority and spending no re-arm. ",
		published.Number, published.TargetRed.TargetBranch, strings.Join(published.TargetRed.WaitingOn(), ", "), published.MergeMethod, state.RunID)
	room := runstate.MaxSelectionReasonBytes - len(reason)
	if room < 0 {
		room = 0
	}
	return singleLine(reason+singleLine(reasoning, room), runstate.MaxSelectionReasonBytes)
}

// rearmablePublication is the publication a re-arm would repeat, and the
// promotion it carries. Every condition here is about the record being able to
// describe a repeated request at all: what is repeated is a merge of a promoted
// commit, by a recorded method, of a request the forge has not merged.
func rearmablePublication(state runstate.State) (runstate.PullRequest, runstate.Integration, error) {
	if state.PullRequest == nil {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf("run %s published nothing, so it has no merge request to repeat", state.RunID)
	}
	published := *state.PullRequest
	if state.CheckFailure != nil && state.CheckFailure.ForgeHeadCommit != "" {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf("pull request %d failed this change's forge checks on %s, so its unchanged revision cannot be re-armed; a repair decided by the development manager continues the preserved change through `yoyo triage repair %s`, with fresh checks and independent review",
			published.Number, state.CheckFailure.ForgeHeadCommit, state.RunID)
	}
	if state.Integration == nil {
		return runstate.PullRequest{}, runstate.Integration{}, UnrearmablePublicationError{RunID: state.RunID, Number: published.Number, Why: fmt.Sprintf(
			"run %s recorded no promotion, so pull request %d carries nothing this harness integrated and its merge is not one to repeat", state.RunID, published.Number)}
	}
	if published.Merged {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf("pull request %d is merged, so there is no dropped merge to repeat", published.Number)
	}
	// A request nothing ever asked the forge to merge has no method on its record,
	// because the method is recorded by the merge request that was never made. What
	// arming it makes is the request the run's own merge would have made, so the
	// method is the one that merge makes rather than one chosen here.
	if strings.TrimSpace(published.MergeMethod) == "" && state.PublicationUnarmed() {
		published.MergeMethod = string(mergeMethod)
	}
	if strings.TrimSpace(published.MergeMethod) == "" {
		return runstate.PullRequest{}, runstate.Integration{}, UnrearmablePublicationError{RunID: state.RunID, Number: published.Number, Why: fmt.Sprintf(
			"run %s records no merge method for pull request %d, so nothing says which request the reviewer's verdict authorized; a re-arm repeats that request rather than making one of its own",
			state.RunID, published.Number)}
	}
	if published.HeadCommit != state.Integration.SourceCommit {
		return runstate.PullRequest{}, runstate.Integration{}, UnrearmablePublicationError{RunID: state.RunID, Number: published.Number, Why: fmt.Sprintf(
			"pull request %d carries %s and run %s promoted %s, so repeating its merge would put on the remote a change the authoritative branch does not have",
			published.Number, published.HeadCommit, state.RunID, state.Integration.SourceCommit)}
	}
	return published, *state.Integration, nil
}

// UnrearmablePublicationError is a publication whose run's record cannot
// describe the merge a re-arm makes: no promotion, no merge method, or a head
// that is not the promoted commit. Nothing the forge does and no later attempt
// changes that, so a re-arm decision about it is one the harness can only
// refuse, and what carries the change forward is a re-run.
//
// The re-arm decision of 2026-09-28 about the supervisor's periodic pass
// (yoyodyne-ifd.413) was of this kind — its run stopped on a diverged target
// before it promoted — and until yoyodyne-edi the watch passed it over without
// a word rather than refusing it
// (docs/diagnoses/yoyodyne-edi-rearms-never-carried-out.md).
type UnrearmablePublicationError struct {
	RunID  string
	Number int
	Why    string
}

func (e UnrearmablePublicationError) Error() string { return e.Why }

// publicationIsSettled reports the run that made the publication being over.
//
// It is the re-run's stoppage precondition applied to the other class of stopped
// work, and it is one condition rather than two: a run still in flight owns its
// own publication, and asking a forge to merge a request that run is still
// working on is what stranded a hand-written amendment on a preserved branch on
// 2026-08-19. A merge the forge is still holding is not a dropped one either, so
// there is nothing to repeat while one is queued.
func publicationIsSettled(state runstate.State, published runstate.PullRequest) error {
	if !state.Status.Terminal() {
		return fmt.Errorf("run %s is recorded as %s rather than ended, so its publication is that run's to finish; a re-arm is refused while anything of it is live",
			state.RunID, state.Status)
	}
	if published.MergeQueued {
		return fmt.Errorf("the forge still has the merge of pull request %d queued, so nothing was dropped and there is nothing to repeat", published.Number)
	}
	return nil
}

// noRearmRunInFlight refuses a re-arm of an item something is already running,
// which is the same rule every triage action that acts on an item's work asks: a
// live run of the item may promote and publish while this is asking a forge
// about the last publication, and the two would be merging into one branch at
// once.
func noRearmRunInFlight(runs RearmRuns, workItemID string) error {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return fmt.Errorf("read what is already in flight: %w", err)
	}
	for _, state := range incomplete {
		if state.WorkItemID == workItemID {
			return fmt.Errorf("%s already has run %s in flight in status %s, so its publication is not settled work to repeat a merge of",
				workItemID, state.RunID, state.Status)
		}
	}
	return nil
}

// docketed reports the publication being on the triage docket, which is what the
// development manager decided against.
//
// The key an entry was written under is accepted in either of its two shapes.
// The docket is an append-only log nothing rewrites, so an entry recorded before
// the pull request joined the key names the run alone, and refusing a re-arm for
// the age of the entry would refuse exactly the oldest publications nobody has
// got to yet. What the re-arm is counted under is the current shape either way:
// that key is derived from the run's own record rather than from the entry.
func (r Rearmer) docketed(key, runID string) error {
	entries, err := r.Docket.List()
	if err != nil {
		return fmt.Errorf("read the triage docket: %w", err)
	}
	legacy := triage.Key(triage.ClassPublication, runID)
	for _, candidate := range entries {
		if candidate.Class != triage.ClassPublication {
			continue
		}
		if candidate.Key == key || candidate.Key == legacy {
			return nil
		}
	}
	return fmt.Errorf("no unfinished publication of run %s is on the triage docket under %s, so there is no publication to re-arm", runID, key)
}

// decided reports a decision of the development manager's that this re-arm may
// carry out, and refuses where there is none. It is the re-run's two questions
// asked of a publication.
//
// The publication's re-arm counter on the item is the decision's own footprint:
// the development manager spends it as the decision is recorded and before
// anything acts on it, so a publication carrying none is one nobody decided this
// about. The counter alone cannot say whether the decision has been acted on —
// it is a total nothing clears — so what has been carried out is read off the
// publication's own record, and a decision already carried out is refused: a
// second drop of the same publication needs a further decision, which past the
// cap is an escalation rather than a larger budget.
//
// The decision standing about the run is read as well, for the reason the
// re-run reads its own: one decision stands per stopped run, and a later one
// supersedes the earlier. A counter spent on a re-arm the development manager
// then decided to escalate instead still reads as one re-arm decided, and a
// re-arm carried out on it would be carried out on a decision nobody holds any
// more. What is standing has to be a re-arm.
func (r Rearmer) decided(state runstate.State, published runstate.PullRequest, key string) (runstate.TriageCounters, runstate.TriageDecision, error) {
	counters, err := r.Decisions.Counters(state.WorkItemID)
	if err != nil {
		return runstate.TriageCounters{}, runstate.TriageDecision{}, fmt.Errorf("read what triage has recorded about %s: %w", state.WorkItemID, err)
	}
	decided := counters.RearmsOf(key)
	if decided < 1 {
		return runstate.TriageCounters{}, runstate.TriageDecision{}, fmt.Errorf(
			"triage has recorded no re-arm of publication %s, so there is no decision here to carry out: the development manager records the decision, which spends that publication's re-arm budget, before the harness asks the forge for anything",
			key)
	}
	if published.MergeRearms >= decided {
		return runstate.TriageCounters{}, runstate.TriageDecision{}, fmt.Errorf(
			"triage has decided %d re-arm(s) of publication %s and the harness has made %d, so this drop has no decision of its own to act on: a merge the forge dropped a second time is an escalation rather than another re-arm",
			decided, key, published.MergeRearms)
	}
	standing, found := counters.DecisionOf(state.RunID)
	if !found {
		return runstate.TriageCounters{}, runstate.TriageDecision{}, permanentCarryOut(triage.CarryOutDecisionMissing, fmt.Errorf(
			"a re-arm of publication %s is recorded as spent and no decision about the stoppage of run %s stands on %s's record, so its durable record disagrees with itself and nothing here is safe to carry out: the decision spends the budget as it is recorded, in one write",
			key, state.RunID, state.WorkItemID))
	}
	if standing.Decision != runstate.TriageDecisionRearm {
		return runstate.TriageCounters{}, runstate.TriageDecision{}, permanentCarryOut(triage.CarryOutDecisionSuperseded, fmt.Errorf(
			"the decision standing about the stoppage of run %s is %q rather than a re-arm, %s: repeating the merge request would carry out a decision nobody holds any more",
			state.RunID, standing.Decision, standing.Cite()))
	}
	return counters, standing, nil
}

// forgeWouldTakeItBack reports the forge having nothing outstanding on the
// request that a person has to supply, and the request still being the one that
// was authorized.
//
// The whole of it is asked of the forge as it stands now rather than of the
// record, because the record says what was true when the merge was dropped and
// what decides this is what is true when the request is repeated. A requirement
// that is still unmet is refused rather than merged past — with administrator
// privileges or by asking again — and what is left is a request whose drop has
// stopped applying.
func (r Rearmer) forgeWouldTakeItBack(ctx context.Context, state runstate.State, published runstate.PullRequest, integration runstate.Integration) error {
	observed, err := r.Forge.State(ctx, published.Branch)
	if err != nil {
		return fmt.Errorf("ask the forge about pull request %d of run %s: %w", published.Number, state.RunID, err)
	}
	if observed.Number != published.Number {
		return fmt.Errorf("the forge reports pull request %d for branch %s, and run %s published request %d there, so what would be repeated is not the request the verdict authorized",
			observed.Number, published.Branch, state.RunID, published.Number)
	}
	if observed.Merged {
		return fmt.Errorf("pull request %d is merged on the forge, so there is no dropped merge to repeat; `yoyo reconcile` settles the run on that answer", published.Number)
	}
	if observed.AutoMerge {
		return fmt.Errorf("the forge has a merge queued for pull request %d again, so nothing is dropped and there is nothing to repeat", published.Number)
	}
	// The head is checked against the commit the promotion made, which is what the
	// repeated request pins. A request that moved carries work no reviewer of this
	// run saw, and the merge would put it on the remote target.
	if head := strings.TrimSpace(observed.HeadCommit); head != "" && head != integration.SourceCommit {
		return fmt.Errorf("pull request %d is at %s on the forge and run %s promoted %s, so the request is not the one the verdict authorized and its merge is not one to repeat",
			published.Number, head, state.RunID, integration.SourceCommit)
	}
	status, err := r.Forge.MergeState(ctx, published.Number)
	if err != nil {
		return fmt.Errorf("ask the forge what is unmet on pull request %d: %w; a re-arm is refused on a request nothing can say the state of", published.Number, err)
	}
	if publish.AwaitsOnlyAPerson(status) {
		return fmt.Errorf("the merge of pull request %d is held by something only a person can satisfy: %s; the harness does not merge past a requirement, so this is an escalation rather than a re-arm",
			published.Number, rearmRequirement(status))
	}
	return nil
}

// landingChecksPass is the gate a first arming passes before the merge request is
// made: the request's head level with its target, and no check on it failing. It
// is asked of the forge now, under the promotion lease, for the reason the merge
// state is: what decides the arming is what is true when it is made.
//
// A re-arm of a dropped merge does not ask it, because what it repeats already
// went through the forge's requirement machinery once. A request nothing ever
// asked the forge to merge has not, and arming one behind its target or red
// would hand the forge a merge that cannot land — the state the queued-merge
// sweep exists to withdraw — so each is refused naming which gate stopped it,
// and nothing is spent. Pending checks are not refused: a merge the forge queues
// waits for them, which is what arming it asks for.
func (r Rearmer) landingChecksPass(ctx context.Context, published runstate.PullRequest, integration runstate.Integration) error {
	if r.Checks == nil {
		return fmt.Errorf("nothing is wired to this harness to read the checks of pull request %d, and a request nothing ever asked the forge to merge is armed only on a reading of them; nothing was spent, so the publication keeps its re-arm",
			published.Number)
	}
	reading, err := r.Checks.Checks(ctx, published.Number, integration.TargetBranch)
	if err != nil {
		return fmt.Errorf("read the checks of pull request %d before arming its merge: %w; a merge is not armed on checks nothing could read, and nothing was spent, so the publication keeps its re-arm",
			published.Number, err)
	}
	if reading.BehindBy > 0 {
		return fmt.Errorf("refused at the head-behind-target gate: pull request %d's head is %d commit(s) behind %s, so its merge would land a change nobody checked against the target it lands on; nothing was spent, so the publication keeps its re-arm, and a re-run hands the change back for a fresh run",
			published.Number, reading.BehindBy, integration.TargetBranch)
	}
	if len(reading.Failing) > 0 {
		names := make([]string, 0, len(reading.Failing))
		for _, failed := range reading.Failing {
			names = append(names, failed.Name)
		}
		return fmt.Errorf("refused at the checks gate: pull request %d has %d failing check(s) on its head (%s), and the harness does not arm a merge its checks refuse; nothing was spent, so the publication keeps its re-arm, and a re-run hands the change back for a fresh run",
			published.Number, len(names), strings.Join(names, ", "))
	}
	return nil
}

// rearmRequirement says what a merge state holds a request on, for a refusal
// that has to name it. A state the forge's vocabulary has grown since is quoted
// rather than described, so the refusal still says something a reader can act on.
func rearmRequirement(status string) string {
	if requirement := publish.MergeRequirement(status); requirement != "" {
		return requirement
	}
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		return "the forge reports its merge state as " + trimmed
	}
	return "the forge reported no merge state for it"
}

// rearmReason is the harness's account of why the request was repeated: the
// decision it verified, the publication it settles, and the reasoning it was
// given.
//
// It is reported rather than recorded, unlike the re-run's, and the difference is
// that a re-run mints a run and this mints nothing. A run's selection reason is
// durable because the run is a new thing the harness chose to do and
// `selected-work-passes-intake-and-records-why` requires it to account for
// itself; a re-arm chooses no work and creates no record to attribute — it
// finishes the publication of work already integrated, and the decision behind it
// is already on the work item where the development manager's conversation wrote
// it.
//
// The two halves are worded apart for the reason the re-run's are. That the
// development manager decided a re-arm of this publication is a fact read from
// the item's durable triage record and is stated as one, cited to the
// conversation and turn the record says it was made on; the prose after it
// arrived with the instruction to carry the decision out, and is attributed to
// that rather than quoted as the development manager's own words.
//
// It is folded to the bound a run's selection reason is held to. That figure is
// not this one's by rights, and it is used anyway because it is the harness's
// settled answer to how long an account of a triage carry-out may be: an
// unbounded one is a whole argument printed into a terminal, and having two
// different bounds for the same sentence would be worse than borrowing one.
func rearmReason(state runstate.State, published runstate.PullRequest, decided runstate.TriageCounters, decision runstate.TriageDecision, key, reasoning string, firstArm bool) string {
	made := "repeated the merge request of"
	if firstArm {
		made = "made the merge request nothing had ever asked the forge for, as the run's own merge would have, of"
	}
	reason := fmt.Sprintf(
		"the development manager's triage decided a re-arm of publication %s, %s — %d recorded against that publication's durable triage budget, %d already made — and the harness %s pull request %d by the %s method, on the promotion run %s made.%s The reasoning given to the harness when it was asked to: ",
		key, decision.Cite(), decided.RearmsOf(key), published.MergeRearms, made, published.Number, published.MergeMethod, state.RunID, crossedRearmCap(decided))
	room := runstate.MaxSelectionReasonBytes - len(reason)
	if room < 0 {
		room = 0
	}
	return singleLine(reason+singleLine(reasoning, room), runstate.MaxSelectionReasonBytes)
}

// crossedRearmCap names the operator override this decision stands on, and says
// nothing on the ordinary publication. A re-arm recorded past the cap exists
// because a person crossed it, and whoever reads this carry-out is owed that in
// the same breath as the decision rather than being left to find it on the item's
// record. The override itself is durable there and is not this account's to keep.
func crossedRearmCap(counters runstate.TriageCounters) string {
	override, found := counters.OverrideOf(runstate.TriageMergeRearmBudget)
	if !found {
		return ""
	}
	return fmt.Sprintf(" It stands on a recorded operator override of the %s cap, by %s at %s.",
		runstate.TriageMergeRearmBudget,
		singleLine(override.DecidedBy, maxCrossedCapAttributionBytes), override.DecidedAt.UTC().Format(time.RFC3339))
}

func (r Rearmer) validate() error {
	var problems []error
	if r.Docket == nil {
		problems = append(problems, errors.New("a re-arm requires the triage docket the decision was made against"))
	}
	if r.Runs == nil {
		problems = append(problems, errors.New("a re-arm requires the durable run state, which is where the publication it repeats is recorded"))
	}
	if r.Forge == nil {
		problems = append(problems, errors.New("a re-arm requires forge access, because what it repeats is a merge request"))
	}
	if r.Worktrees == nil {
		problems = append(problems, errors.New("a re-arm requires the pre-merge check on the remote target, which is what says the request is still the one the gate passed"))
	}
	if r.Decisions == nil {
		problems = append(problems, errors.New("a re-arm requires the item's triage record, which is what says the development manager decided one"))
	}
	return errors.Join(problems...)
}

func (r Rearmer) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result RearmResult) Render() string {
	var rendered strings.Builder
	if result.TargetRed {
		fmt.Fprintf(&rendered, "armed the merge of pull request %d of %s again by the %s method, once the items it waited on for its target's red check closed\n",
			result.Number, result.WorkItemID, result.Method)
	} else if result.FirstArm {
		fmt.Fprintf(&rendered, "armed the merge of pull request %d of %s by the %s method, which nothing had asked the forge for\n",
			result.Number, result.WorkItemID, result.Method)
	} else {
		fmt.Fprintf(&rendered, "repeated the merge request for pull request %d of %s by the %s method\n",
			result.Number, result.WorkItemID, result.Method)
	}
	if result.URL != "" {
		fmt.Fprintf(&rendered, "pull request: %s\n", result.URL)
	}
	fmt.Fprintf(&rendered, "pinned to %s, the commit run %s integrated\n", result.HeadCommit, result.RunID)
	if result.Queued {
		fmt.Fprintln(&rendered, "the forge has the merge queued again and performs it once the base branch's requirements are met; `yoyo reconcile` settles the run when it does")
	} else {
		fmt.Fprintln(&rendered, "the forge merged it on the spot rather than queuing it; `yoyo reconcile` finishes the publication — the merge commit, the consumed branch, and the local target branch")
	}
	if result.TargetRed {
		fmt.Fprintf(&rendered, "no re-arm was spent: %d re-arm(s) of this publication are recorded\n", result.Rearms)
	} else {
		fmt.Fprintf(&rendered, "%d re-arm(s) of this publication are now recorded; a further drop is an escalation rather than another re-arm\n", result.Rearms)
	}
	fmt.Fprintf(&rendered, "repeated because %s\n", result.Reason)
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
