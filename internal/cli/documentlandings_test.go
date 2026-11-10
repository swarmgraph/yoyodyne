package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const landingPath = "docs/goals.md"

// landingRepository makes a repository with one branch and a committed file.
func landingRepository(t *testing.T, name, content string) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Yoyodyne Test"},
		{"config", "user.email", "yoyodyne@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		git(t, root, args...)
	}
	return root, commitLanding(t, root, content)
}

// commitLanding commits content at landingPath and returns the new commit.
func commitLanding(t *testing.T, root, content string) string {
	t.Helper()
	path := filepath.Join(root, landingPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	git(t, root, "add", landingPath)
	git(t, root, "commit", "-m", "land "+content)
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse error = %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestDocumentLandingsReadTheRepositoryTheDocumentsAreKept(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		companion bool
		wantName  string
	}{
		{name: "the project's own repository", wantName: "the project's repository at "},
		{name: "a companion intent repository", companion: true, wantName: "the companion intent repository at "},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			project, projectFirst := landingRepository(t, "project", "project one\n")
			var companion string
			if test.companion {
				companion, _ = landingRepository(t, "intent", "intent one\n")
			}
			landings := conversationDocumentLandings(components{
				config:       configWithCompanion(companion),
				repository:   project,
				worktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
				runner:       execution.OSProcessRunner{},
			})
			root, first, want := project, projectFirst, "project one\n"
			if test.companion {
				root, want = companion, "intent one\n"
				first = gitHead(t, companion)
			}
			second := commitLanding(t, root, strings.Replace(want, "one", "two", 1))
			wantSecond := strings.Replace(want, "one", "two", 1)
			// Written and not committed: it must not count.
			if err := os.WriteFile(filepath.Join(root, "docs", "draft.md"), []byte("draft\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			got, err := landings.OnTarget(ctx, landingPath)
			if err != nil {
				t.Fatalf("OnTarget() error = %v", err)
			}
			if !got.Found || string(got.Content) != wantSecond || got.Commit != second || got.Branch != "main" {
				t.Fatalf("OnTarget() = %+v, want %q at %s on main", got, wantSecond, second)
			}
			if got.Repository != test.wantName+root {
				t.Fatalf("Repository = %q, want %q", got.Repository, test.wantName+root)
			}
			missing, err := landings.OnTarget(ctx, "docs/draft.md")
			if err != nil || missing.Found {
				t.Fatalf("OnTarget(uncommitted) = %+v, %v, want not found", missing, err)
			}

			older, err := landings.AtCommit(ctx, first[:10], landingPath)
			if err != nil {
				t.Fatalf("AtCommit() error = %v", err)
			}
			if !older.Found || string(older.Content) != want || older.Commit != first || older.Branch != "" {
				t.Fatalf("AtCommit() = %+v, want %q at %s", older, want, first)
			}
			none, err := landings.AtCommit(ctx, second, "docs/absent.md")
			if err != nil || none.Found {
				t.Fatalf("AtCommit(absent) = %+v, %v, want not found", none, err)
			}
			if _, err := landings.AtCommit(ctx, strings.Repeat("0", 40), landingPath); err == nil || !strings.Contains(err.Error(), "is not in "+test.wantName) {
				t.Fatalf("AtCommit(unknown) error = %v, want one saying the commit is not in the repository", err)
			}
			if _, err := landings.AtCommit(ctx, "--all", landingPath); err == nil {
				t.Fatal("AtCommit(--all) error = nil, want a refusal")
			}
		})
	}
}

// A companion's content is read, not the project's, even at a commit only the
// project has.
func TestDocumentLandingsCompanionDoesNotReadTheProject(t *testing.T) {
	t.Parallel()
	project, projectCommit := landingRepository(t, "project", "project\n")
	companion, _ := landingRepository(t, "intent", "intent\n")
	landings := conversationDocumentLandings(components{
		config:       configWithCompanion(companion),
		repository:   project,
		worktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		runner:       execution.OSProcessRunner{},
	})
	if _, err := landings.AtCommit(context.Background(), projectCommit, landingPath); err == nil {
		t.Fatal("AtCommit(a commit of the project) error = nil, want it unknown to the companion")
	}
	got, err := landings.OnTarget(context.Background(), landingPath)
	if err != nil || string(got.Content) != "intent\n" {
		t.Fatalf("OnTarget() = %+v, %v, want the companion's text", got, err)
	}
}

func configWithCompanion(companion string) config.Config {
	cfg := config.Config{}
	cfg.Product.IntentRepository = companion
	return cfg
}

func gitHead(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse error = %v", err)
	}
	return strings.TrimSpace(string(out))
}
