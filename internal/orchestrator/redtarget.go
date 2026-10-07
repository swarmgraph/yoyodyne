package orchestrator

// A queued merge whose checks fail on the target rather than on the change.
//
// A head level with its target has nothing but the change between it and the
// target, so a failing check that names no file the change touches failed on
// something the target already carries. Until yoyodyne-m5p that was handed to a
// person as a dropped merge, whatever the check named: on 2026-09-28 at 04:15Z
// the adoption check was red on main itself, pull request 863 was withdrawn and
// its item handed to a person, and every merge queued behind it would have met
// the same check and been handed over the same way. A required check red on the
// target, or flaky there, became a hand step per queued merge.
//
// The harness already knows the other shape of this failure. A red landing is
// news about the target branch, not a verdict on the run that landed it: it
// files one p0 bug per target branch and check, under the goal the landed item
// served, and blocks nothing. This is the same filing for the same fact met from
// the other side. The queued merge is withdrawn, one p0 item is filed per target
// branch and failing check — or, where one is already open for that check on
// that branch, this request is noted on it — and the publication is recorded as
// waiting on those items with the harness as the one to move, rather than handed
// to anybody. The work item is made to wait on them in the tracker too.
//
// What ends the wait is the harness's as well. Once every item it waits on is
// closed, a head the fix left behind the target is brought up to date from the
// kept branch by the reconciling sweep, checked, reviewed, and queued again, as
// a queued head behind its target is (ResumeRedTargets); and a head still level
// with a target whose checks now pass is re-armed by the watch's re-arm
// carry-out, with nobody deciding anything (CarryRearms).
//
// Naming no file the change touches is not proof the failure is the target's.
// On 2026-09-29 pull request 907 failed the build check on a test in a package
// its own change added: the forge reported the failure per package and annotated
// no file, so it was filed as main's, the merge was withdrawn, and a run was
// spent finding that main does not have the package and passes. So before a
// check is filed as the target's the harness confirms it (redTargetOwner, from
// yoyodyne-c02): the forge is asked how the same check ended on the target's
// own head, and a check that passes there is the change's. Where the forge
// cannot say — the read fails, or the check has not run or not finished on the
// target's head — the failing job's log and annotations are read instead, and
// any file the change adds or modifies, or the directory one sits in, that they
// name makes the failure the change's. A failure that is the change's is handed
// back to be repaired, as a check failing on a file the change touches is, and
// nothing is filed against the target.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ActionWaitingOnTarget reports a queued merge the sweep withdrew because its
// checks failed on the target rather than on the change: the failure is filed as
// the target's, and the publication waits on the item filed for it.
const ActionWaitingOnTarget ReconcileAction = "waiting-on-target"

// ReconcileJobLogs reads the failing step's lines from the forge's log of the
// job behind a check run. It is satisfied by publish.GitHub.
type ReconcileJobLogs interface {
	JobLogTail(ctx context.Context, checkRun int64, lines int) (string, error)
}

// ReconcileTargetChecks reads how each check ended on the commit the target
// branch points at. It is satisfied by publish.GitHub.
type ReconcileTargetChecks interface {
	BranchChecks(ctx context.Context, branch string) (publish.BranchCheckReading, error)
}

// redTargetLogLines is how much of a failing job's log an item carries.
const redTargetLogLines = 60

// redTargetOwnerLogLines is how much of a failing job's log is read for the
// change's files before its failure is filed as the target's. It is more than
// the item carries because a test runner's summary of which packages failed can
// sit well above the line the step failed on.
const redTargetOwnerLogLines = 400

// redTargetOwnership is whose failure the checks red on a level head are, as
// redTargetOwner decided it.
type redTargetOwnership struct {
	// Change says why the failure is the change's own, and is empty where every
	// failing check is the target's.
	Change string
	// Confirmed says, per failing check, how it was found to be the target's.
	Confirmed map[string]string
}

// redTargetOwner decides whether checks failing on a head level with its target,
// on no file the change touches, are the target's failure or the change's own.
// A check is the target's where the forge reports it red on the target's own
// head too, and the change's where it passes there. Where the forge cannot say
// either — the target's checks cannot be read, or this check has not run or
// finished on its head — the check's annotations and the tail of its job's log
// are read, and any file the change adds or modifies, or the directory one sits
// in, that they name makes the failure the change's. files are the files the
// change touches, as the forge lists them.
func (r Reconciler) redTargetOwner(ctx context.Context, target string, checks runstate.PullRequestChecks, files []string) redTargetOwnership {
	ownership := redTargetOwnership{Confirmed: make(map[string]string, len(checks.Failing))}
	var onTarget *publish.BranchCheckReading
	unread := "nothing is wired to this harness to read the target's own checks"
	if r.TargetChecks != nil {
		reading, err := r.TargetChecks.BranchChecks(ctx, target)
		if err == nil {
			onTarget = &reading
		} else {
			unread = fmt.Sprintf("the forge could not say how the checks ended on %s's own head (%s)", target, oneline.Bound(err.Error(), 200))
		}
	}
	var own []string
	for _, failing := range checks.Failing {
		why := unread
		if onTarget != nil {
			head := nonEmpty(shortCommit(onTarget.HeadCommit), "its head")
			switch {
			case namedCheck(onTarget.Failing, failing.Name):
				ownership.Confirmed[failing.Name] = fmt.Sprintf("The forge reports %s red on %s's own head, %s, as well.", failing.Name, target, head)
				continue
			case namedCheck(onTarget.Passing, failing.Name):
				own = append(own, fmt.Sprintf("%s passes on %s's own head, %s", failing.Name, target, head))
				continue
			case namedCheck(onTarget.Pending, failing.Name):
				why = fmt.Sprintf("%s has not finished on %s's own head, %s", failing.Name, target, head)
			default:
				why = fmt.Sprintf("%s has not run on %s's own head, %s", failing.Name, target, head)
			}
		}
		if named := r.changeNamedByCheck(ctx, failing, files); len(named) > 0 {
			own = append(own, fmt.Sprintf("%s, and the forge's account of %s names %s, which this change adds or modifies", why, failing.Name, strings.Join(named, ", ")))
			continue
		}
		ownership.Confirmed[failing.Name] = fmt.Sprintf("Whether %s is red on %s's own head was not confirmed, because %s; the forge's log of it and its annotations name no file this change adds or modifies, nor the directory of one.", failing.Name, target, why)
	}
	ownership.Change = strings.Join(own, "; ")
	return ownership
}

func namedCheck(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// changeNamedByCheck is the files the change touches, and the directories they
// sit in, that a failing check's annotations or the tail of its job's log name.
// A test failure reported per package names the package rather than the file
// the failing test is in, so the directory counts as much as the file does. A
// directory at the top of the repository counts only where it is named as part
// of a path, so a word that happens to share its name is not read as it.
func (r Reconciler) changeNamedByCheck(ctx context.Context, failing runstate.FailingCheck, files []string) []string {
	var text strings.Builder
	for _, annotation := range failing.Annotations {
		text.WriteString(annotation.Message)
		text.WriteByte('\n')
	}
	if r.JobLogs != nil && failing.CheckRun > 0 {
		if tail, err := r.JobLogs.JobLogTail(ctx, failing.CheckRun, redTargetOwnerLogLines); err == nil {
			text.WriteString(tail)
		}
	}
	account := text.String()
	if strings.TrimSpace(account) == "" {
		return nil
	}
	seen := map[string]bool{}
	var named []string
	consider := func(candidate string, asPath bool) {
		if candidate == "" || candidate == "." || candidate == "/" || seen[candidate] {
			return
		}
		seen[candidate] = true
		if mentionsPath(account, candidate, asPath) {
			named = append(named, candidate)
		}
	}
	for _, file := range files {
		file = strings.Trim(strings.TrimSpace(file), "/")
		consider(file, false)
		directory := path.Dir(file)
		consider(directory, !strings.Contains(directory, "/"))
	}
	sort.Strings(named)
	if len(named) > runstate.MaxRecordedCheckPaths {
		named = named[:runstate.MaxRecordedCheckPaths]
	}
	return named
}

// mentionsPath reports text naming a repository path whole: not as part of a
// longer name on either side. asPath asks, further, that it be named as part of
// a longer path — a slash beside it — which is how a directory at the top of the
// repository is told apart from an ordinary word.
func mentionsPath(text, name string, asPath bool) bool {
	for from := 0; ; {
		index := strings.Index(text[from:], name)
		if index < 0 {
			return false
		}
		start := from + index
		end := start + len(name)
		before, after := byte(' '), byte(' ')
		if start > 0 {
			before = text[start-1]
		}
		// A full stop ending a sentence ends the name too.
		if end < len(text) && (text[end] != '.' || (end+1 < len(text) && pathNameByte(text[end+1]))) {
			after = text[end]
		}
		if !pathNameByte(before) && !pathNameByte(after) && (!asPath || before == '/' || after == '/') {
			return true
		}
		from = start + 1
	}
}

// pathNameByte reports a byte that continues a file or directory name.
func pathNameByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' ||
		('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

// redTargetMarker is the line an item filed for a check red on the target
// carries in its notes, naming the check and the branch, which is what a later
// request meeting the same check finds it by. It is the red landing's marker in
// shape, and a different line, because a landing check is a command the harness
// ran and this is a check the forge ran.
func redTargetMarker(target, check string) string {
	return "Red forge check: " + check + " on " + target
}

// waitOnRedTarget settles a queued merge whose checks fail on a head level with
// its target, on no file its change touches, as the target's failure. dropped is
// a merge the forge already stopped holding, which has nothing to withdraw.
// files are the files the change touches, as the forge lists them.
//
// The failure is confirmed as the target's first (redTargetOwner). One that is
// the change's own is handed back to be repaired, as a check failing on a file
// the change touches is, and nothing is filed against the target.
//
// Nothing is written until everything the wait names exists. The work item is
// read for the goal it served, and every failing check is filed or found, before
// the merge is withdrawn; a failure at any of those leaves the merge where it was
// and the next sweep asks again, finding by its marker whatever this one filed.
// A sweep wired with nothing to file through hands the merge back as it always
// did.
func (r Reconciler) waitOnRedTarget(ctx context.Context, state runstate.State, checks runstate.PullRequestChecks, files []string, dropped bool) (Reconciliation, error) {
	ownership := r.redTargetOwner(ctx, state.Integration.TargetBranch, checks, files)
	return r.waitOnRedTargetOwned(ctx, state, checks, ownership, dropped)
}

// waitOnRedTargetOwned uses the attribution already read for this head. A
// dropped merge needs that reading before replay eligibility is considered;
// reading it again could give a different answer about the same settlement.
func (r Reconciler) waitOnRedTargetOwned(ctx context.Context, state runstate.State, checks runstate.PullRequestChecks, ownership redTargetOwnership, dropped bool) (Reconciliation, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	describe := checks.Describe(target)
	if ownership.Change != "" {
		published.Checks = &checks
		state.PullRequest = &published
		return r.handBackFailedChange(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail on this change with its head level with %s: %s; so the failure is this change's own rather than %s's, and nothing was filed against %s: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs its change repaired",
			published.Number, target, ownership.Change, target, target, describe))
	}
	if r.Filer == nil {
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail with its head level with %s, so nothing but this change differs from the target and bringing it up to date would change nothing: %s. Nothing is wired to this harness to file the target's failure as its own item, so the harness withdrew the queued merge rather than leave a red change queued, and the pull request needs a person",
			published.Number, target, describe))
	}
	left := func(why string) (Reconciliation, error) {
		result := reconciliationOf(state, ActionQueued)
		stays := "the merge is left queued"
		if dropped {
			stays = "the record is left as it stands"
		}
		result.Detail = fmt.Sprintf("the forge's checks on pull request %d fail on %s itself rather than on this change (%s), and %s, so %s and the next sweep asks again",
			published.Number, target, describe, why, stays)
		return result, nil
	}
	item, err := r.Tracker.Show(ctx, state.WorkItemID)
	if err != nil {
		return left(fmt.Sprintf("the goal %s served could not be read to file the target's failure under (%v)", state.WorkItemID, err))
	}
	statement, _ := goal.NamedIn(item.Notes)
	waiting := runstate.TargetRed{At: r.clock().Now(), TargetBranch: target, HeadCommit: checks.HeadCommit}
	accounts := r.checkAccounts(ctx, checks)
	for index, failing := range checks.Failing {
		filed, err := r.fileRedTargetCheck(ctx, state, failing, target, statement, ownership.Confirmed[failing.Name], accounts[index])
		if err != nil {
			return left(fmt.Sprintf("the failure of %s could not be filed (%v)", failing.Name, err))
		}
		waiting.Checks = append(waiting.Checks, filed)
	}
	if !dropped {
		if err := r.Checks.DisableAutoMerge(ctx, published.Number); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("withdraw the queued merge of pull request %d, whose checks fail on %s itself, for run %s: %w", published.Number, target, state.RunID, err)
		}
	}
	reason := fmt.Sprintf("the forge's checks on pull request %d fail with its head level with %s on no file this change touches, so the failure is %s's rather than this change's: %s. The harness filed it as the target's and the publication %s; it moves by itself once those close — re-armed on a level head whose checks pass, or brought up to date from the kept branch where the fix left the head behind — and nothing here needs a person",
		published.Number, target, target, describe, waiting.Describe())
	// The work item is told, and made to wait on the filed items, before the
	// record changes: a sweep that stops in between leaves the merge recorded as
	// queued, and the next one finds it withdrawn, files nothing new, and writes
	// the record then.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderRedTargetNotes(state, reason, accounts)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the wait of run %s on %s's red check: %w", state.RunID, target, err)
	}
	var problems []string
	for _, blocker := range waiting.WaitingOn() {
		if err := r.Tracker.AddBlocker(ctx, state.WorkItemID, blocker); err != nil {
			problems = append(problems, fmt.Sprintf("%s could not be made to wait on %s in the tracker (%v); the publication's own record still holds it", state.WorkItemID, blocker, err))
		}
	}
	published.MergeQueued = false
	published.Checks = &checks
	published.TargetRed = &waiting
	state.PullRequest = &published
	state.PublishFailure = oneline.Bound(reason, runstate.MaxSelectionReasonBytes)
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the wait of run %s on %s's red check: %w", state.RunID, target, err)
	}
	result := reconciliationOf(state, ActionWaitingOnTarget)
	result.Detail = reason
	if len(problems) > 0 {
		result.Detail += "; " + strings.Join(problems, "; ")
	}
	return result, nil
}

// fileRedTargetCheck files the item one check red on the target is, or finds the
// one already open for it and notes this request on it. It is fileRedLanding's
// filing for a check the forge ran: priority 0, a bug, under the goal the run's
// item served, with the harness's own words in the fields the protected-path
// gate reads and what the forge's log said only in the notes.
//
// confirmed says how the check was found to be the target's, and goes into the
// notes, beside the forge's own account, so whoever works the item knows whether the forge reported it red
// on the target's own head or the harness could only find nothing of the change
// in its log.
func (r Reconciler) fileRedTargetCheck(ctx context.Context, state runstate.State, failing runstate.FailingCheck, target, statement, confirmed, account string) (runstate.TargetRedCheck, error) {
	published := state.PullRequest
	marker := redTargetMarker(target, failing.Name)
	head := shortCommit(published.HeadCommit)
	existing, err := openItemMarked(ctx, r.Filer, marker)
	if err != nil {
		return runstate.TargetRedCheck{}, fmt.Errorf("read whether an item is already open for it: %w", err)
	}
	if existing != "" {
		note := fmt.Sprintf("Red again on %s: pull request %d of %s (%s), level with %s at %s, failed %s on no file its change touches. Its merge waits on this item.",
			target, published.Number, state.WorkItemID, state.RunID, target, head, failing.Name)
		if _, err := r.Tracker.RecordOutcome(ctx, existing, note); err != nil {
			return runstate.TargetRedCheck{}, fmt.Errorf("tell the open item %s about pull request %d: %w", existing, published.Number, err)
		}
		return runstate.TargetRedCheck{Name: failing.Name, WorkItem: existing, FiledEarlier: true}, nil
	}
	ended := nonEmpty(failing.Conclusion, "no conclusion reported")
	description := fmt.Sprintf("The forge's check %s is red on %s itself: pull request %d of %s (%s), queued to merge with its head at %s level with %s, failed it on no file its change touches, so nothing but that change differed from %s and the failure is the target's.\n\n"+
		"The harness withdrew that merge and it waits on this item, as every request queued behind it that meets the same check does. The forge ended the check as: %s. What the forge's log of the job said is in this item's notes; the run is %s on the forge.",
		failing.Name, target, published.Number, state.WorkItemID, state.RunID, head, target, target, ended, published.URL)
	notes := fmt.Sprintf("Filed by the harness for %s red on %s, met by pull request %d of %s (%s) at %s, as a red landing files its own item.\n%s",
		failing.Name, target, published.Number, state.WorkItemID, state.RunID, head, marker)
	if confirmed != "" {
		notes += "\n" + confirmed
	}
	if statement != "" {
		notes += "\n\n" + goal.Note(statement)
	}
	notes += "\n\n" + account
	priority := 0
	created, err := r.Filer.Create(ctx, beads.NewWorkItem{
		Title:       fmt.Sprintf("Red forge check on %s: %s fails with the head level with %s, met by pull request %d", target, failing.Name, target, published.Number),
		Description: description,
		Type:        "bug",
		Notes:       notes,
		Priority:    &priority,
		Origin:      domain.WorkItemOrigin{Asker: domain.AskerHarness},
	})
	if err != nil {
		return runstate.TargetRedCheck{}, err
	}
	return runstate.TargetRedCheck{Name: failing.Name, WorkItem: created.ID}, nil
}

// checkAccount is the forge's account of one failing check, read under the
// harness's own forge access: its name, how the forge ended it, the commit it
// ran on, the forge's own annotations, the failing step's lines from the job's
// log, and a link to that log. A log the harness could not read says why, and
// a token the forge would not let read it is named as the operator's to grant.
// The log is quoted so no line of it is read as one of the harness's.
//
// It is what a merge withdrawn or handed back over a forge check carries onto
// the item, so whoever works the item works from it: a developer run may not
// reach the forge, and until yoyodyne-ifd.429.35 a run given such an item spent
// itself finding that out while a person went and read the log.
func (r Reconciler) checkAccount(ctx context.Context, failing runstate.FailingCheck, head string) string {
	account := fmt.Sprintf("How the forge ended %s: %s.", failing.Name, nonEmpty(failing.Conclusion, "no conclusion reported"))
	if head != "" {
		account += " Commit: " + head + "."
	}
	if failing.URL != "" {
		account += " Log: " + failing.URL
	} else {
		account += " The forge gave no link to the check run."
	}
	if len(failing.Annotations) > 0 {
		lines := make([]string, 0, len(failing.Annotations))
		for _, annotation := range failing.Annotations {
			lines = append(lines, "- "+annotation.Describe())
		}
		account += "\nThe forge's annotations:\n" + strings.Join(lines, "\n")
	} else if len(failing.Paths) > 0 {
		account += "\nIts annotations named: " + strings.Join(failing.Paths, ", ") + "."
	}
	switch {
	case r.JobLogs == nil:
		return account + "\nNothing is wired to this harness to read the job's log."
	case failing.CheckRun <= 0:
		return account + "\nThe forge named no job for it, so there is no log to read."
	}
	tail, err := r.JobLogs.JobLogTail(ctx, failing.CheckRun, redTargetLogLines)
	switch {
	case errors.Is(err, publish.ErrForgeAccessRefused):
		return account + fmt.Sprintf("\nThe forge would not let the harness's token read this job's log (%s). Granting that token read access to the repository's Actions is the operator's; until it is granted this record is all there is of the log, and nobody working this item is asked to fetch it.",
			oneline.Bound(err.Error(), 400))
	case err != nil:
		return account + fmt.Sprintf("\nIts log could not be read: %s.", oneline.Bound(err.Error(), 400))
	case strings.TrimSpace(tail) == "":
		return account + "\nIts log was empty."
	}
	return account + fmt.Sprintf("\n\nThe failing step's lines from the forge's log of the job, at most %d (check run %d):\n\n%s", redTargetLogLines, failing.CheckRun, quotedOutput(tail))
}

// checkAccounts is checkAccount for every failing check of a reading, in order.
func (r Reconciler) checkAccounts(ctx context.Context, checks runstate.PullRequestChecks) []string {
	accounts := make([]string, 0, len(checks.Failing))
	for _, failing := range checks.Failing {
		accounts = append(accounts, r.checkAccount(ctx, failing, checks.HeadCommit))
	}
	return accounts
}

// renderForgeAccount is the note the forge's accounts make on the item.
func renderForgeAccount(accounts []string) string {
	if len(accounts) == 0 {
		return ""
	}
	return "The forge's account of the failing checks, read by the harness under its own forge access. Work from this record: nothing here asks anybody to fetch the forge.\n\n" +
		strings.Join(accounts, "\n\n")
}

// openItemMarked is the identifier of an unfinished item whose notes carry the
// marker line, or nothing. It reads the queue's three unfinished statuses, as a
// red landing's lookup does, because an item somebody has claimed or that is
// blocked is still the item that answers the check being red.
func openItemMarked(ctx context.Context, filer WorkFiler, marker string) (string, error) {
	for _, status := range []string{"open", "in_progress", "blocked"} {
		items, err := filer.List(ctx, status)
		if err != nil {
			return "", err
		}
		for _, item := range items {
			for _, line := range strings.Split(item.Notes, "\n") {
				if strings.TrimSpace(line) == marker {
					return item.ID, nil
				}
			}
		}
	}
	return "", nil
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// renderRedTargetNotes tells the work item its queued merge waits on the
// target's red check rather than on anything about the change.
func renderRedTargetNotes(state runstate.State, reason string, accounts []string) string {
	lines := []string{
		"Yoyodyne withdrew the merge this run left queued with the forge, because its checks fail on the target branch itself rather than on this change.",
		"Outcome: " + reason,
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"Nothing about this item needs a decision: the change stays reviewed on its kept branch, and the harness takes the merge up again once the items it waits on close.",
	}
	if account := renderForgeAccount(accounts); account != "" {
		lines = append(lines, "", account)
	}
	return strings.Join(lines, "\n")
}

// RedTargetResumption is what the sweep did about one publication waiting on
// its target's red check.
type RedTargetResumption struct {
	Finding        *readmodel.Attention `json:"finding,omitempty"`
	FindingProblem string               `json:"finding_problem,omitempty"`
	RunID          string               `json:"run_id"`
	WorkItemID     string               `json:"work_item_id"`
	// Action is what came of it: still waiting, brought up to date, filed
	// again, or left for the watch to re-arm.
	Action  ReconcileAction `json:"action"`
	Detail  string          `json:"detail,omitempty"`
	Failure string          `json:"failure,omitempty"`
}

// ResumeRedTargets takes up every publication waiting on its target's red check
// whose items have all closed. The fix having landed leaves the head behind the
// target, and that is brought up to date from the kept branch — checked,
// reviewed, and queued again, as a queued head behind its target is, and hosted
// by ContinueUpdates. A head still level whose checks still fail on no file the
// change touches is filed again as the target's, since the items that answered
// it closed with the check still red. A head level and passing is left for the
// watch's re-arm carry-out, which arms it with nobody deciding.
//
// A publication with any item still open waits, and says so; nothing is asked of
// the forge for it.
func (r Reconciler) ResumeRedTargets(ctx context.Context) ([]RedTargetResumption, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	if r.Checks == nil {
		return nil, nil
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return nil, fmt.Errorf("read the recorded runs to find the publications waiting on a red target: %w", err)
	}
	var results []RedTargetResumption
	for _, candidate := range recorded {
		if !candidate.WaitingOnRedTarget() {
			continue
		}
		result := RedTargetResumption{RunID: candidate.RunID, WorkItemID: candidate.WorkItemID}
		open, err := r.openRedTargetItems(ctx, *candidate.PullRequest.TargetRed)
		if err != nil {
			result.Action = ActionUnsettled
			result.Failure = err.Error()
			results = append(results, result)
			continue
		}
		if len(open) > 0 {
			result.Action = ActionWaitingOnTarget
			result.Detail = fmt.Sprintf("pull request %d still waits on %s", candidate.PullRequest.Number, strings.Join(open, ", "))
			results = append(results, result)
			continue
		}
		reconciliation, err := r.resumeRedTarget(ctx, candidate.RunID)
		result.Action, result.Detail = reconciliation.Action, reconciliation.Detail
		if err != nil {
			result.Failure = err.Error()
		}
		results = append(results, result)
	}
	for index := range results {
		result := &results[index]
		if result.Action != ActionHeld {
			result.Finding, result.FindingProblem = r.recordReconcileFinding(ctx, result.RunID, runstate.ReconcileRedTarget, result.Failure)
		}
	}
	return results, ctx.Err()
}

// resumeRedTarget reads the checks of one publication whose items have closed,
// under the run's own lease, and decides on them.
func (r Reconciler) resumeRedTarget(ctx context.Context, runID string) (Reconciliation, error) {
	state, lease, err := r.Store.AdoptRun(ctx, runID)
	if err != nil {
		return Reconciliation{RunID: runID, Action: ActionUnsettled}, fmt.Errorf("take run %s to take up its wait on a red target: %w", runID, err)
	}
	defer func() { _ = lease.Release() }()
	if !state.WaitingOnRedTarget() {
		result := reconciliationOf(state, ActionQueued)
		result.Detail = "the publication stopped waiting on its target while this sweep was reading it, so it is left as it stands"
		return result, nil
	}
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	reading, err := r.Checks.Checks(ctx, published.Number, target)
	if err != nil {
		result := reconciliationOf(state, ActionWaitingOnTarget)
		result.Detail = fmt.Sprintf("every item pull request %d waited on is closed, and its checks could not be read (%v), so it is left waiting and the next sweep asks again", published.Number, err)
		return result, r.recordUnreadChecks(state, err)
	}
	checks := recordedChecks(reading, r.clock().Now())
	if prior := published.Checks; prior != nil && prior.HeadCommit == checks.HeadCommit {
		checks.Reruns = prior.Reruns
		checks.RerunChecks = append([]int64(nil), prior.RerunChecks...)
	}
	// A merge handed back from here carries this reading's account onto the
	// item, not the one the wait began on.
	handBack := func(reason string) (Reconciliation, error) {
		published.Checks = &checks
		state.PullRequest = &published
		return r.handBackRedMerge(ctx, state, reason)
	}
	switch {
	case checks.ChangeFails():
		published.Checks = &checks
		state.PullRequest = &published
		return r.handBackFailedChange(ctx, state, fmt.Sprintf(
			"the items pull request %d waited on for %s's red check are closed, and its checks now fail on this change: %s. The pull request needs its change repaired",
			published.Number, target, checks.Describe(target)))
	case checks.BehindBy > 0:
		if refusal := unreplayable(state); refusal != "" {
			return handBack(fmt.Sprintf(
				"the items pull request %d waited on for %s's red check are closed and its head is behind %s, and the harness cannot bring it up to date: %s: %s. The pull request needs a person",
				published.Number, target, target, refusal, checks.Describe(target)))
		}
		published.Checks = &checks
		state.PullRequest = &published
		result := reconciliationOf(state, ActionWaitingOnTarget)
		result.Detail = fmt.Sprintf("every item pull request %d waited on for %s's red check is closed; %s", published.Number, target, checks.Describe(target))
		return r.updateQueuedHead(ctx, state, result, true)
	case checks.Red() && !checks.FailedInTheJob():
		return r.waitOnRedTarget(ctx, state, checks, reading.Files, true)
	case checks.Red():
		return r.rerunEndedJobsAfterRedTarget(ctx, state, checks)
	default:
		published.Checks = &checks
		state.PullRequest = &published
		state.UpdatedAt = r.clock().Now()
		if err := r.Store.Save(state); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the checks of pull request %d on run %s: %w", published.Number, state.RunID, err)
		}
		result := reconciliationOf(state, ActionWaitingOnTarget)
		result.Detail = fmt.Sprintf("every item pull request %d waited on for %s's red check is closed and its head is level with %s (%s), so the watch's re-arm carry-out arms its merge at its next pull",
			published.Number, target, target, checks.Describe(target))
		return result, nil
	}
}

// rerunEndedJobsAfterRedTarget decides a closed wait whose level head is red
// only on jobs the forge ended itself — cancelled, timed out, or never started.
// Nothing in the tree decided those, so they are neither the target's failure to
// file again nor a head the re-arm can arm: they are run again on the same head
// within runstate.MaxCheckReruns, as a merge still queued has its ended jobs
// run again, and the wait stands meanwhile, so the next sweep reads the re-run.
// Ended again past the bound, or refused a re-run, the merge is handed back
// saying the forge ended it, as a queued one is.
func (r Reconciler) rerunEndedJobsAfterRedTarget(ctx context.Context, state runstate.State, checks runstate.PullRequestChecks) (Reconciliation, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	if prior := published.Checks; prior != nil && prior.HeadCommit == checks.HeadCommit {
		checks.Reruns = prior.Reruns
		checks.RerunChecks = append([]int64(nil), prior.RerunChecks...)
	}
	record := func(detail string) (Reconciliation, error) {
		published.Checks = &checks
		state.PullRequest = &published
		state.UpdatedAt = r.clock().Now()
		if err := r.Store.Save(state); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the checks of pull request %d on run %s: %w", published.Number, state.RunID, err)
		}
		result := reconciliationOf(state, ActionWaitingOnTarget)
		result.Detail = detail
		return result, nil
	}
	answered := fmt.Sprintf("every item pull request %d waited on for %s's red check is closed; %s", published.Number, target, checks.Describe(target))
	if checks.AwaitingRerun() {
		return record(answered + "; the forge has not yet started the re-run the harness asked for, so it goes on waiting and the next sweep reads it")
	}
	refused := ""
	if checks.Reruns < runstate.MaxCheckReruns {
		if refused = r.rerunFailedJobs(ctx, checks); refused == "" {
			checks.RecordRerun()
			return record(fmt.Sprintf("%s; the forge ended the failed jobs before any step failed, so the harness asked it to run them again (%d of %d on this head) and the next sweep reads the re-run",
				answered, checks.Reruns, runstate.MaxCheckReruns))
		}
	}
	why := fmt.Sprintf("were ended by the forge before any step failed, and ended that way again on each of %d re-run(s) of this head", checks.Reruns)
	if refused != "" {
		why = fmt.Sprintf("were ended by the forge before any step failed, and the forge would not run them again (%s)", refused)
	}
	published.Checks = &checks
	state.PullRequest = &published
	return r.handBackRedMerge(ctx, state, fmt.Sprintf(
		"the items pull request %d waited on for %s's red check are closed, and its checks %s: %s. The forge's account of each, read under the harness's forge access, is in this item's notes, and the pull request needs a person",
		published.Number, target, why, checks.Describe(target)))
}

// openRedTargetItems is the items a wait on a red target still waits on.
func (r Reconciler) openRedTargetItems(ctx context.Context, waiting runstate.TargetRed) ([]string, error) {
	var open []string
	for _, id := range waiting.WaitingOn() {
		item, err := r.Tracker.Show(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read whether %s, filed for a red check on %s, is closed: %w", id, waiting.TargetBranch, err)
		}
		if !strings.EqualFold(item.Status, "closed") {
			open = append(open, id)
		}
	}
	return open, nil
}
