package checks

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// runRestricted runs a shell command under the restrictions and returns what
// it wrote and whether it exited zero. It skips the test where this process is
// already inside a sandbox, which cannot start another: a developer's own Bash
// is one, and the harness's run of the checks is not.
func runRestricted(t *testing.T, restrictions DeveloperRestrictions, script string) (string, bool) {
	t.Helper()
	wrapped, err := restrictions.Wrap(execution.Command{Name: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	command := exec.Command(wrapped.Name, wrapped.Args...)
	command.Dir = wrapped.Dir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if DeveloperSandboxRefused(stderr.String()) {
		t.Skipf("this process is inside a sandbox already, so sandbox-exec cannot start one: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String() + stderr.String(), err == nil
}

// A command run under the developer's restrictions cannot reach the network
// where the developer session cannot: a connection to this machine's own
// address on another interface, which the same command reaches unrestricted,
// is refused by the sandbox, while localhost, which the developer's
// allowLocalBinding permits, is reached. It may write in its worktree and not
// beside it, and cannot read a folder it is refused.
func TestACommandUnderTheDevelopersRestrictionsCannotReachTheNetwork(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the developer session's restrictions are reproduced only on macOS")
	}

	worktree, outside, refused := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(refused, "secret"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restrictions := DeveloperRestrictions{Worktree: worktree, Unreadable: []string{refused}}

	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	if output, ok := runRestricted(t, restrictions, "/usr/bin/nc -z -G 5 127.0.0.1 "+port); !ok {
		t.Fatalf("localhost was not reached under the developer's restrictions: %s", output)
	}
	if address := machineAddress(t); address != "" {
		if output, err := exec.Command("/usr/bin/nc", "-z", "-G", "5", address, port).CombinedOutput(); err != nil {
			t.Skipf("this machine's own address %s is not reachable unrestricted either, so refusing it proves nothing: %s", address, output)
		}
		output, ok := runRestricted(t, restrictions, "/usr/bin/nc -z -G 5 "+address+" "+port)
		if ok || !strings.Contains(output, "Operation not permitted") {
			t.Fatalf("connecting to %s under the developer's restrictions = %v, %q; want it refused by the sandbox", address, ok, output)
		}
	}
	// An address that is no host at all is refused at once rather than timing
	// out, which is the sandbox and not the network answering.
	if output, ok := runRestricted(t, restrictions, "/usr/bin/nc -z -G 5 192.0.2.1 80"); ok || !strings.Contains(output, "Operation not permitted") {
		t.Fatalf("connecting off the machine = %v, %q; want it refused by the sandbox", ok, output)
	}

	if output, ok := runRestricted(t, restrictions, "echo x > inside"); !ok {
		t.Fatalf("writing in the worktree was refused: %s", output)
	}
	if _, err := os.Stat(filepath.Join(worktree, "inside")); err != nil {
		t.Fatalf("the write in the worktree did not land: %v", err)
	}
	if output, ok := runRestricted(t, restrictions, "echo x > "+strconv.Quote(filepath.Join(outside, "beside"))); ok {
		t.Fatalf("writing beside the worktree was allowed: %s", output)
	}
	if output, ok := runRestricted(t, restrictions, "cat "+strconv.Quote(filepath.Join(refused, "secret"))); ok {
		t.Fatalf("reading the refused folder was allowed: %s", output)
	}
}

// machineAddress is one IPv4 address of this machine other than loopback, or
// empty where it has none.
func machineAddress(t *testing.T) string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
			return network.IP.String()
		}
	}
	return ""
}

// The profile is refused for no worktree or a folder that does not exist, and
// names every folder it grants.
func TestTheDevelopersRestrictionsNameEveryFolder(t *testing.T) {
	t.Parallel()

	if _, err := (DeveloperRestrictions{}).Profile(); err == nil {
		t.Fatal("Profile() with no worktree = nil, want it refused")
	}
	if _, err := (DeveloperRestrictions{Worktree: filepath.Join(t.TempDir(), "missing")}).Profile(); err == nil {
		t.Fatal("Profile() for a missing worktree = nil, want it refused")
	}
	worktree, scratch, services := t.TempDir(), t.TempDir(), t.TempDir()
	profile, err := DeveloperRestrictions{Worktree: worktree, Scratch: scratch, Unreadable: []string{services}}.Profile()
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	for _, folder := range []string{worktree, scratch, services} {
		resolved, _ := filepath.EvalSymlinks(folder)
		if !strings.Contains(profile, strconv.Quote(resolved)) {
			t.Fatalf("profile never names %s:\n%s", resolved, profile)
		}
	}
	for _, want := range []string{"(deny network*)", `(allow network-outbound (remote ip "localhost:*"))`, "(deny file-write*)", "(deny file-read* (subpath"} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile never says %s:\n%s", want, profile)
		}
	}
}
