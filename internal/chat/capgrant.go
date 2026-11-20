package chat

// The Lead Product Manager's grant of a further raise of one item's limit.
//
// The development manager raises an item's limit herself five times
// (runstate.MaxDelegatedCapCrossings). Past those five, whether the item is worth
// more spending is a judgment of its value against the other work waiting, and
// the architect's ruling gives that judgment to the Lead Product Manager: she may
// grant up to five more raises for the item, each the same size as one of the
// development manager's and each with her reason, and no role raises an item past
// ten in all (`docs/designs/stopped-run-recovery.md`, the ruling on raises past
// the development manager's five). Until this action existed her decision waited
// on the operator typing `yoyo triage override`.
//
// A grant is carried out as it is recorded, with nobody typing anything: the raise
// is written to the item's durable triage record, a note naming it lands on the
// item, and the operator is told afterwards in the channel. Like the development
// manager's crossing it spends nothing and buys no attempt: the decision it makes
// recordable — a repair, a re-run — is still the development manager's to record.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CapGrants records the Lead Product Manager's grant of a further raise of one
// item's limit. It is satisfied by a caller that supplies the configured caps and
// the clock, and names her as the one granting, for the reason TriageBudgets
// names the development manager: whose authority a record is written under is a
// fact about the wiring, and a conversation that could state it could state any
// of them.
type CapGrants interface {
	GrantCap(ctx context.Context, workItemID, budget, reason string) (runstate.TriageCrossing, error)
}

// grantProblems holds a grant to naming the cap it raises and the reason for it.
// The budget is required rather than inferred, for the reason a crossing's is: an
// item can be refused by two caps, and a grant that guessed would raise one while
// the other went on refusing.
func (a TrackerAction) grantProblems() []error {
	var problems []error
	switch budget := strings.TrimSpace(a.Budget); {
	case budget == "":
		problems = append(problems, fmt.Errorf("grant requires \"budget\", the cap being raised: %s",
			strings.Join(runstate.TriageOverrideBudgets(), ", ")))
	case !slices.Contains(runstate.TriageOverrideBudgets(), budget):
		problems = append(problems, fmt.Errorf("grant budget %q is not a cap; the caps are %s",
			budget, strings.Join(runstate.TriageOverrideBudgets(), ", ")))
	}
	if strings.TrimSpace(a.Reason) == "" {
		problems = append(problems, errors.New("grant requires \"reason\", why the item is worth another raise: it is recorded on the item as your decision and reported to the operator"))
	}
	return problems
}

// carryOutCapGrant records one grant and writes it onto the item.
//
// The raise is written before the note, the order the development manager's
// crossing keeps: a process that dies between the two has raised a cap it did not
// write down, rather than written down a raise nothing permits, and the durable
// triage record carries the reason either way.
func (s *Session) carryOutCapGrant(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	budget := strings.TrimSpace(action.Budget)
	if s.options.CapGrants == nil {
		outcome.fail(errors.New("no triage record is wired to this conversation, so a raise cannot be granted and nothing was recorded"))
		return
	}
	granted, err := s.options.CapGrants.GrantCap(ctx, id, budget, strings.TrimSpace(action.Reason))
	if err != nil {
		// Each refusal says what to do instead — the raise is still the development
		// manager's, or ten is reached and the item is to be rewritten, split or
		// closed — so it is kept whole rather than cut to a line.
		outcome.refused(err)
		return
	}
	outcome.noteLanded("the %s cap for %s is raised to %d on the item's durable triage record, grant %d of %d past the development manager's own, and granting again would spend another",
		granted.Budget, id, granted.Cap, granted.Number, granted.Bound)
	cost := "a cost the record does not carry"
	if granted.CostSoFar != nil {
		cost = granted.CostSoFar.Text()
	}
	verb := fmt.Sprintf("Granted: the %s cap raised to %d on the Lead Product Manager's decision, which is grant %d of %d past the development manager's own %d raises, and raise %d of the %d no role may pass for this item. The item has cost %s so far",
		granted.Budget, granted.Cap, granted.Number, granted.Bound, runstate.MaxDelegatedCapCrossings,
		granted.Counters.RoleRaises(), runstate.MaxRoleCapRaises, cost)
	note := s.trackerProvenance(verb, action.Reason)
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, id, note, "the grant recorded on the item")
		return
	}
	// The figures travel with the outcome so that what the operator is told in
	// the channel afterwards is the record's own account.
	outcome.Crossing = &CapCrossing{
		Budget:    granted.Budget,
		Cap:       granted.Cap,
		Crossing:  granted.Number,
		Crossings: granted.Bound,
	}
	if granted.CostSoFar != nil {
		outcome.Crossing.CostSoFar = &ItemCost{
			TotalUSD:    granted.CostSoFar.TotalUSD,
			Runs:        granted.CostSoFar.Runs,
			UnknownRuns: granted.CostSoFar.UnknownRuns,
		}
	}
	outcome.applied("granted %s another raise of its %s cap, to %d, which is grant %d of %d past the development manager's own and raise %d of the %d no role may pass, with the item at %s so far; the operator is told afterwards. Nothing was spent and no attempt was bought: the development manager records the decision it permits",
		id, granted.Budget, granted.Cap, granted.Number, granted.Bound, granted.Counters.RoleRaises(), runstate.MaxRoleCapRaises, cost)
}
