package goalreview

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var pacific = time.FixedZone("PST", -8*60*60)

// The daily review is due from 09:00 on the operator's clock: before it, the
// previous day's 09:00 is the most recent start.
func TestDailyStartIsTheMostRecentNineOClockLocal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		now  time.Time
		want time.Time
		day  string
	}{
		{time.Date(2026, 11, 23, 8, 59, 0, 0, pacific), time.Date(2026, 11, 22, 9, 0, 0, 0, pacific), "2026-11-22"},
		{time.Date(2026, 11, 23, 9, 0, 0, 0, pacific), time.Date(2026, 11, 23, 9, 0, 0, 0, pacific), "2026-11-23"},
		{time.Date(2026, 11, 23, 23, 30, 0, 0, pacific), time.Date(2026, 11, 23, 9, 0, 0, 0, pacific), "2026-11-23"},
		// Read in UTC, the operator's 09:00 is still the boundary.
		{time.Date(2026, 11, 23, 17, 1, 0, 0, time.UTC), time.Date(2026, 11, 23, 9, 0, 0, 0, pacific), "2026-11-23"},
	} {
		got := DailyStart(tc.now, pacific)
		if !got.Equal(tc.want) || Day(got, pacific) != tc.day {
			t.Errorf("DailyStart(%s) = %s (%s), want %s (%s)", tc.now, got, Day(got, pacific), tc.want, tc.day)
		}
	}
}

// The fingerprint moves with the goal's wording, a serving item's status or
// priority, an item starting or stopping serving it, and the architect's
// entry, and not with an item's title.
func TestTheFingerprintCoversWhatTheReviewRead(t *testing.T) {
	t.Parallel()

	goal := Goal{Reference: "nearly-autonomous", Statement: "Run development nearly autonomously."}
	items := []Item{{ID: "a", Title: "first", Status: "open", Priority: 1, Serves: goal.Reference}}
	base := Fingerprint(goal, items, "")

	retitled := []Item{{ID: "a", Title: "renamed", Status: "open", Priority: 1, Serves: goal.Reference}}
	if Fingerprint(goal, retitled, "") != base {
		t.Error("retitling an item changed the fingerprint")
	}
	for name, changed := range map[string]string{
		"wording":   Fingerprint(Goal{Reference: goal.Reference, Statement: "Run development autonomously."}, items, ""),
		"status":    Fingerprint(goal, []Item{{ID: "a", Status: "in_progress", Priority: 1}}, ""),
		"priority":  Fingerprint(goal, []Item{{ID: "a", Status: "open", Priority: 0}}, ""),
		"new item":  Fingerprint(goal, append([]Item{{ID: "b", Status: "open"}}, items...), ""),
		"no items":  Fingerprint(goal, nil, ""),
		"architect": Fingerprint(goal, items, "the design for it is missing"),
	} {
		if changed == base {
			t.Errorf("a change of %s left the fingerprint unchanged", name)
		}
	}
}

// Open items count toward a goal, and closed ones only within RecentlyClosed.
func TestServingCountsOpenAndRecentlyClosedWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 23, 9, 0, 0, 0, pacific)
	goal := Goal{Reference: "g"}
	items := []Item{
		{ID: "open", Status: "open", Serves: "g"},
		{ID: "recent", Status: "closed", ClosedAt: now.Add(-6 * 24 * time.Hour), Serves: "g"},
		{ID: "old", Status: "closed", ClosedAt: now.Add(-8 * 24 * time.Hour), Serves: "g"},
		{ID: "undated", Status: "closed", Serves: "g"},
		{ID: "other", Status: "open", Serves: "h"},
		{ID: "none", Status: "open"},
	}
	var ids []string
	for _, item := range Serving(goal, items, now) {
		ids = append(ids, item.ID)
	}
	if got := strings.Join(ids, ","); got != "open,recent" {
		t.Errorf("Serving() = %s, want open,recent", got)
	}
}

// A goal is unchanged only against the fingerprint recorded for it, and then
// carries the time of the review that went through it.
func TestCompareMarksUnchangedGoalsWithTheirReview(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 23, 9, 0, 0, 0, pacific)
	reading := Reading{
		Goals: []Goal{{Reference: "a", Statement: "A"}, {Reference: "b", Statement: "B"}, {Reference: "c", Statement: "C"}},
		Items: []Item{{ID: "x", Status: "open", Priority: 1, Serves: "a"}},
	}
	reviewed := now.Add(-24 * time.Hour)
	first := Compare(reading, nil, now)
	prior := map[string]Prior{
		"a": {Fingerprint: first[0].Fingerprint, ReviewedAt: reviewed},
		"b": {Fingerprint: "something else", ReviewedAt: reviewed},
	}
	outcomes := Compare(reading, prior, now)
	if !outcomes[0].Unchanged || !outcomes[0].Since.Equal(reviewed) {
		t.Errorf("a = %+v, want unchanged since %s", outcomes[0], reviewed)
	}
	if outcomes[1].Unchanged || outcomes[2].Unchanged {
		t.Errorf("b, c = %+v, %+v, want both to be gone through", outcomes[1], outcomes[2])
	}
	if changed := Changed(outcomes); len(changed) != 2 {
		t.Errorf("Changed() = %d goals, want 2", len(changed))
	}

	report := Report(outcomes, Raised([]Item{
		{ID: "r", Title: "Raised", Status: "open", Priority: 1, Labels: []string{domain.RaisedByOperatorLabel}},
		{ID: "done", Status: "closed", Labels: []string{domain.RaisedByOperatorLabel}},
	}), pacific)
	for _, want := range []string{
		"Goals to go through (2)",
		"- [b] B\n  no open or recently closed work item serves it",
		"- [a] A: unchanged since the review at 09:00 PST on Nov 22, 2026",
		"Items the operator raised on purpose (1), left where he put them:\n- r (P1, open): Raised",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}

	// A goal named on work as "[identifier] words" is said that way, once.
	named := Outcome{Goal: Goal{Reference: "[g] Words.", Statement: "Words."}, Unchanged: true, Since: reviewed}
	if line := UnchangedLine(named, pacific); line != "- [g] Words.: unchanged since the review at 09:00 PST on Nov 22, 2026" {
		t.Errorf("UnchangedLine() = %q", line)
	}
}

// A report over far more work than it can list stays within its limit, names
// every goal, lists the most pressing unfinished items, and says what it left
// out; the fingerprint still covers the items it did not list.
func TestAReportOverALongBacklogStaysWithinItsLimit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 23, 9, 0, 0, 0, pacific)
	build := func(goals, items int) Reading {
		var reading Reading
		for g := range goals {
			reference := fmt.Sprintf("goal-%02d", g)
			reading.Goals = append(reading.Goals, Goal{Reference: reference, Statement: "[" + reference + "] " + strings.Repeat("wörds ", 150)})
			for i := range items {
				item := Item{ID: fmt.Sprintf("yoyodyne-%02d.%03d", g, i), Title: strings.Repeat("títle ", 60), Status: "open", Priority: 3, Serves: reference}
				if i%2 == 1 {
					item.Status, item.ClosedAt = "closed", now.Add(-time.Hour)
				}
				if i == items-2 {
					item.Priority = 0
				}
				if i%4 == 0 {
					item.Labels = []string{domain.RaisedByOperatorLabel}
				}
				reading.Items = append(reading.Items, item)
			}
		}
		return reading
	}

	// Few goals: the limit on each goal's list is what holds.
	reading := build(3, 200)
	report := Report(Compare(reading, nil, now), Raised(reading.Items), pacific)
	if len(report) > MaxReportBytes || !utf8.ValidString(report) {
		t.Fatalf("report is %d bytes (valid text %v), limit %d", len(report), utf8.ValidString(report), MaxReportBytes)
	}
	for _, want := range []string{
		"...\n  yoyodyne-00.198 (P0, open): títle",
		"  not listed here: 92 more unfinished and 100 recently closed",
		"Items the operator raised on purpose (150)",
		"- 130 more not listed here; each carries the raised-by-operator label",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}

	// Many goals: fewer items are listed, and every goal is still named.
	reading = build(60, 100)
	report = Report(Compare(reading, nil, now), Raised(reading.Items), pacific)
	if len(report) > MaxReportBytes {
		t.Fatalf("report is %d bytes, limit %d", len(report), MaxReportBytes)
	}
	for _, goal := range reading.Goals {
		if !strings.Contains(report, "- ["+goal.Reference+"]") {
			t.Errorf("report does not name %s", goal.Reference)
		}
	}
	if got := strings.Count(report, "  not listed here: 50 more unfinished and 50 recently closed"); got != 60 {
		t.Errorf("%d goals say what was left out, want 60:\n%s", got, report)
	}

	// An item the report does not list still changes its goal's fingerprint.
	before := Compare(reading, nil, now)[0].Fingerprint
	reading.Items[50].Priority = 4
	if Compare(reading, nil, now)[0].Fingerprint == before {
		t.Error("an unlisted item's priority changed and the fingerprint did not")
	}
}
