package execution

import (
	"os"
	"path/filepath"
	"strings"
)

// A run's sandbox grants writes to its worktree and temporary directory. The
// provider must also admit the declared cache and scratch paths under Git;
// Codex gets explicit, confined roots rather than the whole Git directory. The Go
// toolchain's build cache defaults to none of those -- it is under the user's
// home -- so the first Go command a run makes dies at setup with "operation not
// permitted" before it compiles anything. What that reads like is a broken
// toolchain rather than a directory nobody granted, which is why five reports
// across four work items each rediscovered it and each worked around it by hand.
//
// So the harness points the cache at somewhere the run may actually write, for
// every run it makes: the developer's own execution probe and the check runner
// the harness applies to the change both get it, and neither has to know it
// happened. An environment the harness did not make is the Makefile's to warn --
// `make check` refuses with the redirect named rather than with the setup
// failure alone.
//
// Only GOCACHE is redirected. GOTMPDIR defaults to the temporary directory the
// sandbox already grants, and the Go command refuses a GOTMPDIR that does not
// exist, so naming one would add a way for a run to fail rather than remove one.
const goBuildCacheVariable = "GOCACHE"

// harnessGitSubdirectory is where the harness keeps what it puts in a
// repository's Git directory: the build cache below, and the per-run scratch
// directories in scratch.go beside it. It is one name so that a reader looking
// at a repository can tell what the harness left there from what Git did.
const harnessGitSubdirectory = "yoyodyne"

// WithGoBuildCache returns environment with the Go build cache pointed inside
// the repository that workingDirectory belongs to. A nil environment starts
// from this process's own, which is what a command that would otherwise have
// inherited it needs; a working directory in no repository is left exactly as it
// was, because there is nowhere known to be writable to point the cache at.
//
// An inherited GOCACHE is replaced rather than honored. That value is the one a
// run's sandbox refuses -- an operator who set it set it for this machine, not
// for a sandbox -- so carrying it through would reintroduce the failure on
// exactly the machines most likely to have one.
//
// Nothing here writes: the Go command creates its own cache directory, so the
// harness names a path and creates nothing.
func WithGoBuildCache(environment []string, workingDirectory string) []string {
	cache, found := goBuildCache(workingDirectory)
	if !found {
		return environment
	}
	if environment == nil {
		environment = os.Environ()
	}
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, goBuildCacheVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, goBuildCacheVariable+"="+cache)
}

// goBuildCache is where a working directory's build cache belongs, and whether
// there is anywhere at all: a directory that is not in a repository has no Git
// directory to hold one.
//
// It goes in the repository's own Git directory rather than the worktree's
// administrative one, so every run of every worktree compiles against one cache
// instead of paying for the same packages again; the Go command trims what it
// has not used, so nothing there grows without bound. Outside the working tree
// is the other half of the choice: a cache inside it would be untracked content
// in somebody's repository, which is a dirty tree to every gate that reads one
// and unrecognized content to the composition audit.
//
// Concurrent runs writing one cache was suspected of crossing their verdicts and
// is not doing so. A developer's probe on 2026-09-01 reported a compile error at
// a line of a package that run had never touched; the cache was the only thing
// that run shared with the concurrent run whose in-progress edit the diagnostic
// described. yoyodyne-ifd.238 attributed it elsewhere -- the two runs wrote one
// scratch log in a temporary directory the machine shares, and the reading run's
// own checks had passed -- and found no crossed verdict in deliberate contention
// against one cache. The entries are keyed by the content compiled, so two
// worktrees at different content are two sets of entries.
// `docs/diagnoses/yoyodyne-ifd-238-probe-verdict-crosstalk.md` is the evidence,
// and it is what to reopen this against rather than the suspicion alone.
func goBuildCache(workingDirectory string) (string, bool) {
	if strings.TrimSpace(workingDirectory) == "" {
		return "", false
	}
	gitDirectory, found := repositoryGitDirectory(workingDirectory)
	if !found {
		return "", false
	}
	return filepath.Join(gitDirectory, harnessGitSubdirectory, "go-build"), true
}

// repositoryGitDirectory resolves the Git directory of the repository a working
// directory belongs to, which is the one every worktree of that repository
// shares.
func repositoryGitDirectory(workingDirectory string) (string, bool) {
	directory, found := WorktreeGitDirectory(workingDirectory)
	if !found {
		return "", false
	}
	common, err := os.ReadFile(filepath.Join(directory, "commondir"))
	if err != nil {
		// A worktree whose administrative directory records no common directory
		// is one this cannot share a cache across, which is slower and not wrong.
		return directory, true
	}
	if resolved := resolveGitPath(strings.TrimSpace(string(common)), directory); resolved != "" {
		return resolved, true
	}
	return directory, true
}

// WorktreeGitDirectory resolves the Git directory belonging to one working
// directory, and whether it is in a repository at all. It reads the files Git
// itself writes rather than running Git: this is asked on the way to building
// one command's environment, and a subprocess per invocation to learn a path
// that is written down is a cost with nothing to show for it.
//
// A checkout carries `.git` as a directory, which is the repository's own. A
// worktree carries it as a file naming that worktree's administrative directory,
// which belongs to that worktree alone and which Git removes along with it —
// that is the difference from repositoryGitDirectory above, which goes on to
// follow `commondir` to the directory every worktree shares. Which of the two a
// caller wants is decided by whether what it puts there is the repository's or
// one worktree's.
func WorktreeGitDirectory(workingDirectory string) (string, bool) {
	if strings.TrimSpace(workingDirectory) == "" {
		return "", false
	}
	path := filepath.Join(workingDirectory, ".git")
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return path, true
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	administrative, named := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !named {
		return "", false
	}
	directory := resolveGitPath(strings.TrimSpace(administrative), workingDirectory)
	if directory == "" {
		return "", false
	}
	return directory, true
}

// resolveGitPath reads one of the paths Git writes into these files, which is
// absolute or is relative to the file that named it.
func resolveGitPath(path, against string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(against, path)
	}
	return filepath.Clean(path)
}
