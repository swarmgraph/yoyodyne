//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// observe says whether a started check's processes are alive, stopped, or
// neither can be established, with the reason in a sentence a person reads. It
// reads them as observeExecution reads a provider attempt's: the permit's file
// free, and the process group the check led with no members, looked at twice.
// It is called under the record's lock, which is the only thing that opens a
// permit's file to test it, so nothing can take the file between the answer
// and the record of it.
func (s *HeavyCheckStore) observe(root *repowrite.PinnedRoot, permit HeavyCheckPermit) (executionState, string) {
	process := permit.Process
	if process == nil {
		return executionUnknown, "it was never written down as started"
	}
	if boot := s.currentBoot(); process.Boot != "" && boot != "" && boot != process.Boot {
		return executionStopped, "the machine has restarted since it was started"
	}
	file, err := s.takeHold(root, permit)
	if err != nil {
		return executionUnknown, fmt.Sprintf("the file that shows whether its processes are alive cannot be read: %v", err)
	}
	if file == nil {
		return executionAlive, fmt.Sprintf("pid %d, or a process it started, is still running", process.PID)
	}
	defer releaseStateFile(file)
	signal := s.signalGroup
	if signal == nil {
		signal = func(group int) error { return syscall.Kill(-group, 0) }
	}
	err = lookAtGroup(signal, process.ProcessGroup)
	if err == nil {
		time.Sleep(groupRecheck)
		err = lookAtGroup(signal, process.ProcessGroup)
	}
	switch {
	case errors.Is(err, syscall.ESRCH):
		return executionStopped, ""
	case err == nil:
		return executionAlive, fmt.Sprintf("its process group %d still has members, which may be processes it started", process.ProcessGroup)
	case errors.Is(err, syscall.EPERM):
		return executionUnknown, fmt.Sprintf("its process group %d has members this harness is not permitted to see", process.ProcessGroup)
	default:
		return executionUnknown, fmt.Sprintf("its process group %d could not be checked: %v", process.ProcessGroup, err)
	}
}
