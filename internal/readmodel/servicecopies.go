package readmodel

// How many copies of each part the supervisor hosts are running, and on which
// builds.
//
// Exactly one copy of each part should run, the one the supervisor's record
// names, on the build on disk. Every copy records itself as it starts, whoever
// started it, so those records read beside the supervisor's record are where a
// second copy shows — and where a copy on an old build shows beside the one on
// the current build. More than one copy is a problem whatever else the machine
// is doing: two Slack sinks post every message twice, and a copy on an old build
// reads a configuration its build cannot, so it is said on the attention line
// and in the factory problems, with whose move it is.
//
// A copy that has exited is not running, whatever the process table still
// holds of it, and a process that took a gone copy's number is not that copy;
// the records are read with both told apart (runstate.RecordedProcess).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ServiceCopies is every running copy of one part the supervisor hosts.
type ServiceCopies struct {
	// Service is the part, as the services section names it, or the
	// supervisor.
	Service string `json:"service"`
	// Deployed is the build of the binary on disk, as the supervisor last read
	// it, which a copy on any other build is behind.
	Deployed string        `json:"deployed,omitempty"`
	Copies   []ServiceCopy `json:"copies"`
}

// ServiceCopy is one running copy.
type ServiceCopy struct {
	PID       int       `json:"pid"`
	Build     string    `json:"build,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Supervised is the copy the supervisor's record names as the part: the
	// supervisor itself, or the process it records the child running as.
	Supervised bool `json:"supervised"`
	// Behind is a copy on a build other than the one on disk.
	Behind bool `json:"behind"`
}

// Count is how many copies are running.
func (c ServiceCopies) Count() int { return len(c.Copies) }

// Extra is every running copy the supervisor's record does not name.
func (c ServiceCopies) Extra() []ServiceCopy {
	var extra []ServiceCopy
	for _, running := range c.Copies {
		if !running.Supervised {
			extra = append(extra, running)
		}
	}
	return extra
}

// Problem is more than one copy running.
func (c ServiceCopies) Problem() bool { return len(c.Copies) > 1 }

// Says is the copies in a sentence: how many, and each with its process, build,
// and start, and which is the supervisor's.
func (c ServiceCopies) Says() string {
	described := make([]string, 0, len(c.Copies))
	for _, running := range c.Copies {
		described = append(described, running.describe())
	}
	part := "the " + c.Service + " service"
	if c.Service == runstate.ConfigReaderSupervisor {
		part = "the supervisor"
	}
	return fmt.Sprintf("%s has %d copies running, where one should run: %s", part, len(c.Copies), strings.Join(described, "; "))
}

func (c ServiceCopy) describe() string {
	build := "on a build that recorded no revision"
	if c.Build != "" {
		build = "on build " + shortRevision(c.Build)
		if c.Behind {
			build += ", an old build,"
		}
	}
	whose := "not the supervisor's, left running"
	if c.Supervised {
		whose = "the supervisor's"
	}
	return fmt.Sprintf("pid %d %s since %s (%s)", c.PID, build, localMoment(c.StartedAt), whose)
}

// readServiceCopies is the running copies of each part the supervisor's record
// says it hosts — every child that is a process it starts — and of the
// supervisor, from the records the copies wrote as they started.
func readServiceCopies(record runstate.Supervision, readers []runstate.ConfigReader) []ServiceCopies {
	supervised := map[string]int{runstate.ConfigReaderSupervisor: record.PID}
	for _, child := range record.Children {
		switch child.State {
		case runstate.ChildRunning, runstate.ChildDown, runstate.ChildDegraded:
			pid := 0
			if child.State == runstate.ChildRunning {
				pid = child.PID
			}
			supervised[string(child.Service)] = pid
		}
	}
	byService := map[string]*ServiceCopies{}
	for _, reader := range readers {
		pid, hosted := supervised[reader.Service]
		if !hosted {
			continue
		}
		copies, seen := byService[reader.Service]
		if !seen {
			copies = &ServiceCopies{Service: reader.Service, Deployed: record.Deployed, Copies: []ServiceCopy{}}
			byService[reader.Service] = copies
		}
		copies.Copies = append(copies.Copies, ServiceCopy{
			PID:        reader.PID,
			Build:      reader.Build,
			StartedAt:  reader.StartedAt,
			Supervised: pid > 0 && reader.PID == pid,
			Behind:     record.Deployed != "" && reader.Build != "" && reader.Build != record.Deployed,
		})
	}
	all := make([]ServiceCopies, 0, len(byService))
	for _, copies := range byService {
		sort.Slice(copies.Copies, func(i, j int) bool { return copies.Copies[i].StartedAt.Before(copies.Copies[j].StartedAt) })
		all = append(all, *copies)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Service < all[j].Service })
	return all
}

// serviceCopiesAttention is more than one copy of a part running, as the
// attention line and the factory problems carry it. It is the development
// manager's: the supervisor collects every copy it stopped and leaves a
// running one it did not start rather than stopping a process nobody can
// account for, so what is left is finding what started it and having that
// fixed, which is the reliability work she resolves.
func serviceCopiesAttention(copies ServiceCopies) Attention {
	return Attention{Kind: AttentionServiceCopies, ID: copies.Service, Mover: MoverDevelopmentManager, ServiceCopies: &copies}
}
