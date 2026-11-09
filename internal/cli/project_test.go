package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"go.yaml.in/yaml/v3"
)

// boundProject is a checkout of the yoyodyne product configured in its own
// repository, fetching from the remote given.
func boundProject(t *testing.T, remote string) (project, configPath string) {
	t.Helper()
	project, configPath = stateRootProject(t)
	if remote != "" {
		git(t, project, "remote", "add", "origin", remote)
	}
	return project, configPath
}

func actsOf(t *testing.T, root, productID string) []projectAct {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(home.ProductDirectory(root, productID), projectActsFileName))
	if err != nil {
		t.Fatalf("read the project's acts: %v", err)
	}
	var acts []projectAct
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var act projectAct
		if err := json.Unmarshal([]byte(line), &act); err != nil {
			t.Fatalf("act %q: %v", line, err)
		}
		acts = append(acts, act)
	}
	return acts
}

// A second clone of a bound project refuses to start naming `yoyo project bind
// --replace`; bind refuses to take the project off a repository still there
// unless told to; and --replace moves it, records who did, and leaves the first
// clone the one that now refuses.
func TestASecondCloneIsRefusedAndBindReplaceMovesTheProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	first, firstConfig := boundProject(t, "git@github.com:example/yoyodyne.git")
	second, secondConfig := boundProject(t, "https://github.com/example/yoyodyne")

	if _, stderr, code := runCLI(t, "reports", "--config", firstConfig); code != 0 {
		t.Fatalf("first start code = %d, stderr = %q", code, stderr)
	}
	binding, found, err := home.ReadBinding(root, "yoyodyne")
	if err != nil || !found || binding.Repository != filepath.Join(first) && !strings.HasSuffix(binding.Repository, filepath.Base(first)) {
		t.Fatalf("the first start did not bind the project to %s: %+v, %v, %v", first, binding, found, err)
	}

	_, stderr, code := runCLI(t, "reports", "--config", secondConfig)
	if code == 0 || !strings.Contains(stderr, "second clone") || !strings.Contains(stderr, home.BindCommand+" --replace") {
		t.Fatalf("second clone start code = %d, stderr = %q; want the second-clone refusal naming bind --replace", code, stderr)
	}

	_, stderr, code = runCLI(t, "project", "bind", "--config", secondConfig)
	if code == 0 || !strings.Contains(stderr, "--replace") {
		t.Fatalf("bind over a present repository code = %d, stderr = %q; want a refusal naming --replace", code, stderr)
	}

	stdout, stderr, code := runCLI(t, "project", "bind", "--replace", "--config", secondConfig)
	if code != 0 || !strings.Contains(stdout, "in place of") {
		t.Fatalf("bind --replace code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	acts := actsOf(t, root, "yoyodyne")
	if len(acts) != 1 || acts[0].Act != "bind" || acts[0].From == "" || acts[0].By == "" {
		t.Fatalf("acts = %+v, want the bind recorded with what it moved from and who ran it", acts)
	}
	if _, stderr, code := runCLI(t, "reports", "--config", secondConfig); code != 0 {
		t.Fatalf("start from the newly bound clone code = %d, stderr = %q", code, stderr)
	}
	if _, stderr, code := runCLI(t, "reports", "--config", firstConfig); code == 0 || !strings.Contains(stderr, second) && !strings.Contains(stderr, filepath.Base(second)) {
		t.Fatalf("start from the unbound clone code = %d, stderr = %q; want a refusal naming the bound one", code, stderr)
	}
}

// Two products using one id refuse to start, naming the rename.
func TestTwoProductsSharingAnIdAreRefusedNamingRename(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	_, firstConfig := boundProject(t, "git@github.com:example/yoyodyne.git")
	_, otherConfig := boundProject(t, "git@github.com:somebody/else.git")
	if _, stderr, code := runCLI(t, "reports", "--config", firstConfig); code != 0 {
		t.Fatalf("first start code = %d, stderr = %q", code, stderr)
	}
	_, stderr, code := runCLI(t, "reports", "--config", otherConfig)
	if code == 0 || !strings.Contains(stderr, "two products share the id yoyodyne") || !strings.Contains(stderr, home.RenameCommand+" yoyodyne <new>") {
		t.Fatalf("shared id start code = %d, stderr = %q; want the shared-id refusal naming rename", code, stderr)
	}
}

// A bound repository that is gone refuses every start naming `yoyo project
// bind`, which binds the repository it is run from without --replace, because
// there is nothing left at the old path to point history away from.
func TestAMissingRepositoryIsRefusedAndBindRebindsIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	first, firstConfig := boundProject(t, "")
	moved, movedConfig := boundProject(t, "")
	if _, stderr, code := runCLI(t, "reports", "--config", firstConfig); code != 0 {
		t.Fatalf("first start code = %d, stderr = %q", code, stderr)
	}
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, "reports", "--config", movedConfig)
	if code == 0 || !strings.Contains(stderr, "no longer there") || !strings.Contains(stderr, "`"+home.BindCommand+"`") {
		t.Fatalf("start against a missing repository code = %d, stderr = %q; want the refusal naming bind", code, stderr)
	}
	stdout, _, code := runCLI(t, "project", "list")
	if code != 0 || !strings.Contains(stdout, "which is missing") {
		t.Fatalf("project list = %d, %q; want the missing repository named", code, stdout)
	}
	// --product names the id outright, for a repository whose configuration is
	// kept in the project directory and so cannot be found until it is bound.
	if stdout, stderr, code := runCLI(t, "project", "bind", "--product", "yoyodyne", "--directory", moved); code != 0 {
		t.Fatalf("bind from the moved clone code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, stderr, code := runCLI(t, "reports", "--config", movedConfig); code != 0 {
		t.Fatalf("start after the bind code = %d, stderr = %q", code, stderr)
	}
}

// Bind refuses while a run of the project is in flight: the run holds a
// worktree of the bound repository.
func TestBindAndRenameRefuseWhileARunIsInFlight(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	first, firstConfig := boundProject(t, "")
	_, movedConfig := boundProject(t, "")
	if _, stderr, code := runCLI(t, "reports", "--config", firstConfig); code != 0 {
		t.Fatalf("first start code = %d, stderr = %q", code, stderr)
	}
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: domain.ProductID("yoyodyne"),
		RepositoryID: "yoyodyne", WorkItemID: "yoyodyne-test", Backend: domain.BackendClaudeCode,
		Status: runstate.StatusRunning, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, "project", "bind", "--config", movedConfig); code == 0 || !strings.Contains(stderr, "in flight") || !strings.Contains(stderr, runID) {
		t.Fatalf("bind with a run in flight code = %d, stderr = %q; want a refusal naming the run", code, stderr)
	}
	if _, stderr, code := runCLI(t, "project", "rename", "yoyodyne", "renamed"); code == 0 || !strings.Contains(stderr, "in flight") {
		t.Fatalf("rename with a run in flight code = %d, stderr = %q; want a refusal", code, stderr)
	}
}

// A configuration kept in the project directory has its product.id rewritten
// by the rename, and is found from the repository under its new id afterwards.
func TestRenameMovesAProjectAndRewritesTheConfigurationItKeeps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	project := externalProject(t)
	git(t, project, "init", "-q", "-b", "main")
	if _, stderr, code := runCLI(t, "init", "--directory", project, "--external"); code != 0 {
		t.Fatalf("init --external code = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "their-project", "config.yaml")); err != nil {
		t.Fatalf("init --external wrote no configuration into the project directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "taken"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, "project", "rename", "their-project", "taken"); code == 0 || !strings.Contains(stderr, "taken") {
		t.Fatalf("rename onto a taken id code = %d, stderr = %q", code, stderr)
	}

	stdout, stderr, code := runCLI(t, "project", "rename", "their-project", "renamed")
	if code != 0 || !strings.Contains(stdout, "product.id") {
		t.Fatalf("rename code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "their-project")); !os.IsNotExist(err) {
		t.Fatalf("the old project directory is still there (%v)", err)
	}
	discovered, err := config.Discover(project)
	if err != nil || discovered != filepath.Join(root, "projects", "renamed", "config.yaml") {
		t.Fatalf("Discover() after the rename = %q, %v", discovered, err)
	}
	loaded, err := config.Load(discovered)
	if err != nil || loaded.Product.ID != "renamed" {
		t.Fatalf("the moved configuration reads product.id %q (%v), want renamed", loaded.Product.ID, err)
	}
	if acts := actsOf(t, root, "renamed"); len(acts) != 1 || acts[0].Act != "rename" || acts[0].From != "their-project" {
		t.Fatalf("acts = %+v, want the rename recorded", acts)
	}
	stdout, _, code = runCLI(t, "project", "list", "--json")
	var listed struct {
		Projects []struct {
			ID      string `json:"id"`
			Present bool   `json:"present"`
		} `json:"projects"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &listed) != nil {
		t.Fatalf("project list --json = %d, %q", code, stdout)
	}
	names := map[string]bool{}
	for _, project := range listed.Projects {
		names[project.ID] = project.Present
	}
	if !names["renamed"] || names["their-project"] {
		t.Fatalf("project list = %+v, want renamed present and their-project gone", listed.Projects)
	}
}

// A committed configuration is the repository's to change: the rename requires
// it to read the new id already, so the two never disagree.
func TestRenameRequiresACommittedConfigurationToReadTheNewIdFirst(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	_, configPath := boundProject(t, "")
	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("first start code = %d, stderr = %q", code, stderr)
	}
	_, stderr, code := runCLI(t, "project", "rename", "yoyodyne", "renamed")
	if code == 0 || !strings.Contains(stderr, "set it to renamed") {
		t.Fatalf("rename with the committed id unchanged code = %d, stderr = %q", code, stderr)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Replace(string(content), "id: yoyodyne", "id: renamed", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := runCLI(t, "project", "rename", "yoyodyne", "renamed"); code != 0 {
		t.Fatalf("rename code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("start under the new id code = %d, stderr = %q", code, stderr)
	}
}

// The earlier layout keeps records under products/, which a rename of the
// project directory would leave behind under the old id.
func TestRenameRefusesTheEarlierLayout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "products", "yoyodyne"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "yoyodyne"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, "project", "rename", "yoyodyne", "renamed"); code == 0 || !strings.Contains(stderr, "earlier builds") {
		t.Fatalf("rename in the earlier layout code = %d, stderr = %q", code, stderr)
	}
}

func TestRewriteProductIDTouchesOnlyTheProductsID(t *testing.T) {
	t.Parallel()

	source := "version: 1\n# the product\nproduct:\n  id: old   # named once\n  repository: /somewhere\nagents:\n  developer:\n    id: old\n"
	got, err := rewriteProductID([]byte(source), "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	want := "version: 1\n# the product\nproduct:\n  id: new   # named once\n  repository: /somewhere\nagents:\n  developer:\n    id: old\n"
	if string(got) != want {
		t.Fatalf("rewriteProductID() = %q, want %q", got, want)
	}
	if _, err := rewriteProductID([]byte("product:\n  id: other\n"), "old", "new"); err == nil {
		t.Fatal("rewriteProductID() rewrote an id that was not the one named")
	}
}

// externalClone is a Git repository with the remote given and no committed
// configuration, which is a clone of a project whose configuration is kept in
// the machine home.
func externalClone(t *testing.T, remote string) string {
	t.Helper()
	clone, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, clone, "init", "-b", "main")
	git(t, clone, "remote", "add", "origin", remote)
	return clone
}

func loadedRepository(t *testing.T, path string) string {
	t.Helper()
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", path, err)
	}
	return loaded.Product.Repository
}

func sameDirectory(t *testing.T, got, want string) {
	t.Helper()
	if !home.SameDirectory(got, want) {
		t.Errorf("directory = %q, want %q", got, want)
	}
}

// Rebinding a project whose configuration is kept in the machine home moves
// the configuration's product.repository with the binding, starts from the new
// clone, and takes the state-root marker off the old one.
func TestBindReplaceFollowsAKeptConfigurationAndReleasesTheOldClone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	const remote = "git@github.com:example/external.git"
	cloneA, cloneB := externalClone(t, remote), externalClone(t, remote)
	kept := filepath.Join(root, "projects", "external", config.FileName)

	if stdout, stderr, code := runCLI(t, "init", "--external", "--directory", cloneA, "--product", "external"); code != 0 {
		t.Fatalf("init code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	sameDirectory(t, loadedRepository(t, kept), cloneA)
	if _, stderr, code := runCLI(t, "reports", "--config", kept); code != 0 {
		t.Fatalf("start from A code = %d, stderr = %q", code, stderr)
	}
	marker, err := runstate.ReadRootMarker(cloneA)
	if err != nil || marker.Recorded == "" {
		t.Fatalf("A's marker = %+v, %v; want it recorded", marker, err)
	}

	stdout, stderr, code := runCLI(t, "project", "bind", "--product", "external", "--replace", "--directory", cloneB)
	if code != 0 {
		t.Fatalf("bind --replace code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, want := range []string{"product.repository", "now reads " + cloneB, "state-root marker"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to say %q", stdout, want)
		}
	}
	sameDirectory(t, loadedRepository(t, kept), cloneB)
	discovered, err := config.Discover(cloneB)
	if err != nil || !home.SameDirectory(discovered, kept) {
		t.Errorf("Discover(B) = %q, %v; want the kept configuration %q", discovered, err, kept)
	}

	if _, stderr, code := runCLI(t, "reports", "--config", kept); code != 0 {
		t.Fatalf("start from B code = %d, stderr = %q", code, stderr)
	}
	marker, err = runstate.ReadRootMarker(cloneB)
	if err != nil || marker.Recorded == "" || !home.SameDirectory(marker.Recorded, root) {
		t.Errorf("B's marker = %+v, %v; want it recording %s", marker, err, root)
	}
	marker, err = runstate.ReadRootMarker(cloneA)
	if err != nil || marker.Recorded != "" {
		t.Errorf("A's marker = %+v, %v; want it released", marker, err)
	}
	if _, err := os.Stat(filepath.Join(cloneA, ".git", "yoyodyne", "state-root.writer")); !os.IsNotExist(err) {
		t.Errorf("A's writer account still exists: %v", err)
	}
}

// A bind made before bind rewrote the configuration, or an edit by hand, leaves
// the binding right and product.repository naming another directory; binding
// the bound clone again puts the file right.
func TestBindRepairsAKeptConfigurationNamingAnotherDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", root)
	clone := externalClone(t, "git@github.com:example/external.git")
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(root, "projects", "external", config.FileName)
	if stdout, stderr, code := runCLI(t, "init", "--external", "--directory", clone, "--product", "external"); code != 0 {
		t.Fatalf("init code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	content, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(content), clone, elsewhere, 1)
	if edited == string(content) {
		t.Fatalf("the kept configuration does not contain %q:\n%s", clone, content)
	}
	if err := os.WriteFile(kept, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "project", "bind", "--product", "external", "--directory", clone)
	if code != 0 || !strings.Contains(stdout, "product.repository") || !strings.Contains(stdout, elsewhere) {
		t.Fatalf("bind code = %d, stdout = %q, stderr = %q; want it to say it rewrote product.repository", code, stdout, stderr)
	}
	sameDirectory(t, loadedRepository(t, kept), clone)

	// Run again, there is nothing left to put right.
	stdout, _, code = runCLI(t, "project", "bind", "--product", "external", "--directory", clone)
	if code != 0 || !strings.Contains(stdout, "nothing to bind") {
		t.Errorf("second bind code = %d, stdout = %q; want nothing to bind", code, stdout)
	}
}

func TestRewriteProductRepository(t *testing.T) {
	t.Parallel()

	t.Run("keeps comments and every other line byte for byte", func(t *testing.T) {
		t.Parallel()
		in := "# kept on this machine\nversion: 1\nproduct:\n  id: external # the id\n  repository: /old/clone   # where it is\n\n  other: x\nchecks:\n  - go test ./...\n"
		out, previous, err := rewriteProductRepository([]byte(in), "/new/clone")
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(in, "/old/clone", "/new/clone", 1)
		if string(out) != want || previous != "/old/clone" {
			t.Errorf("out = %q, previous = %q; want %q and /old/clone", out, previous, want)
		}
	})
	t.Run("replaces a quoted value", func(t *testing.T) {
		t.Parallel()
		in := "product:\n  id: x\n  repository: \"/old # clone\"\nz: 1\n"
		out, previous, err := rewriteProductRepository([]byte(in), "/new/clone")
		if err != nil {
			t.Fatal(err)
		}
		if got := string(out); strings.Contains(got, "old") || !strings.Contains(got, "/new/clone") || !strings.HasSuffix(got, "z: 1\n") {
			t.Errorf("out = %q, want the whole quoted value replaced", got)
		}
		if previous != "/old # clone" {
			t.Errorf("previous = %q, want the unquoted old value", previous)
		}
	})
	t.Run("quotes a path with spaces so it reads back the same", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{"/Users/a b/my clone", "/tmp/x: y", "/tmp/with # hash"} {
			out, _, err := rewriteProductRepository([]byte("product:\n  id: x\n  repository: /old\n"), path)
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Product struct {
					Repository string `yaml:"repository"`
				} `yaml:"product"`
			}
			if err := yaml.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("%q does not parse: %v", out, err)
			}
			if parsed.Product.Repository != path {
				t.Errorf("read back %q, want %q (file %q)", parsed.Product.Repository, path, out)
			}
		}
	})
	t.Run("errors without a product.repository", func(t *testing.T) {
		t.Parallel()
		for _, in := range []string{"product:\n  id: x\n", "version: 1\n", "- a\n- b\n"} {
			if _, _, err := rewriteProductRepository([]byte(in), "/new"); err == nil {
				t.Errorf("rewriteProductRepository(%q) = nil error, want one", in)
			}
		}
	})
}
