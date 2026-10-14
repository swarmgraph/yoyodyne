package gitworktree

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// freeMemory reads MemAvailable from /proc/meminfo, in bytes: the kernel's
// estimate of what a new process can have without the machine swapping.
func freeMemory() (uint64, bool) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "MemAvailable:" {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kilobytes * 1024, true
	}
	return 0, false
}
