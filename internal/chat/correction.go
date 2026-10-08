package chat

// Corrections the standing-goal audit admits.
//
// The Lead Product Manager's sweep audits the work closed since its last pass
// against the standing goals, and admits a correction for an item that breaks
// one. A correction is an automatic front-of-queue admission on every cadence,
// so three things hold it in: it names the closed items it corrects, which is
// what the duplicate check matches the next correction against, so one
// violation across several closed items or several passes is one item, widened
// rather than filed again; one pass admits at most maxPassCorrections of them,
// and names the rest in its account as deferred; and it is admitted at
// priority 0, which is what the operator's direction asked for.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/admission"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// maxPassCorrections is how many standing-goal corrections one pass admits. The
// rest wait for a later pass, named in this one's account as deferred. It is
// the sweep account's number, so what a role is told and what is refused agree.
const maxPassCorrections = sweep.MaxPassCorrections

// maxCorrectedItems bounds how many closed items one action names.
const maxCorrectedItems = 10

// correctionPriority is the priority a correction is admitted at.
const correctionPriority = 0

// correctionProblems checks a correction as far as one action can: the closed
// items it names are identifiers, named once each, and not too many; a creation
// naming them asks for no priority but 0. Whether each one is closed needs the
// tracker, so that is asked as the action is carried out.
func (a TrackerAction) correctionProblems() []error {
	if len(a.Corrects) == 0 {
		return nil
	}
	var problems []error
	if len(a.Corrects) > maxCorrectedItems {
		problems = append(problems, fmt.Errorf("corrects names %d closed items, limit is %d", len(a.Corrects), maxCorrectedItems))
	}
	seen := map[string]bool{}
	for i, named := range a.Corrects {
		id := strings.TrimSpace(named)
		if err := beads.ValidateIssueID(id); err != nil {
			problems = append(problems, fmt.Errorf("corrects[%d]: %w", i, err))
			continue
		}
		if seen[id] {
			problems = append(problems, fmt.Errorf("corrects names %s twice", id))
		}
		seen[id] = true
	}
	if a.Action == actionCreate && a.Priority != nil && *a.Priority != correctionPriority {
		problems = append(problems, fmt.Errorf("a standing-goal correction is admitted at priority %d, and this one asks for %d; leave the priority out or give %d", correctionPriority, *a.Priority, correctionPriority))
	}
	return problems
}

// trimmedCorrects is the closed items an action names, blanks dropped.
func trimmedCorrects(named []string) []string {
	var trimmed []string
	for _, id := range named {
		if id = strings.TrimSpace(id); id != "" {
			trimmed = append(trimmed, id)
		}
	}
	return trimmed
}

// correctionFor reads the closed items a creation corrects out of the listing
// the duplicate check already took, and refuses one that names work the tracker
// does not hold as closed, or that would take this pass past its bound. It
// returns nothing for the ordinary creation that corrects nothing.
func (s *Session) correctionFor(action TrackerAction, admitted []beads.WorkItem) ([]beads.WorkItem, string) {
	named := trimmedCorrects(action.Corrects)
	if len(named) == 0 {
		return nil, ""
	}
	byID := make(map[string]beads.WorkItem, len(admitted))
	for _, item := range admitted {
		byID[strings.TrimSpace(item.ID)] = item
	}
	corrected := make([]beads.WorkItem, 0, len(named))
	for _, id := range named {
		item, found := byID[id]
		switch {
		case !found:
			return nil, fmt.Sprintf("nothing was created: the tracker holds no item %s, so there is no closed work for this correction to correct", id)
		case strings.TrimSpace(item.Status) != closedWorkItemStatus:
			return nil, fmt.Sprintf("nothing was created: %s is %s rather than closed, and a standing-goal correction corrects closed work; open work that breaks a standing goal is updated, not corrected beside", id, strings.TrimSpace(item.Status))
		}
		corrected = append(corrected, item)
	}
	if s.pass == "" {
		return corrected, ""
	}
	marker := passCorrectionLine(s.pass)
	already := 0
	for _, item := range admitted {
		if strings.Contains(item.Notes, marker) {
			already++
		}
	}
	if already >= maxPassCorrections {
		return nil, fmt.Sprintf("nothing was created: this pass has already admitted %d standing-goal corrections, the most one pass admits. Name this one in your account's audits as deferred, and a later pass admits it",
			already)
	}
	return corrected, ""
}

// passCorrectionLine is what a correction records about the pass that admitted
// it, which is what the bound on a pass's corrections counts.
func passCorrectionLine(pass string) string {
	return "Admitted as a standing-goal correction on the pass " + strings.TrimSpace(pass) + "."
}

// correctionNote is what a correction records about the closed items it
// corrects, one line each, and is nothing on a creation that corrects none.
func (s *Session) correctionNote(corrected []beads.WorkItem) string {
	if len(corrected) == 0 {
		return ""
	}
	lines := make([]string, 0, len(corrected)+1)
	for _, item := range corrected {
		lines = append(lines, admission.CorrectionLine(item.ID, item.Title))
	}
	if s.pass != "" {
		lines = append(lines, passCorrectionLine(s.pass))
	}
	return "\n\n" + strings.Join(lines, "\n")
}

// correctionClause is what one line about a creation says about the closed
// items it corrects.
func correctionClause(corrected []beads.WorkItem) string {
	if len(corrected) == 0 {
		return ""
	}
	ids := make([]string, 0, len(corrected))
	for _, item := range corrected {
		ids = append(ids, item.ID)
	}
	return ", a standing-goal correction of " + strings.Join(ids, ", ")
}

// widenedCorrection is the lines an update appends to widen a correction to the
// further closed items it covers. Each one is read from the tracker, so a
// correction is never widened to work that is not closed, and an item it
// already names is refused rather than written twice.
func (s *Session) widenedCorrection(ctx context.Context, action TrackerAction) (string, string) {
	target, err := s.options.Tracker.Show(ctx, strings.TrimSpace(action.ID))
	if err != nil {
		return "", "nothing was changed: " + singleLine(fmt.Sprintf("the correction %s could not be read: %v", action.ID, err), maxTrackerFailureBytes)
	}
	already := map[string]bool{}
	for _, id := range admission.Corrected(target) {
		already[id] = true
	}
	var lines []string
	for _, id := range trimmedCorrects(action.Corrects) {
		if already[id] {
			return "", fmt.Sprintf("nothing was changed: %s already corrects %s", target.ID, id)
		}
		item, err := s.options.Tracker.Show(ctx, id)
		if err != nil {
			return "", "nothing was changed: " + singleLine(fmt.Sprintf("the closed item %s could not be read: %v", id, err), maxTrackerFailureBytes)
		}
		if strings.TrimSpace(item.Status) != closedWorkItemStatus {
			return "", fmt.Sprintf("nothing was changed: %s is %s rather than closed, and a correction is widened only to closed work", id, strings.TrimSpace(item.Status))
		}
		lines = append(lines, admission.CorrectionLine(item.ID, item.Title))
	}
	if len(lines) == 0 {
		return "", "nothing was changed: the update names no closed item to widen the correction to"
	}
	return "\n" + strings.Join(lines, "\n"), ""
}
