package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// testGitBudget is the local Git budget the manager here is given. Left to the
// manager, it is an idle thirty seconds scaled by the one-minute load, and a
// full suite's race binaries have run this test's one-file checkout past that
// with Git working. The test is about what the review is shown, never about how
// long Git took, so the budget has only a hung command to catch, which any
// figure does; ten minutes sits under the Makefile's TEST_TIMEOUT, so the hang
// is reported as the command it was. internal/gitworktree holds the scaled
// budget to its own behaviour, where it is the thing under test.
const testGitBudget = 10 * time.Minute

// A change that deletes a file larger than the patch bound is approvable. The
// deletion diff of such a file outgrew the 262,144-byte bound, was named as an
// omitted document, and refused the approval — yoyodyne-ifd.117.4's reduction of
// docs/configuration.md could never pass review that way. Driven here through
// the evidence builder over a real repository, as a run's review is: the file is
// described at the base commit, the approval stands, and the run's record names
// the deletion with its digest.
func TestAChangeDeletingAFileLargerThanTheBoundIsApprovable(t *testing.T) {
	t.Parallel()

	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v error = %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git(repository, "init", "-q", "-b", "main")
	git(repository, "config", "user.name", "Yoyodyne Test")
	git(repository, "config", "user.email", "yoyodyne@example.invalid")
	git(repository, "config", "maintenance.auto", "false")
	git(repository, "config", "gc.auto", "0")
	var content strings.Builder
	for index := 0; content.Len() <= gitworktree.DefaultMaxDiffBytes+4096; index++ {
		fmt.Fprintf(&content, "line %06d of a document that is being retired\n", index)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "configuration.md"), []byte(content.String()), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	git(repository, "add", "--all")
	git(repository, "commit", "-q", "-m", "the document")

	manager, err := gitworktree.New(gitworktree.Options{
		Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		Timeout: testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), gitworktree.CreateRequest{RunID: reviewRunID, WorkItemID: "yoyodyne-task", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repository, "worktree", "remove", "--force", "--force", worktree.Path).Run()
	})
	if err := os.Remove(filepath.Join(worktree.Path, "docs", "configuration.md")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	changes, err := manager.UnifiedChanges(context.Background(), worktree, gitworktree.DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 || len(changes.DeletedFiles) != 1 {
		t.Fatalf("changes: truncated=%t omitted=%#v deleted=%#v, want the deletion described and nothing omitted", changes.Truncated, changes.OmittedFiles, changes.DeletedFiles)
	}
	digest := "git-blob:" + git(repository, "rev-parse", worktree.BaseCommit+":docs/configuration.md")

	var events []execution.Event
	request := newRequest(func(event execution.Event) error {
		events = append(events, event)
		return nil
	})
	request.WorktreePath = worktree.Path
	request.Changes = changes
	provider := &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"the document is retired as the item asks"}`}
	result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v, want the approval to stand", err)
	}
	if result.Decision != DecisionApprove {
		t.Fatalf("Review() decision = %q, want an approval", result.Decision)
	}

	// The reviewer is told what the file was, where to open it, and what to
	// judge the deletion against — and is not handed the content.
	for _, want := range []string{
		"## Files this change removes, described rather than shown",
		"docs/configuration.md: deleted whole",
		fmt.Sprintf("%d bytes at base commit %s (%s)", content.Len(), worktree.BaseCommit, digest),
		"`git show " + worktree.BaseCommit + ":docs/configuration.md`",
		"judge it against the work item's stated reason for it, and raise a finding naming the file where nothing you were given states a reason for removing it",
	} {
		if !strings.Contains(provider.request.Prompt, want) {
			t.Errorf("the evidence does not carry %q", want)
		}
	}
	if strings.Contains(provider.request.Prompt, "-line 000000 of a document") {
		t.Error("the evidence renders the removed content")
	}
	if !strings.Contains(provider.request.SystemPrompt, "deletes whole") {
		t.Error("the contract does not say how a deletion is judged")
	}

	// review.started records the deletion with its digest, as it records an
	// omitted fixture.
	var started map[string]any
	for _, event := range events {
		if event.Type == execution.EventReviewStarted {
			if err := json.Unmarshal(event.Payload, &started); err != nil {
				t.Fatalf("decode review.started: %v", err)
			}
		}
	}
	if files, _ := started["deleted_files"].([]any); len(files) != 1 || files[0] != "docs/configuration.md" {
		t.Errorf("review.started deleted_files = %#v", started["deleted_files"])
	}
	if digests, _ := started["deleted_digests"].(map[string]any); digests["docs/configuration.md"] != digest {
		t.Errorf("review.started deleted_digests = %#v, want %s", started["deleted_digests"], digest)
	}
}
