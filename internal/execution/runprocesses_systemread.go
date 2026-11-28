//go:build darwin || linux

package execution

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"syscall"
	"time"
)

// systemProgram is the system program the check below starts: one macOS
// ships, whose starting environment it may withhold.
const systemProgram = "/bin/sleep"

// ReadsSystemProgramEnvironment starts systemProgram marked with an identifier
// of its own and says whether this harness can read that process's starting
// environment (docs/designs/stopping-what-a-run-started.md, decision 2). On
// November 27, 2026 macOS gave a developer run's sandbox no environment for
// such a process, so a leftover running a system program can be stopped only
// by the run's identifier on a running parent; the maintenance pass runs
// outside any sandbox and records the answer there. The process ends on its own
// after five seconds and is stopped and waited for before this returns.
func (r RunProcesses) ReadsSystemProgramEnvironment() (bool, error) {
	table := r.table
	if table == nil {
		table = systemProcessTable{}
	}
	marker := RunIdentityVariable + "=system-program-check-" + strconv.Itoa(os.Getpid())
	probe := exec.Command(systemProgram, "5")
	probe.Env = []string{"PATH=/bin:/usr/bin", marker}
	if err := probe.Start(); err != nil {
		return false, err
	}
	pid := probe.Process.Pid
	defer func() {
		_ = probe.Process.Signal(syscall.SIGKILL)
		_ = probe.Wait()
	}()
	deadline := time.Now().Add(time.Second)
	for {
		entries, err := table.list()
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if entry.PID == pid && entry.Environment != nil {
				return slices.Contains(entry.Environment, marker), nil
			}
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}
