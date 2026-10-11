package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// recordedRateLimitEvent is a rate_limit_event line as Claude Code wrote it on
// 2026-10-11, byte for byte apart from the session.
const recordedRateLimitEvent = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1791723600,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.02,"resetsAt":1791723600},"seven_day":{"utilization":0.03,"resetsAt":1792292400}}},"uuid":"0e2b5f2a-7c1d-4f0e-9a51-3c2d1e0f9b8a","session_id":"session-1"}`

type usageWindowSink struct {
	recorded [][]backendapi.UsageWindowReading
	fail     error
}

func (s *usageWindowSink) RecordUsageWindows(readings []backendapi.UsageWindowReading) error {
	s.recorded = append(s.recorded, readings)
	return s.fail
}

func runWithUsageWindows(t *testing.T, sink *usageWindowSink, lines ...string) []execution.Event {
	t.Helper()
	var events []execution.Event
	stream := strings.Join(append(append([]string{`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-fable-5-1"}`}, lines...),
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":0.01}`), "\n") + "\n"
	if _, err := (Backend{
		Runner:       &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}},
		Clock:        fixedClock{},
		UsageWindows: sink,
	}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		Model:            "fable",
		AccountAlias:     "second",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return events
}

// Every window a recorded event reports is a reading, with its share used, its
// reset, and the status on the window the event was about — stamped with the
// account, the model, and the session that heard it, and when.
func TestARecordedRateLimitEventIsReadAsOneReadingPerWindow(t *testing.T) {
	t.Parallel()
	sink := &usageWindowSink{}
	runWithUsageWindows(t, sink, recordedRateLimitEvent)
	if len(sink.recorded) != 1 || len(sink.recorded[0]) != 2 {
		t.Fatalf("recorded = %+v, want one event's two windows", sink.recorded)
	}
	fiveHour, sevenDay := sink.recorded[0][0], sink.recorded[0][1]
	if fiveHour.Window != "five_hour" || sevenDay.Window != "seven_day" {
		t.Fatalf("windows = %q, %q", fiveHour.Window, sevenDay.Window)
	}
	if percent, reported := sevenDay.Percent(); !reported || percent != 3 {
		t.Fatalf("seven-day percent = %d, %v", percent, reported)
	}
	if sevenDay.ResetsAt == nil || !sevenDay.ResetsAt.Equal(time.Unix(1792292400, 0)) {
		t.Fatalf("seven-day reset = %v", sevenDay.ResetsAt)
	}
	if fiveHour.Status != "allowed" || sevenDay.Status != "" {
		t.Fatalf("statuses = %q, %q; only the window the event was about carries one", fiveHour.Status, sevenDay.Status)
	}
	for _, reading := range sink.recorded[0] {
		if reading.Account != "second" || reading.Model != "claude-fable-5-1" || reading.Session != "session-1" ||
			reading.Provider != domain.BackendClaudeCode || reading.OverageStatus != "rejected" || !reading.At.Equal(fixedClock{}.Now()) {
			t.Fatalf("reading = %+v, want the invocation's account, resolved model, session, provider, overage, and moment", reading)
		}
	}
}

// An older event carries the window it is about and nothing else; a model's
// own week is named as that model's; and a payload that cannot be read is no
// reading at all, never one of zero.
func TestUsageWindowReadingsReadEveryShapeAndInventNone(t *testing.T) {
	t.Parallel()
	older := usageWindowReadings(json.RawMessage(`{"status":"allowed_warning","rateLimitType":"seven_day","resetsAt":1791687600,"utilization":0.81,"isUsingOverage":false}`))
	if len(older) != 1 || older[0].Window != "seven_day" || older[0].Status != "allowed_warning" {
		t.Fatalf("older = %+v", older)
	}
	if percent, _ := older[0].Percent(); percent != 81 {
		t.Fatalf("older percent = %d", percent)
	}
	bare := usageWindowReadings(json.RawMessage(`{"status":"allowed","rateLimitType":"five_hour","resetsAt":1791723600}`))
	if len(bare) != 1 || bare[0].Utilization != nil {
		t.Fatalf("bare = %+v, want the window with no figure rather than zero", bare)
	}
	models := usageWindowReadings(json.RawMessage(`{"status":"allowed","rateLimitType":"seven_day","unifiedWindows":{"seven_day":{"utilization":0.4,"resetsAt":1792292400},"seven_day_fable":{"utilization":0.62,"resetsAt":1792292400},"seven_day_overage_included":{"utilization":0.5,"resetsAt":1792292400}}}`))
	families := map[string]string{}
	for _, reading := range models {
		families[reading.Window] = reading.SevenDayModel()
	}
	if families["seven_day_fable"] != "fable" || families["seven_day"] != "" || families["seven_day_overage_included"] != "" {
		t.Fatalf("families = %v", families)
	}
	for _, unreadable := range []string{``, `not json`, `{"status":"allowed"}`} {
		if readings := usageWindowReadings(json.RawMessage(unreadable)); len(readings) != 0 {
			t.Fatalf("%q read as %+v", unreadable, readings)
		}
	}
}

// A recorder that fails costs the reading, not the invocation: the failure is
// said in the event log and the stream carries on to its result.
func TestARecorderThatFailsIsSaidAndDoesNotFailTheInvocation(t *testing.T) {
	t.Parallel()
	sink := &usageWindowSink{fail: errors.New("disk full")}
	events := runWithUsageWindows(t, sink, recordedRateLimitEvent)
	said := false
	for _, event := range events {
		said = said || strings.Contains(string(event.Payload), "usage_window_record") && strings.Contains(string(event.Payload), "disk full")
	}
	if !said {
		t.Fatalf("no event says the reading was not recorded: %+v", events)
	}
}
