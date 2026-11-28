package execution

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// systemProcessTable reads Linux's processes from /proc: each process's stat
// file for its parent, state and start time, its environ file for the
// environment it started with, and its cwd link for where it works.
type systemProcessTable struct{}

func (systemProcessTable) list() ([]processEntry, error) {
	names, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid())
	var entries []processEntry
	for _, name := range names {
		pid, err := strconv.Atoi(name.Name())
		if err != nil {
			continue
		}
		directory := filepath.Join("/proc", name.Name())
		info, err := os.Stat(directory)
		if err != nil {
			continue
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, "stat"))
		if err != nil {
			continue
		}
		// The command name is in parentheses and may hold spaces, so the fields
		// are counted from after its closing one: state, parent, group, ... and
		// the start time is the twentieth after it.
		closing := bytes.LastIndexByte(raw, ')')
		if closing < 0 {
			continue
		}
		fields := strings.Fields(string(raw[closing+1:]))
		if len(fields) < 20 {
			continue
		}
		parent, _ := strconv.Atoi(fields[1])
		entry := processEntry{PID: pid, Parent: parent, Start: fields[19], Gone: fields[0] == "Z" || fields[0] == "X"}
		if open := bytes.IndexByte(raw, '('); open >= 0 && open < closing {
			entry.Command = string(raw[open+1 : closing])
		}
		if environ, err := os.ReadFile(filepath.Join(directory, "environ")); err == nil {
			// Read, even if empty: nil is kept for an environment that could
			// not be read at all.
			entry.Environment = []string{}
			for _, field := range bytes.Split(environ, []byte{0}) {
				if len(field) > 0 {
					entry.Environment = append(entry.Environment, string(field))
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (systemProcessTable) workingDirectories(pids []int) map[int]string {
	directories := map[int]string{}
	for _, pid := range pids {
		if directory, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd")); err == nil {
			directories[pid] = directory
		}
	}
	return directories
}

// holding reads each process's open files from its fd directory, which names
// the file each descriptor has open.
func (systemProcessTable) holding(paths []string) map[int]bool {
	wanted := map[string]bool{}
	for _, path := range paths {
		wanted[filepath.Clean(path)] = true
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			wanted[resolved] = true
		}
	}
	holding := map[int]bool{}
	names, err := os.ReadDir("/proc")
	if err != nil {
		return holding
	}
	for _, name := range names {
		pid, err := strconv.Atoi(name.Name())
		if err != nil {
			continue
		}
		descriptors := filepath.Join("/proc", name.Name(), "fd")
		open, err := os.ReadDir(descriptors)
		if err != nil {
			continue
		}
		for _, descriptor := range open {
			if target, err := os.Readlink(filepath.Join(descriptors, descriptor.Name())); err == nil && wanted[target] {
				holding[pid] = true
				break
			}
		}
	}
	return holding
}

func (systemProcessTable) listening(pid int) []string {
	return lsofListening(pid)
}

func sendSignal(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}
