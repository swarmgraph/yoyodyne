// Package epicwake paces the passes of the Lead Product Manager, the development
// manager and the architect while epic mode is on, as the section "When each role
// runs: piece 13" of docs/designs/epic-mode-second-part.md says. A pass is not
// started by the clock then: it is started by an event of an open epic that the
// role owes something for.
//
// The events themselves are worked out afresh at each pull from the records the
// read model works out where each epic stands from (readmodel.EpicWakeEvents).
// This package keeps only what pacing them needs, and the passes it started with
// what each cost, in one file of its own that only the harness writes:
//
//   - an event is gathered for Gather, so several at once cost one pass;
//   - it is settled when its cause is, which is when it is no longer owed;
//   - a pass that ends without settling it is followed by another after Again;
//   - after PassesBeforeMoving such passes it goes to the next role up, and from
//     the Lead Product Manager to status as a problem, where no pass follows.
package epicwake

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
)

const (
	// Gather is how long an event waits for others, so several at once cost one
	// pass.
	Gather = 10 * time.Minute
	// Again is how long after a pass that did not settle an event the next one
	// is started for it.
	Again = time.Hour
	// PassesBeforeMoving is how many passes of one role an event may wake
	// without being settled before it goes to the next role up.
	PassesBeforeMoving = 2
)

// Roles is the roles an event can wake, in the order a pull looks at them.
// A program manager is woken by none.
var Roles = []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect}

// Event is one thing a role owes a step for. Key names its cause, and is the
// same at every reading while the cause stands. Namings are the epics it is for,
// which the pass it wakes is charged to; a critical report about no member is
// for none of them.
type Event struct {
	Key     string                `json:"key"`
	Role    domain.AgentRole      `json:"role"`
	Namings []epicprogress.Naming `json:"namings,omitempty"`
	Says    string                `json:"says"`
}

// Kept is an event as the file keeps it. Role is who it is with now, which is
// the role it was owed to until it moved up, and From that earlier role.
type Kept struct {
	Event
	Seen time.Time `json:"seen"`
	// Woken is when each pass of Role was started for it since it reached Role.
	Woken []time.Time `json:"woken,omitempty"`
	// From is the role it moved up from, where it did.
	From domain.AgentRole `json:"from,omitempty"`
	// Raised is when it was put on status as a problem, after the Lead Product
	// Manager's passes did not settle it. Nothing more is started for it.
	Raised *time.Time `json:"raised,omitempty"`
}

// Pass is one pass an event started, with the epics it was for and, once it
// ended, what it cost.
type Pass struct {
	ID      string                `json:"id"`
	Role    domain.AgentRole      `json:"role"`
	Namings []epicprogress.Naming `json:"namings,omitempty"`
	At      time.Time             `json:"at"`
	Ended   *Ended                `json:"ended,omitempty"`
}

// Ended is what a pass cost. Known is false where the provider did not report
// what all of it cost.
type Ended struct {
	At      time.Time `json:"at"`
	CostUSD float64   `json:"cost_usd"`
	Known   bool      `json:"known"`
}

// Record is the file.
type Record struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Events        []Kept           `json:"events,omitempty"`
	Passes        []Pass           `json:"passes,omitempty"`
}

// SchemaVersion is the shape of the file.
const SchemaVersion = 1

// Due is what one role's next pass is for, and when it fell due.
type Due struct {
	Events []Kept
	At     time.Time
}

// settle takes in what is owed now: an event no longer owed is settled and
// dropped, a new one is kept from now, and one whose passes did not settle it
// moves up. It reports whether anything changed.
func (r *Record) settle(owed []Event, now time.Time) bool {
	byKey := make(map[string]Event, len(owed))
	for _, event := range owed {
		if _, seen := byKey[event.Key]; !seen {
			byKey[event.Key] = event
		}
	}
	changed := false
	kept := r.Events[:0]
	for _, event := range r.Events {
		standing, owedStill := byKey[event.Key]
		if !owedStill {
			changed = true
			continue
		}
		delete(byKey, event.Key)
		if event.Says != standing.Says || !slices.Equal(event.Namings, standing.Namings) {
			event.Says, event.Namings, changed = standing.Says, standing.Namings, true
		}
		if event.Raised == nil && len(event.Woken) >= PassesBeforeMoving && !now.Before(event.Woken[len(event.Woken)-1].Add(Again)) {
			event.moveUp(now)
			changed = true
		}
		kept = append(kept, event)
	}
	for _, event := range owed {
		if _, fresh := byKey[event.Key]; fresh {
			delete(byKey, event.Key)
			kept = append(kept, Kept{Event: event, Seen: now})
			changed = true
		}
	}
	r.Events = kept
	return changed
}

// moveUp gives the event to the next role up, or puts it on status.
func (k *Kept) moveUp(now time.Time) {
	switch k.Role {
	case domain.RoleDevelopmentManager, domain.RoleArchitect:
		k.From, k.Role, k.Woken, k.Seen = k.Role, domain.RoleProductManager, nil, now
	default:
		raised := now
		k.Raised = &raised
	}
}

// due is when the event next owes a pass, and false where it owes none.
func (k Kept) due() (time.Time, bool) {
	switch {
	case k.Raised != nil:
		return time.Time{}, false
	case len(k.Woken) == 0:
		return k.Seen.Add(Gather), true
	default:
		return k.Woken[len(k.Woken)-1].Add(Again), true
	}
}

// Due is each role whose pass is owed at now, with every event of that role the
// pass is for: once one of its events is due, the pass carries each that has not
// woken a pass within the hour, so events that came close together cost one.
func (r Record) Due(now time.Time) map[domain.AgentRole]Due {
	owed := map[domain.AgentRole]Due{}
	for _, role := range Roles {
		var due Due
		ready := false
		for _, event := range r.Events {
			at, owes := event.due()
			if event.Role != role || !owes {
				continue
			}
			if !now.Before(at) {
				if !ready || at.Before(due.At) {
					due.At = at
				}
				ready = true
			}
			if len(event.Woken) == 0 || !now.Before(at) {
				due.Events = append(due.Events, event)
			}
		}
		if ready {
			owed[role] = due
		}
	}
	return owed
}

// woken records that a pass of role was started for the events, by key.
func (r *Record) woken(role domain.AgentRole, pass string, keys []string, now time.Time) error {
	if pass == "" {
		return errors.New("the pass is not named")
	}
	if slices.ContainsFunc(r.Passes, func(kept Pass) bool { return kept.ID == pass }) {
		return nil
	}
	started := Pass{ID: pass, Role: role, At: now}
	for i, event := range r.Events {
		if event.Role != role || !slices.Contains(keys, event.Key) {
			continue
		}
		r.Events[i].Woken = append(r.Events[i].Woken, now)
		for _, naming := range event.Namings {
			if !slices.Contains(started.Namings, naming) {
				started.Namings = append(started.Namings, naming)
			}
		}
	}
	r.Passes = append(r.Passes, started)
	return nil
}

// ended records what a pass cost.
func (r *Record) ended(pass string, ended Ended) error {
	at := slices.IndexFunc(r.Passes, func(kept Pass) bool { return kept.ID == pass })
	if at < 0 {
		return fmt.Errorf("no pass %s was started for an event of a named epic", pass)
	}
	r.Passes[at].Ended = &ended
	return nil
}

// Raised is each event no pass settled, which status shows as a problem.
func (r Record) Raised() []Kept {
	var raised []Kept
	for _, event := range r.Events {
		if event.Raised != nil {
			raised = append(raised, event)
		}
	}
	return raised
}

// Cost is what the passes woken for one naming cost: the known part, with a pass
// woken for several namings shared equally between them, how many passes there
// were, and how many of them could not be priced, which add nothing to the known
// part.
func (r Record) Cost(naming epicprogress.Naming) (knownUSD float64, passes, unknown int) {
	for _, pass := range r.Passes {
		if !slices.Contains(pass.Namings, naming) {
			continue
		}
		passes++
		if pass.Ended == nil || !pass.Ended.Known {
			unknown++
		}
		if pass.Ended != nil {
			knownUSD += pass.Ended.CostUSD / float64(len(pass.Namings))
		}
	}
	return knownUSD, passes, unknown
}

func (r Record) validate(product domain.ProductID) error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("the record of epic events is version %d, and this build reads version %d", r.SchemaVersion, SchemaVersion)
	}
	if r.ProductID != product {
		return fmt.Errorf("the record of epic events is for product %q, not %q", r.ProductID, product)
	}
	for _, event := range r.Events {
		if event.Key == "" || !slices.Contains(Roles, event.Role) || event.Seen.IsZero() {
			return fmt.Errorf("the event %q is not whole", event.Key)
		}
	}
	for _, pass := range r.Passes {
		if pass.ID == "" || pass.At.IsZero() {
			return fmt.Errorf("the pass %q is not whole", pass.ID)
		}
	}
	return nil
}
