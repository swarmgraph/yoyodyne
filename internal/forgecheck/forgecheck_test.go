package forgecheck_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/forgecheck"
	"github.com/mason-bryant/yoyodyne/internal/maintain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const readLine = "forge check: read and acted on; hosting what it started"

// machine stands in for the processes the supervisor starts. `yoyo reconcile`
// is held until released, which is a maintenance pass under way for as long as
// a test likes; `yoyo forge-check` says it has read and then waits on hold, or
// ends at once, or fails, as the test sets.
type machine struct {
	mu          sync.Mutex
	checks      int
	reconciles  int
	failCheck   bool
	failText    string
	holdCheck   chan struct{}
	releaseMain chan struct{}
}

func (m *machine) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	switch command.Args[0] {
	case "reconcile":
		m.mu.Lock()
		m.reconciles++
		m.mu.Unlock()
		select {
		case <-m.releaseMain:
		case <-ctx.Done():
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	case "forge-check":
		m.mu.Lock()
		m.checks++
		fail, hold, text := m.failCheck, m.holdCheck, m.failText
		m.mu.Unlock()
		if fail {
			if text == "" {
				text = "pull request #7 of yoyodyne-task not settled: ask the forge: dial tcp: lookup api.github.com: no such host"
			}
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: text}, nil
		}
		if observer != nil {
			observer(execution.Output{Stream: execution.StreamStderr, Text: readLine})
		}
		if hold != nil {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "forge check: 1 pull request(s) waiting for a person to merge them, 1 read for feedback"}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2}, nil
}

func (m *machine) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checks, m.reconciles
}

func newCheck(m *machine) *forgecheck.Check {
	return &forgecheck.Check{
		Every:      5 * time.Minute,
		Program:    "/opt/calc/bin/yoyo",
		Config:     "/opt/calc/.yoyodyne/config.yaml",
		Repository: "/opt/calc",
		ReadLine:   readLine,
		Runner:     m,
	}
}

// settle waits until the check under way has finished reading, so the next
// look sees the schedule as it stands rather than racing the process.
func settle(t *testing.T, check *forgecheck.Check) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for check.Reading() {
		if time.Now().After(deadline) {
			t.Fatal("the forge check never finished reading")
		}
		time.Sleep(time.Millisecond)
	}
}

// A maintenance pass that is still under way holds no forge check back: the
// checks go on every five minutes beside it, and the pass is still running
// once they have.
func TestAForgeCheckKeepsItsScheduleWhileAMaintenancePassIsRunning(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &machine{releaseMain: make(chan struct{})}
	sweeps, err := runstate.NewSweepStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	pass := &maintain.Pass{
		Product:    "calc",
		Every:      10 * time.Minute,
		Program:    "/opt/calc/bin/yoyo",
		Config:     "/opt/calc/.yoyodyne/config.yaml",
		Repository: "/opt/calc",
		Claims:     sweeps,
		Runner:     m,
		Now:        func() time.Time { return start },
	}
	check := newCheck(m)

	// The supervisor's looks, every thirty seconds for twenty minutes, with the
	// pass's reconcile held the whole time.
	for elapsed := time.Duration(0); elapsed <= 20*time.Minute; elapsed += 30 * time.Second {
		now := start.Add(elapsed)
		pass.Look(ctx, now)
		check.Look(ctx, now)
		settle(t, check)
	}
	checks, reconciles := m.counts()
	if reconciles != 1 {
		t.Fatalf("maintenance ran reconcile %d times, want the one pass still under way", reconciles)
	}
	if checks != 5 {
		t.Fatalf("the forge was checked %d times in twenty minutes beside a running maintenance pass, want five, one every five minutes", checks)
	}
	if _, finished := pass.Last(); finished {
		t.Fatal("the maintenance pass finished, want it still under way while the checks ran")
	}
	close(m.releaseMain)
	check.Wait(ctx)
}

// A check that cannot reach the forge holds nothing back: it is said, and the
// next check comes at the ordinary interval rather than sooner or never.
func TestAFailedForgeCheckIsTriedAgainAtTheNextInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := &machine{failCheck: true}
	var logged []string
	check := newCheck(m)
	check.Log = func(format string, args ...any) { logged = append(logged, format) }
	start := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)

	check.Look(ctx, start)
	check.Wait(ctx)
	if last, found := check.Last(); !found || !last.Failed {
		t.Fatalf("last check = %+v, %t; want it recorded as not finished cleanly", last, found)
	}
	check.Look(ctx, start.Add(4*time.Minute))
	if checks, _ := m.counts(); checks != 1 {
		t.Fatalf("the forge was checked %d times within the interval, want once", checks)
	}
	m.mu.Lock()
	m.failCheck = false
	m.mu.Unlock()
	check.Look(ctx, start.Add(5*time.Minute))
	check.Wait(ctx)
	if last, _ := check.Last(); last.Failed {
		t.Fatalf("last check = %+v, want the next check to have run cleanly", last)
	}
	if checks, _ := m.counts(); checks != 2 || len(logged) != 2 {
		t.Fatalf("checks = %d, log lines = %d; want the failure and the next check each said", checks, len(logged))
	}
}

// One check reads at a time, and a check that has finished reading and goes
// on to host a run it started does not hold the next check back.
func TestACheckHostingARunDoesNotHoldTheNextCheckBack(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hold := make(chan struct{})
	m := &machine{holdCheck: hold}
	check := newCheck(m)
	start := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)

	check.Look(ctx, start)
	settle(t, check)
	check.Look(ctx, start.Add(5*time.Minute))
	settle(t, check)
	if checks, _ := m.counts(); checks != 2 {
		t.Fatalf("the forge was checked %d times while the first check hosted a run, want the second on time", checks)
	}
	close(hold)
	check.Wait(ctx)
	if _, found := check.Last(); !found {
		t.Fatal("no check was recorded once the hosting ones ended")
	}
}

const certificateText = `Get "https://api.github.com/graphql": tls: failed to verify certificate: x509: OSStatus -26276`

func TestCertificateStoreFailureMatchesOnlyTheUnreadableStore(t *testing.T) {
	t.Parallel()
	if line, ok := forgecheck.CertificateStoreFailure("noise\n  " + certificateText + "  \nmore"); !ok || line != certificateText {
		t.Fatalf("CertificateStoreFailure = %q, %t", line, ok)
	}
	for _, text := range []string{
		"dial tcp: lookup api.github.com: no such host",
		"tls: failed to verify certificate: x509: certificate signed by unknown authority",
	} {
		if line, ok := forgecheck.CertificateStoreFailure(text); ok {
			t.Fatalf("CertificateStoreFailure(%q) = %q, true; want false", text, line)
		}
	}
}

type certificateRecord struct {
	failed, cleared int
	pid             int
}

func (r *certificateRecord) Failed(pid int, _ time.Time, _ string) error {
	r.failed++
	r.pid = pid
	return nil
}

func (r *certificateRecord) Cleared() error { r.cleared++; return nil }

// Checks failing on the certificate store are recorded each time and said once
// at the onset; a clean check clears the record and says so once.
func TestChecksFailingOnTheCertificateStoreAreRecordedAndSaidOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := &machine{failCheck: true, failText: certificateText}
	record := &certificateRecord{}
	var logged []string
	check := newCheck(m)
	check.Certificates, check.SupervisorPID = record, 4242
	check.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	start := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	count := func(part string) int {
		n := 0
		for _, line := range logged {
			if strings.Contains(line, part) {
				n++
			}
		}
		return n
	}

	check.Look(ctx, start)
	check.Wait(ctx)
	check.Look(ctx, start.Add(5*time.Minute))
	check.Wait(ctx)
	if last, _ := check.Last(); !last.CertificateStore || !strings.Contains(last.Detail, "x509: OSStatus -26276") {
		t.Fatalf("last = %+v, want it marked as failing on the certificate store", last)
	}
	if record.failed != 2 || record.pid != 4242 || record.cleared != 0 {
		t.Fatalf("record = %+v, want two failures by pid 4242", record)
	}
	if n := count("failing on the system certificate store"); n != 1 {
		t.Fatalf("onset said %d times, want once: %q", n, logged)
	}

	m.mu.Lock()
	m.failCheck = false
	m.mu.Unlock()
	check.Look(ctx, start.Add(10*time.Minute))
	check.Wait(ctx)
	if record.cleared != 1 {
		t.Fatalf("cleared %d times, want once", record.cleared)
	}
	if n := count("is cleared"); n != 1 {
		t.Fatalf("cleared line said %d times, want once: %q", n, logged)
	}
}
