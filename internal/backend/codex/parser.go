package codex

// Reading one `codex exec --json` invocation: which line is the invocation's
// terminal, what goes into the event log, and what the invocation's result is.
// What each thing the provider said *means* is the dialect's, and what to do
// about it is the harness's.
//
// Codex has written two vocabularies. The older one wraps each event in an
// envelope under `msg` and names it in snake case — session_configured,
// agent_message, token_count, task_complete — and is read from the provider's
// documented protocol rather than off a run. The newer one is what
// codex-cli 0.159.2 writes, read off real invocations recorded under
// testdata/streams: bare events named thread.started, turn.started,
// item.completed, and turn.completed, the session carried as `thread_id`, the
// reply as an `agent_message` item's `text`, and the turn's usage on
// turn.completed. Neither a top-level `error` (one per reconnect attempt) nor
// an `error` item (a warning such as a configuration setting the CLI ignored)
// ends the turn: both were recorded with the turn carrying on past them, the
// second ending in a turn.completed. The turn ends at turn.completed, or at
// turn.failed, which this CLI's binary names but no recording here has shown;
// it is read in the shape the provider's exec protocol gives it, an `error`
// object carrying a `message`. Any other turn.* event is not taken for an
// ending: it is named as unrecognized, so a stream that then stops without a
// terminal fails naming it. Shell, patch, and tool items are not yet recorded
// and are named the same way.
//
// Everything here degrades in the safe direction: an event this parser does not
// recognize is recorded whole and read as nothing, and an invocation whose
// terminal never arrives fails with exactly that reason rather than with an
// outcome nobody produced — or, when the stream carried an event this parser
// did not know, with the CLI's version and that event's name, because a stream
// in a vocabulary this adapter does not speak is a different fault from a
// terminal that never came.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The bound is the harness's, shared with the operator's side of a conversation
// it records itself, so the two halves of one exchange are cut to one rule.
const maxEventTextBytes = execution.MaxEventTextBytes

// sourceName is what this adapter's normalized events are recorded as, and what
// its dialect calls itself.
const sourceName = "codex"

// The rest of the provider's event names this parser reads. The three the
// dialect also reads are named beside it, because what they mean is its answer
// rather than this parser's.
const (
	eventSessionConfigured = "session_configured"
	eventAgentMessage      = "agent_message"
	eventExecCommandBegin  = "exec_command_begin"
	eventExecCommandEnd    = "exec_command_end"
	eventMCPToolCallBegin  = "mcp_tool_call_begin"
	eventMCPToolCallEnd    = "mcp_tool_call_end"
	eventPatchApplyBegin   = "patch_apply_begin"
	eventPatchApplyEnd     = "patch_apply_end"
	eventTokenCount        = "token_count"
)

// The newer vocabulary's names, each one seen in a recorded stream. An item is
// recognized by the type of the item it carries rather than by the event alone,
// because the item is what says whether this parser knows what it is reading.
const (
	eventThreadStarted = "thread.started"
	eventTurnStarted   = "turn.started"
	eventItemCompleted = "item.completed"
	eventTurnCompleted = "turn.completed"
	itemError          = "error"
	itemAgentMessage   = "agent_message"
)

// eventTurnFailed is the newer vocabulary's failed ending. codex-cli 0.159.2's
// binary carries the name, but no recorded stream has shown one: its shape is
// the provider's exec protocol's, an `error` object with a `message`.
const eventTurnFailed = "turn.failed"

// truncatedStreamLine is the harness's own name for a provider line the process
// runner had to cut at its per-line bound. It sits beside the duplicate terminal
// below as an anomaly rather than a failure: what that envelope said is gone,
// and everything before and after it is intact.
const truncatedStreamLine = "truncated_stream_line"

// streamedFragment names the events that carry a piece of something the provider
// also sends whole. They are dropped rather than recorded: a delta stream is the
// same text again in hundreds of pieces, and an event log holding both says
// nothing extra while being far harder to read.
func streamedFragment(eventType string) bool {
	return strings.HasSuffix(eventType, "_delta") ||
		eventType == "agent_reasoning_raw_content" ||
		eventType == "agent_reasoning_section_break"
}

// streamEnvelope is one line of the provider's stream. Only the event itself is
// read: the submission identifier beside it correlates a line with a request the
// harness never makes more than one of.
type streamEnvelope struct {
	Msg json.RawMessage `json:"msg"`
}

// streamItem is what a newer-vocabulary item event carries, reduced to the
// fields this adapter reads.
type streamItem struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	// An agent_message item carries the agent's prose here.
	Text string `json:"text"`
}

// turnUsage is what a turn.completed reports the turn read and wrote, as
// codex-cli 0.159.2 wrote it. Input counts every token read, the cached ones
// included, which is the provider's convention rather than the harness's.
type turnUsage struct {
	InputTokens           *int64 `json:"input_tokens"`
	CachedInputTokens     *int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens"`
	OutputTokens          *int64 `json:"output_tokens"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens"`
}

// harnessUsage writes a turn's usage under the names the harness's price reader
// looks for. The price reader's input is fresh input, with cache reads counted
// beside it rather than inside it, so the cached reads are taken out of the
// provider's input here; left in, every cache read would be counted twice in the
// input total. Cache writes are left inside the input rather than moved to the
// price reader's cache-creation count: no recording has shown a non-zero one,
// so whether the provider counts them inside its input is not known, and left
// where they are they are counted once either way. Reasoning is written beside
// the output under the provider's own name, which the price reader does not read.
// A count the provider did not state is left out rather than written as zero.
func (u turnUsage) harnessUsage() (json.RawMessage, bool) {
	if u.InputTokens == nil && u.CachedInputTokens == nil && u.OutputTokens == nil && u.ReasoningOutputTokens == nil {
		return nil, false
	}
	counts := map[string]int64{}
	if u.InputTokens != nil {
		fresh := *u.InputTokens
		if u.CachedInputTokens != nil {
			fresh -= *u.CachedInputTokens
		}
		if fresh < 0 {
			fresh = 0
		}
		counts["input_tokens"] = fresh
	}
	if u.CachedInputTokens != nil {
		counts["cache_read_input_tokens"] = *u.CachedInputTokens
	}
	if u.OutputTokens != nil {
		counts["output_tokens"] = *u.OutputTokens
	}
	if u.ReasoningOutputTokens != nil {
		counts["reasoning_output_tokens"] = *u.ReasoningOutputTokens
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// providerMessage is one event, reduced to the fields this adapter reads. The
// provider sends more than this on several of them, and what is not read here is
// still recorded: the raw line reaches the event log whenever this parser has
// nothing better to say about it.
type providerMessage struct {
	Type string `json:"type"`
	// enveloped says the event arrived under `msg`, which is the older
	// vocabulary. It decides what an `error` is: a terminal there, and a notice
	// the turn carries on past in the newer one.
	enveloped bool
	// thread.started names the session a later invocation resumes.
	ThreadID string `json:"thread_id"`
	// item.completed carries the item it completed.
	Item json.RawMessage `json:"item"`
	// turn.completed carries the turn's usage, and turn.failed its error. Both
	// are kept raw: the older vocabulary uses neither name, and an unexpected
	// shape under either must not make a whole line unreadable.
	TurnUsage json.RawMessage `json:"usage"`
	TurnError json.RawMessage `json:"error"`
	// session_configured names the session a later invocation resumes and the
	// model the provider resolved the requested selector to.
	SessionID       string  `json:"session_id"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoning_effort"`
	// agent_message, error, and stream_error each carry their prose here.
	Message string `json:"message"`
	// task_complete carries the agent's last message, which is the invocation's
	// answer.
	LastAgentMessage string `json:"last_agent_message"`
	// The shell, MCP, and patch calls each identify themselves with a call id and
	// report how they ended.
	CallID   string          `json:"call_id"`
	Command  []string        `json:"command"`
	Server   string          `json:"server"`
	Tool     string          `json:"tool"`
	ExitCode *int            `json:"exit_code"`
	Stdout   string          `json:"stdout"`
	Stderr   string          `json:"stderr"`
	Success  *bool           `json:"success"`
	Changes  json.RawMessage `json:"changes"`
	// token_count reports what the invocation has read and written so far, in
	// either of the two shapes the provider has used for it.
	Info              json.RawMessage `json:"info"`
	InputTokens       *int64          `json:"input_tokens"`
	CachedInputTokens *int64          `json:"cached_input_tokens"`
	OutputTokens      *int64          `json:"output_tokens"`
}

// tokenCountInfo is the newer shape of a token_count, which nests the running
// totals under an info object.
type tokenCountInfo struct {
	TotalTokenUsage *struct {
		InputTokens       *int64 `json:"input_tokens"`
		CachedInputTokens *int64 `json:"cached_input_tokens"`
		OutputTokens      *int64 `json:"output_tokens"`
	} `json:"total_token_usage"`
}

// usage is what this invocation read and wrote, written under the names the
// harness's own price reader looks for rather than under the provider's. Codex
// calls its cached reads `cached_input_tokens`; renaming it here is what stops a
// run priced from this log counting every cache read as a fresh one. A count the
// provider did not state is left out rather than written as zero, because an
// invocation nobody has a measurement for and one measured at nothing are
// opposite facts to anything adding tokens up.
func (m providerMessage) usage() (json.RawMessage, bool) {
	input, cached, output := m.InputTokens, m.CachedInputTokens, m.OutputTokens
	if len(m.Info) > 0 {
		var info tokenCountInfo
		if json.Unmarshal(m.Info, &info) == nil && info.TotalTokenUsage != nil {
			input, cached, output = info.TotalTokenUsage.InputTokens, info.TotalTokenUsage.CachedInputTokens, info.TotalTokenUsage.OutputTokens
		}
	}
	if input == nil && cached == nil && output == nil {
		return nil, false
	}
	counts := map[string]int64{}
	if input != nil {
		counts["input_tokens"] = *input
	}
	if output != nil {
		counts["output_tokens"] = *output
	}
	if cached != nil {
		counts["cache_read_input_tokens"] = *cached
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

type streamParser struct {
	runID string
	// role is who this invocation was made as, and it is written onto the
	// terminal event so the record says whose invocation it priced rather than
	// leaving that to be inferred from where the terminal sits in the log.
	role     domain.AgentRole
	sequence *execution.Sequence
	clock    execution.Clock
	redactor execution.Redactor
	sink     func(execution.Event) error
	// dialect is how what this provider says is read. It is a field rather than a
	// package-level call so the parser depends on the contract rather than on
	// this provider: a project that declared a provider running on this adapter
	// supplies its own, and a test drives the parser with a provider's answers
	// and nothing else about the provider.
	dialect backend.Dialect
	// reply is where the agent's prose goes for whoever is watching it arrive,
	// and nil when nobody is.
	reply       func(string)
	result      backend.RunResult
	sawTerminal bool
	// stderr is what the process wrote to its error stream, and stdout is what
	// it wrote to its output stream before any envelope without its being one,
	// each kept bounded so it can be handed to the dialect once the stream has
	// ended. Both are read only when no terminal arrived — see
	// ObservePlainOutput — so for every invocation that ended the way the
	// provider ends one they are held and never consulted.
	stderr strings.Builder
	stdout strings.Builder
	// sawEnvelope says the stream has carried at least one line this parser
	// could read as an event. An unreadable line before that is held as plain
	// stdout above as well as recorded; one after it is only recorded.
	sawEnvelope bool
	// unrecognized is the type of the first event this parser read and did not
	// know, ahead of any terminal. A stream that then ends with no terminal is
	// most likely one written in a vocabulary this adapter does not speak, and
	// this is what lets that be said instead of a terminal reported missing.
	unrecognized string
	// providerUsage is a turn.completed's usage object as the provider wrote it,
	// kept beside the harness-named one on the terminal because the mapping
	// takes cached reads out of the input and the record should still show what
	// the provider said.
	providerUsage json.RawMessage
}

func newStreamParser(runID string, role domain.AgentRole, lastSequence uint64, clock execution.Clock, redactor execution.Redactor, sink func(execution.Event) error, reply func(string), dialect backend.Dialect) *streamParser {
	if dialect == nil {
		dialect = Dialect{}
	}
	return &streamParser{
		runID:    runID,
		role:     role,
		sequence: execution.NewSequence(lastSequence),
		clock:    clock,
		redactor: redactor,
		sink:     sink,
		dialect:  dialect,
		reply:    reply,
	}
}

// ParseLine reads one line of the provider's stream.
//
// A line this parser cannot read is recorded rather than fatal, which is where
// it differs from the Claude Code adapter beside it. Codex writes its events to
// standard output and nothing guarantees that every line there is one of them: a
// banner or a warning would otherwise fail a run whose work was fine. Nothing is
// lost by being lenient here, because Run still requires a terminal it can read
// — an invocation whose whole stream is unreadable fails with exactly that.
//
// An unreadable line before any event is also held as plain stdout, for the
// same reason stderr is held: a CLI that refuses an expired login before it
// writes anything structured may say so there, and what it said is read once
// the stream has ended without a terminal — see ObservePlainOutput.
func (p *streamParser) ParseLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	message, readable := decodeMessage(line)
	if !readable {
		text := p.redactor.Redact(line)
		if !p.sawEnvelope {
			keepPlain(&p.stdout, text)
		}
		return p.emit(execution.EventProcessOutput, map[string]any{
			"stream": execution.StreamStdout,
			"text":   truncate(text),
		})
	}
	p.sawEnvelope = true
	p.redactMessage(&message)
	if streamedFragment(message.Type) {
		return nil
	}
	// The provider keeps writing after the terminal — a shutdown notice, a last
	// accounting line. Those are recorded so their payload stays diagnosable, and
	// none of them may disturb the decided result: the guarded invariant is that
	// a second terminal cannot replace the first.
	if p.sawTerminal {
		return p.recordAfterTerminal(message)
	}

	if !message.enveloped {
		switch message.Type {
		case eventThreadStarted:
			return p.parseThreadStarted(message)
		case eventTurnStarted:
			return p.emit(execution.EventProcessOutput, map[string]any{"provider_type": message.Type})
		case eventItemCompleted:
			return p.parseItem(message)
		case eventError:
			return p.parseNotice(message)
		case eventTurnCompleted:
			return p.parseTurnCompleted(message)
		case eventTurnFailed:
			return p.parseTurnFailed(message)
		}
	}

	switch message.Type {
	case eventSessionConfigured:
		return p.parseSessionConfigured(message)
	case eventAgentMessage:
		return p.parseAgentMessage(message)
	case eventExecCommandBegin, eventMCPToolCallBegin, eventPatchApplyBegin:
		return p.emit(execution.EventCommandStarted, map[string]any{
			"tool_use_id": message.CallID,
			"tool":        toolName(message),
			"input_bytes": inputBytes(message),
		})
	case eventExecCommandEnd, eventMCPToolCallEnd, eventPatchApplyEnd:
		return p.emit(execution.EventCommandCompleted, map[string]any{
			"tool_use_id":   message.CallID,
			"is_error":      callFailed(message),
			"content_bytes": len(message.Stdout) + len(message.Stderr),
		})
	case eventTokenCount:
		return p.parseTokenCount(message)
	case eventStreamError:
		// The provider is retrying by itself. The dialect is asked anyway, so the
		// answer for a retry in progress is the contract's rather than this
		// parser's silence.
		p.observe(backend.ProviderEvent{Type: message.Type, Text: message.Message})
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type": message.Type,
			"error":         truncate(message.Message),
		})
	case eventTaskComplete:
		return p.parseTerminal(message, message.LastAgentMessage, false)
	case eventError:
		return p.parseTerminal(message, message.Message, true)
	default:
		return p.parseUnrecognized(message.Type)
	}
}

// parseUnrecognized records an event this parser does not know, and remembers
// the first one ahead of any terminal for the error a stream with no terminal
// fails with.
func (p *streamParser) parseUnrecognized(eventType string) error {
	if p.unrecognized == "" {
		p.unrecognized = eventType
	}
	return p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type": eventType,
	})
}

func (p *streamParser) EmitProcessOutput(output execution.Output) error {
	text := p.redactor.Redact(output.Text)
	if output.Stream == execution.StreamStderr {
		keepPlain(&p.stderr, text)
	}
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream": output.Stream,
		"text":   truncate(text),
	})
}

// RecordAfterReply writes into the invocation's log what became of a process
// that went on running after its terminal: waiting on it, and then which way
// it ended. It is its own line rather than a stderr one, so it is never read as
// the provider's prose.
func (p *streamParser) RecordAfterReply(account execution.AfterReply) error {
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream":      "harness",
		"text":        account.Describe(p.clock.Now()),
		"after_reply": account,
	})
}

// keepPlain holds one redacted line of a plain channel for ObservePlainOutput,
// up to the same bound an event's text is held to. A refusal the CLI makes
// before it writes anything structured is one short line at the front, so what
// the bound cuts is the tail of a process that had a great deal else to say.
func keepPlain(held *strings.Builder, text string) {
	if held.Len() >= maxEventTextBytes {
		return
	}
	if held.Len() > 0 {
		held.WriteByte('\n')
	}
	held.WriteString(text)
}

// ObservePlainOutput hands the dialect what the process wrote as prose — its
// stderr, and then the plain stdout it wrote before any event — as one event
// per channel, and records whatever it answers. It is for the stream that
// ended without a terminal of its own: a CLI that refuses an expired login
// before it writes a single event says so on one of the two and exits, and a
// dialect that read only events left that invocation an unclassified process
// failure — which relaunches into the same login, spends the budget, and
// blocks, the shape yoyodyne-ifd.377 closed for Claude Code. The caller asks
// this only in that case, so a terminal the provider did write is never
// second-guessed by its diagnostics, and a process that wrote nothing is asked
// nothing.
//
// Stderr is read first, and stdout only when stderr answered nothing, so that
// a CLI which said the same thing on both leaves one channel on the record
// rather than whichever was recorded last.
func (p *streamParser) ObservePlainOutput() {
	if p.observePlain(domain.ProviderChannelStderr, p.stderr.String()) {
		return
	}
	p.observePlain(domain.ProviderChannelStdout, p.stdout.String())
}

// observePlain hands one plain channel to the dialect and reports whether it
// answered with a wait. A channel that carried only whitespace is one the
// dialect is never asked about.
func (p *streamParser) observePlain(channel domain.ProviderChannel, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	before := p.result.ProviderOutage
	p.observe(backend.ProviderEvent{Channel: channel, Text: text})
	return p.result.ProviderOutage != before
}

// SawUsageLimit reports a stream that told the dialect a limit is refusing
// work. A stream that said so and then ended without a terminal has already
// been answered, and is not one whose plain output should be read for a second
// one.
func (p *streamParser) SawUsageLimit() bool {
	return p.result.UsageLimit != nil
}

// RecordTruncatedLine records a cut line loudly and reads nothing off it.
//
// A truncated line is invalid JSON by construction, so handing it to ParseLine
// would record it as unreadable output and say nothing about why — and why is
// the whole of what a reader needs here, because the envelope that was cut may
// have been the invocation's terminal. If it was, the stream ends without one
// and the invocation is answered the way every other lost terminal already is;
// the anomaly in the log is what says why the terminal is missing.
func (p *streamParser) RecordTruncatedLine(output execution.Output) error {
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream":  output.Stream,
		"anomaly": truncatedStreamLine,
		"text":    truncate(p.redactor.Redact(output.Text)),
	})
}

func (p *streamParser) parseSessionConfigured(message providerMessage) error {
	p.result.SessionID = message.SessionID
	// This is where the provider names the model it resolved the requested
	// selector to. It is recorded as first-class result evidence rather than left
	// buried in the event payload, because a floating family alias makes the
	// resolved identifier the only durable evidence of what really ran.
	p.result.ResolvedModel = message.Model
	if message.ReasoningEffort != nil && strings.TrimSpace(*message.ReasoningEffort) != "" {
		p.result.ResolvedEffort = *message.ReasoningEffort
		p.result.EffortReported = true
	}
	return p.emit(execution.EventRunStarted, map[string]any{
		"session_id": message.SessionID,
		"model":      message.Model,
	})
}

// parseThreadStarted is the newer vocabulary's session: the thread a later
// invocation resumes. It names no model, so the resolved model stays unknown
// rather than being taken from the request.
func (p *streamParser) parseThreadStarted(message providerMessage) error {
	p.result.SessionID = message.ThreadID
	return p.emit(execution.EventRunStarted, map[string]any{
		"session_id": message.ThreadID,
	})
}

// parseNotice reads a newer-vocabulary `error`, which is the CLI saying what
// went wrong while it carries on — "Reconnecting... 2/5" — rather than ending the
// turn. Read as a terminal, the way the older vocabulary's `error` is, the first
// reconnect in a recorded stream ended the invocation as a refusal that stands,
// because its prose quoted the 403 a proxy had answered with. The dialect is
// asked anyway, so what a notice means is the contract's answer rather than this
// parser's.
func (p *streamParser) parseNotice(message providerMessage) error {
	p.observe(backend.ProviderEvent{Type: message.Type, Text: message.Message})
	return p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type": message.Type,
		"error":         truncate(message.Message),
	})
}

// parseItem reads one completed item. Only the item types a recorded stream has
// carried are recognized; any other is named as the event this parser did not
// know, item type and all, because "item.completed" alone would not say which
// item a newer CLI wrote that this one cannot read.
func (p *streamParser) parseItem(message providerMessage) error {
	var item streamItem
	if len(message.Item) > 0 {
		_ = json.Unmarshal(message.Item, &item)
	}
	switch item.Type {
	case itemAgentMessage:
		// The newer vocabulary's reply. Each one replaces the last, as the older
		// vocabulary's agent_message does, so the turn's answer is its last.
		return p.parseAgentMessage(providerMessage{Type: item.Type, Message: p.redactor.Redact(item.Text)})
	case itemError:
		// A warning the CLI carries on past — a recorded one named configuration
		// settings it ignored, and the turn then completed. It is recorded and
		// does not end the turn; a turn that fails says so with turn.failed.
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type": message.Type,
			"item_type":     item.Type,
			"error":         truncate(p.redactor.Redact(item.Message)),
		})
	default:
		return p.parseUnrecognized(message.Type + " (" + item.Type + " item)")
	}
}

// parseTurnCompleted is the newer vocabulary's successful ending. It carries no
// text, so the reply is the last agent_message item before it, and it carries
// the turn's usage, which is written under the harness's names and also kept
// as the provider wrote it.
func (p *streamParser) parseTurnCompleted(message providerMessage) error {
	if len(message.TurnUsage) > 0 {
		var usage turnUsage
		if json.Unmarshal(message.TurnUsage, &usage) == nil {
			if mapped, measured := usage.harnessUsage(); measured {
				p.result.Usage = mapped
				p.providerUsage = message.TurnUsage
			}
		}
	}
	return p.parseTerminal(message, "", false)
}

// parseTurnFailed is the newer vocabulary's failed ending. Its prose is the
// error object's message when it has one, and the raw object otherwise, so a
// shape nobody recorded still reaches the dialect and the record.
func (p *streamParser) parseTurnFailed(message providerMessage) error {
	text := ""
	if len(message.TurnError) > 0 {
		var failure struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(message.TurnError, &failure) == nil && strings.TrimSpace(failure.Message) != "" {
			text = failure.Message
		} else {
			text = string(message.TurnError)
		}
	}
	if strings.TrimSpace(text) == "" {
		text = message.Message
	}
	return p.parseTerminal(message, p.redactor.Redact(text), true)
}

func (p *streamParser) parseAgentMessage(message providerMessage) error {
	p.result.FinalText = message.Message
	if err := p.emit(execution.EventAgentMessage, execution.ReplyPayload(message.Message)); err != nil {
		return err
	}
	// The prose reaches a watcher after the event that records it and after the
	// redaction above, in that order and never the other: nothing may be shown
	// that the record does not hold, and nothing may be shown before it has been
	// redacted.
	if p.reply != nil {
		p.reply(message.Message)
	}
	return nil
}

func (p *streamParser) parseTokenCount(message providerMessage) error {
	usage, measured := message.usage()
	if measured {
		p.result.Usage = usage
	}
	return p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type": message.Type,
		"usage":         rawOrNil(usage),
	})
}

// parseTerminal records the invocation's own ending, whichever way it ended.
//
// Codex prices nothing: it reports what an invocation read and wrote and never
// what it cost, so CostReported stays false and the terminal event carries no
// cost at all rather than a zero that would read as an invocation that spent
// nothing. What it does carry is the role, because a run's log holds several
// invocations and where a terminal sits in it is a fact about the order the
// harness happened to do things in.
func (p *streamParser) parseTerminal(message providerMessage, text string, failed bool) error {
	p.sawTerminal = true
	if strings.TrimSpace(text) != "" || failed {
		p.result.FinalText = text
	}
	p.result.IsError = failed
	p.result.StopReason = message.Type
	// The terminal is where the provider says how the invocation ended, so it is
	// the event whose answer decides whether this is a wait, another attempt, or
	// a refusal that stands. Which of those it is comes back from the dialect;
	// that they cannot stand together is held by the contract.
	p.observe(backend.ProviderEvent{
		Type:     message.Type,
		Subtype:  message.Type,
		Text:     p.result.FinalText,
		Terminal: true,
		Failed:   failed,
	})
	eventType := execution.EventRunCompleted
	if failed {
		eventType = execution.EventRunFailed
	}
	payload := map[string]any{
		"role":            string(p.role),
		"session_id":      p.result.SessionID,
		"is_error":        failed,
		"result":          truncate(p.result.FinalText),
		"terminal_reason": message.Type,
	}
	if len(p.result.Usage) > 0 {
		payload["usage"] = json.RawMessage(p.result.Usage)
	}
	if len(p.providerUsage) > 0 {
		payload["provider_usage"] = p.providerUsage
	}
	return p.emit(eventType, payload)
}

// recordAfterTerminal keeps what the provider said after it had already ended
// the invocation. Nothing here is written to the result: a second terminal is
// recorded as the anomaly it is and never replaces the first.
func (p *streamParser) recordAfterTerminal(message providerMessage) error {
	payload := map[string]any{"provider_type": message.Type}
	if message.Type == eventTaskComplete || message.Type == eventError || message.Type == eventTurnCompleted || message.Type == eventTurnFailed {
		payload["anomaly"] = "terminal_after_terminal"
	}
	return p.emit(execution.EventProcessOutput, payload)
}

// observe hands one provider event to this provider's dialect and records
// whatever answer comes back on the invocation's result. It is the only way an
// answer reaches the result, so nothing in this parser decides what a provider
// said and nothing above it special-cases this provider.
func (p *streamParser) observe(event backend.ProviderEvent) {
	if p.dialect == nil {
		return
	}
	observation, said := p.dialect.Observe(event)
	if !said {
		return
	}
	// Where the provider said it is the event's fact rather than the dialect's
	// claim, so it is written here, after the answer and before the record.
	observation.Channel = event.Channel
	observation.Record(&p.result)
}

// redactMessage removes the values that must not become a durable record from
// everything the provider authored. The event type is left alone: it is a
// control enum used for dispatch rather than provider prose, and redacting it
// could corrupt parsing if a poorly chosen credential happened to equal one.
func (p *streamParser) redactMessage(message *providerMessage) {
	message.SessionID = p.redactor.Redact(message.SessionID)
	message.ThreadID = p.redactor.Redact(message.ThreadID)
	message.Model = p.redactor.Redact(message.Model)
	message.Message = p.redactor.Redact(message.Message)
	message.LastAgentMessage = p.redactor.Redact(message.LastAgentMessage)
	message.CallID = p.redactor.Redact(message.CallID)
	message.Server = p.redactor.Redact(message.Server)
	message.Tool = p.redactor.Redact(message.Tool)
	message.Stdout = p.redactor.Redact(message.Stdout)
	message.Stderr = p.redactor.Redact(message.Stderr)
	for index := range message.Command {
		message.Command[index] = p.redactor.Redact(message.Command[index])
	}
}

func (p *streamParser) emit(eventType execution.EventType, payload any) error {
	event, err := execution.NewEvent(p.runID, p.sequence.Next(), p.clock.Now(), eventType, sourceName, payload)
	if err != nil {
		return err
	}
	p.result.LastEvent = event.Sequence
	if p.sink != nil {
		if err := p.sink(event); err != nil {
			return fmt.Errorf("persist normalized event: %w", err)
		}
	}
	return nil
}

// Result is the invocation's own answer. Everything in it is settled as the
// stream arrives, so there is nothing left to decide here.
func (p *streamParser) Result() backend.RunResult {
	return p.result
}

func (p *streamParser) SawTerminal() bool {
	return p.sawTerminal
}

// FirstUnrecognized is the type of the first event this parser did not know,
// read before any terminal, and empty when every event was one it knows.
func (p *streamParser) FirstUnrecognized() string {
	return p.unrecognized
}

// decodeMessage reads one line into the event it carries. The older vocabulary
// puts the event under `msg` and the newer one writes it bare; both are read,
// and which of the two it was is kept on the message.
func decodeMessage(line string) (providerMessage, bool) {
	var envelope streamEnvelope
	if json.Unmarshal([]byte(line), &envelope) != nil {
		return providerMessage{}, false
	}
	body := envelope.Msg
	if len(body) == 0 {
		body = json.RawMessage(line)
	}
	var message providerMessage
	if json.Unmarshal(body, &message) != nil || strings.TrimSpace(message.Type) == "" {
		return providerMessage{}, false
	}
	message.enveloped = len(envelope.Msg) > 0
	return message, true
}

// toolName is what the harness's command event calls the thing that ran: the
// shell for a command, the MCP tool for a tool call, and the patch applier for
// an edit. The command itself is not recorded, only how big it was — a shell
// line is provider-authored text that may quote anything in the worktree.
func toolName(message providerMessage) string {
	switch message.Type {
	case eventMCPToolCallBegin, eventMCPToolCallEnd:
		if message.Server != "" {
			return message.Server + "." + message.Tool
		}
		return message.Tool
	case eventPatchApplyBegin, eventPatchApplyEnd:
		return "apply_patch"
	default:
		return "shell"
	}
}

func inputBytes(message providerMessage) int {
	if len(message.Changes) > 0 {
		return len(message.Changes)
	}
	total := 0
	for _, word := range message.Command {
		total += len(word)
	}
	return total
}

// callFailed reports a shell, tool, or patch call that ended badly, from
// whichever of the two ways the provider says so. A call that reported neither
// is not called a failure, because absence is not the same as a non-zero exit.
func callFailed(message providerMessage) bool {
	switch {
	case message.ExitCode != nil:
		return *message.ExitCode != 0
	case message.Success != nil:
		return !*message.Success
	default:
		return false
	}
}

// rawOrNil keeps an absent usage object absent in the event payload rather than
// writing an empty one, which would be a measurement of nothing.
func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func truncate(value string) string {
	return execution.TruncateEventText(value)
}
