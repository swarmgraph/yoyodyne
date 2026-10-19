package supervise

// What the supervisor's periodic pass may ask of the supervisor.
//
// The pass (internal/maintain) is a resident: looked at on the supervisor's own
// poll, on the same goroutine as the looks at the children, so everything here
// reads and changes the supervisor's account of its children without a lock.
// It is narrow on purpose. The pass may read where a part stands, restart a
// part a program manager asked to have restarted — under the same bound and
// backoff a death is counted against — and ask the supervisor to take a
// deployed build up by re-executing into it. It may not start, stop, or kill
// anything any other way, and the scheduler is never stopped from here at all:
// stopping it cancels the runs it hosts, and it takes a deployed build up
// itself, between them.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Scheduled is a part of the product that is a pass the supervisor takes
// itself rather than a process it starts. It is recorded under its service
// name with what Describe says.
type Scheduled interface {
	Service() config.ServiceName
	Describe() string
}

// TakeUpError is Run returning because a pass asked the supervisor to take up
// a deployed build: the lease has been let go, the children are running, and
// what remains is for the caller to re-execute into the binary.
type TakeUpError struct {
	Into string
}

func (e *TakeUpError) Error() string {
	return fmt.Sprintf("the supervisor stopped to take up the deployed build %s", short(e.Into))
}

func (s *Supervisor) restartHold() string {
	if s.RestartHold == nil {
		return ""
	}
	return s.RestartHold()
}

// ChildState is what the supervisor last knew about a part it starts, and
// false for a part it does not.
func (s *Supervisor) ChildState(name config.ServiceName) (runstate.SupervisedChild, bool) {
	if state, known := s.states[name]; known {
		return *state, true
	}
	return runstate.SupervisedChild{}, false
}

// Hosted reports what the supervisor does with a part: a child it starts, a
// pass it takes, a part enabled and not yet adopted, or a part that is off.
func (s *Supervisor) Hosted(name config.ServiceName) string {
	for _, child := range s.Children {
		if child.Name() == name {
			return "a process the supervisor starts"
		}
	}
	if _, scheduled := s.scheduled(name); scheduled {
		return "a pass the supervisor takes itself rather than a process"
	}
	if reason, notYet := s.notYet(name); notYet {
		return "enabled and not yet a child of the supervisor: " + reason
	}
	return "not enabled in the services section"
}

// RunningBuild is the revision the supervisor itself is running.
func (s *Supervisor) RunningBuild() string { return s.Build }

// Deployed is the revision of the binary on disk as the last tick read it.
func (s *Supervisor) Deployed() string { return s.deployed }

// Moving names a child the supervisor is in the middle of moving onto a
// deployed build or starting again, and is empty when every part is settled.
// Taking a build up in the middle of either would leave the next supervisor to
// finish a move it did not begin.
func (s *Supervisor) Moving() string {
	for _, child := range s.Children {
		state, known := s.states[child.Name()]
		if !known {
			return string(child.Name())
		}
		if state.RestartingInto != "" || state.SettingsRestarting || state.State == runstate.ChildDown {
			return string(child.Name())
		}
	}
	return ""
}

// TakeUp asks the supervisor to re-execute into the deployed build once the
// tick it was asked in is over. It is refused while a restart is held, and
// where the build on disk is the one running or cannot be read.
func (s *Supervisor) TakeUp(into string) error {
	if held := s.restartHold(); held != "" {
		return fmt.Errorf("not taken up while %s", held)
	}
	if into == "" || into == s.Build {
		return fmt.Errorf("the supervisor is already running build %s", short(s.Build))
	}
	s.takeUp = into
	return nil
}

// RestartOnRequest restarts a part a program manager asked to have restarted,
// and says what it did, which is the request's answer. A requested restart is
// treated as a death: the part is stopped, counted against the restart bound,
// and started again after the backoff its failures earn, so a stream of
// requests leaves a part degraded rather than bouncing it without end. A part
// that is down or degraded is left as it stands, the scheduler is never
// stopped, and nothing is restarted while a restart is held.
func (s *Supervisor) RestartOnRequest(ctx context.Context, name config.ServiceName, now time.Time) string {
	var child Child
	for _, candidate := range s.Children {
		if candidate.Name() == name {
			child = candidate
		}
	}
	if child == nil {
		return fmt.Sprintf("not restarted: the %s is %s", name, s.Hosted(name))
	}
	if held := s.restartHold(); held != "" {
		return fmt.Sprintf("not restarted: nothing is restarted while %s", held)
	}
	state, known := s.states[name]
	if !known {
		return fmt.Sprintf("not restarted: the supervisor has not looked at the %s yet; ask again once it has", name)
	}
	switch state.State {
	case runstate.ChildDegraded:
		return fmt.Sprintf("not restarted: the %s is degraded and left down (%s); a request does not lift the restart bound, and `yoyo stop` then `yoyo start` is what does", name, state.Reason)
	case runstate.ChildDown:
		return fmt.Sprintf("not restarted: the %s is already down and being started again (%s)", name, state.Reason)
	}
	if _, self := child.(RestartsItself); self {
		return fmt.Sprintf("not restarted: the %s is left running, because stopping it cancels the runs it hosts; it takes a deployed build up itself between runs, and `yoyo stop` is the only thing that stops it", name)
	}
	if state.RestartingInto != "" {
		return fmt.Sprintf("not restarted: the %s is already being restarted into the deployed build %s", name, short(state.RestartingInto))
	}
	s.log("restarting the %s service at a program manager's request", name)
	stopped, err := child.Stop(ctx)
	if err != nil {
		return fmt.Sprintf("not restarted: the %s could not be stopped: %v", name, err)
	}
	if !stopped.WasRunning {
		return fmt.Sprintf("not restarted: the %s had already stopped; the supervisor's next look starts it", name)
	}
	if stopped.Detail != "" {
		// Still holding its lease: the next look finds it gone and counts the
		// death then.
		return fmt.Sprintf("asked pid %d to stop; %s, and the supervisor starts it again once it lets go of its lease, counted against its restart bound", stopped.PID, stopped.Detail)
	}
	s.died(child, state, now)
	if state.State == runstate.ChildDown {
		state.Reason = fmt.Sprintf("stopped at %s at a program manager's request; starting again at %s (restart %d of %d before it is left down)",
			now.UTC().Format(time.RFC3339), state.NextStartAt.UTC().Format(time.RFC3339), state.Failures, MaxRapidFailures)
	}
	if state.State == runstate.ChildDegraded {
		return fmt.Sprintf("stopped pid %d, and it is now degraded rather than started again: %s", stopped.PID, state.Reason)
	}
	return fmt.Sprintf("stopped pid %d; the supervisor starts it again at %s, counted as restart %d of %d before it is left down",
		stopped.PID, state.NextStartAt.UTC().Format(time.RFC3339), state.Failures, MaxRapidFailures)
}
