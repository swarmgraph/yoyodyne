package watchdog

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/mason-bryant/yoyodyne"

// livenessDerivations are the packages that notice the harness, or a watcher
// in it, has stopped: the stall derivation here, and the program manager's
// stale derivation in the read model. Both are the out-of-band signal that
// something which should be running is not, so neither may be anything that
// pauses with the provider's usage window — `docs/designs/management-and-supervision.md`
// rules that detection of nothing-running is non-model machinery, and
// `docs/designs/program-manager.md` holds staleness to the same.
var livenessDerivations = []string{
	modulePath + "/internal/watchdog",
	modulePath + "/internal/readmodel",
}

// TestTheStallAndStaleDerivationsReachNoProviderCall fails when anything the
// stall or the stale derivation compiles against, directly or through any
// package of this module, builds a provider invocation. A provider invocation
// cannot avoid constructing a backend.RunRequest, which is the mark the sweep
// of every invocation site in internal/backend looks for too; a package
// carrying one anywhere in this closure is a path from the watchdog to a
// model, however indirect.
func TestTheStallAndStaleDerivationsReachNoProviderCall(t *testing.T) {
	t.Parallel()

	via := map[string]string{}
	var queue []string
	for _, root := range livenessDerivations {
		via[root] = root
		queue = append(queue, root)
	}
	var invoking []string
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		pkg, err := importPackage(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, file := range pkg.GoFiles {
			if buildsRunRequest(t, filepath.Join(pkg.Dir, file)) {
				invoking = append(invoking, strings.TrimPrefix(path, modulePath+"/")+"/"+file+" (reached through "+strings.TrimPrefix(via[path], modulePath+"/")+")")
			}
		}
		for _, next := range pkg.Imports {
			if _, seen := via[next]; seen || !strings.HasPrefix(next, modulePath+"/") {
				continue
			}
			via[next] = path
			queue = append(queue, next)
		}
	}
	if len(via) <= len(livenessDerivations) {
		t.Fatal("found no import of this module's packages, so the walk proved nothing")
	}
	sort.Strings(invoking)
	if len(invoking) > 0 {
		t.Fatalf("the stall or the stale derivation reaches a provider invocation, so a usage window would put it to sleep: %v", invoking)
	}
}

// TestTheLivenessSweepRecognisesAProviderInvocation is the sweep proving it is
// looking for the right thing: the conversation's own invocation site is one.
func TestTheLivenessSweepRecognisesAProviderInvocation(t *testing.T) {
	t.Parallel()

	pkg, err := importPackage(modulePath + "/internal/chat")
	if err != nil {
		t.Fatalf("read internal/chat: %v", err)
	}
	if !buildsRunRequest(t, filepath.Join(pkg.Dir, "chat.go")) {
		t.Fatal("internal/chat/chat.go asks a provider for a turn and the sweep does not see it, so a clean sweep proves nothing")
	}
}

// importPackage reads a package of this module by its folder, found from the
// test's working folder. go/build cannot find one by its import path in a test
// built with -trimpath, which does not know where Go is.
func importPackage(path string) (*build.Package, error) {
	return build.ImportDir(filepath.Join("..", "..", filepath.FromSlash(strings.TrimPrefix(path, modulePath+"/"))), 0)
}

// buildsRunRequest reports whether a file constructs a backend.RunRequest, or
// a RunRequest inside package backend itself.
func buildsRunRequest(t *testing.T, file string) bool {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	built := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return !built
		}
		switch named := literal.Type.(type) {
		case *ast.SelectorExpr:
			if qualifier, ok := named.X.(*ast.Ident); ok && named.Sel.Name == "RunRequest" && (qualifier.Name == "backend" || qualifier.Name == "backendapi") {
				built = true
			}
		case *ast.Ident:
			if named.Name == "RunRequest" && parsed.Name.Name == "backend" {
				built = true
			}
		}
		return !built
	})
	return built
}
