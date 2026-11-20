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
			command := exec.Command("make", "--no-print-directory", "-f", makefile, "fmtcheck", "VERSION=fmtcheck-test")
			command.Dir = root
			command.Env = append(makeEnvironment(t), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
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

// makeEnvironment is the environment a make child runs in. On macOS /usr/bin/make
// is a shim that asks xcrun where the real tool is, and xcrun writes a cache file
// under the user's per-user temporary directory, which does not follow TMPDIR. A
// sandbox that grants only TMPDIR refuses that write, and xcrun then prints a
// diagnostic these cases read as the target's own. The shim skips xcrun when
// DEVELOPER_DIR names the toolchain, so it is set here from xcode-select, which
// only reads a link. A developer directory the caller already set is kept.
func makeEnvironment(t *testing.T) []string {
	t.Helper()

	environment := os.Environ()
	if runtime.GOOS != "darwin" || os.Getenv("DEVELOPER_DIR") != "" {
		return environment
	}
	out, err := exec.Command("xcode-select", "-p").Output()
	if err != nil {
		return environment
	}
	if directory := strings.TrimSpace(string(out)); directory != "" {
		environment = append(environment, "DEVELOPER_DIR="+directory)
	}
	return environment
}

// A make child must not need xcrun's cache file, or the false failure this
// guards against returns wherever the per-user temporary directory is refused.
func TestMakeEnvironmentNamesTheToolchainOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the make shim that calls xcrun is macOS only")
	}
	requireTool(t, "xcode-select")
	if os.Getenv("DEVELOPER_DIR") != "" {
		t.Skip("DEVELOPER_DIR is already set by the caller")
	}
	for _, entry := range makeEnvironment(t) {
		if strings.HasPrefix(entry, "DEVELOPER_DIR=") && len(entry) > len("DEVELOPER_DIR=") {
			return
		}
	}
	t.Error("makeEnvironment does not set DEVELOPER_DIR, so make would call xcrun and write its cache file")
}
