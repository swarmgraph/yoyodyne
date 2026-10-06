package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The bound is the harness's, shared with the operator's side of a conversation
// it records itself, so the two halves of one exchange are cut to one rule.
const maxEventTextBytes = execution.MaxEventTextBytes

type streamParser struct {
	runID string
	// role is who this invocation was made as, and it is written onto the
	// terminal event so the record says whose invocation it priced rather than
	// leaving that to be inferred from where the terminal sits in the log. See
	// the terminal event types in internal/execution for what reads it.
	role domain.AgentRole
	// loaded is what the invocation was given beside its prompt, said on the
	// event that starts it so the run's log carries it for every role.
	loaded   backend.Loaded
	sequence *execution.Sequence
	clock    execution.Clock
	redactor execution.Redactor
	sink     func(execution.Event) error
	// dialect is how what this provider says is read. It is a field rather than a
	// package-level call so the parser depends on the contract rather than on
	// this provider: swapping it is how a test drives the parser with a
	// provider's answers and nothing else about the provider.
	dialect backend.Dialect
	// reply is where the agent's prose goes for whoever is watching it arrive.
	// It is nil when nobody is, which is every invocation the harness makes on
	// its own behalf.
	reply     func(string)
	result    backend.RunResult
	sawResult bool
	// stderr is what the process wrote to its error stream, and stdout is what
	// it wrote to its output stream before any envelope without its being one,
	// each kept bounded so it can be handed to the dialect once the stream has
	// ended. Both are read only when no terminal arrived — see
	// ObservePlainOutput — so for every invocation that ended the way the
	// provider ends one they are held and never consulted.
	stderr strings.Builder
	stdout strings.Builder
	// sawEnvelope says the stream has carried at least one line this parser
	// could read as an envelope. A plain line before that is held as stdout
	// above; a plain line after it is the decode error it always was.
	sawEnvelope bool
	// duplicateTerminal is what to say about an invocation the provider ended
	// more than once, and empty when it ended once. It is held rather than
	// applied because what a duplicate asks the caller for depends on the whole
	// stream: a refusal can be reported after it, and a refusal already carries
	// its own answer. Result decides between them when there is nothing left to
	// arrive.
	duplicateTerminal string
	// reportedCostUSD is the last figure a terminal of this invocation reported,
	// and duplicateCostUSD what every terminal after the first added to it by the
	// session-increment rule. They are kept beside the result rather than in it
	// because nothing off a duplicate is written into the decided result; Result
	// adds the increments on, since the provider charged for them either way.
	reportedCostUSD  float64
	duplicateCostUSD float64
}

type streamEnvelope struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	SessionID      string          `json:"session_id"`
	IsError        bool            `json:"is_error"`
	Result         string          `json:"result"`
	StopReason     string          `json:"stop_reason"`
	TerminalReason string          `json:"terminal_reason"`
	TotalCostUSD   float64         `json:"total_cost_usd"`
	Usage          json.RawMessage `json:"usage"`
	Message        json.RawMessage `json:"message"`
	Model          string          `json:"model"`
	PermissionMode string          `json:"permissionMode"`
	Tools          []string        `json:"tools"`
	Capabilities   []string        `json:"capabilities"`
	Attempt        int             `json:"attempt"`
	MaxRetries     int             `json:"max_retries"`
	Error          string          `json:"error"`
	Output         string          `json:"output"`
	// RateLimitInfo is the payload of a rate_limit_event, kept whole rather than
	// reduced to the few fields the harness reads from it. It is the only
	// evidence anybody has of what the provider says when capacity runs out, and
	// discarding the parts this version does not act on is what made the
	// question unanswerable the last time it was asked.
	RateLimitInfo json.RawMessage `json:"rate_limit_info"`
}

// Everything this parser used to know about rate limits, retries, and reset
// times now lives in dialect.go, as one implementation of the provider contract
// in internal/backend. What stays here is reading the stream: which envelope is
// the invocation's terminal, what goes into the event log, and what the
// invocation's result is. What each thing the provider said *means* is the
// dialect's, and what to do about it is the harness's.

type message struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// newStreamParser reads one invocation's stream with the dialect it is given.
// The dialect is a parameter rather than this file's own choice because a
// project may declare a provider that speaks this adapter's protocol and reports
// its limits, retries, and reset times in some other spelling: the adapter runs
// the process, and what the process said is read by whichever dialect the
// registry resolved for the backend the agent named. Nothing means this
// provider's own.
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

func (p *streamParser) ParseLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	var envelope streamEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return p.plainLine(line, fmt.Errorf("decode stream event: %w", err))
	}
	if envelope.Type == "" {
		return p.plainLine(line, errors.New("decode stream event: type is required"))
	}
	p.sawEnvelope = true
	if err := p.redactEnvelope(&envelope); err != nil {
		return err
	}
	// Not every result envelope is the invocation's result. A nested agent ends
	// in one too, so the guard below is about the terminal rather than about the
	// envelope type: a nested result is recorded as the stream noise it is and
	// never counts as a terminal at all.
	if envelope.Type == "result" && nestedAgentResult(envelope) {
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
			"is_error":         envelope.IsError,
			"total_cost_usd":   envelope.TotalCostUSD,
			"usage":            json.RawMessage(envelope.Usage),
		})
	}
	// A terminal result decides the run's outcome, but the provider keeps
	// writing after it. Trailing events are recorded so their payload stays
	// diagnosable; they must not disturb the decided result, and the guarded
	// invariant is that a second terminal result cannot replace the first.
	if p.sawResult && envelope.Type == "result" {
		return p.recordDuplicateTerminal(envelope)
	}
	if !p.sawResult && envelope.SessionID != "" {
		p.result.SessionID = envelope.SessionID
	}

	switch envelope.Type {
	case systemEventType:
		return p.parseSystem(envelope)
	case "assistant", "user":
		return p.parseMessage(envelope.Type, envelope.Message)
	case "result":
		return p.parseResult(envelope)
	case rateLimitEventType:
		return p.parseRateLimit(envelope)
	default:
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
		})
	}
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
// that went on running after its final reply: waiting on it, and then which
// way it ended. It is its own line rather than a stderr one, so it is never
// read as the provider's prose.
func (p *streamParser) RecordAfterReply(account execution.AfterReply) error {
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream":      "harness",
		"text":        account.Describe(p.clock.Now()),
		"after_reply": account,
	})
}

// plainLine is a stdout line that is not an envelope, and the decode error it
// earned. Before any envelope it is held as plain stdout for ObservePlainOutput
// and recorded, because a CLI that refuses before it writes anything
// structured may say so there as readily as on stderr — and until
// yoyodyne-ifd.400 such a refusal failed the invocation as a stream decode
// error before stderr was ever read, which is 2026-09-17 replayed through a
// different gap. The error is still returned: whether it fails the invocation
// is decided once the process has ended, by whether the held text was the
// refusal, so a stream this parser genuinely cannot read still fails the run
// with the parse error it always did. After an envelope nothing is held: the
// provider was writing its stream, and a line in it that cannot be read is the
// decode error it always was.
func (p *streamParser) plainLine(line string, decodeErr error) error {
	if p.sawEnvelope {
		return decodeErr
	}
	text := p.redactor.Redact(line)
	keepPlain(&p.stdout, text)
	if err := p.emit(execution.EventProcessOutput, map[string]any{
		"stream": execution.StreamStdout,
		"text":   truncate(text),
	}); err != nil {
		return err
	}
	return decodeErr
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
// stderr, and then the plain stdout it wrote before any envelope — as one event
// per channel, and records whatever it answers. It is for the stream that
// ended without a terminal of its own: a CLI that refuses an expired login
// before it writes a single envelope says so on one of the two and exits, and a
// dialect that read only envelopes left that invocation an unclassified process
// failure — which relaunches into the same login, spends the budget, and
// blocks, exactly as 2026-09-17 did. The caller asks this only in that case,
// so a terminal the provider did write is never second-guessed by its
// diagnostics, and a process that wrote nothing is asked nothing.
//
// Stderr is read first, and stdout only when stderr answered nothing, so that
// a CLI which said the same thing on both leaves one channel on the record
// rather than whichever was recorded last. Stderr goes first because it is
// where a CLI's refusal conventionally goes and where the first classified one
// was read; a refusal on stdout is the CLI writing prose where its stream
// should have been.
func (p *streamParser) ObservePlainOutput() {
	if p.observePlain(domain.ProviderChannelStderr, p.stderr.String()) {
		return
	}
	p.observePlain(domain.ProviderChannelStdout, p.stdout.String())
}

// observePlain hands one plain channel to the dialect and reports whether it
// answered with a wait. Text is redacted as it was held and trimmed here, so a
// channel that carried only whitespace is one the dialect is never asked about.
func (p *streamParser) observePlain(channel domain.ProviderChannel, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	before := p.result.ProviderOutage
	p.observe(backend.ProviderEvent{Channel: channel, Text: text})
	return p.result.ProviderOutage != before
}

// ReadRefusalOffPlainOutput reports a wait the dialect read off one of the two
// plain channels. It is what lets the adapter tell a stream it could not
// decode from a refusal the CLI wrote in place of one: the lines that failed
// to decode were the refusal, and the invocation has been answered by it.
func (p *streamParser) ReadRefusalOffPlainOutput() bool {
	return p.result.ProviderOutage != nil && p.result.ProviderOutage.Channel.Plain()
}

// truncatedStreamLine is the harness's own name for a provider line the process
// runner had to cut at its per-line bound. It sits beside the duplicate terminal
// below as an anomaly rather than a failure: what that envelope said is gone,
// and everything before and after it is intact.
const truncatedStreamLine = "truncated_stream_line"

// RecordTruncatedLine records a cut line loudly and reads nothing off it.
//
// This is the decision a cut line forces, and it is skip-with-loud-record rather
// than die. A truncated line is invalid JSON by construction, so handing it to
// ParseLine could only ever produce a decode error — and the one thing a decode
// error has to keep meaning is a stream this parser genuinely cannot read, which
// still fails the run the way it always did. Telling the two apart is why the
// runner marks the line rather than leaving the shape of the failure to be
// guessed at. Failing the invocation over one line the harness could not hold is
// the death yoyodyne-ifd.258 stopped the whole-output bound causing, and there
// is no more reason to accept it here: an oversized tool result is a run doing
// its work, and losing the run loses the work and the account of what it cost.
//
// What is lost with the envelope is whatever it said. If it was the invocation's
// terminal, the stream ends without one and the invocation is answered the way
// every other lost terminal already is; that is the existing answer rather than
// a new one, and the anomaly in the log is what says why the terminal is missing.
func (p *streamParser) RecordTruncatedLine(output execution.Output) error {
	return p.emit(execution.EventProcessOutput, map[string]any{
		"stream":  output.Stream,
		"anomaly": truncatedStreamLine,
		"text":    truncate(p.redactor.Redact(output.Text)),
	})
}

func (p *streamParser) parseSystem(envelope streamEnvelope) error {
	switch envelope.Subtype {
	case "init":
		// The init event is where the provider names the model it resolved the
		// requested selector to. It is recorded as first-class result evidence
		// rather than left buried in the event payload.
		if !p.sawResult && envelope.Model != "" {
			p.result.ResolvedModel = envelope.Model
		}
		payload := map[string]any{
			"session_id":      envelope.SessionID,
			"model":           envelope.Model,
			"permission_mode": envelope.PermissionMode,
			"tools":           envelope.Tools,
			"capabilities":    envelope.Capabilities,
		}
		if p.loaded.Reported {
			payload["loaded"] = p.loaded.String()
		}
		return p.emit(execution.EventRunStarted, payload)
	case apiRetrySubtype:
		// The provider is retrying by itself. The dialect is asked anyway, so the
		// answer for a retry in progress is the contract's rather than this
		// parser's silence -- what it earns the run is nothing, and that is a
		// statement the contract makes rather than one this branch makes by
		// leaving the result alone.
		p.observe(backend.ProviderEvent{Type: envelope.Type, Subtype: envelope.Subtype})
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
			"attempt":          envelope.Attempt,
			"max_retries":      envelope.MaxRetries,
			"error":            envelope.Error,
		})
	default:
		return p.emit(execution.EventProcessOutput, map[string]any{
			"provider_type":    envelope.Type,
			"provider_subtype": envelope.Subtype,
			"error":            truncate(envelope.Error),
			"output":           truncate(envelope.Output),
		})
	}
}

// parseRateLimit records what the provider said about capacity. The whole
// payload goes into the event stream, not the handful of fields the dialect
// reads from it: a limit nobody has seen the shape of cannot be diagnosed from
// an event that already threw the shape away.
//
// What the payload means is the dialect's answer, and what a payload it cannot
// read means is nothing at all: the same event reports healthy utilization far
// more often, so a report that cannot be read must not become exhaustion, and it
// is not a reason to fail the stream either.
func (p *streamParser) parseRateLimit(envelope streamEnvelope) error {
	if err := p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type":   envelope.Type,
		"rate_limit_info": json.RawMessage(envelope.RateLimitInfo),
	}); err != nil {
		return err
	}
	p.observe(backend.ProviderEvent{Type: envelope.Type, Payload: envelope.RateLimitInfo})
	return nil
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

func (p *streamParser) parseMessage(messageType string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s event is missing message", messageType)
	}
	var value message
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode %s message: %w", messageType, err)
	}
	for _, block := range value.Content {
		block.Text = p.redactor.Redact(block.Text)
		block.ID = p.redactor.Redact(block.ID)
		block.Name = p.redactor.Redact(block.Name)
		block.ToolUseID = p.redactor.Redact(block.ToolUseID)
		switch block.Type {
		case "text":
			if messageType == "assistant" {
				if !p.sawResult {
					p.result.FinalText = block.Text
				}
				// A reply is held to its own bound and a cut past it is declared on
				// the event, because this record is where a ruling lives.
				if err := p.emit(execution.EventAgentMessage, execution.ReplyPayload(block.Text)); err != nil {
					return err
				}
				// The prose reaches a watcher after the event that records it and
				// after the redaction above, in that order and never the other:
				// nothing may be shown that the record does not hold, and nothing
				// may be shown before it has been redacted. It is handed over whole
				// rather than cut the way the event is past its bound — a bound
				// that exists to keep the event log readable is not a reason to
				// show the operator half of what they were told.
				if p.reply != nil {
					p.reply(block.Text)
				}
			}
		case "tool_use":
			if err := p.emit(execution.EventCommandStarted, map[string]any{
				"tool_use_id": block.ID,
				"tool":        block.Name,
				"input_bytes": len(block.Input),
			}); err != nil {
				return err
			}
		case "tool_result":
			if err := p.emit(execution.EventCommandCompleted, map[string]any{
				"tool_use_id":   block.ToolUseID,
				"is_error":      block.IsError,
				"content_bytes": len(block.Content),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *streamParser) redactEnvelope(envelope *streamEnvelope) error {
	// Type and subtype are provider control enums used for dispatch, not
	// provider-authored text. Redacting them could corrupt parsing if a poorly
	// chosen credential happened to equal an enum such as "result".
	envelope.SessionID = p.redactor.Redact(envelope.SessionID)
	envelope.Result = p.redactor.Redact(envelope.Result)
	envelope.StopReason = p.redactor.Redact(envelope.StopReason)
	envelope.TerminalReason = p.redactor.Redact(envelope.TerminalReason)
	envelope.Model = p.redactor.Redact(envelope.Model)
	envelope.PermissionMode = p.redactor.Redact(envelope.PermissionMode)
	envelope.Error = p.redactor.Redact(envelope.Error)
	envelope.Output = p.redactor.Redact(envelope.Output)
	for index := range envelope.Tools {
		envelope.Tools[index] = p.redactor.Redact(envelope.Tools[index])
	}
	for index := range envelope.Capabilities {
		envelope.Capabilities[index] = p.redactor.Redact(envelope.Capabilities[index])
	}
	usage, err := p.redactJSONStrings(envelope.Usage)
	if err != nil {
		return fmt.Errorf("redact provider usage: %w", err)
	}
	envelope.Usage = usage
	rateLimit, err := p.redactJSONStrings(envelope.RateLimitInfo)
	if err != nil {
		return fmt.Errorf("redact provider rate limit: %w", err)
	}
	envelope.RateLimitInfo = rateLimit
	return nil
}

func (p *streamParser) redactJSONStrings(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value = redactJSONValue(value, p.redactor)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func redactJSONValue(value any, redactor execution.Redactor) any {
	switch typed := value.(type) {
	case string:
		return redactor.Redact(typed)
	case []any:
		for index := range typed {
			typed[index] = redactJSONValue(typed[index], redactor)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = redactJSONValue(item, redactor)
		}
		return typed
	default:
		return value
	}
}

// nestedAgentResult reports a result envelope that ends something inside the
// invocation rather than the invocation itself. An agent may spawn subagents,
// and a subagent's completion arrives in the parent's stream as another
// type:result envelope; nothing in it says whose result it is, so the two are
// told apart by shape.
//
// Provenance. Run run-841f5ee1866addb533c02a30e67f001a, developing
// yoyodyne-ifd.60 on 2026-08-18, recorded one at sequence 1186 while its
// developer had subagents outstanding: is_error false, empty result, empty
// terminal_reason, no cost, and an all-zero usage object, arriving sixteen
// milliseconds after a system init and sixteen before another. It is the only
// envelope of that shape in the local run history; the 218 genuine terminals
// beside it all carry a terminal_reason and result text. The parser of the day
// read it as this invocation's terminal, so the real terminal three minutes
// later tripped the one-terminal guard and killed a run whose work was already
// complete in the worktree.
//
// The test is deliberately narrow, because both mistakes cost the same thing —
// a nested result read as terminal loses the real outcome, and so does a
// terminal read as nested — and only one of them has ever been observed. An
// envelope carrying either mark is therefore still the terminal it has always
// been, and only one carrying neither is noise.
//
// This is Claude Code's dialect like the rest of this file, and it is one more
// thing yoyodyne-ifd.32 has to name: what generalizes is that a provider which
// can nest agents emits results that are not the run's, and that a contract
// which identifies the terminal by envelope type alone cannot survive one.
func nestedAgentResult(envelope streamEnvelope) bool {
	return envelope.TerminalReason == "" && envelope.Result == ""
}

func (p *streamParser) parseResult(envelope streamEnvelope) error {
	p.sawResult = true
	p.result.SessionID = envelope.SessionID
	// A terminal result names the model only when no init event did; the model
	// the run started on stays authoritative.
	if p.result.ResolvedModel == "" && envelope.Model != "" {
		p.result.ResolvedModel = envelope.Model
	}
	p.result.FinalText = envelope.Result
	p.result.IsError = envelope.IsError
	p.result.CostUSD = envelope.TotalCostUSD
	p.reportedCostUSD = envelope.TotalCostUSD
	// The terminal is where Claude Code prices the invocation, so reaching one is
	// exactly what makes the cost known. An invocation that never reaches one --
	// a killed process, a connection that went away mid-reply -- leaves this
	// false, and what it spent is recorded as unknown rather than as nothing.
	p.result.CostReported = true
	p.result.Usage = append([]byte(nil), envelope.Usage...)
	p.result.StopReason = envelope.TerminalReason
	if p.result.StopReason == "" {
		p.result.StopReason = envelope.StopReason
	}
	// The terminal is where the provider says how the invocation ended, so it is
	// the event whose answer decides whether this is a wait, another attempt, or
	// a refusal that stands. Which of those it is comes back from the dialect;
	// that they cannot stand together is held by the contract.
	p.observe(backend.ProviderEvent{
		Type:     envelope.Type,
		Subtype:  p.result.StopReason,
		Text:     p.result.FinalText,
		Terminal: true,
		Failed:   p.result.IsError,
	})
	eventType := execution.EventRunCompleted
	if envelope.IsError {
		eventType = execution.EventRunFailed
	}
	return p.emit(eventType, map[string]any{
		// The role is the invocation saying whose it was. It costs the stream
		// nothing and it is the whole of what makes the cost beside it
		// attributable: a log holding several invocations otherwise says only
		// where each terminal sits, and where a terminal sits is a fact about
		// the order the harness happened to do things in.
		"role":            string(p.role),
		"session_id":      envelope.SessionID,
		"is_error":        envelope.IsError,
		"result":          truncate(envelope.Result),
		"total_cost_usd":  envelope.TotalCostUSD,
		"usage":           json.RawMessage(envelope.Usage),
		"terminal_reason": envelope.TerminalReason,
	})
}

// duplicateTerminalReason is the harness's own name for an invocation the
// provider ended twice. Neither of the reasons the provider gave can be it:
// which of the two endings was this invocation's is precisely what a second
// terminal makes unanswerable, so the recorded reason says that rather than
// picking one. Both of the provider's own reasons are kept beside it in the
// anomaly event, whichever answer the invocation ends up carrying.
const duplicateTerminalReason = execution.DuplicateTerminalAnomaly

// recordDuplicateTerminal records a second terminal result and what it makes of
// the invocation. The answer it leads to is a relaunch against the run's own
// budget, in the same worktree and the same session — the way every other
// provider death that judged nothing is answered — but that is settled in
// Result rather than here, because a refusal reported later carries an answer
// of its own.
//
// The decided result still stands — nothing off the second envelope is written
// into it, so the guarded invariant that a duplicate cannot replace the first
// terminal holds — but it stops being trusted as the invocation's outcome, and
// what the duplicate cost is added to what the invocation cost. The
// nested-agent case above is why: a subagent completion that carries a
// terminal's marks is read as this invocation's terminal, and the real terminal
// then arrives as the duplicate, so the result already recorded may be a
// subagent's rather than the run's.
//
// This used to fail the stream, which failed the run. Run run-e2b8d016,
// developing yoyodyne-ifd.117.1 on 2026-08-23, died that way mid-development and
// its near-complete change had to be recovered by a triage rerun at triage-grant
// price — for an anomaly that judged nothing about the work and that one
// relaunch in the same session absorbs. A provider's dialect drifting is a
// relaunch condition, not a fatality; a stream this parser genuinely cannot read
// still fails the run with the parse error it always did.
func (p *streamParser) recordDuplicateTerminal(envelope streamEnvelope) error {
	duplicate := envelope.TerminalReason
	if duplicate == "" {
		duplicate = envelope.StopReason
	}
	// The whole of the duplicate goes into the event stream, like every other
	// envelope arriving after the terminal, because what a dialect drifted into
	// cannot be diagnosed from a record that kept only the fact that it drifted.
	//
	// The role and the session are recorded on it for the reason they are on a
	// terminal: the pricing in internal/runstate reads the cost here as more of
	// this invocation's, and the session is what says which running total the
	// figure is an increment over.
	if err := p.emit(execution.EventProcessOutput, map[string]any{
		"provider_type":    envelope.Type,
		"provider_subtype": envelope.Subtype,
		"anomaly":          duplicateTerminalReason,
		"role":             string(p.role),
		"session_id":       envelope.SessionID,
		"is_error":         envelope.IsError,
		"result":           truncate(envelope.Result),
		"terminal_reason":  envelope.TerminalReason,
		"total_cost_usd":   envelope.TotalCostUSD,
		"usage":            json.RawMessage(envelope.Usage),
	}); err != nil {
		return err
	}
	// The turn that produced the duplicate was charged for, so its cost is this
	// invocation's too. It is priced by the rule every terminal is priced by
	// (runstate.OwnCostUSD): Claude Code reports the session's running total, so
	// what the turn cost is what the total moved by since this invocation's last
	// terminal, and a figure below that one is a total that restarted and counts
	// whole. On run-f3755e3f that was $0.40 to $0.88 a time that no cost surface
	// counted, because only the anomaly event carried it.
	if envelope.TotalCostUSD < p.reportedCostUSD {
		p.duplicateCostUSD += envelope.TotalCostUSD
	} else {
		p.duplicateCostUSD += envelope.TotalCostUSD - p.reportedCostUSD
	}
	p.reportedCostUSD = envelope.TotalCostUSD
	// The decided terminal's own reason is read off the result rather than kept
	// beside it, which it can be because nothing here writes to the result: a
	// third terminal names the ending the first one gave, not the anomaly the
	// second one caused.
	p.duplicateTerminal = fmt.Sprintf("the provider ended this invocation twice, first with %s and again with %s",
		terminalReasonName(p.result.StopReason), terminalReasonName(duplicate))
	return nil
}

// terminalReasonName names a terminal reason for the record, including the case
// where the provider named none. An ending nobody named is still one of the two
// this invocation was given, and dropping it would leave the anomaly reading as
// though only one terminal had arrived.
func terminalReasonName(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no terminal reason"
	}
	return fmt.Sprintf("%q", reason)
}

func (p *streamParser) emit(eventType execution.EventType, payload any) error {
	event, err := execution.NewEvent(p.runID, p.sequence.Next(), p.clock.Now(), eventType, string(domainBackend), payload)
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

// Result is the invocation's own answer, decided once the stream has ended.
//
// Everything but the duplicate terminal is settled as it arrives. That one is
// not, because the two answers it must not stand beside are both still moving
// while the stream runs: a usage limit is re-reported as it changes, and a
// serving report supersedes an exhausted one, so which refusals this invocation
// is carrying is a fact about the whole stream rather than about the moment the
// duplicate showed up. Deciding here is what makes the exclusivity a
// construction instead of a race between two envelopes.
//
// An exhausted limit and an overload are the refusals the harness answers with a
// wait, and a wait costs a relaunch nothing: it reissues into the same worktree
// and the same session the relaunch would have continued, without spending an
// attempt on a provider that has already said it will not serve one. So a
// duplicate that arrives beside either of them leaves the answer to it, and adds
// only what it alone knows — that this invocation is not to be trusted to have
// produced one.
func (p *streamParser) Result() backend.RunResult {
	result := p.result
	if p.duplicateTerminal == "" {
		return result
	}
	result.IsError = true
	result.StopReason = duplicateTerminalReason
	// The figure the meter prices against the session's last one is the first
	// terminal's plus what every later terminal added. Where the provider's total
	// only rose, which is every duplicate recorded so far, that is exactly the
	// last terminal's figure, so the session's next invocation is priced against
	// the total the provider last reported.
	result.CostUSD += p.duplicateCostUSD
	if result.UsageLimit == nil && result.ServerOverload == nil {
		result.TransientFailure = &backend.TransientFailure{Detail: p.duplicateTerminal}
	}
	return result
}

func (p *streamParser) SawResult() bool {
	return p.sawResult
}

// SawUsageLimit reports a stream that told the dialect a limit is refusing
// work. A stream that said so and then ended without a terminal has already
// been answered, and is not one whose stderr should be read for a second one.
func (p *streamParser) SawUsageLimit() bool {
	return p.result.UsageLimit != nil
}

const domainBackend = "claude-code"

func truncate(value string) string {
	return execution.TruncateEventText(value)
}
