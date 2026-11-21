package cli

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

// The stale-build count is landings that change the build, read off a real
// repository: a landing of documents counts nothing however many commits it
// carries, and a landing touching what gets built counts once however many
// commits it carries. Uncommitted changes to what gets built are reported, as
// the supervisor's rebuild rule reads them.
func TestTheSinkCountsLandingsThatChangeTheBuildNotCommits(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(repository, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// land merges a branch of commits onto main the way a pull request does.
	land := func(branch string, files ...string) {
		t.Helper()
		git("checkout", "-q", "-b", branch)
		for _, file := range files {
			write(file, branch+"\n")
			git("add", "-A")
			git("commit", "-q", "-m", branch+" "+file)
		}
		git("checkout", "-q", "main")
		git("merge", "-q", "--no-ff", branch, "-m", "land "+branch)
	}

	git("init", "-q", "-b", "main")
	write("internal/a.go", "package a\n")
	write("docs/readme.md", "docs\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	build := git("rev-parse", "HEAD")

	landings := buildLandings{repositoryDeployments{repository: repository, runner: execution.OSProcessRunner{}, timeout: time.Minute}}
	ctx := context.Background()

	for _, branch := range []string{"docs-1", "docs-2", "docs-3"} {
		land(branch, "docs/"+branch+".md", "docs/readme.md")
	}
	if behind, err := landings.Behind(ctx, build); err != nil || behind != 0 {
		t.Fatalf("after three landings of documents Behind = %d, %v; want 0", behind, err)
	}
	if unbuilt, err := landings.Unbuilt(ctx, build); err != nil || !unbuilt.Since.IsZero() {
		t.Fatalf("after three landings of documents Unbuilt = %+v, %v; want nothing unbuilt", unbuilt, err)
	}
	// The repository's own count of commits, which the sink no longer reads.
	if commits := git("rev-list", "--count", build+"..HEAD"); commits != "9" {
		t.Fatalf("the fixture holds %s commits since the build, want 9", commits)
	}

	land("fix", "internal/a.go", "docs/readme.md", "internal/b.go")
	if behind, err := landings.Behind(ctx, build); err != nil || behind != 1 {
		t.Fatalf("after one landing of code Behind = %d, %v; want 1", behind, err)
	}
	unbuilt, err := landings.Unbuilt(ctx, build)
	if err != nil || unbuilt.Since.IsZero() || unbuilt.Uncommitted != "" {
		t.Fatalf("Unbuilt = %+v, %v; want when the code landed and nothing uncommitted", unbuilt, err)
	}

	write("internal/a.go", "package a // edited\n")
	write("docs/readme.md", "edited\n")
	unbuilt, err = landings.Unbuilt(ctx, build)
	if err != nil || !strings.Contains(unbuilt.Uncommitted, "internal/a.go") || strings.Contains(unbuilt.Uncommitted, "readme") {
		t.Fatalf("Unbuilt = %+v, %v; want the uncommitted change to internal/a.go alone", unbuilt, err)
	}
}
