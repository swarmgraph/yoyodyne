package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// backlogTracker is a tracker holding one item of every shape the backlog page
// draws: ready work with an origin, a closed item, a parked item, an item a
// conversation carries, an item a run is carrying, and an item admitted before
// origins were recorded. The tracker reports the parked and the conversation
// items as ready, as bd does, because it knows nothing of either.
func backlogTracker() statusTracker {
	filed := moment.Add(-72 * time.Hour)
	operator := domain.WorkItemOrigin{Asker: domain.AskerOperator, AdmittedBy: domain.RoleProductManager}
	ready := beads.WorkItem{ID: "yoyodyne-ifd.1", Title: "Ready work", Priority: 1, IssueType: "feature",
		Labels: []string{"product"}, CreatedAt: filed, Origin: operator}
	parked := beads.WorkItem{ID: "yoyodyne-ifd.2", Title: "Parked work", Priority: 2, CreatedAt: filed.Add(time.Hour),
		Parking: "deferred until the design lands", Origin: operator}
	conversation := beads.WorkItem{ID: "yoyodyne-ifd.3", Title: "A design to write", Priority: 2, CreatedAt: filed.Add(2 * time.Hour),
		Executor: domain.ConversationWith(domain.RoleArchitect), Parent: "yoyodyne-ifd",
		Origin: domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleProductManager,
			Report: "report-1", ReportedBy: domain.RoleDeveloper}}
	unknown := beads.WorkItem{ID: "yoyodyne-ifd.4", Title: "Admitted before origins", Priority: 3, CreatedAt: filed.Add(3 * time.Hour)}
	return statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"open": {ready, parked, conversation, unknown},
			"closed": {{ID: "yoyodyne-ifd.5", Title: "Done work", Priority: 0, CreatedAt: filed.Add(-time.Hour),
				ClosedAt: moment.Add(-time.Hour), Labels: []string{"reliability"}, Origin: operator}},
			"in_progress": {{ID: "yoyodyne-ifd.6", Title: "Being done", Priority: 1, CreatedAt: filed.Add(4 * time.Hour), Origin: operator}},
		},
		ready: []beads.WorkItem{ready, parked, conversation, unknown},
	}}
}

func backlogByID(t *testing.T, reading Backlog) map[string]BacklogItem {
	t.Helper()
	byID := make(map[string]BacklogItem, len(reading.Items))
	for _, item := range reading.Items {
		byID[item.ID] = item
	}
	return byID
}

// Every item the tracker holds is in the reading, open and closed, oldest filed
// first, each with its filed time, its closed time where it closed, its
// priority, labels, who does it, whether it is ready, and who filed it.
func TestTheBacklogCarriesEveryItemOpenAndClosed(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = backlogTracker()

	reading := ReadBacklog(context.Background(), sources)
	if reading.TrackerProblem != "" || reading.ReadinessProblem != "" {
		t.Fatalf("problems = %q / %q, want none", reading.TrackerProblem, reading.ReadinessProblem)
	}
	if !reading.ObservedAt.Equal(moment) {
		t.Fatalf("ObservedAt = %v, want %v", reading.ObservedAt, moment)
	}
	var order []string
	for _, item := range reading.Items {
		order = append(order, item.ID)
	}
	if got, want := strings.Join(order, " "), "yoyodyne-ifd.5 yoyodyne-ifd.1 yoyodyne-ifd.2 yoyodyne-ifd.3 yoyodyne-ifd.4 yoyodyne-ifd.6"; got != want {
		t.Fatalf("items = %s, want every item oldest filed first: %s", got, want)
	}
	items := backlogByID(t, reading)

	ready := items["yoyodyne-ifd.1"]
	if ready.State != BacklogReady || ready.WaitingOn != "" || ready.DoneBy != DoneByDeveloperRun ||
		ready.Priority != 1 || ready.Kind != "feature" || strings.Join(ready.Labels, ",") != "product" ||
		ready.FiledAt == nil || !ready.FiledAt.Equal(moment.Add(-72*time.Hour)) || ready.ClosedAt != nil {
		t.Fatalf("ready item = %#v", ready)
	}
	if !ready.Origin.Recorded || ready.Origin.FiledBy != "operator" || ready.Origin.AdmittedBy != domain.RoleProductManager {
		t.Fatalf("ready item's origin = %#v, want the operator as recorded", ready.Origin)
	}

	closed := items["yoyodyne-ifd.5"]
	if closed.State != BacklogClosed || closed.ClosedAt == nil || !closed.ClosedAt.Equal(moment.Add(-time.Hour)) || closed.WaitingOn != "" {
		t.Fatalf("closed item = %#v, want closed with its closed time", closed)
	}

	parked := items["yoyodyne-ifd.2"]
	if parked.State != BacklogWaiting || parked.WaitingKind != backlog.HeldParked ||
		!strings.Contains(parked.WaitingOn, "deferred until the design lands") || parked.Parked != "deferred until the design lands" {
		t.Fatalf("parked item = %#v, want waiting on its parking though the tracker calls it ready", parked)
	}

	conversation := items["yoyodyne-ifd.3"]
	if conversation.DoneBy != string(domain.RoleArchitect) || conversation.Executor != domain.ConversationWith(domain.RoleArchitect) ||
		conversation.State != BacklogWaiting || conversation.WaitingKind != backlog.HeldByConversation || conversation.Parent != "yoyodyne-ifd" {
		t.Fatalf("conversation item = %#v, want the architect's, waiting on its conversation", conversation)
	}
	if conversation.Origin.FiledBy != string(domain.RoleDeveloper) || !strings.Contains(conversation.Origin.Words, "report-1") {
		t.Fatalf("conversation item's origin = %#v, want the developer's report", conversation.Origin)
	}

	unknown := items["yoyodyne-ifd.4"]
	if unknown.Origin.Recorded || unknown.Origin.FiledBy != OriginNotRecorded || unknown.Origin.AdmittedBy != "" ||
		!strings.Contains(unknown.Origin.Words, "not recorded") {
		t.Fatalf("unrecorded origin = %#v, want it said as not recorded and nothing guessed", unknown.Origin)
	}
	if unknown.State != BacklogReady {
		t.Fatalf("unrecorded-origin item state = %q, want ready", unknown.State)
	}

	if claimed := items["yoyodyne-ifd.6"]; claimed.State != BacklogInProgress || claimed.WaitingOn != "" {
		t.Fatalf("claimed item = %#v, want in progress", claimed)
	}
}

// The fields the page groups by are on the wire under their own names, and the
// times and labels an item lacks are null and empty rather than missing.
func TestTheBacklogsFieldsAreOnTheWire(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": {{ID: "yoyodyne-ifd.7", Title: "Bare"}}}}}

	encoded, err := json.Marshal(ReadBacklog(context.Background(), sources))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"filed_at":null`, `"closed_at":null`, `"labels":[]`, `"done_by":"developer run"`,
		`"filed_by":"not recorded"`, `"recorded":false`, `"state":"waiting"`, `"priority":0`, `"kind":""`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("the reading lacks %s: %s", want, encoded)
		}
	}
}

// Ready work an unresolved directive pauses, or whose children already carry
// it, is waiting, in the words the standing gives each.
func TestTheBacklogSaysWhatTheStandingSaysOfPausedAndCoveredWork(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	parent := beads.WorkItem{ID: "yoyodyne-ifd.8", Title: "Broken out", CreatedAt: moment.Add(-time.Hour)}
	child := beads.WorkItem{ID: "yoyodyne-ifd.8.1", Title: "A part", Parent: "yoyodyne-ifd.8", CreatedAt: moment.Add(-time.Minute)}
	paused := beads.WorkItem{ID: "yoyodyne-ifd.9", Title: "Paused", CreatedAt: moment.Add(-time.Hour)}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {parent, paused}, "in_progress": {child}},
		ready:    []beads.WorkItem{parent, paused},
	}}
	sources.Directives = fakeDirectives{recorded: []directive.Directive{{
		ID: "dir-1", Kind: directive.KindAmbiguous, Text: "which branch does this land on?",
		Unresolved: "which branch does this land on?", Scope: []string{"yoyodyne-ifd.9"},
	}}}
	items := backlogByID(t, ReadBacklog(context.Background(), sources))
	if covered := items["yoyodyne-ifd.8"]; covered.State != BacklogWaiting || covered.WaitingKind != backlog.HeldCovered ||
		covered.WaitingOn != backlog.CoveredReason([]string{"yoyodyne-ifd.8.1"}) {
		t.Fatalf("covered item = %#v, want waiting on its child", covered)
	}
	if held := items["yoyodyne-ifd.9"]; held.State != BacklogWaiting || held.WaitingKind != backlog.HeldByDirective ||
		held.WaitingOn != "paused for unresolved directive dir-1: which branch does this land on?" {
		t.Fatalf("paused item = %#v, want waiting on the directive", held)
	}
}

// A tracker that cannot be listed costs the items and says why; it is never
// an empty backlog. A ready listing that fails costs each admitted item its
// state, which is said as not read rather than guessed, while closed and
// claimed work still read as what they are.
func TestAnUnreadableTrackerIsSaidAndNeverAnEmptyBacklog(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{fail: errors.New("openat LOCK: operation not permitted")}}
	reading := ReadBacklog(context.Background(), sources)
	if reading.Items != nil || !strings.Contains(reading.TrackerProblem, "operation not permitted") {
		t.Fatalf("reading = %#v, want no items and the tracker's failure", reading)
	}

	sources = quietSources()
	sources.Tracker = readyFails{backlogTracker()}
	reading = ReadBacklog(context.Background(), sources)
	if reading.TrackerProblem != "" || !strings.Contains(reading.ReadinessProblem, "ready listing timed out") {
		t.Fatalf("problems = %q / %q, want the readiness failure alone", reading.TrackerProblem, reading.ReadinessProblem)
	}
	items := backlogByID(t, reading)
	for _, id := range []string{"yoyodyne-ifd.1", "yoyodyne-ifd.2", "yoyodyne-ifd.3", "yoyodyne-ifd.4"} {
		if items[id].State != BacklogNotRead {
			t.Fatalf("%s state = %q, want not read", id, items[id].State)
		}
	}
	if items["yoyodyne-ifd.5"].State != BacklogClosed || items["yoyodyne-ifd.6"].State != BacklogInProgress {
		t.Fatalf("closed and claimed = %q, %q", items["yoyodyne-ifd.5"].State, items["yoyodyne-ifd.6"].State)
	}
}

// readyFails is a tracker whose listing answers and whose ready query does not.
type readyFails struct{ statusTracker }

func (readyFails) Ready(context.Context) ([]beads.WorkItem, error) {
	return nil, errors.New("ready listing timed out")
}
