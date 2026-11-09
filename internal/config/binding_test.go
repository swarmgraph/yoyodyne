package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

func bindingTestRepository(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", directory, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return directory
}

// writeScaffoldAt writes a scaffold naming repository into directory, the way
// a kept configuration is laid out, and returns the configuration's path.
func writeScaffoldAt(t *testing.T, directory, productID, repository string) string {
	t.Helper()
	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: productID, Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range scaffold.Files() {
		target := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, file.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(directory, scaffold.Config.Path)
}

func keptBindingFixture(t *testing.T, configured, bound string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	const id = "kept"
	path = writeScaffoldAt(t, home.ProjectDirectory(root, id), id, configured)
	if _, err := home.Bind(home.AgreeOptions{Root: root, ProductID: id, Checkout: bound}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	return root, path
}

func TestLoadResolvedFollowsTheBindingOfAKeptConfiguration(t *testing.T) {
	t.Parallel()

	a, b := bindingTestRepository(t), bindingTestRepository(t)
	root, path := keptBindingFixture(t, a, b)

	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if !home.SameDirectory(resolved.Config.Product.Repository, b) {
		t.Errorf("Product.Repository = %q, want the bound %q", resolved.Config.Product.Repository, b)
	}
	if !home.SameDirectory(resolved.ConfiguredRepository, a) {
		t.Errorf("ConfiguredRepository = %q, want the file's %q", resolved.ConfiguredRepository, a)
	}
	if want := home.BindingPath(root, "kept"); resolved.BindingPath != want {
		t.Errorf("BindingPath = %q, want %q", resolved.BindingPath, want)
	}
	if origin := resolved.Origins["product.repository"]; !strings.HasPrefix(origin, "binding:") || !strings.HasSuffix(origin, resolved.BindingPath) {
		t.Errorf("origin of product.repository = %q, want binding: and the binding path", origin)
	}
}

func TestLoadResolvedLeavesAKeptConfigurationThatAgreesWithItsBinding(t *testing.T) {
	t.Parallel()

	a := bindingTestRepository(t)
	root, path := keptBindingFixture(t, a, a)

	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if !home.SameDirectory(resolved.Config.Product.Repository, a) {
		t.Errorf("Product.Repository = %q, want %q", resolved.Config.Product.Repository, a)
	}
	if resolved.ConfiguredRepository != "" {
		t.Errorf("ConfiguredRepository = %q, want empty", resolved.ConfiguredRepository)
	}
	if want := home.BindingPath(root, "kept"); resolved.BindingPath != want {
		t.Errorf("BindingPath = %q, want %q", resolved.BindingPath, want)
	}
	if origin := resolved.Origins["product.repository"]; strings.HasPrefix(origin, "binding:") {
		t.Errorf("origin of product.repository = %q, want the file's", origin)
	}
}

func TestLoadResolvedLeavesAConfigurationNotKeptInAProjectDirectoryAlone(t *testing.T) {
	t.Parallel()

	a, b := bindingTestRepository(t), bindingTestRepository(t)
	root := t.TempDir()
	if _, err := home.Bind(home.AgreeOptions{Root: root, ProductID: "kept", Checkout: b}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	for name, directory := range map[string]string{
		"committed":         filepath.Join(a, ".yoyodyne"),
		"wrong project dir": filepath.Join(root, "projects", "other-name"),
		"not projects":      filepath.Join(root, "elsewhere", "kept"),
	} {
		path := writeScaffoldAt(t, directory, "kept", a)
		resolved, err := LoadResolved(path)
		if err != nil {
			t.Fatalf("%s: LoadResolved() error = %v", name, err)
		}
		if resolved.BindingPath != "" || resolved.ConfiguredRepository != "" {
			t.Errorf("%s: BindingPath = %q, ConfiguredRepository = %q, want both empty", name, resolved.BindingPath, resolved.ConfiguredRepository)
		}
		if !home.SameDirectory(resolved.Config.Product.Repository, a) {
			t.Errorf("%s: Product.Repository = %q, want the file's %q", name, resolved.Config.Product.Repository, a)
		}
	}
}
