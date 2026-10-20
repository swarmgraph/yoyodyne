package repowrite

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// This file is every change a Root makes. Each one opens the root afresh and
// holds it for the whole of the change, and makes the change through the
// descriptor-level primitive, so where it lands is decided by the filesystem
// at the moment of the change rather than by a path checked before it.

// begin opens the root for one operation and names the operation in any
// refusal that comes back from it.
func (r Root) begin(verb, relative string) (*anchor, string, error) {
	clean, err := Relative(relative)
	if err != nil {
		return nil, "", err
	}
	a, err := openAnchor(r.path)
	if err != nil {
		return nil, "", describe(err, verb, r.path, clean)
	}
	return a, clean, nil
}

// describe fills in the operation, root and path of a refusal met inside it.
func describe(err error, verb, root, rel string) error {
	var refused *RefusalError
	if errors.As(err, &refused) {
		if refused.Operation == "" {
			refused.Operation = verb
		}
		if refused.Root == "" {
			refused.Root = root
		}
		if refused.Path == "" || refused.Path == "." {
			refused.Path = rel
		}
	}
	return err
}

// landed is where a root-relative path is, for the answer a caller is given
// after the change has been made. It is the path Resolve gives where it can,
// so a caller sees the same answer it always has; nothing writes to it.
func (r Root) landed(clean string) string {
	if _, resolved, err := r.resolve(clean); err == nil {
		return resolved
	}
	return filepath.Join(r.path, filepath.FromSlash(clean))
}

// Appender appends to one file in batches of at most 64 KiB. Before and after
// each batch it checks that the directory is still beneath the root and that
// the file still has one link, and it stops at the first failure: a link that
// appears outside the root sees at most the one batch being written when it
// appeared, and nothing after it.
type Appender struct {
	writer *appendWriter
	root   string
	path   string
}

func (a *Appender) Write(data []byte) (int, error) {
	n, err := a.writer.Write(data)
	return n, describe(err, "append to", a.root, a.path)
}

// Sync makes what has been appended durable.
func (a *Appender) Sync() error { return a.writer.Sync() }

// Close releases the file and its directory.
func (a *Appender) Close() error { return a.writer.Close() }

// OpenAppender opens the file a root-relative path names for appending,
// creating it and any missing directories above it. The file is opened refusing
// a link at its name, and must be a regular file with one link; the directory
// is checked before and after the open.
//
// There is no way to open a file for appending and hand the descriptor on. The
// checks before and after each batch run only on writes this package makes, so
// a descriptor given to another process would carry its writes out unchecked;
// a child's output reaches its log through a pipe its launcher reads and
// appends from instead (internal/slack/logrelay.go).
//
// The modes are the caller's, unlike the fixed ones a repository document is
// written with, because what this opens is not a repository document: a file
// holding what the harness is doing, under a state directory, wants the
// permissions its own root does rather than a checkout's.
func (r Root) OpenAppender(relative string, file, directory fs.FileMode) (*Appender, error) {
	a, clean, err := r.begin("append to", relative)
	if err != nil {
		return nil, err
	}
	defer a.close()
	dir, name := splitTarget(clean)
	held, err := a.makeDirectories(dir, directory)
	if err != nil {
		return nil, describe(err, "append to", r.path, clean)
	}
	opened, err := held.openExisting(name, os.O_WRONLY|os.O_APPEND, file, true)
	if err != nil {
		held.close()
		return nil, describe(err, "append to", r.path, clean)
	}
	// The appender outlives this call, so it keeps its own hold on the root.
	own, err := a.duplicate()
	if err != nil {
		opened.Close()
		held.close()
		return nil, err
	}
	held.anchor = own
	return &Appender{writer: &appendWriter{dir: held, file: opened, name: name, anchor: own}, root: r.path, path: clean}, nil
}

// Append adds content to the end of the file a root-relative path names,
// creating it and any missing directories above it, and syncs it.
func (r Root) Append(relative string, content []byte, file, directory fs.FileMode) error {
	appender, err := r.OpenAppender(relative, file, directory)
	if err != nil {
		return err
	}
	if _, err := appender.Write(content); err != nil {
		return errors.Join(err, appender.Close())
	}
	if err := appender.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync %s: %w", relative, err), appender.Close())
	}
	return appender.Close()
}

// OpenLock opens the lock file a root-relative path names, creating it and any
// missing directories above it. A lock is identified by the file itself, so it
// is created once and never replaced; one found with a second link, or as a
// link, is refused. The descriptor is read-only: an advisory lock needs no
// write.
func (r Root) OpenLock(relative string, file, directory fs.FileMode) (*os.File, error) {
	a, clean, err := r.begin("lock", relative)
	if err != nil {
		return nil, err
	}
	defer a.close()
	dir, name := splitTarget(clean)
	held, err := a.makeDirectories(dir, directory)
	if err != nil {
		return nil, describe(err, "lock", r.path, clean)
	}
	defer held.close()
	opened, err := held.openExisting(name, os.O_RDONLY, file, true)
	if err != nil {
		return nil, describe(err, "lock", r.path, clean)
	}
	return opened, nil
}

// Truncate cuts the existing file a root-relative path names to size bytes,
// and returns where it landed.
//
// It is here for the append-only log that has to lose an unfinished last line.
// A file is never cut in place: the part kept is published as a new file and
// swapped with the old one, which is then removed, so a link to the old file
// from outside the root keeps exactly what it had. The new file keeps the old
// one's permissions. Nothing is created: a file that is not there has nothing
// to cut.
func (r Root) Truncate(relative string, size int64) (string, error) {
	a, clean, err := r.begin("cut", relative)
	if err != nil {
		return "", err
	}
	defer a.close()
	dir, name := splitTarget(clean)
	held, err := a.hold(dir)
	if err != nil {
		return "", describe(err, "cut", r.path, clean)
	}
	defer held.close()
	if err := held.shorten(name, size); err != nil {
		return "", describe(err, "cut", r.path, clean)
	}
	return r.landed(clean), nil
}

// WriteFile replaces the document a repository-relative path names and returns
// where it landed.
//
// The bytes go into a new file beside the target, which is synced and then
// published: by a rename that refuses to replace when nothing is there, and by
// swapping it with the existing document when something is. An interrupted
// write leaves the previous document whole, a reader never sees a partial
// file, and the previous document's content is never changed — a link to it
// from outside the repository keeps what it had. A target that is a link is
// refused rather than written through or replaced.
func (r Root) WriteFile(relative string, content []byte) (string, error) {
	if err := r.publish("write", relative, content, true, filePermissions, directoryPermissions); err != nil {
		return "", err
	}
	clean, _ := Relative(relative)
	return r.landed(clean), nil
}

// CreateFile writes a document a root-relative path names only if nothing is
// there yet, and reports whether this call was the one that wrote it.
//
// It is here for the record two processes may race to make first, where the
// second must read the first one's rather than replace it: WriteFile's swap
// would let the later writer win silently. The bytes go into a new file and
// are published by a rename that refuses to replace, so the document is never
// seen half-written and exactly one of the racers creates it. A target that
// already exists is not a failure: created is false, and the caller reads what
// is there.
func (r Root) CreateFile(relative string, content []byte) (target string, created bool, err error) {
	err = r.publish("create", relative, content, false, filePermissions, directoryPermissions)
	clean, _ := Relative(relative)
	if errors.Is(err, fs.ErrExist) {
		return r.landed(clean), false, nil
	}
	if err != nil {
		return "", false, err
	}
	return r.landed(clean), true, nil
}

// CreateFileMode is CreateFile with the caller's modes, for a record that is not
// a repository document. A name that already exists is reported as an error
// that is fs.ErrExist, and is left as it was.
func (r Root) CreateFileMode(relative string, content []byte, file, directory fs.FileMode) error {
	return r.publish("create", relative, content, false, file, directory)
}

// ReplaceFile is WriteFile with the caller's modes, for a record that is not a
// repository document: a file under a state directory wants the permissions
// its own root does rather than a checkout's.
func (r Root) ReplaceFile(relative string, content []byte, file, directory fs.FileMode) error {
	return r.publish("write", relative, content, true, file, directory)
}

func (r Root) publish(verb, relative string, content []byte, replace bool, file, directory fs.FileMode) error {
	a, clean, err := r.begin(verb, relative)
	if err != nil {
		return err
	}
	defer a.close()
	dir, name := splitTarget(clean)
	held, err := a.makeDirectories(dir, directory)
	if err != nil {
		return describe(err, verb, r.path, clean)
	}
	defer held.close()
	temporary, err := held.createTemporary(file)
	if err != nil {
		return describe(err, verb, r.path, clean)
	}
	if _, err := temporary.file.Write(content); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", clean, err), temporary.discard())
	}
	return describe(temporary.publish(name, replace, nil), verb, r.path, clean)
}

// MakeDirectory creates the directory a root-relative path names, and any
// missing directory above it, and returns where it landed.
//
// It is here for the caller that needs the directory rather than anything in it:
// a per-run scratch directory is created by the harness and written into by an
// agent. Each missing directory is created from the open descriptor of the one
// above it, with the one above checked on both sides. Creating is idempotent,
// so a caller that asks twice is given the same directory rather than a
// failure — which is what a run resumed by a second process needs.
//
// The mode is the caller's for the reason OpenAppend's are.
func (r Root) MakeDirectory(relative string, mode fs.FileMode) (string, error) {
	a, clean, err := r.begin("create", relative)
	if err != nil {
		return "", err
	}
	defer a.close()
	held, err := a.makeDirectories(clean, mode)
	if err != nil {
		return "", describe(err, "create", r.path, clean)
	}
	held.close()
	return r.landed(clean), nil
}

// RemoveDirectory removes the directory a root-relative path names, and
// everything in it, and returns where it removed from.
//
// What needs it is bookkeeping the harness has to take back out of a
// repository — a worktree registration a killed `git worktree add` left
// half-written, which Git's own prune does not reach and which fails every
// later command that walks the registrations.
//
// A final component that is a symlink is refused rather than removed: the link
// would go and its target would stay, which is a removal that did not remove
// what the caller named. A final component that is not a directory is refused,
// because every caller for this asks for a directory by name. The directory is
// moved into the root's holding directory and removed there, depth first, only
// if it is the one that was checked. Removing what is not there is a removal
// already made, so a sweep that runs twice does not fail the second time.
func (r Root) RemoveDirectory(relative string) (string, error) {
	return r.remove(relative, directoryEntry)
}

// RemoveFile removes the file a root-relative path names, and returns where it
// removed from.
//
// What needs it is a file the harness takes back out of a directory it does
// not own — a launchd job's property list the supervisor retires, under the
// user's LaunchAgents directory. The refusals are RemoveDirectory's turned
// round: a final component that is a symlink is refused, and so is one that is
// a directory. Removing what is not there is a removal already made.
func (r Root) RemoveFile(relative string) (string, error) {
	return r.remove(relative, fileEntry)
}

func (r Root) remove(relative string, kind entryKind) (string, error) {
	a, clean, err := r.begin("remove", relative)
	if err != nil {
		return "", err
	}
	defer a.close()
	target := r.landed(clean)
	dir, name := splitTarget(clean)
	held, err := a.hold(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return target, nil
	}
	if err != nil {
		return "", describe(err, "remove", r.path, clean)
	}
	defer held.close()
	if err := held.removeEntry(name, kind); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return target, nil
		}
		return "", describe(err, "remove", r.path, clean)
	}
	return target, nil
}

// RenameDirectory moves the directory one root-relative path names to another
// inside the same root, and returns where it landed.
//
// What needs it is `yoyo project rename`, which moves a project's directory,
// and everything under it, to the name of its new id. A source that is a
// symlink is refused, because the link would move and what it names would not,
// and one that is not a directory is refused, because the caller named one. A
// destination that already exists is refused rather than replaced: the rename
// is the one operation that would otherwise put one project's records over
// another's.
func (r Root) RenameDirectory(from, to string) (string, error) {
	return move(r, from, r, to, directoryPermissions, true)
}

// Move moves the file or directory one root-relative path names under one root
// to a root-relative path under another, and returns where it landed. The two
// roots may be the same.
//
// It is RenameDirectory across two roots, for `yoyo home migrate`, which moves a
// machine's records from the home earlier builds kept them in to the machine
// home. A source that is a symlink is refused, a destination that already
// exists is refused rather than replaced, and directoryMode is what the
// directories created above the destination are given. A move is one rename,
// so it is either made or not: two roots on different filesystems refuse
// rather than being copied, and the source is left where it was.
func Move(fromRoot Root, from string, toRoot Root, to string, directoryMode fs.FileMode) (string, error) {
	return move(fromRoot, from, toRoot, to, directoryMode, false)
}

func move(fromRoot Root, from string, toRoot Root, to string, directoryMode fs.FileMode, directoryOnly bool) (string, error) {
	source, cleanFrom, err := fromRoot.begin("move", from)
	if err != nil {
		return "", err
	}
	defer source.close()
	destination, cleanTo, err := toRoot.begin("move", to)
	if err != nil {
		return "", err
	}
	defer destination.close()
	fromDir, fromName := splitTarget(cleanFrom)
	fromHeld, err := source.hold(fromDir)
	if err != nil {
		return "", describe(err, "move", fromRoot.path, cleanFrom)
	}
	defer fromHeld.close()
	if err := fromHeld.checkMovable(fromName, directoryOnly); err != nil {
		return "", describe(err, "move", fromRoot.path, cleanFrom)
	}
	toDir, toName := splitTarget(cleanTo)
	toHeld, err := destination.makeDirectories(toDir, directoryMode)
	if err != nil {
		return "", describe(err, "move", toRoot.path, cleanTo)
	}
	defer toHeld.close()
	if err := moveEntry(fromHeld, fromName, toHeld, toName); err != nil {
		return "", describe(err, "move", fromRoot.path, cleanFrom)
	}
	return toRoot.landed(cleanTo), nil
}

// copyPrefix copies the first size bytes of from into to, and pads to size
// when from holds fewer.
func copyPrefix(to *os.File, from io.Reader, size int64) error {
	copied, err := io.Copy(to, io.LimitReader(from, size))
	if err != nil {
		return err
	}
	if copied < size {
		return to.Truncate(size)
	}
	return nil
}

// fileID is a file's identity: the same device and file number is the same
// file, whatever names it has.
type fileID struct{ dev, ino uint64 }

func joinRel(base, rel string) string {
	if base == "." || base == "" {
		return rel
	}
	if rel == "." || rel == "" {
		return base
	}
	return base + "/" + rel
}

// entryKind is what a removal was asked to remove.
type entryKind int

const (
	anyEntry entryKind = iota
	treeEntry
	fileEntry
	directoryEntry
)

// splitTarget is a root-relative path's directory and final name.
func splitTarget(rel string) (directory, name string) {
	return path.Dir(rel), path.Base(rel)
}
