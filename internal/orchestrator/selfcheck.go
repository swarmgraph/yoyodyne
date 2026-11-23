package orchestrator

// What a developer executed against its own change is read here, and it is the
// gate a change passes before a reviewer is ever asked about it: the harness
// runs the declared checks itself afterwards, and that proves the change works,
// but nothing except this proves anybody ran anything before handing it over.

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/composition"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/selfcheck"
)

// maxVerificationProblemBytes keeps an unreadable record to a readable line of
// the record, the same bound an unreadable landing claim is held to.
const maxVerificationProblemBytes = 512

// claimVerification takes what the developer recorded executing out of what it
// said and records it, returning the rest for the channels that read the reply
// after it.
//
// Every attempt overwrites what the last one recorded rather than accumulating.
// The record describes the change as it now stands, so a repair attempt that
// re-ran the suite must not be judged on the attempt before it — nor be able to
// inherit an earlier attempt's evidence for a change it has since altered.
//
// It reads the record from the whole reply rather than from what the landing
// claim left of it. A landing block the harness could not read takes everything
// after its own fence with it, and a developer that ran its checks and then
// wrote one malformed claim has still run them: reading this out of the leftover
// would hand that developer back a refusal for evidence it had recorded, on top
// of the withheld closure it already has.
//
// An unreadable block is recorded as a problem rather than swallowed, for the
// reason an unreadable landing claim is: the developer that wrote one was trying
// to say what it ran, and "it wrote something nobody could read" and "it wrote
// nothing" are different facts to whoever reads this afterwards. Both fail the
// gate below, because a record the harness cannot read is a record it cannot
// gate on.
func (a *activeRun) claimVerification(whole, rest string) string {
	_, record, err := selfcheck.Extract(whole)
	verification := runstate.Verification{}
	switch {
	case err != nil:
		verification.Problem = singleLine(err.Error(), maxVerificationProblemBytes)
	case record.Recorded():
		verification = durableVerification(record)
	}
	a.recordVerification(verification)
	return withoutVerificationBlock(rest)
}

// withoutVerificationBlock takes the block back out of what the reply's other
// channels read, so the record does not end up in the summary as well as in the
// record. A reply the split refuses is left as it is: the block is already
// recorded as unreadable, and cutting a reply around a fence nobody could read
// is how prose goes missing.
func withoutVerificationBlock(reply string) string {
	block, err := fenced.Split(reply, selfcheck.Fence, "verification")
	if err != nil || !block.Found {
		return reply
	}
	return block.Rest
}

// recordVerification makes what the developer recorded the run's own record of
// it. The empty record is stored as nothing at all, so a run that recorded
// nothing reads that way rather than carrying an empty shape somebody has to
// interpret — and a record that carries only what is owed is not empty, because
// that is the whole of what a run stopped for want of one has to say.
func (a *activeRun) recordVerification(verification runstate.Verification) {
	if verification.Probe == nil && len(verification.Checks) == 0 && len(verification.Owed) == 0 && verification.Problem == "" {
		a.state.Verification = nil
		a.outcome.Verification = nil
		return
	}
	recorded := verification
	a.state.Verification = &recorded
	a.outcome.Verification = &recorded
}

// durableVerification is the one conversion from what a developer wrote to what
// the schema stores. It is here, at the crossing, rather than in either package:
// the vocabulary is selfcheck's and the bounds are the schema's, and a second
// conversion somewhere else is how the two come to disagree about what a record
// may hold.
func durableVerification(record selfcheck.Record) runstate.Verification {
	probe := durableExecution(record.Probe)
	verification := runstate.Verification{Probe: &probe}
	for _, check := range record.Checks {
		verification.Checks = append(verification.Checks, durableExecution(check))
	}
	return verification
}

func durableExecution(execution selfcheck.Execution) runstate.VerificationExecution {
	return runstate.VerificationExecution{
		Command: bounded(execution.Command, runstate.MaxVerificationCommandBytes),
		Outcome: string(execution.Outcome),
		Detail:  bounded(execution.Detail, runstate.MaxVerificationDetailBytes),
	}
}

// recordedVerification is the conversion back, for the two readings that are
// selfcheck's rather than the schema's: what a record still owes, and how it
// reads to a reviewer. It is the same crossing in the other direction and is
// here for the same reason.
func recordedVerification(verification runstate.Verification) selfcheck.Record {
	record := selfcheck.Record{}
	if verification.Probe != nil {
		record.Probe = recordedExecution(*verification.Probe)
	}
	for _, check := range verification.Checks {
		record.Checks = append(record.Checks, recordedExecution(check))
	}
	return record
}

func recordedExecution(execution runstate.VerificationExecution) selfcheck.Execution {
	return selfcheck.Execution{
		Command: execution.Command,
		Outcome: selfcheck.Outcome(execution.Outcome),
		Detail:  execution.Detail,
	}
}

// bounded cuts an untrusted field to what the schema stores. The decode already
// refuses anything longer, so this is the guard on the schema's own bound rather
// than on the agent — the two are stated separately on purpose, and a record
// that somehow arrived past one of them is stored short rather than refused at
// the save, which is the point in a run where a refusal costs the most. The cut
// falls on a rune boundary, so what is stored short is still text.
func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// probeRefused reports a developer that recorded an environment which could not
// start its probe at all. It is deliberately not a judgement about the change:
// nothing a developer does to its work fixes a sandbox that cannot spawn a
// process, so a run that meets this ends rather than spending an attempt on it.
//
// It asks whether the command started and never whether it passed. A probe that
// ran and failed says the opposite thing — the environment works, and something
// the run did not cause is red — and recording that as a sandbox that could not
// be entered would point triage at the machine while the base commit stayed
// broken, on the one path where the whole point is an honest report.
func (a *activeRun) probeRefused() (runstate.VerificationExecution, bool) {
	if a.state.Verification == nil || a.state.Verification.Probe == nil {
		return runstate.VerificationExecution{}, false
	}
	probe := *a.state.Verification.Probe
	if probe.Started() {
		return runstate.VerificationExecution{}, false
	}
	return probe, true
}

// gateSelfVerification refuses a change its own developer never ran anything
// against. It is the second of the two gates a change passes before the check
// suite is spent on it and before a reviewer sees it, and it is decided exactly
// where the protected-path gate is decided, for the same reason: a change that
// is not allowed to go on does not get a suite run on it first.
//
// What it asks for depends on what the change touched, mechanically. A change
// the declared checks would read has to carry the developer's own record of
// running one of them against it; a change to content no declared check
// exercises carries the probe alone. The coverage question is answered by
// internal/composition, which holds this repository's own account of what its
// checks reach — so the line moves when the checks do, and no developer has to
// judge whether its change was the kind that needed evidence.
//
// It answers with a refusal rather than a verdict, like the path gate: nothing
// here says the change is wrong, only that nobody has shown it running, so it
// goes back to the same developer inside the same repair loop as any other
// failure standing between a change and its reviewer.
func (a *activeRun) gateSelfVerification(ctx context.Context) error {
	changed, err := a.pipeline.Worktrees.ChangedPaths(ctx, a.worktree)
	if err != nil {
		return fmt.Errorf("list the paths this change touches: %w", err)
	}
	evidenceRequired := composition.Exercised(a.worktree.Path, a.pipeline.Config.Checks, changed)
	recorded := runstate.Verification{}
	if a.state.Verification != nil {
		recorded = *a.state.Verification
	}
	owed := recordedVerification(recorded).Missing(evidenceRequired)
	// A block nobody could read leaves no record, so what it owes is what an
	// absent one owes — said after the reason, because the developer that wrote
	// one needs to be told its block was unreadable rather than that it wrote
	// nothing.
	if problem := strings.TrimSpace(recorded.Problem); problem != "" {
		owed = append([]string{"the verification block could not be read, so nothing it recorded counts: " + problem}, owed...)
	}
	if len(owed) == 0 {
		// The record in front of the gate meets the bar, so nothing an earlier
		// attempt owed describes this change any more.
		recorded.Owed = nil
		a.recordVerification(recorded)
		return nil
	}
	recorded.Owed = owed
	a.recordMissingVerification(recorded)
	return phaseError{status: runstate.StatusFailed, cause: missingVerification{verification: recorded}}
}

// recordMissingVerification makes the missing record the run's outstanding
// repair input. It clears the other two for the reason a path refusal clears
// them: this gate is decided in front of the checks, so a check failure or a
// finding recorded beside it describes a change that was judged before this one
// existed.
func (a *activeRun) recordMissingVerification(verification runstate.Verification) {
	a.clearReviewEvidence()
	a.state.CheckFailure = nil
	a.recordVerification(verification)
}

// missingVerification is a change whose developer recorded no execution of its
// own, or too little of one. It carries what was owed rather than a sentence
// about it, so the hand-back and the blocker both say the same thing.
type missingVerification struct {
	verification runstate.Verification
}

func (e missingVerification) Error() string {
	return "the developer handed over a change without recording that it executed anything against it: " +
		strings.Join(e.verification.Owed, "; ")
}

// verificationRepairPrompt hands a change with no execution record back to the
// developer that made it. What is owed carries its own reason — a developer told
// only "record a check" on a change the checks do read has been given the rule
// without the reason it applies here, and the leeway for a change nothing checks
// is real enough to argue with — so the debt is written out as the gate stated
// it rather than restated around it.
//
// The harness contract is repeated for the reason the other repair prompts
// repeat it: it bounds the attempt whether or not the provider actually restored
// the session it was asked to resume.
func verificationRepairPrompt(invariants, scratchDirectory string, verification runstate.Verification, checks []string, limits gitworktree.DiffLimits, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract(scratchDirectory, checks, limits))
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Execution evidence: repair required\n\n")
	fmt.Fprintf(&prompt, "Your change was not handed to a reviewer, because nothing in your reply shows you ran anything against it. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over.\n\n", attempt, limit)
	prompt.WriteString("What is owed:\n\n")
	for _, owed := range verification.Owed {
		prompt.WriteString("- " + owed + "\n")
	}
	prompt.WriteString("\nRun what is missing, then reply with the verification block described in your contract, recording the commands as you actually typed them.")
	return prompt.String()
}

// blockOnMissingVerification ends a run whose repair budget was spent without
// the developer ever recording an execution. It is the third of the blockers the
// repair loop can reach, and it names the one thing a person reading it has to
// know: the change may be perfectly good, and nobody — the developer included —
// has seen it run.
func (a *activeRun) blockOnMissingVerification(missing missingVerification, limit int) error {
	cause := fmt.Errorf("the developer recorded no execution against its change after %d of %d permitted attempt(s): %s",
		a.state.RepairAttempts, limit, strings.Join(missing.verification.Owed, "; "))
	if err := a.block(renderMissingVerificationBlockerNotes(a.outcome, missing, limit)); err != nil {
		return stoppedBy(runstate.StopRepairBudget, fmt.Errorf("record the missing execution evidence as a blocker: %w", err))
	}
	return stoppedBy(runstate.StopRepairBudget, cause)
}

// renderMissingVerificationBlockerNotes describes a run that kept handing over a
// change nobody had run. Like the path refusal it names a decision rather than a
// defect: the change is preserved, and whether it works is exactly what nobody
// has established.
func renderMissingVerificationBlockerNotes(outcome Outcome, missing missingVerification, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its developer handed the change over without recording that it had executed anything against it, after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved. Nothing here says the change is wrong; what nobody has established is that it runs.",
	}
	for _, owed := range missing.verification.Owed {
		lines = append(lines, "Owed: "+owed)
	}
	return strings.Join(lines, "\n")
}

// describeVerification is what a reviewer is told about the developer's own
// executions, so a change is judged beside the evidence its author left rather
// than on the diff alone.
//
// It answers for every run rather than only for the ones that recorded
// something, for the reason the landing description does: the case worth seeing
// most is the reply that recorded nothing, and a reviewer shown only the records
// somebody wrote is shown nothing on exactly those runs. A run that reached a
// reviewer at all has passed the gate above, so an empty description here means
// the change touched nothing this project's checks read.
func describeVerification(state runstate.State) string {
	if state.Verification == nil {
		return "The developer recorded no execution of its own against this change. The change touches nothing this project's declared checks read, so the harness asked for none; judge accordingly."
	}
	if problem := strings.TrimSpace(state.Verification.Problem); problem != "" {
		return "The developer recorded its executions in a block the harness could not read: " + problem
	}
	return recordedVerification(*state.Verification).Describe()
}
