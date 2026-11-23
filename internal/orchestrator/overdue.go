package orchestrator

// Releases and priority 0 work that have waited more than seven days, as the
// Lead Product Manager's recurring pass is handed them and held to them.
//
// A pass told only "decide whether a release should be cut" can answer "not
// yet" every time it is asked while the release waits on an item that never
// runs, and a parked priority 0 item can sit the same way on a parking reason
// nobody looks at again. So the first turn of her pass is
// handed the latest release tag and its age, each open release item with
// every open item it waits on and how long each has gone without a run, and
// every open priority 0 item that has had no developer run and no closing in
// the past seven days. Each item past seven days is one the pass must act on
// with a tracker action — order or start it, unblock it, correct or lift its
// parking, drop it from the release, retire it — or explain by name; that is
// the operator's direction of November 7, 2026. An account that records
// neither for one is sent back once, naming it, with everything else the pass
// did kept; a pass that still only defers it says so by name on its record.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// overdueAfter is how long a release or a priority 0 item may wait before the
// pass must act on it or explain it.
const overdueAfter = 7 * 24 * time.Hour

// maxPastSevenDaysListed is how many items past seven days one pass is held to: as
// many as one turn's account may carry entries for. The rest, newest waits
// last, are handed to the next pass.
const maxPastSevenDaysListed = sweep.MaxLeft

// maxOverdueTitleBytes bounds one item's title in the section.
const maxOverdueTitleBytes = 120

// RecurringWaitingWork reads what the Lead Product Manager's pass is handed
// about releases and priority 0 work. It changes nothing.
type RecurringWaitingWork interface {
	Waiting(ctx context.Context) (WaitingWork, error)
}

// WaitingWork is one reading of it.
type WaitingWork struct {
	// LatestTag is the newest release tag in the repository and TaggedAt when
	// it was made; both are empty where there is none.
	LatestTag string
	TaggedAt  time.Time
	// Items are every open release item, every open item one of them waits on,
	// and every open priority 0 item.
	Items []WaitingItem
}

// WaitingItem is one open item as the section reads it.
type WaitingItem struct {
	ID       string
	Title    string
	Priority int
	// Release marks an open release item the Lead Product Manager decided.
	Release bool
	// Parking is why the item is parked, and empty where it is not.
	Parking string
	// WaitsOn are the unfinished items the tracker says it waits on.
	WaitsOn []string
	// AdmittedAt is when the tracker recorded the item, LastRun when its most
	// recent developer run started, and ClosedAt when it last closed where it
	// has been reopened since; each is the zero time where there is none.
	AdmittedAt time.Time
	LastRun    time.Time
	ClosedAt   time.Time
}

// idleSince is the last moment anything moved the item: its admission, its
// latest run, or its latest closing.
func (w WaitingItem) idleSince() time.Time {
	latest := w.AdmittedAt
	for _, at := range []time.Time{w.LastRun, w.ClosedAt} {
		if at.After(latest) {
			latest = at
		}
	}
	return latest
}

// overdueItem is one item past seven days the pass must act on or explain, and
// the items a tracker action on which counts as acting on it.
type overdueItem struct {
	id      string
	related []string
}

// overdueCheck holds one pass to the items past seven days it was handed.
type overdueCheck struct {
	items    []overdueItem
	sentBack bool
}

func (c *overdueCheck) any() bool { return c != nil && len(c.items) > 0 }

// deferred is each item the account records no act on that the pass's tracker
// actions bear out, and no explanation for, with why.
func (c *overdueCheck) deferred(actedOn []string, account *sweep.Result) []string {
	if !c.any() {
		return nil
	}
	changed := make(map[string]bool, len(actedOn))
	for _, id := range actedOn {
		changed[strings.TrimSpace(id)] = true
	}
	var deferred []string
	for _, item := range c.items {
		entry, found := sweep.Overdue{}, false
		if account != nil {
			entry, found = account.OverdueEntry(item.id)
		}
		act := sweep.Act(strings.TrimSpace(string(entry.Act)))
		switch {
		case !found:
			deferred = append(deferred, item.id+" (the account records nothing about it)")
		case !act.Valid():
			deferred = append(deferred, fmt.Sprintf("%s (%q is not an act on the list)", item.id, entry.Act))
		case act.ChangesTheTracker() && !anyChanged(changed, item):
			deferred = append(deferred, fmt.Sprintf("%s (the account says %q, but no tracker action of this pass changed it or anything it waits on)", item.id, act))
		}
	}
	return deferred
}

func anyChanged(changed map[string]bool, item overdueItem) bool {
	if changed[item.id] {
		return true
	}
	for _, id := range item.related {
		if changed[id] {
			return true
		}
	}
	return false
}

// sendBack is the message that sends the pass back once to act on or explain
// what it only deferred, and empty where it deferred nothing or was already
// sent back.
func (c *overdueCheck) sendBack(actedOn []string, account *sweep.Result) (string, []string) {
	if !c.any() || c.sentBack || account == nil {
		return "", nil
	}
	deferred := c.deferred(actedOn, account)
	if len(deferred) == 0 {
		return "", nil
	}
	c.sentBack = true
	return sweep.OverdueSentBack(deferred), deferred
}

// record is what the pass's record says where items past seven days are still
// only deferred when the pass ends.
func (c *overdueCheck) record(name string, actedOn []string, account *sweep.Result) string {
	if !c.any() || account == nil {
		return ""
	}
	deferred := c.deferred(actedOn, account)
	if len(deferred) == 0 {
		return ""
	}
	return fmt.Sprintf("the pass of %s ended with %s, waiting more than seven days, only deferred again%s", name, strings.Join(deferred, ", "), overdueSentBackSays(c.sentBack))
}

func overdueSentBackSays(sentBack bool) string {
	if sentBack {
		return ", after it was sent back once to act on each or explain it"
	}
	return ""
}

// overdueSentBackProblem is what the pass's record says of a turn sent back.
func overdueSentBackProblem(name string, turn int, deferred []string) string {
	return fmt.Sprintf("turn %d of the recurring task %s ended with %s, waiting more than seven days, only deferred, so the harness sent the pass back once to act on each or explain it, keeping everything else it did", turn, name, strings.Join(deferred, ", "))
}

// waitingWorkSection reads the releases and priority 0 work and renders what
// the pass is handed, with the check its account is held to.
func (t Trigger) waitingWorkSection(ctx context.Context) (string, *overdueCheck, string) {
	reading, err := t.WaitingWork.Waiting(ctx)
	if err != nil {
		unread := fmt.Sprintf("the releases and the priority 0 work could not be read, so this pass is held to none of them: %v", err)
		return "## Releases and priority 0 work waiting over seven days\n\n" + unread + "\n", nil, unread
	}
	section, check := renderWaitingWork(t.now(), reading)
	return section, check, ""
}

// renderWaitingWork is the section and the check for one reading at now.
func renderWaitingWork(now time.Time, reading WaitingWork) (string, *overdueCheck) {
	byID := make(map[string]WaitingItem, len(reading.Items))
	for _, item := range reading.Items {
		byID[item.ID] = item
	}
	overdue := func(at time.Time) bool { return !at.IsZero() && now.Sub(at) > overdueAfter }
	var section strings.Builder
	section.WriteString("## Releases and priority 0 work waiting over seven days\n\n")
	if reading.LatestTag == "" {
		section.WriteString("No release tag is in this repository.\n")
	} else {
		fmt.Fprintf(&section, "The latest release tag is %s, made %s, %s.\n", reading.LatestTag, localStamp(reading.TaggedAt), ago(now, reading.TaggedAt))
	}
	staleTag := reading.LatestTag == "" || overdue(reading.TaggedAt)
	var due []WaitingItem
	var releases, priority0 []WaitingItem
	for _, item := range reading.Items {
		switch {
		case item.Release:
			releases = append(releases, item)
		case item.Priority == 0:
			priority0 = append(priority0, item)
		}
	}
	section.WriteString("\n")
	if len(releases) == 0 {
		section.WriteString("No release item is open.\n")
	}
	for _, release := range releases {
		late := staleTag && overdue(release.AdmittedAt)
		fmt.Fprintf(&section, "- %s%s, release item, admitted %s%s.", pastMark(late), describeItem(release), ago(now, release.AdmittedAt), parkingSays(release))
		if len(release.WaitsOn) == 0 {
			section.WriteString(" It waits on no open item.\n")
		} else {
			section.WriteString(" It waits on:\n")
			for _, id := range release.WaitsOn {
				blocker, known := byID[id]
				if !known {
					fmt.Fprintf(&section, "  - %s, which could not be read\n", id)
					continue
				}
				fmt.Fprintf(&section, "  - %s: %s%s\n", describeItem(blocker), runSays(now, blocker), parkingSays(blocker))
			}
		}
		if late {
			due = append(due, release)
		}
	}
	section.WriteString("\n")
	var idle []WaitingItem
	for _, item := range priority0 {
		if overdue(item.idleSince()) {
			idle = append(idle, item)
		}
	}
	if len(idle) == 0 {
		section.WriteString("Every open priority 0 item has had a developer run, or a closing, in the past seven days, or was admitted within them.\n")
	} else {
		fmt.Fprintf(&section, "%d open priority 0 item(s) have had no developer run and no closing in the past seven days:\n", len(idle))
		sort.SliceStable(idle, func(i, j int) bool { return idle[i].idleSince().Before(idle[j].idleSince()) })
		for _, item := range idle {
			fmt.Fprintf(&section, "- %s%s, admitted %s, %s%s%s.\n", pastMark(true), describeItem(item), ago(now, item.AdmittedAt), runSays(now, item), parkingSays(item), waitsOnSays(item))
		}
		due = append(due, idle...)
	}
	if len(due) == 0 {
		return section.String(), nil
	}
	check := &overdueCheck{}
	for _, item := range due {
		if len(check.items) == maxPastSevenDaysListed {
			break
		}
		check.items = append(check.items, overdueItem{id: item.ID, related: item.WaitsOn})
	}
	section.WriteString("\nEach item marked PAST SEVEN DAYS is one you act on this pass or explain by name; deferring it again is not an answer.")
	if more := len(due) - len(check.items); more > 0 {
		fmt.Fprintf(&section, " This pass is held to the first %d of them; %d more are handed to your next pass.", len(check.items), more)
	}
	section.WriteString("\n\n" + sweep.OverdueContract() + "\n")
	return section.String(), check
}

func pastMark(late bool) string {
	if late {
		return "PAST SEVEN DAYS: "
	}
	return ""
}

func describeItem(item WaitingItem) string {
	if title := strings.TrimSpace(item.Title); title != "" {
		return fmt.Sprintf("%s (%s)", item.ID, oneline.Fold(title, maxOverdueTitleBytes))
	}
	return item.ID
}

func runSays(now time.Time, item WaitingItem) string {
	if item.LastRun.IsZero() {
		return "never run"
	}
	return fmt.Sprintf("no run for %s, the last started %s", days(now.Sub(item.LastRun)), localStamp(item.LastRun))
}

func parkingSays(item WaitingItem) string {
	if reason := strings.TrimSpace(item.Parking); reason != "" {
		return fmt.Sprintf("; parked: %q", oneline.Fold(reason, maxOverdueTitleBytes*2))
	}
	return ""
}

func waitsOnSays(item WaitingItem) string {
	if len(item.WaitsOn) == 0 {
		return "; waits on no open item"
	}
	return "; waits on " + strings.Join(item.WaitsOn, ", ")
}

// ago says how long before now a moment was, in whole days.
func ago(now, at time.Time) string {
	if at.IsZero() {
		return "at a time the tracker does not give"
	}
	return days(now.Sub(at)) + " ago"
}

func days(span time.Duration) string {
	whole := int(span / (24 * time.Hour))
	switch {
	case whole < 1:
		return "less than a day"
	case whole == 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", whole)
	}
}
