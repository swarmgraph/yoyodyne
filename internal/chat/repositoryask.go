package chat

// Asking the reading helper one question about the repository, on a
// conversation role's behalf (docs/designs/reducing-what-the-factory-costs.md,
// section 7).
//
// The role holds no tool and gets none. It names a question and, optionally,
// path prefixes; the harness runs one session of its own on the lightest model,
// handed only the files Git tracks at the commit the turn reads at
// (internal/repositoryask). That session belongs to the harness, as research
// does: it is metered here against the asking role and this conversation, it is
// taken only on the message budget's leave, and what it says comes back framed
// as untrusted evidence that points at places rather than standing for them.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/budget"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repositoryask"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// RepositoryAsker answers one ask. It is satisfied by repositoryask.Asker.
type RepositoryAsker interface {
	Ask(ctx context.Context, request repositoryask.Request, session repositoryask.Session) repositoryask.Answer
}

// toolRepositoryAsk is the reading helper as a stop names it to a person.
const toolRepositoryAsk = "asking the reading helper"

// phaseRepositoryAsk is what the turn is doing while the helper answers.
const phaseRepositoryAsk = "waiting for the reading helper's answer"

// repositoryAskFraming is how the answer is labelled for this role, as a
// repository read is (repositoryFraming).
func (s *Session) repositoryAskFraming() repositoryask.Framing {
	if s.state.Role == domain.RoleProductManager {
		return repositoryask.AsDescription
	}
	return repositoryask.AsEvidence
}

// performRepositoryAsk answers one ask under the tool audit. It never fails
// the turn: whatever stopped the answer is on it, for the role to be told.
func (s *Session) performRepositoryAsk(ctx context.Context, request repositoryask.Request, rounds *int, permit budget.Permission) repositoryask.Answer {
	parameters := map[string]any{"question": request.Question}
	if len(request.Paths) > 0 {
		parameters["paths"] = strings.Join(request.Paths, ", ")
	}
	answer, err := auditTool(ctx, s, capability.RepositoryAsk, parameters, 1, func() (repositoryask.Answer, error) {
		answer := s.performRepositoryAskWithoutToolAudit(ctx, request, rounds, permit)
		if answer.Problem != "" {
			return answer, errors.New(answer.Problem)
		}
		return answer, nil
	}, func(answer repositoryask.Answer) (int, bool, error) {
		if answer.Problem != "" {
			return 0, false, errors.New(answer.Problem)
		}
		return answer.ReturnedBytes(), answer.Truncated, nil
	})
	if err != nil && answer.Problem == "" {
		answer = repositoryask.Answer{Question: request.Question, Paths: request.Paths, Why: request.Why, Problem: singleLine(err.Error(), maxTrackerFailureBytes)}
	}
	return answer
}

func (s *Session) performRepositoryAskWithoutToolAudit(ctx context.Context, request repositoryask.Request, rounds *int, permit budget.Permission) repositoryask.Answer {
	refused := func(problem string) repositoryask.Answer {
		return repositoryask.Answer{Question: request.Question, Paths: request.Paths, Why: request.Why, Problem: problem}
	}
	if s.options.RepositoryAsk == nil {
		return refused("no reading helper is wired to this conversation, so nothing was asked")
	}
	if *rounds >= repositoryask.MaxRoundsPerMessage {
		return refused(fmt.Sprintf("one message asks the reading helper at most %d time(s), and this one has; nothing further was asked", repositoryask.MaxRoundsPerMessage))
	}
	// The helper's session is a paid call, taken only on the budget's leave.
	if !permit.Granted() {
		return refused("this message's budget allows no further provider call, so the reading helper was not asked")
	}
	*rounds++
	attribution := s.spendAttribution()
	attribution.Within = fmt.Sprintf("the reading helper, asked in turn %d", s.state.Turns)
	provider := spend.Metered{
		Provider:    s.options.Backend,
		Log:         s.options.Spend,
		Attribution: attribution,
		Clock:       s.options.Clock,
		ToolUse:     s.options.ToolUse,
		// The same trade the conversation's own meter makes: an answer already
		// paid for is not thrown away because the cost log would not take it.
		RecordFailure: func(err error) {
			s.spendProblem = appendProblem(s.spendProblem, singleLine(err.Error(), maxTrackerFailureBytes))
		},
		// Counted with the message's own spend, so a spending limit on the
		// message holds the helper to it as well.
		Recorded: s.countSpend,
	}
	answer := s.options.RepositoryAsk.Ask(ctx, request, repositoryask.Session{
		Provider:     usageLimitNoting{session: s, provider: provider},
		Role:         s.state.Role,
		RunID:        s.state.ConversationID,
		Model:        s.options.RepositoryAskModel,
		AccountAlias: s.options.AccountAlias,
	})
	if err := s.emit(execution.EventRepositoryRead, repositoryAskPayload(answer)); err != nil {
		answer.Problem = singleLine(fmt.Sprintf("the reading helper answered and its answer could not be recorded on the conversation, so nothing was handed back: %v", err), maxTrackerFailureBytes)
	}
	return answer
}

// usageLimitNoting records an exhausted usage limit met by the helper's
// session against this conversation, as one met by the conversation's own turn
// is (noteUsageLimit): the session has no run to park, and a refusal nobody
// recorded is hours of nothing that nobody is told about. The ask itself ends
// with the refusal on its answer; it is not waited out, because the role can
// carry on by reading what it needs instead.
type usageLimitNoting struct {
	session  *Session
	provider repositoryask.Provider
}

func (u usageLimitNoting) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	result, err := u.provider.Run(ctx, request)
	if noteErr := u.session.noteUsageLimit(result, err, request.Model, request.AccountAlias); noteErr != nil {
		err = errors.Join(err, noteErr)
	}
	return result, err
}

// repositoryAskPayload is what one ask leaves on the conversation's log: the
// question, where and at which commit it was answered, on which model, and the
// places it pointed at. The answer's text is not copied into the record, for the
// reason a read's content is not.
func repositoryAskPayload(answer repositoryask.Answer) map[string]any {
	payload := map[string]any{
		"action":   "ask",
		"question": answer.Question,
		"commit":   answer.Commit,
		"model":    answer.Model,
	}
	if len(answer.Paths) > 0 {
		payload["paths"] = answer.Paths
	}
	if answer.Why != "" {
		payload["why"] = answer.Why
	}
	if answer.Problem != "" {
		payload["problem"] = answer.Problem
		return payload
	}
	places := make([]string, 0, len(answer.Citations))
	for _, citation := range answer.Citations {
		places = append(places, citation.String())
	}
	payload["citations"] = places
	payload["handed"] = answer.Handed
	payload["left_out"] = answer.LeftOut
	payload["truncated"] = answer.Truncated
	return payload
}

// reportRepositoryAsks tells the operator what the reading helper was asked
// and what it pointed at.
func (s *Session) reportRepositoryAsks(out io.Writer, reply Reply) {
	if len(reply.RepositoryAsks) == 0 {
		return
	}
	fmt.Fprintf(out, "[reading helper] %d question(s) asked\n", len(reply.RepositoryAsks))
	for _, answer := range reply.RepositoryAsks {
		fmt.Fprint(out, indent(answer.Describe()))
	}
	fmt.Fprintln(out)
}
