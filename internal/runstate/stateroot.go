package runstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// StateHomeVariable is the explicit instruction that moves the state root for
// one shell, and it wins over everything else because it is one.
const StateHomeVariable = home.StateHomeVariable

// MachineFileName is the machine's own settings file, kept at
// `~/.yoyodyne/machine.yaml`. It describes this machine rather than any
// project, which is why the state root is set here and never in a project file:
// a project file is committed and read on every machine that checks the project
// out, and one that sets the key is refused.
const MachineFileName = home.MachineFileName

// The origins a state root is reported under, in the order they are consulted.
// `yoyo config show --origins` and `yoyo doctor` print them, so they are named
// in the vocabulary an operator would type to change the value.
const (
	RootOriginEnvironment    = home.OriginEnvironment
	RootOriginXDG            = home.OriginXDG
	RootOriginDefault        = home.OriginDefault
	RootOriginEarlierDefault = home.OriginEarlierDefault
)

// ResolvedRoot is the state root one process resolved and the layer it came
// from. The state root is the machine home: the two are one directory.
type ResolvedRoot = home.Resolved

// MachinePath is where this machine's settings file is, whether or not it
// exists.
func MachinePath(userHomeDir func() (string, error)) (string, error) {
	return home.MachinePath(userHomeDir)
}

// ResolveRoot is the one resolution of the state root every process makes:
// YOYODYNE_STATE_HOME, then state_root in the machine file, then
// XDG_STATE_HOME/yoyodyne, then the machine home `~/.yoyodyne` — or the
// platform's earlier default home, where that exists and `~/.yoyodyne` does
// not, until the migration moves it. home.Resolve is the resolution; this is
// its name where run state is concerned.
//
// It resolves and never guards. A process that opens a product's records under
// the root agrees it with the checkout's marker first, through AgreeRoot, which
// is what the command package's productStateRoot does; only the surfaces that
// report the root without opening anything under it — `yoyo config show`,
// `yoyo doctor`, and `yoyo home` — call this without that.
// TestNothingOpensTheStateRootUnguarded in the command package holds every
// caller to that list.
func ResolveRoot(getenv func(string) string, userHomeDir func() (string, error), goos string) (ResolvedRoot, error) {
	return home.Resolve(getenv, userHomeDir, goos)
}

// RootMarkerName is the marker's path inside the primary checkout's Git
// directory. It lives there rather than in the working tree because it is a fact
// about this machine's checkout and nothing a commit should carry.
const RootMarkerName = "yoyodyne/state-root"

// RootMarkerPath is where the state-root marker of a checkout is, and the Git
// directory it sits in. Both are empty for a directory that is not a Git
// checkout: there is nowhere to record a root, so nothing is recorded or
// compared. It reads the filesystem rather than asking Git, as configuration
// discovery does, so every process can answer it before anything else runs.
func RootMarkerPath(checkout string) (marker string, gitDirectory string, err error) {
	gitDirectory, err = home.CommonGitDirectory(checkout)
	if err != nil || gitDirectory == "" {
		return "", "", err
	}
	return filepath.Join(gitDirectory, filepath.FromSlash(RootMarkerName)), gitDirectory, nil
}

// RootWriterName is the account of who recorded the marker, kept beside it
// rather than in it. The marker itself stays the one line of a root that every
// build, older ones included, reads as the whole of the file: a build already
// deployed that met a second line would read the marker as naming a root that
// is not its own and refuse every command.
const RootWriterName = RootMarkerName + ".writer"

// RootMarker is what one checkout's marker says: the root recorded, and where
// the record is. Recorded is empty where no process has recorded one yet, and
// Path is empty for a directory that is not a Git checkout. Writer is the
// process that recorded it, in words, and WrittenAt when; a marker recorded
// before the writer was kept names no writer, and its time is the marker's own
// modification time, since a marker is created once and never rewritten in
// place.
type RootMarker struct {
	Path     string
	Recorded string
	Writer   string
	// Origin is the layer the recording process resolved the root from — the
	// environment variable, the machine file, or a default — which is what says
	// which environment set it when a launch job and a shell disagree. Empty for
	// a marker recorded before the origin was kept.
	Origin    string
	WrittenAt time.Time
}

// ReadRootMarker reads a checkout's marker without writing it, for the surfaces
// that report whether it agrees rather than taking part in it.
func ReadRootMarker(checkout string) (RootMarker, error) {
	marker, _, err := RootMarkerPath(checkout)
	if err != nil || marker == "" {
		return RootMarker{}, err
	}
	content, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return RootMarker{Path: marker}, nil
	}
	if err != nil {
		return RootMarker{}, fmt.Errorf("read the state-root marker %s: %w", marker, err)
	}
	read := RootMarker{Path: marker, Recorded: strings.TrimSpace(string(content))}
	if info, err := os.Stat(marker); err == nil {
		read.WrittenAt = info.ModTime()
	}
	// The account beside it is a description and never a gate: one that cannot
	// be read leaves the marker exactly as usable, naming no writer.
	if account, err := os.ReadFile(marker + ".writer"); err == nil {
		for _, line := range strings.Split(string(account), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch strings.TrimSpace(key) {
			case "writer":
				read.Writer = value
			case "origin":
				read.Origin = value
			case "at":
				if at, err := time.Parse(time.RFC3339, value); err == nil {
					read.WrittenAt = at
				}
			}
		}
	}
	return read, nil
}

// Agrees reports whether the marker leaves a process on this root free to
// start: nothing recorded yet, or the same directory recorded.
func (m RootMarker) Agrees(root string) bool {
	return m.Recorded == "" || sameRoot(m.Recorded, root)
}

// RootGone reports whether the marker names a root that is not on disk. Such a
// marker splits nothing — there is no state at the root it names to split from
// — so it is a stale record rather than a second root, and RebindRoot replaces
// it.
func (m RootMarker) RootGone() bool {
	if m.Recorded == "" {
		return false
	}
	_, err := os.Stat(m.Recorded)
	return errors.Is(err, os.ErrNotExist)
}

// WrittenBy says which process recorded the marker and when, in a phrase that
// follows "recorded".
func (m RootMarker) WrittenBy() string {
	at := "at a time nothing recorded"
	if !m.WrittenAt.IsZero() {
		at = "at " + m.WrittenAt.Local().Format(time.RFC3339)
	}
	from := ""
	if m.Origin != "" {
		from = ", which resolved it from " + m.Origin
	}
	if m.Writer == "" {
		return "by a process that recorded no account of itself, " + at
	}
	return "by " + m.Writer + from + ", " + at
}

// RebindCommand is the one command that replaces a marker naming a root that
// is gone.
const RebindCommand = "yoyo state-root rebind"

// SplitRootError is the refusal the marker exists for: this process resolved a
// root other than the one the checkout's state already lives in. Where the
// recorded root is gone from disk the refusal is the stale-marker one instead,
// and names the command that replaces it.
type SplitRootError struct {
	Marker   string
	Recorded string
	Resolved ResolvedRoot
	// WrittenBy is RootMarker.WrittenBy of the marker that refused.
	WrittenBy string
	// Gone is whether the recorded root is missing from disk.
	Gone bool
}

func (e *SplitRootError) Error() string {
	if e.Gone {
		return fmt.Sprintf("this checkout's state-root marker %s names %s, which no longer exists; it was recorded there %s. "+
			"This process resolved %s (from %s), and it refuses to start rather than open a root the marker disagrees with. "+
			"Nothing is split, because there is no state at the recorded root: `%s` records %s in the marker's place",
			e.Marker, e.Recorded, e.WrittenBy, e.Resolved.Path, e.Resolved.Origin, RebindCommand, e.Resolved.Path)
	}
	return fmt.Sprintf("this checkout's state is kept at %s, recorded in %s %s, "+
		"and this process resolved %s (from %s); one product's state is never split across two roots, so this refuses to start. "+
		"If %s is where it belongs, move it deliberately: stop the product with `yoyo stop`, move the directory, "+
		"change the setting, and run `%s`. Otherwise remove whatever set the second root",
		e.Recorded, e.Marker, e.WrittenBy, e.Resolved.Path, e.Resolved.Origin, e.Resolved.Path, RebindCommand)
}

// AgreeRoot records the root this process resolved in the checkout's marker,
// or refuses with a SplitRootError where the marker already names a different
// one. It is called by every process that opens the state root for a product,
// before it opens anything under it. A checkout that is not a Git repository
// has nowhere to keep the marker and is let through.
//
// The marker is created rather than replaced, so of two processes starting at
// once on two roots exactly one records its root and the other reads it back
// and refuses. The one that created it then writes who it is beside it.
func AgreeRoot(checkout string, resolved ResolvedRoot) error {
	marker, gitDirectory, err := RootMarkerPath(checkout)
	if err != nil || marker == "" {
		return err
	}
	if err := refuseMarkerOutsideTemporary(gitDirectory); err != nil {
		return err
	}
	root, err := repowrite.NewRoot(gitDirectory)
	if err != nil {
		return fmt.Errorf("record the state root in %s: %w", gitDirectory, err)
	}
	_, created, err := root.CreateFile(RootMarkerName, []byte(resolved.Path+"\n"))
	if err != nil {
		return fmt.Errorf("record the state root in %s: %w", marker, err)
	}
	if created {
		if _, err := root.WriteFile(RootWriterName, writerAccount(resolved, time.Now())); err != nil {
			return fmt.Errorf("record who recorded the state root beside %s: %w", marker, err)
		}
	}
	read, err := ReadRootMarker(checkout)
	if err != nil {
		return err
	}
	if !read.Agrees(resolved.Path) {
		return &SplitRootError{Marker: read.Path, Recorded: read.Recorded, Resolved: resolved,
			WrittenBy: read.WrittenBy(), Gone: read.RootGone()}
	}
	return nil
}

// Rebinding is what RebindRoot did: the marker as it stood before, and whether
// it was replaced. Replaced is false where there was nothing to do.
type Rebinding struct {
	Before   RootMarker
	Root     ResolvedRoot
	Replaced bool
}

// RebindRoot replaces a checkout's marker with the root this process resolved,
// and only where doing so splits nothing: the marker names no root, names this
// one already, or names one that is gone from disk. A marker naming a root that
// is still there is a second root rather than a stale record, and is refused
// with the SplitRootError every other command gives, because the state at the
// recorded root is the product's and moving off it is a deliberate move.
func RebindRoot(checkout string, resolved ResolvedRoot) (Rebinding, error) {
	before, err := ReadRootMarker(checkout)
	if err != nil {
		return Rebinding{}, err
	}
	if before.Path == "" {
		return Rebinding{}, fmt.Errorf("%s is not a Git checkout, so it keeps no state-root marker to rebind", checkout)
	}
	if before.Recorded == "" || before.Agrees(resolved.Path) {
		if err := AgreeRoot(checkout, resolved); err != nil {
			return Rebinding{}, err
		}
		return Rebinding{Before: before, Root: resolved}, nil
	}
	if !before.RootGone() {
		return Rebinding{}, &SplitRootError{Marker: before.Path, Recorded: before.Recorded, Resolved: resolved,
			WrittenBy: before.WrittenBy()}
	}
	_, gitDirectory, err := RootMarkerPath(checkout)
	if err != nil {
		return Rebinding{}, err
	}
	if err := refuseMarkerOutsideTemporary(gitDirectory); err != nil {
		return Rebinding{}, err
	}
	root, err := repowrite.NewRoot(gitDirectory)
	if err != nil {
		return Rebinding{}, fmt.Errorf("rebind the state root in %s: %w", gitDirectory, err)
	}
	// The account is written first, so a marker never stands beside the
	// previous writer's account of a root it no longer names.
	if _, err := root.WriteFile(RootWriterName, writerAccount(resolved, time.Now())); err != nil {
		return Rebinding{}, fmt.Errorf("record who rebound the state root beside %s: %w", before.Path, err)
	}
	if _, err := root.WriteFile(RootMarkerName, []byte(resolved.Path+"\n")); err != nil {
		return Rebinding{}, fmt.Errorf("rebind the state root in %s: %w", before.Path, err)
	}
	return Rebinding{Before: before, Root: resolved, Replaced: true}, nil
}

// writerAccount is what is kept beside the marker about the process that
// recorded it: its process id and command line, the layer it resolved the root
// from, and the moment.
func writerAccount(resolved ResolvedRoot, now time.Time) []byte {
	command := "an unnamed command"
	if len(os.Args) > 0 {
		arguments := append([]string{filepath.Base(os.Args[0])}, os.Args[1:]...)
		command = "`" + strings.Join(arguments, " ") + "`"
	}
	return []byte(fmt.Sprintf("writer: process %d, running %s\norigin: %s\nat: %s\n",
		os.Getpid(), command, resolved.Origin, now.Format(time.RFC3339)))
}

// refuseMarkerOutsideTemporary is the fixture that keeps tests out of real
// checkouts. Inside a test binary a marker may be written only into a Git
// directory under the temporary directory: a test that ran a command against
// this repository's own configuration would otherwise record its temporary
// state root in the shared Git directory of the checkout it ran in — the
// operator's primary checkout, for a run's worktree — and every command there
// would refuse to start once the temporary directory was gone. It holds for
// every test in every package without any of them opting in, and it is inert
// outside a test binary.
func refuseMarkerOutsideTemporary(gitDirectory string) error {
	if !testing.Testing() {
		return nil
	}
	if withinTemporary(gitDirectory) {
		return nil
	}
	return fmt.Errorf("a test tried to record a state-root marker in %s, which is not under the temporary directory %s; "+
		"a test records a marker only in a checkout it made under t.TempDir(), never in a real one", gitDirectory, os.TempDir())
}

func withinTemporary(path string) bool {
	temporary := os.TempDir()
	for _, candidate := range [][2]string{{temporary, path}, {resolvedPath(temporary), resolvedPath(path)}} {
		relative, err := filepath.Rel(filepath.Clean(candidate[0]), filepath.Clean(candidate[1]))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolvedPath follows symlinks as far as the path exists, so a temporary
// directory reached through /var and one reached through /private/var compare
// as the one directory they are.
func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolvedPath(parent), filepath.Base(path))
}

// sameRoot compares two roots as directories rather than as strings where both
// exist, so a root reached through a symlink is the root it links to.
func sameRoot(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
