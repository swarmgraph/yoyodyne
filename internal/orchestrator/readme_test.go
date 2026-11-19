package orchestrator

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReadmeNamesOnlyFilesThatExist keeps the code map from going stale. The
// README does not list every file in the package, because a hand-kept list
// made every pair of changes that each added a file conflict there; a file
// describes itself in its own leading comment instead. What the README does
// name must still exist, so a rename or deletion fails here until the README
// follows it.
func TestReadmeNamesOnlyFilesThatExist(t *testing.T) {
	text, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	byBase := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			byBase[d.Name()] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	named := regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:go|md))`")
	for _, m := range named.FindAllStringSubmatch(string(text), -1) {
		name := m[1]
		if strings.Contains(name, "/") {
			if _, err := os.Stat(filepath.Join(root, name)); err != nil {
				t.Errorf("README.md names %s, which does not exist; update the code map", name)
			}
			continue
		}
		if !byBase[name] {
			t.Errorf("README.md names %s, which is in no package under internal; update the code map", name)
		}
	}
}
