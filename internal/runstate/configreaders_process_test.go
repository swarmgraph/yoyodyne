//go:build darwin || linux

package runstate

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A recorded part is running only while its process is the one that wrote the
// record. A process that has exited and waits for its parent to collect it
// still answers the null signal, and a later process can take a gone one's
// number; read as running, either is a copy of the part on whatever build its
// record names, which is how the landing records came to list dozens of Slack
// sinks when one was running. Both are read as what they are, and a record
// nothing runs behind any more is removed.
func TestARecordedPartIsRunningOnlyWhileItsOwnProcessIs(t *testing.T) {
	t.Parallel()

	store, err := NewConfigReaderStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	// A child that exits at once and that nothing waits on: let go the way the
	// launcher lets every part go.
	command := exec.Command("/bin/sh", "-c", "exit 0")
	if err := command.Start(); err != nil {
		t.Fatalf("start a child: %v", err)
	}
	exitedPID := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		t.Fatalf("release the child: %v", err)
	}
	t.Cleanup(func() {
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(exitedPID, &status, syscall.WNOHANG, nil)
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		look, err := LookProcess(exitedPID)
		if err != nil {
			t.Fatalf("LookProcess() error = %v", err)
		}
		if look.Exists && look.Exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child never showed as exited and uncollected: %+v", look)
		}
		time.Sleep(20 * time.Millisecond)
	}

	self := os.Getpid()
	now := time.Now()
	for _, reader := range []ConfigReader{
		// This process, recorded as it runs: the part, running.
		{Service: "slack", PID: self, Build: "3333333333333333", ConfigPath: "/work/.yoyodyne/config.yaml", StartedAt: now, Keys: []string{"version"}},
		// The exited child: a copy stopped and never collected.
		{Service: "slack", PID: exitedPID, Build: "1111111111111111", ConfigPath: "/work/.yoyodyne/config.yaml", StartedAt: now, Keys: []string{"version"}},
		// This process's number, recorded long before it started: a gone copy
		// whose number this process took.
		{Service: "supervisor", PID: self, Build: "2222222222222222", ConfigPath: "/work/.yoyodyne/config.yaml", StartedAt: now.Add(-24 * time.Hour * 365), Keys: []string{"version"}},
	} {
		if err := store.Record(reader); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	running, err := store.Running()
	if err != nil {
		t.Fatalf("Running() error = %v", err)
	}
	if len(running) != 1 || running[0].PID != self || running[0].Service != "slack" {
		t.Fatalf("Running() = %+v, want only this process as the slack part", running)
	}

	processes, err := store.Processes()
	if err != nil {
		t.Fatalf("Processes() error = %v", err)
	}
	states := map[string]string{}
	for _, process := range processes {
		state := "running"
		switch {
		case process.Exited():
			state = "exited"
		case process.Gone():
			state = "gone"
		}
		states[process.Reader.Build] = state
		if err := store.Forget(process); err != nil {
			t.Fatalf("Forget(%+v) error = %v", process.Reader, err)
		}
	}
	want := map[string]string{"3333333333333333": "running", "1111111111111111": "exited", "2222222222222222": "gone"}
	for build, state := range want {
		if states[build] != state {
			t.Errorf("the copy on build %s reads as %q, want %q", build, states[build], state)
		}
	}
	left, err := store.Processes()
	if err != nil {
		t.Fatalf("Processes() error = %v", err)
	}
	if len(left) != 2 {
		t.Errorf("records left = %+v, want the running part's and the exited one's, which only its parent can end; the gone copy's removed", left)
	}
}
