//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOSProcessRunnerTimeoutTerminatesDescendantsHoldingPipes is the shell
// that backgrounds a child sharing its pipes and then waits on it. Killing the
// shell alone leaves that child holding the runner's stdout open, and the
// runner then drains until the child chooses to exit.
//
// Nothing here is a clock. The budget is spent when the shell says the child is
// holding the pipes, so the kill is asked of a tree that has the shape the test
// is about rather than of whatever a loaded machine had forked by then. And
// whether the kill reached the child is read off what it left behind: it holds
// the pipes for the whole of its sleep and writes its marker only afterwards,
// and Run cannot return until every holder of its pipes has closed them -- so a
// marker on disk when Run returns says the child ran to its own end with the
// pipes open, and no marker says the group kill reached it. The bound this used
// to keep, that Run came back inside two seconds, was a guess at how long a
// loaded machine takes to get here.
func TestOSProcessRunnerTimeoutTerminatesDescendantsHoldingPipes(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "outlived")
	budget := newHeldBudget()
	result, err := (OSProcessRunner{budget: budget.arm}).Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", fmt.Sprintf("( sleep 5; touch '%s' ) & echo holding; wait", marker)},
		Timeout: time.Hour,
	}, func(output Output) {
		if output.Text == "holding" {
			budget.spend()
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessTimedOut)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the descendant holding the pipes ran to its own end; the timeout never reached it")
	}
}

func TestOSProcessRunnerTimeoutTerminatesDescendantsAfterOutputCloses(t *testing.T) {
	t.Parallel()
	read, witness, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer witness.Close()
	budget := newHeldBudget()
	runner := OSProcessRunner{budget: budget.arm, outputClosed: budget.spend}
	// fd 0 carries a pipe the test reads. The child keeps it open after
	// closing stdout and stderr, and writes only if it survives the group
	// kill. EOF on this pipe proves the child has ended; checking a marker
	// immediately after the parent exited would prove nothing about it.
	// The explicit <&0 keeps a noninteractive shell from replacing the
	// background child's fd 0 with /dev/null.
	result, err := runner.Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "( exec 1>&- 2>&-; sleep 5; echo survived >&0 ) <&0 & exec 1>&- 2>&-; wait"},
		Stdin:   witness,
		Timeout: time.Hour,
	}, nil)
	witness.Close()
	left, readErr := io.ReadAll(read)
	if err != nil || result.Status != ProcessTimedOut {
		t.Fatalf("Run() = %#v, %v, want the budget to end the tree after EOF", result, err)
	}
	if readErr != nil || len(left) != 0 {
		t.Fatalf("the descendant outlived the group kill: %q, %v", left, readErr)
	}
}

// TestOSProcessRunnerReapsWhatASucceedingCommandLeftRunning is the shape that
// cost the operator a machine: a load test that backgrounds work, points its
// output away from this runner's pipes, and exits 0 before its own cleanup
// runs. Nothing cancels the context on that path, so nothing used to kill the
// group, and the work carried on for hours after the run that spawned it was
// over.
//
// Each descendant here carries its own bound — it sleeps and exits — which is
// the other half of the rule and is what keeps a regression on this test from
// leaving load on the machine that ran it.
func TestOSProcessRunnerReapsWhatASucceedingCommandLeftRunning(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	// The command does not exit until every descendant has said it is running,
	// so a pass here is a pass against work that was genuinely spawned rather
	// than against a loop that never forked anything.
	script := fmt.Sprintf(`for name in one two three; do
	( touch '%[1]s/started.'"$name"; sleep 1; touch '%[1]s/outlived.'"$name" ) >/dev/null 2>&1 &
done
until [ -f '%[1]s/started.one' ] && [ -f '%[1]s/started.two' ] && [ -f '%[1]s/started.three' ]; do :; done
`, directory)

	result, err := (OSProcessRunner{}).Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", script},
		Timeout: testProcessBudget,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessSucceeded)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, statErr := os.Stat(filepath.Join(directory, "started."+name)); statErr != nil {
			t.Fatalf("descendant %q never started, so this proves nothing about reaping: %v", name, statErr)
		}
	}

	// A descendant that survived the reap finishes its second past the command
	// and leaves its marker. Three times its own sleep is the margin against a
	// loaded machine; the failure is reported the moment a marker appears rather
	// than only at the end of it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, name := range []string{"one", "two", "three"} {
			if _, statErr := os.Stat(filepath.Join(directory, "outlived."+name)); statErr == nil {
				t.Fatalf("descendant %q outlived the command that spawned it", name)
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
