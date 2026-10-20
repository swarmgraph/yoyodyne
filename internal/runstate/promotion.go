package runstate

// Development is parallel and integration is serial. Two runs may develop,
// check, and review at the same time, but at most one may promote into a given
// target branch, because a promotion reads where that branch is and then moves
// it. Two of them interleaved is a race every concurrent run can lose; the lease
// below turns it into a queue, so isolated work still lands automatically when
// two runs finish together.
//
// The lease is a sibling of the reservation lock and works the same way: an
// advisory file lock the operating system drops when its holder exits. A
// promotion whose process died therefore leaves no stale lock for anybody to
// clear, and the half-finished promotion behind it is settled by reconciliation
// rather than by the next run to reach the queue.
//
// It is taken by the promoting run's own harness, in that process, and by
// nothing else. No agent has a path to it for the same reason no agent has a
// path to a merge: the roles that authorize a promotion must not be able to
// perform one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	// promotionQueueWait bounds how long a run waits its turn to promote. Unlike
	// a run lease, a promotion lease is held for one short step rather than for
	// the length of a run, so waiting one out is the intended behavior and the
	// bound only has to cover a queue of them: a promotion is a local
	// fast-forward and, where the project publishes, a merge request the forge
	// answers. What the bound is really for is the holder that never finishes —
	// a wedged forge call, a Git command waiting on something that never comes —
	// which must not hold every other run on the branch until its process dies.
	promotionQueueWait = 15 * time.Minute
	// maxPromotionLockNameBytes bounds the encoded branch name in a lease file
	// name, past which a digest of the branch stands in for it. Branch names this
	// long are pathological rather than ordinary, so the readable name is what
	// almost every project gets; what the bound buys is that a branch no
	// filesystem would accept as a file name still gets a lease of its own rather
	// than failing every promotion into it.
	maxPromotionLockNameBytes = 96
)

// LeasePromotion admits this process to promote into one target branch, waiting
// its turn behind whichever run is promoting into it now. The wait is bounded:
// a caller that never reaches the front is told so rather than held forever, and
// a cancelled run stops waiting immediately.
//
// The lease is per product and per target branch, so runs promoting into
// different branches never queue behind one another.
func (s *Store) LeasePromotion(ctx context.Context, targetBranch string) (*Lease, error) {
	branch := strings.TrimSpace(targetBranch)
	if !validLocalBranch(branch) {
		return nil, fmt.Errorf("promotion target %q is not a local branch name", targetBranch)
	}
	// The lease file outlives every promotion, for the reason a run's does:
	// removing it while another process holds it would let a third take a lock on
	// a file nobody else can see.
	path := filepath.Join(s.root, promotionLockName(branch))
	file, err := s.state.openLock(path)
	if err != nil {
		return nil, fmt.Errorf("open the promotion lease for %s: %w", branch, err)
	}
	wait := s.promotionWait
	if wait <= 0 {
		wait = promotionQueueWait
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := queueForStateFile(waitCtx, file, s.promotionQueued); err != nil {
		file.Close()
		// A caller whose own context is still live waited out the bound rather
		// than being cancelled, and the two must not read alike: one is a promotion
		// queue nothing is draining, the other is the run being stopped.
		if ctx.Err() == nil {
			return nil, StopError{Class: StopPromotionWait, Cause: fmt.Errorf("wait to promote into %s: another promotion held the lease for the whole %s wait", branch, wait)}
		}
		return nil, fmt.Errorf("wait to promote into %s: %w", branch, err)
	}
	return &Lease{label: "promotion", file: file}, nil
}

// promotionLockName is the file one target branch's promotion lease is taken on.
// A branch name is not a file name: it may contain slashes, and every other
// character it may contain is one a file name may too. So the separator is
// re-encoded rather than flattened — `release/1.2` and `release-1.2` are
// different branches and must not share a lease — into a character
// validLocalBranch already refuses, which is what keeps the encoding reversible.
func promotionLockName(branch string) string {
	return branchLockName(".promotion-", branch)
}

// branchLockName is the file a per-branch lease with the given prefix is taken
// on, encoded as promotionLockName describes.
func branchLockName(prefix, branch string) string {
	encoded := strings.ReplaceAll(branch, "/", "+")
	if len(encoded) > maxPromotionLockNameBytes {
		sum := sha256.Sum256([]byte(branch))
		encoded = hex.EncodeToString(sum[:])
	}
	return prefix + encoded + ".lock"
}
