package runstate

// What shows whether a merge queue stage an earlier worker started is still
// running. It is the run launch's hold (launch.go) applied to the queue: each
// stage of a generation has a file beside the queue's record, the worker locks
// it before the stage starts, and every process the stage starts inherits the
// locked file. The lock is released only once the last process holding the file
// has exited, so a worker that died with a check or a review still running
// leaves the lock taken, and the next worker finds it taken and starts nothing
// beside it. The process each launch was is written down on the generation as
// well (MergeQueueLaunch), so a process that let go of the file is still looked
// for by its process group.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// MergeQueueStageRunningError is a stage a process started earlier may still
// be running, so nothing is started beside it.
type MergeQueueStageRunningError struct {
	Generation uint64
	Stage      MergeQueueStage
	Reason     string
}

func (e MergeQueueStageRunningError) Error() string {
	return fmt.Sprintf("the %s of merge queue generation %d may still be running from an earlier start (%s), so nothing is started beside it", e.Stage, e.Generation, e.Reason)
}

// MergeQueueStageHold is one stage's hold, taken by the worker that is about to
// start it.
type MergeQueueStageHold struct {
	file *os.File
	root *repowrite.PinnedRoot
}

func stageHoldName(entryID string, number uint64, stage MergeQueueStage) string {
	return fmt.Sprintf("hold-%s-%d-%s", entryID, number, stage)
}

// HoldStage takes the hold of one stage of a generation before the stage is
// started. It is refused with MergeQueueStageRunningError where the hold is
// still taken — by a process an earlier start left running — and with
// ErrMergeQueueWorkerLeaseRequired for anyone but the queue's worker.
func (s *MergeQueueStore) HoldStage(worker *Lease, key MergeQueueKey, generation MergeQueueGeneration, stage MergeQueueStage) (*MergeQueueStageHold, error) {
	if worker == nil || worker.file == nil || worker.label != mergeQueueWorkerLabel || worker.scope != key.directory() {
		return nil, ErrMergeQueueWorkerLeaseRequired
	}
	if running, reason, err := s.StageRunning(key, generation, stage); err != nil || running {
		if err != nil {
			return nil, err
		}
		return nil, MergeQueueStageRunningError{Generation: generation.Number, Stage: stage, Reason: reason}
	}
	queueRoot, err := s.openQueue(key)
	if err != nil {
		return nil, err
	}
	file, err := queueRoot.OpenLock(stageHoldName(generation.EntryID, generation.Number, stage), 0o600)
	if err != nil {
		queueRoot.Close()
		return nil, fmt.Errorf("open the hold of the %s of generation %d: %w", stage, generation.Number, err)
	}
	taken, err := tryLockStateFile(file)
	if err != nil || !taken {
		file.Close()
		queueRoot.Close()
		if err != nil {
			return nil, fmt.Errorf("lock the hold of the %s of generation %d: %w", stage, generation.Number, err)
		}
		return nil, MergeQueueStageRunningError{Generation: generation.Number, Stage: stage, Reason: "another process took its hold first"}
	}
	return &MergeQueueStageHold{file: file, root: queueRoot}, nil
}

// Inherited is a copy of the held file to hand one process the stage starts,
// for an execution.LaunchGate to pass on and then close. It shares the lock,
// so the stage stays held for as long as that process or anything it starts
// is alive. It is nil on a platform where a launch cannot be gated.
func (h *MergeQueueStageHold) Inherited() (*os.File, error) {
	return duplicateHold(h.file)
}

// Close lets go of the worker's own copy of the hold. It does not unlock it:
// the lock belongs to every copy, and a process the stage started that is
// still alive keeps it taken, which is the whole of what the hold is for.
func (h *MergeQueueStageHold) Close() error {
	if h == nil || h.file == nil {
		return nil
	}
	err := h.file.Close()
	h.file = nil
	_ = h.root.Close()
	return err
}

// StageRunning reports whether a process started for one stage of a
// generation may still be running, and why. A stage nothing was ever started
// for is not running. One whose hold is taken is. One whose hold is free is
// running still where a process group it recorded has members on this
// machine, or where it recorded a process on another machine, which this one
// cannot see; an answer nothing can establish is never read as stopped.
func (s *MergeQueueStore) StageRunning(key MergeQueueKey, generation MergeQueueGeneration, stage MergeQueueStage) (bool, string, error) {
	if err := key.validate(); err != nil {
		return false, "", fmt.Errorf("merge queue: %w", err)
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return false, "", fmt.Errorf("pin the merge queue state root: %w", err)
	}
	defer root.Close()
	name := path.Join(filepath.ToSlash(s.directory()), key.directory(), stageHoldName(generation.EntryID, generation.Number, stage))
	var launched []MergeQueueLaunch
	for _, launch := range generation.Launches {
		if launch.Stage == stage {
			launched = append(launched, launch)
		}
	}
	if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		if len(launched) > 0 {
			return true, "the file that shows whether its processes are alive is missing", nil
		}
		return false, "", nil
	} else if err != nil {
		return false, "", fmt.Errorf("inspect the hold of the %s of generation %d: %w", stage, generation.Number, err)
	}
	file, err := root.OpenLock(name, 0o600)
	if err != nil {
		return false, "", fmt.Errorf("open the hold of the %s of generation %d: %w", stage, generation.Number, err)
	}
	taken, err := tryLockStateFile(file)
	if err != nil {
		file.Close()
		return true, fmt.Sprintf("its hold could not be locked: %v", err), nil
	}
	if !taken {
		file.Close()
		return true, describeLaunches("a process it started is still running", launched), nil
	}
	_ = releaseStateFile(file)
	host, _ := os.Hostname()
	for _, launch := range launched {
		if launch.Host != host {
			return true, fmt.Sprintf("pid %d was started on %s, another machine, whose processes this one cannot see", launch.PID, launch.Host), nil
		}
		if alive, reason := groupAlive(launch.ProcessGroup); alive {
			return true, reason, nil
		}
	}
	return false, "", nil
}

func describeLaunches(what string, launched []MergeQueueLaunch) string {
	if len(launched) == 0 {
		return what
	}
	last := launched[len(launched)-1]
	return fmt.Sprintf("%s (last started as pid %d)", what, last.PID)
}

// StageHoldPath is where one stage's hold is, for a reader that wants to look
// at it; nothing outside this file takes it.
func (s *MergeQueueStore) StageHoldPath(key MergeQueueKey, generation MergeQueueGeneration, stage MergeQueueStage) string {
	return filepath.Join(s.Root(), key.directory(), stageHoldName(generation.EntryID, generation.Number, stage))
}
