// Package intervention is a step the operator took by hand, written down as one
// countable event.
//
// One of the operator's delivery measures is how many hand steps each merged
// change cost him. Before this, those steps were scattered across directives,
// run notes, and command logs, and the costliest of them happened outside the
// harness altogether — a scheduler restarted by hand, a branch reset by hand, a
// tracker status edited by hand — where nothing wrote anything down. A count
// nobody can read is not a measure.
//
// So every hand step the operator takes through the harness writes one event,
// and a hand step taken outside it can be written down afterwards by whoever
// noticed it. Both are the same record with the same fields; the second carries
// a mark that it was observed rather than performed, and who took the step and
// who wrote it down. The count read from these is a floor and says so: a step
// nobody recorded is not in it.
//
// An event is never revised. It says that a step was taken, which stays true
// whatever became of the step, so the store is append-only and nothing here
// rewrites one.
package intervention

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// SchemaVersion is 1 and has never changed.
const SchemaVersion = 1

const (
	// MaxSaidBytes bounds what was done, which is a sentence or two.
	MaxSaidBytes = 2 << 10
	// maxLineBytes bounds each value read as one line: an item, the run, what
	// else the step touched, who took it, who recorded it, and where it came in.
	maxLineBytes = 200
	// maxItems bounds how many work items one step can name. A directive is the
	// widest step there is, and it names at most this many too.
	maxItems = 50
	// observedSkew is how far after its recording an observed step may say it
	// happened, to allow for two clocks that disagree. Anything later is a step
	// that has not happened yet.
	observedSkew = time.Minute
)

// Kind is what sort of hand step this was. The kinds are a closed list so the
// count can be broken down by them, and each says in ordinary words what the
// operator did.
type Kind string

const (
	// KindRun is running a work item by name rather than leaving it to the
	// scheduler to choose.
	KindRun Kind = "run"
	// KindRerun is starting a re-run of a stopped item by hand.
	KindRerun Kind = "rerun"
	// KindRepair is starting a repair of a stopped run by hand.
	KindRepair Kind = "repair"
	// KindResume is resuming by hand an approved change the environment stopped
	// short of the target branch.
	KindResume Kind = "resume"
	// KindRearm is asking the forge again, by hand, for a merge it dropped.
	KindRearm Kind = "rearm"
	// KindOverride is crossing one of a work item's triage caps.
	KindOverride Kind = "override"
	// KindStop is stopping a run.
	KindStop Kind = "stop"
	// KindSettle is settling or reconciling what runs left behind, by hand.
	KindSettle Kind = "settle"
	// KindApprove and KindDecline are deciding a proposal, a document, or a
	// proposed change to a document.
	KindApprove Kind = "approve"
	KindDecline Kind = "decline"
	// KindDirective is recording a directive.
	KindDirective Kind = "directive"

	// The kinds below happen only outside the harness, so only an observed event
	// carries one.

	// KindRestart is restarting a part of the product by hand: the scheduler, the
	// Slack sink, the dashboard, or the supervisor.
	KindRestart Kind = "restart"
	// KindReset is moving a branch by hand, such as resetting the target branch.
	KindReset Kind = "reset"
	// KindTracker is changing the tracker by hand: a status, a note, a
	// dependency.
	KindTracker Kind = "tracker"
	// KindOther is a hand step none of the other kinds names.
	KindOther Kind = "other"
)

// performedKinds are the steps the harness carries out on the operator's word,
// in the order they are listed. Each can also be observed: a run stopped by
// killing its process is a stop the harness never saw.
var performedKinds = []Kind{
	KindRun, KindRerun, KindRepair, KindResume, KindRearm, KindOverride,
	KindStop, KindSettle, KindApprove, KindDecline, KindDirective,
}

// outsideKinds are the steps only a person outside the harness takes.
var outsideKinds = []Kind{KindRestart, KindReset, KindTracker, KindOther}

// Kinds is every kind, in the order they are listed.
func Kinds() []Kind {
	return append(append([]Kind{}, performedKinds...), outsideKinds...)
}

// Valid reports one of the kinds above.
func (k Kind) Valid() bool {
	for _, kind := range Kinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// OutsideOnly reports a kind the harness never carries out, so an event of it
// can only have been observed.
func (k Kind) OutsideOnly() bool {
	for _, kind := range outsideKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Describe says what a step of this kind was, in the words a listing uses.
func (k Kind) Describe() string {
	switch k {
	case KindRun:
		return "ran a work item by name"
	case KindRerun:
		return "started a re-run by hand"
	case KindRepair:
		return "started a repair by hand"
	case KindResume:
		return "resumed a stopped integration by hand"
	case KindRearm:
		return "asked the forge again for a dropped merge"
	case KindOverride:
		return "crossed a work item's triage cap"
	case KindStop:
		return "stopped a run"
	case KindSettle:
		return "settled what runs left behind, by hand"
	case KindApprove:
		return "approved a proposal"
	case KindDecline:
		return "declined a proposal"
	case KindDirective:
		return "recorded a directive"
	case KindRestart:
		return "restarted a part of the product by hand"
	case KindReset:
		return "moved a branch by hand"
	case KindTracker:
		return "changed the tracker by hand"
	case KindOther:
		return "took another step by hand"
	default:
		return "took a step the harness does not recognize"
	}
}

// KindNames lists the kinds, for a refusal or a usage line to name them.
func KindNames() []string {
	names := make([]string, 0, len(performedKinds)+len(outsideKinds))
	for _, kind := range Kinds() {
		names = append(names, string(kind))
	}
	return names
}

// Event is one hand step.
type Event struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	Kind          Kind             `json:"kind"`
	// At is when the step was taken. For a step the harness carried out it is
	// the moment it did; for an observed one it is when whoever recorded it says
	// it happened, which can be well before it was written down.
	At time.Time `json:"at"`
	// Items are the work items the step touched, and Run the run. They are what
	// the count per merged change is read by, so a step that touched one says so.
	// Most steps name one item; a directive names every item it is scoped to.
	Items []string `json:"items,omitempty"`
	Run   string   `json:"run,omitempty"`
	// Subject is anything else the step touched that is not a work item or a
	// run: a directive, a proposal, a document, a part of the product, a branch.
	Subject string `json:"subject,omitempty"`
	// Said is what was done, in a sentence.
	Said string `json:"said"`
	// Via is where a step the harness carried out came in: the command line, or
	// the conversation it was typed into. An observed step came in through
	// nothing of the harness's and has none.
	Via string `json:"via,omitempty"`
	// Observed marks a step taken outside the harness and written down after the
	// fact. By is who took it and RecordedBy who wrote it down, which are often
	// the same person and are both asked for because they need not be; a role
	// that noticed a step records its own name and its role in RecordedRole.
	Observed     bool             `json:"observed,omitempty"`
	By           string           `json:"by,omitempty"`
	RecordedBy   string           `json:"recorded_by,omitempty"`
	RecordedRole domain.AgentRole `json:"recorded_role,omitempty"`
	// RecordedAt is when the event was written down.
	RecordedAt time.Time `json:"recorded_at"`
}

var idPattern = regexp.MustCompile(`^intervention-[a-f0-9]{32}$`)

// NewID issues an event identifier.
func NewID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate intervention id: %w", err)
	}
	return "intervention-" + hex.EncodeToString(raw), nil
}

// Validate reports every contract violation in the event at once.
func (e Event) Validate() error {
	var problems []error
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("intervention schema version %d is not supported", e.SchemaVersion))
	}
	if !idPattern.MatchString(e.ID) {
		problems = append(problems, errors.New("intervention id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	switch {
	case !e.Kind.Valid():
		problems = append(problems, fmt.Errorf("kind %q is not a kind of hand step; the kinds are %s", e.Kind, strings.Join(KindNames(), ", ")))
	case e.Kind.OutsideOnly() && !e.Observed:
		problems = append(problems, fmt.Errorf("a %s step is taken outside the harness, so it can only be recorded as observed", e.Kind))
	}
	if e.At.IsZero() {
		problems = append(problems, errors.New("when the step was taken is required"))
	}
	if e.RecordedAt.IsZero() {
		problems = append(problems, errors.New("when the step was recorded is required"))
	}
	if !e.At.IsZero() && !e.RecordedAt.IsZero() && e.At.After(e.RecordedAt.Add(observedSkew)) {
		problems = append(problems, errors.New("the step is recorded as taken after it was written down; a step is recorded once it has happened"))
	}
	if len(e.Items) > maxItems {
		problems = append(problems, fmt.Errorf("the step names %d work items, limit is %d", len(e.Items), maxItems))
	}
	for index, item := range e.Items {
		problems = append(problems, lineProblem(fmt.Sprintf("items[%d]", index), item, true))
	}
	problems = append(problems,
		lineProblem("run", e.Run, false),
		lineProblem("subject", e.Subject, false),
		lineProblem("via", e.Via, false),
		lineProblem("by", e.By, e.Observed),
		lineProblem("recorded by", e.RecordedBy, e.Observed),
	)
	switch said := strings.TrimSpace(e.Said); {
	case said == "":
		problems = append(problems, errors.New("say what was done; a step nobody described is one nobody can check afterwards"))
	case len(said) > MaxSaidBytes:
		problems = append(problems, fmt.Errorf("what was done is %d bytes, limit is %d", len(said), MaxSaidBytes))
	case !utf8.ValidString(said):
		problems = append(problems, errors.New("what was done is not valid UTF-8"))
	}
	if e.Observed {
		if strings.TrimSpace(e.Via) != "" {
			problems = append(problems, errors.New("an observed step came in through nothing of the harness's, so it has no via"))
		}
	} else if strings.TrimSpace(e.Via) == "" {
		problems = append(problems, errors.New("a step the harness carried out says where it came in"))
	}
	if e.RecordedRole != "" {
		if !e.Observed {
			problems = append(problems, errors.New("only an observed step names the role that recorded it"))
		} else if !e.RecordedRole.Valid() {
			problems = append(problems, fmt.Errorf("recorded role %q is not one of the harness's roles", e.RecordedRole))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid intervention: %w", err)
	}
	return nil
}

// Names reports whether the step touched one work item or one run.
func (e Event) Names(workItemID, runID string) bool {
	if runID != "" && e.Run == runID {
		return true
	}
	if workItemID == "" {
		return false
	}
	for _, item := range e.Items {
		if item == workItemID {
			return true
		}
	}
	return false
}

// Sort puts events in the order the steps were taken, ties settled by
// identifier so a listing is stable across processes.
func Sort(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].At.Equal(events[j].At) {
			return events[i].ID < events[j].ID
		}
		return events[i].At.Before(events[j].At)
	})
}

// Render describes one event for a listing, in the operator's local time.
func (e Event) Render(location *time.Location) string {
	if location == nil {
		location = time.Local
	}
	var rendered strings.Builder
	when := e.At.In(location).Format("2006-01-02 15:04 MST")
	if e.Observed {
		fmt.Fprintf(&rendered, "%s  %s %s (observed, recorded by %s)\n", when, e.By, e.Kind.Describe(), e.recorder())
	} else {
		fmt.Fprintf(&rendered, "%s  the operator %s, through %s\n", when, e.Kind.Describe(), e.Via)
	}
	if touched := e.touched(); touched != "" {
		fmt.Fprintf(&rendered, "  touched: %s\n", touched)
	}
	fmt.Fprintf(&rendered, "  %s\n", oneline.Fold(e.Said, MaxSaidBytes))
	return rendered.String()
}

func (e Event) recorder() string {
	if e.RecordedRole != "" {
		return e.RecordedBy + " (as the " + e.RecordedRole.Title() + ")"
	}
	return e.RecordedBy
}

// touched names what the step touched, in one line.
func (e Event) touched() string {
	var parts []string
	if len(e.Items) > 0 {
		parts = append(parts, strings.Join(e.Items, ", "))
	}
	if e.Run != "" {
		parts = append(parts, "run "+e.Run)
	}
	if e.Subject != "" {
		parts = append(parts, e.Subject)
	}
	return strings.Join(parts, "; ")
}

func lineProblem(field, value string, required bool) error {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "" && required:
		return fmt.Errorf("%s is required", field)
	case trimmed == "":
		return nil
	case len(trimmed) > maxLineBytes:
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), maxLineBytes)
	case strings.ContainsAny(trimmed, "\r\n"):
		return fmt.Errorf("%s cannot span lines", field)
	case !utf8.ValidString(trimmed):
		return fmt.Errorf("%s is not valid UTF-8", field)
	default:
		return nil
	}
}
