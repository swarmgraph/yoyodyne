package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The harness performs, the roles decide. The developer's phase is what causes
// a branch to be pushed and a pull request to be opened, and the reviewer's
// approving verdict is what causes it to be merged — but the harness executes
// both. The reviewer's adapter prevents writes and tool network access, so the
// role whose verdict authorizes the merge cannot perform one, and its verdict reaches the target branch only
// through the checks, the independence evidence, and the fast-forward rule that
// already gate integration. The developer's side is weaker and the design says
// so: it has a shell, so what keeps it from pushing is its sandbox and its
// contract rather than something enforced here.
//
// Which branch is authoritative is settled the same way. The local target
// branch is the one that moves, and the forge is then asked to merge the pull
// request that carries exactly that commit. A merge commit is what the forge
// makes of it, so the remote target ends up one commit ahead of the local one
// and identical in content: there is one promotion, one reviewed commit, and
// one answer about where a project's work is, published under a merge commit
// the forge owns. A remote that ends up carrying anything else is reported
// rather than reconciled behind the operator's back.
//
// A target branch the forge protects is the exception, and it is decided per
// promotion by asking the forge (landsThroughPullRequest). There the local
// target does not move first: the change lands only by the forge merging its
// pull request, and the local branch follows by a fast-forward onto the remote.
// A local promotion onto a protected target is one the forge may refuse or hold
// for hours, and every one of those left the primary checkout's main ahead of
// origin with nothing that reconciles the two.

func (p Pipeline) publishes() bool {
	return p.Config.Approvals.Publishing == domain.ApprovalAutomatic
}

// resolvePublishing decides whether this run publishes. A project that did not
// ask for it never does. A project that did, but whose repository has no
// configured remote, degrades to exactly the local behavior it had before
// publishing existed, and says so; that is a property of the repository rather
// than a misconfiguration. A project publishing from a fork needs two remotes
// and degrades the same way on either, naming the one that is absent: a
// contributor who has configured their fork before adding it is looking at a
// repository that has the project's remote and not their own.
//
// Everything else — no publisher wired, no forge CLI, no forge authentication —
// is a configuration failure reported before any work is claimed, because a
// project that asked to publish and silently did not would have no way to
// notice.
func (p Pipeline) resolvePublishing(ctx context.Context) (bool, string, error) {
	if !p.publishes() {
		return false, "", nil
	}
	if p.Publisher == nil {
		return false, "", errors.New("automatic publishing requires a pull request publisher")
	}
	configured, err := p.Worktrees.RemoteConfigured(ctx)
	if err != nil {
		return false, "", fmt.Errorf("resolve the publishing remote: %w", err)
	}
	if !configured {
		return false, fmt.Sprintf("the repository has no %q remote, so this run stays local", p.Config.Execution.Remote), nil
	}
	pushConfigured, err := p.Worktrees.PushRemoteConfigured(ctx)
	if err != nil {
		return false, "", fmt.Errorf("resolve the remote run branches are pushed to: %w", err)
	}
	if !pushConfigured {
		return false, fmt.Sprintf("the repository has no %q remote to push run branches to, so this run stays local", p.Config.Execution.PushRemote), nil
	}
	availability, err := p.Publisher.Availability(ctx)
	if err != nil {
		return false, "", fmt.Errorf("check the pull request CLI: %w", err)
	}
	if !availability.Installed {
		return false, "", errors.New("automatic publishing requires the GitHub CLI; install `gh` or set approvals.publishing to human")
	}
	if !availability.Authenticated {
		return false, "", errors.New("the GitHub CLI is not authenticated; run `gh auth login` before handing published work to Yoyodyne")
	}
	return true, "", nil
}

// commitAttempt records what one developer invocation left in the worktree, as
// the harness-owned commit the branch then stands at. Every invocation passes
// through it — the first attempt, every repair round, and every invocation the
// provider ended in a way that reissues the attempt rather than accepting it —
// and it does the same thing whether or not the run publishes.
//
// It used to be the first half of publishAttempt, and both halves of where it
// sat were wrong. A project that does not publish committed nothing until its
// promotion, so its branch tip stood at the base commit for the whole run; and a
// publishing one committed only on the path where an invocation was accepted, so
// an invocation the provider ended twice — a relaunch condition rather than a
// judgement about the work — left its round's change in the worktree and the
// branch tip on the round before it. Run run-f3755e3f spent four invocations
// that way on yoyodyne-ifd.425: its developer fixed both of the reviewer's
// findings, four further invocations were reissued with the same findings and
// the same prompt, and the branch tip stayed at the repair-3 commit d18d295
// while every one of them wrote to the worktree.
//
// A commit that cannot be made ends the round here rather than at the checks or
// the reviewer. What the two of those judge is the worktree, and a worktree the
// harness cannot record is one nothing downstream can be held to: the evidence a
// review is bound to names a tip commit, and a tip that is not the round's is
// exactly how an approval comes to authorize a change nobody read.
func (a *activeRun) commitAttempt(ctx context.Context) error {
	commit, err := a.pipeline.Worktrees.CommitAttempt(ctx, a.worktree, attemptMessage(a.item, a.outcome))
	if commit != "" {
		// Recorded the moment it exists, for the reason the publishing commit below
		// is: the commit is what permits this worktree's HEAD to have moved, and a
		// later step, a later process, or the very next invocation here accepts
		// exactly that commit and nothing an agent could put in its place.
		a.recordHarnessCommit(commit)
	}
	if errors.Is(err, gitworktree.ErrNoChanges) {
		// An invocation that changed nothing has nothing to record. It is not a
		// failure here: the checks and the reviewer are what judge an empty change.
		return nil
	}
	if err != nil {
		return fmt.Errorf("commit what the developer attempt left in the worktree: %w", err)
	}
	return nil
}

// publishAttempt publishes what one developer attempt produced. The work is
// already in a harness commit by the time this runs — commitAttempt made it —
// so what is left is to push the run branch and open the pull request if the
// branch does not have one yet; a repair attempt updates that same request
// rather than opening another. Publishing happens before the checks run, which
// is deliberate: a pull request is where work is reviewed, and work that does
// not pass yet is exactly what a reviewer should be able to see.
func (a *activeRun) publishAttempt(ctx context.Context) error {
	if !a.publishing {
		return nil
	}
	// A change moved onto the target to reconcile a refused replay is no longer
	// built on the branch the pull request carries, so it replaces that branch
	// rather than extending it: a compare-and-swap from exactly the commit the
	// harness published, and the same request carries the reconciled change.
	if a.reconcilingPublishedBranch() {
		return a.republishRebase(ctx, gitworktree.Rebase{HeadCommit: a.state.HarnessCommit})
	}
	// The push is the first place a run touches the network, and a reset one is
	// what killed a run at this exact step. Asking again is safe as well as
	// necessary: commitAttempt has already recorded the work, so a second attempt
	// commits nothing and pushes that same commit.
	//
	// The commit is still recorded inside the attempt rather than after the last
	// one, for the case commitAttempt cannot cover: a worktree written to between
	// the two — nothing this pipeline does, but the ownership check does not
	// assume that — is committed by the push and must be recorded the moment it
	// exists, including when the push that followed it failed. Recorded after the
	// retries instead, the retry's own ownership check would read a HEAD the run
	// had not yet been told about and refuse the push it was asked to repeat.
	publication, err := recoveringValue(ctx, a, runstate.RetryPublishBranch, func(ctx context.Context) (gitworktree.Publication, error) {
		published, publishErr := a.pipeline.Worktrees.PublishBranch(ctx, a.worktree, attemptMessage(a.item, a.outcome))
		if published.Commit != "" {
			a.recordHarnessCommit(published.Commit)
		}
		return published, publishErr
	})
	if errors.Is(err, gitworktree.ErrNoChanges) {
		// An attempt that changed nothing has nothing to publish. It is not a
		// failure here: the checks and the reviewer are what judge an empty
		// change, and they run next.
		return nil
	}
	if err != nil {
		return fmt.Errorf("publish the developer branch: %w", err)
	}
	// Opening the request is idempotent by contract — a repair attempt updates the
	// request its first attempt opened — so a reset connection here is asked again
	// rather than ending a run whose work is already pushed.
	pullRequest, err := recoveringValue(ctx, a, runstate.RetryOpenPullRequest, func(ctx context.Context) (publish.PullRequest, error) {
		return a.pipeline.Publisher.Ensure(ctx, publish.Request{
			Head:  publication.Branch,
			Base:  a.worktree.TargetBranch,
			Title: pullRequestTitle(a.item, a.outcome),
			Body:  pullRequestBody(a.item, a.outcome, a.worktree.TargetBranch),
		})
	})
	if err != nil {
		return fmt.Errorf("open the pull request for the developer branch: %w", err)
	}
	published := &runstate.PullRequest{
		Remote:     publication.Remote,
		Branch:     publication.Branch,
		Number:     pullRequest.Number,
		URL:        pullRequest.URL,
		HeadCommit: publication.Commit,
		State:      pullRequest.State,
		Merged:     pullRequest.Merged,
	}
	a.state.PullRequest = published
	a.outcome.PullRequest = published
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the published pull request: %w", err)
	}
	return nil
}

// republishRebase puts a replayed run branch back where the pull request that
// carries it can see it. Replaying a change onto a moved target rewrites the
// branch, so the published head stops being the change the harness would
// promote, and a forge asked to merge the old head would put work on the remote
// that the authoritative local branch does not have.
//
// The remote branch is replaced from exactly the commit the harness published
// there, which is the same compare-and-swap the local target is advanced by. A
// remote branch carrying anything else is refused rather than overwritten, and
// that refusal stops the run: the promotion has not happened yet, so there is
// nothing outstanding to report and nothing to lose by leaving it to a person.
func (a *activeRun) republishRebase(ctx context.Context, rebase gitworktree.Rebase) error {
	if !a.publishing || a.outcome.PullRequest == nil {
		return nil
	}
	published := *a.outcome.PullRequest
	if published.HeadCommit == rebase.HeadCommit {
		return nil
	}
	publication, err := recoveringValue(ctx, a, runstate.RetryRepublishBranch, func(ctx context.Context) (gitworktree.Publication, error) {
		return a.pipeline.Worktrees.RepublishBranch(ctx, a.worktree, published.HeadCommit)
	})
	if err != nil {
		return fmt.Errorf("republish the replayed developer branch: %w", err)
	}
	published.HeadCommit = publication.Commit
	a.state.PullRequest = &published
	a.outcome.PullRequest = &published
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the republished pull request: %w", err)
	}
	return nil
}

// settleRemoteTarget settles where the remote target branch stands before the
// promotion rather than only after it.
//
// The check publishIntegration makes against the remote runs once the local
// target branch has already moved, and nothing moves it back. A run that finds
// the remote somewhere else there has integrated already: the item closes as
// integrated, an outstanding publication is recorded, and what is left is a
// local target the remote does not carry and no fast-forward reconciles — a
// divergence nothing owns. Asking the same question first is what makes the same
// movement recoverable, because nothing has been promoted yet and there is still
// a change to replay.
//
// A remote that moved onto work this repository can take on is taken on. The
// local target is fast-forwarded onto it, which leaves the change written
// against a commit the target no longer stands at, and the promotion below
// refuses that as drift exactly as it refuses a target another run moved: the
// run replays onto where the target went, runs its checks again, and earns a
// fresh independent verdict. That is what a human push to the target during a
// run costs, and it is a cost rather than a wedge.
//
// A remote that moved somewhere the local branch cannot be brought onto is the
// divergence only a person can settle, and it stops the run here — with both
// branch positions named and the worktree and branch preserved — rather than
// after an item has been closed against it.
func (a *activeRun) settleRemoteTarget(ctx context.Context) error {
	if !a.publishing {
		return nil
	}
	// The question asked before the promotion is the one publishIntegration asks
	// after it, about the target branch as it stands: the remote must be at or
	// behind it, or carry exactly its content above it. The commit this run was
	// written against is both sides of it, because a local target that has since
	// moved away from that commit is drift the promotion itself refuses.
	standing := gitworktree.Integration{
		TargetBranch:         a.worktree.TargetBranch,
		TargetCommit:         a.worktree.BaseCommit,
		PreviousTargetCommit: a.worktree.BaseCommit,
	}
	// Reading where the remote target stands is a network call like any other, and
	// a reset one here would stop a run whose change is built, checked, and
	// approved. The drift this is actually looking for is not a recoverable class,
	// so it is reported as promptly as it always was.
	err := a.recovering(ctx, runstate.RetryRemoteTarget, func(ctx context.Context) error {
		return a.pipeline.Worktrees.VerifyRemoteTarget(ctx, standing)
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, gitworktree.ErrRemoteTargetDrift) {
		return fmt.Errorf("check the remote target branch before promoting: %w", err)
	}
	catchup, catchupErr := recoveringValue(ctx, a, runstate.RetryCatchUpTarget, func(ctx context.Context) (gitworktree.Catchup, error) {
		return a.pipeline.Worktrees.CatchUpTarget(ctx, a.worktree.TargetBranch)
	})
	if catchupErr != nil {
		return fmt.Errorf("bring %s onto what %s has before promoting: %w",
			a.worktree.TargetBranch, a.pipeline.Config.Execution.Remote, catchupErr)
	}
	// A remote with no such branch is not a divergence but a repository whose
	// target has never been published, and the publication that follows the
	// promotion is what puts it there. It reaches here because an absent remote
	// branch is drift to the check above, and it must not stop a first publication.
	if catchup.RemoteCommit == "" {
		return nil
	}
	if catchup.Held != "" {
		return a.blockOnDivergedTarget(catchup)
	}
	a.outcome.Catchup = &catchup
	return nil
}

// mergeMethod is how the harness asks the forge to merge, and it is a constant
// rather than a setting because the choice decides what the remote history is.
// A merge commit is the only method that puts the promoted commit itself on the
// remote target: a squash replaces it with a commit nobody reviewed, and a
// rebase rewrites it — GitHub updates committer information and mints new SHAs
// even for a request that sits directly on its base — leaving the remote
// carrying a copy of the work that the authoritative branch does not have and
// can never fast-forward onto.
//
// The price is that the two branches do not end at the same commit: the forge
// adds its merge commit above the promotion, and the local target does not
// carry it. That is the relationship the harness maintains and checks — the
// promoted commit is on the remote target, and the remote target carries
// exactly its content — rather than an equality no forge merge can produce.
const mergeMethod = publish.MergeCommit

// errPreMergeVerification stands in, inside the retried merge, for a
// verification of the remote target that is over — drifted, or failing on
// something no further wait would outlast. The real failure is kept beside it
// and is what gets reported; this is what the merge's own recovery sees, and it
// carries no recoverable class, so a verification that already spent its window
// cannot be read as a merge failure worth a second one.
var errPreMergeVerification = errors.New("the remote target branch could not be verified before the merge")

// publishIntegration merges the run's pull request, which the approving verdict
// has just authorized. The harness asks the forge to merge and treats its answer
// as the outcome, rather than pushing the integrated commit at the target
// branch: a repository that requires a pull request before anything reaches its
// target — the ordinary reason to open pull requests at all — refuses that push,
// and the pull request would then be closed as a side effect of its commits
// appearing rather than merged deliberately.
//
// The merge is asked for as of when the requirements are met rather than as of
// now, so a protected branch's required checks are waited for by the forge
// instead of being demanded seconds after the reviewer approved. A queued merge
// therefore ends this step: the request is recorded as queued, the run finishes,
// and everything the merge itself would have settled — the remote target it
// produced, the branch it consumed — is settled by reconciliation once the forge
// has actually merged.
//
// The local target branch stays the authoritative one. It has already moved, so
// everything here checks that the promotion is what reaches the remote: the
// published branch must carry the commit that was integrated, the remote target
// must still hold exactly the content that commit was written against, and
// after the merge it must contain the promoted commit and carry its content.
// The forge's merge commit sits above that and stays on the remote; the local
// branch is never rewritten to take it on.
//
// Almost nothing here can fail the run. The work is integrated and the
// authoritative branch already moved, so a publication that did not finish is an
// outstanding fact for an operator, in the same way an outstanding cleanup is.
// A landing through the pull request moved no branch, so there the same
// unfinished merge is the change not landed at all, and integrate stops the run
// on it (blockOnUnlandedPullRequest) rather than closing the item.
//
// The one exception is the remote target having moved after settleRemoteTarget
// looked at it and before this asks the forge — the window a check-then-act
// leaves open, which the promotion lease closes against this machine and against
// nothing else. That is not an unfinished publication but a divergence: the
// local target carries a promotion the remote does not, and no fast-forward
// reconciles the two. Recording it as outstanding and finishing would close the
// item as integrated against a state nobody owns, so it stops the run instead —
// which is still before the item closes, because finish runs after this.
func (a *activeRun) publishIntegration(ctx context.Context) error {
	if !a.publishing || a.outcome.Integration == nil {
		return nil
	}
	integration := *a.outcome.Integration
	// A publishing run that promoted a change holds a pull request: every attempt
	// that changed anything opened or updated one, and a promotion of no change
	// is refused. A run here with none is therefore a record that lost its
	// publication, and until yoyodyne-ifd.402 it returned nil over that: the run
	// finished succeeded, nothing asked the forge to merge, and no surface said
	// so, because every reading of an unfinished publication starts from the
	// request. It is recorded as the outstanding publication it is, in the one
	// sentence the docket, the status line, and the recovering sweep all select
	// on: the item is held out of the pull with the account on it, the docket and
	// the status line name the promotion before anything has asked the forge, and
	// `yoyo reconcile` looks the request up by the run's branch and arms it.
	if a.outcome.PullRequest == nil {
		a.recordPublishFailure(lostPublication(a.state.RunID, a.state.WorkItemID, integration.TargetBranch, a.worktree.Branch))
		return nil
	}
	published := *a.outcome.PullRequest
	a.landingAt(fmt.Sprintf("checking that pull request #%d carries the approved commit", published.Number), landingAsksNothing)
	// What the forge merges is the pull request's head, so a promotion that
	// integrated some other commit must not be merged: the remote would receive a
	// change the authoritative branch does not have. It happens when the worktree
	// was dirty at integration time — something wrote to it after the last
	// attempt was published — and it is reported rather than republished, because
	// the checks and the review ran against what was published.
	if published.HeadCommit != integration.SourceCommit {
		a.recordDroppedMerge(fmt.Errorf("pull request %d carries %s, but the promotion integrated %s; the published branch is not what would merge",
			published.Number, published.HeadCommit, integration.SourceCommit))
		return nil
	}
	// The merge is the last thing a run asks of the network and the most expensive
	// one to lose: the change is promoted locally by the time it is asked, so a
	// reset connection here is a publication left outstanding on work nobody found
	// anything wrong with. A refusal is not one of these — the forge applying its
	// own rules earns the identical answer next time — so only the transport
	// classes are asked again, and the head commit passed along keeps a request
	// that somehow merged in between from being merged twice.
	//
	// A forge asked to merge into a branch that moved would reconcile that
	// movement itself, so the drift check the promotion made locally is made again
	// here, against the remote — and made inside the retry rather than in front of
	// it, so that it is immediately before *this* merge rather than immediately
	// before the first one. A merge reissued after a wait that can reach half an
	// hour is a merge performed on evidence that old, and evidence a later change
	// to the target branch has invalidated is exactly what may not authorize an
	// integration. The head commit pins the candidate and says nothing about the
	// base, so nothing else here would have caught it.
	//
	// The verification's own transport failures are waited out under their own
	// boundary, so a network that drops the read does not spend the merge's window
	// on it; what reaches the merge's recovery is a verification that is over, and
	// it is handed back as a sentinel rather than as its own failure so the merge
	// does not read a recoverable class in it and buy a second window for a
	// question already answered.
	a.landingAt(fmt.Sprintf("asking the forge to merge pull request #%d into %s", published.Number, integration.TargetBranch), landingAsksForge)
	var verifyFailure error
	result, err := recoveringValue(ctx, a, runstate.RetryMerge, func(ctx context.Context) (publish.MergeResult, error) {
		verifyFailure = a.recovering(ctx, runstate.RetryRemoteTarget, func(ctx context.Context) error {
			return a.pipeline.Worktrees.VerifyRemoteTarget(ctx, integration)
		})
		if verifyFailure != nil {
			return publish.MergeResult{}, errPreMergeVerification
		}
		return a.pipeline.Publisher.Merge(ctx, publish.MergeRequest{
			Number:     published.Number,
			HeadCommit: published.HeadCommit,
			Method:     mergeMethod,
		})
	})
	if verifyFailure != nil {
		a.landingAt(fmt.Sprintf("checking where %s's %s stands before asking the forge to merge pull request #%d", a.pipeline.Config.Execution.Remote, integration.TargetBranch, published.Number), landingReadsRemote)
		cause := fmt.Errorf("check the remote target branch before merging: %w", verifyFailure)
		// A landing through the pull request moved nothing, so a remote that moved
		// under it is the race a promotion can lose rather than a divergence: the
		// change is replayed onto where the target went, as a drifted local target
		// is, and the merge is asked for again with the gate re-earned.
		if integration.ThroughPullRequest && errors.Is(verifyFailure, gitworktree.ErrRemoteTargetDrift) {
			return a.replayUnlandedChange(ctx, cause)
		}
		// The merge was never asked for, and nothing here will ask again: this is
		// the origin-moved refusal that four promotions once waited hours on with
		// nothing said. The divergence below stops the run as well where the
		// movement is one no fast-forward reconciles, and the two say different
		// things — that the publication is not going to happen by itself, and that
		// this run is stopped on something a person has to settle.
		a.recordDroppedMerge(cause)
		if errors.Is(verifyFailure, gitworktree.ErrRemoteTargetDrift) {
			return a.settlePromotedDivergence(ctx, integration, cause)
		}
		return nil
	}
	if err != nil {
		a.recordDroppedMerge(err)
		return nil
	}
	published.MergeMethod = string(mergeMethod)
	// A queued merge is the ordinary answer from a protected branch: the forge
	// performs it once the required checks pass, minutes after this run is over.
	// The run records it as queued and finishes rather than waiting for a
	// confirmation that cannot arrive while it watches — and leaves the published
	// branch on the remote, because that branch is what the forge still has to
	// merge. Reconciliation is what settles the queue afterwards.
	if result.Queued {
		published.MergeQueued = true
		queuedAt := a.pipeline.clock().Now()
		published.QueuedAt = &queuedAt
		a.state.PullRequest = &published
		a.outcome.PullRequest = &published
		a.state.UpdatedAt = a.pipeline.clock().Now()
		if err := a.pipeline.Store.Save(a.state); err != nil {
			a.recordPublishFailure(fmt.Errorf("record the queued merge: %w", err))
		}
		return nil
	}
	a.landingAt(fmt.Sprintf("confirming the forge merged pull request #%d", published.Number), landingAsksForge)
	merged, err := a.awaitMerge(ctx)
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the pull request merged: %w", err))
		return nil
	}
	published.State = merged.State
	published.Merged = merged.Merged
	a.state.PullRequest = &published
	a.outcome.PullRequest = &published
	if !merged.Merged {
		a.recordDroppedMerge(fmt.Errorf("pull request %d is still %s after the forge accepted the merge into %s",
			published.Number, strings.ToLower(nonEmpty(merged.State, "in an unreported state")), integration.TargetBranch))
		return nil
	}
	// The forge says it merged; this is what its merge actually did to the
	// branch. The recorded commit is the forge's merge commit, which is the one
	// commit the remote target has that the local one does not — the forge's own
	// where it is the merge of this promotion on the remote, and otherwise found in
	// the remote history.
	remoteTarget, err := recoveringValue(ctx, a, runstate.RetryRemoteTarget, func(ctx context.Context) (string, error) {
		return a.pipeline.Worktrees.ConfirmRemoteTarget(ctx, integration, merged.MergeCommit)
	})
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the merge reached %s: %w", integration.TargetBranch, err))
		return nil
	}
	published.MergeCommit = remoteTarget
	// The merge left the remote one commit ahead of the local target, so the
	// branch a person reads is now behind the forge by the commit the forge made
	// of this very promotion. Catching it up is the last step of the promotion
	// rather than a chore for afterwards, and it happens here because here is
	// where this run still holds the branch's promotion lease.
	a.catchUpTarget(ctx, integration.TargetBranch)
	// The published branch is debris once its work is on the target, and it is
	// removed on the same evidence the local branch is: the exact commit that was
	// published and merged.
	if err := a.recovering(ctx, runstate.RetryDeleteRemoteBranch, func(ctx context.Context) error {
		return a.pipeline.Worktrees.DeleteRemoteBranch(ctx, a.worktree, published.HeadCommit)
	}); err != nil {
		a.recordPublishFailure(fmt.Errorf("delete the merged remote branch: %w", err))
		return nil
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		a.recordPublishFailure(fmt.Errorf("record the merged pull request: %w", err))
	}
	return nil
}

// settlePromotedDivergence decides what a remote target that moved after the
// promotion leaves behind, and it is the residual half of settleRemoteTarget:
// the same movement, arriving in the window a check-then-act cannot close, with
// the local target branch already advanced.
//
// The promotion cannot be taken back, so the only question left is whether the
// two branches can still be brought together. A remote that swept the promotion
// in along the way — a forge merge carrying this commit and somebody else's — is
// caught up onto and leaves an ordinary outstanding publication: the work is on
// both branches and only the merge request did not happen. A remote that has
// gone somewhere the local branch cannot reach is the divergence nothing
// reconciles, and it stops the run so the item is not closed as integrated
// against it.
//
// A remote with no such branch is neither: it is a target this repository has
// never published, and the outstanding publication already says so.
func (a *activeRun) settlePromotedDivergence(ctx context.Context, integration gitworktree.Integration, cause error) error {
	catchup, err := recoveringValue(ctx, a, runstate.RetryCatchUpTarget, func(ctx context.Context) (gitworktree.Catchup, error) {
		return a.pipeline.Worktrees.CatchUpTarget(ctx, integration.TargetBranch)
	})
	if err != nil {
		// Whether the branches can be reconciled is exactly what could not be
		// established, and a promotion whose remote nobody could look at must not
		// close an item either.
		return a.blockOnPromotedDivergence(integration, gitworktree.Catchup{
			TargetBranch: integration.TargetBranch,
			LocalCommit:  integration.TargetCommit,
			Held:         err.Error(),
		}, cause)
	}
	if catchup.RemoteCommit == "" || catchup.Held == "" {
		a.outcome.Catchup = &catchup
		return nil
	}
	return a.blockOnPromotedDivergence(integration, catchup, cause)
}

// landsThroughPullRequest decides how this run promotes, by asking the forge
// whether the target branch is protected, and says what it found on the outcome.
//
// A protected target lands through the pull request and nothing else. Its local
// copy is never moved ahead of the forge — only fast-forwarded onto what the
// forge has — because a local promotion the forge then refused or has not merged
// yet leaves the primary checkout's target ahead of the remote, where every later
// run that has to bring it onto the remote collides with it. That happened on
// 2026-09-20 and again on 2026-09-24, and each time the checkout was reset by
// hand.
//
// A question the forge did not answer takes the same path. It costs an
// unprotected target nothing it needs — the change still lands, by the merge —
// while the other reading would move a branch the forge may refuse. The question
// is asked once per promotion attempt, under the promotion lease, and it is the
// question scripts/cut-release.sh asks before a release cut.
//
// A run that does not publish never asks: it has no forge, and promoting the
// local target is the whole of what it does.
func (a *activeRun) landsThroughPullRequest(ctx context.Context) bool {
	if !a.publishing {
		return false
	}
	target := a.worktree.TargetBranch
	protection, err := a.pipeline.Publisher.Protection(ctx, target)
	switch {
	case err != nil:
		a.outcome.TargetProtection = fmt.Sprintf(
			"whether %s is protected could not be asked of the forge (%v), so it was taken as protected: the change lands through its pull request, and the local %s is moved only by a fast-forward onto what the forge has",
			target, err, target)
		return true
	case protection.Protected:
		a.outcome.TargetProtection = fmt.Sprintf(
			"%s is protected on the forge by %s, so the change lands through its pull request, and the local %s is moved only by a fast-forward onto what the forge has",
			target, nonEmpty(protection.By, "a rule"), target)
		return true
	default:
		a.outcome.TargetProtection = fmt.Sprintf(
			"%s is not protected on the forge, so the change was promoted onto the local %s and its pull request merged after it",
			target, target)
		return false
	}
}

// landedThroughPullRequest reports a landing through the pull request that
// reached the target: the forge merged it, or holds the merge queued for when the
// branch's requirements are met. Everything else publishIntegration can end on —
// a refusal, a request that is not what was checked, a merge nobody could
// confirm happened — leaves the change on its pull request alone.
func (a *activeRun) landedThroughPullRequest() bool {
	published := a.outcome.PullRequest
	return published != nil && (published.Merged || published.MergeQueued)
}

// blockOnUnlandedPullRequest ends a run whose landing through the pull request
// did not reach the target branch. It is the protected target's counterpart of
// an outstanding publication: there, the change was already on the local target
// and only the publication was left, so the item closed; here, nothing moved,
// so closing the item would record as landed a change that is on no branch but
// its own. The item is handed to a person with the forge's answer instead, and
// the run keeps its record of the promotion it was making and of the dropped
// merge, which is what `yoyo triage rearm` repeats once whatever the forge
// required has been met.
func (a *activeRun) blockOnUnlandedPullRequest(integration gitworktree.Integration) error {
	unlanded := fmt.Errorf("the change was not landed on %s: %s",
		integration.TargetBranch, nonEmpty(a.outcome.PublishFailure, "the forge did not merge its pull request"))
	if err := a.block(renderUnlandedPullRequestNotes(a.outcome, integration, unlanded.Error())); err != nil {
		return stoppedBy(runstate.StopPublish, errors.Join(unlanded, fmt.Errorf("record the unlanded pull request as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopPublish, unlanded)
}

// replayUnlandedChange answers a remote target that moved after the landing was
// prepared and before the forge was asked to merge. Nothing was promoted, so
// nothing is outstanding: the local target is brought onto the remote by a
// fast-forward, and the run is sent to replay its change onto it exactly as a
// promotion that lost its race is. A remote the local target cannot be brought
// onto is the divergence only a person settles, before anything was promoted.
//
// The promotion this run was preparing is taken back off the record first,
// because it no longer describes anything: the change is about to be rewritten
// onto a new base, and a run stopped on the way there promoted nothing.
func (a *activeRun) replayUnlandedChange(ctx context.Context, cause error) error {
	a.outcome.Integration = nil
	a.state.Integration = nil
	a.landingAt(fmt.Sprintf("bringing the local %s onto %s's, which moved before the forge was asked to merge", a.worktree.TargetBranch, a.pipeline.Config.Execution.Remote), landingReadsRemote)
	catchup, err := recoveringValue(ctx, a, runstate.RetryCatchUpTarget, func(ctx context.Context) (gitworktree.Catchup, error) {
		return a.pipeline.Worktrees.CatchUpTarget(ctx, a.worktree.TargetBranch)
	})
	if err != nil {
		return fmt.Errorf("%w; bring %s onto what %s has: %w", cause, a.worktree.TargetBranch, a.pipeline.Config.Execution.Remote, err)
	}
	if catchup.Held != "" {
		return a.blockOnDivergedTarget(catchup)
	}
	a.outcome.Catchup = &catchup
	return fmt.Errorf("%w: %w", gitworktree.ErrTargetDrift, cause)
}

// mergeConfirmationDelays are the waits between asking the forge whether the
// merge it performed is reported on the pull request itself. A forge's own
// record of a request can lag the merge it just made by a moment, so asking
// once would report a successful publication as outstanding whenever the
// harness happened to be quicker. The waits are few and short: this is a race
// with a remote bookkeeping step, not a state a run should sit on. A merge the
// forge queued rather than performed is never waited for here — it lands long
// after any wait a run could hold, and reconciliation settles it instead.
var mergeConfirmationDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// awaitMerge asks the forge whether the pull request it has just merged now
// reports as merged, retrying while it still reports the request open.
//
// A query that fails on the forge's own answer is returned immediately: that is
// something an operator has to see, and asking again would only delay it. A
// query that fails on the connection carrying it is a different thing and is
// waited out like every other transport failure — the merge has already
// happened, and reporting a completed publication as outstanding because the
// confirmation could not be fetched is precisely the loss this exists to stop.
func (a *activeRun) awaitMerge(ctx context.Context) (publish.PullRequest, error) {
	askForge := func(ctx context.Context) (publish.PullRequest, error) {
		return recoveringValue(ctx, a, runstate.RetryMergeConfirmation, func(ctx context.Context) (publish.PullRequest, error) {
			return a.pipeline.Publisher.State(ctx, a.worktree.Branch)
		})
	}
	merged, err := askForge(ctx)
	for attempt := 0; err == nil && !merged.Merged && attempt < len(mergeConfirmationDelays); attempt++ {
		if waitErr := a.pipeline.sleep(ctx, mergeConfirmationDelays[attempt]); waitErr != nil {
			// A cancelled or expired run stops waiting, and reports what the forge
			// last said rather than inventing a verdict about it.
			return merged, nil
		}
		merged, err = askForge(ctx)
	}
	return merged, err
}

// mergeQueued reports a merge the forge accepted and has not performed. It is
// the one way a run can finish with its promotion made and its publication
// still undecided, which is why it is what defers closing the work item: the
// forge either merges minutes later or drops the request, and only its answer
// says whether the change was integrated anywhere but this repository.
func (a *activeRun) mergeQueued() bool {
	return a.outcome.PullRequest != nil && a.outcome.PullRequest.MergeQueued
}

// catchUpTarget brings the local target branch onto the commit the forge's
// merge left on the remote, and records what happened either way.
//
// Nothing here can fail the run, and unlike an outstanding publication nothing
// here is even recorded durably. A catch-up is idempotent, it belongs to no run
// in particular, and `yoyo reconcile` sweeps every target branch the harness
// knows about — so a catch-up that was held is a fact for whoever reads this run
// rather than a debt the run has to carry.
func (a *activeRun) catchUpTarget(ctx context.Context, targetBranch string) {
	catchup, err := recoveringValue(ctx, a, runstate.RetryCatchUpTarget, func(ctx context.Context) (gitworktree.Catchup, error) {
		return a.pipeline.Worktrees.CatchUpTarget(ctx, targetBranch)
	})
	if err != nil {
		catchup.TargetBranch = targetBranch
		catchup.Held = err.Error()
	}
	a.outcome.Catchup = &catchup
}

// recordPublishFailure records an outstanding publication everywhere it has to
// be visible without recasting a promoted change as a failed one. A failure
// saving the record itself is folded into the reported failure rather than
// hidden: durable state that disagrees with the report is the thing an operator
// cannot diagnose.
func (a *activeRun) recordPublishFailure(cause error) {
	a.outcome.PublishFailure = cause.Error()
	a.state.PublishFailure = a.outcome.PublishFailure
	a.outcome.StopClass = runstate.StopPublish
	a.state.StopClass = a.outcome.StopClass
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		a.outcome.PublishFailure = errors.Join(cause, fmt.Errorf("record the outstanding publication: %w", err)).Error()
	}
}

// lostPublication is what the record says about a promotion whose pull request
// it does not hold. The sentence is the durable schema's, because the docket,
// the status line, and the recovering sweep all select on it: it is what tells
// this record from a local promotion, which records no request and no failure.
func lostPublication(runID, workItemID, targetBranch, branch string) error {
	return errors.New(runstate.LostPublication(runID, workItemID, targetBranch, branch))
}

// publicationRecorded refuses to complete a run whose outcome names a pull
// request the durable record does not hold, holds as a different one, or holds
// in a different arming state.
//
// Both are written by one statement wherever a request is obtained, so the two
// cannot disagree by any path this code has — which is exactly why a
// disagreement is refused rather than completed over. The outcome is what the
// run prints as its summary and the record is what every surface reads
// afterwards, and a summary naming a request the record does not is the shape
// yoyodyne-ifd.402 was admitted on: a change on the forge that nothing on any
// surface says is waiting. The arming state is compared with the number because
// it is what the surfaces act on: a record holding the right request with the
// merge not queued, while the summary says it is, is a merge reconciliation
// would never settle. The record is read back from the store rather than from
// memory, because what is being checked is that the write landed.
func (a *activeRun) publicationRecorded() error {
	reported := a.outcome.PullRequest
	if reported == nil {
		return nil
	}
	durable, err := a.pipeline.Store.Load(a.state.RunID)
	if err != nil {
		return fmt.Errorf("confirm the published pull request is on the run record before completing: %w", err)
	}
	recorded := durable.PullRequest
	if recorded == nil {
		return fmt.Errorf("the run reports pull request %d and its durable record holds none, so the publication was lost between obtaining it and completing; the run is refused completion rather than recorded succeeded over a request nothing would see",
			reported.Number)
	}
	if recorded.Number != reported.Number {
		return fmt.Errorf("the run reports pull request %d and its durable record holds pull request %d, so the two disagree about which request carries the work; the run is refused completion rather than recorded succeeded over the disagreement",
			reported.Number, recorded.Number)
	}
	if recorded.MergeQueued != reported.MergeQueued || recorded.Merged != reported.Merged || recorded.MergeMethod != reported.MergeMethod {
		return fmt.Errorf("the run reports pull request %d %s and its durable record holds it %s, so the two disagree about what was asked of the forge; the run is refused completion rather than recorded succeeded over the disagreement",
			reported.Number, describeArming(*reported), describeArming(*recorded))
	}
	return nil
}

// describeArming says what a record claims was asked of the forge about a
// request, for a refusal that has to name both sides of a disagreement.
func describeArming(published runstate.PullRequest) string {
	arming := "with no merge asked for"
	switch {
	case published.Merged:
		arming = "merged"
	case published.MergeQueued:
		arming = "with its merge queued"
	}
	if published.MergeMethod != "" {
		arming += " by the " + published.MergeMethod + " method"
	}
	return arming
}

// recordDroppedMerge records an outstanding publication whose merge is also over:
// the forge refused it, or the state of the remote target says it must not be
// asked for. It is the same record recordPublishFailure writes with the moment
// the merge stopped being expected written beside it, and it is separate because
// most publication failures are not that. A merge the forge performed and a step
// after it that failed — the confirmation, the branch removal, the save — leaves
// a publication to finish rather than a merge that is not going to happen, and a
// record saying otherwise would announce a dropped merge over a merged change.
//
// The moment is what makes the drop sayable at all. Everything else about it was
// always readable from the record by whoever went and looked; what nothing held
// was when it became true, so nothing could say it as it happened.
func (a *activeRun) recordDroppedMerge(cause error) {
	a.state.MergeDrop = &runstate.MergeDrop{At: a.pipeline.clock().Now(), Reason: cause.Error()}
	a.recordPublishFailure(cause)
}

// attemptMessage describes one developer attempt in the harness-owned commit
// that publishes it. A published run reaches integration with these commits
// already on its branch, so this is the message the promoted commit carries;
// the review evidence that authorized the promotion is recorded on the work
// item and in durable run state, where it belongs whether or not a run
// published anything.
func attemptMessage(item beads.WorkItem, outcome Outcome) string {
	subject := strings.TrimSpace(fmt.Sprintf("yoyodyne: %s %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)))
	body := []string{
		"",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Base: " + outcome.BaseCommit,
		fmt.Sprintf("Attempt: %d", outcome.RepairAttempts+1),
		"Developer session: " + outcome.ProviderSessionID,
	}
	return subject + "\n" + strings.Join(body, "\n") + "\n"
}

// maxPullRequestTitleBytes bounds the work item title a pull request title
// carries. A pull request title is not a commit subject and is not bounded like
// one: the forge accepts far more, and cutting a title at the subject bound is
// what left PR #314 titled "... regist". The item id sits in front of what this
// bounds, so the whole stays about the length a forge shows in a listing.
const maxPullRequestTitleBytes = 100

// Bounds on what one pull request body carries of the work item's own text and
// of the change's computed shape. A forge refuses a body past its own limit, so
// neither a long item nor a change touching hundreds of files may be what stops
// a run publishing. Each block is cut on a line boundary and says it was cut.
const (
	maxPullRequestTextBytes  = 12 << 10
	maxPullRequestFilesBytes = 12 << 10
)

func pullRequestTitle(item beads.WorkItem, outcome Outcome) string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", outcome.WorkItemID, wordBounded(item.Title, maxPullRequestTitleBytes)))
}

// wordBounded folds a title into one bounded line and cuts it at a word
// boundary, so a title that did not fit ends on a word rather than inside one,
// and says where it was cut: a title that simply stops reads as the whole
// title. A single word longer than the bound has no boundary to cut at, and
// falls back to the rune-boundary cut a commit subject takes.
func wordBounded(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := singleLine(folded, limit-len("…"))
	if word := strings.LastIndexByte(cut, ' '); word > 0 {
		cut = cut[:word]
	}
	return strings.TrimRight(cut, " ") + "…"
}

// pullRequestBody says what a reader of the pull request needs and nothing an
// agent wrote. Everything here is either the work item's own text — its
// description and its acceptance criteria, admitted through the gate that
// admits work, so governed rather than authored by the run — or a mechanical
// fact computed from the change itself. That is what lets a reader tell what
// the request contains without opening the tracker, while the rule a pull
// request opened by the harness must not become a channel for model output
// nobody reviewed still holds: the developer's own summary, its reports, its
// proposals, and the item's notes stay on the run and on the work item.
func pullRequestBody(item beads.WorkItem, outcome Outcome, base string) string {
	lines := []string{
		fmt.Sprintf("Opened by Yoyodyne for work item `%s`: %s", outcome.WorkItemID, wordBounded(item.Title, maxPullRequestTitleBytes)),
	}
	lines = append(lines, itemSection("What the work item asks for", item.Description)...)
	lines = append(lines, itemSection("Acceptance criteria", item.AcceptanceCriteria)...)
	lines = append(lines, changeSection(outcome.Changes)...)
	lines = append(lines,
		"",
		"## Harness evidence",
		"",
		"- Run: `"+outcome.RunID+"`",
		"- Branch: `"+outcome.Branch+"` into `"+base+"`",
		"- Base commit: `"+outcome.BaseCommit+"`",
		fmt.Sprintf("- The harness merges this request with the `%s` method once the configured checks pass and an independent reviewer approves it; `docs/designs/v1-harness-design.md` says what that involves.", mergeMethod),
	)
	return strings.Join(lines, "\n")
}

// itemSection carries one field of the work item into the body under its own
// heading. A field the item does not have gets no heading: an empty section
// says less than nothing.
func itemSection(heading, text string) []string {
	text = boundedBlock(text, maxPullRequestTextBytes)
	if text == "" {
		return nil
	}
	return []string{"", "## " + heading, "", text}
}

// changeSection is what the change mechanically is: the diff stat and the
// changed-file listing the harness computed against the commit the run was
// written against. Nothing in it is authored, which is why it can be published,
// and it is what tells a reader what the request touches before they open the
// diff. A run whose change was never summarized carries neither rather than an
// empty listing, which would read as a change that touched nothing.
func changeSection(changes gitworktree.ChangeSummary) []string {
	files := boundedBlock(changes.Status, maxPullRequestFilesBytes)
	stat := boundedBlock(changes.DiffStat, maxPullRequestFilesBytes)
	if files == "" && stat == "" {
		return nil
	}
	lines := []string{"", "## What changed", ""}
	if files != "" {
		lines = append(lines, "Changed files:", "", "```", files, "```")
	}
	if stat != "" {
		if files != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "Diff stat:", "", "```", stat, "```")
	}
	return lines
}

// boundedBlock cuts a block of text to a byte bound on a line boundary, and
// says that it cut: a listing that silently stops reads as a complete one. A
// block with no line boundary to cut at is cut on a rune boundary instead,
// because text truncated mid-rune is not text.
func boundedBlock(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	cut := limit
	if line := strings.LastIndexByte(text[:cut], '\n'); line > 0 {
		cut = line
	} else {
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
	}
	return strings.TrimRight(text[:cut], "\n") + "\n\n…cut to keep this body inside what the forge accepts; the work item and the diff carry the whole of it."
}

// renderPublishNotes carries the publication into the tracker, so an operator
// reading a work item can find the pull request without reconstructing it.
func renderPublishNotes(outcome Outcome) []string {
	var lines []string
	if outcome.PullRequest != nil {
		lines = append(lines,
			fmt.Sprintf("Pull request: #%d %s", outcome.PullRequest.Number, outcome.PullRequest.URL),
			"Pull request merged: "+strconv.FormatBool(outcome.PullRequest.Merged),
		)
		// A queued merge is the ordinary outcome on a protected branch, and it is
		// not the same fact as an unmerged one: the forge has accepted it and will
		// perform it once the required checks pass. A reader of the item has to be
		// able to tell the two apart without going to the forge — and has to be
		// told why this item is not closed, because a queued merge is the one
		// integrated outcome whose closure waits for somebody else's answer.
		if outcome.PullRequest.MergeQueued {
			lines = append(lines,
				"Merge queued: the forge merges this request once the base branch's requirements are met; `yoyo reconcile` settles the run when it does.",
				"This item stays open until then: it is closed when the forge's merge is confirmed, and handed back to you with a blocker if the forge drops it.")
		}
		// The method decides what the remote history looks like, and the merge
		// commit is the one commit the remote target has that the authoritative
		// local branch does not. A reader of the item can tell what the remote
		// history became without going to the forge for it.
		if outcome.PullRequest.MergeMethod != "" {
			lines = append(lines, "Merge method: "+outcome.PullRequest.MergeMethod)
		}
		if outcome.PullRequest.MergeCommit != "" {
			lines = append(lines, fmt.Sprintf("Remote target commit: %s (the forge's merge commit above the promoted commit)", outcome.PullRequest.MergeCommit))
		}
	}
	if outcome.PublishSkipped != "" {
		lines = append(lines, "Publishing skipped: "+outcome.PublishSkipped)
	}
	if outcome.TargetProtection != "" {
		lines = append(lines, "Target branch: "+outcome.TargetProtection)
	}
	if outcome.PublishFailure != "" {
		standing := "The change is integrated into the local target branch, which is the authoritative one; only its publication is unfinished."
		if outcome.Integration != nil && outcome.Integration.ThroughPullRequest {
			standing = "The forge merged the change; what is unfinished is the harness's confirmation and hygiene after that merge, which `yoyo reconcile` finishes."
		}
		lines = append(lines, "Publication outstanding: "+outcome.PublishFailure, standing)
	}
	return append(lines, renderCatchupNotes(outcome.Catchup)...)
}

// renderUnlandedPullRequestNotes is the blocker a landing through the pull
// request leaves when the forge did not take the change. It says first that the
// local target was never moved, because that is what makes this a different
// hand-back from a dropped merge after a local promotion: nothing needs undoing,
// and the change is exactly where the pull request has it.
func renderUnlandedPullRequestNotes(outcome Outcome, integration gitworktree.Integration, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: its target branch is protected, so the change was to land through its pull request, and the forge did not merge it.",
		fmt.Sprintf("The local %s was not moved, so nothing needs undoing: the change is on its pull request and on no target branch, which is why the item is left open rather than closed as integrated.", integration.TargetBranch),
		"Once whatever the forge requires is met, the development manager's `rearm` decision repeats the merge request through `yoyo triage rearm`, or a person merges the request.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		fmt.Sprintf("Commit to land: %s, onto %s at %s", integration.SourceCommit, integration.TargetBranch, integration.PreviousTargetCommit),
	}
	if outcome.TargetProtection != "" {
		lines = append(lines, "Target branch: "+outcome.TargetProtection)
	}
	if outcome.PullRequest != nil {
		lines = append(lines, fmt.Sprintf("Pull request left unmerged: #%d %s", outcome.PullRequest.Number, outcome.PullRequest.URL))
	}
	return withLandingStop(strings.Join(append(lines, renderReviewNotes(outcome)...), "\n"), outcome, failure)
}

// renderCatchupNotes says where the local target branch was left relative to
// the forge. A branch that was already there is not reported at all: it is the
// ordinary state and a note for it would say nothing.
func renderCatchupNotes(catchup *gitworktree.Catchup) []string {
	switch {
	case catchup == nil:
		return nil
	case catchup.Held != "":
		return []string{
			fmt.Sprintf("Local %s was left at %s: %s", catchup.TargetBranch, nonEmpty(catchup.LocalCommit, "an unresolved commit"), catchup.Held),
			"`yoyo reconcile` catches it up on its next sweep.",
		}
	case catchup.Advanced:
		lines := []string{fmt.Sprintf("Local %s caught up to %s, the commit the forge's merge left on the remote", catchup.TargetBranch, catchup.RemoteCommit)}
		if len(catchup.Discarded) > 0 {
			lines = append(lines, "Discarded export churn to let it through: "+strings.Join(catchup.Discarded, ", "))
		}
		return lines
	default:
		return nil
	}
}
