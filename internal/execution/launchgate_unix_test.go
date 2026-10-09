//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The process a gated launch starts can do nothing until its registration has
// returned, and it inherits the hold, which nothing but its tree holds once it
// has started.
func TestAGatedLaunchBeginsWorkOnlyAfterItIsRegistered(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	began := filepath.Join(scratch, "began")
	hold := lockedHold(t, scratch)
	var registered StartedProcess
	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", fmt.Sprintf(`touch %q; echo working`, began)},
		Timeout: 10 * time.Second,
		Gate: &LaunchGate{Hold: hold, Register: func(process StartedProcess) error {
			registered = process
			// Long enough that an ungated process would have begun.
			time.Sleep(200 * time.Millisecond)
			if _, err := os.Stat(began); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("the process began before it was registered: %v", err)
			}
			again, err := os.OpenFile(hold.Name(), os.O_RDWR, 0)
			if err != nil {
				return err
			}
			defer again.Close()
			if err := syscall.Flock(int(again.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
				return errors.New("the started process does not hold the hold")
			}
			return nil
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded || strings.TrimSpace(result.Stdout) != "working" {
		t.Fatalf("Run() = %+v", result)
	}
	if registered.PID <= 0 || registered.ProcessGroup != registered.PID || registered.StartedAt.IsZero() {
		t.Fatalf("registered = %+v", registered)
	}
	if _, err := os.Stat(began); err != nil {
		t.Fatalf("the process never began: %v", err)
	}
	if !holdFreed(t, hold.Name()) {
		t.Fatal("the hold is still held after the tree exited")
	}
}

// A launch whose registration fails never runs the command, and says so as a
// process that was never started.
func TestAGatedLaunchThatCannotBeRegisteredRunsNothing(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	began := filepath.Join(scratch, "began")
	refusal := errors.New("the record could not be written")
	_, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", fmt.Sprintf(`touch %q`, began)}, Timeout: 10 * time.Second,
		Gate: &LaunchGate{Hold: lockedHold(t, scratch), Register: func(StartedProcess) error { return refusal }},
	}, nil)
	if !errors.Is(err, ErrProcessNotStarted) || !errors.Is(err, refusal) {
		t.Fatalf("Run() error = %v, want a process never started for the refusal", err)
	}
	if _, err := os.Stat(began); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused process ran: %v", err)
	}
}

// A gate whose opener goes away without opening it — the harness dying —
// leaves the waiting shell to exit without running the command.
func TestAGateThatNeverOpensRunsNothing(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	began := filepath.Join(scratch, "began")
	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", fmt.Sprintf(`touch %q`, began)}, Timeout: 10 * time.Second,
		Gate: &LaunchGate{Hold: lockedHold(t, scratch), Register: func(StartedProcess) error { return nil }},
	}, nil)
	if err != nil || result.Status != ProcessSucceeded {
		t.Fatalf("the opened gate: Run() = %+v, %v", result, err)
	}
	// The shell on its own, with the gate's write end closed unwritten.
	_ = os.Remove(began)
	process, write := startShutGate(t, began)
	_ = write.Close()
	state, err := process.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if state.ExitCode() != gateRefusedExit {
		t.Fatalf("a gate closed unopened exited %d, want %d", state.ExitCode(), gateRefusedExit)
	}
	if _, err := os.Stat(began); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a gate that never opened ran the command: %v", err)
	}
}

// A gated launch that returns before starting anything — a context already
// ended, a program that cannot be found — lets go of the hold it was handed,
// so the hold does not read as a process still running when none started.
//
// The test keeps its own reference to the file it hands over, so a copy the
// runner forgot to close stays open for the whole test rather than until the
// garbage collector happens by. What it waits out is the other way a hold
// reads taken after it was closed: a process another test forks at that moment
// carries a copy of every open descriptor until it starts its own program.
func TestAGatedLaunchThatStartsNothingLetsGoOfTheHold(t *testing.T) {
	t.Parallel()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for _, launch := range []struct {
		name string
		ctx  context.Context
		run  string
	}{
		{"a context already ended", ended, "sh"},
		{"a program that cannot be found", context.Background(), "yoyodyne-no-such-program"},
	} {
		handed := lockedHold(t, t.TempDir())
		_, _ = OSProcessRunner{}.Run(launch.ctx, Command{
			Name: launch.run, Args: []string{"-c", "true"}, Timeout: 10 * time.Second,
			Gate: &LaunchGate{Hold: handed, Register: func(StartedProcess) error { return nil }},
		}, nil)
		if !holdFreed(t, handed.Name()) {
			t.Errorf("%s: the hold is still taken after a launch that started nothing", launch.name)
		}
	}
}

// forkedCopyWindow is how long the test allows a process forked elsewhere in
// this test binary to go on holding a copy of a descriptor this one closed,
// before that process replaces itself with the program it was starting.
const forkedCopyWindow = 5 * time.Second

// holdFreed reports whether the hold can be taken within forkedCopyWindow.
func holdFreed(t *testing.T, path string) bool {
	t.Helper()
	again, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	for deadline := time.Now().Add(forkedCopyWindow); ; time.Sleep(20 * time.Millisecond) {
		if syscall.Flock(int(again.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

func lockedHold(t *testing.T, dir string) *os.File {
	t.Helper()
	hold, err := os.OpenFile(filepath.Join(dir, "hold"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(hold.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Close() })
	return hold
}

func startShutGate(t *testing.T, began string) (*os.Process, *os.File) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	process, err := os.StartProcess(gateShell, []string{gateShell, "-c", gateScript, "yoyodyne-launch", "/bin/sh", "-c", "touch " + began},
		&os.ProcAttr{Files: []*os.File{nil, nil, nil, read}})
	if err != nil {
		t.Fatal(err)
	}
	return process, write
}
