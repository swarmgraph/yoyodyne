package orchestrator

// Running a developer run's invocations on the endpoint pair its developer slot
// is pinned to (docs/designs/claude-execution-and-account-routing.md).
//
// A slot with an explicit `routing` pair is the whole of what this file acts
// on. A run reserved for such a slot records the slot and the resolved pair on
// its own record before anything is claimed, and every developer invocation the
// run makes after that — the first attempt, a repair, a reissue after a wait, a
// resumed attempt after a restart — reads its endpoint off that record rather
// than off the configuration, so a reload, an edited label rule, or another
// run taking a slot changes nothing about a run already going. A slot with no
// pair, and every project that configures none, takes none of this: its runs
// record what they always recorded and are invoked exactly as they always were.
//
// The work a routed run asks of its developer is divided into logical
// operations: the first attempt at the item is one, and each repair answering
// a recorded failure is another. An operation's identity survives a reissue, a
// wait, and a restart, because it is found again on the run's record by what it
// is for (the repair attempt it answers) rather than minted afresh. Every
// provider launch under it is a separately recorded attempt, started behind the
// launch gate of runstate's launch.go so the attempt's process is written down
// before the provider can begin work.
//
// An operation may move from its primary to its alternate once, and only for a
// usage limit: the provider classifying an attempt's refusal as one, or the
// primary already known to be limited before anything is launched, in which case
// the alternate serves the first attempt and no primary attempt is recorded.
// Nothing else switches — a login, a permission refusal, an unknown failure, a
// failing check, or a reviewer's finding is answered exactly as it is on a run
// with no pair. The alternate is checked before it is used, and one that cannot
// serve leaves the operation on its primary, waiting the limit out the way a run
// with no pair does, with the reason recorded on the operation. Once switched,
// the operation stays on the alternate through every reissue and wait; the next
// genuine operation starts from the primary again.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RoutingStore is the run store's routing writer, the launch gate's record, and
// launch recovery. It is satisfied by *runstate.Store, and asked for only by a
// run whose slot has an explicit endpoint pair.
type RoutingStore interface {
	ClaimSlot(ctx context.Context, held runstate.State, number, capacity int, at time.Time) (runstate.State, error)
	UpdateRouting(ctx context.Context, held runstate.State, change func(*runstate.RunRouting) (bool, error)) (runstate.State, error)
	BeginLaunch(ctx context.Context, runID, operationID, attemptID string) (*runstate.AttemptLaunch, error)
	ReconcileLaunch(ctx context.Context, runID, operationID string, recovery runstate.LaunchRecovery) (runstate.LaunchReconciliation, error)
}

// EndpointLimits says whether an account and model are already known to have
// reached a usage limit, before anything is launched on them.
type EndpointLimits interface {
	KnownLimited(accountAlias, model string, now time.Time) (KnownLimit, bool, error)
}

// KnownLimit is a usage limit recorded against an account and model that still
// stands: when it resets where the provider said, and what recorded it.
type KnownLimit struct {
	ResetsAt *time.Time
	Says     string
}

// RecordedEndpointLimits reads known limits from the refusals the harness has
// already written down: the usage-limit log, every run parked on a limit, and
// the served invocations that lift a refusal before its quoted reset. A
// refusal that named no reset stands for one probe interval from when it was
// met, because that is how long the harness itself waits before asking again.
type RecordedEndpointLimits struct {
	Refusals          readmodel.UsageLimits
	Runs              ScheduleRuns
	Served            readmodel.CapacityServedRecord
	UnknownResetPause time.Duration
}

// KnownLimited reports the latest standing refusal of exactly this account and
// model. A refusal that names no account is not read as one of this account's,
// because guessing which account it was is how a working endpoint gets skipped.
func (l RecordedEndpointLimits) KnownLimited(accountAlias, model string, now time.Time) (KnownLimit, bool, error) {
	var refusals []runstate.UsageLimitExhaustion
	if l.Refusals != nil {
		logged, err := l.Refusals.List()
		if err != nil {
			return KnownLimit{}, false, fmt.Errorf("read the usage-limit log: %w", err)
		}
		refusals = append(refusals, logged...)
	}
	if l.Runs != nil {
		runs, err := l.Runs.Incomplete()
		if err != nil {
			return KnownLimit{}, false, fmt.Errorf("read the runs parked on a usage limit: %w", err)
		}
		refusals = append(refusals, readmodel.ParkedRunRefusals(runs)...)
	}
	evidence := readmodel.CapacityEvidence{}
	if l.Served != nil {
		served, err := l.Served.List()
		if err != nil {
			return KnownLimit{}, false, fmt.Errorf("read the invocations the provider served: %w", err)
		}
		evidence.Served = served
	}
	found, known := KnownLimit{}, false
	var latest time.Time
	for _, refusal := range evidence.Standing(refusals) {
		if strings.TrimSpace(refusal.AccountAlias) != accountAlias || strings.TrimSpace(refusal.Model) != model {
			continue
		}
		switch {
		case refusal.ResetsAt != nil && refusal.ResetsAt.After(now):
		case refusal.ResetsAt == nil && l.UnknownResetPause > 0 && now.Sub(refusal.At) < l.UnknownResetPause:
		default:
			continue
		}
		if known && !refusal.At.After(latest) {
			continue
		}
		latest, known = refusal.At, true
		found = KnownLimit{ResetsAt: refusal.ResetsAt, Says: fmt.Sprintf("a usage limit was recorded against it at %s for %s", refusal.At.UTC().Format(time.RFC3339), refusal.Waiting)}
		if refusal.ResetsAt != nil {
			found.Says += fmt.Sprintf(", resetting at %s", refusal.ResetsAt.UTC().Format(time.RFC3339))
		} else {
			found.Says += ", naming no reset time"
		}
	}
	return found, known, nil
}

// developerSlotKey carries the developer slot the scheduler pulled a run into,
// on the context the run is started under, for the reason the landing notice
// travels that way: the scheduler hosts the run and whoever wired the start
// builds the pipeline, and the context is the one thing both hand along.
type developerSlotKey struct{}

func withDeveloperSlot(ctx context.Context, slot int) context.Context {
	return context.WithValue(ctx, developerSlotKey{}, slot)
}

// developerSlotOf is the slot a run was pulled into, and zero for a run started
// any other way — by name, or by a continuation of a run that already has one.
func developerSlotOf(ctx context.Context) int {
	slot, _ := ctx.Value(developerSlotKey{}).(int)
	return slot
}

// reconcileWait bounds how long a run waits, in this process, for an earlier
// attempt it cannot show has stopped, and reconcilePoll is how often it looks.
// Past the bound the run stops with the reason rather than launching beside it.
const (
	reconcileWait = 2 * time.Minute
	reconcilePoll = 5 * time.Second
)

// routesDeveloperSlots reports a configuration naming an endpoint pair for at
// least one developer slot. Only such a project records slots on its runs.
func (p Pipeline) routesDeveloperSlots() bool {
	for _, slot := range p.Config.Execution.DeveloperSlots {
		if slot.Routing != nil {
			return true
		}
	}
	return false
}

func (p Pipeline) routingStore() (RoutingStore, bool) {
	store, ok := p.Store.(RoutingStore)
	return store, ok
}

// developerName is the configured developer agent's name, which endpoint
// resolution reads the developer's defaults under.
func (p Pipeline) developerName() string {
	for _, name := range p.agentNames() {
		if p.Config.Agents[name].Role == domain.RoleDeveloper {
			return name
		}
	}
	return ""
}

// slotPair is the explicit endpoint pair configured for a slot, resolved now,
// and false for a slot with none.
func (p Pipeline) slotPair(slot int, labels []string) (config.ResolvedEndpointPair, bool, error) {
	if slot < 1 || slot > len(p.Config.Execution.DeveloperSlots) || p.Config.Execution.DeveloperSlots[slot-1].Routing == nil {
		return config.ResolvedEndpointPair{}, false, nil
	}
	pair, err := (config.Resolved{Config: p.Config}).ResolveDeveloperEndpoints(slot, p.developerName(), labels)
	if err != nil {
		return config.ResolvedEndpointPair{}, false, fmt.Errorf("resolve developer slot %d's endpoints: %w", slot, err)
	}
	return pair, pair.Explicit, nil
}

// dispatchBackend is the adapter and backend a dispatch into the slot the
// scheduler chose will first invoke: the slot's primary where it has a pair,
// and the configured developer's otherwise. It is what is asked whether it is
// installed and logged in before anything is claimed.
func (p Pipeline) dispatchBackend(ctx context.Context) (backend.Backend, domain.Backend, error) {
	pair, explicit, err := p.slotPair(developerSlotOf(ctx), nil)
	if err != nil {
		return nil, "", err
	}
	if !explicit {
		return p.Backend, p.developer().Backend, nil
	}
	named := pair.Primary.Endpoint.Provider
	adapter, ok := p.adapterFor(named)
	if !ok {
		return nil, named, fmt.Errorf("developer slot %d's primary endpoint runs on %s, which this harness cannot start", pair.Slot, named)
	}
	return adapter, named, nil
}

// adapterFor is the adapter a developer invocation on a backend is made
// through: the pipeline's own for the configured developer's backend, and the
// one built for any other.
func (p Pipeline) adapterFor(named domain.Backend) (backend.Backend, bool) {
	if named == p.developer().Backend && p.Backend != nil {
		return p.Backend, true
	}
	if p.RecordedBackends != nil {
		if adapter, ok := p.RecordedBackends(named); ok && adapter != nil {
			return adapter, true
		}
	}
	return nil, false
}

// requestedModel is the selector an invocation on an endpoint asks for: its
// pinned version where it has one, as endpoint resolution reads it.
func requestedModel(endpoint runstate.RoutedEndpoint) string {
	if version := strings.TrimSpace(endpoint.ModelVersion); version != "" {
		return version
	}
	return endpoint.Model
}

// describeEndpoint names an endpoint as a person reads it.
func describeEndpoint(endpoint runstate.RoutedEndpoint) string {
	return fmt.Sprintf("%s model %s on account %s", endpoint.Provider, requestedModel(endpoint), endpoint.AccountAlias)
}

// sameEndpoint reports two endpoints a native session could pass between: the
// same provider, account and model. Anything else starts a new session.
func sameEndpoint(a, b runstate.RoutedEndpoint) bool {
	return a.Provider == b.Provider && a.AccountAlias == b.AccountAlias && requestedModel(a) == requestedModel(b)
}

// claimDeveloperSlot records the slot a fresh run occupies: the one the
// scheduler pulled it into where that is still free, and otherwise the lowest
// free one. A slot another run in flight records is never taken from it.
func claimDeveloperSlot(ctx context.Context, store RoutingStore, held runstate.State, requested, capacity int, at time.Time) (runstate.State, int, error) {
	order := make([]int, 0, capacity)
	if requested >= 1 && requested <= capacity {
		order = append(order, requested)
	}
	for number := 1; number <= capacity; number++ {
		if number != requested {
			order = append(order, number)
		}
	}
	var occupied []string
	for _, number := range order {
		claimed, err := store.ClaimSlot(ctx, held, number, capacity, at)
		var taken runstate.SlotOccupiedError
		if errors.As(err, &taken) {
			occupied = append(occupied, taken.Error())
			continue
		}
		if err != nil {
			return held, 0, err
		}
		return claimed, number, nil
	}
	return held, 0, fmt.Errorf("no developer slot from 1 to %d is free to record this run in: %s", capacity, strings.Join(occupied, "; "))
}

// pinDeveloperRouting records the run's developer slot and, where that slot
// has an explicit pair, the pair itself, before the run claims anything. The
// run's backend, account, model and effort are then the primary's, and every
// later invocation reads them back off the record.
func (a *activeRun) pinDeveloperRouting(ctx context.Context) error {
	p := a.pipeline
	if !p.routesDeveloperSlots() || a.state.Document != nil {
		return nil
	}
	store, ok := p.routingStore()
	if !ok {
		return errors.New("developer slots are configured with endpoint pairs, and this run's state store cannot record a run's slot and pair, so the run was not started on a pair it could not keep to")
	}
	now := p.clock().Now()
	claimed, slot, err := claimDeveloperSlot(ctx, store, a.state, developerSlotOf(ctx), p.Config.Execution.MaxConcurrentDevelopers, now)
	if err != nil {
		return fmt.Errorf("record the developer slot this run occupies: %w", err)
	}
	a.state.Routing = claimed.Routing
	pair, explicit, err := p.slotPair(slot, a.state.WorkItemLabels)
	if err != nil || !explicit {
		return err
	}
	snapshot := runstate.RoutingSnapshotOf(pair, runstate.RoutingClaimed, now)
	if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
		return routing.RecordSnapshot(snapshot)
	}); err != nil {
		return fmt.Errorf("pin developer slot %d's endpoints to this run: %w", slot, err)
	}
	a.applyDeveloperEndpoint(runstate.EndpointPrimary, snapshot.Primary)
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the endpoint developer slot %d starts this run on: %w", slot, err)
	}
	return nil
}

// routedDeveloper reports a run pinned to an explicit endpoint pair.
func (a *activeRun) routedDeveloper() bool {
	return a.state.Routing != nil && a.state.Routing.Developer != nil
}

// updateRouting applies one routing change to the run's record and takes the
// routing it wrote, so the run's next whole-record save is not refused as
// stale. Only the routing is taken: everything else this process holds is
// ahead of what is stored.
func (a *activeRun) updateRouting(ctx context.Context, change func(*runstate.RunRouting) (bool, error)) error {
	store, ok := a.pipeline.routingStore()
	if !ok {
		return errors.New("this run's state store cannot record routing")
	}
	written, err := store.UpdateRouting(ctx, a.state, change)
	if err != nil {
		return err
	}
	a.state.Routing = written.Routing
	return nil
}

// adoptStoredRouting takes the routing the store holds now, after something
// that writes it on its own — a launch registering its process, recovery
// recording what it found — has written it.
func (a *activeRun) adoptStoredRouting() error {
	stored, err := a.pipeline.Store.Load(a.state.RunID)
	if err != nil {
		return fmt.Errorf("read back this run's routing: %w", err)
	}
	a.state.Routing = stored.Routing
	return nil
}

// applyDeveloperEndpoint makes an endpoint the one this run's developer
// invocations are made on, in the fields every invocation already reads.
func (a *activeRun) applyDeveloperEndpoint(choice runstate.EndpointChoice, endpoint runstate.RoutedEndpoint) {
	a.state.Backend = endpoint.Provider
	a.state.AccountAlias = endpoint.AccountAlias
	a.state.DeveloperModel = requestedModel(endpoint)
	a.state.DeveloperModelReason = fmt.Sprintf("the %s endpoint of developer slot %d", choice, a.state.RecordedSlot())
	a.state.ProviderEffort = endpoint.Effort
	a.state.EffortSettled = true
}

// selectedDeveloperEndpoint is the endpoint the run's open developer operation
// has selected, read off its pinned pair.
func (a *activeRun) selectedDeveloperEndpoint() runstate.RoutedEndpoint {
	snapshot := a.state.Routing.Developer
	if operation := a.openDeveloperOperation(); operation != nil && operation.Selected == runstate.EndpointAlternate && snapshot.Alternate != nil {
		return *snapshot.Alternate
	}
	return snapshot.Primary
}

// developerRoute is one logical operation a developer invocation is serving,
// and the attempt being made under it now.
type developerRoute struct {
	operation string
	// attempt is the latest attempt prepared under the operation, and launch
	// its launch between prepare and finish.
	attempt string
	launch  *runstate.AttemptLaunch
	// firstEvent is the first event sequence the attempt can have written.
	firstEvent uint64
	// relaunch marks the next attempt as a relaunch after something outside the
	// work stopped the last one.
	relaunch bool
}

// developerRound is the logical operation the run's developer is asked for
// now: the first attempt at the item, or the repair answering the recorded
// failure the repair counter names. Being found again by what it is for is
// what keeps a reissue, a wait, or a restart inside the same operation.
func (a *activeRun) developerRound() runstate.OperationRequest {
	repairs := a.state.RepairAttempts
	request := runstate.OperationRequest{Kind: runstate.OperationDevelop, Budget: runstate.OperationBudget{RepairAttempts: &repairs}}
	if repairs > 0 {
		request.Kind = runstate.OperationRepair
		request.Findings = fmt.Sprintf("repair-attempt-%d", repairs)
	}
	return request
}

func (a *activeRun) openDeveloperOperation() *runstate.RoutedOperation {
	if a.state.Routing == nil {
		return nil
	}
	for index := range a.state.Routing.Operations {
		operation := &a.state.Routing.Operations[index]
		if operation.Role == domain.RoleDeveloper && operation.Completed == nil {
			return operation
		}
	}
	return nil
}

func (a *activeRun) developerOperation(id string) (runstate.RoutedOperation, error) {
	operation, ok := a.state.Routing.Operation(id)
	if !ok {
		return runstate.RoutedOperation{}, fmt.Errorf("developer operation %s is not on this run's record", id)
	}
	return *operation, nil
}

// beginDeveloperOperation finds or opens the operation this developer
// invocation serves, settles anything an earlier process left running under
// it, and puts the operation's selected endpoint on the run. It answers nil
// for a run with no pair.
func (a *activeRun) beginDeveloperOperation(ctx context.Context) (*developerRoute, error) {
	if !a.routedDeveloper() {
		return nil, nil
	}
	if err := a.adoptStoredRouting(); err != nil {
		return nil, err
	}
	now := a.pipeline.clock().Now()
	want := a.developerRound()
	open := a.openDeveloperOperation()
	if open != nil && (open.Kind != want.Kind || open.Findings != want.Findings) {
		// A genuinely new operation: the one before it is over, once nothing
		// launched under it can still be running.
		superseded := open.ID
		if err := a.reconcileDeveloperOperation(ctx, superseded); err != nil {
			return nil, err
		}
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.CompleteOperation(superseded, fmt.Sprintf("superseded by the %s for %s", want.Kind, describeRound(want)), now)
		}); err != nil {
			return nil, fmt.Errorf("close developer operation %s: %w", superseded, err)
		}
		open = nil
	}
	var id string
	if open != nil {
		id = open.ID
	} else {
		minted, err := runstate.NewRoutingID("op")
		if err != nil {
			return nil, err
		}
		want.ID = minted
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.OpenOperation(want, now)
		}); err != nil {
			return nil, fmt.Errorf("open the developer operation for %s: %w", describeRound(want), err)
		}
		id = minted
	}
	if err := a.reconcileDeveloperOperation(ctx, id); err != nil {
		return nil, err
	}
	if err := a.selectOperationEndpoint(id); err != nil {
		return nil, err
	}
	return &developerRoute{operation: id}, nil
}

func describeRound(request runstate.OperationRequest) string {
	if request.Kind == runstate.OperationRepair && request.Budget.RepairAttempts != nil {
		return fmt.Sprintf("repair attempt %d", *request.Budget.RepairAttempts)
	}
	return "the first attempt at the work item"
}

// selectOperationEndpoint puts the operation's selected endpoint on the run,
// which is where a switch recorded before a process died is picked up from.
func (a *activeRun) selectOperationEndpoint(id string) error {
	operation, err := a.developerOperation(id)
	if err != nil {
		return err
	}
	endpoint := a.state.Routing.Developer.Primary
	if operation.Selected == runstate.EndpointAlternate && a.state.Routing.Developer.Alternate != nil {
		endpoint = *a.state.Routing.Developer.Alternate
	}
	before := a.state
	a.applyDeveloperEndpoint(operation.Selected, endpoint)
	if before.Backend == a.state.Backend && before.AccountAlias == a.state.AccountAlias && before.DeveloperModel == a.state.DeveloperModel &&
		before.ProviderEffort == a.state.ProviderEffort && before.DeveloperModelReason == a.state.DeveloperModelReason {
		return nil
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the endpoint developer operation %s is on: %w", id, err)
	}
	return nil
}

// reconcileDeveloperOperation settles what an earlier launch under the
// operation left: a reserved attempt that never began is launched again under
// its own identity, one that stopped without a recorded result is ended as
// interrupted, and one that may still be running is waited for. Nothing is
// launched beside an attempt that may still be running; past the wait's bound
// the run stops with the reason, preserved, rather than guessing.
func (a *activeRun) reconcileDeveloperOperation(ctx context.Context, id string) error {
	store, ok := a.pipeline.routingStore()
	if !ok {
		return errors.New("this run's state store cannot reconcile a launch")
	}
	var waited time.Duration
	for {
		found, err := store.ReconcileLaunch(ctx, a.state.RunID, id, runstate.LaunchRecovery{})
		if adoptErr := a.adoptStoredRouting(); adoptErr != nil && err == nil {
			err = adoptErr
		}
		if err != nil {
			return fmt.Errorf("reconcile developer operation %s's last launch: %w", id, err)
		}
		if found.Verdict != runstate.LaunchRunning && found.Verdict != runstate.LaunchUncertain {
			return nil
		}
		if waited >= reconcileWait {
			return stoppedBy(runstate.StopHarness, phaseError{status: runstate.StatusFailed, cause: fmt.Errorf(
				"developer attempt %s of this run may still be running, so no other attempt is started beside it: %s; the run is preserved, and a continuation reconciles it again",
				found.Attempt, found.Reason)})
		}
		if err := a.pipeline.sleep(ctx, reconcilePoll); err != nil {
			return err
		}
		waited += reconcilePoll
	}
}

// prepareDeveloperAttempt reserves the next attempt of the operation and takes
// its launch, so the provider it starts is written down before it can begin.
// It answers the prompt and session the attempt is made with: an attempt on
// another endpoint than the run's last one starts a new session, rebuilt from
// what the run has recorded, and is never handed the old one.
func (a *activeRun) prepareDeveloperAttempt(ctx context.Context, route *developerRoute, prompt, sessionID string) (string, string, error) {
	p := a.pipeline
	if err := a.adoptStoredRouting(); err != nil {
		return prompt, sessionID, err
	}
	operation, err := a.developerOperation(route.operation)
	if err != nil {
		return prompt, sessionID, err
	}
	// A switch planned on a source attempt nobody has yet confirmed stopped
	// waits for that before its destination can be prepared.
	if sw := operation.Switch; sw != nil && sw.Progress == runstate.TransitionPlanned {
		if err := a.reconcileDeveloperOperation(ctx, route.operation); err != nil {
			return prompt, sessionID, err
		}
	}
	if operation.Waiting != nil {
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.ClearWaiting(route.operation)
		}); err != nil {
			return prompt, sessionID, err
		}
	}
	snapshot := *a.state.Routing.Developer
	reserved := reservedAttempt(operation)
	if reserved == nil && operation.Selected == runstate.EndpointPrimary && operation.Switch == nil {
		// The primary already known to be limited is skipped before anything is
		// launched on it, and no attempt is recorded for it.
		if limit, known := a.knownLimited(snapshot.Primary); known {
			evidence := fmt.Sprintf("%s was already known to have reached its usage limit: %s", describeEndpoint(snapshot.Primary), limit.Says)
			if _, err := a.switchDeveloperEndpoint(ctx, route, runstate.SwitchPrimaryLimited, "", evidence); err != nil {
				return prompt, sessionID, err
			}
		}
	}
	operation, err = a.developerOperation(route.operation)
	if err != nil {
		return prompt, sessionID, err
	}
	var attemptID string
	if reserved = reservedAttempt(operation); reserved != nil {
		// A reserved attempt that never began is launched again under its own
		// identity, with the session it was reserved with.
		attemptID = reserved.ID
		sessionID = ""
		if reserved.Mode == runstate.SessionNativeResume && reserved.Session != nil {
			sessionID = reserved.Session.SessionID
		} else if reserved.Mode == runstate.SessionReconstruction {
			prompt = a.reconstructedPrompt(prompt, a.lastLaunchedEndpoint(), reserved.Endpoint)
		}
	} else {
		endpoint := snapshot.Primary
		if operation.Selected == runstate.EndpointAlternate && snapshot.Alternate != nil {
			endpoint = *snapshot.Alternate
		}
		request := runstate.AttemptRequest{Mode: runstate.SessionFresh, Transient: route.relaunch}
		last := a.lastLaunchedEndpoint()
		switch {
		case last != nil && sameEndpoint(*last, endpoint) && sessionID != "":
			request.Mode = runstate.SessionNativeResume
			request.Session = &runstate.SessionEvidence{
				SessionID: sessionID, Provider: endpoint.Provider, AccountAlias: endpoint.AccountAlias, Model: endpoint.Model, Compatible: true,
				Reason: "the run's last attempt opened it on this same provider, account and model",
			}
		case last != nil && !sameEndpoint(*last, endpoint), last == nil && sessionID != "":
			// The session belongs to another endpoint, or to no attempt this run
			// recorded, so it is not offered to this one.
			request.Mode = runstate.SessionReconstruction
			sessionID = ""
			a.state.ProviderSessionID = ""
			a.outcome.ProviderSessionID = ""
			prompt = a.reconstructedPrompt(prompt, last, endpoint)
		}
		request.Inputs = fmt.Sprintf("run %s branch %s from %s, %s", a.state.RunID, a.state.Branch, a.state.BaseCommit, describeRound(a.developerRound()))
		if sw := operation.Switch; sw != nil && sw.Progress == runstate.TransitionSourceReconciled {
			request.ID, request.Predecessor = sw.DestinationAttempt, sw.SourceAttempt
		} else {
			minted, err := runstate.NewRoutingID("att")
			if err != nil {
				return prompt, sessionID, err
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
			return prompt, sessionID, fmt.Errorf("reserve the next developer attempt of operation %s: %w", route.operation, err)
		}
		attemptID = request.ID
	}
	store, _ := p.routingStore()
	// The launch writes the attempt's process down and later how it ended, and
	// both have to be written even when the run's context is cancelled under
	// it — a redeploy drain cancels a run mid-attempt and still commits what
	// the attempt left.
	launch, err := store.BeginLaunch(context.WithoutCancel(ctx), a.state.RunID, route.operation, attemptID)
	if err != nil {
		return prompt, sessionID, fmt.Errorf("begin the launch of developer attempt %s: %w", attemptID, err)
	}
	route.attempt, route.launch, route.relaunch = attemptID, launch, false
	route.firstEvent = a.state.LastSequence + 1
	gate := launch.Gate()
	register := gate.Register
	gate.Register = func(process execution.StartedProcess) error {
		if err := register(process); err != nil {
			return err
		}
		// The launch wrote the run's routing itself; every save after this one
		// has to carry what it wrote.
		return a.adoptStoredRouting()
	}
	a.launchGate = gate
	return prompt, sessionID, nil
}

// reservedAttempt is an attempt of the operation reserved and never begun.
func reservedAttempt(operation runstate.RoutedOperation) *runstate.InvocationAttempt {
	for index := range operation.Attempts {
		attempt := operation.Attempts[index]
		if attempt.State == runstate.AttemptPrepared && attempt.Execution == nil {
			return &attempt
		}
	}
	return nil
}

// lastLaunchedEndpoint is the endpoint the run's latest developer attempt that
// began work ran on, whatever operation it served, and nil before any did. The
// developer session the run carries belongs to it, because every change of
// endpoint clears the session.
func (a *activeRun) lastLaunchedEndpoint() *runstate.RoutedEndpoint {
	var latest *runstate.InvocationAttempt
	for _, operation := range a.state.Routing.Operations {
		if operation.Role != domain.RoleDeveloper {
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

// finishDeveloperAttempt records how the attempt ended, with whether its
// process tree is confirmed stopped, and the usage it reported. One whose stop
// cannot be confirmed is waited on before anything else is launched.
func (a *activeRun) finishDeveloperAttempt(ctx context.Context, route *developerRoute, result backend.RunResult, err error) error {
	a.launchGate = nil
	launch := route.launch
	route.launch = nil
	if launch == nil {
		return nil
	}
	// Written whether or not the run's context was cancelled: see the launch.
	ctx = context.WithoutCancel(ctx)
	classification, said := classifyDeveloperAttempt(result, err)
	termination, finishErr := launch.Finish(runstate.AttemptEnding{Classification: classification, Result: said, At: a.pipeline.clock().Now()})
	if adoptErr := a.adoptStoredRouting(); finishErr == nil {
		finishErr = adoptErr
	}
	if finishErr != nil {
		return fmt.Errorf("record how developer attempt %s ended: %w", route.attempt, finishErr)
	}
	if result.LastEvent >= route.firstEvent {
		if attempt, ok := a.attemptRecord(route.operation, route.attempt); ok && attempt.Ended != nil {
			usage := fmt.Sprintf("events/%s/%d-%d", a.state.RunID, route.firstEvent, result.LastEvent)
			if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
				_, changed, err := routing.AcceptResult(runstate.AttemptResult{Operation: route.operation, Attempt: route.attempt, Ending: *attempt.Ended, Usage: usage})
				return changed, err
			}); err != nil {
				return fmt.Errorf("record the usage developer attempt %s reported: %w", route.attempt, err)
			}
		}
	}
	if termination == runstate.TerminationUncertain {
		return a.reconcileDeveloperOperation(ctx, route.operation)
	}
	return nil
}

func (a *activeRun) attemptRecord(operationID, attemptID string) (runstate.InvocationAttempt, bool) {
	operation, ok := a.state.Routing.Operation(operationID)
	if !ok {
		return runstate.InvocationAttempt{}, false
	}
	for _, attempt := range operation.Attempts {
		if attempt.ID == attemptID {
			return attempt, true
		}
	}
	return runstate.InvocationAttempt{}, false
}

// classifyDeveloperAttempt names how an attempt ended, in the order the
// developer loop answers the same evidence. Only the provider's own usage-limit
// classification is runstate.UsageLimitClassification: a login or reachability
// refusal, an overload, a dropped connection, the harness's own stop, and every
// other failure are named for what they are and permit no switch.
func classifyDeveloperAttempt(result backend.RunResult, err error) (string, string) {
	if outage, away := providerAway(result, err); away {
		return "provider_unavailable", outage.Detail
	}
	if limit, limited := refusedForUsageLimit(result, err); limited {
		said := "the provider refused the attempt for its usage limit"
		if limit.Kind != "" {
			said += " (" + limit.Kind + ")"
		}
		return runstate.UsageLimitClassification, said
	}
	if overload, overloaded := refusedForServerOverload(result, err); overloaded {
		return "server_overload", overload.Detail
	}
	if failure, died := diedTransiently(result.TransientFailure, result.Process.Status, result.IsError, err); died {
		return "transient_failure", failure.Detail
	}
	if reason, stopped := providerStopReason(result.Process.Status); stopped {
		return "harness_stopped", reason
	}
	switch {
	case err != nil:
		return "failed", err.Error()
	case result.IsError:
		return "provider_error", result.DescribeFailure()
	}
	return "served", ""
}

// knownLimited asks whether an endpoint is already known to have reached its
// usage limit. A record nothing can read establishes nothing, so the endpoint
// is tried rather than skipped.
func (a *activeRun) knownLimited(endpoint runstate.RoutedEndpoint) (KnownLimit, bool) {
	if a.pipeline.EndpointLimits == nil {
		return KnownLimit{}, false
	}
	limit, known, err := a.pipeline.EndpointLimits.KnownLimited(endpoint.AccountAlias, requestedModel(endpoint), a.pipeline.clock().Now())
	if err != nil {
		return KnownLimit{}, false
	}
	return limit, known
}

// endpointProblem says why an endpoint cannot serve a developer invocation
// now, and nothing where it can: a provider no longer allowed to serve the
// developer, an account the configuration no longer declares, an adapter this
// harness cannot start, a provider not installed or not logged in, or a usage
// limit already known to stand. It is asked of an alternate before a switch
// commits to it, so a configured alternate is never taken as a promise.
func (a *activeRun) endpointProblem(ctx context.Context, endpoint runstate.RoutedEndpoint) string {
	p := a.pipeline
	providers, err := p.Config.ProviderRegistry()
	if err != nil {
		return fmt.Sprintf("the configured providers could not be read: %v", err)
	}
	if err := providers.Serves(endpoint.Provider, domain.RoleDeveloper); err != nil {
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

// switchDeveloperEndpoint makes the operation's one switch from its primary to
// its alternate, and reports whether it did. Where it cannot — fallback is off,
// the operation's switch is spent or it is already on its alternate, or the
// alternate cannot serve — the operation is left where it is, recorded as
// waiting on its selected endpoint with the reason, and the caller waits the
// limit out as a run with no pair does.
func (a *activeRun) switchDeveloperEndpoint(ctx context.Context, route *developerRoute, trigger runstate.SwitchTrigger, source, evidence string) (bool, error) {
	p := a.pipeline
	operation, err := a.developerOperation(route.operation)
	if err != nil {
		return false, err
	}
	snapshot := *a.state.Routing.Developer
	now := p.clock().Now()
	selected := snapshot.Primary
	if operation.Selected == runstate.EndpointAlternate && snapshot.Alternate != nil {
		selected = *snapshot.Alternate
	}
	var why string
	switch {
	case snapshot.Alternate == nil || !snapshot.FallbackEnabled:
		why = fmt.Sprintf("developer slot %d's pair has fallback switched off", snapshot.Slot)
	case operation.Selected != runstate.EndpointPrimary:
		why = "this operation has already moved to its alternate, and it does not move back"
	case operation.SwitchAllowance < 1:
		why = "this operation's one switch is spent, or its history before routing was recorded is unknown"
	default:
		if problem := a.endpointProblem(ctx, *snapshot.Alternate); problem != "" {
			why = fmt.Sprintf("the alternate, %s, cannot serve it: %s", describeEndpoint(*snapshot.Alternate), problem)
		}
	}
	if why != "" {
		if trigger == runstate.SwitchPrimaryLimited {
			// Nothing has refused this operation yet: the primary is tried, and
			// a refusal is waited out where it is met.
			return false, nil
		}
		reason := fmt.Sprintf("%s reached its usage limit and %s", describeEndpoint(selected), why)
		if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
			return routing.SetWaiting(route.operation, runstate.OperationWait{Reason: reason, Endpoint: operation.Selected, Since: now})
		}); err != nil {
			return false, fmt.Errorf("record why developer operation %s waits: %w", route.operation, err)
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
		ContextReferences:  []string{"run " + a.state.RunID, "branch " + a.state.Branch, describeRound(a.developerRound())},
		DestinationAttempt: destination,
	}
	if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
		return routing.PlanSwitch(route.operation, request, now)
	}); err != nil {
		return false, fmt.Errorf("switch developer operation %s to its alternate: %w", route.operation, err)
	}
	if trigger == runstate.SwitchUsageLimit {
		// The source attempt's stop was confirmed as it was recorded, or the
		// switch waits for recovery to confirm it before its destination.
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
	a.applyDeveloperEndpoint(runstate.EndpointAlternate, *snapshot.Alternate)
	// The session belongs to the endpoint that opened it; the alternate's
	// first attempt starts its own.
	a.state.ProviderSessionID = ""
	a.outcome.ProviderSessionID = ""
	a.state.UpdatedAt = now
	if err := p.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("save the switch of developer operation %s to its alternate: %w", route.operation, err)
	}
	return true, nil
}

// completeDeveloperOperation ends the operation once its developer has
// answered it.
func (a *activeRun) completeDeveloperOperation(ctx context.Context, route *developerRoute, outcome string) error {
	if route == nil {
		return nil
	}
	now := a.pipeline.clock().Now()
	if err := a.updateRouting(ctx, func(routing *runstate.RunRouting) (bool, error) {
		return routing.CompleteOperation(route.operation, outcome, now)
	}); err != nil {
		return fmt.Errorf("close developer operation %s: %w", route.operation, err)
	}
	return nil
}

// reconstructedPromptHeading opens what an attempt on a new endpoint is told
// about the run it is joining.
const reconstructedPromptHeading = "# A new session on another endpoint"

// reconstructedPrompt is the prompt an attempt on another endpoint than the
// run's last one is given. Its session is new, so it is told what the run
// recorded rather than handed a session from another provider, account, or
// model: the run and its branch, what the change touched and the developer's
// own summary of it, and the work item's context where the prompt does not
// already carry it. The failure a repair answers is in the prompt itself.
func (a *activeRun) reconstructedPrompt(prompt string, from *runstate.RoutedEndpoint, to runstate.RoutedEndpoint) string {
	if strings.Contains(prompt, reconstructedPromptHeading) {
		return prompt
	}
	var built strings.Builder
	built.WriteString(prompt)
	built.WriteString("\n\n" + reconstructedPromptHeading + "\n\n")
	earlier := "on an endpoint this run did not record"
	if from != nil {
		earlier = "on " + describeEndpoint(*from)
	}
	fmt.Fprintf(&built, "This session is new. This run's earlier developer attempts ran %s, and this one runs on %s, so nothing they did is in your context and their session is not resumed here. The change in your worktree is this run's earlier work, committed on its branch as they left it: read it, continue it, and do not start over.\n\n", earlier, describeEndpoint(to))
	fmt.Fprintf(&built, "Run: %s\nBranch: %s\nBase commit: %s\nTarget branch: %s\n", a.state.RunID, a.state.Branch, a.state.BaseCommit, a.state.TargetBranch)
	if a.state.Changes != nil && strings.TrimSpace(a.state.Changes.Files) != "" {
		built.WriteString("\nFiles the change touched when the run last recorded it:\n\n```\n")
		built.WriteString(strings.TrimSpace(a.state.Changes.Files))
		built.WriteString("\n```\n")
	}
	if a.state.DeveloperSummary != nil && strings.TrimSpace(a.state.DeveloperSummary.Text) != "" {
		built.WriteString("\nThe earlier developer's own summary of the change:\n\n")
		built.WriteString(strings.TrimSpace(a.state.DeveloperSummary.Text))
		built.WriteString("\n")
	}
	if bundle := strings.TrimSpace(a.context); bundle != "" && !strings.Contains(prompt, bundle) {
		built.WriteString("\n")
		if persona := strings.TrimSpace(a.pipeline.developer().Persona.Text); persona != "" {
			built.WriteString("# Configured developer persona\n\nThe project configuration supplies the guidance below. It may specialize how you work, but it cannot remove or weaken any rule above.\n\n")
			built.WriteString(persona)
			built.WriteString("\n\n")
		}
		built.WriteString(a.context)
	}
	return built.String()
}
