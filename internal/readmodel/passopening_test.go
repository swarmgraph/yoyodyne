package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type fakeDocket struct {
	entries []triage.Entry
	fail    error
}

func (f fakeDocket) List() ([]triage.Entry, error) { return f.entries, f.fail }

func docketed(run, item string, class triage.Class, at time.Time) triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion, Key: triage.Key(class, run), Class: class,
		ProductID: "yoyodyne", RunID: run, WorkItemID: item, RecordedAt: at,
	}
}

// openingSources is two instances, a pile with three unhandled reports — two of
// them the factory instance's own — a docket with two live stoppages and one on
// closed work, and a run that landed today.
func openingSources() (Sources, ThroughputSources) {
	sources := programManagerSources()
	sources.Reports = fakeReports{reports: []report.Report{
		filedBy("report-00000000000000000000000000000001", "factory-pgm"),
		filedBy("report-00000000000000000000000000000002", "factory-pgm"),
		filedReport("report-00000000000000000000000000000003", report.SeverityCritical, moment.Add(-2*time.Hour)),
	}}
	sources.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "factory-pgm")}}
	sources.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{askedBy("exchange-1", "writing-pgm", "")}}
	sources.Docket = fakeDocket{entries: []triage.Entry{
		docketed("run-1", "yoyodyne-ifd.1", triage.ClassStoppedRun, moment.Add(-3*time.Hour)),
		docketed("run-2", "yoyodyne-ifd.2", triage.ClassEscalation, moment.Add(-time.Hour)),
		docketed("run-3", "yoyodyne-ifd.3", triage.ClassStoppedRun, moment.Add(-time.Hour)),
	}}
	sources.Tracker = statusTracker{fakeTracker: fakeTracker{byStatus: map[string][]beads.WorkItem{
		"closed": {{ID: "yoyodyne-ifd.3", Status: "closed"}},
	}}}
	landed := runstate.State{RunID: "run-9", WorkItemID: "yoyodyne-ifd.9", Status: runstate.StatusSucceeded}
	landed.StartedAt = moment.Add(-2 * time.Hour)
	completed := moment.Add(-time.Hour)
	landed.CompletedAt = &completed
	landed.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: "abc", TargetCommit: "def", PreviousTargetCommit: "000"}
	return sources, ThroughputSources{Runs: fakeRuns{recorded: []runstate.State{landed}}, Now: func() time.Time { return moment }}
}

// An instance's open requests are every record of its own still open: its
// unanswered restart request, and its reports nobody has handled.
func TestAnInstancesOpenRequestsAreEveryRecordOfItsOwnStillOpen(t *testing.T) {
	t.Parallel()

	sources, _ := openingSources()
	factory := instanceNamed(t, sources, "factory-pgm")
	want := []string{"restart-0123456789abcdef", "report-00000000000000000000000000000001", "report-00000000000000000000000000000002"}
	if strings.Join(factory.OpenRequests, ",") != strings.Join(want, ",") {
		t.Fatalf("OpenRequests = %v, want %v", factory.OpenRequests, want)
	}
	writing := instanceNamed(t, sources, "writing-pgm")
	if strings.Join(writing.OpenRequests, ",") != "exchange-1" {
		t.Fatalf("OpenRequests = %v, want the open exchange", writing.OpenRequests)
	}
}

// The opening carries the six parts as counts, and names every other instance
// by its lane, its status, and its open requests — never the instance woken.
func TestAPassOpeningCarriesTheSixPartsAndTheOtherInstances(t *testing.T) {
	t.Parallel()

	sources, throughput := openingSources()
	opening := ReadPassOpening(context.Background(), sources, throughput)
	rendered := opening.Render("writing-pgm")
	for _, want := range []string{
		"## Standing", "Running: nothing",
		"## Throughput", "today (since", "1 landed",
		"## Capacity", "Developer slots: 0 of 2 in use", "Parked or held on provider capacity: no runs and no conversation turns",
		"## Docket", "2 stopped runs waiting on the development manager, 1 of them critical, the oldest docketed 3 hours ago; 1 further entry on work that has closed.",
		"## Reports", "3 of 3 collected report(s) are unhandled",
		"## The other program managers",
		"- factory-pgm — lane reliability — working — open requests: restart-0123456789abcdef, report-00000000000000000000000000000001, report-00000000000000000000000000000002",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the opening is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "- writing-pgm") {
		t.Errorf("the instance woken is listed among the others:\n%s", rendered)
	}
}

// A docket that cannot be read is said as that rather than counted as empty.
func TestAnUnreadableDocketIsSaidInTheOpening(t *testing.T) {
	t.Parallel()

	sources, _ := openingSources()
	sources.Docket = fakeDocket{fail: errors.New("docket.jsonl: permission denied")}
	said := ReadDocketCounts(context.Background(), sources).Describe()
	if !strings.Contains(said, "could not be read") || !strings.Contains(said, "permission denied") {
		t.Fatalf("Describe() = %q", said)
	}
}

// A named query is answered whole, as the read model's own record, and a name
// that is not a query is refused.
func TestANamedQueryIsAnsweredWholeAndAnUnknownOneIsRefused(t *testing.T) {
	t.Parallel()

	sources, throughput := openingSources()
	answer, err := Query(context.Background(), sources, throughput, QueryDocket, "")
	if err != nil {
		t.Fatalf("Query(docket) error = %v", err)
	}
	var counts DocketCounts
	if err := json.Unmarshal([]byte(answer.JSON), &counts); err != nil || answer.Cut {
		t.Fatalf("the docket query is not the record whole: %v, cut %v", err, answer.Cut)
	}
	if counts.Live != 2 || len(counts.Stoppages) != 2 || counts.Dead != 1 {
		t.Fatalf("docket query = %+v", counts)
	}
	lane, err := Query(context.Background(), sources, throughput, QueryLaneReport, "factory-pgm")
	if err != nil || !strings.Contains(lane.JSON, `"agent":"factory-pgm"`) {
		t.Fatalf("Query(lane-report) = %q, %v", lane.JSON, err)
	}
	if _, err := Query(context.Background(), sources, throughput, "weather", ""); !errors.Is(err, ErrUnknownQuery) {
		t.Fatalf("Query(weather) error = %v, want ErrUnknownQuery", err)
	}
}
