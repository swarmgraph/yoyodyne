package composition

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Run the repository's target over scratch input, so the recipe rather than a
// copy of its shell logic is what these cases hold to account.
func TestFmtcheck(t *testing.T) {
	t.Parallel()

	requireTool(t, "make")
	requireTool(t, "gofmt")
	makefile, err := filepath.Abs(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		source      string
		formatter   string
		wantFailure bool
		wantStdout  string
		wantStderr  []string
	}{
		{
			name:   "formatted",
			source: "package fixture\n\nvar answer = 42\n",
		},
		{
			name:        "unformatted",
			source:      "package fixture\nvar answer=42\n",
			wantFailure: true,
			wantStdout:  "gofmt needed:\nfixture.go\n",
		},
		{
			name:        "malformed",
			source:      "package fixture\n\nfunc broken(\n",
			wantFailure: true,
			wantStderr:  []string{"fixture.go:", "expected"},
		},
		{
			name:        "formatter failure with diagnostics and empty stdout",
			source:      "package fixture\n",
			formatter:   "#!/bin/sh\nprintf '%s\\n' 'formatter could not read input' >&2\nexit 23\n",
			wantFailure: true,
			wantStderr:  []string{"formatter could not read input", "Error 23"},
		},
		{
			name:        "formatter failure with no output",
			source:      "package fixture\n",
			formatter:   "#!/bin/sh\nexit 29\n",
			wantFailure: true,
			wantStderr:  []string{"Error 29"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := writeFixture(t, map[string]string{"fixture.go": test.source})
			command := exec.Command(makeProgram(t), "--no-print-directory", "-f", makefile, "fmtcheck", "VERSION=fmtcheck-test")
			command.Dir = root
			command.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
			if test.formatter != "" {
				tools := filepath.Join(root, "tools")
				if err := os.Mkdir(tools, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tools, "gofmt"), []byte(test.formatter), 0o755); err != nil {
					t.Fatal(err)
				}
				command.Env = append(command.Env, "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if test.wantFailure {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Errorf("fmtcheck error = %v, want a nonzero exit; stdout:\n%s\nstderr:\n%s", err, &stdout, &stderr)
				}
			} else if err != nil {
				t.Errorf("fmtcheck error = %v; stdout:\n%s\nstderr:\n%s", err, &stdout, &stderr)
			}
			if stdout.String() != test.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.wantStdout)
			}
			for _, diagnostic := range test.wantStderr {
				if !strings.Contains(stderr.String(), diagnostic) {
					t.Errorf("stderr = %q, want it to contain %q", stderr.String(), diagnostic)
				}
			}
			if !test.wantFailure && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want no diagnostics for formatted input", stderr.String())
			}
		})
	}
}

// makeProgram is the make these cases run. On macOS /usr/bin/make is a shim
// that looks the real tool up through xcrun, which keeps its lookups in a cache
// file under the user's per-user temporary directory (not TMPDIR) and writes it
// whenever a lookup misses. A sandbox that grants only TMPDIR refuses that
// write, and xcrun's diagnostic lands on stderr, where the formatted case
// requires silence. Setting DEVELOPER_DIR does not avoid this, because the shim
// still goes through the cache. So on macOS the real make inside the active
// developer directory is run directly, which never calls xcrun. Anywhere else,
// or when that file is missing, it is plain "make".
func makeProgram(t *testing.T) string {
	t.Helper()

	if runtime.GOOS != "darwin" {
		return "make"
	}
	out, err := exec.Command("xcode-select", "-p").Output()
	if err != nil {
		return "make"
	}
	real := filepath.Join(strings.TrimSpace(string(out)), "usr", "bin", "make")
	if info, err := os.Stat(real); err != nil || info.IsDir() {
		return "make"
	}
	return real
}

// The false failure comes back if make is run through the shim again. With
// xcrun's own logging switched on, the shim says it is consulting its cache and
// the real make says nothing, so any mention of xcrun in the output fails this.
func TestMakeProgramDoesNotCallXcrunOnMacOS(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("the make shim that calls xcrun is macOS only")
	}
	requireTool(t, "xcode-select")
	program := makeProgram(t)
	if program == "make" {
		t.Skip("no real make in the active developer directory")
	}
	root := writeFixture(t, map[string]string{"Makefile": "probe:\n\t@true\n"})
	command := exec.Command(program, "probe")
	command.Dir = root
	command.Env = append(os.Environ(), "xcrun_log=1", "xcrun_verbose=1", "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s probe error = %v; output:\n%s", program, err, output)
	}
	if strings.Contains(string(output), "xcrun") {
		t.Errorf("%s consulted xcrun, whose cache file a sandbox may refuse to write; output:\n%s", program, output)
	}
}
