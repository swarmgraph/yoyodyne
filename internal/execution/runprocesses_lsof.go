//go:build darwin || linux

package execution

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// lsofTimeout bounds one lsof call. A machine whose lsof cannot answer in this
// long gives the record no listening addresses rather than holding up the end
// of a run.
const lsofTimeout = 10 * time.Second

// lsofFields runs lsof with field output and returns, for each process it
// names, the values of its n fields. An lsof that is missing or refuses gives
// nothing.
func lsofFields(args ...string) map[int][]string {
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	output, _ := exec.CommandContext(ctx, "lsof", append([]string{"-nP", "-w", "-F", "pn"}, args...)...).Output()
	named := map[int][]string{}
	pid := 0
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid > 0 {
				named[pid] = append(named[pid], line[1:])
			}
		}
	}
	return named
}

// lsofListening is every TCP address and port pid listens on.
func lsofListening(pid int) []string {
	return lsofFields("-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN")[pid]
}

// lsofWorkingDirectories is where each process is working, as lsof says.
func lsofWorkingDirectories(pids []int) map[int]string {
	if len(pids) == 0 {
		return nil
	}
	listed := make([]string, 0, len(pids))
	for _, pid := range pids {
		listed = append(listed, strconv.Itoa(pid))
	}
	directories := map[int]string{}
	for pid, names := range lsofFields("-a", "-d", "cwd", "-p", strings.Join(listed, ",")) {
		if len(names) > 0 {
			directories[pid] = names[0]
		}
	}
	return directories
}

// lsofHolding is every process that has one of the files open, as lsof says.
func lsofHolding(paths []string) map[int]bool {
	holding := map[int]bool{}
	for pid := range lsofFields(append([]string{"--"}, paths...)...) {
		holding[pid] = true
	}
	return holding
}

// lsofOutput runs lsof with field output and returns what it printed. lsof
// exits 1 both when it finds nothing and when it could not read some of what
// it was asked about, and prints what it could read either way, so only a
// listing that never ran, or ran out of time, is an error.
func lsofOutput(fields string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "lsof", append([]string{"-nP", "-w", "-F", fields}, args...)...).Output()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, fmt.Errorf("lsof did not answer within %s", lsofTimeout)
	case err == nil, errors.As(err, &exit) && exit.ExitCode() == 1:
		return output, nil
	default:
		return nil, fmt.Errorf("lsof could not be run: %w", err)
	}
}
