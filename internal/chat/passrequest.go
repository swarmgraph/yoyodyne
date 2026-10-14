package chat

// The Lead Product Manager asking a program manager instance for a pass.
//
// "Passes on request" in docs/designs/program-manager.md: the operator asks
// with `yoyo agent pass`, the Lead Product Manager asks from her conversation
// with `pass.request`, and another role that wants a pass asks her. The block
// here records one request through the same store the command writes, and
// nothing else: the scheduler takes it at its next pull, under every rule a
// pass is taken under, so no conversation invokes an instance — the harness is
// the only invoker. The role is told exactly that, so it never says a pass
// happened that nobody has taken.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const passRequestFence = "```yoyodyne-pass-request"

// maxPassRequestBlockBytes bounds the block itself: an instance, an area, and
// up to ten paths.
const maxPassRequestBlockBytes = 4 << 10

// PassRequests is where a request for a program manager instance's pass is
// recorded. It is satisfied in the CLI over *runstate.PassRequestStore, which
// also refuses an agent that is not a configured program manager instance.
type PassRequests interface {
	Request(request runstate.PassRequest) (runstate.PassRequest, error)
}

// PassAsk is what one reply asked for: which instance, and what to look at.
type PassAsk struct {
	Agent string        `json:"agent"`
	Focus *PassAskFocus `json:"focus,omitempty"`
}

// PassAskFocus is the focus a reply names: one area, or repository paths.
type PassAskFocus struct {
	Area  string   `json:"area,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// PassRequestOutcome is what became of one ask: the request as recorded, or why
// nothing was.
type PassRequestOutcome struct {
	Ask      PassAsk               `json:"ask"`
	Recorded bool                  `json:"recorded"`
	Request  *runstate.PassRequest `json:"request,omitempty"`
	Failure  string                `json:"failure,omitempty"`
}

// PassRequestError reports a pass request block the harness could not read.
// Nothing in it was recorded, and nothing else about the turn is changed by it.
type PassRequestError struct {
	Err error
}

func (e *PassRequestError) Error() string {
	return "the reply carried a pass request block the harness cannot read: " + e.Err.Error()
}

func (e *PassRequestError) Unwrap() error { return e.Err }

// extractPassRequest takes the pass request block out of a reply, leaving its
// prose.
func extractPassRequest(reply string) (string, *PassAsk, error) {
	prose, payload, found, err := splitFencedBlock(reply, passRequestFence, "pass request")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	ask, err := decodePassRequest(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, &ask, nil
}

// decodePassRequest strictly decodes the block. Which instances exist and
// whether the focus is well formed are the store's to decide, and a refusal
// there is an outcome the role is told rather than an unreadable block.
func decodePassRequest(payload string) (PassAsk, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return PassAsk{}, errors.New("decode pass request: the pass request block is empty")
	}
	if len(trimmed) > maxPassRequestBlockBytes {
		return PassAsk{}, fmt.Errorf("decode pass request: block is %d bytes, limit is %d", len(trimmed), maxPassRequestBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var ask PassAsk
	if err := decoder.Decode(&ask); err != nil {
		return PassAsk{}, fmt.Errorf("decode pass request: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return PassAsk{}, errors.New("decode pass request: unexpected trailing content after the request")
	}
	return ask, nil
}

// performPassRequest records the one request a reply asked for, as the Lead
// Product Manager's. A refusal — an agent that is not a program manager
// instance, a focus naming both an area and paths, a store that would not take
// it — is an outcome the role is told, and never a failed turn.
func (s *Session) performPassRequest(ask PassAsk) PassRequestOutcome {
	outcome := PassRequestOutcome{Ask: ask}
	var focus runstate.PassFocus
	if ask.Focus != nil {
		built, err := runstate.NewPassFocus(ask.Focus.Area, ask.Focus.Paths)
		if err != nil {
			outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
			return outcome
		}
		focus = built
	}
	if s.options.PassRequests == nil {
		outcome.Failure = "no pass request store is wired to this conversation, so nothing was recorded"
		return outcome
	}
	id, err := runstate.NewPassRequestID()
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	recorded, err := s.options.PassRequests.Request(runstate.PassRequest{
		SchemaVersion:  runstate.PassRequestSchemaVersion,
		ProductID:      s.options.ProductID,
		ID:             id,
		Agent:          strings.TrimSpace(ask.Agent),
		AskedBy:        runstate.PassAskerProductManager,
		RequestedAt:    s.options.clock().Now(),
		Focus:          focus,
		ConversationID: s.state.ConversationID,
		Turn:           s.state.Turns,
	})
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	outcome.Recorded = true
	outcome.Request = &recorded
	return outcome
}

// renderPassRequestResult is what the role is told became of its request. A
// recorded request says, in as many words, that nothing has run yet, so the
// role never reports a pass as taken that nobody has taken.
func renderPassRequestResult(outcome PassRequestOutcome) string {
	var rendered strings.Builder
	rendered.WriteString("# Pass request\n\n")
	if !outcome.Recorded {
		fmt.Fprintf(&rendered, "Not recorded: %s\n\n", outcome.Failure)
		return rendered.String()
	}
	request := outcome.Request
	if request.JoinedTo != "" {
		fmt.Fprintf(&rendered, "Recorded as %s, added to the request %s that was already open for %s, with its focus: one pass answers both.", request.ID, request.JoinedTo, request.Agent)
	} else {
		fmt.Fprintf(&rendered, "Recorded as %s: a request for a pass of %s.", request.ID, request.Agent)
	}
	rendered.WriteString(" Nothing has run yet: the scheduler takes the request at its next pull, under the operator's pause and behind any turn already in flight on the instance's conversation, and a provider answering nobody leaves it waiting with that reason recorded on it. The instance's digest reaches you as its reports do. Do not say the pass has happened.\n\n")
	return rendered.String()
}

// reportPassRequest tells the operator what the role asked for.
func (s *Session) reportPassRequest(out io.Writer, reply Reply) {
	if reply.PassRequest == nil {
		return
	}
	title := RoleTitle(s.state.Role)
	outcome := *reply.PassRequest
	if outcome.Recorded {
		fmt.Fprintf(out, "the %s asked %s for a pass (%s); the scheduler takes it at its next pull\n\n", title, outcome.Request.Agent, outcome.Request.ID)
		return
	}
	fmt.Fprintf(out, "the %s's request for a pass of %q was not recorded: %s\n\n", title, outcome.Ask.Agent, outcome.Failure)
}

// passRequestContract is what a role holding pass.request is told about it.
const passRequestContract = `# Asking a program manager for a pass

A program manager instance passes over its lane on its own triggers, and one configured with none passes only when asked. You may ask any instance for a pass, by ending your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-pass-request
{"agent":"technical-health-pm","focus":{"area":"test quality"}}
` + "```" + `

"agent" is the instance's configured name. "focus" is optional: one area its remit lists as "area", or up to ten repository paths as "paths", never both; the pass inspects it in place of the next area in the instance's rotation, and without one it takes that next area. Say why you are asking in your prose: the harness records the request as yours, with this conversation and turn, so your reasons are read from here. At most one request per instance is open: a request made while one is open is added to it, with its focus, and one pass answers both. Nothing runs when you ask: the scheduler takes the request at its next pull, as it takes any pass, and the instance's digest reaches you as its reports do. Another role that wants a pass asks you for one; you decide whether to make the request. So say that a pass was asked for, never that it happened.`
