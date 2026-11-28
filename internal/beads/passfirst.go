package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// passFirstKey is where the tracker records that the Lead Product Manager
// named an item for one role's next recurring pass to take first. A placement
// (placement.go) orders developer runs among one priority; this orders what a
// role's pass is handed, whatever carries the item's own execution, because a
// step a role owes on a developer's item — taking off a design hold, breaking
// an item out — is never in that role's own conversation queue. It is kept on
// the item, as a placement is, so every harness sharing the repository hands
// the same pass the same item, and an empty value is no request.
const passFirstKey = "yoyodyne_pass_first"

// PassFirstRoles are the roles whose recurring pass reads the work waiting on
// it as one queue, which is what a request to take an item first reorders. A
// program manager's passes are per instance and read their lane instead, so a
// request to one would be put to no pass.
var PassFirstRoles = []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleArchitect}

// PassFirst records that an item is to be taken first by one role's next
// recurring pass: the role that owes a step on it, who named it, when, and
// what the step is and why it cannot wait.
type PassFirst struct {
	Role   domain.AgentRole `json:"role"`
	By     domain.AgentRole `json:"by"`
	At     time.Time        `json:"at"`
	Reason string           `json:"reason"`
}

// Valid reports a request the harness could have written.
func (p PassFirst) Valid() bool {
	return slices.Contains(PassFirstRoles, p.Role) && p.By != "" && !p.At.IsZero() && strings.TrimSpace(p.Reason) != ""
}

// Validate refuses a request before it is written.
func (p PassFirst) Validate() error {
	var problems []error
	if !slices.Contains(PassFirstRoles, p.Role) {
		problems = append(problems, fmt.Errorf("an item can be put first only in a pass of %s, not %q", passFirstRoleNames(), p.Role))
	}
	if !p.By.Valid() {
		problems = append(problems, fmt.Errorf("the request names no role it was made by (%q)", p.By))
	}
	if p.At.IsZero() {
		problems = append(problems, errors.New("the request has no time"))
	}
	reason := strings.TrimSpace(p.Reason)
	switch {
	case reason == "":
		problems = append(problems, errors.New("the request has no reason"))
	case len(reason) > MaxPlacementReasonBytes:
		problems = append(problems, fmt.Errorf("the request's reason is %d bytes, more than the %d it keeps", len(reason), MaxPlacementReasonBytes))
	case strings.ContainsAny(reason, "\r\n"):
		problems = append(problems, errors.New("the request's reason must be one line"))
	}
	return errors.Join(problems...)
}

func passFirstRoleNames() string {
	names := make([]string, 0, len(PassFirstRoles))
	for _, role := range PassFirstRoles {
		names = append(names, string(role))
	}
	return strings.Join(names, " or ")
}

// encode is the metadata value a request is stored as; the zero request stores
// nothing, which ends it.
func (p *PassFirst) encode() (string, error) {
	if p == nil || *p == (PassFirst{}) {
		return "", nil
	}
	stored := *p
	stored.At = stored.At.UTC()
	stored.Reason = strings.TrimSpace(stored.Reason)
	encoded, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("encode the request to take the item first: %w", err)
	}
	return string(encoded), nil
}

// passFirstIn reads the request the tracker records. As with a placement, a
// value the harness could not have written reads as none: a request nobody can
// account for must not put an item ahead of a role's whole order.
func passFirstIn(metadata map[string]json.RawMessage) *PassFirst {
	saved := metadataString(metadata, passFirstKey)
	if saved == "" {
		return nil
	}
	var request PassFirst
	if json.Unmarshal([]byte(saved), &request) != nil || !request.Valid() {
		return nil
	}
	return &request
}

// FirstInPassOf is the request that puts this item first in the role's next
// pass, and nil where none does. Closing the item ends it, whether or not the
// write that records the ending has been made.
func (w WorkItem) FirstInPassOf(role domain.AgentRole) *PassFirst {
	if w.PassFirst == nil || w.Status == statusClosed || w.PassFirst.Role != role {
		return nil
	}
	return w.PassFirst
}

// Line is the one line a pass's listing shows beside a named item, with the
// time in the operator's local zone and the zone named.
func (p PassFirst) Line() string {
	return p.LineIn(time.Local)
}

// LineIn is Line with the zone named.
func (p PassFirst) LineIn(zone *time.Location) string {
	if zone == nil {
		zone = time.Local
	}
	return fmt.Sprintf("named first for the %s's next pass by the %s at %s: %s",
		p.Role.Title(), p.By.Title(), p.At.In(zone).Format("15:04 MST on January 2"), strings.TrimSpace(p.Reason))
}

// samePassFirst reports two requests as the same record, where nil and the
// zero request are both none.
func samePassFirst(stored, wanted *PassFirst) bool {
	none := func(p *PassFirst) bool { return p == nil || *p == (PassFirst{}) }
	if none(stored) || none(wanted) {
		return none(stored) && none(wanted)
	}
	return stored.Role == wanted.Role && stored.By == wanted.By && stored.At.Equal(wanted.At) &&
		stored.Reason == strings.TrimSpace(wanted.Reason)
}
