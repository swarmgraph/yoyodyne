package supervise_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// endsOnItsOwn runs the program it is given in its own place, with a deadline
// set first: the launch takes the server out of this test's process group, so a
// test that dies would otherwise leave it running.
const endsOnItsOwn = `server=$$
( for i in $(seq 120); do sleep 1; kill -0 $server 2>/dev/null || exit; done; kill -9 $server ) >/dev/null 2>&1 &
exec "$@"`

// The tracker's server, started on a scratch store by the supervisor's own code
// with a real dolt and the launcher the product uses, refuses a connection
// without its password and with a wrong one, takes one with it, and leaves
// neither the password nor dolt's own secret in any file under the store. No
// tracker command is run. The results it logs are recorded in
// docs/diagnoses/yoyodyne-real-dolt-check.md.
func TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeavesNoSecret(t *testing.T) {
	dolt, err := exec.LookPath("dolt")
	if err != nil {
		t.Skip("SKIPPED, not passed: dolt was not found")
	}
	// Nothing set on this machine may point the tracker at the live store, or
	// give the clients below a password this test did not.
	t.Setenv("BEADS_DIR", "")
	t.Setenv("DOLT_CLI_PASSWORD", "")
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository, home := filepath.Join(scratch, "repository"), filepath.Join(scratch, "home")
	calc := filepath.Join(repository, ".beads", beads.EmbeddedDatabase, "calc")
	for _, directory := range []string{calc, home} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte(".beads/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(directory string, environ []string, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.Dir = directory
		command.Env = append(os.Environ(), append([]string{"HOME=" + home, "DOLT_ROOT_PATH=" + home, "DOLT_DISABLE_EVENT_FLUSH=1"}, environ...)...)
		said, err := command.CombinedOutput()
		return strings.TrimSpace(string(said)), err
	}
	must := func(directory, name string, args ...string) string {
		t.Helper()
		said, err := run(directory, nil, name, args...)
		if err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, said)
		}
		return said
	}
	// Without this, asking dolt its version also asks its maker for the latest.
	must(home, dolt, "config", "--global", "--add", "versioncheck.disabled", "true")
	t.Logf("dolt: %s, %s", dolt, must(home, dolt, "version"))
	must(repository, "git", "init", "-q")
	must(calc, dolt, "init", "--name", "t", "--email", "t@example.invalid")

	runner := execution.OSProcessRunner{}
	server := &supervise.TrackerServer{
		Tracker:    beads.Client{Runner: runner, Dir: repository, Timeout: 30 * time.Second},
		Repository: repository, StateRoot: filepath.Join(scratch, "state"), Product: "calc", Build: "b", Runner: runner,
		Program: func() (string, error) { return dolt, nil },
		Launch: func(program string, args []string, dir string, environ []string, logRoot, log string) (int, error) {
			t.Logf("launched: %s %s", program, strings.Join(args, " "))
			return slack.DetachedLauncher{}.Launch(slack.Launch{
				Program: "/bin/sh", Args: slices.Concat([]string{"-c", endsOnItsOwn, "sh", program}, args),
				Dir: dir, Env: environ, LogRoot: logRoot, Log: log,
			})
		},
		Log: t.Logf,
	}
	if err := os.MkdirAll(server.StateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	store, database := server.Tracker.LiveStore()
	if store != filepath.Join(repository, ".beads") {
		t.Fatalf("the store is %s, want the scratch one under %s", store, repository)
	}
	stopped := false
	stop := func(ctx context.Context) {
		said, err := server.StopIfRunning(ctx, "the check is over")
		if err != nil {
			t.Errorf("stop the server: %v", err)
		}
		stopped = err == nil && said != ""
	}
	t.Cleanup(func() {
		if !stopped {
			stop(context.Background())
		}
	})
	server.Look(t.Context(), time.Now())
	record, _, err := beads.ReadServerRecord(store)
	if err != nil || record.State != beads.ServerRunning {
		said, _ := os.ReadFile(filepath.Join(server.StateRoot, "services", "tracker-server-calc", "server.log"))
		t.Fatalf("the server did not start: %#v, %v; dolt said %s", record, err, said)
	}

	credentials, err := os.ReadFile(record.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	_, password, _ := strings.Cut(strings.TrimSpace(string(credentials)), "password=")
	info, err := os.ReadFile(filepath.Join(filepath.Dir(record.Credentials), "local", "sql-server.info"))
	if err != nil {
		t.Fatal(err)
	}
	local := strings.TrimSpace(string(info[bytes.LastIndexByte(info, ':')+1:]))
	if password == "" || local == "" {
		t.Fatal("the server's secrets are not in its folder under the services folder")
	}
	mask := strings.NewReplacer(password, "(password)", local, "(dolt's own secret)")

	// An empty password is how dolt's client connects with none: left unset, it
	// asks for one at the terminal.
	ask := func(with, statement string) (string, error) {
		said, err := run(home, []string{"DOLT_CLI_PASSWORD=" + with}, dolt, "--host", "127.0.0.1", "--port", strconv.Itoa(record.Port), "--no-tls",
			"--user", beads.ServerUser, "sql", "-r", "csv", "-q", statement)
		return mask.Replace(said), err
	}
	refused := func(check, with string) {
		said, err := ask(with, "SELECT 1")
		passed := err != nil && strings.Contains(said, "Access denied")
		t.Logf("%s: refused=%t: %v: %s", check, passed, err, said)
		if !passed {
			t.Errorf("%s was not refused", check)
		}
	}
	refused("check 1, a connection without the password", "")
	refused("check 2, a connection with a wrong password", strings.Repeat("0", len(password)))

	// Written and committed, so the server has put content in the store.
	if said, err := ask(password, "USE calc; CREATE TABLE used (id INT PRIMARY KEY, word VARCHAR(20)); INSERT INTO used VALUES (1, 'kept'); CALL DOLT_COMMIT('-Am', 'used');"); err != nil {
		t.Errorf("check 3, a write with the password: %v: %s", err, said)
	}
	said, err := ask(password, "SELECT word FROM calc.used")
	t.Logf("check 3, a connection with the password: accepted=%t: %v: %s", err == nil, err, said)
	if err != nil || !strings.Contains(said, "kept") {
		t.Errorf("check 3, a connection with the password was not accepted")
	}

	// The password is also looked for as the bytes it is the hex form of.
	raw, err := hex.DecodeString(password)
	if err != nil {
		t.Fatal(err)
	}
	holding := func(root string) (held []string, read int) {
		t.Helper()
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if path == filepath.Join(database, beads.ServerLink) && entry.Type() == fs.ModeSymlink {
				return nil // leads to the server's own folder while it runs
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("%s is not a regular file, so what it holds was not read", path)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			read++
			for name, secret := range map[string][]byte{"the password": []byte(password), "the password's bytes": raw, "dolt's own secret": []byte(local)} {
				if bytes.Contains(content, secret) {
					held = append(held, path+" holds "+name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("look for a secret under %s: %v", root, err)
		}
		return held, read
	}
	if held, _ := holding(filepath.Dir(record.Credentials)); len(held) == 0 {
		t.Error("the search finds no secret even in the server's own folder, so it shows nothing about the store")
	}
	// Looked at while it runs too, since dolt removes its own secret when it stops.
	check := func(when string) {
		held, read := holding(store)
		t.Logf("check 4, secrets in the files under the store %s: %d found in %d files", when, len(held), read)
		if len(held) > 0 || read == 0 {
			t.Errorf("check 4, no secret under the store %s: read %d files: %v", when, read, held)
		}
	}
	check("while the server ran")
	stop(t.Context())
	if runs, err := server.Tracker.ServerProcessRuns(t.Context(), record.PID, database); runs || err != nil {
		t.Errorf("the server still runs: %v, %v", runs, err)
	}
	check("after the server stopped")
}
