// Command movetests moves internal/orchestrator's tests that reach none of the
// package's unexported identifiers out into packages of their own, so that
// go test runs them as separate binaries rather than as one binary nearing the
// Makefile's TEST_TIMEOUT
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// Run from the repository root. With no -target it prints the census: each
// test file, how many tests it holds, how many of them reach internals, and
// its size; -v names what each of those tests reaches.
//
//	go run ./scripts/movetests
//	go run ./scripts/movetests -v
//
// With -target it moves the named files' tests into a new package under
// internal/orchestrator, and the test helpers they need into the shared helper
// package internal/orchestrator/testharness:
//
//	go run ./scripts/movetests -target publicationtests \
//		-files publish_test.go,publication_test.go,publication_settle_test.go
//
// A test in a named file that reaches internals stays where it is, and the tool
// says so. -tests moves single tests out of files that otherwise stay, and
// -keep leaves single tests of a named file where they are.
//
// The change a move makes has to fit the review bound
// (gitworktree.DefaultMaxDiffBytes for the whole patch, and
// gitworktree.DefaultMaxDiffFileBytes for each new file), so a group is sized
// to it. The tool warns about a file it writes past the per-file figure, which
// is split by hand. What it takes out of the package's own files costs the
// patch little only while Git renders it as removals alone: where Git pairs a
// test that stays with one that moved, -keep the one that moved.
//
// A helper declared in a moved file and used by nothing left behind moves with
// its file. Every other helper the moved tests need goes into testharness,
// exported, and stays in the package's own test files too for as long as a test
// there still uses it: an in-package test cannot import testharness, because
// testharness imports orchestrator. Exporting a helper capitalizes its name and
// those of its type's fields and methods; where that would collide with a name
// the type already has, the tool reports the collision and -rename gives the
// member another name, as Type.member=NewName.
//
// What the tool writes is a starting point that compiles, not a finished
// change: read the diff, and run the moved package's tests and the package's
// own before handing it over.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	dir := flag.String("dir", "internal/orchestrator", "the package whose tests move")
	target := flag.String("target", "", "the new package's directory under -dir; empty prints the census")
	harness := flag.String("harness", "testharness", "the shared helper package's directory under -dir")
	fileArg := flag.String("files", "", "comma-separated test files whose tests move")
	testArg := flag.String("tests", "", "comma-separated single tests that move")
	keepArg := flag.String("keep", "", "comma-separated tests that stay although their file moves")
	renameArg := flag.String("rename", "", "comma-separated Type.member=NewName for exported names that would collide")
	dryRun := flag.Bool("n", false, "say what would be written without writing it")
	verbose := flag.Bool("v", false, "with the census, name what each test reaching internals reaches")
	flag.Parse()

	model, err := load(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *target == "" {
		model.census(os.Stdout, *verbose)
		return
	}
	renames := map[string]string{}
	for _, pair := range split(*renameArg) {
		from, to, ok := strings.Cut(pair, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "-rename %q is not Type.member=NewName\n", pair)
			os.Exit(2)
		}
		renames[from] = to
	}
	request := moveRequest{
		dir:     *dir,
		target:  *target,
		harness: *harness,
		files:   split(*fileArg),
		tests:   split(*testArg),
		keep:    split(*keepArg),
		renames: renames,
		dryRun:  *dryRun,
	}
	if err := model.move(request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func split(list string) []string {
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}
