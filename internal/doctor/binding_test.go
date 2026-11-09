package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// realClone is a Git repository with a remote, which is what a binding needs
// to name: the binding check reads the common Git directory for real.
func realClone(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"remote", "add", "origin", "git@github.com:example/thing.git"},
	} {
		if output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return directory
}

// diagnoseConfiguration is the world's diagnosis reading the configuration at
// path, as the CLI hands it over when discovery found that file.
func (w *world) diagnoseConfiguration(path string) Report {
	w.t.Helper()
	load := func() (config.Resolved, error) { return config.LoadResolved(path) }
	return Diagnose(context.Background(), Environment{
		Runner:         w.runner,
		LookPath:       w.lookPath,
		Getenv:         w.getenv,
		UserHomeDir:    func() (string, error) { return w.project, nil },
		GOOS:           w.goos,
		Version:        currentVersion,
		Load:           load,
		Locate:         func() (string, error) { return path, nil },
		Now:            time.Now,
		ProcessRunning: func(pid int) (bool, error) { return w.alive[pid], nil },
	})
}

// keptConfiguration writes the healthy configuration in the project's
// directory in the world's machine home, naming repository.
func (w *world) keptConfiguration(repository string) string {
	w.t.Helper()
	directory := home.ProjectDirectory(w.stateRoot, "yoyodyne")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		w.t.Fatal(err)
	}
	path := filepath.Join(directory, home.ConfigFileName)
	content := strings.Replace(healthyConfig, "  repository: .\n", "  repository: "+repository+"\n", 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *world) bindTo(checkout string) {
	w.t.Helper()
	if _, err := home.Bind(home.AgreeOptions{Root: w.stateRoot, ProductID: "yoyodyne", Checkout: checkout, Remote: "origin"}); err != nil {
		w.t.Fatalf("Bind() error = %v", err)
	}
}

// A configuration kept in the machine home that names one clone while the
// binding names another is out of date, because every command uses the bound
// clone; the finding warns, names both and the bind that puts the file right.
func TestTheBindingFindingIsAWarningWhenTheKeptConfigurationNamesAnotherClone(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	named, bound := realClone(t), realClone(t)
	path := world.keptConfiguration(named)
	world.bindTo(bound)

	finding, found := findingFor(world.diagnoseConfiguration(path), "binding")
	if !found || finding.Status != StatusWarning {
		t.Fatalf("binding = %+v (found %v), want a warning", finding, found)
	}
	for _, want := range []string{named, bound} {
		if !strings.Contains(finding.Summary, want) {
			t.Errorf("binding summary %q does not name %q", finding.Summary, want)
		}
	}
	if !strings.Contains(finding.Summary, "out of date") {
		t.Errorf("binding summary %q does not say the file is out of date", finding.Summary)
	}
	for _, want := range []string{"yoyo project bind --product yoyodyne", bound} {
		if !strings.Contains(finding.Remedy, want) {
			t.Errorf("binding remedy %q does not say %q", finding.Remedy, want)
		}
	}
}

func TestTheBindingFindingIsOKWhenTheKeptConfigurationAndTheBindingAgree(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	clone := realClone(t)
	path := world.keptConfiguration(clone)
	world.bindTo(clone)

	finding, found := findingFor(world.diagnoseConfiguration(path), "binding")
	if !found || finding.Status != StatusOK {
		t.Fatalf("binding = %+v (found %v), want ok", finding, found)
	}
}

// A configuration committed in its repository is the clone it is read from, so
// there is nothing for the binding to disagree with and no finding.
func TestTheBindingFindingIsAbsentForACommittedConfiguration(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	other := realClone(t)
	world.bindTo(other)

	report := world.diagnose()
	if finding, found := findingFor(report, "binding"); found {
		t.Fatalf("binding = %+v, want no binding finding for a committed configuration\n%s", finding, render(report))
	}
}
