package ownership

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeStepFile(t *testing.T, root, name string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The paths a step names are the words that read as paths, and nothing that
// merely holds a slash.
func TestNamedPathsAreTheWordsThatReadAsPaths(t *testing.T) {
	t.Parallel()
	got := NamedPaths("Edit `.claude/settings.json`, then the folder .yoyodyne/roles/ and internal/config/grants.go; and/or on 11/22/2026 see https://example.com/a.b and docs/designs.")
	want := []string{".claude/settings.json", ".yoyodyne/roles/", "internal/config/grants.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NamedPaths() = %q, want %q", got, want)
	}
}

// A "protected-file" step is refused where a path it names does not exist,
// where it names a file a developer run may change, and where it names no file
// at all; a provider settings file or a role definition that is there passes.
func TestAProtectedFileStepNamesFilesThatExistAndOnlyAPersonMayChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeStepFile(t, root, ".claude/settings.json")
	writeStepFile(t, root, ".yoyodyne/roles/developer.yaml")
	writeStepFile(t, root, "internal/config/grants.go")
	world := StepWorld{Repository: root}

	for _, tc := range []struct {
		name, step string
		refusal    string
	}{
		{"a settings file that is there", "add the PreToolUse hook to .claude/settings.json by hand", ""},
		{"a role definition that is there", "change .yoyodyne/roles/developer.yaml to grant the path", ""},
		{"a role folder that is not there", "edit the role-definition folder .yoyodyne/roles/reviewer/ by hand", "does not exist in the repository"},
		{"a file a developer run may change", "edit internal/config/grants.go to widen the grant", "a developer run may change"},
		{"no file at all", "make the change by hand", "names no file"},
	} {
		err := CheckOperatorStep(OperatorProtectedFile, tc.step, nil, world)
		switch {
		case tc.refusal == "" && err != nil:
			t.Errorf("%s: CheckOperatorStep() = %v, want it to pass", tc.name, err)
		case tc.refusal != "" && (err == nil || !strings.Contains(err.Error(), tc.refusal)):
			t.Errorf("%s: CheckOperatorStep() = %v, want a refusal saying %q", tc.name, err, tc.refusal)
		}
	}

	// A person-only remedy's target is checked as a path the step names.
	remedy := &PersonOnlyRemedy{Reason: PersonProtectedFile, Target: ".claude/settings.local.json", Step: "add the hook by hand"}
	if err := CheckOperatorStep(OperatorProtectedFile, "the hook", remedy, world); err == nil || !strings.Contains(err.Error(), ".claude/settings.local.json does not exist") {
		t.Fatalf("CheckOperatorStep(remedy) = %v, want its missing target refused", err)
	}
	// With no repository to look in, only whether a person alone may change the
	// file is asked.
	if err := CheckOperatorStep(OperatorProtectedFile, "edit .claude/settings.local.json", nil, StepWorld{}); err != nil {
		t.Fatalf("CheckOperatorStep(no repository) = %v, want it to pass", err)
	}
	// The other reasons name nothing this can look at.
	if err := CheckOperatorStep(OperatorCredential, "log in again; see internal/config/grants.go", nil, world); err != nil {
		t.Fatalf("CheckOperatorStep(credential) = %v, want it to pass", err)
	}
}

// An "own-hold" request is refused once no hold the operator placed stands, and
// a hold that cannot be read is read as standing.
func TestAnOwnHoldRequestNeedsTheHoldToStand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		stands  bool
		err     error
		refused bool
	}{
		{"the hold stands", true, nil, false},
		{"the hold is gone", false, nil, true},
		{"the hold cannot be read", false, errors.New("unreadable"), false},
	} {
		world := StepWorld{OwnHold: func() (bool, error) { return tc.stands, tc.err }}
		err := CheckOperatorStep(OperatorOwnHold, "lift the intake hold", nil, world)
		if (err != nil) != tc.refused {
			t.Errorf("%s: CheckOperatorStep() = %v, want refused %t", tc.name, err, tc.refused)
		}
	}
}

// A waiting request's paths are checked again only once it has waited three
// days; its hold is checked on every reading.
func TestAWaitingRequestIsCheckedAgainAfterThreeDays(t *testing.T) {
	t.Parallel()
	world := StepWorld{Repository: t.TempDir(), OwnHold: func() (bool, error) { return false, nil }}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	step := "edit .yoyodyne/roles/developer.yaml by hand"
	if err := RecheckOperatorStep(OperatorProtectedFile, step, nil, now.Add(-71*time.Hour), now, world); err != nil {
		t.Fatalf("RecheckOperatorStep(71 hours) = %v, want it not checked yet", err)
	}
	if err := RecheckOperatorStep(OperatorProtectedFile, step, nil, now.Add(-73*time.Hour), now, world); err == nil {
		t.Fatal("RecheckOperatorStep(73 hours) = nil, want the missing file refused")
	}
	if err := RecheckOperatorStep(OperatorOwnHold, "lift the hold", nil, now.Add(-time.Minute), now, world); err == nil {
		t.Fatal("RecheckOperatorStep(own-hold) = nil, want a lifted hold refused at once")
	}
}
