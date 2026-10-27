// Package publish opens and inspects the pull requests a run's work is
// published through. It is a harness-owned adapter over the forge CLI for the
// same reason the Git operations are harness-owned: the developer's phase is
// what causes a pull request to exist and the reviewer's verdict is what causes
// it to be merged, but the harness is what invokes the CLI, and neither role is
// given a credential, a tool, or a request to invoke it themselves.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	defaultBinary    = "gh"
	defaultGitBinary = "git"
	defaultRemote    = "origin"
	defaultTimeout   = 60 * time.Second
	// maxListedOpenRequests bounds how many open pull requests one listing
	// reads. The forge's own default is thirty, which is fewer than the
	// repository has been found holding unnoticed; a repository with more open
	// requests than this reaches the rest once the first are closed.
	maxListedOpenRequests = 200
	// maxBodyBytes bounds the description carried onto the forge. A pull request
	// body summarizes a run; it is not a place to republish everything the run
	// produced.
	maxBodyBytes = 16 << 10
	// maxTitleBytes keeps a title a title.
	maxTitleBytes = 200
)

// Availability reports whether the forge CLI can actually be used. It is
// checked before a run claims anything, so a project that asked for pull
// requests never discovers halfway through that it cannot open one.
type Availability struct {
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`
}

// PullRequest is one pull request as the forge reports it.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state,omitempty"`
	Merged bool   `json:"merged,omitempty"`
	// HeadCommit is the commit the request currently carries, which is what the
	// forge would merge. It is reported so a caller about to ask for a merge can
	// tell a request that still carries the commit it was opened for from one that
	// has moved: the merge itself pins the head as well, and this is what lets the
	// question be answered before anything is asked of the forge.
	HeadCommit string `json:"head_commit,omitempty"`
	// AutoMerge reports a merge the forge is holding for this request: it will
	// perform it once the base branch's requirements are met, or its merge queue
	// has taken the request and will land it. It is what tells a queued merge
	// that has not landed yet from one the forge dropped, which is otherwise the
	// same observation — an open, unmerged request.
	AutoMerge bool `json:"auto_merge,omitempty"`
	// MergeCommit is the commit the forge recorded as the merge of this request,
	// and is empty on a request the forge has not merged or answered about
	// without naming one. It is what lets a merge be confirmed after other merges
	// have landed on top of it: the remote target's tip is then somebody else's
	// merge commit, and this is the one that carried this request.
	MergeCommit string `json:"merge_commit,omitempty"`
	// HeadBranch and BaseBranch are the branch the request carries and the branch
	// it is opened against, as the forge names them. They are reported by the
	// listing of every open request, which is read without knowing any branch
	// in advance: a request found by its branch already knows its head.
	HeadBranch string `json:"head_branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// Request describes the pull request a published run branch must have open.
type Request struct {
	Head  string
	Base  string
	Title string
	Body  string
}

// CloseRequest names a publication to retire and the comment that says why. The
// comment is part of the request rather than optional: a pull request the
// harness closes without saying where its work went is the same unexplained
// state that closing it exists to end.
type CloseRequest struct {
	// Head is the branch the request carries. It is what the forge is asked
	// about before anything is closed, so a number that has stopped naming this
	// branch's request is refused rather than acted on.
	Head    string
	Number  int
	Comment string
}

// Closure is what closing a publication did. A request the forge already has
// closed reports Closed false and is not a failure: whoever closed it — a
// person, or an earlier sweep — already did this, and repeating it must add
// neither a second comment nor a second report.
type Closure struct {
	Closed bool
	State  string
	Merged bool
}

// MergeMethod names how the forge brings a pull request onto its base branch.
// The three produce different remote histories, so a caller states which one it
// means rather than inheriting whatever the repository happens to default to.
//
// Only one of them preserves the commits that were reviewed. A squash replaces
// them with a single commit the head never had, and GitHub's rebase always
// updates committer information and mints new SHAs — even for a request that
// needs no rebasing at all — so both leave the base carrying copies of the work
// rather than the work itself.
type MergeMethod string

const (
	// MergeCommit joins the request onto its base under a merge commit, which
	// keeps the request's own commits on the base exactly as they were pushed:
	// the merge commit's second parent is the head commit itself.
	MergeCommit MergeMethod = "merge"
	// MergeRebase and MergeSquash both rewrite what they merge, and are named
	// here because the method is part of what a caller decides rather than
	// because anything in this repository asks for them.
	MergeRebase MergeMethod = "rebase"
	MergeSquash MergeMethod = "squash"
)

// MergeRequest names the pull request to merge, the commit it must still be at,
// and the method to merge it by.
type MergeRequest struct {
	Number     int
	HeadCommit string
	Method     MergeMethod
}

// MergeResult is what the forge did with a merge request. The harness asks for
// the merge to happen when the base branch's requirements are met rather than
// now, so the answer has to say which of the two the forge did: a queued merge
// completes minutes later, without the harness watching, and a caller that
// assumed otherwise would report a publication as unfinished on every protected
// branch.
type MergeResult struct {
	// Queued reports a merge the forge accepted and will perform itself once the
	// pull request's requirements are met. A result that is not queued is a
	// merge the forge performed while it was asked.
	Queued bool
}

// AutoMergeUnavailable reports a repository that does not offer the queued
// merge the harness asks for, for a pull request that needs one. It is distinct
// from a refusal because the remedy is a repository setting rather than an
// unmet requirement of this change: a repository whose base branch holds the
// request back and whose settings forbid auto-merge cannot be published to at
// all, and the operator has to be told which setting to change rather than left
// with a merge that fails on every run.
//
// Both halves are required before it is reported. "Allow auto-merge" is off by
// default, and a repository that has it off and has nothing holding the request
// back is merged into immediately instead — refusing that one would break every
// project without branch protection, which is most of them.
type AutoMergeUnavailable struct {
	Number int
	// Status is the forge's merge state for the request, which is what says
	// there was something for a queued merge to wait for; Reason is what the
	// forge printed when it would not queue one.
	Status string
	Reason string
}

func (e AutoMergeUnavailable) Error() string {
	message := fmt.Sprintf("the forge cannot queue a merge for pull request %d, and it cannot merge now either", e.Number)
	if requirement := mergeRequirement(e.Status); requirement != "" {
		message += ": " + requirement
	}
	message += ": enable \"Allow auto-merge\" in the repository's settings, or take the unmet requirement out of the base branch's protection rules"
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

// MergeRefused reports a forge that declined to merge a pull request. It is a
// distinct error because a refusal is an answer about the repository's rules —
// a request that conflicts with the base branch, a merge method the repository
// forbids, a head commit that moved — rather than a harness failure, and an
// operator has to be told which requirement was unmet.
//
// A required check that has not finished is deliberately not one of these. The
// harness asks the forge to merge when the requirements are met rather than
// now, so a pending check is what that queued merge waits for; only a request
// the forge will not merge whenever it is asked reaches here.
type MergeRefused struct {
	Number int
	Method MergeMethod
	// Status is the forge's own merge state for the request, when it could be
	// read; Reason is what the forge printed when it refused.
	Status string
	Reason string
	// QueueRequired says the forge refused because the base branch's protection
	// requires the forge's own merge queue, so no direct merge is accepted
	// whatever the change is. That is a repository setting rather than anything
	// about the change, and the refusal names the setting
	// (MergeQueueRequiredRemedy).
	QueueRequired bool
}

// MergeQueueRequiredRemedy is what a person changes when the forge refuses a
// direct merge because the base branch requires its own merge queue.
const MergeQueueRequiredRemedy = "the target branch's protection rules require the forge's own merge queue, which accepts no direct merge: remove that requirement from the branch's protection rules so the harness can merge again"

func (e MergeRefused) Error() string {
	message := fmt.Sprintf("the forge refused to merge pull request %d with the %s method", e.Number, e.Method)
	if e.QueueRequired {
		message += ": " + MergeQueueRequiredRemedy
	} else if requirement := mergeRequirement(e.Status); requirement != "" {
		message += ": " + requirement
	}
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

// MergeQueueRequired reports a merge the forge refused because the base
// branch requires its own merge queue, wherever in err the refusal is wrapped.
func MergeQueueRequired(err error) bool {
	var refused MergeRefused
	return errors.As(err, &refused) && refused.QueueRequired
}

// mergeQueueRequired recognizes the forge saying a merge must go through its
// own merge queue. GitHub says so only in words ("Changes must be made through
// the merge queue"), and the CLI exits the same way it does for any other
// refusal, so the message is what is read.
func mergeQueueRequired(reason string) bool {
	normalized := strings.ToLower(reason)
	if !strings.Contains(normalized, "merge queue") {
		return false
	}
	return strings.Contains(normalized, "made through the merge queue") ||
		strings.Contains(normalized, "merge queue is required") ||
		strings.Contains(normalized, "requires a merge queue") ||
		strings.Contains(normalized, "requires the merge queue")
}

// mergeRequirement states in words what the forge's merge state says is unmet,
// so a refusal reads as the rule it is rather than as a status code. A state
// this does not recognize is left to the forge's own message, which is reported
// beside it either way.
func mergeRequirement(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "BLOCKED":
		return "the base branch's protection rules are not satisfied (BLOCKED)"
	case "BEHIND":
		return "the base branch moved ahead of the pull request (BEHIND)"
	case "DIRTY":
		return "the pull request conflicts with the base branch (DIRTY)"
	case "UNSTABLE":
		return "a required check has not succeeded (UNSTABLE)"
	case "DRAFT":
		return "the pull request is still a draft (DRAFT)"
	case "HAS_HOOKS":
		return "a repository hook rejected the merge (HAS_HOOKS)"
	case "UNKNOWN":
		return "the forge has not finished deciding whether the pull request can merge (UNKNOWN)"
	}
	return ""
}

// GitHub publishes through the `gh` CLI, which holds its own credentials the
// way the coding-agent CLIs hold theirs. The harness reports whether that
// authentication is present and never manages it.
type GitHub struct {
	Runner  execution.ProcessRunner
	Binary  string
	Dir     string
	Timeout time.Duration
	// Remote names the Git remote this client speaks about, so the forge CLI
	// acts on the repository the project configured rather than on whichever
	// one it would infer from the working directory. A checkout with more than
	// one remote is the case that makes inference wrong rather than merely
	// redundant. Pull requests are opened against this repository.
	Remote string
	// PushRemote names the Git remote run branches are pushed to, when that is
	// not Remote. It is what makes a pull request cross-repository: the branch
	// lives in the fork this names and the request is opened against Remote, so
	// a contributor without push access to the project's repository publishes
	// the same way anyone with it does. Empty means the branch is on Remote,
	// which is every project that can push to what it publishes into.
	PushRemote string
	// GitBinary resolves the remote to a repository URL. It is a field so the
	// resolution is testable without a real checkout.
	GitBinary string
	// RedactValues are the sensitive environment values scrubbed from anything
	// the CLI prints, so a token in a diagnostic never reaches a report.
	RedactValues []string
}

func (g GitHub) Availability(ctx context.Context) (Availability, error) {
	if g.Runner == nil {
		return Availability{}, errors.New("pull request process runner is required")
	}
	version, err := g.exec(ctx, "--version")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Availability{Installed: false}, nil
		}
		return Availability{}, fmt.Errorf("check GitHub CLI version: %w", err)
	}
	if version.Status != execution.ProcessSucceeded {
		return Availability{Installed: false}, nil
	}
	availability := Availability{Installed: true, Version: firstLine(version.Stdout)}
	status, err := g.exec(ctx, "auth", "status")
	if err != nil {
		return availability, fmt.Errorf("check GitHub CLI authentication: %w", err)
	}
	availability.Authenticated = status.Status == execution.ProcessSucceeded
	return availability, nil
}

// Ensure returns the pull request for a published branch, opening one if the
// branch does not have it yet. It is called after every developer attempt, so
// it has to be idempotent: a repair attempt updates the pull request its first
// attempt opened rather than opening a second one for the same branch.
func (g GitHub) Ensure(ctx context.Context, request Request) (PullRequest, error) {
	if err := request.validate(); err != nil {
		return PullRequest{}, err
	}
	existing, found, err := g.find(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	if found {
		// A branch whose pull request is already closed or merged cannot receive
		// this run's work. Opening a second one would publish the same branch
		// twice and leave two answers about what is being reviewed.
		if existing.State != "" && !strings.EqualFold(existing.State, "OPEN") {
			return PullRequest{}, fmt.Errorf("pull request %d for branch %s is %s and cannot be republished into", existing.Number, request.Head, strings.ToLower(existing.State))
		}
		return existing, nil
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return PullRequest{}, err
	}
	head, err := g.headRef(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	created, err := g.exec(ctx, append([]string{"pr", "create"}, append(scope,
		"--base", request.Base,
		"--head", head,
		"--title", boundedTitle(request.Title),
		"--body", boundedBody(request.Body))...)...)
	if err != nil {
		return PullRequest{}, fmt.Errorf("open pull request for %s: %w", request.Head, err)
	}
	if created.Status != execution.ProcessSucceeded {
		return PullRequest{}, fmt.Errorf("open pull request for %s failed with exit code %d: %s", request.Head, created.ExitCode, g.redact(strings.TrimSpace(created.Stderr)))
	}
	opened, found, err := g.find(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	if !found {
		return PullRequest{}, fmt.Errorf("pull request for %s was created but cannot be found", request.Head)
	}
	return opened, nil
}

// Close retires a pull request whose work will never merge, leaving a comment
// that names where that work actually landed.
//
// It is the one write here that is not part of publishing a run, and it is
// narrow on purpose. The forge is asked about the request first, and only an
// open one is closed: a merged request is never touched, because closing one
// would be a claim about where the work is that the merge already answered, and
// a request already closed is reported as it stands rather than commented on
// again — a sweep that repeats every hour must not accumulate a comment an hour.
//
// The branch the request carries is deliberately left to the harness's own
// deletion rather than asked for here with `--delete-branch`. That deletion is a
// compare-and-swap on the exact commit the harness published, and the forge's is
// not: a branch somebody else moved must survive this.
func (g GitHub) Close(ctx context.Context, request CloseRequest) (Closure, error) {
	if err := request.validate(); err != nil {
		return Closure{}, err
	}
	existing, found, err := g.find(ctx, request.Head)
	if err != nil {
		return Closure{}, err
	}
	if !found {
		return Closure{}, fmt.Errorf("no pull request exists for branch %s", request.Head)
	}
	if existing.Number != request.Number {
		return Closure{}, fmt.Errorf("branch %s carries pull request %d rather than the %d this would close",
			request.Head, existing.Number, request.Number)
	}
	closure := Closure{State: existing.State, Merged: existing.Merged}
	if existing.Merged || !strings.EqualFold(existing.State, "OPEN") {
		return closure, nil
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return closure, err
	}
	closed, err := g.exec(ctx, append([]string{"pr", "close", strconv.Itoa(request.Number)}, append(scope,
		"--comment", boundedBody(request.Comment))...)...)
	if err != nil {
		return closure, fmt.Errorf("close pull request %d: %w", request.Number, err)
	}
	if closed.Status != execution.ProcessSucceeded {
		return closure, fmt.Errorf("close pull request %d failed with exit code %d: %s",
			request.Number, closed.ExitCode, g.redact(strings.TrimSpace(closed.Stderr)))
	}
	closure.Closed = true
	closure.State = "CLOSED"
	return closure, nil
}

// Merge asks the forge to merge a pull request when its requirements are met,
// which is what brings a promotion onto the remote target branch. The head
// commit is passed along so the forge refuses a request that moved since the
// harness published it: what merges must be the commit the run integrated and
// nothing else.
//
// The request is queued rather than demanded. A protected branch will not
// accept a merge until its required checks have passed, and those take longer
// than the moment between an approving verdict and this call, so asking to
// merge now is asking for a refusal. Queuing is what the branch protection is
// asking for, and the forge performs the merge itself once the requirements it
// names are satisfied. Administrator override is deliberately not offered here:
// merging with it would bypass the very checks the protection expresses, which
// removes the gate rather than satisfying it.
//
// A forge that will not queue the merge is asked whether it would make one now,
// because a request with nothing holding it back has nothing for a queue to
// wait for. That is the ordinary case for a repository without branch
// protection, and for one whose settings forbid queued merges — "Allow
// auto-merge" is off by default — so both are merged into immediately rather
// than reported as unpublishable.
//
// What is left is a request the forge will neither queue nor merge, and that
// comes back as an answer rather than a generic failure. AutoMergeUnavailable
// says the repository forbids queued merges and something is holding this
// request back, so the remedy is a setting; MergeRefused says the repository's
// rules are being applied to this request — a conflict with its base, a merge
// method the repository forbids — and names the requirement that was unmet.
func (g GitHub) Merge(ctx context.Context, request MergeRequest) (MergeResult, error) {
	if err := request.validate(); err != nil {
		return MergeResult{}, err
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return MergeResult{}, err
	}
	arguments := append([]string{"pr", "merge", strconv.Itoa(request.Number)}, append(scope,
		request.Method.flag(),
		"--match-head-commit", request.HeadCommit)...)
	// The queued request is the same command with one flag more, built as its own
	// slice so the arguments stay intact for the immediate merge below.
	queueing := append(append([]string{}, arguments...), "--auto")
	queued, err := g.exec(ctx, queueing...)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge pull request %d: %w", request.Number, err)
	}
	if queued.Status == execution.ProcessSucceeded {
		return MergeResult{Queued: true}, nil
	}
	// The forge declined to queue, and what that means depends on whether the
	// request has anything left to wait for. That question is asked before any
	// other, and asked twice over: the forge says so in words, and the merge state
	// it reports for the request says so in its own vocabulary. Either answer is
	// enough, which is what keeps a repository with nothing holding its requests
	// back — no branch protection, or every requirement already met — publishing
	// whatever the forge's reason for not queuing was, including "Allow
	// auto-merge" being off, which is GitHub's default.
	reason := g.redact(strings.TrimSpace(queued.Stderr))
	status := g.mergeState(ctx, request.Number)
	if !nothingLeftToWaitFor(reason) && !readyToMergeNow(status) {
		// Something is holding the request back and the forge will not hold the
		// merge for it. If that is the repository forbidding queued merges, the
		// remedy is a setting and the operator is told which; anything else is the
		// repository's rules being applied to this request.
		if autoMergeUnavailable(reason) {
			return MergeResult{}, AutoMergeUnavailable{Number: request.Number, Status: status, Reason: reason}
		}
		return MergeResult{}, MergeRefused{
			Number:        request.Number,
			Method:        request.Method,
			Status:        status,
			Reason:        reason,
			QueueRequired: mergeQueueRequired(reason),
		}
	}
	// The forge has nothing left to wait for, so it refused to queue a merge it
	// would perform immediately. Merging now is then what the queued request
	// asked for rather than a way around it: every requirement the base branch
	// names is already satisfied, and nothing is overridden to get there. A forge
	// that turns out to disagree refuses this merge too, and that refusal is what
	// gets reported.
	merged, err := g.exec(ctx, arguments...)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge pull request %d: %w", request.Number, err)
	}
	if merged.Status != execution.ProcessSucceeded {
		refusal := g.redact(strings.TrimSpace(merged.Stderr))
		return MergeResult{}, MergeRefused{
			Number:        request.Number,
			Method:        request.Method,
			Status:        g.mergeState(ctx, request.Number),
			Reason:        refusal,
			QueueRequired: mergeQueueRequired(refusal),
		}
	}
	return MergeResult{}, nil
}

// autoMergeUnavailable recognizes the forge saying that this repository does
// not allow queued merges. It is matched on the message because that is the
// only place the forge reports it: the CLI exits the same way it does for any
// other refusal, and an operator told "the merge was refused" would have no way
// to discover that a repository setting is what needs changing.
func autoMergeUnavailable(reason string) bool {
	normalized := normalizeAutoMerge(reason)
	return strings.Contains(normalized, "auto merge is not allowed") ||
		strings.Contains(normalized, "auto merge is not enabled") ||
		strings.Contains(normalized, "auto merge is disabled")
}

// nothingLeftToWaitFor recognizes the forge refusing to queue a merge because
// the pull request already meets every requirement its base branch names. A
// repository with no required checks answers this way to every queued merge, so
// treating it as a refusal would leave the unprotected case — the one that
// worked before queuing existed — unable to publish at all.
//
// It reads the forge's words, which can be reworded, so it is never the only
// thing asked: readyToMergeNow answers the same question from the merge state
// the forge reports for the request, and either answer is enough.
func nothingLeftToWaitFor(reason string) bool {
	normalized := strings.ToLower(reason)
	return strings.Contains(normalized, "clean status") ||
		strings.Contains(normalized, "not in the correct state") ||
		strings.Contains(normalized, "does not need auto-merge")
}

// readyToMergeNow reports a merge state with nothing outstanding on it. It is
// the forge's own vocabulary rather than its prose, so it survives a reworded
// message, and it is only ever consulted after the forge has already declined
// to queue a merge: a request that is clean has nothing for a queue to wait
// for. A state that could not be read answers no, which leaves the decision to
// what the forge said.
func readyToMergeNow(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	// Mergeable with every requirement met.
	case "CLEAN":
		return true
	// Mergeable, with checks that are running or failing but that no branch
	// protection requires. A repository with CI and no protection reports this
	// rather than CLEAN, and it is the ordinary case: nothing is holding the
	// request back, so there is nothing for a queue to wait for. Treating it as
	// outstanding would leave that repository unable to publish at all.
	case "UNSTABLE":
		return true
	// Mergeable, with repository commit hooks that will run on the merge.
	case "HAS_HOOKS":
		return true
	default:
		// BLOCKED, BEHIND, DIRTY, DRAFT and UNKNOWN all have something to
		// resolve before a merge, so the forge's own words decide.
		return false
	}
}

// normalizeAutoMerge folds the spellings the forge uses for the setting into
// one, so a message is matched on what it says rather than on how it hyphenates.
func normalizeAutoMerge(reason string) string {
	lowered := strings.ToLower(reason)
	lowered = strings.ReplaceAll(lowered, "auto-merge", "auto merge")
	return strings.ReplaceAll(lowered, "automerge", "auto merge")
}

// mergeState asks the forge what it thinks of a pull request it would not
// merge. It reports an empty state rather than an error: this runs only to
// explain a refusal that already happened, and a second failure must not
// replace the answer the forge already gave.
func (g GitHub) mergeState(ctx context.Context, number int) string {
	status, err := g.MergeState(ctx, number)
	if err != nil {
		return ""
	}
	return status
}

// MergeState reports the forge's own merge state for one pull request, in the
// forge's vocabulary — CLEAN, BLOCKED, DIRTY and the rest, which MergeRequirement
// states in words.
//
// It reports a failure rather than swallowing it, unlike the reading that
// explains a refusal: a caller deciding whether to ask the forge for anything is
// gating on this, and a state that could not be read must refuse rather than read
// as one with nothing outstanding.
func (g GitHub) MergeState(ctx context.Context, number int) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("pull request number %d is not a request", number)
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return "", err
	}
	viewed, err := g.exec(ctx, append([]string{"pr", "view", strconv.Itoa(number)}, append(scope, "--json", "mergeStateStatus")...)...)
	if err != nil {
		return "", fmt.Errorf("ask the forge for the merge state of pull request %d: %w", number, err)
	}
	if viewed.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("ask the forge for the merge state of pull request %d: exit code %d: %s",
			number, viewed.ExitCode, g.redact(strings.TrimSpace(viewed.Stderr)))
	}
	var reported struct {
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(viewed.Stdout)), &reported); err != nil {
		return "", fmt.Errorf("decode the merge state of pull request %d: %w", number, err)
	}
	return strings.TrimSpace(reported.MergeStateStatus), nil
}

// MergeRequirement states in words what a forge merge state says is unmet, and
// says nothing for a state with nothing outstanding. It is exported because a
// caller deciding whether a dropped merge is worth asking about again has to be
// able to say what stopped it, in the same words a refusal already uses.
func MergeRequirement(status string) string { return mergeRequirement(status) }

// AwaitsOnlyAPerson reports a merge state naming a requirement that only a
// person can satisfy, so nothing the harness may do would change the answer.
//
// The line is drawn where the harness's own reach ends. A conflict with the base
// branch, a request still in draft, a base branch that has moved ahead of it, and
// a protection rule the request does not satisfy are each somebody's work — and
// the harness does not merge past any of them, not with administrator privileges
// and not by asking again. What is left is a request the forge has nothing
// outstanding on, or has not finished deciding about, where a merge that was
// dropped was dropped for a cause that has passed: those are the ones repeating
// the request is worth anything for, and repeating it puts the forge's whole
// requirement machinery back in front of the merge rather than around it.
//
// A state that could not be read answers yes. A gate is what asks this, and the
// safe answer for a gate is the one that refuses.
func AwaitsOnlyAPerson(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "CLEAN", "UNSTABLE", "HAS_HOOKS", "UNKNOWN":
		return false
	default:
		// BLOCKED, BEHIND, DIRTY and DRAFT each name something a person supplies,
		// and so does a state this does not recognize: a forge vocabulary that grew
		// a word since must not have it read as nothing outstanding.
		return true
	}
}

// State reports what the forge currently says about a branch's pull request. It
// is how the harness confirms that the merge it asked for actually happened
// rather than assuming it did, and — for a merge the forge queued and has not
// performed yet — whether that merge is still waiting or was dropped.
func (g GitHub) State(ctx context.Context, head string) (PullRequest, error) {
	if err := validateArgument("head branch", head); err != nil {
		return PullRequest{}, err
	}
	found, exists, err := g.find(ctx, head)
	if err != nil {
		return PullRequest{}, err
	}
	if !exists {
		return PullRequest{}, fmt.Errorf("no pull request exists for branch %s", head)
	}
	// A request the forge's merge queue has taken is still one the forge holds a
	// merge for, and the listing cannot say so: the queue consumes the request's
	// auto-merge as it takes the request, and the request stays open until the
	// queue lands it. Read from the listing alone, that is an open request with
	// no merge held, which is exactly what a dropped merge looks like — and on
	// 2026-09-27 pull requests 832 and 834 were each handed to a person as
	// dropped while main's merge queue was landing them. So an open request with
	// no auto-merge is asked about the queue before it is reported, and a
	// question the forge does not answer is an error rather than a drop.
	if !found.Merged && !found.AutoMerge && strings.EqualFold(found.State, "OPEN") {
		queued, err := g.inMergeQueue(ctx, found.Number)
		if err != nil {
			return PullRequest{}, err
		}
		found.AutoMerge = queued
	}
	return found, nil
}

// MaxStatesPerQuery bounds how many branches one States query asks about. Each
// branch is one aliased field of a single GraphQL query, and the forge charges a
// query by the nodes it may return, so a bound keeps one query well inside the
// forge's per-query limits whatever the history holds.
const MaxStatesPerQuery = 50

// States reports what the forge says about the pull request of each branch
// named, in one GraphQL query rather than one listing per branch: the answer for
// each branch is the one State gives — the latest request the branch carries, in
// any state, with a request the merge queue has taken read as a merge the forge
// holds. A branch with no request is absent from the answer rather than an
// error, because in a batch it is one branch's answer and not the query's.
//
// A query the forge does not answer is an error for the whole batch: nothing in
// it was answered, and the caller asks again next time.
func (g GitHub) States(ctx context.Context, heads []string) (map[string]PullRequest, error) {
	answered := make(map[string]PullRequest, len(heads))
	if len(heads) == 0 {
		return answered, nil
	}
	if len(heads) > MaxStatesPerQuery {
		return nil, fmt.Errorf("asked about %d branches in one query, and one query asks about at most %d", len(heads), MaxStatesPerQuery)
	}
	variables := make([]string, 0, len(heads))
	fields := make([]string, 0, len(heads))
	args := []string{}
	for index, head := range heads {
		if err := validateArgument("head branch", head); err != nil {
			return nil, err
		}
		variables = append(variables, fmt.Sprintf("$h%d: String!", index))
		// The ordering is the listing's own, so the request answered for a branch
		// carrying more than one is the one State would have answered with.
		fields = append(fields, fmt.Sprintf(`h%d: pullRequests(headRefName: $h%d, states: [OPEN, CLOSED, MERGED], first: 1, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { number url state mergedAt headRefOid autoMergeRequest { mergeMethod } mergeCommit { oid } isInMergeQueue } }`, index, index))
		// -f, not -F: a branch name is passed as the string it is, never read as a
		// number, a boolean, or a file to load.
		args = append(args, "-f", fmt.Sprintf("h%d=%s", index, head))
	}
	query := fmt.Sprintf("query($owner: String!, $name: String!, %s) {\n  repository(owner: $owner, name: $name) {\n    %s\n  }\n}",
		strings.Join(variables, ", "), strings.Join(fields, "\n    "))
	stdout, err := g.graphql(ctx, fmt.Sprintf("ask the forge about the pull requests of %d branch(es)", len(heads)),
		append([]string{"-f", "query=" + query, "-F", "owner={owner}", "-F", "name={repo}"}, args...)...)
	if err != nil {
		return nil, err
	}
	var reported struct {
		Data struct {
			Repository map[string]*struct {
				Nodes []struct {
					Number           int    `json:"number"`
					URL              string `json:"url"`
					State            string `json:"state"`
					MergedAt         string `json:"mergedAt"`
					HeadRefOid       string `json:"headRefOid"`
					AutoMergeRequest *struct {
						MergeMethod string `json:"mergeMethod"`
					} `json:"autoMergeRequest"`
					MergeCommit *struct {
						OID string `json:"oid"`
					} `json:"mergeCommit"`
					IsInMergeQueue *bool `json:"isInMergeQueue"`
				} `json:"nodes"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &reported); err != nil {
		return nil, fmt.Errorf("decode the pull requests of %d branch(es): %w", len(heads), err)
	}
	if reported.Data.Repository == nil {
		return nil, fmt.Errorf("the forge's answer about the pull requests of %d branch(es) names no repository", len(heads))
	}
	for index, head := range heads {
		connection, present := reported.Data.Repository[fmt.Sprintf("h%d", index)]
		if !present || connection == nil {
			return nil, fmt.Errorf("the forge's answer does not say whether branch %s has a pull request", head)
		}
		if len(connection.Nodes) == 0 {
			continue
		}
		one := connection.Nodes[0]
		if one.Number <= 0 {
			return nil, fmt.Errorf("pull request for %s reported no number", head)
		}
		found := PullRequest{
			Number:     one.Number,
			URL:        one.URL,
			State:      one.State,
			Merged:     strings.EqualFold(one.State, "MERGED") || strings.TrimSpace(one.MergedAt) != "",
			HeadCommit: strings.TrimSpace(one.HeadRefOid),
			AutoMerge:  one.AutoMergeRequest != nil,
		}
		if one.MergeCommit != nil {
			found.MergeCommit = strings.TrimSpace(one.MergeCommit.OID)
		}
		// The queue is read as State reads it: an open request with no auto-merge
		// that the queue holds is a merge the forge is holding, and an answer that
		// does not say is an error rather than a drop.
		if !found.Merged && !found.AutoMerge && strings.EqualFold(found.State, "OPEN") {
			if one.IsInMergeQueue == nil {
				return nil, fmt.Errorf("the forge's answer about pull request %d does not say whether it is in the merge queue", one.Number)
			}
			found.AutoMerge = *one.IsInMergeQueue
		}
		answered[head] = found
	}
	return answered, nil
}

// ListOpen reports every pull request the forge holds open for the configured
// repository, whichever branch each carries and whoever opened it. It is the
// reading the forge-hygiene pass takes: what the harness knows about its own
// requests is on the run records, and what has accumulated on the forge
// regardless is only visible from here.
//
// The listing is bounded at maxListedOpenRequests rather than unbounded, and
// the bound is deliberate: a repository holding more open requests than that
// has a problem this reading reports the first two hundred of, and the rest are
// reached as those are closed.
func (g GitHub) ListOpen(ctx context.Context) ([]PullRequest, error) {
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return nil, err
	}
	result, err := g.exec(ctx, append([]string{"pr", "list"}, append(scope,
		"--state", "open",
		"--limit", strconv.Itoa(maxListedOpenRequests),
		"--json", "number,url,headRefName,baseRefName,headRefOid")...)...)
	if err != nil {
		return nil, fmt.Errorf("list open pull requests: %w", err)
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list open pull requests failed with exit code %d: %s", result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	var reported []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		HeadRefOid  string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &reported); err != nil {
		return nil, fmt.Errorf("decode open pull requests: %w", err)
	}
	open := make([]PullRequest, 0, len(reported))
	for _, one := range reported {
		if one.Number <= 0 {
			return nil, errors.New("an open pull request reported no number")
		}
		open = append(open, PullRequest{
			Number:     one.Number,
			URL:        one.URL,
			State:      "OPEN",
			HeadCommit: strings.TrimSpace(one.HeadRefOid),
			HeadBranch: strings.TrimSpace(one.HeadRefName),
			BaseBranch: strings.TrimSpace(one.BaseRefName),
		})
	}
	return open, nil
}

// Contains reports whether a branch on the forge already carries a commit —
// whether the commit is in the branch's history — which is what says a request
// still open on the forge has nothing left to bring to its base. It is asked
// of the forge rather than of a local checkout because the request and the
// branch are both the forge's, and a checkout that has not fetched either has
// no answer to give.
//
// The forge answers with how far the commit is ahead of the base; contained is
// ahead by nothing. The comparison is asked for one commit of its listing, which
// bounds what a request that is far ahead sends back without changing the count
// that decides it.
func (g GitHub) Contains(ctx context.Context, base, commit string) (bool, error) {
	if err := validateArgument("base branch", base); err != nil {
		return false, err
	}
	if !commitPattern.MatchString(commit) {
		return false, fmt.Errorf("head commit %q is invalid", commit)
	}
	url, err := g.remoteURL(ctx, g.remoteName())
	if err != nil {
		return false, err
	}
	// The API verb takes no --repo flag; the repository it addresses is the one
	// the environment names, so the configured remote is put there, and the
	// placeholders in the endpoint are filled from it. It goes in as the
	// `[HOST/]OWNER/REPO` gh documents for GH_REPO rather than as the URL git
	// reports: gh's parser was seen to take the URL too
	// (docs/diagnoses/yoyodyne-ifd-283-2-forge-hygiene-reads.md), but that is
	// gh's leniency rather than its contract, and the documented form is the
	// one a later gh keeps.
	repository, err := remoteRepository(url)
	if err != nil {
		return false, fmt.Errorf("resolve the repository of remote %s: %w", g.remoteName(), err)
	}
	result, err := g.Runner.Run(ctx, execution.Command{
		Name:     g.binary(),
		Args:     []string{"api", "--method", "GET", "-F", "per_page=1", "repos/{owner}/{repo}/compare/" + base + "..." + commit},
		Dir:      g.Dir,
		Env:      append(execution.ForgeEnvironment(nil), "GH_REPO="+repository),
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
	if err != nil {
		return false, fmt.Errorf("compare %s with %s: %w", base, commit, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return false, fmt.Errorf("compare %s with %s failed with exit code %d: %s", base, commit, result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	var compared struct {
		AheadBy *int `json:"ahead_by"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &compared); err != nil {
		return false, fmt.Errorf("decode the comparison of %s with %s: %w", base, commit, err)
	}
	if compared.AheadBy == nil {
		return false, fmt.Errorf("the comparison of %s with %s did not say how far ahead the commit is", base, commit)
	}
	return *compared.AheadBy == 0, nil
}

// BranchProtection is what the forge says stands between a push and one branch.
// By names which of the forge's two mechanisms protects it, for a reader who has
// to go and look at the rule; it is empty for a branch nothing protects.
type BranchProtection struct {
	Protected bool
	By        string
}

// Protection asks the forge whether a branch is protected against changes that
// do not arrive through a pull request, both ways the forge can protect one:
// the older per-branch protection, and a ruleset whose rules apply to the
// branch. It is the question scripts/cut-release.sh asks before a release cut,
// with one difference: per-branch protection is read from the branch's own
// protected flag, which any reader of the repository can see, rather than from
// the protection endpoint only an administrator can read.
//
// A branch with per-branch protection is protected whatever that protection
// requires. A ruleset protects it where it carries a rule that keeps a direct
// push out — a required pull request, a required status check, or a restriction
// on updates — and rules that only shape what may be pushed, such as a naming
// pattern, do not count.
//
// A question the forge does not answer is an error rather than an answer:
// the caller decides what an unanswered question means, and "not protected" is
// the one reading that would let a run move a branch the forge refuses.
func (g GitHub) Protection(ctx context.Context, branch string) (BranchProtection, error) {
	if err := validateArgument("branch", branch); err != nil {
		return BranchProtection{}, err
	}
	// Per-branch protection is read off the branch itself rather than off its
	// protection endpoint. That endpoint needs administrator rights, and the
	// forge can answer a caller without them with the same 404 it gives an
	// unprotected branch, so a branch protected only that way would read as open
	// to the account the harness runs under. The branch's own protected flag is
	// visible to anybody who can read the repository.
	branchInfo, err := g.api(ctx, "repos/{owner}/{repo}/branches/"+branch)
	if err != nil {
		return BranchProtection{}, fmt.Errorf("ask the forge whether %s is protected: %w", branch, err)
	}
	if branchInfo.Status != execution.ProcessSucceeded {
		return BranchProtection{}, fmt.Errorf("ask the forge whether %s is protected: exit code %d: %s",
			branch, branchInfo.ExitCode, g.redact(firstLine(strings.TrimSpace(branchInfo.Stderr+"\n"+branchInfo.Stdout))))
	}
	var reported struct {
		Protected *bool `json:"protected"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(branchInfo.Stdout)), &reported); err != nil {
		return BranchProtection{}, fmt.Errorf("decode what the forge says about %s: %w", branch, err)
	}
	if reported.Protected == nil {
		return BranchProtection{}, fmt.Errorf("the forge's answer about %s does not say whether it is protected", branch)
	}
	if *reported.Protected {
		return BranchProtection{Protected: true, By: "branch protection"}, nil
	}
	rules, err := g.api(ctx, "repos/{owner}/{repo}/rules/branches/"+branch)
	if err != nil {
		return BranchProtection{}, fmt.Errorf("ask the forge which rulesets apply to %s: %w", branch, err)
	}
	if rules.Status != execution.ProcessSucceeded {
		return BranchProtection{}, fmt.Errorf("ask the forge which rulesets apply to %s: exit code %d: %s",
			branch, rules.ExitCode, g.redact(firstLine(strings.TrimSpace(rules.Stderr))))
	}
	var applied []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rules.Stdout)), &applied); err != nil {
		return BranchProtection{}, fmt.Errorf("decode the rulesets that apply to %s: %w", branch, err)
	}
	for _, rule := range applied {
		switch rule.Type {
		case "pull_request", "required_status_checks", "update":
			return BranchProtection{Protected: true, By: "ruleset"}, nil
		}
	}
	return BranchProtection{}, nil
}

// api runs one GET against the forge's REST API for the configured remote's
// repository. The API verb takes no --repo flag, so the repository is named in
// the environment, in the form Contains explains.
func (g GitHub) api(ctx context.Context, endpoint string) (execution.ProcessResult, error) {
	return g.apiMethod(ctx, "GET", endpoint)
}

// apiMethod is api with the request's method named, for the few requests that
// ask the forge to do something rather than to say something.
func (g GitHub) apiMethod(ctx context.Context, method, endpoint string) (execution.ProcessResult, error) {
	return g.apiQuery(ctx, method, endpoint)
}

// apiQuery lets a read select the fields it needs inside gh, after gh has
// decoded the whole response and before the process runner bounds its output.
func (g GitHub) apiQuery(ctx context.Context, method, endpoint string, options ...string) (execution.ProcessResult, error) {
	url, err := g.remoteURL(ctx, g.remoteName())
	if err != nil {
		return execution.ProcessResult{}, err
	}
	repository, err := remoteRepository(url)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("resolve the repository of remote %s: %w", g.remoteName(), err)
	}
	return g.Runner.Run(ctx, execution.Command{
		Name:     g.binary(),
		Args:     append([]string{"api", "--method", method, endpoint}, options...),
		Dir:      g.Dir,
		Env:      append(execution.ForgeEnvironment(nil), "GH_REPO="+repository),
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
}

// find lists the one pull request a run branch may have, in any state. Listing
// is deliberate: it reports "there is none" as an empty result rather than as a
// failed command, so an absent pull request is never confused with a forge that
// could not be reached.
//
// The head is the plain branch name even when the branch lives in a fork, which
// is the one place a cross-repository request is not qualified: the forge
// filters this listing by head reference name, and a request opened from a fork
// carries the same branch name there as it does in the fork. Qualifying it would
// name a reference the base repository has never heard of. What keeps that from
// matching somebody else's request is the branch name itself — a run branch
// carries its run's identifier, which no other repository's branch has.
func (g GitHub) find(ctx context.Context, head string) (PullRequest, bool, error) {
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return PullRequest{}, false, err
	}
	result, err := g.exec(ctx, append([]string{"pr", "list"}, append(scope,
		"--head", head,
		"--state", "all",
		"--limit", "1",
		"--json", "number,url,state,mergedAt,autoMergeRequest,headRefOid,mergeCommit")...)...)
	if err != nil {
		return PullRequest{}, false, fmt.Errorf("list pull requests for %s: %w", head, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return PullRequest{}, false, fmt.Errorf("list pull requests for %s failed with exit code %d: %s", head, result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	var reported []struct {
		Number     int    `json:"number"`
		URL        string `json:"url"`
		State      string `json:"state"`
		MergedAt   string `json:"mergedAt"`
		HeadRefOid string `json:"headRefOid"`
		// AutoMergeRequest is the merge the forge is holding, and is null on a
		// request that has none. Its contents say who asked and how; only its
		// presence matters here.
		AutoMergeRequest *struct {
			MergeMethod string `json:"mergeMethod"`
		} `json:"autoMergeRequest"`
		// MergeCommit is the forge's own record of the commit that merged the
		// request, and is null until it has merged one.
		MergeCommit *struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &reported); err != nil {
		return PullRequest{}, false, fmt.Errorf("decode pull requests for %s: %w", head, err)
	}
	if len(reported) == 0 {
		return PullRequest{}, false, nil
	}
	one := reported[0]
	if one.Number <= 0 {
		return PullRequest{}, false, fmt.Errorf("pull request for %s reported no number", head)
	}
	found := PullRequest{
		Number:     one.Number,
		URL:        one.URL,
		State:      one.State,
		Merged:     strings.EqualFold(one.State, "MERGED") || strings.TrimSpace(one.MergedAt) != "",
		HeadCommit: strings.TrimSpace(one.HeadRefOid),
		AutoMerge:  one.AutoMergeRequest != nil,
	}
	if one.MergeCommit != nil {
		found.MergeCommit = strings.TrimSpace(one.MergeCommit.OID)
	}
	return found, true, nil
}

// mergeQueueQuery asks whether a pull request is in its base branch's merge
// queue. The listing verbs carry no field for it, so it is asked of the forge's
// GraphQL API, with the repository named by the placeholders the API verb fills
// from GH_REPO. The request's node id comes back with the answer, because it is
// what taking the request out of the queue is asked by.
const mergeQueueQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { id isInMergeQueue } }
}`

// dequeueMutation takes a pull request out of its base branch's merge queue.
const dequeueMutation = `mutation($id: ID!) {
  dequeuePullRequest(input: {id: $id}) { mergeQueueEntry { id } }
}`

// inMergeQueue reports whether the forge's merge queue holds a pull request.
func (g GitHub) inMergeQueue(ctx context.Context, number int) (bool, error) {
	_, queued, err := g.mergeQueueEntry(ctx, number)
	return queued, err
}

// mergeQueueEntry reports whether the forge's merge queue holds a pull request,
// and the request's node id beside the answer.
func (g GitHub) mergeQueueEntry(ctx context.Context, number int) (string, bool, error) {
	stdout, err := g.graphql(ctx, fmt.Sprintf("ask the forge whether pull request %d is in the merge queue", number),
		"-f", "query="+mergeQueueQuery,
		"-F", "owner={owner}",
		"-F", "name={repo}",
		"-F", "number="+strconv.Itoa(number))
	if err != nil {
		return "", false, err
	}
	var answered struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					ID             string `json:"id"`
					IsInMergeQueue *bool  `json:"isInMergeQueue"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answered); err != nil {
		return "", false, fmt.Errorf("decode whether pull request %d is in the merge queue: %w", number, err)
	}
	pull := answered.Data.Repository.PullRequest
	if pull == nil || pull.IsInMergeQueue == nil {
		return "", false, fmt.Errorf("the forge's answer about pull request %d does not say whether it is in the merge queue", number)
	}
	return strings.TrimSpace(pull.ID), *pull.IsInMergeQueue, nil
}

// dequeue takes a pull request out of its base branch's merge queue, where the
// forge's answer says the queue holds it. A forge with no merge queue answers
// that nothing is queued, and there is nothing to take out.
func (g GitHub) dequeue(ctx context.Context, number int) error {
	id, queued, err := g.mergeQueueEntry(ctx, number)
	if err != nil {
		return err
	}
	if !queued {
		return nil
	}
	if id == "" {
		return fmt.Errorf("the forge's answer about pull request %d names no request to take out of the merge queue", number)
	}
	_, err = g.graphql(ctx, fmt.Sprintf("take pull request %d out of the merge queue", number),
		"-f", "query="+dequeueMutation,
		"-F", "id="+id)
	return err
}

// graphql runs one request against the forge's GraphQL API, scoped to the
// configured remote's repository, and returns what it answered.
func (g GitHub) graphql(ctx context.Context, what string, args ...string) (string, error) {
	url, err := g.remoteURL(ctx, g.remoteName())
	if err != nil {
		return "", err
	}
	repository, err := remoteRepository(url)
	if err != nil {
		return "", fmt.Errorf("resolve the repository of remote %s: %w", g.remoteName(), err)
	}
	result, err := g.Runner.Run(ctx, execution.Command{
		Name:     g.binary(),
		Args:     append([]string{"api", "graphql"}, args...),
		Dir:      g.Dir,
		Env:      append(execution.ForgeEnvironment(nil), "GH_REPO="+repository),
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("%s: exit code %d: %s", what, result.ExitCode, g.redact(firstLine(strings.TrimSpace(result.Stderr))))
	}
	return result.Stdout, nil
}

// repoArgs scopes a forge command to the configured remote's repository. It
// fails rather than falling back to inference: publishing to a repository the
// project did not name is the mistake worth refusing, and a silent fallback is
// how that mistake would happen.
func (g GitHub) repoArgs(ctx context.Context) ([]string, error) {
	url, err := g.remoteURL(ctx, g.remoteName())
	if err != nil {
		return nil, err
	}
	return []string{"--repo", url}, nil
}

// headRef names the branch a pull request is opened from, in the form the forge
// has to be told it. A project whose run branches are pushed to the repository
// it publishes into names the branch and nothing else. One that pushes to a fork
// has to qualify the branch with that fork's owner, because the reference the
// forge is being asked about is not one the base repository has.
//
// The owner is read out of the fork remote's own URL rather than configured
// beside it. The remote already says which repository the branch was pushed to,
// and a second setting is a second thing that can disagree with the first.
func (g GitHub) headRef(ctx context.Context, branch string) (string, error) {
	pushRemote := g.pushRemoteName()
	if pushRemote == g.remoteName() {
		return branch, nil
	}
	url, err := g.remoteURL(ctx, pushRemote)
	if err != nil {
		return "", err
	}
	owner, err := repositoryOwner(url)
	if err != nil {
		return "", fmt.Errorf("resolve the owner of remote %s: %w", pushRemote, err)
	}
	return owner + ":" + branch, nil
}

func (g GitHub) remoteName() string {
	remote := strings.TrimSpace(g.Remote)
	if remote == "" {
		return defaultRemote
	}
	return remote
}

// pushRemoteName is the remote run branches were pushed to, which is the one
// pull requests are opened against unless the project publishes from a fork.
func (g GitHub) pushRemoteName() string {
	pushRemote := strings.TrimSpace(g.PushRemote)
	if pushRemote == "" {
		return g.remoteName()
	}
	return pushRemote
}

func (g GitHub) remoteURL(ctx context.Context, remote string) (string, error) {
	if err := validateArgument("remote", remote); err != nil {
		return "", err
	}
	// Reading a remote's URL is a local Git command however much it is about the
	// forge, so it gets the plain allowlisted environment: nothing here reaches
	// the network, and a credential handed to it would be a credential handed to
	// whatever hook the repository runs.
	result, err := g.Runner.Run(ctx, execution.Command{
		Name:     g.gitBinary(),
		Args:     []string{"-C", g.Dir, "remote", "get-url", remote},
		Env:      execution.GitEnvironment(nil),
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
	if err != nil {
		return "", fmt.Errorf("resolve remote %s: %w", remote, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve remote %s failed with exit code %d: %s", remote, result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	url := strings.TrimSpace(result.Stdout)
	if url == "" {
		return "", fmt.Errorf("remote %s reported no URL", remote)
	}
	return url, nil
}

func (g GitHub) gitBinary() string {
	if strings.TrimSpace(g.GitBinary) == "" {
		return defaultGitBinary
	}
	return g.GitBinary
}

// exec runs one forge CLI invocation. It is given the forge environment — the
// allowlist every harness-launched process gets, plus the forge's own
// credential and addressing — because this is the command that needs one. No
// other command the harness runs is given it.
func (g GitHub) exec(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	return g.Runner.Run(ctx, execution.Command{
		Name:     g.binary(),
		Args:     args,
		Dir:      g.Dir,
		Env:      execution.ForgeEnvironment(nil),
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
}

func (g GitHub) binary() string {
	if strings.TrimSpace(g.Binary) == "" {
		return defaultBinary
	}
	return g.Binary
}

func (g GitHub) timeout() time.Duration {
	if g.Timeout <= 0 {
		return defaultTimeout
	}
	return g.Timeout
}

// redact scrubs sensitive values out of anything the CLI printed before it
// becomes part of an error a run records.
func (g GitHub) redact(value string) string {
	return execution.NewRedactor(g.RedactValues...).Redact(value)
}

func (r Request) validate() error {
	if err := validateArgument("head branch", r.Head); err != nil {
		return err
	}
	if err := validateArgument("base branch", r.Base); err != nil {
		return err
	}
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("pull request title is required")
	}
	return nil
}

func (r CloseRequest) validate() error {
	var problems []error
	if err := validateArgument("head branch", r.Head); err != nil {
		problems = append(problems, err)
	}
	if r.Number <= 0 {
		problems = append(problems, errors.New("pull request number is required"))
	}
	if strings.TrimSpace(r.Comment) == "" {
		problems = append(problems, errors.New("closing a pull request requires a comment naming where its work landed"))
	}
	return errors.Join(problems...)
}

func (r MergeRequest) validate() error {
	var problems []error
	if r.Number <= 0 {
		problems = append(problems, errors.New("pull request number is required"))
	}
	if !commitPattern.MatchString(r.HeadCommit) {
		problems = append(problems, fmt.Errorf("head commit %q is invalid", r.HeadCommit))
	}
	if r.Method.flag() == "" {
		problems = append(problems, fmt.Errorf("merge method %q is not one the forge offers", r.Method))
	}
	return errors.Join(problems...)
}

// flag is the CLI option for a merge method, and the empty string for anything
// that is not one. The forge requires a method to be named, so an unset one is
// refused rather than left to a default nobody chose.
func (m MergeMethod) flag() string {
	switch m {
	case MergeRebase:
		return "--rebase"
	case MergeCommit:
		return "--merge"
	case MergeSquash:
		return "--squash"
	}
	return ""
}

// commitPattern keeps a commit a commit, for the same reason branch names are
// validated: it reaches a command line.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// ownerPattern keeps a resolved account name an account name. It reaches a
// command line as half of a qualified head reference, so anything that could
// read as an option, as a second reference, or as whitespace is refused rather
// than passed along.
var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// repositoryOwner reads the account a remote's repository belongs to out of the
// remote's URL. A URL that names no such account is refused: publishing from a
// fork nobody can identify would open the request against the wrong head or
// none at all.
func repositoryOwner(url string) (string, error) {
	_, owner, _, err := parseRemoteURL(url)
	return owner, err
}

// defaultForgeHost is the host gh addresses when GH_REPO names none. A remote
// there is named to gh as `OWNER/REPO`; a remote anywhere else carries its host.
const defaultForgeHost = "github.com"

// remoteRepository reads the repository a remote's URL names, in the
// `[HOST/]OWNER/REPO` form gh's GH_REPO variable is documented to take. The
// host is left off for the forge's own, which is the documented plain form, and
// kept for any other so an enterprise remote is not silently resolved against
// github.com.
func remoteRepository(url string) (string, error) {
	host, owner, name, err := parseRemoteURL(url)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(host, defaultForgeHost) {
		return owner + "/" + name, nil
	}
	return host + "/" + owner + "/" + name, nil
}

// parseRemoteURL reads the host, account, and repository name out of a remote's
// URL. Every form Git accepts names the repository as the last two path
// segments — `owner/name` — whether it arrived over SSH, over HTTPS, or in the
// scp-like syntax, so that is what is read rather than the shape of any one of
// them. The host is whatever stands before the path, less any user and port. A
// URL that names no such pair is refused rather than guessed at.
func parseRemoteURL(url string) (host, owner, name string, err error) {
	path := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(url), "/"), ".git")
	if scheme := strings.Index(path, "://"); scheme >= 0 {
		authority := path[scheme+len("://"):]
		slash := strings.Index(authority, "/")
		if slash < 0 {
			return "", "", "", fmt.Errorf("remote URL %q names no repository", url)
		}
		host = authority[:slash]
		path = authority[slash+1:]
	} else if colon := strings.LastIndex(path, ":"); colon >= 0 {
		// The scp-like form, `[user@]host:owner/name`, which has no scheme and
		// separates the path with a colon rather than a slash.
		host = path[:colon]
		path = path[colon+1:]
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if port := strings.LastIndex(host, ":"); port >= 0 {
		host = host[:port]
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 2 {
		return "", "", "", fmt.Errorf("remote URL %q names no repository owner", url)
	}
	owner = segments[len(segments)-2]
	name = segments[len(segments)-1]
	if !ownerPattern.MatchString(owner) {
		return "", "", "", fmt.Errorf("remote URL %q names %q as its owner, which is not an account name", url, owner)
	}
	if !ownerPattern.MatchString(name) {
		return "", "", "", fmt.Errorf("remote URL %q names %q as its repository, which is not a repository name", url, name)
	}
	if host == "" {
		return "", "", "", fmt.Errorf("remote URL %q names no host", url)
	}
	return host, owner, name, nil
}

// validateArgument keeps a branch name a branch name. These values reach a
// command line, so anything that could read as an option is refused rather than
// passed along.
func validateArgument(kind, value string) error {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return fmt.Errorf("%s is required", kind)
	case strings.HasPrefix(trimmed, "-"):
		return fmt.Errorf("%s %q cannot start with a dash", kind, value)
	case strings.IndexFunc(trimmed, func(r rune) bool { return r <= ' ' }) >= 0:
		return fmt.Errorf("%s %q cannot contain whitespace", kind, value)
	}
	return nil
}

func boundedTitle(title string) string {
	folded := strings.Join(strings.Fields(title), " ")
	return bounded(folded, maxTitleBytes)
}

func boundedBody(body string) string {
	return bounded(body, maxBodyBytes)
}

// bounded keeps the head of a value within a limit, cut on a rune boundary so
// truncated text stays text.
func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !isRuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}

func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return line
}
