package maystart

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// launch says why a place that can launch or carry on a run may do it: the
// closed list's paths it is, or, where it decides nothing about starting, why
// not.
type launch struct {
	paths []Path
	why   string
}

func starting(paths ...Path) launch { return launch{paths: paths} }
func because(why string) launch     { return launch{why: why} }

const (
	insideARun    = "a step inside a run that began at a place listed here"
	makesPipeline = "makes the pipeline another place here starts a run on"
	readsPipeline = "uses a pipeline's reading of the configuration, and starts no run"
)

// launches is every place in the module that can launch a developer run or take
// one up again, each as its file, its function and what it uses.
var launches = map[string]launch{
	// Where the work is chosen and handed to the pipeline.
	"internal/cli/run.go runWorkItem buildPipeline":                          starting(NamedRun, PickUpRun),
	"internal/cli/run.go runWorkItem pipeline.Run":                           starting(NamedRun, PickUpRun),
	"internal/cli/run.go buildPipeline pipelineFrom":                         because(makesPipeline),
	"internal/cli/run.go buildPipeline Pipeline{}":                           because(makesPipeline),
	"internal/cli/run.go pipelineFrom Pipeline{}":                            because(makesPipeline),
	"internal/cli/work.go newConversationWork pipelineFrom":                  starting(NamedRun, PickUpRun),
	"internal/cli/work.go conversationWork.Run pipeline.Run":                 starting(NamedRun, PickUpRun),
	"internal/cli/schedule.go openPull pipelineFrom":                         because(readsPipeline),
	"internal/cli/schedule.go openPull pipelineFrom (2)":                     starting(ReadyWork, HoldTest, PickUpRun),
	"internal/cli/schedule.go openPull pipeline.Run":                         starting(ReadyWork, HoldTest, PickUpRun),
	"internal/cli/triage.go rerunnerFrom pipelineFrom":                       starting(DecidedRerun),
	"internal/cli/triage.go rerunnerFrom pipeline.Run":                       starting(DecidedRerun),
	"internal/cli/ground.go feedbackRepairerFrom pipelineFrom":               starting(FeedbackRepair),
	"internal/cli/ground.go feedbackRepairerFrom pipeline.Run":               starting(FeedbackRepair),
	"internal/cli/triage.go repairContinuerFrom pipelineFrom":                starting(DecidedRepair),
	"internal/cli/triage.go repairContinuerFrom pipeline.Continue":           starting(DecidedRepair),
	"internal/cli/triage.go integrationResumerFrom pipelineFrom":             starting(FinishMerge),
	"internal/cli/triage.go integrationResumerFrom pipeline.Continue":        starting(FinishMerge),
	"internal/cli/triage.go stallContinuerFrom pipelineFrom":                 starting(ContinueRun),
	"internal/cli/triage.go stallContinuerFrom pipeline.Continue":            starting(ContinueRun),
	"internal/cli/triage.go checkStageContinuerFrom pipelineFrom":            starting(ContinueRun, AfterLogin),
	"internal/cli/triage.go checkStageContinuerFrom pipeline.Continue":       starting(ContinueRun, AfterLogin),
	"internal/cli/reconcile.go continueWaitFrom pipelineFrom":                starting(PickUpRun),
	"internal/cli/reconcile.go continueWaitFrom pipeline.Continue":           starting(PickUpRun),
	"internal/cli/reconcile.go continueFeedbackRepairFrom pipelineFrom":      starting(FeedbackRepair),
	"internal/cli/reconcile.go continueFeedbackRepairFrom pipeline.Continue": starting(FeedbackRepair),
	"internal/cli/reconcile.go continueUpdateFrom pipelineFrom":              starting(UpdateQueued, UpdateForPerson, BringUpToDate),
	"internal/cli/reconcile.go continueUpdateFrom pipeline.Continue":         starting(UpdateQueued, UpdateForPerson, BringUpToDate),
	"internal/cli/forgecheck.go teamBranchUpdaterFrom pipelineFrom":          starting(ResolveConflict),
	"internal/cli/schedule.go outsideFiles.Next pipelineFrom":                starting(BringInFile),
	"internal/cli/schedule.go outsideFiles.BringIn pipelineFrom":             starting(BringInFile),
	"internal/cli/schedule.go outsideFiles.BringIn BringInOutsideFile":       starting(BringInFile),
	"internal/cli/schedule.go waitingDocuments.Publish pipelineFrom":         starting(PublishDocument),
	"internal/cli/chat.go preparedChat.open pipelineFrom":                    starting(PublishDocument),
	"internal/cli/artifact.go approveArtifact pipelineFrom":                  starting(PublishDocument),

	// Where a run is reserved, made live again or taken up.
	"internal/orchestrator/pipeline.go Pipeline.Run reserveRun":                                  starting(ReadyWork, NamedRun, HoldTest, DecidedRerun, FeedbackRepair),
	"internal/orchestrator/pipeline.go Pipeline.Run develop":                                     starting(ReadyWork, NamedRun, HoldTest, DecidedRerun, FeedbackRepair),
	"internal/orchestrator/pipeline.go Pipeline.Run AdoptWithin":                                 starting(PickUpRun),
	"internal/orchestrator/pipeline.go Pipeline.Run reclaimSlot":                                 starting(PickUpRun),
	"internal/orchestrator/pipeline.go Pipeline.Run resumeRun":                                   starting(PickUpRun),
	"internal/orchestrator/pipeline.go Pipeline.Continue AdoptWithin":                            starting(PickUpRun),
	"internal/orchestrator/pipeline.go Pipeline.Continue reclaimSlot":                            starting(PickUpRun),
	"internal/orchestrator/pipeline.go Pipeline.Continue resumeRun":                              starting(PickUpRun),
	"internal/orchestrator/teambranchresolve.go teamBranchRun.resolve ResolveTeamBranchConflict": starting(ResolveConflict),
	"internal/orchestrator/teambranchresolve.go Pipeline.ResolveTeamBranchConflict reserveRun":   starting(ResolveConflict),
	"internal/orchestrator/teambranchresolve.go activeRun.resolveTeamConflict develop":           starting(ResolveConflict),
	"internal/orchestrator/teambranchresolve.go Pipeline.continueTeamBranchConflict AdoptWithin": starting(ResolveConflict),
	"internal/orchestrator/teambranchresolve.go Pipeline.continueTeamBranchConflict reclaimSlot": starting(ResolveConflict),
	"internal/orchestrator/outsidefilelanding.go Pipeline.BringInOutsideFile reserveRun":         starting(BringInFile),
	"internal/orchestrator/documentpublication.go Pipeline.publishDocumentAttempt reserveRun":    starting(PublishDocument),
	"internal/orchestrator/repaircontinue.go RepairContinuer.saveContinuation ReserveLive":       starting(DecidedRepair),
	"internal/orchestrator/forgefeedback.go Reconciler.takeUpFeedback ReserveLive":               starting(FeedbackRepair),
	"internal/orchestrator/integrationresume.go IntegrationResumer.supersedeOnRun ReserveLive":   starting(FinishMerge),
	"internal/orchestrator/stallcontinue.go StallContinuer.Continue ReserveLive":                 starting(ContinueRun),
	"internal/orchestrator/checkstagecontinue.go CheckStageContinuer.Continue ReserveLive":       starting(ContinueRun),
	"internal/orchestrator/githubidentity.go GitHubIdentityStops.takeUpPublishing ReserveLive":   starting(AfterLogin),
	"internal/orchestrator/mergequeuecatchup.go MergeQueueRunHandback.catchUp ReserveLive":       starting(BringUpToDate),
	"internal/orchestrator/queuedchecks.go Reconciler.updateQueuedHead ReserveLive":              starting(UpdateQueued),
	"internal/orchestrator/personmerge.go Reconciler.updatePersonMerge ReserveLive":              starting(UpdateForPerson),

	// What is named like a launch and begins nothing.
	"internal/orchestrator/pipeline.go Pipeline.reserveRun Reserve":                          because(insideARun),
	"internal/orchestrator/pipeline.go Pipeline.reserveRun ReserveDocumentLanding":           because(insideARun),
	"internal/orchestrator/pipeline.go Pipeline.reserveRun ReserveOutsideFileLanding":        because(insideARun),
	"internal/orchestrator/pipeline.go Pipeline.reclaimSlot ReclaimSlot":                     because(insideARun),
	"internal/orchestrator/pipeline.go Pipeline.resumeRun develop":                           because(insideARun),
	"internal/orchestrator/pipeline.go activeRun.reconcile develop":                          because(insideARun),
	"internal/orchestrator/pipeline.go activeRun.repair develop":                             because(insideARun),
	"internal/orchestrator/pipeline.go activeRun.attemptDevelopment developer session":       because(insideARun),
	"internal/orchestrator/personmergeupdate.go activeRun.handUpToDateConflict develop":      because(insideARun),
	"internal/orchestrator/actions.go deliverySteps develop":                                 because(insideARun),
	"internal/orchestrator/pipeline.go Pipeline.waitInFlightOnDependencies AdoptWithin":      because("records on a run that it waits, and starts nothing"),
	"internal/orchestrator/teambranchresolve.go Pipeline.endWaitingTeamConflict AdoptWithin": because("ends a run that waits, and starts nothing"),
	"internal/runstate/store.go Store.Adopt AdoptWithin":                                     because("another name for AdoptWithin, which nothing in the module uses"),
	"internal/orchestrator/recurring.go Trigger.adopted Adopt":                               because("takes up a recurring task's record rather than a run"),
	"internal/teamrecords/teamrecords.go Sharer.copyIn Adopt":                                because("takes in a shared directive rather than a run"),
	"internal/cli/run.go mergeQueueDriverFrom pipelineFrom":                                  because("checks, reviews and lands changes already in the merge queue"),
	"internal/cli/schedule.go openCheckRunner pipelineFrom":                                  because(readsPipeline),
	"internal/cli/mergecheck.go mergeCheck Pipeline{}":                                       because(readsPipeline),
	"internal/orchestrator/branchreview.go BranchReviewer.agentName Pipeline{}":              because(readsPipeline),
	"internal/orchestrator/branchreview.go BranchReviewer.collectReports Pipeline{}":         because(readsPipeline),
	"internal/orchestrator/spend.go BranchReviewer.spendAttribution Pipeline{}":              because(readsPipeline),
	"internal/orchestrator/developerrouting.go SlotPairsFor Pipeline{}":                      because(readsPipeline),
	"scripts/validationfixture/land.go landIntent Pipeline{}":                                because("a script that lands a test repository's own documents"),
}

// launchNames are the functions that reserve a run, take its slot, take it up,
// or invoke its developer. Naming one is a use of it, called or not.
var launchNames = []string{
	"Reserve", "ReserveLive", "ReserveOutsideFileLanding", "ReserveDocumentLanding", "ReclaimSlot",
	"Adopt", "AdoptWithin", "reserveRun", "reclaimSlot", "resumeRun", "develop",
	"ResolveTeamBranchConflict", "BringInOutsideFile", "pipelineFrom", "buildPipeline",
}

// TestEveryLaunchOfADeveloperRunIsOnTheClosedList reads every Go file of the
// module, tests and the packages tests share aside, and fails on a use of one of
// launchNames, a start of a pipeline, a pipeline made, or a developer session
// asked of a provider, that launches does not list; on a listed one that is
// gone; and on one listed under a path the closed list does not have. It reads
// names and not types, so a new way of reserving a run is added to launchNames.
func TestEveryLaunchOfADeveloperRunIsOnTheClosedList(t *testing.T) {
	t.Parallel()
	found := usesInModule(t, launchesIn)
	for _, problem := range unlisted(found, launches) {
		t.Error(problem)
	}
	listed := map[Path]bool{}
	for _, place := range launches {
		for _, path := range place.paths {
			listed[path] = true
		}
	}
	for _, entry := range Paths() {
		if entry.starts == startsRun && !listed[entry.Path] {
			t.Errorf("no place in the code is listed as starting %q", entry.Path)
		}
	}
}

// usesInModule is each use a finder sees in the module's Go files, tests and the
// packages tests share aside.
func usesInModule(t *testing.T, usesIn func(name string, source []byte) ([]string, error)) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	found := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "testharness" || name == "orchestratortest") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		read++
		uses, err := usesIn(filepath.ToSlash(relative), source)
		for _, use := range uses {
			found[use] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if read < 100 {
		t.Fatalf("only %d files were read, so this is looking in the wrong place", read)
	}
	return found
}

// unlisted is what is wrong between the places found and the places listed.
func unlisted(found map[string]bool, listed map[string]launch) []string {
	var problems []string
	for use := range found {
		if _, ok := listed[use]; !ok {
			problems = append(problems, fmt.Sprintf("%s can start work and is not listed: put the way it starts on the closed list (paths.go), unwired until the architect rules on it, and list it here", use))
		}
	}
	for use, place := range listed {
		if !found[use] {
			problems = append(problems, use+" is listed and no longer there; take it off the list")
		}
		if (len(place.paths) == 0) == (place.why == "") {
			problems = append(problems, use+" needs either the paths it is or why it starts nothing")
		}
		for _, path := range place.paths {
			if path.starts() == 0 {
				problems = append(problems, fmt.Sprintf("%s is listed under %q, which is not on the closed list", use, path))
			}
		}
	}
	slices.Sort(problems)
	return problems
}

func TestTheLaunchSweepSeesEachWayIn(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]string{
		`func f() { p.Store.Reserve(ctx, state, 1) }`:                                       "x.go f Reserve",
		`func (c Continuer) Go() { c.Runs.ReserveLive(ctx, state, 1) }`:                     "x.go Continuer.Go ReserveLive",
		`func f() { keep := store.AdoptWithin; _ = keep }`:                                  "x.go f AdoptWithin",
		`func (a *activeRun) f() error { return a.develop(ctx, "", "") }`:                   "x.go activeRun.f develop",
		`func f() { pipeline.Run(ctx, id) }`:                                                "x.go f pipeline.Run",
		`func f() { w.pipeline.Continue(ctx, id, run) }`:                                    "x.go f pipeline.Continue",
		`func f() { other := pipelineFrom(parts); other.Run(ctx, id) }`:                     "x.go f pipelineFrom",
		`func f() { other := orchestrator.Pipeline{}; other.Run(ctx, id) }`:                 "x.go f Pipeline{}",
		`func f() { provider.Run(ctx, backend.RunRequest{Role: domain.RoleDeveloper}) }`:    "x.go f developer session",
		`var start = func() { p.ResolveTeamBranchConflict(ctx, request) }`:                  "x.go  ResolveTeamBranchConflict",
		`func f() { pipeline.Run(ctx, one); pipeline.Run(ctx, two) }`:                       "x.go f pipeline.Run; x.go f pipeline.Run (2)",
		`func f() { command.Run(); provider.Run(ctx, backend.RunRequest{Role: reviewer}) }`: "",
	} {
		uses, err := launchesIn("x.go", []byte("package planted\n"+source+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(uses, "; ") != want {
			t.Errorf("%s: found %q, want %q", source, uses, want)
		}
		if want != "" && len(unlisted(map[string]bool{want: true}, nil)) != 1 {
			t.Errorf("%s: a launch that is not listed passes", source)
		}
	}
	if problems := unlisted(map[string]bool{"x": true}, map[string]launch{"x": starting("no-such-path")}); len(problems) != 1 {
		t.Errorf("a launch on a path that is not on the closed list passes: %q", problems)
	}
	if problems := unlisted(nil, map[string]launch{"x": starting(NamedRun)}); len(problems) != 1 {
		t.Errorf("a listed launch that is gone passes: %q", problems)
	}
}

// launchesIn is each launch in a source file, in the order first met, named by
// the file, the function it is in and what it uses, and by which use of it in
// that function this is after the first.
func launchesIn(name string, source []byte) ([]string, error) {
	return usesIn(name, source, func(node ast.Node, add func(string)) {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			chosen := node.Sel.Name
			if slices.Contains(launchNames, chosen) {
				add(chosen)
			}
			if (chosen == "Run" || chosen == "Continue") && pipelineNamed(node.X) {
				add("pipeline." + chosen)
			}
		case *ast.CallExpr:
			if called, ok := node.Fun.(*ast.Ident); ok && slices.Contains(launchNames, called.Name) {
				add(called.Name)
			}
		case *ast.CompositeLit:
			switch made := lastName(node.Type); {
			case made == "Pipeline":
				add("Pipeline{}")
			case made == "RunRequest" && asksForADeveloper(node):
				add("developer session")
			}
		}
	})
}

// usesIn is each use that look adds, named as launchesIn says.
func usesIn(name string, source []byte, look func(node ast.Node, add func(what string))) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var uses []string
	for _, declared := range parsed.Decls {
		within := ""
		var body ast.Node = declared
		if function, ok := declared.(*ast.FuncDecl); ok {
			within, body = function.Name.Name, function.Body
			if function.Recv != nil && len(function.Recv.List) == 1 {
				within = lastName(function.Recv.List[0].Type) + "." + within
			}
		}
		if body == ast.Node((*ast.BlockStmt)(nil)) {
			continue
		}
		add := func(what string) {
			use := name + " " + within + " " + what
			for again := 2; slices.Contains(uses, use); again++ {
				use = fmt.Sprintf("%s %s %s (%d)", name, within, what, again)
			}
			uses = append(uses, use)
		}
		ast.Inspect(body, func(node ast.Node) bool {
			if _, ok := node.(*ast.InterfaceType); ok {
				return false // declares methods and calls none
			}
			look(node, add)
			return true
		})
	}
	return uses, nil
}

// pipelineNamed reports whether an expression is, by its name, a pipeline.
func pipelineNamed(expression ast.Expr) bool {
	if call, ok := expression.(*ast.CallExpr); ok {
		expression = call.Fun
	}
	return strings.Contains(strings.ToLower(lastName(expression)), "pipeline")
}

func asksForADeveloper(request *ast.CompositeLit) bool {
	for _, element := range request.Elts {
		if field, ok := element.(*ast.KeyValueExpr); ok && lastName(field.Key) == "Role" && lastName(field.Value) == "RoleDeveloper" {
			return true
		}
	}
	return false
}

// lastName is the last identifier of a name, however it is qualified.
func lastName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	case *ast.StarExpr:
		return lastName(expression.X)
	}
	return ""
}
