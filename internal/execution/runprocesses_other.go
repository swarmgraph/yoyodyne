//go:build !darwin && !linux

package execution

import (
	"errors"
	"syscall"
)

// systemProcessTable cannot read another process's starting environment on
// this platform, so it says so rather than finding nothing.
type systemProcessTable struct{}

var errRunProcessesUnsupported = errors.New("reading another process's environment is unsupported on this platform")

func (systemProcessTable) list() ([]processEntry, error) { return nil, errRunProcessesUnsupported }

func (systemProcessTable) workingDirectories([]int) map[int]string { return nil }

func (systemProcessTable) listening(int) []string { return nil }

func (systemProcessTable) holding([]string) map[int]bool { return nil }

func sendSignal(int, syscall.Signal) error { return errRunProcessesUnsupported }

// ReadsSystemProgramEnvironment cannot be answered on this platform.
func (RunProcesses) ReadsSystemProgramEnvironment() (bool, error) {
	return false, errRunProcessesUnsupported
}
