package execution

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// A process of the run that something still alive is connected to is left
// running (docs/designs/stopping-what-a-run-started.md, decision 6): a build
// server or other helper the run started, which another run, a person, or the
// harness itself has connected to since. Stopping it would break work that is
// not this run's.
//
// Before every signal the stop reads each target's live local connections —
// TCP between loopback addresses, and connected socket files — and the process
// at each other end. A target is in use, and left running, when a connection
// leads to a process the same stop leaves alone (another run's while that run
// has something going, a person's, or the harness's own), or to an end the
// harness cannot find or read: the second leans the same way as decision 2,
// because stopping the wrong process is the worse mistake. A port listened on
// with nothing connected is not use, and nor is a connection to another
// process the same stop is stopping.

// localConnection is one live local connection a process has open: over is
// what it is made over, as said to a person ("127.0.0.1:5000", or "a socket
// file"), and peers the processes holding the other end. No peers is an end
// the harness could not find or read.
type localConnection struct {
	Over  string
	Peers []int
}

// inUseCheck is what one stop knows to tell use: its own run, and which other
// runs have nothing going (RunProcessesRequest.Idle).
type inUseCheck struct {
	runID string
	idle  func(runID string) bool
}

// leaveInUse splits the targets into those still to be signalled and those in
// use, which are left running and named with what is connected to them. A
// target left running is then one the stop leaves alone, so a target connected
// only to it is in use as well. Connections that cannot be read at all leave
// every target running.
func (c inUseCheck) leaveInUse(table processTable, targets []processEntry) ([]processEntry, []LeftProcess) {
	if len(targets) == 0 {
		return targets, nil
	}
	connections, err := table.connections()
	if err != nil {
		used := make([]LeftProcess, 0, len(targets))
		for _, target := range targets {
			used = append(used, LeftProcess{PID: target.PID, Command: target.Command, InUse: true,
				Why: "left running because the harness could not read its local connections: " + err.Error()})
		}
		return nil, used
	}
	entries, err := table.list()
	if err != nil {
		entries = nil
	}
	byPID := make(map[int]processEntry, len(entries))
	for _, entry := range entries {
		byPID[entry.PID] = entry
	}
	lineage := ownLineage(entries)
	stopping := make(map[int]bool, len(targets))
	for _, target := range targets {
		stopping[target.PID] = true
	}
	why := map[int]string{}
	for changed := true; changed; {
		changed = false
		for _, target := range targets {
			if !stopping[target.PID] {
				continue
			}
			if reason := c.usedBy(connections[target.PID], target.PID, stopping, byPID, lineage); reason != "" {
				stopping[target.PID] = false
				why[target.PID] = reason
				changed = true
			}
		}
	}
	kept := targets[:0:0]
	var used []LeftProcess
	for _, target := range targets {
		if stopping[target.PID] {
			kept = append(kept, target)
			continue
		}
		used = append(used, LeftProcess{PID: target.PID, Command: target.Command, InUse: true, Why: why[target.PID]})
	}
	return kept, used
}

// usedBy says why the process is in use, or nothing where none of its
// connections leads to a process the stop leaves alone.
func (c inUseCheck) usedBy(connections []localConnection, self int, stopping map[int]bool, byPID map[int]processEntry, lineage map[int]bool) string {
	var reasons []string
	for _, connection := range connections {
		if len(connection.Peers) == 0 {
			reasons = append(reasons, fmt.Sprintf("the other end of its connection over %s could not be read", connection.Over))
			continue
		}
		for _, peer := range connection.Peers {
			if peer == self || stopping[peer] {
				continue
			}
			if said, protects := c.peer(peer, byPID, lineage); protects {
				reasons = append(reasons, fmt.Sprintf("%s is connected to it over %s", said, connection.Over))
			}
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	sort.Strings(reasons)
	reasons = slices.Compact(reasons)
	return "left running because " + strings.Join(reasons, ", and ")
}

// peer names the process at the other end of a connection, and says whether
// the stop leaves it alone.
func (c inUseCheck) peer(pid int, byPID map[int]processEntry, lineage map[int]bool) (string, bool) {
	entry, found := byPID[pid]
	switch {
	case lineage[pid] && found:
		return fmt.Sprintf("process %d (%s, the harness itself)", pid, entry.Command), true
	case lineage[pid]:
		return fmt.Sprintf("process %d (the harness itself)", pid), true
	case !found:
		return fmt.Sprintf("process %d, which the harness cannot look at,", pid), true
	}
	id, carries := runIdentityIn(entry.Environment)
	switch {
	case !carries:
		return fmt.Sprintf("process %d (%s, which carries no run's identifier)", pid, entry.Command), true
	case id != c.runID && c.idle != nil && c.idle(id):
		return "", false
	default:
		return fmt.Sprintf("process %d (%s, run %s)", pid, entry.Command, id), true
	}
}
