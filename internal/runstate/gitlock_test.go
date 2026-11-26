package runstate

import (
	"strings"
	"testing"
	"time"
)

// A Git lock the harness would not remove ends the round before anything judged
// it, so the round spends nothing; and the run's record of each lock it looked
// at is held to its bounds.
func TestAGitLockLeftAloneEndsTheRoundUnjudgedAndItsRecordIsBounded(t *testing.T) {
	t.Parallel()
	if !CauseGitLockHeld.Valid() || !CauseGitLockHeld.EndsTheRoundUnjudged() || CauseGitLockHeld.NeedsAPerson() {
		t.Fatalf("%q: valid %t, unjudged %t, needs a person %t; want a recorded cause that ends the round unjudged and that the harness clears",
			CauseGitLockHeld, CauseGitLockHeld.Valid(), CauseGitLockHeld.EndsTheRoundUnjudged(), CauseGitLockHeld.NeedsAPerson())
	}
	if title := CauseGitLockHeld.Title(); !strings.Contains(title, "Git lock file") {
		t.Fatalf("Title() = %q, want it to say what stopped the round", title)
	}

	at := time.Date(2026, 11, 23, 13, 27, 0, 0, time.UTC)
	state := testState(t, StatusRunning)
	state.GitLocks = []GitLock{{Path: "/repo/.git/worktrees/run/index.lock", Removed: true, Why: "removed because no live process had it open", At: at}}
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() of a recorded removal error = %v", err)
	}
	for name, broken := range map[string]GitLock{
		"no path":     {Why: "why", At: at},
		"no reason":   {Path: "/x/index.lock", At: at},
		"no time":     {Path: "/x/index.lock", Why: "why"},
		"long reason": {Path: "/x/index.lock", Why: strings.Repeat("x", MaxGitLockWhyBytes+1), At: at},
	} {
		state.GitLocks = []GitLock{broken}
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "git_locks[0]") {
			t.Errorf("%s: Validate() error = %v, want the lock refused", name, err)
		}
	}
	state.GitLocks = make([]GitLock, MaxGitLocks+1)
	for index := range state.GitLocks {
		state.GitLocks[index] = GitLock{Path: "/x/index.lock", Why: "why", At: at}
	}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "entry bound") {
		t.Fatalf("Validate() of too many locks error = %v, want the bound", err)
	}
}
