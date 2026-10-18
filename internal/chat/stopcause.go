package chat

// A program manager's post-mortems of stopped runs: one block in a reply,
// counting the stops its pass was handed against their causes and filing the
// one report each cause is owed.
//
// "Repeated stop causes" in docs/designs/program-manager.md is the rule, and
// runstate/stopcause.go keeps the record. What this adds is the instance's
// side of it, and the harness's checks on each part:
//
//   - A stop is named by its run and the moment it stopped, and is counted only
//     where this pass was handed exactly that stop. Its cause is the one the
//     harness read off the run record when it handed the stop over; the block
//     cannot name one.
//   - A report about a cause is written here as what was at fault, the remedy,
//     and what happened, and the harness files it at warning severity with the
//     cause's count and runs, then attaches it to the cause. It is refused
//     where the cause already has a report standing: the digest carries the
//     count against that report instead. Once the Lead Product Manager has
//     handled it, a new report is accepted at the set count of stops since, or
//     at once on a stop of priority 0 work, and names the handled one.
//   - A note corrects a report already filed, and is appended to the record
//     against it. Nothing here changes a report once filed.
//
// Like the lane report, a refusal is an outcome the role is told and never a
// failed turn.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

const stopCauseFence = "```yoyodyne-stop-causes"

// The bounds one block is held to. A pass hands at most a few hundred events,
// and the reports are bounded by how many reports a reply may file.
const (
	maxStopCauseBlockBytes  = 64 << 10
	maxStopCauseStops       = 200
	maxStopCauseReports     = 5
	maxStopCauseNotes       = 5
	maxStopCauseDigestLines = 20
)

// StopCauses is where a program manager's account of stopped runs is kept. It
// is satisfied by *runstate.StopCauseStore.
type StopCauses interface {
	Record(ctx context.Context, agent string, at time.Time, decide func(runstate.StopCauseAccount) ([]runstate.StopCauseEntry, error)) (runstate.StopCauseAccount, error)
}

// StopCauseAsk is the block as the role wrote it.
type StopCauseAsk struct {
	Stops   []StopCauseStopAsk   `json:"stops,omitempty"`
	Reports []StopCauseReportAsk `json:"reports,omitempty"`
	Notes   []StopCauseNoteAsk   `json:"notes,omitempty"`
}

// StopCauseStopAsk names one stop the pass was handed.
type StopCauseStopAsk struct {
	Run       string `json:"run"`
	StoppedAt string `json:"stopped_at"`
}

// StopCauseReportAsk is the post-mortem of one cause.
type StopCauseReportAsk struct {
	Cause   string `json:"cause"`
	Fault   string `json:"fault"`
	Remedy  string `json:"remedy"`
	Account string `json:"account"`
}

// StopCauseNoteAsk corrects a report already filed.
type StopCauseNoteAsk struct {
	Report string `json:"report"`
	Note   string `json:"note"`
}

// StopCauseFiled is one report the block's post-mortems filed.
type StopCauseFiled struct {
	Cause   string `json:"cause"`
	Report  string `json:"report"`
	Follows string `json:"follows,omitempty"`
}

// StopCauseOutcome is what became of one block.
type StopCauseOutcome struct {
	Counted int              `json:"counted"`
	Already int              `json:"already,omitempty"`
	Filed   []StopCauseFiled `json:"filed,omitempty"`
	Noted   int              `json:"noted,omitempty"`
	// Refused names each part of the block that was not carried out, and why.
	Refused []string `json:"refused,omitempty"`
	// Unnamed is the stops this pass was handed that the block did not count.
	Unnamed []string `json:"unnamed,omitempty"`
	// Digest is the line the pass's digest carries for each cause.
	Digest  []string `json:"digest,omitempty"`
	Failure string   `json:"failure,omitempty"`
	// reports are the reports filed, carried onto the reply as any report is.
	reports []report.Report
}

// StopCauseError reports a block the harness could not read. Nothing in it was
// recorded.
type StopCauseError struct {
	Err error
}

func (e *StopCauseError) Error() string {
	return "the reply carried a stop causes block the harness cannot read: " + e.Err.Error()
}

func (e *StopCauseError) Unwrap() error { return e.Err }

// extractStopCauses takes the block out of a reply, leaving its prose.
func extractStopCauses(reply string) (string, *StopCauseAsk, error) {
	prose, payload, found, err := splitFencedBlock(reply, stopCauseFence, "stop causes")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	ask, err := decodeStopCauses(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, &ask, nil
}

// decodeStopCauses strictly decodes the block. Whether a stop was handed and
// whether a cause is owed a report are decided against the record, and a
// refusal there is an outcome rather than an unreadable block.
func decodeStopCauses(payload string) (StopCauseAsk, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return StopCauseAsk{}, errors.New("decode stop causes: the block is empty")
	}
	if len(trimmed) > maxStopCauseBlockBytes {
		return StopCauseAsk{}, fmt.Errorf("decode stop causes: block is %d bytes, limit is %d", len(trimmed), maxStopCauseBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var ask StopCauseAsk
	if err := decoder.Decode(&ask); err != nil {
		return StopCauseAsk{}, fmt.Errorf("decode stop causes: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return StopCauseAsk{}, errors.New("decode stop causes: unexpected trailing content after the block")
	}
	var problems []error
	if len(ask.Stops)+len(ask.Reports)+len(ask.Notes) == 0 {
		problems = append(problems, errors.New("the block names no stop, report, or note"))
	}
	if len(ask.Stops) > maxStopCauseStops {
		problems = append(problems, fmt.Errorf("%d stops, limit is %d", len(ask.Stops), maxStopCauseStops))
	}
	if len(ask.Reports) > maxStopCauseReports {
		problems = append(problems, fmt.Errorf("%d reports, limit is %d", len(ask.Reports), maxStopCauseReports))
	}
	if len(ask.Notes) > maxStopCauseNotes {
		problems = append(problems, fmt.Errorf("%d notes, limit is %d", len(ask.Notes), maxStopCauseNotes))
	}
	if err := errors.Join(problems...); err != nil {
		return StopCauseAsk{}, fmt.Errorf("decode stop causes: %w", err)
	}
	return ask, nil
}

// HandStops says which stopped runs the pass this session's next turns belong
// to was handed, each with the cause the harness read off its run record. They
// are the only stops a stop causes block may count. ForPass clears them.
func (s *Session) HandStops(stops []runstate.StopEvent) {
	s.handedStops = append([]runstate.StopEvent(nil), stops...)
}

// performStopCausesWithoutToolAudit carries out one block: the stops first, so
// a report filed in the same block carries the count including them.
func (s *Session) performStopCausesWithoutToolAudit(ctx context.Context, ask StopCauseAsk) StopCauseOutcome {
	var outcome StopCauseOutcome
	if s.options.StopCauses == nil {
		outcome.Failure = "no stop cause record is wired to this conversation, so nothing was counted or filed"
		return outcome
	}
	now := s.options.clock().Now()
	stamp := runstate.StopCauseStamp{Pass: s.pass, ConversationID: s.state.ConversationID, Turn: s.state.Turns}
	handed := make(map[string]runstate.StopEvent, len(s.handedStops))
	for _, stop := range s.handedStops {
		handed[stop.Key()] = stop
	}
	named := map[string]bool{}
	var stops []runstate.StopEvent
	for index, entry := range ask.Stops {
		at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.StoppedAt))
		if err != nil {
			outcome.Refused = append(outcome.Refused, fmt.Sprintf("stops[%d]: stopped_at %q is not a time written as 2026-10-17T15:04:05Z", index, entry.StoppedAt))
			continue
		}
		stop, found := handed[runstate.StopKey(entry.Run, at)]
		if !found {
			outcome.Refused = append(outcome.Refused, fmt.Sprintf("stops[%d]: this pass was not handed a stop of %s at %s, so it was not counted", index, strings.TrimSpace(entry.Run), at.UTC().Format(time.RFC3339)))
			continue
		}
		named[stop.Key()] = true
		stops = append(stops, stop)
	}
	account, err := s.options.StopCauses.Record(ctx, s.options.Agent, now, func(account runstate.StopCauseAccount) ([]runstate.StopCauseEntry, error) {
		entries := account.StopEntries(stops)
		for index := range entries {
			entries[index].Stamp = stamp
		}
		outcome.Counted = len(entries)
		outcome.Already = len(stops) - len(entries)
		return entries, nil
	})
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	for _, stop := range s.handedStops {
		if !named[stop.Key()] {
			outcome.Unnamed = append(outcome.Unnamed, fmt.Sprintf(`{"run":%q,"stopped_at":%q}`, stop.Run, stop.StoppedAt.UTC().Format(time.RFC3339Nano)))
		}
	}
	if len(ask.Reports) > 0 || len(ask.Notes) > 0 {
		handled, problem := s.reportHandlings()
		if problem != "" {
			outcome.Refused = append(outcome.Refused, problem)
		}
		account, err = s.options.StopCauses.Record(ctx, s.options.Agent, now, func(account runstate.StopCauseAccount) ([]runstate.StopCauseEntry, error) {
			return s.stopCauseReports(account, ask, handled, stamp, &outcome), nil
		})
		if err != nil {
			outcome.Refused = append(outcome.Refused, "the record could not take everything the block asked for, and what it took is counted below: "+singleLine(err.Error(), maxTrackerFailureBytes))
		}
	}
	handled, _ := s.reportHandlings()
	outcome.Digest = account.Digest(handled, s.options.StopCauseRecurrence, maxStopCauseDigestLines)
	return outcome
}

// reportHandlings is when each report was handled, read from the pile. A pile
// whose handlings cannot be read reads every report as not yet handled, which
// never files a second report early, and says so.
func (s *Session) reportHandlings() (func(string) (time.Time, bool), string) {
	if s.options.Reports == nil {
		return nil, ""
	}
	handlings, err := s.options.Reports.Handlings()
	if err != nil {
		return nil, "what became of the reports could not be read, so every cause's report is taken as not yet handled: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	handled := report.Handled(handlings)
	return func(id string) (time.Time, bool) {
		handling, found := handled[id]
		return handling.RecordedAt, found
	}, ""
}

// stopCauseReports files the reports the block asked for that their causes are
// owed, and returns the entries attaching each and the notes. It runs under the
// record's lock, so two turns cannot each file a cause's first report.
func (s *Session) stopCauseReports(account runstate.StopCauseAccount, ask StopCauseAsk, handled func(string) (time.Time, bool), stamp runstate.StopCauseStamp, outcome *StopCauseOutcome) []runstate.StopCauseEntry {
	var entries []runstate.StopCauseEntry
	filedFor := map[string]bool{}
	for index, request := range ask.Reports {
		id := strings.TrimSpace(request.Cause)
		refuse := func(why string) {
			outcome.Refused = append(outcome.Refused, fmt.Sprintf("reports[%d] (%s): %s", index, id, why))
		}
		tally, found := account.Cause(id)
		if !found || tally.Count() == 0 {
			refuse("no stop is counted against that cause, so there is nothing to report on; count its stops first")
			continue
		}
		if filedFor[id] {
			refuse("this block already filed that cause's report")
			continue
		}
		fault := runstate.StopFault(strings.TrimSpace(request.Fault))
		remedy, reading := strings.TrimSpace(request.Remedy), strings.TrimSpace(request.Account)
		var problems []string
		if !fault.Valid() {
			problems = append(problems, fmt.Sprintf("fault %q must be %q, %q, or %q", request.Fault, runstate.StopFaultBound, runstate.StopFaultWork, runstate.StopFaultEnvironment))
		}
		if remedy == "" || reading == "" {
			problems = append(problems, "a report names the remedy and says what happened")
		}
		if len(remedy) > runstate.MaxStopCauseTextBytes || len(reading) > runstate.MaxStopCauseTextBytes {
			problems = append(problems, fmt.Sprintf("the remedy and the account are each held to %d bytes", runstate.MaxStopCauseTextBytes))
		}
		if len(problems) > 0 {
			refuse(strings.Join(problems, "; "))
			continue
		}
		standing := tally.Standing(handled, s.options.StopCauseRecurrence)
		current, reported := tally.Current()
		if !standing.Due.NewReport() {
			refuse(fmt.Sprintf("its report %s stands, so no second report is filed; carry its count of %d in your digest, naming %s", current.Report, tally.Count(), current.Report))
			continue
		}
		message := stopCauseReportMessage(tally, standing, current, fault, remedy, reading)
		filed, problem := s.recordReports([]report.Entry{{Severity: report.SeverityWarning, Message: message}})
		if len(filed) == 0 {
			if problem == "" {
				problem = "the report was not filed"
			}
			refuse(problem)
			continue
		}
		filedFor[id] = true
		outcome.reports = append(outcome.reports, filed...)
		entry := runstate.StopCauseEntry{Kind: runstate.StopCauseReport, Cause: tally.Cause, Report: filed[0].ID, Fault: fault, Remedy: remedy, Reading: reading, Stamp: stamp}
		if reported {
			entry.Follows = current.Report
		}
		outcome.Filed = append(outcome.Filed, StopCauseFiled{Cause: id, Report: filed[0].ID, Follows: entry.Follows})
		entries = append(entries, entry)
	}
	for index, request := range ask.Notes {
		id, note := strings.TrimSpace(request.Report), strings.TrimSpace(request.Note)
		tally, found := account.CauseOfReport(id)
		switch {
		case !found:
			outcome.Refused = append(outcome.Refused, fmt.Sprintf("notes[%d]: %s is not a report attached to any cause, so there is nothing to correct", index, id))
		case note == "" || len(note) > runstate.MaxStopCauseTextBytes:
			outcome.Refused = append(outcome.Refused, fmt.Sprintf("notes[%d]: a note says its correction in at most %d bytes", index, runstate.MaxStopCauseTextBytes))
		default:
			entries = append(entries, runstate.StopCauseEntry{Kind: runstate.StopCauseNote, Cause: tally.Cause, Report: id, Reading: note, Stamp: stamp})
			outcome.Noted++
		}
	}
	return entries
}

// stopCauseReportMessage is the report filed about a cause: what stopped the
// runs and where, how many and which, what was at fault, what happened, and
// the remedy, in that order. A later report says which handled report it
// follows and how many runs the cause stopped since. A person reads it, so it
// names the cause in words only; the cause's identifier is in what the program
// manager is told (StopCauseTally.Line, renderStopCauseResult), which is where
// a stop is counted against the report that names it.
func stopCauseReportMessage(tally runstate.StopCauseTally, standing runstate.StopCauseStanding, current runstate.StopCauseReportRef, fault runstate.StopFault, remedy, reading string) string {
	var message strings.Builder
	fmt.Fprintf(&message, "Post-mortem: %d run(s) stopped because %s. Runs: %s.", tally.Count(), tally.Cause.Describe(), tally.DescribeRuns())
	if standing.Due == runstate.StopCauseDueRecurred || standing.Due == runstate.StopCauseDueTopPriority {
		fmt.Fprintf(&message, " This cause was reported before as %s, which the Lead Product Manager handled at %s; it has stopped %d run(s) since", current.Report, runstate.LocalStamp(standing.Handled), standing.SinceHandled)
		if standing.Due == runstate.StopCauseDueTopPriority {
			message.WriteString(", including priority 0 work")
		}
		message.WriteString(".")
	}
	describedFault := fault.Describe()
	fmt.Fprintf(&message, " %s%s. What happened: %s Remedy: %s", strings.ToUpper(describedFault[:1]), describedFault[1:], terminate(reading), terminate(remedy))
	return oneline.Bound(message.String(), report.MaxMessageBytes)
}

func terminate(text string) string {
	text = strings.TrimSpace(text)
	if text != "" && !strings.ContainsAny(text[len(text)-1:], ".!?") {
		text += "."
	}
	return text
}

// renderStopCauseResult is what the role is told became of its block.
func renderStopCauseResult(outcome StopCauseOutcome) string {
	var rendered strings.Builder
	rendered.WriteString("# Stop causes result\n\n")
	if outcome.Failure != "" {
		fmt.Fprintf(&rendered, "Nothing was counted or filed: %s\n\n", outcome.Failure)
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "%d stop(s) newly counted", outcome.Counted)
	if outcome.Already > 0 {
		fmt.Fprintf(&rendered, ", %d already counted on an earlier pass and not counted again", outcome.Already)
	}
	rendered.WriteString(".\n")
	for _, filed := range outcome.Filed {
		if filed.Follows != "" {
			fmt.Fprintf(&rendered, "- Filed %s about %s at warning, naming the handled report %s.\n", filed.Report, filed.Cause, filed.Follows)
			continue
		}
		fmt.Fprintf(&rendered, "- Filed %s about %s at warning; it is that cause's report from now on.\n", filed.Report, filed.Cause)
	}
	if outcome.Noted > 0 {
		fmt.Fprintf(&rendered, "- %d correction(s) noted against their reports; the reports themselves are unchanged.\n", outcome.Noted)
	}
	for _, refused := range outcome.Refused {
		fmt.Fprintf(&rendered, "- Not done: %s\n", refused)
	}
	if len(outcome.Unnamed) > 0 {
		fmt.Fprintf(&rendered, "- This pass was handed %d stop(s) the block did not count: %s. Count them in another block.\n", len(outcome.Unnamed), strings.Join(outcome.Unnamed, ", "))
	}
	if len(outcome.Digest) > 0 {
		rendered.WriteString("\nFor your digest, one line per cause:\n")
		for _, line := range outcome.Digest {
			fmt.Fprintf(&rendered, "- %s\n", line)
		}
	}
	rendered.WriteString("\n")
	return rendered.String()
}

// reportStopCauses tells the operator what the post-mortems counted and filed.
func (s *Session) reportStopCauses(out io.Writer, reply Reply) {
	if reply.StopCauses == nil {
		return
	}
	title := RoleTitle(s.state.Role)
	outcome := *reply.StopCauses
	if outcome.Failure != "" {
		fmt.Fprintf(out, "the %s's stop causes were not recorded: %s\n\n", title, outcome.Failure)
		return
	}
	fmt.Fprintf(out, "the %s counted %d stopped run(s) against their causes and filed %d post-mortem report(s)\n", title, outcome.Counted, len(outcome.Filed))
	if len(outcome.Refused) > 0 {
		fmt.Fprintf(out, "not done: %s\n", strings.Join(outcome.Refused, "; "))
	}
	fmt.Fprintln(out)
}

// stopCauseContract is what a role holding stop-cause.record is told about it.
var stopCauseContract = `# Post-mortems on stopped runs

When a pass is handed stopped runs, post-mortem every one, grouped by cause. A cause is three things the harness reads off the run record, never your choice: the stop cause the record prints, the class it was stopped in — the work, a bound on the work, or something outside the work — and the step it stopped at. Your pass's message lists each stopped run with its cause, how many stops each cause has had, the report it already has, and what it is owed.

Record them with one block, after the prose:

` + "```" + `yoyodyne-stop-causes
{"stops":[{"run":"run-…","stopped_at":"2026-10-17T15:04:05Z"}],"reports":[{"cause":"check-time-limit/bound/checking","fault":"bound|work|environment","remedy":"the remedy, in a sentence or two","account":"what happened, in plain words"}],"notes":[{"report":"report-…","note":"the correction"}]}
` + "```" + `

- "stops" counts each stop your pass was handed, named exactly as the message lists it. A stop is counted once by its run and the moment it stopped, so naming one already counted adds nothing, and one your pass was not handed is refused.
- "reports" is the post-mortem of a cause, and the harness files it for you at warning severity, with the count and the runs. Say whether the bound was at fault (it stopped work it should have let finish), the work (the bound was right to stop it), or the environment (something outside both), and name the remedy. A cause gets one report. While its report stands, a report about it is refused: carry its count in your digest, naming the report. Once the Lead Product Manager has handled it, a new report is taken when the cause has stopped ` + fmt.Sprint(runstate.DefaultStopCauseRecurrence) + ` more runs since, or at once when it has stopped priority 0 work, and it names the handled one.
- "notes" corrects a report already filed. A filed report is never changed; the correction is kept beside it.

Your digest carries one line for each cause, as the block's result gives it: the cause, its count, and the report it names. Admit the remedy in your lane when it is your lane's work; name in your digest every remedy you did not admit, for the Lead Product Manager.

The reports are read by a person. Name the stop cause the way a person would — what stopped the run, in ordinary words — and give every time in the operator's local time with the zone named. ` + terms.PersonWriting
