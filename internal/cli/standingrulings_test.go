package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// rulingArchitect rules on the item whenever a pass hands it to her, the way a
// document answering it does: a ruling note appended to the item, which changes
// when the tracker says it last changed.
type rulingArchitect struct {
	listing  *passWorkListing
	clock    *time.Time
	messages []string
	// leave has her leave what she is handed; during runs while her turn does.
	leave  bool
	during func()
}

func (r *rulingArchitect) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string, _ orchestrator.RecurringTurnOptions) (orchestrator.Turn, error) {
	r.messages = append(r.messages, message)
	if r.during != nil {
		r.during()
	}
	var acted []string
	if !r.leave && strings.Contains(message, "(item-ruled) [") {
		appendFramedNote(&r.listing.items[0], *r.clock, runstate.RulingNoteOpening+"design-x (Design X, document document-3.1) by the architect in conversation chat-a, after turn 3.\n\nIt is finished and sent for publication.\n\nThe design.")
		acted = []string{"item-ruled"}
	}
	return orchestrator.Turn{ActedOn: acted, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "Looked at the waiting work."}}, nil
}

func appendFramedNote(item *beads.WorkItem, at time.Time, note string) {
	if item.FramedNotes != "" {
		item.FramedNotes += "\n"
	}
	item.FramedNotes += beads.FrameNote(note)
	item.Notes = beads.PlainNotes(item.FramedNotes)
	item.UpdatedAt = at
}

// A ruling the architect recorded is not ruled on again by her next pass while
// nothing about the item has changed, the pass's record says so, and a new note,
// or a change to a document the item cites, puts it back in front of her.
func TestAStandingRulingIsNotRuledAgainUntilSomethingNewArrives(t *testing.T) {
	t.Parallel()
	f := newRulingPasses(t)
	listing, role, fire, handed := f.listing, f.role, f.fire, f.handed

	first := fire("first pass")
	handed("first pass", true)
	if len(first.Rulings) != 1 || first.Rulings[0].ID != "item-ruled" || len(first.RulingsStanding) != 0 {
		t.Fatalf("first pass rulings = %+v, standing = %v, want the new ruling recorded", first.Rulings, first.RulingsStanding)
	}

	second := fire("second pass")
	handed("second pass", false)
	if len(second.RulingsStanding) != 1 || second.RulingsStanding[0] != "item-ruled" || len(second.Rulings) != 1 {
		t.Fatalf("second pass standing = %v, rulings = %+v, want the item left out and still recorded", second.RulingsStanding, second.Rulings)
	}
	if message := role.messages[1]; !strings.Contains(message, "Left out of this list because your ruling on each stands (1): item-ruled.") {
		t.Fatalf("the pass is not told what was left out:\n%s", message)
	}
	if rendered := renderSweep(second); !strings.Contains(rendered, "left out 1 item waiting in the architect's conversation because the architect's ruling on each stands and nothing about it had changed since it was dealt with: item-ruled") {
		t.Fatalf("the record does not say what was left out:\n%s", rendered)
	}

	// A new note after the ruling is something to rule on.
	f.now = f.now.Add(time.Minute)
	appendFramedNote(&listing.items[0], f.now, "Answer from the Lead Product Manager: take the second option.")
	third := fire("pass after a new note")
	handed("pass after a new note", true)
	if len(third.RulingsStanding) != 0 {
		t.Fatalf("pass after a new note standing = %v, want none", third.RulingsStanding)
	}

	fire("pass after ruling again")
	handed("pass after ruling again", false)

	// So is a change to a document the item cites.
	f.revise("Revised.")
	fire("pass after the cited document changed")
	handed("pass after the cited document changed", true)
}

// A change a pass was handed and left, or one that arrives while a pass runs,
// is handed to the next pass: the pass's end never makes it part of what stood.
func TestAChangeNoPassDealtWithIsHandedToTheNext(t *testing.T) {
	t.Parallel()

	t.Run("left by the pass it was handed to", func(t *testing.T) {
		t.Parallel()
		f := newRulingPasses(t)
		f.fire("ruled")
		f.revise("Revised.")
		f.role.leave = true
		if left := f.fire("left"); len(left.Rulings) != 0 {
			t.Fatalf("a pass that left the change recorded %+v", left.Rulings)
		}
		f.handed("left", true)
		f.fire("after it was left")
		f.handed("after it was left", true)
		f.role.leave = false
		f.fire("ruled again")
		f.fire("after ruling again")
		f.handed("after ruling again", false)
	})

	for _, test := range []struct {
		name   string
		change func(f *rulingPasses)
	}{
		{"cited document", func(f *rulingPasses) { f.revise("Revised.") }},
		{"report", func(f *rulingPasses) { f.reportItem() }},
		{"edit", func(f *rulingPasses) { f.listing.items[0].Priority = 0 }},
	} {
		for _, when := range []string{"left out", "ruled on"} {
			t.Run(test.name+" arriving while a pass runs on an item it "+when, func(t *testing.T) {
				t.Parallel()
				f := newRulingPasses(t)
				if when == "left out" {
					f.fire("ruled")
				}
				f.role.during = func() { test.change(f) }
				f.fire("change arrives")
				f.handed("change arrives", when == "ruled on")
				f.role.during = nil
				f.fire("after the change")
				f.handed("after the change", true)
				f.fire("after ruling on the change")
				f.handed("after ruling on the change", false)
			})
		}
	}
}

// rulingPasses is an architect's recurring task with one item waiting on her
// that cites a document.
type rulingPasses struct {
	t       *testing.T
	now     time.Time
	design  string
	listing *passWorkListing
	role    *rulingArchitect
	pile    *rulingReports
	store   *runstate.SweepStore
	trigger orchestrator.Trigger
}

type rulingReports struct{ filed []report.Report }

func (r *rulingReports) List() ([]report.Report, error) { return r.filed, nil }

func (r *rulingReports) Handlings() ([]report.Handling, error) { return nil, nil }

func (f *rulingPasses) revise(text string) {
	if err := os.WriteFile(f.design, []byte("# Design X\n\n"+text+"\n"), 0o644); err != nil {
		f.t.Error(err)
	}
}

func (f *rulingPasses) reportItem() {
	// Filed after the pass read its work and before the pass ends.
	f.now = f.now.Add(time.Second)
	f.pile.filed = append(f.pile.filed, report.Report{Role: domain.RoleDeveloper, Message: "The design for item-ruled contradicts the brief.", RecordedAt: f.now})
	f.now = f.now.Add(time.Second)
}

func newRulingPasses(t *testing.T) *rulingPasses {
	t.Helper()
	f := &rulingPasses{t: t}
	repository := t.TempDir()
	f.design = filepath.Join(repository, "docs", "designs", "design-x.md")
	if err := os.MkdirAll(filepath.Dir(f.design), 0o755); err != nil {
		t.Fatal(err)
	}
	f.revise("First.")
	f.now = time.Date(2026, 11, 10, 5, 15, 0, 0, time.UTC)
	f.listing = &passWorkListing{items: []beads.WorkItem{{
		ID: "item-ruled", Title: "Rule on design X", Status: "open", Priority: 1,
		Description: "The ruling rests on docs/designs/design-x.md.",
		Executor:    domain.ConversationWith(domain.RoleArchitect), UpdatedAt: f.now.Add(-time.Hour),
	}}}
	f.role = &rulingArchitect{listing: f.listing, clock: &f.now}
	f.pile = &rulingReports{}
	store, err := runstate.NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{"architect-pass": {
			Role: domain.RoleArchitect, Every: config.Duration(45 * time.Minute), Enabled: true, Prompt: "Rule on waiting work.", MaxTurns: 1,
		}},
		Claims: store, Reports: store, Roles: f.role, Clock: settableClock{at: &f.now}, Pile: f.pile,
		ConversationWork: sweepConversationWork{items: f.listing, repository: repository},
	}
	f.trigger = trigger
	return f
}

func (f *rulingPasses) fire(step string) runstate.Sweep {
	t := f.t
	t.Helper()
	f.now = f.now.Add(time.Hour)
	if fired, err := f.trigger.Fire(context.Background()); err != nil || len(fired.Fired) != 1 || fired.Fired[0].Problem != "" {
		t.Fatalf("%s: Fire() = %+v, %v", step, fired, err)
	}
	recorded, _, err := f.store.List()
	if err != nil || len(recorded) == 0 {
		t.Fatalf("%s: List() = %d passes, %v", step, len(recorded), err)
	}
	latest := recorded[0]
	for _, pass := range recorded {
		if pass.StartedAt.After(latest.StartedAt) {
			latest = pass
		}
	}
	return latest
}

func (f *rulingPasses) handed(step string, want bool) {
	f.t.Helper()
	message := f.role.messages[len(f.role.messages)-1]
	if got := strings.Contains(message, "(item-ruled) ["); got != want {
		f.t.Fatalf("%s: item handed = %v, want %v:\n%s", step, got, want, message)
	}
}
