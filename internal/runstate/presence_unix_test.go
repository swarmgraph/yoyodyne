//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A process holding a run's lease stamps itself beside it, so a surface that
// only reads can tell a run somebody is working on from one nobody is — and the
// run that sat in developer slot 1 for twenty hours on 2026-09-26 would have
// read as the second the whole time.
func TestPresenceFindsTheHolderAndNamesARunNothingHolds(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusPending)
	lease, err := store.Reserve(context.Background(), state, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	// Long after the record last moved: only the stamp can say somebody holds it.
	later := state.UpdatedAt.Add(24 * time.Hour)
	held, err := store.Presence(state, 30*time.Minute, later)
	if err != nil {
		t.Fatalf("Presence() while held error = %v", err)
	}
	if !held.Found {
		t.Fatalf("Presence() while this process holds the lease = %+v, want found", held)
	}
	if found, err := store.Held(state.RunID); err != nil || !found {
		t.Fatalf("Held() = %v, %v, want a live holder", found, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if found, err := store.Held(state.RunID); err != nil || found {
		t.Fatalf("Held() after release = %v, %v", found, err)
	}
	if _, err := os.Stat(store.root + "/" + state.RunID + ".holder"); !os.IsNotExist(err) {
		t.Fatalf("holder stamp after release: stat error = %v, want it gone", err)
	}

	// Let go of, and quiet for longer than the window: nothing is working on it.
	gone, err := store.Presence(state, 30*time.Minute, later)
	if err != nil {
		t.Fatalf("Presence() after release error = %v", err)
	}
	if gone.Found || !strings.Contains(gone.Says, "no process holds it") {
		t.Fatalf("Presence() after release = %+v, want no process found", gone)
	}
	// Let go of, but moved inside the window: a holder from a build that stamps
	// nothing is given the benefit of the doubt.
	recent, err := store.Presence(state, 30*time.Minute, state.UpdatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("Presence() inside the window error = %v", err)
	}
	if !recent.Found {
		t.Fatalf("Presence() inside the window = %+v, want found", recent)
	}
}

// A holder killed outright leaves its stamp behind, and the stamp is checked
// against the operating system rather than trusted, so the run reads as having
// no process at once rather than after the quiet window.
func TestPresenceReadsAStampWhoseProcessHasExitedAsNoProcess(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatalf("run a process to exit: %v", err)
	}
	path, err := store.holderPath(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stamp := `{"pid":` + strconv.Itoa(exited.Process.Pid) + `,"held_at":"2026-08-14T12:00:00Z"}`
	if err := os.WriteFile(path, []byte(stamp), 0o600); err != nil {
		t.Fatal(err)
	}
	presence, err := store.Presence(state, 30*time.Minute, state.UpdatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("Presence() error = %v", err)
	}
	if presence.Found || !strings.Contains(presence.Says, "has exited") {
		t.Fatalf("Presence() over a dead holder's stamp = %+v, want no process found", presence)
	}
}

// A run with no process behind it is ended by different things depending on
// what it was parked on, and the sentence every surface carries says which: the
// sweep leaves a standing operator pause alone and continues a usage-limit wait
// at its deadline, so telling either that `yoyo reconcile` settles it would send
// the operator to a command that does nothing.
func TestDeadRunRemedyNamesWhatActuallyEndsTheRun(t *testing.T) {
	t.Parallel()

	grace := 30 * time.Minute
	base := testState(t, StatusRunning)
	base.WorkItemID = "yoyodyne-ifd.1"

	parked := base
	if says := DeadRunRemedy(parked, grace); !strings.Contains(says, "`yoyo reconcile` settles it") {
		t.Fatalf("remedy for a parked run = %q, want the sweep's settlement named", says)
	}

	held := base
	since := base.UpdatedAt
	held.OperatorHeldSince = &since
	held.PauseCause = PauseOperatorHold
	if says := DeadRunRemedy(held, grace); !strings.Contains(says, "leaves alone while it stands") || strings.HasPrefix(says, "`yoyo reconcile` settles it") {
		t.Fatalf("remedy under the operator's pause = %q, want the pause named as what holds it", says)
	}

	limited := base
	deadline := base.UpdatedAt.Add(2 * time.Hour)
	limited.UsageLimitResetsAt = &deadline
	limited.PauseCause = PauseUsageLimit
	if says := DeadRunRemedy(limited, grace); !strings.Contains(says, "continues it once that has passed") || strings.Contains(says, "settles") {
		t.Fatalf("remedy for a usage-limit wait = %q, want the continuation at its deadline named", says)
	}

	// A run paused on work its item waits on is continued by the watch's pull
	// once that work closes, and the sweep never settles it.
	waiting := base
	waiting.DependencyPause = &DependencyPause{Blockers: []string{"yoyodyne-ifd.2"}}
	if says := DeadRunRemedy(waiting, grace); !strings.Contains(says, "continues it at the first pull after that work closes") || strings.Contains(says, "settles") {
		t.Fatalf("remedy for a dependency pause = %q, want the pull's continuation named", says)
	}

	// An outage wait is settled like any park, so it reads as one.
	away := limited
	away.PauseCause = PauseProviderUnauthenticated
	if says := DeadRunRemedy(away, grace); !strings.Contains(says, "`yoyo reconcile` settles it") {
		t.Fatalf("remedy for an outage wait = %q, want the sweep's settlement named", says)
	}

	// A wait for the forge's checks taken up again and let go of: the sweep
	// settles it once the forge has answered, and leaves it to the pull before.
	takenUp := base
	takenUp.CIWait = &CIWait{Commit: "9afda54ec758f261fd2f6dd6bc74645d6c9b98e8", Branch: "yoyodyne/task", PushedAt: base.UpdatedAt, Required: []string{"test"}, SlotTaken: true}
	if says := DeadRunRemedy(takenUp, grace); !strings.Contains(says, "before the forge had answered") || strings.Contains(says, "settles") {
		t.Fatalf("remedy for an unanswered wait taken up = %q, want the pull's continuation named", says)
	}
	answered := takenUp
	wait := *takenUp.CIWait
	wait.Decided = &CIWaitDecision{At: base.UpdatedAt, Passed: true, Says: "every required check passed on the commit pushed"}
	answered.CIWait = &wait
	says := DeadRunRemedy(answered, grace)
	if !strings.Contains(says, "the forge has answered its wait") || !strings.Contains(says, "`yoyo reconcile`, which a watching session's maintenance pass runs, settles it once its record has not moved for 30m0s") ||
		strings.Contains(says, "could not") {
		t.Fatalf("remedy for an answered wait taken up = %q, want the sweep's settlement after the grace named", says)
	}

	// Where the sweep tried and failed, the remedy says why rather than
	// promising a settlement that keeps not happening.
	failed := answered
	failed.ReconcileFindings = []ReconcileFinding{{Step: ReconcileRun, Problem: "observe run artifacts: worktree is locked."}}
	if says := DeadRunRemedy(failed, grace); !strings.HasSuffix(says, "; the last `yoyo reconcile` tried to settle it and could not: observe run artifacts: worktree is locked") {
		t.Fatalf("remedy after a failed settlement = %q, want the failure said", says)
	}
	failed.ReconcileFindings[0].Resolved = true
	if says := DeadRunRemedy(failed, grace); strings.Contains(says, "could not") {
		t.Fatalf("remedy after a resolved settlement finding = %q, want no failure said", says)
	}
	parkedFailed := parked
	parkedFailed.ReconcileFindings = []ReconcileFinding{{Step: ReconcileRun, Problem: "adopt run: store unreadable"}}
	if says := DeadRunRemedy(parkedFailed, grace); !strings.Contains(says, "tried to settle it and could not: adopt run: store unreadable") {
		t.Fatalf("remedy for a parked run after a failed settlement = %q, want the failure said", says)
	}
}
