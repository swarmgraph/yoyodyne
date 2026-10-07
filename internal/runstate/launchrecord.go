package runstate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// The routing record's half of launching an attempt: registering the execution
// before it may begin work, setting aside a registered launch that never began,
// what recovery is waiting on, results that arrive after their attempt stopped
// mattering, and the external effects an operation intends. Launching and
// observing processes are launch.go's.

// RegisterExecution records the execution a prepared attempt was started as,
// before that execution is allowed to begin work. Registering the same
// execution again changes nothing; a different one is refused while one is
// registered, because a second launch of the attempt is allowed only once
// recovery has confirmed the first stopped without beginning (ReleaseUnlaunched).
func (r *RunRouting) RegisterExecution(operationID, attemptID string, identity ExecutionIdentity) (bool, error) {
	operation, attempt, err := r.find(operationID, attemptID)
	if err != nil {
		return false, err
	}
	identity.StartedAt = identity.StartedAt.UTC()
	identity.RegisteredAt = identity.RegisteredAt.UTC()
	if problems := identity.problems(attemptID); len(problems) > 0 {
		return false, fmt.Errorf("invalid execution identity: %s", strings.Join(problems, "; "))
	}
	if attempt.Execution != nil {
		if reflect.DeepEqual(*attempt.Execution, identity) {
			return false, nil
		}
		return false, conflict("attempt %s already registered execution pid %d", attemptID, attempt.Execution.PID)
	}
	if attempt.State != AttemptPrepared {
		return false, conflict("attempt %s is %s, so no execution is registered for it", attemptID, attempt.State)
	}
	if operation.Completed != nil {
		return false, conflict("operation %s is complete", operationID)
	}
	attempt.Execution = &identity
	return true, nil
}

// ReleaseUnlaunched sets aside a registered execution of a prepared attempt
// that recovery has confirmed stopped. The attempt was never marked launched,
// and MarkLaunched is written before the gate opens, so that execution never
// began work and the same reserved attempt may be launched again. It is the
// caller's to have confirmed the stop; see Store.ReconcileLaunch.
func (r *RunRouting) ReleaseUnlaunched(operationID, attemptID string) (bool, error) {
	_, attempt, err := r.find(operationID, attemptID)
	if err != nil {
		return false, err
	}
	if attempt.State != AttemptPrepared || attempt.Execution == nil {
		return false, nil
	}
	if len(attempt.Unreleased) >= maxUnreleasedLaunches {
		return false, conflict("attempt %s has reached its bound of %d launches that never began", attemptID, maxUnreleasedLaunches)
	}
	attempt.Unreleased = append(attempt.Unreleased, *attempt.Execution)
	attempt.Execution = nil
	return true, nil
}

// NoteReconciling records what recovery is waiting on before the operation may
// launch again. The same wait noted again keeps when it began. It changes no
// selection, allowance, or counter.
func (r *RunRouting) NoteReconciling(operationID string, wait OperationReconciling) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if strings.TrimSpace(wait.Reason) == "" || len(wait.Reason) > maxRoutingText {
		return false, errors.New("a recovery wait states its reason within its bound")
	}
	if held := operation.Reconciling; held != nil && held.Attempt == wait.Attempt && held.Reason == wait.Reason {
		return false, nil
	}
	wait.Since = wait.Since.UTC()
	operation.Reconciling = &wait
	return true, nil
}

// ClearReconciling records that recovery is no longer waiting.
func (r *RunRouting) ClearReconciling(operationID string) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if operation.Reconciling == nil {
		return false, nil
	}
	operation.Reconciling = nil
	return true, nil
}

// AttemptResult is a result an execution reported, with the operation,
// attempt, and candidate it was produced for.
type AttemptResult struct {
	Operation string
	Attempt   string
	Candidate string
	Ending    AttemptEnding
	// Usage references the usage report the result carried. It is kept on the
	// attempt whatever becomes of the result.
	Usage string
}

// ResultDisposition is what became of a reported result.
type ResultDisposition string

const (
	// ResultAdopted ended the attempt that reported it.
	ResultAdopted ResultDisposition = "adopted"
	// ResultLate is a result from an attempt that had already ended another
	// way: the recorded ending stands, and only its usage is kept.
	ResultLate ResultDisposition = "late"
	// ResultRefused is a result naming a candidate its operation is not
	// judging: it ends nothing and decides nothing, and only its usage is kept.
	ResultRefused ResultDisposition = "refused"
)

// AcceptResult takes a result an execution reported. A result stays tied to
// the attempt and operation that produced it: it can end only its own attempt,
// never the operation's current one in its place, and never one whose ending is
// already recorded; one naming another candidate supplies no verdict at all.
// Whatever happens to the result, the usage it reported is kept on its attempt.
func (r *RunRouting) AcceptResult(result AttemptResult) (ResultDisposition, bool, error) {
	operation, attempt, err := r.find(result.Operation, result.Attempt)
	if err != nil {
		return "", false, err
	}
	changed := false
	if result.Usage != "" {
		if !routingReferencePattern.MatchString(result.Usage) {
			return "", false, fmt.Errorf("usage reference %q is invalid", result.Usage)
		}
		if !containsString(attempt.Usage, result.Usage) {
			if len(attempt.Usage) >= maxAttemptUsage {
				return "", false, conflict("attempt %s has reached its bound of %d usage reports", attempt.ID, maxAttemptUsage)
			}
			attempt.Usage = append(attempt.Usage, result.Usage)
			changed = true
		}
	}
	if operation.Candidate != "" && result.Candidate != operation.Candidate {
		return ResultRefused, changed, nil
	}
	if attempt.State == AttemptPrepared {
		return "", false, conflict("attempt %s was never launched, so it reported no result", attempt.ID)
	}
	if attempt.Ended != nil {
		ending := result.Ending
		if attempt.Ended.Classification == ending.Classification && attempt.Ended.Result == ending.Result {
			return ResultAdopted, changed, nil
		}
		return ResultLate, changed, nil
	}
	if _, err := r.EndAttempt(result.Operation, result.Attempt, result.Ending); err != nil {
		return "", false, err
	}
	return ResultAdopted, true, nil
}

func containsString(values []string, value string) bool {
	for _, held := range values {
		if held == value {
			return true
		}
	}
	return false
}

// IntendEffect records an external effect before it is attempted. Recording
// the same intent again changes nothing; a key already settled is not intended
// again, because a performed effect is not repeated and an absent one is
// intended under a new key.
func (r *RunRouting) IntendEffect(operationID string, effect ExternalEffect) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	if !routingReferencePattern.MatchString(effect.Key) || !routingReferencePattern.MatchString(effect.Kind) {
		return false, errors.New("an effect names its key and kind as references")
	}
	if operation.Completed != nil {
		return false, conflict("operation %s is complete", operationID)
	}
	for _, held := range operation.Effects {
		if held.Key != effect.Key {
			continue
		}
		if held.State == EffectIntended && held.Kind == effect.Kind && held.Attempt == effect.Attempt {
			return false, nil
		}
		return false, conflict("effect %s is already recorded as %s", effect.Key, held.State)
	}
	if effect.Attempt != "" {
		if _, ok := operation.attempt(effect.Attempt); !ok {
			return false, conflict("effect %s names attempt %s, which is not an attempt of operation %s", effect.Key, effect.Attempt, operationID)
		}
	}
	if len(operation.Effects) >= maxOperationEffects {
		return false, conflict("operation %s has reached its bound of %d effects", operationID, maxOperationEffects)
	}
	effect.State = EffectIntended
	effect.At = effect.At.UTC()
	operation.Effects = append(operation.Effects, effect)
	return true, nil
}

// SettleEffect records whether an intended effect happened. Settling it the
// same way again changes nothing; settling it the other way is refused.
func (r *RunRouting) SettleEffect(operationID, key string, performed bool, at time.Time) (bool, error) {
	operation, ok := r.Operation(operationID)
	if !ok {
		return false, conflict("operation %s is not recorded", operationID)
	}
	state := EffectAbsent
	if performed {
		state = EffectPerformed
	}
	for index := range operation.Effects {
		effect := &operation.Effects[index]
		if effect.Key != key {
			continue
		}
		switch effect.State {
		case state:
			return false, nil
		case EffectIntended:
			effect.State = state
			effect.At = at.UTC()
			return true, nil
		}
		return false, conflict("effect %s is already recorded as %s", key, effect.State)
	}
	return false, conflict("effect %s is not recorded on operation %s", key, operationID)
}

// pendingEffects are the operation's effects nobody has yet established as
// performed or absent.
func (o RoutedOperation) pendingEffects() []ExternalEffect {
	var pending []ExternalEffect
	for _, effect := range o.Effects {
		if effect.State == EffectIntended {
			pending = append(pending, effect)
		}
	}
	return pending
}

func (e ExecutionIdentity) problems(attemptID string) []string {
	var problems []string
	if strings.TrimSpace(e.Host) == "" || len(e.Host)+len(e.Boot)+len(e.Launcher)+len(e.HoldFile) > maxRoutingText {
		problems = append(problems, "an execution names its host, and its host, boot, launcher, and hold fit their bound")
	}
	if e.Machine != "" && !machineIDPattern.MatchString(e.Machine) {
		problems = append(problems, "an execution's machine is an identifier this harness makes")
	}
	if strings.TrimSpace(e.Launcher) == "" {
		problems = append(problems, "an execution names the launcher that started it")
	}
	if e.PID <= 0 || e.ProcessGroup <= 0 {
		problems = append(problems, "an execution names its process and the group it leads")
	}
	if e.Hold != holdName(attemptID) || strings.TrimSpace(e.HoldFile) == "" {
		problems = append(problems, fmt.Sprintf("an execution of attempt %s holds %s", attemptID, holdName(attemptID)))
	}
	if e.StartedAt.IsZero() || e.RegisteredAt.IsZero() {
		problems = append(problems, "an execution says when it started and when it was registered")
	}
	return problems
}

func (a InvocationAttempt) launchProblems() []string {
	var problems []string
	if a.Execution != nil {
		problems = append(problems, a.Execution.problems(a.ID)...)
	}
	if len(a.Unreleased) > maxUnreleasedLaunches {
		problems = append(problems, fmt.Sprintf("attempt %s has too many launches that never began", a.ID))
	}
	for _, unreleased := range a.Unreleased {
		problems = append(problems, unreleased.problems(a.ID)...)
	}
	if len(a.Usage) > maxAttemptUsage {
		problems = append(problems, fmt.Sprintf("attempt %s has too many usage reports", a.ID))
	}
	for _, usage := range a.Usage {
		if !routingReferencePattern.MatchString(usage) {
			problems = append(problems, fmt.Sprintf("attempt %s usage reference %q is invalid", a.ID, usage))
		}
	}
	return problems
}

func (o RoutedOperation) launchProblems() []string {
	var problems []string
	if len(o.Effects) > maxOperationEffects {
		problems = append(problems, fmt.Sprintf("operation %s has too many effects", o.ID))
	}
	keys := map[string]bool{}
	for _, effect := range o.Effects {
		if !routingReferencePattern.MatchString(effect.Key) || !routingReferencePattern.MatchString(effect.Kind) {
			problems = append(problems, fmt.Sprintf("operation %s has an effect with an invalid key or kind", o.ID))
		}
		if keys[effect.Key] {
			problems = append(problems, fmt.Sprintf("operation %s records effect %s twice", o.ID, effect.Key))
		}
		keys[effect.Key] = true
		switch effect.State {
		case EffectIntended, EffectPerformed, EffectAbsent:
		default:
			problems = append(problems, fmt.Sprintf("operation %s effect %s state %q is unknown", o.ID, effect.Key, effect.State))
		}
	}
	if o.Reconciling != nil && o.Reconciling.Attempt != "" && !routingIDPattern.MatchString(o.Reconciling.Attempt) {
		problems = append(problems, fmt.Sprintf("operation %s waits on an invalid attempt", o.ID))
	}
	return problems
}
