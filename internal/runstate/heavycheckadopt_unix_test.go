//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// Each product's permits are kept under its own project, nothing is kept at
// the top of the home, and every product on the machine still counts against
// the same places: a second product's run waits for the first product's check.
func TestHeavyCheckPlacesAreKeptPerProjectAndCountedAcrossThem(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	first := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	held, release := heldUntil(t)
	command := launchHeld(t, first, held)
	other := heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary)
	other.Product = "other"
	granted, waiting, failed := acquireLater(store, other)
	waited := expectWaiting(t, waiting, failed)
	expectStillWaiting(t, granted, failed)

	for product, permit := range map[string]string{"yoyodyne": first.Permit.ID, "other": waited.ID} {
		directory := filepath.Join(home.ProductDirectory(store.stateRoot, product), HeavyCheckDirectory)
		root, err := pinExisting(directory)
		if err != nil {
			t.Fatalf("%s keeps no part of the record at %s: %v", product, directory, err)
		}
		record, err := readHeavyCheckRecord(root, true)
		root.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := record.Permit(permit); !ok || len(record.Permits) != 1 {
			t.Fatalf("%s's part = %#v, want only its own permit %s", product, record.Permits, permit)
		}
	}
	if _, err := os.Lstat(filepath.Join(store.stateRoot, HeavyCheckDirectory)); !os.IsNotExist(err) {
		t.Fatalf("the top of the home holds a record of heavy check places: %v", err)
	}

	release()
	_ = command.Wait()
	if _, err := first.Finish("succeeded, exit code 0"); err != nil {
		t.Fatal(err)
	}
	second := expectGranted(t, granted, failed)
	command = launchHeld(t, second, "exit 0")
	_ = command.Wait()
	if _, err := second.Finish("succeeded, exit code 0"); err != nil {
		t.Fatal(err)
	}
	if measurements, err := store.Measurements(); err != nil || len(measurements.Measurements) != 2 {
		t.Fatalf("measurements = %#v, %v; want both products' launches", measurements, err)
	}
}

// A record a build before this one kept at the top of the home is filed under
// the project it belongs to by the next look at the places, once, with its
// measurements: a check still running keeps its place and is released once it
// ends, and the top of the home is left holding no heavy-checks folder.
func TestTheTopLevelRecordOfPlacesIsFiledUnderItsProject(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	held, release := heldUntil(t)
	command := launchHeld(t, grant, held)
	// The record as an earlier build left it: the same files, at the top.
	part := filepath.Join(home.ProductDirectory(store.stateRoot, "yoyodyne"), HeavyCheckDirectory)
	top := filepath.Join(store.stateRoot, HeavyCheckDirectory)
	if err := os.Rename(part, top); err != nil {
		t.Fatal(err)
	}
	if kept, ok := recordOf(t, store).Permit(grant.Permit.ID); !ok || kept.State != HeavyCheckRunning {
		t.Fatalf("the record shown = %#v, want the top-level permit shown running before it is filed", kept)
	}
	if _, err := os.Lstat(top); err != nil {
		t.Fatalf("showing the record moved it: %v", err)
	}

	waiting := heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary)
	granted, told, failed := acquireLater(store, waiting)
	expectWaiting(t, told, failed)
	expectStillWaiting(t, granted, failed)
	if _, err := os.Lstat(top); !os.IsNotExist(err) {
		t.Fatalf("the top-level record is still at %s: %v", top, err)
	}
	if kept, ok := recordOf(t, store).Permit(grant.Permit.ID); !ok || kept.State != HeavyCheckRunning {
		t.Fatalf("the filed permit = %#v, want it still running and holding its place", kept)
	}
	for _, name := range []string{heavyCheckRecordName, heavyCheckMeasurementsName, grant.Permit.Hold} {
		if _, err := os.Lstat(filepath.Join(part, name)); err != nil {
			t.Fatalf("%s was not filed under the project: %v", name, err)
		}
	}

	release()
	_ = command.Wait()
	ended, err := grant.Finish("succeeded, exit code 0")
	if err != nil {
		t.Fatal(err)
	}
	if ended.State != HeavyCheckReleased {
		t.Fatalf("the filed permit = %#v, want it released once its check ended", ended)
	}
	second := expectGranted(t, granted, failed)
	command = launchHeld(t, second, "exit 0")
	_ = command.Wait()
	if _, err := second.Finish("succeeded, exit code 0"); err != nil {
		t.Fatal(err)
	}
	measurements, err := store.Measurements()
	if err != nil || len(measurements.Measurements) != 2 || measurements.Measurements[0].Permit != grant.Permit.ID || measurements.Measurements[0].ExitedAt == nil {
		t.Fatalf("measurements = %#v, %v; want the filed launch, finished, before the next", measurements, err)
	}
}
