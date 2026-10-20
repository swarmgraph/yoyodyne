package slack

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A sink never holds its log: it writes to a pipe its relay reads, and the
// relay appends in checked batches. A link to the log added from outside the
// root between two of those batches stops the copying at once: the outside
// link sees what was written before it appeared and nothing after, the sink is
// not stopped by it, and a note beside the log says why the log stopped.
func TestALinkAddedToASinkLogBetweenCopiedBatchesStopsTheCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is exercised on the Unix hosts Yoyodyne supports")
	}
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, outside := filepath.Join(base, "state"), filepath.Join(base, "outside")
	for _, directory := range []string{root, outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	relative := "projects/yoyodyne/state/slack/" + sinkLogFile
	log := filepath.Join(root, filepath.FromSlash(relative))
	proceed := filepath.Join(base, "proceed")
	// The sink says one thing, waits to be told to go on, and says another. It
	// gives up waiting after thirty seconds, so it ends whatever the test does.
	script := `echo first; end=$(( $(date +%s) + 30 )); while [ ! -f "$1" ] && [ "$(date +%s)" -lt "$end" ]; do sleep 0.05; done; echo second`
	pid, err := DetachedLauncher{}.Launch(Launch{
		Program: "/bin/sh",
		Args:    []string{"-c", script, "sink", proceed},
		Dir:     base,
		Env:     Environment(nil, "yoyodyne", botToken, appToken),
		LogRoot: root,
		Log:     relative,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if content, _ := os.ReadFile(log); string(content) == "first\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sink's first line never reached its log")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stolen := filepath.Join(outside, "stolen")
	if err := os.Link(log, stolen); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proceed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The sink is the relay's child, so its end is watched for rather than
	// waited on, and then the note the relay writes when it stops.
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	var note []byte
	for time.Now().Before(deadline) {
		if note, err = os.ReadFile(log + relayStoppedSuffix); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if content, err := os.ReadFile(stolen); err != nil || string(content) != "first\n" {
		t.Fatalf("the outside link reads %q (%v), want only what was written before it appeared", content, err)
	}
	if err != nil || !strings.Contains(string(note), "more than one link") {
		t.Fatalf("the note beside the stopped log reads %q (%v), want it to say the log gained a link", note, err)
	}
}
