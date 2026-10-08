package chat

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// replyingBackend writes its answer to whoever is watching before it returns
// it, which is what a provider does: the assistant's message is reported before
// the terminal result the turn is built from. It answers with the same text
// either way, so what the conversation records cannot depend on whether anybody
// was watching.
type replyingBackend struct {
	// fragments are the messages the provider reports as it writes, and reply is
	// what its terminal result says. They are separate so a test can be sure the
	// record comes from the result rather than from what was shown.
	fragments []string
	reply     string
	// costUSD is what the provider charges for each invocation.
	costUSD float64
	// fail ends the invocation after the fragments have been shown, which is a
	// reply cut off part way.
	fail error
	// refusals is how many of the first invocations the provider declines for
	// want of capacity, and refusedFragments is what one of them manages to write
	// before it does — the preamble that arrived while the rest never did. It is a
	// refusal rather than a failure: the harness waits it out and asks again, so
	// what it leaves on screen is the start of an answer about to be replaced
	// rather than the end of one.
	refusals         int
	refusedFragments []string
	limit            *backendapi.UsageLimit
	sinks            int
	requests         []backendapi.RunRequest
}

func (b *replyingBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	b.requests = append(b.requests, request)
	sequence := request.LastSequence
	written := b.fragments
	if b.refusals > 0 {
		written = b.refusedFragments
	}
	for _, fragment := range written {
		sequence++
		event, err := execution.NewEvent(request.RunID, sequence, fixedClock{}.Now(), execution.EventAgentMessage,
			"provider.claude-code", map[string]any{"text": fragment})
		if err != nil {
			return backendapi.RunResult{}, err
		}
		if request.EventSink != nil {
			if err := request.EventSink(event); err != nil {
				return backendapi.RunResult{}, err
			}
		}
		if request.ReplySink != nil {
			b.sinks++
			request.ReplySink(fragment)
		}
	}
	if b.refusals > 0 {
		b.refusals--
		return backendapi.RunResult{IsError: true, StopReason: "usage_limit", UsageLimit: b.limit, LastEvent: sequence}, nil
	}
	if b.fail != nil {
		return backendapi.RunResult{LastEvent: sequence}, b.fail
	}
	// A provider that names a cost has reported one. The flag beside it is what
	// separates an invocation priced at nothing from one nobody was told the
	// price of, so a fixture that charges nothing leaves it unset rather than
	// claiming the provider called the turn free.
	return backendapi.RunResult{
		SessionID:    "session-1",
		FinalText:    b.reply,
		CostUSD:      b.costUSD,
		CostReported: b.costUSD > 0,
		LastEvent:    sequence,
	}, nil
}

// dressed is a conversation held over a stream that is dressed as a colour
// terminal would be, which is how everything gated on the dressing is testable
// without a terminal to drive.
func dressed(in io.Reader, out io.Writer) dressedConsole {
	return dressedConsole{Console: testConsole(in, out), theme: dressedTheme()}
}

// TestTheReplyIsReadAsItIsWrittenAndRecordedUnchanged is the whole bargain:
// the operator reads the answer while it is being written, and the conversation
// that gets recorded is the one that would have been recorded without anybody
// watching. The same turn is run both ways and the records are compared, so
// "identical" is an assertion rather than a claim about the code path.
func TestTheReplyIsReadAsItIsWrittenAndRecordedUnchanged(t *testing.T) {
	t.Parallel()

	reply := "Two goals, then.\n\nWhich of them matters first?"
	transcripts := map[string]string{}
	records := map[string][]execution.Event{}
	replies := map[string]string{}
	for _, held := range []struct {
		name  string
		open  func(io.Reader, io.Writer) console.Console
		shown bool
	}{
		{"streamed", func(in io.Reader, out io.Writer) console.Console { return dressed(in, out) }, true},
		{"held", testConsole, false},
	} {
		root := t.TempDir()
		options := testOptions(t, &replyingBackend{fragments: []string{reply}, reply: reply})
		options.Store = newTestStore(t, root)
		session := openTestSession(t, options)

		var out strings.Builder
		screen := held.open(strings.NewReader("what is missing from the brief?\n/exit\n"), &out)
		if err := session.Converse(context.Background(), screen); err != nil {
			t.Fatalf("%s: Converse() error = %v", held.name, err)
		}
		transcripts[held.name] = escapes.ReplaceAllString(out.String(), "")
		records[held.name] = loadTestEvents(t, root, session)
		replies[held.name] = reply
		if session.shownReply != held.shown {
			t.Fatalf("%s: shownReply = %v, want %v", held.name, session.shownReply, held.shown)
		}
	}

	// The answer reads the same either way, blank lines and all, and it is read
	// once: a reply shown as it formed is not written again when it is finished.
	for name, transcript := range transcripts {
		if !strings.Contains(transcript, "\nproduct-manager> "+reply+"\n\n") {
			t.Fatalf("%s: the answer did not read as it does when it is held: %q", name, transcript)
		}
		if strings.Count(transcript, "Which of them matters first?") != 1 {
			t.Fatalf("%s: the answer was shown %d times: %q", name,
				strings.Count(transcript, "Which of them matters first?"), transcript)
		}
	}

	// The record is the same one either way, event for event.
	streamed, held := records["streamed"], records["held"]
	if len(streamed) != len(held) {
		t.Fatalf("recorded %d event(s) streamed and %d held", len(streamed), len(held))
	}
	for index := range streamed {
		if streamed[index].Type != held[index].Type || string(streamed[index].Payload) != string(held[index].Payload) {
			t.Fatalf("event %d differs: streamed %s %s, held %s %s", index,
				streamed[index].Type, streamed[index].Payload, held[index].Type, held[index].Payload)
		}
	}
}

// TestNothingIsStreamedWhereTheConsoleMayNotBeDressed keeps every part of this
// behind the one check the colours are behind. A pipe, a file, NO_COLOR, and a
// terminal that says it is dumb all reach the same console theme, and none of
// them is a place to write escapes or to make what a transcript holds depend on
// when the provider said something.
func TestNothingIsStreamedWhereTheConsoleMayNotBeDressed(t *testing.T) {
	t.Parallel()

	provider := &replyingBackend{fragments: []string{"Two goals, then."}, reply: "Two goals, then."}
	session := openTestSession(t, testOptions(t, provider))

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if session.shownReply {
		t.Fatal("a stream was shown a reply as it formed")
	}
	if strings.Count(out.String(), "Two goals, then.") != 1 {
		t.Fatalf("the reply was not written exactly once: %q", out.String())
	}
	// The backend is still offered somewhere to put the reply. What decides
	// whether anything is shown is the console, not the provider.
	if provider.sinks == 0 {
		t.Fatal("the provider was never offered the reply sink")
	}
}

// TestABlockWrittenForTheHarnessIsNotShownAsProse keeps the protocol out of the
// answer. Proposals, tracker actions, concerns, and reports are each reported
// in their own way once the turn is over, so the source of one arriving in the
// middle of a reply is the one thing the operator must not be shown.
func TestABlockWrittenForTheHarnessIsNotShownAsProse(t *testing.T) {
	t.Parallel()

	answer := "Here is what I would do.\n\n" +
		"```yoyodyne-proposal\n" +
		`{"items":[{"kind":"feature","title":"Stream the reply","description":"d","rationale":"r","goal":"a conversation that answers"}]}` +
		"\n```\n\n" +
		"That is the lot."
	options := testOptions(t, &replyingBackend{fragments: []string{answer}, reply: answer})
	session := openTestSession(t, options)

	var out strings.Builder
	// The input ends where the proposal is put, which leaves it undecided: this
	// is about what the operator was shown, not about what they did with it.
	if err := session.Converse(context.Background(), dressed(strings.NewReader("what next?\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := escapes.ReplaceAllString(out.String(), "")
	for _, prose := range []string{"Here is what I would do.", "That is the lot."} {
		if !strings.Contains(transcript, prose) {
			t.Fatalf("the prose around the block was lost: %q", transcript)
		}
	}
	if strings.Contains(transcript, "yoyodyne-proposal") || strings.Contains(transcript, `"rationale"`) {
		t.Fatalf("the block the harness reads was shown as prose: %q", transcript)
	}
	// What the block was for still reached the operator, by the path a proposal
	// takes: hidden from the prose is not dropped.
	if !strings.Contains(transcript, "Stream the reply") {
		t.Fatalf("the proposal was never put to the operator: %q", transcript)
	}
	if len(session.Proposals()) != 1 {
		t.Fatalf("proposals = %d, want 1", len(session.Proposals()))
	}
}

// TestAnInterruptedStreamSaysItWasInterrupted is the failure this must not
// have. Prose that simply stops reads as a product manager that had nothing
// more to say, and an operator who acts on half an answer believing it whole is
// worse off than one who was shown nothing.
func TestAnInterruptedStreamSaysItWasInterrupted(t *testing.T) {
	t.Parallel()

	provider := &replyingBackend{
		fragments: []string{"I would start with the first goal, because"},
		fail:      errors.New("the provider closed the stream"),
	}
	session := openTestSession(t, testOptions(t, provider))

	var out strings.Builder
	err := session.Converse(context.Background(), dressed(strings.NewReader("what next?\n"), &out))
	if err == nil {
		t.Fatal("Converse() error = nil, want the failed turn")
	}
	transcript := escapes.ReplaceAllString(out.String(), "")
	if !strings.Contains(transcript, "I would start with the first goal, because") {
		t.Fatalf("what did arrive was not shown: %q", transcript)
	}
	if !strings.Contains(transcript, replyCutOff) {
		t.Fatalf("half an answer was left looking whole: %q", transcript)
	}
}

// A turn refused for want of capacity is asked again, so what it had written by
// then is neither a whole answer nor the start of the one that replaces it. It
// is closed off and said to be so, and the reissued reply opens underneath it —
// otherwise the operator reads one paragraph running into another, written by
// two attempts, as a single answer.
func TestAStreamInterruptedByAUsageLimitIsClosedOffBeforeTheReissue(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	answer := "I would start with the first goal, because it is the one the brief turns on."
	provider := &replyingBackend{
		fragments:        []string{answer},
		reply:            answer,
		refusals:         1,
		refusedFragments: []string{"I would start with the first goal, because"},
		limit:            &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: clock.now.Add(45 * time.Minute)},
	}
	session := openTestSession(t, waitingOptions(testOptions(t, provider), clock))

	var out strings.Builder
	if err := session.Converse(context.Background(), dressed(strings.NewReader("what next?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := escapes.ReplaceAllString(out.String(), "")
	if !strings.Contains(transcript, replyInterrupted) {
		t.Fatalf("the interrupted attempt was left looking whole: %q", transcript)
	}
	if !strings.Contains(transcript, "it is the one the brief turns on.") {
		t.Fatalf("the reissued answer was not shown: %q", transcript)
	}
	// The turn was not cut off: it completed, after waiting, and saying otherwise
	// would teach the operator to discount the warning that means it did not.
	if strings.Contains(transcript, replyCutOff) {
		t.Fatalf("a turn that waited and completed was reported as cut off: %q", transcript)
	}
	// Each attempt opens its own answer, so what the operator reads is one whole
	// reply rather than two run together.
	if strings.Count(transcript, strings.TrimSpace(replyOpening)) != 2 {
		t.Fatalf("the reissued reply did not open an answer of its own: %q", transcript)
	}
}

// TestAFailureAfterAWholeReplyIsNotReportedAsAnInterruption keeps the warning
// meaningful. A block the harness could not read belongs to an answer that
// arrived complete, so saying it was cut off would teach the operator to
// discount the warning that matters.
func TestAFailureAfterAWholeReplyIsNotReportedAsAnInterruption(t *testing.T) {
	t.Parallel()

	answer := "Here it is.\n\n```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\"}]}\n```\n"
	options := testOptions(t, &replyingBackend{fragments: []string{answer}, reply: answer})
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), dressed(strings.NewReader("what next?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := escapes.ReplaceAllString(out.String(), "")
	if !strings.Contains(transcript, "Here it is.") {
		t.Fatalf("the answer was not shown: %q", transcript)
	}
	if strings.Contains(transcript, replyCutOff) {
		t.Fatalf("a complete answer was reported as cut off: %q", transcript)
	}
	if !strings.Contains(transcript, "Nothing was proposed as far as the harness is concerned") {
		t.Fatalf("the unreadable block was not reported: %q", transcript)
	}
}

// TestTheCostOfATurnRestsUnderTheConversation covers what an operator watching
// their spend needs: the number is there between turns without being asked for,
// and it is not written into the conversation, where it would become a running
// log of itself.
func TestTheCostOfATurnRestsUnderTheConversation(t *testing.T) {
	t.Parallel()

	provider := &replyingBackend{fragments: []string{"Two goals."}, reply: "Two goals.", costUSD: 0.0125}
	session := openTestSession(t, testOptions(t, provider))

	var out strings.Builder
	screen := &statusConsole{Console: dressed(strings.NewReader("what next?\nand then?\n/exit\n"), &out)}
	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if len(screen.statuses) != 2 {
		t.Fatalf("statuses = %#v, want one for each turn", screen.statuses)
	}
	if screen.statuses[0] != "this turn $0.0125 · this session $0.0125" {
		t.Fatalf("first turn status = %q", screen.statuses[0])
	}
	// The session total accumulates while the per-turn cost describes only the
	// turn, which is the difference the operator is watching for.
	if screen.statuses[1] != "this turn $0.0125 · this session $0.0250" {
		t.Fatalf("second turn status = %q", screen.statuses[1])
	}
	if strings.Contains(out.String(), "this session") {
		t.Fatalf("the spend was written into the conversation: %q", out.String())
	}
}

// TestSpendIsNotShownWhereTheConsoleMayNotBeDressedOrWhereNothingWasCharged
// keeps the line honest at both ends: a stream has nowhere to rest one, and a
// confident zero from a provider that charged nothing it could report would be
// an answer rather than the absence of one.
func TestSpendIsNotShownWhereTheConsoleMayNotBeDressedOrWhereNothingWasCharged(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		name    string
		cost    float64
		dressed bool
	}{
		{"a stream has nowhere to rest it", 0.0125, false},
		{"a provider that charged nothing it reported", 0, true},
	} {
		t.Run(held.name, func(t *testing.T) {
			t.Parallel()

			provider := &replyingBackend{fragments: []string{"Two goals."}, reply: "Two goals.", costUSD: held.cost}
			session := openTestSession(t, testOptions(t, provider))
			var out strings.Builder
			input := strings.NewReader("what next?\n/exit\n")
			var inner console.Console = testConsole(input, &out)
			if held.dressed {
				inner = dressed(input, &out)
			}
			screen := &statusConsole{Console: inner}
			if err := session.Converse(context.Background(), screen); err != nil {
				t.Fatalf("Converse() error = %v", err)
			}
			if len(screen.statuses) != 0 {
				t.Fatalf("statuses = %#v, want none", screen.statuses)
			}
		})
	}
}

// statusConsole records what was left resting under the conversation, so a line
// that is replaced rather than written is still assertable.
type statusConsole struct {
	console.Console
	statuses []string
}

func (c *statusConsole) Status(text string) { c.statuses = append(c.statuses, text) }
