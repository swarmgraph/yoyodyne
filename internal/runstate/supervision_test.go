package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

func newSupervisionStoreAt(t *testing.T, root string) *SupervisionStore {
	t.Helper()
	store, err := NewSupervisionStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSupervisionStore() error = %v", err)
	}
	return store
}

func recordedSupervision() Supervision {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	return Supervision{
		PID:        4242,
		Build:      "abc1234",
		StartedAt:  at,
		ObservedAt: at.Add(time.Minute),
		Children: []SupervisedChild{
			{Service: config.ServiceSlack, State: ChildRunning, PID: 77, Log: "/state/sink.log", StartedAt: at, Starts: 1},
			{Service: config.ServiceDashboard, State: ChildNotYet, Reason: "its adoption is yoyodyne-ifd.414"},
			{Service: config.ServiceScheduler, State: ChildDegraded, Reason: "died 6 times", Failures: 6, DiedAt: at.Add(time.Minute)},
			{Service: config.ServiceMaintenance, State: ChildOff},
		},
	}
}

// The record is written by the supervisor and read by the verbs and the
// surfaces, so it has to come back exactly as it went in, and it has to be
// absent rather than an error on a product nothing has ever supervised.
func TestTheSupervisionRecordSurvivesTheSupervisorThatWroteIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionStoreAt(t, root)
	if _, found, err := store.Load(); err != nil || found {
		t.Fatalf("Load() = %t, %v on a fresh root, want no record and no error", found, err)
	}
	if err := store.Save(recordedSupervision()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v, want the record back", found, err)
	}
	if loaded.SchemaVersion != SupervisionSchemaVersion || loaded.ProductID != "yoyodyne" || loaded.PID != 4242 {
		t.Errorf("Load() = %+v, want the schema, the product, and the pid stamped", loaded)
	}
	if got, ok := loaded.Child(config.ServiceScheduler); !ok || got.State != ChildDegraded || got.Reason != "died 6 times" {
		t.Errorf("scheduler = %+v (%t), want the degraded child with its reason", got, ok)
	}
	if degraded := loaded.Degraded(); len(degraded) != 1 || degraded[0].Service != config.ServiceScheduler {
		t.Errorf("Degraded() = %v, want the one child", degraded)
	}
	if _, ok := loaded.Child("nothing"); ok {
		t.Error("Child() found a service the record does not carry")
	}
	if info, err := os.Stat(filepath.Join(store.Root(), supervisionFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("record is mode %v (%v), want it readable only by its owner", info.Mode(), err)
	}
}

// A record that is not a record is refused rather than read as one: a state
// the harness does not name, a child recorded twice, a supervisor with no
// process.
func TestASupervisionRecordThatIsNotOneIsRefused(t *testing.T) {
	t.Parallel()

	store := newSupervisionStoreAt(t, t.TempDir())
	for name, corrupt := range map[string]func(*Supervision){
		"no pid":        func(s *Supervision) { s.PID = 0 },
		"unknown state": func(s *Supervision) { s.Children[0].State = "asleep" },
		"twice":         func(s *Supervision) { s.Children[3].Service = config.ServiceSlack },
		"long reason":   func(s *Supervision) { s.Children[2].Reason = strings.Repeat("x", MaxChildReasonBytes+1) },
	} {
		recorded := recordedSupervision()
		corrupt(&recorded)
		if err := store.Save(recorded); err == nil {
			t.Errorf("Save() accepted a record with %s", name)
		}
	}
	other, err := NewSupervisionStore(store.base, "other")
	if err != nil {
		t.Fatalf("NewSupervisionStore() error = %v", err)
	}
	if err := store.Save(recordedSupervision()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.MkdirAll(other.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(store.Root(), supervisionFile), filepath.Join(other.Root(), supervisionFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Load(); err == nil || !strings.Contains(err.Error(), "belongs to product") {
		t.Errorf("Load() of another product's record error = %v, want it refused as the wrong product's", err)
	}
}

// Whether a supervisor is running is the lease's answer: held while a process
// holds it, free the moment it lets go, and asking does not take it from
// anybody.
func TestWhetherASupervisorIsRunningIsTheLeasesAnswer(t *testing.T) {
	t.Parallel()

	store := newSupervisionStoreAt(t, t.TempDir())
	if running, err := store.Running(); err != nil || running {
		t.Fatalf("Running() = %t, %v with nobody holding the lease, want false", running, err)
	}
	lease, held, err := store.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want it taken", held, err)
	}
	if running, err := store.Running(); err != nil || !running {
		t.Fatalf("Running() = %t, %v while the lease is held, want true", running, err)
	}
	if _, held, err := store.Lease(); err != nil || held {
		t.Fatalf("second Lease() = %t, %v, want it refused", held, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if running, err := store.Running(); err != nil || running {
		t.Fatalf("Running() = %t, %v after release, want false", running, err)
	}
}

// The supervisor's log is a root and a path within it, as every detached
// process's log is, so the launcher can confine the write.
func TestTheSupervisorLogIsNamedInsideTheStateRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionStoreAt(t, root)
	logRoot, relative := store.SupervisorLog()
	if logRoot != filepath.Clean(root) || relative != "projects/yoyodyne/state/supervisor/"+supervisorLogFile {
		t.Errorf("SupervisorLog() = %q, %q, want the state root and the path under it", logRoot, relative)
	}
	if store.Product() != "yoyodyne" {
		t.Errorf("Product() = %q", store.Product())
	}
}

// The watch's own lease answers whether a session holds it without stamping a
// holder, because the supervisor asking is not a session.
func TestTheWatchSaysWhetherItIsHeldWithoutTakingIt(t *testing.T) {
	t.Parallel()

	store, err := NewWatchStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	if held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v with no session, want false", held, err)
	}
	if _, found, err := store.Holder(); err != nil || found {
		t.Fatalf("Holder() = %t, %v after asking, want nothing stamped by the asking", found, err)
	}
	lease, held, err := store.Lease("watch-0123456789abcdef0123456789abcdef")
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the session to take it", held, err)
	}
	defer lease.Release()
	if held, err := store.Held(); err != nil || !held {
		t.Fatalf("Held() = %t, %v with a session holding it, want true", held, err)
	}
}
