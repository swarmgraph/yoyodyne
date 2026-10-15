package repositoryread

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Reader performs repository reads on a role's behalf. It is the whole of the
// capability: the role never runs anything, and nothing here consults the role
// about what to run — every command is Git, every argument but the path is the
// harness's, and the path is resolved inside one commit's tree and nowhere else.
type Reader struct {
	// Process runs Git. A reader without one performs nothing and says so,
	// rather than reporting that the repository held nothing.
	Process execution.ProcessRunner
	Clock   execution.Clock
	// Git is the executable, "git" when empty.
	Git string
	// Directory is the repository whose HEAD is read. It is the primary checkout
	// a conversation is opened in, and the working tree there is never read: the
	// commit HEAD names is resolved once per block, and every path is read out of
	// that commit's tree.
	Directory string
	// Timeout bounds each Git command. Zero takes DefaultTimeout.
	Timeout time.Duration
	// RedactValues are the values that must not reach a prompt. Content is
	// redacted before it is returned, exactly as every other provider-facing path
	// redacts what it sends.
	RedactValues []string
}

const defaultGit = "git"

// symlinkMode is the mode Git records a symbolic link under. A link is a blob
// holding its target's path; reading it would hand the role that string, which
// answers nothing, and following it is what a committed tree makes impossible
// and this refuses anyway.
const symlinkMode = "120000"

// Read resolves each request against the tree of HEAD as it stands now and
// returns what came back, one result per request and in the order asked. It
// never returns fewer results than it was given requests: a path that could not
// be read comes back as a result saying why, because a role that gets silence
// for an answer concludes there was nothing there.
//
// The error return is for the capability failing rather than for a path failing.
// A path that names nothing, a directory asked for as a file, and a bound spent
// are each a result with a problem on it; a reader with no process to run Git
// with, or a repository whose HEAD cannot be resolved, is an error, because
// nothing was read and nothing could be.
func (r Reader) Read(ctx context.Context, requests []Request) ([]Result, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	if r.Process == nil {
		return nil, errors.New("no process runner is configured for repository reads, so nothing was read")
	}
	if len(requests) > MaxRequestsPerReply {
		return nil, fmt.Errorf("%d repository paths were named in one reply and the harness permits %d; none were read", len(requests), MaxRequestsPerReply)
	}
	commit, err := r.head(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve the commit to read at: %w", err)
	}
	redactor := execution.NewRedactor(r.RedactValues...)
	results := make([]Result, 0, len(requests))
	// remaining is what this reply may still return of content across all its
	// reads. It is spent in the order the paths were named, so a role that named
	// a long file first has spent the budget on it and is told so on the next.
	remaining := MaxBytesPerReply
	for _, request := range requests {
		result := r.resolve(ctx, commit, request, redactor, &remaining)
		results = append(results, result)
	}
	return results, nil
}

// resolve performs one request. Everything it can go wrong with is recorded on
// the result rather than raised, so one path failing never costs the answers
// beside it.
func (r Reader) resolve(ctx context.Context, commit string, request Request, redactor execution.Redactor, remaining *int) Result {
	action := strings.TrimSpace(request.Action)
	result := Result{
		Action: action,
		Why:    strings.TrimSpace(request.Why),
		Commit: commit,
		ReadAt: r.now(),
	}
	if action == ActionSearch {
		result.Pattern = redactor.Redact(request.Pattern)
		result.Regexp = request.Regexp
	}
	cleaned, err := CleanPath(request.Path, action != ActionRead)
	result.Path = cleaned
	if err != nil {
		result.Path = strings.TrimSpace(request.Path)
		result.Problem = err.Error()
		return result
	}
	switch action {
	case ActionRead:
		r.readFile(ctx, &result, redactor, remaining)
	case ActionList:
		r.listDirectory(ctx, &result)
	case ActionSearch:
		r.search(ctx, &result, request, redactor, remaining)
	default:
		result.Problem = fmt.Sprintf("action %q is not one the harness performs", action)
	}
	// The moment is taken after Git answered rather than before it was asked:
	// what the record says is when the content was obtained.
	result.ReadAt = r.now()
	return result
}

// readFile reads one blob out of the commit's tree.
func (r Reader) readFile(ctx context.Context, result *Result, redactor execution.Redactor, remaining *int) {
	entry, problem := r.entry(ctx, result.Commit, result.Path)
	if problem != "" {
		result.Problem = problem
		return
	}
	switch {
	case entry.kind == "tree":
		result.Problem = fmt.Sprintf("%s is a directory at %s; ask to list it rather than read it", result.Path, shortCommit(result.Commit))
		return
	case entry.mode == symlinkMode:
		result.Problem = fmt.Sprintf("%s is a symbolic link at %s, which the harness does not follow; name the path it points at", result.Path, shortCommit(result.Commit))
		return
	case entry.kind != "blob":
		result.Problem = fmt.Sprintf("%s is a %s at %s, which is not a file the harness can read", result.Path, entry.kind, shortCommit(result.Commit))
		return
	}
	size, err := r.blobSize(ctx, entry.object)
	if err != nil {
		result.Problem = fmt.Sprintf("the size of %s at %s could not be read: %s", result.Path, shortCommit(result.Commit), singleLine(err.Error()))
		return
	}
	result.Size = size
	if *remaining <= 0 {
		result.Problem = fmt.Sprintf("this reply's reads have already returned %s, which is all one reply may return; nothing of %s was read", maxBytesPerReplyText, result.Path)
		return
	}
	// What Git is asked to keep is a little past what will be returned, so the
	// retained copy is a whole prefix of the file rather than one the runner cut
	// at a different place, and the bound below is the only cut a reader sees.
	output, err := r.git(ctx, MaxContentBytes+(64<<10), "cat-file", "blob", entry.object)
	if err != nil {
		result.Problem = fmt.Sprintf("%s at %s could not be read: %s", result.Path, shortCommit(result.Commit), singleLine(err.Error()))
		return
	}
	content := output.Stdout
	if output.OutputTruncation != "" {
		content = strings.TrimSuffix(content, output.OutputTruncation+"\n")
	}
	// The runner hands output back a line at a time and ends every line with a
	// newline, so a file with none at its end gains one here; it is taken off
	// again so the bound below is measured against the file rather than the copy.
	content = strings.TrimRight(content, "\n")
	if strings.ContainsRune(content, 0) || !utf8.ValidString(content) {
		result.Problem = fmt.Sprintf("%s at %s is not text, and the harness reads text only", result.Path, shortCommit(result.Commit))
		return
	}
	bounded, truncatedBy := bound(content, size, *remaining)
	result.Content = redactor.Redact(bounded)
	result.Truncated = truncatedBy != ""
	result.TruncatedBy = truncatedBy
	*remaining -= len(bounded)
}

// bound cuts content to what one read and the rest of the reply may carry, on
// a rune boundary so a cut file is still text, and says which bound cut it.
// Whether a file was cut is decided from its whole size rather than from the
// copy in hand: what the runner retained is at least the bound, so a file
// larger than the bound is cut whatever the copy happens to hold.
func bound(content string, size, remaining int) (string, string) {
	limit := MaxContentBytes
	truncatedBy := fmt.Sprintf("by the %s one read may return", maxContentBytesText)
	if limit > remaining {
		limit = remaining
		truncatedBy = fmt.Sprintf("by the %s one reply's reads may return together", maxBytesPerReplyText)
	}
	if size <= limit && len(content) <= limit {
		return content, ""
	}
	if len(content) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
		content = content[:cut]
	}
	return content, truncatedBy
}

// listDirectory lists the names one tree holds, one level deep.
func (r Reader) listDirectory(ctx context.Context, result *Result) {
	args := []string{"ls-tree", result.Commit}
	if result.Path != "" {
		entry, problem := r.entry(ctx, result.Commit, result.Path)
		if problem != "" {
			result.Problem = problem
			return
		}
		if entry.kind != "tree" {
			result.Problem = fmt.Sprintf("%s is a file at %s, not a directory; ask to read it rather than list it", result.Path, shortCommit(result.Commit))
			return
		}
		// A trailing slash is how ls-tree is asked for what is inside a
		// directory rather than for the directory's own entry.
		args = append(args, "--", result.Path+"/")
	}
	output, err := r.git(ctx, 0, args...)
	if err != nil {
		result.Problem = fmt.Sprintf("%s at %s could not be listed: %s", displayPath(result.Path), shortCommit(result.Commit), singleLine(err.Error()))
		return
	}
	entries, err := parseEntries(output.Stdout)
	if err != nil {
		result.Problem = fmt.Sprintf("the listing of %s at %s could not be read: %s", displayPath(result.Path), shortCommit(result.Commit), singleLine(err.Error()))
		return
	}
	names := make([]string, 0, len(entries))
	prefix := ""
	if result.Path != "" {
		prefix = result.Path + "/"
	}
	for _, entry := range entries {
		name := strings.TrimPrefix(entry.path, prefix)
		if entry.kind == "tree" {
			name += "/"
		}
		names = append(names, name)
	}
	result.Size = len(names)
	if len(names) > MaxEntries {
		names = names[:MaxEntries]
		result.Truncated = true
		result.TruncatedBy = fmt.Sprintf("by the %d names one list may return", MaxEntries)
	}
	// An empty directory cannot exist in a Git tree, so an empty listing is a
	// tree the harness could not read rather than one with nothing in it; it is
	// still reported as what it is rather than as a problem, because the path
	// resolved to a tree and the answer is that Git listed nothing under it.
	result.Entries = names
}

// treeEntry is one line of ls-tree: what kind of object a path names, under
// which mode, and the object itself.
type treeEntry struct {
	mode   string
	kind   string
	object string
	path   string
}

// entry resolves one path in the commit's tree, and says why it could not.
func (r Reader) entry(ctx context.Context, commit, repositoryPath string) (treeEntry, string) {
	output, err := r.git(ctx, 0, "ls-tree", commit, "--", repositoryPath)
	if err != nil {
		return treeEntry{}, fmt.Sprintf("%s could not be resolved at %s: %s", repositoryPath, shortCommit(commit), singleLine(err.Error()))
	}
	entries, err := parseEntries(output.Stdout)
	if err != nil {
		return treeEntry{}, fmt.Sprintf("%s could not be resolved at %s: %s", repositoryPath, shortCommit(commit), singleLine(err.Error()))
	}
	for _, entry := range entries {
		if entry.path == repositoryPath {
			return entry, ""
		}
	}
	return treeEntry{}, fmt.Sprintf("there is no %s in the tree at %s; it may exist only in a working tree, on another branch, or under another name", repositoryPath, shortCommit(commit))
}

// parseEntries reads ls-tree's default output: mode, type, object, a tab, and
// the path, which Git quotes when it holds anything unusual.
func parseEntries(listing string) ([]treeEntry, error) {
	var entries []treeEntry
	for _, line := range strings.Split(listing, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		meta, name, found := strings.Cut(line, "\t")
		if !found {
			return nil, fmt.Errorf("unexpected ls-tree line %q", singleLine(line))
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree line %q", singleLine(line))
		}
		if strings.HasPrefix(name, `"`) {
			unquoted, err := strconv.Unquote(name)
			if err != nil {
				return nil, fmt.Errorf("unreadable path in ls-tree line %q", singleLine(line))
			}
			name = unquoted
		}
		entries = append(entries, treeEntry{mode: fields[0], kind: fields[1], object: fields[2], path: name})
	}
	return entries, nil
}

// blobSize is the whole size of a blob, which is what a cut read names.
func (r Reader) blobSize(ctx context.Context, object string) (int, error) {
	output, err := r.git(ctx, 0, "cat-file", "-s", object)
	if err != nil {
		return 0, err
	}
	size, err := strconv.Atoi(strings.TrimSpace(output.Stdout))
	if err != nil {
		return 0, fmt.Errorf("git cat-file -s did not answer with a size: %s", singleLine(output.Stdout))
	}
	return size, nil
}

// head is the commit every path in one block is read at. It is resolved once
// per block rather than once per path, so a block's reads are all of one tree
// even where a commit lands while they are being made.
func (r Reader) head(ctx context.Context) (string, error) {
	output, err := r.git(ctx, 0, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(output.Stdout)
	if commit == "" {
		return "", errors.New("git rev-parse named no commit")
	}
	return commit, nil
}

// git runs one Git command and returns its output, treating anything but a
// clean exit as the error it is.
func (r Reader) git(ctx context.Context, maxOutput int, args ...string) (execution.ProcessResult, error) {
	return r.gitWithin(ctx, r.timeout(), maxOutput, args...)
}

// gitWithin is git under a bound of the caller's choosing, which a search
// needs because its time limit is the search's rather than one command's.
func (r Reader) gitWithin(ctx context.Context, timeout time.Duration, maxOutput int, args ...string) (execution.ProcessResult, error) {
	result, err := r.Process.Run(ctx, execution.Command{
		Name:           r.gitName(),
		Args:           args,
		Dir:            r.Directory,
		Env:            execution.GitEnvironment(nil),
		Timeout:        timeout,
		MaxOutputBytes: maxOutput,
	}, nil)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("run git %s: %w", args[0], err)
	}
	switch result.Status {
	case execution.ProcessSucceeded:
		return result, nil
	case execution.ProcessTimedOut, execution.ProcessStalled:
		return result, fmt.Errorf("git %s did not answer within %s", args[0], timeout)
	case execution.ProcessCancelled:
		return result, fmt.Errorf("git %s was stopped before it answered", args[0])
	default:
		detail := singleLine(firstNonEmpty(result.Stderr, result.Stdout))
		if detail == "" {
			return result, fmt.Errorf("git %s failed with exit code %d", args[0], result.ExitCode)
		}
		return result, fmt.Errorf("git %s failed: %s", args[0], detail)
	}
}

func (r Reader) gitName() string {
	if strings.TrimSpace(r.Git) == "" {
		return defaultGit
	}
	return r.Git
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r Reader) timeout() time.Duration {
	if r.Timeout <= 0 {
		return DefaultTimeout
	}
	return r.Timeout
}

func (r Reader) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return r.Clock.Now().UTC()
}
