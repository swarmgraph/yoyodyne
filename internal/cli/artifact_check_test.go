package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
)

// writeHomeIndexes puts the index `yoyo setup` writes at the door of every home
// the configuration names. Every fixture that expects a clean check needs them,
// because the check reads them: an index is a file inside a home, so it is
// refused in a developer's diff exactly as the documents beside it are, and a
// home without one is a defect this command answers for.
func writeHomeIndexes(t *testing.T, configPath string) {
	t.Helper()
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	project := filepath.Dir(configPath)
	for _, home := range artifacthome.Homes(resolved.Config) {
		writeArtifact(t, project, home.Path(), string(home.README()))
	}
}

// briefStatingOneGoal is the root of the chain the fixtures below hang off. The
// brief states its goals as bolded claims, which is the name a goal downstream
// reaches upward to.
const briefStatingOneGoal = "\n## Goals\n\n- **Intent in, software out.** What the product is for.\n"

// oneGoalOnOneLine is a goals document with nothing wrong with it: one goal, on
// one physical line, naming a claim the brief above states.
const oneGoalOnOneLine = "\n## Goals\n\n- A goal written on one line.\n  *Supports: Intent in, software out.*\n"

// oneGoalWrappedOntoTwoLines is the defect the whole arrangement is named for.
// It is invisible to a reader and changes what the recorded goal is.
const oneGoalWrappedOntoTwoLines = "\n## Goals\n\n- A goal somebody hard-wrapped\n  across two physical lines.\n" +
	"  *Supports: Intent in, software out.*\n"

func TestCheckIsSilentAndGreenWhenTheGovernedDocumentsAreWellFormed(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalOnOneLine)
	writeArtifact(t, project, "docs/designs/v1-harness.md",
		artifactDocument("v1-harness", "design", "V1 harness design", []string{"v1-goals"}))
	writeHomeIndexes(t, configPath)

	stdout, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 0 {
		t.Fatalf("check code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// A clean report says where it looked. A check that had stopped reading
	// anything would otherwise be indistinguishable from a clean repository,
	// which is the way this gate can pass while checking nothing. The
	// configuration directory is among them because a link written there is this
	// command's too: it is protected, and no artifact home claims it.
	for _, want := range []string{"well-formed", "docs/product", "docs/decisions/invariants", ".yoyodyne"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("check stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

// An index is the one file in a home that is not a governed document, and it is
// refused in a developer's diff exactly as the documents beside it are. The gate
// during a run escalates a home whose index has stopped answering, so this is
// where that fails.
func TestCheckFailsOnAnArtifactHomeWhoseIndexHasStoppedAnswering(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalOnOneLine)
	writeHomeIndexes(t, configPath)
	// One index replaced by prose that answers none of the three questions it
	// exists for.
	writeArtifact(t, project, "docs/designs/README.md", "# docs/designs\n\nSomebody wrote over this.\n")

	_, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 1 {
		t.Fatalf("check code = %d, want 1; stderr = %q", code, stderr)
	}
	for _, want := range []string{"docs/designs/README.md", "incomplete", "yoyo setup", "architect"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("check stderr never says %q:\n%s", want, stderr)
		}
	}
}

// The gate itself: the two defects the work behind this could not fail anywhere
// anybody could act, each in a document owned by a different role, both named
// with the role and the route.
func TestCheckFailsOnAGovernedDocumentDefectAndNamesItsOwner(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalWrappedOntoTwoLines)
	// And a design whose `supports` entry names a document nobody wrote, which is
	// the other half of the pair the item names.
	writeArtifact(t, project, "docs/designs/v1-harness.md",
		artifactDocument("v1-harness", "design", "V1 harness design", []string{"goals-that-moved"}))
	writeHomeIndexes(t, configPath)

	_, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 1 {
		t.Fatalf("check code = %d, want 1; stderr = %q", code, stderr)
	}
	for _, want := range []string{
		// The wrapped goal, whose owner is the Lead Product Manager.
		"docs/product/goals/v1-goals.md",
		"not written on one line",
		"Lead Product Manager owns docs/product/goals",
		"yoyo chat",
		// The dangling reference, whose owner is the architect.
		"docs/designs/v1-harness.md",
		"goals-that-moved",
		"architect",
		"yoyo agent chat architect",
		// And the way out of both, which is the amendment rather than the editor.
		"yoyo amendment list",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("check stderr never says %q:\n%s", want, stderr)
		}
	}
}

// The same findings structurally, so anything automating a repair reads them
// rather than parsing the prose above -- which is the discipline `yoyo doctor`
// already holds itself to for the same reason.
func TestCheckCarriesTheOwnerAndTheRouteInItsJSON(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalWrappedOntoTwoLines)
	writeHomeIndexes(t, configPath)

	stdout, stderr, code := runCLI(t, "artifact", "check", "--config", configPath, "--json")
	if code != 1 {
		t.Fatalf("check --json code = %d, want 1; stderr = %q", code, stderr)
	}
	var report struct {
		Defects []struct {
			Path   string `json:"path"`
			Detail string `json:"detail"`
			Home   string `json:"home"`
			Owner  string `json:"owner"`
			Route  string `json:"route"`
		} `json:"defects"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(report.Defects) == 0 {
		t.Fatalf("report = %#v, want the wrapped goal", report)
	}
	wrapped := report.Defects[0]
	if wrapped.Path != "docs/product/goals/v1-goals.md" || wrapped.Home != "docs/product/goals" {
		t.Fatalf("defect = %#v", wrapped)
	}
	if wrapped.Owner != "product-manager" || !strings.Contains(wrapped.Route, "yoyo chat") {
		t.Fatalf("defect = %#v, want the owner and the way to reach them", wrapped)
	}
}

// The class the escalation would otherwise leave with no gate at all: the doclink
// gate escalates a broken link written in a document no developer may edit, so
// this command has to be where it fails. Both kinds of such document are here —
// one inside an artifact home, one under the configuration directory, which is
// protected and which no home claims — because filtering by home rather than by
// what is protected would leave the second escalated and failed nowhere.
//
// And it has to stop there: a link written anywhere else is a developer's, and it
// already fails in the check that found it.
func TestCheckFailsOnABrokenLinkInEveryDocumentNoDeveloperMayEdit(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md",
		artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal+
			"\nSee [the harness design](../designs/never-written.md).\n")
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalOnOneLine)
	writeArtifact(t, project, ".yoyodyne/personas/developer.md",
		"# Developer\n\nSee [the design](../../docs/designs/never-written.md).\n")
	writeArtifact(t, project, "docs/notes.md", "# Notes\n\nSee [what happened](gone.md).\n")
	writeHomeIndexes(t, configPath)

	_, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 1 {
		t.Fatalf("check code = %d, want 1; stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"docs/product/brief.md",
		"a link resolves to nothing",
		"Lead Product Manager owns docs/product",
		"yoyo chat",
		// The configuration directory is no role's document to amend, so the route
		// says a change needs a grant to edit it and names who admits that work.
		".yoyodyne/personas/developer.md",
		"only where its work item grants it",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("check stderr never says %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "docs/notes.md") {
		t.Errorf("check answered for a broken link a developer may fix:\n%s", stderr)
	}
}

// An invariants home that could not be read is a gap in what was checked rather
// than a clean bill over it. This command says it reads the invariants, so
// reporting the documents it never opened as well-formed would be the one green
// nobody could act on.
func TestCheckReportsAnInvariantsHomeItCouldNotRead(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+briefStatingOneGoal)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+oneGoalOnOneLine)
	// The configured invariants home is a file rather than a directory, so nothing
	// in it can be read at all.
	writeArtifact(t, project, "docs/decisions/invariants", "this is not a directory\n")

	stdout, stderr, code := runCLI(t, "artifact", "check", "--config", configPath)
	if code != 1 {
		t.Fatalf("check code = %d, want 1; stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "well-formed") {
		t.Errorf("check reported a clean bill over an invariants home it could not read:\n%s", stdout)
	}
	if !strings.Contains(stderr, "docs/decisions/invariants") {
		t.Errorf("check does not name the home it could not read:\n%s", stderr)
	}
}

func TestCheckTakesNoPositionalArgument(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	if _, stderr, code := runCLI(t, "artifact", "check", "--config", configPath, "brief"); code != 2 {
		t.Fatalf("check with an id code = %d, want 2; stderr = %q", code, stderr)
	}
}
