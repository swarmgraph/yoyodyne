//go:build darwin || linux

package execution

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runProcessHelperVariable selects what this test binary does when it is run as
// a helper rather than as the tests.
const runProcessHelperVariable = "GO_WANT_RUN_PROCESS_HELPER"

// TestRunProcessesHelper is the test binary as a helper. "launch" starts a
// server in a session of its own, so it leaves the launcher's process group,
// and exits at once; "serve" listens on a port, writes its number and port, and
// ends on its own after a minute however the test went.
func TestRunProcessesHelper(t *testing.T) {
	switch os.Getenv(runProcessHelperVariable) {
	case "launch":
		server := exec.Command(os.Args[0], "-test.run=^TestRunProcessesHelper$")
		server.Env = append(os.Environ(), runProcessHelperVariable+"=serve")
		server.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := server.Start(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "serve":
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		said := strconv.Itoa(os.Getpid()) + " " + listener.Addr().String()
		_ = os.WriteFile(os.Getenv("RUN_PROCESS_HELPER_FILE")+".tmp", []byte(said), 0o600)
		_ = os.Rename(os.Getenv("RUN_PROCESS_HELPER_FILE")+".tmp", os.Getenv("RUN_PROCESS_HELPER_FILE"))
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// startDetachedServer runs the launcher through the runner the way a session
// is run, marked for runID, and returns the server it left behind, which is no
// longer in the launcher's process group or session.
func startDetachedServer(t *testing.T, runID string) int {
	t.Helper()
	record := filepath.Join(t.TempDir(), "server")
	result, err := (OSProcessRunner{}).Run(context.Background(), Command{
		Name:    os.Args[0],
		Args:    []string{"-test.run=^TestRunProcessesHelper$"},
		Env:     WithRunIdentity(append(os.Environ(), runProcessHelperVariable+"=launch", "RUN_PROCESS_HELPER_FILE="+record), runID),
		Timeout: time.Minute,
	}, nil)
	if err != nil || result.Status != ProcessSucceeded {
		t.Fatalf("launcher: %+v, %v", result, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(record)
		if err == nil {
			fields := strings.Fields(string(raw))
			pid, _ := strconv.Atoi(fields[0])
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			if group, err := syscall.Getpgid(pid); err != nil || group == syscall.Getpgrp() {
				t.Fatalf("the server is in group %d (%v), want one of its own", group, err)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the detached server never said it was listening")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func alive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// TestRunProcessesStopsAServerThatLeftTheRunsGroup is the reported case: a
// session starts a server that leaves its process group and session and keeps
// listening on a port after the session has ended. When the run's processes
// are stopped it is gone, and another run's server is untouched.
func TestRunProcessesStopsAServerThatLeftTheRunsGroup(t *testing.T) {
	ours := startDetachedServer(t, "run-stop-test-ours")
	theirs := startDetachedServer(t, "run-stop-test-theirs")
	if !alive(ours) {
		t.Fatal("the server ended with the session that started it; nothing is left to test")
	}
	left := (RunProcesses{TermWait: 5 * time.Second, KillWait: 5 * time.Second}).Stop(context.Background(), RunProcessesRequest{RunID: "run-stop-test-ours"})
	if len(left) != 0 {
		t.Fatalf("Stop() left %+v, want nothing", left)
	}
	deadline := time.Now().Add(10 * time.Second)
	for alive(ours) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d, the run's detached server, is still running", ours)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !alive(theirs) {
		t.Fatalf("process %d, another run's server, was stopped", theirs)
	}
}

// TestRunProcessesStopsADetachedChildHoldingTheLaunchFile is a server that
// cleared its environment, so it carries no identifier, and left its session,
// but still holds the file the harness handed the launch. It is the run's and
// is stopped.
func TestRunProcessesStopsADetachedChildHoldingTheLaunchFile(t *testing.T) {
	hold, err := os.Create(filepath.Join(t.TempDir(), "run-hold-test.attempt.hold"))
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command("/bin/sh", "-c", "sleep 60")
	server.Env = []string{"PATH=/bin:/usr/bin"}
	server.ExtraFiles = []*os.File{hold}
	server.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	_ = hold.Close()
	pid := server.Process.Pid
	reaped := make(chan struct{})
	go func() { _ = server.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !(systemProcessTable{}).holding([]string{hold.Name()})[pid] {
		t.Skip("this machine cannot say which processes hold a file (lsof sees nothing here), so the hold test cannot be shown")
	}
	left := (RunProcesses{TermWait: 5 * time.Second, KillWait: 5 * time.Second}).Stop(context.Background(), RunProcessesRequest{RunID: "run-hold-test", Holds: []string{hold.Name()}})
	if len(left) != 0 {
		t.Fatalf("Stop() left %+v, want nothing", left)
	}
	select {
	case <-reaped:
	case <-time.After(10 * time.Second):
		t.Fatalf("process %d, which holds the run's launch file, is still running", pid)
	}
}
