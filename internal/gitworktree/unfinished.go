package gitworktree

// Clearing what a killed `git worktree add` leaves behind.
//
// An add registers its entry under the common Git directory's worktrees/ and
// then fills it in one file at a time: a locked file saying "initializing"
// first, then gitdir, commondir and HEAD, then the checkout that writes the
// index, and last of all it removes the lock. A process killed anywhere along
// that line leaves the entry as it stood, and nothing Git has clears it. `git
// worktree prune` skips an entry that is locked and judges an unlocked one by
// its gitdir file, which such an entry usually has; `git worktree remove` and
// `git worktree unlock` find the entry through the listing, which is what the
// entry breaks. So the entry stays, and where the file it was killed writing
// was commondir, every command that walks the registrations — every listing,
// every creation, a rebase checking that a branch is not checked out elsewhere
// — fails over it from then on: one killed add stops every later run on the
// repository until a person removes a directory by hand.
//
// What clears it is here, and it runs where nothing can still be writing the
// entry. The harness's own adds hold the registry lease from before `git
// worktree add` until the new checkout is verified, so under that lease an
// entry that is not finished is not one of ours in flight. An add somebody else
// is running holds no lease, so an entry is also left alone while it is young
// enough to be one of those still working — see unfinishedRegistrationGrace.
// Only the registration is removed: the branch the add created is a branch
// like any other, and a directory the add left on disk is the same thing to
// this package as any directory Git is not managing, which it never touches.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

const (
	// unfinishedRegistrationGrace is how long an unfinished registration is left
	// alone after the last write to it. It exists for an add the harness did not
	// make: a person's, or one an agent ran inside a checkout, which takes no
	// lease and so cannot be told from a dead one by the lease alone. What tells
	// them apart is that a live add keeps writing — it puts the index down
	// through a lock file in the same directory — and a dead one stopped. A
	// creation waits such an entry out rather than failing over it, because a
	// run lost at creation costs more than the wait.
	unfinishedRegistrationGrace = time.Minute
	// unfinishedRegistrationPoll is how often that wait looks again. It is a
	// poll rather than one sleep to the end of the grace because what the wait
	// is actually for is the neighbouring add getting past the two files a walk
	// dies on, which takes milliseconds: sleeping the whole grace would make
	// every creation that met a live add beside it a minute slower, and the
	// grace is the bound on the wait rather than its length.
	unfinishedRegistrationPoll = 20 * time.Millisecond
	// initializingLock is what `git worktree add` writes into an entry's locked
	// file before it fills the entry in, and removes once it has. It is Git's
	// own marker for an add in flight, so an entry still carrying it with no
	// index is one whose add never reached the end.
	initializingLock = "initializing"
	// indexFile is the last thing an add writes into its entry before it
	// unlocks: the checkout's index. An entry that has one was checked out, so
	// whatever else it lacks it is not a killed add — a worktree added with
	// --no-checkout has no index either, and has to be told apart from one.
	indexFile = "index"
)

// UnfinishedRegistration is one entry under worktrees/ that a `git worktree add`
// registered and never finished filling in, and what was done about it. Kept is
// why it was left where it is, and it is empty exactly when Cleared is set.
type UnfinishedRegistration struct {
	// Name is the entry's directory name under worktrees/, which is what a
	// person removing one by hand would have named.
	Name string `json:"name"`
	// Path is the directory the entry's gitdir named, where the add had got as
	// far as writing it and the directory is still on disk. It is what the
	// clearing leaves behind, so it is named only where there is something to
	// find.
	Path string `json:"path,omitempty"`
	// Reason is what marks the entry as an add that never finished.
	Reason  string `json:"reason"`
	Cleared bool   `json:"cleared"`
	Kept    string `json:"kept,omitempty"`
}

// Describe is the one line said about the entry, whichever way it went.
func (u UnfinishedRegistration) Describe() string {
	if u.Cleared {
		line := fmt.Sprintf("cleared the worktree registration %s, left by a git worktree add that never finished: %s", u.Name, u.Reason)
		if u.Path != "" {
			line += fmt.Sprintf("; the directory it named at %s is not a worktree any more, and is left where it is", u.Path)
		}
		return line
	}
	return fmt.Sprintf("left the worktree registration %s, which a git worktree add has not finished (%s): %s", u.Name, u.Reason, u.Kept)
}

// unfinishedRegistration is one entry judged unfinished, with what the judgement
// needs beside what is reported: how long ago anything last wrote to it, and
// whether Git refuses to walk the registrations while it stands.
type unfinishedRegistration struct {
	UnfinishedRegistration
	age time.Duration
	// blocking is an entry a listing cannot describe — a file the walk reads
	// absent or empty — which is the shape that fails every creation. An entry
	// killed later, during its checkout, is walked over fine and stops nothing;
	// it is cleared on the same terms, but nobody waits for it.
	blocking bool
}

// settleRegistrations clears every registration an add never finished, where
// nothing can still be writing it, and reports each one it met. The caller holds
// the registry lease, which is what rules out a creation of the harness's own
// being the add in question; the grace rules out one somebody else is running.
// With wait set, a blocking entry younger than the grace is waited for rather
// than left, so a creation that meets one killed a moment ago still gets to
// create; an entry that blocks nothing is left for the next pass to judge.
//
// A clearing that could not be made keeps the entry with the reason, as every
// other refusal in this package does: what stopped it is reported, and whatever
// the caller was doing carries on — a creation over an entry that stays will
// fail as it always did, and a sweep reports what it could not do beside what it
// could.
func (m *Manager) settleRegistrations(ctx context.Context, wait bool) ([]UnfinishedRegistration, error) {
	directory, err := m.commonGitDirectory(ctx)
	if err != nil {
		return nil, err
	}
	root, err := repowrite.NewRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve the common Git directory: %w", err)
	}
	unfinished, err := readUnfinishedRegistrations(root)
	if err != nil {
		return nil, err
	}
	if wait {
		if unfinished, err = waitOutLiveRegistrations(ctx, root, unfinished); err != nil {
			return nil, err
		}
	}
	settled := make([]UnfinishedRegistration, 0, len(unfinished))
	for _, entry := range unfinished {
		if entry.age < unfinishedRegistrationGrace {
			entry.Kept = fmt.Sprintf("it was last written %s ago and may still be being filled in", entry.age.Round(time.Second))
			settled = append(settled, entry.UnfinishedRegistration)
			continue
		}
		// Clearing an entry changes the registrations under the lease's own
		// listing, and one that failed part-way may have changed them as well, so
		// the listing kept on the lease goes either way — see registryState.
		heldRegistry(ctx).forgetListing()
		if err := clearRegistration(root, entry.Name); err != nil {
			entry.Kept = fmt.Sprintf("it could not be removed: %v", err)
		} else {
			entry.Cleared = true
		}
		settled = append(settled, entry.UnfinishedRegistration)
	}
	return settled, nil
}

// waitOutLiveRegistrations waits while an entry that would stop a registration
// walk is young enough to be an add somebody else is still running, reading the
// bookkeeping again until either no such entry is left or the ones that are have
// aged past the grace.
//
// It ends on the first of those far more often than the second, and that is the
// point of looking again rather than sleeping to the end of the grace. An add in
// flight stops blocking the moment it writes the file a walk dies on, which is
// within milliseconds of registering; what is left after the grace is an add
// that stopped, and clearing that is what the caller is here for. One sleep to
// the end of the grace would charge every creation that happened to meet a live
// neighbour a full minute for a wait that was over almost at once.
func waitOutLiveRegistrations(ctx context.Context, root repowrite.Root, unfinished []unfinishedRegistration) ([]unfinishedRegistration, error) {
	for longestRemainingGrace(unfinished) > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(unfinishedRegistrationPoll):
		}
		read, err := readUnfinishedRegistrations(root)
		if err != nil {
			return nil, err
		}
		unfinished = read
	}
	return unfinished, nil
}

// longestRemainingGrace is how long the youngest blocking entry has left inside
// the grace, or zero when none is inside it.
func longestRemainingGrace(unfinished []unfinishedRegistration) time.Duration {
	var remaining time.Duration
	for _, entry := range unfinished {
		if left := unfinishedRegistrationGrace - entry.age; entry.blocking && left > remaining {
			remaining = left
		}
	}
	return remaining
}

// readUnfinishedRegistrations reads every registration and keeps the ones an
// add never finished. A worktrees/ directory that does not exist is a repository
// that has never had a linked worktree, which holds nothing to settle.
func readUnfinishedRegistrations(root repowrite.Root) ([]unfinishedRegistration, error) {
	registrations, err := os.ReadDir(filepath.Join(root.Path(), worktreeRegistrations))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the worktree registrations: %w", err)
	}
	var unfinished []unfinishedRegistration
	now := time.Now()
	for _, registration := range registrations {
		if !registration.IsDir() {
			continue
		}
		directory := filepath.Join(root.Path(), worktreeRegistrations, registration.Name())
		reason, ok := unfinishedReason(directory)
		if !ok {
			continue
		}
		_, describable := registeredEntry(directory)
		entry := unfinishedRegistration{
			UnfinishedRegistration: UnfinishedRegistration{Name: registration.Name(), Reason: reason},
			// A clock that has gone backwards makes an entry look written in the
			// future, which is the same thing as written just now.
			age:      max(now.Sub(lastWritten(directory)), 0),
			blocking: !describable,
		}
		if gitdir, written := readWritten(filepath.Join(directory, "gitdir")); written {
			// gitdir names the checkout's own .git file, read the way registeredEntry
			// reads it. A directory that is not there any more is nothing anybody is
			// left with, so it is not named.
			path := filepath.Dir(gitdir)
			if !filepath.IsAbs(path) {
				path = filepath.Clean(filepath.Join(directory, path))
			}
			if _, err := os.Stat(path); err == nil {
				entry.Path = path
			}
		}
		unfinished = append(unfinished, entry)
	}
	return unfinished, nil
}

// unfinishedReason judges one registration, and says what marks it as an add
// that never finished. An entry with an index was checked out and is never one,
// whatever else it lacks: the shapes stepped over here are Git's own marker for
// an add in flight still standing, and a file the add creates before the
// checkout that was never written. An entry with no index and neither of those
// — a worktree added with --no-checkout — is somebody's and is left alone.
func unfinishedReason(directory string) (string, bool) {
	if _, err := os.Lstat(filepath.Join(directory, indexFile)); !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if lock, written := readWritten(filepath.Join(directory, "locked")); written && lock == initializingLock {
		return "its lock still says initializing, which the add removes only once it has checked the worktree out", true
	}
	for _, name := range []string{"gitdir", "commondir", "HEAD"} {
		if _, written := readWritten(filepath.Join(directory, name)); !written {
			return fmt.Sprintf("its %s was created and never written", name), true
		}
	}
	return "", false
}

// lastWritten is the newest modification time among a registration's directory
// and the files in it, which is the last moment anything was writing the entry.
// A live add keeps that recent — the checkout puts the index down through a lock
// file beside the others — and a dead one stops moving it.
func lastWritten(directory string) time.Time {
	var newest time.Time
	if info, err := os.Lstat(directory); err == nil {
		newest = info.ModTime()
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return newest
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

// clearUnfinishedRegistrationFor clears the registration an add the caller ran
// itself left unfinished at path, and nothing else. It is for an add killed so
// early that its checkout directory never got the .git file `git worktree
// remove` finds a worktree by, so the removal refuses the path and the
// registration, still locked as initializing, is one `git worktree prune`
// skips. No grace applies, unlike settleRegistrations: the caller holds the
// registry lease and the add that registered path was its own and has ended, so
// nothing can still be writing the entry. A registration that is finished, or
// names another path, is left alone.
func (m *Manager) clearUnfinishedRegistrationFor(ctx context.Context, path string) error {
	directory, err := m.commonGitDirectory(ctx)
	if err != nil {
		return err
	}
	root, err := repowrite.NewRoot(directory)
	if err != nil {
		return fmt.Errorf("resolve the common Git directory: %w", err)
	}
	registrations, err := os.ReadDir(filepath.Join(root.Path(), worktreeRegistrations))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the worktree registrations: %w", err)
	}
	wanted := map[string]bool{filepath.Clean(path): true}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		wanted[filepath.Join(parent, filepath.Base(path))] = true
	}
	for _, registration := range registrations {
		if !registration.IsDir() {
			continue
		}
		entry := filepath.Join(root.Path(), worktreeRegistrations, registration.Name())
		gitdir, written := readWritten(filepath.Join(entry, "gitdir"))
		if !written {
			continue
		}
		named := filepath.Dir(gitdir)
		if !filepath.IsAbs(named) {
			named = filepath.Clean(filepath.Join(entry, named))
		}
		if !wanted[named] {
			continue
		}
		if _, unfinished := unfinishedReason(entry); !unfinished {
			continue
		}
		heldRegistry(ctx).forgetListing()
		if err := clearRegistration(root, registration.Name()); err != nil {
			return fmt.Errorf("clear the unfinished worktree registration %s: %w", registration.Name(), err)
		}
	}
	return nil
}

// clearRegistration removes one registration directory, and nothing that is not
// one. The name came from reading worktrees/ itself, and the removal goes
// through the one primitive that decides confinement against the filesystem —
// so the path is resolved against the common Git directory immediately before
// it is removed, a component pointing out of that directory is refused rather
// than followed, and a final component that is a link or is not a directory is
// refused too. A registration is Git's own bookkeeping, and the one thing this
// may take out of the repository is an entry Git itself would have removed had
// the add reached its end.
func clearRegistration(root repowrite.Root, name string) error {
	_, err := root.RemoveDirectory(worktreeRegistrations + "/" + name)
	return err
}
