package readmodel

// The events of the named epics that start a role's pass while epic mode is on:
// docs/designs/epic-mode-second-part.md, "When each role runs: piece 13".
// internal/epicwake paces them; this says which are owed, from the same reading
// of where each epic stands that status shows.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/epicwake"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// EpicWakeEvents is each event owed now:
//
//   - every step status names as owed by the Lead Product Manager, the
//     development manager or the architect in an open epic, which is how each
//     step the design's list gives a role reaches it;
//   - every unhandled report waiting on one of those roles that names an item of
//     an open epic, and one waiting on a program manager that does, which goes
//     to the Lead Product Manager, who may handle any report;
//   - every critical report waiting on the development manager or the architect,
//     whatever it names; one waiting on the Lead Product Manager or a program
//     manager is not an event, because it is delivered to her as a pass of its
//     own as every critical report is;
//   - each comment left after an epic was delivered, for the Lead Product
//     Manager, while some epic is open and not delivered.
//
// A step owed by the operator, the reviewer, the harness or something outside
// is no role's event, so an epic that waits only on those starts nobody.
func EpicWakeEvents(epics *Epics, record epicscope.Record, view epicprogress.View, reports []report.Report, handlings []report.Handling) []epicwake.Event {
	if epics == nil || !record.On() {
		return nil
	}
	var events []epicwake.Event
	working := false
	for _, open := range epics.Open {
		if !open.delivered() {
			working = true
		}
	}
	for _, open := range epics.Open {
		epic, named := record.Named(open.WorkItemID)
		if !named || !epic.Open() {
			continue
		}
		naming := epicprogress.Of(epic)
		for _, wait := range open.Waits {
			role, owes := wakesRole(wait.Owner)
			if !owes {
				continue
			}
			events = append(events, epicwake.Event{
				Key:  "step:" + namingKey(naming) + ":" + digest(string(role), wait.WorkItemID, wait.Says),
				Role: role, Namings: []epicprogress.Naming{naming},
				Says: fmt.Sprintf("epic %s: %s", epic.WorkItemID, wait.Says),
			})
		}
		if working {
			for _, feedback := range open.Feedback {
				events = append(events, epicwake.Event{
					Key:  "feedback:" + namingKey(naming) + ":" + feedback.Comment,
					Role: domain.RoleProductManager, Namings: []epicprogress.Naming{naming},
					Says: fmt.Sprintf("epic %s: feedback %s", epic.WorkItemID, feedback.Line(epic.WorkItemID)),
				})
			}
		}
	}
	for _, reported := range report.ByFiling(report.Unhandled(reports, handlings)) {
		if reported.PassFailureTask != "" {
			continue
		}
		role := reported.Addressee()
		holder, member := holdingEpic(record, view, reported.WorkItemID)
		critical := reported.Severity == report.SeverityCritical
		if critical && (role == domain.RoleProductManager || role == domain.RoleProgramManager) {
			// Delivered to the Lead Product Manager as every critical report is,
			// once, as a pass of its own (internal/orchestrator, criticalreports.go).
			continue
		}
		switch {
		case member && role == domain.RoleProgramManager:
			role = domain.RoleProductManager
		case member && slices.Contains(epicwake.Roles, role):
		case critical && (role == domain.RoleDevelopmentManager || role == domain.RoleArchitect):
		default:
			continue
		}
		event := epicwake.Event{Key: "report:" + reported.ID, Role: role,
			Says: fmt.Sprintf("report %s (%s): %s", reported.ID, reported.Severity, singleLine(reported.Message, maxEpicReasonBytes))}
		if member {
			event.Namings = []epicprogress.Naming{epicprogress.Of(holder)}
			event.Says = fmt.Sprintf("epic %s: %s, about %s", holder.WorkItemID, event.Says, reported.WorkItemID)
		}
		events = append(events, event)
	}
	return events
}

// wakesRole is the role a step's owner is, where an event of it starts a pass.
func wakesRole(owner Mover) (domain.AgentRole, bool) {
	for _, role := range epicwake.Roles {
		if owner == MoverOf(role) {
			return role, true
		}
	}
	return "", false
}

// holdingEpic is the open epic an item is in, or is.
func holdingEpic(record epicscope.Record, view epicprogress.View, item string) (epicscope.Epic, bool) {
	if item == "" {
		return epicscope.Epic{}, false
	}
	for _, epic := range record.OpenEpics() {
		if epic.WorkItemID == item || slices.Contains(view.Items(epic), item) {
			return epic, true
		}
	}
	return epicscope.Epic{}, false
}

func namingKey(naming epicprogress.Naming) string {
	return naming.WorkItemID + "@" + naming.NamedAt.UTC().Format("20060102T150405.000000000Z")
}

func digest(parts ...string) string {
	sum := sha256.New()
	for _, part := range parts {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}
