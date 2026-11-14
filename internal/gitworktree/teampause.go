package gitworktree

// The team pause as a Git reference on the shared remote, for team mode
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// The pause is refs/yoyodyne/pause on the remote the claims live on. Where it
// exists, it points at a small commit whose message is the pause record, and no
// factory in team mode starts new work from the product. It is fetched by
// FetchClaims, in the one fetch that comes before every claim, and written the
// way a claim is: a push with a lease naming what the reference is expected to
// be.
//
// This file is only the transport. What a pause record says, and what it stops,
// is internal/teamclaim's.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// TeamPauseRef is the team pause on the shared remote.
const TeamPauseRef = "refs/yoyodyne/pause"

// fetchedTeamPauseRef is where the last fetch kept the team pause in the local
// repository, apart from TeamPauseRef so a local reference is never mistaken
// for one the remote holds.
const fetchedTeamPauseRef = "refs/yoyodyne/fetched-pause"

// teamPauseRefspec fetches the pause with a pattern rather than by name, so a
// remote holding no pause is an ordinary fetch that finds nothing, rather than
// a fetch that fails for a reference it could not find, and --prune drops the
// local copy once the remote's is cleared.
const teamPauseRefspec = "+" + TeamPauseRef + "*:" + fetchedTeamPauseRef + "*"

// TeamPauseReading is the team pause as the last fetch found it.
type TeamPauseReading struct {
	// Set is false where the remote held no pause.
	Set bool
	// Commit is the pause commit the remote's reference points at.
	Commit string
	// Record is the pause record the commit carries, as written.
	Record string
	// CommittedAt is when the pause commit was made.
	CommittedAt time.Time
}

// FetchedTeamPause reads the team pause as the last FetchClaims found it. It
// asks the remote nothing.
func (m *Manager) FetchedTeamPause(ctx context.Context) (TeamPauseReading, error) {
	resolved, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "--quiet", fetchedTeamPauseRef+"^{commit}")
	if err != nil {
		return TeamPauseReading{}, err
	}
	if resolved.Status != execution.ProcessSucceeded {
		return TeamPauseReading{}, nil
	}
	commit := strings.TrimSpace(resolved.Stdout)
	if !commitPattern.MatchString(commit) {
		return TeamPauseReading{}, fmt.Errorf("fetched team pause commit %q is invalid", commit)
	}
	message, err := m.run(ctx, "-C", m.repositoryRoot, "show", "-s", "--format=%ct%n%B", commit)
	if err != nil {
		return TeamPauseReading{}, err
	}
	if message.Status != execution.ProcessSucceeded {
		return TeamPauseReading{}, fmt.Errorf("read the team pause failed with exit code %d: %s", message.ExitCode, strings.TrimSpace(message.Stderr))
	}
	stamp, record, _ := strings.Cut(message.Stdout, "\n")
	reading := TeamPauseReading{Set: true, Commit: commit, Record: strings.TrimSpace(record)}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64); err == nil {
		reading.CommittedAt = time.Unix(seconds, 0).UTC()
	}
	return reading, nil
}

// PushTeamPause sets the team pause to a commit carrying record, with a lease:
// expected is the pause commit the reference must be at for the push to land,
// and empty means no pause may be set. A lease the remote refused ends on
// ErrClaimLeaseRefused.
func (m *Manager) PushTeamPause(ctx context.Context, record, expected string, now time.Time) (string, error) {
	commit, err := m.claimCommit(ctx, record, "", now)
	if err != nil {
		return "", err
	}
	if err := m.pushClaimRef(ctx, TeamPauseRef, commit, expected); err != nil {
		return "", err
	}
	return commit, nil
}

// DeleteTeamPause clears the team pause, with a lease naming the pause commit
// the harness last read. A pause that has moved since is left alone and ends on
// ErrClaimLeaseRefused.
func (m *Manager) DeleteTeamPause(ctx context.Context, expected string) error {
	if !commitPattern.MatchString(expected) {
		return fmt.Errorf("team pause commit %q is invalid", expected)
	}
	return m.pushClaimRef(ctx, TeamPauseRef, "", expected)
}
