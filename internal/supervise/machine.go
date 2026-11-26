package supervise

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReadPowerHistory reads the OS's own account, including transitions while
// the supervisor was down. It does not infer sleep from elapsed wall time.
func ReadPowerHistory(ctx context.Context, runner execution.ProcessRunner) ([]runstate.PowerEvent, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("OS sleep history is unavailable on %s", runtime.GOOS)
	}
	var events []runstate.PowerEvent
	var parseErr error
	result, err := runner.Run(ctx, execution.Command{Name: "/usr/bin/pmset", Args: []string{"-g", "log"}, Timeout: time.Minute}, func(output execution.Output) {
		if output.Stream != execution.StreamStdout {
			return
		}
		event, found, err := parsePowerEvent(output.Text)
		if err != nil {
			parseErr = err
		}
		if found {
			events = append(events, event)
		}
	})
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("read OS sleep history: %s: %s", result.Status, result.Stderr)
	}
	return events, parseErr
}

func parsePowerEvent(line string) (runstate.PowerEvent, bool, error) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return runstate.PowerEvent{}, false, nil
	}
	// Match event kinds and their account, never Wake Requests or acknowledgments.
	awake := fields[3] == "Wake" || fields[3] == "DarkWake"
	account := strings.Join(fields[4:], " ")
	sleep := fields[3] == "Sleep" && strings.HasPrefix(account, "Entering Sleep state")
	wake := awake && (strings.HasPrefix(account, fields[3]+" from") || (fields[3] == "Wake" && strings.HasPrefix(account, "DarkWake to FullWake")))
	if !sleep && !wake {
		return runstate.PowerEvent{}, false, nil
	}
	at, err := time.Parse("2006-01-02 15:04:05 -0700", fields[0]+" "+fields[1]+" "+fields[2])
	if err != nil {
		return runstate.PowerEvent{}, false, fmt.Errorf("read OS power timestamp: %w", err)
	}
	return runstate.PowerEvent{At: at, Awake: awake, Source: "pmset: " + fields[3]}, true, nil
}

type machineRecords interface {
	RecordMachine(context.Context, runstate.MachineObservation) error
	MachineHistory() ([]runstate.MachineObservation, error)
}

func (s *Supervisor) observeMachine(ctx context.Context, now time.Time) {
	store, ok := s.Records.(machineRecords)
	if !ok || s.PowerHistory == nil || (!s.lastMachineLook.IsZero() && now.Before(s.lastMachineLook.Add(runstate.MachineObservationInterval))) {
		return
	}
	if s.lastMachineLook.IsZero() {
		s.seenPower = make(map[string]bool)
		history, err := store.MachineHistory()
		if err != nil {
			s.log("machine history could not be read: %v", err)
		}
		for _, observation := range history {
			for _, event := range observation.Power {
				s.seenPower[powerKey(event)] = true
			}
		}
	}
	observation := runstate.MachineObservation{At: now}
	for _, child := range s.Children {
		if child.Name() == config.ServiceScheduler {
			running, err := child.Running(ctx)
			if err != nil {
				s.log("scheduler presence could not be recorded: %v", err)
				return
			}
			observation.Watching = running
		}
	}
	events, err := s.PowerHistory(ctx)
	if err != nil {
		observation.PowerProblem = err.Error()
	}
	s.noteWake(events, now)
	for _, event := range events {
		if !s.seenPower[powerKey(event)] {
			observation.Power = append(observation.Power, event)
		}
	}
	if err := store.RecordMachine(ctx, observation); err != nil {
		s.log("machine observation could not be recorded: %v", err)
		return
	}
	s.lastMachineLook = now
	for _, event := range observation.Power {
		s.seenPower[powerKey(event)] = true
	}
}

// noteWake keeps the latest wake in the operating system's account that is not
// later than now. The account includes what happened while the supervisor was
// down, so a supervisor started afresh knows the same wake as the one before.
func (s *Supervisor) noteWake(events []runstate.PowerEvent, now time.Time) {
	for _, event := range events {
		if event.Awake && !event.At.After(now) && event.At.After(s.awakeSince) {
			s.awakeSince = event.At
		}
	}
}

func powerKey(event runstate.PowerEvent) string {
	return event.At.Format(time.RFC3339Nano) + "/" + event.Source
}
