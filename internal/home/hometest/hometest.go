// Package hometest keeps a package's tests out of the machine's live home.
//
// The supervisor and the commands that start it record themselves in the home
// every process resolves: configuration readers, supervision, retired jobs. A
// test that reaches that resolution without a home of its own writes those
// records beside the running product's, where the product's own comparison
// later reads a record pointing at a temporary file and clears it — or a
// status read shows it as a part that is not there. GuardLiveHomes is the check
// that no test in a package left one there: it is a TestMain wrapper, so it
// holds for every test in the package, including one written next year.
//
// A record is the tests' when it names something only they could: a path under
// the private temporary directory the guard gives the package's tests, which
// every t.TempDir and every process they start lies under, or the test
// process's own pid. Anything else that changed meanwhile is the running
// product's own work and is left alone, and the guard removes nothing from the
// live home: what it finds is reported, and the test binary fails.
package hometest

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

// GuardLiveHomes runs a package's tests under a private temporary directory
// and returns their exit code, or 1 when they left a record in any live
// product's state directory. It resolves the live homes before anything is
// changed, so call it first in TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(hometest.GuardLiveHomes(m.Run, os.Stderr)) }
func GuardLiveHomes(run func() int, stderr io.Writer) int {
	return Guard(LiveHomes(), run, stderr)
}

// LiveHomes is every home a process on this machine may resolve when nothing
// moves it: the one this environment resolves, the default, and the earlier
// default a machine not yet migrated still uses.
func LiveHomes() []string {
	var homes []string
	if resolved, err := home.Resolve(os.Getenv, os.UserHomeDir, runtime.GOOS); err == nil {
		homes = append(homes, resolved.Path)
	}
	if path, err := home.DefaultPath(os.UserHomeDir); err == nil {
		homes = append(homes, path)
	}
	if path, err := home.EarlierDefault(os.Getenv, os.UserHomeDir, runtime.GOOS); err == nil {
		homes = append(homes, path)
	}
	slices.Sort(homes)
	return slices.Compact(homes)
}

// Guard is GuardLiveHomes over the homes given. TMPDIR is pointed at a private
// directory while run runs and restored afterwards.
func Guard(homes []string, run func() int, stderr io.Writer) int {
	before := snapshot(homes)
	private, err := os.MkdirTemp("", "yoyodyne-test-")
	if err != nil {
		fmt.Fprintf(stderr, "make the tests' private temporary directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(private)
	previous, had := os.LookupEnv("TMPDIR")
	os.Setenv("TMPDIR", private)
	code := run()
	if had {
		os.Setenv("TMPDIR", previous)
	} else {
		os.Unsetenv("TMPDIR")
	}
	if leaked := leaks(homes, before, markers(private, os.Getpid())); len(leaked) > 0 {
		fmt.Fprintf(stderr, "tests left records in the live product state, which the running product reads as its own; give the test a state root of its own:\n")
		for _, path := range leaked {
			fmt.Fprintf(stderr, "  %s\n", path)
		}
		code = 1
	}
	return code
}

type fileState struct {
	size     int64
	modified time.Time
}

// files is the files under the live product directories at one moment.
type files map[string]fileState

func snapshot(homes []string) files {
	found := files{}
	for _, directory := range productDirectories(homes) {
		filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || !entry.Type().IsRegular() {
				return nil
			}
			if info, err := entry.Info(); err == nil {
				found[path] = fileState{size: info.Size(), modified: info.ModTime()}
			}
			return nil
		})
	}
	return found
}

// productDirectories is each product's records directory under each home.
func productDirectories(homes []string) []string {
	var directories []string
	for _, root := range homes {
		directories = append(directories, home.ProductDirectories(root)...)
	}
	return directories
}

// marker is one way a record says a test wrote it.
type marker func(content []byte) bool

func markers(private string, pid int) []marker {
	paths := [][]byte{[]byte(private)}
	if resolved, err := filepath.EvalSymlinks(private); err == nil && resolved != private {
		paths = append(paths, []byte(resolved))
	}
	ownPID := regexp.MustCompile(`"pid":\s*` + strconv.Itoa(pid) + `\b`)
	return []marker{
		func(content []byte) bool {
			return slices.ContainsFunc(paths, func(path []byte) bool { return bytes.Contains(content, path) })
		},
		ownPID.Match,
	}
}

// leaks is every file under the homes' product directories that is new or
// changed since before and carries one of the markers.
func leaks(homes []string, before files, marks []marker) []string {
	var leaked []string
	for path, state := range snapshot(homes) {
		if earlier, found := before[path]; found && earlier.size == state.size && earlier.modified.Equal(state.modified) {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if slices.ContainsFunc(marks, func(m marker) bool { return m(content) }) {
			leaked = append(leaked, path)
		}
	}
	slices.Sort(leaked)
	return leaked
}
