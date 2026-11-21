package intake

// One run of a query (docs/designs/scheduled-message-intake.md, sections 1, 2,
// 4 and 9).
//
// A query is a recurring task: one run for each window of its schedule,
// recorded before anything is spent, skipped while its previous run is still
// working, and holding no developer slot. A run reads from the time the query
// has read up to, never closer than ten minutes to now, one time window to a
// call:
//
//   - an answer that is cut, by the call path's 32 KiB or by the source saying
//     it held more back, writes nothing down and halves the window, asking
//     again from the same start;
//   - an answer that fits has every message in it written down, then moves
//     the time read up to to the window's end, and the next window is twice
//     the size of this one;
//   - a one-minute window still cut is recorded as skipped, the time read up
//     to moves past it, and one warning goes to the development manager.
//
// A run makes at most five calls, the MCP design's limit for one batch, and
// the next run carries on from where it stopped, so a busy hour or days of
// catching up move forward with nobody changing anything.
//
// The arguments the harness fills with a window's start and end are times
// written in RFC 3339, in UTC, to the second.
//
// No provider is called here at all: what is written down waits for the
// message reader, which is a later piece of the design. A message from a
// sender the query does not list is ignored here, before any reader could see
// it, and its text is written nowhere.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const (
	// Settle is how close to now a run reads: a message that arrives late at
	// its source has settled before it is read, and no time is read twice.
	Settle = 10 * time.Minute
	// MinimumWindow is the smallest window a run asks for. A minute still too
	// large to read is skipped rather than stopping the query for good.
	MinimumWindow = time.Minute
	// MaximumWindow bounds how far the window grows by doubling, so a query
	// that has been quiet for a long time still asks for a stretch a busy
	// source could answer in a few halvings.
	MaximumWindow = 7 * 24 * time.Hour
)

// MaxCallsPerRun is how many calls one run makes: the MCP design's limit for a
// batch, which is a scheduled caller's calls in one reply.
var MaxCallsPerRun = mcpcall.DefaultLimits(mcpcall.CallerScheduled).CallsPerReply

// FailedRunsBeforeWarning is how many runs in a row fail before one warning is
// filed.
const FailedRunsBeforeWarning = 3

// Calls is the one MCP call path. *mcpcall.Path is it.
type Calls interface {
	Do(ctx context.Context, caller mcpcall.Caller, requests []mcpcall.Request) ([]mcpcall.Answer, error)
}

var _ Calls = (*mcpcall.Path)(nil)

// Runner runs the configuration's queries. Config, Records, Calls and Hold are
// required.
type Runner struct {
	Config  config.Config
	Records *Records
	Calls   Calls
	// Hold is the operator's pause, read before a run spends anything.
	Hold mcpcall.Hold
	// Notes is where the harness's own warnings are filed, under Attribution's
	// product, repository and build. Nil files none.
	Notes       mcpcall.Notes
	Attribution report.Attribution
	// Now is the clock; nil is the machine's.
	Now func() time.Time
}

// running is every query with a run working in this process, by its record
// and name. It is held for the process rather than the runner, because the
// work session builds what it calls with afresh at every pull.
var running sync.Map

// Result is how one run went.
type Result struct {
	Query string
	// Outcome is how it ended; empty where it did not run.
	Outcome RunOutcome
	// Busy says the query's previous run is still working, so this one did not
	// start; AlreadyRan says a run of this schedule window already started.
	Busy, AlreadyRan bool
	Failure          Failure
	Calls            int
	// Messages is how many messages this run recorded for the first time.
	Messages int
	// Skipped is the minutes this run skipped as too large to read.
	Skipped []Window
	// Reason says, in words, why it failed or what it found.
	Reason string
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Slot is the schedule window a query's run at now belongs to.
func Slot(query config.IntakeQuery, now time.Time) time.Time {
	every := query.Every.Duration()
	if every <= 0 {
		every = config.DefaultIntakeEvery.Duration()
	}
	return now.UTC().Truncate(every)
}

// Due is every query whose run for the schedule window now falls in has not
// started, in name order.
func (r *Runner) Due(now time.Time) ([]string, error) {
	if !r.Config.Intake.Declared() {
		return nil, nil
	}
	state, err := r.Records.Read()
	if err != nil {
		return nil, err
	}
	var due []string
	for _, name := range queryNames(r.Config.Intake) {
		if state.Query(name).LastSlot.Before(Slot(r.Config.Intake.Queries[name], now)) {
			due = append(due, name)
		}
	}
	return due, nil
}

// Run runs one query once. Its error is for what the record could not keep;
// how the run went is in the result, and a failed run is a result, not an
// error.
func (r *Runner) Run(ctx context.Context, name string) (Result, error) {
	result := Result{Query: name}
	query, ok := r.Config.Intake.Queries[name]
	if !ok {
		return result, fmt.Errorf("there is no intake query %q in the configuration", name)
	}
	if r.Records == nil || r.Calls == nil || r.Hold == nil {
		return result, errors.New("an intake query run needs its record, the MCP call path, and the operator's pause")
	}
	key := r.Records.lockKey(name)
	if _, busy := running.LoadOrStore(key, true); busy {
		result.Busy = true
		return result, nil
	}
	defer running.Delete(key)

	state, err := r.Records.Read()
	if err != nil {
		return result, err
	}
	now := r.now()
	slot := Slot(query, now)
	standing := state.Query(name)
	if !standing.LastSlot.Before(slot) {
		result.AlreadyRan = true
		return result, nil
	}
	run := &queryRun{runner: r, name: name, query: query, state: state, standing: standing, result: &result,
		id: name + "@" + slot.Format(time.RFC3339)}
	// The run is recorded before anything is spent, so a run that is killed
	// part-way is still the one instance of its window.
	if err := r.Records.append(Line{Kind: KindRun, Query: name, Run: run.id, Slot: slot, Phase: PhaseStarted, At: now}); err != nil {
		return result, err
	}
	run.do(ctx, now)
	return result, run.end()
}

// queryRun is one run in progress.
type queryRun struct {
	runner   *Runner
	name     string
	query    config.IntakeQuery
	state    State
	standing QueryState
	result   *Result
	id       string
	problems []error
}

func (q *queryRun) do(ctx context.Context, now time.Time) {
	if _, held, err := q.runner.Hold.Held(); held || err != nil {
		q.result.Outcome, q.result.Reason = RunPaused, "the operator has paused the harness with `yoyo pause`, so nothing is read until it is lifted"
		if err != nil {
			q.result.Reason = "the operator's pause could not be read, and a pause nobody can read is treated as on, so nothing was read"
		}
		return
	}
	if q.standing.SwitchedOn.IsZero() {
		// The first run reads nothing: what the query reads starts now, so
		// switching it on never spends anything on old messages.
		if err := q.runner.Records.append(Line{Kind: KindSwitchedOn, Query: q.name, At: now}); err != nil {
			q.fail(FailureCall, "the moment the query was switched on could not be recorded: "+err.Error())
			return
		}
		q.result.Outcome, q.result.Reason = RunSwitchedOn, "the query is switched on; it reads messages sent from now on"
		return
	}
	caller, err := mcpcall.IntakeQueryCaller(q.runner.Config, q.name)
	if err != nil {
		q.fail(FailureCall, err.Error())
		return
	}
	readUpTo := q.standing.ReadUpTo
	size := q.standing.NextWindow
	if size <= 0 {
		size = q.query.Every.Duration()
	}
	edge := now.Add(-Settle).Truncate(time.Second)
	for q.result.Calls < MaxCallsPerRun {
		size = min(max(size, MinimumWindow), MaximumWindow)
		until := readUpTo.Add(size)
		if until.After(edge) {
			until = edge
		}
		if !until.After(readUpTo) {
			break
		}
		window := Window{From: readUpTo, Until: until}
		answer, ok := q.call(ctx, caller, window)
		if !ok {
			return
		}
		messages, more, problem := []sourceMessage(nil), false, ""
		if !answer.Cut {
			messages, more, problem = readAnswer(answer.Result, q.query)
			if problem != "" {
				q.fail(FailureShape, problem)
				return
			}
		}
		span := until.Sub(readUpTo)
		if answer.Cut || more {
			if span <= MinimumWindow {
				if !q.skip(window) {
					return
				}
				readUpTo, size = until, MinimumWindow
				continue
			}
			size = max(span/2, MinimumWindow)
			if err := q.runner.Records.append(Line{Kind: KindNarrowed, Query: q.name, Run: q.id, From: window.From, Until: window.Until, NextWindow: seconds(size)}); err != nil {
				q.fail(FailureCall, "the narrowed window could not be recorded: "+err.Error())
				return
			}
			continue
		}
		// Every message is written down before the time read up to moves, so a
		// run stopped in between asks for the same window again and finds the
		// messages it already wrote.
		for _, message := range messages {
			if err := q.write(message); err != nil {
				q.fail(FailureCall, "a message could not be recorded: "+err.Error())
				return
			}
		}
		size = min(2*size, MaximumWindow)
		if err := q.runner.Records.append(Line{Kind: KindWindow, Query: q.name, Run: q.id, From: window.From, Until: window.Until, NextWindow: seconds(size)}); err != nil {
			q.fail(FailureCall, "the window read could not be recorded: "+err.Error())
			return
		}
		readUpTo = until
	}
	q.result.Outcome = RunRead
	if q.result.Calls == 0 {
		q.result.Outcome, q.result.Reason = RunNothingDue, "no time old enough to read has passed since the last run"
	}
}

// call asks for one window. A call that failed was already tried once more by
// the call path where a read could be; ok is false when the run has failed.
func (q *queryRun) call(ctx context.Context, caller mcpcall.Caller, window Window) (mcpcall.Answer, bool) {
	arguments := make(map[string]any, len(q.query.Arguments)+2)
	for argument, value := range q.query.Arguments {
		arguments[argument] = value
	}
	arguments[q.query.SinceArgument] = window.From.UTC().Format(time.RFC3339)
	arguments[q.query.UntilArgument] = window.Until.UTC().Format(time.RFC3339)
	encoded, err := json.Marshal(arguments)
	if err != nil {
		q.fail(FailureCall, "the query's arguments could not be written as JSON: "+err.Error())
		return mcpcall.Answer{}, false
	}
	q.result.Calls++
	answers, err := q.runner.Calls.Do(ctx, caller, []mcpcall.Request{{Tool: q.query.Tool, Arguments: encoded, Why: "intake query " + q.name}})
	if err != nil {
		// The call record could not keep the call; whatever came back is not
		// used, because a call nobody can account for is not one to act on.
		q.fail(FailureCall, "the MCP call record could not keep the call: "+err.Error())
		return mcpcall.Answer{}, false
	}
	if len(answers) != 1 {
		q.fail(FailureCall, "the MCP call path gave no answer for the call")
		return mcpcall.Answer{}, false
	}
	answer := answers[0]
	switch {
	case answer.Refusal == mcpcall.RefusedPaused:
		q.result.Outcome, q.result.Reason = RunPaused, answer.Reason
		return answer, false
	case answer.Outcome != mcpcall.OutcomePerformed:
		q.fail(FailureCall, answer.Reason)
		return answer, false
	case answer.ToolError:
		q.fail(FailureCall, "the server answered that the tool failed")
		return answer, false
	}
	return answer, true
}

// skip records a one-minute window still too large to read, moves past it,
// and files one warning for the development manager.
func (q *queryRun) skip(window Window) bool {
	if err := q.runner.Records.append(Line{Kind: KindWindow, Query: q.name, Run: q.id, From: window.From, Until: window.Until,
		NextWindow: seconds(MinimumWindow), Skipped: true, Reason: "one minute of messages was still too large to read in one call"}); err != nil {
		q.fail(FailureCall, "the skipped minute could not be recorded: "+err.Error())
		return false
	}
	q.result.Skipped = append(q.result.Skipped, window)
	message := fmt.Sprintf("Intake query %s skipped the minute from %s to %s: even one minute of its messages was too large to read in one call, "+
		"because the answer was more than 32 KiB or the source said it held more back. The messages sent in that minute are not read; the query has moved on past it "+
		"and goes on reading. `yoyo status` counts the minutes skipped. If minutes keep being skipped, the query's arguments may need to ask for less, which is the operator's to change in the intake section.",
		q.name, localTime(window.From), localTime(window.Until))
	q.file(report.Entry{Severity: report.SeverityWarning, For: domain.RoleDevelopmentManager, Message: message}, "intake-skipped:"+q.name+"@"+window.From.Format(time.RFC3339))
	return true
}

// write records one message the query returned, once for its key whichever
// query returns it and however often.
func (q *queryRun) write(source sourceMessage) error {
	server := q.query.Tool
	if index := strings.IndexByte(server, '.'); index >= 0 {
		server = server[:index]
	}
	key := messageKey(server, source.ID)
	if existing, ok := q.state.Messages[key]; ok {
		returned := Line{Kind: KindReturned, Query: q.name, Run: q.id, Message: &Message{Server: server, SourceID: source.ID, Number: existing.Number}}
		known := false
		for _, query := range existing.Queries {
			known = known || query == q.name
		}
		if existing.Digest != "" && q.listed(source.Sender) {
			if digest := textDigest(source.Text); digest != existing.LatestDigest {
				// Edited since it was recorded: the change is noted, and it is not
				// read again.
				returned.Message.Digest = digest
				known = false
			}
		}
		if known {
			return nil
		}
		if err := q.runner.Records.append(returned); err != nil {
			return err
		}
		q.state.apply(returned)
		return nil
	}
	number := MessageNumber(server, source.ID)
	if other, taken := q.state.Numbers[number]; taken && other != key {
		return fmt.Errorf("two messages share the number %s", number)
	}
	message := &Message{Server: server, SourceID: source.ID, Number: number, Query: q.name, Sender: source.Sender}
	if q.listed(source.Sender) {
		message.SentAt, message.Link = source.SentAt, source.Link
		message.Text = cutText(source.Text, q.query.Limits.TextBytes)
		message.TextCut = len(message.Text) < len(source.Text)
		message.Size, message.Digest = len(source.Text), textDigest(source.Text)
	} else {
		// Nothing of what the sender wrote is kept: not the text, not a digest
		// of it, and not the link, whose words a sender may have chosen.
		message.Ending, message.EndingReason = EndingIgnored, ReasonSenderNotListed
	}
	line := Line{Kind: KindMessage, Query: q.name, Run: q.id, Message: message}
	if err := q.runner.Records.append(line); err != nil {
		return err
	}
	q.state.apply(line)
	q.result.Messages++
	return nil
}

func (q *queryRun) listed(sender string) bool {
	if q.query.Anyone {
		return true
	}
	for _, listed := range q.query.Senders {
		if sender == listed {
			return true
		}
	}
	return false
}

func (q *queryRun) fail(kind Failure, reason string) {
	q.result.Outcome, q.result.Failure = RunFailed, kind
	q.result.Reason = oneline.Fold(reason, 600)
}

// end records how the run ended, and files the one warning when this is the
// third failed run in a row.
func (q *queryRun) end() error {
	line := Line{Kind: KindRun, Query: q.name, Run: q.id, Phase: PhaseEnded, Outcome: q.result.Outcome, Failure: q.result.Failure,
		Calls: q.result.Calls, Messages: q.result.Messages, Reason: q.result.Reason}
	if q.result.Outcome == RunFailed && q.standing.FailedInARow+1 >= FailedRunsBeforeWarning && !q.standing.Warned {
		line.Warned = q.file(q.failingEntry(), "intake-failing:"+q.id)
	}
	if err := q.runner.Records.append(line); err != nil {
		q.problems = append(q.problems, err)
	}
	return errors.Join(q.problems...)
}

// failingEntry is the warning for three failed runs in a row. A call failing
// is the development manager's to look into. An answer of the wrong shape can
// be mended only by changing the query's fields in the intake section, which
// every role's write refuses, so only the operator can; the report opens with
// the reason that says so and names the line to change.
func (q *queryRun) failingEntry() report.Entry {
	since := "it was switched on"
	if !q.standing.ReadUpTo.IsZero() {
		since = localTime(q.standing.ReadUpTo)
	}
	if q.result.Failure == FailureShape {
		return report.Entry{Severity: report.SeverityWarning, Message: fmt.Sprintf(
			"protected-file: intake query %s has failed %d runs in a row because the answer from %s does not hold the messages where the configuration says: %s. "+
				"Only a change to intake.queries.%s in .yoyodyne/config.yaml can mend this, by setting items and fields to where the server now puts the messages and their parts, and every role's write to the intake section is refused. "+
				"Until then the query reads nothing and marks nothing as read, so no message is lost; it holds up no other query and no other work.",
			q.name, FailedRunsBeforeWarning, q.query.Tool, q.result.Reason, q.name)}
	}
	return report.Entry{Severity: report.SeverityWarning, For: domain.RoleDevelopmentManager, Message: fmt.Sprintf(
		"Intake query %s has failed %d runs in a row calling %s; the last failure: %s. "+
			"The time it has read up to has stayed at %s, and every later run asks again from there, so no message is lost while it fails. No other warning is filed until a run succeeds.",
		q.name, FailedRunsBeforeWarning, q.query.Tool, q.result.Reason, since)}
}

// file files one of the harness's own warnings, and says whether it was filed.
func (q *queryRun) file(entry report.Entry, runID string) bool {
	if q.runner.Notes == nil {
		return false
	}
	attribution := q.runner.Attribution
	attribution.Role, attribution.Agent, attribution.WorkItemID, attribution.RunID = report.HarnessReporter, "", "", runID
	collected, err := report.Collect([]report.Entry{entry}, attribution, q.runner.now())
	if err != nil {
		q.problems = append(q.problems, fmt.Errorf("the warning about intake query %s could not be made: %w", q.name, err))
		return false
	}
	for _, reported := range collected {
		if err := q.runner.Notes.Append(reported); err != nil {
			q.problems = append(q.problems, fmt.Errorf("the warning about intake query %s could not be filed: %w", q.name, err))
			return false
		}
	}
	return true
}

func queryNames(intake config.Intake) []string {
	names := make([]string, 0, len(intake.Queries))
	for name := range intake.Queries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func seconds(d time.Duration) Seconds { return Seconds(d / time.Second) }

func textDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// cutText is text cut to at most limit bytes on a rune boundary.
func cutText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// localTime is a time as a person reads it in a report, in the machine's own
// zone with the zone named.
func localTime(when time.Time) string {
	return when.Local().Format("2006-01-02 15:04 MST")
}
