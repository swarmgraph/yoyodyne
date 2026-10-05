package runstate

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

func TestConfigReaderMismatchesNameTheRunningPartsThatCannotReadTheFile(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte("agents:\n  developer:\n    role: developer\n    effort: medium\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewConfigReaderStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	// The dashboard and the Slack sink both run a build from before the key;
	// the sink's process has gone, so it says nothing. The scheduler runs this
	// build and reads everything. A second dashboard starts on that current
	// schema without replacing the first dashboard's older record.
	alive := map[int]bool{101: true, 102: false, 103: true, 104: true}
	store = store.WithProcessCheck(func(pid int) (bool, error) { return alive[pid], nil })
	older := slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
	started := time.Date(2026, 9, 26, 23, 36, 0, 0, time.UTC)
	for _, reader := range []ConfigReader{
		{Service: "dashboard", PID: 101, Build: "0123456789abcdef", ConfigPath: configPath, StartedAt: started, Keys: older},
		{Service: "slack", PID: 102, Build: "0123456789abcdef", ConfigPath: configPath, StartedAt: started, Keys: older},
		{Service: "dashboard", PID: 104, Build: "fedcba9876543210", ConfigPath: configPath, StartedAt: started, Keys: config.SchemaKeys()},
		{Service: "scheduler", PID: 103, Build: "fedcba9876543210", ConfigPath: configPath, StartedAt: started, Keys: config.SchemaKeys()},
	} {
		if err := store.Record(reader); err != nil {
			t.Fatalf("Record(%s) error = %v", reader.Service, err)
		}
	}

	if readers, err := store.Running(); err != nil || len(readers) != 3 {
		t.Fatalf("Running() = %+v, %v, want both dashboards and the scheduler", readers, err)
	}
	// Re-recording one instance is idempotent, not another live instance.
	reader := ConfigReader{Service: "dashboard", PID: 104, Build: "fedcba9876543210", ConfigPath: configPath, StartedAt: started, Keys: config.SchemaKeys()}
	if err := store.Record(reader); err != nil {
		t.Fatal(err)
	}
	if readers, err := store.Running(); err != nil || len(readers) != 3 {
		t.Fatalf("repeated Record duplicated an instance: %+v, %v", readers, err)
	}

	mismatches, err := store.Mismatches()
	if err != nil {
		t.Fatalf("Mismatches() error = %v", err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("Mismatches() = %+v, want the dashboard alone", mismatches)
	}
	got := mismatches[0]
	if got.Service != "dashboard" || got.Build != "0123456789abcdef" || got.PID != 101 || !reflect.DeepEqual(got.Keys, []string{"agents.developer.effort"}) {
		t.Fatalf("Mismatches()[0] = %+v", got)
	}
	says := got.Says()
	for _, want := range []string{"the dashboard service", "build 0123456789ab", "pid 101", "agents.developer.effort", configPath} {
		if !strings.Contains(says, want) {
			t.Errorf("Says() = %q, want it to name %q", says, want)
		}
	}

	// A template-only key still names the old build, even when the active file
	// has no such key. Current builds and exited parts remain absent.
	if err := os.WriteFile(configPath, []byte("agents: {developer: {role: developer}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if active, err := store.Mismatches(); err != nil || len(active) != 0 {
		t.Fatalf("healthy active file = %+v, %v", active, err)
	}
	prospective, err := store.TemplateMismatches("internal/config/builtin/v1/bundle.yaml", []string{"agents.*.effort"})
	if err != nil || len(prospective) != 1 || prospective[0].Service != "dashboard" {
		t.Fatalf("TemplateMismatches() = %+v, %v, want the dashboard alone", prospective, err)
	}
	for _, want := range []string{"build 0123456789ab", "agents.*.effort", "adopting those keys", "would make"} {
		if !strings.Contains(prospective[0].Says(), want) {
			t.Errorf("prospective finding %q lacks %q", prospective[0].Says(), want)
		}
	}
	if strings.Contains(prospective[0].Says(), "every read it makes of the configuration fails") {
		t.Fatal("prospective finding claims the healthy active file is already broken")
	}
}

func TestConfigReaderRestartSupersedesThePreviousBuildForTheSameProcess(t *testing.T) {
	for _, service := range []string{"scheduler", ConfigReaderSupervisor} {
		t.Run(service, func(t *testing.T) {
			store, err := NewConfigReaderStore(t.TempDir(), "example")
			if err != nil {
				t.Fatal(err)
			}
			store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
			older := configReaderRecordFixture()
			older.Service = service
			older.Build = "0123456789abcdef"
			older.Keys = slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
			if err := store.Record(older); err != nil {
				t.Fatal(err)
			}
			// A separate live process must remain visible after this one execs.
			other := older
			other.PID++
			if err := store.Record(other); err != nil {
				t.Fatal(err)
			}
			restarted := older
			restarted.StartedAt = older.StartedAt.Add(time.Minute)
			restarted.Build = "fedcba9876543210"
			restarted.Keys = config.SchemaKeys()
			if err := store.Record(restarted); err != nil {
				t.Fatal(err)
			}
			// Writing the older account again must not undo the later startup.
			if err := store.Record(older); err != nil {
				t.Fatal(err)
			}
			restarted.recordPath = filepath.Join(store.root, restarted.InstanceID()+".json")
			other.recordPath = filepath.Join(store.root, other.InstanceID()+".json")
			readers, err := store.Running()
			if err != nil || !reflect.DeepEqual(readers, []ConfigReader{restarted, other}) {
				t.Fatalf("Running() = %+v, %v, want the restarted build and the other process", readers, err)
			}
			active, err := store.MismatchesIn(func(string) ([]byte, error) {
				return []byte("agents: {developer: {role: developer, effort: medium}}\n"), nil
			})
			if err != nil || len(active) != 1 || active[0].PID != other.PID || active[0].Build != other.Build {
				t.Fatalf("MismatchesIn() = %+v, %v, want only the other process's older build", active, err)
			}
			prospective, err := store.TemplateMismatches("internal/config/builtin/v1/bundle.yaml", []string{"agents.*.effort"})
			if err != nil || len(prospective) != 1 || prospective[0].PID != other.PID || prospective[0].Build != other.Build {
				t.Fatalf("TemplateMismatches() = %+v, %v, want only the other process's older build", prospective, err)
			}
		})
	}
}

func TestConfigReaderRefusesAPartTheProductDoesNotHave(t *testing.T) {
	store, err := NewConfigReaderStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	err = store.Record(ConfigReader{Service: "printer", PID: 1, ConfigPath: "/x/config.yaml", StartedAt: time.Now(), Keys: []string{"version"}})
	if err == nil || !strings.Contains(err.Error(), "printer") {
		t.Fatalf("Record() error = %v, want a refusal naming the part", err)
	}
}

func TestConfigReaderWritesStayInsideTheStateRoot(t *testing.T) {
	writertest.Run(t, writertest.Writer{
		Name: "configuration reader", Directory: "products/example/config-readers", File: configReaderRecordFixture().InstanceID() + ".json",
		Write: func(t *testing.T, root string) error {
			store, err := NewConfigReaderStore(root, "example")
			if err != nil {
				return err
			}
			return store.Record(configReaderRecordFixture())
		},
	})
}

func TestConfigReaderRefusesAReplacedWriteRoot(t *testing.T) {
	for _, replacement := range []string{"symlink", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			base := t.TempDir()
			statePath := filepath.Join(base, "state")
			outside := filepath.Join(base, "outside")
			for _, path := range []string{statePath, outside} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			store, err := NewConfigReaderStore(statePath, "example")
			if err != nil {
				t.Fatal(err)
			}
			pinned, err := store.pinWriteRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer pinned.Close()
			moved := filepath.Join(base, "moved")
			if err := os.Rename(statePath, moved); err != nil {
				t.Fatal(err)
			}
			if replacement == "symlink" {
				if err := os.Symlink(outside, statePath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(statePath, 0o700); err != nil {
				t.Fatal(err)
			}
			// This is the second half of Record, after its directory was pinned
			// and before writing. A check-to-use replacement must be refused.
			if err := store.recordIn(pinned, configReaderRecordFixture()); err == nil {
				t.Fatal("a replaced state root was accepted")
			}
			for _, path := range []string{outside, moved, statePath} {
				if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
					t.Fatalf("records appeared after a refused replacement in %s: %v, %v", path, entries, err)
				}
			}
			if replacement == "symlink" {
				if err := store.Record(configReaderRecordFixture()); err == nil {
					t.Fatal("Record followed a replacement symlink at its declared root")
				}
				if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
					t.Fatalf("Record escaped into %s: %v, %v", outside, entries, err)
				}
			}
		})
	}
}

func TestConfigReaderCreatesAMissingStateRootThroughTheConfinedWriter(t *testing.T) {
	store, err := NewConfigReaderStore(filepath.Join(t.TempDir(), "new", "state"), "example")
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
	if err := store.Record(configReaderRecordFixture()); err != nil {
		t.Fatal(err)
	}
	if readers, err := store.Running(); err != nil || len(readers) != 1 {
		t.Fatalf("newly created record = %+v, %v", readers, err)
	}
	info, err := os.Stat(filepath.Join(store.root, configReaderRecordFixture().InstanceID()+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record permissions: %v, %v", info, err)
	}
}

func configReaderRecordFixture() ConfigReader {
	return ConfigReader{
		SchemaVersion: ConfigReaderSchemaVersion, ProductID: "example",
		Service: "dashboard", PID: 4242, ConfigPath: "/example/config.yaml",
		StartedAt: time.Date(2026, 9, 26, 23, 36, 0, 0, time.UTC), Keys: []string{"version"},
	}
}

func TestConfigReaderKeepsLegacyRecordsWithoutDuplicatingAnInstance(t *testing.T) {
	t.Parallel()
	store, err := NewConfigReaderStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
	first := configReaderRecordFixture()
	if err := store.Record(first); err != nil {
		t.Fatal(err)
	}
	// A pre-existing service-only record remains readable by the new build.
	instancePath := filepath.Join(store.root, first.InstanceID()+".json")
	legacyPath := filepath.Join(store.root, first.Service+".json")
	if err := os.Rename(instancePath, legacyPath); err != nil {
		t.Fatal(err)
	}
	second := first
	second.PID++
	if err := store.Record(second); err != nil {
		t.Fatal(err)
	}
	if readers, err := store.Running(); err != nil || len(readers) != 2 {
		t.Fatalf("legacy instance was hidden: %+v, %v", readers, err)
	}
	if err := store.Record(first); err != nil {
		t.Fatal(err)
	}
	if readers, err := store.Running(); err != nil || len(readers) != 2 {
		t.Fatalf("one instance counted twice: %+v, %v", readers, err)
	}
	restarted := first
	restarted.StartedAt = first.StartedAt.Add(time.Minute)
	restarted.Build = "fedcba9876543210"
	if err := store.Record(restarted); err != nil {
		t.Fatal(err)
	}
	restarted.recordPath = filepath.Join(store.root, restarted.InstanceID()+".json")
	second.recordPath = filepath.Join(store.root, second.InstanceID()+".json")
	if readers, err := store.Running(); err != nil || !reflect.DeepEqual(readers, []ConfigReader{restarted, second}) {
		t.Fatalf("legacy startup was not superseded: %+v, %v", readers, err)
	}
}

func TestAStaleConfigurationRecordDoesNotHideOtherParts(t *testing.T) {
	root := t.TempDir()
	store, err := NewConfigReaderStore(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("agents: {developer: {effort: medium}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := ConfigReader{Service: ConfigReaderSupervisor, PID: 101, ConfigPath: filepath.Join(root, "gone.yaml"), StartedAt: time.Now(), Keys: []string{"version"}}
	for _, reader := range []ConfigReader{stale, {Service: "dashboard", PID: 102, ConfigPath: path, StartedAt: time.Now(), Keys: []string{"version"}}} {
		if err := store.Record(reader); err != nil {
			t.Fatal(err)
		}
	}
	legacy := filepath.Join(store.root, "supervisor.json")
	if err := os.Rename(filepath.Join(store.root, stale.InstanceID()+".json"), legacy); err != nil {
		t.Fatal(err)
	}
	mismatches, err := store.Mismatches()
	if err == nil {
		t.Fatal("missing stale record diagnostic")
	}
	for _, want := range []string{"stale configuration reader record", legacy, stale.ConfigPath, "removed this stale record automatically", "other parts were still checked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v does not name %q", err, want)
		}
	}
	if len(mismatches) != 1 || mismatches[0].Service != "dashboard" {
		t.Fatalf("other parts not checked: %+v", mismatches)
	}
}

func TestStaleConfigurationCleanupPreservesAChangedRecord(t *testing.T) {
	store, err := NewConfigReaderStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
	old := ConfigReader{Service: ConfigReaderSupervisor, PID: 101, ConfigPath: filepath.Join(store.stateRoot, "gone.yaml"), StartedAt: time.Now(), Keys: []string{"version"}}
	if err := store.Record(old); err != nil {
		t.Fatal(err)
	}
	readers, err := store.Running()
	if err != nil || len(readers) != 1 {
		t.Fatalf("readers: %+v, %v", readers, err)
	}
	current := old
	current.ConfigPath = filepath.Join(store.stateRoot, "current.yaml")
	if err := os.WriteFile(current.ConfigPath, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(current); err != nil {
		t.Fatal(err)
	}
	if err := store.removeStale(readers[0]); err == nil || !strings.Contains(err.Error(), "record changed") {
		t.Fatalf("cleanup did not protect new startup: %v", err)
	}
	readers, err = store.Running()
	if err != nil || len(readers) != 1 || readers[0].ConfigPath != current.ConfigPath {
		t.Fatalf("current startup lost: %+v, %v", readers, err)
	}
}
