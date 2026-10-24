package exchange

// The advisory half of the management loop: what the harness reads before it
// takes a round, and what it reads again after a restart.
//
// Three things are derived here and nothing else is. They are the whole of what
// this file is for, and each of them is a property of the durable record rather
// than of any process that happens to be running.
//
// A restart loses nothing and repeats nothing. A round is on disk before the
// answering provider is invoked, so a process that dies mid-round leaves a round
// that was asked and never answered — which a later pass can tell from a round
// that came back, and from a round somebody is taking right now. The first is
// reclaimed, the second is left alone, and the third belongs to whoever holds
// the lease.
//
// Coordination is bounded. One exchange takes its rounds one at a time, which is
// the lease; across the product the number of exchanges with a round open at
// once is capped, which is the bound below. Neither is enforced here — Survey
// says which exchanges may be carried now and which are queued behind the bound,
// and the harness is what carries them.
//
// Judgments go stale visibly. An exchange records the durable things it was
// asked against and the revision each was at, so a question put against
// something that has since moved is derivable rather than remembered. What that
// produces is a reason to tell the asking role, and nothing else.
//
// # Advisory rather than authoritative
//
// Nothing here invokes a role, and nothing here stops one. Survey reads records
// and returns what the harness may do now; the harness is what invokes, under
// its own lease and its own gates, because it is the only thing that may — the
// harness-is-the-only-role-invoker invariant is why this returns a plan rather
// than taking one. A stale exchange in that plan blocks nothing either: it names
// the asker to tell and what moved underneath it, and what happens next is that
// role's decision or the operator's.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DefaultInFlight is how many exchanges may have a round open at once across one
// product where nothing configures otherwise.
//
// Four is small on purpose. Every exchange in flight is a provider invocation
// the harness is paying for beside the runs it is already paying for, and the
// thing this bounds is the case nobody designs: a morning in which five roles
// each decide they need one more judgment before they can proceed. It is a bound
// on concurrency rather than on how many questions may be asked — an exchange
// held back by it is queued and not refused.
const DefaultInFlight = 4

// MaxReferenceBytes bounds each part of one reference, and MaxReferences bounds
// how many of them one exchange may rest on. Every reference is a revision
// somebody has to keep current, so a record naming fifty of them is a record
// that will be stale the moment it is written.
const (
	MaxReferenceBytes = 200
	MaxReferences     = 32
)

// Reference is one durable thing an exchange was asked against, and the revision
// of it the asking role read.
//
// The revision is the point. A reference without one says which document was
// consulted and nothing about whether what it said then is what it says now, and
// "whether it still says that" is the only question staleness asks.
type Reference struct {
	// What names the kind of thing referred to — an artifact, a work item, a run.
	// It is free text rather than a closed set, because what a role reads before
	// it asks something is not this package's to enumerate.
	What string `json:"what"`
	ID   string `json:"id"`
	// Revision is what that thing was at when it was read. Anything the producer
	// can compare for equality serves: a revision timestamp, a content hash, a
	// tracker's own version. It is optional: a reference naming none is attached
	// as the record is now, and its staleness cannot be judged.
	Revision string `json:"revision,omitempty"`
}

// Key names the thing referred to without the revision. It is how a reference is
// looked up against what is current now.
func (r Reference) Key() string { return r.What + "/" + r.ID }

func (r Reference) validate(what string) error {
	return errors.Join(
		boundedText(what+" what", r.What, MaxReferenceBytes, true),
		boundedText(what+" id", r.ID, MaxReferenceBytes, true),
		boundedText(what+" revision", r.Revision, MaxReferenceBytes, false),
	)
}

// validateReferences holds one list of references to the bounds above.
func validateReferences(field string, references []Reference) error {
	var problems []error
	if len(references) > MaxReferences {
		problems = append(problems, fmt.Errorf("%d %s are recorded, limit is %d", len(references), field, MaxReferences))
	}
	for i, reference := range references {
		problems = append(problems, reference.validate(fmt.Sprintf("%s[%d]", field, i)))
	}
	return errors.Join(problems...)
}

// Moved is one reference an exchange was asked against that has since changed.
type Moved struct {
	Reference Reference
	// Now is what that thing is at currently. It is quoted beside the revision the
	// asker read so a reader can see the two rather than being told they differ.
	Now string
}

// Bounds is what the harness will allow this pass. A zero value takes the
// defaults, which is what a caller that has nothing to say about concurrency
// gets.
type Bounds struct {
	// InFlight caps how many exchanges may have a round open at once across the
	// product. Anything below one takes DefaultInFlight.
	InFlight int
}

func (b Bounds) inFlight() int {
	if b.InFlight < 1 {
		return DefaultInFlight
	}
	return b.InFlight
}

// State is everything one survey reads.
type State struct {
	// Exchanges are every recorded exchange, closed ones included. A closed one
	// is read and skipped rather than left out, so a caller need not filter
	// before it asks.
	Exchanges []Exchange
	// Carried names the exchanges a live process is holding the lease on, by
	// identifier. They are the ones this pass may not touch: the process holding
	// one is mid-round, and a second pass acting on it is the double delivery the
	// lease exists to prevent.
	Carried map[string]bool
	// Revisions is what everything the records name is at now, by reference key.
	// A survey given none was not asked about staleness and judges none — silence
	// is not evidence that something held still, so a reference nothing current is
	// known about is named as unjudged rather than as unmoved.
	Revisions map[string]string
	Bounds    Bounds
}

// Reclamation is one exchange whose round was asked and never answered, by a
// process that is no longer there.
//
// It is recovery rather than delivery: the round was spent and stays spent, and
// what reclaiming it does is say why it produced nothing, so the record stops
// reading as a question somebody is still working on.
type Reclamation struct {
	ExchangeID string
	// Round is which round was interrupted, and Holder is the process that was
	// carrying it where the record names one.
	Round   int
	Holder  string
	Because string
}

// Exhaustion is one exchange that has spent every round it was opened with and
// has not closed.
//
// The conductor closes an exchange that reaches its cap at the moment somebody
// asks it something further. Nothing closes one that reached the cap and was
// never asked again — a thread whose last round died, or whose asker simply
// stopped — so it sits open for ever and the operator is never told. That is
// what this ending is for.
type Exhaustion struct {
	ExchangeID string
	Rounds     int
	Cap        int
}

// Delivery is one exchange the harness may take a round on now: it is open, it
// has rounds left, nobody is carrying it, and the in-flight bound has room.
type Delivery struct {
	ExchangeID string
	// To is the role that would be asked, and Round is which round it would be.
	To    domain.AgentRole
	Round int
	// Reclaimed marks a delivery on an exchange this same pass reclaimed, so what
	// acts on the plan knows the round before it was interrupted rather than
	// answered.
	Reclaimed bool
}

// Queued is one exchange the harness may not take a round on yet, and why.
type Queued struct {
	ExchangeID string
	Because    string
}

// Staleness is one open exchange asked against something that has since moved.
// It is advisory in the strongest sense: nothing in it stops the exchange, and
// what it produces is a reason to tell the asking role.
type Staleness struct {
	ExchangeID string
	// Tell is the role to tell, which is the asker: it is the one that put the
	// question and the only one that can say whether what moved changes it.
	Tell  domain.AgentRole
	Moved []Moved
	// Unjudged are the references the survey could say nothing current about. They
	// are named rather than counted as unmoved, because a survey that reported
	// silence as stability would be one nobody could trust.
	Unjudged []Reference
}

// Plan is what one survey came to. Every field is something the harness may do
// or should know; none of it has been done.
type Plan struct {
	// Carried are the exchanges a live process holds, in identifier order. They
	// are in the plan rather than left out so a reader can tell an exchange this
	// pass declined to touch from one it never saw.
	Carried  []string
	Reclaim  []Reclamation
	Exhaust  []Exhaustion
	Deliver  []Delivery
	Queued   []Queued
	Stale    []Staleness
	InFlight int
}

// Survey reads the records and says what the harness may do now.
//
// It takes nothing, holds nothing, and invokes nothing. Everything it decides is
// decided from the exchanges it was given and the leases the caller has already
// found out about, so two callers reading the same records reach the same plan.
func Survey(state State) Plan {
	exchanges := append([]Exchange(nil), state.Exchanges...)
	Sort(exchanges)

	plan := Plan{InFlight: state.Bounds.inFlight()}
	// An exchange somebody is carrying is one round already in flight, so it
	// counts against the bound before anything this pass might add to it.
	var open int
	for _, recorded := range exchanges {
		if recorded.Open() && state.Carried[recorded.ID] {
			plan.Carried = append(plan.Carried, recorded.ID)
			open++
		}
	}
	sort.Strings(plan.Carried)

	for _, recorded := range exchanges {
		if !recorded.Open() || state.Carried[recorded.ID] {
			continue
		}
		if stale, found := survey(recorded, state.Revisions); found {
			plan.Stale = append(plan.Stale, stale)
		}
		spent := recorded.Spent()
		reclaimed := false
		// The lease is free, so an unanswered round belongs to a process that is
		// gone rather than to one still working. That is the whole of how a restart
		// tells the two apart, and it is why the reclaim is derived from the lease
		// rather than from a timeout.
		if round, interrupted := recorded.Interrupted(); interrupted {
			plan.Reclaim = append(plan.Reclaim, Reclamation{
				ExchangeID: recorded.ID,
				Round:      round.Number,
				Holder:     round.Holder,
				Because:    reclaimBecause(round.Holder),
			})
			reclaimed = true
		}
		// The cap is read against what the record will say once this pass has
		// finished with it. A round that was interrupted is still a round spent, so
		// an exchange reclaimed onto its cap is exhausted in the same pass rather
		// than in the next one.
		if spent >= recorded.MaxRounds {
			plan.Exhaust = append(plan.Exhaust, Exhaustion{
				ExchangeID: recorded.ID,
				Rounds:     spent,
				Cap:        recorded.MaxRounds,
			})
			continue
		}
		if open >= plan.InFlight {
			plan.Queued = append(plan.Queued, Queued{
				ExchangeID: recorded.ID,
				Because: fmt.Sprintf("%d exchange(s) already have a round open, which is the whole of what this product allows at once",
					open),
			})
			continue
		}
		plan.Deliver = append(plan.Deliver, Delivery{
			ExchangeID: recorded.ID,
			To:         recorded.Answerer.Role,
			Round:      spent + 1,
			Reclaimed:  reclaimed,
		})
		open++
	}
	return plan
}

// survey derives what has moved under one exchange since it was asked.
func survey(recorded Exchange, revisions map[string]string) (Staleness, bool) {
	if len(recorded.Refers) == 0 {
		return Staleness{}, false
	}
	stale := Staleness{ExchangeID: recorded.ID, Tell: recorded.Asker.Role}
	for _, reference := range recorded.Refers {
		current, known := revisions[reference.Key()]
		switch {
		case !known, strings.TrimSpace(reference.Revision) == "":
			stale.Unjudged = append(stale.Unjudged, reference)
		case current != reference.Revision:
			stale.Moved = append(stale.Moved, Moved{Reference: reference, Now: current})
		}
	}
	if len(stale.Moved) == 0 && len(stale.Unjudged) == 0 {
		return Staleness{}, false
	}
	return stale, true
}

// reclaimBecause says what happened to an interrupted round, naming the process
// that was carrying it where the record has one. A round recorded before holders
// were written down says so rather than naming nobody, since "no holder" and
// "the holder was not recorded" are different facts and only the first is about
// this round.
func reclaimBecause(holder string) string {
	if strings.TrimSpace(holder) == "" {
		return "the process carrying it is gone and the record does not name it; nothing came back"
	}
	return fmt.Sprintf("%s was carrying it and is gone; nothing came back", holder)
}
