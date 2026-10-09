package landing_test

// The audit of every place this repository closes a work item, held to the code
// it describes.
//
// Closure is the one act a landing decides. A run that lands evidence integrates
// exactly as any other run does and leaves its item open, so a closure taken
// without reading the claim is a false closure — the class that closed
// yoyodyne-ifd.284 against a diagnosis saying in its own words that the work was
// not doable yet, and that yoyodyne-ifd.209.23 and .209.24 shipped the claim to
// stop. What those two could not ship is a guarantee that the next closure route
// somebody writes reads it, because the guarantee is about code that does not
// exist yet.
//
// Two readers decide it now rather than one: the developer's claim, and the
// reviewer's approval, which since yoyodyne-ifd.209.27 says whether it approves
// the work or evidence. Either one withholds the closure, so what a settlement
// has to consult is the question over both of them rather than either half.
//
// So every call that closes an item is listed below with what makes it safe, and
// this fails on one that is not listed. Three kinds are safe and the audit says
// which each is: a run's own settlement, which must ask whether the change
// discharges the item; an act a person took, which is neither a landing nor a
// review and has nothing to consult; and a landing the document's owner
// recorded for an item no run carried, which consults that record.
// The consult is checked mechanically rather than taken from the row — a row can
// claim a guard the declaration lost — so a settlement that stops asking fails
// here even though its row still reads correctly.
//
// What the sweep sees is every `Complete` call that is passed something,
// anywhere in the swept trees and on any receiver, so a closure written through
// a local or a differently named field is caught rather than missed. What it
// does not see is a closure that reaches the tracker under another name — a
// wrapper called `Finish`, a `bd close` shelled out to directly, a status set
// through `Update`. Those are not hypothetical categories to guard here; they
// are what a reader of this file should know it is not watching for.
//
// `docs/diagnoses/yoyodyne-ifd-209-26-closure-routes.md` is the audit written
// out, with how yoyodyne-ifd.284 came to be closed.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repositoryRoot is the checkout this test reads, reached from the package
// directory. The code is swept where it actually lives, because a copy is
// exactly what cannot carry the route somebody added.
const repositoryRoot = "../.."

// closureTrees are the source trees swept. Everything the harness runs is in one
// of them, and a tree added later is one this has to be told about, which is a
// change somebody makes on purpose.
var closureTrees = []string{"internal", "cmd"}

// closureKind is what makes one closure site safe.
type closureKind string

const (
	// kindSettlement is a run settling its own work item. It closes only where the
	// change discharges the item, and the declaration it sits in has to ask so.
	kindSettlement closureKind = "settlement"
	// kindHuman is a person closing an item themselves, through the product
	// manager's conversation. There is no landing to consult: nothing integrated,
	// no developer claimed anything, and the decision is the one the operator's
	// role is for.
	kindHuman closureKind = "human"
	// kindRecordedLanding is the harness closing an item no run ever carried, on
	// the landing its owner recorded: a revision of a document the item's
	// conversation role owns, made by that role, whose reason opens with the
	// item's identifier. There is no developer claim and no review to consult,
	// because there was no run; what is consulted is the owner's own record that
	// the item's work is in the document, which is what the product manager read
	// by hand before yoyodyne-ifd.367.
	kindRecordedLanding closureKind = "recorded-landing"
	// kindUnrelated is a call the sweep's shape catches that closes no work item.
	// There are none today, and the row exists so that the next one is excluded by
	// somebody writing down why rather than by the sweep quietly not seeing it.
	kindUnrelated closureKind = "unrelated"
)

// closureSite is one declaration that closes a work item, and what makes it safe
// there.
type closureSite struct {
	File        string
	Declaration string
	// Calls is how many closures the declaration takes, so a second one added to a
	// declaration already listed is audited rather than covered by the first.
	Calls int
	// ConsultsDischarge is whether the declaration asks whether the change
	// discharges the item — the whole question, over the developer's claim and the
	// reviewer's approval together. It is swept from the source rather than
	// asserted here, so the audit cannot claim a guard the code no longer has.
	ConsultsDischarge bool
	// Kind is which of the two safe shapes this is.
	Kind closureKind
	// Why is what makes it safe, in prose, because the question a reader has to
	// settle is whether this site is a run's settlement or a person's decision and
	// no sweep can answer that.
	Why string
}

func (s closureSite) key() string { return s.File + "\t" + s.Declaration }

// auditedClosures is the audit itself: every place this repository closes a work
// item, and what makes each one safe.
var auditedClosures = []closureSite{
	{
		File: "internal/chat/tracker.go", Declaration: "(*Session) carryOutTrackerAction", Calls: 2,
		ConsultsDischarge: false, Kind: kindHuman,
		Why: "the product manager closing or retiring an item in conversation. Nothing integrated, no developer claimed anything and no reviewer approved anything, so there is nothing to read; what authorizes it is the role, and the two calls are the close and the retirement.",
	},
	{
		File: "internal/chat/trackerrecovery.go", Declaration: "(recoveringTracker) Complete", Calls: 1,
		ConsultsDischarge: false, Kind: kindHuman,
		Why: "the conversation's tracker with the recovery rule around it, forwarding the one closure above to the configured tracker and asking again while it fails recoverably. It decides nothing: the only route that reaches it is carryOutTrackerAction, so what authorizes the closure is the role that asked for it there, and what this adds is the wait.",
	},
	{
		File: "internal/orchestrator/pipeline.go", Declaration: "(*activeRun) complete", Calls: 1,
		ConsultsDischarge: true, Kind: kindSettlement,
		Why: "a run settling its own item once its promotion is where it will stay. It closes only where the change discharges the item — the developer's claim and the reviewer's approval both saying so; anything else goes to settleUndischarged instead, and a merge the forge only queued defers the whole settlement to reconciliation.",
	},
	{
		File: "internal/orchestrator/reconcile.go", Declaration: "(Reconciler) closeSettledMerge", Calls: 1,
		ConsultsDischarge: true, Kind: kindSettlement,
		Why: "the sweep settling a run whose queued merge the forge has since performed. The forge merging the change settles where the work is and not whether it discharges the item, so the claim and the approval are read back from the durable record the run that made them left behind.",
	},
	{
		File: "internal/orchestrator/reconcile.go", Declaration: "(Reconciler) completeIntegrated", Calls: 1,
		ConsultsDischarge: true, Kind: kindSettlement,
		Why: "the sweep finishing a run somebody interrupted after its change was promoted. It decides the same way the run itself would have, from the same durable record, so an interrupted run and a finished one leave the item in the same place.",
	},
	{
		File: "internal/orchestrator/mergequeuepromotion.go", Declaration: "(MergeQueueRunCompletion) RecordWorkItem", Calls: 1,
		ConsultsDischarge: true, Kind: kindSettlement,
		Why: "the merge queue settling the item of a change it landed, once the landing is confirmed on the target. The candidate's own checks and review authorized the landing and say nothing about whether the change finishes the item, so the developer's claim and the reviewer's approval are read back from the run's durable record, as the sweep reads them for a merge the forge performed; anything that does not discharge goes to settleUndischarged instead.",
	},
	{
		File: "internal/orchestrator/conversationlanding.go", Declaration: "(ConversationLander) Settle", Calls: 1,
		ConsultsDischarge: false, Kind: kindRecordedLanding,
		Why: "the pass closing an item a conversation carries, which no run ever claimed and no reviewer ever judged, so there is no landing claim to read. What it reads instead is the landing the owner recorded: a revision of a document the item's role owns, by that role, whose reason opens with the item's identifier — the architect saying the item's work is in the document. It closes only on that, never on a revision that mentions the item further in, and the close reason cites the revision so a person can reopen it with a note — which holds, because the revision is recorded on the item ahead of the close and an open item still carrying it is not closed on it again.",
	},
}

// Every closure in this repository is listed above, everything listed above
// still closes something, and every settlement among them still reads the
// landing. The first stops a new route shipping unaudited; the second stops the
// list becoming a record of code that has moved on; the third is the one the
// false-closure class actually needs, because a settlement can lose its guard
// without moving.
func TestEveryRouteThatClosesAWorkItemIsAudited(t *testing.T) {
	t.Parallel()

	found, err := closureSitesIn(repositoryRoot)
	if err != nil {
		t.Fatalf("closureSitesIn() error = %v", err)
	}
	// A sweep that found nothing would report no unaudited route, which is the one
	// way this check can pass while reading nothing at all.
	if len(found) == 0 {
		t.Fatal("the sweep found no work item closures in this repository; it is looking in the wrong place")
	}

	audited := make(map[string]closureSite, len(auditedClosures))
	for _, site := range auditedClosures {
		if _, listed := audited[site.key()]; listed {
			t.Errorf("the audit lists %s: %s twice", site.File, site.Declaration)
		}
		if strings.TrimSpace(site.Why) == "" {
			t.Errorf("the audit lists %s: %s and does not say what makes it safe", site.File, site.Declaration)
		}
		if site.Kind == kindSettlement && !site.ConsultsDischarge {
			t.Errorf("the audit lists %s: %s as a run's settlement that does not ask whether the change discharges the item, which is the false closure itself",
				site.File, site.Declaration)
		}
		audited[site.key()] = site
	}

	for _, site := range found {
		listed, ok := audited[site.key()]
		if !ok {
			// Named one at a time, with what the reader has to decide, because a count
			// of unaudited routes is not something anybody can act on.
			t.Errorf("%s: %s closes a work item in %d place(s) and the audit does not list it. Add a row saying what makes it safe: "+
				"a run's settlement closes only where the change discharges the item, and a closure a person took has nothing to read. "+
				"A settlement that closes without asking is how yoyodyne-ifd.284 was closed against its own diagnosis",
				site.File, site.Declaration, site.Calls)
			continue
		}
		if listed.Calls != site.Calls {
			t.Errorf("%s: %s closes a work item in %d place(s) and the audit records %d; read the ones that changed and correct the row",
				site.File, site.Declaration, site.Calls, listed.Calls)
		}
		if listed.ConsultsDischarge != site.ConsultsDischarge {
			t.Errorf("%s: %s %s whether the change discharges the item and the audit says it %s; the guard moved and the row did not",
				site.File, site.Declaration, consults(site.ConsultsDischarge), consults(listed.ConsultsDischarge))
		}
		delete(audited, site.key())
	}

	stale := make([]string, 0, len(audited))
	for _, site := range audited {
		stale = append(stale, site.File+": "+site.Declaration)
	}
	sort.Strings(stale)
	for _, row := range stale {
		t.Errorf("the audit lists %s, which closes no work item any more; remove the row", row)
	}
}

func consults(reads bool) string {
	if reads {
		return "reads"
	}
	return "does not read"
}

// closureSitesIn is every declaration in the swept trees that closes a work
// item, in a stable order. Test sources are left out: what this is for is the
// routes the harness actually takes, and a test that closes an item is
// describing one rather than deciding anything.
func closureSitesIn(root string) ([]closureSite, error) {
	var sites []closureSite
	for _, tree := range closureTrees {
		treeRoot := filepath.Join(root, tree)
		if _, err := os.Stat(treeRoot); err != nil {
			// A tree a checkout does not carry is not a failure here: the caller
			// finding no closures at all is what says the sweep read the wrong place.
			continue
		}
		err := filepath.WalkDir(treeRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				// A fixture is a description of code rather than code the harness runs,
				// exactly as every other repository sweep here reads it.
				if entry.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			found, err := closureSitesInFile(path, filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			sites = append(sites, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(sites, func(first, second int) bool {
		if sites[first].File != sites[second].File {
			return sites[first].File < sites[second].File
		}
		return sites[first].Declaration < sites[second].Declaration
	})
	return sites, nil
}

// closureSitesInFile counts the closures in one file, by the declaration they
// sit in, and says of each declaration whether it also asks whether the change
// discharges the item. Both are read off the same declaration on purpose: what makes a
// settlement safe is that the closure and the consult are in one place, so a
// guard moved out of the declaration is a guard this reports as gone.
func closureSitesInFile(path, relative string) ([]closureSite, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}
	var sites []closureSite
	for _, declaration := range parsed.Decls {
		closures, consultsLanding := 0, false
		ast.Inspect(declaration, func(node ast.Node) bool {
			if closesWorkItem(node) {
				closures++
			}
			if readsDischargeDecision(node) {
				consultsLanding = true
			}
			return true
		})
		if closures == 0 {
			continue
		}
		sites = append(sites, closureSite{
			File:              relative,
			Declaration:       declarationName(declaration),
			Calls:             closures,
			ConsultsDischarge: consultsLanding,
		})
	}
	return sites, nil
}

// closesWorkItem reports a call the tracker closes a work item through. It is
// every `Complete` call that is passed something, whatever it is called on: a
// sweep keyed to a receiver named `Tracker` would miss the closure written
// through a local, a parameter, or a field somebody named otherwise, which is
// exactly the route this audit exists to have nowhere to hide.
//
// The argument is what keeps the shape usable. `Complete` is also what a cost
// record and a worktree cleanup answer about themselves, and both of those are
// predicates taking nothing, so requiring an argument separates the question
// from the act without naming either. A `Complete(x)` that turns out to close
// nothing is audited as kindUnrelated rather than being excluded here, because a
// row somebody wrote is a decision and a silent exclusion is not.
func closesWorkItem(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	switch named := call.Fun.(type) {
	case *ast.SelectorExpr:
		return named.Sel.Name == "Complete"
	case *ast.Ident:
		return named.Name == "Complete"
	}
	return false
}

// readsDischargeDecision reports a declaration asking whether the change it is
// closing an item against discharges that item. Both derivations count:
// Discharges is the question itself, and itemSettled is how a sweep asks it of
// an item it has already read.
//
// It is deliberately the whole question rather than either half of it.
// LandingDischarges answers for the developer's claim alone and
// ApprovalDischarges for the reviewer's approval alone, and a settlement that
// consulted one of them would close an item the other said to leave open — which
// is the same false closure one step along from the one that closed
// yoyodyne-ifd.284.
func readsDischargeDecision(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch named := call.Fun.(type) {
	case *ast.SelectorExpr:
		return named.Sel.Name == "Discharges"
	case *ast.Ident:
		return named.Name == "itemSettled"
	}
	return false
}

// declarationName says where a closure sits, in the words somebody would use to
// find it: the method with its receiver, the function, or the file scope a
// package-level value is initialized in.
func declarationName(declaration ast.Decl) string {
	function, ok := declaration.(*ast.FuncDecl)
	if !ok {
		return "file scope"
	}
	if function.Recv != nil && len(function.Recv.List) == 1 {
		return "(" + receiverName(function.Recv.List[0].Type) + ") " + function.Name.Name
	}
	return function.Name.Name
}

func receiverName(receiver ast.Expr) string {
	switch typed := receiver.(type) {
	case *ast.StarExpr:
		return "*" + receiverName(typed.X)
	case *ast.IndexExpr:
		return receiverName(typed.X)
	case *ast.Ident:
		return typed.Name
	}
	return "?"
}
