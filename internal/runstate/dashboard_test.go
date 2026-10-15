package runstate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDashboardStore(t *testing.T) *DashboardStore {
	t.Helper()
	store, err := NewDashboardStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func dashboardInstance(t *testing.T, port int) DashboardInstance {
	t.Helper()
	startID, err := NewDashboardStartID()
	if err != nil {
		t.Fatal(err)
	}
	return DashboardInstance{StartID: startID, PID: os.Getpid(), Build: "abc", StartedAt: time.Now().UTC(), URL: "http://127.0.0.1:8765/", Port: port, Supervised: true}
}

// A dashboard is live for exactly as long as it holds its lease, and the
// record of one that let go is removed as it is found.
func TestADashboardIsLiveWhileItHoldsItsLease(t *testing.T) {
	t.Parallel()

	store := newDashboardStore(t)
	instance := dashboardInstance(t, 8765)
	lease, err := store.Register(instance)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	look, found, err := store.Instance(instance.StartID)
	if err != nil || !found || !look.Live || look.Instance.PID != instance.PID || look.Instance.Port != 8765 {
		t.Fatalf("Instance() = %+v, %v, %v, want the registered dashboard live", look, found, err)
	}
	if _, err := store.Register(instance); err == nil {
		t.Error("a second Register() of the same start took the lease the first holds")
	}
	instance.ReadyAt = time.Now().UTC()
	if err := store.Update(instance); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if look, _, _ := store.Instance(instance.StartID); look.Instance.ReadyAt.IsZero() {
		t.Error("Update() did not record the dashboard ready")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Instance(instance.StartID); err != nil || found {
		t.Errorf("Instance() of a dashboard that let its lease go = %v, %v, want it gone", found, err)
	}
	if _, err := os.Stat(filepath.Join(store.Directory(), dashboardInstancesFolder, instance.StartID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the record of a dashboard that is gone is still there: %v", err)
	}

	second := dashboardInstance(t, 8765)
	lease, err = store.Register(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Unregister(lease, second.StartID); err != nil {
		t.Fatalf("Unregister() error = %v", err)
	}
	if looks, err := store.Instances(); err != nil || len(looks) != 0 {
		t.Errorf("Instances() after Unregister() = %+v, %v, want none", looks, err)
	}
}

// The supervisor's record of its start is read back as it was written.
func TestTheSupervisorsRecordOfTheDashboardsStartIsKept(t *testing.T) {
	t.Parallel()

	store := newDashboardStore(t)
	if _, found, err := store.Start(); err != nil || found {
		t.Fatalf("Start() before any = %v, %v, want none", found, err)
	}
	if _, err := store.ReadToken(); !errors.Is(err, ErrNoDashboardToken) {
		t.Errorf("ReadToken() before any = %v, want no token", err)
	}
	if _, err := os.Stat(store.Directory()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading the dashboard's records created %s: %v", store.Directory(), err)
	}
	startID, _ := NewDashboardStartID()
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if err := store.RecordStart(DashboardStart{StartID: startID, PID: 4242, Build: "abc", Port: 8765, StartedAt: at}); err != nil {
		t.Fatalf("RecordStart() error = %v", err)
	}
	start, found, err := store.Start()
	if err != nil || !found || start.StartID != startID || start.PID != 4242 || !start.StartedAt.Equal(at) {
		t.Errorf("Start() = %+v, %v, %v", start, found, err)
	}
}

// The generated token is published into a directory only its owner can read,
// in a file only its owner can read, and a dashboard's clean stop removes the
// file it wrote and never one a later dashboard wrote over it.
func TestTheTokenIsPublishedForItsOwnerAloneAndWithdrawnByWhoeverWroteIt(t *testing.T) {
	t.Parallel()

	store := newDashboardStore(t)
	first, err := store.PublishToken("first-token")
	if err != nil {
		t.Fatalf("PublishToken() error = %v", err)
	}
	directory, err := os.Lstat(store.Directory())
	if err != nil || directory.Mode().Perm() != 0o700 {
		t.Errorf("dashboard directory = %v, %v, want mode 0700", directory.Mode(), err)
	}
	file, err := os.Lstat(store.TokenPath())
	if err != nil || file.Mode().Perm() != 0o600 {
		t.Errorf("token file = %v, %v, want mode 0600", file.Mode(), err)
	}
	if token, err := store.ReadToken(); err != nil || token != "first-token" {
		t.Errorf("ReadToken() = %q, %v", token, err)
	}
	if _, err := store.PublishToken("second-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.WithdrawToken(first); err != nil {
		t.Fatalf("WithdrawToken() error = %v", err)
	}
	if token, err := store.ReadToken(); err != nil || token != "second-token" {
		t.Errorf("after the first dashboard's stop, ReadToken() = %q, %v, want the second's token kept", token, err)
	}
	if _, err := store.PublishToken("two\nlines"); err == nil {
		t.Error("a token of two lines was published")
	}
}

// Whatever is at the token's path that is not as it should be is refused and
// left as it is, never repaired and never written through.
func TestAnUnsafeTokenPathIsRefusedAndLeftAsItIs(t *testing.T) {
	t.Parallel()

	t.Run("a link at the file", func(t *testing.T) {
		store := newDashboardStore(t)
		if _, err := store.PublishToken("token"); err != nil {
			t.Fatal(err)
		}
		elsewhere := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.WriteFile(elsewhere, []byte("untouched\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(store.TokenPath()); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, store.TokenPath()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishToken("token"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("PublishToken() over a link = %v, want it refused", err)
		}
		if _, err := store.ReadToken(); err == nil {
			t.Error("ReadToken() read through a link")
		}
		if contents, _ := os.ReadFile(elsewhere); string(contents) != "untouched\n" {
			t.Errorf("the link's target was written: %q", contents)
		}
	})
	t.Run("a file readable beyond its owner", func(t *testing.T) {
		store := newDashboardStore(t)
		if _, err := store.PublishToken("token"); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.TokenPath(), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishToken("token"); err == nil || !strings.Contains(err.Error(), "readable beyond its owner") {
			t.Errorf("PublishToken() over a 0644 file = %v, want it refused", err)
		}
		if info, _ := os.Lstat(store.TokenPath()); info.Mode().Perm() != 0o644 {
			t.Errorf("the refused file was repaired to %v", info.Mode().Perm())
		}
	})
	t.Run("a directory readable beyond its owner", func(t *testing.T) {
		store := newDashboardStore(t)
		if err := os.MkdirAll(store.Directory(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.Directory(), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishToken("token"); err == nil || !strings.Contains(err.Error(), "readable beyond its owner") {
			t.Errorf("PublishToken() into a 0755 directory = %v, want it refused", err)
		}
	})
	t.Run("a link at the directory", func(t *testing.T) {
		store := newDashboardStore(t)
		elsewhere := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(store.Directory()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, store.Directory()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishToken("token"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("PublishToken() into a linked directory = %v, want it refused", err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("the link's target was written: %v", entries)
		}
	})
	t.Run("another user's file", func(t *testing.T) {
		store := newDashboardStore(t)
		if _, err := store.PublishToken("token"); err != nil {
			t.Fatal(err)
		}
		store.owned = func(info fs.FileInfo) bool { return info.IsDir() }
		if _, err := store.ReadToken(); err == nil || !strings.Contains(err.Error(), "owned by another user") {
			t.Errorf("ReadToken() of another user's file = %v, want it refused", err)
		}
	})
}
