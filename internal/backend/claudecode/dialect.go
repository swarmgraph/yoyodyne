package claudecode

// Claude Code's operational dialect: how this provider says it is throttled, how
// it says it is exhausted, how to tell those apart, and when to come back.
//
// All of it used to be spread through the parser, which is what made it look
// general when it never was. It is one implementation of the provider contract
// in internal/backend now, and nothing above it special-cases this provider: the
// parser hands the dialect an event, the dialect answers with one of the
// contract's answers, and the harness decides what waiting -- if any -- that
// answer earns. The dialect states no duration anywhere, which is the property
// that keeps a provider from being able to spend an account.

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// The provider's own names for the events this dialect reads. Everything else in
// the stream is prose, tool calls, and accounting, and the dialect says nothing
// about any of it.
const (
	rateLimitEventType = "rate_limit_event"
	systemEventType    = "system"
	// apiRetrySubtype is the provider's own transient retry, which it is
	// handling itself. The attempt has not ended and nothing about the account
	// is exhausted, so it is evidence rather than a reason for the harness to
	// wait.
	apiRetrySubtype = "api_retry"
)

// terminalAPIError is the provider's own terminal reason for an invocation that
// ended on an error from the API rather than on anything the agent did or the
// harness decided.
const terminalAPIError = "api_error"

// overloadedStatus is the HTTP status the provider's API answers with when it is
// transiently unable to serve a request at all.
const overloadedStatus = "529"

// notFoundStatus is the HTTP status the provider's API answers with when the
// request names something it has not got. On a message request there is exactly
// one such thing — the model — so a not-found here is a selector this provider
// will not serve rather than a missing endpoint or a bad path.
//
// Provenance, stated because it is weaker than the overload's. No recorded run
// carries one: a model this account cannot reach has never been asked for, since
// every configured selector until now has been a family alias the provider
// always resolves. What it is read from instead is the API's own not-found
// answer, whose body names the model — `{"type":"error","error":{"type":
// "not_found_error","message":"model: …"}}` — and which the CLI puts on its
// terminal API-error message the same way it puts the overload's.
//
// The match is narrow in both halves for that reason: the status has to be the
// not-found one and the message has to name a model. A message this version does
// not recognize is left where it was — a refusal that stands, failing the turn
// exactly as it does today — rather than becoming a silent move to another
// model. The first real occurrence recorded is the evidence that should replace
// this comment.
//
// What this cannot reach is a CLI that refuses the selector before it calls the
// API at all. That ends the process without a terminal envelope, so the only
// place the refusal could be read is the process's plain output — which this
// dialect is now handed, but reads only for the two refusals below that a
// person or the network ends. A pinned version rejected up front therefore
// still fails the turn rather than falling back; the case for reading it off
// plain output is a recorded occurrence.
const notFoundStatus = "404"

// modelNotFound is the API's own name for the not-found it answers with, and the
// prefix its body puts in front of the selector. Either is enough: the CLI may
// carry the body whole or quote only its message, and both name the model.
var modelNotFound = regexp.MustCompile(`(?i)not_found_error|\bmodel:`)

// notAuthenticated is this provider saying it will not accept the account the
// invocation was made under: the CLI's own words for an account that is not
// logged in, beside the API's names for the same refusal, and the status the API
// answers it with.
//
// It is the one place the leftovers below are deliberately narrowed, so the
// reason is worth stating. The trade those leftovers make — being wrong about a
// transient death costs one more invocation, being wrong about a refusal that
// stands costs the whole run — holds for weather and not for this: relaunching
// into an account the provider will not accept spends a run's whole relaunch
// budget on an answer that cannot change, and no attempt of it is any different
// from the first. What it is read as is the contract's own answer for a login
// nobody has renewed, which the harness waits out spending nothing rather than
// failing the run over; the status form is matched here as well so that one
// provider reporting one condition two ways earns one answer.
//
// It is read off a terminal API error, like every other match here, and off
// the process's plain output — stderr, or stdout where what it wrote there was
// not an envelope — when the stream ended without a terminal at all. An agent's
// own prose about being logged out is left where it was either way: what the
// provider said about the request is on that envelope or in that plain output
// and nowhere else.
//
// The words are the CLI's own. Claude Code 2.1.276 titles the refusals it
// makes over an account it will not accept "Not logged in", "Login expired",
// "OAuth token revoked", "Invalid API key", and "Invalid auth token", and pairs
// the ones a login fixes with "Please run /login". One recorded shape: a home
// nobody has logged into, asked on 2026-09-19 with `-p --output-format
// stream-json`, ends on a terminal envelope with terminal_reason "api_error"
// and the result text "Not logged in · Please run /login", is_error true,
// subtype "success", and nothing on stderr. The remedy is the same whichever
// title it arrives under, which is why one answer covers them all.
//
// A session whose login lapses partway through ends on a different title:
// "Failed to authenticate: OAuth session expired and could not be refreshed",
// with no remedy beside it. It is the same condition with the same remedy, and
// read as a death it spends a run's relaunches on a login nobody renewed.
var notAuthenticated = regexp.MustCompile(`(?i)not logged in|login expired|oauth (?:token|session) (?:expired|revoked)|failed to authenticate|please run /login|invalid api key|invalid auth token|authentication_error|\bunauthorized\b`)

// unauthenticatedStatus is the HTTP status the provider's API answers with when
// it will not accept the credentials the request carried.
const unauthenticatedStatus = "401"

// unreachable is this provider saying nothing answered at its API at all: the
// CLI's own words when the machine is offline or asleep, beside the transport
// errors Node reports when a name does not resolve or a connection is refused.
//
// It is narrower than the leftovers on purpose, and the reason is the reverse of
// the not-authenticated one above. A connection that dropped mid-reply reached
// the provider and may well not drop again, so relaunching it is right; nothing
// reaching the provider at all is not answered by asking twice more against a
// budget and then blocking, which is what happened to at least two runs in the
// 2026-09-15..18 outage. What it is read as is a wait that spends nothing, so a
// message this version does not recognize keeps failing over into the relaunch
// path rather than into a wait nobody can justify.
//
// The specimen is the CLI's own: "API Error: Can't reach the API server",
// recorded on the runs that died during that outage, which quotes no status
// because nothing answered.
var unreachable = regexp.MustCompile(`(?i)can(?:'|’|no)?t reach the api|unable to reach the api|network is unreachable|getaddrinfo|\benotfound\b|\beconnrefused\b|\behostunreach\b|\benetunreach\b|fetch failed`)

// clientErrorPrefix marks the API statuses that describe the request rather than
// the server's ability to serve it. A relaunch would put the identical request in
// front of the provider again and earn the identical refusal, so nothing in this
// class is transient — an exhausted key and a malformed request both stay what
// they are however many times they are asked.
const clientErrorPrefix = "4"

// apiErrorStatus reads the status out of the message the provider CLI writes on
// a terminal API error, which it puts at the front, before its own prose.
//
// Provenance. Two runs on 2026-08-18 — run-ff3c59bff086d6ac16dbf5101778843d and
// run-19dc9dff153e1eb89a2470f78f02f240 — recorded a terminal result byte for
// byte identical apart from the session, with terminal_reason "api_error" and
// this result text:
//
//	API Error: 529 Overloaded. This is a server-side issue, usually temporary —
//	try again in a moment. If it persists, check https://status.claude.com.
//
// Both had already exhausted the CLI's own ten api_retry attempts on the same
// condition, so a terminal result in this shape is what the provider says after
// it has finished retrying rather than instead of retrying.
//
// The match is deliberately narrow: only a terminal API error whose status is
// the overloaded one becomes a waitable refusal, so a message this version does
// not recognize fails the run exactly as it does today rather than becoming a
// wait nobody can justify.
var apiErrorStatus = regexp.MustCompile(`(?i)\bapi error\b\D{0,4}(\d{3})\b`)

// rateLimitRejected is the provider's name for a limit that is refusing work.
// Its other statuses describe a limit that is still serving, and the transient
// throttles the provider CLI retries by itself arrive separately as the system
// api_retry subtype, so neither is a reason for the harness to wait.
const rateLimitRejected = "rejected"

// rateLimitInfo names the fields of a rate_limit_event this dialect acts on. The
// provider sends more than this — utilization, overage accounting, the reason
// overage is unavailable — and all of it is preserved in the event stream; only
// these decide whether a run waits.
//
// Provenance of these names. No recorded rate_limit_event was available to read
// them off: this parser used to route the event to its default branch, so all
// twenty entries in the local run history had already had their payload
// discarded, and a hard limit could not be provoked on demand to record a fresh
// one. They were therefore read out of the shipped provider CLI itself —
// Claude Code 2.1.224, the emitted-message schema `$1v` and the payload schema
// it references, `L1v` — which declares:
//
//	type:           "rate_limit_event"
//	rate_limit_info: status ∈ {allowed, allowed_warning, rejected}   (required)
//	                 resetsAt?: int
//	                 rateLimitType? ∈ {five_hour, seven_day, seven_day_opus,
//	                                   seven_day_sonnet,
//	                                   seven_day_overage_included, overage}
//	                 utilization?, isUsingOverage?, overageStatus?,
//	                 overageResetsAt?, overageDisabledReason?, …
//
// resetTime reads resetsAt as whole Unix seconds because that CLI compares it as
// `resetsAt * 1000 <= Date.now()` and renders it as `resetsAt - Date.now()/1000`.
// exhausted excludes isUsingOverage because that CLI shows a hard limit only
// when overage is not already serving the request.
//
// That is a description of the provider, not a promise from it. Everything here
// degrades safely if a future version disagrees: an unreadable payload is
// recorded whole and never read as exhaustion, so the harness stops waiting
// rather than starts waiting wrongly. The first real exhausted limit this
// records is the evidence that should replace this comment.
type rateLimitInfo struct {
	Status        string `json:"status"`
	RateLimitType string `json:"rateLimitType"`
	// ResetsAt stays raw so that a reset time in a shape this version does not
	// expect cannot sink the decode of the whole payload. An exhausted limit
	// whose reset time is unreadable is still an exhausted limit, and it has to
	// reach the caller as one: what the harness refuses is guessing the wait, not
	// noticing the refusal.
	ResetsAt       json.RawMessage `json:"resetsAt"`
	IsUsingOverage bool            `json:"isUsingOverage"`
}

// exhausted reports a limit that actually stops work. A rejected primary limit
// with overage already in use is still being served, which is the provider's own
// rule for whether to show the user a hard limit or leave them working.
func (r rateLimitInfo) exhausted() bool {
	return r.Status == rateLimitRejected && !r.IsUsingOverage
}

// resetTime reads the instant the limit resets. The provider sends whole
// seconds since the Unix epoch; anything else is no reset time at all, and what
// the harness makes of that is the contract's to say rather than this dialect's.
func (r rateLimitInfo) resetTime() time.Time {
	var seconds int64
	if len(r.ResetsAt) == 0 || json.Unmarshal(r.ResetsAt, &seconds) != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// Dialect reads Claude Code. It is stateless and carries no clock, because
// nothing it answers depends on either: it says what the provider said, and
// every judgement about time belongs to the caller.
type Dialect struct{}

var _ backend.Dialect = Dialect{}

func (Dialect) Name() string { return domainBackend }

// Observe reports what one event from this provider says about the attempt.
//
// A terminal that succeeded is deliberately not an answer. It says nothing about
// capacity — the provider reports its limits on their own event, and re-reports
// them as they change — so reading a completed invocation as evidence that a
// limit has lifted would be this dialect inventing a fact the provider never
// stated.
func (Dialect) Observe(event backend.ProviderEvent) (backend.Observation, bool) {
	switch {
	case event.Channel.Plain():
		return observePlainOutput(event.Text)
	case event.Type == rateLimitEventType:
		return observeRateLimit(event.Payload)
	case event.Type == systemEventType && event.Subtype == apiRetrySubtype:
		return backend.Observation{Answer: backend.AnswerRetrying}, true
	case event.Terminal && event.Failed:
		return observeFailedTerminal(event)
	default:
		return backend.Observation{}, false
	}
}

// observePlainOutput reads what the process wrote as prose — to stderr, or to
// stdout in place of the envelopes it was asked for — which the adapter hands
// over one channel at a time and only when the stream ended without a terminal
// of its own. It reads two things off it and nothing else: the account this
// provider will not accept, and an API nothing reaches. Both are waits that
// spend nothing, and both are refusals a CLI can make before it has written a
// single envelope — which is the shape yoyodyne-ifd.377 could not see, and the
// one that would replay the 2026-09-17 stall through the gap it left: a
// process failure nobody classified relaunches into the same login, spends the
// budget, and blocks. The stdout form is the gap yoyodyne-ifd.393 left in turn:
// a refusal written there as plain text was a stream the parser could not
// decode, and it failed the invocation before stderr was ever read.
//
// Everything else the prose says is left where it was. A terminal is read into
// five answers because the provider named the ending and the status; prose
// names neither, so an overload, a refused request, or a transient death read
// off it would be a guess about diagnostics, and a process that died without a
// terminal for any other reason keeps being the process failure it always was.
// The match is on the CLI's own words rather than on any status, because a
// refusal made before the API is called quotes none. The two channels are read
// by one rule because the CLI chooses between them and the words are the same
// whichever it chose; which one it was is the event's fact, written onto the
// record by the caller.
func observePlainOutput(text string) (backend.Observation, bool) {
	switch {
	case notAuthenticated.MatchString(text):
		return backend.Observation{Answer: backend.AnswerUnauthenticated, Detail: backend.DescribeFailure("", text)}, true
	case unreachable.MatchString(text):
		return backend.Observation{Answer: backend.AnswerUnreachable, Detail: backend.DescribeFailure("", text)}, true
	default:
		return backend.Observation{}, false
	}
}

// observeRateLimit reads what the provider said about capacity. A payload this
// dialect cannot read says nothing: it must not be read as exhaustion, because
// the same event reports healthy utilization far more often and reading that as
// exhaustion would stop runs that have capacity.
func observeRateLimit(payload json.RawMessage) (backend.Observation, bool) {
	var info rateLimitInfo
	if len(payload) == 0 || json.Unmarshal(payload, &info) != nil {
		return backend.Observation{}, false
	}
	if !info.exhausted() {
		return backend.Observation{Answer: backend.AnswerServed}, true
	}
	return backend.Observation{
		Answer:   backend.AnswerLimitReached,
		Kind:     info.RateLimitType,
		ResetsAt: info.resetTime(),
	}, true
}

// observeFailedTerminal tells the ways this provider can end an invocation badly
// apart. A server overload is a wait; a not-found naming a model is a refusal
// about the selector, which the caller can answer by asking for another one; an
// account this provider will not accept is a login nobody has renewed, however
// it was spelled, and nothing answering at all is a provider nobody can reach —
// both are waits that spend nothing; any other status describing the request is
// a refusal that stands; and everything else left in the API-error category is a
// death that judged nothing about the work.
//
// The model case is read ahead of the general client-error one it is a member
// of, because it is the narrower reading of the same status and the general one
// would otherwise swallow it.
//
// The two are matched from opposite directions on purpose. The overload turns a
// failure into a wait, so a message this version does not recognize has to keep
// failing the run. The transient death turns a failure into another attempt
// against a budget, so the cost of being wrong is one more invocation and a
// blocker that arrives later than it might have — while the cost of missing a
// case is the whole run, which is what a person spent a week reconciling by
// hand. It is therefore the leftovers that are read as transient, and being
// narrow there is the safe direction only if the harness is content to keep
// failing on weather it has not seen yet, and it is not.
func observeFailedTerminal(event backend.ProviderEvent) (backend.Observation, bool) {
	described := backend.DescribeFailure(event.Subtype, event.Text)
	if event.Subtype != terminalAPIError {
		return backend.Observation{Answer: backend.AnswerRefused, Detail: described}, true
	}
	status := apiErrorStatus.FindStringSubmatch(event.Text)
	switch {
	case status != nil && status[1] == overloadedStatus:
		// The provider's own message is carried whole here rather than folded:
		// it is the only evidence of an overload, and it is short by
		// construction.
		return backend.Observation{Answer: backend.AnswerUnavailable, Detail: event.Text}, true
	case status != nil && status[1] == notFoundStatus && modelNotFound.MatchString(event.Text):
		return backend.Observation{Answer: backend.AnswerModelUnavailable, Detail: described}, true
	case notAuthenticated.MatchString(event.Text), status != nil && status[1] == unauthenticatedStatus:
		return backend.Observation{Answer: backend.AnswerUnauthenticated, Detail: described}, true
	case unreachable.MatchString(event.Text):
		return backend.Observation{Answer: backend.AnswerUnreachable, Detail: described}, true
	case status != nil && strings.HasPrefix(status[1], clientErrorPrefix):
		return backend.Observation{Answer: backend.AnswerRefused, Detail: described}, true
	default:
		// The shape of a connection that went away, among others: "Connection
		// closed mid-response" quotes no status because nothing answered.
		return backend.Observation{Answer: backend.AnswerInterrupted, Detail: described}, true
	}
}
