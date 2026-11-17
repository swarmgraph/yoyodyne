package orchestrator

// Checks on CI, build 3 (docs/designs/checks-on-ci.md): with
// execution.ci_checks.enabled the harness runs only the short local check
// before review, pushes the candidate commit, records the wait on the run and
// gives its developer slot back. The wait is a recorded state of the run
// (runstate.CIWait): a later process reads the forge for that commit and
// carries on, and never pushes a different commit or starts the wait again.
//
// Review starts only once every required check has passed on exactly that
// commit. Where the wait ends any other way — a required check failed, was
// cancelled or skipped, never started within the wait limit, the forge cannot
// be read, or the switch was turned off — the run runs the full declared checks
// on this machine over the same commit and goes on from their result. That is
// what the design gives for CI being down or slow and for switching off. A
// check that failed on the forge is not yet handed to the developer with its
// output, which is a later build; until then the local full checks judge it.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CIForge is what the wait asks of the forge: push a run's branch, open or
// refresh its draft pull request, and read the required checks on a commit.
// publish.GitHub does all three.
type CIForge interface {
	PushBranch(ctx context.Context, request publish.PushRequest) error
	EnsureDraft(ctx context.Context, request publish.Request) (publish.PullRequest, error)
	RequiredChecks(ctx context.Context, commit string, required []string) (publish.RequiredCheckReading, error)
}

var fullCommit = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// ciFlow reports whether this run's checks follow the checks-on-CI flow: the
// switch is on, a forge is wired, the run is a developer's change before
// review, and it is not the run's own full local pass over a commit the forge
// could not settle.
func (a *activeRun) ciFlow() bool {
	ci := a.pipeline.Config.Execution.CIChecks
	switch {
	case !ci.Enabled || a.pipeline.CIForge == nil:
		return false
	case a.checkingCandidate || a.state.HarnessAuthored() || a.state.Document != nil || a.state.OutsideFile != nil:
		return false
	case a.fullLocalCommit != "" && a.fullLocalCommit == a.state.HarnessCommit:
		return false
	}
	return true
}

// shortLocalChecks is the configured checks named in execution.ci_checks.local,
// in the order checks lists them. Each is matched as an exact command, as the
// setting's validation requires.
func shortLocalChecks(configured, local []string) []string {
	var short []string
	for _, command := range configured {
		for _, name := range local {
			if strings.TrimSpace(command) == strings.TrimSpace(name) {
				short = append(short, command)
				break
			}
		}
	}
	return short
}

// checkRound is one round's checks before review. Ordinarily it is verify, and
// with the flow on it is verify over the short check followed by the push and
// the recorded wait. A run resumed with a wait recorded settles the wait
// instead of checking again.
func (a *activeRun) checkRound(ctx context.Context) error {
	if a.state.CIWait != nil {
		return a.settleCIWait(ctx)
	}
	if err := a.verify(ctx); err != nil {
		return err
	}
	if !a.ciFlow() {
		return nil
	}
	return a.pushAndWait(ctx)
}

// pushAndWait pushes the candidate commit, records the wait with the commit,
// the push time and the required checks, and parks the run, which gives its
// developer slot back. A push the forge refuses holds the run for the
// development manager with the forge's refusal, because integration would need
// the same forge.
func (a *activeRun) pushAndWait(ctx context.Context) error {
	p := a.pipeline
	ci := p.Config.Execution.CIChecks
	commit := strings.ToLower(strings.TrimSpace(a.state.HarnessCommit))
	if commit == "" {
		// An attempt that changed nothing has no commit to wait on; the reviewer
		// judges an empty change as it always has.
		return nil
	}
	if !fullCommit.MatchString(commit) {
		return stoppedBy(runstate.StopPublish, fmt.Errorf("checks on CI needs the candidate commit named in full, and the run's is %q", commit))
	}
	branch := a.worktree.Branch
	previous := ""
	if pr := a.state.PullRequest; pr != nil && pr.Branch == branch {
		previous = pr.HeadCommit
	}
	if previous != commit {
		if err := p.CIForge.PushBranch(ctx, publish.PushRequest{Branch: branch, Commit: commit, Previous: previous}); err != nil {
			return stoppedBy(runstate.StopPublish, fmt.Errorf("push the candidate commit for the forge's checks: %w", err))
		}
	}
	pushedAt := p.clock().Now()
	if pr := a.state.PullRequest; pr == nil || pr.HeadCommit != commit {
		opened, err := p.CIForge.EnsureDraft(ctx, publish.Request{
			Head:  branch,
			Base:  a.worktree.TargetBranch,
			Title: pullRequestTitle(a.item, a.outcome),
			Body:  pullRequestBody(a.item, a.outcome, a.worktree.TargetBranch, p.automatic(), p.factoryLine(ctx)),
			Mark:  forgeMark(ctx, p.Factory, p.Config.Product.ID),
		})
		if err != nil {
			return stoppedBy(runstate.StopPublish, fmt.Errorf("open the draft pull request for the forge's checks: %w", err))
		}
		remote := strings.TrimSpace(p.Config.Execution.PushRemote)
		if remote == "" {
			remote = p.Config.Execution.Remote
		}
		published := &runstate.PullRequest{
			Remote: remote, Branch: branch, Number: opened.Number, URL: opened.URL, HeadCommit: commit,
			State: opened.State, Merged: opened.Merged, PushedAt: &pushedAt,
		}
		if earlier := a.state.PullRequest; earlier != nil {
			published.Feedback = earlier.Feedback
		}
		a.state.PullRequest = published
		a.outcome.PullRequest = published
	}
	wait := runstate.CIWait{
		Commit:           commit,
		Branch:           branch,
		PushedAt:         pushedAt,
		Required:         append([]string(nil), ci.Required...),
		WaitLimitSeconds: int64(ci.WaitLimitInForce() / time.Second),
	}
	a.state.CIWait = &wait
	a.state.UpdatedAt = pushedAt
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the wait for the forge's checks: %w", err)
	}
	return ciWaitPause{wait: wait}
}

// settleCIWait is the first thing a run does once it has a slot again with a
// wait recorded. It reads the forge for exactly the commit the wait names and
// never pushes. Every required check passed: the wait ends and review goes on.
// Still running within the limit: the run parks again. Anything else, or the
// switch turned off, ends the wait and runs the full declared checks over the
// same commit.
func (a *activeRun) settleCIWait(ctx context.Context) error {
	p := a.pipeline
	wait := *a.state.CIWait
	now := p.clock().Now()
	if wait.Commit != strings.ToLower(strings.TrimSpace(a.state.HarnessCommit)) {
		return a.endCIWaitWithFullChecks(ctx, wait, "the run's change moved on from the commit it waited for, so the forge's answer does not apply to it")
	}
	if !p.Config.Execution.CIChecks.Enabled || p.CIForge == nil {
		return a.endCIWaitWithFullChecks(ctx, wait, "checks on CI was switched off during the wait")
	}
	reading, err := p.CIForge.RequiredChecks(ctx, wait.Commit, wait.Required)
	if err != nil {
		if wait.Overdue(now) {
			return a.endCIWaitWithFullChecks(ctx, wait, "the forge could not be read after the wait limit: "+err.Error())
		}
		return a.parkAgain(wait, "the forge could not be read: "+err.Error())
	}
	if reading.Commit != wait.Commit {
		return a.endCIWaitWithFullChecks(ctx, wait, "the forge answered for another commit")
	}
	if reading.AllPassed() {
		a.state.CIWait = nil
		a.state.CIWaitEnd = &runstate.CIWaitEnd{Commit: wait.Commit, At: now, Passed: true,
			Says: "every required check passed on the commit pushed: " + strings.Join(wait.Required, ", ")}
		a.state.UpdatedAt = now
		if err := p.Store.Save(a.state); err != nil {
			return fmt.Errorf("record that the forge's checks passed: %w", err)
		}
		return nil
	}
	var unsettled []string
	for _, check := range reading.Checks {
		switch check.Result {
		case publish.CheckPassed:
		case publish.CheckPending, publish.CheckNotStarted:
			if !wait.Overdue(now) {
				unsettled = append(unsettled, check.Name)
				continue
			}
			return a.endCIWaitWithFullChecks(ctx, wait, fmt.Sprintf("%s had not finished within the wait limit", check.Name))
		default:
			return a.endCIWaitWithFullChecks(ctx, wait, fmt.Sprintf("%s ended %s on the forge", check.Name, check.Result))
		}
	}
	return a.parkAgain(wait, "still waiting for "+strings.Join(unsettled, ", "))
}

// parkAgain returns a run that was continued before the forge had decided to
// its wait, without a slot and without pushing anything.
func (a *activeRun) parkAgain(wait runstate.CIWait, _ string) error {
	wait.SlotTaken = false
	a.state.CIWait = &wait
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the wait for the forge's checks: %w", err)
	}
	return ciWaitPause{wait: wait}
}

// endCIWaitWithFullChecks ends the wait, records why, and runs the full
// declared checks over the same commit. The result stands for this commit only:
// a repaired commit starts the flow again at the short check.
func (a *activeRun) endCIWaitWithFullChecks(ctx context.Context, wait runstate.CIWait, why string) error {
	a.state.CIWait = nil
	a.state.CIWaitEnd = &runstate.CIWaitEnd{Commit: wait.Commit, At: a.pipeline.clock().Now(), Says: why + "; the full checks run on this machine over the same commit"}
	a.fullLocalCommit = wait.Commit
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record how the wait for the forge's checks ended: %w", err)
	}
	return a.verify(ctx)
}

// ciWaitPause reports a run that pushed its candidate and waits for the forge's
// checks. Like the other pauses it is an error only so that it travels the path
// a stopped step already travels; the run is still in flight, still claimed,
// and holds no developer slot.
type ciWaitPause struct {
	wait runstate.CIWait
}

func (e ciWaitPause) Error() string {
	return "waiting for " + e.wait.Summary()
}

// pausedForCIWait reports a run with a wait for the forge's checks recorded,
// whether or not it has taken a slot back to read the forge.
func pausedForCIWait(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.CIWait == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// pauseForCIWait is the outcome of a run that parked on the forge. Nothing is
// cleaned up and nothing is made terminal.
func (a *activeRun) pauseForCIWait(paused ciWaitPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	wait := paused.wait
	a.outcome.PausedByCIWait = &wait
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	return a.outcome, nil
}

// CIWaitReader is what a pull asks of the forge about a run waiting for its
// checks. It is the wait's own read, made without a developer slot.
type CIWaitReader interface {
	// Decided reports whether the forge has said enough for the run to be
	// continued, and says why in a sentence.
	Decided(ctx context.Context, state runstate.State) (bool, string, error)
}

// ciWaitDecided is the rule a pull applies to one reading of a waiting run.
func ciWaitDecided(wait runstate.CIWait, reading publish.RequiredCheckReading, now time.Time) (bool, string) {
	if reading.AllPassed() {
		return true, "every required check passed on the commit pushed"
	}
	for _, check := range reading.Checks {
		switch check.Result {
		case publish.CheckPassed:
		case publish.CheckPending, publish.CheckNotStarted:
			if wait.Overdue(now) {
				return true, check.Name + " had not finished within the wait limit"
			}
		default:
			return true, fmt.Sprintf("%s ended %s on the forge", check.Name, check.Result)
		}
	}
	return false, "the required checks are still running"
}

// ciPollEvery is the least time between two reads of the forge for one waiting
// run, so a pull every few seconds does not ask the forge that often.
const ciPollEvery = time.Minute

// ForgeCIWaits reads the forge for the runs a pull finds waiting. Enabled says
// whether checks on CI is on now: a wait found with it off is decided at once,
// and the run then runs the full checks on this machine.
type ForgeCIWaits struct {
	Forge   CIForge
	Enabled func() bool
	Now     func() time.Time
}

// ciPolls is when each waiting run's forge was last read, and what that read
// said. It outlives any one reader because a pull builds its reader afresh, and
// the limit of one read a minute for a run is across pulls.
var ciPolls = struct {
	mu      sync.Mutex
	polled  map[string]time.Time
	lastSay map[string]string
	decided map[string]bool
}{polled: map[string]time.Time{}, lastSay: map[string]string{}, decided: map[string]bool{}}

// Decided reads the forge for the commit the run's wait names, at most once a
// minute for one run.
func (f *ForgeCIWaits) Decided(ctx context.Context, state runstate.State) (bool, string, error) {
	wait := state.CIWait
	if wait == nil {
		return false, "", errors.New("the run has no wait recorded")
	}
	now := time.Now()
	if f.Now != nil {
		now = f.Now()
	}
	if f.Enabled != nil && !f.Enabled() {
		return true, "checks on CI was switched off during the wait", nil
	}
	if f.Forge == nil {
		return wait.Overdue(now), "no forge is wired to read the checks", nil
	}
	ciPolls.mu.Lock()
	if at, ok := ciPolls.polled[state.RunID]; ok && now.Sub(at) < ciPollEvery && now.After(at) {
		say, decided := ciPolls.lastSay[state.RunID], ciPolls.decided[state.RunID]
		ciPolls.mu.Unlock()
		return decided, say, nil
	}
	ciPolls.polled[state.RunID] = now
	ciPolls.mu.Unlock()
	reading, err := f.Forge.RequiredChecks(ctx, wait.Commit, wait.Required)
	if err != nil {
		if wait.Overdue(now) {
			return true, "the forge could not be read after the wait limit", nil
		}
		return false, "", err
	}
	decided, why := ciWaitDecided(*wait, reading, now)
	ciPolls.mu.Lock()
	ciPolls.lastSay[state.RunID], ciPolls.decided[state.RunID] = why, decided
	ciPolls.mu.Unlock()
	return decided, why, nil
}
