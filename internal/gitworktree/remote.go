package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
)

// Publication is what publishing a run's branch produced: the commit the
// harness made of the developer's work, and the remote branch it now sits on.
type Publication struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// ErrRemotePushRejected reports a push the remote refused. It is never resolved
// by forcing: a remote branch that moved is reconciled, exactly as a local
// target that drifted is.
var ErrRemotePushRejected = errors.New("remote rejected the push")

// ErrRemoteTargetDrift reports a remote target branch carrying work the
// promotion was not written against. It is the remote half of ErrTargetDrift:
// the merge must fail rather than have the forge reconcile a change nobody saw.
var ErrRemoteTargetDrift = errors.New("remote target branch moved away from the content the promotion was written against")

// ErrRemoteTargetMismatch reports a merge that did not put the promotion on the
// remote target branch. It is deliberately distinct from drift: drift is a
// reason not to merge, this is what a merge that already happened turned out to
// be.
var ErrRemoteTargetMismatch = errors.New("remote target branch does not carry the promoted commit")

// ErrRemoteAuthRefused reports a remote that turned away the credential the
// harness presented: an SSH key the server would not take, or a forge login over
// HTTPS that was refused or missing. It is told apart from every other failed
// remote command by recovery's closed reading of what Git printed, and it is a
// sentinel because a caller has to tell it from a rejected push: this one is
// the machine's credential and not the branch, and nothing about the change or
// the remote moves it — somebody loading the key or renewing the login does.
var ErrRemoteAuthRefused = errors.New("the remote refused the credential the harness presented")

// authRefusal is the failure a remote command ends on when what refused it was
// the credential, and nil for every other result. Every remote command reaches
// it through runRemote, so a push, a fetch, and a listing refused for a key all
// say so in one class rather than in whatever words their own failure used.
func authRefusal(args []string, result execution.ProcessResult) error {
	if result.Status == execution.ProcessSucceeded || !recovery.AuthenticationRefusedDetail(result.Stderr) {
		return nil
	}
	return fmt.Errorf("%w: git %s exited %d: %s", ErrRemoteAuthRefused, remoteVerb(args), result.ExitCode, strings.TrimSpace(result.Stderr))
}

// remoteVerb names the Git subcommand a remote command ran, past the options
// that precede it, so a refusal says which of push, fetch, and ls-remote it was.
func remoteVerb(args []string) string {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "-C", "-c":
			index++
		default:
			return args[index]
		}
	}
	return "command"
}

// VerifyRemoteAccess asks both remotes a run publishes through whether they
// still accept the harness's credential, by listing the target branch on each.
// It moves nothing and writes nothing, which is what lets the resumption of a
// promotion refused for a key ask it before writing anything of its own.
//
// A listing is a read, and a forge can accept a key for reading that it will
// not accept for a push. It is the question that is cheap to ask and the one
// the field cases failed: an SSH key the server does not take is refused at the
// connection, before anything is read or written.
func (m *Manager) VerifyRemoteAccess(ctx context.Context, targetBranch string) error {
	if err := validateTargetBranch(targetBranch); err != nil {
		return err
	}
	remotes := []string{m.remote}
	if m.pushRemote != m.remote {
		remotes = append(remotes, m.pushRemote)
	}
	for _, remote := range remotes {
		if _, _, err := m.remoteCommit(ctx, remote, targetBranch); err != nil {
			return fmt.Errorf("list %s on %s: %w", targetBranch, remote, err)
		}
	}
	return nil
}

// TargetDivergence answers the question CatchUpTarget answers before it moves
// anything — whether the local target branch can be brought onto the remote's
// by a fast-forward — and moves nothing. Held is empty when it can, or when the
// remote has no such branch; otherwise it says why not, in the words the
// catch-up itself would hold on.
//
// It is what the resumption of a promotion refused for a diverged target asks
// before writing anything, so a person who has not settled the branches yet is
// told so rather than having the run made live into the same refusal. The
// resumed run's own catch-up is what then moves the branch, under the promotion
// lease this does not hold. Uncommitted work in the primary checkout that would
// hold the catch-up is not asked here: the resumption asks the checkout itself.
func (m *Manager) TargetDivergence(ctx context.Context, targetBranch string) (Catchup, error) {
	catchup := Catchup{TargetBranch: targetBranch}
	if err := validateTargetBranch(targetBranch); err != nil {
		return catchup, err
	}
	local, exists, err := m.optionalBranchCommit(ctx, targetBranch)
	if err != nil {
		return catchup, err
	}
	if !exists {
		catchup.Held = fmt.Sprintf("%s is not a branch of this repository", targetBranch)
		return catchup, nil
	}
	catchup.LocalCommit = local
	published, onRemote, err := m.remoteCommit(ctx, m.remote, targetBranch)
	if err != nil || !onRemote || published == local {
		return catchup, err
	}
	catchup.RemoteCommit = published
	if err := m.fetchRemoteBranch(ctx, targetBranch, published); err != nil {
		return catchup, fmt.Errorf("fetch %s from %s: %w", targetBranch, m.remote, err)
	}
	carries, err := m.descendsFrom(ctx, local, published)
	if err != nil {
		return catchup, err
	}
	if !carries {
		catchup.Held = fmt.Sprintf("%s on %s is at %s, which does not contain the local %s at %s; only a person can say which history is right",
			targetBranch, m.remote, published, targetBranch, local)
	}
	return catchup, nil
}

// RemoteConfigured reports whether the repository has the remote publishing
// would open pull requests against. A repository without one is not an error and
// never becomes a failed run: publishing is skipped and the run behaves exactly
// as a local-only run does.
func (m *Manager) RemoteConfigured(ctx context.Context) (bool, error) {
	return m.remoteConfigured(ctx, m.remote)
}

// PushRemoteConfigured reports whether the repository has the remote run
// branches are pushed to. It is the same question RemoteConfigured asks, about
// the other half of a fork arrangement, and it is asked separately so a
// contributor who has configured a fork they have not added yet is told which
// remote is missing rather than which one is not.
//
// A project that names no push remote is asking about the same remote twice,
// which is what makes this safe to call unconditionally.
func (m *Manager) PushRemoteConfigured(ctx context.Context) (bool, error) {
	return m.remoteConfigured(ctx, m.pushRemote)
}

// PushRemote names the remote run branches are pushed to, so a caller reporting
// what publishing needs can name it without knowing how it was resolved.
func (m *Manager) PushRemote() string {
	return m.pushRemote
}

func (m *Manager) remoteConfigured(ctx context.Context, remote string) (bool, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "remote", "get-url", remote)
	if err != nil {
		return false, err
	}
	if result.Status != execution.ProcessSucceeded {
		return false, nil
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

// PublishBranch commits whatever the developer left in the worktree as one
// harness-owned commit and pushes the run branch to the configured push remote,
// which is the project's fork where it has one and the publishing remote
// otherwise. It is what the developer phase causes and the harness performs:
// nothing here is routed through an agent, and the commit carries the harness
// identity rather than the developer's.
//
// The push is an ordinary fast-forward push. A remote branch that somehow moved
// away from what the harness put there is refused rather than forced, because a
// published branch nobody can explain is exactly the thing a person has to look
// at.
func (m *Manager) PublishBranch(ctx context.Context, worktree Worktree, message string) (Publication, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Publication{}, err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Publication{}, err
	}
	if !dirty && head == worktree.BaseCommit {
		return Publication{}, ErrNoChanges
	}
	commit := head
	if dirty {
		commit, err = m.commitWorktree(ctx, path, message)
		if err != nil {
			return Publication{}, err
		}
	}
	if commit == worktree.BaseCommit {
		return Publication{}, ErrNoChanges
	}
	publication := Publication{Remote: m.pushRemote, Branch: worktree.Branch, Commit: commit}
	// The commit is reported even when the push fails, because it exists either
	// way. A caller that could not learn of it would leave the worktree at a HEAD
	// nothing recorded, which is the one state the ownership check has to be able
	// to tell apart from an agent's own commit.
	if err := m.pushBranch(ctx, worktree.Branch, commit); err != nil {
		return publication, err
	}
	return publication, nil
}

// RepublishBranch puts a replayed run branch back on the remote it was already
// published to. Replaying a change onto a moved target rewrites the run branch,
// so the published branch and the pull request that carries it would otherwise
// describe work the authoritative local branch no longer has.
//
// The push is a compare-and-swap on the exact commit the harness published,
// which is the same discipline every other write here follows: the local target
// is advanced from a named commit, the merged remote branch is deleted from
// one, and a remote branch that moved away from what the harness put there is
// still refused rather than overwritten. What is replaced is only ever this
// run's own branch, never a target branch and never work the harness did not
// author.
func (m *Manager) RepublishBranch(ctx context.Context, worktree Worktree, previousCommit string) (Publication, error) {
	if !commitPattern.MatchString(previousCommit) {
		return Publication{}, fmt.Errorf("published commit %q is invalid", previousCommit)
	}
	if err := validateRef(worktree.Branch); err != nil {
		return Publication{}, err
	}
	_, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Publication{}, err
	}
	publication := Publication{Remote: m.pushRemote, Branch: worktree.Branch, Commit: head}
	if head == previousCommit {
		return publication, nil
	}
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", "--force-with-lease=refs/heads/"+worktree.Branch+":"+previousCommit,
		m.pushRemote, head+":refs/heads/"+worktree.Branch)
	if err != nil {
		return publication, err
	}
	if result.Status != execution.ProcessSucceeded {
		return publication, fmt.Errorf("%w: replace %s with %s on %s failed with exit code %d: %s",
			ErrRemotePushRejected, previousCommit, head, worktree.Branch, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	published, exists, err := m.remoteCommit(ctx, m.pushRemote, worktree.Branch)
	if err != nil {
		return publication, err
	}
	if !exists || published != head {
		return publication, fmt.Errorf("%w: %s on %s is at %q after the push, want %s", ErrRemotePushRejected, worktree.Branch, m.pushRemote, published, head)
	}
	return publication, nil
}

// VerifyRemoteTarget checks that the remote target branch can still receive the
// promotion the harness already made locally. The forge is what performs the
// merge, and a forge asked to merge into a branch that moved would reconcile
// that movement itself — resolving a conflict nothing in this run ever saw.
//
// What the remote target may be is decided by what a forge merge leaves behind.
// A merged pull request puts the promoted commit on the target and a merge
// commit above it, so the target of a repository that has published before is a
// commit this repository does not have and never will: the harness does not
// carry the forge's merge commits onto the local branch. Such a target is not
// drift, and telling the two apart is a question about content rather than
// identity — it must carry exactly what the promotion was written against, and
// it must contain the commit that was promoted last.
func (m *Manager) VerifyRemoteTarget(ctx context.Context, integration Integration) error {
	if err := validateTargetBranch(integration.TargetBranch); err != nil {
		return err
	}
	if !commitPattern.MatchString(integration.TargetCommit) {
		return fmt.Errorf("integrated target commit %q is invalid", integration.TargetCommit)
	}
	if !commitPattern.MatchString(integration.PreviousTargetCommit) {
		return fmt.Errorf("previous target commit %q is invalid", integration.PreviousTargetCommit)
	}
	published, exists, err := m.remoteCommit(ctx, m.remote, integration.TargetBranch)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s does not exist on %s", ErrRemoteTargetDrift, integration.TargetBranch, m.remote)
	}
	// The remote is at or behind the promotion: a repository that has never
	// published, or one whose earlier publication the forge merged by
	// fast-forward. Nothing has to be fetched to know that.
	behind, err := m.descendsFrom(ctx, published, integration.TargetCommit)
	if err != nil {
		return err
	}
	if behind {
		return nil
	}
	// Anything else has to be looked at, so it is fetched. A forge merge commit
	// and someone else's work are both commits this repository does not have;
	// only their content tells them apart.
	if err := m.fetchRemoteBranch(ctx, integration.TargetBranch, published); err != nil {
		return fmt.Errorf("%w: %w", ErrRemoteTargetDrift, err)
	}
	carries, err := m.descendsFrom(ctx, integration.PreviousTargetCommit, published)
	if err != nil {
		return err
	}
	if !carries {
		return fmt.Errorf("%w: %s on %s is at %s, which does not contain the commit this promotion was made from (%s)",
			ErrRemoteTargetDrift, integration.TargetBranch, m.remote, published, integration.PreviousTargetCommit)
	}
	same, err := m.sameContent(ctx, published, integration.PreviousTargetCommit)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("%w: %s on %s is at %s, which carries content the promotion was not written against (%s)",
			ErrRemoteTargetDrift, integration.TargetBranch, m.remote, published, integration.PreviousTargetCommit)
	}
	return nil
}

// ConfirmRemoteTarget establishes that a forge's merge put the promotion on the
// remote target branch, and names the merge commit that carried it there. The
// two branches do not end at the same commit — the forge adds a merge commit of
// its own, which is the price of not pushing the target branch — so what is
// checked is the relationship that a merge does guarantee: the promoted commit
// itself is on the remote target, unrewritten. A forge that replayed the
// commits instead of merging them fails that, and it is reported rather than
// reconciled: which history is right is a person's decision, not a harness's.
//
// What is deliberately not checked is that the remote target carries exactly
// the promotion's content. That was the rule until yoyodyne-ifd.357, and it is
// satisfied only by the last merge into the branch: a queued merge that lands
// among others — ten held requests merged in one sitting, on 2026-09-13 — puts
// the promoted commit on the remote under a merge commit that later merges have
// since built on, and every reconcile then reported the remote as carrying
// content the promotion did not, for good. Containment is the fact a merge
// guarantees; equality is a fact about being merged last.
//
// mergeCommit is the commit the forge recorded as this request's merge, where
// the caller has it, and empty otherwise. It never decides the confirmation:
// the containment check above is the whole of what confirms a publication, and
// a forge's record that disagrees with a remote that provably carries the
// promotion is a fact about the record. What the forge's commit decides is only
// what is reported as the merge commit — it is taken when it is on the remote
// target with the promoted commit as a parent, which is what a merge of this
// request looks like, and otherwise the merge is found in the remote history
// itself: the merge on the path from the promoted commit to the tip whose
// parents include the promoted commit. A fast-forward, and a request the forge
// marked merged because its head became reachable from the base under somebody
// else's merge, have no such commit and report none.
func (m *Manager) ConfirmRemoteTarget(ctx context.Context, integration Integration, mergeCommit string) (string, error) {
	if err := validateTargetBranch(integration.TargetBranch); err != nil {
		return "", err
	}
	if !commitPattern.MatchString(integration.TargetCommit) {
		return "", fmt.Errorf("integrated target commit %q is invalid", integration.TargetCommit)
	}
	if mergeCommit != "" && !commitPattern.MatchString(mergeCommit) {
		return "", fmt.Errorf("forge merge commit %q is invalid", mergeCommit)
	}
	published, exists, err := m.remoteCommit(ctx, m.remote, integration.TargetBranch)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: %s does not exist on %s after the merge", ErrRemoteTargetMismatch, integration.TargetBranch, m.remote)
	}
	if published != integration.TargetCommit {
		if err := m.fetchRemoteBranch(ctx, integration.TargetBranch, published); err != nil {
			return "", fmt.Errorf("%w: %w", ErrRemoteTargetMismatch, err)
		}
	}
	promoted, err := m.descendsFrom(ctx, integration.TargetCommit, published)
	if err != nil {
		return "", err
	}
	if !promoted {
		return "", fmt.Errorf("%w: %s on %s is at %s, which does not contain the promoted commit %s",
			ErrRemoteTargetMismatch, integration.TargetBranch, m.remote, published, integration.TargetCommit)
	}
	if mergeCommit != "" {
		recorded, err := m.mergedBy(ctx, mergeCommit, integration.TargetCommit, published)
		if err != nil {
			return "", err
		}
		if recorded {
			return mergeCommit, nil
		}
	}
	return m.mergeOf(ctx, integration.TargetCommit, published)
}

// mergedBy reports whether a commit the forge named is the merge of this
// promotion into the branch: on the branch, with the promoted commit as one of
// its parents. Containment alone would accept every later merge on the branch
// as well, since each of those carries the promotion too.
//
// The forge's commit is an ancestor of the fetched tip whenever it is on the
// branch, so the fetch that preceded this brought it in; a commit this
// repository does not have answers no to the first question, which is the right
// answer, and is never asked the second.
func (m *Manager) mergedBy(ctx context.Context, mergeCommit, promoted, tip string) (bool, error) {
	onTarget, err := m.descendsFrom(ctx, mergeCommit, tip)
	if err != nil || !onTarget {
		return false, err
	}
	parents, err := m.parentsOf(ctx, mergeCommit)
	if err != nil {
		return false, err
	}
	return slices.Contains(parents, promoted), nil
}

// parentsOf lists a commit's parents, for the one question containment cannot
// answer: whether a merge commit is the merge of this promotion rather than of
// something later that carries it.
func (m *Manager) parentsOf(ctx context.Context, commit string) ([]string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the parents of %s failed with exit code %d: %s", commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 || fields[0] != commit {
		return nil, fmt.Errorf("list the parents of %s answered about %q", commit, strings.TrimSpace(result.Stdout))
	}
	return fields[1:], nil
}

// mergeOf names the merge commit that brought one commit into a branch whose
// tip is another: the merge on the ancestry path between the two that has the
// promoted commit as a parent. Nothing is found for a fast-forward, where the
// promoted commit is on the branch's own line and no merge was made of it, and
// that is reported as no merge commit rather than as a failure — the promotion
// is on the remote either way, and containment already said so.
//
// The listing is newest first, so where more than one merge has the promoted
// commit as a parent — a branch merged twice, which nothing here does — the
// oldest is taken, because it is the one that brought the commit in.
func (m *Manager) mergeOf(ctx context.Context, promoted, tip string) (string, error) {
	if promoted == tip {
		return "", nil
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot,
		"rev-list", "--merges", "--parents", "--ancestry-path", promoted+".."+tip)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("list the merges between %s and %s failed with exit code %d: %s", promoted, tip, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	merge := ""
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !commitPattern.MatchString(fields[0]) {
			continue
		}
		for _, parent := range fields[1:] {
			if parent == promoted {
				merge = fields[0]
			}
		}
	}
	return merge, nil
}

// fetchRemoteBranch brings one remote branch's tip into this repository so it
// can be inspected, and refuses a branch that moved between being resolved and
// being fetched: an answer about a commit other than the one that was asked
// about is worse than no answer.
func (m *Manager) fetchRemoteBranch(ctx context.Context, branch, commit string) error {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"fetch", "--no-tags", "--no-write-fetch-head", m.remote, "+refs/heads/"+branch+":refs/"+fetchedRemoteTarget)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("fetch %s from %s failed with exit code %d: %s", branch, m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	fetched, err := m.resolveCommit(ctx, "refs/"+fetchedRemoteTarget)
	if err != nil {
		return err
	}
	if fetched != commit {
		return fmt.Errorf("%s on %s is at %s, not the %s that was resolved a moment earlier", branch, m.remote, fetched, commit)
	}
	return nil
}

// fetchedRemoteTarget is the scratch ref a remote target is fetched into. It is
// deliberately not under refs/heads or refs/remotes: nothing about inspecting
// the remote may look like a branch this repository keeps.
const fetchedRemoteTarget = "yoyodyne/fetched-remote-target"

// sameContent reports whether two commits carry an identical tree. It is how a
// forge merge commit is told from work the harness never saw: the commits
// differ by construction, the content must not.
func (m *Manager) sameContent(ctx context.Context, one, other string) (bool, error) {
	oneTree, err := m.resolveCommit(ctx, one+"^{tree}")
	if err != nil {
		return false, err
	}
	otherTree, err := m.resolveCommit(ctx, other+"^{tree}")
	if err != nil {
		return false, err
	}
	return oneTree == otherTree, nil
}

// descendsFrom reports whether one commit is part of another's history. Like
// contains, any answer other than a clean yes is a no: a commit this repository
// has never seen makes the question unanswerable, and an unanswered question
// must not authorize a merge.
func (m *Manager) descendsFrom(ctx context.Context, ancestor, descendant string) (bool, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	return result.Status == execution.ProcessSucceeded, nil
}

// resolveCommit resolves any revision this repository has, which is how a
// fetched remote commit and the trees on either side of a comparison are read.
func (m *Manager) resolveCommit(ctx context.Context, revision string) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "--quiet", revision)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve %s failed with exit code %d: %s", revision, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	resolved := strings.TrimSpace(result.Stdout)
	if !commitPattern.MatchString(resolved) {
		return "", fmt.Errorf("resolved object %q for %s is invalid", resolved, revision)
	}
	return resolved, nil
}

// DeleteRemoteBranch removes a merged run branch from the remote it was pushed
// to, which under a fork arrangement is the contributor's fork rather than the
// repository the work was merged into. It is a compare-and-swap on the exact
// published commit, like the local deletion: a remote branch that carries
// anything else is left alone. If its tip is already contained in the remote
// target, leaving it is successful cleanup: no unpublished work is outstanding.
// A moved tip outside that target keeps the refusal. A branch a previous attempt
// already deleted is reported as done rather than as a failure.
func (m *Manager) DeleteRemoteBranch(ctx context.Context, worktree Worktree, commit string) error {
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("published commit %q is invalid", commit)
	}
	if err := validateRef(worktree.Branch); err != nil {
		return err
	}
	published, exists, err := m.remoteCommit(ctx, m.pushRemote, worktree.Branch)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if published != commit {
		if err := validateTargetBranch(worktree.TargetBranch); err != nil {
			return err
		}
		target, exists, err := m.remoteCommit(ctx, m.remote, worktree.TargetBranch)
		if err != nil {
			return err
		}
		if exists {
			if err := m.fetchRemoteBranch(ctx, worktree.TargetBranch, target); err != nil {
				return err
			}
			contained, err := m.descendsFrom(ctx, published, target)
			if err != nil {
				return err
			}
			if contained {
				// Keep the moved branch rather than deleting a ref somebody else
				// may move again. The target already carries the tip we observed.
				return nil
			}
		}
		return fmt.Errorf("remote branch %s is at %s, want the published commit %s; its tip is not contained in target %s on %s",
			worktree.Branch, published, commit, worktree.TargetBranch, m.remote)
	}
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", m.pushRemote, "--delete", "refs/heads/"+worktree.Branch)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("delete remote branch %s failed with exit code %d: %s", worktree.Branch, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// pushBranch advances one branch on the push remote to an exact commit and
// proves it arrived. The refspec is written out in full so the push never
// depends on whatever `push.default` or an upstream configuration happens to
// say.
func (m *Manager) pushBranch(ctx context.Context, branch, commit string) error {
	if err := validateRef(branch); err != nil {
		return err
	}
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", m.pushRemote, commit+":refs/heads/"+branch)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("%w: push %s to %s on %s failed with exit code %d: %s",
			ErrRemotePushRejected, commit, branch, m.pushRemote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	published, exists, err := m.remoteCommit(ctx, m.pushRemote, branch)
	if err != nil {
		return err
	}
	if !exists || published != commit {
		return fmt.Errorf("%w: %s on %s is at %q after the push, want %s", ErrRemotePushRejected, branch, m.pushRemote, published, commit)
	}
	return nil
}

// remoteCommit resolves one branch on a named remote, reporting absence as an
// observation rather than a failure. The remote is named by the caller because
// the two it can be are not interchangeable: a run branch lives on the push
// remote and a target branch on the one the work is published into, and under a
// fork arrangement those are different repositories.
func (m *Manager) remoteCommit(ctx context.Context, remote, branch string) (string, bool, error) {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+branch)
	if err != nil {
		return "", false, err
	}
	switch {
	case result.ExitCode == 2:
		return "", false, nil
	case result.Status != execution.ProcessSucceeded:
		return "", false, fmt.Errorf("resolve %s on %s failed with exit code %d: %s", branch, remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 {
		return "", false, nil
	}
	commit := fields[0]
	if !commitPattern.MatchString(commit) {
		return "", false, fmt.Errorf("remote commit %q for branch %s is invalid", commit, branch)
	}
	return commit, true, nil
}
