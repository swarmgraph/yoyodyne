package execution

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultMaxOutputBytes = 8 << 20

// maxLineBytes bounds one line of process output. A line past it is cut here,
// with a marker, rather than killing the process that wrote it: a provider
// stream puts one tool result on one line, and a large enough result used to end
// the run with bufio.Scanner's "token too long" — the same self-inflicted death
// the retained-output bound no longer causes, one layer down.
//
// What it bounds is what this runner passes on and nothing else. The rest of an
// oversized line is drained and discarded rather than left in the pipe, so the
// process is never blocked on a full one and every line after the long one is
// still read.
const maxLineBytes = 1 << 20

type ProcessStatus string

const (
	ProcessSucceeded ProcessStatus = "succeeded"
	ProcessFailed    ProcessStatus = "failed"
	ProcessCancelled ProcessStatus = "cancelled"
	ProcessTimedOut  ProcessStatus = "timed_out"
	// ProcessStalled is a process that stopped producing output for longer than
	// its idle bound allowed while its output streams were open. It is deliberately
	// not ProcessTimedOut: a stalled process demonstrably stopped doing anything,
	// while a timed-out one may have been working the whole time and simply ran
	// out of budget.
	ProcessStalled ProcessStatus = "stalled"
)

// ErrProcessNotStarted reports a process the operating system never began
// running: the binary could not be executed, or the sandbox it would have run
// in refused to spawn it. It is a sentinel rather than only a message because a
// caller has to tell it from a process that ran and failed — nothing was asked
// and nothing answered, so what the invocation would have produced is not
// evidence about the work but about the machine.
var ErrProcessNotStarted = errors.New("the process was never started")

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Command struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
	// RawStdout receives exact bytes instead of line-oriented, redacted output.
	// It is for repository object data, never provider output. The caller owns
	// the writer and its size bound; stderr and process budgets still apply.
	RawStdout io.Writer
	// Timeout is the total budget: how long the process may run at all,
	// whatever it is doing. It is the right and only bound for a short command
	// whose duration is known, such as a Git invocation.
	Timeout time.Duration
	// IdleTimeout bounds the gap between one line of output and the next, which
	// is a different question from the total budget: a process still producing
	// output is demonstrably working, however long it has been running, and one
	// producing nothing is stalled however recently it started. Zero or less
	// disables the check, which is what a command whose output arrives in one
	// burst at the end needs.
	// Once both output streams close, there is no output left to wait for and
	// this bound stops applying; the total and after-reply budgets still apply.
	IdleTimeout time.Duration
	// Replied reports that the process has written its final reply, and is asked
	// after each line has been handed to the observer, which is where a caller
	// reading a provider's stream finds out. Once it answers yes the turn is over:
	// the idle bound no longer applies, because a process with nothing left to say
	// is not a stalled one, and what the process goes on doing — work it started
	// in the background — is waited out to AfterReplyTimeout and then ended. Nil
	// is a command with no reply to watch for, which is every command but a
	// provider invocation.
	Replied func() bool
	// AfterReplyTimeout bounds how long a process still running after its final
	// reply is waited for before its tree is ended. Zero or less waits for it
	// under the total budget alone.
	AfterReplyTimeout time.Duration
	// AfterReplyWaiting is called once, with the account so far, when the process
	// is still running afterReplyNotice after its reply, so a caller can record
	// that it is waiting on background processes rather than on the provider. It
	// is called from the goroutine running Run, as the observer is.
	AfterReplyWaiting func(AfterReply)
	// MaxOutputBytes bounds what this runner retains of the process's output,
	// and bounds nothing else: the process runs to its own end however much it
	// says, and every line still reaches the observer. Zero is
	// defaultMaxOutputBytes.
	MaxOutputBytes int
	// OutputRecord names the durable record holding the whole of this process's
	// output, which is what the marker left in a cut copy points a reader at. A
	// caller that streams every line into a run's event log names that log; one
	// that keeps the retained copy and nothing else leaves this empty, and the
	// marker says the rest was not kept rather than sending a reader after a
	// record nobody wrote.
	OutputRecord string
	Redactor     Redactor
	// Gate, when set, starts the process behind a launch gate: it can do no
	// work until Gate.Register has recorded which process it is. A provider
	// attempt is launched this way so its record exists before its work does;
	// see launchgate.go. A platform that cannot gate a launch refuses it with
	// ErrGateUnsupported rather than running it ungated.
	Gate *LaunchGate
}

type Output struct {
	Stream    Stream
	Text      string
	Timestamp time.Time
	// LineTruncated reports a line this runner cut at maxLineBytes, whose text
	// above therefore ends in a marker and is a prefix of what the process wrote.
	// It is a field rather than something to infer from the marker because an
	// observer parsing these lines has to tell a line it cannot read from a line
	// nobody could: a cut line is invalid by construction whatever it carried,
	// and a parse failure on an intact line still means the stream itself is
	// unreadable.
	LineTruncated bool
}

type ProcessResult struct {
	Status     ProcessStatus
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Stdout     string
	Stderr     string
	// OutputClosedAt records when both output streams finished draining,
	// separately from FinishedAt: closing output does not mean the process
	// exited, and its budgets keep running while it is waited for.
	OutputClosedAt time.Time `json:",omitzero"`
	// OutputTruncation is the marker standing where the retained output above
	// was cut, naming the bound and the record holding the whole. It is empty
	// when nothing was cut, so its presence is the fact and its text is what to
	// say about it: a caller reporting the truncation into a run's record says
	// the sentence the cut copy already carries rather than composing a second
	// one. It is omitted from the record when there is nothing to say, because a
	// truncation that did not happen is not a field a reader has to interpret.
	OutputTruncation string `json:",omitempty"`
	// AfterReply is the account of a process that went on running after its
	// final reply, and is absent where it ended within afterReplyNotice of it.
	// A process ended at the bound after its reply is not a failed one: the
	// ending was this runner's, and what the invocation said is its reply.
	AfterReply *AfterReply `json:",omitempty"`
}

type OutputObserver func(Output)

type ProcessRunner interface {
	Run(ctx context.Context, command Command, observer OutputObserver) (ProcessResult, error)
}

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

type OSProcessRunner struct {
	Clock Clock
	// budget arms the total budget once the process is running, and is nil for
	// a real timer. What it returns is the channel that fires when the budget is
	// spent and a stop for a process that finished first. It is armed after
	// Start rather than before it because the exec is not the process running:
	// a budget armed ahead of a slow exec fired inside Start and came back as
	// ErrProcessNotStarted, which is an answer about the machine given to a
	// question about the work. A test supplies one it fires itself, so what
	// ends the process it started is the test's own signal rather than a guess
	// at how long a loaded machine takes to exec a helper.
	budget func(time.Duration) (<-chan time.Time, func())
	// idle is where the idle bound comes from, for the same reason: nil is a
	// real timer, and a test supplies a bound it trips or holds itself, so a
	// test about a process that keeps talking is not lost to a helper binary
	// that took longer than the bound to start.
	idle func(time.Duration) idleBound
	// afterReply arms the two clocks that start at a final reply — the notice
	// that the process has outlived it, and the bound it is waited out to — and
	// is nil for real timers, for the same reason as the two above.
	afterReply func(time.Duration) (<-chan time.Time, func())
	// outputClosed lets a test wait until the runner has drained both streams,
	// before firing a bound that must still apply while waiting for exit.
	outputClosed func()
}

// armAfterReply starts one of the clocks a final reply starts. A span of zero
// or less is a clock that never fires.
func (r OSProcessRunner) armAfterReply(span time.Duration) (<-chan time.Time, func()) {
	if span <= 0 {
		return nil, func() {}
	}
	if r.afterReply != nil {
		return r.afterReply(span)
	}
	timer := time.NewTimer(span)
	return timer.C, func() { timer.Stop() }
}

// armBudget starts the total budget's clock. Like the after-reply clocks it
// uses a runtime timer, not the wall timestamps recorded in ProcessResult:
// where the monotonic clock pauses during machine sleep, the budget pauses
// too, as the rule to wait out a sleeping laptop requires.
func (r OSProcessRunner) armBudget(timeout time.Duration) (<-chan time.Time, func()) {
	if r.budget != nil {
		return r.budget(timeout)
	}
	timer := time.NewTimer(timeout)
	return timer.C, func() { timer.Stop() }
}

func (r OSProcessRunner) Run(ctx context.Context, command Command, observer OutputObserver) (ProcessResult, error) {
	if strings.TrimSpace(command.Name) == "" {
		return ProcessResult{}, errors.New("command name is required")
	}
	clock := r.Clock
	if clock == nil {
		clock = RealClock{}
	}
	maxOutput := command.MaxOutputBytes
	if maxOutput == 0 {
		maxOutput = defaultMaxOutputBytes
	}
	if maxOutput < 0 {
		return ProcessResult{}, errors.New("max output bytes cannot be negative")
	}
	if ctx.Err() != nil {
		now := clock.Now()
		status := ProcessCancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = ProcessTimedOut
		}
		return ProcessResult{
			Status:     status,
			ExitCode:   -1,
			StartedAt:  now,
			FinishedAt: now,
		}, nil
	}

	processCtx, stopProcess := context.WithCancel(ctx)
	defer stopProcess()

	process := exec.CommandContext(processCtx, command.Name, command.Args...)
	configureProcessTree(process)
	process.Dir = command.Dir
	process.Stdin = command.Stdin
	// Every process this runner starts carries the Git maintenance fence, and no
	// caller opts out of it. What the fence is for is the Git commands the
	// harness never composes -- an agent's, a project's build tooling's -- and
	// those arrive as descendants of whatever was launched here rather than as
	// commands anybody could add options to.
	//
	// A caller that named no environment still gets this process's own beside
	// the fence, which is what it would have inherited. Nothing that launches a
	// process into somebody else's program leaves it unnamed: an invocation for
	// a run or a conversation is given ExplicitEnvironment, a Git command is
	// given GitEnvironment, and a forge command ForgeEnvironment -- each built
	// from the allowlist, so nothing the harness's environment happened to carry
	// reaches an agent's tree or a hook the repository supplies.
	process.Env = WithGitMaintenanceFence(command.Env)
	stdout, err := process.StdoutPipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := process.StderrPipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create stderr pipe: %w", err)
	}

	var gate *gatedProcess
	if command.Gate != nil {
		gate, err = prepareGate(process, command.Name, command.Args, command.Gate)
		if err != nil {
			return ProcessResult{ExitCode: -1}, fmt.Errorf("%w: start %q behind its launch gate: %w", ErrProcessNotStarted, command.Name, err)
		}
	}

	result := ProcessResult{StartedAt: clock.Now(), ExitCode: -1}
	if err := process.Start(); err != nil {
		if gate != nil {
			gate.abandon(process)
		}
		return result, fmt.Errorf("%w: start %q: %w", ErrProcessNotStarted, command.Name, err)
	}
	if gate != nil {
		gate.started(process)
		registered := command.Gate.Register(StartedProcess{PID: process.Process.Pid, ProcessGroup: processGroupOf(process), StartedAt: result.StartedAt})
		if registered != nil {
			// The launch could not be written down, so it must not run: the gate
			// stays shut and the waiting shell is ended. Nothing it would have
			// run has begun, which is what ErrProcessNotStarted says.
			gate.refuse()
			stopProcess()
			_ = process.Wait()
			reapProcessTree(process)
			result.FinishedAt = clock.Now()
			result.Status = ProcessFailed
			return result, fmt.Errorf("%w: register the launch of %q: %w", ErrProcessNotStarted, command.Name, registered)
		}
		gate.release()
	}
	// The budget's clock starts here, with the process running, and never
	// earlier: see OSProcessRunner.budget for what arming it before Start cost.
	// A nil channel never fires, so a command with no budget needs no case of
	// its own below.
	var spent <-chan time.Time
	if command.Timeout > 0 {
		var stopBudget func()
		spent, stopBudget = r.armBudget(command.Timeout)
		defer stopBudget()
	}

	outputs := make(chan Output)
	scanErrors := make(chan error, 2)
	var scanners sync.WaitGroup
	scanners.Add(2)
	if command.RawStdout == nil {
		go scanOutput(stdout, StreamStdout, clock, command.Redactor, outputs, scanErrors, stopProcess, &scanners)
	} else {
		go func() {
			defer scanners.Done()
			if _, err := io.Copy(command.RawStdout, stdout); err != nil {
				scanErrors <- err
				stopProcess()
			}
		}()
	}
	go scanOutput(stderr, StreamStderr, clock, command.Redactor, outputs, scanErrors, stopProcess, &scanners)
	go func() {
		scanners.Wait()
		close(outputs)
		close(scanErrors)
	}()

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	outputBytes := 0
	stdoutTruncated := false
	stderrTruncated := false
	stalled := false
	timedOut := false
	idle := r.armIdle(command.IdleTimeout)
	defer idle.stop()
	// The final reply, once it arrives, ends the turn: from then on the idle
	// bound is out of the select, and what bounds the process is the wait below.
	var repliedAt time.Time
	var noticed, afterBound <-chan time.Time
	stopNotice, stopAfterBound := func() {}, func() {}
	defer func() { stopNotice(); stopAfterBound() }()
	waitingAfterReply := false
	endedAfterReply := false
	linesAfterReply := 0
	afterReplySoFar := func() AfterReply {
		account := AfterReply{RepliedAt: repliedAt, Lines: linesAfterReply}
		if command.AfterReplyTimeout > 0 {
			account.BoundSeconds = int64(command.AfterReplyTimeout / time.Second)
		}
		return account
	}
	var waited <-chan error
	var waitErr error
drain:
	for {
		idleExpired := idle.expired()
		if outputs == nil || !repliedAt.IsZero() {
			idleExpired = nil
		}
		select {
		case waitErr = <-waited:
			break drain
		case output, received := <-outputs:
			if !received {
				outputs = nil
				idle.stop()
				result.OutputClosedAt = clock.Now()
				// Wait may only begin after all pipe reads have finished: it
				// closes the pipes itself. Keep watching the same deadlines
				// until it returns, because EOF alone proves nothing about exit.
				finished := make(chan error, 1)
				waited = finished
				go func() { finished <- process.Wait() }()
				if r.outputClosed != nil {
					r.outputClosed()
				}
				continue
			}
			// Any line at all is proof the process is still doing something, so
			// the idle bound starts over from here rather than from the start.
			idle.reset()
			lineBytes := len(output.Text) + 1
			// A stream that has already lost a line keeps losing them, so what
			// is retained of it is a prefix ending where its marker says rather
			// than a copy with a hole in the middle where a later short line
			// happened to fit. The budget itself is shared, so a chatty stdout
			// can still spend all of it; what is tracked per stream is which
			// ones actually lost something, because a marker on a stream that
			// stayed whole would report a truncation that never happened to it.
			cut := stdoutTruncated
			if output.Stream == StreamStderr {
				cut = stderrTruncated
			}
			switch {
			case cut || outputBytes+lineBytes > maxOutput:
				// The bound is on what this runner keeps in memory and on
				// nothing else. A process is never killed for being verbose and
				// its output is never an error: a run that bursts stdout
				// diagnosing something is a run doing its work, and failing it
				// for that loses the work and the account of what it cost.
				if output.Stream == StreamStdout {
					stdoutTruncated = true
				} else {
					stderrTruncated = true
				}
			case output.Stream == StreamStdout:
				outputBytes += lineBytes
				stdoutBuffer.WriteString(output.Text)
				stdoutBuffer.WriteByte('\n')
			default:
				outputBytes += lineBytes
				stderrBuffer.WriteString(output.Text)
				stderrBuffer.WriteByte('\n')
			}
			// The observer is called for every line, past the bound as much as
			// under it. It is what carries output into the durable record the
			// marker points at, so stopping here would cut the whole as well as
			// the copy -- and for a provider stream it would drop the terminal
			// result the invocation's own cost is read from.
			if observer != nil {
				observer(output)
			}
			switch {
			case !repliedAt.IsZero():
				linesAfterReply++
			case command.Replied != nil && command.Replied():
				repliedAt = output.Timestamp
				if repliedAt.IsZero() {
					repliedAt = clock.Now()
				}
				idle.stop()
				noticed, stopNotice = r.armAfterReply(afterReplyNotice)
				afterBound, stopAfterBound = r.armAfterReply(command.AfterReplyTimeout)
			}
		case <-noticed:
			// The process is still here well after its reply, so what is keeping
			// it is work of its own rather than the provider: say so once, to a
			// caller that records it, and carry on waiting.
			noticed = nil
			waitingAfterReply = true
			if command.AfterReplyWaiting != nil {
				command.AfterReplyWaiting(afterReplySoFar())
			}
		case <-afterBound:
			// The wait a finished turn is given has run out. The tree is ended and
			// the drain carries on until both pipes close, so whatever the
			// background work said on its way down is still reported.
			afterBound = nil
			noticed = nil
			endedAfterReply = true
			stopProcess()
		case <-idleExpired:
			// Nothing has been produced for the whole idle bound, so the process
			// is not working on anything this runner can see. Terminate the tree
			// and keep draining until both pipes close, so whatever it did say
			// before it went quiet is still reported.
			stalled = true
			stopProcess()
			idle.stop()
		case <-spent:
			// The process has had all the time it was given, whatever it was
			// doing with it. The tree is terminated and the drain carries on
			// until both pipes close, as for a stall; the channel is put out of
			// the select so a budget that has fired is not read again.
			//
			// A process that has already written its final reply is not one that
			// ran out of time on its work: the turn was over, and what the budget
			// ended is the wait after it.
			if repliedAt.IsZero() {
				timedOut = true
			} else {
				endedAfterReply = true
				noticed, afterBound = nil, nil
			}
			stopProcess()
			spent = nil
		}
	}

	// Nothing this command spawned outlives it, whatever became of the command:
	// the group goes here on the succeeding path as much as on the failing one.
	// A command that exits 0 having backgrounded work is the case that costs,
	// because every other signal in this result reads as a clean finish.
	reapProcessTree(process)
	result.FinishedAt = clock.Now()
	if waitingAfterReply || endedAfterReply {
		account := afterReplySoFar()
		account.Outcome = AfterReplyExited
		if endedAfterReply {
			account.Outcome = AfterReplyEnded
		}
		if waited := result.FinishedAt.Sub(repliedAt); waited > 0 {
			account.WaitedSeconds = int64(waited / time.Second)
		}
		result.AfterReply = &account
	}
	if stdoutTruncated || stderrTruncated {
		result.OutputTruncation = truncationMarker(maxOutput, command.OutputRecord)
		// The marker goes into each stream that lost a line, so whichever of the
		// two a reader has in front of them says it is a cut copy. A stream that
		// stayed whole gains nothing, because a marker on it would report a
		// truncation that did not happen to it.
		if stdoutTruncated {
			stdoutBuffer.WriteString(result.OutputTruncation)
			stdoutBuffer.WriteByte('\n')
		}
		if stderrTruncated {
			stderrBuffer.WriteString(result.OutputTruncation)
			stderrBuffer.WriteByte('\n')
		}
	}
	result.Stdout = stdoutBuffer.String()
	result.Stderr = stderrBuffer.String()
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}

	var readErrors []error
	for scanErr := range scanErrors {
		if scanErr != nil {
			readErrors = append(readErrors, scanErr)
		}
	}
	if len(readErrors) > 0 {
		return result, errors.Join(readErrors...)
	}

	switch {
	case stalled:
		// The runner stopped this process itself, so the kill it observes is its
		// own and says nothing about what the process was doing.
		result.Status = ProcessStalled
	case endedAfterReply:
		// The same is true of a process ended after its final reply, and more:
		// the turn it was running was over, so the kill ends background work
		// rather than the invocation, and says nothing about either.
		result.Status = ProcessSucceeded
	case timedOut || errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Status = ProcessTimedOut
	case errors.Is(ctx.Err(), context.Canceled):
		result.Status = ProcessCancelled
	case waitErr != nil:
		result.Status = ProcessFailed
	default:
		result.Status = ProcessSucceeded
	}
	return result, nil
}

// EventLogOf names a run's event log as a reader would have to name it to go
// and find it. It is what a caller streaming a process's output into that log
// puts on Command.OutputRecord, so the marker in a cut copy sends a reader to
// the file that has the rest.
func EventLogOf(runID string) string {
	if strings.TrimSpace(runID) == "" {
		return ""
	}
	return "the event log of " + runID
}

// truncationMarker is the line that stands where a retained copy of a process's
// output was cut. It names the bound it was cut at and the record holding the
// whole, because a reader who cannot tell a cut copy from a complete one reads
// the cut one as everything the process said.
//
// A caller that named no record has the absence said plainly instead. Pointing
// a reader at "the durable record" where nothing kept one would send them
// looking for a file nobody wrote, which is worse than saying the rest is gone.
func truncationMarker(maxOutput int, record string) string {
	if strings.TrimSpace(record) == "" {
		return fmt.Sprintf("[output truncated at %d bytes; the rest was not retained]", maxOutput)
	}
	return fmt.Sprintf("[output truncated at %d bytes; the whole of it is in %s]", maxOutput, record)
}

// idleBound bounds the gap between one line of process output and the next. It
// is the whole of the runner's liveness signal: a process that keeps producing
// output keeps resetting it, and only one that produces nothing at all trips it.
type idleBound interface {
	// expired reports the bound elapsing. A disabled bound returns a nil
	// channel, which never fires, so a caller selecting on it needs no special
	// case.
	expired() <-chan time.Time
	// reset starts the bound over, which every line of output does.
	reset()
	// stop disables the bound for good. It is idempotent, so a bound that has
	// already tripped can still be cleaned up by the caller's deferred stop.
	stop()
}

// armIdle starts the idle bound's clock.
func (r OSProcessRunner) armIdle(timeout time.Duration) idleBound {
	if r.idle != nil {
		return r.idle(timeout)
	}
	return newIdleWatch(timeout)
}

// idleWatch is the idle bound as a real timer.
type idleWatch struct {
	timeout time.Duration
	timer   *time.Timer
}

func newIdleWatch(timeout time.Duration) *idleWatch {
	watch := &idleWatch{timeout: timeout}
	if timeout > 0 {
		watch.timer = time.NewTimer(timeout)
	}
	return watch
}

func (w *idleWatch) expired() <-chan time.Time {
	if w.timer == nil {
		return nil
	}
	return w.timer.C
}

func (w *idleWatch) reset() {
	if w.timer == nil {
		return
	}
	w.timer.Reset(w.timeout)
}

func (w *idleWatch) stop() {
	if w.timer == nil {
		return
	}
	w.timer.Stop()
	w.timer = nil
}

func scanOutput(reader io.Reader, stream Stream, clock Clock, redactor Redactor, outputs chan<- Output, scanErrors chan<- error, stopProcess context.CancelFunc, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()

	lines := bufio.NewReaderSize(reader, 64*1024)
	for {
		line, dropped, err := readLine(lines, maxLineBytes)
		// A line that ended at its own newline is a line however empty it is, so
		// a blank one is still reported. Only the end of the stream can leave
		// nothing at all, and that is the one case with no line to report.
		if err == nil || len(line) > 0 || dropped > 0 {
			if dropped > 0 {
				// Redaction replaces whole values, so a secret straddling the cut
				// would have its leading half kept verbatim — the one thing cutting
				// a line could put into the record that killing the process never
				// did. Giving back the longest secret's worth of bytes removes the
				// case, and against a bound of a megabyte it costs the cut copy
				// nothing.
				if held := redactor.longest(); held > 0 && len(line) > held {
					line = line[:len(line)-held]
					dropped += held
				}
			}
			text := redactor.Redact(string(line))
			if dropped > 0 {
				text += lineTruncationMarker(maxLineBytes, dropped)
			}
			outputs <- Output{
				Stream:        stream,
				Text:          text,
				Timestamp:     clock.Now(),
				LineTruncated: dropped > 0,
			}
		}
		switch {
		case err == nil:
		case errors.Is(err, io.EOF):
			return
		default:
			// A reader that stops draining can leave the child blocked on a full
			// pipe. Terminate the process tree immediately so the other stream
			// closes and Run can return the read failure without waiting for timeout.
			stopProcess()
			scanErrors <- fmt.Errorf("read %s: %w", stream, err)
			return
		}
	}
}

// readLine reads one line of process output, bounded. It returns what fits
// within the bound, how many bytes past it were drained and discarded, and the
// error that ended the read — nil when the line ended at its own newline, io.EOF
// at the end of the stream, and anything else a genuine read failure.
//
// The tail past the bound is read and thrown away rather than left in the pipe,
// which is what separates truncating a line from refusing to read it: a reader
// that stops draining is a child blocked on a full pipe.
func readLine(reader *bufio.Reader, bound int) ([]byte, int, error) {
	var kept []byte
	dropped := 0
	for {
		chunk, err := reader.ReadSlice('\n')
		// ErrBufferFull is a line longer than the reader's own buffer, which is
		// an instruction to come back for the rest of the same line rather than a
		// failure. It is the only error that does not end the line.
		partial := errors.Is(err, bufio.ErrBufferFull)
		if !partial {
			chunk = dropLineEnding(chunk)
		}
		if room := bound - len(kept); room > 0 {
			take := len(chunk)
			if take > room {
				take = room
			}
			kept = append(kept, chunk[:take]...)
			dropped += len(chunk) - take
		} else {
			dropped += len(chunk)
		}
		if partial {
			continue
		}
		return kept, dropped, err
	}
}

// dropLineEnding removes the terminator, so what a line reports is the text the
// process wrote and not the newline it ended with. A carriage return before it
// goes too, which is what the line splitter this replaced always did.
func dropLineEnding(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte{'\n'})
	return bytes.TrimSuffix(line, []byte{'\r'})
}

// lineTruncationMarker ends a line this runner had to cut. Unlike the marker for
// the retained copy as a whole, it names no record holding the rest, because
// there is none: the observer is handed the same cut line, so the bytes past the
// bound reached nothing. Saying so plainly is the point — a reader who cannot
// tell a cut line from a whole one reads the cut one as everything the process
// said on it.
func lineTruncationMarker(bound int, dropped int) string {
	return fmt.Sprintf("…[line truncated at %d bytes; %d further bytes were not retained]", bound, dropped)
}

type Redactor struct {
	values []string
}

func NewRedactor(values ...string) Redactor {
	seen := make(map[string]struct{})
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		for _, candidate := range redactionCandidates(value) {
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			filtered = append(filtered, candidate)
		}
	}
	// Replace longer overlapping values first. Otherwise redacting "token"
	// before "token-suffix" would persist "[REDACTED]-suffix".
	sort.SliceStable(filtered, func(i, j int) bool {
		return len(filtered[i]) > len(filtered[j])
	})
	return Redactor{values: filtered}
}

func redactionCandidates(value string) []string {
	if value == "" {
		return nil
	}
	candidates := []string{value}
	if !strings.ContainsAny(value, "\r\n") {
		return candidates
	}
	// Output is scanned one line at a time. Retain the complete value for
	// other call sites and also protect every non-empty line independently.
	for _, line := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '\r' || r == '\n'
	}) {
		if line != value {
			candidates = append(candidates, line)
		}
	}
	return candidates
}

// SensitiveEnvironmentValues returns values held in conventionally sensitive
// environment variables. Provider authentication remains CLI-managed, but
// subprocess output still needs these values removed before it becomes a
// durable event or result.
func SensitiveEnvironmentValues(environment []string) []string {
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || value == "" || !sensitiveEnvironmentName(name) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func sensitiveEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{
		"TOKEN",
		"PASSWORD",
		"PASSWD",
		"API_KEY",
		"PRIVATE_KEY",
		"CLIENT_SECRET",
		"ACCESS_KEY",
		"CREDENTIAL",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_SECRET")
}

// longest is the length of the longest value this redactor replaces, and zero
// when it replaces nothing. It is what a caller cutting a line short holds back
// to be sure it is not cutting through a secret; the values are already sorted
// longest first, so the first one is it.
func (r Redactor) longest() int {
	if len(r.values) == 0 {
		return 0
	}
	return len(r.values[0])
}

func (r Redactor) Redact(value string) string {
	for _, secret := range r.values {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}
