package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

var followUpWritten = time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)

func newFollowUp() followup.FollowUp {
	return followup.FollowUp{
		SchemaVersion: followup.SchemaVersion,
		Waits:         ownership.MoverDevelopmentManager,
		Owner:         ownership.MoverArchitect,
		About:         []followup.Subject{{Kind: followup.SubjectWorkItem, ID: "yoyodyne-ifd.432.25.19"}},
		Blocks:        followup.Blocks{Items: []string{"yoyodyne-ifd.432.25.19"}},
		Check:         followup.Check{Name: followup.CheckItemClosed, Arguments: map[string]string{followup.ArgItem: "yoyodyne-ifd.432.25.21"}},
		BackToOwnerAt: followUpWritten.Add(4 * time.Hour),
		ToManagerAt:   followUpWritten.Add(24 * time.Hour),
		Origin:        followup.Origin{Kind: followup.OriginRole, Role: "development-manager"},
		CreatedAt:     followUpWritten,
		State:         followup.StateOpen,
	}
}

func newFollowUpStore(t *testing.T) (*FollowUpStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewFollowUpStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return store, root
}

// A follow-up is kept under the product's state, survives the store that wrote
// it, and reads back as it was written.
func TestAFollowUpIsKeptAndReadBack(t *testing.T) {
	t.Parallel()
	store, root := newFollowUpStore(t)
	if kept, err := store.List(); err != nil || len(kept) != 0 {
		t.Fatalf("List() on a fresh root = %v, %v; want nothing", kept, err)
	}
	added, err := store.Add(newFollowUp())
	if err != nil {
		t.Fatalf("Add() = %v", err)
	}
	if !followup.ValidID(added.ID) || added.State != followup.StateOpen {
		t.Fatalf("Add() = %+v, want an open follow-up under an issued identifier", added)
	}
	if _, err := os.Stat(filepath.Join(home.ProductDirectory(root, "yoyodyne"), "follow-ups", added.ID+".json")); err != nil {
		t.Fatalf("the follow-up is not in the product's state: %v", err)
	}
	reopened, err := NewFollowUpStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load(added.ID)
	if err != nil || loaded.ID != added.ID || loaded.Check.Arguments[followup.ArgItem] != "yoyodyne-ifd.432.25.21" || !loaded.ToManagerAt.Equal(added.ToManagerAt) {
		t.Fatalf("Load() = %+v, %v; want what was added", loaded, err)
	}
	second := newFollowUp()
	second.CreatedAt = followUpWritten.Add(time.Minute)
	second.BackToOwnerAt = second.CreatedAt.Add(time.Hour)
	if _, err := store.Add(second); err != nil {
		t.Fatal(err)
	}
	kept, err := reopened.List()
	if err != nil || len(kept) != 2 || kept[0].ID != added.ID {
		t.Fatalf("List() = %+v, %v; want both, oldest first", kept, err)
	}
	if _, err := store.Load("follow-up-nothingkepthere00"); !errors.Is(err, ErrFollowUpUnknown) {
		t.Errorf("Load(unknown) = %v, want ErrFollowUpUnknown", err)
	}
	if _, err := store.Load("../../etc/passwd"); !errors.Is(err, ErrFollowUpUnknown) {
		t.Errorf("Load(a path) = %v, want ErrFollowUpUnknown", err)
	}
}

// A record the rules refuse is never written: one naming a command rather than
// a check, one the operator owns for no reason on his list, one already ended,
// and one carrying an identifier of its own.
func TestTheStoreRefusesWhatTheRulesRefuse(t *testing.T) {
	t.Parallel()
	store, _ := newFollowUpStore(t)
	cases := map[string]func(*followup.FollowUp){
		"a command":                func(f *followup.FollowUp) { f.Check = followup.Check{Name: "xcodebuild -license check"} },
		"the operator for nothing": func(f *followup.FollowUp) { f.Owner = ownership.MoverOperator },
		"already closed":           func(f *followup.FollowUp) { f.State = followup.StateClosed },
		"its own identifier":       func(f *followup.FollowUp) { f.ID = followup.NewID() },
		"one deadline":             func(f *followup.FollowUp) { f.ToManagerAt = time.Time{} },
		"recorded by the assistant": func(f *followup.FollowUp) {
			f.Origin, f.Owner, f.OwnerReason = followup.Origin{Kind: followup.OriginAssistant}, ownership.MoverOperator, ownership.OperatorOwnHold
		},
	}
	for name, change := range cases {
		f := newFollowUp()
		change(&f)
		if _, err := store.Add(f); err == nil {
			t.Errorf("%s: Add() = nil, want a refusal", name)
		}
	}
	if kept, err := store.List(); err != nil || len(kept) != 0 {
		t.Fatalf("List() = %+v, %v; want nothing written", kept, err)
	}
}

// A follow-up leaves the open state by its check passing or by a close with a
// reason, and once ended is not ended again. A close without a reason is
// refused and leaves the record open.
func TestAStoredFollowUpEndsOnlyByItsCheckOrAReason(t *testing.T) {
	t.Parallel()
	store, _ := newFollowUpStore(t)
	first, err := store.Add(newFollowUp())
	if err != nil {
		t.Fatal(err)
	}
	for _, by := range followup.Closers() {
		if _, err := store.Close(first.ID, by, "  ", followUpWritten.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "reason is required") {
			t.Errorf("closing by %s without a reason = %v, want a refusal", by, err)
		}
	}
	if standing, err := store.Load(first.ID); err != nil || standing.State != followup.StateOpen {
		t.Fatalf("after refused closes, Load() = %+v, %v; want it still open", standing, err)
	}
	passed, err := store.Pass(first.ID, followUpWritten.Add(2*time.Hour), "yoyodyne-ifd.432.25.21 closed")
	if err != nil || passed.State != followup.StatePassed || passed.Passed.Result != "yoyodyne-ifd.432.25.21 closed" {
		t.Fatalf("Pass() = %+v, %v", passed, err)
	}
	if _, err := store.Close(first.ID, followup.ClosedByOperator, "override", followUpWritten.Add(3*time.Hour)); !errors.Is(err, followup.ErrNotOpen) {
		t.Errorf("closing a passed follow-up = %v, want ErrNotOpen", err)
	}

	second, err := store.Add(newFollowUp())
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := store.Close(second.ID, followup.ClosedByWaiter, "no longer needed: the item it waited on was retired", followUpWritten.Add(time.Hour))
	if err != nil || withdrawn.State != followup.StateClosed || withdrawn.Closed.By != followup.ClosedByWaiter {
		t.Fatalf("Close() = %+v, %v; want it withdrawn by whoever waits", withdrawn, err)
	}
	if loaded, err := store.Load(second.ID); err != nil || loaded.Closed == nil || loaded.Closed.Reason != withdrawn.Closed.Reason {
		t.Fatalf("Load() after the withdrawal = %+v, %v", loaded, err)
	}
	if _, err := store.Pass(second.ID, followUpWritten.Add(2*time.Hour), ""); !errors.Is(err, followup.ErrNotOpen) {
		t.Errorf("passing a withdrawn follow-up = %v, want ErrNotOpen", err)
	}
	if _, err := store.Pass("follow-up-nothingkepthere00", followUpWritten, ""); !errors.Is(err, ErrFollowUpUnknown) {
		t.Errorf("Pass(unknown) = %v, want ErrFollowUpUnknown", err)
	}
}

// Two acts ending one follow-up at once: exactly one ends it, and the other is
// told it is no longer open.
func TestOneFollowUpIsEndedOnceUnderConcurrentCloses(t *testing.T) {
	t.Parallel()
	store, _ := newFollowUpStore(t)
	added, err := store.Add(newFollowUp())
	if err != nil {
		t.Fatal(err)
	}
	const closers = 8
	var wait sync.WaitGroup
	errs := make([]error, closers)
	for i := range closers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if i%2 == 0 {
				_, errs[i] = store.Pass(added.ID, followUpWritten.Add(time.Hour), "")
			} else {
				_, errs[i] = store.Close(added.ID, followup.ClosedByOwner, "done another way", followUpWritten.Add(time.Hour))
			}
		}()
	}
	wait.Wait()
	ended := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ended++
		case !errors.Is(err, followup.ErrNotOpen):
			t.Errorf("a concurrent end failed for another reason: %v", err)
		}
	}
	if ended != 1 {
		t.Fatalf("%d acts ended the follow-up, want exactly one", ended)
	}
}

// A record this build cannot read whole is refused rather than rewritten from
// part of it, and a record a listing cannot read is named without hiding the
// rest.
func TestAFollowUpThisBuildCannotReadIsNeverRewritten(t *testing.T) {
	t.Parallel()
	store, root := newFollowUpStore(t)
	added, err := store.Add(newFollowUp())
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Add(newFollowUp())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "follow-ups", added.ID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Replace(string(encoded), `"schema_version": 1,`, `"schema_version": 1, "escalated_to": "architect",`, 1)
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(added.ID, followup.ClosedByOwner, "done", followUpWritten.Add(time.Hour)); err == nil {
		t.Fatal("a record carrying a field this build does not know was rewritten")
	}
	if after, _ := os.ReadFile(path); string(after) != newer {
		t.Fatal("the refused record was changed")
	}
	if loaded, err := store.Load(added.ID); err != nil || loaded.State != followup.StateOpen {
		t.Fatalf("Load() of a newer record = %+v, %v; want it read past the field it does not know", loaded, err)
	}

	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := store.List()
	if err == nil || len(kept) != 1 || kept[0].ID != other.ID {
		t.Fatalf("List() = %+v, %v; want the readable one and an error naming the other", kept, err)
	}
}

// The follow-ups' directory replaced by a link out of the state root is never
// written through.
func TestTheFollowUpStoreNeverWritesOutsideTheStateRoot(t *testing.T) {
	t.Parallel()
	store, root := newFollowUpStore(t)
	outside := t.TempDir()
	product := home.ProductDirectory(root, "yoyodyne")
	if err := os.MkdirAll(product, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(product, "follow-ups")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(newFollowUp()); err == nil {
		t.Fatal("Add() wrote through a link out of the state root")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the store wrote %d entries outside the state root", len(entries))
	}
}
