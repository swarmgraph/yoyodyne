//go:build darwin || linux

package epicscope

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	cutOffRoot  = "EPICSCOPE_TEST_CUT_OFF_ROOT"
	cutOffAfter = 200
)

// The act's own write fails after its first bytes are on the disk, as on a full
// disk: a limit on how large a file the writing process may write cuts it.
func TestAWriteCutOffPartwayLeavesTheOldFileWhole(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startA(t, root)
	before := contents(t, root)
	if len(before) <= cutOffAfter {
		t.Fatalf("the record is %d bytes, which %d would not cut", len(before), cutOffAfter)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCutOffWriter$")
	command.Env = append(os.Environ(), cutOffRoot+"="+root)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "write the record of named epics") || !strings.Contains(string(output), "file too large") {
		t.Fatalf("the write was not cut off: %v\n%s", err, output)
	}
	if after := contents(t, root); !bytes.Equal(before, after) {
		t.Fatalf("the old file was changed to:\n%s", after)
	}
	assertNextActGoesThrough(t, root)
}

// TestCutOffWriter is the process the test above cuts off.
func TestCutOffWriter(t *testing.T) {
	root := os.Getenv(cutOffRoot)
	if root == "" {
		t.Skip("run by TestAWriteCutOffPartwayLeavesTheOldFileWhole")
	}
	limit := syscall.Rlimit{Cur: cutOffAfter, Max: cutOffAfter}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorAt(t, root).Add(epicA, "yoyodyne-cut.1", namedAt); err != nil {
		t.Fatal(err)
	}
}

// A process killed partway never reaches the rename. What it leaves is what the
// confined writer an act uses has put on the disk by then, and that is made here
// by writing half a record through it and never closing it.
func TestAWriterKilledPartwayLeavesTheOldFileWhole(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startA(t, root)
	before := contents(t, root)
	pinned, err := runstate.PinStateRoot(filepath.Dir(filePath(root)))
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	half, err := pinned.FileWriter(fileName, 0o600, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := half.Write(before[:len(before)/2]); err != nil {
		t.Fatal(err)
	}
	if after := contents(t, root); !bytes.Equal(before, after) {
		t.Fatalf("the old file was changed to:\n%s", after)
	}
	assertNextActGoesThrough(t, root)
}

func contents(t *testing.T, root string) []byte {
	t.Helper()
	content, err := os.ReadFile(filePath(root))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func assertNextActGoesThrough(t *testing.T, root string) {
	t.Helper()
	if epic, _ := read(t, root).Named(epicA); epic.Has("yoyodyne-cut.1") {
		t.Fatal("the act that was cut off is in the record")
	}
	if _, err := operatorAt(t, root).Add(epicA, "yoyodyne-cut.1", namedAt); err != nil {
		t.Fatal(err)
	}
}
