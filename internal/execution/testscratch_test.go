package execution

import (
	"os"
	"path/filepath"
	"testing"
)

// Each run of an environment's check or test is given a folder of its own
// under the run's scratch directory, empty when it starts whatever an earlier
// run of the same name left, and removed afterwards; a link left inside is
// removed as a link and what it points at is kept.
func TestEachEnvironmentCommandIsGivenAnEmptyFolderThatIsRemovedAfterwards(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	folder, err := PrepareTestScratch(state, "run-one", "test-1")
	if err != nil {
		t.Fatalf("PrepareTestScratch() error = %v", err)
	}
	if want := filepath.Join(resolve(t, state), "scratch", "run-one", "environment-runs", "test-1"); folder != want {
		t.Fatalf("PrepareTestScratch() = %q, want %q", folder, want)
	}
	outside := filepath.Join(t.TempDir(), "kept")
	writeFile(t, outside, "kept\n")
	makeDirectory(t, filepath.Join(folder, "profile"))
	writeFile(t, filepath.Join(folder, "profile", "state"), "left by the last run\n")
	if err := os.Symlink(outside, filepath.Join(folder, "link")); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}
	other, err := PrepareTestScratch(state, "run-one", "check-1")
	if err != nil {
		t.Fatal(err)
	}

	again, err := PrepareTestScratch(state, "run-one", "test-1")
	if err != nil || again != folder {
		t.Fatalf("PrepareTestScratch() again = %q, %v; want %q", again, err, folder)
	}
	if entries, err := os.ReadDir(again); err != nil || len(entries) != 0 {
		t.Fatalf("the folder holds %v (%v), want it empty", entries, err)
	}
	if err := RemoveTestScratch(state, "run-one", "test-1"); err != nil {
		t.Fatalf("RemoveTestScratch() error = %v", err)
	}
	if _, err := os.Lstat(folder); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there (%v)", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("what a link inside pointed at was removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another command's folder was removed: %v", err)
	}
	if err := RemoveTestScratch(state, "run-one", "test-1"); err != nil {
		t.Fatalf("removing a folder already gone = %v, want nothing", err)
	}
	if err := RemoveTestScratch(t.TempDir(), "run-one", "test-1"); err != nil {
		t.Fatalf("removing from a state directory with no scratch = %v, want nothing", err)
	}
	for _, name := range []string{"", ".", "..", "a/b", "../escape"} {
		if _, err := PrepareTestScratch(state, "run-one", name); err == nil {
			t.Errorf("PrepareTestScratch(%q) was accepted, want it refused", name)
		}
	}
}
