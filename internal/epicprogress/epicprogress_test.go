package epicprogress

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	product = "yoyodyne"
	epicA   = "yoyodyne-9k1y"
	epicB   = "yoyodyne-7abc"
	memberA = "yoyodyne-9k1y.2"
	memberB = "yoyodyne-7abc.2"
	piece   = "yoyodyne-piece"
)

var namedAt = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

func operatorAt(t *testing.T, root string) *epicscope.Operator {
	t.Helper()
	operator, err := epicscope.ForOperator(root, product)
	must(t, err)
	return operator
}

func name(t *testing.T, root, epic string, at time.Time, members ...string) Naming {
	t.Helper()
	started, err := operatorAt(t, root).Start(epicscope.Naming{Epic: epic, From: "terminal", Members: members}, at)
	must(t, err)
	return Of(started.Epic)
}

// twoNamed is a state root with two epics named and nothing begun.
func twoNamed(t *testing.T) (root string, a, b Naming) {
	t.Helper()
	root = t.TempDir()
	return root, name(t, root, epicA, namedAt, memberA), name(t, root, epicB, namedAt, memberB)
}

func storeAt(t *testing.T, root string) *Store {
	t.Helper()
	store, err := Open(root, product)
	must(t, err)
	return store
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantError(t *testing.T, err error, says string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), says) {
		t.Errorf("error = %v, want one saying %q", err, says)
	}
}

func view(t *testing.T, root string) View {
	t.Helper()
	record, err := epicscope.Read(root, product)
	must(t, err)
	read, err := Read(root, product, record)
	must(t, err)
	return read
}

func entry(t *testing.T, root string, naming Naming) Entry {
	t.Helper()
	found, kept := view(t, root).Entry(naming)
	if !kept {
		t.Fatalf("no entry is kept for %+v", naming)
	}
	return found
}

func pathOf(root, file string) string {
	return filepath.Join(home.ProductDirectory(root, product), file)
}

func contents(t *testing.T, root, file string) string {
	t.Helper()
	content, err := os.ReadFile(pathOf(root, file))
	must(t, err)
	return string(content)
}

func joins(item string) Joined {
	return Joined{WorkItemID: item, From: memberA, Judgment: "it asks only for what the description asks", Session: "session-1", At: namedAt}
}

func TestEveryFieldIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	store, at := storeAt(t, root), namedAt.Add(time.Hour)
	store.now = func() time.Time { return at }
	kept := Entry{Comments: []Comment{{ID: "forge-7", Became: "repaired", At: at}}, Checks: []Check{{ID: "check-1", Commit: "abc", Found: "all met", At: at}},
		Deliveries: []Delivery{{At: at, Evidence: "check-1"}}}
	must(t, store.Join(epicA, joins(piece)))
	must(t, store.change(a, func(entry *Entry) error {
		entry.Comments, entry.Checks, entry.Deliveries = kept.Comments, kept.Checks, kept.Deliveries
		return nil
	}))
	// A reservation another record holds is only pointed to, and once.
	must(t, errors.Join(store.Point(a, "run-1", "a run", "the record of run run-1"), store.Point(a, "run-1", "a run", "again")))
	wantError(t, store.settle(a, "run-1", Result{Says: "landed"}), "another record holds it")
	_, err := store.Call(a, "judge-1", "a joining judgment", nil, func() (string, error) { return "inside", nil })
	must(t, err)
	want := Entry{Naming: a, BegunAt: at, NamedBeforeKept: true,
		Joined: []Joined{joins(piece)}, Comments: kept.Comments, Checks: kept.Checks, Deliveries: kept.Deliveries,
		Reservations: []Reservation{
			{ID: "run-1", For: "a run", At: at, HeldBy: "the record of run run-1"},
			{ID: "judge-1", For: "a joining judgment", At: at, Result: &Result{At: at, Says: "inside"}},
		}}
	if got := entry(t, root, a); !reflect.DeepEqual(got, want) {
		t.Fatalf("the entry is %+v\nwant %+v", got, want)
	}
	// The list holds the naming and nothing else.
	if got, want := strings.Join(strings.Fields(contents(t, root, "epic-progress-begun.json")), ""), `{"schema_version":1,"product_id":"yoyodyne","namings":[{"work_item_id":"yoyodyne-9k1y","named_at":"2026-11-24T09:00:00Z"}]}`; got != want {
		t.Fatalf("the list is %s\nwant %s", got, want)
	}
}

// Rule 1 too. Rule 3, the stop between the two writes, is in cutoff_unix_test.go.
func TestANamingEndedAndMadeAgainStartsEmptyAndKeepsTheOldEntry(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	store := storeAt(t, root)
	must(t, store.Join(epicA, joins(piece)))
	old := entry(t, root, a)
	_, err := operatorAt(t, root).End(epicA, "", namedAt)
	must(t, err)
	again := name(t, root, epicA, keptSince.Add(time.Hour), memberA)
	must(t, store.Begin(again))
	got := entry(t, root, again)
	if !view(t, root).begun.has(again) || !got.empty() || got.NamedBeforeKept || !got.NamedAt.Equal(again.NamedAt) {
		t.Fatalf("the new entry is %+v, want a listed empty one named after progress was kept", got)
	}
	if kept := entry(t, root, a); !reflect.DeepEqual(kept, old) || !kept.Has(piece) {
		t.Fatalf("the old entry is %+v, want %+v", kept, old)
	}
}

// Rules 2 and 6.
func TestANamingWithNeitherFileIsNotBegunAndReadingItWritesNothing(t *testing.T) {
	t.Parallel()
	root, a, b := twoNamed(t)
	assertNotBegun(t, root, a)
	unnamed := t.TempDir()
	assertNotBegun(t, unnamed, a)
	wantError(t, storeAt(t, unnamed).Join(epicA, joins(piece)), "is not open")
	for _, path := range []string{pathOf(root, "epic-progress.json"), pathOf(root, begunName), pathOf(unnamed, "epic-scope.json"), pathOf(unnamed, progressName), pathOf(unnamed, begunName)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s is there: %v", path, err)
		}
	}
	must(t, storeAt(t, root).Join(epicA, joins(piece)))
	assertNotBegun(t, root, b)
	must(t, os.Remove(pathOf(root, progressName)))
	must(t, os.Remove(pathOf(root, begunName)))
	assertNotBegun(t, root, a)
}

func assertNotBegun(t *testing.T, root string, naming Naming) {
	t.Helper()
	if found, kept := view(t, root).Entry(naming); kept || view(t, root).Unreadable(naming) != "" {
		t.Fatalf("entry = %+v, %t, unreadable %q, want one not begun", found, kept, view(t, root).Unreadable(naming))
	}
}

// Rule 4.
func TestOneOfTheTwoMissingIsUnreadableForThatNamingAlone(t *testing.T) {
	t.Parallel()
	for missing, says := range map[string]string{
		progressName: "holds no entry for it",
		begunName:    "does not say it was begun",
	} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			root, a, b := twoNamed(t)
			store := storeAt(t, root)
			must(t, store.Join(epicA, joins(piece)))
			must(t, os.Remove(pathOf(root, missing)))
			read := view(t, root)
			if why := read.Unreadable(a); !strings.Contains(why, says) || read.Unreadable(b) != "" {
				t.Fatalf("Unreadable() = %q and %q, want only the first", why, read.Unreadable(b))
			}
			wantError(t, store.Begin(a), says)
			wantError(t, store.Join(epicA, joins("yoyodyne-more")), says)
			must(t, store.Join(epicB, joins("yoyodyne-other")))
			// Ending it and naming it again mends it.
			_, err := operatorAt(t, root).End(epicA, "", namedAt)
			must(t, err)
			must(t, store.Begin(name(t, root, epicA, namedAt.Add(time.Hour), memberA)))
		})
	}
}

// Rule 5.
func TestAFileThatCannotBeReadWholeIsAnErrorAndIsNotWrittenOver(t *testing.T) {
	t.Parallel()
	for _, file := range []string{progressName, begunName} {
		for broken, spoil := range map[string]func(good string) string{
			"empty":                  func(string) string { return "" },
			"null":                   func(string) string { return "null" },
			"something after it":     func(good string) string { return good + "{}" },
			"half of it":             func(good string) string { return good[:len(good)/2] },
			"a field not known":      func(good string) string { return strings.Replace(good, "{", `{"newer": 1,`, 1) },
			"another schema version": func(good string) string { return strings.Replace(good, `version": 1`, `version": 2`, 1) },
			"its list lost":          func(string) string { return `{"schema_version": 1, "product_id": "yoyodyne"}` },
			"past its limit":         func(good string) string { return strings.Repeat(" ", maxFileBytes) + good },
			"a link":                 nil,
		} {
			t.Run(file+" "+broken, func(t *testing.T) {
				t.Parallel()
				root, a, b := twoNamed(t)
				store := storeAt(t, root)
				must(t, store.Begin(a))
				path, good := pathOf(root, file), contents(t, root, file)
				must(t, os.Remove(path))
				if spoil == nil {
					must(t, os.Symlink(filepath.Join(root, "gone"), path))
				} else {
					must(t, os.WriteFile(path, []byte(spoil(good)), 0o600))
				}
				before, err := os.Lstat(path)
				must(t, err)
				if _, err := Read(root, product, epicscope.Record{}); err == nil {
					t.Error("it was read")
				}
				if store.Begin(b) == nil || store.Join(epicB, joins(piece)) == nil {
					t.Error("a write was accepted")
				}
				if after, err := os.Lstat(path); err != nil || !os.SameFile(before, after) {
					t.Errorf("it was replaced: %v", err)
				}
			})
		}
	}
}

func TestTwoJoinsOfOneItemAtOncePutItInOneNamingOnly(t *testing.T) {
	t.Parallel()
	root, _, _ := twoNamed(t)
	wantError(t, storeAt(t, root).Join(epicA, joins(memberB)), "already in epic "+epicB)
	for round := range 10 {
		item := fmt.Sprintf("%s.%d", piece, round)
		var waiting sync.WaitGroup
		results := make([]error, 2)
		for at, epic := range []string{epicA, epicB} {
			store := storeAt(t, root)
			waiting.Go(func() { results[at] = store.Join(epic, joins(item)) })
		}
		waiting.Wait()
		if (results[0] == nil) == (results[1] == nil) {
			t.Fatalf("the two joins came to %v, want one refused", results)
		}
		wantError(t, errors.Join(results...), "already in epic")
		if holders := view(t, root).Holders(item); len(holders) != 1 {
			t.Fatalf("%s is held by %+v", item, holders)
		}
	}
}

// A join takes the operator's lock first, so it has not taken its own, nor
// written anything, while that one is held; and it takes its own inside it.
func TestAJoinTakesTheOperatorsLockAndThenItsOwn(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	done := make(chan error, 1)
	before := contents(t, root, "epic-scope.json")
	must(t, epicscope.UnderLock(root, product, func(record epicscope.Record) error {
		// What is done to the reading reaches nothing.
		record.Mode, record.Epics[0].Members = epicscope.ModeGoal, nil
		store := storeAt(t, root)
		go func() { done <- store.Join(epicA, joins(piece)) }()
		select {
		case err := <-done:
			t.Errorf("the join did not wait for the operator's lock: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		// The progress lock is still free, so the join has not taken it.
		return store.Begin(a)
	}))
	must(t, <-done)
	if !entry(t, root, a).Has(piece) || contents(t, root, "epic-scope.json") != before {
		t.Fatal("the piece did not join, or the operator's record was changed")
	}
}

func TestACallIsReservedBeforeItIsMadeAndMadeOnce(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	must(t, storeAt(t, root).Point(a, "run-1", "a run", "the record of run run-1"))
	_, err := storeAt(t, root).Call(a, "run-1", "a run", nil, nil)
	wantError(t, err, "already reserved in the record of run")
	_, err = storeAt(t, root).Call(a, "judge-1", "a judgment", nil, func() (string, error) {
		got := entry(t, root, a).Reservations
		if len(got) != 2 || got[1].ID != "judge-1" || got[1].Result != nil {
			t.Errorf("while it was made the reservations were %+v", got)
		}
		return "inside", nil
	})
	must(t, err)
	if says, err := storeAt(t, root).Call(a, "judge-1", "a judgment", nil, nil); err != nil || says != "inside" {
		t.Fatalf("made again, Call() = %q, %v", says, err)
	}
}

// A call that was cut off leaves a reservation with no result. A store opened
// afterwards, as at a restart, looks it up before it sends anything.
func TestAReservationWithNoResultIsLookedUpBeforeAnythingIsSentAgain(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		says   string
		looked Looked
		result Result
		sent   bool
	}{
		"found":          {says: "inside", looked: Found, result: Result{Says: "inside"}},
		"never sent":     {looked: NotSent, result: Result{Says: "sent again"}, sent: true},
		"cannot be told": {looked: CannotTell, result: Result{Says: "nothing shows whether it was sent", Unknown: true}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, a, _ := twoNamed(t)
			cut := errors.New("cut off")
			if _, err := storeAt(t, root).Call(a, "judge-1", "a judgment", nil, func() (string, error) { return "", cut }); !errors.Is(err, cut) {
				t.Fatal(err)
			}
			looked, sent := false, false
			_, err := storeAt(t, root).Call(a, "judge-1", "a judgment",
				func() (string, Looked, error) { looked = true; return test.says, test.looked, nil },
				func() (string, error) {
					if !looked {
						t.Error("it was sent before it was looked up")
					}
					sent = true
					return "sent again", nil
				})
			unknown := test.result.Unknown
			if !looked || sent != test.sent || errors.Is(err, ErrResultUnknown) != unknown || (err != nil) != unknown {
				t.Fatalf("looked up %t, sent %t, error %v", looked, sent, err)
			}
			got := entry(t, root, a).Reservations[0].Result
			if got.Says != test.result.Says || got.Unknown != unknown || got.At.IsZero() {
				t.Fatalf("the result kept is %+v, want %+v", got, test.result)
			}
			if unknown {
				if _, err := storeAt(t, root).Call(a, "judge-1", "a judgment", nil, nil); !errors.Is(err, ErrResultUnknown) {
					t.Fatalf("asked once more: %v", err)
				}
			}
		})
	}
}

// The operator's act reads the progress under the lock a join takes first, so
// only one of the two puts the item in an epic.
func TestAJoinAndAnOperatorsAddOfOneItemAtOncePutItInOneEpicOnly(t *testing.T) {
	t.Parallel()
	root, _, _ := twoNamed(t)
	operator := operatorAt(t, root).Knowing(Holding(root, product))
	for round := range 10 {
		item := fmt.Sprintf("%s.%d", piece, round)
		var waiting sync.WaitGroup
		var joined, added error
		store := storeAt(t, root)
		waiting.Go(func() { joined = store.Join(epicA, joins(item)) })
		waiting.Go(func() { _, added = operator.Add(epicB, item, namedAt) })
		waiting.Wait()
		if (joined == nil) == (added == nil) {
			t.Fatalf("the join came to %v and the add to %v, want one refused", joined, added)
		}
		wantError(t, errors.Join(joined, added), "already in epic")
		if holders := view(t, root).Holders(item); len(holders) != 1 {
			t.Fatalf("%s is held by %+v", item, holders)
		}
	}
}

func TestAnOperatorsActRefusesAnItemThatJoinedAnotherOpenEpic(t *testing.T) {
	t.Parallel()
	root, _, _ := twoNamed(t)
	operator := operatorAt(t, root).Knowing(Holding(root, product))
	must(t, storeAt(t, root).Join(epicA, joins(piece)))
	_, err := operator.Add(epicB, piece, namedAt)
	wantError(t, err, "already in epic "+epicA)
	for _, naming := range []epicscope.Naming{{Epic: piece}, {Epic: "yoyodyne-new", Members: []string{piece}}} {
		naming.From = "terminal"
		_, err = operator.Start(naming, namedAt)
		wantError(t, err, "already in epic "+epicA)
	}
	// Its own epic may take it as a member, where it is still one item.
	_, err = operator.Add(epicA, piece, namedAt)
	must(t, err)
	read := view(t, root)
	named, _ := read.Record.Named(epicA)
	if got := read.Items(named); !reflect.DeepEqual(got, []string{memberA, piece}) || len(read.Holders(piece)) != 1 {
		t.Fatalf("the epic has %v", got)
	}
	// With a file not whole the act is refused, and ending still works.
	must(t, os.WriteFile(pathOf(root, begunName), []byte("{"), 0o600))
	_, err = operator.Add(epicB, "yoyodyne-more", namedAt)
	wantError(t, err, begunName)
	_, err = operator.End(epicA, "", namedAt)
	must(t, err)
}

func TestARunIsReservedInTheEpicsItIsForAndCanBeTakenBack(t *testing.T) {
	t.Parallel()
	root, a, b := twoNamed(t)
	store := storeAt(t, root)
	reserved := func(naming Naming) []string {
		var ids []string
		found, _ := view(t, root).Entry(naming)
		for _, reservation := range found.Reservations {
			ids = append(ids, reservation.HeldBy)
		}
		return ids
	}
	_, err := store.ReserveRun("run-0", []string{"yoyodyne-outside"})
	wantError(t, err, "in no open epic")
	_, err = store.ReserveRun("run-1", []string{memberA})
	must(t, err)
	// A document is for each epic that holds, or is, what it answers.
	withdraw, err := store.ReserveRun("run-2", []string{memberA, epicB, "yoyodyne-outside"})
	must(t, err)
	if got := reserved(a); !reflect.DeepEqual(got, []string{"the record of run run-1", "the record of run run-2"}) || len(reserved(b)) != 1 {
		t.Fatalf("reserved %v and %v", got, reserved(b))
	}
	// One that found the reservation made takes nothing back.
	again, err := store.ReserveRun("run-2", []string{epicB})
	must(t, err)
	must(t, again())
	if len(reserved(b)) != 1 {
		t.Fatal("another start's reservation was taken back")
	}
	must(t, withdraw())
	if len(reserved(a)) != 1 || len(reserved(b)) != 0 {
		t.Fatalf("taken back, reserved %v and %v", reserved(a), reserved(b))
	}
	must(t, os.Remove(pathOf(root, begunName)))
	_, err = store.ReserveRun("run-3", []string{memberA})
	wantError(t, err, "cannot be read")
	must(t, os.WriteFile(pathOf(root, progressName), []byte("{"), 0o600))
	_, err = operatorAt(t, root).TurnOff()
	must(t, err)
	// With epic mode off nothing is read.
	_, err = store.ReserveRun("run-4", []string{memberA})
	must(t, err)
}

// A run from before progress was kept, or that an entry points to, shows no loss.
func TestWhichRunsAreOlderThanAnythingKept(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	before, ran, begun := keptSince.Add(-time.Hour), keptSince.Add(time.Hour), keptSince.Add(2*time.Hour)
	if view(t, root).Precedes(a, "run-1", before) || !view(t, root).Precedes(a, "run-1", ran) {
		t.Fatal("with nothing kept, want only the run since progress was kept")
	}
	store := storeAt(t, root)
	store.now = func() time.Time { return begun }
	_, err := store.ReserveRun("run-2", []string{memberA})
	must(t, err)
	for run, want := range map[string]bool{"run-1": true, "run-2": false} {
		if got := view(t, root).Precedes(a, run, ran); got != want {
			t.Errorf("%s is older than anything kept: %t, want %t", run, got, want)
		}
	}
	if view(t, root).Precedes(a, "run-3", begun) {
		t.Error("a run since the entry was begun is not older than it")
	}
}

func TestNoMorePiecesJoinThanTheLimit(t *testing.T) {
	t.Parallel()
	root, _, _ := twoNamed(t)
	store := storeAt(t, root)
	for at := range MaxJoined {
		must(t, store.Join(epicA, joins(fmt.Sprintf("yoyodyne-piece.%d", at))))
	}
	wantError(t, store.Join(epicA, joins(piece)), "12 pieces have joined")
	must(t, store.Join(epicB, joins(piece)))
}

// A check that passed is owed again once a head handed over moves past it, and
// that check is not a round of correction, whatever it finds. A member added or a
// piece joined since is a new opening of the epic rather than a repair.
func TestACheckMadeAgainAfterARepairIsNotARoundOfCorrection(t *testing.T) {
	t.Parallel()
	named := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	epic := epicscope.Epic{WorkItemID: "yoyodyne-ep", Members: []epicscope.Member{{WorkItemID: "yoyodyne-ep.1", AddedAt: named}}}
	passed := Check{ID: "check-1", Commit: "head-1 head-9", Found: FoundAllMet, At: named.Add(time.Hour)}
	entry := Entry{Checks: []Check{passed}}
	for heads, want := range map[string]bool{"head-1": false, "head-1 head-9": false, "head-2": true, "": false} {
		if got := entry.Repaired(epic, strings.Fields(heads)); got != want {
			t.Errorf("heads %q: repaired is %t, want %t", heads, got, want)
		}
	}
	added := epic
	added.Members = append(added.Members, epicscope.Member{WorkItemID: "yoyodyne-ep.2", AddedAt: named.Add(2 * time.Hour)})
	joined := Entry{Checks: entry.Checks, Joined: []Joined{{WorkItemID: "yoyodyne-piece", At: named.Add(2 * time.Hour)}}}
	if added := entry.Repaired(added, []string{"head-2"}); added || joined.Repaired(epic, []string{"head-2"}) {
		t.Error("a check owed after the epic opened to more work is taken for one owed after a repair")
	}
	entry.Checks = append(entry.Checks, Check{ID: "check-2", Commit: "head-2", Found: FoundNotMet, At: named.Add(3 * time.Hour), Again: true})
	if rounds := entry.Rounds(epic); rounds != 0 {
		t.Errorf("%d rounds of correction, want none", rounds)
	}
	if entry.Repaired(epic, []string{"head-3"}) {
		t.Error("a check that did not pass is taken for one that delivered the epic")
	}
	entry.Checks[1].Again = false
	if rounds := entry.Rounds(epic); rounds != 1 {
		t.Errorf("%d rounds of correction, want one", rounds)
	}
}

// A store handed a clock stamps from it and leaves the store it came from alone.
func TestAStoreHandedAClockStampsFromIt(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	plain := storeAt(t, root)
	if plain.WithClock(nil) != plain {
		t.Fatal("a nil clock changed the store")
	}
	at := namedAt.Add(-365 * 24 * time.Hour)
	must(t, plain.WithClock(func() time.Time { return at }).Begin(a))
	if begun := entry(t, root, a).BegunAt; !begun.Equal(at) {
		t.Errorf("begun at %s, want %s", begun, at)
	}
	if plain.now().Year() == at.Year() {
		t.Error("the store the clock was handed to reads it too")
	}
}
