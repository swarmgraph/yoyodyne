package runstate

// Which process is working on a run, said precisely enough that a process
// number the operating system has since handed to somebody else is never taken
// for it.
//
// A worker is the process holding a run's lease, and it is identified by its
// process number, the start time the operating system reports for that process,
// the time the program running in it began, the build it runs, and the run and
// attempt number it recorded when it took the run ("How a worker is identified"
// in docs/designs/fresh-factory-health-and-preserved-run-recovery.md). Every
// process that takes a run's lease is a new attempt, so a replacement worker for
// the same run carries a new number and anything decided against the earlier
// one can tell it has been replaced.
//
// The identity is written twice, on purpose: on the run record, where it
// outlives the process, and in the stamp beside the lease (presence.go), which
// is removed when the lease is let go. A worker is live only when the two agree
// and the operating system reports a running process with that number that
// started when the worker said it did. The build and the run and attempt are
// matched between the stamp and the record, because the operating system cannot
// say which build a process runs; the number and the start time are matched
// against the operating system, because only it can say whether that process is
// still the one with the number.
//
// The program's own start time is there because the operating system's is not
// enough. The scheduler takes up a deployed build by replacing its program in
// place (internal/redeploy), which keeps both its process number and the start
// time the operating system reports, so a run taken by the scheduler's earlier
// program names a process that is still running and still started when it said
// it did. Only the program knows when it began, so only a process reading a
// worker that names its own number can settle the question this way; it is
// what keeps the scheduler from taking a run its earlier program held for one
// it holds itself.

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
)

// WorkerIdentity is what a worker recorded about itself when it took a run.
type WorkerIdentity struct {
	PID int `json:"pid"`
	// StartedAt is the start time the operating system reported for the
	// process, and zero where it would not say; a worker with no start time is
	// never reported live, because its number alone proves nothing.
	StartedAt time.Time `json:"started_at,omitempty"`
	// ProgramStartedAt is when the program that took the run began running in
	// the process, which a process replacing its program in place changes and
	// StartedAt does not. It is zero on an identity written by a build that
	// recorded none.
	ProgramStartedAt time.Time `json:"program_started_at,omitempty"`
	// Build is the repository revision the worker's binary was built from, as
	// buildinfo.Commit names it, and empty for a binary that carries none.
	Build   string `json:"build,omitempty"`
	RunID   string `json:"run_id"`
	Attempt int    `json:"attempt"`
}

// Same reports two identities naming one worker: every field equal, the start
// time within the slack the platforms' clocks are read at.
func (w WorkerIdentity) Same(other WorkerIdentity) bool {
	return w.PID == other.PID && w.Build == other.Build && w.RunID == other.RunID &&
		w.Attempt == other.Attempt && sameStart(w.StartedAt, other.StartedAt) &&
		sameStart(w.ProgramStartedAt, other.ProgramStartedAt)
}

// programStartedAt is when the program this process is running began, read
// once as the package is initialized, which a program replacing itself in place
// does again.
var programStartedAt = time.Now().UTC()

// ProgramStartedAt is when the program this process is running began.
func ProgramStartedAt() time.Time { return programStartedAt }

// replacedProgram reports a worker or stamp that names this very process as it
// was before the program now running in it began, and says so in a sentence a
// person reads. Such a worker is gone: the program that took the run is no
// longer running anywhere. A worker naming any other process is not judged
// here, because no process can read when another's program began. A stamp from
// a build that recorded no worker, or a worker with no program start time,
// naming this process was written by an earlier program, since this one
// records both.
func replacedProgram(pid int, worker *WorkerIdentity) (bool, string) {
	if pid != os.Getpid() {
		return false, ""
	}
	now := programStartedAt.Local().Format(personTime)
	switch {
	case worker == nil:
		return true, fmt.Sprintf("pid %d is this process, whose program started at %s; the stamp was written by an earlier program in it, from a build that recorded no worker", pid, now)
	case worker.ProgramStartedAt.IsZero():
		return true, fmt.Sprintf("pid %d is this process, whose program started at %s; attempt %d's worker was an earlier program in it, from a build that recorded no program start time", pid, now, worker.Attempt)
	case !worker.ProgramStartedAt.Equal(programStartedAt):
		return true, fmt.Sprintf("pid %d is this process, whose program started at %s; attempt %d's worker was an earlier program in it, started at %s", pid, now, worker.Attempt,
			worker.ProgramStartedAt.Local().Format(personTime))
	}
	return false, ""
}

// personTime is how a time is written in a sentence a person reads: local, with
// its zone named.
const personTime = "2006-01-02 15:04:05 MST"

func sameStart(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	difference := a.Sub(b)
	if difference < 0 {
		difference = -difference
	}
	return difference <= RecordedStartSlack
}

// WorkerLiveness is the answer to whether a run's worker is still working on
// it. Uncertain is an answer in its own right and is never read as live: it is
// what a reading that could not be completed says.
type WorkerLiveness string

const (
	WorkerLive      WorkerLiveness = "live"
	WorkerAbsent    WorkerLiveness = "absent"
	WorkerUncertain WorkerLiveness = "uncertain"
)

// WorkerReading is one look at a run's worker and what was looked at.
type WorkerReading struct {
	// Recorded is the identity on the run record, absent on a record written by
	// a build that kept none.
	Recorded *WorkerIdentity `json:"recorded,omitempty"`
	// Holder is the stamp beside the run's lease, absent where nothing holds it.
	Holder   *LeaseHolder   `json:"holder,omitempty"`
	Liveness WorkerLiveness `json:"liveness"`
	// Says is why, in a sentence a person reads.
	Says       string    `json:"says"`
	ObservedAt time.Time `json:"observed_at"`
}

// LeaseHolder is the stamp a process holding a run's lease writes beside it.
type LeaseHolder struct {
	PID    int             `json:"pid"`
	HeldAt time.Time       `json:"held_at"`
	Worker *WorkerIdentity `json:"worker,omitempty"`
}

// thisWorker is the identity of this process taking a run as one attempt.
func thisWorker(runID string, attempt int) WorkerIdentity {
	identity := WorkerIdentity{PID: os.Getpid(), ProgramStartedAt: programStartedAt, Build: buildinfo.Commit(), RunID: runID, Attempt: attempt}
	if look, err := LookProcess(identity.PID); err == nil && !look.StartedAt.IsZero() {
		identity.StartedAt = look.StartedAt.UTC()
	}
	return identity
}

// nextAttempt is the attempt a process taking a run now makes: one past the
// last the record names. A record written before attempts were counted names
// none, and the first counted attempt is the first.
func nextAttempt(state State) int {
	if state.Worker == nil || state.Worker.Attempt < 1 {
		return 1
	}
	return state.Worker.Attempt + 1
}

// beginAttempt makes this process the run's worker for a new attempt, under the
// lease the caller has just taken. The attempt is stamped beside the lease at
// once and reaches the record with the worker's first write of it (Save), not
// before: taking a run and deciding to do nothing must leave its record as it
// was. Until that write a reader sees a stamp newer than the record and reports
// the worker uncertain rather than live. The state handed back carries the new
// attempt, so the caller's copy is what its first write records. A lease whose
// attempt cannot be stamped is let go rather than kept, for the reason
// takeLease gives about the stamp.
func (s *Store) beginAttempt(lease *Lease, state State) (State, error) {
	worker := thisWorker(state.RunID, nextAttempt(state))
	if err := s.stampLease(lease, state.RunID, worker); err != nil {
		return State{}, errors.Join(err, lease.Release())
	}
	state.Worker = &worker
	return state, nil
}

// heldWorkers is the worker attempt this process holds each run's lease as,
// keyed by the stamp beside the lease, so every store in the process that
// writes the run records the same attempt. An entry is made when the lease is
// stamped and removed when it is let go.
var heldWorkers = struct {
	sync.Mutex
	byHolder map[string]WorkerIdentity
}{byHolder: map[string]WorkerIdentity{}}

func holdWorker(holder string, worker WorkerIdentity) {
	heldWorkers.Lock()
	defer heldWorkers.Unlock()
	heldWorkers.byHolder[holder] = worker
}

func releaseWorker(holder string) {
	heldWorkers.Lock()
	defer heldWorkers.Unlock()
	delete(heldWorkers.byHolder, holder)
}

// workerToWrite is the worker a whole-record write of a run records: the
// attempt this process holds the run's lease as, where it holds it, and
// otherwise the worker already stored. The caller's copy is never what
// decides, because a copy read before the current attempt began would put an
// earlier worker back.
func (s *Store) workerToWrite(runID string, stored *WorkerIdentity) *WorkerIdentity {
	holder, err := s.holderPath(runID)
	if err != nil {
		return stored
	}
	heldWorkers.Lock()
	defer heldWorkers.Unlock()
	if worker, held := heldWorkers.byHolder[holder]; held {
		return &worker
	}
	return stored
}

// Holder is the stamp beside a run's lease, or nil where nothing holds it.
func (s *Store) Holder(runID string) (*LeaseHolder, error) {
	path, err := s.holderPath(runID)
	if err != nil {
		return nil, err
	}
	holder, err := readRunHolder(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &LeaseHolder{PID: holder.PID, HeldAt: holder.HeldAt, Worker: holder.Worker}, nil
}

// Worker reports whether the worker a run's record names is still working on
// it. It takes nothing to answer and never fails: what it could not read makes
// the answer uncertain, and says what could not be read.
//
// The operating system is asked first, because what it says settles the
// question outright when the answer is no: a number nothing has, or a number
// now held by a process that started at another time — a reused number — is a
// worker that is gone, whatever the stamp says. A number that is this process's
// own is then held to when this process's program began, which settles it the
// same way. Only a process that matches is then held to the stamp, which must
// name the same worker the record does. A worker recorded without its program's
// start time, naming another process, is never live: that process may have
// replaced its program since, and nothing here can tell.
func (s *Store) Worker(state State, now time.Time) WorkerReading {
	reading := WorkerReading{Recorded: state.Worker, ObservedAt: now.UTC()}
	holder, holderErr := s.Holder(state.RunID)
	reading.Holder = holder
	uncertain := func(says string) WorkerReading {
		reading.Liveness, reading.Says = WorkerUncertain, says
		return reading
	}
	absent := func(says string) WorkerReading {
		reading.Liveness, reading.Says = WorkerAbsent, says
		return reading
	}
	recorded := state.Worker
	if recorded == nil {
		return uncertain("the run was recorded by a build that wrote down no worker identity, so no process can be shown to be its worker")
	}
	if recorded.PID <= 0 || recorded.Attempt < 1 || recorded.RunID != state.RunID {
		return uncertain(fmt.Sprintf("the worker identity on the record is incomplete or names another run (pid %d, run %q, attempt %d)", recorded.PID, recorded.RunID, recorded.Attempt))
	}
	look, err := LookProcess(recorded.PID)
	if err != nil {
		return uncertain(fmt.Sprintf("the operating system could not be asked about pid %d: %v", recorded.PID, err))
	}
	if !look.Running() {
		return absent(fmt.Sprintf("attempt %d's worker, pid %d, has exited", recorded.Attempt, recorded.PID))
	}
	if look.StartedAt.IsZero() || recorded.StartedAt.IsZero() {
		return uncertain(fmt.Sprintf("a process has pid %d, but the start time that would show it is attempt %d's worker is not known", recorded.PID, recorded.Attempt))
	}
	if !sameStart(look.StartedAt, recorded.StartedAt) {
		return absent(fmt.Sprintf("pid %d now belongs to a process started at %s, not attempt %d's worker, which started at %s",
			recorded.PID, look.StartedAt.UTC().Format(time.RFC3339), recorded.Attempt, recorded.StartedAt.UTC().Format(time.RFC3339)))
	}
	if replaced, says := replacedProgram(recorded.PID, recorded); replaced {
		return absent(says)
	}
	switch {
	case holderErr != nil:
		return uncertain(fmt.Sprintf("the stamp beside the run's lease could not be read: %v", holderErr))
	case holder == nil:
		return absent(fmt.Sprintf("nothing holds the run's lease, so attempt %d's worker is not working on it; pid %d is still running, as that worker or as a program that has replaced it", recorded.Attempt, recorded.PID))
	case holder.Worker == nil:
		return uncertain(fmt.Sprintf("the run's lease is held by pid %d, stamped by a build that wrote down no worker identity", holder.PID))
	case !holder.Worker.Same(*recorded):
		return uncertain(fmt.Sprintf("the run's lease is stamped by attempt %d, pid %d, and the record names attempt %d, pid %d; a new attempt may be starting",
			holder.Worker.Attempt, holder.Worker.PID, recorded.Attempt, recorded.PID))
	case recorded.ProgramStartedAt.IsZero():
		return uncertain(fmt.Sprintf("attempt %d's worker, pid %d, was recorded by a build that wrote down no program start time, so whether the process has replaced its program since cannot be told", recorded.Attempt, recorded.PID))
	}
	reading.Liveness = WorkerLive
	reading.Says = fmt.Sprintf("attempt %d's worker, pid %d, started at %s running a program started at %s on build %s, holds the run's lease", recorded.Attempt, recorded.PID,
		recorded.StartedAt.UTC().Format(time.RFC3339), recorded.ProgramStartedAt.UTC().Format(time.RFC3339), nonEmptyBuild(recorded.Build))
	return reading
}

func nonEmptyBuild(build string) string {
	if build == "" {
		return "(unrecorded)"
	}
	return build
}
