package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// answeringHolders is a finder that gives each answer in turn and the last one
// after that.
type answeringHolders struct {
	mu      sync.Mutex
	answers [][]StoreHolder
	asked   int
}

func (h *answeringHolders) Holders(context.Context, string) ([]StoreHolder, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	answer := h.answers[min(h.asked, len(h.answers)-1)]
	h.asked++
	return answer, nil
}

// A squash copies the live store whole, hands the copy over to be recorded
// before bd runs, and then runs the squash on the live store, pinned to it by
// BEADS_DIR. The copy is left in place, a project of its own holding the store
// as it was before the squash.
func TestASquashCopiesTheStoreAndRecordsTheCopyBeforeBdRuns(t *testing.T) {
	t.Parallel()
	for stage, verb := range map[string]string{StageOldSquashed: "compact", StageAllSquashed: "flatten"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			project, store := liveStore(t)
			before := snapshot(t, store)
			tracker := &fakeTracker{shrinkTo: defaultShrinks()}
			client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
			copies, copiesDir := workDirectory(t)
			var handed StoreSquash
			ranBefore := -1

			squash, err := client.SquashStore(context.Background(), stage, copies, "20260929T170000.000000000Z-A", time.Minute, func(copied StoreSquash) error {
				handed, ranBefore = copied, len(tracker.ran())
				return nil
			})
			if err != nil {
				t.Fatalf("SquashStore() error = %v", err)
			}
			copyDir := filepath.Join(copiesDir, "20260929T170000.000000000Z-A")
			if ranBefore != 0 || handed.Copy != copyDir || handed.CopiedAt.IsZero() || handed.Ran() {
				t.Errorf("handed over %+v after %d bd command(s), want the copy at %s before bd ran", handed, ranBefore, copyDir)
			}
			if copied := snapshot(t, filepath.Join(copyDir, ".beads")); !reflect.DeepEqual(contents(copied), contents(before)) {
				t.Errorf("the copy holds %v, want the store as it was %v", copied, before)
			}
			ran := tracker.ran()
			want, _ := SquashCommand(stage)
			if len(ran) != 1 || !reflect.DeepEqual(ran[0].Args, want) || environmentValue(ran[0].Env, "BEADS_DIR") != canonicalPath(store) || ran[0].Timeout != time.Minute {
				t.Fatalf("bd ran %+v, want %v once on the live store under the limit", ran, want)
			}
			if !squash.Ran() || squash.Copy != copyDir || squash.SizeBefore != 4096 || squash.SizeAfter != int64(defaultShrinks()[verb]) || squash.Failed != "" {
				t.Errorf("SquashStore() = %+v, want the squash measured either side", squash)
			}
		})
	}
}

// contents is a snapshot without modification times, which a copy does not
// keep.
func contents(files map[string]string) map[string]string {
	kept := map[string]string{}
	for name, file := range files {
		_, rest, _ := strings.Cut(file, " ")
		_, rest, _ = strings.Cut(rest, " ")
		kept[name] = rest
	}
	return kept
}

// Maintenance alone holds the store, or nothing is squashed: a store another
// process has open, one whose holders cannot be told, and one opened by
// another process while it was being copied are left alone, and any copy
// made is removed.
func TestASquashRunsOnlyWhereNothingElseHoldsTheStore(t *testing.T) {
	t.Parallel()
	holder := StoreHolder{PID: 4242, Command: "bd list --json"}
	for name, tc := range map[string]struct {
		holders HolderFinder
		busy    bool
		unknown bool
	}{
		"open before":         {holders: fixedHolders{holders: []StoreHolder{holder}}, busy: true},
		"cannot be told":      {holders: fixedHolders{err: errors.New("lsof is not installed")}, unknown: true},
		"open after the copy": {holders: &answeringHolders{answers: [][]StoreHolder{nil, {holder}}}, busy: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			project, store := liveStore(t)
			before := snapshot(t, store)
			tracker := &fakeTracker{shrinkTo: defaultShrinks()}
			client := Client{Runner: tracker, Dir: project, Holders: tc.holders}
			copies, copiesDir := workDirectory(t)
			recorded := false

			squash, err := client.SquashStore(context.Background(), StageOldSquashed, copies, "c", time.Minute, func(StoreSquash) error {
				recorded = true
				return nil
			})
			if err != nil || squash.Ran() || recorded || (len(squash.Busy) > 0) != tc.busy || (squash.Unknown != "") != tc.unknown {
				t.Fatalf("SquashStore() = %+v, %v, recorded %t, want nothing squashed or recorded", squash, err, recorded)
			}
			if ran := tracker.ran(); len(ran) != 0 {
				t.Errorf("bd ran %+v, want nothing", ran)
			}
			if entries, _ := os.ReadDir(copiesDir); len(entries) != 0 {
				t.Errorf("copies = %v, want none left", entries)
			}
			if after := snapshot(t, store); !reflect.DeepEqual(after, before) {
				t.Errorf("the live store changed")
			}
		})
	}
}

// A store written to while it was copied is not squashed, and the copy, which
// is no one state of it, is removed.
func TestAStoreThatChangedWhileItWasCopiedIsNotSquashed(t *testing.T) {
	t.Parallel()
	project, store := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	client.storeCopied = func() {
		writeFile(t, filepath.Join(store, "embeddeddolt", "calc", "data"), strings.Repeat("y", 5000))
	}
	copies, copiesDir := workDirectory(t)

	squash, err := client.SquashStore(context.Background(), StageOldSquashed, copies, "c", time.Minute, func(StoreSquash) error {
		t.Error("a copy of a store that changed was handed over to be recorded")
		return nil
	})
	if err != nil || squash.Ran() || !strings.Contains(squash.Moved, "embeddeddolt/calc/data was written to") {
		t.Fatalf("SquashStore() = %+v, %v, want nothing squashed and the change named", squash, err)
	}
	if ran := tracker.ran(); len(ran) != 0 {
		t.Errorf("bd ran %+v, want nothing", ran)
	}
	if entries, _ := os.ReadDir(copiesDir); len(entries) != 0 {
		t.Errorf("copies = %v, want the copy removed", entries)
	}
}

// A copy that cannot be recorded is removed, and the store is not squashed.
func TestACopyThatCannotBeRecordedIsRemovedAndNothingIsSquashed(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	copies, copiesDir := workDirectory(t)

	squash, err := client.SquashStore(context.Background(), StageOldSquashed, copies, "c", time.Minute, func(StoreSquash) error {
		return errors.New("the state directory is read-only")
	})
	if err == nil || !strings.Contains(err.Error(), "the state directory is read-only") || squash.Ran() || squash.Copy != "" {
		t.Fatalf("SquashStore() = %+v, %v, want it refused for the record", squash, err)
	}
	if ran := tracker.ran(); len(ran) != 0 {
		t.Errorf("bd ran %+v, want nothing", ran)
	}
	if entries, _ := os.ReadDir(copiesDir); len(entries) != 0 {
		t.Errorf("copies = %v, want the unrecorded copy removed", entries)
	}
}

// A squash bd fails is returned with what bd said, and its copy is kept.
func TestASquashThatFailsKeepsItsCopy(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks(), fail: "compact"}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	copies, copiesDir := workDirectory(t)

	squash, err := client.SquashStore(context.Background(), StageOldSquashed, copies, "c", time.Minute, func(StoreSquash) error { return nil })
	if err == nil || !squash.Ran() || !strings.Contains(squash.Failed, "storage backend does not support it") {
		t.Fatalf("SquashStore() = %+v, %v, want the failure with bd's words", squash, err)
	}
	if _, err := os.Stat(filepath.Join(copiesDir, "c", ".beads", "embeddeddolt", "calc", "data")); err != nil {
		t.Errorf("the copy is gone after a failed squash: %v", err)
	}
}

// Only the two squashes are squashes.
func TestOnlyTheTwoSquashesAreRun(t *testing.T) {
	t.Parallel()
	project, _ := liveStore(t)
	tracker := &fakeTracker{shrinkTo: defaultShrinks()}
	client := Client{Runner: tracker, Dir: project, Holders: fixedHolders{}}
	copies, _ := workDirectory(t)
	if _, err := client.SquashStore(context.Background(), StageCleanedUp, copies, "c", time.Minute, func(StoreSquash) error { return nil }); err == nil {
		t.Errorf("SquashStore(%q) succeeded, want it refused", StageCleanedUp)
	}
	if ran := tracker.ran(); len(ran) != 0 {
		t.Errorf("bd ran %+v, want nothing", ran)
	}
}
