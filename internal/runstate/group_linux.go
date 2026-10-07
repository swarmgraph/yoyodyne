package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// exitingFlag is PF_EXITING in a task's flags: the task has begun exiting and
// runs nothing of its own again.
const exitingFlag = 0x4

// groupHasOnlyExited says whether every process in a process group has exited,
// and at least one was found. Linux counts a process that has exited and not
// yet been reaped as a member of its group, so a group whose leader has just
// exited answers a signal for as long as nobody has collected it — which, for
// a provider whose launcher died, is until the process it was handed to gets
// round to it. Such a process holds no files and runs nothing, so a group of
// them alone is a group that has stopped. One live process, or any process
// whose state cannot be read, answers false.
func groupHasOnlyExited(group int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	found := false
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		task, err := readTaskStat(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if task.group != group {
			continue
		}
		found = true
		exited, err := processHasExited(pid)
		if err != nil || !exited {
			return false, err
		}
	}
	return found, nil
}

// processHasExited says whether every thread of a process has exited. A
// process whose first thread has exited while another runs on is shown as
// exited at its own entry, so each thread is read.
func processHasExited(pid int) (bool, error) {
	tasks := filepath.Join("/proc", strconv.Itoa(pid), "task")
	entries, err := os.ReadDir(tasks)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		task, err := readTaskStat(filepath.Join(tasks, entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !task.exited() {
			return false, nil
		}
	}
	return true, nil
}

// taskStat is what this package reads of a task's stat file.
type taskStat struct {
	state string
	group int
	flags uint64
}

func (t taskStat) exited() bool {
	return t.state == "Z" || t.state == "X" || t.flags&exitingFlag != 0
}

// readTaskStat reads a task's state, process group, and flags. The command
// name is in parentheses and may itself contain spaces and parentheses, so the
// fields are counted from the last closing one.
func readTaskStat(path string) (taskStat, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return taskStat{}, err
	}
	text := string(encoded)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return taskStat{}, errors.New(path + " has no command name")
	}
	// state ppid pgrp session tty_nr tpgid flags ...
	fields := strings.Fields(text[end+1:])
	if len(fields) < 7 {
		return taskStat{}, errors.New(path + " is shorter than expected")
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return taskStat{}, err
	}
	flags, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil {
		return taskStat{}, err
	}
	return taskStat{state: fields[0], group: group, flags: flags}, nil
}
