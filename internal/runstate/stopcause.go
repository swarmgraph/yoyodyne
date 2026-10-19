package runstate

// A program manager instance's account of why runs stopped.
//
// "Repeated stop causes" in docs/designs/program-manager.md is the rule this
// keeps. An instance post-mortems every stopped run it is handed, and has to say
// how often one cause recurs across passes, while a report is never revised
// once filed. So the count is kept here, out of the report: each stop is
// counted once against its cause, the report filed about a cause is attached to
// it once and never changed, and the digest carries the count beside the report
// it cites.
//
// A cause is three things read from the run record and never chosen by the
// instance: the stop cause the record prints, the class it was stopped in — the
// work, a bound on the work, or something outside the work — and the step it
// stopped at. A stop is one run at one stop time, so a pass handed the same
// stop again, or run again after it failed, counts nothing twice, and a run
// stopped again after a re-run is a new stop time and counts again.
//
// The record is only appended to. A stop whose run record has since come to
// name a different cause is moved by an entry naming it, and a correction to
// what a report said is a note naming the report; neither rewrites anything
// already written. It lives beside the instance's lane report under the state
// root, at products/<product>/program-managers/<agent>/stop-causes.jsonl, and
// never in the repository.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// StopCauseSchemaVersion is the shape of one entry in the record.
const StopCauseSchemaVersion = 1

// DefaultStopCauseRecurrence is how many further stops of a cause, counted
// after the Lead Product Manager handled its report, bring a new report about
// it. A stop of top-priority work brings one at once, whatever the count.
const DefaultStopCauseRecurrence = 3

const (
	stopCauseFile             = "stop-causes.jsonl"
	maxEncodedStopCauseBytes  = 16 << 10
	stopCauseLockWait         = 30 * time.Second
	MaxStopCauseTextBytes     = 1 << 10
	unrecordedStep            = Phase("unrecorded")
	maxStopCauseRunsDescribed = 10
)

// StopSide is the class a run was stopped in: by the work, by a bound on the
// work, or by something outside the work. It is read off the stop cause the run
// record prints, never judged.
type StopSide string

const (
	// StopSideWork is the change itself failing a gate: checks, review,
	// integration, or the item escalated as unmeetable.
	StopSideWork StopSide = "work"
	// StopSideBound is a limit the harness sets on the work running out: a time
	// limit, a repair or relaunch budget, a size bound.
	StopSideBound StopSide = "bound"
	// StopSideOutside is the provider, the machine, the forge, the tracker, a
	// person or a manager stopping the run, or the harness's own step failing
	// around the work. The hold on new work still counts the last of these
	// (StopKeptOutOfHold).
	StopSideOutside StopSide = "outside"
)

func (s StopSide) Valid() bool {
	return s == StopSideWork || s == StopSideBound || s == StopSideOutside
}

// Describe says the class in the words a person reads.
func (s StopSide) Describe() string {
	switch s {
	case StopSideWork:
		return "the work"
	case StopSideBound:
		return "a bound on the work"
	default:
		return "something outside the work"
	}
}

// StopSideOf is the class a recorded stop cause falls in. Every class it sorts
// is named, the causes outside the work among them, and a cause it does not
// name is about the work: an unrecognised cause is counted rather than excused,
// because sorting it outside by default is what let a harness fault that kept
// recurring escape the hold on new work (recoverable-and-terminal-failures,
// "Trip the brake"). StopRecognised says which causes are named here.
func StopSideOf(class StopClass) StopSide {
	side, _ := stopSideListed(class)
	return side
}

// StopRecognised reports whether StopSideOf names a cause rather than sorting
// it by default. A cause that is not is the one a reader is told about each
// time it is met.
func StopRecognised(class StopClass) bool {
	_, listed := stopSideListed(class)
	return listed
}

func stopSideListed(class StopClass) (StopSide, bool) {
	switch class {
	case StopChecks, StopReview, StopIntegration, StopIntegrationPolicy, StopEscalated:
		return StopSideWork, true
	case StopCheckTimeout, StopProviderBudget, StopRelaunchBudget, StopRepairBudget,
		StopIntegrationBudget, StopPromotionWait, StopContextBound, StopStateBound,
		StopEventBound, StopReviewBound, StopRecoveryWindow,
		CauseCheckStageBound.StopClass(), CauseCheckTimeLimit.StopClass(),
		CauseWorktreeCheckoutKilled.StopClass():
		return StopSideBound, true
	case StopPublish, StopCleanup, StopRecording, StopProvider, StopOutside,
		StopCancelled, StopHarness, StopUnknown, StopProviderIdle, StopUsagePause,
		StopOperator, StopManager, StopRedeploy, StopDeadClaim,
		StopDeveloperAccount, StopReviewAccount:
		return StopSideOutside, true
	}
	if EnvironmentalCause(class).Valid() {
		return StopSideOutside, true
	}
	return StopSideWork, false
}

// stopsTheHoldCounts are the causes StopSideOf puts outside the work that the
// hold on new work counts all the same: the harness's own step failing around
// the run, and a record that names no cause. A harness that keeps failing is
// what the hold exists to stop, and nothing else would stop it
// (recoverable-and-terminal-failures, "Trip the brake").
var stopsTheHoldCounts = []StopClass{StopHarness, StopRecording, StopCleanup, StopUnknown}

// StopKeptOutOfHold reports whether the hold on new work leaves a stop of this
// cause out of its count. Only the causes outside the work — the provider, the
// forge, the machine, a move to a new build, the environment — and a run
// cancelled by a person, a manager, or its context are left out. Everything
// else counts, an unrecognised cause included.
func StopKeptOutOfHold(class StopClass) bool {
	if slices.Contains(stopsTheHoldCounts, class) {
		return false
	}
	side, listed := stopSideListed(class)
	return listed && side == StopSideOutside
}

// StopCauseKey is one cause: the three facts two stops must agree on to be
// counted together.
type StopCauseKey struct {
	Cause StopClass `json:"cause"`
	Side  StopSide  `json:"class"`
	Step  Phase     `json:"step"`
}

// StopCauseKeyOf reads a stopped run's cause off its record. A record that
// names no cause is counted under unknown, and one that names no step under
// unrecorded, rather than either being guessed.
func StopCauseKeyOf(state State) StopCauseKey {
	class := state.RecordedStopClass()
	if class == "" {
		class = StopUnknown
	}
	step := state.Phase
	if strings.TrimSpace(string(step)) == "" {
		step = unrecordedStep
	}
	return StopCauseKey{Cause: class, Side: StopSideOf(class), Step: step}
}

// ID names the cause in one word an instance can write back.
func (k StopCauseKey) ID() string {
	return string(k.Cause) + "/" + string(k.Side) + "/" + string(k.Step)
}

// Describe says the cause in the words a person reads: what stopped the run,
// at which step, and which class that is.
func (k StopCauseKey) Describe() string {
	return fmt.Sprintf("%s, at the %s step, which is %s", StopSays(k.Cause), describeStep(k.Step), k.Side.Describe())
}

// stopSays is what each recorded stop cause means, in the words a person
// would use for it. The environmental causes say themselves (Title).
var stopSays = map[StopClass]string{
	StopChecks:            "the checks kept failing or could not run",
	StopReview:            "the review found problems the repairs could not resolve",
	StopIntegration:       "the change could not be merged onto the target branch",
	StopPublish:           "the merged change could not be published to the forge",
	StopCleanup:           "the run's worktree could not be removed after its change merged",
	StopRecording:         "the harness could not record that the run finished",
	StopProvider:          "the AI provider ended the run without the work being judged",
	StopOutside:           "something outside the work refused the run",
	StopCancelled:         "the run was cancelled",
	StopHarness:           "one of the harness's own steps around the work failed",
	StopUnknown:           "something the run record does not name",
	StopCheckTimeout:      "a check ran past its time limit",
	StopProviderIdle:      "the AI session produced no output for too long",
	StopProviderBudget:    "the AI session ran past the time it is given",
	StopRelaunchBudget:    "the AI session died more times than the harness restarts it",
	StopRepairBudget:      "the repair attempts ran out",
	StopIntegrationBudget: "the merge attempts ran out",
	StopPromotionWait:     "the wait for its turn to merge ran past its limit",
	StopUsagePause:        "the provider's usage limit paused the run",
	StopOperator:          "the operator stopped the run",
	StopManager:           "the development manager stopped the run",
	StopRedeploy:          "a new build was deployed while the run was going",
	StopDeadClaim:         "the process carrying the run died",
	StopDeveloperAccount:  "the developer's provider account could not be used",
	StopReviewAccount:     "the reviewer's provider account could not be used",
	StopEscalated:         "the item was raised as one that cannot be met as written",
	StopContextBound:      "what the AI session is sent grew past its size limit",
	StopStateBound:        "the run's record grew past its size limit",
	StopEventBound:        "the run's event log grew past its size limit",
	StopIntegrationPolicy: "the merge rules refused the change",
	StopRecoveryWindow:    "the time allowed for recovering the run ran out",
	StopReviewBound:       "the change was too large for the reviewer's copy",
}

// StopSays is what a recorded stop cause means, in ordinary words, and the
// cause's own name where nothing says it better.
func StopSays(class StopClass) string {
	if says, found := stopSays[class]; found {
		return says
	}
	if cause := EnvironmentalCause(class); cause.Valid() {
		return cause.Title()
	}
	return string(class)
}

func describeStep(step Phase) string {
	switch step {
	case PhaseDeveloping:
		return "development"
	case PhaseChecking:
		return "checks"
	case PhaseReviewing:
		return "review"
	case PhaseIntegrating:
		return "merge"
	case PhaseCompleting, PhaseComplete:
		return "completion"
	case PhaseCleaningUp:
		return "cleanup"
	default:
		return strings.ReplaceAll(string(step), "_", " ")
	}
}

func (k StopCauseKey) Validate() error {
	var problems []error
	if !k.Cause.Valid() {
		problems = append(problems, fmt.Errorf("cause %q is not a stop cause the run record prints", k.Cause))
	}
	if !k.Side.Valid() {
		problems = append(problems, fmt.Errorf("class %q must be %q, %q, or %q", k.Side, StopSideWork, StopSideBound, StopSideOutside))
	} else if k.Cause.Valid() && StopSideOf(k.Cause) != k.Side {
		problems = append(problems, fmt.Errorf("class %q is not the class of %s, which is %q", k.Side, k.Cause, StopSideOf(k.Cause)))
	}
	if err := domain.ValidateIdentifier("step", strings.ReplaceAll(string(k.Step), "_", "-")); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// StopEvent is one stopped run as a pass is handed it: the run, when it
// stopped, the item it was for and that item's priority where the tracker
// gave one, and its cause as read from the record.
type StopEvent struct {
	Run       string       `json:"run"`
	StoppedAt time.Time    `json:"stopped_at"`
	Item      string       `json:"item,omitempty"`
	Priority  *int         `json:"priority,omitempty"`
	Cause     StopCauseKey `json:"cause"`
}

// StopEventOf is a stopped run's stop, at the moment it ended.
func StopEventOf(state State, ended time.Time) StopEvent {
	return StopEvent{Run: state.RunID, StoppedAt: ended.UTC(), Item: state.WorkItemID, Cause: StopCauseKeyOf(state)}
}

// Key is what a stop is counted once by: its run and its stop time.
func (e StopEvent) Key() string {
	return StopKey(e.Run, e.StoppedAt)
}

// StopKey is the key of the stop of one run at one moment.
func StopKey(run string, at time.Time) string {
	return strings.TrimSpace(run) + "@" + at.UTC().Format(time.RFC3339Nano)
}

// TopPriority reports a stop of priority 0 work.
func (e StopEvent) TopPriority() bool {
	return e.Priority != nil && *e.Priority == 0
}

// StopCauseEntryKind is what one entry in the record says.
type StopCauseEntryKind string

const (
	// StopCauseStop counts one stop against its cause.
	StopCauseStop StopCauseEntryKind = "stop"
	// StopCauseMove moves a stop already counted to the cause its run record
	// now names, without rewriting the entry that counted it.
	StopCauseMove StopCauseEntryKind = "move"
	// StopCauseReport attaches a report filed about a cause, which later passes
	// cite until a newer one is attached.
	StopCauseReport StopCauseEntryKind = "report"
	// StopCauseNote appends a correction to what a report about a cause said.
	// The report itself is never changed.
	StopCauseNote StopCauseEntryKind = "note"
)

// StopFault is the instance's judgement of what was at fault for a cause: the
// bound that stopped the runs, the work they carried, or the environment they
// ran in.
type StopFault string

const (
	StopFaultBound       StopFault = "bound"
	StopFaultWork        StopFault = "work"
	StopFaultEnvironment StopFault = "environment"
)

func (f StopFault) Valid() bool {
	return f == StopFaultBound || f == StopFaultWork || f == StopFaultEnvironment
}

// Describe says what was at fault in the words a person reads.
func (f StopFault) Describe() string {
	switch f {
	case StopFaultBound:
		return "the bound was at fault: it stopped work it should have let finish"
	case StopFaultWork:
		return "the work was at fault: the bound was right to stop it"
	default:
		return "the environment was at fault: something outside the work and the bound stopped it"
	}
}

// StopCauseStamp is the turn an entry was written in.
type StopCauseStamp struct {
	Pass           string `json:"pass,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Turn           int    `json:"turn,omitempty"`
}

// StopCauseEntry is one line of the record.
type StopCauseEntry struct {
	SchemaVersion int                `json:"schema_version"`
	ProductID     domain.ProductID   `json:"product_id"`
	Agent         string             `json:"agent"`
	Kind          StopCauseEntryKind `json:"kind"`
	// Cause is the cause the entry is about: the one a stop is counted against,
	// the one a move moves it to, or the one a report or note is attached to.
	Cause StopCauseKey `json:"cause"`
	// Run and StoppedAt name the stop a stop or a move entry is about.
	Run       string     `json:"run,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	Item      string     `json:"item,omitempty"`
	Priority  *int       `json:"priority,omitempty"`
	// Report is the report a report entry attaches, or the one a note corrects.
	Report string `json:"report,omitempty"`
	// Follows is the handled report a later report about the same cause names.
	Follows string    `json:"follows,omitempty"`
	Fault   StopFault `json:"fault,omitempty"`
	Remedy  string    `json:"remedy,omitempty"`
	// Reading is the instance's account of the cause on a report, the
	// correction on a note, and why on a move.
	Reading    string         `json:"reading,omitempty"`
	Stamp      StopCauseStamp `json:"stamp"`
	RecordedAt time.Time      `json:"recorded_at"`
}

// Stop is the stop a stop or move entry is about.
func (e StopCauseEntry) Stop() StopEvent {
	stop := StopEvent{Run: e.Run, Item: e.Item, Priority: e.Priority, Cause: e.Cause}
	if e.StoppedAt != nil {
		stop.StoppedAt = e.StoppedAt.UTC()
	}
	return stop
}

// Validate reports every contract violation in the entry at once.
func (e StopCauseEntry) Validate() error {
	var problems []error
	if e.SchemaVersion != StopCauseSchemaVersion {
		problems = append(problems, fmt.Errorf("stop cause schema version %d is not supported", e.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("agent", e.Agent); err != nil {
		problems = append(problems, err)
	}
	if err := e.Cause.Validate(); err != nil {
		problems = append(problems, err)
	}
	for field, text := range map[string]string{"remedy": e.Remedy, "reading": e.Reading} {
		if len(text) > MaxStopCauseTextBytes {
			problems = append(problems, fmt.Errorf("%s is %d bytes, limit is %d", field, len(text), MaxStopCauseTextBytes))
		}
	}
	switch e.Kind {
	case StopCauseStop, StopCauseMove:
		if strings.TrimSpace(e.Run) == "" || e.StoppedAt == nil || e.StoppedAt.IsZero() {
			problems = append(problems, fmt.Errorf("a %s entry names the run and when it stopped", e.Kind))
		}
		if e.Report != "" || e.Fault != "" || e.Remedy != "" {
			problems = append(problems, fmt.Errorf("a %s entry carries no report", e.Kind))
		}
	case StopCauseReport:
		if strings.TrimSpace(e.Report) == "" {
			problems = append(problems, errors.New("a report entry names the report it attaches"))
		}
		if !e.Fault.Valid() {
			problems = append(problems, fmt.Errorf("fault %q must be %q, %q, or %q", e.Fault, StopFaultBound, StopFaultWork, StopFaultEnvironment))
		}
		if strings.TrimSpace(e.Remedy) == "" {
			problems = append(problems, errors.New("a report entry names the remedy"))
		}
	case StopCauseNote:
		if strings.TrimSpace(e.Report) == "" || strings.TrimSpace(e.Reading) == "" {
			problems = append(problems, errors.New("a note names the report it corrects and says the correction"))
		}
	default:
		problems = append(problems, fmt.Errorf("kind %q must be %q, %q, %q, or %q", e.Kind, StopCauseStop, StopCauseMove, StopCauseReport, StopCauseNote))
	}
	if e.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid stop cause entry: %w", err)
	}
	return nil
}

// StopCauseReportRef is one report attached to a cause.
type StopCauseReportRef struct {
	Report     string    `json:"report"`
	Follows    string    `json:"follows,omitempty"`
	Fault      StopFault `json:"fault"`
	Remedy     string    `json:"remedy"`
	Reading    string    `json:"reading,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	// Notes are the corrections appended against the report, oldest first.
	Notes []string `json:"notes,omitempty"`
}

// StopCauseTally is everything the record says about one cause.
type StopCauseTally struct {
	Cause StopCauseKey `json:"cause"`
	// Stops are the stops counted against the cause, oldest first, each once.
	Stops []StopEvent `json:"stops"`
	// Reports are the reports attached to the cause, oldest first. The last is
	// the one later passes cite.
	Reports []StopCauseReportRef `json:"reports,omitempty"`
}

// Count is how many stops the cause has had.
func (t StopCauseTally) Count() int { return len(t.Stops) }

// Current is the report later passes cite, and false where none is attached.
func (t StopCauseTally) Current() (StopCauseReportRef, bool) {
	if len(t.Reports) == 0 {
		return StopCauseReportRef{}, false
	}
	return t.Reports[len(t.Reports)-1], true
}

// Since is the stops after a moment.
func (t StopCauseTally) Since(at time.Time) []StopEvent {
	var since []StopEvent
	for _, stop := range t.Stops {
		if stop.StoppedAt.After(at) {
			since = append(since, stop)
		}
	}
	return since
}

// DescribeRuns names the cause's runs, the most recent last, bounded so a cause
// that stopped a hundred runs is one line.
func (t StopCauseTally) DescribeRuns() string {
	runs := make([]string, 0, len(t.Stops))
	for _, stop := range t.Stops {
		runs = append(runs, stop.Run)
	}
	return describeRunList(runs)
}

func describeRunList(runs []string) string {
	if len(runs) <= maxStopCauseRunsDescribed {
		return strings.Join(runs, ", ")
	}
	shown := runs[len(runs)-maxStopCauseRunsDescribed:]
	return fmt.Sprintf("%s, and %d earlier", strings.Join(shown, ", "), len(runs)-maxStopCauseRunsDescribed)
}

// StopCauseDue is what a cause asks of the instance on a pass.
type StopCauseDue string

const (
	// StopCauseDueOriginal is a cause with no report yet: file its one report.
	StopCauseDueOriginal StopCauseDue = "original"
	// StopCauseDueCarried is a cause whose report stands: carry its count in the
	// digest, naming the report, and file nothing new.
	StopCauseDueCarried StopCauseDue = "carried"
	// StopCauseDueRecurred is a cause whose report was handled and which has
	// stopped the set number of runs since: file a new report naming it.
	StopCauseDueRecurred StopCauseDue = "recurred"
	// StopCauseDueTopPriority is a cause whose report was handled and which has
	// since stopped a run of priority 0 work: file a new report naming it.
	StopCauseDueTopPriority StopCauseDue = "top-priority"
)

// NewReport reports a cause that is owed a report on this pass.
func (d StopCauseDue) NewReport() bool { return d != StopCauseDueCarried }

// StopCauseStanding is where one cause stands against the Lead Product
// Manager's handling of its report.
type StopCauseStanding struct {
	Due StopCauseDue
	// Handled is when the current report was handled, zero where it is not.
	Handled time.Time
	// SinceHandled is the stops counted after that handling.
	SinceHandled int
}

// Standing is what the cause asks of a pass, given when each report was
// handled and how many stops after a handling bring a new report. A threshold
// of zero or less is the default.
func (t StopCauseTally) Standing(handled func(report string) (time.Time, bool), threshold int) StopCauseStanding {
	if threshold <= 0 {
		threshold = DefaultStopCauseRecurrence
	}
	current, reported := t.Current()
	if !reported {
		return StopCauseStanding{Due: StopCauseDueOriginal}
	}
	at, done := time.Time{}, false
	if handled != nil {
		at, done = handled(current.Report)
	}
	if !done {
		return StopCauseStanding{Due: StopCauseDueCarried}
	}
	since := t.Since(at)
	standing := StopCauseStanding{Due: StopCauseDueCarried, Handled: at, SinceHandled: len(since)}
	for _, stop := range since {
		if stop.TopPriority() {
			standing.Due = StopCauseDueTopPriority
			return standing
		}
	}
	if len(since) >= threshold {
		standing.Due = StopCauseDueRecurred
	}
	return standing
}

// LocalStamp is a moment in this machine's zone with the zone named, which is
// how every time in a post-mortem is said.
func LocalStamp(at time.Time) string {
	return at.Local().Format("2006-01-02 15:04 MST")
}

// Line is the cause on one line, as a digest and a pass's message say it: its
// identifier, its count, what stopped the runs, the report it cites, and what it
// is owed.
func (t StopCauseTally) Line(standing StopCauseStanding) string {
	line := fmt.Sprintf("%s — %d stop(s), %s", t.Cause.ID(), t.Count(), t.Cause.Describe())
	current, reported := t.Current()
	switch {
	case !reported:
		return line + "; no report filed yet: file its one report"
	case standing.Due == StopCauseDueRecurred:
		return fmt.Sprintf("%s; report %s was handled at %s and %d more run(s) stopped since: file a new report naming it", line, current.Report, LocalStamp(standing.Handled), standing.SinceHandled)
	case standing.Due == StopCauseDueTopPriority:
		return fmt.Sprintf("%s; report %s was handled at %s and priority 0 work stopped since: file a new report naming it", line, current.Report, LocalStamp(standing.Handled))
	case !standing.Handled.IsZero():
		return fmt.Sprintf("%s; report %s, handled at %s, %d stop(s) since", line, current.Report, LocalStamp(standing.Handled), standing.SinceHandled)
	default:
		return fmt.Sprintf("%s; report %s, not yet handled", line, current.Report)
	}
}

// StopCauseAccount is the record folded: every cause, and which cause each
// stop is counted against.
type StopCauseAccount struct {
	Causes []StopCauseTally
	// counted is each stop's key to the cause it is counted against now.
	counted map[string]string
}

// Cause is one cause by its identifier.
func (a StopCauseAccount) Cause(id string) (StopCauseTally, bool) {
	for _, tally := range a.Causes {
		if tally.Cause.ID() == strings.TrimSpace(id) {
			return tally, true
		}
	}
	return StopCauseTally{}, false
}

// Counted is the cause a stop is counted against, and false where it is not
// counted.
func (a StopCauseAccount) Counted(stop StopEvent) (string, bool) {
	cause, counted := a.counted[stop.Key()]
	return cause, counted
}

// CauseOfReport is the cause a report is attached to.
func (a StopCauseAccount) CauseOfReport(report string) (StopCauseTally, bool) {
	report = strings.TrimSpace(report)
	for _, tally := range a.Causes {
		for _, ref := range tally.Reports {
			if ref.Report == report {
				return tally, true
			}
		}
	}
	return StopCauseTally{}, false
}

// Digest is the line a digest carries for each cause that has stopped a run,
// the most recently stopped first, at most limit of them and a line saying
// more were left out.
func (a StopCauseAccount) Digest(handled func(report string) (time.Time, bool), threshold, limit int) []string {
	tallies := make([]StopCauseTally, 0, len(a.Causes))
	for _, tally := range a.Causes {
		if tally.Count() > 0 {
			tallies = append(tallies, tally)
		}
	}
	sort.SliceStable(tallies, func(first, second int) bool {
		return tallies[first].Stops[len(tallies[first].Stops)-1].StoppedAt.After(tallies[second].Stops[len(tallies[second].Stops)-1].StoppedAt)
	})
	var lines []string
	for index, tally := range tallies {
		if limit > 0 && index == limit {
			lines = append(lines, fmt.Sprintf("and %d further cause(s) not listed here", len(tallies)-limit))
			break
		}
		lines = append(lines, tally.Line(tally.Standing(handled, threshold)))
	}
	return lines
}

// FoldStopCauses folds the record's entries, in the order they were written,
// into where each cause stands. A move takes its stop off the cause it was
// counted against and counts it against the new one, so a stop is never
// counted under two causes at once.
func FoldStopCauses(entries []StopCauseEntry) StopCauseAccount {
	type building struct {
		tally StopCauseTally
		stops map[string]StopEvent
		first int
	}
	causes := map[string]*building{}
	counted := map[string]string{}
	order := 0
	cause := func(key StopCauseKey) *building {
		id := key.ID()
		if causes[id] == nil {
			causes[id] = &building{tally: StopCauseTally{Cause: key}, stops: map[string]StopEvent{}, first: order}
			order++
		}
		return causes[id]
	}
	for _, entry := range entries {
		switch entry.Kind {
		case StopCauseStop, StopCauseMove:
			stop := entry.Stop()
			key := stop.Key()
			if earlier, found := counted[key]; found {
				if entry.Kind == StopCauseStop || earlier == entry.Cause.ID() {
					continue
				}
				delete(causes[earlier].stops, key)
			}
			cause(entry.Cause).stops[key] = stop
			counted[key] = entry.Cause.ID()
		case StopCauseReport:
			built := cause(entry.Cause)
			built.tally.Reports = append(built.tally.Reports, StopCauseReportRef{
				Report: entry.Report, Follows: entry.Follows, Fault: entry.Fault,
				Remedy: entry.Remedy, Reading: entry.Reading, RecordedAt: entry.RecordedAt,
			})
		case StopCauseNote:
			built := cause(entry.Cause)
			for index := range built.tally.Reports {
				if built.tally.Reports[index].Report == entry.Report {
					built.tally.Reports[index].Notes = append(built.tally.Reports[index].Notes, entry.Reading)
				}
			}
		}
	}
	account := StopCauseAccount{counted: counted}
	built := make([]*building, 0, len(causes))
	for _, cause := range causes {
		built = append(built, cause)
	}
	sort.Slice(built, func(first, second int) bool { return built[first].first < built[second].first })
	for _, cause := range built {
		for _, stop := range cause.stops {
			cause.tally.Stops = append(cause.tally.Stops, stop)
		}
		sort.Slice(cause.tally.Stops, func(first, second int) bool {
			if !cause.tally.Stops[first].StoppedAt.Equal(cause.tally.Stops[second].StoppedAt) {
				return cause.tally.Stops[first].StoppedAt.Before(cause.tally.Stops[second].StoppedAt)
			}
			return cause.tally.Stops[first].Run < cause.tally.Stops[second].Run
		})
		account.Causes = append(account.Causes, cause.tally)
	}
	return account
}

// StopEntries is what recording stops adds to the record: a stop entry for
// each stop not yet counted, and a move for each one counted against a cause
// its run record no longer names. A stop already counted against the cause it
// names adds nothing, which is what keeps a pass handed the same stop again
// from counting it twice.
func (a StopCauseAccount) StopEntries(stops []StopEvent) []StopCauseEntry {
	var entries []StopCauseEntry
	seen := map[string]bool{}
	for _, stop := range stops {
		key := stop.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		at := stop.StoppedAt.UTC()
		entry := StopCauseEntry{Kind: StopCauseStop, Cause: stop.Cause, Run: stop.Run, StoppedAt: &at, Item: stop.Item, Priority: stop.Priority}
		if earlier, counted := a.Counted(stop); counted {
			if earlier == stop.Cause.ID() {
				continue
			}
			entry.Kind = StopCauseMove
			entry.Reading = fmt.Sprintf("the run record now names %s rather than %s", stop.Cause.ID(), earlier)
		}
		entries = append(entries, entry)
	}
	return entries
}

// StopCauseStore keeps every instance's stop cause record for one product.
type StopCauseStore struct {
	root      string
	productID domain.ProductID
}

// NewStopCauseStore opens the records kept under a state root.
func NewStopCauseStore(root string, productID domain.ProductID) (*StopCauseStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &StopCauseStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "program-managers"),
		productID: productID,
	}, nil
}

// StopCauses is the record store for this run store's product, reached from
// here for the reason the pass cursors are.
func (s *Store) StopCauses() *StopCauseStore {
	return &StopCauseStore{
		root:      filepath.Join(filepath.Dir(s.root), "program-managers"),
		productID: s.productID,
	}
}

// Path names one instance's record, so a failure can say where it is.
func (s *StopCauseStore) Path(agent string) string {
	return filepath.Join(s.root, agent, stopCauseFile)
}

// List reads one instance's record, oldest entry first. An instance that has
// recorded nothing yet is the ordinary answer rather than a failure.
func (s *StopCauseStore) List(agent string) ([]StopCauseEntry, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return nil, err
	}
	file, err := os.Open(s.Path(agent))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the stop causes of %s: %w", agent, err)
	}
	defer file.Close()
	var entries []StopCauseEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedStopCauseBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var decoded StopCauseEntry
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			return nil, fmt.Errorf("decode the stop causes of %s: %w", agent, err)
		}
		if err := decoded.Validate(); err != nil {
			return nil, fmt.Errorf("decode the stop causes of %s: %w", agent, err)
		}
		if decoded.ProductID != s.productID || decoded.Agent != agent {
			return nil, fmt.Errorf("decode the stop causes of %s: an entry for product %q and instance %q is in this record", agent, decoded.ProductID, decoded.Agent)
		}
		entries = append(entries, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the stop causes of %s: %w", agent, err)
	}
	return entries, nil
}

// Account is one instance's record folded.
func (s *StopCauseStore) Account(agent string) (StopCauseAccount, error) {
	entries, err := s.List(agent)
	if err != nil {
		return StopCauseAccount{}, err
	}
	return FoldStopCauses(entries), nil
}

// Record reads one instance's record, hands the account to decide, and appends
// the entries it returns, all under one lock across processes, so two turns
// recording the same stop cannot each find it uncounted. Each entry is
// stamped with the store's product and the instance, and with the moment given
// where it carries none. It returns the account as the record now stands,
// including whatever was appended before an append failed.
func (s *StopCauseStore) Record(ctx context.Context, agent string, at time.Time, decide func(StopCauseAccount) ([]StopCauseEntry, error)) (StopCauseAccount, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return StopCauseAccount{}, err
	}
	unlock, err := s.lock(ctx, agent)
	if err != nil {
		return StopCauseAccount{}, err
	}
	defer unlock()
	entries, err := s.List(agent)
	if err != nil {
		return StopCauseAccount{}, err
	}
	added, decideErr := decide(FoldStopCauses(entries))
	var problems []error
	if decideErr != nil {
		problems = append(problems, decideErr)
	}
	for _, entry := range added {
		entry.SchemaVersion = StopCauseSchemaVersion
		entry.ProductID = s.productID
		entry.Agent = agent
		if entry.RecordedAt.IsZero() {
			entry.RecordedAt = at
		}
		entry.RecordedAt = entry.RecordedAt.UTC()
		if err := entry.Validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		if err := s.append(entry); err != nil {
			problems = append(problems, err)
			break
		}
		entries = append(entries, entry)
	}
	return FoldStopCauses(entries), errors.Join(problems...)
}

func (s *StopCauseStore) lock(ctx context.Context, agent string) (func(), error) {
	directory := filepath.Join(s.root, agent)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the program manager directory of %s: %w", agent, err)
	}
	file, err := os.OpenFile(s.Path(agent)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the stop cause lock of %s: %w", agent, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, stopCauseLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the stop causes of %s: %w", agent, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

func (s *StopCauseStore) append(entry StopCauseEntry) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entry); err != nil {
		return fmt.Errorf("encode stop cause entry: %w", err)
	}
	encoded := buffer.Bytes()
	if len(encoded) > maxEncodedStopCauseBytes {
		return fmt.Errorf("encoded stop cause entry is %d bytes, limit is %d", len(encoded), maxEncodedStopCauseBytes)
	}
	logPath := s.Path(entry.Agent)
	_, statErr := os.Stat(logPath)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect the stop causes of %s: %w", entry.Agent, statErr)
	}
	file, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open the stop causes of %s: %w", entry.Agent, err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append stop cause entry: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append stop cause entry: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync the stop causes of %s: %w", entry.Agent, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close the stop causes of %s: %w", entry.Agent, err)
	}
	if created {
		return syncDirectory(filepath.Dir(logPath))
	}
	return nil
}
