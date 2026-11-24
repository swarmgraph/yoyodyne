// Package goalreview is the part of the unmet-goal review the harness works out
// without asking any role: when the daily review is due, what the review read
// about each goal, and which goals have not changed since a review last went
// through them (docs/designs/unmet-goal-review.md, sections 1 and 5).
//
// # The fingerprint
//
// For each goal the review records a fingerprint of what it read: the goal's
// wording, the open and recently closed items serving it with their status and
// priority, and the architect's entry for it. Two reviews that read the same
// things for a goal compute the same fingerprint, and any one of those things
// changing changes it. A goal whose fingerprint matches the one recorded by the
// last review that went through it is said in one line, "unchanged since", and
// no provider call is spent on it.
//
// An item counts as recently closed for RecentlyClosed after it closed. An item
// leaving that window changes its goal's fingerprint once, which is the review
// noticing that the goal's recent history moved on.
//
// The architect's entry is what her pass wrote about the goal in the review
// that read it. Until her pass writes one (piece 3 of the design) every goal's
// entry is empty, which fingerprints the same every time.
//
// Nothing here reads the tracker, the goals documents, or the clock: the
// caller hands in what it read and when, so what is decided is decided the same
// way in every test.
package goalreview

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DailyHour is the hour of the operator's local day at which the daily review
// becomes due.
const DailyHour = 9

// RecentlyClosed is how long after it closed an item still counts toward its
// goal's fingerprint.
const RecentlyClosed = 7 * 24 * time.Hour

// MaxReportBytes is what a report is kept to, by listing fewer items; see
// Report. It is well under the bound on the pass message that carries it, so
// the review's pass is not refused for its size, and small enough that the
// record of every kept review is cheap to read at each pull.
const MaxReportBytes = 24 << 10

const (
	// MaxItemsListed is how many unfinished items are listed under one goal.
	MaxItemsListed = 8
	// MaxRaisedListed is how many of the items the operator raised are listed.
	MaxRaisedListed = 20
	maxTitleBytes   = 100
	// maxGoalBytes is the most of a goal's wording a report says, and
	// minGoalBytes the most where the report would otherwise be too long.
	maxGoalBytes = 500
	minGoalBytes = 160
)

// Goal is one goal the review goes through: the name work items record it by
// and its words.
type Goal struct {
	Reference string
	Statement string
}

// Item is one work item as the review reads it.
type Item struct {
	ID       string
	Title    string
	Status   string
	Priority int
	// ClosedAt is when the tracker closed the item, and zero on open work.
	ClosedAt time.Time
	Labels   []string
	// Serves is the reference of the goal the item's attribution resolves to,
	// and empty where it resolves to none.
	Serves string
}

// Closed reports an item that is finished or deleted.
func (i Item) Closed() bool {
	status := strings.TrimSpace(i.Status)
	return status == "closed" || status == "tombstone"
}

// Reading is what one review read.
type Reading struct {
	Goals []Goal
	Items []Item
	// ArchitectEntries is the architect's entry for each goal, by reference.
	// Empty until her pass writes them.
	ArchitectEntries map[string]string
}

// Prior is what the last review that went through a goal recorded about it.
type Prior struct {
	Fingerprint string
	ReviewedAt  time.Time
}

// Outcome is one goal at one review: what it read, and whether that is the
// same as what the last review that went through it read.
type Outcome struct {
	Goal        Goal
	Fingerprint string
	// Serving is the open and recently closed items counted in the fingerprint.
	Serving []Item
	// Unchanged reports a fingerprint equal to the one recorded by the review
	// at Since, which went through the goal.
	Unchanged bool
	Since     time.Time
}

// DailyStart is the most recent moment at or before now that is DailyHour:00
// on the operator's local clock, in the location given.
func DailyStart(now time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.Local
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), DailyHour, 0, 0, 0, location)
	if local.Before(start) {
		start = time.Date(local.Year(), local.Month(), local.Day()-1, DailyHour, 0, 0, 0, location)
	}
	return start
}

// Day names the operator's local day a daily review starting at start is for.
func Day(start time.Time, location *time.Location) string {
	if location == nil {
		location = time.Local
	}
	return start.In(location).Format("2006-01-02")
}

// Serving is the items that count toward a goal's fingerprint at now: the
// open ones serving it, and the ones that closed within RecentlyClosed. They
// are in a stable order, by id.
func Serving(goal Goal, items []Item, now time.Time) []Item {
	var serving []Item
	for _, item := range items {
		if item.Serves == "" || item.Serves != goal.Reference {
			continue
		}
		if strings.TrimSpace(item.Status) == "tombstone" {
			continue
		}
		if item.Closed() && (item.ClosedAt.IsZero() || now.Sub(item.ClosedAt) > RecentlyClosed) {
			continue
		}
		serving = append(serving, item)
	}
	sort.SliceStable(serving, func(a, b int) bool { return serving[a].ID < serving[b].ID })
	return serving
}

// Fingerprint is the digest of what the review read for one goal. Titles are
// left out: retitling an item is not something that moves a goal.
func Fingerprint(goal Goal, serving []Item, architectEntry string) string {
	var canonical strings.Builder
	fmt.Fprintf(&canonical, "goal\x00%s\x00%s\n", goal.Reference, strings.Join(strings.Fields(goal.Statement), " "))
	ordered := append([]Item(nil), serving...)
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].ID < ordered[b].ID })
	for _, item := range ordered {
		fmt.Fprintf(&canonical, "item\x00%s\x00%s\x00%d\n", item.ID, strings.TrimSpace(item.Status), item.Priority)
	}
	fmt.Fprintf(&canonical, "architect\x00%s\n", strings.TrimSpace(architectEntry))
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:])
}

// Compare works out every goal's outcome at now against what earlier reviews
// recorded, in the order the goals were read.
func Compare(reading Reading, prior map[string]Prior, now time.Time) []Outcome {
	outcomes := make([]Outcome, 0, len(reading.Goals))
	for _, goal := range reading.Goals {
		serving := Serving(goal, reading.Items, now)
		outcome := Outcome{
			Goal:        goal,
			Serving:     serving,
			Fingerprint: Fingerprint(goal, serving, reading.ArchitectEntries[goal.Reference]),
		}
		if earlier, found := prior[goal.Reference]; found && earlier.Fingerprint == outcome.Fingerprint && !earlier.ReviewedAt.IsZero() {
			outcome.Unchanged = true
			outcome.Since = earlier.ReviewedAt
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

// Changed is the outcomes a review has to go through.
func Changed(outcomes []Outcome) []Outcome {
	var changed []Outcome
	for _, outcome := range outcomes {
		if !outcome.Unchanged {
			changed = append(changed, outcome)
		}
	}
	return changed
}

// Raised is the open items carrying the label the operator's raising puts on
// an item, by id.
func Raised(items []Item) []Item {
	var raised []Item
	for _, item := range items {
		if item.Closed() || !domain.RaisedByOperator(item.Labels) {
			continue
		}
		raised = append(raised, item)
	}
	sort.SliceStable(raised, func(a, b int) bool { return raised[a].ID < raised[b].ID })
	return raised
}

// UnchangedLine is the one line an unchanged goal gets.
func UnchangedLine(outcome Outcome, location *time.Location) string {
	return unchangedLine(outcome, location, maxGoalBytes)
}

func unchangedLine(outcome Outcome, location *time.Location, goalBytes int) string {
	return fmt.Sprintf("- %s: unchanged since the review at %s", shorten(label(outcome.Goal), goalBytes), Moment(outcome.Since, location))
}

// Moment is a time on the operator's clock with the zone named.
func Moment(at time.Time, location *time.Location) string {
	if location == nil {
		location = time.Local
	}
	return at.In(location).Format("15:04 MST on Jan 2, 2006")
}

// Report is what a review says: the goals it goes through, each with the most
// pressing unfinished items serving it and how many more serve it, the goals
// unchanged since an earlier review in one line each, and the items the
// operator raised on purpose, listed apart.
//
// Every goal is always named, because a goal the review records as gone through
// has to be one its pass was shown. So where the report would pass
// MaxReportBytes it lists fewer items under each goal, down to the counts
// alone, and then shortens the goals' wording.
func Report(outcomes []Outcome, raised []Item, location *time.Location) string {
	for listed := MaxItemsListed; listed >= 0; listed-- {
		if text := report(outcomes, raised, location, listed, maxGoalBytes); len(text) <= MaxReportBytes {
			return text
		}
	}
	return report(outcomes, raised, location, 0, minGoalBytes)
}

func report(outcomes []Outcome, raised []Item, location *time.Location, listed, goalBytes int) string {
	var lines []string
	changed := Changed(outcomes)
	if len(changed) > 0 {
		lines = append(lines, fmt.Sprintf("Goals to go through (%d), each changed since a review last went through it or never gone through:", len(changed)))
		for _, outcome := range changed {
			lines = append(lines, "- "+shorten(label(outcome.Goal), goalBytes))
			lines = append(lines, servingLines(outcome.Serving, listed)...)
		}
	}
	var unchanged []string
	for _, outcome := range outcomes {
		if outcome.Unchanged {
			unchanged = append(unchanged, unchangedLine(outcome, location, goalBytes))
		}
	}
	if len(unchanged) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprintf("Goals unchanged since a review went through them (%d), not gone through again:", len(unchanged)))
		lines = append(lines, unchanged...)
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	if len(raised) == 0 {
		lines = append(lines, "Items the operator raised on purpose: none carry the "+domain.RaisedByOperatorLabel+" label.")
	} else {
		lines = append(lines, fmt.Sprintf("Items the operator raised on purpose (%d), left where he put them:", len(raised)))
		ordered := byPriority(raised)
		shown := min(len(ordered), MaxRaisedListed)
		for _, item := range ordered[:shown] {
			lines = append(lines, "- "+itemLine(item))
		}
		if more := len(ordered) - shown; more > 0 {
			lines = append(lines, fmt.Sprintf("- %d more not listed here; each carries the %s label", more, domain.RaisedByOperatorLabel))
		}
	}
	return strings.Join(lines, "\n")
}

// servingLines is the items serving one goal: the unfinished ones first by
// priority, up to the number listed, and how many it leaves out. Closed items
// are counted and not listed, because they are work nobody can start.
func servingLines(serving []Item, listed int) []string {
	if len(serving) == 0 {
		return []string{"  no open or recently closed work item serves it"}
	}
	var unfinished []Item
	closed := 0
	for _, item := range serving {
		if item.Closed() {
			closed++
			continue
		}
		unfinished = append(unfinished, item)
	}
	unfinished = byPriority(unfinished)
	shown := min(len(unfinished), listed)
	var lines []string
	for _, item := range unfinished[:shown] {
		lines = append(lines, "  "+itemLine(item))
	}
	switch more := len(unfinished) - shown; {
	case len(unfinished) == 0:
		lines = append(lines, fmt.Sprintf("  no unfinished item serves it; %d recently closed not listed here", closed))
	case more > 0 || closed > 0:
		lines = append(lines, fmt.Sprintf("  not listed here: %d more unfinished and %d recently closed", more, closed))
	}
	return lines
}

// byPriority is the items most pressing first, and by id within a priority.
func byPriority(items []Item) []Item {
	ordered := append([]Item(nil), items...)
	sort.SliceStable(ordered, func(a, b int) bool {
		if ordered[a].Priority != ordered[b].Priority {
			return ordered[a].Priority < ordered[b].Priority
		}
		return ordered[a].ID < ordered[b].ID
	})
	return ordered
}

// shorten cuts text to at most limit bytes, on a whole character, marking the
// cut.
func shorten(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const mark = "..."
	return strings.TrimSpace(strings.ToValidUTF8(text[:limit-len(mark)], "")) + mark
}

func label(goal Goal) string {
	statement := strings.Join(strings.Fields(goal.Statement), " ")
	reference := strings.Join(strings.Fields(goal.Reference), " ")
	switch {
	case reference == "" || reference == statement:
		return statement
	case strings.HasPrefix(statement, "["):
		return statement
	case strings.HasPrefix(reference, "[") && strings.HasSuffix(reference, statement):
		// A goal with an identifier is named on work as "[identifier] words".
		return reference
	}
	return "[" + reference + "] " + statement
}

func itemLine(item Item) string {
	line := fmt.Sprintf("%s (P%d, %s)", item.ID, item.Priority, strings.TrimSpace(item.Status))
	if title := strings.Join(strings.Fields(item.Title), " "); title != "" {
		line += ": " + shorten(title, maxTitleBytes)
	}
	return line
}
