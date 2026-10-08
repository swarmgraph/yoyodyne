//go:build linux

package runstate

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is the unit /proc gives a process's start time in, since boot. It
// is USER_HZ, which Linux fixes at 100 on every architecture it reports to
// user space.
const clockTicks = 100

// describeProcess reads /proc/<pid>/stat: the state letter after the command's
// closing parenthesis, Z for a process exited and not collected, and the start
// time twenty fields on, which the boot time in /proc/stat turns into a moment.
func describeProcess(pid int) (exited bool, started time.Time, known bool) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false, time.Time{}, false
	}
	closing := bytes.LastIndexByte(stat, ')')
	if closing < 0 {
		return false, time.Time{}, false
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) < 20 {
		return false, time.Time{}, false
	}
	exited = fields[0] == "Z"
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return exited, time.Time{}, true
	}
	if boot, ok := bootTime(); ok {
		started = boot.Add(time.Duration(ticks) * time.Second / clockTicks)
	}
	return exited, started, true
}

func bootTime() (time.Time, bool) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(stat), "\n") {
		if value, found := strings.CutPrefix(line, "btime "); found {
			seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(seconds, 0), true
		}
	}
	return time.Time{}, false
}
