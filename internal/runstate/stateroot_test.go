package runstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// machine is a described machine: a home directory, the variables its shell
// exports, and optionally a machine file in its machine home.
type machine struct {
	home string
	env  map[string]string
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	return &machine{home: t.TempDir(), env: map[string]string{}}
}

func (m *machine) getenv(key string) string { return m.env[key] }

func (m *machine) homeDir() (string, error) { return m.home, nil }

func (m *machine) writeMachineFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(m.home, ".yoyodyne", MachineFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (m *machine) resolve(t *testing.T) ResolvedRoot {
	t.Helper()
	resolved, err := ResolveRoot(m.getenv, m.homeDir, "linux")
	if err != nil {
		t.Fatalf("ResolveRoot() error = %v", err)
	}
	return resolved
}

func TestTheStateRootResolvesInTheOrderTheDesignRules(t *testing.T) {
	t.Parallel()

	m := newMachine(t)
	if got := m.resolve(t); got.Path != filepath.Join(m.home, ".yoyodyne") || got.Origin != RootOriginDefault {
		t.Fatalf("nothing configured = %+v, want ~/.yoyodyne", got)
	}

	m.env["XDG_STATE_HOME"] = "/xdg"
	if got := m.resolve(t); got.Path != filepath.Join("/xdg", "yoyodyne") || got.Origin != RootOriginXDG {
		t.Fatalf("XDG only = %+v, want XDG_STATE_HOME/yoyodyne", got)
	}

	machinePath := m.writeMachineFile(t, "state_root: /machine/state\n")
	if got := m.resolve(t); got.Path != "/machine/state" || got.Origin != "machine:"+machinePath {
		t.Fatalf("machine key over XDG = %+v, want the machine key naming its file", got)
	}

	m.env[StateHomeVariable] = "/explicit"
	if got := m.resolve(t); got.Path != "/explicit" || got.Origin != RootOriginEnvironment {
		t.Fatalf("variable over machine key = %+v, want YOYODYNE_STATE_HOME", got)
	}
}

// The machine file is read from the machine home and nowhere else: one left in
// the configurations home earlier builds kept it in, wherever YOYODYNE_CONFIG_HOME
// or XDG_CONFIG_HOME pointed that, moves nothing.
func TestAMachineFileInTheEarlierConfigurationsHomeIsNotRead(t *testing.T) {
	t.Parallel()

	m := newMachine(t)
	relocated := t.TempDir()
	m.env["YOYODYNE_CONFIG_HOME"] = relocated
	m.env["XDG_CONFIG_HOME"] = filepath.Join(m.home, "xdg")
	for _, directory := range []string{relocated, filepath.Join(m.home, "xdg", "yoyodyne"), filepath.Join(m.home, ".config", "yoyodyne")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, MachineFileName), []byte("state_root: /earlier\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.resolve(t); got.Path != filepath.Join(m.home, ".yoyodyne") || got.Origin != RootOriginDefault {
		t.Fatalf("ResolveRoot() = %+v, want ~/.yoyodyne with the earlier machine files unread", got)
	}
}

func TestAMachineFileThatSaysNothingUsefulIsRefusedOrIgnored(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"relative":     "state_root: state\n",
		"unknown key":  "state_rot: /state\n",
		"not yaml map": "- /state\n",
	} {
		m := newMachine(t)
		m.writeMachineFile(t, content)
		if _, err := ResolveRoot(m.getenv, m.homeDir, "linux"); err == nil {
			t.Errorf("%s: ResolveRoot() accepted %q", name, content)
		}
	}
	for name, content := range map[string]string{"empty file": "", "empty key": "state_root: \"\"\n"} {
		m := newMachine(t)
		m.writeMachineFile(t, content)
		if got := m.resolve(t); got.Origin != RootOriginDefault {
			t.Errorf("%s: ResolveRoot() = %+v, want ~/.yoyodyne", name, got)
		}
	}
}

func gitCheckout(t *testing.T) string {
	t.Helper()
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return checkout
}

func TestTheFirstProcessRecordsTheRootAndASecondRootIsRefused(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	first := ResolvedRoot{Path: t.TempDir(), Origin: RootOriginDefault}
	if err := AgreeRoot(checkout, first); err != nil {
		t.Fatalf("AgreeRoot() first = %v", err)
	}
	marker := filepath.Join(checkout, ".git", "yoyodyne", "state-root")
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if strings.TrimSpace(string(content)) != first.Path {
		t.Fatalf("marker = %q, want %q", content, first.Path)
	}
	if err := AgreeRoot(checkout, first); err != nil {
		t.Fatalf("AgreeRoot() same root again = %v", err)
	}

	second := ResolvedRoot{Path: t.TempDir(), Origin: RootOriginEnvironment}
	err = AgreeRoot(checkout, second)
	var split *SplitRootError
	if !errors.As(err, &split) {
		t.Fatalf("AgreeRoot() second root = %v, want a SplitRootError", err)
	}
	for _, want := range []string{first.Path, second.Path, marker, RootOriginEnvironment, "yoyo stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if content, _ := os.ReadFile(marker); strings.TrimSpace(string(content)) != first.Path {
		t.Fatalf("the refused process rewrote the marker to %q", content)
	}
}

func TestARootReachedThroughASymlinkIsTheRootItLinksTo(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := AgreeRoot(checkout, ResolvedRoot{Path: real}); err != nil {
		t.Fatal(err)
	}
	if err := AgreeRoot(checkout, ResolvedRoot{Path: link}); err != nil {
		t.Fatalf("AgreeRoot() through a symlink = %v, want agreement", err)
	}
}

func TestALinkedWorktreeSharesItsPrimaryCheckoutsMarker(t *testing.T) {
	t.Parallel()

	primary := gitCheckout(t)
	private := filepath.Join(primary, ".git", "worktrees", "one")
	if err := os.MkdirAll(private, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+private+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := ResolvedRoot{Path: t.TempDir()}
	if err := AgreeRoot(primary, first); err != nil {
		t.Fatal(err)
	}
	if err := AgreeRoot(worktree, ResolvedRoot{Path: t.TempDir()}); err == nil {
		t.Fatal("a process in a linked worktree resolved a second root and was let start")
	}
	if err := AgreeRoot(worktree, first); err != nil {
		t.Fatalf("AgreeRoot() from the worktree on the recorded root = %v", err)
	}
}

func TestADirectoryThatIsNotACheckoutKeepsNoMarker(t *testing.T) {
	t.Parallel()

	plain := t.TempDir()
	if err := AgreeRoot(plain, ResolvedRoot{Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := AgreeRoot(plain, ResolvedRoot{Path: t.TempDir()}); err != nil {
		t.Fatalf("AgreeRoot() on a non-checkout = %v, want nothing recorded or compared", err)
	}
	marker, err := ReadRootMarker(plain)
	if err != nil || marker.Path != "" {
		t.Fatalf("ReadRootMarker() = %+v, %v; want no marker", marker, err)
	}
}

// Two products on one machine share the root by default, and the operator hold
// at the root is one switch over both: each product's own checkout records the
// shared root, neither refuses the other, and a hold placed through one is read
// through the other.
func TestTwoProductsShareOneRootAndOneOperatorHold(t *testing.T) {
	t.Parallel()

	shared := ResolvedRoot{Path: t.TempDir(), Origin: RootOriginDefault}
	yoyodyne, conductor := gitCheckout(t), gitCheckout(t)
	for _, checkout := range []string{yoyodyne, conductor} {
		if err := AgreeRoot(checkout, shared); err != nil {
			t.Fatalf("AgreeRoot(%s) = %v", checkout, err)
		}
	}

	placing, err := NewOperatorHoldStore(shared.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := placing.Hold(time.Now()); err != nil {
		t.Fatal(err)
	}
	reading, err := NewOperatorHoldStore(shared.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, held, err := reading.Held(); err != nil || !held {
		t.Fatalf("Held() through the second product = %v, %v; want the machine-wide hold", held, err)
	}

	first, err := NewStore(shared.Path, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(shared.Path, "context-conductor")
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{first, second} {
		if !strings.HasPrefix(store.Root(), filepath.Join(shared.Path, "projects")+string(filepath.Separator)) {
			t.Errorf("store %s is not a product's own directory under the shared root", store.Root())
		}
	}
	if first.Root() == second.Root() {
		t.Fatalf("both products keep their runs in %s", first.Root())
	}
}

// A marker naming a root that is gone is a stale record, not a second root: the
// refusal says so, says which process recorded it and when, and names the one
// command that replaces it; that command replaces it and nothing else.
func TestAMarkerNamingAMissingRootIsRefusedWithTheRebindRemedy(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	gone := filepath.Join(t.TempDir(), "deleted-root")
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := AgreeRoot(checkout, ResolvedRoot{Path: gone}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	resolved := ResolvedRoot{Path: t.TempDir(), Origin: RootOriginDefault}
	err := AgreeRoot(checkout, resolved)
	var split *SplitRootError
	if !errors.As(err, &split) || !split.Gone {
		t.Fatalf("AgreeRoot() over a missing root = %v, want a SplitRootError for a root that is gone", err)
	}
	marker := filepath.Join(checkout, ".git", "yoyodyne", "state-root")
	for _, want := range []string{gone, "no longer exists", RebindCommand, resolved.Path, marker,
		fmt.Sprintf("process %d", os.Getpid()), "recorded there by", time.Now().Format("2006-01-02")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}

	rebinding, err := RebindRoot(checkout, resolved)
	if err != nil || !rebinding.Replaced || rebinding.Before.Recorded != gone {
		t.Fatalf("RebindRoot() = %+v, %v; want the missing root replaced", rebinding, err)
	}
	if err := AgreeRoot(checkout, resolved); err != nil {
		t.Fatalf("AgreeRoot() after the rebind = %v", err)
	}
	if again, err := RebindRoot(checkout, resolved); err != nil || again.Replaced {
		t.Fatalf("RebindRoot() on an agreeing marker = %+v, %v; want nothing to do", again, err)
	}
}

// A marker naming a root still on disk is a second root, and rebinding it would
// be moving the product's state off where it is: refused, as every command is.
func TestRebindRefusesAMarkerWhoseRootIsStillThere(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	kept := ResolvedRoot{Path: t.TempDir()}
	if err := AgreeRoot(checkout, kept); err != nil {
		t.Fatal(err)
	}
	_, err := RebindRoot(checkout, ResolvedRoot{Path: t.TempDir()})
	var split *SplitRootError
	if !errors.As(err, &split) || split.Gone {
		t.Fatalf("RebindRoot() over a root still on disk = %v, want the split refusal", err)
	}
	if marker, _ := ReadRootMarker(checkout); marker.Recorded != kept.Path {
		t.Fatalf("the refused rebind rewrote the marker to %q", marker.Recorded)
	}
}

// A marker recorded before the writer was kept is one line and nothing beside
// it; it still reads, and says it names no writer rather than inventing one.
func TestAPathOnlyMarkerStillReads(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	root := t.TempDir()
	marker := filepath.Join(checkout, ".git", "yoyodyne", "state-root")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(root+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := ReadRootMarker(checkout)
	if err != nil || read.Recorded != root || read.Writer != "" || read.WrittenAt.IsZero() {
		t.Fatalf("ReadRootMarker() = %+v, %v; want the root, no writer, and the file's time", read, err)
	}
	if !strings.Contains(read.WrittenBy(), "recorded no account of itself") {
		t.Fatalf("WrittenBy() = %q", read.WrittenBy())
	}
	if err := AgreeRoot(checkout, ResolvedRoot{Path: root}); err != nil {
		t.Fatalf("AgreeRoot() on a path-only marker = %v", err)
	}
}

// The marker the older builds read is still the one line of a root: the writer
// goes beside it, so a build already deployed reads a marker a newer one wrote.
func TestTheMarkerStaysOneLineAndTheWriterGoesBesideIt(t *testing.T) {
	t.Parallel()

	checkout := gitCheckout(t)
	root := t.TempDir()
	if err := AgreeRoot(checkout, ResolvedRoot{Path: root}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(checkout, ".git", "yoyodyne", "state-root"))
	if err != nil || string(content) != root+"\n" {
		t.Fatalf("marker = %q, %v; want exactly the root", content, err)
	}
	read, err := ReadRootMarker(checkout)
	if err != nil || !strings.Contains(read.Writer, fmt.Sprintf("process %d", os.Getpid())) {
		t.Fatalf("ReadRootMarker() = %+v, %v; want this process named as the writer", read, err)
	}
}

// Inside a test binary a marker is written only under the temporary directory,
// so no test can leave one in a real checkout's Git directory — the operator's
// primary checkout, for a run's worktree, whose .git every worktree shares.
func TestATestBinaryRefusesAMarkerOutsideTheTemporaryDirectory(t *testing.T) {
	t.Parallel()

	if err := refuseMarkerOutsideTemporary(filepath.Join(t.TempDir(), ".git")); err != nil {
		t.Fatalf("a Git directory under t.TempDir() was refused: %v", err)
	}
	outside := filepath.Join(string(filepath.Separator), "not-a-temporary-directory", "checkout", ".git")
	err := refuseMarkerOutsideTemporary(outside)
	if err == nil || !strings.Contains(err.Error(), outside) {
		t.Fatalf("refuseMarkerOutsideTemporary(%s) = %v, want a refusal naming it", outside, err)
	}
	escaping := filepath.Join(os.TempDir(), "..", "checkout", ".git")
	if err := refuseMarkerOutsideTemporary(escaping); err == nil {
		t.Fatalf("refuseMarkerOutsideTemporary(%s) let a path out of the temporary directory through", escaping)
	}
}
