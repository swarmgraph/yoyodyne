package gitworktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	// defaultTimeout bounds one local Git command on an idle machine. It is the
	// idle figure rather than the budget, because the budget is spent in wall
	// clock and a local Git command's wall clock grows with what else the
	// machine is doing: at a load average near forty, `git worktree list` and
	// `git status` were killed at thirty seconds under two race suites and the
	// provider processes beside them, which failed the run that asked and every
	// test exercising Git under the suite. So a manager left to the default
	// scales it by the load, per command, in localTimeout; a caller that names
	// a Timeout has said what it means and is not scaled.
	defaultTimeout = 30 * time.Second
	// maxLoadFactor caps that scaling. A machine ten times oversubscribed gets a
	// five-minute local budget, and a Git command that has hung is still ended
	// rather than holding a run open for as long as the load lasts.
	maxLoadFactor = 10
	// checkoutFileBudget is what one file of a new worktree's checkout adds to
	// the budget its `git worktree add` gets.
	//
	// The local figure above is for a command that reads a ref or writes a
	// handful of small files, and an add does neither: it writes the whole tree,
	// so a budget that does not grow with the tree bounds it by something it has
	// nothing to do with. That is what killed three creations of yoyodyne-ifd.441
	// in three hours on 2026-09-22, each with no Git error at all — the runner's
	// own exit code, and a stderr holding nothing but the checkout's progress,
	// one of them stopped at 87% of 1099 files — while the claim audit gave the
	// item back half an hour later and the next pull started over.
	//
	// Fifty milliseconds a file is far above what a checkout costs on an idle
	// machine, deliberately: the bound is here for an add that has hung, and an
	// add merely crawling under three concurrent race suites is the case that has
	// to survive it. The load scaling the local figure gets applies to the whole
	// of it, so an oversubscribed machine grows this as well.
	//
	// The liveness bound a provider invocation gets is not the alternative, and
	// not for want of output: `git worktree add` does report its checkout to a
	// pipe, but it writes the whole progress stream as one carriage-return line
	// and terminates it only at the end, so a runner that watches for the next
	// line of output sees nothing at all until the add is over.
	checkoutFileBudget = 50 * time.Millisecond
	// uncountedCheckoutFiles is the tree a creation is budgeted for when the
	// count could not be read. It is a figure rather than a refusal because the
	// count only sizes a bound: a creation stopped for want of it would be a
	// creation this budget cost somebody, which is a poor trade for a repository
	// that would have checked out fine.
	//
	// Two thousand files is above anything this repository has held, so the
	// ordinary tree still fits; it is not above every repository, and a very
	// large one whose count failed can still be ended by this bound. That is the
	// right way round — an uncounted tree that is genuinely too big is refused as
	// the environmental death it is and charged nothing, where a budget large
	// enough for any tree would hold a developer slot for the length of an add
	// that has hung. It also keeps the worst case, this figure scaled by a fully
	// oversubscribed machine, inside the thirty minutes a claim may have nothing
	// alive behind it (readmodel.DefaultDeadClaimThreshold).
	uncountedCheckoutFiles = 2000
	// defaultRemote is the remote publishing pushes to when nothing names
	// another.
	defaultRemote = "origin"
	// pushTimeout is longer than the local Git timeout because a push talks to
	// another machine. It is still bounded: a hung network must not hold a run
	// open indefinitely.
	pushTimeout = 5 * time.Minute
	// registrationWalkAttempts and registrationWalkRetryWait bound running a Git
	// command again when it crossed a creation or a removal. `git worktree add`
	// registers the new entry under worktrees/ before it fills the entry in, and
	// any command that walks the registrations in between — the listing, but
	// equally a rebase, a checkout, or a branch deletion checking that a branch
	// is not checked out elsewhere — reads a file that has been created and not
	// yet written and fails the whole command rather than skipping the one entry,
	// so a run can be lost to nothing but another run starting beside it. A
	// removal, and the end of an add, delete files out from under the same walk
	// and fail it the same way; crossedRegistration names all three.
	//
	// The creation lease is not what a reader can take here. A creation holds it
	// while it verifies what it just made, which is itself a listing, so a
	// listing that waited for the lease would wait for itself. What a reader can
	// do is run again: the half-written instant is the time Git takes to write a
	// handful of small files, and it never comes back for the same entry. The
	// retry lives under every command rather than around the listing, because
	// Git walks the registrations from more commands than the one that describes
	// them — see runBounded, and crossedRegistration for the one refusal that is
	// run again.
	//
	// Running again is not the whole answer, because the entry is not always
	// half-written for an instant. An add whose process died between creating a
	// registration file and filling it in leaves the entry half-written for good,
	// and no prune clears it: `git worktree prune` judges an entry by its gitdir
	// file, which such an entry has, and skips one still marked as initializing.
	// That one entry would otherwise fail every registration walk on the
	// repository from then on. So such an entry is cleared where nothing can still
	// be writing it — see settleRegistrations — and a listing refusal that
	// survives the attempts is checked against the bookkeeping rather than
	// believed — see listWorktrees.
	registrationWalkAttempts  = 3
	registrationWalkRetryWait = 50 * time.Millisecond
)

// crossedRegistration is the one refusal a Git command is run again over: Git
// walking the worktree registrations and dying on an entry that changed under
// it. It is Git's own wording, and it names the entry, which is what lets a
// listing that keeps failing be checked against the bookkeeping rather than
// believed. Git has four ways of saying it, one for an entry being written and
// three for an entry going away.
//
// An entry being written: `failed to read .git/worktrees/<id>/commondir`, from
// the one place Git reads a registration's commondir, over a file an add has
// created and not yet filled in. Matching that one file rather than any of the
// entry's is not a narrowing: an empty commondir is the only half-written shape
// Git refuses a walk over at all. Every other file of a registration — gitdir,
// HEAD, index, locked — is walked over in silence whether it is empty or
// absent, and so is a commondir that is missing rather than empty, which is why
// the walk survives an add killed a moment earlier and dies on one killed a
// moment later. That is a claim about the Git on this machine rather than about
// Git in general, so it is asked of Git rather than asserted here — see
// TestOnlyAnEmptyCommondirMakesGitRefuseARegistrationWalk, which fails if a
// future Git starts refusing over some other file and this pattern therefore
// stops covering it.
//
// That test holds each shape still, and one refusal only exists while a file
// is moving: Git asks whether an entry's locked file exists and then reads it,
// and an add that finishes between the two removes the lock it is about to
// read, which Git dies over as "failed to read '<entry>/locked': No such file
// or directory". It is the same passing instant from the other end — the add
// completing rather than starting — and is gone on the next walk, so it is run
// again on the same terms. It is matched on the missing file alone, because a
// lock Git could not read for any other reason is not an instant that passes.
// TestTwoRunsPromotingIntoOneTargetBranchSerializeAndBothLand met it on
// 2026-09-25 against its own creation loop.
//
// The other refusal of an entry going away is the entry itself: Git reads an
// entry's commondir and then resolves the common directory through the entry,
// and a `git worktree remove` beside it that deletes the entry in between is
// what Git dies over as "Invalid path '<common>/worktrees/<id>'". That is what
// failed TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn once,
// over its own creation loop's removals (yoyodyne-ifd.429.3), and it too is
// gone once the command beside it returns, so it is run again on the same
// terms. It is matched only under a Git directory's worktrees/ — a path ending
// in .git, which a primary checkout's and a bare repository's both do — because
// Git names every other path it cannot resolve the same way, and a refusal over
// /repo/src/worktrees/foo is Git's answer about something else.
//
// The last is the add's own, and it is a removal taking worktrees/ itself. Git
// deletes that directory when the entry it removes is the last one there, and
// an add creates the directory and then makes its entry inside it, so a removal
// landing between the two leaves the add nothing to make its entry in: Git dies
// as "could not create directory of '<common>/worktrees/<id>': No such file or
// directory". The add has made nothing by then — its entry is the first thing it
// writes — so running it again is running it. It is matched on the same terms as
// the invalid path, and on the missing directory alone, because an add refused
// that directory for any other reason is not an instant that passes.
// yoyodyne-ifd.429.33 added it, from Git's source rather than from a failure:
// the creation loop the concurrent-runs tests run holds one entry for the whole
// test, so it cannot produce this form, and a machine running one run beside
// another's cleanup can.
var crossedRegistration = regexp.MustCompile(
	`failed to read '?(?:.*[/\\])?worktrees[/\\][^/\\\s]+[/\\](?:commondir|locked'?: No such file or directory)` +
		`|Invalid path '[^']*\.git[/\\]worktrees[/\\][^/\\']+'` +
		`|could not create directory of '[^']*\.git[/\\]worktrees[/\\][^/\\']+': No such file or directory`)

// maintenanceOptions stop a Git command from handing this repository to Git's
// automatic maintenance, and every command the harness runs carries them.
//
// Maintenance prunes worktree registrations, and it judges one stale by whether
// its gitdir file is there — which is exactly what a `git worktree add` has not
// written yet while it is still filling the entry in. A prune reaching that
// window deletes the registration out from under the add, which fails with
// "could not open '.git/worktrees/<id>/gitdir' for writing", and the run is lost
// to nothing but timing. The registry lease queues the harness's own writes to
// that bookkeeping; it cannot queue a prune Git starts for itself after an
// ordinary write command, so the harness never asks for one. Both settings are
// needed because either one alone still leaves a path to the same prune:
// maintenance.auto governs whether the detached run starts at all, and gc.auto
// governs the task inside it that does the pruning.
//
// They are passed per command rather than written into the repository's config,
// because the repository belongs to whoever is developing in it and its
// maintenance is theirs to configure. A Git command the harness did not compose
// is fenced all the same, one layer down: every process the harness launches
// carries the same two settings in its environment, so an agent's Git command
// and a project's build tooling inside a worktree inherit them — see
// execution.WithGitMaintenanceFence. What is left is a prune nobody here
// started, a person's own `git gc` in the checkout, which is the operator's and
// is written down as theirs in `docs/operations.md`.
var maintenanceOptions = []string{"-c", "maintenance.auto=false", "-c", "gc.auto=0"}

type Manager struct {
	runner                execution.ProcessRunner
	gitBinary             string
	repositoryRoot        string
	worktreeRoot          string
	remote                string
	pushRemote            string
	allowedPrimaryChanges map[string]struct{}
	currentExports        []string
	prepareExports        func(context.Context) error
	// timeout is the local Git budget a caller named, and zero where it left
	// the budget to the default, which localTimeout then scales by the load.
	timeout time.Duration
	note    func(format string, args ...any)
	// commonDirectory is the repository's common Git directory once it has been
	// asked, and empty before; see commonGitDirectory.
	commonDirectoryMu sync.Mutex
	commonDirectory   string
}

type Options struct {
	Runner         execution.ProcessRunner
	GitBinary      string
	RepositoryRoot string
	WorktreeRoot   string
	// Remote names the Git remote the target branch lives on: the one a
	// promotion is published into and every question about the target is asked
	// of. It defaults to origin, and a repository that has no remote by that
	// name is never pushed to.
	Remote string
	// PushRemote names the Git remote run branches are pushed to, when that is
	// not the same repository the work is published into. It is what a
	// contributor without push access to the target repository needs: the branch
	// goes to their fork and the pull request is opened across the two. Empty
	// means the run branch is pushed to Remote, which is the arrangement every
	// project with push access has.
	PushRemote string
	// AllowedPrimaryChanges lists repository-relative control-plane files that
	// may be updated after preflight without becoming part of the worktree base.
	AllowedPrimaryChanges []string
	// CurrentExports lists repository-relative files derived from a store that is
	// authoritative outside Git, which a new worktree is given the primary
	// checkout's copy of rather than the copy its base commit happened to carry.
	// They are read by a run and never written by one, so each is held out of the
	// change the run makes.
	CurrentExports []string
	// PrepareExports refreshes the primary snapshots before they are copied.
	// A failure refuses the copy, so a run is never given a stale export as current.
	PrepareExports func(context.Context) error
	// Timeout bounds one local Git command, as named. Zero leaves it to the
	// default, which is scaled by the machine's load per command: see
	// defaultTimeout for why a fixed figure was killing Git under a loaded
	// suite.
	Timeout time.Duration
	// Note is where the manager says what it worked around. There is one such
	// thing and it is worth a line: a listing that described this repository
	// without a worktree another run had not finished registering, which nothing
	// else would ever tell a reader about. It is optional, and a manager
	// assembled without one steps over the same entry silently.
	Note func(format string, args ...any)
}

type CreateRequest struct {
	// ResumeCreation permits the harness to recover a creation whose returned
	// worktree was never recorded. It accepts only an unchanged checkout on the
	// same run's branch, whose base is already contained in the target.
	ResumeCreation bool
	RunID          string
	WorkItemID     string
	BaseRef        string
	// TargetBranch names the local branch the finished work is meant to be
	// promoted into. It is recorded on the worktree and never changes
	// afterwards. When it is empty the worktree can be created and inspected
	// but never integrated.
	TargetBranch string
}

type Worktree struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseRef    string `json:"base_ref"`
	BaseCommit string `json:"base_commit"`
	// TargetBranch is the immutable integration target recorded at creation.
	TargetBranch string `json:"target_branch,omitempty"`
	// HarnessCommit is the last commit the harness itself made on this branch,
	// empty until it makes one. It is what permits the worktree's HEAD to have
	// moved at all: publishing needs a commit before it can push, and naming the
	// permitted commit in advance is what keeps "Git commits are owned by the
	// harness" a fact about recorded hashes rather than about what a commit looks
	// like. A caller carries it forward from the publication that produced it.
	HarnessCommit string `json:"harness_commit,omitempty"`
}

type Inspection struct {
	Registered bool
	Dirty      bool
	Branch     string
}

// Observation is what a run's recorded artifacts actually look like now. It is
// read-only and every field is optional, because the question it answers is
// asked exactly when a process died partway through creating or removing them:
// an absent worktree, a deleted branch, and a target that has moved on are all
// observations rather than failures.
type Observation struct {
	WorktreeRegistered bool   `json:"worktree_registered"`
	WorktreePresent    bool   `json:"worktree_present"`
	WorktreeDirty      bool   `json:"worktree_dirty"`
	WorktreeBranch     string `json:"worktree_branch,omitempty"`
	WorktreeHead       string `json:"worktree_head,omitempty"`
	BranchExists       bool   `json:"branch_exists"`
	BranchCommit       string `json:"branch_commit,omitempty"`
	TargetExists       bool   `json:"target_exists"`
	TargetCommit       string `json:"target_commit,omitempty"`
	// BranchIntegrated reports that the run branch carries a commit past its
	// base and that commit is contained in the recorded target. It is the only
	// evidence that an integration nobody managed to write down actually
	// promoted the work, so it is deliberately not satisfied by the base commit
	// the target already contains by construction.
	BranchIntegrated bool `json:"branch_integrated"`
}

// Integration reports exactly which commits an integration produced, so a
// caller can record the promoted work and prove the target moved where it
// expected before removing anything.
//
// A refused promotion reports what it had already done rather than nothing: the
// harness commits whatever the developer left before it advances anything, and
// that commit exists whether or not the advance succeeds. TargetCommit is the
// one field that is only ever set by a promotion that happened.
//
// ThroughPullRequest marks a promotion PrepareLanding prepared rather than one
// Integrate made: the local target was not moved, and the change reaches the
// target only by the forge merging the pull request that carries it. Its
// TargetCommit is the commit being landed — the promoted commit every check
// after it asks the remote about — and not where the local target stands.
type Integration struct {
	Branch               string `json:"branch"`
	TargetBranch         string `json:"target_branch"`
	SourceCommit         string `json:"source_commit"`
	TargetCommit         string `json:"target_commit"`
	PreviousTargetCommit string `json:"previous_target_commit"`
	ThroughPullRequest   bool   `json:"through_pull_request,omitempty"`
}

// Rebase is what re-preparing a run's change against a moved target produced:
// the target commit the change now sits on, which is the worktree's new base,
// and the harness commit that carries the replayed work. The previous pair is
// reported beside them because a caller records both and has to be able to say
// what it replaced.
//
// A refused rebase reports the same way an integration does. HeadCommit names
// the commit the harness owns in that worktree either way, so a caller that
// could not replay the work still knows which HEAD it is permitted to accept;
// BaseCommit is the base the change actually sits on, which an aborted rebase
// leaves exactly where it was.
type Rebase struct {
	Branch             string `json:"branch"`
	TargetBranch       string `json:"target_branch"`
	BaseCommit         string `json:"base_commit"`
	HeadCommit         string `json:"head_commit"`
	PreviousBaseCommit string `json:"previous_base_commit"`
	PreviousHeadCommit string `json:"previous_head_commit"`
}

// RebaseConflict is a refused replay with the detail whoever settles it needs
// rather than only the fact that one happened: the paths Git stopped on, and
// where the target had moved to. Both are collected before the replay is
// abandoned, because abandoning it is what puts the worktree back and takes the
// evidence with it.
//
// It carries the sentinel above rather than replacing it, so every caller that
// only asks which class of failure this is keeps working unchanged.
type RebaseConflict struct {
	Branch       string
	TargetBranch string
	// TargetCommit is where the target branch had got to: the other side of the
	// disagreement, and the commit anybody reconciling the change has to read.
	TargetCommit string
	// Paths are the repository-relative paths Git left unmerged, in the order it
	// listed them. A replay refused for something other than content can leave
	// none, so an empty list is a conflict with nothing to name rather than an
	// absent one.
	Paths []string
	// Err is Git's own account of the failure, kept whole.
	Err error
}

func (c *RebaseConflict) Error() string {
	return ErrRebaseConflict.Error() + ": " + c.Err.Error()
}

// Unwrap reports the sentinel and Git's own failure both, so errors.Is finds
// either exactly as the wrapped pair this replaced did.
func (c *RebaseConflict) Unwrap() []error { return []error{ErrRebaseConflict, c.Err} }

// Moved reports that the replay actually put the change on a different base. A
// rebase onto a target that never moved is a legitimate no-op — an integration
// can lose a race to a lock rather than to a commit — and it must not read as
// work that was replayed.
func (r Rebase) Moved() bool {
	return r.BaseCommit != r.PreviousBaseCommit
}

// CleanupRequest names exactly which artifacts may be removed and the evidence
// that permits removing them. Carrying the source commit rather than only a
// branch name is what makes cleanup resumable: the proof that the work was
// integrated survives the branch that carried it.
type CleanupRequest struct {
	Worktree     Worktree
	TargetBranch string
	SourceCommit string
}

// Cleanup reports what is absent after a cleanup attempt, whether this attempt
// removed it or a previous one did. Reporting the two artifacts separately is
// what lets a caller describe a partial cleanup truthfully.
type Cleanup struct {
	WorktreeRemoved bool `json:"worktree_removed"`
	BranchRemoved   bool `json:"branch_removed"`
}

// Complete reports whether nothing is left to clean up.
func (c Cleanup) Complete() bool {
	return c.WorktreeRemoved && c.BranchRemoved
}

// Identity of the harness-owned integration commit. It is deliberately not the
// developer's identity: the harness, not the agent, authors Git history.
const (
	harnessCommitAuthorName  = "Yoyodyne Harness"
	harnessCommitAuthorEmail = "harness@yoyodyne.invalid"
)

var (
	// ErrNoChanges reports that there is nothing to integrate. It is a refusal,
	// not a failure: an empty commit would claim work that does not exist.
	ErrNoChanges = errors.New("worktree has no changes to integrate")
	// ErrOwnedHeadMoved reports a checkout whose HEAD differs from the recorded
	// harness commit, rather than a failed reading of the checkout.
	ErrOwnedHeadMoved = errors.New("the preserved worktree HEAD moved")
	// ErrTargetDrift reports that the target branch moved away from the base the
	// work was written against, so the change must be reconciled rather than
	// merged.
	ErrTargetDrift = errors.New("target branch moved away from the recorded base commit")
	// ErrNotFastForward reports that the target could not be advanced by a
	// fast-forward. The harness never resolves this by forcing or resetting.
	ErrNotFastForward = errors.New("target branch cannot be fast-forwarded")
	// ErrRebaseConflict reports a change that could not be replayed onto its
	// moved target. Nothing here resolves one: the harness replays work, and
	// which side of a conflict is right is a judgement about the product rather
	// than a Git operation. What the caller does with it — hand it to whoever
	// wrote the change, or to a person — is the caller's.
	ErrRebaseConflict = errors.New("change cannot be replayed onto the moved target branch")
	// ErrReplayKilled reports a replay this harness ended before Git finished it:
	// a budget that ran out, a context that was cancelled, or a process that went
	// silent past its liveness bound. It is never a conflict. A killed rebase
	// leaves the same state directory a conflicted one does, and reading it as one
	// sent an operator to settle a disagreement that did not exist (run b82c1c5a,
	// under load against the local Git budget). It is returned only once the
	// replay has been abandoned and the worktree is back on its branch, so a
	// caller may treat it as the environment having stopped the promotion rather
	// than as anything about the change.
	ErrReplayKilled = errors.New("the replay onto the moved target branch was ended by the harness before it finished")
	// ErrPrimaryNotReady reports the primary checkout carrying uncommitted state
	// the harness does not own, so no worktree may be cut from it and no change
	// may be promoted into it. It is a sentinel rather than only a message because
	// a caller has to tell it from every other reason a Git operation refused: a
	// round turned away by this delivered nothing because the environment was
	// wrong, which is a different fact about the work than a change that failed.
	ErrPrimaryNotReady = errors.New("the primary checkout is not as the harness left it")
	// ErrCheckoutKilled reports a Git command a worktree checkout needed — the
	// `git worktree add` itself, or the count that sizes its budget — which this
	// harness ended while it was still running, rather than Git answering. It is
	// a sentinel for the reason the one above is: nothing of the work was
	// reached — no worktree exists and no agent was ever invoked — so a round
	// turned away by it delivered nothing because the machine was too busy, which
	// is a different fact about the work than a creation Git refused.
	//
	// It covers both commands rather than only the add because they are one
	// failure: each is a local Git command of the same creation ended by a
	// deadline under the same load, neither says anything about the change, and
	// a class that held only one of them would charge the item a round and its
	// re-run whenever the load happened to arrive a command earlier.
	ErrCheckoutKilled = errors.New("a Git command the worktree checkout needed was ended by the budget the harness gave it")
)

type ChangeSummary struct {
	Status   string `json:"status"`
	DiffStat string `json:"diff_stat,omitempty"`
}

// Default bounds for the unified change representation handed to a reviewer.
const (
	DefaultMaxDiffBytes     = 256 << 10
	DefaultMaxDiffFileBytes = 64 << 10
	DefaultMaxDiffFiles     = 200
	// DefaultMaxListedFiles bounds the listing of every file a change touches.
	// It is separate from the file bound above, which is for files rendered one
	// by one into the patch: a listing line is a hundred bytes where a rendered
	// file can be sixty thousand, so the listing can afford to name far more
	// than the patch can show.
	DefaultMaxListedFiles = 1000
)

// DiffLimits bounds how much of a worktree's change a caller is willing to
// carry. Zero fields fall back to the defaults above.
type DiffLimits struct {
	// MaxTotalBytes bounds the complete tracked-and-untracked patch. It is
	// spent file by file: a file's diff is in the patch whole or it is named as
	// omitted, and no file is cut part-way through.
	MaxTotalBytes int
	// MaxFileBytes bounds each untracked file before Git renders its patch.
	// Tracked changes remain bounded by MaxTotalBytes alone, because a tracked
	// file's diff is often far smaller than the file — and where it is not, a
	// reduction that deletes three thousand lines is exactly the change a
	// reviewer has to be shown, not the one a per-file ceiling should drop.
	MaxFileBytes int
	// MaxFiles bounds separately rendered untracked files. Tracked changes are
	// not counted against it; each of them is one Git rendering, and they
	// remain bounded by MaxTotalBytes.
	MaxFiles int
	// MaxListedFiles bounds the listing of every file the change touches, which
	// is carried beside the patch rather than inside it.
	MaxListedFiles int
	// MaxCommits bounds how many commits of a change are described. It bounds a
	// branch-scope change and the listing that says what a worktree's patch
	// spans; neither patch is bounded by it, because a worktree's change is
	// measured against one base commit rather than assembled out of its history.
	MaxCommits int
}

// ChangeDiff is a bounded unified view of everything a developer changed in a
// worktree. Untracked files are diffed against /dev/null so they appear in the
// same patch as tracked edits without staging them or otherwise mutating the
// worktree. Truncated reports that the bounds dropped part of the change, so a
// caller never mistakes a clamped patch for the whole story.
//
// The bounds are applied whole file by whole file, never to the tail of the
// patch. A patch cut at a byte count keeps whichever files Git happened to
// render first and loses the rest without a name, so a reviewer handed one
// could not say which files it had judged; every file this carries is either
// shown in full or listed in OmittedFiles with its size and the bound that
// dropped it, and Files names every file the change touches whether or not the
// patch could show it.
//
// The patch presents the files in class order — source first, then tests, then
// test data and generated or golden files — rather than in the order Git lists
// them, which is alphabetical. Spent alphabetically, the bound falls wherever
// the paths happen to sort: a change whose committed fixtures sort before its
// code (five and a half thousand lines of internal/dashboard/testdata ahead of
// the internal/readmodel derivation the page depends on) spent the whole bound
// on renders and sent the code to review unseen, and that cost yoyodyne-ifd.141.3
// two rounds. Spent in class order, the bound falls on the tail — on the
// fixtures — and the code that matters is what is presented whole.
type ChangeDiff struct {
	Status         string        `json:"status"`
	DiffStat       string        `json:"diff_stat,omitempty"`
	Patch          string        `json:"patch,omitempty"`
	UntrackedFiles []string      `json:"untracked_files,omitempty"`
	OmittedFiles   []OmittedFile `json:"omitted_files,omitempty"`
	// DeletedFiles are the files whose diff is nothing but removed lines and
	// that are described at the base commit rather than rendered as a removal
	// diff: every file the change deletes whole, and every file it reduces by
	// removal alone that did not fit in what the bound had left once every other
	// file was placed. A removal carries no new content to judge, so what a
	// reader needs of one is that it happened and what the file was, and none of
	// them displaces another file from the bound or is omitted by it
	// (yoyodyne-ifd.429.7).
	DeletedFiles []DeletedFile `json:"deleted_files,omitempty"`
	Truncated    bool          `json:"truncated"`
	// Files is the tree listing of the change: every path it touches against
	// the base, with the file's size at the tip, whether it is binary, and
	// whether it is already committed on the branch. It is carried beside the
	// patch because the patch cannot show everything the change delivers — a
	// binary has no textual diff at all — and a reviewer that is not told a file
	// is there infers it from whatever else passed. FilesOmitted counts the
	// entries the listing bound dropped; the patch is not bounded by it.
	Files        []ChangedFile `json:"files,omitempty"`
	FilesOmitted int           `json:"files_omitted,omitempty"`
	// BaseCommit is the commit the change is measured against, HeadCommit is
	// the branch's tip the change was read at — the base itself where nothing
	// has been committed, and the last harness commit otherwise — and Commits are
	// the commits already made for it over that base, oldest first. They are
	// carried because a patch says what changed and nothing says what it spans:
	// a run that continues on a branch its earlier attempts already committed to
	// hands over a patch that covers those commits, and a reader told only
	// "worktree changes" reads it as the uncommitted tail of one. That reading
	// cost nine review filings across two work items, each discounting a verdict
	// over committed work the reviewer had in fact been shown. The tip is what a
	// review record names beside the base so what a verdict was judged against
	// can be read back as two commits rather than reconstructed from the branch.
	//
	// They are empty on a branch-scope change, which names its own base and
	// history in the fields beside this one.
	BaseCommit string   `json:"base_commit,omitempty"`
	HeadCommit string   `json:"head_commit,omitempty"`
	Commits    []Commit `json:"commits,omitempty"`
	// CommitsOmitted counts the commits the bound dropped from that listing,
	// oldest first. It does not truncate the change: the patch is the whole
	// range whatever the listing holds, so what an omission costs is the reader's
	// account of which commits made it rather than any part of the change itself.
	CommitsOmitted int `json:"commits_omitted,omitempty"`
	// CommitsWithoutEffect are the commits the worktree carries over its base
	// where the change against that base is nothing: work an attempt committed
	// and a later attempt undid. It is filled in that case alone, because that
	// is the only case where an empty patch says nothing about why — everywhere
	// else the patch is what the commits did, and listing them beside it would
	// describe the same change twice.
	CommitsWithoutEffect []Commit `json:"commits_without_effect,omitempty"`
}

// OmissionReason is which bound kept one delivered file out of the patch. It is
// recorded rather than folded into a single "omitted" flag because the reasons
// are different facts about the change: a file too big to render, a file with no
// reviewable diff, and a file the budget ran out before are three different
// things for a reviewer to do something about.
type OmissionReason string

const (
	// OmittedTooLarge is the per-file ceiling: the file is delivered, it is
	// bigger than one file is allowed to be in a patch, and none of it is shown.
	OmittedTooLarge OmissionReason = "too-large"
	// OmittedBinary is content Git itself will not render as a diff.
	OmittedBinary OmissionReason = "binary"
	// OmittedPatchFull is the total bound: the patch had no room left by the
	// time this file was reached.
	OmittedPatchFull OmissionReason = "patch-full"
	// OmittedTooManyFiles is the file-count bound.
	OmittedTooManyFiles OmissionReason = "too-many-files"
	// OmittedUnreadable is anything that is not a regular file the harness can
	// read from the worktree: a symlink, a socket, a path that left between
	// being listed and being read.
	OmittedUnreadable OmissionReason = "unreadable"
)

// FileClass is which of three kinds of file a path in a change is, for the
// order the patch presents them in and for what a reader is told about a file
// the patch could not show: source, a test, or test data — fixtures, golden
// files, renders, generated code, lock files — that a test consumes rather than
// a reader reviews line by line. It is decided from the path and, for a file
// whose diff has been rendered, from a generated-code marker in it, because a
// generated file announces itself in its own first line and nowhere else.
type FileClass string

const (
	// FileClassSource is everything that is neither of the two below: the code,
	// and the documents beside it, that a review is for.
	FileClassSource FileClass = "source"
	// FileClassTest is a test file: named `_test`, `.test`, `_spec`, or `.spec`
	// before its extension, or living in a directory named for tests.
	FileClassTest FileClass = "test"
	// FileClassFixture is test data and generated content: anything under a
	// `testdata`, `fixtures`, `golden`, or `snapshots` directory, a `.golden` or
	// `.snap` file, a lock file a package manager writes, a Go file named as
	// generated, or a file whose rendered diff carries the generated-code marker.
	FileClassFixture FileClass = "fixture"
)

// Describe is the words a reader is given for the class.
func (c FileClass) Describe() string {
	switch c {
	case FileClassTest:
		return "test"
	case FileClassFixture:
		return "test data or generated"
	}
	return "source"
}

// order is the position of the class in the patch: source first, then tests,
// then fixtures, so a bound spent in this order falls on the tail.
func (c FileClass) order() int {
	switch c {
	case FileClassTest:
		return 1
	case FileClassFixture:
		return 2
	}
	return 0
}

var (
	// fixtureDirectories are the directory names that make everything beneath
	// them test data. `testdata` is Go's own convention; the rest are the
	// conventions of the ecosystems a project here has actually shipped.
	fixtureDirectories = map[string]bool{
		"testdata": true, "fixtures": true, "fixture": true, "golden": true,
		"snapshots": true, "__snapshots__": true, "__fixtures__": true,
	}
	// testDirectories are the directory names that make everything beneath them
	// a test.
	testDirectories = map[string]bool{
		"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true, "e2e": true,
	}
	// lockFiles are what a package manager writes and nobody reviews by hand.
	lockFiles = map[string]bool{
		"go.sum": true, "package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
		"Cargo.lock": true, "Gemfile.lock": true, "poetry.lock": true, "composer.lock": true,
	}
	// generatedLine is the line a generated file carries at its head, in the
	// form Go specifies — `// Code generated ... DO NOT EDIT.` — and the same
	// sentence behind another comment leader. generatedMarker matches it in a
	// rendered diff, where the line arrives prefixed by the diff's own `+` or
	// space and a removed line is the file's past; generatedHead matches it in
	// the file itself.
	generatedLine   = `\S{0,3} ?Code generated .* DO NOT EDIT\.$`
	generatedMarker = regexp.MustCompile(`(?m)^[+ ]` + generatedLine)
	generatedHead   = regexp.MustCompile(`(?m)^` + generatedLine)
)

// ClassifyPath decides a file's class from its path alone: the directories it
// is under and the name it has. A file whose content is to hand is classed by
// classifySection or classifyUntracked, which read the generated-code marker
// as well; this is what the tree listing uses, which describes a path rather
// than a rendering.
func ClassifyPath(path string) FileClass {
	path = filepath.ToSlash(path)
	directory, name := "", path
	if cut := strings.LastIndexByte(path, '/'); cut >= 0 {
		directory, name = path[:cut], path[cut+1:]
	}
	for _, component := range strings.Split(directory, "/") {
		if fixtureDirectories[component] {
			return FileClassFixture
		}
	}
	if lockFiles[name] {
		return FileClassFixture
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	switch {
	case strings.HasSuffix(name, ".golden"), strings.HasSuffix(name, ".snap"):
		return FileClassFixture
	case strings.HasSuffix(name, ".pb.go"), strings.HasSuffix(name, ".gen.go"), strings.HasSuffix(name, "_gen.go"),
		strings.HasSuffix(name, "_generated.go"), strings.HasPrefix(name, "zz_generated"):
		return FileClassFixture
	case strings.HasSuffix(stem, "_test"), strings.HasSuffix(stem, ".test"),
		strings.HasSuffix(stem, "_spec"), strings.HasSuffix(stem, ".spec"):
		return FileClassTest
	}
	for _, component := range strings.Split(directory, "/") {
		if testDirectories[component] {
			return FileClassTest
		}
	}
	return FileClassSource
}

// classifySection decides a rendered file's class: its path first, and then the
// generated-code marker its diff would carry if the file were generated. The
// marker is only looked for in a file the path calls source, since the path has
// already settled the other two.
func classifySection(path, patch string) FileClass {
	class := ClassifyPath(path)
	if class == FileClassSource && generatedMarker.MatchString(patch) {
		return FileClassFixture
	}
	return class
}

// classifyUntracked decides an untracked file's class the way classifySection
// decides a rendered one's, reading the marker from the head of the file
// itself because its diff is rendered only once the bounds have admitted it.
// A file that cannot be read is classed by its path: what becomes of it is
// the omission record's to say, not this.
func (m *Manager) classifyUntracked(path, relative string) FileClass {
	class := ClassifyPath(relative)
	if class != FileClassSource {
		return class
	}
	// Only a regular file is opened: a symlink is named as unreadable by the
	// omission record and is not followed here either.
	if _, regular, err := m.untrackedSize(path, relative); err != nil || !regular {
		return class
	}
	file, err := os.Open(filepath.Join(path, filepath.FromSlash(filepath.Clean(relative))))
	if err != nil {
		return class
	}
	defer file.Close()
	head := make([]byte, binarySniffBytes)
	n, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return class
	}
	if generatedHead.Match(head[:n]) {
		return FileClassFixture
	}
	return class
}

// OmittedFile is one file a change delivers that the bounds kept out of the
// patch. It carries the path, the size, and the bound that dropped it, because
// a name on its own leaves the reviewer unable to tell an oversized file from a
// missing one — which is the reading that refused two changes before this was
// recorded. Every file the untracked half drops is one of these: a file is in
// the patch or it is here, and nothing leaves on a flag alone.
type OmittedFile struct {
	Path   string         `json:"path"`
	Bytes  int64          `json:"bytes,omitempty"`
	Reason OmissionReason `json:"reason"`
	// Class is what kind of file was kept out, so a reader told the patch is
	// missing something knows whether it is missing code or a fixture. The
	// patch is presented in class order, so what the bound drops is the tail of
	// that order — fixtures before tests, and tests before source — and a
	// source file here means the change is too large for the bound even before
	// its test data.
	Class FileClass `json:"class,omitempty"`
	// Bound is the limit that dropped it — bytes for the size bounds, a file
	// count for the file bound, and zero where the reason is not a bound at all.
	// It is recorded beside the size so a reader sees the comparison that was
	// made rather than being asked to know the harness's defaults.
	Bound int64 `json:"bound,omitempty"`
	// DiffBytes is how big the file's rendered diff was, for a tracked file the
	// patch bound kept out. It is the number the bound was actually compared
	// against — a tracked file's diff can be far smaller than the file, or, for
	// a deletion, far larger than the nothing left at the tip — and it is zero
	// for an untracked file, which is measured by its size before it is
	// rendered at all.
	DiffBytes int64 `json:"diff_bytes,omitempty"`
	// Digest is the content digest of the file at the tip of the change. It is
	// what turns a named omission into openable evidence: the size says how much
	// was kept out and the digest says exactly which bytes, so a person opening
	// the fixture afterwards can prove they opened what the review was made over
	// rather than what the file became later. Nothing else binds a verdict to
	// content the patch never carried.
	//
	// It says which digest it is, because the two scopes deliver the file in
	// different places and a reader checks it where they are sent. A worktree's
	// change carries `sha256:<hex>` of the file on disk, which is what somebody
	// holding the worktree checks with `shasum -a 256`; a branch's accumulated
	// change carries `git-blob:<object-id>`, which is what `git rev-parse
	// <commit>:<path>` answers at the tip it sends them to.
	//
	// It is empty in the two cases where the change leaves nothing at the tip to
	// digest: a file the change deletes, whose zero size says the same thing, and
	// a path that is not a readable regular file, whose reason does. Both are
	// stated rather than left to be inferred, because a missing digest and an
	// undigestable file are different facts about the change.
	Digest string `json:"digest,omitempty"`
	// Undigestable reports that the change leaves something at this path that is
	// not a readable regular file — a symlink, a socket, a device — so there is
	// no content to digest and nothing anybody could open, whatever the bound
	// above says about why the patch does not show it.
	//
	// It is carried because a missing digest has two causes that measure
	// identically: this, and a file the change deletes. Both are zero bytes with
	// no digest, and only one of them is a listing a reader has the whole of, so
	// without this the two are the same record and a link reads as a deletion.
	Undigestable bool `json:"undigestable,omitempty"`
}

// Describe is the one sentence a reader is given about a file that is in the
// change and not in the patch. It names the file, its size, and the bound,
// because "omitted" alone tells a reviewer that something is missing and nothing
// about whether anybody could have shown it.
func (f OmittedFile) Describe() string {
	switch f.Reason {
	case OmittedTooLarge:
		if f.DiffBytes > 0 {
			return fmt.Sprintf("%s: delivered but too large to show; its diff alone is %d bytes and the patch bound is %d bytes.", f.sized(), f.DiffBytes, f.Bound)
		}
		return fmt.Sprintf("%s: delivered but too large to show; the per-file bound is %d bytes.", f.sized(), f.Bound)
	case OmittedBinary:
		return fmt.Sprintf("%s: delivered but binary, so it has no reviewable diff.", f.sized())
	case OmittedPatchFull:
		if f.DiffBytes > 0 {
			return fmt.Sprintf("%s: delivered but not shown; its diff is %d bytes and the %d-byte patch bound had no room left for it.", f.sized(), f.DiffBytes, f.Bound)
		}
		return fmt.Sprintf("%s: delivered but not shown; the %d-byte patch bound was already spent.", f.sized(), f.Bound)
	case OmittedTooManyFiles:
		return fmt.Sprintf("%s: delivered but not shown; the change adds more new files than the bound of %d.", f.sized(), f.Bound)
	case OmittedUnreadable:
		return fmt.Sprintf("%s: delivered but not a readable regular file, so there is nothing to diff.", f.Path)
	}
	return fmt.Sprintf("%s: delivered but not shown.", f.sized())
}

// sized is the path with the file's size at the tip beside it, its digest where
// there is content at the tip to digest, and its class where the class is not
// the source a reader assumes: a reviewer told that a fixture was kept out reads
// the omission differently from one told that code was, and a person opening the
// fixture afterwards needs the digest to know it is the one the review covered.
func (f OmittedFile) sized() string {
	qualities := []string{fmt.Sprintf("%d bytes", f.Bytes)}
	if f.Digest != "" {
		qualities = append(qualities, f.Digest)
	}
	// Why there is no digest, where the reason for the omission does not already
	// say it: the reader is otherwise sent to open something the change does not
	// leave there to open.
	if f.Digest == "" && f.Undigestable {
		qualities = append(qualities, "not a regular file at the tip, so nothing to open")
	}
	if f.Class != "" && f.Class != FileClassSource {
		qualities = append(qualities, f.Class.Describe())
	}
	return fmt.Sprintf("%s (%s)", f.Path, strings.Join(qualities, ", "))
}

// ListedWhole reports an omission a reader has the whole of: the path, the size,
// and the digest that says which bytes were kept out. It is the half of
// yoyodyne-ifd.425's rule that a listing can fail — an omission the patch names
// with nothing to open is an absence rather than evidence — and it is what
// UnreviewableOmissions below holds every omitted fixture to.
//
// A file that is not a readable regular file is never listed whole: there is
// nothing delivered for anybody to open, whatever the patch says about it. That
// holds for a path Git tracks as well as one it does not, by two different marks
// — an untracked one is omitted as unreadable, and a tracked one keeps the
// reason of whichever bound dropped it and carries Undigestable — because a
// tracked path that became a link has a rendered diff the bound had no room for,
// and naming it unreadable would claim there was nothing to diff.
//
// A file the change deletes is listed whole with no digest at all: the change
// leaves nothing at the tip, which is the whole of its content there, and its
// zero size says so beside the reason. That is the case the flag above exists to
// keep this from confusing a link with.
func (f OmittedFile) ListedWhole() bool {
	if f.Path == "" || f.Reason == OmittedUnreadable || f.Undigestable {
		return false
	}
	return f.Digest != "" || f.Bytes == 0
}

// UnreviewableOmissions names every part of this change's representation a
// review cannot be completed over, and answers nothing where the representation
// is one a reviewer can judge.
//
// It exists because "any omission refuses approval" made a whole class of change
// reviewable and unclosable. A change whose test data alone outgrows the patch
// bound presents its code whole — the bound is spent in class order, so what it
// keeps out is fixtures — and was then refused approval for the omission that
// ordering exists to produce, which is the dashboard page's shape
// (yoyodyne-ifd.141.3) and the question yoyodyne-ifd.404 left open. The product
// manager's rule narrows the refusal to the two omissions that really do leave a
// change unjudged: a non-fixture file the patch could not show, and a fixture the
// patch named without saying what it is.
//
// A truncation with nothing named is the third, and it is not an omitted file at
// all: a branch whose history the bound clipped reports itself truncated and
// lists no file, and a reviewer cannot tell what it did not see. The caller adds
// that case, which is why this answers a list rather than a verdict.
func (d ChangeDiff) UnreviewableOmissions() []string {
	var problems []string
	for _, file := range d.OmittedFiles {
		switch {
		case file.Class != FileClassFixture:
			problems = append(problems, fmt.Sprintf("%s is %s and the patch does not show it", file.Path, file.Class.Describe()))
		case !file.ListedWhole():
			problems = append(problems, fmt.Sprintf("%s is test data the patch does not show and does not list whole: %s", file.Path, file.Describe()))
		}
	}
	return problems
}

// OverReviewBound is every source or test file the patch's size and count bounds
// kept out: the omissions that refuse an approval and that no review round can
// change, because the bound is the harness's and not the reviewer's. A fixture
// is never one of them — the rules allow an approval over omitted test data —
// and neither is content kept out for being binary or unreadable, which is not
// the bound at all. It is what the pipeline measures a change against before any
// check or review is spent on it.
func (d ChangeDiff) OverReviewBound() []OmittedFile {
	var over []OmittedFile
	for _, file := range d.OmittedFiles {
		if file.Class == FileClassFixture {
			continue
		}
		switch file.Reason {
		case OmittedTooLarge, OmittedPatchFull, OmittedTooManyFiles:
			over = append(over, file)
		}
	}
	return over
}

// DeletedFile is one file whose change is removal alone, described by what it
// was at the base commit rather than rendered as the diff that removes it.
//
// It exists because a deletion diff is the file's whole content with a minus in
// front of each line, and a large enough file outgrew the patch bound, was named
// as omitted, and — being a document rather than test data — refused the
// approval. yoyodyne-ifd.117.4's reduction of docs/configuration.md could never
// pass review that way however sound it was. A removal adds nothing to judge:
// what a reviewer needs is that the file is gone, what it was — its path, its
// size and a digest at the base, where the whole of it can still be opened —
// and the work's own account of why.
type DeletedFile struct {
	Path string `json:"path"`
	// Whole reports that the change deletes the file outright. Otherwise the file
	// is still there at the tip, reduced by removed lines alone, and the diff that
	// removes them did not fit in what the patch bound had left once every other
	// file was placed.
	Whole bool `json:"whole"`
	// BaseCommit is the commit the file is described at, which is where it can be
	// opened whole as `git show <base>:<path>`.
	BaseCommit string `json:"base_commit"`
	// BaseBytes and BaseDigest are the file's size at the base commit and the
	// object its content is there, as `git-blob:<object-id>`, which is what
	// `git rev-parse <base>:<path>` answers.
	BaseBytes  int64  `json:"base_bytes"`
	BaseDigest string `json:"base_digest"`
	// Bytes and Digest are the file at the tip, for a file reduced rather than
	// deleted: zero and empty for a whole deletion, which leaves nothing there.
	// The digest is the one an omission of the same scope would carry.
	Bytes  int64  `json:"bytes,omitempty"`
	Digest string `json:"digest,omitempty"`
	// RemovedLines is how many lines the diff removes, and DiffBytes how big the
	// diff that was not rendered is.
	RemovedLines int       `json:"removed_lines,omitempty"`
	DiffBytes    int64     `json:"diff_bytes,omitempty"`
	Class        FileClass `json:"class,omitempty"`
}

// Describe is the one sentence a reader is given about a removal the patch
// describes rather than renders: what the file was, where it can be opened, and
// what is left of it.
func (f DeletedFile) Describe() string {
	base := fmt.Sprintf("%d bytes at base commit %s (%s), where the whole of it can be opened as `git show %s:%s`",
		f.BaseBytes, f.BaseCommit, f.BaseDigest, f.BaseCommit, f.Path)
	class := ""
	if f.Class != "" && f.Class != FileClassSource {
		class = " (" + f.Class.Describe() + ")"
	}
	if f.Whole {
		return fmt.Sprintf("%s%s: deleted whole; it was %s, and the change leaves nothing at this path.", f.Path, class, base)
	}
	tip := fmt.Sprintf("%d bytes", f.Bytes)
	if f.Digest != "" {
		tip += " (" + f.Digest + ")"
	}
	return fmt.Sprintf("%s%s: reduced by removal alone — %d line(s) removed and none added, a %d-byte diff not rendered here; it was %s, and it is %s at the tip.",
		f.Path, class, f.RemovedLines, f.DiffBytes, base, tip)
}

// removalOnly reads one file's rendered diff and reports whether it is nothing
// but removed lines: whole when the file is deleted outright, and otherwise the
// count of lines removed from a file that remains. A diff that adds a line, a
// type change, a creation, and a binary change to a file that remains are not
// removals, and neither is a diff with nothing removed at all, such as a mode
// change. A binary file deleted outright is a whole deletion like any other.
func removalOnly(patch string) (whole bool, removed int, ok bool) {
	inHunks, binary, headers := false, false, 0
	for _, line := range strings.Split(patch, "\n") {
		// A content line is prefixed by a space, a plus, or a minus, so a header
		// here is a second block: a type change renders as two.
		if strings.HasPrefix(line, "diff --git ") {
			if headers++; headers > 1 {
				return false, 0, false
			}
			continue
		}
		if !inHunks {
			switch {
			case strings.HasPrefix(line, "deleted file mode "):
				whole = true
			case strings.HasPrefix(line, "new file mode "):
				return false, 0, false
			case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
				binary = true
			case strings.HasPrefix(line, "@@"):
				inHunks = true
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			return false, 0, false
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	if whole {
		return true, removed, true
	}
	if binary || removed == 0 {
		return false, 0, false
	}
	return false, removed, true
}

// ChangedFile is one entry of a change's tree listing: a path the change
// touches, and the facts about it a patch does not carry. It exists because a
// reviewer shown a text diff is shown nothing of a binary, and a listing that
// names the file and its size is the evidence that the file is there — which,
// until this was recorded, a reviewer inferred from a link checker passing.
type ChangedFile struct {
	Path string `json:"path"`
	// Status is Git's own letter for the change — A, M, D, T — or "??" for a
	// file the worktree holds that nothing has added yet.
	Status string `json:"status"`
	// Bytes is the file's size at the tip of the change, as it is in the
	// worktree. It is zero for a deleted file.
	Bytes int64 `json:"bytes"`
	// Binary reports content Git will not render as a textual diff.
	Binary bool `json:"binary,omitempty"`
	// Committed reports that the path is changed by a commit already on the
	// branch — work an earlier attempt published — rather than only by what
	// is still uncommitted in the worktree.
	Committed bool `json:"committed,omitempty"`
	// Class is what kind of file it is, decided from the path: the listing is
	// where a reader sees how much of a change is code and how much is the test
	// data beside it.
	Class FileClass `json:"class,omitempty"`
}

// Describe is the one line a reader is given about a file in the listing.
func (f ChangedFile) Describe() string {
	var qualities []string
	if f.Class != "" && f.Class != FileClassSource {
		qualities = append(qualities, f.Class.Describe())
	}
	if f.Binary {
		qualities = append(qualities, "binary")
	}
	if f.Committed {
		qualities = append(qualities, "already committed on this branch")
	}
	line := fmt.Sprintf("%s %s (%d bytes)", f.Status, f.Path, f.Bytes)
	if len(qualities) > 0 {
		line += " — " + strings.Join(qualities, ", ")
	}
	return line
}

var (
	runIDPattern    = regexp.MustCompile(`^run-[a-f0-9]{32}$`)
	workItemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	commitPattern   = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
	remotePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func New(options Options) (*Manager, error) {
	if options.Runner == nil {
		return nil, errors.New("Git process runner is required")
	}
	repositoryRoot, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	worktreeRoot, err := filepath.Abs(options.WorktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root: %w", err)
	}
	worktreeRoot, err = canonicalizeFuturePath(worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root symlinks: %w", err)
	}
	if isFilesystemRoot(worktreeRoot) {
		return nil, errors.New("worktree root cannot be a filesystem root")
	}
	// The message names both roots and what to do, because whoever reads it is
	// usually standing in one of them: a verb run from inside a managed worktree
	// resolves its repository to that worktree, and the answer there is to run it
	// from the checkout the worktree was added from.
	if containsPath(repositoryRoot, worktreeRoot) || containsPath(worktreeRoot, repositoryRoot) {
		return nil, fmt.Errorf("the repository %s and the worktree root %s must not contain one another;"+
			" run yoyo from the checkout the worktrees were added from, or set execution.worktree_root to a directory outside the repository",
			repositoryRoot, worktreeRoot)
	}
	binary := options.GitBinary
	if binary == "" {
		binary = "git"
	}
	remote := options.Remote
	if remote == "" {
		remote = defaultRemote
	}
	if !remotePattern.MatchString(remote) {
		return nil, fmt.Errorf("remote %q must be a plain Git remote name", remote)
	}
	// An unnamed push remote is the ordinary arrangement rather than a missing
	// setting: the run branch goes to the same remote the work is published into.
	pushRemote := strings.TrimSpace(options.PushRemote)
	if pushRemote == "" {
		pushRemote = remote
	}
	if !remotePattern.MatchString(pushRemote) {
		return nil, fmt.Errorf("push remote %q must be a plain Git remote name", options.PushRemote)
	}
	if options.Timeout < 0 {
		return nil, fmt.Errorf("timeout %s must not be negative", options.Timeout)
	}
	allowedPrimaryChanges := make(map[string]struct{}, len(options.AllowedPrimaryChanges))
	for _, path := range options.AllowedPrimaryChanges {
		clean := filepath.Clean(path)
		if filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("allowed primary change %q must be a repository-relative file path", path)
		}
		allowedPrimaryChanges[filepath.ToSlash(clean)] = struct{}{}
	}
	currentExports, err := cleanExportPaths(options.CurrentExports)
	if err != nil {
		return nil, err
	}
	return &Manager{
		runner:                options.Runner,
		gitBinary:             binary,
		repositoryRoot:        repositoryRoot,
		worktreeRoot:          worktreeRoot,
		remote:                remote,
		pushRemote:            pushRemote,
		allowedPrimaryChanges: allowedPrimaryChanges,
		currentExports:        currentExports,
		prepareExports:        options.PrepareExports,
		timeout:               options.Timeout,
		note:                  options.Note,
	}, nil
}

// recordNote says what the manager worked around, where a caller asked to be
// told. A caller that asked for nothing is not a failure: the work still
// happened, and the note is what somebody reads afterwards rather than
// something the work depends on.
func (m *Manager) recordNote(format string, args ...any) {
	if m.note == nil {
		return
	}
	m.note(format, args...)
}

func (m *Manager) Create(ctx context.Context, request CreateRequest) (Worktree, error) {
	if err := validateCreateRequest(request); err != nil {
		return Worktree{}, err
	}
	if err := m.ValidateReady(ctx); err != nil {
		return Worktree{}, err
	}
	if request.ResumeCreation {
		if recovered, found, err := m.resumeCreation(ctx, request); found || err != nil {
			return recovered, err
		}
	}
	baseResult, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", request.BaseRef+"^{commit}")
	if err != nil {
		return Worktree{}, err
	}
	if baseResult.Status != execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("resolve base ref %s failed with exit code %d: %s", request.BaseRef, baseResult.ExitCode, strings.TrimSpace(baseResult.Stderr))
	}
	baseCommit := strings.TrimSpace(baseResult.Stdout)
	if !commitPattern.MatchString(baseCommit) {
		return Worktree{}, fmt.Errorf("resolved base commit %q is invalid", baseCommit)
	}
	// An integratable worktree must start from exactly the branch it will be
	// promoted into, so the recorded pair is coherent from the moment it is
	// written and integration can insist on that equality later.
	if request.TargetBranch != "" {
		targetCommit, err := m.resolveBranchCommit(ctx, request.TargetBranch)
		if err != nil {
			return Worktree{}, fmt.Errorf("resolve the target branch: %w", err)
		}
		if targetCommit != baseCommit {
			return Worktree{}, fmt.Errorf("target branch %s is at %s, which is not the base commit %s", request.TargetBranch, targetCommit, baseCommit)
		}
	}
	if err := os.MkdirAll(m.worktreeRoot, 0o700); err != nil {
		return Worktree{}, fmt.Errorf("create worktree root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(m.worktreeRoot)
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve worktree root symlinks: %w", err)
	}
	if resolvedRoot != m.worktreeRoot {
		return Worktree{}, errors.New("worktree root must not be a symlink")
	}

	branch := branchName(request.WorkItemID, request.RunID)
	path := filepath.Join(m.worktreeRoot, worktreeDirectoryName(request.WorkItemID, request.RunID))
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return Worktree{}, fmt.Errorf("worktree path already exists: %s", path)
		}
		return Worktree{}, fmt.Errorf("inspect worktree path: %w", err)
	}
	branchResult, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return Worktree{}, err
	}
	if branchResult.Status == execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("branch already exists: %s", branch)
	}
	if branchResult.ExitCode != 1 {
		return Worktree{}, fmt.Errorf("check branch %s failed with exit code %d: %s", branch, branchResult.ExitCode, strings.TrimSpace(branchResult.Stderr))
	}
	// How much tree the add below has to write, which is what its budget is sized
	// by. It is asked here rather than under the lease because it reads history
	// and touches no bookkeeping, and the lease is held for the creation rather
	// than for everything a creation happens to need.
	//
	// A count that could not be read is not a creation that fails: the add is
	// budgeted as an uncounted tree and runs. The one count that does stop the
	// creation is one the harness itself ended, which is the same machine-too-busy
	// death the add's own budget exists for and is refused in the same class —
	// see checkoutFiles.
	files, counted, err := m.checkoutFiles(ctx, baseCommit)
	if err != nil {
		return Worktree{}, err
	}

	// Development is parallel, and every run on this repository writes the same
	// worktree bookkeeping. Creation queues from here so a run is never lost to
	// another one's half-written registration; the lease is held through the
	// verification below, which reads that same bookkeeping.
	ctx, lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return Worktree{}, err
	}
	// Releasing is this process letting the next creation in, and the operating
	// system does it anyway when the process exits. A close that failed therefore
	// says nothing about the worktree below, which either exists or does not.
	defer func() { _ = lease.release() }()

	// A registration a killed add left half-written fails every creation on the
	// repository, and under the lease it cannot be a creation of ours in flight,
	// so it is cleared here rather than failed over — see settleRegistrations.
	// An entry that stays is said so, and the add below then fails over it
	// exactly as it would have.
	settled, err := m.settleRegistrations(ctx, true)
	if err != nil {
		return Worktree{}, fmt.Errorf("settle the worktree registrations before creating: %w", err)
	}
	for _, entry := range settled {
		m.recordNote("%s", entry.Describe())
	}

	// The branch is made first and the checkout added on it, rather than both in
	// one `worktree add -b`. That flag makes the branch before the add walks the
	// registrations, so an add that crossed a creation there and was run again
	// would meet its own branch and refuse over that instead. Made apart, the add
	// has done nothing before the walk, and running it again is running it.
	created, err := m.run(ctx, "-C", m.repositoryRoot, "branch", branch, baseCommit)
	if err != nil {
		return Worktree{}, err
	}
	if created.Status != execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("create branch %s failed with exit code %d: %s", branch, created.ExitCode, strings.TrimSpace(created.Stderr))
	}
	budget := m.checkoutTimeout(files, counted)
	result, err := m.runBounded(ctx, nil, budget, "-C", m.repositoryRoot, "worktree", "add", path, branch)
	if err != nil {
		// Splitting the two is what leaves a branch to take back: `worktree add
		// -b` made the branch and the checkout as one thing, and an add that
		// fails here has made a branch nothing will ever check out. It is removed
		// with the commit it was proven to be at a moment ago, so a branch
		// something else moved in between is kept rather than discarded, and
		// whatever came of that is said without displacing what actually failed.
		m.discardUncheckedOutBranch(ctx, branch, baseCommit)
		return Worktree{}, err
	}
	// An add the harness ended is said as the one thing a reader can act on, and
	// deliberately without the process output: what Git leaves on a killed
	// checkout is its own progress meter, which says how far it got and nothing
	// about why it stopped, and a failure carrying that is a failure nobody can
	// read. The budget and the size of the tree are what a reader needs, because
	// together they say whether the machine was too busy or the add was stuck.
	if killedCheckout(result) {
		m.discardUncheckedOutBranch(ctx, branch, baseCommit)
		return Worktree{}, fmt.Errorf("%w: %s was still running after %s, so no worktree was made and no agent of this run was invoked",
			ErrCheckoutKilled, describeCheckout(files, counted), budget)
	}
	if result.Status != execution.ProcessSucceeded {
		m.discardUncheckedOutBranch(ctx, branch, baseCommit)
		return Worktree{}, fmt.Errorf("create worktree failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	worktree := Worktree{
		RunID:        request.RunID,
		WorkItemID:   request.WorkItemID,
		Path:         path,
		Branch:       branch,
		BaseRef:      request.BaseRef,
		BaseCommit:   baseCommit,
		TargetBranch: request.TargetBranch,
	}
	// The worktree is cut from a commit, so anything in it that a store outside
	// Git is authoritative for is only as current as the last commit that carried
	// it. Those files are refreshed from the primary checkout here, before
	// anything has read one, and held out of the change this run will make.
	if err := m.refreshExports(ctx, path); err != nil {
		return worktree, fmt.Errorf("refresh the current exports in the created worktree: %w", err)
	}
	inspection, err := m.Inspect(ctx, worktree)
	if err != nil {
		return worktree, fmt.Errorf("verify created worktree: %w", err)
	}
	if !inspection.Registered || inspection.Branch != branch {
		return worktree, errors.New("created worktree is not registered with the expected branch")
	}
	// Nothing has been developed here yet, so a worktree that already carries a
	// change carries the refresh above. Proving it does not is what keeps a
	// fresher export from becoming a file every run commits and promotes.
	if inspection.Dirty {
		return worktree, errors.New("created worktree already carries uncommitted changes")
	}
	return worktree, nil
}

// CurrentBranch reports the local branch the primary checkout is on, which is
// the branch finished work is promoted back into. A detached HEAD names no
// branch, so it is refused rather than guessed at.
func (m *Manager) CurrentBranch(ctx context.Context) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		if result.ExitCode == 1 {
			return "", errors.New("primary checkout has no current branch; HEAD is detached")
		}
		return "", fmt.Errorf("resolve current branch failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	branch := strings.TrimSpace(result.Stdout)
	if err := validateTargetBranch(branch); err != nil {
		return "", err
	}
	return branch, nil
}

// PrimaryDirtyError is a refusal to start work because somebody's uncommitted
// changes are sitting in the primary checkout. It is a type rather than a
// sentence so a caller can tell it from a failure of whatever it was starting:
// the same condition refuses every other piece of work exactly as completely,
// and the item that happened to be chosen when it was met has nothing wrong with
// it.
//
// The paths travel with it because they are the whole of what somebody does
// about it. "The checkout is dirty" sends a person to `git status`; naming the
// file is the difference between a message that reports the stall and a message
// that ends it.
type PrimaryDirtyError struct {
	// Paths are the uncommitted changes, repository-relative, as Git reported
	// them and in the order it reported them.
	Paths []string
}

func (e PrimaryDirtyError) Error() string {
	return ErrPrimaryNotReady.Error() + ": primary repository has uncommitted changes: " + strings.Join(e.Paths, ", ")
}

func (e PrimaryDirtyError) Unwrap() error { return ErrPrimaryNotReady }

func (m *Manager) ValidateReady(ctx context.Context) error {
	if err := m.validateRepository(ctx); err != nil {
		return err
	}
	unexpected, err := m.unexpectedPrimaryChanges(ctx)
	if err != nil {
		return err
	}
	if len(unexpected) > 0 {
		// Named by the sentinel as well as in words: a caller deciding what a
		// refused round cost has to tell a dirty checkout from every other reason a
		// Git operation can refuse, and the message alone is not something to match
		// on. The repository failures above are deliberately not wrapped in it —
		// "there is no repository here" is not a checkout somebody left dirty.
		return PrimaryDirtyError{Paths: unexpected}
	}
	return nil
}

func (m *Manager) SummarizeChanges(ctx context.Context, worktree Worktree) (ChangeSummary, error) {
	path, _, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return ChangeSummary{}, err
	}
	return m.summarize(ctx, path, worktree)
}

// ChangedPaths lists every repository-relative path a worktree's change
// touches, tracked and untracked, against the recorded base commit. It is
// base-relative for the same reason the summary is: publishing commits each
// developer attempt, so a listing built from uncommitted status would report a
// published change as touching nothing.
//
// Rename detection is deliberately off. A rename reported as one entry names the
// path the file arrived at and says nothing about the one it left, and a caller
// deciding whether a change may touch a path has to see both sides: moving a
// document out of a protected home is exactly as much of an edit to that home as
// writing in it. Without detection Git reports the two halves as a deletion and
// an addition, which is what this needs.
//
// It only reads, and it is bounded by nothing: a listing of names is small even
// where the patch it describes is not, and a caller that gates on paths must see
// all of them rather than the first few hundred.
func (m *Manager) ChangedPaths(ctx context.Context, worktree Worktree) ([]string, error) {
	path, _, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return nil, err
	}
	// The NUL-separated form is used here for the reason it is used for untracked
	// files: a path containing a space, a quote, or a newline survives it, and
	// Git's quoted form would have to be parsed back.
	names, err := m.run(ctx, "-C", path, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", worktree.BaseCommit, "--")
	if err != nil {
		return nil, err
	}
	if names.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list changed worktree paths failed with exit code %d: %s", names.ExitCode, strings.TrimSpace(names.Stderr))
	}
	var changed []string
	for _, entry := range strings.Split(strings.TrimSuffix(names.Stdout, "\n"), "\x00") {
		if entry != "" {
			changed = append(changed, entry)
		}
	}
	untracked, err := m.untrackedFiles(ctx, path)
	if err != nil {
		return nil, err
	}
	changed = append(changed, untracked...)
	sort.Strings(changed)
	return changed, nil
}

// UnifiedChanges reports the actual change a developer produced, tracked and
// untracked, as one bounded patch. It only reads: the untracked half is built
// from `git diff --no-index` rather than from a staged index, so inspecting a
// worktree never changes what the developer left behind.
//
// Every part of it is measured against the recorded base commit rather than
// against the index, for the reason the summary beside it is: publishing commits
// each attempt, so a patch built from what happens to be uncommitted would hand
// a reviewer an empty change and a published branch full of work.
func (m *Manager) UnifiedChanges(ctx context.Context, worktree Worktree, limits DiffLimits) (ChangeDiff, error) {
	limits, err := limits.resolve()
	if err != nil {
		return ChangeDiff{}, err
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return ChangeDiff{}, err
	}
	summary, err := m.summarize(ctx, path, worktree)
	if err != nil {
		return ChangeDiff{}, err
	}
	changes := ChangeDiff{Status: summary.Status, DiffStat: summary.DiffStat}

	sections, err := m.trackedSections(ctx, path, worktree.BaseCommit, "")
	if err != nil {
		return ChangeDiff{}, err
	}
	untracked, err := m.untrackedFiles(ctx, path)
	if err != nil {
		return ChangeDiff{}, err
	}

	// Every file leaves the loop below one of two ways: written into the patch
	// whole, or recorded as an omission that names it, its size, and the bound
	// that dropped it. There is no third way out, and the accounting below
	// refuses a change where one appears — a file that is neither shown nor named
	// is exactly what a reviewer cannot know it is missing.
	//
	// The bound is spent file by file rather than cut at the bound. A patch cut
	// at a byte count keeps whichever files Git rendered first and loses the
	// rest with nothing naming them, so a reviewer handed one could not say which
	// files its verdict covered. Here a file's diff is in the patch whole or it
	// is named, and the scan carries on past a diff that does not fit so a
	// smaller file after it is still shown.
	//
	// It is spent in class order — source, then tests, then test data — over the
	// tracked and untracked halves together, so the bound falls on the tail of
	// the change rather than wherever the paths sort. Git lists both halves
	// alphabetically, and a change whose fixtures sort ahead of its code would
	// otherwise spend the whole bound on them (yoyodyne-ifd.141.3).
	candidates := make([]patchCandidate, 0, len(sections)+len(untracked))
	for _, section := range sections {
		candidates = append(candidates, patchCandidate{
			path: section.path, class: classifySection(section.path, section.patch), tracked: true, patch: section.patch,
		})
	}
	for _, relative := range untracked {
		candidates = append(candidates, patchCandidate{path: relative, class: m.classifyUntracked(path, relative)})
	}
	orderForPresentation(candidates)

	var patch strings.Builder
	remaining := limits.MaxTotalBytes
	// The file-count bound and the accounting below are over new files alone: a
	// tracked file the bound names is not one the untracked half owes.
	newFiles, omittedNew := 0, 0
	var reductions []reduction
	for _, candidate := range candidates {
		size, regular, err := m.untrackedSize(path, candidate.path)
		if err != nil {
			return ChangeDiff{}, err
		}
		// A file the bounds keep out is digested as well as measured, so what the
		// patch could not show is evidence somebody can open and prove rather than
		// a name and a byte count. Only a regular file the worktree still holds is
		// digested: a file the change deletes leaves nothing at the tip, which the
		// omission records as no digest and zero bytes.
		omit := func(reason OmissionReason, bound int64, diffBytes int) error {
			digest, undigestable := "", false
			switch {
			case regular:
				computed, err := m.fileDigest(path, candidate.path)
				if err != nil {
					return err
				}
				digest = computed
			case m.irregularAtTip(path, candidate.path):
				// Nothing regular is there to digest and the path is there all
				// the same: a symlink under a fixture directory, a socket, a
				// device. It is recorded rather than folded into the reason,
				// because the reason is which bound dropped the file and that is
				// still true of it — a tracked path that became a link has a
				// rendered diff the bound had no room for, and calling it
				// unreadable would say there was nothing to diff. What the flag
				// buys is the one thing the measurements cannot say: this omission
				// and a deletion both carry zero bytes and no digest, and only
				// this tells them apart for ListedWhole.
				undigestable = true
			}
			changes.OmittedFiles = append(changes.OmittedFiles, OmittedFile{
				Path: candidate.path, Bytes: size, Reason: reason, Class: candidate.class,
				Bound: bound, DiffBytes: int64(diffBytes), Digest: digest, Undigestable: undigestable,
			})
			changes.Truncated = true
			if !candidate.tracked {
				omittedNew++
			}
			return nil
		}
		if candidate.tracked {
			// A file deleted whole is described at the base rather than rendered:
			// its diff is its old content and nothing else. A file reduced by removal
			// alone is set aside and placed after every other file, so it is spent
			// only against what they leave and can never push one of them out.
			if whole, removed, ok := removalOnly(candidate.patch); ok {
				if !whole {
					reductions = append(reductions, reduction{candidate: candidate, removed: removed})
					continue
				}
				deleted, described, err := m.describeRemoval(ctx, worktree.BaseCommit, candidate, whole, removed, func() (int64, string, error) {
					if !regular {
						return size, "", nil
					}
					digest, err := m.fileDigest(path, candidate.path)
					return size, digest, err
				})
				if err != nil {
					return ChangeDiff{}, err
				}
				if described {
					changes.DeletedFiles = append(changes.DeletedFiles, deleted)
					continue
				}
			}
			var err error
			switch {
			case containsBinaryDiff(candidate.patch):
				err = omit(OmittedBinary, 0, len(candidate.patch))
			case len(candidate.patch) > limits.MaxTotalBytes:
				err = omit(OmittedTooLarge, int64(limits.MaxTotalBytes), len(candidate.patch))
			case len(candidate.patch) > remaining:
				err = omit(OmittedPatchFull, int64(limits.MaxTotalBytes), len(candidate.patch))
			default:
				patch.WriteString(candidate.patch)
				remaining -= len(candidate.patch)
			}
			if err != nil {
				return ChangeDiff{}, err
			}
			continue
		}
		newFiles++
		switch {
		case newFiles > limits.MaxFiles:
			if err := omit(OmittedTooManyFiles, int64(limits.MaxFiles), 0); err != nil {
				return ChangeDiff{}, err
			}
			continue
		case !regular:
			if err := omit(OmittedUnreadable, 0, 0); err != nil {
				return ChangeDiff{}, err
			}
			continue
		case size > int64(limits.MaxFileBytes):
			if err := omit(OmittedTooLarge, int64(limits.MaxFileBytes), 0); err != nil {
				return ChangeDiff{}, err
			}
			continue
		}
		filePatch, err := m.untrackedPatch(ctx, path, candidate.path)
		if err != nil {
			return ChangeDiff{}, err
		}
		switch {
		case containsBinaryDiff(filePatch):
			err = omit(OmittedBinary, 0, 0)
		case len(filePatch) > remaining:
			err = omit(OmittedPatchFull, int64(limits.MaxTotalBytes), 0)
		default:
			patch.WriteString(filePatch)
			remaining -= len(filePatch)
			changes.UntrackedFiles = append(changes.UntrackedFiles, candidate.path)
		}
		if err != nil {
			return ChangeDiff{}, err
		}
	}
	// The reductions set aside above are shown whole from what the bound has
	// left, and described at the base where it has too little: which lines went
	// is what a reviewer of a partial reduction reads when there is room, and
	// where there is not the reduction spends nothing and refuses nothing.
	for _, set := range reductions {
		if len(set.candidate.patch) <= remaining {
			patch.WriteString(set.candidate.patch)
			remaining -= len(set.candidate.patch)
			continue
		}
		deleted, err := m.describeReduction(ctx, worktree.BaseCommit, set, func() (int64, string, error) {
			size, regular, err := m.untrackedSize(path, set.candidate.path)
			if err != nil || !regular {
				return size, "", err
			}
			digest, err := m.fileDigest(path, set.candidate.path)
			return size, digest, err
		})
		if err != nil {
			return ChangeDiff{}, err
		}
		changes.DeletedFiles = append(changes.DeletedFiles, deleted)
	}
	if accounted := len(changes.UntrackedFiles) + omittedNew; accounted != len(untracked) {
		return ChangeDiff{}, fmt.Errorf("assembled change accounts for %d of %d new files; a file dropped without being named is not reviewable",
			accounted, len(untracked))
	}
	changes.Patch = patch.String()

	// The tree listing: every file the change touches, whether or not the patch
	// could show it. It is built after the patch so it can say what the patch
	// cannot — a binary's size, a file that is already committed — and it is
	// bounded on its own count rather than on the patch's bytes.
	changes.Files, changes.FilesOmitted, err = m.listChangedFiles(ctx, path, worktree.BaseCommit, head, untracked, limits.MaxListedFiles)
	if err != nil {
		return ChangeDiff{}, err
	}

	// What the patch spans, said rather than left to be inferred. The base is
	// what every part of the change above was measured against, the tip is the
	// HEAD it was read at, and the commits are the attempts already published
	// for it: together they say the patch covers the branch and not the tail of
	// it.
	changes.BaseCommit = worktree.BaseCommit
	changes.HeadCommit = head
	if head != worktree.BaseCommit {
		total, err := m.countCommits(ctx, worktree.BaseCommit, head)
		if err != nil {
			return ChangeDiff{}, err
		}
		changes.Commits, err = m.describeCommits(ctx, worktree.BaseCommit, head, limits.MaxCommits)
		if err != nil {
			return ChangeDiff{}, err
		}
		changes.CommitsOmitted = total - len(changes.Commits)
	}

	// An empty change over the base is evidence only once it says what the
	// commits above that base did. A published attempt's work lives in commits
	// here, so an empty patch under a HEAD that has moved reads two ways — a
	// change made and then undone, or a change the assembly failed to collect —
	// and a reviewer with no way to tell them apart reads the second, which is
	// the report yoyodyne-ifd.236 was filed on. The commits are the same ones
	// listed above; what this field adds is the sentence saying which emptiness
	// the patch is.
	//
	// A truncated change is left alone: a patch the bounds emptied is not a change
	// that came to nothing, and the reviewer is already told which of those it has.
	if head != worktree.BaseCommit && changes.Patch == "" && !changes.Truncated &&
		len(changes.UntrackedFiles) == 0 && len(changes.OmittedFiles) == 0 && len(changes.DeletedFiles) == 0 {
		changes.CommitsWithoutEffect = changes.Commits
	}
	return changes, nil
}

// VerifyOwnedHead proves one run's worktree is still exactly as the harness left
// it, and reports what stopped it where it is not. It adds nothing to the gate
// below: it is the same proof every read of a worktree's change already makes,
// reached by a caller that has to ask before it spends anything rather than as a
// side effect of asking for a diff.
//
// Re-entering a stopped run's repair loop is that caller. The change a developer
// is handed back is whatever is in that worktree now, so a worktree somebody has
// been operating on by hand — an operator mid-surgery, an agent that committed —
// is one where continuing would hand a developer work nothing recorded and then
// promote it. Refusing here is what makes that a person's decision.
func (m *Manager) VerifyOwnedHead(ctx context.Context, worktree Worktree) error {
	_, _, err := m.verifyOwnedHead(ctx, worktree)
	return err
}

// verifyOwnedHead confirms the worktree is the one the harness created and that
// its HEAD is exactly where the harness left it: the recorded base commit, or
// the one commit the harness itself made and recorded. It reports the path and
// that HEAD.
//
// Publishing is why this is not simply "HEAD never moved". A branch cannot be
// pushed before it carries a commit, so a published run has a harness commit on
// it well before integration. What makes that safe is that the permitted commit
// is named in advance by durable run state rather than recognized by how it
// looks: an agent with a shell in the worktree can imitate the harness identity,
// but it cannot make its commit be the hash the harness already wrote down.
func (m *Manager) verifyOwnedHead(ctx context.Context, worktree Worktree) (string, string, error) {
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return "", "", err
	}
	if worktree.HarnessCommit != "" && !commitPattern.MatchString(worktree.HarnessCommit) {
		return "", "", errors.New("recorded harness commit is invalid")
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return "", "", err
	}
	if !registered || branch != worktree.Branch {
		return "", "", errors.New("worktree is not registered with the expected branch")
	}
	commit, err := m.resolveWorktreeHead(ctx, path)
	if err != nil {
		return "", "", err
	}
	// Only one HEAD is permitted: the commit the harness recorded when it made
	// it, or the base when it has made none. A run that has published nothing
	// therefore permits no movement at all, which is the pre-publishing rule
	// unchanged.
	expected := worktree.BaseCommit
	if worktree.HarnessCommit != "" {
		expected = worktree.HarnessCommit
	}
	if commit != expected {
		return "", "", fmt.Errorf("%w: worktree HEAD is %s, want the commit the harness recorded (%s); Git commits are owned by the harness", ErrOwnedHeadMoved, commit, expected)
	}
	if commit == worktree.BaseCommit {
		return path, commit, nil
	}
	if err := m.verifyHarnessHistory(ctx, path, worktree.BaseCommit, commit); err != nil {
		return "", "", err
	}
	return path, commit, nil
}

// verifyHarnessHistory is the secondary assertion on a HEAD that already
// matched the commit durable state named: the commit descends from the recorded
// base, and it carries the harness identity. Neither check is what keeps an
// agent's commit out — the recorded hash does that — but a recorded commit that
// fails either one means the repository is not what the harness recorded, which
// is worth refusing rather than promoting.
func (m *Manager) verifyHarnessHistory(ctx context.Context, path, baseCommit, headCommit string) error {
	ancestor, err := m.run(ctx, "-C", path, "merge-base", "--is-ancestor", baseCommit, headCommit)
	if err != nil {
		return err
	}
	if ancestor.Status != execution.ProcessSucceeded {
		return errors.New("recorded harness commit does not descend from the worktree base; Git commits are owned by the harness")
	}
	identities, err := m.run(ctx, "-C", path, "log", "--format=%ae %ce", baseCommit+".."+headCommit)
	if err != nil {
		return err
	}
	if identities.Status != execution.ProcessSucceeded {
		return fmt.Errorf("inspect worktree commit identities failed with exit code %d: %s", identities.ExitCode, strings.TrimSpace(identities.Stderr))
	}
	expected := harnessCommitAuthorEmail + " " + harnessCommitAuthorEmail
	for _, line := range strings.Split(strings.TrimSuffix(identities.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		if line != expected {
			return errors.New("recorded harness commit does not carry the harness identity; Git commits are owned by the harness")
		}
	}
	return nil
}

// summarize describes the change against the worktree's recorded base commit.
// Every part of it is base-relative, including the file list: publishing
// commits each developer attempt, so a summary built from the working tree's
// uncommitted status would describe a published change as no change at all,
// printed beside a diff that plainly shows one. What a reviewer is handed has
// to be the change, not the part of it that happens not to be committed yet.
func (m *Manager) summarize(ctx context.Context, path string, worktree Worktree) (ChangeSummary, error) {
	names, err := m.run(ctx, "-C", path, "diff", "--name-status", "--no-ext-diff", worktree.BaseCommit, "--")
	if err != nil {
		return ChangeSummary{}, err
	}
	if names.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("summarize worktree status failed with exit code %d: %s", names.ExitCode, strings.TrimSpace(names.Stderr))
	}
	// Untracked files are not part of a diff against the base until something
	// adds them, so they are listed alongside it in the shape Git reports them.
	untracked, err := m.untrackedFiles(ctx, path)
	if err != nil {
		return ChangeSummary{}, err
	}
	diffStat, err := m.run(ctx, "-C", path, "diff", "--stat", "--no-ext-diff", worktree.BaseCommit, "--")
	if err != nil {
		return ChangeSummary{}, err
	}
	if diffStat.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("summarize worktree diff failed with exit code %d: %s", diffStat.ExitCode, strings.TrimSpace(diffStat.Stderr))
	}
	return ChangeSummary{
		Status:   renderChangedNames(names.Stdout, untracked),
		DiffStat: strings.TrimSpace(diffStat.Stdout),
	}, nil
}

// renderChangedNames turns Git's tab-separated name-status output and the
// untracked file list into one short-status-shaped listing, so a reader sees
// the same `<code> <path>` lines whether or not the change has been committed.
func renderChangedNames(nameStatus string, untracked []string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(nameStatus, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		switch len(fields) {
		case 0, 1:
			lines = append(lines, line)
		case 2:
			lines = append(lines, fields[0]+" "+fields[1])
		default:
			// A rename or copy names both paths, and which file became which is
			// exactly what a reviewer needs from a line like this.
			lines = append(lines, fields[0]+" "+fields[1]+" -> "+strings.Join(fields[2:], " "))
		}
	}
	for _, file := range untracked {
		lines = append(lines, "?? "+file)
	}
	return strings.Join(lines, "\n")
}

// untrackedFiles lists ignored-file-free untracked paths in a stable order.
// The NUL-separated form is used so paths containing spaces, quotes, or
// newlines survive without Git's quoting.
func (m *Manager) untrackedFiles(ctx context.Context, path string) ([]string, error) {
	result, err := m.run(ctx, "-C", path, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list untracked worktree files failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var files []string
	for _, entry := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\x00") {
		if entry != "" {
			files = append(files, entry)
		}
	}
	sort.Strings(files)
	return files, nil
}

// irregularAtTip reports a path the worktree still holds as something other
// than a regular file — a symlink, a socket, a device. It exists to tell two
// omissions apart that untrackedSize answers identically: a path that is gone
// and a path that is there and undigestable both measure zero and report
// regular=false, and only this says which. The difference decides an approval,
// because a change that deletes a fixture leaves nothing at the tip to digest
// and is completely listed without one, where a link leaves something nobody
// can open and is not.
//
// It refuses the same paths untrackedSize refuses, and follows nothing: Lstat
// asks about the link rather than its target, which is the whole question here.
func (m *Manager) irregularAtTip(path, relative string) bool {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	info, err := os.Lstat(filepath.Join(path, filepath.FromSlash(clean)))
	if err != nil {
		return false
	}
	return !info.Mode().IsRegular()
}

// untrackedSize measures one untracked file, and reports whether it is a
// regular file the harness can render at all. The size is read before any bound
// is applied, because the size is what a reader is told about a file the bounds
// then drop: a name with no size says a file is missing without saying whether
// showing it was ever possible. A path that is unsafe, gone, or not a regular
// file reports regular=false and a zero size, which the caller records as an
// omission rather than passing over.
func (m *Manager) untrackedSize(path, relative string) (int64, bool, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return 0, false, nil
	}
	info, err := os.Lstat(filepath.Join(path, filepath.FromSlash(clean)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("inspect untracked file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, false, nil
	}
	return info.Size(), true, nil
}

// fileDigest is the SHA-256 of one file in a worktree, as `sha256:<hex>`, for an
// omission record that has to say which bytes the patch could not show. It
// answers nothing for a path the change leaves nothing at, which is what a
// deletion leaves behind and is the one case a caller must not read as a failure.
//
// It refuses the same paths untrackedSize refuses, and for the same reason: a
// path that climbs out of the worktree is not part of the change whatever the
// listing said, and it is never followed here.
func (m *Manager) fileDigest(path, relative string) (string, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil
	}
	file, err := os.Open(filepath.Join(path, filepath.FromSlash(clean)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("digest omitted file: %w", err)
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", fmt.Errorf("digest omitted file: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sum.Sum(nil)), nil
}

// untrackedPatch renders one untracked file as a new-file patch. The caller has
// already measured it and decided it is within the bounds.
func (m *Manager) untrackedPatch(ctx context.Context, path, relative string) (string, error) {
	result, err := m.run(ctx, "-C", path, "diff", "--no-index", "--no-ext-diff", "--patch", "--", os.DevNull, filepath.Clean(relative))
	if err != nil {
		return "", err
	}
	// `git diff` exits 1 to report differences, which is the normal outcome
	// here because every untracked file differs from /dev/null.
	if result.Status != execution.ProcessSucceeded && result.ExitCode != 1 {
		return "", fmt.Errorf("diff untracked worktree file failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return result.Stdout, nil
}

// trackedSection is one file's part of a tracked patch: the path Git listed
// for it and the bytes of its own `diff --git` block, header included.
type trackedSection struct {
	path  string
	patch string
}

// patchCandidate is one file the bound is offered, from either half of a
// change. A tracked file arrives with its diff already rendered; an untracked
// file is rendered only once the per-file bounds have admitted it, so it
// arrives with its path and its class and no patch yet.
type patchCandidate struct {
	path    string
	class   FileClass
	tracked bool
	patch   string
}

// describeRemoval records a file whose change is removal alone as it was at the
// base commit. The base is read from the object store rather than from any
// checkout, so a worktree's change and a branch's are described the same way
// and the digest is the one `git rev-parse <base>:<path>` answers. atTip
// measures what is left of a file reduced rather than deleted, in the scope's
// own terms, and is not asked about a file deleted whole.
//
// It describes nothing where the base holds no blob at the path — a submodule
// is the case, which has no content to digest — and the caller then renders
// or omits the diff as it would any other.
func (m *Manager) describeRemoval(ctx context.Context, baseCommit string, candidate patchCandidate, whole bool, removed int, atTip func() (int64, string, error)) (DeletedFile, bool, error) {
	baseBytes, baseDigest, err := m.blobEntry(ctx, baseCommit, candidate.path)
	if err != nil || baseDigest == "" {
		return DeletedFile{}, false, err
	}
	deleted := DeletedFile{
		Path: candidate.path, Whole: whole, BaseCommit: baseCommit, BaseBytes: baseBytes, BaseDigest: baseDigest,
		RemovedLines: removed, DiffBytes: int64(len(candidate.patch)), Class: candidate.class,
	}
	if !whole {
		deleted.Bytes, deleted.Digest, err = atTip()
		if err != nil {
			return DeletedFile{}, false, err
		}
	}
	return deleted, true, nil
}

// reduction is a tracked file whose diff is removed lines alone and that the
// file remains after, held back until every other file has been placed.
type reduction struct {
	candidate patchCandidate
	removed   int
}

// describeReduction describes a reduction the bound had no room left for. The
// base always carries a blob for a file whose diff removes lines of text, so a
// reduction that cannot be described is refused rather than dropped unnamed.
func (m *Manager) describeReduction(ctx context.Context, baseCommit string, set reduction, atTip func() (int64, string, error)) (DeletedFile, error) {
	deleted, described, err := m.describeRemoval(ctx, baseCommit, set.candidate, false, set.removed, atTip)
	if err != nil {
		return DeletedFile{}, err
	}
	if !described {
		return DeletedFile{}, fmt.Errorf("%s is reduced from base commit %s, which holds no blob for it; a removal that cannot be described is not reviewable", set.candidate.path, baseCommit)
	}
	return deleted, nil
}

// orderForPresentation puts the candidates in the order the patch presents
// them: source first, then tests, then test data and generated files, and
// within a class in path order, which is the order Git gave. It is a stable
// sort on the class, so two files of one class keep the order they arrived in.
func orderForPresentation(candidates []patchCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].class.order() != candidates[j].class.order() {
			return candidates[i].class.order() < candidates[j].class.order()
		}
		return candidates[i].path < candidates[j].path
	})
}

// trackedSections renders the tracked change as one section per file, so the
// bounds can be spent a whole file at a time. An empty head is the working
// tree, which is what a worktree's change is measured to; a commit is the
// range a branch accumulated.
//
// The paths come from a second listing in the same order rather than from the
// `diff --git` headers, because Git quotes a header path that carries a tab, a
// newline, or a byte outside ASCII and leaves one with a space bare, and parsing
// that back is a second copy of Git's quoting rules. The listing and the patch
// are two renderings of the same diff under the same options, so they name the
// same files in the same order, with one shape to allow for: a type change — a
// file that became a symlink, or the reverse — is one `T` entry in the listing
// and two blocks in the patch, a deletion and a creation of the same path,
// which are one section here because they are one file's change. The pairing
// is checked rather than trusted, and a patch it cannot pair is refused.
func (m *Manager) trackedSections(ctx context.Context, dir, baseCommit, headCommit string) ([]trackedSection, error) {
	patchArgs := []string{"-C", dir, "diff", "--no-ext-diff", "--patch", baseCommit}
	namesArgs := []string{"-C", dir, "diff", "--no-ext-diff", "--name-status", "-z", baseCommit}
	if headCommit != "" {
		patchArgs = append(patchArgs, headCommit)
		namesArgs = append(namesArgs, headCommit)
	}
	patch, err := m.run(ctx, append(patchArgs, "--")...)
	if err != nil {
		return nil, err
	}
	if patch.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("diff tracked changes failed with exit code %d: %s", patch.ExitCode, strings.TrimSpace(patch.Stderr))
	}
	names, err := m.run(ctx, append(namesArgs, "--")...)
	if err != nil {
		return nil, err
	}
	if names.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list tracked changes failed with exit code %d: %s", names.ExitCode, strings.TrimSpace(names.Stderr))
	}
	entries := parseNameStatus(names.Stdout)
	blocks := splitPatchSections(patch.Stdout)
	sections := make([]trackedSection, 0, len(entries))
	next := 0
	for _, entry := range entries {
		take := 1
		if strings.HasPrefix(entry.status, "T") {
			take = 2
		}
		if next+take > len(blocks) {
			return nil, fmt.Errorf("tracked patch renders %d block(s) where the listing names %d file(s); a section that cannot be named is not reviewable", len(blocks), len(entries))
		}
		sections = append(sections, trackedSection{path: entry.path, patch: strings.Join(blocks[next:next+take], "")})
		next += take
	}
	if next != len(blocks) {
		return nil, fmt.Errorf("tracked patch renders %d block(s) where the listing names %d file(s); a section that cannot be named is not reviewable", len(blocks), len(entries))
	}
	return sections, nil
}

// nameStatus is one entry of Git's NUL-separated name-status listing. A rename
// or copy names both paths; path is the one the file arrived at, which is the
// one a patch section is headed by.
type nameStatus struct {
	status string
	path   string
}

// parseNameStatus reads `git diff --name-status -z`. Each entry is a status
// field, then one path, or two for a rename or copy, each NUL-terminated.
func parseNameStatus(output string) []nameStatus {
	fields := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	var entries []nameStatus
	for i := 0; i+1 < len(fields); {
		status := fields[i]
		path := fields[i+1]
		i += 2
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i < len(fields) {
				path = fields[i]
				i++
			}
		}
		if status == "" {
			continue
		}
		entries = append(entries, nameStatus{status: status, path: path})
	}
	return entries
}

// splitPatchSections cuts a `git diff --patch` rendering at each file's
// header. A content line is prefixed by a space, a plus, or a minus, so nothing
// inside a file's body can open a section.
func splitPatchSections(patch string) []string {
	var sections []string
	start := -1
	for offset := 0; offset < len(patch); {
		end := strings.IndexByte(patch[offset:], '\n')
		if end < 0 {
			end = len(patch)
		} else {
			end += offset + 1
		}
		if strings.HasPrefix(patch[offset:end], "diff --git ") {
			if start >= 0 {
				sections = append(sections, patch[start:offset])
			}
			start = offset
		}
		offset = end
	}
	if start >= 0 {
		sections = append(sections, patch[start:])
	}
	return sections
}

// listChangedFiles builds the tree listing of a change: every tracked path
// changed against the base, with Git's own status and binary reading, then
// every untracked file, each measured in the worktree at the tip. Rename
// detection is off for the reason it is off in ChangedPaths: a listing of what
// the change touches has to name both sides of a move.
func (m *Manager) listChangedFiles(ctx context.Context, path, baseCommit, headCommit string, untracked []string, limit int) ([]ChangedFile, int, error) {
	names, err := m.run(ctx, "-C", path, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", baseCommit, "--")
	if err != nil {
		return nil, 0, err
	}
	if names.Status != execution.ProcessSucceeded {
		return nil, 0, fmt.Errorf("list changed files failed with exit code %d: %s", names.ExitCode, strings.TrimSpace(names.Stderr))
	}
	// Git's numstat reports a binary file as "-\t-", which is its own reading of
	// the content rather than a guess made here from the name.
	numstat, err := m.run(ctx, "-C", path, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", baseCommit, "--")
	if err != nil {
		return nil, 0, err
	}
	if numstat.Status != execution.ProcessSucceeded {
		return nil, 0, fmt.Errorf("measure changed files failed with exit code %d: %s", numstat.ExitCode, strings.TrimSpace(numstat.Stderr))
	}
	binary := map[string]bool{}
	for _, entry := range strings.Split(strings.TrimSuffix(numstat.Stdout, "\x00"), "\x00") {
		fields := strings.SplitN(entry, "\t", 3)
		if len(fields) == 3 && fields[0] == "-" && fields[1] == "-" {
			binary[fields[2]] = true
		}
	}
	// What the branch's commits already carry, so a file an earlier attempt
	// published is told apart from one only the worktree holds.
	committed := map[string]bool{}
	if headCommit != baseCommit {
		published, err := m.run(ctx, "-C", path, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", baseCommit, headCommit, "--")
		if err != nil {
			return nil, 0, err
		}
		if published.Status != execution.ProcessSucceeded {
			return nil, 0, fmt.Errorf("list committed files failed with exit code %d: %s", published.ExitCode, strings.TrimSpace(published.Stderr))
		}
		for _, entry := range strings.Split(strings.TrimSuffix(published.Stdout, "\x00"), "\x00") {
			if entry != "" {
				committed[entry] = true
			}
		}
	}
	var files []ChangedFile
	for _, entry := range parseNameStatus(names.Stdout) {
		size, _, err := m.untrackedSize(path, entry.path)
		if err != nil {
			return nil, 0, err
		}
		files = append(files, ChangedFile{
			Path: entry.path, Status: entry.status, Bytes: size,
			Binary: binary[entry.path], Committed: committed[entry.path], Class: ClassifyPath(entry.path),
		})
	}
	for _, relative := range untracked {
		size, regular, err := m.untrackedSize(path, relative)
		if err != nil {
			return nil, 0, err
		}
		file := ChangedFile{Path: relative, Status: "??", Bytes: size, Class: ClassifyPath(relative)}
		if regular {
			file.Binary, err = sniffBinary(filepath.Join(path, filepath.FromSlash(filepath.Clean(relative))))
			if err != nil {
				return nil, 0, err
			}
		}
		files = append(files, file)
	}
	omitted := 0
	if len(files) > limit {
		omitted = len(files) - limit
		files = files[:limit]
	}
	return files, omitted, nil
}

// binarySniffBytes is how far into a file Git looks for a NUL before calling
// it binary, and so how far this looks too.
const binarySniffBytes = 8000

// sniffBinary reads a file the way Git decides a path with no attribute is
// binary: a NUL in its first eight thousand bytes. It is for untracked files,
// which Git has not measured; a tracked file's reading comes from Git itself.
func sniffBinary(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("inspect untracked file: %w", err)
	}
	defer file.Close()
	head := make([]byte, binarySniffBytes)
	n, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("inspect untracked file: %w", err)
	}
	return bytes.IndexByte(head[:n], 0) >= 0, nil
}

func (l DiffLimits) resolve() (DiffLimits, error) {
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = DefaultMaxDiffBytes
	}
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = DefaultMaxDiffFileBytes
	}
	if l.MaxFiles == 0 {
		l.MaxFiles = DefaultMaxDiffFiles
	}
	if l.MaxCommits == 0 {
		l.MaxCommits = DefaultMaxDiffCommits
	}
	if l.MaxListedFiles == 0 {
		l.MaxListedFiles = DefaultMaxListedFiles
	}
	if l.MaxTotalBytes < 0 || l.MaxFileBytes < 0 || l.MaxFiles < 0 || l.MaxCommits < 0 || l.MaxListedFiles < 0 {
		return DiffLimits{}, errors.New("diff limits cannot be negative")
	}
	return l, nil
}

// containsBinaryDiff detects Git's metadata-only representation of a binary
// change. Such a patch proves that a file changed but does not carry content an
// independent reviewer can evaluate, so the caller must treat it as truncated.
func containsBinaryDiff(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ") {
			return true
		}
	}
	return false
}

func (m *Manager) Inspect(ctx context.Context, worktree Worktree) (Inspection, error) {
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return Inspection{}, err
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Inspection{}, err
	}
	inspection := Inspection{Registered: registered, Branch: branch}
	if registered {
		inspection.Dirty, err = m.isDirty(ctx, path)
		if err != nil {
			return Inspection{}, err
		}
	}
	return inspection, nil
}

// Observe reports the current state of one run's recorded artifacts without
// changing any of them. Reconciliation needs this because durable run state
// says what a process intended and only the repository says what it achieved.
// Ownership is still proven first: the path is derived from the recorded
// identifiers rather than trusted, so this never reports on a directory or a
// branch that is not this run's.
func (m *Manager) Observe(ctx context.Context, worktree Worktree) (Observation, error) {
	path, err := m.ownedPath(worktree)
	if err != nil {
		return Observation{}, err
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Observation{}, err
	}
	observation := Observation{WorktreeRegistered: registered, WorktreeBranch: branch}
	info, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Observation{}, fmt.Errorf("inspect worktree path: %w", statErr)
	}
	observation.WorktreePresent = statErr == nil
	if observation.WorktreePresent && info.Mode()&os.ModeSymlink != 0 {
		return Observation{}, errors.New("worktree path must not be a symlink")
	}
	// A registration whose directory is gone has nothing left to read, so the
	// content of the checkout is only inspected while both are still there.
	if registered && observation.WorktreePresent {
		observation.WorktreeDirty, err = m.isDirty(ctx, path)
		if err != nil {
			return Observation{}, err
		}
		head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return Observation{}, err
		}
		if head.Status != execution.ProcessSucceeded {
			return Observation{}, fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
		}
		observation.WorktreeHead = strings.TrimSpace(head.Stdout)
	}

	observation.BranchCommit, observation.BranchExists, err = m.optionalBranchCommit(ctx, worktree.Branch)
	if err != nil {
		return Observation{}, err
	}
	if worktree.TargetBranch == "" {
		return observation, nil
	}
	if err := validateTargetBranch(worktree.TargetBranch); err != nil {
		return Observation{}, err
	}
	observation.TargetCommit, observation.TargetExists, err = m.optionalBranchCommit(ctx, worktree.TargetBranch)
	if err != nil {
		return Observation{}, err
	}
	if observation.TargetExists && observation.BranchExists && observation.BranchCommit != worktree.BaseCommit {
		observation.BranchIntegrated, err = m.contains(ctx, observation.BranchCommit, worktree.TargetBranch)
		if err != nil {
			return Observation{}, err
		}
	}
	return observation, nil
}

// Survival is which of one run's recorded artifacts are actually there: the
// branch in the repository, and the checkout on disk. It is the narrow half of
// an Observation, answered without reading either.
type Survival struct {
	BranchExists    bool `json:"branch_exists"`
	WorktreePresent bool `json:"worktree_present"`
}

// Any reports the run's change surviving in at least one of the two places.
func (s Survival) Any() bool { return s.BranchExists || s.WorktreePresent }

// Survives reports whether one run's branch and checkout still exist, looked at
// rather than read off the run's record. The record's removal flags are what a
// sweep or a cleanup remembered to write, and a hold decided from them alone
// released yoyodyne-ifd.372 on 2026-09-19 as no longer preserved; this is the
// check the hold is decided from instead. Ownership is proven first, as Observe
// proves it, so this never reports on a directory or a branch that is not the
// run's — and it stops there: one stat and one ref lookup, because it is asked
// of every stopped run on every pull.
func (m *Manager) Survives(ctx context.Context, worktree Worktree) (Survival, error) {
	path, err := m.ownedPath(worktree)
	if err != nil {
		return Survival{}, err
	}
	_, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Survival{}, fmt.Errorf("inspect worktree path: %w", statErr)
	}
	survival := Survival{WorktreePresent: statErr == nil}
	_, survival.BranchExists, err = m.optionalBranchCommit(ctx, worktree.Branch)
	if err != nil {
		return Survival{}, err
	}
	return survival, nil
}

// optionalBranchCommit resolves a branch that may legitimately be gone, which
// is the ordinary case after cleanup deleted the run's branch.
func (m *Manager) optionalBranchCommit(ctx context.Context, branch string) (string, bool, error) {
	existing, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return "", false, err
	}
	switch {
	case existing.ExitCode == 1:
		return "", false, nil
	case existing.Status != execution.ProcessSucceeded:
		return "", false, fmt.Errorf("check branch %s failed with exit code %d: %s", branch, existing.ExitCode, strings.TrimSpace(existing.Stderr))
	}
	commit, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return "", false, err
	}
	return commit, true, nil
}

// contains reports whether a commit is already part of a branch's history. Any
// answer other than a clean yes is reported as no, because containment is what
// permits destructive follow-up work and an unclear answer must never authorize
// it.
func (m *Manager) contains(ctx context.Context, commit, branch string) (bool, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", commit, "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	return result.Status == execution.ProcessSucceeded, nil
}

// CommitAttempt records whatever one developer invocation left in the worktree
// as a harness-owned commit, and reports the commit the branch stands at
// afterwards. It is the local half of what PublishBranch used to do on its own,
// separated from the push because committing is what every run does and pushing
// is what a publishing one does: a local project's branch tip lagged its
// worktree by a whole run, and a publishing one lagged it by every invocation
// the provider did not end cleanly.
//
// A worktree holding nothing above its base is ErrNoChanges, the same answer
// PublishBranch gives: an invocation that wrote nothing has nothing to record,
// which is not a failure. A worktree that is clean above a commit the harness
// already made is not that — the commit is reported, because the branch does
// carry the attempt.
func (m *Manager) CommitAttempt(ctx context.Context, worktree Worktree, message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return "", err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return "", err
	}
	if !dirty {
		if head == worktree.BaseCommit {
			return "", ErrNoChanges
		}
		return head, nil
	}
	commit, err := m.commitWorktree(ctx, path, message)
	// A dirty worktree with nothing stageable in it — the exports the manager
	// holds out of the change are the case — is the branch as it already stands
	// rather than a failure, and it is ErrNoChanges only where the branch stands
	// at its base.
	if errors.Is(err, ErrNoChanges) {
		if head == worktree.BaseCommit {
			return "", ErrNoChanges
		}
		return head, nil
	}
	if err != nil {
		return "", err
	}
	if commit == worktree.BaseCommit {
		return "", ErrNoChanges
	}
	return commit, nil
}

// Integrate promotes an already checked and approved worktree into its
// recorded target branch. The harness owns every Git write here: it commits
// whatever the developer left uncommitted, revalidates the target, and advances
// it with a fast-forward-only update. Nothing is forced, reset, or removed. When
// any step refuses, the worktree and its harness commits stay exactly as they
// are so the failure can be reconciled rather than reconstructed.
//
// A published run arrives here with its work already in harness commits on the
// run branch, because a branch cannot be pushed before it has one. That changes
// nothing about the promotion: the source commit is the branch tip either way,
// and the target is still only ever fast-forwarded onto it.
func (m *Manager) Integrate(ctx context.Context, worktree Worktree, message string) (Integration, error) {
	attempted, inPrimary, err := m.prepareIntegration(ctx, worktree, message, true)
	if err != nil {
		return attempted, err
	}
	if err := m.fastForward(ctx, worktree.Branch, attempted.TargetBranch, attempted.PreviousTargetCommit, attempted.SourceCommit, inPrimary); err != nil {
		return attempted, err
	}
	integrated, err := m.resolveBranchCommit(ctx, attempted.TargetBranch)
	if err != nil {
		return attempted, err
	}
	if integrated != attempted.SourceCommit {
		return attempted, fmt.Errorf("%w: %s is at %s after the update, want %s", ErrNotFastForward, attempted.TargetBranch, integrated, attempted.SourceCommit)
	}
	attempted.TargetCommit = integrated
	return attempted, nil
}

// PrepareLanding is Integrate for a target branch the forge protects: every
// check Integrate makes before it moves anything, and the commit of whatever
// the developer left, and then nothing more. The local target branch is not
// moved. The change reaches the target through the pull request that carries it,
// and the primary checkout's copy of the target only ever follows the forge,
// by a fast-forward onto what the remote has.
//
// The target must still stand at the worktree's recorded base, exactly as for a
// promotion: a target that moved is ErrTargetDrift, which is what sends a run to
// replay its change onto where the target went rather than asking the forge to
// merge a change written against somewhere else.
//
// What is reported is the same shape Integrate reports, marked ThroughPullRequest,
// with TargetCommit naming the commit being landed.
func (m *Manager) PrepareLanding(ctx context.Context, worktree Worktree, message string) (Integration, error) {
	attempted, _, err := m.prepareIntegration(ctx, worktree, message, false)
	if err != nil {
		return attempted, err
	}
	attempted.TargetCommit = attempted.SourceCommit
	attempted.ThroughPullRequest = true
	return attempted, nil
}

// prepareIntegration is what Integrate and PrepareLanding share: the ownership
// and drift checks and the harness commit. A local promotion also has the
// primary checkout to answer for, which is ready and not holding the target
// elsewhere; a landing through a pull request writes nothing there, so it asks
// neither, and the catch-up that later moves the checkout makes its own checks.
func (m *Manager) prepareIntegration(ctx context.Context, worktree Worktree, message string, local bool) (Integration, bool, error) {
	target := worktree.TargetBranch
	if err := validateTargetBranch(target); err != nil {
		return Integration{}, false, err
	}
	if target == worktree.Branch {
		return Integration{}, false, errors.New("target branch must differ from the worktree branch")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}

	// Ownership, registration, the expected branch, and a HEAD carrying only
	// harness commits are all revalidated here rather than trusted from run
	// state.
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Integration{}, false, err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Integration{}, false, err
	}
	if !dirty && head == worktree.BaseCommit {
		return Integration{}, false, ErrNoChanges
	}
	if local {
		if err := m.ValidateReady(ctx); err != nil {
			return Integration{}, false, fmt.Errorf("primary checkout is not ready for integration: %w", err)
		}
	}
	previousTarget, err := m.resolveBranchCommit(ctx, target)
	if err != nil {
		return Integration{}, false, err
	}
	if previousTarget != worktree.BaseCommit {
		return Integration{}, false, fmt.Errorf("%w: %s is at %s, recorded base is %s", ErrTargetDrift, target, previousTarget, worktree.BaseCommit)
	}
	inPrimary := false
	if local {
		inPrimary, err = m.targetCheckout(ctx, target)
		if err != nil {
			return Integration{}, false, err
		}
	}

	sourceCommit := head
	if dirty {
		sourceCommit, err = m.commitWorktree(ctx, path, message)
		if err != nil {
			return Integration{}, false, err
		}
	}
	if sourceCommit == worktree.BaseCommit {
		return Integration{}, false, ErrNoChanges
	}
	// From here the harness may already have made a commit, which exists whether
	// or not the promotion that follows succeeds. It is reported alongside a refusal for
	// the reason PublishBranch reports its own: a caller that could not learn of
	// it would be left holding a worktree at a HEAD nothing recorded, which is the
	// one state the ownership check has to be able to tell from an agent's commit.
	attempted := Integration{
		Branch:               worktree.Branch,
		TargetBranch:         target,
		SourceCommit:         sourceCommit,
		PreviousTargetCommit: previousTarget,
	}
	return attempted, inPrimary, nil
}

// RebaseOntoTarget replays a run's change onto wherever its target branch is
// now, so a promotion that lost a race to another run — or to an operator who
// moved the target mid-run — can be attempted again against what the target
// actually became. Nothing about the promotion rule is relaxed by it: the
// change is moved to the target rather than the target to the change, and the
// retried promotion is still the same fast-forward onto a base the worktree
// records.
//
// Only the run's own branch is rewritten, and only into commits the harness
// authors. A conflict is refused rather than resolved: the replay is aborted,
// the branch is left exactly where it was, and ErrRebaseConflict is what the
// caller reports to whoever owns that decision. A replay the harness killed —
// timed out, cancelled, or stalled — is abandoned the same way and reported as
// ErrReplayKilled instead, because nobody has a decision to make about it.
func (m *Manager) RebaseOntoTarget(ctx context.Context, worktree Worktree, message string) (Rebase, error) {
	target := worktree.TargetBranch
	if err := validateTargetBranch(target); err != nil {
		return Rebase{}, err
	}
	if target == worktree.Branch {
		return Rebase{}, errors.New("target branch must differ from the worktree branch")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Rebase{}, err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Rebase{}, err
	}
	if !dirty && head == worktree.BaseCommit {
		return Rebase{}, ErrNoChanges
	}
	targetCommit, err := m.resolveBranchCommit(ctx, target)
	if err != nil {
		return Rebase{}, err
	}
	// A replay needs the work in commits, and a promotion that was refused before
	// it committed anything leaves the developer's change in the working tree. It
	// is committed here for exactly the reason integration commits it, under the
	// same harness identity.
	source := head
	if dirty {
		source, err = m.commitWorktree(ctx, path, message)
		if err != nil {
			return Rebase{}, err
		}
	}
	// Everything from here reports the commit the harness owns, whichever way the
	// replay goes, so the caller can record it and keep accepting this worktree.
	rebase := Rebase{
		Branch:             worktree.Branch,
		TargetBranch:       target,
		BaseCommit:         worktree.BaseCommit,
		HeadCommit:         source,
		PreviousBaseCommit: worktree.BaseCommit,
		PreviousHeadCommit: source,
	}
	if source == worktree.BaseCommit {
		return rebase, ErrNoChanges
	}
	if targetCommit == worktree.BaseCommit {
		// The target never moved, so there is nothing to replay onto. Reporting it
		// as a no-op rather than a failure is what lets a promotion that lost to a
		// held index lock rather than to a commit simply be retried.
		return rebase, nil
	}
	if err := m.replay(ctx, path, worktree, targetCommit); err != nil {
		return rebase, err
	}
	replayed, err := m.resolveWorktreeHead(ctx, path)
	if err != nil {
		return rebase, err
	}
	if replayed != targetCommit {
		if err := m.verifyHarnessHistory(ctx, path, targetCommit, replayed); err != nil {
			return rebase, err
		}
	}
	rebase.BaseCommit = targetCommit
	rebase.HeadCommit = replayed
	return rebase, nil
}

// replay runs the rebase itself and refuses to leave a half-applied one behind.
// The commits it writes carry the harness identity for the same reason every
// other commit the harness makes does, and the abort is what keeps a conflict
// from becoming a worktree nobody can promote or inspect afterwards.
//
// Only a replay that actually began and was then abandoned is reported as a
// conflict. Git refuses a rebase it never started with the same exit code — an
// unusable revision or an unusable worktree looks exactly like a conflict from
// the outside — and reporting one of those as a conflict would send a person to
// settle a disagreement that does not exist. An abort that fails is not
// reported as a conflict either, for the other half of the same reason: what
// makes a conflict safe to hand over is that the branch and the worktree were
// put back, and that is precisely what did not happen.
//
// Nor is a replay the harness itself ended. A rebase killed part-way leaves the
// same state directory a conflicted one does, so it is told apart by how the
// process stopped before that directory is ever asked about, and it is reported
// as ErrReplayKilled once the worktree is back on its branch.
func (m *Manager) replay(ctx context.Context, path string, worktree Worktree, targetCommit string) error {
	// A refreshed export is a path this worktree's index has been told to leave
	// alone, and Git refuses to move a HEAD across one. The branch's own copies go
	// back first so the replay sees an ordinary worktree, and they are refreshed
	// again below however the replay went.
	if err := m.restoreExports(ctx, path); err != nil {
		return fmt.Errorf("put the current exports back before the replay: %w", err)
	}
	// A refresh that fails after the replay leaves the branch's own exports in
	// place, which is what this worktree held before any of this existed: older
	// than the store rather than wrong. Failing the replay over it would throw
	// away work that landed, so it is not reported here.
	defer func() { _ = m.refreshExports(ctx, path) }()
	rebased, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"rebase", "--no-gpg-sign", "--onto", targetCommit, worktree.BaseCommit, worktree.Branch)
	if err != nil {
		return err
	}
	if rebased.Status == execution.ProcessSucceeded {
		return nil
	}
	if stopped, killed := killedReplay(rebased.Status); killed {
		return m.abandonKilledReplay(ctx, path, worktree, targetCommit, stopped)
	}
	failed := fmt.Errorf("replay %s onto %s at %s failed with exit code %d: %s",
		worktree.Branch, worktree.TargetBranch, targetCommit, rebased.ExitCode, strings.TrimSpace(rebased.Stderr))
	started, err := m.replayInProgress(ctx, path)
	if err != nil {
		return errors.Join(failed, err)
	}
	if !started {
		// Nothing was applied, so there is nothing to abandon and nothing anybody
		// has to choose between: the branch is where it was, and the failure is
		// Git's own.
		return failed
	}
	// Read before the abort, because the abort is what removes it. A listing that
	// could not be taken names no paths rather than failing the report: what makes
	// this a conflict is that a replay began and was abandoned, which is already
	// established, and a conflict named without its paths is still one somebody
	// can settle.
	conflicted, _ := m.conflictedPaths(ctx, path)
	aborted, err := m.run(ctx, "-C", path, "rebase", "--abort")
	if err != nil {
		return errors.Join(failed, err)
	}
	if aborted.Status != execution.ProcessSucceeded {
		return errors.Join(failed, fmt.Errorf("abandon the replay failed with exit code %d: %s; the worktree is left part-way through it",
			aborted.ExitCode, strings.TrimSpace(aborted.Stderr)))
	}
	return &RebaseConflict{
		Branch:       worktree.Branch,
		TargetBranch: worktree.TargetBranch,
		TargetCommit: targetCommit,
		Paths:        conflicted,
		Err:          failed,
	}
}

// conflictedPaths lists what Git left unmerged in a half-applied replay or
// apply, which is the nearest thing to a statement of what the two sides
// disagree about.
func (m *Manager) conflictedPaths(ctx context.Context, path string) ([]string, error) {
	listed, err := m.run(ctx, "-C", path, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if listed.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the unmerged paths failed with exit code %d: %s", listed.ExitCode, strings.TrimSpace(listed.Stderr))
	}
	var paths []string
	for _, line := range strings.Split(listed.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	return paths, nil
}

// ReplayForRepair moves a change whose replay was refused onto the target
// anyway, and leaves the disagreement in the worktree for whoever wrote the
// change to settle. It exists because the refusal cannot be answered from the
// base the change still sits on: a resolution written there is another change
// to lines the target already changed, so replaying it conflicts exactly as the
// first attempt did, however carefully it was written. The only place the two
// answers can be reconciled is on top of the target.
//
// Nothing is resolved here and nothing is chosen. Git's own three-way apply puts
// every hunk that fits where it goes and writes the ones that do not as
// conflict markers — the state a person resolving a rebase by hand works in.
// Unlike a rebase left half-applied it is an ordinary dirty worktree: no
// operation is in progress, HEAD is the target commit the caller records as the
// run's new base, and a process that dies here leaves something the next one
// can read.
//
// The change is applied as one commit rather than as the commits the run
// happened to make, because a branch that published several attempts replays
// each of them in turn, and the first one conflicting refuses the whole replay
// even where the change as a whole would apply.
//
// What the move drops is only this run's own commits, whose content is
// re-applied over the target as uncommitted work; the target keeps every commit
// it has, and nothing is force-merged, chosen between, or pushed anywhere.
func (m *Manager) ReplayForRepair(ctx context.Context, worktree Worktree, message string) (Rebase, error) {
	target := worktree.TargetBranch
	if err := validateTargetBranch(target); err != nil {
		return Rebase{}, err
	}
	if target == worktree.Branch {
		return Rebase{}, errors.New("target branch must differ from the worktree branch")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		resumed, recovered := m.resumeInterruptedMove(ctx, worktree)
		if !recovered {
			return Rebase{}, err
		}
		path, head = resumed, worktree.HarnessCommit
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Rebase{}, err
	}
	// The change has to be in a commit to be applied over anything. A refused
	// replay has already committed it, so this ordinarily finds nothing to do.
	source := head
	if dirty {
		source, err = m.commitWorktree(ctx, path, message)
		if err != nil {
			return Rebase{}, err
		}
	}
	rebase := Rebase{
		Branch:             worktree.Branch,
		TargetBranch:       target,
		BaseCommit:         worktree.BaseCommit,
		HeadCommit:         source,
		PreviousBaseCommit: worktree.BaseCommit,
		PreviousHeadCommit: source,
	}
	if source == worktree.BaseCommit {
		return rebase, ErrNoChanges
	}
	targetCommit, err := m.resolveBranchCommit(ctx, target)
	if err != nil {
		return rebase, err
	}
	if targetCommit == worktree.BaseCommit {
		// The target is back where this change was written, so there is nothing to
		// reconcile it with, and the change is left exactly where it is.
		return rebase, nil
	}
	squashed, err := m.squashChange(ctx, path, worktree.BaseCommit, source, message)
	if err != nil {
		return rebase, err
	}
	// A refreshed export is a path this worktree's index has been told to leave
	// alone, and Git refuses to move a HEAD across one — the same reason replay
	// puts the branch's own copies back first and refreshes them afterwards.
	if err := m.restoreExports(ctx, path); err != nil {
		return rebase, fmt.Errorf("put the current exports back before moving onto the target: %w", err)
	}
	defer func() { _ = m.refreshExports(ctx, path) }()
	if err := m.applyOverTarget(ctx, path, targetCommit, source, squashed); err != nil {
		return rebase, err
	}
	rebase.BaseCommit = targetCommit
	rebase.HeadCommit = targetCommit
	return rebase, nil
}

// resumeInterruptedMove recognises the one worktree state a move of this kind
// leaves that the ownership check refuses: a process that died after the
// worktree was put on the target and before the caller recorded the move. The
// recorded state still names the harness commit the change was in, while HEAD
// sits on the target's history with some or all of the change applied over it.
//
// Nobody has worked in that worktree since — the developer is only invoked once
// the move is recorded — so whatever is there beyond the target is this
// function's own interrupted apply, and the recorded commit still holds the
// whole change. The worktree is put back on that commit, which is exactly the
// state the move starts from, and the move is made again. Anything else — no
// recorded commit, a HEAD off the target's history, a recorded commit that is
// not the harness's above the base — is refused as it always was, to a person.
func (m *Manager) resumeInterruptedMove(ctx context.Context, worktree Worktree) (string, bool) {
	if worktree.HarnessCommit == "" || !commitPattern.MatchString(worktree.HarnessCommit) {
		return "", false
	}
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return "", false
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil || !registered || branch != worktree.Branch {
		return "", false
	}
	head, err := m.resolveWorktreeHead(ctx, path)
	if err != nil || head == worktree.HarnessCommit {
		return "", false
	}
	if onTarget, err := m.contains(ctx, head, worktree.TargetBranch); err != nil || !onTarget {
		return "", false
	}
	if err := m.verifyHarnessHistory(ctx, path, worktree.BaseCommit, worktree.HarnessCommit); err != nil {
		return "", false
	}
	if err := m.restoreExports(ctx, path); err != nil {
		return "", false
	}
	// An apply stopped part-way can leave its own state behind; clearing it is
	// best-effort because the hard reset below discards what it describes.
	_ = m.quitCherryPick(ctx, path)
	if err := m.resetHard(ctx, path, worktree.HarnessCommit); err != nil {
		return "", false
	}
	return path, true
}

// squashChange records the whole of a run's change as one harness commit above
// the base without touching the branch. It is built from the source commit's
// tree rather than from a patch, because a tree is what the run's work actually
// is, and renames, modes, and binary content survive Git's own merge more
// reliably than a round trip through a diff.
func (m *Manager) squashChange(ctx context.Context, path, base, source, message string) (string, error) {
	written, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit-tree", "--no-gpg-sign", source+"^{tree}", "-p", base, "-m", message)
	if err != nil {
		return "", err
	}
	if written.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("record the change as one commit failed with exit code %d: %s", written.ExitCode, strings.TrimSpace(written.Stderr))
	}
	commit := strings.TrimSpace(written.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("recorded squashed commit %q is invalid", commit)
	}
	return commit, nil
}

// applyOverTarget puts the branch and worktree on the target and applies the
// change over it, leaving whatever would not merge as conflict markers.
//
// The two things it will not leave behind are a Git operation in progress and an
// index that claims a resolution: the sequencer state a stopped apply writes is
// cleared, and the index is reset to the target, so the only thing carrying the
// change afterwards is the working tree. That is what makes this recoverable — a
// run that dies here is a run with a dirty worktree, which every other step
// already knows how to read.
//
// An apply that never started is not a conflict, and it is the one failure here
// that would otherwise lose work: the branch has already been moved, so the
// change would be left in a commit nothing points at. The branch is put back on
// the change before the failure is reported.
func (m *Manager) applyOverTarget(ctx context.Context, path, targetCommit, source, squashed string) error {
	if err := m.resetHard(ctx, path, targetCommit); err != nil {
		return err
	}
	picked, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"cherry-pick", "--no-commit", squashed)
	if err != nil {
		return errors.Join(err, m.resetHard(ctx, path, source))
	}
	if picked.Status != execution.ProcessSucceeded {
		failed := fmt.Errorf("apply %s over %s failed with exit code %d: %s",
			squashed, targetCommit, picked.ExitCode, strings.TrimSpace(picked.Stderr))
		conflicted, err := m.conflictedPaths(ctx, path)
		if err != nil {
			return errors.Join(failed, err, m.resetHard(ctx, path, source))
		}
		if len(conflicted) == 0 {
			// Git declined the apply rather than stopping inside it, so there is no
			// disagreement to hand anybody and nothing was written.
			return errors.Join(failed, m.resetHard(ctx, path, source))
		}
		// An apply that stopped inside itself can leave sequencer state behind,
		// which would make this an operation in progress rather than a dirty
		// worktree. Clearing it keeps what was written and nothing else.
		if err := m.quitCherryPick(ctx, path); err != nil {
			return errors.Join(failed, err)
		}
	}
	// Whatever the apply staged is unstaged, resolved and conflicting alike: a
	// resolution is the developer's to make, and an index that already recorded
	// one would hide from every later step which paths still hold a disagreement.
	unstaged, err := m.run(ctx, "-C", path, "reset", "--quiet")
	if err != nil {
		return err
	}
	if unstaged.Status != execution.ProcessSucceeded {
		return fmt.Errorf("unstage the applied change failed with exit code %d: %s", unstaged.ExitCode, strings.TrimSpace(unstaged.Stderr))
	}
	return nil
}

func (m *Manager) resetHard(ctx context.Context, path, commit string) error {
	result, err := m.run(ctx, "-C", path, "reset", "--hard", "--quiet", commit)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("move the worktree to %s failed with exit code %d: %s", commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// quitCherryPick clears any sequencer state a stopped apply left, keeping the
// working tree exactly as that apply wrote it. An apply asked not to commit
// ordinarily leaves none, and Git answers that case the same way, so this is
// asked unconditionally rather than only when there is something to clear.
func (m *Manager) quitCherryPick(ctx context.Context, path string) error {
	result, err := m.run(ctx, "-C", path, "cherry-pick", "--quit")
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("clear the stopped apply failed with exit code %d: %s; the worktree is left part-way through it",
			result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// killedReplay reports a rebase this harness ended rather than Git answering,
// and how it was ended, in the words the stop is recorded in. A cancelled one is
// in the class with the other two: nothing about the change stopped it.
func killedReplay(status execution.ProcessStatus) (string, bool) {
	switch status {
	case execution.ProcessTimedOut:
		return "timed out", true
	case execution.ProcessCancelled:
		return "was cancelled", true
	case execution.ProcessStalled:
		return "stalled", true
	default:
		return "", false
	}
}

// abandonKilledReplay puts a worktree back on its branch after the harness
// killed the rebase replaying it, and only then reports the stop. A rebase
// killed part-way leaves its state directory and a detached HEAD behind, and a
// worktree left like that is one nothing may promote or replay again.
//
// The clean-up runs outside the caller's cancellation, because a cancelled
// context is one of the ways a replay is killed and the clean-up is still owed
// then; it is still bounded by the local Git budget every command gets. An
// abort that fails is not reported as ErrReplayKilled, for the reason a failed
// abort is not reported as a conflict: what makes the stop safe to resume from
// is the worktree having been put back, and that is what did not happen.
func (m *Manager) abandonKilledReplay(ctx context.Context, path string, worktree Worktree, targetCommit, stopped string) error {
	account := fmt.Sprintf("replay %s onto %s at %s %s", worktree.Branch, worktree.TargetBranch, targetCommit, stopped)
	cleanup := context.WithoutCancel(ctx)
	started, err := m.replayInProgress(cleanup, path)
	if err != nil {
		return errors.Join(errors.New(account), err)
	}
	if started {
		aborted, err := m.run(cleanup, "-C", path, "rebase", "--abort")
		if err != nil {
			return errors.Join(errors.New(account), err)
		}
		if aborted.Status != execution.ProcessSucceeded {
			return fmt.Errorf("%s, and abandoning it failed with exit code %d: %s; the worktree is left part-way through it",
				account, aborted.ExitCode, strings.TrimSpace(aborted.Stderr))
		}
		if left, err := m.replayInProgress(cleanup, path); err != nil || left {
			return errors.Join(fmt.Errorf("%s, and its state directory survived the abort; the worktree is left part-way through it", account), err)
		}
	}
	// Killed before it wrote any state, a rebase has nothing to abort, and the
	// branch still being checked out is what says it also moved nothing.
	head, err := m.run(cleanup, "-C", path, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return errors.Join(errors.New(account), err)
	}
	if head.Status != execution.ProcessSucceeded || strings.TrimSpace(head.Stdout) != "refs/heads/"+worktree.Branch {
		return fmt.Errorf("%s, and the worktree was not back on %s afterwards (HEAD %q); the worktree is left part-way through it",
			account, worktree.Branch, strings.TrimSpace(head.Stdout))
	}
	return fmt.Errorf("%w: %s", ErrReplayKilled, account)
}

// replayInProgress reports whether Git left a rebase half-applied in this
// worktree. It is what tells a conflict from a rebase that never started, which
// the exit code alone cannot: Git keeps the state of an interrupted rebase in
// one of two directories under the worktree's Git directory, and a rebase it
// refused outright leaves neither.
func (m *Manager) replayInProgress(ctx context.Context, path string) (bool, error) {
	for _, state := range []string{"rebase-merge", "rebase-apply"} {
		result, err := m.run(ctx, "-C", path, "rev-parse", "--git-path", state)
		if err != nil {
			return false, err
		}
		if result.Status != execution.ProcessSucceeded {
			return false, fmt.Errorf("resolve the %s state directory failed with exit code %d: %s", state, result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		directory := strings.TrimSpace(result.Stdout)
		if directory == "" {
			return false, fmt.Errorf("resolved %s state directory is empty", state)
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(path, directory)
		}
		switch _, err := os.Stat(directory); {
		case err == nil:
			return true, nil
		case errors.Is(err, os.ErrNotExist):
		default:
			return false, fmt.Errorf("inspect the %s state directory: %w", state, err)
		}
	}
	return false, nil
}

// resolveWorktreeHead reads a worktree's HEAD without judging it, which is what
// a step that has just moved that HEAD on purpose needs.
func (m *Manager) resolveWorktreeHead(ctx context.Context, path string) (string, error) {
	head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if head.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
	}
	commit := strings.TrimSpace(head.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("resolved worktree HEAD %q is invalid", commit)
	}
	return commit, nil
}

// commitWorktree stages tracked edits, deletions, and untracked files, then
// records one harness-owned commit. The identity, hooks, and signing are all
// pinned so the commit does not depend on whatever the developer left in the
// worktree's Git configuration.
func (m *Manager) commitWorktree(ctx context.Context, path, message string) (string, error) {
	staged, err := m.run(ctx, "-C", path, "add", "--all")
	if err != nil {
		return "", err
	}
	if staged.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("stage worktree changes failed with exit code %d: %s", staged.ExitCode, strings.TrimSpace(staged.Stderr))
	}
	pending, err := m.run(ctx, "-C", path, "diff", "--cached", "--quiet")
	if err != nil {
		return "", err
	}
	switch {
	case pending.Status == execution.ProcessSucceeded:
		return "", ErrNoChanges
	case pending.ExitCode != 1:
		return "", fmt.Errorf("inspect staged worktree changes failed with exit code %d: %s", pending.ExitCode, strings.TrimSpace(pending.Stderr))
	}
	// Command-line config disables every repository hook, including post-commit
	// and reference-transaction hooks that --no-verify does not bypass. Explicit
	// environment values take precedence over ambient GIT_* identity variables.
	committed, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "--no-verify", "--no-gpg-sign", "--message", message)
	if err != nil {
		return "", err
	}
	if committed.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("commit worktree changes failed with exit code %d: %s", committed.ExitCode, strings.TrimSpace(committed.Stderr))
	}
	head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if head.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve integrated commit failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
	}
	commit := strings.TrimSpace(head.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("integrated commit %q is invalid", commit)
	}
	return commit, nil
}

// fastForward advances the target branch and refuses anything else. When the
// primary checkout is on the target, a fast-forward-only merge keeps its
// working tree consistent with the moved branch; otherwise the ref is advanced
// with a compare-and-swap so a target that drifted since the earlier check
// still loses the race instead of being overwritten.
func (m *Manager) fastForward(ctx context.Context, branch, target, previousTarget, sourceCommit string, inPrimary bool) error {
	if inPrimary {
		// The merge rewrites the primary checkout's working tree, so a readiness
		// read beside it waits rather than reading it half-moved: see
		// primarylease.go.
		ctx, release, err := m.leasePrimary(ctx)
		if err != nil {
			return err
		}
		defer release()
		// Merge the exact commit we just created, not the mutable source branch.
		// A concurrent ref update must never redirect approved integration work.
		merged, err := m.run(ctx, "-C", m.repositoryRoot,
			"-c", "core.hooksPath="+os.DevNull,
			"merge", "--ff-only", sourceCommit)
		if err != nil {
			return err
		}
		if merged.Status != execution.ProcessSucceeded {
			return fmt.Errorf("%w: merge %s from %s into %s failed with exit code %d: %s", ErrNotFastForward, sourceCommit, branch, target, merged.ExitCode, strings.TrimSpace(merged.Stderr))
		}
		return nil
	}
	updated, err := m.run(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", "refs/heads/"+target, sourceCommit, previousTarget)
	if err != nil {
		return err
	}
	if updated.Status != execution.ProcessSucceeded {
		return fmt.Errorf("%w: update %s to %s failed with exit code %d: %s", ErrNotFastForward, target, sourceCommit, updated.ExitCode, strings.TrimSpace(updated.Stderr))
	}
	return nil
}

// targetCheckout reports whether the target branch is checked out in the
// primary repository. A target checked out in some other worktree is refused
// outright: moving it would silently invalidate that checkout's working tree.
func (m *Manager) targetCheckout(ctx context.Context, target string) (bool, error) {
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.branch != target {
			continue
		}
		if samePath(entry.path, m.repositoryRoot) {
			return true, nil
		}
		return false, fmt.Errorf("target branch %s is checked out in another worktree: %s", target, entry.path)
	}
	return false, nil
}

// samePath compares two checkout paths, tolerating symlinked ancestors. A
// primary checkout mistaken for a foreign one would be advanced by a bare ref
// update and left holding a working tree its branch no longer describes.
func samePath(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	resolvedRight, err := filepath.EvalSymlinks(right)
	if err != nil {
		return false
	}
	return resolvedLeft == resolvedRight
}

func (m *Manager) resolveBranchCommit(ctx context.Context, branch string) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve branch %s failed with exit code %d: %s", branch, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	commit := strings.TrimSpace(result.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("resolved commit %q for branch %s is invalid", commit, branch)
	}
	return commit, nil
}

func defaultCommitMessage(worktree Worktree) string {
	return fmt.Sprintf("yoyodyne: integrate %s\n\nRun: %s\nBranch: %s\nBase: %s\n",
		worktree.WorkItemID, worktree.RunID, worktree.Branch, worktree.BaseCommit)
}

// harnessCommitEnvironment is the environment a commit the harness makes is
// given: the allowlisted one every Git command here gets, with the harness
// identity written over whatever the allowlist admitted. The identity is stated
// on the command line too; it is stated here as well because an explicit
// environment value takes precedence over an ambient GIT_* one.
func harnessCommitEnvironment() []string {
	return append(execution.GitEnvironment(nil),
		"GIT_AUTHOR_NAME="+harnessCommitAuthorName,
		"GIT_AUTHOR_EMAIL="+harnessCommitAuthorEmail,
		"GIT_COMMITTER_NAME="+harnessCommitAuthorName,
		"GIT_COMMITTER_EMAIL="+harnessCommitAuthorEmail,
	)
}

// CleanupIntegrated removes the artifacts of a proven-integrated run: first the
// worktree, then its branch. Removal is two Git operations that cannot be made
// atomic, so this is written to be resumable from durable evidence rather than
// to assume it runs once. It re-derives the current state of both artifacts and
// finishes whatever remains, including when the worktree is already gone and
// only the branch is left, and it reports what is actually absent afterwards so
// a caller never has to infer it from an error.
//
// Resumability does not relax ownership. Every attempt still proves the exact
// recorded source commit reached the recorded target, and it refuses to delete
// a branch that no longer points at that commit or a directory Git does not
// manage.
func (m *Manager) CleanupIntegrated(ctx context.Context, request CleanupRequest) (Cleanup, error) {
	worktree := request.Worktree
	if err := validateTargetBranch(request.TargetBranch); err != nil {
		return Cleanup{}, err
	}
	// The request describes an integration that already happened, so it must
	// agree with what the worktree recorded at creation. Checking the two
	// independently would let a caller name a branch this worktree was never
	// aimed at and satisfy the containment proof below against that branch
	// instead of the real target.
	if worktree.TargetBranch != "" && request.TargetBranch != worktree.TargetBranch {
		return Cleanup{}, fmt.Errorf("cleanup target %q does not match the worktree's recorded target %q", request.TargetBranch, worktree.TargetBranch)
	}
	if !commitPattern.MatchString(request.SourceCommit) {
		return Cleanup{}, fmt.Errorf("integrated source commit %q is invalid", request.SourceCommit)
	}
	// The base commit is contained in the target by construction, so accepting
	// it as the integrated commit would make the containment proof vacuous:
	// it would hold for a worktree that never integrated anything.
	if request.SourceCommit == worktree.BaseCommit {
		return Cleanup{}, fmt.Errorf("integrated source commit %s is the worktree's base commit, which proves no integration", request.SourceCommit)
	}
	path, err := m.ownedPath(worktree)
	if err != nil {
		return Cleanup{}, err
	}
	// Nothing is removed until the recorded commit is proven to be in the
	// recorded target. This holds on a retry even after the branch is gone,
	// because it asks about the commit rather than the branch that carried it.
	integrated, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", request.SourceCommit, "refs/heads/"+request.TargetBranch)
	if err != nil {
		return Cleanup{}, err
	}
	if integrated.Status != execution.ProcessSucceeded {
		return Cleanup{}, fmt.Errorf("integrated commit %s is not contained in %s", request.SourceCommit, request.TargetBranch)
	}

	cleanup, err := m.removeIntegratedWorktree(ctx, worktree, path, request.SourceCommit)
	if err != nil {
		return cleanup, err
	}
	branchRemoved, err := m.deleteIntegratedBranch(ctx, worktree.Branch, request.SourceCommit)
	cleanup.BranchRemoved = branchRemoved
	return cleanup, err
}

// removeIntegratedWorktree brings the worktree to absent from whatever state a
// previous attempt left it in. Every path acts on the one recorded owned path:
// a registration whose directory is already gone is removed by name rather than
// by pruning the repository, so an unrelated stale registration is never
// touched. A directory Git does not manage is never deleted, because the
// harness only removes what it registered.
func (m *Manager) removeIntegratedWorktree(ctx context.Context, worktree Worktree, path, sourceCommit string) (Cleanup, error) {
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Cleanup{}, err
	}
	info, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Cleanup{}, fmt.Errorf("inspect worktree path: %w", statErr)
	}
	present := statErr == nil
	if present && info.Mode()&os.ModeSymlink != 0 {
		return Cleanup{}, errors.New("worktree path must not be a symlink")
	}

	if !registered {
		if present {
			return Cleanup{}, fmt.Errorf("worktree path %s exists but is not a registered worktree; it must be inspected by hand", path)
		}
		return Cleanup{WorktreeRemoved: true}, nil
	}
	if branch != worktree.Branch {
		return Cleanup{}, fmt.Errorf("worktree branch %q does not match recorded branch %q", branch, worktree.Branch)
	}
	// A registration whose directory is already gone is an interrupted removal.
	// There is nothing on disk left to inspect, so the content checks below only
	// apply while the worktree is still there.
	if present {
		// The integrated worktree is left at the harness commit. Anything else is
		// a different worktree or one that moved on, and is not ours to remove.
		head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return Cleanup{}, err
		}
		if head.Status != execution.ProcessSucceeded {
			return Cleanup{}, fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
		}
		if strings.TrimSpace(head.Stdout) != sourceCommit {
			return Cleanup{}, fmt.Errorf("worktree HEAD is %s, want the integrated commit %s", strings.TrimSpace(head.Stdout), sourceCommit)
		}
		dirty, err := m.isDirty(ctx, path)
		if err != nil {
			return Cleanup{}, err
		}
		if dirty {
			return Cleanup{}, errors.New("refusing to remove a dirty worktree")
		}
	}
	// A removal unregisters an entry in the same unguarded pieces an add writes
	// one, so it queues on the same lease: a creation that reached a half-deleted
	// entry would exit rather than create anything, which is the run this lease
	// exists to stop losing. It is held through the verification below, which
	// reads the bookkeeping the removal just wrote.
	ctx, lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return Cleanup{}, err
	}
	// Releasing is this process letting the next write in, and the operating
	// system does it anyway when the process exits. A close that failed therefore
	// says nothing about the worktree below, which either exists or does not.
	defer func() { _ = lease.release() }()

	removed, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "remove", path)
	if err != nil {
		return Cleanup{}, err
	}
	if removed.Status != execution.ProcessSucceeded {
		return Cleanup{}, fmt.Errorf("remove worktree failed with exit code %d: %s", removed.ExitCode, strings.TrimSpace(removed.Stderr))
	}
	// Removal is only believed once the exact registration is gone. If the check
	// itself cannot run, the removal that already succeeded is still reported:
	// the artifact is gone whether or not this command could confirm it, and
	// claiming otherwise would send an operator after a worktree that no longer
	// exists. Only an observation that it survived clears the flag.
	stillRegistered, _, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Cleanup{WorktreeRemoved: true}, fmt.Errorf("verify removal of worktree %s: %w", path, err)
	}
	if stillRegistered {
		return Cleanup{}, fmt.Errorf("worktree %s is still registered after removal", path)
	}
	return Cleanup{WorktreeRemoved: true}, nil
}

// deleteIntegratedBranch deletes the run's branch, tolerating a branch a
// previous attempt already deleted and refusing one that no longer points at
// the integrated commit.
//
// Deletion is a compare-and-swap on the exact recorded commit rather than
// `git branch -d`. That keeps it deterministic: `-d` decides mergedness against
// the current HEAD or a configured upstream, so it can refuse a branch already
// proven to be contained in the recorded target simply because the target is
// not the branch that happens to be checked out.
func (m *Manager) deleteIntegratedBranch(ctx context.Context, branch, sourceCommit string) (bool, error) {
	existing, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	switch {
	case existing.ExitCode == 1:
		return true, nil
	case existing.Status != execution.ProcessSucceeded:
		return false, fmt.Errorf("check branch %s failed with exit code %d: %s", branch, existing.ExitCode, strings.TrimSpace(existing.Stderr))
	}
	commit, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return false, err
	}
	if commit != sourceCommit {
		return false, fmt.Errorf("branch %s is at %s, want the integrated commit %s", branch, commit, sourceCommit)
	}
	// A ref update cannot see checkouts, so a branch still checked out anywhere
	// is refused rather than deleted out from under that working tree.
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.branch == branch {
			return false, fmt.Errorf("branch %s is still checked out in %s", branch, entry.path)
		}
	}
	deleted, err := m.run(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", "-d", "refs/heads/"+branch, sourceCommit)
	if err != nil {
		return false, err
	}
	if deleted.Status != execution.ProcessSucceeded {
		return false, fmt.Errorf("delete integrated branch %s at %s failed with exit code %d: %s", branch, sourceCommit, deleted.ExitCode, strings.TrimSpace(deleted.Stderr))
	}
	// As with the worktree, a compare-and-swap deletion that already succeeded
	// stays reported as a deletion even when this confirmation cannot run. Only
	// observing the branch still there clears the flag.
	gone, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return true, fmt.Errorf("verify deletion of branch %s: %w", branch, err)
	}
	if gone.ExitCode != 1 {
		return false, fmt.Errorf("branch %s still exists after deletion", branch)
	}
	return true, nil
}

// discardUncheckedOutBranch takes back the branch a creation made just before
// an add that then failed, so a creation that made nothing leaves nothing.
//
// It reports nothing, deliberately. The caller is already returning the failure
// that matters, and the branch is inert either way: nothing has it checked out,
// because the add that was going to is what failed, and the next run for the
// same item gets a name of its own. So what this can go wrong with — a ref
// something else moved in between, a repository that stopped answering — is
// noted and never put in front of the real failure. The deletion is a
// compare-and-swap on the commit the branch was made at, which is what makes
// leaving such a branch alone the safe outcome rather than a guess.
func (m *Manager) discardUncheckedOutBranch(ctx context.Context, branch, commit string) {
	deleted, err := m.run(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", "-d", "refs/heads/"+branch, commit)
	if err != nil {
		m.recordNote("the branch %s was made for a worktree that could not be added, and could not be removed: %v", branch, err)
		return
	}
	if deleted.Status != execution.ProcessSucceeded {
		m.recordNote("the branch %s was made for a worktree that could not be added, and is left at %s: %s", branch, commit, strings.TrimSpace(deleted.Stderr))
	}
}

func (m *Manager) validateRepository(ctx context.Context) error {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("repository validation failed: %s", strings.TrimSpace(result.Stderr))
	}
	topLevel, err := filepath.EvalSymlinks(strings.TrimSpace(result.Stdout))
	if err != nil {
		return fmt.Errorf("resolve Git top-level path: %w", err)
	}
	if topLevel != m.repositoryRoot {
		return fmt.Errorf("configured repository %s is inside Git repository %s", m.repositoryRoot, topLevel)
	}
	return nil
}

func (m *Manager) isDirty(ctx context.Context, path string) (bool, error) {
	// Status is an inspection, including during checkout restoration. Do not
	// let its optional index refresh turn that read into a pathname-based write.
	environment := append(execution.GitEnvironment(nil), "GIT_OPTIONAL_LOCKS=0")
	result, err := m.runWithEnvironment(ctx, environment, "-C", path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if result.Status != execution.ProcessSucceeded {
		return false, fmt.Errorf("inspect Git status failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

type worktreeEntry struct {
	path   string
	branch string
}

// listWorktrees reads the shared worktree bookkeeping. A refusal over a
// registration another run is still writing has already been re-read below the
// command — see registrationWalkAttempts — so a refusal that reaches here
// survived the re-reads, and it is checked against that bookkeeping rather than
// believed: where an entry is registered and not filled in, the listing is
// answered from the registrations with that entry left out and a note saying
// so. A refusal nothing in the bookkeeping accounts for is reported with what
// Git said, exactly as one failing once used to be.
//
// Under an exclusive registry lease the listing Git gives is kept on the lease
// and answered from there until the holder changes the registrations or lets
// the lease go — see registryState. One answered from the bookkeeping over a
// refusal is not kept: it carries a note each time it is given, and the entry
// it stepped over may be cleared by the next thing the holder does.
func (m *Manager) listWorktrees(ctx context.Context) ([]worktreeEntry, error) {
	lease := heldRegistry(ctx)
	entries, generation, ok := lease.cachedListing()
	if ok {
		return entries, nil
	}
	result, err := m.readWorktreeListing(ctx)
	if err != nil {
		var refused listingRefused
		if !errors.As(err, &refused) {
			return nil, err
		}
		return m.readRegistrations(ctx, refused)
	}
	entries = parseWorktreeListing(result.Stdout)
	lease.keepListing(generation, entries)
	return entries, nil
}

// parseWorktreeListing takes the path and the branch of each checkout Git
// described, which is everything this package asks a listing for.
func parseWorktreeListing(listing string) []worktreeEntry {
	var entries []worktreeEntry
	var current worktreeEntry
	flush := func() {
		if current.path != "" {
			entries = append(entries, current)
		}
		current = worktreeEntry{}
	}
	for _, line := range strings.Split(listing, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return entries
}

// readWorktreeListing runs the listing, which runBounded has already run again
// for the passing instant. A Git that ran and refused is reported as a refusal
// worth checking against the bookkeeping; a Git that could not be run at all is
// the harness failing to execute a command, and one that timed out or stalled
// has spent the command's whole budget and is reported as it always was.
func (m *Manager) readWorktreeListing(ctx context.Context) (execution.ProcessResult, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return execution.ProcessResult{}, err
	}
	if result.Status == execution.ProcessSucceeded {
		return result, nil
	}
	if result.Status != execution.ProcessFailed {
		return execution.ProcessResult{}, fmt.Errorf("list worktrees failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return execution.ProcessResult{}, listingRefused{exitCode: result.ExitCode, stderr: strings.TrimSpace(result.Stderr)}
}

// listingRefused is a Git that ran, refused the listing, and went on refusing it
// for every attempt. It says exactly what a refusal has always said, so nothing
// that only reads the message sees a change; it is a type so that the one
// refusal worth checking against the bookkeeping — a Git that ran and would not
// describe the repository — can be told from a Git that never ran or never
// answered.
type listingRefused struct {
	exitCode int
	stderr   string
}

func (r listingRefused) Error() string {
	return fmt.Sprintf("list worktrees failed with exit code %d: %s", r.exitCode, r.stderr)
}

// worktreeRegistrations is the directory under the common Git directory where
// Git keeps one entry per linked worktree.
const worktreeRegistrations = "worktrees"

// readRegistrations answers a listing from the bookkeeping Git was reading when
// it refused, leaving out every entry that is registered and not filled in.
//
// This is the other half of tolerating a creation happening beside this run. The
// re-run under every command covers the instant: `git worktree add` writes an
// entry's files one after another, and a command crossing that instant reads a
// file that has been created and not yet written, which Git treats as a
// repository it cannot describe at all rather than as one entry to skip. What a
// re-run cannot cover is the entry that stays that way — an add whose process
// was killed between the two writes leaves one, `git worktree prune` judges an
// entry by its gitdir file and so leaves it alone, and from then on every
// listing on the repository fails over it. settleRegistrations clears such an
// entry once nothing can still be writing it; this is for a listing that meets
// one before that has happened, or meets the one shape that is not cleared.
//
// So a refusal that survived the re-runs is checked rather than believed. Where
// the bookkeeping holds at least one entry that is demonstrably unfinished, that
// entry is what Git refused over and the listing is answered without it. Where it
// holds none, nothing here accounts for what Git said and the refusal is returned
// as it was: a repository Git cannot describe must not be reported as an empty
// one.
func (m *Manager) readRegistrations(ctx context.Context, refused listingRefused) ([]worktreeEntry, error) {
	directory, err := m.commonGitDirectory(ctx)
	if err != nil {
		return nil, refused
	}
	// The primary checkout has no registration of its own: its HEAD is the common
	// directory's, and a listing names it first.
	primary, ok := checkoutEntry(m.repositoryRoot, filepath.Join(directory, "HEAD"))
	if !ok {
		return nil, refused
	}
	registrations, err := os.ReadDir(filepath.Join(directory, worktreeRegistrations))
	if err != nil {
		return nil, refused
	}
	entries := []worktreeEntry{primary}
	var unfinished []string
	for _, registration := range registrations {
		if !registration.IsDir() {
			continue
		}
		entry, ok := registeredEntry(filepath.Join(directory, worktreeRegistrations, registration.Name()))
		if !ok {
			unfinished = append(unfinished, registration.Name())
			continue
		}
		entries = append(entries, entry)
	}
	if len(unfinished) == 0 {
		return nil, refused
	}
	m.recordNote("the worktree listing left out %s, registered and not yet filled in, which Git refused the whole listing over: %s",
		strings.Join(unfinished, ", "), refused.Error())
	return entries, nil
}

// registeredEntry reads one worktree registration, and reports whether it is one
// a listing can describe. Every file the listing depends on has to be there and
// to have been written: Git creates them one at a time, so a file that exists and
// is empty is an add still in flight — or one that died mid-flight — rather than
// a checkout to name.
func registeredEntry(directory string) (worktreeEntry, bool) {
	// commondir is the file Git itself refuses over, so an entry missing it is
	// exactly the one being stepped over here.
	if _, ok := readWritten(filepath.Join(directory, "commondir")); !ok {
		return worktreeEntry{}, false
	}
	gitdir, ok := readWritten(filepath.Join(directory, "gitdir"))
	if !ok {
		return worktreeEntry{}, false
	}
	// gitdir names the checkout's own .git file, so the checkout is the directory
	// holding it. A relative one is read against the entry, which is how Git reads
	// it too.
	path := filepath.Dir(gitdir)
	if !filepath.IsAbs(path) {
		path = filepath.Clean(filepath.Join(directory, path))
	}
	return checkoutEntry(path, filepath.Join(directory, "HEAD"))
}

// checkoutEntry pairs a checkout with the branch its HEAD names. A detached HEAD
// carries no branch, which is what a listing says of one as well.
func checkoutEntry(path, headPath string) (worktreeEntry, bool) {
	head, ok := readWritten(headPath)
	if !ok {
		return worktreeEntry{}, false
	}
	entry := worktreeEntry{path: path}
	if branch, onBranch := strings.CutPrefix(head, "ref: refs/heads/"); onBranch {
		entry.branch = branch
	}
	return entry, true
}

// readWritten reads a file Git writes while registering a worktree, and reports
// whether it has been written yet. Absent and empty are one answer here: an add
// creates each of these and then fills it in, so both are an entry that is not
// finished rather than one that is malformed.
func readWritten(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	written := strings.TrimSpace(string(content))
	return written, written != ""
}

func (m *Manager) registeredWorktree(ctx context.Context, path string) (bool, string, error) {
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return false, "", err
	}
	for _, entry := range entries {
		if entry.path == path {
			return true, entry.branch, nil
		}
	}
	return false, "", nil
}

// ownedPath derives where this run's worktree must live from its recorded
// identifiers, without requiring the directory to still exist. A resumed
// cleanup needs ownership proven for a worktree that is already gone.
func (m *Manager) ownedPath(worktree Worktree) (string, error) {
	if !runIDPattern.MatchString(worktree.RunID) || !workItemPattern.MatchString(worktree.WorkItemID) {
		return "", errors.New("worktree ownership identifiers are invalid")
	}
	if !commitPattern.MatchString(worktree.BaseCommit) {
		return "", errors.New("worktree base commit is invalid")
	}
	expectedBranch := branchName(worktree.WorkItemID, worktree.RunID)
	if worktree.Branch != expectedBranch {
		return "", fmt.Errorf("worktree branch %q does not match owned branch %q", worktree.Branch, expectedBranch)
	}
	expectedPath := filepath.Join(m.worktreeRoot, worktreeDirectoryName(worktree.WorkItemID, worktree.RunID))
	path, err := filepath.Abs(worktree.Path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	if filepath.Clean(path) != expectedPath {
		return "", fmt.Errorf("worktree path %s does not match owned path %s", path, expectedPath)
	}
	return expectedPath, nil
}

func (m *Manager) validateOwnedPath(worktree Worktree) (string, error) {
	path, err := m.ownedPath(worktree)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect worktree path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("worktree path must not be a symlink")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path symlinks: %w", err)
	}
	if resolved != path {
		return "", errors.New("worktree path resolves outside its owned location")
	}
	return path, nil
}

func (m *Manager) run(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	return m.runWithEnvironment(ctx, nil, args...)
}

// runWithEnvironment runs one local Git command. A nil environment is the
// allowlisted one every Git command here gets -- see runBounded, which is where
// nil is resolved so that no call site can inherit the harness's own by
// forgetting to say.
func (m *Manager) runWithEnvironment(ctx context.Context, environment []string, args ...string) (execution.ProcessResult, error) {
	return m.runBounded(ctx, environment, m.localTimeout(), args...)
}

// localTimeout is the budget one local Git command gets now. A caller that
// named one gets it as named. The default is the idle figure scaled by how far
// the machine's one-minute load average exceeds its cores, read per command
// rather than once, because a run lasts long enough for the load to change
// under it. A load the platform cannot report leaves the idle figure alone.
func (m *Manager) localTimeout() time.Duration {
	if m.timeout > 0 {
		return m.timeout
	}
	load, ok := loadAverage()
	if !ok {
		return defaultTimeout
	}
	return scaledTimeout(defaultTimeout, load, runtime.NumCPU())
}

// MachineLoad is the machine's one-minute load average and the number of cores
// it is read against, the same two figures a local Git command's budget is
// scaled by, and whether the platform could report the load at all.
func MachineLoad() (load float64, cores int, ok bool) {
	load, ok = loadAverage()
	return load, runtime.NumCPU(), ok
}

// ScaleForLoad is a budget scaled for the machine's load exactly as a local Git
// command's is: unchanged at or below one load per core, grown in proportion
// past that, and capped at maxLoadFactor times. It is exported so a bound kept
// elsewhere — the check stage's — scales by the one reading and the one cap
// rather than by a copy of them that could drift.
func ScaleForLoad(base time.Duration, load float64, cores int) time.Duration {
	return scaledTimeout(base, load, cores)
}

// checkoutTimeout is the budget one creation's `git worktree add` gets: the
// budget a local Git command gets, plus an allowance for every file the add has
// to write.
//
// The allowance is added to a caller's named Timeout rather than replacing it,
// and this is the one place a named budget is not taken as the whole answer. A
// caller naming a figure is saying what a Git command is worth, and it cannot
// have meant the same figure for the one command that writes the tree out —
// leaving it to replace the allowance would make this fix inert for any manager
// built with a Timeout, which is the same silent regression as never having
// made it. What the caller named is honoured exactly for every other command,
// and for this one it is the floor.
//
// The default figure is scaled by the load as localTimeout scales its own, and
// a named one is not, for the reason localTimeout does not scale one: the load
// is what the idle figure has to be corrected for, and a caller who named a
// budget has already said what it means.
func (m *Manager) checkoutTimeout(files int, counted bool) time.Duration {
	allowance := time.Duration(checkoutAllowanceFiles(files, counted)) * checkoutFileBudget
	if m.timeout > 0 {
		return m.timeout + allowance
	}
	base := defaultTimeout + allowance
	load, ok := loadAverage()
	if !ok {
		return base
	}
	return scaledTimeout(base, load, runtime.NumCPU())
}

// checkoutAllowanceFiles is how many files the budget is bought for. A tree
// nobody could count is bought for uncountedCheckoutFiles rather than for
// nothing, which is the difference between a creation that runs on a budget it
// did not earn and one held to a figure the checkout was never going to fit in.
func checkoutAllowanceFiles(files int, counted bool) int {
	if !counted {
		return uncountedCheckoutFiles
	}
	if files < 0 {
		return 0
	}
	return files
}

// describeCheckout names the tree an add was writing, for a failure that has to
// say what its budget was bought for. A tree nobody could count says so, rather
// than reading as an empty one.
func describeCheckout(files int, counted bool) string {
	if !counted {
		return "the checkout of a tree that could not be counted"
	}
	return fmt.Sprintf("the checkout of %d file(s)", files)
}

// checkoutFiles counts what a worktree cut from this commit has to check out,
// and says whether the count was read at all.
//
// It reads the commit rather than any working tree, because the tree the add
// writes is the commit's, and it is the same figure Git counts down as it goes —
// so the budget and the progress a killed add left are about one thing. Output
// the runner cut at its own bound leaves a count that is low rather than wrong,
// which costs a creation some of its allowance and never gives it one the tree
// did not earn.
//
// Counting is a Git command the creation did not used to run, so it is a way
// for a creation to die that this budget introduced, and it is kept from being
// one. A count that failed for its own reasons is not fatal: the creation goes
// on, budgeted as an uncounted tree, and says so — refusing there would fail
// creations that would have succeeded, for want of a figure that only sizes a
// bound. The single failure that does stop the creation is a count the harness
// itself ended, and it stops it as ErrCheckoutKilled: a count killed by the
// load is the same machine-too-busy death as an add killed by it, and refusing
// it in any other class would charge the item a round and its re-run for
// exactly the failure this item exists to stop charging for.
func (m *Manager) checkoutFiles(ctx context.Context, commit string) (int, bool, error) {
	// Bounded by the figure this reads rather than by the one runWithEnvironment
	// would read for itself, which is the same budget and not the same number:
	// the load is read per command, so a refusal naming a figure it read a second
	// time would name a budget the count did not actually run under.
	budget := m.localTimeout()
	result, err := m.runBounded(ctx, nil, budget, "-C", m.repositoryRoot, "ls-tree", "-r", "--name-only", commit)
	if err != nil {
		m.recordNote("the tree under %s could not be counted, so its checkout is bounded as an uncounted tree: %v", commit, err)
		return 0, false, nil
	}
	if killedCheckout(result) {
		return 0, false, fmt.Errorf("%w: counting the tree to be checked out was still running after %s, so the checkout never started and no agent of this run was invoked",
			ErrCheckoutKilled, budget)
	}
	if result.Status != execution.ProcessSucceeded {
		m.recordNote("counting the tree under %s failed with exit code %d, so its checkout is bounded as an uncounted tree: %s",
			commit, result.ExitCode, strings.TrimSpace(result.Stderr))
		return 0, false, nil
	}
	return strings.Count(result.Stdout, "\n"), true, nil
}

// killedCheckout reports an add this harness ended rather than Git answering:
// a total budget that ran out, or a process that produced nothing for longer
// than a liveness bound allowed. Neither leaves a Git exit code or a Git error,
// which is what made the field cases read as a creation that failed for reasons
// nobody could find in their own message.
func killedCheckout(result execution.ProcessResult) bool {
	return result.Status == execution.ProcessTimedOut || result.Status == execution.ProcessStalled
}

// scaledTimeout is base multiplied by how oversubscribed the machine is: a load
// average at or under the core count is an idle machine for this purpose and
// gets base, and one at twice the cores gets twice base, up to maxLoadFactor.
func scaledTimeout(base time.Duration, load float64, cores int) time.Duration {
	if cores < 1 {
		cores = 1
	}
	factor := load / float64(cores)
	if factor <= 1 {
		return base
	}
	if factor > maxLoadFactor {
		factor = maxLoadFactor
	}
	return time.Duration(float64(base) * factor)
}

// runRemote runs a Git command that talks to the remote. It is bounded by its
// own longer timeout, because a network round trip is not a local ref update
// and holding both to the same deadline would either starve the push or let a
// local command hang.
//
// It is also the one family of Git commands here given a forge credential.
// Reaching a remote is what needs one; a local ref update, a diff, and a
// checkout do not, and handing every Git command a token so that the push has
// one would put it in front of every hook the repository runs.
//
// A remote that refused that credential is an error here rather than a result
// for each caller to word, so every remote command refused for a key ends on
// ErrRemoteAuthRefused — see authRefusal.
func (m *Manager) runRemote(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	result, err := m.runBounded(ctx, execution.ForgeEnvironment(nil), m.remoteTimeout(), args...)
	if err != nil {
		return result, err
	}
	return result, authRefusal(args, result)
}

func (m *Manager) remoteTimeout() time.Duration {
	if local := m.localTimeout(); local > pushTimeout {
		return local
	}
	return pushTimeout
}

// runBounded runs one Git command, and runs it again where it crossed a
// creation: a Git that ran and refused over a registration it could not read —
// see crossedRegistration — is the one failure the passing instant produces, and
// it is produced by every command that walks the registrations rather than only
// by the listing. Git walks them while checking what it is about to do, before
// it does it, so a command run again after that refusal starts over rather than
// carrying on from something half-done. Only a Git that ran and refused that way
// is run again: a Git that could not be run at all is the harness failing to
// execute a command, any other refusal is an answer, and one that timed out or
// stalled has already spent the command's whole budget — spending it twice more
// would turn a slow repository into a run three times slower to fail.
func (m *Manager) runBounded(ctx context.Context, environment []string, timeout time.Duration, args ...string) (execution.ProcessResult, error) {
	// A command that walks the registrations reads them under the lease, so it
	// queues behind a creation rather than crossing one — see
	// leaseRegistryShared. The re-run below is what is left for the crossings the
	// lease cannot stop: another harness on an older binary, a Git command
	// somebody ran by hand, and a platform with no advisory lock at all.
	if walksRegistrations(args) {
		lease, err := m.leaseRegistryShared(ctx)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		defer func() { _ = lease.release() }()
	}
	// A command that can change the registrations drops the listing kept on the
	// lease it runs under, both before it starts and once it has ended, so no
	// listing is answered from across it — see registryState. Outside a lease
	// nothing is kept and this does nothing.
	if held := heldRegistry(ctx); held != nil && mayChangeRegistrations(args) {
		held.forgetListing()
		defer held.forgetListing()
	}
	// A Git command runs hooks the repository supplies, so what it is launched
	// with is what those hooks are launched with. Nothing here inherits the
	// harness's own environment: a caller that named none gets the allowlist an
	// agent invocation is built from, and the one family of commands that needs a
	// forge credential asks for it by name — see runRemote.
	if environment == nil {
		environment = execution.GitEnvironment(nil)
	}
	for attempt := 1; ; attempt++ {
		result, err := m.runner.Run(ctx, execution.Command{
			Name:    m.gitBinary,
			Args:    append(append([]string(nil), maintenanceOptions...), args...),
			Env:     environment,
			Timeout: timeout,
		}, nil)
		if err != nil {
			return execution.ProcessResult{}, fmt.Errorf("run Git command: %w", err)
		}
		if result.Status != execution.ProcessFailed || !crossedRegistration.MatchString(result.Stderr) || attempt >= registrationWalkAttempts {
			return result, nil
		}
		crossedAgain(ctx, Crossing{
			Command:  describeGitCommand(args),
			Attempt:  attempt,
			Attempts: registrationWalkAttempts,
			Refusal:  strings.TrimSpace(crossedRegistration.FindString(result.Stderr)),
		})
		select {
		case <-ctx.Done():
			return execution.ProcessResult{}, ctx.Err()
		case <-time.After(registrationWalkRetryWait):
		}
	}
}

// Crossing is one Git command run again because it crossed another worktree's
// creation or removal: which command, which attempt Git refused, out of how
// many, and Git's own words for the refusal. It is said rather than kept quiet
// because a crossing is the one failure the manager absorbs without anybody
// having asked it to, and a run that took three tries to get its worktree is
// worth knowing about when it takes four.
type Crossing struct {
	Command  string
	Attempt  int
	Attempts int
	Refusal  string
}

// crossingsKey carries, on a context, where the Git commands run under it say a
// crossing they ran again.
type crossingsKey struct{}

// WithCrossings is ctx carrying where the manager says each crossing it runs a
// Git command again over. It travels on the context rather than on the manager,
// because one manager serves every run a process hosts while the reader who
// wants to hear about a crossing — a watch session, for the dispatch it
// started — is one of them.
func WithCrossings(ctx context.Context, record func(Crossing)) context.Context {
	return context.WithValue(ctx, crossingsKey{}, record)
}

// crossedAgain says one crossing where the context asked for them, and nowhere
// where it did not. Saying it never touches the command: the re-run is decided
// before this is called and goes ahead whatever became of it.
func crossedAgain(ctx context.Context, crossing Crossing) {
	if record, wired := ctx.Value(crossingsKey{}).(func(Crossing)); wired && record != nil {
		record(crossing)
	}
}

// describeGitCommand names a Git command as a reader would recognise it — `git
// worktree add`, `git rebase` — and without its paths, which are a run's own
// and say nothing about which step crossed.
func describeGitCommand(args []string) string {
	subcommand, rest := gitSubcommand(args)
	if subcommand == "worktree" && len(rest) > 0 {
		return "git worktree " + rest[0]
	}
	return "git " + subcommand
}

// registrationWalkers are the Git subcommands that read the worktree
// bookkeeping while doing whatever else they do, and so meet an entry a
// creation beside them has not finished writing.
//
// `worktree` is the obvious one and is not the interesting one. The rest are
// here because Git checks that a branch is not checked out in another worktree
// before it moves one: a rebase does it for the branch it replays, a checkout
// and a switch for the branch they leave and take, and a branch deletion or
// rename for the branch it is about to change. Every one of them walks
// worktrees/ to find out, and every one of them fails outright rather than
// skipping an entry it cannot read — which is how a rebase came to fail a
// concurrent-runs test with `failed to read .git/worktrees/<other>/commondir`
// while the listing beside it was tolerating the same entry.
//
// Nothing else the manager runs is here, and the two kinds left out are left
// out for different reasons. A command that only reads history or a working
// tree — rev-parse, diff, log, status, show-ref, merge-base — never opens the
// registrations. And `gc` and `maintenance`, which do, are never asked for:
// they are fenced off by maintenanceOptions rather than queued, because a prune
// deletes a registration being written rather than merely failing over it.
var registrationWalkers = map[string]struct{}{
	"worktree": {},
	"rebase":   {},
	"checkout": {},
	"switch":   {},
	"branch":   {},
}

// walksRegistrations says whether this Git command reads the worktree
// bookkeeping, and so should take the shared registry lease before it runs. The
// subcommand is the first argument that is not a global option, because the
// manager passes `-C <path>` and `-c <setting>` ahead of it.
func walksRegistrations(args []string) bool {
	subcommand, _ := gitSubcommand(args)
	_, walks := registrationWalkers[subcommand]
	return walks
}

// mayChangeRegistrations says whether this Git command can change what a
// worktree listing describes, and so drops a listing kept on the lease it runs
// under. It is every command that walks the registrations except the listing
// itself: an add, a removal, and a prune change the entries, and a checkout, a
// switch, a rebase, and a branch rename change the branch an entry names. It is
// deliberately that whole family rather than the three writes the lease was
// taken for, because a listing kept past a change the list forgot to name is
// exactly the stale answer the lease must never give.
func mayChangeRegistrations(args []string) bool {
	subcommand, rest := gitSubcommand(args)
	if _, walks := registrationWalkers[subcommand]; !walks {
		return false
	}
	return subcommand != "worktree" || len(rest) == 0 || rest[0] != "list"
}

// gitSubcommand is the first argument that is not a global option, and the
// arguments after it. The manager passes `-C <path>` and `-c <setting>` ahead of
// it.
func gitSubcommand(args []string) (string, []string) {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace":
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				continue
			}
			return args[index], args[index+1:]
		}
	}
	return "", nil
}

func validateCreateRequest(request CreateRequest) error {
	if !runIDPattern.MatchString(request.RunID) {
		return errors.New("run id is invalid")
	}
	if !workItemPattern.MatchString(request.WorkItemID) {
		return errors.New("work item id is invalid")
	}
	if request.TargetBranch != "" {
		if err := validateTargetBranch(request.TargetBranch); err != nil {
			return err
		}
		if request.TargetBranch == branchName(request.WorkItemID, request.RunID) {
			return errors.New("target branch must differ from the worktree branch")
		}
	}
	return validateRef(request.BaseRef)
}

// validateTargetBranch keeps an integration target a plain local branch name.
// Fully qualified refs and HEAD are refused so a caller can never aim a
// fast-forward at something other than refs/heads/<target>.
func validateTargetBranch(branch string) error {
	if branch == "" {
		return errors.New("worktree has no recorded target branch")
	}
	if err := validateRef(branch); err != nil {
		return fmt.Errorf("invalid target branch: %w", err)
	}
	if branch == "HEAD" || strings.HasPrefix(branch, "refs/") {
		return fmt.Errorf("target branch %q must be a local branch name", branch)
	}
	return nil
}

func validateRef(value string) error {
	if !refPattern.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("Git ref %q is invalid", value)
	}
	return nil
}

func branchName(workItemID, runID string) string {
	item := strings.ToLower(strings.ReplaceAll(workItemID, ".", "-"))
	return "yoyodyne/" + item + "/" + strings.TrimPrefix(runID, "run-")[:8]
}

func worktreeDirectoryName(workItemID, runID string) string {
	item := strings.ToLower(strings.ReplaceAll(workItemID, ".", "-"))
	return item + "-" + strings.TrimPrefix(runID, "run-")[:8]
}

// WithinWorktreeRoot reports whether a path is the root the harness keeps its
// worktrees under, or lies beneath it. It decides that exactly as the
// containment check in New does — through both paths' symlinks, and tolerating a
// worktree root nothing has created yet — so a caller that has to know whether
// it is standing inside a managed worktree asks here rather than comparing the
// strings itself, and gets the answer New would have refused it over.
func WithinWorktreeRoot(worktreeRoot, path string) (bool, error) {
	root, err := absoluteCanonicalPath(worktreeRoot)
	if err != nil {
		return false, fmt.Errorf("resolve worktree root: %w", err)
	}
	candidate, err := absoluteCanonicalPath(path)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", path, err)
	}
	return containsPath(root, candidate), nil
}

func absoluteCanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return canonicalizeFuturePath(absolute)
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func isFilesystemRoot(path string) bool {
	return filepath.Dir(path) == path
}

func canonicalizeFuturePath(path string) (string, error) {
	path = filepath.Clean(path)
	current := path
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return resolved, nil
}
