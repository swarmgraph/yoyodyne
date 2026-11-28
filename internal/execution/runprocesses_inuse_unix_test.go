//go:build darwin || linux

package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// unmarkedEnvironment is this process's environment with no run's identifier,
// as a person's process has.
func unmarkedEnvironment(extra ...string) []string {
	var environment []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, RunIdentityVariable+"=") {
			environment = append(environment, entry)
		}
	}
	return append(environment, extra...)
}

// startHelper starts this test binary as a helper of the given kind, which ends
// on its own after a minute, and returns it with a channel closed once it has
// been waited for.
func startHelper(t *testing.T, environment []string, files ...*os.File) (*exec.Cmd, chan struct{}) {
	t.Helper()
	helper := exec.Command(os.Args[0], "-test.run=^TestRunProcessesHelper$")
	helper.Env = environment
	helper.ExtraFiles = files
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = helper.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = helper.Process.Kill(); <-reaped })
	return helper, reaped
}

// waitConnected waits until the machine's connections show peer at the other
// end of one of pid's, and skips the test where this machine never shows it.
func waitConnected(t *testing.T, pid, peer int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		connections, err := (systemProcessTable{}).connections()
		if err != nil {
			t.Skipf("this machine's local connections cannot be read here (%v), so the test cannot be shown", err)
		}
		for _, connection := range connections[pid] {
			for _, found := range connection.Peers {
				if found == peer {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Skipf("this machine never showed process %d connected to process %d (got %+v), so the test cannot be shown", peer, pid, connections[pid])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stopAndReap(t *testing.T, helper *exec.Cmd, reaped chan struct{}) {
	t.Helper()
	_ = helper.Process.Kill()
	select {
	case <-reaped:
	case <-time.After(10 * time.Second):
		t.Fatalf("helper %d did not end", helper.Process.Pid)
	}
}

// A server of the run that a process carrying no run's identifier is
// connected to over TCP is left running and named with that process; once the
// connection closes, the next stop stops it.
func TestRunProcessesLeavesRunningAServerSomethingIsConnectedTo(t *testing.T) {
	const runID = "run-in-use-test-tcp"
	record := filepath.Join(t.TempDir(), "server")
	served, servedReaped := startHelper(t, WithRunIdentity(unmarkedEnvironment(runProcessHelperVariable+"=serve", "RUN_PROCESS_HELPER_FILE="+record), runID))
	server, address := served.Process.Pid, waitListening(t, record)
	client, reaped := startHelper(t, unmarkedEnvironment(runProcessHelperVariable+"=connect", "RUN_PROCESS_HELPER_ADDRESS="+address))
	waitConnected(t, server, client.Process.Pid)

	stopper := RunProcesses{TermWait: 5 * time.Second, KillWait: 5 * time.Second}
	left := stopper.Stop(context.Background(), RunProcessesRequest{RunID: runID})
	if len(left) != 1 || left[0].PID != server || !left[0].InUse || !strings.Contains(left[0].Why, "process "+strconv.Itoa(client.Process.Pid)+" (") {
		t.Fatalf("Stop() left %+v, want the server left running for process %d", left, client.Process.Pid)
	}
	if !alive(server) {
		t.Fatal("the server something is connected to was stopped")
	}

	stopAndReap(t, client, reaped)
	if left := stopper.Stop(context.Background(), RunProcessesRequest{RunID: runID}); len(left) != 0 {
		t.Fatalf("Stop() after the connection closed left %+v", left)
	}
	select {
	case <-servedReaped:
	case <-time.After(10 * time.Second):
		t.Fatalf("process %d is still running after nothing was connected to it", server)
	}
}

// waitListening is the address the serve helper wrote to record once it
// listens.
func waitListening(t *testing.T, record string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if raw, err := os.ReadFile(record); err == nil {
			if fields := strings.Fields(string(raw)); len(fields) == 2 {
				return fields[1]
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never said it was listening")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The same over a socket: a process of the run joined by a socket pair to a
// process carrying no run's identifier is left running until that process
// ends.
func TestRunProcessesLeavesRunningOneJoinedByASocket(t *testing.T) {
	const runID = "run-in-use-test-socket"
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	ours, theirs := os.NewFile(uintptr(pair[0]), "ours"), os.NewFile(uintptr(pair[1]), "theirs")
	marked, markedReaped := startHelper(t, WithRunIdentity(unmarkedEnvironment(runProcessHelperVariable+"=hold"), runID), ours)
	person, personReaped := startHelper(t, unmarkedEnvironment(runProcessHelperVariable+"=hold"), theirs)
	_ = ours.Close()
	_ = theirs.Close()
	waitConnected(t, marked.Process.Pid, person.Process.Pid)

	stopper := RunProcesses{TermWait: 5 * time.Second, KillWait: 5 * time.Second}
	left := stopper.Stop(context.Background(), RunProcessesRequest{RunID: runID})
	if len(left) != 1 || left[0].PID != marked.Process.Pid || !left[0].InUse || !strings.Contains(left[0].Why, "process "+strconv.Itoa(person.Process.Pid)+" (") {
		t.Fatalf("Stop() left %+v, want process %d left running for process %d", left, marked.Process.Pid, person.Process.Pid)
	}

	stopAndReap(t, person, personReaped)
	if left := stopper.Stop(context.Background(), RunProcessesRequest{RunID: runID}); len(left) != 0 {
		t.Fatalf("Stop() after the other end closed left %+v", left)
	}
	select {
	case <-markedReaped:
	case <-time.After(10 * time.Second):
		t.Fatalf("process %d is still running after nothing was connected to it", marked.Process.Pid)
	}
}
