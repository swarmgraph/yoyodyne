package repositoryread

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The reader is checked against a real repository, because what is claimed is
// about Git rather than about how the harness spells a command: a path is read
// out of the commit HEAD names, the working tree is never consulted, and a path
// that names nothing at that commit is refused with the commit named.
func TestReaderReadsTheRecordedCommitAndNeverTheWorkingTree(t *testing.T) {
	t.Parallel()

	repository := newRepository(t, map[string]string{
		"CLAUDE.md":             "# Project Instructions\n\n## The tracker is not a developer-run tool\n",
		"docs/work.md":          "how work flows\n",
		"docs/decisions/one.md": "a decision\n",
	})
	head := revParse(t, repository)
	// An edit in the working tree that nothing committed is exactly what a read
	// must not see: the tree at HEAD is the record, and the working tree is
	// whatever an operator is in the middle of.
	if err := os.WriteFile(filepath.Join(repository, "CLAUDE.md"), []byte("uncommitted edit\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "untracked.md"), []byte("never committed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reader := Reader{
		Process:      execution.OSProcessRunner{},
		Directory:    repository,
		Clock:        fixedClock{},
		RedactValues: []string{"developer-run"},
	}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "CLAUDE.md", Why: "before advising"},
		{Action: ActionList, Path: "docs"},
		{Action: ActionList},
		{Action: ActionRead, Path: "untracked.md"},
		{Action: ActionRead, Path: "docs"},
		{Action: ActionList, Path: "docs/work.md"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("Read() returned %d result(s) for 6 requests", len(results))
	}
	// A path that climbs out, an absolute one, and the root on a read are each
	// refused as a result of their own, against the commit, beside the paths that
	// were read — never by losing the block. They are a block of their own here
	// only so the six above keep their positions.
	refused, err := reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "../secret"},
		{Action: ActionRead, Path: "/etc/passwd"},
		{Action: ActionRead, Path: "."},
		{Action: ActionRead, Path: "docs/work.md"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	for i, want := range map[int]string{0: "climbs out of the repository", 1: "path is absolute", 2: "path is required on a read"} {
		if !strings.Contains(refused[i].Problem, want) || refused[i].Commit != head || refused[i].Content != "" {
			t.Errorf("refused[%d] = %#v, want a refusal saying %q at HEAD", i, refused[i], want)
		}
	}
	if refused[3].Problem != "" || !strings.Contains(refused[3].Content, "how work flows") {
		t.Fatalf("a good path beside refused ones = %#v", refused[3])
	}
	for i, result := range results {
		if result.Commit != head {
			t.Errorf("results[%d].Commit = %q, want HEAD %q", i, result.Commit, head)
		}
		if result.ReadAt != (fixedClock{}).Now() {
			t.Errorf("results[%d].ReadAt = %v, want the harness's clock", i, result.ReadAt)
		}
	}

	read := results[0]
	if read.Problem != "" || read.Truncated {
		t.Fatalf("read CLAUDE.md = %#v", read)
	}
	if strings.Contains(read.Content, "uncommitted") || !strings.Contains(read.Content, "# Project Instructions") {
		t.Fatalf("the read saw the working tree rather than the commit: %q", read.Content)
	}
	// The content is redacted like every other provider-facing path.
	if strings.Contains(read.Content, "developer-run") || !strings.Contains(read.Content, "not a ") {
		t.Fatalf("the content was not redacted: %q", read.Content)
	}
	if read.Size != len("# Project Instructions\n\n## The tracker is not a developer-run tool\n") || read.Why != "before advising" || read.Path != "CLAUDE.md" {
		t.Fatalf("read = %#v", read)
	}

	listed := results[1]
	if listed.Problem != "" || strings.Join(listed.Entries, ",") != "decisions/,work.md" || listed.Size != 2 {
		t.Fatalf("list docs = %#v", listed)
	}
	root := results[2]
	if root.Problem != "" || root.Path != "" || strings.Join(root.Entries, ",") != "CLAUDE.md,docs/" {
		t.Fatalf("list root = %#v", root)
	}
	for i, want := range map[int]string{
		3: "there is no untracked.md in the tree at " + head[:12],
		4: "docs is a directory at " + head[:12],
		5: "docs/work.md is a file at " + head[:12] + ", not a directory",
	} {
		if !strings.Contains(results[i].Problem, want) {
			t.Errorf("results[%d].Problem = %q, want it to say %q", i, results[i].Problem, want)
		}
		if results[i].Content != "" || results[i].Entries != nil {
			t.Errorf("results[%d] refused and still returned something: %#v", i, results[i])
		}
	}
}

// A file larger than one read may return is cut with the cut declared and its
// whole size named, and a reply's reads together are bounded again: the second
// long file in one block is cut by what the first left.
func TestReaderBoundsWhatOneReadAndOneReplyReturn(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("0123456789abcdef\n", (MaxContentBytes/17)+64)
	repository := newRepository(t, map[string]string{
		"long-1.md":  long,
		"long-2.md":  long,
		"long-3.md":  long,
		"short.md":   "short\n",
		"binary.bin": string([]byte{0x89, 'P', 'N', 'G', 0, 1, 2}),
	})

	reader := Reader{Process: execution.OSProcessRunner{}, Directory: repository}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "long-1.md"},
		{Action: ActionRead, Path: "long-2.md"},
		{Action: ActionRead, Path: "long-3.md"},
		{Action: ActionRead, Path: "short.md"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	first, second, third, short := results[0], results[1], results[2], results[3]
	if !first.Truncated || len(first.Content) > MaxContentBytes || first.Size != len(long) || !strings.Contains(first.TruncatedBy, "one read may return") {
		t.Fatalf("the first long read = size %d, %d bytes returned, truncated %t by %q", first.Size, len(first.Content), first.Truncated, first.TruncatedBy)
	}
	if !second.Truncated || len(second.Content) > MaxContentBytes {
		t.Fatalf("the second long read = %d bytes returned, truncated %t", len(second.Content), second.Truncated)
	}
	// Two reads at the per-read bound spend the reply's budget between them, so
	// the third is refused for want of budget rather than cut to nothing.
	if third.Problem == "" || !strings.Contains(third.Problem, "all one reply may return") || third.Content != "" {
		t.Fatalf("the third long read = %#v, want it refused for the reply's budget", third)
	}
	if len(first.Content)+len(second.Content) > MaxBytesPerReply {
		t.Fatalf("the reply returned %d bytes, over the %d one reply may", len(first.Content)+len(second.Content), MaxBytesPerReply)
	}
	if short.Problem == "" || !strings.Contains(short.Problem, "all one reply may return") {
		t.Fatalf("a read after the budget was spent = %#v", short)
	}
	// The budget is per reply, so the next block starts with it whole; and a
	// file that is not text is refused as such rather than handed over as bytes.
	results, err = reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "short.md"},
		{Action: ActionRead, Path: "binary.bin"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if results[0].Problem != "" || results[0].Content != "short" {
		t.Fatalf("a short read in a fresh block = %#v", results[0])
	}
	if results[1].Problem == "" || !strings.Contains(results[1].Problem, "is not text") {
		t.Fatalf("a binary read = %#v, want it refused as not text", results[1])
	}
}

// A reader with nothing to run Git with, and a directory that is not a
// repository, are the capability failing rather than a path failing: nothing was
// read, and the error says so instead of a result saying the path was missing.
func TestReaderFailsAsACapabilityWhereNothingCanBeRead(t *testing.T) {
	t.Parallel()

	if _, err := (Reader{}).Read(context.Background(), []Request{{Action: ActionRead, Path: "x"}}); err == nil {
		t.Fatal("a reader with no process runner read something")
	}
	notARepository := Reader{Process: execution.OSProcessRunner{}, Directory: t.TempDir()}
	if _, err := notARepository.Read(context.Background(), []Request{{Action: ActionRead, Path: "x"}}); err == nil || !strings.Contains(err.Error(), "resolve the commit") {
		t.Fatalf("a reader outside a repository error = %v, want the commit it could not resolve", err)
	}
	if results, err := notARepository.Read(context.Background(), nil); err != nil || results != nil {
		t.Fatalf("Read() of nothing = %#v, %v", results, err)
	}
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

// newRepository is a repository holding the given files in one commit. One
// commit rather than one per file, because a commit costs the better part of a
// second on some machines and nothing here is about history.
func newRepository(t *testing.T, files map[string]string) string {
	t.Helper()

	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	git(t, repository, "init", "-q", "-b", "main")
	git(t, repository, "config", "user.name", "Yoyodyne Test")
	git(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	for name, content := range files {
		target := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	git(t, repository, "add", "-A")
	git(t, repository, "commit", "-q", "-m", "fixture")
	return repository
}

func revParse(t *testing.T, repository string) string {
	t.Helper()

	output, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse error = %v", err)
	}
	return strings.TrimSpace(string(output))
}

func git(t *testing.T, repository string, args ...string) {
	t.Helper()

	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}

// The companion intent repository is read at the commit its target branch
// names: an edit in its working tree and a file nobody committed are never what
// a role is shown, and the result and its rendering say which repository and
// branch answered.
func TestReaderReadsTheCompanionIntentRepositoryAtItsTargetBranch(t *testing.T) {
	t.Parallel()

	project := newRepository(t, map[string]string{"README.md": "the project\n"})
	intent := newRepository(t, map[string]string{"docs/product/brief.md": "committed brief\n"})
	intentHead := revParse(t, intent)
	if err := os.WriteFile(filepath.Join(intent, "docs/product/brief.md"), []byte("uncommitted edit\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(intent, "docs/product/draft.md"), []byte("never committed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	branchOutput, err := exec.Command("git", "-C", intent, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("git symbolic-ref error = %v", err)
	}
	branch := strings.TrimSpace(string(branchOutput))

	reader := Reader{Process: execution.OSProcessRunner{}, Directory: project, IntentDirectory: intent, Clock: fixedClock{}}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "docs/product/brief.md", Repository: RepositoryIntent},
		{Action: ActionList, Path: "docs/product", Repository: RepositoryIntent},
		{Action: ActionRead, Path: "README.md"},
		{Action: ActionRead, Path: "docs/product/brief.md"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	read, list, projectRead, missing := results[0], results[1], results[2], results[3]
	if read.Problem != "" || strings.TrimSpace(read.Content) != "committed brief" {
		t.Fatalf("intent read = %#v, want the committed text", read)
	}
	if read.Commit != intentHead || read.Repository != RepositoryIntent || read.Branch != branch {
		t.Fatalf("intent read = %#v, want commit %s of the intent repository on %s", read, intentHead, branch)
	}
	if list.Problem != "" || len(list.Entries) != 1 || list.Entries[0] != "brief.md" {
		t.Fatalf("intent list = %#v, want brief.md and not the untracked draft", list)
	}
	if projectRead.Problem != "" || projectRead.Commit != revParse(t, project) || projectRead.Repository != "" || projectRead.Branch != "" {
		t.Fatalf("project read = %#v", projectRead)
	}
	if missing.Problem == "" || !strings.Contains(missing.Problem, "no docs/product/brief.md in the tree") {
		t.Fatalf("project read of an intent path = %#v, want it refused", missing)
	}
	rendered := Render(results, AsEvidence)
	if want := "in the intent repository at " + intentHead[:12] + " on " + branch; !strings.Contains(rendered, want) {
		t.Fatalf("Render() = %q, want it to contain %q", rendered, want)
	}
	if !strings.Contains(read.Describe(), "in the intent repository at "+intentHead[:12]+" on "+branch) {
		t.Fatalf("Describe() = %q", read.Describe())
	}
	if strings.Contains(projectRead.Describe(), "intent") {
		t.Fatalf("a project read describes itself as %q", projectRead.Describe())
	}
}

// A project that keeps its intent in its own repository refuses a request naming
// the intent repository, as a result, and the project read beside it succeeds.
func TestReaderRefusesTheIntentRepositoryWhereThereIsNone(t *testing.T) {
	t.Parallel()

	project := newRepository(t, map[string]string{"README.md": "the project\n"})
	reader := Reader{Process: execution.OSProcessRunner{}, Directory: project, Clock: fixedClock{}}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "README.md", Repository: RepositoryIntent},
		{Action: ActionRead, Path: "README.md"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(results[0].Problem, "keeps its intent in its own repository") || results[0].Content != "" || results[0].Repository != RepositoryIntent {
		t.Fatalf("intent read = %#v, want a refusal", results[0])
	}
	if results[1].Problem != "" || results[1].Content == "" || results[1].Commit != revParse(t, project) {
		t.Fatalf("project read = %#v", results[1])
	}
}

// An intent repository with no branch checked out names no target branch, so
// nothing is read from it, and the project's reads are unaffected.
func TestReaderReadsNothingFromADetachedIntentRepository(t *testing.T) {
	t.Parallel()

	project := newRepository(t, map[string]string{"README.md": "the project\n"})
	intent := newRepository(t, map[string]string{"docs/product/brief.md": "committed brief\n"})
	git(t, intent, "checkout", "-q", "--detach")

	reader := Reader{Process: execution.OSProcessRunner{}, Directory: project, IntentDirectory: intent, Clock: fixedClock{}}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionRead, Path: "docs/product/brief.md", Repository: RepositoryIntent},
		{Action: ActionRead, Path: "README.md"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(results[0].Problem, "detached") || results[0].Content != "" {
		t.Fatalf("intent read = %#v, want a refusal saying the HEAD is detached", results[0])
	}
	if results[1].Problem != "" || results[1].Content == "" {
		t.Fatalf("project read = %#v", results[1])
	}
}
