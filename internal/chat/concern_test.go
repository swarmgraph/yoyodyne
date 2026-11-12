package chat

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

func TestExtractConcernsSeparatesProseFromWhatItWillNotPropose(t *testing.T) {
	t.Parallel()

	reply := "I would not put either of these in the queue yet.\n\n" +
		concernFence + "\n" +
		`{"concerns":[
		   {"kind":"unplaceable","subject":"A plugin marketplace","detail":"No goal in the specifications covers third-party extensions.","question":"Is there a goal this serves that I have not been given?"},
		   {"kind":"conflict","subject":"Let the reviewer merge its own work","goal":"Publish work as pull requests without letting any agent push or merge.","detail":"This is the thing that goal exists to prevent.","question":"Do you want that goal changed?"}
		 ]}` + "\n```\n\nSay which and I will write it up.\n"

	prose, concerns, err := extractConcerns(reply)
	if err != nil {
		t.Fatalf("extractConcerns() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-concern") || strings.Contains(prose, "\"kind\"") {
		t.Fatalf("prose kept the concern block: %q", prose)
	}
	if !strings.HasPrefix(prose, "I would not put either") || !strings.HasSuffix(prose, "write it up.") {
		t.Fatalf("prose = %q", prose)
	}
	if len(concerns) != 2 {
		t.Fatalf("concerns = %#v", concerns)
	}
	// The cases stay apart: what the operator answers about unplaceable work is
	// not what they answer about a conflict.
	if concerns[0].Kind != ConcernUnplaceable || concerns[0].Goal != "" {
		t.Fatalf("first concern = %#v", concerns[0])
	}
	if concerns[1].Kind != ConcernConflict || !strings.HasPrefix(concerns[1].Goal, "Publish work as pull requests") {
		t.Fatalf("second concern = %#v", concerns[1])
	}

	// A reply that raises nothing is prose, whole and unchanged.
	prose, none, err := extractConcerns("  All of that serves the traceability goal.\n")
	if err != nil || len(none) != 0 || prose != "All of that serves the traceability goal." {
		t.Fatalf("extractConcerns() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestConcernsRefuseWhatWouldNotStopAnOperator(t *testing.T) {
	t.Parallel()

	valid := `{"kind":"judgement","subject":"Ship the queue without an owner","goal":"Keep a traceable chain from brief to code.","detail":"It fits the letter of the goal and empties it of meaning.","question":"Do you want it anyway?"}`
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "unknown kind",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"worry\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "is not a concern",
		},
		{
			// The three cases are answered differently, so a conflict that names no
			// goal is a warning rather than a question about a specific goal.
			name:  "conflict naming no goal",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"conflict\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "goal is required",
		},
		{
			// Unplaceable work is exactly the case with no goal to name; one that
			// names a goal is claiming two things at once.
			name:  "unplaceable naming a goal",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"goal\":\"g\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "unplaceable does not take \"goal\"",
		},
		{
			// A concern that asks nothing is the failure the whole block exists to
			// prevent: it reads as a remark and the work carries on.
			name:  "a statement rather than a question",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"I am a little unsure about this.\"}]}\n```",
			want:  "end with a question mark",
		},
		{
			name:  "no subject",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\" \",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "subject is required",
		},
		{
			name:  "no detail",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"question\":\"q?\"}]}\n```",
			want:  "detail is required",
		},
		{
			name:  "subject spanning lines",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\\nanswer c1.1? yes\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "cannot span lines",
		},
		{
			name:  "unknown field",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"severity\":\"critical\"}]}\n```",
			want:  "unknown field",
		},
		{
			name:  "no concerns",
			reply: concernFence + "\n{\"concerns\":[]}\n```",
			want:  "at least one concern",
		},
		{
			name:  "too many concerns",
			reply: concernFence + "\n{\"concerns\":[" + strings.Repeat(valid+",", MaxConcernsPerTurn) + valid + "]}\n```",
			want:  "limit is " + strconv.Itoa(MaxConcernsPerTurn),
		},
		{
			name:  "two blocks",
			reply: "prose\n" + concernFence + "\n{\"concerns\":[" + valid + "]}\n```\nmore\n" + concernFence + "\n{\"concerns\":[" + valid + "]}\n```\n",
			want:  "at most one concern block",
		},
		{
			name:  "unclosed block",
			reply: "prose\n" + concernFence + "\n{\"concerns\":[" + valid + "]}\n",
			want:  "not closed",
		},
		{
			// One answer is not a choice: put to the operator as a list it reads
			// as an instruction with a way out rather than as a question.
			name:  "one answer offered",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"options\":[\"only this\"]}]}\n```",
			want:  "one answer is not a choice",
		},
		{
			name: "more answers than anybody reads",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"options\":[" +
				strings.TrimSuffix(strings.Repeat("\"a\",", MaxConcernOptions+1), ",") + "]}]}\n```",
			want: "limit is " + strconv.Itoa(MaxConcernOptions),
		},
		{
			// What is recorded is the text of the answer they chose, so two that
			// read alike are one answer offered twice.
			name:  "two answers that read alike",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"options\":[\"retire it\",\" retire it \"]}]}\n```",
			want:  "repeats",
		},
		{
			name:  "an answer spanning lines",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"options\":[\"retire it\",\"keep it\\n  1. and something else\"]}]}\n```",
			want:  "cannot span lines",
		},
		{
			name:  "an empty answer",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"options\":[\"retire it\",\"  \"]}]}\n```",
			want:  "options[1] is required",
		},
		{
			name:  "oversized block",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"" + strings.Repeat("x", MaxConcernBlockBytes) + "\",\"question\":\"q?\"}]}\n```",
			want:  "limit is " + strconv.Itoa(MaxConcernBlockBytes),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prose, concerns, err := extractConcerns(test.reply)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extractConcerns() error = %v, want it to contain %q", err, test.want)
			}
			if prose != "" || len(concerns) != 0 {
				t.Fatalf("a refused block still yielded %q and %#v", prose, concerns)
			}
		})
	}
}

func TestConcernRenderingKeepsTheCasesApart(t *testing.T) {
	t.Parallel()

	// Each kind says something different about what the operator is being asked,
	// because a listing that called all three "a concern" would hide exactly the
	// distinction they answer on.
	headlines := make(map[string]struct{})
	for _, kind := range concernKinds {
		headline := kind.Headline()
		if headline == "" {
			t.Fatalf("%s has no headline", kind)
		}
		headlines[headline] = struct{}{}
	}
	if len(headlines) != len(concernKinds) {
		t.Fatalf("the kinds share a headline: %#v", headlines)
	}

	pending := PendingConcern{
		ID:             "c2.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           2,
		Concern: Concern{
			Kind:     ConcernConflict,
			Subject:  "Let the reviewer merge its own work",
			Goal:     "No agent pushes or merges.",
			Detail:   "This is the thing that goal exists to prevent.",
			Question: "Do you want that goal changed?",
		},
	}
	rendered := pending.Render(console.Theme{})
	for _, required := range []string{
		"[c2.1] " + ConcernConflict.Headline(),
		"about: Let the reviewer merge its own work",
		"goal: No agent pushes or merges.",
		"Do you want that goal changed?",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered concern = %q, want it to contain %q", rendered, required)
		}
	}
	// Provider text is indented under the concern's own identifier, so no line of
	// it sits at the margin where the harness speaks to the operator.
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")[1:] {
		if !strings.HasPrefix(line, "    ") {
			t.Fatalf("rendered concern line %q is not indented", line)
		}
	}
}

// All three kinds stop and wait, and one of them says something is already
// wrong: work that would cut against a goal the product has written down. A
// listing that dressed all three alike would hide exactly the distinction the
// operator reads first, and the emphasis is the one every report is filed under
// because reports and concerns share the same scroll.
func TestAConcernIsEmphasizedByWhatItsKindAsksFor(t *testing.T) {
	t.Parallel()

	if severity := ConcernConflict.Severity(); severity != report.SeverityCritical {
		t.Fatalf("a conflict is filed at %q, want %q", severity, report.SeverityCritical)
	}
	for _, kind := range []ConcernKind{ConcernUnplaceable, ConcernJudgement} {
		if severity := kind.Severity(); severity != report.SeverityWarning {
			t.Fatalf("%s is filed at %q, want %q", kind, severity, report.SeverityWarning)
		}
	}

	conflict := pendingConcern("c1.1", ConcernConflict)
	unplaceable := pendingConcern("c1.2", ConcernUnplaceable)
	// The theme a redirected stream gets dresses nothing, so what is left is the
	// marker and the headline — which is the whole of what a dumb terminal has.
	plain := conflict.Render(console.Theme{})
	if !strings.HasPrefix(plain, "!! [c1.1] ") {
		t.Fatalf("a conflict is not marked at the margin: %q", plain)
	}
	if quieter := unplaceable.Render(console.Theme{}); !strings.HasPrefix(quieter, "!  [c1.2] ") {
		t.Fatalf("work it cannot place is not marked at the margin: %q", quieter)
	}
	theme := dressedTheme()
	if dressed := conflict.Render(theme); dressed == unplaceable.Render(theme) || dressed == plain {
		t.Fatalf("a conflict reads like the other kinds: %q", dressed)
	}
	// The question inside it still gets the colour every question gets, under the
	// severity the whole of it is weighted by: what the operator has to do about
	// a concern is answer it.
	question, _, _ := strings.Cut(theme.Questions("?"), "?")
	if !strings.Contains(conflict.Render(theme), question+"    Do you want that goal changed?") {
		t.Fatalf("the question in a concern lost its own colour: %q", conflict.Render(theme))
	}
}

func pendingConcern(id string, kind ConcernKind) PendingConcern {
	concern := Concern{
		Kind:     kind,
		Subject:  "Let the reviewer merge its own work",
		Goal:     "No agent pushes or merges.",
		Detail:   "This is the thing that goal exists to prevent.",
		Question: "Do you want that goal changed?",
	}
	// Work that traces to no goal is the one case with no goal to name.
	if kind == ConcernUnplaceable {
		concern.Goal = ""
	}
	return PendingConcern{
		ID:             id,
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           1,
		Concern:        concern,
	}
}

func TestConverseStopsAndWaitsForAnAnswerToEachConcern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: concernReply(
			"Neither of these is mine to decide.",
			`{"kind":"unplaceable","subject":"A plugin marketplace","detail":"Nothing in the goals covers third-party extensions.","question":"Is there a goal for this that I have not been given?"}`,
			`{"kind":"judgement","subject":"Skip the reviewer for small changes","goal":"Keep a traceable chain from brief to code.","detail":"It is consistent with the goal as written and empties it of meaning.","question":"Do you want it anyway?"}`,
		)},
		{SessionID: "session-1", FinalText: "Then I will write the goal up first."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	// The operator answers the first question and leaves the second alone.
	input := strings.NewReader("what should we do next?\nthere is no such goal; write one\n\nnoted\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	for _, required := range []string{
		"will not propose 2 thing(s)",
		"[c1.1] " + ConcernUnplaceable.Headline(),
		"Is there a goal for this that I have not been given?",
		"answer c1.1?",
		"[c1.2] " + ConcernJudgement.Headline(),
		"c1.2 is still open",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// Raising a concern proposes nothing and creates nothing: it is the product
	// manager declining to put the work in front of the operator as work.
	if len(tracker.created) != 0 || len(session.Proposals()) != 0 {
		t.Fatalf("a concern created %#v and proposed %#v", tracker.created, session.Proposals())
	}
	// Silence is not agreement: the unanswered question is still open.
	open := session.Concerns()
	if len(open) != 1 || open[0].ID != "c1.2" {
		t.Fatalf("open concerns = %#v", open)
	}
	counted := countEvents(t, root, session)
	if counted[execution.EventConcernRaised] != 2 || counted[execution.EventConcernAnswered] != 1 {
		t.Fatalf("recorded concern events = %#v", counted)
	}
	// What the operator said reaches the product manager on its next turn, which
	// is the point of stopping to ask.
	if len(provider.requests) != 2 {
		t.Fatalf("provider turns = %d, want 2", len(provider.requests))
	}
	if !strings.Contains(provider.requests[1].Prompt, "answered concern c1.1") ||
		!strings.Contains(provider.requests[1].Prompt, "there is no such goal; write one") {
		t.Fatalf("second prompt = %q", provider.requests[1].Prompt)
	}
}

// TestAnEnumerableQuestionIsAnsweredByChoosing is the operator's side of the
// one question a turn is allowed to stop on: where the product manager can name
// the answers, answering is a selection rather than a sentence typed into a
// chat field, and what they chose is what the record and the next turn carry.
func TestAnEnumerableQuestionIsAnsweredByChoosing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: concernReply(
			"Nothing in the goals covers this.",
			`{"kind":"unplaceable","subject":"A plugin marketplace","detail":"No goal covers third-party extensions.","question":"Which of these should it be?","options":["Write a goal for extensions","Retire the work"]}`,
		)},
		{SessionID: "session-1", FinalText: "Retired, then."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)

	var out strings.Builder
	// The conversation is a stream here, which is the degraded path every
	// terminal falls back to: the answers are numbered and the number is typed.
	input := strings.NewReader("what should we do about the marketplace?\n2\nnoted\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	for _, required := range []string{
		"Which of these should it be?",
		"  1. Write a goal for extensions",
		"  2. Retire the work",
		// Free entry is offered wherever the question is put, so a list nobody's
		// answer is on never traps the operator.
		"  3. " + console.FreeEntryChoice,
		"answer c1.1?",
		"answered c1.1",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// The answers are put once. The question above the prompt does not list them
	// as well, because the prompt is where they can be chosen from.
	if listed := strings.Count(transcript, "2. Retire the work"); listed != 1 {
		t.Fatalf("the answers were listed %d times:\n%s", listed, transcript)
	}
	if open := session.Concerns(); len(open) != 0 {
		t.Fatalf("a chosen answer left the question open: %#v", open)
	}
	// What lands in the record is the answer itself, in the words it was offered
	// in, rather than the number that was typed to choose it.
	payload := onlyEventPayload(t, root, session, execution.EventConcernAnswered)
	if !strings.Contains(payload, `"answer":"Retire the work"`) {
		t.Fatalf("answer event = %s", payload)
	}
	// And it reaches the product manager as the answer to that question, which
	// is what stopping to ask was for.
	if len(provider.requests) != 2 {
		t.Fatalf("provider turns = %d, want 2", len(provider.requests))
	}
	if !strings.Contains(provider.requests[1].Prompt, "answered concern c1.1") ||
		!strings.Contains(provider.requests[1].Prompt, "Retire the work") {
		t.Fatalf("second prompt = %q", provider.requests[1].Prompt)
	}
}

// TestOfferedAnswersNeverNarrowWhatTheOperatorMaySay covers the answer nobody
// listed and the question asked back. A prompt that could not carry one would
// make the operator abandon it, which is the opposite of what offering the
// answers is for.
func TestOfferedAnswersNeverNarrowWhatTheOperatorMaySay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: concernReply(
			"Nothing in the goals covers this.",
			`{"kind":"unplaceable","subject":"A plugin marketplace","detail":"No goal covers third-party extensions.","question":"Which of these should it be?","options":["Write a goal for extensions","Retire the work"]}`,
		)},
	}})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("what should we do about the marketplace?\nneither; what would a goal cost us?\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if open := session.Concerns(); len(open) != 0 {
		t.Fatalf("a question answered in the operator's own words is still open: %#v", open)
	}
	payload := onlyEventPayload(t, root, session, execution.EventConcernAnswered)
	if !strings.Contains(payload, "neither; what would a goal cost us?") {
		t.Fatalf("answer event = %s", payload)
	}
}

// TestTheAnswersOnOfferReachAReaderWhoIsNotBeingPrompted covers `--message` and
// `--json`, which have nobody standing at a prompt. Nothing is put to anybody
// there, so the answers are reported as what they are: what was on offer, in
// the words the product manager offered them in, and nothing the console adds.
func TestTheAnswersOnOfferReachAReaderWhoIsNotBeingPrompted(t *testing.T) {
	t.Parallel()

	pending := PendingConcern{
		ID:             "c1.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           1,
		Concern: Concern{
			Kind:     ConcernUnplaceable,
			Subject:  "A plugin marketplace",
			Detail:   "No goal covers third-party extensions.",
			Question: "Which of these should it be?",
			Options:  []string{"Write a goal for extensions", "Retire the work"},
		},
	}
	rendered := pending.Render(console.Theme{})
	for _, required := range []string{"1. Write a goal for extensions", "2. Retire the work"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered concern = %q, want it to contain %q", rendered, required)
		}
	}
	if strings.Contains(rendered, console.FreeEntryChoice) {
		t.Fatalf("a reader who is not being prompted was offered a prompt's choice: %q", rendered)
	}

	document, err := json.Marshal(pending)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(document), `"options":["Write a goal for extensions","Retire the work"]`) {
		t.Fatalf("encoded concern = %s", document)
	}
	if strings.Contains(string(document), console.FreeEntryChoice) {
		t.Fatalf("the console's own choice reached the document: %s", document)
	}
	// A question with no answers to enumerate encodes exactly as it always did.
	pending.Concern.Options = nil
	document, err = json.Marshal(pending)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(document), "options") {
		t.Fatalf("encoded concern = %s", document)
	}
}

func TestAnsweringAConcernIsRecordedOnceAndOnlyForAConcernThatExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: concernReply("This one is yours.",
			`{"kind":"conflict","subject":"Let an agent merge its own work","goal":"No agent pushes or merges.","detail":"It is the thing that goal exists to prevent.","question":"Do you want that goal changed?"}`),
	}}})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "should we let agents merge?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if err := session.Answer("c1.1", "  "); err == nil || !strings.Contains(err.Error(), "leaves the question open") {
		t.Fatalf("empty Answer() error = %v", err)
	}
	if len(session.Concerns()) != 1 {
		t.Fatalf("an empty answer settled the question: %#v", session.Concerns())
	}
	if err := session.Answer("c1.1", "no, the goal stands"); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if len(session.Concerns()) != 0 {
		t.Fatalf("an answered concern is still open: %#v", session.Concerns())
	}
	payload := onlyEventPayload(t, root, session, execution.EventConcernAnswered)
	if !strings.Contains(payload, "no, the goal stands") || !strings.Contains(payload, "Let an agent merge its own work") {
		t.Fatalf("answer event = %s", payload)
	}
	if err := session.Answer("c1.1", "again"); err == nil || !strings.Contains(err.Error(), "already been answered") {
		t.Fatalf("second Answer() error = %v", err)
	}
	if err := session.Answer("c9.9", "no such thing"); err == nil || !strings.Contains(err.Error(), "awaiting an answer") {
		t.Fatalf("unknown Answer() error = %v", err)
	}
}

func TestConverseSurvivesAConcernItCannotRead(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Something worries me here.\n\n" + concernFence +
			"\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"severity\":\"high\"}]}\n```\n"},
		{SessionID: "session-1", FinalText: "It was about the marketplace item."},
	}})
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\nwhat was it?\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	// The answer is still the operator's to read, the loss is named, and the
	// conversation carries on rather than ending on an unreadable block.
	for _, required := range []string{
		"Something worries me here.",
		"cannot read",
		"unknown field",
		"concern block was refused and nothing in it was carried out",
		"Everything else in the reply was carried out",
		"It was about the marketplace item.",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	if len(session.Concerns()) != 0 {
		t.Fatalf("an unreadable block raised %#v", session.Concerns())
	}
	if session.Evidence().Turns != 2 {
		t.Fatalf("turns = %d, want 2: an unreadable concern ended the conversation", session.Evidence().Turns)
	}
}

func TestContractStatesTheConcernProtocolItEnforces(t *testing.T) {
	t.Parallel()

	prompt := SystemPrompt(domain.RoleProductManager, Admission{}, nil, hostilePersona)
	for _, required := range []string{
		concernFence,
		"Raise at most " + strconv.Itoa(MaxConcernsPerTurn) + " concerns",
		string(ConcernUnplaceable), string(ConcernConflict), string(ConcernJudgement),
		// The four cases the product manager has to tell apart, and the two rules
		// that make a concern something other than a caveat: it is not proposed,
		// and it waits.
		"Every piece of work you admit or propose serves a goal",
		"waits for their answer",
		"Nothing you raise this way is proposed, admitted, or created",
		// And how a question whose answers can be named is asked, including the
		// thing the product manager must not conclude from being able to name
		// them: that the operator is confined to what it listed.
		"\"options\" is optional",
		"between 2 and " + maxConcernOptionsText,
		"always offers their own words as the last choice",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("system prompt does not state %q", required)
		}
	}
	// The bounds the product manager is told are the bounds that are enforced.
	if maxConcernsPerTurnText != strconv.Itoa(MaxConcernsPerTurn) {
		t.Fatalf("contract states a limit of %s, enforced limit is %d", maxConcernsPerTurnText, MaxConcernsPerTurn)
	}
	if maxConcernOptionsText != strconv.Itoa(MaxConcernOptions) {
		t.Fatalf("contract states %s answers, enforced limit is %d", maxConcernOptionsText, MaxConcernOptions)
	}
}

// concernReply renders a provider answer that raises concerns the way the
// contract asks for them.
func concernReply(prose string, concerns ...string) string {
	return prose + "\n\n" + concernFence + "\n{\"concerns\":[" + strings.Join(concerns, ",") + "]}\n```\n"
}
