package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const intentConfigBody = `version: 1
extends: builtin:v1
product:
  id: calc
  repository: /somewhere/calc
intent:
  repository: %s
`

func writeIntentConfig(t *testing.T, path, repository string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(intentConfigBody, "%s", repository, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A configuration kept in the project's directory in the machine home names
// its companion intent repository relative to that directory, and every
// reader's root is that repository.
func TestIntentRepositoryResolvesAgainstTheProjectDirectory(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "projects", "calc")
	path := filepath.Join(directory, FileName)
	writeIntentConfig(t, path, "intent")

	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	want := filepath.Join(directory, "intent")
	if got := resolved.Config.Product.IntentRoot("/somewhere/calc"); got != want {
		t.Fatalf("IntentRoot() = %q, want %q", got, want)
	}
	if resolved.Origins["intent.repository"] == "" {
		t.Error("intent.repository has no recorded origin")
	}
	// The resolved path is machine-local and never part of what a revision digests.
	if strings.Contains(resolved.Config.Revision(), directory) {
		t.Error("the revision carries a machine-local path")
	}
}

// A project naming none reads its intent in its own repository.
func TestIntentRootIsTheProjectRepositoryByDefault(t *testing.T) {
	t.Parallel()
	if got := (Product{}).IntentRoot("/somewhere/calc"); got != "/somewhere/calc" {
		t.Fatalf("IntentRoot() = %q", got)
	}
}

// A configuration committed with the repository may not name one: choosing a
// companion intent repository is choosing to keep everything of Yoyodyne's out
// of the project's repository.
func TestIntentRepositoryInACommittedConfigurationIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "calc", DirectoryName, FileName)
	writeIntentConfig(t, path, "intent")
	_, err := LoadResolved(path)
	if err == nil || !strings.Contains(err.Error(), "intent.repository") {
		t.Fatalf("LoadResolved() error = %v, want the committed intent repository refused", err)
	}
}

func TestIntentRepositoryOutsideTheProjectDirectoryIsRefused(t *testing.T) {
	t.Parallel()
	for _, repository := range []string{"../elsewhere", "/abs/intent", "."} {
		path := filepath.Join(t.TempDir(), "projects", "calc", FileName)
		writeIntentConfig(t, path, repository)
		if _, err := LoadResolved(path); err == nil || !strings.Contains(err.Error(), "intent repository") {
			t.Errorf("repository %q: LoadResolved() error = %v, want it refused", repository, err)
		}
	}
}
