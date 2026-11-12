package orchestrator

// Taking up runs that stopped only because the harness could not reach GitHub
// as itself: the SSH key the remote refused, a machine that could not name the
// user it runs as, the GitHub command line tool not logged in.
//
// Nothing is wrong with the change in such a run, and nothing a person decides
// is needed to move it, only GitHub answering the harness's identity again. So
// the harness looks for itself, on the look it already makes every few minutes
// at the changes the merge queue held (MergeQueueDriver.TakeHeld), and never
// against a clock: a run is taken up at the first look that finds GitHub
// answering with the identity it would publish under, and not before.
//
// Two kinds of run are taken up, each where it stopped.
//
//   - An approved change stopped at its landing (IntegrationStop with the
//     remote-auth-refused cause) is resumed at its promotion by
//     IntegrationResumer, with its approval, passed checks and repair count
//     kept and nothing spent. The resumer asks the remote itself once more
//     before it writes anything.
//   - A change stopped while publishing its branch, before its checks ran
//     (IdentityStop), is taken up at its checks on the same branch and in the
//     same worktree, owing its push. It is pushed once the checks pass and
//     before its review, so the harness never puts a change on the forge that
//     it has not checked. No developer is invoked, and no repair attempt,
//     review round or re-run is spent. It is taken up at most
//     runstate.MaxIdentityTakeUps times; past that the stoppage is the
//     development manager's.
//
// GitHub is asked once per target branch per look, so a look over many stopped
// runs makes one call. While GitHub does not answer, nothing is written to any
// run, item or docket entry: the stoppage stays as it is for the development
// manager, and the look says so. A change whose branch or checkout is gone, or
// cannot be verified, is not taken up and not restored; that is the
// development manager's to decide.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// GitHubIdentityStops takes up the runs GitHub's refusal of the harness's
// identity stopped, once GitHub answers that identity again.
type GitHubIdentityStops struct {
	// Docket is where the stoppages are listed, and where a taken-up run's entry
	// is closed.
	Docket ResumeDocket
	// Redocket is accepted for wiring beside CheckStageContinuer; a run that
	// cannot be taken up is left on the docket as it is, so nothing is put back.
	Redocket  CheckStageRedocket
	Runs      RepairRuns
	Intake    IntakeHolds
	Items     RepairItems
	Worktrees RepairWorktrees
	// Capacity is execution.max_concurrent_developers.
	Capacity int
	// Answers asks GitHub, with the identity the harness publishes under, whether
	// it answers for this target branch: a nil error means it did.
	Answers func(ctx context.Context, targetBranch string) error
	// ResumeLanding resumes an approved change stopped at its landing. It is
	// satisfied by IntegrationResumer.Resume given the reason.
	ResumeLanding func(ctx context.Context, runID, reason string) (IntegrationResumeResult, error)
	// Start continues a run taken up at its checks, as CheckStageContinuer.Start.
	Start RepairContinueStarter
	Clock execution.Clock
}

// GitHubIdentityPass is what one look at the runs GitHub's refusal stopped found
// and did.
type GitHubIdentityPass struct {
	// Stopped is how many such runs were found, and TakenUp how many of them were
	// taken up.
	Stopped int `json:"stopped"`
	TakenUp int `json:"taken_up"`
	// Says is the look's account, in ordinary words.
	Says string `json:"says,omitempty"`
}

// githubIdentityDecidedBy is who the docket names as having closed the entry of
// a run taken up this way.
const githubIdentityDecidedBy = "the harness, taking up a run GitHub refused once it answered again"

// Take looks at every run GitHub's refusal of the harness's identity stopped and
// takes up those whose target GitHub now answers for. It never returns an error:
// what stopped it is its account.
func (g GitHubIdentityStops) Take(ctx context.Context) GitHubIdentityPass {
	var pass GitHubIdentityPass
	if g.Docket == nil || g.Runs == nil || g.Answers == nil {
		return pass
	}
	entries, err := g.Docket.List()
	if err != nil {
		pass.Says = fmt.Sprintf("the runs GitHub's refusal of the harness's identity stopped could not be listed: %v", err)
		return pass
	}
	answered := map[string]error{}
	var says []string
	for _, entry := range entries {
		if entry.Class != triage.ClassStoppedRun || entry.Closed != nil {
			continue
		}
		if ctx.Err() != nil {
			says = append(says, "the session taking up the runs stopped; the next look carries on")
			break
		}
		run, err := g.Runs.Load(entry.RunID)
		if err != nil {
			continue
		}
		publishing := run.IdentityStop != nil && run.Status.Terminal() && run.Integration == nil && run.Phase == runstate.PhaseDeveloping
		landing := run.IntegrationStop != nil && run.IntegrationStop.Cause == runstate.CauseRemoteAuthRefused
		if !publishing && !landing {
			// A run whose record no longer says it stopped for this was taken up
			// already or settled some other way, and its entry closes on its own terms.
			continue
		}
		pass.Stopped++
		where := "landing"
		if publishing {
			where = "publishing"
		}
		refused, asked := answered[run.TargetBranch]
		if !asked {
			refused = g.Answers(ctx, run.TargetBranch)
			answered[run.TargetBranch] = refused
		}
		if refused != nil {
			says = append(says, fmt.Sprintf("run %s of %s stays stopped at %s for the development manager: GitHub still does not accept the harness's identity: %s",
				run.RunID, run.WorkItemID, where, singleLine(refused.Error(), 300)))
			continue
		}
		var tookUp bool
		var account string
		var wait bool
		if publishing {
			tookUp, account, wait = g.takeUpPublishing(ctx, entry, run)
		} else {
			tookUp, account, wait = g.takeUpLanding(ctx, run)
		}
		if tookUp {
			pass.TakenUp++
		}
		if account != "" {
			says = append(says, account)
		}
		if wait {
			// Intake held or no slot free is the same for every run behind this one.
			break
		}
	}
	pass.Says = strings.Join(says, ". ")
	return pass
}

// takeUpLanding resumes an approved change stopped at its landing, and says what
// became of it. wait reports a hold or a full harness, which the next look asks
// about again.
func (g GitHubIdentityStops) takeUpLanding(ctx context.Context, run runstate.State) (tookUp bool, says string, wait bool) {
	if g.ResumeLanding == nil {
		return false, fmt.Sprintf("run %s of %s stays stopped at landing for the development manager: nothing here can resume it", run.RunID, run.WorkItemID), false
	}
	now := g.now()
	reason := fmt.Sprintf("GitHub answered again with the same identity at %s, so the harness took the approved change up again at its landing, with its approval, passed checks and repair count kept and no repair attempt or re-run spent.", runstate.LocalStamp(now))
	result, err := g.ResumeLanding(ctx, run.RunID, reason)
	switch {
	case result.Resumed:
		says = fmt.Sprintf("run %s of %s was taken up again at its landing", run.RunID, run.WorkItemID)
		if err != nil {
			says += fmt.Sprintf(" and then ended: %s", singleLine(err.Error(), 300))
		}
		return true, says, false
	case result.IntakeHeld != nil:
		return false, fmt.Sprintf("run %s of %s waits at landing: intake is held, so nothing is taken up until it is released; it is asked again next look", run.RunID, run.WorkItemID), true
	case result.CapacityFull != nil:
		return false, fmt.Sprintf("run %s of %s waits at landing: every developer slot is occupied (%d of %d); it is asked again next look", run.RunID, run.WorkItemID, result.CapacityFull.Active, result.CapacityFull.Limit), true
	case err != nil:
		return false, fmt.Sprintf("run %s of %s stays stopped at landing for the development manager: it could not be taken up: %s", run.RunID, run.WorkItemID, singleLine(err.Error(), 300)), false
	}
	return false, fmt.Sprintf("run %s of %s was not taken up at its landing", run.RunID, run.WorkItemID), false
}

// takeUpPublishing takes a change that stopped while publishing up at its
// checks, owing its push. It is held to what a continuation at the checks is
// held to, except that a missing branch or checkout is never restored: it is
// left for the development manager, because the push this run owes is of exactly
// the change that was on that branch.
//
// The order is the continuation's: everything that can refuse is asked before
// anything is written, the item is put back before the run is made live, and the
// docket entry is closed last.
func (g GitHubIdentityStops) takeUpPublishing(ctx context.Context, entry triage.Entry, listed runstate.State) (tookUp bool, says string, wait bool) {
	stays := func(why string) (bool, string, bool) {
		return false, fmt.Sprintf("run %s of %s stays stopped at publishing for the development manager: %s", listed.RunID, listed.WorkItemID, why), false
	}
	if err := g.validatePublishing(); err != nil {
		return stays("it cannot be taken up from here: " + singleLine(err.Error(), 300))
	}
	prior, lease, err := g.Runs.AdoptRun(ctx, entry.RunID)
	if err != nil {
		return stays("the run could not be taken to continue it: " + singleLine(err.Error(), 300))
	}
	defer lease.Release()

	if prior.IdentityStop == nil || !prior.Status.Terminal() || prior.Integration != nil || prior.Phase != runstate.PhaseDeveloping {
		// It moved since it was listed, so there is nothing here to take up.
		return false, "", false
	}
	if prior.IdentityTakeUpsLeft() == 0 {
		return stays(fmt.Sprintf("the harness has taken it up %d times already after GitHub refused its identity, and the stoppage is a person's to look at", len(prior.IdentityTakeUps)))
	}
	if err := noRunInFlight(g.Runs, entry.WorkItemID); err != nil {
		return stays(singleLine(err.Error(), 300))
	}
	item, err := g.Items.Show(ctx, entry.WorkItemID)
	if err != nil {
		return stays("the work item could not be read: " + singleLine(err.Error(), 300))
	}
	if err := continuableItem(releaseTeamMergedFrom(g.Runs, item, prior.TargetBranch), entry.WorkItemID); err != nil {
		return stays(singleLine(err.Error(), 300))
	}
	_, held, err := g.Intake.Held()
	if err != nil {
		return stays("whether intake is held could not be read: " + singleLine(err.Error(), 300))
	}
	if held {
		return false, fmt.Sprintf("run %s of %s waits at publishing: intake is held, so nothing is taken up until it is released; it is asked again next look", prior.RunID, prior.WorkItemID), true
	}
	full, free, err := slotIsFree(g.Runs, g.Capacity)
	if err != nil {
		return stays(singleLine(err.Error(), 300))
	}
	if !free {
		return false, fmt.Sprintf("run %s of %s waits at publishing: every developer slot is occupied (%d of %d); it is asked again next look", prior.RunID, prior.WorkItemID, full.Active, full.Limit), true
	}
	remains, _ := g.Worktrees.(readmodel.Remains)
	found := readmodel.LookFor(ctx, remains, prior)
	if found.Unknown || !found.BranchThere || !found.WorktreeThere {
		return stays(fmt.Sprintf("its branch or checkout is gone or could not be verified, and the harness does not restore one for a push it owes: %s", found.Describe()))
	}
	if err := g.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
		return stays(singleLine(WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}.Error(), 300))
	}
	if err := preservedChangeHeld(ctx, g.Worktrees, prior); err != nil {
		return stays(singleLine(MissingPreservedChangeError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}.Error(), 300))
	}

	now := g.now()
	reason := fmt.Sprintf("Taken up again by the harness at %s: run %s stopped while publishing its branch only because GitHub would not accept the harness's identity (%s), and GitHub has now answered with the same identity again. It goes on at its checks on the same branch and in the same worktree, and pushes its branch once they pass and before its review. Its repair count is kept; no repair attempt or re-run was spent.",
		runstate.LocalStamp(now), prior.RunID, prior.IdentityStop.Detail)
	if _, err := g.Items.RecordOutcome(ctx, entry.WorkItemID, reason); err != nil {
		return stays("taking it up could not be noted on the work item: " + singleLine(err.Error(), 300))
	}
	claimed, _, err := g.Items.Claim(ctx, entry.WorkItemID)
	if err != nil {
		return stays("the work item could not be put back to work: " + singleLine(err.Error(), 300))
	}
	if err := validateClaimedItem(releaseTeamMergedFrom(g.Runs, claimed, prior.TargetBranch), entry.WorkItemID); err != nil {
		return stays("the work item put back to work was not one to continue on: " + singleLine(err.Error(), 300))
	}
	if err := g.Runs.ReserveLive(ctx, prior.TakenUpAtChecks(reason, now), g.Capacity); err != nil {
		if capacity, taken := slotTakenMeanwhile(err); taken {
			noteSlotTaken(ctx, g.Items, entry.WorkItemID, prior.RunID, capacity)
			return false, fmt.Sprintf("run %s of %s waits at publishing: the last developer slot went to another run (%d of %d); it is asked again next look", prior.RunID, prior.WorkItemID, capacity.Active, capacity.Limit), true
		}
		return stays(fmt.Sprintf("taking it up could not be recorded on the run, whose item has already been told why: %s", singleLine(err.Error(), 300)))
	}
	says = fmt.Sprintf("run %s of %s was taken up again at its checks, owing its push", prior.RunID, prior.WorkItemID)
	if err := g.closeEntry(entry, reason, now); err != nil {
		says += fmt.Sprintf("; its docket entry could not be closed: %s", singleLine(err.Error(), 300))
	}
	// Given up before the run is continued, because continuing it is the pipeline
	// adopting the same run.
	lease.Release()
	if _, err := g.Start(ctx, entry.WorkItemID, prior.RunID); err != nil {
		says += fmt.Sprintf("; it then ended: %s", singleLine(err.Error(), 300))
	}
	return true, says, false
}

// closeEntry takes the stoppage off the docket in the harness's own name.
func (g GitHubIdentityStops) closeEntry(entry triage.Entry, reason string, now time.Time) error {
	_, err := g.Docket.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entry.Key,
		ProductID:     entry.ProductID,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		Decision:      triage.ContinuedChecksDecision,
		Reason:        singleLine(reason, triage.MaxMessageBytes),
		DecidedBy:     githubIdentityDecidedBy,
		ClosedAt:      now,
	})
	return err
}

// validatePublishing reports what taking up a run at its checks needs that this
// was wired without.
func (g GitHubIdentityStops) validatePublishing() error {
	var problems []error
	if g.Intake == nil {
		problems = append(problems, errors.New("taking a run up requires the intake hold, because carrying work on is the harness choosing to"))
	}
	if g.Items == nil {
		problems = append(problems, errors.New("taking a run up requires the work item the stopped run holds"))
	}
	if g.Worktrees == nil {
		problems = append(problems, errors.New("taking a run up requires the worktree, because what the checks judge is whatever is in it"))
	}
	if g.Start == nil {
		problems = append(problems, errors.New("taking a run up requires a way to continue it"))
	}
	if g.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which runs nothing", g.Capacity))
	}
	return errors.Join(problems...)
}

func (g GitHubIdentityStops) now() time.Time {
	if g.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return g.Clock.Now().UTC()
}
