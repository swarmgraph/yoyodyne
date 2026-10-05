package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The verb carries out a decision somebody else recorded, so what it needs is
// the stoppage and the reasoning. Neither is guessed at.
func TestTriageRerunRequiresTheStoppageItActsOn(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "rerun"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "exactly one run identifier") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTriageRefusesACommandItDoesNotHave(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "requeue", "run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown triage command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// A re-run that was refused before anything was claimed is reported as a
// refusal rather than as a run that failed, and in JSON it carries the refusal
// where a script reads it.
func TestTriageRerunReportsARefusalAsJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "version: 3\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "rerun", "--config", path, "--json",
		"run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v; stdout = %q", err, stdout.String())
	}
	if result["error"] == nil || result["rerun"] != nil {
		t.Fatalf("result = %v, want the refusal and no re-run", result)
	}
}

// A carry-out that met a full harness is not a failure: nothing was claimed and
// the decision still stands, so what it reports is the state it is waiting on.
// A claim that could not be given back is the exception, because the stoppage
// has then paid for a wait that was meant to cost it nothing.
func TestTriageRerunReportsAFullHarnessAsAWaitRatherThanAFailure(t *testing.T) {
	t.Parallel()

	waiting := orchestrator.RerunResult{
		WorkItemID:   "yoyodyne-ifd.68.13",
		PriorRunID:   "run-0123456789abcdef0123456789abcdef",
		CapacityFull: &runstate.CapacityError{Limit: 2, Active: 2},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportRerun(&stdout, &stderr, false, waiting, nil); code != 0 {
		t.Fatalf("reportRerun() code = %d, want a wait reported as something other than a failure; stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"limit 2", "keeps its one re-run", "decision still stands"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}

	waiting.RecordProblem = "the claim taken for it could not be given back"
	stdout.Reset()
	stderr.Reset()
	if code := reportRerun(&stdout, &stderr, false, waiting, nil); code != 1 {
		t.Fatalf("reportRerun() code = %d, want a spent claim reported as a failure", code)
	}
	if !strings.Contains(stdout.String(), "could not be given back") {
		t.Fatalf("stdout = %q, want the spent claim named", stdout.String())
	}
}

// A carry-out whose fresh run met a pause where it would have started is the
// same kind of state: the claim was given back, so the decision stands and what
// the operator has to do is lift the pause. It is reported as the pause itself
// rather than as a refusal with nothing to name, which is what a re-run that
// started nothing and reported no error would otherwise read as.
func TestTriageRerunReportsAPauseMetBeforeStartingRatherThanAFailure(t *testing.T) {
	t.Parallel()

	held := runstate.OperatorHold{HeldAt: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)}
	paused := orchestrator.RerunResult{
		WorkItemID: "yoyodyne-ifd.68.13",
		PriorRunID: "run-0123456789abcdef0123456789abcdef",
		PausedBeforeStarting: &orchestrator.Outcome{
			WorkItemID: "yoyodyne-ifd.68.13", Paused: true, PausedByOperator: &held,
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportRerun(&stdout, &stderr, false, paused, nil); code != 0 {
		t.Fatalf("reportRerun() code = %d, want a pause reported as something other than a failure; stderr = %q", code, stderr.String())
	}
	// The accounting and the way out, since neither is any use without the other.
	for _, want := range []string{"NOT STARTED", "keeps its one re-run", "`yoyo resume`"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}

	paused.RecordProblem = "the claim taken for it could not be given back"
	stdout.Reset()
	stderr.Reset()
	if code := reportRerun(&stdout, &stderr, false, paused, nil); code != 1 {
		t.Fatalf("reportRerun() code = %d, want a spent claim reported as a failure", code)
	}
	if !strings.Contains(stdout.String(), "could not be given back") {
		t.Fatalf("stdout = %q, want the spent claim named", stdout.String())
	}
}

// The usage says what is easy to get wrong about the two verbs: the stoppage is
// re-run once, a repair continues the run instead of starting it over and
// supersedes the blocker itself, the intake hold applies to both because the
// harness is the one spending, and a harness with no free developer is a wait
// rather than a refusal.
func TestTriageUsageSaysWhatBoundsEachDecision(t *testing.T) {
	t.Parallel()

	var usage bytes.Buffer
	printTriageUsage(&usage)
	for _, want := range []string{
		"once", "intake hold", "--reason", "no free developer",
		"triage.repair_grant_attempts", "supersedes the blocker", "same developer session",
		"Precondition refusals", "reported as uncertain", "reuses recorded claims",
		"expenditure", "already served continuation reports its existing outcome",
		"success note remains pending independently of dispatch",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("usage does not mention %q:\n%s", want, usage.String())
		}
	}
	if strings.Contains(usage.String(), "Everything that refuses") {
		t.Fatalf("usage still promises every refusal costs nothing:\n%s", usage.String())
	}
}

// An override names one item, and the ceiling it puts in force is stated rather
// than inferred. Both refusals happen before anything is read or written, so a
// mistyped command costs nothing.
func TestTriageOverrideRequiresTheItemAndACeiling(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		name string
		args []string
		says string
	}{
		{
			name: "with no item named",
			args: []string{"triage", "override", "--cap", "8", "--by", "mason", "--reason", "REBUILD"},
			says: "exactly one work item identifier",
		},
		{
			name: "with neither a ceiling nor a clearing",
			args: []string{"triage", "override", "--by", "mason", "--reason", "REBUILD", "yoyodyne-ifd.143"},
			says: "requires --cap <n> or --clear",
		},
		{
			name: "with both a ceiling and a clearing",
			args: []string{"triage", "override", "--cap", "8", "--clear", "--by", "mason", "--reason", "REBUILD", "yoyodyne-ifd.143"},
			says: "give one of --cap or --clear",
		},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := Run(refused.args, &stdout, &stderr, "test"); code != 2 {
				t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), refused.says) {
				t.Fatalf("stderr = %q, want it to say %q", stderr.String(), refused.says)
			}
		})
	}
}

// A recorded override says what it changed, who decided it, and what the item now
// stands under -- and says plainly that it started nothing, because an operator
// who reads "override recorded" and expects a run is an operator who never asks
// for one.
func TestTriageOverrideReportsWhatItChangedAndThatItStartedNothing(t *testing.T) {
	t.Parallel()

	decided := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	override := runstate.TriageOverride{
		Budget:    runstate.TriageReviewRoundBudget,
		Cap:       8,
		DecidedBy: "mason",
		DecidedAt: decided,
		Reason:    "REBUILD: the rounds were spent against a base that had moved",
	}
	recorded := triageOverrideResult{
		WorkItemID: "yoyodyne-ifd.143",
		Recorded:   override,
		Caps:       runstate.TriageCaps{ReviewRounds: 8, RepairGrants: 1, Reruns: 1, MergeRearms: 2},
		Counters: runstate.TriageCounters{
			WorkItemID:   "yoyodyne-ifd.143",
			ReviewRounds: 5,
			Overrides:    []runstate.TriageOverride{override},
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportTriageOverride(&stdout, &stderr, false, recorded, nil); code != 0 {
		t.Fatalf("reportTriageOverride() code = %d; stderr = %q", code, stderr.String())
	}
	for _, want := range []string{
		"yoyodyne-ifd.143", "raised the review round cap to 8", "decided by mason",
		"5 spent across every run of this item, under the cap of 8",
		"nothing was started, granted, or spent",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}
}

// A refusal says nothing was recorded, because an operator who believes a cap was
// crossed and then finds the next decision refused is back in the deadlock this
// verb exists to end.
func TestTriageOverrideReportsARefusalAsHavingRecordedNothing(t *testing.T) {
	t.Parallel()

	refusal := runstate.TriageOverrideError{
		Budget:     runstate.TriageReviewRoundBudget,
		WorkItemID: "yoyodyne-ifd.143",
		Standing:   8,
		Asked:      8,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := reportTriageOverride(&stdout, &stderr, false,
		triageOverrideResult{WorkItemID: "yoyodyne-ifd.143"}, refusal)
	if code != 1 {
		t.Fatalf("reportTriageOverride() code = %d, want a refusal reported as one", code)
	}
	for _, want := range []string{"nothing was recorded", "already stands at 8", "yoyo status"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, is missing %q", stderr.String(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want a refusal to report nothing as done", stdout.String())
	}
}

// A refused override carries the refusal where a script reads it and no override
// beside it, exactly as a refused re-run does.
func TestTriageOverrideReportsARefusalAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := reportTriageOverride(&stdout, &stderr, true,
		triageOverrideResult{WorkItemID: "yoyodyne-ifd.143"}, errors.New("refused"))
	if code != 1 {
		t.Fatalf("reportTriageOverride() code = %d, want 1", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v; stdout = %q", err, stdout.String())
	}
	if result["error"] == nil || result["override"] != nil {
		t.Fatalf("result = %v, want the refusal and no override", result)
	}
}

// The usage says the three things about the override an operator gets wrong: it
// is theirs and nobody else's, it only ever gives more room, and it starts
// nothing.
func TestTriageUsageSaysWhatAnOverrideIsAndIsNot(t *testing.T) {
	t.Parallel()

	var usage bytes.Buffer
	printTriageUsage(&usage)
	for _, want := range []string{
		"triage override", "--by", "--clear", "--budget",
		"only thing that does", "never lowers", "carries nothing out",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("usage does not mention %q:\n%s", want, usage.String())
		}
	}
}

// The verb carries out a decision somebody else recorded, so what it needs is
// the stoppage. It is not guessed at.
func TestTriageRepairRequiresTheStoppageItActsOn(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "repair"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "exactly one run identifier") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// The verb takes the run and nothing else: the reasoning is read from the
// decision the development manager recorded, so words offered here are refused
// before anything is read, rather than recorded as hers.
func TestTriageRepairTakesNoReasoning(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "repair", "--reason", "the findings are both small",
		"run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined: -reason") {
		t.Fatalf("stderr = %q, want the reasoning flag refused", stderr.String())
	}
}

// A repair that was refused before anything was granted is reported as a
// refusal rather than as a run that failed, and in JSON it carries the refusal
// where a script reads it.
func TestTriageRepairReportsARefusalAsJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "version: 3\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "repair", "--config", path, "--json",
		"run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v; stdout = %q", err, stdout.String())
	}
	if result["error"] == nil || result["repair"] != nil {
		t.Fatalf("result = %v, want the refusal and no repair", result)
	}
}

// A carry-out that met a full harness is not a failure: nothing was spent and
// the decision still stands, so what it reports is the state it is waiting on.
// A worktree somebody has been in is the opposite — a refusal that says whose
// decision it now is.
func TestTriageRepairTellsAWaitFromARefusal(t *testing.T) {
	t.Parallel()

	waiting := orchestrator.RepairContinueResult{
		WorkItemID:   "yoyodyne-ifd.102.5",
		RunID:        "run-0123456789abcdef0123456789abcdef",
		CapacityFull: &runstate.CapacityError{Limit: 2, Active: 2},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportRepair(&stdout, &stderr, false, waiting, nil); code != 0 {
		t.Fatalf("reportRepair() code = %d, want a wait reported as something other than a failure; stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"limit 2", "keeps its repair grant", "decision still stands"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	stderr.Reset()
	refused := orchestrator.WorktreeSurgeryError{
		RunID:        "run-0123456789abcdef0123456789abcdef",
		WorktreePath: "/state/worktrees/task",
		Cause:        errors.New("worktree HEAD is 9f9f9f"),
	}
	if code := reportRepair(&stdout, &stderr, false, orchestrator.RepairContinueResult{}, refused); code != 1 {
		t.Fatalf("reportRepair() code = %d, want a refused worktree reported as a failure", code)
	}
	if !strings.Contains(stderr.String(), "still blocked") {
		t.Fatalf("stderr = %q, want it to say what the operator is left holding", stderr.String())
	}

	// The twin refusal: the worktree is the harness's own and holds none of the
	// change, so what the operator is told is where the work still is.
	stdout.Reset()
	stderr.Reset()
	empty := orchestrator.MissingPreservedChangeError{
		RunID:        "run-0123456789abcdef0123456789abcdef",
		WorktreePath: "/state/worktrees/task",
		Cause:        errors.New("/state/worktrees/task holds no change at all"),
	}
	if code := reportRepair(&stdout, &stderr, false, orchestrator.RepairContinueResult{}, empty); code != 1 {
		t.Fatalf("reportRepair() code = %d, want a handback without its change reported as a failure", code)
	}
	if !strings.Contains(stderr.String(), "the run's branch is where the preserved change is") {
		t.Fatalf("stderr = %q, want it to say where the preserved work is", stderr.String())
	}
}

func TestTriageRepairReportsAnUncertainSaveWithoutClaimingNothingWasWritten(t *testing.T) {
	t.Parallel()
	result := orchestrator.RepairContinueResult{
		WorkItemID: "yoyodyne-ifd.428.65",
		RunID:      "run-0123456789abcdef0123456789abcdef",
	}
	uncertain := errors.New("the continuation is present in the run record, but its durable save was not confirmed")
	var stdout, stderr bytes.Buffer
	if code := reportRepair(&stdout, &stderr, false, result, uncertain); code != 1 {
		t.Fatalf("reportRepair() code = %d; want an unconfirmed transition to fail", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), uncertain.Error()) || !strings.Contains(stderr.String(), "repair continuation was not confirmed") || strings.Contains(stderr.String(), "nothing was continued") {
		t.Fatalf("stdout = %q, stderr = %q; want the uncertain save reported without a success or a claim that nothing was written", stdout.String(), stderr.String())
	}
}

// The verb needs the publication it acts on, exactly as the two beside it need
// the stoppage.
func TestTriageRearmRequiresThePublicationItActsOn(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "rearm", "--reason", "the wedged check has since passed"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "exactly one run identifier") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// A re-arm is spent by recording it, which happens before the forge is asked, so
// there are three outcomes rather than two: refused before anything was spent,
// spent on a request the forge then would not take, and taken.
//
// The middle one is the one that reads wrongly if the report branches on the
// repeat alone. The publication's only re-arm is gone and no merge is pending,
// and an operator told "no merge was asked for" beside an error saying it was
// spent has been told two opposite things about the budget they are deciding
// against next.
func TestTriageRearmSeparatesARefusalFromASpentRequestTheForgeRefused(t *testing.T) {
	t.Parallel()

	refused := orchestrator.RearmResult{
		WorkItemID: "yoyodyne-ifd.68.13",
		RunID:      "run-0123456789abcdef0123456789abcdef",
		Number:     92,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportRearm(&stdout, &stderr, false, refused,
		errors.New("the merge is held by something only a person can satisfy")); code != 1 {
		t.Fatalf("reportRearm() code = %d, want a refusal reported as a failure", code)
	}
	if !strings.Contains(stderr.String(), "no merge was asked for") {
		t.Fatalf("stderr = %q, want a refusal that says nothing was asked", stderr.String())
	}

	// Spent and refused by the forge: the count is what says so, because it is
	// written before the request and stands whatever the request came to.
	spent := refused
	spent.Rearms = 1
	stdout.Reset()
	stderr.Reset()
	if code := reportRearm(&stdout, &stderr, false, spent,
		errors.New("repeat the merge request for pull request 92: the forge could not be reached")); code != 1 {
		t.Fatalf("reportRearm() code = %d, want a spent repeat reported as a failure", code)
	}
	if strings.Contains(stderr.String(), "no merge was asked for") {
		t.Fatalf("a spent re-arm was reported as one that asked for nothing: %q", stderr.String())
	}
	for _, want := range []string{"re-arm is spent", "none left", "escalation rather than another re-arm"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, is missing %q", stderr.String(), want)
		}
	}

	// A cap refusal names the once-per-publication bound, which is the evidence
	// for escalating instead.
	stdout.Reset()
	stderr.Reset()
	capped := runstate.TriageCapError{
		Action: runstate.TriageMergeRearm, WorkItemID: "yoyodyne-ifd.68.13", Publication: "publication:run-a#92",
		Refusals: []runstate.TriageCapRefusal{{Budget: runstate.TriageMergeRearmBudget, Spent: 1, Cap: 1}},
	}
	if code := reportRearm(&stdout, &stderr, false, refused, capped); code != 1 {
		t.Fatalf("reportRearm() code = %d, want a cap refusal reported as a failure", code)
	}
	if !strings.Contains(stderr.String(), "one publication's merge request once") {
		t.Fatalf("stderr = %q, want the per-publication bound named", stderr.String())
	}
}

// A repeat the forge took reports what was repeated, so an operator can check it
// against the request the verdict authorized without going to the forge.
func TestTriageRearmReportsTheRequestItRepeated(t *testing.T) {
	t.Parallel()

	taken := orchestrator.RearmResult{
		WorkItemID: "yoyodyne-ifd.68.13",
		RunID:      "run-0123456789abcdef0123456789abcdef",
		Number:     92,
		URL:        "https://forge.invalid/pull/92",
		Method:     "merge",
		HeadCommit: strings.Repeat("c", 40),
		Reason:     "the development manager's triage decided a re-arm of publication publication:run-a#92",
		Rearmed:    true,
		Queued:     true,
		Rearms:     1,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportRearm(&stdout, &stderr, false, taken, nil); code != 0 {
		t.Fatalf("reportRearm() code = %d, want a repeated request reported as success; stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"pull request 92", "merge method", strings.Repeat("c", 40), "queued again", "1 re-arm(s)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}
}
