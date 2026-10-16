package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// slowReader is a read model whose standing build blocks until the test
// releases it, answering with whatever the release carries, and counts the
// builds it was asked for.
type slowReader struct {
	stubReader
	release chan error
	mu      sync.Mutex
	builds  int
}

func (r *slowReader) Standing(ctx context.Context) (readmodel.Standing, error) {
	r.mu.Lock()
	r.builds++
	build := r.builds
	r.mu.Unlock()
	select {
	case err := <-r.release:
		if err != nil {
			return readmodel.Standing{}, err
		}
		return standingWith(fmt.Sprintf("build %d", build)), nil
	case <-ctx.Done():
		return readmodel.Standing{}, ctx.Err()
	}
}

func (r *slowReader) built() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.builds
}

// heldClock is a clock the test moves, and a wait between builds the test
// ends: the builder says it is asleep, and builds again when ticked.
type heldClock struct {
	mu     sync.Mutex
	at     time.Time
	asleep chan struct{}
	tick   chan bool
}

func (c *heldClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *heldClock) advance(by time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(by)
	c.mu.Unlock()
}

func (c *heldClock) sleep(ctx context.Context, _ time.Duration) bool {
	select {
	case c.asleep <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	select {
	case again := <-c.tick:
		return again
	case <-ctx.Done():
		return false
	}
}

// answered is one /api/standing answer decoded: its snapshot and the title the
// build put in it.
type answered struct {
	Snapshot     SnapshotAge `json:"snapshot"`
	NotStartable []struct {
		Title string `json:"title"`
	} `json:"not_startable"`
}

// A slow builder costs a request nothing: every request is served the latest
// snapshot with the moment it was taken and its age, two tabs asking at once
// cost one build, a request made while a build is under way is answered at once
// from the one before, a failed build leaves the last snapshot served with the
// failure named beside its age, and a reading nobody asks for stops being
// built.
func TestTheStandingIsASnapshotServedWithItsAgeWhateverTheBuilderIsDoing(t *testing.T) {
	t.Parallel()
	reader := &slowReader{release: make(chan error)}
	w := serve(t, reader)
	start := time.Date(2026, 9, 26, 16, 26, 0, 0, time.UTC)
	clock := &heldClock{at: start, asleep: make(chan struct{}), tick: make(chan bool)}
	w.server.standing.now = clock.now
	w.server.standing.sleep = clock.sleep
	// The two first requests below wait for the first build, which the test
	// releases at once; the bound on that wait is held by its own test.
	w.server.standing.firstWait = time.Hour
	life, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	w.server.standing.live(life)

	// ask is one request. It carries no bound: a request that waits on a build
	// it should not be waiting on waits on a build the test is holding, and the
	// binary's own -timeout reports that hang naming where it waited, rather
	// than a bound a loaded machine could reach with the request answering.
	ask := func() (int, answered, string) {
		t.Helper()
		response, body := w.get("/api/standing", bearer(w.server.Token()))
		var decoded answered
		if response.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
		}
		return response.StatusCode, decoded, body
	}
	title := func(got answered) string {
		if len(got.NotStartable) == 0 {
			return ""
		}
		return got.NotStartable[0].Title
	}

	// Two tabs ask before anything has been built. Both wait for the first
	// build, and there is one build between them.
	type first struct {
		status int
		got    answered
	}
	firsts := make(chan first, 2)
	for range 2 {
		go func() {
			response, body := w.get("/api/standing", bearer(w.server.Token()))
			var decoded answered
			_ = json.Unmarshal([]byte(body), &decoded)
			firsts <- first{response.StatusCode, decoded}
		}()
	}
	reader.release <- nil
	<-clock.asleep
	for range 2 {
		got := <-firsts
		if got.status != http.StatusOK || title(got.got) != "build 1" || got.got.Snapshot.AgeSeconds != 0 || !got.got.Snapshot.TakenAt.Equal(start) {
			t.Fatalf("first reading: %d %+v", got.status, got.got)
		}
	}
	if built := reader.built(); built != 1 {
		t.Fatalf("two tabs cost %d builds, not one", built)
	}

	// Twenty-five seconds on, the next build starts and is slow. A request made
	// while it runs is answered at once from the first, with its age, and says
	// it is older than two intervals.
	clock.advance(25 * time.Second)
	clock.tick <- true
	for reader.built() < 2 {
		time.Sleep(time.Millisecond)
	}
	status, got, body := ask()
	if status != http.StatusOK || title(got) != "build 1" || got.Snapshot.AgeSeconds != 25 || got.Snapshot.IntervalSeconds != 10 || !got.Snapshot.Stale || got.Snapshot.Failure != "" {
		t.Fatalf("reading during a slow build: %d %s", status, body)
	}
	// The reading's own fields are what they were: the snapshot is beside them.
	if !strings.Contains(body, `"observed_at":"2026-09-18T12:00:00Z"`) || !strings.HasPrefix(body, `{"snapshot":{"taken_at":"2026-09-26T16:26:00Z"`) {
		t.Fatalf("the reading lost its own fields: %s", body)
	}

	// The slow build fails. The first reading is still served, with the
	// failure named beside its age rather than an error.
	reader.release <- errors.New("bd list timed out after 30s")
	<-clock.asleep
	clock.advance(5 * time.Second)
	status, got, body = ask()
	if status != http.StatusOK || title(got) != "build 1" || got.Snapshot.AgeSeconds != 30 || got.Snapshot.Failure != "bd list timed out after 30s" || got.Snapshot.FailedAt == nil {
		t.Fatalf("reading after a failed build: %d %s", status, body)
	}

	// The next build succeeds: the failure is gone and the age starts again.
	clock.tick <- true
	reader.release <- nil
	<-clock.asleep
	clock.advance(3 * time.Second)
	status, got, body = ask()
	if status != http.StatusOK || title(got) != "build 3" || got.Snapshot.AgeSeconds != 3 || got.Snapshot.Stale || got.Snapshot.Failure != "" || got.Snapshot.FailedAt != nil {
		t.Fatalf("reading after a recovered build: %d %s", status, body)
	}
	if built := reader.built(); built != 3 {
		t.Fatalf("%d builds for three intervals and four requests", built)
	}

	// Nobody asks for longer than the idle bound: the builder stops by itself.
	clock.advance(idleAfter + time.Second)
	clock.tick <- true
	for {
		w.server.standing.mu.Lock()
		running := w.server.standing.running
		w.server.standing.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if built := reader.built(); built != 3 {
		t.Fatalf("an idle reading was built again: %d builds", built)
	}
}

// Where every build so far has failed there is nothing to serve, and the
// request is refused with what the build said, carrying nothing else.
func TestAReadingNeverBuiltIsRefusedWithTheFailure(t *testing.T) {
	t.Parallel()
	reader := &slowReader{release: make(chan error, 1)}
	reader.release <- errors.New("the state root could not be resolved")
	w := serve(t, reader)
	life, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	w.server.standing.live(life)

	response, body := w.get("/api/standing", bearer(w.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "snapshot") {
		t.Fatalf("a reading never built: %d %s", response.StatusCode, body)
	}
}

// A standing whose build takes longer than the page's refresh interval is
// still answered at once: the first request after a restart is refused within
// a few seconds saying how long the first build has been under way, rather
// than held for the whole build, and once a reading has been taken every
// request is served it with its age and how long it took. A reading as new as
// a slow build can make it is not stale; one older than two rounds of the
// builder's own schedule is.
func TestASlowStandingIsAnsweredAtOnceWithItsAgeRatherThanAsStale(t *testing.T) {
	t.Parallel()
	reader := &slowReader{release: make(chan error)}
	w := serve(t, reader)
	start := time.Date(2026, 10, 14, 15, 5, 0, 0, time.UTC)
	clock := &heldClock{at: start, asleep: make(chan struct{}), tick: make(chan bool)}
	w.server.standing.now = clock.now
	w.server.standing.sleep = clock.sleep
	w.server.standing.firstWait = 20 * time.Millisecond
	life, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	w.server.standing.live(life)

	ask := func() (int, answered, string, time.Duration) {
		t.Helper()
		asked := time.Now()
		response, body := w.get("/api/standing", bearer(w.server.Token()))
		took := time.Since(asked)
		var decoded answered
		if response.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
		}
		return response.StatusCode, decoded, body, took
	}

	// The dashboard starts its first build before anybody asks.
	w.server.standing.start()
	for reader.built() < 1 {
		time.Sleep(time.Millisecond)
	}
	// Thirty seconds into a first build that takes fifty-eight, a request is
	// refused at once, saying so.
	clock.advance(30 * time.Second)
	status, _, body, took := ask()
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "first reading since it started has been under way for 30s and is not finished yet") || strings.Contains(body, "snapshot") {
		t.Fatalf("a request during the first slow build: %d %s", status, body)
	}
	if took > 5*time.Second {
		t.Fatalf("a request during the first slow build took %s to be refused", took)
	}

	// The build ends fifty-eight seconds after it began. Thirty-four seconds
	// later the reading is ninety-two seconds old, which is as new as a
	// fifty-eight-second build rebuilt every ten seconds can make it, and it
	// is served at once, said with its age and not as stale.
	clock.advance(28 * time.Second)
	reader.release <- nil
	<-clock.asleep
	clock.advance(34 * time.Second)
	status, got, body, took := ask()
	if status != http.StatusOK || got.Snapshot.AgeSeconds != 92 || got.Snapshot.BuildSeconds != 58 || got.Snapshot.IntervalSeconds != 10 || got.Snapshot.Stale {
		t.Fatalf("a slow but current reading: %d %s", status, body)
	}
	if took > 5*time.Second {
		t.Fatalf("a slow but current reading took %s to serve", took)
	}

	// The next build hangs. Once the reading is older than two intervals and
	// two of its builds, the building has fallen behind, and it is stale.
	clock.tick <- true
	for reader.built() < 2 {
		time.Sleep(time.Millisecond)
	}
	clock.advance(45 * time.Second)
	if status, got, body, _ = ask(); status != http.StatusOK || got.Snapshot.AgeSeconds != 137 || !got.Snapshot.Stale {
		t.Fatalf("a reading behind a hung build: %d %s", status, body)
	}
}

// A stopping dashboard waits for a build it cancelled to end, so nothing the
// dashboard started is still reading or writing once it has stopped.
func TestAStoppedReadingIsWaitedForUntilItsBuildEnds(t *testing.T) {
	t.Parallel()
	reader := &slowReader{release: make(chan error)}
	w := serve(t, reader)
	life, stop := context.WithCancel(context.Background())
	w.server.standing.live(life)
	// The build ignores its context, as a build reading files from disk does,
	// until the test lets it end.
	w.server.standing.build = func(context.Context) (any, error) {
		return reader.Standing(context.Background())
	}
	w.server.standing.start()
	for reader.built() < 1 {
		time.Sleep(time.Millisecond)
	}
	stop()

	expired := make(chan struct{})
	close(expired)
	if w.server.standing.settled(expired) {
		t.Fatal("the reading said it had ended while its build was still running")
	}
	reader.release <- nil
	if !w.server.standing.settled(make(chan struct{})) {
		t.Fatal("the reading did not end once its build had")
	}
}
