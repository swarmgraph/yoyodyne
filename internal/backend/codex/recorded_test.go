package codex

import (
	"bufio"
	"encoding/json"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// recordedStream is what a stream under testdata/streams is expected to leave
// the parser holding. Each recorded file has one, and a recorded file with
// none fails the test, so a stream nobody has said anything about cannot sit
// in the directory looking like coverage.
type recordedStream struct {
	sessionID string
	// terminal is whether the stream carried a terminal this parser reads.
	terminal bool
	// unrecognized is the first event this parser did not know, or empty.
	unrecognized string
	// failed is whether the terminal, when there is one, was a failure.
	failed bool
	// reply is the agent's answer the stream leaves on the result.
	reply string
	// usage is the terminal's usage under the harness's names, or nil for a
	// stream that reported none.
	usage map[string]int64
	// providerUsage is the terminal's usage as the provider wrote it, or nil.
	providerUsage map[string]int64
	// warnings is how many `error` items the stream carried, each of which must
	// have been recorded as an item and not as the turn's ending.
	warnings int
}

var recordedStreams = map[string]recordedStream{
	// Never reached the provider: a sandbox proxy refused every connection, so
	// the CLI reconnected until the recording was stopped. Every `error` in it
	// is a reconnect notice, and none of them ends the turn.
	"codex-cli-0.159.2/no-provider-reached.jsonl": {
		sessionID: "01a0f859-a609-71b2-8b5a-a4a52704a5df",
		warnings:  1,
	},
	// A turn that reached the provider and completed. Two `error` items ahead of
	// the turn are warnings — configuration settings the CLI ignored, with the
	// operator's home directory written as `~` — and the turn carries on past
	// them to its reply and a turn.completed. The provider's input counts its
	// cached reads, so the harness's fresh input is the difference.
	"codex-cli-0.159.2/reply-and-turn-completed.jsonl": {
		sessionID: "01a0f87c-cdff-7233-adc4-8d2078a8ec52",
		terminal:  true,
		reply:     "ready",
		usage: map[string]int64{
			"input_tokens":            16304 - 13184,
			"cache_read_input_tokens": 13184,
			"output_tokens":           5,
			"reasoning_output_tokens": 0,
		},
		providerUsage: map[string]int64{
			"input_tokens":             16304,
			"cached_input_tokens":      13184,
			"cache_write_input_tokens": 0,
			"output_tokens":            5,
			"reasoning_output_tokens":  0,
		},
		warnings: 2,
	},
}

func TestEveryRecordedStreamIsReadAsTheCLIWroteIt(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(filepath.Join("testdata", "streams", "*", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no recorded Codex streams under testdata/streams")
	}
	for _, path := range paths {
		name := filepath.ToSlash(strings.TrimPrefix(path, filepath.Join("testdata", "streams")+string(filepath.Separator)))
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want, ok := recordedStreams[name]
			if !ok {
				t.Fatalf("recorded stream %s has no expectation in recordedStreams", name)
			}
			var events []execution.Event
			sink := func(event execution.Event) error {
				events = append(events, event)
				return nil
			}
			parser := newStreamParser(testRunID, domain.RoleDeveloper, 0, fixedClock{}, execution.NewRedactor(), sink, nil, Dialect{})
			for _, line := range readRecordedLines(t, path) {
				if err := parser.ParseLine(line); err != nil {
					t.Fatalf("ParseLine(%q) error = %v", line, err)
				}
			}
			result := parser.Result()
			if result.SessionID != want.sessionID {
				t.Errorf("session = %q, want %q", result.SessionID, want.sessionID)
			}
			if parser.SawTerminal() != want.terminal {
				t.Errorf("terminal = %v, want %v (stop reason %q)", parser.SawTerminal(), want.terminal, result.StopReason)
			}
			if got := parser.FirstUnrecognized(); got != want.unrecognized {
				t.Errorf("first unrecognized event = %q, want %q", got, want.unrecognized)
			}
			if result.FinalText != want.reply {
				t.Errorf("reply = %q, want %q", result.FinalText, want.reply)
			}
			if want.terminal && result.IsError != want.failed {
				t.Errorf("terminal failed = %v, want %v (stop reason %q)", result.IsError, want.failed, result.StopReason)
			}
			var usage map[string]int64
			if len(result.Usage) > 0 {
				if err := json.Unmarshal(result.Usage, &usage); err != nil {
					t.Fatalf("decode usage %s: %v", result.Usage, err)
				}
			}
			if !reflect.DeepEqual(usage, want.usage) {
				t.Errorf("usage = %v, want %v", usage, want.usage)
			}

			// Exactly one event ends the invocation, it is the last one, and it
			// carries what the result does. Every `error` item before it was
			// recorded as an item: a warning taken for the ending would have
			// ended the turn before the reply arrived.
			endings, warnings := 0, 0
			for index, event := range events {
				var payload struct {
					ProviderType  string           `json:"provider_type"`
					ItemType      string           `json:"item_type"`
					Usage         map[string]int64 `json:"usage"`
					ProviderUsage map[string]int64 `json:"provider_usage"`
				}
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatalf("decode event %d: %v", index, err)
				}
				if payload.ProviderType == eventItemCompleted && payload.ItemType == itemError {
					warnings++
					if event.Type != execution.EventProcessOutput {
						t.Errorf("an error item was recorded as %s, want process output", event.Type)
					}
				}
				if event.Type != execution.EventRunCompleted && event.Type != execution.EventRunFailed {
					continue
				}
				endings++
				if index != len(events)-1 {
					t.Errorf("the invocation ended at event %d of %d", index+1, len(events))
				}
				if !reflect.DeepEqual(payload.Usage, want.usage) {
					t.Errorf("terminal usage = %v, want %v", payload.Usage, want.usage)
				}
				if !reflect.DeepEqual(payload.ProviderUsage, want.providerUsage) {
					t.Errorf("terminal provider usage = %v, want %v", payload.ProviderUsage, want.providerUsage)
				}
			}
			if wantEndings := map[bool]int{true: 1, false: 0}[want.terminal]; endings != wantEndings {
				t.Errorf("events ending the invocation = %d, want %d", endings, wantEndings)
			}
			if warnings != want.warnings {
				t.Errorf("error items recorded = %d, want %d", warnings, want.warnings)
			}
			if !want.terminal {
				// A stream that has not ended has been answered nothing: a notice
				// read as a terminal would leave a failure, a refusal, or a wait
				// here that the provider never gave.
				if result.IsError || result.ServerOverload != nil || result.TransientFailure != nil || result.ProviderOutage != nil || result.UsageLimit != nil || result.ModelUnavailable != nil {
					t.Errorf("a stream with no terminal was given an outcome: %#v", result)
				}
			}
		})
	}
}

// A newer-vocabulary `error` is the provider retrying, whatever status its
// prose quotes; the recorded one quoted a 403, which read as a terminal is a
// refusal that stands.
func TestANoticeIsTheProviderRetrying(t *testing.T) {
	t.Parallel()

	observation, said := (Dialect{}).Observe(backendapi.ProviderEvent{
		Type: eventError,
		Text: "Reconnecting... 2/5 (stream disconnected before completion: URL error: Proxy connection failed: HTTP CONNECT failed with status 403)",
	})
	if !said || observation.Answer != backendapi.AnswerRetrying {
		t.Fatalf("Observe() = %#v, %v; want retrying", observation, said)
	}
}

// The older vocabulary's `error` is still the invocation's failed terminal; a
// bare one is what changed.
func TestAnEnvelopedErrorIsStillATerminal(t *testing.T) {
	t.Parallel()

	parser := newStreamParser(testRunID, domain.RoleDeveloper, 0, fixedClock{}, execution.NewRedactor(), nil, nil, Dialect{})
	if err := parser.ParseLine(`{"id":"1","msg":{"type":"error","message":"stream failed"}}`); err != nil {
		t.Fatal(err)
	}
	if !parser.SawTerminal() || !parser.Result().IsError {
		t.Fatalf("enveloped error was not read as a failed terminal: %#v", parser.Result())
	}
}

// An item this parser has not seen recorded is named by its item type, so the
// error a stream with no terminal fails with says which item it was. The line
// is written by hand; no stream recorded here has carried a command item.
func TestAnUnknownItemIsNamedByItsType(t *testing.T) {
	t.Parallel()

	parser := newStreamParser(testRunID, domain.RoleDeveloper, 0, fixedClock{}, execution.NewRedactor(), nil, nil, Dialect{})
	if err := parser.ParseLine(`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"ls"}}`); err != nil {
		t.Fatal(err)
	}
	if got, want := parser.FirstUnrecognized(), "item.completed (command_execution item)"; got != want {
		t.Fatalf("first unrecognized = %q, want %q", got, want)
	}
}

func readRecordedLines(t *testing.T, path string) []string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var read []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		read = append(read, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return read
}

// turn.failed is read as the turn's failed ending, with the error object's
// message as its prose, so the dialect can tell a limit or a refusal from it.
// The line is written by hand in the shape the provider's exec protocol gives
// it: codex-cli 0.159.2's binary names the event, and no recording here has
// carried one.
func TestATurnFailedIsAFailedTerminal(t *testing.T) {
	t.Parallel()

	parser := newStreamParser(testRunID, domain.RoleDeveloper, 0, fixedClock{}, execution.NewRedactor(), nil, nil, Dialect{})
	for _, line := range []string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"error","message":"a warning"}}`,
		`{"type":"turn.failed","error":{"message":"You've hit your usage limit. Try again in 2 hours."}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
	} {
		if err := parser.ParseLine(line); err != nil {
			t.Fatal(err)
		}
	}
	result := parser.Result()
	if !parser.SawTerminal() || !result.IsError || result.StopReason != eventTurnFailed {
		t.Fatalf("turn.failed was not read as a failed terminal: %#v", result)
	}
	if result.FinalText != "You've hit your usage limit. Try again in 2 hours." {
		t.Fatalf("failure text = %q", result.FinalText)
	}
	if result.UsageLimit == nil {
		t.Fatalf("the dialect was not asked about the failure: %#v", result)
	}
	if len(result.Usage) != 0 {
		t.Fatalf("a turn.completed after the ending replaced the result's usage: %s", result.Usage)
	}
}

// A turn.* event this parser does not know is not taken for an ending: it is
// named, so a stream that then stops fails saying which event it was.
func TestAnUnknownTurnEventIsNotAnEnding(t *testing.T) {
	t.Parallel()

	parser := newStreamParser(testRunID, domain.RoleDeveloper, 0, fixedClock{}, execution.NewRedactor(), nil, nil, Dialect{})
	if err := parser.ParseLine(`{"type":"turn.interrupted"}`); err != nil {
		t.Fatal(err)
	}
	if parser.SawTerminal() || parser.FirstUnrecognized() != "turn.interrupted" {
		t.Fatalf("terminal = %v, unrecognized = %q", parser.SawTerminal(), parser.FirstUnrecognized())
	}
}

// The recorded successful turn, run through the whole adapter, is an invocation
// that replied, carries no price the provider never stated, and leaves usage on
// its terminal under the names the harness's price reader decodes.
func TestARecordedSuccessfulTurnRunsToAReply(t *testing.T) {
	t.Parallel()

	stream := strings.Join(readRecordedLines(t, filepath.Join("testdata", "streams", "codex-cli-0.159.2", "reply-and-turn-completed.jsonl")), "\n") + "\n"
	result, events := runStream(t, domain.RoleDeveloper, stream)
	if result.IsError || result.FinalText != "ready" || result.SessionID != "01a0f87c-cdff-7233-adc4-8d2078a8ec52" {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.CostReported || result.CostUSD != 0 {
		t.Fatalf("Run() priced an invocation the provider never priced: %#v", result)
	}
	terminal := events[len(events)-1]
	if terminal.Type != execution.EventRunCompleted {
		t.Fatalf("last event = %s, want the run completed", terminal.Type)
	}
	var payload struct {
		Usage        *usageRow `json:"usage"`
		TotalCostUSD *float64  `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TotalCostUSD != nil {
		t.Fatalf("terminal carries a price the provider never stated: %v", *payload.TotalCostUSD)
	}
	if payload.Usage == nil || *payload.Usage != (usageRow{InputTokens: 3120, OutputTokens: 5, CacheReadTokens: 13184}) {
		t.Fatalf("terminal usage = %+v", payload.Usage)
	}
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: testRunID, ProductID: "yoyodyne", RepositoryID: "yoyodyne", WorkItemID: "codex-usage", Backend: domain.BackendCodex, Status: runstate.StatusSucceeded, StartedAt: now, UpdatedAt: now, CompletedAt: &now}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := store.AppendEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	if price.Tokens.InputTotal() != 16304 || price.Tokens.CacheReadTokens != 13184 || price.Tokens.OutputTokens != 5 || price.Tokens.NoCost != 1 || len(price.Runs) != 1 || price.Runs[0].Tokens != price.Tokens {
		t.Fatalf("price = %+v", price)
	}
	if text := price.Tokens.CostText(price.TotalUSD); strings.Contains(text, "$0") || !strings.Contains(text, "no cost reported") {
		t.Fatal(text)
	}

}

func TestCompletedCodexStreamWithoutUsage(t *testing.T) {
	result, events := runStream(t, domain.RoleDeveloper, "{\"type\":\"turn.completed\"}\n")
	if len(result.Usage) != 0 || result.CostReported {
		t.Fatalf("result = %+v", result)
	}
	var payload struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(events[len(events)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Usage) != 0 && string(payload.Usage) != "null" {
		t.Fatalf("usage = %s", payload.Usage)
	}
}
