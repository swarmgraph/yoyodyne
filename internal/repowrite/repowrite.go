// Package repowrite is how this harness writes a document into the repository it
// is working on, and the one place the confinement that keeps such a write
// inside that repository is decided.
//
// The paths these writes land at come from a project's own configuration — the
// artifact homes, the invariants directory, the `.yoyodyne` directory an
// initialization creates — and a configuration is checked for traversal when it
// loads. That check is lexical, and the filesystem is not. A directory that
// reads as `docs/decisions` in the configuration is whatever the filesystem says
// it is by the time anything writes there, and one symlink along it puts the
// bytes outside the repository without a single `..` appearing anywhere. What a
// run promotes is the repository's own diff, so a write that escaped is a change
// nobody reviews, nobody promotes, and nobody finds afterwards: the document
// simply is not where the harness says it wrote it.
//
// So confinement is decided here, against the filesystem, at the moment the
// bytes are written rather than wherever the path was configured — a
// confinement that holds only when a caller remembered to check is not one.
//
// The readers of those same directories resolve through Resolve, and for a
// defect that costs more rather than less: bytes that escaped a write are
// simply missing afterwards, while a document read from outside the repository
// comes back named by a path the repository appears to hold. An invariant
// planted behind a symlinked `docs` was delivered to every developer's context
// and every reviewer's evidence as a constraint nobody committed. So Resolve is
// where a configured directory is turned into a real one for a read.
//
// A write does not use Resolve's answer, because a path that was checked can
// be replaced before it is used. Every change a Root or a PinnedRoot makes goes
// through the descriptor-level primitive in confine_unix.go, which is the
// repository-confined-writes design implemented: lookups relative to the
// root's open descriptor, the directory written in checked on both sides of
// every change, new files published rather than old ones written in place, and
// a refusal naming one reason from that design's closed list. On a platform or
// filesystem the design does not name, every change is refused.
package repowrite

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The permissions a repository document and the directories above it are given.
// These are files reviewed with the code and committed beside it rather than
// runtime state, so they are readable exactly as the rest of a checkout is, and
// they are fixed here rather than per caller because "what a repository document
// is written as" is one answer rather than each writer's own.
const (
	filePermissions      fs.FileMode = 0o644
	directoryPermissions fs.FileMode = 0o755
)

// A new file is written under a temporary name before it is published under
// its own. The name is hidden and it is not Markdown, so a crash between the
// two leaves something the directories these writes land in already skip
// rather than a half-written document the next load believes in.
const (
	temporaryPrefix = ".yoyodyne-tmp-"
	temporarySuffix = ".tmp"
	// legacyTemporaryPrefix is the name builds before the descriptor-level
	// primitive gave their temporary files, which an interrupted write of
	// theirs can still have left behind.
	legacyTemporaryPrefix = ".yoyo-write-"
)

// IsTemporaryFile identifies the reserved name of an interrupted confined
// write. A caller still has to establish its age and that nobody holds it open
// before removing one.
func IsTemporaryFile(name string) bool {
	if filepath.Base(name) != name || !strings.HasSuffix(name, temporarySuffix) {
		return false
	}
	for _, prefix := range []string{temporaryPrefix, legacyTemporaryPrefix} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix)+len(temporarySuffix) {
			return true
		}
	}
	return false
}

// Root is one repository, its own symlinks already resolved, that every write
// through it lands inside.
type Root struct {
	path string
}

// NewRoot resolves the repository writes are confined to. The root is resolved
// through its own symlinks once, here, because every later answer is a
// comparison against it: a root still carrying a symlink would call the resolved
// form of a path inside it an escape.
func NewRoot(repositoryRoot string) (Root, error) {
	trimmed := strings.TrimSpace(repositoryRoot)
	if trimmed == "" {
		return Root{}, errors.New("repository root is required")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return Root{}, fmt.Errorf("resolve repository root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Root{}, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Root{}, fmt.Errorf("inspect repository root %q: %w", repositoryRoot, err)
	}
	if !info.IsDir() {
		return Root{}, fmt.Errorf("repository root %q is not a directory", repositoryRoot)
	}
	return Root{path: resolved}, nil
}

// Path is the repository every write is held inside, absolute and with its own
// symlinks resolved.
func (r Root) Path() string {
	return r.path
}

// EscapeError is the refusal this package exists for: a repository-relative path
// that resolved somewhere the repository does not contain. It is a type rather
// than a message because a caller that wants to say something better about it —
// which directory the operator configured, which document was being written —
// needs the part that left and where it went.
type EscapeError struct {
	// Path is the repository-relative path that was asked for.
	Path string
	// Component is the prefix of it that leaves, so a refusal names the symlink
	// rather than only the document that tripped over it.
	Component string
	// Resolved is where that prefix actually points.
	Resolved string
	// Root is the repository it had to stay inside.
	Root string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%s resolves outside the repository: %s points at %s, which is not inside %s",
		e.Path, e.Component, e.Resolved, e.Root)
}

// Relative is the repository-relative slash form a confined path is named in. It
// refuses what cannot name a file inside a repository at all — nothing, an
// absolute path, one that climbs out lexically, and the repository root itself —
// before the filesystem is touched, so a plainly impossible path fails the same
// way whether or not anything exists yet.
func Relative(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("a repository path is required")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("%q is absolute and resolves outside the repository", value)
	}
	clean := path.Clean(filepath.ToSlash(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q resolves outside the repository", value)
	}
	return clean, nil
}

// Resolve is where a repository-relative path actually lands: every component
// below the root followed through whatever the filesystem has put there, and an
// EscapeError rather than a path for anything that leaves. It answers a read as
// well as a write, because "where does this configured path go" is one question
// however the bytes are about to move.
//
// The path does not have to exist. What does not exist cannot be a symlink, so
// once a component is missing the rest of the path is taken as written — which
// is what lets a writer create a directory and the document in it in one go.
func (r Root) Resolve(relative string) (string, error) {
	_, resolved, err := r.resolve(relative)
	return resolved, err
}

// ResolveDirectory is Resolve for a directory rather than a document: the same
// component-by-component walk, and the repository root itself accepted as an
// answer. Relative refuses "." because nothing writes a document at the root,
// and a reader configured to read the whole repository is pointed there
// legitimately — so the one path a writer has no use for is the one a reader may
// be given.
func (r Root) ResolveDirectory(relative string) (string, error) {
	trimmed := strings.TrimSpace(relative)
	if trimmed != "" && !filepath.IsAbs(trimmed) && path.Clean(filepath.ToSlash(trimmed)) == "." {
		return r.path, nil
	}
	return r.Resolve(relative)
}

func (r Root) resolve(relative string) (clean, resolved string, err error) {
	clean, err = Relative(relative)
	if err != nil {
		return "", "", err
	}
	elements := strings.Split(clean, "/")
	current := r.path
	for index, element := range elements {
		candidate := filepath.Join(current, element)
		info, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return clean, filepath.Join(append([]string{candidate}, elements[index+1:]...)...), nil
		}
		if err != nil {
			return "", "", fmt.Errorf("inspect %s inside the repository: %w", strings.Join(elements[:index+1], "/"), err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			current = candidate
			continue
		}
		// A symlink is followed rather than refused outright: one that points
		// somewhere else in the same repository still lands the write inside it,
		// and a project that keeps its designs behind one has not thereby put them
		// out of reach. What is refused is where it points, not that it is one.
		linked, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", "", fmt.Errorf("resolve the symlink at %s inside the repository: %w", strings.Join(elements[:index+1], "/"), err)
		}
		if !r.contains(linked) {
			return "", "", &EscapeError{
				Path:      clean,
				Component: strings.Join(elements[:index+1], "/"),
				Resolved:  linked,
				Root:      r.path,
			}
		}
		current = linked
	}
	return clean, current, nil
}

// contains reports a resolved absolute path the repository holds. The root
// itself counts as inside it, because a symlink pointing at the repository root
// has not left the repository.
func (r Root) contains(candidate string) bool {
	relative, err := filepath.Rel(r.path, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
