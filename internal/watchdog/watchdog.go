// Package watchdog notices that the harness has stopped doing anything, from
// machinery that is always running.
//
// The reading itself is the read model's and the record is the runstate store's.
// What is here is the one thing neither of them holds: the pass that reads the
// durable records, derives the silence from them, and hands it to the record —
// assembled once so that every process which can run it comes to the same
// answer.
//
// It lives here rather than in the Slack sink, which is where it was first
// built. The sink's loop was chosen because it outlives the scheduler, and that
// is true; what it is not is always running. Slack reporting is opt-in
// throughout — an observation and never a gate — so a product that never turned
// it on had no watchdog at all, and its stall history was permanently empty. The
// instrument that exists so silence is impossible cannot depend on an optional
// process, which is the same lesson the provider usage window taught one layer
// down: detection of nothing-running belongs in machinery that keeps running.
//
// # Nothing here may be a model-based watcher
//
// This is plain Go reading durable files, and it has to stay that way. Every
// watcher the harness has that asks a model something pauses with the provider's
// usage window, so a watchdog built on one goes to sleep at exactly the moment
// the thing it watches goes quiet. No provider call is on this path, directly or
// transitively, ever.
//
// # It notices and does nothing else
//
// Restarting whatever died is not this. A checker that restarted the watch
// session would be a second thing that invokes and supervises the harness's
// processes, and both halves already have owners: the session's own bounded exit
// and the supervisor that starts it.
package watchdog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Runs is every run this product has recorded, read for two facts nothing else
// holds: when a run last demonstrably held a slot, and whether any of them
// is still moving. It is satisfied by *runstate.Store.
type Runs interface {
	Recorded() ([]runstate.State, error)
}

// Backlog is how much admitted work the tracker itself calls ready. It is a
// count rather than the items because which items they are is the scheduler's
// business, and it is the whole of what separates a stalled machine from a
// drained queue.
type Backlog interface {
	Ready(ctx context.Context) (int, error)
}

// Checker is one pass over the records that say whether anything is happening.
//
// Every source is required. A check assembled without one of them would be
// deciding a stall over a record it could not read, and both directions of that
// mistake are the ones this instrument exists to avoid: inventing ready work
// wakes somebody for nothing, and assuming an unread hold is absent reports a
// deliberate stop as a machine that died.
type Checker struct {
	Runs     Runs
	Sessions readmodel.Sessions
	// Holds and Intake are the operator's two switches, read because each is a
	// decision somebody made and is therefore never a silence anybody needs
	// waking for.
	Holds  readmodel.OperatorHolds
	Intake readmodel.IntakeHolds
	// Outages is the provider answering nobody, read because a line of runs each
	// waiting on a login is not a line that died. It is the one optional source:
	// a checker built without it reads as it did before the wait was named, and
	// what it costs is a page for a machine waiting correctly.
	Outages readmodel.ProviderOutages
	// Divergences is the product's record of the target branches the harness
	// will not catch up to the remote's, read because a watching session chooses
	// nothing while one stands, on purpose and saying why. Optional for the
	// reason Outages is.
	Divergences readmodel.DivergedTargets
	// LaunchSettings is the product's record of a developer's provider found not
	// to put in force what a developer is launched with, read because a watching
	// session starts no developer while it stands, on purpose and after reporting
	// it once. Optional for the reason Outages is.
	LaunchSettings readmodel.LaunchSettingsHolds
	// Backlog is asked only where nothing else accounts for the quiet, which is
	// what keeps a healthy idle product from spawning a tracker process on every
	// check.
	Backlog Backlog
	// Stalls is the durable record. One stall at a time is its rule rather than
	// this pass's, which is what makes a checker running every few seconds and one
	// running every few minutes agree about how many stalls there were.
	Stalls *runstate.StallStore
	// Threshold is how long nothing may start, over ready work and with nothing
	// accounting for it, before that is a stall. Zero takes
	// readmodel.DefaultStallThreshold.
	Threshold time.Duration
	// RunActivityWindow is how long a run's own record may go unmoved before it
	// stops accounting for a quiet line. Zero takes
	// readmodel.DefaultRunActivityWindow.
	RunActivityWindow time.Duration
	// Now stamps the reading, and is injected so a test can say when it was taken.
	Now func() time.Time
}

// Reading is what one check came to: the silence it read, and what that did to
// the record.
//
// All three events are reported because a caller does different things with
// each. What was opened is what a surface says out loud; what stands is what a
// caller checks its own memory against; and what was closed is neither, because
// whatever cleared the stall has already said so itself.
type Reading struct {
	Silence  readmodel.Silence    `json:"silence"`
	Opened   *runstate.StallEvent `json:"opened,omitempty"`
	Closed   *runstate.StallEvent `json:"closed,omitempty"`
	Standing *runstate.StallEvent `json:"standing,omitempty"`
	// Window is the provider's usage window that accounted for the quiet, where
	// one did. It is carried because a caller that reported nothing at all here
	// would leave an operator unable to tell a window from a watchdog somebody
	// switched off.
	Window readmodel.ProviderWindow `json:"window"`
}

// Stalled reports a check that came back with a stall standing after it,
// whether this check opened it or an earlier one did.
func (r Reading) Stalled() bool { return r.Standing != nil }

// Check reads the records once and reconciles what they say against the durable
// stall record.
//
// It fails rather than deciding where a source could not be read, and the
// tracker is the case that matters: a tracker that will not answer leaves this
// unable to tell a stalled machine from a drained queue, and it must not guess in
// either direction. Nothing is recorded on a failed check, so a caller reports it
// and asks again at its next pass.
func (c Checker) Check(ctx context.Context) (Reading, error) {
	if err := c.validate(); err != nil {
		return Reading{}, err
	}
	now := c.now()
	runs, err := c.Runs.Recorded()
	if err != nil {
		return Reading{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	sessions, err := c.Sessions.List()
	if err != nil {
		return Reading{}, fmt.Errorf("read the sessions that choose work: %w", err)
	}
	_, operatorHeld, err := c.Holds.Held()
	if err != nil {
		return Reading{}, fmt.Errorf("read the operator's pause: %w", err)
	}
	_, intakeHeld, err := c.Intake.Held()
	if err != nil {
		return Reading{}, fmt.Errorf("read the intake hold: %w", err)
	}
	var outage runstate.ProviderOutage
	providerAway := false
	if c.Outages != nil {
		outage, providerAway, err = c.Outages.Standing()
		if err != nil {
			return Reading{}, fmt.Errorf("read whether the provider is answering: %w", err)
		}
	}

	var diverged []runstate.DivergedTarget
	if c.Divergences != nil {
		diverged, err = c.Divergences.Standing()
		if err != nil {
			return Reading{}, fmt.Errorf("read whether a target branch stands diverged: %w", err)
		}
	}

	var launchSettings *runstate.LaunchSettingsHold
	if c.LaunchSettings != nil {
		hold, standing, err := c.LaunchSettings.Standing()
		if err != nil {
			return Reading{}, fmt.Errorf("read whether developers are held on their provider's launch settings: %w", err)
		}
		if standing {
			launchSettings = &hold
		}
	}

	activity := readmodel.Activity{
		// Dated from the last moment a slot was held rather than the last start,
		// so a batch of runs ending just before the pull that refills the slots
		// is not read as a line that has been quiet since the batch began.
		Since: readmodel.LastHeld(runs, sessions),
		// In flight *and still moving*: a run whose process was killed leaves a
		// record saying it is in flight until `yoyo reconcile` settles it, and
		// taking that at face value would silence this for the crash it exists to
		// catch.
		Running:      readmodel.ActiveRuns(runs, now, c.runActivityWindow()),
		OperatorHeld: operatorHeld,
		IntakeHeld:   intakeHeld,
		// The provider refusing to serve any more work, as the session that met it
		// recorded: a session waiting out a usage window starts nothing over a ready
		// queue and looks from here exactly like one that has died.
		ProviderWindow: readmodel.WaitingOnProvider(sessions),
		ProviderOutage: outage,
		ProviderAway:   providerAway,
		Diverged:       diverged,
		LaunchSettings: launchSettings,
		// A dispatch waiting out the tracker before it claims anything, as the
		// session that started it recorded: it holds a slot with no run to say so,
		// and for up to the recovery window looks from here like a session that hung.
		TrackerWaits: readmodel.WaitingOnTracker(sessions, now),
		Watched:      len(sessions) > 0,
		Threshold:    c.threshold(),
		Now:          now,
	}
	if activity.Unexplained() {
		// The one reading here that costs a process. Everything above is derived
		// from records already in hand, and a drained queue is deliberately not one
		// of the accounted-for states, because nothing but the tracker can say the
		// queue is drained.
		count, err := c.Backlog.Ready(ctx)
		if err != nil {
			return Reading{}, fmt.Errorf("read what is ready to pull: %w", err)
		}
		activity.Ready = count
	}
	silence := readmodel.ReadSilence(activity)

	reconciled, err := c.Stalls.Reconcile(runstate.StallObservation{
		Stalled:  silence.Stalled,
		Since:    silence.Since,
		Ready:    silence.Ready,
		Chooser:  readmodel.LastWord(sessions),
		Explains: silence.Explains,
		At:       now,
	})
	if err != nil {
		return Reading{}, err
	}
	window, _ := activity.StandingWindow()
	return Reading{
		Silence:  silence,
		Opened:   reconciled.Opened,
		Closed:   reconciled.Closed,
		Standing: reconciled.Standing,
		Window:   window,
	}, nil
}

func (c Checker) validate() error {
	var problems []error
	if c.Runs == nil {
		problems = append(problems, errors.New("a stall check requires the recorded runs"))
	}
	if c.Sessions == nil {
		problems = append(problems, errors.New("a stall check requires the sessions that choose work"))
	}
	if c.Holds == nil {
		problems = append(problems, errors.New("a stall check requires the operator's pause"))
	}
	if c.Intake == nil {
		problems = append(problems, errors.New("a stall check requires the intake hold"))
	}
	if c.Backlog == nil {
		problems = append(problems, errors.New("a stall check requires what is ready to pull"))
	}
	if c.Stalls == nil {
		problems = append(problems, errors.New("a stall check requires the product's stall record"))
	}
	return errors.Join(problems...)
}

func (c Checker) threshold() time.Duration {
	if c.Threshold > 0 {
		return c.Threshold
	}
	return readmodel.DefaultStallThreshold
}

func (c Checker) runActivityWindow() time.Duration {
	if c.RunActivityWindow > 0 {
		return c.RunActivityWindow
	}
	return readmodel.DefaultRunActivityWindow
}

func (c Checker) now() time.Time {
	if c.Now == nil {
		return time.Now().UTC()
	}
	return c.Now().UTC()
}
