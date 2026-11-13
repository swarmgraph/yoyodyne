package teamrecords_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Records are written only by the harness's own Git code in the machine's state
// directory, reached from the forge check and the Slack connection. Nothing an
// agent is given reaches it, which holds while the reference is named only in
// internal/gitworktree, the records repository is opened only by
// internal/cli/teamrecords.go, and a Sharer is built only in internal/cli.
func TestNothingAnAgentIsGivenWritesARecord(t *testing.T) {
	root := moduleRoot(t)
	var seen int
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		text := string(content)
		seen++
		if strings.Contains(text, "refs/yoyodyne/records") && !strings.HasPrefix(relative, "internal/gitworktree/") {
			t.Errorf("%s names refs/yoyodyne/records; only internal/gitworktree does", relative)
		}
		if strings.Contains(text, ".RecordsRepository(") && relative != "internal/cli/teamrecords.go" {
			t.Errorf("%s opens the records repository; only internal/cli/teamrecords.go does", relative)
		}
		if strings.Contains(text, "teamrecords.Sharer{") && !strings.HasPrefix(relative, "internal/cli/") {
			t.Errorf("%s builds a teamrecords.Sharer; only internal/cli does", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no Go files were read, so nothing was checked")
	}
}

// moduleRoot is the directory holding go.mod, found by walking up from the
// package under test.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
