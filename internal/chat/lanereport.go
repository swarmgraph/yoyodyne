package chat

// A program manager's lane report: one block in a reply, rewriting the
// instance's report whole.
//
// "The lane report" in `docs/designs/program-manager.md` is the whole of the
// rule. The block is typed and it is the only way a report is written, so what
// the status derivation and the digest read is always something one turn said in
// full. A block the harness cannot read is refused whole, the report before it
// stands, and the refusal is recorded where a pass's problems are: the
// conversation's log, the reply, and — through the recurring task that woke the
// turn — the pass record. It never fails the turn, because the rest of the reply
// is real and the report is a surface rather than a decision.
//
// Only a role holding `lane-report.write` may carry the block at all, and that is
// the program manager alone. Any other role's reply carrying one is refused by
// the authority table before anything is recorded, as every block a role has no
// authority for is.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

const laneReportFence = "```yoyodyne-lane-report"

// maxLaneReportBlockBytes bounds the block the harness will decode. The report
// itself is held to runstate.MaxLaneReportBytes as it is stored; this bounds the
// request, which may be laid out with whitespace the stored report does not
// carry, so a block is refused for its size here only where no layout could make
// it fit.
const maxLaneReportBlockBytes = 4 * runstate.MaxLaneReportBytes

// LaneReports is where a program manager's report is kept. It is satisfied by
// *runstate.LaneReportStore, which redacts, bounds, numbers, and keeps the
// history.
type LaneReports interface {
	Write(ctx context.Context, version runstate.LaneReport) (runstate.LaneReport, error)
}

// laneReportDocument is the block as it is decoded. Every field is a pointer so
// that a field left out is told apart from one written empty: an empty list says
// nothing remains, and a missing one is a report that did not say.
type laneReportDocument struct {
	// BlockedOnAHuman is the one opening list the instance writes. It may be left
	// out: what an earlier pass wrote is carried by the store until the
	// operator's act clears it, so leaving it out removes nothing.
	BlockedOnAHuman []laneReportHumanBlock        `json:"blocked_on_a_human"`
	Summary         *string                       `json:"summary"`
	Remaining       *[]string                     `json:"remaining"`
	Blockers        *[]runstate.LaneReportBlocker `json:"blockers"`
}

// laneReportHumanBlock is one Blocked on a human entry as the block carries it.
// Its moment is read here, so an entry whose since is not a time is refused on
// its own rather than taking the whole report with it.
type laneReportHumanBlock struct {
	Item   string `json:"item"`
	What   string `json:"what"`
	Reason string `json:"reason"`
	Cites  string `json:"cites"`
	Since  string `json:"since"`
}

// LaneReportOutcome is what became of the lane report a reply carried: the
// version it became, or why it was refused and the report before it stands.
type LaneReportOutcome struct {
	Turn     int    `json:"turn"`
	Recorded bool   `json:"recorded"`
	Version  int    `json:"version,omitempty"`
	Pass     string `json:"pass,omitempty"`
	Failure  string `json:"failure,omitempty"`
	// RefusedEntries names each Blocked on a human entry left out of a report
	// that was otherwise written, and why.
	RefusedEntries []string `json:"refused_entries,omitempty"`
}

// Refusal is what a pass record says about a report that did not land, or
// about the Blocked on a human entries left out of one that did, and empty
// where nothing was refused or the reply carried no report.
func (o *LaneReportOutcome) Refusal() string {
	if o == nil {
		return ""
	}
	if o.Recorded {
		if len(o.RefusedEntries) == 0 {
			return ""
		}
		return "the lane report this turn carried was written without " + refusedEntries(len(o.RefusedEntries)) + ": " + strings.Join(o.RefusedEntries, "; ")
	}
	return "the lane report this turn carried was refused whole, and the report before it stands: " + o.Failure
}

func refusedEntries(n int) string {
	if n == 1 {
		return "1 Blocked on a human entry"
	}
	return fmt.Sprintf("%d Blocked on a human entries", n)
}

// extractLaneReport takes the lane report block out of a reply. found says the
// reply carried one, readable or not, which is what the authority table refuses
// a role for; problem says why one that was carried cannot be written. The prose
// comes back without the block wherever the block's bounds could be found.
func extractLaneReport(reply string) (prose string, content *runstate.LaneReportContent, found bool, problem error) {
	prose, payload, found, err := splitFencedBlock(reply, laneReportFence, "lane report")
	if err != nil {
		return reply, nil, true, err
	}
	if !found {
		return reply, nil, false, nil
	}
	decoded, err := decodeLaneReport(payload)
	if err != nil {
		return prose, nil, true, err
	}
	return prose, decoded, true, nil
}

// decodeLaneReport strictly decodes the block and holds it to the report's
// shape. Unknown fields, trailing content, a missing field, and a mover outside
// the vocabulary are all refused, and the refusal names every problem at once.
func decodeLaneReport(payload string) (*runstate.LaneReportContent, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode lane report: the block is empty")
	}
	if len(trimmed) > maxLaneReportBlockBytes {
		return nil, fmt.Errorf("decode lane report: block is %d bytes, and the report is held to %d", len(trimmed), runstate.MaxLaneReportBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var document laneReportDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode lane report: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode lane report: unexpected trailing content after the report")
	}
	var missing []string
	content := runstate.LaneReportContent{}
	if document.Summary == nil {
		missing = append(missing, "summary")
	} else {
		content.Summary = *document.Summary
	}
	if document.Remaining == nil {
		missing = append(missing, "remaining")
	} else {
		content.Remaining = append([]string{}, *document.Remaining...)
	}
	if document.Blockers == nil {
		missing = append(missing, "blockers")
	} else {
		content.Blockers = append([]runstate.LaneReportBlocker{}, *document.Blockers...)
	}
	if document.BlockedOnAHuman != nil {
		content.BlockedOnAHuman = []runstate.LaneReportHumanBlock{}
	}
	for index, entry := range document.BlockedOnAHuman {
		since, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Since))
		if err != nil {
			content.Refused = append(content.Refused, fmt.Sprintf("blocked_on_a_human[%d] was refused: since %q is not a time written as 2026-10-12T16:40:00Z", index, entry.Since))
			continue
		}
		content.BlockedOnAHuman = append(content.BlockedOnAHuman, runstate.LaneReportHumanBlock{
			Item: entry.Item, What: entry.What, Reason: ownership.OperatorReason(entry.Reason), Cites: entry.Cites, Since: since,
		})
	}
	// An entry that breaks the contract is refused on its own and the rest of
	// the report stands, which is the design's rule for this list alone.
	content = content.KeepValidHumanBlocks()
	if len(missing) > 0 {
		return nil, fmt.Errorf("decode lane report: a report carries summary, remaining, and blockers, and this one has no %s", strings.Join(missing, " or "))
	}
	// The shape is the record's and the movers are the read model's, so both are
	// asked and every problem either finds is named at once.
	problems := []error{content.Validate()}
	for index, blocker := range content.Blockers {
		if err := readmodel.CheckLaneReportMover(blocker.WaitingOn); err != nil {
			problems = append(problems, fmt.Errorf("blockers[%d]: %w", index, err))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return &content, nil
}

// keepsLaneReport reports whether this conversation's role writes a lane report.
func (s *Session) keepsLaneReport() bool {
	return s.authority().LaneReport
}

// ForPass says the turns this session takes from here are a pass's, named as the
// recurring task that fired it names it, so a lane report written by one is
// stamped with the pass as well as the turn. A session nothing marks is an
// operator's conversation, and its reports are stamped with the turn alone.
//
// The next message is the recurring turn's own, and it starts a provider
// session of its own rather than resuming the conversation's; the messages
// after it, until ForPass is called again, continue that session. EndPass puts
// the conversation's session back. See passcontext.go.
func (s *Session) ForPass(pass string) {
	s.pass = strings.TrimSpace(pass)
	s.handedStops = nil
	s.passFresh = s.pass != ""
}

// writeLaneReport carries out one reply's lane report, or records why it could
// not be, and says which in the conversation's log. The log names the version
// and never the report's text, which is the store's alone.
func (s *Session) writeLaneReportWithoutToolAudit(ctx context.Context, content *runstate.LaneReportContent, problem error) (LaneReportOutcome, error) {
	outcome := LaneReportOutcome{Turn: s.state.Turns, Pass: s.pass}
	switch {
	case problem != nil:
		outcome.Failure = singleLine(problem.Error(), maxTrackerFailureBytes)
	case s.options.LaneReports == nil:
		outcome.Failure = "no lane report store is wired to this conversation, so nothing was written"
	default:
		written, err := s.options.LaneReports.Write(ctx, runstate.LaneReport{
			ProductID: s.options.ProductID,
			Agent:     s.options.Agent,
			Report:    *content,
			Stamp: runstate.LaneReportStamp{
				Pass:           s.pass,
				ConversationID: s.state.ConversationID,
				Turn:           s.state.Turns,
			},
			RecordedAt: s.options.clock().Now(),
		})
		if err != nil {
			outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		} else {
			outcome.Recorded = true
			outcome.Version = written.Version
			// The store keeps the entries this reply's block already refused and
			// adds the ones the waiting records refuse, so its answer names them
			// all; a store that hands back none of them still leaves the block's.
			refused := written.Report.Refused
			if len(refused) == 0 {
				refused = content.Refused
			}
			for _, entry := range refused {
				outcome.RefusedEntries = append(outcome.RefusedEntries, singleLine(entry, maxTrackerFailureBytes))
			}
		}
	}
	eventType := execution.EventLaneReportRefused
	if outcome.Recorded {
		eventType = execution.EventLaneReportRecorded
	}
	if err := s.emit(eventType, map[string]any{
		"turn":    outcome.Turn,
		"pass":    outcome.Pass,
		"version": outcome.Version,
		"failure": outcome.Failure,
		"refused": outcome.RefusedEntries,
	}); err != nil {
		return outcome, fmt.Errorf("record what became of the lane report: %w", err)
	}
	return outcome, nil
}

// renderLaneReportResult is what the role is told became of its report. One
// that was refused is not visible anywhere the role reads, and a role never told
// would go on believing the lane said what it wrote.
func renderLaneReportResult(outcome LaneReportOutcome) string {
	if outcome.Recorded {
		written := fmt.Sprintf("# Lane report result\n\nYour lane report was written as version %d.\n\n", outcome.Version)
		if len(outcome.RefusedEntries) > 0 {
			written += "These Blocked on a human entries were left out of it, and nothing else was: " + strings.Join(outcome.RefusedEntries, "; ") + "\n\n"
		}
		return written
	}
	return "# Lane report result\n\nYour lane report was refused whole and nothing was written, so the report before it still stands: " + outcome.Failure + "\n\n"
}

// reportLaneReport tells the operator what became of the report, without its
// text, which is in the store where it is read.
func (s *Session) reportLaneReport(out io.Writer, reply Reply) {
	if reply.LaneReport == nil {
		return
	}
	title := RoleTitle(s.state.Role)
	if reply.LaneReport.Recorded {
		fmt.Fprintf(out, "the %s rewrote its lane report (version %d)\n\n", title, reply.LaneReport.Version)
		if len(reply.LaneReport.RefusedEntries) > 0 {
			fmt.Fprintf(out, "%s\n\n", reply.LaneReport.Refusal())
		}
		return
	}
	fmt.Fprintf(out, "the %s's lane report was refused, and the previous one stands: %s\n\n", title, reply.LaneReport.Failure)
}

// laneReportContract is what the program manager is told about its report. It
// is part of the contract rather than the persona because what the role may
// write is authority. The movers it names are read from the read model rather
// than written out here, so the contract cannot offer one the check refuses.
var laneReportContract = `# Your lane report

You keep one report on your lane. It opens with three lists, ahead of its summary, and every surface shows them first, one line per entry:

- **Completed**: what you finished in the last 24 hours. The harness derives it from your recorded acts and the tracker: items you admitted or in your lane that closed, and your own requests that were decided. You do not write it.
- **Handed off**: what you passed to another agent in the last 24 hours: an admission a developer will be given, a report to the Lead Product Manager, an ask to another role, an amendment proposed to a document's owner. The harness derives it too, naming the agent each went to and never a person.
- **Blocked on a human**: only what needs the operator himself. You write it, as "blocked_on_a_human".

Then an executive summary of the lane's progress, what remains, and what is blocking it. The report is pulled rather than pushed — the operator and the other roles read it when they choose — and it is yours alone to write. Rewrite it whole whenever where the lane stands has changed, and on every pass, by ending your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-lane-report
{"blocked_on_a_human":[{"item":"the work item it is about, where there is one","what":"what the operator must do","reason":"credential","cites":"the id of the report, amendment, or directive his act is recorded on","since":"2026-10-12T16:40:00Z"}],"summary":"where the lane stands, in a few sentences","remaining":["what is still to do"],"blockers":[{"what":"what is blocked","waiting_on":"product-manager","cites":"the id of the request, report, amendment, or exchange you raised about it"}]}
` + "```" + `

"summary", "remaining", and "blockers" are required; an empty list says nothing remains or nothing is blocking. "waiting_on" is who has to move: ` + quotedLaneReportMovers() + `. Never the operator: what waits on him goes under "blocked_on_a_human", and a blocker naming him is refused. "cites" is the identifier of a record you already raised about the blocker — a blocker you have asked nobody about is not yet a blocker, so raise it first.

An entry under "blocked_on_a_human" names what the operator must do, its "reason" — one of ` + quotedOperatorReasons() + ` — the waiting record his act is recorded on as "cites", and since when, as a UTC time. "item" may be left out for a question no work item carries. Under the operator's rule of 2026-09-26 only those reasons are his: a decision a role could make is a defect to report, never an entry here. Work you handed to a step only a person can take, such as reading a log only he can open, is listed here as well, and you file it as a defect. An entry with no reason from that list, citing no waiting record, or citing one he has already cleared is refused on its own, the rest of the report is written, and you are told which. The harness carries every entry forward from pass to pass until the act that clears it is recorded on the record it cites, so you never need to write an entry again, and leaving one out does not remove it.

The whole report is held to 16 KiB and redacted before it is written, and a block that is malformed, too large, missing a field, or naming anybody else as a mover is refused whole: nothing is written, the report before it stands, and you are told why on your next turn. Never put a secret in it.

The report is read by people who do not know your lane's items by number. ` + terms.ItemNaming + `

` + terms.PersonWriting + `

Apply the standing set to your lane report and post-mortems as to every other output and decision.

A blocker waiting on the operator for an approval is not a blocker to carry from pass to pass: it is a defect in this system, and you report it as one. ` + terms.StandingGoals + `

` + terms.DecideAndReport

// quotedOperatorReasons names the reasons a Blocked on a human entry may give,
// read from the ownership registry so the contract cannot offer one it refuses.
func quotedOperatorReasons() string {
	reasons := ownership.OperatorReasons()
	quoted := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		quoted = append(quoted, fmt.Sprintf("%q (%s)", string(reason), reason.Says()))
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
}

// quotedLaneReportMovers names the movers a blocker may wait on, as the contract
// says them.
func quotedLaneReportMovers() string {
	movers := readmodel.LaneReportMovers()
	quoted := make([]string, 0, len(movers))
	for _, mover := range movers {
		quoted = append(quoted, fmt.Sprintf("%q", string(mover)))
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
}
