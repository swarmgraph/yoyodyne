package gitworktree

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// silentGit answers every Git command the way one stopped by its time limit
// does: no exit of its own and nothing on either stream.
type silentGit struct{ status execution.ProcessStatus }

func (g silentGit) Run(_ context.Context, _ execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	started := time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)
	return execution.ProcessResult{Status: g.status, ExitCode: -1, StartedAt: started, FinishedAt: started.Add(12 * time.Second)}, nil
}

// A repository check whose Git command printed nothing used to be refused as
// "repository validation failed:" with nothing after the colon, which is a stop
// nobody can act on. The refusal names what happened to the command instead.
func TestARepositoryValidationThatPrintedNothingStillSaysWhatFailed(t *testing.T) {
	t.Parallel()
	for _, status := range []execution.ProcessStatus{execution.ProcessTimedOut, execution.ProcessCancelled, execution.ProcessFailed} {
		manager, err := New(Options{Runner: silentGit{status: status}, RepositoryRoot: t.TempDir(), WorktreeRoot: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		err = manager.validateRepository(context.Background())
		if err == nil {
			t.Fatalf("%s: validation passed over a Git command that did not succeed", status)
		}
		said := err.Error()
		reason := strings.TrimSpace(strings.TrimPrefix(said, "repository validation failed:"))
		if reason == "" || !strings.Contains(said, "git rev-parse --show-toplevel") || !strings.Contains(said, "after 12s") {
			t.Fatalf("%s: refusal = %q, want the command, what stopped it, and how long it ran", status, said)
		}
	}
	manager, err := New(Options{Runner: silentGit{status: execution.ProcessTimedOut}, RepositoryRoot: t.TempDir(), WorktreeRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.validateRepository(context.Background()); !strings.Contains(err.Error(), "was stopped by its time limit after 12s and printed no reason") {
		t.Fatalf("refusal = %q, want the time limit named", err)
	}
}
