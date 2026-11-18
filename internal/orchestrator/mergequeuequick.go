package orchestrator

// The merge queue under checks on CI: the quick local checks on this machine,
// and GitHub's required checks on the candidate's own commit for the rest
// ("GitHub's required checks as the queue's full check" in
// docs/designs/integration-through-a-merge-queue.md).
//
// Where the project has checks on CI switched on, a code entry's candidate is
// verified by the checks execution.ci_checks.local names — formatting, vet,
// build and the tests that need macOS in this repository — and, while no
// required check is the race check, the queue's race check narrowed to what the
// candidate touches where the change owes it (quickQueueChecks). The required
// checks are recorded with that configuration (ForgeChecks), so the candidate's
// record says it lands only where they passed on its exact commit, and
// runstate's Gate refuses it otherwise.
//
// Once the quick checks pass, the candidate is pushed to its candidate branch
// and the pull request that carries it is opened, which is what starts the
// forge's checks on it (pushForForge). The independent review then runs beside
// the wait for them: the forge is read every ForgePoll while the review runs,
// and a required check that fails on the candidate stops the review, which
// earns nothing (reviewBesideForge). The reading is recorded on the candidate,
// with the result of each failed check at the candidate's base, which is what
// says whether the failure is the change's or the target's (recovery reads it,
// ClassifyMergeQueueFailure). A check still running is waited for on a later
// pass of the queue, so no developer slot is held for it.
//
// A required check that ends without judging the candidate — cancelled,
// skipped, or anything but passed, failed or still to finish — or one not
// finished within execution.ci_checks.wait_limit of the push, means the forge
// cannot settle it: the reason is recorded (MergeQueueCIEvidence.FellBack), and
// the entry's next candidate runs the full local checks instead, charged to
// nothing (forgeChecksFellBack).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// defaultForgePoll is how often the forge is read while a review runs beside
// the wait for its checks.
const defaultForgePoll = 30 * time.Second

// MergeQueueCandidatePublisher puts a candidate on its candidate branch on the
// push remote. It is satisfied by *gitworktree.Manager.
type MergeQueueCandidatePublisher interface {
	QueueCandidateBranchCommit(ctx context.Context, branch string) (string, bool, error)
	PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error
}

// forgeChecksStandIn reports whether the forge's required checks stand in for
// the full local run of a queue candidate: checks on CI is on, names the checks
// that must pass, and there is a forge to push to and read.
func (p *Pipeline) forgeChecksStandIn() bool {
	ci := p.Config.Execution.CIChecks
	return ci.Enabled && len(ci.Required) > 0 && p.CIForge != nil && p.Publisher != nil
}

// quickQueueChecks is the checks a code entry's candidate runs on this machine
// where the forge's required checks stand in for the rest: the configured
// checks execution.ci_checks.local names, in the order checks lists them, and
// the queue's race check, told only the packages the change touches, where the
// change owes it and no required check is a race check of the target's own.
func (p *Pipeline) quickQueueChecks(entry runstate.MergeQueueEntry) runstate.MergeQueueCheckConfiguration {
	ci := p.Config.Execution.CIChecks
	quick := shortLocalChecks(p.Config.Checks, ci.Local)
	commands := slices.Clone(quick)
	race := p.Config.MergeQueueRaceCheck
	if race != "" && !slices.Contains(commands, race) && !requiresRaceCheck(ci.Required, race) {
		owed := true
		if p.Store != nil {
			if run, err := p.Store.Load(entry.RunID); err == nil {
				owed = raceOwed(run, entry.ApprovedHead, race)
			}
		}
		if owed {
			commands = append(commands, race)
		}
	}
	return runstate.NewMergeQueueForgeConfiguration(commands, quick, ci.Required)
}

// requiresRaceCheck reports whether the target requires a race check of its
// own: one of the required checks is named by a word of the queue's race check
// command, as the job "race" is by "make race". The configuration names no
// other link between a forge's job and a local command, so this is the one the
// two share.
func requiresRaceCheck(required []string, race string) bool {
	words := strings.Fields(race)
	for _, name := range required {
		if slices.Contains(words, strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// forgeChecksFellBack is why an entry's candidates no longer lean on the
// forge's checks, and empty where none of them found the forge unable to
// settle one.
func forgeChecksFellBack(generations []runstate.MergeQueueGeneration) string {
	for index := len(generations) - 1; index >= 0; index-- {
		if ci := generations[index].CIChecks; ci != nil && ci.FellBack != "" {
			return ci.FellBack
		}
	}
	return ""
}

func (w MergeQueueWorker) forgePoll() time.Duration {
	if w.ForgePoll > 0 {
		return w.ForgePoll
	}
	return defaultForgePoll
}

// reviewBesideForge pushes the candidate for the forge's checks and runs its
// review while they run. A required check that fails on the candidate stops
// the review, or keeps it from starting. It reports moved where the target
// moved under the candidate, and fellBack where the forge cannot settle it and
// the entry's next candidate runs the full local checks; a check still running
// is a wait.
func (w MergeQueueWorker) reviewBesideForge(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, run runstate.State, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, bool, bool, error) {
	generation, err := w.pushForForge(ctx, lease, key, entry, generation)
	if err != nil {
		return generation, false, false, err
	}
	reviewed := generation.Review != nil && generation.Review.FinishedAt != nil && generation.Review.Problem == ""
	if !reviewed && len(w.failedOnForge(ctx, generation)) == 0 {
		generation, err = w.reviewWhileWaiting(ctx, lease, key, entry, run, generation)
		var wait mergeQueueWait
		if err != nil && !errors.As(err, &wait) {
			return generation, false, false, err
		}
		if err != nil {
			// The provider could not make the review now; what the forge says is
			// still recorded, so a failure is not left waiting on the review.
			if settled, _, fellBack, settleErr := w.settleForge(ctx, lease, key, generation); settleErr == nil && (len(settled.CIChecks.FailedOnForge()) > 0 || fellBack) {
				return settled, false, fellBack, nil
			}
			return generation, false, false, err
		}
	}
	generation, moved, err := w.moved(ctx, lease, key, generation)
	if err != nil || moved {
		return generation, moved, false, err
	}
	generation, waiting, fellBack, err := w.settleForge(ctx, lease, key, generation)
	if err != nil || fellBack {
		return generation, false, fellBack, err
	}
	// A review that did not approve refuses the candidate whatever the forge
	// says later, so only an approved one waits for the forge.
	if waiting != "" && reviewApproved(generation) {
		return generation, false, false, mergeQueueWait{reason: waiting}
	}
	return generation, false, false, nil
}

func reviewApproved(generation runstate.MergeQueueGeneration) bool {
	verdict := generation.Review
	return verdict != nil && verdict.FinishedAt != nil && verdict.Problem == "" && verdict.Decision == "approve"
}

// reviewWhileWaiting runs the candidate's review and reads the forge every
// ForgePoll while it runs. A required check failing on the candidate cancels
// the review, and what the review recorded by then is kept as it is: a review
// that never finished earns nothing.
func (w MergeQueueWorker) reviewWhileWaiting(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, run runstate.State, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, error) {
	reviewCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type reviewed struct {
		generation runstate.MergeQueueGeneration
		err        error
	}
	done := make(chan reviewed, 1)
	go func() {
		// The target moving is read once the review and the wait are both over.
		g, _, err := w.review(reviewCtx, lease, key, entry, run, generation)
		done <- reviewed{generation: g, err: err}
	}()
	poll := time.NewTicker(w.forgePoll())
	defer poll.Stop()
	for {
		select {
		case result := <-done:
			return result.generation, result.err
		case <-poll.C:
			if failed := w.failedOnForge(ctx, generation); len(failed) > 0 {
				cancel()
				result := <-done
				// The review was stopped, not refused: what it says about the
				// stop is not an error of the queue's.
				return result.generation, nil
			}
		}
	}
}

// failedOnForge is the required checks the forge reports failed on the
// candidate commit now. A forge that cannot be read says nothing here; the
// reading recorded after the review says it.
func (w MergeQueueWorker) failedOnForge(ctx context.Context, generation runstate.MergeQueueGeneration) []string {
	reading, err := w.Pipeline.CIForge.RequiredChecks(ctx, generation.Candidate, generation.Checks.ForgeChecks)
	if err != nil || reading.Commit != generation.Candidate {
		return nil
	}
	var failed []string
	for _, check := range reading.Checks {
		if check.Result == publish.CheckFailed && check.Commit == generation.Candidate {
			failed = append(failed, check.Name)
		}
	}
	return failed
}

// pushForForge puts the candidate on its candidate branch on the forge and
// opens the pull request that carries it, so the forge runs its required
// checks on exactly that commit. A candidate already pushed is not pushed
// again. The promotion pushes the same commit to the same branch and finds the
// same pull request, so neither is done twice.
func (w MergeQueueWorker) pushForForge(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, error) {
	if push := generation.ForgePush; push != nil && push.Binding == generation.Binding() && push.PullRequest != 0 {
		return generation, nil
	}
	if w.Publish == nil {
		return generation, errors.New("the forge's required checks stand in for this candidate's full check, and the merge queue worker has no way to push the candidate to the forge")
	}
	p := w.Pipeline
	branch := runstate.MergeQueueCandidateBranch(entry.EntryID)
	published, exists, err := w.Publish.QueueCandidateBranchCommit(ctx, branch)
	if err != nil {
		return generation, fmt.Errorf("read where %s stands before pushing the candidate for the forge's checks: %w", branch, err)
	}
	expected := ""
	if exists {
		expected = published
	}
	err = w.Publish.PublishQueueCandidate(ctx, branch, generation.Candidate, expected)
	if errors.Is(err, gitworktree.ErrRemotePushInterrupted) {
		return generation, mergeQueueWait{reason: fmt.Sprintf("the push of candidate %s to %s for the forge's checks ended before the remote answered (%v); the next pass looks again", generation.Candidate, branch, err)}
	}
	if err != nil {
		return generation, fmt.Errorf("push candidate %s to %s for the forge's checks: %w", generation.Candidate, branch, err)
	}
	pushedAt := w.now()
	if push := generation.ForgePush; push != nil && push.Binding == generation.Binding() {
		pushedAt = push.PushedAt
	}
	generation.ForgePush = &runstate.MergeQueueForgePush{Binding: generation.Binding(), Branch: branch, Commit: generation.Candidate, PushedAt: pushedAt}
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record the push of generation %d's candidate for the forge's checks: %w", generation.Number, err)
	}
	opened, err := p.Publisher.Ensure(ctx, publish.Request{
		Head:  branch,
		Base:  key.TargetBranch,
		Title: fmt.Sprintf("%s: %s", entry.WorkItemID, entry.WorkItemTitle),
		Body: fmt.Sprintf("Merge queue candidate %s for %s: %s at %s with the approved head %s merged onto it, generation %d of entry %s. Its quick local checks passed; GitHub's required checks (%s) and an independent review are run on it now, and it is merged only once both pass.\n\nRun: %s\n",
			generation.Candidate, entry.WorkItemID, key.TargetBranch, generation.TargetBase, entry.ApprovedHead, generation.Number, entry.EntryID,
			strings.Join(generation.Checks.ForgeChecks, ", "), entry.RunID) + nonEmptyLine(p.factoryLine(ctx)),
		Mark: forgeMark(ctx, p.Factory, entry.ProductID, p.Config.Product.ID),
	})
	if err != nil {
		return generation, fmt.Errorf("open the pull request that carries %s for the forge's checks: %w", branch, err)
	}
	generation.ForgePush.PullRequest, generation.ForgePush.URL = opened.Number, opened.URL
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record the pull request that carries generation %d's candidate: %w", generation.Number, err)
	}
	return generation, nil
}

func nonEmptyLine(line string) string {
	if line == "" {
		return ""
	}
	return line + "\n"
}

// settleForge reads the forge's required checks on the candidate and records
// the reading (readCIChecks). Where one failed, its result at the candidate's
// base is read and recorded beside it. It returns why it is still waiting, and
// fellBack where the forge cannot settle the candidate, which is then recorded.
func (w MergeQueueWorker) settleForge(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, string, bool, error) {
	ci := w.Pipeline.Config.Execution.CIChecks
	overdue := generation.ForgePush != nil && w.now().Sub(generation.ForgePush.PushedAt) > ci.WaitLimitInForce()
	read, err := w.readCIChecks(ctx, lease, key, generation)
	if err != nil {
		if !overdue {
			return generation, "", false, mergeQueueWait{reason: fmt.Sprintf("the forge's required checks on candidate %s could not be read (%v); the next pass reads them again", generation.Candidate, err)}
		}
		generation, err = w.fallBack(lease, key, generation, read.CIChecks, fmt.Sprintf("the forge's required checks on candidate %s could not be read within execution.ci_checks.wait_limit (%s) of the push: %v", generation.Candidate, ci.WaitLimitInForce(), err))
		return generation, "", err == nil, err
	}
	generation = read
	evidence := generation.CIChecks
	if evidence == nil {
		return generation, "", false, nil
	}
	if failed := evidence.FailedOnForge(); len(failed) > 0 {
		if base, err := w.Pipeline.CIForge.RequiredChecks(ctx, generation.TargetBase, failed); err == nil && base.Commit == generation.TargetBase {
			revised := *evidence
			for _, check := range base.Checks {
				revised.AtBase = append(revised.AtBase, runstate.CICheckEvidence{Name: check.Name, Result: string(check.Result), Commit: check.Commit})
			}
			generation.CIChecks = &revised
			if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
				return generation, "", false, fmt.Errorf("record the forge's checks at generation %d's base: %w", generation.Number, err)
			}
		}
		return generation, "", false, nil
	}
	var unfinished []string
	for _, check := range evidence.Checks {
		switch publish.CheckResult(check.Result) {
		case publish.CheckPassed:
		case publish.CheckPending, publish.CheckNotStarted:
			unfinished = append(unfinished, check.Name)
		default:
			generation, err = w.fallBack(lease, key, generation, evidence, fmt.Sprintf("%s ended %s on the forge for candidate %s, which judged nothing about it", check.Name, check.Result, generation.Candidate))
			return generation, "", err == nil, err
		}
	}
	if len(unfinished) == 0 {
		return generation, "", false, nil
	}
	if overdue {
		generation, err = w.fallBack(lease, key, generation, evidence, fmt.Sprintf("%s had not finished on the forge for candidate %s within execution.ci_checks.wait_limit (%s) of the push", strings.Join(unfinished, ", "), generation.Candidate, ci.WaitLimitInForce()))
		return generation, "", err == nil, err
	}
	return generation, fmt.Sprintf("waiting for the forge's required checks on candidate %s: %s not finished yet", generation.Candidate, strings.Join(unfinished, ", ")), false, nil
}

// forgeFailure reads a candidate whose quick checks passed for a required
// check the forge failed on its commit. As with a local check, the failure is
// the change's only where the forge passed the same check at the candidate's
// base; failed there too, it is the target's, and not read there, nothing is
// charged until it is. A candidate with no forge failure reports false.
func forgeFailure(failure MergeQueueFailure, generation runstate.MergeQueueGeneration, suspected []string) (MergeQueueFailure, bool) {
	evidence := generation.CIChecks
	if len(generation.Checks.ForgeChecks) == 0 || evidence == nil || evidence.Binding != generation.Binding() || evidence.Commit != generation.Candidate {
		return failure, false
	}
	failed := evidence.FailedOnForge()
	if len(failed) == 0 {
		return failure, false
	}
	check := failed[0]
	failure.Check = check
	result, read := evidence.ResultAtBase(check)
	switch {
	case !read:
		failure.Class = runstate.MergeQueueUnreadableEvidence
		failure.Reason = fmt.Sprintf("the forge's required check %s failed on generation %d's candidate %s, and whether it passes at the candidate's base %s is not known yet; nothing is charged until it is",
			check, generation.Number, generation.Candidate, generation.TargetBase)
		return failure, true
	case result == string(publish.CheckFailed):
		failure.Class = runstate.MergeQueueTargetFailure
		failure.Reason = fmt.Sprintf("the forge's required check %s fails on generation %d's candidate and on %s at its base %s too, so the failure is the target's; the entry waits on the target moving, and nothing is charged for it",
			check, generation.Number, generation.TargetBranch, generation.TargetBase)
		return failure, true
	case result != string(publish.CheckPassed):
		failure.Class = runstate.MergeQueueUnreadableEvidence
		failure.Reason = fmt.Sprintf("the forge's required check %s failed on generation %d's candidate, and ended %s at the candidate's base %s, so whether the failure is the change's is not known; nothing is charged for it",
			check, generation.Number, result, generation.TargetBase)
		return failure, true
	}
	failure.Reason = fmt.Sprintf("the forge's required check %s failed on generation %d's candidate %s, %s at %s with %s merged onto it, and passes on %s at that base; the review was stopped when it failed",
		check, generation.Number, generation.Candidate, generation.TargetBranch, generation.TargetBase, strings.Join(generation.Heads, ", "), generation.TargetBranch)
	return defect(failure, generation, suspected), true
}

// fallBack records why the forge cannot settle a candidate, which has the
// entry's next candidate run the full local checks. evidence is the reading
// to record it on, or nil where none could be made.
func (w MergeQueueWorker) fallBack(lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, evidence *runstate.MergeQueueCIEvidence, why string) (runstate.MergeQueueGeneration, error) {
	var revised runstate.MergeQueueCIEvidence
	if evidence != nil {
		revised = *evidence
	} else {
		revised = runstate.MergeQueueCIEvidence{
			Binding: generation.Binding(), ReadAt: w.now(), Commit: generation.Candidate, Base: generation.TargetBase,
			TargetAtRead: generation.TargetBase, Required: slices.Clone(generation.Checks.ForgeChecks),
		}
	}
	revised.FellBack = boundedEvidence(why + "; the entry's next candidate runs the full local checks instead, and nothing is charged for it")
	generation.CIChecks = &revised
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record that the forge cannot settle generation %d's candidate: %w", generation.Number, err)
	}
	return generation, nil
}
