package releasecut

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// oweTag parks the fixture's release item as the run that landed its notes
// leaves it.
func (f *fixture) oweTag() {
	f.tracker.mu.Lock()
	defer f.tracker.mu.Unlock()
	f.tracker.items[0].Parking = TagOwedParking("v0.6.0", "run-notes")
}

// tagByHand cuts and pushes the tag as a person does, on commit.
func (f *fixture) tagByHand(t *testing.T, commit string) {
	t.Helper()
	git(t, f.repository, "tag", "-a", "v0.6.0", "-m", "v0.6.0", commit)
	git(t, f.repository, "push", "-q", "origin", "refs/tags/v0.6.0")
}

func (f *fixture) item() beads.WorkItem {
	f.tracker.mu.Lock()
	defer f.tracker.mu.Unlock()
	return f.tracker.items[0]
}

func TestTagOwedParkingIsReadBackAndAnotherParkingIsNot(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{ID: "calc-fvn", Title: "Cut v0.6.0: the release", Status: "open",
		Origin: domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}}
	if _, owed := OwesTag(item); owed {
		t.Fatalf("an unparked release reads as owing its tag")
	}
	item.Parking = TagOwedParking("v0.6.0", "run-1")
	if version, owed := OwesTag(item); !owed || version != "v0.6.0" || heldBack(item) {
		t.Fatalf("OwesTag = %q, %v, heldBack = %v; want v0.6.0 owed and not held back", version, owed, heldBack(item))
	}
	item.Parking = domain.WorkItemParking("held until the operator decides whether v0.6.0 is skipped")
	if _, owed := OwesTag(item); owed || !heldBack(item) {
		t.Fatalf("a parking of the Lead Product Manager's reads as a tag owed")
	}
	item.Title = "Write the v0.6.0 notes"
	item.Parking = TagOwedParking("v0.6.0", "run-1")
	if _, owed := OwesTag(item); owed {
		t.Fatalf("an item that is not a release reads as owing a tag")
	}
}

// Under human the pass runs nothing and tags nothing. It asks the operator once,
// on the item and in a report the Lead Product Manager records as his, naming
// the commands; and once a person's tag is on origin it closes the item with
// the tag and its commit.
func TestUnderHumanAnOwedTagIsTheOperatorsAndTheItemClosesOnceItIsOnOrigin(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.cutter.Mode = domain.ApprovalHuman
	f.oweTag()

	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "make release") {
		t.Fatalf("step = %+v, want it skipped naming the person's verb", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" {
		t.Fatalf("a human-mode pass ran the gates or pushed a tag")
	}
	filed := f.reports.all()
	if len(filed) != 1 || !strings.HasPrefix(filed[0].Message, "human-gate: ") ||
		!strings.Contains(filed[0].Message, "`make release VERSION=v0.6.0`") || !strings.Contains(filed[0].Message, "`git push origin v0.6.0`") ||
		filed[0].WorkItemID != "calc-fvn" {
		t.Fatalf("reports = %+v, want one asking the operator for the cut, naming the commands", filed)
	}
	notes := f.tracker.noted()
	if len(notes) != 1 || !strings.Contains(notes[0], "Cutting v0.6.0 is a step only the operator can take") || !strings.Contains(notes[0], "make release VERSION=v0.6.0") {
		t.Fatalf("notes = %q, want one saying the cut is the operator's step", notes)
	}
	if f.item().Status == "closed" {
		t.Fatalf("the item closed with its tag still owed")
	}

	f.now = f.now.Add(time.Hour)
	f.cutter.Pass(context.Background())
	if len(f.reports.all()) != 1 || len(f.tracker.noted()) != 1 {
		t.Fatalf("a second pass asked again: %d reports, %d notes", len(f.reports.all()), len(f.tracker.noted()))
	}

	f.tagByHand(t, f.named)
	closing := f.cutter.Pass(context.Background())
	if closing.Outcome != runstate.StepRan || !strings.Contains(closing.Detail, "closed calc-fvn") {
		t.Fatalf("step = %+v, want the item closed", closing)
	}
	closed := f.tracker.closures()
	if len(closed) != 1 || !strings.Contains(closed[0], "The v0.6.0 tag is on origin, naming commit "+f.named) {
		t.Fatalf("closures = %q, want one recording the tag and %s", closed, f.named)
	}
}

// Under automatic the release left open for its tag is cut, not passed over as
// parked, and the pass after the push closes it with the tag and its commit.
// Nothing asks the operator.
func TestUnderAutomaticAnOwedTagIsCutAndTheItemClosesOnTheNextPass(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.oweTag()

	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "pushed v0.6.0") {
		t.Fatalf("step = %+v, want the release left open for its tag cut", step)
	}
	if len(f.tracker.closures()) != 0 {
		t.Fatalf("the item closed before origin was read back on a later pass")
	}
	f.now = f.now.Add(10 * time.Minute)
	f.cutter.Pass(context.Background())
	closed := f.tracker.closures()
	if len(closed) != 1 || !strings.Contains(closed[0], "naming commit "+f.named) {
		t.Fatalf("closures = %q, want one recording the tag on %s", closed, f.named)
	}
	for _, filed := range f.reports.all() {
		if strings.HasPrefix(filed.Message, "human-gate: ") {
			t.Errorf("an automatic pass asked the operator: %q", filed.Message)
		}
	}
}

// A release the Lead Product Manager parked herself is hers: it is neither cut
// nor closed, whatever origin holds.
func TestAReleaseParkedForAnotherReasonIsNotClosed(t *testing.T) {
	t.Parallel()
	for _, mode := range []domain.ApprovalMode{domain.ApprovalHuman, domain.ApprovalAutomatic} {
		f := newFixture(t, true)
		f.cutter.Mode = mode
		f.tracker.items[0].Parking = "held until the operator decides whether v0.6.0 is skipped"
		f.tagByHand(t, f.named)
		f.cutter.Pass(context.Background())
		if closed := f.tracker.closures(); len(closed) != 0 {
			t.Errorf("under %s a parked release was closed: %q", mode, closed)
		}
	}
}
