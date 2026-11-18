//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A place kept for a check's leftover process is listed with why, is refused
// to a developer's process, and is given back by the development manager or a
// person, recorded with who and why.
func TestAKeptHeavyCheckPlaceIsListedAndGivenBackByHand(t *testing.T) {
	// Not parallel: the state root and the role marker are this process's
	// environment.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewHeavyCheckStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	grant, err := store.Acquire(ctx, runstate.HeavyCheckAsk{
		Product: "yoyodyne", RunID: "run-kept", WorkItemID: "yoyodyne-ifd.429.47",
		Check: "make race", Command: "make race", Limit: 1, Priority: domain.HeavyCheckOrdinary,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The leftover process runs until the test ends, or for at most five
	// minutes, so it outlives its check however loaded the machine is: under a
	// full test suite the check's own exit alone has taken over half a minute to
	// be recorded, and a deadline of one minute ended the leftover first.
	marker := filepath.Join(t.TempDir(), "stop")
	t.Cleanup(func() { _ = os.WriteFile(marker, nil, 0o600) })
	command := exec.Command("/bin/sh", "-c", "end=$(( $(date +%s) + 300 )); ( while [ ! -e '"+marker+"' ] && [ \"$(date +%s)\" -lt \"$end\" ]; do sleep 0.1; done ) & exit 0")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.ExtraFiles = []*os.File{grant.Hold()}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	_ = grant.Hold().Close()
	if err := grant.Register(execution.StartedProcess{PID: command.Process.Pid, ProcessGroup: command.Process.Pid, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	kept, err := grant.Finish("succeeded, exit code 0")
	if err != nil {
		t.Fatal(err)
	}
	if !kept.Stuck() {
		t.Fatalf("permit = %#v, want it keeping its place for the leftover process", kept)
	}

	stdout, stderr, code := runCLI(t, "heavy-checks", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, kept.ID) || !strings.Contains(stdout, "keeping its place because") || !strings.Contains(stdout, "make race of run run-kept") {
		t.Fatalf("list code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	t.Setenv(execution.AgentRoleVariable, string(domain.RoleDeveloper))
	_, stderr, code = runCLI(t, "heavy-checks", "release", kept.ID, "--by", "developer", "--reason", "in my way", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "refused from a process the harness launched for the developer") {
		t.Fatalf("a developer's release: code = %d, stderr = %q; want it refused", code, stderr)
	}
	if still, _ := mustRecord(t, store).Permit(kept.ID); still.State != runstate.HeavyCheckExited {
		t.Fatalf("after the refused release the permit is %s", still.State)
	}

	t.Setenv(execution.AgentRoleVariable, string(domain.RoleDevelopmentManager))
	if _, _, code := runCLI(t, "heavy-checks", "release", kept.ID, "--config", configPath); code != 2 {
		t.Fatalf("a release naming nobody: code = %d, want it refused as incomplete", code)
	}
	stdout, stderr, code = runCLI(t, "heavy-checks", "release", kept.ID, "--by", "development-manager", "--reason", "the leftover sleep is a known hang", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "recorded as released by development-manager") {
		t.Fatalf("release code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	released, _ := mustRecord(t, store).Permit(kept.ID)
	if released.State != runstate.HeavyCheckReleased || released.ReleasedBy != "development-manager" || !strings.Contains(released.Released, "known hang") {
		t.Fatalf("permit = %#v, want it released and recorded with who and why", released)
	}
	if stdout, _, _ := runCLI(t, "heavy-checks", "--config", configPath); !strings.Contains(stdout, "no heavy check is running or waiting") {
		t.Fatalf("list after release = %q", stdout)
	}

	// The launch was measured, and one launch is too few to propose a limit
	// from: the command says so, and changes nothing.
	stdout, stderr, code = runCLI(t, "heavy-checks", "measurements", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "make race: 1 launches, limit 1 at once") || !strings.Contains(stdout, "no limit proposed: only 0 launches finished") ||
		!strings.Contains(stdout, "nothing here changes one") {
		t.Fatalf("measurements code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	stdout, _, code = runCLI(t, "heavy-checks", "measurements", "--json", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, `"limits"`) || !strings.Contains(stdout, `"check":"make race"`) {
		t.Fatalf("measurements --json code = %d, stdout = %q", code, stdout)
	}
}

// A launch whose end could not be written down is listed with the reason the
// harness stored, and left out of the limit proposed, which is then read from
// the launches that did finish.
func TestHeavyCheckMeasurementsListALaunchWhoseEndWasNotWritten(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewHeavyCheckStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	launched := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	exited := launched.Add(time.Minute)
	finished := runstate.HeavyCheckMeasurement{
		Permit: "permit-0123456789abcdef", Product: "yoyodyne", RunID: "run-a", Check: "make race", Command: "make race",
		Priority: domain.HeavyCheckOrdinary, Limit: 1, AskedAt: launched, GrantedAt: launched, LaunchedAt: launched,
		ExitedAt: &exited, BudgetMS: (20 * time.Minute).Milliseconds(), Outcome: runstate.HeavyCheckPassed,
		Running: map[string]int{"make race": 1},
	}
	unsaved := finished
	unsaved.Permit, unsaved.RunID, unsaved.ExitedAt, unsaved.BudgetMS, unsaved.Outcome = "permit-fedcba9876543210", "run-b", nil, 0, ""
	unsaved.Unsaved = "the end of make race could not be written to the heavy check measurements: the disk refused the write"
	encoded, err := json.Marshal(runstate.HeavyCheckMeasurements{
		SchemaVersion: runstate.HeavyCheckMeasurementSchemaVersion, Measurements: []runstate.HeavyCheckMeasurement{finished, unsaved},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "measurements.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, "heavy-checks", "measurements", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "make race: 1 launches, limit 1 at once") ||
		!strings.Contains(stdout, "1 launches ended without their end written down, so how long they ran is not known and they are left out of every limit above") ||
		!strings.Contains(stdout, "make race of run run-b (yoyodyne), launched") || !strings.Contains(stdout, "the disk refused the write") {
		t.Fatalf("measurements code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	stdout, _, code = runCLI(t, "heavy-checks", "measurements", "--json", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, `"unsaved"`) || !strings.Contains(stdout, `"run_id":"run-b"`) {
		t.Fatalf("measurements --json code = %d, stdout = %q", code, stdout)
	}
}

// A machine with no heavy check measured says so.
func TestHeavyCheckMeasurementsSayWhenNothingWasMeasured(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	stdout, stderr, code := runCLI(t, "heavy-checks", "measurements", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no heavy check launch has been measured on this machine yet") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func mustRecord(t *testing.T, store *runstate.HeavyCheckStore) runstate.HeavyCheckRecord {
	t.Helper()
	record, err := store.Record()
	if err != nil {
		t.Fatal(err)
	}
	return record
}
