package orchestrator

// The declarative path: the delivery definition in force stepped beside a run
// that is really happening. It is what a new run does unless its project rolled
// back, which is `execution.declarative_delivery: false` and nothing else.
//
// Which definition that is, is the project's to decide. A project that keeps its
// own copy under its configuration directory — `workflows/delivery.yaml` beside
// the personas — runs that one, and one that keeps none runs the definition this
// build ships. Nothing is merged between the two, and neither is silently
// substituted for the other: a project file that does not compile stops the run
// that would have executed it before it claims anything.
//
// `workflow.go` compiles the definitions and `parity_test.go` walks them against
// the recorded baseline. Both of those are transcripts of runs that already
// finished. This is the same walk over a run that has not: each new run records
// a workflow instance, and every boundary the run crosses is put to the
// definition — the state the run just performed and the outcome it produced — so
// the definition resolves the transition and the instance records where it went.
//
// What it does not do is perform anything, and that is the difference between
// this milestone and the one that follows it. The pipeline claims the item,
// invokes the developer, runs the checks, buys the verdict and takes the
// promotion lease exactly as it always has; the doors this graph holds are the
// registered ones with `Perform` replaced by a function that does nothing, so
// stepping the instance costs a file write and can do nothing else. The design
// settles that division: the run record owns the delivery facts and the instance
// owns the topology position. Until the step-attempt record, the idempotency key
// and the reconciliation the design calls for exist, an executor that performed
// `candidate.integrate` would be an executor that could perform it twice after a
// death, and nothing here is worth that.
//
// So what the default produces is a record rather than behaviour: the sequence
// the definition chose, standing beside the durable record of the sequence the
// run took, for every item the harness delivers. Where the two disagree the run
// is untouched and the disagreement is recorded on it — which is what made the
// soak countable and is now what makes every run's topology position readable.
//
// One class of disagreement is already known and is deliberately not smoothed
// over. A run interrupted while its reviewer was being asked resumes at the
// check rather than at the review — `resumeRun` re-earns the whole gate, which
// is what keeps an approval from outliving the change it was given for — and no
// definition has a transition from `review` back to `check`, because no recorded
// trace holds that path. Such a run records a divergence naming both places. The
// observation says so rather than inventing a transition, because an observation
// that quietly agreed with itself would be worth nothing.

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/workflow"
)

// The states of the built-in delivery definitions, by the names the files
// declare them under.
//
// They are constants because two things count on them: the observation below,
// which names the state a run has just performed, and the parity harness, which
// counts them. A state renamed in a file and not here is a definition that
// compiles and an observation that immediately disagrees with it.
const (
	deliveryClaim     = "claim"
	deliveryDevelop   = "develop"
	deliveryCheck     = "check"
	deliveryReview    = "review"
	deliveryIntegrate = "integrate"
	deliveryComplete  = "complete"
	deliveryCleanUp   = "clean-up"
)

// Whether the shared repair budget is already spent, said at the call sites that
// decide it. The two outcomes a repair input has are the same failure with the
// budget left and with it gone, and a bare boolean at those call sites reads as
// neither.
const (
	stillRepairable = false
	budgetSpent     = true
)

// deliveryTrial is one run's instance of the definition that claims to express
// what it is doing, and the executor that steps it.
//
// It holds the outcome the run last reported because the executor reads what a
// state produced through a function rather than being handed it: a registered
// action returns an error and not yet an outcome, so the pipeline is what knows
// which of a state's outcomes it just produced, and this is where it says so.
type deliveryTrial struct {
	// instance is the durable record's identifier, which is also the run's.
	instance string
	executor workflow.Executor[*activeRun]
	// reported is the outcome the run said the state it just performed produced.
	// It is written immediately before the step that reads it.
	reported string
	// stopped is why this trial is no longer observing, and is empty while it
	// still is. Once it is set nothing further is stepped: an instance that has
	// stopped agreeing with the run is left standing at the last boundary the two
	// agreed on, rather than being dragged forward into a position neither of
	// them was in.
	stopped string
}

// deliveryInstanceID is what a run's instance is recorded under. It is derived
// from the run rather than minted, so the instance a run is observed through is
// findable from the run and cannot be a second instance nobody expected. The
// store keeps the two files apart by suffix, and the run-identifier pattern the
// store scans for does not match this, so an instance is never read back as a
// run.
func deliveryInstanceID(runID string) string {
	return runID + "-delivery"
}

// beginDeliveryTrial creates the durable instance a new run is observed through,
// unless the project rolled back to the legacy path or this process has nowhere
// to record one.
//
// Everything it can refuse leaves the run exactly as it was, with no instance:
// this is an observation of delivery and is never a reason delivery does not
// happen. The refusals are not interchangeable, though they were recorded as
// though they were. A project that rolled back asked for a run with no instance
// and there is nothing to say about it; a run that was to be observed and could
// not be is a run nothing is watching, and both left the same absent field
// behind. Anything counting the runs the definition agreed with reads that
// absence as the first case, so it counts the second as clean — the run it is
// least entitled to pass. The second says so on the run instead, with the cause.
//
// It is recorded rather than returned because the run is not what is broken. The
// one exception is a definition the project wrote itself, which is returned and
// stops the run: a project that keeps its own file asked for that sequence, a
// defect in it is somebody's to fix, and this is the last moment where saying so
// is free — before the item is claimed, before a worktree exists, and before a
// provider has been paid for anything.
func (a *activeRun) beginDeliveryTrial() error {
	p := a.pipeline
	if !p.Config.Execution.DeclarativeDelivery || p.Instances == nil {
		return nil
	}
	trial, err := p.deliveryTrialOver(a.state.RunID)
	if err != nil {
		var refused projectDefinitionRefusal
		if errors.As(err, &refused) {
			return err
		}
		a.noteUnobserved(fmt.Errorf("the delivery definition this run would be observed against could not be built: %w", err))
		return nil
	}
	if _, err := trial.executor.Start(trial.instance); err != nil {
		a.noteUnobserved(fmt.Errorf("the instance this run would be observed through could not be created: %w", err))
		return nil
	}
	a.trial = trial
	// The run's own record names the instance from here on, and it is what a
	// later process reads to decide whether this run is observed at all. It is
	// carried by the run's next save rather than saved here: every path out of a
	// run saves its record, and a write of its own at this boundary would be the
	// trial touching a run it is only watching.
	//
	// The instance is created before the record names it, rather than after,
	// because the two orders fail differently. This way a process that dies in
	// between leaves an instance standing on its first state that nothing points
	// at, which nothing reads and nothing acts on; the other way it would leave a
	// run naming an instance that does not exist, which every later process would
	// pick up and record a divergence over.
	a.state.WorkflowInstanceID = trial.instance
	return nil
}

// resumeDeliveryTrial picks the observation back up on a run this process did
// not start, from the instance the run's own record names.
//
// The record decides and the configuration is never consulted, which is the
// whole of "an in-flight run is never migrated": a run started on the legacy
// path names no instance and is served here exactly as it was served before, and
// a run started on the definition keeps being observed even if the project has
// rolled back since. What each run is is settled once, when it is created.
func (a *activeRun) resumeDeliveryTrial() {
	p := a.pipeline
	if a.state.WorkflowInstanceID == "" || p.Instances == nil {
		return
	}
	trial, err := p.deliveryTrialOver(a.state.RunID)
	if err != nil {
		// A run already in flight is never failed for its project's definition —
		// the work is under way, and what is broken is the watching. But this run
		// was being observed and now cannot be, so the reason is recorded rather
		// than passed over: an observation that stops without saying so is exactly
		// what the divergence exists to prevent, and a project that edited its file
		// into something that no longer compiles is the case somebody has to see.
		var refused projectDefinitionRefusal
		if errors.As(err, &refused) {
			a.recordDivergence(err)
		}
		return
	}
	if trial.instance != a.state.WorkflowInstanceID {
		// The record names an instance this build would not have created for this
		// run, so there is nothing here to carry on stepping.
		return
	}
	a.trial = trial
	// A definition edited under an instance already running it is refused by the
	// executor rather than migrated, and that refusal is worth recording once
	// here rather than at whichever boundary the run reaches first.
	if _, err := trial.executor.Resume(trial.instance); err != nil {
		a.recordDivergence(err)
	}
}

// deliveryTrialOver builds the executor a run is stepped with: the definition
// this project's integration policy binds, compiled with doors that perform
// nothing, under the same grant a delivery run is compiled and performed under.
func (p Pipeline) deliveryTrialOver(runID string) (*deliveryTrial, error) {
	bound := HumanApprovalWorkflowID
	if p.automatic() {
		bound = DeliveryWorkflowID
	}
	graph, err := observedDeliveryGraph(bound, p.ConfigPath)
	if err != nil {
		return nil, err
	}
	grant, err := deliveryGrant()
	if err != nil {
		return nil, err
	}
	trial := &deliveryTrial{instance: deliveryInstanceID(runID)}
	trial.executor = workflow.Executor[*activeRun]{
		Graph:     graph,
		Instances: p.Instances,
		Grant:     grant,
		// The same store the instances live in is where a person's recorded act
		// lives. The delivery definition declares no gate today, so nothing here
		// reads it yet; it is wired because a definition that declares one has to
		// meet the person's step rather than an executor that cannot see it.
		Gates: p.Instances,
		Outcome: func(_ string, _ *activeRun) (string, error) {
			// What the state produced is what the run said it produced a moment ago.
			// The state the executor is asking about is not re-checked here because
			// it was checked before the step: an observation that does not match
			// where the instance stands never becomes a step at all.
			return trial.reported, nil
		},
		Now: p.clock().Now,
	}
	return trial, nil
}

// observedDeliveryGraph compiles the definition in force — the project's own
// copy where it keeps one, otherwise the one this build ships — into a graph
// whose doors perform nothing.
//
// Everything else about the actions is the registered thing: the names, the
// summaries, the capabilities and what each wraps come from the same table
// `actions.go` builds the delivery registry from, so the topology stepped here
// is the topology the production registry compiles, the grant is checked at
// every boundary against what the real action requires, and the separation
// policies are asked the same question. Only `Perform` is replaced — and the
// digest says the two are the same definition, so an instance pinned by this
// graph is pinned to what the file it was read from means.
//
// A project's own file is refused exactly as strictly as the shipped one and
// answers to the same name: what a project can change is the sequence, and a
// file selecting an action nothing registers, requiring authority the grant does
// not confer, or routing an outcome no step produces is refused whole and named
// here rather than half adopted.
func observedDeliveryGraph(id, configPath string) (workflow.Graph[*activeRun], error) {
	sequence, err := deliverySequenceFor(id, configPath)
	if err != nil {
		return workflow.Graph[*activeRun]{}, err
	}
	steps := deliverySteps()
	inert := make([]action.Action[*activeRun], 0, len(steps))
	for _, step := range steps {
		door := step.action
		door.Perform = func(context.Context, *activeRun) error { return nil }
		inert = append(inert, door)
	}
	registry, err := action.New(inert...)
	if err != nil {
		return workflow.Graph[*activeRun]{}, fmt.Errorf("the delivery steps this build registers are not a registry: %w", err)
	}
	observed, err := compileDeliveryWith(sequence, registry)
	if err != nil {
		return workflow.Graph[*activeRun]{}, sequence.refuse(err)
	}
	// The graph compiled against the real registry is what the definition means;
	// this one has to be the same definition, and the digest is what says so.
	compiled, err := compileDelivery(sequence)
	if err != nil {
		return workflow.Graph[*activeRun]{}, sequence.refuse(err)
	}
	if observed.Digest() != compiled.Digest() {
		// Both graphs were compiled from the same bytes, so a disagreement here is
		// a defect in this build rather than in whichever file supplied them.
		return workflow.Graph[*activeRun]{}, fmt.Errorf("%s digests to %s with its doors performing nothing and to %s as it is compiled to be performed; they are not the same sequence", sequence.Source, observed.Digest(), compiled.Digest())
	}
	return observed, nil
}

// observe records that the run performed one state and produced one outcome.
//
// It returns nothing, and that is the contract the rest of the pipeline is
// written against: an observation cannot fail a run, cannot change where a run
// goes, and cannot cost it anything but a file write. A run on the legacy path
// passes straight through, which is every run a rolled-back project starts.
func (a *activeRun) observe(ctx context.Context, state, outcome string) {
	if a.trial == nil || a.trial.stopped != "" {
		return
	}
	if err := a.trial.step(ctx, a, state, outcome); err != nil {
		a.recordDivergence(err)
	}
}

// step puts one boundary to the definition: it refuses an observation of a state
// the instance is not standing in, and otherwise lets the executor resolve where
// the outcome goes and record the crossing.
func (t *deliveryTrial) step(ctx context.Context, run *activeRun, state, outcome string) error {
	instance, err := t.executor.Resume(t.instance)
	if err != nil {
		return err
	}
	if instance.State != state {
		return fmt.Errorf("the run performed %q and produced %q, and its instance stands in %q; the definition and the run are no longer in the same place", state, outcome, instance.State)
	}
	t.reported = outcome
	if _, err := t.executor.Step(ctx, t.instance, run); err != nil {
		return err
	}
	return nil
}

// recordDivergence writes on the run why it stopped being observed.
//
// The first one is kept: what a divergence costs is trust in the definition, and
// the boundary where the two first disagreed is where somebody reading it has to
// start. It is written to the record this process holds and saved, because a run
// can diverge after its last save of its own — the cleanup boundary is one — and
// a divergence nothing wrote down is a soak that looks clean. A save that fails
// is dropped for the same reason the observation cannot fail the run: this is
// watching delivery, not deciding it.
func (a *activeRun) recordDivergence(cause error) {
	if !a.noteDivergence(cause) {
		return
	}
	_ = a.pipeline.Store.Save(a.state)
}

// noteDivergence puts the divergence on the record this process holds without
// saving it, and reports whether this was the one that took.
//
// It is separate from the save so a caller that is about to write the run's
// record anyway can carry the divergence in that write rather than adding one of
// its own. Which divergence is kept is decided here and in one place, so the
// two callers cannot disagree about it.
func (a *activeRun) noteDivergence(cause error) bool {
	if a.trial == nil {
		// The observation could not be set up at all, so there is no trial to stop
		// and the run's own record is the only place this can be kept. A run
		// already carrying a divergence keeps the one it has, for the reason a
		// trial keeps its first: the boundary where the two first disagreed is
		// where somebody reading it has to start.
		if a.state.WorkflowDivergence != "" {
			return false
		}
		a.state.WorkflowDivergence = cause.Error()
		return true
	}
	if a.trial.stopped != "" {
		return false
	}
	a.trial.stopped = cause.Error()
	a.state.WorkflowDivergence = cause.Error()
	return true
}

// noteUnobserved puts on the run why it is not observed although its project
// asked for it to be.
//
// It is not a divergence and is kept in its own field rather than in that one. A
// divergence is what an instance and a run disagreed about, so it is only ever
// said of a run there was an instance for; this is said of a run there is not,
// which is why the record refuses to carry a divergence beside it and why a
// reader counting divergences would otherwise never see this at all.
//
// It notes rather than saves, as the divergence does: this is written before the
// item is claimed, and every path onwards from there writes the run's record.
func (a *activeRun) noteUnobserved(cause error) {
	if a.state.WorkflowUnobserved != "" {
		return
	}
	a.state.WorkflowUnobserved = cause.Error()
}

// observeUnfinished records, on a run that is ending terminally, that its
// instance never reached a terminal of its own.
//
// Every ending the definition expresses has already been observed by the time a
// run reaches here, so an instance still standing in a state is one the run left
// by a route the definition has no outcome for: a review that ended without a
// verdict and without the operator's stop, a `complete` that failed, a worktree
// that could not be cut before the first attempt. Leaving the instance where it
// stands is right, and it is what `observeReviewEnded` deliberately does — an
// instance dragged to the nearest outcome would record a run ending somewhere it
// did not.
//
// What is wrong is saying nothing else. The soak counts the items that carry no
// divergence, so a run whose instance stopped mid-graph would be counted as one
// that walked the definition to the end — the headline number silently
// undercounting exactly the runs it is least entitled to pass. So the gap is
// itself the divergence, named with the state the instance stopped in.
//
// It notes rather than saves: the caller is writing the run's terminal record
// next, and this is one of the fields that record carries.
func (a *activeRun) observeUnfinished() {
	if a.trial == nil || a.trial.stopped != "" {
		return
	}
	instance, err := a.trial.executor.Resume(a.trial.instance)
	if err != nil {
		// The instance cannot be read back, or is pinned to a definition this build
		// no longer holds. Either way the run is ending with nothing to say where
		// its observation got to, which is the same silence this exists to close.
		a.noteDivergence(err)
		return
	}
	if instance.Terminal {
		return
	}
	a.noteDivergence(unfinishedInstance(a.state.Status, instance.State))
}

// unfinishedInstance is the divergence a run ending terminally with a
// non-terminal instance records, wherever the ending is recorded.
//
// It is one function because there are two places a run becomes terminal and
// only one thing to say at either: the live pipeline's own ending, and the
// settlement a sweep writes for a run whose process died. A soak counts the runs
// carrying no divergence, so the two have to say the same thing in the same
// words or the count is read off two different measures.
func unfinishedInstance(status runstate.Status, state string) error {
	return fmt.Errorf("the run ended as %s and its instance stands in %q, which is not a terminal; the run left by a route the definition has no outcome for", status, state)
}

// workflowInstanceReader is the half of the instance store an ending needs.
//
// Reading whether the observation finished is all of it: nothing here resolves a
// transition, holds a graph, or steps anything, because a run that is being made
// terminal is one whose observation has already stopped. That is what lets a
// settlement ask the question at all — the sweep holds no definition and has no
// business compiling one to find out where an instance it will never step got to.
type workflowInstanceReader interface {
	LoadWorkflowInstance(instanceID string) (runstate.WorkflowInstance, error)
}

// unfinishedObservation is what a run being made terminal has to record about
// the instance that was observing it, and "" when there is nothing to record.
//
// It is the same question observeUnfinished asks, asked from outside a live run:
// a run whose process died is settled by a sweep rather than by its own pipeline,
// and its instance is left standing wherever the dead process got to. Without
// this, exactly those runs — the ones a soak is least entitled to pass — would
// reach a terminal status carrying no divergence and be counted clean.
//
// An instance that cannot be read is itself the divergence, for the reason
// observeUnfinished treats it as one: the run is ending with nothing able to say
// where its observation reached, which is the same silence this exists to close.
func unfinishedObservation(instances workflowInstanceReader, state runstate.State) string {
	if state.WorkflowInstanceID == "" || state.WorkflowDivergence != "" || instances == nil {
		return ""
	}
	instance, err := instances.LoadWorkflowInstance(state.WorkflowInstanceID)
	if err != nil {
		return err.Error()
	}
	if instance.Terminal {
		return ""
	}
	return unfinishedInstance(state.Status, instance.State).Error()
}

// parksTheRun reports an ending that is an instruction to continue later rather
// than an outcome the definition has anywhere to send.
//
// These are the five endings `stop` recognizes as pauses. A run that takes one
// has not left the state it is in — a later invocation reissues the attempt it
// was owed — so the instance stays exactly where it is and the transition is
// recorded when the resumed run makes it.
func parksTheRun(err error) bool {
	var (
		limited  usageLimitPause
		stopped  providerStop
		directed directivePause
		waiting  dependencyPause
		held     operatorHoldPause
	)
	return errors.As(err, &limited) || errors.As(err, &stopped) ||
		errors.As(err, &directed) || errors.As(err, &waiting) || errors.As(err, &held)
}

// raisesAnEscalation reports an ending the definition has no outcome for at all:
// a developer or a reviewer having said the work item cannot be met as it stands.
//
// It is not a park and not a failure. The run ends, successfully, having produced
// a decision for the development manager rather than a change — and no state in
// either built-in definition has an outcome that means that, so nothing is
// observed for it and the instance is left standing where the run left it. The
// gap is recorded as a divergence at the ending, which is what observeUnfinished
// is for.
func raisesAnEscalation(err error) bool {
	var raised escalationRaised
	return errors.As(err, &raised)
}

// observeDevelopEnded records what the developer's state produced when an
// attempt stopped being made.
//
// A park is not an outcome, and neither is an escalation. Everything else that
// ends a developer state is `stopped`, which is what the definition says it is:
// the operator stopped the run, or the invocation failed in a way no relaunch
// answers. The one ending that is neither is the spent relaunch budget, which is
// observed where it is decided because that is the only place that knows the
// budget is what ran out.
func (a *activeRun) observeDevelopEnded(ctx context.Context, err error) {
	switch {
	case err == nil:
		a.observe(ctx, deliveryDevelop, "produced")
	case parksTheRun(err), raisesAnEscalation(err):
	default:
		a.observe(ctx, deliveryDevelop, "stopped")
	}
}

// observeCheckEnded records what the gate produced. The two repair inputs each
// have a second outcome for the round that finds the shared budget already
// spent, which is the only way a definition can send a spent budget somewhere
// else; a suite that could not run at all is neither, and ends the run.
//
// A park is guarded for here as it is at the developer's state, and for the same
// reason rather than for an ending `verify` currently produces. It does not
// produce one today: both callers ask the directive and the dependency before
// they call it, and what `verify` itself returns is a refusal, a check failure, a
// check stopped on time, or an infrastructure error. But a park is an instruction
// to reissue the round rather than an outcome — the run has not left the check —
// and stepping the instance to `unrunnable` for one would strand it a state ahead
// of the run and make the resumed run record a divergence that is this
// classifier's own doing. Guarding costs nothing and does not depend on that
// reading of `verify` staying true.
func (a *activeRun) observeCheckEnded(ctx context.Context, err error, unrepaired bool) {
	if parksTheRun(err) {
		return
	}
	var refused pathRefusal
	if errors.As(err, &refused) {
		if unrepaired {
			a.observe(ctx, deliveryCheck, "refused-unrepaired")
			return
		}
		a.observe(ctx, deliveryCheck, "refused")
		return
	}
	// A change nobody ran anything against is refused by the same state, in front
	// of the same checks, and handed back into the same loop — so it is the same
	// outcome. The definition distinguishes what the transition is rather than
	// which gate decided it, and which one this was is on the run's own record.
	var missing missingVerification
	if errors.As(err, &missing) {
		if unrepaired {
			a.observe(ctx, deliveryCheck, "refused-unrepaired")
			return
		}
		a.observe(ctx, deliveryCheck, "refused")
		return
	}
	// A change too large for the reviewer's copy is refused by the same state and
	// never handed back, whatever budget is left.
	var overBound reviewBoundRefusal
	if errors.As(err, &overBound) {
		a.observe(ctx, deliveryCheck, "refused-unrepaired")
		return
	}
	var failing checkFailure
	if errors.As(err, &failing) {
		if unrepaired {
			a.observe(ctx, deliveryCheck, "failed-unrepaired")
			return
		}
		a.observe(ctx, deliveryCheck, "failed")
		return
	}
	a.observe(ctx, deliveryCheck, "unrunnable")
}

// observeReviewEnded records what the reviewer's state produced.
//
// A verdict that was reached is one of the three the definition distinguishes,
// or the escalation it does not. An ending without a verdict is `stopped` only
// where the definition says it is — the boundary before a verdict is bought,
// which is where a run the operator stopped ends. Anything else that ends a
// review is deliberately not observed: the definition has no outcome for it, and
// leaving the instance standing in the state it never left says that truthfully,
// where naming the nearest outcome would record a run that ended somewhere it did
// not.
func (a *activeRun) observeReviewEnded(ctx context.Context, decision review.Decision, err error, unrepaired bool) {
	if err != nil {
		var stopped operatorStop
		if errors.As(err, &stopped) {
			a.observe(ctx, deliveryReview, "stopped")
		}
		return
	}
	switch {
	case decision == review.DecisionApprove:
		a.observe(ctx, deliveryReview, "approved")
	case decision == review.DecisionEscalate:
		// The definition has no outcome for it, so nothing is observed and the
		// instance is left standing in the review state it never left. That is the
		// rule this function already holds to, and it is the truthful answer here
		// too: naming the nearest outcome would record the run ending somewhere it
		// did not. The gap is recorded as a divergence where the run ends, exactly
		// as every other ending the definition cannot express is.
	case unrepaired:
		a.observe(ctx, deliveryReview, "unresolved")
	default:
		a.observe(ctx, deliveryReview, "changes-requested")
	}
}
