package maintain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The sample secrets are made up and put together from pieces while the test
// runs, as internal/execution's are: a sample written out whole is one the
// forge's secret scanner refuses a push over.
func sampleSecret(prefix, fill string, n int) string {
	return prefix + strings.Repeat(fill, n/len(fill)+1)[:n]
}

var (
	sampleAnthropicKey = sampleSecret("sk-"+"ant-"+"api03-", "Q2xhdWRlS2V5", 90)
	sampleWebToken     = strings.Join([]string{"ey" + "JhbGciOiJSUzI1NiJ9", "ey" + "JzdWIiOiJ1c2VyLWV4YW1wbGUifQ", sampleSecret("", "c2lnbmF0dXJl", 20)}, ".")
	sampleGitHubToken  = sampleSecret("gh"+"p_", "R2l0SHVi9", 36)
)

// exportLine is one work item as the tracker's export records it.
type exportLine struct {
	ID       string           `json:"id"`
	Title    string           `json:"title"`
	Notes    string           `json:"notes,omitempty"`
	Comments []map[string]any `json:"comments,omitempty"`
}

// withExport writes the tracker's export into a repository of its own, wires
// it and a report pile to the pass, and returns the pile.
func withExport(t *testing.T, f *fixture, items ...exportLine) *runstate.ReportStore {
	t.Helper()
	f.pass.Repository = t.TempDir()
	writeExport(t, f, items...)
	reports, err := runstate.NewReportStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.Reports, f.pass.RepositoryID = reports, "calc"
	return reports
}

func writeExport(t *testing.T, f *fixture, items ...exportLine) {
	t.Helper()
	var lines []string
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(encoded))
	}
	path := filepath.Join(f.pass.Repository, beads.ExportPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listReports(t *testing.T, reports *runstate.ReportStore) []report.Report {
	t.Helper()
	listed, err := reports.List()
	if err != nil {
		t.Fatal(err)
	}
	return listed
}

// Whole secrets are found and each is reported once, naming the item and the
// kind and never the value; prefixes quoted in a note are not secrets.
func TestTheExportIsReadForWholeSecretsAndEachIsReportedOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	items := []exportLine{
		{ID: "calc-1", Title: "a provider failed", Notes: "claude exited 1:\nerror: invalid key " + sampleAnthropicKey + "\n(401)"},
		{ID: "calc-2", Title: "a login expired", Comments: []map[string]any{{"text": "Authorization: Bearer " + sampleWebToken}}},
		{ID: "calc-3", Title: "search the notes", Notes: "look for " + "sk-" + "ant-" + ", " + "sk-" + "proj-" + ", " + "ey" + "J" + " or " + "Bear" + "er tokens"},
		{ID: "calc-4", Title: "ordinary work", Notes: "settled 3 runs; Bearer [REDACTED]"},
	}
	reports := withExport(t, f, items...)

	step := stepNamed(t, f.pass1(t), StepTrackerSecrets)
	if step.Outcome != runstate.StepRan || !strings.HasPrefix(step.Detail, "THE TRACKER'S EXPORT HOLDS 2 WHOLE KEYS OR TOKENS: calc-1 (Anthropic key or token), calc-2 (JSON web token)") {
		t.Fatalf("secrets step = %+v, want the two items named with their kinds", step)
	}
	filed := listReports(t, reports)
	if len(filed) != 2 {
		t.Fatalf("filed %d reports, want 2: %+v", len(filed), filed)
	}
	for index, want := range []string{"Anthropic key or token in work item calc-1", "JSON web token in work item calc-2"} {
		got := filed[index]
		if got.Severity != report.SeverityCritical || !strings.HasPrefix(got.Message, "credential: ") || !strings.Contains(got.Message, want) {
			t.Errorf("report %d = %+v, want a critical credential report naming %q", index, got, want)
		}
		if !strings.HasPrefix(got.RunID, secretRunPrefix) {
			t.Errorf("report %d run id = %q, want it to begin %q", index, got.RunID, secretRunPrefix)
		}
	}
	for _, secret := range []string{sampleAnthropicKey, sampleWebToken} {
		for _, part := range []string{secret, secret[:24]} {
			if strings.Contains(step.Detail, part) {
				t.Errorf("the step's detail carries a secret's value")
			}
			for _, got := range filed {
				if strings.Contains(got.Message, part) || strings.Contains(got.RunID, part) {
					t.Errorf("a report carries a secret's value")
				}
			}
		}
	}

	// The next pass finds the same and reports nothing again.
	f.now = f.now.Add(11 * time.Minute)
	step = stepNamed(t, f.pass1(t), StepTrackerSecrets)
	if !strings.HasSuffix(step.Detail, "; each was already reported") {
		t.Fatalf("second pass = %+v, want each already reported", step)
	}
	if got := len(listReports(t, reports)); got != 2 {
		t.Fatalf("filed %d reports after the second pass, want still 2", got)
	}

	// Nor does a restarted supervisor, which knows the earlier reports only from
	// the pile; a new value is reported, and only it.
	restarted := newFixture(t)
	restarted.now = f.now.Add(11 * time.Minute)
	restarted.pass.Repository, restarted.pass.Reports, restarted.pass.RepositoryID = f.pass.Repository, reports, "calc"
	items[3].Notes += "\ngh auth: " + sampleGitHubToken
	writeExport(t, restarted, items...)
	step = stepNamed(t, restarted.pass1(t), StepTrackerSecrets)
	if !strings.Contains(step.Detail, "calc-4 (GitHub token)") || !strings.HasSuffix(step.Detail, "; 1 reported now, the rest already reported") {
		t.Fatalf("restarted pass = %+v, want the one new token reported", step)
	}
	filed = listReports(t, reports)
	if len(filed) != 3 || !strings.Contains(filed[2].Message, "GitHub token in work item calc-4") {
		t.Fatalf("filed %+v, want the GitHub token reported third", filed)
	}
}

// An export holding no whole secret is said so, and nobody is told anything.
func TestAnExportWithOnlyPrefixesHoldsNoSecret(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	reports := withExport(t, f,
		exportLine{ID: "calc-1", Title: "redact by shape", Notes: "sk-" + "ant-" + " sk-" + "proj-" + " ey" + "J" + " Bear" + "er; a fragment " + sampleAnthropicKey[:28]},
	)
	step := stepNamed(t, f.pass1(t), StepTrackerSecrets)
	if step.Outcome != runstate.StepRan || step.Detail != "the tracker's export holds no whole key or token" {
		t.Fatalf("secrets step = %+v, want no secret found", step)
	}
	if got := len(listReports(t, reports)); got != 0 {
		t.Fatalf("filed %d reports, want none", got)
	}
}

// With no export to read, the step says so and is not a failure.
func TestNoExportIsASkip(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.pass.Repository = t.TempDir()
	step := stepNamed(t, f.pass1(t), StepTrackerSecrets)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "there is no tracker export") {
		t.Fatalf("secrets step = %+v, want a skip", step)
	}
}
