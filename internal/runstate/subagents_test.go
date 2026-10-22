package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func subagentEvent(t *testing.T, sequence uint64, eventType execution.EventType, payload map[string]any) execution.Event {
	t.Helper()
	event, err := execution.NewEvent("run-1", sequence, time.Date(2026, 10, 22, 9, 0, int(sequence), 0, time.UTC), eventType, "claude-code", payload)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

// The calls to sub-agents are read back off the event log: one finished with its usage,
// one the invocation ended before it finished, and one cut off by the harness
// stopping or restarting with no ending recorded at all. Neither unfinished
// call is resumed by anything, and the record says both were interrupted; a
// call in a developer invocation that may still be going is not.
func TestSubagentCallsAreReadBackAndUnfinishedOnesAreInterrupted(t *testing.T) {
	t.Parallel()

	events := []execution.Event{
		subagentEvent(t, 1, execution.EventCommandStarted, map[string]any{"tool_use_id": "a", "tool": "Agent", "subagent": "coder", "subagent_model": "sonnet", "description": "write the parser"}),
		subagentEvent(t, 2, execution.EventCommandStarted, map[string]any{"tool_use_id": "a1", "tool": "Edit", "parent_tool_use_id": "a"}),
		subagentEvent(t, 3, execution.EventCommandCompleted, map[string]any{"tool_use_id": "a", "subagent_finished": true, "tool_uses": 3, "total_tokens": 1200}),
		subagentEvent(t, 4, execution.EventCommandStarted, map[string]any{"tool_use_id": "b", "tool": "Agent", "subagent": "coder", "subagent_model": "sonnet", "description": "write the tests"}),
		subagentEvent(t, 5, execution.EventRunCompleted, map[string]any{"role": "developer"}),
		subagentEvent(t, 6, execution.EventCommandStarted, map[string]any{"tool_use_id": "c", "tool": "Agent", "subagent": "coder", "subagent_model": "sonnet", "description": "fix the build"}),
	}

	calls := SubagentCallsOf(events, false)
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	finished := calls[0]
	if finished.Interrupted || finished.EndedAt.IsZero() || finished.ToolUses != 3 || !finished.UsageReported || finished.TotalTokens != 1200 || finished.Model != "sonnet" || finished.Description != "write the parser" {
		t.Fatalf("finished call = %+v", finished)
	}
	for _, call := range calls[1:] {
		if !call.Interrupted || !call.EndedAt.IsZero() {
			t.Fatalf("unfinished call %q = %+v, want interrupted", call.Description, call)
		}
	}

	if live := SubagentCallsOf(events, true); live[2].Interrupted || !live[1].Interrupted {
		t.Fatalf("with the invocation still going: %+v", live)
	}
}

// What a run says about sub-agents: nothing when they are off and nothing was
// reported per model, why it went without them, and an unknown split said as
// unknown rather than estimated.
func TestARunSaysWhatItsSubagentsAndModelsDid(t *testing.T) {
	t.Parallel()

	if says := (State{}).SubagentsSays(); says != "" {
		t.Fatalf("a run with sub-agents off says %q", says)
	}
	without := State{Subagents: &SubagentRecord{Enabled: true, Model: "sonnet", Unavailable: "Codex does not offer them"}}
	if says := without.SubagentsSays(); says != "sub-agents off for this run: Codex does not offer them" {
		t.Fatalf("says %q", says)
	}
	unknown := State{Subagents: &SubagentRecord{Enabled: true, Model: "sonnet"}, ProviderSessionID: "session-1"}
	if says := unknown.SubagentsSays(); !strings.Contains(says, "which model did what is unknown") {
		t.Fatalf("says %q", says)
	}
	resets := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	limited := State{Subagents: &SubagentRecord{Enabled: true, Model: "sonnet",
		Unavailable: "the provider's seven_day_sonnet usage limit for the sub-agents' model is reached",
		Limit:       &SubagentLimit{Kind: "seven_day_sonnet", ResetsAt: &resets}}}
	want := "sub-agents off for this run: the sub-agents' model has reached the provider's Sonnet weekly limit, so the developer works without them until it resets at " + resets.Local().Format("Jan 2 15:04 MST")
	if says := limited.SubagentsSays(); says != want {
		t.Fatalf("a run under a known limit says %q, want %q", says, want)
	}
	unrecognized := State{Subagents: &SubagentRecord{Enabled: true, Model: "sonnet", Limit: &SubagentLimit{Kind: "monthly_sonnet"}}}
	want = `sub-agents off for this run: the sub-agents' model has reached the provider's unrecognized usage limit (named "monthly_sonnet") and it named no reset time, so the developer works without them for the rest of this run`
	if says := unrecognized.SubagentsSays(); says != want {
		t.Fatalf("a run under an unrecognized limit says %q, want %q", says, want)
	}
	reported := State{Subagents: &SubagentRecord{Enabled: true, Model: "sonnet"}, ProviderModelUsage: []backend.ModelUsage{{Model: "claude-sonnet-5-5", CostUSD: 0.25}}}
	if says := reported.SubagentsSays(); !strings.Contains(says, "sub-agents on sonnet") || !strings.Contains(says, "claude-sonnet-5-5 $0.25") {
		t.Fatalf("says %q", says)
	}
}
