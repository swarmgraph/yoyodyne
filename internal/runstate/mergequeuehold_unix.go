//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// duplicateHold is a second descriptor on the held file's open file
// description, which is what a lock taken with flock belongs to.
func duplicateHold(file *os.File) (*os.File, error) {
	duplicate, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("copy the stage's hold for the process it starts: %w", err)
	}
	syscall.CloseOnExec(duplicate)
	return os.NewFile(uintptr(duplicate), file.Name()), nil
}

// groupAlive says whether a recorded process group may still have a live
// member, on the rule observeExecution applies: a group with no members, or
// with only members that have exited, has stopped, and anything else that
// cannot be established is read as alive.
func groupAlive(group int) (bool, string) {
	if group <= 0 {
		return false, ""
	}
	switch err := syscall.Kill(-group, 0); {
	case errors.Is(err, syscall.ESRCH):
		return false, ""
	case err == nil:
		if exited, _ := groupHasOnlyExited(group); exited {
			return false, ""
		}
		return true, fmt.Sprintf("its process group %d still has members", group)
	default:
		return true, fmt.Sprintf("its process group %d could not be checked: %v", group, err)
	}
}
