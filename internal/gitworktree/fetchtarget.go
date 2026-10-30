package gitworktree

// The fetch before each developer run (docs/designs/working-alongside-other-contributors.md,
// section 3). Other contributors merge onto the remote target while the
// factory works, so a run cut from the local branch as it last stood starts
// behind them. Before a run's worktree is cut, the local target branch is
// brought onto the remote's, and what that found is handed back for the run's
// record: the commit the run starts from, whether the remote was reached for
// it, and when the target was last fetched.
//
// Nothing here stops a run. A remote that cannot be reached leaves the local
// branch where the last fetch put it, and the run starts from there; landing
// never trusts the base anyway, so a stale base costs at most a replay.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// fetchedTargetRefPrefix is where the harness keeps, for each target branch,
// the commit its last successful fetch found and when that fetch was. Each
// entry is a commit whose parent is the fetched target and whose committer time
// is the moment of the fetch, which is how a fetch that found the same commit
// as the one before still moves the entry. It lives outside refs/heads for the
// reason preservedWorkRefPrefix does.
const fetchedTargetRefPrefix = "refs/yoyodyne/fetched-target/"

// FetchedTargetRef is where the last successful fetch of one target branch is
// kept.
func FetchedTargetRef(targetBranch string) string {
	return fetchedTargetRefPrefix + targetBranch
}

// TargetFetch is what bringing a target branch up to date for one run found and
// did.
type TargetFetch struct {
	TargetBranch string `json:"target_branch"`
	// Remote is the remote the target was asked of, and empty where the
	// repository has no such remote, which is a project that only ever works
	// locally rather than a fetch that failed.
	Remote string `json:"remote,omitempty"`
	// Commit is where the local target branch stands after the attempt: the
	// commit the run starts from.
	Commit string `json:"commit"`
	// Fetched reports that the remote answered for this run. Where it did,
	// RemoteCommit is where it has the branch, empty when it has no such branch.
	Fetched      bool   `json:"fetched"`
	RemoteCommit string `json:"remote_commit,omitempty"`
	// Advanced reports the local branch moved onto the remote's for this run.
	Advanced bool `json:"advanced,omitempty"`
	// Held is why a remote that answered was not followed: a history that does
	// not contain the local branch, or uncommitted work in the primary checkout
	// in the way. The run starts from the local branch as it stands.
	Held string `json:"held,omitempty"`
	// Failure is why the remote could not be read or the branch not brought
	// onto it. The run starts from the local branch as the last fetch left it.
	Failure string `json:"failure,omitempty"`
	// LastFetchedAt is when this target was last fetched successfully, which is
	// now on a fetch that worked, and zero where no fetch has ever been recorded.
	LastFetchedAt time.Time `json:"last_fetched_at,omitzero"`
	// CommittedAt is when the commit the run starts from was made.
	CommittedAt time.Time `json:"committed_at,omitzero"`
}

// FetchTarget brings the local target branch onto the remote's, for a run about
// to be cut from it, and reports what that found. It never fails: what went
// wrong is reported in the result, and the branch is left wherever it stood.
//
// The local branch is moved exactly as CatchUpTarget moves it, so the caller
// holds the target branch's promotion lease, for the reason every caller of
// that does.
func (m *Manager) FetchTarget(ctx context.Context, targetBranch string, now time.Time) TargetFetch {
	fetch := TargetFetch{TargetBranch: targetBranch}
	if err := validateTargetBranch(targetBranch); err != nil {
		fetch.Failure = err.Error()
		return fetch
	}
	configured, err := m.remoteConfigured(ctx, m.remote)
	switch {
	case err != nil:
		fetch.Remote = m.remote
		fetch.Failure = fmt.Sprintf("ask whether the repository has the remote %s: %v", m.remote, err)
	case !configured:
		// A repository with no remote has nothing to fetch, so its local branch
		// is the latest there is.
	default:
		fetch.Remote = m.remote
		catchup, err := m.CatchUpTarget(ctx, targetBranch)
		switch {
		case err != nil:
			fetch.Failure = err.Error()
		default:
			fetch.Fetched = true
			fetch.RemoteCommit = catchup.RemoteCommit
			fetch.Advanced = catchup.Advanced
			fetch.Held = catchup.Held
		}
	}
	local, exists, err := m.optionalBranchCommit(ctx, targetBranch)
	if err != nil || !exists {
		if fetch.Failure == "" && err != nil {
			fetch.Failure = err.Error()
		}
		return fetch
	}
	fetch.Commit = local
	if committed, err := m.commitTime(ctx, local); err == nil {
		fetch.CommittedAt = committed
	}
	if fetch.Fetched && fetch.RemoteCommit != "" {
		// Kept so a later run whose fetch fails can say how old its target is. A
		// record that could not be written costs that later run the figure and
		// nothing else, so it does not change what this fetch reports.
		if err := m.recordFetch(ctx, targetBranch, fetch.RemoteCommit, now); err == nil {
			fetch.LastFetchedAt = now.UTC().Truncate(time.Second)
		}
		return fetch
	}
	if fetch.Remote == "" {
		return fetch
	}
	if at, ok := m.lastFetch(ctx, targetBranch); ok {
		fetch.LastFetchedAt = at
	}
	return fetch
}

// recordFetch keeps the moment one fetch of a target found the commit it did,
// as a commit on the target's own fetched-target ref.
func (m *Manager) recordFetch(ctx context.Context, targetBranch, fetched string, now time.Time) error {
	tree, err := m.resolveCommit(ctx, fetched+"^{tree}")
	if err != nil {
		return err
	}
	stamp := fmt.Sprintf("%d +0000", now.UTC().Unix())
	environment := append(harnessCommitEnvironment(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	made, err := m.runWithEnvironment(ctx, environment, "-C", m.repositoryRoot,
		"commit-tree", tree, "-p", fetched, "-m", "fetched "+targetBranch+" for a run")
	if err != nil {
		return err
	}
	if made.Status != execution.ProcessSucceeded {
		return fmt.Errorf("record the fetch of %s failed with exit code %d: %s", targetBranch, made.ExitCode, strings.TrimSpace(made.Stderr))
	}
	entry := strings.TrimSpace(made.Stdout)
	updated, err := m.run(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", FetchedTargetRef(targetBranch), entry)
	if err != nil {
		return err
	}
	if updated.Status != execution.ProcessSucceeded {
		return fmt.Errorf("record the fetch of %s failed with exit code %d: %s", targetBranch, updated.ExitCode, strings.TrimSpace(updated.Stderr))
	}
	return nil
}

// lastFetch is when the target was last fetched successfully, and false where
// no fetch of it was ever recorded or the record cannot be read.
func (m *Manager) lastFetch(ctx context.Context, targetBranch string) (time.Time, bool) {
	entry, err := m.resolveCommit(ctx, FetchedTargetRef(targetBranch))
	if err != nil {
		return time.Time{}, false
	}
	at, err := m.commitTime(ctx, entry)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// commitTime is when a commit was made, by its committer time.
func (m *Manager) commitTime(ctx context.Context, commit string) (time.Time, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "show", "-s", "--format=%ct", commit)
	if err != nil {
		return time.Time{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return time.Time{}, fmt.Errorf("read the time of %s failed with exit code %d: %s", commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("read the time of %s: %w", commit, err)
	}
	return time.Unix(seconds, 0).UTC(), nil
}
