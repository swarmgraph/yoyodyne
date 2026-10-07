//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// observeExecution says whether an execution is alive, stopped, or neither can
// be established, with the reason in a sentence a person reads. Where it finds
// the execution stopped, it is holding the execution's hold when it returns,
// so nothing can take the hold between the answer and the record of it; settle
// lets it go, removing the file first when the stop has been recorded. settle
// may be called more than once, and only the first call acts.
//
// What establishes a stop is the hold being free, which means no process of
// the tree still has the file it inherited, together with the process group
// the execution led having no members, which means no process that let go of
// the file is left either. A process identifier that answers proves nothing
// either way: it may have been given to an unrelated process since.
func (s *Store) observeExecution(runID string, identity ExecutionIdentity) (executionState, string, func(remove bool)) {
	nothing := func(bool) {}
	host, err := os.Hostname()
	if err != nil {
		return executionUnknown, fmt.Sprintf("this host could not be named, so pid %d cannot be checked: %v", identity.PID, err), nothing
	}
	if host != identity.Host {
		return executionUnknown, fmt.Sprintf("it was started on %s, and this is %s, which cannot see its processes", identity.Host, host), nothing
	}
	if boot := currentBoot(); identity.Boot != "" && boot != "" && boot != identity.Boot {
		return executionStopped, "the machine has restarted since it was started", nothing
	}
	path, err := s.holdPath(runID, attemptOfHold(identity.Hold))
	if err != nil {
		return executionUnknown, fmt.Sprintf("its hold %q is not one this harness names: %v", identity.Hold, err), nothing
	}
	hold, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return executionUnknown, fmt.Sprintf("the file that shows whether its processes are alive, %s, is missing", identity.Hold), nothing
	}
	if err != nil {
		return executionUnknown, fmt.Sprintf("the file that shows whether its processes are alive, %s, cannot be opened: %v", identity.Hold, err), nothing
	}
	settled := false
	settle := func(remove bool) {
		if settled {
			return
		}
		settled = true
		if remove {
			_ = os.Remove(path)
		}
		_ = releaseStateFile(hold)
	}
	current, err := fileIdentity(hold)
	if err != nil || current != identity.HoldFile {
		settle(false)
		return executionUnknown, fmt.Sprintf("the file that shows whether its processes are alive, %s, has been replaced since it was started", identity.Hold), nothing
	}
	taken, err := tryLockStateFile(hold)
	if err != nil {
		settle(false)
		return executionUnknown, fmt.Sprintf("the file that shows whether its processes are alive, %s, cannot be locked: %v", identity.Hold, err), nothing
	}
	if !taken {
		settle(false)
		return executionAlive, fmt.Sprintf("pid %d, or a process it started, is still running", identity.PID), nothing
	}
	signal := s.signalGroup
	if signal == nil {
		signal = func(group int) error { return syscall.Kill(-group, 0) }
	}
	switch err := signal(identity.ProcessGroup); {
	case errors.Is(err, syscall.ESRCH):
		return executionStopped, "", settle
	case err == nil:
		settle(false)
		return executionUnknown, fmt.Sprintf("its process group %d still has members, which may be processes it started", identity.ProcessGroup), nothing
	case errors.Is(err, syscall.EPERM):
		settle(false)
		return executionUnknown, fmt.Sprintf("its process group %d has members this harness is not permitted to see", identity.ProcessGroup), nothing
	default:
		settle(false)
		return executionUnknown, fmt.Sprintf("its process group %d could not be checked: %v", identity.ProcessGroup, err), nothing
	}
}

// fileIdentity is a file's device and inode, which tell it apart from a file
// put at the same path later.
func fileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("read the identity of %s: %w", file.Name(), err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("the identity of %s is not available here", file.Name())
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}
