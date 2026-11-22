package review

import (
	"context"
	"errors"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type failingInvocationLog struct{ roles []domain.AgentRole }

func (l *failingInvocationLog) Recorder(_ string, role domain.AgentRole, _ string, _ func() time.Time) backendapi.InvocationRecorder {
	l.roles = append(l.roles, role)
	return failingRecorder{}
}

type failingRecorder struct{}

func (failingRecorder) RecordInvocation(backendapi.SentInvocation) error {
	return errors.New("the state directory is full")
}

// recordingFirst is a backend that does what every adapter does with a
// recorder: it records before it starts anything, and starts nothing if it cannot.
type recordingFirst struct {
	inner  *fakeBackend
	called bool
}

func (r *recordingFirst) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	if err := backendapi.RecordInvocation(request, backendapi.SentInvocation{Messages: []backendapi.SentMessage{{Role: backendapi.SentUser, Parts: []backendapi.SentPart{{Name: "first message", Text: request.Prompt}}}}}); err != nil {
		return backendapi.RunResult{}, err
	}
	r.called = true
	return r.inner.Run(ctx, request)
}

// A review whose record cannot be written is not started, and is not mistaken
// for a resume that failed and asked again in a fresh session: the run stops on
// the harness's own store.
func TestAReviewThatCannotBeRecordedIsNotStartedAndIsNotAskedAgain(t *testing.T) {
	t.Parallel()
	for _, session := range []string{"", "review-session"} {
		provider := &recordingFirst{inner: &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"fine"}`}}
		log := &failingInvocationLog{}
		request := newRequest(nil)
		request.Changes = gitworktree.ChangeDiff{Patch: "diff --git a/runner.go b/runner.go\n+added\n"}
		if session != "" {
			request.Reask = &Reask{SessionID: session, Problem: "not a verdict"}
		}
		_, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel, Invocations: log}).Review(context.Background(), request)
		var stopped runstate.StopError
		if !errors.As(err, &stopped) || stopped.Class != runstate.StopRecording {
			t.Fatalf("session %q: error = %v, want a stop on the harness's own record", session, err)
		}
		var resume ResumeRefusedError
		if errors.As(err, &resume) {
			t.Fatalf("session %q: an unrecorded review was read as a refused resume: %v", session, err)
		}
		if provider.called || provider.inner.calls != 0 {
			t.Fatalf("session %q: the provider was started although nothing was recorded", session)
		}
		if len(log.roles) != 1 || log.roles[0] != domain.RoleReviewer {
			t.Fatalf("session %q: recorders asked for = %v", session, log.roles)
		}
	}
}

// A branch review belongs to no run, so nothing is kept for it and it is never
// refused for want of a record.
func TestABranchReviewIsNeverRecordedAsARunsInvocation(t *testing.T) {
	t.Parallel()
	provider := &recordingFirst{inner: &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"fine"}`}}
	log := &failingInvocationLog{}
	request := newBranchRequest(nil)
	if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel, Invocations: log}).Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(log.roles) != 0 || !provider.called {
		t.Fatalf("recorders asked for = %v, provider called = %v", log.roles, provider.called)
	}
}
