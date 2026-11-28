package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// putFirstProblems holds a "put-first" to what the request can keep: a role
// whose pass reads one queue, and a reason that fits on the one line every
// listing of that pass shows.
func putFirstProblems(a TrackerAction) []error {
	var problems []error
	to := domain.AgentRole(strings.TrimSpace(a.To))
	if !slices.Contains(beads.PassFirstRoles, to) {
		problems = append(problems, fmt.Errorf("put-first requires \"to\", the role whose next pass takes the item first: %s", passFirstRolesText()))
	}
	if reason := strings.TrimSpace(a.Reason); len(reason) > beads.MaxPlacementReasonBytes {
		problems = append(problems, fmt.Errorf("reason is what the pass is told at %d bytes, limit is %d", len(reason), beads.MaxPlacementReasonBytes))
	} else if strings.ContainsAny(reason, "\r\n") {
		problems = append(problems, errors.New("reason is what the pass is told and must be one line"))
	}
	return problems
}

func passFirstRolesText() string {
	quoted := make([]string, 0, len(beads.PassFirstRoles))
	for _, role := range beads.PassFirstRoles {
		quoted = append(quoted, fmt.Sprintf("%q", role))
	}
	return strings.Join(quoted, " or ")
}

// putFirst carries out "put-first": the Lead Product Manager names an item
// whose step a role owes, and that role's next recurring pass is handed it
// before anything else it carries, whatever the item's priority and whoever
// carries the rest of its work. The request ends when a pass of that role
// takes the item (orchestrator/passfirst.go), when the item closes, or when
// she drops it. Naming the item again, for the same role or another, replaces
// the request standing on it, and the note says what it replaced.
func (s *Session) putFirst(ctx context.Context, outcome *TrackerOutcome) {
	id := outcome.WorkItemID
	if outcome.target == nil {
		outcome.fail(fmt.Errorf("cannot put %s first without reading it: %s", id, outcome.TargetUnread))
		return
	}
	request := beads.PassFirst{
		Role:   domain.AgentRole(strings.TrimSpace(outcome.Action.To)),
		By:     s.state.Role,
		At:     s.options.clock().Now(),
		Reason: strings.TrimSpace(outcome.Action.Reason),
	}
	notes := s.trackerProvenance(fmt.Sprintf("Named first for the %s's next pass", request.Role.Title()), outcome.Action.Reason)
	replaced := ""
	if standing := outcome.target.PassFirst; standing != nil && outcome.target.FirstInPassOf(standing.Role) != nil {
		notes += "\n\nIt replaces the request that had it " + standing.Line() + "."
		replaced = "; it replaces the request that had it named first for the " + standing.Role.Title() + "'s pass"
	}
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{PassFirst: &request, AppendNotes: notes}); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("named %s for the %s's next pass to take before anything else in it; the request ends when that role's pass takes it, and while it is left status says so with the reason%s", id, request.Role.Title(), replaced)
}

// dropFirst carries out "drop-first": the Lead Product Manager takes back a
// request she made. An item with none is refused rather than written to.
func (s *Session) dropFirst(ctx context.Context, outcome *TrackerOutcome) {
	id := outcome.WorkItemID
	if outcome.target == nil {
		outcome.fail(fmt.Errorf("cannot drop the request on %s without reading it: %s", id, outcome.TargetUnread))
		return
	}
	standing := outcome.target.PassFirst
	if standing == nil || outcome.target.FirstInPassOf(standing.Role) == nil {
		outcome.Failure = fmt.Sprintf("%s is not named first for any role's pass, so there was no request to drop; nothing was changed", id)
		return
	}
	change := beads.WorkItemChange{
		PassFirst: &beads.PassFirst{},
		AppendNotes: s.trackerProvenance(fmt.Sprintf("Request to take it first in the %s's next pass dropped", standing.Role.Title()), outcome.Action.Reason) +
			"\n\nIt had been " + standing.Line() + ".",
	}
	if _, err := s.options.Tracker.Update(ctx, id, change); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("dropped the request that had %s taken first in the %s's next pass; it goes back to wherever that role's order puts it", id, standing.Role.Title())
}
