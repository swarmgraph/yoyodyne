package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scratchTemporaryFixture is a run's scratch folder under a state directory
// whose path has a space in it, as the default one on macOS does
// ("Application Support"), with its temporary folder prepared.
func scratchTemporaryFixture(t *testing.T) (string, ScratchTemporary) {
	t.Helper()

	state := filepath.Join(t.TempDir(), "state dir")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	scratch, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	temporary, err := PrepareScratchTemporary(scratch, "run-one", "")
	if err != nil {
		t.Fatalf("PrepareScratchTemporary() error = %v", err)
	}
	return state, temporary
}

func requireMktemp(t *testing.T) {
	t.Helper()

	for _, program := range []string{"sh", "mktemp"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed here: %v", program, err)
		}
	}
}

// runMktemp runs mktemp with arguments the way a project's script would, by
// name through a shell, in the environment a developer session is given.
func runMktemp(t *testing.T, temporary ScratchTemporary, directory string, arguments ...string) (string, error) {
	t.Helper()

	script := "mktemp"
	for _, argument := range arguments {
		script += " '" + argument + "'"
	}
	command := exec.Command("sh", "-c", script)
	command.Dir = directory
	command.Env = temporary.Environment(os.Environ())
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func TestTheTemporaryFolderAndItsProgramAreInsideTheRunsScratch(t *testing.T) {
	t.Parallel()

	state, temporary := scratchTemporaryFixture(t)
	scratch := filepath.Join(resolve(t, state), "scratch", "run-one")
	if temporary.Folder != filepath.Join(scratch, "tmp") || temporary.Programs != filepath.Join(scratch, "bin") {
		t.Fatalf("PrepareScratchTemporary() = %+v, want tmp and bin inside %s", temporary, scratch)
	}
	info, err := os.Stat(filepath.Join(temporary.Programs, "mktemp"))
	if err != nil || info.Mode()&0o100 == 0 {
		t.Fatalf("the mktemp program = %v, %v; want an executable file", info, err)
	}

	// Preparing again, as a resumed run does, keeps what the run left.
	left := filepath.Join(temporary.Folder, "left")
	if err := os.WriteFile(left, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := PrepareScratchTemporary(scratch, "run-one", ""); err != nil {
		t.Fatalf("PrepareScratchTemporary() again error = %v", err)
	}
	if _, err := os.Stat(left); err != nil {
		t.Fatalf("preparing again removed what the run left: %v", err)
	}

	// It goes with the run's other scratch.
	if err := RemoveScratchDirectory(state, "run-one"); err != nil {
		t.Fatalf("RemoveScratchDirectory() error = %v", err)
	}
	if _, err := os.Stat(temporary.Folder); !os.IsNotExist(err) {
		t.Fatalf("the temporary folder outlived the run's scratch: %v", err)
	}
}

func TestATemporaryFolderIsNotPreparedInAFolderThatIsNotTheRunsScratch(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	other, err := PrepareScratchDirectory(state, "run-two")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if _, err := PrepareScratchTemporary(other, "run-one", ""); err == nil {
		t.Fatal("PrepareScratchTemporary() of another run's scratch succeeded, want a refusal")
	}
	if _, err := PrepareScratchTemporary(resolve(t, state), "run-one", ""); err == nil {
		t.Fatal("PrepareScratchTemporary() of the state directory succeeded, want a refusal")
	}
}

func TestTheEnvironmentPutsTheProgramFirstAndTellsJavaWhereToWrite(t *testing.T) {
	t.Parallel()

	temporary := ScratchTemporary{Folder: "/state dir/scratch/run-one/tmp", Programs: "/state dir/scratch/run-one/bin"}
	got := temporary.Environment([]string{"HOME=/home", "PATH=/usr/bin:/bin", "JAVA_TOOL_OPTIONS=-Xmx1g"})
	want := []string{
		"HOME=/home",
		"PATH=/state dir/scratch/run-one/bin" + string(filepath.ListSeparator) + "/usr/bin:/bin",
		`JAVA_TOOL_OPTIONS=-Djava.io.tmpdir="/state dir/scratch/run-one/tmp" -Xmx1g`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Environment() = %q, want %q", got, want)
	}
	bare := temporary.Environment([]string{"HOME=/home"})
	if !strings.HasPrefix(bare[1], "PATH=/state dir/scratch/run-one/bin"+string(filepath.ListSeparator)) || bare[2] != `JAVA_TOOL_OPTIONS=-Djava.io.tmpdir="/state dir/scratch/run-one/tmp"` {
		t.Fatalf("Environment() of an environment with neither = %q", bare)
	}
}

// Where the caller names no place, the file lands in the run's temporary
// folder; where it names one, it lands there, exactly as mktemp alone would put
// it. The program is installed only on macOS, and the cases are macOS mktemp's
// options: GNU mktemp reads `-t` as a flag with no value.
func TestMktempPutsAFileWithNoPlaceInTheRunsTemporaryFolder(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the mktemp program is installed only on macOS, and these are macOS mktemp's options")
	}
	requireMktemp(t)

	_, temporary := scratchTemporaryFixture(t)
	working := resolve(t, t.TempDir())
	elsewhere := resolve(t, t.TempDir())
	for _, tc := range []struct {
		arguments []string
		in        string
	}{
		{nil, temporary.Folder},
		{[]string{"-d"}, temporary.Folder},
		{[]string{"-q", "-d"}, temporary.Folder},
		{[]string{"-t", "prefix"}, temporary.Folder},
		{[]string{"-d", "-t", "prefix"}, temporary.Folder},
		{[]string{"-dt", "prefix"}, temporary.Folder},
		{[]string{"relative.XXXXXX"}, working},
		{[]string{"-d", "relative.XXXXXX"}, working},
		{[]string{filepath.Join(elsewhere, "absolute.XXXXXX")}, elsewhere},
		{[]string{"-p", elsewhere, "-d"}, elsewhere},
	} {
		made, err := runMktemp(t, temporary, working, tc.arguments...)
		if err != nil {
			t.Errorf("mktemp %q error = %v: %s", tc.arguments, err, made)
			continue
		}
		// A relative template is printed as given, relative to where it ran.
		if !filepath.IsAbs(made) {
			made = filepath.Join(working, made)
		}
		if filepath.Dir(resolve(t, made)) != tc.in {
			t.Errorf("mktemp %q made %s, want it in %s", tc.arguments, made, tc.in)
		}
	}
}

// A developer session's sandbox on macOS refuses the per-user temporary folder
// mktemp uses by default and grants the run's scratch. This builds that
// sandbox around the command, checks it refuses the system's own mktemp -d,
// and then runs `mktemp -d` with no template, by name, in the environment a
// session is given: it has to succeed and make its folder in the run's scratch.
//
// A sandbox cannot be started from inside another one, so where this test is
// itself running in a developer session it skips; the harness's own check stage
// runs it.
func TestMktempWithNoTemplateSucceedsInADeveloperSandbox(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the per-user temporary folder mktemp ignores TMPDIR for is macOS's")
	}
	requireMktemp(t)
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec is not installed here: %v", err)
	}
	userTemporary, err := exec.Command("getconf", "DARWIN_USER_TEMP_DIR").Output()
	if err != nil {
		t.Fatalf("getconf DARWIN_USER_TEMP_DIR error = %v", err)
	}
	refused := resolve(t, strings.TrimSpace(string(userTemporary)))

	_, temporary := scratchTemporaryFixture(t)
	scratch := filepath.Dir(temporary.Folder)
	// Later rules win: everything is allowed, writes to the per-user temporary
	// folder are refused, and the run's scratch, which may sit inside it on a
	// machine whose TMPDIR is the default, is granted again.
	profile := `(version 1)(allow default)` +
		`(deny file-write* (subpath "` + refused + `"))` +
		`(allow file-write* (subpath "` + scratch + `"))`
	sandboxed := func(environment []string, script string) (string, error) {
		command := exec.Command("sandbox-exec", "-p", profile, "/bin/sh", "-c", script)
		command.Env = environment
		output, err := command.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	if output, err := sandboxed(os.Environ(), "true"); err != nil {
		t.Skipf("a sandbox cannot be started here, most likely because this test is already in one: %v: %s", err, output)
	}

	if output, err := sandboxed(os.Environ(), "/usr/bin/mktemp -d"); err == nil {
		_ = os.Remove(output)
		t.Fatalf("the sandbox let the system's mktemp -d make %s, so it does not refuse what a developer session refuses and this test proves nothing", output)
	}

	made, err := sandboxed(temporary.Environment(os.Environ()), "mktemp -d")
	if err != nil {
		t.Fatalf("mktemp -d in a developer sandbox error = %v: %s", err, made)
	}
	if filepath.Dir(resolve(t, made)) != temporary.Folder {
		t.Fatalf("mktemp -d made %s, want it in the run's temporary folder %s", made, temporary.Folder)
	}
	if info, err := os.Stat(made); err != nil || !info.IsDir() {
		t.Fatalf("mktemp -d reported %s, but it is not a folder: %v", made, err)
	}
}
