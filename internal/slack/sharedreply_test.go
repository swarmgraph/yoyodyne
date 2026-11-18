package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/teamrecords"
)

// sharing is a SharedDirectives that answers as told, recording what it was
// offered.
type sharing struct {
	answer  func(offered directive.Directive) (directive.Directive, teamrecords.Sharing, error)
	offered []directive.Directive
}

func (s *sharing) ShareDirective(_ context.Context, offered directive.Directive) (directive.Directive, teamrecords.Sharing, error) {
	s.offered = append(s.offered, offered)
	return s.answer(offered)
}

// A directive from a reply is named by the message, so the same reply is the
// same directive wherever it is recorded.
func TestAReplysDirectiveIsNamedByTheMessage(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", "1750000001.000200"))

	if got, want := onlyDirective(t, directives).ID, directive.ForSlackMessage("C1", "1750000001.000200"); got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
}

// A reply delivered again after a restart is acknowledged as the directive
// already recorded: no second directive and no refusal.
func TestAReplyDeliveredAgainAfterARestartNamesTheDirectiveAlreadyRecorded(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	redelivered := reply(testOperator, "prefer the smaller change here", "1750000001.000200")
	sink.steering.handle(context.Background(), redelivered)
	// A restarted process remembers no replies.
	sink.steering.acted = map[string]bool{}
	sink.steering.handle(context.Background(), redelivered)

	onlyDirective(t, directives)
	if len(posts.requests) != 2 {
		t.Fatalf("answers = %#v, want an acknowledgment for each delivery", posts.requests)
	}
	for _, answer := range posts.requests {
		if !strings.Contains(answer.Text, "Recorded") || strings.Contains(answer.Text, "Nothing was recorded") {
			t.Fatalf("answer = %q, want the directive acknowledged rather than refused", answer.Text)
		}
	}
}

// The acknowledgment names the directive that stands, which is another
// factory's where it recorded the same reply first.
func TestTheAcknowledgmentNamesTheDirectiveThatStands(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.shared = &sharing{answer: func(offered directive.Directive) (directive.Directive, teamrecords.Sharing, error) {
		stands := offered
		stands.Text = "the other factory's reading of the same reply"
		return stands, teamrecords.SharingAlreadyRecorded, nil
	}}
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", "1750000001.000200"))

	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "the other factory's reading of the same reply") || strings.Contains(answer.Text, "prefer the smaller change here") {
		t.Fatalf("answer = %q, want the directive that stands", answer.Text)
	}
}

// A directive that could not be sent is in force here and is told so, in place
// of the ordinary acknowledgment.
func TestADirectiveNotYetSharedIsToldSoInsteadOfAcknowledged(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.shared = &sharing{answer: func(offered directive.Directive) (directive.Directive, teamrecords.Sharing, error) {
		return offered, teamrecords.SharingNotShared, nil
	}}
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", "1750000001.000200"))

	answer := onlyPost(t, posts)
	if answer.ThreadTS != testThreadTS || answer.Text != tagged(testOperator, keptHere) {
		t.Fatalf("answer = %#v, want the not-yet-shared sentence in the thread", answer)
	}
	if strings.Contains(answer.Text, "Recorded") {
		t.Fatalf("answer = %q, want no ordinary acknowledgment", answer.Text)
	}
}

// A decision is named by the message that answered, and a decision not yet
// shared says so.
func TestADecisionIsNamedByTheMessageAndSaysWhenItIsNotYetShared(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	message := inboundMessage{user: testOperator, text: "1", ts: "1750000001.000200", threadTS: testDMThreadTS, channel: testDMChannel}
	sink.steering.decide(context.Background(), testAsked(), message, time.Now())
	if got, want := onlyDirective(t, directives).ID, directive.ForSlackMessage(testDMChannel, message.ts); got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}

	sink.steering.shared = &sharing{answer: func(offered directive.Directive) (directive.Directive, teamrecords.Sharing, error) {
		return offered, teamrecords.SharingNotShared, nil
	}}
	message.ts = "1750000002.000200"
	if answer := sink.steering.decide(context.Background(), testAsked(), message, time.Now()); answer != keptHere {
		t.Fatalf("answer = %q, want the not-yet-shared sentence", answer)
	}
}
