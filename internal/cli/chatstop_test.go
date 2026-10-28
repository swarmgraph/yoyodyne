package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/budget"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// readableChatTracker holds one item a tracker read can find.
type readableChatTracker struct{ recordingChatTracker }

func (t *readableChatTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Title: "Readable conversations", Description: "Say who is speaking.", Status: "open"}, nil
}

// A one-shot message the message budget stops after one tracker action says
// why it stopped, and does not say the tracker ran out of rounds, in either the
// text or the JSON output.
func TestChatSaysABudgetStopWithoutBlamingTheTracker(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			store, err := runstate.NewConversationStore(root, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			ask := "Reading it.\n\n```yoyodyne-tracker\n" + `{"actions":[{"action":"read","id":"yoyodyne-ifd.22"}]}` + "\n```\n"
			provider := &sequencingBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: ask},
				{SessionID: "session-1", FinalText: "That is what it says."},
			}}
			session, err := chat.Open(chat.Options{
				Role: domain.RoleProductManager, Agent: "product-manager", Backend: provider, Store: store,
				Tracker: &readableChatTracker{}, Model: "opus", Provider: domain.BackendClaudeCode,
				AccountAlias: config.DefaultAccountAlias, Repository: filepath.Join(root, "repository"),
				ProductID: "yoyodyne", RepositoryID: "yoyodyne",
				Briefing:      chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
				MessageBudget: budget.Limits{Turns: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "Tell me about ifd.22.", format == "json", &stdout, &stderr); code != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if provider.calls != 1 {
				t.Fatalf("provider turns = %d, want 1", provider.calls)
			}
			if format == "json" {
				var output chatOutput
				if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
					t.Fatal(err)
				}
				if len(output.Actions) != 1 || !output.ResultsCarriedOver || output.TrackerRoundsSpent ||
					!strings.Contains(output.Stopped, "this message has taken 1 provider turn,") {
					t.Fatalf("output = %+v", output)
				}
				return
			}
			text := stdout.String()
			if !strings.Contains(text, "The harness stopped answering this message: this message has taken 1 provider turn,") {
				t.Fatalf("the stop was not said: %q", text)
			}
			if strings.Contains(text, "ran out of rounds") {
				t.Fatalf("the output blamed the tracker's rounds: %q", text)
			}
		})
	}
}
