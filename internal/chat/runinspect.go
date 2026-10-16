package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
)

// RunInspector answers run.inspect and run.verify for this conversation's
// product. runinspect.Inspector is one.
type RunInspector interface {
	Inspect(ctx context.Context, runID string) runinspect.Inspection
	Verify(ctx context.Context, runID string, expect runinspect.Expected) runinspect.Verification
}

// RunRound is one round of run reads a reply set off, and what each request
// handed back.
type RunRound struct {
	Requests []runinspect.Request `json:"requests,omitempty"`
	Results  []runinspect.Result  `json:"results,omitempty"`
	Problem  string               `json:"problem,omitempty"`
}

// Render names each request and its answer without repeating the evidence.
func (r RunRound) Render() string {
	var out strings.Builder
	for i, request := range r.Requests {
		fmt.Fprintf(&out, "%s %s", request.Tool(), request.Run)
		if request.Expect != "" {
			fmt.Fprintf(&out, " expecting %s", request.Expect)
		}
		out.WriteString(": ")
		switch {
		case r.Problem != "":
			fmt.Fprintf(&out, "nothing handed back: %s\n", r.Problem)
		case i < len(r.Results) && r.Results[i].Problem != "":
			fmt.Fprintf(&out, "refused: %s\n", r.Results[i].Problem)
		case i < len(r.Results) && r.Results[i].Verification != nil:
			verification := r.Results[i].Verification
			fmt.Fprintf(&out, "%s, observed at %s\n", verification.Verdict, verification.ObservedAt.Local().Format("2006-01-02 15:04:05 MST"))
		case i < len(r.Results) && r.Results[i].Inspection != nil:
			inspection := r.Results[i].Inspection
			fmt.Fprintf(&out, "worker %s, observed at %s", inspection.Worker.Liveness, inspection.ObservedAt.Local().Format("2006-01-02 15:04:05 MST"))
			if inspection.RecordCut {
				fmt.Fprintf(&out, "; record cut at %d of %d bytes", len(inspection.Record), inspection.RecordBytes)
			}
			out.WriteByte('\n')
		default:
			out.WriteString("nothing handed back\n")
		}
	}
	return out.String()
}

// outsideFactoryFlow is why a lane-scoped agent may not read runs: the design
// gives these tools to the program manager watching the factory-flow lane
// alone, and a grant is held by a role, so the lane is checked here as the
// tools' scope.
func (s *Session) outsideFactoryFlow() string {
	lane := s.lane()
	if lane == "" || lane == readmodel.FactoryFlowLane {
		return ""
	}
	return fmt.Sprintf("only the program manager watching the %s lane may read runs, and this one watches %s", readmodel.FactoryFlowLane, lane)
}

func (s *Session) performRunReads(ctx context.Context, requests []runinspect.Request, rounds *int) RunRound {
	var round RunRound
	remaining := runinspect.MaxBytesPerReply
	spent := *rounds >= runinspect.MaxRoundsPerMessage
	if !spent {
		*rounds++
	}
	for _, request := range requests {
		round.Requests = append(round.Requests, request)
		// The run and the expected state are closed identifiers, so they are the
		// whole of what the audit records.
		parameters := map[string]any{"action": request.Action, "run": request.Run}
		if request.Expect != "" {
			parameters["expect"] = string(request.Expect)
		}
		result, err := auditTool(ctx, s, request.Tool(), parameters, 1, func() (runinspect.Result, error) {
			result := runinspect.Result{Action: request.Action, Run: request.Run}
			if spent {
				return result, errors.New("run reads have spent their rounds for this message")
			}
			if refusal := s.outsideFactoryFlow(); refusal != "" {
				return result, errors.New(refusal)
			}
			if s.options.RunInspector == nil {
				return result, errors.New("no run inspector is wired to this conversation")
			}
			if request.Action == runinspect.ActionVerify {
				verification := s.options.RunInspector.Verify(ctx, request.Run, request.Expect)
				result.Verification = &verification
			} else {
				inspection := s.options.RunInspector.Inspect(ctx, request.Run)
				result.Inspection = &inspection
			}
			return result, nil
		}, func(result runinspect.Result) (int, bool, error) {
			encoded, err := json.Marshal(result)
			if err != nil {
				return 0, false, err
			}
			if len(encoded) > remaining {
				return 0, false, errors.New("the reply's allowance for run reads is spent")
			}
			truncated := result.Inspection != nil && result.Inspection.RecordCut
			return len(encoded), truncated, nil
		})
		if err != nil {
			// An audit failure must never expose even an earlier request's evidence.
			if strings.Contains(err.Error(), "audit failed") {
				round.Results = nil
				round.Problem = err.Error()
				return round
			}
			round.Results = append(round.Results, runinspect.Result{Action: request.Action, Run: request.Run, Problem: err.Error()})
			continue
		}
		encoded, _ := json.Marshal(result)
		remaining -= len(encoded)
		round.Results = append(round.Results, result)
	}
	if spent {
		round.Results = nil
		round.Problem = "run reads have spent their rounds for this message"
	}
	return round
}

// runReadTools counts a reply's run requests by the tool each asks for.
func runReadTools(requests []runinspect.Request) map[capability.Capability]int {
	counts := map[capability.Capability]int{}
	for _, request := range requests {
		counts[request.Tool()]++
	}
	return counts
}
