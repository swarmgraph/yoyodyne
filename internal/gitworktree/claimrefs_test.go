package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// secondFactory is another clone of the same remote with a manager of its own:
// the arrangement two factories working one repository from two machines have.
func secondFactory(t *testing.T, remote string) *Manager {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "second")
	if output, err := attemptGit(filepath.Dir(clone), "clone", "--quiet", remote, clone); err != nil {
		t.Fatalf("clone the remote: %v: %s", err, output)
	}
	disableBackgroundMaintenance(t, clone)
	return newConvergeManager(t, clone)
}

// Two factories pushing a claim on one item at the same moment: the remote's
// compare and swap lets exactly one of them land, and the other is told the
// lease was refused rather than that the remote failed.
func TestTwoFactoriesRacingForOneClaimExactlyOneWins(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	factories := []*Manager{newConvergeManager(t, repository), secondFactory(t, remote)}
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	var wait sync.WaitGroup
	errs := make([]error, len(factories))
	commits := make([]string, len(factories))
	start := make(chan struct{})
	for index, factory := range factories {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			commits[index], errs[index] = factory.PushClaim(context.Background(), "yoyodyne-race", "claimed by factory "+string(rune('A'+index)), "", "", now)
		}()
	}
	close(start)
	wait.Wait()

	won := 0
	for index, err := range errs {
		switch {
		case err == nil:
			won++
			if remoteRef := gitLine(t, remote, "rev-parse", ClaimRefPrefix+"yoyodyne-race"); remoteRef != commits[index] {
				t.Errorf("remote claim = %s, want the winner's %s", remoteRef, commits[index])
			}
		case !errors.Is(err, ErrClaimLeaseRefused):
			t.Errorf("factory %d push error = %v, want a refused lease", index, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d claim pushes succeeded (errors %v), want exactly one", won, errs)
	}
}

// Renewing names the commit last pushed; taking over names the stale commit;
// releasing names the claim's own commit. A lease naming anything else is
// refused and leaves the reference where it is.
func TestClaimLeasesNameTheCommitTheyExpect(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	first := newConvergeManager(t, repository)
	second := secondFactory(t, remote)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	taken, err := first.PushClaim(ctx, "yoyodyne-lease", "taken", "", "", now)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if err := second.FetchClaims(ctx); err != nil {
		t.Fatalf("FetchClaims() error = %v", err)
	}
	reading, err := second.FetchedClaim(ctx, "yoyodyne-lease")
	if err != nil || !reading.Exists || reading.Commit != taken || reading.Record != "taken" {
		t.Fatalf("FetchedClaim() = %#v, %v, want the claim %s read back", reading, err, taken)
	}

	renewed, err := first.PushClaim(ctx, "yoyodyne-lease", "renewed", taken, taken, now.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if parent := gitLine(t, remote, "rev-parse", renewed+"^"); parent != taken {
		t.Errorf("renewal parent = %s, want %s", parent, taken)
	}

	// The second factory's reading is now stale: a takeover naming it is refused.
	if _, err := second.PushClaim(ctx, "yoyodyne-lease", "taken over", "", taken, now.Add(3*time.Hour)); !errors.Is(err, ErrClaimLeaseRefused) {
		t.Fatalf("takeover on a stale reading error = %v, want a refused lease", err)
	}
	overtaken, err := second.PushClaim(ctx, "yoyodyne-lease", "taken over", "", renewed, now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("takeover naming the current commit: %v", err)
	}

	// The first factory's renewal and release now name a commit that is no
	// longer its own, so both are refused and the takeover stands.
	if _, err := first.PushClaim(ctx, "yoyodyne-lease", "renewed again", renewed, renewed, now.Add(3*time.Hour)); !errors.Is(err, ErrClaimLeaseRefused) {
		t.Errorf("renewal after a takeover error = %v, want a refused lease", err)
	}
	if err := first.DeleteClaim(ctx, "yoyodyne-lease", renewed); !errors.Is(err, ErrClaimLeaseRefused) {
		t.Errorf("release after a takeover error = %v, want a refused lease", err)
	}
	if remoteRef := gitLine(t, remote, "rev-parse", ClaimRefPrefix+"yoyodyne-lease"); remoteRef != overtaken {
		t.Fatalf("remote claim = %s, want the takeover %s left alone", remoteRef, overtaken)
	}

	if err := second.DeleteClaim(ctx, "yoyodyne-lease", overtaken); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := attemptGit(remote, "rev-parse", "--verify", ClaimRefPrefix+"yoyodyne-lease"); err == nil {
		t.Errorf("claim reference still on the remote after its release")
	}
	if err := first.FetchClaims(ctx); err != nil {
		t.Fatalf("FetchClaims() error = %v", err)
	}
	if reading, err := first.FetchedClaim(ctx, "yoyodyne-lease"); err != nil || reading.Exists {
		t.Errorf("FetchedClaim() after release = %#v, %v, want no claim", reading, err)
	}
}

// The doctor's probe leaves nothing behind on a remote that accepts it, and
// names a remote that will not hold references under refs/yoyodyne/.
func TestProbeClaimReferences(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newConvergeManager(t, repository)
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	if err := manager.ProbeClaimReferences(context.Background(), "doctor-test", now); err != nil {
		t.Fatalf("ProbeClaimReferences() error = %v", err)
	}
	if listed := gitLine(t, remote, "for-each-ref", "refs/yoyodyne/"); listed != "" {
		t.Errorf("remote refs/yoyodyne/ = %q after the probe, want nothing left", listed)
	}

	writeFile(t, remote, "hooks/update", "#!/bin/sh\ncase \"$1\" in refs/yoyodyne/*) echo 'refs/yoyodyne/ is not allowed here' >&2; exit 1;; esac\n")
	runGit(t, remote, "config", "core.hooksPath", filepath.Join(remote, "hooks"))
	if err := os.Chmod(filepath.Join(remote, "hooks", "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := manager.ProbeClaimReferences(context.Background(), "doctor-test", now); !errors.Is(err, ErrClaimRefRefused) {
		t.Fatalf("ProbeClaimReferences() on a refusing remote error = %v, want ErrClaimRefRefused", err)
	}
}

func TestClaimRefRefusesNamesThatAreNotOneReference(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", "a/b", "x.lock", "a..b"} {
		if _, err := ClaimRef(id); err == nil {
			t.Errorf("ClaimRef(%q) accepted", id)
		}
	}
	if ref, err := ClaimRef("yoyodyne-ifd.429.103"); err != nil || ref != "refs/yoyodyne/claims/yoyodyne-ifd.429.103" {
		t.Errorf("ClaimRef() = %q, %v", ref, err)
	}
}
