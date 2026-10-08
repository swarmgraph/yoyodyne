package runstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// earlierHome is a home laid out the way earlier builds kept it.
func earlierHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "products", "yoyodyne"), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

// The migration is refused while a live process holds a run, a conversation's
// turn, or the watch session that hosts the recurring passes, in one sentence
// naming each; once each lets go, nothing is in flight, including the run whose
// record still says it is running, because nothing is behind it.
func TestAHomeWithAnythingInFlightIsNamedAndOneLetGoIsNot(t *testing.T) {
	t.Parallel()

	root := earlierHome(t)
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	run := testState(t, StatusPending)
	reservation, err := store.Reserve(context.Background(), run, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	conversations, err := NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := conversations.Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	watch, err := NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	session := "watch-" + strings.Repeat("a", 32)
	watching, held, err := watch.Lease(session)
	if err != nil || !held {
		t.Fatalf("Lease() = %v, %v", held, err)
	}
	sweeps, err := NewSweepStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sweeps.Claim(context.Background(), "architect-pass", time.Hour, time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	named, err := HomeInFlight(root)
	if err != nil {
		t.Fatalf("HomeInFlight() error = %v", err)
	}
	refusal := (&InFlightError{Home: root, InFlight: named}).Error()
	for _, want := range []string{"run " + run.RunID + " (yoyodyne-test) of yoyodyne", "a turn of the product-manager conversation of yoyodyne",
		"the watch session " + session, "the recurring pass architect-pass of yoyodyne", "moved nothing", home.MigrateCommand} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal = %q, want it to name %q", refusal, want)
		}
	}
	if strings.Count(strings.TrimSuffix(refusal, "."), ". ") != 0 {
		t.Errorf("refusal = %q, want one sentence", refusal)
	}

	for _, lease := range []*Lease{reservation, turn, watching} {
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if named, err := HomeInFlight(root); err != nil || len(named) != 0 {
		t.Fatalf("HomeInFlight() after every holder let go = %v, %v; want nothing in flight", named, err)
	}
	if recorded, err := store.Load(run.RunID); err != nil || !recorded.Status.InFlight() {
		t.Fatalf("the run's own record = %+v, %v; want it still recorded in flight, with nothing behind it", recorded, err)
	}
}

// A run record naming a worktree the migration moved is rewritten to where the
// worktree now is, with the path it had kept beside it and every counter as it
// was; one naming a worktree that did not move, or is not there, is left as it
// is, and a second pass rewrites nothing.
func TestARunRecordFollowsItsMovedWorktree(t *testing.T) {
	t.Parallel()

	from, to := t.TempDir(), t.TempDir()
	store, err := NewStore(to, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	earlierWorktree := filepath.Join(from, "worktrees", "yoyodyne", "yoyodyne", "yoyodyne-task-01234567")
	moved := filepath.Join(home.ProjectDirectory(to, "yoyodyne"), home.WorktreesDirectoryName, "yoyodyne-task-01234567")
	if err := os.MkdirAll(moved, 0o700); err != nil {
		t.Fatal(err)
	}
	preserved := testState(t, StatusRunning)
	preserved.WorktreePath = earlierWorktree
	preserved.Branch = "yoyodyne/task/01234567"
	preserved.BaseCommit = strings.Repeat("a", 40)
	preserved.TargetBranch = "main"
	preserved.Phase = PhaseChecking
	preserved.RepairAttempts = 2
	preserved.ReviewRounds = 3
	preserved.RedeployStop = &RedeployStop{At: preserved.StartedAt, Phase: PhaseChecking, BoundSeconds: 900, SessionID: "watch-before"}
	elsewhere := testState(t, StatusFailed)
	elsewhere.WorktreePath = "/somewhere/else"
	elsewhere.Branch = "yoyodyne/other/01234567"
	elsewhere.BaseCommit = strings.Repeat("b", 40)
	for _, state := range []State{preserved, elsewhere} {
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	rewritten, left, err := RewriteMigratedWorktrees(from, to, []string{"yoyodyne"}, now, "the test")
	if err != nil || len(left) != 0 || len(rewritten) != 1 {
		t.Fatalf("RewriteMigratedWorktrees() = %+v, %+v, %v; want the one moved worktree rewritten", rewritten, left, err)
	}
	after, err := store.Load(preserved.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(moved); err == nil {
		moved = resolved
	}
	if after.WorktreePath != moved || after.HomeMigration == nil || after.HomeMigration.FromWorktreePath != earlierWorktree || !after.HomeMigration.At.Equal(now) {
		t.Fatalf("rewritten record = %+v (migration %+v), want the new path with the old one kept", after, after.HomeMigration)
	}
	if after.RepairAttempts != 2 || after.ReviewRounds != 3 || after.RedeployStop == nil || after.Status != StatusRunning || after.Phase != PhaseChecking {
		t.Fatalf("rewritten record = %+v, want every counter and the redeploy stop as they were", after)
	}
	if untouched, err := store.Load(elsewhere.RunID); err != nil || untouched.WorktreePath != "/somewhere/else" || untouched.HomeMigration != nil {
		t.Fatalf("a record the migration did not move = %+v, %v; want it left alone", untouched, err)
	}
	if again, left, err := RewriteMigratedWorktrees(from, to, []string{"yoyodyne"}, now, "the test"); err != nil || len(again) != 0 || len(left) != 0 {
		t.Fatalf("a second pass = %+v, %+v, %v; want nothing rewritten", again, left, err)
	}
}

// A checkout whose marker names a home the migration emptied follows the state
// to where it went: the marker is rewritten to the machine home rather than
// every command refusing over a split nothing made. A marker naming a home that
// is still the product's, with no migration marker in it, still refuses.
func TestAMarkerNamingAMigratedHomeFollowsTheState(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	earlier, machineHome := t.TempDir(), t.TempDir()
	if err := AgreeRoot(checkout, ResolvedRoot{Path: earlier, Origin: RootOriginEarlierDefault}); err != nil {
		t.Fatal(err)
	}
	if err := AgreeRoot(checkout, ResolvedRoot{Path: machineHome, Origin: RootOriginDefault}); err == nil {
		t.Fatal("a second root was agreed before any migration said the state had moved there")
	}
	if err := os.WriteFile(filepath.Join(earlier, home.MovedMarkerName), []byte(machineHome+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AgreeRoot(checkout, ResolvedRoot{Path: machineHome, Origin: RootOriginDefault}); err != nil {
		t.Fatalf("AgreeRoot() after the migration = %v, want the marker to follow the state", err)
	}
	marker, err := ReadRootMarker(checkout)
	if err != nil || !sameRoot(marker.Recorded, machineHome) {
		t.Fatalf("marker = %+v, %v; want it to name the machine home now", marker, err)
	}
	var split *SplitRootError
	if err := AgreeRoot(checkout, ResolvedRoot{Path: earlier, Origin: RootOriginEarlierDefault}); !errors.As(err, &split) {
		t.Fatalf("AgreeRoot() back onto the emptied home = %v, want it refused as a second root", err)
	}
}
