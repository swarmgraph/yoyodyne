package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// The hold is placed by the operator's process and read by every process that
// would start work, so it has to survive the gap between them, and it must be
// readable without any lease: the processes reading it are the ones being
// stopped from choosing.
func TestHoldingIntakeSurvivesTheProcessThatPlacedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	if _, held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v, want nothing holding a fresh state root", held, err)
	}

	heldAt := time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC)
	recorded, err := store.Hold(IntakeHolderOperator, "the decomposition is heading somewhere odd", heldAt)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if !recorded.HeldAt.Equal(heldAt) || recorded.Reason != "the decomposition is heading somewhere odd" {
		t.Fatalf("Hold() = %#v, want the moment it was placed and why", recorded)
	}
	if recorded.HeldBy != IntakeHolderOperator {
		t.Fatalf("Hold() held by %q, want who placed it recorded rather than left to be guessed", recorded.HeldBy)
	}

	// A second hold says the same thing as the first. Restamping it would make a
	// hold that has been in force since yesterday describe itself as new, and
	// rewriting its reason would rewrite the account of why nothing has started.
	// The holder is kept for the same reason and one more: the brake tripping over
	// a hold the operator placed must not make the harness the one holding it.
	again, err := store.Hold(IntakeHolderBrake, "something else entirely", heldAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Hold() error = %v", err)
	}
	if !again.HeldAt.Equal(heldAt) || again.Reason != "the decomposition is heading somewhere odd" {
		t.Fatalf("second Hold() = %#v, want the hold left exactly as it was placed", again)
	}
	if again.HeldBy != IntakeHolderOperator {
		t.Fatalf("second Hold() held by %q, want the operator still named as the holder", again.HeldBy)
	}

	// A separate store over the same root is what every other process is.
	loaded, held, err := newIntakeStoreAt(t, root, "yoyodyne").Held()
	if err != nil || !held {
		t.Fatalf("Held() = %t, %v, want the recorded hold", held, err)
	}
	if !loaded.HeldAt.Equal(heldAt) || loaded.HeldBy != IntakeHolderOperator {
		t.Fatalf("Held() = %#v, want the hold as it was placed, holder and all", loaded)
	}

	lifted, wasHeld, err := store.Release()
	if err != nil || !wasHeld {
		t.Fatalf("Release() = %t, %v, want the hold lifted", wasHeld, err)
	}
	if !lifted.HeldAt.Equal(heldAt) {
		t.Fatalf("Release() = %#v, want it to report what was lifted", lifted)
	}
	if _, held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() after release = %t, %v, want the harness choosing work again", held, err)
	}
	// Releasing what is not held is what an operator does when they are not sure,
	// and it means the harness should be picking work up, which it is.
	if _, wasHeld, err := store.Release(); err != nil || wasHeld {
		t.Fatalf("second Release() = %t, %v, want a no-op over intake that is running", wasHeld, err)
	}
}

// The hold is about one backlog, so holding what one product's harness may pull
// must leave another's alone. This is the whole reason it is not the switch at
// the state root.
// A release records who lifted the hold and when, beside the absence, and it is
// what the channel reads to say the ending by name: the operator's release at a
// terminal, and the harness lifting its own brake's hold on what moved it. A
// release nothing named reads as one naming nobody rather than as somebody, and
// releasing what is not held records nothing.
func TestAReleaseRecordsWhoLiftedTheHold(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	if _, recorded, err := store.LastRelease(); err != nil || recorded {
		t.Fatalf("LastRelease() = %t, %v, want no release on a fresh state root", recorded, err)
	}

	heldAt := time.Date(2026, 9, 19, 17, 56, 0, 0, time.UTC)
	if _, err := store.Hold(IntakeHolderOperator, "reordering the backlog first", heldAt); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	releasedAt := heldAt.Add(2 * time.Hour)
	lifted, wasHeld, err := store.ReleaseBy("the operator, at a terminal (`yoyo release`)", releasedAt)
	if err != nil || !wasHeld {
		t.Fatalf("ReleaseBy() = %t, %v, want the hold lifted", wasHeld, err)
	}
	release, recorded, err := newIntakeStoreAt(t, root, "yoyodyne").LastRelease()
	if err != nil || !recorded {
		t.Fatalf("LastRelease() = %t, %v, want the release recorded", recorded, err)
	}
	if !release.ReleasedAt.Equal(releasedAt) || release.ReleasedBy != "the operator, at a terminal (`yoyo release`)" || !release.Hold.HeldAt.Equal(lifted.HeldAt) {
		t.Fatalf("LastRelease() = %#v, want who lifted which hold and when", release)
	}
	if release.Says() != "released by the operator, at a terminal (`yoyo release`)" {
		t.Fatalf("Says() = %q", release.Says())
	}
	if (IntakeRelease{}).Says() != "released by somebody the record does not name" {
		t.Fatalf("an unnamed release says %q", (IntakeRelease{}).Says())
	}

	// Releasing what is not held records nothing: there was nothing lifted.
	if _, wasHeld, err := store.ReleaseBy("somebody else", releasedAt.Add(time.Hour)); err != nil || wasHeld {
		t.Fatalf("second ReleaseBy() = %t, %v, want a no-op", wasHeld, err)
	}
	if again, _, err := store.LastRelease(); err != nil || again.ReleasedBy != release.ReleasedBy {
		t.Fatalf("LastRelease() after a no-op = %#v, want the record left as it was", again)
	}

	// The harness lifting its own brake's hold records what moved it.
	trip := IntakeBrake{
		Blocked:        []BrakeBlockedRun{{RunID: "run-1", WorkItemID: "yoyodyne-ifd.398", Reason: "checks failed"}},
		CooldownEndsAt: releasedAt.Add(3 * time.Hour),
	}
	if _, err := store.Brake(trip, "3 run(s) blocked in a row", releasedAt.Add(2*time.Hour)); err != nil {
		t.Fatalf("Brake() error = %v", err)
	}
	if _, lifted, err := store.ReleaseBrake("the harness, because the probe run of yoyodyne-ifd.405 landed", releasedAt.Add(3*time.Hour)); err != nil || !lifted {
		t.Fatalf("ReleaseBrake() = %t, %v, want the brake's hold lifted", lifted, err)
	}
	release, _, err = store.LastRelease()
	if err != nil || release.ReleasedBy != "the harness, because the probe run of yoyodyne-ifd.405 landed" || release.Hold.HeldBy != IntakeHolderBrake {
		t.Fatalf("LastRelease() = %#v, %v, want the brake's release naming what moved the harness", release, err)
	}
}

func TestHoldingIntakeOnOneProductLeavesAnotherAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held := newIntakeStoreAt(t, root, "yoyodyne")
	other := newIntakeStoreAt(t, root, "something-else")
	if _, err := held.Hold(IntakeHolderOperator, "", time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, isHeld, err := other.Held(); err != nil || isHeld {
		t.Fatalf("Held() on the other product = %t, %v, want it unaffected", isHeld, err)
	}
}

// A hold nobody can read must never be reported as a harness that may choose
// work: the quiet an operator is looking at would then be unexplained, which is
// exactly what the hold exists to explain.
func TestAnUnreadableIntakeHoldIsNotReportedAsAbsent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	directory := home.ProductDirectory(root, "yoyodyne")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for name, content := range map[string]string{
		"not JSON at all":          "held\n",
		"a hold with no moment":    `{"schema_version":1,"product_id":"yoyodyne"}`,
		"an unsupported schema":    `{"schema_version":2,"product_id":"yoyodyne","held_at":"2026-08-18T18:15:00Z"}`,
		"a key nothing writes":     `{"schema_version":1,"product_id":"yoyodyne","held_at":"2026-08-18T18:15:00Z","until":"never"}`,
		"another product's record": `{"schema_version":1,"product_id":"elsewhere","held_at":"2026-08-18T18:15:00Z"}`,
	} {
		if err := os.WriteFile(filepath.Join(directory, "intake-hold.json"), []byte(content), 0o600); err != nil {
			t.Fatalf("%s: WriteFile() error = %v", name, err)
		}
		if _, held, err := store.Held(); err == nil || held {
			t.Fatalf("%s: Held() = %t, %v, want a refusal to read it as absent", name, held, err)
		}
	}
}

// Who holds intake is recorded state, so a hold this harness cannot attribute is
// refused at the point it would be written rather than reported as somebody's
// later.
func TestHoldingIntakeRequiresAHolderThisHarnessRecords(t *testing.T) {
	t.Parallel()

	store := newIntakeStoreAt(t, t.TempDir(), "yoyodyne")
	for _, holder := range []IntakeHolder{"", "someone", "the operator"} {
		if _, err := store.Hold(holder, "the queue looks wrong", time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)); err == nil {
			t.Fatalf("Hold(%q) was accepted, want a holder nothing can name refused", holder)
		}
	}
	if _, held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v, want a refused hold to have stopped nothing", held, err)
	}
}

// The one sentence every surface prints about a hold. It names the actual holder
// and the cause, and it never says the operator did something the brake did:
// what that misreporting cost was somebody diagnosing a decision nobody made.
func TestAnIntakeHoldSaysWhoPlacedItAndWhy(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		hold IntakeHold
		want string
	}{
		"the brake, with the storm that tripped it": {
			hold: IntakeHold{HeldBy: IntakeHolderBrake, Reason: "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3"},
			want: "the harness's own brake placed it after 3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
		},
		"the brake, with nothing recorded": {
			hold: IntakeHold{HeldBy: IntakeHolderBrake},
			want: "the harness's own brake placed it after runs kept blocking",
		},
		"the operator, with what looked wrong": {
			hold: IntakeHold{HeldBy: IntakeHolderOperator, Reason: "the queue needs reordering first"},
			want: "the operator placed it — the queue needs reordering first",
		},
		"the operator, in a hurry": {
			hold: IntakeHold{HeldBy: IntakeHolderOperator},
			want: "the operator placed it and gave no reason",
		},
		"a hold from before the holder was recorded": {
			hold: IntakeHold{Reason: "the queue needs reordering first"},
			want: "the record does not say who placed it — the queue needs reordering first",
		},
		"a hold from before, saying nothing at all": {
			hold: IntakeHold{},
			want: "the record does not say who placed it or why",
		},
	} {
		if said := testCase.hold.Says(); said != testCase.want {
			t.Fatalf("%s: Says() = %q, want %q", name, said, testCase.want)
		}
	}
}

// A hold written before the holder was recorded is still a hold, and reading it
// as absent would start work under a stopped line. What it must not do is
// acquire an attribution nobody wrote: the operator is the likely answer and a
// guess either way.
func TestAnIntakeHoldWithNoRecordedHolderIsReadWithoutOneBeingInvented(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	directory := home.ProductDirectory(root, "yoyodyne")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacy := `{"schema_version":1,"product_id":"yoyodyne","held_at":"2026-08-18T18:15:00Z","reason":"the queue needs reordering first"}`
	if err := os.WriteFile(filepath.Join(directory, "intake-hold.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	held, isHeld, err := store.Held()
	if err != nil || !isHeld {
		t.Fatalf("Held() = %t, %v, want a hold with no recorded holder still read as a hold", isHeld, err)
	}
	if held.HeldBy != "" {
		t.Fatalf("Held() held by %q, want no holder invented for a record that names none", held.HeldBy)
	}
	if said := held.Says(); !strings.Contains(said, "does not say who placed it") || strings.Contains(said, "the operator") {
		t.Fatalf("Says() = %q, want the absence stated rather than the operator assumed", said)
	}
}

func TestTheIntakeHoldIsRefusedARelativeStateRoot(t *testing.T) {
	t.Parallel()

	if _, err := NewIntakeHoldStore("state", "yoyodyne"); err == nil {
		t.Fatal("NewIntakeHoldStore() accepted a relative state root")
	}
}

func newIntakeStoreAt(t *testing.T, root string, productID domain.ProductID) *IntakeHoldStore {
	t.Helper()
	store, err := NewIntakeHoldStore(root, productID)
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	return store
}
