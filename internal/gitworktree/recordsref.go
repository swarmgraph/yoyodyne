package gitworktree

// Records as a Git reference on the shared remote, for team mode
// (docs/designs/working-alongside-other-contributors.md, section 7).
//
// refs/yoyodyne/records on the remote points at a commit whose tree holds one
// file per record. A record is never rewritten or removed, so every new commit
// is the last fetched one plus new files, built with Git's own object commands
// in a bare repository of the machine's own: no working tree is checked out,
// and nothing is written in the user's repository, its object store or its
// references. A push names the fetched commit in its lease, so two factories
// adding records at once cannot both win; the loser fetches, builds again on
// the winner's commit and pushes again.
//
// This file is only the transport. What a record says, and which ones to share,
// is not its business.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// RecordsRef is where the shared records live on the remote.
const RecordsRef = "refs/yoyodyne/records"

// fetchedRecordsRef is where the records fetched from the remote are kept in the
// records repository, apart from RecordsRef so a local reference is never
// mistaken for the one the remote holds.
const fetchedRecordsRef = "refs/yoyodyne/fetched-records"

// recordsBuildingRefPrefix is where the commit being built is held for the
// moment between writing it and reading it back.
const recordsBuildingRefPrefix = "refs/yoyodyne/building/"

// ErrRecordsLeaseRefused is a records push the remote refused because the
// reference was not at the commit the lease named: another factory pushed
// records first. It is an answer about who got there first, never a failure to
// reach the remote.
var ErrRecordsLeaseRefused = errors.New("the records reference on the remote was not at the commit the harness expected")

// ErrRecordPathTaken is a record written at a path the commit it is built on
// already holds. A record is never rewritten.
var ErrRecordPathTaken = errors.New("the records commit already holds that path")

// RecordsRepository is the bare repository one product's shared records are
// fetched into, built in and pushed from.
type RecordsRepository struct {
	manager *Manager
	dir     string
}

// RecordsRepository opens the bare repository kept in dir, creating it where it
// does not exist. dir is in the machine's state directory, never in the user's
// repository, and must be absolute.
func (m *Manager) RecordsRepository(ctx context.Context, dir string) (*RecordsRepository, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("records repository %q must be an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	// Initialising an existing repository leaves it as it was, so this is also the
	// check that dir is one. Git makes any directory leading to it.
	result, err := m.run(ctx, "init", "--bare", "--quiet", dir)
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("create the records repository %s failed with exit code %d: %s", dir, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return &RecordsRepository{manager: m, dir: dir}, nil
}

// Dir is the bare repository's directory.
func (r *RecordsRepository) Dir() string { return r.dir }

// gitDir is the Git option every command here starts with.
func (r *RecordsRepository) gitDir() []string { return []string{"--git-dir=" + r.dir} }

// remoteURL is what to fetch from (push false) or push to (push true): the
// URL the user's repository has configured for the manager's remote. A remote
// that is not a configured name is already a URL or a path and is used as it
// is; a relative path is taken as relative to the repository, as Git would.
func (r *RecordsRepository) remoteURL(ctx context.Context, push bool) (string, error) {
	m := r.manager
	args := []string{"-C", m.repositoryRoot, "remote", "get-url"}
	if push {
		args = append(args, "--push")
	}
	result, err := m.run(ctx, append(args, m.remote)...)
	if err != nil {
		return "", err
	}
	url := m.remote
	if result.Status == execution.ProcessSucceeded {
		url = strings.TrimSpace(result.Stdout)
	}
	if url == "" {
		return "", fmt.Errorf("remote %q has no URL", m.remote)
	}
	if !filepath.IsAbs(url) && !strings.Contains(url, ":") {
		url = filepath.Join(m.repositoryRoot, url)
	}
	return url, nil
}

// Fetch copies the remote's records reference into the repository and returns
// the commit it points at, or "" where the remote holds none, in which case the
// local copy is dropped too. A remote that cannot be reached returns the
// failure.
func (r *RecordsRepository) Fetch(ctx context.Context) (string, error) {
	m := r.manager
	url, err := r.remoteURL(ctx, false)
	if err != nil {
		return "", err
	}
	// The reference is read first because fetching one the remote does not hold
	// fails like a remote that cannot be reached does.
	remote, err := m.remoteRefAt(ctx, r.gitDir(), url, RecordsRef)
	if err != nil {
		return "", err
	}
	if remote == "" {
		dropped, err := m.run(ctx, append(r.gitDir(), "update-ref", "-d", fetchedRecordsRef)...)
		if err != nil {
			return "", err
		}
		if dropped.Status != execution.ProcessSucceeded {
			return "", fmt.Errorf("drop the fetched records reference failed with exit code %d: %s", dropped.ExitCode, strings.TrimSpace(dropped.Stderr))
		}
		return "", nil
	}
	fetched, err := m.runRemote(ctx, append(r.gitDir(),
		"-c", "core.hooksPath="+os.DevNull,
		"fetch", "--no-tags", "--no-write-fetch-head", url,
		"+"+RecordsRef+":"+fetchedRecordsRef)...)
	if err != nil {
		return "", err
	}
	if fetched.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("fetch the records from %s failed with exit code %d: %s", url, fetched.ExitCode, strings.TrimSpace(fetched.Stderr))
	}
	// The reference may have moved between reading it and fetching it, so the
	// commit returned is the one that was fetched.
	return r.revParse(ctx, fetchedRecordsRef+"^{commit}")
}

// revParse resolves one name to a commit in the records repository.
func (r *RecordsRepository) revParse(ctx context.Context, name string) (string, error) {
	resolved, err := r.manager.run(ctx, append(r.gitDir(), "rev-parse", "--verify", "--quiet", name)...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(resolved.Stdout)
	if resolved.Status != execution.ProcessSucceeded || !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("resolve %s in the records repository failed with exit code %d: %s", name, resolved.ExitCode, strings.TrimSpace(resolved.Stderr))
	}
	return commit, nil
}

// Paths lists every file path in commit's tree. No commit is no paths.
func (r *RecordsRepository) Paths(ctx context.Context, commit string) (map[string]bool, error) {
	paths := map[string]bool{}
	if commit == "" {
		return paths, nil
	}
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("records commit %q is invalid", commit)
	}
	listed, err := r.manager.run(ctx, append(r.gitDir(), "-c", "core.quotePath=false", "ls-tree", "-r", "--name-only", commit)...)
	if err != nil {
		return nil, err
	}
	if listed.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the records in %s failed with exit code %d: %s", commit, listed.ExitCode, strings.TrimSpace(listed.Stderr))
	}
	for _, path := range strings.Split(listed.Stdout, "\n") {
		if path != "" {
			paths[path] = true
		}
	}
	return paths, nil
}

// Read returns the contents of the named paths in commit. A path the commit does
// not hold is an error.
func (r *RecordsRepository) Read(ctx context.Context, commit string, paths []string) (map[string][]byte, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("records commit %q is invalid", commit)
	}
	contents := map[string][]byte{}
	if len(paths) == 0 {
		return contents, nil
	}
	var request strings.Builder
	for _, path := range paths {
		if err := validateRecordPath(path); err != nil {
			return nil, err
		}
		request.WriteString(commit + ":" + path + "\n")
	}
	var out bytes.Buffer
	result, err := r.runWithInput(ctx, nil, strings.NewReader(request.String()), &out, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("read the records in %s failed with exit code %d: %s", commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	// Each answer is "<id> <type> <size>\n<content>\n", or "<name> missing\n".
	reader := bufio.NewReader(&out)
	for _, path := range paths {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read the record %s: %w", path, err)
		}
		fields := strings.Fields(header)
		if len(fields) == 2 && fields[1] == "missing" {
			return nil, fmt.Errorf("record %s is not in %s", path, commit)
		}
		if len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("record %s is not a file in %s: %q", path, commit, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 {
			return nil, fmt.Errorf("record %s has an unreadable size in %q", path, strings.TrimSpace(header))
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("read the record %s: %w", path, err)
		}
		if _, err := reader.Discard(1); err != nil {
			return nil, fmt.Errorf("read the record %s: %w", path, err)
		}
		contents[path] = content
	}
	return contents, nil
}

// runWithInput runs one Git command in the records repository with the input
// given and its standard output copied exactly, which the runner's line-oriented
// output is not: a record's bytes are never altered on the way. A nil
// environment is the allowlisted one, as in runBounded.
func (r *RecordsRepository) runWithInput(ctx context.Context, environment []string, stdin io.Reader, stdout io.Writer, args ...string) (execution.ProcessResult, error) {
	m := r.manager
	if environment == nil {
		environment = execution.GitEnvironment(nil)
	}
	result, err := m.runner.Run(ctx, execution.Command{
		Name:      m.gitBinary,
		Args:      append(append(append([]string(nil), maintenanceOptions...), r.gitDir()...), args...),
		Env:       environment,
		Stdin:     stdin,
		RawStdout: stdout,
		Timeout:   m.localTimeout(),
	}, nil)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("run Git command: %w", err)
	}
	return result, nil
}

// Build writes a commit whose tree is parent's tree plus files (path to
// content), with parent as its only parent, and returns it. No parent makes a
// root commit. A path the parent already holds is refused with
// ErrRecordPathTaken, because a record is never rewritten. The commit is
// referenced by nothing once this returns, until Push puts it on the remote.
func (r *RecordsRepository) Build(ctx context.Context, parent string, files map[string][]byte, now time.Time) (string, error) {
	if len(files) == 0 {
		return "", errors.New("a records commit needs at least one file")
	}
	if parent != "" && !commitPattern.MatchString(parent) {
		return "", fmt.Errorf("records parent commit %q is invalid", parent)
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		if err := validateRecordPath(path); err != nil {
			return "", err
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	existing, err := r.Paths(ctx, parent)
	if err != nil {
		return "", err
	}
	if err := refuseTakenPaths(paths, existing); err != nil {
		return "", err
	}

	var identifier [8]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return "", fmt.Errorf("name the records commit being built: %w", err)
	}
	building := recordsBuildingRefPrefix + hex.EncodeToString(identifier[:])
	if err := validateRef(building); err != nil {
		return "", err
	}
	message := fmt.Sprintf("yoyodyne records: %d record(s)", len(paths))
	var stream bytes.Buffer
	fmt.Fprintf(&stream, "commit %s\n", building)
	fmt.Fprintf(&stream, "committer %s <%s> %d +0000\n", harnessCommitAuthorName, harnessCommitAuthorEmail, now.UTC().Unix())
	fmt.Fprintf(&stream, "data %d\n%s\n", len(message), message)
	if parent != "" {
		fmt.Fprintf(&stream, "from %s\n", parent)
	}
	for _, path := range paths {
		fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n", path, len(files[path]))
		stream.Write(files[path])
		stream.WriteString("\n")
	}
	stream.WriteString("done\n")

	made, err := r.runWithInput(ctx, harnessCommitEnvironment(), &stream, io.Discard, "fast-import", "--quiet", "--done")
	if err != nil {
		return "", err
	}
	if made.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("write the records commit failed with exit code %d: %s", made.ExitCode, strings.TrimSpace(made.Stderr))
	}
	commit, err := r.revParse(ctx, building+"^{commit}")
	if err != nil {
		return "", err
	}
	dropped, err := r.manager.run(ctx, append(r.gitDir(), "update-ref", "-d", building)...)
	if err != nil {
		return "", err
	}
	if dropped.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("drop the temporary reference %s failed with exit code %d: %s", building, dropped.ExitCode, strings.TrimSpace(dropped.Stderr))
	}
	return commit, nil
}

// refuseTakenPaths refuses paths that would rewrite a record the parent holds,
// or that sit under a file or over a directory of one, which writing would
// replace. The new paths are checked against one another the same way.
func refuseTakenPaths(paths []string, existing map[string]bool) error {
	directories := map[string]bool{}
	for path := range existing {
		for index := 0; index < len(path); index++ {
			if path[index] == '/' {
				directories[path[:index]] = true
			}
		}
	}
	added := map[string]bool{}
	for _, path := range paths {
		if existing[path] || directories[path] {
			return fmt.Errorf("%w: %s", ErrRecordPathTaken, path)
		}
		for index := 0; index < len(path); index++ {
			if path[index] == '/' && (existing[path[:index]] || added[path[:index]]) {
				return fmt.Errorf("%w: %s lies under the record %s", ErrRecordPathTaken, path, path[:index])
			}
		}
		added[path] = true
	}
	return nil
}

// validateRecordPath refuses a path that is not a relative, slash-separated file
// path of the kind a record is kept at.
func validateRecordPath(path string) error {
	switch {
	case path == "":
		return errors.New("a record needs a path")
	case strings.HasPrefix(path, "/"):
		return fmt.Errorf("record path %q must be relative", path)
	case !strings.HasSuffix(path, ".json"):
		return fmt.Errorf("record path %q must end in .json", path)
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f || r == '\\' || r == '"' {
			return fmt.Errorf("record path %q holds a character that cannot be in one", path)
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || segment == ".git" {
			return fmt.Errorf("record path %q holds a segment that cannot be in one", path)
		}
	}
	return nil
}

// Push moves the remote's records reference to commit, with a lease naming the
// commit it must be at: expected, or empty where it must not exist. A lease the
// remote refused ends on ErrRecordsLeaseRefused. The commit is pushed from the
// records repository, so the user's repository is not involved.
func (r *RecordsRepository) Push(ctx context.Context, commit, expected string) error {
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("records commit %q is invalid", commit)
	}
	if expected != "" && !commitPattern.MatchString(expected) {
		return fmt.Errorf("expected records commit %q is invalid", expected)
	}
	url, err := r.remoteURL(ctx, true)
	if err != nil {
		return err
	}
	return r.manager.pushLeased(ctx, leasedPush{git: r.gitDir(), url: url, lease: ErrRecordsLeaseRefused}, RecordsRef, commit, expected)
}
