package runstate

// The account of every routine reader that still reads every run record.
//
// The design for one owner for each guarantee
// (docs/designs/one-owner-for-each-guarantee.md, guarantee 8 and "Checks run by
// make test") says routine reads never walk all kept history: the runs in
// flight, the runs owing a step, and one item's runs are read through the run
// index (runindex.go), and a finished stream's spend through what was kept of
// it (streamspendindex.go). Only rebuilding an index and maintenance may read
// every record. Much code still does, so this sweep holds it to a list of the
// places that still do, and the list only shrinks: each item that moves a
// reader onto an index takes its entry off, and the list goes when it is empty.
//
// What reads every record is worked out rather than written down. It starts
// from the store's full listing, scan and scanning, and adds every method of
// the run store that calls one of them, until nothing more is added: Recorded,
// RecordedReadable, Prices, Shipped, and the rest are found that way, and a new
// method that lists every record is found the day it is written. A site is then
// any top-level function, in any package of the module, that calls one of
// those methods — on the store itself, or on an interface whose method of that
// name has the store's signature, which is how most readers hold it. Inside
// this package the methods themselves are not sites: each one's own callers
// are.
//
// The spend report is not one of them: it reads the logs of the streams still
// going and of the ones finished since the last report, and what was kept for
// the rest.
//
// A site is the file and the top-level function it is in, never a line, so an
// edit elsewhere leaves the list alone. The list is checked both ways: a site
// not on it fails, and so does an entry that matches no site.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// everyRecordListings is where working out what reads every record starts:
// the store's own walk over its directory.
var everyRecordListings = []string{"scan", "scanning"}

// everyRecordReadersAllowed is every function outside the list that may read
// every record, and why: maintenance, and test support nothing the harness
// runs. Rebuilding the run index reads every record too, one by one by name
// (buildRunIndex, behind RebuildRunIndex and an index found missing), and is
// not a site here because it is how the index every routine read uses is made.
var everyRecordReadersAllowed = map[everyRecordSite]string{
	{"internal/orchestrator/testharness/outcome.go", "OnlyRecordedRun"}: "test support: the orchestrator's tests read what a run left behind through it, and nothing the harness runs calls it",
	{"internal/runstate/homemigrate.go", "RewriteMigratedWorktrees"}:    "moves every run's recorded worktree path once, when the state home is moved; it is maintenance, run by the move and never by a routine pass",
}

// everyRecordSite is one top-level function that reads every run record.
type everyRecordSite struct {
	file     string // from the module's root, with forward slashes
	function string
}

// everyRecordReaders is every routine reader that still reads every run
// record. It only shrinks.
var everyRecordReaders = []everyRecordSite{
	{"internal/cli/account.go", "(accountPool).ChooseAccount"},
	{"internal/cli/cost.go", "readPrices"},
	{"internal/cli/exchangeevidence.go", "(exchangeEvidenceReaders).pendingDocument"},
	{"internal/cli/exchangeevidence.go", "(exchangeEvidenceReaders).publication"},
	{"internal/cli/intervention.go", "listInterventions"},
	{"internal/cli/passevents.go", "runEvents"},
	{"internal/cli/status.go", "reportRunStatus"},
	{"internal/cli/statusshipped.go", "reportShipped"},
	{"internal/cli/work.go", "(conversationWork).Price"},
	{"internal/cost/cost.go", "(Ledger).Record"},
	{"internal/cost/cost.go", "(Ledger).RecordAll"},
	{"internal/forgehygiene/forgehygiene.go", "(Sweeper).workItems"},
	{"internal/orchestrator/carryout.go", "(CarryOut).applicableDecision"},
	{"internal/orchestrator/carryout.go", "(CarryOut).howRunEnded"},
	{"internal/orchestrator/carryout.go", "onceRecorded"},
	{"internal/orchestrator/claims.go", "(ClaimAuditor).Audit"},
	{"internal/orchestrator/converge.go", "(Reconciler).Converge"},
	{"internal/orchestrator/documentpublication.go", "(Pipeline).ResumableDocuments"},
	{"internal/orchestrator/escalationended.go", "(Reconciler).EndEscalations"},
	{"internal/orchestrator/mergedisagreement.go", "(Reconciler).FindMergeDisagreements"},
	{"internal/orchestrator/publication.go", "(Reconciler).FinishPublications"},
	{"internal/orchestrator/publication.go", "(Reconciler).RecoverPublications"},
	{"internal/orchestrator/publication.go", "(Reconciler).RefreshPublications"},
	{"internal/orchestrator/reconcilefinding.go", "(Reconciler).maintainReconcileFindings"},
	{"internal/orchestrator/recoveryapplicability.go", "(CarryOut).settledNow"},
	{"internal/orchestrator/redtarget.go", "(Reconciler).ResumeRedTargets"},
	{"internal/orchestrator/repaircontinue.go", "(RepairContinuer).DeliverNotes"},
	{"internal/orchestrator/repaircontinue.go", "(RepairContinuer).carriedOut"},
	{"internal/orchestrator/runretirement.go", "(RunRetirer).Retire"},
	{"internal/orchestrator/schedule.go", "(Scheduler).usageWindow"},
	{"internal/orchestrator/schedule.go", "keptChange"},
	{"internal/orchestrator/schedule.go", "priceRun"},
	{"internal/orchestrator/triage.go", "(Docketer).Build"},
	{"internal/readmodel/accomplishment.go", "(*accomplishmentRead).runs"},
	{"internal/readmodel/capacityblocked.go", "CapacityBlockedOf"},
	{"internal/readmodel/delivery.go", "readDeliverySources"},
	{"internal/readmodel/factorystall.go", "ReadFactoryStall"},
	{"internal/readmodel/found.go", "LookForPrices"},
	{"internal/readmodel/found.go", "LookForSummaries"},
	{"internal/readmodel/holds.go", "HeldForAPerson"},
	{"internal/readmodel/operatoraction.go", "readOperatorActions"},
	{"internal/readmodel/standing.go", "readNeedsHuman"},
	{"internal/readmodel/standing.go", "readRunning"},
	{"internal/readmodel/throughput.go", "ReadThroughput"},
	{"internal/readmodel/unrunchecks.go", "ReadUnrunChecks"},
	{"internal/slack/feed.go", "(*HarnessFeed).Poll"},
	{"internal/watchdog/factory.go", "(*FactoryWatch).Check"},
	{"internal/watchdog/watchdog.go", "(Checker).Check"},
}

func TestNoRoutineReaderReadsEveryRunRecordUnlisted(t *testing.T) {
	module := loadModuleTypes(t)
	var own listedPackage
	for _, listed := range module.listed {
		if listed.ImportPath == module.path+"/internal/runstate" {
			own = listed
		}
	}
	if own.ImportPath == "" {
		t.Fatal("go list did not list internal/runstate")
	}
	ownFiles, ownInfo := typeCheckBounded(t, module, own)
	readers := everyRecordMethods(ownFiles, ownInfo, module.path)
	found := map[everyRecordSite]token.Position{}
	for _, listed := range module.listed {
		files, info := ownFiles, ownInfo
		if listed.ImportPath != own.ImportPath {
			files, info = typeCheckBounded(t, module, listed)
		}
		for site, at := range everyRecordSites(module.fileSet, module.root, files, info, module.path, readers) {
			found[site] = at
		}
	}
	for _, problem := range everyRecordProblems(found, everyRecordReaders, everyRecordReadersAllowed) {
		t.Error(problem)
	}
}

// TestTheBoundedReadSweepNamesAnAddedReader holds the sweep to the case it
// exists for, over source written here, so a sweep that stopped seeing anything
// fails here rather than passing above.
func TestTheBoundedReadSweepNamesAnAddedReader(t *testing.T) {
	module := loadModuleTypes(t)
	var own listedPackage
	for _, listed := range module.listed {
		if listed.ImportPath == module.path+"/internal/runstate" {
			own = listed
		}
	}
	ownFiles, ownInfo := typeCheckBounded(t, module, own)
	readers := everyRecordMethods(ownFiles, ownInfo, module.path)
	for _, want := range []string{"Recorded", "RecordedReadable", "Prices"} {
		if _, ok := readers[want]; !ok {
			t.Errorf("the sweep did not work out that %s reads every record; it found %v", want, readers)
		}
	}
	for _, indexed := range []string{"Incomplete", "Outstanding", "Runs", "Latest", "ReviewRounds"} {
		if _, ok := readers[indexed]; ok {
			t.Errorf("the sweep says %s reads every record, and it reads the index", indexed)
		}
	}

	const name = "internal/fixture/reader.go"
	parsed, err := parser.ParseFile(module.fileSet, name, `package fixture

import "`+module.path+`/internal/runstate"

type runs interface{ Recorded() ([]runstate.State, error) }

type other interface{ Recorded() bool }

func Everything(store *runstate.Store) int { all, _ := store.Recorded(); return len(all) }

func Through(source runs) int { all, _ := source.Recorded(); return len(all) }

func Priced(store *runstate.Store) { _, _ = store.Prices() }

func Indexed(store *runstate.Store) { _, _ = store.Incomplete() }

func NotARunStore(source other) bool { return source.Recorded() }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info := checkBounded(t, module, module.path+"/internal/fixture", []*ast.File{parsed})
	found := everyRecordSites(module.fileSet, ".", []*ast.File{parsed}, info, module.path, readers)
	problems := strings.Join(everyRecordProblems(found, nil, nil), "\n")
	for _, want := range []string{"in Everything,", "in Through,", "in Priced,"} {
		if !strings.Contains(problems, want) {
			t.Errorf("the sweep did not report %q; it reported:\n%s", want, problems)
		}
	}
	for _, unwanted := range []string{"in Indexed,", "in NotARunStore,"} {
		if strings.Contains(problems, unwanted) {
			t.Errorf("the sweep reported %q, which reads no full listing:\n%s", unwanted, problems)
		}
	}
	// The other half: an entry whose reader has moved onto an index fails
	// until it is taken off.
	moved := everyRecordProblems(found, []everyRecordSite{{name, "Indexed"}}, nil)
	if !strings.Contains(strings.Join(moved, "\n"), "in Indexed, is on everyRecordReaders") {
		t.Errorf("the sweep did not refuse a listed entry with no site; it reported:\n%s", strings.Join(moved, "\n"))
	}
}

// typeCheckBounded parses and type-checks one of the module's packages as
// typeCheckListed does, keeping what each declaration defines as well, which
// working out the run store's methods needs.
func typeCheckBounded(t *testing.T, module *moduleTypes, listed listedPackage) ([]*ast.File, *types.Info) {
	t.Helper()
	var files []*ast.File
	for _, name := range listed.GoFiles {
		parsed, err := parser.ParseFile(module.fileSet, filepath.Join(listed.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	return files, checkBounded(t, module, listed.ImportPath, files)
}

func checkBounded(t *testing.T, module *moduleTypes, path string, files []*ast.File) *types.Info {
	t.Helper()
	info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}, Defs: map[*ast.Ident]types.Object{}}
	config := types.Config{Importer: module.importer}
	if _, err := config.Check(path, module.fileSet, files, info); err != nil {
		t.Fatalf("type-check %s: %v", path, err)
	}
	return info
}

// isRunStore reports the run store's type, *Store or Store, however the
// package declaring it was read: from its source here, or from its compiled
// types everywhere else.
func isRunStore(typ types.Type, modulePath string) bool {
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	named, ok := typ.(*types.Named)
	return ok && named.Obj().Name() == "Store" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == modulePath+"/internal/runstate"
}

// everyRecordMethods works out which of the run store's methods read every
// record, with each one's signature, from this package's source.
func everyRecordMethods(files []*ast.File, info *types.Info, modulePath string) map[string]string {
	readers := map[string]string{}
	for _, name := range everyRecordListings {
		readers[name] = ""
	}
	type method struct {
		name      string
		signature string
		calls     map[string]bool
	}
	var methods []method
	for _, file := range files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil || !strings.HasPrefix(functionName(function), "(*Store).") {
				continue
			}
			found := method{name: function.Name.Name, calls: map[string]bool{}}
			if object := info.Defs[function.Name]; object != nil {
				found.signature = signatureOf(object.Type())
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if selection := info.Selections[selector]; selection != nil && selection.Kind() == types.MethodVal && isRunStore(selection.Recv(), modulePath) {
						found.calls[selector.Sel.Name] = true
					}
				}
				return true
			})
			methods = append(methods, found)
		}
	}
	for added := true; added; {
		added = false
		for _, candidate := range methods {
			if _, known := readers[candidate.name]; known {
				continue
			}
			for called := range candidate.calls {
				if _, reads := readers[called]; reads {
					readers[candidate.name] = candidate.signature
					added = true
					break
				}
			}
		}
	}
	return readers
}

// signatureOf is a method's parameters and results, with every type named by
// its package's full path, so a method read from source and one read from
// compiled types compare alike.
func signatureOf(typ types.Type) string {
	signature, ok := typ.(*types.Signature)
	if !ok {
		return ""
	}
	qualifier := func(pkg *types.Package) string { return pkg.Path() }
	return types.TypeString(types.NewSignatureType(nil, nil, nil, signature.Params(), signature.Results(), signature.Variadic()), qualifier)
}

// everyRecordSites finds the top-level functions in files that call a method
// in readers, keyed by their file's path from root. The run store's own
// methods that read every record are not sites: each is a way in, and its
// callers are the sites.
func everyRecordSites(fileSet *token.FileSet, root string, files []*ast.File, info *types.Info, modulePath string, readers map[string]string) map[everyRecordSite]token.Position {
	found := map[everyRecordSite]token.Position{}
	reads := func(selector *ast.SelectorExpr) bool {
		selection := info.Selections[selector]
		if selection == nil || selection.Kind() != types.MethodVal {
			return false
		}
		signature, known := readers[selector.Sel.Name]
		if !known {
			return false
		}
		if isRunStore(selection.Recv(), modulePath) {
			return true
		}
		return types.IsInterface(selection.Recv()) && signature != "" && signatureOf(selection.Obj().Type()) == signature
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
				if _, way := readers[declared.Name.Name]; way && strings.HasPrefix(function, "(*Store).") {
					continue
				}
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && reads(selector) {
					site := everyRecordSite{file: name, function: function}
					if _, seen := found[site]; !seen {
						found[site] = fileSet.Position(selector.Pos())
					}
				}
				return true
			})
		}
	}
	return found
}

// everyRecordProblems returns one sentence for each site found that is neither
// listed nor allowed, each listed entry matching no site, and each entry
// listed twice.
func everyRecordProblems(found map[everyRecordSite]token.Position, listed []everyRecordSite, allowed map[everyRecordSite]string) []string {
	var problems []string
	onList := map[everyRecordSite]bool{}
	for _, entry := range listed {
		if onList[entry] {
			problems = append(problems, fmt.Sprintf("%s, in %s, is listed twice in everyRecordReaders; list it once.", entry.file, entry.function))
		}
		onList[entry] = true
		if _, ok := found[entry]; !ok {
			problems = append(problems, fmt.Sprintf("%s, in %s, is on everyRecordReaders as a reader of every run record, and it no longer is one: take it off the list, which only ever shrinks.", entry.file, entry.function))
		}
	}
	for entry, why := range allowed {
		if strings.TrimSpace(why) == "" {
			problems = append(problems, fmt.Sprintf("%s, in %s, is allowed to read every run record without saying why.", entry.file, entry.function))
		}
		if _, ok := found[entry]; !ok {
			problems = append(problems, fmt.Sprintf("%s, in %s, is allowed to read every run record and no longer does: take it out of everyRecordReadersAllowed.", entry.file, entry.function))
		}
	}
	for site, at := range found {
		if onList[site] {
			continue
		}
		if _, ok := allowed[site]; ok {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s, in %s, reads every run record (at line %d), and is not on everyRecordReaders. A routine read asks the run index instead: Incomplete for the runs in flight, Outstanding for the runs owing a step, and Runs or Latest for one item's runs. Where a question needs something the index does not hold, add it to the index rather than reading every record.", site.file, site.function, at.Line))
	}
	sort.Strings(problems)
	return problems
}
