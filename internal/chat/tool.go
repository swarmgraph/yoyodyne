package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/logread"
	"github.com/mason-bryant/yoyodyne/internal/toolcatalog"
)

// ToolAudit carries only request metadata and measured bounds. Neither request
// prose nor returned content belongs in these events. The pass identifier links
// a scheduled invocation to its conversation record without duplicating it.
type ToolAudit = execution.ToolAudit

type toolUse struct {
	turn, rounds, requests, bytes int
}

// auditTool invokes the registered action around the existing trusted handler.
// Failed request auditing prevents the act; failed outcome auditing prevents
// evidence delivery. The handler's own refusals and scope checks stay intact.
func auditTool[T any](ctx context.Context, s *Session, id capability.Capability, parameters map[string]any, count int, run func() (T, error), measure func(T) (int, bool, error)) (T, error) {
	var zero T
	registered, found := toolcatalog.Registry().Lookup(string(id))
	if !found {
		return zero, fmt.Errorf("unregistered tool %s", id)
	}
	audit := ToolAudit{ID: fmt.Sprintf("t%d.%d", s.state.Turns, s.state.LastSequence+1), Tool: id, Role: s.state.Role, Turn: s.state.Turns, Pass: s.pass, Parameters: parameters, Bounds: registered.Tool.Bounds, Requests: count}
	if id == capability.ExchangeAsk {
		audit.Bounds.RoundsPerMessage = s.options.askRounds()
	}
	allowed := false
	for _, held := range toolcatalog.Granted(s.state.Role) {
		if held.Name == registered.Name {
			allowed = true
		}
	}
	var result T
	var performed error
	if s.toolUses == nil {
		s.toolUses = make(map[capability.Capability]*toolUse)
	}
	use := s.toolUses[id]
	if use == nil {
		use = &toolUse{turn: -1}
		s.toolUses[id] = use
	}
	if use.turn != s.state.Turns {
		use.turn, use.requests, use.bytes = s.state.Turns, 0, 0
		use.rounds++
	}
	use.requests += count
	audit.Round = use.rounds
	if err := s.emit(execution.EventToolRequested, audit); err != nil {
		return zero, fmt.Errorf("tool request audit failed; nothing was done: %w", err)
	}
	// These handlers already enforce their round bound and explain its ending.
	// Keep their refusal wording and bookkeeping during this migration slice.
	handlerBoundsRounds := id == capability.RepositoryRead || id == capability.RepositoryList || id == capability.ResearchCommission || id == capability.LogRead
	if !allowed {
		performed = errors.New("the role holds no grant for this tool")
	} else if !handlerBoundsRounds && use.rounds > audit.Bounds.RoundsPerMessage || use.requests > audit.Bounds.RequestsPerReply {
		performed = errors.New("tool request exceeds its descriptor's request or round bound")
	} else {
		performed = registered.Perform(ctx, toolcatalog.Call{Perform: func(context.Context) error { var err error; result, err = run(); return err }})
	}
	if measure != nil && performed == nil {
		audit.Bytes, audit.Truncated, performed = measure(result)
		use.bytes += audit.Bytes
		if performed == nil && (audit.Bytes > count*audit.Bounds.BytesPerRequest || use.bytes > audit.Bounds.BytesPerReply) {
			result = zero
			performed = errors.New("tool evidence exceeds its descriptor's byte bound")
		}
	}
	event := execution.EventToolPerformed
	if performed != nil {
		event = execution.EventToolRefused
	}
	if err := s.emit(event, audit); err != nil {
		return zero, fmt.Errorf("tool outcome audit failed; nothing was handed back: %w", err)
	}
	return result, performed
}

// refuseToolBlocks audits unreadable or unauthorized requests without persisting
// any of their untrusted JSON (which may contain credentials or record text).
func (s *Session) refuseToolBlocks(answer string) error {
	seen := map[string]bool{}
	for _, registered := range toolcatalog.Registry().Actions() {
		block := registered.Tool.Block
		if block == "yoyodyne-report" || seen[block] || !strings.Contains(answer, "```"+block) {
			continue
		}
		seen[block] = true
		_, err := auditTool(context.Background(), s, registered.Tool.ID, map[string]any{"block": block}, 1, func() (struct{}, error) { return struct{}{}, errors.New("block refused before invocation") }, nil)
		// The refusal itself is expected; failure to write either event is not.
		if err != nil && err.Error() != "block refused before invocation" && err.Error() != "the role holds no grant for this tool" {
			return err
		}
	}
	return nil
}

type LogReader interface {
	Read(context.Context, []logread.Request) ([]logread.Result, error)
}
type LogRound struct {
	Results []logread.Result `json:"results,omitempty"`
	Problem string           `json:"problem,omitempty"`
}

func (s *Session) performLogReads(ctx context.Context, requests []logread.Request, rounds *int) ([]logread.Result, string) {
	var all []logread.Result
	remaining := logread.MaxBytesPerReply
	spent := *rounds >= logread.MaxRoundsPerMessage
	if !spent {
		*rounds++
	}
	for _, request := range requests {
		// Only closed identifiers and numeric/time bounds are audit parameters.
		parameters := map[string]any{"record": request.Record, "name": request.Name, "max_bytes": min(request.MaxBytes, remaining), "scan_bytes": logread.MaxScanBytes, "since": request.Since, "until": request.Until, "cursor": request.Cursor}
		result, err := auditTool(ctx, s, capability.LogRead, parameters, 1, func() ([]logread.Result, error) {
			if spent {
				return nil, errors.New("log.read has spent its rounds for this message")
			}
			if s.options.LogReader == nil {
				return nil, errors.New("no log reader is wired to this conversation")
			}
			if remaining == 0 {
				return []logread.Result{{Record: request.Record, Name: request.Name, Truncated: true}}, nil
			}
			bounded := request
			bounded.MaxBytes = min(request.MaxBytes, remaining)
			return s.options.LogReader.Read(ctx, []logread.Request{bounded})
		}, func(results []logread.Result) (int, bool, error) {
			if len(results) != 1 {
				return 0, false, errors.New("log reader must return exactly one named result")
			}
			result := results[0]
			if result.Record != request.Record || result.Name != request.Name || len(result.Content) > min(request.MaxBytes, remaining) {
				return 0, false, errors.New("log reader exceeded the requested bounds")
			}
			if result.Problem != "" {
				return 0, false, errors.New(result.Problem)
			}
			return len(result.Content), result.Truncated, nil
		})
		if err != nil {
			// An audit failure must never expose even an earlier request's evidence.
			if strings.Contains(err.Error(), "audit failed") {
				return nil, err.Error()
			}
			all = append(all, logread.Result{Record: request.Record, Name: request.Name, Problem: err.Error()})
		} else {
			remaining -= len(result[0].Content)
			all = append(all, result...)
		}
	}
	if spent {
		return nil, "log.read has spent its rounds for this message"
	}
	return all, ""
}
