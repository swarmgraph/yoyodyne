package home

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The identity is made the first time it is asked for, kept in the home, and
// read back unchanged on every later call.
func TestTheMachineIdentityIsMadeOnceAndReused(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), DirectoryName)
	first, err := MachineIdentity(root)
	if err != nil {
		t.Fatalf("MachineIdentity() error = %v", err)
	}
	if !machineIdentityPattern.MatchString(first) {
		t.Fatalf("MachineIdentity() = %q, want an identifier this harness makes", first)
	}
	written, err := os.ReadFile(filepath.Join(root, MachineIdentityFileName))
	if err != nil {
		t.Fatalf("the identity was not kept in the home: %v", err)
	}
	for range 3 {
		again, err := MachineIdentity(root)
		if err != nil || again != first {
			t.Fatalf("MachineIdentity() again = %q, %v; want %q", again, err, first)
		}
	}
	unchanged, err := os.ReadFile(filepath.Join(root, MachineIdentityFileName))
	if err != nil || string(unchanged) != string(written) {
		t.Fatalf("reading the identity rewrote its file:\nbefore %s\nafter %s", written, unchanged)
	}
}

// Two homes are two factories, so each makes an identity of its own.
func TestTwoHomesHaveDifferentMachineIdentities(t *testing.T) {
	t.Parallel()

	left, err := MachineIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	right, err := MachineIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatalf("two homes share the machine identity %q", left)
	}
}

// Processes asking at once on a home that has none yet agree on one identity.
func TestProcessesAskingAtOnceAgreeOnOneMachineIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	answers := make([]string, 8)
	failures := make([]error, len(answers))
	var group sync.WaitGroup
	for index := range answers {
		group.Add(1)
		go func() {
			defer group.Done()
			answers[index], failures[index] = MachineIdentity(root)
		}()
	}
	group.Wait()
	for index, answer := range answers {
		if failures[index] != nil || answer != answers[0] {
			t.Fatalf("answers = %v, errors = %v; want one identity", answers, failures)
		}
	}
}

// A file that cannot be read is an error naming it, never a new identity:
// making another would make this machine read as a different factory.
func TestAnUnreadableMachineIdentityIsNotMadeAgain(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"not a record":              "not json",
		"not an identifier we make": `{"machine":"somebody-else"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			target := filepath.Join(root, MachineIdentityFileName)
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if machine, err := MachineIdentity(root); err == nil || !strings.Contains(err.Error(), target) {
				t.Fatalf("MachineIdentity() = %q, %v; want an error naming %s", machine, err, target)
			}
			if kept, err := os.ReadFile(target); err != nil || string(kept) != content {
				t.Fatalf("the unreadable file was replaced: %q, %v", kept, err)
			}
		})
	}
}

func TestTheMachineIdentityNeedsAnAbsoluteHome(t *testing.T) {
	t.Parallel()

	if _, err := MachineIdentity("relative/home"); err == nil {
		t.Fatal("MachineIdentity() accepted a relative home")
	}
}
