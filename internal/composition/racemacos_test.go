package composition

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// `make race-macos` is the race detector over the code built only on macOS,
// for the checks on main. Its packages have to be exactly MACOS_TEST_PACKAGES:
// one the target dropped would be code built only on macOS that nothing runs
// the race detector over, and one it added would be repeating what GitHub's
// race job already runs. The target is run here, with a stand-in for the Go
// command that records what it was asked, so what is held to the list is the
// recipe and not a copy of it, and with a stand-in for uname, so the list is
// held on GitHub's Linux machines too.
func TestTheMacOSRaceCheckRunsExactlyTheMacOSTestPackages(t *testing.T) {
	t.Parallel()

	requireTool(t, "make")
	makefile, err := os.ReadFile(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	want := makeList(t, string(makefile), "MACOS_TEST_PACKAGES")
	if len(want) == 0 {
		t.Fatal("the Makefile's MACOS_TEST_PACKAGES names nothing")
	}

	output, calls := runRaceMacOS(t, "Darwin", "")
	if len(calls) != 1 {
		t.Fatalf("make race-macos ran go test %d times (%q), want once; output:\n%s", len(calls), calls, output)
	}
	words := strings.Fields(calls[0])
	if !slices.Contains(words, "-race") || !slices.Contains(words, "-timeout") {
		t.Fatalf("make race-macos ran %q, want the race detector under the test time limit", calls[0])
	}
	var got []string
	for _, word := range words {
		if strings.HasPrefix(word, "./") {
			got = append(got, word)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("make race-macos runs the race detector over %q, want exactly MACOS_TEST_PACKAGES %q", got, want)
	}
}

// On any other system the target says so, passes, and runs nothing, whether or
// not it is told failing tests.
func TestTheMacOSRaceCheckSaysSoAndPassesOffMacOS(t *testing.T) {
	t.Parallel()

	requireTool(t, "make")
	for _, failing := range []string{"", "TestQueue\n"} {
		output, calls := runRaceMacOS(t, "Linux", failing)
		if !strings.Contains(output, "this is not macOS") || len(calls) != 0 {
			t.Fatalf("off macOS, told failing tests %q, make race-macos said %q and ran %q; want it to say so and run nothing", failing, output, calls)
		}
	}
}

// Told the tests a first run named failing, the target runs only those, with
// the race detector, through scripts/rerun-failing.sh as `make race` does.
func TestTheMacOSRaceCheckRunOnceMoreRunsOnlyTheFailingTests(t *testing.T) {
	t.Parallel()

	requireTool(t, "make")
	output, calls := runRaceMacOS(t, "Darwin", "TestQueue\nTestQueue/held\n")
	want := `test -race -run ^(TestQueue)$ ./... -count=1 -timeout`
	if len(calls) != 1 || !strings.HasPrefix(calls[0], want) {
		t.Fatalf("make race-macos told TestQueue failed ran %q, want one call beginning %q; output:\n%s", calls, want, output)
	}
}

// runRaceMacOS runs `make race-macos` at the top of this checkout with GO set
// to a script that answers `env GOCACHE` with a scratch directory and records
// the arguments of every `go test`, one call to a line, and with a uname ahead
// on PATH that answers system. failing, where not empty, is given as
// YOYODYNE_FAILING_TESTS.
func runRaceMacOS(t *testing.T, system, failing string) (output string, calls []string) {
	t.Helper()

	scratch := t.TempDir()
	record := filepath.Join(scratch, "calls")
	fake := filepath.Join(scratch, "go")
	script := "#!/bin/sh\n" +
		"case $1 in\n" +
		"env) echo \"$FAKE_GO_CACHE\" ;;\n" +
		"test) echo \"$*\" >>\"$FAKE_GO_CALLS\" ;;\n" +
		"esac\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(scratch, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "uname"), []byte("#!/bin/sh\necho "+system+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(makeProgram(t), "--no-print-directory", "race-macos", "GO="+fake)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0",
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GO_CACHE="+filepath.Join(scratch, "cache"), "FAKE_GO_CALLS="+record,
		"YOYODYNE_FAILING_TESTS="+failing)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make race-macos failed (%v):\n%s", err, out)
	}
	recorded, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	return string(out), calls
}
