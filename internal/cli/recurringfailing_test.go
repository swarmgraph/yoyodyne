package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// steppedClock is a clock a test moves by hand, so a recurring task's cadence
// comes round without the test waiting an hour.
type steppedClock struct{ at time.Time }

func (c *steppedClock) Now() time.Time { return c.at }

// A recurring task fired twice into a message its own bound refuses is, from
// the second firing, an entry on the needs-a-human line naming the task, the
// failure, and the count, as the harness's move; the channel says it once as a
// warning, once more as critical after two hours, and nothing further; and a
// firing that takes a turn ends it.
//
// The firings go through the role's real conversation, opened the way Wake
// opens it, over a provider that answers whatever reaches it — so the refusal
// the record carries is the conversation's own, and the provider is asked
// nothing until the message fits.
func TestARecurringTaskFailingBeforeItsFirstTurnTwiceIsRaisedOnTheAttentionLineAndInTheChannel(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	task := config.RecurringTask{
		Role:    domain.RoleDevelopmentManager,
		Every:   config.Duration(time.Hour),
		Enabled: true,
		// A prompt past the bound on what a pass may put into a conversation,
		// standing in for the triage docket that grew past it on 2026-09-26.
		Prompt: strings.Repeat("look at the docket. ", chat.MaxPassMessageBytes/10),
	}
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"development-manager": {Role: domain.RoleDevelopmentManager, Backend: domain.BackendClaudeCode, Model: "fable"},
		},
		RecurringTasks: map[string]config.RecurringTask{"development-manager-sweep": task},
	}
	conversations, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	provider := &modelRecordingBackend{}
	open := func(_ context.Context, role domain.AgentRole, _, model string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        "development-manager",
			Backend:      provider,
			Store:        conversations,
			Model:        "fable",
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "yoyodyne",
			RepositoryID: "yoyodyne",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
		})
		return session, nil, nil, err
	}
	sweeps, err := runstate.NewSweepStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	clock := &steppedClock{at: time.Date(2026, 9, 26, 6, 39, 0, 0, time.UTC)}
	trigger := orchestrator.Trigger{
		Tasks:   cfg.RecurringTasks,
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   roleConversation{open: open},
		Clock:   clock,
	}
	standing := readmodel.Sources{Passes: sweeps, Now: func() time.Time { return clock.at }}
	fire := func() orchestrator.Fired {
		t.Helper()
		fired, err := trigger.Fire(context.Background())
		if err != nil {
			t.Fatalf("Fire() error = %v", err)
		}
		if len(fired.Fired) != 1 {
			t.Fatalf("fired = %+v, want the one due task", fired.Fired)
		}
		return fired.Fired[0]
	}
	failingEntries := func() []readmodel.Attention {
		t.Helper()
		var found []readmodel.Attention
		for _, entry := range readmodel.ReadStanding(context.Background(), standing).NeedsHuman {
			if entry.Kind == readmodel.AttentionFailingTask {
				found = append(found, entry)
			}
		}
		return found
	}

	// The first firing is a failed firing, recorded as one, and is not yet a
	// task that is failing.
	first := fire()
	if first.NotStarted != runstate.PreTurnMessageRefused || first.Turns != 0 {
		t.Fatalf("first firing = %+v, want a failed firing refused on its message and no turn", first)
	}
	if len(provider.models) != 0 {
		t.Fatalf("the provider was asked %d time(s), want none: the message never left the harness", len(provider.models))
	}
	if entries := failingEntries(); len(entries) != 0 {
		t.Fatalf("after one failed firing the attention line carries %+v, want nothing yet", entries)
	}

	// The second, an hour later, makes it an entry.
	clock.at = clock.at.Add(time.Hour)
	fire()
	recorded, _, err := sweeps.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, pass := range recorded {
		if pass.NotStarted != runstate.PreTurnMessageRefused || strings.Contains(pass.Problem, "its pass is partial") {
			t.Fatalf("sweep record = %+v, want a failed firing rather than a partial pass", pass)
		}
		if pass.Model != "" {
			t.Fatalf("sweep record names model %q, want none: no turn ran on anything", pass.Model)
		}
		if !strings.Contains(pass.Problem, "limit is") {
			t.Fatalf("sweep record problem = %q, want the refusal's own words", pass.Problem)
		}
	}
	entries := failingEntries()
	if len(entries) != 1 {
		t.Fatalf("attention line carries %d failing-task entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.ID != "development-manager-sweep" || entry.Mover != readmodel.MoverHarness {
		t.Fatalf("entry = %+v, want the task named as the harness's move", entry)
	}
	for _, said := range []string{"development-manager-sweep", "2 times in a row", "refused the message", "limit is"} {
		if !strings.Contains(entry.What(), said) {
			t.Fatalf("entry says %q, want it to say %q", entry.What(), said)
		}
	}
	if !strings.HasPrefix(entry.Whose(), "the harness's — ") {
		t.Fatalf("entry whose = %q, want the harness's move", entry.Whose())
	}

	// The channel says it once, as a warning.
	feed := channelFeed(t, root, &standing, func() time.Time { return clock.at })
	cursors := slack.Cursors{SchemaVersion: slack.CursorsSchemaVersion, Since: clock.at.Add(-24 * time.Hour), Streams: map[string]slack.Cursor{}}
	cursors, said := pollFailing(t, feed, cursors)
	if len(said) != 1 || said[0].Severity != report.SeverityWarning {
		t.Fatalf("channel said %+v, want one warning", said)
	}
	if rendered := renderEvent(t, said[0]); !strings.Contains(rendered, "development-manager-sweep") || !strings.Contains(rendered, "2 times in a row") {
		t.Fatalf("the channel line is %q, want the task and the count", rendered)
	}
	// And not again while it stands.
	cursors, said = pollFailing(t, feed, cursors)
	if len(said) != 0 {
		t.Fatalf("channel said %+v again, want nothing while it stands", said)
	}
	// Once more, as critical, when it has stood two hours.
	clock.at = clock.at.Add(2 * time.Hour)
	cursors, said = pollFailing(t, feed, cursors)
	if len(said) != 1 || said[0].Severity != report.SeverityCritical {
		t.Fatalf("channel said %+v after two hours, want one critical", said)
	}
	cursors, said = pollFailing(t, feed, cursors)
	if len(said) != 0 {
		t.Fatalf("channel said %+v again after the critical, want nothing", said)
	}

	// A firing that takes a turn ends it, on the line and in the channel.
	fixed := task
	fixed.Prompt = "look at the docket."
	trigger.Tasks = map[string]config.RecurringTask{"development-manager-sweep": fixed}
	if taken := fire(); taken.Turns != 1 || taken.NotStarted != "" {
		t.Fatalf("the firing after the fix = %+v, want a turn taken", taken)
	}
	if entries := failingEntries(); len(entries) != 0 {
		t.Fatalf("after a firing took a turn the attention line still carries %+v", entries)
	}
	if _, said = pollFailing(t, feed, cursors); len(said) != 0 {
		t.Fatalf("channel said %+v once the task took a turn, want nothing", said)
	}
}

// channelFeed is the sink's feed over this test's state root, reading the
// standing through the sources the test hands it.
func channelFeed(t *testing.T, root string, standing *readmodel.Sources, now func() time.Time) *slack.HarnessFeed {
	t.Helper()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	reports, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	amendments, err := runstate.NewAmendmentStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	return &slack.HarnessFeed{
		Runs:      runs,
		Reports:   reports,
		Proposals: amendments,
		Intake:    intake,
		Holds:     holds,
		Standing:  standing,
		Now:       now,
		Log:       func(string, ...any) {},
	}
}

// pollFailing makes one pass and returns the cursors as the sink would write
// them, with what it said about a failing recurring task.
func pollFailing(t *testing.T, feed *slack.HarnessFeed, cursors slack.Cursors) (slack.Cursors, []notify.Event) {
	t.Helper()
	batch, err := feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	advanced := slack.Cursors{SchemaVersion: cursors.SchemaVersion, Since: cursors.Since, Streams: map[string]slack.Cursor{}}
	for stream, cursor := range cursors.Streams {
		advanced.Streams[stream] = cursor
	}
	var said []notify.Event
	for _, delivery := range batch.Deliveries {
		advanced.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Posts() && delivery.Notification.Event.Kind == notify.KindRecurringTaskFailing {
			said = append(said, delivery.Notification.Event)
		}
	}
	return advanced, said
}

func renderEvent(t *testing.T, event notify.Event) string {
	t.Helper()
	message, err := notify.Render(notify.Product(), notify.Harness(), event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return message.Body
}
