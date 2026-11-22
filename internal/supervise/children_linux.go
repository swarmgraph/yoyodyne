//go:build linux

package supervise

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// childrenOf lists the processes whose parent is pid, from each process's
// /proc/<pid>/stat: the fields after the command's closing parenthesis are its
// state, its parent, and its group.
func childrenOf(pid int) ([]process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read the process table: %w", err)
	}
	var children []process
	for _, entry := range entries {
		id, err := strconv.Atoi(entry.Name())
		if err != nil || id == pid {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			// Gone between the listing and the read.
			continue
		}
		closing := strings.LastIndexByte(string(stat), ')')
		if closing < 0 {
			continue
		}
		fields := strings.Fields(string(stat)[closing+1:])
		if len(fields) < 3 {
			continue
		}
		parent, err1 := strconv.Atoi(fields[1])
		group, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || parent != pid {
			continue
		}
		children = append(children, process{pid: id, parent: parent, group: group})
	}
	return children, nil
}
