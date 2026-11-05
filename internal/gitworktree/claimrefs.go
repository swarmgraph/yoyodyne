package gitworktree

// Claims as Git references on the shared remote, for team mode
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// A claim is refs/yoyodyne/claims/<item id> on the remote the target branch
// lives on, pointing at a small commit whose message is the claim record. Every
// write to one is a push with a lease naming exactly what the harness expects
// the reference to be: nothing when taking, the commit it last pushed when
// renewing or releasing, and the stale commit when taking one over. The remote's
// own compare and swap is the whole of the guarantee across machines, so two
// factories racing for one item cannot both win.
//
// This file is only the transport. What a claim record says, and when to take,
// renew, take over or release one, is internal/teamclaim's.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ClaimRefPrefix is where claims live on the shared remote.
const ClaimRefPrefix = "refs/yoyodyne/claims/"

// fetchedClaimRefPrefix is where the claims fetched from the remote are kept in
// the local repository, apart from refs/yoyodyne/claims/ so a local reference
// is never mistaken for one the remote holds.
const fetchedClaimRefPrefix = "refs/yoyodyne/fetched-claims/"

// claimProbeRef is the reference `yoyo doctor` pushes and deletes to learn
// whether the forge accepts references under refs/yoyodyne/.
const claimProbeRef = "refs/yoyodyne/probe/"

// ErrClaimLeaseRefused is a claim push the remote refused because the reference
// was not where the lease said it would be: another factory took the item
// first, renewed it, took it over, or released it. It is an answer about who
// holds the item, never a failure to reach the remote.
var ErrClaimLeaseRefused = errors.New("the claim reference on the remote was not where the harness expected it")

// ErrClaimRefRefused is a remote that will not hold a reference under
// refs/yoyodyne/ at all, which a forge can be configured to do. Team mode cannot
// run on such a forge.
var ErrClaimRefRefused = errors.New("the remote refused a reference under refs/yoyodyne/")

// ClaimReading is one claim reference as the last fetch found it.
type ClaimReading struct {
	// Exists is false where the remote holds no claim on the item.
	Exists bool
	// Commit is the claim commit the remote's reference points at.
	Commit string
	// Record is the claim record the commit carries, as written.
	Record string
	// CommittedAt is when the claim commit was made: when it was taken or last
	// renewed, whatever its record says.
	CommittedAt time.Time
}

// ClaimRef is the reference a claim on one item is kept at.
func ClaimRef(workItemID string) (string, error) {
	ref := ClaimRefPrefix + workItemID
	if strings.TrimSpace(workItemID) == "" || strings.Contains(workItemID, "/") || strings.HasSuffix(workItemID, ".lock") {
		return "", fmt.Errorf("work item %q cannot name a claim reference", workItemID)
	}
	if err := validateRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

// ClaimRemote names the remote claims are pushed to: the one the target branch
// lives on, which is the one every factory working the repository shares.
func (m *Manager) ClaimRemote() string { return m.remote }

// FetchClaims copies every claim reference the remote holds into the local
// repository, and drops the local copy of any the remote no longer holds.
func (m *Manager) FetchClaims(ctx context.Context) error {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"fetch", "--no-tags", "--prune", "--no-write-fetch-head", m.remote,
		"+"+ClaimRefPrefix+"*:"+fetchedClaimRefPrefix+"*")
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("fetch the claims from %s failed with exit code %d: %s", m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// FetchedClaim reads one claim as the last FetchClaims found it.
func (m *Manager) FetchedClaim(ctx context.Context, workItemID string) (ClaimReading, error) {
	if _, err := ClaimRef(workItemID); err != nil {
		return ClaimReading{}, err
	}
	resolved, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "--quiet", fetchedClaimRefPrefix+workItemID+"^{commit}")
	if err != nil {
		return ClaimReading{}, err
	}
	if resolved.Status != execution.ProcessSucceeded {
		return ClaimReading{}, nil
	}
	commit := strings.TrimSpace(resolved.Stdout)
	if !commitPattern.MatchString(commit) {
		return ClaimReading{}, fmt.Errorf("fetched claim commit %q for %s is invalid", commit, workItemID)
	}
	message, err := m.run(ctx, "-C", m.repositoryRoot, "show", "-s", "--format=%ct%n%B", commit)
	if err != nil {
		return ClaimReading{}, err
	}
	if message.Status != execution.ProcessSucceeded {
		return ClaimReading{}, fmt.Errorf("read the claim on %s failed with exit code %d: %s", workItemID, message.ExitCode, strings.TrimSpace(message.Stderr))
	}
	stamp, record, _ := strings.Cut(message.Stdout, "\n")
	reading := ClaimReading{Exists: true, Commit: commit, Record: strings.TrimSpace(record)}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64); err == nil {
		reading.CommittedAt = time.Unix(seconds, 0).UTC()
	}
	return reading, nil
}

// PushClaim writes a claim commit carrying record and pushes it to the item's
// claim reference with a lease: expected is the commit the reference must be at
// for the push to land, and empty means the reference must not exist. parent,
// where given, is the claim commit this one follows, so a claim's history is its
// renewals. A lease the remote refused ends on ErrClaimLeaseRefused.
func (m *Manager) PushClaim(ctx context.Context, workItemID, record, parent, expected string, now time.Time) (string, error) {
	ref, err := ClaimRef(workItemID)
	if err != nil {
		return "", err
	}
	commit, err := m.claimCommit(ctx, record, parent, now)
	if err != nil {
		return "", err
	}
	if err := m.pushClaimRef(ctx, ref, commit, expected); err != nil {
		return "", err
	}
	return commit, nil
}

// DeleteClaim removes the item's claim reference from the remote, with a lease
// naming the commit the harness last pushed there. A reference that has moved
// since is left alone and ends on ErrClaimLeaseRefused.
func (m *Manager) DeleteClaim(ctx context.Context, workItemID, expected string) error {
	ref, err := ClaimRef(workItemID)
	if err != nil {
		return err
	}
	if !commitPattern.MatchString(expected) {
		return fmt.Errorf("claim commit %q is invalid", expected)
	}
	return m.pushClaimRef(ctx, ref, "", expected)
}

// ProbeClaimReferences asks the remote whether it accepts a reference under
// refs/yoyodyne/, by pushing one of its own and deleting it again. It is what
// `yoyo doctor` asks before team mode may run. A remote that refuses ends on
// ErrClaimRefRefused; one that cannot be reached ends on the failure itself.
func (m *Manager) ProbeClaimReferences(ctx context.Context, name string, now time.Time) error {
	ref := claimProbeRef + name
	if err := validateRef(ref); err != nil {
		return err
	}
	commit, err := m.claimCommit(ctx, "yoyo doctor checking that this remote accepts references under refs/yoyodyne/", "", now)
	if err != nil {
		return err
	}
	if err := m.pushClaimRef(ctx, ref, commit, ""); err != nil {
		return err
	}
	return m.pushClaimRef(ctx, ref, "", commit)
}

// claimCommit writes a commit of the empty tree whose message is the record. It
// is written in the repository's object store and referenced by nothing local,
// so the next garbage collection takes the ones the remote no longer holds.
func (m *Manager) claimCommit(ctx context.Context, record, parent string, now time.Time) (string, error) {
	if strings.TrimSpace(record) == "" {
		return "", errors.New("a claim commit needs a record")
	}
	tree, err := m.run(ctx, "-C", m.repositoryRoot, "hash-object", "-t", "tree", "-w", os.DevNull)
	if err != nil {
		return "", err
	}
	if tree.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("write the empty tree failed with exit code %d: %s", tree.ExitCode, strings.TrimSpace(tree.Stderr))
	}
	args := []string{"-C", m.repositoryRoot, "commit-tree", "--no-gpg-sign", strings.TrimSpace(tree.Stdout)}
	if parent != "" {
		if !commitPattern.MatchString(parent) {
			return "", fmt.Errorf("claim parent commit %q is invalid", parent)
		}
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", record)
	stamp := fmt.Sprintf("%d +0000", now.UTC().Unix())
	environment := append(harnessCommitEnvironment(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	made, err := m.runWithEnvironment(ctx, environment, args...)
	if err != nil {
		return "", err
	}
	if made.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("write the claim commit failed with exit code %d: %s", made.ExitCode, strings.TrimSpace(made.Stderr))
	}
	commit := strings.TrimSpace(made.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("claim commit %q is invalid", commit)
	}
	return commit, nil
}

// pushClaimRef moves ref on the remote to commit, or deletes it where commit is
// empty, with a lease naming expected (empty: the reference must not exist).
//
// The push is made with --porcelain so the remote's verdict on the one
// reference can be read apart from a failure to reach it: a lease refused is
// "stale info" or a reference no longer where the lease expected it, and a
// reference refused while still where it was expected is the forge declining
// references under refs/yoyodyne/.
func (m *Manager) pushClaimRef(ctx context.Context, ref, commit, expected string) error {
	if expected != "" && !commitPattern.MatchString(expected) {
		return fmt.Errorf("expected claim commit %q is invalid", expected)
	}
	for attempt := 1; ; attempt++ {
		line, err := m.pushClaimRefOnce(ctx, ref, commit, expected)
		if err != nil || line == "" {
			return err
		}
		// A push that lost a race inside the remote is refused in the remote's
		// own words ("failed to update ref": another push held the reference's
		// lock) rather than as stale info, and the winner's update may not be
		// readable for a while after. So the reference is read again: one no
		// longer where the lease expected it is another factory having moved it.
		// One still where it was is pushed again after a wait while the refusal
		// is that lock, and taken as the remote refusing references under
		// refs/yoyodyne/ when it is anything else, such as a hook that declined.
		current, err := m.remoteRef(ctx, ref)
		if err != nil {
			return fmt.Errorf("push %s to %s was refused (%s), and the reference could not be read again: %w", ref, m.remote, line, err)
		}
		if current != expected {
			return fmt.Errorf("%w: %s on %s", ErrClaimLeaseRefused, ref, m.remote)
		}
		if !lostRefLock(line) {
			return fmt.Errorf("%w: %s on %s: %s", ErrClaimRefRefused, ref, m.remote, line)
		}
		if attempt >= claimPushAttempts {
			return fmt.Errorf("push %s to %s kept losing the remote's lock on it to another push (%s)", ref, m.remote, line)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * claimPushRetryWait):
		}
	}
}

// lostRefLock reports a refusal that is another push holding the reference on
// the remote, rather than the remote refusing the reference itself.
func lostRefLock(line string) bool {
	return strings.Contains(line, "failed to update ref") || strings.Contains(line, "failed to lock") || strings.Contains(line, "cannot lock ref")
}

// claimPushAttempts bounds how many times a claim push that lost the remote's
// lock on the reference is made, with the reference still where the lease
// expected it, before it is reported as a push that did not go through. With
// the waits below that is about fourteen seconds of waiting.
const claimPushAttempts = 11

// claimPushRetryWait is the first wait before such a push is made again; each
// later wait is one more of it.
const claimPushRetryWait = 250 * time.Millisecond

// pushClaimRefOnce makes one claim push. It returns nil and an empty line where
// the push landed, ErrClaimLeaseRefused where the remote said the lease was
// stale, and the remote's own words for any other refusal of the reference,
// which the caller judges by reading the reference again.
func (m *Manager) pushClaimRefOnce(ctx context.Context, ref, commit, expected string) (string, error) {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", "--porcelain", "--no-verify",
		"--force-with-lease="+ref+":"+expected,
		m.remote, commit+":"+ref)
	if err != nil {
		return "", err
	}
	if result.Status == execution.ProcessSucceeded {
		return "", nil
	}
	line, found := porcelainRefLine(result.Stdout, ref)
	if !found || !strings.HasPrefix(line, "!") {
		return "", fmt.Errorf("push %s to %s failed with exit code %d: %s", ref, m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	if strings.Contains(line, "stale info") {
		return "", fmt.Errorf("%w: %s on %s", ErrClaimLeaseRefused, ref, m.remote)
	}
	return strings.TrimSpace(line), nil
}

// remoteRef is the commit ref is at on the claims remote, and empty where the
// remote holds no such reference.
func (m *Manager) remoteRef(ctx context.Context, ref string) (string, error) {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot, "ls-remote", "--exit-code", m.remote, ref)
	if err != nil {
		return "", err
	}
	switch {
	case result.ExitCode == 2:
		return "", nil
	case result.Status != execution.ProcessSucceeded:
		return "", fmt.Errorf("read %s on %s failed with exit code %d: %s", ref, m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			return fields[0], nil
		}
	}
	return "", nil
}

// porcelainRefLine finds the line `git push --porcelain` printed for ref.
func porcelainRefLine(output, ref string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && strings.HasSuffix(fields[1], ":"+ref) {
			return fields[0] + " " + fields[2], true
		}
	}
	return "", false
}
