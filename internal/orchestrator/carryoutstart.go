package orchestrator

import (
	"context"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Whether a decided repair or re-run a pull fired has actually begun, told back
// to the pull before it gives the next decision a slot.
//
// A pull fires each decision it has a slot for in a goroutine and counts the
// slot taken the moment it fires. A decision whose carry-out a gate then refuses
// started nothing and gave the slot straight back, but the pull had already
// passed every decision behind it over as having no slot. The docket is walked
// oldest decision first, so an old decision refused on every pass kept every
// decision recorded after it from ever starting (yoyodyne-ifd.428.115).
//
// So the pull waits, briefly, for each decided recovery it fired to say either
// that its run has begun — a re-run whose fresh run has reserved its slot, or a
// stopped run put back in flight by a repair — or that the carry-out came
// back without starting one.
// One that came back gives its slot to the next decision on the docket, or to
// the queue, in the same pull. One that says neither within the bound is
// counted as holding its slot, which is what every fired decision was before.

// carryOutStartWait bounds how long a pull waits for one round of fired
// decisions to say whether they began. A refusal is read from records and comes
// back in moments; the bound is set past the tracker's own slowest answer to the
// reads a carry-out makes before its run begins.
const carryOutStartWait = 2 * time.Minute

// carryOutBegunKey is the context key the notice that a carried-out run has
// begun travels under.
type carryOutBegunKey struct{}

// withCarryOutBegun is a carry-out's context carrying what its action calls once
// its run has begun. It travels on the context for the reason the landing
// notice does (withLandingNotice): the pull that waits on it hosts the carry-out,
// and the run that calls it is built by whoever wired the action.
func withCarryOutBegun(ctx context.Context, begun func()) context.Context {
	return context.WithValue(ctx, carryOutBegunKey{}, begun)
}

// carryOutBegun tells the pull that fired this carry-out, where one asked, that
// its run has begun.
func carryOutBegun(ctx context.Context) {
	if begun, wired := ctx.Value(carryOutBegunKey{}).(func()); wired && begun != nil {
		begun()
	}
}

// carryOutStarts is a carry-out whose actions say when the run they start has
// begun (carryOutBegun). A pull waits on a fired decision only where the
// carry-out says so, because one that never calls the notice would hold every
// pull for the whole bound. It is satisfied by CarryOut.
type carryOutStarts interface {
	SaysWhenStarted() bool
}

// SaysWhenStarted reports that the re-run and the repair this carries out tell
// the pull when their run has begun: the re-run as its fresh run reserves its
// slot, the repair as it puts the stopped run back in flight.
func (c CarryOut) SaysWhenStarted() bool { return true }

// carryOutSignal is what one fired decision tells the pull first: that its run
// began, or what its carry-out came back with. Only the first is kept.
type carryOutSignal struct {
	task CarryOutTask
	// tookSlot is whether firing it took one of the pull's free slots, which
	// is what is given back where it started nothing.
	tookSlot bool
	once     sync.Once
	said     chan carryOutStart
}

// carryOutStart is one fired decision's first word to the pull.
type carryOutStart struct {
	began   bool
	carried CarriedOut
}

func newCarryOutSignal(task CarryOutTask, tookSlot bool) *carryOutSignal {
	return &carryOutSignal{task: task, tookSlot: tookSlot, said: make(chan carryOutStart, 1)}
}

func (s *carryOutSignal) begun() {
	s.once.Do(func() { s.said <- carryOutStart{began: true} })
}

func (s *carryOutSignal) returned(carried CarriedOut) {
	s.once.Do(func() { s.said <- carryOutStart{carried: carried} })
}

// awaitCarryOutStarts waits for each of one round of fired decisions to say
// whether it began, up to bound in all. It returns the ones that came back
// without starting a run, and whether any of those was refused because every
// developer slot is taken — after which nothing more in this pull can start
// either, so the pull stops giving slots back. A decision that says nothing
// within the bound is counted as having begun.
func awaitCarryOutStarts(ctx context.Context, signals []*carryOutSignal, bound time.Duration) (unstarted []*carryOutSignal, full bool) {
	timer := time.NewTimer(bound)
	defer timer.Stop()
	for _, signal := range signals {
		select {
		case said := <-signal.said:
			if said.began || said.carried.Carried {
				continue
			}
			unstarted = append(unstarted, signal)
			// A run waiting for the one slot it keeps the endpoint pair of
			// says nothing about the others, which stay free for other work.
			if said.carried.Waiting && said.carried.Gate == runstate.TriageGateCapacity && said.carried.WaitsForSlot == 0 {
				full = true
			}
		case <-timer.C:
			return unstarted, full
		case <-ctx.Done():
			return unstarted, full
		}
	}
	return unstarted, full
}

// carryOutStartWait is the bound the scheduler was given, or the default.
func (s Scheduler) carryOutStartWait() time.Duration {
	if s.CarryOutStartWait > 0 {
		return s.CarryOutStartWait
	}
	return carryOutStartWait
}
