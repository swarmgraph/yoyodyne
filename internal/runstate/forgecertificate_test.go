package runstate

import (
	"os"
	"strings"
	"testing"
	"time"
)

func newForgeCertificateStore(t *testing.T) *ForgeCertificateStore {
	t.Helper()
	store, err := NewForgeCertificateStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestForgeCertificateReadsEmptyWhereNothingWasRecorded(t *testing.T) {
	t.Parallel()
	got, err := newForgeCertificateStore(t).Read()
	if err != nil || got.Failing() || got.Says() != "" {
		t.Fatalf("Read = %+v, %v; want an empty record", got, err)
	}
}

func TestForgeCertificateFailuresCountAndKeepSince(t *testing.T) {
	t.Parallel()
	store := newForgeCertificateStore(t)
	first := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)
	if err := store.Failed(4242, first, "x509: OSStatus -26276"); err != nil {
		t.Fatal(err)
	}
	if err := store.Failed(4242, second, "x509: OSStatus -26276 again"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Since.Equal(first) || got.Checks != 2 || !got.LatestAt.Equal(second) || got.Latest != "x509: OSStatus -26276 again" || got.SupervisorPID != 4242 {
		t.Fatalf("record = %+v", got)
	}
}

func TestForgeCertificateAnotherSupervisorStartsTheCountAgain(t *testing.T) {
	t.Parallel()
	store := newForgeCertificateStore(t)
	first := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	later := first.Add(time.Hour)
	_ = store.Failed(1, first, "a")
	_ = store.Failed(1, first.Add(time.Minute), "a")
	if err := store.Failed(2, later, "b"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Read()
	if !got.Since.Equal(later) || got.Checks != 1 || got.SupervisorPID != 2 {
		t.Fatalf("record = %+v", got)
	}
}

func TestForgeCertificateClearedEmptiesTheRecordAndCreatesNothingOtherwise(t *testing.T) {
	t.Parallel()
	store := newForgeCertificateStore(t)
	if err := store.Cleared(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Fatalf("Cleared with nothing standing left a file: %v", err)
	}
	if err := store.Failed(7, time.Now(), "x509: OSStatus -26276"); err != nil {
		t.Fatal(err)
	}
	if err := store.Cleared(); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read()
	if err != nil || got.Failing() || got.Checks != 0 {
		t.Fatalf("after Cleared, Read = %+v, %v", got, err)
	}
}

func TestForgeCertificateSaysNamesTheSupervisorAndTheCount(t *testing.T) {
	t.Parallel()
	store := newForgeCertificateStore(t)
	at := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	for range 3 {
		if err := store.Failed(4242, at, "x509: OSStatus -26276"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.Read()
	says := got.Says()
	for _, want := range []string{"pid 4242", "3 in all", "x509: OSStatus -26276", "certificate store"} {
		if !strings.Contains(says, want) {
			t.Fatalf("Says = %q, missing %q", says, want)
		}
	}
}
