package claudecode

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Claude Code's dialect is one implementation of the provider contract, and
// nothing above it special-cases this provider. What this states is that every
// reading the adapter used to hold privately is now an answer the contract
// names, reachable by handing the dialect an event and nothing else.
func TestTheClaudeDialectAnswersInTheContractsTerms(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 8, 27, 20, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		event backend.ProviderEvent
		said  bool
		want  backend.Observation
	}{
		{
			name: "a limit refusing work names when it lifts",
			event: backend.ProviderEvent{
				Type:    rateLimitEventType,
				Payload: json.RawMessage(`{"status":"rejected","rateLimitType":"five_hour","resetsAt":` + unixSeconds(resetsAt) + `}`),
			},
			said: true,
			want: backend.Observation{Answer: backend.AnswerLimitReached, Kind: "five_hour", ResetsAt: resetsAt},
		},
		{
			// The overage allowance reports this way, and it is unknown rather
			// than fatal: the limit still reaches the caller as a limit.
			name: "a limit naming no reset time is still a limit",
			event: backend.ProviderEvent{
				Type:    rateLimitEventType,
				Payload: json.RawMessage(`{"status":"rejected","rateLimitType":"overage"}`),
			},
			said: true,
			want: backend.Observation{Answer: backend.AnswerLimitReached, Kind: "overage"},
		},
		{
			// A rejected primary limit with overage already in use is still being
			// served, which is the provider's own rule for showing a hard limit.
			name: "a limit overage is already serving is not a refusal",
			event: backend.ProviderEvent{
				Type:    rateLimitEventType,
				Payload: json.RawMessage(`{"status":"rejected","isUsingOverage":true}`),
			},
			said: true,
			want: backend.Observation{Answer: backend.AnswerServed},
		},
		{
			name: "healthy utilization is capacity",
			event: backend.ProviderEvent{
				Type:    rateLimitEventType,
				Payload: json.RawMessage(`{"status":"allowed_warning","utilization":0.8}`),
			},
			said: true,
			want: backend.Observation{Answer: backend.AnswerServed},
		},
		{
			// A payload this dialect cannot read says nothing about capacity, and
			// reading it as exhaustion would stop runs that have capacity.
			name:  "a payload nobody can read says nothing",
			event: backend.ProviderEvent{Type: rateLimitEventType, Payload: json.RawMessage(`"a string"`)},
		},
		{
			name:  "no payload at all says nothing",
			event: backend.ProviderEvent{Type: rateLimitEventType},
		},
		{
			name:  "the provider's own retry is a retry in progress",
			event: backend.ProviderEvent{Type: systemEventType, Subtype: apiRetrySubtype},
			said:  true,
			want:  backend.Observation{Answer: backend.AnswerRetrying},
		},
		{
			name: "an overloaded server is a wait",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "API Error: 529 Overloaded. This is a server-side issue, usually temporary.",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnavailable,
				Detail: "API Error: 529 Overloaded. This is a server-side issue, usually temporary.",
			},
		},
		{
			// The one refusal the caller can answer by changing the request rather
			// than by waiting: a selector this provider will not serve, which a
			// pinned version falls back to its family on.
			name: "a not-found naming a model is the model being unavailable",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: `API Error: 404 {"type":"error","error":{"type":"not_found_error","message":"model: claude-opus-5-20260401"}}`,
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerModelUnavailable,
				Detail: `api_error: API Error: 404 {"type":"error","error":{"type":"not_found_error","message":"model: claude-opus-5-20260401"}}`,
			},
		},
		{
			// The match is narrow in both halves on purpose: a not-found this
			// version does not recognize keeps failing the turn rather than
			// becoming a silent move to another model.
			name: "a not-found that names no model is a refusal that stands",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "API Error: 404 Not Found",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerRefused,
				Detail: "api_error: API Error: 404 Not Found",
			},
		},
		{
			// A relaunch would put the identical request in front of the provider
			// again and earn the identical refusal.
			name: "a status describing the request is a refusal that stands",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "API Error: 403 Forbidden",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerRefused,
				Detail: "api_error: API Error: 403 Forbidden",
			},
		},
		{
			// The API's status for credentials it will not accept is the same
			// condition the CLI's own words below report, and earns the same wait.
			name: "an unauthorized status is a login nobody has renewed",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "API Error: 401 Unauthorized",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "api_error: API Error: 401 Unauthorized",
			},
		},
		{
			// The CLI's own words for an account it will not accept, which quote
			// no status at all. Reading it as weather would relaunch a run into an
			// answer no attempt can change, and reading it as a refusal that
			// stands would fail a run over a login the operator renews in a minute.
			name: "an account that is not logged in is a wait for the operator",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "Not logged in",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "api_error: Not logged in",
			},
		},
		{
			// The one recorded shape of a home nobody has logged into, asked on
			// 2026-09-19 with Claude Code 2.1.276: a terminal envelope with
			// terminal_reason "api_error" and the title paired with its remedy.
			name: "the recorded not-logged-in terminal is a wait for the operator",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "Not logged in · Please run /login",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "api_error: Not logged in · Please run /login",
			},
		},
		{
			// The CLI's other titles for the same condition, each paired with the
			// same remedy, earn the same wait.
			name: "a login that expired is a wait for the operator",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "Login expired · Please run /login",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "api_error: Login expired · Please run /login",
			},
		},
		{
			// The words a reviewer's session ended on when its login lapsed
			// mid-session: no status, no remedy, and the same wait.
			name: "an OAuth session that could not be refreshed is a wait for the operator",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "Failed to authenticate: OAuth session expired and could not be refreshed",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "api_error: Failed to authenticate: OAuth session expired and could not be refreshed",
			},
		},
		{
			// A CLI that refuses the login before it writes any envelope says so
			// on stderr, which is handed over as the one event on that channel.
			// It is the same wait the terminal form earns: relaunching a process
			// that died this way is the 2026-09-17 stall through the gap
			// yoyodyne-ifd.377 left.
			name:  "a login refused on stderr before any envelope is the same wait",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStderr, Text: "Not logged in · Please run /login"},
			said:  true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "Not logged in · Please run /login",
			},
		},
		{
			name:  "nothing answering, said on stderr, is the same wait",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStderr, Text: "API Error: Can't reach the API server"},
			said:  true,
			want: backend.Observation{
				Answer: backend.AnswerUnreachable,
				Detail: "API Error: Can't reach the API server",
			},
		},
		{
			// The same refusal written to stdout as plain text, where the stream
			// should have been, is the same wait: the CLI chose the channel and
			// the words are its own either way. This is the gap yoyodyne-ifd.393
			// reported after closing the stderr one.
			name:  "a login refused as plain text on stdout is the same wait",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStdout, Text: "Not logged in · Please run /login"},
			said:  true,
			want: backend.Observation{
				Answer: backend.AnswerUnauthenticated,
				Detail: "Not logged in · Please run /login",
			},
		},
		{
			name:  "nothing answering, said as plain text on stdout, is the same wait",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStdout, Text: "API Error: Can't reach the API server"},
			said:  true,
			want: backend.Observation{
				Answer: backend.AnswerUnreachable,
				Detail: "API Error: Can't reach the API server",
			},
		},
		{
			name:  "a banner on stdout says nothing",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStdout, Text: "Claude Code v2.1.276"},
		},
		{
			// Stderr names no ending and no status, so nothing else is read off
			// it: an overload there would be a guess about diagnostics, and a
			// process that died for any other reason stays the process failure
			// it always was.
			name:  "an overload on stderr says nothing",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStderr, Text: "API Error: 529 Overloaded"},
		},
		{
			name:  "a crash on stderr says nothing",
			event: backend.ProviderEvent{Channel: domain.ProviderChannelStderr, Text: "TypeError: Cannot read properties of undefined"},
		},
		{
			// The CLI's own words when nothing answers at the API, recorded on the
			// runs that died in the 2026-09-15..18 outage. It quotes no status
			// because nothing answered, and it is not the mid-reply drop below:
			// that one reached the provider.
			name: "nothing answering at the API is a wait for the provider",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "API Error: Can't reach the API server",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerUnreachable,
				Detail: "api_error: API Error: Can't reach the API server",
			},
		},
		{
			// "Connection closed mid-response" quotes no status because nothing
			// answered, and a harness that read it as a judgement of the work
			// would fail a whole run on weather.
			name: "a death that named no status is a transient one",
			event: backend.ProviderEvent{
				Type: "result", Subtype: terminalAPIError, Terminal: true, Failed: true,
				Text: "API Error: Connection closed mid-response. The response above may be incomplete.",
			},
			said: true,
			want: backend.Observation{
				Answer: backend.AnswerInterrupted,
				Detail: "api_error: API Error: Connection closed mid-response. The response above may be incomplete.",
			},
		},
		{
			name: "an ending that is not an API error at all is a refusal",
			event: backend.ProviderEvent{
				Type: "result", Subtype: "refusal", Terminal: true, Failed: true, Text: "I cannot help with that",
			},
			said: true,
			want: backend.Observation{Answer: backend.AnswerRefused, Detail: "refusal: I cannot help with that"},
		},
		{
			// A completed invocation says nothing about capacity: the provider
			// reports its limits on their own event, so reading a success as
			// evidence a limit has lifted would be inventing a fact.
			name:  "a terminal that succeeded says nothing",
			event: backend.ProviderEvent{Type: "result", Terminal: true},
		},
		{
			name:  "prose says nothing",
			event: backend.ProviderEvent{Type: "assistant", Text: "working on it"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation, said := Dialect{}.Observe(test.event)
			if said != test.said {
				t.Fatalf("said = %t, want %t (observation %#v)", said, test.said, observation)
			}
			if !said {
				return
			}
			if observation.Answer != test.want.Answer || observation.Kind != test.want.Kind || observation.Detail != test.want.Detail {
				t.Fatalf("observation = %#v, want %#v", observation, test.want)
			}
			if !observation.ResetsAt.Equal(test.want.ResetsAt) {
				t.Fatalf("ResetsAt = %s, want %s", observation.ResetsAt, test.want.ResetsAt)
			}
		})
	}
}

// The dialect is named, because a record of what read a provider's answer is
// part of what makes the answer attributable.
func TestTheClaudeDialectIsNamed(t *testing.T) {
	t.Parallel()

	if name := (Dialect{}).Name(); name != "claude-code" {
		t.Fatalf("Name() = %q", name)
	}
}

// A reset time in a shape this version does not expect is no reset time at all,
// and the limit still reaches the caller as one: what the harness refuses is
// guessing the wait, not noticing the refusal.
func TestAnUnreadableResetTimeIsNoResetTime(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"status":"rejected","resetsAt":"8:30pm"}`,
		`{"status":"rejected","resetsAt":0}`,
		`{"status":"rejected","resetsAt":-1}`,
		`{"status":"rejected"}`,
	} {
		observation, said := Dialect{}.Observe(backend.ProviderEvent{
			Type: rateLimitEventType, Payload: json.RawMessage(payload),
		})
		if !said || observation.Answer != backend.AnswerLimitReached {
			t.Fatalf("payload %s: observation = %#v, said = %t", payload, observation, said)
		}
		if !observation.ResetsAt.IsZero() {
			t.Fatalf("payload %s: ResetsAt = %s, want no reset time rather than a guessed one", payload, observation.ResetsAt)
		}
	}
}

// The dialect states no duration anywhere. It is the property that keeps a
// provider from being able to spend an account, and it is worth a test rather
// than a comment because it is the one thing about a plugin nobody can see by
// reading a configuration file.
func TestTheDialectStatesNoWait(t *testing.T) {
	t.Parallel()

	observation, _ := Dialect{}.Observe(backend.ProviderEvent{
		Type:    rateLimitEventType,
		Payload: json.RawMessage(`{"status":"rejected","rateLimitType":"seven_day"}`),
	})
	described := strings.ToLower(observation.Detail + observation.Kind)
	for _, forbidden := range []string{"minute", "hour", "wait ", "retry in"} {
		if strings.Contains(described, forbidden) {
			t.Fatalf("the dialect said %q, and how long to wait is the harness's", described)
		}
	}
}

// A project may declare a provider that this adapter launches and that reports
// its limits in some other spelling. The declaration's dialect is what reads the
// stream then, in place of this provider's own — so a declared rule actually
// fires on an invocation rather than sitting in a registry nothing consults.
func TestADeclaredDialectReadsTheStreamInsteadOfThisProvidersOwn(t *testing.T) {
	t.Parallel()

	// The provider spells its exhausted limit in prose on the terminal, which
	// this adapter's own dialect reads as a refusal that stands.
	const message = "Quota exhausted for this workspace; the window rolls at midnight."
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"result","subtype":"error","session_id":"session-1","is_error":true,` +
			`"terminal_reason":"quota_exhausted","result":"` + message + `","usage":{}}`,
	}, "\n") + "\n"

	declared, err := backend.NewDeclarativeDialect("my-harness", backend.DialectSpec{Rules: []backend.DialectRule{{
		Answer:   backend.AnswerLimitReached,
		Terminal: &terminalEvent,
		Failed:   &failedEvent,
		Match:    `(?i)quota exhausted`,
		Kind:     "workspace",
	}}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}

	// The same stream, once through this provider's own dialect and once through
	// the declared one, so what differs is only which dialect read it.
	own, _ := runUsageLimitStream(t, stream)
	if own.UsageLimit != nil {
		t.Fatalf("this provider's own dialect read the declared provider's prose as a limit: %#v", own.UsageLimit)
	}

	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stdout: stream}}},
		Clock:  fixedClock{},
		// What the caller resolved for the backend the agent named: the declared
		// provider's identity, and the dialect that reads its stream.
		Provider: "my-harness",
		Dialect:  declared,
	}).Run(context.Background(), backend.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.UsageLimit == nil || result.UsageLimit.Kind != "workspace" {
		t.Fatalf("UsageLimit = %#v, want the limit the declared dialect read", result.UsageLimit)
	}
	// The reset time is unknown rather than invented: the provider named none the
	// declaration could read, and what the harness does about that is the
	// contract's answer rather than the plugin's.
	if !result.UsageLimit.ResetsAt.IsZero() {
		t.Fatalf("ResetsAt = %s, want no reset time rather than a guessed one", result.UsageLimit.ResetsAt)
	}
	// The record says which provider answered, not which adapter launched it.
	if result.Backend != "my-harness" {
		t.Fatalf("Backend = %q, want the provider the agent named", result.Backend)
	}
	// And it says which adapter did launch it, beside rather than instead: the
	// provider is the project's declaration, and the adapter version is this
	// build's code that read the stream with the declared dialect. A record
	// carrying only the first could not say which harness classified the refusal
	// above.
	if result.AdapterVersion != backend.ClaudeCodeAdapterVersion {
		t.Fatalf("AdapterVersion = %q, want the compiled adapter that read the stream", result.AdapterVersion)
	}
	if len(events) == 0 {
		t.Fatal("the invocation recorded nothing")
	}
}

// terminalEvent and failedEvent are the two facts a declared rule matches on,
// addressable because a rule distinguishes "not a condition" from "must be
// false".
var terminalEvent, failedEvent = true, true

// unixSeconds is how this provider quotes a reset time: whole seconds since the
// epoch, which is what its own CLI compares and renders.
func unixSeconds(at time.Time) string {
	return strconv.FormatInt(at.Unix(), 10)
}
