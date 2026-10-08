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
//   - a copy still running that is not the supervisor's is one it did not start
//     and cannot account for. It is said in the log once, with its process,
//     build, and start time, and left running: stopping a process nobody can
//     account for is a decision, and the read model reports it as one
//     (readmodel.ServiceCopies), with whose move it is.

import (
	"context"
	"fmt"
	"time"

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

// settleCopies is one look over the recorded copies of every part the
// supervisor hosts, and of the supervisor.
func (s *Supervisor) settleCopies() {
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
			// A part the supervisor does not start — the dashboard, until it is
			// adopted — may have several copies by design, and is not its to settle.
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
		case process.Running() && reader.PID != running && running > 0:
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
