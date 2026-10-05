package orchestrator

// The frozen behavioral baseline of the delivery pipeline.
//
// Every test beside this one states one promise and asserts exactly that. This
// one states none: it drives each delivery path end to end and writes down
// everything the path produced -- the outcome the caller was handed, the durable
// run record left on disk, the events appended to the run's log, what the run
// did to the work item, and which provider invocations it made -- and compares
// the whole of it against a recorded trace. What that buys is the thing an
// assertion cannot: a field added, dropped, or written at a different step
// changes a trace whether or not anybody thought to assert about it.
//
// It is the executable half of docs/delivery-pipeline-baseline.md. The document
// enumerates what the pipeline guarantees; the traces are the same guarantees in
// a form a later executor can be diffed against mechanically, which is what a
// parity harness needs and what a prose specification could never give it.
//
// A trace changes when behavior changes. Re-record with:
//
//	go test ./internal/orchestrator -run TestDeliveryPipelineBaseline -update-baseline
//
// and read the diff: an intended change shows as the fields it moved, and an
// unintended one shows as the fields nobody meant to move.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// updateBaseline rewrites the recorded traces instead of comparing against
// them. It is a flag rather than an environment variable so the command that
// re-records is the command that runs the test.
var updateBaseline = flag.Bool("update-baseline", false, "rewrite the golden delivery-pipeline traces under testdata/baseline")

// baselineDirectory is where the traces live, one file per scenario.
const baselineDirectory = "testdata/baseline"

func TestDeliveryPipelineBaseline(t *testing.T) {
	t.Parallel()

	for _, scenario := range baselineScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			fixture := scenario.drive(t)
			trace := fixture.trace(t, scenario)
			if refusal := baselineObservationRefusal(fixture.observable, trace.Durable); refusal != "" {
				t.Fatalf("this path was driven on the declarative default and %s.\nA trace of a run nothing observed is not this path's baseline, so it is neither compared nor recorded: fix what stopped the observation first.", refusal)
			}
			compareBaselineTrace(t, scenario.name, trace)
		})
	}
}

// TestBaselineCoversEveryRecordedScenario refuses a trace nothing drives any
// more. A recorded path whose scenario was deleted is a baseline that looks
// broader than it is, which is the one failure a golden test cannot report by
// failing to match.
func TestBaselineCoversEveryRecordedScenario(t *testing.T) {
	t.Parallel()

	recorded, err := filepath.Glob(filepath.Join(baselineDirectory, "*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	driven := map[string]bool{}
	for _, scenario := range baselineScenarios() {
		driven[scenario.name] = true
	}
	for _, path := range recorded {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if !driven[name] {
			t.Errorf("%s records a scenario nothing drives any more; delete the file or restore the scenario", path)
		}
	}
}

// baselineDocument is the enumeration these traces are the executable half of.
const baselineDocument = "../../docs/delivery-pipeline-baseline.md"

// baselineGapHeading opens the section where the document names what it states
// and no trace holds. The check below reads it as data, so moving or renaming
// the heading is a change to the check as much as to the document.
const baselineGapHeading = "## What this baseline does not yet cover"

// baselineFencedBlock is a fenced code block, which is a command to run rather
// than a claim about the pipeline. The terms register's own sweep skips these
// for the same reason.
var baselineFencedBlock = regexp.MustCompile("(?s)```.*?```")

// baselineQuoted is a span the document set in backticks, kept to one line so a
// stray backtick cannot swallow a paragraph and read as one enormous name.
var baselineQuoted = regexp.MustCompile("`([^`\n]+)`")

// baselineFieldName recognizes a durable field: lower snake_case, and no dot,
// because a dotted name is a configuration key rather than something a trace
// would carry.
var baselineFieldName = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$`)

// TestBaselineDocumentDisclosesEveryFieldNoTraceHolds holds the document to its
// own promise: that what it lists as not covered is every behavior it states and
// no trace holds, so a parity harness measuring against this baseline knows what
// it is not measuring.
//
// It exists because that promise was broken three times in a row while every
// other check was green, each time by a field the document names in one section
// and the gap list forgot -- and each time it was a reviewer reading prose who
// caught it, which is not a thing to rely on twice, let alone four times. A
// document that overstates its coverage is worse than one that claims none: it
// is the coverage a later migration would trust.
//
// The check is deliberately narrow. It recognizes the durable field names, which
// is the half that is mechanical; a behavior stated only in prose -- an ordering,
// a refusal, a step nothing writes a field for -- it cannot see, and those stay
// a reviewer's to catch. The document decides and the check only reports:
// recording a trace for a field satisfies it, and so does naming the field in
// the gap list, neither of which is a change to any code.
func TestBaselineDocumentDisclosesEveryFieldNoTraceHolds(t *testing.T) {
	t.Parallel()

	document, err := os.ReadFile(baselineDocument)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", baselineDocument, err)
	}
	undisclosed, named, err := baselineUndisclosedFields(string(document), baselineRecordedNames(t))
	if err != nil {
		t.Fatalf("%s: %v", baselineDocument, err)
	}
	for _, field := range undisclosed {
		t.Errorf("%s states %q and no trace holds it: record a trace that carries the field, or name it under %q.\nThe sentence stating it: %s",
			baselineDocument, field.name, baselineGapHeading, field.sentence)
	}
	// A document naming no fields at all would pass every assertion above while
	// measuring nothing, which is the way this check could quietly stop working.
	if named == 0 {
		t.Errorf("%s named no durable fields, so this check compared nothing", baselineDocument)
	}
}

// TestBaselineDisclosureCheckNamesTheFieldAndTheSentence holds the check above to
// the failure it exists to report, on a document written for the purpose: a
// field the traces do not carry and the gap list does not name is reported with
// the sentence that states it, and one the gap list names, one a trace carries,
// and one only a command mentions are not.
func TestBaselineDisclosureCheckNamesTheFieldAndTheSentence(t *testing.T) {
	t.Parallel()

	document := strings.Join([]string{
		"# A baseline",
		"",
		"Every run writes `recorded_field` first. Then it writes `undisclosed_field` at the",
		"end, which nothing records.",
		"",
		"| Phase | What it writes |",
		"| --- | --- |",
		"| `developing` | `recorded_field`, `table_field` |",
		"",
		"- A list item naming `disclosed_field` and not.",
		"",
		"```sh",
		"go test -run `command_field`",
		"```",
		"",
		baselineGapHeading,
		"",
		"- `disclosed_field`, which no trace holds.",
	}, "\n")
	held := map[string]bool{"recorded_field": true}

	undisclosed, named, err := baselineUndisclosedFields(document, held)
	if err != nil {
		t.Fatalf("baselineUndisclosedFields() error = %v", err)
	}
	want := []baselineUndisclosedField{
		{name: "undisclosed_field", sentence: "Then it writes `undisclosed_field` at the end, which nothing records."},
		{name: "table_field", sentence: "| `developing` | `recorded_field`, `table_field` |"},
	}
	if len(undisclosed) != len(want) {
		t.Fatalf("undisclosed = %+v, want %+v", undisclosed, want)
	}
	for i := range want {
		if undisclosed[i] != want[i] {
			t.Errorf("undisclosed[%d] = %+v, want %+v", i, undisclosed[i], want[i])
		}
	}
	if named != 4 {
		t.Errorf("named = %d, want 4: recorded, undisclosed, table, and disclosed, and not the one only a command mentions", named)
	}

	if _, _, err := baselineUndisclosedFields("no gap list here", held); err == nil {
		t.Errorf("a document without %q was accepted", baselineGapHeading)
	}
}

// baselineUndisclosedField is one durable field the document states that no trace
// carries and the gap list does not name, with the sentence it was first stated in
// so whoever reads the failure can find the claim without searching for it.
type baselineUndisclosedField struct {
	name     string
	sentence string
}

// baselineUndisclosedFields compares the durable field names a baseline document
// states above its gap list against the names the traces hold and the gap list
// itself, and returns those neither accounts for in the order the document first
// states them. named is how many distinct field names it compared.
func baselineUndisclosedFields(document string, held map[string]bool) (undisclosed []baselineUndisclosedField, named int, err error) {
	prose := baselineFencedBlock.ReplaceAllString(document, "")
	body, gaps, found := strings.Cut(prose, baselineGapHeading)
	if !found {
		return nil, 0, fmt.Errorf("carries no %q section, which is where it says what it does not measure", baselineGapHeading)
	}
	seen := map[string]bool{}
	for _, quoted := range baselineQuoted.FindAllStringSubmatchIndex(body, -1) {
		name := body[quoted[2]:quoted[3]]
		if !baselineFieldName.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		if held[name] || strings.Contains(gaps, name) {
			continue
		}
		undisclosed = append(undisclosed, baselineUndisclosedField{name: name, sentence: baselineSentence(body, quoted[0], quoted[1])})
	}
	return undisclosed, len(seen), nil
}

// baselineSentenceEnd is a full stop, question mark, or exclamation mark followed
// by whitespace. A dot inside a name -- `execution.check_timeout`, `baseline_test.go`
// -- is followed by more of the name and does not end anything.
var baselineSentenceEnd = regexp.MustCompile(`[.?!]\s`)

// baselineSentence is the sentence of body that contains the span [start, end),
// on one line. A table row is its own sentence, since a cell read without its row
// says nothing about which phase or scenario it belongs to; otherwise a sentence
// is bounded by the paragraph, a list item, or a sentence end.
func baselineSentence(body string, start, end int) string {
	lineStart := strings.LastIndex(body[:start], "\n") + 1
	lineEnd := len(body)
	if at := strings.Index(body[end:], "\n"); at >= 0 {
		lineEnd = end + at
	}
	if strings.HasPrefix(strings.TrimSpace(body[lineStart:lineEnd]), "|") {
		return strings.TrimSpace(body[lineStart:lineEnd])
	}

	from := 0
	if at := strings.LastIndex(body[:start], "\n\n"); at >= 0 {
		from = at + 2
	}
	if at := strings.LastIndex(body[:start], "\n- "); at >= 0 && at+3 > from {
		from = at + 3
	}
	if ends := baselineSentenceEnd.FindAllStringIndex(body[from:start], -1); len(ends) > 0 {
		from += ends[len(ends)-1][1]
	}

	to := len(body)
	if at := strings.Index(body[end:], "\n\n"); at >= 0 {
		to = end + at
	}
	if at := strings.Index(body[end:], "\n- "); at >= 0 && end+at < to {
		to = end + at
	}
	if at := baselineSentenceEnd.FindStringIndex(body[end:to]); at != nil {
		to = end + at[0] + 1
	}
	return strings.Join(strings.Fields(body[from:to]), " ")
}

// The baseline's representative gate and bound endings remain pinned by the
// field's values, not by words that might also occur in its prose. The exhaustive
// terminal-write test covers storage of the full vocabulary; producer scenarios
// separately reload the causes recorded by real stopping paths.
func TestBaselineTracesRepresentativeStopClasses(t *testing.T) {
	t.Parallel()

	recorded, err := filepath.Glob(filepath.Join(baselineDirectory, "*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	traced := map[string]bool{}
	for _, path := range recorded {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		var trace any
		if err := json.Unmarshal(content, &trace); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", path, err)
		}
		baselineCollectField(trace, "stop_class", traced)
	}
	for _, class := range []runstate.StopClass{
		runstate.StopRepairBudget, runstate.StopIntegrationBudget, runstate.StopPublish,
		runstate.StopCleanup, runstate.StopRecording, runstate.StopRelaunchBudget,
		runstate.StopOperator, runstate.StopHarness, runstate.CauseSandboxSpawnFailure.StopClass(),
	} {
		if !traced[string(class)] {
			t.Errorf("no baseline trace records stop_class %q; drive a scenario that stops there", class)
		}
	}
}

// baselineCollectField is every string value a trace holds under one key,
// wherever in the trace that key appears.
func baselineCollectField(value any, field string, into map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if text, ok := nested.(string); ok && key == field {
				into[text] = true
			}
			baselineCollectField(nested, field, into)
		}
	case []any:
		for _, nested := range typed {
			baselineCollectField(nested, field, into)
		}
	}
}

// baselineRecordedNames is every key and every string value in every recorded
// trace. Values count as well as keys because what the document names is not
// always a field -- `provider_stop` and `timed_out` are values a field carries --
// and a parity harness diffing the document field by field would find them
// either way.
func baselineRecordedNames(t *testing.T) map[string]bool {
	t.Helper()

	recorded, err := filepath.Glob(filepath.Join(baselineDirectory, "*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	names := map[string]bool{}
	for _, path := range recorded {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		var trace any
		if err := json.Unmarshal(content, &trace); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", path, err)
		}
		baselineCollectNames(trace, names)
	}
	return names
}

func baselineCollectNames(value any, into map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			into[key] = true
			baselineCollectNames(nested, into)
		}
	case []any:
		for _, nested := range typed {
			baselineCollectNames(nested, into)
		}
	case string:
		into[typed] = true
	}
}

// baselineScenario is one delivery path, driven end to end. freezes is the
// sentence the trace beneath it stands for, recorded with the trace so a
// reviewer reading the file knows what it is supposed to be evidence of.
type baselineScenario struct {
	name    string
	freezes string
	drive   func(t *testing.T) *baselineFixture
}

func baselineScenarios() []baselineScenario {
	return []baselineScenario{
		{
			name:    "human-approved-change-is-preserved-for-its-approver",
			freezes: "A project whose integration a person approves runs the developer and the checks, records the outcome on the item, and stops with the change preserved on its branch: nothing is reviewed, promoted, closed, or cleaned up.",
			drive:   baselineHumanApproved,
		},
		{
			name:    "automatic-run-promotes-reviews-closes-and-cleans-up",
			freezes: "An automatic project develops, checks, obtains an independent approving verdict, promotes onto the target branch, closes the item, and removes both the worktree and the branch.",
			drive:   baselineAutomaticPromotion,
		},
		{
			name:    "failing-check-is-repaired-and-then-promoted",
			freezes: "A configured check that fails goes back to the same developer as one repair attempt, and the attempt that passes is reviewed and promoted; the failing check is durable while it is outstanding and cleared once it passes.",
			drive:   baselineCheckRepaired,
		},
		{
			name:    "failing-check-spends-the-repair-budget-and-blocks",
			freezes: "A check that keeps failing spends execution.repair_attempts_before_replan and ends the run blocked, with the failing check on the record, the item blocked rather than closed, and the worktree and branch preserved.",
			drive:   baselineCheckBudgetSpent,
		},
		{
			name:    "review-findings-are-repaired-and-then-promoted",
			freezes: "A reviewer's repair verdict returns its findings to the same developer on the shared repair budget, and the next attempt is reviewed again by its own invocation before anything is promoted.",
			drive:   baselineReviewRepaired,
		},
		{
			name:    "review-findings-spend-the-repair-budget-and-block",
			freezes: "A change no verdict approves spends the repair budget and ends blocked with the unresolved findings recorded, and nothing is promoted or closed.",
			drive:   baselineReviewBudgetSpent,
		},
		{
			name:    "protected-path-refusal-is-repaired-before-any-check-runs",
			freezes: "A change touching an upstream artifact home the item never granted is refused before the checks and before the reviewer, handed back on the same repair budget, and promoted once the developer takes the path back out.",
			drive:   baselineProtectedPathRepaired,
		},
		{
			name:    "protected-path-refusal-spends-the-repair-budget-and-blocks",
			freezes: "A change that keeps touching upstream artifact homes spends the same repair budget a failing check spends and ends blocked, with the refusal durable while it is outstanding: the refused paths in their sorted order, and a blocker naming what was spent. An item granting nothing records no grants beside them, which is what the granted scenario records the other half of.",
			drive:   baselineProtectedPathBudgetSpent,
		},
		{
			name:    "protected-path-grant-admits-the-change-it-names",
			freezes: "A work item granting a protected path on its own text admits exactly that path: the same change that would be refused without the grant passes the gate and is promoted.",
			drive:   baselineProtectedPathGranted,
		},
		{
			name:    "usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it",
			freezes: "An exhausted provider usage limit parks the run on the deadline the provider named, exits without making it terminal, and a later invocation past that deadline resumes the same run, worktree, branch, and developer session and finishes it.",
			drive:   baselineUsageLimitPause,
		},
		{
			name:    "provider-stopped-on-time-is-resumable-and-continues-the-same-attempt",
			freezes: "An invocation the harness stopped on time -- stalled, or out of its total budget -- is recorded as a stop rather than a failure by the developer: the run stays in flight, the partial work stays in the worktree, and a later invocation continues the same session immediately.",
			drive:   baselineProviderStop,
		},
		{
			name:    "operator-stop-cancels-the-run-and-preserves-its-work",
			freezes: "A stop the operator asked for ends the run at its next provider-call boundary as cancelled rather than failed, before a verdict is bought, and leaves the worktree and branch for reconciliation to settle.",
			drive:   baselineOperatorStop,
		},
		{
			name:    "verdict-fields-the-schema-does-not-name-are-recorded-as-drift",
			freezes: "A reviewer that answers with a field the verdict schema does not define is acted on without it and the drift is recorded as its own event: the run integrates, and nothing the schema does not define reaches anything that acts on a verdict.",
			drive:   baselineReviewDrift,
		},
		{
			name:    "partial-cleanup-leaves-a-succeeded-run-reporting-what-survives",
			freezes: "Cleanup that removed one artifact and not the other leaves the run succeeded and the item closed, reports each artifact separately, and names only the one that actually survives.",
			drive:   baselinePartialCleanup,
		},
		{
			name:    "outstanding-publication-leaves-a-succeeded-run-naming-it",
			freezes: "A promotion the forge could not confirm is an outstanding publication rather than a failed run: the run succeeds, the item closes, and the record names the publication as what the run stopped short of, in stop_class publish beside the failure it names.",
			drive:   baselineOutstandingPublication,
		},
		{
			name:    "completion-record-that-arrives-late-says-so",
			freezes: "A run whose artifacts were all removed and whose terminal record the store refused twice stays succeeded with nothing reported outstanding, and the late write that lands carries why it was late in completion_recording_failure and stop_class recording.",
			drive:   baselineCompletionRecordedLate,
		},
		{
			name:    "environment-refusing-the-probe-ends-the-run-as-outside-the-work",
			freezes: "A developer that could not start its probe at all ends the run on its first reply, before the checks and the reviewer, with the round recorded as refused from outside the work and stop_class sandbox-spawn-failure rather than as anything the change did.",
			drive:   baselineEnvironmentRefusedProbe,
		},
		{
			name:    "server-overload-pause-reissues-the-same-attempt",
			freezes: "A transiently overloaded provider parks the run on the configured overload interval rather than an exhausted account, and the reissued attempt continues the session the refused one established.",
			drive:   baselineServerOverloadPause,
		},
		{
			name:    "transient-provider-death-relaunches-without-charging-the-developer",
			freezes: "A provider invocation that died without judging the work is relaunched against its own budget: the run spends a relaunch and no repair attempt, and finishes normally.",
			drive:   baselineTransientRelaunch,
		},
		{
			name:    "transient-deaths-spend-the-relaunch-budget-and-block",
			freezes: "A provider that keeps dying of something the harness cannot classify spends execution.transient_relaunches_before_blocking and ends blocked on the provider rather than on the change, with the work preserved.",
			drive:   baselineRelaunchBudgetSpent,
		},
		{
			name:    "recoverable-death-carries-on-past-the-relaunch-budget",
			freezes: "A provider death that is plainly a dropped connection is waited out on a Fibonacci backoff past the spent relaunch budget rather than blocking the item, and each wait is recorded on the run with the boundary and the interval.",
			drive:   baselineRecoverableDeathCarriesOn,
		},
		{
			name:    "unresolved-directive-pauses-the-work-before-anything-is-claimed",
			freezes: "An unresolved directive that pauses this item stops the work before it is claimed: no run is reserved, no worktree is cut, and the outcome names the directive somebody has to settle.",
			drive:   baselineDirectivePause,
		},
		{
			name:    "unfinished-dependency-pauses-the-work-before-anything-is-claimed",
			freezes: "An item waiting on unfinished work is paused before it is claimed and the outcome names the blocking items; a parent-child link is not a blocker.",
			drive:   baselineDependencyPause,
		},
		{
			name:    "operator-hold-starts-nothing-at-all",
			freezes: "The operator's hold on harness activity is read before anything else: nothing is claimed, no run is reserved, and the provider is not asked so much as whether it is installed.",
			drive:   baselineOperatorHold,
		},
		{
			name:    "operator-hold-parks-a-claimed-run-and-accounts-for-what-it-cost",
			freezes: "A hold placed after the claim reaches the run at its next provider call rather than stopping it where it stands: the claim, worktree, branch, and session are preserved across the park, lifting the hold is all it takes to carry on, and the waiting is accounted for in operator_held_seconds rather than charged to any budget.",
			drive:   baselineHeldClaimedRun,
		},
		{
			name:    "intake-hold-starts-nothing-the-harness-chose",
			freezes: "A hold on intake stops work the harness chose for itself before a run is reserved, and the outcome names the hold rather than a run.",
			drive:   baselineIntakeHold,
		},
		{
			name:    "promotion-is-replayed-when-the-target-branch-moves",
			freezes: "A promotion that lost its race is replayed onto where the target went and re-earns the whole gate -- the checks run again and a fresh independent verdict is obtained -- without spending a repair attempt.",
			drive:   baselineReplayedPromotion,
		},
		{
			name:    "a-replay-that-stops-on-the-change-spends-the-integration-budget",
			freezes: "A lost race costs nothing; what spends execution.integration_retries_before_reconciliation is a replay that stops on the change, and at 0 the replayed change drawing a repair verdict blocks the run there with nothing promoted and the replayed change preserved.",
			drive:   baselineIntegrationBudgetSpent,
		},
		{
			name:    "reconciliation-completes-a-run-interrupted-inside-integration",
			freezes: "A process killed after the promotion landed and before it was recorded is settled by the sweep from the repository rather than from the run's record: the item closes and the artifacts are removed, and no developer is restarted.",
			drive:   baselineReconcileCompleted,
		},
		{
			name:    "reconciliation-blocks-a-run-interrupted-while-developing",
			freezes: "A process killed before anything was promoted is settled as a blocker with the work preserved, and a second sweep finds nothing outstanding.",
			drive:   baselineReconcileBlocked,
		},
	}
}

// baselineFixture is one scenario's world: the repository the run promotes
// into, the durable stores it writes to, the work item it serves, and the
// invocations it made. Everything the trace is built from is read back out of
// these rather than remembered as the scenario goes, so what is recorded is what
// survived rather than what the scenario believed.
type baselineFixture struct {
	repository   string
	worktreeRoot string
	store        *runstate.Store
	tracker      recordingTracker
	providers    []recordingBackend
	steps        []baselineStep
	reconciled   []Reconciliation
	// reserved says a run record exists, and observable says the invocation that
	// reserved it was one the declarative path observes. Both are read off what
	// the scenario actually drove rather than assumed of every scenario, because
	// the two that matter here are told apart by nothing else: a rolled-back run
	// carries no instance legitimately, and a run that was to be observed and
	// carries none is a run nothing watched.
	reserved   bool
	observable bool
	// masked are values a scenario mints at random -- a directive identifier and
	// nothing else so far -- which are replaced by a stable placeholder so the
	// trace is the behavior rather than the identifier.
	masked []baselineMask
}

// baselineStep is one invocation of the pipeline. A scenario that pauses and is
// resumed makes two of them, which is what lets the trace state that the second
// continued the first rather than starting anything.
type baselineStep struct {
	name    string
	outcome Outcome
	err     error
}

type baselineMask struct {
	actual      string
	placeholder string
}

func newBaselineFixture(t *testing.T, item beads.WorkItem) *baselineFixture {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	return &baselineFixture{
		repository:   pipelineRepository(t),
		worktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		store:        store,
		tracker:      &orchestratortest.Tracker{Item: item},
	}
}

// baselineItem is the ordinary work item every scenario that needs nothing
// special serves.
func baselineItem() beads.WorkItem {
	return beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}
}

// implements is the developer every scenario shares unless it needs another: it
// writes one file into its worktree and says so.
func baselineImplements(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// pipeline builds a pipeline over this fixture's own repository and stores. Every
// provider it is handed is remembered, so the trace can report the invocations of
// a scenario that used more than one.
func (f *baselineFixture) pipeline(t *testing.T, provider recordingBackend, commands []string) Pipeline {
	t.Helper()
	return f.pipelineOver(t, f.store, provider, commands)
}

// pipelineOver is the same over a store the scenario wraps, which is how an
// interrupted process is driven: the run writes through the wrapper and what
// survives is read back from the real store underneath it.
func (f *baselineFixture) pipelineOver(t *testing.T, store StateStore, provider recordingBackend, commands []string) Pipeline {
	t.Helper()
	f.providers = append(f.providers, provider)
	pipeline := newSharedPipeline(t, f.repository, f.worktreeRoot, store, f.tracker, provider, commands)
	// Instances go to the store itself rather than through whatever the scenario
	// wrapped it in: a scenario that wraps its store is modelling a process that
	// stopped writing its run record, and what such a process leaves behind is an
	// instance standing wherever it had got to.
	pipeline.Instances = f.store
	return pipeline
}

// automatic is the same pipeline with the reviewer wired and integration taken
// by the harness, which is what all but the human-approval scenario runs under.
func (f *baselineFixture) automatic(t *testing.T, provider recordingBackend, commands []string) Pipeline {
	t.Helper()
	return automatic(f.pipeline(t, provider, commands), provider)
}

// invoke runs the pipeline once and records what it returned, whichever way it
// went. A failure is part of the trace rather than a reason to stop building it.
func (f *baselineFixture) invoke(t *testing.T, name string, pipeline Pipeline) Outcome {
	t.Helper()
	outcome, err := pipeline.Run(context.Background(), f.tracker.Record().Item.ID)
	f.steps = append(f.steps, baselineStep{name: name, outcome: outcome, err: err})
	f.noteObservable(pipeline)
	return outcome
}

// noteObservable remembers whether the invocation that reserved this scenario's
// run was one the declarative path observes.
//
// It is the reserving invocation that decides, and not the last one: whether a
// run is observed is settled once, when the run is created, so a scenario whose
// later invocations are configured differently is still a run of whatever the
// first of them was.
func (f *baselineFixture) noteObservable(pipeline Pipeline) {
	if f.reserved {
		return
	}
	if _, err := f.store.Load(pipelineRunID); err != nil {
		return
	}
	f.reserved = true
	f.observable = pipeline.Config.Execution.DeclarativeDelivery && pipeline.Instances != nil
}

// mask replaces a value the scenario could not choose with a stable placeholder.
func (f *baselineFixture) mask(actual, placeholder string) {
	if strings.TrimSpace(actual) == "" {
		return
	}
	f.masked = append(f.masked, baselineMask{actual: actual, placeholder: placeholder})
}

// maskInstant gives one moment the scenario chose a placeholder of its own,
// ahead of the one every other timestamp collapses into. It is how a trace
// states that a run recorded the instant it was given rather than an instant:
// the deadline a paused run is waiting on is the whole of what the pause
// promises, and a placeholder shared with `started_at` would hold nothing about
// it. Both renderings are registered because a whole second marshals without a
// fractional part and anything else keeps one.
func (f *baselineFixture) maskInstant(instant time.Time, placeholder string) {
	f.mask(instant.UTC().Format(time.RFC3339Nano), placeholder)
	f.mask(instant.UTC().Format(time.RFC3339), placeholder)
}

// sweep reconciles everything still outstanding and records what the sweep
// decided, which is the terminal half of the paths a killed process leaves.
func (f *baselineFixture) sweep(t *testing.T, label string) []Reconciliation {
	t.Helper()
	results := reconcileSweep(t, f.repository, f.worktreeRoot, f.store, f.tracker)
	f.reconciled = append(f.reconciled, results...)
	if len(results) == 0 {
		// A sweep that settled nothing is itself evidence -- it is what a second
		// sweep must report -- so it is recorded rather than dropped.
		f.reconciled = append(f.reconciled, Reconciliation{RunID: label, Action: "nothing outstanding"})
	}
	return results
}

//
// The scenarios.
//

func baselineHumanApproved(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	fixture.invoke(t, "run", fixture.pipeline(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineAutomaticPromotion(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineCheckRepaired(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	attempts := 0
	// The first attempt leaves the check failing; the repair is what makes it
	// pass, so the trace carries one attempt of each kind.
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return baselineImplements(request)
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "repaired.txt"), []byte("repaired\n"), 0o600)
	}, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f repaired.txt"}))
	return fixture
}

func baselineCheckBudgetSpent(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"exit 1"}))
	return fixture
}

func baselineReviewRepaired(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, repairVerdict, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineReviewBudgetSpent(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, repairVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineProtectedPathRepaired(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := baselineImplements(request); err != nil {
			return err
		}
		// The first attempt rewrites the product's own brief; the repair takes it
		// back out, which is the only thing that gets the change to a reviewer.
		if attempts == 1 {
			return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the product is whatever this run needed it to be\n")
		}
		return os.RemoveAll(filepath.Join(request.WorkingDirectory, "docs", "product"))
	}, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

// baselineProtectedPathBudgetSpent is the refusal reached the other way: a
// developer that never takes the path back out. It exists beside the repaired
// scenario because that one ends with the refusal cleared, so nothing there
// records what a refusal looks like while it is outstanding -- the paths, what
// the item did grant beside them, and the blocker the item is left carrying.
func baselineProtectedPathBudgetSpent(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := baselineImplements(request); err != nil {
			return err
		}
		// Two upstream homes on every attempt, so the recorded refusal is a list
		// in its sorted order rather than a single path that cannot show one.
		if err := writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the product is whatever this run needed it to be\n"); err != nil {
			return err
		}
		return writeUpstream(t, request.WorkingDirectory, "docs/designs/delivery.md", "and so is the design\n")
	}, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineProtectedPathGranted(t *testing.T) *baselineFixture {
	item := baselineItem()
	item.Description = "Correct the brief's own account of the pipeline.\n\n" +
		protectedpath.GrantMarker + " docs/product/brief.md\n"
	fixture := newBaselineFixture(t, item)
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := baselineImplements(request); err != nil {
			return err
		}
		return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the corrected account\n")
	}, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineUsageLimitPause(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	// The deadline gets a placeholder of its own rather than the one every
	// timestamp shares, so what the trace freezes is that the run recorded the
	// instant the provider named and not merely that it recorded some instant.
	fixture.maskInstant(resetsAt, "<usage-limit-reset>")

	// The first invocation is refused for want of capacity and the wait is longer
	// than this process will hold open, so it exits with the run still in flight.
	refused := usageLimitBackend(1, limit, approveVerdict)
	pausing := waiting(fixture.automatic(t, refused, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	fixture.invoke(t, "paused invocation", pausing)

	// The second is made past the deadline and continues the same run.
	served := usageLimitBackend(0, limit, approveVerdict)
	resuming := waiting(fixture.automatic(t, served, []string{"test -f feature.txt"}),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	fixture.invoke(t, "resumed invocation", resuming)
	return fixture
}

func baselineProviderStop(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	// The first invocation stops the developer for stalling, after it has already
	// written part of its change.
	stopped := providerStopBackend(1, execution.ProcessStalled, approveVerdict)
	fixture.invoke(t, "stopped invocation", fixture.automatic(t, stopped, []string{"test -f feature.txt"}))

	// The second is owed the rest of that attempt and takes it immediately: there
	// is no deadline to wait out.
	served := providerStopBackend(0, execution.ProcessStalled, approveVerdict)
	fixture.invoke(t, "continued invocation", fixture.automatic(t, served, []string{"test -f feature.txt"}))
	return fixture
}

func baselineOperatorStop(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	// The operator stops the run mid-attempt, from a process holding nothing:
	// this is the file they write beside the run.
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := fixture.store.RecordStop(runstate.StopRequest{
			SchemaVersion: runstate.StopSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         request.RunID,
			WorkItemID:    fixture.tracker.Record().Item.ID,
			RequestedAt:   baseTime,
			Reason:        "it is rewriting the wrong file",
		}); err != nil {
			return err
		}
		return baselineImplements(request)
	}, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineReviewDrift(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements,
		`{"decision":"approve","approves":"implementation","summary":"the change matches the acceptance criteria","severity_note":"no blocking issues found"}`)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselinePartialCleanup(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	// The worktree is removed and the branch deletion fails, which is exactly what
	// an interrupted two-step removal leaves behind.
	pipeline.Worktrees = &hookedWorktrees{
		WorktreeManager: pipeline.Worktrees,
		cleanup: func(gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
			return gitworktree.Cleanup{WorktreeRemoved: true}, errors.New("branch is checked out elsewhere")
		},
	}
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineOutstandingPublication(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	remote := addBareRemote(t, fixture.repository)
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	// The forge takes the branch and the pull request and then cannot say what
	// became of the merge, which is the publication left outstanding.
	forge := &orchestratortest.Forge{Remote: remote, StateErr: errors.New("the forge is unreachable")}
	fixture.invoke(t, "run", publishing(fixture.automatic(t, provider, []string{"test -f feature.txt"}), forge))
	return fixture
}

func baselineCompletionRecordedLate(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	// The terminal write and its retry are refused, and the late write after them
	// lands, so what survives is the record that says it arrived late.
	refusing := &briefOutageStore{StateStore: fixture.store, at: runstate.PhaseComplete, refusals: 2}
	fixture.invoke(t, "run", automatic(fixture.pipelineOver(t, refusing, provider, []string{"test -f feature.txt"}), provider))
	if refusing.refusals != 0 {
		t.Fatal("the scenario never refused the terminal record it was driving")
	}
	return fixture
}

func baselineEnvironmentRefusedProbe(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	provider.DeveloperFinalText = "I cannot run anything in this worktree.\n\n" +
		verificationBlock(`{"probe":{"command":"make build","outcome":"refused","detail":"could not start /bin/sh: operation not permitted"}}`)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

// briefOutageStore refuses a fixed number of saves of a state at one phase and
// takes every save after them, which is a store that was briefly unavailable
// rather than one that went away.
type briefOutageStore struct {
	StateStore
	at       runstate.Phase
	refusals int
}

func (s *briefOutageStore) Save(state runstate.State) error {
	if state.Phase == s.at && s.refusals > 0 {
		s.refusals--
		return errors.New("state store is unavailable")
	}
	return s.StateStore.Save(state)
}

func baselineServerOverloadPause(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := serverOverloadBackend(1, approveVerdict)
	// The whole wait is taken inside this process, so the run pauses and finishes
	// in one invocation rather than exiting resumable.
	pipeline := waiting(fixture.automatic(t, provider, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineTransientRelaunch(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := transientDeathBackend(1, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineRelaunchBudgetSpent(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	// More deaths than the budget can pay for, and of something the harness
	// cannot classify, so what stops the run is the budget rather than the
	// provider recovering. A death that is plainly a dropped connection carries
	// on past this budget instead; that is the trace below.
	provider := opaqueDeathBackend(10, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineRecoverableDeathCarriesOn(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	// Four deaths against a budget of two, all of them a dropped connection. The
	// first two are relaunches; the two after them are what this trace is for,
	// waited out on the backoff and recorded with their intervals, and the fifth
	// invocation serves the work.
	provider := transientDeathBackend(4, approveVerdict)
	pipeline := waiting(fixture.automatic(t, provider, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineDirectivePause(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	directives := newDirectiveStore(t)
	held := pausingDirective(t, directives, directive.KindArtifact, nil)
	fixture.mask(held.ID, "<directive-id>")
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.Directives = directives
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineDependencyPause(t *testing.T) *baselineFixture {
	item := baselineItem()
	item.Dependencies = []beads.Dependency{
		{ID: "yoyodyne-blocker", Type: "blocks", Status: "open"},
		{ID: "yoyodyne-parent", Type: "parent-child", Status: "open"},
	}
	fixture := newBaselineFixture(t, item)
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineOperatorHold(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	holds := newOperatorHoldStore(t)
	if _, err := holds.Hold(baseTime); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.Holds = holds
	fixture.invoke(t, "run", pipeline)
	return fixture
}

// baselineHeldClaimedRun is the hold arriving too late to stop anything: the
// developer is already working, so what the hold reaches is a run with a claim,
// a worktree, a branch, and a session. The scenario beside it records the hold
// read before the claim, where there is no run at all; this one records the same
// hold read at a provider-call boundary, and what the wait cost the run.
func baselineHeldClaimedRun(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	holds := newOperatorHoldStore(t)
	// The hold is placed from inside the developer's own invocation, which is the
	// case the boundary exists for: what is already streaming is not interrupted,
	// and the next provider call is where the run notices.
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return baselineImplements(request)
	}, approveVerdict)
	clock := &pausingClock{now: baseTime}
	// Lifting it while the run is asleep on it is what makes the held span a
	// number rather than a wait this test would have to sit through. The clock
	// advances by exactly what it slept, so the seconds the run accounts for are
	// the same on every machine.
	clock.onSleep = func() {
		if _, _, err := holds.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}
	pipeline := waiting(fixture.automatic(t, provider, []string{"test -f feature.txt"}),
		clock, 6*time.Hour, time.Minute)
	pipeline.Holds = holds
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineIntakeHold(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	intake := newIntakeHoldStore(t)
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere wrong", baseTime); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	// A pipeline that accounts for no choice is treated as the harness choosing,
	// which is exactly the work an intake hold stops.
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.Intake = intake
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineReplayedPromotion(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	developed := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := baselineImplements(request); err != nil {
			return err
		}
		// The target moves once, after this run's worktree was cut from it, which
		// is exactly the window a losing promotion opens.
		developed++
		if developed > 1 {
			return nil
		}
		writePipelineFile(t, fixture.repository, "elsewhere.txt", "somebody else's work\n")
		runPipelineGit(t, fixture.repository, "add", "elsewhere.txt")
		runPipelineGit(t, fixture.repository, "commit", "-m", "concurrent target change")
		return nil
	}, approveVerdict)
	fixture.invoke(t, "run", fixture.automatic(t, provider, []string{"test -f feature.txt"}))
	return fixture
}

func baselineIntegrationBudgetSpent(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := baselineImplements(request); err != nil {
			return err
		}
		writePipelineFile(t, fixture.repository, "elsewhere.txt", "somebody else's work\n")
		runPipelineGit(t, fixture.repository, "add", "elsewhere.txt")
		runPipelineGit(t, fixture.repository, "commit", "-m", "concurrent target change")
		return nil
	}, approveVerdict, repairVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 0
	fixture.invoke(t, "run", pipeline)
	return fixture
}

func baselineReconcileCompleted(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	// Writes stop for good once the run reaches the completing phase, so the
	// promotion landed and nothing recorded it.
	halting := &haltingStore{StateStore: fixture.store, at: runstate.PhaseCompleting}
	fixture.invoke(t, "interrupted invocation", automatic(fixture.pipelineOver(t, halting, provider, []string{"test -f feature.txt"}), provider))
	if !halting.halted {
		t.Fatal("the scenario never interrupted the run it was driving")
	}
	fixture.sweep(t, "first sweep")
	fixture.sweep(t, "second sweep")
	return fixture
}

func baselineReconcileBlocked(t *testing.T) *baselineFixture {
	fixture := newBaselineFixture(t, baselineItem())
	provider := orchestratortest.RoleBackend(baselineImplements, approveVerdict)
	// Writes stop while the developer's work is still the only thing that
	// happened, so nothing was promoted and nothing can be recovered from the
	// repository.
	halting := &haltingStore{StateStore: fixture.store, at: runstate.PhaseChecking}
	fixture.invoke(t, "interrupted invocation", automatic(fixture.pipelineOver(t, halting, provider, []string{"test -f feature.txt"}), provider))
	if !halting.halted {
		t.Fatal("the scenario never interrupted the run it was driving")
	}
	fixture.sweep(t, "first sweep")
	fixture.sweep(t, "second sweep")
	return fixture
}

//
// The trace.
//

// baselineTrace is everything one delivery path produced, in the shape a later
// executor can be compared against field by field.
type baselineTrace struct {
	Scenario string `json:"scenario"`
	Freezes  string `json:"freezes"`
	// Steps are the pipeline invocations the scenario made, in order.
	Steps []baselineTracedStep `json:"steps"`
	// Reconciliations are what a sweep decided, on the scenarios that ran one.
	Reconciliations []map[string]any `json:"reconciliations,omitempty"`
	// Durable is the run record left on disk, which is what a later invocation
	// and a reconciler both read. It is absent where no run was ever reserved.
	Durable map[string]any `json:"durable_run_record"`
	// Events is the run's event log, consecutive repeats collapsed.
	Events []string `json:"events"`
	// WorkItem is what the run did to the item it served.
	WorkItem baselineTracedItem `json:"work_item"`
	// Provider is every invocation the run made, in order.
	Provider []string `json:"provider_invocations"`
}

type baselineTracedStep struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
	// Ending is the outcome word the run's status reads as, recorded beside the
	// status rather than instead of it. It is derived rather than durable, so a
	// trace carrying only the status would freeze "failed" for both a run handed
	// back to a person with its work intact and one that broke with nothing to
	// show -- which is the whole distinction this word exists to draw, and the
	// one an executor could get wrong without moving any recorded field.
	Ending  runstate.RunOutcome `json:"ending"`
	Outcome map[string]any      `json:"outcome"`
}

// baselineTracedItem is the durable side effect on the work item: which tracker
// calls were made and in what order, where the item ended up, and the words the
// run left on it.
type baselineTracedItem struct {
	Calls   []string `json:"calls"`
	Status  string   `json:"status"`
	Closed  bool     `json:"closed"`
	Blocked bool     `json:"blocked"`
	// Notes are the accounts the run wrote onto the item, one entry per record,
	// each as the lines it is read as.
	Notes   [][]string `json:"notes,omitempty"`
	Blocker []string   `json:"blocker,omitempty"`
}

func (f *baselineFixture) trace(t *testing.T, scenario baselineScenario) baselineTrace {
	t.Helper()
	normalizer := f.normalizer(t)
	record := f.tracker.Record()
	trace := baselineTrace{
		Scenario: scenario.name,
		Freezes:  scenario.freezes,
		WorkItem: baselineTracedItem{
			Calls:   record.Calls,
			Status:  record.Item.Status,
			Closed:  record.Closed,
			Blocked: record.Blocked,
			Notes:   normalizer.records(record.NoteRecords),
			Blocker: normalizer.lines(record.BlockReason),
		},
	}
	if trace.WorkItem.Calls == nil {
		trace.WorkItem.Calls = []string{}
	}
	for _, step := range f.steps {
		traced := baselineTracedStep{
			Name:    step.name,
			Ending:  step.outcome.Ending(),
			Outcome: normalizer.value(t, step.outcome),
		}
		if step.err != nil {
			traced.Error = normalizer.text(step.err.Error())
		}
		trace.Steps = append(trace.Steps, traced)
	}
	for _, reconciliation := range f.reconciled {
		trace.Reconciliations = append(trace.Reconciliations, normalizer.value(t, reconciliation))
	}
	trace.Durable, trace.Events = f.recorded(t, normalizer)
	trace.Provider = f.invocations()
	return trace
}

// recorded reads back the run record and the event log the scenario left on
// disk. A scenario that reserved no run leaves neither, which is itself part of
// what the path guarantees.
func (f *baselineFixture) recorded(t *testing.T, normalizer *baselineNormalizer) (map[string]any, []string) {
	t.Helper()
	state, err := f.store.Load(pipelineRunID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, []string{}
		}
		t.Fatalf("Load() error = %v", err)
	}
	events, err := f.store.LoadEvents(pipelineRunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	return normalizer.value(t, state), baselineEventSequence(events)
}

// baselineEventSequence is the run's log as a reader compares it: the types in
// the order they were appended, with consecutive repeats counted rather than
// listed, so a suite of three checks reads as three commands rather than six
// lines.
func baselineEventSequence(events []execution.Event) []string {
	sequence := []string{}
	for _, event := range events {
		name := string(event.Type)
		if len(sequence) > 0 {
			last := sequence[len(sequence)-1]
			if last == name {
				sequence[len(sequence)-1] = name + " x2"
				continue
			}
			if repeated, count, found := baselineRepeatCount(last, name); found {
				sequence[len(sequence)-1] = fmt.Sprintf("%s x%d", repeated, count+1)
				continue
			}
		}
		sequence = append(sequence, name)
	}
	return sequence
}

func baselineRepeatCount(entry, name string) (string, int, bool) {
	repeated, suffix, found := strings.Cut(entry, " x")
	if !found || repeated != name {
		return "", 0, false
	}
	count := 0
	if _, err := fmt.Sscanf(suffix, "%d", &count); err != nil {
		return "", 0, false
	}
	return repeated, count, true
}

// invocations is every provider call the scenario made, said by the three things
// that distinguish one from another: which role it was made as, whether it
// continued a session an earlier attempt established, and which prompt the
// harness handed it.
func (f *baselineFixture) invocations() []string {
	invocations := []string{}
	for _, provider := range f.providers {
		for _, request := range provider.RequestsMade() {
			continued := "new session"
			if request.SessionID != "" {
				continued = "continues session"
			}
			invocations = append(invocations,
				fmt.Sprintf("%s: %s, %s", request.Role, baselinePromptKind(request), continued))
		}
	}
	return invocations
}

// baselinePromptKind names what an invocation was asked for. The three repair
// prompts are told apart by their own headings, because which failure a
// developer was handed back is the whole of what a repair round is.
func baselinePromptKind(request backend.RunRequest) string {
	if request.Role != domain.RoleDeveloper {
		return "review of the change"
	}
	switch {
	case strings.Contains(request.Prompt, "# Independent review: repair required"):
		return "repair of review findings"
	case strings.Contains(request.Prompt, "# Protected paths: repair required"):
		return "repair of a protected-path refusal"
	case strings.Contains(request.Prompt, "# Failing check: repair required"):
		return "repair of a failing check"
	default:
		return "the assigned work item"
	}
}

//
// Normalization. A trace has to be the behavior rather than the machine it ran
// on, so everything a rerun would legitimately change is replaced by a stable
// placeholder: temporary directories, commit identifiers, and timestamps.
// Commits are numbered in the order they are first seen rather than collapsed
// into one placeholder, because which commit a field carries -- the base, the
// promoted commit, the one the target moved to -- is exactly what a promotion
// path is about.
//

type baselineNormalizer struct {
	replacements []baselineMask
	commits      map[string]string
	order        int
}

var (
	// No word boundaries: a commit is recorded at the end of one note and
	// immediately followed by the start of the next in the same field, so a
	// pattern that insisted on one would leave that commit in the trace.
	baselineCommitPattern = regexp.MustCompile(`[0-9a-f]{40}(?:[0-9a-f]{24})?`)
	baselineTimePattern   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	// The configuration revision is a digest of every effective value, and one
	// of those values is where the repository is -- a temporary directory here.
	// What a trace can state about it is that a run recorded one, which is the
	// part that is behavior rather than machine.
	baselineRevisionPattern = regexp.MustCompile(`\bcfg-[0-9a-f]{8,}\b`)
	// The content identity the checks are bound to is a digest over the base
	// commit and the change, so it moves with the temporary repository. What a
	// trace can state about it is that the checks were bound to one; it is
	// taken out ahead of the commit pattern, which its digest would otherwise
	// match and be numbered as a commit it is not.
	baselineContentPattern = regexp.MustCompile(regexp.QuoteMeta(gitworktree.ContentIdentityPrefix) + `[0-9a-f]{64}`)
	// How long a check actually ran is the machine rather than the behavior. The
	// budget it ran against is behavior and is left alone, which is why this
	// matches the pair rather than any duration.
	baselineElapsedPattern = regexp.MustCompile(`(exit=-?\d+, )\S+( of )`)
	// What the whole stage spent is the machine for the same reason, and its
	// bound is behavior for the same reason.
	baselineStageElapsedPattern = regexp.MustCompile(`(Check stage: )\S+( of the )`)
)

// baselineElapsedKeys are the recorded numbers that measure how long something
// ran rather than what it did: a stage's spend as each check ended, and a
// stage's or a landing check's spend once it ended.
var baselineElapsedKeys = map[string]struct{}{"stage_elapsed": {}, "elapsed_seconds": {}}

func (f *baselineFixture) normalizer(t *testing.T) *baselineNormalizer {
	t.Helper()
	normalizer := &baselineNormalizer{commits: map[string]string{}}
	for _, root := range []struct{ path, placeholder string }{
		{f.worktreeRoot, "<worktrees>"},
		{f.repository, "<repository>"},
		{f.store.Root(), "<runstate>"},
	} {
		normalizer.addPath(root.path, root.placeholder)
	}
	normalizer.replacements = append(normalizer.replacements, f.masked...)
	// Longest first, so a root nested inside another is replaced by its own
	// placeholder rather than by its parent's.
	sort.SliceStable(normalizer.replacements, func(i, j int) bool {
		return len(normalizer.replacements[i].actual) > len(normalizer.replacements[j].actual)
	})
	return normalizer
}

// addPath registers a directory under both the name it was handed and the name
// the filesystem resolves it to. Git reports resolved paths, which on macOS is
// not the temporary directory a test was given.
func (n *baselineNormalizer) addPath(path, placeholder string) {
	n.replacements = append(n.replacements, baselineMask{actual: path, placeholder: placeholder})
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		n.replacements = append(n.replacements, baselineMask{actual: resolved, placeholder: placeholder})
	}
}

// value normalizes anything the pipeline returned or recorded, by way of the
// JSON the harness itself stores and reports it as.
func (n *baselineNormalizer) value(t *testing.T, subject any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(subject)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	normalized, ok := n.walk(decoded).(map[string]any)
	if !ok {
		t.Fatalf("normalized %T is not an object", subject)
	}
	return normalized
}

// walk normalizes every string in a decoded document. Object keys are visited in
// sorted order so the commit numbering is the same on every run.
func (n *baselineNormalizer) walk(subject any) any {
	switch typed := subject.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		walked := make(map[string]any, len(typed))
		for _, key := range keys {
			if _, elapsed := baselineElapsedKeys[key]; elapsed {
				walked[key] = "<elapsed>"
				continue
			}
			walked[key] = n.walk(typed[key])
		}
		return walked
	case []any:
		walked := make([]any, 0, len(typed))
		for _, element := range typed {
			walked = append(walked, n.walk(element))
		}
		return walked
	case string:
		return n.text(typed)
	default:
		return subject
	}
}

// text is one string with everything a rerun would change taken out of it.
func (n *baselineNormalizer) text(value string) string {
	for _, replacement := range n.replacements {
		value = strings.ReplaceAll(value, replacement.actual, replacement.placeholder)
	}
	value = baselineTimePattern.ReplaceAllString(value, "<time>")
	value = baselineRevisionPattern.ReplaceAllString(value, "<config-revision>")
	value = baselineContentPattern.ReplaceAllString(value, "<content>")
	value = baselineElapsedPattern.ReplaceAllString(value, "${1}<elapsed>${2}")
	value = baselineStageElapsedPattern.ReplaceAllString(value, "${1}<elapsed>${2}")
	return baselineCommitPattern.ReplaceAllStringFunc(value, n.commit)
}

// lines is a multi-line record -- what a run wrote onto its work item -- as the
// lines it is read as, normalized and with the trailing blank ones dropped.
func (n *baselineNormalizer) lines(value string) []string {
	trimmed := strings.TrimRight(n.text(value), "\n")
	if strings.TrimSpace(trimmed) == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// records is every account a run wrote onto its work item, each as its own
// lines, so a pause and the completion after it read as two records rather than
// as one run of text.
func (n *baselineNormalizer) records(values []string) [][]string {
	var recorded [][]string
	for _, value := range values {
		if lines := n.lines(value); len(lines) > 0 {
			recorded = append(recorded, lines)
		}
	}
	return recorded
}

// commit numbers each distinct commit in the order the trace first mentions it.
func (n *baselineNormalizer) commit(value string) string {
	if placeholder, known := n.commits[value]; known {
		return placeholder
	}
	n.order++
	placeholder := fmt.Sprintf("<commit-%d>", n.order)
	n.commits[value] = placeholder
	return placeholder
}

//
// Comparison.
//

// baselineObservationRefusal is why a trace must not be frozen, and "" when
// there is no reason it must not be.
//
// A scenario driven on the declarative default and reserving a run has to record
// the instance that observed it. The delivery is unchanged either way — an
// observation never decides anything about a run — which is exactly why this has
// to be refused mechanically rather than noticed: a run nothing watched produces
// a trace that differs from the right one by one absent field, passes every
// other check in this file, and is then the recorded baseline of the path.
//
// That is not a hypothetical.
// `recoverable-death-carries-on-past-the-relaunch-budget` was recorded with no
// instance, agreed with three consecutive full checks, and began failing only
// when the same code reliably produced one — so the frozen document failed for a
// reason that had nothing to do with the change that had to fix it, and until
// then the run it froze counted as one the definition agreed with.
//
// It refuses on a comparison as well as on a re-record. Recording is where the
// damage is done, but a run that stops being observed between two recordings is
// a defect at the moment it happens rather than at the next `-update-baseline`,
// and there is no reason to hold the report until somebody re-records.
func baselineObservationRefusal(observable bool, durable map[string]any) string {
	if !observable || durable == nil {
		return ""
	}
	if instance, _ := durable["workflow_instance_id"].(string); instance != "" {
		return ""
	}
	if unobserved, _ := durable["workflow_unobserved"].(string); unobserved != "" {
		return fmt.Sprintf("the run it reserved was not observed: %s", unobserved)
	}
	return "the run it reserved records no workflow_instance_id, so nothing observed it and nothing said why"
}

func compareBaselineTrace(t *testing.T, name string, trace baselineTrace) {
	t.Helper()
	recorded, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	recorded = append(recorded, '\n')
	path := filepath.Join(baselineDirectory, name+".json")
	if *updateBaseline {
		if err := os.MkdirAll(baselineDirectory, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, recorded, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no recorded trace for this path: %v\nre-record with: go test ./internal/orchestrator -run TestDeliveryPipelineBaseline -update-baseline", err)
	}
	if string(expected) == string(recorded) {
		return
	}
	t.Errorf("%s no longer describes what this delivery path does.\n%s\n\nIf the change was intended, re-record with:\n\tgo test ./internal/orchestrator -run TestDeliveryPipelineBaseline -update-baseline",
		path, baselineDifference(string(expected), string(recorded)))
}

// baselineDifference reports the lines that moved, which is what makes a failing
// trace readable: the whole document would say only that something changed.
func baselineDifference(expected, recorded string) string {
	expectedLines := strings.Split(strings.TrimRight(expected, "\n"), "\n")
	recordedLines := strings.Split(strings.TrimRight(recorded, "\n"), "\n")
	var report strings.Builder
	reported := 0
	for index := 0; index < len(expectedLines) || index < len(recordedLines); index++ {
		was, is := "", ""
		if index < len(expectedLines) {
			was = expectedLines[index]
		}
		if index < len(recordedLines) {
			is = recordedLines[index]
		}
		if was == is {
			continue
		}
		if reported == baselineReportedDifferences {
			fmt.Fprintf(&report, "\n... and further differences past line %d", index+1)
			break
		}
		fmt.Fprintf(&report, "\nline %d:\n  recorded: %s\n       now: %s", index+1, was, is)
		reported++
	}
	return report.String()
}

// baselineReportedDifferences bounds a failure report. A trace that changed
// shape differs on every line after the first, and a reader needs the first few
// rather than all of them.
const baselineReportedDifferences = 12
