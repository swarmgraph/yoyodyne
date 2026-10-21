//go:build darwin || linux

package runstate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The worker that reserves a run records itself as its first attempt, on the
// record and beside the lease, and reads as live while it holds the lease. A
// second process taking the run is a new attempt, and the record says so.
func TestAWorkerRecordsItsIdentityAndAReplacementIsANewAttempt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusPending)
	lease, err := store.Reserve(context.Background(), state, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	reserved, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	first := reserved.Worker
	if first == nil || first.PID != os.Getpid() || first.RunID != state.RunID || first.Attempt != 1 || first.StartedAt.IsZero() ||
		!first.ProgramStartedAt.Equal(ProgramStartedAt()) {
		t.Fatalf("reserved worker = %+v, want this process as attempt 1 with its start time and its program's", first)
	}
	if look, err := LookProcess(os.Getpid()); err != nil || !sameStart(look.StartedAt, first.StartedAt) {
		t.Fatalf("recorded start %v is not what the operating system reports (%+v, %v)", first.StartedAt, look, err)
	}
	if reading := store.Worker(reserved, time.Now()); reading.Liveness != WorkerLive || reading.Holder == nil || reading.Holder.Worker == nil {
		t.Fatalf("Worker() while held = %+v, want live with the stamp read", reading)
	}

	// A copy read before a later attempt began must not put the earlier worker
	// back when it is saved.
	stale := reserved
	stale.Status = StatusRunning
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if reading := store.Worker(reserved, time.Now()); reading.Liveness != WorkerAbsent || !strings.Contains(reading.Says, "nothing holds the run's lease") {
		t.Fatalf("Worker() after release = %+v, want absent", reading)
	}
	adopted, again, err := store.Adopt(context.Background(), state.WorkItemID)
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	defer again.Release()
	if adopted.Worker == nil || adopted.Worker.Attempt != 2 || adopted.Worker.PID != os.Getpid() {
		t.Fatalf("adopted worker = %+v, want attempt 2", adopted.Worker)
	}
	// Taking the run wrote nothing to its record: the new attempt is in the
	// stamp, and until the worker writes the record the two disagree, so the
	// worker is uncertain rather than live.
	untouched, err := store.Load(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Worker == nil || untouched.Worker.Attempt != 1 {
		t.Fatalf("record after Adopt names worker %+v, want attempt 1 still", untouched.Worker)
	}
	if reading := store.Worker(untouched, time.Now()); reading.Liveness != WorkerUncertain || !strings.Contains(reading.Says, "new attempt") {
		t.Fatalf("Worker() between taking the run and writing it = %+v, want uncertain", reading)
	}
	if err := store.Save(stale); err != nil {
		t.Fatalf("Save(stale) error = %v", err)
	}
	saved, err := store.Load(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Worker == nil || saved.Worker.Attempt != 2 || saved.Status != StatusRunning {
		t.Fatalf("saved record = worker %+v status %s, want attempt 2 kept and the caller's status written", saved.Worker, saved.Status)
	}
	if reading := store.Worker(saved, time.Now()); reading.Liveness != WorkerLive {
		t.Fatalf("Worker() for attempt 2 = %+v, want live", reading)
	}
	// The first attempt's identity no longer matches the stamp beside the lease.
	if reading := store.Worker(reserved, time.Now()); reading.Liveness == WorkerLive {
		t.Fatalf("Worker() for the replaced attempt = %+v, want it not live", reading)
	}
}

// A process number the operating system has given to another process is a
// worker that is gone. Here the number is this test's own, and the start time
// the record names is an hour before this process started: whatever holds the
// number is not the worker, so the answer is absent and never live, even with a
// stamp beside the lease that agrees with the record.
func TestAReusedProcessNumberIsNeverALiveWorker(t *testing.T) {
	t.Parallel()

	look, err := LookProcess(os.Getpid())
	if err != nil || look.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time here (%+v, %v)", look, err)
	}
	store := newTestStore(t)
	state := testState(t, StatusRunning)
	earlier := WorkerIdentity{PID: os.Getpid(), StartedAt: look.StartedAt.Add(-time.Hour).UTC(), Build: "abcdef1", RunID: state.RunID, Attempt: 1}
	state.Worker = &earlier
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.holderPath(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.stampRunHolder(path, earlier); err != nil {
		t.Fatal(err)
	}
	reading := store.Worker(state, time.Now())
	if reading.Liveness != WorkerAbsent || !strings.Contains(reading.Says, "now belongs to a process started at") {
		t.Fatalf("Worker() with a reused number = %+v, want absent", reading)
	}
	// The presence every surface reads is held to the start time too.
	if held, err := store.Held(state.RunID); err != nil || held {
		t.Fatalf("Held() with a reused number = %v, %v, want not held", held, err)
	}
}

// A worker whose process has exited is absent, whatever its stamp says.
func TestAWorkerWhoseProcessExitedIsAbsent(t *testing.T) {
	t.Parallel()

	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatalf("run a process to exit: %v", err)
	}
	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.Worker = &WorkerIdentity{PID: exited.Process.Pid, StartedAt: time.Now().UTC(), RunID: state.RunID, Attempt: 3}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	if reading := store.Worker(state, time.Now()); reading.Liveness != WorkerAbsent || !strings.Contains(reading.Says, "has exited") {
		t.Fatalf("Worker() for an exited process = %+v, want absent", reading)
	}
}

// A record written before workers recorded themselves is read without error,
// and its worker is uncertain: nothing on it can show a process is its worker.
// A stamp written by such a build makes a recorded worker uncertain too.
func TestARecordFromBeforeWorkerIdentityIsReadAndUncertain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"worker"`) {
		t.Fatalf("a record created without a worker carries one: %s", encoded)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() of a record with no worker error = %v", err)
	}
	if reading := store.Worker(loaded, time.Now()); reading.Liveness != WorkerUncertain || reading.Recorded != nil {
		t.Fatalf("Worker() with no identity = %+v, want uncertain", reading)
	}

	look, err := LookProcess(os.Getpid())
	if err != nil || look.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time here (%+v, %v)", look, err)
	}
	identity := WorkerIdentity{PID: os.Getpid(), StartedAt: look.StartedAt.UTC(), ProgramStartedAt: ProgramStartedAt(), RunID: state.RunID, Attempt: 1}
	loaded.Worker = &identity
	holder, err := store.holderPath(state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stamp, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "held_at": time.Now().UTC()})
	if err := os.WriteFile(holder, stamp, 0o600); err != nil {
		t.Fatal(err)
	}
	if reading := store.Worker(loaded, time.Now()); reading.Liveness != WorkerUncertain || !strings.Contains(reading.Says, "no worker identity") {
		t.Fatalf("Worker() with an old stamp = %+v, want uncertain", reading)
	}
}

// writeStamp puts a stamp beside a run's lease as the worker given, which is
// what a program that held the run and was replaced before it let go leaves.
func writeStamp(t *testing.T, store *Store, runID string, worker WorkerIdentity) {
	t.Helper()
	path, err := store.holderPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.stampRunHolder(path, worker); err != nil {
		t.Fatal(err)
	}
}

// A process that replaces its program in place, as the scheduler does to take
// up a deployed build, keeps its number and the start time the operating system
// reports. A worker recorded by the earlier program is gone, and reads so even
// with the stamp it left beside the lease agreeing with the record: the number
// and the operating system's start time match, and the program's start time
// does not. The same identity with this program's start time is live.
func TestAWorkerFromAnEarlierProgramInThisProcessIsGone(t *testing.T) {
	t.Parallel()

	look, err := LookProcess(os.Getpid())
	if err != nil || look.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time here (%+v, %v)", look, err)
	}
	store := newTestStore(t)
	state := testState(t, StatusRunning)
	current := WorkerIdentity{PID: os.Getpid(), StartedAt: look.StartedAt.UTC(), ProgramStartedAt: ProgramStartedAt(), Build: "abcdef1", RunID: state.RunID, Attempt: 1}
	earlier := current
	earlier.ProgramStartedAt = ProgramStartedAt().Add(-15 * 24 * time.Hour)
	before := current
	before.ProgramStartedAt = time.Time{}

	for _, tc := range []struct {
		name   string
		worker WorkerIdentity
		live   bool
		says   string
	}{
		{"the same process number and start times", current, true, "holds the run's lease"},
		{"a different program start time", earlier, false, "was an earlier program in it, started at"},
		{"no program start time, recorded before it was kept", before, false, "from a build that recorded no program start time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := testState(t, StatusRunning)
			worker := tc.worker
			worker.RunID = state.RunID
			state.Worker = &worker
			if err := store.Create(state); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			writeStamp(t, store, state.RunID, worker)
			reading := store.Worker(state, time.Now())
			want := WorkerAbsent
			if tc.live {
				want = WorkerLive
			}
			if reading.Liveness != want || !strings.Contains(reading.Says, tc.says) {
				t.Fatalf("Worker() = %+v, want %s saying %q", reading, want, tc.says)
			}
			if !tc.live && strings.Contains(reading.Says, "let go") {
				t.Fatalf("Worker() = %+v, a worker that is gone must not read as one that let go of the run", reading)
			}
			// What the scheduler and every surface read is held to the same identity.
			if held, err := store.Held(state.RunID); err != nil || held != tc.live {
				t.Fatalf("Held() = %v, %v, want %v", held, err, tc.live)
			}
			presence, err := store.Presence(state, time.Hour, state.UpdatedAt)
			if err != nil || presence.Found != tc.live {
				t.Fatalf("Presence() = %+v, %v, want found %v", presence, err, tc.live)
			}
			if !tc.live && !strings.Contains(presence.Says, "has been replaced") {
				t.Fatalf("Presence() = %+v, want it to say the program was replaced", presence)
			}
		})
	}
}

// A record whose worker carries no start time at all, or names another running
// process without its program's start time, is uncertain and never live: the
// number alone proves nothing, and no process can read when another's program
// began.
func TestAWorkerRecordedWithoutStartTimesIsUncertain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	noStart := testState(t, StatusRunning)
	noStart.Worker = &WorkerIdentity{PID: os.Getpid(), RunID: noStart.RunID, Attempt: 1}
	if err := store.Create(noStart); err != nil {
		t.Fatal(err)
	}
	writeStamp(t, store, noStart.RunID, *noStart.Worker)
	if reading := store.Worker(noStart, time.Now()); reading.Liveness != WorkerUncertain || !strings.Contains(reading.Says, "start time") {
		t.Fatalf("Worker() with no start time = %+v, want uncertain", reading)
	}

	parent, err := LookProcess(os.Getppid())
	if err != nil || !parent.Running() || parent.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time for this test's parent (%+v, %v)", parent, err)
	}
	other := testState(t, StatusRunning)
	other.Worker = &WorkerIdentity{PID: os.Getppid(), StartedAt: parent.StartedAt.UTC(), RunID: other.RunID, Attempt: 1}
	if err := store.Create(other); err != nil {
		t.Fatal(err)
	}
	writeStamp(t, store, other.RunID, *other.Worker)
	writeProgram(t, store, programRecord{PID: os.Getppid(), StartedAt: parent.StartedAt.UTC(), ProgramStartedAt: time.Now().UTC()})
	if reading := store.Worker(other, time.Now()); reading.Liveness != WorkerUncertain || !strings.Contains(reading.Says, "no program start time") {
		t.Fatalf("Worker() naming another process with no program start time = %+v, want uncertain", reading)
	}
}

// writeProgram records, in another process's name, which program is running in
// it, as that program's StartProgram would.
func writeProgram(t *testing.T, store *Store, record programRecord) {
	t.Helper()
	path := store.programPath(record.PID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A reader in another process applies the same rule as the worker's own: the
// worker names a process by its number, the start time the operating system
// reports, and when its program began, and the record the program running there
// now wrote says which program that is. The same program is live; an earlier
// one is gone, never a live worker that let go of the run; a process nothing
// recorded a program for, or a record left by an earlier process with the same
// number, is uncertain.
func TestAnotherProcessIsHeldToTheProgramItRecorded(t *testing.T) {
	t.Parallel()

	parent, err := LookProcess(os.Getppid())
	if err != nil || !parent.Running() || parent.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time for this test's parent (%+v, %v)", parent, err)
	}
	began := time.Date(2026, 10, 5, 20, 12, 0, 0, time.UTC)
	worker := WorkerIdentity{PID: os.Getppid(), StartedAt: parent.StartedAt.UTC(), ProgramStartedAt: began, Build: "abcdef1", Attempt: 1}
	for _, tc := range []struct {
		name     string
		record   *programRecord
		liveness WorkerLiveness
		held     bool
		says     string
	}{
		{"the program that took the run", &programRecord{PID: os.Getppid(), StartedAt: parent.StartedAt.UTC(), ProgramStartedAt: began}, WorkerLive, true, "holds the run's lease"},
		{"a later program in the same process", &programRecord{PID: os.Getppid(), StartedAt: parent.StartedAt.UTC(), ProgramStartedAt: began.Add(15 * 24 * time.Hour)}, WorkerAbsent, false, "was an earlier program in it"},
		{"no record of the program", nil, WorkerUncertain, true, "no record says which program"},
		{"a record from an earlier process with the number", &programRecord{PID: os.Getppid(), StartedAt: parent.StartedAt.Add(-time.Hour).UTC(), ProgramStartedAt: began.Add(time.Hour)}, WorkerUncertain, true, "no record says which program"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newTestStore(t)
			state := testState(t, StatusRunning)
			named := worker
			named.RunID = state.RunID
			state.Worker = &named
			if err := store.Create(state); err != nil {
				t.Fatal(err)
			}
			writeStamp(t, store, state.RunID, named)
			if tc.record != nil {
				writeProgram(t, store, *tc.record)
			}
			reading := store.Worker(state, time.Now())
			if reading.Liveness != tc.liveness || !strings.Contains(reading.Says, tc.says) || strings.Contains(reading.Says, "let go") {
				t.Fatalf("Worker() = %+v, want %s saying %q", reading, tc.liveness, tc.says)
			}
			if held, err := store.Held(state.RunID); err != nil || held != tc.held {
				t.Fatalf("Held() = %v, %v, want %v", held, err, tc.held)
			}
		})
	}
}

// A program starting records itself where every process can read it, and lets
// go of each lease an earlier program in its process left stamped, through the
// lease: the stamp goes and the lease is free. A stamp this program wrote, and a
// lease another holder has taken since, are left as they are.
func TestAStartingProgramRecordsItselfAndReleasesWhatItsEarlierProgramLeft(t *testing.T) {
	t.Parallel()

	look, err := LookProcess(os.Getpid())
	if err != nil || look.StartedAt.IsZero() {
		t.Skipf("the operating system reports no start time here (%+v, %v)", look, err)
	}
	store := newTestStore(t)
	current := WorkerIdentity{PID: os.Getpid(), StartedAt: look.StartedAt.UTC(), ProgramStartedAt: ProgramStartedAt(), Attempt: 1}
	earlier := current
	earlier.ProgramStartedAt = ProgramStartedAt().Add(-time.Hour)

	left := testState(t, StatusRunning)
	ours := testState(t, StatusRunning)
	taken := testState(t, StatusRunning)
	for _, run := range []struct {
		state  State
		worker WorkerIdentity
	}{{left, earlier}, {ours, current}, {taken, earlier}} {
		worker := run.worker
		worker.RunID = run.state.RunID
		run.state.Worker = &worker
		if err := store.Create(run.state); err != nil {
			t.Fatal(err)
		}
		writeStamp(t, store, run.state.RunID, worker)
	}
	// Another holder has the third run's lease now.
	leasePath, err := store.leasePath(taken.RunID)
	if err != nil {
		t.Fatal(err)
	}
	holding, err := os.OpenFile(leasePath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holding.Close()
	if held, err := tryLockStateFile(holding); err != nil || !held {
		t.Fatalf("take the third run's lease = %v, %v", held, err)
	}

	if err := store.StartProgram(); err != nil {
		t.Fatalf("StartProgram() error = %v", err)
	}
	record, known := store.readProgram(os.Getpid())
	if !known || !record.ProgramStartedAt.Equal(ProgramStartedAt()) || !sameStart(record.StartedAt, look.StartedAt) {
		t.Fatalf("program record = %+v, %v; want this program's start time", record, known)
	}
	stamped := func(runID string) bool {
		path, err := store.holderPath(runID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = os.Stat(path)
		return err == nil
	}
	if stamped(left.RunID) || !stamped(ours.RunID) || !stamped(taken.RunID) {
		t.Fatalf("stamps after StartProgram: earlier program's %v, this program's %v, another holder's %v; want only the earlier program's gone",
			stamped(left.RunID), stamped(ours.RunID), stamped(taken.RunID))
	}
	// The earlier program's lease is free for the sweep to take.
	if _, lease, err := store.AdoptRun(context.Background(), left.RunID); err != nil {
		t.Fatalf("AdoptRun() of the released run = %v", err)
	} else {
		lease.Release()
	}
}
