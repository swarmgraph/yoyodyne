package beads_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// The project's tracker file leaves the export to the harness, so no bd write
// made here, by the harness or by a person, rewrites the whole export inside
// its store lock.
func TestThisRepositorysTrackerWritesDoNotExport(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join(repositoryRoot, ".beads", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	settings := regexp.MustCompile(`(?m)^export\.auto:\s*(\S+)\s*$`).FindAllStringSubmatch(string(content), -1)
	if len(settings) != 1 || settings[0][1] != "false" {
		t.Fatalf(".beads/config.yaml sets export.auto %v; want it set once, to false", settings)
	}
}

// The repository's pre-commit hook has every commit that carries the export
// take a fresh one first, and leaves every other commit alone.
func TestACommitCarryingTheExportCarriesOneTakenJustBeforeIt(t *testing.T) {
	t.Parallel()
	hook, err := os.ReadFile(filepath.Join(repositoryRoot, ".beads", "hooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a commit carrying the export", func(t *testing.T) {
		t.Parallel()
		world := hookWorld(t, hook, "printf '{\"id\":\"fresh\"}\\n' > .beads/issues.jsonl\nprintf 'asked\\n' >> \"$YOYO_CALLS\"\n")
		world.write(beads.ExportPath, "{\"id\":\"stale\"}\n")
		world.git("add", beads.ExportPath)
		world.git("commit", "--quiet", "-m", "carry the export")
		if committed := world.git("show", "HEAD:"+beads.ExportPath); committed != "{\"id\":\"fresh\"}\n" {
			t.Fatalf("committed export = %q; want the one taken just before the commit", committed)
		}
		if calls := world.calls(); calls != 1 {
			t.Fatalf("yoyo was asked %d times; want once", calls)
		}
	})

	t.Run("a commit leaving the export alone", func(t *testing.T) {
		t.Parallel()
		world := hookWorld(t, hook, "printf 'asked\\n' >> \"$YOYO_CALLS\"\n")
		world.write(beads.ExportPath, "{\"id\":\"working copy\"}\n")
		world.write("other.txt", "changed\n")
		world.git("add", "other.txt")
		world.git("commit", "--quiet", "-m", "something else")
		if calls := world.calls(); calls != 0 {
			t.Fatalf("yoyo was asked %d times for a commit that does not carry the export", calls)
		}
	})

	t.Run("an export that fails", func(t *testing.T) {
		t.Parallel()
		world := hookWorld(t, hook, "printf 'asked\\n' >> \"$YOYO_CALLS\"\nexit 1\n")
		world.write(beads.ExportPath, "{\"id\":\"stale\"}\n")
		world.git("add", beads.ExportPath)
		if output, err := world.command("commit", "--quiet", "-m", "carry the export").CombinedOutput(); err == nil {
			t.Fatal("a commit carrying an export that could not be taken fresh was made")
		} else if !strings.Contains(string(output), "could not be exported") {
			t.Fatalf("refusal said %q", output)
		}
		if head := world.git("rev-list", "--count", "HEAD"); head != "1\n" {
			t.Fatalf("commits = %q; want only the first", head)
		}
	})

	t.Run("no yoyo to export with", func(t *testing.T) {
		t.Parallel()
		world := hookWorld(t, hook, "")
		world.write(beads.ExportPath, "{\"id\":\"stale\"}\n")
		world.git("add", beads.ExportPath)
		if output, err := world.command("commit", "--quiet", "-m", "carry the export").CombinedOutput(); err == nil {
			t.Fatal("a commit carrying the export was made with nothing to export it")
		} else if !strings.Contains(string(output), "no yoyo on PATH") {
			t.Fatalf("refusal said %q", output)
		}
	})
}

type commitWorld struct {
	t                        *testing.T
	repository, bin, yoyoLog string
}

// hookWorld is a repository with one commit, the hook under test as its only
// hook, and a PATH holding git, sh, and — where yoyo is not empty — a yoyo whose
// body it is. bd is not on that PATH, so the hook's bd section passes over.
func hookWorld(t *testing.T, hook []byte, yoyo string) commitWorld {
	t.Helper()
	root := t.TempDir()
	world := commitWorld{t: t, repository: filepath.Join(root, "repository"), bin: filepath.Join(root, "bin"), yoyoLog: filepath.Join(root, "yoyo-calls")}
	for _, dir := range []string{world.repository, world.bin, filepath.Join(root, "hooks"), filepath.Join(world.repository, ".beads"), filepath.Join(root, "home")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"git", "sh"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed: %v", tool, err)
		}
		if err := os.Symlink(path, filepath.Join(world.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if yoyo != "" {
		script := "#!/bin/sh\n[ \"$1 $2\" = \"tracker export\" ] || exit 2\n" + yoyo
		if err := os.WriteFile(filepath.Join(world.bin, "yoyo"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "hooks", "pre-commit"), hook, 0o700); err != nil {
		t.Fatal(err)
	}
	world.git("init", "--quiet")
	world.git("config", "core.hooksPath", filepath.Join(root, "hooks"))
	world.git("config", "user.name", "Test")
	world.git("config", "user.email", "test@example.com")
	world.git("config", "commit.gpgsign", "false")
	world.write("other.txt", "first\n")
	world.write(beads.ExportPath, "{\"id\":\"first\"}\n")
	world.git("add", ".")
	world.git("commit", "--quiet", "--no-verify", "-m", "first")
	return world
}

func (w commitWorld) write(path, content string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.repository, filepath.FromSlash(path)), []byte(content), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w commitWorld) command(args ...string) *exec.Cmd {
	command := exec.Command(filepath.Join(w.bin, "git"), args...)
	command.Dir = w.repository
	command.Env = []string{
		"PATH=" + w.bin,
		"HOME=" + filepath.Join(filepath.Dir(w.bin), "home"),
		"GIT_CONFIG_NOSYSTEM=1",
		"YOYO_CALLS=" + w.yoyoLog,
	}
	return command
}

func (w commitWorld) git(args ...string) string {
	w.t.Helper()
	output, err := w.command(args...).CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func (w commitWorld) calls() int {
	w.t.Helper()
	content, err := os.ReadFile(w.yoyoLog)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.Count(string(content), "asked\n")
}
