package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/health"
)

// HealthObserver answers health.observe and health.follow for this
// conversation's product. health.Observer is one.
type HealthObserver interface {
	Observe(ctx context.Context, caller health.Caller, runs []string) health.Observation
	Follow(id, cursor string) (health.Part, error)
}

// HealthRound is one round of health requests a reply set off, and what each
// handed back.
type HealthRound struct {
	Requests []health.Request `json:"requests,omitempty"`
	Results  []health.Result  `json:"results,omitempty"`
	Problem  string           `json:"problem,omitempty"`
}

// Render names each request and its answer without repeating the evidence.
func (r HealthRound) Render() string {
	var out strings.Builder
	for i, request := range r.Requests {
		fmt.Fprintf(&out, "%s", request.Tool())
		if request.Action == health.ActionFollow {
			fmt.Fprintf(&out, " %s from %s", request.Observation, request.Cursor)
		} else if len(request.Runs) > 0 {
			fmt.Fprintf(&out, " of %s", strings.Join(request.Runs, ", "))
		}
		out.WriteString(": ")
		switch {
		case r.Problem != "":
			fmt.Fprintf(&out, "nothing handed back: %s\n", r.Problem)
		case i < len(r.Results) && r.Results[i].Problem != "":
			fmt.Fprintf(&out, "refused: %s\n", r.Results[i].Problem)
		case i < len(r.Results) && r.Results[i].Observation != nil:
			observation := r.Results[i].Observation
			fmt.Fprintf(&out, "%s, observed at %s: %s\n", observation.ID, observation.ObservedAt.Local().Format("2006-01-02 15:04:05 MST"), observation.Says)
		case i < len(r.Results) && r.Results[i].Part != nil:
			part := r.Results[i].Part
			fmt.Fprintf(&out, "%d more condition(s) of %d", len(part.Anomalies), part.Totals.Conditions)
			if part.Cursor != "" {
				fmt.Fprintf(&out, "; next cursor %s", part.Cursor)
			}
			out.WriteByte('\n')
		default:
			out.WriteString("nothing handed back\n")
		}
	}
	return out.String()
}

// healthCaller is who an observation is taken for. A program manager's passes
// are recorded under its instance, and every other role's under the role.
func (s *Session) healthCaller() health.Caller {
	caller := health.Caller{Role: s.state.Role}
	if s.state.Role == domain.RoleProgramManager {
		caller.Agent = s.options.Agent
	}
	return caller
}

func (s *Session) performHealthReads(ctx context.Context, requests []health.Request, rounds *int) HealthRound {
	var round HealthRound
	remaining := health.MaxBytesPerReply
	spent := *rounds >= health.MaxRoundsPerMessage
	if !spent {
		*rounds++
	}
	for _, request := range requests {
		round.Requests = append(round.Requests, request)
		// The product, the runs, the observation, and the cursor are closed
		// identifiers, so they are the whole of what the audit records.
		parameters := map[string]any{"action": request.Action}
		if request.Product != "" {
			parameters["product"] = request.Product
		}
		if len(request.Runs) > 0 {
			parameters["runs"] = strings.Join(request.Runs, ",")
		}
		if request.Observation != "" {
			parameters["observation"], parameters["cursor"] = request.Observation, request.Cursor
		}
		result, err := auditTool(ctx, s, request.Tool(), parameters, 1, func() (health.Result, error) {
			result := health.Result{Action: request.Action}
			if spent {
				return result, errors.New("health requests have spent their rounds for this message")
			}
			if refusal := s.outsideFactoryFlow("observe the factory's health"); refusal != "" {
				return result, errors.New(refusal)
			}
			if request.Product != "" && request.Product != string(s.options.ProductID) {
				return result, fmt.Errorf("this conversation observes the product %s, not %s", s.options.ProductID, request.Product)
			}
			if s.options.HealthObserver == nil {
				return result, errors.New("no health observer is wired to this conversation")
			}
			if request.Action == health.ActionFollow {
				part, err := s.options.HealthObserver.Follow(request.Observation, request.Cursor)
				if err != nil {
					return result, err
				}
				result.Part = &part
				return result, nil
			}
			observation := s.options.HealthObserver.Observe(ctx, s.healthCaller(), request.Runs)
			result.Observation = &observation
			return result, nil
		}, func(result health.Result) (int, bool, error) {
			encoded, err := json.Marshal(result)
			if err != nil {
				return 0, false, err
			}
			if len(encoded) > remaining {
				return 0, false, errors.New("the reply's allowance for health requests is spent")
			}
			cut := (result.Observation != nil && result.Observation.Cursor != "") || (result.Part != nil && result.Part.Cursor != "")
			return len(encoded), cut, nil
		})
		if err != nil {
			// An audit failure must never expose even an earlier request's evidence.
			if strings.Contains(err.Error(), "audit failed") {
				round.Results = nil
				round.Problem = err.Error()
				return round
			}
			round.Results = append(round.Results, health.Result{Action: request.Action, Problem: err.Error()})
			continue
		}
		encoded, _ := json.Marshal(result)
		remaining -= len(encoded)
		round.Results = append(round.Results, result)
	}
	if spent {
		round.Results = nil
		round.Problem = "health requests have spent their rounds for this message"
	}
	return round
}

// healthTools counts a reply's health requests by the tool each asks for.
func healthTools(requests []health.Request) map[capability.Capability]int {
	counts := map[capability.Capability]int{}
	for _, request := range requests {
		counts[request.Tool()]++
	}
	return counts
}
