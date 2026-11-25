package orchestrator

// Repeating a merge request and finishing the publication of a reviewed change
// start no work: each of their branches is run here against a provider, a
// reviewer, a check runner and an item claim that fail the test if used.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// failsIfUsed is what neither may use. It is the value behind every seam the two
// are given, so that one that started work through a seam would be seen.
type failsIfUsed struct{ t *testing.T }

func (f failsIfUsed) used(what string) error {
	f.t.Helper()
	f.t.Errorf("%s was used", what)
	return errors.New(what + " was used")
}

type noProvider struct{ failsIfUsed }

func (n noProvider) CheckAvailability(context.Context) (backend.Availability, error) {
	return backend.Availability{}, n.used("a provider")
}
func (n noProvider) Capabilities() backend.Capabilities { return backend.Capabilities{} }
func (n noProvider) Run(context.Context, backend.RunRequest) (backend.RunResult, error) {
	return backend.RunResult{}, n.used("a provider")
}
func (n noProvider) Review(context.Context, review.Request) (review.Result, error) {
	return review.Result{}, n.used("a reviewer")
}

type noChecks struct{ failsIfUsed }

func (n noChecks) Run(context.Context, checks.Request, func(execution.Event) error) ([]checks.Result, uint64, error) {
	return nil, 0, n.used("a check runner")
}

// noClaim is a tracker that can do nothing but read.
type noClaim struct {
	WorkTracker
	failsIfUsed
}

func (n noClaim) Claim(context.Context, string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	return beads.WorkItem{}, nil, n.used("an item claim")
}
func (n noClaim) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{Status: "closed"}, nil
}

// merging is the one forge both ask, over a provider that fails.
type merging struct {
	ReconcilePullRequests
	*orchestratortest.RearmForge
	noProvider
}

func (m merging) State(ctx context.Context, head string) (publish.PullRequest, error) {
	return m.RearmForge.State(ctx, head)
}
func (m merging) Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	return m.RearmForge.Merge(ctx, request)
}

// target is the repository both read, over a check runner that fails.
type target struct {
	ReconcileWorktrees
	*orchestratortest.RemoteTarget
	noChecks
}

func (r target) VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error {
	return r.RemoteTarget.VerifyRemoteTarget(ctx, integration)
}
func (r target) PushRemote() string { return "origin" }

// failingRuns is the run store with each thing the two use of it able to fail.
type failingRuns struct {
	*runstate.Store
	adopt, lease error
	// saveFails is which save fails, counted from one.
	saveFails, saves int
}

func (r *failingRuns) AdoptRun(ctx context.Context, id string) (runstate.State, *runstate.Lease, error) {
	if r.adopt != nil {
		return runstate.State{}, nil, r.adopt
	}
	return r.Store.AdoptRun(ctx, id)
}
func (r *failingRuns) LeasePromotion(ctx context.Context, branch string) (*runstate.Lease, error) {
	if r.lease != nil {
		return nil, r.lease
	}
	return r.Store.LeasePromotion(ctx, branch)
}
func (r *failingRuns) Save(state runstate.State) error {
	if r.saves++; r.saves == r.saveFails {
		return errors.New("the record could not be written")
	}
	return r.Store.Save(state)
}

type standingLanding struct{}

func (standingLanding) Standing(target string, now time.Time) (runstate.LandingHold, bool, error) {
	return runstate.LandingHold{TargetBranch: target, Reason: "a script", SetAt: now, Until: now.Add(time.Hour)}, true, nil
}

// mergeRequestCase is one way through either of the two.
type mergeRequestCase struct {
	state    runstate.State
	runs     *failingRuns
	forge    *orchestratortest.RearmForge
	worktree *orchestratortest.RemoteTarget
	checks   *checksStub
	holds    LandingHolds
	noChecks bool
	number   int
	says     string
	// seen changes the record as the sweep first read it.
	seen func(*runstate.State)
}

func newMergeRequestCase(t *testing.T, state runstate.State) *mergeRequestCase {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return &mergeRequestCase{
		state: state, runs: &failingRuns{Store: store}, number: 92,
		forge: &orchestratortest.RearmForge{
			Observed: publish.PullRequest{Number: 92, URL: "https://forge.invalid/pull/92", State: "OPEN", HeadCommit: rearmedCommit},
			Status:   "CLEAN", Result: publish.MergeResult{Queued: true},
		},
		worktree: &orchestratortest.RemoteTarget{},
		checks:   &checksStub{reading: publish.CheckReading{HeadCommit: rearmedCommit, Passing: 4}},
	}
}

func (c *mergeRequestCase) record(t *testing.T) {
	t.Helper()
	if err := c.runs.Store.Create(c.state); err != nil {
		t.Fatal(err)
	}
	if err := c.runs.Store.Save(c.state); err != nil {
		t.Fatal(err)
	}
}

// startedNothing is that no run is in flight, so none was put back to work and
// none holds a slot.
func (c *mergeRequestCase) startedNothing(t *testing.T) {
	t.Helper()
	incomplete, err := c.runs.Incomplete()
	if err != nil || len(incomplete) != 0 {
		t.Errorf("%d run(s) in flight, %v; want none", len(incomplete), err)
	}
}

func TestRepeatingAMergeRequestStartsNoWorkOnAnyBranch(t *testing.T) {
	t.Parallel()
	waiting := func() runstate.State {
		state := droppedPublication()
		state.PullRequest.TargetRed = &runstate.TargetRed{At: state.UpdatedAt, TargetBranch: "main", HeadCommit: rearmedCommit, Checks: []runstate.TargetRedCheck{{Name: "test", WorkItem: "yoyodyne-red"}}}
		return state
	}
	for name, test := range map[string]struct {
		state   func() runstate.State
		arrange func(*mergeRequestCase)
	}{
		"the run is held":        {droppedPublication, func(c *mergeRequestCase) { c.runs.adopt, c.says = runstate.ErrRunHeld, "take run" }},
		"no turn to promote":     {droppedPublication, func(c *mergeRequestCase) { c.runs.lease, c.says = errors.New("held"), "wait for a turn" }},
		"hold unreadable":        {droppedPublication, func(c *mergeRequestCase) { c.holds, c.says = unreadableLandingHolds{}, "garbled" }},
		"landing held":           {droppedPublication, func(c *mergeRequestCase) { c.holds, c.says = standingLanding{}, "keeps its re-arm" }},
		"settled meanwhile":      {droppedPublication, func(c *mergeRequestCase) { c.state.PullRequest.Merged, c.says = true, "was settled while" }},
		"not what was checked":   {droppedPublication, func(c *mergeRequestCase) { c.number, c.says = 93, "no longer what was checked" }},
		"forge unreadable":       {droppedPublication, func(c *mergeRequestCase) { c.forge.ObserveErr, c.says = errors.New("down"), "ask the forge about" }},
		"another request":        {droppedPublication, func(c *mergeRequestCase) { c.forge.Observed.Number, c.says = 5, "the forge reports pull request 5" }},
		"merged on the forge":    {droppedPublication, func(c *mergeRequestCase) { c.forge.Observed.Merged, c.says = true, "is merged on the forge" }},
		"queued on the forge":    {droppedPublication, func(c *mergeRequestCase) { c.forge.Observed.AutoMerge, c.says = true, "has a merge queued" }},
		"head moved":             {droppedPublication, func(c *mergeRequestCase) { c.forge.Observed.HeadCommit, c.says = rearmedBase, "is at "+rearmedBase }},
		"merge state unreadable": {droppedPublication, func(c *mergeRequestCase) { c.forge.StatusErr, c.says = errors.New("down"), "what is unmet" }},
		"a person is needed":     {droppedPublication, func(c *mergeRequestCase) { c.forge.Status, c.says = "BLOCKED", "only a person" }},
		"no checks wired":        {unarmedPublicationRun, func(c *mergeRequestCase) { c.noChecks, c.says = true, "nothing is wired" }},
		"checks unreadable":      {unarmedPublicationRun, func(c *mergeRequestCase) { c.checks.err, c.says = errors.New("down"), "read the checks" }},
		"head behind":            {unarmedPublicationRun, func(c *mergeRequestCase) { c.checks.reading.BehindBy, c.says = 2, "behind the target" }},
		"checks failing": {unarmedPublicationRun, func(c *mergeRequestCase) {
			c.checks.reading.Failing, c.says = []publish.FailedCheck{{Name: "test"}}, "checks fail"
		}},
		"red target, checks failing": {waiting, func(c *mergeRequestCase) {
			c.checks.reading.Failing, c.says = []publish.FailedCheck{{Name: "test"}}, "withdrawn for its target's red check"
		}},
		"target fails its check":   {droppedPublication, func(c *mergeRequestCase) { c.worktree.Failure, c.says = errors.New("moved"), "check the remote target" }},
		"re-arm not recorded":      {droppedPublication, func(c *mergeRequestCase) { c.runs.saveFails, c.says = 1, "record the re-arm" }},
		"merge refused":            {droppedPublication, func(c *mergeRequestCase) { c.forge.MergeErr, c.says = errors.New("no"), "repeat the merge request" }},
		"queued but not recorded":  {droppedPublication, func(c *mergeRequestCase) { c.runs.saveFails = 2 }},
		"queued":                   {droppedPublication, func(*mergeRequestCase) {}},
		"merged at once":           {droppedPublication, func(c *mergeRequestCase) { c.forge.Result.Queued = false }},
		"stopped while publishing": {droppedPublication, func(c *mergeRequestCase) { c.state.StopClass = runstate.StopPublish }},
		"armed for the first time": {unarmedPublicationRun, func(*mergeRequestCase) {}},
		"armed after a red target": {waiting, func(*mergeRequestCase) {}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newMergeRequestCase(t, test.state())
			test.arrange(c)
			c.record(t)
			unused := failsIfUsed{t}
			rearmer := Rearmer{
				Runs: c.runs, Forge: merging{RearmForge: c.forge, noProvider: noProvider{unused}}, LandingHolds: c.holds,
				Worktrees: target{RemoteTarget: c.worktree, noChecks: noChecks{unused}}, Items: noClaim{failsIfUsed: unused}, Clock: docketClock{},
			}
			if !c.noChecks {
				rearmer.Checks = c.checks
			}
			published := c.state.PullRequest
			result, err := rearmer.repeat(context.Background(), c.state.RunID, "main", published.MergeRearms, RearmResult{
				Number: c.number, FirstArm: c.state.PublicationUnarmed(), TargetRed: c.state.WaitingOnRedTarget(),
			})
			switch {
			case c.says == "" && (err != nil || !result.Rearmed):
				t.Errorf("repeat() = %+v, %v; want it made", result, err)
			case c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)):
				t.Errorf("repeat() error = %v, want it to say %q", err, c.says)
			}
			c.startedNothing(t)
		})
	}
}

func TestFinishingAPublicationStartsNoWorkOnAnyBranch(t *testing.T) {
	t.Parallel()
	for name, arrange := range map[string]func(*mergeRequestCase){
		"forge unreadable":  func(c *mergeRequestCase) { c.forge.ObserveErr, c.says = errors.New("down"), "ask the forge" },
		"no request":        func(c *mergeRequestCase) { c.forge.Observed, c.says = publish.PullRequest{}, "no pull request" },
		"the run is held":   func(c *mergeRequestCase) { c.runs.adopt, c.says = runstate.ErrRunHeld, "a live process holds" },
		"run not adopted":   func(c *mergeRequestCase) { c.runs.adopt, c.says = errors.New("gone"), "adopt run" },
		"settled meanwhile": func(c *mergeRequestCase) { c.state.PublishFailure, c.says = "", "was settled while" },
		"merged":            func(c *mergeRequestCase) { c.forge.Observed.Merged, c.says = true, "reports pull request 92 merged" },
		"merged, not recorded": func(c *mergeRequestCase) {
			c.forge.Observed.Merged, c.runs.saveFails, c.says = true, 1, "record pull request"
		},
		"queued by hand": func(c *mergeRequestCase) { c.forge.Observed.AutoMerge, c.says = true, "already holds a merge" },
		"queued, not recorded": func(c *mergeRequestCase) {
			c.forge.Observed.AutoMerge, c.runs.saveFails, c.says = true, 1, "record pull request"
		},
		"head moved": func(c *mergeRequestCase) { c.forge.Observed.HeadCommit, c.says = rearmedBase, "carries "+rearmedBase },
		"drop not recorded": func(c *mergeRequestCase) {
			c.forge.Observed.HeadCommit, c.runs.saveFails, c.says = rearmedBase, 1, "could not say why"
		},
		"no turn to promote": func(c *mergeRequestCase) { c.runs.lease, c.says = errors.New("held"), "wait for a turn" },
		"hold unreadable":    func(c *mergeRequestCase) { c.holds, c.says = unreadableLandingHolds{}, "garbled" },
		"landing held":       func(c *mergeRequestCase) { c.holds, c.says = standingLanding{}, "nothing was asked of the forge" },
		"target fails":       func(c *mergeRequestCase) { c.worktree.Failure, c.says = errors.New("moved"), "check the remote target" },
		"merge refused": func(c *mergeRequestCase) {
			c.forge.MergeErr, c.says = errors.New("requires a review"), "requires a review"
		},
		"armed, not recorded": func(c *mergeRequestCase) { c.runs.saveFails, c.says = 1, "was armed and the run's record" },
		"armed":               func(*mergeRequestCase) {},
		"armed, head unnamed": func(c *mergeRequestCase) { c.forge.Observed.HeadCommit = "" },
		"of a document": func(c *mergeRequestCase) {
			c.seen = func(state *runstate.State) { state.OutsideFile = &runstate.OutsideFileLanding{} }
		},
		"of another repository": func(c *mergeRequestCase) {
			c.seen = func(state *runstate.State) { state.Repository = runstate.RepositoryIntent }
		},
		"not approved": func(c *mergeRequestCase) {
			c.says = "nothing authorizes the merge"
			c.seen = func(state *runstate.State) { state.ReviewDecision = runstate.ReviewRepair }
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newMergeRequestCase(t, lostPublicationState())
			arrange(c)
			c.record(t)
			unused := failsIfUsed{t}
			worktrees := target{RemoteTarget: c.worktree, noChecks: noChecks{unused}}
			reconciler := Reconciler{
				Tracker: noClaim{failsIfUsed: unused}, Store: c.runs, Worktrees: worktrees, IntentWorktrees: worktrees,
				Publisher: merging{RearmForge: c.forge, noProvider: noProvider{unused}}, LandingHolds: c.holds, Clock: docketClock{},
				Continue: func(context.Context, string, string) (Outcome, error) {
					return Outcome{}, unused.used("a run's continuation")
				},
				MayStart: func(maystart.Start) maystart.Decision {
					return maystart.Decision{Answer: maystart.MayNot, Why: unused.used("the decision on a start").Error()}
				},
			}
			if c.seen != nil {
				c.seen(&c.state)
			}
			var recovery PublicationRecovery
			if name == "not approved" {
				// The run is not selected then; the action refuses it too.
				recovery = reconciler.armRecoveredMerge(context.Background(), c.state, runstate.PullRequest{Number: 92, HeadCommit: rearmedCommit}, PublicationRecovery{})
			} else {
				recovery = reconciler.recoverPublication(context.Background(), forgeAnswers{}, c.state)
			}
			said := recovery.Failure + recovery.Refused + recovery.Kept
			if c.says == "" && (!recovery.Armed || said != "") || !strings.Contains(said, c.says) {
				t.Errorf("recovery = %+v, want it to say %q", recovery, c.says)
			}
			c.startedNothing(t)
		})
	}
}
