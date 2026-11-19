package gitworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLandingPathsListsWhatTheLandingChanged(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repository, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(repository, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("README.md", "one\n")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	write("internal/a.go", "package a\n")
	write("README.md", "two\n")
	git("add", ".")
	git("commit", "-q", "-m", "landing")
	landing := git("rev-parse", "HEAD")

	manager := newConvergeManager(t, repository)
	paths, err := manager.LandingPaths(context.Background(), landing)
	if err != nil {
		t.Fatalf("LandingPaths() error = %v", err)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"README.md", "internal/a.go"}) {
		t.Fatalf("LandingPaths() = %q, want README.md and internal/a.go", paths)
	}
	if _, err := manager.LandingPaths(context.Background(), "HEAD"); err == nil {
		t.Fatal("LandingPaths(HEAD) = nil error, want a refusal of anything but a full commit hash")
	}
}
