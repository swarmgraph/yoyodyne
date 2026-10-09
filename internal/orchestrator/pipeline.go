package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/selfcheck"
	"github.com/mason-bryant/yoyodyne/internal/spend"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// maxCommitSubjectBytes bounds the work item title carried into the
// harness-owned commit subject.
const maxCommitSubjectBytes = 72

type WorkTracker interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	// Claim takes the item, and reports beside it the stale blocked status it
	// cleared on the way where it met one — beside the error too, where no read
	// confirmed the clear — so the run's record can say which ending the clear
	// had. Nil is a claim that met no stale status, which is nearly all of them.
	Claim(ctx context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
	// Block records a durable blocker. The harness uses it when a run stops on
	// something no further attempt of its own can resolve.
	Block(ctx context.Context, id, reason string) (beads.WorkItem, error)
	// Release gives the claimed item back to the queue. The harness uses it when
	// a run ends on something that is nobody's decision and lifts by itself — the
	// provider's usage window — so the item is pulled again once it has.
	Release(ctx context.Context, id, reason string) (beads.WorkItem, error)
	Complete(ctx context.Context, id, reason string) (beads.WorkItem, error)
	// Reopen returns a claimed item to the backlog under the parking it is given.
	// The harness uses it when a run integrated its change and claimed that change
	// does not discharge the item, which is a landing worth keeping and not a
	// closure. The parking is what stops the same item being selected again
	// immediately, and it is empty only where the landing named the impediment the
	// item is instead made to wait on.
	Reopen(ctx context.Context, id, reason string, parking domain.WorkItemParking) (beads.WorkItem, error)
	// AddBlocker makes one item wait for another. The harness uses it for the
	// other half of that settlement: a landing that named its impediment leaves
	// the item open waiting on it, which selection honours and which releases
	// itself when the impediment closes.
	AddBlocker(ctx context.Context, id, blockerID string) error
}

// Pricer records what a work item has cost across every run made for it. The
// pipeline never prices anything itself: it says when a run for an item has
// ended, and what that is worth is read from the recorded evidence elsewhere. It
// is satisfied by cost.Ledger.
type Pricer interface {
	Record(ctx context.Context, workItemID string) (*beads.Cost, error)
}

type WorktreeManager interface {
	ValidateReady(ctx context.Context) error
	CurrentBranch(ctx context.Context) (string, error)
	Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error)
	// Observe reports what is actually there of a run's branch and checkout. A
	// failing run asks it before it writes its own failure down, because a note
	// saying the work is preserved is a claim about the repository rather than
	// about the record, and only this can settle it.
	Observe(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Observation, error)
	SummarizeChanges(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.ChangeSummary, error)
	UnifiedChanges(ctx context.Context, worktree gitworktree.Worktree, limits gitworktree.DiffLimits) (gitworktree.ChangeDiff, error)
	// FileAtCommit reads one path as a commit holds it. It is what the reviewer's
	// copy of a document the change is measured against is read with, because
	// that copy has to be the one at the change's own base rather than whatever
	// the checkout holds by the time the review is asked for.
	FileAtCommit(ctx context.Context, commit, path string, maxBytes int64) (gitworktree.FileAt, error)
	FilesAtCommit(ctx context.Context, commit string, maxFiles, maxBytes int) (gitworktree.CommitListing, error)
	// ChangedPaths names every path the change touches. It is what the gate in
	// front of the checks decides on, so it is separate from the summary and the
	// patch above: those are bounded for a reader, and a gate that saw a bounded
	// listing would pass whatever the bound had cut.
	ChangedPaths(ctx context.Context, worktree gitworktree.Worktree) ([]string, error)
	// ContentIdentity names the change as the worktree holds it, so evidence
	// about the change can be bound to what it is rather than to when it was
	// read. The check phase records it and the promotion reads it again: a
	// project that does not publish has no commit to bind its checks to until
	// the promotion itself, and this is what binds them.
	ContentIdentity(ctx context.Context, worktree gitworktree.Worktree) (string, error)
	// CurrentExports names the derived files the manager refreshes into a
	// worktree and holds out of the change. The same gate refuses them, so the
	// list is asked of the thing that holds them rather than restated here: an
	// export the harness stopped holding must not go on being refused, and one it
	// began holding must not go on being committable.
	CurrentExports() []string
	// CommitAttempt records what one developer invocation left in the worktree as
	// a harness-owned commit and reports the commit the branch then stands at. It
	// is what every run does with an attempt, publishing or not: the checks, the
	// reviewer and the promotion all read the change base-relative, but the branch
	// tip is what a reviewer's evidence names and what a repair round is judged
	// against, and a tip that lags the worktree is a round judged on the round
	// before it.
	CommitAttempt(ctx context.Context, worktree gitworktree.Worktree, message string) (string, error)
	Integrate(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error)
	// PrepareLanding is the promotion onto a target branch the forge protects:
	// the same checks and the same harness commit Integrate makes, and the local
	// target left where it is. The change reaches the target through its pull
	// request, and the local branch only follows the forge by CatchUpTarget.
	PrepareLanding(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error)
	// RebaseOntoTarget re-prepares a change whose promotion lost a race, by
	// replaying it onto wherever the target branch went. It never resolves a
	// conflict, and it is one of the two things that rewrite a run's branch.
	RebaseOntoTarget(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Rebase, error)
	// ReplayForRepair is the other, and it is what a refused replay is answered
	// with: the change moved onto the target anyway, with whatever would not merge
	// left in the worktree as conflict markers for its author to settle. It
	// resolves nothing either — leaving the disagreement is the point.
	ReplayForRepair(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Rebase, error)
	CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
	// The publishing half. RemoteConfigured is what lets a repository with no
	// remote degrade to purely local behavior instead of failing; PublishBranch
	// and DeleteRemoteBranch are the Git writes publishing needs, which the
	// harness performs whichever phase asked for them. The merge itself is the
	// forge's, so the two remaining calls only observe it: one says whether the
	// remote target may still be merged into, the other whether the merge put
	// the promotion there and at which commit it left the branch.
	RemoteConfigured(ctx context.Context) (bool, error)
	// PushRemoteConfigured asks the same question about the remote run branches
	// are pushed to, which is a different repository from the one above whenever
	// a project publishes from a fork. It is separate so a contributor whose
	// fork remote is missing is told about the fork rather than about the
	// project's own remote, which is there.
	PushRemoteConfigured(ctx context.Context) (bool, error)
	PublishBranch(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Publication, error)
	// RepublishBranch puts a replayed run branch back on the remote, replacing
	// exactly the commit the harness published there and nothing else.
	RepublishBranch(ctx context.Context, worktree gitworktree.Worktree, previousCommit string) (gitworktree.Publication, error)
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
	// ConfirmRemoteTarget establishes that the forge's merge put the promotion on
	// the remote target and names the merge commit that carried it. The commit
	// the forge recorded as the merge, where the caller has one, decides only
	// what is named and never whether the merge is confirmed.
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration, mergeCommit string) (string, error)
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
	// CatchUpTarget brings the local target branch onto what the forge has,
	// which is the local half of the merge the forge just performed. It moves
	// the same branch a promotion does, so it is only ever called while this run
	// holds that branch's promotion lease.
	CatchUpTarget(ctx context.Context, targetBranch string) (gitworktree.Catchup, error)
}

// PullRequests is the forge access publishing needs. The pipeline decides when
// a pull request must exist and when its work has been authorized for merging;
// it never decides forge semantics itself.
type PullRequests interface {
	Availability(ctx context.Context) (publish.Availability, error)
	Ensure(ctx context.Context, request publish.Request) (publish.PullRequest, error)
	Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error)
	State(ctx context.Context, head string) (publish.PullRequest, error)
	// Protection asks the forge whether the target branch is protected, which
	// decides whether a promotion may move the local target before the forge
	// merges. An error is a question nobody answered, and is read as protected.
	Protection(ctx context.Context, branch string) (publish.BranchProtection, error)
}

// ChangeReviewer runs one independent review of a developer's change. The
// pipeline never decides review semantics itself; it only acts on the verdict.
type ChangeReviewer interface {
	Review(ctx context.Context, request review.Request) (review.Result, error)
}

type StateStore interface {
	RetirementRuns
	// Reserve creates a fresh run and returns the lease that makes this process
	// its only owner; Adopt takes the same lease over the run already in flight
	// for an item, reporting runstate.ErrNoRunInFlight when there is none. Every
	// entry point into an in-flight run goes through one of them, so resuming a
	// run is exactly as exclusive as starting one.
	Reserve(ctx context.Context, state runstate.State, maxConcurrent int) (*runstate.Lease, error)
	Adopt(ctx context.Context, workItemID string) (runstate.State, *runstate.Lease, error)
	// ReclaimSlot takes a developer slot back for an adopted run paused on work
	// its item waited on, under the limit Reserve enforces and refusing with the
	// same CapacityError; the pause gave its slot up as it was recorded.
	ReclaimSlot(ctx context.Context, state runstate.State, maxConcurrent int) (runstate.State, error)
	// LeasePromotion admits this run to promote into one target branch, waiting
	// its turn behind whatever is promoting into it now. Development is parallel
	// and integration is serial, and this is what makes the second half true
	// across processes rather than only within one.
	LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error)
	// LeaseLanding admits this run's landing checks onto one target branch,
	// waiting its turn behind whichever landing on it is running now, for at most
	// wait, and telling queued once as the wait begins. It is what keeps two
	// whole suites from running at once over one branch.
	LeaseLanding(ctx context.Context, targetBranch string, wait time.Duration, queued func()) (*runstate.Lease, error)
	// LeaseRotation admits this start to choose the account it will be served by
	// and to record the run that spends it, waiting its turn behind whichever
	// start is choosing now. The pool's cursor is the run records, so the choice
	// and the reservation that moves the cursor are one step or they are a race
	// every simultaneous start can lose.
	LeaseRotation(ctx context.Context) (*runstate.Lease, error)
	Save(state runstate.State) error
	// Load reads one run's record back. A run holding its own lease has no reason
	// to ask what is on disk — its own state is ahead of it — with two exceptions:
	// a save the store refused leaves the record behind what this process holds,
	// and the ending has to be written onto what is actually there rather than
	// onto the record that was refused; and a run about to report a pull request
	// reads the record back to confirm the request is on it before it completes.
	Load(runID string) (runstate.State, error)
	AppendEvent(event execution.Event) error
	// ReleasedWait reports whether the operator has said that a run's recorded
	// usage-limit deadline no longer describes the provider, and ClearRelease
	// consumes that statement as the run acts on it. They are read and written
	// from beside the run rather than in it, because the process serving the wait
	// holds the run's lease and the operator releasing it does not.
	ReleasedWait(runID string) (runstate.Release, bool, error)
	ClearRelease(runID string) error
	// StopRequested reports whether the operator has asked one run to stop. It is
	// read from beside the run for the same reason a release is: the operator does
	// not hold the run's lease, so they state the fact in a file of their own and
	// the run reads it at the boundaries where it would otherwise spend.
	StopRequested(runID string) (runstate.StopRequest, bool, error)
	// Triage is the product's durable per-work-item counters, where the review
	// rounds an item has spent are recorded. They are reached through the run
	// store because they are the same product's record, and they are not part of
	// any run: what an item has cost spans every run of it, and a run that ends
	// takes none of it with it.
	Triage() *runstate.TriageStore
	// Incomplete lists the runs still in flight. It is how a step that declines to
	// act on a run can still say what it is leaving alone, which is the whole of
	// what it is for here: reading a record is not acting on it, so this takes no
	// lease and the run it describes may well belong to another process.
	Incomplete() ([]runstate.State, error)
	// Latest is the most recently started run recorded for this item, whatever
	// became of it, and runstate.ErrNoRecordedRun where the harness has never run
	// it. Adopt answers for the runs in flight and says nothing about the ones
	// that ended, which is exactly the blind spot a fresh run started in place of
	// a repair falls into: the stopped run is not in flight, so nothing above
	// notices that starting over is the wrong thing to do to it.
	Latest(workItemID string) (runstate.State, error)
	// Reruns is what triage has claimed of the fresh runs it decided. A fresh run
	// of an item whose last run stopped owing a repair is right in exactly one
	// case — the development manager decided the ground moved and the work is to
	// be done again — and a claim against that stoppage is what says so.
	Reruns() *runstate.RerunStore
}

type CheckRunner interface {
	Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error)
}

// LandingCheckouts cuts a checkout of an integrated commit for the landing
// checks to run in, and removes it afterwards. It is satisfied by
// gitworktree.Manager, and it is its own interface rather than two more methods
// on WorktreeManager because it is asked after the run's own worktree is gone
// and about a commit rather than a change: nothing about a run's worktree, its
// branch, or its promotion is involved.
type LandingCheckouts interface {
	CheckoutCommit(ctx context.Context, runID, commit string) (string, error)
	RemoveCheckout(ctx context.Context, path string) error
}

// WorkFiler admits a work item the harness itself found: today, the item a red
// landing files. It is satisfied by beads.Client, and it is separate from
// WorkTracker because that is what a run does to the item it is running and
// this brings work into existence — the caller is responsible for having the
// authority to ask, and the only caller is the landing, under the operator's
// standing order that a red landing files its own item.
type WorkFiler interface {
	Create(ctx context.Context, item beads.NewWorkItem) (beads.WorkItem, error)
	// List is what a landing reads before it files: an open item the harness
	// already filed for the same check on the same branch is noted rather than
	// filed again, so a target branch left red for several landings is one item
	// at the front of the queue and not one per landing.
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// Directives is what the operator has told the harness, as a run reads it.
//
// It is consulted rather than delivered. A directive that changes a governed
// artifact this work derives from, or that nobody can act on until the operator
// says what they meant, is not context for the developer to weigh — it is a
// reason the work must not proceed at all, because the intent it would be
// written against is being rewritten or was never settled. So the pipeline asks
// this question at every point where it is about to commit to work: before it
// claims an item, before it resumes a run, and before it puts a change through
// the gate that would integrate it.
//
// It is read from durable records every time, never cached. The directive that
// matters most is the one recorded by another process while this run was
// developing, and a run that answered from what it read at the start would be
// exactly the run this exists to stop.
type Directives interface {
	// Pausing lists the unresolved directives that pause one work item. An empty
	// result is the ordinary answer and means the work may proceed.
	Pausing(workItemID string) ([]directive.Directive, error)
}

// OperatorHolds is the operator's switch over everything the harness would spend
// on a provider, as a run reads it.
//
// It is consulted for the same reason and in the same way the directives are,
// and it is read from durable records every time rather than cached: the hold
// that matters is the one an operator placed while this run was developing. What
// differs is its scope. A directive is about work; this is about spending, so it
// is asked at every provider-call boundary rather than at the points where a run
// commits to work, and it says nothing about the work being right or wrong.
//
// It is satisfied by runstate.OperatorHoldStore.
type OperatorHolds interface {
	// Held reports whether the operator is holding harness activity. Not held is
	// the ordinary answer and means the run may spend.
	Held() (runstate.OperatorHold, bool, error)
}

// IntakeHolds is the operator's switch over the work the harness chooses for
// itself, as a run reads it.
//
// It is the narrower of the two switches and it is asked in exactly one place:
// where a run would be started for a reason other than the operator naming the
// item. A hold on intake stops the harness pulling anything more and leaves
// everything already running alone, which is the point of having it apart from
// the hold over spending — an operator who suspects the queue is heading
// somewhere wrong wants the queue stopped, not the half-finished change thrown
// away.
//
// It is satisfied by runstate.IntakeHoldStore.
type IntakeHolds interface {
	// Held reports whether intake is held for this product, by the operator or by
	// the harness's own brake. Not held is the ordinary answer and means the
	// harness may choose work.
	Held() (runstate.IntakeHold, bool, error)
}

type Pipeline struct {
	// Availability reads the same OS and scheduler observations as services.
	Availability func(from, to time.Time, task string) readmodel.GapCause
	Tracker      WorkTracker
	Worktrees    WorktreeManager
	Store        StateStore
	// Backend is the adapter for the backend the developer slot is configured
	// for, which is what every run reserved under this configuration records.
	Backend backend.Backend
	// RecordedBackends builds the adapter for a backend a run recorded that is
	// not the configured developer's, so a run reserved before the developer
	// moved to another backend carries on in the session it opened (see
	// recordedbackend.go). It reports false for a backend the project no longer
	// describes or this build cannot launch. Optional: a pipeline without it
	// refuses to invoke such a run's developer, before any provider call.
	RecordedBackends func(named domain.Backend) (backend.Backend, bool)
	Checks           CheckRunner
	// Load is the machine's load, read as each check begins to scale the check
	// stage's bound the way a local Git command's budget is scaled. Optional: a
	// pipeline without one — or on a platform that cannot report the load —
	// holds the stage to the configured figure, as Git reads an unknown load as
	// idle.
	Load MachineLoad
	// Instances is where the workflow instances runs are executed against are
	// recorded. It is the same store the runs are in — a harness has one durable
	// state root — and it is named separately because an instance is written
	// through the store itself rather than through the interface a run's record
	// goes through. A pipeline without one observes nothing, which is what a
	// pipeline whose project rolled back to the legacy path does anyway.
	Instances *runstate.Store
	// Reviewer is required only when integration is automatic, because nothing
	// is ever integrated without an independent verdict.
	Reviewer ChangeReviewer
	// Publisher is required only when publishing is automatic, because a project
	// that has not opted in never opens a pull request.
	Publisher PullRequests
	// Directives is what the operator has told the harness. It is required rather
	// than optional, unlike the two collectors below: a run that cannot find out
	// what has been directed would proceed against intent that may already have
	// been withdrawn, and a directive nothing enforces is the state this was built
	// to end.
	Directives Directives
	// Holds is the operator's switch over provider spending. It is required for
	// the same reason the directives are: a run that cannot find out whether the
	// operator has paused everything would spend against a hold that is in force,
	// and a pause the harness can miss is not a pause.
	Holds OperatorHolds
	// Intake is the operator's switch over work the harness chooses for itself.
	// It is required for the same reason, and it stops nothing an operator asked
	// for by name: what it holds is the choosing, so a run this pipeline was told
	// to make proceeds under it exactly as it would have.
	Intake IntakeHolds
	// ProviderOutages is the product's record of the provider answering nobody —
	// a login nobody has renewed, an API nothing reaches — written by the run that
	// meets it and cleared by the first the provider answers again. It is
	// optional: a run wired without one waits exactly as it would have, and what
	// is lost is the record every surface names the wait from.
	ProviderOutages ProviderOutages
	// CapacityServed is where every invocation the provider served records the
	// account and model it was served on, which is what reads a refusal of that
	// account and model as lifted before the reset it quoted. It is optional: a
	// run wired without one runs exactly as it would have, and every refusal then
	// stands until its quoted reset.
	CapacityServed CapacityServedRecorder
	// EndpointLimits says whether an account and model are already known to
	// have reached a usage limit, which is what lets a run pinned to a developer
	// slot's endpoint pair start on its alternate without first being refused on
	// its primary (developerrouting.go). It is optional: a run wired without one
	// tries its primary and switches only once the provider refuses it.
	EndpointLimits EndpointLimits
	// DivergedTargets is the product's record of the target branches the harness
	// will not catch up to the remote's, written by the run whose promotion is
	// refused on one and lifted by the convergence sweep that finds the branches
	// converged. It is optional: a run wired without one stops exactly as it
	// would have, and what is lost is the record a watching session reads to
	// stop pulling items into the same refusal.
	DivergedTargets DivergedTargets
	// LaunchSettings is the product's record of a developer's provider that did
	// not put in force what a developer is launched with, written by the
	// dispatch whose check found it and lifted by the first check that finds the
	// settings in force again. It is optional; see LaunchSettingsHolds.
	LaunchSettings LaunchSettingsHolds
	// Selection is why this pipeline is running what it runs: who chose the work
	// and on what grounds. It is recorded with the run so that an operator reading
	// what is in flight can see why each item was picked, which is the question
	// with no answer at all once something other than them does the picking. It is
	// also what tells this pipeline whether an intake hold applies to it.
	//
	// The zero value is a pipeline that cannot account for its choice. It is
	// permitted — a caller that says nothing records nothing rather than being
	// refused — and it is treated as the harness choosing rather than the
	// operator, because unaccounted work is the case the hold exists to catch.
	Selection runstate.Selection
	// Reports is where what this run's agents noticed is collected. It is
	// optional: a pipeline wired without one still runs exactly as it would
	// have, and a report it cannot keep is named on the outcome rather than
	// disappearing quietly.
	Reports ReportCollector
	// Amendments is where changes this run's agents propose to documents they do
	// not own are kept, so the argument outlives the run that made it. It is
	// optional in the same way as the reports and for the same reason: a proposal
	// decides nothing about the run, so a pipeline wired without one still runs
	// exactly as it would have and names the proposal it could not keep.
	Amendments AmendmentRecorder
	// Prices puts the price of this work item on the item itself as a run for it
	// ends. It is optional in the same way and for the same reason: a run is not
	// worth failing over a number nobody could write down, so a pipeline wired
	// without one runs exactly as it would have and one that could not record a
	// price names that on the outcome.
	Prices Pricer
	// Spend is the cost log every provider invocation this run makes lands in,
	// one line each at the moment its cost is known. It is optional in the same
	// way as the three above and for a narrower reason: a pipeline wired without
	// one runs exactly as it would have, and what it loses is the only record of
	// what the run spent that does not have to be read back out of an event log.
	// The harness always wires it; a test that does not care about money does not
	// have to.
	Spend SpendLog
	// Docket is where a run that ends on a durable blocker is put in front of the
	// development manager. It is optional in the same way as the three above: a
	// stoppage is already recorded on the work item and in this run's own record
	// by the time it is docketed, so a pipeline wired without one stops exactly
	// as it would have and loses only the delivery.
	Docket *Docketer
	// Landings is where a landing's checks are given a checkout of the integrated
	// commit. It is optional: a pipeline wired without one lands exactly as it
	// would have, and what is lost is the landing checks, which the run's record
	// says could not run rather than saying nothing.
	Landings LandingCheckouts
	// Filer is what a red landing files its work item through. It is optional in
	// the same way: a red landing nothing can file is still recorded and reported
	// as red, with the record saying no item could be filed and why.
	Filer WorkFiler
	// ConfigReaders is what the running parts of the product recorded about the
	// configuration keys their builds read. A landing asks it, once the run is
	// over, which running parts cannot read the configuration the landing left,
	// and names each on the item and in the outcome. It is optional: a pipeline
	// wired without one lands exactly as it would have and names nothing.
	ConfigReaders ConfigReaders
	Clock         execution.Clock
	// Sleep waits out a usage-limit pause. It is a field so a test can drive a
	// pause without spending the real time, and so the wait is always cut short
	// by a cancelled context rather than holding the process past a shutdown.
	Sleep    func(ctx context.Context, duration time.Duration) error
	NewRunID func() (string, error)
	// Accounts chooses which of the configured provider accounts a fresh run is
	// served by, and is optional. A pipeline wired without one runs every run
	// under the configuration's single account, which is what a project with one
	// has and is exactly the behaviour there was before pooling existed.
	//
	// It is consulted once, when a run is started. Everything after that reads the
	// alias the run recorded: a resumed run, a repair attempt, and the review of
	// the change are all the same run, and a run that changed account mid-flight
	// would leave half its spend on one subscription and half on another with
	// nothing saying so.
	Accounts AccountChooser
	// StateRoot is where everything durable that is not the repository lives. The
	// pipeline needs it because a pooled alias authenticates in a provider home
	// under it, and that path is this machine's business rather than the
	// configuration's — which is why it is wired here rather than written down in
	// a file that is versioned with the repository.
	StateRoot  string
	Repository string
	Config     config.Config
	// ConfigPath is the configuration file this pipeline's configuration was read
	// from. It is held beside the resolved values because a project keeps its own
	// workflow definitions beside that file, exactly as it keeps its personas
	// there: what a run executes is the project's own copy of the delivery
	// definition where it has one, and a pipeline that does not know where its
	// configuration is finds none and executes the definition this build ships.
	ConfigPath string
	// Build is the repository revision this harness binary was built from,
	// recorded on every run this pipeline reserves. It is wired in rather than
	// read here because which binary is executing is a fact about the process the
	// command started, exactly as the state root is, and because a pipeline that
	// read it for itself would be one no test could drive.
	//
	// A caller that supplies none records none, which is what a binary carrying no
	// revision of its own leaves behind: a comparison nobody can make, rather than
	// a run that is current.
	Build        string
	RedactValues []string
}

// AccountChooser picks the provider account the next run is served by. It is an
// interface rather than the configuration's own method because choosing needs
// evidence the configuration does not hold — what each account has already spent
// this week, and which one the last run used — and that evidence is in the run
// records.
type AccountChooser interface {
	ChooseAccount() (config.AccountEndpoint, error)
}

// chooseAccount is the account a fresh run is served by. A pipeline with no
// chooser wired runs under the configuration's single account, which is what a
// project with one has; a pooled configuration with no chooser names no account
// and is refused here, before anything is claimed, rather than starting a run
// nothing could attribute.
func (p Pipeline) chooseAccount() (config.AccountEndpoint, error) {
	if p.Accounts != nil {
		return p.Accounts.ChooseAccount()
	}
	return p.Config.Endpoint(p.StateRoot, p.Config.AccountAlias())
}

// reserveRun settles which account serves this run and records the run that will
// spend it, with the pool's rotation lease held across both. It answers with the
// state as it was recorded, because the alias and the moment are settled here
// rather than by the caller that built the rest of it.
//
// Holding the two together is the whole point. The pool reads its cursor out of
// the run records and the reservation is what writes the record that moves it,
// so two starts that read before either wrote would be served by the same
// account — a pool double-serving under exactly the concurrency it exists for.
// The lease is released the moment the record exists, which is as soon as the
// next start could read it: a run is affined to its account for its whole
// length, but the rotation only has to be exclusive for the choosing.
//
// When the run started is read inside the lease rather than before it, because
// the cursor is the account the latest-started run recorded and that is only the
// account chosen last if the two orders agree. Three starts that queued in one
// order and were dated in another would leave the cursor on a run somebody had
// already rotated past, and the account behind it would be handed out twice —
// the same race the lease closes, arriving through the dates instead.
//
// A project with one account is not rotating anything, so it takes no lease and
// starts exactly as it did before pooling existed.
func (p Pipeline) reserveRun(ctx context.Context, state runstate.State) (runstate.State, *runstate.Lease, error) {
	if p.Config.Pooled() {
		rotation, err := p.Store.LeaseRotation(ctx)
		if err != nil {
			return state, nil, err
		}
		defer rotation.Release()
	}
	now := p.clock().Now()
	state.StartedAt, state.UpdatedAt = now, now
	// Why this item was chosen is written with the run and never rewritten, dated
	// from the moment the run was reserved — which is when the choice took effect.
	// A caller that said nothing records nothing, which is reported afterwards as
	// a run nothing accounted for rather than as a run whose reason was empty.
	if selection, stated := p.Selection.Stamped(now); stated {
		state.Selection = &selection
	}
	account, err := p.chooseAccount()
	if err != nil {
		return state, nil, fmt.Errorf("choose the provider account for this run: %w", err)
	}
	state.AccountAlias = account.Alias
	// Which model this run's developer invocations ask for is settled here too,
	// and for the same reason the account is: the labels the item was pulled with
	// are in hand, the mapping is configuration and configuration is edited, and
	// every invocation this run goes on to make reads the answer back off the
	// record rather than resolving it again. A project that configured no mapping
	// chooses nothing and records nothing, and its runs ask for the developer's
	// configured model exactly as they always did.
	if choice := config.ResolveDeveloperModel(p.Config.Execution.DeveloperModels, state.WorkItemLabels, p.developer().Model); choice.Chosen() {
		state.DeveloperModel, state.DeveloperModelReason = choice.Model, choice.Reason
	}
	// The effort level is settled beside the model, from the developer agent's
	// configuration, so an edit to it reaches the next run and never one already
	// in flight. A mapped model keeps the agent's level: the mapping chooses the
	// model, and nothing in it is a decision about how hard it is asked to think.
	effortModel := state.DeveloperModel
	if effortModel == "" {
		effortModel = p.developer().Model
	}
	state.ProviderEffort = p.Config.InvocationEffort(p.developer(), effortModel)
	state.EffortSettled = true
	lease, err := p.Store.Reserve(ctx, state, p.Config.Execution.MaxConcurrentDevelopers)
	if err != nil {
		// The wrapping is the reservation's own, so that what a caller reports about
		// a run it could not start still says which of the two steps refused it.
		return state, nil, fmt.Errorf("reserve developer run: %w",
			refusedByEnvironment("the run could not be reserved in durable state", err))
	}
	return state, lease, nil
}

// accountFor is where an alias a run already recorded authenticates. An alias
// the configuration no longer declares is still the alias that run spent, and
// the run is not worth failing over a mapping edited underneath it: the
// invocation is made where that alias authenticates, and the record goes on
// saying which account it was.
func (p Pipeline) accountFor(alias string) config.AccountEndpoint {
	if endpoint, err := p.Config.Endpoint(p.StateRoot, alias); err == nil {
		return endpoint
	}
	return config.AccountEndpoint{Alias: alias, Directory: config.AccountConfigDirectory(p.StateRoot, alias)}
}

// Preservation is what the harness actually found of a failing run's own branch
// and checkout, looked at as the run's failure was written down rather than
// inferred from the record.
//
// It exists because the record cannot answer the question the note asks. A run's
// state names the branch and the worktree it made and carries a flag for each
// removal the harness performed, so a note derived from it says "preserved"
// whenever no removal was recorded — which is a claim about what the harness did
// and not about what is there. On 2026-09-03 a run that died on a provider
// output bound wrote exactly that note, naming its worktree and 23 files of
// uncommitted work; the checkout was retired hours later by the convergence
// sweep, and the next developer read the note, went to the path, found nothing,
// and reported the work destroyed. It was not — the sweep had recorded it on a
// run-scoped ref — but nothing the developer could read said so.
// `docs/diagnoses/yoyodyne-ifd-275-preservation-claimed-without-a-check.md` is
// that sequence established from the records and the code.
//
// So this says only what was seen. Unverified is why nothing could be seen at
// all, which is a third answer rather than a missing artifact: a check that
// could not be made must not be rendered as either preservation or loss.
//
// The removal flags are the fourth answer, and they are here because the same
// false claim runs the other way. A run that promoted its work, cleaned up after
// it, and then failed has a branch and a worktree that are legitimately gone —
// the integrated commit is what survives — and an observation alone cannot tell
// that from an artifact something took. Reported as a loss it would send a reader
// after a branch and a preserved-work ref for work that is in the target branch,
// which is this item's own defect pointed backwards. So an artifact whose earned
// removal the record already carries is not checked and never reported lost.
type Preservation struct {
	Branch string `json:"branch,omitempty"`
	// BranchRemoved and WorktreeRemoved are the run record's own account of a
	// removal this run earned: its promotion's cleanup, in the only phase that can
	// reach one. Neither is ever set from an observation — that is what the
	// present flags are — and each one says the artifact beside it is gone on
	// purpose.
	BranchRemoved   bool   `json:"branch_removed,omitempty"`
	BranchPresent   bool   `json:"branch_present"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	WorktreeRemoved bool   `json:"worktree_removed,omitempty"`
	WorktreePresent bool   `json:"worktree_present"`
	Unverified      string `json:"unverified,omitempty"`
}

// Verified reports that nothing stopped the check the note needed. It is true
// where the check was made and true where there was nothing left to check,
// because both leave the note with no unanswered question; Unverified is only
// ever a check that was wanted and could not be made.
func (p Preservation) Verified() bool { return strings.TrimSpace(p.Unverified) == "" }

// Lost reports an artifact the run recorded making, nothing recorded removing,
// and the check did not find. That is the state this whole type exists to make
// sayable, and all three parts of it are load-bearing: an artifact this run's own
// cleanup removed is gone on purpose and is not a loss.
func (p Preservation) Lost() bool {
	if !p.Verified() {
		return false
	}
	return p.branchLost() || p.worktreeLost()
}

func (p Preservation) branchLost() bool {
	return p.Branch != "" && !p.BranchRemoved && !p.BranchPresent
}

func (p Preservation) worktreeLost() bool {
	return p.WorktreePath != "" && !p.WorktreeRemoved && !p.WorktreePresent
}

// checkable reports an artifact whose fate the record does not already settle,
// which is the only kind there is anything to look for.
func (p Preservation) checkable() bool {
	return (p.Branch != "" && !p.BranchRemoved) || (p.WorktreePath != "" && !p.WorktreeRemoved)
}

type Outcome struct {
	Retirement   *runstate.RunRetirement `json:"retirement,omitempty"`
	RunID        string                  `json:"run_id"`
	WorkItemID   string                  `json:"work_item_id"`
	Status       runstate.Status         `json:"status"`
	Phase        runstate.Phase          `json:"phase,omitempty"`
	Branch       string                  `json:"branch,omitempty"`
	WorktreePath string                  `json:"worktree_path,omitempty"`
	BaseCommit   string                  `json:"base_commit,omitempty"`
	// continuationAccepted is set only after an adopted run has passed its
	// resume preconditions. A pre-adoption pause can name an existing run
	// without accepting it.
	continuationAccepted bool
	// Preservation is what was actually found of the branch and the worktree
	// above when this run failed. It is present on a failed run that made either
	// of them and absent everywhere else, because a run that made neither has
	// nothing to claim and a run that succeeded had its artifacts cleaned up by
	// the step that earned the removal.
	Preservation *Preservation `json:"preservation,omitempty"`
	// ProviderSessionID identifies the developer session; ReviewSessionID
	// identifies the separate reviewer session that judged its work. The model
	// pairs are the requested selector and what the provider reported serving.
	ProviderSessionID      string          `json:"provider_session_id,omitempty"`
	ProviderModel          string          `json:"provider_model,omitempty"`
	ProviderResolvedModel  string          `json:"provider_resolved_model,omitempty"`
	ProviderEffort         string          `json:"provider_effort,omitempty"`
	ProviderResolvedEffort string          `json:"provider_resolved_effort,omitempty"`
	ProviderEffortReported bool            `json:"provider_effort_reported"`
	Checks                 []checks.Result `json:"checks,omitempty"`
	// CheckStage is the check stage the checks above ran in: its bound and what
	// it spent, and the narrowing every check was told. It is on the outcome so
	// the notes the item carries say what the stage cost against what it was
	// allowed, beside the per-check figures.
	CheckStage *runstate.CheckStage `json:"check_stage,omitempty"`
	// LandingChecks is what the landing checks made of the integrated commit,
	// once the run was over. It is absent from a run that integrated nothing and
	// from a project that configured no landing checks.
	LandingChecks *runstate.LandingChecks `json:"landing_checks,omitempty"`
	// ConfigMismatches is every running part of the product whose build cannot
	// read a key in the configuration this landing left, as the landing found
	// them once the run was over. It is absent where every part reads the file.
	ConfigMismatches []runstate.ConfigMismatch `json:"config_mismatches,omitempty"`
	// TemplateConfigMismatches names parts that could not adopt the new keys
	// this landing adds to shipped templates. Their active files may be healthy.
	TemplateConfigMismatches []runstate.ConfigTemplateMismatch `json:"template_config_mismatches,omitempty"`
	// ConfigComparison includes saved read problems and any delivery still owed.
	ConfigComparison *runstate.ConfigComparison `json:"config_comparison,omitempty"`
	Changes          gitworktree.ChangeSummary  `json:"changes"`
	Summary          string                     `json:"summary,omitempty"`
	// Reports are what this run's agents noticed and reported while their work
	// carried on: risks worked around, assumptions that may not hold, things
	// outside the assigned work. They are collected beside the run rather than
	// on it, so nothing here decided anything about what the run did.
	// ReportProblem names a report that could not be read or could not be kept,
	// because a report nobody collected would otherwise leave no trace at all.
	Reports       []report.Report `json:"reports,omitempty"`
	ReportProblem string          `json:"report_problem,omitempty"`
	// Amendments are the changes this run's agents proposed to documents they do
	// not own. Like the reports they are recorded beside the run and decided
	// nothing about it: each one is waiting on the role that owns the document or
	// on the operator, and nothing was written to any document.
	// AmendmentProblem names a proposal that could not be read or could not be
	// kept, because a proposal nobody recorded would otherwise leave no trace.
	Amendments       []amendment.Proposal `json:"amendments,omitempty"`
	AmendmentProblem string               `json:"amendment_problem,omitempty"`
	// Landing is what the developer claimed its change does to the work item, and
	// LandingReason is its own account of the claim. Unlike the two channels above
	// this one decides something: an item is closed on a landing that discharges
	// it and left open on one that does not, so a run that landed evidence reads
	// afterwards as the evidence it was rather than as work that was done.
	// LandingBlockedBy is the impediment a landing named to have its item left
	// open waiting on that work rather than parked, resolved against the tracker,
	// and is empty for the parking default and for every landing that discharges.
	// LandingImpedimentProblem is why a marker the landing carried was not one the
	// item could be made to wait on, which is what put it in the parking instead.
	// LandingProblem names a claim that could not be read, which withholds the
	// closure for the reason its durable twin gives.
	Landing                  landing.Outcome `json:"landing,omitempty"`
	LandingReason            string          `json:"landing_reason,omitempty"`
	LandingBlockedBy         string          `json:"landing_blocked_by,omitempty"`
	LandingImpedimentProblem string          `json:"landing_impediment_problem,omitempty"`
	LandingProblem           string          `json:"landing_problem,omitempty"`
	// Cost is what every run made for this work item has cost, as the provider
	// reported it: this run and every earlier one, the attempts that failed as
	// well as the one that finished. It is absent when nothing priced the item,
	// and CostProblem names why when something tried and could not.
	Cost        *beads.Cost `json:"cost,omitempty"`
	CostProblem string      `json:"cost_problem,omitempty"`
	// ProviderOutageProblem names a provider outage this run met that could not
	// be recorded on the product, or a provider answering again that could not be
	// cleared from it. The run waited or carried on exactly as it would have;
	// what was lost is the record the surfaces name the wait from.
	ProviderOutageProblem string `json:"provider_outage_problem,omitempty"`
	// CapacityServedProblem names every served invocation of this run that could
	// not be recorded as served, joined. The run carried on exactly as it would
	// have; what was lost is the evidence that would have read an earlier refusal
	// of that account and model as lifted before its quoted reset.
	CapacityServedProblem string `json:"capacity_served_problem,omitempty"`
	// DivergedTargetProblem names a diverged target this run stopped on that
	// could not be recorded on the product. The run stopped exactly as it would
	// have and its blocker says so; what was lost is the record a watching
	// session holds its choosing on.
	DivergedTargetProblem string `json:"diverged_target_problem,omitempty"`
	// Invariants names the architectural invariants this run delivered to its
	// developer and to its reviewer. It is the audit record of which durable
	// constraints the change was actually held to, which is the thing a
	// transcribed constraint in a bead could never say afterwards.
	// InvariantProblems names what the delivered set was missing: a file in the
	// invariants directory that could not be read as one, or an invariant that
	// matched and did not fit the prompt's bound. Both mean the set the agents saw
	// was incomplete, which is a fact for the operator rather than a run failure.
	Invariants           []string `json:"invariants,omitempty"`
	InvariantProblems    []string `json:"invariant_problems,omitempty"`
	ReviewSessionID      string   `json:"review_session_id,omitempty"`
	ReviewModel          string   `json:"review_model,omitempty"`
	ReviewResolvedModel  string   `json:"review_resolved_model,omitempty"`
	ReviewEffort         string   `json:"review_effort,omitempty"`
	ReviewResolvedEffort string   `json:"review_resolved_effort,omitempty"`
	ReviewEffortReported bool     `json:"review_effort_reported"`
	// ReviewBaseCommit and ReviewHeadCommit are the commits the reviewed change
	// was measured between — the run's base and the branch's tip at the review —
	// so the record of a verdict names what it was judged against.
	ReviewBaseCommit string          `json:"review_base_commit,omitempty"`
	ReviewHeadCommit string          `json:"review_head_commit,omitempty"`
	ReviewDecision   review.Decision `json:"review_decision,omitempty"`
	// ReviewApproves is what the reviewer said its approval approves. It decides
	// the closure alongside the developer's claim above: an approval of evidence
	// leaves the item open exactly as an evidence landing does, whatever the
	// developer claimed.
	ReviewApproves review.Approval  `json:"review_approves,omitempty"`
	ReviewSummary  string           `json:"review_summary,omitempty"`
	ReviewFindings []review.Finding `json:"review_findings,omitempty"`
	// RepairAttempts counts the times this run returned a failure to the
	// developer, whether it was a failing check or the reviewer's findings; the
	// two share one budget. Blocked reports that the budget was spent and what
	// remained unresolved was recorded on the work item.
	RepairAttempts int `json:"repair_attempts,omitempty"`
	// IntegrationRetries counts the promotions this run re-prepared after losing
	// a race for its target branch. Each one replayed the change onto where the
	// target went and put it back through the checks and a fresh independent
	// review, so it is evidence about the target moving rather than about the
	// change being wrong.
	IntegrationRetries int `json:"integration_retries,omitempty"`
	// ChargedReplays counts the replays that stopped on the change — conflicted,
	// failed their checks, or drew a repair verdict — which are the only replays
	// execution.integration_retries_before_reconciliation bounds. A lost race
	// whose replay passed is in IntegrationRetries and not here.
	ChargedReplays int `json:"charged_replays,omitempty"`
	// TransientRelaunches counts the provider invocations this run reissued after
	// one died without judging the work. It is evidence about the provider rather
	// than about the change or the target branch, and a run reporting some and
	// finishing anyway is the whole point of the budget: the deaths cost nobody
	// anything. Blocked reports the budget spent, with what killed the last
	// attempt recorded on the work item.
	TransientRelaunches int `json:"transient_relaunches,omitempty"`
	// Retries are the recoverable failures this run waited out and asked again —
	// a reset connection at a publish, at a merge, or under a provider — each with
	// the boundary it happened at and the interval that was waited. A run
	// reporting some and succeeding anyway is what yoyodyne-ifd.264 is for: before
	// it, one of these recorded completed and sometimes reviewed work as failed.
	Retries []runstate.Retry `json:"retries,omitempty"`
	Blocked bool             `json:"blocked,omitempty"`
	// Environmental is the environment refusing this round rather than the work
	// failing: which environmental cause the run recorded, and what the settle
	// gave back once it found the round had delivered nothing. It is the one
	// stoppage that leaves the item's budgets exactly where they were, so a caller
	// that reported the blocker without it would say an item had spent another
	// round toward its cap when it had spent none.
	Environmental *runstate.EnvironmentalRefusal `json:"environmental,omitempty"`
	// Verification is what the developer recorded executing against this change:
	// the probe it ran before it changed anything, and the checks it ran against
	// the change itself. It is here rather than only in the durable record
	// because it is what says a change reached a reviewer having been run, and a
	// caller reporting the run without it reports a change nobody can tell was
	// ever executed from one that was.
	Verification *runstate.Verification `json:"verification,omitempty"`
	// IntegrationStop is the environment having stopped this run's approved
	// change short of its promotion, when that is what stopped it: the one
	// failure that is resumable at the step it stopped in, with the approval
	// standing and nothing charged. A caller that reported the failure without it
	// would send a reader to the verbs that each spend something for it.
	IntegrationStop *runstate.IntegrationStop `json:"integration_stop,omitempty"`
	// ReplayConflict is this run's approved change having conflicted when it was
	// replayed onto its target, when that is what stopped it: the one stop after
	// an approval that a person settles rather than the harness resumes past. It
	// is reported beside the failure so a reader shown a run that ended at its
	// promotion is not sent to the resume verb, which would meet the conflict
	// again.
	ReplayConflict *runstate.ReplayConflict `json:"replay_conflict,omitempty"`
	// Paused reports a run that stopped short of finishing and is owed a
	// continuation rather than having failed. The run is still in flight when it
	// is set: its worktree, branch, claimed item, and developer session are all
	// preserved. Four things pause a run, and they are told apart by which of the
	// fields below is set: an exhausted provider usage limit, whose deadline says
	// when the run becomes runnable again; a provider invocation the harness
	// stopped on time, which is runnable immediately; an unresolved user
	// directive, which is runnable once somebody settles the directive; and the
	// operator's hold on all harness activity, which is runnable once they lift it.
	//
	// The directive and the hold are the two that can pause work before there is a
	// run at all. Nothing is claimed and no worktree exists in that case, so the
	// paused outcome names the work item and what stopped it and nothing else.
	Paused bool `json:"paused,omitempty"`
	// PausedByDirective is the unresolved directive this run or this work item
	// stopped short for. It carries the directive itself rather than only its
	// identifier, because what an operator has to do about it — answer the
	// question, or decide the artifact change — is in the directive's own words.
	PausedByDirective *directive.Directive `json:"paused_by_directive,omitempty"`
	// PausedByDependency is the unfinished work this run or this work item stopped
	// short for. It carries the blocking items themselves rather than only saying
	// there are some, because what somebody has to do about it — close that work,
	// or unlink it — is a decision about those items by name.
	//
	// Like the directive it can appear with no run behind it, on work a dependency
	// stopped before anything was claimed, and unlike the directive what lifts it
	// is other work finishing rather than a person deciding.
	PausedByDependency *runstate.DependencyPause `json:"paused_by_dependency,omitempty"`
	// PausedByTracker is the unanswered tracker read this run parked on: the gate
	// boundary whose read went unanswered for the whole of its recovery window,
	// what the window was spent on, and what the store last said. It carries the
	// park itself rather than only saying there is one, because whether the store
	// was contended or broken is the whole of what somebody would act on.
	//
	// Unlike the two above it never appears with no run behind it: it is a run
	// that got as far as a gate and could not find out whether it may take the
	// next step.
	PausedByTracker *runstate.TrackerPause `json:"paused_by_tracker,omitempty"`
	// PausedByOperator is the operator's hold on all harness activity, present on
	// a run parked at a provider-call boundary for it and on work this process
	// declined to start while it was in force. It carries the hold itself rather
	// than only saying there is one, because when it was placed is what tells an
	// operator whether they are looking at a system they paused or one that died.
	PausedByOperator *runstate.OperatorHold `json:"paused_by_operator,omitempty"`
	// PausedByIntake is the operator's hold on the work the harness chooses for
	// itself, present on work this pipeline declined to start because of it.
	// Unlike the four pauses above it never appears on a run: what it holds is the
	// choosing, so there is nothing claimed, nothing developed, and nothing to
	// resume — only an item that was not started and says why.
	PausedByIntake *runstate.IntakeHold `json:"paused_by_intake,omitempty"`
	// UsageLimitResetsAt is when the provider said the exhausted limit resets,
	// and UsageLimitKind is the provider's own name for it. They are reported on
	// a paused run and on a run that stopped because the reset was unusable.
	UsageLimitResetsAt *time.Time `json:"usage_limit_resets_at,omitempty"`
	UsageLimitKind     string     `json:"usage_limit_kind,omitempty"`
	// PauseCause is which refusal that deadline is being waited out for, one of
	// the runstate.Pause constants. A transiently overloaded server and an
	// exhausted account both park a run on a deadline, and only this tells a
	// reader which of them they are looking at.
	PauseCause string `json:"pause_cause,omitempty"`
	// ProviderOutageChannel is where the provider's refusal was read on a run
	// waiting out an outage: the terminal of its stream, or its process's stderr
	// or plain stdout because it refused before writing one. Evidence, reported
	// for the reason the kind above is.
	ProviderOutageChannel domain.ProviderChannel `json:"provider_outage_channel,omitempty"`
	// ProviderStop names why the harness stopped a provider invocation on time
	// rather than the provider ending it: runstate.ProviderStopStalled when it
	// stopped emitting events, runstate.ProviderStopBudgetExhausted when it was
	// still live and out of budget. It is never a report of failure by the agent.
	ProviderStop string `json:"provider_stop,omitempty"`
	// RedeployStop is the run having been stopped by the watch session hosting
	// it, so that session could restart into a build deployed over it once its
	// drain bound ran out. Like ProviderStop it is the harness's own clock and
	// never a report by the agent; the run is in flight and owed a continuation
	// from the phase recorded on it.
	RedeployStop *runstate.RedeployStop   `json:"redeploy_stop,omitempty"`
	Integration  *gitworktree.Integration `json:"integration,omitempty"`
	// PullRequest is the published pull request, present only on a run that
	// published one. PublishSkipped says why a run that asked to publish did not,
	// which is a repository with no configured remote and nothing else.
	PullRequest    *runstate.PullRequest `json:"pull_request,omitempty"`
	PublishSkipped string                `json:"publish_skipped,omitempty"`
	// TargetProtection says what the forge answered about the target branch
	// being protected, and so which way this run promoted: a protected target —
	// or one the forge could not be asked about — lands through the pull request
	// and never moves the local target, and an unprotected one is promoted
	// locally first. It is set by a publishing run that reached its promotion.
	TargetProtection string `json:"target_protection,omitempty"`
	// PublishFailure reports a promotion that could not be published. On an
	// unprotected target the local target branch is the authoritative one and it
	// already moved, so this is an outstanding publication rather than a failed
	// run. A landing through the pull request moved no local branch: there it is
	// either bookkeeping left after a merge the forge made, or — where the forge
	// did not merge — the reason the run was stopped and the item blocked.
	PublishFailure string `json:"publish_failure,omitempty"`
	// Catchup is the local aftermath of a merge the forge performed: the target
	// branch brought onto the merge commit the forge made above the promotion,
	// so the checkout a person reads carries what the forge has. It is recorded
	// rather than made durable because it is idempotent and unowned — `yoyo
	// reconcile` sweeps every target branch and would do it again — so a
	// catch-up that was held is a fact to report, not outstanding work anybody
	// has to track.
	Catchup *gitworktree.Catchup `json:"catchup,omitempty"`
	// DivergedTarget is the catch-up the harness would not make, when that is
	// what stopped this run: the remote target does not contain the local one,
	// or the primary checkout holds work the catch-up would overwrite, and only
	// a person can say which history is right. It is set beside the blocker that
	// hands the item to that person, before or after the local promotion. The
	// stop is the environment's rather than a verdict on the change, and unlike
	// an IntegrationStop it is not one the harness resumes from — so it is not
	// that record, and what reads it is the brake, which counts a stop of this
	// class toward nothing: three of them tripped it on 2026-09-21 over one
	// divergence the item's own blocker had already put in front of a person.
	DivergedTarget *gitworktree.Catchup `json:"diverged_target,omitempty"`
	WorkItemClosed bool                 `json:"work_item_closed"`
	// WorktreeRemoved and BranchRemoved report each artifact separately, because
	// cleanup removes them in two steps and a partial result must not describe
	// a deleted artifact as remaining or a surviving one as gone.
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchRemoved   bool   `json:"branch_removed"`
	Failure         string `json:"failure,omitempty"`
	// StopClass is which gate stopped the run, exactly as the run's record
	// carries it: it is set where the record's is, and never anywhere else.
	StopClass runstate.StopClass `json:"stop_class,omitempty"`
	// CleanupFailure is set when the run completed but its post-completion
	// cleanup did not finish cleanly. The work is integrated and the item is
	// closed either way, so this is evidence for reconciliation rather than a
	// run failure. It covers two different situations, which WorktreeRemoved and
	// BranchRemoved tell apart: an artifact that survives, when either flag is
	// false, and a removal that succeeded but could not be confirmed afterwards,
	// when both are true. Only the first leaves something to remove, so a report
	// must read these flags rather than infer leftovers from this field alone.
	CleanupFailure string `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure is set when the run completed, both artifacts
	// were removed and confirmed gone, and only the final completion record
	// could not be written. It is deliberately distinct from CleanupFailure:
	// cleanup itself reported nothing wrong, so nothing must be reported as
	// remaining or as unconfirmed.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
}

// Ending is what became of this run, in the fixed vocabulary every surface says
// it in. It is the read model's own derivation over the two facts an outcome
// carries — the status the run reached, and whether it left somebody a blocker —
// rather than a second reading of them here, so a run described from the outcome
// it returned and the same run described from its durable record cannot be given
// two different words.
//
// Blocked is the outcome's half of State.Blocker: both are written together and
// only once the tracker has taken the blocker, so a stoppage the tracker refused
// is not claimed as one on either side.
func (o Outcome) Ending() runstate.RunOutcome {
	return runstate.Ending(o.Status, o.Blocked)
}

type ExistingRunError struct {
	State runstate.State
}

func (e ExistingRunError) Error() string {
	return fmt.Sprintf("work item %s already has incomplete run %s in status %s", e.State.WorkItemID, e.State.RunID, e.State.Status)
}

// validateDispatch asks what every dispatch must be able to answer before it
// touches a work item at all: that this process is configured to run one, with a
// developer it can invoke, a model named for it, checks to put the change
// through, and a review policy where the policy decides integration. It is
// stated apart from the entry points because both of them ask it — a
// continuation invokes the same roles a fresh run does, and a harness that
// cannot run one cannot continue one either.
func (p Pipeline) validateDispatch() error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := p.Config.Validate(); err != nil {
		return err
	}
	developer := p.developer()
	if !p.runsOnCompiledAdapter(developer.Backend) {
		return fmt.Errorf("run pipeline requires a developer on a backend this build can launch, configured backend is %q", developer.Backend)
	}
	// Every invocation names its own model; the harness never lets a provider
	// pick one for it, so the run evidence always says what actually ran.
	if err := config.ValidateModelSelector(developer.Model); err != nil {
		return fmt.Errorf("developer agent %s", err)
	}
	if len(p.Config.Checks) == 0 {
		return errors.New("run pipeline requires at least one configured check")
	}
	if p.automatic() {
		if err := p.validateReviewPolicy(); err != nil {
			return err
		}
	}
	return nil
}

// EnvironmentRefusedError is a start the machine refused before the work was
// ever attempted: the checkout, the state store, the invariants directory, the
// shell — something that would have refused whatever item was chosen, exactly as
// completely. It says nothing about the item it happened to fall on.
//
// It is declared here, by the step that refused, rather than inferred by
// whatever collects the failure. A collector can only ask questions it already
// knows to ask, and the classes it does not know to ask about are precisely the
// ones that arrive later — a sandbox that will not spawn a process is not
// something a repository readiness read has any way to see. What the scheduler
// does with the distinction is remember the item or not, and getting it wrong in
// this direction is what turns one broken machine into a backlog that reads as
// exhausted. So the answer comes from the site that has it.
//
// Condition is the operator-facing phrase — what cannot happen, not the stack it
// happened in. Error delegates to the wrapped failure verbatim, so wrapping a
// refusal in this changes what the harness knows about it and never changes what
// it says.
//
// It goes innermost, around the bare cause, with whatever context a site was
// already adding left wrapped around the outside. errors.As finds it at any
// depth, so nothing is lost by putting it there — and what is gained is that Err
// is the cause alone. A marker wrapped around a message that already names the
// condition would have the operator's line say the condition twice and then spend
// its length bound doing it, which is the reason-cut-off failure this item exists
// to end, arriving by the door marked report.
type EnvironmentRefusedError struct {
	Condition string
	Err       error
}

// Error and describe both tolerate a marker carrying no wrapped failure. It is
// not a shape any caller here builds, but this is read on the path where
// something has already gone wrong, and a panic while reporting a refusal would
// lose the refusal.
func (e EnvironmentRefusedError) Error() string {
	if e.Err == nil {
		return strings.TrimSpace(e.Condition)
	}
	return e.Err.Error()
}

func (e EnvironmentRefusedError) Unwrap() error { return e.Err }

// describe is the refusal in one operator-facing line: the condition this step
// named, and the machine's own words for what went wrong.
//
// The length bound falls on the cause alone. The condition is a fixed phrase
// written a few lines from here, so its length is known and it is not the part
// that can run away; the cause arrives from somewhere else at whatever length it
// likes. A bound spread across the pair spends the budget on the words that could
// have been guessed and truncates the ones nobody can — which is a line that says
// something has stopped without saying why, on exactly the refusals that most
// need it.
func (e EnvironmentRefusedError) describe() string {
	condition := strings.TrimSpace(e.Condition)
	if e.Err == nil {
		return condition
	}
	cause := singleLine(e.Err.Error(), maxBlockedDetailBytes)
	if condition == "" {
		return cause
	}
	return condition + ": " + cause
}

// refusedByEnvironment marks a failure as the machine's. It is a helper rather
// than a literal at each site so that adding a refusal to the pre-reservation
// path is one call rather than a decision somebody has to remember to make.
//
// Wrap the bare cause with it and leave any context the site was already adding
// on the outside — fmt.Errorf("...: %w", refusedByEnvironment(condition, err)) —
// so that the message stays exactly what it was and the condition is said once.
// The condition is what cannot happen in an operator's terms, and it should not
// restate the sentence the wrap around it already carries.
func refusedByEnvironment(condition string, err error) error {
	return EnvironmentRefusedError{Condition: condition, Err: err}
}

func (p Pipeline) Run(ctx context.Context, workItemID string) (Outcome, error) {
	if err := p.validateDispatch(); err != nil {
		return Outcome{}, err
	}
	// The operator's hold is read before anything else this command would do,
	// because it is the cheapest question here and the broadest answer: a held
	// harness starts nothing, claims nothing, and asks the provider nothing, not
	// even whether it is installed. A run already in flight is left in flight, and
	// it is left held rather than resumed into a boundary that would park it again.
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return Outcome{}, err
		}
		return p.holdWorkItem(workItemID, hold)
	}
	// Whether this run publishes is settled before anything is claimed, so a
	// project that asked for pull requests and cannot open one fails here rather
	// than after a developer has already produced work.
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return Outcome{}, err
	}

	// The read is waited out rather than taken once. It is the same store a gate
	// boundary reads, contended by the same processes, and a `bd` killed under
	// load here turned away a dispatch that had nothing wrong with it.
	item, err := p.readWorkItem(ctx, workItemID)
	if err != nil {
		return Outcome{}, fmt.Errorf("load work item: %w", err)
	}
	// What the operator has directed is read before anything is claimed, adopted,
	// or resumed. A directive that changes the artifact this work derives from, or
	// that nobody can act on until the operator says what they meant, stops the
	// work here rather than after a developer has already written a change against
	// intent that is being rewritten. Nothing has been claimed at this point, so
	// the item is simply left where it is, and a run already in flight for it is
	// left in flight rather than resumed.
	pausing, err := p.pausingDirectives(workItemID)
	if err != nil {
		return Outcome{}, err
	}
	if len(pausing) > 0 {
		return pauseWorkItem(workItemID, pausing[0]), nil
	}
	// What the item waits on is read at the same boundary, from the same freshly
	// loaded item, and for the same reason. A dependency link applied after this
	// item was selected is a gate somebody added to work that was already moving,
	// and a run that trusted the readiness selection saw would develop straight
	// through it — which is exactly what a link applied to an in-flight item is
	// filed to stop. Nothing has been claimed at this point, so the item is left
	// where it is, and a run already in flight for it is left in flight rather
	// than resumed.
	if blockers := blockingDependencies(item); len(blockers) > 0 {
		return pauseWorkItemForDependencies(workItemID, blockers), nil
	}
	// An incomplete run for this item is either a run an interrupted process left
	// behind, a run waiting out a provider usage limit, or a duplicate that must
	// be refused. Adopting it takes the same exclusive lease a fresh reservation
	// takes, so entering a run this process did not start can never put two
	// developers on one item; a run another process is still holding is reported
	// as existing rather than picked up. Only runs whose remaining work is fully
	// described by durable state are continued: the repair loop, a paused run that
	// recorded the deadline it is waiting on, and a run that recorded the directive
	// it stopped short for — which is reached only once that directive is settled,
	// because the directives were read a moment ago.
	inFlight, lease, err := p.Store.Adopt(ctx, workItemID)
	switch {
	case err == nil:
		defer lease.Release()
		// A usage-limit pause, a provider the harness stopped on time, a run held
		// up by a directive, one waiting on work its item depends on, and one the
		// operator parked are all resumable whatever the approval policy, because
		// none of them depends on the repair loop: the first has not had its
		// attempt served yet, the second is owed the rest of an attempt it was
		// making, and the rest are owed the rest of the step they stopped short of.
		// Nothing reaches here while the hold or the dependency is still in force,
		// so a run carrying one is a run whose reason to wait has gone.
		if !pausedForUsageLimit(inFlight) && !pausedForDirective(inFlight) && !pausedForDependency(inFlight) && !pausedForTracker(inFlight) && !pausedForOperatorHold(inFlight) && !stoppedProviderIsResumable(inFlight) && !stoppedForRedeployIsResumable(inFlight) && !continuedAtCheckStage(inFlight) && !(p.automatic() && resumableRepair(inFlight)) {
			return Outcome{}, ExistingRunError{State: inFlight}
		}
		inFlight, err = p.reclaimSlot(ctx, inFlight)
		if err != nil {
			return Outcome{}, err
		}
		return p.resumeRun(ctx, inFlight, item, publishing, skipped)
	case errors.Is(err, runstate.ErrNoRunInFlight):
	default:
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return Outcome{}, ExistingRunError{State: existing.State}
		}
		return Outcome{}, fmt.Errorf("adopt run in flight: %w",
			refusedByEnvironment("what is already in flight could not be read", err))
	}
	// Nothing is in flight for this item, so what follows would start something
	// new — which is the one thing an intake hold stops. It is asked here rather
	// than at the top because that is exactly the distinction the hold is for:
	// everything above this point either resumed a run or found none, and a run
	// already under way carries on while intake is held.
	if outcome, held, err := p.holdIntake(workItemID); err != nil || held {
		return outcome, err
	}
	// And what follows would start it clean, which for an item whose last run
	// stopped owing a repair is starting over on work that is waiting to be
	// continued. It is asked before the item's own readiness, because the item
	// having been put back is what lets this substitution past every other gate
	// and says nothing about whether starting over is the right thing to do.
	if err := p.refuseSubstitutedHandback(workItemID); err != nil {
		return Outcome{}, err
	}
	if err := validateReadyItem(item, workItemID); err != nil {
		return Outcome{}, err
	}
	// And whether its done-conditions are ones a run may satisfy at all, asked
	// here for the same reason the provider grant is asked above: the answer is
	// in the item's text, nothing has been claimed, and a run that started on
	// such an item spends itself finding out.
	if err := p.refuseUngrantedCondition(item); err != nil {
		return Outcome{}, err
	}
	if _, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item}); err != nil {
		return Outcome{}, fmt.Errorf("validate work item context: %w", err)
	}
	// The invariants are read before anything is claimed. A directory that cannot
	// be read at all is refused here rather than delivering nothing, because a
	// repository whose constraints silently failed to load looks exactly like one
	// that has none.
	invariants, err := p.loadInvariants()
	if err != nil {
		return Outcome{}, err
	}
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		return Outcome{}, fmt.Errorf("repository is not ready for an isolated run: %w",
			refusedByEnvironment("the repository is not ready for an isolated run", err))
	}
	// Everything above this point was answerable from the repository alone, so it
	// is answered first: a dirty checkout is a refusal a newcomer meets whether or
	// not they have installed Claude Code, and it names the files they have to
	// commit. Only now is the provider asked, and still before anything is
	// reserved, claimed, or cut. A slot with an endpoint pair is asked about the
	// provider its primary runs on, which is the one its first attempt asks.
	dispatchProvider, dispatchNamed, err := p.dispatchBackend(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if err := p.requireBackendReady(ctx, workItemID, dispatchProvider, dispatchNamed); err != nil {
		return Outcome{}, err
	}
	// And whether the provider puts in force what its developer would be
	// launched with — the sandbox, the notes guard, the settings that keep
	// personal configuration out — asked last for the reason the provider is
	// asked late, and still before anything is claimed (launchsettings.go).
	if err := p.requireLaunchSettings(ctx, "the dispatch of "+workItemID, dispatchProvider, dispatchNamed); err != nil {
		return Outcome{}, err
	}
	// An automatic run is written against exactly the branch it will be promoted
	// into, so the integration target is fixed before any work starts and never
	// inferred afterwards. A published run fixes the same branch for the same
	// reason: it is the base its pull request is opened against, and a pull
	// request whose base could still change is not describing one change.
	baseRef := "HEAD"
	targetBranch := ""
	if p.automatic() || publishing {
		targetBranch, err = p.Worktrees.CurrentBranch(ctx)
		if err != nil {
			return Outcome{}, fmt.Errorf("resolve the target branch: %w",
				refusedByEnvironment("the branch work would be promoted into could not be resolved", err))
		}
		baseRef = targetBranch
	}
	runID, err := p.NewRunID()
	if err != nil {
		return Outcome{}, refusedByEnvironment("a run identifier could not be generated", err)
	}
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     p.Config.Product.ID,
		RepositoryID:  string(p.Config.Product.RepositoryID),
		WorkItemID:    workItemID,
		// What the item is called is written with the run because this is where the
		// tracker's answer is in hand: everything that reads the record afterwards
		// reads only the record, so a title not copied here is a title nothing can
		// say the work by.
		WorkItemTitle: item.Title,
		// And what it carries, for the same reason: the developer slot a run
		// occupies is read off the labels the item was pulled with, by the status
		// as much as by the scheduler, and neither goes back to the tracker for it.
		WorkItemLabels: append([]string(nil), item.Labels...),
		Backend:        p.developer().Backend,
		// Which configuration set this run up is written with the run for the reason
		// the title is: this is where the answer is in hand, everything that reads
		// the record afterwards reads only the record, and a configuration is
		// edited. Which account it spends is written here too, by the reservation
		// below rather than by this literal, because the choice and the record that
		// moves the pool's cursor have to be one step. Under a pool that alias is
		// also what the run is affined to: every invocation this run goes on to make
		// reads it back off the record rather than asking the pool a second time.
		ConfigRevision: p.Config.Revision(),
		// And which harness dispatched it, written in the same breath and for the
		// same reason: a process runs whatever binary it was started with while the
		// harness moves on underneath it, so what a run's record says about the code
		// that made its decisions is a fact only this process holds. Without it a
		// run that behaved like a build from before the fix is indistinguishable
		// from a fix that does not work.
		Build:  p.Build,
		Status: runstate.StatusPending,
	}
	// Which account will serve this run is settled here, before anything is
	// claimed, so a pool with nothing left to spend refuses before a work item has
	// been taken and a worktree cut for it. When the run started, why it was
	// chosen, and which account it spends are all dated from inside the same step,
	// which is what the reservation is.
	state, reservation, err := p.reserveRun(ctx, state)
	if err != nil {
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return Outcome{}, ExistingRunError{State: existing.State}
		}
		return Outcome{}, err
	}
	defer reservation.Release()
	run := &activeRun{
		pipeline:   p,
		state:      state,
		outcome:    Outcome{RunID: runID, WorkItemID: workItemID, Status: runstate.StatusPending, PublishSkipped: skipped},
		item:       item,
		publishing: publishing,
		invariants: invariants,
	}

	// The run records its instance of the delivery definition here, standing on
	// the definition's first state, before the first thing this run changes
	// outside itself. Nothing about the run depends on it, and a project that
	// rolled back to the legacy path records none.
	//
	// The one thing that stops the run is the project's own definition being
	// wrong. It is refused here rather than anywhere later because here is where
	// refusing is free: nothing has been claimed, no worktree exists, and no
	// provider has been paid.
	if err := run.beginDeliveryTrial(); err != nil {
		return run.fail(err, runstate.StatusFailed)
	}
	// A run reserved for a developer slot with an endpoint pair records the slot
	// and the pair before it claims anything, and starts on the pair's primary
	// (developerrouting.go). Every other run is left exactly as reserved.
	if err := run.pinDeveloperRouting(ctx); err != nil {
		return run.fail(err, runstate.StatusFailed)
	}

	if err := run.claim(ctx); err != nil {
		run.observe(ctx, deliveryClaim, "unavailable")
		return run.fail(err, runstate.StatusFailed)
	}
	run.observe(ctx, deliveryClaim, "claimed")
	worktree, err := p.Worktrees.Create(ctx, gitworktree.CreateRequest{
		RunID:        runID,
		WorkItemID:   workItemID,
		BaseRef:      baseRef,
		TargetBranch: targetBranch,
	})
	if err != nil {
		if worktree.Path != "" {
			run.recordWorktree(worktree)
		}
		// A creation the harness's own budget ended is refused here rather than
		// from the failure alone, because here is the one place that knows nothing
		// of this round ran: the developer is invoked below, so a creation that did
		// not return has invoked nobody and left nothing anywhere to be measured
		// against. Every other creation failure is classified from the error like
		// any other, in fail.
		if errors.Is(err, gitworktree.ErrCheckoutKilled) {
			run.recordEnvironmentalRefusal(runstate.CauseWorktreeCheckoutKilled, err.Error(), nothingRan)
		}
		return run.fail(fmt.Errorf("create isolated worktree: %w", err), runstate.StatusFailed)
	}
	run.recordWorktree(worktree)
	if err := run.liftPreserved(ctx); err != nil {
		return run.fail(err, runstate.StatusFailed)
	}
	if err := run.prepareScratch(); err != nil {
		return run.fail(err, runstate.StatusFailed)
	}
	run.state.Status = runstate.StatusRunning
	run.state.Phase = runstate.PhaseDeveloping
	run.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(run.state); err != nil {
		return run.fail(fmt.Errorf("save running state: %w", err), runstate.StatusFailed)
	}
	run.outcome.Status = runstate.StatusRunning
	run.outcome.Phase = run.state.Phase

	if err := run.develop(ctx, developerPrompt(p.developer().Persona.Text, run.deliveredInvariants().Text(), run.context, run.scratch, p.Config.Checks), ""); err != nil {
		return run.stop(ctx, err)
	}
	return run.verifyReviewAndFinish(ctx)
}

// claim takes the work item this run was dispatched for and assembles the
// context its developer is given.
//
// It is the first thing a run changes outside itself: everything above it in Run
// is a question, and after this the item is claimed and a failure has to give it
// back. The three failures are wrapped separately and none of them is handled
// here, because what a caller does about a run that could not be started is the
// caller's — Run fails it, and the failure says which of the three it was.
func (a *activeRun) claim(ctx context.Context) error {
	item, cleared, err := a.pipeline.Tracker.Claim(ctx, a.state.WorkItemID)
	// Recorded before the error is judged, because the ending it matters most
	// on is the one that fails: a clear no read confirmed leaves the item for the
	// next pull, and the record is what says that is what happened rather than
	// the run dying at the claim for nothing anybody can read.
	a.state.StaleBlockClear = recordedStaleBlockClear(cleared)
	if err != nil {
		return fmt.Errorf("claim work item: %w", err)
	}
	a.claimed = true
	// Dated on the record as well as flagged on this process, because which side
	// of the claim a run died on is what says whether its failure left anything
	// behind — and the process that knows is gone by the time anybody asks.
	claimedAt := a.pipeline.clock().Now()
	a.state.WorkItemClaimedAt = &claimedAt
	a.item = item
	if err := validateClaimedItem(item, a.state.WorkItemID); err != nil {
		return fmt.Errorf("validate claimed work item: %w", err)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: a.pipeline.Repository, WorkItem: item, Specifications: a.pipeline.Config.Product.Specifications, IntentRoot: a.pipeline.Config.Product.IntentRoot(a.pipeline.Repository)})
	if err != nil {
		return fmt.Errorf("assemble claimed work item context: %w", err)
	}
	a.context = bundle.Text
	a.state.ContextTruncation = recordedContextTruncation(bundle)
	return nil
}

// recordedContextTruncation carries what an item's notes lost to the context
// budget onto the run record, and nothing where they lost nothing. The context
// says so in its own marker, which is what the agent reading it sees; this is
// what a reader of the record sees without opening the context, and it is worth
// seeing because an item's notes only ever grow — the run where an item first
// stops fitting is a run nobody decided anything about.
func recordedContextTruncation(bundle contextbundle.Bundle) *runstate.ContextTruncation {
	if bundle.NotesTruncation == nil {
		return nil
	}
	return &runstate.ContextTruncation{
		DroppedNotes: bundle.NotesTruncation.DroppedNotes,
		DroppedBytes: bundle.NotesTruncation.DroppedBytes,
		KeptBytes:    bundle.NotesTruncation.KeptBytes,
	}
}

// recordedStaleBlockClear carries what the claim found when it read a cleared
// stale blocked status back onto the run record, and nothing where the claim
// met no stale status. The claim is where the harness has the tracker's answer
// in hand; the record is what says afterwards which of the clear's three
// endings this run had.
func recordedStaleBlockClear(cleared *beads.StaleBlockClear) *runstate.StaleBlockClear {
	if cleared == nil {
		return nil
	}
	return &runstate.StaleBlockClear{
		Outcome:       cleared.Outcome,
		Reads:         cleared.Reads,
		Status:        cleared.Status,
		ClaimsRefused: cleared.ClaimsRefused,
	}
}

// ErrNoRunToContinue is what a continuation refused for not finding the run it
// was dispatched to continue unwraps to, so a caller can tell it from the
// refusals about the change that run preserved — those mean the run was found
// and its worktree was wrong, and this one means the run was never entered.
var ErrNoRunToContinue = errors.New("the run a continuation was dispatched to is not the run in flight for its item")

// ContinuationMismatchError refuses a repair-intent dispatch that did not find
// the run it was told to continue. Nothing was reserved, claimed, or created:
// the stoppage is exactly as it was, and so is its branch.
type ContinuationMismatchError struct {
	WorkItemID string
	// RunID is the run the dispatch was told to continue.
	RunID string
	// InFlight is the run actually in flight for that item, empty where none is.
	// It is carried separately from the sentence below so a caller can act on it
	// without reading prose.
	InFlight string
	// Found says what was there instead, in the words an operator needs to know
	// what to do about it.
	Found string
}

func (e ContinuationMismatchError) Error() string {
	return fmt.Sprintf(
		"a repair of %s was dispatched to continue run %s, and %s; nothing was reserved and no worktree was created. A repair continues a change that already exists, so it re-enters the run that holds it or it does nothing — starting a fresh run in its place hands a developer the findings about that change and an empty worktree off the target branch, which is delivered as an empty repair or as the same change reinvented. `yoyo triage rerun` is what starts %s over deliberately",
		e.WorkItemID, e.RunID, e.Found, e.WorkItemID)
}

func (e ContinuationMismatchError) Unwrap() error { return ErrNoRunToContinue }

// Continue re-enters one run the harness already made, and can do nothing else.
//
// This is the dispatch half of the loss `refuseSubstitutedHandback` refuses the
// landing half of, and it exists because the two are the same failure seen from
// each end. A repair is decided about one stopped run and is worth exactly the
// change that run preserved; every recorded instance of it going wrong is a
// dispatch that started something fresh instead, and a fresh worktree off the
// target branch is perfectly valid, so nothing downstream noticed. Routing the
// carry-out through Run left that possible by construction: Run resumes what it
// finds in flight and otherwise starts over, which is the right answer to "run
// this item" and the wrong answer to "continue this run".
//
// So repair intent says which run it means and gets an entry point that cannot
// start one. Nothing here reserves a run, claims an item, or creates a worktree:
// the run named is adopted and resumed, or the dispatch is refused with the
// mismatch named. The refusal costs the stoppage nothing — its record, its
// branch, and its worktree are untouched — so whatever the mismatch turns out to
// have been, the same decision is carried out by asking again once it is settled.
//
// The approval policy is not asked, unlike the repair resume Run will make on
// its own. Run's is a judgement about an item somebody named, where picking a
// repair loop up silently is the harness deciding to spend for them; here the
// caller named the run and verified the grant that pays for it, so what the
// policy governs is integrating the result rather than whether the re-entry may
// happen.
func (p Pipeline) Continue(ctx context.Context, workItemID, runID string) (Outcome, error) {
	if err := p.validateDispatch(); err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(runID) == "" {
		return Outcome{}, errors.New("a continuation names the run it continues; a dispatch that named none would be a fresh run of the item under another name")
	}
	// The hold is read exactly where a fresh run reads it and for the same
	// reason: continuing a run spends on a provider, and a held harness spends
	// nothing. The run is left as it stands, which is what it would be left as
	// anyway.
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return Outcome{}, err
		}
		return p.holdWorkItem(workItemID, hold)
	}
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return Outcome{}, err
	}
	// The provider is not asked here: a continuation that names a run nobody is
	// holding, or the wrong one, is refused from durable state alone, and the
	// resume below asks about the provider once there is a run to spend it on. The
	// read is waited out exactly as a fresh dispatch's is: a continuation turned
	// away by a contended store is a repair decision that has to be made again.
	item, err := p.readWorkItem(ctx, workItemID)
	if err != nil {
		return Outcome{}, fmt.Errorf("load work item: %w", err)
	}
	// What the operator has directed and what the item waits on are read before
	// the run is touched, exactly as they are for a fresh run: a continuation is
	// still a developer invoked against intent that may be being rewritten, and
	// the run is left in flight rather than resumed.
	pausing, err := p.pausingDirectives(workItemID)
	if err != nil {
		return Outcome{}, err
	}
	if len(pausing) > 0 {
		return pauseWorkItem(workItemID, pausing[0]), nil
	}
	if blockers := blockingDependencies(item); len(blockers) > 0 {
		return pauseWorkItemForDependencies(workItemID, blockers), nil
	}
	inFlight, lease, err := p.Store.Adopt(ctx, workItemID)
	switch {
	case err == nil:
		defer lease.Release()
	case errors.Is(err, runstate.ErrNoRunInFlight):
		// The run the dispatch names is not going, and this path will not start
		// one in its place. A stopped run is re-entered by the triage carry-out,
		// which makes it live again under the grant it verified; reaching here
		// means that never happened or something has settled it since.
		return Outcome{}, ContinuationMismatchError{
			WorkItemID: workItemID,
			RunID:      runID,
			Found:      "no run is in flight for that item at all, so there is nothing to continue",
		}
	default:
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return Outcome{}, ExistingRunError{State: existing.State}
		}
		return Outcome{}, fmt.Errorf("adopt run in flight: %w", err)
	}
	if inFlight.RunID != runID {
		// Another run of the same item is going. Continuing it would spend this
		// repair's grant on a change nobody decided about, so it is named rather
		// than picked up.
		return Outcome{}, ContinuationMismatchError{
			WorkItemID: workItemID,
			RunID:      runID,
			InFlight:   inFlight.RunID,
			Found: fmt.Sprintf("the run in flight for that item is %s, which is not the run the repair was decided about",
				inFlight.RunID),
		}
	}
	// Three shapes of run are re-entered here: one inside its repair loop, one
	// at the promotion its approval already authorized, and one asleep on a
	// recorded usage-limit deadline whose process exited on the in-process
	// bound. The second is the integration resume; the third is what the
	// reconcile sweep continues once the deadline has passed. All three are the
	// same entry point because they are the same act — the run named is adopted
	// and carried on, and a fresh run can satisfy none of them.
	//
	// A fourth is a run the check stage bound stopped, put back at its checks by
	// the harness, automatically or by a decided repair: it is re-entered at
	// that step on the change it already has, which is what the continuation
	// recorded on it says it is owed.
	if !resumableRepair(inFlight) && !resumableIntegration(inFlight) && !pausedForUsageLimit(inFlight) && !continuedAtCheckStage(inFlight) {
		return Outcome{}, ContinuationMismatchError{
			WorkItemID: workItemID,
			RunID:      runID,
			InFlight:   inFlight.RunID,
			Found: fmt.Sprintf("that run is in flight in status %s at the %s phase, which is not a repair loop, an approved promotion, a check stage the harness continued, or a recorded usage-limit wait this can re-enter",
				inFlight.Status, inFlight.Phase),
		}
	}
	inFlight, err = p.reclaimSlot(ctx, inFlight)
	if err != nil {
		return Outcome{}, err
	}
	return p.resumeRun(ctx, inFlight, item, publishing, skipped)
}

// recordDependencyContinued tells the item a run paused on work it waited on is
// going again. It is written where the pause is actually lifted rather than by
// whoever asked for the continuation, so a continuation refused before it got
// that far writes nothing and one retried writes one note per run that really
// went on. A note that cannot be written costs the run nothing: the lifted pause
// is already durable. state is the run as it stood paused, naming what it waited
// on.
func (p Pipeline) recordDependencyContinued(ctx context.Context, state runstate.State) {
	recordCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = p.Tracker.RecordOutcome(recordCtx, state.WorkItemID, renderDependencyContinuedNotes(state, p.Selection, p.clock().Now()))
}

// reclaimSlot takes a developer slot back for an adopted run whose dependency
// pause gave its slot up, before anything about the run is resumed, and does
// nothing for any other run. Nothing reaches here while the item still waits on
// unfinished work — that was read before the run was adopted — so the pause is
// over and what is left of it is the slot. A full harness refuses exactly as a
// fresh reservation does, wrapped the same way, so every caller that waits on a
// full harness rather than failing — the scheduler's pull, a triage carry-out —
// waits here too, and the run is left paused, holding no slot, exactly as it
// was.
func (p Pipeline) reclaimSlot(ctx context.Context, state runstate.State) (runstate.State, error) {
	if state.DependencyPause == nil {
		return state, nil
	}
	state.UpdatedAt = p.clock().Now()
	reclaimed, err := p.Store.ReclaimSlot(ctx, state, p.Config.Execution.MaxConcurrentDevelopers)
	if err != nil {
		return state, fmt.Errorf("reserve developer run: %w", err)
	}
	// This is where the pause is lifted, so this is where the item is told the
	// run is going again — for the reason resumeRun gives for a caller that
	// lifts it there: a continuation turned away for want of a slot writes
	// nothing, and one that went on writes one note.
	p.recordDependencyContinued(ctx, state)
	return reclaimed, nil
}

// resumeRun picks up a run this process did not finish: one an interrupted
// process left inside its repair loop, one that paused because a provider usage
// limit was exhausted, one whose provider the harness stopped on time, or one
// that stopped short for a directive somebody has since settled. The
// worktree, branch, developer session, attempt
// count, and the failure the interrupted attempt was handed all come from
// durable state, so the resumed run continues the same change at the attempt it
// had reached instead of starting a second one against a fresh budget. Its
// caller holds the run's lease for the whole of it.
func (p Pipeline) resumeRun(ctx context.Context, state runstate.State, item beads.WorkItem, publishing bool, skipped string) (Outcome, error) {
	if state.Document != nil {
		return Outcome{}, errors.New("a document run is resumed from its owning conversation")
	}
	retired, handled, err := (RunRetirer{Runs: p.Store, Tracker: p.Tracker, Now: p.clock().Now(), ReadItem: p.readWorkItem}).Retire(ctx, state)
	if handled {
		if retired.Retirement == nil {
			return Outcome{}, err
		}
		return Outcome{Retirement: retired.Retirement, RunID: retired.RunID, WorkItemID: retired.WorkItemID, Status: retired.Status, Phase: retired.Phase, Branch: retired.Branch, WorktreePath: retired.WorktreePath, Summary: retirementReason(retired)}, err
	}
	if err := validateClaimedItem(item, state.WorkItemID); err != nil {
		return Outcome{}, fmt.Errorf("validate resumed work item: %w", err)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item, Specifications: p.Config.Product.Specifications, IntentRoot: p.Config.Product.IntentRoot(p.Repository)})
	if err != nil {
		return Outcome{}, fmt.Errorf("assemble resumed work item context: %w", err)
	}
	// The item's notes have grown since the interrupted process read them — this
	// run's own stoppage is among them — so what they lost to the budget is
	// recorded from the context this process actually assembled rather than left
	// as what the earlier one recorded.
	state.ContextTruncation = recordedContextTruncation(bundle)
	// Refusing here costs the run nothing: it stays exactly as the interrupted
	// process left it, still resumable, rather than spending an attempt on work
	// that could not be integrated afterwards anyway.
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		refused := fmt.Errorf("repository is not ready to resume run %s: %w", state.RunID,
			refusedByEnvironment("the repository is not ready to resume a run", err))
		// The round this would have been is turned away by the environment, so the
		// run says so rather than leaving the reason in an error a caller prints
		// once. Nothing is charged here — the run is untouched and resumable — and
		// the settle records exactly that.
		if named, environmental := environmentalCauseOf(err); environmental {
			if recordErr := p.refuseDispatchEnvironmentally(state, named, err.Error()); recordErr != nil {
				refused = errors.Join(refused, recordErr)
			}
		}
		return Outcome{}, refused
	}
	// And the provider after it, for the reason a fresh run asks in that order: the
	// checkout is the same refusal on every machine, the provider is a refusal only
	// on the machines that lack it, and a run turned back by both should be told
	// about the one it can act on. Nothing is charged here either — the run is
	// still exactly as the process that stopped it left it.
	//
	// A run resumed at its promotion is not asked. Nothing on that path invokes a
	// provider — the developer's attempt is behind it and the reviewer's verdict is
	// standing — so a provider that is logged out would refuse a promotion it has
	// no part in. A replay that puts the change back through the gate meets the
	// provider where the gate does, and is paused there exactly as any round is.
	//
	// The provider asked is the one the run recorded (recordedbackend.go). A run
	// put back at its developer attempt on a backend this harness cannot start is
	// refused here with its record untouched; one put back at its checks or its
	// review invokes no developer there, and meets the refusal only if a repair
	// is asked of it.
	if !resumableIntegration(state) {
		provider, named, err := p.developerBackendFor(state)
		switch {
		case err == nil:
			if err := p.requireBackendReady(ctx, state.WorkItemID, provider, named); err != nil {
				return Outcome{}, err
			}
			// A resumed developer is launched with the same settings as a fresh
			// one, on whatever CLI is installed now. A refusal leaves the run
			// exactly as it was and says on it that the environment turned it
			// back, as the repository's refusal above does.
			if err := p.requireLaunchSettings(ctx, fmt.Sprintf("run %s of %s", state.RunID, state.WorkItemID), provider, named); err != nil {
				var held LaunchSettingsError
				if errors.As(err, &held) {
					if recordErr := p.refuseDispatchEnvironmentally(state, runstate.CauseDeveloperSettingsNotApplied, held.Error()); recordErr != nil {
						err = errors.Join(err, recordErr)
					}
				}
				return Outcome{}, err
			}
		case state.Phase == runstate.PhaseDeveloping:
			return Outcome{}, err
		}
	}
	// An environmental refusal on the record belongs to a round that is over: a
	// dispatch something turned away before it reached this run, or a round an
	// earlier process settled. This one is a fresh round and is classified on its
	// own evidence, so the old record is cleared here — where every resume passes,
	// rather than only on the repair-grant continuation that clears it for the same
	// reason. Left standing it would reach the terminal record, the docket, and the
	// thread of whatever this round turns out to be, telling an operator the item
	// stands where it did while its counters say otherwise, which is the misreading
	// this class exists to prevent, inverted.
	state.Environmental = nil
	// A redeploy stop is spent by being picked up: this process is the
	// continuation it promised, whichever session or command it is. Left
	// standing it would have the next session re-adopt a run that is already
	// being carried. What it was is kept as the re-adoption the run went on
	// from, because a stall later in the run began in the session it resumed.
	if state.RedeployStop != nil {
		readopted := *state.RedeployStop
		state.Readopted = &readopted
	}
	state.RedeployStop = nil
	// The invariants are re-read rather than carried in run state: they are the
	// repository's current constraints, and a resumed attempt must be held to what
	// holds now rather than to what held when the interrupted process started.
	invariants, err := p.loadInvariants()
	if err != nil {
		return Outcome{}, err
	}
	// A run reserved before either was recorded acquires them as it is picked up,
	// which is the best either can be: the account is the one this process is
	// about to spend, and the configuration is the one the rest of the run is
	// carried out under. A record that already names them keeps what it names —
	// re-stamping would quietly replace evidence about the run with a reading of
	// the file as it stands now.
	if state.AccountAlias == "" {
		// The pool is asked here rather than only at the start, because a record
		// written before the alias was carried has no account to be affined to and
		// this process is about to spend one. A pool that cannot choose leaves the
		// record as it found it: a resume is not worth refusing over an attribution
		// that was already missing.
		if account, err := p.chooseAccount(); err == nil {
			state.AccountAlias = account.Alias
		}
	}
	if state.ConfigRevision == "" {
		state.ConfigRevision = p.Config.Revision()
	}
	// The build is deliberately not acquired the same way. The account and the
	// revision are what the rest of the run is carried out under, so this process
	// can honestly supply them; the build says which harness reserved the run, and
	// stamping this one onto a record that never carried it would assert that this
	// binary started work it only picked up. An empty build stays empty, which is
	// the truthful answer: nobody wrote down what dispatched it.
	run := &activeRun{
		pipeline:   p,
		state:      state,
		item:       item,
		context:    bundle.Text,
		claimed:    true,
		publishing: publishing,
		invariants: invariants,
		// The worktree is reconstructed from what was recorded when it was
		// created, never from what the repository looks like now. The manager
		// revalidates ownership of every field before it acts on them.
		worktree: gitworktree.Worktree{
			RunID:         state.RunID,
			WorkItemID:    state.WorkItemID,
			Path:          state.WorktreePath,
			Branch:        state.Branch,
			BaseCommit:    state.BaseCommit,
			TargetBranch:  state.TargetBranch,
			HarnessCommit: state.HarnessCommit,
		},
		outcome: Outcome{
			// Both callers adopted this run and still hold its lease. Every
			// refusal before this boundary leaves the dispatch unserved.
			continuationAccepted:   true,
			RunID:                  state.RunID,
			WorkItemID:             state.WorkItemID,
			Status:                 runstate.StatusRunning,
			Phase:                  state.Phase,
			Branch:                 state.Branch,
			WorktreePath:           state.WorktreePath,
			BaseCommit:             state.BaseCommit,
			ProviderSessionID:      state.ProviderSessionID,
			ProviderModel:          state.ProviderModel,
			ProviderResolvedModel:  state.ProviderResolvedModel,
			ProviderEffort:         state.ProviderEffort,
			ProviderResolvedEffort: state.ProviderResolvedEffort,
			ProviderEffortReported: state.ProviderEffortReported,
			RepairAttempts:         state.RepairAttempts,
			TransientRelaunches:    state.TransientRelaunches,
			Retries:                state.Retries,
			UsageLimitKind:         state.UsageLimitKind,
			PauseCause:             state.PauseCause,
			ProviderOutageChannel:  state.ProviderOutageChannel,
			// A resumed run keeps the pull request the interrupted process
			// published, so the attempt it is owed updates that request rather than
			// opening a second one for the same branch. It reports a skipped
			// publication for the same reason a fresh run does: a repository with no
			// remote must say so on every pass, not only the first.
			PullRequest:    state.PullRequest,
			PublishSkipped: skipped,
			// What the interrupted process could not keep of the agents' reports and
			// proposals is carried into this outcome, so a problem noted by this
			// process accumulates onto the record rather than writing over what the
			// earlier one recorded.
			ReportProblem:    state.ReportProblem,
			AmendmentProblem: state.AmendmentProblem,
		},
	}
	// A run resumed at its promotion carries the verdict that authorized it into
	// the outcome it reports, because the steps past this point read the outcome:
	// the independence check reads the two sessions off it, the notes recorded on
	// the item and the closure read the verdict off it. A run resumed anywhere
	// else earns a fresh verdict before any of those steps, and carries nothing.
	if resumableIntegration(state) {
		run.carryReviewEvidence()
	}
	// Whether this run is observed is read off its own record rather than off the
	// configuration this process loaded. A run started on the legacy path names no
	// instance and is served here exactly as it was before the definition existed,
	// and a run started on the definition keeps being observed however the
	// configuration has since changed — a rollback included: what a run is was
	// settled when it was created.
	run.resumeDeliveryTrial()
	// A stop the operator asked for is honored before anything is resumed. The
	// process that was serving this run may have exited before it reached a
	// boundary, so without this a later invocation would pick the run up and carry
	// on with work somebody has already stopped.
	if err := run.stopRequested(); err != nil {
		return run.stop(ctx, err)
	}
	// A recorded directive pause is lifted rather than honored. Nothing reaches
	// this point while a directive still pauses the item — they were read before
	// the run was adopted — so the pause is over, and clearing it is what keeps a
	// running attempt from looking like a waiting one to the next process.
	if state.DirectivePause != nil {
		if err := run.clearDirectivePause(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
	}
	// A recorded dependency pause is lifted rather than honored, on the same
	// evidence and for the same reason: nothing reaches this point while the item
	// still waits on unfinished work, because what it waits on was read from a
	// freshly loaded item before the run was adopted. Both entry points have
	// already lifted it as they took the run's developer slot back, in
	// reclaimSlot, so this is only ever a caller that did not.
	if state.DependencyPause != nil {
		if err := run.clearDependencyPause(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
		p.recordDependencyContinued(ctx, state)
	}
	// A recorded tracker park is lifted on the same evidence: the read that went
	// unanswered is the one the dispatch above has just made for itself, and
	// nothing reaches this point without it having been answered. Its window goes
	// with it, so the gate this run re-enters may ask again rather than finding
	// its whole window already spent.
	if state.TrackerPause != nil {
		if err := run.clearTrackerPause(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
	}
	// A recorded operator hold is lifted rather than served, for the same reason
	// and on the same evidence: nothing reaches this point while the operator is
	// still holding activity, because the hold was read before the run was
	// adopted. What the run was held for is added to its account as it is cleared,
	// so the time nobody spent is attributed to whoever decided it.
	if state.OperatorHeldSince != nil {
		if err := run.clearOperatorHold(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
	}
	// A recorded deadline is honored before anything else happens, so a restart
	// during a pause waits out the rest of it rather than asking the provider
	// again and being refused by the same limit.
	if state.UsageLimitResetsAt != nil {
		if err := run.awaitRecordedUsageLimit(ctx); err != nil {
			return run.stop(ctx, err)
		}
	}
	// The scratch directory is cut again before anything can invoke a developer.
	// It belongs to the run rather than to the process serving it, so a run picked
	// up by a second process is handed the same directory the first one was.
	if err := run.prepareScratch(); err != nil {
		return run.fail(err, runstate.StatusFailed)
	}
	// A re-entry carries the preserved change, not a clean worktree. It is asked
	// here rather than inside the branch below because both routes out of this
	// point continue a change: the branch below hands a developer a failure about
	// a change this run already made, and the step past it puts that change
	// through the checks and the reviewer. Neither has anything to work on if the
	// worktree lost it.
	// A conflict whose move onto the target was never recorded — the run stopped
	// with its budget spent and triage has granted it a repair, or a process died
	// part-way through the move — is moved before anything else reads the
	// worktree, because the conflict is only answerable on top of the target, the
	// prompt below says the worktree is already there, and an interrupted move
	// leaves a HEAD the ownership check would otherwise refuse. The move puts the
	// worktree back on the recorded change before it moves it, so a worktree
	// holding none of the change has nothing to move, and is left to the handback
	// check below, which stops it as a missing change.
	if state.Phase == runstate.PhaseDeveloping && run.state.ReplayConflict != nil && !run.state.ReplayConflict.Moved {
		if err := run.moveOntoTargetForRepair(ctx); err != nil && !errors.Is(err, gitworktree.ErrNoChanges) {
			return run.fail(err, failureStatus(ctx, err))
		}
		state = run.state
	}
	if err := run.verifyHandback(ctx); err != nil {
		return run.stop(ctx, err)
	}
	// A run resumed at its promotion is promoted, and nothing before that is done
	// again: the checks passed and the reviewer approved, and the environment is
	// what stopped the change short of the target branch. What it is charged is
	// nothing — no attempt, no review round, no repair grant — because there is no
	// verdict here to charge for. The one thing that leaves this path is a replay
	// whose target moved, and that re-earns the whole gate exactly as a first
	// promotion that lost its race does, through the loop below.
	if resumableIntegration(state) {
		outcome, replayed, err := run.promoteApproved(ctx)
		if !replayed {
			return outcome, err
		}
		return run.verifyReviewAndFinish(ctx)
	}
	// A repair attempt that was in flight when the process stopped was already
	// counted against the budget, so it is re-run rather than re-counted, with
	// the same session and the same repair input it was given.
	if state.Phase == runstate.PhaseDeveloping {
		prompt, err := resumedDeveloperPrompt(state, p.developer().Persona.Text, run.deliveredInvariants().Text(), bundle.Text, run.scratch, p.Config.Checks,
			protectedpath.Protect(p.Config, p.Worktrees.CurrentExports()...), run.repairBudget())
		if err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
		// With no session to resume, the developer starts fresh and knows only what
		// the prompt says, so it is handed the work item and the run's record too.
		if state.ProviderSessionID == "" && handedBackRepair(state) {
			prompt = freshSessionRepairPrompt(prompt, p.developer().Persona.Text, bundle.Text, state)
		}
		// The attempt this run was owed is made again, which is the same state
		// again: the transition a pause or a death takes in the definition, taken
		// here by whichever process picks the run up rather than by the one that
		// put it down.
		run.observe(ctx, deliveryDevelop, "reissued")
		if err := run.develop(ctx, prompt, state.ProviderSessionID); err != nil {
			return run.stop(ctx, err)
		}
	}
	return run.verifyReviewAndFinish(ctx)
}

// resumedDeveloperPrompt rebuilds the prompt the interrupted attempt was given
// from what survived on disk. Only one kind of repair input is ever recorded at
// a time, and where more than one is somehow present the most recent trigger
// wins. That is the earliest gate a run meets rather than the latest, because a
// gate that refuses is a gate the ones behind it never ran: refused paths and a
// change nobody ran anything against are both decided in front of the checks, so
// a check failure beside either was recorded against a change this run has
// already moved past, and the same holds for findings beside a failing check. A
// run that recorded none of the five never had a failure returned to it — it
// paused before or during its first attempt — so what it is owed is that
// attempt.
//
// A refused replay is the exception that proves the ordering rather than one
// against it. It is answered first because it is the only input decided after
// every gate has passed, and every gate that decides another one clears it, so a
// run still carrying one carries the most recent trigger there is.
func resumedDeveloperPrompt(state runstate.State, persona, invariants, bundle, scratchDirectory string, checks []string, protected protectedpath.Set, limit int) (string, error) {
	switch {
	case state.ReplayConflict != nil:
		return replayConflictRepairPrompt(invariants, scratchDirectory, checks, *state.ReplayConflict, state.RepairAttempts, limit), nil
	case state.PathRefusal != nil:
		return pathRefusalRepairPrompt(invariants, scratchDirectory, checks, *state.PathRefusal, protected, state.RepairAttempts, limit), nil
	case owesVerification(state):
		return verificationRepairPrompt(invariants, scratchDirectory, *state.Verification, checks, state.RepairAttempts, limit), nil
	case state.CheckFailure != nil:
		return checkRepairPrompt(invariants, scratchDirectory, checks, *state.CheckFailure, state.RepairAttempts, limit), nil
	case len(state.ReviewFindingDetails) > 0:
		return repairPrompt(invariants, state.ReviewSummary, scratchDirectory, checks, state.ReviewFindingDetails, state.RepairAttempts, limit)
	default:
		return developerPrompt(persona, invariants, bundle, scratchDirectory, checks), nil
	}
}

// handedBackRepair reports a run carrying a failure that was actually returned
// to its developer: refused paths, a change its developer ran nothing against, a
// failing check, the reviewer's findings, or a replay the moved target refused.
// Each of the five is a failure about a change that exists, so the presence of
// any of them is what says a worktree is supposed to hold one, and a run that
// recorded none of them never had a failure returned at all.
//
// A refused replay is recorded on a run whose budget is spent too, before the
// run stops, and that is deliberate: it is what lets a repair triage grants
// afterwards hand the same developer the same conflict in the same session
// rather than leaving a re-run as the only way on.
func handedBackRepair(state runstate.State) bool {
	return state.HandedBack()
}

// owesVerification reports a run holding the execution-evidence gate's refusal:
// a record of the developer's own executions that does not meet the bar. A
// record that meets it is kept too — it is the evidence the reviewer is shown —
// so the repair input is the outstanding debt rather than the record's presence.
func owesVerification(state runstate.State) bool {
	return state.Verification != nil && len(state.Verification.Owed) > 0
}

// resumesAnExistingChange reports a resumed run whose worktree is supposed to
// hold a change already. Two different facts put a run in that position, and
// both of them have to be here or the gate below covers one route and reads as
// though it covers every one.
//
// A run resumed inside its repair loop carries a failure returned about a change
// it made, and the prompt it is about to be handed describes that change. A run
// resumed at the checks or at the review has completed a developer attempt
// whatever else it recorded — what those two steps judge is the change that
// attempt made, and there is nothing else there for them to judge. That second
// one is not hypothetical: a repair round that reached a review and burned it on
// an empty diff is one of the field instances this item was filed for.
//
// A run resumed at its promotion is the third: what it promotes is the approved
// change, and a worktree that lost it would promote nothing or something else.
//
// The one resume this is false for is the run owed its first attempt — paused
// before or during it, with no failure ever returned — and an empty worktree is
// exactly what that attempt starts from.
func resumesAnExistingChange(state runstate.State) bool {
	switch state.Phase {
	case runstate.PhaseChecking, runstate.PhaseReviewing, runstate.PhaseIntegrating:
		return true
	case runstate.PhaseDeveloping:
		return handedBackRepair(state)
	default:
		return false
	}
}

// continuableStall reports a settled run whose provider the harness stopped
// before anything was ever returned to its developer, with its session, branch,
// and worktree recorded. The docket and the continuation ask the repository
// whether the branch and worktree are still there.
//
// It is the one stoppage that is owed a continuation and carries no repair
// input. The harness stops a provider that has gone silent or run out of its
// total budget and leaves the run in flight to be continued; nothing continues
// one on its own, so half an hour later the reconciling sweep settles it as an
// environmental stop and dockets it. What that leaves is a run with a live
// developer session, a worktree holding whatever the stopped attempt had
// written, and no findings, failing check, or refused paths — so the repair
// carry-out refused it for want of a repair input, and the only decision left
// was a re-run, which discards both the session and the uncommitted work.
//
// A stall judges nothing, which is why the continuation it authorizes is not a
// repair round: what the run is owed is the step the harness stopped it in,
// resumed at the point it stalled.
//
// That step is not always the developer's. A run whose provider stalled in its
// review, or whose process went at its checks, has completed a developer
// attempt, and what it is owed is that step asked again on the change the
// attempt left — the review re-read, or the checks re-run — with no developer
// attempt and nothing handed back. Before this only a stall in the developing
// phase was admitted, so a first attempt whose reviewer stalled left a re-run as
// the only decision, and a re-run discards the branch the finished attempt
// produced.
//
// Which of the two a stall is decides what the resumed run is held to, and it
// is read from the same phase resumesAnExistingChange reads, because the two
// have to agree: a continuation this admitted and that gate then refused would
// spend the item's grant on a run the pipeline stops at its first step. A stall
// mid-attempt is owed that attempt, and its worktree need not hold anything yet;
// a stall past the attempt is owed that step, and its worktree has to hold the
// change exactly as a repair's does.
//
// Either way it is admitted only where nothing was ever handed back. A run
// carrying a failure — findings, a failing check, refused paths — is in its
// repair loop: something did judge the work, so it is a repair's to carry out,
// and everything this admission makes the entry, the reason, and the
// continuation say ("nothing was judged", no attempt counted) would be false of
// it.
func continuableStall(state runstate.State) bool {
	if state.Environmental == nil || state.Environmental.Cause != runstate.CauseProcessVanished {
		return false
	}
	// An approved change the environment stopped is never this, however it
	// stopped: what it needs is its integration resumed, which is the refusal
	// continuableRepair already gives ahead of everything else.
	if state.IntegrationStop != nil {
		return false
	}
	if state.ProviderSessionID == "" {
		return false
	}
	if state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" || state.TargetBranch == "" {
		return false
	}
	if handedBackRepair(state) {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseChecking, runstate.PhaseReviewing:
		return true
	default:
		return false
	}
}

// stallResumesPastTheAttempt reports a continuable stall whose run had already
// completed its developer attempt: one stopped at its checks or its review. What
// it is continued at is that step, on the change it has, rather than at a
// developer attempt it does not need.
func stallResumesPastTheAttempt(state runstate.State) bool {
	return continuableStall(state) && state.Phase != runstate.PhaseDeveloping
}

// owedARepair reports a stopped run a repair would continue rather than replace:
// it ended on a blocker nobody has settled, a failure was returned to its
// developer, and the branch it left still carries the change. All three are read
// from the run's own record, which is the only account of it that survives the
// process that made it.
func owedARepair(state runstate.State) bool {
	if !state.Status.Terminal() || strings.TrimSpace(state.Blocker) == "" {
		return false
	}
	if !handedBackRepair(state) {
		return false
	}
	return state.Branch != "" && !state.BranchRemoved
}

// ErrHandbackSubstituted is what a fresh run refused for standing in place of a
// repair unwraps to, so a caller can tell it from a handback that arrived
// without its change — the opposite failure, and the one that at least got as
// far as re-entering the run.
var ErrHandbackSubstituted = errors.New("a fresh run would start over on work a repair is owed")

// SubstitutedHandbackError refuses a fresh run of an item whose last run stopped
// owing a repair of the change it preserved. Nothing was reserved, claimed, or
// created: the stopped run is exactly as it was, and so is its branch.
type SubstitutedHandbackError struct {
	WorkItemID   string
	RunID        string
	Branch       string
	WorktreePath string
}

func (e SubstitutedHandbackError) Error() string {
	return fmt.Sprintf(
		"run %s of %s stopped on a blocker with a failure returned to its developer and its change preserved on %s, so what that stoppage is owed is a repair of the change it already has rather than a fresh run started from nothing; no run was reserved and no worktree was created. A fresh run here hands a developer the work item and an empty worktree off the target branch, which is delivered as an empty change or as the preserved change re-derived by hand — `yoyo triage repair %s` continues the stopped run in the worktree it preserved at %s, and `yoyo triage rerun %s` is what starts over deliberately, recording that the development manager decided the ground moved",
		e.RunID, e.WorkItemID, e.Branch, e.RunID, e.WorktreePath, e.RunID)
}

func (e SubstitutedHandbackError) Unwrap() error { return ErrHandbackSubstituted }

// refuseSubstitutedHandback refuses to start a fresh run of an item whose last
// run stopped owing a repair of the change it preserved.
//
// This is the other half of the failure this item was filed for, and it is not
// the same failure as a handback arriving on an empty worktree: nothing is
// handed back at all. A fresh run reserves a new run, creates a new worktree off
// the target branch, and hands a developer the work item — so the developer sees
// work to do from nothing, and what it delivers is either an empty change or the
// preserved change re-derived by hand against a base that has moved. It is
// silent by construction, because the fresh worktree is perfectly valid and the
// only record that says otherwise belongs to a run nothing in this path reads.
//
// A re-run is the one fresh run of such an item that is right, and it says so in
// the record before it starts: triage claims it against the stoppage, and a claim
// naming this run is the development manager deciding that the ground moved and
// the work is to be done again. So the claim is what this looks for, and its
// absence is what refuses. An item nobody has run yet, one whose last run
// finished, and one whose preserved branch has since been retired all pass
// without a question being asked of them.
func (p Pipeline) refuseSubstitutedHandback(workItemID string) error {
	latest, err := p.Store.Latest(workItemID)
	if errors.Is(err, runstate.ErrNoRecordedRun) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the last run of %s, to tell a fresh run from work a repair is owed: %w", workItemID, err)
	}
	if !owedARepair(latest) {
		return nil
	}
	claimed, err := p.Store.Reruns().Claimed(workItemID)
	if err != nil {
		return fmt.Errorf("read the re-runs triage has claimed of %s: %w", workItemID, err)
	}
	for _, rerun := range claimed {
		if rerun.PriorRunID == latest.RunID {
			return nil
		}
	}
	return SubstitutedHandbackError{
		WorkItemID:   workItemID,
		RunID:        latest.RunID,
		Branch:       latest.Branch,
		WorktreePath: latest.WorktreePath,
	}
}

// activeRun is one work item's run in progress: the durable state, the reported
// outcome, and the worktree and context every attempt shares. A fresh run and a
// resumed one both build one and then take the same steps, so a repair attempt
// behaves identically whichever process started it.
type activeRun struct {
	pipeline Pipeline
	state    runstate.State
	outcome  Outcome
	item     beads.WorkItem
	worktree gitworktree.Worktree
	context  string
	// scratch is the directory this run's developer is told to write its scratch
	// files to, cut for this run alone and outside the worktree. It is derived
	// from the worktree rather than recorded with the run, because it is a fact
	// about where the worktree is: a resumed run re-derives the same path from
	// the same recorded worktree, so there is nothing here for a stored copy to
	// disagree with.
	scratch string
	// claimed records that the tracker holds this item, which is what makes a
	// failure worth reporting back to it.
	claimed bool
	// publishing records that this run publishes: the configuration asked for it
	// and the repository has a remote to publish to. It is decided once, before
	// the item is claimed, so no step has to re-derive it.
	publishing bool
	// invariants is every architectural invariant the repository records. The set
	// is kept rather than one selection of it because the two roles are selected
	// for differently: the developer's set is what the work item names, and the
	// reviewer's adds what the change turned out to touch.
	invariants invariant.Set
	// artifactSet is the recorded canonical documents, read only if something in
	// this run proposes a change to one and kept so a second proposal in the same
	// run does not read the repository again. Nil means nothing has needed it.
	artifactSet *artifact.Set
	// inProcessWait is how long this process has already slept waiting out usage
	// limits for this run, across every probe and every phase. It is what the
	// in-process bound is measured against, because that bound is on how long a
	// process stays open rather than on any one probe. It is deliberately not
	// durable: a later invocation is a new process and gets the whole bound.
	inProcessWait time.Duration
	// pausedAt is when this process recorded the deadline it is now serving. It
	// dates the pause so an operator's release can be told apart from one aimed
	// at a pause this run has already served. The zero time means the deadline
	// was written by an earlier process, which no release can predate.
	pausedAt time.Time
	// launchGate is the gate the developer attempt about to be made is started
	// behind, on a run pinned to an endpoint pair (developerrouting.go), and nil
	// for every other invocation.
	launchGate *execution.LaunchGate
	// charger is the identity this process charges the item's review rounds
	// under, minted on first use and kept for the rest of the run so the charge
	// and the settle that may return it agree. It is deliberately not durable: a
	// later invocation of the same run is a different process and gets one of its
	// own, which is exactly what stops it returning this one's round.
	charger string
	// trial is the workflow instance this run is observed through, created with
	// the run unless the project had rolled back to the legacy path. Nil is a run
	// nothing is watching, which is every run a rolled-back project starts and
	// every run started before the definition existed.
	trial *deliveryTrial
}

// chargingProcess is what this process charges this run's review rounds under.
// A round is only ever given back to the process that charged it, so the same
// identity has to answer both the charge and the settle, and a run picked up by
// a later process has to answer differently — which is why it is minted here
// rather than derived from the run, whose identifier and attempt number are the
// same in both processes.
func (a *activeRun) chargingProcess() (string, error) {
	if a.charger == "" {
		minted, err := runstate.NewChargingProcess()
		if err != nil {
			return "", err
		}
		a.charger = minted
	}
	return a.charger, nil
}

// loadInvariants reads the architect's durable constraints. It is a hard failure
// rather than an empty set, because delivering nothing is what an unconstrained
// repository looks like and the whole point of an invariant is that a developer
// whose own work looks correct is stopped by it.
func (p Pipeline) loadInvariants() (invariant.Set, error) {
	store := invariant.StoreFor(p.Repository, p.Config.Product)
	set, err := store.Load()
	if err != nil {
		return invariant.Set{}, fmt.Errorf("load architectural invariants: %w",
			refusedByEnvironment("the repository's invariants could not be read", err))
	}
	return set, nil
}

// deliveredInvariants selects the invariants relevant to this run's work item and
// records what was delivered. It is what reaches the developer, and it depends on
// nothing anybody wrote into the bead by hand: the work item's own prose is the
// evidence, and every repository-wide invariant reaches every item regardless.
func (a *activeRun) deliveredInvariants() invariant.Delivery {
	delivery := a.invariants.Select(workItemEvidence(a.item)...)
	a.recordDeliveredInvariants(delivery)
	return delivery
}

// reviewedInvariants selects the invariants for the reviewer. The change itself
// is added to the evidence, so an invariant scoped to code the work item never
// mentioned still reaches the gate that judges the change that touched it —
// which is exactly the case a developer's own reading of its work cannot catch.
func (a *activeRun) reviewedInvariants(changes gitworktree.ChangeDiff) invariant.Delivery {
	evidence := append(workItemEvidence(a.item), changes.Status, changes.DiffStat)
	delivery := a.invariants.Select(evidence...)
	a.recordDeliveredInvariants(delivery)
	return delivery
}

// workItemEvidence is what the harness knows about the code a work item concerns
// before any of it exists: the item's own prose. Scope selection is textual over
// this, so an item that names the package it is about pulls in the invariants
// that constrain it.
func workItemEvidence(item beads.WorkItem) []string {
	return []string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes}
}

// grantEvidence is the part of a work item that can admit a protected path, and
// it is deliberately narrower than the evidence above: the fields somebody
// authored, and not the notes.
//
// The notes are where the harness appends what a run produced, and some of that
// is written by an agent — the reviewer's verdict summary and every finding
// message go into the item's notes through RecordOutcome and Block. Reading
// grants from there would mean an agent's own prose could admit a protected path
// for the next run of the same item, which is exactly the thing this gate exists
// to stop, and it would make "nothing an agent writes grants a path" false in the
// contract that says it. Nothing the harness writes touches the four fields below.
//
// The consequence is worth stating because it is the failure an operator would
// meet: a grant written into the notes does not count. The refusal names the
// fields a grant is read from, so an item that visibly says the words and is
// still refused says why.
func grantEvidence(item beads.WorkItem) []string {
	return []string{item.Title, item.Description, item.Design, item.AcceptanceCriteria}
}

// refuseProviderGrant refuses to start on an item that grants a path no provider
// honours. Such a grant admits work no attempt can finish, so no attempt is
// made: the run stops here, before the item is claimed and before a single
// repair round is spent, which is the entire failure this gate was built for.
//
// It exists as well as the check admission makes, rather than instead of it,
// because the two doors admission holds — a proposal and a tracker action —
// carry an item's title and description and nothing else. A grant is honoured
// from the design guidance and the acceptance criteria too, and nothing in the
// harness writes either of those: they are set with the tracker's own command,
// by an operator or by an agent's shell, so there is no admission door for them
// to be refused at. This is where a grant written into one is caught, and it is
// also what catches an item admitted before that gate existed.
//
// It reads exactly the fields the gate that obeys a grant reads, through the
// same predicate admission asks, so what a run refuses and what a run would have
// obeyed can never come apart.
func refuseProviderGrant(item beads.WorkItem) error {
	problems := protectedpath.GrantProblems(grantEvidence(item)...)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("work item %s grants a path no run can write to: %w", item.ID, errors.Join(problems...))
}

// refuseUngrantedCondition refuses to start on an item whose done-conditions
// name a document under an artifact home that the item does not grant. Such a
// condition is one no diff can satisfy — the path is refused in the change by
// the gate above — so a run that started on it would land what it could and
// park on the rest, which is what three items did in one week before admission
// learned to refuse the clause (yoyodyne-ifd.141.1, .63, .68.25). The run stops
// here, before the item is claimed and before an attempt is spent.
//
// It exists as well as the check admission makes, rather than instead of it,
// for the reasons refuseProviderGrant does: the acceptance criteria are written
// with the tracker's own command and reach no admission door, and the queue
// predates the gate. It asks the same predicate admission asks, of the same
// fields, so what a run refuses and what admission would have refused can never
// come apart. The documents the homes own are read from the repository the run
// is about to cut from; where that read fails the paths are still checked, and
// the check by name is what is lost.
//
// The item's executor is part of the question. An item a conversation carries
// is never chosen here, but it can be named — `yoyo run <id>` is the operator
// deciding — and it is then judged as that conversation's work rather than
// refused for stating what that work is. An item nobody marked whose title or
// done-condition reads as conversation work is refused with the marker named,
// which is what yoyodyne-ifd.330 would have met instead of a run.
func (p Pipeline) refuseUngrantedCondition(item beads.WorkItem) error {
	documents, err := protectedpath.OwnedDocuments(p.Repository, p.Config.Product)
	if err != nil {
		// A repository whose artifact homes cannot be read is about to be refused
		// by the invariants load below; what this gate can still judge, it does.
		documents = nil
	}
	homes := protectedpath.ArtifactHomes(p.Config, documents...)
	problems := homes.ConditionProblems(protectedpath.Subject{
		Title:              item.Title,
		Description:        item.Description,
		AcceptanceCriteria: item.AcceptanceCriteria,
		Granted:            protectedpath.Grants(grantEvidence(item)...),
		Executor:           item.Executor,
	})
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("work item %s states a done-condition no developer run can satisfy, so no run was started on it: %w", item.ID, errors.Join(problems...))
}

// recordDeliveredInvariants keeps the run's account of which constraints its
// change was held to. It merges rather than replaces, because a run delivers
// twice — once to the developer and once to the reviewer — and the second
// selection can legitimately be wider than the first.
func (a *activeRun) recordDeliveredInvariants(delivery invariant.Delivery) {
	for _, id := range delivery.IDs() {
		a.outcome.Invariants = appendUnique(a.outcome.Invariants, id)
	}
	for _, problem := range delivery.Problems {
		a.outcome.InvariantProblems = appendUnique(a.outcome.InvariantProblems, problem.String())
	}
	for _, id := range delivery.Omitted {
		a.outcome.InvariantProblems = appendUnique(a.outcome.InvariantProblems,
			id+": it matched this work item and did not fit the delivered context")
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// verifyReviewAndFinish is the gated half of a run: the deterministic checks,
// the independent review behind them, the bounded repair loop that returns
// either kind of failure to the developer, and then integration and cleanup of
// a change an attempt actually got approved.
func (a *activeRun) verifyReviewAndFinish(ctx context.Context) (Outcome, error) {
	if a.state.Document != nil {
		return a.reviewDocument(ctx)
	}
	// A run whose integration a human still approves has no repair loop to
	// return anything to: nothing is promoted without that person, and the
	// worktree is preserved for them either way, so a failing check ends the
	// run exactly as it always has.
	if !a.pipeline.automatic() {
		if err := a.holdForDirective(); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.holdForDependency(ctx); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.verify(ctx); err != nil {
			// The three ways this gate stops a run under a person's approval are one
			// error out of one call, and the definition that expresses this path
			// sends all three to the same ending. Nothing on this path is ever handed
			// back, so there is no budget here to have spent.
			a.observeCheckEnded(ctx, err, stillRepairable)
			return a.stop(ctx, err)
		}
		a.observe(ctx, deliveryCheck, "passed")
		return a.finish(ctx)
	}
	// The whole gate repeats when a promotion loses its race for the target
	// branch, because everything it established belongs to a change that would
	// now be promoted onto a different base: the checks ran against the old one
	// and the approval was given for the old diff. Nothing about the loop
	// weakens the gate — it re-earns it.
	for {
		if err := a.repairLoop(ctx); err != nil {
			return a.stop(ctx, err)
		}
		outcome, replayed, err := a.promoteApproved(ctx)
		if !replayed {
			return outcome, err
		}
	}
}

// promoteApproved is the promotion half of the gate: the approval the repair
// loop just earned — or the one a resumed run already holds — is checked for
// independence, the item is asked one last time what it waits on, and the change
// is promoted onto the target branch. It reports whether the change was replayed
// onto a target that moved instead, in which case nothing has ended and the
// whole gate is to be re-earned by the caller; every other way out is the run's
// own ending, returned as it is.
//
// It is one function rather than the tail of the loop above because two routes
// reach it, and they must not be able to promote differently: the loop, where
// the approval was just given, and the integration resume, where the approval
// was given by a process that then stopped short of this step for a reason the
// environment answers for.
func (a *activeRun) promoteApproved(ctx context.Context) (Outcome, bool, error) {
	// An approval only authorizes integration when it demonstrably came from a
	// second invocation. Missing or reused provider identity means the
	// independence the policy relies on was never established.
	if err := a.validateIndependentReview(); err != nil {
		outcome, err := a.fail(stoppedBy(runstate.StopReview, err), runstate.StatusFailed)
		return outcome, false, err
	}
	// The promotion is the last moment a directive can still stop this work,
	// and the loop above can have spent hours in the provider since it last
	// asked. Asking again here is what keeps a directive recorded mid-repair
	// from reaching the run only after its change was already on the target
	// branch, which is indistinguishable from it reaching nothing. A dependency
	// link applied in that same stretch is the same fact and is asked the same
	// way: promoting work somebody has just made wait on other work is the one
	// outcome a link applied late must not still produce.
	if err := a.holdForDirective(); err != nil {
		outcome, err := a.stop(ctx, err)
		return outcome, false, err
	}
	if err := a.holdForDependency(ctx); err != nil {
		outcome, err := a.stop(ctx, err)
		return outcome, false, err
	}
	err := a.integrate(ctx)
	if err == nil {
		a.observe(ctx, deliveryIntegrate, "integrated")
		outcome, err := a.finish(ctx)
		return outcome, false, err
	}
	retry, retryErr := a.prepareIntegrationRetry(ctx, err)
	if retryErr != nil {
		outcome, err := a.endPromotion(ctx, retryErr)
		return outcome, false, err
	}
	if !retry {
		outcome, err := a.endPromotion(ctx, err)
		return outcome, false, err
	}
	return Outcome{}, true, nil
}

// endPromotion is the ending a failed promotion gets. Ordinarily that is the
// failure it always was. The one exception is a run whose hosting session
// cancelled it for its own redeploy in the moment between reading its phase and
// the promotion starting: that goes through stop, so it ends with its reason
// naming the redeploy — nothing at a promotion is resumable, so it is cancelled
// with its change preserved rather than held, and never silently.
func (a *activeRun) endPromotion(ctx context.Context, cause error) (Outcome, error) {
	if _, forRedeploy := drainedForRedeploy(ctx); forRedeploy {
		return a.stop(ctx, cause)
	}
	return a.fail(cause, failureStatus(ctx, cause))
}

// contendedIntegration reports a promotion refused because the target branch is
// not where this run left it. Both refusals mean it: the recorded base no
// longer matches the branch, or the fast-forward itself lost the race. Neither
// says anything is wrong with the change, which is why they are the only
// failures worth re-preparing rather than ending the run on.
func contendedIntegration(err error) bool {
	return errors.Is(err, gitworktree.ErrTargetDrift) || errors.Is(err, gitworktree.ErrNotFastForward)
}

// prepareIntegrationRetry re-prepares a change whose promotion lost its race,
// and reports whether the run may try again. The retry is recorded before any
// of it happens, so a process that dies part-way through cannot come back to a
// fresh budget, and the commit the refused promotion had already made is
// recorded for the same reason publishing records its own: a worktree at a HEAD
// nothing named is a worktree nothing may promote afterwards.
//
// A replay that conflicts is not another retry, and it is not the end of the
// line either: the harness still decides nothing about which side is right, but
// the developer that wrote the change is asked to reconcile it before anybody
// else is. Only a run whose repair budget is spent hands the conflict to a
// person.
func (a *activeRun) prepareIntegrationRetry(ctx context.Context, cause error) (bool, error) {
	if !contendedIntegration(cause) {
		return false, nil
	}
	// Losing the race is not what the budget bounds, and it never stops the run.
	// The change standing here passed its checks and was approved, so a replay
	// that passes again is charged nothing and the run replays for as long as the
	// target keeps moving. What the budget bounds is a replay that stops on the
	// change — conflicting, or handed back for a failing check or a repair
	// verdict — and it is enforced where that happens (chargeReplayStop), not
	// here. The replay being prepared is marked unjudged until its gate says
	// which it was.
	a.state.IntegrationRetries++
	a.state.ReplayUnjudged = true
	a.outcome.IntegrationRetries = a.state.IntegrationRetries
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("save integration retry %d: %w", a.state.IntegrationRetries, err)
	}
	rebase, err := a.pipeline.Worktrees.RebaseOntoTarget(ctx, a.worktree, integrationMessage(a.item, a.outcome))
	// The replay reports the commit it owns whichever way it went, so it is
	// recorded before the failure is: an aborted replay leaves the branch on that
	// commit, and the worktree is preserved for whoever picks the conflict up.
	if recordErr := a.recordRebase(rebase); recordErr != nil {
		return false, withFailedRecord(err, recordErr)
	}
	if errors.Is(err, gitworktree.ErrRebaseConflict) {
		return a.continueOnRebaseConflict(ctx, err)
	}
	if err != nil {
		return false, fmt.Errorf("replay the change onto the moved target branch: %w", err)
	}
	// The published branch has to become the replayed one, or the pull request
	// would carry work the authoritative local branch no longer has.
	if err := a.republishRebase(ctx, rebase); err != nil {
		return false, err
	}
	// The approval that was granted described the change on its old base. It is
	// discarded rather than carried over, so the next pass through the gate gets
	// its own independent verdict on the replayed diff.
	//
	// The deterministic checks need no equivalent, and the asymmetry is not an
	// omission: a verdict is a recorded fact that would otherwise still be
	// standing, while the checks are simply run again. verify() executes every
	// configured command on every pass and records what they did, so the replayed
	// change is judged by checks that ran against it rather than by a result from
	// before it was replayed.
	a.clearReviewEvidence()
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("save replayed run state: %w", err)
	}
	// The replay is prepared and the whole gate is about to be re-earned, which is
	// where the definition sends a superseded promotion. It is observed here
	// rather than beside the refusal that caused it, because until the replay has
	// actually been prepared the run may still be ending on it.
	a.observe(ctx, deliveryIntegrate, "superseded")
	return true, nil
}

// recordRebase makes the replayed base and the harness commit that carries the
// work durable together. They move as one — the recorded base is what the
// promotion is checked against and what every diff is taken from, and the
// harness commit is the only HEAD the worktree may be at — so a record with one
// of them updated and not the other describes a worktree nothing would accept.
func (a *activeRun) recordRebase(rebase gitworktree.Rebase) error {
	if rebase.HeadCommit == "" {
		return nil
	}
	a.worktree.BaseCommit = rebase.BaseCommit
	a.state.BaseCommit = rebase.BaseCommit
	a.outcome.BaseCommit = rebase.BaseCommit
	// A replay that leaves nothing above the base has no harness commit to name:
	// the work it replayed is already in the target. Recording one anyway would
	// claim a commit past a base that is also that commit.
	harnessCommit := rebase.HeadCommit
	if harnessCommit == rebase.BaseCommit {
		harnessCommit = ""
	}
	a.recordHarnessCommit(harnessCommit)
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the replayed change: %w", err)
	}
	return nil
}

// block records one durable blocker on the work item and keeps its text on the
// run. Every stoppage a person has to decide about goes through here, which is
// what makes the blocker a triage docket entry carries the same words the item
// carries rather than a second account assembled from the same evidence — and
// what lets a reader of the run record afterwards say what stopped it without
// working out which of the item's notes was this run's.
//
// It is recorded on its own deadline rather than on a context this run may have
// exhausted: a run that stopped on something nobody was told about is the one
// outcome this must never produce. The text is durable on the run only when the
// tracker took it, so a blocker the item never carried is never claimed here.
func (a *activeRun) block(notes string) error {
	blockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.Block(blockCtx, a.state.WorkItemID, notes); err != nil {
		return err
	}
	a.outcome.Blocked = true
	// The terminal write that ends the run is a moment away and carries this
	// with it, so nothing is saved here: a second write would be one more chance
	// for the record and the item to disagree about what stopped the run.
	a.state.Blocker = runstate.RecordBlocker(notes)
	return nil
}

// chargeReplayStop charges the latest replay to the integration budget when
// its change is about to be handed back, and reports whether that charge
// spends more than the budget permits. Only the first stop after a replay is
// charged: the replay is one replay however many repairs it goes on to need,
// and those are bounded by the repair budget. A run nothing replayed, or whose
// replay has already been charged, charges nothing. The charge is saved before
// anything it decides takes effect, as every counter here is.
func (a *activeRun) chargeReplayStop() (bool, error) {
	if !a.state.ReplayUnjudged {
		return false, nil
	}
	a.state.ReplayUnjudged = false
	a.state.ChargedReplays++
	a.outcome.ChargedReplays = a.state.ChargedReplays
	a.outcome.IntegrationRetries = a.state.IntegrationRetries
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("save charged replay %d: %w", a.state.ChargedReplays, err)
	}
	return a.state.ChargedReplays > a.pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation, nil
}

// blockOnChargedReplay ends a run whose replayed change stopped on the change
// once more than the integration budget permits. It is a stop about the change
// rather than about the target: the target moving is what caused the replay,
// and it costs nothing, but a change that keeps failing or being sent back on
// each new base is one a person should look at before another replay.
func (a *activeRun) blockOnChargedReplay(stop string) error {
	limit := a.pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation
	blocked := fmt.Errorf("the change replayed onto its moved target stopped on the change with %d of %d permitted replay stop(s) spent: %s",
		a.state.ChargedReplays, limit, stop)
	if err := a.block(renderChargedReplayBlockerNotes(a.outcome, blocked.Error(), limit)); err != nil {
		return stoppedBy(runstate.StopIntegrationBudget, withFailedRecord(blocked, fmt.Errorf("record the charged replay as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopIntegrationBudget, blocked)
}

// continueOnRebaseConflict hands a change that cannot be replayed back to the
// developer that wrote it, and reports whether the run may go round the gate
// again.
//
// It is the repair loop applied to the one failure that used to end a run
// outright, and what changed is who is asked rather than what the harness
// decides: nothing is forced, no side is chosen, and neither answer is
// discarded. The developer is asked because it is the one party that already
// knows what this change was for, its session and worktree are still open, and
// reconciling its own work with what the target became is the same kind of work
// every other repair asks for — at a continuation's price rather than a fresh
// run's.
//
// It is not asked to answer from where the change sits, because a resolution
// written there is another change to lines the target has already changed and
// replays into the same conflict however carefully it was made. The change is
// moved onto the target first, with the disagreement left in the worktree as
// Git's own markers, and what the developer is asked for is the two answers
// reconciled on the ground the target now holds.
//
// The resolution earns nothing on its way through. The caller re-enters the
// gate, so the reconciled change is checked again, reviewed again by its own
// invocation, and promoted only after both — exactly as a change repaired for a
// finding is.
//
// Two budgets bound it. A conflict is a replay that stopped on the change, so it
// is charged to the integration budget exactly as a replay handed back for a
// failing check is, and a run past that budget stops on it. And the hand-back is
// a repair, so it spends from the repair budget, shared with the other inputs for
// the reason they share it: what it bounds is how many times a run may ask a
// developer. A run that has spent either stops with both sides intact, which is
// what this always did, and carries the conflict on its record so a repair
// triage grants afterwards hands the same developer the same disagreement.
func (a *activeRun) continueOnRebaseConflict(ctx context.Context, cause error) (bool, error) {
	if a.state.Document != nil {
		a.recordReplayConflict(recordedReplayConflict(a.worktree, cause, a.state.Phase, a.pipeline.clock().Now().UTC()))
		return false, cause
	}
	limit := a.repairBudget()
	a.recordReplayConflict(recordedReplayConflict(a.worktree, cause, a.state.Phase, a.pipeline.clock().Now().UTC()))
	overCharged, err := a.chargeReplayStop()
	if err != nil {
		return false, withFailedRecord(cause, err)
	}
	if overCharged || a.state.RepairAttempts >= limit {
		a.observe(ctx, deliveryIntegrate, "conflicted")
		return false, a.blockOnRebaseConflict(cause, limit)
	}
	// Observed before the hand-back, because the hand-back is the next state.
	a.observe(ctx, deliveryIntegrate, "reconciling")
	if err := a.moveOntoTargetForRepair(ctx); err != nil {
		return false, errors.Join(cause, err)
	}
	if err := a.repair(ctx, replayConflictRepairPrompt(a.deliveredInvariants().Text(), a.scratch, a.pipeline.Config.Checks, *a.state.ReplayConflict, a.state.RepairAttempts+1, limit)); err != nil {
		return false, err
	}
	return true, nil
}

// moveOntoTargetForRepair puts the run's change onto the target with the
// conflict left in the worktree, and makes the move durable before anything
// acts on it. The recorded base and the harness commit move together for the
// reason they always do, and a move that leaves the change as uncommitted work
// above the new base has no harness commit, which is exactly what this leaves.
//
// The approval is discarded here rather than left for the next review to
// overwrite: it described the change as it stood before the move, and the move
// and the attempt after it both change what it described. A standing approval
// for a diff nobody has seen is what the gate exists to rule out.
//
// It is also what a continued run calls when it finds a recorded conflict whose
// move never happened — a process that died between the two, or a run whose own
// budget was spent and that triage granted a repair — so it is safe to reach
// with the conflict already recorded and only the move owed.
//
// The published branch is not touched here. Pushing the target alone as the run
// branch would put a pull request's head inside its own base, which a forge can
// read as the request having been merged; the next attempt's own commit replaces
// the published branch instead, from exactly the commit the harness put there.
func (a *activeRun) moveOntoTargetForRepair(ctx context.Context) error {
	a.clearReviewEvidence()
	rebase, err := a.pipeline.Worktrees.ReplayForRepair(ctx, a.worktree, integrationMessage(a.item, a.outcome))
	if err != nil {
		if recordErr := a.recordRebase(rebase); recordErr != nil {
			return errors.Join(err, recordErr)
		}
		return fmt.Errorf("move the change onto the target for its author to reconcile: %w", err)
	}
	if rebase.HeadCommit != "" {
		a.worktree.BaseCommit = rebase.BaseCommit
		a.state.BaseCommit = rebase.BaseCommit
		a.outcome.BaseCommit = rebase.BaseCommit
		// The change is uncommitted work above the new base, so no harness commit
		// is standing: the worktree's HEAD is the base itself.
		harnessCommit := rebase.HeadCommit
		if harnessCommit == rebase.BaseCommit {
			harnessCommit = ""
		}
		a.recordHarnessCommit(harnessCommit)
	}
	a.state.ReplayConflict.Moved = true
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the change moved onto the target: %w", err)
	}
	return nil
}

// reconcilingPublishedBranch reports a run whose published branch the next
// attempt has to replace rather than extend: the change was moved onto the
// target to be reconciled, so the branch the pull request carries is no longer
// an ancestor of what the attempt committed.
func (a *activeRun) reconcilingPublishedBranch() bool {
	return a.state.ReplayConflict != nil && a.state.ReplayConflict.Moved && a.outcome.PullRequest != nil && a.state.HarnessCommit != ""
}

// recordReplayConflict makes the refused replay the run's outstanding repair
// input. It clears the others for the reason each of them clears the rest: at
// most one input describes the change now in the worktree.
func (a *activeRun) recordReplayConflict(conflict runstate.ReplayConflict) {
	a.state.CheckFailure = nil
	a.state.PathRefusal = nil
	a.state.ChecksPassed = nil
	recorded := conflict
	a.state.ReplayConflict = &recorded
}

// recordedReplayConflict is what a refused replay carries into durable state and
// into the developer's next attempt. A replay that stopped on more paths than the
// bound allows names the first of them and counts the rest, because a listing
// that stopped without saying so would read as the whole disagreement. A refusal
// reported without its detail still produces a usable record: the branch is
// known from the worktree, and what is missing is left out rather than guessed.
func recordedReplayConflict(worktree gitworktree.Worktree, cause error, phase runstate.Phase, at time.Time) runstate.ReplayConflict {
	recorded := runstate.ReplayConflict{
		TargetBranch: worktree.TargetBranch,
		Detail:       boundedTail(cause.Error(), runstate.MaxConflictDetailBytes),
		Phase:        phase,
		RecordedAt:   at,
	}
	var conflict *gitworktree.RebaseConflict
	if !errors.As(cause, &conflict) {
		return recorded
	}
	recorded.TargetCommit = conflict.TargetCommit
	recorded.Paths = conflict.Paths
	if len(conflict.Paths) > runstate.MaxConflictedPaths {
		recorded.Paths = conflict.Paths[:runstate.MaxConflictedPaths]
		recorded.Omitted = len(conflict.Paths) - runstate.MaxConflictedPaths
	}
	return recorded
}

// blockOnRebaseConflict ends a run whose change cannot be replayed onto what its
// target became and whose repair budget leaves its developer no attempt to
// reconcile it. Nothing is forced and nothing is resolved: the worktree and the
// branch stay exactly as they were, and the conflict is recorded for whoever
// owns the decision.
//
// The conflict is already on the run when this is reached, written by
// recordReplayConflict before the blocker is attempted on the tracker, because
// the tracker can fail to take it: on yoyodyne-ifd.441 the write timed out, and
// with nothing else on the run saying what stopped it, the error that ended it
// was classified from its tail as a transport failure. The record is the account
// of the conflict that survives the write failing, and it is the fact the docket
// names the next mover from. It is not saved on its own, for the reason the
// blocker text is not: the terminal write that ends the run is a moment away and
// carries it.
func (a *activeRun) blockOnRebaseConflict(cause error, limit int) error {
	a.outcome.ReplayConflict = a.state.ReplayConflict
	if err := a.block(renderRebaseConflictNotes(a.outcome, cause.Error(), a.state.ReplayConflict, limit)); err != nil {
		return stoppedBy(runstate.StopRepairBudget, withFailedRecord(cause, fmt.Errorf("record the replay conflict as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopRepairBudget, cause)
}

// ErrDivergedTarget is what a run stopped because its target branch could not
// be brought onto the remote's before promoting unwraps to. It is declared here
// rather than by the worktree manager because the manager holds such a catch-up
// rather than failing it: the stop is this package's decision, made from what
// the catch-up found.
var ErrDivergedTarget = errors.New("the target branch would not catch up to the remote's")

// blockOnDivergedTarget ends a run whose target branch and the remote's have
// gone different ways, before anything is promoted. It is the remote-side twin
// of the replay conflict: the local branch cannot be brought onto what the
// remote holds, so there is nowhere to replay onto, and promoting anyway would
// close this item as integrated against a divergence no later sweep can
// reconcile. Nothing is forced and nothing is reset — both branches are left
// exactly where they are and named for whoever settles them.
//
// The held catch-up goes on the outcome before the blocker is written, because
// it is what stopped the run whether or not the tracker takes the note: the
// brake reads it to count the stop as the environment's, and a refusal recorded
// only when the item could be written would count toward the brake exactly
// when nothing else had told anybody about it.
//
// It is found before anything is promoted, so an approved change stopped here
// is an integration stop the harness resumes once the branches are settled: the
// error carries ErrDivergedTarget for recordIntegrationStop to read, and the
// blocker names the resume beside the recovery.
func (a *activeRun) blockOnDivergedTarget(catchup gitworktree.Catchup) error {
	remote := a.pipeline.Config.Execution.Remote
	diverged := fmt.Errorf("%w: %s cannot be brought onto %s before promoting: %s",
		ErrDivergedTarget, catchup.TargetBranch, remote, catchup.Held)
	a.outcome.DivergedTarget = &catchup
	a.noticeDivergedTarget(catchup)
	if err := a.block(renderDivergedTargetNotes(a.outcome, catchup, remote, diverged.Error(), a.state.ApprovedAwaitingIntegration())); err != nil {
		return stoppedBy(runstate.StopIntegration, withFailedRecord(diverged, fmt.Errorf("record the diverged target branch as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopIntegration, diverged)
}

// blockOnPromotedDivergence ends a run whose remote target diverged in the
// window after the local promotion, which is the same divergence as
// blockOnDivergedTarget's with one thing already done that cannot be undone: the
// change is on the local target branch. So the blocker says so rather than
// pretending otherwise — what a person settles here is the two branches and the
// publication, not the work — and the run ends without closing the item, because
// an item closed as integrated is exactly the receipt that made this outcome
// invisible.
func (a *activeRun) blockOnPromotedDivergence(integration gitworktree.Integration, catchup gitworktree.Catchup, cause error) error {
	remote := a.pipeline.Config.Execution.Remote
	diverged := fmt.Errorf("%w; %s cannot be brought onto %s afterwards: %s",
		cause, integration.TargetBranch, remote, catchup.Held)
	// The same held catch-up, for the same reader: the brake counts this stop
	// toward nothing whichever side of the promotion the divergence was found on.
	a.outcome.DivergedTarget = &catchup
	a.noticeDivergedTarget(catchup)
	if err := a.block(renderPromotedDivergenceNotes(a.outcome, integration, catchup, remote, diverged.Error())); err != nil {
		return stoppedBy(runstate.StopIntegration, withFailedRecord(diverged, fmt.Errorf("record the diverged target branch as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopIntegration, diverged)
}

// repairLoop returns each failure to the same developer until an attempt both
// passes the deterministic gates and is approved, or the configured repair
// budget is spent. A refused path, a failing check, and a reviewer's findings
// are the same kind of event here: each is repair input for the developer that
// produced the change, and all three draw on one shared budget. Sharing it is
// what bounds the total number of developer invocations a run can make, which is
// what the budget exists to do; separate budgets would let a run alternating
// between them spend far more than the operator configured. Every attempt puts
// the change through both gates again and obtains its own independent review, so
// an approval always belongs to a change that passed them, and nothing an
// earlier attempt was granted carries forward.
func (a *activeRun) repairLoop(ctx context.Context) error {
	limit := a.repairBudget()
	for {
		// Every round of the gate asks what the operator has directed, because a
		// round is another developer invocation and a directive recorded while one
		// was running must not buy the run another one. The first round is where a
		// directive that arrived during the attempt that got the run here reaches
		// it, which is the case this exists for.
		if err := a.holdForDirective(); err != nil {
			return err
		}
		// It asks what the item waits on for the same reason, from a freshly read
		// item rather than from the one selection saw. The first round is where a
		// dependency link applied while the developer was working reaches the run,
		// and it reaches it before the reviewer is asked to judge work that should
		// never have been developed — which is the round that was burned when this
		// gate was missing.
		if err := a.holdForDependency(ctx); err != nil {
			return err
		}
		if err := a.verify(ctx); err != nil {
			// A change too large for the reviewer's copy is not repair input: the
			// bound is the harness's, so it ends the run where it was measured and
			// goes to the development manager with the sizes.
			var overBound reviewBoundRefusal
			if errors.As(err, &overBound) {
				a.observeCheckEnded(ctx, err, budgetSpent)
				return a.blockOnReviewBound(overBound)
			}
			// A change refused for what it touched is answered before anything else,
			// because it is what the gate decided first: the checks never ran on this
			// attempt, so there is no check failure competing with it.
			var refused pathRefusal
			if errors.As(err, &refused) {
				a.recordPathRefusal(refused.refusal)
				if a.state.RepairAttempts >= limit {
					a.observeCheckEnded(ctx, err, budgetSpent)
					return a.blockOnRefusedPaths(refused, limit)
				}
				if spent, chargeErr := a.chargeReplayStop(); chargeErr != nil || spent {
					a.observeCheckEnded(ctx, err, budgetSpent)
					return a.replayStopEnds(chargeErr, err.Error())
				}
				// Observed before the failure is handed back, because the hand-back is
				// the next state: the gate has to have left the check before the
				// developer's own state can be entered again.
				a.observeCheckEnded(ctx, err, stillRepairable)
				if err := a.repair(ctx, pathRefusalRepairPrompt(a.deliveredInvariants().Text(), a.scratch, a.pipeline.Config.Checks, refused.refusal, refused.set, a.state.RepairAttempts+1, limit)); err != nil {
					return err
				}
				continue
			}
			// A change nobody ran anything against is answered next, before the
			// checks are read for a failure, because the gate that refused it is
			// decided ahead of them: no check ran on this attempt either.
			var missing missingVerification
			if errors.As(err, &missing) {
				// The conflict is answered by the attempt that is owed evidence, so
				// what is outstanding now is the evidence.
				a.state.ReplayConflict = nil
				if a.state.RepairAttempts >= limit {
					a.observeCheckEnded(ctx, err, budgetSpent)
					return a.blockOnMissingVerification(missing, limit)
				}
				if spent, chargeErr := a.chargeReplayStop(); chargeErr != nil || spent {
					a.observeCheckEnded(ctx, err, budgetSpent)
					return a.replayStopEnds(chargeErr, err.Error())
				}
				a.observeCheckEnded(ctx, err, stillRepairable)
				if err := a.repair(ctx, verificationRepairPrompt(a.deliveredInvariants().Text(), a.scratch,
					missing.verification, a.pipeline.Config.Checks, a.state.RepairAttempts+1, limit)); err != nil {
					return err
				}
				continue
			}
			// Verification that could not run at all is not something a developer
			// can repair, so it ends the run rather than spending an attempt.
			var failing checkFailure
			if !errors.As(err, &failing) {
				a.observeCheckEnded(ctx, err, stillRepairable)
				return err
			}
			a.recordCheckFailure(failing.result)
			if a.state.RepairAttempts >= limit {
				a.observeCheckEnded(ctx, err, budgetSpent)
				return a.blockOnFailingCheck(limit)
			}
			if spent, chargeErr := a.chargeReplayStop(); chargeErr != nil || spent {
				a.observeCheckEnded(ctx, err, budgetSpent)
				return a.replayStopEnds(chargeErr, err.Error())
			}
			a.observeCheckEnded(ctx, err, stillRepairable)
			if err := a.repair(ctx, checkRepairPrompt(a.deliveredInvariants().Text(), a.scratch, a.pipeline.Config.Checks, *a.state.CheckFailure, a.state.RepairAttempts+1, limit)); err != nil {
				return err
			}
			continue
		}
		a.observe(ctx, deliveryCheck, "passed")
		decision, err := a.reviewChange(ctx)
		if err != nil {
			a.observeReviewEnded(ctx, "", err, stillRepairable)
			return err
		}
		if decision == review.DecisionApprove {
			a.observeReviewEnded(ctx, decision, nil, stillRepairable)
			return nil
		}
		// An escalation ends the loop where it was raised. There is nothing to hand
		// back — the reviewer's whole verdict is that no change to this change would
		// help — so spending another attempt on it would be the repair round against
		// a wall this verb exists to stop.
		if decision == review.DecisionEscalate {
			a.observeReviewEnded(ctx, decision, nil, stillRepairable)
			return escalationRaised{}
		}
		if a.state.RepairAttempts >= limit {
			a.observeReviewEnded(ctx, decision, nil, budgetSpent)
			return a.blockOnUnresolvedFindings(limit)
		}
		if spent, chargeErr := a.chargeReplayStop(); chargeErr != nil || spent {
			a.observeReviewEnded(ctx, decision, nil, budgetSpent)
			return a.replayStopEnds(chargeErr, "independent review requires repair: "+a.state.ReviewSummary)
		}
		a.observeReviewEnded(ctx, decision, nil, stillRepairable)
		prompt, err := repairPrompt(a.deliveredInvariants().Text(), a.state.ReviewSummary, a.scratch, a.pipeline.Config.Checks, a.state.ReviewFindingDetails, a.state.RepairAttempts+1, limit)
		if err != nil {
			return err
		}
		if err := a.repair(ctx, prompt); err != nil {
			return err
		}
	}
}

// replayStopEnds is how a repair loop ends on a replay charged past the
// integration budget: the save that charged it failed, or the charge spent more
// than the budget permits and the run blocks on the change.
func (a *activeRun) replayStopEnds(chargeErr error, stop string) error {
	if chargeErr != nil {
		return chargeErr
	}
	return a.blockOnChargedReplay(stop)
}

// repairBudget is how many repair attempts this run may make: what the project
// configured, plus whatever triage has granted it to continue on. A run nothing
// continued is the configured budget unchanged, which is every run until triage
// re-enters one.
func (a *activeRun) repairBudget() int {
	return a.state.RepairBudget(a.pipeline.Config.Execution.RepairAttemptsBeforeReplan)
}

// repair records one attempt against the budget and then hands the failure back
// to the developer. The attempt is recorded before the developer is invoked, so
// an interrupted attempt still counts and a restart resumes at the attempt the
// run reached rather than buying it another one. The same developer continues in
// the same worktree, resuming its session so the attempt keeps the context it
// already built.
func (a *activeRun) repair(ctx context.Context, prompt string) error {
	a.state.RepairAttempts++
	a.state.Phase = runstate.PhaseDeveloping
	a.state.UpdatedAt = a.pipeline.clock().Now()
	a.outcome.RepairAttempts = a.state.RepairAttempts
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save repair attempt %d: %w", a.state.RepairAttempts, err)
	}
	return a.develop(ctx, prompt, a.state.ProviderSessionID)
}

// recordCheckFailure makes the failing check the run's outstanding repair input.
// The review recorded beside it judged an earlier change, so its verdict is
// cleared: nothing may read it as a judgement of the change that now fails. Its
// findings are not dropped with it. They are what the reviewer asked for, the
// failing change is the developer's answer to them, and no reviewer has yet
// seen whether that answer holds, so they go onto the failure as the review it
// came after (see reviewBeforeCheck).
func (a *activeRun) recordCheckFailure(result checks.Result) {
	before := a.reviewBeforeCheck()
	a.clearReviewEvidence()
	a.state.ChecksPassed = nil
	// A conflict handed back before this attempt has been answered by it: what
	// is outstanding now is the check the answer fails.
	a.state.ReplayConflict = nil
	a.state.CheckFailure = &runstate.CheckFailure{
		Command:      result.Command,
		ExitCode:     result.Process.ExitCode,
		Output:       boundedCheckOutput(result),
		ReviewBefore: before,
	}
}

// reviewBeforeCheck is the review a check failing now came after, if one
// recorded findings no reviewer has judged answered since. That is the review
// recorded beside the failure, or, where this is a second failure in a row, the
// one the first failure kept: the attempt between them never reached a
// reviewer, so the findings are as outstanding as they were.
func (a *activeRun) reviewBeforeCheck() *runstate.ReviewBefore {
	if len(a.state.ReviewFindingDetails) > 0 {
		return &runstate.ReviewBefore{
			Decision:       a.state.ReviewDecision,
			Summary:        a.state.ReviewSummary,
			ReviewedCommit: a.state.ReviewHeadCommit,
			Findings:       slices.Clone(a.state.ReviewFindingDetails),
		}
	}
	if a.state.CheckFailure != nil {
		return a.state.CheckFailure.ReviewBefore
	}
	return nil
}

// recordPathRefusal makes the refused paths the run's outstanding repair input.
// It clears both of the others for the reason recordCheckFailure clears the
// findings, and it clears one more of them than that does: this gate is decided
// in front of the checks, so a check failure recorded beside it describes a
// suite that did not run on the change now in the worktree.
func (a *activeRun) recordPathRefusal(refusal runstate.PathRefusal) {
	a.clearReviewEvidence()
	a.state.CheckFailure = nil
	a.state.ChecksPassed = nil
	a.state.ReplayConflict = nil
	recorded := refusal
	a.state.PathRefusal = &recorded
}

// blockOnUnresolvedFindings ends a run whose repair budget is spent. The design
// hands control back to a development manager at this point; that role does not
// exist yet, so the unresolved findings are recorded as a durable blocker on the
// work item rather than disappearing along with the failed run.
func (a *activeRun) blockOnUnresolvedFindings(limit int) error {
	cause := fmt.Errorf("independent review requires repair after %d of %d permitted attempt(s): %s",
		a.state.RepairAttempts, limit, a.outcome.ReviewSummary)
	if err := a.block(renderBlockerNotes(a.outcome, limit)); err != nil {
		return stoppedBy(runstate.StopRepairBudget, withFailedRecord(cause, fmt.Errorf("record unresolved review findings as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopRepairBudget, cause)
}

// blockOnFailingCheck ends a run whose repair budget was spent on a check that
// still fails. It is the check-side twin of blockOnUnresolvedFindings: the run
// cannot reach review or integration, so what stopped it is recorded on the work
// item rather than disappearing along with the failed run.
func (a *activeRun) blockOnFailingCheck(limit int) error {
	failure := *a.state.CheckFailure
	cause := fmt.Errorf("verification failed after %d of %d permitted attempt(s): %s exited with %d",
		a.state.RepairAttempts, limit, failure.Command, failure.ExitCode)
	if err := a.block(renderCheckBlockerNotes(a.outcome, failure, limit)); err != nil {
		return stoppedBy(runstate.StopRepairBudget, withFailedRecord(cause, fmt.Errorf("record the failing check as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopRepairBudget, cause)
}

// verifyHandback proves the worktree a run is re-entered in still holds the
// change the next step is about, before that step spends anything on it.
//
// This is the enforcement rather than the courtesy. The triage action that
// carries out a handback asks the same question before it spends the item's
// grant, and asking it here is what makes the answer bind every route into a
// resumed run — that action, an interrupted process a later invocation picks up,
// and whatever re-entry is built next, none of which will mention this. The
// failure it catches is silent by construction: a worktree that lost its change
// looks exactly like a valid one, so what is spent on it comes back as an empty
// repair, a review round burned on an empty diff, or the change reinvented, and
// the run's own record afterwards says none of those.
//
// It refuses to a person rather than starting over. Whether the change was never
// seeded or somebody removed it is not something this can tell, and both are
// decisions about work that may still exist on the preserved branch.
func (a *activeRun) verifyHandback(ctx context.Context) error {
	if !resumesAnExistingChange(a.state) {
		return nil
	}
	if err := preservedChangeHeld(ctx, a.pipeline.Worktrees, a.state); err != nil {
		return a.blockOnMissingPreservedChange(err)
	}
	return nil
}

// blockOnMissingPreservedChange ends a run re-entered to continue a change its
// worktree does not hold. It is the handback-side twin of the repair blockers:
// nothing here says the change was wrong, and the branch the run recorded may
// still carry every line of it, so what this hands a person is where to go
// looking rather than a verdict.
func (a *activeRun) blockOnMissingPreservedChange(cause error) error {
	blocked := fmt.Errorf("%w: run %s was picked up again at the %s phase and %v", ErrPreservedChangeMissing, a.state.RunID, a.state.Phase, cause)
	// The environment is what refused this round, and saying so here is what lets
	// the settle give back what the round would otherwise have spent. It is
	// recorded before the block, so the terminal write that carries the blocker
	// carries the cause with it.
	a.recordEnvironmentalRefusal(runstate.CauseHandbackMissingChange, cause.Error(), nothingRan)
	if err := a.block(renderMissingPreservedChangeNotes(a.outcome, blocked.Error())); err != nil {
		return stoppedBy(runstate.StopOutside, withFailedRecord(blocked, fmt.Errorf("record the missing preserved change as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopOutside, blocked)
}

// nothingRan says a refusal happened before anything this round would have
// delivered could exist, and ranAnyway that it did not. They are named rather
// than passed as bare booleans, because which one a site says is the whole of
// what makes the emptiness question answerable on a run whose worktree already
// holds an earlier round's work.
const (
	nothingRan = true
	ranAnyway  = false
)

// recordEnvironmentalRefusal notes on the run that the environment, rather than
// the work, is why this round has nothing to show. It is written where the
// refusal is decided, because that is the only place that knows which cause it
// was and whether anything of the round ran; what the record is worth is decided
// at settle, which is the first point the other half of the definition can be
// asked.
//
// Nothing here is saved on its own. Every caller is a step away from the
// terminal write that ends the run, and a second write would be one more chance
// for the record and the item to disagree about what stopped it — which is the
// reason the blocker is not saved here either.
func (a *activeRun) recordEnvironmentalRefusal(cause runstate.EnvironmentalCause, detail string, nothingOfItRan bool) {
	refusal := &runstate.EnvironmentalRefusal{
		Cause:      cause,
		Detail:     singleLine(detail, runstate.MaxEnvironmentalDetailBytes),
		NothingRan: nothingOfItRan,
		RecordedAt: a.pipeline.clock().Now().UTC(),
	}
	a.state.Environmental = refusal
	a.outcome.Environmental = refusal
}

// settleEnvironmentalRound classifies the round this run is ending, and gives
// back what an environmental one must never have spent.
//
// The class is the conjunction and nothing less. The run recorded a cause the
// harness is answerable for, and the change that round was to deliver is not
// there. A cause on its own excuses nothing — a round that recorded one and
// delivered a change anyway spends exactly as any round does — and an empty
// delivery on its own excuses nothing either, which is what keeps a developer
// that did nothing out of a class built for a harness that handed it nothing.
//
// It runs here because here is the first point both halves are known, and before
// the stoppage is docketed, so the entry a development manager reads carries the
// budgets as this settle leaves them rather than as the round spent them.
//
// It never fails the run. The run is already ending, and a return that could not
// be written is a fact for whoever reads the record rather than a second reason
// to end it — so it is recorded on the refusal, where the docket and the thread
// both carry it, and the round stays spent until somebody looks.
func (a *activeRun) settleEnvironmentalRound() {
	refusal := a.state.Environmental
	if refusal == nil || refusal.Settled {
		return
	}
	// Whatever this concludes, it concludes once. A round is settled where it
	// ends, and a later round of the same run is judged on its own evidence rather
	// than inheriting a cause recorded before it.
	refusal.Settled = true
	settleCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	delivered, err := a.roundDelivered(settleCtx, refusal)
	if err != nil {
		// A round whose delivery cannot be read is left spent. That is the
		// direction this must fail in: an item charged a round it should have kept
		// is visible in its counters, and one credited a round it did spend is a
		// budget nothing bounds.
		refusal.Problem = singleLine(fmt.Sprintf(
			"whether run %s delivered anything could not be read, so the round was left spent: %v", a.state.RunID, err),
			runstate.MaxEnvironmentalProblemBytes)
		return
	}
	if delivered {
		return
	}
	refusal.Refused = true
	// The review round the item was charged against its cap, given back under the
	// attempt that produced it and the process that charged it — the attempt keeps
	// the return to this round rather than to whatever the item spent before it,
	// and the process keeps it to a round this run's own charge bought. A run
	// re-entered at the review carries the same attempt number as the process
	// before it, so without the second key a refusal here would give back a round
	// that process spent on a verdict the item really got.
	attempt := runstate.RoundKey(a.state.RunID, a.state.RepairAttempts)
	charger, err := a.chargingProcess()
	if err != nil {
		refusal.Problem = singleLine(fmt.Sprintf(
			"the process charging attempt %s could not be identified, so the review round it was charged was left spent: %v", attempt, err),
			runstate.MaxEnvironmentalProblemBytes)
		refusal.GrantReturned = a.state.ReturnGrantedRound()
		a.outcome.Environmental = refusal
		return
	}
	_, round, err := a.pipeline.Store.Triage().ReturnReviewRound(settleCtx, a.state.WorkItemID, attempt, charger, a.pipeline.clock().Now())
	if err != nil {
		refusal.Problem = singleLine(fmt.Sprintf(
			"the review round attempt %s was charged could not be returned: %v", attempt, err),
			runstate.MaxEnvironmentalProblemBytes)
	}
	refusal.RoundReturned = round.Returned
	// A round another process is credited with is left spent, and the record says
	// so rather than reading as a refusal that found nothing to give back. The two
	// leave the item in different places, and this is the one that leaves it a
	// round nearer its cap.
	refusal.RoundLeftSpent = round.Mismatched
	refusal.RoundChargedBy = round.ChargedBy
	// And the granted repair round the continuation consumed, which is the budget
	// the field cases actually burned: the grant itself still stands, and this is
	// what stops the run's record saying it was carried out.
	refusal.GrantReturned = a.state.ReturnGrantedRound()
	a.outcome.Environmental = refusal
}

// roundDelivered reports this round having left a change behind.
//
// A refusal that says nothing of this round ran answers it without reading
// anything, and that is the case the worktree cannot answer. A round of a repair
// grant runs in the worktree earlier rounds already filled, so what is in it
// answers "did this run ever deliver anything" rather than "did this round" —
// and a granted round whose developer the machine never started would read as a
// delivery, spend the grant on work no agent did, and be described on every
// surface as a round that delivered a change. Where the refusal knows nothing
// ran, this round added nothing whatever the worktree holds.
//
// Everything else is measured against the worktree rather than trusted: a cause
// recorded from the error that ended a run can belong to a round that ran and
// delivered, and the conjunction the class is defined by is what stops one
// excusing the other. It asks the worktree rather than the run's recorded
// summary, because a summary is written by steps a refused round never reached,
// and it asks it through the run's own durable record, which is the only account
// of that worktree surviving the process that made it.
func (a *activeRun) roundDelivered(ctx context.Context, refusal *runstate.EnvironmentalRefusal) (bool, error) {
	if refusal.NothingRan {
		return false, nil
	}
	// A round the provider's usage window ended delivered nothing anybody judged,
	// whatever the refused invocation left in the worktree: no check and no
	// reviewer read it, and the branch keeps it for the run that is pulled once
	// the window resets.
	if refusal.Cause.EndsTheRoundUnjudged() {
		return false, nil
	}
	// A run with no worktree recorded delivered nothing, and that is proved rather
	// than assumed: the harness never gave it anywhere to deliver to. It is the
	// state a run refused before its worktree could be cut is in, which is exactly
	// a round the environment turned away.
	if a.state.WorktreePath == "" || a.state.BaseCommit == "" {
		return false, nil
	}
	changed, err := a.pipeline.Worktrees.ChangedPaths(ctx, worktreeOf(a.state))
	if err != nil {
		return false, err
	}
	return len(changed) > 0, nil
}

// environmentalCauseOf reports the environmental cause a failure names, where it
// names one. It is how the causes the harness recognizes from the failure alone
// reach the record: a run turned away because the primary checkout was not the
// harness's to cut from, and one whose provider invocation the machine never
// started at all. Both are the environment rather than the work, and neither has
// a refusal site of its own to record at — they surface as the error that ends
// the run.
//
// Nothing here guesses. A cause is named only by a sentinel the refusing package
// declares, so a message somebody rewords cannot silently move a round into or
// out of a class that returns budget. And it is the step's own failure that is
// read, not the failure of any write the run then attempted about it, for the
// reason integrationStopCauseOf reads the same.
func environmentalCauseOf(failure error) (runstate.EnvironmentalCause, bool) {
	failure = stepCauseOf(failure)
	switch {
	case errors.Is(failure, gitworktree.ErrPrimaryNotReady):
		return runstate.CauseDirtyPrimary, true
	case errors.Is(failure, gitworktree.ErrCheckoutKilled):
		return runstate.CauseWorktreeCheckoutKilled, true
	case errors.Is(failure, execution.ErrProcessNotStarted):
		return runstate.CauseSandboxSpawnFailure, true
	default:
		return "", false
	}
}

// stopAndFailedRecord is a step's own failure and the failure of the write the
// run then attempted about it — a blocker the tracker did not take, a replayed
// base the store would not save — kept apart rather than joined. Its message is
// the two in order, exactly as errors.Join's was, so nothing a reader was shown
// is lost, and both are still found by errors.Is; what changes is that the
// cause is still the cause afterwards, which stepCauseOf reads back.
//
// It exists because of what the join did on yoyodyne-ifd.441. A replay onto
// main conflicted, the blocker write about it timed out, and the two were
// joined into the error that ended the run; the integration-stop classifier,
// reading the closed set of transport errors anywhere in that message, matched
// the timed-out write on its tail and recorded a conflict a person has to settle
// as weather the harness could resume past. The resume would have met the same
// conflict, and the docket sent the development manager to it.
type stopAndFailedRecord struct {
	cause  error
	record error
}

func (f stopAndFailedRecord) Error() string   { return f.cause.Error() + "\n" + f.record.Error() }
func (f stopAndFailedRecord) Unwrap() []error { return []error{f.cause, f.record} }

// withFailedRecord pairs what stopped a step with the failure of the write the
// run then attempted about it. A step that did not fail has only the write's
// failure to report, and reports that.
func withFailedRecord(cause, record error) error {
	if cause == nil {
		return record
	}
	return stopAndFailedRecord{cause: cause, record: record}
}

// stepCauseOf is the step's own failure inside an error that ended a run: what
// stopped the step, before any write the run attempted about it afterwards.
// Every classification of a stop reads this rather than the whole error,
// because the whole error carries the harness's own failures to record the
// stop, and those are transport-shaped by nature — a tracker that timed out, a
// store that would not save — while the stop they were about may be anything.
func stepCauseOf(failure error) error {
	var paired stopAndFailedRecord
	for errors.As(failure, &paired) {
		failure = paired.cause
	}
	return failure
}

// integrationStopCauseOf reports the environmental cause a failure between an
// approval and a promotion names, where it names one. It is wider than
// environmentalCauseOf by exactly the class that stopped yoyodyne-ifd.309's
// approved change the second time: a transport the harness depends on not
// answering — a tracker read killed under load, a forge or a network that
// reset — which is the class the recovery package already waits out at the
// boundaries that have a window, and which reaches a step with no window as the
// error that ends the run.
//
// Nothing here guesses either. The dirty checkout is named by the sentinel the
// refusing package declares, and the transport class by the same closed reading
// the recovery package applies to every boundary it retries — so a failure this
// classifies is one the harness would have asked again somewhere else, and a
// failure it does not is one somebody has to look at. A replay that conflicted
// and an approval that could not be shown independent are the second kind. A replay the harness itself killed is the first kind,
// named by its own sentinel: it is never a conflict, and it is returned only
// once the worktree is back on its branch (yoyodyne-ifd.406).
//
// Two more are the first kind although a person clears them, because what they
// clear is the branches or the credential and never the change: a target the
// harness would not catch up before promoting, named by ErrDivergedTarget, and a
// remote that refused the harness's key or login, named by the sentinel the
// worktree manager declares for it. Three approved changes stopped on those
// cost a re-run each before they were here (yoyodyne-ifd.429.9). The credential
// is asked before the transport class, because an SSH refusal is followed by a
// closed connection and the refusal is what it was.
//
// A replay conflict is asked before all of them, because it is settled before
// anything is written about it and nothing written afterwards unsettles it. The
// error that ends a conflicted run carries the conflict joined to whatever
// failed while it was being recorded — a tracker write that timed out, a record
// the store would not take — and those read as the transport class or a dirty
// checkout on their own. Run run-c4f75e5b's conflict reached the record as a
// transport failure that way, and the docket named a resume that could only
// conflict again (yoyodyne-ifd.429.10).
//
// It reads the step's own cause and not the whole error. A stop the run then
// failed to record carries that failure too, and a blocker write that timed out
// reads as transport whatever it was recording — which is how yoyodyne-ifd.441's
// replay conflict came to be recorded as an environmental stop. The sentinel
// above refuses the conflict whatever wrapped it; reading the step's cause is
// what keeps any other stop from being classified by the write that followed it.
func integrationStopCauseOf(failure error) (runstate.EnvironmentalCause, bool) {
	failure = stepCauseOf(failure)
	switch {
	case errors.Is(failure, gitworktree.ErrRebaseConflict):
		return "", false
	case errors.Is(failure, gitworktree.ErrPrimaryNotReady):
		return runstate.CauseDirtyPrimary, true
	case errors.Is(failure, gitworktree.ErrReplayKilled):
		return runstate.CauseReplayKilled, true
	case errors.Is(failure, ErrDivergedTarget):
		return runstate.CauseDivergedTarget, true
	case errors.Is(failure, gitworktree.ErrRemoteAuthRefused):
		return runstate.CauseRemoteAuthRefused, true
	case recovery.Recoverable(failure):
		return runstate.CauseTransportFailure, true
	default:
		return "", false
	}
}

// recordIntegrationStop notes on the run that the environment stopped an
// approved change short of its promotion, where that is what the failure ending
// it says. It is the classification that makes the run resumable at that step
// with its approval standing, so it is written only where both halves hold: the
// approval is standing with nothing promoted, and the cause is one the
// environment answers for. A run stopped for anything else records nothing here
// and is decided about as it always was — and a run that already recorded what
// stopped it as a replay conflict is never asked, because the record refuses
// the two together and the conflict was decided where it was met.
//
// Nothing here is saved on its own. Every caller is a step away from the
// terminal write that ends the run, and a second write would be one more chance
// for the record and the item to disagree about what stopped it.
func (a *activeRun) recordIntegrationStop(cause error) {
	if !a.state.ApprovedAwaitingIntegration() || a.state.ReplayConflict != nil {
		return
	}
	named, environmental := integrationStopCauseOf(cause)
	if !environmental {
		return
	}
	stopped := &runstate.IntegrationStop{
		Cause:      named,
		Detail:     singleLine(cause.Error(), runstate.MaxEnvironmentalDetailBytes),
		Phase:      a.state.Phase,
		RecordedAt: a.pipeline.clock().Now().UTC(),
	}
	a.state.IntegrationStop = stopped
	a.outcome.IntegrationStop = stopped
}

// refuseDispatchEnvironmentally records, on a run the harness turned away before
// it entered it at all, that the environment is what turned it away.
//
// It exists for the one refusal outside the run's own machinery: a resumed run
// turned back because the repository is not in a state anything may be resumed
// against. The dispatch never became a round, so what it delivered is nothing —
// not because a worktree is empty, which is how a round that ran is measured, but
// because nothing ran. That is why the class is settled here without asking the
// worktree: the worktree holds whatever earlier rounds left, and none of it is
// this dispatch's.
//
// It gives nothing back, and says so. Nothing was charged: the run stays exactly
// as the interrupted process left it, with its attempt and its grant still live
// and still to be spent when it is resumed. What this buys is not an accounting
// correction but the record and the surfaces saying what happened, which is
// otherwise lost in an error a caller prints once.
//
// A record that could not be written is reported beside the refusal rather than
// in place of it: the refusal stands either way, and the run is untouched.
func (p Pipeline) refuseDispatchEnvironmentally(state runstate.State, cause runstate.EnvironmentalCause, detail string) error {
	turned := &activeRun{pipeline: p, state: state}
	turned.recordEnvironmentalRefusal(cause, detail, nothingRan)
	turned.state.Environmental.Settled = true
	turned.state.Environmental.Refused = true
	turned.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(turned.state); err != nil {
		return fmt.Errorf("record that run %s was refused from outside the work: %w", state.RunID, err)
	}
	return nil
}

// blockOnRefusedPaths ends a run whose repair budget was spent still touching
// paths its work item does not grant. It is the scope-side twin of
// blockOnFailingCheck, and what it hands to a person is a decision rather than a
// defect: either the change keeps reaching for something outside its item, or the
// item is missing a grant it should have had. The note names both possibilities,
// because only a person can say which one it is.
func (a *activeRun) blockOnRefusedPaths(refused pathRefusal, limit int) error {
	cause := fmt.Errorf("protected paths refused after %d of %d permitted attempt(s): %s",
		a.state.RepairAttempts, limit, strings.Join(refused.refusal.Paths, ", "))
	if err := a.block(renderPathRefusalBlockerNotes(a.outcome, refused, limit)); err != nil {
		return stoppedBy(runstate.StopRepairBudget, withFailedRecord(cause, fmt.Errorf("record the refused protected paths as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopRepairBudget, cause)
}

// develop runs one developer attempt in the run's worktree and records what the
// provider reported. A repair attempt passes the recorded session so the
// developer continues the change it already made instead of re-deriving it.
//
// A provider that refuses the attempt does not end the run, whether it refused
// for want of capacity on the account or because its own servers could not serve
// it. The work was never judged in either case, so the attempt is waited out and
// reissued in the same worktree and the same session. Only a wait the harness
// cannot take — an unusable reset time, or one beyond the configured maximum —
// stops the run.
//
// An attempt the provider killed rather than refused is reissued too, against a
// budget rather than a clock. There is no condition to wait out: a connection
// that dropped is already gone, and the provider's own retry ladder is spent
// before the harness ever sees the terminal, so what a relaunch waits for has
// already been waited. The relaunch keeps the worktree and the session for the
// same reason a refusal's reissue does, and here it matters more: the attempt
// that died mid-response had already made part of the change, and continuing the
// session is what carries that work into the next attempt instead of asking a
// developer to derive it a second time.
func (a *activeRun) develop(ctx context.Context, prompt, sessionID string) error {
	if a.state.Document != nil {
		return errors.New("a document publication has no developer; its owning conversation must revise it")
	}
	// A change this developer proposed that the harness refused opens the prompt,
	// ahead of the contract and of whatever this invocation is actually for. It is
	// prepended here rather than built into each kind of prompt because here is the
	// one place every developer invocation passes through — the first attempt, both
	// kinds of repair, a resumed attempt, and a granted continuation — and a
	// refusal that reached only some of them would be a refusal the developer
	// learns of depending on why it was asked again.
	prompt = a.openWithDeveloperRefusals(prompt)
	// A run whose recorded backend cannot be invoked here ends before anything is
	// spent, rather than offering its session to a provider that never opened it
	// (recordedbackend.go).
	if _, _, err := a.pipeline.developerBackendFor(a.state); err != nil {
		a.observeDevelopEnded(ctx, err)
		return err
	}
	// A run pinned to an endpoint pair makes every attempt under one recorded
	// logical operation, on the endpoint that operation has selected, and
	// started behind a launch gate (developerrouting.go). route is nil for
	// every other run, which is invoked exactly as before.
	route, err := a.beginDeveloperOperation(ctx)
	if err != nil {
		a.observeDevelopEnded(ctx, err)
		return err
	}
	if route != nil {
		// The operation may have moved to its alternate before this process
		// picked the run up, so the backend is asked for again.
		if _, _, err := a.pipeline.developerBackendFor(a.state); err != nil {
			a.observeDevelopEnded(ctx, err)
			return err
		}
	}
	reasked := false
	for {
		// A stop is asked for before the hold, so a run the operator both stopped
		// and paused stops rather than parking on a hold nothing will lift for it.
		if err := a.stopRequested(); err != nil {
			a.observeDevelopEnded(ctx, err)
			return err
		}
		// The operator's hold is asked before every attempt, including the reissue
		// after a refusal: a developer invocation is the largest thing this harness
		// spends, and a pause that only covered the first one would let a run keep
		// spending for as long as the provider kept refusing it.
		if err := a.holdForOperator(ctx); err != nil {
			a.observeDevelopEnded(ctx, err)
			return err
		}
		if route != nil {
			var prepareErr error
			prompt, sessionID, prepareErr = a.prepareDeveloperAttempt(ctx, route, prompt, sessionID)
			if prepareErr != nil {
				a.observeDevelopEnded(ctx, prepareErr)
				return prepareErr
			}
		}
		responseStarted := a.pipeline.clock().Now()
		providerResult, err := a.attemptDevelopment(ctx, prompt, sessionID)
		// How a routed attempt ended is recorded before anything below reads
		// it, with whether its process tree is confirmed stopped.
		if route != nil {
			if finishErr := a.finishDeveloperAttempt(ctx, route, providerResult, err); finishErr != nil {
				a.observeDevelopEnded(ctx, finishErr)
				return finishErr
			}
		}
		// A run its hosting watch session stopped for a redeploy keeps what the
		// attempt left: the session it established, which is what the session that
		// comes back continues in, and the worktree committed under a context the
		// stop did not cancel — the stop is the harness's clock, not a verdict on
		// the work, and a commit made under the cancelled context would fail and
		// end the round before the session was recorded, leaving nothing a
		// continuation could pick up.
		if drained, forRedeploy := drainedForRedeploy(ctx); forRedeploy {
			sessionID = a.carrySession(providerResult.SessionID, sessionID)
			commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redeployCommitTimeout)
			committed := a.commitAttempt(commitCtx)
			cancel()
			ended := error(drained)
			if committed != nil {
				ended = errors.Join(drained, committed)
			}
			a.observeDevelopEnded(ctx, ended)
			return ended
		}
		// What the invocation left in the worktree is committed here, before
		// anything below decides what became of the invocation. Here is the one
		// place every ending passes through, and every ending below it either hands
		// the change to the checks and the reviewer or reissues the attempt into the
		// same worktree — and both of those want the branch tip to be what the last
		// invocation actually wrote. A commit made only on the accepted path is what
		// left run-f3755e3f reissuing its developer against findings it had already
		// fixed, with the branch tip four invocations behind the worktree.
		//
		// A commit that could not be made ends the round rather than carrying on:
		// the reviewer's evidence names a tip commit, and a tip that is not the
		// round's is an approval of something nobody read.
		if committed := a.commitAttempt(ctx); committed != nil {
			a.observeDevelopEnded(ctx, committed)
			return committed
		}
		// A provider nobody is logged into or nobody can reach is answered before
		// anything is counted, because what it asks for is the one wait that spends
		// nothing: no relaunch, no repair attempt, no blocker. The refused attempt
		// may still have established a session, which is kept for the reason a
		// limit's is.
		if outage, away := providerAway(providerResult, err); away {
			sessionID = a.carrySession(providerResult.SessionID, sessionID)
			if err := a.pauseForProviderOutage(ctx, outage); err != nil {
				a.observeDevelopEnded(ctx, err)
				return err
			}
			a.observe(ctx, deliveryDevelop, "reissued")
			continue
		}
		// An attempt the provider served, however it went, is the provider
		// answering again, and the process that finds that out is rarely the one
		// that met it refusing. A record that would not clear is said on the
		// outcome rather than allowed to end an attempt that was served.
		if err == nil && providerResult.ProviderOutage == nil {
			if servedErr := a.pipeline.noticeProviderServed(); servedErr != nil {
				a.outcome.ProviderOutageProblem = servedErr.Error()
			}
		}
		limit, refusedForLimit := refusedForUsageLimit(providerResult, err)
		overload, refusedForOverload := refusedForServerOverload(providerResult, err)
		// An attempt served is also the provider saying the window of the model it
		// asked for is open on this account, whatever reset an earlier refusal of it
		// quoted. The model is the one this invocation recorded asking for, as
		// attemptDevelopment wrote it, rather than the configuration: nothing in a
		// run moves an attempt onto another model, and if anything ever does, what
		// the invocation asked for is still what served it.
		if servedCleanly(providerResult, err) {
			what := fmt.Sprintf("a developer attempt of run %s of %s", a.state.RunID, a.state.WorkItemID)
			if servedErr := a.pipeline.noticeCapacityServed(a.state.AccountAlias, a.state.ProviderModel, what); servedErr != nil {
				a.outcome.CapacityServedProblem = appendProblem(a.outcome.CapacityServedProblem, servedErr.Error())
			}
		}
		transient, died := diedTransiently(providerResult.TransientFailure, providerResult.Process.Status, providerResult.IsError, err)
		_, bounded := providerStopReason(providerResult.Process.Status)
		if (died || (bounded && err != nil)) && a.pipeline.Availability != nil {
			from := providerResult.Process.StartedAt
			if from.IsZero() {
				from = responseStarted
			}
			a.state.LastSequence = max(a.state.LastSequence, providerResult.LastEvent)
			detail := transient.Detail
			if !died {
				detail = err.Error()
			}
			account, causeErr := a.responseCause(domain.RoleDeveloper, detail, from, providerResult.Process.FinishedAt)
			if causeErr != nil {
				a.observeDevelopEnded(ctx, causeErr)
				return causeErr
			}
			providerResult.LastEvent = a.state.LastSequence
			if died {
				transient.Detail = account
			} else {
				err = fmt.Errorf("%w; %s", err, strings.TrimPrefix(account, detail+"; "))
			}
		}
		if !refusedForLimit && !refusedForOverload && !a.mayRelaunch(died) {
			// The relaunch budget is spent, which is the right bound for a provider
			// dying in ways nobody can classify. A death that is plainly a dropped
			// connection is not one of those, and stopping a run on one is what cost
			// four runs their completed work: so it is waited out on a backoff and
			// asked again past the budget, and only a spent recovery window blocks.
			if died {
				retried, recoverErr := a.recoverProvider(ctx, transient)
				if recoverErr != nil {
					a.observeDevelopEnded(ctx, recoverErr)
					return recoverErr
				}
				if retried {
					sessionID = a.carrySession(providerResult.SessionID, sessionID)
					if route != nil {
						route.relaunch = true
					}
					a.observe(ctx, deliveryDevelop, "reissued")
					continue
				}
			}
			recorded := a.recordDevelopment(ctx, providerResult, err)
			// A transient death with the budget already spent is recorded exactly as
			// any other developer failure is — the attempt's changes are part of what
			// the run has to say about itself — and then handed to a person, because
			// nothing else is going to relaunch it.
			if died {
				a.observe(ctx, deliveryDevelop, "relaunches-spent")
				return a.blockOnSpentRelaunchBudget(ctx, transient, recorded)
			}
			// An invocation that ended without accounting for the work is asked once
			// more, in its own session, exactly as a verdict the review contract could
			// not read is. Nothing is wrong with the change: the worktree holds
			// whatever the developer did, and what is missing is the developer saying
			// what that was. Refusing outright would fail a run over a reply, and
			// recording the interim line as the account is the silence this stops.
			//
			// The second one ends the run and names why. Two replies that account for
			// nothing is a developer that will not account for its work, and a third
			// invocation asking again would spend the largest thing the harness buys
			// on the same question.
			var unaccounted unaccountedReply
			if errors.As(recorded, &unaccounted) {
				if !reasked {
					reasked = true
					sessionID = a.carrySession(providerResult.SessionID, sessionID)
					// The re-ask is a developer invocation like any other, and the
					// account it asks for is exactly where a developer would say it had
					// raised a proposal — so what the interim reply proposed and the
					// harness refused opens this prompt too.
					prompt = a.openWithDeveloperRefusals(accountPrompt(a.deliveredInvariants().Text(), a.scratch, unaccounted.reason, a.pipeline.Config.Checks))
					a.observe(ctx, deliveryDevelop, "reissued")
					continue
				}
				recorded = stoppedBy(runstate.StopDeveloperAccount, fmt.Errorf("the developer ended two invocations without accounting for the work: %s", unaccounted.reason))
			}
			if recorded != nil {
				// The developer invocation is what this round delivers with, so an
				// invocation the machine never started is a round that added nothing —
				// and it is said here because here is the only place that knows which
				// invocation it was. The same failure from a check would be a round
				// whose developer had already written a change, and this is what keeps
				// the two apart. The settle needs it: on a granted repair the worktree
				// holds what earlier rounds left, so nothing else could tell that this
				// round delivered nothing.
				if errors.Is(recorded, execution.ErrProcessNotStarted) {
					a.recordEnvironmentalRefusal(runstate.CauseSandboxSpawnFailure, recorded.Error(), nothingRan)
				}
				a.observeDevelopEnded(ctx, recorded)
				return recorded
			}
			// The attempt that just finished is what publishes. Its work is already
			// committed — commitAttempt did that above, for this ending and for
			// every other one — so what happens here is the push and the pull
			// request, and it happens on the accepted path because that is the
			// change the checks and the reviewer are about to judge.
			//
			// The developer has answered the operation, so a routed run closes it
			// here: what follows is publishing, the checks and the reviewer, and the
			// next thing asked of the developer is a new operation.
			if err := a.completeDeveloperOperation(ctx, route, "the developer answered it"); err != nil {
				a.observeDevelopEnded(ctx, err)
				return err
			}
			published := stoppedBy(runstate.StopPublish, a.publishAttempt(ctx))
			a.observeDevelopEnded(ctx, published)
			return published
		}
		// The refused attempt may still have established a session. Continuing in
		// it is what lets the reissued attempt resume in context rather than
		// starting the work over.
		sessionID = a.carrySession(providerResult.SessionID, sessionID)
		// An exhausted limit is answered first when a refused attempt somehow
		// reports both: it is the longer wait of the two, and waiting an overload's
		// interval into a limit that has hours left would spend the budget on
		// attempts the account cannot serve.
		if refusedForLimit {
			// A routed operation still on its primary moves to its alternate once,
			// automatically, when the alternate can serve; the refused attempt's
			// session stays with the primary. Otherwise the limit is waited out on
			// the endpoint the operation has selected, exactly as below.
			if route != nil {
				evidence := fmt.Sprintf("developer attempt %s on %s was refused for its usage limit", route.attempt, describeEndpoint(a.selectedDeveloperEndpoint()))
				if limit.Kind != "" {
					evidence += " (" + limit.Kind + ")"
				}
				switched, switchErr := a.switchDeveloperEndpoint(ctx, route, runstate.SwitchUsageLimit, route.attempt, evidence)
				if switchErr != nil {
					a.observeDevelopEnded(ctx, switchErr)
					return switchErr
				}
				if switched {
					sessionID = ""
					a.observe(ctx, deliveryDevelop, "reissued")
					continue
				}
			}
			if err := a.pauseForUsageLimit(ctx, limit); err != nil {
				a.observeDevelopEnded(ctx, err)
				return err
			}
			// The wait was taken in this process, so the attempt is reissued here
			// rather than by whatever picks the run up: the same state again, which
			// is the one transition the definition has for all four of these.
			a.observe(ctx, deliveryDevelop, "reissued")
			continue
		}
		// An overload is answered before a transient death for the same reason: a
		// terminal the backend somehow reported as both is the one condition here
		// that names a wait, and taking the wait costs a relaunch nothing while
		// skipping it spends the budget on a server that has not recovered yet.
		if refusedForOverload {
			if err := a.pauseForServerOverload(ctx, overload); err != nil {
				a.observeDevelopEnded(ctx, err)
				return err
			}
			a.observe(ctx, deliveryDevelop, "reissued")
			continue
		}
		if err := a.recordRelaunch(); err != nil {
			a.observeDevelopEnded(ctx, err)
			return err
		}
		if route != nil {
			route.relaunch = true
		}
		a.observe(ctx, deliveryDevelop, "reissued")
	}
}

// carrySession keeps the session an ended attempt established, so the attempt
// reissued after it resumes in context rather than deriving the change again. It
// reports what the next attempt should continue in, which is the session the
// last one reported where it reported one and what the run was already carrying
// otherwise.
func (a *activeRun) carrySession(reported, current string) string {
	if reported == "" {
		return current
	}
	a.state.ProviderSessionID = reported
	a.outcome.ProviderSessionID = reported
	return reported
}

// mayRelaunch reports a provider death this run still has budget to absorb. It
// reads the durable count rather than anything this process is holding, so a run
// resumed after a crash mid-relaunch is bounded by what it already spent.
func (a *activeRun) mayRelaunch(died bool) bool {
	return died && a.state.TransientRelaunches < a.pipeline.Config.Execution.TransientRelaunchesBeforeBlocking
}

// diedTransiently reports an invocation the provider ended without judging the
// work, on something that may not happen again. It takes the same shape as the
// two refusals beside it: a transient failure reported alongside an invocation
// that still produced its answer is evidence rather than a death, so only one
// accompanying a failed attempt relaunches the run.
//
// An invocation the harness stopped on time is never one of these, however the
// provider's last words read. That stop is the harness's own decision, the run it
// leaves behind is owed a continuation rather than a relaunch, and charging it to
// a budget for the provider's weather would spend the run's tolerance on the
// harness's own clock.
func diedTransiently(failure *backend.TransientFailure, status execution.ProcessStatus, isError bool, err error) (backend.TransientFailure, bool) {
	if failure == nil || (err == nil && !isError) {
		return backend.TransientFailure{}, false
	}
	if _, stopped := providerStopReason(status); stopped {
		return backend.TransientFailure{}, false
	}
	return *failure, true
}

// recordRelaunch counts one provider death against the budget and records what
// killed the attempt. Like a repair attempt it is recorded before the relaunch it
// authorizes happens, so a process that dies here comes back to the budget it had
// spent rather than to a fresh one — which is the difference between a bounded
// self-repair and an unbounded loop that a crash resets.
//
// Nothing about the attempt's own outcome is recorded, because there is no
// outcome: the provider judged nothing, and writing a failure the next attempt
// will overwrite would make a run that recovered look like one that failed first.
// What killed the attempt is already durable without this — the terminal the
// provider sent is in the run's event stream — so the count is all this has to
// add.
func (a *activeRun) recordRelaunch() error {
	a.state.TransientRelaunches++
	a.state.UpdatedAt = a.pipeline.clock().Now()
	a.outcome.TransientRelaunches = a.state.TransientRelaunches
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save transient relaunch %d: %w", a.state.TransientRelaunches, err)
	}
	return nil
}

// blockOnSpentRelaunchBudget ends a run the provider kept killing. It is the
// provider-side twin of the repair blockers, and it says the opposite thing about
// the change: every gate that ran was satisfied or never got to run, and nothing
// here found anything wrong with the work. What the reader has to look at is why
// the provider will not carry this run, which is the one question the harness
// cannot answer by asking again.
//
// recorded is what recording the dead attempt reported, and it is read rather
// than discarded. Its status is the run's, so a provider killed by a cancelled
// context is not filed as one that failed; and anything it says beyond the
// provider's own failure — a store that would not take the record, a change
// summary that could not be taken — travels with the blocker, because that is a
// second thing wrong rather than another way of saying this one.
func (a *activeRun) blockOnSpentRelaunchBudget(ctx context.Context, failure backend.TransientFailure, recorded error) error {
	limit := a.pipeline.Config.Execution.TransientRelaunchesBeforeBlocking
	class := runstate.StopRelaunchBudget
	boundary := runstate.RetryProviderInvocation
	if recovery.RecoverableDetail(failure.Detail) && a.state.RetryWaited(boundary)+recovery.Interval(a.state.RetryAttempts(boundary)+1) > recovery.Window {
		class = runstate.StopRecoveryWindow
	}
	blocked := fmt.Errorf("the provider ended this run without judging the work after %d of %d permitted relaunch(es): %s",
		a.state.TransientRelaunches, limit, failure.Detail)
	var reported phaseError
	if recorded != nil && !errors.As(recorded, &reported) {
		blocked = errors.Join(blocked, recorded)
	}
	cause := error(phaseError{status: failureStatus(ctx, recorded), cause: blocked})
	if err := a.block(renderRelaunchBlockerNotes(a.outcome, failure, a.state.CheckFailure, a.state.PathRefusal, a.state.ReplayConflict, limit)); err != nil {
		return stoppedBy(class, withFailedRecord(cause, fmt.Errorf("record the spent relaunch budget as a blocker: %w", err)))
	}
	return stoppedBy(class, cause)
}

// account is where this run's invocations are made. It is read off the run's own
// record rather than chosen again, which is what affinity means here: a repair
// attempt, a resumed attempt, and the review of the change are all the same run,
// and a run that moved between accounts mid-flight would leave its spend split
// across subscriptions with nothing saying so.
func (a *activeRun) account() config.AccountEndpoint {
	return a.pipeline.accountFor(a.state.AccountAlias)
}

// developerModel is the selector this run's developer invocations ask for. It is
// read off the run's own record for the reason the account is: the model was
// chosen once, from the labels the item was pulled with, and a run that resolved
// execution.developer_models again per invocation would move mid-flight the
// first time the mapping was edited under it. A run whose record names none —
// a project that configured no mapping, and every run written before the
// mapping existed — asks for the developer's configured model.
//
// A run recorded on a backend other than the configured developer's asks for
// what that backend reported it ran, or for the backend's own default where it
// reported nothing: the configured developer's model belongs to the other
// backend (see recordedbackend.go).
func (a *activeRun) developerModel() string {
	if model := strings.TrimSpace(a.state.DeveloperModel); model != "" {
		return model
	}
	if a.onAnotherBackend() {
		return strings.TrimSpace(a.state.ProviderResolvedModel)
	}
	return a.pipeline.developer().Model
}

// onAnotherBackend reports a run recorded on a backend other than the one the
// developer is configured for now.
func (a *activeRun) onAnotherBackend() bool {
	return a.state.Backend != "" && a.state.Backend != a.pipeline.developer().Backend
}

// developerEffort is the effort level this run's developer invocations ask for,
// read off the run's own record for the reason the model is -- including an
// empty one, which is the agent having named no level when the run was
// reserved. Only a run reserved before the level was recorded asks for the
// developer agent's configured level, settling it on that first reading.
func (a *activeRun) developerEffort() string {
	if a.state.EffortSettled {
		agent := a.pipeline.developer()
		agent.Backend = a.state.Backend
		agent.Effort = a.state.ProviderEffort
		return a.pipeline.Config.InvocationEffort(agent, a.developerModel())
	}
	a.state.EffortSettled = true
	agent := a.pipeline.developer()
	if a.onAnotherBackend() {
		// The configured level belongs to the configured backend.
		agent.Backend = a.state.Backend
		agent.Effort = ""
	}
	return a.pipeline.Config.InvocationEffort(agent, a.developerModel())
}

// attemptDevelopment makes one developer invocation.
//
// It goes through the meter rather than straight at the backend, so that what
// this attempt spends is one line in the cost log however it ends. An attempt
// the provider refused, killed, or answered badly was charged for exactly as one
// that succeeded was, and the reissued invocation after it is charged again.
func (a *activeRun) attemptDevelopment(ctx context.Context, prompt, sessionID string) (backend.RunResult, error) {
	p := a.pipeline
	// Keep the latest completed account until another replaces it. An interrupted
	// continuation has no new account; review labels the saved attempt and content
	// rather than losing the developer's earlier testimony.
	a.outcome.Summary = ""
	if err := p.Store.Save(a.state); err != nil {
		return backend.RunResult{}, fmt.Errorf("save the developer invocation state: %w", err)
	}
	// The backend is the one the run recorded, resolved before anything about
	// the attempt is written: a run whose backend cannot be invoked here is
	// refused with its record, its session included, exactly as it was.
	developerBackend, named, err := p.developerBackendFor(a.state)
	if err != nil {
		return backend.RunResult{}, err
	}
	account := a.account()
	model := a.developerModel()
	a.state.ProviderModel = model
	a.outcome.ProviderModel = model
	effort := a.developerEffort()
	a.state.ProviderEffort = effort
	a.outcome.ProviderEffort = effort
	attribution := a.spendAttribution(domain.RoleDeveloper, a.developmentPhase())
	attribution.Backend = named
	provider := spend.Metered{
		Provider:    developerBackend,
		Log:         p.Spend,
		Attribution: attribution,
		Clock:       p.Clock,
	}
	result, err := provider.Run(ctx, backend.RunRequest{
		RunID:             a.state.RunID,
		Role:              domain.RoleDeveloper,
		WorkingDirectory:  a.worktree.Path,
		RepositoryRoot:    p.Repository,
		Prompt:            prompt,
		SessionID:         sessionID,
		Model:             model,
		Effort:            effort,
		LastSequence:      a.state.LastSequence,
		RedactValues:      p.RedactValues,
		EventSink:         a.sink,
		AfterReplyWaiting: a.recordAfterReplyWaiting,
		AccountAlias:      account.Alias,
		AccountConfigDir:  account.Directory,
		// A routed attempt is started behind its launch gate, so its process is
		// on the run's record before the provider can begin work; nil for an
		// invocation nothing reserved (developerrouting.go).
		LaunchGate: a.launchGate,
	})
	// What became of a session that outlived its reply is this attempt's, and
	// replaces the waiting account the record carried while it lasted. An
	// attempt whose session exited with its reply carries none, and clears the
	// previous attempt's rather than leaving it to be read as this one's.
	a.state.AfterReply = result.Process.AfterReply
	return result, err
}

// recordAfterReplyWaiting records that the developer's session has written its
// final reply and is still running on work it started in the background, so
// every surface reading the run says it is waiting on that work rather than
// that the provider is developing. A record that cannot be saved costs the
// surfaces the sentence and nothing else: the wait goes on, and the attempt's
// own ending is recorded as it returns.
func (a *activeRun) recordAfterReplyWaiting(account execution.AfterReply) {
	a.state.AfterReply = &account
	a.state.UpdatedAt = a.pipeline.clock().Now()
	_ = a.pipeline.Store.Save(a.state)
}

// recordDevelopment records what a served developer attempt reported.
func (a *activeRun) recordDevelopment(ctx context.Context, providerResult backend.RunResult, err error) error {
	p := a.pipeline
	if err != nil {
		// The error says what refused the attempt; whatever the session wrote to
		// standard error before it did is said beside it, for the reason
		// developerFailure says it.
		if last := sessionLastLines(providerResult.Process.Stderr); last != "" {
			err = fmt.Errorf("%w; the last lines the session wrote to standard error were:\n%s", err, last)
		}
		cause := stoppedBy(runstate.StopProvider, fmt.Errorf("developer backend failed: %w", err))
		summaryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		changeSummary, summaryErr := p.Worktrees.SummarizeChanges(summaryCtx, a.worktree)
		cancel()
		if summaryErr != nil {
			cause = errors.Join(cause, fmt.Errorf("summarize changes after developer backend failure: %w", summaryErr))
		} else {
			// The record is left to the terminal save this failure is on its way
			// to: the run is ending, and what it changed before it did is part of
			// what the record has to say about it.
			a.recordChanges(changeSummary)
		}
		return cause
	}
	// An attempt that reports no session — one whose budget ran out before the
	// provider said, or a provider that returned none — keeps the session the run
	// already held rather than erasing it: the record is what every later
	// continuation of this run resumes from (carrySession).
	a.carrySession(providerResult.SessionID, a.state.ProviderSessionID)
	a.state.ProviderResolvedModel = providerResult.ResolvedModel
	a.state.ProviderResolvedEffort = providerResult.ResolvedEffort
	a.state.ProviderEffortReported = providerResult.EffortReported
	a.state.ProviderLoaded = providerResult.Loaded.Recorded()
	a.state.LastSequence = providerResult.LastEvent
	// Whatever stopped the previous attempt is spent: this one ran, and how it
	// ended is recorded below.
	a.state.ProviderStop = ""
	a.state.UpdatedAt = p.clock().Now()
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	a.outcome.ProviderResolvedModel = providerResult.ResolvedModel
	a.outcome.ProviderResolvedEffort = providerResult.ResolvedEffort
	a.outcome.ProviderEffortReported = providerResult.EffortReported
	// The attempt that produced this reply opened with whatever the harness had
	// refused of the developer's own earlier proposals, so those are spent: they
	// are cleared before the reply is read, and anything this reply proposes that
	// is refused takes their place below.
	a.clearCarriedAmendmentRefusals(domain.RoleDeveloper)
	// What the developer claimed its change does to the item is read before the
	// channels that decide nothing, because the contract puts its block ahead of
	// theirs and an unreadable report block takes everything after its own fence
	// with it.
	reply := a.claimLanding(ctx, providerResult.FinalText)
	// What the developer executed is read next, ahead of the channels that decide
	// nothing, because it decides something too: whether this change may be handed
	// to a reviewer at all, and whether this environment can run anything.
	reply = a.claimVerification(providerResult.FinalText, reply)
	// Anything the developer reported is collected out of what it said, so the
	// summary stays the account of the work and the report reaches the operator
	// instead of sitting in prose nothing surfaces.
	account := a.collectFromReply(domain.RoleDeveloper, reply)
	// What is left has to be an account of the work before it is recorded as one.
	// An interim progress line is not: it empties every channel at once and leaves
	// a run that says "completed" with nothing anybody can judge, which is part of
	// what the yoyodyne-ifd.284 false closure rode on — no summary and no landing
	// claim ever existed to judge. So it is not written down as the summary: the
	// caller asks for the account instead, and a run that never gets one ends with
	// the summary honestly empty rather than holding a line about a check somebody
	// never reported on.
	unaccounted, nothingAccounted := accountedForNothing(account, a.state.LandingOutcome, a.state.LandingProblem)
	a.outcome.Summary = ""
	if !nothingAccounted {
		a.outcome.Summary = account
	}
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save developer outcome state: %w", err)
	}
	changeSummary, err := p.Worktrees.SummarizeChanges(ctx, a.worktree)
	if err != nil {
		return fmt.Errorf("summarize developer changes: %w", err)
	}
	a.recordChanges(changeSummary)
	if !providerResult.IsError && strings.TrimSpace(a.outcome.Summary) != "" {
		content, err := p.Worktrees.ContentIdentity(ctx, a.worktree)
		if err != nil {
			return fmt.Errorf("bind the developer summary to its change: %w", err)
		}
		a.state.DeveloperSummary = &runstate.DeveloperSummary{
			Text:    runstate.RecordDeveloperSummary(a.outcome.Summary),
			Content: content,
			Attempt: a.state.RepairAttempts,
		}
	}
	// The account of the change is saved as soon as it is taken rather than with
	// whatever the run does next, because a process that dies here still leaves
	// somebody able to say what the run had changed.
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the account of what the developer changed: %w", err)
	}
	// An environment that could not start the developer's probe at all is the
	// run's ending rather than a problem with the change. Nothing a developer
	// does to its work fixes a sandbox that cannot spawn a process, so this is
	// read before every ending below it: a run that carried on would spend its
	// repair budget, its reviewer, and the rest of its context against a wall
	// that was already named in the first reply.
	//
	// A probe that ran and failed is deliberately not this. It says the
	// environment works and something else is red — the commit the run was cut
	// from, most often — which the checks this run makes for itself will say
	// again with the failure in hand, inside the repair loop that exists for it.
	//
	// It is the same class the harness records when a provider invocation never
	// starts, and it is recorded here because here is the only place that knows
	// the developer itself met it. The round delivered nothing, which the settle
	// confirms against the worktree before it gives anything back.
	if probe, refused := a.probeRefused(); refused {
		detail := fmt.Sprintf("the developer could not start %s in this worktree: %s", probe.Command, probe.Detail)
		a.recordEnvironmentalRefusal(runstate.CauseSandboxSpawnFailure, detail, nothingRan)
		return phaseError{status: runstate.StatusFailed, cause: errors.New(detail)}
	}
	// An escalation ends the run in the round it was raised in, which is the whole
	// of what the verb buys: nothing is published, nothing is checked, nothing is
	// reviewed, and the item goes to the development manager with the developer's
	// account on it.
	//
	// It is read before the invocation's own exit status because it is a claim
	// about the work item rather than about the attempt. The block arrived whole —
	// an unreadable one is recorded as a problem above and is not this — so a
	// developer that said the item cannot be met said so whichever way its process
	// then ended, and asking it to be said again by a resumed run would be spending
	// the run this verb exists to save.
	if a.state.LandingOutcome == runstate.LandingEscalate {
		return escalationRaised{}
	}
	if !providerResult.IsError {
		// An invocation that ended cleanly and said nothing about the work is
		// handed back to the caller as that, rather than as a finished attempt.
		// Only a clean ending reaches this: an attempt the provider failed, or the
		// harness stopped on time, is answered below by what actually ended it, and
		// an interim line is what a stopped invocation is expected to leave behind.
		if nothingAccounted {
			return unaccountedReply{reason: unaccounted}
		}
		return nil
	}
	// A stopped invocation is the harness's own doing, so it is never reported as
	// the developer having said anything. What it produced is already in the
	// worktree and its session is already recorded, so the run is left owed a
	// continuation instead of being failed.
	if reason, stopped := providerStopReason(providerResult.Process.Status); stopped {
		resumable, err := a.recordProviderStop(reason)
		if err != nil {
			return err
		}
		if resumable {
			return providerStop{reason: reason}
		}
		return stoppedBy(runstate.ProviderStopClass(reason), phaseError{
			status: statusForProcess(providerResult.Process.Status),
			cause: fmt.Errorf("the harness stopped the developer: %s, and this run has nothing to continue from",
				describeProviderStop(reason)),
		})
	}
	return stoppedBy(runstate.StopProvider, phaseError{
		status: statusForProcess(providerResult.Process.Status),
		cause:  errors.New("developer reported failure: " + developerFailure(providerResult)),
	})
}

// developerFailure says what ended a developer attempt the provider failed, in
// the words that go onto the run's record, the item's notes and the docket
// entry. A session that dies before it writes a terminal of its own leaves the
// adapter's stand-in reason, "process_exit_1", which names nothing anybody can
// decide from, so the provider's own answer, the exit status, and the last lines
// the session wrote to standard error are each said where there is one, and
// their absence is said where there is none.
func developerFailure(result backend.RunResult) string {
	reason := strings.TrimSpace(result.StopReason)
	if reason == fmt.Sprintf("process_exit_%d", result.Process.ExitCode) || reason == string(result.Process.Status) {
		reason = ""
	}
	var parts []string
	if reason != "" || strings.TrimSpace(result.FinalText) != "" {
		parts = append(parts, "the provider answered: "+backend.DescribeFailure(reason, result.FinalText))
	} else {
		parts = append(parts, "the provider gave no answer of its own")
	}
	if result.Process.Status == execution.ProcessFailed {
		parts = append(parts, fmt.Sprintf("the session exited with status %d", result.Process.ExitCode))
	}
	if last := sessionLastLines(result.Process.Stderr); last != "" {
		parts = append(parts, "the last lines it wrote to standard error were:\n"+last)
	} else {
		parts = append(parts, "it wrote nothing to standard error")
	}
	return strings.Join(parts, "; ")
}

// sessionLastLines is the end of what a session wrote to one stream, bounded so
// it fits beside everything else a failure record carries: the last ten
// non-empty lines, each indented, and at most 2 KiB of them.
func sessionLastLines(stream string) string {
	const maxLines, maxBytes = 10, 2 << 10
	var lines []string
	size := 0
	all := strings.Split(stream, "\n")
	for index := len(all) - 1; index >= 0 && len(lines) < maxLines; index-- {
		line := singleLine(all[index], maxBytes-2)
		if line == "" {
			continue
		}
		line = "  " + line
		if size+len(line) > maxBytes {
			break
		}
		size += len(line) + 1
		lines = append([]string{line}, lines...)
	}
	return strings.Join(lines, "\n")
}

// refusedForUsageLimit reports an attempt the provider declined for want of
// capacity. A limit reported alongside work that still finished is evidence
// rather than a refusal — the provider re-reports its limits whenever they
// change, and almost every such report arrives on a run with capacity to spare —
// so only a report accompanying an attempt that produced no usable result
// pauses the run.
func refusedForUsageLimit(result backend.RunResult, err error) (backend.UsageLimit, bool) {
	if result.UsageLimit == nil || (err == nil && !result.IsError) {
		return backend.UsageLimit{}, false
	}
	return *result.UsageLimit, true
}

// refusedForServerOverload reports an attempt the provider could not serve
// because its own servers were transiently overloaded. An overload is reported
// as the terminal error that ended the invocation rather than beside a result,
// so in practice an attempt carrying one produced nothing to keep; the guard on
// a finished attempt is kept regardless, so no backend can park a run that
// already has its answer.
func refusedForServerOverload(result backend.RunResult, err error) (backend.ServerOverload, bool) {
	if result.ServerOverload == nil || (err == nil && !result.IsError) {
		return backend.ServerOverload{}, false
	}
	return *result.ServerOverload, true
}

// providerStopReason names the harness's reason for stopping a provider
// invocation, and reports whether it stopped one at all. Only these two process
// statuses are the harness acting on time; every other way a process ends is the
// provider's own, including a cancelled one, which is an operator's.
func providerStopReason(status execution.ProcessStatus) (string, bool) {
	switch status {
	case execution.ProcessStalled:
		return runstate.ProviderStopStalled, true
	case execution.ProcessTimedOut:
		return runstate.ProviderStopBudgetExhausted, true
	default:
		return "", false
	}
}

func describeProviderStop(reason string) string {
	switch reason {
	case runstate.ProviderStopStalled:
		return "it produced no output for longer than the harness allows"
	case runstate.ProviderStopBudgetExhausted:
		return "it was still working when its total budget ran out"
	default:
		return "it was stopped on time"
	}
}

// providerStop reports an invocation the harness stopped on time rather than one
// the provider ended. Like a usage-limit pause it is an error only so that it
// travels the path a stopped step already travels; it is deliberately not a
// failure, and the run it leaves behind is still in flight and still resumable.
type providerStop struct {
	reason string
}

func (e providerStop) Error() string {
	return "the harness stopped the provider: " + describeProviderStop(e.reason)
}

// recordProviderStop makes a stop durable and reports whether the run can be
// picked up again from it. A stop the run could not be resumed from is
// deliberately not recorded: a marker nothing can act on would leave the run in
// flight with no way back into it, which is worse than ending it honestly.
func (a *activeRun) recordProviderStop(reason string) (bool, error) {
	a.state.ProviderStop = reason
	if !stoppedProviderIsResumable(a.state) {
		a.state.ProviderStop = ""
		return false, nil
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("record the stopped provider invocation: %w", err)
	}
	return true, nil
}

// stoppedProviderIsResumable reports a run whose provider the harness stopped on
// time and which can be continued from durable state. It needs the worktree the
// stopped invocation was working in and the developer session a continuation
// resumes: a developer attempt continues in that session, and a review re-reads
// the change that session produced, which is also the session integration later
// demands as evidence of two independent invocations.
func stoppedProviderIsResumable(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.ProviderStop == "" {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseReviewing:
	default:
		return false
	}
	if state.ProviderSessionID == "" {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// releaseCheckInterval is how often a waiting run looks for the operator's
// release of its wait. It bounds how long "release this now" takes to take
// effect in a process that is already asleep, so it is short enough to read as
// immediate to the person who typed it; the cost of it is reading one small file
// that usually does not exist.
const releaseCheckInterval = 5 * time.Second

// pauseForUsageLimit records an exhausted limit and waits it out. The reset time
// and the run's remaining pause budget are both checked before anything is
// written, because a wait the harness will not take must stop the run rather
// than become a pause nobody can honor.
func (a *activeRun) pauseForUsageLimit(ctx context.Context, limit backend.UsageLimit) error {
	p := a.pipeline
	a.state.UsageLimitKind = limit.Kind
	a.outcome.UsageLimitKind = limit.Kind
	// Which model was refused is written with the kind, so the park reads back
	// as a refusal of that model wherever the refusals outside a run are read.
	a.state.UsageLimitModel = a.refusedModel()
	a.state.PauseCause = runstate.PauseUsageLimit
	a.outcome.PauseCause = runstate.PauseUsageLimit
	// A limit is the account's state rather than an outage, so a channel left
	// over from an earlier outage pause would describe this one as a refusal it
	// is not. It is cleared for the reason the kind is cleared on an overload.
	a.state.ProviderOutageChannel = ""
	a.outcome.ProviderOutageChannel = ""
	maximum := p.Config.Execution.UsageLimitMaxPause
	spent := a.state.UsageLimitPaused()
	now := p.clock().Now()
	wait := limit.ResetsAt.Sub(now)
	// What a reset time is worth is the provider contract's answer rather than
	// this run's: the two cases that are not simply a time in the future were
	// each learned at the cost of a run, and every provider inherits both rather
	// than rediscovering them. What the harness decides is what each one earns,
	// which is here, because a wait spends an account.
	reset := backend.ReadReset(limit.ResetsAt, now)
	// A limit with no reset time is unknown rather than unwaitable. The overage
	// allowance reports this way while the ordinary rolling window keeps
	// resetting on its usual schedule, so the work resumes -- the harness simply
	// has to ask again rather than be told when. It waits a configured interval
	// and reattempts, and because that wait spends the same budget as any other,
	// a provider that keeps refusing walks into the maximum instead of polling
	// forever.
	unknownReset := reset == backend.ResetUnknown
	if unknownReset {
		wait = p.Config.Execution.UsageLimitUnknownResetPause.Duration()
		limit.ResetsAt = p.clock().Now().Add(wait)
	}
	switch {
	case reset == backend.ResetMalformed:
		// A limit still refusing work while naming a reset that has already
		// passed is not describing a wait. Honoring it would mean reissuing
		// immediately into the same refusal, over and over, with nothing bounding
		// the attempts; a clock skew or a window the provider has not rolled yet
		// is a fact for a person, not something to spin on.
		return a.blockOnUsageLimit(fmt.Sprintf("it reports resetting at %s, which is not in the future",
			limit.ResetsAt.UTC().Format(time.RFC3339)))
	case wait > maximum.Duration()-spent:
		// The budget covers the run, not one pause. Checking each wait on its own
		// would let a provider that keeps refusing walk a run far past the
		// maximum an operator configured, one acceptable-looking wait at a time.
		reason := fmt.Sprintf("waiting until %s would take this run past the %s maximum pause",
			limit.ResetsAt.UTC().Format(time.RFC3339), maximum)
		if unknownReset {
			reason = fmt.Sprintf("it named no reset time, and waiting %s to ask again would take this run past the %s maximum pause",
				p.Config.Execution.UsageLimitUnknownResetPause, maximum)
		}
		if spent > 0 {
			reason += fmt.Sprintf(", and it has already committed %s to waiting", spent)
		}
		return a.stopOnUsageWindow(reason, limit.ResetsAt, unknownReset)
	}
	// The deadline becomes durable before the wait starts, so a process that dies
	// during the wait honors the same deadline on restart rather than retrying
	// straight back into the same limit. What the wait spends is recorded as it is
	// spent, one probe at a time, by the wait itself.
	resetsAt := limit.ResetsAt.UTC()
	a.state.UsageLimitResetsAt = &resetsAt
	// Whether that deadline is the provider's or the harness's own probe goes
	// beside it, because a reader saying when the wait lifts cannot tell the two
	// apart from the time alone.
	a.state.UsageLimitResetUnknown = unknownReset
	a.state.UpdatedAt = p.clock().Now()
	// When this pause was recorded is what tells a release meant for it apart
	// from one meant for a pause this run has already served and reissued past.
	a.pausedAt = p.clock().Now()
	a.recordPauseStart()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record usage limit pause: %w", err)
	}
	return a.awaitRecordedUsageLimit(ctx)
}

// refusedModel is the model selector the invocation this run is parked on asked
// for: the reviewer's during a review, and the developer's otherwise, which are
// the two invocations a run pauses for. Each is read where that attempt read it
// — the reviewer's from the configuration, the developer's off this run's own
// record — so it is the selector that attempt actually requested.
func (a *activeRun) refusedModel() string {
	if a.state.Phase == runstate.PhaseReviewing {
		return a.pipeline.reviewer().Model
	}
	return a.developerModel()
}

// recordPauseStart writes when the pause being recorded began, once per pause:
// the moment its deadline is first written, which every probe after that leaves
// alone. It is the durable copy of pausedAt, kept so a process that picks the
// run up mid-wait, and every reader of the record, can say how long the run
// has been parked rather than only when it last probed.
func (a *activeRun) recordPauseStart() {
	since := a.pausedAt.UTC()
	a.state.UsageLimitPausedSince = &since
}

// pauseForServerOverload records a transiently overloaded provider and waits it
// out. It is the usage-limit pause with a different clock and nothing else: an
// overload quotes no reset time and lifts in seconds rather than hours, so the
// run sets its own short deadline instead of honoring one, and everything after
// that is shared. The deadline is durable before the wait starts, the wait
// spends the same aggregate budget, and a provider that stays overloaded walks
// into the same configured maximum rather than reissuing forever.
//
// The provider CLI has already spent its own retries on this condition before it
// reports it, so the overload the harness sees is one that outlasted them.
func (a *activeRun) pauseForServerOverload(ctx context.Context, overload backend.ServerOverload) error {
	p := a.pipeline
	// An overload is the provider's own state rather than the account's, so
	// nothing names a limit here. The kind is cleared for that reason: a limit
	// left over from an earlier pause would describe this one as an exhaustion it
	// is not.
	a.state.UsageLimitKind = ""
	a.outcome.UsageLimitKind = ""
	a.state.ProviderOutageChannel = ""
	a.outcome.ProviderOutageChannel = ""
	a.state.PauseCause = runstate.PauseServerOverload
	a.outcome.PauseCause = runstate.PauseServerOverload
	maximum := p.Config.Execution.UsageLimitMaxPause
	spent := a.state.UsageLimitPaused()
	wait := p.Config.Execution.ServerOverloadPause.Duration()
	if wait > maximum.Duration()-spent {
		// The budget covers the run rather than one pause, exactly as it does for a
		// limit. A provider that keeps refusing therefore reaches the maximum an
		// operator configured instead of reissuing a short attempt forever.
		reason := fmt.Sprintf("waiting %s to ask again would take this run past the %s maximum pause",
			p.Config.Execution.ServerOverloadPause, maximum)
		if spent > 0 {
			reason += fmt.Sprintf(", and it has already committed %s to waiting", spent)
		}
		return a.blockOnUsageLimit(reason)
	}
	resetsAt := p.clock().Now().Add(wait).UTC()
	a.state.UsageLimitResetsAt = &resetsAt
	// The deadline is the harness's own, and the record says so: an overload
	// names no model and no reset, and the model left over from a limit would
	// describe this wait as a refusal of it.
	a.state.UsageLimitResetUnknown = true
	a.state.UsageLimitModel = ""
	a.state.UpdatedAt = p.clock().Now()
	a.pausedAt = p.clock().Now()
	a.recordPauseStart()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record server overload pause: %w", err)
	}
	return a.awaitRecordedUsageLimit(ctx)
}

// awaitRecordedUsageLimit serves the deadline already in durable state. It is
// the whole of a resumed pause and the tail of a fresh one, so a restart during
// a wait takes exactly the path the interrupted process was on.
//
// The deadline is an upper bound on the wait rather than a gate on it. A run
// sleeps the shorter of the configured probe interval and the time left, and
// then reissues the attempt — the reissue *is* the probe. A reset time is a
// claim about the provider, and claims go stale in both directions: capacity
// gets bought mid-wait, and a rolling window can free room before the quoted
// edge. A probe into a window that is still closed costs one refused request and
// re-parks on whatever the provider now reports, which is the same price a wrong
// release costs. This is also what unifies the two cases: a limit that named no
// reset time already polled at this interval, and one that named a distant reset
// now polls at it too, under one discipline rather than two. A server overload
// falls out of the same rule without a case of its own: the deadline it sets is
// already shorter than the probe interval, so the shorter of the two is the
// whole of its wait.
func (a *activeRun) awaitRecordedUsageLimit(ctx context.Context) error {
	p := a.pipeline
	// A provider answering nobody shares the deadline field and the resume path
	// and none of the budgets: the wait below charges every probe to the pause
	// budget and blocks once it is spent, and an outage spends nothing.
	if _, away := runstate.PausedForProviderOutage(a.state.PauseCause); away {
		return a.awaitProviderOutage(ctx)
	}
	deadline := a.state.UsageLimitResetsAt.UTC()
	a.outcome.UsageLimitKind = a.state.UsageLimitKind
	a.outcome.PauseCause = a.state.PauseCause
	a.outcome.UsageLimitResetsAt = &deadline
	// A committed wait that no longer fits the bound — because the bound was
	// lowered, or because a differently configured process wrote it — is refused
	// here for the same reason it would have been refused on arrival. It is asked
	// before the release is, because the release says the deadline went stale and
	// says nothing about a run that has already spent everything it was allowed to
	// spend waiting.
	if a.state.UsageLimitPaused() > p.Config.Execution.UsageLimitMaxPause.Duration() {
		reason := fmt.Sprintf("this run has committed %s to waiting, which is past the %s maximum pause",
			a.state.UsageLimitPaused(), p.Config.Execution.UsageLimitMaxPause)
		// A usage limit is a window with an end, and ends the run the way any wait
		// past the bound on one does. An overload has no window to wait for, so it
		// stops the way it always has.
		if capacityWindow(a.state.PauseCause) {
			return a.stopOnUsageWindow(reason, deadline, a.state.UsageLimitResetUnknown)
		}
		return a.blockOnUsageLimit(reason)
	}
	// The operator may have released this wait while no process was serving it,
	// so it is asked before anything is decided about sleeping or exiting.
	released, err := a.releasedByOperator()
	if err != nil {
		return err
	}
	remaining := deadline.Sub(p.clock().Now())
	probe := min(remaining, p.Config.Execution.UsageLimitUnknownResetPause.Duration())
	switch {
	case released:
		// The next probe is now. Nothing else in the run reaches this: the
		// deadline binds every other path exactly as strictly as it did before.
	case remaining <= 0:
		// The recorded deadline has passed, so the wait this run committed to is
		// served and the refused attempt is owed its reissue. A deadline already
		// behind us is the normal way a paused run is resumed, and is a different
		// thing from a fresh report naming a reset in the past.
	case a.inProcessWait+probe > p.Config.Execution.UsageLimitInProcessPause.Duration():
		// This process has stayed open for this run as long as it is allowed to.
		// The bound counts every probe this process has already slept rather than
		// this one on its own, because a bound applied per probe would not bound
		// anything: an hour's worth of it would hold a process open for a whole
		// six-hour deadline, half an hour at a time. The deadline is durable, so
		// the run is left in flight for a later invocation to continue.
		return usageLimitPause{cause: a.state.PauseCause, kind: a.state.UsageLimitKind, resetsAt: deadline}
	default:
		// What this probe will spend is committed before it is spent, so a process
		// that dies mid-sleep cannot buy the run a fresh budget by forgetting it.
		// Only this probe is committed, not the whole span to the deadline: the
		// budget has to describe what was actually waited.
		wakesAt := p.clock().Now().Add(probe)
		a.state.UsageLimitPausedSeconds += int64(probe / time.Second)
		a.state.UpdatedAt = p.clock().Now()
		if err := p.Store.Save(a.state); err != nil {
			return fmt.Errorf("record usage limit pause: %w", err)
		}
		if err := a.waitForProbe(ctx, wakesAt); err != nil {
			return err
		}
		// A release that landed mid-probe leaves time on the clock that was never
		// waited. The budget must not be charged for it, and it is not time this
		// process stayed open for either.
		waited := probe
		if unspent := wakesAt.Sub(p.clock().Now()); unspent > 0 {
			waited -= unspent
			a.state.UsageLimitPausedSeconds -= int64(unspent / time.Second)
		}
		a.inProcessWait += waited
	}
	return a.clearUsageLimitPause()
}

// waitForProbe sleeps until the next probe is due, in slices short enough that
// an operator releasing the wait is acted on while this process is still asleep.
// Waking on a release is the whole reason the sleep is sliced rather than taken
// in one piece: a run that only looked at the release record between probes
// would make "release this now" mean "release this within half an hour".
func (a *activeRun) waitForProbe(ctx context.Context, wakesAt time.Time) error {
	p := a.pipeline
	for {
		remaining := wakesAt.Sub(p.clock().Now())
		if remaining <= 0 {
			return nil
		}
		if err := p.sleep(ctx, min(remaining, releaseCheckInterval)); err != nil {
			return err
		}
		released, err := a.releasedByOperator()
		if err != nil {
			return err
		}
		if released {
			return nil
		}
	}
}

// releasedByOperator reports whether the operator has said this run's recorded
// deadline no longer describes the provider. A record that cannot be read fails
// the wait rather than being treated as an absence: an operator who released a
// run and was silently ignored would be back where this verb exists to get them
// out of, and the run is preserved and resumable either way.
//
// A release older than the pause being served belongs to a pause this process
// has already served and reissued past. The operator reads the waiting run
// without holding its lease, so a release they typed against the pause they saw
// can land just after this run cleared it — and honoring it would release a
// pause the provider reported afterwards, which nobody has said anything about.
// A release recorded while no process was serving the run has no such pause to
// be older than, and is honored.
func (a *activeRun) releasedByOperator() (bool, error) {
	released, found, err := a.pipeline.Store.ReleasedWait(a.state.RunID)
	if err != nil {
		return false, fmt.Errorf("read whether the operator released this usage limit wait: %w", err)
	}
	if !found {
		return false, nil
	}
	return !released.ReleasedAt.Before(a.pausedAt), nil
}

// clearUsageLimitPause records that the run is no longer waiting, before the
// attempt it was waiting for is reissued. A deadline left behind would make a
// running attempt look like a pause to the next process that adopts the run.
//
// The operator's release is consumed here for the same reason and at the same
// moment: it has been acted on, and a record left behind would release whatever
// pause the reissued attempt earns next — a pause nobody has said anything about
// yet.
func (a *activeRun) clearUsageLimitPause() error {
	if a.state.UsageLimitResetsAt == nil {
		return nil
	}
	if err := a.pipeline.Store.ClearRelease(a.state.RunID); err != nil {
		return err
	}
	a.state.UsageLimitResetsAt = nil
	// The cause goes with the deadline it described, and so do the start of the
	// pause and what kind of deadline it was. What refused the run is kept on
	// the outcome for the record, but a cause left in durable state beside no
	// deadline would describe a wait this run is no longer taking.
	a.state.PauseCause = ""
	a.state.UsageLimitPausedSince = nil
	a.state.UsageLimitResetUnknown = false
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear usage limit pause: %w", err)
	}
	a.outcome.UsageLimitResetsAt = nil
	return nil
}

// blockOnUsageLimit ends a run the provider refused and whose wait the harness
// will not take: an unusable reset time, or a server overload that outlasted the
// configured maximum. A usage limit whose reset merely lies past the maximum is
// not one of these; it ends in stopOnUsageWindow, because nothing about it is a
// person's to decide. Guessing a wait is the one thing that must not happen
// here, so what stopped the run is recorded on the work item and a person
// decides what to do about it. It serves both refusals, because what is undecidable about them is
// the same thing — how long to wait — whichever one asked.
func (a *activeRun) blockOnUsageLimit(reason string) error {
	cause := fmt.Errorf("this run was refused by %s and cannot wait for it: %s",
		runstate.DescribePause(a.state.PauseCause, a.state.UsageLimitKind), reason)
	if err := a.block(renderUsageLimitBlockerNotes(a.outcome, reason)); err != nil {
		return stoppedBy(runstate.StopUsagePause, withFailedRecord(cause, fmt.Errorf("record the provider's refusal as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopUsagePause, cause)
}

// keptPauseCause is the pause cause a terminal record keeps. Every pause is
// cleared as a run ends, because a cause is an instruction to resume, except
// the capacity cause of a run blockOnUsageLimit stopped: there it is the only
// record of which refusal stopped the run — an overload clears the limit kind
// — and the read model lists the run as capacity-blocked by it. A terminal
// record carrying a cause is no instruction to anything, since everything that
// resumes a pause asks first whether the run is still in flight.
func keptPauseCause(cause string, class runstate.StopClass) string {
	if class != runstate.StopUsagePause {
		return ""
	}
	switch cause {
	case runstate.PauseUsageLimit, runstate.PauseServerOverload:
		return cause
	}
	return ""
}

// capacityWindow reports a pause cause that is an exhausted usage limit: a
// window on the provider's clock with an end the harness can name. The empty
// cause is one, because every deadline written before the cause was carried was
// a usage limit's.
func capacityWindow(cause string) bool {
	return cause == runstate.PauseUsageLimit || cause == ""
}

// stopOnUsageWindow ends a run the provider's usage window refused, where the
// reset lies past what the run may still wait. It is the counterpart of
// blockOnUsageLimit for the one refusal whose wait is perfectly well defined and
// merely longer than the harness will take: nothing about it is a person's to
// decide, so nothing is blocked. What stopped the run is recorded as an
// environmental refusal naming the reset, which is what keeps the stop off the
// failure-storm brake, gives back what the round was charged, and tells the
// watch session when to pull the item again. The run itself ends in stop, where
// the claim is given back.
func (a *activeRun) stopOnUsageWindow(reason string, resetsAt time.Time, resetUnknown bool) error {
	refused := fmt.Sprintf("this run was refused by %s and the harness will not wait for it: %s",
		runstate.DescribePause(a.state.PauseCause, a.state.UsageLimitKind), reason)
	a.recordEnvironmentalRefusal(runstate.CauseUsageWindow, refused, ranAnyway)
	reset := resetsAt.UTC()
	a.state.Environmental.ResetsAt = &reset
	a.state.Environmental.ResetUnknown = resetUnknown
	return usageWindowStop{reason: refused}
}

// usageWindowStop is a run the provider's usage window refused for longer than
// the harness will wait. It is an error only so that it travels the path every
// ending travels; it is deliberately not a failure, and stop ends the run on it
// as cancelled with its claim given back.
type usageWindowStop struct{ reason string }

func (e usageWindowStop) Error() string { return e.reason }

// endOnUsageWindow ends a run stopped by the provider's usage window. It is
// recorded cancelled rather than failed, in the read model's own vocabulary for
// an ending nothing judged, and it leaves nobody a decision: no blocker is
// written, the branch and worktree are left exactly as a stopped run's are, and
// the claim is given back so the item is ready to be pulled again once the
// window resets. The environmental refusal recorded where the wait was refused
// is settled by fail, which is what returns the round and the grant.
//
// A claim that could not be given back is reported rather than swallowed. The
// run is terminal either way, so the claim audit gives it back on its next pass;
// until then the item reads as claimed, which is worth an operator knowing.
func (a *activeRun) endOnUsageWindow(stopped usageWindowStop) (Outcome, error) {
	outcome, err := a.fail(stopped, runstate.StatusCancelled)
	// fail hands back the stop itself, joined with anything that went wrong
	// recording it, and only the second is a problem.
	var problems []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if !errors.Is(part, stopped) {
				problems = append(problems, part)
			}
		}
	} else if err != nil && !errors.Is(err, stopped) {
		problems = append(problems, err)
	}
	if a.claimed {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, releaseErr := a.pipeline.Tracker.Release(releaseCtx, a.state.WorkItemID, renderUsageWindowReleaseNotes(outcome))
		cancel()
		if releaseErr != nil {
			problems = append(problems, fmt.Errorf("give back the claim on %s after the provider's usage limit stopped run %s; the claim audit gives it back once it finds the run ended: %w",
				a.state.WorkItemID, a.state.RunID, releaseErr))
		}
	}
	return outcome, errors.Join(problems...)
}

// usageLimitPause reports a run that stopped short of finishing because it is
// waiting out a provider that refused it for longer than this process will stay
// open. It is an error only so that it travels the path a stopped step already
// travels; it is deliberately not a failure, and the run it leaves behind is
// still in flight and still resumable.
type usageLimitPause struct {
	cause    string
	kind     string
	resetsAt time.Time
}

func (e usageLimitPause) Error() string {
	return fmt.Sprintf("paused until %s for %s",
		e.resetsAt.Format(time.RFC3339), runstate.DescribePause(e.cause, e.kind))
}

// pausedForUsageLimit reports a run waiting out an exhausted provider usage
// limit. The recorded deadline is what makes it a pause rather than an
// interruption, and the worktree is what makes it resumable: without the change
// every attempt shares there is nothing to continue. A developer session is
// deliberately not required, because a limit can refuse the very first attempt
// before the provider ever established one, and neither is a spent repair
// attempt, because a run can pause before any failure was returned to it.
//
// Both provider-invoking phases can pause. A developer attempt resumes by being
// reissued; a review resumes by re-verifying and reviewing again, which is what
// an interrupted review already does.
func pausedForUsageLimit(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.UsageLimitResetsAt == nil {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseReviewing:
	default:
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// pausingDirectives asks what the operator has directed that stops this work. A
// failure to read is a failure to run: a harness that cannot find out what has
// been directed is indistinguishable from one that has been directed nothing,
// and proceeding on that reading is the whole failure directives exist to
// prevent.
func (p Pipeline) pausingDirectives(workItemID string) ([]directive.Directive, error) {
	pausing, err := p.Directives.Pausing(workItemID)
	if err != nil {
		return nil, fmt.Errorf("read what the operator has directed about %s: %w", workItemID, err)
	}
	return pausing, nil
}

// pauseWorkItem reports work the harness declined to start or resume because a
// directive is unresolved. There is no run behind it: nothing was claimed, no
// worktree exists, and a run already in flight for the item was left exactly as
// it was. It is a pause rather than a failure because settling the directive is
// all that stands between here and the work proceeding.
func pauseWorkItem(workItemID string, paused directive.Directive) Outcome {
	return Outcome{
		WorkItemID:        workItemID,
		Paused:            true,
		PausedByDirective: &paused,
	}
}

// holdForDirective stops a run in flight for an unresolved directive, and does
// nothing at all when there is none, which is the ordinary case. What it returns
// on the pausing path is a directivePause, which travels the path a stopped step
// already travels.
//
// This is what makes a directive reach work that is already under way. The check
// before a run starts covers work that has not begun; without this one, a
// directive recorded while a developer was working would be enforced against
// every item except the one it was about.
func (a *activeRun) holdForDirective() error {
	pausing, err := a.pipeline.pausingDirectives(a.state.WorkItemID)
	if err != nil {
		return err
	}
	if len(pausing) == 0 {
		return nil
	}
	return a.recordDirectivePause(pausing[0])
}

// recordDirectivePause makes the pause durable and then reports it. The record
// comes first for the same reason a usage-limit deadline is written before the
// wait begins: a process that dies here must leave a run that can be told from
// an interrupted one and picked up again, rather than one nothing will resume.
func (a *activeRun) recordDirectivePause(paused directive.Directive) error {
	// The developer's attempt is behind this run and the gate is what it stopped
	// short of, so the recorded phase says so. Left at developing, a resumed run
	// would be handed a second developer attempt it does not need.
	if a.state.Phase == runstate.PhaseDeveloping {
		a.state.Phase = runstate.PhaseChecking
	}
	a.state.DirectivePause = &runstate.DirectivePause{
		DirectiveID: paused.ID,
		Kind:        string(paused.Kind),
		Unresolved:  paused.Unresolved,
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the directive that paused this run: %w", err)
	}
	return directivePause{directive: paused}
}

// clearDirectivePause records that the run is no longer held up, before it
// carries on. A pause left behind would make a running attempt look like a
// waiting one to the next process that adopts the run.
func (a *activeRun) clearDirectivePause() error {
	if a.state.DirectivePause == nil {
		return nil
	}
	a.state.DirectivePause = nil
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the directive pause: %w", err)
	}
	return nil
}

// directivePause reports a run that stopped short of finishing because an
// unresolved user directive affects its work item. Like a usage-limit pause it
// is an error only so that it travels the path a stopped step already travels;
// it is deliberately not a failure, and the run it leaves behind is still in
// flight, still claimed, and still resumable.
type directivePause struct {
	directive directive.Directive
}

func (e directivePause) Error() string {
	return "paused for an unresolved directive: " + e.directive.Summary()
}

// pausedForDirective reports a run held up by an unresolved user directive. The
// recorded pause is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForDirective(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.DirectivePause == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// pauseWorkItemForDependencies reports work the harness declined to start or
// resume because the item waits on work that is not finished. There is no run
// behind it: nothing was claimed, no worktree exists, and a run already in flight
// for the item was left exactly as it was. It is a pause rather than a failure
// because closing what it waits on is all that stands between here and the work
// proceeding.
func pauseWorkItemForDependencies(workItemID string, blockers []string) Outcome {
	paused := runstate.DependencyPause{Blockers: blockers}
	return Outcome{
		WorkItemID:         workItemID,
		Paused:             true,
		PausedByDependency: &paused,
	}
}

// holdForDependency stops a run in flight for work its item has since been made
// to wait on, and does nothing at all when there is none, which is the ordinary
// case. What it returns on the pausing path is a dependencyPause, which travels
// the path a stopped step already travels.
//
// The item is re-read from the tracker rather than taken from the run, and that
// is the whole of what this adds. The item a run carries is what selection read
// when the run started; a dependency link applied since then is precisely the
// gate that would otherwise stop nothing, and a run answering from its own copy
// would be the run this exists to end.
//
// A failure to read is a failure to proceed, exactly as an unreadable directive
// is: a harness that cannot find out what an item waits on is indistinguishable
// from one whose item waits on nothing, and spending another attempt on that
// reading is the whole failure this exists to prevent.
//
// What "a failure to read" means is the part that had to change. The store is a
// local database other processes write to, so a `bd show` killed under load is
// the same class of non-answer a reset connection is — and until
// yoyodyne-ifd.428.6 this boundary ran under a flat deadline and ended the run
// on the first one. Three runs died that way in two days, two of them holding
// finished work: an approved change stopped at its promotion, and a lifted
// worktree stopped at the start of a round. So the read is waited out on the
// same Fibonacci window every other recoverable boundary gets, and only a read
// that spends the whole of it stops the run — as a park rather than a failure,
// because a store that was busy for two hours says nothing about the change.
func (a *activeRun) holdForDependency(ctx context.Context) error {
	var item beads.WorkItem
	err := a.recovering(ctx, runstate.RetryDependencyRead, func(ctx context.Context) error {
		var readErr error
		item, readErr = a.pipeline.Tracker.Show(ctx, a.state.WorkItemID)
		return readErr
	})
	if err != nil {
		// A failure that is still recoverable-classed after the window is the
		// window having run out rather than an answer: recovering returns anything
		// else exactly as the read produced it. The run parks instead of failing,
		// keeping its claim, its branch, its worktree, and its developer session.
		if recovery.Recoverable(err) {
			return a.recordTrackerPause(runstate.RetryDependencyRead, err)
		}
		return fmt.Errorf("read what %s waits on: %w", a.state.WorkItemID, err)
	}
	blockers := blockingDependencies(item)
	if len(blockers) == 0 {
		return nil
	}
	return a.recordDependencyPause(blockers)
}

// recordTrackerPause makes the park durable and then reports it, for the reason
// recordDependencyPause does: a process that dies here must leave a run that can
// be told from an interrupted one and picked up again.
//
// The phase is left exactly where the run reached, unlike the dependency pause
// beside it. That pause is taken having decided the work must not proceed, so it
// moves a developer's phase on; this is taken having decided nothing at all, and
// a resumed run has to re-ask the question this one could not get an answer to.
func (a *activeRun) recordTrackerPause(boundary string, cause error) error {
	paused := runstate.TrackerPause{
		Boundary:      boundary,
		Attempts:      a.state.RetryAttempts(boundary),
		WaitedSeconds: int64(a.state.RetryWaited(boundary) / time.Second),
		Failure:       boundedFailureDetail(cause.Error()),
	}
	a.state.TrackerPause = &paused
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the unanswered tracker read that parked this run: %w", err)
	}
	return trackerPause{paused: paused}
}

// clearTrackerPause records that the run is no longer waiting on the store,
// before it carries on. A park left behind would make a running attempt look
// like a waiting one to the next process that adopts the run.
//
// It clears the window with it. The park is the end of one boundary's window,
// and a resumed run that found it already spent would park again on its first
// read without ever asking twice — so a resume that got as far as re-entering
// the gate is given the window back, which is the same rule every other spent
// window follows: it bounds one stretch of asking rather than the run.
func (a *activeRun) clearTrackerPause() error {
	if a.state.TrackerPause == nil {
		return nil
	}
	boundary := a.state.TrackerPause.Boundary
	a.state.TrackerPause = nil
	a.state.Retries = slices.DeleteFunc(a.state.Retries, func(retry runstate.Retry) bool {
		return retry.Boundary == boundary
	})
	a.outcome.Retries = a.state.Retries
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the tracker pause: %w", err)
	}
	return nil
}

// trackerPause reports a run that stopped short of finishing because the tracker
// did not answer a read it makes at a gate boundary. Like the directive and
// dependency pauses it is an error only so that it travels the path a stopped
// step already travels; it is deliberately not a failure, and the run it leaves
// behind is still in flight, still claimed, and still resumable.
type trackerPause struct {
	paused runstate.TrackerPause
}

func (e trackerPause) Error() string {
	return "parked for a tracker read that went unanswered: " + e.paused.Summary()
}

// pausedForTracker reports a run parked on an unanswered tracker read. The
// recorded park is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForTracker(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.TrackerPause == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// recordDependencyPause makes the pause durable and then reports it, for the
// reason recordDirectivePause does: a process that dies here must leave a run
// that can be told from an interrupted one and picked up again.
//
// Recording it is also what gives the run's developer slot back: a run carrying
// a dependency pause holds none (runstate.State.HoldsDeveloperSlot), so the next
// pull can fill the slot while this run waits with no process behind it.
func (a *activeRun) recordDependencyPause(blockers []string) error {
	// The developer's attempt is behind this run and the gate is what it stopped
	// short of, so the recorded phase says so. Left at developing, a resumed run
	// would be handed a second developer attempt it does not need.
	if a.state.Phase == runstate.PhaseDeveloping {
		a.state.Phase = runstate.PhaseChecking
	}
	paused := runstate.DependencyPause{Blockers: blockers}
	a.state.DependencyPause = &paused
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the unfinished work that paused this run: %w", err)
	}
	return dependencyPause{blockers: blockers}
}

// clearDependencyPause records that the run is no longer waiting, before it
// carries on. A pause left behind would make a running attempt look like a
// waiting one to the next process that adopts the run.
func (a *activeRun) clearDependencyPause() error {
	if a.state.DependencyPause == nil {
		return nil
	}
	a.state.DependencyPause = nil
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the dependency pause: %w", err)
	}
	return nil
}

// dependencyPause reports a run that stopped short of finishing because its work
// item waits on work that is not finished. Like the directive pause it is an
// error only so that it travels the path a stopped step already travels; it is
// deliberately not a failure, and the run it leaves behind is still in flight,
// still claimed, and still resumable.
type dependencyPause struct {
	blockers []string
}

func (e dependencyPause) Error() string {
	return "paused for unfinished work this item waits on: " + strings.Join(e.blockers, ", ")
}

// pausedForDependency reports a run held up by work its item waits on. The
// recorded pause is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForDependency(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.DependencyPause == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// operatorHoldProbe is how often a held run looks for the operator lifting the
// hold. It bounds how long `yoyo resume` takes to be acted on by a process that
// is already parked, so it is short enough to read as immediate to the person
// who typed it; what it costs is reading one small file that usually is not
// there, and never a request to the provider. It is the interval a released wait
// is noticed on, for the same reason and at the same price.
const operatorHoldProbe = releaseCheckInterval

// operatorHold asks whether the operator has paused all harness activity. A
// failure to read is a failure to proceed, for the reason a directive that
// cannot be read is: a harness that cannot find out whether it has been paused
// is indistinguishable from one that has not been, and spending on that reading
// is the whole failure this exists to prevent.
func (p Pipeline) operatorHold() (runstate.OperatorHold, bool, error) {
	hold, held, err := p.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

// holdWorkItem reports work the harness declined to start or resume because the
// operator is holding all activity. It is a pause rather than a failure because
// lifting the hold is all that stands between here and the work proceeding.
//
// It names the run this item already has, when it has one. That run was left
// exactly as it was — nothing here claims, adopts, or touches it — but "left
// alone" and "never started" are opposite facts about an operator's worktree and
// their claimed item, and a report that could not tell them apart would say
// nothing was started for a run that is parked with hours of work in it.
//
// The run is found by reading rather than by adopting, exactly as releasing a
// wait finds one: a run another process is serving must keep serving it, and
// taking its lease to describe it would be the harness stopping the very run the
// pause exists to preserve. A lookup that fails travels back with the pause
// rather than replacing it — the hold is in force either way and nothing will be
// spent — so what is lost is the description and not the answer.
func (p Pipeline) holdWorkItem(workItemID string, hold runstate.OperatorHold) (Outcome, error) {
	outcome := Outcome{
		WorkItemID:       workItemID,
		Paused:           true,
		PauseCause:       runstate.PauseOperatorHold,
		PausedByOperator: &hold,
	}
	inFlight, err := p.Store.Incomplete()
	if err != nil {
		return outcome, fmt.Errorf("find what is in flight for %s while activity is paused: %w", workItemID, err)
	}
	for _, existing := range inFlight {
		if existing.WorkItemID != workItemID {
			continue
		}
		outcome.RunID = existing.RunID
		outcome.Status = existing.Status
		outcome.Phase = existing.Phase
		outcome.Branch = existing.Branch
		outcome.WorktreePath = existing.WorktreePath
		outcome.BaseCommit = existing.BaseCommit
		outcome.ProviderSessionID = existing.ProviderSessionID
		break
	}
	return outcome, nil
}

// holdIntake reports work this pipeline declined to start because the operator
// is holding intake, and reports nothing at all in the ordinary case. It is the
// whole of what holding intake means: nothing is claimed, nothing is developed,
// and the item stays exactly where it was, so lifting the hold is all that
// stands between here and the work starting.
//
// A hold never stops the operator. They are the one who placed it, and an item
// they then name is them deciding that this piece of work is the exception —
// which is the distinction between holding what the harness chooses and pausing
// everything, and the reason both switches exist.
//
// It stops one harness selection short of every other: the brake's own probe,
// and only where the hold's own record names this item as the probe it has in
// flight. The selection saying so is not enough — the record is what is read,
// because the brake writes it under its lock and a selection is words any
// caller could supply — and a hold that is not the brake's lets nothing
// through however the selection is named.
func (p Pipeline) holdIntake(workItemID string) (Outcome, bool, error) {
	if !p.Selection.SelectedByHarness() {
		return Outcome{}, false, nil
	}
	hold, held, err := p.Intake.Held()
	if err != nil {
		return Outcome{}, false, fmt.Errorf("read whether intake is held: %w", err)
	}
	if !held {
		return Outcome{}, false, nil
	}
	if strings.TrimSpace(p.Selection.By) == runstate.SelectedByBrake && hold.Probing(workItemID) {
		return Outcome{}, false, nil
	}
	return Outcome{
		WorkItemID:     workItemID,
		Paused:         true,
		PausedByIntake: &hold,
	}, true, nil
}

// stopRequested reports the operator asking this run to stop, and nothing at all
// in the ordinary case. It is asked at the boundaries where the operator's hold
// is asked, and for the same reason: those are the points at which a run is about
// to spend, so they are the points at which stopping it costs the least.
//
// A record that cannot be read stops nothing and fails nothing. It is deliberately
// the opposite of how an unreadable hold is treated, because the two protect
// against opposite mistakes: an unreadable hold might be a pause being spent
// through, while an unreadable stop request would be a run killed on the strength
// of a file nobody could parse. The failure travels back with the run instead.
func (a *activeRun) stopRequested() error {
	request, requested, err := a.pipeline.Store.StopRequested(a.state.RunID)
	if err != nil {
		return fmt.Errorf("read whether the operator asked this run to stop: %w", err)
	}
	if !requested {
		return nil
	}
	return operatorStop{request: request}
}

// operatorStop reports a run the operator asked to stop. Unlike the pauses it is
// a real ending: the run is made terminal and cancelled, its artifacts are left
// exactly where they are, and settling what they amount to is reconciliation's
// job rather than this one's — which is what stopping has always done here, and
// is why a stop aimed at another process's run leaves the same thing behind as a
// stop aimed at this one's.
type operatorStop struct {
	request runstate.StopRequest
}

func (e operatorStop) Error() string {
	stopped := e.request.StoppedBy() + " stopped this run at " + e.request.RequestedAt.Format(time.RFC3339)
	if strings.TrimSpace(e.request.Reason) != "" {
		stopped += ": " + strings.TrimSpace(e.request.Reason)
	}
	return stopped
}

// holdForOperator parks a run at a provider-call boundary for as long as the
// operator holds harness activity, and does nothing at all when they do not,
// which is the ordinary case. It is the whole of what "pause" means to a run:
// every provider invocation a run makes passes through here first, so a hold
// placed while a developer was working reaches that run at its next attempt
// rather than only reaching the runs that had not started.
//
// What it deliberately does not do is interrupt an invocation already under way.
// A generation that is streaming has already been paid for, and stopping it
// mid-flight would throw that away and leave the run needing the same work
// again — which is the cost that makes killing processes the wrong verb in the
// first place.
func (a *activeRun) holdForOperator(ctx context.Context) error {
	p := a.pipeline
	for {
		hold, held, err := p.operatorHold()
		if err != nil {
			return err
		}
		if !held {
			// Clearing is what keeps a run that is working again from looking parked
			// to the next process that reads it, and it is where the hold's share of
			// this run's elapsed time is accounted.
			return a.clearOperatorHold()
		}
		if err := a.recordOperatorHold(hold); err != nil {
			return err
		}
		// This process stays open for a held run exactly as long as it stays open
		// for a refused one, and the bound counts every probe it has already slept
		// rather than this one alone. The hold is durable, so what the bound costs
		// is a later invocation picking the run up rather than anything being lost.
		if a.inProcessWait+operatorHoldProbe > p.Config.Execution.UsageLimitInProcessPause.Duration() {
			return operatorHoldPause{hold: hold}
		}
		if err := p.sleep(ctx, operatorHoldProbe); err != nil {
			return err
		}
		a.inProcessWait += operatorHoldProbe
	}
}

// recordOperatorHold makes the park durable, once, before any waiting happens.
// The record comes first for the reason a usage-limit deadline is written before
// its wait: a process that dies while the harness is held must leave a run that
// says so and can be picked up, rather than one that looks interrupted.
//
// Only the first park of a stretch is written. When it began is what says how
// long the harness has been quiet, and restamping it every probe would make a
// hold that has been in force since yesterday describe itself as five seconds
// old.
func (a *activeRun) recordOperatorHold(hold runstate.OperatorHold) error {
	a.outcome.PauseCause = runstate.PauseOperatorHold
	a.outcome.PausedByOperator = &hold
	if a.state.OperatorHeldSince != nil {
		return nil
	}
	since := a.pipeline.clock().Now().UTC()
	a.state.OperatorHeldSince = &since
	a.state.PauseCause = runstate.PauseOperatorHold
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the operator's pause this run parked on: %w", err)
	}
	return nil
}

// clearOperatorHold records that the run is spending again, and adds what the
// hold cost it to the run's account of held time. The span is measured from when
// the park was recorded rather than summed probe by probe, because a hold that
// outlived the process serving it held the run for the whole of it: the time a
// run spent doing nothing is the ledger's question, and "no process was awake
// for part of it" is not an answer to that question.
func (a *activeRun) clearOperatorHold() error {
	if a.state.OperatorHeldSince == nil {
		return nil
	}
	if held := a.pipeline.clock().Now().Sub(*a.state.OperatorHeldSince); held > 0 {
		a.state.OperatorHeldSeconds += int64(held / time.Second)
	}
	a.state.OperatorHeldSince = nil
	// The cause goes with the park it described. Left behind, it would say a run
	// that is working is waiting on somebody.
	a.state.PauseCause = ""
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the operator's pause: %w", err)
	}
	// Only a cause this park set is cleared from the outcome. A provider refusal
	// this run waited out earlier is kept there for the record, exactly as the
	// limit it named is, and a hold arriving afterwards must not erase it.
	if a.outcome.PauseCause == runstate.PauseOperatorHold {
		a.outcome.PauseCause = ""
	}
	a.outcome.PausedByOperator = nil
	return nil
}

// operatorHoldPause reports a run parked at a provider-call boundary because the
// operator holds harness activity, for longer than this process will stay open.
// Like the other pauses it is an error only so that it travels the path a
// stopped step already travels; it is deliberately not a failure, and the run it
// leaves behind is still in flight, still claimed, and still resumable.
type operatorHoldPause struct {
	hold runstate.OperatorHold
}

func (e operatorHoldPause) Error() string {
	return "parked because the operator paused all harness activity, at " + e.hold.HeldAt.Format(time.RFC3339)
}

// pausedForOperatorHold reports a run parked on the operator's hold. The
// recorded park is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForOperatorHold(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.OperatorHeldSince == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// sleep waits out a pause, cut short by a cancelled context so a shutdown is
// never held up by a deadline hours away.
func (p Pipeline) sleep(ctx context.Context, duration time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// verify puts the change through the two deterministic gates in front of the
// reviewer: what it was allowed to touch, and then the configured checks over
// what it did. Every attempt runs both again: no attempt inherits an earlier
// attempt's verification, and review and integration are only reachable through
// a change that passed both on the change being judged.
func (a *activeRun) verify(ctx context.Context) error {
	p := a.pipeline
	a.state.Phase = runstate.PhaseChecking
	// Scope is settled before the suite runs, because it costs a listing of names
	// against a handful of prefixes and the suite costs whatever the project's
	// suite costs. A change that is not allowed to stand does not get a check
	// suite spent on it first.
	changed, err := a.gateProtectedPaths(ctx)
	if err != nil {
		return err
	}
	// Then whether the reviewer could be shown it at all, for the same reason
	// again: a change the review bound would keep source or test files of out of
	// the reviewer's copy cannot be approved, so neither a check suite nor a
	// review round is spent finding that out (gateReviewBound).
	if err := a.gateReviewBound(ctx); err != nil {
		return err
	}
	// And the developer's own execution record is settled before the suite too,
	// for the same reason and one more: a change nobody ran is one the harness is
	// about to run for the first time, and the whole point of asking is that the
	// harness's suite is not supposed to be the first execution of anything.
	if err := a.gateCandidateVerification(ctx); err != nil {
		return err
	}
	// What the change touches is worked out once, here, and told to every check:
	// a check the operator wrote to narrow itself reads it, and one that did not
	// is unaffected. The stage's record is written before the first check runs
	// rather than after the last, because the record is what a surface reads to
	// say how much of the bound has gone while the checks are still running.
	//
	// Both writes of it are best effort. The record is visibility and nothing
	// reads it to decide anything, so a store that refuses it costs the status
	// line a figure and costs the stage nothing — and the first event the checks
	// persist is what says the store has gone, in the words it always said it in.
	//
	// What it touches also decides which path checks join the configured ones:
	// a check that vouches for part of the repository — the adoption walk, for
	// the README and the program it documents — runs for a change that touches
	// that part and costs nothing for one that does not. Where it runs, its
	// result is one more check result, so the review evidence carries it the
	// way it carries every other.
	narrowing := checks.NarrowGoPackages(a.worktree.Path, changed)
	added := pathChecksFor(a.worktree.Path, p.Config.PathChecks, changed)
	configured := p.checkStageTimeout()
	stage := &runstate.CheckStage{
		StartedAt:         p.clock().Now(),
		BoundSeconds:      int64(configured / time.Second),
		ConfiguredSeconds: int64(configured / time.Second),
		Narrowed:          narrowing.Describe() + describePathChecks(p.Config.PathChecks, added),
	}
	p.scaleCheckStage(stage)
	a.state.CheckStage = stage
	a.state.UpdatedAt = p.clock().Now()
	_ = p.Store.Save(a.state)
	stageCommands, providerCLIs := withPathChecks(p.Config.Checks, added)
	checkResults, lastSequence, err := p.Checks.Run(ctx, checks.Request{
		RunID:        a.state.RunID,
		Directory:    a.worktree.Path,
		Commands:     stageCommands,
		ProviderCLIs: providerCLIs,
		LastSequence: a.state.LastSequence,
		Env:          []string{narrowing.Env()},
		// The bound is read again as each check begins, because the load a
		// stage starts under is not the load three suites beside each other
		// build up; it only ever grows, and the record carries it before the
		// check it bounds starts.
		StageBound: func() time.Duration {
			p.scaleCheckStage(stage)
			return stage.Bound()
		},
		// Which check the stage is on goes onto the record as each begins.
		Started: func(command string, _ time.Duration) {
			stage.Command = command
			a.state.UpdatedAt = p.clock().Now()
			_ = p.Store.Save(a.state)
		},
	}, a.sink)
	a.outcome.Checks = checkResults
	a.state.LastSequence = lastSequence
	a.closeCheckStage(stage, checkResults)
	if err != nil {
		return stoppedBy(runstate.StopChecks, fmt.Errorf("verification infrastructure failed: %w", err))
	}
	for _, check := range checkResults {
		// A check that said it could not run judged nothing about the change,
		// so it is neither repair input nor a stop: the change goes on without
		// it, and the record below says which check it went without and why.
		if check.Passed || check.CouldNotRun != "" {
			continue
		}
		// Only a check that actually ran and failed describes something the
		// developer can repair. A cancelled or timed-out one says the run itself
		// was stopped, so it ends the run rather than spending an attempt on a
		// developer that would be stopped the same way.
		var cause error = fmt.Errorf("verification failed: %s exited with %d", check.Command, check.Process.ExitCode)
		switch {
		case check.Process.Status == execution.ProcessFailed:
			cause = checkFailure{result: check}
		case check.StoppedByCaller:
			// The context the run was given ended under the check: a deadline
			// on whatever started the run, or its cancellation. Neither is the
			// check's budget nor the stage's bound, so the stop names what did
			// end and keeps the budget beside it only as the one it did not reach.
			cause = callerStoppedCheck(ctx, check)
		case check.StoppedByStage:
			// The stage reached its bound, which is a different fact from a
			// check reaching its own: raising the per-check budget would not
			// have saved it, and the check it stopped may be one that had only
			// just started. So the stoppage names the bound, the check, what the
			// stage had spent across how many checks, and the two things that
			// move it — narrowing the gate, or raising the bound.
			//
			// The bound it reached was already scaled for the machine's load, so
			// what stopped the stage is the machine rather than the change: it is
			// recorded as a stop from outside the work, naming the bound, the
			// load, and the check, and it counts toward nothing — no brake, no
			// review round, no repair grant, no re-run.
			cause = fmt.Errorf(
				"the check stage reached its %s execution.check_stage_timeout bound%s during %s, which had run for %s; the stage had spent %s across %d check(s) (gate narrowed to: %s); narrow the per-run gate to what the change touches with $%s, move the whole suite to landing_checks, or raise the bound",
				stage.Bound(), stageBoundLoad(*stage), check.Command, check.Elapsed().Round(time.Second), stage.Elapsed().Round(time.Second), len(checkResults), stage.Narrowed, checks.ChangedGoPackagesVariable)
			a.recordEnvironmentalRefusal(runstate.CauseCheckStageBound, cause.Error(), ranAnyway)
		case check.Process.Status == execution.ProcessTimedOut:
			// A check stopped on time says nothing about the change: the work
			// may have been passing the whole way, as it was when this bound
			// was flat and a contended suite grew past it. So the failure names
			// both numbers and the setting that moves the ceiling, rather than
			// reporting the kill as an exit code nobody chose.
			cause = stoppedBy(runstate.StopCheckTimeout, checkTimedOut(check))
		case check.Process.Status == execution.ProcessCancelled || check.Process.Status == execution.ProcessStalled:
			cause = fmt.Errorf("verification was stopped: %s was %s after %s and judged nothing about the change", check.Command, check.Process.Status, check.Elapsed().Round(time.Second))
		}
		return stoppedBy(runstate.StopChecks, phaseError{status: statusForProcess(check.Process.Status), cause: cause})
	}
	// The change in the worktree now passes, so any failure an earlier attempt
	// was handed is no longer this run's outstanding repair input. What replaces
	// it is the evidence the promotion reads: these checks passed over this
	// content, on this attempt, at this commit, and integrate refuses without
	// exactly that. The content is read after the suite rather than before it,
	// because what the promotion moves is the tree as the suite left it. A
	// reading that fails is the harness's problem rather than a verdict on the
	// change, and is reported the way a check runner that broke is.
	content, err := p.Worktrees.ContentIdentity(ctx, a.worktree)
	if err != nil {
		return fmt.Errorf("verification infrastructure failed: name the change the checks passed over: %w", err)
	}
	a.state.CheckFailure = nil
	commands := make([]string, 0, len(checkResults))
	for _, check := range checkResults {
		if check.CouldNotRun == "" {
			commands = append(commands, check.Command)
		}
	}
	a.state.ChecksPassed = &runstate.ChecksPassed{
		Content:     content,
		Attempt:     a.state.RepairAttempts,
		Commit:      a.state.HarnessCommit,
		Commands:    commands,
		CouldNotRun: stage.CouldNotRun,
		At:          p.clock().Now().UTC(),
	}
	// A replay conflict is cleared here too, and this is the moment it stops
	// describing the change: it was moved into the worktree for its author to
	// settle, and a change that passes its checks on top of the target is that
	// settlement. Nothing later can say it — the promotion after it is an
	// ordinary fast-forward, with no replay left to prove anything.
	a.state.ReplayConflict = nil
	return nil
}

// ErrIntegrationUnearned is a promotion refused because the record does not
// show the change earned it: no passing checks recorded for the attempt about
// to be promoted, a check failure or path refusal still standing, or no
// approving verdict. It never fires on the ordinary path, where the repair loop
// re-earns the whole gate before every promotion; it is here for every other
// route into the promotion, which is the code that will never mention it.
var ErrIntegrationUnearned = errors.New("integration refused: the record does not show the change passed its gate")

// integrationEarned is the promotion's own reading of the gate. It asks the
// durable record rather than trusting that the caller ran the checks first,
// because control flow is a property of one caller: a definition that routed
// straight to `candidate.integrate`, or a resumed run that skipped the loop,
// would otherwise promote on a green it never saw. What it requires is bound to
// the exact candidate — the content of the change as the worktree holds it now,
// the attempt count, and the harness commit where the run made one — so
// evidence from an earlier attempt, or from a tree that has since moved, is not
// evidence for this one. The content is the binding that holds on a project
// that has committed nothing; it is read from the worktree again here rather
// than off the record, because the record can only say what the checks ran
// over, and the question is whether that is what is about to be promoted.
func (a *activeRun) integrationEarned(ctx context.Context) error {
	state := a.state
	if state.Document != nil {
		if _, err := a.gateProtectedPaths(ctx); err != nil {
			return err
		}
	}
	switch {
	case state.PathRefusal != nil:
		return fmt.Errorf("%w: a protected-path refusal is still recorded against the change", ErrIntegrationUnearned)
	case state.CheckFailure != nil:
		return fmt.Errorf("%w: %s exited with %d and nothing has passed the checks over the change since",
			ErrIntegrationUnearned, state.CheckFailure.Command, state.CheckFailure.ExitCode)
	case state.ChecksPassed == nil:
		return fmt.Errorf("%w: no configured check is recorded as having passed over the change", ErrIntegrationUnearned)
	case state.ChecksPassed.Attempt != state.RepairAttempts:
		return fmt.Errorf("%w: the checks passed over attempt %d and the change being promoted is attempt %d",
			ErrIntegrationUnearned, state.ChecksPassed.Attempt, state.RepairAttempts)
	case state.ChecksPassed.Commit != state.HarnessCommit:
		return fmt.Errorf("%w: the checks passed at commit %q and the change being promoted is at %q",
			ErrIntegrationUnearned, state.ChecksPassed.Commit, state.HarnessCommit)
	case state.ReviewDecision != runstate.ReviewApprove:
		return fmt.Errorf("%w: the recorded review decision is %q rather than an approval",
			ErrIntegrationUnearned, state.ReviewDecision)
	}
	content, err := a.pipeline.Worktrees.ContentIdentity(ctx, a.worktree)
	if err != nil {
		return fmt.Errorf("%w: the change about to be promoted could not be named: %v", ErrIntegrationUnearned, err)
	}
	if content != state.ChecksPassed.Content {
		return fmt.Errorf("%w: the checks passed over content %s and the change being promoted is %s",
			ErrIntegrationUnearned, state.ChecksPassed.Content, content)
	}
	return nil
}

// closeCheckStage records how the stage ended: when, what it spent, and
// whether it was the bound that ended it. The last check's figures are the
// stage's, because the runner measures the stage as its checks run; a stage no
// check reported on ended at the moment it is closed.
func (a *activeRun) closeCheckStage(stage *runstate.CheckStage, results []checks.Result) {
	finished := a.pipeline.clock().Now()
	stage.FinishedAt = &finished
	stage.ElapsedSeconds = int64(finished.Sub(stage.StartedAt) / time.Second)
	if last := len(results) - 1; last >= 0 {
		stage.Command = results[last].Command
		stage.ElapsedSeconds = int64(results[last].StageElapsed / time.Second)
		stage.StoppedAtBound = results[last].StoppedByStage
	}
	stage.Ran, stage.CouldNotRun = nil, nil
	for _, result := range results {
		switch {
		case result.CouldNotRun != "":
			stage.CouldNotRun = append(stage.CouldNotRun, runstate.CheckCouldNotRun{Command: result.Command, Reason: result.CouldNotRun})
		case result.Process.Status == execution.ProcessSucceeded || result.Process.Status == execution.ProcessFailed:
			// A check stopped on time, cancelled, or never started by the stage
			// reached no verdict, so it is not said to have run.
			stage.Ran = append(stage.Ran, result.Command)
		}
	}
	a.outcome.CheckStage = stage
	a.state.UpdatedAt = finished
}

// scaleCheckStage raises the stage's bound for the machine's load as it reads
// now: the configured figure scaled by the reading and the cap a local Git
// command's budget is scaled by (gitworktree.ScaleForLoad). It never lowers the
// bound — a check already given what the stage had left keeps it — and it
// records the heaviest reading it scaled for, beside the configured figure.
func (p Pipeline) scaleCheckStage(stage *runstate.CheckStage) {
	if p.Load == nil {
		return
	}
	load, cores, ok := p.Load()
	if !ok {
		return
	}
	if stage.Cores == 0 || load > stage.Load {
		stage.Load, stage.Cores = load, cores
	}
	scaled := int64(gitworktree.ScaleForLoad(stage.Configured(), load, cores) / time.Second)
	if scaled > stage.BoundSeconds {
		stage.BoundSeconds = scaled
	}
}

// checkTimedOut is the stop of a check the process runner reported as timed
// out, in words that cannot contradict themselves: the budget is named as what
// stopped the check only where the check ran for the whole of it. One stopped
// short of it was stopped by a limit the record does not name, and saying it
// reached a budget it did not is a reason nobody can act on.
func checkTimedOut(check checks.Result) error {
	ran := check.Elapsed().Round(time.Second)
	if check.Timeout > 0 && check.Elapsed() < check.Timeout-time.Second {
		return fmt.Errorf(
			"verification timed out: %s ran for %s and was stopped by a time limit before it reached its own %s execution.check_timeout budget, which judged nothing about the change; the limit that stopped it is not one this run records",
			check.Command, ran, check.Timeout)
	}
	return fmt.Errorf(
		"verification timed out: %s ran for %s and was stopped at its %s execution.check_timeout budget; raise that budget or lower execution.max_concurrent_developers, because concurrent runs multiply the wall clock of every suite",
		check.Command, ran, check.Timeout)
}

// callerStoppedCheck is the stop of a check whose run's own context ended
// under it. A deadline is a time limit and is classified as one; a
// cancellation is the run being stopped.
func callerStoppedCheck(ctx context.Context, check checks.Result) error {
	ran := check.Elapsed().Round(time.Second)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("verification was stopped: %s was cancelled after %s because the run it belonged to was stopped, and judged nothing about the change", check.Command, ran)
	}
	at := ""
	if deadline, ok := ctx.Deadline(); ok {
		at = " at " + deadline.Local().Format("15:04:05 MST")
	}
	return stoppedBy(runstate.StopCheckTimeout, fmt.Errorf(
		"verification timed out: %s ran for %s and was stopped when the deadline of the work that started this run passed%s, before its own %s execution.check_timeout budget; the check judged nothing about the change",
		check.Command, ran, at, check.Timeout))
}

// stageBoundLoad is what a stoppage at the stage bound says about the load the
// bound was scaled for: the configured figure and the reading where the load
// raised it, the reading alone where it did not, and nothing where the load
// could not be read.
func stageBoundLoad(stage runstate.CheckStage) string {
	switch {
	case stage.Scaled():
		return fmt.Sprintf(" (the configured %s scaled for %s)", stage.Configured(), stage.LoadSays())
	case stage.LoadSays() != "":
		return fmt.Sprintf(" (not scaled: %s is at or under one per core)", stage.LoadSays())
	default:
		return ""
	}
}

// landingCheckTimeout is the budget each landing check is given, read from the
// configuration with the same fallback the stage bound has.
func (p Pipeline) landingCheckTimeout() time.Duration {
	if budget := p.Config.Execution.LandingCheckTimeout.Duration(); budget > 0 {
		return budget
	}
	return checks.DefaultLandingCheckTimeout
}

// checkStageTimeout is the bound the stage is recorded under. It is read from
// the configuration rather than from the runner, which is an interface here,
// and a configuration that names none — a pipeline assembled in a test — is
// recorded at the runner's own default so the record and the runner agree.
func (p Pipeline) checkStageTimeout() time.Duration {
	if bound := p.Config.Execution.CheckStageTimeout.Duration(); bound > 0 {
		return bound
	}
	return checks.DefaultStageTimeout
}

// gateProtectedPaths refuses a change that touched an upstream artifact this
// work item never admitted into its scope. The paths are the harness's own
// configuration, the artifact homes the roles above the developer own, and the
// derived exports the worktree manager refreshes and holds out of every run's
// change; the grants are read from the work item's own text — which is the whole
// point of doing it this way. An exception written into an item was decided
// before the run started and reviewed with the rest of it; an exception the run
// discovers is the developer deciding what its work was allowed to redefine.
//
// The exports are asked of the manager at each round rather than remembered,
// because their hold is an index bit under `.git` that a developer's sandbox can
// write: what makes the refreshed export stay out of a change is this comparison
// and not that bit surviving whatever ran in the worktree.
//
// It answers with a refusal rather than a verdict. Nothing here says the change
// is wrong, only that part of it is not this run's to make, so it goes back to
// the same developer inside the same repair loop as any other failure that
// stands between a change and its reviewer.
//
// It reports the paths it read, because the check stage after it narrows on
// the same listing and a second reading of the worktree would be a second
// chance for the two to disagree about one change.
func (a *activeRun) gateProtectedPaths(ctx context.Context) ([]string, error) {
	changed, err := a.pipeline.Worktrees.ChangedPaths(ctx, a.worktree)
	if err != nil {
		return nil, fmt.Errorf("list the paths this change touches: %w", err)
	}
	if a.state.Document != nil {
		if err := a.gateDocument(ctx, changed); err != nil {
			return nil, err
		}
	}
	protected := protectedpath.Protect(a.pipeline.Config, a.pipeline.Worktrees.CurrentExports()...)
	granted := protectedpath.Grants(grantEvidence(a.item)...)
	if a.state.Document != nil {
		// The harness grants only its confirmed file. Absolute refusals such as
		// role definitions and held tracker exports still apply below.
		granted = []string{a.state.Document.Candidate.Artifact.Path}
	}
	refused := protected.Refused(changed, granted)
	if len(refused) == 0 {
		// The change in the worktree is within its scope now, so a refusal an
		// earlier attempt was handed no longer describes it.
		a.state.PathRefusal = nil
		return changed, nil
	}
	return nil, phaseError{status: runstate.StatusFailed, cause: pathRefusal{
		refusal: boundedPathRefusal(refused, granted),
		set:     protected,
	}}
}

// boundedPathRefusal is what a refusal is allowed to carry into durable state
// and into the developer's next attempt. A change that rewrote a whole artifact
// home is refused on all of it and told about the first of it, with the rest
// counted rather than dropped silently: the developer has to take every one of
// them back out, and a listing that stopped without saying so would read as the
// whole of what the gate caught.
func boundedPathRefusal(refused, granted []string) runstate.PathRefusal {
	recorded := runstate.PathRefusal{Paths: refused, Grants: granted}
	if len(refused) > runstate.MaxRefusedPaths {
		recorded.Paths = refused[:runstate.MaxRefusedPaths]
		recorded.Omitted = len(refused) - runstate.MaxRefusedPaths
	}
	return recorded
}

// pathRefusal is a change refused for the paths it touched. Like a failing check
// it is its own error type because it is repair input for the developer that
// produced the change, which has to be told apart from the gate failing to run
// at all — an unreadable worktree is not something a developer can fix by
// editing its change.
type pathRefusal struct {
	refusal runstate.PathRefusal
	set     protectedpath.Set
}

func (e pathRefusal) Error() string {
	return fmt.Sprintf("change touches protected paths this work item does not grant: %s",
		strings.Join(e.refusal.Paths, ", "))
}

// checkFailure is a deterministic check that ran and failed. It is its own error
// type because a failure the developer can be asked to repair has to be told
// apart from verification infrastructure that could not run at all.
type checkFailure struct {
	result checks.Result
}

func (e checkFailure) Error() string {
	return fmt.Sprintf("verification failed: %s exited with %d", e.result.Command, e.result.Process.ExitCode)
}

// integrate promotes an approved change and records the promotion.
//
// Reaching this phase is what puts a run in the promotion queue for its target
// branch. Everything under the lease reads where that branch is and then moves
// it — the drift check, the fast-forward, and the merge the forge is asked for
// against the same commit — so a second promotion interleaved with it is a race
// rather than a second promotion. The lease is the harness's own, taken in this
// process: no agent asks for it, and none can perform what it admits.
//
// It is released as soon as the promotion settles, which is well before the run
// ends. Cleanup is this run's own artifacts rather than the target branch, and
// holding the queue through it would make every other run wait on work that
// cannot affect them.
func (a *activeRun) integrate(ctx context.Context) error {
	p := a.pipeline
	// The gate is read off the record before anything here is written or taken:
	// a refusal must leave the run exactly as the reviewer left it, holding no
	// lease and standing in no phase it did not earn.
	if err := a.integrationEarned(ctx); err != nil {
		return err
	}
	a.state.Phase = runstate.PhaseIntegrating
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save integrating run state: %w", err)
	}
	lease, err := p.Store.LeasePromotion(ctx, a.worktree.TargetBranch)
	if err != nil {
		return stoppedBy(runstate.StopIntegration, fmt.Errorf("wait for this run's turn to promote: %w", err))
	}
	// Releasing is this process letting the next promotion in, and the operating
	// system does it anyway when the process exits. A close that failed therefore
	// says nothing about the promotion below, which either happened or did not.
	defer func() { _ = lease.Release() }()
	// Where the remote target stands is settled before the promotion rather than
	// only after it. A promotion made onto a target the remote has moved away from
	// can be neither published nor taken back, so finding out afterwards closes the
	// item as integrated against a divergence nothing owns; finding out here leaves
	// a change that can still be replayed onto wherever the target went.
	if err := a.settleRemoteTarget(ctx); err != nil {
		return stoppedBy(runstate.StopIntegration, err)
	}
	// A target the forge protects is one whose local copy this run never moves:
	// the change lands by the forge merging its pull request, and the local branch
	// follows the forge by a fast-forward afterwards. Promoting locally first is
	// what stranded commits on main twice, because a merge the forge refused or
	// has not performed yet leaves the local target ahead of the remote with
	// nothing that ever reconciles the two.
	promote := p.Worktrees.Integrate
	if a.landsThroughPullRequest(ctx) {
		promote = p.Worktrees.PrepareLanding
	}
	integration, err := promote(ctx, a.worktree, integrationMessage(a.item, a.outcome))
	if err != nil {
		// A refused promotion may already have committed what the developer left,
		// and that commit is what this worktree's HEAD is now. It is recorded
		// before the failure is reported, for the reason publishing records its
		// own: a retry, a resumed run, and a reconciler all have to be able to tell
		// it from a commit an agent made for itself.
		if integration.SourceCommit != "" && integration.SourceCommit != a.state.HarnessCommit {
			a.recordHarnessCommit(integration.SourceCommit)
			a.state.UpdatedAt = p.clock().Now()
			if saveErr := p.Store.Save(a.state); saveErr != nil {
				return stoppedBy(runstate.StopIntegration, withFailedRecord(fmt.Errorf("integrate approved change: %w", err),
					fmt.Errorf("record the commit the refused promotion made: %w", saveErr)))
			}
		}
		return stoppedBy(runstate.StopIntegration, fmt.Errorf("integrate approved change: %w", err))
	}
	a.outcome.Integration = &integration
	a.state.Integration = &runstate.Integration{
		TargetBranch:         integration.TargetBranch,
		SourceCommit:         integration.SourceCommit,
		TargetCommit:         integration.TargetCommit,
		PreviousTargetCommit: integration.PreviousTargetCommit,
		ThroughPullRequest:   integration.ThroughPullRequest,
	}
	// A replay that reached the promotion passed its gate, so it is no longer
	// waiting to be judged: left set, a later re-entry into the repair loop would
	// charge a replay that in fact landed.
	a.state.ReplayUnjudged = false
	// A landing through the pull request is on the record before the forge is
	// asked for anything, so a process killed from here on leaves reconciliation
	// the fact it needs: the change was to land by the forge's merge, and the
	// local target says nothing about whether it did.
	if integration.ThroughPullRequest {
		a.state.UpdatedAt = p.clock().Now()
		if err := p.Store.Save(a.state); err != nil {
			return fmt.Errorf("record the landing through pull request before asking the forge to merge: %w", err)
		}
	}
	// The approving verdict authorized this promotion, so it also authorized the
	// merge of the pull request that carried it. Publishing does not fail the run
	// over an unfinished publication — the local target branch has already moved
	// and it is the authoritative one — but it does fail it over a remote target
	// that diverged in the window this check-then-act leaves open, because the
	// alternative is closing the item as integrated against a divergence no
	// fast-forward reconciles. The promotion stands either way; the blocker says so.
	if err := a.publishIntegration(ctx); err != nil {
		return err
	}
	// A landing through the pull request has landed only where the forge merged
	// it or holds the merge queued. Anything else left the change on its pull
	// request and nowhere on the target, so the item is not closed on it.
	if integration.ThroughPullRequest && !a.landedThroughPullRequest() {
		return a.blockOnUnlandedPullRequest(integration)
	}
	a.state.Phase = runstate.PhaseCompleting
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save integrated run state: %w", err)
	}
	return nil
}

// finish is the completing half of a run: what the run produced is recorded and
// the run is made terminal, and only then is what it created removed.
//
// It is control flow rather than an operation of its own. Both halves are
// registered steps — run.complete and run.clean-up — so a workflow definition
// orders the same two in the same order this does, and a step that arrives
// between them arrives in the registry rather than only here.
func (a *activeRun) finish(ctx context.Context) (Outcome, error) {
	outcome, err := a.complete(ctx)
	if err != nil {
		// Completing failed, and the definition has no outcome for that: the state
		// was entered and produced nothing it can route. The instance is left
		// standing in it, which says exactly that.
		return outcome, err
	}
	a.observe(ctx, deliveryComplete, "completed")
	if a.outcome.Integration == nil {
		return outcome, nil
	}
	// A landing the forge has queued is on no target branch yet, so nothing
	// proves this run's artifacts are integrated and cleanup would refuse them.
	// They stay where they are, in the cleaning_up phase, until `yoyo reconcile`
	// finds the merge performed, catches the local target up onto it, and cleans
	// up on that proof.
	if a.outcome.Integration.ThroughPullRequest && a.mergeQueued() {
		a.observe(ctx, deliveryCleanUp, "partial")
		return outcome, nil
	}
	if err := a.cleanUp(ctx); err != nil {
		// Both cleanup failures are the same outcome here, because the definition
		// distinguishes only whether the cleanup finished: an artifact that survived
		// and a terminal record that would not save are each a run that succeeded
		// with something left for somebody else to settle.
		a.observe(ctx, deliveryCleanUp, "partial")
		// The two things cleanup can fail at are reported differently. A run whose
		// artifacts are all gone and whose terminal record would not save is one
		// reconciliation has to settle; anything else left an artifact standing,
		// which is reported on a succeeded run because the change is integrated and
		// the item is closed either way.
		var unrecorded completionRecordingFailure
		if errors.As(err, &unrecorded) {
			return a.pipeline.reportCompletionRecordingFailure(a.state, a.outcome, unrecorded.cause)
		}
		return a.pipeline.reportOutstandingCleanup(a.state, a.outcome, err)
	}
	a.observe(ctx, deliveryCleanUp, "cleaned")
	// The landing checks come after everything the run is judged by. The run is
	// terminal, its item is settled, and its artifacts are gone; what runs now is
	// over the target branch rather than over the change, and nothing it finds
	// changes what was just recorded.
	a.runLandingChecks(ctx)
	return a.outcome, nil
}

// runLandingChecks runs the configured landing checks over the commit this run
// integrated, and records what they made of it on the run and on the item.
//
// They run here, after the run is over, because they are the other half of a
// per-run gate that is narrowed to what a change touches: the suite the gate no
// longer runs whole is run whole once per landing, over what actually landed,
// rather than once per attempt over each candidate. A landing that goes red is
// news about the target branch and not a verdict on this run — the run passed
// its own gate and was approved — so it never fails the run, never reopens the
// item, and never blocks anything. It is recorded, said, and filed as its own
// work, under the operator's standing order that a red landing files its own
// item.
//
// Nothing here returns an error. Every way the landing can go wrong short of a
// red result — no checkout, checks that could not run, a checkout that would
// not go away, an item the tracker would not take, a note the item would not
// take — is written onto the record as what it was, because a landing nobody
// can read is the one thing worse than a red one.
func (a *activeRun) runLandingChecks(ctx context.Context) {
	p := a.pipeline
	if a.outcome.Integration == nil || len(p.Config.LandingChecks) == 0 {
		return
	}
	// A watch session hosting this run is told the landing has begun, so a
	// drain bound that runs out stops it — unverified, filing nothing — rather
	// than reading a run with no in-flight record as one still before its claim
	// and waiting a two-hour suite out past the bound.
	landingBegun(ctx)
	commit := a.outcome.Integration.TargetCommit
	budget := p.landingCheckTimeout()
	landed := &runstate.LandingChecks{
		Commit:       commit,
		StartedAt:    p.clock().Now(),
		BoundSeconds: int64(budget / time.Second),
		TargetBranch: a.outcome.Integration.TargetBranch,
	}
	a.state.LandingChecks = landed
	a.outcome.LandingChecks = landed
	a.saveLanding(landed)
	var problems []string
	lease, err := a.leaseLanding(ctx, landed)
	if err != nil {
		problems = append(problems, fmt.Sprintf("the landing checks never started: %v", err))
	} else if p.Landings == nil {
		problems = append(problems, "nothing is wired to cut a checkout of the integrated commit for them")
	} else if path, err := p.Landings.CheckoutCommit(ctx, a.state.RunID, commit); err != nil {
		problems = append(problems, fmt.Sprintf("no checkout of the integrated commit could be cut: %v", err))
	} else {
		results, lastSequence, err := p.Checks.Run(ctx, checks.Request{
			RunID:        a.state.RunID,
			Directory:    path,
			Commands:     p.Config.LandingChecks,
			LastSequence: a.state.LastSequence,
			// A landing is where the whole suite runs, so a landing check written
			// to read the narrowing is told there is none — and it is given the
			// landing's own budget with no stage bound, because the suite moved
			// here is the one the gate's stage bound cannot hold.
			Env:       []string{checks.Narrowing{Whole: true, Reason: "a landing runs the whole suite"}.Env()},
			Timeout:   budget,
			Unbounded: true,
		}, a.sink)
		a.state.LastSequence = lastSequence
		if removeErr := p.Landings.RemoveCheckout(ctx, path); removeErr != nil {
			problems = append(problems, fmt.Sprintf("the landing checkout at %s could not be removed and is left for somebody to remove by hand: %v", path, removeErr))
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("the landing checks could not be run: %v", err))
		}
		stopped := ""
		for _, result := range results {
			// A check killed on time or cancelled judged nothing, which is the
			// same rule the per-run gate applies: it is neither a pass nor a
			// failure, and a landing it happened in is unverified rather than
			// red, filing nothing.
			atBudget := result.Process.Status == execution.ProcessTimedOut
			if atBudget {
				stopped = fmt.Sprintf("%s was stopped at its %s execution.landing_check_timeout budget after %s and judged nothing", result.Command, budget, result.Elapsed().Round(time.Second))
			} else if result.Process.Status == execution.ProcessCancelled || result.Process.Status == execution.ProcessStalled {
				stopped = fmt.Sprintf("%s was %s after %s and judged nothing", result.Command, result.Process.Status, result.Elapsed().Round(time.Second))
			}
			landed.Checks = append(landed.Checks, runstate.LandingCheckResult{
				Command:        result.Command,
				Passed:         result.Passed,
				ExitCode:       result.Process.ExitCode,
				ElapsedSeconds: int64(result.Elapsed() / time.Second),
				StoppedAtBound: atBudget,
				Output:         landingCheckOutput(result),
				CouldNotRun:    result.CouldNotRun,
			})
		}
		if stopped != "" {
			problems = append(problems, stopped)
		}
		// Every check ran to its own exit and passed is green; one that failed
		// on its own exit is red; a list stopped short of a verdict is neither.
		landed.Ran = err == nil && len(results) > 0 && stopped == ""
		landed.Green = landed.Ran && len(results) == len(p.Config.LandingChecks) && landed.AllPassed()
	}
	// The next landing on the branch is let in once this one's checkout is gone,
	// and not before: the lease is what the queue is, so it is held for exactly
	// as long as a suite is running or its checkout is standing.
	if lease != nil {
		if err := lease.Release(); err != nil {
			problems = append(problems, fmt.Sprintf("the landing lease would not release: %v", err))
		}
	}
	// A landing the hosting session stopped at its drain bound says so, so the
	// unverified landing names the redeploy rather than only a check cancelled.
	if drained, stopped := drainedForRedeploy(ctx); stopped {
		problems = append(problems, "the landing checks were stopped because "+drained.Error())
	}
	finished := p.clock().Now()
	landed.FinishedAt = &finished
	if landed.Red() {
		a.fileRedLanding(ctx, landed)
	}
	landed.Problem = strings.Join(problems, "; ")
	a.saveLanding(landed)
	// The item the change landed for carries the landing's result too, so a
	// reader of the item sees what its landing made of the target branch without
	// opening the run. The item is closed by now, and appending a note to a
	// closed item is the one write to it that is still right. The write is
	// bounded rather than recovered: the run is over, and a `bd` too busy to
	// take a note is recorded as such rather than waited out.
	noteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(noteCtx, a.state.WorkItemID, "Landing checks: "+landed.Describe()); err != nil {
		landed.Problem = strings.Join(append(problems, fmt.Sprintf("the item could not be told: %v", err)), "; ")
		a.saveLanding(landed)
	}
}

// ConfigReaders is what the running parts of the product recorded about the
// configuration keys their builds read, compared against the file each reads
// and against newly introduced shipped-template keys.
// It is satisfied by *runstate.ConfigReaderStore.
type ConfigReaders interface {
	MismatchesIn(read func(configPath string) ([]byte, error)) ([]runstate.ConfigMismatch, error)
	TemplateMismatches(templatePath string, added []string) ([]runstate.ConfigTemplateMismatch, error)
}

// nameUnreadingParts saves the landing's active and prospective comparisons
// before completion and cleanup. A failed tracker delivery is retained for
// reconciliation; an unsaved comparison refuses completion.
func (a *activeRun) nameUnreadingParts(ctx context.Context) error {
	p := a.pipeline
	if a.outcome.Integration == nil || a.mergeQueued() {
		return nil
	}
	comparison, err := (configFindingRecorder{
		configLanding: configLanding{repository: p.Repository, files: p.Worktrees, readers: p.ConfigReaders},
		store:         p.Store, tracker: p.Tracker,
	}).name(ctx, &a.state, *a.outcome.Integration)
	a.outcome.ConfigMismatches = comparison.active
	a.outcome.TemplateConfigMismatches = comparison.templates
	a.outcome.ConfigComparison = a.state.ConfigComparison
	var delivery configFindingDelivery
	if errors.As(err, &delivery) {
		return nil
	}
	return err
}

// landingQueueSlack is the margin a landing's wait allows beyond the checks of
// the landing ahead of it, for that landing's checkout and its removal: the
// lease is released once the checkout is removed, before a red landing's
// filing and note, so those are not what it covers. The wait is sized for one
// landing ahead; a third queued behind two full-budget suites waits it out.
const landingQueueSlack = 15 * time.Minute

// leaseLanding waits this landing's turn on its target branch. At most one
// landing per target branch runs its checks at a time, because the landing
// checks are the whole suite and two of them at once, beside the next runs'
// gates, is the load the suite was moved to the landing to escape.
//
// The wait is bounded by what the landing ahead may take — every landing check
// at its whole budget, and the slack around them — rather than by the
// promotion queue's fifteen minutes, which a two-hour suite would outlast every
// time. A landing that waits the bound out, or whose process is stopped while
// it waits, has run nothing and is unverified. The moment it begins waiting is
// on the record before it waits, so `yoyo status` and the sweep can say it is
// waiting rather than running.
func (a *activeRun) leaseLanding(ctx context.Context, landed *runstate.LandingChecks) (*runstate.Lease, error) {
	p := a.pipeline
	wait := p.landingCheckTimeout()*time.Duration(len(p.Config.LandingChecks)) + landingQueueSlack
	lease, err := p.Store.LeaseLanding(ctx, landed.TargetBranch, wait, func() {
		since := p.clock().Now()
		landed.WaitingSince = &since
		a.saveLanding(landed)
	})
	if err != nil {
		return nil, err
	}
	if landed.WaitingSince != nil {
		admitted := p.clock().Now()
		landed.AdmittedAt = &admitted
		a.saveLanding(landed)
	}
	return lease, nil
}

// saveLanding writes the landing as it stands onto the run's record. The run is
// terminal by now, so a save that fails loses only the landing's account of
// itself, and that loss is put on the outcome rather than swallowed.
func (a *activeRun) saveLanding(landed *runstate.LandingChecks) {
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		landed.Problem = strings.TrimPrefix(landed.Problem+"; the run's record would not take the landing: "+err.Error(), "; ")
	}
}

// fileRedLanding admits the work a red landing is: the failing check, over the
// commit, after the run that landed it, with the check's own output in it. It
// is filed under the goal the landed item served, because a break in work that
// served a goal is work serving the same goal, and at the front of the queue,
// because a red target branch is what every run after it is cut from.
//
// It is filed once per landing and never again for the same commit: a landing
// is one run's, and the record it is written on says whether it was filed.
func (a *activeRun) fileRedLanding(ctx context.Context, landed *runstate.LandingChecks) {
	p := a.pipeline
	if p.Filer == nil {
		landed.FilingProblem = "nothing is wired to file a work item"
		return
	}
	failing, _ := landed.Failing()
	commit := landed.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	target := a.outcome.Integration.TargetBranch
	what := fmt.Sprintf("%s exited %d", failing.Command, failing.ExitCode)
	marker := redLandingMarker(target, failing.Command)
	// A red target branch that stays red is one item, not one per landing: an
	// open item the harness filed for the same check on the same branch is told
	// about this landing instead of a second being filed beside it, which the
	// scheduler would otherwise start concurrently with the first.
	existing, err := a.openRedLandingItem(ctx, marker)
	if err != nil {
		landed.FilingProblem = fmt.Sprintf("could not read whether a red-landing item is already open: %v", err)
		return
	}
	if existing != "" {
		landed.FiledWorkItem = existing
		landed.FiledEarlier = true
		note := fmt.Sprintf("Red again at %s on %s, after %s (%s) integrated: %s.", commit, target, a.state.WorkItemID, a.state.RunID, what)
		if _, err := p.Tracker.RecordOutcome(ctx, existing, note); err != nil {
			landed.FilingProblem = fmt.Sprintf("the open item %s could not be told about this landing: %v", existing, err)
		}
		return
	}
	// The title and the description are the harness's own words and nothing
	// else. Both are fields the protected-path gate reads grants from, so what a
	// check printed — text a change can shape — goes in the notes, which the gate
	// deliberately never reads.
	description := fmt.Sprintf("Red landing on %s at %s, after %s (%s) integrated: %s.\n\n"+
		"The per-run gate passed on the change and the reviewer approved it; the landing checks then ran the whole suite over the integrated commit and this one failed. "+
		"So %s is red at %s, and every run cut from it starts on a red base until this is fixed or the landing is shown to have been the suite's fault. "+
		"Reproduce with `%s` at %s. What the check said is in this item's notes.",
		target, commit, a.state.WorkItemID, a.state.WorkItemTitle, what, target, commit, failing.Command, landed.Commit)
	notes := fmt.Sprintf("Filed by the harness for the red landing of %s (%s) at %s on %s, under the operator's standing order that a red landing files its own item.\n%s",
		a.state.WorkItemID, a.state.RunID, commit, target, marker)
	// The goal line is written in the words the landed item states it in, and
	// the tracker client's Create derives the goal witness from the notes it is
	// handed — every creation that writes a `Goal served:` line records
	// yoyodyne_goal_recorded beside it — so the filed item's attribution is
	// witnessed exactly as an admission's is.
	if statement, named := goal.NamedIn(a.item.Notes); named {
		notes += "\n\n" + goal.Note(statement)
	}
	if output := strings.TrimSpace(failing.Output); output != "" {
		notes += "\n\nWhat the check said (bounded):\n\n" + quotedOutput(output)
	}
	// Priority 0 is where this project puts an operator's order, and a red
	// target branch is that: every run until it is fixed is cut from it.
	priority := 0
	created, err := p.Filer.Create(ctx, beads.NewWorkItem{
		Title:       fmt.Sprintf("Red landing on %s at %s: %s after %s integrated", target, commit, what, a.state.WorkItemID),
		Description: description,
		Type:        "bug",
		Notes:       notes,
		Priority:    &priority,
		Origin:      domain.WorkItemOrigin{Asker: domain.AskerHarness},
	})
	if err != nil {
		landed.FilingProblem = err.Error()
		return
	}
	landed.FiledWorkItem = created.ID
}

// redLandingMarker is the line a red-landing item's notes carry naming the
// branch and the check, which is what a later landing reads to find it. It is
// in the notes rather than the title because a title is prose somebody may
// edit, and the notes are what the harness appends to and never rewrites.
func redLandingMarker(target, command string) string {
	return "Red-landing check: " + command + " on " + target
}

// openRedLandingItem is the identifier of an unfinished item the harness filed
// for the same check on the same branch, or nothing. It reads the queue's
// three unfinished statuses, because an item somebody has claimed or that is
// blocked is still the item that answers this branch being red.
func (a *activeRun) openRedLandingItem(ctx context.Context, marker string) (string, error) {
	for _, status := range []string{"open", "in_progress", "blocked"} {
		items, err := a.pipeline.Filer.List(ctx, status)
		if err != nil {
			return "", err
		}
		for _, item := range items {
			for _, line := range strings.Split(item.Notes, "\n") {
				if strings.TrimSpace(line) == marker {
					return item.ID, nil
				}
			}
		}
	}
	return "", nil
}

// quotedOutput sets what a check printed apart from the harness's own lines in
// a note, one "> " per line. The notes are read line by line for two things a
// check's output must never be taken for — the newest `Goal served:` line is
// the item's attribution, and a `Red-landing check:` line is what a later
// landing finds the item by — and both readers match a line that begins with
// their prefix, which a quoted line does not.
func quotedOutput(output string) string {
	lines := strings.Split(output, "\n")
	for index, line := range lines {
		lines[index] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// landingCheckOutput is what a failing landing check said, cut as a repair
// attempt's input is cut. A check that passed said nothing worth carrying.
func landingCheckOutput(result checks.Result) string {
	if result.Passed || result.CouldNotRun != "" {
		return ""
	}
	return boundedCheckOutput(result)
}

// complete records the outcome on the work item, closes an integrated item whose
// publication is settled, prices what the run spent, and makes the run durably
// terminal.
//
// It stops short of removing anything, and that boundary is the point: the run
// becomes terminal here and cleanup is the only step left, so a process
// interrupted between the two leaves a succeeded run in the cleaning_up phase
// rather than a closed item behind a run nothing finished.
func (a *activeRun) complete(ctx context.Context) (Outcome, error) {
	p := a.pipeline
	// The pull request the run is about to report is on its record, read back
	// from the store, before anything about the run is recorded as finished. A
	// summary naming a request the record does not hold is a change on the forge
	// nothing afterwards can see waiting, and the run is refused completion over
	// it rather than recorded succeeded.
	if err := a.publicationRecorded(); err != nil {
		return a.fail(stoppedBy(runstate.StopPublish, err), runstate.StatusFailed)
	}
	if err := a.nameUnreadingParts(ctx); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, err), runstate.StatusFailed)
	}
	// Where the landing does not discharge the item, where that item goes is
	// decided before the outcome is recorded rather than as part of the settlement
	// below. The notes recorded there name the disposition, and it is derived from
	// the run's own landing fields — which a tracker that refuses the dependency
	// still moves at this point. Deciding first is what stops the notes, and every
	// surface that reads the run afterwards, naming a disposition the item did not
	// get. Only that decision moves: the item's status is still settled below,
	// after the dependency, so there is no window where it is open and unheld.
	//
	// A tracker that could not be read here is not the failure. It is left to the
	// settlement below, which asks again under the same recovery it always did, so
	// a run this costs is costed in the same place as before and its outcome still
	// reaches the item first.
	undischarged := a.outcome.Integration != nil && !a.mergeQueued() && !a.state.Discharges()
	var undischargedItem beads.WorkItem
	decided := false
	if undischarged {
		if arranged, item, err := arrangeUndischarged(ctx, p.Tracker, a.state); err == nil {
			a.applyUndischargedDisposition(arranged)
			undischargedItem = item
			decided = true
		}
	}
	// The tracker is updated only once the work is durably where it belongs:
	// after integration when it is automatic, and after passing checks when a
	// human still owns the promotion.
	// The tracker is a store other processes are writing to, so a write here that
	// produced no answer at all — the `bd` that was killed or never completed —
	// is contention far more often than a store that is broken, and it judges
	// nothing about a run whose work is already integrated. Recording that as a
	// failed run is the same loss the forge boundaries carried, which is why the
	// product manager joined these writes to this item's set. They share one
	// boundary and one window: a `bd` that could not be run for the outcome could
	// not be run for the closure either.
	if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		_, err := p.Tracker.RecordOutcome(ctx, a.state.WorkItemID, renderOutcomeNotes(a.outcome))
		return err
	}); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record successful run outcome: %w", err)), runstate.StatusFailed)
	}
	// An item closes as integrated once the promotion is where it is going to
	// stay. A merge the forge only queued is not that yet: it lands minutes
	// later, or the forge drops it because something the base branch requires
	// went unmet, and closing here would record integrated for a publication
	// that may never happen. So the closure waits for the forge's answer, which
	// is the same step that already settles the queue — and until it arrives the
	// item stays claimed with the queued merge named on it, rather than closed
	// against a merge nobody has confirmed.
	// A change that discharges nothing is the other reason the closure does not
	// follow the promotion. The change is integrated and the run succeeded; what
	// was said about it — by the developer's claim, by the reviewer's approval, or
	// by both — is that it is evidence rather than the work the item asked for, and
	// closing on it would record as done exactly what the evidence says was not.
	// The item goes back to the backlog with that account on it instead, parked or
	// waiting on the impediment the landing named, which is the state a person or a
	// later run can still act on.
	if a.outcome.Integration != nil && !a.mergeQueued() {
		if undischarged {
			if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
				if decided {
					return reopenUndischarged(ctx, p.Tracker, a.state, undischargedItem)
				}
				// The read that would have decided it above failed, so the whole
				// settlement is made here. What it settles on is still taken back onto
				// the run, which is all that is left to keep true: the notes are already
				// written, and they say what the claim asked for.
				settled, err := settleUndischarged(ctx, p.Tracker, a.state)
				if err != nil {
					return err
				}
				a.applyUndischargedDisposition(settled)
				return nil
			}); err != nil {
				return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("reopen the work item this run did not discharge: %w", err)), runstate.StatusFailed)
			}
		} else {
			if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
				_, err := p.Tracker.Complete(ctx, a.state.WorkItemID, completionReason(a.outcome))
				return err
			}); err != nil {
				return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("close integrated work item: %w", err)), runstate.StatusFailed)
			}
			a.outcome.WorkItemClosed = true
			p.closeDocketWithItem(a.state, "closed by run "+a.state.RunID+", whose change landed")
		}
	}
	// The item is priced when the run that spent it ends, whether or not the
	// closure waits: a queued merge defers the closure to a later sweep, and that
	// sweep does not re-read what this run cost.
	a.recordPrice()

	// The run becomes durably terminal before anything is destroyed. Cleanup is
	// the only remaining step, it removes evidence, and it must never be able to
	// leave a closed item behind a non-terminal run: an interrupted process at
	// this boundary leaves a succeeded run in the cleaning_up phase with
	// worktree_removed still false, which is a resumable instruction rather than
	// a lost run. A reconciler re-runs cleanup, which refuses anything that is
	// not the recorded, registered, already-integrated worktree.
	completedAt := p.clock().Now()
	// A recorded stop or park is an instruction to continue later, and this run
	// has finished. Leaving either would promise a continuation of a completed run.
	a.state.ProviderStop = ""
	a.state.RedeployStop = nil
	a.state.OperatorHeldSince = nil
	a.state.Status = runstate.StatusSucceeded
	a.state.Phase = runstate.PhaseCleaningUp
	a.state.UpdatedAt = completedAt
	a.state.CompletedAt = &completedAt
	if a.outcome.Integration == nil {
		a.state.Phase = runstate.PhaseComplete
		a.state.StopClass = runstate.StopIntegrationPolicy
		a.outcome.StopClass = a.state.StopClass
	}
	if err := p.Store.Save(a.state); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("save successful run state: %w", err)), runstate.StatusFailed)
	}
	a.outcome.Status = runstate.StatusSucceeded
	a.outcome.Phase = a.state.Phase
	return a.outcome, nil
}

// closeDocketWithItem closes the docket entries standing for an item this run
// has just closed: a stoppage an earlier run left, settled by this one landing.
// A closure that could not be written does not fail a run whose work is
// integrated and whose item is closed. The reconcile sweep closes every entry
// whose item the tracker holds as closed, so the next sweep closes this one.
func (p Pipeline) closeDocketWithItem(state runstate.State, reason string) {
	if p.Docket == nil {
		return
	}
	_, _ = p.Docket.SettleClosedItem(state.WorkItemID, reason)
}

// cleanUp removes what this run created, once its work is somewhere else, and
// records the run as complete once nothing it made is left.
//
// Only artifacts proven to be integrated are removed, and only after the tracker
// agrees the item is done and that fact is durable. Cleanup reports each
// artifact separately, so a partial removal is recorded as what it is rather
// than collapsed into a single failed flag — which is why what was removed is
// recorded before the failure is returned rather than after it.
//
// The terminal record is here rather than beside the call because it is the last
// thing cleanup is for: it says the run has nothing left standing, so a run that
// left an artifact behind never reaches it and is settled as the outstanding
// cleanup it is.
func (a *activeRun) cleanUp(ctx context.Context) error {
	cleanup, err := a.pipeline.Worktrees.CleanupIntegrated(ctx, gitworktree.CleanupRequest{
		Worktree:     a.worktree,
		TargetBranch: a.outcome.Integration.TargetBranch,
		SourceCommit: a.outcome.Integration.SourceCommit,
	})
	a.outcome.WorktreeRemoved = cleanup.WorktreeRemoved
	a.outcome.BranchRemoved = cleanup.BranchRemoved
	a.state.WorktreeRemoved = cleanup.WorktreeRemoved
	a.state.BranchRemoved = cleanup.BranchRemoved
	if err != nil {
		return fmt.Errorf("clean up integrated run artifacts: %w", err)
	}
	// Cleanup finished, so the run is complete whatever happens to the record of
	// it. The reported phase follows that fact rather than the write below.
	a.state.Phase = runstate.PhaseComplete
	a.state.UpdatedAt = a.pipeline.clock().Now()
	a.outcome.Phase = a.state.Phase
	if err := a.pipeline.Store.Save(a.state); err != nil {
		// An interrupted write that recovers must leave a clean terminal record,
		// not a cleanup warning about artifacts that are already gone.
		a.state.UpdatedAt = a.pipeline.clock().Now()
		if retryErr := a.pipeline.Store.Save(a.state); retryErr != nil {
			return completionRecordingFailure{cause: fmt.Errorf(
				"save completed run state after cleanup: %w", errors.Join(err, retryErr))}
		}
	}
	return nil
}

// completionRecordingFailure is a run whose artifacts are all gone and whose
// record of that would not save. It is its own error type because it is the one
// cleanup failure that is not an outstanding artifact: there is nothing left to
// remove and nothing for anybody to do by hand, only a terminal record that a
// later process has to write.
type completionRecordingFailure struct{ cause error }

func (e completionRecordingFailure) Error() string { return e.cause.Error() }

func (e completionRecordingFailure) Unwrap() error { return e.cause }

// stop turns a stopped step into the outcome the run reports. Two things it can
// be handed are deliberately not failures, because both leave the run in flight
// with its worktree, branch, claimed work item, and developer session preserved:
// a usage-limit pause, which a later invocation resumes once the recorded
// deadline passes, and a provider the harness stopped on time, which a later
// invocation resumes straight away.
func (a *activeRun) stop(ctx context.Context, cause error) (Outcome, error) {
	var paused usageLimitPause
	if errors.As(cause, &paused) {
		return a.pause(paused)
	}
	var stopped providerStop
	if errors.As(cause, &stopped) {
		return a.pauseForProviderStop(stopped)
	}
	var directed directivePause
	if errors.As(cause, &directed) {
		return a.pauseForDirective(directed)
	}
	var waiting dependencyPause
	if errors.As(cause, &waiting) {
		return a.pauseForDependency(waiting)
	}
	var unanswered trackerPause
	if errors.As(cause, &unanswered) {
		return a.pauseForTracker(unanswered)
	}
	var operatorHeld operatorHoldPause
	if errors.As(cause, &operatorHeld) {
		return a.pauseForOperatorHold(operatorHeld)
	}
	// A usage window past the maximum pause is neither a pause nor a failure: the
	// harness will not wait for it, and nothing about the work was judged, so the
	// run ends cancelled with its claim given back.
	var windowed usageWindowStop
	if errors.As(cause, &windowed) {
		return a.endOnUsageWindow(windowed)
	}
	// An escalation is the one ending here that is neither a pause nor a failure
	// of anything. The role that raised it did its job and the run did too: what
	// it produced is a decision for the development manager rather than a change,
	// so it ends successfully with nothing integrated and its item parked.
	var raised escalationRaised
	if errors.As(cause, &raised) {
		return a.escalate(ctx)
	}
	// A stop the hosting watch session made to restart into a build deployed
	// over it is the harness's own clock again, and is read off the context's
	// cause rather than the error: every step between the cancelled process and
	// here reports it as the step failing, and what it is instead is a run owed a
	// continuation by the session that comes back.
	if drained, forRedeploy := drainedForRedeploy(ctx); forRedeploy {
		return a.pauseForRedeploy(drained)
	}
	// A stop the operator asked for is the one ending here that is not a pause and
	// not a failure of the work. It is recorded as cancelled, which is exactly
	// what a run this process cancelled itself is recorded as, so what it leaves
	// behind reads the same to reconciliation whichever way the stop arrived.
	var stoppedByOperator operatorStop
	if errors.As(cause, &stoppedByOperator) {
		outcome, err := a.fail(cause, runstate.StatusCancelled)
		// A stop the development manager decided leaves a stoppage she has already
		// settled, so it is docketed with her decision closing it: it reads as
		// decided rather than as one more run waiting on her. An operator's stop
		// hands nobody a decision and is docketed nowhere, as it always was.
		if strings.TrimSpace(stoppedByOperator.request.Decision) != "" && a.pipeline.Docket != nil {
			if docketErr := a.pipeline.Docket.RecordDecidedStop(a.state, stoppedByOperator.request); docketErr != nil {
				err = errors.Join(err, fmt.Errorf("docket the stop the development manager decided: %w", docketErr))
			}
		}
		return outcome, err
	}
	return a.fail(cause, failureStatus(ctx, cause))
}

// escalationRaised is a developer or a reviewer having said the work item cannot
// be met as it stands. It is an error only in the sense that it unwinds the run —
// it carries no message of its own, because what was said is on the run's record
// in the raiser's own words and every reader of it goes there.
//
// It is a type rather than a sentinel value for the reason every other ending
// here is: what a run does about it is decided by matching the type, and a
// sentinel wrapped in a formatted error is one a later `fmt.Errorf` can hide.
type escalationRaised struct{}

func (escalationRaised) Error() string {
	return "a role raised this work item as one that cannot be met as it stands"
}

// escalate ends a run either role escalated: the item goes back to the backlog
// parked, the escalation is docketed for the development manager, and the run is
// recorded as having succeeded at what it was for.
//
// It is a separate ending from `complete` rather than a branch inside it, and the
// difference is what each is for. Completing settles an item whose promotion is
// where it is going to stay; this settles an item nothing was promoted for. Every
// step `complete` takes that still applies is taken here in the same order — the
// outcome onto the item, the item's disposition, the price — and the two it does
// not are the closure and the cleanup, because there is nothing to close the item
// against and nothing integrated to clean up after.
//
// The run succeeds. It spent the review the verb was raised in, charged the item
// no round for it, and produced exactly what the verb is for, and recording it
// as a failure would put honesty about an
// unmeetable item into the same count as a broken toolchain — which is the
// failure-storm brake counting the one thing it must not.
//
// What it leaves standing is the worktree and the branch. Whatever the developer
// had written is in them, and the development manager deciding to replan or
// redirect is the reader most likely to want it; the sweep that retires an
// unintegrated run's artifacts is what settles them afterwards, exactly as it
// does for a stopped run.
func (a *activeRun) escalate(ctx context.Context) (Outcome, error) {
	p := a.pipeline
	// The disposition is decided before the outcome is recorded, for the reason
	// `complete` decides it there: the notes name where the item went, and they are
	// written first. An escalation carries no impediment by contract, so this only
	// ever reads the item — which is what the parking it must not lose comes from.
	arranged, item, err := arrangeUndischarged(ctx, p.Tracker, a.state)
	if err == nil {
		a.applyUndischargedDisposition(arranged)
	}
	if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		_, err := p.Tracker.RecordOutcome(ctx, a.state.WorkItemID, renderOutcomeNotes(a.outcome))
		return err
	}); err != nil {
		return a.fail(fmt.Errorf("record the escalated run's outcome: %w", err), runstate.StatusFailed)
	}
	// The item goes back parked. It is never closed and never left bare: the
	// decision is the development manager's and is not made yet, so an item back in
	// the queue unheld is one the next pull selects for another run of the work a
	// role has just said cannot be done.
	if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		return reopenUndischarged(ctx, p.Tracker, a.state, item)
	}); err != nil {
		return a.fail(fmt.Errorf("park the work item this run escalated: %w", err), runstate.StatusFailed)
	}
	a.recordPrice()
	completedAt := p.clock().Now()
	// A recorded stop or park is an instruction to continue later, and this run has
	// finished, exactly as it has when `complete` clears the same two.
	a.state.ProviderStop = ""
	a.state.RedeployStop = nil
	a.state.OperatorHeldSince = nil
	a.state.StopClass = runstate.StopEscalated
	a.outcome.StopClass = a.state.StopClass
	a.state.Status = runstate.StatusSucceeded
	a.state.Phase = runstate.PhaseComplete
	a.state.UpdatedAt = completedAt
	a.state.CompletedAt = &completedAt
	// The definition has no outcome for an escalation, so the instance is standing
	// in whichever state the run left, and this says so on the record the terminal
	// write is about to carry. Without it the soak would count an escalated run as
	// one that walked the definition to the end.
	a.observeUnfinished()
	if err := p.Store.Save(a.state); err != nil {
		return a.fail(fmt.Errorf("save the escalated run state: %w", err), runstate.StatusFailed)
	}
	a.outcome.Status = a.state.Status
	a.outcome.Phase = a.state.Phase
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	// Docketed after the terminal write, exactly as a stoppage is, so the entry a
	// development manager reads describes a run whose record already says how it
	// ended. A write that failed fails the run: an escalation nobody was told is
	// the silence the whole verb exists to end, and a failed run at least says so
	// where somebody is looking — and dockets itself as a preserved death, since
	// the worktree and the branch are still standing.
	if p.Docket != nil {
		if _, err := p.Docket.RecordEscalation(a.state); err != nil {
			return a.fail(fmt.Errorf("docket the escalation this run raised: %w", err), runstate.StatusFailed)
		}
	}
	return a.outcome, nil
}

// pause reports a run left waiting. Nothing is cleaned up and nothing is made
// terminal; the deadline written before the wait began is what a later
// invocation resumes from, so the run survives this process exiting.
func (a *activeRun) pause(paused usageLimitPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	a.outcome.UsageLimitKind = paused.kind
	a.outcome.PauseCause = paused.cause
	a.outcome.ProviderOutageChannel = a.state.ProviderOutageChannel
	resetsAt := paused.resetsAt
	a.outcome.UsageLimitResetsAt = &resetsAt
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderUsageLimitPauseNotes(a.outcome)); err != nil {
		// The pause itself is already durable, so a note that could not be
		// written costs the run nothing: it stays claimed and resumable either
		// way. It is still reported, because an operator watching the tracker
		// would otherwise see the item simply stop moving.
		return a.outcome, fmt.Errorf("record the pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForProviderStop reports a run whose provider the harness stopped on time.
// It is the twin of pause: the stop was made durable before this point, nothing
// is cleaned up, and nothing is made terminal, so the change the stopped
// invocation had already made stays where the next one continues it.
func (a *activeRun) pauseForProviderStop(stopped providerStop) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	a.outcome.ProviderStop = stopped.reason
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderProviderStopNotes(a.outcome)); err != nil {
		// The stop is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason a pause is.
		return a.outcome, fmt.Errorf("record the stopped provider invocation on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForDirective reports a run held up by an unresolved user directive. It is
// the third of the pauses and behaves exactly as the other two: the pause was
// made durable before this point, nothing is cleaned up, and nothing is made
// terminal, so the change the run has already produced stays where the next
// attempt continues it. What differs is only what lifts it — somebody answering
// the question or deciding the artifact change, rather than a clock.
func (a *activeRun) pauseForDirective(paused directivePause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	held := paused.directive
	a.outcome.PausedByDirective = &held
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderDirectivePauseNotes(a.outcome, held)); err != nil {
		// The pause is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason the other two
		// pauses report it: an operator watching the tracker would otherwise see
		// the item simply stop moving.
		return a.outcome, fmt.Errorf("record the directive pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForDependency reports a run held up by work its item waits on. It behaves
// exactly as the directive pause it sits beside: the pause was made durable
// before this point, nothing is cleaned up, and nothing is made terminal, so the
// change the run has already produced stays where the next attempt continues it.
// What differs is only what lifts it — the work it waits on being closed or
// unlinked, rather than somebody settling a question.
func (a *activeRun) pauseForDependency(paused dependencyPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	waiting := runstate.DependencyPause{Blockers: paused.blockers}
	a.outcome.PausedByDependency = &waiting
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderDependencyPauseNotes(a.outcome, waiting)); err != nil {
		// The pause is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason the other
		// pauses report it: an operator watching the tracker would otherwise see
		// the item simply stop moving.
		return a.outcome, fmt.Errorf("record the dependency pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForTracker reports a run parked because the tracker did not answer a read
// it makes at a gate boundary. It behaves exactly as the dependency pause it
// sits beside: the park was made durable before this point, nothing is cleaned
// up, and nothing is made terminal, so the change the run has already produced
// stays where the next attempt continues it. What differs is only what lifts it —
// the store answering, rather than the work it waits on being closed.
func (a *activeRun) pauseForTracker(paused trackerPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	unanswered := paused.paused
	a.outcome.PausedByTracker = &unanswered
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderTrackerPauseNotes(a.outcome, unanswered)); err != nil {
		// The park is already durable, so a note that could not be written costs
		// the run nothing — and here it is the likeliest outcome of all, since what
		// parked the run is the same store this note goes to. It is still reported,
		// for the same reason the other pauses report it.
		return a.outcome, fmt.Errorf("record the tracker pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForOperatorHold reports a run parked because the operator holds harness
// activity. It is the fourth of the pauses and behaves exactly as the other
// three: the park was made durable before this point, nothing is cleaned up, and
// nothing is made terminal, so the change the run has already produced stays
// where the next attempt continues it. What differs is only what lifts it, which
// is the operator rather than a clock or a resolved directive.
func (a *activeRun) pauseForOperatorHold(paused operatorHoldPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	held := paused.hold
	a.outcome.PausedByOperator = &held
	a.outcome.PauseCause = runstate.PauseOperatorHold
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderOperatorHoldNotes(a.outcome, held)); err != nil {
		// The park is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason the other
		// pauses report it.
		return a.outcome, fmt.Errorf("record the operator's pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// fail records a terminal run failure everywhere it has to be visible: the
// durable state, the reported outcome, and the work item when the run holds it.
func (a *activeRun) fail(cause error, status runstate.Status) (Outcome, error) {
	p := a.pipeline
	// A failure that names an environmental cause is recorded as one here, which
	// is where a cause with no refusal site of its own reaches the record: a
	// worktree that could not be cut from the primary checkout, a check the
	// machine would not start. A refusal site that already recorded one knew more
	// than the error does and is left alone; a settled record belongs to a round
	// that has already ended, so it does not stand in the way of this one.
	//
	// What is recorded here is never "nothing ran", because the error alone cannot
	// say so. The same sentinels arrive both before a round could deliver anything
	// and long after one did — a checkout the harness does not own refuses a
	// promotion as well as a worktree — so a round classified from here is measured
	// against the worktree like any other. The sites that do know say so
	// themselves.
	if a.state.Environmental == nil || a.state.Environmental.Settled {
		if named, environmental := environmentalCauseOf(cause); environmental {
			a.recordEnvironmentalRefusal(named, cause.Error(), ranAnyway)
		}
	}
	// Which gate stopped the run is decided here, once the environment has been
	// asked and before the round is settled, because settling is what marks the
	// environment's record as belonging to a round that is over. It goes on the
	// record and the outcome together, so the two cannot name different gates.
	a.state.StopClass = a.classifyStop(cause, status)
	a.outcome.StopClass = a.state.StopClass
	// An approved change the environment stopped short of its promotion is
	// recorded as exactly that, so what resumes it reads the classification off
	// the record rather than deciding it from the failure's prose afterwards. It
	// is asked here, of the error that ended the run, because here is the only
	// place the sentinel that names the cause still exists.
	a.recordIntegrationStop(cause)
	// This is where a round settles, so it is where the environment refusing one
	// is decided and paid back. It happens before the terminal write below, so the
	// record that ends the run carries the classification, and before the docket
	// write after it, so the entry a development manager reads reports the budgets
	// as this leaves them rather than as the round spent them.
	a.settleEnvironmentalRound()
	// Cut to the record's own bound as it is taken rather than as it is stored: an
	// error that ends a run can carry a provider's whole output, and a terminal
	// record the store refuses for its length is a run whose ending reaches
	// nobody. The outcome carries the same words the record does, so the note on
	// the work item and the record say the same thing about why it stopped.
	message := runstate.RecordFailure(cause.Error())
	completedAt := p.clock().Now()
	// A recorded pause or stop is an instruction to resume later, and this run is
	// ending now. Clearing all six keeps the terminal record coherent; what
	// stopped the run is still named by the recorded limit kind and by the
	// failure, and what it spent waiting stays on the record either way. The
	// one exception is the cause of a capacity wait the run refused to take
	// (keptPauseCause).
	a.state.UsageLimitResetsAt = nil
	a.state.UsageLimitPausedSince = nil
	a.state.UsageLimitResetUnknown = false
	a.state.PauseCause = keptPauseCause(a.state.PauseCause, a.state.StopClass)
	a.state.ProviderStop = ""
	a.state.RedeployStop = nil
	a.state.DirectivePause = nil
	a.state.DependencyPause = nil
	a.state.TrackerPause = nil
	a.state.OperatorHeldSince = nil
	a.state.Status = status
	a.state.UpdatedAt = completedAt
	a.state.CompletedAt = &completedAt
	a.state.Failure = message
	// This is every terminal *this process* records that is not the one cleanup
	// ends. An observed run whose instance is still standing in a state left by a
	// route the definition cannot express, and the record about to be written is
	// this process's last chance to say so.
	//
	// It is not every terminal the run can reach: a process that dies never gets
	// here, and its run is made terminal by a sweep instead. That ending records
	// the same divergence in the same words, from Reconciler.noteUnfinishedObservation.
	a.observeUnfinished()
	if saveErr := p.Store.Save(a.state); saveErr != nil {
		cause = errors.Join(cause, fmt.Errorf("save failed run state: %w", saveErr))
		// Only a state the store refuses is salvaged, and never a store that could
		// not be written. The second leaves an interrupted run behind, which is what
		// it is and what reconciliation exists to settle; ending it here would take
		// a resumable run away from the process that comes back for it.
		var refused runstate.RefusedStateError
		if errors.As(saveErr, &refused) {
			if endingErr := a.recordEndingAfterRefusedSave(status, completedAt, refused); endingErr != nil {
				cause = errors.Join(cause, endingErr)
			}
		}
	}
	a.outcome.Status = status
	a.outcome.Phase = a.state.Phase
	a.outcome.Failure = message
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	// Asked before the note is written and never after, because the note is the
	// only thing anybody reads about a failed run's remains and it must not
	// promise a checkout on the strength of no removal having been recorded.
	a.outcome.Preservation = a.verifyPreservation()
	if a.claimed {
		recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, recordErr := p.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderFailureNotes(a.outcome))
		cancel()
		if recordErr != nil {
			cause = errors.Join(cause, fmt.Errorf("record failed run outcome: %w", recordErr))
		}
	}
	// A preservation that did not happen is reported rather than left to whoever
	// reads the note: the run is failing either way, and an artifact this run made
	// that is already gone is the one thing about its ending nobody can recover
	// from later.
	if a.outcome.Preservation != nil && a.outcome.Preservation.Lost() {
		cause = errors.Join(cause, errors.New(preservationLoss(*a.outcome.Preservation)))
	}
	// A run that stopped on something a person has to decide reaches the
	// development manager by being docketed as it ends, rather than by an
	// operator noticing the item went quiet. The write is keyed to this
	// stoppage, so a later sweep that walks past the same run adds nothing.
	if a.pipeline.Docket != nil {
		if _, err := a.pipeline.Docket.RecordStoppedRun(a.state); err != nil {
			cause = errors.Join(cause, fmt.Errorf("docket the stopped run: %w", err))
		}
		// And the one failure that leaves nothing at all behind. A run that died
		// before it claimed its item has no blocker and no branch, so every other
		// rule that decides a failure is worth somebody's attention reads it as
		// nothing having happened — which is how twenty-nine dispatches of one item
		// died in twenty hours with no surface saying so.
		if _, err := a.pipeline.Docket.RecordUnstartedRun(a.state); err != nil {
			cause = errors.Join(cause, fmt.Errorf("docket the run that died before it started: %w", err))
		}
	}
	// A failed attempt spent real money, so it is priced exactly as a successful
	// one is. An item priced only by the run that finished it would be recorded
	// at less than it cost, which is the whole reason the price is per item.
	a.recordPrice()
	return a.outcome, cause
}

// maxRefusedRecordFailureBytes keeps the failure a salvaged record carries to a
// readable line. It is bounded rather than joined whole because one reason a
// record is refused is that it is too big to store, and a salvage that carried
// the same bytes back would be refused for the same reason.
const maxRefusedRecordFailureBytes = 1024

// recordEndingAfterRefusedSave writes the smallest true record of a run whose
// own terminal state the durable schema refused.
//
// A refused save leaves on disk whichever earlier state validated, and for a run
// ending here that is a snapshot of a run still in flight. Everything that reads
// the run after this process exits reads that snapshot rather than the outcome
// this process returns — `yoyo status`, reconciliation, the triage docket, cost
// attribution — so a run that was reviewed and blocked reads back afterwards as
// a run nothing ever judged. The run has already failed; what must not also
// happen is that it reads as one nothing ever ended.
//
// This is only for the refusal that no later write closes. A store that could not
// be written is left alone: the interrupted run it leaves is a true record of a
// process that stopped, and something comes back for it.
//
// So the ending is carried onto the record that is actually there rather than
// onto the one the store refused, and the failure names what cost it. The
// evidence the refused record held is lost from the record, not from the run:
// the work item carries the blocker and the docket entry carries the findings,
// and the caller writes both from this process's own state.
//
// Refused again, there is nothing further to try. The run reports both refusals,
// and what is left on disk is an interrupted run for reconciliation to settle —
// which is the one reading of it that is not a lie.
func (a *activeRun) recordEndingAfterRefusedSave(status runstate.Status, completedAt time.Time, refused error) error {
	p := a.pipeline
	durable, err := p.Store.Load(a.state.RunID)
	if err != nil {
		return fmt.Errorf("load the run state the refused save left behind: %w", err)
	}
	// The resume markers are cleared for the reason the refused record cleared
	// them: each is an instruction to continue a run that is now over.
	durable.UsageLimitResetsAt = nil
	durable.UsageLimitPausedSince = nil
	durable.UsageLimitResetUnknown = false
	durable.PauseCause = keptPauseCause(durable.PauseCause, a.state.StopClass)
	durable.ProviderStop = ""
	durable.RedeployStop = nil
	durable.DirectivePause = nil
	durable.DependencyPause = nil
	durable.TrackerPause = nil
	durable.OperatorHeldSince = nil
	durable.Status = status
	durable.UpdatedAt = completedAt
	durable.CompletedAt = &completedAt
	durable.StopClass = a.state.StopClass
	durable.Failure = singleLine(fmt.Sprintf("%s (this record carries the run's ending and not its evidence, because the run's own state could not be stored: %s)",
		a.state.Failure, refused), maxRefusedRecordFailureBytes)
	if err := p.Store.Save(durable); err != nil {
		return fmt.Errorf("record the run's ending over the state the store refused: %w", err)
	}
	return nil
}

// maxCostProblemBytes keeps a price that could not be recorded to a readable
// line of the outcome.
const maxCostProblemBytes = 512

// recordPrice puts what this work item has cost onto the item, aggregated
// across every run made for it rather than only this one.
//
// Nothing here may fail the run. The spending already happened and the run is
// already over; a price nobody could write down is a fact for the operator
// rather than a reason to recast a finished run as a failed one. It is given its
// own bounded context for the same reason recording a failure is: the run's
// context is often already cancelled by the time it ends, and what it cost is
// worth recording anyway.
func (a *activeRun) recordPrice() {
	if a.pipeline.Prices == nil || !a.claimed {
		return
	}
	// The price is written to the tracker like the outcome and the closure, so it
	// is waited out on the same boundary and the same window. Each attempt gets
	// its own deadline rather than sharing one: a bound that started before the
	// wait would leave every attempt after the first with no time to be made in.
	var cost *beads.Cost
	err := a.recovering(context.Background(), runstate.RetryTrackerWrite, func(ctx context.Context) error {
		priceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		priced, priceErr := a.pipeline.Prices.Record(priceCtx, a.state.WorkItemID)
		if priced != nil {
			cost = priced
		}
		return priceErr
	})
	if cost != nil {
		a.outcome.Cost = cost
	}
	if err != nil {
		a.outcome.CostProblem = singleLine(err.Error(), maxCostProblemBytes)
	}
}

// sink persists one normalized event and the progress it represents.
func (a *activeRun) sink(event execution.Event) error {
	if err := a.pipeline.Store.AppendEvent(event); err != nil {
		return err
	}
	a.state.LastSequence = event.Sequence
	a.state.UpdatedAt = event.Timestamp
	return a.pipeline.Store.Save(a.state)
}

// recordWorktree records the created worktree, including the integration target
// fixed with it, so a later process promotes the work into the branch it was
// written against rather than one it has to infer.
func (a *activeRun) recordWorktree(worktree gitworktree.Worktree) {
	a.worktree = worktree
	a.state.WorktreePath = worktree.Path
	a.state.Branch = worktree.Branch
	a.state.BaseCommit = worktree.BaseCommit
	a.state.TargetBranch = worktree.TargetBranch
	a.outcome.WorktreePath = worktree.Path
	a.outcome.Branch = worktree.Branch
	a.outcome.BaseCommit = worktree.BaseCommit
}

// ChangeLifter applies a change an earlier run of the item left on its branch
// into a fresh worktree, uncommitted. It is satisfied by *gitworktree.Manager,
// and asked for only by a run whose selection names a lift, so a manager that
// does not offer it costs every other run nothing.
type ChangeLifter interface {
	LiftChange(ctx context.Context, worktree gitworktree.Worktree, branch string) (gitworktree.Lift, error)
}

// liftPreserved starts this run from the preserved change its selection names,
// before its developer is invoked, and records the commit it started from.
//
// A lift that could not be made ends the run rather than going on without it.
// The run was started to carry that change forward, and a developer handed an
// empty worktree in its place would re-derive it or deliver nothing, which is
// the failure a fresh run over a preserved change always had. The one ending
// that is not a failure is a branch carrying nothing past the target: there is
// no change to start from, so the run starts from the target as any run does.
func (a *activeRun) liftPreserved(ctx context.Context) error {
	selection := a.state.Selection
	if selection == nil || selection.Lift == nil {
		return nil
	}
	lift := *selection.Lift
	lifter, ok := a.pipeline.Worktrees.(ChangeLifter)
	if !ok {
		return fmt.Errorf("this run was selected to start from run %s's change on %s, and the worktree manager it was given cannot lift one", lift.RunID, lift.Branch)
	}
	lifted, err := lifter.LiftChange(ctx, a.worktree, lift.Branch)
	if errors.Is(err, gitworktree.ErrNothingToLift) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start from run %s's preserved change on %s: %w", lift.RunID, lift.Branch, err)
	}
	a.state.LiftedCommit = lifted.Commit
	return nil
}

// prepareScratch cuts this run the directory its contract names, before the
// contract that names it is built. It is done once per process that serves the
// run rather than once per run: creating it is idempotent, and a run resumed by
// a second process has to find the directory there whether or not the first
// process got that far.
//
// A failure here ends the run rather than being carried. The alternative is a
// contract naming a directory that is not there, which is the run inventing
// somewhere else to write — and somewhere else is the shared temporary directory
// this exists to keep runs out of.
func (a *activeRun) prepareScratch() error {
	directory, err := execution.PrepareScratchDirectory(a.pipeline.Repository, a.worktree.Path, a.state.RunID)
	if err != nil {
		return err
	}
	a.scratch = directory
	return nil
}

// verifyPreservation looks at what is actually left of this run's branch and
// checkout, so the failure note it is about to write says work is preserved only
// where the repository agrees.
//
// A run that made neither returns nothing, which is not the same answer as work
// that is gone: a bootstrap failure before the worktree existed preserved
// nothing and must not be rendered as having lost anything.
//
// Neither is an artifact this run's own cleanup already removed. That removal is
// earned — the work is in the target branch and the record says so — and looking
// for the artifact would find it absent and report the integrated change as
// lost. So the record's removal flags are carried through and the repository is
// only asked about what they leave open; a run that cleaned up both artifacts
// asks nothing at all.
//
// It is given its own deadline for the reason recording the outcome and the
// price are: a run's own context is usually already cancelled by the time it
// ends, and a check that could not be made is what the note has to say rather
// than a reason to claim preservation anyway. Nothing here can fail the run —
// the run has already failed — so an unreadable repository becomes the recorded
// reason the check could not be made.
func (a *activeRun) verifyPreservation() *Preservation {
	if a.state.Branch == "" && a.state.WorktreePath == "" {
		return nil
	}
	preservation := &Preservation{
		Branch:          a.state.Branch,
		BranchRemoved:   a.state.BranchRemoved,
		WorktreePath:    a.state.WorktreePath,
		WorktreeRemoved: a.state.WorktreeRemoved,
	}
	if !preservation.checkable() {
		return preservation
	}
	if a.pipeline.Worktrees == nil {
		preservation.Unverified = "this process has no repository access to check them with"
		return preservation
	}
	observeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	observed, err := a.pipeline.Worktrees.Observe(observeCtx, worktreeOf(a.state))
	if err != nil {
		preservation.Unverified = err.Error()
		return preservation
	}
	preservation.BranchPresent = observed.BranchExists
	preservation.WorktreePresent = observed.WorktreePresent
	return preservation
}

// recordChanges keeps the account of what the run has changed, in the outcome
// its caller reads and in the durable record that outlives the worktree it
// describes. The two are set together so they can never disagree, and the
// durable one is what an operator is shown afterwards: cleanup removes the
// worktree and the branch, and nothing can be diffed out of a tree that is gone.
func (a *activeRun) recordChanges(summary gitworktree.ChangeSummary) {
	a.outcome.Changes = summary
	a.state.Changes = runstate.RecordChanges(summary.Status, summary.DiffStat)
}

// recordHarnessCommit names the commit the harness just made in the worktree.
// Every later inspection of that worktree accepts this exact commit and no
// other, so it is held in the durable state a resumed run rebuilds from as well
// as in the worktree this process is holding.
func (a *activeRun) recordHarnessCommit(commit string) {
	a.worktree.HarnessCommit = commit
	a.state.HarnessCommit = commit
}

// reportCompletionRecordingFailure covers a run whose artifacts were all
// removed but whose final completion record could not be written. Nothing is
// outstanding to clean up, so this must never be described as an incomplete
// cleanup. The durable state keeps the pre-cleanup marker, and resolving it
// costs nothing: a resumed cleanup over absent artifacts is a safe no-op.
func (p Pipeline) reportCompletionRecordingFailure(state runstate.State, outcome Outcome, cause error) (Outcome, error) {
	outcome.CompletionRecordingFailure = cause.Error()
	outcome.StopClass = runstate.StopRecording
	state.StopClass = outcome.StopClass
	// A further write is attempted with the failure on it. The store just
	// refused this record twice, so this is best effort — but when it lands,
	// the terminal record is whole and carries why it was late, which is the
	// only durable home this failure class has: the work-item note below may
	// itself fail, and that is part of what makes this class distinct.
	state.CompletionRecordingFailure = outcome.CompletionRecordingFailure
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		outcome.CompletionRecordingFailure = errors.Join(cause, fmt.Errorf("record the completion problem in the run record: %w", err)).Error()
	}
	notesCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(notesCtx, outcome.WorkItemID, renderCompletionRecordingNotes(outcome)); err != nil {
		outcome.CompletionRecordingFailure = errors.Join(cause, fmt.Errorf("record the completion problem on the work item: %w", err)).Error()
	}
	return outcome, nil
}

// reportOutstandingCleanup records a post-completion problem without recasting
// a finished run as a failed one. The work is integrated, the item is closed,
// and that is already durable; what is left is a janitorial fact an operator
// and a later reconciler both need to see.
func (p Pipeline) reportOutstandingCleanup(state runstate.State, outcome Outcome, cause error) (Outcome, error) {
	outcome.CleanupFailure = cause.Error()
	state.CleanupFailure = outcome.CleanupFailure
	outcome.StopClass = runstate.StopCleanup
	state.StopClass = outcome.StopClass
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		outcome.CleanupFailure = errors.Join(cause, fmt.Errorf("record outstanding cleanup: %w", err)).Error()
	}
	notesCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(notesCtx, outcome.WorkItemID, renderCleanupNotes(outcome)); err != nil {
		outcome.CleanupFailure = errors.Join(errors.New(outcome.CleanupFailure), fmt.Errorf("record outstanding cleanup on the work item: %w", err)).Error()
	}
	return outcome, nil
}

// reviewChange obtains one independent verdict on the change. A review the
// provider declined for want of capacity was never made, so it is waited out and
// asked for again rather than ending the run: the reviewer is a provider
// invocation like the developer's, and a run stopped there loses just as much
// work. A reply nothing could read as a verdict was not a review either, and it
// is asked for once more for the same reason, in the reviewer's own session and
// told what was wrong with the reply. Nothing else an unanswered review leaves
// behind is carried forward — the retry starts from the same cleared evidence
// any review starts from.
func (a *activeRun) reviewChange(ctx context.Context) (review.Decision, error) {
	var reask *review.Reask
	for {
		// A review is a provider invocation like the developer's, so it passes the
		// same two boundaries in the same order: a run the operator stopped stops
		// here rather than buying a verdict on a change nobody is going to take.
		if err := a.stopRequested(); err != nil {
			return "", err
		}
		// A run that reached the gate while the operator was holding activity waits
		// there rather than buying one more invocation.
		if err := a.holdForOperator(ctx); err != nil {
			return "", err
		}
		responseStarted := a.pipeline.clock().Now()
		decision, reported, err := a.attemptReview(ctx, reask)
		// A review the provider refused because nobody is logged into it or nobody
		// can reach it was never made, and it is answered before anything is
		// counted for the reason a developer attempt's is: the wait spends nothing.
		if reported.providerOutage != nil && err != nil {
			if pauseErr := a.pauseForProviderOutage(ctx, *reported.providerOutage); pauseErr != nil {
				return "", pauseErr
			}
			continue
		}
		// A review the provider answered — with a verdict, a refusal, or a death
		// that reached it — is the provider answering again, and it is recorded
		// here for the reason a developer attempt records it: a run that met the
		// outage in review is the only invocation that would ever find out.
		if reviewReachedProvider(reported, err) {
			if servedErr := a.pipeline.noticeProviderServed(); servedErr != nil {
				a.outcome.ProviderOutageProblem = servedErr.Error()
			}
		}
		// A review that came back with a verdict, and with no refusal reported
		// beside it, was served on the model it asked for, which is the provider
		// saying that model's window is open. The model is the one the invocation
		// recorded asking for — the reviewer's RequestedModel, written to the run
		// by attemptReview — rather than the configuration.
		if err == nil && reported.servedCleanly {
			what := fmt.Sprintf("a review of run %s of %s", a.state.RunID, a.state.WorkItemID)
			if servedErr := a.pipeline.noticeCapacityServed(a.state.AccountAlias, a.state.ReviewModel, what); servedErr != nil {
				a.outcome.CapacityServedProblem = appendProblem(a.outcome.CapacityServedProblem, servedErr.Error())
			}
		}
		if limit, refused := refusedReviewForUsageLimit(reported.usageLimit, err); refused {
			if pauseErr := a.pauseForUsageLimit(ctx, limit); pauseErr != nil {
				return "", pauseErr
			}
			continue
		}
		// A review the provider's own servers could not serve was never made
		// either, and is waited out for the same reason and on the same budget.
		if overload, refused := refusedReviewForServerOverload(reported.serverOverload, err); refused {
			if pauseErr := a.pauseForServerOverload(ctx, overload); pauseErr != nil {
				return "", pauseErr
			}
			continue
		}
		// A review the provider killed rather than refused was not made either, and
		// it costs more to lose than a developer attempt does: the change is already
		// built, checked, and waiting on the one thing that has to happen before it
		// can be promoted. So it is asked for again against the same budget a
		// developer death spends, and a run that spends the budget here stops with
		// the provider named rather than the change.
		if transient, died := diedTransiently(reported.transientFailure, reported.processStatus, false, err); died {
			var causeErr error
			transient.Detail, causeErr = a.responseCause(domain.RoleReviewer, transient.Detail, responseStarted, a.pipeline.clock().Now())
			if causeErr != nil {
				return "", causeErr
			}
			if !a.mayRelaunch(true) {
				// Past the budget a plainly recoverable death is still not a verdict,
				// and it costs more here than anywhere: the change is built, checked,
				// and waiting on the one invocation that has to happen before it can be
				// promoted. It shares the developer's recovery window for the reason it
				// shares the developer's relaunch budget.
				retried, recoverErr := a.recoverProvider(ctx, transient)
				if recoverErr != nil {
					return "", recoverErr
				}
				if retried {
					continue
				}
				return "", a.blockOnSpentRelaunchBudget(ctx, transient, err)
			}
			if relaunchErr := a.recordRelaunch(); relaunchErr != nil {
				return "", relaunchErr
			}
			continue
		}
		// A review the harness stopped on time was never made either, and the
		// change waiting to be judged is untouched by it. Continuing that run
		// costs one more review; failing it would cost the whole change.
		if reason, stopped := providerStopReason(reported.processStatus); stopped && err != nil {
			if a.pipeline.Availability != nil {
				detail := err.Error()
				account, causeErr := a.responseCause(domain.RoleReviewer, detail, responseStarted, a.pipeline.clock().Now())
				if causeErr != nil {
					return "", causeErr
				}
				err = fmt.Errorf("%w; %s", err, strings.TrimPrefix(account, detail+"; "))
			}
			resumable, recordErr := a.recordProviderStop(reason)
			if recordErr != nil {
				return "", recordErr
			}
			if resumable {
				return "", providerStop{reason: reason}
			}
			return "", stoppedBy(runstate.ProviderStopClass(reason), err)
		}
		// A reply the verdict contract could not read at all is a failed review
		// invocation rather than a failed change: the reviewer said nothing about
		// the work, and the change waiting to be judged is untouched by it. So it
		// is asked once more, exactly as a declined review is. Two unreadable
		// replies in a row is a reviewer that cannot answer the contract and the
		// run ends on it; one is weather.
		// An approval that never said what it approves is asked for once more on the
		// same budget, and for a related reason: the reviewer answered everything
		// except the question that decides whether the item closes, and deciding it
		// from an answer nobody gave is the false closure this channel exists to
		// stop. Refusing outright would cost a built, checked, approved change its
		// whole run over one missing word.
		// An approval over a change whose test data the bound kept out, that did not
		// say which of those fixtures it accounted for, is asked again on the same
		// budget for the same reason: the change is sound, and what is missing is
		// the reviewer's statement of what the approval covered.
		//
		// The second asking is made in the reviewer's own session and tells it what
		// was wrong with the reply, because a fresh invocation told nothing new
		// tends to make the same slip again.
		if problem, unsettled := unsettledReply(err); unsettled {
			if reask == nil {
				reask = &review.Reask{SessionID: a.state.ReviewSessionID, Problem: problem}
				continue
			}
			return "", stoppedBy(runstate.StopReviewAccount, fmt.Errorf("the reviewer was asked twice and neither reply could be accepted as a verdict; the second: %w", err))
		}
		// A verdict that was actually reached is recorded against the work item
		// before it is acted on, and what it costs depends on which way it went.
		// Everything above this point produced no verdict at all — declined,
		// unserved, killed, or unreadable — and none of those is anything the item
		// was answered about.
		if err == nil {
			if countErr := a.recordReviewVerdict(ctx, decision); countErr != nil {
				return "", countErr
			}
		}
		return decision, err
	}
}

// recordReviewVerdict records against the work item's durable triage counters
// that a reviewer has answered about this developer attempt, and charges the
// item a round where the answer sent the work back. The count spans every run of
// the item, which is what makes it the figure a repair grant is truncated
// against: a run's own repair budget starts again at zero each time, so nothing
// inside a run says what the item has already cost.
//
// Four verdicts are recorded and charge nothing, and the rule they add up to is
// yoyodyne-ifd.391's: the cap counts only rounds that ended in a verdict
// requiring repair against a change that was present. An escalation is one: the
// reviewer said the item cannot be met and handed nothing back, which is no turn
// of any argument. A verdict that approved
// the change is another: the cap this feeds exists to stop an item buying the same
// argument another round, and an approval is the end of that argument rather than
// another turn of it — what happens to an approved change afterwards, a promotion
// that lost its race or a merge the forge dropped, is not the change disputing
// with its reviewer, and charging the item for it walks the item toward its cap
// on its own success. That is not hypothetical. An item whose last permitted
// round was an approval, and whose promotion then conflicted, arrived at triage
// with a decision every recorded path refused; it took an operator override and a
// fresh work item to move.
//
// A repair whose whole residue is one finding the reviewer disposed of as out of
// scope is the other, by the operator's own direction of 2026-09-05 after four
// such escalations in a week. It is the same ending with a note attached: the
// reviewer said the work is right and named one thing beside it that is not this
// change's to do, and an item that reaches its cap on that is an item the
// semantics stuck rather than the work. It is the finding's disposition that
// decides, never its severity — a single minor finding is charged like any
// other — and what counts is the reviewer's vocabulary rather than this
// pipeline's: review.TrivialResidue is where the line is drawn.
//
// It unbounds nothing, which is what makes both exclusions safe rather than
// generous, and what holds that is other budgets rather than the rounds. An
// approval sends the change to promotion rather than back to the developer, so
// the only thing that asks for another verdict inside one run is a promotion
// that lost its race and replayed. A replay that passes spends nothing, so
// those approvals are bounded by how often other work lands on the target
// rather than by a budget: each one is a race somebody else's promotion caused.
// A replay that stops on the change is what
// execution.integration_retries_before_reconciliation bounds. A trivial
// residue does send the change back, and what bounds that is the run's own repair
// budget, which is spent by the attempt whether or not the verdict that asked for
// it cost a round: a run gets execution.repair_attempts_before_replan and no
// more, however small the findings. How many runs an item gets is bounded in
// turn by budgets of its own — one repair grant and one re-run per item — each
// refused by a counter neither of these can stand in for.
//
// An uncharged verdict is recorded rather than passed over, and that is
// load-bearing. The verdict is recorded under the developer attempt that produced
// the change, so an attempt answered about twice is charged at most once, and
// that is what keeps two cases off the item's bill. A review re-asked for after
// an interrupted process re-judges an attempt already answered about. And a
// promotion that loses its race replays the same work onto where the target went
// and obtains a fresh verdict on it — which is the reviewer judging the same
// developer attempt on moved ground, and can come back as a repair. A verdict
// nothing recorded would leave that attempt looking unjudged and charge the item
// for losing a race it did not cause.
//
// A verdict on a run with no change present charges nothing either, whichever
// way it went. The reviewer was shown an empty diff — a mis-selected run, a stale
// worktree, a developer that delivered nothing — and what it said about nothing
// is not the change disputing with it; the development manager's reports of
// 2026-09-14 had rounds of that shape counting identically to real repair rounds,
// and yoyodyne-ifd.391 rules them out beside the other two. The worktree is asked
// rather than the verdict, because an empty diff is a fact about the worktree
// and the reviewer's words about one vary. A worktree that cannot be read is
// charged as though the change were there: an item charged a round it should
// have kept is visible in its counters, and one credited a round it did spend is
// a budget nothing bounds.
//
// A round is charged under this process as well as under the attempt, because
// the attempt does not say which process spent it and only the process that
// spent one may give it back. The two keys answer different questions: a run
// re-entered at the review re-judges an attempt the record already holds and
// charges nothing, and the charger is what stops the refusal that follows
// crediting this process with the round the process before it bought.
func (a *activeRun) recordReviewVerdict(ctx context.Context, decision review.Decision) error {
	attempt := runstate.RoundKey(a.state.RunID, a.state.RepairAttempts)
	counters := a.pipeline.Store.Triage()
	changed, err := a.pipeline.Worktrees.ChangedPaths(ctx, a.worktree)
	changePresent := err != nil || len(changed) > 0
	// The findings are the ones this verdict arrived with: the evidence is cleared
	// before every review and written from the reply that produced this decision,
	// so what is asked about is what the reviewer just said rather than anything an
	// earlier attempt left behind.
	// An escalation charges nothing, whatever findings it happened to carry. It is
	// not a turn of the argument the cap bounds — the reviewer said the item
	// cannot be met as it stands and handed nothing back — so under
	// yoyodyne-ifd.391's rule it is not a round, and "spends at most the round it
	// is raised in" is satisfied by spending none. Until that item it was charged,
	// which walked an item at 3 of 4 to 4 of 4 on the honest answer and refused
	// the re-run recorded once the escalation was decided.
	if !changePresent || decision == review.DecisionApprove || decision == review.DecisionEscalate ||
		(decision == review.DecisionRepair && review.TrivialResidue(a.outcome.ReviewFindings)) {
		if _, err := counters.RecordUnchargedVerdict(ctx, a.state.WorkItemID, attempt, a.pipeline.clock().Now()); err != nil {
			return fmt.Errorf("record the verdict that cost attempt %s nothing: %w", attempt, err)
		}
		return nil
	}
	charger, err := a.chargingProcess()
	if err != nil {
		return fmt.Errorf("identify the process charging the review round attempt %s produced: %w", attempt, err)
	}
	if _, err := counters.RecordReviewRound(ctx, a.state.WorkItemID, attempt, charger, a.pipeline.clock().Now()); err != nil {
		return fmt.Errorf("count the review round attempt %s produced: %w", attempt, err)
	}
	return nil
}

// providerEvidence is what an attempted invocation says about why it produced no
// answer, separately from the error it returned: a usage limit the provider
// refused it for, an overload of the provider's own servers, or the way its own
// process ended. Each decides whether the run continues, and none is legible
// from the error alone.
type providerEvidence struct {
	usageLimit       *backend.UsageLimit
	serverOverload   *backend.ServerOverload
	transientFailure *backend.TransientFailure
	providerOutage   *backend.ProviderOutage
	processStatus    execution.ProcessStatus
	// servedCleanly is a review the provider answered with a verdict, a process
	// that succeeded, and no refusal reported anywhere on it — the only review
	// that is evidence a window on its model is open. It is set on the verdict's
	// path alone; every other field here describes a review that was not made.
	servedCleanly bool
}

// reviewReachedProvider reports a review attempt the provider actually
// answered, however it answered: a verdict, a reply the contract could not
// read, a limit, an overload, or a death that reached it and dropped. Every one
// of those is the provider at the other end of the connection, which is what
// ends an outage. A review the harness stopped on time, or one refused before
// the provider was reached, says nothing either way.
func reviewReachedProvider(reported providerEvidence, err error) bool {
	if reported.providerOutage != nil {
		return false
	}
	if err == nil || reported.usageLimit != nil || reported.serverOverload != nil || reported.transientFailure != nil {
		return true
	}
	_, unsettled := unsettledReply(err)
	return unsettled
}

// unsettledReply reports a review the reviewer answered with a reply the harness
// could not settle a verdict from — unreadable, an approval that never said what
// it approves, or one that never said which kept-out fixtures it covered — and
// says what was wrong with it in the words of the refusal, which is what the
// reviewer is told when it is asked again.
func unsettledReply(err error) (string, bool) {
	var undecodable review.UndecodableVerdictError
	if errors.As(err, &undecodable) {
		return "it is not one JSON object in the verdict's schema (" + undecodable.Error() + ")", true
	}
	var incomplete review.IncompleteApprovalError
	if errors.As(err, &incomplete) {
		return incomplete.Error(), true
	}
	var unaccounted review.UnaccountedFixturesError
	if errors.As(err, &unaccounted) {
		return unaccounted.Error(), true
	}
	return "", false
}

// refusedReviewForUsageLimit reports a review the provider declined for want of
// capacity. A limit reported by a review that still produced a verdict is
// evidence rather than a refusal, exactly as it is for a developer attempt.
func refusedReviewForUsageLimit(limit *backend.UsageLimit, err error) (backend.UsageLimit, bool) {
	if limit == nil || err == nil {
		return backend.UsageLimit{}, false
	}
	return *limit, true
}

// reviewedContext is the work-item context the reviewer judges the change
// against, with every file the item references read at the change's recorded
// base and labelled with that commit.
//
// The developer's context was read from the checkout when the item was claimed,
// which was the base then. By the time a review is asked for the checkout may
// hold a later revision — anything else promoted meanwhile moves it — and a
// document read there beside a patch measured against the older base makes a
// correct change read as a divergent one: yoyodyne-ifd.117.3 spent three repair
// rounds on an extraction judged against docs/configuration.md as main had it
// rather than as the branch's base did.
func (a *activeRun) reviewedContext(ctx context.Context, baseCommit string) (string, error) {
	p := a.pipeline
	// A change that names no base has nothing to read at, and its evidence says
	// so; the context the developer was given is what there is.
	if baseCommit == "" {
		return a.context, nil
	}
	revision := reviewedRevision(ctx, p.Worktrees, baseCommit)
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: a.worktree.Path, WorkItem: a.item, Revision: revision, Specifications: p.Config.Product.Specifications, IntentRoot: p.Config.Product.IntentRoot(a.worktree.Path)})
	if err != nil {
		return "", fmt.Errorf("assemble reviewed work item context at %s: %w", baseCommit, err)
	}
	return bundle.Text + a.dependencyEvidence(ctx), nil
}

// refusedReviewForServerOverload reports a review the provider's own servers
// could not serve, on the same rule: a review that still produced a verdict is
// evidence rather than a refusal.
func refusedReviewForServerOverload(overload *backend.ServerOverload, err error) (backend.ServerOverload, bool) {
	if overload == nil || err == nil {
		return backend.ServerOverload{}, false
	}
	return *overload, true
}

// attemptReview runs the configured independent reviewer once and records
// exactly what it decided, including when it fails or answers with something the
// verdict contract rejects. Every recorded outcome is written into the run state
// before the caller acts on it, so a stopped run still explains why it stopped.
func (a *activeRun) attemptReview(ctx context.Context, reask *review.Reask) (review.Decision, providerEvidence, error) {
	p := a.pipeline
	a.state.Phase = runstate.PhaseReviewing
	// Nothing an earlier attempt was told carries into this one. Clearing the
	// evidence before the reviewer runs is what makes a recorded verdict always
	// belong to the change that is about to be judged, so an earlier approval
	// can never authorize a later attempt.
	a.clearReviewEvidence()
	// Whatever stopped a previous invocation is spent once this one starts: a
	// stop left behind would make a running review look continuable to the next
	// process that adopts the run.
	a.state.ProviderStop = ""
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return "", providerEvidence{}, fmt.Errorf("save reviewing run state: %w", err)
	}
	changes, err := p.Worktrees.UnifiedChanges(ctx, a.worktree, gitworktree.DiffLimits{})
	if err != nil {
		return "", providerEvidence{}, fmt.Errorf("assemble reviewed change: %w", err)
	}
	// What the reviewer is about to be shown is recorded before it is shown,
	// beside the verdict it will produce: the base the change is measured
	// against and the tip it was read at. A review that fails past this point
	// still says what it was judging.
	a.state.ReviewBaseCommit = changes.BaseCommit
	a.state.ReviewHeadCommit = changes.HeadCommit
	a.outcome.ReviewBaseCommit = changes.BaseCommit
	a.outcome.ReviewHeadCommit = changes.HeadCommit
	reviewedContext, err := a.reviewedContext(ctx, changes.BaseCommit)
	if err != nil {
		return "", providerEvidence{}, err
	}
	developerSummary, summaryContext, err := a.developerSummaryForReview(ctx)
	if err != nil {
		return "", providerEvidence{}, err
	}
	account := a.account()
	result, reviewErr := p.Reviewer.Review(ctx, review.Request{
		RunID:      a.state.RunID,
		WorkItemID: a.state.WorkItemID,
		// The item as the developer was given it, with every file it references
		// read at the base the patch is measured against rather than at whatever
		// the checkout holds now.
		Context: reviewedContext,
		// The invariants reach the reviewer's evidence by the same delivery that
		// reached the developer's context, so a change that violates one is judged
		// against it whether or not the work item ever mentioned it.
		Invariants: a.reviewedInvariants(changes).Text(),
		// What the developer claimed its change does to the item, so the change is
		// judged against what it was offered as. It comes from the durable record
		// rather than from what this attempt happened to return, so a repair round
		// judges the claim the run currently holds.
		Landing: describeLanding(a.state),
		// And what the developer recorded executing against it, so the change is
		// judged beside the evidence its author left rather than on the patch
		// alone. It comes from the durable record for the reason the claim does: a
		// repair round judges what the run currently holds.
		Verification:            describeVerification(a.state),
		DeveloperSummary:        developerSummary,
		DeveloperSummaryContext: summaryContext,
		WorktreePath:            a.worktree.Path,
		Changes:                 changes,
		Repository:              reviewedRepository(ctx, p.Worktrees, changes.HeadCommit, a.item, changes),
		Checks:                  a.outcome.Checks,
		// What the item's done-conditions quote, so every line of a check's
		// output carrying one reaches the reviewer beside the check's result.
		CheckPatterns: review.CriterionPatterns(a.item.Description, a.item.AcceptanceCriteria),
		// On the second asking, the reviewer's own session and what was wrong
		// with the reply it gave there.
		Reask:        reask,
		RedactValues: p.RedactValues,
		LastSequence: a.state.LastSequence,
		EventSink:    a.sink,
		// What the reviewer's invocation spends is this run's spend, charged to
		// the review rather than to the change: an item that was reviewed four
		// times is where that distinction is the whole answer.
		Spend: a.spendAttribution(domain.RoleReviewer, runstate.SpendPhaseReview),
		// The review is made under the account the run is affined to, so what one
		// piece of work cost is what one subscription was spent.
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	})
	if result.LastSequence > a.state.LastSequence {
		a.state.LastSequence = result.LastSequence
	}
	// What the reviewer reported is collected before its verdict is read,
	// because a report survives a review the harness went on to reject: the
	// reviewer noticed the thing whatever became of the verdict beside it.
	a.collectReports(domain.RoleReviewer, result.Reports)
	if result.ReportProblem != "" {
		a.noteReportProblem(domain.RoleReviewer, errors.New(result.ReportProblem))
	}
	a.state.ReviewSessionID = result.SessionID
	a.state.ReviewModel = result.RequestedModel
	a.state.ReviewResolvedModel = result.ResolvedModel
	a.state.ReviewEffort = result.RequestedEffort
	a.state.ReviewResolvedEffort = result.ResolvedEffort
	a.state.ReviewEffortReported = result.EffortReported
	a.state.ReviewLoaded = result.Loaded.Recorded()
	a.outcome.ReviewSessionID = result.SessionID
	a.outcome.ReviewModel = result.RequestedModel
	a.outcome.ReviewResolvedModel = result.ResolvedModel
	a.outcome.ReviewResolvedEffort = result.ResolvedEffort
	a.outcome.ReviewEffortReported = result.EffortReported
	a.outcome.ReviewEffort = result.RequestedEffort
	if result.Verdict.Summary != "" {
		// Cut to the record's own bound as it is taken rather than as it is stored:
		// a reviewer writes at whatever length it likes, and a summary the docket
		// entry cannot carry is a stopped run the development manager never hears
		// about. The outcome carries the same words the record does, so what the run
		// reports and what it recorded say the same thing about the review.
		summary := runstate.RecordReviewSummary(result.Verdict.Summary)
		a.state.ReviewSummary = summary
		a.state.ReviewFindings = len(result.Verdict.Findings)
		a.state.ReviewFindingDetails = durableFindings(result.Verdict.Findings)
		a.outcome.ReviewSummary = summary
		a.outcome.ReviewFindings = result.Verdict.Findings
	}
	if result.Decision.Valid() {
		a.state.ReviewDecision = string(result.Decision)
		a.outcome.ReviewDecision = result.Decision
		// What the approval approves is recorded beside the decision, because it is
		// the reviewer's half of whether this item closes and the closure is not
		// always made by this process. It is recorded only on an approval: a repair
		// closes nothing, and the durable schema refuses a record that says
		// otherwise.
		if result.Decision == review.DecisionApprove {
			a.state.ReviewApproves = string(result.Verdict.Approves)
			a.outcome.ReviewApproves = result.Verdict.Approves
		}
		// One round with a reviewer is a verdict, whichever way it went, and this
		// is how many of them this run has had. It is counted here rather than
		// derived from the repair attempts because the two are not the same number:
		// a refused path and a failing check are handed back without anybody
		// reviewing anything, and an approved change was reviewed without a repair
		// at all. Unlike the verdict beside it, it is never cleared: what the next
		// attempt discards is the judgement, not the fact that this work has been
		// round once more.
		//
		// It is this run's own figure and not the one the triage caps are measured
		// against. That one is the item's durable counter, which spans every run
		// and excludes the verdicts that sent nothing back — see
		// recordReviewVerdict.
		a.state.ReviewRounds++
	}
	if reviewErr != nil {
		return "", providerEvidence{
			usageLimit:       result.UsageLimit,
			serverOverload:   result.ServerOverload,
			transientFailure: result.TransientFailure,
			providerOutage:   result.ProviderOutage,
			processStatus:    result.ProcessStatus,
		}, stoppedBy(runstate.StopProvider, fmt.Errorf("independent review failed: %w", reviewErr))
	}
	// The reviewer reports the selector it actually ran with. Auditing that
	// against configuration here keeps the recorded evidence a fact rather than
	// an assumption about how the reviewer was wired.
	configured := p.reviewer().Model
	if result.RequestedModel != configured {
		return "", providerEvidence{}, stoppedBy(runstate.StopProvider, fmt.Errorf("reviewer ran with model %q, configured reviewer model is %q", result.RequestedModel, configured))
	}
	return result.Decision, providerEvidence{
		servedCleanly: result.ProcessStatus == execution.ProcessSucceeded &&
			result.UsageLimit == nil && result.ServerOverload == nil && result.ProviderOutage == nil,
	}, nil
}

// developerSummaryForReview supplies the latest completed account, even after
// replay or re-adoption, and states how its binding relates to today's candidate.
// The binding qualifies testimony; it never grants check or integration credit.
func (a *activeRun) developerSummaryForReview(ctx context.Context) (string, string, error) {
	if a.state.Document != nil {
		return a.context, "The owning role wrote this document in its conversation. The harness confirmed and copied it unchanged; there is no developer.", nil
	}
	summary := a.state.DeveloperSummary
	if summary == nil {
		return "", "No final account is saved in this run's durable record. The record may predate summary retention, or no developer invocation returned a final account.", nil
	}
	content, err := a.pipeline.Worktrees.ContentIdentity(ctx, a.worktree)
	if err != nil {
		return "", "", fmt.Errorf("identify the change for the developer summary: %w", err)
	}
	context := fmt.Sprintf("Latest completed developer account: recorded at attempt %d; review is at attempt %d.", summary.Attempt, a.state.RepairAttempts)
	if summary.Attempt != a.state.RepairAttempts {
		context += " This account is from an earlier attempt."
	}
	if summary.Content != content {
		context += " The candidate content or base has changed since this account was recorded; its verification claims do not establish verification of the current candidate."
	} else {
		context += " Its recorded content and base match the candidate."
	}
	return summary.Text, context, nil
}

func (a *activeRun) clearReviewEvidence() {
	a.state.ReviewSessionID = ""
	a.state.ReviewModel = ""
	a.state.ReviewResolvedModel = ""
	a.state.ReviewEffort = ""
	a.state.ReviewResolvedEffort = ""
	a.state.ReviewEffortReported = false
	a.state.ReviewLoaded = nil
	a.state.ReviewBaseCommit = ""
	a.state.ReviewHeadCommit = ""
	a.state.ReviewDecision = ""
	a.state.ReviewApproves = ""
	a.state.ReviewSummary = ""
	a.state.ReviewFindings = 0
	a.state.ReviewFindingDetails = nil
	a.outcome.ReviewSessionID = ""
	a.outcome.ReviewModel = ""
	a.outcome.ReviewResolvedModel = ""
	a.outcome.ReviewEffort = ""
	a.outcome.ReviewResolvedEffort = ""
	a.outcome.ReviewEffortReported = false
	a.outcome.ReviewBaseCommit = ""
	a.outcome.ReviewHeadCommit = ""
	a.outcome.ReviewDecision = ""
	a.outcome.ReviewApproves = ""
	a.outcome.ReviewSummary = ""
	a.outcome.ReviewFindings = nil
}

// durableFindings converts a reviewer's findings into the durable schema. They
// are what the next repair attempt is built from and what a recorded blocker
// names, so they have to survive the process that received them.
func durableFindings(findings []review.Finding) []runstate.Finding {
	if len(findings) == 0 {
		return nil
	}
	durable := make([]runstate.Finding, 0, len(findings))
	for _, finding := range findings {
		recorded := runstate.Finding{Severity: string(finding.Severity), Disposition: string(finding.Disposition), Message: finding.Message, Absent: finding.Absent}
		if finding.Location != nil {
			recorded.File = finding.Location.File
			recorded.Line = finding.Location.Line
		}
		durable = append(durable, recorded)
	}
	return durable
}

// resumableRepair reports whether an incomplete run stopped inside its repair
// loop, which is the only interrupted run this pipeline picks up. It needs the
// worktree every attempt shares, the developer session the next attempt
// continues, a recorded attempt count, and, when an attempt was in flight, the
// repair input that attempt was given: the refused paths, the failing check, or
// the reviewer's findings. Anything else is left to reconciliation rather than
// reconstructed from guesswork.
//
// A stalled attempt being carried on is the one run here with neither of the
// last two, and the carry-out's own record is what says so. The harness stopped
// such a run before anything was returned to its developer, so what it is owed
// is the attempt it was making rather than another one, and the continuation
// counts none — which leaves a run with a grant against it and no attempt, and
// no repair input for the developing phase to recognize it by. What it is
// recognized by instead is the continuation the carry-out recorded: the
// environmental account of the stoppage is cleared as the re-entry is written,
// because a run that is going again has not stopped.
func resumableRepair(state runstate.State) bool {
	if state.Status != runstate.StatusRunning {
		return false
	}
	if state.RepairAttempts == 0 && !state.ContinuedStall() {
		return false
	}
	if state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" || state.TargetBranch == "" {
		return false
	}
	// A decided repair of a run that recorded no session starts a fresh one at
	// the developer attempt, and the continuation it recorded is what says so.
	// Nothing else here is picked up without the session its next step needs.
	if state.ProviderSessionID == "" && !(state.Phase == runstate.PhaseDeveloping && state.ContinuedInFreshSession()) {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping:
		return handedBackRepair(state) || state.ContinuedStall()
	case runstate.PhaseChecking, runstate.PhaseReviewing:
		return true
	default:
		return false
	}
}

// continuedAtCheckStage reports an in-flight run put back at its checks after
// execution.check_stage_timeout stopped the stage, either automatically or by
// a decided repair: running, at the checking phase, with a continuation on its
// record saying it was put there on purpose, and with the worktree, branch, and
// developer session its change and its review need. A run at the checks with
// none is one a process died in, and that is the sweep's to settle rather than
// this path's to adopt.
func continuedAtCheckStage(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseChecking {
		return false
	}
	if len(state.CheckStageContinuations) == 0 && !state.ContinuedCheckStage() {
		return false
	}
	if state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" || state.TargetBranch == "" {
		return false
	}
	return state.ProviderSessionID != ""
}

// resumableIntegration reports an in-flight run standing at its promotion with
// the approval that authorizes it, which is the other shape of run this
// pipeline picks up: the integration resume, made live again by the triage
// action after the environment stopped it short of the target branch. It needs
// the worktree and the branch that hold the approved change, the developer
// session the independence check pairs the reviewer's against, and the
// resumption on its record that says the harness put it here on purpose — a run
// at the integrating phase with none is one a process died in, and that is
// reconciliation's to settle rather than something to promote from durable
// state alone.
func resumableIntegration(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseIntegrating {
		return false
	}
	if state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" || state.TargetBranch == "" {
		return false
	}
	if state.ProviderSessionID == "" || len(state.IntegrationResumptions) == 0 {
		return false
	}
	return state.ApprovedAwaitingIntegration()
}

// carryReviewEvidence puts the verdict the durable record holds onto the outcome
// this process reports, for a run resumed past the review. Every step from the
// promotion on reads the outcome rather than the record — the independence check,
// the notes recorded on the item, the closure — and a resumed run that carried
// nothing would be refused as unreviewed by the first of them and recorded as
// unreviewed by the rest.
func (a *activeRun) carryReviewEvidence() {
	state := a.state
	a.outcome.ReviewSessionID = state.ReviewSessionID
	a.outcome.ReviewModel = state.ReviewModel
	a.outcome.ReviewResolvedModel = state.ReviewResolvedModel
	a.outcome.ReviewEffort = state.ReviewEffort
	a.outcome.ReviewResolvedEffort = state.ReviewResolvedEffort
	a.outcome.ReviewEffortReported = state.ReviewEffortReported
	a.outcome.ReviewBaseCommit = state.ReviewBaseCommit
	a.outcome.ReviewHeadCommit = state.ReviewHeadCommit
	a.outcome.ReviewDecision = review.Decision(state.ReviewDecision)
	a.outcome.ReviewApproves = review.Approval(state.ReviewApproves)
	a.outcome.ReviewSummary = state.ReviewSummary
	a.outcome.ReviewFindings = reportedFindings(state.ReviewFindingDetails)
	// The landing travels with the verdict, because the closure is decided from
	// both: a resumed run that lost the developer's claim would close an item the
	// developer said its change does not discharge.
	a.outcome.Landing = landing.Outcome(state.LandingOutcome)
	a.outcome.LandingReason = state.LandingReason
	a.outcome.LandingBlockedBy = state.LandingBlockedBy
	a.outcome.LandingImpedimentProblem = state.LandingImpedimentProblem
	a.outcome.LandingProblem = state.LandingProblem
}

// reportedFindings converts durable findings back into the reviewer's own
// shape, which is what the outcome carries. It is the inverse of
// durableFindings, and it is total: a durable finding with no file has no
// location, exactly as it had none when it was recorded.
func reportedFindings(findings []runstate.Finding) []review.Finding {
	if len(findings) == 0 {
		return nil
	}
	reported := make([]review.Finding, 0, len(findings))
	for _, finding := range findings {
		restored := review.Finding{Severity: review.Severity(finding.Severity), Disposition: review.Disposition(finding.Disposition), Message: finding.Message, Absent: finding.Absent}
		if finding.File != "" {
			restored.Location = &review.Location{File: finding.File, Line: finding.Line}
		}
		reported = append(reported, restored)
	}
	return reported
}

// phaseError carries the run status a failed step must be recorded with, so a
// cancelled or timed-out step reports what actually happened to it rather than
// whatever the surrounding context looks like afterwards.
type phaseError struct {
	status runstate.Status
	cause  error
}

func (e phaseError) Error() string { return e.cause.Error() }

func (e phaseError) Unwrap() error { return e.cause }

// classifiedStop carries which gate a stop happened at from the site that knew
// it to fail, which is where the run's record is written. It travels on the error
// rather than being written onto the record where it is known, because most of
// these sites return an error something above them may still answer — a relaunch,
// a re-ask, a pause — and a class written early would outlive the stop it
// described.
type classifiedStop struct {
	class runstate.StopClass
	cause error
}

func (e classifiedStop) Error() string { return e.cause.Error() }

func (e classifiedStop) Unwrap() error { return e.cause }

// stoppedBy marks cause as a stop at the gate class names. A nil cause stays
// nil, so a site can mark whatever it returns without asking first.
func stoppedBy(class runstate.StopClass, cause error) error {
	if cause == nil {
		return nil
	}
	return classifiedStop{class: class, cause: cause}
}

// classifyStop decides the class fail records. The environment and a
// cancellation are asked before anything a site said, because each is true of
// the run whichever gate it was at when it met them: a check that could not start
// because the machine would not spawn it is the environment's stop, not the
// checks'. Past those, the class the stopping site gave is the one that knew most
// when the run stopped, and a stop no site classified is the harness's own step.
func (a *activeRun) classifyStop(cause error, status runstate.Status) runstate.StopClass {
	class, classified := recordedStopIn(cause)
	if a.state.Environmental != nil && !a.state.Environmental.Settled && class != runstate.StopRecoveryWindow {
		if a.state.Environmental.Cause == runstate.CauseProcessVanished && a.state.Environmental.ProviderStop != "" {
			return runstate.ProviderStopClass(a.state.Environmental.ProviderStop)
		}
		return a.state.Environmental.Cause.StopClass()
	}
	if a.state.ApprovedAwaitingIntegration() && a.state.ReplayConflict == nil && class != runstate.StopRecoveryWindow {
		if named, environmental := integrationStopCauseOf(cause); environmental {
			return named.StopClass()
		}
	}
	var requested operatorStop
	if errors.As(cause, &requested) {
		return stopRequestClass(requested.request)
	}
	var drained RedeployDrain
	if errors.As(cause, &drained) {
		return runstate.StopRedeploy
	}
	if status == runstate.StatusCancelled {
		return runstate.StopCancelled
	}
	if classified {
		return class
	}
	return runstate.StopHarness
}

// A specific bound outranks a broader gate, whether the gate wrapped it or
// supplied the error that spent the bound. For joined errors the original
// failure comes before its recording failure.
func recordedStopIn(cause error) (runstate.StopClass, bool) {
	if cause == nil {
		return "", false
	}
	var own runstate.StopClass
	switch stopped := cause.(type) {
	case classifiedStop:
		own = stopped.class
	case runstate.StopError:
		own = stopped.Class
	case *runstate.StopError:
		own = stopped.Class
	}
	choose := func(child runstate.StopClass, found bool) (runstate.StopClass, bool) {
		if found && (own == "" || own.Gate() || !child.Gate()) {
			return child, true
		}
		return own, own != ""
	}
	if joined, ok := cause.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if class, found := recordedStopIn(child); found {
				return choose(class, true)
			}
		}
	} else {
		return choose(recordedStopIn(errors.Unwrap(cause)))
	}
	return own, own != ""
}

func stopRequestClass(request runstate.StopRequest) runstate.StopClass {
	if strings.TrimSpace(request.Decision) != "" {
		return runstate.StopManager
	}
	return runstate.StopOperator
}

func failureStatus(ctx context.Context, err error) runstate.Status {
	var phase phaseError
	if errors.As(err, &phase) {
		return phase.status
	}
	return statusForContext(ctx)
}

func (p Pipeline) automatic() bool {
	return p.Config.Approvals.Integration == domain.ApprovalAutomatic
}

// validateIndependentInvocations refuses to integrate work whose developer and
// reviewer cannot be told apart. An empty or shared session identifier means
// the second opinion the policy depends on was never demonstrated.
func validateIndependentInvocations(outcome Outcome) error {
	developer := strings.TrimSpace(outcome.ProviderSessionID)
	reviewer := strings.TrimSpace(outcome.ReviewSessionID)
	switch {
	case developer == "" || reviewer == "":
		return fmt.Errorf("integration requires recorded developer and reviewer sessions, got developer %q and reviewer %q", outcome.ProviderSessionID, outcome.ReviewSessionID)
	case developer == reviewer:
		return fmt.Errorf("integration requires an independent reviewer, but both invocations reported session %q", outcome.ProviderSessionID)
	}
	if strings.TrimSpace(outcome.ProviderModel) == "" || strings.TrimSpace(outcome.ReviewModel) == "" {
		return fmt.Errorf("integration requires recorded developer and reviewer model selectors, got developer %q and reviewer %q", outcome.ProviderModel, outcome.ReviewModel)
	}
	return nil
}

func (p Pipeline) validate() error {
	var problems []error
	if p.Tracker == nil {
		problems = append(problems, errors.New("work tracker is required"))
	}
	if p.Worktrees == nil {
		problems = append(problems, errors.New("worktree manager is required"))
	}
	if p.Store == nil {
		problems = append(problems, errors.New("state store is required"))
	}
	if p.Backend == nil {
		problems = append(problems, errors.New("agent backend is required"))
	}
	if p.Checks == nil {
		problems = append(problems, errors.New("check runner is required"))
	}
	if p.Directives == nil {
		problems = append(problems, errors.New("durable user directives are required"))
	}
	if p.Holds == nil {
		problems = append(problems, errors.New("the operator's pause on harness activity is required"))
	}
	if p.NewRunID == nil {
		problems = append(problems, errors.New("run id generator is required"))
	}
	if strings.TrimSpace(p.Repository) == "" {
		problems = append(problems, errors.New("repository is required"))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func (p Pipeline) developer() config.AgentConfig {
	return p.agentForRole(domain.RoleDeveloper)
}

func (p Pipeline) reviewer() config.AgentConfig {
	return p.agentForRole(domain.RoleReviewer)
}

func (p Pipeline) agentForRole(role domain.AgentRole) config.AgentConfig {
	for _, name := range p.agentNames() {
		agent := p.Config.Agents[name]
		if agent.Role == role {
			return agent
		}
	}
	return config.AgentConfig{}
}

// agentNames lists the configured agents in a fixed order, so the same
// configuration always resolves a role to the same agent.
func (p Pipeline) agentNames() []string {
	names := make([]string, 0, len(p.Config.Agents))
	for name := range p.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// runsOnCompiledAdapter reports a configured backend this build can actually
// launch. That is a built-in naming an adapter this build carries, and — since a
// project may declare a provider of its own — anything the project declared that
// runs on one of them: a declared provider is that adapter driving its
// executable and reading its stream with the dialect the declaration supplied,
// which is exactly what the
// pipeline wires. A backend the vocabulary has and this build ships no adapter
// for is refused here rather than started.
func (p Pipeline) runsOnCompiledAdapter(named domain.Backend) bool {
	registry, err := p.Config.ProviderRegistry()
	if err != nil {
		// A configuration whose providers will not resolve has already been
		// refused by Config.Validate above, so the only honest answer left is what
		// this build ships unconditionally.
		descriptor, shipped := backend.BuiltInDescriptor(named)
		return shipped && descriptor.Runnable()
	}
	descriptor, known := registry.Lookup(named)
	return known && descriptor.Runnable()
}

// validateReviewPolicy refuses automatic integration that is not actually
// gated. An unenforceable policy must stop the run before anything is claimed,
// rather than integrate work no independent reviewer ever saw.
//
// What it asks of the configured agent is whether its role returns the verdict an
// integration is gated on, rather than whether the role is named "reviewer". The
// two are one question today, and asking it as the capability is what keeps this
// gate and the registry from being able to disagree about which role that is.
func (p Pipeline) validateReviewPolicy() error {
	if p.Reviewer == nil {
		return errors.New("automatic integration requires an independent reviewer")
	}
	reviewer := p.reviewer()
	if !rolecapability.MustDefault().Holds(reviewer.Role, capability.ReviewVerdict) {
		return errors.New("automatic integration requires a configured reviewer agent")
	}
	if !p.runsOnCompiledAdapter(reviewer.Backend) {
		return fmt.Errorf("run pipeline requires a reviewer on a backend this build can launch, configured backend is %q", reviewer.Backend)
	}
	if err := config.ValidateModelSelector(reviewer.Model); err != nil {
		return fmt.Errorf("reviewer agent %s", err)
	}
	return nil
}

// startableStatuses are the tracker statuses a run may be started on. Blocked is
// one of them, and that is not a widening of what this gate accepts: the
// dependency reading below is what refuses a blocked item, and it refuses one
// whose blockers are unfinished whatever its status says. What the status
// answered before was the same question worse — it is written when work stops
// and never rewritten when what stopped it clears, so it went on refusing items
// whose every blocker had closed, which is how two-thirds of this backlog came
// to be unreachable on 2026-09-04.
//
// What it still refuses is an item that has left the backlog: work already
// claimed by a run, and work that is closed. Neither is something to start.
var startableStatuses = []string{"open", "blocked"}

func validateReadyItem(item beads.WorkItem, requestedID string) error {
	return validateWorkItem(item, requestedID, startableStatuses...)
}

func validateClaimedItem(item beads.WorkItem, requestedID string) error {
	return validateWorkItem(item, requestedID, "in_progress")
}

func validateWorkItem(item beads.WorkItem, requestedID string, expectedStatuses ...string) error {
	if item.ID != requestedID {
		return fmt.Errorf("Beads returned work item %q for requested id %q", item.ID, requestedID)
	}
	if !slices.Contains(expectedStatuses, item.Status) {
		return fmt.Errorf("work item %s status is %q, want %s", item.ID, item.Status, strings.Join(expectedStatuses, " or "))
	}
	if blockers := blockingDependencies(item); len(blockers) > 0 {
		return fmt.Errorf("work item %s is blocked by: %s", item.ID, strings.Join(blockers, ", "))
	}
	// Asked of a ready item and of a claimed one alike, because an item that
	// acquired the grant after it was claimed is the same wall as one that carried
	// it all along, and a run resumed into it spends the rounds a run refused here
	// does not.
	return refuseProviderGrant(item)
}

// blocksDependency is the tracker relation that makes one item wait for another.
// It is named once because more than one gate decides on it, and two gates
// spelling the relation differently would be two accounts of what "blocked"
// means that could disagree.
const blocksDependency = "blocks"

// closedStatus is finished work. It is what makes a dependency stop holding
// anything back, which is why the reading below and the resolution of a landing's
// impediment both have to agree about it: work a gate calls closed is work no
// dependency on it can hold an item for.
const closedStatus = "closed"

// blockingDependencies names the unfinished work an item waits for, in a stable
// order. It is the single reading every dependency gate in this package decides
// on: the run-start check, the gate each attempt passes through, and the refusal
// a stopped run's continuation is held to.
//
// A dependency whose own item is closed is not something anybody is waiting for,
// and a relation that is not "blocks" — a parent-child link, say — says nothing
// about whether this work may proceed.
func blockingDependencies(item beads.WorkItem) []string {
	var blockers []string
	for _, dependency := range item.Dependencies {
		if dependency.Type == blocksDependency && dependency.Status != closedStatus {
			blockers = append(blockers, dependency.ID)
		}
	}
	sort.Strings(blockers)
	return blockers
}

func (p Pipeline) clock() execution.Clock {
	if p.Clock == nil {
		return execution.RealClock{}
	}
	return p.Clock
}

// scratchDirectoryPlaceholder is where a run's own scratch directory is
// substituted into the contract below. The directory is cut per run, so the
// contract is a template rather than a constant; the token is deliberately not
// something the contract's prose could produce, so a substitution can never land
// anywhere but where it was meant to.
const scratchDirectoryPlaceholder = "{{scratch-directory}}"

// selfCheckContractPlaceholder is where the section about proving the
// environment can execute is substituted into the contract below. It is built
// per project rather than written into the template for the reason the scratch
// directory is: it names this project's own declared checks, and the token is
// deliberately not something the contract's prose could produce.
const selfCheckContractPlaceholder = "{{self-check-contract}}"

// developerContract is the harness policy every developer run carries, with this
// run's scratch directory named in it. It is a Go constant rather than
// configuration because a configured persona may specialize how a developer
// works but must never be able to remove the bounds it works within.
func developerContract(scratchDirectory string, checks []string) string {
	contract := strings.ReplaceAll(developerContractTemplate, scratchDirectoryPlaceholder, scratchDirectory)
	// The self-verification section is built rather than written into the
	// template above, because it names this project's own declared checks: a
	// contract asking a developer to run commands the project does not declare
	// would be asking for something nobody can run.
	return strings.ReplaceAll(contract, selfCheckContractPlaceholder, selfcheck.Contract(checks))
}

const developerContractTemplate = `You are the developer for one bounded Yoyodyne work item.

Work only inside the current assigned worktree. Do not create, remove, or switch branches or worktrees. Do not commit, push, or integrate the change; the harness does all three. Do not modify upstream product, goal, design, or specification artifacts; propose the change instead, in the block described below. Implement the assigned work, run relevant focused checks, and finish with a concise summary of changes, verification, and any remaining risk.

The reply you end on is the whole of what this run records about itself: the summary above, the landing you claim, anything you report, and anything you propose are all read off it and nothing else. So it has to be the account. An invocation that ends on interim progress — a check still running, something you will report once it lands — accounts for nothing, and is asked for the account once more; a second reply that accounts for nothing ends the run with the work unrecorded. Finish what you left in flight before you reply.

That boundary is enforced rather than trusted. The project's configuration directory and the homes its product artifacts, designs, and decision records live in are refused in your change: the harness compares what you touched against them before any check runs and before any reviewer sees the work, and hands the change back to you if it touches one of them. The only exception is a path this work item grants, on a line beginning ` + "`" + protectedpath.GrantMarker + "`" + ` in its title, description, design guidance, or acceptance criteria. Nothing you write grants a path, and neither does anything written into the item's notes, which is where a run's own record goes. If your work genuinely needs one, leave it alone and say so — the refusal you would get names the same thing this does.

The same gate refuses the work tracker's export where your worktree carries one. The harness copies it in from the checkout your worktree was cut from, so you read the work around your own rather than the copy your base commit carried, and holds it out of your change: it is derived from a store outside Git that nothing you write reaches, and every run is given its own copy, so the same file in two changes is a merge conflict between runs rather than a contribution. Read it and leave it alone. A change containing it is handed back to you whatever became of the bit that holds it.

A grant lifts the harness's refusal and never somebody else's. Claude Code refuses your writes to ".claude/settings.json" and ".claude/settings.local.json" above anything this harness permits: the editing tools are denied there however this run is configured, and the shell sandbox names the file and cannot be disabled. No grant reaches those paths, and an item that tries to grant one is refused before a run starts, so the case you can actually meet is work that needs one of them changed and says so in prose. Those files are the operator's to change by hand. Do the rest of the work, say in your summary exactly what has to go into the file and that a person has to put it there, and do not spend attempts finding another way in — there is not one. The same holds of this harness's own ".yoyodyne/roles/": a role definition says what a role may do, so the harness refuses a change touching that directory whatever the work item grants, and refuses an item whose text grants it before a run starts. A person changes a role definition and the operator activates it; say in your summary what it should say.

The work backlog is upstream in the same way. The Lead Product Manager decides what is admitted to it and in what order it is pulled, so do not admit work to it, reorder it, or retire anything from it. Work you discover goes in your summary, as work to be admitted rather than work you have queued.

` + terms.PersonWriting + `

` + terms.StandingGoals + `

` + terms.DecideAndReport + `

Your worktree is yours alone, and so is the scratch directory the harness cut for this run, at ` + scratchDirectoryPlaceholder + ` — anything your work needs that your change must not carry goes there. The log you redirect a check into is the ordinary case. No other run is given that directory, so nothing you write in it can be read back by a run working beside you, and nothing in it can enter your change or leave your worktree dirty. Neither of the two obvious alternatives is one you can use: a scratch file inside the worktree is untracked content every reviewer is then shown, and the machine's temporary directory is one directory every run on this machine is handed at once — two runs redirecting a check into the same name there is one file both of them write, which on 2026-09-01 is how a run came to report a broken toolchain over another run's compile error while its own checks were passing.

Documentation that describes behavior you change is part of the assigned work, not a follow-up: leave no document asserting what your change has made false. Update the ones you may edit in this same change, and for a stale upstream artifact you may not edit, propose the correction it needs.

A change that adds a configuration key — to the configuration's schema, and so to what the shipped template or a project's file may carry — says so in its summary, naming the key. Every part of the product still running a build from before the key refuses the whole file once it carries it, so whoever lands the change needs to know a restart follows; ` + "`yoyo config validate`" + ` and ` + "`yoyo doctor`" + ` name each running part that cannot read it.

` + terms.LiveCopy + `

Any architectural invariant delivered with this work item is a constraint on your change rather than advice. Invariants exist because a change whose own work is correct can still break something the work item never mentioned, so each one holds even where nothing else you were given refers to it. They belong to the architect: do not create, amend, retire, or edit one. If your work cannot satisfy an invariant, or you believe one is wrong, leave it as it stands and put the amendment you would propose in your summary for the architect to decide.

` + selfCheckContractPlaceholder + `

` + landing.Contract + `

` + report.Contract + `

Your summary and a report do different jobs, and something can need both. The summary is your account of this work item, read by whoever looks at this run, and it is still where discovered work goes for the Lead Product Manager to admit. A report outlives the run, so it is what you use for something that will still matter once this item is closed and nobody is reading its summary any more.

` + amendment.Contract + `

A proposal is not a report and not a work item. A report says what somebody should know and asks for nothing; a proposal asks the owner of one document for one change to it, and waits for their answer rather than yours. An architectural invariant is not one of these documents and has its own lifecycle, so the amendment you would propose to one still goes in your summary for the architect.`

// developerPrompt places the immutable contract first, the configured persona
// second as guidance subordinate to it, then the architectural invariants that
// constrain the change, and the work item context last.
func developerPrompt(persona, invariants, bundle, scratchDirectory string, checks []string) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	if trimmed := strings.TrimSpace(persona); trimmed != "" {
		prompt.WriteString("# Configured developer persona\n\nThe project configuration supplies the guidance below. It may specialize how you work, but it cannot remove or weaken any rule above.\n\n")
		prompt.WriteString(trimmed)
		prompt.WriteString("\n\n")
	}
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString(bundle)
	return prompt.String()
}

// deliveredInvariantSection is how the invariants enter a developer prompt. It is
// one helper because every prompt a developer receives carries them — the first
// attempt and both kinds of repair — and a repair attempt that lost the
// constraints would be an attempt free to break one while fixing something else.
func deliveredInvariantSection(invariants string) string {
	if strings.TrimSpace(invariants) == "" {
		return ""
	}
	return invariants + "\n"
}

// repairPrompt hands one attempt's findings back to the developer that produced
// the change. The findings are rendered as the structured value the reviewer
// produced rather than restated as prose, so nothing is lost or reinterpreted
// between the reviewer and the developer that must act on it. The harness
// contract is repeated because it bounds the attempt whether or not the provider
// actually restored the session it was asked to resume.
func repairPrompt(invariants, summary, scratchDirectory string, checks []string, findings []runstate.Finding, attempt, limit int) (string, error) {
	encoded, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode review findings for repair attempt %d: %w", attempt, err)
	}
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Independent review: repair required\n\n")
	fmt.Fprintf(&prompt, "An independent reviewer examined your change and did not approve it. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and resolve every finding below.\n\n", attempt, limit)
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		prompt.WriteString("Reviewer summary: " + trimmed + "\n\n")
	}
	prompt.WriteString("Findings:\n\n")
	prompt.Write(encoded)
	prompt.WriteString("\n\nFix each finding, re-run the relevant checks, and finish with a concise summary of what you changed. If a finding is wrong, say why in your summary rather than leaving it unaddressed.")
	return prompt.String(), nil
}

// freshSessionRepairPrompt is a repair prompt for a developer session that holds
// none of the context the run built, because the run recorded no session to
// resume. The repair prompt already carries the contract, the invariants, and
// the failure to act on; this adds what a resumed session would have held — the
// persona, the run's own record of the change, and the work item — and says the
// change in the worktree is the run's earlier work, to continue rather than
// start over.
func freshSessionRepairPrompt(repair, persona, bundle string, state runstate.State) string {
	var prompt strings.Builder
	prompt.WriteString(repair)
	prompt.WriteString("\n\n# A fresh session on an earlier change\n\n")
	prompt.WriteString("This session is new. The run recorded no earlier developer session to resume, so nothing you did before is in your context. The change in your worktree is this run's earlier work on the item below, and the failure above is about that change: read the change, continue it, and do not start over.\n\n")
	fmt.Fprintf(&prompt, "Run: %s\nBranch: %s\nBase commit: %s\nTarget branch: %s\n", state.RunID, state.Branch, state.BaseCommit, state.TargetBranch)
	if state.Changes != nil && strings.TrimSpace(state.Changes.Files) != "" {
		prompt.WriteString("\nFiles the change touched when the run last recorded it:\n\n```\n")
		prompt.WriteString(strings.TrimSpace(state.Changes.Files))
		prompt.WriteString("\n```\n")
	}
	if state.DeveloperSummary != nil && strings.TrimSpace(state.DeveloperSummary.Text) != "" {
		prompt.WriteString("\nThe earlier developer's own summary of the change:\n\n")
		prompt.WriteString(strings.TrimSpace(state.DeveloperSummary.Text))
		prompt.WriteString("\n")
	}
	prompt.WriteString("\n")
	if trimmed := strings.TrimSpace(persona); trimmed != "" {
		prompt.WriteString("# Configured developer persona\n\nThe project configuration supplies the guidance below. It may specialize how you work, but it cannot remove or weaken any rule above.\n\n")
		prompt.WriteString(trimmed)
		prompt.WriteString("\n\n")
	}
	prompt.WriteString(bundle)
	return prompt.String()
}

// pathRefusalRepairPrompt hands a refused change back to the developer that
// produced it. It says what was refused, what the item did grant, and how a
// grant is made, in that order: the first is what has to come back out of the
// change, and the last is the whole reason this is a refusal rather than a
// finding — a developer that genuinely needs one of these paths has to be able
// to ask instead of quietly reaching for it again. The harness contract is
// repeated for the reason both other repair prompts repeat it: it bounds the
// attempt whether or not the provider actually restored the session.
func pathRefusalRepairPrompt(invariants, scratchDirectory string, checks []string, refusal runstate.PathRefusal, protected protectedpath.Set, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Protected paths: repair required\n\n")
	fmt.Fprintf(&prompt, "Your change touches paths this work item does not grant, so it was refused before any check ran and before it reached a reviewer. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and take these paths back out of it.\n\n", attempt, limit)
	prompt.WriteString("Refused paths:\n\n")
	for _, refused := range refusal.Paths {
		prompt.WriteString("- " + refused + "\n")
	}
	if refusal.Omitted > 0 {
		fmt.Fprintf(&prompt, "- and %d further refused path(s) not listed here\n", refusal.Omitted)
	}
	fmt.Fprintf(&prompt, "\nProtected by this project: %s\n", strings.Join(protected.Directories(), ", "))
	if held := protected.HeldExports(); len(held) > 0 {
		fmt.Fprintf(&prompt, "Held out of every run's change by the harness: %s\n", strings.Join(held, ", "))
	}
	if len(refusal.Grants) > 0 {
		fmt.Fprintf(&prompt, "Granted by this work item: %s\n", strings.Join(refusal.Grants, ", "))
	} else {
		prompt.WriteString("Granted by this work item: nothing\n")
	}
	prompt.WriteString("\n" + protectedpath.GrantInstruction + "\n")
	// Said only when one of these was actually caught. A developer refused for an
	// artifact home has no use for an explanation of a file it never touched, and
	// a repair prompt that explains everything is one nothing in particular stands
	// out of.
	if len(protected.HeldExportsAmong(refusal.Paths)) > 0 {
		prompt.WriteString("\n" + protectedpath.ExportInstruction + "\n")
	}
	if len(protectedpath.RoleDefinitionsAmong(refusal.Paths)) > 0 {
		prompt.WriteString("\n" + protectedpath.RoleInstruction + "\n")
	}
	prompt.WriteString("\nRestore each refused path to what it held before your change, finish the work you were assigned within the paths that are yours, and finish with a concise summary of what you changed. The harness applies this gate again afterwards; the checks, review, and integration stay out of reach until the change touches nothing it was not granted.")
	return prompt.String()
}

// checkRepairPrompt hands one failing deterministic check back to the developer
// that produced the change. The command, its exit code, and its captured output
// go back verbatim, because what makes a check better repair input than a
// reviewer's finding is that it is reproducible and names the exact failure.
// The harness contract is repeated for the same reason the review repair repeats
// it: it bounds the attempt whether or not the provider actually restored the
// session it was asked to resume.
func checkRepairPrompt(invariants, scratchDirectory string, checks []string, failure runstate.CheckFailure, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Failing check: repair required\n\n")
	if failure.ForgeHeadCommit != "" {
		fmt.Fprintf(&prompt, "The forge checks failed on your change and the harness withdrew its queued merge. This is repair attempt %d of %d. Continue the preserved change in your worktree and fix the reported failure.\n\nForge check: %s\nChecked commit: %s\n\n", attempt, limit, failure.Command, failure.ForgeHeadCommit)
	} else {
		fmt.Fprintf(&prompt, "A configured check failed on your change. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and make this check pass.\n\n", attempt, limit)
		fmt.Fprintf(&prompt, "Command: %s\nExit code: %d\n\n", failure.Command, failure.ExitCode)
	}
	if failure.Output != "" {
		prompt.WriteString("Captured output:\n\n```\n")
		prompt.WriteString(failure.Output)
		prompt.WriteString("\n```\n\n")
	}
	if before := failure.ReviewBefore; before != nil {
		encoded, err := json.MarshalIndent(before.Findings, "", "  ")
		if err == nil {
			prompt.WriteString("## The review this check failed after\n\n")
			prompt.WriteString("First an independent reviewer examined an earlier version of your change")
			if before.ReviewedCommit != "" {
				prompt.WriteString(" (commit " + before.ReviewedCommit + ")")
			}
			if before.Decision == runstate.ReviewApprove {
				prompt.WriteString(" and approved it with the findings below.")
			} else {
				prompt.WriteString(" and sent it back with the findings below.")
			}
			prompt.WriteString(" Then the change that answered them failed the check above, so no reviewer has yet seen whether it resolves them. Fix the check without undoing what these findings ask for; the reviewer reads the change again once every check passes.\n\n")
			if trimmed := strings.TrimSpace(before.Summary); trimmed != "" {
				prompt.WriteString("Reviewer summary: " + trimmed + "\n\n")
			}
			prompt.WriteString("Findings:\n\n")
			prompt.Write(encoded)
			prompt.WriteString("\n\n")
		}
	}
	if failure.ForgeHeadCommit != "" {
		prompt.WriteString("Work from the forge's account above; nothing asks you to fetch the forge. Run the relevant local checks and finish with a concise summary of what you changed. The harness runs fresh configured checks and independent review before publishing the repaired change.")
	} else {
		prompt.WriteString("Fix the cause, run the command yourself to confirm it passes, and finish with a concise summary of what you changed. The harness re-runs every configured check afterwards; review and integration stay out of reach until they all pass.")
	}
	return prompt.String()
}

// accountPrompt asks a developer that ended an invocation without accounting for
// its work to account for it now.
//
// It is not a repair attempt and spends nothing from that budget: no check
// failed, no reviewer found anything, and the change in the worktree may be
// finished. So the prompt says that in as many words, because a developer told
// only that its reply was refused would start the work over — which is the one
// expensive way this could go wrong. The harness contract is repeated for the
// reason every repair prompt repeats it: it bounds the attempt whether or not
// the provider actually restored the session it was asked to resume.
// replayConflictRepairPrompt hands a refused replay back to the developer that
// wrote the change. What it describes is a worktree that has already been moved
// onto the target, so both answers are in front of the developer with Git's
// markers between them — the state a person resolving a rebase by hand works
// in, and the only state in which this is answerable at all.
//
// It is deliberately not phrased as a Git operation to perform. Nothing is
// half-applied waiting to be continued, and what is being asked for is a
// judgement: two changes to the same lines, one of which this developer wrote,
// reconciled into something that does the work the item asked for.
func replayConflictRepairPrompt(invariants, scratchDirectory string, checks []string, conflict runstate.ReplayConflict, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Integration conflict: repair required\n\n")
	fmt.Fprintf(&prompt, "Your change passed its checks and was approved, but the branch it is to be integrated into moved while you were working, and your change could not be replayed onto where it went. This is repair attempt %d of %d. Your change has been moved onto the target for you, and the parts that would not merge are in your worktree between Git's conflict markers. Continue the change you already made instead of starting over, and settle every conflict.\n\n", attempt, limit)
	fmt.Fprintf(&prompt, "Target branch: %s\n", conflict.TargetBranch)
	if conflict.TargetCommit != "" {
		fmt.Fprintf(&prompt, "Your worktree now sits on: %s\n", conflict.TargetCommit)
	}
	if len(conflict.Paths) > 0 {
		prompt.WriteString("\nThe replay stopped on:\n\n")
		for _, path := range conflict.Paths {
			prompt.WriteString("- " + path + "\n")
		}
		if conflict.Omitted > 0 {
			fmt.Fprintf(&prompt, "- and %d further path(s) not listed here\n", conflict.Omitted)
		}
	}
	if conflict.Detail != "" {
		prompt.WriteString("\nWhat Git reported when the replay was refused:\n\n```\n")
		prompt.WriteString(conflict.Detail)
		prompt.WriteString("\n```\n")
	}
	prompt.WriteString("\nWhat is in front of you is a disagreement rather than a mechanical merge: your change and the target branch each hold an answer for the same lines, and only one of them was yours. Read both, decide what each file should actually say, and edit it into that, leaving no conflict markers behind. Everything the target changed that your work did not touch is already in your worktree and is not yours to undo. `git status`, `git diff`, and `git log` all work, and the target's own history is in this same repository.\n")
	prompt.WriteString("\nIf the two answers genuinely cannot both stand — the target has done your work differently, or has made it unnecessary — say so plainly in your summary rather than forcing a reconciliation you do not believe in. The checks and an independent review both run again on what you produce, so nothing your change was granted before carries over.")
	return prompt.String()
}

func accountPrompt(invariants, scratchDirectory, reason string, checks []string) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Your reply did not account for the work\n\n")
	fmt.Fprintf(&prompt, "The harness reads your reply for the run's whole record of itself — the summary of what you did, the landing you claim, anything you reported, anything you proposed — and %s. Nothing recorded what this run was for.\n\n", reason)
	prompt.WriteString("The work you already did is untouched. Your worktree holds it and this is the same session, so continue rather than starting anything over: finish whatever you left in flight — a check you said was running, a step you said you would come back to — and then reply with the account itself. What you changed, how you verified it, and what risk remains, plus any landing claim, report, or proposal you have to make.\n\n")
	prompt.WriteString("This is asked once. A second reply that accounts for nothing ends the run with the work unrecorded.")
	return prompt.String()
}

// boundedCheckOutput is what a failing check gets to say, in the order a
// terminal would have shown it. When a check says more than the bound allows,
// what is kept is the output around the first line reporting a failure and the
// end of the output, each cut stated; checks.FailureOutput says how.
func boundedCheckOutput(result checks.Result) string {
	return checks.FailureOutput(result)
}

// truncationNotice replaces the output boundedTail dropped. It counts against
// the bound, so what is kept plus the notice never exceeds it.
const truncationNotice = "[earlier check output truncated]\n"

// boundedTail keeps the last bytes of a value within a limit, cut on a rune
// boundary: output truncated mid-rune is not text.
func boundedTail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	keep := limit - len(truncationNotice)
	if keep <= 0 {
		keep = limit
	}
	cut := len(value) - keep
	for cut < len(value) && !utf8.RuneStart(value[cut]) {
		cut++
	}
	if keep == limit {
		return value[cut:]
	}
	return truncationNotice + value[cut:]
}

// renderBlockerNotes describes a run that spent its repair budget. It names the
// findings that are still unresolved and the artifacts that were kept, because
// this note is what a human or a later development manager replans from.
func renderBlockerNotes(outcome Outcome, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved; the unresolved findings below need a replan or a reassignment.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderCheckBlockerNotes describes a run that spent its repair budget on a
// check that still fails. It names the exact command and what it printed,
// because that is what a human or a later development manager replans from.
func renderCheckBlockerNotes(outcome Outcome, failure runstate.CheckFailure, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: a configured check still failed after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved; the failing check below needs a replan or a reassignment.",
		fmt.Sprintf("Failing check: %s (exit %d)", failure.Command, failure.ExitCode),
	}
	if failure.Output != "" {
		lines = append(lines, "Captured output:\n"+failure.Output)
	}
	return strings.Join(lines, "\n")
}

// renderPathRefusalBlockerNotes describes a run that kept reaching for paths its
// work item does not grant. Unlike the other blockers this one names a decision
// somebody has to take rather than a defect somebody has to fix: the grant is
// the product manager's to add to the item, so the note says what was refused
// and what the item grants today, and leaves which of the two is wrong to the
// reader.
func renderPathRefusalBlockerNotes(outcome Outcome, refused pathRefusal, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its change kept touching paths the work item does not grant, after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved. Either the change reaches outside this item, or this item is missing a grant it should have had; the grant belongs in the item's own text.",
		"Refused paths: " + strings.Join(refused.refusal.Paths, ", "),
	}
	if refused.refusal.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("Further refused paths not listed: %d", refused.refusal.Omitted))
	}
	lines = append(lines, "Protected by this project: "+strings.Join(refused.set.Directories(), ", "))
	// A refused export is a different decision from a refused document, so the
	// note says which one this is. Granting the path would admit derived state
	// into the change rather than settle anything: the file is the harness's copy
	// of a store outside Git, and a change carrying it means the hold that keeps
	// it out of every run's change was lifted inside this one.
	if held := refused.set.HeldExportsAmong(refused.refusal.Paths); len(held) > 0 {
		lines = append(lines, "Held out of every run's change by the harness: "+strings.Join(held, ", "),
			"That is a derived export the harness refreshes into each worktree and holds out of its change, so a change carrying one is a lifted hold rather than a missing grant.")
	}
	// A role definition is the other refusal no grant would have settled, so the
	// note says so rather than leaving "missing a grant" as a reading of it.
	if roles := protectedpath.RoleDefinitionsAmong(refused.refusal.Paths); len(roles) > 0 {
		lines = append(lines, "Role definitions: "+strings.Join(roles, ", "),
			"No grant reaches "+protectedpath.RoleDefinitions+"/, so a change carrying one is a change to take back out rather than a missing grant; a person changes a role definition by hand and the operator activates it.")
	}
	if len(refused.refusal.Grants) > 0 {
		lines = append(lines, "Granted by this work item: "+strings.Join(refused.refusal.Grants, ", "))
	} else {
		lines = append(lines, "Granted by this work item: nothing")
	}
	return strings.Join(lines, "\n")
}

// renderChargedReplayBlockerNotes describes a run stopped because a replayed
// change stopped on the change past the integration budget. It keeps the two
// counts apart — the races lost, which cost nothing, and the replays that
// stopped on the change, which are what the budget bounds — so the reader sees
// that the target moving is not what ended the run.
func renderChargedReplayBlockerNotes(outcome Outcome, failure string, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: after its target branch moved, the change replayed onto it stopped on the change once more than execution.integration_retries_before_reconciliation permits.",
		fmt.Sprintf("Replays that stopped on the change: %d of %d permitted", outcome.ChargedReplays, limit),
		fmt.Sprintf("Races lost to the moving target: %d (these cost nothing)", outcome.IntegrationRetries),
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		"What stopped the replay is about the change on its new base: a failing check, a refused path, missing verification, or a repair verdict. The branch and worktree are preserved with the replayed change in them.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderRelaunchBlockerNotes describes a run the provider kept killing. What
// stopped it is never a verdict on the change, so like the integration blocker it
// preserves work that is worth picking up rather than replanning: the developer's
// session is still resumable and the worktree holds whatever the last attempt
// reached.
//
// What the run was already carrying when the provider killed it is a separate
// question, and the note answers it rather than assuming. A provider can die
// during a repair attempt as easily as during the first one, so the run may hold
// a spent repair attempt, refused paths, a failing check, a reviewer's findings,
// and a replay its target refused — all of which are named here, because a reader told only about the
// provider would go looking for a clean change and find a dirty one.
func renderRelaunchBlockerNotes(outcome Outcome, failure backend.TransientFailure, checkFailure *runstate.CheckFailure, refusal *runstate.PathRefusal, conflict *runstate.ReplayConflict, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: the provider kept ending its invocations without judging the work, and the relaunch budget is spent.",
		fmt.Sprintf("Relaunches: %d of %d permitted", outcome.TransientRelaunches, limit),
		"Last provider failure: " + failure.Detail,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
	}
	// A run that also spent its recovery window says so, because the two say
	// different things to whoever reads this. A budget spent on its own is a
	// provider ending invocations for reasons nobody has classified; a window
	// spent as well is a network that stayed down for as long as the harness was
	// willing to wait, which is a machine to look at rather than an account.
	lines = append(lines, renderRetryNotes(outcome)...)
	if outcome.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts already spent: "+strconv.Itoa(outcome.RepairAttempts))
	}
	if refusal != nil {
		lines = append(lines, "Refused protected paths: "+strings.Join(refusal.Paths, ", "))
	}
	if checkFailure != nil {
		lines = append(lines, fmt.Sprintf("Last failing check: %s (exit %d)", checkFailure.Command, checkFailure.ExitCode))
	}
	if conflict != nil {
		lines = append(lines, "Unreconciled replay conflict against: "+conflict.TargetBranch)
	}
	lines = append(lines, relaunchBlockerVerdict(outcome, checkFailure, conflict))
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// relaunchBlockerVerdict says what the run's own evidence supports about the
// change, which is not the same sentence on every run the provider killed. A run
// killed before anything judged it carries no verdict at all, and saying so is
// what tells the reader to pick the work up rather than replan it. A run killed
// inside its repair loop carries a failing check or a reviewer's findings, and
// that same sentence would deny evidence recorded in the note around it.
func relaunchBlockerVerdict(outcome Outcome, checkFailure *runstate.CheckFailure, conflict *runstate.ReplayConflict) string {
	if outcome.RepairAttempts > 0 || checkFailure != nil || conflict != nil || len(outcome.ReviewFindings) > 0 {
		return "What stopped this run is the provider rather than a verdict on the change. The repair evidence recorded with this note is what the run was already carrying, and it is unresolved rather than dismissed. The branch, worktree, and developer session are preserved."
	}
	return "No check failed and no reviewer asked for repair; nothing here says the change is wrong. The branch, worktree, and developer session are preserved, and what needs looking at is the provider."
}

// renderMissingPreservedChangeNotes describes a run picked up again to continue
// a change its worktree does not hold. It names the branch first and says
// plainly that nothing was developed, because the two things a reader has to be
// stopped from concluding are that the work is gone and that the empty diff
// behind this is somebody's verdict on it: the change may be on the recorded
// branch in full, and no developer and no reviewer were ever invoked.
func renderMissingPreservedChangeNotes(outcome Outcome, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: its run was picked up again to continue a change, and the worktree it was re-entered in holds none of that change.",
		"Nothing was developed, checked, or reviewed from an empty worktree; continuing a run means continuing a change that already exists, and doing it from nothing is how an empty repair, a review round burned on an empty diff, or a reinvented change gets delivered.",
		"This round was ended by something outside the work: what it was handed held none of the change, so whatever it would have spent — a review round against the item's cap, a granted repair round out of the item's grant — is given back as the run settles. This item is no closer to its cap for the round, and the note recorded as the run ends says exactly what was returned.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		"Nothing here says the change was wrong, and nothing was deleted: the branch above is where the preserved work is if it survived. What this needs is somebody to say whether the worktree was seeded from that branch, and to re-enter the run once it carries the change again.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderRebaseConflictNotes describes a change that cannot be replayed onto what
// its target became, on a run whose repair budget leaves its developer no
// attempt to reconcile it. It names both sides and says explicitly that nothing
// was resolved automatically.
//
// It counts the repair attempts, because that is what changes what the reader is
// being handed. A conflict the run's own developer was asked to settle and could
// not is a disagreement worth a person's judgement; one reached with the budget
// already spent on something else is work still waiting for that developer, and
// a repair triage grants hands it exactly this conflict in the same session.
func renderRebaseConflictNotes(outcome Outcome, failure string, conflict *runstate.ReplayConflict, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its target branch moved, and this change conflicts with what the target now holds.",
		"Nothing was force-merged, reset, or auto-resolved; which side of the conflict is right is a decision, and the run's repair budget leaves its developer no attempt to reconcile it.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
	}
	if conflict != nil {
		if conflict.TargetCommit != "" {
			lines = append(lines, "Target branch is at: "+conflict.TargetCommit)
		}
		if len(conflict.Paths) > 0 {
			listed := "The replay stopped on: " + strings.Join(conflict.Paths, ", ")
			if conflict.Omitted > 0 {
				listed = fmt.Sprintf("%s, and %d more", listed, conflict.Omitted)
			}
			lines = append(lines, listed)
		}
	}
	lines = append(lines, "The branch and worktree are preserved exactly as they were. A repair granted in triage continues this run's developer session with the conflict handed back to reconcile against the target; a re-run starts the change over on the target as it now stands.")
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderDivergedTargetNotes describes a target branch that has gone a different
// way from the one on the remote, with nothing promoted. Like the replay
// conflict it says explicitly that nothing was resolved, and it names where each
// branch stands: what a person settles here is which of the two histories the
// project's target branch is, and neither commit can be found from the other.
//
// An approved change stopped here is resumable, and the note says so after the
// recovery: settling the branches is the person's, and carrying the approved
// change the rest of the way afterwards is the harness's, at no cost to the item.
func renderDivergedTargetNotes(outcome Outcome, catchup gitworktree.Catchup, remote, failure string, resumable bool) string {
	lines := []string{
		"Yoyodyne stopped this item: its target branch and the one on the remote have diverged, so the change was never promoted.",
		"Nothing was force-merged, reset, or auto-resolved; which history is right is a decision for a person.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		fmt.Sprintf("Local %s: %s", catchup.TargetBranch, nonEmpty(catchup.LocalCommit, "an unresolved commit")),
		fmt.Sprintf("%s %s: %s", remote, catchup.TargetBranch, nonEmpty(catchup.RemoteCommit, "no such branch")),
		"The checks passed and the reviewer approved; nothing here says the change is wrong. The branch and worktree are preserved, and reconciling the two target branches is what this needs before the change can be promoted.",
		divergedTargetRecovery,
	}
	if resumable {
		lines = append(lines, fmt.Sprintf("Once the branches are settled, `yoyo triage resume %s` resumes the promotion with the approval standing and charges no review round, repair grant, or re-run; asked before then, it refuses and says what is still diverged.", outcome.RunID))
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// divergedTargetRecovery is the route out of a target branch that has gone a
// different way from the remote's, named wherever that divergence is reported. It
// is the one repository state the harness will not decide, so a report of it that
// says only that a person is needed leaves them to invent the steps: which side
// is the shared truth, where the unpublished promotions go so nothing is lost,
// and how the branch is moved without racing a promotion. Those steps are written
// down, and every report of the divergence says where.
const divergedTargetRecovery = "Recovery: follow \"Unwedging a target branch that diverged from the forge\" in docs/operations.md. It preserves the local-only promotions on their own branch, puts the target back onto the remote's history, and leaves that branch for you to republish; the harness will not do it unasked, because which history is right is yours to say."

// renderPromotedDivergenceNotes describes the same divergence found one step too
// late: the local target branch already carries the promotion. It says that
// plainly and first, because a reader who assumed the change was lost would go
// looking for work that is already on their branch — and it says the item is
// deliberately not closed, because the promotion alone would ordinarily have
// closed it and what stops that here is the divergence rather than any doubt
// about the change.
func renderPromotedDivergenceNotes(outcome Outcome, integration gitworktree.Integration, catchup gitworktree.Catchup, remote, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: the change was promoted onto the local target branch, and the one on the remote then turned out to have diverged from it.",
		"The change itself is not at risk — it is on the local target branch, which is the authoritative one — but the publication did not happen and the two branches cannot be brought together by a fast-forward.",
		"The item is deliberately left open rather than closed as integrated: closing it would record this work as landed against a state nothing reconciles.",
		"Nothing was force-merged, reset, or auto-resolved; which history is right is a decision for a person.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		fmt.Sprintf("Promoted commit: %s, onto local %s at %s", integration.SourceCommit, integration.TargetBranch, integration.TargetCommit),
		fmt.Sprintf("%s %s: %s", remote, integration.TargetBranch, nonEmpty(catchup.RemoteCommit, "could not be resolved")),
		divergedTargetRecovery,
	}
	if outcome.PullRequest != nil {
		lines = append(lines, fmt.Sprintf("Pull request left unmerged: #%d %s", outcome.PullRequest.Number, outcome.PullRequest.URL))
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderUsageLimitPauseNotes describes a run that is waiting rather than one
// that stopped. It says plainly that nothing was abandoned, because an operator
// reading a claimed item that has gone quiet needs to know the difference
// between work in progress and work that needs them.
func renderUsageLimitPauseNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne paused this run: the provider refused the attempt without judging the work, so the run is waiting rather than failing.",
		"Run: " + outcome.RunID,
		"Waiting out: " + runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind),
	}
	if outcome.UsageLimitResetsAt != nil {
		lines = append(lines, "Asks again by: "+outcome.UsageLimitResetsAt.Format(time.RFC3339))
	}
	lines = append(lines,
		"Branch: "+outcome.Branch,
		"Worktree: "+outcome.WorktreePath,
	)
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Running Yoyodyne on this item again continues the same run; nothing needs to be restarted. The time above bounds the wait rather than gating it: the run asks the provider again at its configured probe interval, and `yoyo resume` moves the next probe to now if what refused it has stopped being true.",
	)
	return strings.Join(lines, "\n")
}

// renderProviderStopNotes describes a run whose provider the harness stopped on
// time. It says which of the two reasons it was, because they call for different
// things from whoever reads it: a stall is worth investigating, an exhausted
// budget is work that needs another pass. Neither is a report from the agent.
func renderProviderStopNotes(outcome Outcome) string {
	headline := "Yoyodyne stopped this run's provider: it stopped emitting events, so nothing was happening. The developer reported no failure."
	if outcome.ProviderStop == runstate.ProviderStopBudgetExhausted {
		headline = "Yoyodyne stopped this run's provider: it was still working when its total budget ran out. The developer reported no failure."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Running Yoyodyne on this item again continues the same run from where it stopped; nothing needs to be restarted.",
	)
	return strings.Join(lines, "\n")
}

// renderDirectivePauseNotes describes a run held up by an unresolved directive.
// It names the directive and what is unresolved about it, because those are the
// two things somebody needs in order to lift the pause, and it says plainly that
// the work was not abandoned: an operator reading a claimed item that has gone
// quiet has to be able to tell waiting from stopped.
func renderDirectivePauseNotes(outcome Outcome, held directive.Directive) string {
	lines := []string{
		"Yoyodyne paused this run: a user directive affects this work and is unresolved, so the run is waiting rather than failing.",
		"Directive: " + held.ID + " (" + string(held.Kind) + "), received by the " + string(held.ReceivedBy),
		"The operator said: " + held.Text,
	}
	if held.Artifact != "" {
		lines = append(lines, "Governed artifact it changes: "+held.Artifact)
	}
	lines = append(lines,
		"Unresolved: "+held.Unresolved,
		"Run: "+outcome.RunID,
	)
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Resolving the directive is what lifts the pause; running Yoyodyne on this item after that continues the same run.",
	)
	return strings.Join(lines, "\n")
}

// renderDependencyPauseNotes describes a run held up by work its item waits on.
// It names the blocking items, because closing or unlinking one of them is what
// lifts the pause, and it says plainly that the work was not abandoned: an
// operator reading a claimed item that has gone quiet has to be able to tell
// waiting from stopped.
func renderDependencyPauseNotes(outcome Outcome, waiting runstate.DependencyPause) string {
	lines := []string{
		"Yoyodyne paused this run: this item waits on work that is not finished, so the run is waiting rather than failing. Nothing about the change was judged.",
		"Waiting on: " + waiting.Summary(),
		"Run: " + outcome.RunID,
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Closing the work above, or removing the dependency link, is what lifts the pause; a watching `yoyo work` session then continues the same run at its next pull, with nobody having to run anything.",
	)
	return strings.Join(lines, "\n")
}

// renderDependencyContinuedNotes records a run paused on work its item waited on
// going again once that work closed: which run, what it had waited on, and who
// continued it — the harness, with its reason, or whoever named the item.
func renderDependencyContinuedNotes(state runstate.State, selection runstate.Selection, at time.Time) string {
	harness := selection.Stated() && selection.SelectedByHarness()
	by := "by a run named for this item"
	if harness {
		by = "by the harness"
	}
	note := fmt.Sprintf("Continued %s at %s: run %s had paused waiting on %s, which has closed; it goes on in its own worktree and developer session.",
		by, at.UTC().Format(time.RFC3339), state.RunID, state.DependencyPause.Summary())
	if harness {
		note += "\nWhy: " + strings.TrimSpace(selection.Reason)
	}
	return note
}

// renderTrackerPauseNotes describes a run parked because the tracker did not
// answer the read it makes at a gate boundary. It names the boundary and what
// the window was spent on, because a store that was contended for two hours and
// a store that is broken are different things to act on, and it says plainly
// that the work was not abandoned: an operator reading a claimed item that has
// gone quiet has to be able to tell waiting from stopped.
func renderTrackerPauseNotes(outcome Outcome, unanswered runstate.TrackerPause) string {
	lines := []string{
		"Yoyodyne parked this run: the tracker did not answer a read the run makes before it may take its next step, for the whole of that boundary's recovery window, so the run is waiting rather than failing. Nothing about the change was judged.",
		"Waiting on: " + unanswered.Summary(),
		"Run: " + outcome.RunID,
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"The tracker answering is what lifts the park; running Yoyodyne on this item continues the same run from where it stopped.",
	)
	return strings.Join(lines, "\n")
}

// renderOperatorHoldNotes describes a run parked because the operator paused all
// harness activity. It says who paused it and when, because the one thing an
// operator reading a quiet item has to be able to tell is a system they paused
// from a system that died.
func renderOperatorHoldNotes(outcome Outcome, held runstate.OperatorHold) string {
	lines := []string{
		"Yoyodyne parked this run: the operator paused all harness activity, so the run is waiting rather than failing. Nothing about the work was judged.",
		"Paused at: " + held.HeldAt.Format(time.RFC3339),
		"Run: " + outcome.RunID,
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"`yoyo resume` lifts the hold; a process still parked on it carries on within seconds, and running Yoyodyne on this item after that continues the same run.",
	)
	return strings.Join(lines, "\n")
}

// renderUsageLimitBlockerNotes describes a run stopped by a refusal it could not
// wait out. It names the reason the wait was refused, because the alternative to
// a stated deadline is a guessed one, and that is the decision being handed to a
// person.
func renderUsageLimitBlockerNotes(outcome Outcome, reason string) string {
	lines := []string{
		"Yoyodyne stopped this item: the provider refused it in a way this run could not wait out.",
		"Reason: " + reason,
		"Run: " + outcome.RunID,
		"Refused by: " + runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind),
	}
	if outcome.UsageLimitResetsAt != nil {
		lines = append(lines, "Reported reset: "+outcome.UsageLimitResetsAt.Format(time.RFC3339))
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	lines = append(lines, "The branch and worktree are preserved; this needs more provider capacity, a longer configured maximum pause, or a replan.")
	return strings.Join(lines, "\n")
}

func renderOutcomeNotes(outcome Outcome) string {
	headline := "Yoyodyne bootstrap run succeeded."
	// A protected target's change lands by the forge's merge, and a run whose
	// merge is still queued has landed it nowhere yet; "integrated" is said only
	// of a change that is on its target branch.
	integrated := "was integrated automatically"
	if outcome.Integration != nil && outcome.Integration.ThroughPullRequest {
		integrated = "was landed through its pull request"
		if outcome.PullRequest == nil || !outcome.PullRequest.Merged {
			integrated = "was handed to the forge to land through its pull request"
		}
	}
	if outcome.Integration != nil {
		headline = "Yoyodyne run passed checks, was approved by an independent reviewer, and " + integrated + "."
		// The headline of an item that stays open has to say so, because it is the
		// line somebody scanning the notes reads instead of the closure that is not
		// there. It names which reader withheld the closure, because the two are
		// different facts about the change: the account below is the developer's in
		// one case and the reviewer's summary in the other.
		switch {
		case outcome.Landing == landing.OutcomeEvidence || outcome.LandingProblem != "":
			headline = "Yoyodyne run passed checks, was approved by an independent reviewer, and " + integrated + "; the developer did not claim it discharges this item, so the item " +
				outcome.UndischargedDisposition() + "."
		case !outcome.ApprovalDischarges():
			headline = "Yoyodyne run passed checks and " + integrated + "; the independent reviewer approved the change as evidence rather than as the work this item asked for, so the item " +
				outcome.UndischargedDisposition() + "."
		}
	}
	// An escalated run integrated nothing, so it takes neither headline above. It
	// is said in the words the item is actually left in: nothing landed, and what
	// the run produced is a decision somebody named has to take.
	if outcome.Escalated() {
		headline = "Yoyodyne run raised this item as one that cannot be met as it stands, in the round the " +
			outcome.EscalatedBy().Title() + " reached. Nothing was integrated; the escalation is on the triage docket for the development manager and the item " +
			outcome.UndischargedDisposition() + "."
	}
	// These notes are recorded before cleanup, because the promotion is settled
	// first and only a promoted change's artifacts are removed. Cleanup can still
	// fail, so say it is scheduled rather than predicting that it happened.
	worktree := "Worktree: " + outcome.WorktreePath
	if outcome.Integration != nil {
		worktree = "Worktree (cleanup pending): " + outcome.WorktreePath
	}
	// A queued landing keeps its branch and worktree until the forge's merge is
	// confirmed (finish), because nothing proves the change is on the target and
	// the kept branch is what a head fallen behind is brought up to date from.
	if outcome.Integration != nil && outcome.Integration.ThroughPullRequest && outcome.PullRequest != nil && outcome.PullRequest.MergeQueued {
		worktree = "Worktree (kept with its branch until the forge's merge is confirmed): " + outcome.WorktreePath
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		worktree,
		"Base commit: " + outcome.BaseCommit,
	}
	// The claim is recorded on the item whichever way it went, so the item says
	// what this run said about it rather than only what became of the item. An
	// item that closed on a claimed discharge and one that closed because nobody
	// claimed anything are different records, and only this tells them apart.
	if line := renderLandingNote(outcome); line != "" {
		lines = append(lines, line)
	}
	if outcome.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts: "+strconv.Itoa(outcome.RepairAttempts))
	}
	// A promotion that had to be re-prepared says something about the branch it
	// was promoted into rather than about the change, and it is the only record
	// that the approved diff was replayed and judged again.
	if outcome.IntegrationRetries > 0 {
		lines = append(lines, "Integration retries: "+strconv.Itoa(outcome.IntegrationRetries))
	}
	if outcome.ChargedReplays > 0 {
		lines = append(lines, "Replays that stopped on the change: "+strconv.Itoa(outcome.ChargedReplays))
	}
	// A run that absorbed the provider dying under it and finished anyway says
	// nothing about the change, but it is the only place the deaths are counted
	// where somebody watching the item will see them: a provider degrading run
	// after run is visible here before it is visible as a blocked item.
	if outcome.TransientRelaunches > 0 {
		lines = append(lines, "Relaunches after a provider death: "+strconv.Itoa(outcome.TransientRelaunches))
	}
	// A run that waited a network out and finished is the one this has to say
	// most about, because nothing else about it looks unusual: before
	// yoyodyne-ifd.264 each of these was a run recorded as failed with its work
	// completed, and a reader who cannot see the retries cannot see how close it
	// came or that the network is degrading.
	lines = append(lines, renderRetryNotes(outcome)...)
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.ProviderModel != "" {
		lines = append(lines, "Developer model: "+renderModel(outcome.ProviderModel, outcome.ProviderResolvedModel))
	}
	if outcome.ProviderEffort != "" {
		lines = append(lines, "Developer effort: "+outcome.ProviderEffort)
	}
	if outcome.Changes.Status != "" {
		lines = append(lines, "Changes:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Diff stat:\n"+outcome.Changes.DiffStat)
	}
	lines = append(lines, renderCheckNotes(outcome)...)
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderCheckNotes records what the checks cost against what they were
// allowed, on the item itself, so a suite growing toward its budget is visible
// run after run rather than only in the run the budget finally stops. The
// stage is said first and whole — what it spent of its bound, and what the
// gate was narrowed to — because the per-check figures under it answer a
// different question: which check, not whether the stage fits.
func renderCheckNotes(outcome Outcome) []string {
	var lines []string
	if stage := outcome.CheckStage; stage != nil {
		line := fmt.Sprintf("Check stage: %s of the %s execution.check_stage_timeout bound%s", stage.Elapsed().Round(time.Second), stage.Bound(), stageBoundLoad(*stage))
		if stage.StoppedAtBound {
			line += ", stopped at the bound during " + stage.Command
		}
		if stage.Narrowed != "" {
			line += " (gate narrowed to: " + stage.Narrowed + ")"
		}
		lines = append(lines, line)
	}
	for _, check := range outcome.Checks {
		if check.CouldNotRun != "" {
			lines = append(lines, fmt.Sprintf("Check: %s could not run, so it judged nothing and spent no repair attempt: %s (exit=%d, %s of %s)",
				check.Command, check.CouldNotRun, check.Process.ExitCode, check.Elapsed().Round(time.Second), check.Timeout))
			continue
		}
		lines = append(lines, fmt.Sprintf("Check: %s (passed=%t, exit=%d, %s of %s)",
			check.Command, check.Passed, check.Process.ExitCode, check.Elapsed().Round(time.Second), check.Timeout))
		if !check.Passed {
			lines = append(lines, boundedCheckOutput(check))
		}
	}
	return lines
}

// renderInvariantNotes records which durable constraints this change was held to,
// and what the delivered set was missing. The tracker is where that outlives the
// run: an operator asking later what constrained a change gets an answer, and one
// asking why an invariant did not stop something sees whether it was delivered at
// all.
func renderInvariantNotes(outcome Outcome) []string {
	var lines []string
	if len(outcome.Invariants) > 0 {
		lines = append(lines, "Invariants delivered: "+strings.Join(outcome.Invariants, ", "))
	}
	for _, problem := range outcome.InvariantProblems {
		lines = append(lines, "Invariant not delivered: "+problem)
	}
	return lines
}

// stoppedByUsageWindow reports an environmental refusal by the provider's usage
// window, which every reader of a run's ending names apart from a failure.
func stoppedByUsageWindow(refused *runstate.EnvironmentalRefusal) bool {
	return refused != nil && refused.Cause == runstate.CauseUsageWindow
}

// renderUsageWindowReleaseNotes is what the item's notes say about its claim
// being given back after the provider's usage window stopped its run. The
// outcome note written just before it carries the account; this says only what
// became of the claim, and when the item will be pulled again.
func renderUsageWindowReleaseNotes(outcome Outcome) string {
	note := "Yoyodyne gave this item back to the queue: run " + outcome.RunID + " was stopped by the provider's usage limit rather than by anything about the work, and nothing was charged to the item for it."
	if outcome.Environmental != nil {
		if said := outcome.Environmental.ResetSays(); said != "" {
			note += " " + strings.ToUpper(said[:1]) + said[1:] + ", and a watching session pulls the item again once that has passed."
		}
	}
	return note
}

// renderFailureNotes describes a failed run on its work item.
//
// No headline here claims preservation, and neither does any line below one.
// What survives of the change is said once, from the check the run made against
// the repository as it ended, by renderPreservationNotes — because a headline
// that promises a branch and a worktree on the strength of the record alone is
// exactly the note that sent a developer to a checkout that was not there.
func renderFailureNotes(outcome Outcome) string {
	headline := "Yoyodyne bootstrap run failed."
	switch {
	case outcome.WorktreeRemoved || outcome.BranchRemoved:
		headline = "Yoyodyne run failed after the change was integrated and its artifacts were cleaned up; the integrated commit is the surviving evidence."
	case outcome.Integration != nil:
		headline = "Yoyodyne run failed after the change was already integrated; the integrated commit is what reconciliation settles from."
	}
	if outcome.Blocked {
		// A run is blocked by a spent repair budget, by a target branch it could
		// not promote into, or by a provider that kept killing it, and the recorded
		// blocker says which. This headline deliberately does not: naming one of
		// them would be wrong more often than not.
		headline = "Yoyodyne blocked this item; the blocker recorded on the item says what stopped it."
	}
	if stoppedByUsageWindow(outcome.Environmental) {
		headline = "Yoyodyne stopped this run without judging anything: the provider's usage limit refused it and resets later than the harness will wait, so the item goes back to the queue and is picked again once the limit resets."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Failure: " + outcome.Failure,
	}
	if outcome.Phase != "" {
		lines = append(lines, "Phase: "+string(outcome.Phase))
	}
	// The accounting comes before the counts below it, because it changes what
	// they mean: a reader shown "repair attempts: 3" with nothing saying the last
	// of them was refused by the environment reads an item three rounds closer to
	// its cap than it actually is.
	if outcome.Environmental != nil {
		lines = append(lines, "Round: "+outcome.Environmental.Describe())
	}
	// An approved change the environment stopped says so beside the failure, and
	// says what that costs, because the item's notes are what the next reader
	// decides from: every verb they would otherwise reach for spends something
	// for this stop, and the one that spends nothing is named here.
	if outcome.IntegrationStop != nil {
		lines = append(lines, "Integration stop: "+outcome.IntegrationStop.Describe()+
			"; `yoyo triage resume "+outcome.RunID+"` resumes the promotion with the approval standing once the cause has cleared, charging no review round, repair grant, or re-run")
	}
	// An approved change whose replay conflicted says so beside the failure for
	// the same reason, and in particular where the blocker that would have said
	// it never reached the item: the reader is otherwise sent to the resume verb,
	// which replays onto the same target and meets the same conflict.
	if outcome.ReplayConflict != nil {
		lines = append(lines, "Replay conflict: "+outcome.ReplayConflict.Describe()+"; "+outcome.ReplayConflict.Says(outcome.RunID))
	}
	if outcome.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts: "+strconv.Itoa(outcome.RepairAttempts))
	}
	if outcome.IntegrationRetries > 0 {
		lines = append(lines, "Integration retries: "+strconv.Itoa(outcome.IntegrationRetries))
	}
	if outcome.ChargedReplays > 0 {
		lines = append(lines, "Replays that stopped on the change: "+strconv.Itoa(outcome.ChargedReplays))
	}
	if outcome.TransientRelaunches > 0 {
		lines = append(lines, "Relaunches after a provider death: "+strconv.Itoa(outcome.TransientRelaunches))
	}
	lines = append(lines, renderPreservationNotes(outcome)...)
	if outcome.BaseCommit != "" {
		lines = append(lines, "Base commit: "+outcome.BaseCommit)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.ProviderModel != "" {
		lines = append(lines, "Developer model: "+renderModel(outcome.ProviderModel, outcome.ProviderResolvedModel))
	}
	if outcome.ProviderEffort != "" {
		lines = append(lines, "Developer effort: "+outcome.ProviderEffort)
	}
	// The change summary is the run's own record of what it had done and stays
	// true whatever became of the checkout, so it is named for what it is rather
	// than as preserved work. Where the artifacts are gone, the lines above are
	// what say so, and this is all that is left of the change.
	if outcome.Changes.Status != "" {
		lines = append(lines, "Changes when the run ended:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Diff stat when the run ended:\n"+outcome.Changes.DiffStat)
	}
	// A run that ended while the developer was working, before any check ran, has
	// nothing after the change summary to say what stopped it, and a note ending
	// on the diff stat reads as a change that was simply left there. So it ends on
	// the cause, in the words of the failure line above.
	if outcome.Phase == runstate.PhaseDeveloping && len(outcome.Checks) == 0 && strings.TrimSpace(outcome.Failure) != "" {
		lines = append(lines, "Ended before any check ran. What ended it: "+outcome.Failure)
	}
	// A failed run's checks are recorded for the reason a successful run's are,
	// and with more at stake: a run the stage bound stopped is read from this
	// note, and the note has to say the stage was what stopped it.
	lines = append(lines, renderCheckNotes(outcome)...)
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderPreservationNotes says what is left of a failed run's change, from what
// the run saw of it rather than from what its record claims.
//
// Every phrase here is a different answer on purpose. "Checked and there" is the
// only one that promises anything, and it is the only one earned by an
// observation; "removed by this run's cleanup" is the record's own account of a
// removal the run earned, which is why nothing looks for that artifact and
// nothing calls it lost; a check that could not be made says so and promises
// nothing; and an artifact the run made that nothing removed and the check did
// not find is stated as the loss it is, at the top of the note, with the two
// places the work can still be rather than a verdict this cannot reach.
func renderPreservationNotes(outcome Outcome) []string {
	preservation := outcome.Preservation
	if preservation == nil {
		// Nothing was made, so there is nothing to say. A note that said "no work
		// preserved" here would read as work thrown away.
		if outcome.Branch == "" && outcome.WorktreePath == "" {
			return nil
		}
		// The record names an artifact and nothing checked it. Naming it without the
		// check is what this exists to stop, so it is named as unchecked — except for
		// an artifact the record already says the run's cleanup removed, which is
		// settled without any check.
		preservation = &Preservation{
			Branch:          outcome.Branch,
			BranchRemoved:   outcome.BranchRemoved,
			WorktreePath:    outcome.WorktreePath,
			WorktreeRemoved: outcome.WorktreeRemoved,
		}
		if preservation.checkable() {
			preservation.Unverified = "nothing checked them as this run ended"
		}
	}
	var lines []string
	if preservation.Lost() {
		lines = append(lines, "PRESERVATION FAILED: "+preservationLoss(*preservation))
	}
	if preservation.Branch != "" {
		lines = append(lines, "Branch: "+preservation.Branch+describeArtifact(preservation.BranchRemoved, preservation.BranchPresent, preservation.Verified()))
	}
	if preservation.WorktreePath != "" {
		lines = append(lines, "Worktree: "+preservation.WorktreePath+describeArtifact(preservation.WorktreeRemoved, preservation.WorktreePresent, preservation.Verified()))
	}
	if !preservation.Verified() {
		// Said once, under the artifacts it applies to. It is deliberately not
		// silence: an operator who is told nothing goes to the path anyway, which is
		// the same walk the false claim sends them on.
		lines = append(lines, "Preservation unchecked: "+preservation.Unverified+", so nothing here says the branch or the worktree is still there.")
	}
	return lines
}

// preservationLoss says which artifact this run made is gone and where its work
// can still be, in one line that reads the same in the note, in the run's own
// error, and anywhere else it is reported.
//
// The two pointers are the whole of what is recoverable. Commits the checkout
// carried are on the branch when that survived, and a checkout the convergence
// sweep retired had whatever it held uncommitted recorded on the run-scoped ref
// that sweep writes — which is where this run's own record names it.
func preservationLoss(preservation Preservation) string {
	var lost []string
	if preservation.branchLost() {
		lost = append(lost, "the branch "+preservation.Branch)
	}
	if preservation.worktreeLost() {
		lost = append(lost, "the worktree "+preservation.WorktreePath)
	}
	return strings.Join(lost, " and ") + " this run made is already gone. What it held is recoverable only from what survives: the branch above where it still exists, and the preserved-work ref named on this run's record where the checkout was retired by the convergence sweep."
}

// describeArtifact is the one phrase an artifact's fate is said in, so a branch
// and a worktree are never described in two different vocabularies. The removal
// is read first because it settles the question without an observation: an
// artifact the run's own cleanup removed is gone on purpose, and nothing looked
// for it.
func describeArtifact(removed, present, verified bool) string {
	switch {
	case removed:
		return " (removed by this run's cleanup)"
	case !verified:
		return " (unchecked)"
	case present:
		return " (checked and there)"
	default:
		return " (checked and NOT there)"
	}
}

// renderReviewNotes carries the invariant, review, and integration evidence into
// the tracker, so an operator reconciling an item never has to reconstruct which
// constraints applied, which reviewer decided what, or which commit carried the
// work. Every kind of recorded note ends with this, so a run that succeeded, one
// that failed, and one that was blocked all say the same things about themselves.
func renderReviewNotes(outcome Outcome) []string {
	lines := renderInvariantNotes(outcome)
	if outcome.ReviewSessionID != "" {
		lines = append(lines, "Reviewer session: "+outcome.ReviewSessionID)
	}
	if outcome.ReviewModel != "" {
		lines = append(lines, "Reviewer model: "+renderModel(outcome.ReviewModel, outcome.ReviewResolvedModel))
	}
	if outcome.ReviewEffort != "" {
		lines = append(lines, "Reviewer effort: "+outcome.ReviewEffort)
	}
	// What the verdict was judged against, as two commits: a reader of the item
	// can tell from this alone whether a review saw the branch's earlier
	// attempts, rather than inferring it from the verdict's own hedging.
	if outcome.ReviewBaseCommit != "" && outcome.ReviewHeadCommit != "" {
		lines = append(lines, "Reviewed against: base "+outcome.ReviewBaseCommit+", tip "+outcome.ReviewHeadCommit)
	}
	if outcome.ReviewDecision != "" {
		lines = append(lines, "Review decision: "+string(outcome.ReviewDecision))
	}
	// What the approval approved is its own line, because it decides something the
	// decision above does not: an approval of evidence promotes the change and
	// closes nothing. It is recorded whichever way it went, so an item closed on an
	// approval of the implementation says that rather than saying only "approve".
	if outcome.ReviewApproves != "" {
		approved := "Approved as: " + string(outcome.ReviewApproves)
		if !outcome.ApprovalDischarges() {
			approved += " — the reviewer approved the change without approving it as the work this item asked for, so it discharges nothing"
		}
		lines = append(lines, approved)
	}
	if outcome.ReviewSummary != "" {
		lines = append(lines, "Review summary: "+outcome.ReviewSummary)
	}
	for _, finding := range outcome.ReviewFindings {
		location := ""
		if finding.Location != nil {
			location = fmt.Sprintf(" (%s:%d)", finding.Location.File, finding.Location.Line)
		}
		label := string(finding.Severity)
		if finding.Disposition != "" {
			label += ", " + string(finding.Disposition)
		}
		lines = append(lines, fmt.Sprintf("Finding [%s]%s: %s", label, location, finding.Message))
	}
	switch {
	case outcome.Integration != nil && outcome.Integration.ThroughPullRequest:
		// Nothing moved the local target, so the lines say what is to land and
		// where, and never that it is integrated there.
		lines = append(lines,
			"Lands on: "+outcome.Integration.TargetBranch+", by the forge's merge of its pull request; the local "+outcome.Integration.TargetBranch+" is not moved until then",
			"Commit to land: "+outcome.Integration.SourceCommit,
			"Target commit it was prepared on: "+outcome.Integration.PreviousTargetCommit,
		)
	case outcome.Integration != nil:
		lines = append(lines,
			"Integrated into: "+outcome.Integration.TargetBranch,
			"Integrated commit: "+outcome.Integration.SourceCommit,
			"Previous target commit: "+outcome.Integration.PreviousTargetCommit,
		)
	}
	return append(lines, renderPublishNotes(outcome)...)
}

// renderModel reports a requested selector alongside what the provider
// resolved it to, because a floating alias only becomes audit evidence once the
// served model is named.
func renderModel(requested, resolved string) string {
	if resolved == "" || resolved == requested {
		return requested
	}
	return requested + " (resolved: " + resolved + ")"
}

// renderCleanupNotes records that a completed run left its worktree behind. The
// integrated commit and the closed item are already true; this is what an
// operator needs in order to finish the job by hand.
func renderCleanupNotes(outcome Outcome) string {
	// Cleanup can fail after both removals already succeeded, when only the
	// confirmation of them failed. Nothing remains in that case, so it must not
	// be described as unfinished work.
	headline := "Yoyodyne run completed but its post-completion cleanup did not finish. The change is integrated and this item is closed."
	if outcome.WorktreeRemoved && outcome.BranchRemoved {
		headline = "Yoyodyne run completed and both run artifacts were removed, but confirming their removal failed. Nothing is known to remain; a repeated cleanup re-checks and removes nothing."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Cleanup failure: " + outcome.CleanupFailure,
		"Worktree removed: " + strconv.FormatBool(outcome.WorktreeRemoved),
		"Branch removed: " + strconv.FormatBool(outcome.BranchRemoved),
	}
	// Only artifacts that actually survive are reported as remaining; cleanup
	// is retryable, so naming a deleted one would send an operator after
	// something that is not there.
	if outcome.Branch != "" && !outcome.BranchRemoved {
		lines = append(lines, "Remaining branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" && !outcome.WorktreeRemoved {
		lines = append(lines, "Remaining worktree: "+outcome.WorktreePath)
	}
	if outcome.Integration != nil {
		lines = append(lines, "Integrated commit: "+outcome.Integration.SourceCommit)
	}
	return strings.Join(lines, "\n")
}

// renderCompletionRecordingNotes describes a finished run whose completion
// could not be written down. It states that removal is done, because an
// operator reading "cleanup" here must not go looking for artifacts that no
// longer exist.
func renderCompletionRecordingNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne run completed and its worktree and branch were both removed, but recording final completion failed. Cleanup is finished; nothing remains to remove.",
		"Run: " + outcome.RunID,
		"Completion recording failure: " + outcome.CompletionRecordingFailure,
		"Worktree removed: " + strconv.FormatBool(outcome.WorktreeRemoved),
		"Branch removed: " + strconv.FormatBool(outcome.BranchRemoved),
	}
	if outcome.Integration != nil {
		lines = append(lines, "Integrated commit: "+outcome.Integration.SourceCommit)
	}
	lines = append(lines, "Durable run state may still show the pre-cleanup marker; reconciling it requires no further removal.")
	return strings.Join(lines, "\n")
}

// integrationMessage describes the promoted work in the harness-owned commit,
// including the review that authorized it.
func integrationMessage(item beads.WorkItem, outcome Outcome) string {
	subject := strings.TrimSpace(fmt.Sprintf("yoyodyne: %s %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)))
	body := []string{
		"",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Base: " + outcome.BaseCommit,
		"Developer session: " + outcome.ProviderSessionID,
		"Reviewer session: " + outcome.ReviewSessionID,
		"Review decision: " + string(outcome.ReviewDecision),
	}
	return subject + "\n" + strings.Join(body, "\n") + "\n"
}

// singleLine folds a tracker-supplied title into one bounded subject line, so
// the commit subject stays a subject whatever the work item contains. It is cut
// on a rune boundary: a subject truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut])
}

func completionReason(outcome Outcome) string {
	return fmt.Sprintf("Reviewed and integrated by Yoyodyne run %s: %s is at %s",
		outcome.RunID, outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
}

func statusForContext(ctx context.Context) runstate.Status {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return runstate.StatusTimedOut
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return runstate.StatusCancelled
	}
	return runstate.StatusFailed
}

func statusForProcess(status execution.ProcessStatus) runstate.Status {
	switch status {
	case execution.ProcessCancelled:
		return runstate.StatusCancelled
	case execution.ProcessTimedOut, execution.ProcessStalled:
		// Both are the harness stopping a process on time. There is no separate
		// durable status for a stall, and recording it as a plain failure would
		// describe the agent as having failed at something.
		return runstate.StatusTimedOut
	default:
		return runstate.StatusFailed
	}
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown provider failure"
}
