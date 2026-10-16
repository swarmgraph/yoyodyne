package execution

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestProcessResultRecordsOutputClosureOnlyWhenObserved(t *testing.T) {
	t.Parallel()
	for _, closedAt := range []time.Time{{}, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)} {
		result := ProcessResult{OutputClosedAt: closedAt}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if _, present := fields["OutputClosedAt"]; present == closedAt.IsZero() {
			t.Fatalf("encoded result = %s, want output closure recorded only when observed", encoded)
		}
		var restored ProcessResult
		if err := json.Unmarshal(encoded, &restored); err != nil || !restored.OutputClosedAt.Equal(closedAt) {
			t.Fatalf("output closure round trip = %v, %v, want %v", restored.OutputClosedAt, err, closedAt)
		}
	}
}

func TestOSProcessRunnerPreservesRawObjectBytes(t *testing.T) {
	t.Parallel()
	command := helperCommand("raw-object", "")
	want := []byte("binary\x00\xff\r\nno final newline")
	var output bytes.Buffer
	command.RawStdout = &output
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil || result.Status != ProcessSucceeded {
		t.Fatalf("Run = %#v, %v", result, err)
	}
	if !bytes.Equal(output.Bytes(), want) || result.Stdout != "" {
		t.Fatalf("object bytes = %q; line output = %q", output.Bytes(), result.Stdout)
	}
}

type refusingRawOutput struct{ err error }

func (w refusingRawOutput) Write(data []byte) (int, error) { return 0, w.err }

func TestOSProcessRunnerReportsARawObjectWriteFailure(t *testing.T) {
	t.Parallel()
	command := helperCommand("raw-object", "")
	want := errors.New("object data exceeded its bound")
	command.RawStdout = refusingRawOutput{want}
	command.Timeout = time.Minute
	if _, err := (OSProcessRunner{}).Run(context.Background(), command, nil); !errors.Is(err, want) {
		t.Fatalf("raw write failure = %v", err)
	}
}

func TestOSProcessRunnerSuccessAndRedaction(t *testing.T) {
	t.Parallel()

	var observed []Output
	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("success", "secret-value"), func(output Output) {
		observed = append(observed, output)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded || result.ExitCode != 0 {
		t.Fatalf("Run() result = %#v", result)
	}
	if strings.Contains(result.Stdout+result.Stderr, "secret-value") {
		t.Fatal("result persisted an unredacted secret")
	}
	if !strings.Contains(result.Stdout, "[REDACTED]") || !strings.Contains(result.Stderr, "warning") {
		t.Fatalf("Run() stdout = %q, stderr = %q", result.Stdout, result.Stderr)
	}
	if len(observed) != 2 {
		t.Fatalf("observed %d outputs, want 2", len(observed))
	}
}

func TestOSProcessRunnerRedactsEveryLineOfAMultilineSecret(t *testing.T) {
	t.Parallel()

	secret := "private-key-header\nprivate-key-body\nprivate-key-footer"
	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("success", secret), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, fragment := range strings.Split(secret, "\n") {
		if strings.Contains(result.Stdout+result.Stderr, fragment) {
			t.Fatalf("result persisted multiline secret fragment %q: %q", fragment, result.Stdout+result.Stderr)
		}
	}
	if strings.Count(result.Stdout, "[REDACTED]") != 3 {
		t.Fatalf("Run() stdout = %q, want three redactions", result.Stdout)
	}
}

func TestOSProcessRunnerFailure(t *testing.T) {
	t.Parallel()

	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("failure", ""), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessFailed || result.ExitCode != 7 {
		t.Fatalf("Run() result = %#v", result)
	}
}

// A process that outlives its total budget is reported as timed out.
//
// The budget is the test's to spend rather than a duration it guesses at. It
// used to be twenty milliseconds of wall clock armed ahead of the exec, which
// on a loaded machine under the race detector fired inside Start() and came
// back as ErrProcessNotStarted -- a different answer to a different question --
// and then five hundred, which is a guess at the same thing with more room. Now
// the runner arms it once the process is running and this test spends it on
// that signal: what ends the process is unambiguously the budget, and no load
// on the machine can make it anything else.
func TestOSProcessRunnerTimeout(t *testing.T) {
	t.Parallel()

	budget := newHeldBudget()
	command := helperCommand("sleep", "")
	command.Timeout = time.Hour
	type outcome struct {
		result ProcessResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := (OSProcessRunner{budget: budget.arm}).Run(context.Background(), command, nil)
		finished <- outcome{result: result, err: err}
	}()
	<-budget.armed
	budget.spend()
	ran := <-finished
	if ran.err != nil {
		t.Fatalf("Run() error = %v", ran.err)
	}
	if ran.result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", ran.result.Status, ProcessTimedOut)
	}
}

// The runner's own timer is the thing under test here: a budget the process
// outlives fires and ends it as timed out, with no seam supplied. The wait is
// on Run and on nothing else, so a loaded machine makes this slower and never
// wrong -- the budget is armed after Start, so the exec cannot spend it, and
// the helper sleeps far longer than any delay in the timer being served.
func TestOSProcessRunnerRealBudgetEndsAProcessThatOutlivesIt(t *testing.T) {
	t.Parallel()

	command := helperCommand("sleep", "")
	command.Timeout = 50 * time.Millisecond
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q from the runner's own timer", result.Status, ProcessTimedOut)
	}
}

// EOF ends the output's idle bound, not the process's budget. Fire both only
// once the runner has seen EOF, so this cannot pass by killing the helper
// before it closed its streams, even on a loaded machine.
func TestOSProcessRunnerKeepsItsBudgetAfterOutputCloses(t *testing.T) {
	t.Parallel()
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw stdout %t", raw), func(t *testing.T) {
			budget := newHeldBudget()
			idle := newHeldIdleBound()
			command := helperCommand("close-output-then-linger", "")
			command.Timeout = time.Hour
			command.IdleTimeout = time.Hour
			if raw {
				command.RawStdout = io.Discard
			}
			runner := OSProcessRunner{
				budget: budget.arm,
				idle:   idle.arm,
				outputClosed: func() {
					idle.trip()
					budget.spend()
				},
			}
			result, err := runner.Run(context.Background(), command, nil)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Status != ProcessTimedOut {
				t.Fatalf("Run() status = %q, want the budget to end it, not EOF or the idle bound", result.Status)
			}
			if result.ExitCode == 0 {
				t.Fatal("the helper slept to its own end instead of being killed at the budget")
			}
			if result.OutputClosedAt.IsZero() || result.OutputClosedAt.After(result.FinishedAt) {
				t.Fatalf("Run() = %#v, want output closure recorded before completion", result)
			}
		})
	}
}

// A real timer still ends a process after EOF. It starts at EOF here so helper
// startup cannot spend the timer first and make the test miss the wait at exit.
func TestOSProcessRunnerRealBudgetEndsAProcessAfterOutputCloses(t *testing.T) {
	t.Parallel()
	var timer *time.Timer
	spent := make(chan time.Time, 1)
	command := helperCommand("close-output-then-linger", "")
	command.Timeout = 50 * time.Millisecond
	runner := OSProcessRunner{
		budget: func(span time.Duration) (<-chan time.Time, func()) {
			return spent, func() {
				if timer != nil {
					timer.Stop()
				}
			}
		},
		outputClosed: func() {
			timer = time.AfterFunc(command.Timeout, func() { spent <- time.Now() })
		},
	}
	result, err := runner.Run(context.Background(), command, nil)
	if err != nil || result.Status != ProcessTimedOut {
		t.Fatalf("Run() = %#v, %v, want the timer to end the process after EOF", result, err)
	}
	if result.ExitCode == 0 {
		t.Fatal("the helper slept to its own end instead of being killed at the budget")
	}
}

func TestOSProcessRunnerWaitsForExitAfterOutputCloses(t *testing.T) {
	t.Parallel()
	for _, cancelProcess := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel %t", cancelProcess), func(t *testing.T) {
			read, release, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			defer release.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			idle := newHeldIdleBound()
			command := helperCommand("close-output-then-finish", "")
			command.Stdin = read
			command.IdleTimeout = time.Hour
			runner := OSProcessRunner{
				idle: idle.arm,
				outputClosed: func() {
					idle.trip()
					if cancelProcess {
						cancel()
					} else if _, err := release.Write([]byte("go\n")); err != nil {
						t.Errorf("release the helper: %v", err)
					}
				},
			}
			result, err := runner.Run(ctx, command, nil)
			want := ProcessSucceeded
			if cancelProcess {
				want = ProcessCancelled
			}
			if err != nil || result.Status != want {
				t.Fatalf("Run() = %#v, %v, want %q after EOF", result, err, want)
			}
			if !cancelProcess && result.ExitCode != 0 {
				t.Fatalf("Run() exit code = %d, want the helper's successful exit", result.ExitCode)
			}
		})
	}
}

// The same for the idle bound as a real timer: a process that says nothing for
// the whole of it is stopped as stalled. That a line of output starts the bound
// over is asserted on the held bound above and not here, because asserting it
// on a real timer means betting that the helper's first line lands inside the
// bound, and the bound counts from Start -- the bet this file used to make and
// lost on the exec alone.
func TestOSProcessRunnerRealIdleBoundStopsASilentProcess(t *testing.T) {
	t.Parallel()

	command := helperCommand("sleep", "")
	command.Timeout = time.Hour
	command.IdleTimeout = 50 * time.Millisecond
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessStalled {
		t.Fatalf("Run() status = %q, want %q from the runner's own timer", result.Status, ProcessStalled)
	}
}

// heldBudget is a total budget a test spends when it chooses. It stands in for
// the runner's timer so that a test about the budget waits on its own signal
// rather than on a wall-clock guess a loaded machine falsifies: the runner
// arming it says the process is running, and spend is the budget running out.
type heldBudget struct {
	armed chan struct{}
	spent chan time.Time
}

func newHeldBudget() *heldBudget {
	return &heldBudget{armed: make(chan struct{}), spent: make(chan time.Time, 1)}
}

func (b *heldBudget) arm(time.Duration) (<-chan time.Time, func()) {
	close(b.armed)
	return b.spent, func() {}
}

// spend is what the timer firing would have been. It never blocks, so it can be
// called from inside the runner's own output observer.
func (b *heldBudget) spend() {
	b.spent <- time.Now()
}

// A process that says nothing for the whole idle bound is stopped as stalled,
// long before a total budget it would otherwise have to exhaust. The bound is
// tripped here, once the runner has armed it, rather than left to a timer the
// test would then have to out-wait.
func TestOSProcessRunnerStopsASilentProcessAsStalled(t *testing.T) {
	t.Parallel()

	idle := newHeldIdleBound()
	command := helperCommand("sleep", "")
	command.Timeout = time.Hour
	command.IdleTimeout = time.Hour
	type outcome struct {
		result ProcessResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := (OSProcessRunner{idle: idle.arm}).Run(context.Background(), command, nil)
		finished <- outcome{result: result, err: err}
	}()
	<-idle.armed
	idle.trip()
	ran := <-finished
	if ran.err != nil {
		t.Fatalf("Run() error = %v", ran.err)
	}
	if ran.result.Status != ProcessStalled {
		t.Fatalf("Run() status = %q, want %q", ran.result.Status, ProcessStalled)
	}
}

// A process that keeps producing output keeps proving it is working: every line
// starts the idle bound over, so a bound far shorter than the process's total
// runtime never stops it.
//
// The claim is read off the bound rather than off a clock. This used to run a
// chatty helper against a two-second bound and assert that it outlived it,
// which made the test a bet that the helper binary would start inside two
// seconds -- lost under the race detector beside another suite, where the first
// line arrived after the bound had already tripped on the startup itself.
func TestOSProcessRunnerLeavesAChattyProcessAlone(t *testing.T) {
	t.Parallel()

	idle := newHeldIdleBound()
	command := helperCommand("counted-chatter", "")
	command.Timeout = time.Hour
	command.IdleTimeout = time.Hour
	result, err := (OSProcessRunner{idle: idle.arm}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() status = %q, want %q for a process that never went quiet", result.Status, ProcessSucceeded)
	}
	lines := strings.Count(result.Stdout, "\n")
	if lines < 2 {
		t.Fatalf("Run() stdout = %q, want the chatter it kept producing", result.Stdout)
	}
	if resets := idle.resets(); resets != lines {
		t.Fatalf("the idle bound was started over %d time(s) for %d lines, want every line to count as life", resets, lines)
	}
}

// The total budget still bounds a process that is alive and producing output,
// and what stops it is reported as the budget rather than as a stall. The
// budget is spent on the process's first line, so what it ends is a process
// demonstrably talking.
func TestOSProcessRunnerTimesOutAChattyProcessOnItsTotalBudget(t *testing.T) {
	t.Parallel()

	budget := newHeldBudget()
	command := helperCommand("endless-chatter", "")
	command.Timeout = time.Hour
	command.IdleTimeout = time.Hour
	var spent sync.Once
	result, err := (OSProcessRunner{budget: budget.arm}).Run(context.Background(), command, func(Output) {
		spent.Do(budget.spend)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessTimedOut)
	}
}

// heldIdleBound is an idle bound a test trips when it chooses, and that counts
// how often the runner started it over. It stands in for the runner's timer for
// the reason heldBudget does.
type heldIdleBound struct {
	armed    chan struct{}
	tripped  chan time.Time
	mutex    sync.Mutex
	restarts int
}

func newHeldIdleBound() *heldIdleBound {
	return &heldIdleBound{armed: make(chan struct{}), tripped: make(chan time.Time, 1)}
}

func (b *heldIdleBound) arm(time.Duration) idleBound {
	close(b.armed)
	return b
}

func (b *heldIdleBound) expired() <-chan time.Time { return b.tripped }

func (b *heldIdleBound) reset() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.restarts++
}

func (b *heldIdleBound) stop() {}

// trip is what the timer firing would have been.
func (b *heldIdleBound) trip() {
	b.tripped <- time.Now()
}

func (b *heldIdleBound) resets() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.restarts
}

func TestOSProcessRunnerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (OSProcessRunner{}).Run(ctx, helperCommand("sleep", ""), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessCancelled {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessCancelled)
	}
}

// A process that outruns the retained bound is truncated and not killed. The
// run it belongs to has to complete on its own merits: a run that died of its
// own diagnostics took its work and the account of what it cost with it.
func TestOSProcessRunnerTruncatesOutputInsteadOfFailingTheProcess(t *testing.T) {
	t.Parallel()

	command := helperCommand("success", "")
	command.MaxOutputBytes = 2
	command.OutputRecord = EventLogOf("run-0123456789abcdef")
	var observed []Output
	result, err := (OSProcessRunner{}).Run(context.Background(), command, func(output Output) {
		observed = append(observed, output)
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want output past the bound to be no error at all", err)
	}
	if result.Status != ProcessSucceeded || result.ExitCode != 0 {
		t.Fatalf("Run() result = %#v, want the process judged on its own exit", result)
	}
	// Every line still reaches the observer, which is what puts the whole of the
	// output in the record the marker names -- and, for a provider stream, what
	// keeps the terminal result the run is priced from from being dropped.
	if len(observed) != 2 {
		t.Fatalf("observed %d outputs, want the 2 the process produced", len(observed))
	}
	if result.OutputTruncation == "" {
		t.Fatal("Run() reported no truncation for output the bound cut")
	}
	if !strings.Contains(result.OutputTruncation, EventLogOf("run-0123456789abcdef")) {
		t.Fatalf("truncation marker = %q, want the durable record named", result.OutputTruncation)
	}
	if !strings.Contains(result.Stdout, result.OutputTruncation) {
		t.Fatalf("Run() stdout = %q, want the cut copy to carry the marker", result.Stdout)
	}
	if !strings.Contains(result.Stderr, result.OutputTruncation) {
		t.Fatalf("Run() stderr = %q, want the cut copy to carry the marker", result.Stderr)
	}
}

// A caller that kept the retained copy and nothing else is told the rest is
// gone, rather than sent after a durable record nobody wrote.
func TestOSProcessRunnerSaysWhenNothingHoldsTheOutputItCut(t *testing.T) {
	t.Parallel()

	command := helperCommand("success", "")
	command.MaxOutputBytes = 2
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.OutputTruncation, "not retained") {
		t.Fatalf("truncation marker = %q, want the absence of a record said plainly", result.OutputTruncation)
	}
}

// What is retained of a cut stream is a prefix ending at the marker. A stream
// that has lost a line keeps losing them, rather than resuming wherever a later
// short line happened to fit and leaving a copy with a hole in the middle.
func TestOSProcessRunnerRetainsAPrefixOfACutStream(t *testing.T) {
	t.Parallel()

	command := helperCommand("counted-chatter", "")
	// Wide enough for the first few lines and nowhere near all of them, so the
	// cut lands mid-stream rather than at either end.
	command.MaxOutputBytes = 40
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.OutputTruncation == "" {
		t.Fatalf("Run() stdout = %q, want the bound to have cut it", result.Stdout)
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("Run() stdout = %q, want the lines that fit and then the marker", result.Stdout)
	}
	if lines[len(lines)-1] != result.OutputTruncation {
		t.Fatalf("last retained line = %q, want the marker to end the prefix", lines[len(lines)-1])
	}
	// The retained lines are the first ones the process wrote, in order and with
	// none missing between them.
	for index, line := range lines[:len(lines)-1] {
		if want := fmt.Sprintf("line %d", index); line != want {
			t.Fatalf("retained line %d = %q, want %q; the copy is not a prefix", index, line, want)
		}
	}
}

// A process that stayed under the bound is never marked, so the marker's
// presence is the whole of the fact a reader has to check.
func TestOSProcessRunnerLeavesOutputUnderTheBoundUnmarked(t *testing.T) {
	t.Parallel()

	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("success", ""), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.OutputTruncation != "" {
		t.Fatalf("Run() truncation = %q, want none for output that fit", result.OutputTruncation)
	}
}

// One line too long to hold is cut with a marker, and neither the process nor
// the run dies of it. This used to fail the whole invocation with "token too
// long": a provider stream puts one tool result on one line, so a large enough
// result took the run's work and the account of what it cost with it.
func TestOSProcessRunnerTruncatesAnOversizedLineInsteadOfFailingTheProcess(t *testing.T) {
	t.Parallel()

	command := helperCommand("oversized-line", "")
	command.Timeout = 30 * time.Second
	var observed []Output
	result, err := (OSProcessRunner{}).Run(context.Background(), command, func(output Output) {
		observed = append(observed, output)
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want an oversized line to be no error at all", err)
	}
	if result.Status != ProcessSucceeded || result.ExitCode != 0 {
		t.Fatalf("Run() result status = %q exit = %d, want the process judged on its own exit", result.Status, result.ExitCode)
	}
	// The long line, then the ordinary one after it: the stream keeps being read
	// past the cut rather than ending at it.
	if len(observed) != 2 {
		t.Fatalf("observed %d outputs, want the 2 the process produced", len(observed))
	}
	if !observed[0].LineTruncated {
		t.Fatal("the oversized line was not reported as truncated")
	}
	if !strings.Contains(observed[0].Text, "line truncated at") {
		t.Fatalf("oversized line ends %q, want the marker naming the cut", tail(observed[0].Text))
	}
	if !strings.HasPrefix(observed[0].Text, strings.Repeat("x", 1024)) {
		t.Fatal("the retained part of the oversized line is not a prefix of what the process wrote")
	}
	if observed[1].Text != "after the long line" || observed[1].LineTruncated {
		t.Fatalf("second output = %#v, want the untouched line that followed the cut one", observed[1])
	}
	if !strings.Contains(result.Stdout, "after the long line") {
		t.Fatal("the retained copy lost the line that followed the cut one")
	}
}

// A secret straddling the cut must not have its leading half kept: redaction
// replaces whole values, so a partial one survives it.
func TestOSProcessRunnerDoesNotCutThroughASecret(t *testing.T) {
	t.Parallel()

	secret := "sk-cut-straddling-credential"
	command := helperCommand("straddling-secret", secret)
	command.Timeout = 30 * time.Second
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for length := 8; length <= len(secret); length++ {
		if strings.Contains(result.Stdout, secret[:length]) {
			t.Fatalf("the cut line retained %q, a leading part of the secret that redaction cannot replace", secret[:length])
		}
	}
}

func TestReadLineBoundsAndDrainsALongLine(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		stream  string
		bound   int
		want    []string
		dropped []int
	}{
		{
			name:    "lines under the bound are untouched",
			stream:  "alpha\nbeta\n",
			bound:   16,
			want:    []string{"alpha", "beta"},
			dropped: []int{0, 0},
		},
		{
			name:    "a blank line is still a line",
			stream:  "\nbeta\n",
			bound:   16,
			want:    []string{"", "beta"},
			dropped: []int{0, 0},
		},
		{
			name:    "a carriage return before the newline is not part of the line",
			stream:  "alpha\r\n",
			bound:   16,
			want:    []string{"alpha"},
			dropped: []int{0},
		},
		{
			name:    "an unterminated last line is reported",
			stream:  "alpha\nbeta",
			bound:   16,
			want:    []string{"alpha", "beta"},
			dropped: []int{0, 0},
		},
		{
			// The long line is cut and the one after it is read whole, which is
			// the whole claim: the tail was drained rather than left in the pipe.
			name:    "a long line is cut and the stream carries on",
			stream:  "aaaaaaaaaa\nbeta\n",
			bound:   4,
			want:    []string{"aaaa", "beta"},
			dropped: []int{6, 0},
		},
		{
			name:    "a long unterminated last line is cut",
			stream:  "aaaaaaaaaa",
			bound:   4,
			want:    []string{"aaaa"},
			dropped: []int{6},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// Smaller than the shortest line above, so every case reads through
			// the buffer-full path a megabyte-long line reaches in production.
			reader := bufio.NewReaderSize(strings.NewReader(testCase.stream), 16)
			for index, want := range testCase.want {
				line, dropped, err := readLine(reader, testCase.bound)
				if err != nil && !errors.Is(err, io.EOF) {
					t.Fatalf("readLine() line %d error = %v", index, err)
				}
				if string(line) != want || dropped != testCase.dropped[index] {
					t.Fatalf("readLine() line %d = %q dropped %d, want %q dropped %d", index, line, dropped, want, testCase.dropped[index])
				}
			}
			line, dropped, err := readLine(reader, testCase.bound)
			if !errors.Is(err, io.EOF) || len(line) > 0 || dropped > 0 {
				t.Fatalf("readLine() after the last line = %q dropped %d err %v, want an empty end of stream", line, dropped, err)
			}
		})
	}
}

// tail is the end of a string, for a failure message that must not print a
// megabyte of it.
func tail(value string) string {
	if len(value) <= 80 {
		return value
	}
	return "…" + value[len(value)-80:]
}

func TestSensitiveEnvironmentValues(t *testing.T) {
	t.Parallel()

	got := SensitiveEnvironmentValues([]string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"GH_TOKEN=github-secret",
		"SERVICE_PASSWORD=password-secret",
		"DUPLICATE_TOKEN=github-secret",
		"EMPTY_SECRET=",
		"MALFORMED",
	})
	want := []string{"anthropic-secret", "github-secret", "password-secret"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("SensitiveEnvironmentValues() = %#v, want %#v", got, want)
	}
}

func TestRedactorReplacesOverlappingSecretsLongestFirst(t *testing.T) {
	t.Parallel()

	redactor := NewRedactor("token", "token-suffix")
	got := redactor.Redact("short=token long=token-suffix")
	if got != "short=[REDACTED] long=[REDACTED]" {
		t.Fatalf("Redact() = %q", got)
	}
}

func helperCommand(mode, secret string) Command {
	return Command{
		Name:     os.Args[0],
		Args:     []string{"-test.run=TestProcessHelper", "--", mode, secret},
		Env:      append(os.Environ(), "GO_WANT_PROCESS_HELPER=1"),
		Redactor: NewRedactor(secret),
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PROCESS_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := 0
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || len(args) <= separator+1 {
		os.Exit(99)
	}
	mode := args[separator+1]
	secret := ""
	if len(args) > separator+2 {
		secret = args[separator+2]
	}
	switch mode {
	case "raw-object":
		os.Stdout.Write([]byte("binary\x00\xff\r\nno final newline"))
		os.Exit(0)
	case "success":
		fmt.Printf("result %s\n", secret)
		fmt.Fprintln(os.Stderr, "warning")
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "failed")
		os.Exit(7)
	case "sleep":
		// Long enough that no delay in a timer being served on a loaded machine
		// lets this exit on its own first, and bounded, so a kill that regressed
		// leaves nothing running past the minute.
		time.Sleep(time.Minute)
		os.Exit(0)
	case "close-output-then-linger", "reply-close-output-then-linger", "close-output-then-finish":
		if mode == "reply-close-output-then-linger" {
			fmt.Println("final reply")
		}
		os.Stdout.Close()
		os.Stderr.Close()
		// Every helper ends on its own even if the runner's group kill breaks.
		if mode == "close-output-then-finish" {
			time.AfterFunc(time.Minute, func() { os.Exit(97) })
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		} else {
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	case "counted-chatter":
		// Numbered so a retained copy can be checked for being a prefix rather
		// than only for being short.
		for line := 0; line < 40; line++ {
			fmt.Printf("line %d\n", line)
		}
		os.Exit(0)
	case "reply-then-linger":
		// A provider session that has written its final reply and is kept alive by
		// work it started in the background. Bounded, like "sleep", so a kill that
		// regressed leaves nothing running past the minute.
		fmt.Println("final reply")
		time.Sleep(time.Minute)
		os.Exit(0)
	case "reply-then-finish":
		// The same, where the background work finishes on its own: it says one
		// thing when the test lets it and exits.
		fmt.Println("final reply")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Println("background work done")
		os.Exit(0)
	case "endless-chatter":
		for {
			fmt.Println("still working")
			time.Sleep(10 * time.Millisecond)
		}
	case "oversized-line":
		// One line past the per-line bound and then an ordinary one, so a reader
		// is asked both to cut the long line and to keep reading after it.
		fmt.Printf("%s\n", strings.Repeat("x", maxLineBytes+(1<<16)))
		fmt.Println("after the long line")
		os.Exit(0)
	case "straddling-secret":
		// The secret sits astride the cut, half of it inside the bound and half
		// outside, which is the only place a cut can leave a partial credential
		// that redaction has no whole value to replace.
		fmt.Printf("%s%s%s\n",
			strings.Repeat("x", maxLineBytes-len(secret)/2),
			secret,
			strings.Repeat("x", 1<<16))
		os.Exit(0)
	default:
		os.Exit(98)
	}
}

func TestDiagnosticTailRedactsASecretAcrossItsCutAndKeepsWholeCharacters(t *testing.T) {
	secret := "secret-across-the-tail-boundary"
	for _, line := range []string{
		strings.Repeat("x", 100) + secret + strings.Repeat("é", 1275) + "last cause",
		strings.Repeat("é", 2000) + "last cause",
	} {
		outputs := make(chan Output, 2)
		scanErrors := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(1)
		scanOutput(strings.NewReader(line+"\n"), StreamStderr, RealClock{}, NewRedactor(secret), outputs, scanErrors, func() {}, &group)
		group.Wait()
		output := <-outputs
		if !utf8.ValidString(output.Tail) || !strings.HasSuffix(output.Tail, "last cause") || !strings.Contains(output.Tail, "earlier output omitted") {
			t.Fatalf("tail = %q", output.Tail)
		}
		for i := 0; i+8 <= len(secret); i++ {
			if strings.Contains(output.Tail, secret[i:i+8]) {
				t.Fatalf("tail retained part of a secret: %q", output.Tail)
			}
		}
	}
}
