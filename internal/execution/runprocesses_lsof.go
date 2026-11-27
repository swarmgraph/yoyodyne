//go:build darwin || linux

package execution

import (
	"context"
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
