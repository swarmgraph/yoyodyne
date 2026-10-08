package gitworktree

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A run asking whether the primary checkout is clean while another run's
// promotion is fast-forwarding it must not read the promotion's files as
// somebody's uncommitted work. `git merge --ff-only` writes the new files before
// it records them in the index and moves the branch, so a status read inside
// that window names them; that is how a scheduler run beside a promotion was
// refused with "primary repository has uncommitted changes: yoyodyne-alpha.txt".
//
// Every promotion here is the manager's own, and every readiness read is too,
// so a read that lands mid-merge is the race and nothing else: the checkout is
// never left dirty by anybody. The same reads beside the same promotions also
// used to fail the promotion itself, on the index lock a refreshing status held.
func TestReadinessIsNeverReadInsideAPromotionIntoThePrimaryCheckout(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	base := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))
	runGit(t, repository, "checkout", "-q", "-b", "ahead")
	const promotions = 30
	for index := range promotions {
		// Several files per commit widen the window the merge writes them in, which
		// is what makes the race show within a few promotions rather than a few
		// hundred.
		for file := range 20 {
			writeFile(t, repository, fmt.Sprintf("promoted/%02d/file-%02d.txt", index, file), "content\n")
		}
		runGit(t, repository, "add", "--all")
		runGit(t, repository, "commit", "-q", "-m", fmt.Sprintf("promotion %d", index))
	}
	commits := strings.Fields(gitOutput(t, repository, "rev-list", "--reverse", base+"..ahead"))
	runGit(t, repository, "checkout", "-q", "main")

	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	promoting := make(chan struct{})
	var (
		wait  sync.WaitGroup
		mu    sync.Mutex
		dirty []string
		reads int
	)
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-promoting:
				return
			default:
			}
			err := manager.ValidateReady(ctx)
			mu.Lock()
			reads++
			if err != nil {
				dirty = append(dirty, err.Error())
			}
			mu.Unlock()
		}
	}()

	previous := base
	for _, commit := range commits {
		if err := manager.fastForward(ctx, "ahead", "main", previous, commit, true); err != nil {
			close(promoting)
			wait.Wait()
			t.Fatalf("fastForward(%s) error = %v", commit, err)
		}
		previous = commit
	}
	close(promoting)
	wait.Wait()

	if len(dirty) > 0 {
		t.Fatalf("%d of %d readiness read(s) beside %d promotion(s) refused a checkout nobody had touched; first: %s",
			len(dirty), reads, len(commits), dirty[0])
	}
	if reads == 0 {
		t.Fatal("no readiness read ran beside the promotions, so this proves nothing")
	}
	if err := manager.ValidateReady(ctx); err != nil {
		t.Fatalf("ValidateReady() after the promotions error = %v", err)
	}
}
