package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unreadReviews makes the first unread reviews of provider write a tool call
// into their reply as text before answering, which is what a reviewer started
// without tools did on October 6: the call runs nothing, and the verdict that
// follows was reached from the evidence alone.
func unreadReviews(t *testing.T, provider *orchestratortest.Backend, unread int) {
	t.Helper()
	respond := provider.Respond
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleReviewer || reviews >= unread {
			return respond(request)
		}
		reviews++
		event, err := execution.NewEvent(request.RunID, request.LastSequence+1, time.Now(), execution.EventAgentMessage, "fake.reviewer",
			execution.ReplyPayload("<invoke name=\"Bash\"><parameter name=\"command\">git grep -n Save -- internal/</parameter></invoke>"))
		if err != nil {
			return backend.RunResult{}, err
		}
		if err := request.EventSink(event); err != nil {
			return backend.RunResult{}, err
		}
		request.LastSequence = event.Sequence
		return respond(request)
	}
}

// A review whose every read came back empty is not counted as a judged review:
// it is asked once more in the reviewer's own session, told why, and the change
// integrates on the second review, which read.
func TestPipelineAsksAgainWhenEveryReviewerReadCameBackEmpty(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	unreadReviews(t, provider, 1)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the unread review asked again", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("the re-asked review did not integrate: %#v, closed = %t", outcome, tracker.Closed)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want the unread review asked again once", len(reviews))
	}
	if reviews[1].SessionID != provider.ReviewerSession || !strings.Contains(reviews[1].Prompt, "came back empty") {
		t.Fatalf("re-ask ran in session %q saying %q, want the reviewer's own session told its reads came back empty", reviews[1].SessionID, reviews[1].Prompt)
	}
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 || outcome.RepairAttempts != 0 {
		t.Fatalf("developer invocations = %d, repair attempts = %d; the re-ask costs neither", runs, outcome.RepairAttempts)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	unread, completed := 0, 0
	for _, event := range events {
		switch event.Type {
		case execution.EventReviewUnread:
			unread++
			if !strings.Contains(string(event.Payload), `"reads_written_as_text":1`) || !strings.Contains(string(event.Payload), `"unjudged_decision":"approve"`) {
				t.Fatalf("review.unread payload = %s, want what was tried and the verdict it did not count", event.Payload)
			}
		case execution.EventReviewCompleted:
			completed++
		}
	}
	if unread != 1 || completed != 1 {
		t.Fatalf("review.unread = %d, review.completed = %d; want the unread review recorded as unread and only the second as judged", unread, completed)
	}
}

// Two reviews in a row whose reads came back empty end the run on that cause,
// for the development manager, and nothing integrates on either verdict.
func TestPipelineStopsAfterASecondReviewWhoseReadsCameBackEmpty(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	unreadReviews(t, provider, 2)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopReviewAccount)
	if err == nil || !strings.Contains(err.Error(), "came back empty") {
		t.Fatalf("Run() error = %v, want the run stopped on reads that came back empty", err)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a change no reviewer read around was integrated: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Fatalf("reviews = %d, want exactly one re-ask", reviews)
	}
	if !strings.Contains(tracker.Notes, "not an independently judged review") {
		t.Fatalf("item notes = %q, want the cause named for whoever takes the stopped run", tracker.Notes)
	}
}
