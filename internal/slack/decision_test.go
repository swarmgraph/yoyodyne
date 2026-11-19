package slack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The state the asks below are about, and the direct message thread the operator
// answers in.
const (
	testStoppedMark = "intake:2026-08-30T02:02:00Z"
	testStopped     = "intake is held, and the operator placed it — overnight"
	testDMChannel   = "D0OPERATOR"
	testDMThreadTS  = "1750000000.000900"
)

var testOptions = []string{
	"release intake so admitted work can be chosen again",
	"keep intake held; let what is running finish and choose nothing new",
}

// The grammar is one rule and one refusal. A number the ask offered is the
// option it names; everything else is a decision in the operator's own words,
// which is the reading that cannot silently throw away what somebody said.
func TestParseDecisionReadsTheOptionAReplyNamedAndKeepsTheWords(t *testing.T) {
	t.Parallel()

	for _, said := range []struct {
		reply  string
		option int
	}{
		{reply: "1", option: 1},
		{reply: "2", option: 2},
		{reply: "2.", option: 2},
		{reply: "2) keep it held for now", option: 2},
		{reply: "option 1", option: 1},
		{reply: "Option 2 — for the reason you said", option: 2},
		// Anything the list did not anticipate is still a decision, recorded as it
		// was typed rather than forced onto the nearest number.
		{reply: "leave it until the morning and we will look together", option: 0},
		{reply: "neither of those", option: 0},
	} {
		chosen, err := parseDecision(said.reply, len(testOptions))
		if err != nil {
			t.Fatalf("parseDecision(%q) error = %v, want it read as a decision", said.reply, err)
		}
		if chosen.option != said.option {
			t.Fatalf("parseDecision(%q).option = %d, want %d", said.reply, chosen.option, said.option)
		}
		// What the operator typed is kept whole whichever they meant, because it is
		// what gets written into a record somebody reads weeks later.
		if chosen.said != strings.TrimSpace(said.reply) {
			t.Fatalf("parseDecision(%q).said = %q, want the operator's own words", said.reply, chosen.said)
		}
	}
}

// A number that names no option is refused rather than recorded as prose.
// Somebody who typed 5 was answering the list and believes they have; recording
// the digit verbatim would leave them thinking they had decided something.
func TestParseDecisionRefusesANumberThatNamesNoOption(t *testing.T) {
	t.Parallel()

	for _, said := range []string{"5", "0", "-1", "option 9", "option"} {
		chosen, err := parseDecision(said, len(testOptions))
		if err == nil {
			t.Fatalf("parseDecision(%q) = %+v, want a number naming no option refused", said, chosen)
		}
		if !strings.Contains(err.Error(), "own words") {
			t.Fatalf("parseDecision(%q) error = %q, want it to say what can be typed instead", said, err)
		}
	}
	if _, err := parseDecision("   ", len(testOptions)); err == nil {
		t.Fatal("parseDecision(blank) recorded something, want a reply that said nothing refused")
	}
}

// The decision is recorded where every run reads it: one operational directive
// in the same durable record a terminal writes to, unscoped because what was
// asked was about the whole line rather than any one item. The option chosen is
// named in it, with the words the operator typed.
func TestADecisionRecordsTheChosenOptionWhereEveryRunReadsIt(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	answer := sink.steering.decide(context.Background(), testAsked(), inboundMessage{
		user:     testOperator,
		text:     "2 — let the overnight finish first",
		ts:       "1750000001.000200",
		threadTS: testDMThreadTS,
		channel:  testDMChannel,
	}, time.Now())

	recorded := onlyDirective(t, directives)
	if recorded.Kind != directive.KindOperational || recorded.Pauses() {
		t.Fatalf("recorded = %+v, want an operational directive that pauses nothing", recorded)
	}
	if len(recorded.Scope) != 0 || !recorded.Affects("yoyodyne-ifd.1") {
		t.Fatalf("recorded = %+v, want a decision about the line to reach every item", recorded)
	}
	if recorded.ReceivedBy != domain.RoleProductManager {
		t.Fatalf("received by %q, want the product manager for a decision that named no role", recorded.ReceivedBy)
	}
	// All three, because a record holding any one of them alone is unreadable
	// afterwards: what was asked, which option was taken, and what was said.
	for _, wanted := range []string{testStopped, "option 2", testOptions[1], "let the overnight finish first"} {
		if !strings.Contains(recorded.Text, wanted) {
			t.Fatalf("recorded text = %q, want it to carry %q", recorded.Text, wanted)
		}
	}
	// The answer says which option was taken and the sentence it stood for, and
	// names no identifier — the same rule a channel acknowledgment follows, since
	// a slug is the one thing in it the person would have to go and resolve.
	if !strings.Contains(answer, "option 2") || !strings.Contains(answer, testOptions[1]) {
		t.Fatalf("answer = %q, want it to say which option was taken and what it stood for", answer)
	}
	if strings.Contains(answer, recorded.ID) {
		t.Fatalf("answer = %q, want no directive identifier posted; `yoyo directive list` is where it is read", answer)
	}
}

// A decision in the operator's own words is recorded exactly as one that took an
// option, because the options are shortcuts rather than the whole of what may be
// answered.
func TestADecisionInTheOperatorsOwnWordsIsRecordedAsTheyTypedIt(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	sink.steering.decide(context.Background(), testAsked(), inboundMessage{
		user:     testOperator,
		text:     "neither; I am about to change what is admitted",
		ts:       "1750000001.000200",
		threadTS: testDMThreadTS,
		channel:  testDMChannel,
	}, time.Now())

	recorded := onlyDirective(t, directives)
	if !strings.Contains(recorded.Text, "I am about to change what is admitted") {
		t.Fatalf("recorded text = %q, want the operator's own words", recorded.Text)
	}
	if strings.Contains(recorded.Text, "chose option") {
		t.Fatalf("recorded text = %q, want no option claimed for a reply that named none", recorded.Text)
	}
}

// Authority defaults closed here exactly as it does in a thread, and it is
// checked before the reply is read at all: an ask somebody forwarded is not a
// grant, and the refusal names what is missing rather than going silent.
func TestADecisionFromSomebodyWithoutDirectWorkRecordsNothingAndSaysSo(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	answer := sink.steering.decide(context.Background(), testAsked(), inboundMessage{
		user:     testStranger,
		text:     "1",
		ts:       "1750000001.000200",
		threadTS: testDMThreadTS,
		channel:  testDMChannel,
	}, time.Now())

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded %+v, want nothing recorded for somebody the project granted nothing", recorded)
	}
	if !strings.Contains(answer, "direct-work") {
		t.Fatalf("answer = %q, want a refusal naming the grant it is missing", answer)
	}
}

// The whole of the inbound half: a reply in the thread the ask was made in is
// the decision. Nothing else is — no command, no button — so this drives the
// connection exactly as Slack does and expects the record to move.
func TestAReplyInTheAsksThreadIsTheDecision(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "thread_ts": testDMThreadTS, "channel": testDMChannel,
	}))

	recorded := onlyDirective(t, directives)
	if !strings.Contains(recorded.Text, testOptions[0]) {
		t.Fatalf("recorded text = %q, want the option the reply chose", recorded.Text)
	}
	// The answer goes back into the thread that asked, addressed to whoever
	// answered: a decision acknowledged at the top of a channel would tell
	// everybody except the person who made it.
	answer := onlyPost(t, posts)
	if answer.Channel != testDMChannel || answer.ThreadTS != testDMThreadTS {
		t.Fatalf("answer = %#v, want it in the direct message thread the ask was made in", answer)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %q, want it to tag whoever decided", answer.Text)
	}
}

// Slack redelivers an envelope whose acknowledgment did not reach it, and a
// redelivered decision recorded twice would be two directives for one thing the
// operator said.
func TestARedeliveredDecisionIsRecordedOnce(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	envelope := envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "thread_ts": testDMThreadTS, "channel": testDMChannel,
	})
	sink.steering.handle(context.Background(), envelope)
	sink.steering.handle(context.Background(), envelope)

	onlyDirective(t, directives)
	onlyPost(t, posts)
}

// Answering in the conversation instead of in the ask's thread is what a phone
// notification invites, so it is the likeliest reply this surface gets. Nothing
// can be recorded from it — which ask it answers is exactly what the thread was
// carrying — but the one thing that must not happen is silence: a decision
// somebody believes they made, lost without a word, is the failure this whole
// tier exists to end.
func TestADecisionTypedOutsideTheAsksThreadIsAnsweredRatherThanDropped(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	standingOn(t, sink, testStoppedMark)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "channel": testDMChannel,
	}))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded %+v, want nothing recorded from a reply that named no ask", recorded)
	}
	answer := onlyPost(t, posts)
	if answer.Channel != testDMChannel || answer.ThreadTS != "" {
		t.Fatalf("answer = %#v, want it where they typed rather than in a thread they are not looking at", answer)
	}
	// What was not recorded first, then what to do about it, naming the ask so the
	// instruction is followable in a conversation holding more than one.
	for _, wanted := range []string{"Nothing was recorded", "inside the thread", "is above", testStopped} {
		if !strings.Contains(answer.Text, wanted) {
			t.Fatalf("answer = %q, want it to carry %q", answer.Text, wanted)
		}
	}
}

// The pointer is only given while the ask it names is still the live one. Once
// that state has cleared — the line is moving, or a different state is standing
// — sending somebody to its thread would send them to a decision about a line
// that is already moving, so what is said instead is that it has cleared and
// that nothing is waiting on them here.
func TestAMisplacedReplyAfterTheAskHasClearedSaysSoRatherThanPointingAtIt(t *testing.T) {
	t.Parallel()

	for name, standing := range map[string]string{
		"the line is moving":           "",
		"a later state is standing on": "hold:2026-08-30T09:00:00Z",
	} {
		sink, _, posts := newDecidingSink(t, testOperator)
		standingOn(t, sink, standing)
		sink.steering.handle(context.Background(), envelopeFor(map[string]any{
			"type": "message", "user": testOperator, "text": "1",
			"ts": "1750000001.000200", "channel": testDMChannel,
		}))

		answer := onlyPost(t, posts)
		if strings.Contains(answer.Text, "is above") {
			t.Fatalf("%s: answer = %q, want no pointer at a thread whose state has cleared", name, answer.Text)
		}
		for _, wanted := range []string{"Nothing was recorded", testStopped, "has since cleared", "nothing is waiting on a decision"} {
			if !strings.Contains(answer.Text, wanted) {
				t.Fatalf("%s: answer = %q, want it to carry %q", name, answer.Text, wanted)
			}
		}
	}
}

// Whether the ask still stands is read from the heartbeat's cursor, and a cursor
// that cannot be read says nothing either way. "It has since cleared" said of a
// line that is still stopped sends somebody away from the one decision it waits
// on, so an unreadable cursor is said as that, and the ask is still named.
func TestAMisplacedReplyWhenTheStandingCannotBeReadClaimsNeither(t *testing.T) {
	t.Parallel()

	sink, _, posts := newDecidingSink(t, testOperator)
	if err := os.WriteFile(filepath.Join(sink.store.Root(), "cursors.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "channel": testDMChannel,
	}))

	answer := onlyPost(t, posts)
	for _, unwanted := range []string{"has since cleared", "nothing is waiting on a decision", "is above"} {
		if strings.Contains(answer.Text, unwanted) {
			t.Fatalf("answer = %q, want no claim %q about a standing that could not be read", answer.Text, unwanted)
		}
	}
	for _, wanted := range []string{"Nothing was recorded", testStopped, "could not be read"} {
		if !strings.Contains(answer.Text, wanted) {
			t.Fatalf("answer = %q, want it to carry %q", answer.Text, wanted)
		}
	}
}

// A message that @-mentions the app is answered wherever the sink can see it,
// and reading direct messages made direct messages somewhere it can. So a
// mention in a direct message goes through the mention door exactly as one at
// the top of the channel does — in a conversation the sink never asked in, and
// in one it did, where it is a question rather than a misplaced decision — and
// the answer lands in that conversation rather than in the reporting channel.
func TestAMentionInADirectMessageIsAnsweredThere(t *testing.T) {
	t.Parallel()

	for name, channel := range map[string]string{
		"a conversation the sink never asked in": "D0SOMEBODYELSE",
		"a conversation holding an ask":          testDMChannel,
	} {
		const askedTS = "1750000001.000200"
		sink, directives, posts := newDecidingSink(t, testOperator)
		standingOn(t, sink, testStoppedMark)
		sink.steering.handle(context.Background(), envelopeFor(map[string]any{
			"type": "message", "user": testOperator, "text": "<@" + testApp + "> status",
			"ts": askedTS, "channel": channel,
		}))

		answer := onlyPost(t, posts)
		if answer.Channel != channel || answer.ThreadTS != askedTS {
			t.Fatalf("%s: answer = %#v, want it in the direct message, hanging from the message that asked", name, answer)
		}
		if strings.Contains(answer.Text, "Nothing was recorded") {
			t.Fatalf("%s: answer = %q, want the mention answered rather than read as a misplaced decision", name, answer.Text)
		}
		for _, line := range fourLines {
			if !strings.Contains(answer.Text, line) {
				t.Fatalf("%s: answer = %q, want the four lines and so the %q line", name, answer.Text, line)
			}
		}
		if recorded, err := directives.List(); err != nil {
			t.Fatalf("List() error = %v", err)
		} else if len(recorded) != 0 {
			t.Fatalf("%s: recorded = %+v, want a question to record nothing", name, recorded)
		}
	}
}

// Somebody the project does not know who addresses the app in a direct message
// is told so there, and the refusal's mark goes on their message there: a mark
// put on by timestamp in the reporting channel lands on nothing.
func TestAStrangersMentionInADirectMessageIsRefusedThere(t *testing.T) {
	t.Parallel()

	const (
		stranger = "U0STRANGER"
		askedTS  = "1750000001.000200"
	)
	sink, _, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": stranger, "text": "<@" + testApp + "> status",
		"ts": askedTS, "channel": "D0STRANGER",
	}))

	answer := onlyPost(t, posts)
	if answer.Channel != "D0STRANGER" {
		t.Fatalf("answer = %#v, want the refusal in the direct message it was addressed in", answer)
	}
	if len(posts.marks) == 0 {
		t.Fatalf("marks = %#v, want the refused message marked", posts.marks)
	}
	for index, marked := range posts.marks {
		if posts.markedIn[index] != "D0STRANGER" || marked.ts != askedTS {
			t.Fatalf("marks = %#v in %v, want every mark on the stranger's own message in the direct message", posts.marks, posts.markedIn)
		}
	}
}

// A message in a conversation this sink has never asked anything in is somebody
// starting their own, and it is left alone: there is no ask to point them at.
func TestAMessageInAConversationTheSinkNeverAskedInIsLeftAlone(t *testing.T) {
	t.Parallel()

	sink, _, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "hello",
		"ts": "1750000001.000200", "channel": "D0SOMEBODYELSE",
	}))

	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing said in a conversation this sink never asked in", posts.requests)
	}
}

// A message in the reporting channel that is not in a thread is still somebody
// talking in the channel. Letting the un-threaded direct message through must
// not have made the app answer conversations between people.
func TestAMessageInTheChannelOutsideAThreadIsStillLeftAlone(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "ambiguous: anything",
		"ts": "1750000001.000200", "channel": "C1",
	}))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 || len(posts.requests) != 0 {
		t.Fatalf("recorded %+v and said %#v, want somebody talking in the channel left alone", recorded, posts.requests)
	}
}

// The map is the one record here that would otherwise grow forever, so it is
// bounded — and the bound never drops the state somebody is being asked about
// now, because dropping that is asking them the same question twice.
func TestTheOldestAsksAreForgottenAndTheStandingOneNeverIs(t *testing.T) {
	t.Parallel()

	decisions := DecisionMap{Decisions: map[string]Decision{}}
	// One standing state, asked of two people, and a long history behind it. The
	// standing pair is deliberately the oldest in the map: age is what decides
	// what goes, so if being the standing state did not protect them they would be
	// the first two dropped.
	for _, member := range []string{testOperator, testStranger} {
		decisions.Record(Decision{
			Mark: testStoppedMark, Member: member,
			Channel: "D" + member, ThreadTS: "1760000000.000000",
			AskedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
	}
	for round := 0; round < maxRememberedAsks+10; round++ {
		decisions.Record(Decision{
			Mark: fmt.Sprintf("idle:%d", round), Member: testOperator,
			Channel: "D" + testOperator, ThreadTS: fmt.Sprintf("17500000%02d.000000", round),
			AskedAt: time.Date(2026, 8, round/24+1, round%24, 0, 0, 0, time.UTC),
		})
	}

	if !decisions.Forget(testStoppedMark, maxRememberedAsks) {
		t.Fatal("Forget() dropped nothing, want a map past its bound cut back")
	}
	if len(decisions.Decisions) != maxRememberedAsks {
		t.Fatalf("kept %d asks, want the map held to %d", len(decisions.Decisions), maxRememberedAsks)
	}
	for _, member := range []string{testOperator, testStranger} {
		if !decisions.AskedOf(testStoppedMark, member) {
			t.Fatalf("%s is no longer recorded as asked about the standing state, want it never forgotten", member)
		}
	}
	// The oldest of the rest are what went.
	if _, found := decisions.Lookup("D"+testOperator, "1750000000.000000"); found {
		t.Fatal("the oldest ask survived, want the oldest dropped first")
	}
	// A map inside its bound is left alone, so nothing is rewritten for nothing.
	if decisions.Forget(testStoppedMark, maxRememberedAsks) {
		t.Fatal("Forget() dropped something from a map already inside its bound")
	}
}

// A reply into an ask whose state has since cleared is still a decision, so the
// thread stays answerable rather than being pruned the moment the line moves.
func TestAnAskWhoseStateHasClearedIsStillAnswerable(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	// The line moved on: a different state is standing, and it is asked about.
	asking := &Ask{Mark: "hold:2026-08-30T09:00:00Z", Stopped: "all harness activity is held by the operator", Ready: 7, Options: testOptions}
	sink.ask(context.Background(), asking)

	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "thread_ts": testDMThreadTS, "channel": testDMChannel,
	}))

	recorded := onlyDirective(t, directives)
	if !strings.Contains(recorded.Text, testStopped) {
		t.Fatalf("recorded text = %q, want the earlier ask still answerable after its state cleared", recorded.Text)
	}
}

// A direct message thread this sink asked nothing in is somebody having their
// own conversation with the app. Answering it would be talking over them, and
// there is no ask to read a number against.
func TestAReplyInADirectMessageThreadTheSinkNeverAskedInIsLeftAlone(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "thread_ts": "1749999999.000000", "channel": "D0SOMEBODYELSE",
	}))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 || len(posts.requests) != 0 {
		t.Fatalf("recorded %+v and said %#v, want a thread this sink did not ask in left alone", recorded, posts.requests)
	}
}

// A reply in a work item's thread is still a directive against that item. The
// two halves share a connection and must not have become one behavior: the
// decision tier is a second receiver rather than a replacement.
func TestAReplyInTheChannelIsStillSteeredAgainstItsWorkItem(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if len(recorded.Scope) != 1 || recorded.Scope[0] != testItem {
		t.Fatalf("scope = %v, want the item whose thread it was said in", recorded.Scope)
	}
}

// The outbound half: a line that has stopped reaches every operator, as a brief
// top line carrying the ask with the context and the numbered options threaded
// under it.
func TestAStoppedLineIsPutToEveryOperatorAsADirectMessage(t *testing.T) {
	t.Parallel()

	asking := &Ask{
		Mark:    testStoppedMark,
		Stopped: testStopped,
		Since:   time.Date(2026, 8, 30, 2, 2, 0, 0, time.UTC),
		Ready:   7,
		Options: testOptions,
	}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking}, testOperator, testStranger)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	// Every operator separately: a decision addressed to a room is one each of
	// them can assume somebody else is making.
	if len(posts.opened) != 2 || posts.opened[0] != testOperator || posts.opened[1] != testStranger {
		t.Fatalf("opened = %v, want a direct message with each operator", posts.opened)
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %#v, want a top line and a threaded ask for each of the two", posts.requests)
	}
	top, threaded := posts.requests[0], posts.requests[1]
	if top.Channel != "D"+testOperator || top.ThreadTS != "" {
		t.Fatalf("top line = %#v, want it opening the direct message with the operator", top)
	}
	// Brief, and carrying the ask: it is what a lock screen shows.
	if !strings.Contains(top.Text, "waiting on you") || !strings.Contains(top.Text, testStopped) {
		t.Fatalf("top line = %q, want it to say what stopped the line and that it waits on them", top.Text)
	}
	if threaded.Channel != top.Channel || threaded.ThreadTS != posts.timestamps[0] {
		t.Fatalf("threaded ask = %#v, want it under the top line rather than beside it", threaded)
	}
	for _, wanted := range []string{"1. " + testOptions[0], "2. " + testOptions[1], "2026-08-30T02:02:00Z", "Ready to pull: 7"} {
		if !strings.Contains(threaded.Text, wanted) {
			t.Fatalf("threaded ask = %q, want it to carry %q", threaded.Text, wanted)
		}
	}
}

// One message per person per state. The channel says it again every hour while
// it stands; a direct message repeated hourly is what gets an app muted, and a
// muted app is silence exactly where this had to be heard.
func TestOneStoppedStateIsPutToAnOperatorOnce(t *testing.T) {
	t.Parallel()

	asking := &Ask{Mark: testStoppedMark, Stopped: testStopped, Ready: 7, Options: testOptions}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking}, testOperator)
	for round := 0; round < 3; round++ {
		if err := sink.pass(context.Background()); err != nil {
			t.Fatalf("pass() error = %v", err)
		}
	}

	if len(posts.opened) != 1 || len(posts.requests) != 2 {
		t.Fatalf("opened %v and posted %#v, want one operator asked once however many passes", posts.opened, posts.requests)
	}
	// A different state is a different question, so it is asked afresh rather
	// than swallowed by the first.
	asking.Mark = "hold:2026-08-30T09:00:00Z"
	asking.Stopped = "all harness activity is held by the operator"
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.opened) != 2 {
		t.Fatalf("opened = %v, want a state that replaced the last one asked on its own", posts.opened)
	}
}

// A product that has granted nobody is asked nothing, which is what every
// workspace gets until somebody names themselves. Shipping this changes nothing
// for them.
func TestAProjectThatGrantedNobodyIsAskedNothing(t *testing.T) {
	t.Parallel()

	asking := &Ask{Mark: testStoppedMark, Stopped: testStopped, Ready: 7, Options: testOptions}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking})
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.opened) != 0 || len(posts.requests) != 0 {
		t.Fatalf("opened %v and posted %#v, want nobody asked where nobody holds direct-work", posts.opened, posts.requests)
	}
}

// Asking is an observation like everything else this sink does, so a workspace
// that will not carry a direct message costs that message and nothing else: the
// pass finishes, the channel is unaffected, and nothing is recorded as asked.
func TestAWorkspaceThatRefusesAnAskDoesNotFailThePass(t *testing.T) {
	t.Parallel()

	asking := &Ask{Mark: testStoppedMark, Stopped: testStopped, Ready: 7, Options: testOptions}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking}, testOperator)
	posts.refuseDirect = "cannot_dm_bot"
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	asked, err := sink.store.LoadDecisions()
	if err != nil {
		t.Fatalf("LoadDecisions() error = %v", err)
	}
	if asked.AskedOf(testStoppedMark, testOperator) {
		t.Fatal("the operator is recorded as asked, want a refused message to be asked again rather than remembered")
	}
}

// newDecidingSink is a sink that has already asked one operator to decide about
// a stopped line, which is the state every reply above arrives into: a reply is
// correlated through the direct message thread the ask was made in.
func newDecidingSink(t *testing.T, operators ...string) (*Sink, *runstate.DirectiveStore, *recordedPosts) {
	t.Helper()
	sink, directives, posts := newSteeringSink(t, operators...)
	decisions, err := sink.store.LoadDecisions()
	if err != nil {
		t.Fatalf("LoadDecisions() error = %v", err)
	}
	decisions.Record(testAsked())
	if err := sink.store.SaveDecisions(decisions); err != nil {
		t.Fatalf("SaveDecisions() error = %v", err)
	}
	return sink, directives, posts
}

// standingOn puts the heartbeat's cursor on one state, which is what the
// delivery pass records while it repeats a stopped line and what a misplaced
// reply is answered against. An empty mark is a line that is moving.
func standingOn(t *testing.T, sink *Sink, mark string) {
	t.Helper()
	cursors, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if cursors.Streams == nil {
		cursors.Streams = map[string]Cursor{}
	}
	cursors.Streams[heartbeatStream] = Cursor{Standing: mark, Said: time.Now().UTC()}
	if err := sink.store.SaveCursors(cursors); err != nil {
		t.Fatalf("SaveCursors() error = %v", err)
	}
}

// testAsked is the ask as it was put to the operator and remembered.
func testAsked() Decision {
	return Decision{
		Mark:     testStoppedMark,
		Member:   testOperator,
		Channel:  testDMChannel,
		ThreadTS: testDMThreadTS,
		Stopped:  testStopped,
		Options:  testOptions,
		AskedAt:  time.Date(2026, 8, 30, 3, 2, 0, 0, time.UTC),
	}
}
