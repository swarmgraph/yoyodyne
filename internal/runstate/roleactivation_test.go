package runstate

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestOnlyTheCommandLineRecordsARoleActivation(t *testing.T) {
	t.Parallel()
	found := make(map[string]bool)
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "RecordRoleActivation(") {
			relative, err := filepath.Rel("../..", path)
			if err != nil {
				return err
			}
			found[filepath.ToSlash(relative)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || !found["internal/runstate/roleactivation.go"] || !found["internal/cli/role.go"] {
		t.Fatalf("role activation writers = %v, want only the store and the person's CLI verb", found)
	}
}

func TestRoleActivationHistoryRetainsConcurrentActivations(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "missing", "state")
	store := roleActivationStoreForTest(t, root)
	if history, err := store.History(); err != nil || len(history) != 0 {
		t.Fatalf("empty history = %v, %v", history, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("history created its root: %v", err)
	}
	const count = 12
	var writers sync.WaitGroup
	for range count {
		writers.Go(func() {
			if _, err := store.RecordRoleActivation("specialist", strings.Repeat("a", 64), "/project/roles/specialist.yaml", "Ada"); err != nil {
				t.Error(err)
			}
		})
	}
	writers.Wait()
	reopened := roleActivationStoreForTest(t, root)
	history, err := reopened.History()
	if err != nil || len(history) != count {
		t.Fatalf("history = %v, %v", history, err)
	}
	seen := make(map[string]bool)
	for index, activation := range history {
		if seen[activation.ID] || activation.Person != "Ada" || activation.ProductID != domain.ProductID("example") {
			t.Fatalf("activation = %#v", activation)
		}
		seen[activation.ID] = true
		if index > 0 && activation.ActivatedAt.After(history[index-1].ActivatedAt) {
			t.Fatal("history is not newest first")
		}
		info, err := os.Stat(filepath.Join(store.Root(), activation.ID+".json"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("activation permissions = %v, %v", info, err)
		}
	}
}

func TestRoleActivationHistoryToleratesFutureFieldsAndRefusesCorruptRecords(t *testing.T) {
	t.Parallel()
	store := roleActivationStoreForTest(t, t.TempDir())
	activation, err := store.RecordRoleActivation("specialist", strings.Repeat("a", 64), "/project/roles/specialist.yaml", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Root(), activation.ID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(encoded, &record); err != nil {
		t.Fatal(err)
	}
	record["future_field"] = true
	encoded, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if history, err := store.History(); err != nil || len(history) != 1 || history[0].ID != activation.ID {
		t.Fatalf("future field history = %v, %v", history, err)
	}
	for _, corrupt := range []string{"{", strings.Replace(string(encoded), `"example"`, `"another"`, 1), strings.Replace(string(encoded), activation.Digest, "bad-digest", 1)} {
		if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
			t.Fatal(err)
		}
		if history, err := store.History(); err == nil {
			t.Fatalf("corrupt history was reported as readable: %#v", history)
		}
	}
}

func TestRoleActivationWriterRefusesEscapingSymlinksAndRootReplacement(t *testing.T) {
	t.Parallel()
	for _, component := range []string{"projects", "projects/example", "projects/example/state", "projects/example/state/role-activations", "state-root"} {
		t.Run(component, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			store := roleActivationStoreForTest(t, root)
			if component == "state-root" {
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(root + "-old") })
				if err := os.Symlink(outside, root); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(root, component)
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.RecordRoleActivation("specialist", strings.Repeat("a", 64), "/project/roles/specialist.yaml", "Ada"); err == nil {
				t.Fatal("activation escaped its state root")
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatalf("activation wrote outside the state root: %v, %v", entries, err)
			}
		})
	}
}

func TestRoleActivationRefusesBadRecordsBeforeCreatingState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := roleActivationStoreForTest(t, root)
	for _, record := range []struct{ name, digest, source, person string }{
		{"../specialist", strings.Repeat("a", 64), "/project/role.yaml", "Ada"},
		{"specialist", "bad", "/project/role.yaml", "Ada"},
		{"specialist", strings.Repeat("a", 64), "role.yaml", "Ada"},
		{"specialist", strings.Repeat("a", 64), "/project/role.yaml", " "},
	} {
		if _, err := store.RecordRoleActivation(record.name, record.digest, record.source, record.person); err == nil {
			t.Fatalf("invalid activation accepted: %#v", record)
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("invalid activation created state: %v, %v", entries, err)
	}
}

func roleActivationStoreForTest(t *testing.T, root string) *RoleActivationStore {
	t.Helper()
	store, err := NewRoleActivationStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
