// Package maincheck is the supervisor's run of the project's main_checks on the
// head of the target branch, every execution.main_check_interval, whenever the
// branch has moved since the last run.
//
// It exists for a check a project has taken off each change and runs on what
// has landed instead. In this repository that is the race check: race point 4
// of guarantee 9 in docs/designs/one-owner-for-each-guarantee.md, and the
// precondition the merge queue's declared check list waits on in
// docs/designs/merge-queue-pace.md, decision 1. The harness knows nothing about
// what the commands are; it runs the ones the project lists.
//
// A run reads where the branch stands on the remote, fetches it, and checks it
// out detached in a worktree of its own under the product's state directory,
// never in the operator's checkout and never moving a branch. Every check runs
// whole there through the shared checks runner, heavy-check permits included,
// and the worktree is removed afterwards however the run ended. The run is
// recorded with the commit, the time and how each check ended
// (runstate.MainCheckStore). A failure is never run again to make it pass: it
// files a critical report naming the commit, the failing tests, the last
// commit that passed and the commits between, and the next run waits for the
// branch to move.
//
// The schedule is kept in that record rather than in memory, so the supervisor
// restarting into a new build neither starts a run early nor forgets one is
// due: the next run is due an interval after the latest one started. A run
// under way holds the supervisor's move onto a new build back until it ends
// (Underway), as a release cut does, because a whole race suite takes longer
// than the gap between two landings.
package maincheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Name is how the run is named in what the supervisor says.
const Name = "checks on main"

// gitTimeout bounds each local Git question, and fetchTimeout each one that
// reaches the remote.
const (
	gitTimeout   = time.Minute
	fetchTimeout = 5 * time.Minute
)

// maxCommitsBetween bounds how many commits a report lists between the last
// commit that passed and the one that failed.
const maxCommitsBetween = 20

// CheckRunner runs one list of checks. It is satisfied by checks.Runner.
type CheckRunner interface {
	Run(ctx context.Context, request checks.Request, sink func(execution.Event) error) ([]checks.Result, uint64, error)
}

// Runs is the record of runs. It is satisfied by *runstate.MainCheckStore.
type Runs interface {
	List() ([]runstate.MainCheckRun, error)
	Append(runstate.MainCheckRun) error
}

// Reports is where a failure's report is filed. It is satisfied by
// *runstate.ReportStore.
type Reports interface {
	Append(report.Report) error
}

// Check is the run on main as a resident of the supervisor.
type Check struct {
	// Every is the interval, from execution.main_check_interval, and Commands
	// the checks, from main_checks.
	Every    time.Duration
	Commands []string
	// Repository is the primary checkout, which the worktree is made from and
	// Git is asked in. Remote is the remote the branch is read from. Branch is
	// the target branch; empty reads the branch the primary checkout is on at
	// each run, which is the branch runs land on.
	Repository string
	Remote     string
	Branch     string
	// WorkRoot is the directory under the product's state the run's worktree
	// is made in.
	WorkRoot string
	// Git runs Git, and Checks runs the checks.
	Git    execution.ProcessRunner
	Checks CheckRunner
	// CheckTimeout is the budget each check gets; there is no bound on the
	// list, as for landing checks.
	CheckTimeout time.Duration
	Runs         Runs
	Reports      Reports
	Product      domain.ProductID
	RepositoryID string
	Build        string
	Now          func() time.Time
	Log          func(format string, args ...any)

	nextDue time.Time
	running *underway
}

type underway struct {
	started time.Time
	done    chan Outcome
}

// Outcome is what one look's run came to.
type Outcome struct {
	// Run is the run recorded, and Recorded whether there was one: a branch
	// that had not moved records nothing.
	Run      runstate.MainCheckRun
	Recorded bool
	// Said is what the supervisor's log says about it.
	Said string
}

// Name is how the run is named in what the supervisor says.
func (c *Check) Name() string { return Name }

// Describe is the run's line: its interval, and what the latest run came to.
func (c *Check) Describe() string {
	said := fmt.Sprintf("every %s, whenever the branch has moved: %s", c.every(), strings.Join(c.Commands, "; "))
	if c.running != nil {
		said += fmt.Sprintf("; a run has been under way since %s", runstate.LocalStamp(c.running.started))
	}
	return said
}

// Underway is what holds the supervisor's move onto a new build back while a
// run is under way, in words that finish "it moves onto it", and empty when no
// run is.
func (c *Check) Underway() string {
	c.collect()
	if c.running == nil {
		return ""
	}
	return fmt.Sprintf("once the run of the checks on main under way since %s has finished", runstate.LocalStamp(c.running.started))
}

// Look starts a run when one is due and none is under way. It never returns an
// error: a run that could not be made is recorded and said, and the next one
// is due at the ordinary interval.
func (c *Check) Look(ctx context.Context, now time.Time) {
	c.collect()
	if c.running != nil {
		return
	}
	if c.nextDue.IsZero() {
		c.nextDue = c.dueFromRecord()
	}
	if !c.nextDue.IsZero() && now.Before(c.nextDue) {
		return
	}
	c.nextDue = now.Add(c.every())
	if len(c.Commands) == 0 || c.Git == nil || c.Checks == nil || c.Runs == nil {
		c.log("the checks on main have no commands, Git, check runner or record wired, so nothing is run")
		return
	}
	started := &underway{started: now, done: make(chan Outcome, 1)}
	c.running = started
	go func() { started.done <- c.run(ctx, now) }()
}

// Wait blocks until a run under way has ended, and collects it. It is for a
// test, and for nothing that holds the supervisor's looks.
func (c *Check) Wait(ctx context.Context) (Outcome, bool) {
	if c.running == nil {
		return Outcome{}, false
	}
	select {
	case outcome := <-c.running.done:
		c.running = nil
		c.finish(outcome)
		return outcome, true
	case <-ctx.Done():
		return Outcome{}, false
	}
}

// NextDue is when the next run is due, zero before the first look.
func (c *Check) NextDue() time.Time { return c.nextDue }

func (c *Check) collect() {
	if c.running == nil {
		return
	}
	select {
	case outcome := <-c.running.done:
		c.running = nil
		c.finish(outcome)
	default:
	}
}

func (c *Check) finish(outcome Outcome) {
	if outcome.Said != "" {
		c.log("%s", outcome.Said)
	}
}

// dueFromRecord is an interval after the latest recorded run started, and zero
// where none is recorded or the record cannot be read.
func (c *Check) dueFromRecord() time.Time {
	runs, err := c.Runs.List()
	if err != nil {
		c.log("the record of checks on main could not be read, so a run is due now: %v", err)
		return time.Time{}
	}
	latest := runstate.ReadMainCheckStanding(runs).Latest
	if latest == nil {
		return time.Time{}
	}
	return latest.StartedAt.Add(c.every())
}

// run is one run, off the supervisor's goroutine. It touches nothing of the
// resident's but its own channel.
func (c *Check) run(ctx context.Context, startedAt time.Time) Outcome {
	branch, err := c.branch(ctx)
	if err != nil {
		return c.record(ctx, runstate.MainCheckRun{Branch: "unknown", StartedAt: startedAt, Outcome: runstate.MainCheckCouldNotRun,
			Detail: fmt.Sprintf("which branch runs land on could not be read: %v", err)})
	}
	commit, readFrom, err := c.head(ctx, branch)
	if err != nil {
		return c.record(ctx, runstate.MainCheckRun{Branch: branch, StartedAt: startedAt, Outcome: runstate.MainCheckCouldNotRun,
			Detail: fmt.Sprintf("the head of %s could not be read: %v", branch, err)})
	}
	runs, err := c.Runs.List()
	if err != nil {
		return c.record(ctx, runstate.MainCheckRun{Branch: branch, Commit: commit, StartedAt: startedAt, Outcome: runstate.MainCheckCouldNotRun,
			Detail: fmt.Sprintf("the record of earlier runs could not be read, so whether %s has moved is not known: %v", branch, err)})
	}
	standing := runstate.ReadMainCheckStanding(runs)
	if last := standing.LatestResult; last != nil && last.Commit == commit && last.Branch == branch {
		return Outcome{Said: fmt.Sprintf("the checks on main did not run: %s is still at %s, which they %s on at %s", branch, short(commit), last.Outcome.Words(), runstate.LocalStamp(last.StartedAt))}
	}
	run := runstate.MainCheckRun{Branch: branch, Commit: commit, StartedAt: startedAt, Detail: readFrom}
	results, err := c.checkOut(ctx, commit)
	if err != nil {
		run.Outcome = runstate.MainCheckCouldNotRun
		run.Detail = joinDetail(run.Detail, err.Error())
		return c.record(ctx, run)
	}
	run.Checks, run.Outcome = summarize(results)
	if run.Outcome == runstate.MainCheckFailed {
		id, err := c.report(ctx, run, standing)
		if err != nil {
			run.Detail = joinDetail(run.Detail, fmt.Sprintf("the report of this failure could not be filed: %v", err))
		}
		run.ReportID = id
	}
	return c.record(ctx, run)
}

// record writes the run down and says what it came to.
func (c *Check) record(ctx context.Context, run runstate.MainCheckRun) Outcome {
	run.SchemaVersion, run.ProductID = runstate.MainCheckRunSchemaVersion, c.Product
	run.StartedAt, run.EndedAt = run.StartedAt.UTC(), c.now().UTC()
	run.Detail = runstate.BoundMainCheckText(run.Detail)
	said := fmt.Sprintf("the checks on main %s", run.Outcome.Words())
	if run.Commit != "" {
		said += fmt.Sprintf(" on %s at %s", run.Branch, short(run.Commit))
	}
	if run.Detail != "" {
		said += ": " + run.Detail
	}
	if err := c.Runs.Append(run); err != nil {
		return Outcome{Run: run, Said: said + fmt.Sprintf("; the run could not be recorded: %v", err)}
	}
	return Outcome{Run: run, Recorded: true, Said: said}
}

// summarize is each check's ending, and the run's: failed where any check
// failed, could not run where none failed and one could not run, and passed
// only where every check passed.
func summarize(results []checks.Result) ([]runstate.MainCheckResult, runstate.MainCheckOutcome) {
	outcome := runstate.MainCheckPassed
	if len(results) == 0 {
		outcome = runstate.MainCheckCouldNotRun
	}
	ended := make([]runstate.MainCheckResult, 0, len(results))
	for _, result := range results {
		check := runstate.MainCheckResult{Command: result.Command, Took: result.Elapsed()}
		switch {
		case result.Passed:
			check.Outcome = runstate.MainCheckPassed
		case result.CouldNotRun != "" || result.NotLaunched:
			check.Outcome = runstate.MainCheckCouldNotRun
			check.Detail = runstate.BoundMainCheckText(firstOf(result.CouldNotRun, "it was stopped before it started"))
			if outcome == runstate.MainCheckPassed {
				outcome = runstate.MainCheckCouldNotRun
			}
		default:
			check.Outcome = runstate.MainCheckFailed
			check.FailingTests = runstate.BoundMainCheckFailingTests(checks.FailingNames(result))
			if len(check.FailingTests) == 0 {
				check.Detail = runstate.BoundMainCheckText(fmt.Sprintf("it ended %s with exit code %d and named no failing test", result.Process.Status, result.Process.ExitCode))
			}
			outcome = runstate.MainCheckFailed
		}
		if check.Took < 0 {
			check.Took = 0
		}
		ended = append(ended, check)
	}
	return ended, outcome
}

// report files the critical report a failed run owes.
func (c *Check) report(ctx context.Context, run runstate.MainCheckRun, standing runstate.MainCheckStanding) (string, error) {
	if c.Reports == nil {
		return "", errors.New("no report pile is wired")
	}
	message := c.failureMessage(ctx, run, standing)
	id, err := report.NewID()
	if err != nil {
		return "", err
	}
	return id, c.Reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion, ID: id, Role: report.HarnessReporter,
		RunID: "main-check:" + run.Commit, Build: c.Build,
		ProductID: c.Product, RepositoryID: c.RepositoryID, Severity: report.SeverityCritical,
		Message: oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)), RecordedAt: c.now().UTC(),
	})
}

// failureMessage says which checks failed on which commit, naming the failing
// tests, the last commit that passed and the commits between.
func (c *Check) failureMessage(ctx context.Context, run runstate.MainCheckRun, standing runstate.MainCheckStanding) string {
	var failed []string
	for _, check := range run.Checks {
		if check.Outcome != runstate.MainCheckFailed {
			continue
		}
		named := "no failing test was named in its output"
		if len(check.FailingTests) > 0 {
			named = "failing: " + strings.Join(check.FailingTests, ", ")
		}
		failed = append(failed, fmt.Sprintf("%q (%s)", check.Command, named))
	}
	message := fmt.Sprintf("The checks run on %s failed on commit %s, in the run started at %s: %s.",
		run.Branch, run.Commit, runstate.LocalStamp(run.StartedAt), strings.Join(failed, "; "))
	if subject := c.subject(ctx, run.Commit); subject != "" {
		message += fmt.Sprintf(" That commit is %q.", subject)
	}
	clean := standing.LatestPassed
	if clean == nil || clean.Branch != run.Branch {
		message += " No earlier run on this branch passed, so there is no last clean commit to compare with."
	} else {
		message += fmt.Sprintf(" The last commit they passed on is %s, at %s.", clean.Commit, runstate.LocalStamp(clean.StartedAt))
		if between := c.between(ctx, clean.Commit, run.Commit); between != "" {
			message += " " + between
		}
	}
	if previous := standing.LatestResult; previous != nil && previous.Outcome == runstate.MainCheckFailed {
		message += fmt.Sprintf(" The run before this one, on %s, failed too.", short(previous.Commit))
	}
	message += " The failure was not run again. It blocks reliable execution, so it is reliability work at priority 0, and more work landing on this branch lands on it."
	return message
}

// between lists the commits after clean up to and including failed.
func (c *Check) between(ctx context.Context, clean, failed string) string {
	count, err := c.git(ctx, gitTimeout, "rev-list", "--count", clean+".."+failed)
	if err != nil {
		return ""
	}
	listed, err := c.git(ctx, gitTimeout, "log", "--format=%h %s", "-n", strconv.Itoa(maxCommitsBetween), clean+".."+failed)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(listed), "\n")
	said := fmt.Sprintf("%s commit(s) landed between them: %s", strings.TrimSpace(count), strings.Join(lines, "; "))
	if n, err := strconv.Atoi(strings.TrimSpace(count)); err == nil && n > len(lines) {
		said += fmt.Sprintf("; and %d earlier", n-len(lines))
	}
	return said + "."
}

func (c *Check) subject(ctx context.Context, commit string) string {
	out, err := c.git(ctx, gitTimeout, "log", "-1", "--format=%s", commit)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// branch is the configured target branch, or the one the primary checkout is
// on.
func (c *Check) branch(ctx context.Context) (string, error) {
	if c.Branch != "" {
		return c.Branch, nil
	}
	out, err := c.git(ctx, gitTimeout, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", errors.New("the primary checkout is on no branch")
	}
	return branch, nil
}

// head is the commit the remote has the branch at, fetched so it is here to
// check out. Where the remote cannot be read the local branch is used, and the
// second answer says so.
func (c *Check) head(ctx context.Context, branch string) (string, string, error) {
	ref := "refs/heads/" + branch
	remote := c.remote()
	out, remoteErr := c.gitWith(ctx, fetchTimeout, execution.ForgeEnvironment(nil), "ls-remote", remote, ref)
	if remoteErr == nil {
		object, _, found := strings.Cut(strings.TrimSpace(out), "\t")
		switch {
		case !found || object == "":
			remoteErr = fmt.Errorf("%s has no branch %s", remote, branch)
		default:
			if _, err := c.gitWith(ctx, fetchTimeout, execution.ForgeEnvironment(nil), "fetch", "--quiet", remote, ref); err != nil {
				remoteErr = err
			} else if _, err := c.git(ctx, gitTimeout, "cat-file", "-e", object+"^{commit}"); err != nil {
				remoteErr = fmt.Errorf("%s has %s at %s, which the fetch did not bring here", remote, branch, object)
			} else {
				return object, "", nil
			}
		}
	}
	local, err := c.git(ctx, gitTimeout, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", "", errors.Join(remoteErr, err)
	}
	return strings.TrimSpace(local), fmt.Sprintf("%s could not be read (%v), so the local %s was checked", remote, remoteErr, branch), nil
}

// checkOut runs the checks in a worktree of the commit's own, removed
// afterwards however they ended.
func (c *Check) checkOut(ctx context.Context, commit string) ([]checks.Result, error) {
	if err := os.MkdirAll(c.WorkRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", c.WorkRoot, err)
	}
	path := filepath.Join(c.WorkRoot, "main")
	// A worktree an interrupted run left is removed before this one is made; it
	// holds nothing but a checkout of a commit and what the checks built.
	_, _ = c.git(ctx, gitTimeout, "worktree", "remove", "--force", path)
	_, _ = c.git(ctx, gitTimeout, "worktree", "prune")
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s is in the way and is not a worktree Git could remove; remove it and the next run checks", path)
	}
	if _, err := c.git(ctx, gitTimeout, "worktree", "add", "--detach", path, commit); err != nil {
		return nil, fmt.Errorf("check out %s: %w", short(commit), err)
	}
	defer func() {
		cleanup := context.WithoutCancel(ctx)
		_, _ = c.git(cleanup, gitTimeout, "worktree", "remove", "--force", path)
	}()
	results, _, err := c.Checks.Run(ctx, checks.Request{
		RunID:     "main-check-" + short(commit),
		Directory: path,
		Commands:  append([]string(nil), c.Commands...),
		Timeout:   c.CheckTimeout,
		Unbounded: true,
		Permit:    checks.PermitRequest{Commit: commit},
	}, nil)
	if err != nil && len(results) == 0 {
		return nil, fmt.Errorf("the checks could not be run: %w", err)
	}
	return results, nil
}

func (c *Check) git(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	return c.gitWith(ctx, timeout, execution.GitEnvironment(nil), args...)
}

func (c *Check) gitWith(ctx context.Context, timeout time.Duration, env []string, args ...string) (string, error) {
	result, err := c.Git.Run(ctx, execution.Command{Name: "git", Args: args, Dir: c.Repository, Env: env, Timeout: timeout}, nil)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("git %s ended %s with exit code %d: %s", args[0], result.Status, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return result.Stdout, nil
}

func (c *Check) remote() string {
	if c.Remote == "" {
		return "origin"
	}
	return c.Remote
}

func (c *Check) every() time.Duration {
	if c.Every <= 0 {
		return config.DefaultMainCheckInterval.Duration()
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

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func joinDetail(earlier, later string) string {
	if earlier == "" {
		return later
	}
	return earlier + "; " + later
}
