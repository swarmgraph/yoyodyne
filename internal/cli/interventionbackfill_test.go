package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func loggedEvent(t *testing.T, at time.Time, kind execution.EventType, payload map[string]any) execution.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Event{Type: kind, Timestamp: at, Payload: encoded}
}

// A conversation's log does not say who sent a message, so the backfill tells
// a person's from the harness's by what the log does say: a pass's first
// message follows its start, every other message the harness sends opens with
// words of its own, and a message typed at a console says so. What is left is a
// person's, and only from the moment asked for.
func TestTheBackfillCountsOnlyAPersonsMessages(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 17, 7, 0, 0, 0, time.UTC)
	at := func(hours int) time.Time { return since.Add(time.Duration(hours) * time.Hour) }
	text := func(said string) map[string]any { return map[string]any{"text": said} }
	events := []execution.Event{
		loggedEvent(t, at(-2), execution.EventOperatorMessage, text("From Mason's assistant: before the moment asked for")),
		loggedEvent(t, at(1), execution.EventSessionPassStarted, map[string]any{}),
		loggedEvent(t, at(1), execution.EventOperatorMessage, text("Sweep the backlog for work that serves no goal.")),
		loggedEvent(t, at(2), execution.EventAgentMessage, text("done")),
		loggedEvent(t, at(3), execution.EventOperatorMessage, text("Your previous reply omitted its closing report block.")),
		loggedEvent(t, at(4), execution.EventOperatorMessage, text("Run run-1234 of yoyodyne-ifd.1 stopped: independent review still required repair.")),
		loggedEvent(t, at(5), execution.EventOperatorMessage, text("From Mason's operator assistant: start counting interventions now.")),
		loggedEvent(t, at(6), execution.EventOperatorMessage, map[string]any{"text": "The harness woke you for something I typed", "typed_at_console": true}),
	}
	found := personMessagesIn("chat-1", domain.RoleProductManager, events, since)
	if len(found) != 2 || found[0].text != "From Mason's operator assistant: start counting interventions now." || !found[1].console {
		t.Fatalf("found %+v, want the assistant's message and the one typed at the console", found)
	}
	event, err := backfilledMessage("yoyodyne", found[0], at(10))
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil || event.Kind != intervention.KindMessage || !event.At.Equal(at(5)) {
		t.Fatalf("recorded %+v, %v; want a valid message at the moment it was sent", event, err)
	}
	if !alreadyRecorded([]intervention.Event{event}, found[0]) || alreadyRecorded([]intervention.Event{event}, found[1]) {
		t.Error("a message already recorded is found again, and only that one")
	}
}

// Every phrase the catch-up takes as the harness's own opening is written by
// the harness somewhere outside the tests, so rewording one of those messages
// fails here rather than quietly counting it as the operator's.
func TestEveryHarnessOpeningIsWrittenByTheHarness(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	var sources []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "interventionbackfill.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources = append(sources, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(sources, "\n")
	for _, opening := range harnessOpenings {
		if !strings.Contains(all, opening) {
			t.Errorf("no code outside the tests writes %q any more; update harnessOpenings to the wording the harness now uses", opening)
		}
	}
	escalation := escalationMessage(triage.Entry{RunID: "run-0123abcd", WorkItemID: "yoyodyne-ifd.1", WorkItemTitle: "A title"})
	if !harnessComposed(escalation) {
		t.Errorf("the escalation of a stopped run is not recognized as the harness's: %q", escalation)
	}
	if harnessComposed("Run run-1234 again, please; it was stopped by mistake") {
		t.Error("a person's message beginning with a run is taken for the harness's")
	}
}

// The harness reads the operator's earlier messages back itself, on the first
// `yoyo reconcile` its own maintenance pass runs, records each once, and
// records that it did so, so a later sweep reads nothing again.
func TestTheHarnessCatchesUpTheOperatorsEarlierMessagesOnce(t *testing.T) {
	// Not parallel: the state root and the process's own markers are set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv(execution.StartedByVariable, maintain.StartedByMaintenance)
	t.Setenv(execution.LaunchdJobVariable, "")

	project := t.TempDir()
	git(t, project, "init", "-b", "main")
	git(t, project, "config", "user.name", "Yoyodyne Test")
	git(t, project, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, project, "first")
	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(configPath, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		t.Fatal(err)
	}
	conversations := filepath.Join(home.ProductDirectory(stateRoot, string(resolved.Config.Product.ID)), "conversations")
	if err := os.MkdirAll(conversations, 0o700); err != nil {
		t.Fatal(err)
	}
	sent := time.Date(2026, 10, 20, 16, 0, 0, 0, time.UTC)
	var log strings.Builder
	for index, event := range []execution.Event{
		loggedEvent(t, sent, execution.EventOperatorMessage, map[string]any{"text": "From Mason's assistant: start counting interventions now."}),
		loggedEvent(t, sent.Add(time.Minute), execution.EventAgentMessage, map[string]any{"text": "Noted."}),
		loggedEvent(t, sent.Add(time.Hour), execution.EventOperatorMessage, map[string]any{"text": "Your previous reply omitted its closing report block."}),
	} {
		event.SchemaVersion = execution.EventSchemaVersion
		event.RunID = "chat-0123456789abcdef0123456789abcdef"
		event.Sequence = uint64(index + 1)
		event.Source = "harness.chat"
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		log.Write(append(encoded, '\n'))
	}
	if err := os.WriteFile(filepath.Join(conversations, "chat-0123456789abcdef0123456789abcdef.events.jsonl"), []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	for pass := 0; pass < 2; pass++ {
		if code, _, stderr := runYoyo(t, "reconcile", "--config", configPath); code != 0 {
			t.Fatalf("the maintenance pass's reconcile %d exited %d: %s", pass+1, code, stderr)
		}
	}
	t.Setenv(execution.StartedByVariable, "")
	listed := listedInterventions(t, configPath)
	if len(listed.Interventions) != 1 {
		t.Fatalf("recorded %+v, want the assistant's message once and nothing for the harness's own", listed.Interventions)
	}
	if message := listed.Interventions[0]; message.Kind != intervention.KindMessage || !message.At.Equal(sent) {
		t.Errorf("recorded %+v, want a message at the moment it was sent", message)
	}
	store, err := runstate.NewInterventionStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	caught, done, err := store.MessagesCaughtUp()
	if err != nil || !done || caught.Recorded != 1 || !caught.Since.Equal(messagesCountedFrom) {
		t.Errorf("the record of the catch-up = %+v, %v, %v; want one recorded from %s", caught, done, err, messagesCountedFrom)
	}
	if again := catchUpOperatorMessages(stateRoot, resolved.Config.Product.ID, store, time.Now().UTC()); again.Caught != nil || again.Problem != "" {
		t.Errorf("a later sweep read the logs again: %+v", again)
	}
}
