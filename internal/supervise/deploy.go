package supervise

// Moving every part onto the build deployed over it.
//
// A part of the product is a process that stays up for days, and it runs the
// binary it was started from while builds land behind it. The watch session has
// long taken a deployed build up by itself, between runs; nothing did it for the
// Slack sink but the operator's maintenance script, which the supervisor now
// retires, and nothing did it for the dashboard at all — on 2026-09-26 it served
// a day and a half from a build without the section the operator was waiting
// for, until somebody restarted it by hand.
//
// So on its own poll the supervisor reads the revision of the binary on disk,
// asks each part which build it is running, and where the two differ moves the
// part onto the binary: one part at a time, and never while the part is in the
// middle of something a restart would cut short. A part that restarts itself —
// the watch, which drains its runs and re-executes in place — is left to do so,
// and the moment its lease is let go for the re-execution is read as that
// rather than as a death. Either way the move is recorded as a restart, apart
// from the failures the restart bound counts, so a day of deploys can never
// leave a part degraded.

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// DeployEvery is how often the supervisor asks each part which build it is
	// on. The binary on disk is looked at on every tick, because that is one
	// stat; asking a part reads its records, and a deploy waits at most this
	// long to be noticed.
	DeployEvery = 30 * time.Second
	// SelfRestartGrace is how long a part that restarts itself is given to take
	// its lease again before the supervisor starts it. Re-executing is a moment,
	// and a part not back after this is started from the deployed binary.
	SelfRestartGrace = 30 * time.Second
	// MoveLimit is how long after the supervisor first records a part behind
	// the newest installed build that part must be running on it: the
	// scheduler, past the time limit of a merge queue check its wind-down waits
	// out, and the supervisor itself. It is counted from that first record and
	// never started again by a newer build landing meanwhile, so a day of
	// landings every few minutes cannot put the move off. It is held in code,
	// not a setting (docs/designs/management-and-supervision.md,
	// yoyodyne-ifd.428.225).
	MoveLimit = 15 * time.Minute
)

// Deployable is a child whose build the supervisor can read, and so move onto a
// binary deployed over it. Every part the supervisor hosts should be one; a
// child that is not is left on whatever it runs.
type Deployable interface {
	// Build is the revision the running child was built from, as the child
	// recorded it, and empty where it recorded none.
	Build(ctx context.Context) (string, error)
	// Busy is what the running child is in the middle of that a restart now
	// would cut short, and empty where a restart would interrupt nothing.
	Busy(ctx context.Context) (string, error)
}

// Passes is a child whose work comes in passes — the Slack sink's passes over
// the records. HoldBetweenPasses keeps it from starting another pass until
// release is called, so a restart lands between two passes rather than inside
// one; where it could not, because a pass is under way, it says what that pass
// is and holds nothing.
type Passes interface {
	HoldBetweenPasses(ctx context.Context) (release func(), pass string, err error)
}

// Replaces is a child moved onto a deployed build by starting its replacement
// first: the new process is started from the binary and confirmed ready, and
// only then is the old one stopped, so there is no moment at which neither
// serves. The dashboard is one. What it returns is the replacement, as Ensure
// reports a start.
type Replaces interface {
	Replace(ctx context.Context) (Ensured, error)
}

// RestartsItself is a child that takes a deployed build up on its own, in its
// own time, so the supervisor waits for it rather than stopping it. What it
// returns says how, in the words the record carries.
type RestartsItself interface {
	RestartsItself() string
}

// OverdueRestart is a child that restarts itself and can say when it has gone
// too far past the moment it was due to: the scheduler, whose wind-down for a
// move onto a new build is bounded, and which a fault inside it can leave
// winding down for ever (readmodel.DrainRestartDue). What it returns says how
// it is overdue, in words that finish "the scheduler ...", and is empty where
// it is not.
type OverdueRestart interface {
	RestartOverdue(ctx context.Context, now time.Time) (string, error)
}

// ScheduledMove is a child that restarts itself onto a deployed build and
// whose move the supervisor times: the scheduler. The child's own build
// decides when it begins its move, and a build from before a fix to that
// decision — such as one still keeping three hours between moves — or one a
// fault has stopped from beginning its wind-down would otherwise hold the
// product on it unnoticed, so the supervisor reads where the move stands from
// the child's own records and restarts a child that has not begun by its limit.
type ScheduledMove interface {
	MoveStanding(ctx context.Context) (MoveStanding, error)
}

// MoveStanding is where a child's own move onto a new build stands, as its
// records say.
type MoveStanding struct {
	// WindingDown is a child that has begun winding down for the move.
	WindingDown bool
	// QueueCheck names a merge queue check the wind-down waits out, and
	// QueueCheckUntil the latest that check can run to under its own time
	// limit; both are empty where the wind-down waits on no queue check.
	QueueCheck      string
	QueueCheckUntil time.Time
	// Waiting is what the wind-down still waits out before the child has
	// nothing in flight, in words that follow "held by": the checks by run and
	// item, promotions, or the runs it has not yet paused. Empty where it waits
	// on nothing it named.
	Waiting string
}

// DeployedBinary is the binary the parts are started from, and the revision it
// was built from. The file is looked at on every call and read again only when
// it has been written, which is what a deploy does to it.
type DeployedBinary struct {
	Path string
	// Read reads the revision a binary was built from; nil reads Go's own stamp.
	Read func(path string) (string, error)

	modTime  time.Time
	size     int64
	revision string
}

// Revision is the revision of the binary as it stands now.
func (b *DeployedBinary) Revision() (string, error) {
	info, err := os.Stat(b.Path)
	if err != nil {
		return "", fmt.Errorf("read the deployed binary %s: %w", b.Path, err)
	}
	if info.ModTime().Equal(b.modTime) && info.Size() == b.size && b.revision != "" {
		return b.revision, nil
	}
	read := b.Read
	if read == nil {
		read = Revision
	}
	revision, err := read(b.Path)
	if err != nil {
		// A binary caught half-written by a deploy reads as nothing this once, and
		// is read again at the next look rather than remembered as unreadable.
		return "", fmt.Errorf("read which build %s is: %w", b.Path, err)
	}
	b.modTime, b.size, b.revision = info.ModTime(), info.Size(), revision
	return revision, nil
}

// Written is when the binary was written, as the last good reading found it:
// when the build it is landed on disk.
func (b *DeployedBinary) Written() time.Time { return b.modTime }

// Revision reads the revision a Go binary was built from out of the file, and
// nothing where the build recorded none.
func Revision(path string) (string, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value, nil
		}
	}
	return "", nil
}

// readDeployed is the tick's reading of the binary on disk. A reading that
// failed is said once and leaves the last good one standing, because a deploy
// in the middle of writing the file is exactly when it fails.
func (s *Supervisor) readDeployed() {
	if s.Binary == nil {
		return
	}
	revision, err := s.Binary.Revision()
	if err != nil {
		s.sayOnce(fmt.Sprintf("which build the binary on disk is could not be read, so no part is moved onto it this look: %v", err))
		return
	}
	s.deployed = revision
	s.deployedAt = s.Binary.Written()
}

// behindWait is what the record says the supervisor itself waits for before it
// moves onto the deployed build, and nothing where it is on that build or
// either build cannot be told.
func (s *Supervisor) behindWait() string {
	if s.Build == "" || s.deployed == "" || s.Build == s.deployed {
		return ""
	}
	if s.takeUpWait != "" {
		return s.takeUpWait
	}
	return "at its next look"
}

// deploy is one running child against the binary on disk: its build noted, and
// a restart into the binary begun where it is behind and nothing stops it.
func (s *Supervisor) deploy(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time, returned bool) {
	deployable, ok := child.(Deployable)
	if !ok || s.deployed == "" {
		return
	}
	// Asked every DeployEvery, and on every tick only while the supervisor is
	// itself moving the child: a part that restarts itself drains on its own
	// clock, for as long as its runs take, and is not asked any more often.
	_, self := child.(RestartsItself)
	if !s.deployLook && !returned && (state.RestartingInto == "" || self) {
		return
	}
	build, err := deployable.Build(ctx)
	if err != nil {
		s.log("which build the %s service is running could not be read, so it is not compared with the deployed binary this look: %v", child.Name(), err)
		return
	}
	s.carryMoveTiming(child, state, build)
	if build != "" && build != state.Build {
		// When a part moved is known only where the supervisor saw it move: a
		// part it reattached was on its build since some moment before the
		// supervisor looked, and saying it moved at the first look would be a
		// date the record does not have.
		if state.Build != "" {
			state.BuildSince = now
			// It moved, so a move that was being timed is over, even where a
			// newer build has landed since and it is behind again.
			clearMove(state)
		} else {
			state.BuildSince = time.Time{}
		}
		state.Build = build
	}
	if state.RestartingInto != "" && build == state.RestartingInto {
		s.restarted(child, state, now, "took the deployed build up itself")
	}
	if build == "" || build == s.deployed {
		clearMove(state)
		state.RestartingInto = ""
		state.Redeploy = ""
		return
	}
	behind := fmt.Sprintf("on build %s, behind the deployed %s", short(build), short(s.deployed))
	if self {
		state.RestartingInto = s.deployed
		state.Redeploy = behind + "; " + child.(RestartsItself).RestartsItself()
		if _, timed := child.(ScheduledMove); timed && state.MoveDue.IsZero() {
			// First recorded behind: the limit counts from now, and nothing that
			// lands while it is behind starts it again.
			state.MoveDue, state.MoveBy, state.MoveCheck, state.MoveHeld = now, now.Add(MoveLimit), "", ""
		}
		if !s.restartOverdue(ctx, child, state, now, build, behind) {
			s.moveOverdue(ctx, child, state, now, build)
		}
		return
	}
	if state.RestartingInto == s.deployed {
		// Asked to stop already, and still letting go of its lease.
		return
	}
	if ahead := s.restarting(child); ahead != "" {
		state.Redeploy = fmt.Sprintf("%s; restarted into it once the %s service has moved, one part at a time", behind, ahead)
		return
	}
	if held := s.restartHold(); held != "" {
		state.Redeploy = fmt.Sprintf("%s; not restarted while %s", behind, held)
		return
	}
	busy, err := deployable.Busy(ctx)
	if err != nil {
		state.Redeploy = fmt.Sprintf("%s; not restarted, because whether it is in the middle of anything could not be read: %v", behind, err)
		return
	}
	if busy != "" {
		state.Redeploy = fmt.Sprintf("%s; restarted into it once it finishes %s", behind, busy)
		return
	}
	s.restart(ctx, child, state, now, build)
}

// restartOverdue restarts a child that restarts itself, is behind the deployed
// binary, and has gone too far past the moment it was due to restart itself.
// It is the one case the supervisor stops such a child: left alone it would
// hold the product on the old build, pulling no new work, until a person
// noticed. The restart is the ordinary one, so the child is stopped the way
// `yoyo stop` stops it and started from the deployed binary, and it counts as
// a restart rather than a death. What the scheduler had already stopped for
// the move is kept as the move keeps it, preserved for the session that comes
// back. Nothing is restarted while a restart is held. It reports whether it
// found the child overdue, restarted or held.
func (s *Supervisor) restartOverdue(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time, from, behind string) bool {
	overdue, ok := child.(OverdueRestart)
	if !ok {
		return false
	}
	why, err := overdue.RestartOverdue(ctx, now)
	if err != nil {
		s.log("whether the %s service is overdue to restart itself into the deployed build could not be read, so it is asked again at the next look: %v", child.Name(), err)
		return false
	}
	if why == "" {
		return false
	}
	if held := s.restartHold(); held != "" {
		state.Redeploy = fmt.Sprintf("%s; it %s, and is not restarted while %s", behind, why, held)
		if !state.MoveBy.IsZero() && !now.Before(state.MoveBy) {
			state.MoveHeld = "nothing is restarted while " + held
		}
		return true
	}
	s.log("the %s service %s, so the supervisor restarts it into the deployed build %s rather than wait any longer", child.Name(), why, short(s.deployed))
	s.restart(ctx, child, state, now, from)
	return true
}

// moveOverdue times the move of a child that restarts itself onto a new build.
// The limit counts from when the supervisor first recorded the child behind
// (MoveDue), never from when the binary on disk was written: a binary is
// written by every build installed, and counting from it let builds landing
// every half hour put the deadline off all day. A merge queue check the
// child's wind-down waits out lengthens the limit once, to that check's own
// time limit and MoveLimit past it. A machine that slept after the move fell
// due has the limit counted again from its wake (observeMachine), because the
// child's own loop has yet to run then.
//
// A child that has begun winding down is left to reach a point where nothing
// is in flight the way it does by itself — after its own bounded wind-down it
// pauses each run it holds, keeping its work, for the session on the new build
// to continue — and to restartOverdue if it sticks; nothing here ends a run to
// meet the limit. Past the limit the record says what holds it. One that has
// not begun by its limit is restarted the ordinary way, which pauses the runs
// it holds the same way (the child's Stop).
func (s *Supervisor) moveOverdue(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time, from string) {
	scheduled, ok := child.(ScheduledMove)
	if !ok {
		return
	}
	if s.awakeSince.After(state.MoveDue) && s.awakeSince.Add(MoveLimit).After(state.MoveBy) {
		state.MoveBy = s.awakeSince.Add(MoveLimit)
	}
	standing, err := scheduled.MoveStanding(ctx)
	if err != nil {
		s.log("where the %s service's move onto a new build stands could not be read, so it is asked again at the next look: %v", child.Name(), err)
		return
	}
	if state.MoveCheck == "" && standing.QueueCheck != "" {
		state.MoveCheck = oneline.Bound(standing.QueueCheck, runstate.MaxChildReasonBytes)
		if by := standing.QueueCheckUntil.Add(MoveLimit); by.After(state.MoveBy) {
			state.MoveBy = by
		}
	}
	apart := fmt.Sprintf("on build %s while the rest of the product is on the newest installed build %s, since %s", short(from), short(s.deployed), localStamp(state.MoveDue))
	overdue := !now.Before(state.MoveBy)
	switch {
	case standing.WindingDown && !overdue:
		state.MoveHeld = ""
		state.Redeploy = fmt.Sprintf("%s; it is winding down to move onto it, and is to be on it by %s", apart, localStamp(state.MoveBy))
		return
	case standing.WindingDown:
		held := "its own winding down, which has not yet reached a point where nothing is in flight"
		switch {
		case standing.Waiting != "" && state.MoveCheck != "":
			held = state.MoveCheck + "; " + standing.Waiting
		case standing.Waiting != "":
			held = standing.Waiting
		case state.MoveCheck != "":
			held = state.MoveCheck
		}
		state.MoveHeld = oneline.Bound(held, runstate.MaxChildReasonBytes)
		state.Redeploy = fmt.Sprintf("%s; it was to be on it by %s and is still winding down, held by %s", apart, localStamp(state.MoveBy), state.MoveHeld)
		return
	case !overdue:
		state.MoveHeld = ""
		state.Redeploy = fmt.Sprintf("%s; it has not begun winding down to move onto it, and the supervisor restarts it into the newest build at %s if it has not begun by then", apart, localStamp(state.MoveBy))
		return
	}
	why := fmt.Sprintf("was to be on the newest build by %s and has not begun to move, %s after it fell behind", localStamp(state.MoveBy), now.Sub(state.MoveDue).Round(time.Second))
	if held := s.restartHold(); held != "" {
		state.MoveHeld = "nothing is restarted while " + held
		state.Redeploy = fmt.Sprintf("%s; it %s, and is not restarted while %s", apart, why, held)
		return
	}
	s.log("the %s service is %s: it %s, so the supervisor restarts it into the newest build rather than wait any longer", child.Name(), apart, why)
	s.restart(ctx, child, state, now, from)
}

// carryMoveTiming takes up, at the supervisor's first reading of a child's
// build, the timing of its move that the supervisor before this one left in
// the record, where the child is still on the build it was timed on. That is
// what keeps a supervisor's own restart — and it restarts onto every build
// installed — from starting the count again.
func (s *Supervisor) carryMoveTiming(child Child, state *runstate.SupervisedChild, build string) {
	previous, found := s.previous[child.Name()]
	if !found {
		return
	}
	delete(s.previous, child.Name())
	if previous.MoveDue.IsZero() || build == "" || previous.Build != build || !state.MoveDue.IsZero() {
		return
	}
	state.MoveDue, state.MoveBy, state.MoveCheck, state.MoveHeld = previous.MoveDue, previous.MoveBy, previous.MoveCheck, previous.MoveHeld
}

// clearMove forgets the timing of a child's move: it is on the newest build,
// or it has moved.
func clearMove(state *runstate.SupervisedChild) {
	state.MoveDue, state.MoveBy, state.MoveCheck, state.MoveHeld = time.Time{}, time.Time{}, "", ""
}

// localStamp is a moment in the reader's own zone, with the zone named.
func localStamp(at time.Time) string {
	return at.Local().Format("2006-01-02 15:04 MST")
}

// restart stops a child that is behind the deployed binary and starts it again
// from that binary. The stop is the one `yoyo stop` makes, and the start is the
// ordinary lease-checked start, so a part restarted here holds exactly what it
// held before.
func (s *Supervisor) restart(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time, from string) {
	behind := fmt.Sprintf("on build %s, behind the deployed %s", short(from), short(s.deployed))
	release := func() {}
	if between, ok := child.(Passes); ok {
		held, pass, err := between.HoldBetweenPasses(ctx)
		switch {
		case err != nil:
			state.Redeploy = fmt.Sprintf("%s; not restarted, because it could not be held between two passes: %v", behind, err)
			return
		case pass != "":
			state.Redeploy = fmt.Sprintf("%s; restarted into it once it finishes %s", behind, pass)
			return
		}
		release = held
	}
	state.RestartingInto = s.deployed
	state.Redeploy = fmt.Sprintf("restarting from build %s into the deployed %s", short(from), short(s.deployed))
	if replacer, ok := child.(Replaces); ok {
		release()
		s.replace(ctx, child, replacer, state, now, from)
		return
	}
	s.log("the %s service is on build %s and %s was deployed over it; restarting it into the deployed build", child.Name(), short(from), short(s.deployed))
	stopped, err := child.Stop(ctx)
	// Let go once the part is stopped, so the part started in its place can take
	// its passes up; it is held no longer than the stop.
	release()
	s.recordForced(child, state, stopped)
	if err != nil {
		state.RestartingInto = ""
		state.Redeploy = fmt.Sprintf("on build %s, behind the deployed %s; it could not be stopped to move it, and is tried again at the next look: %v", short(from), short(s.deployed), err)
		if !state.MoveDue.IsZero() {
			state.MoveHeld = oneline.Bound(fmt.Sprintf("a restart that failed: it could not be stopped to move it: %v", err), runstate.MaxChildReasonBytes)
		}
		s.log("the %s service %s", child.Name(), state.Redeploy)
		return
	}
	if stopped.Detail != "" {
		state.Redeploy = fmt.Sprintf("asked to stop so it can be restarted into the deployed %s; %s, and it is started again once it has let go of its lease", short(s.deployed), stopped.Detail)
		return
	}
	// Gone, by the lease's own answer: started again now rather than at the next
	// look, so the part is down for as little as the stop took.
	s.start(ctx, child, state, now)
}

// replace moves a child that starts its replacement first onto the deployed
// build. A replacement that could not be made ready leaves the old process
// serving, as it was, and is tried again at the next look at builds; it is
// never counted against the restart bound, because the part never stopped.
func (s *Supervisor) replace(ctx context.Context, child Child, replacer Replaces, state *runstate.SupervisedChild, now time.Time, from string) {
	s.log("the %s service is on build %s and %s was deployed over it; starting its replacement from the deployed build, and stopping it once the replacement is ready", child.Name(), short(from), short(s.deployed))
	ensured, err := replacer.Replace(ctx)
	if err != nil {
		state.RestartingInto = ""
		state.Redeploy = fmt.Sprintf("on build %s, behind the deployed %s; its replacement could not be made ready, so it is left serving and the replacement is tried again at the next look at builds: %v", short(from), short(s.deployed), err)
		s.log("the %s service %s", child.Name(), state.Redeploy)
		return
	}
	s.sayEarlier(child.Name(), ensured.Earlier)
	state.State = runstate.ChildRunning
	state.Reason = ""
	state.PID = ensured.PID
	if ensured.Log != "" {
		state.Log = ensured.Log
	}
	state.Reattached = false
	state.StartedAt = now
	state.Starts++
	s.serving(ctx, child, state)
	s.restarted(child, state, now, "was replaced by one started from the deployed binary, ready before the old one was stopped")
}

// restarted records a child's move onto the deployed build as the restart it
// was, apart from the failures the bound counts.
func (s *Supervisor) restarted(child Child, state *runstate.SupervisedChild, now time.Time, how string) {
	into := state.RestartingInto
	state.Restarts++
	state.RestartedAt = now
	state.Build = into
	state.BuildSince = now
	state.RestartingInto = ""
	state.Redeploy = ""
	clearMove(state)
	s.log("the %s service %s and is on build %s: restart %d into a deployed build, a restart rather than a death, so its restart bound is untouched", child.Name(), how, short(into), state.Restarts)
}

// restarting names a child the supervisor is itself moving onto the deployed
// build, other than the one asking, so the moves are made one part at a time.
// A child that restarts itself is its own schedule and holds nobody back.
func (s *Supervisor) restarting(except Child) string {
	for _, child := range s.Children {
		if child.Name() == except.Name() {
			continue
		}
		if _, self := child.(RestartsItself); self {
			continue
		}
		if state, known := s.states[child.Name()]; known && (state.RestartingInto != "" || state.SettingsRestarting) {
			return string(child.Name())
		}
	}
	return ""
}

// selfRestarting reports a child found without its lease that is re-executing
// itself into the deployed build rather than dying: one that restarts itself,
// and was last seen on a build the binary on disk has moved past.
func (s *Supervisor) selfRestarting(child Child, state *runstate.SupervisedChild) bool {
	if _, self := child.(RestartsItself); !self {
		return false
	}
	if state.RestartingInto != "" {
		return true
	}
	if state.Build == "" || s.deployed == "" || state.Build == s.deployed {
		return false
	}
	state.RestartingInto = s.deployed
	return true
}

// sayOnce logs a line, and the same line once.
func (s *Supervisor) sayOnce(said string) {
	if said == s.lastSaid {
		return
	}
	s.lastSaid = said
	s.log("%s", said)
}

// recordForced records on the child's record a stop the supervisor had to
// force, with when and why, where status and the health check read it, and
// says it in the supervisor's log. A stop that was not forced records nothing.
func (s *Supervisor) recordForced(child Child, state *runstate.SupervisedChild, stopped Stopped) {
	if stopped.Forced == "" {
		return
	}
	state.ForcedStops++
	state.ForcedStopAt = stopped.ForcedAt
	state.ForcedStop = stopped.Forced
	s.log("the %s service, pid %d, did not stop when asked: %s", child.Name(), stopped.PID, stopped.Forced)
}
