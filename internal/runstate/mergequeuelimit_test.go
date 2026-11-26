package runstate

// The merge queue's record keeps every entry ever admitted, so it has a bound
// of its own, larger than the one every other state record shares. These tests
// hold both sides of it: a record past the shared bound is still admitted to
// and read, and a record past its own bound is refused without touching what
// was already written.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestAMergeQueueRecordPastTheSharedStateBoundIsAdmittedToAndRead(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	queue := fillMergeQueue(t, store, maxEncodedStateBytes, true)

	asked := largeMergeQueueAdmission(t)
	entry, admitted, err := store.Admit(context.Background(), asked)
	if err != nil {
		t.Fatalf("Admit() onto a record of more than %d bytes error = %v", maxEncodedStateBytes, err)
	}
	if !admitted || entry.Order != queue.NextOrder || entry.PredecessorOrder != queue.NextOrder-1 {
		t.Fatalf("Admit() = order %d after %d (admitted %v), want order %d after %d", entry.Order, entry.PredecessorOrder, admitted, queue.NextOrder, queue.NextOrder-1)
	}
	if size := mergeQueueRecordSize(t, store); size <= maxEncodedStateBytes {
		t.Fatalf("the record is %d bytes after the admission, want more than %d", size, maxEncodedStateBytes)
	}

	recorded, err := store.Entries(mainQueue)
	if err != nil {
		t.Fatalf("Entries() of a record of more than %d bytes error = %v", maxEncodedStateBytes, err)
	}
	if len(recorded) != len(queue.Entries)+1 {
		t.Fatalf("Entries() = %d entries, want %d", len(recorded), len(queue.Entries)+1)
	}
	for i, got := range recorded[:len(queue.Entries)] {
		if got.EntryID != queue.Entries[i].EntryID || got.Order != queue.Entries[i].Order {
			t.Fatalf("entry %d read back as %s at order %d, want %s at order %d", i, got.EntryID, got.Order, queue.Entries[i].EntryID, queue.Entries[i].Order)
		}
	}
	if last := recorded[len(recorded)-1]; last.EntryID != entry.EntryID || last.RunID != asked.RunID {
		t.Fatalf("the last entry read back is %s for run %s, want %s for run %s", last.EntryID, last.RunID, entry.EntryID, asked.RunID)
	}
	keys, err := store.Keys()
	if err != nil || len(keys) != 1 || keys[0] != mainQueue {
		t.Fatalf("Keys() = %v, %v; want only %v", keys, err, mainQueue)
	}
}

func TestAnAdmissionThatWouldTakeTheMergeQueuePastItsOwnBoundIsRefused(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	fillMergeQueue(t, store, maxEncodedMergeQueueBytes, false)
	path := filepath.Join(store.Root(), mainQueue.directory(), mergeQueueRecordName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(before) > maxEncodedMergeQueueBytes {
		t.Fatalf("the filled record is %d bytes, past the %d byte bound before anything is admitted", len(before), maxEncodedMergeQueueBytes)
	}

	_, admitted, err := store.Admit(context.Background(), largeMergeQueueAdmission(t))
	if err == nil || admitted {
		t.Fatalf("Admit() past the bound = admitted %v, error %v; want a refusal", admitted, err)
	}
	var stop StopError
	if !errors.As(err, &stop) || stop.Class != StopStateBound {
		t.Fatalf("Admit() error = %#v, want a StopError of class %v", err, StopStateBound)
	}
	// The refusal is the harness's own limit, which is what lets the stop it
	// ends a run on be held rather than charged, and the room a retry waits on
	// is not there.
	if !errors.Is(err, ErrMergeQueueFull) {
		t.Fatalf("Admit() error = %v, want it to be ErrMergeQueueFull", err)
	}
	if room, says, roomErr := store.RoomForAdmission(mainQueue); roomErr != nil || room || !strings.Contains(says, strconv.Itoa(maxEncodedMergeQueueBytes)) {
		t.Fatalf("RoomForAdmission() = %t, %q, %v; want no room, said against the limit", room, says, roomErr)
	}
	match := regexp.MustCompile(`would be (\d+) bytes, limit is (\d+)`).FindStringSubmatch(err.Error())
	if match == nil {
		t.Fatalf("Admit() error = %q, want it to name the record's size and its limit", err)
	}
	if size, _ := strconv.Atoi(match[1]); size <= maxEncodedMergeQueueBytes {
		t.Fatalf("Admit() error names a size of %d bytes, want one past %d", size, maxEncodedMergeQueueBytes)
	}
	if match[2] != strconv.Itoa(maxEncodedMergeQueueBytes) {
		t.Fatalf("Admit() error names a limit of %s, want %d", match[2], maxEncodedMergeQueueBytes)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() after the refusal error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused admission changed the record already written")
	}
}

func TestAMergeQueueRecordPastItsOwnBoundIsNotRead(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	if _, _, err := store.Admit(context.Background(), testAdmission(t, mainQueue)); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	path := filepath.Join(store.Root(), mainQueue.directory(), mergeQueueRecordName)
	if err := os.WriteFile(path, bytes.Repeat([]byte(" "), maxEncodedMergeQueueBytes+1), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := store.Entries(mainQueue)
	want := fmt.Sprintf("%d bytes, which exceeds the %d byte bound", maxEncodedMergeQueueBytes+1, maxEncodedMergeQueueBytes)
	if err == nil || !strings.Contains(err.Error(), want) || !errors.Is(err, ErrMergeQueueFull) {
		t.Fatalf("Entries() of an oversized record error = %v, want ErrMergeQueueFull saying %q", err, want)
	}
	if room, says, roomErr := store.RoomForAdmission(mainQueue); roomErr != nil || room || !strings.Contains(says, want) {
		t.Fatalf("RoomForAdmission() of an oversized record = %t, %q, %v; want no room, saying %q", room, says, roomErr, want)
	}
}

// A queue with no record, and one well inside its bound, have room for the
// next admission, so a change refused for want of room is taken up again.
func TestAMergeQueueInsideItsBoundHasRoomForAnAdmission(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	if room, says, err := store.RoomForAdmission(mainQueue); err != nil || !room || says == "" {
		t.Fatalf("RoomForAdmission() of a queue with no record = %t, %q, %v; want room", room, says, err)
	}
	fillMergeQueue(t, store, maxEncodedMergeQueueBytes-2*mergeQueueAdmissionRoom, false)
	if room, says, err := store.RoomForAdmission(mainQueue); err != nil || !room || !strings.Contains(says, "bytes of its") {
		t.Fatalf("RoomForAdmission() of a queue inside its bound = %t, %q, %v; want room, said by size", room, says, err)
	}
}

// largeMergeQueueAdmission is an admission whose title is as long as an
// admission's text may be, so a record reaches its bound in few entries.
func largeMergeQueueAdmission(t *testing.T) MergeQueueAdmission {
	t.Helper()
	asked := testAdmission(t, mainQueue)
	asked.WorkItemTitle = strings.Repeat("t", MaxMergeQueueTextBytes)
	return asked
}

// fillMergeQueue writes the queue's record directly, rather than admitting
// thousands of entries one at a time, and returns it. Every entry is a large
// admission with a run of its own. With past set the record holds the fewest
// entries that take it past bound bytes; without it, the most that keep it
// within bound, so one more entry of the same shape would take it past.
func fillMergeQueue(t *testing.T, store *MergeQueueStore, bound int, past bool) MergeQueue {
	t.Helper()
	entry, _, err := store.Admit(context.Background(), largeMergeQueueAdmission(t))
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	header := MergeQueue{SchemaVersion: MergeQueueSchemaVersion, ProductID: "yoyodyne", Repository: mainQueue.Repository, TargetBranch: mainQueue.TargetBranch}
	entries := make([]MergeQueueEntry, 0, bound/len(encodeMergeQueue(t, MergeQueue{Entries: []MergeQueueEntry{entry}}))+2)
	encoded := func(count int) []byte {
		for len(entries) < count {
			next := entry
			if next.RunID, err = NewRunID(); err != nil {
				t.Fatalf("NewRunID() error = %v", err)
			}
			if next.EntryID, err = newMergeQueueEntryID(); err != nil {
				t.Fatalf("newMergeQueueEntryID() error = %v", err)
			}
			next.Order, next.PredecessorOrder = uint64(len(entries)+1), uint64(len(entries))
			entries = append(entries, next)
		}
		queue := header
		queue.Entries, queue.NextOrder = entries[:count], uint64(count+1)
		return encodeMergeQueue(t, queue)
	}
	// Each step encodes the whole record, so the count is found by halving.
	low, high := 0, cap(entries)
	for len(encoded(high)) <= bound {
		low, high = high, high*2
	}
	for high-low > 1 {
		if middle := (low + high) / 2; len(encoded(middle)) <= bound {
			low = middle
		} else {
			high = middle
		}
	}
	count := low
	if past {
		count = high
	}
	record := encoded(count)
	path := filepath.Join(store.Root(), mainQueue.directory(), mergeQueueRecordName)
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	queue := header
	queue.Entries, queue.NextOrder = entries[:count], uint64(count+1)
	return queue
}

func encodeMergeQueue(t *testing.T, queue MergeQueue) []byte {
	t.Helper()
	encoded, err := encodeRecord("merge queue", queue)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	return encoded
}

func mergeQueueRecordSize(t *testing.T, store *MergeQueueStore) int {
	t.Helper()
	info, err := os.Stat(filepath.Join(store.Root(), mainQueue.directory(), mergeQueueRecordName))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	return int(info.Size())
}
