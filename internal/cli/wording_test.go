package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

func TestTheReportsCommandFlagsWordsInADigestAndItsHandling(t *testing.T) {
	t.Parallel()
	digest := report.Report{ID: "report-one", Role: "program-manager", Message: "digest: the posture changed"}
	handled := map[string]report.Handling{digest.ID: {Reason: "change its cadence"}}
	var out strings.Builder
	writeReports(&out, console.NewTheme(func(string) string { return "" }, nil), []report.Report{digest}, handled, nil, nil, nil, readmodel.ReadTextTerms("../.."))
	for _, term := range []string{"posture", "cadence"} {
		if !strings.Contains(out.String(), `[wording: "`+term+`" was replaced`) {
			t.Errorf("digest = %q, want %s flagged", out.String(), term)
		}
	}
	if digest.Message != "digest: the posture changed" || handled[digest.ID].Reason != "change its cadence" {
		t.Fatal("rendering rewrote a record")
	}
}

func TestAOneShotConversationFlagsADigestAsSoonAsItIsFiled(t *testing.T) {
	t.Parallel()
	const message = "digest: the posture changed"
	answer := "A digest was filed.\n\n" + report.Fence + "\n" + `{"reports":[{"severity":"note","message":"` + message + `"}]}` + "\n```\n"
	for _, jsonOutput := range []bool{false, true} {
		root := t.TempDir()
		store, err := runstate.NewConversationStore(root, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		collected, err := runstate.NewReportStore(root, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		session, err := chat.Open(chat.Options{
			Role: domain.RoleProductManager, Agent: "product-manager",
			Backend: &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: answer}}, Store: store, Reports: collected,
			Model: "opus", Provider: domain.BackendClaudeCode, AccountAlias: config.DefaultAccountAlias,
			Repository: "../..", ProductID: "yoyodyne", RepositoryID: "yoyodyne",
			Briefing: chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
		})
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr strings.Builder
		if code := runChatMessage(context.Background(), session, domain.RoleProductManager, "what changed?", jsonOutput, &stdout, &stderr); code != 0 {
			t.Fatalf("runChatMessage() = %d, stderr = %q", code, stderr.String())
		}
		if jsonOutput {
			var output chatOutput
			if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
				t.Fatal(err)
			}
			if len(output.Reports) != 1 || output.Reports[0].Message != message {
				t.Fatalf("JSON reports = %+v, want the author's unchanged message", output.Reports)
			}
		} else if text := stdout.String(); !strings.Contains(text, message) || strings.Count(text, `[wording: "posture" was replaced; write tool access`) != 1 {
			t.Fatalf("immediate report output = %q, want the report and its replacement flag", text)
		}
		stored, err := collected.List()
		if err != nil || len(stored) != 1 || stored[0].Message != message {
			t.Fatalf("stored reports = %+v, %v, want the author's unchanged message", stored, err)
		}
	}
}

func TestAFailedOneShotTurnStillFlagsItsCollectedDigest(t *testing.T) {
	t.Parallel()
	digest := report.Report{ID: "report-one", Role: domain.RoleProductManager, Message: "digest: the posture changed"}
	reply := chat.Reply{Reports: []report.Report{digest}}
	var stdout, stderr strings.Builder
	if code := reportChatFailure(&stdout, &stderr, false, domain.RoleProductManager, &reply, errors.New("the turn failed after filing"), readmodel.ReadTextTerms("../..").Render); code != 1 {
		t.Fatalf("reportChatFailure() = %d, want the failed turn preserved", code)
	}
	if text := stdout.String(); !strings.Contains(text, digest.Message) || !strings.Contains(text, `[wording: "posture" was replaced; write tool access`) {
		t.Fatalf("failed turn's report = %q, want its replacement flag", text)
	}
	if reply.Reports[0].Message != digest.Message {
		t.Fatal("rendering changed the reply's collected report")
	}
}

func TestTheSweepListingFlagsTheSummaryQuestionsAndFindings(t *testing.T) {
	t.Parallel()
	account := &sweep.Result{Status: sweep.StatusComplete, Summary: "the posture changed", Questions: []string{"change the cadence?"}, Findings: []sweep.Finding{{Issue: "an idle-bound", Detail: "a stall continuation"}}}
	pass := recordedSweep("look", time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), account, "")
	text := renderSweeps([]runstate.Sweep{pass}, nil, 0, nil, readmodel.ReadTextTerms("../.."))
	for _, term := range []string{"posture", "cadence", "idle bound", "stall continuation"} {
		if !strings.Contains(text, `[wording: "`+term+`" was replaced`) {
			t.Errorf("pass = %q, want %s flagged", text, term)
		}
	}
	if account.Summary != "the posture changed" {
		t.Fatal("rendering rewrote the pass account")
	}
}
