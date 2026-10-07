package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestWhatASessionSaidSurvivesTheProcessThatSaidIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	session := testWatchSessionID
	transitions := []WatchTransition{
		testWatchTransition(session, WatchWatching, "watching the backlog until stopped"),
		testWatchTransition(session, WatchIdle, "the backlog is empty"),
		testWatchTransition(session, WatchStopped, "the scheduler was cancelled"),
	}
	for _, transition := range transitions {
		if err := store.Record(transition); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	// The reader is a different process: what makes a watch session legible at
	// all is that somebody who is not at its terminal can read what it is doing.
	reloaded := newTestWatchStore(t, root)
	recorded, err := reloaded.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != len(transitions) {
		t.Fatalf("List() = %#v, want every transition in the order it happened", recorded)
	}
	for index, transition := range transitions {
		if recorded[index].State != transition.State || recorded[index].Reason != transition.Reason {
			t.Fatalf("transition %d = %#v, want %#v", index, recorded[index], transition)
		}
	}
	latest, watched, err := reloaded.Latest()
	if err != nil || !watched {
		t.Fatalf("Latest() = watched %v, error %v", watched, err)
	}
	if latest.State != WatchStopped {
		t.Fatalf("latest state = %q, want where the session actually got to", latest.State)
	}
}

// A product nobody has watched has no session rather than an idle one: never
// having watched and having stopped watching are different answers, and only one
// of them is a reason to go looking for a dead process.
func TestAProductNobodyHasWatchedHasNoSession(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	transitions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("List() = %#v, want nothing", transitions)
	}
	if _, watched, err := store.Latest(); err != nil || watched {
		t.Fatalf("Latest() = watched %v, error %v, want no session at all", watched, err)
	}
}

// One product has one watching session. Two of them read one queue and choose
// from it independently, and an item chosen is not in the run store until the
// run reserves, so the second session picks work the first has already taken in
// exactly that window -- which is what happened while the 2026-09-05 wedge was
// being cleared.
func TestOneProductHasOneWatchingSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	lease, held, err := store.Lease(testWatchSessionID)
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the first session admitted", held, err)
	}
	// A second session against the same product, from a store built exactly as
	// another process would build it.
	second := newTestWatchStore(t, root)
	if _, held, err := second.Lease("watch-fedcba9876543210fedcba9876543210"); err != nil || held {
		t.Fatalf("second Lease() = %t, %v, want the second session refused while the first watches", held, err)
	}
	// And the refusal can say which session it was refused for, rather than only
	// that somebody is there.
	holder, found, err := second.Holder()
	if err != nil || !found {
		t.Fatalf("Holder() = %#v, found %v, error %v, want the session holding it named", holder, found, err)
	}
	if holder.SessionID != testWatchSessionID || holder.PID != os.Getpid() || holder.HeldAt.IsZero() {
		t.Fatalf("holder = %#v, want the session, the process and when it took the watch", holder)
	}
	// And which build it is, which is what the supervisor reads to know a session
	// that restarted itself into a deployed build has moved.
	if holder.Build != buildinfo.Commit() {
		t.Fatalf("holder build = %q, want the holding process's own %q", holder.Build, buildinfo.Commit())
	}

	// The session ends, and the next one is admitted. What the first left behind
	// is nothing: the stamp goes with the lock, so a reader is never told a
	// session holds a watch it has let go of.
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, found, err := store.Holder(); err != nil || found {
		t.Fatalf("Holder() after release = found %v, error %v, want nobody named", found, err)
	}
	next, held, err := second.Lease("watch-fedcba9876543210fedcba9876543210")
	if err != nil || !held {
		t.Fatalf("Lease() after release = %t, %v, want the next session admitted", held, err)
	}
	if err := next.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A watch is held for a session, so a lease asked for under a name nothing
// generated is refused rather than taken under a name no surface can match.
func TestAWatchIsHeldForASessionThatCanBeNamed(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	if _, held, err := store.Lease("session-one"); err == nil || held {
		t.Fatalf("Lease() with an invented session = %t, %v, want a refusal", held, err)
	}
	// A stamp that will not decode is a holder nobody can name, which is reported
	// rather than answered with a session invented from a broken file.
	lease, held, err := store.Lease(testWatchSessionID)
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the session admitted", held, err)
	}
	defer lease.Release()
	if err := os.WriteFile(filepath.Join(store.Root(), watchHolderFile), []byte("{not json}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Holder(); err == nil {
		t.Fatal("Holder() error = nil, want an unreadable stamp reported rather than read as nobody")
	}
}

func TestATransitionThatCannotBeReadBackOrDoesNotBelongHereIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	elsewhere := testWatchTransition(testWatchSessionID, WatchIdle, "")
	elsewhere.ProductID = "another-product"
	if err := store.Record(elsewhere); err == nil {
		t.Fatal("Record() error = nil, want another product's session refused")
	}
	unnamed := testWatchTransition("session-one", WatchIdle, "")
	if err := store.Record(unnamed); err == nil {
		t.Fatal("Record() error = nil, want a session identifier nothing generated refused")
	}
	// A state nothing says is refused at the write rather than reaching a reader
	// that has no words for it.
	invented := testWatchTransition(testWatchSessionID, WatchState("pondering"), "")
	if err := store.Record(invented); err == nil {
		t.Fatal("Record() error = nil, want a state no session takes refused")
	}
	verbose := testWatchTransition(testWatchSessionID, WatchIdle, strings.Repeat("x", MaxWatchReasonBytes+1))
	if err := store.Record(verbose); err == nil {
		t.Fatal("Record() error = nil, want a reason past its bound refused")
	}
	// The build is handed to Git by whatever measures the session's age, so a
	// field that could carry anything is refused at the write rather than there.
	misbuilt := testWatchTransition(testWatchSessionID, WatchIdle, "")
	misbuilt.Build = "--upload-pack=touch /tmp/x"
	if err := store.Record(misbuilt); err == nil {
		t.Fatal("Record() error = nil, want a build that is not a revision refused")
	}
	// The two fields a reader takes whose move follows an idle session from. A
	// count nothing observed and a marker naming no role would both leave a surface
	// saying something about the machine that nobody recorded.
	miscounted := testWatchTransition(testWatchSessionID, WatchIdle, "")
	miscounted.Running = -1
	if err := store.Record(miscounted); err == nil {
		t.Fatal("Record() error = nil, want a negative count of runs in flight refused")
	}
	misaddressed := testWatchTransition(testWatchSessionID, WatchIdle, "")
	misaddressed.Executor = "conversation:nobody"
	if err := store.Record(misaddressed); err == nil {
		t.Fatal("Record() error = nil, want a marker naming no role refused")
	}
}

// A session idle on one slot while a run works on the other is the state that
// was read three times as a line that had stopped. What it recorded said only
// that it had started nothing, so every surface downstream had to guess: these
// two fields are what it says instead, and they survive the process that said
// them like everything else here.
func TestASessionRecordsWhatItSawGoingAndWhoItIsWaitingOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	idle := testWatchTransition(testWatchSessionID, WatchIdle,
		"1 run in flight; 3 items passed over, of 3 admitted: carried in conversation (architect: yoyodyne-ifd.212)")
	idle.Running = 1
	idle.Executor = domain.ConversationWith(domain.RoleArchitect)
	if err := store.Record(idle); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the one transition back", recorded)
	}
	if recorded[0].Running != 1 || recorded[0].Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("transition = %#v, want the runs it saw and the conversation it waits on carried", recorded[0])
	}
	if recorded[0].Unreadable {
		t.Fatalf("transition = %#v, want a queue it read left unmarked", recorded[0])
	}

	// The other poll that chose nothing, and the one no admission would change:
	// the store would not answer, so what is in the queue is not what stopped it.
	outage := testWatchTransition(testWatchSessionID, WatchIdle, "the harness could not be read and is being read again")
	outage.Unreadable = true
	if err := store.Record(outage); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	reread, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reread) != 2 || !reread[1].Unreadable {
		t.Fatalf("transitions = %#v, want the reading that failed marked as one", reread)
	}
}

// A session that stays open runs whatever binary it was started with, and
// nothing else in the record says which. It is carried on every transition
// rather than only the opening one, because a reader arriving in the middle of a
// night reads the entry the session happened to write last.
//
// A session started by a binary that stamped no revision records none, and that
// is an ordinary entry rather than a malformed one: what it costs is the one
// comparison.
func TestASessionRecordsTheBuildItIsRunning(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	build := "4c1f2b3a9d8e7f6a5b4c3d2e1f0099887766554433221100aabbccddeeff0011"
	running := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	running.Build = build
	if err := store.Record(running); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	unstamped := testWatchTransition(testWatchSessionID, WatchIdle, "the backlog is empty")
	if err := store.Record(unstamped); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both transitions", recorded)
	}
	if recorded[0].Build != build {
		t.Fatalf("build = %q, want %q", recorded[0].Build, build)
	}
	if recorded[1].Build != "" {
		t.Fatalf("build = %q, want a binary that stamped nothing to record nothing", recorded[1].Build)
	}
}

// A session that stopped to be restarted into a build deployed over it says so
// on the stop, because the state alone cannot tell that apart from a line
// somebody closed — and the two ask opposite things of whoever reads the log.
//
// The mark is a field rather than a state of its own on purpose: a reader from
// before it existed ignores an unknown field and refuses an unknown state, and
// the reader running while a redeploy happens is exactly the older one.
func TestASessionSaysWhenItsStopIsARestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	restarting := testWatchTransition(testWatchSessionID, WatchStopped, "a build was deployed over the one this session was started from")
	restarting.Restarting = true
	if err := store.Record(restarting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	ended := testWatchTransition(testWatchSessionID, WatchStopped, "the operator stopped it")
	if err := store.Record(ended); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both stops", recorded)
	}
	if !recorded[0].Restarting {
		t.Fatal("the stop that was a restart reads back as an ending")
	}
	if recorded[1].Restarting {
		t.Fatal("the stop the operator asked for reads back as a restart")
	}
	// Only a stop can be one. A session marked as coming back while it is still
	// watching would have every surface announcing a restart nothing is going to
	// make.
	watching := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	watching.Restarting = true
	if err := store.Record(watching); err == nil {
		t.Fatal("Record() error = nil, want a restart marked on something that is not a stop refused")
	}
}

// A session draining to restart into a build deployed over it says so on every
// line it writes while it drains, with the bound on the wait: a reader of any of
// them is owed what stops the wait rather than left to time it, because on
// 2026-09-19 the wait was two hours and nothing said what it was waiting on.
//
// The mark is a field rather than a state of its own for the reason the restart
// above is: a reader from before it existed ignores an unknown field and refuses
// an unknown state.
func TestASessionSaysWhenItIsDrainingAndUnderWhatBound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	since := time.Date(2026, 9, 19, 7, 35, 0, 0, time.UTC)
	draining := testWatchTransition(testWatchSessionID, WatchIdle, "nothing more is ready to pull")
	draining.Draining = &WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1}
	if err := store.Record(draining); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	reached := testWatchTransition(testWatchSessionID, WatchIdle, "the bound has run out")
	reached.Draining = &WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1, BoundReached: true}
	if err := store.Record(reached); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[0].Draining == nil || recorded[1].Draining == nil {
		t.Fatalf("List() = %#v, want both lines carrying the drain", recorded)
	}
	if got := recorded[0].Draining; got.Bound() != 15*time.Minute || !got.Since.Equal(since) || got.Hosting != 1 || got.BoundReached {
		t.Fatalf("drain = %#v, want the bound, the start, and the hosted run read back", got)
	}
	if !recorded[1].Draining.BoundReached {
		t.Fatal("the line that said the bound had run out reads back as one still waiting")
	}
	for _, said := range []string{"since 2026-09-19T07:35:00Z", "bounded at 15m0s", "until 2026-09-19T07:50:00Z", "waiting out 1 run(s)"} {
		if !strings.Contains(recorded[0].Draining.Says(), said) {
			t.Fatalf("Says() = %q, want %q in it", recorded[0].Draining.Says(), said)
		}
	}
	if !strings.Contains(recorded[1].Draining.Says(), "the bound has run out") {
		t.Fatalf("Says() = %q, want the bound running out said", recorded[1].Draining.Says())
	}
	// A drain waiting out a check stage past its bound says so and until when,
	// and one claiming to wait on a stage with no end named is refused.
	checking := WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 2, BoundReached: true, Checking: 1, ChecksUntil: since.Add(30 * time.Minute)}
	for _, said := range []string{"waiting out a check stage in 1 run(s)", "until 2026-09-19T08:05:00Z at the latest", "developer attempt or a review"} {
		if !strings.Contains(checking.Says(), said) {
			t.Fatalf("Says() = %q, want %q in it", checking.Says(), said)
		}
	}
	endless := testWatchTransition(testWatchSessionID, WatchIdle, "draining")
	endless.Draining = &WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), BoundReached: true, Checking: 1}
	if err := store.Record(endless); err == nil {
		t.Fatal("Record() error = nil, want a check wait with no end refused")
	}
	// A drain waiting out a promotion past its bound says so and since when, reads
	// back whole, and one claiming that wait with no start named is refused.
	promoting := testWatchTransition(testWatchSessionID, WatchIdle, "draining")
	promoting.Draining = &WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1, BoundReached: true, Promoting: 1, PromotingSince: since.Add(15 * time.Minute)}
	if err := store.Record(promoting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if recorded, err := newTestWatchStore(t, root).List(); err != nil || recorded[len(recorded)-1].Draining.Promoting != 1 ||
		!recorded[len(recorded)-1].Draining.PromotingSince.Equal(since.Add(15*time.Minute)) {
		t.Fatalf("List() = %#v, %v; want the promotion wait read back", recorded, err)
	}
	for _, said := range []string{"waiting out 1 run(s) it hosts at their promotion since 2026-09-19T07:50:00Z", "restarts the moment they finish"} {
		if !strings.Contains(promoting.Draining.Says(), said) {
			t.Fatalf("Says() = %q, want %q in it", promoting.Draining.Says(), said)
		}
	}
	undated := testWatchTransition(testWatchSessionID, WatchIdle, "draining")
	undated.Draining = &WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), BoundReached: true, Promoting: 1}
	if err := store.Record(undated); err == nil {
		t.Fatal("Record() error = nil, want a promotion wait with no start refused")
	}
	// A drain with no bound is the wait this field exists to bound, so it is
	// refused rather than recorded as a wait on nothing.
	unbounded := testWatchTransition(testWatchSessionID, WatchIdle, "draining")
	unbounded.Draining = &WatchDrain{Since: since, Until: since}
	if err := store.Record(unbounded); err == nil {
		t.Fatal("Record() error = nil, want a drain with no bound refused")
	}
}

// A session waiting out the provider's usage window says so on the poll it made
// inside one, and says when the provider named the window lifting. Nothing else
// in the record distinguishes that poll from a poll over an empty queue, and on
// 2026-09-05 ninety minutes of it was read as a line that had quietly stopped.
//
// The mark is a field rather than a state of its own for the reason the restart
// above is: a reader from before it existed ignores an unknown field and refuses
// an unknown state.
func TestASessionSaysWhenAPollWasMadeInsideTheProvidersWindow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	waiting := testWatchTransition(testWatchSessionID, WatchIdle, "waiting on the provider's usage window until 13:43Z")
	waiting.ProviderWindow = true
	waiting.ProviderWindowResetsAt = &lifts
	if err := store.Record(waiting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	// A provider that named no reset time records none rather than a moment
	// nobody said: the harness asks again rather than being told when.
	untimed := testWatchTransition(testWatchSessionID, WatchIdle, "waiting on the provider's usage window")
	untimed.ProviderWindow = true
	if err := store.Record(untimed); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both polls", recorded)
	}
	if !recorded[0].ProviderWindow || recorded[0].ProviderWindowResetsAt == nil ||
		!recorded[0].ProviderWindowResetsAt.Equal(lifts) {
		t.Fatalf("the timed window reads back as %+v", recorded[0])
	}
	if !recorded[1].ProviderWindow || recorded[1].ProviderWindowResetsAt != nil {
		t.Fatalf("the untimed window reads back as %+v", recorded[1])
	}

	// Only an idle poll can be one. A session marked as held by the provider while
	// it is watching would have every surface accounting for a silence something
	// else is causing, which is the alarm this is supposed to keep honest.
	watching := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	watching.ProviderWindow = true
	if err := store.Record(watching); err == nil {
		t.Fatal("Record() error = nil, want a window marked on something that is not an idle poll refused")
	}
	// A deadline with nothing waiting on it is a moment nobody is held to, and a
	// surface reading it would say the harness is held until a time it is not.
	orphaned := testWatchTransition(testWatchSessionID, WatchIdle, "the backlog is empty")
	orphaned.ProviderWindowResetsAt = &lifts
	if err := store.Record(orphaned); err == nil {
		t.Fatal("Record() error = nil, want a reset time with no window to belong to refused")
	}
}

// What a poll passed over, kept in classes rather than only in the prose the
// reason states. The prose is for whoever reads the log; the classes are for
// whatever has to answer a question about the queue, which on 2026-09-06 was an
// alarm that derived its own answer beside a session that had already found this
// one.
func TestASessionRecordsWhatItPassedOverInClasses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	idle := testWatchTransition(testWatchSessionID, WatchIdle,
		"33 items passed over, of 47 admitted: held for a person (yoyodyne-ifd.212, and 32 further)")
	idle.PassedOver = PassedOver{Admitted: 47, Groups: []PassedOverGroup{
		{Class: PassedOverHeldForAPerson, Count: 33, Items: []string{"yoyodyne-ifd.212"}},
		{Class: PassedOverCarriedInConversation, Role: domain.RoleArchitect, Count: 9},
	}}
	if err := store.Record(idle); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].PassedOver.Admitted != 47 || recorded[0].PassedOver.Passed() != 42 {
		t.Fatalf("transitions = %#v, want the account back with its counts", recorded)
	}

	// Only an idle poll passes anything over: a watching session is starting what
	// it read, and a braked or stopped one never got as far as the queue.
	watching := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	watching.PassedOver = PassedOver{Admitted: 1, Groups: []PassedOverGroup{{Class: PassedOverParked, Count: 1}}}
	if err := store.Record(watching); err == nil {
		t.Fatal("Record() error = nil, want an account of a pull no watching session made refused")
	}
	// A class nothing named is an item disappearing into a count that says
	// something else about it, which is the misreading the taxonomy exists to end.
	unnamed := testWatchTransition(testWatchSessionID, WatchIdle, "1 item passed over, of 1 admitted")
	unnamed.PassedOver = PassedOver{Admitted: 1, Groups: []PassedOverGroup{{Class: "something-nobody-named", Count: 1}}}
	if err := store.Record(unnamed); err == nil {
		t.Fatal("Record() error = nil, want a class outside the taxonomy refused")
	}
	// And an account that contradicts itself: every fraction said about a class is
	// drawn from its count, so a count under what it names is refused rather than
	// rendered.
	miscounted := testWatchTransition(testWatchSessionID, WatchIdle, "2 items passed over, of 2 admitted")
	miscounted.PassedOver = PassedOver{Admitted: 2, Groups: []PassedOverGroup{
		{Class: PassedOverParked, Count: 1, Items: []string{"one", "two"}},
	}}
	if err := store.Record(miscounted); err == nil {
		t.Fatal("Record() error = nil, want a group naming more items than it counts refused")
	}
}

// A log that cannot be read is an error rather than an absence, for the reason
// every other record here is: a session nobody can read must not be reported as
// a session that never ran.
func TestAnUnreadableWatchLogIsAnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	if err := store.Record(testWatchTransition(testWatchSessionID, WatchWatching, "")); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List() error = nil, want an unreadable log refused rather than read as empty")
	}
}

func TestWatchSessionIdentifiersAreDistinct(t *testing.T) {
	t.Parallel()

	first, err := NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	second, err := NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	if first == second {
		t.Fatalf("two sessions were named %q, want each session identifiable on its own", first)
	}
	if !watchSessionIDPattern.MatchString(first) {
		t.Fatalf("session id = %q, want the shape the record is validated against", first)
	}
}

const testWatchSessionID = "watch-0123456789abcdef0123456789abcdef"

func newTestWatchStore(t *testing.T, root string) *WatchStore {
	t.Helper()
	store, err := NewWatchStore(filepath.Clean(root), "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	return store
}

func testWatchTransition(sessionID string, state WatchState, reason string) WatchTransition {
	return WatchTransition{
		SchemaVersion: WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         state,
		At:            time.Date(2026, 8, 19, 21, 0, 0, 0, time.UTC),
		Reason:        reason,
	}
}

// What excluded an item this session already tried survives the session that
// tried it, against the item it names. It is refused where it could not be read
// back that way: more reasons than names is an account nothing can attach to an
// item, and a reason past the bound is a line nobody reads at a glance.
func TestASessionRecordsWhyItExcludedWhatItAlreadyTried(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	reason := "this session tried it and the dispatch failed before any run was recorded: the checkout is dirty"
	idle := testWatchTransition(testWatchSessionID, WatchIdle,
		"2 items passed over, of 74 admitted: already tried this session (yoyodyne-ifd.353 — "+reason+", yoyodyne-ifd.354 — "+reason+")")
	idle.PassedOver = PassedOver{Admitted: 74, Groups: []PassedOverGroup{{
		Class:   PassedOverAlreadyTried,
		Count:   2,
		Items:   []string{"yoyodyne-ifd.353", "yoyodyne-ifd.354"},
		Reasons: []string{reason, reason},
	}}}
	if err := store.Record(idle); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || len(recorded[0].PassedOver.Groups) != 1 {
		t.Fatalf("transitions = %#v, want the account back", recorded)
	}
	if got := recorded[0].PassedOver.Groups[0].Reasons; len(got) != 2 || got[0] != reason || got[1] != reason {
		t.Fatalf("reasons = %q, want what excluded each item read back against it", got)
	}

	unattached := testWatchTransition(testWatchSessionID, WatchIdle, "1 item passed over, of 1 admitted")
	unattached.PassedOver = PassedOver{Admitted: 1, Groups: []PassedOverGroup{{
		Class: PassedOverAlreadyTried, Count: 1, Items: []string{"one"}, Reasons: []string{reason, reason},
	}}}
	if err := store.Record(unattached); err == nil {
		t.Fatal("Record() error = nil, want more reasons than named items refused")
	}
	unbounded := testWatchTransition(testWatchSessionID, WatchIdle, "1 item passed over, of 1 admitted")
	unbounded.PassedOver = PassedOver{Admitted: 1, Groups: []PassedOverGroup{{
		Class: PassedOverAlreadyTried, Count: 1, Items: []string{"one"}, Reasons: []string{strings.Repeat("x", MaxPassedOverReasonBytes+1)},
	}}}
	if err := store.Record(unbounded); err == nil {
		t.Fatal("Record() error = nil, want a reason past the bound refused")
	}
}

// A dispatch's wait on the tracker is written onto the log as a watching entry,
// which is what a reader that knows nothing of it takes it for, and it is not
// where the session got to: the session is exactly as it was.
func TestADispatchWaitIsANoteOnTheLogAndNotWhereTheSessionGotTo(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	idle := testWatchTransition(testWatchSessionID, WatchIdle, "nothing further pullable")
	note := testWatchTransition(testWatchSessionID, WatchWatching, "the dispatch for yoyodyne-task is waiting out a tracker failure")
	note.At = idle.At.Add(time.Minute)
	note.DispatchWait = &DispatchWait{
		WorkItemID:   "yoyodyne-task",
		Boundary:     RetryDependencyRead,
		Attempt:      1,
		DelaySeconds: 1,
		At:           note.At,
		Failure:      "bd show failed with status timed_out",
	}
	for _, transition := range []WatchTransition{idle, note} {
		if err := store.Record(transition); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	recorded, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[1].DispatchWait == nil || *recorded[1].DispatchWait != *note.DispatchWait {
		t.Fatalf("List() = %#v, want the wait read back whole", recorded)
	}
	if !recorded[1].DispatchWait.Until().Equal(note.At.Add(time.Second)) {
		t.Fatalf("Until() = %s, want the moment the dispatch asks again", recorded[1].DispatchWait.Until())
	}
	latest, watched, err := store.Latest()
	if err != nil || !watched || latest.State != WatchIdle {
		t.Fatalf("Latest() = %#v (watched %v, error %v), want the idle poll rather than the note", latest, watched, err)
	}

	// A wait said on any other state would tell an older reader the session had
	// braked, idled, or stopped, and one that names no item or no retry says
	// nothing anybody can act on.
	for name, broken := range map[string]func(*WatchTransition){
		"on an idle entry":    func(t *WatchTransition) { t.State = WatchIdle },
		"with no item":        func(t *WatchTransition) { t.DispatchWait.WorkItemID = "" },
		"with no boundary":    func(t *WatchTransition) { t.DispatchWait.Boundary = "" },
		"with no retry":       func(t *WatchTransition) { t.DispatchWait.Attempt = 0 },
		"with no moment":      func(t *WatchTransition) { t.DispatchWait.At = time.Time{} },
		"with a negative one": func(t *WatchTransition) { t.DispatchWait.DelaySeconds = -1 },
	} {
		refused := note
		wait := *note.DispatchWait
		refused.DispatchWait = &wait
		broken(&refused)
		if err := store.Record(refused); err == nil {
			t.Errorf("Record() of a dispatch wait %s was accepted", name)
		}
	}
}

// A Git command a dispatch ran again over another worktree's creation or removal
// is a note on the log in exactly the way a dispatch's wait is: read back whole,
// and read past as where the session got to.
func TestAWorktreeCrossingIsANoteOnTheLogAndNotWhereTheSessionGotTo(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	idle := testWatchTransition(testWatchSessionID, WatchIdle, "nothing further pullable")
	note := testWatchTransition(testWatchSessionID, WatchWatching, "")
	note.At = idle.At.Add(time.Minute)
	note.WorktreeCrossing = &WorktreeCrossing{
		WorkItemID: "yoyodyne-task",
		Command:    "git worktree add",
		Attempt:    1,
		Attempts:   3,
		At:         note.At,
		Refusal:    "Invalid path '.git/worktrees/run-0b1434ad'",
	}
	note.Reason = note.WorktreeCrossing.Says()
	for _, transition := range []WatchTransition{idle, note} {
		if err := store.Record(transition); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	recorded, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[1].WorktreeCrossing == nil || *recorded[1].WorktreeCrossing != *note.WorktreeCrossing || !recorded[1].Note() {
		t.Fatalf("List() = %#v, want the crossing read back whole, as a note", recorded)
	}
	for _, says := range []string{"yoyodyne-task", "`git worktree add`", "attempt 1 of 3", "Invalid path"} {
		if !strings.Contains(recorded[1].Reason, says) {
			t.Errorf("reason = %q, want it to say %q", recorded[1].Reason, says)
		}
	}
	latest, watched, err := store.Latest()
	if err != nil || !watched || latest.State != WatchIdle {
		t.Fatalf("Latest() = %#v (watched %v, error %v), want the idle poll rather than the note", latest, watched, err)
	}

	for name, broken := range map[string]func(*WatchTransition){
		"on an idle entry": func(t *WatchTransition) { t.State = WatchIdle },
		"beside a dispatch wait": func(t *WatchTransition) {
			t.DispatchWait = &DispatchWait{WorkItemID: "yoyodyne-task", Boundary: RetryDependencyRead, Attempt: 1, At: t.At}
		},
		"with no item":             func(t *WatchTransition) { t.WorktreeCrossing.WorkItemID = "" },
		"with no command":          func(t *WatchTransition) { t.WorktreeCrossing.Command = " " },
		"with no attempt":          func(t *WatchTransition) { t.WorktreeCrossing.Attempt = 0 },
		"past the attempts it had": func(t *WatchTransition) { t.WorktreeCrossing.Attempt = 4 },
		"with no moment":           func(t *WatchTransition) { t.WorktreeCrossing.At = time.Time{} },
	} {
		refused := note
		crossing := *note.WorktreeCrossing
		refused.WorktreeCrossing = &crossing
		broken(&refused)
		if err := store.Record(refused); err == nil {
			t.Errorf("Record() of a worktree crossing %s was accepted", name)
		}
	}
}

func TestOnlyAnIdlePollThatCouldNotReadTheStoreIsARetriedRead(t *testing.T) {
	t.Parallel()

	for _, state := range []WatchState{WatchWatching, WatchIdle, WatchBraked, WatchStopped} {
		unread := WatchTransition{State: state, Unreadable: true}
		if got, want := unread.RetryingRead(), state == WatchIdle; got != want {
			t.Errorf("an unreadable %s transition: RetryingRead() = %v, want %v", state, got, want)
		}
		if (WatchTransition{State: state}).RetryingRead() {
			t.Errorf("a readable %s transition reads as a retried read", state)
		}
	}
}
