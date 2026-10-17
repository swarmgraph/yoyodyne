package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// handledPile is a report pile that says when each report was handled.
type handledPile map[string]time.Time

func (handledPile) List() ([]report.Report, error) { return nil, nil }

func (p handledPile) Handlings() ([]report.Handling, error) {
	var handlings []report.Handling
	for id, at := range p {
		handlings = append(handlings, report.Handling{ReportID: id, RecordedAt: at})
	}
	return handlings, nil
}

// A pass handed stopped runs is told, cause by cause, how many stops each has
// with this pass's counted, the report it already cites, and what it is owed;
// and the stops themselves are handed to its turn, which is what its block may
// count.
func TestAPassHandedStoppedRunsIsToldTheirCausesAndHandedTheStops(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerStoppages))
	stores, err := runstate.NewStopCauseStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	h.trigger.StopCauses = stores
	h.fire(t, recurringNow)

	limit := runstate.StopCauseKey{Cause: runstate.CauseCheckTimeLimit.StopClass(), Side: runstate.StopSideBound, Step: runstate.PhaseChecking}
	review := runstate.StopCauseKey{Cause: runstate.StopReview, Side: runstate.StopSideWork, Step: runstate.PhaseReviewing}
	handledAt := recurringNow.Add(-time.Hour)
	if _, err := stores.Record(context.Background(), instanceName, handledAt, func(account runstate.StopCauseAccount) ([]runstate.StopCauseEntry, error) {
		entries := account.StopEntries([]runstate.StopEvent{{Run: "run-old", StoppedAt: handledAt.Add(-time.Hour), Cause: limit}})
		return append(entries, runstate.StopCauseEntry{Kind: runstate.StopCauseReport, Cause: limit, Report: "report-first", Fault: runstate.StopFaultBound, Remedy: "raise it"}), nil
	}); err != nil {
		t.Fatal(err)
	}
	h.trigger.Pile = handledPile{"report-first": handledAt}

	stopped := []runstate.StopEvent{
		{Run: "run-a", StoppedAt: recurringNow.Add(time.Minute), Item: "yoyodyne-ifd.7", Cause: limit},
		{Run: "run-b", StoppedAt: recurringNow.Add(2 * time.Minute), Item: "yoyodyne-ifd.8", Cause: review},
	}
	for _, stop := range stopped {
		h.events.events = append(h.events.events, PassEvent{Stream: runstate.PassStreamRuns, Class: config.TriggerStoppages, At: stop.StoppedAt, Key: stop.Run + "/stoppages", Subject: stop.Item, Detail: "stopped", Stop: &stop})
	}
	h.role.answers = []scriptedTurn{{result: complete("post-mortems")}}
	if fired := h.fire(t, recurringNow.Add(6*time.Minute)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want one pass", fired.Fired)
	}
	message := h.role.messages[len(h.role.messages)-1]
	for _, want := range []string{
		"## Stopped runs, by cause",
		limit.ID() + " — 2 stop(s)",
		"report report-first, handled at",
		"1 stop(s) since",
		review.ID() + " — 1 stop(s)",
		"no report filed yet: file its one report",
		`"stops":[{"run":"run-a"`,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the pass's message does not say %q:\n%s", want, message)
		}
	}
	handed := h.role.stops[len(h.role.stops)-1]
	if len(handed) != 2 || handed[0].Run != "run-a" || handed[1].Cause != review {
		t.Errorf("handed stops = %+v, want both stops with their causes", handed)
	}
	// The message previews the count; only the instance's block records it.
	if account, _ := stores.Account(instanceName); account.Causes[0].Count() != 1 {
		t.Errorf("the record counts %d, want the pass's message to have recorded nothing", account.Causes[0].Count())
	}
}

// A pass handed no stopped runs carries no post-mortem section.
func TestAPassHandedNoStoppedRunsCarriesNoPostMortemSection(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(0, config.TriggerLandings))
	h.fire(t, recurringNow)
	h.events.land(recurringNow.Add(time.Minute), "yoyodyne-ifd.2")
	h.role.answers = []scriptedTurn{{result: complete("landed")}}
	h.fire(t, recurringNow.Add(6*time.Minute))
	if message := h.role.messages[len(h.role.messages)-1]; strings.Contains(message, "Stopped runs, by cause") {
		t.Errorf("a pass with no stoppages was given the section:\n%s", message)
	}
}
