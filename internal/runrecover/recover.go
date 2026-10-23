package runrecover

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/health"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Runs is the run store a recovery acts through. *runstate.Store is one.
type Runs interface {
	// Read is the tolerant door, for answering a repeat without the lease.
	Read(runID string) (runstate.State, error)
	// AdoptRun takes the run's lease and reads its record strictly under it.
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Load(runID string) (runstate.State, error)
	Save(state runstate.State) error
}

// Observations is where the health observations a request names are kept.
// *runstate.HealthObservationStore is one.
type Observations interface {
	Load(id string) (runstate.HealthObservationRecord, error)
}

// Recoverer carries out run.recover for one product.
type Recoverer struct {
	Runs         Runs
	Observations Observations
	// Blockers is the run's item's unfinished dependencies, read fresh from the
	// tracker as the harness acts. It is the rule the pipeline pauses a run by
	// (orchestrator.BlockingDependencies).
	Blockers func(ctx context.Context, workItemID string) ([]string, error)
	// Continuable says whether the pipeline can take this in-flight run up from
	// its record, and why not where it cannot. Finishable says whether the
	// steps after this run's stopped integration can be finished, and why not.
	// Both are the orchestrator's (RecoveryContinuable, RecoveryFinishable),
	// because the rules are the ones the pipeline and the integration resumer
	// apply when they take the run up.
	Continuable func(state runstate.State) error
	Finishable  func(state runstate.State) error
	// Waitable says whether a dependency wait may be recorded on this run: not
	// on one whose integration is begun or done, which reconciliation settles
	// (orchestrator.RecoveryWaitable).
	Waitable func(state runstate.State) error
	Now      func() time.Time
}

func (r Recoverer) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// notBuilt names the slice of stopped-run recovery that builds each action
// this build refuses.
var notBuilt = map[runstate.RecoveryAction]string{
	runstate.RecoverRepair:   "a repair through run.recover is built by stopped-run recovery (yoyodyne-ifd.428.99); until then a repair is decided on the list of stopped runs as before, and the harness carries it out",
	runstate.RecoverRerun:    "a rerun through run.recover is built by stopped-run recovery (yoyodyne-ifd.428.99); until then a rerun is decided on the list of stopped runs as before, and the harness carries it out",
	runstate.RecoverGiveBack: "giving the item back is built by stopped-run recovery (yoyodyne-ifd.428.99)",
	runstate.RecoverAsk:      "asking the Lead Product Manager through run.recover is built by stopped-run recovery (yoyodyne-ifd.428.99); until then ask her with the ask block",
}

// Recover carries out one request for the role by. It never fails: everything
// that went wrong is in the result, as a refusal or as unknown.
func (r Recoverer) Recover(ctx context.Context, by domain.AgentRole, request Request) Result {
	result := Result{Request: request.ID, Run: request.Run, Action: request.Action, At: r.now()}
	refuse := func(why Refusal, says string, next string) Result {
		result.Outcome, result.Refusal, result.Says = Refused, why, says
		if result.NextOwner == "" {
			result.NextOwner = ownership.MoverDevelopmentManager
		}
		result.Next = next
		return result
	}
	if err := request.Validate(); err != nil {
		return refuse(RefusedNotAllowed, "the request is not one this tool takes: "+err.Error(), "correct the request and send it with a new request identifier")
	}
	if by != domain.RoleDevelopmentManager {
		return refuse(RefusedNoGrant, fmt.Sprintf("run.recover is the development manager's, and %s holds no grant for it", by), "")
	}
	if why, built := notBuilt[request.Action]; built {
		return refuse(RefusedNotBuilt, why, "")
	}
	digest := request.Digest()

	// A repeat of a request already applied is answered from the record and needs
	// no lease, so it is answered even while another process holds the run.
	if recorded, err := r.Runs.Read(request.Run); err == nil {
		if prior, found := recorded.RecoveryRequest(request.ID); found && prior.Outcome == runstate.RecoveryApplied {
			if prior.Digest != digest {
				return refuse(RefusedIdentifierReused, fmt.Sprintf("request %s was already used on run %s for %q, with other content", request.ID, request.Run, prior.Action), "send a new request identifier")
			}
			return r.repeat(result, recorded, prior)
		}
	}

	if refusal := r.checkObservation(request); refusal != nil {
		return refuse(refusal.why, refusal.says, "observe again with health.observe and decide from the new observation")
	}

	state, lease, err := r.Runs.AdoptRun(ctx, request.Run)
	if err != nil {
		if errors.Is(err, runstate.ErrRunHeld) {
			result.NextOwner = ownership.MoverHarness
			return refuse(RefusedWorkerPresent, fmt.Sprintf("another process holds run %s's lease, so its worker is live or is being taken up; nothing was changed", request.Run),
				"run.inspect shows who holds it; a live worker keeps the run, whatever its age")
		}
		return refuse(RefusedUnreadable, fmt.Sprintf("run %s could not be taken up to act on: %v", request.Run, err), "run.inspect shows what can be read of it")
	}
	defer lease.Release()

	// A request written before whose writer could not read it back is settled
	// first: its entry and its effect were one write, so finding the entry is
	// finding the effect.
	if settled, found := r.settlePending(state); found {
		// Saved at once, so a request answered from here on — this one's repeat
		// included — leaves the record settled. A save that fails leaves the
		// entries pending for the next request, which changes nothing about what
		// was done.
		_ = r.Runs.Save(settled)
		state = settled
	}
	if prior, found := state.RecoveryRequest(request.ID); found {
		if prior.Digest != digest {
			return refuse(RefusedIdentifierReused, fmt.Sprintf("request %s was already used on run %s for %q, with other content", request.ID, request.Run, prior.Action), "send a new request identifier")
		}
		return r.repeat(result, state, prior)
	}

	if differs := expectationDiffers(state, request); len(differs) > 0 {
		result.Differs = differs
		return refuse(RefusedStateMoved, fmt.Sprintf("run %s is not as the request expected: %s; nothing was changed", request.Run, strings.Join(differs, "; ")),
			"inspect the run again and decide from what it says now")
	}
	// The observation's age is asked again at the moment of acting, because that
	// is the moment the rule is about.
	if refusal := r.checkObservation(request); refusal != nil {
		return refuse(refusal.why, refusal.says, "observe again with health.observe and decide from the new observation")
	}

	next, effect := r.effect(ctx, state, request)
	if effect.refusal != "" {
		result.NextOwner = effect.owner
		return refuse(effect.refusal, effect.says, effect.next)
	}
	return r.write(result, state, next, request, by, digest, effect)
}

type observationRefusal struct {
	why  Refusal
	says string
}

func (r Recoverer) checkObservation(request Request) *observationRefusal {
	if r.Observations == nil {
		return &observationRefusal{RefusedUnreadable, "no health observations are kept for this conversation to check the request against"}
	}
	record, err := r.Observations.Load(request.Observation)
	if errors.Is(err, runstate.ErrHealthObservationUnknown) {
		return &observationRefusal{RefusedObservationAbsent, fmt.Sprintf("observation %s is not one this product keeps; it may be older than an hour", request.Observation)}
	}
	if err != nil {
		return &observationRefusal{RefusedUnreadable, fmt.Sprintf("observation %s could not be read: %v", request.Observation, err)}
	}
	if age := r.now().Sub(record.ObservedAt); age > health.MaxAge {
		return &observationRefusal{RefusedObservationOld, fmt.Sprintf("observation %s was taken %s ago, and nothing is acted on from one older than %s",
			request.Observation, age.Round(time.Second), health.MaxAge)}
	}
	return nil
}

// repeat is the answer to a request the run already recorded as applied.
func (r Recoverer) repeat(result Result, state runstate.State, prior runstate.RecoveryRequest) Result {
	result.Outcome, result.Repeat = Applied, true
	at := prior.RequestedAt
	if prior.SettledAt != nil {
		at = *prior.SettledAt
	}
	result.Says = fmt.Sprintf("request %s was already applied to run %s at %s; nothing was done again", prior.ID, state.RunID, at.UTC().Format(time.RFC3339))
	result.NextOwner, result.Next, result.Verify = followUp(state, prior.Action)
	return result
}

// settlePending marks every pending request on the record applied, in the
// copy handed back, and reports whether there was one.
func (r Recoverer) settlePending(state runstate.State) (runstate.State, bool) {
	if len(state.PendingRecoveryRequests()) == 0 {
		return state, false
	}
	now := r.now()
	requests := append([]runstate.RecoveryRequest(nil), state.RecoveryRequests...)
	for index := range requests {
		if requests[index].Outcome == runstate.RecoveryPending {
			requests[index].Outcome = runstate.RecoveryApplied
			requests[index].SettledAt = &now
		}
	}
	state.RecoveryRequests = requests
	return state, true
}

// expectationDiffers is each way the run is not as the request expected it.
func expectationDiffers(state runstate.State, request Request) []string {
	var differs []string
	if state.Status != request.ExpectStatus {
		differs = append(differs, fmt.Sprintf("its saved status is %q, not %q", state.Status, request.ExpectStatus))
	}
	if state.Phase != request.ExpectPhase {
		differs = append(differs, fmt.Sprintf("its saved phase is %q, not %q", state.Phase, request.ExpectPhase))
	}
	if request.Stop != nil {
		attempt := previousAttempt(state)
		if attempt != request.Stop.Attempt {
			differs = append(differs, fmt.Sprintf("its latest attempt is %d, not %d", attempt, request.Stop.Attempt))
		}
		at, stopped := stopTime(state)
		switch {
		case !stopped:
			differs = append(differs, "it records no stop")
		case !sameInstant(at, request.Stop.At):
			differs = append(differs, fmt.Sprintf("its latest stop is at %s, not %s; a later stop has replaced the one answered", at.UTC().Format(time.RFC3339), request.Stop.At.UTC().Format(time.RFC3339)))
		}
	}
	return differs
}

// previousAttempt is the attempt the run was at before this lease was taken:
// taking it began a new one (runstate's worker.go).
func previousAttempt(state runstate.State) int {
	if state.Worker == nil || state.Worker.Attempt < 1 {
		return 0
	}
	return state.Worker.Attempt - 1
}

// stopTime is when the run's latest stop happened, as the record says it: the
// redeploy that put it down, or its end.
func stopTime(state runstate.State) (time.Time, bool) {
	switch {
	case state.RedeployStop != nil:
		return state.RedeployStop.At, true
	case state.Status.Terminal() && state.CompletedAt != nil:
		return *state.CompletedAt, true
	}
	return time.Time{}, false
}

func sameInstant(a, b time.Time) bool {
	difference := a.Sub(b)
	if difference < 0 {
		difference = -difference
	}
	return difference < time.Second
}

// effect is what one action does to the run, decided against the record read
// under the lease.
type effect struct {
	refusal Refusal
	says    string
	owner   ownership.Mover
	next    string
	// applied is the account of what the write did.
	applied string
}

func (r Recoverer) effect(ctx context.Context, state runstate.State, request Request) (runstate.State, effect) {
	next := state
	now := r.now()
	notAllowed := func(says, next string) (runstate.State, effect) {
		return state, effect{refusal: RefusedNotAllowed, says: says, owner: ownership.MoverDevelopmentManager, next: next}
	}
	switch request.Action {
	case runstate.RecoverContinue:
		if !state.Status.InFlight() {
			return notAllowed(fmt.Sprintf("run %s has ended %s, and only a run still in flight is continued", state.RunID, state.Status),
				"a stopped run is repaired, rerun or given back from the list of stopped runs")
		}
		if r.Continuable == nil {
			return notAllowed("nothing is wired to this conversation to say whether the pipeline can take the run up", "")
		}
		if err := r.Continuable(state); err != nil {
			return notAllowed(fmt.Sprintf("run %s cannot be continued from its record: %v", state.RunID, err), "record an unresolved hold naming what has to happen first")
		}
		blockers, refusal := r.blockers(ctx, state)
		if refusal != nil {
			return state, *refusal
		}
		if len(blockers) > 0 {
			return notAllowed(fmt.Sprintf("%s still waits on unfinished work (%s), so a continued run would only stop again before it did anything", state.WorkItemID, strings.Join(blockers, ", ")),
				"record a dependency wait instead, which gives back the developer slot until that work closes")
		}
		next.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: request.ID, Action: runstate.RecoverContinue, Reason: request.Reason, RequestedAt: now}
		next.RecoveryHold = nil
		return next, effect{applied: fmt.Sprintf("run %s is recorded to be continued from its %s phase, in its own branch, worktree and session; the work session's next pull takes it up", state.RunID, nonEmpty(string(state.Phase)))}
	case runstate.RecoverFinishMerge:
		if r.Finishable == nil {
			return notAllowed("nothing is wired to this conversation to say whether the run's integration can be finished", "")
		}
		if err := r.Finishable(state); err != nil {
			return notAllowed(fmt.Sprintf("the steps after run %s's integration cannot be finished from its record: %v", state.RunID, err), "")
		}
		next.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: request.ID, Action: runstate.RecoverFinishMerge, Reason: request.Reason, RequestedAt: now}
		next.RecoveryHold = nil
		return next, effect{applied: fmt.Sprintf("run %s's approved change is recorded to have its integration finished; the work session's next carry-out of recorded decisions takes it up", state.RunID)}
	case runstate.RecoverWait:
		if !state.Status.InFlight() {
			return notAllowed(fmt.Sprintf("run %s has ended %s, so there is nothing in flight to wait", state.RunID, state.Status), "")
		}
		if state.Document != nil {
			return notAllowed(fmt.Sprintf("run %s lands a document, which holds a document landing and no developer slot, and waits on no work item", state.RunID), "")
		}
		if state.OutsideFile != nil {
			return notAllowed(fmt.Sprintf("run %s brings an outside file in, which holds no developer slot and waits on no work item", state.RunID), "")
		}
		if r.Waitable == nil {
			return notAllowed("nothing is wired to this conversation to say whether the run may wait", "")
		}
		if err := r.Waitable(state); err != nil {
			return notAllowed(fmt.Sprintf("run %s may not be put on a wait: %v", state.RunID, err),
				"leave it to reconciliation, and check integration completed with run.verify; an approved change whose integration stopped is finished with finish the merge")
		}
		blockers, refusal := r.blockers(ctx, state)
		if refusal != nil {
			return state, *refusal
		}
		if len(blockers) == 0 {
			return notAllowed(fmt.Sprintf("%s waits on no unfinished work, so there is nothing for the run to wait on", state.WorkItemID),
				"continue it instead, or record an unresolved hold naming what it is waiting for")
		}
		// The interrupted phase is kept exactly: a run waiting at developing is
		// one whose developer had not finished, and it resumes there.
		next.DependencyPause = &runstate.DependencyPause{Blockers: blockers}
		next.RecoveryHold = nil
		if next.RecoveryContinuation.Outstanding() {
			next.RecoveryContinuation = nil
		}
		return next, effect{applied: fmt.Sprintf("run %s waits, at its %s phase, on %s closing; it keeps its item, branch, worktree and session, and holds no developer slot until then",
			state.RunID, nonEmpty(string(state.Phase)), strings.Join(blockers, ", "))}
	case runstate.RecoverHold:
		hold := runstate.RecoveryHold{RequestID: request.ID, Owner: request.Hold.Owner, NextAction: request.Hold.NextAction, FollowUp: request.Hold.FollowUp, At: now}
		if request.Hold.FollowUpAt != nil {
			at := request.Hold.FollowUpAt.UTC()
			hold.FollowUpAt = &at
		}
		next.RecoveryHold = &hold
		return next, effect{applied: fmt.Sprintf("run %s carries an unresolved hold: %s, the %s's move, looked at again %s", state.RunID, hold.NextAction, hold.Owner, hold.FollowUp)}
	case runstate.RecoverRelease:
		if state.Status.InFlight() {
			return notAllowed(fmt.Sprintf("run %s is still in flight, and this releases only what a run that has ended holds", state.RunID),
				"continue it, record a dependency wait, which gives back its developer slot, or record an unresolved hold")
		}
		// Of what a finished run with no worker could still hold, two are counted
		// only from runs in flight: developer slots (runstate.State.HoldsDeveloperSlot)
		// and the files the scheduler keeps other work away from (its occupied and
		// waiting runs). So a finished run holds neither, and there is nothing of
		// either to write. The third is its item's claim in the tracker, which is
		// kept while a decision about the stop stands; giving that claim back is
		// giving the item back, which stopped-run recovery builds. Saying this
		// action applied would claim a change nobody made.
		return notAllowed(fmt.Sprintf("run %s has ended %s; a run that has ended holds no developer slot and no files, because both are counted only from runs still in flight, so there is none of either to release, and run.verify %q answers met; if its item is still claimed after the stop, that claim is given back by giving the item back", state.RunID, state.Status, runinspect.ExpectReservationReleased),
			"giving the item back is built by stopped-run recovery (yoyodyne-ifd.428.99); until then the item's claim follows the decision recorded on the list of stopped runs")
	}
	return notAllowed(fmt.Sprintf("action %q is not carried out by this build", request.Action), "")
}

func (r Recoverer) blockers(ctx context.Context, state runstate.State) ([]string, *effect) {
	if r.Blockers == nil {
		return nil, &effect{refusal: RefusedUnreadable, says: "nothing is wired to this conversation to read what the run's item waits on", owner: ownership.MoverDevelopmentManager}
	}
	blockers, err := r.Blockers(ctx, state.WorkItemID)
	if err != nil {
		return nil, &effect{refusal: RefusedUnreadable, says: fmt.Sprintf("what %s waits on could not be read from the tracker: %v; nothing was changed", state.WorkItemID, err),
			owner: ownership.MoverDevelopmentManager, next: "ask again once the tracker answers"}
	}
	return blockers, nil
}

func nonEmpty(phase string) string {
	if phase == "" {
		return "unrecorded"
	}
	return phase
}

// write records the request with its effect in one write, reads it back, and
// marks it applied.
func (r Recoverer) write(result Result, state, next runstate.State, request Request, by domain.AgentRole, digest string, done effect) Result {
	now := r.now()
	entry := runstate.RecoveryRequest{ID: request.ID, Digest: digest, Action: request.Action, Observation: request.Observation, By: by, Reason: request.Reason, RequestedAt: now}
	requests := append([]runstate.RecoveryRequest(nil), next.RecoveryRequests...)
	if len(requests) >= runstate.MaxRecoveryRequests {
		requests = requests[len(requests)-runstate.MaxRecoveryRequests+1:]
	}
	next.RecoveryRequests = append(requests, entry)
	next.UpdatedAt = now

	saveErr := r.Runs.Save(next)
	readback, readErr := r.Runs.Load(state.RunID)
	_, found := readback.RecoveryRequest(request.ID)
	switch {
	case readErr != nil:
		result.Outcome = Unknown
		result.Says = fmt.Sprintf("the write of request %s to run %s could not be read back (%s), so whether it happened is not known", request.ID, state.RunID, describeWrite(saveErr, readErr))
		result.NextOwner = ownership.MoverDevelopmentManager
		result.Next = "do not act on this run again until a later run.recover request has read it back under its lease; health.observe reports it until then"
		return result
	case !found:
		result.Outcome, result.Refusal = Refused, RefusedNotWritten
		result.Says = fmt.Sprintf("request %s was not written to run %s, which reads back as it was: %v", request.ID, state.RunID, saveErr)
		result.NextOwner = ownership.MoverDevelopmentManager
		result.Next = "nothing changed; ask again with a new request identifier once whatever refused the write is cleared"
		return result
	}
	// The change reads back, so it is applied. Marking the entry so is the
	// writer's account of having read it; a mark that cannot be saved leaves
	// the entry for the next request under the lease to settle, which changes
	// nothing about what was done.
	settledAt := r.now()
	marked := readback
	marked.RecoveryRequests = append([]runstate.RecoveryRequest(nil), readback.RecoveryRequests...)
	for index := range marked.RecoveryRequests {
		if marked.RecoveryRequests[index].ID == request.ID {
			marked.RecoveryRequests[index].Outcome = runstate.RecoveryApplied
			marked.RecoveryRequests[index].SettledAt = &settledAt
		}
	}
	_ = r.Runs.Save(marked)

	result.Outcome = Applied
	result.Says = done.applied
	if saveErr != nil {
		result.Says += fmt.Sprintf("; the store reported an error as it wrote (%v), and the change reads back", saveErr)
	}
	result.NextOwner, result.Next, result.Verify = followUp(readback, request.Action)
	return result
}

func describeWrite(saveErr, readErr error) string {
	if saveErr == nil {
		return fmt.Sprintf("the write reported success, and reading it back failed: %v", readErr)
	}
	return fmt.Sprintf("the write reported %v, and reading it back failed: %v", saveErr, readErr)
}

// followUp is whose move comes after an applied action, what it is, and the
// run.verify expected state that checks it ("Checking the outcome" in the
// design).
func followUp(state runstate.State, action runstate.RecoveryAction) (ownership.Mover, string, runinspect.Expected) {
	switch action {
	case runstate.RecoverContinue:
		return ownership.MoverHarness, "a watching work session takes the run up at its next pull; check continuation started, and then worker live", runinspect.ExpectContinuationStarted
	case runstate.RecoverFinishMerge:
		return ownership.MoverHarness, "the work session's carry-out of recorded decisions finishes the integration; check integration completed", runinspect.ExpectIntegrationCompleted
	case runstate.RecoverWait:
		blockers := ""
		if state.DependencyPause != nil {
			blockers = " (" + state.DependencyPause.Summary() + ")"
		}
		return ownership.MoverHarness, "a watching work session continues the run at the first pull after the work it waits on" + blockers + " closes; check wait recorded", runinspect.ExpectWaitRecorded
	case runstate.RecoverHold:
		owner := ownership.MoverDevelopmentManager
		if state.RecoveryHold != nil {
			owner = ownership.MoverOf(state.RecoveryHold.Owner)
		}
		return owner, "the hold's owner takes its next action at its follow-up; check unresolved hold recorded", runinspect.ExpectUnresolvedHoldRecorded
	}
	return ownership.MoverDevelopmentManager, "", ""
}
