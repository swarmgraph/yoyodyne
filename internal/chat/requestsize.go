package chat

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
)

// requestBounded sits immediately outside an endpoint's meter, after failover
// has selected it and rebuilt any context. Each actual attempt is still metered
// separately. No size check is made against a different endpoint's bound.
type requestBounded struct {
	session      *Session
	adapter      Backend
	provider     modelfailover.Invoker
	savingMemory bool
}

func (b requestBounded) Run(ctx context.Context, request backend.RunRequest) (result backend.RunResult, err error) {
	s := b.session
	// Replacing or shortening a session can append events after the provider's
	// last event. Even when rebuilding fails, the caller must keep that position.
	defer func() { result.LastEvent = max(result.LastEvent, s.state.LastSequence) }()
	prepared, err := s.fitRequest(b.adapter, request)
	if err != nil {
		return backend.RunResult{LastEvent: s.state.LastSequence}, err
	}
	request = prepared
	s.sentRequest = &request
	result, err = b.provider.Run(ctx, request)
	if !requestRejectedForSize(result, err) {
		return result, err
	}
	// A provider can change its limit or count more than the adapter can see.
	// Retry once, dropping old conversation content rather than any of this
	// turn's instructions or evidence. A second rejection goes back unchanged.
	s.state.LastSequence = max(s.state.LastSequence, request.LastSequence, result.LastEvent)
	request.LastSequence = s.state.LastSequence
	before := request.Prompt
	if request.SessionID != "" {
		var rebuildErr error
		request, rebuildErr = s.replaceSession(request, backend.Endpoint{Provider: result.Backend,
			AccountAlias: request.AccountAlias, Model: request.Model}, request.SessionID, sizeRefusalDetail(result, err))
		if rebuildErr != nil {
			return result, rebuildErr
		}
		s.compacting = true
	}
	smaller, ok, shortenErr := s.shortenRequest(request)
	if shortenErr != nil {
		return backend.RunResult{LastEvent: s.state.LastSequence}, shortenErr
	}
	if !ok && request.Prompt == before {
		return result, err
	}
	if ok {
		request = smaller
	}
	if recordErr := s.emit(execution.EventSessionCompacted, map[string]any{
		"reason": "request_size_retry", "request_bytes": len(before), "saving_memory": b.savingMemory,
		"rebuilt_bytes": len(request.Prompt), "provider_detail": singleLine(sizeRefusalDetail(result, err), maxTrackerFailureBytes),
	}); recordErr != nil {
		return backend.RunResult{LastEvent: s.state.LastSequence}, recordErr
	}
	request.LastSequence = s.state.LastSequence
	request, fitErr := s.fitRequest(b.adapter, request)
	if fitErr != nil {
		return backend.RunResult{LastEvent: s.state.LastSequence}, fitErr
	}
	if hold, held, holdErr := s.heldByOperator(); holdErr != nil || held {
		if holdErr != nil {
			return backend.RunResult{LastEvent: s.state.LastSequence}, holdErr
		}
		return backend.RunResult{LastEvent: s.state.LastSequence}, &OperatorHoldError{Hold: hold}
	}
	s.stream.interrupted()
	s.sentRequest = &request
	return b.provider.Run(ctx, request)
}

// fitRequest measures what this adapter will send, with five percent reserved
// for endpoint framing or a slightly different character count. Only recorded
// conversation messages can be removed: the briefing, memory, role contract,
// pending results and current turn stay intact. A fixed input that cannot fit
// is refused here without starting the provider.
func (s *Session) fitRequest(adapter Backend, request backend.RunRequest) (backend.RunRequest, error) {
	sizer, ok := adapter.(backend.RequestSizer)
	if !ok {
		return request, nil
	}
	size, limit := sizer.RequestSize(request)
	if limit <= 0 {
		return request, nil
	}
	budget := limit - limit/20
	before := size
	for size > budget {
		smaller, changed, err := s.shortenRequest(request)
		if err != nil {
			return request, err
		}
		if !changed {
			return request, errors.Join(ErrTurnUnassembled, &backend.RequestTooLarge{Bytes: size, LimitBytes: budget})
		}
		request = smaller
		size, _ = sizer.RequestSize(request)
	}
	if size != before {
		s.state.LastSequence = max(s.state.LastSequence, request.LastSequence)
		if err := s.emit(execution.EventSessionCompacted, map[string]any{
			"reason": "request_size", "request_bytes": before, "limit_bytes": limit,
			"budget_bytes": budget, "rebuilt_bytes": size,
		}); err != nil {
			return request, err
		}
		request.LastSequence = s.state.LastSequence
	}
	return request, nil
}

// shortenRequest uses the existing reconstruction, oldest messages first. Keep
// lowering its message budget until it changes, since a sparse history may fit
// into several successively smaller budgets. A negative override means no old
// messages; zero retains the existing default for turns not shortened here.
func (s *Session) shortenRequest(request backend.RunRequest) (backend.RunRequest, bool, error) {
	from := s.rebuiltFrom
	if from == nil || !strings.HasPrefix(request.Prompt, rebuiltContextHeader) || !strings.HasSuffix(request.Prompt, from.prompt) {
		return request, false, nil
	}
	for s.rebuildMessageBudget() > 0 {
		s.rebuiltMessageBytes = s.rebuildMessageBudget() / 2
		if s.rebuiltMessageBytes == 0 {
			s.rebuiltMessageBytes = -1
		}
		prompt, err := s.rebuiltPrompt(from.systemPrompt, from.prompt, from.why)
		if err != nil {
			return request, false, err
		}
		if prompt != request.Prompt {
			request.Prompt = prompt
			return request, true, nil
		}
	}
	return request, false, nil
}

var requestSizeRefusal = regexp.MustCompile(`(?i)input_too_large|input exceeds the maximum length|request_too_large|request exceeds the maximum size`)

func requestRejectedForSize(result backend.RunResult, err error) bool {
	var tooLarge *backend.RequestTooLarge
	if errors.As(err, &tooLarge) {
		return true
	}
	if err != nil && requestSizeRefusal.MatchString(err.Error()) {
		return true
	}
	return (err != nil || result.IsError) && requestSizeRefusal.MatchString(result.DescribeFailure())
}

func sizeRefusalDetail(result backend.RunResult, err error) string {
	if err != nil {
		return err.Error()
	}
	return result.DescribeFailure()
}
