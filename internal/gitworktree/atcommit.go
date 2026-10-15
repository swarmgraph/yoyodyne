package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ErrNotAtCommit is what FileAtCommit answers for a path the commit does not
// carry as a regular file: absent, a directory, a symlink, or a submodule. It is
// a sentinel rather than an empty answer because an empty file is a file.
var ErrNotAtCommit = errors.New("not a regular file at that commit")

// FileAt is one file as a commit holds it.
type FileAt struct {
	// Size is the blob's size at the commit, which is known whether or not the
	// content was read.
	Size int64
	// Content is the whole of the blob, and is nil where Size exceeded what the
	// caller would read.
	Content []byte
}

// FileAtCommit reads one repository-relative path as the named commit holds it,
// rather than as any working tree has it now. It exists for the evidence a
// reviewer judges a change against: a file the change is measured against has to
// be the copy at the change's own base, because the primary checkout has moved on
// by the time a review is asked for whenever anything else was promoted meanwhile,
// and a correct change read against a later revision reads as a divergent one.
//
// A committed tree has no link to follow out of the repository, so the read is
// confined without a filesystem check; a symlink entry is refused rather than
// read, because its blob is the link's target path and not a document.
//
// The content comes back through the line-oriented process runner, so it is held
// to the size the tree entry states: a blob the runner could not return whole —
// a line past its bound, a carriage return it folded — is an error rather than a
// document that is quietly not the file.
func (m *Manager) FileAtCommit(ctx context.Context, commit, path string, maxBytes int64) (FileAt, error) {
	if strings.TrimSpace(commit) == "" {
		return FileAt{}, errors.New("a commit is required to read a file at one")
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "ls-tree", "-l", "-z", commit, "--", path)
	if err != nil {
		return FileAt{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return FileAt{}, fmt.Errorf("read %s at %s failed with exit code %d: %s", path, commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	entry, _, _ := strings.Cut(result.Stdout, "\x00")
	if entry == "" {
		return FileAt{}, fmt.Errorf("%s at %s: %w", path, commit, ErrNotAtCommit)
	}
	// The entry is "<mode> <type> <object> <size>\t<path>".
	meta, entryPath, _ := strings.Cut(entry, "\t")
	fields := strings.Fields(meta)
	if len(fields) != 4 {
		return FileAt{}, fmt.Errorf("read %s at %s returned an unreadable entry %q", path, commit, entry)
	}
	// ls-tree answers a directory's own entry for a path naming one, so the path
	// has to be the entry's rather than a prefix of it.
	if entryPath != path || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return FileAt{}, fmt.Errorf("%s at %s: %w", path, commit, ErrNotAtCommit)
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return FileAt{}, fmt.Errorf("parse size of %s at %s: %w", path, commit, err)
	}
	file := FileAt{Size: size}
	if size > maxBytes {
		return file, nil
	}
	blob, err := m.run(ctx, "-C", m.repositoryRoot, "cat-file", "blob", fields[2])
	if err != nil {
		return FileAt{}, err
	}
	if blob.Status != execution.ProcessSucceeded {
		return FileAt{}, fmt.Errorf("read %s at %s failed with exit code %d: %s", path, commit, blob.ExitCode, strings.TrimSpace(blob.Stderr))
	}
	content := blob.Stdout
	// The runner ends every line it returns with a newline, the last one included,
	// so a file that did not end in one comes back one byte longer than it is.
	if int64(len(content)) == size+1 && strings.HasSuffix(content, "\n") {
		content = content[:len(content)-1]
	}
	if int64(len(content)) != size {
		return FileAt{}, fmt.Errorf("read %s at %s returned %d bytes of a %d-byte file, so it could not be read whole", path, commit, len(content), size)
	}
	file.Content = []byte(content)
	return file, nil
}

// CommitsChanging lists, newest first, up to maxCommits commits reachable from
// ref that changed path. Read with FileAtCommit, each names a copy of the file
// the branch once held, which is how a document run finds the commit whose copy
// its text was written against.
func (m *Manager) CommitsChanging(ctx context.Context, ref, path string, maxCommits int) ([]string, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, errors.New("a commit is required to list the changes to a file from")
	}
	if maxCommits <= 0 {
		return nil, nil
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "log", "--format=%H", "--max-count="+strconv.Itoa(maxCommits), ref, "--", path)
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the changes to %s from %s failed with exit code %d: %s", path, ref, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return strings.Fields(result.Stdout), nil
}
