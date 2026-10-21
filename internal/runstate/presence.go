package runstate

// Whether a process can be found behind a run in flight.
//
// A run's record says "running" from the moment it is reserved until something
// writes its ending, and a process that dies writes nothing. So the status field
// is a claim the process made about itself, and the one question every reader
// of an in-flight run actually has — is anything working on it — is not in the
// record at all. The lease answers it for whoever takes the lease, which is how
// the sweep and the claim audit have always decided; but a surface that only
// reads must not take it, because for the instant it lasts a reading is a second
// owner, and a process adopting the run in that instant would be refused.
//
// So a process that takes a run's lease writes down which process it is beside
// the lease, exactly as a conversation's holder does, and a reader checks the
// process it names rather than taking anything. That is what lets `yoyo status`
// and the dashboard say a run has no process behind it rather than printing it
// as running: on 2026-09-26 run-3b94404c read as running, in developer slot 1,
// for twenty hours after its process exited
// (docs/diagnoses/yoyodyne-ifd-428-49-dead-run-held-its-slot.md).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runHolder is what a process holding a run's lease writes beside it.
type runHolder struct {
	// PID is the process that took the lease. A holder killed outright leaves the
	// stamp behind, and a reader that finds no such process reports what the
	// operating system already decided when it dropped the lock.
	PID int `json:"pid"`
	// HeldAt is when the lease was taken, written so a state directory somebody
	// reads by hand says when. Nothing decides from it.
	HeldAt time.Time `json:"held_at"`
	// Worker is the identity the holder took the run with (worker.go). A stamp
	// written by a build that kept none carries only the process number.
	Worker *WorkerIdentity `json:"worker,omitempty"`
}

// holderRunning reports whether the process a stamp names is still the process
// that wrote it, and where it is not, says why. A stamp that recorded its
// process's start time is held to it, so a number the operating system has
// handed to another process reads as the holder gone rather than as the holder;
// and a stamp naming this process is held to when its program began, so one an
// earlier program in this process left behind reads as gone too
// (replacedProgram).
func holderRunning(holder runHolder) (bool, string, error) {
	look, err := LookProcess(holder.PID)
	if err != nil {
		return false, "", err
	}
	if !look.Running() {
		return false, fmt.Sprintf("the process that held it, pid %d, has exited without recording an ending", holder.PID), nil
	}
	if holder.Worker != nil && !holder.Worker.StartedAt.IsZero() && !look.StartedAt.IsZero() && !sameStart(holder.Worker.StartedAt, look.StartedAt) {
		return false, fmt.Sprintf("the process that held it, pid %d, has exited without recording an ending, and its number now belongs to a process started at %s",
			holder.PID, look.StartedAt.Local().Format(personTime)), nil
	}
	if replaced, says := replacedProgram(holder.PID, holder.Worker); replaced {
		return false, "the program that held it has been replaced without recording an ending: " + says, nil
	}
	return true, "", nil
}

// RunPresence is whether a process can be found behind one run in flight, and
// what was looked at to say so.
type RunPresence struct {
	// Found is a process holding the run, or a run too recently moved to say
	// otherwise. It is the answer every reading assumed before this existed, so a
	// reading that could not be made is reported as found and never as missing.
	Found bool `json:"found"`
	// Says is why no process can be found, in a sentence a person reads, and is
	// empty where one was.
	Says string `json:"says,omitempty"`
	// LastMoved is the later of the record's last write and its event log's, which
	// is the last moment anything was demonstrably working on the run.
	LastMoved time.Time `json:"last_moved,omitempty"`
}

// Held observes the process holding a run, including a finished run whose
// landing checks are still executing. It never takes the run's lease.
func (s *Store) Held(runID string) (bool, error) {
	path, err := s.holderPath(runID)
	if err != nil {
		return false, err
	}
	holder, err := readRunHolder(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	running, _, err := holderRunning(holder)
	return running, err
}

// Presence reports whether a process can be found behind a run, and takes
// nothing to answer it.
//
// A stamp naming a live process is a holder. A stamp naming a process that is
// gone is a holder that was killed, and is said to be at once. No stamp at all is
// either a run whose process let go of it — a park that returns and exits, or a
// death the stamp's removal preceded — or a holder from a build that wrote no
// stamp, and those two are told apart only by the run going quiet: a run nothing
// has written to, record or event log, for quiet is reported as having no
// process, and one written to more recently is given the benefit of the doubt.
// A run that is not in flight has no process to find and reports found.
func (s *Store) Presence(state State, quiet time.Duration, now time.Time) (RunPresence, error) {
	if !state.Status.InFlight() {
		return RunPresence{Found: true}, nil
	}
	lastMoved, err := s.lastMoved(state)
	if err != nil {
		return RunPresence{Found: true}, err
	}
	presence := RunPresence{LastMoved: lastMoved}
	path, err := s.holderPath(state.RunID)
	if err != nil {
		return RunPresence{Found: true}, err
	}
	holder, err := readRunHolder(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if now.Sub(lastMoved) < quiet {
			presence.Found = true
			return presence, nil
		}
		presence.Says = fmt.Sprintf("no process holds it, and nothing has been written to it since %s",
			lastMoved.UTC().Format(time.RFC3339))
		return presence, nil
	case err != nil:
		return RunPresence{Found: true}, err
	}
	running, gone, err := holderRunning(holder)
	if err != nil {
		return RunPresence{Found: true}, fmt.Errorf("ask whether run %s's holder is running: %w", state.RunID, err)
	}
	if running {
		presence.Found = true
		return presence, nil
	}
	presence.Says = fmt.Sprintf("%s, and nothing has been written to it since %s", gone, lastMoved.UTC().Format(time.RFC3339))
	return presence, nil
}

// lastMoved is the later of the run's record and its event log. The event log is
// read by its modification time rather than parsed, because a streaming provider
// appends to it every few seconds and parsing it whole to find the last line
// would make every status reading as expensive as pricing the run.
func (s *Store) lastMoved(state State) (time.Time, error) {
	moved := state.UpdatedAt
	path, err := s.eventPath(state.RunID)
	if err != nil {
		return moved, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return moved, nil
	}
	if err != nil {
		return moved, fmt.Errorf("read when run %s last wrote an event: %w", state.RunID, err)
	}
	if info.ModTime().After(moved) {
		moved = info.ModTime()
	}
	return moved, nil
}

// holderPath names the stamp beside a run's lease. It is not a `.json` file, so
// what the listings read stays the runs themselves.
func (s *Store) holderPath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".holder"), nil
}

// stampLease writes this process's stamp, as the worker it took the run as,
// beside a run lease it now holds, and has the lease clear it when let go.
func (s *Store) stampLease(lease *Lease, runID string, worker WorkerIdentity) error {
	path, err := s.holderPath(runID)
	if err != nil {
		return err
	}
	if err := s.stampRunHolder(path, worker); err != nil {
		return err
	}
	lease.holder = path
	holdWorker(path, worker)
	return nil
}

// stampRunHolder writes this process's stamp for a run whose lease it now holds.
// It is replaced by rename rather than written in place, so a reader sees the
// whole of one stamp or none of it.
func (s *Store) stampRunHolder(path string, worker WorkerIdentity) error {
	temporary, err := os.CreateTemp(s.root, ".holder-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary run holder: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary run holder: %w", err)
	}
	if err := writeJSONFile(temporary, "run holder", runHolder{PID: worker.PID, HeldAt: time.Now().UTC(), Worker: &worker}); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary run holder: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace run holder: %w", err)
	}
	return syncDirectory(s.root)
}

// readRunHolder is one stamp as it sits on disk. A stamp that will not decode is
// a failure to answer rather than an absence, because a reader that guessed at
// it would be inventing whether anybody is working on the run.
func readRunHolder(path string) (runHolder, error) {
	file, err := os.Open(path)
	if err != nil {
		return runHolder{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var holder runHolder
	if err := decoder.Decode(&holder); err != nil {
		return runHolder{}, fmt.Errorf("decode run holder %s: %w", filepath.Base(path), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return runHolder{}, fmt.Errorf("decode run holder %s: %w", filepath.Base(path), err)
	}
	if holder.PID <= 0 {
		return runHolder{}, fmt.Errorf("run holder %s names no process", filepath.Base(path))
	}
	return holder, nil
}

// DeadRunRemedy is what ends a run in flight that has no process behind it, in
// a sentence a person reads beside the run. It is not the same for every run,
// and a surface that said one remedy for all of them would send an operator to
// a command that does nothing: the reconciling sweep settles a parked run
// nothing continues once its record has sat still for the grace, but it leaves
// a run parked on the operator's pause while the pause stands, it continues
// a usage-limit or overload wait once its deadline has passed rather than
// settling it, and it leaves a run paused on work its item waits on to the
// watching session's pull, which continues it once that work closes. Those are
// the sweep's own rules (Reconciler.parkNothingServes in
// internal/orchestrator), read here from the same fields, so the surfaces carry
// the answer rather than each writing their own.
func DeadRunRemedy(state State, grace time.Duration) string {
	item := strings.TrimSpace(state.WorkItemID)
	switch {
	case state.OperatorHeldSince != nil:
		return fmt.Sprintf("it is parked on the operator's pause, which the sweep leaves alone while it stands: `yoyo run %s` continues it, and once the pause is lifted `yoyo reconcile` settles it if nothing has continued it within %s", item, grace)
	case state.DependencyPause != nil:
		return fmt.Sprintf("it is paused because %s waits on unfinished work (%s): a watching `yoyo work` session continues it at the first pull after that work closes, and the sweep leaves it until then", item, state.DependencyPause.Summary())
	case state.UsageLimitResetsAt != nil && (state.PauseCause == PauseUsageLimit || state.PauseCause == PauseServerOverload || state.PauseCause == ""):
		return fmt.Sprintf("it is waiting out %s until %s: `yoyo reconcile` continues it once that has passed, and `yoyo run %s` continues it sooner",
			DescribePause(state.PauseCause, state.UsageLimitKind), state.UsageLimitResetsAt.UTC().Format(time.RFC3339), item)
	}
	return fmt.Sprintf("`yoyo reconcile` settles it — a parked run once its record has not moved for %s — and `yoyo run %s` continues it before then", grace, item)
}
