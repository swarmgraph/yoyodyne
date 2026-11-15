package twofactory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/factory"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamclaim"
)

// claimRaceItems is how many items check 06 races for, so that one lucky
// ordering of the two pushes does not pass the check by chance.
const claimRaceItems = 5

// claimClock is a clock the check sets, so a claim's age is what the check
// says it is and not how long the test took.
type claimClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *claimClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *claimClock) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *claimClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

// stubForge answers which forge account a factory acts as.
type stubForge string

func (s stubForge) Account(context.Context) (string, error) { return string(s), nil }

// claimingFactory is one factory's claims, with the store it records what it
// holds in and the name its claims carry.
type claimingFactory struct {
	home   *factoryHome
	claims teamclaim.Claims
	store  *runstate.TeamClaimStore
	// name is the factory identity every claim of this factory names.
	name string
}

// newClaimingFactory builds the harness's own claim code for one factory,
// working its clone of the shared remote and naming it by product, machine
// identity and forge account.
func newClaimingFactory(t *testing.T, f *factoryHome, account string, clock execution.Clock) *claimingFactory {
	t.Helper()
	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: f.clone,
		WorktreeRoot:   f.worktreeRoot,
		Remote:         "origin",
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatalf("make %s's Git manager: %v", f.name, err)
	}
	store, err := runstate.NewTeamClaimStore(f.stateRoot(), product)
	if err != nil {
		t.Fatalf("open %s's claim store: %v", f.name, err)
	}
	reader := &factory.Reader{Home: f.home, Product: product, Forge: stubForge(account)}
	name, err := reader.Name(context.Background())
	if err != nil {
		t.Fatalf("read %s's factory identity: %v", f.name, err)
	}
	if !strings.Contains(name, f.machine) {
		t.Fatalf("%s's factory identity is %q, expected it to contain its machine identity %q", f.name, name, f.machine)
	}
	return &claimingFactory{
		home:   f,
		store:  store,
		name:   name,
		claims: teamclaim.Claims{Remote: manager, Store: store, Factory: f.name, Identity: reader, Clock: clock},
	}
}

// remoteClaimCommit is the commit the shared remote's claim reference on an
// item points at, or "" where the remote holds none.
func remoteClaimCommit(t *testing.T, w *world, item string) string {
	t.Helper()
	output, _ := exec.Command("git", "-C", w.remote, "rev-parse", "--verify", "--quiet", gitworktree.ClaimRefPrefix+item).Output()
	return strings.TrimSpace(string(output))
}

// remoteClaimRecord is the record the remote's claim commit on an item carries
// in its message.
func remoteClaimRecord(t *testing.T, w *world, item string) teamclaim.Record {
	t.Helper()
	message := git(t, w.remote, "log", "-1", "--format=%B", gitworktree.ClaimRefPrefix+item)
	var record teamclaim.Record
	if err := json.Unmarshal([]byte(message), &record); err != nil {
		t.Fatalf("the remote's claim commit on %s carries %q, expected a JSON claim record: %v", item, message, err)
	}
	return record
}

// requireHeldBy fails the check unless err is a refusal because the item is
// held, naming the factory that holds it.
func requireHeldBy(t *testing.T, err error, item string, holder *claimingFactory) {
	t.Helper()
	var held teamclaim.HeldError
	if !errors.As(err, &held) {
		t.Fatalf("expected the claim on %s to be refused with a HeldError naming %s, but got error %v", item, holder.name, err)
	}
	if held.Holder != holder.name {
		t.Errorf("expected the refusal on %s to name the holder %q, but it named %q", item, holder.name, held.Holder)
	}
	if !strings.Contains(err.Error(), holder.home.machine) {
		t.Errorf("expected the refusal text %q to contain the holder's machine identity %q", err.Error(), holder.home.machine)
	}
}

// checkClaimRace starts both factories' claims on one item at the same moment
// and expects the remote's compare and swap to let exactly one through.
func checkClaimRace(t *testing.T, w *world) {
	clock := &claimClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	a := newClaimingFactory(t, w.a, "account-a", clock)
	b := newClaimingFactory(t, w.b, "account-b", clock)
	racers := []*claimingFactory{a, b}

	for n := 1; n <= claimRaceItems; n++ {
		item := fmt.Sprintf("race-%d", n)
		claims := make([]runstate.TeamClaim, len(racers))
		errs := make([]error, len(racers))
		release := make(chan struct{})
		var wait sync.WaitGroup
		for i, racer := range racers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-release
				claims[i], errs[i] = racer.claims.Take(context.Background(), item, "run-"+racer.home.name)
			}()
		}
		close(release)
		wait.Wait()

		winner := -1
		for i, err := range errs {
			if err == nil {
				if winner >= 0 {
					t.Fatalf("expected exactly one factory to claim %s, but both %s and %s did", item, racers[winner].home.name, racers[i].home.name)
				}
				winner = i
			}
		}
		if winner < 0 {
			t.Fatalf("expected exactly one factory to claim %s, but neither did: %v", item, errs)
		}
		loser := 1 - winner
		requireHeldBy(t, errs[loser], item, racers[winner])

		if got := remoteClaimCommit(t, w, item); got != claims[winner].Commit {
			t.Errorf("expected the remote's claim on %s to point at the winner's claim commit %q, but it points at %q", item, claims[winner].Commit, got)
		}
		if record := remoteClaimRecord(t, w, item); record.Factory != racers[winner].name {
			t.Errorf("expected the remote's claim record on %s to name the winner %q, but it names %q", item, racers[winner].name, record.Factory)
		}
		if held, found, err := racers[loser].store.Load(item); err != nil {
			t.Errorf("read the loser's claim store for %s: %v", item, err)
		} else if found && held.Held() {
			t.Errorf("expected the losing factory %s to hold nothing for %s, but its store holds a claim by %s", racers[loser].home.name, item, held.Factory)
		}
	}
}

// checkClaimTakeover has factory A claim an item and stop renewing it, and
// expects factory B to be refused until the claim is two hours old and then to
// take it over, with the earlier holder named.
func checkClaimTakeover(t *testing.T, w *world) {
	const item = "takeover-1"
	taken := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	clock := &claimClock{now: taken}
	a := newClaimingFactory(t, w.a, "account-a", clock)
	b := newClaimingFactory(t, w.b, "account-b", clock)

	first, err := a.claims.Take(context.Background(), item, "run-a")
	if err != nil {
		t.Fatalf("expected factory A to take %s, but got error %v", item, err)
	}

	clock.set(taken.Add(teamclaim.ExpireAfter - time.Minute))
	_, err = b.claims.Take(context.Background(), item, "run-b")
	requireHeldBy(t, err, item, a)
	if got := remoteClaimCommit(t, w, item); got != first.Commit {
		t.Errorf("expected the remote's claim on %s to be A's commit %q after B was refused, but it points at %q", item, first.Commit, got)
	}

	clock.set(taken.Add(teamclaim.ExpireAfter))
	second, err := b.claims.Take(context.Background(), item, "run-b")
	if err != nil {
		t.Fatalf("expected factory B to take over %s once A's claim was %s old, but got error %v", item, teamclaim.ExpireAfter, err)
	}
	if second.PreviousHolder != a.name {
		t.Errorf("expected the claim B was returned to name %q as the previous holder, but it names %q", a.name, second.PreviousHolder)
	}
	if got := remoteClaimCommit(t, w, item); got != second.Commit {
		t.Errorf("expected the remote's claim on %s to point at B's commit %q, but it points at %q", item, second.Commit, got)
	}
	record := remoteClaimRecord(t, w, item)
	if record.Factory != b.name {
		t.Errorf("expected the remote's claim record to name B %q as the factory, but it names %q", b.name, record.Factory)
	}
	if record.PreviousHolder != a.name {
		t.Errorf("expected the remote's claim record to name A %q as previous_holder, but it names %q", a.name, record.PreviousHolder)
	}
	if !strings.Contains(record.PreviousHolder, w.a.machine) {
		t.Errorf("expected previous_holder %q to contain A's machine identity %q", record.PreviousHolder, w.a.machine)
	}
	// The harness also writes a note on the item itself naming the earlier
	// holder, but only from an unexported method of the orchestrator's run
	// (activeRun.noteTeamTakeover), which no check outside that package can
	// call, so it is not asserted here.
}
