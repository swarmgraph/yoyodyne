package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// conflictForge is a forge that also says whether a pull request conflicts with
// its target. Where conflictOn is set, that head is the one that does.
type conflictForge struct {
	*fakeCIForge
	conflictOn string
	answer     publish.MergeWait
	answerErr  error
	asked      int
}

func (f *conflictForge) MergeWait(_ context.Context, number int) (publish.MergeWait, error) {
	f.asked++
	if f.answerErr != nil {
		return publish.MergeWait{}, f.answerErr
	}
	answer := f.answer
	answer.Number = number
	if f.conflictOn != "" {
		answer.HeadCommit, answer.Mergeable = f.conflictOn, "CONFLICTING"
	}
	return answer, nil
}

// conflicting is the fixture's pipeline with the forge reporting that the
// head the run waits on conflicts with its target.
func (f *ciFixture) conflicting(t *testing.T, head string) (Pipeline, *conflictForge) {
	t.Helper()
	forge := &conflictForge{fakeCIForge: f.forge, conflictOn: head}
	pipeline := f.pipeline(t, true)
	pipeline.CIForge = forge
	return pipeline, forge
}

// targetGains commits one file onto the fixture's target.
func (f *ciFixture) targetGains(t *testing.T, path, content string) {
	t.Helper()
	writePipelineFile(t, f.repository, path, content)
	runPipelineGit(t, f.repository, "add", path)
	runPipelineGit(t, f.repository, "commit", "-m", "other work on "+path)
}

func (f *ciFixture) developerPrompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var prompts []string
	for _, provider := range f.providers {
		for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
			prompts = append(prompts, request.Prompt)
		}
	}
	return prompts
}

// lastDeveloperSession is the session the developer was last asked to resume.
func (f *ciFixture) lastDeveloperSession() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	session := ""
	for _, provider := range f.providers {
		for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
			session = request.SessionID
		}
	}
	return session
}

func waitingOnPullRequest(runID string, at time.Time) runstate.State {
	head := strings.Repeat("c", 40)
	return runstate.State{RunID: runID, TargetBranch: "main",
		PullRequest: &runstate.PullRequest{Number: 7, HeadCommit: head},
		CIWait:      &runstate.CIWait{Commit: head, Branch: "b", PushedAt: at, Required: []string{"test"}, WaitLimitSeconds: 3600}}
}

// A conflict decides the wait at the look that finds it, the first or a later
// one, and whatever the checks say. An answer GitHub has not worked out, could
// not give, or gave for another head does not, and the wait goes on.
func TestAConflictEndsTheWaitAtTheLookThatFindsItAndAnUnknownAnswerDoesNot(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	look := func(forge *conflictForge, state runstate.State, now time.Time) (bool, string) {
		t.Helper()
		reader := &ForgeCIWaits{Forge: forge, Enabled: func() bool { return true }, Now: func() time.Time { return now }}
		decided, why, err := reader.Decided(context.Background(), state)
		if err != nil {
			t.Fatal(err)
		}
		return decided, why
	}

	first := &conflictForge{fakeCIForge: &fakeCIForge{result: publish.CheckNotStarted}, conflictOn: strings.Repeat("c", 40)}
	if decided, why := look(first, waitingOnPullRequest("run-conflict-first", at), at.Add(time.Minute)); !decided || !strings.Contains(why, "pull request #7 conflicts with main") {
		t.Fatalf("first look = %v, %q, want the conflict", decided, why)
	}
	if first.reads != 0 {
		t.Fatalf("the checks were read %d times on a head that conflicts", first.reads)
	}

	later := &conflictForge{fakeCIForge: &fakeCIForge{result: publish.CheckPending}}
	state := waitingOnPullRequest("run-conflict-later", at)
	for minute, answer := range map[int]publish.MergeWait{
		1:  {HeadCommit: state.CIWait.Commit, Mergeable: "UNKNOWN", MergeState: "UNKNOWN"},
		3:  {HeadCommit: strings.Repeat("d", 40), Mergeable: "CONFLICTING", MergeState: "DIRTY"},
		5:  {HeadCommit: state.CIWait.Commit, Mergeable: "MERGEABLE", MergeState: "BLOCKED"},
		7:  {},
		59: {HeadCommit: state.CIWait.Commit, Mergeable: "UNKNOWN"},
	} {
		later.answer = answer
		waiting := waitingOnPullRequest("run-conflict-later"+time.Duration(minute).String(), at)
		if decided, why := look(later, waiting, at.Add(time.Duration(minute)*time.Minute)); decided {
			t.Fatalf("answer %#v decided the wait: %q", answer, why)
		}
	}
	later.answerErr = errors.New("GitHub could not say")
	if decided, _ := look(later, state, at.Add(10*time.Minute)); decided {
		t.Fatal("a reading that failed decided the wait")
	}
	later.answerErr, later.answer = nil, publish.MergeWait{HeadCommit: state.CIWait.Commit, MergeState: "DIRTY"}
	if decided, why := look(later, state, at.Add(12*time.Minute)); !decided || !strings.Contains(why, "conflicts with main") {
		t.Fatalf("a later look = %v, %q, want the conflict", decided, why)
	}

	// The wait on the head pushed next is not answered from this reading.
	next := state
	wait := *state.CIWait
	wait.Commit = strings.Repeat("e", 40)
	next.CIWait, next.PullRequest = &wait, &runstate.PullRequest{Number: 7, HeadCommit: wait.Commit}
	if decided, why := look(later, next, at.Add(12*time.Minute+time.Second)); decided {
		t.Fatalf("the next head's wait was decided by the earlier head's conflict: %q", why)
	}
}

// A conflict the run could not act on holds the run without a slot until it is
// tried again, past the wait limit too; one the replay did not find leaves the
// checks to decide.
func TestAConflictLeftWaitingIsTriedAgainLater(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	state := waitingOnPullRequest("run-conflict-retry", at)
	tried := at.Add(2 * time.Hour)
	state.CIWait.Conflict = &runstate.CIWaitConflict{At: tried, Says: "GitHub could not be reached"}
	if decided, says, ordinary := ciConflictDecided(state, tried.Add(time.Minute)); decided || ordinary || says != "GitHub could not be reached" {
		t.Fatalf("soon after = %v, %q, %v, want it left waiting", decided, says, ordinary)
	}
	if decided, _, _ := ciConflictDecided(state, tried.Add(ciConflictRetry)); !decided {
		t.Fatal("a conflict that could not be acted on was not tried again")
	}
	state.CIWait.Conflict.NothingToBringIn = true
	if _, _, ordinary := ciConflictDecided(state, tried.Add(time.Minute)); !ordinary {
		t.Fatal("a conflict the replay did not find kept the checks from being read")
	}
}

// Where nothing conflicts after all, the target is brought in, the short check
// runs on the new head, and it is pushed and waited on; the developer is not
// asked, and neither the full checks nor a review runs.
func TestAConflictThatReplaysCleanlyIsPushedAndWaitedOnAtItsNewHead(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.targetGains(t, "elsewhere.txt", "other work\n")
	pipeline, _ := f.conflicting(t, parked.CIWait.Commit)

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Paused || outcome.PausedByCIWait == nil {
		t.Fatalf("outcome = %#v, want the run waiting on its new head", outcome)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.CIWait.Commit == parked.CIWait.Commit || state.CIWait.Commit != state.HarnessCommit || state.BaseCommit == parked.BaseCommit || state.CIWait.SlotTaken {
		t.Fatalf("wait = %#v, head %s, base %s; want a fresh wait on the replayed head", state.CIWait, state.HarnessCommit, state.BaseCommit)
	}
	if f.forge.pushed() != 2 || f.forge.pushes[1].Previous != parked.CIWait.Commit || f.forge.pushes[1].Commit != state.HarnessCommit {
		t.Fatalf("pushes = %#v, want the new head in place of the old", f.forge.pushes)
	}
	if f.developed != 1 || f.reviewed() != 0 || logLines(t, f.fullLog) != 0 || logLines(t, f.shortLog) != 2 {
		t.Fatalf("developed=%d reviews=%d full=%d short=%d, want 1, 0, 0, 2", f.developed, f.reviewed(), logLines(t, f.fullLog), logLines(t, f.shortLog))
	}
	if state.RepairAttempts != 0 || state.Reconciliation != nil || state.CIWaitEnd == nil || !state.CIWaitEnd.Conflict {
		t.Fatalf("attempts %d, reconciliation %#v, end %#v", state.RepairAttempts, state.Reconciliation, state.CIWaitEnd)
	}
	if says := outcome.PausedByCIWait.Summary(); !strings.Contains(says, "pull request #7 conflicts with main") || !strings.Contains(says, "brought into the change without a conflict") {
		t.Fatalf("the run prints %q", says)
	}
}

// A change already on the target keeps its wait, however GitHub reads it.
func TestAReportedConflictWithNothingToBringInKeepsTheWait(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	pipeline, _ := f.conflicting(t, parked.CIWait.Commit)

	if outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID); err != nil || outcome.PausedByCIWait == nil {
		t.Fatalf("Run() = %#v, %v, want the run still waiting", outcome, err)
	}
	state, _ := f.store.Load(parked.RunID)
	if state.CIWait == nil || state.CIWait.Commit != parked.CIWait.Commit || state.CIWait.Conflict == nil || !state.CIWait.Conflict.NothingToBringIn || state.CIWait.SlotTaken {
		t.Fatalf("wait = %#v, want the same one, saying nothing was brought in", state.CIWait)
	}
	if f.forge.pushed() != 1 || logLines(t, f.fullLog) != 0 {
		t.Fatalf("pushes=%d full=%d", f.forge.pushed(), logLines(t, f.fullLog))
	}
}

// A conflict goes to the same run's developer as one of its reconciliation
// attempts, spending no repair attempt, and the settled change waits again. The
// second and third conflicts get the run's other two attempts, each recording
// the commit it conflicted with and that no hold applied, and the fourth
// stops the run for the development manager.
func TestAConflictInTheWaitGoesToTheDeveloperThreeTimesAndThenStops(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.targetGains(t, "feature.txt", "somebody else's answer\n")
	pipeline, _ := f.conflicting(t, parked.CIWait.Commit)

	outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID)
	if err != nil || outcome.PausedByCIWait == nil {
		t.Fatalf("Run() = %#v, %v, want the settled change waiting", outcome, err)
	}
	state, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RepairAttempts != 0 || state.Reconciliation == nil || state.ReplayConflict != nil || len(state.ConflictHandbacks) != 1 {
		t.Fatalf("attempts %d, reconciliation %#v, conflict %#v, hand-backs %#v", state.RepairAttempts, state.Reconciliation, state.ReplayConflict, state.ConflictHandbacks)
	}
	if state.WorkflowDivergence != "" {
		t.Fatalf("divergence %q", state.WorkflowDivergence)
	}
	prompts := f.developerPrompts()
	last := prompts[len(prompts)-1]
	if f.developed != 2 || !strings.Contains(last, "waiting for its required checks on GitHub") || !strings.Contains(last, "feature.txt") || !strings.Contains(last, "The development manager's decision") || strings.Contains(last, "was approved") {
		t.Fatalf("developed %d times, last prompt:\n%s", f.developed, last)
	}
	// No review approved this change, so it goes on in the session that wrote it,
	// which is recorded as having resolved the conflict too.
	if session := f.lastDeveloperSession(); session != "developer-session" {
		t.Fatalf("the conflict was settled in session %q, want the writer's", session)
	}
	if authors := state.AuthorSessions; len(authors) != 2 || authors[1].SessionID != "developer-session" || authors[1].Work != runstate.AuthorResolvedConflict {
		t.Fatalf("author sessions = %#v", authors)
	}
	if f.reviewed() != 0 || logLines(t, f.fullLog) != 0 || f.forge.pushed() != 2 || state.CIWait.Commit == parked.CIWait.Commit {
		t.Fatalf("reviews=%d full=%d pushes=%d wait=%#v", f.reviewed(), logLines(t, f.fullLog), f.forge.pushed(), state.CIWait)
	}
	if says := state.CIWait.Summary(); !strings.Contains(says, "went back to its developer") || !strings.Contains(says, "feature.txt") {
		t.Fatalf("the wait says %q", says)
	}

	for attempt := 2; attempt <= runstate.MaxReconciliations; attempt++ {
		f.targetGains(t, "feature.txt", fmt.Sprintf("answer %d from somebody else\n", attempt))
		again, _ := f.conflicting(t, state.CIWait.Commit)
		if outcome, err := again.Run(context.Background(), f.tracker.Item.ID); err != nil || outcome.PausedByCIWait == nil {
			t.Fatalf("conflict %d: Run() = %#v, %v, want the settled change waiting", attempt, outcome, err)
		}
		if state, err = f.store.Load(outcome.RunID); err != nil {
			t.Fatal(err)
		}
		used := state.Reconciliation
		if state.RepairAttempts != 0 || used == nil || used.Attempt != attempt || used.HoldMissed != runstate.HoldMissedNoHold || used.TargetCommit == "" {
			t.Fatalf("conflict %d: attempts %d, reconciliation %#v; want attempt %d naming its commit and no hold", attempt, state.RepairAttempts, used, attempt)
		}
	}

	f.targetGains(t, "feature.txt", "a fourth answer\n")
	again, _ := f.conflicting(t, state.CIWait.Commit)
	if _, err := again.Run(context.Background(), f.tracker.Item.ID); err == nil {
		t.Fatal("a fourth conflict did not stop the run")
	}
	stopped, err := f.store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != runstate.StatusFailed || stopped.ReplayConflict == nil || !stopped.ReplayConflict.BeforeReview() || stopped.CIWait != nil || !strings.Contains(stopped.Blocker, "already used its 3 attempts") {
		t.Fatalf("status %q, conflict %#v, wait %#v, blocker %q", stopped.Status, stopped.ReplayConflict, stopped.CIWait, stopped.Blocker)
	}
	if f.developed != 1+runstate.MaxReconciliations || f.reviewed() != 0 || logLines(t, f.fullLog) != 0 || stopped.RepairAttempts != 0 || stopped.ReconciliationsUsed() != runstate.MaxReconciliations {
		t.Fatalf("developed=%d reviews=%d full=%d attempts=%d", f.developed, f.reviewed(), logLines(t, f.fullLog), stopped.RepairAttempts)
	}
	if says := stopped.ReplayConflict.Says(stopped.RunID); strings.Contains(says, "approved") {
		t.Fatalf("the list says %q of a change nobody reviewed", says)
	}
}

// A run taken up again after the conflict was recorded, and before the change
// was moved onto the target, is given that same attempt and charged nothing
// more. Its earlier repair is what lets an interrupted run be taken up at all.
func TestAnInterruptedConflictAttemptIsMadeAgainAndNotCountedTwice(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.targetGains(t, "feature.txt", "somebody else's answer\n")
	state := parked
	state.CIWait = nil
	state.Phase = runstate.PhaseDeveloping
	state.RepairAttempts = 1
	now := time.Now().UTC()
	state.ReplayConflict = &runstate.ReplayConflict{TargetBranch: "main", Phase: runstate.PhaseChecking, RecordedAt: now, Reconciliation: true}
	state.Reconciliation = &runstate.Reconciliation{TargetBranch: "main", UsedAt: now}
	if err := f.store.Save(state); err != nil {
		t.Fatal(err)
	}
	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil || outcome.PausedByCIWait == nil {
		t.Fatalf("Run() = %#v, %v, want the settled change waiting", outcome, err)
	}
	if session := f.lastDeveloperSession(); session != "developer-session" {
		t.Fatalf("the attempt was made again in session %q, want the writer's", session)
	}
	prompts := f.developerPrompts()
	if last := prompts[len(prompts)-1]; !strings.Contains(last, "attempts this run gets to bring your change level") || strings.Contains(last, "was approved") {
		t.Fatalf("the attempt made again was told:\n%s", last)
	}
	after, _ := f.store.Load(parked.RunID)
	if after.RepairAttempts != 1 || after.ReconciliationsUsed() != 1 || after.BaseCommit == parked.BaseCommit || f.developed != 2 || logLines(t, f.fullLog) != 0 {
		t.Fatalf("attempts=%d developed=%d full=%d", after.RepairAttempts, f.developed, logLines(t, f.fullLog))
	}
}

// The target is read from the remote: one that cannot be reached leaves the run
// waiting without a slot and without the full checks, past the wait limit too,
// and the try after brings in what the remote has and the local target had not.
func TestAConflictWaitsOutAnUnreachableGitHubAndBringsInTheRemotesTarget(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	_, parked := f.park(t)
	f.overdue(t, parked.RunID)
	addBareRemote(t, f.repository)
	f.targetGains(t, "elsewhere.txt", "other work\n")
	runPipelineGit(t, f.repository, "push", "origin", "main")
	runPipelineGit(t, f.repository, "reset", "--hard", "HEAD~1")
	pipeline, _ := f.conflicting(t, parked.CIWait.Commit)
	pipeline = publishing(pipeline, &orchestratortest.Forge{})
	worktrees := pipeline.Worktrees
	pipeline.Worktrees = &unreachableTargetReads{WorktreeManager: worktrees, failures: 1}

	if outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID); err != nil || outcome.PausedByCIWait == nil {
		t.Fatalf("Run() = %#v, %v, want the run still waiting", outcome, err)
	}
	waiting, _ := f.store.Load(parked.RunID)
	if waiting.CIWait == nil || waiting.CIWait.Conflict == nil || waiting.CIWait.Conflict.NothingToBringIn || waiting.CIWait.SlotTaken || waiting.CIWait.Commit != parked.CIWait.Commit {
		t.Fatalf("wait = %#v, want it kept and tried again", waiting.CIWait)
	}

	pipeline.Worktrees = worktrees
	if outcome, err := pipeline.Run(context.Background(), f.tracker.Item.ID); err != nil || outcome.PausedByCIWait == nil {
		t.Fatalf("Run() = %#v, %v, want the run waiting on its new head", outcome, err)
	}
	state, _ := f.store.Load(parked.RunID)
	if state.CIWait.Commit == parked.CIWait.Commit || state.BaseCommit == parked.BaseCommit || logLines(t, f.fullLog) != 0 || f.reviewed() != 0 {
		t.Fatalf("wait %#v, base %s, full=%d", state.CIWait, state.BaseCommit, logLines(t, f.fullLog))
	}
}

// A replayed change whose short check fails goes back to its developer while
// the pull request still carries the old head, and the repaired head replaces
// that one on the remote, in this process or in one that takes the run up after
// it stopped between the replay and the push.
func TestAFailingShortCheckAfterACleanReplayIsRepairedAndReplacesThePublishedHead(t *testing.T) {
	t.Parallel()
	for name, interrupted := range map[string]bool{"in one process": false, "taken up again": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newCIFixture(t)
			remote := addBareRemote(t, f.repository)
			forge := &orchestratortest.Forge{Remote: remote}
			// The short check fails on the replayed change until its developer has
			// answered it.
			short := "echo short >> '" + f.shortLog + "' && { ! test -f elsewhere.txt || ! grep -qx 'implemented 1' feature.txt; }"
			process := func() Pipeline {
				pipeline := publishing(f.pipeline(t, true), forge)
				pipeline.Config.Checks[0] = short
				pipeline.Config.Execution.CIChecks.Local = []string{short}
				return pipeline
			}
			outcome, err := process().Run(context.Background(), f.tracker.Item.ID)
			if err != nil || outcome.PausedByCIWait == nil {
				t.Fatalf("Run() = %#v, %v, want the run waiting", outcome, err)
			}
			parked, err := f.store.Load(outcome.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if at := publishedCommit(t, remote, parked.Branch); at != parked.CIWait.Commit {
				t.Fatalf("the remote branch is at %q, want %q", at, parked.CIWait.Commit)
			}
			f.targetGains(t, "elsewhere.txt", "other work\n")
			runPipelineGit(t, f.repository, "push", "origin", "main")

			pipeline := process()
			pipeline.CIForge = &conflictForge{fakeCIForge: f.forge, conflictOn: parked.CIWait.Commit}
			if interrupted {
				// The process stops being able to save once the repair is recorded, so
				// the next one finds the replayed head saved and nothing pushed for it.
				dying := &interruptedStore{StateStore: f.store, atAttempt: 1, allowSaves: 1}
				first := pipeline
				first.Store = dying
				if _, err := first.Run(context.Background(), f.tracker.Item.ID); err == nil || !dying.stopped {
					t.Fatalf("Run() error = %v, stopped = %t; want the process interrupted at the repair", err, dying.stopped)
				}
				if at := publishedCommit(t, remote, parked.Branch); at != parked.CIWait.Commit {
					t.Fatalf("the interrupted process pushed %q", at)
				}
			}
			outcome, err = pipeline.Run(context.Background(), f.tracker.Item.ID)
			if err != nil || outcome.PausedByCIWait == nil {
				t.Fatalf("Run() error = %v, failure %q, want the repaired head waiting", err, outcome.Failure)
			}
			state, err := f.store.Load(parked.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if state.CIWait.Commit == parked.CIWait.Commit || state.CIWait.Commit != state.HarnessCommit || state.PullRequest.HeadCommit != state.HarnessCommit || state.BaseCommit == parked.BaseCommit {
				t.Fatalf("wait %#v, head %s, request %#v", state.CIWait, state.HarnessCommit, state.PullRequest)
			}
			if at := publishedCommit(t, remote, parked.Branch); at != state.HarnessCommit {
				t.Fatalf("the remote branch is at %q, want the repaired head %q", at, state.HarnessCommit)
			}
			// The failing check is the one repair charged; bringing the target in is
			// charged nothing.
			if state.RepairAttempts != 1 || state.Reconciliation != nil || f.reviewed() != 0 || logLines(t, f.fullLog) != 0 {
				t.Fatalf("attempts=%d reconciliation=%#v reviews=%d full=%d", state.RepairAttempts, state.Reconciliation, f.reviewed(), logLines(t, f.fullLog))
			}
		})
	}
}
