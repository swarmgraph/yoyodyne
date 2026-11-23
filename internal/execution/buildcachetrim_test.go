package execution

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

var trimNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// trimFixture is a repository whose cache folder is the one a run's own
// environment names.
func trimFixture(t *testing.T) (repository, cache string) {
	t.Helper()
	repository = t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range WithGoBuildCache([]string{}, repository) {
		if named, found := strings.CutPrefix(entry, goBuildCacheVariable+"="); found {
			cache = named
		}
	}
	if cache == "" {
		t.Fatal("a run's environment names no build cache")
	}
	return repository, cache
}

func cacheFile(t *testing.T, cache, name string, size int, age time.Duration) string {
	t.Helper()
	path := filepath.Join(cache, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := trimNow.Add(-age)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	return path
}

func here(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func trimmed(t *testing.T, repository string, limit int64) BuildCacheTrim {
	t.Helper()
	trim, err := TrimBuildCache(context.Background(), repository, limit, trimNow)
	if err != nil {
		t.Fatalf("TrimBuildCache() error = %v", err)
	}
	return trim
}

// The folder trimmed is the one the run's environment names, and the oldest
// files go first, across folders, until it is at 80% of the limit and no
// further.
func TestABuildCacheOverItsLimitIsTrimmedOldestFirstToEightyPercent(t *testing.T) {
	t.Parallel()
	repository, cache := trimFixture(t)
	var files []string
	for i := 0; i < 12; i++ {
		// Names run against ages, so an order by name would fail.
		files = append(files, cacheFile(t, cache, fmt.Sprintf("%02d/entry", 50-i), 100, time.Duration(30-i)*time.Hour))
	}
	elsewhere := cacheFile(t, repository, ".git/objects/ab/old", 100000, 1000*time.Hour)

	trim := trimmed(t, repository, 1000)
	if !trim.Trimmed || trim.Before != 1200 || trim.After != 800 || trim.Removed != 4 {
		t.Fatalf("trim = %+v, want 1200 down to 800 by 4 files", trim)
	}
	for i, file := range files {
		if want := i >= 4; here(t, file) != want {
			t.Errorf("file %d (age %dh) kept = %t, want %t", i, 30-i, !want, want)
		}
	}
	if !here(t, filepath.Dir(files[0])) {
		t.Error("an emptied folder was removed")
	}
	if !here(t, elsewhere) {
		t.Error("a file outside the cache was removed")
	}
	if again := trimmed(t, repository, 1000); again.Trimmed || again.Removed != 0 {
		t.Errorf("a folder within its limit was trimmed: %+v", again)
	}
}

func TestABuildCacheAtItsLimitIsNotTrimmed(t *testing.T) {
	t.Parallel()
	repository, cache := trimFixture(t)
	file := cacheFile(t, cache, "00/entry", 1000, 50*time.Hour)
	if trim := trimmed(t, repository, 1000); trim.Trimmed || !here(t, file) {
		t.Fatalf("trim = %+v, want nothing removed", trim)
	}
}

// However far over its limit the folder is, a file changed in the last two
// hours stays, and so does one stamped in the future.
func TestATrimNeverRemovesAFileChangedInTheLastTwoHours(t *testing.T) {
	t.Parallel()
	repository, cache := trimFixture(t)
	kept := []string{
		cacheFile(t, cache, "00/now", 1000, 0),
		cacheFile(t, cache, "00/almost", 1000, 2*time.Hour-time.Second),
		cacheFile(t, cache, "00/future", 1000, -time.Hour),
	}
	old := cacheFile(t, cache, "00/old", 1000, 2*time.Hour+time.Second)

	trim := trimmed(t, repository, 100)
	if trim.Removed != 1 || trim.After != 3000 || here(t, old) {
		t.Fatalf("trim = %+v, want the one old file removed", trim)
	}
	for _, file := range kept {
		if !here(t, file) {
			t.Errorf("%s was removed", file)
		}
	}
}

// A file a build used between the measurement and its removal stays.
func TestATrimLeavesAFileChangedSinceItWasMeasured(t *testing.T) {
	t.Parallel()
	_, cache := trimFixture(t)
	file := cacheFile(t, cache, "00/entry", 10, 50*time.Hour)
	resolved, err := filepath.EvalSymlinks(cache)
	if err != nil {
		t.Fatal(err)
	}
	held, err := repowrite.OpenPinnedRoot(resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	measured := buildCacheFile{folder: "00", name: "entry", size: 10, changed: trimNow.Add(-50 * time.Hour)}
	cacheFile(t, cache, "00/entry", 10, time.Minute)
	if removed, gone, err := measured.remove(held); removed || gone || err != nil || !here(t, file) {
		t.Fatalf("removed = %t, gone = %t, err = %v, want the file left", removed, gone, err)
	}
}

func TestATrimLeavesTheBookkeepingFiles(t *testing.T) {
	t.Parallel()
	repository, cache := trimFixture(t)
	var kept []string
	for _, name := range []string{"README", "trim.txt", "testexpire.txt"} {
		kept = append(kept, cacheFile(t, cache, name, 100, 500*time.Hour))
	}
	entry := cacheFile(t, cache, "00/entry", 1000, 50*time.Hour)
	trim := trimmed(t, repository, 10)
	if trim.Removed != 1 || trim.After != 300 || here(t, entry) {
		t.Fatalf("trim = %+v, want only the entry removed", trim)
	}
	for _, file := range kept {
		if !here(t, file) {
			t.Errorf("%s was removed", file)
		}
	}
}

// Links are removed as links: what they point at, file or folder, is not
// touched, read, or counted.
func TestATrimRemovesALinkAndNeverFollowsIt(t *testing.T) {
	t.Parallel()
	repository, cache := trimFixture(t)
	outside := t.TempDir()
	target := cacheFile(t, outside, "folder/target", 5000, 500*time.Hour)
	cacheFile(t, cache, "00/entry", 1000, 50*time.Hour)
	toFile, toFolder := filepath.Join(cache, "00", "file"), filepath.Join(cache, "00", "folder")
	for link, to := range map[string]string{toFile: target, toFolder: filepath.Dir(target)} {
		if err := os.Symlink(to, link); err != nil {
			t.Skipf("this filesystem does not support symlinks: %v", err)
		}
	}
	// A link's own time cannot be set portably, so the trim is taken long after
	// the links were made.
	later := time.Now().Add(1000 * time.Hour)
	trim, err := TrimBuildCache(context.Background(), repository, 10, later)
	if err != nil {
		t.Fatal(err)
	}
	if trim.Before >= 5000 || trim.Removed != 3 || here(t, toFile) || here(t, toFolder) {
		t.Fatalf("trim = %+v, want the entry and both links removed and the target not counted", trim)
	}
	if !here(t, target) {
		t.Fatal("what a link pointed at was removed")
	}
}

// A cache folder that is itself a link is refused, and what it points at stays.
func TestATrimRefusesACacheFolderThatIsALink(t *testing.T) {
	t.Parallel()
	repository, cache := trimFixture(t)
	outside := t.TempDir()
	target := cacheFile(t, outside, "00/entry", 5000, 500*time.Hour)
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, cache); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}
	if _, err := TrimBuildCache(context.Background(), repository, 10, trimNow); err == nil || !here(t, target) {
		t.Fatalf("err = %v, want the link refused and its target left", err)
	}
}

func TestATrimOfNoCacheIsNothing(t *testing.T) {
	t.Parallel()
	repository, _ := trimFixture(t)
	for _, directory := range []string{repository, t.TempDir(), ""} {
		if trim := trimmed(t, directory, 10); trim != (BuildCacheTrim{}) {
			t.Errorf("%q: trim = %+v, want nothing", directory, trim)
		}
	}
}
