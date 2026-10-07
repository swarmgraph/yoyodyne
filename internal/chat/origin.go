package chat

import (
	"regexp"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// The sentences an admission has always written into an item's notes about
// where it came from: trackerProvenance's first line, reportNote, and
// directiveNote. They are read back here only by the one-time backfill of
// origins onto items admitted before origins were recorded as fields.
var (
	admittedByNote     = regexp.MustCompile(`(?m)^Admitted to the backlog(?: in lane \S+)? by the (.+?) in conversation \S+, after turn \d+\.`)
	reportCitedNote    = regexp.MustCompile(`(?m)^Admitted from report (\S+), filed at "[^"]*" by the (.+?): `)
	directiveCitedNote = regexp.MustCompile(`(?m)^In answer to directive ([^\s,]+), received by the `)
)

// OriginFromNotes is the origin an item's notes already state, for an item
// admitted before origins were recorded as fields, and false where the notes do
// not state one exactly. It guesses nothing: the notes have to name the role
// that admitted the work, and the report or directive it answered, in the words
// an admission writes them in. A conversation's notes never said whether the
// operator was speaking or a sweep was running, so work that cites neither a
// report nor a directive is not given an origin at all. Notes naming two
// different admitters, reports, or directives are not given one either.
func OriginFromNotes(notes string) (domain.WorkItemOrigin, bool) {
	admitters := admittedByNote.FindAllStringSubmatch(notes, -1)
	admittedBy, ok := onlyRole(admitters, 1, domain.Roles())
	if !ok {
		return domain.WorkItemOrigin{}, false
	}
	reports := reportCitedNote.FindAllStringSubmatch(notes, -1)
	directives := directiveCitedNote.FindAllStringSubmatch(notes, -1)
	if len(reports) == 0 && len(directives) == 0 {
		return domain.WorkItemOrigin{}, false
	}
	reportID, ok := onlyValue(reports, 1)
	if !ok {
		return domain.WorkItemOrigin{}, false
	}
	var reportedBy domain.AgentRole
	if reportID != "" {
		reportedBy, ok = onlyRole(reports, 2, append(domain.Roles(), report.HarnessReporter))
		if !ok {
			return domain.WorkItemOrigin{}, false
		}
	}
	directiveID, ok := onlyValue(directives, 1)
	if !ok {
		return domain.WorkItemOrigin{}, false
	}
	// The session the work was admitted in is not stated, and it is never what
	// answers here: a report or a directive always takes its place.
	origin := admittedOrigin(domain.AskerOperator, admittedBy, reportID, reportedBy, directiveID)
	if origin.Validate() != nil {
		return domain.WorkItemOrigin{}, false
	}
	return origin, true
}

// onlyValue is the one value a group of matches names, empty where there are
// none, and false where they name more than one.
func onlyValue(matches [][]string, group int) (string, bool) {
	value := ""
	for _, match := range matches {
		if value != "" && match[group] != value {
			return "", false
		}
		value = match[group]
	}
	return value, true
}

// onlyRole is the one role a group of matches names by its title, and false
// where they name none, more than one, or a title no candidate carries.
func onlyRole(matches [][]string, group int, candidates []domain.AgentRole) (domain.AgentRole, bool) {
	title, ok := onlyValue(matches, group)
	if !ok || title == "" {
		return "", false
	}
	for _, candidate := range candidates {
		if RoleTitle(candidate) == title {
			return candidate, true
		}
	}
	return "", false
}
