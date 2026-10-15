package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A forge that accepted the merge and reports the request merged, where the
// target branch does not carry the reviewed commit, has not merged the change:
// the run is not recorded as merged, the item is not closed, and the forge's
// state is what the record says of the request.
func TestAMergeTheForgeAcceptedThatIsNotOnTheTargetIsNotRecordedAsMerged(t *testing.T) {
	t.Parallel()

	// A replaying forge puts the change's content on main under a fresh commit,
	// so the reviewed commit itself never reaches the target branch.
	run := newProtectedRun(t, &orchestratortest.Forge{
		TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"},
		ReplayMerge:      true,
	})
	outcome, err := run.pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatalf("Run() = %#v, want the run stopped at its merge", outcome)
	}
	if outcome.WorkItemClosed || run.tracker.Closed {
		t.Fatal("the item was closed on a merge the target branch does not carry")
	}
	state, err := run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status == runstate.StatusSucceeded || state.RecordedAsMerged() {
		t.Fatalf("status = %s, want the run not recorded as merged", state.Status)
	}
	if state.PullRequest == nil || state.PullRequest.Merged {
		t.Fatalf("pull request = %#v, want it not recorded as merged", state.PullRequest)
	}
	if state.PullRequest.State != "MERGED" {
		t.Errorf("recorded pull request state = %q, want the forge's own MERGED", state.PullRequest.State)
	}
	if state.Integration == nil || !state.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want the landing kept on the record for finishing the merge", state.Integration)
	}
	for _, want := range []string{"not recorded as merged", "does not contain the promoted commit"} {
		if !strings.Contains(state.PublishFailure, want) {
			t.Errorf("publish failure %q does not say %q", state.PublishFailure, want)
		}
	}
	run.assertMainNotAhead(t)
}

// disagreementFixture is one landed run whose record a test can then make
// disagree with the target branch, and the reconciler and docket that sweep it.
type disagreementFixture struct {
	run        *protectedRun
	reconciler Reconciler
	docket     *runstate.DocketStore
	docketer   *Docketer
}

func newDisagreementFixture(t *testing.T) *disagreementFixture {
	t.Helper()
	run := newProtectedRun(t, &orchestratortest.Forge{TargetProtection: publish.BranchProtection{Protected: true, By: "ruleset"}})
	outcome, err := run.pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !run.tracker.Closed {
		t.Fatalf("outcome = %#v, want a landed run", outcome)
	}
	// The item is open again, which is the shape every lost change had: its run
	// recorded as merged and its item still waiting on the work.
	run.tracker.Closed = false
	run.tracker.Item.Status = "in_progress"
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	docketer := &Docketer{
		Docket:    docket,
		Runs:      run.store,
		Decisions: run.store.Triage(),
		Reruns:    run.store.Reruns(),
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		ProductID: "yoyodyne",
	}
	return &disagreementFixture{
		run: run,
		reconciler: Reconciler{
			Tracker:   run.tracker,
			Worktrees: newObserver(t, run.repository, filepath.Dir(outcome.WorktreePath)),
			Store:     run.store,
			Publisher: run.forge,
			Docket:    docketer,
		},
		docket:   docket,
		docketer: docketer,
	}
}

// forgetMerge rewrites the landed run's record onto a commit main does not
// carry, which is what a run recorded as merged over a pull request that never
// landed looks like.
func (f *disagreementFixture) forgetMerge(t *testing.T) string {
	t.Helper()
	tree := strings.TrimSpace(gitOutput(t, f.run.repository, "rev-parse", "main^{tree}"))
	unlanded := strings.TrimSpace(gitOutput(t, f.run.repository,
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit-tree", tree, "-p", "main", "-m", "a change that never landed"))
	state, err := f.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.Integration.SourceCommit = unlanded
	state.Integration.TargetCommit = unlanded
	published := *state.PullRequest
	published.HeadCommit = unlanded
	published.MergeCommit = ""
	published.State = "OPEN"
	state.PullRequest = &published
	if err := f.run.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return unlanded
}

func (f *disagreementFixture) sweep(t *testing.T) []MergeDisagreementFinding {
	t.Helper()
	findings, err := f.reconciler.FindMergeDisagreements(context.Background())
	if err != nil {
		t.Fatalf("FindMergeDisagreements() error = %v", err)
	}
	return findings
}

func (f *disagreementFixture) entries(t *testing.T) []triage.Entry {
	t.Helper()
	entries, err := f.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	return entries
}

// A run recorded as merged whose change is on the target branch is the record
// being right, and nothing is written or listed about it.
func TestARunRecordedAsMergedWhoseCommitIsOnTheTargetIsNotListed(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	before, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	findings := fixture.sweep(t)
	if len(findings) != 1 || !findings[0].OnTarget || findings[0].Failure != "" {
		t.Fatalf("findings = %#v, want the one run found on its target", findings)
	}
	if entries := fixture.entries(t); len(entries) != 0 {
		t.Fatalf("docket = %#v, want nothing listed", entries)
	}
	after, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.MergeDisagreement != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("record = %#v, want it untouched", after.MergeDisagreement)
	}
}

// A run recorded as merged whose change is not on the target branch has the
// disagreement written on it and is listed once, with the cause given as
// uncertain; a second sweep, and a docket built from the records, add nothing,
// and nothing is merged, closed or re-run.
func TestARunRecordedAsMergedWhoseCommitIsAbsentIsListedOnce(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	unlanded := fixture.forgetMerge(t)
	merges := len(fixture.run.forge.Merges)

	findings := fixture.sweep(t)
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want the one run", findings)
	}
	finding := findings[0]
	if finding.OnTarget || !finding.Recorded || !finding.Docketed || finding.Failure != "" {
		t.Fatalf("finding = %#v, want the disagreement recorded and docketed", finding)
	}
	state, err := fixture.run.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	disagreement := state.MergeDisagreement
	if disagreement == nil || disagreement.Cause != runstate.MergeCauseUncertain || disagreement.Commit != unlanded || disagreement.TargetBranch != "main" {
		t.Fatalf("disagreement = %#v, want the absent commit recorded with an uncertain cause", disagreement)
	}
	// The forge is asked what it says of the request now, and that is what the
	// disagreement and the entry name.
	if disagreement.ForgeState != "MERGED" || disagreement.PullRequest != state.PullRequest.Number {
		t.Errorf("disagreement = %#v, want the request and the forge's state named", disagreement)
	}
	// Recording and listing is all the sweep did.
	if state.Status != runstate.StatusSucceeded || len(fixture.run.forge.Merges) != merges || len(fixture.run.forge.Closed) != 0 || fixture.run.tracker.Closed {
		t.Fatalf("status %s, merges %d (was %d), closed %v; want nothing acted on", state.Status, len(fixture.run.forge.Merges), merges, fixture.run.forge.Closed)
	}

	entries := fixture.entries(t)
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want one entry", entries)
	}
	entry := entries[0]
	if entry.Class != triage.ClassPublication || entry.Key != triage.DisagreementKey(pipelineRunID, state.PullRequest.Number) ||
		entry.Publication == nil || entry.Publication.Disagreement == nil || entry.Publication.Disagreement.Cause != "uncertain" {
		t.Fatalf("entry = %#v, want a publication entry carrying the uncertain disagreement", entry)
	}
	rendered := entry.Render()
	for _, want := range []string{"yoyodyne-task", pipelineRunID, "Pull request #1", "Forge state: MERGED", "Not on the target branch", unlanded, "Cause: uncertain"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("entry does not say %q:\n%s", want, rendered)
		}
	}

	again := fixture.sweep(t)
	if len(again) != 1 || again[0].Recorded || again[0].Docketed || again[0].OnTarget {
		t.Fatalf("second sweep = %#v, want the run found again with nothing new written", again)
	}
	built, err := fixture.docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 {
		t.Errorf("docket build added %d entries, want none beside the one already standing", built.Added)
	}
	if entries := fixture.entries(t); len(entries) != 1 {
		t.Fatalf("docket after a second sweep = %#v, want still one entry", entries)
	}
}

// A run whose item has closed is not the sweep's: the item no longer waits on
// the change, whatever the branch shows.
func TestARunRecordedAsMergedWhoseItemIsClosedIsNotRead(t *testing.T) {
	t.Parallel()

	fixture := newDisagreementFixture(t)
	fixture.forgetMerge(t)
	fixture.run.tracker.Item.Status = "closed"
	if findings := fixture.sweep(t); len(findings) != 0 {
		t.Fatalf("findings = %#v, want none for a closed item", findings)
	}
	if entries := fixture.entries(t); len(entries) != 0 {
		t.Fatalf("docket = %#v, want nothing listed", entries)
	}
}
