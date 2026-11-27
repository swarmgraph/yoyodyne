package orchestrator

// Checks on CI, build 3 (docs/designs/checks-on-ci.md): with
// execution.ci_checks.enabled the harness runs only the short local check
// before review, pushes the candidate commit, records the wait on the run and
// gives its developer slot back. The wait is a recorded state of the run
// (runstate.CIWait): a later process reads the forge for that commit and
// carries on, and never pushes a different commit or starts the wait again.
//
// Review starts only once every required check has passed on exactly that
// commit. Where the wait ends any other way — a required check was cancelled
// or skipped, never started within the wait limit, the forge cannot be read, or
// the switch was turned off — the run runs the full declared checks
// on this machine over the same commit and goes on from their result (the
// fallback). That is what the design gives for CI being down or slow and for
// switching off, and none of it spends a repair round: only a check that fails
// does.
//
// A required check that failed on the forge is a failed check, not an outage:
// it goes back to the developer with the failing checks, the failing tests and
// the end of each check's output, secrets removed, recorded on the run as a
// local failed check is, and it spends one repair round. A failed check whose
// log cannot be read is still a failed check: it goes back with why its log
// could not be read, and is not a reason to run the full checks here
// (handBackCIFailure). After the repair the
// short local check runs again and the new commit is pushed and waited on for
// its own result; a result for an earlier commit is never carried forward.
//
// After a fallback the forge is read again for the same commit once the local
// checks have passed and once more before an approved change is promoted. An
// answer found then is recorded on the run (runstate.CIWaitLate), and a failure
// from the forge counts as a failure even though the local checks passed.
//
// A pull request that conflicts with its target ends the wait at the look that
// finds it, before anything its checks say (ciconflict.go).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CIForge is what the wait asks of the forge: push a run's branch, open or
// refresh its draft pull request, and read the required checks on a commit.
// publish.GitHub does all four; the last reads the log of a failing check.
type CIForge interface {
	PushBranch(ctx context.Context, request publish.PushRequest) error
	EnsureDraft(ctx context.Context, request publish.Request) (publish.PullRequest, error)
	RequiredChecks(ctx context.Context, commit string, required []string) (publish.RequiredCheckReading, error)
	CheckOutput(ctx context.Context, check publish.RequiredCheck) (publish.CheckOutput, error)
	// WorkflowDigest is the digest of the forge's workflow files at a commit,
	// recorded with a passing result so a change to them withdraws it.
	WorkflowDigest(ctx context.Context, commit string) (string, error)
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
		// A commit whose wait already fell back to this machine is not pushed
		// again, but what the forge has said about it since still counts.
		return a.lateCIResult(ctx)
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
			return a.blockOnRefusedPush(fmt.Errorf("push the candidate commit for the forge's checks: %w", err))
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
			return a.blockOnRefusedPush(fmt.Errorf("open the draft pull request for the forge's checks: %w", err))
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
	// A wait that follows one a conflict ended says what was done about it.
	if end := a.state.CIWaitEnd; end != nil && end.Conflict {
		wait.Follows = end.Says
	}
	a.state.CIWait = &wait
	a.state.UpdatedAt = pushedAt
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the wait for the forge's checks: %w", err)
	}
	return ciWaitPause{wait: wait}
}

// blockOnRefusedPush holds a run whose push, or the draft pull request that
// carries it, the forge refused. Integration would need the same forge, so
// nothing is gained by checking the change on this machine instead: the item is
// blocked for the development manager with the forge's own words, the branch
// and worktree are kept, and no repair round is spent, because the refusal is
// not the change's.
func (a *activeRun) blockOnRefusedPush(refusal error) error {
	if err := a.block(renderRefusedPushBlockerNotes(a.outcome, refusal)); err != nil {
		return stoppedBy(runstate.StopPublish, withFailedRecord(refusal, fmt.Errorf("record the forge's refusal as a blocker: %w", err)))
	}
	return stoppedBy(runstate.StopPublish, refusal)
}

// renderRefusedPushBlockerNotes describes a run held because the forge
// refused the push its checks on CI needed.
func renderRefusedPushBlockerNotes(outcome Outcome, refusal error) string {
	return strings.Join([]string{
		"Yoyodyne did not send this item's change for review: the forge refused the push the change's checks on CI needed, and the change would have to reach the same forge to be merged.",
		"No repair attempt was spent, because the refusal is the forge's and not the change's.",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are kept. Once the forge accepts the push, the change can be run again from where it stopped.",
		"What the forge said: " + refusal.Error(),
	}, "\n")
}

// settleCIWait is the first thing a run does once it has a slot again with a
// wait recorded. It reads the forge for exactly the commit the wait names and
// pushes nothing for it. A pull request that conflicts with its target is
// answered first (bringTargetIn), and where that changes the head the wait
// starts again on it. Every required check passed: the wait ends and review
// goes on.
// A required check that failed goes back to the developer as a failed check
// (handBackCIFailure). Still running within the limit: the run parks again.
// Anything else, or the switch turned off, ends the wait and runs the full
// declared checks over the same commit. A forge that cannot be read here is one
// of those: the run only takes its slot back once a pull decided the wait
// (ForgeCIWaits.Decided, which already waits out a short spell of failed reads)
// or after a restart, and parking again on an unreadable forge would give the
// slot back to take it again on the next pull.
func (a *activeRun) settleCIWait(ctx context.Context) error {
	p := a.pipeline
	wait := *a.state.CIWait
	now := p.clock().Now()
	a.noteContinuedUnderHold(now)
	if wait.Commit != strings.ToLower(strings.TrimSpace(a.state.HarnessCommit)) {
		return a.endCIWaitWithFullChecks(ctx, wait, "the run's change moved on from the commit it waited for, so the forge's answer does not apply to it")
	}
	if !p.Config.Execution.CIChecks.Enabled || p.CIForge == nil {
		return a.endCIWaitWithFullChecks(ctx, wait, "checks on CI was switched off during the wait")
	}
	// A conflict is answered before anything the checks say (ciconflict.go).
	if ciWaitConflicts(ctx, p.CIForge, a.state) {
		answered, err := a.bringTargetIn(ctx, wait)
		if answered || err != nil {
			return err
		}
		wait = *a.state.CIWait
	}
	reading, err := p.CIForge.RequiredChecks(ctx, wait.Commit, wait.Required)
	if err != nil {
		return a.endCIWaitWithFullChecks(ctx, wait, "the forge could not be read: "+err.Error())
	}
	if reading.Commit != wait.Commit {
		return a.endCIWaitWithFullChecks(ctx, wait, "the forge answered for another commit")
	}
	if reading.AllPassed() {
		evidence, err := ciEvidenceFrom(ctx, p.CIForge, wait, reading)
		if err != nil {
			if wait.Overdue(now) {
				return a.endCIWaitWithFullChecks(ctx, wait, "the passing result could not be recorded as evidence after the wait limit: "+err.Error())
			}
			return a.parkAgain(wait, "the passing result could not be recorded as evidence: "+err.Error())
		}
		a.state.CIWait = nil
		a.state.CIWaitEnd = &runstate.CIWaitEnd{Commit: wait.Commit, At: now, Passed: true,
			Says:           "every required check passed on the commit pushed: " + strings.Join(wait.Required, ", "),
			Checks:         evidence.checks,
			WorkflowDigest: evidence.digest,
		}
		a.state.UpdatedAt = now
		if err := p.Store.Save(a.state); err != nil {
			return fmt.Errorf("record that the forge's checks passed: %w", err)
		}
		return nil
	}
	var failed []publish.RequiredCheck
	for _, check := range reading.Checks {
		if check.Result == publish.CheckFailed {
			failed = append(failed, check)
		}
	}
	if len(failed) > 0 {
		return a.handBackCIFailure(ctx, wait, failed)
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

// noteContinuedUnderHold notes on a run the operator started by name that its
// wait was taken up while new work was held, and why that was allowed. Every way
// settling a wait can end saves the run or ends it, and that save writes this.
// The hold is read for the record only, so one that cannot be read notes nothing.
func (a *activeRun) noteContinuedUnderHold(now time.Time) {
	if !a.state.NamedByOperator() {
		return
	}
	hold, held, err := a.pipeline.Intake.Held()
	if err != nil || !held {
		return
	}
	a.state.ContinuedUnderHold = &runstate.ContinuedUnderHold{At: now.UTC(), Why: fmt.Sprintf(
		"the operator started this run by name, so it was taken up from its wait for the forge's checks although new work has been held since %s (%s)",
		runstate.LocalStamp(hold.HeldAt), hold.Says())}
}

// handBackCIFailure ends the wait on required checks that failed on the
// commit and returns them as a failed check, which the round's repair loop
// records and answers with one repair round like any failed check. The forge's
// output is read here because a developer's sandbox has no network.
//
// A log that cannot be read does not send the run to the full checks on this
// machine: the forge has just said the check failed, so the failure is handed
// back all the same, naming the check, why its log could not be read and where
// it is, and the unreadable log is reported once (readCIOutputs). Running the
// whole suite here instead would cost up to an hour and a half and, when it
// passed, still end with the forge's failure handed back without its cause.
func (a *activeRun) handBackCIFailure(ctx context.Context, wait runstate.CIWait, failed []publish.RequiredCheck) error {
	outputs, reported := a.readCIOutputs(ctx, wait.Commit, failed)
	names := make([]string, len(failed))
	for i, check := range failed {
		names[i] = check.Name
	}
	now := a.pipeline.clock().Now()
	a.state.CIWait = nil
	a.state.CIWaitEnd = &runstate.CIWaitEnd{Commit: wait.Commit, At: now,
		Says:              "required checks failed on the forge, so the change went back to its developer: " + strings.Join(names, ", "),
		UnreadLogReported: reported}
	a.state.UpdatedAt = now
	// Nothing is saved here: the repair loop records the failure and saves the
	// run when it spends the repair round. A process that stops before then
	// finds the wait still recorded, reads the same failure from the forge, and
	// hands it back again.
	return checkFailure{result: ciFailureResult(wait.Commit, failed, outputs)}
}

// ciCheckOutput is one failing check's output as read from the forge, or why
// it could not be read.
type ciCheckOutput struct {
	publish.CheckOutput
	unread error
}

// readCIOutputs reads the log of each failing check on commit. A log that
// cannot be read is kept as the reason, and the first time a log on this commit
// cannot be read the harness reports it, once, naming each such check and why.
// It returns whether that report is owed no longer, which the caller records on
// the wait's ending (runstate.CIWaitEnd.UnreadLogReported) so a later read of
// the same commit does not report it again.
func (a *activeRun) readCIOutputs(ctx context.Context, commit string, failed []publish.RequiredCheck) ([]ciCheckOutput, bool) {
	outputs := make([]ciCheckOutput, len(failed))
	var unread, links []string
	for i, check := range failed {
		output, err := a.pipeline.CIForge.CheckOutput(ctx, check)
		outputs[i] = ciCheckOutput{CheckOutput: output, unread: err}
		if err != nil {
			unread = append(unread, fmt.Sprintf("%s (%v)", check.Name, err))
			if check.URL != "" {
				links = append(links, check.Name+": "+check.URL)
			}
		}
	}
	reported := a.unreadLogReported(commit)
	if len(unread) == 0 || reported {
		return outputs, reported
	}
	return outputs, a.reportUnreadCILogs(commit, unread, links)
}

// unreadLogReported reports whether an unreadable log on commit was already
// reported for this run.
func (a *activeRun) unreadLogReported(commit string) bool {
	end := a.state.CIWaitEnd
	return end != nil && end.UnreadLogReported && end.Commit == commit
}

// reportUnreadCILogs files the harness's one report that the log of a check
// that failed on the forge could not be read, for the development manager,
// and returns whether it was filed. A report that could not be filed is noted
// on the run and tried again the next time the commit's logs are read.
func (a *activeRun) reportUnreadCILogs(commit string, unread, links []string) bool {
	p := a.pipeline
	if p.Reports == nil {
		return false
	}
	message := fmt.Sprintf("Run %s: GitHub says a required check failed on commit %s, and the harness could not read that check's log: %s. "+
		"The developer was handed the failure without the failing test or its output, so the repair may be a guess until somebody reads the log on GitHub",
		a.state.RunID, shortCommit(commit), oneline.Fold(strings.Join(unread, "; "), report.MaxMessageBytes/2))
	if len(links) > 0 {
		message += " (" + strings.Join(links, ", ") + ")"
	}
	message += "."
	collected, err := report.Collect([]report.Entry{{Severity: report.SeverityWarning, Message: message, For: domain.RoleDevelopmentManager}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        a.state.RunID,
		WorkItemID:   a.state.WorkItemID,
		Build:        p.Build,
		ProductID:    p.Config.Product.ID,
		RepositoryID: string(p.Config.Product.RepositoryID),
	}, p.clock().Now())
	if err == nil {
		for _, reported := range collected {
			if err = p.Reports.Append(reported); err != nil {
				break
			}
		}
	}
	if err != nil {
		a.noteReportProblem(report.HarnessReporter, fmt.Errorf("the report that a check's log on the forge could not be read: %w", err))
		return false
	}
	return true
}

// ciFailureResult is the forge's failure in the shape of a local failed check:
// a command, a failing exit, and an output that opens with the failing tests
// as checks.FailingNames reads them, followed by the end of each failing
// check's output. The whole is held to the bound a recorded check output has,
// so each check keeps an equal share of its end.
func ciFailureResult(commit string, failed []publish.RequiredCheck, outputs []ciCheckOutput) checks.Result {
	var names []string
	for _, check := range failed {
		names = append(names, check.Name)
	}
	header := ciFailingTestsLine(outputs) + fmt.Sprintf("Failed on the forge for commit %s.\n", commit)
	return checks.Result{
		Command:       "checks on the forge: " + strings.Join(names, ", "),
		Process:       execution.ProcessResult{ExitCode: 1},
		FailureOutput: ciFailureOutput(header, failed, outputs, runstate.MaxCheckOutputBytes),
	}
}

// ciFailingTestsLine is the line that opens a forge failure's output: the
// failing tests as checks.FailingNames reads them back, or why none is named.
func ciFailingTestsLine(outputs []ciCheckOutput) string {
	var tests []string
	read := 0
	for _, output := range outputs {
		if output.unread == nil {
			read++
			tests = append(tests, output.FailingTests...)
		}
	}
	if line := checks.FailingNamesLine(tests); line != "" {
		return line
	}
	if read == 0 {
		return "The forge's log of the failing checks could not be read, so the failing tests are not known.\n"
	}
	return "No failing test was named in the forge's output, so only its end is kept.\n"
}

// ciFailureOutput is header followed by each failing check's section: the end
// of its log, or why its log could not be read and where it is. The whole is
// held to limit, and each check keeps an equal share of what the header
// leaves.
func ciFailureOutput(header string, failed []publish.RequiredCheck, outputs []ciCheckOutput, limit int) string {
	share := (limit - len(header)) / max(len(failed), 1)
	var body strings.Builder
	for i, check := range failed {
		section := fmt.Sprintf("\nCheck %s (%s):\n", check.Name, check.Result)
		var tail string
		if outputs[i].unread != nil {
			tail = "Its log on the forge could not be read: " + outputs[i].unread.Error()
			if check.URL != "" {
				tail += "\nThe log is at " + check.URL
			}
		} else {
			tail = strings.TrimRight(outputs[i].Tail, "\n")
			if outputs[i].Cut {
				tail = "[earlier check output truncated]\n" + tail
			}
		}
		body.WriteString(section)
		body.WriteString(boundedTail(tail, max(share-len(section)-1, 0)))
		body.WriteString("\n")
	}
	return boundedTail(header+body.String(), limit)
}

type ciEvidence struct {
	checks []runstate.CICheckEvidence
	digest string
}

// ciEvidenceFrom makes a passing reading into evidence for the commit the wait
// names. Every check the wait required must be in the reading, passed, and ran on
// that exact commit; a reading that is not is an error rather than evidence. The
// workflow digest is read from the forge for the same commit.
func ciEvidenceFrom(ctx context.Context, forge CIForge, wait runstate.CIWait, reading publish.RequiredCheckReading) (ciEvidence, error) {
	byName := map[string]publish.RequiredCheck{}
	for _, check := range reading.Checks {
		byName[check.Name] = check
	}
	var evidence ciEvidence
	for _, name := range wait.Required {
		check, ok := byName[strings.TrimSpace(name)]
		if !ok || check.Result != publish.CheckPassed || strings.ToLower(check.Commit) != wait.Commit {
			return ciEvidence{}, fmt.Errorf("%s did not read as passed on %s", name, wait.Commit)
		}
		evidence.checks = append(evidence.checks, runstate.CICheckEvidence{Name: check.Name, Result: string(check.Result), Commit: check.Commit})
	}
	digest, err := forge.WorkflowDigest(ctx, wait.Commit)
	if err != nil {
		return ciEvidence{}, err
	}
	evidence.digest = digest
	return evidence, nil
}

// describeCIEvidence is what the reviewer is shown of the forge's checks: each
// required check's name, result and the commit it ran on, and whether the result
// counts as evidence for the candidate. The workflow digest is read from the
// forge again here, so a result recorded against workflow files that have since
// changed is shown as giving no credit. It is empty when the run did not wait on
// the forge.
func (a *activeRun) describeCIEvidence(ctx context.Context) string {
	end := a.state.CIWaitEnd
	p := a.pipeline
	if end == nil || !p.Config.Execution.CIChecks.Enabled {
		return ""
	}
	candidate := strings.ToLower(strings.TrimSpace(a.state.HarnessCommit))
	var digest string
	var readErr error
	if p.CIForge != nil && candidate != "" {
		digest, readErr = p.CIForge.WorkflowDigest(ctx, candidate)
	}
	counts, why := end.CountsFor(candidate, p.Config.Execution.CIChecks.Required, digest)
	if readErr != nil {
		counts, why = false, "the workflow files could not be read to confirm it: "+readErr.Error()
	}
	var text strings.Builder
	if counts {
		text.WriteString("The harness read these results from the forge for the candidate commit, and they count as evidence:\n")
	} else {
		text.WriteString("These results give no credit as evidence for the candidate commit: " + why + ".\n")
	}
	for _, check := range end.Checks {
		fmt.Fprintf(&text, "- %s: %s, on commit %s\n", check.Name, check.Result, check.Commit)
	}
	if len(end.Checks) == 0 {
		text.WriteString("- no required check result was recorded; " + end.Says + "\n")
	}
	return text.String()
}

// refusedAfterTakeUp is what Run and Continue return from resumeRun for a run
// they adopted and took a developer slot back for (reclaimSlot): the resume as
// it came, with the slot given back where it was refused before the run went
// on (giveBackCIWaitSlot).
func (p Pipeline) refusedAfterTakeUp(ctx context.Context, runID string, outcome Outcome, err error) (Outcome, error) {
	if err != nil && !outcome.continuationAccepted {
		err = p.giveBackCIWaitSlot(ctx, runID, err)
	}
	return outcome, err
}

// giveBackCIWaitSlot puts a run continued from its wait for the forge's checks
// back to waiting, holding no developer slot, where resumeRun refused it after
// reclaimSlot had taken the slot back and before anything else was written: an
// item that does not read in progress, a provider whose settings could not be
// checked, a context that could not be assembled. Taking the slot back saved
// the run as holding one, worked by this process, so without this the run
// stands as running at its checks with nothing behind it once the lease is let
// go: no pull continues it, because its worker is alive, until the slot release
// finds nothing holding the lease. Given back, it reads as waiting on a forge
// that has already decided, the next pull continues it (after the pull's pacing
// of a refused continuation), and its record says why it was not taken up.
// refusal is returned as it came, with any failure to give the slot back added.
func (p Pipeline) giveBackCIWaitSlot(ctx context.Context, runID string, refusal error) error {
	state, err := p.Store.Load(runID)
	if err != nil {
		return errors.Join(refusal, fmt.Errorf("read run %s to give back the developer slot its refused continuation took: %w", runID, err))
	}
	if !pausedForCIWait(state) || !state.CIWait.SlotTaken {
		return refusal
	}
	now := p.clock().Now()
	if _, err := p.Store.Update(context.WithoutCancel(ctx), runID, runstate.CIWaitSlotGivenBack{
		Commit: state.CIWait.Commit,
		Why: fmt.Sprintf("continuing run %s from its wait for %s was refused at %s after it had taken a developer slot back, so the slot was given back and the run waits to be continued again: %s",
			runID, state.CIWait.Summary(), runstate.LocalStamp(now), oneline.Fold(refusal.Error(), 2048)),
		At: now,
	}); err != nil {
		return errors.Join(refusal, fmt.Errorf("give back the developer slot run %s took for its refused continuation: %w", runID, err))
	}
	return refusal
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
	reported := a.unreadLogReported(wait.Commit)
	a.state.CIWait = nil
	a.state.CIWaitEnd = &runstate.CIWaitEnd{Commit: wait.Commit, At: a.pipeline.clock().Now(), Says: why + "; the full checks run on this machine over the same commit",
		UnreadLogReported: reported}
	a.fullLocalCommit = wait.Commit
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record how the wait for the forge's checks ended: %w", err)
	}
	if err := a.verify(ctx); err != nil {
		return err
	}
	// The local checks take long enough that the forge has often answered by
	// now, and its answer is read before review rather than after it.
	return a.lateCIResult(ctx)
}

// lateCIResult reads the forge once more for a commit whose wait ended without
// its answer and the full checks ran on this machine instead, and records what
// it finds. A required check that failed there is returned as a failing check,
// so it is handed to the developer and spends a repair round like any failing
// check: a failure from either the forge or this machine counts. An answer
// still to come, or a forge that cannot be read, records nothing and holds
// nothing up, because the local checks already stand for the commit. Once an
// answer is recorded the forge is not read again for that commit, and a
// recorded failure keeps counting for as long as the change stays at it.
//
// The answer is kept on the run's record in memory and written by the save
// the run makes next — the failing check handed back, the review, or the
// run's ending — rather than by a save of its own.
func (a *activeRun) lateCIResult(ctx context.Context) error {
	p := a.pipeline
	end := a.state.CIWaitEnd
	switch {
	case end == nil || end.Passed:
		return nil
	case end.Commit != strings.ToLower(strings.TrimSpace(a.state.HarnessCommit)):
		return nil
	case end.Late != nil && end.Late.Passed:
		return nil
	case end.Late != nil:
		// The forge already failed this commit and a repair left it as it was, so
		// the failure still stands for it however the local checks went.
		return lateCIFailure(end.Late.Says)
	case !p.Config.Execution.CIChecks.Enabled || p.CIForge == nil || len(p.Config.Execution.CIChecks.Required) == 0:
		return nil
	}
	reading, err := p.CIForge.RequiredChecks(ctx, end.Commit, p.Config.Execution.CIChecks.Required)
	if err != nil || reading.Commit != end.Commit {
		return nil
	}
	now := p.clock().Now()
	if reading.AllPassed() {
		end.Late = &runstate.CIWaitLate{At: now, Passed: true,
			Says: "after the full checks ran on this machine, every required check passed on the forge too: " + strings.Join(p.Config.Execution.CIChecks.Required, ", ")}
		a.state.UpdatedAt = now
		return nil
	}
	var failed []publish.RequiredCheck
	for _, check := range reading.Checks {
		if check.Result == publish.CheckFailed {
			failed = append(failed, check)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	outputs, reported := a.readCIOutputs(ctx, end.Commit, failed)
	end.UnreadLogReported = reported
	end.Late = &runstate.CIWaitLate{At: now, Says: lateCIFailureSays(end.Commit, failed, outputs)}
	a.state.UpdatedAt = now
	return lateCIFailure(end.Late.Says)
}

// lateCIFailureSays is the forge's later failure as the run records it and the
// developer is handed it: the failing tests first, as checks.FailingNames reads
// them, then which checks failed and where, then the end of each check's log or
// why it could not be read. It is held to the bound the record keeps of it, so
// a repair that leaves the commit as it was is handed the same text again.
func lateCIFailureSays(commit string, failed []publish.RequiredCheck, outputs []ciCheckOutput) string {
	lines := make([]string, 0, len(failed)+1)
	lines = append(lines, "The full checks passed on this machine, but these required checks failed on the forge for the same commit "+commit+", and a failure from either counts:")
	for _, check := range failed {
		line := "- " + check.Name + " ended " + string(check.Result)
		if check.URL != "" {
			line += " (" + check.URL + ")"
		}
		lines = append(lines, line)
	}
	header := ciFailingTestsLine(outputs) + strings.Join(lines, "\n") + "\n"
	return strings.TrimRight(ciFailureOutput(header, failed, outputs, runstate.MaxRecordedTextBytes), "\n")
}

// lateCIFailure is the forge's later failure as a failing check, in the shape
// a local failing check is handed to the developer in.
func lateCIFailure(says string) error {
	return checkFailure{result: checks.Result{
		Command:       "the forge's required checks",
		Process:       execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
		FailureOutput: says + "\n",
	}}
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

// ciAllPassed is what a pull's reading says of a commit on which every required
// check passed, and what marks a recorded decision as a pass.
const ciAllPassed = "every required check passed on the commit pushed"

// ciWaitDecided is the rule a pull applies to one reading of a waiting run.
func ciWaitDecided(wait runstate.CIWait, reading publish.RequiredCheckReading, now time.Time) (bool, string) {
	if reading.AllPassed() {
		return true, ciAllPassed
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

// ciUnreadableFor is how long the forge may go unreadable for one waiting run
// before the wait is decided and the run falls back to the full checks on this
// machine. A single failed read is waited out, because a network that drops
// for a moment is not a forge that cannot be read; one that stays unreadable
// this long is, and waiting out the whole wait limit for it would hold the
// change for an hour with nothing to show.
const ciUnreadableFor = 5 * time.Minute

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
	mu         sync.Mutex
	polled     map[string]time.Time
	lastSay    map[string]string
	decided    map[string]bool
	unreadable map[string]time.Time
}{polled: map[string]time.Time{}, lastSay: map[string]string{}, decided: map[string]bool{}, unreadable: map[string]time.Time{}}

// ciPollKey is what one waiting run's last reading is kept under. It names the
// commit and the last answered conflict with the run, so a reading that decided
// a wait is never taken for the wait on the head that followed it.
func ciPollKey(state runstate.State) string {
	key := state.RunID + " " + state.CIWait.Commit
	if tried := state.CIWait.Conflict; tried != nil {
		key += " " + tried.At.UTC().Format(time.RFC3339Nano)
	}
	return key
}

// Decided reads the forge for the commit the run's wait names, at most once a
// minute for one wait. A conflict decides it whatever the checks say.
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
	poll := ciPollKey(state)
	ciPolls.mu.Lock()
	if at, ok := ciPolls.polled[poll]; ok && now.Sub(at) < ciPollEvery && now.After(at) {
		say, decided := ciPolls.lastSay[poll], ciPolls.decided[poll]
		ciPolls.mu.Unlock()
		return decided, say, nil
	}
	ciPolls.polled[poll] = now
	ciPolls.mu.Unlock()
	if ciWaitConflicts(ctx, f.Forge, state) {
		if decided, says, ordinary := ciConflictDecided(state, now); !ordinary {
			ciPolls.mu.Lock()
			ciPolls.lastSay[poll], ciPolls.decided[poll] = says, decided
			ciPolls.mu.Unlock()
			return decided, says, nil
		}
	}
	reading, err := f.Forge.RequiredChecks(ctx, wait.Commit, wait.Required)
	if err != nil {
		ciPolls.mu.Lock()
		since, ok := ciPolls.unreadable[state.RunID]
		if !ok || now.Before(since) {
			since = now
			ciPolls.unreadable[state.RunID] = since
		}
		ciPolls.mu.Unlock()
		switch {
		case wait.Overdue(now):
			return true, "the forge could not be read after the wait limit", nil
		case now.Sub(since) >= ciUnreadableFor:
			return true, fmt.Sprintf("the forge could not be read for %s", now.Sub(since).Round(time.Second)), nil
		}
		return false, "", err
	}
	decided, why := ciWaitDecided(*wait, reading, now)
	ciPolls.mu.Lock()
	delete(ciPolls.unreadable, state.RunID)
	ciPolls.lastSay[poll], ciPolls.decided[poll] = why, decided
	ciPolls.mu.Unlock()
	return decided, why, nil
}
