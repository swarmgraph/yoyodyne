package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/dolttool"
)

func TestDoctorReportsDoltMissingOrAtAnotherVersion(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		arrange func(t *testing.T, tools string)
		want    []string
	}{
		"missing": {func(t *testing.T, tools string) {
			if err := os.RemoveAll(tools); err != nil {
				t.Fatal(err)
			}
		}, []string{"dolt 2.7.6 is not in the harness's tools folder", "opened directly"}},
		"another version": {func(t *testing.T, tools string) {
			path := filepath.Join(tools, "dolt.json")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(content), "2.7.6", "2.8.0", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}, []string{"version 2.8.0", "dolt 2.7.6 is wanted", "opened directly"}},
		"changed": {func(t *testing.T, tools string) {
			if err := os.WriteFile(filepath.Join(tools, "dolt"), []byte("another"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, []string{"not the one setup installed", "dolt 2.7.6 is wanted"}},
	} {
		world := newWorld(t)
		test.arrange(t, filepath.Join(world.stateRoot, dolttool.ToolsDirectoryName))
		report := world.diagnose()
		finding, found := findingFor(report, doltCheck)
		if !found || finding.Status != StatusWarning || finding.Remedy == "" {
			t.Errorf("%s: finding = %#v, want a warning with a remedy", name, finding)
		}
		for _, want := range test.want {
			if !strings.Contains(finding.Summary, want) {
				t.Errorf("%s: summary = %q, want %q in it", name, finding.Summary, want)
			}
		}
		if !report.Healthy() {
			t.Errorf("%s stopped work running: %s", name, render(report))
		}
	}
}
