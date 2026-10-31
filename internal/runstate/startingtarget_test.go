package runstate

import (
	"strings"
	"testing"
	"time"
)

// A local branch ahead of the remote is described as that, never as a split
// history a person has to settle; a split history keeps its reason.
func TestStartingTargetDescribesALocalBranchAheadOfTheRemote(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	ahead := StartingTarget{TargetBranch: "main", Commit: strings.Repeat("a", 40), Remote: "origin", Fetched: true, RemoteCommit: strings.Repeat("b", 40), Ahead: true, RecordedAt: now}
	described := ahead.Describe()
	if !strings.Contains(described, "the local branch already has everything origin has on it") || strings.Contains(described, "only a person") {
		t.Errorf("Describe() = %q, want a branch ahead of the remote described as that", described)
	}
	split := ahead
	split.Ahead = false
	split.Held = "main on origin is at b, which does not contain the local main at a; only a person can say which history is right"
	if described := split.Describe(); !strings.Contains(described, "was not moved onto it") || !strings.Contains(described, "only a person") {
		t.Errorf("Describe() = %q, want a split history to keep its reason", described)
	}
	failed := StartingTarget{TargetBranch: "main", Commit: strings.Repeat("a", 40), Remote: "origin", Failure: "could not read from remote repository", LastFetchedAt: now.Add(-26*time.Hour - 5*time.Minute), RecordedAt: now}
	if described := failed.Describe(); !strings.Contains(described, "last fetched 1 day 2 hours 5 minutes before this run started") {
		t.Errorf("Describe() = %q, want the failed fetch and the target's age", described)
	}
	if err := ahead.Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}
}
