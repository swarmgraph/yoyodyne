package claudecode

// The usage windows a rate_limit_event reports, read as readings rather than as
// answers.
//
// The dialect reads the same event for one thing: whether a limit is refusing
// work. Every event also says how much of each window is used, and that is what
// this reads. The shape is the one Claude Code 2.1.27x writes into every
// invocation's event log, recorded in this product's own logs: a top-level
// window with its status, and beside it every window the account has —
//
//	{"status":"allowed","rateLimitType":"five_hour","resetsAt":1791723600,
//	 "isUsingOverage":false,"overageStatus":"rejected",
//	 "unifiedWindows":{"five_hour":{"resetsAt":1791723600,"utilization":0.02},
//	                   "seven_day":{"resetsAt":1792292400,"utilization":0.03}}}
//
// An older event carries the top-level window alone, with a utilization or
// without one. A payload this cannot read yields no readings, and never a
// reading of zero.

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// usageWindowsInfo is the part of a rate_limit_event's payload the readings
// come from.
type usageWindowsInfo struct {
	Status         string                       `json:"status"`
	RateLimitType  string                       `json:"rateLimitType"`
	ResetsAt       json.RawMessage              `json:"resetsAt"`
	Utilization    *float64                     `json:"utilization"`
	IsUsingOverage bool                         `json:"isUsingOverage"`
	OverageStatus  string                       `json:"overageStatus"`
	UnifiedWindows map[string]usageWindowFigure `json:"unifiedWindows"`
}

type usageWindowFigure struct {
	ResetsAt    json.RawMessage `json:"resetsAt"`
	Utilization *float64        `json:"utilization"`
}

// usageWindowReadings reads every window one event reports, in window order.
// The invocation's own facts — provider, account, model, session, and when it
// arrived — are the caller's to fill in.
func usageWindowReadings(payload json.RawMessage) []backend.UsageWindowReading {
	var info usageWindowsInfo
	if len(payload) == 0 || json.Unmarshal(payload, &info) != nil {
		return nil
	}
	named := strings.TrimSpace(info.RateLimitType)
	readings := make([]backend.UsageWindowReading, 0, len(info.UnifiedWindows)+1)
	for window, figure := range info.UnifiedWindows {
		window = strings.TrimSpace(window)
		if window == "" {
			continue
		}
		reading := backend.UsageWindowReading{
			Window:      window,
			Utilization: figure.Utilization,
			ResetsAt:    resetsAt(figure.ResetsAt),
		}
		if window == named {
			reading.Status = info.Status
		}
		readings = append(readings, reading)
	}
	if _, listed := info.UnifiedWindows[named]; named != "" && !listed {
		readings = append(readings, backend.UsageWindowReading{
			Window:      named,
			Utilization: info.Utilization,
			ResetsAt:    resetsAt(info.ResetsAt),
			Status:      info.Status,
		})
	}
	for index := range readings {
		readings[index].UsingOverage = info.IsUsingOverage
		readings[index].OverageStatus = info.OverageStatus
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].Window < readings[j].Window })
	return readings
}

// resetsAt reads a reset time the way the dialect does: whole Unix seconds, and
// nothing at all for anything else.
func resetsAt(raw json.RawMessage) *time.Time {
	reset := rateLimitInfo{ResetsAt: raw}.resetTime()
	if reset.IsZero() {
		return nil
	}
	return &reset
}

// usageWindowSource is what the parser knows about the invocation a reading
// was heard on, set from the request before the stream is read.
type usageWindowSource struct {
	recorder backend.UsageWindowRecorder
	provider domain.Backend
	account  string
	model    string
}

// recordUsageWindows hands one event's readings to the recorder, stamped with
// the invocation that heard them. A recorder that fails costs the reading and
// not the invocation: the failure is said in the event log beside the event,
// and the stream carries on.
func (p *streamParser) recordUsageWindows(payload json.RawMessage) error {
	source := p.usageWindows
	if source.recorder == nil {
		return nil
	}
	readings := usageWindowReadings(payload)
	if len(readings) == 0 {
		return nil
	}
	model := p.result.ResolvedModel
	if model == "" {
		model = source.model
	}
	at := p.clock.Now().UTC()
	for index := range readings {
		readings[index].Provider = source.provider
		readings[index].Account = source.account
		readings[index].Model = model
		readings[index].Session = p.result.SessionID
		readings[index].At = at
	}
	if err := source.recorder.RecordUsageWindows(readings); err != nil {
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":       rateLimitEventType,
			"usage_window_record": "failed: " + err.Error(),
		})
	}
	return nil
}
