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
	for _, verb := range []string{repositoryread.ActionRead, repositoryread.ActionList} {
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
		id := capability.RepositoryRead
		if verb == repositoryread.ActionList {
			id = capability.RepositoryList
		}
		run = func() ([]repositoryread.Result, error) {
			return auditTool(ctx, s, id, nil, count, inner, func(results []repositoryread.Result) (int, bool, error) {
				size := 0
				truncated := false
				for _, result := range results {
					if result.Action != verb {
						continue
					}
					size += len(result.Content)
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

func (s *Session) recordEvaluation(entry evaluation.Entry) (*evaluation.Evaluation, error) {
	return auditTool(context.Background(), s, capability.EvaluationRecord, nil, 1, func() (*evaluation.Evaluation, error) { return s.recordEvaluationWithoutToolAudit(entry) }, nil)
}

func (s *Session) recordWrites(writes []artifact.Write) ([]PendingWrite, error) {
	if len(writes) == 0 {
		return nil, nil
	}
	id := capability.ArtifactProductMutate
	if rolecapability.MustDefault().Holds(s.state.Role, capability.ArtifactDesignMutate) {
		id = capability.ArtifactDesignMutate
	}
	return auditTool(context.Background(), s, id, nil, len(writes), func() ([]PendingWrite, error) { return s.recordWritesWithoutToolAudit(writes) }, nil)
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
