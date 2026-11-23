package chat

// The development manager's record of a defect traced to a landed run's change.
//
// The trial of starting developers on the cheaper model
// (docs/designs/reducing-what-the-factory-costs.md, section 4) stops at once on
// a defect after landing traced to a run that started cheaper. She decides on
// the trial, so recording that defect is hers: a block naming the run and what
// the defect is writes one narrow update to the run's record
// (runstate.RecordDefectAfterLanding), refused exactly as the command
// `yoyo model-trial defect` is refused — a run outside the trial, a run whose
// change never landed, a note that says nothing. It changes no work and
// invokes nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const landedDefectFence = "```yoyodyne-landed-defect"

// maxLandedDefectBlockBytes bounds the block itself: a run id and a note.
const maxLandedDefectBlockBytes = runstate.MaxLandedDefectNoteBytes + 1<<10

// LandedDefects is where a defect after landing is recorded on a run. It is
// satisfied by landedDefectRecorder over *runstate.Store in internal/cli.
type LandedDefects interface {
	RecordDefectAfterLanding(ctx context.Context, runID, note string, at time.Time) (runstate.State, error)
}

// LandedDefectAsk is the run a reply named and what the defect is.
type LandedDefectAsk struct {
	Run  string `json:"run"`
	Note string `json:"note"`
}

// LandedDefectOutcome is what became of one ask.
type LandedDefectOutcome struct {
	Ask      LandedDefectAsk `json:"ask"`
	Recorded bool            `json:"recorded"`
	// StoppedTrial says the run started on the cheaper model, so the trial
	// stopped with this record.
	StoppedTrial bool   `json:"stopped_trial,omitempty"`
	Failure      string `json:"failure,omitempty"`
}

// LandedDefectError reports a block the harness could not read. Nothing in it
// was recorded.
type LandedDefectError struct {
	Err error
}

func (e *LandedDefectError) Error() string {
	return "the reply carried a block recording a defect after landing that the harness cannot read: " + e.Err.Error()
}

func (e *LandedDefectError) Unwrap() error { return e.Err }

// extractLandedDefect takes the block recording a defect after landing out of a reply, leaving its
// prose.
func extractLandedDefect(reply string) (string, *LandedDefectAsk, error) {
	prose, payload, found, err := splitFencedBlock(reply, landedDefectFence, "landed defect")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	ask, err := decodeLandedDefect(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, &ask, nil
}

func decodeLandedDefect(payload string) (LandedDefectAsk, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return LandedDefectAsk{}, errors.New("decode landed defect: the block is empty")
	}
	if len(trimmed) > maxLandedDefectBlockBytes {
		return LandedDefectAsk{}, fmt.Errorf("decode landed defect: block is %d bytes, limit is %d", len(trimmed), maxLandedDefectBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var ask LandedDefectAsk
	if err := decoder.Decode(&ask); err != nil {
		return LandedDefectAsk{}, fmt.Errorf("decode landed defect: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return LandedDefectAsk{}, errors.New("decode landed defect: unexpected trailing content after the request")
	}
	return ask, nil
}

// performLandedDefect records the one defect a reply named, audited as the
// tool it is. A refusal is an outcome the role is told, never a failed turn.
func (s *Session) performLandedDefect(ctx context.Context, ask LandedDefectAsk) LandedDefectOutcome {
	outcome, err := auditTool(ctx, s, capability.RunRecordLandedDefect, map[string]any{"run": ask.Run}, 1, func() (LandedDefectOutcome, error) {
		return s.performLandedDefectWithoutToolAudit(ctx, ask), nil
	}, func(outcome LandedDefectOutcome) (int, bool, error) {
		if outcome.Failure != "" {
			return 0, false, errors.New(outcome.Failure)
		}
		return 0, false, nil
	})
	if err != nil && outcome.Failure == "" {
		outcome = LandedDefectOutcome{Ask: ask, Failure: singleLine(err.Error(), maxTrackerFailureBytes)}
	}
	return outcome
}

func (s *Session) performLandedDefectWithoutToolAudit(ctx context.Context, ask LandedDefectAsk) LandedDefectOutcome {
	outcome := LandedDefectOutcome{Ask: ask}
	run := strings.TrimSpace(ask.Run)
	if run == "" {
		outcome.Failure = "the block names no run"
		return outcome
	}
	if s.options.LandedDefects == nil {
		outcome.Failure = "nothing is wired to record a defect on a run in this conversation, so nothing was recorded"
		return outcome
	}
	state, err := s.options.LandedDefects.RecordDefectAfterLanding(ctx, run, ask.Note, s.options.clock().Now())
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	outcome.Recorded = true
	outcome.StoppedTrial = state.CheaperFirst != nil && state.CheaperFirst.Arm == runstate.CheaperFirstCheaper
	return outcome
}

// renderLandedDefectResult is what the role is told became of its record.
func renderLandedDefectResult(outcome LandedDefectOutcome) string {
	var rendered strings.Builder
	rendered.WriteString("# Defect after landing\n\n")
	switch {
	case !outcome.Recorded:
		fmt.Fprintf(&rendered, "Not recorded: %s\n\n", outcome.Failure)
	case outcome.StoppedTrial:
		fmt.Fprintf(&rendered, "Recorded on run %s. It started on the cheaper model, so the trial of starting developers on the cheaper model is stopped: every eligible run from now on starts on the developer's configured model while the setting stays on.\n\n", strings.TrimSpace(outcome.Ask.Run))
	default:
		fmt.Fprintf(&rendered, "Recorded on run %s. It started on the stronger model, so the trial goes on, and the defect is counted against the stronger half.\n\n", strings.TrimSpace(outcome.Ask.Run))
	}
	return rendered.String()
}

// reportLandedDefect tells the operator what the role recorded.
func (s *Session) reportLandedDefect(out io.Writer, reply Reply) {
	if reply.LandedDefect == nil {
		return
	}
	title := RoleTitle(s.state.Role)
	outcome := *reply.LandedDefect
	run := strings.TrimSpace(outcome.Ask.Run)
	switch {
	case !outcome.Recorded:
		fmt.Fprintf(out, "the %s's record of a defect after landing on run %q was not kept: %s\n\n", title, run, outcome.Failure)
	case outcome.StoppedTrial:
		fmt.Fprintf(out, "the %s recorded a defect after landing on run %s, which started on the cheaper model, so the trial of starting developers on the cheaper model is stopped\n\n", title, run)
	default:
		fmt.Fprintf(out, "the %s recorded a defect after landing on run %s, which started on the stronger model; the trial goes on\n\n", title, run)
	}
}

// landedDefectContract is what a role holding run.record-landed-defect is told.
const landedDefectContract = `# Recording a defect found after a change landed

Where a defect is traced to the change a run landed, and that run took part in the trial of starting developers on the cheaper model, record it by ending your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-landed-defect
{"run":"run-…","note":"what the defect is and where it was found"}
` + "```" + `

The harness writes it on the run's record. A run that started on the cheaper model stops the trial at once: every eligible run after it starts on the developer's configured model. A run that started on the stronger model is counted against that half and the trial goes on. It is refused for a run that took no part in the trial, for one whose change never landed, and for a note that says nothing, and you are told why. The trial's figures open each of your passes while the trial has anything to say; keeping execution.developer_cheaper_first on, or turning it off, is your decision on them.`
