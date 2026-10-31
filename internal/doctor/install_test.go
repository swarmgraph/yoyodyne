package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each declared install step is shown, with what it runs, reads, and makes, and
// a step whose command this machine cannot start is a problem with a remedy. A
// project that declares none is shown no step.
func TestDoctorShowsEachDeclaredInstallStep(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	if report := world.diagnose(); hasInstallFinding(report) {
		t.Fatalf("a project with no install step was shown one: %s", render(report))
	}

	for name, body := range map[string]string{"go.mod": "module example\n", "requirements.txt": "\n", ".gitignore": "vendor/\n.venv/\n"} {
		if err := os.WriteFile(filepath.Join(world.project, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "go.mod", "requirements.txt", ".gitignore"}} {
		if output, err := exec.Command("git", append([]string{"-C", world.project}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	world.configuration = healthyConfig + `install:
  - name: go modules
    run: go mod vendor
    inputs: [go.mod]
    outputs: [vendor]
  - name: python packages
    run: pip install -r requirements.txt
    inputs: [requirements.txt]
    outputs: [.venv]
    time_limit: 4m
`
	world.absent("pip")
	report := world.diagnose()

	vendored, found := findingFor(report, "install:go modules")
	if !found || vendored.Status != StatusOK {
		t.Fatalf("go step = %#v, want it shown as fine: %s", vendored, render(report))
	}
	for _, want := range []string{"go mod vendor", "go.mod", "vendor", "10m0s"} {
		if !strings.Contains(vendored.Summary, want) {
			t.Fatalf("go step summary = %q, want it to say %q", vendored.Summary, want)
		}
	}
	python, found := findingFor(report, "install:python packages")
	if !found || python.Status != StatusProblem || !strings.Contains(python.Summary, "pip") || python.Remedy == "" {
		t.Fatalf("python step = %#v, want pip reported missing with a remedy", python)
	}
}

func hasInstallFinding(report Report) bool {
	for _, finding := range report.Findings {
		if strings.HasPrefix(finding.Check, "install:") {
			return true
		}
	}
	return false
}
