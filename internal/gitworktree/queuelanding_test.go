package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestAQueueCandidateIsPromotedOnlyFromTheBaseItWasBuiltOn(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()
	base := gitLine(t, repository, "rev-parse", "main")
	candidate, err := manager.BuildQueueCandidate(ctx, QueueCandidateRequest{Entry: testQueueEntry, TargetBranch: "main", Heads: []string{head}})
	if err != nil {
		t.Fatalf("BuildQueueCandidate() error = %v", err)
	}

	// A commit that does not descend from the base is never promoted onto it.
	if err := manager.PromoteQueueCandidate(ctx, "main", base, head); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("PromoteQueueCandidate(the head alone) = %v, want it refused", err)
	}
	// A target that moved off the base loses the race and stays where it went.
	writeFile(t, repository, "elsewhere.txt", "elsewhere\n")
	runGit(t, repository, "add", "elsewhere.txt")
	runGit(t, repository, "commit", "-m", "the target moved")
	moved := gitLine(t, repository, "rev-parse", "main")
	if err := manager.PromoteQueueCandidate(ctx, "main", base, candidate.Commit); !errors.Is(err, ErrTargetDrift) {
		t.Fatalf("PromoteQueueCandidate(after the target moved) = %v, want drift", err)
	}
	if now := gitLine(t, repository, "rev-parse", "main"); now != moved {
		t.Fatalf("main = %s, want it left at %s", now, moved)
	}
	runGit(t, repository, "reset", "--hard", base)
	if err := manager.PromoteQueueCandidate(ctx, "main", base, candidate.Commit); err != nil {
		t.Fatalf("PromoteQueueCandidate() error = %v", err)
	}
	if now := gitLine(t, repository, "rev-parse", "main"); now != candidate.Commit {
		t.Fatalf("main = %s, want the candidate", now)
	}
	if held, err := manager.TargetHolds(ctx, "main", head); err != nil || !held {
		t.Fatalf("TargetHolds(the approved head) = %t, %v", held, err)
	}
}

func TestAQueueCandidateBranchIsReplacedOnlyFromWhatItHeld(t *testing.T) {
	t.Parallel()

	repository, head := queueRepository(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, remote, "init", "--bare", "-b", "main")
	runGit(t, repository, "remote", "add", "origin", remote)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()
	branch := "yoyodyne/merge-queue/" + testQueueEntry
	base := gitLine(t, repository, "rev-parse", "main")

	if err := manager.PublishQueueCandidate(ctx, "yoyodyne/some-run/0123abcd", head, ""); err == nil {
		t.Fatal("PublishQueueCandidate(a branch that is not the queue's) = nil, want it refused")
	}
	if err := manager.PublishQueueCandidate(ctx, branch, head, ""); err != nil {
		t.Fatalf("PublishQueueCandidate(new) error = %v", err)
	}
	if err := manager.PublishQueueCandidate(ctx, branch, base, ""); !errors.Is(err, ErrRemotePushRejected) || errors.Is(err, ErrRemotePushInterrupted) {
		t.Fatalf("PublishQueueCandidate(expecting no branch where one stands) = %v, want it refused", err)
	}
	// A push cut off before the remote answers — the session stopping for a
	// restart — is not a refusal, and moves nothing.
	stopped, stop := context.WithCancel(ctx)
	stop()
	if err := manager.PublishQueueCandidate(stopped, branch, base, head); !errors.Is(err, ErrRemotePushInterrupted) || errors.Is(err, ErrRemotePushRejected) {
		t.Fatalf("PublishQueueCandidate(cut off) = %v, want it read as interrupted", err)
	}
	if published, _, err := manager.QueueCandidateBranchCommit(ctx, branch); err != nil || published != head {
		t.Fatalf("QueueCandidateBranchCommit() after the cut-off push = %s, %v; want it where it was", published, err)
	}
	if err := manager.PublishQueueCandidate(ctx, branch, base, head); err != nil {
		t.Fatalf("PublishQueueCandidate(over what it held) error = %v", err)
	}
	if published, exists, err := manager.QueueCandidateBranchCommit(ctx, branch); err != nil || !exists || published != base {
		t.Fatalf("QueueCandidateBranchCommit() = %s, %t, %v; want the replacement", published, exists, err)
	}
}

// A push is a refusal only where Git exited by itself naming a ref the remote
// refused; anything else, a network or server failure included, ended without
// the remote's refusal and is asked for again.
func TestOnlyAPushTheRemoteAnsweredIsARefusal(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name        string
		result      execution.ProcessResult
		interrupted bool
	}{
		{"refused by a hook", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "To github.com:o/r.git\n ! [remote rejected] main -> main (protected branch hook declined)\nerror: failed to push some refs"}, false},
		{"refused as stale", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "To github.com:o/r.git\n ! [rejected]        abc -> yoyodyne/merge-queue/mqe-1 (stale info)\nerror: failed to push some refs"}, false},
		{"host not found", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 128, Stderr: "fatal: unable to access 'https://github.com/o/r.git/': Could not resolve host: github.com"}, true},
		{"server error", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 128, Stderr: "error: RPC failed; HTTP 500 curl 22 The requested URL returned error: 500\nfatal: the remote end hung up unexpectedly"}, true},
		{"connection dropped", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 128, Stderr: "Connection reset by peer\nfatal: the remote end hung up unexpectedly"}, true},
		{"succeeded", execution.ProcessResult{Status: execution.ProcessSucceeded}, false},
		{"killed with exit code -1", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: -1}, true},
		{"cancelled by a restart", execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1}, true},
		{"timed out", execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1, Stderr: "Writing objects: 40%"}, true},
		{"stalled", execution.ProcessResult{Status: execution.ProcessStalled, ExitCode: -1}, true},
		{"exited saying nothing", execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 128, Stderr: "  "}, true},
	} {
		if got := pushInterrupted(c.result); got != c.interrupted {
			t.Errorf("%s: pushInterrupted() = %t, want %t", c.name, got, c.interrupted)
		}
	}
}
