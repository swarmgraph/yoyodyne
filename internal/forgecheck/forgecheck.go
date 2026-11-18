// Package forgecheck is the supervisor's check of the forge: `yoyo
// forge-check`, run on a schedule of its own, every
// execution.forge_check_interval, apart from the maintenance pass
// (docs/designs/working-alongside-other-contributors.md, section 6).
//
// It is a resident of the supervisor beside the maintenance pass rather than a
// step of it. Maintenance does slow work that has nothing to do with the forge,
// such as clearing old checkouts and shrinking the tracker's store, so a forge
// check waiting behind it would notice a person's merge late, or not at all
// while a maintenance step kept failing. The two share nothing: a maintenance
// pass under way holds no check back, and a check holds no pass back.
//
// A check is a subprocess for the reason the maintenance pass's reconcile is:
// the verb is the whole of what a check does, and a second copy of its wiring
// here would be a second check to keep in step with the first. One check reads
// at a time. A check that goes on to host a repair round or an update it
// started stops holding the schedule once it says it has finished reading
// (cli.ForgeCheckReadLine), so the next check starts on time while that run is
// hosted; every act a check takes is under the run's own lease, so a second
// check meeting a run the first is hosting leaves it alone.
//
// A check that fails — a forge that cannot be reached, a tracker write that did
// not go through — is said in the supervisor's log and changes nothing else:
// the next check, at the ordinary interval, tries again. No run is stopped and
// no other work waits on it. The one exception is a check failing on the system
// certificate store, which every check of a supervisor started from a shell that
// cannot read it does: that is also recorded once, for `yoyo status` and the
// dashboard (runstate.ForgeCertificateStore).
package forgecheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Name is how the check is named in what the supervisor says.
const Name = "forge check"

// StartedBy is what a `yoyo forge-check` this resident starts is marked as
// started by.
const StartedBy = "the supervisor's forge check"

// CheckTimeout bounds one `yoyo forge-check`, the runs it hosts included, as
// maintain.ReconcileTimeout bounds the sweep: a run it hosts that outlasts it is
// stopped and kept, and the next check that finds nothing hosting it hosts it
// again.
const CheckTimeout = 15 * time.Minute

// CertificateRecord is where a check failing on the system certificate store is
// recorded, and cleared. runstate.ForgeCertificateStore is one.
type CertificateRecord interface {
	Failed(pid int, at time.Time, detail string) error
	Cleared() error
}

// Check is the forge check as a resident of the supervisor.
type Check struct {
	// Every is the interval, from execution.forge_check_interval.
	Every time.Duration
	// Program is the binary the supervisor runs, which `forge-check` is run
	// from, Config the configuration file it reads, Repository where it runs,
	// and Environ what it is given.
	Program    string
	Config     string
	Repository string
	Environ    []string
	// ReadLine is the line the verb writes once it has finished reading
	// (cli.ForgeCheckReadLine). Empty holds the schedule until the process ends.
	ReadLine string
	Runner   execution.ProcessRunner
	Now      func() time.Time
	Log      func(format string, args ...any)
	// Certificates records a check failing on the certificate store, with
	// SupervisorPID as the supervisor that ran it. Nil records nothing.
	Certificates  CertificateRecord
	SupervisorPID int

	// certificateSince is when the failure on the certificate store began, as
	// this resident saw it, and zero while no check is failing that way.
	certificateSince time.Time
	nextDue          time.Time
	reading          *check
	hosting          []*check
	last             *Result
	checks           int
}

// Result is what one check came to.
type Result struct {
	StartedAt time.Time
	EndedAt   time.Time
	// Failed says the check did not finish cleanly, and Detail says what it came
	// to either way, from the end of its output.
	Failed bool
	Detail string
	// CertificateStore says the check failed on the system certificate store.
	CertificateStore bool
}

type check struct {
	started time.Time
	read    chan struct{}
	done    chan Result
}

// Name is how the check is named in what the supervisor says.
func (c *Check) Name() string { return Name }

// Describe is the check's line: its interval, and what the last check came to.
func (c *Check) Describe() string {
	said := fmt.Sprintf("every %s", c.every())
	if c.last != nil {
		outcome := "finished"
		if c.last.Failed {
			outcome = "did not finish cleanly"
		}
		said += fmt.Sprintf("; the last check, at %s, %s", c.last.StartedAt.UTC().Format(time.RFC3339), outcome)
	}
	if c.reading != nil {
		said += fmt.Sprintf("; a check has been reading since %s", c.reading.started.UTC().Format(time.RFC3339))
	}
	if len(c.hosting) > 0 {
		said += fmt.Sprintf("; %d earlier check(s) still hosting a run they started", len(c.hosting))
	}
	return said
}

// Last is what the most recent check that ended came to.
func (c *Check) Last() (Result, bool) {
	if c.last == nil {
		return Result{}, false
	}
	return *c.last, true
}

// Checks is how many checks this resident has started.
func (c *Check) Checks() int { return c.checks }

// Look is the check as a resident of the supervisor. On every tick it collects
// the checks that have ended, lets one that has finished reading stop holding
// the schedule, and starts one when the interval has come round and none is
// reading. It never returns an error: a check that failed is said, and the
// next check reads everything this one would have.
func (c *Check) Look(ctx context.Context, now time.Time) {
	c.collect()
	if c.reading != nil {
		return
	}
	if !c.nextDue.IsZero() && now.Before(c.nextDue) {
		return
	}
	if c.Runner == nil {
		c.nextDue = now.Add(c.every())
		c.log("the forge check has no process runner, so nothing is checked")
		return
	}
	c.nextDue = now.Add(c.every())
	c.checks++
	started := &check{started: now, read: make(chan struct{}), done: make(chan Result, 1)}
	c.reading = started
	go func() { started.done <- c.run(ctx, started) }()
}

// Wait blocks until every check under way has ended, and collects them. It is
// for a test, and for nothing that holds the supervisor's looks.
func (c *Check) Wait(ctx context.Context) {
	for _, under := range append(c.hosting, c.reading) {
		if under == nil {
			continue
		}
		select {
		case result := <-under.done:
			under.done <- result
		case <-ctx.Done():
			return
		}
	}
	c.collect()
}

// Reading reports a check that has not yet finished reading.
func (c *Check) Reading() bool {
	c.collect()
	return c.reading != nil
}

// collect moves a check that has finished reading out of the way of the
// schedule, and ends the record of the ones whose process has ended.
func (c *Check) collect() {
	if c.reading != nil {
		select {
		case result := <-c.reading.done:
			c.finish(result)
			c.reading = nil
		case <-c.reading.read:
			c.hosting = append(c.hosting, c.reading)
			c.reading = nil
		default:
		}
	}
	still := c.hosting[:0]
	for _, under := range c.hosting {
		select {
		case result := <-under.done:
			c.finish(result)
		default:
			still = append(still, under)
		}
	}
	c.hosting = still
}

func (c *Check) finish(result Result) {
	c.last = &result
	if result.Failed {
		c.log("forge check started at %s did not finish cleanly, and the next check tries again: %s", result.StartedAt.UTC().Format(time.RFC3339), result.Detail)
		if result.CertificateStore {
			if c.certificateSince.IsZero() {
				c.certificateSince = result.EndedAt
				c.log("every forge check is now failing on the system certificate store, so the supervisor cannot reach GitHub from the shell it was started in: %s", runstate.ForgeCertificateRemedy)
			}
			if c.Certificates != nil {
				if err := c.Certificates.Failed(c.SupervisorPID, result.EndedAt, result.Detail); err != nil {
					c.log("the forge check could not record its failure on the certificate store: %v", err)
				}
			}
		}
		return
	}
	if c.Certificates != nil {
		if err := c.Certificates.Cleared(); err != nil {
			c.log("the forge check could not clear its record of a failure on the certificate store: %v", err)
		}
	}
	if !c.certificateSince.IsZero() {
		c.certificateSince = time.Time{}
		c.log("forge checks finish cleanly again, and the failure on the system certificate store is cleared")
	}
	c.log("forge check started at %s: %s", result.StartedAt.UTC().Format(time.RFC3339), result.Detail)
}

// run is one `yoyo forge-check`, off the supervisor's goroutine. It touches
// nothing of the resident's but the check's own channels.
func (c *Check) run(ctx context.Context, started *check) Result {
	var signalled bool
	observer := func(output execution.Output) {
		if signalled || c.ReadLine == "" {
			return
		}
		if strings.Contains(output.Text, c.ReadLine) {
			signalled = true
			close(started.read)
		}
	}
	result, err := c.Runner.Run(ctx, execution.Command{
		Name:    c.Program,
		Args:    []string{"forge-check", "--config", c.Config},
		Dir:     c.Repository,
		Env:     execution.WithStartedBy(c.Environ, StartedBy),
		Timeout: CheckTimeout,
	}, observer)
	ended := Result{StartedAt: started.started, EndedAt: c.now()}
	switch {
	case err != nil:
		ended.Failed, ended.Detail = true, fmt.Sprintf("yoyo forge-check could not be run: %v", err)
	case result.Status != execution.ProcessSucceeded:
		ended.Failed, ended.Detail = true, "yoyo forge-check "+describe(result)
	default:
		ended.Detail = "yoyo forge-check " + describe(result)
	}
	if ended.Failed {
		texts := []string{result.Stdout, result.Stderr}
		if err != nil {
			texts = append([]string{err.Error()}, texts...)
		}
		for _, text := range texts {
			if line, ok := CertificateStoreFailure(text); ok {
				ended.CertificateStore = true
				if !strings.Contains(ended.Detail, line) {
					ended.Detail += "; " + line
				}
				break
			}
		}
	}
	return ended
}

// CertificateStoreFailure reports text that says the system certificate store
// could not be read, and the line that says so. Only Go's `x509: OSStatus` is
// matched: it is what a process that cannot read the macOS store reports, and
// an ordinary network failure, or an unknown authority behind a proxy, is a
// different problem that starting the supervisor elsewhere would not mend and
// that must not be read as this one.
func CertificateStoreFailure(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "x509: OSStatus") {
			line = strings.TrimSpace(line)
			if len(line) > 512 {
				line = line[:512]
			}
			return line, true
		}
	}
	return "", false
}

// describe is how a check's process ended, with the last line it said.
func describe(result execution.ProcessResult) string {
	said := "finished"
	if result.Status != execution.ProcessSucceeded {
		said = fmt.Sprintf("ended %s with exit code %d", result.Status, result.ExitCode)
	}
	if line := lastLine(result.Stdout); line != "" {
		said += ": " + line
	}
	if line := lastLine(result.Stderr); line != "" && result.Status != execution.ProcessSucceeded {
		said += "; " + line
	}
	return said
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 512 {
		line = line[:512]
	}
	return line
}

func (c *Check) every() time.Duration {
	if c.Every <= 0 {
		return config.DefaultForgeCheckInterval.Duration()
	}
	return c.Every
}

func (c *Check) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Check) log(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}
