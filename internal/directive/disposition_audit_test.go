package directive

// The audit of every place this repository reads what became of a directive,
// held to the code it describes.
//
// Resolved and InForce answer different questions, and the one that reads like
// liveness is the one that is not. A standing instruction carries a disposition
// from the moment somebody records what came of it, and goes on constraining
// work until the operator withdraws it, so a filter that asks Resolved to find
// out what still applies retires that instruction the moment its first item is
// admitted — the record still says it, the listing stops showing it, and nobody
// is told. That is the failure this whole area exists to prevent, and it is
// silent in every direction.
//
// So every read of a disposition is listed below with what it means where it
// sits, and this fails on one that is not listed. The list is the audit; this
// check is what stops it going stale, because the reader that will get this
// wrong is the one written a year from now by somebody who never read the
// distinction. ResolvedAt is swept beside Resolved deliberately: reading the
// field is the one way to ask the same question without going through the
// predicate, and an audit that watched only the predicate would watch the door
// beside the open window.

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
// exactly what cannot carry the reader that moved.
const repositoryRoot = "../.."

// dispositionTrees are the source trees swept. Everything the harness runs is in
// one of them, and a tree added later is one this has to be told about, which is
// a change somebody makes on purpose.
var dispositionTrees = []string{"internal", "cmd"}

// dispositionSite is one declaration that reads a disposition, and what reading
// it means there.
type dispositionSite struct {
	File        string
	Declaration string
	// Read is what was read: Resolved() for the predicate, ResolvedAt for the
	// field behind it.
	Read string
	// Reads is how many times the declaration reads it, so a second read added to
	// a declaration already listed is audited rather than covered by the first.
	Reads int
	// Means is why this reading is the right one here. It is prose because the
	// question it answers is one only a reader can settle: whether the site wanted
	// has-a-disposition, which is what these two say, or still-applies, which is
	// InForce.
	Means string
}

func (s dispositionSite) key() string {
	return s.File + "\t" + s.Declaration + "\t" + s.Read
}

// auditedDispositionReads is the audit itself: every disposition read in this
// repository's non-test sources, and what it means where it is.
//
// Two types answer to the name. A directive's Resolved says somebody has
// accounted for it and says nothing about whether it still applies; a goal
// attribution's says the statement was matched to a recorded goal, which has no
// liveness reading at all. Both are listed, because the sweep cannot tell them
// apart and a reader adding either should say which they meant.
var auditedDispositionReads = []dispositionSite{
	{
		File: "internal/goal/relevant.go", Declaration: "(Set) ResolveRelevant", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: whether a relevant goal matched a recorded goal, so its canonical reference can be stored.",
	},
	{
		File: "internal/goal/relevant.go", Declaration: "(Set) DescribeRelevant", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: whether a recorded reference matched a goal, so the reader sees its current wording.",
	},

	{
		File: "internal/chat/admission.go", Declaration: "(*Session) admissionGap", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: whether the statement was matched to a recorded goal. It has no liveness reading at all.",
	},
	{
		File: "internal/chat/directive.go", Declaration: "(DirectiveWithdrawn) Render", Read: "Resolved()", Reads: 1,
		Means: "whether a withdrawn pausing directive was ever answered, which decides one sentence about what the withdrawal released. What the listing beside it groups on is InForce.",
	},
	{
		File: "internal/chat/tracker.go", Declaration: "(*Session) admissionRefusal", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: whether the goal the creation names is one the repository records.",
	},
	{
		File: "internal/cli/directive.go", Declaration: "withdrawDirective", Read: "Resolved()", Reads: 1,
		Means: "the same sentence at the command line: a pause withdrawn without ever being answered says so. The listing above it asks InForce.",
	},
	{
		File: "internal/cli/goals.go", Declaration: "attributionDetail", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: the goal a work item resolved to, or what is wrong with the attribution.",
	},
	{
		File: "internal/cli/goals.go", Declaration: "recordGoalIdentities", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: an item whose recorded goal matched nothing is reported and left alone rather than moved onto an identity, and one that already matched is skipped.",
	},
	{
		File: "internal/conformance/conformance.go", Declaration: "(*Assessment) checkGoals", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: how many admitted items serve a recorded goal, counted for the release-readiness report. What refuses a tag is Divergent, which is the other end of the same judgement and not liveness either.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) CarryOut", Read: "Resolved()", Reads: 1,
		Means: "refuses a second settlement; the outcome already recorded is what somebody would lose.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) InForce", Read: "Resolved()", Reads: 1,
		Means: "the one place the two questions meet, and only for the kinds that pause: settling a hold is what lifts it. A directive that pauses nothing never reaches this line, which is exactly why liveness cannot be read off the disposition anywhere else.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) Render", Read: "Resolved()", Reads: 2,
		Means: "what the record says about itself: what it was waiting on, in the past tense once answered, and the settlement under it. A carried-out standing instruction renders this way and stays among what is in force.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) Render", Read: "ResolvedAt", Reads: 1,
		Means: "stamps the settlement it prints.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) Resolve", Read: "Resolved()", Reads: 1,
		Means: "refuses a second settlement; the first one's account of why the work resumed must not be overwritten.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) Resolved", Read: "ResolvedAt", Reads: 1,
		Means: "the predicate itself: a directive has a disposition when the field is set.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) Validate", Read: "ResolvedAt", Reads: 3,
		Means: "what a record carrying a disposition has to carry with it: a resolution, and a time that is not the zero time.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) Withdraw", Read: "ResolvedAt", Reads: 1,
		Means: "names when a directive that is already out of force was settled, in the refusal. Whether it is out of force was asked of InForce a line above.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) ResolveInto", Read: "ResolvedAt", Reads: 2,
		Means: "names when a directive already out of force was settled, in the refusal, having asked InForce a line above; and writes the resolution a pausing directive takes when it is resolved into what carries it, so the pause reads as answered everywhere.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) alreadySettled", Read: "ResolvedAt", Reads: 1,
		Means: "names when it was settled, in the refusal of settling it again.",
	},
	{
		File: "internal/directive/directive.go", Declaration: "(Directive) settle", Read: "ResolvedAt", Reads: 1,
		Means: "writes the disposition; it is the only thing that does.",
	},
	{
		File: "internal/goal/goal.go", Declaration: "(Attribution) ApprovalGap", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: an unresolved attribution has already said what is wrong with it.",
	},
	{
		File: "internal/goal/goal.go", Declaration: "(Attribution) ResolvedByWording", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: an attribution that matched a recorded goal, and matched it on the words rather than on the goal's identity.",
	},
	{
		File: "internal/goal/goal.go", Declaration: "(Set) NoteFor", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: only a name that matched a recorded goal is written down as that goal's identity, and anything else is written down as it was given.",
	},
	{
		File: "internal/slack/feed.go", Declaration: "(*HarnessFeed) directiveDeliveries", Read: "Resolved()", Reads: 1,
		Means: "which steered directives have an outcome to announce in the thread they were said in. What is announced is the disposition, so this is the question it wants; a standing instruction is announced as carried out and goes on standing.",
	},
	{
		File: "internal/slack/feed.go", Declaration: "(*HarnessFeed) directiveDeliveries", Read: "ResolvedAt", Reads: 2,
		Means: "when it was settled: read past where that is older than the reporting watermark, and stamped on the acknowledgment.",
	},
	{
		File: "internal/slack/inbound.go", Declaration: "unsettled", Read: "Resolved()", Reads: 1,
		Means: "what a directive is still waiting on, which is nothing once somebody has settled it. A standing instruction was waiting on nothing to begin with.",
	},
	{
		File: "internal/staleness/staleness.go", Declaration: "Survey", Read: "Resolved()", Reads: 1,
		Means: "goal attribution, not a directive: an item with no goal to follow upstream has no staleness to report.",
	},
}

// Every disposition read in this repository is listed above, and everything
// listed above is still a disposition read. The first half is what makes a new
// filter get audited before it ships; the second is what stops the list becoming
// a record of code that has moved on.
func TestEveryReadOfADirectiveDispositionIsAudited(t *testing.T) {
	t.Parallel()

	found, err := dispositionReadsIn(repositoryRoot)
	if err != nil {
		t.Fatalf("dispositionReadsIn() error = %v", err)
	}
	// A sweep that found nothing would report no unaudited site, which is the one
	// way this check can pass while reading nothing at all.
	if len(found) == 0 {
		t.Fatal("the sweep found no disposition reads in this repository; it is looking in the wrong place")
	}

	audited := make(map[string]dispositionSite, len(auditedDispositionReads))
	for _, site := range auditedDispositionReads {
		if _, listed := audited[site.key()]; listed {
			t.Errorf("the audit lists %s: %s reading %s twice", site.File, site.Declaration, site.Read)
		}
		if strings.TrimSpace(site.Means) == "" {
			t.Errorf("the audit lists %s: %s reading %s and does not say what it means there", site.File, site.Declaration, site.Read)
		}
		audited[site.key()] = site
	}

	for _, site := range found {
		listed, ok := audited[site.key()]
		if !ok {
			// Named one at a time, with what the reader has to decide, because a count
			// of unaudited sites is not something anybody can act on.
			t.Errorf("%s: %s reads %s in %d place(s) and the audit does not list it. Add a row saying what it means there: "+
				"Resolved is has-a-disposition and InForce is still-applies, and a filter that wanted liveness "+
				"and asked Resolved silently retires a standing instruction the operator never withdrew",
				site.File, site.Declaration, site.Read, site.Reads)
			continue
		}
		if listed.Reads != site.Reads {
			t.Errorf("%s: %s reads %s in %d place(s) and the audit records %d; read the ones that changed and correct the row",
				site.File, site.Declaration, site.Read, site.Reads, listed.Reads)
		}
		delete(audited, site.key())
	}

	stale := make([]string, 0, len(audited))
	for _, site := range audited {
		stale = append(stale, fmt.Sprintf("%s: %s reading %s", site.File, site.Declaration, site.Read))
	}
	sort.Strings(stale)
	for _, row := range stale {
		t.Errorf("the audit lists %s, which reads no disposition any more; remove the row", row)
	}
}

// dispositionReadsIn is every declaration in the swept trees that reads a
// disposition, in a stable order. Test sources are left out: what this is for is
// the filters the harness actually runs on, and a test that reads a disposition
// is describing one rather than deciding anything.
func dispositionReadsIn(root string) ([]dispositionSite, error) {
	var sites []dispositionSite
	for _, tree := range dispositionTrees {
		treeRoot := filepath.Join(root, tree)
		if _, err := os.Stat(treeRoot); err != nil {
			// A tree a checkout does not carry is not a failure here: the caller
			// finding no reads at all is what says the sweep read the wrong place.
			continue
		}
		err := filepath.WalkDir(treeRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				// A fixture is a description of code rather than code the harness runs,
				// and it is free to be malformed on purpose, exactly as every other
				// repository check here reads it.
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
			found, err := dispositionReadsInFile(path, filepath.ToSlash(relative))
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
		if sites[first].Declaration != sites[second].Declaration {
			return sites[first].Declaration < sites[second].Declaration
		}
		return sites[first].Read < sites[second].Read
	})
	return sites, nil
}

// dispositionReadsInFile counts the disposition reads in one file, by the
// declaration they sit in. Where the field is assigned rather than read it is
// counted too, because the sweep is looking at a name rather than at a
// direction, and the row that lists it says which it is.
func dispositionReadsInFile(path, relative string) ([]dispositionSite, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}
	counted := map[string]int{}
	var order []dispositionSite
	for _, declaration := range parsed.Decls {
		name := declarationName(declaration)
		ast.Inspect(declaration, func(node ast.Node) bool {
			read := dispositionRead(node)
			if read == "" {
				return true
			}
			site := dispositionSite{File: relative, Declaration: name, Read: read}
			if counted[site.key()] == 0 {
				order = append(order, site)
			}
			counted[site.key()]++
			return true
		})
	}
	for index := range order {
		order[index].Reads = counted[order[index].key()]
	}
	return order, nil
}

// dispositionRead says what this node reads, and nothing where it reads neither.
//
// The predicate is recognized as a call rather than as a name, because
// `Resolved` is also a plain field on the resolved configuration and on a
// write refusal, and neither of those says anything about a directive. The
// field is recognized wherever it appears: every mention of it is somebody
// asking the question the predicate answers.
func dispositionRead(node ast.Node) string {
	switch typed := node.(type) {
	case *ast.CallExpr:
		selector, ok := typed.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Resolved" && len(typed.Args) == 0 {
			return "Resolved()"
		}
	case *ast.SelectorExpr:
		if typed.Sel.Name == "ResolvedAt" {
			return "ResolvedAt"
		}
	}
	return ""
}

// declarationName says where a read sits, in the words somebody would use to
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
