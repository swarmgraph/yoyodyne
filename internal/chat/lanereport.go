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

	"github.com/mason-bryant/yoyodyne/internal/execution"
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
	Summary   *string                       `json:"summary"`
	Remaining *[]string                     `json:"remaining"`
	Blockers  *[]runstate.LaneReportBlocker `json:"blockers"`
}

// LaneReportOutcome is what became of the lane report a reply carried: the
// version it became, or why it was refused and the report before it stands.
type LaneReportOutcome struct {
	Turn     int    `json:"turn"`
	Recorded bool   `json:"recorded"`
	Version  int    `json:"version,omitempty"`
	Pass     string `json:"pass,omitempty"`
	Failure  string `json:"failure,omitempty"`
}

// Refusal is what a pass record says about a report that did not land, and
// empty where it did or where the reply carried none.
func (o *LaneReportOutcome) Refusal() string {
	if o == nil || o.Recorded {
		return ""
	}
	return "the lane report this turn carried was refused whole, and the report before it stands: " + o.Failure
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
func (s *Session) ForPass(pass string) {
	s.pass = strings.TrimSpace(pass)
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
		return fmt.Sprintf("# Lane report result\n\nYour lane report was written as version %d.\n\n", outcome.Version)
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
		return
	}
	fmt.Fprintf(out, "the %s's lane report was refused, and the previous one stands: %s\n\n", title, reply.LaneReport.Failure)
}

// laneReportContract is what the program manager is told about its report. It
// is part of the contract rather than the persona because what the role may
// write is authority. The movers it names are read from the read model rather
// than written out here, so the contract cannot offer one the check refuses.
var laneReportContract = `# Your lane report

You keep one report on your lane: an executive summary of its progress, what remains, and what is blocking it. It is pulled rather than pushed — the operator and the other roles read it when they choose — and it is yours alone to write. Rewrite it whole whenever where the lane stands has changed, and on every pass, by ending your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-lane-report
{"summary":"where the lane stands, in a few sentences","remaining":["what is still to do"],"blockers":[{"what":"what is blocked","waiting_on":"product-manager","cites":"the id of the request, report, amendment, or exchange you raised about it"}]}
` + "```" + `

All three fields are required; an empty list says nothing remains or nothing is blocking. "waiting_on" is who has to move: ` + quotedLaneReportMovers() + `. "cites" is the identifier of a record you already raised about the blocker — a blocker you have asked nobody about is not yet a blocker, so raise it first. The whole report is held to 16 KiB and redacted before it is written, and a block that is malformed, too large, missing a field, or naming anybody else as a mover is refused whole: nothing is written, the report before it stands, and you are told why on your next turn. Never put a secret in it.

The report is read by people who do not know your lane's items by number. ` + terms.ItemNaming + `

A blocker waiting on the operator for an approval is not a blocker to carry from pass to pass: it is a defect in this system, and you report it as one. ` + terms.DecideAndReport

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
