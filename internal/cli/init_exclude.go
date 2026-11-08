package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// trackerFiles are the files `bd init` writes into a checkout: its database
// directory, the skill and hook files it installs for the agents that read them,
// the AGENTS.md and CLAUDE.md sections that tell those agents to use it, and the
// project's .gitignore, which it creates or appends to. The list was taken by
// running bd 1.1.2's init in a throwaway repository, and has to be read again
// when bd is upgraded.
//
// Each entry is anchored to the root of the checkout, so a file of the same name
// deeper in the project is not caught by it.
var trackerFiles = []string{
	"/.beads/",
	"/.agents/skills/beads/",
	"/.claude/settings.json",
	"/.codex/config.toml",
	"/.codex/hooks.json",
	"/AGENTS.md",
	"/CLAUDE.md",
	"/.gitignore",
}

// trackerExclusionHeading introduces the lines init adds, so a person reading
// .git/info/exclude later can tell where they came from.
const trackerExclusionHeading = "# The tracker's own files, kept out of this checkout by yoyo init --external"

// trackerExclusion is what became of keeping the tracker's files out of the
// checkout, reported beside the rest of the init.
type trackerExclusion struct {
	Path    string   `json:"path,omitempty"`    // the exclude file
	Added   []string `json:"added"`             // entries this init added
	Present []string `json:"present,omitempty"` // entries already there
	Tracked []string `json:"tracked,omitempty"` // entries skipped: the project tracks them, and an exclude line does not reach a tracked file
	Reason  string   `json:"reason,omitempty"`  // why nothing could be added
}

// excludeTrackerFiles adds the tracker's files to the checkout's own
// .git/info/exclude, which only this clone reads.
//
// An external configuration is there so that nothing of Yoyodyne's reaches the
// project, and a run refuses to start while the primary checkout holds
// untracked files nobody declared, which is what the tracker's files are the
// moment it is initialized. The project's .gitignore is not touched for the same
// reason the configuration is not committed: it is the project's, not this
// machine's.
//
// A file the project already tracks is skipped and reported, because an exclude
// line does not reach a tracked file and saying it was excluded would be wrong.
// Problems are reported in the result and never raised: the configuration is
// written and valid whether or not this worked.
func excludeTrackerFiles(ctx context.Context, runner execution.ProcessRunner, repository string) trackerExclusion {
	result := trackerExclusion{Added: []string{}}
	fail := func(format string, args ...any) trackerExclusion {
		result.Reason = fmt.Sprintf(format, args...)
		return result
	}

	output, err := runGit(ctx, runner, repository, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return fail("%v", err)
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return fail("git reported no path for its exclude file")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repository, path)
	}
	result.Path = path

	root, err := repowrite.NewRoot(repository)
	if err != nil {
		return fail("%v", err)
	}
	// The root is resolved through its symlinks, so the exclude file is too, by
	// way of its directory because the file itself may not exist yet.
	directory, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail("%v", err)
	}
	if err != nil {
		directory = filepath.Dir(path)
	}
	relative, err := filepath.Rel(root.Path(), filepath.Join(directory, filepath.Base(path)))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fail("its exclude file %s is outside the checkout", path)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail("%v", err)
	}
	lines := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		lines[strings.TrimSpace(line)] = true
	}

	for _, entry := range trackerFiles {
		bare := strings.TrimPrefix(entry, "/")
		tracked, err := runGit(ctx, runner, repository, "ls-files", "--", bare)
		if err != nil {
			return fail("%v", err)
		}
		switch {
		case strings.TrimSpace(tracked) != "":
			result.Tracked = append(result.Tracked, bare)
		case lines[entry] || lines[bare]:
			result.Present = append(result.Present, entry)
		default:
			result.Added = append(result.Added, entry)
		}
	}
	if len(result.Added) == 0 {
		return result
	}

	content := string(existing)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += trackerExclusionHeading + "\n" + strings.Join(result.Added, "\n") + "\n"
	if _, err := root.WriteFile(filepath.ToSlash(relative), []byte(content)); err != nil {
		result.Added = []string{}
		return fail("%v", err)
	}
	return result
}

// runGit asks Git one question about the checkout and answers its output, or
// Git's own words when it could not answer.
func runGit(ctx context.Context, runner execution.ProcessRunner, repository string, args ...string) (string, error) {
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    append([]string{"-C", repository}, args...),
		Env:     execution.GitEnvironment(nil),
		Timeout: trackerCommandTimeout,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("git could not be run here: %v", err)
	}
	if result.Status != execution.ProcessSucceeded {
		message := singleLine(firstNonEmpty(result.Stderr, result.Stdout))
		if message == "" {
			message = fmt.Sprintf("git %s failed", args[0])
		}
		return "", errors.New(message)
	}
	return result.Stdout, nil
}

// describeTrackerExclusion says what became of keeping the tracker's files out
// of the checkout, one line each. A result with a reason is the one init reports
// on stderr, and it names the entries so the operator can add them.
func describeTrackerExclusion(exclusion trackerExclusion) []string {
	var lines []string
	switch {
	case exclusion.Reason != "":
		lines = append(lines, fmt.Sprintf("the tracker's files could not be kept out of the checkout -- %s; add these to .git/info/exclude yourself: %s",
			exclusion.Reason, strings.Join(trackerFilesNotTracked(exclusion), ", ")))
	case len(exclusion.Added) > 0:
		lines = append(lines, fmt.Sprintf("added to %s so the tracker's files stay out of the checkout: %s",
			exclusion.Path, strings.Join(exclusion.Added, ", ")))
	default:
		lines = append(lines, fmt.Sprintf("the tracker's files were already kept out of the checkout by %s", exclusion.Path))
	}
	if len(exclusion.Tracked) > 0 {
		lines = append(lines, "not excluded, because the project tracks them and an exclude line does not reach a tracked file: "+
			strings.Join(exclusion.Tracked, ", "))
	}
	return lines
}

// trackerFilesNotTracked is the entries an operator would have to add by hand:
// every one that is not tracked, whether or not this run got as far as sorting it.
func trackerFilesNotTracked(exclusion trackerExclusion) []string {
	var entries []string
	for _, entry := range trackerFiles {
		tracked := false
		for _, name := range exclusion.Tracked {
			tracked = tracked || name == strings.TrimPrefix(entry, "/")
		}
		if !tracked {
			entries = append(entries, entry)
		}
	}
	return entries
}
