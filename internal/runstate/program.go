package runstate

// Which program is running in a process now, said where any process can read it.
//
// A worker's identity carries the time the program that took the run began
// (worker.go), because the scheduler takes up a deployed build by replacing its
// program in place (internal/redeploy), which keeps the process number and the
// start time the operating system reports. Only a program knows when it began,
// so each program writes it down in its process's name the first time it uses
// a run store, before it takes any run, and a reader in any process compares a
// worker against that record rather than against its own clock. One rule, in
// programVerdict, is what every reading of a worker applies.
//
// A program starting in a process also releases what an earlier program in the
// same process left holding runs (StartProgram): the operating system dropped
// the earlier program's lease locks when it replaced the program, but the stamps
// beside them name a process that is still running and still started when the
// stamps say, and a reader would otherwise take them for live holders.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
)

// EarlierProgramRelease is what a starting program writes on a run whose lease
// it let go of because the run's worker was an earlier program in its process.
type EarlierProgramRelease struct {
	ReleasedAt time.Time `json:"released_at"`
	// Worker is the earlier program's worker, as the stamp beside the lease
	// named it.
	Worker WorkerIdentity `json:"worker"`
	// Says is why the worker was taken for an earlier program, in a sentence a
	// person reads.
	Says string `json:"says"`
}

// programRecord is what a program writes about itself under its process's
// number.
type programRecord struct {
	PID int `json:"pid"`
	// StartedAt is the start time the operating system reports for the process,
	// which tells a record left by an earlier process that had the same number
	// from one written by the process that has it now.
	StartedAt time.Time `json:"started_at,omitempty"`
	// ProgramStartedAt is when the program that wrote the record began.
	ProgramStartedAt time.Time `json:"program_started_at"`
	Build            string    `json:"build,omitempty"`
}

// startedPrograms is the run stores this program has already recorded itself
// in and cleared its earlier program's leases from, keyed by the store's
// directory, so each is done once per program however many stores open it.
var startedPrograms sync.Map

// StartProgram records this program as the one running in this process, and
// lets go of every run lease an earlier program in this process left stamped.
// It does both once per program and store directory; every process that takes
// a run calls it before taking the first, and the scheduler calls it as it
// starts, so a restart releases what its earlier program held before anything
// reads it. A lease another process has since taken is left to it.
func (s *Store) StartProgram() error {
	if _, done := startedPrograms.Load(s.root); done {
		return nil
	}
	if err := s.recordProgram(); err != nil {
		return err
	}
	startedPrograms.Store(s.root, true)
	return s.releaseEarlierProgram()
}

// ProgramRecorded reports this program as having recorded itself in this store.
func (s *Store) ProgramRecorded() bool { return programStarted(s.root) }

func programStarted(root string) bool {
	_, done := startedPrograms.Load(root)
	return done
}

func (s *Store) programPath(pid int) string {
	return filepath.Join(s.root, "programs", strconv.Itoa(pid)+".program.json")
}

// recordProgram writes this program's record, replacing whatever an earlier
// program in this process wrote. It is replaced by rename, so a reader sees one
// whole record or the one before it.
func (s *Store) recordProgram() error {
	record := programRecord{PID: os.Getpid(), ProgramStartedAt: programStartedAt, Build: buildinfo.Commit()}
	if look, err := LookProcess(record.PID); err == nil && !look.StartedAt.IsZero() {
		record.StartedAt = look.StartedAt.UTC()
	}
	directory := filepath.Dir(s.programPath(record.PID))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create the directory programs record themselves in: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".program-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary program record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary program record: %w", err)
	}
	if err := writeJSONFile(temporary, "program record", record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary program record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.programPath(record.PID)); err != nil {
		return fmt.Errorf("replace program record: %w", err)
	}
	return syncDirectory(directory)
}

// readProgram is the record of the program running in a process, and false
// where none was written or it cannot be read: a reader that cannot say which
// program runs there says it does not know rather than guessing.
func (s *Store) readProgram(pid int) (programRecord, bool) {
	file, err := os.Open(s.programPath(pid))
	if err != nil {
		return programRecord{}, false
	}
	defer file.Close()
	var record programRecord
	if err := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes)).Decode(&record); err != nil || record.PID != pid || record.ProgramStartedAt.IsZero() {
		return programRecord{}, false
	}
	return record, true
}

// releaseEarlierProgram lets go of each run lease whose stamp names this
// process as an earlier program, through the lease itself: the lock is taken
// without waiting, which succeeds because the operating system dropped the
// earlier program's lock, and releasing it removes the stamp exactly as any
// holder's release does. A lease somebody holds now is theirs, and is left.
func (s *Store) releaseEarlierProgram() error {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the run leases an earlier program may have left: %w", err)
	}
	var problems []error
	for _, entry := range entries {
		runID, stamp := strings.CutSuffix(entry.Name(), ".holder")
		if !stamp || entry.IsDir() || !runIDPattern.MatchString(runID) {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		holder, err := readRunHolder(path)
		if err != nil || holder.PID != os.Getpid() {
			continue
		}
		verdict, says := s.programVerdict(holder.PID, holder.Worker, ProcessLook{})
		if verdict != programReplaced {
			continue
		}
		if err := s.releaseLeftLease(runID, path, holder.Worker, says); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// releaseLeftLease takes one run's lease without waiting and, holding it,
// records on the run that its worker was an earlier program in this process,
// then lets the lease go, which removes the stamp. The run's status is left as
// it is: the release ends nothing, and the run is then a run with no worker. A
// stamp with no run record behind it is only cleared.
func (s *Store) releaseLeftLease(runID, stamp string, worker *WorkerIdentity, says string) error {
	path, err := s.leasePath(runID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open run %s's lease, which an earlier program left: %w", runID, err)
	}
	held, err := tryLockStateFile(file)
	if err != nil || !held {
		file.Close()
		if err != nil {
			return fmt.Errorf("take run %s's lease, which an earlier program left: %w", runID, err)
		}
		return nil
	}
	lease := &Lease{label: "run", file: file, holder: stamp}
	var noted error
	if state, err := s.Load(runID); err == nil {
		named := state.Worker
		if worker != nil {
			named = worker
		}
		if named != nil {
			state.EarlierProgram = &EarlierProgramRelease{ReleasedAt: time.Now().UTC(), Worker: *named, Says: says}
			if err := s.Save(state); err != nil {
				noted = fmt.Errorf("record on run %s that its worker was an earlier program in this process: %w", runID, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		noted = fmt.Errorf("read run %s to record that its worker was an earlier program in this process: %w", runID, err)
	}
	return errors.Join(noted, lease.Release())
}

// programVerdict is whether a worker or stamp naming a process names the
// program running in it now.
type programVerdict int

const (
	// programCurrent is the program running in the process now.
	programCurrent programVerdict = iota
	// programReplaced is an earlier program in the process: the worker is gone.
	programReplaced
	// programUnknown is a process no record says which program runs in, or a
	// worker that recorded no program start time naming another process.
	programUnknown
)

// programVerdict is the one rule every reading of a worker applies to the
// program it names, with a sentence saying why where it is not the current one.
// look is what the operating system said about the process, and may be empty.
// A reader in the worker's own process asks its own clock; any other reader
// asks the record the process's current program wrote, held to the operating
// system's start time so a record left by an earlier process with the same
// number is not taken for it.
func (s *Store) programVerdict(pid int, worker *WorkerIdentity, look ProcessLook) (programVerdict, string) {
	now, known := programRecord{PID: pid, ProgramStartedAt: programStartedAt}, true
	if pid != os.Getpid() {
		now, known = s.readProgram(pid)
		if known && !now.StartedAt.IsZero() && !look.StartedAt.IsZero() && !sameStart(now.StartedAt, look.StartedAt) {
			known = false
		}
	}
	running := ""
	if known {
		running = now.ProgramStartedAt.Local().Format(personTime)
	}
	switch {
	case !known:
		return programUnknown, fmt.Sprintf("no record says which program is running in pid %d now, so whether it is still the one that took the run cannot be told", pid)
	case worker == nil && pid == os.Getpid():
		return programReplaced, fmt.Sprintf("pid %d is running a program started at %s; the stamp was written by an earlier program in it, from a build that recorded no worker", pid, running)
	case worker == nil:
		return programUnknown, fmt.Sprintf("pid %d is running a program started at %s, and the stamp, from a build that recorded no worker, does not say which program wrote it", pid, running)
	case worker.ProgramStartedAt.IsZero() && pid == os.Getpid():
		return programReplaced, fmt.Sprintf("pid %d is running a program started at %s; attempt %d's worker was an earlier program in it, from a build that recorded no program start time", pid, running, worker.Attempt)
	case worker.ProgramStartedAt.IsZero():
		return programUnknown, fmt.Sprintf("attempt %d's worker, pid %d, was recorded by a build that wrote down no program start time, so whether the process has replaced its program since cannot be told", worker.Attempt, pid)
	case worker.ProgramStartedAt.Equal(now.ProgramStartedAt):
		return programCurrent, ""
	case worker.ProgramStartedAt.Before(now.ProgramStartedAt):
		return programReplaced, fmt.Sprintf("pid %d is running a program started at %s; attempt %d's worker was an earlier program in it, started at %s", pid, running, worker.Attempt,
			worker.ProgramStartedAt.Local().Format(personTime))
	}
	return programUnknown, fmt.Sprintf("attempt %d's worker, pid %d, says its program started at %s, after the program the process's record names, so which program is running there cannot be told",
		worker.Attempt, pid, worker.ProgramStartedAt.Local().Format(personTime))
}

// ThisProgram reports a worker that is the program running in this process.
func (w WorkerIdentity) ThisProgram() bool {
	return w.PID == os.Getpid() && w.ProgramStartedAt.Equal(programStartedAt)
}
