package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

// `yoyo init --external --intent` keeps the project's intent in a companion
// repository in its directory in the machine home: the configuration names it,
// the repository is created there with the index at the door of each home, and
// the project's own repository is left exactly as it was.
func TestRunInitExternalIntentCreatesTheCompanionRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", home)
	project := externalProject(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--external", "--intent"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	for _, untouched := range []string{config.DirectoryName, "docs"} {
		if _, err := os.Stat(filepath.Join(project, untouched)); !os.IsNotExist(err) {
			t.Errorf("init --intent wrote %s into the repository", untouched)
		}
	}

	projectDirectory := filepath.Join(home, "projects", "their-project")
	intent := filepath.Join(projectDirectory, "intent")
	loaded, err := config.Load(filepath.Join(projectDirectory, config.FileName))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Intent.Repository != "intent" || loaded.Product.IntentRepository != intent {
		t.Fatalf("intent = %#v, resolved %q, want %q", loaded.Intent, loaded.Product.IntentRepository, intent)
	}
	if _, err := os.Stat(filepath.Join(intent, ".git")); err != nil {
		t.Fatalf("the companion intent repository was not created: %v", err)
	}
	for _, index := range []string{"docs/product/README.md", "docs/designs/README.md", "docs/decisions/README.md", "docs/decisions/invariants/README.md"} {
		if _, err := os.Stat(filepath.Join(intent, filepath.FromSlash(index))); err != nil {
			t.Errorf("the companion intent repository has no %s: %v", index, err)
		}
	}
	// It says what the repository holds, in the project's own directories.
	if !strings.Contains(stdout.String(), "created the companion intent repository "+intent) ||
		!strings.Contains(stdout.String(), "the designs (docs/designs)") {
		t.Errorf("stdout = %q, want it to say where the intent repository is and what it holds", stdout.String())
	}
}

// A teammate clones the intent repository somebody already shares rather than
// creating an empty one.
func TestRunInitExternalIntentFromClonesTheCompanionRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", home)
	project := externalProject(t)
	shared := filepath.Join(t.TempDir(), "shared-intent")
	if err := os.MkdirAll(filepath.Join(shared, "docs", "product"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "docs", "product", "brief.md"), []byte("# Brief\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInCompanion(t, shared, "init", "-b", "main")
	gitInCompanion(t, shared, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "add", ".")
	gitInCompanion(t, shared, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "brief")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--external", "--intent-from", shared}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	cloned := filepath.Join(home, "projects", "their-project", "intent", "docs", "product", "brief.md")
	if _, err := os.Stat(cloned); err != nil {
		t.Fatalf("the shared intent repository was not cloned: %v; stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cloned the companion intent repository from "+shared) {
		t.Errorf("stdout = %q, want it to say what was cloned", stdout.String())
	}
}

// A companion intent repository exists to keep everything of Yoyodyne's out of
// the project's repository, so one chosen with a configuration committed there
// is refused with nothing written.
func TestRunInitIntentWithoutExternalIsRefused(t *testing.T) {
	project := externalProject(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--intent"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--external") {
		t.Errorf("stderr = %q, want it to name --external", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, config.DirectoryName)); !os.IsNotExist(err) {
		t.Error("a refused init wrote a configuration into the repository")
	}
}

func gitInCompanion(t *testing.T, directory string, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

// An approval in a project that keeps its intent in a companion repository
// lands there, and the command says so: it names that repository, the path
// inside it, and does not claim a run will refuse to start over it, since no
// run starts from that repository.
func TestApprovingInACompanionIntentProjectNamesTheCompanionRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := filepath.Join(root, "calc")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDirectory := filepath.Join(root, "home", "projects", "calc")
	intent := filepath.Join(projectDirectory, "intent")
	writeArtifact(t, intent, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, intent, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"}))
	configPath := filepath.Join(projectDirectory, config.FileName)
	writeArtifact(t, projectDirectory, config.FileName, "version: 1\nextends: builtin:v1\nproduct:\n  id: calc\n  repository: "+project+"\nintent:\n  repository: intent\n")

	stdout, stderr, code := runCLI(t, "artifact", "approve", "--config", configPath, "brief",
		"--reason", "approved by the operator in conversation")
	if code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	want := artifact.PendingCommitInCompanion(intent, "docs/product/brief.md")
	if !strings.Contains(stdout, want) {
		t.Fatalf("approve stdout = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, "refuses to start") || strings.Contains(stdout, project+",") {
		t.Fatalf("approve stdout = %q claims the project's checkout holds the write", stdout)
	}
	approved, err := os.ReadFile(filepath.Join(intent, "docs", "product", "brief.md"))
	if err != nil || !strings.Contains(string(approved), "approved by the operator in conversation") {
		t.Fatalf("the approval did not land in the companion repository: %v", err)
	}

	stdout, stderr, code = runCLI(t, "artifact", "approve", "--config", configPath, "--json", "v1-goals",
		"--reason", "approved with the adoption goal added")
	if code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	var written struct {
		PendingCommit string `json:"pending_commit"`
	}
	if err := json.Unmarshal([]byte(stdout), &written); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if written.PendingCommit != artifact.PendingCommitInCompanion(intent, "docs/product/goals/v1-goals.md") {
		t.Fatalf("pending_commit = %q", written.PendingCommit)
	}
}
