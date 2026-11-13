package ownership

// Why the scheduler left a developer slot empty while ready work waited, as a
// closed vocabulary. The scheduler names one cause each time it leaves a slot
// empty (orchestrator/emptyslots.go) and the watch log keeps it
// (runstate.EmptySlots); the read model reads it back for the health check and
// `yoyo status` (readmodel.EmptySlots). Who moves each cause is decided here,
// beside the rest of the registry, so no surface decides it for itself.

// KindEmptySlot is a developer slot the scheduler left empty while ready work
// waited. It is not an entry on the attention line: the running line and the
// health check carry it, and they ask the registry who moves it.
const KindEmptySlot Kind = "empty-developer-slot"

// EmptySlotCause is one reason the scheduler left a developer slot empty while
// ready work waited. The set is closed, and a reason the scheduler cannot name
// is EmptySlotNotKnown rather than nothing.
type EmptySlotCause string

const (
	// EmptySlotHold is a hold on new work: the operator's own, or the one the
	// harness's brake placed after runs kept stopping.
	EmptySlotHold EmptySlotCause = "a hold on new work"
	// EmptySlotRule is a rule keeping ready work out: work that overlaps a run
	// in flight, an item this session already tried, a restart onto a new build,
	// the limit on pull requests waiting for a person, the provider's limit, or
	// a machine that refuses every start.
	EmptySlotRule EmptySlotCause = "a rule keeping work out"
	// EmptySlotStillCounted is a slot the scheduler still counts as taken by a
	// run that has nothing in flight on record.
	EmptySlotStillCounted EmptySlotCause = "a slot still counted as taken"
	// EmptySlotNothingEligible is ready work every item of which was passed
	// over for something about the item itself: held, parked, waiting on other
	// work, and the like.
	EmptySlotNothingEligible EmptySlotCause = "no eligible item"
	// EmptySlotNotKnown is a slot left empty for a reason the scheduler could
	// not name.
	EmptySlotNotKnown EmptySlotCause = "not known"
)

// EmptySlotCauses is the whole vocabulary.
func EmptySlotCauses() []EmptySlotCause {
	return []EmptySlotCause{EmptySlotHold, EmptySlotRule, EmptySlotStillCounted, EmptySlotNothingEligible, EmptySlotNotKnown}
}

// Valid reports whether a token is one of the causes.
func (c EmptySlotCause) Valid() bool {
	for _, known := range EmptySlotCauses() {
		if c == known {
			return true
		}
	}
	return false
}

// emptySlotRule is who moves an empty slot. A hold is whoever the hold's own
// rule says, read with the hold's facts on the record. A rule keeping work out
// lifts itself when what it waits on ends, so it is the harness's. Every other
// cause is the development manager's, because she decides what becomes of
// stopped and held work and of a slot the harness cannot account for.
func emptySlotRule(r Record) (Answer, bool) {
	switch r.EmptySlot {
	case EmptySlotHold:
		if r.Hold == "" {
			r.Hold = IntakeHold
		}
		return holdRule(r)
	case EmptySlotRule:
		return owned(MoverHarness, "nothing: the slot is filled at the first poll after what keeps the work out ends", "")
	case EmptySlotStillCounted:
		return owned(MoverDevelopmentManager, "find why the run is still counted against the slot and have it continued or stopped", "run.recover")
	case EmptySlotNothingEligible:
		return owned(MoverDevelopmentManager, "decide the ready items that were passed over, which each say why", "triage decision")
	case EmptySlotNotKnown:
		return owned(MoverDevelopmentManager, "find why the scheduler left the slot empty; it could not say", "health.observe")
	}
	return Answer{}, false
}
