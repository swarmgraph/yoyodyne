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
}

func (r *rulingArchitect) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string, _ orchestrator.RecurringTurnOptions) (orchestrator.Turn, error) {
	r.messages = append(r.messages, message)
	if strings.Contains(message, "(item-ruled) [") {
		appendFramedNote(&r.listing.items[0], *r.clock, runstate.RulingNoteOpening+"design-x (Design X, document document-3.1) by the architect in conversation chat-a, after turn 3.\n\nIt is finished and sent for publication.\n\nThe design.")
	}
	return orchestrator.Turn{Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "Looked at the waiting work."}}, nil
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
	repository := t.TempDir()
	design := filepath.Join(repository, "docs", "designs", "design-x.md")
	if err := os.MkdirAll(filepath.Dir(design), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(design, []byte("# Design X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 5, 15, 0, 0, time.UTC)
	listing := &passWorkListing{items: []beads.WorkItem{{
		ID: "item-ruled", Title: "Rule on design X", Status: "open", Priority: 1,
		Description: "The ruling rests on docs/designs/design-x.md.",
		Executor:    domain.ConversationWith(domain.RoleArchitect), UpdatedAt: now.Add(-time.Hour),
	}}}
	role := &rulingArchitect{listing: listing, clock: &now}
	store, err := runstate.NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{"architect-pass": {
			Role: domain.RoleArchitect, Every: config.Duration(45 * time.Minute), Enabled: true, Prompt: "Rule on waiting work.", MaxTurns: 1,
		}},
		Claims: store, Reports: store, Roles: role, Clock: settableClock{at: &now},
		ConversationWork: sweepConversationWork{items: listing, repository: repository},
	}
	fire := func(step string) runstate.Sweep {
		t.Helper()
		now = now.Add(time.Hour)
		if fired, err := trigger.Fire(context.Background()); err != nil || len(fired.Fired) != 1 || fired.Fired[0].Problem != "" {
			t.Fatalf("%s: Fire() = %+v, %v", step, fired, err)
		}
		recorded, _, err := store.List()
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
	handed := func(step string, want bool) {
		t.Helper()
		message := role.messages[len(role.messages)-1]
		if got := strings.Contains(message, "(item-ruled) ["); got != want {
			t.Fatalf("%s: item handed = %v, want %v:\n%s", step, got, want, message)
		}
	}

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
	if rendered := renderSweep(second); !strings.Contains(rendered, "left out 1 item waiting in the architect's conversation because the architect's ruling on each stands and nothing about it had changed since the last pass: item-ruled") {
		t.Fatalf("the record does not say what was left out:\n%s", rendered)
	}

	// A new note after the ruling is something to rule on.
	now = now.Add(time.Minute)
	appendFramedNote(&listing.items[0], now, "Answer from the Lead Product Manager: take the second option.")
	third := fire("pass after a new note")
	handed("pass after a new note", true)
	if len(third.RulingsStanding) != 0 {
		t.Fatalf("pass after a new note standing = %v, want none", third.RulingsStanding)
	}

	fire("pass after ruling again")
	handed("pass after ruling again", false)

	// So is a change to a document the item cites.
	if err := os.WriteFile(design, []byte("# Design X\n\nRevised.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fire("pass after the cited document changed")
	handed("pass after the cited document changed", true)
}
