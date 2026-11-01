package execution

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Git's automatic maintenance, fenced out of every process the harness starts.
//
// Maintenance prunes worktree registrations, and it judges one stale by whether
// its administrative files are there -- which is exactly what a `git worktree
// add` has not written yet while it is still filling the entry in. A prune
// reaching that window deletes the registration out from under the add, which
// fails with "could not open .git/worktrees/<id>/locked for writing", and the
// run is lost to nothing but timing. It reproduces on git 2.50.1 within seconds
// of putting a prune beside concurrent adds.
//
// The harness already refuses to ask for maintenance in the Git commands it
// composes itself, by passing its settings as `-c` options. That fences one
// caller and no others: every worktree the harness cuts shares the repository's
// common Git directory, so a Git command an agent runs, or one a project's own
// build tooling runs inside a worktree, hands the same repository to the same
// prune. Those commands are not the harness's to compose.
//
// What the harness does control is the environment it launches them in, and Git
// reads configuration from the environment as well as from files: GIT_CONFIG_COUNT
// with a GIT_CONFIG_KEY_n and GIT_CONFIG_VALUE_n for each setting applies to the
// command that reads it and to everything it goes on to spawn. So the fence is
// injected there, once, and every descendant of a harness-launched process
// carries it without knowing it did.
//
// It is deliberately not written into the managed repository's config. That
// repository belongs to whoever is developing in it, its maintenance is theirs
// to configure, and object GC turned off in a repository that keeps growing is
// a cost the harness would be imposing on someone else's machine for good. The
// environment fence lasts exactly as long as the process it was given to.
//
// What it does not reach is a Git command nobody here launched -- a person's own
// `git gc` in the checkout, or a tool they started themselves. That residual is
// the operator's, and `docs/operations.md` says so rather than leaving it to be
// rediscovered by a lost run.

// The environment variables Git reads configuration from. The count names how
// many of the numbered pairs are real; a pair past it is ignored, which is why
// the count is written last and always rewritten.
const (
	gitConfigCountVariable = "GIT_CONFIG_COUNT"
	gitConfigKeyVariable   = "GIT_CONFIG_KEY_"
	gitConfigValueVariable = "GIT_CONFIG_VALUE_"
)

// gitSetting is one configuration setting as Git reads it out of an
// environment: a name and the value applied for it.
type gitSetting struct {
	key   string
	value string
}

// gitMaintenanceSettings is what the fence sets. Each is needed because any one
// alone still leaves a path to the same prune: maintenance.auto governs whether
// the detached run starts at all, gc.auto governs the gc task inside it that
// prunes worktrees, and maintenance.worktree-prune.auto governs the separate
// worktree-prune task, which prunes them without gc and which gc.auto does not
// reach. A Git too old to know the third setting ignores it.
var gitMaintenanceSettings = []gitSetting{
	{key: "gc.auto", value: "0"},
	{key: "maintenance.auto", value: "false"},
	{key: "maintenance.worktree-prune.auto", value: "0"},
}

// WithGitMaintenanceFence returns environment carrying the settings that stop
// Git handing a repository to its automatic maintenance. A nil environment
// starts from this process's own, which is what a command that would otherwise
// have inherited it needs.
//
// Configuration the environment already carried is kept, and the fence is
// written after it: Git applies these in order, so the fence is the last word on
// its own settings and says nothing about any other. The numbered pairs are
// rewritten from scratch rather than appended to, which is what makes applying
// this twice the same as applying it once -- a harness process launched by a
// harness process is an ordinary arrangement, and a fence that grew a pair per
// generation would be one nobody could read.
//
// A setting the environment already carried that the fence also sets is dropped
// rather than left below it. Which of two entries of one name a process sees is
// the operating system's to decide, and a fence that depends on that is not a
// fence.
func WithGitMaintenanceFence(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	settings := append(carriedGitConfig(environment), gitMaintenanceSettings...)
	fence := make([]string, 0, 1+2*len(settings))
	for index, setting := range settings {
		fence = append(fence,
			fmt.Sprintf("%s%d=%s", gitConfigKeyVariable, index, setting.key),
			fmt.Sprintf("%s%d=%s", gitConfigValueVariable, index, setting.value),
		)
	}
	fence = append(fence, gitConfigCountVariable+"="+strconv.Itoa(len(settings)))

	kept := make([]string, 0, len(environment)+len(fence))
	for _, entry := range environment {
		if name, _, named := strings.Cut(entry, "="); named && configuresGit(name) {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, fence...)
}

// carriedGitConfig is the environment-configured settings Git would have read
// from this environment, in the order it would have applied them, less the ones
// the fence is about to set for itself.
//
// Only the pairs the count admits are carried: a numbered pair above it is one
// Git ignores, and carrying it would turn something already inert into
// configuration. A count that is not a number, or a pair the count admits and
// the environment does not complete, is an environment Git refuses every command
// over rather than one it reads differently, so the list is rebuilt from what is
// there instead of preserving a refusal.
func carriedGitConfig(environment []string) []gitSetting {
	count := 0
	keys := make(map[int]string)
	values := make(map[int]string)
	for _, entry := range environment {
		name, value, named := strings.Cut(entry, "=")
		if !named {
			continue
		}
		// The last entry of a name is the one a process reads, so a later entry
		// replaces an earlier one rather than being ignored.
		switch {
		case name == gitConfigCountVariable:
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 0 {
				parsed = 0
			}
			count = parsed
		case strings.HasPrefix(name, gitConfigKeyVariable):
			if index, ok := configIndex(name, gitConfigKeyVariable); ok {
				keys[index] = value
			}
		case strings.HasPrefix(name, gitConfigValueVariable):
			if index, ok := configIndex(name, gitConfigValueVariable); ok {
				values[index] = value
			}
		}
	}

	carried := make([]gitSetting, 0, count)
	for index := range count {
		key, named := keys[index]
		value, valued := values[index]
		if !named || !valued || fencedGitSetting(key) {
			continue
		}
		carried = append(carried, gitSetting{key: key, value: value})
	}
	return carried
}

// configuresGit reports an environment variable this rewrites: the count and
// every numbered pair, whether or not the count admitted it.
func configuresGit(name string) bool {
	if name == gitConfigCountVariable {
		return true
	}
	if _, ok := configIndex(name, gitConfigKeyVariable); ok {
		return true
	}
	_, ok := configIndex(name, gitConfigValueVariable)
	return ok
}

// configIndex reads the number off one of Git's numbered configuration
// variables. A suffix that is not a number names no pair, so it is left alone
// rather than treated as one.
func configIndex(name, prefix string) (int, bool) {
	suffix, named := strings.CutPrefix(name, prefix)
	if !named {
		return 0, false
	}
	index, err := strconv.Atoi(suffix)
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

// fencedGitSetting reports a setting the fence states itself. Git names are
// case-insensitive, so a carried `GC.Auto` is the same setting as the fence's
// and must not survive beside it.
func fencedGitSetting(key string) bool {
	for _, setting := range gitMaintenanceSettings {
		if strings.EqualFold(key, setting.key) {
			return true
		}
	}
	return false
}
