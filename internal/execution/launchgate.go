package execution

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// A launch held behind a gate until whoever asked for it has written down which
// process it is.
//
// A provider process started the ordinary way begins its work the moment it is
// spawned, so a record of it can only be written afterwards, and a harness that
// dies in between leaves a provider working on a run whose record says nothing
// was started. Recovery reading that record would start a second one beside it.
// The gate closes that gap: the process is started as a small shell that waits
// on a pipe, Register is called with its identity, and only once Register has
// returned is the pipe written and the shell replaced by the provider. A
// harness that dies before then closes the pipe by dying, the shell reads the
// end of it and exits without running anything, and so a record with no
// registered execution is proof that no provider work began.
//
// The process is also handed Hold, a file the caller has locked, and everything
// the provider starts inherits it. The lock is released only when the last
// process holding that file has exited, which is what lets a later reader tell
// whether any part of the tree is still alive without trusting a process
// number that may since have been given to something else
// (runstate's launch.go reads it).

// LaunchGate starts a command behind a gate. See the comment above.
type LaunchGate struct {
	// Hold is handed to the process as an inherited descriptor and passes to
	// everything it starts. Run closes this process's copy once the process has
	// started, so what holds the file afterwards is the process tree alone, and
	// closes it as well on every return that started nothing, so a launch that
	// never happened leaves nothing holding the file.
	Hold *os.File
	// Register is called once the process exists and before it may do any
	// work. Returning an error ends the process before the gate opens, so a
	// launch that could not be written down never ran.
	Register func(StartedProcess) error
}

// StartedProcess is the process a gated launch started: its identifier, the
// process group it leads, and when it was started.
type StartedProcess struct {
	PID          int
	ProcessGroup int
	StartedAt    time.Time
}

// ErrGateUnsupported is a gated launch asked for on a platform with no way to
// hold a process back or to tell later whether it has stopped.
var ErrGateUnsupported = errors.New("starting a provider behind a launch gate is unsupported on this platform")

// gateScript waits for the word go on descriptor 3 and only then replaces
// itself with the command. Anything else — the end of the pipe because the
// harness died, or a refusal — exits with gateRefusedExit and runs nothing.
// Descriptor 3 is closed for the command; descriptor 4, the hold, is not.
const gateScript = `IFS= read -r gate <&3 || exit 97; [ "$gate" = go ] || exit 97; exec 3<&-; exec "$@"`

// gateRefusedExit is the exit status of a gate that never opened.
const gateRefusedExit = 97

// gateShell is the shell the gate runs in.
const gateShell = "/bin/sh"

// gatedProcess is the command rewritten to start behind a gate, and the pipe
// end that opens it.
type gatedProcess struct {
	open *os.File
}

// prepareGate rewrites process to start behind the gate. The command name is
// resolved here, as exec.Command would have, so a name found on this process's
// PATH runs the same program behind the gate.
func prepareGate(process *exec.Cmd, name string, args []string, gate *LaunchGate) (*gatedProcess, error) {
	if !gateSupported {
		return nil, ErrGateUnsupported
	}
	if gate.Hold == nil || gate.Register == nil {
		return nil, errors.New("a launch gate names the file its process holds and how its launch is registered")
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	shell, err := exec.LookPath(gateShell)
	if err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create launch gate: %w", err)
	}
	process.Path = shell
	process.Args = append([]string{shell, "-c", gateScript, "yoyodyne-launch", resolved}, args...)
	process.Err = nil
	process.ExtraFiles = []*os.File{read, gate.Hold}
	return &gatedProcess{open: write}, nil
}

// started is called once the gated process exists: this process's copies of
// the gate's read end and of the hold are closed, so the tree alone holds them.
func (g *gatedProcess) started(process *exec.Cmd) {
	for _, file := range process.ExtraFiles {
		_ = file.Close()
	}
}

// release opens the gate.
func (g *gatedProcess) release() {
	_, _ = g.open.Write([]byte("go\n"))
	_ = g.open.Close()
}

// refuse leaves the gate shut for good: the shell reads the end of the pipe
// and exits without running anything.
func (g *gatedProcess) refuse() {
	_ = g.open.Close()
}

// abandon closes everything a gate set up for a process that never started.
func (g *gatedProcess) abandon(process *exec.Cmd) {
	g.started(process)
	g.refuse()
}
