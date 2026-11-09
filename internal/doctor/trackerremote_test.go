package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// externalDiagnosis diagnoses the world with its configuration kept outside the
// repository it describes, in a directory of its own, naming the project's
// repository by absolute path -- which is how `yoyo init --external` writes it.
func externalDiagnosis(t *testing.T, w *world) Report {
	t.Helper()
	kept := filepath.Join(t.TempDir(), config.DirectoryName)
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	external := strings.Replace(w.configuration, "repository: .", "repository: "+w.project, 1)
	path := filepath.Join(kept, config.FileName)
	if err := os.WriteFile(path, []byte(external), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return Diagnose(context.Background(), Environment{
		Runner:      w.runner,
		LookPath:    w.lookPath,
		Getenv:      w.getenv,
		UserHomeDir: func() (string, error) { return w.project, nil },
		GOOS:        w.goos,
		Version:     currentVersion,
		Load:        func() (config.Resolved, error) { return config.LoadResolved(path) },
		Locate:      func() (string, error) { return path, nil },
		Now:         time.Now,
	})
}

func (w *world) gitRemotes(output string) { w.runner.reply("remote -v", succeeded(output)) }

func (w *world) trackerRemotes(output string) {
	w.runner.reply("dolt remote list", succeeded(output))
}

func (w *world) askedForTrackerRemotes() bool {
	for _, command := range w.runner.commands {
		if strings.Contains(strings.Join(command, " "), "dolt remote list") {
			return true
		}
	}
	return false
}

const projectOrigin = "origin\tgit@github.com:acme/thing.git (fetch)\norigin\tgit@github.com:acme/thing.git (push)\n"

func TestATrackerSyncingThroughTheProjectsOwnRemoteIsAWarningWithTheRemoveCommand(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.gitRemotes(projectOrigin)
	world.trackerRemotes(`[{"name":"origin","url":"git+ssh://git@github.com/acme/thing.git"}]`)
	report := externalDiagnosis(t, world)

	finding, found := findingFor(report, "tracker-remote")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("tracker-remote = %#v, found = %t, want a warning: %s", finding, found, render(report))
	}
	want := "bd dolt remote remove origin   # in " + world.project
	if finding.Remedy != want {
		t.Errorf("remedy = %q, want %q", finding.Remedy, want)
	}
	for _, text := range []string{"origin", "backlog", "--tracker-remote"} {
		if !strings.Contains(finding.Summary, text) {
			t.Errorf("summary = %q, want %q said", finding.Summary, text)
		}
	}
	if !strings.Contains(finding.Detail, "git+ssh://git@github.com/acme/thing.git") {
		t.Errorf("detail = %q, want the URL named", finding.Detail)
	}
	if !report.Healthy() {
		t.Errorf("Diagnose() = %s, want work still able to run", report.Status)
	}
}

func TestATrackerSyncingElsewhereIsFine(t *testing.T) {
	t.Parallel()

	for name, tracker := range map[string]string{
		"another repository": `[{"name":"origin","url":"git+ssh://git@github.com/acme/backlog.git"}]`,
		"no remotes":         `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			world := newWorld(t)
			world.gitRemotes(projectOrigin)
			world.trackerRemotes(tracker)
			finding, found := findingFor(externalDiagnosis(t, world), "tracker-remote")
			if !found || finding.Status != StatusOK {
				t.Fatalf("tracker-remote = %#v, found = %t, want ok", finding, found)
			}
		})
	}
}

func TestAConfigurationInsideTheRepositoryNeverAsksTheTrackerWhereItSyncs(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.gitRemotes(projectOrigin)
	world.trackerRemotes(`[{"name":"origin","url":"git+ssh://git@github.com/acme/thing.git"}]`)
	report := world.diagnose()

	if finding, found := findingFor(report, "tracker-remote"); found {
		t.Fatalf("tracker-remote = %#v, want nothing said about a configuration inside the repository", finding)
	}
	if world.askedForTrackerRemotes() {
		t.Error("bd was asked for its sync remotes about a configuration inside the repository")
	}
}

func TestATrackerThatCannotBeAskedWhereItSyncsSaysNothingMore(t *testing.T) {
	t.Parallel()

	t.Run("bd fails", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.gitRemotes(projectOrigin)
		world.runner.reply("dolt remote list", failed("no beads database here"))
		if finding, found := findingFor(externalDiagnosis(t, world), "tracker-remote"); found {
			t.Fatalf("tracker-remote = %#v, want checkTracker alone to report it", finding)
		}
	})
	t.Run("git fails", func(t *testing.T) {
		t.Parallel()
		world := newWorld(t)
		world.runner.reply("remote -v", failed("not a git repository"))
		world.trackerRemotes(`[]`)
		if finding, found := findingFor(externalDiagnosis(t, world), "tracker-remote"); found {
			t.Fatalf("tracker-remote = %#v, want nothing said", finding)
		}
	})
}

func TestProjectGitRemotesReadsEveryFetchAndPushURLByName(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.gitRemotes("origin\tgit@github.com:acme/thing.git (fetch)\n" +
		"origin\tgit@github.com:acme/thing.git (push)\n" +
		"upstream\thttps://github.com/up/thing (fetch)\n" +
		"upstream\tgit@github.com:up/thing.git (push)\n")
	got, err := ProjectGitRemotes(context.Background(), world.runner, world.project)
	if err != nil {
		t.Fatalf("ProjectGitRemotes() error = %v", err)
	}
	want := map[string][]string{
		"origin":   {"git@github.com:acme/thing.git"},
		"upstream": {"https://github.com/up/thing", "git@github.com:up/thing.git"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectGitRemotes() = %v, want %v", got, want)
	}

	world.gitRemotes("")
	none, err := ProjectGitRemotes(context.Background(), world.runner, world.project)
	if err != nil || len(none) != 0 {
		t.Errorf("ProjectGitRemotes() with no remotes = %v, %v, want an empty map and no error", none, err)
	}

	world.runner.reply("remote -v", failed("fatal: not a git repository"))
	if _, err := ProjectGitRemotes(context.Background(), world.runner, world.project); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("ProjectGitRemotes() error = %v, want Git's own words", err)
	}
	world.runner.fail("remote -v", errors.New("exec format error"))
	if _, err := ProjectGitRemotes(context.Background(), world.runner, world.project); err == nil {
		t.Error("ProjectGitRemotes() with git unable to run = nil error")
	}
}

func TestTrackerRemotesOnProjectComparesPlacesNotSpellings(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.gitRemotes("origin\thttps://github.com/acme/thing (fetch)\norigin\thttps://github.com/acme/thing (push)\n")
	world.trackerRemotes(`[{"name":"backup","url":"git+ssh://git@github.com/acme/thing.git"},{"name":"other","url":"git+ssh://git@github.com/acme/else.git"}]`)
	shared, all, err := TrackerRemotesOnProject(context.Background(), world.runner, world.project)
	if err != nil {
		t.Fatalf("TrackerRemotesOnProject() error = %v", err)
	}
	if len(all) != 2 {
		t.Errorf("all = %v, want every remote bd lists", all)
	}
	want := []SharedTrackerRemote{{Name: "backup", URL: "git+ssh://git@github.com/acme/thing.git", GitRemote: "origin"}}
	if !slices.Equal(shared, want) {
		t.Errorf("shared = %v, want %v", shared, want)
	}
}

func TestRemoveTrackerRemotesCommand(t *testing.T) {
	t.Parallel()

	if got, want := RemoveTrackerRemotesCommand([]string{"origin", "backup"}, "/work/thing"),
		"bd dolt remote remove origin && bd dolt remote remove backup   # in /work/thing"; got != want {
		t.Errorf("RemoveTrackerRemotesCommand() = %q, want %q", got, want)
	}
	if got, want := RemoveTrackerRemotesCommand([]string{"origin"}, ""), "bd dolt remote remove origin"; got != want {
		t.Errorf("RemoveTrackerRemotesCommand() without a directory = %q, want %q", got, want)
	}
}
