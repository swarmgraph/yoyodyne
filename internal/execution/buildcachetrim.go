package execution

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// BuildCacheKept is how recently changed a file in the shared build cache has
// to be for a trim never to remove it: the Go command refreshes the time of an
// entry it uses at most once an hour, so a running build may be reading
// anything changed inside this.
const BuildCacheKept = 2 * time.Hour

// BuildCacheTrim is what one look at the shared build cache came to.
type BuildCacheTrim struct {
	// Folder is the cache, and empty where the harness names none for the
	// working directory or nothing has made it yet.
	Folder string
	// Before and After are the total size of the files in it either side.
	Before, After int64
	// Removed is how many files and links were removed.
	Removed int
	// Trimmed says the folder was over the limit, so a trim was taken.
	Trimmed bool
}

// TrimBuildCache holds the shared build cache of the repository
// workingDirectory belongs to under limit bytes: where the files in it come to
// more, it removes them oldest last change first until they come to 80% of the
// limit, so the next look has nothing to remove
// (docs/designs/shared-build-cache-size.md).
//
// The folder is the one WithGoBuildCache points a run at. Nothing here knows
// what the files are. A file changed within BuildCacheKept of now is never
// removed. Folders stay, and so do files standing directly in the cache folder,
// which is where the Go command keeps its bookkeeping (README, trim.txt,
// testexpire.txt) while every entry is in a folder below. A link is removed as
// a link and never followed, and anything that is neither a regular file nor a
// link is left.
//
// It removes through held directories, with the folder the cache stands in as
// the root. Nothing is kept between trims, so one cut off part-way is finished
// by the next. A file that could not be removed is named in the error beside
// what was done.
func TrimBuildCache(ctx context.Context, workingDirectory string, limit int64, now time.Time) (BuildCacheTrim, error) {
	var trim BuildCacheTrim
	cache, found := goBuildCache(workingDirectory)
	if !found {
		return trim, nil
	}
	root, err := repowrite.NewRoot(filepath.Dir(cache))
	if errors.Is(err, fs.ErrNotExist) {
		return trim, nil
	}
	if err != nil {
		return trim, fmt.Errorf("confine the trim of the build cache %s: %w", cache, err)
	}
	parent, err := repowrite.OpenPinnedRoot(root.Path())
	if err != nil {
		return trim, fmt.Errorf("open the folder the build cache %s stands in: %w", cache, err)
	}
	defer parent.Close()
	held, err := parent.OpenDirectory(filepath.Base(cache))
	if errors.Is(err, fs.ErrNotExist) {
		return trim, nil
	}
	if err != nil {
		return trim, fmt.Errorf("open the build cache %s: %w", cache, err)
	}
	defer held.Close()
	trim.Folder = held.Path()

	scan := buildCacheScan{cutoff: now.Add(-BuildCacheKept)}
	if err := scan.read(ctx, held, "."); err != nil {
		return trim, fmt.Errorf("measure the build cache %s: %w", trim.Folder, err)
	}
	trim.Before, trim.After = scan.size, scan.size
	if trim.Before <= limit {
		return trim, nil
	}
	trim.Trimmed = true
	sort.SliceStable(scan.removable, func(i, j int) bool { return scan.removable[i].changed.Before(scan.removable[j].changed) })
	target := limit / 5 * 4
	var first error
	failed := 0
	for _, file := range scan.removable {
		if trim.After <= target {
			break
		}
		if err := ctx.Err(); err != nil {
			return trim, err
		}
		removed, gone, err := file.remove(held)
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
			continue
		}
		if removed {
			trim.Removed++
		}
		if removed || gone {
			trim.After -= file.size
		}
	}
	if first != nil {
		return trim, fmt.Errorf("%d file(s) in the build cache %s could not be removed, the first: %w", failed, trim.Folder, first)
	}
	return trim, nil
}

// buildCacheFile is one file or link a trim may remove. folder is relative to
// the cache, and shared by every file in it.
type buildCacheFile struct {
	folder, name string
	size         int64
	changed      time.Time
}

type buildCacheScan struct {
	cutoff    time.Time
	size      int64
	removable []buildCacheFile
}

// read adds up one folder of the cache and everything below it. What another
// process removed meanwhile is passed over.
func (s *buildCacheScan) read(ctx context.Context, folder *repowrite.PinnedRoot, relative string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := folder.ReadDirectory(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := folder.Lstat(entry.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			below, err := folder.OpenDirectory(entry.Name())
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			err = s.read(ctx, below, filepath.Join(relative, entry.Name()))
			below.Close()
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		link := info.Mode()&fs.ModeSymlink != 0
		if !info.Mode().IsRegular() && !link {
			continue
		}
		s.size += info.Size()
		if info.ModTime().After(s.cutoff) || (relative == "." && !link) {
			continue
		}
		s.removable = append(s.removable, buildCacheFile{folder: relative, name: entry.Name(), size: info.Size(), changed: info.ModTime()})
	}
	return nil
}

// remove removes the file unless it was changed since it was read, which is a
// build using it. gone says another process had already removed it.
func (f buildCacheFile) remove(cache *repowrite.PinnedRoot) (removed, gone bool, err error) {
	folder, err := cache.OpenDirectory(f.folder)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	defer folder.Close()
	info, err := folder.Lstat(f.name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	if !info.ModTime().Equal(f.changed) || info.IsDir() {
		return false, false, nil
	}
	if err := folder.Unchanged(); err != nil {
		return false, false, err
	}
	if err := folder.Remove(f.name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, true, nil
		}
		return false, false, err
	}
	return true, false, folder.Unchanged()
}
