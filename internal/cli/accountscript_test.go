package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bin/yoyo-account signs an account in under the machine home's accounts
// directory, and it has to be the directory the harness reads them from. It
// asks the binary where that is, so a home the machine file moved — which the
// variables alone never said — is where the login lands, and nothing is written
// under the home the variables would have named.
func TestTheAccountScriptSignsInUnderAHomeTheMachineFileMoved(t *testing.T) {
	t.Parallel()

	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	userHome := t.TempDir()
	moved := filepath.Join(t.TempDir(), "moved-home")
	machine := filepath.Join(userHome, ".yoyodyne", "machine.yaml")
	if err := os.MkdirAll(filepath.Dir(machine), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machine, []byte("state_root: "+moved+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The binary the script asks is this test binary acting as yoyo, and the
	// provider's login is a stand-in that records where it was told to sign in.
	tools := t.TempDir()
	yoyo := filepath.Join(tools, "yoyo")
	if err := os.WriteFile(yoyo, []byte("#!/bin/sh\nexec \""+program+"\" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	recorded := filepath.Join(tools, "claude-config-dir")
	claude := "#!/bin/sh\n[ \"$1 $2\" = \"auth login\" ] || exit 3\nprintf '%s' \"$CLAUDE_CONFIG_DIR\" > \"" + recorded + "\"\n"
	if err := os.WriteFile(filepath.Join(tools, "claude"), []byte(claude), 0o700); err != nil {
		t.Fatal(err)
	}

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
	if err := script.Run(); err != nil {
		t.Fatalf("yoyo-account: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	want := filepath.Join(moved, "accounts", "second")
	got, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatalf("the login was never run: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if string(got) != want {
		t.Fatalf("the login signed in under %s, want the moved home's %s", got, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("the account's home %s was not made (%v)", want, err)
	}
	if !strings.Contains(stdout.String(), want) || !strings.Contains(stdout.String(), "pool: reserved") {
		t.Errorf("the script's account of itself does not name %s and the entry to paste:\n%s", want, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(userHome, ".yoyodyne", "accounts")); !os.IsNotExist(err) {
		t.Fatalf("the script wrote under the home the machine file moved away from (%v)", err)
	}
}
