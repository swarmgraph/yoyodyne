package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// onAMachineNobodyConfigured clears every variable that would choose a home,
// so the home is found the way a machine with nothing exported finds it, under
// a user home of the test's own.
func onAMachineNobodyConfigured(t *testing.T) string {
	t.Helper()
	user := t.TempDir()
	t.Setenv("HOME", user)
	for _, variable := range []string{"YOYODYNE_STATE_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "YOYODYNE_CONFIG_HOME"} {
		t.Setenv(variable, "")
	}
	return user
}

// `yoyo home migrate` moves nothing while a run is in flight and says which, in
// one sentence; once nothing is, it moves the product's records and the machine
// file out of the earlier homes, says what it moved and what it left, binds the
// project, and the checkout's commands then run on the machine home, its marker
// following the state.
func TestHomeMigrateRefusesWhileARunIsInFlightAndThenMovesTheHome(t *testing.T) {
	// Not parallel: the home every command resolves is set for this process.
	user := onAMachineNobodyConfigured(t)
	project, configPath := stateRootProject(t)
	earlier, err := home.EarlierDefault(os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(earlier, "products", "yoyodyne"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports on the earlier home code = %d, stderr = %q", code, stderr)
	}
	machineFile := filepath.Join(user, ".config", "yoyodyne", home.MachineFileName)
	if err := os.MkdirAll(filepath.Dir(machineFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineFile, []byte("# nothing set here\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runID := "run-" + strings.Repeat("0", 32)
	lease, held, err := runstate.TryLeasePath(filepath.Join(home.ProductDirectory(earlier, "yoyodyne"), "runs", runID+".lease"), "a run the test holds")
	if err != nil || !held {
		t.Fatalf("TryLeasePath() = %v, %v", held, err)
	}
	_, stderr, code := runCLI(t, "home", "migrate", "--config", configPath)
	if code == 0 || !strings.Contains(stderr, "moved nothing") || !strings.Contains(stderr, "run "+runID) || strings.Count(strings.TrimSpace(stderr), "\n") != 0 {
		t.Fatalf("migrate with a run in flight: code = %d, stderr = %q; want one sentence refusing and naming the run", code, stderr)
	}
	machineHome := filepath.Join(user, home.DirectoryName)
	if _, err := os.Stat(machineHome); !os.IsNotExist(err) || !home.EarlierLayout(earlier) {
		t.Fatalf("the refused migration moved something (machine home: %v)", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "home", "migrate", "--config", configPath)
	if code != 0 {
		t.Fatalf("migrate code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, want := range []string{"moved the harness's state from " + earlier, "the records of yoyodyne", "the machine file", "left behind", "bound it to the repository"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("migrate said %q, want it to say %q", stdout, want)
		}
	}
	for _, target := range []string{home.ProductDirectory(machineHome, "yoyodyne"), filepath.Join(machineHome, home.MachineFileName), home.BindingPath(machineHome, "yoyodyne")} {
		if _, err := os.Stat(target); err != nil {
			t.Errorf("%s is not there after the migration: %v", target, err)
		}
	}
	if _, err := os.Stat(machineFile); !os.IsNotExist(err) {
		t.Errorf("the earlier machine file is still there (%v)", err)
	}

	if _, stderr, code := runCLI(t, "reports", "--config", configPath); code != 0 {
		t.Fatalf("reports after the migration code = %d, stderr = %q; want the marker to follow the state", code, stderr)
	}
	marker, err := runstate.ReadRootMarker(project)
	if err != nil || !home.SameDirectory(marker.Recorded, machineHome) {
		t.Fatalf("marker = %+v, %v; want it to name %s now", marker, err, machineHome)
	}
	if stdout, _, code := runCLI(t, "project", "list", "--home"); code != 0 || strings.TrimSpace(stdout) != machineHome {
		t.Fatalf("project list --home = %q (%d), want %s", stdout, code, machineHome)
	}
}

// Until the migration moves it, the machine file earlier builds kept in
// ~/.config/yoyodyne is read where the machine home holds none, rather than the
// home it names going unread; one in the machine home wins once it is there.
func TestTheEarlierMachineFileIsReadUntilTheMigrationMovesIt(t *testing.T) {
	// Not parallel: the home every command resolves is set for this process.
	user := onAMachineNobodyConfigured(t)
	named := t.TempDir()
	earlierFile := filepath.Join(user, ".config", "yoyodyne", home.MachineFileName)
	if err := os.MkdirAll(filepath.Dir(earlierFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(earlierFile, []byte("state_root: "+named+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, code := runCLI(t, "project", "list", "--home"); code != 0 || strings.TrimSpace(stdout) != named {
		t.Fatalf("project list --home = %q (%d, %q), want the home the earlier machine file names, %s", stdout, code, stderr, named)
	}
	current := t.TempDir()
	if err := os.MkdirAll(filepath.Join(user, home.DirectoryName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, home.DirectoryName, home.MachineFileName), []byte("state_root: "+current+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if stdout, _, code := runCLI(t, "project", "list", "--home"); code != 0 || strings.TrimSpace(stdout) != current {
		t.Fatalf("project list --home = %q (%d), want the machine home's file, %s", stdout, code, current)
	}
}
