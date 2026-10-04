package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every item held after a stopped run says since when it has been held, read
// from the record that holds it, and the held items are listed oldest hold
// first, whatever order the product manager's priorities put them in. On
// 2026-09-27 thirty-four held items read alike, and nothing said which of them
// had waited longest.
func TestHeldItemsSaySinceWhenAndAreListedOldestHoldFirst(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	stopped := func(hoursAgo int) *time.Time {
		at := moment.Add(-time.Duration(hoursAgo) * time.Hour)
		return &at
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"": {
				{ID: "yoyodyne-ifd.2", Title: "not offered", Status: "open"},
				{ID: "yoyodyne-ifd.1", Title: "held two hours", Status: "blocked"},
				{ID: "yoyodyne-ifd.3", Title: "held nine days", Status: "blocked"},
				{ID: "yoyodyne-ifd.4", Title: "held three days", Status: "blocked"},
			},
			"open": {
				// An open item the tracker does not offer is refused for something
				// other than a hold, and keeps its place in the order.
				{ID: "yoyodyne-ifd.2", Title: "not offered", Status: "open"},
			},
			"blocked": {
				{ID: "yoyodyne-ifd.1", Title: "held two hours", Status: "blocked"},
				{ID: "yoyodyne-ifd.3", Title: "held nine days", Status: "blocked"},
				{ID: "yoyodyne-ifd.4", Title: "held three days", Status: "blocked"},
			},
		},
	}}
	sources.Stoppages = fakeStoppages{
		runs: []runstate.State{
			// The stop is when the run completed, not when its record last moved.
			{RunID: "run-1", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusFailed, CompletedAt: stopped(2), UpdatedAt: moment.Add(-time.Minute),
				Branch: "yoyodyne/yoyodyne-ifd-1/run-1", Blocker: "Yoyodyne stopped this item."},
			{RunID: "run-4", WorkItemID: "yoyodyne-ifd.4", Status: runstate.StatusFailed, CompletedAt: stopped(72), UpdatedAt: moment.Add(-72 * time.Hour),
				Branch: "yoyodyne/yoyodyne-ifd-4/run-4", Blocker: "Yoyodyne stopped this item."},
		},
		// A stoppage nobody has decided about is held from when it was docketed.
		escalations: []runstate.Escalation{{WorkItemID: "yoyodyne-ifd.3", RunID: "run-3", DocketedAt: moment.Add(-9 * 24 * time.Hour)}},
	}
	sources.Decisions = recordedDecisions{}
	sources.Remains = &remainsOf{survives: map[string]gitworktree.Survival{
		"run-1": {BranchExists: true},
		"run-4": {BranchExists: true},
	}}

	standing := ReadStanding(context.Background(), sources)
	var order []string
	for _, refused := range standing.NotStartable {
		order = append(order, refused.WorkItemID)
	}
	// The held items take the places held items already had, oldest hold first;
	// the one refused for something else stays where the order put it, which is
	// first, since the tracker lists open work ahead of blocked.
	if want := []string{"yoyodyne-ifd.2", "yoyodyne-ifd.3", "yoyodyne-ifd.4", "yoyodyne-ifd.1"}; strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("not startable in order %v, want %v", order, want)
	}
	since := map[string]*time.Time{}
	for _, refused := range standing.NotStartable {
		since[refused.WorkItemID] = refused.HeldSince
		if (refused.Kind == backlog.HeldForAPerson) != (refused.HeldSince != nil) {
			t.Fatalf("%s is %s and carries held_since %v: only a held item says since when", refused.WorkItemID, refused.Kind, refused.HeldSince)
		}
	}
	for id, want := range map[string]time.Time{
		"yoyodyne-ifd.3": moment.Add(-9 * 24 * time.Hour),
		"yoyodyne-ifd.4": moment.Add(-72 * time.Hour),
		"yoyodyne-ifd.1": moment.Add(-2 * time.Hour),
	} {
		if since[id] == nil || !since[id].Equal(want) {
			t.Fatalf("%s held since %v, want %v", id, since[id], want)
		}
	}

	// The terminal says the moment in the machine's zone and how long ago it was
	// in words, ahead of the reason.
	rendered := standing.Render()
	var lines []string
	for _, id := range []string{"yoyodyne-ifd.3", "yoyodyne-ifd.4", "yoyodyne-ifd.1"} {
		for _, line := range strings.Split(rendered, "\n") {
			if strings.Contains(line, "("+id+") — held since ") {
				lines = append(lines, line)
			}
		}
	}
	for index, want := range []string{
		"  (P0) held nine days (yoyodyne-ifd.3) — held since " + localMoment(moment.Add(-9*24*time.Hour)) + ", 9 days ago; its stoppage",
		"  (P0) held three days (yoyodyne-ifd.4) — held since " + localMoment(moment.Add(-72*time.Hour)) + ", 3 days ago; run run-4 stopped on it",
		"  (P0) held two hours (yoyodyne-ifd.1) — held since " + localMoment(moment.Add(-2*time.Hour)) + ", 2 hours ago; run run-1 stopped on it",
	} {
		if index >= len(lines) || !strings.HasPrefix(lines[index], want) {
			t.Fatalf("rendered:\n%s\nwant a line opening %q", rendered, want)
		}
	}
	if strings.Index(rendered, "(yoyodyne-ifd.3) —") > strings.Index(rendered, "(yoyodyne-ifd.4) —") || strings.Index(rendered, "(yoyodyne-ifd.4) —") > strings.Index(rendered, "(yoyodyne-ifd.1) —") {
		t.Fatalf("rendered:\n%s\nwant the held items oldest hold first", rendered)
	}
	if strings.Contains(rendered, "(yoyodyne-ifd.2) — held since") {
		t.Fatalf("rendered:\n%s\nan item nothing holds says since when", rendered)
	}

	// --json carries the moment on each held entry.
	encoded, err := json.Marshal(standing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"work_item_id":"yoyodyne-ifd.3","title":"held nine days"`) ||
		!strings.Contains(string(encoded), `"held_since":"2026-08-21T12:00:00Z"`) {
		t.Fatalf("json does not carry held_since: %s", encoded)
	}
}

func TestHowLongAgoIsSaidInWords(t *testing.T) {
	t.Parallel()
	for elapsed, want := range map[time.Duration]string{
		30 * time.Second:           "less than a minute ago",
		time.Minute:                "1 minute ago",
		45 * time.Minute:           "45 minutes ago",
		time.Hour:                  "1 hour ago",
		47 * time.Hour:             "47 hours ago",
		48 * time.Hour:             "2 days ago",
		21*24*time.Hour + 5*3600e9: "21 days ago",
		-time.Minute:               "a moment stamped ahead of this reading",
	} {
		if said := agoSaid(elapsed); said != want {
			t.Errorf("agoSaid(%v) = %q, want %q", elapsed, said, want)
		}
	}
}
