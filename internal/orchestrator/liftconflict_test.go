package orchestrator

// A re-run whose kept change no longer merges cleanly with the target.
//
// yoyodyne-yrku (run 4d993b06) and yoyodyne-9f4 (run 61842ea4) are the shape: the
// kept change would not apply onto main, so the re-run started from main with
// nothing said, its developer brought the change in by hand and committed it,
// and the harness refused the run for that commit. What is asserted here is the
// other ending: the re-run starts with the change merged and its conflicts left
// in the worktree, its developer is told which files to resolve and not to
// commit, and the change it resolves integrates.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// resolvedChange is what the re-run's developer leaves in the conflicting file
// once it has resolved the merge.
const resolvedChange = "the rewrite the reviewer found sound, on top of main's\n"

func TestARerunWhoseKeptChangeConflictsStartsWithTheConflictsForItsDeveloper(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	tracker.Item.Description = "Done means: the README is rewritten to two reviews, the architect's among them."
	attempts := 0
	var seen struct {
		prompt  string
		content string
		head    string
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		path := filepath.Join(request.WorkingDirectory, "lifted.txt")
		if attempts == 1 {
			return os.WriteFile(path, []byte(raisedChange), 0o600)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		seen.prompt = request.Prompt
		seen.content = string(content)
		head, err := attemptPipelineGit(request.WorkingDirectory, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		seen.head = strings.TrimSpace(head)
		if err := os.WriteFile(path, []byte(resolvedChange), 0o600); err != nil {
			return err
		}
		return writeFeature(request)
	}, escalateVerdict, approveVerdict)
	pipeline, store, docket := escalatingPipeline(t, tracker, provider)
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	pipeline.Intake = intake
	raisedOutcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	pipeline.NewRunID = func() (string, error) { return "run-fedcba9876543210fedcba9876543210", nil }
	raised := &raisedItem{pipeline: pipeline, store: store, docket: docket, intake: intake, tracker: tracker, raised: raisedOutcome}

	// Main moves past the kept change: the same file, written differently.
	if err := os.WriteFile(filepath.Join(pipeline.Repository, "lifted.txt"), []byte("main wrote this file meanwhile\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, pipeline.Repository, "add", "lifted.txt")
	runPipelineGit(t, pipeline.Repository, "-c", "user.name=Someone Else", "-c", "user.email=someone@example.invalid", "commit", "--quiet", "-m", "main moved on")
	prior, err := store.Read(raisedOutcome.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	preservedCommit := gitLine(t, pipeline.Repository, "rev-parse", "refs/heads/"+prior.Branch)

	raised.amendAndRelease()
	raised.decideRerun(t)
	result, err := raised.rerunner().Rerun(context.Background(), RerunRequest{Run: raisedOutcome.RunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.Started {
		t.Fatalf("result = %#v, want the re-run started", result)
	}
	// The developer was handed the merge, not the target alone: the file carries
	// both sides between conflict markers, nothing was committed past the base,
	// and the prompt named the file and said not to commit.
	if !strings.Contains(seen.content, "<<<<<<<") || !strings.Contains(seen.content, raisedChange) || !strings.Contains(seen.content, "main wrote this file meanwhile") {
		t.Fatalf("lifted.txt as the developer found it = %q, want both sides between conflict markers", seen.content)
	}
	fresh, err := store.Read(result.Outcome.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if seen.head != fresh.BaseCommit {
		t.Fatalf("HEAD the developer found = %s, want the base %s with the merge uncommitted", seen.head, fresh.BaseCommit)
	}
	if fresh.LiftedCommit != preservedCommit || len(fresh.LiftConflicts) != 1 || fresh.LiftConflicts[0] != "lifted.txt" {
		t.Fatalf("lifted commit = %q, conflicts = %v; want %s with lifted.txt in conflict", fresh.LiftedCommit, fresh.LiftConflicts, preservedCommit)
	}
	for _, want := range []string{"# The change this run starts from", "- lifted.txt", prior.Branch, "Do not commit"} {
		if !strings.Contains(seen.prompt, want) {
			t.Fatalf("the developer's prompt does not say %q:\n%s", want, seen.prompt)
		}
	}
	// And what the developer resolved is what integrated, under the harness's
	// own commit rather than one the developer made.
	if result.Outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the re-run integrated", result.Outcome)
	}
	if got := gitOutput(t, pipeline.Repository, "show", "main:lifted.txt"); got != resolvedChange {
		t.Fatalf("main:lifted.txt = %q, want the resolved change integrated", got)
	}
}

// conflictingLifter is a worktree manager whose every lift leaves the named
// files in conflict.
type conflictingLifter struct {
	WorktreeManager
	conflicts []string
}

func (l conflictingLifter) LiftChange(context.Context, gitworktree.Worktree, string) (gitworktree.Lift, error) {
	return gitworktree.Lift{Branch: "yoyodyne/xdj/01234567", Commit: "0123456789abcdef0123456789abcdef01234567", Conflicts: l.conflicts}, nil
}

// A lift that conflicts is recorded with its files whether or not the lift may
// fall back to the target, so neither kind of re-run drops the kept change.
func TestALiftThatConflictsIsRecordedWithItsFiles(t *testing.T) {
	t.Parallel()

	for _, fallBack := range []bool{true, false} {
		run := &activeRun{
			pipeline: Pipeline{Worktrees: conflictingLifter{conflicts: []string{"README.txt", "docs/a b.md"}}},
			state: runstate.State{Selection: &runstate.Selection{
				Lift: &runstate.Lift{RunID: docketedRunID, Branch: "yoyodyne/xdj/01234567", FallBackToTarget: fallBack},
			}},
		}
		if err := run.liftPreserved(context.Background()); err != nil {
			t.Fatalf("liftPreserved() fall back %v error = %v", fallBack, err)
		}
		if run.state.LiftedCommit == "" || strings.Join(run.state.LiftConflicts, ",") != "README.txt,docs/a b.md" {
			t.Fatalf("fall back %v: lifted %q conflicts %v, want both files recorded", fallBack, run.state.LiftedCommit, run.state.LiftConflicts)
		}
	}
}

// What the developer is told matches what its worktree was given, and it is
// told in every case not to commit; a run with no lift is told nothing.
func TestTheLiftSectionSaysWhatTheWorktreeWasGiven(t *testing.T) {
	t.Parallel()

	if got := liftSection(runstate.State{}); got != "" {
		t.Fatalf("liftSection() with no lift = %q, want nothing", got)
	}
	lift := &runstate.Lift{RunID: docketedRunID, Branch: "yoyodyne/xdj/01234567", FallBackToTarget: true}
	commit := "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		name  string
		state runstate.State
		want  []string
		not   []string
	}{
		{
			name:  "nothing-lifted",
			state: runstate.State{Selection: &runstate.Selection{Lift: lift}},
			want:  []string{"nothing of it was brought into your worktree", "Do not bring that change in yourself", "do not commit"},
		},
		{
			name:  "clean",
			state: runstate.State{Selection: &runstate.Selection{Lift: lift}, LiftedCommit: commit},
			want:  []string{"already holds, uncommitted", commit, "do not commit it"},
			not:   []string{"conflict markers"},
		},
		{
			name:  "conflicts",
			state: runstate.State{Selection: &runstate.Selection{Lift: lift}, LiftedCommit: commit, LiftConflicts: []string{"README.txt"}},
			want:  []string{"conflict markers in these files", "- README.txt", "Do not commit", "`git status`"},
		},
	} {
		got := liftSection(test.state)
		for _, want := range test.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: liftSection() = %q, want it to say %q", test.name, got, want)
			}
		}
		for _, not := range test.not {
			if strings.Contains(got, not) {
				t.Errorf("%s: liftSection() = %q, want it not to say %q", test.name, got, not)
			}
		}
	}
}
