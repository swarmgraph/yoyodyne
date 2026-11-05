package teamclaim

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

func (c *testClock) Sleep(ctx context.Context, d time.Duration) error { c.advance(d); return nil }

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(command.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// sharedRemote is a bare remote with one commit on main.
func sharedRemote(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "--quiet", "--bare", "-b", "main", remote)
	git(t, root, "init", "--quiet", "-b", "main", seed)
	git(t, seed, "commit", "--quiet", "--allow-empty", "-m", "first")
	git(t, seed, "push", "--quiet", remote, "main")
	return remote
}

// factory is one machine working the shared remote: its own clone, its own
// state root, and its own name.
func factory(t *testing.T, remote, name string, clock *testClock) Claims {
	t.Helper()
	root := t.TempDir()
	clone := filepath.Join(root, "repository")
	git(t, root, "clone", "--quiet", remote, clone)
	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: clone,
		WorktreeRoot:   filepath.Join(root, "worktrees"),
		Remote:         "origin",
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	store, err := runstate.NewTeamClaimStore(filepath.Join(root, "state"), "example")
	if err != nil {
		t.Fatalf("NewTeamClaimStore() error = %v", err)
	}
	return Claims{Remote: manager, Store: store, Factory: name, Clock: clock}
}

func remoteClaim(t *testing.T, remote, item string) string {
	t.Helper()
	command := exec.Command("git", "-C", remote, "rev-parse", "--verify", "--quiet", gitworktree.ClaimRefPrefix+item)
	output, _ := command.Output()
	return strings.TrimSpace(string(output))
}

// Two factories racing for one item: exactly one claim push succeeds, and the
// other is told who holds the item rather than that something failed.
func TestTwoFactoriesRacingForOneItemExactlyOneClaims(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	factories := []Claims{factory(t, remote, "example on alpha", clock), factory(t, remote, "example on beta", clock)}

	var wait sync.WaitGroup
	errs := make([]error, len(factories))
	claims := make([]runstate.TeamClaim, len(factories))
	start := make(chan struct{})
	for index, f := range factories {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claims[index], errs[index] = f.Take(context.Background(), "example-1", "run-"+f.Factory[len("example on "):])
		}()
	}
	close(start)
	wait.Wait()

	won := 0
	for index, err := range errs {
		var held HeldError
		switch {
		case err == nil:
			won++
			if got := remoteClaim(t, remote, "example-1"); got != claims[index].Commit {
				t.Errorf("remote claim = %s, want the winner's %s", got, claims[index].Commit)
			}
		case errors.As(err, &held):
			winner := factories[1-index].Factory
			if held.Holder != winner {
				t.Errorf("loser told the holder is %q, want %q", held.Holder, winner)
			}
			if !strings.Contains(err.Error(), winner) {
				t.Errorf("refusal %q does not name the holder", err)
			}
		default:
			t.Errorf("factory %d error = %v, want the item held by the other", index, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d claims succeeded (errors %v), want exactly one", won, errs)
	}
}

// A held claim is renewed every fifteen minutes with a lease naming the commit
// last pushed, and released when its run no longer holds the item.
func TestHeldClaimIsRenewedAndThenReleased(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	ctx := context.Background()

	taken, err := alpha.Take(ctx, "example-2", "run-a")
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	holding := true
	stillHeld := func(runstate.TeamClaim) (bool, error) { return holding, nil }

	clock.advance(5 * time.Minute)
	if upkeep, err := alpha.Tend(ctx, stillHeld); err != nil || len(upkeep.Renewed) != 0 {
		t.Fatalf("Tend() before it is due = %#v, %v, want nothing renewed", upkeep, err)
	}
	clock.advance(RenewEvery)
	upkeep, err := alpha.Tend(ctx, stillHeld)
	if err != nil || len(upkeep.Renewed) != 1 {
		t.Fatalf("Tend() when due = %#v, %v, want the claim renewed", upkeep, err)
	}
	renewed, _, _ := alpha.Store.Load("example-2")
	if renewed.Commit == taken.Commit || remoteClaim(t, remote, "example-2") != renewed.Commit {
		t.Fatalf("renewed commit %s, remote %s, want a new commit on the remote", renewed.Commit, remoteClaim(t, remote, "example-2"))
	}
	if parent := git(t, remote, "rev-parse", renewed.Commit+"^"); parent != taken.Commit {
		t.Errorf("renewal parent = %s, want the commit last pushed %s", parent, taken.Commit)
	}

	holding = false
	upkeep, err = alpha.Tend(ctx, stillHeld)
	if err != nil || len(upkeep.Released) != 1 {
		t.Fatalf("Tend() after the run ended = %#v, %v, want the claim released", upkeep, err)
	}
	if got := remoteClaim(t, remote, "example-2"); got != "" {
		t.Errorf("remote claim = %s after release, want none", got)
	}
	if released, _, _ := alpha.Store.Load("example-2"); released.Held() {
		t.Errorf("claim record %#v still held after release", released)
	}
}

// A renewal that cannot reach the remote is recorded and tried again on the
// next pass; the claim is not given up.
func TestFailedRenewalIsTriedAgain(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	ctx := context.Background()
	if _, err := alpha.Take(ctx, "example-3", "run-a"); err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	failing := &failingRemote{Remote: alpha.Remote, fail: true}
	broken := alpha
	broken.Remote = failing
	clock.advance(RenewEvery)
	upkeep, err := broken.Tend(ctx, func(runstate.TeamClaim) (bool, error) { return true, nil })
	if err != nil || len(upkeep.Problems) != 1 || len(upkeep.Lost) != 0 {
		t.Fatalf("Tend() with the remote down = %#v, %v, want one problem and nothing lost", upkeep, err)
	}
	if claim, _, _ := alpha.Store.Load("example-3"); !claim.Held() || claim.RenewalProblem == "" {
		t.Fatalf("claim after a failed renewal = %#v, want it held with the problem recorded", claim)
	}
	failing.fail = false
	upkeep, err = broken.Tend(ctx, func(runstate.TeamClaim) (bool, error) { return true, nil })
	if err != nil || len(upkeep.Renewed) != 1 {
		t.Fatalf("Tend() with the remote back = %#v, %v, want the claim renewed", upkeep, err)
	}
	if claim, _, _ := alpha.Store.Load("example-3"); claim.RenewalProblem != "" {
		t.Errorf("renewal problem %q kept after a renewal succeeded", claim.RenewalProblem)
	}
}

// A claim not renewed for two hours is taken over with a lease naming the stale
// commit, and the takeover names the earlier holder. The earlier holder's
// renewal and release then find the claim is not theirs, and leave it alone.
func TestExpiredClaimIsTakenOverNamingTheEarlierHolder(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	beta := factory(t, remote, "example on beta", clock)
	ctx := context.Background()
	if _, err := alpha.Take(ctx, "example-4", "run-a"); err != nil {
		t.Fatalf("alpha Take() error = %v", err)
	}

	clock.advance(ExpireAfter - time.Minute)
	var held HeldError
	if _, err := beta.Take(ctx, "example-4", "run-b"); !errors.As(err, &held) || held.Holder != "example on alpha" {
		t.Fatalf("beta Take() before expiry error = %v, want held by alpha", err)
	}

	clock.advance(time.Minute)
	overtaken, err := beta.Take(ctx, "example-4", "run-b")
	if err != nil {
		t.Fatalf("beta Take() after expiry error = %v", err)
	}
	if overtaken.PreviousHolder != "example on alpha" {
		t.Errorf("takeover previous holder = %q, want alpha", overtaken.PreviousHolder)
	}
	message := git(t, remote, "show", "-s", "--format=%B", gitworktree.ClaimRefPrefix+"example-4")
	if !strings.Contains(message, `"previous_holder":"example on alpha"`) {
		t.Errorf("claim record %s does not name the earlier holder", message)
	}

	upkeep, err := alpha.Tend(ctx, func(runstate.TeamClaim) (bool, error) { return true, nil })
	if err != nil || len(upkeep.Lost) != 1 || !strings.Contains(upkeep.Lost[0], "example on beta") {
		t.Fatalf("alpha Tend() after the takeover = %#v, %v, want the claim reported lost to beta", upkeep, err)
	}
	if got := remoteClaim(t, remote, "example-4"); got != overtaken.Commit {
		t.Errorf("remote claim = %s, want beta's takeover %s left alone", got, overtaken.Commit)
	}
	if claim, _, _ := alpha.Store.Load("example-4"); claim.Held() || claim.Lost == "" {
		t.Errorf("alpha's record %#v, want it no longer held and the loss said", claim)
	}
}

// A later run of an item on the machine that already holds its claim takes the
// claim on rather than waiting for it to expire.
func TestLaterRunOnTheSameFactoryTakesItsOwnClaimOn(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	ctx := context.Background()
	first, err := alpha.Take(ctx, "example-5", "run-a")
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	second, err := alpha.Take(ctx, "example-5", "run-b")
	if err != nil {
		t.Fatalf("second Take() error = %v", err)
	}
	if second.Commit == first.Commit || second.RunID != "run-b" || second.PreviousHolder != "" {
		t.Errorf("second claim = %#v, want a fresh claim for run-b", second)
	}
	// Releasing for the first run, which no longer holds it, does nothing.
	if err := alpha.Release(ctx, "example-5", "run-a"); err != nil {
		t.Fatalf("Release() for the earlier run error = %v", err)
	}
	if got := remoteClaim(t, remote, "example-5"); got != second.Commit {
		t.Errorf("remote claim = %s, want run-b's %s", got, second.Commit)
	}
}

// A remote that cannot be reached starts nothing.
func TestTakeOnAnUnreachableRemoteClaimsNothing(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	alpha.Remote = &failingRemote{Remote: alpha.Remote, fail: true}
	var unreachable UnreachableError
	if _, err := alpha.Take(context.Background(), "example-6", "run-a"); !errors.As(err, &unreachable) {
		t.Fatalf("Take() error = %v, want UnreachableError", err)
	}
	if _, found, _ := alpha.Store.Load("example-6"); found {
		t.Errorf("a claim was recorded for a take that never reached the remote")
	}
}

type failingRemote struct {
	Remote
	fail bool
}

func (r *failingRemote) FetchClaims(ctx context.Context) error {
	if r.fail {
		return errors.New("could not resolve host")
	}
	return r.Remote.FetchClaims(ctx)
}

func (r *failingRemote) PushClaim(ctx context.Context, workItemID, record, parent, expected string, now time.Time) (string, error) {
	if r.fail {
		return "", errors.New("could not resolve host")
	}
	return r.Remote.PushClaim(ctx, workItemID, record, parent, expected, now)
}

// Two factories that name themselves the same, such as two homes on one
// machine, are still two holders: the second cannot take the first's live
// claim, and is told it is held.
func TestAFactoryUnderTheSameNameCannotTakeALiveClaim(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	first := factory(t, remote, "example on studio", clock)
	second := factory(t, remote, "example on studio", clock)
	ctx := context.Background()
	taken, err := first.Take(ctx, "example-7", "run-a")
	if err != nil {
		t.Fatalf("first Take() error = %v", err)
	}
	clock.advance(20 * time.Minute)
	var held HeldError
	if _, err := second.Take(ctx, "example-7", "run-b"); !errors.As(err, &held) {
		t.Fatalf("second Take() under the same name error = %v, want the item held", err)
	}
	if !strings.Contains(held.Error(), "last renewed 20 minutes ago") {
		t.Errorf("refusal %q, want how long ago the claim was renewed in plain words", held.Error())
	}
	if got := remoteClaim(t, remote, "example-7"); got != taken.Commit {
		t.Errorf("remote claim = %s, want the first factory's %s left alone", got, taken.Commit)
	}
}

// A machine whose name changed since it took a claim still knows the claim is
// its own, from its own record, and its next run of the item takes it on.
func TestAClaimStaysThisMachinesOwnWhenItsNameChanges(t *testing.T) {
	t.Parallel()
	remote := sharedRemote(t)
	clock := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	alpha := factory(t, remote, "example on alpha", clock)
	ctx := context.Background()
	first, err := alpha.Take(ctx, "example-8", "run-a")
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	renamed := alpha
	renamed.Factory = "example on alpha-2"
	second, err := renamed.Take(ctx, "example-8", "run-b")
	if err != nil {
		t.Fatalf("Take() after the name changed error = %v, want this machine's own claim taken on", err)
	}
	if second.Commit == first.Commit || second.PreviousHolder != "" {
		t.Errorf("claim after the rename = %#v, want a fresh claim with no earlier holder", second)
	}
}
