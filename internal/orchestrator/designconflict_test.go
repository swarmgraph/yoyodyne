package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	conflictLanded = "# Recovery\n\nThe widget spins once through internal/widget/widget.go, and nothing else moves.\n"
	conflictHeld   = "# Recovery\n\nThe widget spins once through internal/widget/widget.go, and the\nrevision adds a second turn.\n"
	conflictQuote  = "The widget spins once through internal/widget/widget.go, and nothing else moves."
)

func conflictDesign() artifact.Artifact {
	return artifact.Artifact{ID: "recovery-design", Kind: artifact.KindDesign, Title: "Recovery design", Path: "docs/designs/recovery-design.md"}
}

func TestDesignConflictRule(t *testing.T) {
	t.Parallel()

	wrapped := "The widget spins once through   internal/widget/widget.go,\nand nothing else moves."
	cases := []struct {
		name string
		item beads.WorkItem
		want bool
	}{
		{"a dropped passage quoted by an item naming the design by id", beads.WorkItem{ID: "x-1", Description: `Follow recovery-design: "` + conflictQuote + `"`}, true},
		{"curly quotes", beads.WorkItem{ID: "x-1", Design: "See recovery-design, which says “" + conflictQuote + "”"}, true},
		{"backticks", beads.WorkItem{ID: "x-1", Notes: "recovery-design says `" + conflictQuote + "`"}, true},
		{"the passage wrapped differently", beads.WorkItem{ID: "x-1", AcceptanceCriteria: `recovery-design: "` + wrapped + `"`}, true},
		{"the design named by path", beads.WorkItem{ID: "x-1", Title: `"` + conflictQuote + `" docs/designs/recovery-design.md`}, true},
		{"a passage the held copy still has", beads.WorkItem{ID: "x-1", Description: `recovery-design: "The widget spins once through internal/widget/widget.go"`}, false},
		{"an item that does not name the design", beads.WorkItem{ID: "x-1", Description: `"` + conflictQuote + `"`}, false},
		{"the id only as part of a longer word", beads.WorkItem{ID: "x-1", Description: `not-recovery-design-2 says "` + conflictQuote + `"`}, false},
		{"a short quotation", beads.WorkItem{ID: "x-1", Description: `recovery-design: "nothing else"`}, false},
		{"no quotation", beads.WorkItem{ID: "x-1", Description: "recovery-design says nothing else moves"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := designConflicts(conflictDesign(), conflictLanded, conflictHeld, []beads.WorkItem{tc.item})
			if _, found := got["x-1"]; found != tc.want {
				t.Fatalf("designConflicts() = %v, want conflict %v", got, tc.want)
			}
		})
	}
	if got := designConflicts(conflictDesign(), "", conflictHeld, []beads.WorkItem{{ID: "x-1", Description: `recovery-design: "` + conflictQuote + `"`}}); len(got) != 0 {
		t.Fatalf("a design that has not landed conflicts with %v", got)
	}
}

// conflictTracker is the pipeline's tracker with the listing a tracker that
// can list unfinished work has.
type conflictTracker struct {
	WorkTracker
	items []beads.WorkItem
	err   error
}

func (c conflictTracker) ListUnfinished(context.Context) ([]beads.WorkItem, error) {
	return c.items, c.err
}

// conflictReports keeps what is filed and reads it back, as the report store does.
type conflictReports struct {
	mu    sync.Mutex
	kept  []report.Report
	fails bool
}

func (c *conflictReports) Append(r report.Report) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fails {
		return errors.New("the pile is full")
	}
	c.kept = append(c.kept, r)
	return nil
}

func (c *conflictReports) List() ([]report.Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]report.Report(nil), c.kept...), nil
}

func (c *conflictReports) all() []report.Report {
	kept, _ := c.List()
	return kept
}

func admittedItem() beads.WorkItem {
	return beads.WorkItem{ID: "yoyodyne-admitted.1", Title: "Spin the widget once", Status: "open",
		Description: `Implement recovery-design exactly as it says: "` + conflictQuote + `"`}
}

// requireTwoReports checks the pair a conflict files, one for each role.
func requireTwoReports(t *testing.T, kept []report.Report, document runstate.DocumentPublication) {
	t.Helper()
	if len(kept) != 2 {
		t.Fatalf("reports = %+v, want two", kept)
	}
	var got []domain.AgentRole
	for _, r := range kept {
		got = append(got, r.For)
		if r.Severity != report.SeverityWarning || r.Role != report.HarnessReporter || r.WorkItemID != "yoyodyne-admitted.1" {
			t.Errorf("report = %+v; want a harness warning about the item", r)
		}
		for _, want := range []string{landedDesign, "Recovery design", "yoyodyne-admitted.1", "Spin the widget once", "(document " + document.WriteID + ")", "nothing else moves", "One of them has to change"} {
			if !strings.Contains(r.Message, want) {
				t.Errorf("report says %q, want it to say %q", r.Message, want)
			}
		}
		if err := r.Validate(); err != nil {
			t.Errorf("report is invalid: %v", err)
		}
	}
	if got[0] != domain.RoleArchitect || got[1] != domain.RoleProductManager {
		t.Fatalf("reports are for %v, want the architect and the Lead Product Manager", got)
	}
}

// A revision that drops wording an admitted item quotes is reported to both
// roles while the revision itself still lands.
func TestARevisionDroppingWhatAnAdmittedItemQuotesIsReportedToBothRoles(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	first := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	if delivery, err := f.pipeline.PublishDocument(context.Background(), first); err != nil || !delivery.Landed {
		t.Fatalf("PublishDocument() = %+v, %v; want it landed", delivery, err)
	}
	reports := &conflictReports{}
	f.pipeline.Reports = reports
	f.pipeline.Tracker = conflictTracker{WorkTracker: f.pipeline.Tracker, items: []beads.WorkItem{admittedItem()}}

	revision := f.confirm(t, designWrite(artifact.WriteRevise, revisedBody), "document-2.1", 2)
	delivery, err := f.pipeline.PublishDocument(context.Background(), revision)
	if err != nil || !delivery.Landed {
		t.Fatalf("PublishDocument(revision) = %+v, %v; the check must not stop the landing", delivery, err)
	}
	requireTwoReports(t, reports.all(), revision)
}

// A document the intake hold keeps from starting is checked and reported, and
// offering it again files nothing more.
func TestAnIntakeHeldDesignIsCheckedAndReportedOnce(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	first := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	if delivery, err := f.pipeline.PublishDocument(context.Background(), first); err != nil || !delivery.Landed {
		t.Fatalf("PublishDocument() = %+v, %v; want it landed", delivery, err)
	}
	revision := f.confirm(t, designWrite(artifact.WriteRevise, revisedBody), "document-2.1", 2)

	intake := newIntakeHoldStore(t)
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere wrong", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	reports := &conflictReports{}
	held := f.pipeline
	held.Intake = intake
	held.Reports = reports
	held.Tracker = conflictTracker{WorkTracker: f.pipeline.Tracker, items: []beads.WorkItem{admittedItem()}}
	for range 2 {
		delivery, _ := held.PublishDocument(context.Background(), revision)
		if delivery.Landed {
			t.Fatalf("delivery = %+v; the intake hold should have kept the document from landing", delivery)
		}
	}
	requireTwoReports(t, reports.all(), revision)

	// A later revision is another handoff and is reported again.
	later := f.confirm(t, designWrite(artifact.WriteRevise, revisedBody+"\nMore.\n"), "document-3.1", 3)
	held.PublishDocument(context.Background(), later)
	if got := len(reports.all()); got != 4 {
		t.Fatalf("reports = %d, want four after a second handoff", got)
	}
}

// A landing that keeps the wording the item quotes, an item that does not name
// the design, a tracker that cannot list, and a pile that cannot be written
// each file nothing and change nothing.
func TestARevisionNoItemDisagreesWithFilesNoReport(t *testing.T) {
	t.Parallel()

	f := newCompanionFixture(t)
	first := f.confirm(t, designWrite(artifact.WriteCreate, firstBody), "document-1.1", 1)
	if delivery, err := f.pipeline.PublishDocument(context.Background(), first); err != nil || !delivery.Landed {
		t.Fatalf("PublishDocument() = %+v, %v; want it landed", delivery, err)
	}
	reports := &conflictReports{}
	f.pipeline.Reports = reports
	kept := admittedItem()
	kept.Description = `recovery-design: "The widget spins once through internal/widget/widget.go"`
	unnamed := admittedItem()
	unnamed.Description = `"` + conflictQuote + `"`
	f.pipeline.Tracker = conflictTracker{WorkTracker: f.pipeline.Tracker, items: []beads.WorkItem{kept, unnamed}}

	revision := f.confirm(t, designWrite(artifact.WriteRevise, revisedBody), "document-2.1", 2)
	if delivery, err := f.pipeline.PublishDocument(context.Background(), revision); err != nil || !delivery.Landed {
		t.Fatalf("PublishDocument(revision) = %+v, %v; want it landed", delivery, err)
	}
	if got := reports.all(); len(got) != 0 {
		t.Fatalf("reports = %+v, want none", got)
	}
}
