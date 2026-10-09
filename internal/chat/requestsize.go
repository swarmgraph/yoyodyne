package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
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
	if !requestRejectedForSize(result, err) || s.requestSizeRetried {
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
	s.requestSizeRetried = true
	s.sentRequest = &request
	return b.provider.Run(ctx, request)
}

// fitRequest measures what this adapter will send, with five percent reserved
// for endpoint framing or a slightly different character count. Old
// conversation messages are removed first, oldest first. Where none are left
// to remove, the briefing's sections give way in contextbundle.GiveWayOrder,
// and the role is told in the briefing which ones and how to read them. The
// role contract, memory, pending results, the current turn, and the briefing's
// fixed sections and standing goals stay intact. A turn those alone cannot fit
// is refused here, once, without starting the provider.
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
	var fitted *briefingFit
	for size > budget {
		smaller, changed, err := s.shortenRequest(request)
		if err != nil {
			return request, err
		}
		if changed {
			request = smaller
			size, _ = sizer.RequestSize(request)
			continue
		}
		shorter, fit, ok := s.fitBriefing(sizer, request, size, budget)
		if !ok {
			return request, errors.Join(ErrTurnUnassembled, &TurnTooLarge{
				RequestBytes: size, LimitBytes: limit, BudgetBytes: budget,
				InstructionsBytes: len(request.SystemPrompt), BriefingBytes: len(fit.text),
				TurnBytes: len(request.Prompt) - len(fit.full),
			})
		}
		request, fitted = shorter, &fit
		size, _ = sizer.RequestSize(request)
	}
	if size != before {
		s.state.LastSequence = max(s.state.LastSequence, request.LastSequence)
		payload := map[string]any{
			"reason": "request_size", "request_bytes": before, "limit_bytes": limit,
			"budget_bytes": budget, "rebuilt_bytes": size,
		}
		if fitted != nil {
			payload["briefing_bytes"] = len(fitted.full)
			payload["briefing_fitted_bytes"] = len(fitted.text)
			payload["briefing_sections"] = fitted.sections
		}
		if err := s.emit(execution.EventSessionCompacted, payload); err != nil {
			return request, err
		}
		request.LastSequence = s.state.LastSequence
	}
	return request, nil
}

// TurnTooLarge is a turn refused before sending because the parts of it that
// never give way are past what the endpoint accepts. It names each part's size,
// because what has to change is one of them and not the turn's luck.
type TurnTooLarge struct {
	RequestBytes      int
	LimitBytes        int
	BudgetBytes       int
	InstructionsBytes int
	BriefingBytes     int
	TurnBytes         int
}

func (e *TurnTooLarge) Error() string {
	return fmt.Sprintf("this turn was not sent: it is %d bytes as the provider would receive it, and the provider accepts at most %d, or %d once the harness keeps five percent spare. "+
		"Nothing left in it may be shortened: the role's own instructions are %d bytes, the briefing is %d bytes with every section that may give way already left out, "+
		"and this turn's evidence and message are %d bytes. None of those is cut to make a turn fit, so the turn is not tried again",
		e.RequestBytes, e.LimitBytes, e.BudgetBytes, e.InstructionsBytes, e.BriefingBytes, e.TurnBytes)
}

// Unwrap lets a caller matching the adapter's own refusal match this one.
func (e *TurnTooLarge) Unwrap() error {
	return &backend.RequestTooLarge{Bytes: e.RequestBytes, LimitBytes: e.BudgetBytes}
}

// briefingFit is the briefing a request carried and what it was fitted to.
type briefingFit struct {
	full     string
	text     string
	sections []contextbundle.FittedSection
}

// maxBriefingFitAttempts bounds how often a fitted briefing is measured again.
// An adapter can count escaped text as more than its bytes, so a first fit to
// the excess in bytes may still be over; each attempt takes off what is left.
const maxBriefingFitAttempts = 4

// fitBriefing shortens the briefing a request carries until the adapter
// measures the request within budget. It is false where the request carries no
// briefing or where the briefing's fixed sections alone do not fit; the fit is
// then the shortest briefing there is, for naming sizes.
func (s *Session) fitBriefing(sizer backend.RequestSizer, request backend.RunRequest, size, budget int) (backend.RunRequest, briefingFit, bool) {
	full := s.briefingIn(request.Prompt)
	if full == "" {
		return request, briefingFit{}, false
	}
	at := strings.Index(request.Prompt, full)
	target := len(full) - (size - budget)
	for attempt := 0; attempt < maxBriefingFitAttempts && target > 0; attempt++ {
		fit := contextbundle.FitProductContext(full, target)
		if !fit.Fits {
			break
		}
		candidate := request
		candidate.Prompt = request.Prompt[:at] + fit.Text + request.Prompt[at+len(full):]
		measured, _ := sizer.RequestSize(candidate)
		if measured <= budget {
			return candidate, briefingFit{full: full, text: fit.Text, sections: fit.Sections}, true
		}
		target -= measured - budget
	}
	return request, briefingFit{full: full, text: contextbundle.FitProductContext(full, 0).Text}, false
}

// briefingIn is the picture a request's prompt carries: the one this turn
// carries itself, or the one a rebuild put in front of it, which is trimmed.
// The longest that appears is the one fitted, so a picture is never mistaken
// for part of a longer one.
func (s *Session) briefingIn(prompt string) string {
	candidates := []string{s.options.Briefing.Text}
	if s.carried != nil {
		candidates = append(candidates, s.carried.Text)
	}
	if s.refresh != nil {
		candidates = append(candidates, s.refresh.briefing.Text)
	}
	found := ""
	for _, candidate := range candidates {
		for _, text := range []string{candidate, strings.TrimSpace(candidate)} {
			if len(text) > len(found) && strings.Contains(prompt, text) {
				found = text
			}
		}
	}
	return found
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
