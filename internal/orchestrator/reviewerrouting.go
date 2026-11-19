package orchestrator

// Running a run's independent review on the reviewer's own endpoint pair
// (docs/designs/claude-execution-and-account-routing.md).
//
// A project turns this on by enabling fallback on the reviewer agent
// (agents.<reviewer>.failover). A project that does not has none of this: its
// reviews are made exactly as they always were, under the run's account on the
// configured reviewer. Once a run has a reviewer pair recorded it is routed by
// that record, so a reload that switched fallback off changes nothing about a
// run already going.
//
// The pair is the reviewer agent's alone. It is resolved from the reviewer's
// own configuration (ResolveReviewerEndpoints), so the developer slot a run
// occupies, the labels it carries and the account its developer was affined to
// have no say in which endpoint judges it, and the review is made under the
// endpoint's own account rather than the developer's.
//
// Each review of one candidate — one commit — is one logical operation, found
// again on the run's record by the candidate it judges, so a second asking, a
// wait, a relaunch, and a restart all stay inside it, and a review of any other
// candidate is a new operation that starts from the primary. Every provider
// launch under it is a separately recorded attempt, started behind the launch
// gate so the process is written down before the provider can begin work.
//
// An operation moves from its primary to its alternate once, and only for a
// usage limit: the provider classifying the refusal as one, or the primary
// already known to be limited before anything is launched. The alternate has to
// be able to serve the reviewer role under its read-only posture and be
// installed, logged in, declared, and not itself known to be limited; one that
// cannot leaves the operation waiting on its primary with the reason recorded.
// Once switched it stays on the alternate, so a wait or a retry is made there.
//
// Every attempt is independent of the developer by construction. The review's
// evidence is built whole by the harness for the exact candidate, no session of
// the developer's is ever offered, and an attempt on another endpoint than the
// last review attempt starts a new session of its own and is recorded as
// reconstructed from that evidence. A result is accepted only for the attempt
// and candidate it was produced for (runstate's AcceptResult): one for another
// candidate, or an attempt that already ended another way, decides nothing, and
// a review that did not end in a verdict is never one.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReviewerForEndpoint builds the reviewer that judges on one endpoint of a
// reviewer pair, with the reviewer agent's own persona and time limit, and
// reports false for an endpoint this build cannot launch.
type ReviewerForEndpoint func(endpoint runstate.RoutedEndpoint) (ChangeReviewer, bool)

// reviewerRoute is one logical review operation and the attempt being made
// under it now.
type reviewerRoute struct {
	operation string
	candidate string
	attempt   string
	launch    *runstate.AttemptLaunch
	endpoint  runstate.RoutedEndpoint
	choice    runstate.EndpointChoice
	reviewer  ChangeReviewer
	// firstEvent is the first event sequence the attempt can have written.
	firstEvent uint64
	gate       *execution.LaunchGate
	// reconstructed is set when the attempt runs on another endpoint than the
	// run's last review attempt, so any session the earlier one opened is not
	// offered to it.
	reconstructed bool
}

// reviewerPair is the reviewer's endpoint pair resolved now, and false where
// the project has not enabled fallback for it.
func (p Pipeline) reviewerPair() (config.ResolvedEndpointPair, bool, error) {
	name := p.agentNameForRole(domain.RoleReviewer)
	if name == "" || p.ReviewerFor == nil {
		return config.ResolvedEndpointPair{}, false, nil
	}
	if _, ok := p.routingStore(); !ok {
		return config.ResolvedEndpointPair{}, false, nil
	}
	pair, err := (config.Resolved{Config: p.Config}).ResolveReviewerEndpoints(name)
	if err != nil {
		return config.ResolvedEndpointPair{}, false, fmt.Errorf("resolve the reviewer's endpoints: %w", err)
	}
	if !pair.Enabled || pair.Alternate == nil {
		return config.ResolvedEndpointPair{}, false, nil
	}
	return pair, true, nil
}

// routedReviewer reports a run whose reviews are made on a recorded pair.
func (a *activeRun) routedReviewer() bool {
	return a.state.Routing != nil && a.state.Routing.Reviewer != nil
}

// pinReviewerRouting records the reviewer's pair on the run the first time a
// review is made in a project that enables reviewer fallback. A run that has a
// pair keeps it, whatever the configuration says now.
func (a *activeRun) pinReviewerRouting(ctx context.Context) error {
	if a.routedReviewer() || a.state.HarnessAuthored() {
		return nil
	}
	pair, routed, err := a.pipeline.reviewerPair()
	if err != nil || !routed {
		return err
	}
	snapshot := runstate.RoutingSnapshotOf(pair, runstate.RoutingClaimed, a.pipeline.clock().Now())
	if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
		return routing.RecordSnapshot(snapshot)
	}); err != nil {
		return fmt.Errorf("pin the reviewer's endpoints to this run: %w", err)
	}
	return nil
}

func (a *activeRun) openReviewOperation() *runstate.RoutedOperation {
	if a.state.Routing == nil {
		return nil
	}
	for index := range a.state.Routing.Operations {
		operation := &a.state.Routing.Operations[index]
		if operation.Role == domain.RoleReviewer && operation.Completed == nil {
			return operation
		}
	}
	return nil
}

func (a *activeRun) reviewOperation(id string) (runstate.RoutedOperation, error) {
	operation, ok := a.state.Routing.Operation(id)
	if !ok {
		return runstate.RoutedOperation{}, fmt.Errorf("review operation %s is not on this run's record", id)
	}
	return *operation, nil
}

func describeReviewOf(candidate string) string {
	return "the review of candidate " + candidate
}

// beginReviewOperation finds or opens the operation that judges the candidate,
// settles anything an earlier process left running under it, and answers nil
// for a run whose reviews are not routed. An operation open for another
// candidate is over: the candidate it judged is gone.
func (a *activeRun) beginReviewOperation(ctx context.Context, candidate string) (*reviewerRoute, error) {
	if err := a.pinReviewerRouting(ctx); err != nil {
		return nil, err
	}
	if !a.routedReviewer() {
		return nil, nil
	}
	if err := a.adoptStoredRouting(); err != nil {
		return nil, err
	}
	now := a.pipeline.clock().Now()
	open := a.openReviewOperation()
	if open != nil && open.Candidate != candidate {
		superseded := open.ID
		if err := a.reconcileOperation(ctx, superseded, "review"); err != nil {
			return nil, err
		}
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.CompleteOperation(superseded, "superseded: its candidate "+open.Candidate+" is not the candidate now under review", now)
		}); err != nil {
			return nil, fmt.Errorf("close review operation %s: %w", superseded, err)
		}
		open = nil
	}
	id := ""
	if open != nil {
		id = open.ID
	} else {
		minted, err := runstate.NewRoutingID("op")
		if err != nil {
			return nil, err
		}
		rounds := a.state.ReviewRounds
		request := runstate.OperationRequest{ID: minted, Kind: runstate.OperationReview, Candidate: candidate, Budget: runstate.OperationBudget{ReviewRounds: &rounds}}
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.OpenOperation(request, now)
		}); err != nil {
			return nil, fmt.Errorf("open the review operation for %s: %w", describeReviewOf(candidate), err)
		}
		id = minted
	}
	if err := a.reconcileOperation(ctx, id, "review"); err != nil {
		return nil, err
	}
	return &reviewerRoute{operation: id, candidate: candidate}, nil
}

// selectedReviewEndpoint is the endpoint the operation has selected.
func (a *activeRun) selectedReviewEndpoint(operation runstate.RoutedOperation) (runstate.RoutedEndpoint, runstate.EndpointChoice) {
	snapshot := a.state.Routing.Reviewer
	if operation.Selected == runstate.EndpointAlternate && snapshot.Alternate != nil {
		return *snapshot.Alternate, runstate.EndpointAlternate
	}
	return snapshot.Primary, runstate.EndpointPrimary
}

// lastReviewEndpoint is the endpoint the run's latest review attempt that began
// work ran on, and nil before any did.
func (a *activeRun) lastReviewEndpoint() *runstate.RoutedEndpoint {
	var latest *runstate.InvocationAttempt
	for _, operation := range a.state.Routing.Operations {
		if operation.Role != domain.RoleReviewer {
			continue
		}
		for index := range operation.Attempts {
			attempt := &operation.Attempts[index]
			if attempt.LaunchedAt == nil {
				continue
			}
			if latest == nil || attempt.LaunchedAt.After(*latest.LaunchedAt) {
				latest = attempt
			}
		}
	}
	if latest == nil {
		return nil
	}
	endpoint := latest.Endpoint
	return &endpoint
}

// prepareReviewAttempt reserves the operation's next attempt on its selected
// endpoint and takes its launch, so the process is written down before the
// review can begin. A primary already known to be limited is passed over first,
// without recording an attempt that never happened.
func (a *activeRun) prepareReviewAttempt(ctx context.Context, route *reviewerRoute) error {
	p := a.pipeline
	if err := a.adoptStoredRouting(); err != nil {
		return err
	}
	operation, err := a.reviewOperation(route.operation)
	if err != nil {
		return err
	}
	if sw := operation.Switch; sw != nil && sw.Progress == runstate.TransitionPlanned {
		if err := a.reconcileOperation(ctx, route.operation, "review"); err != nil {
			return err
		}
	}
	if operation.Waiting != nil {
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.ClearWaiting(route.operation)
		}); err != nil {
			return err
		}
	}
	snapshot := *a.state.Routing.Reviewer
	reserved := reservedAttempt(operation)
	if reserved == nil && operation.Selected == runstate.EndpointPrimary && operation.Switch == nil {
		if limit, known := a.knownLimited(snapshot.Primary); known {
			evidence := fmt.Sprintf("%s was already known to have reached its usage limit: %s", describeEndpoint(snapshot.Primary), limit.Says)
			if _, err := a.switchReviewEndpoint(ctx, route, runstate.SwitchPrimaryLimited, "", evidence); err != nil {
				return err
			}
		}
	}
	if operation, err = a.reviewOperation(route.operation); err != nil {
		return err
	}
	endpoint, choice := a.selectedReviewEndpoint(operation)
	last := a.lastReviewEndpoint()
	route.endpoint, route.choice = endpoint, choice
	route.reconstructed = last != nil && !sameEndpoint(*last, endpoint)
	var attemptID string
	if reserved = reservedAttempt(operation); reserved != nil {
		attemptID = reserved.ID
	} else {
		request := runstate.AttemptRequest{Mode: runstate.SessionFresh}
		if route.reconstructed {
			request.Mode = runstate.SessionReconstruction
		}
		request.Inputs = fmt.Sprintf("run %s, %s, evidence built whole by the harness", a.state.RunID, describeReviewOf(route.candidate))
		if sw := operation.Switch; sw != nil && sw.Progress == runstate.TransitionSourceReconciled {
			request.ID, request.Predecessor = sw.DestinationAttempt, sw.SourceAttempt
		} else {
			minted, err := runstate.NewRoutingID("att")
			if err != nil {
				return err
			}
			request.ID = minted
			if count := len(operation.Attempts); count > 0 {
				request.Predecessor = operation.Attempts[count-1].ID
			}
		}
		now := p.clock().Now()
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.PrepareAttempt(route.operation, request, now)
		}); err != nil {
			return fmt.Errorf("reserve the next review attempt of operation %s: %w", route.operation, err)
		}
		attemptID = request.ID
	}
	reviewer, ok := p.ReviewerFor(endpoint)
	if !ok || reviewer == nil {
		return fmt.Errorf("this harness cannot start %s to review on", describeEndpoint(endpoint))
	}
	store, _ := p.routingStore()
	launch, err := store.BeginLaunch(context.WithoutCancel(ctx), a.state.RunID, route.operation, attemptID)
	if err != nil {
		return fmt.Errorf("begin the launch of review attempt %s: %w", attemptID, err)
	}
	route.attempt, route.launch, route.reviewer = attemptID, launch, reviewer
	route.firstEvent = a.state.LastSequence + 1
	gate := launch.Gate()
	register := gate.Register
	gate.Register = func(process execution.StartedProcess) error {
		if err := register(process); err != nil {
			return err
		}
		return a.adoptStoredRouting()
	}
	route.gate = gate
	return nil
}

// classifyReviewAttempt names how a review attempt ended. Only the provider's
// own usage-limit classification permits a switch; a refusal for being logged
// out, an overload, a dropped connection, the harness's own stop, a reply the
// contract could not read, and every other failure are named for what they are.
func classifyReviewAttempt(result review.Result, err error) (string, string) {
	if result.ProviderOutage != nil && err != nil {
		return "provider_unavailable", result.ProviderOutage.Detail
	}
	if limit, limited := refusedReviewForUsageLimit(result.UsageLimit, err); limited {
		said := "the provider refused the review for its usage limit"
		if limit.Kind != "" {
			said += " (" + limit.Kind + ")"
		}
		return runstate.UsageLimitClassification, said
	}
	if overload, overloaded := refusedReviewForServerOverload(result.ServerOverload, err); overloaded {
		return "server_overload", overload.Detail
	}
	if failure, died := diedTransiently(result.TransientFailure, result.ProcessStatus, false, err); died {
		return "transient_failure", failure.Detail
	}
	if reason, stopped := providerStopReason(result.ProcessStatus); stopped && err != nil {
		return "harness_stopped", reason
	}
	if err != nil {
		return "failed", err.Error()
	}
	return "served", ""
}

// finishReviewAttempt records how the attempt ended, with whether its process
// tree is confirmed stopped, and takes its result for the candidate the
// operation judges. It reports whether the result may supply a verdict: a
// partial or interrupted review, one for another candidate, a late result for
// an attempt that ended another way, and a candidate the worktree no longer
// holds are none of them one. The usage the attempt reported is kept whatever
// becomes of the result.
func (a *activeRun) finishReviewAttempt(ctx context.Context, route *reviewerRoute, result review.Result, reviewErr error) (bool, error) {
	launch := route.launch
	route.launch = nil
	if launch == nil {
		return false, nil
	}
	ctx = context.WithoutCancel(ctx)
	classification, said := classifyReviewAttempt(result, reviewErr)
	termination, finishErr := launch.Finish(runstate.AttemptEnding{Classification: classification, Result: said, At: a.pipeline.clock().Now()})
	if adoptErr := a.adoptStoredRouting(); finishErr == nil {
		finishErr = adoptErr
	}
	if finishErr != nil {
		return false, fmt.Errorf("record how review attempt %s ended: %w", route.attempt, finishErr)
	}
	// The candidate the result is for is the one the review was handed. A
	// worktree that holds another commit now is a result for a candidate that
	// has gone.
	reportedFor := route.candidate
	if observed, err := a.pipeline.Worktrees.Observe(ctx, a.worktree); err == nil && observed.WorktreeHead != "" && observed.WorktreeHead != route.candidate {
		reportedFor = observed.WorktreeHead
	}
	attempt, ok := a.attemptRecord(route.operation, route.attempt)
	accepted := false
	if ok && attempt.Ended != nil {
		usage := ""
		if result.LastSequence >= route.firstEvent {
			usage = fmt.Sprintf("events/%s/%d-%d", a.state.RunID, route.firstEvent, result.LastSequence)
		}
		var disposition runstate.ResultDisposition
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			var changed bool
			var err error
			disposition, changed, err = routing.AcceptResult(runstate.AttemptResult{
				Operation: route.operation, Attempt: route.attempt, Candidate: reportedFor, Ending: *attempt.Ended, Usage: usage,
			})
			return changed, err
		}); err != nil {
			return false, fmt.Errorf("take the result review attempt %s reported: %w", route.attempt, err)
		}
		accepted = disposition == runstate.ResultAdopted && classification == "served" && reviewErr == nil
	}
	if termination == runstate.TerminationUncertain {
		if err := a.reconcileOperation(ctx, route.operation, "review"); err != nil {
			return false, err
		}
	}
	return accepted, nil
}

// switchReviewEndpoint makes the operation's one switch from its primary to its
// alternate, and reports whether it did. Where it cannot, the operation is left
// where it is, recorded as waiting on its selected endpoint with the reason, and
// the caller waits the limit out as a run with no pair does.
func (a *activeRun) switchReviewEndpoint(ctx context.Context, route *reviewerRoute, trigger runstate.SwitchTrigger, source, evidence string) (bool, error) {
	p := a.pipeline
	operation, err := a.reviewOperation(route.operation)
	if err != nil {
		return false, err
	}
	snapshot := *a.state.Routing.Reviewer
	now := p.clock().Now()
	selected, _ := a.selectedReviewEndpoint(operation)
	var why string
	switch {
	case snapshot.Alternate == nil || !snapshot.FallbackEnabled:
		why = "the reviewer's pair has fallback switched off"
	case operation.Selected != runstate.EndpointPrimary:
		why = "this operation has already moved to its alternate, and it does not move back"
	case operation.SwitchAllowance < 1:
		why = "this operation's one switch is spent, or its history before routing was recorded is unknown"
	default:
		if problem := a.endpointProblemFor(ctx, *snapshot.Alternate, domain.RoleReviewer); problem != "" {
			why = fmt.Sprintf("the alternate, %s, cannot serve it: %s", describeEndpoint(*snapshot.Alternate), problem)
		}
	}
	if why != "" {
		if trigger == runstate.SwitchPrimaryLimited {
			return false, nil
		}
		reason := fmt.Sprintf("%s reached its usage limit and %s", describeEndpoint(selected), why)
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.SetWaiting(route.operation, runstate.OperationWait{Reason: reason, Endpoint: operation.Selected, Since: now})
		}); err != nil {
			return false, fmt.Errorf("record why review operation %s waits: %w", route.operation, err)
		}
		return false, nil
	}
	switchID, err := runstate.NewRoutingID("sw")
	if err != nil {
		return false, err
	}
	destination, err := runstate.NewRoutingID("att")
	if err != nil {
		return false, err
	}
	request := runstate.SwitchRequest{
		ID: switchID, SourceAttempt: source, Trigger: trigger, Evidence: evidence, ConfigRevision: snapshot.ConfigRevision,
		ContextReferences:  []string{"run " + a.state.RunID, "branch " + a.state.Branch, describeReviewOf(route.candidate)},
		DestinationAttempt: destination,
	}
	if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
		return routing.PlanSwitch(route.operation, request, now)
	}); err != nil {
		return false, fmt.Errorf("switch review operation %s to its alternate: %w", route.operation, err)
	}
	if trigger == runstate.SwitchUsageLimit {
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			changed, err := routing.ReconcileSource(route.operation, switchID, now)
			if errors.Is(err, runstate.ErrRoutingConflict) {
				return false, nil
			}
			return changed, err
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// completeReviewOperation ends the operation once it has produced the verdict.
func (a *activeRun) completeReviewOperation(ctx context.Context, route *reviewerRoute, outcome string) error {
	if route == nil {
		return nil
	}
	now := a.pipeline.clock().Now()
	if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
		return routing.CompleteOperation(route.operation, outcome, now)
	}); err != nil {
		return fmt.Errorf("close review operation %s: %w", route.operation, err)
	}
	return nil
}

// reviewRefusedEndpoint is the endpoint the review that was refused asked for,
// so the pause records the refusal against the endpoint that made it rather
// than the developer's.
func (a *activeRun) reviewRefusedEndpoint() (runstate.RoutedEndpoint, bool) {
	if !a.routedReviewer() {
		return runstate.RoutedEndpoint{}, false
	}
	operation := a.openReviewOperation()
	if operation == nil {
		return runstate.RoutedEndpoint{}, false
	}
	endpoint, _ := a.selectedReviewEndpoint(*operation)
	return endpoint, true
}

// reviewReask is the second asking handed to a routed review: the reviewer's
// own session where the attempt runs on the endpoint that opened it, and never
// a session of the developer's or of any other endpoint.
func (a *activeRun) reviewReask(reask *review.Reask, route *reviewerRoute) *review.Reask {
	if reask == nil {
		return nil
	}
	session := strings.TrimSpace(reask.SessionID)
	if session != "" && session == strings.TrimSpace(a.state.ProviderSessionID) {
		session = ""
	}
	if route != nil && route.reconstructed {
		session = ""
	}
	if session == reask.SessionID {
		return reask
	}
	changed := *reask
	changed.SessionID = session
	return &changed
}

// endpointProblemFor says why an endpoint cannot serve a role now.
func (a *activeRun) endpointProblemFor(ctx context.Context, endpoint runstate.RoutedEndpoint, role domain.AgentRole) string {
	p := a.pipeline
	providers, err := p.Config.ProviderRegistry()
	if err != nil {
		return fmt.Sprintf("the configured providers could not be read: %v", err)
	}
	if err := providers.Serves(endpoint.Provider, role); err != nil {
		return err.Error()
	}
	if _, err := p.Config.Endpoint(p.StateRoot, endpoint.AccountAlias); err != nil {
		return err.Error()
	}
	adapter, ok := p.adapterFor(endpoint.Provider)
	if !ok {
		return fmt.Sprintf("this harness cannot start %s", endpoint.Provider)
	}
	availability, err := adapter.CheckAvailability(ctx)
	switch {
	case err != nil:
		return fmt.Sprintf("whether %s is installed and logged in could not be checked: %v", endpoint.Provider, err)
	case !availability.Installed:
		return availability.NotInstalled(endpoint.Provider)
	case !availability.Authenticated:
		return fmt.Sprintf("%s is not logged in", endpoint.Provider)
	}
	if limit, known := a.knownLimited(endpoint); known {
		return "it is already known to have reached its usage limit: " + limit.Says
	}
	return ""
}
