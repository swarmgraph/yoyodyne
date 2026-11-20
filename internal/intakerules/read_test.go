package intakerules

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func git(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The rules are read as the target branch holds them, and neither an edit in
// the working tree nor a commit on the branch checked out there is read.
func TestRulesAreReadFromTheTargetBranchAndNeverTheWorkingTree(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	git(t, repository, "init", "--quiet", "--initial-branch=main")
	write(t, repository, "setup/intake/rules.md", "## Rules\n\n"+goodRule)
	git(t, repository, "add", "-A")
	git(t, repository, "commit", "--quiet", "-m", "rules")
	target := git(t, repository, "rev-parse", "HEAD")

	// Another branch checked out with a different rule committed, and an edit on
	// top of that nobody committed: neither is what main holds.
	git(t, repository, "checkout", "--quiet", "-b", "work")
	write(t, repository, "setup/intake/rules.md", "## Rules\n\n"+strings.Replace(goodRule, "slack-bug-report", "committed-elsewhere", 1))
	git(t, repository, "commit", "--quiet", "-am", "elsewhere")
	write(t, repository, "setup/intake/rules.md", "## Rules\n\n"+strings.Replace(goodRule, "slack-bug-report", "uncommitted", 1))

	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Remote:         "origin",
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	rules, err := Read(context.Background(), manager, Request{Query: "slack-bugs", Path: "setup/intake/rules.md", TargetBranch: "main", Goals: testGoals()})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if rules.Commit != target || rules.Branch != "main" || rules.Path != "setup/intake/rules.md" || rules.Query != "slack-bugs" {
		t.Fatalf("Read() read at %q on %q for %q, want main's commit %s", rules.Commit, rules.Branch, rules.Query, target)
	}
	if rules.Unreadable != "" || len(rules.Rules) != 1 || rules.Rules[0].ID != "slack-bug-report" {
		t.Fatalf("Read() = %#v, want the rule main holds", rules)
	}

	missing, err := Read(context.Background(), manager, Request{Query: "slack-bugs", Path: "setup/intake/other.md", TargetBranch: "main", Goals: testGoals()})
	if err != nil {
		t.Fatalf("Read() of a missing document error = %v, want it reported as unreadable", err)
	}
	if missing.Commit != target || !strings.Contains(missing.Unreadable, "main holds no file at setup/intake/other.md") {
		t.Fatalf("Read() of a missing document = %#v", missing)
	}
}

type fakeRepository struct {
	commit    string
	commitErr error
	file      gitworktree.FileAt
	fileErr   error
	read      []string
}

func (f *fakeRepository) TargetCommit(context.Context, string) (string, error) {
	return f.commit, f.commitErr
}

func (f *fakeRepository) FileAtCommit(_ context.Context, commit, path string, _ int64) (gitworktree.FileAt, error) {
	f.read = append(f.read, commit+":"+path)
	return f.file, f.fileErr
}

// A read that fails is an error to try again, never a document to report, and
// a document the target branch holds past the size read is unreadable.
func TestAReadThatFailsIsAnErrorAndATooLargeDocumentIsUnreadable(t *testing.T) {
	t.Parallel()

	request := Request{Query: "q", Path: "rules.md", TargetBranch: "main", Goals: testGoals()}
	if _, err := Read(context.Background(), &fakeRepository{commitErr: errors.New("no such branch")}, request); err == nil || !strings.Contains(err.Error(), "no such branch") {
		t.Fatalf("Read() with an unresolvable branch error = %v", err)
	}
	if _, err := Read(context.Background(), &fakeRepository{commit: "abc", fileErr: errors.New("git died")}, request); err == nil || !strings.Contains(err.Error(), "git died") {
		t.Fatalf("Read() with Git failing error = %v", err)
	}
	large, err := Read(context.Background(), &fakeRepository{commit: "abc", file: gitworktree.FileAt{Size: MaxDocumentBytes + 1}}, request)
	if err != nil || !strings.Contains(large.Unreadable, "read only up to") || large.Commit != "abc" {
		t.Fatalf("Read() of a large document = %#v, %v", large, err)
	}
	unknown := request
	unknown.Goals = goal.Unreadable("the goals document is missing")
	repository := &fakeRepository{commit: "abc"}
	if _, err := Read(context.Background(), repository, unknown); err == nil || !strings.Contains(err.Error(), "cannot be read") || len(repository.read) != 0 {
		t.Fatalf("Read() with no goals to check against error = %v, read %v", err, repository.read)
	}
	for _, path := range []string{"../rules.md", "/rules.md", "rules.txt", ""} {
		bad := request
		bad.Path = path
		if _, err := Read(context.Background(), &fakeRepository{commit: "abc"}, bad); err == nil {
			t.Errorf("Read() of %q = nil error, want the path refused", path)
		}
	}
}
