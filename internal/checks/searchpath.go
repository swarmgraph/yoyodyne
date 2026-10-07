package checks

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// A check is the project's own command, and where it runs elsewhere -- the
// forge's continuous integration, a collaborator's machine -- the provider CLIs
// the harness drives are usually not installed. A test that finds `claude` or
// `codex` on the search path passes here and fails there, which is the most
// expensive place to learn it: after a run, a review, and a publish. So the
// check stage is given a search path with every provider executable left off
// it, and a test that needed one fails before review.
//
// A directory on the search path is rarely the provider's alone: Homebrew puts
// `claude` beside `go` and `git`. Dropping the directory would take the
// toolchain with it, so a directory holding a provider executable is replaced by
// one made for the stage, holding a link to everything the original holds except
// the provider executables. A directory holding none is passed through as it is.
//
// The stage's directories go in the temporary directory under a name nobody
// else is given, written through the confinement primitive, and are removed
// when the stage ends. One left behind by a harness that was killed mid-stage
// is links and nothing else, in a directory the system clears.

// hiddenSearchPath is one stage's search path with the provider executables
// left off it, and what has to be removed once the stage is over.
type hiddenSearchPath struct {
	root      *repowrite.PinnedRoot
	directory string
}

// withoutExecutables returns environment with its PATH rewritten so that none
// of names is found on it, and what to call once the checks are over. names
// may be bare executable names or paths; a path hides its last element, since
// that is what a lookup on the search path would find. A relative or empty
// PATH entry is read against workingDirectory, as the check's shell would read
// it.
//
// Nothing is written unless some directory on the search path actually holds
// one of the names.
func withoutExecutables(environment []string, workingDirectory string, names []string) ([]string, func(), error) {
	hidden := hiddenNames(names)
	index := -1
	for i, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			index = i
		}
	}
	if len(hidden) == 0 || index < 0 {
		return environment, func() {}, nil
	}
	shadow := &hiddenSearchPath{}
	cleanup := func() { shadow.remove() }
	entries := filepath.SplitList(strings.TrimPrefix(environment[index], "PATH="))
	rewritten := make([]string, 0, len(entries))
	for _, entry := range entries {
		directory := entry
		if directory == "" {
			directory = "."
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(workingDirectory, directory)
		}
		if !holdsAny(directory, hidden) {
			rewritten = append(rewritten, entry)
			continue
		}
		replacement, err := shadow.without(directory, hidden, len(rewritten))
		if err != nil {
			cleanup()
			return environment, func() {}, err
		}
		if replacement != "" {
			rewritten = append(rewritten, replacement)
		}
	}
	result := append([]string(nil), environment...)
	result[index] = "PATH=" + strings.Join(rewritten, string(filepath.ListSeparator))
	return result, cleanup, nil
}

func hiddenNames(names []string) map[string]struct{} {
	hidden := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if base := filepath.Base(name); base != "." && base != string(filepath.Separator) {
			hidden[base] = struct{}{}
		}
	}
	return hidden
}

// holdsAny reports whether directory has an entry by any of the names, of any
// kind: a lookup that skips a non-executable one still says it is there in its
// refusal, and a test reading that refusal is reading the installation.
func holdsAny(directory string, names map[string]struct{}) bool {
	for name := range names {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			return true
		}
	}
	return false
}

// without makes the stage's copy of directory, less the hidden names, and
// returns where it is. A directory that cannot be listed is dropped from the
// path rather than passed through, because passing it through is passing the
// provider through with it.
func (s *hiddenSearchPath) without(directory string, hidden map[string]struct{}, position int) (string, error) {
	listing, err := os.ReadDir(directory)
	if err != nil {
		return "", nil
	}
	if err := s.open(); err != nil {
		return "", err
	}
	name := strconv.Itoa(position)
	copied, err := s.root.CreateDirectory(filepath.Join(s.directory, name))
	if err != nil {
		return "", fmt.Errorf("make the check stage's search path: %w", err)
	}
	defer copied.Close()
	for _, entry := range listing {
		if _, skip := hidden[entry.Name()]; skip {
			continue
		}
		// An entry whose name cannot be linked is one tool fewer for the check,
		// which a check that needs it says plainly; it does not reopen the path
		// to the provider.
		_ = copied.Symlink(filepath.Join(directory, entry.Name()), entry.Name())
	}
	return copied.Path(), nil
}

// open makes the stage's directory, once, the first time a search path entry
// needs replacing.
func (s *hiddenSearchPath) open() error {
	if s.root != nil {
		return nil
	}
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return fmt.Errorf("find the temporary directory for the check stage's search path: %w", err)
	}
	root, err := repowrite.OpenPinnedRoot(temporary)
	if err != nil {
		return fmt.Errorf("open the temporary directory for the check stage's search path: %w", err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		root.Close()
		return err
	}
	directory := "yoyodyne-check-path-" + hex.EncodeToString(suffix)
	created, err := root.CreateDirectory(directory)
	if err != nil {
		root.Close()
		return fmt.Errorf("make the check stage's search path: %w", err)
	}
	created.Close()
	s.root, s.directory = root, directory
	return nil
}

func (s *hiddenSearchPath) remove() {
	if s.root == nil {
		return
	}
	// Removal is best effort: what is left is links in the temporary directory.
	_ = s.root.RemoveAll(s.directory)
	_ = s.root.Close()
	s.root = nil
}
