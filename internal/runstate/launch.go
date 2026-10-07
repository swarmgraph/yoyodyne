package runstate

// Launching a provider attempt so that a crash at any moment leaves a record
// recovery can act on without starting a second execution beside the first
// (docs/designs/claude-execution-and-account-routing.md, "Durable transition
// and execution records" and "Restart and uncertain outcomes").
//
// The order is fixed, and each step is durable before the next begins:
//
//  1. The attempt is reserved (RunRouting.PrepareAttempt), naming it before
//     anything runs.
//  2. BeginLaunch takes the attempt's hold: a file in the run state directory,
//     locked, that the process and everything it starts inherit. The lock is
//     released only when the last process holding the file exits.
//  3. The process is started behind a gate (execution.LaunchGate), as a shell
//     that cannot run the provider until the gate opens.
//  4. Its execution identity is registered on the attempt.
//  5. The attempt is marked launched.
//  6. The gate opens and the provider begins.
//
// So a record that says what happened is never behind what happened. An
// attempt with no registered execution never began work, because nothing opens
// the gate before registration and a harness that dies closes the gate by
// dying. An attempt registered but not marked launched never began either, for
// the same reason, and is launched again under the same reserved identity once
// its waiting shell has gone. An attempt marked launched may have begun, and is
// observed rather than assumed: its hold says whether any process of its tree
// is still alive, and its process group whether any process that left the hold
// behind still is. Nothing about a process identifier on its own is trusted to
// say an execution stopped, because the number may since belong to something
// else and a process the provider started can outlive it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// executionState is what observing an execution found.
type executionState int

const (
	// executionUnknown is an execution nothing here can establish the state
	// of. It is never read as stopped.
	executionUnknown executionState = iota
	executionAlive
	executionStopped
)

// holdName is the file name an attempt's hold has, beside the run's id, in the
// run state directory.
func holdName(attemptID string) string {
	return attemptID + ".hold"
}

// attemptOfHold is the attempt a hold's file name belongs to.
func attemptOfHold(hold string) string {
	return strings.TrimSuffix(hold, ".hold")
}

func (s *Store) holdPath(runID, attemptID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	if !routingIDPattern.MatchString(attemptID) {
		return "", fmt.Errorf("attempt identity %q is invalid", attemptID)
	}
	return filepath.Join(s.root, runID+"."+holdName(attemptID)), nil
}

// holdFile is an attempt's hold opened through the run state directory pinned
// against replacement, as every harness-owned write is
// (docs/decisions/invariants, repository-writes-are-physically-confined): a
// link planted at its name, or a directory swapped in on the way to it, is
// refused rather than followed. The directory stays pinned until close, so
// removing the file afterwards removes the same name in the same directory.
type holdFile struct {
	file *os.File
	root *repowrite.PinnedRoot
	name string
}

// pinHolds pins the run state directory and names an attempt's hold in it.
func (s *Store) pinHolds(runID, attemptID string) (*repowrite.PinnedRoot, string, error) {
	path, err := s.holdPath(runID, attemptID)
	if err != nil {
		return nil, "", err
	}
	stateRoot, anchor, err := confinedStateRoot(s.root)
	if err != nil {
		return nil, "", err
	}
	root, err := pinStateRoot(stateRoot, anchor)
	if err != nil {
		return nil, "", err
	}
	return root, filepath.Base(path), nil
}

// openHold opens an attempt's existing hold. One that does not exist is
// reported as os.ErrNotExist, and one that is not a regular file is refused.
func (s *Store) openHold(runID, attemptID string) (holdFile, error) {
	root, name, err := s.pinHolds(runID, attemptID)
	if err != nil {
		return holdFile{}, err
	}
	file, err := openRegular(root, name)
	if err != nil {
		root.Close()
		return holdFile{}, err
	}
	return holdFile{file: file, root: root, name: name}, nil
}

func openRegular(root *repowrite.PinnedRoot, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	return root.OpenLock(name, 0o600)
}

// createHold makes a new hold for one launch of an attempt and returns it
// locked, with the random mark written into it. The mark is what tells this
// file from any put at the same name later: a file system may give a file
// created after another was removed the removed file's inode, so only what the
// file says identifies it. A hold left by an earlier launch is removed first,
// once its lock shows nothing still has it.
func (s *Store) createHold(runID, attemptID string) (*os.File, string, error) {
	root, name, err := s.pinHolds(runID, attemptID)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	switch earlier, err := openRegular(root, name); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, "", err
	default:
		taken, err := tryLockStateFile(earlier)
		if err != nil || !taken {
			earlier.Close()
			if err != nil {
				return nil, "", err
			}
			return nil, "", conflict("a process from an earlier launch of attempt %s is still running", attemptID)
		}
		removed := root.Remove(name)
		_ = releaseStateFile(earlier)
		if removed != nil && !errors.Is(removed, os.ErrNotExist) {
			return nil, "", removed
		}
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return nil, "", fmt.Errorf("mark the file of attempt %s: %w", attemptID, err)
	}
	mark := hex.EncodeToString(bytes)
	if err := root.CreateFile(name, []byte(mark), 0o600); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, "", conflict("another launch of attempt %s is starting", attemptID)
		}
		return nil, "", err
	}
	file, err := openRegular(root, name)
	if err != nil {
		return nil, "", err
	}
	taken, err := tryLockStateFile(file)
	if err == nil && !taken {
		err = conflict("another launch of attempt %s is starting", attemptID)
	}
	if err == nil {
		if found, readErr := readHoldMark(file); readErr != nil || found != mark {
			err = conflict("another launch of attempt %s is starting", attemptID)
		}
	}
	if err != nil {
		file.Close()
		return nil, "", err
	}
	return file, mark, nil
}

// readHoldMark is the mark a hold carries.
func readHoldMark(file *os.File) (string, error) {
	buffer := make([]byte, 128)
	read, err := file.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(buffer[:read]), nil
}

// remove removes the hold's name from the pinned directory.
func (h holdFile) remove() {
	_ = h.root.Remove(h.name)
}

// close lets go of the pinned directory; the file is the caller's.
func (h holdFile) close() {
	_ = h.root.Close()
}

// launcherGeneration identifies this harness process for as long as it runs,
// so an execution's record says which launcher started it even after that
// launcher's process identifier is reused.
var launcherGeneration = sync.OnceValue(func() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("launcher-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("launcher-%d-%s", os.Getpid(), hex.EncodeToString(bytes))
})

// launchPoint names a moment inside a launch, for a test that stops the
// launcher there.
type launchPoint string

const (
	launchHoldTaken  launchPoint = "hold_taken"
	launchRegistered launchPoint = "registered"
	launchMarked     launchPoint = "marked_launched"
	launchExited     launchPoint = "exited"
)

// AttemptLaunch is one launch of a reserved attempt, from taking its hold to
// recording how it ended. The caller puts Gate on the command that starts the
// provider and calls Finish once that command has returned.
type AttemptLaunch struct {
	store     *Store
	ctx       context.Context
	runID     string
	operation string
	attempt   string
	hold      *os.File
	holdFile  string
	// registered is the execution this launch wrote down, and launched whether
	// it went on to mark the attempt launched.
	registered *ExecutionIdentity
	launched   bool
	// pause is a test's way of stopping the launcher at a launch point; it is
	// nil in every launch the harness makes.
	pause func(launchPoint)
}

// BeginLaunch takes the hold of a reserved attempt that has not been
// launched, ready to start it behind a gate. The caller holds the run's lease.
//
// It is refused for an attempt with a registered execution, which recovery has
// to settle first (ReconcileLaunch); for an operation recovery is waiting on;
// for one with an external effect nobody has established, which a launch could
// repeat; and for an attempt whose hold a process from an earlier launch still
// has, which is the same launch still on its way out.
func (s *Store) BeginLaunch(ctx context.Context, runID, operationID, attemptID string) (*AttemptLaunch, error) {
	state, err := s.Load(runID)
	if err != nil {
		return nil, err
	}
	operation, attempt, err := state.Routing.find(operationID, attemptID)
	if err != nil {
		return nil, err
	}
	switch {
	case attempt.State != AttemptPrepared:
		return nil, conflict("attempt %s is %s and is not launched again", attemptID, attempt.State)
	case attempt.Execution != nil:
		return nil, conflict("attempt %s already registered an execution; reconcile it before launching again", attemptID)
	case operation.Reconciling != nil:
		return nil, conflict("operation %s is waiting on recovery: %s", operationID, operation.Reconciling.Reason)
	case len(operation.pendingEffects()) > 0:
		return nil, conflict("operation %s has effect %s that nobody has established as performed or absent", operationID, operation.pendingEffects()[0].Key)
	}
	hold, mark, err := s.createHold(runID, attemptID)
	if err != nil {
		return nil, fmt.Errorf("prepare the file that shows whether attempt %s's processes are alive: %w", attemptID, err)
	}
	return &AttemptLaunch{store: s, ctx: ctx, runID: runID, operation: operationID, attempt: attemptID, hold: hold, holdFile: mark}, nil
}

// Gate is the launch gate to start the attempt's provider behind.
func (l *AttemptLaunch) Gate() *execution.LaunchGate {
	l.stop(launchHoldTaken)
	return &execution.LaunchGate{Hold: l.hold, Register: l.register}
}

func (l *AttemptLaunch) stop(point launchPoint) {
	if l.pause != nil {
		l.pause(point)
	}
}

// register writes the started process down and marks the attempt launched,
// both before the gate opens. A failure of either leaves the gate shut.
func (l *AttemptLaunch) register(process execution.StartedProcess) error {
	machine, err := l.store.machine()
	if err != nil {
		return fmt.Errorf("identify this machine for attempt %s: %w", l.attempt, err)
	}
	if machine.Host == "" {
		return fmt.Errorf("name this host for attempt %s: the operating system gave no host name", l.attempt)
	}
	identity := ExecutionIdentity{
		Machine: machine.Machine, Host: machine.Host, Boot: currentBoot(), Launcher: launcherGeneration(),
		PID: process.PID, ProcessGroup: process.ProcessGroup, StartedAt: process.StartedAt,
		Hold: holdName(l.attempt), HoldFile: l.holdFile, RegisteredAt: time.Now(),
	}
	if _, err := l.store.updateRoutingFresh(l.ctx, l.runID, func(routing *RunRouting) (bool, error) {
		return routing.RegisterExecution(l.operation, l.attempt, identity)
	}); err != nil {
		return err
	}
	l.registered = &identity
	l.stop(launchRegistered)
	if _, err := l.store.updateRoutingFresh(l.ctx, l.runID, func(routing *RunRouting) (bool, error) {
		return routing.MarkLaunched(l.operation, l.attempt, time.Now())
	}); err != nil {
		return err
	}
	l.launched = true
	l.stop(launchMarked)
	return nil
}

// Finish records how the launch ended once the command that started it has
// returned, and says whether its execution is confirmed stopped. The caller
// classifies the ending; whether the tree stopped is observed here, so an
// ending is recorded as confirmed only when no process of the tree remains.
//
// A launch whose gate never opened records no ending: the attempt is still
// reserved, and is launched again under the same identity once its waiting
// shell is confirmed gone, here or by ReconcileLaunch.
func (l *AttemptLaunch) Finish(ending AttemptEnding) (Termination, error) {
	// Run closes the hold once the process holds it; a command that never
	// started leaves it to be closed here, and closing it twice is harmless.
	_ = l.hold.Close()
	l.stop(launchExited)
	if l.registered == nil {
		return TerminationConfirmed, nil
	}
	state, reason, settle := l.store.observeExecution(l.runID, *l.registered)
	defer settle(false)
	if !l.launched {
		if state != executionStopped {
			return TerminationUncertain, nil
		}
		if _, err := l.store.updateRoutingFresh(l.ctx, l.runID, func(routing *RunRouting) (bool, error) {
			return routing.ReleaseUnlaunched(l.operation, l.attempt)
		}); err != nil {
			return TerminationUncertain, err
		}
		settle(true)
		return TerminationConfirmed, nil
	}
	ending.Termination = TerminationConfirmed
	if state != executionStopped {
		ending.Termination = TerminationUncertain
		if ending.Result == "" {
			ending.Result = reason
		}
	}
	if ending.At.IsZero() {
		ending.At = time.Now()
	}
	if _, err := l.store.updateRoutingFresh(l.ctx, l.runID, func(routing *RunRouting) (bool, error) {
		return routing.EndAttempt(l.operation, l.attempt, ending)
	}); err != nil {
		return ending.Termination, err
	}
	settle(ending.Termination == TerminationConfirmed)
	return ending.Termination, nil
}

// LaunchVerdict is what reconciling an operation's launch found.
type LaunchVerdict string

const (
	// LaunchIdle is an operation with nothing that may still be executing: the
	// caller may prepare its next attempt, or its switch's destination.
	LaunchIdle LaunchVerdict = "idle"
	// LaunchRunning is an execution that is still alive. It is observed until
	// it stops; nothing is launched beside it.
	LaunchRunning LaunchVerdict = "running"
	// LaunchFinished is an attempt whose result is recorded and whose
	// execution is confirmed stopped. It is adopted rather than launched again.
	LaunchFinished LaunchVerdict = "finished"
	// LaunchNeverStarted is a reserved attempt that demonstrably began no
	// work. It is launched again under the same identity.
	LaunchNeverStarted LaunchVerdict = "never_started"
	// LaunchInterrupted is an attempt that was launched and is confirmed
	// stopped, with no result anybody recorded. It is ended as interrupted, and
	// what follows it is a new attempt under the same operation.
	LaunchInterrupted LaunchVerdict = "interrupted"
	// LaunchUncertain is an execution, or an external effect, nothing here can
	// establish the state of. Recovery waits, recording why, and launches
	// nothing and spends nothing.
	LaunchUncertain LaunchVerdict = "uncertain"
)

// InterruptedClassification is how an attempt that was launched and stopped
// with no recorded result is ended.
const InterruptedClassification = "interrupted"

// LaunchReconciliation is what ReconcileLaunch found and recorded.
type LaunchReconciliation struct {
	Verdict LaunchVerdict
	// Attempt is the attempt the verdict is about, where there is one.
	Attempt string
	// Execution is the execution observed, where one was registered.
	Execution *ExecutionIdentity
	// Ending is the attempt's ending, for a finished attempt.
	Ending *AttemptEnding
	// Reason says why recovery waits, for a running or uncertain verdict.
	Reason string
}

// LaunchRecovery is what reconciliation is told by the caller that knows the
// provider and the forge.
type LaunchRecovery struct {
	// Outcome returns the durable result an attempt's execution left behind —
	// its terminal event, read from the run's event log — or nil where it left
	// none. It is asked only of an attempt confirmed stopped.
	Outcome func(InvocationAttempt) (*AttemptEnding, error)
	// Effect establishes whether an intended external effect happened. known
	// is false where it cannot be told yet. Nil establishes nothing.
	Effect func(ExternalEffect) (performed, known bool, err error)
}

// ReconcileLaunch settles an operation's launch after a restart, before
// anything else is launched for it. The caller holds the run's lease, so no
// other process is launching for the run; every change goes through the run's
// serialized routing writer all the same, so two recoveries that both got this
// far still record one answer.
//
// An execution still alive is reported running and adopted as it is. One
// confirmed stopped with a recorded result is finished; one that was launched
// and stopped with none is ended as interrupted; a reserved attempt that never
// began is reported for launching again under the same identity. Anything
// nothing can establish — an execution that may be alive, an external effect
// that may have happened — is recorded as what recovery waits on, and nothing
// is launched, released, or spent for it: a selection, a switch allowance, and
// every counter are left exactly as they were.
func (s *Store) ReconcileLaunch(ctx context.Context, runID, operationID string, recovery LaunchRecovery) (LaunchReconciliation, error) {
	state, err := s.Load(runID)
	if err != nil {
		return LaunchReconciliation{}, err
	}
	operation, ok := state.Routing.Operation(operationID)
	if !ok {
		return LaunchReconciliation{}, conflict("operation %s is not recorded", operationID)
	}
	if operation.Completed != nil {
		return LaunchReconciliation{Verdict: LaunchIdle}, nil
	}
	attempt, executing := operation.executingAttempt()
	if !executing {
		return s.settleIdle(ctx, runID, *operation, recovery, LaunchReconciliation{Verdict: LaunchIdle})
	}
	found := LaunchReconciliation{Attempt: attempt.ID, Execution: attempt.Execution}
	if attempt.Execution == nil {
		if attempt.State == AttemptPrepared {
			// Nothing opens the gate before registration, so a reserved attempt
			// with none registered began no work.
			found.Verdict = LaunchNeverStarted
			return s.settleIdle(ctx, runID, *operation, recovery, found)
		}
		return s.wait(ctx, runID, operationID, found, LaunchUncertain,
			fmt.Sprintf("attempt %s was launched with no registered execution, so nothing can establish that it stopped", attempt.ID))
	}
	observed, reason, settle := s.observeExecution(runID, *attempt.Execution)
	defer settle(false)
	switch observed {
	case executionAlive:
		return s.wait(ctx, runID, operationID, found, LaunchRunning, reason)
	case executionUnknown:
		return s.wait(ctx, runID, operationID, found, LaunchUncertain, reason)
	}
	switch attempt.State {
	case AttemptPrepared:
		if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
			return routing.ReleaseUnlaunched(operationID, attempt.ID)
		}); err != nil {
			return LaunchReconciliation{}, err
		}
		settle(true)
		found.Verdict = LaunchNeverStarted
		return s.settleIdle(ctx, runID, *operation, recovery, found)
	case AttemptLaunched:
		ending := AttemptEnding{
			Classification: InterruptedClassification, Termination: TerminationConfirmed, At: time.Now(),
			Result: "the harness stopped before recording this attempt's result",
		}
		found.Verdict = LaunchInterrupted
		if recovery.Outcome != nil {
			recorded, err := recovery.Outcome(*attempt)
			if err != nil {
				return s.wait(ctx, runID, operationID, found, LaunchUncertain,
					fmt.Sprintf("the result attempt %s left could not be read: %v", attempt.ID, err))
			}
			if recorded != nil {
				ending = *recorded
				ending.Termination = TerminationConfirmed
				if ending.At.IsZero() {
					ending.At = time.Now()
				}
				found.Verdict = LaunchFinished
			}
		}
		if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
			return routing.EndAttempt(operationID, attempt.ID, ending)
		}); err != nil {
			return LaunchReconciliation{}, err
		}
		found.Ending = &ending
	default:
		// Ended with its stop unconfirmed: the stop is confirmed now, and the
		// ending recorded then is the result.
		ending := *attempt.Ended
		ending.Termination = TerminationConfirmed
		if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
			return routing.EndAttempt(operationID, attempt.ID, ending)
		}); err != nil {
			return LaunchReconciliation{}, err
		}
		found.Verdict = LaunchFinished
		found.Ending = &ending
	}
	settle(true)
	state, err = s.Load(runID)
	if err != nil {
		return LaunchReconciliation{}, err
	}
	operation, _ = state.Routing.Operation(operationID)
	return s.settleIdle(ctx, runID, *operation, recovery, found)
}

// settleIdle finishes a reconciliation that found nothing executing: a switch
// whose source is now confirmed stopped is advanced, and every external effect
// nobody has established is settled through the caller or waited on. Only then
// is the verdict, which may let the caller launch, returned.
func (s *Store) settleIdle(ctx context.Context, runID string, operation RoutedOperation, recovery LaunchRecovery, found LaunchReconciliation) (LaunchReconciliation, error) {
	if sw := operation.Switch; sw != nil && sw.Progress == TransitionPlanned {
		if source, ok := operation.attempt(sw.SourceAttempt); ok && !source.mayBeExecuting() {
			if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
				return routing.ReconcileSource(operation.ID, sw.ID, time.Now())
			}); err != nil {
				return LaunchReconciliation{}, err
			}
		}
	}
	for _, effect := range operation.pendingEffects() {
		performed, known := false, false
		var err error
		if recovery.Effect != nil {
			performed, known, err = recovery.Effect(effect)
		}
		if err != nil || !known {
			reason := fmt.Sprintf("nobody has established whether %s %s happened, and it is not repeated until somebody has", effect.Kind, effect.Key)
			if err != nil {
				reason = fmt.Sprintf("whether %s %s happened could not be established: %v", effect.Kind, effect.Key, err)
			}
			found.Reason = reason
			return s.wait(ctx, runID, operation.ID, found, LaunchUncertain, reason)
		}
		if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
			return routing.SettleEffect(operation.ID, effect.Key, performed, time.Now())
		}); err != nil {
			return LaunchReconciliation{}, err
		}
	}
	if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
		return routing.ClearReconciling(operation.ID)
	}); err != nil {
		return LaunchReconciliation{}, err
	}
	return found, nil
}

// wait records what recovery is waiting on and returns the verdict.
func (s *Store) wait(ctx context.Context, runID, operationID string, found LaunchReconciliation, verdict LaunchVerdict, reason string) (LaunchReconciliation, error) {
	found.Verdict = verdict
	found.Reason = reason
	if len(reason) > maxRoutingText {
		reason = reason[:maxRoutingText]
	}
	if _, err := s.updateRoutingFresh(ctx, runID, func(routing *RunRouting) (bool, error) {
		return routing.NoteReconciling(operationID, OperationReconciling{Attempt: found.Attempt, Reason: reason, Since: time.Now()})
	}); err != nil {
		return LaunchReconciliation{}, err
	}
	return found, nil
}

// updateRoutingFresh applies one routing change to the run as it is stored
// now. The change is decided from the stored routing itself, so a copy gone
// stale between reading and writing is read again rather than refused.
func (s *Store) updateRoutingFresh(ctx context.Context, runID string, change func(*RunRouting) (bool, error)) (State, error) {
	for tries := 0; ; tries++ {
		state, err := s.Load(runID)
		if err != nil {
			return State{}, err
		}
		next, err := s.UpdateRouting(ctx, state, change)
		var stale StaleRoutingError
		if errors.As(err, &stale) && tries < 4 {
			continue
		}
		return next, err
	}
}
