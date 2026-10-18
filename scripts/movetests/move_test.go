package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// TestCommentsNameTheHelpersByTheirExportedNames exports a helper type, its
// fields, and a short helper function the way a move does, and checks that the
// comments naming them name them as the harness does: a name that mixes cases
// wherever a comment uses it, a lower-case one only where a comment quotes it
// as code, and any of them as the first word of its own comment.
func TestCommentsNameTheHelpersByTheirExportedNames(t *testing.T) {
	const src = `package p

// pausingClock is moved by hand; see waitedSpan. A pausingClock never sleeps.
type pausingClock struct {
	// now is when it is, and now is an ordinary word elsewhere.
	now int
	slept []int // slept records each wait; onSleep observes it.
	// onSleep observes durable state.
	onSleep func()
}

// waitedSpan is the whole span slept, now that onSleep has run.
func waitedSpan(c *pausingClock) int { return 0 }

// run calls run and waitedSpan.
func run() {}

// publishing wraps a pipeline, so a publishing run pushes; see ` + "`slept`" + `.
func publishing() {}

// unrenamed mentions run, now, publishing, and slept in their ordinary sense,
// and ` + "`publishing`" + ` and ` + "`c.slept`" + ` as code; a quotation that is
// more than the name is left alone.
func unrenamed() {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{}).Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	clock := pkg.Scope().Lookup("pausingClock")
	p := &plan{
		exported: map[types.Object]bool{clock: true, pkg.Scope().Lookup("waitedSpan"): true, pkg.Scope().Lookup("run"): true, pkg.Scope().Lookup("publishing"): true},
		owner:    map[types.Object]string{},
		inMember: map[types.Object]bool{},
		renames:  map[string]string{"pausingClock.now": "Instant"},
	}
	fields := clock.Type().Underlying().(*types.Struct)
	for i := 0; i < fields.NumFields(); i++ {
		p.owner[fields.Field(i)] = "pausingClock"
		p.inMember[fields.Field(i)] = true
	}

	renames := p.commentRenames([]*ast.File{file}, info)
	base := fset.File(file.Pos()).Base()
	got := string(apply([]byte(src), 0, len(src), commentEdits(file, base, renames)))

	const want = `package p

// PausingClock is moved by hand; see WaitedSpan. A PausingClock never sleeps.
type pausingClock struct {
	// Instant is when it is, and now is an ordinary word elsewhere.
	now int
	slept []int // Slept records each wait; OnSleep observes it.
	// OnSleep observes durable state.
	onSleep func()
}

// WaitedSpan is the whole span slept, now that OnSleep has run.
func waitedSpan(c *pausingClock) int { return 0 }

// Run calls run and WaitedSpan.
func run() {}

// Publishing wraps a pipeline, so a publishing run pushes; see ` + "`Slept`" + `.
func publishing() {}

// unrenamed mentions run, now, publishing, and slept in their ordinary sense,
// and ` + "`Publishing`" + ` and ` + "`c.slept`" + ` as code; a quotation that is
// more than the name is left alone.
func unrenamed() {}
`
	if got != want {
		t.Errorf("the comments read\n%s\nwant\n%s", got, want)
	}
}

// TestAWordTwoRenamesDisagreeOnIsLeftAlone checks that a member name two types
// export under different names is not renamed in comments, where the tool
// cannot tell which of them a comment means, except as the first word of each
// member's own comment.
func TestAWordTwoRenamesDisagreeOnIsLeftAlone(t *testing.T) {
	const src = `package p

type first struct {
	// onSleep is the first one.
	onSleep func()
}

type second struct {
	// onSleep is the second one.
	onSleep func()
}

// both mention onSleep.
func both() {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{}).Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	p := &plan{
		exported: map[types.Object]bool{},
		owner:    map[types.Object]string{},
		inMember: map[types.Object]bool{},
		renames:  map[string]string{"second.onSleep": "Observe"},
	}
	for _, name := range []string{"first", "second"} {
		field := pkg.Scope().Lookup(name).Type().Underlying().(*types.Struct).Field(0)
		p.owner[field] = name
		p.inMember[field] = true
	}

	renames := p.commentRenames([]*ast.File{file}, info)
	base := fset.File(file.Pos()).Base()
	got := string(apply([]byte(src), 0, len(src), commentEdits(file, base, renames)))

	const want = `package p

type first struct {
	// OnSleep is the first one.
	onSleep func()
}

type second struct {
	// Observe is the second one.
	onSleep func()
}

// both mention onSleep.
func both() {}
`
	if got != want {
		t.Errorf("the comments read\n%s\nwant\n%s", got, want)
	}
}
