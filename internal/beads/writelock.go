package beads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// writeLockDirectory is where the lock each item's writes queue on lives,
// relative to the store bd writes to. It is inside the store's own directory
// because that is what every writer to one store shares whatever process it is
// and whichever directory it reached the store from, and bd's own ignore file
// there already keeps `*.lock` out of the repository.
const writeLockDirectory = "yoyodyne-writes"

// write runs one bd invocation that changes the item id names, holding that
// item's write lock for as long as bd runs.
//
// bd does not serialize two writes to one item. An append is a read of the notes
// already there and a write of them with the new line added, and a metadata key
// is set the same way, so two that overlap each read the same notes and the one
// that finishes second writes back over the first. Both exit 0, and each one's
// own answer carries its own line, so nothing a writer can read off its own
// invocation says the other was lost: against bd 1.1.2, six appends and three
// other writes made at once to one item lost an append or a metadata key in 4
// of 15 batches, every invocation reporting success
// (docs/diagnoses/yoyodyne-ifd-433-23-concurrent-writes-to-one-item.md). A
// lost append is a lost outcome, price, or goal attribution, which is the loss
// nobody finds.
//
// So writes to one item queue here, across processes: the lock is an advisory
// lock on a file inside the store bd will write to, which the operating system
// drops when its holder exits, so a writer that dies holds nothing. Writes to
// different items do not queue on each other — nothing was lost between them —
// so a bd invocation that stalls holds up only the writes to its own item. The
// wait is bounded by the client's own bound on an invocation, and a write that
// waited it out fails rather than running unqueued.
//
// The store is found the way bd finds it from the same directory and the same
// environment (storeDirectory), so two clients reaching one store through a
// subdirectory, a symlink, a redirect, or a linked worktree queue on one file.
// Where that finds no store, bd run there finds none either and says so itself,
// so the write is left to bd rather than queued against nothing.
func (c Client) write(ctx context.Context, id string, args ...string) ([]byte, error) {
	unlock, err := c.lockItem(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return c.run(ctx, frameNoteArguments(args)...)
}

// lockItem takes the write lock for one item, waiting for whoever holds it, and
// returns what releases it.
func (c Client) lockItem(ctx context.Context, id string) (func(), error) {
	if err := validateIssueID(id); err != nil {
		return nil, err
	}
	// A client naming no directory runs bd wherever this process stands. No
	// harness client is built that way, since every one names the repository; the
	// clients that are are this package's and its callers' tests, scripting bd's
	// answers, and resolving their writes from the process's directory would take
	// locks in the live store of whatever checkout the suite runs in.
	if c.Dir == "" {
		return func() {}, nil
	}
	store := storeDirectory(c.Dir, os.Getenv("BEADS_DIR"))
	if store == "" {
		return func() {}, nil
	}
	root, err := repowrite.NewRoot(store)
	if err != nil {
		return nil, fmt.Errorf("lock writes to %s: %w", id, err)
	}
	file, err := root.OpenAppend(writeLockDirectory+"/"+id+".lock", 0o600, 0o700)
	if err != nil {
		return nil, fmt.Errorf("lock writes to %s: %w", id, err)
	}
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	if err := lockWriteFile(waiting, file); err != nil {
		_ = file.Close()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf("another write to %s held it for longer than %s, so this one was not made: %w", id, c.timeout(), err)
		}
		return nil, fmt.Errorf("lock writes to %s: %w", id, err)
	}
	return func() {
		_ = unlockWriteFile(file)
		_ = file.Close()
	}, nil
}

// storeDirectory is the store directory bd run in dir would write to, resolved
// through symlinks, or empty where bd would find none. It follows bd's own
// order (FindBeadsDir in bd's internal/beads package), for the layouts a
// harness client is pointed at:
//
//  1. BEADS_DIR, where it names a store.
//  2. A `.beads` in dir or any directory above it, up to and including the root
//     of the Git checkout dir is in, following a redirect file inside it.
//  3. In a linked Git worktree, the worktree's own `.beads` only where it holds a
//     database of its own — a worktree checks out the tracked half of `.beads`
//     and not the database — and otherwise the shared `.beads` beside the main
//     repository's Git directory.
//
// bd also recognises a Jujutsu secondary workspace and prefers a branch
// worktree over a detached snapshot; neither is a layout the harness writes
// from, and both resolve here as the directory they are rather than the one bd
// would move to.
func storeDirectory(dir, beadsDirEnv string) string {
	if beadsDirEnv != "" {
		if store := followRedirect(canonicalPath(beadsDirEnv)); hasStoreFiles(store) {
			return store
		}
	}
	start := canonicalPath(dir)
	checkout, worktreeCommon := gitCheckout(start)
	for current := start; ; {
		if current == checkout && worktreeCommon != "" {
			return worktreeStore(checkout, worktreeCommon)
		}
		candidate := filepath.Join(current, ".beads")
		if isDirectory(candidate) {
			if store := followRedirect(candidate); hasStoreFiles(store) {
				return store
			}
		}
		parent := filepath.Dir(current)
		if current == checkout || parent == current {
			return ""
		}
		current = parent
	}
}

// worktreeStore is step 3 above: the store bd uses from the root of a linked
// worktree whose Git common directory is common.
func worktreeStore(checkout, common string) string {
	own := filepath.Join(checkout, ".beads")
	if isDirectory(own) {
		if _, err := os.Stat(filepath.Join(own, "redirect")); err == nil {
			if store := followRedirect(own); store != own && hasStoreFiles(store) {
				return store
			}
		}
		if hasDatabase(own) {
			return own
		}
	}
	shared := filepath.Join(common, ".beads")
	if filepath.Base(common) == ".git" {
		shared = filepath.Join(filepath.Dir(common), ".beads")
	}
	if isDirectory(shared) {
		if store := followRedirect(canonicalPath(shared)); hasStoreFiles(store) {
			if hasDatabase(store) || !hasStoreFiles(own) {
				return store
			}
		}
	}
	if isDirectory(own) && hasStoreFiles(own) {
		return own
	}
	return ""
}

// gitCheckout is the root of the Git checkout start is inside, and, where that
// checkout is a linked worktree, its common Git directory. A linked worktree's
// `.git` is a file naming its own Git directory, and that directory's
// `commondir` names the repository's; both are read off disk rather than asked
// of git, because this runs before every tracker write.
func gitCheckout(start string) (checkout, common string) {
	for current := start; ; {
		dotGit := filepath.Join(current, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			if info.IsDir() {
				return current, ""
			}
			return current, linkedCommonDirectory(current, dotGit)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", ""
		}
		current = parent
	}
}

func linkedCommonDirectory(checkout, dotGit string) string {
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(checkout, gitDir)
	}
	data, err = os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(data))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return canonicalPath(common)
}

// followRedirect is bd's one level of redirection: a `redirect` file in a
// `.beads` directory names the store to use instead, relative to the directory
// the `.beads` is in, and its first line that is not blank or a comment is the
// name. A redirect naming nothing usable leaves the directory as it was.
func followRedirect(beadsDir string) string {
	data, err := os.ReadFile(filepath.Join(beadsDir, "redirect"))
	if err != nil {
		return beadsDir
	}
	target := ""
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			target = line
			break
		}
	}
	if target == "" {
		return beadsDir
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(beadsDir), target)
	}
	target = canonicalPath(target)
	if !isDirectory(target) {
		return beadsDir
	}
	return target
}

// hasStoreFiles is bd's test for a directory that is a store: its project
// files, or a database.
func hasStoreFiles(dir string) bool {
	for _, name := range []string{"metadata.json", "config.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return hasDatabase(dir)
}

// hasDatabase is bd's stricter test, for a directory holding the database
// itself rather than the tracked files a checkout carries.
func hasDatabase(dir string) bool {
	if isDirectory(filepath.Join(dir, "dolt")) || isDirectory(filepath.Join(dir, "embeddeddolt")) {
		return true
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.db"))
	for _, match := range matches {
		if base := filepath.Base(match); !strings.Contains(base, ".backup") && base != "vc.db" {
			return true
		}
	}
	return false
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// canonicalPath is path made absolute and resolved through symlinks, so one
// store reached two ways is one lock file; a path that does not resolve is kept
// as written, cleaned.
func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return absolute
}
