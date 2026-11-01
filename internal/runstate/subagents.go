package runstate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// SubagentRecord is what a developer run records about sub-agents
// (docs/designs/developer-sub-agents.md): the setting it started with, settled
// before its first launch and read back by every invocation after, so a
// configuration reload changes it only for runs that start later; and why the
// run's last invocation went without them where the setting is on and the
// endpoint serving it does not offer them.
//
// The calls to sub-agents themselves are in the run's event log, each on the event that
// started it and the one that ended it, which is where every other tool call
// of the invocation is; SubagentCallsOf reads them back.
type SubagentRecord struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	// Origin says which configuration decided the setting.
	Origin string `json:"origin,omitempty"`
	// Slot is the developer slot the setting was settled for, and zero on a
	// record from before it was kept. A run moved to another slot when it was
	// taken up again settles its setting afresh from that slot.
	Slot int `json:"slot,omitempty"`
	// Unavailable is why the run's last developer invocation went without
	// sub-agents although the setting is on, in words a person reads, and empty
	// where it had them or the setting is off.
	Unavailable string `json:"unavailable,omitempty"`
	// Limit is a usage limit the provider reported reached on the sub-agents'
	// model alone, and nil where none stands. While it stands the run goes on
	// without sub-agents on its own endpoint; it lifts at the reset the
	// provider named, and one that named none stands for the rest of the run.
	Limit *SubagentLimit `json:"limit,omitempty"`
}

// SubagentLimit is a usage limit on the sub-agents' model alone.
type SubagentLimit struct {
	// Kind is the provider's own name for the limit, such as seven_day_sonnet.
	// It is kept as the provider said it and shown to a person only through
	// backend.LimitName.
	Kind string `json:"kind"`
	// ResetsAt is when the provider said it resets, and nil where it said
	// nothing usable.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	At       time.Time  `json:"at"`
}

// Stands reports a limit that still stops sub-agents at now.
func (l *SubagentLimit) Stands(now time.Time) bool {
	return l != nil && (l.ResetsAt == nil || now.Before(*l.ResetsAt))
}

// Says is why a standing limit leaves the run without sub-agents, with the
// reset in the reader's own zone.
func (l SubagentLimit) Says() string {
	said := "the sub-agents' model has reached the provider's " + backend.LimitName(l.Kind)
	if l.ResetsAt == nil {
		return said + " and it named no reset time, so the developer works without them for the rest of this run"
	}
	return said + ", so the developer works without them until it resets at " + l.ResetsAt.Local().Format("Jan 2 15:04 MST")
}

// SubagentsSays is what a run says about sub-agents and the models that did
// its work, in one line a person reads, and empty for a run with sub-agents
// off and nothing recorded per model.
func (s State) SubagentsSays() string {
	var parts []string
	if record := s.Subagents; record != nil && record.Enabled {
		switch {
		case record.Limit != nil:
			// Said afresh from the limit rather than read off Unavailable, which
			// a record written before the limit was named in words still carries
			// with the provider's key in it.
			parts = append(parts, "sub-agents off for this run: "+record.Limit.Says())
		case record.Unavailable != "":
			parts = append(parts, "sub-agents off for this run: "+record.Unavailable)
		default:
			said := "sub-agents on " + record.Model
			if record.Effort != "" {
				said += " at " + record.Effort + " effort"
			}
			parts = append(parts, said)
		}
	}
	switch {
	case len(s.ProviderModelUsage) > 0:
		models := make([]string, 0, len(s.ProviderModelUsage))
		for _, usage := range s.ProviderModelUsage {
			models = append(models, fmt.Sprintf("%s $%.2f", usage.Model, usage.CostUSD))
		}
		parts = append(parts, "work by model in the last session, as the provider reported it: "+strings.Join(models, ", "))
	case s.Subagents != nil && s.Subagents.Enabled && s.ProviderSessionID != "":
		parts = append(parts, "which model did what is unknown: the provider reported a total only")
	}
	return strings.Join(parts, "; ")
}

// SubagentCallsOf is every call to a sub-agent a run's event log records,
// in the order they started. A call whose ending was never recorded is
// interrupted once the run is no longer in its invocation — the invocation
// ended, the run stopped, or the harness restarted — and nothing resumes it;
// live says the run's developer invocation may still be going, so an
// unfinished call is in progress rather than interrupted.
func SubagentCallsOf(events []execution.Event, live bool) []backend.SubagentCall {
	var calls []backend.SubagentCall
	open := map[string]int{}
	for _, event := range events {
		var payload struct {
			ToolUseID        string `json:"tool_use_id"`
			Subagent         string `json:"subagent"`
			SubagentModel    string `json:"subagent_model"`
			Description      string `json:"description"`
			SubagentFinished bool   `json:"subagent_finished"`
			IsError          bool   `json:"is_error"`
			ToolUses         *int   `json:"tool_uses"`
			TotalTokens      *int64 `json:"total_tokens"`
			ParentToolUseID  string `json:"parent_tool_use_id"`
		}
		switch event.Type {
		case execution.EventCommandStarted, execution.EventCommandCompleted:
		case execution.EventRunCompleted, execution.EventRunFailed:
			// The invocation is over, so whatever it left unfinished is too.
			for id, index := range open {
				calls[index].Interrupted = true
				delete(open, id)
			}
			continue
		default:
			continue
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		switch {
		case event.Type == execution.EventCommandStarted && payload.ParentToolUseID != "":
			if index, ok := open[payload.ParentToolUseID]; ok {
				calls[index].ToolUses++
			}
		case event.Type == execution.EventCommandStarted && payload.Subagent != "":
			open[payload.ToolUseID] = len(calls)
			calls = append(calls, backend.SubagentCall{
				ID: payload.ToolUseID, StartedAt: event.Timestamp, Agent: payload.Subagent,
				Model: payload.SubagentModel, Description: payload.Description,
			})
		case event.Type == execution.EventCommandCompleted && payload.SubagentFinished:
			index, ok := open[payload.ToolUseID]
			if !ok {
				continue
			}
			delete(open, payload.ToolUseID)
			calls[index].EndedAt, calls[index].Failed = event.Timestamp, payload.IsError
			if payload.ToolUses != nil && *payload.ToolUses > calls[index].ToolUses {
				calls[index].ToolUses = *payload.ToolUses
			}
			if payload.TotalTokens != nil {
				calls[index].TotalTokens, calls[index].UsageReported = *payload.TotalTokens, true
			}
		}
	}
	if !live {
		for _, index := range open {
			calls[index].Interrupted = true
		}
	}
	return calls
}
