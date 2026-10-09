package readmodel

// Nothing happening, which is the one state nothing in the record announces.
//
// Every other reading in this package answers a question somebody asked. The
// four lines say where the harness stands, and the stall beside them says what
// stopped the choosing — both of them derived from records something wrote down
// on purpose. That works exactly as long as the thing that would have written
// one is alive to write it. A watch session that crashes writes no stop, a
// wedged one goes on saying it is watching, and both of them look from every
// existing surface like a machine with nothing to do.
//
// So this derives the absence instead. Not "what does the record say is
// stopping the line" but "how long is it since anything actually started, and is
// there anything at all that accounts for it" — a question answered from the
// runs' own start and end times rather than from any process's account of itself, which
// is why it survives the death of the process it is about. On 2026-09-01 that
// was seven and a half hours of a dead watch that no surface reported and a
// person eventually noticed.
//
// What is here is only the reading. Recording that it happened is the durable
// stall record's, and deciding it is worth waking somebody for is the surface's,
// exactly as the four lines and the heartbeat divide the same work.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultStallThreshold is how long nothing may start, over work the tracker
// calls ready and with nothing accounting for it, before that is a stall rather
// than a gap between runs.
//
// Ten minutes is the operator's own bar, set on 2026-09-05 after an hour passed
// between the harness stopping and him hearing about it: minutes, not an hour.
// It is chosen against what it costs to be wrong in each direction, and the two
// costs are no longer symmetric. A watch session polls in seconds and starts
// what it can immediately, and every legitimate reason for a quiet line is now
// read before this one — the two switches, a run in flight and still moving, a
// product nobody watches, and, since 2026-09-05, the provider's own usage
// window. So the states that used to need a wide margin are named rather than
// waited out, and what is left inside ten minutes of ready work with nothing
// started is already outside anything healthy.
//
// The margin it used to buy was half an hour, and the window that was actually
// paid for while nobody was told was seven and a half hours. An operator who
// wants the older margin back sets it: this is the default rather than the rule,
// and it is changed on the two commands that take the reading, `yoyo work
// --watch --stall-after` and `yoyo reconcile --stall-after`. Not on the sink,
// which reports what they recorded rather than noticing anything itself.
const DefaultStallThreshold = 10 * time.Minute

// DefaultRunActivityWindow is how long a run's own record may go unmoved before
// it stops accounting for a quiet line.
//
// A run in flight is the one explanation here that is read from a record its
// own process writes, so it is the one that survives that process dying: a
// killed run leaves durable state saying it is in flight until `yoyo reconcile`
// settles it, and taking that at face value would silence this watchdog for
// exactly the crash it exists to catch. The window is what turns "in flight"
// into "in flight and still moving".
//
// An hour is chosen against what the harness itself guarantees about a live
// run. Every provider event a run parses is stamped onto its record as it is
// persisted, and an invocation that emits nothing for five minutes is stopped as
// stalled, so a working run moves its record continuously. The slowest thing a
// legitimately live run does is wait out a provider limit, which probes every
// `usage_limit_unknown_reset_pause` — thirty minutes by default — and writes as
// it reissues. An hour clears that by a factor of two, so a run that is merely
// slow is never mistaken for a dead one, while a phantom run silences this for
// an hour rather than until somebody notices.
const DefaultRunActivityWindow = time.Hour

// Activity is what a stall reading is derived from: the last time the harness
// demonstrably started work, and everything that could legitimately account for
// nothing having started since.
//
// Every field is passed in rather than read here, for the reason the stall's
// conditions are: the caller has already read all of it for something else, and
// a second reading is a second chance for one pass to report one machine two
// ways.
type Activity struct {
	// Since is the last moment anything demonstrably held a developer slot: the
	// later of the last run start and the last run end (see LastHeld). Where the
	// harness has never started a run, it is the earliest moment anything recorded
	// that a session was watching this product — which is the earliest point from
	// which a failure to start anything means something. Zero is a harness nothing
	// has ever observed, and nothing is concluded from it.
	Since time.Time
	// Ready is how much admitted work the tracker itself calls ready. It is the
	// whole of what separates a stall from a drained queue, and it costs a tracker
	// read, which is why Unexplained answers without it.
	Ready int
	// Running is how many developer runs are in flight *and still moving*, which
	// is what ActiveRuns counts and is deliberately narrower than how many runs
	// the records call in flight. A run that is genuinely working is not a
	// stalled line whatever else is true — a message saying nothing is happening
	// while a run posts its way through a review is false in the way that teaches
	// people to stop reading — but a run whose process is gone is not working,
	// and its record goes on saying it is in flight until `yoyo reconcile`
	// settles it. Counting that would silence this watchdog for the crash it
	// exists to catch, so the record has to show the run still moving.
	Running int
	// OperatorHeld and IntakeHeld are the operator's two switches. Each is a
	// deliberate decision that is already said where decisions are said, so
	// neither is a silence anybody needs waking for.
	OperatorHeld bool
	IntakeHeld   bool
	// ProviderWindow is the provider's usage window the session that chooses work
	// recorded itself waiting out, where one is standing. It is the accounting this
	// reading had no way to see: on 2026-09-05 a session waited a window out from
	// 12:13Z to 13:43Z, and every condition above answered no, so a machine doing
	// exactly what the provider had told it to was reported as one that had
	// stopped and the operator was woken for it. See ProviderWindow.Standing for
	// what bounds it — a window that has lifted accounts for nothing, which is what
	// keeps this from being a way to silence the watchdog rather than to inform it.
	ProviderWindow ProviderWindow
	// ProviderOutage is the provider answering nobody — a login nobody has
	// renewed, an API nothing reaches — and ProviderAway whether it stands. It
	// accounts for the quiet the way the window does and for a stronger reason:
	// the runs holding the slots are each waiting on the same provider, their
	// records unmoved, and a watchdog that read that as a machine that died would
	// wake somebody to restart what is already waiting correctly. What bounds it
	// is the record itself, which the first served invocation clears.
	ProviderOutage runstate.ProviderOutage
	ProviderAway   bool
	// Diverged is every target branch recorded as one the harness will not catch
	// up to the remote's. It accounts for the quiet because a watching session
	// chooses nothing while one stands, deliberately, and says why on the
	// attention line and in the channel; what bounds it is the record, which the
	// convergence sweep lifts the moment it finds the branches converged.
	Diverged []runstate.DivergedTarget
	// LaunchSettings is the hold on developers whose provider was found not to
	// put in force what a developer is launched with, where one stands. It
	// accounts for the quiet because a watching session starts no developer
	// while it stands, deliberately, and the harness reported it once when it
	// began; what bounds it is the record, which the first check that finds the
	// settings in force lifts.
	LaunchSettings *runstate.LaunchSettingsHold
	// TrackerWaits is every dispatch a session started that is waiting out a
	// tracker failure before it has claimed anything, as WaitingOnTracker reads
	// them. Such a dispatch holds a slot with no run record, so nothing above can
	// see it, and for up to the recovery window it starts nothing over a ready
	// queue and looks from here exactly like a session that has hung. What bounds
	// it is the wait itself: each one stands only until the attempt it precedes has
	// had time to answer, so a dispatch that died mid-wait stops accounting for the
	// quiet within minutes of when it would have asked again.
	TrackerWaits []DispatchWait
	// Watched says a session has at some point watched this product. A product no
	// session has ever watched is not a line that stopped — nothing was choosing
	// work here, so nothing is failing to, and an operator running items by name
	// has a queue by choice.
	Watched bool
	// Threshold is how long nothing may start before it is a stall. Zero takes
	// DefaultStallThreshold.
	Threshold time.Duration
	// Now is when the reading was taken.
	Now time.Time
}

// Silence is one reading of the harness having gone quiet: whether it has, since
// when, over how much ready work, and — where it has not — what accounts for it.
type Silence struct {
	Stalled bool `json:"stalled,omitempty"`
	// Since is when anything last held a developer slot, whether by starting or
	// by ending. The age of the stall is measured from it.
	Since time.Time `json:"since,omitempty"`
	// Ready is how much admitted work waited through it.
	Ready int `json:"ready,omitempty"`
	// Explains is what accounts for nothing having started, on a reading that is
	// not a stall. It is the words a closed stall record keeps, so a stall that
	// cleared says what cleared it rather than only stopping.
	Explains string `json:"explains,omitempty"`
}

// ActiveRuns counts the developer runs whose records say they are in flight and
// still moving, which is the only sense in which a run accounts for a quiet
// line.
//
// The liveness test is the run's own UpdatedAt, because that is the stamp the
// run writes as it works: every provider event it parses is persisted onto its
// record. A run that has stopped writing has stopped running, whatever its
// status field still says — which is precisely the state a killed process
// leaves behind. See DefaultRunActivityWindow for how the window is chosen.
func ActiveRuns(runs []runstate.State, now time.Time, within time.Duration) int {
	if within <= 0 {
		within = DefaultRunActivityWindow
	}
	active := 0
	for _, run := range runs {
		if run.Status.Terminal() {
			continue
		}
		// A record with no moment on it says nothing about whether its run is
		// alive, and reading that as activity is the assumption this exists to
		// remove. It is not counted, so it cannot silence anything.
		if run.UpdatedAt.IsZero() {
			continue
		}
		if now.Sub(run.UpdatedAt) < within {
			active++
		}
	}
	return active
}

// Unexplained is the half of the reading that costs nothing: whether anything
// other than an empty queue accounts for nothing having started.
//
// It is separate so that a caller can spend the tracker read only where the
// answer still turns on it. That is a necessary condition for the read rather
// than a sufficient one: a drained queue is deliberately not an explanation
// here, because nothing but the tracker can say the queue is drained, so a
// caller that asked on this alone would ask on every poll of an idle product.
// Gating how often it then asks is the caller's, and the two callers that take
// this reading gate it on the stall threshold itself rather than on an interval
// of their own: a watching session reads at most once per threshold however
// often it pulls, and a sweep reads once per sweep. How promptly this is
// noticed is the whole of what the threshold is for, and gating the read at an
// hour made a ten-minute bar mean an hour.
func (a Activity) Unexplained() bool {
	return a.explanation() == ""
}

// explanation is what accounts for nothing having started, or nothing at all.
// The order is the order a reader would accept them in: the switches somebody
// placed, then the work that is visibly moving, then the provider refusing to
// serve any more of it, then a dispatch waiting out the tracker, then a product nobody ever watched, then a machine too
// young to have gone quiet.
func (a Activity) explanation() string {
	switch {
	case a.OperatorHeld:
		return "all harness activity is held by the operator"
	case a.IntakeHeld:
		return "intake is held"
	case a.ProviderAway:
		return a.ProviderOutage.Says()
	case len(a.Diverged) > 0:
		return a.Diverged[0].Says()
	case a.LaunchSettings != nil:
		return a.LaunchSettings.Says()
	case a.Running > 0:
		return fmt.Sprintf("%d developer run(s) are in flight and still moving", a.Running)
	case a.ProviderWindow.Standing(a.Now):
		return a.ProviderWindow.Says()
	case len(a.TrackerWaits) > 0:
		return a.TrackerWaits[0].Says()
	case !a.Watched:
		return "no watch session has ever run on this product"
	case a.Since.IsZero():
		return "nothing is recorded that this product was ever choosing work"
	case a.Now.Sub(a.Since) < a.threshold():
		return "a developer run started or ended within the last " + a.threshold().String()
	default:
		return ""
	}
}

// StandingWindow is the provider's usage window that accounts for this reading's
// quiet, where one does. It is how a surface says the window rather than only
// declining to raise an alarm about it: the reading knows both whether a window
// was recorded and whether it has lifted, and a caller that asked the first
// question on its own would report a window that ended hours ago.
func (a Activity) StandingWindow() (ProviderWindow, bool) {
	if !a.ProviderWindow.Standing(a.Now) {
		return ProviderWindow{}, false
	}
	return a.ProviderWindow, true
}

func (a Activity) threshold() time.Duration {
	if a.Threshold > 0 {
		return a.Threshold
	}
	return DefaultStallThreshold
}

// ReadSilence says whether the harness has gone quiet with work waiting.
//
// A drained queue is answered last rather than first, because it is the one
// answer that costs a tracker read: everything above it is derived from records
// the caller already holds, and Unexplained is what lets a caller skip the read
// entirely.
func ReadSilence(activity Activity) Silence {
	silence := Silence{Since: activity.Since, Ready: activity.Ready}
	if explains := activity.explanation(); explains != "" {
		silence.Explains = explains
		return silence
	}
	if activity.Ready <= 0 {
		silence.Explains = "the tracker reports nothing ready to pull"
		return silence
	}
	silence.Stalled = true
	return silence
}

// SessionSays is what one transition's state is said as, wherever a surface names
// where a session got to. The recorded state is the word for it, except for the
// two transitions whose mark says the state alone would be read backwards: a stop
// that is a restart, which reads as a session somebody has to start again, and an
// idle poll that could not read the store at all, which reads as a queue with
// nothing in it for as long as the outage lasts. A session draining to restart
// has the drain and its bound added to whichever of those it is.
func SessionSays(transition runstate.WatchTransition) string {
	said := string(transition.State)
	switch {
	case transition.Restarting:
		said = "stopped to restart into the build deployed over it"
	case transition.RetryingRead():
		said = "retrying a failed read of the harness's store"
	}
	if transition.Draining != nil {
		said += ", " + transition.Draining.Says()
	}
	return said
}

// LastWord is what the sessions that choose work last said about themselves, as
// the one clause a stall is reported with.
//
// It is the whole of what tells a dead scheduler from a wedged one, and that is
// the first thing an operator woken by a stall needs: a session whose last word
// was "stopped" wants starting, and one still claiming to be watching wants
// killing. Neither is derivable from the stall itself, because a stall is
// precisely the absence of anything being written down.
func LastWord(sessions []runstate.WatchTransition) string {
	if len(sessions) == 0 {
		return "no watch session has ever run on this product"
	}
	// Live is newest first, so the first entry is the latest word from a session
	// that has not stopped.
	if live := Live(sessions); len(live) > 0 {
		latest := live[0]
		return fmt.Sprintf("the session choosing work last recorded %s at %s, and has said nothing since",
			SessionSays(latest), latest.At.UTC().Format(time.RFC3339))
	}
	var stopped runstate.WatchTransition
	for _, transition := range sessions {
		if transition.State == runstate.WatchStopped && transition.At.After(stopped.At) {
			stopped = transition
		}
	}
	if stopped.At.IsZero() {
		return "no watch session is running"
	}
	return "no watch session is running; the last one stopped at " +
		stopped.At.UTC().Format(time.RFC3339)
}

// LastHeld is the last moment anything demonstrably held a developer slot: the
// later of the last run start and the last run end. Where no run has ever
// started, it is the earliest moment anything recorded that a session was
// watching this product.
//
// The end counts as well as the start because a slot is held for the whole of a
// run, not only at its start. Dating the silence from the last start alone reads
// a batch of runs that each took more than the threshold as a line that has
// been quiet since the batch began. On 2026-09-24 the watch filled every free
// slot in one pull, runs took more than an hour, and the moment the batch ended
// there was an instant with nothing in flight and a last start over an hour old.
// The watch read a stall at exactly that instant, just before the pull that
// filled the slots again. That happened twenty times running, and the operator
// learned to ignore the alarm. A slot free for seconds before a pull is not a
// stall. A slot free for the whole threshold still is, because nothing moves
// this reading on except a run starting or ending.
//
// The fallback is what makes a machine that has never started anything readable
// at all. Without it a product whose scheduler died before its first run would
// have no anchor and would therefore never be reported as stalled, which is the
// case that most looks like the harness working.
func LastHeld(runs []runstate.State, sessions []runstate.WatchTransition) time.Time {
	var latest time.Time
	for _, run := range runs {
		if run.StartedAt.After(latest) {
			latest = run.StartedAt
		}
		if ended := heldUntil(run); ended.After(latest) {
			latest = ended
		}
	}
	if !latest.IsZero() {
		return latest
	}
	var earliest time.Time
	for _, transition := range sessions {
		if transition.At.IsZero() {
			continue
		}
		if earliest.IsZero() || transition.At.Before(earliest) {
			earliest = transition.At
		}
	}
	return earliest
}

// heldUntil is the moment a run last held its slot at its end, or zero for a
// run still in flight, whose hold is read from whether it is still moving
// (ActiveRuns) rather than from here.
//
// An end the run's own process wrote is when it let the slot go. An end the
// harness wrote while settling a run whose process was already gone is not: it
// is when somebody noticed, and a sweep reads the stall straight after it
// settles. So such a run is taken as holding its slot until its record last
// moved, which the settlement keeps as SettledQuietSince. Reading the
// settlement as activity would silence the alarm for the crash it exists to
// catch, and date the stall from the settlement rather than from the silence.
func heldUntil(run runstate.State) time.Time {
	switch {
	case run.CompletedAt == nil:
		return time.Time{}
	case run.SettledQuietSince != nil:
		return *run.SettledQuietSince
	default:
		return *run.CompletedAt
	}
}
