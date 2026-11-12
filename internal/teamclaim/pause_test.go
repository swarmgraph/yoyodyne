package teamclaim

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

func remotePause(t *testing.T, remote string) string {
	t.Helper()
	output, _ := exec.Command("git", "-C", remote, "rev-parse", "--verify", "--quiet", gitworktree.TeamPauseRef).Output()
	return strings.TrimSpace(string(output))
}

func teamPause(claims Claims, setBy string) TeamPause {
	return TeamPause{Remote: claims.Remote.(PauseRemote), SetBy: setBy, Factory: claims.Factory}
}

// The team pause set on one factory stops another from claiming work the
// harness chose, names who set it, and once cleared stops nothing.
func TestTheTeamPauseIsSetReadByAnotherFactoryAndCleared(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	beta := factory(t, remote, "example on beta", clock)
	ctx := context.Background()

	set, already, err := teamPause(alpha, "mason").Set(ctx, clock.Now())
	if err != nil || already {
		t.Fatalf("Set() = %#v, already %v, error %v; want a new pause", set, already, err)
	}
	if got := remotePause(t, remote); got == "" || got != set.Commit {
		t.Fatalf("remote pause = %q, want %s", got, set.Commit)
	}
	again, already, err := teamPause(beta, "ana").Set(ctx, clock.Now())
	if err != nil || !already || again.SetBy != "mason" {
		t.Fatalf("second Set() = %#v, already %v, error %v; want the standing pause by mason left alone", again, already, err)
	}

	var paused PausedError
	if _, err := beta.Take(ctx, "example-p1", "run-b"); !errors.As(err, &paused) {
		t.Fatalf("Take() under the pause error = %v, want PausedError", err)
	}
	if paused.Pause.Setter() != "mason (example on alpha)" || !strings.Contains(paused.Error(), "mason (example on alpha)") {
		t.Fatalf("paused = %#v (%v), want it to name mason on alpha", paused.Pause, paused)
	}
	if got := remoteClaim(t, remote, "example-p1"); got != "" {
		t.Fatalf("a claim %s was pushed under the team pause", got)
	}
	if _, found, _ := beta.Store.Load("example-p1"); found {
		t.Fatalf("a claim was recorded under the team pause")
	}
	if !strings.Contains(paused.Pause.Says(), "mason (example on alpha) set it at") {
		t.Errorf("Says() = %q, want who set it", paused.Pause.Says())
	}

	// The operator's own named item is his exception.
	if _, err := beta.TakeNamed(ctx, "example-p2", "run-b2"); err != nil {
		t.Fatalf("TakeNamed() under the pause error = %v, want the claim taken", err)
	}

	cleared, was, err := teamPause(beta, "ana").Clear(ctx)
	if err != nil || !was || cleared.Commit != set.Commit {
		t.Fatalf("Clear() = %#v, was %v, error %v; want the pause cleared", cleared, was, err)
	}
	if got := remotePause(t, remote); got != "" {
		t.Fatalf("remote pause = %q after clearing, want none", got)
	}
	if _, err := alpha.Take(ctx, "example-p1", "run-a"); err != nil {
		t.Fatalf("Take() after the pause was cleared error = %v", err)
	}
	if _, was, err := teamPause(alpha, "mason").Clear(ctx); err != nil || was {
		t.Fatalf("Clear() of no pause = was %v, error %v; want nothing to clear", was, err)
	}
}

// A factory in team mode that cannot reach the remote claims nothing, so it
// starts no new work on a reading of the pause it could not refresh; and a
// pause it read before stops nothing differently because the fetch failed.
func TestAnUnreachableRemoteInTeamModeStartsNoNewWork(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	alpha.Remote = &failingRemote{Remote: alpha.Remote, fail: true}

	var unreachable UnreachableError
	if _, err := alpha.Take(context.Background(), "example-p3", "run-a"); !errors.As(err, &unreachable) {
		t.Fatalf("Take() error = %v, want UnreachableError", err)
	}
	if _, err := alpha.TakeNamed(context.Background(), "example-p3", "run-a"); !errors.As(err, &unreachable) {
		t.Fatalf("TakeNamed() error = %v, want UnreachableError", err)
	}
	if got := remoteClaim(t, remote, "example-p3"); got != "" {
		t.Fatalf("a claim %s was pushed without reaching the remote", got)
	}
}

// A pause somebody pushed by hand, whose record says nothing, still stops new
// work, and is said as naming nobody.
func TestAPauseThatNamesNobodyStillStopsNewWork(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	manager := alpha.Remote.(*gitworktree.Manager)
	if _, err := manager.PushTeamPause(context.Background(), "paused by hand", "", clock.Now()); err != nil {
		t.Fatalf("PushTeamPause() error = %v", err)
	}
	var paused PausedError
	if _, err := alpha.Take(context.Background(), "example-p4", "run-a"); !errors.As(err, &paused) {
		t.Fatalf("Take() error = %v, want PausedError", err)
	}
	if paused.Pause.Setter() != "somebody the pause does not name" {
		t.Errorf("Setter() = %q", paused.Pause.Setter())
	}
}
