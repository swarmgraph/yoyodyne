package runstate

// The account of every place in this package that can write a whole run record,
// held to the code it describes.
//
// A run's routing changes only through UpdateRouting, and Save refuses a record
// whose routing is not the one stored (see Save and checkRoutingWrite), or whose
// revision is not (runupdate.go). These guarantees hold only while every write
// of a run record goes through Save, UpdateRouting, or Update, or through
// Create, which makes a record that does not exist yet and so has no stored
// routing to lose. A writer that replaced the record file
// any other way would drop or forge a run's routing without either check seeing
// it, and the run would be resumed on a backend nobody chose. Save also refuses
// a write that would make a run that is not in flight live again holding a
// developer slot, which only ReserveLive may do, under the reservation lock;
// both reach the record through save, so a writer going round it would take a
// slot nobody counted as well.
//
// So the sweep below fails on:
//
//   - a function naming a run record's path (statePath) that is not listed in
//     runRecordPathSites;
//   - a function calling writeState other than save (behind Save and
//     ReserveLive), UpdateRouting, and Update;
//   - a *Store method other than statePath building a name ending in exactly
//     ".json" by concatenation, which is how a writer would name the record
//     without asking statePath for it. The other stores in this package name
//     their own records this way, so they are not swept for it.
//
// It fails on a listed site that is gone too, so the table stays the account of
// what is there. What it does not see is a write reached by a path computed some
// other way — read back from a directory listing, say. Writers outside this
// package are the second sweep's, further down.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// runRecordPathSites is every function that names a run record's file, and what
// it does with it.
var runRecordPathSites = map[string]string{
	"(*Store).Create":          "writes a record that must not exist yet, with O_EXCL, so there is no stored routing to lose or overwrite",
	"(*Store).writeState":      "the one replacement of an existing record; only save (behind Save and ReserveLive), UpdateRouting and Update call it, under the run's write lock",
	"(*Store).load":            "reads the record; Load and Read both go through it",
	"(*Store).storedHead":      "reads the stored routing and worker so Save can compare the one and keep the other before writing",
	"(*Store).WaitForSlot":     "asks only whether the document's run is recorded, under the reservation lock, before writing a wait of its own beside it",
	"(*Store).whatIsLeft":      "reads a record that would not load for its work item and status alone, so the run index can say whether it may be a run in flight; it writes nothing",
	"(*Store).unreadableStale": "looks at the size and time of a record the run index holds as unreadable, to decide whether to read it again; it writes nothing",
}

// runRecordReplacers is every function allowed to call writeState.
var runRecordReplacers = map[string]bool{
	"(*Store).save":          true,
	"(*Store).UpdateRouting": true,
	"(*Store).Update":        true,
}

// runRecordWriteSite is one place the sweep found, keyed by the function it sits
// in.
type runRecordWriteSite struct {
	names    bool // calls statePath
	replaces bool // calls writeState
	builds   bool // concatenates a name ending in ".json"
	at       token.Position
}

func TestEveryRunRecordWriteGoesThroughSaveOrUpdateRouting(t *testing.T) {
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	problems, found := runRecordWriteProblems(fileSet, files)
	for _, problem := range problems {
		t.Error(problem)
	}
	var stale []string
	for key := range runRecordPathSites {
		if found[key] == nil || !found[key].names {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("%s is in runRecordPathSites and no longer names a run record's file; take it out of the table", key)
	}
	for key, why := range runRecordPathSites {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is listed without saying what it does with a run record", key)
		}
	}
}

// TestTheRunRecordSweepCatchesABypassingWriter holds the sweep to the case it
// exists for, so a sweep that stopped seeing anything would fail here rather
// than pass above.
func TestTheRunRecordSweepCatchesABypassingWriter(t *testing.T) {
	const source = `package runstate

func (s *Store) statePath(runID string) (string, error) { return filepath.Join(s.root, runID+".json"), nil }
func (s *Store) writeState(state State) error { return nil }
func (s *Store) Save(state State) error { return s.save(state, false) }
func (s *Store) save(state State, reserved bool) error { return s.writeState(state) }
func (s *Store) StopQuietly(state State) error {
	path, _ := s.statePath(state.RunID)
	return replaceJSONFile(s.root, path, "run state", state)
}
func (s *Store) ForceSave(state State) error { return s.writeState(state) }
func (s *Store) Sideways(state State) error {
	return replaceJSONFile(s.root, filepath.Join(s.root, state.RunID+".json"), "run state", state)
}
`
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "bypass.go", source, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found, _ := runRecordWriteProblems(fileSet, []*ast.File{parsed})
	problems := strings.Join(found, "\n")
	for _, want := range []string{"(*Store).StopQuietly names", "(*Store).ForceSave replaces", "(*Store).Sideways builds"} {
		if !strings.Contains(problems, want) {
			t.Errorf("the sweep did not report %q; it reported:\n%s", want, problems)
		}
	}
	if strings.Contains(problems, "(*Store).Save replaces") || strings.Contains(problems, "(*Store).save replaces") || strings.Contains(problems, "(*Store).statePath builds") {
		t.Errorf("the sweep reported an allowed site:\n%s", problems)
	}
}

// runRecordWriteProblems sweeps files for the sites described at the top of
// this file and returns one sentence for each that is not allowed, with every
// site it found.
func runRecordWriteProblems(fileSet *token.FileSet, files []*ast.File) ([]string, map[string]*runRecordWriteSite) {
	found := map[string]*runRecordWriteSite{}
	site := func(key string, at token.Pos) *runRecordWriteSite {
		if found[key] == nil {
			found[key] = &runRecordWriteSite{at: fileSet.Position(at)}
		}
		return found[key]
	}
	for _, parsed := range files {
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			key := functionName(function)
			sweepsNames := strings.HasPrefix(key, "(*Store).")
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CallExpr:
					switch calledName(node) {
					case "statePath":
						site(key, node.Pos()).names = true
					case "writeState":
						site(key, node.Pos()).replaces = true
					}
				case *ast.BinaryExpr:
					if literal, ok := node.Y.(*ast.BasicLit); sweepsNames && ok && node.Op == token.ADD && literal.Value == `".json"` {
						site(key, node.Pos()).builds = true
					}
				}
				return true
			})
		}
	}

	var problems []string
	for key, at := range found {
		if at.names {
			if _, listed := runRecordPathSites[key]; !listed {
				problems = append(problems, key+" names a run record's file (at "+at.at.String()+") and is not in runRecordPathSites. A whole run record is written only through Save, or UpdateRouting for its routing; if this only reads the record, list it with what it does.")
			}
		}
		if at.replaces && !runRecordReplacers[key] {
			problems = append(problems, key+" replaces a run record through writeState (at "+at.at.String()+"). Only save, behind Save and ReserveLive, UpdateRouting, and Update may, because they hold the run's write lock, compare the stored routing and revision, and refuse a run made live without a developer slot first; declare a RunUpdate and call Update, or ReserveLive to make a run live.")
		}
		if at.builds && key != "(*Store).statePath" {
			problems = append(problems, key+" builds a name ending in \".json\" (at "+at.at.String()+"), which is how a run record is named. Ask statePath for a run record's name and write it through Save; another record wants a suffix of its own, such as \".stop.json\".")
		}
	}
	sort.Strings(problems)
	return problems, found
}

// calledName is the name a call is made by, whether on a receiver or not.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}

// The second sweep: run record writers outside this package.
//
// The design for one owner for each guarantee
// (docs/designs/one-owner-for-each-guarantee.md, "Checks run by make test")
// gives this package two guarantees. A run record has one writer at a time and
// loses no change, which holds only while code elsewhere changes a record
// through one of the store's narrow updates rather than by loading it, doing
// other work, and saving the whole record back over whatever another writer
// wrote in between. And a run moves between states only through this package,
// which holds only while nothing elsewhere sets a run's status or step itself.
// Much code outside the package does both today, so the sweep holds it to a list
// of the places that still do, and the list only shrinks: each item that moves a
// place onto a narrow update takes its entry off, and the list goes when it is
// empty.
//
// It reads every package of the module but this one, with its types, from the
// build's own account of the module (go list), and finds:
//
//   - a whole run record saved through the run store: a call of (*Store).Save,
//     or of a Save taking a State on an interface, which is how most callers
//     hold the store; and Save taken as a value to be called later;
//   - such a call, or a call of the store's narrow Update, whose error is thrown
//     away: made as a statement on its own, assigned to _, or run by go or
//     defer;
//   - an assignment to a run record's Status or Phase, its saved status and
//     step.
//
// A site is the file and the top-level function it is written in, with the rule
// it breaks, and never a line, so an edit elsewhere in the file leaves the list
// alone; a function breaking a rule twice is one site. The list is checked both
// ways: a site that is not on it fails, and so does an entry that matches no
// site, so it stays the account of what is there.
//
// What it does not read: test files, which build their own records to test
// against; the store's other writers, ReserveLive and Create, which are the
// reservation's way in and the making of a record that does not exist yet; a
// save reached through a function value of some other name; and an error kept
// in a variable and then never looked at.

// outsideRunRecordRule is what a site outside this package does to a run record,
// in the words a failure says it in.
type outsideRunRecordRule string

const (
	savesWholeRecord  outsideRunRecordRule = "saves a whole run record"
	discardsSaveError outsideRunRecordRule = "throws away the error a run record save or update returns"
	setsStatusOrStep  outsideRunRecordRule = "sets a run's saved status or step"
)

// outsideRunRecordSite is one place outside this package that writes a run
// record round the store's narrow updates.
type outsideRunRecordSite struct {
	file     string // from the module's root, with forward slashes
	function string // the top-level function, or "package level"
	rule     outsideRunRecordRule
}

// outsideRunRecordBypasses is every place outside this package that still
// writes a run record round the store's narrow updates. It only shrinks.
var outsideRunRecordBypasses = []outsideRunRecordSite{
	{"internal/orchestrator/checkoutrecovery.go", "(checkoutRecovery).recordRestoredCheckout", savesWholeRecord},
	{"internal/orchestrator/checkoutrecovery.go", "(checkoutRecovery).restoreCheckout", savesWholeRecord},
	{"internal/orchestrator/checkoutrecovery.go", "(checkoutRecovery).restoreCheckout", setsStatusOrStep},
	{"internal/orchestrator/checkstagecontinue.go", "(CheckStageContinuer).NoteWaiting", savesWholeRecord},
	{"internal/orchestrator/checkstagecontinue.go", "(CheckStageContinuer).refuse", savesWholeRecord},
	{"internal/orchestrator/checkstagecontinue.go", "continuedAtChecks", setsStatusOrStep},
	{"internal/orchestrator/claims.go", "(ClaimAuditor).settle", savesWholeRecord},
	{"internal/orchestrator/claims.go", "(ClaimAuditor).settle", setsStatusOrStep},
	{"internal/orchestrator/configreaders.go", "(configFindingRecorder).name", savesWholeRecord},
	{"internal/orchestrator/converge.go", "(Reconciler).correctRelease", savesWholeRecord},
	{"internal/orchestrator/converge.go", "(Reconciler).recordPreservedWork", savesWholeRecord},
	{"internal/orchestrator/converge.go", "(Reconciler).recordSweptBranch", savesWholeRecord},
	{"internal/orchestrator/converge.go", "(Reconciler).recordSweptWorktree", savesWholeRecord},
	{"internal/orchestrator/converge.go", "(Reconciler).sweepPublication", savesWholeRecord},
	{"internal/orchestrator/declarative.go", "(*activeRun).recordDivergence", discardsSaveError},
	{"internal/orchestrator/declarative.go", "(*activeRun).recordDivergence", savesWholeRecord},
	{"internal/orchestrator/developerrouting.go", "(*activeRun).pinDeveloperRouting", savesWholeRecord},
	{"internal/orchestrator/developerrouting.go", "(*activeRun).selectOperationEndpoint", savesWholeRecord},
	{"internal/orchestrator/developerrouting.go", "(*activeRun).switchDeveloperEndpoint", savesWholeRecord},
	{"internal/orchestrator/documentdecision.go", "(DocumentDecider).record", savesWholeRecord},
	{"internal/orchestrator/documentpublication.go", "(*activeRun).prepareDocument", savesWholeRecord},
	{"internal/orchestrator/documentpublication.go", "(*activeRun).recoverDocumentIntegration", savesWholeRecord},
	{"internal/orchestrator/documentpublication.go", "(Pipeline).handOverDocument", savesWholeRecord},
	{"internal/orchestrator/documentpublication.go", "(Pipeline).publishDocumentAttempt", savesWholeRecord},
	{"internal/orchestrator/documentpublication.go", "(Pipeline).publishDocumentAttempt", setsStatusOrStep},
	{"internal/orchestrator/documentpublication.go", "(Pipeline).recordDocumentRetry", savesWholeRecord},
	{"internal/orchestrator/escalationended.go", "(Reconciler).endEscalation", savesWholeRecord},
	{"internal/orchestrator/integrationresume.go", "(IntegrationResumer).restoreWorktree", savesWholeRecord},
	{"internal/orchestrator/integrationresume.go", "(IntegrationResumer).supersedeOnRun", setsStatusOrStep},
	{"internal/orchestrator/keptchecks.go", "(*activeRun).keepPassed", discardsSaveError},
	{"internal/orchestrator/keptchecks.go", "(*activeRun).keepPassed", savesWholeRecord},
	{"internal/orchestrator/mergedisagreement.go", "(Reconciler).findMergeDisagreement", savesWholeRecord},
	{"internal/orchestrator/mergedisagreement.go", "(Reconciler).recordUnconfirmedMerge", savesWholeRecord},
	{"internal/orchestrator/mergequeueadmission.go", "(*activeRun).admitToMergeQueue", savesWholeRecord},
	{"internal/orchestrator/mergequeueadmission.go", "(*activeRun).completeQueued", savesWholeRecord},
	{"internal/orchestrator/mergequeuepromotion.go", "(MergeQueueRunCompletion).RecordWorkItem", savesWholeRecord},
	{"internal/orchestrator/mergequeuerecovery.go", "(MergeQueueRunHandback).HandBack", savesWholeRecord},
	{"internal/orchestrator/mergequeuerecovery.go", "(MergeQueueRunHandback).HandBack", setsStatusOrStep},
	{"internal/orchestrator/outsidefilelanding.go", "(*activeRun).writeOutsideFile", savesWholeRecord},
	{"internal/orchestrator/outsidefilelanding.go", "(Pipeline).BringInOutsideFile", savesWholeRecord},
	{"internal/orchestrator/outsidefilelanding.go", "(Pipeline).BringInOutsideFile", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).attemptDevelopment", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).attemptReview", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).attemptReview", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).awaitRecordedUsageLimit", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).chargeReplayStop", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).judgeReplay", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).cleanUp", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).cleanUp", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).clearDependencyPause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).clearDirectivePause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).clearOperatorHold", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).clearTrackerPause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).clearUsageLimitPause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).complete", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).complete", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).escalate", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).escalate", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).fail", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).fail", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).integrate", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).integrate", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).leaveDeveloping", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).moveOntoTargetForRepair", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).noteTrackerPause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).pauseForServerOverload", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).pauseForUsageLimit", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).prepareIntegrationRetry", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordAfterReplyWaiting", discardsSaveError},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordAfterReplyWaiting", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordDependencyPause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordDevelopment", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordDirectivePause", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordDirectivePause", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordEndingAfterRefusedSave", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordEndingAfterRefusedSave", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordOperatorHold", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordProviderStop", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).reconcile", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).reconcile", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordRebase", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).recordRelaunch", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).repair", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).repair", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(*activeRun).saveLanding", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).sink", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).verify", discardsSaveError},
	{"internal/orchestrator/pipeline.go", "(*activeRun).verify", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(*activeRun).verify", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(Pipeline).Run", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(Pipeline).Run", setsStatusOrStep},
	{"internal/orchestrator/pipeline.go", "(Pipeline).refuseDispatchEnvironmentally", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(Pipeline).reportCompletionRecordingFailure", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(Pipeline).reportOutstandingCleanup", savesWholeRecord},
	{"internal/orchestrator/pipeline.go", "(Pipeline).waitInFlightOnDependencies", savesWholeRecord},
	{"internal/orchestrator/provideroutage.go", "(*activeRun).pauseForProviderOutage", savesWholeRecord},
	{"internal/orchestrator/publication.go", "(Reconciler).recordRecoveredRequest", savesWholeRecord},
	{"internal/orchestrator/publication.go", "(Reconciler).recordSettledPublication", savesWholeRecord},
	{"internal/orchestrator/publication.go", "(Reconciler).refreshPublication", savesWholeRecord},
	{"internal/orchestrator/publish.go", "(*activeRun).publishAttempt", savesWholeRecord},
	{"internal/orchestrator/publish.go", "(*activeRun).publishIntegration", savesWholeRecord},
	{"internal/orchestrator/publish.go", "(*activeRun).recordPublishFailure", savesWholeRecord},
	{"internal/orchestrator/publish.go", "(*activeRun).republishRebase", savesWholeRecord},
	{"internal/orchestrator/queuedchecks.go", "(Reconciler).recordUnreadChecks", savesWholeRecord},
	{"internal/orchestrator/queuedchecks.go", "(Reconciler).settleStillQueued", savesWholeRecord},
	{"internal/orchestrator/queuedchecks.go", "(Reconciler).updateQueuedHead", setsStatusOrStep},
	{"internal/orchestrator/rearm.go", "(Rearmer).repeat", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).completeIntegrated", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).completeIntegrated", setsStatusOrStep},
	{"internal/orchestrator/reconcile.go", "(Reconciler).deleteMergedBranch", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).saveTerminalFailure", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).saveTerminalFailure", setsStatusOrStep},
	{"internal/orchestrator/reconcile.go", "(Reconciler).settleInterruptedLanding", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).settleInterruptedLanding", setsStatusOrStep},
	{"internal/orchestrator/reconcile.go", "(Reconciler).settleInterruptedLandingChecks", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).settleStopRequest", savesWholeRecord},
	{"internal/orchestrator/reconcile.go", "(Reconciler).settleStopRequest", setsStatusOrStep},
	{"internal/orchestrator/reconcilefinding.go", "(Reconciler).clearReconcileFindings", savesWholeRecord},
	{"internal/orchestrator/reconcilefinding.go", "(Reconciler).deliverReconcileFinding", savesWholeRecord},
	{"internal/orchestrator/reconcilefinding.go", "(Reconciler).recordReconcileFinding", savesWholeRecord},
	{"internal/orchestrator/reconcilewait.go", "(Reconciler).takeUpWait", savesWholeRecord},
	{"internal/orchestrator/recovery.go", "(*activeRun).recordRetry", savesWholeRecord},
	{"internal/orchestrator/recovery.go", "(Reconciler).recovering", savesWholeRecord},
	{"internal/orchestrator/recovery.go", "waitOnTracker", savesWholeRecord},
	{"internal/orchestrator/recoveryrequest.go", "(RecoveredIntegrations).Wait", savesWholeRecord},
	{"internal/orchestrator/recoveryrequest.go", "(RecoveredIntegrations).refuse", savesWholeRecord},
	{"internal/orchestrator/redeploydrain.go", "(*activeRun).pauseForRedeploy", savesWholeRecord},
	{"internal/orchestrator/redtarget.go", "(Reconciler).rerunEndedJobsAfterRedTarget", savesWholeRecord},
	{"internal/orchestrator/redtarget.go", "(Reconciler).resumeRedTarget", savesWholeRecord},
	{"internal/orchestrator/redtarget.go", "(Reconciler).waitOnRedTargetOwned", savesWholeRecord},
	{"internal/orchestrator/repaircontinue.go", "(RepairContinuer).confirmContinuationNote", savesWholeRecord},
	{"internal/orchestrator/repaircontinue.go", "(RepairContinuer).recordDispatchedContinuation", savesWholeRecord},
	{"internal/orchestrator/repaircontinue.go", "(RepairContinuer).saveContinuation", savesWholeRecord},
	{"internal/orchestrator/repaircontinue.go", "(RepairContinuer).supersedeOnRun", setsStatusOrStep},
	{"internal/orchestrator/rerun.go", "(Rerunner).handBackPublication", savesWholeRecord},
	{"internal/orchestrator/rerun.go", "(Rerunner).recordRemoval", savesWholeRecord},
	{"internal/orchestrator/reviewbound.go", "(*activeRun).gateReviewBound", setsStatusOrStep},
	{"internal/orchestrator/runretirement.go", "(RunRetirer).Retire", savesWholeRecord},
	{"internal/orchestrator/runretirement.go", "(RunRetirer).Retire", setsStatusOrStep},
	{"internal/orchestrator/runretirement.go", "(RunRetirer).note", savesWholeRecord},
	{"internal/orchestrator/schedulingwait.go", "(*activeRun).clearAcceptedSchedulingWait", savesWholeRecord},
	{"internal/orchestrator/schedulingwait.go", "(Reconciler).retrySchedulingWaitClear", savesWholeRecord},
	{"internal/orchestrator/stallcontinue.go", "(StallContinuer).refuse", savesWholeRecord},
	{"internal/orchestrator/stallcontinue.go", "continuedAfterStall", setsStatusOrStep},
	{"internal/orchestrator/stuckmerge.go", "putBackAtPromotion", setsStatusOrStep},
	{"internal/orchestrator/testharness/pipeline.go", "(*InterruptedStore).Save", savesWholeRecord},
	{"internal/orchestrator/testharness/pipeline.go", "(*InterruptingStore).Save", savesWholeRecord},
	{"internal/orchestrator/testharness/reconcile.go", "(*HaltingStore).Save", savesWholeRecord},
	{"internal/runrecover/recover.go", "(Recoverer).Recover", discardsSaveError},
	{"internal/runrecover/recover.go", "(Recoverer).Recover", savesWholeRecord},
	{"internal/runrecover/recover.go", "(Recoverer).write", discardsSaveError},
	{"internal/runrecover/recover.go", "(Recoverer).write", savesWholeRecord},
}

func TestNoRunRecordWriterOutsideThisPackageIsUnlisted(t *testing.T) {
	module := loadModuleTypes(t)
	found := map[outsideRunRecordSite]token.Position{}
	for _, listed := range module.listed {
		if listed.ImportPath == module.path+"/internal/runstate" || strings.HasPrefix(listed.ImportPath, module.path+"/internal/runstate/") {
			continue
		}
		files, info := typeCheckListed(t, module, listed)
		for site, at := range outsideRunRecordSites(module.fileSet, module.root, files, info, module.record) {
			found[site] = at
		}
	}
	for _, problem := range outsideRunRecordProblems(found, outsideRunRecordBypasses) {
		t.Error(problem)
	}
}

// TestTheOutsideSweepNamesEachAddedSite holds the second sweep to the cases it
// exists for, over source written here rather than the module's, so a sweep that
// stopped seeing anything fails here rather than passing above.
func TestTheOutsideSweepNamesEachAddedSite(t *testing.T) {
	module := loadModuleTypes(t)
	found := fixtureRunRecordSites(t, module, "internal/fixture/added.go", `package fixture

import (
	"context"

	"`+module.path+`/internal/runstate"
)

type saver interface{ Save(runstate.State) error }

type other struct{}

func (other) Save(runstate.State) error { return nil }

func AddedSave(store *runstate.Store, state runstate.State) error {
	if err := store.Save(state); err != nil {
		return err
	}
	return nil
}

func DroppedSave(runs saver, state runstate.State) {
	_ = runs.Save(state)
}

type updater interface {
	Update(ctx context.Context, runID string, update runstate.RunUpdate) (runstate.State, error)
}

func DroppedUpdate(runs updater, store *runstate.Store, runID string) {
	_, _ = runs.Update(context.Background(), runID, runstate.QueuedRunCompleted{})
	store.Update(context.Background(), runID, runstate.QueuedRunCompleted{})
}

func KeptUpdate(runs updater, runID string) error {
	_, err := runs.Update(context.Background(), runID, runstate.QueuedRunCompleted{})
	return err
}

func (w *watcher) Moved(state *runstate.State) {
	state.Status = runstate.StatusFailed
	state.Phase = runstate.PhaseComplete
}

type watcher struct{}

func NotARunRecord(o other, state runstate.State) {
	o.Save(state)
	var status runstate.Status
	status = runstate.StatusFailed
	_ = status
}
`)
	problems := strings.Join(outsideRunRecordProblems(found, nil), "\n")
	for _, want := range []string{
		"internal/fixture/added.go, in AddedSave, saves a whole run record",
		"internal/fixture/added.go, in DroppedSave, saves a whole run record",
		"internal/fixture/added.go, in DroppedSave, throws away the error a run record save or update returns",
		"internal/fixture/added.go, in (*watcher).Moved, sets a run's saved status or step",
		"internal/fixture/added.go, in DroppedUpdate, throws away the error a run record save or update returns",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("the sweep did not report %q; it reported:\n%s", want, problems)
		}
	}
	for _, unwanted := range []string{"in AddedSave, throws away", "in NotARunRecord", "in KeptUpdate", "in DroppedUpdate, saves"} {
		if strings.Contains(problems, unwanted) {
			t.Errorf("the sweep reported %q, which is not a run record write round the store:\n%s", unwanted, problems)
		}
	}
	if count := strings.Count(problems, "\n") + 1; count != 5 {
		t.Errorf("the sweep reported %d sites, want 5, one for each rule each function breaks:\n%s", count, problems)
	}

	listed := []outsideRunRecordSite{
		{"internal/fixture/added.go", "AddedSave", savesWholeRecord},
		{"internal/fixture/added.go", "DroppedSave", savesWholeRecord},
		{"internal/fixture/added.go", "DroppedSave", discardsSaveError},
		{"internal/fixture/added.go", "(*watcher).Moved", setsStatusOrStep},
		{"internal/fixture/added.go", "DroppedUpdate", discardsSaveError},
	}
	if problems := outsideRunRecordProblems(found, listed); len(problems) != 0 {
		t.Errorf("the sweep failed sites that are listed:\n%s", strings.Join(problems, "\n"))
	}
}

// TestTheOutsideSweepFailsAListedEntryWithNoSite is the half that keeps the list
// shrinking: an entry whose site has moved onto a narrow update fails until it is
// taken off.
func TestTheOutsideSweepFailsAListedEntryWithNoSite(t *testing.T) {
	module := loadModuleTypes(t)
	found := fixtureRunRecordSites(t, module, "internal/fixture/moved.go", `package fixture

import "`+module.path+`/internal/runstate"

func Moved(store *runstate.Store, state runstate.State) error { return store.Save(state) }
`)
	listed := []outsideRunRecordSite{
		{"internal/fixture/moved.go", "Moved", savesWholeRecord},
		{"internal/fixture/moved.go", "Moved", setsStatusOrStep},
	}
	problems := outsideRunRecordProblems(found, listed)
	want := "internal/fixture/moved.go, in Moved, is on outsideRunRecordBypasses as a place that sets a run's saved status or step, and it no longer does"
	if len(problems) != 1 || !strings.Contains(problems[0], want) {
		t.Errorf("the sweep reported:\n%s\nwant one problem saying %q", strings.Join(problems, "\n"), want)
	}
	duplicated := append(listed[:1:1], listed[0])
	if problems := strings.Join(outsideRunRecordProblems(found, duplicated), "\n"); !strings.Contains(problems, "listed twice") {
		t.Errorf("the sweep did not refuse an entry listed twice; it reported:\n%s", problems)
	}
}

// outsideRunRecordProblems returns one sentence for each site found and not
// listed, each listed entry matching no site, and each entry listed twice.
func outsideRunRecordProblems(found map[outsideRunRecordSite]token.Position, listed []outsideRunRecordSite) []string {
	var problems []string
	onList := map[outsideRunRecordSite]bool{}
	for _, entry := range listed {
		if onList[entry] {
			problems = append(problems, fmt.Sprintf("%s, in %s, is listed twice in outsideRunRecordBypasses as a place that %s; list it once.", entry.file, entry.function, entry.rule))
		}
		onList[entry] = true
		if _, ok := found[entry]; !ok {
			problems = append(problems, fmt.Sprintf("%s, in %s, is on outsideRunRecordBypasses as a place that %s, and it no longer does: take it off the list, which only ever shrinks.", entry.file, entry.function, entry.rule))
		}
	}
	for site, at := range found {
		if onList[site] {
			continue
		}
		var why string
		switch site.rule {
		case savesWholeRecord:
			why = "Outside internal/runstate a run record changes only through one of the run store's narrow updates, which writes its own fields under the run's write lock, so nothing another writer changed in between is lost. Add the update this needs to the run store and call it."
		case discardsSaveError:
			why = "A save or update that fails leaves the record as it was, and code that does not look carries on as if it had been written. Handle the error."
		case setsStatusOrStep:
			why = "A run moves between states only through internal/runstate, so whether a move is allowed is decided in one place. Ask the run store for the move instead."
		}
		problems = append(problems, fmt.Sprintf("%s, in %s, %s (at line %d), and is not on outsideRunRecordBypasses. %s", site.file, site.function, site.rule, at.Line, why))
	}
	sort.Strings(problems)
	return problems
}

// runRecordTypes is what the second sweep compares against: this package's
// types as the rest of the module sees them.
type runRecordTypes struct {
	state, store, update types.Type
	status, phase        *types.Var
}

// outsideRunRecordSites finds the sites in files, which info describes, keyed by
// their file's path from root.
func outsideRunRecordSites(fileSet *token.FileSet, root string, files []*ast.File, info *types.Info, record runRecordTypes) map[outsideRunRecordSite]token.Position {
	found := map[outsideRunRecordSite]token.Position{}
	isSave := func(expression ast.Expr) bool {
		selector, ok := ast.Unparen(expression).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		selection := info.Selections[selector]
		if selection == nil || selection.Kind() == types.FieldVal || selection.Obj().Name() != "Save" {
			return false
		}
		signature := selection.Obj().Type().(*types.Signature)
		receiver := signature.Recv().Type()
		if pointer, ok := receiver.(*types.Pointer); ok {
			receiver = pointer.Elem()
		}
		if types.Identical(receiver, record.store) {
			return true
		}
		return types.IsInterface(receiver) && signature.Params().Len() == 1 && types.Identical(signature.Params().At(0).Type(), record.state)
	}
	// isUpdate is a call of the store's narrow Update, on the store or on an
	// interface taking a RunUpdate last. Making one is the way in, but throwing
	// its error away is the same defect as throwing a save's away.
	isUpdate := func(expression ast.Expr) bool {
		selector, ok := ast.Unparen(expression).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		selection := info.Selections[selector]
		if selection == nil || selection.Kind() == types.FieldVal || selection.Obj().Name() != "Update" {
			return false
		}
		params := selection.Obj().Type().(*types.Signature).Params()
		return params.Len() > 0 && types.Identical(params.At(params.Len()-1).Type(), record.update)
	}
	isSaveCall := func(expression ast.Expr) bool {
		call, ok := ast.Unparen(expression).(*ast.CallExpr)
		return ok && (isSave(call.Fun) || isUpdate(call.Fun))
	}
	for _, file := range files {
		name, err := filepath.Rel(root, fileSet.Position(file.Pos()).Filename)
		if err != nil {
			name = fileSet.Position(file.Pos()).Filename
		}
		name = filepath.ToSlash(name)
		for _, declaration := range file.Decls {
			function := "package level"
			if declared, ok := declaration.(*ast.FuncDecl); ok {
				function = functionName(declared)
			}
			note := func(rule outsideRunRecordRule, at token.Pos) {
				site := outsideRunRecordSite{file: name, function: function, rule: rule}
				if _, seen := found[site]; !seen {
					found[site] = fileSet.Position(at)
				}
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.SelectorExpr:
					if isSave(node) {
						note(savesWholeRecord, node.Pos())
					}
				case *ast.ExprStmt:
					if isSaveCall(node.X) {
						note(discardsSaveError, node.Pos())
					}
				case *ast.GoStmt:
					if isSaveCall(node.Call) {
						note(discardsSaveError, node.Pos())
					}
				case *ast.DeferStmt:
					if isSaveCall(node.Call) {
						note(discardsSaveError, node.Pos())
					}
				case *ast.AssignStmt:
					for i, left := range node.Lhs {
						if blank, ok := left.(*ast.Ident); ok && blank.Name == "_" && len(node.Lhs) == len(node.Rhs) && isSaveCall(node.Rhs[i]) {
							note(discardsSaveError, node.Pos())
						}
						// An update answers with the run and an error, so its error is
						// the last of two places assigned.
						if blank, ok := left.(*ast.Ident); ok && blank.Name == "_" && i == len(node.Lhs)-1 && len(node.Rhs) == 1 && len(node.Lhs) == 2 && isSaveCall(node.Rhs[0]) {
							note(discardsSaveError, node.Pos())
						}
						selector, ok := ast.Unparen(left).(*ast.SelectorExpr)
						if !ok {
							continue
						}
						if selection := info.Selections[selector]; selection != nil && selection.Kind() == types.FieldVal && (selection.Obj() == record.status || selection.Obj() == record.phase) {
							note(setsStatusOrStep, node.Pos())
						}
					}
				}
				return true
			})
		}
	}
	return found
}

// listedPackage is what go list says of one package.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	DepOnly    bool
}

// moduleTypes is the module as go list describes it, with an importer reading
// every package's compiled types, shared by the second sweep's tests so the
// module is listed once.
type moduleTypes struct {
	root, path string
	fileSet    *token.FileSet
	importer   types.Importer
	listed     []listedPackage // the module's own packages
	record     runRecordTypes
}

var (
	loadedModuleTypesOnce sync.Once
	loadedModuleTypes     *moduleTypes
	loadedModuleTypesErr  error
)

func loadModuleTypes(t *testing.T) *moduleTypes {
	t.Helper()
	loadedModuleTypesOnce.Do(func() {
		loadedModuleTypes, loadedModuleTypesErr = listModuleTypes()
	})
	if loadedModuleTypesErr != nil {
		t.Fatal(loadedModuleTypesErr)
	}
	return loadedModuleTypes
}

func listModuleTypes() (*moduleTypes, error) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return nil, err
	}
	modulePath, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w", err)
	}
	command := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,DepOnly", "./...")
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -export -deps ./... in %s: %v: %s", root, err, stderr.String())
	}
	module := &moduleTypes{root: root, path: strings.TrimSpace(string(modulePath)), fileSet: token.NewFileSet()}
	exports := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("read go list's account of the module: %w", err)
		}
		exports[listed.ImportPath] = listed.Export
		if !listed.DepOnly {
			module.listed = append(module.listed, listed)
		}
	}
	module.importer = importer.ForCompiler(module.fileSet, "gc", func(path string) (io.ReadCloser, error) {
		export := exports[path]
		if export == "" {
			return nil, fmt.Errorf("go list gave no compiled types for %s", path)
		}
		return os.Open(export)
	})
	runstate, err := module.importer.Import(module.path + "/internal/runstate")
	if err != nil {
		return nil, fmt.Errorf("read internal/runstate's compiled types: %w", err)
	}
	state := runstate.Scope().Lookup("State")
	store := runstate.Scope().Lookup("Store")
	update := runstate.Scope().Lookup("RunUpdate")
	if state == nil || store == nil || update == nil {
		return nil, errors.New("internal/runstate declares no State, no Store, or no RunUpdate")
	}
	module.record = runRecordTypes{state: state.Type(), store: store.Type(), update: update.Type()}
	fields := state.Type().Underlying().(*types.Struct)
	for i := 0; i < fields.NumFields(); i++ {
		switch fields.Field(i).Name() {
		case "Status":
			module.record.status = fields.Field(i)
		case "Phase":
			module.record.phase = fields.Field(i)
		}
	}
	if module.record.status == nil || module.record.phase == nil {
		return nil, errors.New("internal/runstate's State has no Status or no Phase field; the sweep has to be told what a run's saved status and step are now")
	}
	return module, nil
}

// typeCheckListed parses and type-checks one of the module's packages from its
// source, reading what it imports from the compiled types.
func typeCheckListed(t *testing.T, module *moduleTypes, listed listedPackage) ([]*ast.File, *types.Info) {
	t.Helper()
	var files []*ast.File
	for _, name := range listed.GoFiles {
		parsed, err := parser.ParseFile(module.fileSet, filepath.Join(listed.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	return files, typeCheck(t, module, listed.ImportPath, files)
}

func typeCheck(t *testing.T, module *moduleTypes, path string, files []*ast.File) *types.Info {
	t.Helper()
	info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Importer: module.importer}
	if _, err := config.Check(path, module.fileSet, files, info); err != nil {
		t.Fatalf("type-check %s: %v", path, err)
	}
	return info
}

// fixtureRunRecordSites runs the second sweep over source written for a test,
// as if it were the file name in the module.
func fixtureRunRecordSites(t *testing.T, module *moduleTypes, name, source string) map[outsideRunRecordSite]token.Position {
	t.Helper()
	parsed, err := parser.ParseFile(module.fileSet, name, source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	info := typeCheck(t, module, module.path+"/"+filepath.Dir(name), []*ast.File{parsed})
	return outsideRunRecordSites(module.fileSet, ".", []*ast.File{parsed}, info, module.record)
}
