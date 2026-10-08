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
