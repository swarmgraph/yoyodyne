package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each declared environment is shown with every field it was read with, and a
// project that declares none is shown no environment.
func TestDoctorShowsEachDeclaredEnvironment(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	for _, finding := range world.diagnose().Findings {
		if strings.HasPrefix(finding.Check, "environment:") {
			t.Fatalf("a project with no environment was shown one: %#v", finding)
		}
	}

	for name, body := range map[string]string{"go.mod": "module example\n", ".gitignore": "vendor/\n"} {
		if err := os.WriteFile(filepath.Join(world.project, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "go.mod", ".gitignore"}} {
		if output, err := exec.Command("git", append([]string{"-C", world.project}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	world.configuration = healthyConfig + `install:
  - name: go modules
    run: go mod vendor
    inputs: [go.mod]
    outputs: [vendor]
environments:
  - name: android client
    needs: [go modules]
    check: scripts/ready-android.sh
    test: scripts/integration-android.sh
  - name: web ui
    machine: true
    how_to_install: "npx playwright install chromium"
    where: harness
    check: scripts/ready-browser.sh
    test: scripts/integration-web.sh
    time_limit: 4m
`
	report := world.diagnose()

	android, found := findingFor(report, "environment:android client")
	if !found || android.Status != StatusOK {
		t.Fatalf("android = %#v, want it shown: %s", android, render(report))
	}
	for _, want := range []string{"go modules", "nothing installed for the whole machine", "scripts/ready-android.sh", "scripts/integration-android.sh", "where: developer", "15m0s"} {
		if !strings.Contains(android.Summary, want) {
			t.Fatalf("android summary = %q, want it to say %q", android.Summary, want)
		}
	}
	web, found := findingFor(report, "environment:web ui")
	if !found {
		t.Fatalf("web ui not shown: %s", render(report))
	}
	for _, want := range []string{"no install step", "npx playwright install chromium", "scripts/ready-browser.sh", "scripts/integration-web.sh", "where: harness", "4m0s"} {
		if !strings.Contains(web.Summary, want) {
			t.Fatalf("web summary = %q, want it to say %q", web.Summary, want)
		}
	}
}
