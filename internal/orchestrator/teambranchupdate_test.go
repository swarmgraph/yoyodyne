package orchestrator

// Bringing the target branch up to date with the team's branch, over real Git:
// a primary checkout on the target branch, a bare remote that holds both
// branches, and a second clone the team merges into the team's branch from.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// waitingRuns is a run store that also lists the runs waiting for the team's
// merge, which is what a conflict names items from.
type waitingRuns struct {
	StateStore
	runs []runstate.State
}

func (w waitingRuns) WaitingForTeamMerge() ([]runstate.State, error) { return w.runs, nil }

const (
	teamApprove = `{"decision":"approve","summary":"the merge carries both branches' work and the checks pass"}`
	teamRepair  = `{"decision":"repair","summary":"the merge drops the target's own change to the greeting","findings":[{"severity":"major","message":"the greeting the target changed is back to the old one","location":{"file":"app.txt","line":2}}]}`
)

type teamUpdateFixture struct {
	t          *testing.T
	repository string
	remote     string
	team       string
	pipeline   *Pipeline
	checks     *countingChecks
	reviewer   *countingReviewer
	reports    *runstate.ReportStore
	records    *runstate.TeamBranchUpdateStore
	holds      *runstate.OperatorHoldStore
	waiting    *waitingRuns
	forge      *orchestratortest.Forge
	verdict    string
	updater    TeamBranchUpdater
}

// newTeamUpdateFixture is a project whose target is staging, checked out in the
// primary checkout, and whose team's branch is main; both are on the remote.
func newTeamUpdateFixture(t *testing.T) *teamUpdateFixture {
	t.Helper()
	repository := pipelineRepository(t)
	writeQueueFile(t, repository, "app.txt", "one\ntwo\nthree\n")
	runPipelineGit(t, repository, "add", "app.txt")
	runPipelineGit(t, repository, "commit", "-m", "the application")
	runPipelineGit(t, repository, "branch", "staging")
	remote := addBareRemote(t, repository)
	runPipelineGit(t, repository, "push", "origin", "refs/heads/staging:refs/heads/staging")
	runPipelineGit(t, repository, "switch", "staging")
	team := filepath.Join(t.TempDir(), "team")
	runPipelineGit(t, repository, "clone", "--branch", "main", remote, team)
	runPipelineGit(t, team, "config", "user.name", "Teammate")
	runPipelineGit(t, team, "config", "user.email", "teammate@example.invalid")

	f := &teamUpdateFixture{t: t, repository: repository, remote: remote, team: team, verdict: teamApprove}
	provider := &orchestratortest.Backend{Respond: func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: "update-review-session", ResolvedModel: "claude-opus-5",
			FinalText: f.verdict, Process: execution.ProcessResult{Status: execution.ProcessSucceeded},
		}, nil
	}}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f app.txt", "test -f team-check.txt || true"})
	pipeline.Config.Execution.UpstreamBranch = "main"
	f.holds = newOperatorHoldStore(t)
	pipeline.Holds = f.holds
	f.waiting = &waitingRuns{StateStore: store}
	pipeline.Store = f.waiting
	f.checks = &countingChecks{runner: pipeline.Checks}
	pipeline.Checks = f.checks
	f.pipeline = &pipeline
	manager := pipeline.Worktrees.(*gitworktree.Manager)

	stateRoot := t.TempDir()
	reviews, err := runstate.NewBranchReviewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	f.reports, err = runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	f.records, err = runstate.NewTeamBranchUpdateStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	f.reviewer = &countingReviewer{reviewer: review.Reviewer{Backend: provider, Model: testReviewerModel, Clock: fixedBranchClock{}}}
	f.updater = TeamBranchUpdater{
		Pipeline: f.pipeline,
		Git:      manager,
		Records:  f.records,
		Reviewer: BranchReviewer{
			Worktrees: manager, Reviewer: f.reviewer, Reviews: reviews, Reports: f.reports,
			Clock: fixedBranchClock{}, Repository: repository, Config: pipeline.Config,
		},
		Reports: f.reports,
	}
	return f
}

// teamMerges is the team merging a change into the team's branch.
func (f *teamUpdateFixture) teamMerges(file, content, message string) string {
	f.t.Helper()
	writeQueueFile(f.t, f.team, file, content)
	runPipelineGit(f.t, f.team, "add", file)
	runPipelineGit(f.t, f.team, "commit", "-m", message)
	runPipelineGit(f.t, f.team, "push", "origin", "HEAD:refs/heads/main")
	return gitLine(f.t, f.team, "rev-parse", "HEAD")
}

// landsOnTarget is work landing on the target branch, as it does while an update
// waits.
func (f *teamUpdateFixture) landsOnTarget(file, content, message string) string {
	f.t.Helper()
	writeQueueFile(f.t, f.repository, file, content)
	runPipelineGit(f.t, f.repository, "add", file)
	runPipelineGit(f.t, f.repository, "commit", "-m", message)
	return gitLine(f.t, f.repository, "rev-parse", "HEAD")
}

func (f *teamUpdateFixture) update() TeamBranchUpdateOutcome {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	outcome, err := f.updater.Update(ctx)
	if err != nil {
		f.t.Fatalf("Update() error = %v (outcome %#v)", err, outcome)
	}
	return outcome
}

func (f *teamUpdateFixture) record() runstate.TeamBranchUpdate {
	f.t.Helper()
	record, found, err := f.records.Load("staging")
	if err != nil || !found {
		f.t.Fatalf("Load(staging) = %v, found %t", err, found)
	}
	return record
}

func (f *teamUpdateFixture) local(branch string) string {
	return gitLine(f.t, f.repository, "rev-parse", "refs/heads/"+branch)
}

// toldTheDevelopmentManager is the reports the harness filed to the development
// manager.
func (f *teamUpdateFixture) toldTheDevelopmentManager() []report.Report {
	f.t.Helper()
	all, err := f.reports.List()
	if err != nil {
		f.t.Fatal(err)
	}
	var told []report.Report
	for _, reported := range all {
		if reported.Role == report.HarnessReporter && reported.For == domain.RoleDevelopmentManager {
			told = append(told, reported)
		}
	}
	return told
}

func TestTheTargetIsBroughtUpToDateWithAMergeThatIsCheckedAndReviewedAndLandedOnce(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	before := f.local("staging")
	tip := f.teamMerges("team.txt", "the team's work\n", "the team's change")

	outcome := f.update()
	landed := f.local("staging")
	if outcome.Landed != landed || landed == before || outcome.TeamCommit != tip || outcome.Conflict != nil || outcome.Waiting != "" {
		t.Fatalf("Update() = %#v; staging was %s and is %s; want the merge landed", outcome, before, landed)
	}
	// The merge is exactly the harness's own: the target it was built on, then
	// the team's tip.
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", landed); parents != landed+" "+before+" "+tip {
		t.Fatalf("landed parents = %q, want the old target then the team's tip", parents)
	}
	if readQueueFile(t, f.repository, "team.txt") != "the team's work\n" {
		t.Fatal("the primary checkout on staging does not hold the team's work")
	}
	if f.checks.calls() != 1 || !strings.Contains(filepath.Base(f.checks.directory()), "candidate-") {
		t.Fatalf("checks ran %d time(s) in %q, want once in the merge's own checkout", f.checks.calls(), f.checks.directory())
	}
	if f.reviewer.calls() != 1 || f.reviewer.last().Scope != review.ScopeBranch || f.reviewer.last().WorkItemID != "" {
		t.Fatalf("reviewer asked %d time(s) at %q, want once at branch scope with no item", f.reviewer.calls(), f.reviewer.last().Scope)
	}
	if !strings.Contains(f.reviewer.last().Context, "brings the team's merged work from `main` into `staging`") || len(f.reviewer.last().Checks) != 2 {
		t.Fatalf("the reviewer was told %q with %d check result(s)", f.reviewer.last().Context, len(f.reviewer.last().Checks))
	}
	record := f.record()
	if record.Pending != nil || len(record.Landed) != 1 {
		t.Fatalf("record = %#v, want one landed update and nothing pending", record)
	}
	attempt := record.Landed[0]
	if attempt.LandedCommit != landed || attempt.Candidate != landed || attempt.TeamCommit != tip || attempt.TargetBase != before ||
		!attempt.ChecksPassed() || !attempt.Approved() || attempt.Review.SessionID != "update-review-session" || attempt.LandedAt == nil {
		t.Fatalf("landed attempt = %#v", attempt)
	}
	if record.CurrentWith != tip || record.CurrentAt == nil {
		t.Fatalf("record is current with %q at %v, want the team's tip", record.CurrentWith, record.CurrentAt)
	}

	again := f.update()
	if !again.Current || again.Landed != "" || again.Waiting != "" || f.reviewer.calls() != 1 || f.checks.calls() != 1 {
		t.Fatalf("second Update() = %#v after %d review(s) and %d check run(s), want current and nothing asked", again, f.reviewer.calls(), f.checks.calls())
	}
	if len(f.record().Landed) != 1 || f.local("staging") != landed {
		t.Fatal("a second update changed the target or the record")
	}
}

func TestAConflictWaitsIsReportedOnceAndIsBuiltAgainWhenABranchMoves(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	landing := f.landsOnTarget("app.txt", "one\nstaging two\nthree\n", "staging changes the second line")
	f.waiting.runs = []runstate.State{{
		RunID: "run-staging-item", WorkItemID: "yoyodyne-staging-item", WorkItemTitle: "Reword the greeting", Status: runstate.StatusSucceeded,
		Integration: &runstate.Integration{TargetBranch: "staging", SourceCommit: landing, TargetCommit: landing},
		TeamMerge:   &runstate.TeamMerge{TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: landing, Since: time.Now()},
	}, {
		// An item waiting on another target touched nothing here.
		RunID: "run-other", WorkItemID: "yoyodyne-other", Status: runstate.StatusSucceeded,
		Integration: &runstate.Integration{TargetBranch: "other", SourceCommit: landing, TargetCommit: landing},
		TeamMerge:   &runstate.TeamMerge{TargetBranch: "other", UpstreamBranch: "main", LandingCommit: landing, Since: time.Now()},
	}}
	f.teamMerges("app.txt", "one\nteam two\nthree\n", "the team changes the second line")
	staging := f.local("staging")

	outcome := f.update()
	if outcome.Landed != "" || len(outcome.Conflict) != 1 || outcome.Conflict[0] != "app.txt" || outcome.Waiting == "" || f.local("staging") != staging {
		t.Fatalf("Update() = %#v, want a conflict in app.txt, nothing landed and staging left at %s", outcome, staging)
	}
	record := f.record()
	conflict := record.Pending.Conflict
	if conflict == nil || len(conflict.Paths) != 1 || conflict.Paths[0] != "app.txt" || len(conflict.Items) != 1 || conflict.Items[0] != (runstate.TeamBranchUpdateConflictItem{ID: "yoyodyne-staging-item", Title: "Reword the greeting"}) || record.Pending.ReportedAt == nil {
		t.Fatalf("pending = %#v, want the conflict naming app.txt and the item that touched it, reported", record.Pending)
	}
	if f.checks.calls() != 0 || f.reviewer.calls() != 0 {
		t.Fatal("a merge that conflicts was checked or reviewed")
	}
	told := f.toldTheDevelopmentManager()
	if len(told) != 1 || told[0].Severity != report.SeverityWarning ||
		!strings.Contains(told[0].Message, "app.txt") || !strings.Contains(told[0].Message, "Reword the greeting (yoyodyne-staging-item)") || strings.Contains(told[0].Message, "yoyodyne-other") {
		t.Fatalf("reports to the development manager = %#v, want one naming the path and the item by its title with its identifier after it", told)
	}

	// Work keeps landing on the target meanwhile. The merge is built again on
	// it, meets the same conflict, and the development manager is not told twice.
	f.landsOnTarget("other.txt", "other\n", "more work lands")
	staging = f.local("staging")
	again := f.update()
	if again.Landed != "" || len(again.Conflict) != 1 || again.Waiting == "" || f.local("staging") != staging {
		t.Fatalf("second Update() = %#v, want it still waiting on the same conflict", again)
	}
	if told := f.toldTheDevelopmentManager(); len(told) != 1 {
		t.Fatalf("%d reports after the target moved under the same conflict, want one", len(told))
	}

	// The team's next change makes the two lines the same, so the merge goes in.
	tip := f.teamMerges("app.txt", "one\nstaging two\nthree\n", "the team settles on the target's wording")
	resolved := f.update()
	if resolved.Landed == "" || resolved.Conflict != nil || f.local("staging") != resolved.Landed {
		t.Fatalf("Update() after the team's branch moved = %#v, want a new merge landed", resolved)
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", resolved.Landed); !strings.HasSuffix(parents, " "+tip) {
		t.Fatalf("landed parents = %q, want the team's new tip second", parents)
	}
}

func TestAConflictIsReportedOnceHoweverOftenItIsChecked(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.landsOnTarget("app.txt", "one\nstaging two\nthree\n", "staging changes the second line")
	f.teamMerges("app.txt", "one\nteam two\nthree\n", "the team changes the second line")
	for range 3 {
		if outcome := f.update(); len(outcome.Conflict) != 1 || outcome.Landed != "" {
			t.Fatalf("Update() = %#v, want the conflict", outcome)
		}
	}
	if told := f.toldTheDevelopmentManager(); len(told) != 1 {
		t.Fatalf("%d reports after three checks, want one", len(told))
	}
}

func TestAMergeWhoseChecksFailIsRefusedReportedOnceAndNotLanded(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.pipeline.Config.Checks = []string{"test -f app.txt", "exit 3"}
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	staging := f.local("staging")

	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "the configured checks failed on the merge: exit 3") || f.local("staging") != staging {
		t.Fatalf("Update() = %#v, want the checks refused and staging left at %s", outcome, staging)
	}
	if f.reviewer.calls() != 0 {
		t.Fatal("a merge whose checks failed was reviewed")
	}
	record := f.record()
	if record.Pending == nil || !strings.Contains(record.Pending.Refused, "exit 3") || record.Pending.ReportedAt == nil || len(record.Landed) != 0 || record.Pending.ChecksPassed() {
		t.Fatalf("record = %#v", record)
	}
	f.update()
	if told := f.toldTheDevelopmentManager(); len(told) != 1 || f.checks.calls() != 1 {
		t.Fatalf("%d reports and %d check run(s) after two checks, want one of each", len(told), f.checks.calls())
	}
}

func TestAMergeTheReviewerDoesNotApproveIsRefusedAndNotLanded(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.verdict = teamRepair
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	staging := f.local("staging")

	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "drops the target's own change to the greeting") || f.local("staging") != staging {
		t.Fatalf("Update() = %#v, want the reviewer's refusal said and staging left at %s", outcome, staging)
	}
	record := f.record()
	if record.Pending == nil || record.Pending.Approved() || record.Pending.Review == nil || record.Pending.Review.Decision != "repair" ||
		!strings.Contains(record.Pending.Refused, "reviewer did not approve") || record.Pending.ReportedAt == nil {
		t.Fatalf("pending = %#v", record.Pending)
	}
	f.update()
	if told := f.toldTheDevelopmentManager(); len(told) != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("%d reports and %d review(s) after two checks, want one of each", len(told), f.reviewer.calls())
	}
}

func TestATargetThatMovesBeforeTheLandingIsNotOverwrittenAndTheMergeIsBuiltAgain(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	tip := f.teamMerges("team.txt", "the team's work\n", "the team's change")
	var moved string
	f.reviewer.before = func(call int) {
		if call == 1 {
			moved = f.landsOnTarget("work.txt", "work that landed during the review\n", "work lands during the review")
		}
	}

	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "moved") || f.local("staging") != moved {
		t.Fatalf("Update() = %#v; staging = %s, want nothing landed over %s", outcome, f.local("staging"), moved)
	}
	if record := f.record(); record.Pending != nil || len(record.Landed) != 0 {
		t.Fatalf("record = %#v, want the update dropped with nothing landed", record)
	}
	if told := f.toldTheDevelopmentManager(); len(told) != 0 {
		t.Fatalf("a target that moved was reported: %#v", told)
	}

	next := f.update()
	if next.Landed == "" || f.reviewer.calls() != 2 {
		t.Fatalf("Update() = %#v after %d review(s), want the merge built again on the moved target and landed", next, f.reviewer.calls())
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", next.Landed); parents != next.Landed+" "+moved+" "+tip {
		t.Fatalf("landed parents = %q, want the moved target then the team's tip", parents)
	}
	if readQueueFile(t, f.repository, "work.txt") == "" {
		t.Fatal("the work that landed meanwhile is not on the target")
	}
}

func TestNothingRunsWithoutATeamsBranchConfigured(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	f.pipeline.Config.Execution.UpstreamBranch = ""
	staging := f.local("staging")

	if outcome := f.update(); outcome.TargetBranch != "" || outcome.Landed != "" || outcome.Current || outcome.Waiting != "" || outcome.Busy {
		t.Fatalf("Update() = %#v, want nothing", outcome)
	}
	if _, found, err := f.records.Load("staging"); err != nil || found || f.checks.calls() != 0 || f.reviewer.calls() != 0 || f.local("staging") != staging {
		t.Fatalf("something ran: record found %t (%v), %d check run(s), %d review(s)", found, err, f.checks.calls(), f.reviewer.calls())
	}
}

func TestTheOperatorsPauseStartsNothing(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	if _, err := f.holds.Hold(time.Now()); err != nil {
		t.Fatal(err)
	}
	staging := f.local("staging")

	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "paused") || f.checks.calls() != 0 || f.reviewer.calls() != 0 || f.local("staging") != staging {
		t.Fatalf("Update() = %#v, want it waiting on the pause with nothing started", outcome)
	}
	if _, found, _ := f.records.Load("staging"); found {
		t.Fatal("a paused check wrote a record")
	}
	if _, _, err := f.holds.Release(); err != nil {
		t.Fatal(err)
	}
	if lifted := f.update(); lifted.Landed == "" {
		t.Fatalf("Update() after the pause = %#v, want the merge landed", lifted)
	}
}

func TestOnlyOneProcessBringsTheTargetUpToDate(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	lease, held, err := f.records.LeaseUpdater(context.Background(), "staging")
	if err != nil || !held {
		t.Fatalf("LeaseUpdater() = %v, %t", err, held)
	}
	defer lease.Release()

	outcome := f.update()
	if !outcome.Busy || outcome.Waiting == "" || outcome.Landed != "" || f.checks.calls() != 0 {
		t.Fatalf("Update() = %#v, want it busy with nothing started", outcome)
	}
}

func TestAProjectThatPublishesLandsTheMergeThroughAPullRequestTheForgeMerges(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.forge = &orchestratortest.Forge{Remote: f.remote}
	*f.pipeline = publishing(*f.pipeline, f.forge)
	before := f.local("staging")
	tip := f.teamMerges("team.txt", "the team's work\n", "the team's change")

	outcome := f.update()
	record := f.record()
	if outcome.Landed == "" || record.Pending != nil || len(record.Landed) != 1 {
		t.Fatalf("Update() = %#v, record = %#v, want the update landed through the forge", outcome, record)
	}
	attempt := record.Landed[0]
	if len(f.forge.Opened) != 1 || f.forge.Opened[0].Base != "staging" || f.forge.Opened[0].Head != runstate.MergeQueueCandidateBranch(teamBranchUpdateEntry("yoyodyne", "staging", "main")) ||
		!strings.Contains(f.forge.Opened[0].Title, "Bring main into staging") {
		t.Fatalf("pull requests opened = %#v", f.forge.Opened)
	}
	if len(f.forge.Merges) != 1 || f.forge.Merges[0].HeadCommit != attempt.Candidate || f.forge.Merges[0].Method != publish.MergeCommit || attempt.PullRequest != 1 || attempt.MergeRequestedAt == nil {
		t.Fatalf("merges asked = %#v, attempt = %#v, want the candidate merged as a merge commit", f.forge.Merges, attempt)
	}
	// The forge's merge commit carries the candidate, and the target on the
	// remote and here stands at it.
	remoteStaging := gitLine(t, f.remote, "rev-parse", "refs/heads/staging")
	if outcome.Landed != remoteStaging || attempt.LandedCommit != remoteStaging || f.local("staging") != remoteStaging {
		t.Fatalf("landed %s, remote staging %s, local staging %s", outcome.Landed, remoteStaging, f.local("staging"))
	}
	if parents := gitLine(t, f.repository, "rev-list", "--parents", "-n", "1", attempt.Candidate); parents != attempt.Candidate+" "+before+" "+tip {
		t.Fatalf("candidate parents = %q", parents)
	}
	if again := f.update(); !again.Current || len(f.forge.Merges) != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("second Update() = %#v after %d merge(s) and %d review(s), want current and nothing asked", again, len(f.forge.Merges), f.reviewer.calls())
	}
}

func TestAPullRequestThatWaitsForAPersonIsConfirmedAtTheCheckThatFindsItMerged(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.forge = &orchestratortest.Forge{Remote: f.remote}
	*f.pipeline = publishing(*f.pipeline, f.forge)
	f.pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	f.teamMerges("team.txt", "the team's work\n", "the team's change")

	outcome := f.update()
	pending := f.record().Pending
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "pull request #1 waits for a person to merge it") || pending == nil || pending.PullRequest != 1 || len(f.forge.Merges) != 0 {
		t.Fatalf("Update() = %#v, pending = %#v, want a pull request waiting for a person and no merge asked", outcome, pending)
	}
	if waiting := f.update(); waiting.Landed != "" || !strings.Contains(waiting.Waiting, "waits for a person") || len(f.forge.Opened) != 1 || f.reviewer.calls() != 1 {
		t.Fatalf("second Update() = %#v after %d pull request(s) and %d review(s), want it still waiting and nothing repeated", waiting, len(f.forge.Opened), f.reviewer.calls())
	}

	// A person merges it.
	if err := f.forge.MergeIntoRemote("staging", pending.Candidate); err != nil {
		t.Fatal(err)
	}
	f.forge.Merged = true
	landed := f.update()
	record := f.record()
	remoteStaging := gitLine(t, f.remote, "rev-parse", "refs/heads/staging")
	if landed.Landed != remoteStaging || record.Pending != nil || len(record.Landed) != 1 || record.Landed[0].Candidate != pending.Candidate || f.local("staging") != remoteStaging {
		t.Fatalf("Update() = %#v, record = %#v, want the person's merge recorded as landed", landed, record)
	}
}

func TestAPullRequestClosedWithoutMergingIsBuiltAgain(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.forge = &orchestratortest.Forge{Remote: f.remote}
	*f.pipeline = publishing(*f.pipeline, f.forge)
	f.pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	f.update()
	f.forge.StateErr = nil

	closed := &closedForge{Forge: f.forge}
	f.pipeline.Publisher = closed
	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "closed without being merged") || f.record().Pending != nil {
		t.Fatalf("Update() = %#v, want the closed pull request dropped", outcome)
	}
	closed.closed = false
	if next := f.update(); !strings.Contains(next.Waiting, "waits for a person") || f.record().Pending == nil || f.reviewer.calls() != 2 {
		t.Fatalf("Update() = %#v after %d review(s), want a new merge built, reviewed and waiting", next, f.reviewer.calls())
	}
}

// closedForge reports its pull request closed while closed is set.
type closedForge struct {
	*orchestratortest.Forge
	closed bool
}

func (c *closedForge) State(ctx context.Context, head string) (publish.PullRequest, error) {
	if !c.closed && c.Forge != nil {
		c.closed = true
		return publish.PullRequest{Number: 1, State: "CLOSED"}, nil
	}
	return c.Forge.State(ctx, head)
}

func TestAProjectWithNoConfiguredChecksNeverLandsAnUpdate(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.pipeline.Config.Checks = nil
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	staging := f.local("staging")

	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "no checks are configured") || f.local("staging") != staging {
		t.Fatalf("Update() = %#v, want nothing landed for want of checks and staging left at %s", outcome, staging)
	}
	if f.reviewer.calls() != 0 {
		t.Fatal("a merge no check ran on was reviewed")
	}
	if record := f.record(); record.Pending == nil || record.Pending.Refused == "" || len(record.Landed) != 0 {
		t.Fatalf("record = %#v, want the update refused and nothing landed", record)
	}
}

func TestALocalProjectWhoseIntegrationNeedsAPersonKeepsTheMergeForThem(t *testing.T) {
	t.Parallel()

	f := newTeamUpdateFixture(t)
	f.pipeline.Config.Approvals.Integration = domain.ApprovalHuman
	f.teamMerges("team.txt", "the team's work\n", "the team's change")
	staging := f.local("staging")

	outcome := f.update()
	if outcome.Landed != "" || !strings.Contains(outcome.Waiting, "integration needs a person's approval") || f.local("staging") != staging {
		t.Fatalf("Update() = %#v, want the merge kept for a person and staging left at %s", outcome, staging)
	}
	record := f.record()
	if record.Pending == nil || !record.Pending.Approved() || !record.Pending.ChecksPassed() || len(record.Landed) != 0 {
		t.Fatalf("record = %#v, want a checked and approved merge waiting", record)
	}
	if again := f.update(); again.Landed != "" || f.reviewer.calls() != 1 || f.checks.calls() != 1 {
		t.Fatalf("second Update() = %#v after %d review(s) and %d check run(s), want nothing asked again", again, f.reviewer.calls(), f.checks.calls())
	}
}
