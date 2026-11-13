package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// folderProvider builds the Codex adapter with the usual install folders
// pointed at folders the test made, so nothing on the machine is searched.
func folderProvider(t *testing.T, descriptor backend.Descriptor, folders ...string) (backend.Backend, *recordingRunner) {
	t.Helper()
	runner := &recordingRunner{}
	provider, _ := For(descriptor, descriptor.ID, runner, "")
	built, ok := provider.(codex.Backend)
	if !ok {
		t.Fatalf("adapter = %T, want the Codex adapter", provider)
	}
	built.Runner.(*executableRunner).folders = func() []string { return folders }
	return provider, runner
}

func launch(t *testing.T, provider backend.Backend) (backend.RunResult, error) {
	t.Helper()
	return provider.Run(context.Background(), backend.RunRequest{RunID: "run-fixture", Role: domain.RoleArchitect, Model: "gpt-6-astra", WorkingDirectory: t.TempDir(), Prompt: "ping"})
}

// The program is not on the search path and nothing is configured, so the
// usual install folders are looked in, in order; what is found there is
// version-checked, used for every launch, and named on availability.
func TestAProgramOffTheSearchPathIsFoundInAUsualInstallFolder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "service path"))
	empty := filepath.Join(root, "empty folder")
	folder := filepath.Join(root, "local bin")
	later := filepath.Join(root, "later folder")
	writeCLI(t, filepath.Join(folder, "codex"), codexCLI, 0o755)
	writeCLI(t, filepath.Join(later, "codex"), strings.ReplaceAll(codexCLI, "Fixture reply.", "Wrong installation."), 0o755)
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	provider, runner := folderProvider(t, descriptor, empty, folder, later)

	availability, err := provider.CheckAvailability(context.Background())
	want := filepath.Join(folder, "codex")
	if err != nil || !availability.Installed || availability.Path != want {
		t.Fatalf("availability = %+v, %v; want the program in the first folder that has it", availability, err)
	}
	result, err := launch(t, provider)
	if err != nil || result.IsError || result.FinalText != "Fixture reply." {
		t.Fatalf("launch = %+v, %v", result, err)
	}
	for _, command := range runner.commands {
		if command.Name != want {
			t.Fatalf("started %q, want %q", command.Name, want)
		}
	}
	// The version check is the first thing asked of what was found.
	if len(runner.commands) == 0 || runner.commands[0].Args[0] != "--version" {
		t.Fatalf("commands = %+v, want the version check first", runner.commands)
	}
}

// A program on the search path is used from there even where a usual install
// folder also has one.
func TestTheSearchPathIsLookedInBeforeTheUsualInstallFolders(t *testing.T) {
	root := t.TempDir()
	onPath := filepath.Join(root, "service path")
	folder := filepath.Join(root, "local bin")
	writeCLI(t, filepath.Join(onPath, "codex"), codexCLI, 0o755)
	writeCLI(t, filepath.Join(folder, "codex"), codexCLI, 0o755)
	t.Setenv("PATH", onPath)
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	provider, _ := folderProvider(t, descriptor, folder)
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil || availability.Path != filepath.Join(onPath, "codex") {
		t.Fatalf("availability = %+v, %v; want the program on the search path", availability, err)
	}
}

// A configured binary always wins: it is used where it is, and where it is
// missing the folders are not searched for another installation.
func TestAConfiguredBinaryIsNeverReplacedByOneInAUsualInstallFolder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "service path"))
	folder := filepath.Join(root, "local bin")
	writeCLI(t, filepath.Join(folder, "codex"), codexCLI, 0o755)
	configured := filepath.Join(root, "configured", "codex")
	writeCLI(t, configured, codexCLI, 0o755)
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	descriptor.Binary = configured
	provider, _ := folderProvider(t, descriptor, folder)
	if availability, err := provider.CheckAvailability(context.Background()); err != nil || availability.Path != configured {
		t.Fatalf("availability = %+v, %v; want the configured binary", availability, err)
	}

	missing := filepath.Join(root, "configured", "gone")
	descriptor.Binary = missing
	provider, runner := folderProvider(t, descriptor, folder)
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil || availability.Installed || !availability.ProgramMissing || strings.Contains(availability.Missing, "usual install folders") {
		t.Fatalf("availability = %+v, %v; want the configured binary reported missing and no folder searched", availability, err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("started %+v, want nothing started in place of the configured binary", runner.commands)
	}

	// A bare configured name is looked up on the search path only.
	descriptor.Binary = "codex"
	provider, _ = folderProvider(t, descriptor, folder)
	if availability, err := provider.CheckAvailability(context.Background()); err != nil || availability.Installed {
		t.Fatalf("availability = %+v, %v; want a configured bare name looked for on the search path alone", availability, err)
	}
}

// Found nowhere: availability says it is missing, rather than unable to run,
// names the folders looked in and the one fix; a launch starts nothing and
// hands back the wait for the program, not a process the machine could not
// start.
func TestAProgramFoundNowhereIsAWaitForTheProgram(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "service path"))
	folder := filepath.Join(root, "local bin")
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	provider, runner := folderProvider(t, descriptor, folder)
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil || availability.Installed || !availability.ProgramMissing ||
		!strings.Contains(availability.Missing, folder) || !strings.Contains(availability.Missing, "providers.codex.binary") {
		t.Fatalf("availability = %+v, %v", availability, err)
	}
	result, err := launch(t, provider)
	if err == nil || !errors.Is(err, execution.ErrProcessNotStarted) || len(runner.commands) != 0 {
		t.Fatalf("launch = %+v, %v, commands %v", result, err, runner.commands)
	}
	if result.ProviderOutage == nil || result.ProviderOutage.Cause != domain.ProviderProgramMissing || !result.IsError ||
		result.ProviderOutage.Provider != domain.BackendCodex || !strings.Contains(result.ProviderOutage.Detail, "codex") {
		t.Fatalf("result = %+v, want the missing program handed back as a wait naming it", result)
	}
	// A program that is there and cannot be run is a setup fault, not a wait.
	writeCLI(t, filepath.Join(root, "service path", "codex"), codexCLI, 0o644)
	availability, err = provider.CheckAvailability(context.Background())
	if err != nil || availability.Installed || availability.ProgramMissing {
		t.Fatalf("availability = %+v, %v; want a program that cannot run told apart from a missing one", availability, err)
	}
}

// A program that moves after it was found is looked for afresh rather than
// launched from where it was: found again in a usual install folder, the work
// carries on; removed altogether, it is reported missing at once rather than
// from what was found earlier.
func TestAProgramThatMovesAfterItWasFoundIsLookedForAgain(t *testing.T) {
	root := t.TempDir()
	onPath := filepath.Join(root, "service path")
	folder := filepath.Join(root, "local bin")
	t.Setenv("PATH", onPath)
	writeCLI(t, filepath.Join(onPath, "codex"), codexCLI, 0o755)
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	provider, _ := folderProvider(t, descriptor, folder)
	if availability, err := provider.CheckAvailability(context.Background()); err != nil || availability.Path != filepath.Join(onPath, "codex") {
		t.Fatalf("before the move = %+v, %v", availability, err)
	}

	if err := os.Remove(filepath.Join(onPath, "codex")); err != nil {
		t.Fatal(err)
	}
	if availability, err := provider.CheckAvailability(context.Background()); err != nil || availability.Installed || !availability.ProgramMissing {
		t.Fatalf("after removal = %+v, %v; want it reported missing now", availability, err)
	}
	if result, _ := launch(t, provider); result.ProviderOutage == nil || result.ProviderOutage.Cause != domain.ProviderProgramMissing {
		t.Fatalf("launch after removal = %+v, want a wait for the program", result)
	}

	writeCLI(t, filepath.Join(folder, "codex"), codexCLI, 0o755)
	if availability, err := provider.CheckAvailability(context.Background()); err != nil || availability.Path != filepath.Join(folder, "codex") {
		t.Fatalf("after it came back = %+v, %v; want it found in the usual install folder", availability, err)
	}
	if result, err := launch(t, provider); err != nil || result.IsError {
		t.Fatalf("launch after it came back = %+v, %v", result, err)
	}
}
