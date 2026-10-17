package chat

// These envelopes add the common audit without migrating any block protocol or
// moving the session, proposal, decision or replay semantics out of chat.

import (
	"context"
	"errors"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
	"github.com/mason-bryant/yoyodyne/internal/research"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func (s *Session) trackerTool(verb string) capability.Capability {
	if s.authority().LaneScoped(verb) {
		return laneCapabilities[verb]
	}
	return trackerCapabilities[verb]
}

func (s *Session) performRepositoryReads(ctx context.Context, requests []repositoryread.Request, rounds *int) ([]repositoryread.Result, string) {
	var problem string
	run := func() ([]repositoryread.Result, error) {
		results, why := s.performRepositoryReadsWithoutToolAudit(ctx, requests, rounds)
		problem = why
		if why != "" {
			return results, errors.New(why)
		}
		return results, nil
	}
	// The single existing reader call retains its aggregate budget and result
	// order. Mixed read/list blocks audit both grants around that same call.
	for _, verb := range []string{repositoryread.ActionRead, repositoryread.ActionList, repositoryread.ActionSearch} {
		count := 0
		for _, request := range requests {
			if request.Action == verb {
				count++
			}
		}
		if count == 0 {
			continue
		}
		inner := run
		id := repositoryTool(verb)
		run = func() ([]repositoryread.Result, error) {
			return auditTool(ctx, s, id, nil, count, inner, func(results []repositoryread.Result) (int, bool, error) {
				size := 0
				truncated := false
				for _, result := range results {
					if result.Action != verb {
						continue
					}
					size += result.ReturnedBytes()
					truncated = truncated || result.Truncated
					if result.Problem != "" {
						return size, truncated, errors.New(result.Problem)
					}
				}
				return size, truncated, nil
			})
		}
	}
	results, err := run()
	if err != nil && (len(results) == 0 || strings.Contains(err.Error(), "audit failed")) {
		return nil, err.Error()
	}
	return results, problem
}

// repositoryTool is the tool one action in the repository block is audited
// and granted under.
func repositoryTool(verb string) capability.Capability {
	switch verb {
	case repositoryread.ActionList:
		return capability.RepositoryList
	case repositoryread.ActionSearch:
		return capability.RepositorySearch
	default:
		return capability.RepositoryRead
	}
}

func (s *Session) performResearch(ctx context.Context, queries []research.Query, rounds *int) ([]research.Finding, string) {
	results, err := auditTool(ctx, s, capability.ResearchCommission, nil, len(queries), func() ([]research.Finding, error) {
		findings, problem := s.performResearchWithoutToolAudit(ctx, queries, rounds)
		if problem != "" {
			return findings, errors.New(problem)
		}
		return findings, nil
	}, func(findings []research.Finding) (int, bool, error) {
		size := 0
		truncated := false
		for _, finding := range findings {
			size += len(finding.Evidence)
			truncated = truncated || finding.Truncated
			if finding.Problem != "" {
				return size, truncated, errors.New(finding.Problem)
			}
		}
		return size, truncated, nil
	})
	if err != nil && (len(results) == 0 || strings.Contains(err.Error(), "audit failed")) {
		return nil, err.Error()
	}
	return results, ""
}

func (s *Session) recordReports(entries []report.Entry) ([]report.Report, string) {
	if len(entries) == 0 {
		return nil, ""
	}
	results, err := auditTool(context.Background(), s, capability.ReportFile, nil, len(entries), func() ([]report.Report, error) {
		recorded, problem := s.recordReportsWithoutToolAudit(entries)
		if problem != "" {
			return recorded, errors.New(problem)
		}
		return recorded, nil
	}, nil)
	if err != nil {
		return results, err.Error()
	}
	return results, ""
}

func (s *Session) performMemoryWrites(ctx context.Context, writes []MemoryWrite) ([]MemoryOutcome, error) {
	var handlerErr error
	outcomes, err := auditTool(ctx, s, capability.AgentContextMutate, nil, len(writes), func() ([]MemoryOutcome, error) {
		outcomes, e := s.performMemoryWritesWithoutToolAudit(ctx, writes)
		handlerErr = e
		return outcomes, e
	}, func(outcomes []MemoryOutcome) (int, bool, error) {
		for _, outcome := range outcomes {
			if outcome.Failure != "" {
				return 0, false, errors.New(outcome.Failure)
			}
		}
		return 0, false, nil
	})
	if err != nil && (len(outcomes) == 0 || strings.Contains(err.Error(), "audit failed")) {
		return nil, err
	}
	return outcomes, handlerErr
}

func (s *Session) writeLaneReport(ctx context.Context, content *runstate.LaneReportContent, problem error) (LaneReportOutcome, error) {
	var handlerErr error
	outcome, err := auditTool(ctx, s, capability.LaneReportWrite, nil, 1, func() (LaneReportOutcome, error) {
		outcome, e := s.writeLaneReportWithoutToolAudit(ctx, content, problem)
		handlerErr = e
		return outcome, e
	}, func(outcome LaneReportOutcome) (int, bool, error) {
		if outcome.Failure != "" {
			return 0, false, errors.New(outcome.Failure)
		}
		return 0, false, nil
	})
	if err != nil && (outcome.Failure == "" || strings.Contains(err.Error(), "audit failed")) {
		return outcome, err
	}
	return outcome, handlerErr
}

func (s *Session) performRestartRequest(ask RestartAsk) RestartOutcome {
	outcome, err := auditTool(context.Background(), s, capability.ServiceRequestRestart, nil, 1, func() (RestartOutcome, error) { return s.performRestartRequestWithoutToolAudit(ask), nil }, func(outcome RestartOutcome) (int, bool, error) {
		if outcome.Failure != "" {
			return 0, false, errors.New(outcome.Failure)
		}
		return 0, false, nil
	})
	if err != nil && outcome.Failure == "" {
		outcome.Failure = err.Error()
	}
	return outcome
}

// performObserved records a hand step through the report tool: a hand step is
// a report about the line, so it is audited under the grant that files one,
// named by its own block.
func (s *Session) performObserved(ask ObservedAsk) ObservedOutcome {
	parameters := map[string]any{"block": strings.TrimPrefix(interventionFence, "```"), "kind": ask.Kind}
	outcome, err := auditTool(context.Background(), s, capability.ReportFile, parameters, 1, func() (ObservedOutcome, error) {
		return s.performObservedWithoutToolAudit(ask), nil
	}, func(outcome ObservedOutcome) (int, bool, error) {
		if outcome.Failure != "" {
			return 0, false, errors.New(outcome.Failure)
		}
		return 0, false, nil
	})
	if err != nil && outcome.Failure == "" {
		outcome = ObservedOutcome{Ask: ask, Failure: err.Error()}
	}
	return outcome
}

func (s *Session) performStopCauses(ctx context.Context, ask StopCauseAsk) StopCauseOutcome {
	outcome, err := auditTool(ctx, s, capability.StopCauseRecord, nil, 1, func() (StopCauseOutcome, error) { return s.performStopCausesWithoutToolAudit(ctx, ask), nil }, func(outcome StopCauseOutcome) (int, bool, error) {
		if outcome.Failure != "" {
			return 0, false, errors.New(outcome.Failure)
		}
		return 0, false, nil
	})
	if err != nil && outcome.Failure == "" {
		outcome.Failure = err.Error()
	}
	return outcome
}

func (s *Session) performPassRequest(ask PassAsk) PassRequestOutcome {
	outcome, err := auditTool(context.Background(), s, capability.PassRequest, nil, 1, func() (PassRequestOutcome, error) { return s.performPassRequestWithoutToolAudit(ask), nil }, func(outcome PassRequestOutcome) (int, bool, error) {
		if outcome.Failure != "" {
			return 0, false, errors.New(outcome.Failure)
		}
		return 0, false, nil
	})
	if err != nil && outcome.Failure == "" {
		outcome.Failure = err.Error()
	}
	return outcome
}

func (s *Session) conductAsk(ctx context.Context, ask exchange.Ask) conducted {
	outcome, err := auditTool(ctx, s, capability.ExchangeAsk, nil, 1, func() (conducted, error) { return s.conductAskWithoutToolAudit(ctx, ask), nil }, func(outcome conducted) (int, bool, error) {
		if outcome.round.Problem != "" {
			return 0, false, errors.New(outcome.round.Problem)
		}
		return len(outcome.round.Answer), false, nil
	})
	if err != nil && (outcome.delivery == "" || strings.Contains(err.Error(), "audit failed")) {
		outcome.delivery = askUnavailable(err)
		outcome.round.Problem = err.Error()
	}
	return outcome
}

// refuseAsk records a bounded request through the tool envelope without opening
// an exchange. Keep the message's existing refusal unless auditing itself fails.
func (s *Session) refuseAsk(ctx context.Context, ask exchange.Ask, problem string) ExchangeRound {
	_, err := auditTool(ctx, s, capability.ExchangeAsk, nil, 1, func() (struct{}, error) {
		return struct{}{}, errors.New(problem)
	}, nil)
	if err != nil && strings.Contains(err.Error(), "audit failed") {
		problem = err.Error()
	}
	return ExchangeRound{Asked: ask.Role, Question: oneLineAsk(ask), Problem: problem}
}

func (s *Session) recordEvaluation(entry evaluation.Entry) (*evaluation.Evaluation, error) {
	return auditTool(context.Background(), s, capability.EvaluationRecord, nil, 1, func() (*evaluation.Evaluation, error) { return s.recordEvaluationWithoutToolAudit(entry) }, nil)
}

// writeTool is the document tool this role's bundle grants: the architect's
// design write where it holds one, and otherwise the product write.
func (s *Session) writeTool() capability.Capability {
	if rolecapability.MustDefault().Holds(s.state.Role, capability.ArtifactDesignMutate) {
		return capability.ArtifactDesignMutate
	}
	return capability.ArtifactProductMutate
}

func (s *Session) recordWrites(writes []artifact.Write) ([]PendingWrite, error) {
	if len(writes) == 0 {
		return nil, nil
	}
	return auditTool(context.Background(), s, s.writeTool(), nil, len(writes), func() ([]PendingWrite, error) { return s.recordWritesWithoutToolAudit(writes) }, nil)
}

// auditRefusedWrites records a document block refused before it was recorded.
// The refusal is handed back to the role as a result; only a failure to write
// either audit event stops the turn.
func (s *Session) auditRefusedWrites(ctx context.Context, count int, refusal error) error {
	_, err := auditTool(ctx, s, s.writeTool(), nil, max(count, 1), func() (struct{}, error) { return struct{}{}, refusal }, nil)
	if err != nil && strings.Contains(err.Error(), "audit failed") {
		return err
	}
	return nil
}

func (s *Session) recordProposals(proposals []Proposal, resembling []string) ([]PendingProposal, error) {
	if len(proposals) == 0 {
		return nil, nil
	}
	return auditTool(context.Background(), s, capability.ProposalRaise, nil, len(proposals), func() ([]PendingProposal, error) { return s.recordProposalsWithoutToolAudit(proposals, resembling) }, nil)
}

func (s *Session) recordConcerns(concerns []Concern) ([]PendingConcern, error) {
	if len(concerns) == 0 {
		return nil, nil
	}
	return auditTool(context.Background(), s, capability.ConcernRaise, nil, len(concerns), func() ([]PendingConcern, error) { return s.recordConcernsWithoutToolAudit(concerns) }, nil)
}
