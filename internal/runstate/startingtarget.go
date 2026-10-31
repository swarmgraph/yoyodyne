package runstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// StartingTarget is the target a developer run's worktree was cut from, and
// whether the harness reached the remote for it just before
// (docs/designs/working-alongside-other-contributors.md, section 3). A fetch
// that failed never stops a run: the run starts from the target the last fetch
// left, and this is what says so and how old that target was.
type StartingTarget struct {
	TargetBranch string `json:"target_branch"`
	// Commit is the commit the run's worktree was cut from.
	Commit string `json:"commit"`
	// Remote is the remote the target was fetched from, and empty for a
	// repository that has none.
	Remote string `json:"remote,omitempty"`
	// Fetched reports the remote answered for this run; RemoteCommit is where
	// it had the branch, empty when it had none.
	Fetched      bool   `json:"fetched"`
	RemoteCommit string `json:"remote_commit,omitempty"`
	// Advanced reports the local target moved onto the remote's for this run.
	Advanced bool `json:"advanced,omitempty"`
	// Ahead reports the local target already contained the remote's and
	// carried commits not yet on it, so there was nothing to move.
	Ahead bool `json:"ahead,omitempty"`
	// Held is why a remote that answered was not followed.
	Held string `json:"held,omitempty"`
	// Failure is why the fetch failed.
	Failure string `json:"failure,omitempty"`
	// LastFetchedAt is when the target was last fetched successfully, zero
	// where no fetch of it was ever recorded, and CommittedAt when the starting
	// commit was made.
	LastFetchedAt time.Time `json:"last_fetched_at,omitzero"`
	CommittedAt   time.Time `json:"committed_at,omitzero"`
	// RecordedAt is when the run started from it, which is what its age is
	// measured to.
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate checks the fields a reader acts on: the branch and the commits. The
// texts beside them are bounded with the rest of the run's recorded text.
func (t StartingTarget) Validate() error {
	var problems []error
	if !validLocalBranch(t.TargetBranch) {
		problems = append(problems, fmt.Errorf("target branch %q is not a local branch name", t.TargetBranch))
	}
	for _, commit := range []string{t.Commit, t.RemoteCommit} {
		if commit != "" && !commitPattern.MatchString(commit) {
			problems = append(problems, fmt.Errorf("commit %q is invalid", commit))
		}
	}
	return errors.Join(problems...)
}

// Age is how long before the run started the target was last fetched, and
// false where no fetch of it was ever recorded.
func (t StartingTarget) Age() (time.Duration, bool) {
	if t.LastFetchedAt.IsZero() || t.RecordedAt.IsZero() {
		return 0, false
	}
	age := t.RecordedAt.Sub(t.LastFetchedAt)
	if age < 0 {
		age = 0
	}
	return age, true
}

// Describe says, in one sentence, which target the run started from and how
// the harness came by it, in the words every surface that names it uses.
func (t StartingTarget) Describe() string {
	commit := t.Commit
	if commit == "" {
		commit = "an unknown commit"
	}
	start := fmt.Sprintf("started from %s at %s", t.TargetBranch, commit)
	switch {
	case t.Remote == "":
		return start + ", the local branch; the repository has no remote to fetch it from"
	case t.Fetched && t.RemoteCommit == "":
		return fmt.Sprintf("%s, fetched for this run; %s has no %s branch", start, t.Remote, t.TargetBranch)
	case t.Fetched && t.Ahead:
		return fmt.Sprintf("%s, fetched from %s for this run; the local branch already has everything %s has on it, and commits it does not have yet", start, t.Remote, t.Remote)
	case t.Fetched && t.Held != "":
		return fmt.Sprintf("%s; %s was fetched for this run but the local branch was not moved onto it: %s", start, t.Remote, singleSentence(t.Held))
	case t.Fetched:
		return fmt.Sprintf("%s, fetched from %s for this run", start, t.Remote)
	}
	described := fmt.Sprintf("%s, the target the last fetch left, because fetching it from %s failed: %s", start, t.Remote, singleSentence(t.Failure))
	if age, ok := t.Age(); ok {
		return fmt.Sprintf("%s; it was last fetched %s before this run started", described, roundedAge(age))
	}
	return described + "; when it was last fetched is not recorded"
}

func singleSentence(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// roundedAge says a duration in whole minutes, hours and days, which is the
// precision a reader judging how stale a target was needs.
func roundedAge(age time.Duration) string {
	minutes := int(age.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		return "under a minute"
	}
	days, hours := minutes/(24*60), minutes/60%24
	minutes %= 60
	var parts []string
	for _, part := range []struct {
		count int
		unit  string
	}{{days, "day"}, {hours, "hour"}, {minutes, "minute"}} {
		switch {
		case part.count == 1:
			parts = append(parts, "1 "+part.unit)
		case part.count > 1:
			parts = append(parts, fmt.Sprintf("%d %ss", part.count, part.unit))
		}
	}
	return strings.Join(parts, " ")
}
