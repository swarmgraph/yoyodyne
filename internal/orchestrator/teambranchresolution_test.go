package orchestrator

// Resolving a conflict between the team's branch and the target branch by a run
// of the item the development manager names, over the same real-Git fixture the
// update's own tests use and a fake developer that writes into the checkout it
// is given.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	conflictItemID    = "yoyodyne-staging-item"
	conflictItemTitle = "Changes the second line"
	resolvedApp       = "one\nstaging two and team two\nthree\n"
)

// conflictFixture is a target and a team's branch that disagree about the
// second line of app.txt, with the item that changed it on the target waiting
// for the team's merge.
func newConflictFixture(t *testing.T) (*teamUpdateFixture, string) {
	t.Helper()
	f := newTeamUpdateFixture(t)
	f.tracker.Item = beads.WorkItem{ID: conflictItemID, Title: conflictItemTitle, Description: "Makes the second line of app.txt say staging.", Status: "in_progress"}
	landing := f.landsOnTarget("app.txt", "one\nstaging two\nthree\n", "staging changes the second line")
	f.waiting.runs = []runstate.State{{
		RunID: "run-staging-item", WorkItemID: conflictItemID, WorkItemTitle: conflictItemTitle, Status: runstate.StatusSucceeded,
		Integration: &runstate.Integration{TargetBranch: "staging", SourceCommit: landing, TargetCommit: landing},
		TeamMerge:   &runstate.TeamMerge{TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: landing, Since: time.Now()},
	}}
	tip := f.teamMerges("app.txt", "one\nteam two\nthree\n", "the team changes the second line")
	return f, tip
}

// resolvesApp is a developer that resolves the conflict in app.txt.
func resolvesApp(request backend.RunRequest, _ int) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "app.txt"), []byte(resolvedApp), 0o600)
}

// leavesMarkers is a developer that writes something else and leaves the
// conflict where it was.
func leavesMarkers(request backend.RunRequest, attempt int) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "notes.txt"), []byte(strings.Repeat("a note\n", attempt)), 0o600)
}

func (f *teamUpdateFixture) decide(reason string) runstate.TeamBranchUpdate {
	f.t.Helper()
	decided, err := f.records.DecideResolution(context.Background(), runstate.TeamBranchResolutionDecision{
		TargetBranch: "staging", WorkItemID: conflictItemID, WorkItemTitle: conflictItemTitle, Reason: reason,
		DecidedBy: "development-manager", Conversation: "dm-1", Turn: 4, At: time.Now(),
	})
	if err != nil {
		f.t.Fatalf("DecideResolution() error = %v", err)
	}
	return decided
}

func TestAConflictReportNamesItemsByTitleWithTheirIdentifierAndSaysHowToResolveIt(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.update()
	record := f.record()
	items := record.Pending.Conflict.Items
	if len(items) != 1 || items[0].Title != conflictItemTitle || items[0].Says() != "Changes the second line (yoyodyne-staging-item)" {
		t.Fatalf("conflict items = %#v, want the title with the identifier after it", items)
	}
	told := f.toldTheDevelopmentManager()
	if len(told) != 1 || !strings.Contains(told[0].Message, "Changes the second line (yoyodyne-staging-item)") ||
		!strings.Contains(told[0].Message, `{"action":"resolve-conflict","target":"staging","id":"<the item's id>","reason":"<why this item>"}`) || !strings.Contains(told[0].Message, "3 such rounds") ||
		!strings.Contains(told[0].Message, "not counted against the item's repair attempts") {
		t.Fatalf("report = %#v, want the item by title and how she decides", told)
	}
	says := strings.Join(record.Says(), "\n")
	if !strings.Contains(says, "waiting for the development manager to name an item to resolve it") {
		t.Fatalf("Says() = %q, want it to say it waits for her decision", says)
	}
}

func TestADecidedResolutionIsRunCheckedReviewedAndLanded(t *testing.T) {
	t.Parallel()

	f, tip := newConflictFixture(t)
	f.develop = resolvesApp
	base := f.local("staging")

	f.update()
	f.decide("it changed that line on staging, so it knows what the line is for")
	if says := strings.Join(f.record().Says(), "\n"); !strings.Contains(says, "the development manager decided that Changes the second line (yoyodyne-staging-item) resolves it (round 1 of 3 starts at the next check)") {
		t.Fatalf("Says() after the decision = %q", says)
	}

	outcome := f.update()
	landed := f.local("staging")
	if outcome.Landed != landed || landed == base || outcome.Waiting != "" || outcome.Conflict != nil {
		t.Fatalf("Update() = %#v; staging was %s and is %s; want the resolved merge landed", outcome, base, landed)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", landed); parents != landed+" "+base+" "+tip {
		t.Fatalf("landed parents = %q, want the target then the team's tip", parents)
	}
	if got := readQueueFile(t, f.repository, "app.txt"); got != resolvedApp {
		t.Fatalf("app.txt on staging = %q, want the developer's resolution", got)
	}
	if f.developed != 1 {
		t.Fatalf("the developer was asked %d time(s), want once", f.developed)
	}

	record := f.record()
	if record.Pending != nil || len(record.Landed) != 1 {
		t.Fatalf("record = %#v, want one landed update", record)
	}
	attempt := record.Landed[0]
	resolution := attempt.Resolution
	if resolution == nil || len(resolution.Rounds) != 1 || resolution.Rounds[0].Resolved == "" || resolution.Rounds[0].EndedAt == nil ||
		resolution.WorkItemID != conflictItemID || resolution.DecidedBy != "development-manager" || resolution.RoundsLeft() != 2 {
		t.Fatalf("landed attempt's resolution = %#v, want one resolved round of the named item", resolution)
	}
	if attempt.LandedCommit != landed || attempt.Candidate != landed || !attempt.ChecksPassed() || !attempt.Approved() {
		t.Fatalf("landed attempt = %#v, want the merge checked and approved", attempt)
	}
	// The merge that landed was checked and reviewed as itself, and the reviewer
	// was told the conflicts were resolved by the run.
	if !strings.Contains(filepath.Base(f.checks.directory()), "candidate-") {
		t.Fatalf("the last checks ran in %q, want the merge's own checkout", f.checks.directory())
	}
	if last := f.reviewer.last(); last.Branch.HeadCommit != landed || !strings.Contains(last.Context, "resolved those conflicts for Changes the second line (yoyodyne-staging-item)") || !strings.Contains(last.Context, resolution.Rounds[0].RunID) {
		t.Fatalf("the merge was reviewed with context %q at %s", last.Context, last.Branch.HeadCommit)
	}

	// The run is a run of the harness's own, ended at its approval, and charged
	// to nothing the item owns.
	runID := resolution.Rounds[0].RunID
	run, err := f.waiting.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != runstate.StatusSucceeded || run.TeamBranchResolution == nil || run.TeamBranchResolution.ApprovedHead != resolution.Rounds[0].Resolved ||
		run.Integration != nil || run.MergeQueue != nil || run.UpstreamBranch != "" || run.RepairAttempts != 0 || !strings.HasPrefix(run.WorkItemID, runstate.TeamBranchResolutionItemPrefix) {
		t.Fatalf("run = %#v, want a succeeded run that integrated nothing under its own item", run)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", run.TeamBranchResolution.ApprovedHead); !strings.HasPrefix(parents, run.TeamBranchResolution.ApprovedHead+" "+run.TeamBranchResolution.ConflictedBase) {
		t.Fatalf("approved head parents = %q, want the conflicted base", parents)
	}
	if author := gitLine(t, f.repository, "log", "-1", "--format=%an", run.TeamBranchResolution.ApprovedHead); !strings.Contains(strings.ToLower(author), "harness") && !strings.Contains(strings.ToLower(author), "yoyodyne") {
		t.Fatalf("approved head author = %q, want the harness's identity", author)
	}

	// The real item was only written a note on.
	if f.tracker.Closed || f.tracker.Blocked || f.tracker.Released || f.tracker.Reopened || f.tracker.Item.Status != "in_progress" || f.tracker.Claimed {
		t.Fatalf("the real item was changed: %#v", f.tracker.Record())
	}
	if calls := slices.Compact(slices.Clone(f.tracker.Calls)); len(calls) != 1 || calls[0] != "record" {
		t.Fatalf("tracker calls = %v, want notes only", f.tracker.Calls)
	}
	if !strings.Contains(f.tracker.Notes, "started run "+runID) || !strings.Contains(f.tracker.Notes, "resolved the conflict") {
		t.Fatalf("notes on the item = %q, want the start and the end of the round", f.tracker.Notes)
	}
	if told := f.toldTheDevelopmentManager(); len(told) != 1 {
		t.Fatalf("%d reports, want only the conflict's", len(told))
	}
}

func TestAResolutionThatLeavesConflictMarkersIsHandedBackToTheDeveloper(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.develop = func(request backend.RunRequest, attempt int) error {
		if attempt == 1 {
			// Touches the file and keeps one of Git's markers in it.
			marked, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "app.txt"))
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "app.txt"), append(marked, []byte("tidied\n")...), 0o600)
		}
		return resolvesApp(request, attempt)
	}
	f.update()
	f.decide("it knows the line")

	outcome := f.update()
	if outcome.Landed == "" || f.developed != 2 {
		t.Fatalf("Update() = %#v after %d developer call(s), want it landed after the marker was handed back", outcome, f.developed)
	}
	round := f.record().Landed[0].Resolution.Rounds[0]
	run, err := f.waiting.Load(round.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.RepairAttempts != 1 {
		t.Fatalf("the run's repair attempts = %d, want one for the marker", run.RepairAttempts)
	}
	repair := f.provider.RequestsForRole("developer")
	if len(repair) != 2 || !strings.Contains(repair[1].Prompt, ConflictMarkersCheck) || !strings.Contains(repair[1].Prompt, "app.txt:") {
		t.Fatalf("repair prompt = %q, want the marker check handed back naming the file", repair[len(repair)-1].Prompt)
	}
	if got := readQueueFile(t, f.repository, "app.txt"); strings.Contains(got, "<<<<<<<") {
		t.Fatalf("app.txt on staging holds a marker: %q", got)
	}
}

func TestTheFourthRoundIsRefusedAndTheDevelopmentManagerIsToldOnceTheRoundsAreSpent(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.develop = leavesMarkers
	staging := f.local("staging")
	f.update()
	f.decide("it knows the line")

	for round := 1; round <= runstate.MaxTeamBranchResolutionRounds; round++ {
		outcome := f.update()
		if outcome.Landed != "" || len(outcome.Conflict) != 1 || outcome.Waiting == "" {
			t.Fatalf("round %d: Update() = %#v, want it waiting on the conflict", round, outcome)
		}
		resolution := f.record().Pending.Resolution
		if len(resolution.Rounds) != round || resolution.Rounds[round-1].EndedAt == nil || resolution.Rounds[round-1].Why == "" || resolution.Rounds[round-1].Resolved != "" {
			t.Fatalf("round %d: resolution = %#v, want %d round(s) each ended with why", round, resolution, round)
		}
		if round < runstate.MaxTeamBranchResolutionRounds && !strings.Contains(outcome.Waiting, "the next check starts round") {
			t.Fatalf("round %d: waiting = %q, want it to say the next round starts at the next check", round, outcome.Waiting)
		}
	}
	asked := f.developed
	if told := f.toldTheDevelopmentManager(); len(told) != 2 || !strings.Contains(told[1].Message, "All 3 rounds of resolving the conflict") || !strings.Contains(told[1].Message, "Changes the second line (yoyodyne-staging-item)") {
		t.Fatalf("reports = %#v, want the conflict's and one that says the rounds are spent", told)
	}

	// A fourth check starts no run, whatever the decision says, and tells nobody
	// again.
	outcome := f.update()
	if f.developed != asked || !strings.Contains(outcome.Waiting, "all 3 rounds of resolving it are used") || len(f.toldTheDevelopmentManager()) != 2 {
		t.Fatalf("fourth Update() = %#v with %d developer call(s) after %d, want no run and no new report", outcome, f.developed, asked)
	}
	if says := strings.Join(f.record().Says(), "\n"); !strings.Contains(says, "rounds of resolving it are spent (3 of 3)") {
		t.Fatalf("Says() = %q, want the rounds spent", says)
	}
	if _, err := f.records.DecideResolution(context.Background(), runstate.TeamBranchResolutionDecision{
		TargetBranch: "staging", WorkItemID: conflictItemID, Reason: "again", DecidedBy: "development-manager",
	}); err == nil || !strings.Contains(err.Error(), "all 3 rounds of resolving this conflict have been used") {
		t.Fatalf("DecideResolution() after the rounds were spent = %v, want it refused in plain words", err)
	}
	if f.local("staging") != staging || f.tracker.Closed || f.tracker.Item.Status != "in_progress" {
		t.Fatal("a refused round moved the target or changed the item")
	}
}

func TestTheDecisionAndTheRoundsSpentCarryOverWhenTheTargetMovesAndTheTipStays(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.develop = leavesMarkers
	f.update()
	f.decide("it knows the line")
	f.update()
	if got := len(f.record().Pending.Resolution.Rounds); got != 1 {
		t.Fatalf("%d round(s) after one check, want 1", got)
	}

	// Unrelated work lands on the target; the merge is built again on it, meets
	// the same conflict, and the decision and the round it spent are still there.
	f.landsOnTarget("other.txt", "other\n", "unrelated work lands")
	moved := f.update()
	pending := f.record().Pending
	if len(moved.Conflict) != 1 || pending == nil || pending.Resolution == nil || pending.Resolution.WorkItemID != conflictItemID || len(pending.Resolution.Rounds) != 1 || pending.Resolution.RoundsLeft() != 2 {
		t.Fatalf("after the target moved: %#v, resolution %#v; want the decision and one spent round carried", moved, pending)
	}
	if told := f.toldTheDevelopmentManager(); len(told) != 1 {
		t.Fatalf("%d reports after the target moved, want the conflict's only", len(told))
	}
	f.update()
	if got := len(f.record().Pending.Resolution.Rounds); got != 2 {
		t.Fatalf("%d round(s) after the next check, want 2: the limit is the conflict's, not the target's", got)
	}

	// A new tip starts afresh: nothing of the decision is kept for another merge.
	f.teamMerges("app.txt", "one\nteam two again\nthree\n", "the team changes it again")
	f.update()
	if p := f.record().Pending; p == nil || p.Resolution != nil {
		t.Fatalf("pending after a new team tip = %#v, want no decision carried", p)
	}
}

func TestAnUpdaterWithNoWayToStartARunWaitsOnTheDecision(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.updater.Resolve = nil
	f.update()
	f.decide("it knows the line")
	outcome := f.update()
	if f.developed != 0 || !strings.Contains(outcome.Waiting, "cannot start the run") {
		t.Fatalf("Update() = %#v, want it waiting because it cannot start a run", outcome)
	}
}

func TestAnUpdateWithNoWorkToDoIsNotAskedToResolveAnything(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	f.update()
	if f.developed != 0 || len(f.provider.RequestsForRole("developer")) != 0 {
		t.Fatal("a developer was asked about a merge that did not conflict")
	}
	var told []report.Report = f.toldTheDevelopmentManager()
	if len(told) != 0 {
		t.Fatalf("%d reports for a clean merge", len(told))
	}
}

func TestARoundWhoseProcessDiedIsSettledFromItsRunRecordAtTheNextCheck(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.develop = resolvesApp
	f.update()
	f.decide("it knows the line")

	// The check that made the round ends after the run did, before it could
	// record what it came to.
	resolve := f.updater.Resolve
	dies := true
	f.updater.Resolve = func(ctx context.Context, request TeamBranchResolutionRequest) (TeamBranchResolutionOutcome, error) {
		outcome, err := resolve(ctx, request)
		if dies {
			dies = false
			return TeamBranchResolutionOutcome{}, errors.New("the process was stopped")
		}
		return outcome, err
	}
	if _, err := f.updater.Update(context.Background()); err == nil {
		t.Fatal("Update() succeeded, want the stopped process's error")
	}
	resolution := f.record().Pending.Resolution
	if round := resolution.InProgress(); round == nil || len(resolution.Rounds) != 1 {
		t.Fatalf("resolution = %#v, want the one round recorded as running", resolution)
	}
	if _, err := f.records.DecideResolution(context.Background(), runstate.TeamBranchResolutionDecision{TargetBranch: "staging", WorkItemID: conflictItemID, Reason: "again", DecidedBy: "development-manager"}); err == nil || !strings.Contains(err.Error(), "is running now") {
		t.Fatalf("DecideResolution() during the round = %v, want it refused", err)
	}

	outcome := f.update()
	if outcome.Landed == "" || f.developed != 1 {
		t.Fatalf("Update() = %#v after %d developer call(s), want the finished run settled and its resolution landed without another developer", outcome, f.developed)
	}
	if rounds := f.record().Landed[0].Resolution.Rounds; len(rounds) != 1 || rounds[0].Resolved == "" || rounds[0].EndedAt == nil {
		t.Fatalf("rounds = %#v, want the one round settled as resolved", rounds)
	}
}

func TestARoundThatCannotStartForWantOfADeveloperSlotIsNotUsed(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.develop = resolvesApp
	f.update()
	f.decide("it knows the line")
	asked := false
	resolve := f.updater.Resolve
	f.updater.Resolve = func(ctx context.Context, request TeamBranchResolutionRequest) (TeamBranchResolutionOutcome, error) {
		if !asked {
			asked = true
			return TeamBranchResolutionOutcome{RunID: request.RunID(), NotStarted: true, Why: "every developer slot is in use"}, nil
		}
		return resolve(ctx, request)
	}
	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "every developer slot is in use") || !strings.Contains(outcome.Waiting, "asked for again at the next check") {
		t.Fatalf("Update() = %#v, want it waiting on a slot", outcome)
	}
	if rounds := f.record().Pending.Resolution.Rounds; len(rounds) != 0 {
		t.Fatalf("rounds = %#v, want none used", rounds)
	}
	if again := f.update(); again.Landed == "" {
		t.Fatalf("Update() at the next check = %#v, want it landed", again)
	}
	if rounds := f.record().Landed[0].Resolution.Rounds; len(rounds) != 1 {
		t.Fatalf("rounds = %#v, want exactly one used", rounds)
	}
}

func TestARunThatResolvesAConflictEndsAtItsApprovalWhereAPersonApprovesIntegrationAndTheMergeIsMadeAgainWhenItIsGone(t *testing.T) {
	t.Parallel()

	f, tip := newConflictFixture(t)
	f.develop = resolvesApp
	f.pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	f.updater.Resolve = f.pipeline.ResolveTeamBranchConflict
	base := f.local("staging")
	f.update()
	f.decide("it knows the line")

	// The run is never integrated, so a person's approval of integration changes
	// nothing about it: it ends at its own approval, opens no pull request and
	// publishes nothing. The merge made from it waits for that person.
	outcome := f.update()
	pending := f.record().Pending
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "integration needs a person's approval") || pending == nil || pending.Candidate == "" {
		t.Fatalf("Update() = %#v, pending %#v; want the merge built, checked and reviewed and left for a person", outcome, pending)
	}
	round := pending.Resolution.Rounds[0]
	run, err := f.waiting.Load(round.RunID)
	if err != nil || run.Status != runstate.StatusSucceeded || run.PullRequest != nil || run.TeamBranchResolution.ApprovedHead != round.Resolved {
		t.Fatalf("run = %#v, %v; want it succeeded at its approved head with no pull request", run, err)
	}

	// The merge commit goes missing; it is made again from the run's approved
	// head, which the run's branch keeps, and not built from the two branches,
	// which would only conflict again.
	pending.Candidate = strings.Repeat("d", 40)
	record := f.record()
	record.Pending = pending
	if err := f.records.Save(record); err != nil {
		t.Fatal(err)
	}
	f.pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	landed := f.update()
	if landed.Landed == "" || f.developed != 1 {
		t.Fatalf("Update() = %#v after %d developer call(s), want the merge made again and landed with no new run", landed, f.developed)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", landed.Landed); parents != landed.Landed+" "+base+" "+tip {
		t.Fatalf("landed parents = %q, want the target then the team's tip", parents)
	}
	if got := readQueueFile(t, f.repository, "app.txt"); got != resolvedApp {
		t.Fatalf("app.txt = %q, want the resolution", got)
	}
}

func TestARoundWhoseProcessDiedBeforeItsChangeWasCommittedIsNotCountedAndStartsAgain(t *testing.T) {
	t.Parallel()

	f, _ := newConflictFixture(t)
	f.develop = resolvesApp
	f.update()
	f.decide("it knows the line")

	// The process making the round dies after the run was reserved and before the
	// developer's change was committed: its record is left in flight, holding no
	// commit.
	resolve := f.updater.Resolve
	dies := true
	var first string
	f.updater.Resolve = func(ctx context.Context, request TeamBranchResolutionRequest) (TeamBranchResolutionOutcome, error) {
		if !dies {
			return resolve(ctx, request)
		}
		dies = false
		// The run is reserved as the round's own start reserves it, and the
		// process goes before anything else is done.
		first = request.RunID()
		run := request.run()
		state := runstate.State{
			SchemaVersion: runstate.StateSchemaVersion, RunID: first, ProductID: f.pipeline.Config.Product.ID,
			RepositoryID: string(f.pipeline.Config.Product.RepositoryID), WorkItemID: run.WorkItemBookkeepingID(), WorkItemTitle: "resolving",
			Backend: f.pipeline.developer().Backend, ConfigRevision: f.pipeline.Config.Revision(), Status: runstate.StatusPending,
			TeamBranchResolution: &run, TargetBranch: request.TargetBranch,
		}
		_, lease, err := f.pipeline.reserveRun(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		_ = lease.Release()
		return TeamBranchResolutionOutcome{}, errors.New("the process was stopped")
	}
	if _, err := f.updater.Update(context.Background()); err == nil {
		t.Fatal("Update() succeeded, want the stopped process's error")
	}

	waiting := f.update()
	resolution := f.record().Pending.Resolution
	if waiting.Landed != "" || len(resolution.Rounds) != 0 || resolution.Restarts != 1 {
		t.Fatalf("Update() = %#v, resolution = %#v, want the dead round ended without being counted", waiting, resolution)
	}
	if ended, err := f.pipeline.Store.Load(first); err != nil || ended.Status != runstate.StatusCancelled {
		t.Fatalf("the dead round's run = %#v, %v, want it ended as cancelled", ended.Status, err)
	}

	landed := f.update()
	if landed.Landed == "" || f.developed != 1 {
		t.Fatalf("Update() = %#v after %d developer call(s), want a new run of the round to resolve and land it", landed, f.developed)
	}
	rounds := f.record().Landed[0].Resolution.Rounds
	if len(rounds) != 1 || rounds[0].Resolved == "" || rounds[0].RunID == first {
		t.Fatalf("rounds = %#v, want one resolved round made by a new run", rounds)
	}
}
