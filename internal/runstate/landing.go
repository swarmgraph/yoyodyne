package runstate

// A landing runs the whole suite once over the commit a run integrated, after
// that run is over, so nothing about the run bounds how many landings run at
// once: two runs landing back to back ran two whole race suites side by side,
// beside the next runs' gates, and that load is what the suites then failed
// under. The landing lease makes landings on one target branch a queue, the way
// the promotion lease makes promotions one.
//
// It is the promotion lease's sibling and deliberately not the promotion lease
// itself. A promotion is one short step, and its queue is bounded at fifteen
// minutes for that reason; a landing suite runs for up to its budget times the
// number of landing checks, and a landing holding the promotion lease would hold
// every run on the branch out of integration for that long. So a landing takes
// a lock file of its own per branch, and its queue is bounded by what the caller
// says a landing may take rather than by the promotion's bound.
//
// Like every lease here it is an advisory file lock the operating system drops
// when its holder exits, so a landing whose process died leaves no lock behind,
// and it is taken by the harness in the landing's own process and by nothing
// else.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// LeaseLanding admits this process to run the landing checks over a commit
// integrated into one target branch, waiting its turn behind whichever landing
// on that branch is running now. The wait is bounded by wait: a caller that
// never reaches the front is told so, in words that tell it apart from its own
// context being cancelled. queued, when it is not nil, is told once, as the
// wait begins, that another landing holds the branch — which is how the caller
// records that it is waiting before it waits.
func (s *Store) LeaseLanding(ctx context.Context, targetBranch string, wait time.Duration, queued func()) (*Lease, error) {
	branch := strings.TrimSpace(targetBranch)
	if !validLocalBranch(branch) {
		return nil, fmt.Errorf("landing target %q is not a local branch name", targetBranch)
	}
	if wait <= 0 {
		return nil, fmt.Errorf("the wait for a landing on %s must be positive, got %s", branch, wait)
	}
	path := filepath.Join(s.root, branchLockName(".landing-", branch))
	file, err := s.state.openLock(path)
	if err != nil {
		return nil, fmt.Errorf("open the landing lease for %s: %w", branch, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := queueForStateFile(waitCtx, file, queued); err != nil {
		file.Close()
		if ctx.Err() == nil {
			return nil, fmt.Errorf("wait to land on %s: another landing held the lease for the whole %s wait", branch, wait)
		}
		return nil, fmt.Errorf("wait to land on %s: %w", branch, err)
	}
	return &Lease{label: "landing", file: file}, nil
}
