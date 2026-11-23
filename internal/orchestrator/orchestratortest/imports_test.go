package orchestratortest

import (
	"go/build"
	"path/filepath"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/mason-bryant/yoyodyne"
	// orchestratorPath is the package this one must never reach. An in-package
	// orchestrator test cannot import a package that imports orchestrator, so one
	// such import, direct or through anything else here, would close this package
	// to the tests it exists for.
	orchestratorPath = modulePath + "/internal/orchestrator"
)

func TestThePackageImportsNothingFromTheOrchestrator(t *testing.T) {
	t.Parallel()

	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("read this package: %v", err)
	}
	// Imports is what the package itself compiles against, its own tests left
	// out: the conformance test beside this one imports orchestrator on purpose,
	// from a test package of its own. Every package of this module it reaches is
	// walked, so an import through another package is found as well.
	via := map[string]string{}
	queue := []string{}
	for _, path := range pkg.Imports {
		if strings.HasPrefix(path, modulePath+"/") {
			via[path] = "orchestratortest"
			queue = append(queue, path)
		}
	}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if path == orchestratorPath || strings.HasPrefix(path, orchestratorPath+"/") {
			t.Errorf("%s imports %s", via[path], path)
			continue
		}
		dependency, err := importPackage(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, next := range dependency.Imports {
			if _, seen := via[next]; seen || !strings.HasPrefix(next, modulePath+"/") {
				continue
			}
			via[next] = path
			queue = append(queue, next)
		}
	}
	if len(via) == 0 {
		t.Fatal("found no import of this module's packages, so the walk proved nothing")
	}
}

// importPackage reads a package of this module by its folder, found from the
// test's working folder. go/build cannot find one by its import path in a test
// built with -trimpath, which does not know where Go is.
func importPackage(path string) (*build.Package, error) {
	return build.ImportDir(filepath.Join("..", "..", "..", filepath.FromSlash(strings.TrimPrefix(path, modulePath+"/"))), 0)
}
