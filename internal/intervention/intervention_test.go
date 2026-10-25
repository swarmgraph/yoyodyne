package intervention

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var recordedAt = time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)

func performed(kind Kind) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		ID:            "intervention-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		Kind:          kind,
		At:            recordedAt,
		Items:         []string{"yoyodyne-a6q"},
		Said:          "ran yoyodyne-a6q by name with yoyo run",
		Via:           "the command line",
		RecordedAt:    recordedAt,
	}
}

func observed(kind Kind) Event {
	event := performed(kind)
	event.Via = ""
	event.Observed = true
	event.By = "Mason"
	event.RecordedBy = "Mason"
	event.At = recordedAt.Add(-20 * time.Hour)
	event.Said = "restarted the scheduler by hand"
	return event
}

func TestEveryPerformedKindIsValidThroughTheHarness(t *testing.T) {
	t.Parallel()
	for _, kind := range performedKinds {
		if err := performed(kind).Validate(); err != nil {
			t.Errorf("a %s step the harness carried out: %v", kind, err)
		}
		if err := observed(kind).Validate(); err != nil {
			t.Errorf("a %s step observed outside the harness: %v", kind, err)
		}
	}
}

// A restart, a branch reset, and a tracker edit are steps the harness never
// carries out, so a record claiming it did is refused: it would count a step
// through a door that does not exist.
func TestAnOutsideKindIsOnlyEverObserved(t *testing.T) {
	t.Parallel()
	for _, kind := range outsideKinds {
		if err := observed(kind).Validate(); err != nil {
			t.Errorf("an observed %s: %v", kind, err)
		}
		err := performed(kind).Validate()
		if err == nil || !strings.Contains(err.Error(), "only be recorded as observed") {
			t.Errorf("a %s the harness claims to have carried out: error = %v, want it refused", kind, err)
		}
	}
}

func TestAnObservedStepSaysWhoTookItAndWhoRecordedIt(t *testing.T) {
	t.Parallel()
	nobody := observed(KindRestart)
	nobody.By = ""
	if err := nobody.Validate(); err == nil || !strings.Contains(err.Error(), "by is required") {
		t.Errorf("an observed step nobody took: error = %v", err)
	}
	unrecorded := observed(KindRestart)
	unrecorded.RecordedBy = ""
	if err := unrecorded.Validate(); err == nil || !strings.Contains(err.Error(), "recorded by is required") {
		t.Errorf("an observed step nobody recorded: error = %v", err)
	}
	via := observed(KindRestart)
	via.Via = "the command line"
	if err := via.Validate(); err == nil || !strings.Contains(err.Error(), "no via") {
		t.Errorf("an observed step that came in through the harness: error = %v", err)
	}
	byRole := observed(KindRestart)
	byRole.RecordedBy = "factory-flow"
	byRole.RecordedRole = domain.RoleProgramManager
	if err := byRole.Validate(); err != nil {
		t.Errorf("a step a program manager noticed: %v", err)
	}
	roleOnPerformed := performed(KindRun)
	roleOnPerformed.RecordedRole = domain.RoleProgramManager
	if err := roleOnPerformed.Validate(); err == nil {
		t.Error("a step the harness carried out names a role that recorded it, and was accepted")
	}
}

func TestAStepIsRecordedOnceItHasHappened(t *testing.T) {
	t.Parallel()
	ahead := observed(KindRestart)
	ahead.At = recordedAt.Add(time.Hour)
	if err := ahead.Validate(); err == nil || !strings.Contains(err.Error(), "after it was written down") {
		t.Errorf("a step recorded before it was taken: error = %v", err)
	}
	unsaid := performed(KindStop)
	unsaid.Said = " "
	if err := unsaid.Validate(); err == nil || !strings.Contains(err.Error(), "say what was done") {
		t.Errorf("a step nobody described: error = %v", err)
	}
	unknown := performed("rebooted")
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "the kinds are") {
		t.Errorf("an unknown kind: error = %v", err)
	}
}

func TestNamesReadsTheItemsAndTheRun(t *testing.T) {
	t.Parallel()
	event := performed(KindStop)
	event.Run = "run-0123"
	if !event.Names("yoyodyne-a6q", "") || !event.Names("", "run-0123") || !event.Names("other", "run-0123") {
		t.Error("the step does not name the item or the run it touched")
	}
	if event.Names("other", "run-9") || event.Names("", "") {
		t.Error("the step names something it did not touch")
	}
}

func TestRenderSaysWhetherTheStepWasObserved(t *testing.T) {
	t.Parallel()
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	rendered := observed(KindRestart).Render(pacific)
	for _, wanted := range []string{"Mason restarted a part of the product by hand", "observed, recorded by Mason", "PDT", "yoyodyne-a6q"} {
		if !strings.Contains(rendered, wanted) {
			t.Errorf("rendered %q, want it to say %q", rendered, wanted)
		}
	}
	rendered = performed(KindRun).Render(pacific)
	if !strings.Contains(rendered, "the operator ran a work item by name, through the command line") {
		t.Errorf("rendered %q, want it to say the harness carried it out", rendered)
	}
}

// What a step unblocked is written as a kind and a name, or none, and anything
// else is refused with the kinds named.
func TestWhatAStepUnblockedIsReadAsAPersonWritesIt(t *testing.T) {
	t.Parallel()
	for written, want := range map[string]Unblocked{
		"none":                       {Kind: UnblockedNothing},
		"item:yoyodyne-ifd.436.8":    {Kind: UnblockedItem, ID: "yoyodyne-ifd.436.8"},
		"run: run-1234":              {Kind: UnblockedRun, ID: "run-1234"},
		"part:dashboard":             {Kind: UnblockedPart, ID: "dashboard"},
		"conversation:chat-91253e0e": {Kind: UnblockedConversation, ID: "chat-91253e0e"},
	} {
		got, err := ParseUnblocked(written)
		if err != nil || got != want {
			t.Errorf("ParseUnblocked(%q) = %+v, %v; want %+v", written, got, err, want)
		}
	}
	for _, written := range []string{"", "dashboard", "branch:main", "item:", "none:thing"} {
		if _, err := ParseUnblocked(written); err == nil {
			t.Errorf("ParseUnblocked(%q) was accepted", written)
		}
	}
	event := observed(KindRestart)
	event.Unblocked = &Unblocked{Kind: UnblockedNothing, ID: "something"}
	if err := event.Validate(); err == nil || !strings.Contains(err.Error(), "names nothing it unblocked") {
		t.Errorf("a step that unblocked nothing and names something: error = %v", err)
	}
	no := false
	event.Unblocked = &Unblocked{Kind: UnblockedPart, ID: "dashboard"}
	event.PersonOnly = &no
	if err := event.Validate(); err != nil {
		t.Fatalf("a restart of the dashboard: %v", err)
	}
	rendered := event.Render(time.UTC)
	if !strings.Contains(rendered, "unblocked: the dashboard part of the product") || !strings.Contains(rendered, "the harness should have taken it itself") {
		t.Errorf("rendered %q, want what it unblocked and that the harness should have taken it", rendered)
	}
}

// A message is a step the harness records as it is sent, never one somebody
// observed outside it.
func TestAMessageIsRecordedThroughTheHarness(t *testing.T) {
	t.Parallel()
	if KindMessage.OutsideOnly() || !KindMessage.Valid() {
		t.Fatal("a message is a kind recorded through the harness")
	}
	if err := performed(KindMessage).Validate(); err != nil {
		t.Errorf("a message sent through the harness: %v", err)
	}
}
