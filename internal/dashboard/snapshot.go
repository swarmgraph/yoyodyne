package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// The readings the pages poll — the standing, the throughput, the spend, and
// the delivery measures — are built in the background, once per interval, and every request is
// served the latest one with the moment it was taken. Until yoyodyne-ifd.432.16
// each request built its reading from scratch: the whole tracker listing and
// every run record for every poll of every tab, which at a load average of 117
// took 6.4 seconds a standing and, under heavier load, past the tracker's own
// thirty-second timeout, so the page hung or errored exactly when the harness
// was busiest. Now two tabs cost one build, a slow build costs a request
// nothing, and a build that fails leaves the last reading served with the
// failure named beside its age rather than a refusal.

const (
	// standingInterval is how long after one standing build ends the next
	// starts. It is the page's own ten-second clock, so a tab polling at its
	// ordinary rate is handed a reading at most one interval and one build old.
	standingInterval = 10 * time.Second
	// minuteInterval is the throughput's, the spend's, and the delivery
	// measures', which the pages ask for once a minute: the spend prices every
	// event log a month holds, and the delivery measures read every merged
	// change's.
	minuteInterval = time.Minute
	// accomplishmentInterval is what the spend accomplished, which reads the
	// whole spend log and every conversation's tracker actions and changes on
	// the scale of a pass rather than a poll.
	accomplishmentInterval = 15 * time.Minute
	// firstWait is how long a request waits for a reading no build has
	// finished yet, which is every request in the first build after the
	// dashboard starts. Past it the request is refused saying how long the
	// first build has been under way, and the page asks again, rather than
	// holding the request for however long the build takes.
	firstWait = 4 * time.Second
	// idleAfter is how long a reading goes on being built with nobody asking
	// for it. A dashboard nobody has open builds nothing past it, and the next
	// request is served the last reading with its age and starts the building
	// again. It is longer than the page's slowest backed-off poll, so an open
	// tab never lets the building lapse.
	idleAfter = 5 * time.Minute
)

// SnapshotAge is what every reading served from a snapshot carries beside it,
// under "snapshot": when it was taken, how old it was when served, how often it
// is taken, how long the latest build took, whether it is older than the
// builder's own schedule leaves it, and — where the latest build since failed —
// what the failure said and when. The page shows the age from it and says so
// plainly when the reading is stale.
type SnapshotAge struct {
	TakenAt         time.Time `json:"taken_at"`
	AgeSeconds      int64     `json:"age_seconds"`
	IntervalSeconds int64     `json:"interval_seconds"`
	// BuildSeconds is how long the latest build took, whether it succeeded or
	// failed. A reading that takes longer to build than its interval is never
	// younger than its build, however promptly it is rebuilt.
	BuildSeconds int64 `json:"build_seconds"`
	// Stale is the reading being older than two rounds of its builder's own
	// schedule — two intervals and two of the latest build's durations: a build
	// is stuck or failing, and what is shown is not what the harness is doing
	// now. A build that is only slow is not stale, because each reading is as
	// new as one build of it can be; its age says how old that is.
	Stale bool `json:"stale"`
	// Failure is what the latest build said when it failed, after this reading
	// was taken. It is empty when the latest build succeeded.
	Failure  string     `json:"failure,omitempty"`
	FailedAt *time.Time `json:"failed_at,omitempty"`
}

// snapshot is one reading kept for every request. Its builder runs on its own
// goroutine, started by the first request that asks and stopping itself once
// nobody has asked for idleAfter, or when the server stops — so the load it
// carries is bounded by itself rather than by a cleanup somebody has to reach.
type snapshot struct {
	build    func(context.Context) (any, error)
	interval time.Duration
	idle     time.Duration
	now      func() time.Time
	// sleep waits the interval between builds, and says false where the
	// context ended first. It is a field so a test can hold the builder
	// between builds rather than waiting ten real seconds.
	sleep func(context.Context, time.Duration) bool
	// firstWait bounds a request's wait for the first build; see firstWait.
	firstWait time.Duration

	mu sync.Mutex
	// life is what a build runs under: the server's lifetime once Serve has
	// started, and until then a context nothing cancels, which the idle bound
	// still stops.
	life context.Context
	// encoded is the latest reading, encoded once when it was built rather than
	// once per request; have is whether there is one at all.
	encoded  []byte
	have     bool
	takenAt  time.Time
	failure  string
	failedAt time.Time
	// took is how long the latest build to end took, and buildingSince when
	// the build under way started, zero where none is.
	took          time.Duration
	buildingSince time.Time
	// attempted is closed once the first build has ended, either way. A
	// request before then waits for it, because there is nothing yet to serve.
	attempted chan struct{}
	running   bool
	askedAt   time.Time
	// loops counts the builders running, so a stopping server can wait for the
	// one it cancelled to end rather than leave it writing after the stop.
	loops sync.WaitGroup
}

func newSnapshot(interval time.Duration, build func(context.Context) (any, error)) *snapshot {
	return &snapshot{
		build:     build,
		interval:  interval,
		idle:      idleAfter,
		now:       time.Now,
		sleep:     sleep,
		firstWait: firstWait,
		life:      context.Background(),
		attempted: make(chan struct{}),
	}
}

// live sets the lifetime every later build runs under.
func (s *snapshot) live(ctx context.Context) {
	s.mu.Lock()
	s.life = ctx
	s.mu.Unlock()
}

// get is one request's reading: the latest one, encoded, with its age as of
// now. It never builds: it starts the builder where none is running, and waits
// only where no build has ever ended — for firstWait at most, and within the
// request's own context. Where the only builds there have been failed, the
// latest failure is the answer, as an error.
func (s *snapshot) get(ctx context.Context) ([]byte, SnapshotAge, error) {
	s.mu.Lock()
	s.askedAt = s.now()
	s.wake()
	attempted := s.attempted
	s.mu.Unlock()

	bound := time.NewTimer(s.firstWait)
	defer bound.Stop()
	select {
	case <-attempted:
	case <-bound.C:
		return nil, SnapshotAge{}, s.notYet()
	case <-ctx.Done():
		return nil, SnapshotAge{}, errors.New("the dashboard's first reading was still being taken when the request ended; the page asks again")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.have {
		return nil, SnapshotAge{}, errors.New(s.failure)
	}
	age := s.now().Sub(s.takenAt)
	if age < 0 {
		age = 0
	}
	served := SnapshotAge{
		TakenAt:         s.takenAt.UTC(),
		AgeSeconds:      int64(age / time.Second),
		IntervalSeconds: int64(s.interval / time.Second),
		BuildSeconds:    int64(s.took / time.Second),
		Stale:           age > 2*(s.interval+s.took),
	}
	if s.failure != "" {
		failedAt := s.failedAt.UTC()
		served.Failure = s.failure
		served.FailedAt = &failedAt
	}
	return s.encoded, served, nil
}

// start begins building now, ahead of any request, so the first request after
// the dashboard starts finds the first build already under way. Nobody asking
// still stops it after the idle bound.
func (s *snapshot) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.askedAt = s.now()
	s.wake()
}

// wake starts the builder where none is running. The caller holds mu.
func (s *snapshot) wake() {
	if !s.running {
		s.running = true
		s.loops.Add(1)
		go func() {
			defer s.loops.Done()
			s.loop(s.life)
		}()
	}
}

// settled waits for every builder to end, until the deadline. It is what a
// server calls once it has cancelled their lifetime, and it says whether they
// all ended in time.
func (s *snapshot) settled(deadline <-chan struct{}) bool {
	ended := make(chan struct{})
	go func() {
		s.loops.Wait()
		close(ended)
	}()
	select {
	case <-ended:
		return true
	case <-deadline:
		return false
	}
}

// notYet is the refusal of a request that waited firstWait for a first build
// still under way: how long it has been under way, so a slow first build reads
// as one rather than as a dashboard that is not answering.
func (s *snapshot) notYet() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buildingSince.IsZero() {
		return errors.New("the dashboard has not yet started its first reading; the page asks again")
	}
	under := s.now().Sub(s.buildingSince).Round(time.Second)
	return fmt.Errorf("the dashboard's first reading since it started has been under way for %s and is not finished yet; the page asks again", under)
}

// loop builds, waits an interval from the end of the build, and builds again,
// until nobody has asked for idle or its context ends. Waiting from the end
// rather than the start is what keeps a build slower than its interval from
// running back to back and becoming the load it was taken to relieve.
func (s *snapshot) loop(ctx context.Context) {
	for {
		s.take(ctx)
		if !s.sleep(ctx, s.interval) {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		if s.now().Sub(s.askedAt) > s.idle {
			s.running = false
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// sleep is the real wait between builds.
func sleep(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// take is one build, recorded. A success replaces the reading and clears any
// failure; a failure keeps the reading and records what it said.
func (s *snapshot) take(ctx context.Context) {
	started := s.now()
	s.mu.Lock()
	s.buildingSince = started
	s.mu.Unlock()
	reading, err := s.build(ctx)
	var encoded []byte
	if err == nil {
		encoded, err = encode(reading)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.took = s.now().Sub(started)
	if s.took < 0 {
		s.took = 0
	}
	s.buildingSince = time.Time{}
	if err != nil {
		s.failure = err.Error()
		s.failedAt = s.now()
	} else {
		s.encoded = encoded
		s.have = true
		s.takenAt = started
		s.failure = ""
		s.failedAt = time.Time{}
	}
	select {
	case <-s.attempted:
	default:
		close(s.attempted)
	}
}

// encode is a reading as the JSON object it is served as, with tags escaped as
// every answer's are.
func encode(reading any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(reading); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSpace(buffer.Bytes())
	if len(encoded) < 2 || encoded[0] != '{' {
		return nil, errors.New("the reading did not encode as a JSON object")
	}
	return encoded, nil
}

// withAge is the encoded reading with its age added as a "snapshot" member,
// first, so the reading's own fields are exactly what they were and a tool
// that read them before reads them unchanged.
func withAge(encoded []byte, age SnapshotAge) ([]byte, error) {
	meta, err := json.Marshal(age)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Grow(len(encoded) + len(meta) + 16)
	out.WriteString(`{"snapshot":`)
	out.Write(meta)
	rest := bytes.TrimSpace(encoded[1:])
	if len(rest) > 0 && rest[0] != '}' {
		out.WriteByte(',')
	}
	out.Write(rest)
	out.WriteByte('\n')
	return out.Bytes(), nil
}
