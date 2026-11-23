package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheServicesFolderIsOpenToItsOwnerOnly(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	services, err := PrepareServicesDirectory(state)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolve(t, state), "services"); services != want {
		t.Fatalf("PrepareServicesDirectory() = %q, want %q", services, want)
	}
	info, err := os.Stat(services)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("services folder = %v, %v; want access for its owner only", info, err)
	}
	writeFile(t, filepath.Join(services, "token"), "kept")
	if again, err := PrepareServicesDirectory(state); err != nil || again != services {
		t.Fatalf("PrepareServicesDirectory() again = %q, %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(services, "token")); err != nil {
		t.Fatalf("preparing again lost what the folder held: %v", err)
	}
	if err := os.Chmod(services, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareServicesDirectory(state); err == nil {
		t.Fatal("a services folder others can open was accepted")
	}
}

func TestAServicesFolderReachedThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	for _, target := range []string{t.TempDir(), "inside"} {
		state := t.TempDir()
		makeDirectory(t, filepath.Join(state, "inside"))
		if err := os.Symlink(target, filepath.Join(state, "services")); err != nil {
			t.Skipf("this filesystem does not support symlinks: %v", err)
		}
		if services, err := PrepareServicesDirectory(state); err == nil {
			t.Fatalf("PrepareServicesDirectory() = %q through a link to %q, want a refusal", services, target)
		}
	}
	if _, err := PrepareServicesDirectory(" "); err == nil {
		t.Fatal("no state directory was accepted")
	}
}

// The folder a run is refused is the one beside its own scratch folder, and a
// run whose folder cannot be found or named exactly is not started.
func TestTheServicesFolderDeniedToARunIsFoundFromItsScratch(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	scratch, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	services, err := ServicesDirectoryDeniedTo(scratch, "run-one", "")
	if want := filepath.Join(resolve(t, state), "services"); err != nil || services != want {
		t.Fatalf("ServicesDirectoryDeniedTo() = %q, %v; want %q", services, err, want)
	}
	odd := filepath.Join(t.TempDir(), "state*")
	makeDirectory(t, odd)
	oddScratch, err := PrepareScratchDirectory(odd, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	for name, refused := range map[string]string{"no scratch": "", "another run's scratch": filepath.Join(filepath.Dir(scratch), "run-two"), "a path rules cannot name": oddScratch} {
		if got, err := ServicesDirectoryDeniedTo(refused, "run-one", ""); err == nil {
			t.Errorf("%s: ServicesDirectoryDeniedTo() = %q, want a refusal", name, got)
		}
	}
}
