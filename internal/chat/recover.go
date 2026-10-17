package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runrecover"
)

// RunRecoverer carries out run.recover for this conversation's product.
// runrecover.Recoverer is one.
type RunRecoverer interface {
	Recover(ctx context.Context, by domain.AgentRole, request runrecover.Request) runrecover.Result
}

// RecoveryRound is one round of run.recover requests a reply set off, and what
// each came to.
type RecoveryRound struct {
	Requests []runrecover.Request `json:"requests,omitempty"`
	Results  []runrecover.Result  `json:"results,omitempty"`
	Problem  string               `json:"problem,omitempty"`
}

// Render names each request and what it came to.
func (r RecoveryRound) Render() string {
	var out strings.Builder
	for i, request := range r.Requests {
		fmt.Fprintf(&out, "run.recover %s %q on %s: ", request.ID, request.Action, request.Run)
		switch {
		case r.Problem != "":
			fmt.Fprintf(&out, "nothing done: %s\n", r.Problem)
		case i < len(r.Results):
			result := r.Results[i]
			fmt.Fprintf(&out, "%s", result.Outcome)
			if result.Refusal != "" {
				fmt.Fprintf(&out, " (%s)", result.Refusal)
			}
			if result.Repeat {
				out.WriteString(", already recorded")
			}
			fmt.Fprintf(&out, ": %s\n", result.Says)
		default:
			out.WriteString("nothing done\n")
		}
	}
	return out.String()
}

// performRecoveries carries a reply's run.recover requests out, one at a time
// and in order, each audited. Like the run reads it never fails the turn: what
// each request came to is handed back, and a spent bound ends recoveries for
// this message.
func (s *Session) performRecoveries(ctx context.Context, requests []runrecover.Request, rounds *int) RecoveryRound {
	var round RecoveryRound
	spent := *rounds >= runrecover.MaxRoundsPerMessage
	if !spent {
		*rounds++
	}
	if spent {
		round.Requests = requests
		round.Problem = "run.recover has spent its rounds for this message"
		return round
	}
	for _, request := range requests {
		round.Requests = append(round.Requests, request)
		// Identifiers and closed values only: the reason and a hold's sentences are
		// on the run's record, where the request is kept whole.
		parameters := map[string]any{"request": request.ID, "run": request.Run, "action": string(request.Action), "observation": request.Observation,
			"expect_status": string(request.ExpectStatus), "expect_phase": string(request.ExpectPhase)}
		result, err := auditTool(ctx, s, request.Tool(), parameters, 1, func() (runrecover.Result, error) {
			if s.options.RunRecoverer == nil {
				return runrecover.Result{}, errors.New("no run.recover is wired to this conversation")
			}
			return s.options.RunRecoverer.Recover(ctx, s.state.Role, request), nil
		}, func(result runrecover.Result) (int, bool, error) {
			encoded, err := json.Marshal(result)
			if err != nil {
				return 0, false, err
			}
			if len(encoded) > runrecover.MaxResultBytes {
				return 0, false, errors.New("the result is larger than one request may hand back")
			}
			return len(encoded), false, nil
		})
		if err != nil {
			if strings.Contains(err.Error(), "audit failed") {
				// An act whose audit could not be written is still an act the harness
				// may have made: what it came to is the run's record, which the next
				// request reads back.
				round.Results = nil
				round.Problem = err.Error() + "; what was done to the run, if anything, is on its record, and a repeat of the same request reads it back"
				return round
			}
			// A refusal before the act changed nothing. One after it — the answer
			// was larger than a request may hand back — hides an act that may have
			// been made, so it is unknown, and a repeat reads the run back.
			if strings.Contains(err.Error(), "byte bound") {
				round.Results = append(round.Results, runrecover.Result{Request: request.ID, Run: request.Run, Action: request.Action, Outcome: runrecover.Unknown,
					Says: "the request was carried out, and its answer was too large to hand back; a repeat of the same request reads the run back and answers from its record"})
				continue
			}
			round.Results = append(round.Results, runrecover.Result{Request: request.ID, Run: request.Run, Action: request.Action, Outcome: runrecover.Refused, Says: err.Error() + "; nothing was done"})
			continue
		}
		round.Results = append(round.Results, result)
	}
	return round
}
