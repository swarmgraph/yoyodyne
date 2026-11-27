package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// leftRuns is a reading over runs that names the runs left running processes.
type leftRuns struct {
	Runs
	left []runstate.State
}

func (r leftRuns) LeftRunning() ([]runstate.State, error) { return r.left, nil }

// A process a run could not stop is shown on status under its run, with its
// number and what it listens on, whether or not the run has ended.
func TestStatusNamesAProcessARunLeftRunning(t *testing.T) {
	process := execution.LeftProcess{PID: 4242, Command: "java", Listening: []string{"127.0.0.1:25565"}, Why: "the harness could not stop it: permission refused"}
	sources := Sources{Runs: leftRuns{left: []runstate.State{{RunID: "run-1", WorkItemID: "item-1", Status: runstate.StatusSucceeded, LeftProcesses: []execution.LeftProcess{process}}}}}
	left, problem := readLeftProcesses(sources)
	if problem != "" || len(left) != 1 {
		t.Fatalf("readLeftProcesses() = %+v, %q", left, problem)
	}
	rendered := Standing{LeftProcesses: left}.renderLeftProcesses()
	for _, want := range []string{
		"Left running by runs (1 process):",
		"item-1, run run-1: a process this run started is still running: process 4242, java, listening on 127.0.0.1:25565; the harness could not stop it: permission refused",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("status does not say %q:\n%s", want, rendered)
		}
	}
	if (Standing{}).renderLeftProcesses() != "" {
		t.Fatal("status says something about left processes where there are none")
	}
}
