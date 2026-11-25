//go:build darwin || linux

package epicprogress

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	cutOffRoot  = "EPICPROGRESS_TEST_CUT_OFF_ROOT"
	cutOffAfter = "EPICPROGRESS_TEST_CUT_OFF_AFTER"
)

// joinCutOff joins a piece to epic B in a process that may write no file longer than the
// limit, so the write of a longer one fails after its first bytes.
func joinCutOff(t *testing.T, root string, limit int) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCutOffWriter$")
	command.Env = append(os.Environ(), cutOffRoot+"="+root, cutOffAfter+"="+strconv.Itoa(limit))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "write the epics' progress") || !strings.Contains(string(output), "file too large") {
		t.Fatalf("the write was not cut off: %v\n%s", err, output)
	}
}

// TestCutOffWriter is the process joinCutOff starts.
func TestCutOffWriter(t *testing.T) {
	root := os.Getenv(cutOffRoot)
	if root == "" {
		t.Skip("run by the tests that cut a write off")
	}
	after, err := strconv.ParseUint(os.Getenv(cutOffAfter), 10, 64)
	must(t, err)
	must(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: after, Max: after}))
	must(t, storeAt(t, root).Join(epicB, joins(piece)))
}

// Where the limit cuts off the entries, nothing changes. The list is longer than
// the second limit and the entries are not, so there the entry is written, still
// empty, and the list is cut off before the join: a stop between the two writes.
func TestAWriteCutOffPartwayLeavesTheOldFileWhole(t *testing.T) {
	t.Parallel()
	root, a, b := twoNamed(t)
	must(t, storeAt(t, root).Begin(a))
	list := Begun{SchemaVersion: SchemaVersion, ProductID: product, Namings: []Naming{a}}
	for index := range 50 {
		list.Namings = append(list.Namings, Naming{WorkItemID: fmt.Sprintf("yoyodyne-long.%d", index), NamedAt: namedAt})
	}
	pinned, err := runstate.PinStateRoot(filepath.Dir(pathOf(root, begunName)))
	must(t, err)
	defer pinned.Close()
	must(t, write(pinned, begunName, list, func(b Begun) error { return b.validate(product) }))
	entries, listed := contents(t, root, progressName), contents(t, root, begunName)
	joinCutOff(t, root, 200)
	if contents(t, root, progressName) != entries || contents(t, root, begunName) != listed {
		t.Fatal("a file was changed")
	}
	assertNotBegun(t, root, b)
	joinCutOff(t, root, 1000)
	read := view(t, root)
	if found, kept := read.Entry(b); !kept || !found.empty() || read.Unreadable(b) != "" || contents(t, root, begunName) != listed {
		t.Fatalf("entry %+v, kept %t, want an empty entry that can be read and the list as it was", found, kept)
	}
	must(t, storeAt(t, root).Join(epicB, joins(piece)))
	if !view(t, root).begun.has(b) || !entry(t, root, b).Has(piece) {
		t.Fatal("it was not listed and gone on with")
	}
}
