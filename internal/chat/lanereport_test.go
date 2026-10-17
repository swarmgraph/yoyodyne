package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func laneReportBlock(payload string) string {
	return laneReportFence + "\n" + payload + "\n```\n"
}

func laneReportPayload(summary string) string {
	return fmt.Sprintf(`{"summary":%q,"remaining":["root-cause work for the stalled checks"],"blockers":[{"what":"prevention work outside the lane","waiting_on":"product-manager","cites":"report-7"}]}`, summary)
}

// programManagerSession opens a program manager's conversation over a lane
// report store, answering with the turns given.
func programManagerSession(t *testing.T, laneReports *runstate.LaneReportStore, answers ...string) (*Session, *fakeBackend, string) {
	t.Helper()
	root := t.TempDir()
	results := make([]backendapi.RunResult, 0, len(answers))
	for _, answer := range answers {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: answer})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "factory"
	options.Store = newTestStore(t, root)
	options.LaneReports = laneReports
	return openTestSession(t, options), provider, root
}

func newChatLaneReportStore(t *testing.T, redact ...string) *runstate.LaneReportStore {
	t.Helper()
	store, err := runstate.NewLaneReportStore(t.TempDir(), "yoyodyne", readmodel.CheckLaneReportMover, redact...)
	if err != nil {
		t.Fatalf("NewLaneReportStore() error = %v", err)
	}
	return store
}

// A well-formed block from a program manager rewrites its report whole and adds
// a stamped version to the history: three turns write three, and the newest is
// the report while all three are the history.
func TestAProgramManagersLaneReportIsRewrittenEachTurnAndKept(t *testing.T) {
	t.Parallel()

	store := newChatLaneReportStore(t)
	session, provider, root := programManagerSession(t, store,
		"First pass.\n\n"+laneReportBlock(laneReportPayload("pass 1")),
		"Second pass.\n\n"+laneReportBlock(laneReportPayload("pass 2")),
		"Third pass.\n\n"+laneReportBlock(laneReportPayload("pass 3")),
		"Nothing new.",
	)
	session.ForPass("factory-watch#9")

	for turn := 1; turn <= 3; turn++ {
		reply, err := session.Send(context.Background(), "How does the lane stand?")
		if err != nil {
			t.Fatalf("Send(%d) error = %v", turn, err)
		}
		if strings.Contains(reply.Text, "yoyodyne-lane-report") {
			t.Errorf("the block reached the operator's prose:\n%s", reply.Text)
		}
		if reply.LaneReport == nil || !reply.LaneReport.Recorded || reply.LaneReport.Version != turn {
			t.Fatalf("Send(%d) lane report = %+v, want version %d recorded", turn, reply.LaneReport, turn)
		}
	}

	current, found, err := store.Current("factory")
	if err != nil || !found {
		t.Fatalf("Current() = %v, %v", found, err)
	}
	if current.Report.Summary != "pass 3" || current.Version != 3 {
		t.Fatalf("the current report is version %d %q, want the third", current.Version, current.Report.Summary)
	}
	history, err := store.History("factory")
	if err != nil || len(history) != 3 {
		t.Fatalf("History() = %d versions, %v; want 3", len(history), err)
	}
	for index, version := range history {
		stamp := version.Stamp
		if stamp.Turn != index+1 || stamp.ConversationID != session.state.ConversationID || stamp.Pass != "factory-watch#9" {
			t.Errorf("history[%d] is stamped %+v, want turn %d of this conversation in its pass", index, stamp, index+1)
		}
	}

	if counted := countEvents(t, root, session); counted[execution.EventLaneReportRecorded] != 3 {
		t.Errorf("lane_report.recorded events = %d, want 3", counted[execution.EventLaneReportRecorded])
	}
	for _, event := range loadTestEvents(t, root, session) {
		if strings.Contains(string(event.Payload), "root-cause work for the stalled checks") {
			t.Errorf("the conversation log copied the report's text: %s", event.Payload)
		}
	}

	if _, err := session.Send(context.Background(), "Anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(provider.requests[3].Prompt, "Your lane report was written as version 3.") {
		t.Errorf("the role was not told what became of its report:\n%s", provider.requests[3].Prompt)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-lane-report") {
		t.Error("the program manager's contract does not say how to write a lane report")
	}
	for _, mover := range readmodel.LaneReportMovers() {
		if !strings.Contains(provider.requests[0].SystemPrompt, fmt.Sprintf("%q", string(mover))) {
			t.Errorf("the contract does not offer the mover %q the read model admits", mover)
		}
	}
}

// A block over 16 KiB, missing a field, or naming a mover outside the
// vocabulary is refused whole: the turn is not failed, the report before it
// stands, the refusal is recorded and said to the role, and a pass that woke
// the turn has it to record.
func TestAMalformedLaneReportIsRefusedWholeAndThePreviousReportStands(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"over the bound":   laneReportPayload(strings.Repeat("x", runstate.MaxLaneReportBytes)),
		"missing blockers": `{"summary":"s","remaining":[]}`,
		"missing summary":  `{"remaining":[],"blockers":[]}`,
		"unknown mover":    `{"summary":"s","remaining":[],"blockers":[{"what":"w","waiting_on":"the-weather","cites":"report-7"}]}`,
		"blocker no cite":  `{"summary":"s","remaining":[],"blockers":[{"what":"w","waiting_on":"operator"}]}`,
		"unknown field":    `{"summary":"s","remaining":[],"blockers":[],"mood":"fine"}`,
		"not json":         `the lane is fine`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newChatLaneReportStore(t)
			session, provider, root := programManagerSession(t, store,
				"First.\n\n"+laneReportBlock(laneReportPayload("the standing report")),
				"Second.\n\n"+laneReportBlock(payload),
				"Noted.",
			)
			if _, err := session.Send(context.Background(), "Report."); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			before, err := os.ReadFile(store.ReportPath("factory"))
			if err != nil {
				t.Fatal(err)
			}

			reply, err := session.Send(context.Background(), "Report again.")
			if err != nil {
				t.Fatalf("Send() error = %v; a refused report does not fail the turn", err)
			}
			if reply.LaneReport == nil || reply.LaneReport.Recorded || reply.LaneReport.Failure == "" {
				t.Fatalf("reply.LaneReport = %+v, want it refused", reply.LaneReport)
			}
			if !strings.Contains(reply.LaneReport.Refusal(), "the report before it stands") {
				t.Errorf("the refusal a pass records = %q", reply.LaneReport.Refusal())
			}
			if strings.Contains(reply.Text, "yoyodyne-lane-report") {
				t.Errorf("the refused block reached the operator's prose:\n%s", reply.Text)
			}
			after, err := os.ReadFile(store.ReportPath("factory"))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Errorf("a refused report changed the standing one")
			}
			if history, err := store.History("factory"); err != nil || len(history) != 1 {
				t.Errorf("History() = %d versions, %v; want the one that landed", len(history), err)
			}
			if counted := countEvents(t, root, session); counted[execution.EventLaneReportRefused] != 1 {
				t.Errorf("lane_report.refused events = %d, want 1", counted[execution.EventLaneReportRefused])
			}
			if _, err := session.Send(context.Background(), "And?"); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if !strings.Contains(provider.requests[2].Prompt, "Your lane report was refused whole") {
				t.Errorf("the role was not told its report was refused:\n%s", provider.requests[2].Prompt)
			}
		})
	}
}

// The report is redacted with the values every durable record is redacted with,
// before it reaches the disk.
func TestALaneReportIsRedactedWithTheDurableRecordsValues(t *testing.T) {
	t.Parallel()

	store := newChatLaneReportStore(t, "sk-live-secret")
	session, _, _ := programManagerSession(t, store,
		"Reported.\n\n"+laneReportBlock(laneReportPayload("the token sk-live-secret leaked into a check log")))
	reply, err := session.Send(context.Background(), "Report.")
	if err != nil || reply.LaneReport == nil || !reply.LaneReport.Recorded {
		t.Fatalf("Send() = %+v, %v", reply.LaneReport, err)
	}
	stored, err := os.ReadFile(store.ReportPath("factory"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "sk-live-secret") || !strings.Contains(string(stored), "[REDACTED]") {
		t.Errorf("the stored report was not redacted:\n%s", stored)
	}
}

// Only the program manager holds lane-report.write. The block in any other
// role's reply is refused with nothing written, and no other contract describes
// it.
func TestALaneReportFromAnyOtherRoleIsRefusedWithNothingWritten(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		if role == domain.RoleProgramManager {
			continue
		}
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			store := newChatLaneReportStore(t)
			provider := &fakeBackend{results: []backendapi.RunResult{{
				SessionID: "session-1",
				FinalText: "Here is where things stand.\n\n" + laneReportBlock(laneReportPayload("not mine to write")),
			}}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.LaneReports = store
			session := openTestSession(t, options)

			_, err := session.Send(context.Background(), "Report.")
			var refused *AuthorityError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), "lane report") {
				t.Fatalf("Send() error = %v, want the lane report refused for want of authority", err)
			}
			if _, found, err := store.Current(string(role)); found || err != nil {
				t.Errorf("Current() = %v, %v; a refused block wrote a report", found, err)
			}
			if strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-lane-report") {
				t.Errorf("the %s contract describes the lane report block", role)
			}
		})
	}
}

func TestALaneReportBlockIsReadStrictly(t *testing.T) {
	t.Parallel()

	prose, content, found, err := extractLaneReport("prose\n\n" + laneReportBlock(`{"summary":"s","remaining":[],"blockers":[]}`))
	if err != nil || !found || prose != "prose" || content == nil || content.Summary != "s" {
		t.Fatalf("extractLaneReport() = %q, %+v, %v, %v", prose, content, found, err)
	}
	if content.Remaining == nil || content.Blockers == nil {
		t.Errorf("empty lists were read as missing: %+v", content)
	}
	if _, _, found, err := extractLaneReport("just prose"); found || err != nil {
		t.Errorf("a reply with no block reads as carrying one: %v, %v", found, err)
	}
	for name, reply := range map[string]string{
		"two blocks":                        laneReportBlock(`{"summary":"s","remaining":[],"blockers":[]}`) + laneReportBlock(`{"summary":"s","remaining":[],"blockers":[]}`),
		"unclosed":                          laneReportFence + "\n{}",
		"trailing content":                  laneReportBlock(`{"summary":"s","remaining":[],"blockers":[]} {}`),
		"a nobody mover":                    laneReportBlock(`{"summary":"s","remaining":[],"blockers":[{"what":"w","waiting_on":"nobody","cites":"report-7"}]}`),
		"the operator as a blocker's mover": laneReportBlock(`{"summary":"s","remaining":[],"blockers":[{"what":"w","waiting_on":"operator","cites":"report-7"}]}`),
	} {
		if _, _, found, err := extractLaneReport(reply); !found || err == nil {
			t.Errorf("%s: extractLaneReport() = found %v, err %v; want it carried and refused", name, found, err)
		}
	}
}

// A Blocked on a human entry that breaks the contract — no reason from the
// closed list, no time it began — is refused on its own, and the report and the
// other entries are read.
func TestABadBlockedOnAHumanEntryIsRefusedAloneAndTheReportIsRead(t *testing.T) {
	t.Parallel()

	_, content, found, err := extractLaneReport(laneReportBlock(`{"blocked_on_a_human":[` +
		`{"item":"yoyodyne-ifd.9","what":"renew the forge token","reason":"credential","cites":"report-7","since":"2026-10-12T16:40:00Z"},` +
		`{"what":"approve the design","reason":"approval","cites":"report-8","since":"2026-10-12T16:40:00Z"},` +
		`{"what":"answer the question","reason":"fundamental-intent","cites":"report-9","since":"yesterday"}],` +
		`"summary":"s","remaining":[],"blockers":[]}`))
	if err != nil || !found || content == nil {
		t.Fatalf("extractLaneReport() = %+v, %v, %v; want the report read", content, found, err)
	}
	if len(content.BlockedOnAHuman) != 1 || content.BlockedOnAHuman[0].Item != "yoyodyne-ifd.9" {
		t.Fatalf("kept %+v, want only the credential entry", content.BlockedOnAHuman)
	}
	refused := strings.Join(content.Refused, "\n")
	if len(content.Refused) != 2 || !strings.Contains(refused, `reason "approval"`) || !strings.Contains(refused, `since "yesterday"`) {
		t.Fatalf("refused %q, want the approval and the entry with no time", refused)
	}
	outcome := LaneReportOutcome{Recorded: true, Version: 2, RefusedEntries: content.Refused}
	if refusal := outcome.Refusal(); !strings.Contains(refusal, "was written without 2 Blocked on a human entries") {
		t.Fatalf("the pass record would say %q", refusal)
	}
}
