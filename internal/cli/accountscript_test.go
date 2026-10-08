package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// bin/yoyo-account signs an account in under the machine home's accounts
// directory, and it has to be the directory the harness reads them from. It
// asks the binary where that is (`yoyo project list --home`) rather than working
// it out in shell, so each machine state below is answered by the harness's own
// resolution: where the login lands is compared with what runstate.ResolveRoot
// and config.AccountConfigDirectory say for the same user home.
func TestTheAccountScriptSignsInWhereTheHarnessReadsTheAccount(t *testing.T) {
	t.Parallel()

	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		// arrange lays out the user home and returns the home the account
		// must land under, or "" where it must land in ~/.yoyodyne.
		arrange func(t *testing.T, userHome string) string
	}{
		{
			name:    "nothing is there yet, so the home is ~/.yoyodyne",
			arrange: func(t *testing.T, userHome string) string { return "" },
		},
		{
			name: "~/.yoyodyne holds only machine.yaml and the earlier builds' home holds the state",
			arrange: func(t *testing.T, userHome string) string {
				writeMachineFile(t, userHome, "# no state_root: the home is wherever the state is\n")
				earlier, err := home.EarlierDefault(func(string) string { return "" }, func() (string, error) { return userHome, nil }, runtime.GOOS)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(earlier, 0o700); err != nil {
					t.Fatal(err)
				}
				return earlier
			},
		},
		{
			name: "machine.yaml moves the home with state_root",
			arrange: func(t *testing.T, userHome string) string {
				moved := filepath.Join(t.TempDir(), "moved-home")
				writeMachineFile(t, userHome, "state_root: "+moved+"\n")
				return moved
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			userHome := t.TempDir()
			expectedHome := tc.arrange(t, userHome)
			if expectedHome == "" {
				expectedHome = filepath.Join(userHome, home.DirectoryName)
			}

			// The harness's own answer for this user home, which is what the
			// script must agree with.
			resolved, err := runstate.ResolveRoot(func(string) string { return "" }, func() (string, error) { return userHome, nil }, runtime.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			want := config.AccountConfigDirectory(resolved.Path, "second")
			if want != filepath.Join(expectedHome, "accounts", "second") {
				t.Fatalf("the harness reads the account from %s, but this case expects %s: the case no longer describes the machine state it names", want, expectedHome)
			}

			tools := accountScriptTools(t, program)
			stdout, stderr, err := runAccountScript(t, userHome, tools)
			if err != nil {
				t.Fatalf("yoyo-account: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			got, err := os.ReadFile(filepath.Join(tools, "claude-config-dir"))
			if err != nil {
				t.Fatalf("the login was never run: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if string(got) != want {
				t.Fatalf("the login signed in under %s, want %s, where the harness reads the account", got, want)
			}
			if info, err := os.Stat(want); err != nil || !info.IsDir() {
				t.Fatalf("the account's home %s was not made (%v)", want, err)
			}
			if !strings.Contains(stdout, want) || !strings.Contains(stdout, "pool: reserved") {
				t.Errorf("the script's account of itself does not name %s and the entry to paste:\n%s", want, stdout)
			}
			if fresh := filepath.Join(userHome, home.DirectoryName); expectedHome != fresh {
				if _, err := os.Stat(filepath.Join(fresh, "accounts")); !os.IsNotExist(err) {
					t.Fatalf("the script wrote under %s, which the harness does not read accounts from (%v)", fresh, err)
				}
			}
		})
	}
}

// Without a yoyo to ask, the script says so and stops before it makes a
// directory or opens a login, rather than guessing where the home is.
func TestTheAccountScriptRefusesWithoutAYoyoToAsk(t *testing.T) {
	t.Parallel()

	userHome := t.TempDir()
	tools := accountScriptTools(t, "")
	stdout, stderr, err := runAccountScript(t, userHome, tools)
	if err == nil {
		t.Fatalf("yoyo-account succeeded with no yoyo on PATH\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "yoyo is not on PATH") || !strings.Contains(stderr, "YOYO=") {
		t.Errorf("the refusal does not say yoyo is missing and how to name it:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(tools, "claude-config-dir")); !os.IsNotExist(err) {
		t.Fatalf("the login ran although the home was never asked for (%v)", err)
	}
	for _, guessed := range []string{
		filepath.Join(userHome, home.DirectoryName),
		filepath.Join(userHome, "Library"),
		filepath.Join(userHome, ".local"),
	} {
		if _, err := os.Stat(guessed); !os.IsNotExist(err) {
			t.Errorf("the script made %s although it could not ask where the home is (%v)", guessed, err)
		}
	}
}

func writeMachineFile(t *testing.T, userHome, content string) {
	t.Helper()
	machine := filepath.Join(userHome, home.DirectoryName, home.MachineFileName)
	if err := os.MkdirAll(filepath.Dir(machine), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machine, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// accountScriptTools is a directory to put on the script's PATH: this test
// binary acting as yoyo, unless program is empty, and a stand-in for the
// provider's login that records where it was told to sign in.
func accountScriptTools(t *testing.T, program string) string {
	t.Helper()
	tools := t.TempDir()
	if program != "" {
		yoyo := filepath.Join(tools, "yoyo")
		if err := os.WriteFile(yoyo, []byte("#!/bin/sh\nexec \""+program+"\" \"$@\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	recorded := filepath.Join(tools, "claude-config-dir")
	claude := "#!/bin/sh\n[ \"$1 $2\" = \"auth login\" ] || exit 3\nprintf '%s' \"$CLAUDE_CONFIG_DIR\" > \"" + recorded + "\"\n"
	if err := os.WriteFile(filepath.Join(tools, "claude"), []byte(claude), 0o700); err != nil {
		t.Fatal(err)
	}
	return tools
}

func runAccountScript(t *testing.T, userHome, tools string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	script := exec.CommandContext(ctx, "bash", filepath.Join("..", "..", "bin", "yoyo-account"), "--alias", "second", "--pool", "reserved")
	script.Env = []string{
		"HOME=" + userHome,
		"PATH=" + tools + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		productHelperVariable + "=1",
	}
	// No budget, and yes to the login.
	script.Stdin = strings.NewReader("\nyes\n")
	var stdout, stderr bytes.Buffer
	script.Stdout, script.Stderr = &stdout, &stderr
	err := script.Run()
	return stdout.String(), stderr.String(), err
}
