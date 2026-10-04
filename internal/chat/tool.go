package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	metadata := make(map[string]any, len(parameters))
	redactor := execution.NewRedactor(s.options.RedactValues...)
	for key, value := range parameters {
		if text, ok := value.(string); ok {
			value = redactor.Redact(text)
		}
		metadata[key] = value
	}
	audit := ToolAudit{ID: fmt.Sprintf("t%d.%d", s.state.Turns, s.state.LastSequence+1), Tool: id, Role: s.state.Role, Turn: s.state.Turns, Pass: s.pass, Parameters: metadata, Bounds: registered.Tool.Bounds, Requests: count}
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
func (s *Session) refuseToolBlocks(answer string, parsed parsedReply, alreadyPerformed ...capability.Capability) error {
	counts := map[capability.Capability]int{}
	seen := map[string]bool{"yoyodyne-report": true}
	add := func(id capability.Capability, count int) {
		if count > 0 {
			counts[id] += count
			if registered, found := toolcatalog.Registry().Lookup(string(id)); found {
				seen[registered.Tool.Block] = true
			}
		}
	}
	for _, action := range parsed.Actions {
		add(s.trackerTool(action.Action), 1)
	}
	for _, read := range parsed.Reads {
		id := capability.RepositoryRead
		if read.Action == "list" {
			id = capability.RepositoryList
		}
		add(id, 1)
	}
	add(capability.LogRead, len(parsed.LogReads))
	add(capability.ResearchCommission, len(parsed.Queries))
	add(capability.ProposalRaise, len(parsed.Proposals))
	add(capability.ConcernRaise, len(parsed.Concerns))
	add(capability.AgentContextMutate, len(parsed.Memories))
	if parsed.Ask != nil {
		add(capability.ExchangeAsk, 1)
	}
	if parsed.Evaluation != nil {
		add(capability.EvaluationRecord, 1)
	}
	if parsed.LaneReportCarried {
		add(capability.LaneReportWrite, 1)
	}
	if parsed.Restart != nil {
		add(capability.ServiceRequestRestart, 1)
	}
	writeTool := capability.ArtifactProductMutate
	for _, registered := range toolcatalog.Granted(s.state.Role) {
		if registered.Tool.ID == capability.ArtifactDesignMutate {
			writeTool = capability.ArtifactDesignMutate
		}
	}
	add(writeTool, len(parsed.Writes))
	for _, id := range alreadyPerformed {
		delete(counts, id)
		if registered, found := toolcatalog.Registry().Lookup(string(id)); found {
			seen[registered.Tool.Block] = true
		}
	}
	for _, registered := range toolcatalog.Registry().Actions() {
		block := registered.Tool.Block
		count := counts[registered.Tool.ID]
		if count == 0 {
			if seen[block] || !strings.Contains(answer, "```"+block) {
				continue
			}
			// An unreadable block cannot identify a validated operation. Record
			// its protocol once without guessing parameters out of malformed JSON.
			count = 1
		}
		seen[block] = true
		_, err := auditTool(context.Background(), s, registered.Tool.ID, map[string]any{"block": block}, count, func() (struct{}, error) { return struct{}{}, errors.New("block refused before invocation") }, nil)
		// The refusal itself is expected; failure to write either event is not.
		if err != nil && strings.Contains(err.Error(), "audit failed") {
			return err
		}
	}
	return nil
}

type LogReader interface {
	Read(context.Context, []logread.Request) ([]logread.Result, error)
}
type LogRound struct {
	Requests []logread.Request `json:"requests,omitempty"`
	Results  []logread.Result  `json:"results,omitempty"`
	Problem  string            `json:"problem,omitempty"`
}

// Render reports the records and applied bounds without repeating their content.
func (r LogRound) Render() string {
	var out strings.Builder
	for i, request := range r.Requests {
		name := request.Record
		if request.Name != "" {
			name += "/" + request.Name
		}
		fmt.Fprintf(&out, "log.read %s with ", name)
		if request.Cursor != nil {
			fmt.Fprintf(&out, "cursor %d", *request.Cursor)
		} else {
			fmt.Fprintf(&out, "window %s to %s", request.Since.Format(time.RFC3339), request.Until.Format(time.RFC3339))
		}
		fmt.Fprintf(&out, ", byte cap %d (scan cap %d): ", request.MaxBytes, logread.MaxScanBytes)
		switch {
		case r.Problem != "":
			fmt.Fprintf(&out, "nothing handed back: %s\n", r.Problem)
		case i < len(r.Results):
			result := r.Results[i]
			if result.Problem != "" {
				fmt.Fprintf(&out, "refused: %s\n", result.Problem)
				continue
			}
			fmt.Fprintf(&out, "%d byte(s), next cursor %d", result.Bytes, result.NextCursor)
			if result.Truncated {
				out.WriteString("; truncated")
			}
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (s *Session) performLogReads(ctx context.Context, requests []logread.Request, rounds *int) LogRound {
	var round LogRound
	remaining := logread.MaxBytesPerReply
	spent := *rounds >= logread.MaxRoundsPerMessage
	if !spent {
		*rounds++
	}
	for _, request := range requests {
		bounded := request
		bounded.MaxBytes = min(request.MaxBytes, remaining)
		round.Requests = append(round.Requests, bounded)
		// Only closed identifiers and numeric/time bounds are audit parameters.
		parameters := map[string]any{"record": request.Record, "name": request.Name, "max_bytes": bounded.MaxBytes, "scan_bytes": logread.MaxScanBytes, "since": request.Since, "until": request.Until, "cursor": request.Cursor}
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
				round.Results = nil
				round.Problem = err.Error()
				return round
			}
			round.Results = append(round.Results, logread.Result{Record: request.Record, Name: request.Name, Problem: err.Error()})
		} else {
			remaining -= len(result[0].Content)
			round.Results = append(round.Results, result...)
		}
	}
	if spent {
		round.Results = nil
		round.Problem = "log.read has spent its rounds for this message"
	}
	return round
}
