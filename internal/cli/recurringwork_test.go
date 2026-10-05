package cli

import (
	"context"
	"errors"
	"io"
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

// The actual trigger wiring and tracker decoder are exercised here: the queue
// must arrive ordered in the wake, even when the tracker lists it differently.
func TestArchitectPassDeliversWorkByPriorityThenAge(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, items, first, second string
	}{
		{
			name: "newer P0 before older P2",
			items: `[{"id":"item-old","title":"Older design","status":"open","priority":2,"created_at":"2026-10-01T18:00:00Z","metadata":{"yoyodyne_executor":"conversation:architect"}},
			         {"id":"item-new","title":"Urgent design","status":"open","priority":0,"created_at":"2026-10-03T18:00:00Z","metadata":{"yoyodyne_executor":"conversation:architect"}}]`,
			first: "Urgent design (item-new) [P0, open]", second: "Older design (item-old) [P2, open]",
		},
		{
			name: "older first at one priority",
			items: `[{"id":"item-new","title":"Newer design","status":"in_progress","priority":1,"created_at":"2026-10-03T18:00:00Z","metadata":{"yoyodyne_executor":"conversation:architect"}},
			         {"id":"item-old","title":"Older design","status":"blocked","priority":1,"created_at":"2026-10-01T18:00:00Z","metadata":{"yoyodyne_executor":"conversation:architect"}}]`,
			first: "Older design (item-old) [P1, blocked]", second: "Newer design (item-new) [P1, in_progress]",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &statusRunner{stdout: map[string]string{"list": test.items}}
			store, err := runstate.NewStore(t.TempDir(), "example")
			if err != nil {
				t.Fatal(err)
			}
			parts := components{store: store, config: config.Config{
				Product: config.Product{ID: "example"},
				RecurringTasks: map[string]config.RecurringTask{"architect-pass": {
					Role: domain.RoleArchitect, Every: config.Duration(45 * time.Minute), Enabled: true,
					Prompt: "Rule on waiting work.", MaxTurns: 1,
				}},
			}}
			wired := recurringTrigger(parts, "", io.Discard).(*orchestrator.Trigger)
			work := wired.ConversationWork.(sweepConversationWork)
			work.items = chatTracker(runner, "/repo")
			role := &wakeCapture{}
			trigger := orchestrator.Trigger{
				Tasks: parts.config.RecurringTasks, Claims: store.Sweeps(), Reports: store.Sweeps(),
				Roles: role, ConversationWork: work,
			}
			if fired, err := trigger.Fire(context.Background()); err != nil || len(fired.Fired) != 1 || fired.Fired[0].Problem != "" {
				t.Fatalf("Fire() = %+v, %v", fired, err)
			}
			if len(role.messages) != 1 {
				t.Fatalf("messages = %v, want one turn", role.messages)
			}
			message := role.messages[0]
			first, second := strings.Index(message, test.first), strings.Index(message, test.second)
			if first < 0 || second < 0 || first >= second {
				t.Fatalf("want %q before %q with priorities shown:\n%s", test.first, test.second, message)
			}
			if second > strings.Index(message, "Rule on waiting work.") {
				t.Fatalf("the queue follows the task:\n%s", message)
			}
		})
	}
}

type passWorkListing struct {
	items []beads.WorkItem
	err   error
	reads int
}

func (l *passWorkListing) List(context.Context, string) ([]beads.WorkItem, error) {
	l.reads++
	return l.items, l.err
}

func TestRecurringWorkExcludesParkedFinishedAndOtherRoles(t *testing.T) {
	t.Parallel()
	architect := domain.ConversationWith(domain.RoleArchitect)
	listing := &passWorkListing{items: []beads.WorkItem{
		{ID: "item-live", Title: "Live design", Status: "open", Priority: 2, Executor: architect},
		{ID: "item-parked", Status: "open", Executor: architect, Parking: domain.WorkItemParking("waiting for a ruling")},
		{ID: "item-closed", Status: "closed", Executor: architect},
		{ID: "item-deferred", Status: "deferred", Executor: architect},
		{ID: "item-other", Status: "open", Executor: domain.ConversationWith(domain.RoleProductManager)},
		{ID: "item-developer", Status: "open"},
	}}
	message, err := (sweepConversationWork{items: listing}).Read(context.Background(), domain.RoleArchitect)
	if err != nil || !strings.Contains(message, "Live design (item-live) [P2, open]") {
		t.Fatalf("Read() = %q, %v", message, err)
	}
	for _, excluded := range []string{"item-parked", "item-closed", "item-deferred", "item-other", "item-developer"} {
		if strings.Contains(message, excluded) {
			t.Errorf("the queue includes %s:\n%s", excluded, message)
		}
	}
}

func TestRecurringWorkDistinguishesEmptyFromUnreadable(t *testing.T) {
	t.Parallel()
	listing := &passWorkListing{}
	work := sweepConversationWork{items: listing}
	message, err := work.Read(context.Background(), domain.RoleArchitect)
	if err != nil || !strings.Contains(message, "Nothing is waiting") {
		t.Fatalf("empty queue = %q, %v", message, err)
	}
	listing.err = errors.New("tracker listing failed")
	message, err = work.Read(context.Background(), domain.RoleArchitect)
	if err == nil || !strings.Contains(message, "tracker listing failed") || strings.Contains(message, "Nothing is waiting") {
		t.Fatalf("unreadable queue = %q, %v", message, err)
	}
}

type queueChangingRole struct {
	listing  *passWorkListing
	messages []string
}

func (r *queueChangingRole) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string, _ orchestrator.RecurringTurnOptions) (orchestrator.Turn, error) {
	r.messages = append(r.messages, message)
	status := sweep.StatusComplete
	if len(r.messages) == 1 {
		r.listing.items = nil
		status = sweep.StatusMore
	}
	return orchestrator.Turn{Result: &sweep.Result{Status: status, Summary: "Looked at the queue."}}, nil
}

func TestRecurringWorkIsReadAgainForEachTurn(t *testing.T) {
	t.Parallel()
	listing := &passWorkListing{items: []beads.WorkItem{{ID: "item-live", Title: "Live design", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)}}}
	role := &queueChangingRole{listing: listing}
	store, err := runstate.NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{"architect-pass": {
			Role: domain.RoleArchitect, Every: config.Duration(45 * time.Minute), Enabled: true, Prompt: "Rule on waiting work.", MaxTurns: 2,
		}},
		Claims: store, Reports: store, Roles: role, ConversationWork: sweepConversationWork{items: listing},
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if listing.reads != 2 || len(role.messages) != 2 {
		t.Fatalf("reads = %d, turns = %d, want a fresh reading on both turns", listing.reads, len(role.messages))
	}
	if !strings.Contains(role.messages[0], "item-live") || strings.Contains(role.messages[1], "item-live") || !strings.Contains(role.messages[1], "Nothing is waiting") {
		t.Fatalf("the second turn kept the old queue: %v", role.messages)
	}
}
