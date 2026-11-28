package execution

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// systemProcessTable reads macOS's processes from the kernel: the process list
// from kern.proc.all, and each process's starting arguments and environment
// from its kern.procargs2 record.
type systemProcessTable struct{}

func (systemProcessTable) list() ([]processEntry, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid())
	entries := make([]processEntry, 0, len(procs))
	for _, proc := range procs {
		if proc.Eproc.Ucred.Uid != uid {
			continue
		}
		pid := int(proc.Proc.P_pid)
		entry := processEntry{
			PID:    pid,
			Parent: int(proc.Eproc.Ppid),
			Start:  fmt.Sprintf("%d.%06d", proc.Proc.P_starttime.Sec, proc.Proc.P_starttime.Usec),
			Gone:   proc.Proc.P_stat == darwinZombie,
		}
		entry.Command, entry.Environment = darwinStartingArguments(pid)
		if entry.Command == "" {
			entry.Command = unix.ByteSliceToString(proc.Proc.P_comm[:])
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// darwinZombie is the kernel's SZOMB: a process that has exited and waits to be
// reaped.
const darwinZombie = 5

// darwinStartingArguments reads a process's kern.procargs2 record: the number
// of arguments, the executable's path, the arguments, and then the environment
// it started with. A record that cannot be read gives no environment, which
// makes the process nobody's run's.
func darwinStartingArguments(pid int) (string, []string) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return "", nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return "", nil
	}
	command := filepath.Base(string(rest[:end]))
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	fields := bytes.Split(rest, []byte{0})
	if len(fields) < argc {
		return command, nil
	}
	var environment []string
	for _, field := range fields[argc:] {
		if len(field) == 0 {
			break
		}
		environment = append(environment, string(field))
	}
	return command, environment
}

func (systemProcessTable) workingDirectories(pids []int) map[int]string {
	return lsofWorkingDirectories(pids)
}

func (systemProcessTable) holding(paths []string) map[int]bool {
	return lsofHolding(paths)
}

func (systemProcessTable) listening(pid int) []string {
	return lsofListening(pid)
}

func sendSignal(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}
