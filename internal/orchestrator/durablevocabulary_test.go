package orchestrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/selfcheck"
)

// The durable schema keeps its own copy of the reviewer's vocabularies, so a
// record is checked against what a record may hold rather than against whatever
// this version of the harness happens to produce. The copy is worth having; the
// drift between the copies is not. A severity or a decision the reviewer can
// produce and the schema will not store is refused at the moment a run records
// what its reviewer decided — in a process that has already told the tracker and
// the operator, and whose next act is to end the run over it.
//
// This is what makes that a failing check instead. Both lists are closed by
// decision, for the reasons recorded on them in the runstate package, so adding a
// severity or a decision means adding it in both places; this is what says
// whether that was done.
func TestTheDurableSchemaStoresEveryVerdictTheReviewerCanProduce(t *testing.T) {
	t.Parallel()

	durableSeverities := runstate.FindingSeverities()
	for _, severity := range review.Severities() {
		if !slices.Contains(durableSeverities, string(severity)) {
			t.Fatalf("the reviewer can produce severity %q and the durable schema stores only %v", severity, durableSeverities)
		}
		// Checked through the conversion a run actually stores findings by, so the
		// vocabulary is held where it crosses rather than only where it is listed.
		findings := durableFindings([]review.Finding{{
			Severity: severity,
			Message:  "the record has to be able to carry this",
			Location: &review.Location{File: "internal/run.go", Line: 12},
		}})
		if len(findings) != 1 {
			t.Fatalf("durableFindings() dropped the %q finding it was given", severity)
		}
		if err := findings[0].Validate(); err != nil {
			t.Fatalf("a stored %q finding is refused: %v", severity, err)
		}
	}

	// A finding's disposition is held the same way, and it is the one of these the
	// review budget reads: a disposition the record could not carry would be
	// refused at the save of a verdict whose round was already charged or spared
	// by it.
	durableDispositions := runstate.FindingDispositions()
	for _, disposition := range review.Dispositions() {
		if !slices.Contains(durableDispositions, string(disposition)) {
			t.Fatalf("the reviewer can dispose of a finding as %q and the durable schema stores only %v", disposition, durableDispositions)
		}
		// Checked through both conversions a run stores and restores findings by,
		// so the vocabulary is held where it crosses and the budget reads the same
		// answer from a restored run as from the verdict that produced it.
		given := []review.Finding{{
			Severity:    review.SeverityMinor,
			Disposition: disposition,
			Message:     "the record has to be able to carry this",
		}}
		findings := durableFindings(given)
		if len(findings) != 1 || findings[0].Disposition != string(disposition) {
			t.Fatalf("durableFindings() = %#v, want the %q disposition carried", findings, disposition)
		}
		if err := findings[0].Validate(); err != nil {
			t.Fatalf("a stored %q finding is refused: %v", disposition, err)
		}
		restored := reportedFindings(findings)
		if len(restored) != 1 || restored[0].Disposition != disposition {
			t.Fatalf("reportedFindings() = %#v, want the %q disposition restored", restored, disposition)
		}
		if review.TrivialResidue(restored) != review.TrivialResidue(given) {
			t.Errorf("disposition %q is a trivial residue=%t as a verdict and %t once restored", disposition,
				review.TrivialResidue(given), review.TrivialResidue(restored))
		}
	}

	durableDecisions := runstate.ReviewDecisions()
	for _, decision := range review.Decisions() {
		if !slices.Contains(durableDecisions, string(decision)) {
			t.Fatalf("the reviewer can decide %q and the durable schema stores only %v", decision, durableDecisions)
		}
	}

	// What an approval approves is the third of these vocabularies and the one that
	// decides most: an approval of evidence closes no work item, so a word the
	// record could not carry would settle the item as though the reviewer had said
	// the other thing.
	durableApprovals := runstate.ReviewApprovals()
	for _, approval := range review.Approvals() {
		if !slices.Contains(durableApprovals, string(approval)) {
			t.Fatalf("the reviewer can approve %q and the durable schema stores only %v", approval, durableApprovals)
		}
		// Checked through a state a run actually saves, so the vocabulary is held
		// where it crosses rather than only where it is listed.
		state := runstate.State{ReviewDecision: runstate.ReviewApprove, ReviewApproves: string(approval)}
		if err := state.Validate(); err != nil && strings.Contains(err.Error(), "review_approves is invalid") {
			t.Fatalf("a stored %q approval is refused: %v", approval, err)
		}
		// And the two derivations have to agree about which approval withholds the
		// closure, because the reviewer answers in one vocabulary and the closure is
		// decided in the other.
		if approval.Discharges() != state.ApprovalDischarges() {
			t.Errorf("approval %q discharges=%t as a verdict and %t as a record", approval,
				approval.Discharges(), state.ApprovalDischarges())
		}
	}
}

// The landing vocabulary is kept in two places for the reason the review
// vocabularies are, and it is held together here for a sharper reason than they
// are: what a landing outcome decides is whether the work item closes. An
// outcome a developer can claim and the schema will not store is refused at the
// save of a run whose change is already integrated, and the closure that then
// follows is decided from a record that never took the claim.
func TestTheDurableSchemaStoresEveryLandingADeveloperCanClaim(t *testing.T) {
	t.Parallel()

	durableOutcomes := runstate.LandingOutcomes()
	for _, outcome := range landing.Outcomes() {
		if !slices.Contains(durableOutcomes, string(outcome)) {
			t.Fatalf("a developer can claim landing %q and the durable schema stores only %v", outcome, durableOutcomes)
		}
		// Checked through a state a run actually saves, so the vocabulary is held
		// where it crosses rather than only where it is listed.
		state := runstate.State{LandingOutcome: string(outcome), LandingReason: "the record has to be able to carry this"}
		if err := state.Validate(); err != nil && strings.Contains(err.Error(), "landing_outcome is invalid") {
			t.Fatalf("a stored %q landing is refused: %v", outcome, err)
		}
	}
	// The two derivations have to agree about which outcome withholds the
	// closure, because the claim is made in one vocabulary and the closure is
	// decided in the other.
	for _, outcome := range landing.Outcomes() {
		claimed := landing.Claim{Outcome: outcome, Why: "recorded"}
		stored := runstate.State{LandingOutcome: string(outcome), LandingReason: "recorded"}
		if claimed.Discharges() != stored.LandingDischarges() {
			t.Errorf("landing %q discharges=%t as a claim and %t as a record", outcome,
				claimed.Discharges(), stored.LandingDischarges())
		}
	}
}

// The stop vocabulary is kept in one place rather than two, so what is held
// together here is different: the list the durable schema validates against and
// the constants the pipeline writes. A class declared and left off the list is
// refused at the save of a run that has already stopped — the one write that
// says why — and a class on the list that the schema then drops is a stop every
// surface prints without its first word. The list is also pinned, because every
// surface prints these words and a reader who learned them should not find one
// renamed under them.
func TestTheDurableSchemaStoresEveryStopClassThePipelineRecords(t *testing.T) {
	t.Parallel()

	want := []runstate.StopClass{"checks", "review", "integration", "publish", "cleanup",
		"recording", "provider", "outside", "cancelled", "harness",
		"unknown", "check-timeout", "provider-idle", "provider-budget", "relaunch-budget",
		"repair-budget", "integration-budget", "promotion-wait", "usage-pause", "operator-stop",
		"manager-stop", "redeploy-drain", "dead-claim", "developer-account", "review-account",
		"work-item-escalated", "context-bound", "state-bound", "event-bound", "integration-policy", "recovery-window",
		"review-bound"}
	for _, cause := range runstate.EnvironmentalCauses() {
		want = append(want, cause.StopClass())
	}
	if got := runstate.StopClasses(); !slices.Equal(got, want) {
		t.Fatalf("the stop vocabulary is %v, pinned as %v; changing it is changing a word every surface prints, so change both", got, want)
	}

	// Every constant of the type is on the list, read from the source that
	// declares them so a constant nobody listed is found by this rather than by a
	// refused save.
	declared := declaredStopClasses(t)
	if len(declared) == 0 {
		t.Fatal("no StopClass constant was found, so this compared nothing")
	}
	for name, value := range declared {
		if !slices.Contains(want, runstate.StopClass(value)) {
			t.Errorf("runstate.%s = %q is a stop class the durable schema does not store", name, value)
		}
	}

	for _, class := range want {
		// Checked through a state a run actually saves, so the vocabulary is held
		// where it crosses rather than only where it is listed.
		state := runstate.State{StopClass: class}
		if err := state.Validate(); err != nil && strings.Contains(err.Error(), "stop_class is invalid") {
			t.Errorf("a stored %q stop class is refused: %v", class, err)
		}
		// And every class leads the reason the way every surface prints it.
		if reason := runstate.StopReason(class, "what happened"); class != runstate.StopUnknown && !strings.HasPrefix(reason, string(class)+": ") {
			t.Errorf("StopReason(%q) = %q, want the class as its first word", class, reason)
		}
	}
	unknown := runstate.State{StopClass: "somebody"}
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "stop_class is invalid") {
		t.Errorf("a stop class nothing recognizes was stored: %v", err)
	}
}

// declaredStopClasses is every constant of type StopClass the runstate package
// declares, by name, with its value.
func declaredStopClasses(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../runstate/*.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	declared := map[string]string{}
	fileSet := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", path, err)
		}
		for _, decl := range parsed.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				typed, ok := value.Type.(*ast.Ident)
				if !ok || typed.Name != "StopClass" {
					continue
				}
				for i, name := range value.Names {
					literal, ok := value.Values[i].(*ast.BasicLit)
					if !ok {
						t.Fatalf("runstate.%s is not declared as a literal, so its value cannot be checked here", name.Name)
					}
					unquoted, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("runstate.%s: %v", name.Name, err)
					}
					declared[name.Name] = unquoted
				}
			}
		}
	}
	return declared
}

// The execution vocabulary is the third kept in two places, and it is held
// together here for the same reason as the other two. What an outcome decides is
// whether a change may be handed to a reviewer at all, so a word a developer can
// write and the schema will not store would be refused at the save — in the
// middle of a run whose developer had done exactly what its contract asked.
func TestTheDurableSchemaStoresEveryExecutionOutcomeADeveloperCanRecord(t *testing.T) {
	t.Parallel()

	durableOutcomes := runstate.VerificationOutcomes()
	for _, outcome := range selfcheck.Outcomes() {
		if !slices.Contains(durableOutcomes, string(outcome)) {
			t.Fatalf("a developer can record %q and the durable schema stores only %v", outcome, durableOutcomes)
		}
		// Checked through the conversion a run actually stores the record by, so
		// the vocabulary is held where it crosses rather than only where it is
		// listed.
		stored := durableVerification(selfcheck.Record{
			Probe: selfcheck.Execution{Command: "make build", Outcome: outcome, Detail: "what refused"},
		})
		if err := stored.Validate(); err != nil {
			t.Fatalf("a stored %q execution is refused: %v", outcome, err)
		}
		// And the two derivations have to agree about which outcome is a passing
		// one, because the developer writes in one vocabulary and the gate decides
		// in the other.
		written := selfcheck.Execution{Command: "make build", Outcome: outcome, Detail: "what refused"}
		if written.Passed() != stored.Probe.Passed() {
			t.Errorf("execution %q passes=%t as a record and %t as a stored one", outcome, written.Passed(), stored.Probe.Passed())
		}
	}
}
