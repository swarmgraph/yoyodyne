package supervise

// Exactly one copy of each part, and a copy nobody can account for said aloud.
//
// Every long-running part records itself as it starts (runstate's
// ConfigReaderStore), one record per process. Those records are the
// one place every copy of a part shows, whoever started it, so the supervisor
// reads them on its look at builds and settles each copy of a part it hosts:
//
//   - the copy the supervisor runs — the process its account of the part names,
//     or the supervisor itself — is the part, and is left alone;
//   - a copy that has exited and waits to be collected is collected, where the
//     supervisor is its parent. That is every part the supervisor stopped
//     before it collected what it stops, since the launcher lets each child go
//     without waiting on it and the supervisor re-executes in place, keeping
//     its process and every uncollected child with it;
//   - a copy that is gone has its record removed, so the records are the parts
//     running rather than every start the product has made;
//   - a copy still running that the supervisor started before the one it runs
//     now, which a part that keeps a record of each start can name
//     (StopsEarlierCopies; the dashboard does), is stopped and the stop said.
//     A replacement stops these as soon as it is ready; this look stops any
//     that stop did not, whatever port or build it is on;
//   - a copy still running that is not the supervisor's is one it did not start
//     and cannot account for. It is said in the log once, with its process,
//     build, and start time, and left running: stopping a process nobody can
//     account for is a decision, and the read model reports it as one
//     (readmodel.ServiceCopies), with whose move it is.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CopyRecords is every process that recorded itself as a part of the product.
// It is satisfied by *runstate.ConfigReaderStore.
type CopyRecords interface {
	Processes() ([]runstate.RecordedProcess, error)
	Forget(runstate.RecordedProcess) error
}

// Identified is a child that can say which process holds its lease, which the
// supervisor asks of a child it took back running rather than started.
type Identified interface {
	HolderPID(ctx context.Context) int
}

// StopsEarlierCopies is a child that keeps a record of every start the
// supervisor made of it, and so can tell the copies the supervisor started
// before the one it runs now from copies nobody can account for, and stop them.
type StopsEarlierCopies interface {
	StopEarlierCopies(ctx context.Context) ([]EarlierCopy, error)
}

// EarlierCopy is one copy of a part the supervisor started before the one it
// runs now, and what stopping it came to.
type EarlierCopy struct {
	PID int
	// Said is the copy in words: its process, where it served, its build, and
	// the start it was.
	Said    string
	Stopped Stopped
	// Problem is why it could not be stopped, where it could not.
	Problem string
}

// sayEarlier logs each earlier copy a child stopped, or could not, which is the
// supervisor's record that it did.
func (s *Supervisor) sayEarlier(name config.ServiceName, copies []EarlierCopy) {
	for _, earlier := range copies {
		switch {
		case earlier.Problem != "":
			s.log("the %s service's earlier copy %s, which the supervisor started, could not be stopped and is tried again at the next look at builds: %s", name, earlier.Said, earlier.Problem)
		case earlier.Stopped.Detail != "":
			s.log("asked the %s service's earlier copy %s, which the supervisor started, to stop; %s", name, earlier.Said, earlier.Stopped.Detail)
		default:
			s.log("stopped the %s service's earlier copy %s, which the supervisor started and a newer copy has replaced", name, earlier.Said)
		}
	}
}

// settleCopies is one look over the recorded copies of every part the
// supervisor hosts, and of the supervisor.
func (s *Supervisor) settleCopies(ctx context.Context) {
	earlier := map[int]bool{}
	for _, child := range s.Children {
		stops, ok := child.(StopsEarlierCopies)
		if !ok {
			continue
		}
		copies, err := stops.StopEarlierCopies(ctx)
		if err != nil {
			s.sayOnce(fmt.Sprintf("which copies of the %s service the supervisor started could not all be read, so one it started may be left running until the next look: %v", child.Name(), err))
		}
		s.sayEarlier(child.Name(), copies)
		for _, said := range copies {
			earlier[said.PID] = true
		}
	}
	if s.Copies == nil {
		return
	}
	processes, err := s.Copies.Processes()
	if err != nil {
		s.sayOnce(fmt.Sprintf("the records of which copies of the product's parts are running could not all be read; the ones that could are settled: %v", err))
	}
	current := map[string]int{runstate.ConfigReaderSupervisor: s.PID}
	for _, child := range s.Children {
		pid := 0
		if state, known := s.states[child.Name()]; known && state.State == runstate.ChildRunning {
			pid = state.PID
		}
		current[string(child.Name())] = pid
	}
	if s.unaccounted == nil {
		s.unaccounted = map[string]bool{}
	}
	for _, process := range processes {
		reader := process.Reader
		running, hosted := current[reader.Service]
		if !hosted {
			// A part the supervisor does not start is not its to settle.
			continue
		}
		switch {
		case process.Exited():
			collected, err := s.collectCopy(reader.PID)
			if err != nil {
				s.log("the %s service's copy pid %d has exited and could not be collected: %v", reader.Service, reader.PID, err)
				continue
			}
			if !collected {
				// Another process is its parent and collects it; until then it runs
				// nothing, and every reading already counts it as gone.
				continue
			}
			s.log("collected the %s service's copy pid %d, %s, started %s, which had exited after it was stopped; nothing of it was running",
				reader.Service, reader.PID, describeCopyBuild(reader.Build), reader.StartedAt.Local().Format(time.RFC3339))
			process.Look = runstate.ProcessLook{}
		case process.Running() && reader.PID != running && running > 0 && !earlier[reader.PID]:
			if key := reader.InstanceID(); !s.unaccounted[key] {
				s.unaccounted[key] = true
				s.log("found a copy of the %s service the supervisor did not start and cannot account for: pid %d, %s, started %s; it is left running rather than stopped blindly, and `yoyo status` reports it",
					reader.Service, reader.PID, describeCopyBuild(reader.Build), reader.StartedAt.Local().Format(time.RFC3339))
			}
		}
		if err := s.Copies.Forget(process); err != nil {
			s.sayOnce(fmt.Sprintf("the records of copies of the product's parts that are gone could not be removed, so they are read again at the next look: %v", err))
		}
	}
}

// collectCopy collects an exited child, through the test's hand where it has
// one.
func (s *Supervisor) collectCopy(pid int) (bool, error) {
	if s.Collect != nil {
		return s.Collect(pid)
	}
	return collect(pid)
}

// collectDied collects a child the supervisor saw die, where it is the
// supervisor's own and has exited, so its process number is free for the next
// process rather than read as the part still running.
func (s *Supervisor) collectDied(pid int) {
	if pid <= 0 {
		return
	}
	look, err := lookProcess(pid)
	if err != nil || !look.Exists || !look.Exited {
		return
	}
	if _, err := s.collectCopy(pid); err != nil {
		s.log("pid %d, which died, could not be collected: %v", pid, err)
	}
}

func describeCopyBuild(build string) string {
	if build == "" {
		return "on a build that recorded no revision"
	}
	return "on build " + short(build)
}
