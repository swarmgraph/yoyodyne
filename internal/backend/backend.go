package backend

import (
	"context"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Capabilities is what a backend can do, stated by the backend rather than
// assumed of it. The yaml spelling is here because a user-supplied provider
// declares its own capabilities in configuration, and a capability nobody
// declared is one the harness must not assume: an unsupported combination is
// refused where the configuration is validated, before any work is assigned.
type Capabilities struct {
	StructuredEvents  bool `yaml:"structured_events" json:"structured_events"`
	SessionResumption bool `yaml:"session_resumption" json:"session_resumption"`
	StructuredOutput  bool `yaml:"structured_output" json:"structured_output"`
	ToolControl       bool `yaml:"tool_control" json:"tool_control"`
	LocalAuth         bool `yaml:"local_auth" json:"local_auth"`
}

// Availability is what a provider's executable said about itself. Installed is
// false only for an executable that was not there to ask; one that was there and
// did not answer -- it timed out, it was cancelled, it exited nonzero -- is an
// error from CheckAvailability saying which, and never Installed false. See
// availability.go for why the two are kept apart.
type Availability struct {
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"`
	APIProvider   string `json:"api_provider,omitempty"`
	// Missing says what was looked for and where, when Installed is false: the
	// executable's name and the PATH it was not found on. It is empty whenever
	// Installed is true, and for an adapter that does not say.
	Missing string `json:"missing,omitempty"`
}

// RunRequest is one provider invocation the harness asks for.
//
// There is deliberately no session mode on it. A provider that has interactive
// modes has one for planning, and Claude Code's puts that layer's own workflow
// into the session's system prompt: do not execute yet, write a plan file,
// launch planning agents, finish by declaring the plan. That reaches a
// harness-invoked role on top of a role contract saying the opposite -- a
// reviewer told to plan, or worse, a developer told not to edit. A reviewer read
// exactly that on 2026-08-30 in run-fe0ad8461100ca399c4d2dee371afd53 and
// reported it.
//
// So the mode is a function of the role's posture, decided by the adapter that
// knows the provider's spelling of it, and there is no field here for a caller
// to name one. What a role's session is is settled by which role it is, and no
// caller is in a position to say otherwise.
type RunRequest struct {
	RunID            string
	Role             domain.AgentRole
	WorkingDirectory string
	// RepositoryRoot is the harness's checkout, independent of the developer's
	// mutable worktree. Adapters use it to confine cache and scratch grants.
	// Empty uses WorkingDirectory for standalone invocations.
	RepositoryRoot string
	Prompt         string
	SystemPrompt   string
	SessionID      string
	Model          string
	// Effort is the effort level this invocation asks the provider for, and
	// resolved to the model default for Codex when the agent configured none.
	// Claude retains its resolution of an omitted level. A failover or a version fallback keeps it: the level is the
	// agent's, and a substitution moves the model rather than how hard the model
	// is asked to think. The one exception is a failover that crosses onto a
	// provider accepting no level, which is asked with none. See effort.go.
	Effort string
	// AllowedTools narrows tools where the adapter supports a named tool list.
	// An empty list disables tools on Claude Code; Codex uses the role-derived
	// sandbox instead and refuses any nonempty list. Empty is not a portable
	// request for a tool-free session.
	AllowedTools []string
	// AccountAlias is the provider account this invocation is made under, and
	// AccountConfigDir is where that account's authentication lives on this
	// machine. The alias is the name a record says the invocation by; the
	// directory is how the provider is actually reached, and an empty one is the
	// machine's own provider home — which is what a single-account installation
	// has always used and still does.
	//
	// They are on the request rather than on the backend value because the
	// account belongs to the run rather than to the adapter: one process serves
	// runs affined to different accounts, and a backend built for one of them
	// would have to be rebuilt to serve the next. Nothing here is a credential —
	// the directory names where the provider keeps its own, and the harness never
	// reads what is in it.
	//
	// An adapter that has no way to be pointed at one account's authentication
	// ignores both, which is the honest answer for a provider whose credentials
	// are not per-directory. What the record says is still the alias, because
	// attribution is the harness's and not the provider's.
	AccountAlias     string
	AccountConfigDir string
	// Timeout is the total budget for the invocation and IdleTimeout is how long
	// it may go without emitting an event. They answer different questions -- is
	// this run worth continuing, and is it doing anything at all -- so a backend
	// applies both rather than letting either stand in for the other. Zero means
	// the backend's default for each.
	Timeout     time.Duration
	IdleTimeout time.Duration
	// AfterReplyTimeout is how long a process still running after the provider
	// has written its final reply is waited for before it is ended, and zero is
	// the backend's default. A final reply ends the turn, so what keeps the
	// process alive after it — work the agent started in the background — is
	// never read as a provider gone silent: it is waited out to this bound, and
	// the result's Process.AfterReply says whether it ended on its own or was
	// ended.
	AfterReplyTimeout time.Duration
	// AfterReplyWaiting is called once, when the process is still running a
	// short grace after its final reply, so a caller can record that the run is
	// waiting on background processes rather than on the provider. It is
	// optional and decides nothing.
	AfterReplyWaiting func(execution.AfterReply)
	LastSequence      uint64
	RedactValues      []string
	EventSink         func(execution.Event) error
	// ReplySink receives the agent's prose as the provider produces it, so a
	// caller with somebody watching can show a reply forming rather than holding
	// it until the invocation is over. It is optional and it decides nothing:
	// the same text reaches the event stream and the result whether or not
	// anybody is listening, so a run watched and a run unwatched record and
	// return byte for byte the same thing.
	//
	// A backend calls it only with text it has already redacted and already
	// recorded, so what a watcher can be shown is bounded by what the durable
	// record holds. Fragments are whatever size the provider reports; a caller
	// that shows them has to cope with a reply that stops part way, because an
	// invocation that fails is one whose fragments were the start of an answer
	// nobody finished.
	ReplySink func(fragment string)
}

// UsageLimit is a provider's report that a usage limit is exhausted. It is
// deliberately not a failure: the work was never judged, only declined for want
// of capacity, so what it calls for is a wait rather than a failure record. A
// transient throttle never produces one of these — the provider CLI retries
// those itself and reports them as its own api_retry event.
type UsageLimit struct {
	// Kind is the provider's own name for the exhausted limit, carried as
	// evidence rather than interpreted by the harness.
	Kind string
	// ResetsAt is when the provider said the limit resets. It is zero when the
	// provider named no usable reset time, which is not a wait a caller may
	// guess at.
	ResetsAt time.Time
}

// ServerOverload is a provider's report that its own servers could not serve the
// attempt. Like UsageLimit it is deliberately not a failure: the work was never
// judged, only refused, and the provider's own message says the condition is
// temporary. It names no reset time, because a server under load never quotes
// one, so what it calls for is a short wait and another attempt rather than a
// deadline to sleep on.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A transient server refusal is a fourth answer that contract has to name,
// beside the ordinary retry a provider takes itself, the limit that will lift,
// and the refusal that will not. It is not a variant of any of them: nothing
// about the account is exhausted, so it is not a usage limit; the provider has
// already stopped retrying by the time it says this, so it is not an ordinary
// retry; and it is explicitly temporary, so it is not a refusal that stands.
// What makes it a distinct answer operationally is that it carries no reset
// time and never will, so the contract cannot fold it into the unknown-reset
// case: that case polls on the interval an exhausted limit deserves, and an
// overload wants one two orders of magnitude shorter.
//
// The evidence, so a plugin author has the shape rather than the assertion. Two
// runs on 2026-08-18, run-ff3c59bff086d6ac16dbf5101778843d and
// run-19dc9dff153e1eb89a2470f78f02f240, each failed a whole run instantly on a
// terminal result identical apart from the session: terminal_reason "api_error"
// and the text
//
//	API Error: 529 Overloaded. This is a server-side issue, usually temporary —
//	try again in a moment. If it persists, check https://status.claude.com.
//
// Both had already exhausted the provider CLI's own ten api_retry attempts on
// error "overloaded" before emitting it. That is Claude Code's spelling of the
// answer and belongs behind the contract rather than in it; how the Claude Code
// adapter reads it is in that adapter's parser. What generalizes is the answer
// itself, and that the harness — never the plugin — decides how long to wait
// for it and against which budget.
type ServerOverload struct {
	// Detail is the provider's own message, carried as evidence rather than
	// interpreted by the harness.
	Detail string
}

// TransientFailure is a provider invocation that died of something that judged
// nothing about the work: the API answered with an error the provider's own
// retries did not outlast, or the connection carrying the response went away
// before the invocation reached a terminal at all.
//
// It is not a refusal the way UsageLimit and ServerOverload are. Those two say
// in advance that the attempt was never served and name the condition; this one
// is the provider ending an invocation badly with nothing to say about why it
// would go better next time. What makes it the same kind of answer operationally
// is what it leaves behind: a change part-made in a worktree and a session that
// can still be continued, and no verdict on any of it. So it asks the harness to
// ask again against a budget it keeps, rather than to wait on a clock it does
// not have.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A transient death is a fifth answer that contract has to name. It is not the
// ordinary retry a provider takes itself, because the provider has already
// stopped by the time it says this; not a usage limit, because nothing about the
// account is exhausted; not an overload, because no condition is named that will
// lift; and not a refusal that stands, because the same request may well be
// served on the next attempt. A contract that folded it into the last of those
// would fail a whole run on weather, which is what this harness did until
// yoyodyne-ifd.101.
//
// The evidence, so a plugin author has the shape rather than the assertion. Run
// run-80f99e14210681e07bb07722f1b91483, developing yoyodyne-ifd.68.2 on
// 2026-08-19, ended on terminal_reason "api_error" with the text
//
//	API Error: Connection closed mid-response. The response above may be
//	incomplete.
//
// which names no HTTP status because nothing answered — the transport went away
// mid-reply. An earlier run, run-77cd11f0f96309286ddf3d2d2449ca26, recorded the
// bare category "api_error" and nothing else, because its record predates the
// harness keeping the provider's message beside it. That it can no longer be told
// apart from any other member of the category is part of the point: the category
// alone does not say what happened, so a harness that reads every member of it as
// a judgement of the work is guessing.
type TransientFailure struct {
	// Detail is the provider's own account of the death, carried as evidence
	// rather than interpreted by the harness. It is bounded, because it is what a
	// durable failure record and a blocker on a work item both keep.
	Detail string
}

// ModelUnavailable is a provider's report that it has not got the model the
// attempt asked for. Like UsageLimit and ServerOverload it says the work was
// never judged, and unlike either of them what it asks for is neither a wait nor
// another attempt at the same thing: the selector is what was refused, so the
// answer is to ask for a model the provider does have.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A model the provider has not got is a seventh answer that contract has to name.
// It is not a usage limit, because nothing about the account is exhausted and no
// reset time is ever quoted; not an overload, because no condition is named that
// will lift; not a transient death, because relaunching the identical request
// earns the identical answer however many attempts there are; and folding it
// into a refusal that stands would throw away the one thing that makes it
// actionable — that a different selector is not the same request.
//
// It is what makes an optional pinned model version safe to name. An agent
// pinned to an exact provider identifier is pinned to something the provider can
// retire, and a pin the provider has stopped serving must fall back to the
// family it belongs to rather than stop the agent. That fallback needs to know
// which failure was about the model, which is this answer and nothing else.
type ModelUnavailable struct {
	// Detail is the provider's own words about the model it would not serve,
	// carried as evidence rather than interpreted by the harness.
	Detail string
}

// ProviderOutage is a provider's report that it is answering nobody: the account
// the attempt was made under is not logged in, or nothing reaches its API at
// all. Like UsageLimit it is deliberately not a failure — the work was never
// judged — and unlike UsageLimit it names no reset, because neither a login nor
// a network quotes one.
//
// What it asks for is the one response none of the other answers gives: a wait
// that spends nothing. No relaunch is counted against it, no repair attempt is
// charged, and no blocker is recorded, because no attempt of the same request
// goes differently until a person logs in or the network returns, and every
// budget the harness keeps is a budget for something a run can do something
// about. The wait ends when the provider answers again, which the harness finds
// by asking, and nothing else ends it.
//
// The evidence. From 2026-09-17 18:17 local the Claude Code login on the
// operator's machine had expired. Every dispatch was refused at the availability
// check; the runs already going died on
//
//	API Error: Can't reach the API server
//
// spent their relaunch budgets, and were recorded as blocked; three of them in a
// row tripped the intake brake, whose remedy — `yoyo release` — was the wrong
// one; every recurring sweep pass recorded 0 turns; and the operator's
// maintenance job restarted the watch 158 times. Nothing told him. He learned by
// asking, three days later.
type ProviderOutage struct {
	// Cause is which of the two it is, in the domain's own vocabulary, because it
	// decides what the operator is told to do.
	Cause domain.ProviderOutageCause
	// Detail is the provider's own words, carried as evidence rather than
	// interpreted by the harness.
	Detail string
	// Channel is where the provider said it: on the terminal of its stream, or
	// on its process's stderr because it refused before writing a terminal at
	// all. It is evidence for the record rather than anything the harness acts
	// on — the wait is the same either way — and it is what lets a reader of a
	// run that waited tell which of the two shapes the provider produced.
	Channel domain.ProviderChannel
}

// RunResult is what one provider invocation is worth: the invocation's own
// terminal, and nothing nested inside it.
//
// For yoyodyne-ifd.32, which turns this package into a plugin contract.
//
// A provider whose agents can spawn agents emits results that are not the run's.
// Claude Code spells this as a second envelope of the terminal type carrying
// neither a terminal reason nor result text — run
// run-841f5ee1866addb533c02a30e67f001a, sequence 1186, is the recorded specimen,
// and how that adapter tells the two apart is in its parser. What generalizes is
// that the terminal cannot be identified by envelope type or by arrival order,
// because a nested agent may finish before the parent and one that finishes
// after is not a duplicate; a plugin has to say which result is the
// invocation's, and the contract has to make that answerable rather than assume
// exactly one result arrives.
type RunResult struct {
	Backend domain.Backend
	// AdapterVersion is the compiled adapter that reached the provider, said by
	// that adapter rather than assumed of the backend name beside it. The two are
	// separate facts: a provider a project declared is reached by an adapter this
	// build ships, so the backend says which provider and this says which harness
	// code read what it said. Together with the account and the model it is the
	// endpoint identity a record keeps -- see Endpoint.
	//
	// It is empty on a result no adapter got far enough to build, which is the
	// same absence ResolvedModel carries and means the same thing: nobody is
	// guessing on the record's behalf.
	AdapterVersion string
	SessionID      string
	// ResolvedModel is the model the provider reported actually serving the
	// invocation. A requested selector may be a floating family alias, so the
	// resolved identifier is the only durable evidence of what really ran.
	ResolvedModel string
	// ResolvedEffort is what the stream reported, never inferred from the request.
	// EffortReported is false when the provider omitted it.
	ResolvedEffort string
	EffortReported bool
	FinalText      string
	IsError        bool
	CostUSD        float64
	// CostReported says the provider actually told the harness what this
	// invocation cost. It is separate from CostUSD because a float has no way to
	// say it was never set: an invocation the provider ended without pricing and
	// one it priced at nothing are the same zero, and they are opposite facts to
	// anything adding costs up. A plugin sets it exactly when the provider named
	// an amount, whatever the amount was and however the invocation ended.
	CostReported bool
	Usage        []byte
	Process      execution.ProcessResult
	LastEvent    uint64
	StopReason   string
	// UsageLimit is set when the provider reported an exhausted usage limit
	// during this invocation. It is separate from IsError because the two call
	// for opposite responses: a failure ends the run, an exhausted limit asks
	// the caller to wait and ask again.
	UsageLimit *UsageLimit
	// ServerOverload is set when the invocation ended because the provider's
	// servers were transiently unable to serve it. It travels beside IsError
	// rather than instead of it — the provider does report this as a terminal
	// error — and it asks the same thing of the caller an exhausted limit does:
	// wait and ask again rather than end the run.
	ServerOverload *ServerOverload
	// TransientFailure is set when the invocation died of something that judged
	// nothing about the work and may not happen again. It travels beside IsError
	// like an overload does, and it is never set alongside one: an overload is
	// the transient death the caller already has a wait for, so a backend that
	// reported both would leave which answer a run took depending on the order
	// the caller read them.
	TransientFailure *TransientFailure
	// ModelUnavailable is set when the provider refused the attempt because it
	// has not got the model the request named. It travels beside IsError like the
	// two above, and it is separate from them because it is the one refusal the
	// caller can answer by changing the request rather than by waiting: a pinned
	// version the provider has retired falls back to its family on this and on
	// nothing else.
	ModelUnavailable *ModelUnavailable
	// ProviderOutage is set when the provider refused the attempt because nobody
	// is logged into it or nobody can reach it. It travels beside IsError like
	// the three above, and it is never set alongside an overload or a transient
	// failure: it is the one wait that spends nothing, and a result that also
	// asked for a relaunch would spend exactly what this exists not to.
	ProviderOutage *ProviderOutage
}

// maxFailureDetailBytes bounds the provider's own words in a described failure.
// What the bound cuts is the tail of a message; nothing that identifies the
// failure is at the end of one, and the whole of it is in the invocation's event
// log either way.
const maxFailureDetailBytes = 512

// ServedCleanly reports an invocation the provider genuinely served: it ended
// without error, its process succeeded, and nothing on it reports a refusal —
// no usage limit, no overload, no outage, not even one reported beside an
// answer. It is the one result that is evidence the window on the model it
// asked for is open. A death, a malformed stream, a terminal api_error the
// dialect could not classify, and a limit the provider is enforcing all end
// otherwise, and a window read as open on one of those would be read as open
// on a guess.
func (r RunResult) ServedCleanly() bool {
	return !r.IsError && r.Process.Status == execution.ProcessSucceeded &&
		r.UsageLimit == nil && r.ServerOverload == nil && r.ProviderOutage == nil
}

// DescribeFailure says why a provider ended an invocation badly, in its own name
// for the ending and its own words about it. It lives on the result rather than
// at any one caller because every role's invocation dies the same way and the
// description of it outlives them all: a developer's death becomes the run's
// durable failure, a reviewer's ends the run that asked for it, and a
// conversation turn's is what an operator is shown.
//
// The name alone is a category -- `api_error` covers a transient 529 and a
// refused request alike -- and it is what a run's durable failure keeps, so a
// record carrying nothing else leaves whoever reads it afterwards with no idea
// which of them happened. The message is added when it says something the
// category does not, so a provider that only names the category still reads as
// one reason rather than as the same words twice.
func (r RunResult) DescribeFailure() string {
	return DescribeFailure(r.StopReason, r.FinalText)
}

// DescribeFailure is the same description built from the two pieces alone, for a
// dialect that has the provider's reason and its message but no result to read
// them off: the contract's answers are decided from an event, and the event is
// what a dialect is handed.
func DescribeFailure(stopReason, finalText string) string {
	reason := strings.TrimSpace(stopReason)
	detail := boundFailureDetail(finalText)
	switch {
	case reason == "" && detail == "":
		return "unknown provider failure"
	case reason == "" || reason == detail:
		return firstOf(reason, detail)
	case detail == "" || strings.Contains(detail, reason):
		return firstOf(detail, reason)
	default:
		return reason + ": " + detail
	}
}

// boundFailureDetail folds the provider's message into one bounded line, so a
// final reply that runs to pages becomes a reason somebody can read rather than
// the body of a work item note.
func boundFailureDetail(detail string) string {
	return oneline.Fold(detail, maxFailureDetailBytes)
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type Backend interface {
	CheckAvailability(ctx context.Context) (Availability, error)
	Capabilities() Capabilities
	Run(ctx context.Context, request RunRequest) (RunResult, error)
}
