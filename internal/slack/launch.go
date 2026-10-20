package slack

// Starting a sink that outlives whatever started it.
//
// A maintenance pass is a short process: it runs, it acts, it exits. The sink
// it starts has to still be reporting an hour later, so it is started in a
// session of its own rather than as a child that shares the pass's terminal,
// process group, and lifetime. Without that, whatever stops the pass — a
// launchd job being unloaded, a terminal closing, a group signal — takes
// reporting down with it, hours after anybody was watching.
//
// The product's supervisor starts its other children the same way, through
// this launcher: the scheduler, and the supervisor itself when `yoyo start`
// detaches it. Each is a process that has to survive whatever started it, and
// one launcher is one set of rules about how.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// What the sink's log and the directories above it are created as. A log of
// what the harness is doing is state rather than a checked-in document, so it
// is the owner's to read and nobody else's.
const (
	logPermissions   fs.FileMode = 0o600
	logDirectoryMode fs.FileMode = 0o700
)

// DetachedLauncher starts the process described as its own session and returns
// without waiting for it.
type DetachedLauncher struct{}

func (DetachedLauncher) Launch(spec Launch) (int, error) {
	if strings.TrimSpace(spec.Program) == "" {
		return 0, errors.New("starting a process needs the binary to start")
	}
	if strings.TrimSpace(spec.Log) == "" {
		return 0, errors.New("starting a process needs somewhere for it to say what it is doing")
	}
	// The log is written through the confined writer rather than by name. A
	// path string proves nothing about where bytes land: one symlink along it
	// and the sink's whole output — every line of what the harness is doing — is
	// appended to a file outside the root this was told to stay in. It is
	// opened once here so that a log that cannot be kept inside the root
	// refuses the launch, rather than a sink starting with nowhere to write.
	root, err := repowrite.NewRoot(spec.LogRoot)
	if err != nil {
		return 0, fmt.Errorf("the process log has to stay inside %s: %w", spec.LogRoot, err)
	}
	// Appended to rather than replaced: the log of the sink that stopped is how
	// anybody finds out why it stopped, and a pass that starts a new one every
	// few minutes would otherwise erase that before it was read.
	check, err := root.OpenAppender(spec.Log, logPermissions, logDirectoryMode)
	if err != nil {
		return 0, fmt.Errorf("open the process log: %w", err)
	}
	if err := check.Close(); err != nil {
		return 0, fmt.Errorf("open the process log: %w", err)
	}

	// No descriptor the confined writer opened is handed to another process,
	// because nothing could check what that process writes through it. The
	// process started is the log relay (logrelay.go): it starts the sink with
	// its output on a pipe, and appends what it reads to the log in checked
	// batches for as long as the sink runs. It is this executable, which every
	// binary built from this package is.
	relay, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("find the log relay: %w", err)
	}
	config, err := json.Marshal(relayConfig{Program: spec.Program, Args: spec.Args, Dir: spec.Dir, LogRoot: spec.LogRoot, Log: spec.Log})
	if err != nil {
		return 0, fmt.Errorf("describe the process to the log relay: %w", err)
	}
	command := exec.Command(relay)
	command.Dir = spec.Dir
	// The environment the caller constructed, and nothing this process happens
	// to be holding: the tokens in it are for this product's sink alone. The
	// Git maintenance fence goes on top of it, because the sink outlives what
	// started it and everything it spawns inherits this environment — a sink
	// exempt from the fence is a long-lived source of exactly the prune the
	// fence exists to stop. The relay passes it on to the sink without the
	// variable that makes it a relay.
	command.Env = append(execution.WithGitMaintenanceFence(spec.Env), relayVariable+"=1")
	command.Stdin = bytes.NewReader(config)
	// The relay says on this pipe which process the sink is, once it has
	// started it, or why it could not.
	started, report, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("start %s: %w", spec.Program, err)
	}
	defer started.Close()
	command.ExtraFiles = []*os.File{report}
	detachProcess(command)
	if err := command.Start(); err != nil {
		report.Close()
		return 0, fmt.Errorf("start %s: %w", spec.Program, err)
	}
	report.Close()
	sink, err := readStartedSink(started)
	if err != nil {
		_ = command.Process.Release()
		return 0, fmt.Errorf("start %s: %w", spec.Program, err)
	}
	// The process reported is the sink itself, which is what whoever started it
	// signals and waits to see recorded. The relay ends when the sink does.
	// Released rather than waited for: nothing here is going to reap it, and a
	// process this one never waits on is one the operating system reparents when
	// this one exits.
	if err := command.Process.Release(); err != nil {
		return sink, fmt.Errorf("release the log relay: %w", err)
	}
	return sink, nil
}

// readStartedSink is the relay's one line about the sink: its process, or why
// it could not be started. A relay that ends without a line has failed before
// it started anything.
func readStartedSink(started io.Reader) (int, error) {
	line, err := bufio.NewReader(started).ReadString('\n')
	line = strings.TrimSpace(line)
	if pid, convErr := strconv.Atoi(line); convErr == nil && pid > 0 {
		return pid, nil
	}
	if line != "" {
		return 0, errors.New(line)
	}
	return 0, fmt.Errorf("the log relay ended without starting it: %v", err)
}
