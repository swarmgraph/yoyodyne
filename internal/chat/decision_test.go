package chat

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// testCards is a batch of proposals as the operator is asked about them.
func testCards(count int) []card {
	cards := make([]card, 0, count)
	for i := 1; i <= count; i++ {
		cards = append(cards, card{
			number: i,
			proposal: PendingProposal{
				ID:             "2." + string(rune('0'+i)),
				ConversationID: "conversation-1",
				Turn:           2,
				Proposal: Proposal{
					Title:       "Something to do",
					Description: "What the work is.",
					Rationale:   "Why it follows.",
					Goal:        "Run development nearly autonomously.",
				},
			},
		})
	}
	return cards
}

// An approval has to name what it creates. That is the fail-closed rule the
// batch prompt inherits rather than relaxes: the only approval that names
// nothing is the one answering a single question, where there is exactly one
// item it could mean.
func TestOnlyAnApprovalThatNamesItsItemsCreatesAnything(t *testing.T) {
	t.Parallel()

	t.Run("a bare yes approves the one proposal it is asked about", func(t *testing.T) {
		t.Parallel()

		for _, answer := range []string{"y", "Y", "yes", "YES", " yes ", "approve"} {
			decisions, err := readDecisions(answer, testCards(1))
			if err != nil {
				t.Fatalf("readDecisions(%q) error = %v", answer, err)
			}
			if want := []decision{{proposalID: "2.1", approve: true}}; !reflect.DeepEqual(decisions, want) {
				t.Fatalf("readDecisions(%q) = %#v, want %#v", answer, decisions, want)
			}
		}
	})

	t.Run("a bare yes approves the last card left, whatever number it carries", func(t *testing.T) {
		t.Parallel()

		// Card numbers are a proposal's place in its turn and never renumber, so
		// the last of three undecided proposals is still card 3. The prompt for
		// it says y creates it, and y has to mean that card rather than card 1.
		last := testCards(3)[2:]
		for _, answer := range []string{"y", "yes", "approve"} {
			decisions, err := readDecisions(answer, last)
			if err != nil {
				t.Fatalf("readDecisions(%q) error = %v", answer, err)
			}
			if want := []decision{{proposalID: "2.3", approve: true}}; !reflect.DeepEqual(decisions, want) {
				t.Fatalf("readDecisions(%q) = %#v, want %#v", answer, decisions, want)
			}
		}
	})

	t.Run("a bare yes to several proposals names none of them and creates none", func(t *testing.T) {
		t.Parallel()

		for _, answer := range []string{"y", "yes", "approve", "approve all"} {
			decisions, err := readDecisions(answer, testCards(3))
			if err == nil {
				t.Fatalf("readDecisions(%q) = %#v, want a refusal", answer, decisions)
			}
			if errors.Is(err, errNotADecision) {
				t.Fatalf("readDecisions(%q) declined instead of asking which items were meant", answer)
			}
			if len(decisions) != 0 {
				t.Fatalf("readDecisions(%q) = %#v, want nothing decided", answer, decisions)
			}
		}
	})

	t.Run("an approval naming something that is not there decides nothing at all", func(t *testing.T) {
		t.Parallel()

		// The second clause is perfectly good. It is not carried out either: an
		// answer the harness cannot read whole is one it acts on no part of.
		decisions, err := readDecisions("approve 9 and decline 1 no", testCards(3))
		if err == nil || len(decisions) != 0 {
			t.Fatalf("readDecisions() = %#v, %v, want nothing decided", decisions, err)
		}
		if !strings.Contains(err.Error(), "9 is not one of the proposals") {
			t.Fatalf("readDecisions() error = %v", err)
		}
	})
}

// A refusal tells the operator what to type instead, so what it names has to be
// on the table: a batch decided down to its last two proposals is showing cards
// 4 and 5, and "say approve 1,3" there is an instruction to type something the
// harness refuses.
func TestARefusalOnlyNamesProposalsThatAreThere(t *testing.T) {
	t.Parallel()

	remaining := testCards(5)[3:]
	for _, answer := range []string{"y", "approve all"} {
		_, err := readDecisions(answer, remaining)
		if err == nil {
			t.Fatalf("readDecisions(%q) was accepted, want a refusal", answer)
		}
		if !strings.Contains(err.Error(), "approve 4,5") {
			t.Fatalf("readDecisions(%q) error = %v, want it to name the cards on the table", answer, err)
		}
	}
	// One card left is one number to name.
	if _, err := readDecisions("approve all", testCards(3)[2:]); err == nil || !strings.Contains(err.Error(), "approve 3") {
		t.Fatalf("readDecisions() error = %v, want it to name the one card on the table", err)
	}
}

func TestOneAnswerDecidesSeveralProposals(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		answer string
		want   []decision
	}{
		{
			name:   "approvals and a decline in one line",
			answer: "approve 1,3 and decline 2 not this quarter",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.2", reason: "not this quarter"},
				{proposalID: "2.3", approve: true},
			},
		},
		{
			name:   "selectors separated however they were typed",
			answer: "approve 1, 3",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.3", approve: true},
			},
		},
		{
			name:   "selectors joined by a word",
			answer: "approve 1 and 2",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.2", approve: true},
			},
		},
		{
			name:   "the proposals named by their own identifiers",
			answer: "approve 2.2",
			want:   []decision{{proposalID: "2.2", approve: true}},
		},
		{
			name:   "everything declined at once, with the reason kept",
			answer: "decline all we are not doing any of this",
			want: []decision{
				{proposalID: "2.1", reason: "we are not doing any of this"},
				{proposalID: "2.2", reason: "we are not doing any of this"},
				{proposalID: "2.3", reason: "we are not doing any of this"},
			},
		},
		{
			// The words somebody declines work in start with a number often
			// enough to matter, and reading one as a second proposal would decide
			// something they never named.
			name:   "a decline reason that begins with a number is a reason",
			answer: "decline 2 3 weeks out",
			want:   []decision{{proposalID: "2.2", reason: "3 weeks out"}},
		},
		{
			name:   "a decline still names several proposals when they are separated",
			answer: "decline 1,3 too speculative",
			want: []decision{
				{proposalID: "2.1", reason: "too speculative"},
				{proposalID: "2.3", reason: "too speculative"},
			},
		},
		{
			name:   "a decline names several joined by a word",
			answer: "decline 1 and 3 too speculative",
			want: []decision{
				{proposalID: "2.1", reason: "too speculative"},
				{proposalID: "2.3", reason: "too speculative"},
			},
		},
		{
			// The decisions come back in the order the operator was shown them,
			// whatever order they were written in.
			name:   "decisions ordered by the cards rather than the clauses",
			answer: "approve 3,1",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.3", approve: true},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decisions, err := readDecisions(test.answer, testCards(3))
			if err != nil {
				t.Fatalf("readDecisions(%q) error = %v", test.answer, err)
			}
			if !reflect.DeepEqual(decisions, test.want) {
				t.Fatalf("readDecisions(%q) = %#v, want %#v", test.answer, decisions, test.want)
			}
		})
	}
}

// A decline that names nothing decides nothing where several proposals are
// waiting: it has not said which, and declining them all on its account would
// decide work the operator never named. Where one is waiting it names that one,
// and keeps the whole answer as the reason.
func TestADeclineNamingNothingDeclinesOnlyTheOneWaiting(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"n", "no thanks", "approve 2 and decline too speculative"} {
		decisions, err := readDecisions(answer, testCards(3))
		if err == nil || len(decisions) != 0 {
			t.Fatalf("readDecisions(%q) = %#v, %v, want nothing decided", answer, decisions, err)
		}
		if !strings.Contains(err.Error(), "nothing is declined that you did not name") {
			t.Fatalf("readDecisions(%q) error = %v", answer, err)
		}
	}
	decisions, err := readDecisions("no thanks", testCards(1))
	if err != nil {
		t.Fatalf("readDecisions() error = %v", err)
	}
	if want := []decision{{proposalID: "2.1", reason: "no thanks"}}; !reflect.DeepEqual(decisions, want) {
		t.Fatalf("readDecisions() = %#v, want %#v", decisions, want)
	}
}

// An answer that names no proposal and begins with no decision word is not a
// decision at all. A message reading it passes it on as speech.
func TestAnAnswerThatDecidesNothingIsNotADecision(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"", "hmm", "not this quarter", "aprove 1", "maybe the first one"} {
		if _, err := readDecisions(answer, testCards(3)); !errors.Is(err, errNotADecision) {
			t.Fatalf("readDecisions(%q) error = %v, want it read as no decision at all", answer, err)
		}
	}
}

// A decline's reason is the operator's own words and runs to the end of the
// line, so an approval written after one is never carried out. Nothing is
// created by accident; the proposal it named is simply put again.
func TestADeclineReasonRunsToTheEndOfTheAnswer(t *testing.T) {
	t.Parallel()

	decisions, err := readDecisions("decline 2 no goal and approve 1", testCards(3))
	if err != nil {
		t.Fatalf("readDecisions() error = %v", err)
	}
	want := []decision{{proposalID: "2.2", reason: "no goal and approve 1"}}
	if !reflect.DeepEqual(decisions, want) {
		t.Fatalf("readDecisions() = %#v, want %#v", decisions, want)
	}
}

func TestAnAnswerThatContradictsItselfDecidesNothing(t *testing.T) {
	t.Parallel()

	decisions, err := readDecisions("approve 1 and decline 1 on second thoughts", testCards(3))
	if err == nil || len(decisions) != 0 {
		t.Fatalf("readDecisions() = %#v, %v, want nothing decided", decisions, err)
	}
	if !strings.Contains(err.Error(), "decided twice") {
		t.Fatalf("readDecisions() error = %v", err)
	}
}

// A card is presentation. The frame makes the boundary between two proposals
// findable, and everything that carries meaning survives its removal: the same
// card drawn where nothing may be dressed is the heading and the body it always
// was.
func TestAProposalCardCarriesNoMeaningInItsFrame(t *testing.T) {
	t.Parallel()

	entry := testCards(1)[0]
	entry.proposal.Proposal.Parent = "yoyodyne-ifd.1"
	entry.proposal.Proposal.Dependencies = []string{"yoyodyne-ifd.2"}
	entry.proposal.Proposal.Kind = domain.WorkItemKindBug

	plain := entry.Render(console.Theme{})
	want := strings.Join([]string{
		"1 · proposal 2.1 · Something to do",
		"    What the work is.",
		"    why: Why it follows.",
		"    goal: Run development nearly autonomously.",
		"    relevant goals: none recorded",
		"    kind: bug",
		"    parent: yoyodyne-ifd.1",
		"    depends on: yoyodyne-ifd.2",
		"",
	}, "\n")
	if plain != want {
		t.Fatalf("undressed card =\n%q\nwant\n%q", plain, want)
	}

	theme := console.NewTheme(func(name string) string {
		if name == "TERM" {
			return "xterm-256color"
		}
		return ""
	}, func() int { return 60 })
	framed := entry.Render(theme)
	if !strings.Contains(framed, "╭─ 1 · proposal 2.1 · Something to do ") {
		t.Fatalf("framed card =\n%s", framed)
	}
	// Every line of the body is inside the frame, and the frame is the only
	// thing the plain rendering does not have.
	for _, line := range entry.proposal.body() {
		if !strings.Contains(framed, "│ "+line) {
			t.Fatalf("framed card =\n%s\nwant it to carry %q", framed, line)
		}
	}
	if !strings.Contains(framed, "\n╰") {
		t.Fatalf("framed card =\n%s\nwant it closed", framed)
	}
}

// twoProposalReply is a turn that proposes two work items.
func twoProposalReply() backendapi.RunResult {
	return backendapi.RunResult{
		SessionID: "session-1",
		FinalText: proposalReply(
			"Two things.",
			`{"kind":"feature","title":"Pause on a usage limit","description":"Wait and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}`,
			`{"kind":"feature","title":"Add a retry budget","description":"Bound repair attempts.","rationale":"You asked for a stopping rule.","goal":"Run development nearly autonomously."}`,
		),
	}
}

// Two waiting proposals are two questions, asked in order, each naming the
// proposal and what deciding it unblocks. One answer decides one of them.
func TestTwoWaitingProposalsAreAskedSeparately(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{twoProposalReply()}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("what next?\ny\nn not this quarter\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if len(tracker.created) != 1 || tracker.created[0].Title != "Pause on a usage limit" {
		t.Fatalf("created work items = %#v, want only the first", tracker.created)
	}
	if pending := session.Proposals(); len(pending) != 0 {
		t.Fatalf("awaiting decision = %#v, want both decided", pending)
	}
	transcript := out.String()
	for _, required := range []string{
		"proposes 2 work item(s), asked one at a time",
		`Approving proposal 1.1 creates the work item "Pause on a usage limit", so it can be started`,
		"create 1.1 (1 of 2)? [y approves it; n <reason> declines it; ask <question> asks the Lead Product Manager about it first; anything else leaves it waiting]",
		"created yoyodyne-1",
		`Approving proposal 1.2 creates the work item "Add a retry budget"`,
		"create 1.2 (2 of 2)?",
		"declined 1.2",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// The second question is asked only after the first is answered.
	if strings.Index(transcript, "create 1.2 (2 of 2)?") < strings.Index(transcript, "created yoyodyne-1") {
		t.Fatalf("transcript = %q, want 1.2 asked after 1.1 was decided", transcript)
	}
	counted := countEvents(t, root, session)
	if counted[execution.EventProposalApproved] != 1 || counted[execution.EventProposalCreated] != 1 || counted[execution.EventProposalRejected] != 1 {
		t.Fatalf("recorded proposal events = %#v", counted)
	}
	// The decline keeps the operator's words whole.
	for _, event := range loadTestEvents(t, root, session) {
		if event.Type == execution.EventProposalRejected && !strings.Contains(string(event.Payload), "n not this quarter") {
			t.Fatalf("rejection = %s, want the operator's words as the reason", event.Payload)
		}
	}
}

// Asking about a proposal leaves it, and every one after it, undecided. The
// question is the next thing said to the role that proposed it, and once it
// has answered the proposals are put again, in order.
func TestAFollowUpQuestionLeavesTheProposalUndecided(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	backend := &fakeBackend{results: []backendapi.RunResult{
		twoProposalReply(),
		{SessionID: "session-1", FinalText: "It waits out the limit instead of failing the run."},
	}}
	options := testOptions(t, backend)
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("what next?\nask what happens to a run meanwhile?\ny\ny\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if len(backend.requests) != 2 {
		t.Fatalf("turns taken = %d, want the question to have been a turn of its own", len(backend.requests))
	}
	asked := backend.requests[1].Prompt
	for _, required := range []string{"A question about proposal 1.1 (Pause on a usage limit) before I decide it; it is still undecided", "what happens to a run meanwhile?"} {
		if !strings.Contains(asked, required) {
			t.Fatalf("follow-up prompt = %q, want it to contain %q", asked, required)
		}
	}
	transcript := out.String()
	for _, required := range []string{
		"proposal 1.1 stays undecided. Your question goes to the Lead Product Manager now, and proposal 1.1 is put to you again after its answer, with the 1 after it.",
		"It waits out the limit instead of failing the run.",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// Nothing was decided before the answer: the second proposal was not asked
	// about until both were put again, and then each once more.
	answer := strings.Index(transcript, "It waits out the limit")
	if first := strings.Index(transcript, "create 1.2 (2 of 2)?"); first < answer {
		t.Fatalf("transcript = %q, want 1.2 asked only after the answer", transcript)
	}
	if strings.Count(transcript, "create 1.1 (1 of 2)?") != 2 {
		t.Fatalf("transcript = %q, want 1.1 asked before and after the question", transcript)
	}
	if len(tracker.created) != 2 {
		t.Fatalf("created work items = %#v, want both approved after the answer", tracker.created)
	}
	if counted := countEvents(t, root, session); counted[execution.EventProposalRejected] != 0 {
		t.Fatalf("recorded proposal events = %#v, want nothing declined", counted)
	}
}

// A question that is never answered with a decision leaves the proposal waiting
// across the end of the conversation.
func TestAFollowUpQuestionDecidesNothingByItself(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		twoProposalReply(),
		{SessionID: "session-1", FinalText: "Because runs fail on limits today."},
	}})
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\nwhy is the first one needed?\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v", tracker.created)
	}
	if pending := session.Proposals(); len(pending) != 2 {
		t.Fatalf("awaiting decision = %#v, want both still undecided", pending)
	}
}

// An answer the harness does not recognise, and an empty one, decide nothing:
// no proposal is declined on their account, and the transcript says so.
func TestAnUnrecognisedAnswerDeclinesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{twoProposalReply()}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\nmaybe later\n\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v", tracker.created)
	}
	if pending := session.Proposals(); len(pending) != 2 {
		t.Fatalf("awaiting decision = %#v, want both still undecided", pending)
	}
	if counted := countEvents(t, root, session); counted[execution.EventProposalRejected] != 0 {
		t.Fatalf("recorded proposal events = %#v, want nothing declined", counted)
	}
	transcript := out.String()
	for _, required := range []string{
		`"maybe later" is not an answer the harness recognises, so proposal 1.1 is left undecided; nothing was approved or declined.`,
		"proposal 1.2 is left undecided; nothing was approved or declined.",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
}

// What one answer to one question reads as.
func TestReadOneAnswer(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		line string
		want oneAnswer
	}{
		{"y", oneAnswer{kind: answerApprove}},
		{" Yes ", oneAnswer{kind: answerApprove}},
		{"approve", oneAnswer{kind: answerApprove}},
		{"n", oneAnswer{kind: answerDecline}},
		{"no thanks", oneAnswer{kind: answerDecline, words: "no thanks"}},
		{"decline too vague", oneAnswer{kind: answerDecline, words: "decline too vague"}},
		{"ask why now", oneAnswer{kind: answerAsk, words: "why now"}},
		{"ask", oneAnswer{kind: answerAsk}},
		{"? why now", oneAnswer{kind: answerAsk, words: "why now"}},
		{"what does it cost?", oneAnswer{kind: answerAsk, words: "what does it cost?"}},
		{"", oneAnswer{kind: answerUndecided}},
		// An approval with words after it is not one the harness can be sure of,
		// so it decides nothing rather than creating anything.
		{"yes but later", oneAnswer{kind: answerUndecided, words: "yes but later"}},
		{"approve 1,3 and decline 2", oneAnswer{kind: answerUndecided, words: "approve 1,3 and decline 2"}},
		{"not this quarter", oneAnswer{kind: answerUndecided, words: "not this quarter"}},
	} {
		if got := readOneAnswer(test.line); got != test.want {
			t.Fatalf("readOneAnswer(%q) = %#v, want %#v", test.line, got, test.want)
		}
	}
}

// A tracker that will not create the item is not a reason to hold the operator
// at the prompt. The proposal stays undecided, exactly as it does when a single
// answer fails, and the conversation carries on.
func TestAProposalTheTrackerRefusesIsNotAskedAboutAgain(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{err: errors.New("bd create failed")}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{twoProposalReply()}})
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	// Both are approved and neither can be created. The conversation reaches the
	// /exit that follows rather than asking about them until the input runs out.
	input := strings.NewReader("what next?\ny\ny\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if len(tracker.created) != 0 {
		t.Fatalf("a refusing tracker created %#v", tracker.created)
	}
	// Neither was decided, so both are named as loose ends when the conversation
	// ends rather than being recorded as declined by a failure.
	if pending := session.Proposals(); len(pending) != 2 {
		t.Fatalf("awaiting decision = %#v, want both still undecided", pending)
	}
	transcript := out.String()
	for _, required := range []string{
		"1.1 was not created: ",
		"bd create failed",
		"left undecided rather than asked about again",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	if asked := strings.Count(transcript, "create 1.1 (1 of 2)?"); asked != 1 {
		t.Fatalf("the operator was asked %d times, want once", asked)
	}
}

// Which answers are decisions when there is nothing left to resolve them
// against. The grammar above cannot be asked with no cards on the table, so this
// is the rule that separates an operator deciding a proposal a second process
// already settled from an operator talking — and it errs toward speech, because
// treating speech as a decision would refuse a message somebody meant to send.
func TestAnAnswerNamesAProposalOnlyWhenItDecidesOneByName(t *testing.T) {
	t.Parallel()

	for _, decision := range []struct {
		answer string
		named  string
	}{
		{answer: "approve 1.1", named: "1.1"},
		{answer: "decline 1.1 we already handle this", named: "1.1"},
		{answer: "yes 3.1 and 3.2", named: "3.1"},
		{answer: "approve 2.1, 2.2", named: "2.1"},
	} {
		named, names := namesAProposal(decision.answer)
		if !names || named != decision.named {
			t.Errorf("namesAProposal(%q) = %q, %t; want %q named", decision.answer, named, names, decision.named)
		}
	}

	// Everything else is somebody talking. A bare yes names no proposal, so with
	// nothing on the table there is none it could mean; a number is a position in
	// a listing that is gone; and a sentence that merely mentions a proposal is a
	// sentence.
	for _, speech := range []string{
		"y",
		"yes",
		"yes please",
		"no thanks",
		"approve the resolver work",
		"approve 1",
		"what about 1.1?",
		"no, 1.1 was the one I meant",
		"",
	} {
		if named, names := namesAProposal(speech); names {
			t.Errorf("namesAProposal(%q) = %q, true; want it read as speech", speech, named)
		}
	}
}
