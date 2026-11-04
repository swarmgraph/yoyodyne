package runstate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

// A product secret that is not there is created readable and writable by its
// owner alone, under the product's own directory, which is made if need be.
func TestCreateProductSecretCreatesAMissingFileForItsOwnerAlone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path, created, err := CreateProductSecret(root, "yoyodyne", "dashboard.mason.token", []byte("secret\n"))
	if err != nil || !created {
		t.Fatalf("CreateProductSecret() = %v, %v", created, err)
	}
	if want := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "dashboard.mason.token"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
	if contents, _ := os.ReadFile(path); string(contents) != "secret\n" {
		t.Fatalf("contents = %q", contents)
	}
}

// A file already there is never replaced, whatever it holds.
func TestCreateProductSecretLeavesAnExistingFileAsItIs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "dashboard.mason.token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, created, err := CreateProductSecret(root, "yoyodyne", "dashboard.mason.token", []byte("new"))
	if err != nil || created || got != path {
		t.Fatalf("CreateProductSecret() = %q, %v, %v", got, created, err)
	}
	contents, _ := os.ReadFile(path)
	if !bytes.Equal(contents, []byte("kept")) {
		t.Fatalf("contents = %q, want them unchanged", contents)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Fatalf("permissions changed to %o", info.Mode().Perm())
	}
}

// A name that would reach outside the product's directory is refused.
func TestCreateProductSecretRefusesANameWithADirectory(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".", "..", "../escape", "a/b"} {
		if _, _, err := CreateProductSecret(t.TempDir(), "yoyodyne", name, []byte("x")); err == nil {
			t.Fatalf("CreateProductSecret(%q) was not refused", name)
		}
	}
}
