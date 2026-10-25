package cli

// `yoyo intervention backfill`: the operator's messages sent before each was
// recorded as it was sent, read back from the conversations' logs.
//
// Every message a person sends a role is now recorded as one of his
// interventions when it is sent (chat's noteMessage). The ones sent before that
// are in the conversations' event logs and nowhere else, and the count of his
// interventions is read from the record of hand steps rather than from those
// logs, which are far too large to read on every `yoyo status`. So this reads
// them once, from a moment the person running it names, and records each
// person's message it finds as a message, at the time it was sent.
//
// A log does not say who sent a message, so this decides it from what the log
// does say: a message typed at a console says so; the first message of a
// recurring pass follows the pass's start; and every other message the harness
// sends a role opens with words of its own, listed in harnessOpenings. What is
// left is counted as a person's. It is run again safely: a message already
// recorded, whether by an earlier backfill or as it was sent, is not recorded
// twice.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// harnessOpenings are the words every message the harness itself sends a role
// opens with, other than a recurring pass's first message: a later turn of a
// recurring pass (internal/orchestrator's recurring and program manager passes,
// and the wakeup after a refused tracker block), the work waiting in a role's
// conversation (internal/contextbundle), the request for a missing closing
// report (sweep.ReportRequest), the critical-report and brake summonses
// (internal/orchestrator), and the escalation of a stopped run (escalate.go).
var harnessOpenings = []string{
	"The harness woke you",
	"## Work waiting in your conversation",
	"The triage docket below was read",
	"Your previous reply omitted its closing",
	"You said the pass of",
	"A critical report was filed",
	"The intake brake summoned you",
	"Run run-",
}

// backfillMatchWindow is how close in time a recorded message has to be to one
// in a log to be taken for the same message.
const backfillMatchWindow = 5 * time.Second

// loggedMessage is one person's message found in a conversation's log.
type loggedMessage struct {
	conversationID string
	role           domain.AgentRole
	at             time.Time
	text           string
	console        bool
}

func backfillInterventions(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("intervention backfill", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	sinceFlag := flags.String("since", "", "the moment to read from, as 2026-10-17T00:00:00-07:00; required")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "intervention backfill does not accept positional arguments")
		return 2
	}
	if refusal := refusedToAgentProcess("yoyo intervention backfill", "it records the operator's own messages"); refusal != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, refusal)
	}
	if strings.TrimSpace(*sinceFlag) == "" {
		return reportInterventionError(stdout, stderr, *jsonOutput,
			errors.New("say where to read from, as --since 2026-10-17T00:00:00-07:00"))
	}
	since, err := time.Parse(time.RFC3339, strings.TrimSpace(*sinceFlag))
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput,
			fmt.Errorf("--since %q is not a time; give one as 2026-10-17T00:00:00-07:00", *sinceFlag))
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	productID := resolved.Config.Product.ID
	store, err := runstate.NewInterventionStore(stateRoot, productID)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	conversations, err := runstate.NewConversationStore(stateRoot, productID)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	found, problems, err := loggedPersonMessages(conversations, since.UTC())
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	existing, err := store.List()
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	var recorded []intervention.Event
	skipped := 0
	now := time.Now().UTC()
	for _, message := range found {
		if alreadyRecorded(existing, message) {
			skipped++
			continue
		}
		event, err := backfilledMessage(productID, message, now)
		if err == nil {
			err = store.Record(event)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("a message in conversation %s at %s could not be recorded: %v",
				message.conversationID, message.at.Format(time.RFC3339), err))
			continue
		}
		recorded = append(recorded, event)
		existing = append(existing, event)
	}
	if *jsonOutput {
		output := interventionOutput{Interventions: recorded}
		if len(problems) > 0 {
			output.Error = strings.Join(problems, "; ")
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(stdout, "recorded %d of the operator's messages sent since %s; %d were recorded already\n",
			len(recorded), since.In(time.Local).Format("2006-01-02 15:04 MST"), skipped)
		for _, problem := range problems {
			fmt.Fprintln(stderr, problem)
		}
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

// loggedPersonMessages is every person's message in every conversation log
// since a moment, oldest first. A log that cannot be read is named and the
// others are read.
func loggedPersonMessages(conversations *runstate.ConversationStore, since time.Time) ([]loggedMessage, []string, error) {
	ids, err := conversations.LoggedConversations()
	if err != nil {
		return nil, nil, err
	}
	records, err := conversations.Recorded()
	if err != nil {
		return nil, nil, err
	}
	roles := make(map[string]domain.AgentRole, len(records))
	for _, record := range records {
		roles[record.ConversationID] = record.Role
	}
	var found []loggedMessage
	var problems []string
	for _, id := range ids {
		events, _, err := conversations.ScanEvents(id)
		if err != nil {
			problems = append(problems, fmt.Sprintf("conversation %s's log could not be read, so none of its messages was recorded: %v", id, err))
			continue
		}
		found = append(found, personMessagesIn(id, roles[id], events, since)...)
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at.Before(found[j].at) })
	return found, problems, nil
}

// personMessagesIn is the person's messages in one log since a moment.
func personMessagesIn(conversationID string, role domain.AgentRole, events []execution.Event, since time.Time) []loggedMessage {
	var found []loggedMessage
	passStarting := false
	for _, event := range events {
		switch event.Type {
		case execution.EventSessionPassStarted:
			passStarting = true
			continue
		case execution.EventOperatorMessage:
		default:
			continue
		}
		opensPass := passStarting
		passStarting = false
		if event.Timestamp.Before(since) {
			continue
		}
		var payload struct {
			Text    string `json:"text"`
			Console bool   `json:"typed_at_console"`
		}
		if len(event.Payload) > 0 && json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		if !payload.Console && (opensPass || harnessComposed(payload.Text)) {
			continue
		}
		found = append(found, loggedMessage{
			conversationID: conversationID,
			role:           role,
			at:             event.Timestamp.UTC(),
			text:           payload.Text,
			console:        payload.Console,
		})
	}
	return found
}

func harnessComposed(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, opening := range harnessOpenings {
		if strings.HasPrefix(trimmed, opening) {
			return true
		}
	}
	return false
}

// alreadyRecorded is a message event about the same conversation at the same
// moment, which is the one the message would be recorded as.
func alreadyRecorded(existing []intervention.Event, message loggedMessage) bool {
	for _, event := range existing {
		if event.Kind != intervention.KindMessage || !strings.HasPrefix(event.Subject, "conversation "+message.conversationID) {
			continue
		}
		gap := event.At.Sub(message.at)
		if gap < 0 {
			gap = -gap
		}
		if gap <= backfillMatchWindow {
			return true
		}
	}
	return false
}

// backfilledMessage is the event a message found in a log is recorded as, in
// the words a message recorded as it was sent uses.
func backfilledMessage(productID domain.ProductID, message loggedMessage, now time.Time) (intervention.Event, error) {
	id, err := intervention.NewID()
	if err != nil {
		return intervention.Event{}, err
	}
	title := "a role"
	if message.role != "" {
		title = "the " + chat.RoleTitle(message.role)
	}
	said := oneline.Fold(message.text, 300)
	if said == "" {
		said = "an empty message"
	}
	via := "conversation " + message.conversationID + ", read back from its log"
	if message.console {
		via = "the console, read back from conversation " + message.conversationID + "'s log"
	}
	return intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            id,
		ProductID:     productID,
		Kind:          intervention.KindMessage,
		At:            message.at,
		Subject:       "conversation " + message.conversationID + " with " + title,
		Said:          "sent " + title + " a message: " + said,
		Via:           via,
		RecordedAt:    now,
	}, nil
}
