package orchestrator

// Keeping the target branch current with the team's branch
// (docs/designs/integrating-to-a-local-branch.md, section 3).
//
// With execution.upstream_branch set, work lands on the target branch (say
// staging) and the team merges the work they want into their own (say main).
// What the team merged is then ahead of what the target holds, so each forge
// check asks whether the team's branch has moved past the target and, where it
// has, builds a merge of the team's tip into the target, checks it, has it
// reviewed independently, and lands that exact commit. The target is never
// rebased, because pull requests and other contributors point at its commits.
//
// The update borrows the merge queue's candidate checkout and its candidate
// branch naming, since it is the same thing: a merge commit built in a checkout
// of its own from the target as it stands, verified before anything moves, and
// landed as a compare and swap. It has no work item or run behind it, so
// nothing is charged to any item's repair budget, and the record of what it did
// is runstate.TeamBranchUpdate. Every step saves that record before the next,
// so a later check resumes where this one stopped; a step that cannot reach
// the remote or the forge changes nothing and returns an error for the next
// check to try again.
//
// A merge that conflicts is not resolved by the harness itself. The update
// waits, and the development manager is told once which paths conflict and which
// items on the target touched them. Her decision names one item, and each next
// check starts a run of it (teambranchresolution.go) that resolves the conflict
// on the target, up to three rounds. What such a run approves is turned into
// the merge commit of the update, which is then checked, reviewed and landed as
// any update is. The update is tried again whenever either branch moves.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// teamBranchConflictPathsShown bounds the conflicting paths a report names.
const teamBranchConflictPathsShown = 20

// teamBranchItemSearchDepth is how many commits that changed a conflicting path
// are read to find the items on the target that touched it.
const teamBranchItemSearchDepth = 200

// TeamBranchGit is the Git the update uses. It is satisfied by
// gitworktree.Manager, and is narrow so a test can hand it a real repository
// through that manager.
type TeamBranchGit interface {
	RemoteTargetCarries(ctx context.Context, branch string, commits ...string) (gitworktree.TargetReading, error)
	CatchUpTarget(ctx context.Context, branch string) (gitworktree.Catchup, error)
	TargetCommit(ctx context.Context, branch string) (string, error)
	TargetHolds(ctx context.Context, branch, commit string) (bool, error)
	HoldsCommit(ctx context.Context, commit string) (bool, error)
	BuildQueueCandidate(ctx context.Context, request gitworktree.QueueCandidateRequest) (gitworktree.QueueCandidate, error)
	BuildConflictedMerge(ctx context.Context, request gitworktree.ConflictedMergeRequest) (gitworktree.ConflictedMerge, error)
	CommitMergeOf(ctx context.Context, source, base, head, message string) (string, error)
	RestoreQueueCandidate(ctx context.Context, entry, commit string) (string, error)
	RemoveQueueCandidate(ctx context.Context, path string) error
	CandidateChanges(ctx context.Context, baseCommit, candidate string, limits gitworktree.DiffLimits) (gitworktree.BranchChange, error)
	CommitParents(ctx context.Context, commit string) ([]string, error)
	CommitsChanging(ctx context.Context, ref, path string, maxCommits int) ([]string, error)
	PromoteQueueCandidate(ctx context.Context, branch, base, candidate string) error
	QueueCandidateBranchCommit(ctx context.Context, branch string) (string, bool, error)
	PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration, mergeCommit string) (string, error)
}

// TeamBranchUpdateRecords keeps the update's record and the lease that makes a
// process the one working it. It is satisfied by runstate.TeamBranchUpdateStore.
type TeamBranchUpdateRecords interface {
	Load(targetBranch string) (runstate.TeamBranchUpdate, bool, error)
	Save(update runstate.TeamBranchUpdate) error
	LeaseUpdater(ctx context.Context, targetBranch string) (*runstate.Lease, bool, error)
}

// TeamBranchUpdater brings the target branch up to date with the team's branch,
// one update at most per call.
type TeamBranchUpdater struct {
	// Pipeline supplies the configuration, the check runner, the run store (the
	// promotion lease, and the runs waiting for the team's merge), the forge,
	// the operator's pause and the clock.
	Pipeline *Pipeline
	Git      TeamBranchGit
	Records  TeamBranchUpdateRecords
	// Reviewer makes the independent review of the merge.
	Reviewer BranchReviewer
	// Reports is the development manager's list.
	Reports ReportCollector
	// Events receives the checks' event stream. Nil discards it.
	Events func(execution.Event) error
	// Resolve makes one round of resolving a conflict: a run of the item the
	// development manager named (Pipeline.ResolveTeamBranchConflict). Nil is an
	// updater that cannot start one, and a decision then waits.
	Resolve func(context.Context, TeamBranchResolutionRequest) (TeamBranchResolutionOutcome, error)
}

// TeamBranchUpdateOutcome is what one call did, as the forge check prints it.
type TeamBranchUpdateOutcome struct {
	TargetBranch   string `json:"target_branch,omitempty"`
	UpstreamBranch string `json:"upstream_branch,omitempty"`
	// TeamCommit is the tip of the team's branch that was read.
	TeamCommit string `json:"team_commit,omitempty"`
	// Current reports the target already carried the team's tip.
	Current bool `json:"current,omitempty"`
	// Landed is the commit the target stands at after an update landed in this
	// call.
	Landed string `json:"landed,omitempty"`
	// Conflict lists the conflicting paths where the merge conflicts.
	Conflict []string `json:"conflict,omitempty"`
	// Waiting is why nothing more happens until a branch moves or the forge
	// answers, in plain words.
	Waiting string `json:"waiting,omitempty"`
	// Busy reports another process holds the updater lease.
	Busy bool `json:"busy,omitempty"`
}

// teamBranchUpdateEntry is the name the update's candidate checkout and
// candidate branch are made under. The queue's naming asks for an entry
// identity, so the update has a stable one of its own, derived from what it is
// for rather than minted, so a later call finds the checkout and branch an
// earlier one left.
func teamBranchUpdateEntry(product domain.ProductID, target, upstream string) string {
	sum := sha256.Sum256([]byte("team-branch-update\x00" + string(product) + "\x00" + target + "\x00" + upstream))
	return "mqe-" + hex.EncodeToString(sum[:16])
}

func (u TeamBranchUpdater) validate() error {
	switch {
	case u.Pipeline == nil:
		return errors.New("bringing the target branch up to date requires a pipeline")
	case u.Pipeline.Worktrees == nil || u.Pipeline.Store == nil || u.Pipeline.Checks == nil || u.Pipeline.Holds == nil:
		return errors.New("bringing the target branch up to date requires the pipeline's worktrees, run store, check runner, and the reader of the operator's pause")
	case u.Git == nil:
		return errors.New("bringing the target branch up to date requires Git")
	case u.Records == nil:
		return errors.New("bringing the target branch up to date requires a record to keep")
	}
	return nil
}

// Update does what one forge check owes the target branch: nothing where no
// team's branch is configured or the target already carries its tip, and
// otherwise the next step of bringing it up to date, which is at most one
// update landed.
func (u TeamBranchUpdater) Update(ctx context.Context) (TeamBranchUpdateOutcome, error) {
	configured := strings.TrimSpace(configuredUpstream(u.Pipeline))
	if configured == "" {
		return TeamBranchUpdateOutcome{}, nil
	}
	if err := u.validate(); err != nil {
		return TeamBranchUpdateOutcome{}, err
	}
	p := u.Pipeline
	// The target is the branch work lands on, which is the one the primary
	// checkout is on, as it is for a run and for the waits that name it.
	target, err := p.Worktrees.CurrentBranch(ctx)
	if err != nil {
		return TeamBranchUpdateOutcome{}, fmt.Errorf("resolve the branch work lands on, to bring it up to date with %s: %w", configured, err)
	}
	upstream := upstreamBranchFor(configured, target)
	if upstream == "" {
		return TeamBranchUpdateOutcome{}, nil
	}
	outcome := TeamBranchUpdateOutcome{TargetBranch: target, UpstreamBranch: upstream}
	lease, held, err := u.Records.LeaseUpdater(ctx, target)
	if err != nil {
		return outcome, err
	}
	if !held {
		outcome.Busy = true
		outcome.Waiting = fmt.Sprintf("another process is bringing %s up to date with %s now; this check leaves it to that one", target, upstream)
		return outcome, nil
	}
	defer func() { _ = lease.Release() }()
	run := &teamBranchRun{
		u: u, p: p, target: target, upstream: upstream, out: outcome,
		entry: teamBranchUpdateEntry(p.Config.Product.ID, target, upstream),
	}
	run.branch = runstate.MergeQueueCandidateBranch(run.entry)
	err = run.update(ctx)
	return run.out, err
}

func configuredUpstream(p *Pipeline) string {
	if p == nil {
		return ""
	}
	return p.Config.Execution.UpstreamBranch
}

// teamBranchRun is one call's working state.
type teamBranchRun struct {
	u        TeamBranchUpdater
	p        *Pipeline
	target   string
	upstream string
	entry    string
	// branch is the branch the merge is published on for a pull request.
	branch string
	record runstate.TeamBranchUpdate
	// publishing is whether the project publishes, which decides how the merge
	// lands and which copy of the target is read for what it carries.
	publishing bool
	// leased is whether this call holds the target's promotion lease, which a
	// catch-up made inside a landing must not take a second time.
	leased bool
	// prior is the update this call replaced because the target moved while the
	// team's tip stayed, kept so that a conflict or refusal it already told the
	// development manager of is not told again when the new merge meets the same.
	prior *runstate.TeamBranchUpdateAttempt
	// resolution is the decision to resolve a conflict, and the rounds spent on
	// it, that the update this call replaced carried, for the same reason: a
	// target that moved does not reset the limit or lose the decision. It is set
	// only where the team's tip stayed, and a new tip starts afresh.
	resolution *runstate.TeamBranchResolution
	out        TeamBranchUpdateOutcome
}

func (r *teamBranchRun) now() time.Time { return r.p.clock().Now() }

func (r *teamBranchRun) save() error { return r.u.Records.Save(r.record) }

func (r *teamBranchRun) pending() *runstate.TeamBranchUpdateAttempt { return r.record.Pending }

func (r *teamBranchRun) wait(format string, args ...any) {
	r.out.Waiting = fmt.Sprintf(format, args...)
}

func (r *teamBranchRun) update(ctx context.Context) error {
	p := r.p
	record, found, err := r.u.Records.Load(r.target)
	if err != nil {
		return err
	}
	if !found {
		record = runstate.TeamBranchUpdate{TargetBranch: r.target, UpstreamBranch: r.upstream}
	}
	record.UpstreamBranch = r.upstream
	r.record = record

	hold, held, err := p.operatorHold()
	if err != nil {
		return err
	}
	if held {
		r.wait("the operator has paused harness activity since %s, so %s is not brought up to date until the pause is lifted", hold.HeldAt.Local().Format("2006-01-02 15:04 MST"), r.target)
		return nil
	}

	reading, err := r.u.Git.RemoteTargetCarries(ctx, r.upstream)
	if err != nil {
		return fmt.Errorf("read the team's branch %s: %w", r.upstream, err)
	}
	if !reading.Exists {
		return fmt.Errorf("the team's branch %s is not on the remote, so %s cannot be brought up to date with it", r.upstream, r.target)
	}
	tip := reading.Tip
	r.out.TeamCommit = tip
	checked := r.now()
	r.record.CheckedAt = &checked

	r.publishing, _, err = p.resolvePublishing(ctx)
	if err != nil {
		return fmt.Errorf("resolve whether the project publishes: %w", err)
	}

	// An update whose pull request is open or merged is followed before anything
	// is read about what the target carries, because a merged one carries the
	// team's tip and still has to be recorded as landed.
	if pending := r.pending(); pending != nil && pending.PullRequest != 0 {
		return r.followPullRequest(ctx)
	}

	carried, err := r.carries(ctx, tip)
	if err != nil {
		return err
	}
	if carried {
		return r.current(tip)
	}
	if r.publishing {
		held, err := r.catchUp(ctx)
		if err != nil {
			return err
		}
		if held != "" {
			r.wait("the local %s cannot be brought onto the remote's, so no merge is built on it: %s", r.target, held)
			return r.save()
		}
	}
	base, err := r.u.Git.TargetCommit(ctx, r.target)
	if err != nil {
		return fmt.Errorf("read where %s stands: %w", r.target, err)
	}
	if pending := r.pending(); pending != nil && (pending.TeamCommit != tip || pending.TargetBase != base) {
		// Either branch moved, so what was built, checked or waiting is about a
		// merge nobody would land now. Its checkout is replaced by the next one.
		if pending.TeamCommit == tip && pending.ReportedAt != nil {
			r.prior = pending
		}
		if pending.TeamCommit == tip && pending.Resolution != nil {
			r.resolution = r.carriedResolution(*pending.Resolution)
		}
		r.record.Pending = nil
	}
	if r.pending() == nil {
		r.record.Pending = &runstate.TeamBranchUpdateAttempt{TeamCommit: tip, TargetBase: base, StartedAt: r.now()}
	}
	if err := r.save(); err != nil {
		return err
	}
	if pending := r.pending(); pending.Conflict != nil {
		return r.conflictWaiting(ctx)
	} else if pending.Refused != "" {
		return r.stillWaiting()
	}
	return r.work(ctx)
}

// carriedResolution is a decision and its rounds as they carry over to the merge
// built again on a target that moved. The rounds stay counted. A round still
// running is ended, because its change was made on the target as it stood, and
// one that resolved the conflict is no longer a resolution of the merge now
// built.
func (r *teamBranchRun) carriedResolution(resolution runstate.TeamBranchResolution) *runstate.TeamBranchResolution {
	carried := resolution
	carried.Rounds = append([]runstate.TeamBranchResolutionRound(nil), resolution.Rounds...)
	for i := range carried.Rounds {
		round := &carried.Rounds[i]
		switch {
		case round.EndedAt == nil:
			ended := r.now()
			round.EndedAt, round.Why = &ended, fmt.Sprintf("%s moved while this round was running, so what it makes is not used", r.target)
		case round.Resolved != "":
			round.Why, round.Resolved = fmt.Sprintf("resolved the conflict at %s, but %s moved before the merge landed, so the merge was built again", shortCommit(round.Resolved), r.target), ""
		}
	}
	return &carried
}

// carries reports whether the target already holds the team's tip. A project
// that publishes keeps its target on the remote, so that is the one asked; one
// that does not lands on its local target alone, so that is.
func (r *teamBranchRun) carries(ctx context.Context, tip string) (bool, error) {
	if !r.publishing {
		carried, err := r.u.Git.TargetHolds(ctx, r.target, tip)
		if err != nil {
			return false, fmt.Errorf("read whether %s holds %s: %w", r.target, r.upstream, err)
		}
		return carried, nil
	}
	reading, err := r.u.Git.RemoteTargetCarries(ctx, r.target, tip)
	if err != nil {
		return false, fmt.Errorf("read whether %s on the remote carries %s: %w", r.target, r.upstream, err)
	}
	if !reading.Exists {
		return false, fmt.Errorf("%s is not on the remote, so it cannot be brought up to date with %s there", r.target, r.upstream)
	}
	return reading.Found != "", nil
}

// current records the target carrying the team's tip, and clears any update
// that was waiting to bring it there, since there is nothing left for it to do.
func (r *teamBranchRun) current(tip string) error {
	at := r.now()
	r.record.Pending = nil
	r.record.CurrentAt, r.record.CurrentWith = &at, tip
	r.out.Current = true
	return r.save()
}

// catchUp brings the local target onto the remote's under the promotion lease,
// which is what a move of the local target is always made under. It returns why
// the local target was left where it is, empty where it is on the remote's.
func (r *teamBranchRun) catchUp(ctx context.Context) (string, error) {
	if !r.leased {
		lease, err := r.p.Store.LeasePromotion(ctx, r.target)
		if err != nil {
			return "", fmt.Errorf("wait for the turn to promote into %s: %w", r.target, err)
		}
		defer func() { _ = lease.Release() }()
	}
	catchup, err := r.u.Git.CatchUpTarget(ctx, r.target)
	if err != nil {
		return "", fmt.Errorf("bring the local %s onto the remote's: %w", r.target, err)
	}
	return catchup.Held, nil
}

// stillWaiting says why an update already refused waits, and tells the
// development manager once if that has not happened yet.
func (r *teamBranchRun) stillWaiting() error {
	pending := r.pending()
	r.wait("%s; it is tried again when either branch moves", pending.Waiting())
	if pending.ReportedAt != nil {
		return nil
	}
	return r.reportRefusal(pending.Refused)
}

// work takes the pending update from wherever it stands to landed.
func (r *teamBranchRun) work(ctx context.Context) error {
	pending := r.pending()
	base, tip := pending.TargetBase, pending.TeamCommit
	path, ready, err := r.checkout(ctx)
	if err != nil || !ready {
		return err
	}
	// The checkout is only for the checks and the review, so it goes whatever
	// becomes of them; the commit is kept, and a later call cuts the checkout
	// from it again.
	defer func() { _ = r.u.Git.RemoveQueueCandidate(ctx, path) }()

	if pending.ChecksFinishedAt == nil {
		refusal, err := r.runChecks(ctx, path)
		if err != nil {
			return err
		}
		if refusal != "" {
			return r.refuse(refusal)
		}
	}
	if !teamBranchChecksPassed(*pending, len(r.p.Config.Checks)) {
		return r.refuse(teamBranchChecksFailure(*pending))
	}
	if pending.Review == nil {
		approved, summary, err := r.review(ctx, path)
		if err != nil {
			return err
		}
		if !approved {
			return r.refuse("the independent reviewer did not approve the merge: " + oneline.Fold(summary, 1500))
		}
	}
	if !pending.Approved() {
		return r.refuse("the independent reviewer did not approve the merge: " + oneline.Fold(pending.Review.Summary, 1500))
	}
	// What lands is the merge the harness built and nothing else. The protected-path
	// check that stops a run editing paths it may not is not asked of the paths
	// the team's branch brings in: they are the team's own merged history, and
	// the update writes no line of its own. What is checked instead is that the
	// commit is exactly that merge, with the target it was built on as its first
	// parent and the team's tip as its second, now and again under the lease
	// before the target moves.
	if err := r.exactMerge(ctx, pending.Candidate, base, tip); err != nil {
		return err
	}
	if r.publishing {
		return r.landThroughPullRequest(ctx)
	}
	return r.landLocally(ctx)
}

// checkout puts the merge's checkout in place, building the merge where none was
// built before. It reports false where the update was decided without one: the
// target already holds the team's tip, the merge conflicts, or the target moved
// while it was built.
func (r *teamBranchRun) checkout(ctx context.Context) (string, bool, error) {
	pending := r.pending()
	base, tip := pending.TargetBase, pending.TeamCommit
	if pending.Candidate != "" {
		held, err := r.u.Git.HoldsCommit(ctx, pending.Candidate)
		if err != nil {
			return "", false, fmt.Errorf("read whether the merge %s is still kept: %w", pending.Candidate, err)
		}
		if held {
			path, err := r.u.Git.RestoreQueueCandidate(ctx, r.entry, pending.Candidate)
			if err != nil {
				return "", false, fmt.Errorf("put the checkout of merge %s back: %w", pending.Candidate, err)
			}
			return path, true, nil
		}
		// A merge that is gone cannot be checked again, and nothing recorded
		// about it says anything about the one that replaces it.
		pending.Candidate, pending.Checks, pending.ChecksFinishedAt, pending.Review = "", nil, nil, nil
		// A merge made from a resolution is made again from the resolution's
		// approved commit where that is still kept; building it from the branches
		// would only meet the conflict again.
		if round, resolved := pending.Resolution.ResolvedBy(); resolved {
			if kept, err := r.u.Git.HoldsCommit(ctx, round.Resolved); err == nil && kept {
				if err := r.resolvedMerge(ctx, round.Resolved); err != nil {
					return "", false, err
				}
				path, err := r.u.Git.RestoreQueueCandidate(ctx, r.entry, pending.Candidate)
				if err != nil {
					return "", false, fmt.Errorf("put the checkout of merge %s back: %w", pending.Candidate, err)
				}
				return path, true, nil
			}
		}
	}
	candidate, err := r.u.Git.BuildQueueCandidate(ctx, gitworktree.QueueCandidateRequest{
		Entry:        r.entry,
		TargetBranch: r.target,
		Heads:        []string{tip},
		Message: fmt.Sprintf("yoyodyne: bring %s into %s\n\nMerges %s at %s into %s at %s, so %s carries the team's merged work.\n",
			r.upstream, r.target, r.upstream, tip, r.target, base, r.target),
	})
	var conflict *gitworktree.QueueCandidateConflict
	switch {
	case errors.Is(err, gitworktree.ErrQueueCandidateEmpty):
		return "", false, r.current(tip)
	case errors.As(err, &conflict):
		return "", false, r.conflicted(ctx, base, *conflict)
	case err != nil:
		return "", false, fmt.Errorf("build the merge of %s into %s: %w", r.upstream, r.target, err)
	}
	if candidate.BaseCommit != base {
		_ = r.u.Git.RemoveQueueCandidate(ctx, candidate.Path)
		r.wait("%s moved to %s while the merge was being built on %s; it is built again at the next check", r.target, shortCommit(candidate.BaseCommit), shortCommit(base))
		return "", false, nil
	}
	if err := r.exactMerge(ctx, candidate.Commit, base, tip); err != nil {
		_ = r.u.Git.RemoveQueueCandidate(ctx, candidate.Path)
		return "", false, err
	}
	pending.Candidate = candidate.Commit
	if err := r.save(); err != nil {
		_ = r.u.Git.RemoveQueueCandidate(ctx, candidate.Path)
		return "", false, err
	}
	return candidate.Path, true, nil
}

// exactMerge checks the commit is the merge the harness built: two parents, the
// target it was built on first and the team's tip second.
func (r *teamBranchRun) exactMerge(ctx context.Context, candidate, base, tip string) error {
	parents, err := r.u.Git.CommitParents(ctx, candidate)
	if err != nil {
		return fmt.Errorf("read the parents of the merge %s: %w", candidate, err)
	}
	if len(parents) != 2 || parents[0] != base || parents[1] != tip {
		return fmt.Errorf("commit %s is not the merge of %s at %s into %s at %s (its parents are %s), so it is not landed",
			candidate, r.upstream, tip, r.target, base, strings.Join(parents, ", "))
	}
	return nil
}

// conflicted records a merge that will not go in, names what it conflicts with,
// and tells the development manager. A conflict is not a failure: the update
// waits and the call succeeds.
func (r *teamBranchRun) conflicted(ctx context.Context, base string, conflict gitworktree.QueueCandidateConflict) error {
	pending := r.pending()
	paths := append([]string(nil), conflict.Paths...)
	sort.Strings(paths)
	items, itemsErr := r.conflictItems(ctx, base, paths)
	pending.Conflict = &runstate.TeamBranchUpdateConflict{Paths: paths, Items: items, FoundAt: r.now()}
	if r.resolution != nil && pending.Resolution == nil {
		pending.Resolution = r.resolution
	}
	if err := r.save(); err != nil {
		return err
	}
	r.out.Conflict = paths
	r.wait("%s; it is tried again when either branch moves", pending.Waiting())
	if prior := r.prior; prior != nil && prior.Conflict != nil && slices.Equal(prior.Conflict.Paths, paths) {
		return r.alreadyTold(prior)
	}
	return r.reportConflictWith(*pending.Conflict, itemsErr)
}

// conflictItems is the work items on the target whose landed changes touched
// any of the paths: the runs waiting for the team's merge whose landing commit
// is among the commits on the target that changed one of them.
func (r *teamBranchRun) conflictItems(ctx context.Context, base string, paths []string) ([]runstate.TeamBranchConflictItem, error) {
	waits, ok := r.p.Store.(TeamMergeWaits)
	if !ok {
		return nil, errors.New("the run store cannot list the runs waiting for the team's merge")
	}
	waiting, err := waits.WaitingForTeamMerge()
	if err != nil {
		return nil, fmt.Errorf("list the runs waiting for the team's merge: %w", err)
	}
	touching := map[string]bool{}
	for _, path := range paths {
		commits, err := r.u.Git.CommitsChanging(ctx, base, path, teamBranchItemSearchDepth)
		if err != nil {
			return nil, fmt.Errorf("read what changed %s on %s: %w", path, r.target, err)
		}
		for _, commit := range commits {
			touching[commit] = true
		}
	}
	found := map[string]string{}
	for _, state := range waiting {
		if state.TeamMerge.TargetBranch != r.target {
			continue
		}
		landed := []string{state.TeamMerge.LandingCommit}
		if state.Integration != nil {
			landed = append(landed, state.Integration.SourceCommit)
		}
		for _, commit := range landed {
			if commit != "" && touching[commit] {
				found[state.WorkItemID] = state.WorkItemTitle
			}
		}
	}
	items := make([]runstate.TeamBranchConflictItem, 0, len(found))
	for id, title := range found {
		items = append(items, runstate.TeamBranchConflictItem{ID: id, Title: r.currentTitle(ctx, id, title)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// currentTitle is the title the tracker holds for an item now, and the run's own
// where the tracker cannot say.
func (r *teamBranchRun) currentTitle(ctx context.Context, id, fallback string) string {
	if r.p.Tracker == nil {
		return fallback
	}
	bounded, cancel := trackerRecordContext(ctx)
	defer cancel()
	shown, err := r.p.Tracker.Show(bounded, id)
	if err != nil || shown.ID != id || strings.TrimSpace(shown.Title) == "" {
		return fallback
	}
	return shown.Title
}

// runChecks runs the project's configured checks on the merge, after its
// install steps, and records each result. It returns a refusal where the merge
// cannot be checked, and an error where the checks could not be run at all so
// the next call runs them again; nothing is recorded for those.
func (r *teamBranchRun) runChecks(ctx context.Context, path string) (string, error) {
	p := r.p
	pending := r.pending()
	stream := fmt.Sprintf("%s-update-%s", r.entry, shortCommit(pending.Candidate))
	sink := r.u.Events
	if sink == nil {
		sink = func(execution.Event) error { return nil }
	}
	commands := append([]string(nil), p.Config.Checks...)
	if refusal, err := r.install(ctx, path, stream, sink); err != nil || refusal != "" {
		return refusal, err
	}
	var results []checks.Result
	if len(commands) > 0 {
		var runErr error
		results, _, runErr = p.Checks.Run(ctx, checks.Request{
			RunID:     stream,
			Directory: path,
			Commands:  commands,
			// The merge is what would land, and nothing narrows what a landing
			// has to answer for.
			Narrowing: &checks.Narrowing{Whole: true, Reason: "a merge bringing in the team's branch is checked whole"},
			// A merge is checked for a landing, so its heavy checks are let start
			// ahead of every run's checks (heavychecks.go).
			Permit: checks.PermitRequest{Priority: domain.HeavyCheckForLanding},
		}, sink)
		if runErr != nil {
			return "", fmt.Errorf("run the checks on the merge %s: %w", pending.Candidate, runErr)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	recorded := make([]runstate.TeamBranchUpdateCheck, 0, len(results))
	for _, result := range results {
		check := runstate.TeamBranchUpdateCheck{
			Command:     result.Command,
			Passed:      result.Passed,
			ExitCode:    result.Process.ExitCode,
			Status:      string(result.Process.Status),
			CouldNotRun: boundedEvidence(result.CouldNotRun),
		}
		// A check stopped on time or cancelled judged nothing about the merge,
		// which is different from a check that failed it.
		switch result.Process.Status {
		case execution.ProcessTimedOut, execution.ProcessCancelled, execution.ProcessStalled:
			if check.CouldNotRun == "" {
				check.CouldNotRun = fmt.Sprintf("%s was %s and judged nothing", result.Command, result.Process.Status)
			}
		}
		recorded = append(recorded, check)
	}
	finished := r.now()
	pending.Checks, pending.ChecksFinishedAt = recorded, &finished
	return "", r.save()
}

// install runs each declared install step that is due in the merge's checkout,
// as the merge queue's checks do. A step that does not pass means the checks
// never ran, which is said as a refusal.
func (r *teamBranchRun) install(ctx context.Context, path, stream string, sink func(execution.Event) error) (string, error) {
	p := r.p
	if len(p.Config.Install) == 0 {
		return "", nil
	}
	var installed []runstate.InstallRun
	cache := ""
	for _, step := range p.Config.Install {
		digest, err := installDigest(path, step.Inputs)
		if err != nil {
			return fmt.Sprintf("the inputs of install step %q could not be read: %v", step.Name, err), nil
		}
		due, err := installDue(installed, step, path, digest)
		if err != nil {
			return fmt.Sprintf("the outputs of install step %q could not be looked for: %v", step.Name, err), nil
		}
		if !due {
			continue
		}
		if cache == "" {
			if cache, err = p.installCache(); err != nil {
				return err.Error(), nil
			}
		}
		record, _, runErr := runInstallStep(ctx, *p, step, digest, checks.InstallRequest{
			RunID:     stream,
			Directory: path,
			Cache:     cache,
			Permit:    checks.PermitRequest{Priority: domain.HeavyCheckForLanding},
		}, sink)
		if runErr != nil && ctx.Err() != nil {
			return "", runErr
		}
		if record.Result != runstate.InstallPassed {
			return boundedEvidence(installFailure{record: record, before: "before the checks of the merge"}.Error()), nil
		}
		record.Output = ""
		installed = keptInstall(installed, record)
	}
	return "", nil
}

// teamBranchChecksPassed reports the checks finished, every configured one ran,
// and every one passed. A check that could not run is not a pass.
func teamBranchChecksPassed(attempt runstate.TeamBranchUpdateAttempt, configured int) bool {
	// A merge no configured check ran on has earned nothing, whatever the
	// configuration says, so a project with none never lands an update
	// (docs/decisions/invariants/integration-requires-revision-bound-evidence.md).
	if configured == 0 || attempt.ChecksFinishedAt == nil || len(attempt.Checks) != configured {
		return false
	}
	return attempt.ChecksPassed()
}

func teamBranchChecksFailure(attempt runstate.TeamBranchUpdateAttempt) string {
	var failed, unrun []string
	for _, check := range attempt.Checks {
		switch {
		case check.CouldNotRun != "":
			unrun = append(unrun, check.Command)
		case !check.Passed:
			failed = append(failed, check.Command)
		}
	}
	var parts []string
	if len(failed) > 0 {
		parts = append(parts, "the configured checks failed on the merge: "+strings.Join(failed, ", "))
	}
	if len(unrun) > 0 {
		parts = append(parts, "the configured checks could not be run on the merge: "+strings.Join(unrun, ", "))
	}
	if len(parts) == 0 {
		if len(attempt.Checks) == 0 {
			return "no checks are configured, so the merge cannot earn the checks every landing needs"
		}
		return "the configured checks did not all run on the merge"
	}
	return strings.Join(parts, "; ")
}

// review asks the independent reviewer about the merge and records what it
// decided. A review that could not be made is an error and records nothing, so
// the next call asks again.
func (r *teamBranchRun) review(ctx context.Context, path string) (bool, string, error) {
	pending := r.pending()
	base, candidate := pending.TargetBase, pending.Candidate
	change, err := r.u.Git.CandidateChanges(ctx, base, candidate, gitworktree.DiffLimits{})
	if err != nil {
		return false, "", fmt.Errorf("describe the merge %s: %w", candidate, err)
	}
	shown := make([]checks.Result, 0, len(pending.Checks))
	for _, check := range pending.Checks {
		shown = append(shown, checks.Result{
			Command:     check.Command,
			Passed:      check.Passed,
			CouldNotRun: check.CouldNotRun,
			Process:     execution.ProcessResult{ExitCode: check.ExitCode, Status: execution.ProcessStatus(check.Status)},
		})
	}
	outcome, err := r.u.Reviewer.ReviewCandidate(ctx, CandidateReviewRequest{
		Name:         fmt.Sprintf("%s-into-%s", r.upstream, r.target),
		BaseCommit:   base,
		HeadCommit:   candidate,
		Change:       change,
		Context:      r.reviewContext(*pending),
		Checks:       shown,
		WorktreePath: path,
	})
	if err != nil {
		return false, "", fmt.Errorf("review the merge %s: %w", candidate, err)
	}
	if !outcome.Decided() {
		return false, "", fmt.Errorf("the independent review of the merge %s produced no decision", candidate)
	}
	pending.Review = &runstate.TeamBranchUpdateReview{
		SessionID:  outcome.SessionID,
		Model:      outcome.Model,
		Decision:   string(outcome.Decision),
		Summary:    boundedEvidence(outcome.Summary),
		FinishedAt: r.now(),
	}
	if err := r.save(); err != nil {
		return false, "", err
	}
	return outcome.Approved(), outcome.Summary, nil
}

// reviewContext is what the harness tells the reviewer about the merge, and
// nothing an agent wrote.
func (r *teamBranchRun) reviewContext(attempt runstate.TeamBranchUpdateAttempt) string {
	own := "The harness wrote no line of its own; every line in it was already on one of the two branches."
	if round, resolved := attempt.Resolution.ResolvedBy(); resolved {
		own = fmt.Sprintf("Merging the two conflicted in %s. Run %s resolved those conflicts for %s, as the development manager decided; that run's change was checked and independently reviewed before this commit was built from it. The harness took exactly the content of that run's approved commit `%s` and wrote nothing else, so judge in particular whether the resolution keeps both the team's change and the target's in those files.",
			strings.Join(attempt.Resolution.Paths, ", "), round.RunID, attempt.Resolution.Says(), round.Resolved)
	}
	lines := []string{
		fmt.Sprintf("# Bringing %s into %s", r.upstream, r.target),
		"",
		fmt.Sprintf("This change brings the team's merged work from `%s` into `%s`. The harness built it as commit `%s`: `%s` at `%s`, the team's branch tip, merged onto `%s` at `%s`. %s",
			r.upstream, r.target, attempt.Candidate, r.upstream, attempt.TeamCommit, r.target, attempt.TargetBase, own),
		"",
		"What you are asked is whether the merge is faithful: that it carries what the team's branch gained and what the target already held, and loses neither. Look in particular for a merge that has no textual conflict and still breaks behaviour, such as two changes that each call or rename the same thing in a way the other does not expect. The configured checks have already run on exactly this commit, and their results are below; judge whether they pass.",
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// refuse records a merge that does not land as it stands, tells the development
// manager, and leaves the update waiting for a branch to move.
func (r *teamBranchRun) refuse(reason string) error {
	pending := r.pending()
	at := r.now()
	pending.Refused, pending.RefusedAt = reason, &at
	if err := r.save(); err != nil {
		return err
	}
	r.wait("%s; it is tried again when either branch moves", strings.TrimRight(strings.TrimSpace(reason), "."))
	if prior := r.prior; prior != nil && prior.Refused == reason {
		return r.alreadyTold(prior)
	}
	return r.reportRefusal(reason)
}

// landLocally moves the local target onto the merge, under the promotion lease
// and as a compare and swap on the target the merge was built on.
func (r *teamBranchRun) landLocally(ctx context.Context) error {
	pending := r.pending()
	base, tip, candidate := pending.TargetBase, pending.TeamCommit, pending.Candidate
	if !r.p.automatic() {
		// Integration is a person's to approve, and a local landing has no pull
		// request to carry that approval, so the harness does not move the target
		// itself. The checked and reviewed merge is kept for whoever lands it.
		r.wait("integration needs a person's approval, so the merge %s, checked and reviewed, is not landed on %s by the harness; a person can merge %s into %s, after which the next check finds %s current",
			shortCommit(candidate), r.target, r.upstream, r.target, r.target)
		return nil
	}
	lease, err := r.p.Store.LeasePromotion(ctx, r.target)
	if err != nil {
		return fmt.Errorf("wait for the turn to promote into %s: %w", r.target, err)
	}
	defer func() { _ = lease.Release() }()
	if r.paused() {
		return nil
	}
	current, err := r.u.Git.TargetCommit(ctx, r.target)
	if err != nil {
		return fmt.Errorf("read where %s stands: %w", r.target, err)
	}
	if current != base {
		return r.moved(fmt.Sprintf("%s moved to %s while the merge was checked and reviewed", r.target, shortCommit(current)))
	}
	if err := r.exactMerge(ctx, candidate, base, tip); err != nil {
		return err
	}
	err = r.u.Git.PromoteQueueCandidate(ctx, r.target, base, candidate)
	if errors.Is(err, gitworktree.ErrTargetDrift) || errors.Is(err, gitworktree.ErrNotFastForward) {
		return r.moved(fmt.Sprintf("%s moved while the merge was being landed", r.target))
	}
	if err != nil {
		return fmt.Errorf("move %s onto the merge: %w", r.target, err)
	}
	return r.landed(candidate)
}

// paused says the operator's pause stopped a landing that was about to start.
func (r *teamBranchRun) paused() bool {
	hold, held, err := r.p.operatorHold()
	if err != nil {
		r.wait("whether the operator has paused harness activity could not be read, so the landing waits: %v", err)
		return true
	}
	if held {
		r.wait("the operator has paused harness activity since %s, so the merge is not landed until the pause is lifted", hold.HeldAt.Local().Format("2006-01-02 15:04 MST"))
		return true
	}
	return false
}

// moved drops an update whose target moved, charging it to nothing: the next
// check builds the merge again on the target as it stands.
func (r *teamBranchRun) moved(why string) error {
	r.record.Pending = nil
	r.wait("%s, so the merge is built again at the next check", why)
	return r.save()
}

// landed records the update landed, with the target standing at commit.
func (r *teamBranchRun) landed(commit string) error {
	at := r.now()
	tip := r.pending().TeamCommit
	r.record.Land(at, commit)
	r.record.CurrentAt, r.record.CurrentWith = &at, tip
	r.out.Landed = commit
	return r.save()
}

// landThroughPullRequest publishes the merge on its branch and opens a pull
// request into the target. Where integration needs a person the request waits
// for them; otherwise the forge is asked to merge it.
func (r *teamBranchRun) landThroughPullRequest(ctx context.Context) error {
	p := r.p
	pending := r.pending()
	published, exists, err := r.u.Git.QueueCandidateBranchCommit(ctx, r.branch)
	if err != nil {
		return fmt.Errorf("read where %s stands: %w", r.branch, err)
	}
	expected := ""
	if exists {
		expected = published
	}
	if err := r.u.Git.PublishQueueCandidate(ctx, r.branch, pending.Candidate, expected); err != nil {
		return fmt.Errorf("publish the merge on %s: %w", r.branch, err)
	}
	opener := p.factoryLine(ctx)
	if opener != "" {
		opener = "\n" + opener + "\n"
	}
	opened, err := p.Publisher.Ensure(ctx, publish.Request{
		Head:  r.branch,
		Base:  r.target,
		Title: fmt.Sprintf("Bring %s into %s", r.upstream, r.target),
		Body:  r.pullRequestBody(*pending) + opener,
		Mark:  forgeMark(ctx, p.Factory, p.Config.Product.ID),
	})
	if err != nil {
		return fmt.Errorf("open the pull request that carries %s: %w", r.branch, err)
	}
	pending.PullRequest, pending.PullRequestURL = opened.Number, opened.URL
	if err := r.save(); err != nil {
		return err
	}
	return r.askForMerge(ctx)
}

func (r *teamBranchRun) pullRequestBody(attempt runstate.TeamBranchUpdateAttempt) string {
	commands := make([]string, 0, len(attempt.Checks))
	for _, check := range attempt.Checks {
		commands = append(commands, check.Command)
	}
	checked := "no checks are configured"
	if len(commands) > 0 {
		checked = "passed " + strings.Join(commands, ", ")
	}
	return fmt.Sprintf("Brings the team's merged work into %s: %s at %s merged onto %s at %s, as commit %s. The harness built the merge and wrote no line of its own.\n\nThe configured checks %s on this commit, and an independent reviewer approved it.\n",
		r.target, r.upstream, attempt.TeamCommit, r.target, attempt.TargetBase, attempt.Candidate, checked)
}

// askForMerge asks the forge to merge the open pull request, under the
// promotion lease and once the remote target is read to be still where the
// merge was built. A merge the forge would reconcile itself — resolving
// something nothing here checked or reviewed — is never asked for.
func (r *teamBranchRun) askForMerge(ctx context.Context) error {
	p := r.p
	pending := r.pending()
	if !p.automatic() {
		r.wait("pull request #%d waits for a person to merge it", pending.PullRequest)
		return nil
	}
	lease, err := p.Store.LeasePromotion(ctx, r.target)
	if err != nil {
		return fmt.Errorf("wait for the turn to promote into %s: %w", r.target, err)
	}
	r.leased = true
	defer func() { r.leased = false; _ = lease.Release() }()
	if r.paused() {
		return nil
	}
	err = r.u.Git.VerifyRemoteTarget(ctx, gitworktree.Integration{
		TargetBranch: r.target, TargetCommit: pending.TargetBase, PreviousTargetCommit: pending.TargetBase,
	})
	if errors.Is(err, gitworktree.ErrRemoteTargetDrift) {
		return r.moved(fmt.Sprintf("%s on the remote moved off the commit the merge was built on", r.target))
	}
	if err != nil {
		return fmt.Errorf("check the remote %s before asking for the merge: %w", r.target, err)
	}
	// The request is written down before it is made, so a call that stops
	// between the two knows the forge may have been asked.
	asked := r.now()
	pending.MergeRequestedAt = &asked
	if err := r.save(); err != nil {
		return err
	}
	result, err := p.Publisher.Merge(ctx, publish.MergeRequest{Number: pending.PullRequest, HeadCommit: pending.Candidate, Method: mergeMethod})
	var refused publish.MergeRefused
	var unavailable publish.AutoMergeUnavailable
	if errors.As(err, &refused) || errors.As(err, &unavailable) {
		pending.MergeRequestedAt = nil
		if saveErr := r.save(); saveErr != nil {
			return saveErr
		}
		r.wait("the forge did not merge pull request #%d: %v; it is asked again at the next check", pending.PullRequest, err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("ask the forge to merge pull request #%d: %w", pending.PullRequest, err)
	}
	if result.Queued {
		r.wait("the forge holds the merge of pull request #%d queued until its own requirements are met", pending.PullRequest)
		return nil
	}
	return r.confirmMerge(ctx, "")
}

// followPullRequest reads the pull request an earlier call opened and goes on
// from what the forge says. A pull request closed without merging drops the
// update, so the next call builds the merge again.
func (r *teamBranchRun) followPullRequest(ctx context.Context) error {
	pending := r.pending()
	state, err := r.p.Publisher.State(ctx, r.branch)
	if err != nil {
		return fmt.Errorf("read pull request #%d: %w", pending.PullRequest, err)
	}
	switch {
	case state.Merged:
		return r.confirmMerge(ctx, state.MergeCommit)
	case strings.EqualFold(state.State, "OPEN"):
		if pending.MergeRequestedAt != nil && state.AutoMerge {
			r.wait("the forge holds the merge of pull request #%d queued until its own requirements are met", pending.PullRequest)
			return nil
		}
		if !r.p.automatic() {
			r.wait("pull request #%d waits for a person to merge it", pending.PullRequest)
			return nil
		}
		// Open, and the forge is holding no merge of it: either none was asked
		// for yet or the forge dropped it, and asking again is safe because the
		// request is bound to the commit it was checked as.
		held, err := r.u.Git.HoldsCommit(ctx, pending.Candidate)
		if err != nil {
			return fmt.Errorf("read whether the merge %s is still kept: %w", pending.Candidate, err)
		}
		if !held {
			return r.moved(fmt.Sprintf("the merge %s is no longer kept here", shortCommit(pending.Candidate)))
		}
		pending.MergeRequestedAt = nil
		return r.askForMerge(ctx)
	default:
		r.record.Pending = nil
		r.wait("pull request #%d was closed without being merged, so the merge is built again at the next check", pending.PullRequest)
		return r.save()
	}
}

// confirmMerge establishes on the remote target that the merge itself landed,
// and records the landing with the commit the target stands at.
func (r *teamBranchRun) confirmMerge(ctx context.Context, mergeCommit string) error {
	pending := r.pending()
	remoteMerge, err := r.u.Git.ConfirmRemoteTarget(ctx, gitworktree.Integration{
		TargetBranch: r.target, TargetCommit: pending.Candidate, PreviousTargetCommit: pending.TargetBase,
	}, mergeCommit)
	if errors.Is(err, gitworktree.ErrRemoteTargetMismatch) {
		r.wait("the forge reports pull request #%d merged and the remote %s does not hold the merge %s; this is left for a person: %v",
			pending.PullRequest, r.target, shortCommit(pending.Candidate), err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("confirm the merge reached the remote %s: %w", r.target, err)
	}
	if err := r.landed(nonEmpty(remoteMerge, pending.Candidate)); err != nil {
		return err
	}
	// The local target follows the remote's now that the forge has merged. A
	// catch-up that cannot be made is left for the next check, which makes it
	// before it builds anything.
	if held, err := r.catchUp(ctx); err != nil {
		r.wait("the update landed, and the local %s could not be brought onto the remote's yet: %v", r.target, err)
	} else if held != "" {
		r.wait("the update landed, and the local %s was left where it is: %s", r.target, held)
	}
	return nil
}

// conflictWaiting is an update found to conflict: it tells the development
// manager once, and then does what her decision, if she made one, asks: a round
// of resolving the conflict while the three the conflict is allowed are not all
// used, and waiting once they are.
func (r *teamBranchRun) conflictWaiting(ctx context.Context) error {
	pending := r.pending()
	conflict := pending.Conflict
	r.out.Conflict = append([]string(nil), conflict.Paths...)
	if pending.ReportedAt == nil {
		if err := r.reportConflict(*conflict); err != nil {
			return err
		}
	}
	resolution := pending.Resolution
	switch {
	case resolution == nil:
		r.wait("%s; it waits for the development manager to record a decision to resolve the conflict with one item, and is tried again when either branch moves", pending.Waiting())
		return nil
	case resolution.InProgress() != nil || !resolution.Spent():
		return r.runRound(ctx)
	}
	r.wait("%s; all %d rounds of resolving it are used, so it waits for a branch to move or for a person to merge %s into %s by hand", pending.Waiting(), runstate.MaxTeamBranchResolutionRounds, r.upstream, r.target)
	if resolution.SpentReportedAt == nil {
		return r.reportRoundsSpent()
	}
	return nil
}

// runRound makes the next round of resolving the conflict, or settles the one
// recorded as running, and acts on what it came to. The round is recorded before
// the run starts, so a call that dies in the run leaves it findable.
func (r *teamBranchRun) runRound(ctx context.Context) error {
	pending := r.pending()
	resolution, conflict := pending.Resolution, pending.Conflict
	base, tip := pending.TargetBase, pending.TeamCommit
	if r.u.Resolve == nil {
		r.wait("%s; the decision that %s resolves it stands, and this check cannot start the run", pending.Waiting(), resolution.Says())
		return nil
	}
	index := -1
	for i := range resolution.Rounds {
		if resolution.Rounds[i].EndedAt == nil {
			index = i
		}
	}
	conflicted := ""
	if index >= 0 {
		// A run recorded for this round, started by a call that has ended, keeps the
		// commit it was cut from; one that never started is cut from a new one.
		if state, err := r.p.Store.Load(resolution.Rounds[index].RunID); err == nil && state.TeamBranchResolution != nil {
			conflicted = state.TeamBranchResolution.ConflictedBase
		}
	}
	if conflicted == "" {
		built, err := r.u.Git.BuildConflictedMerge(ctx, gitworktree.ConflictedMergeRequest{
			Entry: r.entry, TargetBranch: r.target, Head: tip, Base: base,
			Message: fmt.Sprintf("yoyodyne: %s at %s merged onto %s at %s, with the conflicts in %s left in place\n\nThis is the commit a run that resolves the conflicts starts from. Its only parent is %s.\n",
				r.upstream, tip, r.target, base, strings.Join(conflict.Paths, ", "), base),
		})
		if err != nil {
			return fmt.Errorf("leave the merge of %s into %s conflicted for the run that resolves it: %w", r.upstream, r.target, err)
		}
		conflicted = built.Commit
	}
	if index < 0 {
		number := len(resolution.Rounds) + 1
		resolution.Paths = append([]string(nil), conflict.Paths...)
		resolution.Rounds = append(resolution.Rounds, runstate.TeamBranchResolutionRound{
			RunID: runstate.TeamBranchResolutionRunID(r.target, tip, base, number, resolution.Restarts), StartedAt: r.now(),
		})
		index = len(resolution.Rounds) - 1
		if err := r.save(); err != nil {
			return err
		}
	}
	var earlier []string
	for _, round := range resolution.Rounds[:index] {
		earlier = append(earlier, nonEmpty(round.Why, "it ended without resolving the conflict"))
	}
	request := TeamBranchResolutionRequest{
		TargetBranch: r.target, UpstreamBranch: r.upstream, TeamCommit: tip, TargetBase: base, ConflictedBase: conflicted,
		Paths: conflict.Paths, WorkItemID: resolution.WorkItemID, WorkItemTitle: resolution.WorkItemTitle,
		Reason: resolution.Reason, DecidedBy: resolution.DecidedBy, Items: conflict.Items, Round: index + 1, Earlier: earlier,
		Attempt: resolution.Restarts,
	}
	outcome, err := r.u.Resolve(ctx, request)
	if err != nil {
		return fmt.Errorf("make round %d of resolving the conflict in %s: %w", index+1, strings.Join(conflict.Paths, ", "), err)
	}
	switch {
	case outcome.NotStarted:
		// Nothing was started, or what was started was ended for a cause outside
		// the work, so the round was not used; the next run of it is a new one.
		resolution.Rounds = resolution.Rounds[:index]
		resolution.Restarts++
		if err := r.save(); err != nil {
			return err
		}
		r.wait("%s; the run of %s that resolves it was not started (%s), and is asked for again at the next check", pending.Waiting(), resolution.Says(), nonEmpty(outcome.Why, "no reason was given"))
		return nil
	case outcome.Running:
		r.wait("%s; round %d of %d, a run of %s, has not ended: %s", pending.Waiting(), index+1, runstate.MaxTeamBranchResolutionRounds, resolution.Says(), nonEmpty(outcome.Why, "it is running"))
		return nil
	case outcome.Resolved:
		return r.resolved(ctx, index, outcome.ApprovedHead)
	}
	ended := r.now()
	resolution.Rounds[index].EndedAt, resolution.Rounds[index].Why = &ended, nonEmpty(outcome.Why, "the run ended without resolving the conflict")
	if err := r.save(); err != nil {
		return err
	}
	if resolution.Spent() {
		r.wait("%s; round %d of %d did not resolve it and all rounds are used, so it waits for a branch to move or for a person to merge %s into %s by hand", pending.Waiting(), index+1, runstate.MaxTeamBranchResolutionRounds, r.upstream, r.target)
		return r.reportRoundsSpent()
	}
	r.wait("%s; round %d of %d did not resolve it (%s); the decision that %s resolves it stands, so the next check starts round %d",
		pending.Waiting(), index+1, runstate.MaxTeamBranchResolutionRounds, oneline.Fold(strings.TrimRight(outcome.Why, "."), 400), resolution.Says(), index+2)
	return nil
}

// resolved takes a run's approved head to a merge commit, which then goes the
// way every update goes: its checks, the independent review of that exact
// commit, and landing.
func (r *teamBranchRun) resolved(ctx context.Context, index int, head string) error {
	pending := r.pending()
	resolution := pending.Resolution
	if err := r.resolvedMerge(ctx, head); err != nil {
		return err
	}
	ended := r.now()
	resolution.Rounds[index].EndedAt, resolution.Rounds[index].Resolved = &ended, head
	pending.Conflict = nil
	if err := r.save(); err != nil {
		return err
	}
	r.out.Conflict, r.out.Waiting = nil, ""
	return r.work(ctx)
}

// resolvedMerge makes the merge commit of a resolution: the content of the run's
// approved commit, with the target it was built on as its first parent and the
// team's tip as its second, which is the shape every update's merge has.
func (r *teamBranchRun) resolvedMerge(ctx context.Context, head string) error {
	pending := r.pending()
	base, tip := pending.TargetBase, pending.TeamCommit
	message := fmt.Sprintf("yoyodyne: bring %s into %s\n\nMerges %s at %s into %s at %s. The merge conflicted in %s; the content is that of %s, which a run resolved the conflicts in and which was checked and reviewed independently.\n",
		r.upstream, r.target, r.upstream, tip, r.target, base, strings.Join(pending.Resolution.Paths, ", "), head)
	merge, err := r.u.Git.CommitMergeOf(ctx, head, base, tip, message)
	if err != nil {
		return fmt.Errorf("make the merge of %s into %s from the resolution %s: %w", r.upstream, r.target, head, err)
	}
	if err := r.exactMerge(ctx, merge, base, tip); err != nil {
		return err
	}
	pending.Candidate, pending.Checks, pending.ChecksFinishedAt, pending.Review = merge, nil, nil, nil
	return r.save()
}

// reportRoundsSpent tells the development manager, once, that every round of
// resolving the conflict was used and what each came to.
func (r *teamBranchRun) reportRoundsSpent() error {
	pending := r.pending()
	resolution := pending.Resolution
	reasons := make([]string, 0, len(resolution.Rounds))
	for i, round := range resolution.Rounds {
		reasons = append(reasons, fmt.Sprintf("round %d: %s", i+1, oneline.Fold(strings.TrimRight(nonEmpty(round.Why, "it did not resolve the conflict"), "."), 500)))
	}
	message := fmt.Sprintf("All %d rounds of resolving the conflict in %s, between %s at %s and %s at %s, were used by runs of %s without resolving it (%s). No further run is started for this conflict. The update waits for a branch to move, or for a person to merge %s into %s by hand; work keeps landing on %s meanwhile.",
		runstate.MaxTeamBranchResolutionRounds, strings.Join(pending.Conflict.Paths, ", "), r.upstream, shortCommit(pending.TeamCommit), r.target, shortCommit(pending.TargetBase),
		resolution.Says(), strings.Join(reasons, "; "), r.upstream, r.target, r.target)
	if err := r.tell(message); err != nil {
		return err
	}
	at := r.now()
	resolution.SpentReportedAt = &at
	return r.save()
}

// alreadyTold carries over that the development manager was told of the same
// thing about the update this one replaced.
func (r *teamBranchRun) alreadyTold(prior *runstate.TeamBranchUpdateAttempt) error {
	r.pending().ReportedAt = prior.ReportedAt
	return r.save()
}

func (r *teamBranchRun) reportConflict(conflict runstate.TeamBranchUpdateConflict) error {
	return r.reportConflictWith(conflict, nil)
}

// reportConflictWith tells the development manager, once, of a conflict, in
// plain words: what conflicts, which items on the target touched it, and what
// the harness does and does not do about it.
func (r *teamBranchRun) reportConflictWith(conflict runstate.TeamBranchUpdateConflict, itemsErr error) error {
	pending := r.pending()
	shown := conflict.Paths
	more := ""
	if len(shown) > teamBranchConflictPathsShown {
		more = fmt.Sprintf(" and %d more", len(shown)-teamBranchConflictPathsShown)
		shown = shown[:teamBranchConflictPathsShown]
	}
	var touched string
	switch {
	case itemsErr != nil:
		touched = fmt.Sprintf("Which items on %s touched those files could not be read: %v.", r.target, itemsErr)
	case len(conflict.Items) > 0:
		names := make([]string, 0, len(conflict.Items))
		for _, item := range conflict.Items {
			names = append(names, item.Says())
		}
		touched = fmt.Sprintf("Items on %s whose landed changes touched those files: %s.", r.target, strings.Join(names, ", "))
	default:
		touched = fmt.Sprintf("No item waiting for the team's merge on %s landed a change to those files.", r.target)
	}
	block := fmt.Sprintf(`{"action":"resolve-conflict","target":%q,"id":"<the item's id>","reason":"<why this item>"}`, r.target)
	message := fmt.Sprintf("Bringing %s up to date with %s stopped on a conflict: merging %s at %s into %s at %s conflicts in %s%s. %s The harness does not resolve a conflict by itself. To resolve it, choose one item, one of those or another whose context fits, and write this tracker action in your reply: %s. The harness then starts a run of that item on %s with the conflict in front of the developer, checks it and has it reviewed independently, then checks and reviews the merged result before it lands. Up to %d such rounds are made, and they are not counted against the item's repair attempts. Work keeps landing on %s while the update waits, and the update is tried again whenever either branch moves.",
		r.target, r.upstream, r.upstream, shortCommit(pending.TeamCommit), r.target, shortCommit(pending.TargetBase), strings.Join(shown, ", "), more, touched, block, r.target, runstate.MaxTeamBranchResolutionRounds, r.target)
	return r.tell(message)
}

func (r *teamBranchRun) reportRefusal(reason string) error {
	pending := r.pending()
	message := fmt.Sprintf("Bringing %s up to date with %s stopped: %s. This was the merge of %s at %s into %s at %s. The harness did not land it; work keeps landing on %s while the update waits, and the update is tried again whenever either branch moves.",
		r.target, r.upstream, strings.TrimRight(strings.TrimSpace(reason), "."), r.upstream, shortCommit(pending.TeamCommit), r.target, shortCommit(pending.TargetBase), r.target)
	return r.tell(message)
}

// tell puts one warning on the development manager's list. The harness files it
// itself, with no run or item behind it, so its run id names the update.
func (r *teamBranchRun) tell(message string) error {
	if r.u.Reports == nil {
		return errors.New("bringing the target branch up to date has nowhere to put reports, so the development manager cannot be told")
	}
	p := r.p
	entry := report.Entry{Severity: report.SeverityWarning, Message: oneline.Fold(message, report.MaxMessageBytes), For: domain.RoleDevelopmentManager}
	collected, err := report.Collect([]report.Entry{entry}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "team-branch-update@" + r.target,
		Build:        p.Build,
		ProductID:    p.Config.Product.ID,
		RepositoryID: string(p.Config.Product.RepositoryID),
	}, r.now())
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := r.u.Reports.Append(reported); err != nil {
			return fmt.Errorf("tell the development manager: %w", err)
		}
	}
	at := r.now()
	r.pending().ReportedAt = &at
	return r.save()
}
