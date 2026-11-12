package chat

// A turn the provider refused because nobody is logged into it or nobody can
// reach it, written down where somebody who is not at this terminal can read
// it.
//
// It is the usage-limit note's twin and differs in what it records: a limit is
// one refusal in a log the read model adds up, and an outage is one standing
// fact every surface names. So the record here is the product's outage, kept
// standing while turns keep meeting it and cleared by the first turn the
// provider serves — which is what makes re-authentication resume the line
// without anybody releasing anything. An attended turn waits within its
// provider waiting budget; a background turn defers to its next cadence.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ProviderOutages is where the provider answering nobody is recorded and
// cleared. It is satisfied by *runstate.ProviderOutageStore.
type ProviderOutages interface {
	Notice(observed runstate.ProviderOutageObservation) (runstate.ProviderOutage, error)
	Standing() (runstate.ProviderOutage, bool, error)
	Clear() (runstate.ProviderOutage, bool, error)
}

// ErrProviderAway marks the failure of a turn the provider refused because
// nobody is logged into it or nobody can reach it. It is a sentinel joined into
// the error the turn fails with, for the reason ErrProviderCapacity is: what an
// operator reads is unchanged, and a caller that is not a person can tell that
// the role was never asked — and, unlike a limit, that no reset is coming and
// nothing but a person or the network ends it.
var ErrProviderAway = errors.New("the provider is answering nobody, so the role was never asked")

// noteProviderOutage records the outage this turn met, and reports the error
// the turn fails with for it: the sentinel, and the wait named the way every
// surface names it. A turn the provider served, and a conversation with
// nowhere to record one, both record nothing and say nothing.
func (s *Session) noteProviderOutage(result backend.RunResult, err error) error {
	outage := providerAway(result, err)
	if outage == nil {
		return nil
	}
	failed := fmt.Errorf("%w: %s", ErrProviderAway, runstate.DescribeProviderOutage(outage.Cause))
	if s.options.ProviderOutages == nil {
		return failed
	}
	provider := s.options.Provider
	if outage.Provider != "" {
		provider = outage.Provider
	}
	recorded, recordErr := s.options.ProviderOutages.Notice(runstate.ProviderOutageObservation{
		Cause:        outage.Cause,
		Provider:     provider,
		AccountAlias: s.options.AccountAlias,
		Detail:       outage.Detail,
		Channel:      outage.Channel,
		Waiting:      fmt.Sprintf("the %s conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
		At:           s.options.clock().Now(),
	})
	if recordErr != nil {
		return errors.Join(failed, fmt.Errorf("record that the provider is answering nobody: %w", recordErr))
	}
	// A turn that opens an outage over a missing program files its one report,
	// as a run that opened it would (runstate.ProgramMissingReport).
	if recorded.OpensProgramMissing() && s.options.Reports != nil {
		reported, err := runstate.ProgramMissingReport(recorded, s.options.Build, s.options.RepositoryID, s.options.clock().Now())
		if err == nil {
			err = s.options.Reports.Append(reported)
		}
		if err != nil {
			return errors.Join(failed, fmt.Errorf("report that the provider's program cannot be found: %w", err))
		}
	}
	return failed
}

// noteProviderServed records that the provider answered this turn, which ends
// any outage standing on the product. It is asked after every served turn
// rather than only after one that was refused first, because the process that
// finds the provider answering again is rarely the one that met it refusing.
// What went wrong clearing it is carried on the session's own problems rather
// than failing a turn the provider served.
func (s *Session) noteProviderServed() {
	if s.options.ProviderOutages == nil {
		return
	}
	// A missing program of another provider is not this turn's to end
	// (runstate.ProviderOutage.ClearedBy).
	if standing, found, err := s.options.ProviderOutages.Standing(); err == nil && (!found || !standing.ClearedBy(s.options.Provider)) {
		return
	}
	if _, _, err := s.options.ProviderOutages.Clear(); err != nil {
		s.failoverProblem = appendProblem(s.failoverProblem, singleLine(
			fmt.Sprintf("record that the provider is answering again: %v", err), maxTrackerFailureBytes))
	}
}

// CapacityServed is where a served turn is written down as the evidence that a
// usage window on its account and model has lifted. It is satisfied by
// *runstate.CapacityServedStore.
type CapacityServed interface {
	Record(served runstate.CapacityServed) error
}

// noteCapacityServed records the account and model this turn was served on,
// which every reading of the provider's refusals takes as the window having
// lifted for each refusal of that account and model recorded before it. What
// went wrong recording it is carried on the session's own problems, as the
// outage's clearing is.
func (s *Session) noteCapacityServed(endpoint backend.Endpoint) {
	if s.options.CapacityServed == nil || strings.TrimSpace(endpoint.Model) == "" {
		return
	}
	if err := s.options.CapacityServed.Record(runstate.CapacityServed{
		AccountAlias: endpoint.AccountAlias,
		Model:        endpoint.Model,
		At:           s.options.clock().Now(),
		What:         fmt.Sprintf("a turn of the %s conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
	}); err != nil {
		s.failoverProblem = appendProblem(s.failoverProblem, singleLine(
			fmt.Sprintf("record that the provider served %s: %v", endpoint.Model, err), maxTrackerFailureBytes))
	}
}

// providerAway reports a turn the provider refused because nobody is logged
// into it or nobody can reach it, rather than one it answered. Like a limit it
// is only a refusal where the invocation actually failed.
func providerAway(result backend.RunResult, err error) *backend.ProviderOutage {
	if result.ProviderOutage == nil || (err == nil && !result.IsError) {
		return nil
	}
	return result.ProviderOutage
}
