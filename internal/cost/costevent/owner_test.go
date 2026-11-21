package costevent

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writers are the files outside internal/cost that may name the field a cost
// event carries its figure in, and why. They write the event rather than read
// one back: the Claude Code adapter reads the cost off the provider's own
// stream and records it on the terminal. Codex reports no cost, so its adapter
// names none. The list may only shrink.
var writers = map[string]string{
	"internal/backend/claudecode/parser.go": "reads the provider's stream and writes the terminal event that carries the cost",
}

// No package outside internal/cost decodes a cost event: every reader of what
// an event log spent reads through Reader, so two surfaces cannot disagree
// about what the same log cost or whether its total is complete. A non-test
// file that names the cost field is reading or writing one, and a writer has to
// be named above.
func TestOnlyThisPackageDecodesACostEvent(t *testing.T) {
	root := repositoryRoot(t)
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata", "node_modules", "bin":
				return filepath.SkipDir
			}
			if relative == "internal/cost" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), `total_cost_usd`) {
			found = append(found, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	sort.Strings(found)
	for _, path := range found {
		if _, named := writers[path]; !named {
			t.Errorf("%s names the cost field of an event; read cost events through internal/cost/costevent instead", path)
		}
	}
	for path := range writers {
		if !contains(found, path) {
			t.Errorf("%s is listed as writing cost events but no longer names the field; take it off the list", path)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", root, err)
	}
	return root
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
