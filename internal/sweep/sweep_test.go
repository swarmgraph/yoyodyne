package sweep

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/terms"
)

const completeBlock = "I looked at the stopped work.\n\n" +
	"```yoyodyne-sweep\n" +
	`{"status":"complete","summary":"two dead claims, both released","findings":[{"issue":"two claims on runs nothing is running","disposition":"fixed","detail":"released both","filed":["yoyodyne-ifd.300"]}]}` +
	"\n```\n"

func TestExtractReadsTheAccountAndLeavesTheProse(t *testing.T) {
	t.Parallel()

	prose, result, note, err := Extract(completeBlock)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result == nil {
		t.Fatal("Extract() found no result in a reply that carries one")
	}
	if result.Status != StatusComplete {
		t.Errorf("status = %q, want %q", result.Status, StatusComplete)
	}
	if len(result.Findings) != 1 || result.Findings[0].Disposition != DispositionFixed {
		t.Errorf("findings = %+v, want the one fixed finding", result.Findings)
	}
	if !strings.Contains(prose, "I looked at the stopped work.") {
		t.Errorf("prose = %q, want what the role said before the block", prose)
	}
	if strings.Contains(prose, "yoyodyne-sweep") {
		t.Errorf("prose = %q, want the block taken out of it", prose)
	}
	// A single block is the contract kept, and there is nothing to note about it.
	if note != "" {
		t.Errorf("note = %q, want nothing said about a reply with one block", note)
	}
}

// The contract is one block per reply, and a role that sends two has slipped;
// but the slip is the model's and recurs, and the pass's decisions were taken
// whether or not its record survives. So the last block is the account and the
// note says the reply carried more than one — rather than the pass being thrown
// away, which is what happened on 2026-09-14 to a triage pass that wrote "more"
// and then "complete".
func TestAReplyWithMoreThanOneBlockRecordsTheLast(t *testing.T) {
	t.Parallel()

	reply := "I worked the pile.\n\n" +
		"```yoyodyne-sweep\n" +
		`{"status":"more","summary":"twelve decided, more behind them"}` +
		"\n```\n\nOn reflection that was all of it.\n\n" +
		"```yoyodyne-sweep\n" +
		`{"status":"complete","summary":"twelve decided, nothing behind them","findings":[{"issue":"a stale report","disposition":"filed","filed":["yoyodyne-ifd.400"]}]}` +
		"\n```\n"
	prose, result, note, err := Extract(reply)
	if err != nil {
		t.Fatalf("Extract() refused a reply with two blocks: %v", err)
	}
	if result == nil {
		t.Fatal("Extract() found no result in a reply that carries two")
	}
	if result.Status != StatusComplete || len(result.Findings) != 1 {
		t.Errorf("result = %+v, want the last block's account", result)
	}
	if note == "" || !strings.Contains(note, "2 sweep blocks") {
		t.Errorf("note = %q, want it to say the reply carried two blocks", note)
	}
	if strings.Contains(prose, "yoyodyne-sweep") || strings.Contains(prose, `"status"`) {
		t.Errorf("prose = %q, want every block taken out of it", prose)
	}
	for _, want := range []string{"I worked the pile.", "On reflection that was all of it."} {
		if !strings.Contains(prose, want) {
			t.Errorf("prose = %q, want what the role said around the blocks", prose)
		}
	}
}

// Tolerating a second block is not tolerating a broken one: a block that cannot
// be read is refused wherever it sits in the reply.
func TestAMalformedBlockAmongSeveralIsStillRefused(t *testing.T) {
	t.Parallel()

	for name, reply := range map[string]string{
		"the last is not JSON": "```yoyodyne-sweep\n" + `{"status":"more","summary":"started"}` + "\n```\n\n```yoyodyne-sweep\nnot json\n```\n",
		"the last is unclosed": "```yoyodyne-sweep\n" + `{"status":"more","summary":"started"}` + "\n```\n\n```yoyodyne-sweep\n" + `{"status":"complete","summary":"done"}` + "\n",
	} {
		if _, result, _, err := Extract(reply); err == nil {
			t.Errorf("%s: Extract() accepted %q as %+v", name, reply, result)
		}
	}
}

// Most replies in this harness carry no block of any given kind, and a turn that
// answered in prose is not a failure: what is lost is the structure, which the
// caller says out loud rather than losing the turn over.
func TestReplyWithoutABlockIsNotAFailure(t *testing.T) {
	t.Parallel()

	prose, result, _, err := Extract("I found nothing worth reporting.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want none from a reply that carries no block", result)
	}
	if prose != "I found nothing worth reporting." {
		t.Errorf("prose = %q, want the whole reply", prose)
	}
}

// A fix that files nothing for its root cause is a silent repair. It is the one
// thing a week of these reports is read for, so it is a question the record
// answers rather than one every reader computes.
func TestSilentRepairIsAFixThatFiledNothing(t *testing.T) {
	t.Parallel()

	silent := Finding{Issue: "a stuck delivery", Disposition: DispositionFixed}
	if !silent.SilentRepair() {
		t.Error("a fix with nothing filed is not reported as a silent repair")
	}
	filed := Finding{Issue: "a stuck delivery", Disposition: DispositionFixed, Filed: []string{"yoyodyne-ifd.300"}}
	if filed.SilentRepair() {
		t.Error("a fix that filed root-cause work is reported as a silent repair")
	}
	// A thing the role did not fix files nothing by definition, and calling that
	// a silent repair would bury the fixes that are.
	left := Finding{Issue: "a design contradiction", Disposition: DispositionConsulted}
	if left.SilentRepair() {
		t.Error("a finding nobody fixed is reported as a silent repair")
	}
	result := Result{Status: StatusComplete, Summary: "one of each", Findings: []Finding{silent, filed, left}}
	if count := result.SilentRepairs(); count != 1 {
		t.Errorf("silent repairs = %d, want 1", count)
	}
}

func TestDecodeRefusesWhatCannotBeTrusted(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"an empty block":          "",
		"an unknown field":        `{"status":"complete","summary":"fine","verdict":"approve"}`,
		"an unknown status":       `{"status":"finished","summary":"fine"}`,
		"no summary":              `{"status":"complete"}`,
		"an unknown disposition":  `{"status":"complete","summary":"fine","findings":[{"issue":"a thing","disposition":"handled"}]}`,
		"a finding with no issue": `{"status":"complete","summary":"fine","findings":[{"issue":"","disposition":"fixed"}]}`,
		"trailing content":        `{"status":"complete","summary":"fine"} and then some`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(payload); err == nil {
				t.Fatalf("Decode(%q) accepted %s", payload, name)
			}
		})
	}
}

// The volume bound is what keeps a channel like this worth reading. A pass that
// found forty things has found something systemic, and the summary is where that
// belongs.
func TestDecodeRefusesMoreFindingsThanTheBound(t *testing.T) {
	t.Parallel()

	findings := make([]string, 0, MaxFindings+1)
	for i := 0; i <= MaxFindings; i++ {
		findings = append(findings, `{"issue":"a thing","disposition":"left"}`)
	}
	payload := `{"status":"complete","summary":"many","findings":[` + strings.Join(findings, ",") + `]}`
	if _, err := Decode(payload); err == nil {
		t.Fatalf("Decode() accepted %d findings, and the bound is %d", MaxFindings+1, MaxFindings)
	}
}

// A firing takes several turns, and the record has to hold what all of them
// found: the last turn says how the pass ended, and the findings accumulate.
func TestMergeKeepsEveryTurnsFindings(t *testing.T) {
	t.Parallel()

	first := Result{Status: StatusMore, Summary: "started", Findings: []Finding{{Issue: "one", Disposition: DispositionFixed}}}
	second := Result{Status: StatusComplete, Summary: "finished", Findings: []Finding{{Issue: "two", Disposition: DispositionFiled}}}
	merged := first.Merge(second)
	if merged.Status != StatusComplete || merged.Summary != "finished" {
		t.Errorf("merged = %+v, want the last turn's status and summary", merged)
	}
	if len(merged.Findings) != 2 {
		t.Errorf("findings = %+v, want both turns' findings", merged.Findings)
	}
	// Merging must not write into what it was given: the earlier turn's record is
	// held elsewhere and rewriting it would corrupt what was already reported.
	if len(first.Findings) != 1 {
		t.Errorf("the earlier turn's findings = %+v, want them untouched", first.Findings)
	}
}

// The bounds a role is told about and the bounds enforced on what it sends back
// have to be one statement, or the contract teaches an agent to write something
// that is then refused.
func TestContractStatesTheBoundsItEnforces(t *testing.T) {
	t.Parallel()

	contract := Contract()
	for _, want := range []string{Fence, maxFindingsText, maxQuestionsText, string(StatusComplete), string(StatusMore), string(DispositionFixed)} {
		if !strings.Contains(contract, want) {
			t.Errorf("the contract does not state %q:\n%s", want, contract)
		}
	}
	if maxFindingsText != "20" || MaxFindings != 20 {
		t.Errorf("the contract says %s findings and the code enforces %d", maxFindingsText, MaxFindings)
	}
	if maxQuestionsText != "5" || MaxQuestions != 5 {
		t.Errorf("the contract says %s questions and the code enforces %d", maxQuestionsText, MaxQuestions)
	}
}

// The whole example in the contract has to decode, because it is what an agent
// copies.
func TestContractExampleDecodes(t *testing.T) {
	t.Parallel()

	contract := Contract()
	opens := strings.Index(contract, Fence)
	if opens < 0 {
		t.Fatalf("the contract shows no block:\n%s", contract)
	}
	payload := contract[opens+len(Fence):]
	closes := strings.Index(payload, "\n```")
	if closes < 0 {
		t.Fatalf("the contract's block is not closed:\n%s", contract)
	}
	// The example writes the vocabulary as alternatives -- "complete|more" -- so
	// what is checked is that one concrete choice of them decodes.
	example := strings.NewReplacer(
		"complete|more", string(StatusComplete),
		"fixed|filed|consulted|left", string(DispositionFixed),
	).Replace(payload[:closes])
	if _, err := Decode(example); err != nil {
		t.Fatalf("the contract's own example does not decode: %v\n%s", err, example)
	}
}

// The bound a whole firing is held to is not the bound one turn is held to, and
// the difference is the heavy pass this whole mechanism exists for: a role that
// legitimately reported the per-turn maximum on each of several turns produces a
// merged account several times that size, and holding it to the per-turn cap
// discarded the durable report of exactly those passes.
func TestAFullPassWorthOfTurnsStillValidates(t *testing.T) {
	t.Parallel()

	merged := Result{Status: StatusComplete, Summary: "a heavy pass"}
	for turn := 0; turn < MaxMergedTurns; turn++ {
		next := Result{Status: StatusComplete, Summary: "a heavy pass"}
		for i := 0; i < MaxFindings; i++ {
			next.Findings = append(next.Findings, Finding{Issue: "a thing", Disposition: DispositionFiled})
		}
		for i := 0; i < MaxQuestions; i++ {
			next.Questions = append(next.Questions, "something only a person can settle")
		}
		merged = merged.Merge(next)
	}
	if len(merged.Findings) != MaxPassFindings {
		t.Errorf("findings = %d, want every turn's kept up to %d", len(merged.Findings), MaxPassFindings)
	}
	if len(merged.Questions) != MaxPassQuestions {
		t.Errorf("questions = %d, want every turn's kept up to %d", len(merged.Questions), MaxPassQuestions)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("a full pass worth of turns does not validate, so its durable report would be discarded: %v", err)
	}
}

// The per-turn cap still binds what one turn may send, which is the number the
// contract states and the one that keeps a single reply readable.
func TestOneTurnIsStillHeldToThePerTurnBound(t *testing.T) {
	t.Parallel()

	overTurn := Result{Status: StatusComplete, Summary: "one very long turn"}
	for i := 0; i <= MaxFindings; i++ {
		overTurn.Findings = append(overTurn.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
	}
	if err := overTurn.validateTurn(); err == nil {
		t.Fatalf("a turn carrying %d findings passed the per-turn bound of %d", len(overTurn.Findings), MaxFindings)
	}
	// The same account is fine as a whole pass's, which is the distinction the two
	// contracts exist to draw.
	if err := overTurn.Validate(); err != nil {
		t.Fatalf("an account within the pass bound does not validate as one: %v", err)
	}
}

// Beyond the pass bound the merge drops rather than growing without limit, and
// says so: a silently shortened list reads as a pass that found less than it did.
func TestMergeSaysWhatItDropped(t *testing.T) {
	t.Parallel()

	full := Result{Status: StatusComplete, Summary: "the pass"}
	for i := 0; i < MaxPassFindings; i++ {
		full.Findings = append(full.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
	}
	for i := 0; i < MaxPassQuestions; i++ {
		full.Questions = append(full.Questions, "a question")
	}
	merged := full.Merge(Result{
		Status:    StatusComplete,
		Summary:   "one turn too many",
		Findings:  []Finding{{Issue: "the one over", Disposition: DispositionLeft}},
		Questions: []string{"the question over"},
	})
	if len(merged.Findings) != MaxPassFindings || len(merged.Questions) != MaxPassQuestions {
		t.Errorf("merged = %d findings and %d questions, want them held at the pass bound",
			len(merged.Findings), len(merged.Questions))
	}
	if !strings.Contains(merged.Summary, "not listed") {
		t.Errorf("summary = %q, want it to say what was dropped", merged.Summary)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("a merge held at its own bound does not validate: %v", err)
	}
}

// The note about what was dropped must not itself push the summary past what the
// record accepts, which would lose the report the note exists to preserve.
func TestTheDroppedNoteKeepsTheSummaryInsideItsBound(t *testing.T) {
	t.Parallel()

	full := Result{Status: StatusComplete, Summary: strings.Repeat("x", MaxSummaryBytes)}
	for i := 0; i < MaxPassFindings; i++ {
		full.Findings = append(full.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
	}
	merged := full.Merge(Result{
		Status:   StatusComplete,
		Summary:  strings.Repeat("y", MaxSummaryBytes),
		Findings: []Finding{{Issue: "the one over", Disposition: DispositionLeft}},
	})
	if len(merged.Summary) > MaxSummaryBytes {
		t.Errorf("summary is %d bytes, and the record's bound is %d", len(merged.Summary), MaxSummaryBytes)
	}
	if !strings.Contains(merged.Summary, "not listed") {
		t.Errorf("summary = %q, want the note kept whole when the summary is cut", merged.Summary)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("a merge with a full-length summary does not validate: %v", err)
	}
}

// And the summary is cut on a rune boundary to make room for the note. An "é" is
// two bytes, so of the two summaries below, one puts the cut in the middle of
// one whatever the note's length.
func TestTheDroppedNoteCutsTheSummaryOnARuneBoundary(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"", "x"} {
		full := Result{Status: StatusComplete, Summary: "a thing"}
		for i := 0; i < MaxPassFindings; i++ {
			full.Findings = append(full.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
		}
		merged := full.Merge(Result{
			Status:   StatusComplete,
			Summary:  prefix + strings.Repeat("é", MaxSummaryBytes/2),
			Findings: []Finding{{Issue: "the one over", Disposition: DispositionLeft}},
		})
		if !utf8.ValidString(merged.Summary) || len(merged.Summary) > MaxSummaryBytes {
			t.Errorf("summary after %q is %d bytes, valid = %v, want valid text inside %d", prefix, len(merged.Summary), utf8.ValidString(merged.Summary), MaxSummaryBytes)
		}
		if !strings.HasSuffix(merged.Summary, "not listed.)") {
			t.Errorf("summary after %q does not end with the note kept whole", prefix)
		}
	}
}

// Every recurring task and every program manager pass ends on this contract,
// and what it asks for is read by a person, so it carries the rule for naming
// a work item by what it is rather than by its number alone.
func TestContractNamesWorkItemsByWhatTheyAre(t *testing.T) {
	t.Parallel()

	if !strings.Contains(Contract(), terms.ItemNaming) {
		t.Errorf("the contract does not carry the rule for naming work items:\n%s", Contract())
	}
}

// Every recurring task and every program manager pass ends on this contract,
// and a pass decides things, so it carries the rule that a decision the role
// can make is made and reported afterwards rather than put to the operator.
func TestContractDecidesAndReportsRatherThanRoutingApprovals(t *testing.T) {
	t.Parallel()

	if !strings.Contains(Contract(), terms.DecideAndReport) {
		t.Errorf("the contract does not carry the rule against routing approvals to the operator:\n%s", Contract())
	}
}

// Every recurring task and every program manager pass ends on this contract, so
// it is where the role is told that a finding has to leave a trace outside the
// account, and which traces count.
func TestContractSaysAFindingMustLeaveATrace(t *testing.T) {
	t.Parallel()

	for _, want := range []string{"A finding must leave a trace", "a memory written", "your lane report changed", "a report filed", "work admitted", "untraced"} {
		if !strings.Contains(Contract(), want) {
			t.Errorf("the contract does not carry %q:\n%s", want, Contract())
		}
	}
}

func TestContractAppliesStandingGoals(t *testing.T) {
	t.Parallel()

	if !strings.Contains(Contract(), terms.StandingGoals) {
		t.Fatal("the pass report contract does not apply standing goals to its own output and decisions")
	}
	if !strings.Contains(Contract(), terms.PersonWriting) {
		t.Fatal("the pass report contract does not hold what it writes for a person to ordinary words and local time")
	}
}

// A pass asks for its report block once, at the end of its answer, and says the
// last block is the account if more than one is written.
func TestTheContractAsksForTheBlockOnceAtTheEnd(t *testing.T) {
	t.Parallel()

	contract := Contract()
	for _, wanted := range []string{"Write this block once, at the very end of your answer", "the last one is your account"} {
		if !strings.Contains(contract, wanted) {
			t.Fatalf("the contract is missing %q:\n%s", wanted, contract)
		}
	}
}
