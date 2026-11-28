package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

type fakeEpicFeedback struct {
	lines []string
	err   error
}

func (f fakeEpicFeedback) FeedbackAfterDelivery() ([]string, error) { return f.lines, f.err }

const waitingFeedback = "Epic yoyodyne-ep: feedback after delivery, on merged pull request #7 of yoyodyne-ep.1 (comment comment-31): teammate said: rename the flag; it is yoyodyne-later, which starts nothing"

// Feedback left after an epic was delivered is put in front of every turn of the
// Lead Product Manager, the operator's own messages included and not only the
// first, ahead of the report pile, and a recurring pass's first turn carries it
// too. No other role is shown it.
func TestFeedbackAfterDeliveryIsInFrontOfEveryTurnOfTheLeadProductManager(t *testing.T) {
	t.Parallel()
	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}, {FinalText: "noted", SessionID: "session-1"}}}
	options := testOptions(t, provider)
	options.EpicFeedback = fakeEpicFeedback{lines: []string{waitingFeedback}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for _, message := range []string{"what needs deciding?", "and now?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
	for at, request := range provider.requests {
		if !strings.Contains(request.Prompt, epicFeedbackHeading) || !strings.Contains(request.Prompt, "- "+waitingFeedback) {
			t.Fatalf("turn %d does not carry the feedback:\n%s", at, request.Prompt)
		}
		if strings.Index(request.Prompt, epicFeedbackHeading) > strings.Index(request.Prompt, "# Operator message") {
			t.Fatalf("turn %d carries the feedback after the operator's message", at)
		}
	}
	session.freshPass = true
	if prompt := session.turnPrompt("the recurring pass", nil); !strings.Contains(prompt, waitingFeedback) {
		t.Fatalf("a recurring pass's first turn does not carry the feedback:\n%s", prompt)
	}

	other := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-2"}}})
	other.Role, other.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	other.EpicFeedback = fakeEpicFeedback{lines: []string{waitingFeedback}}
	manager, err := Open(other)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if prompt := manager.turnPrompt("hello", nil); strings.Contains(prompt, epicFeedbackHeading) {
		t.Fatalf("the development manager was shown the feedback:\n%s", prompt)
	}
}

// Feedback that cannot be read is said, and none waiting says nothing.
func TestFeedbackAfterDeliveryThatCannotBeReadIsSaid(t *testing.T) {
	t.Parallel()
	options := testOptions(t, &fakeBackend{})
	options.EpicFeedback = fakeEpicFeedback{err: errors.New("the epics' progress is not whole")}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if said := session.renderEpicFeedback(); !strings.Contains(said, "could not be read, so this turn does not say: the epics' progress is not whole") {
		t.Fatalf("said %q", said)
	}
	session.options.EpicFeedback = fakeEpicFeedback{}
	if said := session.renderEpicFeedback(); said != "" {
		t.Fatalf("said %q with nothing waiting", said)
	}
}
