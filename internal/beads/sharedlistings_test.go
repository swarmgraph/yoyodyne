package beads

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// verbRunner answers bd by its verb, keeping every command it was asked to
// run. A listing waits for hold to be closed where hold is set.
type verbRunner struct {
	mu       sync.Mutex
	commands [][]string
	hold     chan struct{}
	started  chan struct{}
}

const twoStatusListing = `[{"id":"yoyodyne-1","title":"One","status":"open"},{"id":"yoyodyne-2","title":"Two","status":"in_progress"},{"id":"yoyodyne-3","title":"Three","status":"blocked"}]`

const claimedItem = `[{"id":"yoyodyne-1","title":"One","status":"in_progress"}]`

func (r *verbRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.mu.Lock()
	r.commands = append(r.commands, append([]string(nil), command.Args...))
	r.mu.Unlock()
	switch command.Args[0] {
	case "list", "ready":
		if r.started != nil {
			r.started <- struct{}{}
		}
		if r.hold != nil {
			<-r.hold
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: twoStatusListing}, nil
	case "show":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `[{"id":"yoyodyne-1","title":"One","status":"open"}]`}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: claimedItem}, nil
}

// asked counts the commands run whose verb is verb.
func (r *verbRunner) asked(verb string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, command := range r.commands {
		if command[0] == verb {
			count++
		}
	}
	return count
}

func (r *verbRunner) verbs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var verbs []string
	for _, command := range r.commands {
		verbs = append(verbs, strings.Join(command, " "))
	}
	return verbs
}

// listingClockAt is a clock a test moves by hand.
type listingClockAt struct{ now atomic.Int64 }

func (c *listingClockAt) read() time.Time       { return time.Unix(0, c.now.Load()) }
func (c *listingClockAt) move(by time.Duration) { c.now.Add(int64(by)) }

func newListingClock() *listingClockAt {
	clock := &listingClockAt{}
	clock.now.Store(time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC).UnixNano())
	return clock
}

func TestCallersWithinThirtySecondsShareOneListing(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{}
	clock := newListingClock()
	// Each caller builds its own client, as the scheduler, the dashboard and
	// status each do.
	client := func() Client {
		return Client{Runner: runner, Binary: "bd-test", Dir: dir, listingClock: clock.read}
	}
	ctx := context.Background()

	open, err := client().List(ctx, "open")
	if err != nil || len(open) != 1 || open[0].ID != "yoyodyne-1" {
		t.Fatalf("List(open) = %v, %v; want yoyodyne-1 alone", open, err)
	}
	clock.move(10 * time.Second)
	statuses, err := client().ListStatuses(ctx, []string{"open", "blocked", "in_progress"})
	if err != nil || len(statuses) != 3 {
		t.Fatalf("ListStatuses() = %v, %v; want all three", statuses, err)
	}
	clock.move(10 * time.Second)
	unfinished, err := client().ListUnfinished(ctx)
	if err != nil || len(unfinished) != 3 {
		t.Fatalf("ListUnfinished() = %v, %v; want all three", unfinished, err)
	}
	if got := runner.asked("list"); got != 1 {
		t.Fatalf("bd list ran %d times for three callers within 30 seconds, want 1: %v", got, runner.verbs())
	}

	for range 2 {
		if _, err := client().Ready(ctx); err != nil {
			t.Fatalf("Ready() error = %v", err)
		}
		clock.move(5 * time.Second)
	}
	if got := runner.asked("ready"); got != 1 {
		t.Fatalf("bd ready ran %d times for two callers within 30 seconds, want 1", got)
	}

	// Each caller decodes its own copy.
	open[0].Title = "changed"
	again, _ := client().List(ctx, "open")
	if again[0].Title == "changed" {
		t.Fatal("callers share one decoded listing")
	}
}

func TestListingNamingClosedWorkIsNotShared(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{}
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir}
	ctx := context.Background()
	for range 2 {
		if _, err := client.List(ctx, "closed"); err != nil {
			t.Fatal(err)
		}
		if _, err := client.List(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := runner.asked("list"); got != 4 {
		t.Fatalf("bd list ran %d times for four listings naming closed work, want 4", got)
	}
}

func TestWriteClearsBothSharedListings(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{}
	clock := newListingClock()
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir, listingClock: clock.read}
	ctx := context.Background()

	if _, err := client.ListUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	// A write made by another client of the same store in this process clears
	// them as well as one made by this client.
	writer := Client{Runner: runner, Binary: "bd-other", Dir: dir}
	if _, err := writer.run(ctx, "update", "yoyodyne-1", "--append-notes=n", "--json"); err != nil {
		t.Fatal(err)
	}
	clock.move(time.Second)
	if _, err := client.ListUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runner.asked("list"); got != 2 {
		t.Fatalf("bd list ran %d times around a write, want 2", got)
	}
	if got := runner.asked("ready"); got != 2 {
		t.Fatalf("bd ready ran %d times around a write, want 2", got)
	}
	// A read clears nothing.
	if _, err := client.run(ctx, "show", "yoyodyne-1", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runner.asked("list"); got != 2 {
		t.Fatalf("bd list ran %d times after a read, want still 2", got)
	}
}

func TestListingBegunBeforeWriteEndsIsNotKept(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{hold: make(chan struct{}), started: make(chan struct{}, 4)}
	// The line would make the write wait for the listing; skipping it stands for
	// a write that lands while the listing is being read.
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir, UnorderedCalls: true}
	ctx := context.Background()

	listed := make(chan error, 1)
	go func() { _, err := client.ListUnfinished(ctx); listed <- err }()
	<-runner.started
	if _, err := client.run(ctx, "update", "yoyodyne-1", "--claim", "--json"); err != nil {
		t.Fatal(err)
	}
	close(runner.hold)
	if err := <-listed; err != nil {
		t.Fatalf("the listing under way when the write landed failed: %v", err)
	}
	if _, err := client.ListUnfinished(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runner.asked("list"); got != 2 {
		t.Fatalf("bd list ran %d times, want 2: the listing begun before the write must not be kept", got)
	}
}

func TestSharedListingOlderThanThirtySecondsIsReadAfresh(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{}
	clock := newListingClock()
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir, listingClock: clock.read}
	ctx := context.Background()

	for _, step := range []time.Duration{0, 29 * time.Second, 2 * time.Second} {
		clock.move(step)
		if _, err := client.ListUnfinished(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Ready(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Read at 0, shared at 29 seconds, and read afresh at 31.
	if got := runner.asked("list"); got != 2 {
		t.Fatalf("bd list ran %d times, want 2", got)
	}
	if got := runner.asked("ready"); got != 2 {
		t.Fatalf("bd ready ran %d times, want 2", got)
	}
}

func TestFailedSharedListingIsNotKept(t *testing.T) {
	dir := listingStore(t)
	runner := &gatedRunner{release: make(chan struct{}), status: execution.ProcessFailed}
	close(runner.release)
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir}
	ctx := context.Background()
	for range 2 {
		if _, err := client.Ready(ctx); err == nil {
			t.Fatal("Ready() answered over a failed listing")
		}
	}
	if got := runner.calls.Load(); got != 2 {
		t.Fatalf("bd ran %d times for two callers after a failure, want 2", got)
	}
}

func TestClaimReadsItsItemAfresh(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{}
	reads := &ItemReads{}
	// The item held from a read ahead of need says it was claimed by a person;
	// the tracker says it is open and unassigned now.
	reads.hold(map[string]WorkItem{"yoyodyne-1": {ID: "yoyodyne-1", Status: "open", Assignee: "someone"}}, time.Now())
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir, Reads: reads}
	ctx := context.Background()

	if _, err := client.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Claim(ctx, "yoyodyne-1"); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	verbs := runner.verbs()
	show, claim := -1, -1
	for index, command := range verbs {
		if strings.HasPrefix(command, "show yoyodyne-1") && show < 0 {
			show = index
		}
		if strings.HasPrefix(command, "update yoyodyne-1 --claim") && claim < 0 {
			claim = index
		}
	}
	if show < 0 || claim < 0 || show > claim {
		t.Fatalf("the claim did not read its item from the tracker before claiming it: %v", verbs)
	}
	if reads.Held() != 0 {
		t.Fatalf("the claim left the item read ahead held: %d", reads.Held())
	}
	// The claim cleared the shared listing of what is ready.
	if _, err := client.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runner.asked("ready"); got != 2 {
		t.Fatalf("bd ready ran %d times around a claim, want 2", got)
	}
}

func TestLiveClientListsAfresh(t *testing.T) {
	dir := listingStore(t)
	runner := &verbRunner{}
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir}
	ctx := context.Background()
	if _, err := client.List(ctx, "open"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.live().unfinished(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runner.asked("list"); got != 3 {
		t.Fatalf("bd list ran %d times, want 3: the shared listing, then open and blocked afresh", got)
	}
}
