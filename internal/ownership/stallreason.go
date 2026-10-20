package ownership

// Why the harness is choosing nothing, as a closed vocabulary. The derivation of
// which reason stands, and the sentences each surface prints for it, are the
// read model's (readmodel.Stall); the vocabulary is here because the registry's
// rule for a stall decides its owner by it, and readmodel.Reason is an alias of
// it.

// StallReason is one member of the fixed set of reasons the harness is choosing no
// work. The set is closed: a state outside it cannot be reported, which is what
// makes an unnamed reason impossible rather than unlikely.
//
// The values are the tokens a durable cursor already holds — the sink names the
// state it is standing on by them, so that a different state re-arms its clock
// rather than inheriting the last one's. They are kept as they were found for
// that reason: renaming one would re-arm every state standing at the moment this
// landed, and the hour that costs is an hour of exactly the silence this exists
// to end.
type StallReason string

const (
	// StallOperatorHold is the switch over everything the harness would spend.
	StallOperatorHold StallReason = "hold"
	// StallIntakeHold is the switch over the work the harness chooses for itself.
	StallIntakeHold StallReason = "intake"
	// StallNoCapacity is a machine with every developer slot taken. It is the one
	// reason here that is the harness working rather than the harness stopped.
	StallNoCapacity StallReason = "capacity"
	// StallProviderAway is the provider answering nobody: a login nobody has
	// renewed, or an API nothing reaches. It is distinct from the window below
	// because no clock ends it — a person logging in or the network returning is
	// what does — and distinct from the intake hold because no switch lifts it.
	// It is read ahead of a full machine because the runs holding the slots are
	// waiting on the same provider.
	StallProviderAway StallReason = "provider-away"
	// StallDivergedTarget is a target branch the harness will not catch up to
	// the remote's, recorded by the run whose promotion was refused on it. It is
	// distinct from the intake hold because nobody placed it and `yoyo release`
	// does not lift it, and distinct from the brake because it counts nothing: a
	// person settling the branches is what ends it, and the convergence sweep
	// that finds them settled lifts it with nothing to release. It is read ahead
	// of a full machine because every run holding a slot will stop on it too.
	StallDivergedTarget StallReason = "diverged-target"
	// StallProviderWindow is a live session waiting out the provider's usage
	// window. It is distinct from an idle session because an operator does nothing
	// at all about it: the window lifts on the provider's clock, and a surface that
	// reported this as a session finding nothing to start would be sending somebody
	// to look at a queue that is fine.
	StallProviderWindow StallReason = "provider"
	// StallTrackerWait is a dispatch a live session started that is waiting out a
	// tracker failure before it has claimed anything. It is distinct from an idle
	// session for the reason the window is: the session found work and started it,
	// and the dispatch asks the tracker again on its own clock, so a reader told
	// the session had found nothing to start would be sent to look at a queue that
	// is fine.
	StallTrackerWait StallReason = "tracker"
	// StallStoreUnreadable is a live session whose last poll could not read the
	// harness's store at all and is reading it again. It is distinct from an idle
	// session because the queue was never read: a reader told the session had found
	// nothing to start would take an outage for an empty queue, and on 2026-09-01
	// that is how a store outage was voiced for its whole length. It is the
	// harness's to clear, by reading again until the store answers or the session
	// gives up on it and stops.
	StallStoreUnreadable StallReason = "unreadable"
	// StallSessionIdle is a live session that is choosing nothing. It is distinct
	// from having no session at all because an operator does an entirely different
	// thing about it, and because telling them to start a session they are already
	// running is worse than telling them nothing.
	StallSessionIdle StallReason = "idle"
	// StallSessionBlocked is a machine refusing starts, with its own cause.
	StallSessionBlocked StallReason = "blocked"
	// StallRedeploying is a session restarting into a build deployed over it: it
	// has found the deploy, its bounded drain has run out with runs still going,
	// and it is stopping and preserving them, or it has already stopped and is
	// being re-executed. It is distinct from an idle session and from no session
	// because an operator does nothing at all about it — the session comes back
	// on its own within a minute and re-adopts what it stopped — and a surface
	// that reported it as either would send somebody to start a session that is
	// already on its way back.
	StallRedeploying StallReason = "redeploying"
	// StallDrainOverrun is a session whose drain bound ran out more than
	// DrainOverrunAfter ago and which has recorded no restart since. Past the
	// bound the session stops the runs it hosts and restarts at once, waiting
	// only on a run at its promotion, so one still there this long and not
	// saying it is waiting on a promotion is a session that stopped restarting
	// rather than one on its way back: it pulls no new work until it does. It is the harness's, because nothing anybody
	// configured keeps it there. On 2026-10-05 a session sat like this for
	// forty minutes, pulling nothing, while every surface read it as working.
	StallDrainOverrun StallReason = "drain-overrun"
	// StallNoWatchSession is a product that was being watched and is not any more.
	StallNoWatchSession StallReason = "stopped"
	// StallUnwatched is a product no session has ever watched. It is not a line
	// that stopped: nothing was choosing work here, so nothing is failing to, and
	// an operator running items by name has a queue by choice.
	StallUnwatched StallReason = "unwatched"
)

// StallReasons is the whole taxonomy, in the order an operator acts on it. A caller
// that has to cover every reason reads it from here rather than repeating the
// list.
func StallReasons() []StallReason {
	return []StallReason{
		StallOperatorHold,
		StallIntakeHold,
		StallProviderAway,
		StallDivergedTarget,
		StallNoCapacity,
		StallProviderWindow,
		StallTrackerWait,
		StallStoreUnreadable,
		StallSessionBlocked,
		StallSessionIdle,
		StallDrainOverrun,
		StallRedeploying,
		StallNoWatchSession,
		StallUnwatched,
	}
}
