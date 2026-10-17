package exchange

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// An ask comes only from the block. Prose that wonders aloud what the architect
// would say reaches nobody, which is what stops a channel with three enforced
// properties being bypassed by writing a sentence.
func TestAnAskComesOnlyFromItsBlock(t *testing.T) {
	t.Parallel()

	prose, ask, err := Extract("I wonder what the architect would say about the cost of this goal.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if ask != nil {
		t.Fatalf("prose was read as an ask: %+v", ask)
	}
	if prose != "I wonder what the architect would say about the cost of this goal." {
		t.Fatalf("prose = %q", prose)
	}

	reply := "The cost is the open question.\n\n" + Fence + `
{"ask":{"role":"architect","question":"what would this goal cost, and what am I missing?","context":"I am about to order the backlog with it."}}
` + "```" + "\n"
	prose, ask, err = Extract(reply)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if ask == nil {
		t.Fatal("the block was not read as an ask")
	}
	if ask.Role != domain.RoleArchitect || !strings.HasPrefix(ask.Question, "what would this goal cost") {
		t.Fatalf("ask = %+v", *ask)
	}
	if prose != "The cost is the open question." {
		t.Fatalf("prose = %q", prose)
	}
}

// The channel is bidirectional and the two directions are one mechanism, so the
// architect's canonical question decodes exactly as the product manager's does.
func TestBothCanonicalDirectionsAreOneMechanism(t *testing.T) {
	t.Parallel()

	for _, direction := range []struct {
		name string
		role domain.AgentRole
		text string
	}{
		{
			name: "product manager asks the architect",
			role: domain.RoleArchitect,
			text: `{"ask":{"role":"architect","question":"what does this goal cost, and what am I missing?"}}`,
		},
		{
			name: "architect asks the product manager",
			role: domain.RoleProductManager,
			text: `{"ask":{"role":"product-manager","question":"if we sacrifice some performance, is that an unacceptable trade-off from the user's standpoint?"}}`,
		},
	} {
		ask, err := Decode(direction.text)
		if err != nil {
			t.Fatalf("%s: Decode() error = %v", direction.name, err)
		}
		if ask.Role != direction.role {
			t.Fatalf("%s: role = %q, want %q", direction.name, ask.Role, direction.role)
		}
	}
}

func TestAnAskIsRefusedWhenItAsksNothing(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"no role":             `{"ask":{"question":"what would this cost?"}}`,
		"role nobody fills":   `{"ask":{"role":"designer","question":"what would this cost?"}}`,
		"no question":         `{"ask":{"role":"architect"}}`,
		"a statement":         `{"ask":{"role":"architect","question":"this will be expensive."}}`,
		"asking and settling": `{"ask":{"role":"architect","exchange":"exchange-` + strings.Repeat("a", 32) + `","question":"and?","settled":"done"}}`,
		"settling nothing":    `{"ask":{"role":"architect","settled":"done"}}`,
		"unknown field":       `{"ask":{"role":"architect","question":"what?","urgency":"high"}}`,
		"trailing content":    `{"ask":{"role":"architect","question":"what?"}} and also`,
	} {
		if _, err := Decode(payload); err == nil {
			t.Errorf("%s: Decode() accepted %s", name, payload)
		}
	}
}

// Judgment-only and decisionless are the same rule from the answering side: an
// answer is prose, and a reply reaching for any harness block at all is refused
// whole rather than half carried out.
func TestAnAnswerCarryingAnyHarnessBlockIsRefusedWhole(t *testing.T) {
	t.Parallel()

	for name, answer := range map[string]string{
		"a tracker action": "I would admit this.\n\n```yoyodyne-tracker\n{\"actions\":[{\"action\":\"survey\"}]}\n```",
		"a proposal":       "Worth doing.\n\n```yoyodyne-proposal\n{\"items\":[]}\n```",
		"a report":         "Worth knowing.\n\n```yoyodyne-report\n{\"reports\":[]}\n```",
		"an ask of its own": "Let me ask them.\n\n" + Fence + "\n" +
			`{"ask":{"role":"developer","question":"what?"}}` + "\n```",
	} {
		if _, err := ReadAnswer(answer); err == nil {
			t.Errorf("%s: ReadAnswer() accepted a block", name)
		}
	}

	// A fence quoted inside prose is text. An answer that talks about the
	// protocol is still an answer.
	answer, err := ReadAnswer("The asker should use a ```yoyodyne-tracker block for that, not me.")
	if err != nil {
		t.Fatalf("ReadAnswer() refused quoted prose: %v", err)
	}
	if !strings.HasPrefix(answer, "The asker") {
		t.Fatalf("answer = %q", answer)
	}
	if _, err := ReadAnswer("   "); err == nil {
		t.Error("ReadAnswer() accepted an empty answer")
	}
}

func TestAnExchangeRecordIsRefusedWhenItContradictsItself(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	sound := func() Exchange {
		return Exchange{
			SchemaVersion: SchemaVersion,
			ID:            "exchange-" + strings.Repeat("a", 32),
			ProductID:     "yoyodyne",
			Asker:         Party{Role: domain.RoleProductManager},
			Answerer:      Party{Role: domain.RoleArchitect},
			Question:      "what would this cost?",
			MaxRounds:     10,
			OpenedAt:      at,
			UpdatedAt:     at,
		}
	}
	if err := sound().Validate(); err != nil {
		t.Fatalf("a sound exchange was refused: %v", err)
	}

	closed := at.Add(time.Minute)
	for name, broken := range map[string]func(*Exchange){
		"a role asking itself": func(e *Exchange) { e.Answerer.Role = e.Asker.Role },
		"no rounds allowed":    func(e *Exchange) { e.MaxRounds = 0 },
		"more rounds than the cap": func(e *Exchange) {
			e.MaxRounds = 1
			e.Rounds = []Round{{Number: 1, Question: "a?", AskedAt: at}, {Number: 2, Question: "b?", AskedAt: at}}
		},
		"an outcome with no moment": func(e *Exchange) { e.Outcome = OutcomeResolved },
		// A round may name nothing that served it, because rounds recorded before
		// the harness pinned them name nothing. What it may not do is name an
		// account, a backend, or a configuration nothing could have produced: such a
		// round reads as evidence about who paid for it and is not.
		"an account nothing configured": func(e *Exchange) {
			e.Rounds = []Round{{Number: 1, Question: "a?", AskedAt: at, AccountAlias: "Someone Else's"}}
		},
		"a revision of no digest": func(e *Exchange) {
			e.Rounds = []Round{{Number: 1, Question: "a?", AskedAt: at, ConfigRevision: "yesterday's"}}
		},
		"a backend nothing runs": func(e *Exchange) {
			e.Rounds = []Round{{Number: 1, Question: "a?", AskedAt: at, Backend: "carrier pigeon"}}
		},
		// The build is the sixth of the same kind, and is refused the same way:
		// which harness made the call is a place in a repository's history, and
		// something no repository could resolve is not an answer to it.
		"a build that is not a revision": func(e *Exchange) {
			e.Rounds = []Round{{Number: 1, Question: "a?", AskedAt: at, Build: "the one from Tuesday"}}
		},
		"unresolved but settled": func(e *Exchange) {
			e.Outcome = OutcomeUnresolved
			e.ClosedAt = &closed
			e.Settled = "we agreed"
		},
	} {
		one := sound()
		broken(&one)
		if err := one.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted it", name)
		}
	}
}

// The rounds and the cost are read together everywhere, because either alone
// answers the operator's question wrongly.
func TestAnExchangeReportsItsCostBesideItsRounds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	one := Exchange{
		SchemaVersion: SchemaVersion,
		ID:            "exchange-" + strings.Repeat("b", 32),
		ProductID:     "yoyodyne",
		Asker:         Party{Role: domain.RoleProductManager, Agent: "product-manager"},
		Answerer:      Party{Role: domain.RoleArchitect, Agent: "architect"},
		Question:      "what would this cost?",
		MaxRounds:     10,
		Rounds: []Round{
			{Number: 1, Question: "what would this cost?", Answer: "more than you think", CostUSD: 0.25, AskedAt: at},
			{Number: 2, Question: "how much more?", Answer: "twice", CostUSD: 0.5, AskedAt: at},
		},
		OpenedAt:  at,
		UpdatedAt: at,
	}
	if got := one.CostUSD(); got != 0.75 {
		t.Fatalf("cost = %v, want 0.75", got)
	}
	if got := one.RoundsRemaining(); got != 8 {
		t.Fatalf("remaining = %d, want 8", got)
	}
	summary := one.Summary()
	for _, wanted := range []string{"2/10 round(s)", "$0.7500", "Lead Product Manager asked architect"} {
		if !strings.Contains(summary, wanted) {
			t.Fatalf("summary %q is missing %q", summary, wanted)
		}
	}
	thread := one.RenderThread()
	for _, wanted := range []string{"more than you think", "twice", "round 2 of 10"} {
		if !strings.Contains(thread, wanted) {
			t.Fatalf("thread is missing %q:\n%s", wanted, thread)
		}
	}
	// What the asker is handed back says where the thread has got to against its
	// cap, because the cap is configurable and the contract cannot state it.
	delivery := one.Delivery()
	for _, wanted := range []string{"round 2 of the 10", "8 round(s)", "twice"} {
		if !strings.Contains(delivery, wanted) {
			t.Fatalf("delivery is missing %q:\n%s", wanted, delivery)
		}
	}
}

// Every block the asking contract shows has to be one the harness accepts. A
// template that is refused is worse than none: the role follows it, the block is
// called unreadable, and what it was showing never happens — which is exactly
// how the documented way to close an exchange would have left every thread
// dangling open.
func TestEveryExampleTheAskingContractShowsIsAccepted(t *testing.T) {
	t.Parallel()

	examples := blocksIn(AskingContract)
	if len(examples) != 4 {
		t.Fatalf("found %d example blocks in the asking contract, want the open, the one naming what it rests on, the continue, and the close", len(examples))
	}
	for _, example := range examples {
		// The contract writes the identifier as a placeholder, because thirty-two
		// hex digits in a contract teach nothing. Everything else is decoded as
		// written.
		payload := strings.ReplaceAll(example, "exchange-…", "exchange-"+strings.Repeat("a", 32))
		ask, err := Decode(payload)
		if err != nil {
			t.Fatalf("the contract shows a block the harness refuses:\n%s\n%v", payload, err)
		}
		if err := ask.Validate(); err != nil {
			t.Fatalf("the contract shows a block that does not validate:\n%s\n%v", payload, err)
		}
	}
}

// blocksIn returns the payload of every ask block in a contract, in order.
func blocksIn(contract string) []string {
	var payloads []string
	rest := contract
	for {
		at := strings.Index(rest, Fence+"\n")
		if at < 0 {
			return payloads
		}
		rest = rest[at+len(Fence)+1:]
		closesAt := strings.Index(rest, "\n```")
		if closesAt < 0 {
			return payloads
		}
		payloads = append(payloads, rest[:closesAt])
		rest = rest[closesAt:]
	}
}

// A follow-up in a thread names no role, because the thread already says who is
// in it. That is what the contract's continue and close templates show, and it
// is the ordinary way an exchange ends.
func TestAFollowUpNeedNotRestateWhoIsBeingAsked(t *testing.T) {
	t.Parallel()

	thread := "exchange-" + strings.Repeat("a", 32)
	for name, payload := range map[string]string{
		"a further question": `{"ask":{"exchange":"` + thread + `","question":"a further question in that thread?"}}`,
		"a close":            `{"ask":{"exchange":"` + thread + `","settled":"what you took from it"}}`,
	} {
		ask, err := Decode(payload)
		if err != nil {
			t.Fatalf("%s: Decode() error = %v", name, err)
		}
		if ask.Role != "" || ask.Exchange != thread {
			t.Fatalf("%s: ask = %+v", name, ask)
		}
	}
	// Opening one still has to say who is being asked: there is no thread to take
	// it from.
	if _, err := Decode(`{"ask":{"question":"what would this cost?"}}`); err == nil {
		t.Fatal("an exchange was opened without naming who is asked")
	}
}

// An unresolved exchange's own report carries the build the latest round was
// spoken under, skipping rounds written before rounds recorded one, and none at
// all where no round did rather than a guess.
func TestAnExchangesReportIsAboutTheBuildItsLatestRoundRan(t *testing.T) {
	t.Parallel()

	const earlier, later = "0123456789abcdef0123456789abcdef01234567", "fedcba9876543210fedcba9876543210fedcba98"
	recorded := Exchange{Rounds: []Round{{Build: earlier}, {Build: later}, {}}}
	if got := recorded.lastBuild(); got != later {
		t.Fatalf("lastBuild() = %q, want the latest recorded %q", got, later)
	}
	if got := (Exchange{Rounds: []Round{{}}}).lastBuild(); got != "" {
		t.Fatalf("lastBuild() = %q, want none where no round recorded one", got)
	}
}

// An ask may name the operator, whose reason the conversation checks; it opens
// no exchange, so it continues and settles none, and only it takes a reason.
func TestAnAskNamingTheOperatorOpensNoExchange(t *testing.T) {
	t.Parallel()

	fine := Ask{Role: "operator", OperatorReason: ownership.OperatorCredential, Question: "will you renew the login?"}
	if err := fine.Validate(); err != nil || !fine.AsksOperator() {
		t.Fatalf("Validate(%+v) = %v, want an ask naming the operator accepted", fine, err)
	}
	for _, wrong := range []Ask{
		{Role: "operator", Exchange: "exchange-" + strings.Repeat("a", 32), Question: "and again?"},
		{Role: "operator", Exchange: "exchange-" + strings.Repeat("a", 32), Settled: "done"},
		{Role: domain.RoleArchitect, OperatorReason: ownership.OperatorCredential, Question: "what does it cost?"},
	} {
		if err := wrong.Validate(); err == nil {
			t.Fatalf("Validate(%+v) passed, want it refused", wrong)
		}
	}
}
