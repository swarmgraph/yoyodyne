package gitworktree

// The aftermath of a forge merge. A promotion moves the local target branch and
// the forge then merges the pull request that carries it, which leaves the
// remote target one merge commit ahead of the local one and identical in
// content. Nothing about that is a judgement — the merge already happened, the
// commit above it is the forge's own, and the two branches carry the same
// tree — so bringing the local branch onto it is machinery's work rather than an
// operator's. What was manual before was exactly this: pulling after a merge,
// discarding the export churn that stopped the pull, and deleting the run
// branches whose work the target already carries.
//
// Every write here is the same fast-forward-and-nothing-else the promotion
// itself is. A remote target that has moved somewhere the local branch cannot
// fast-forward onto is held rather than reconciled, because a divergence is the
// one thing in this file that does need a person.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// Catchup is what one attempt to bring a local target branch onto the forge's
// truth found and did. Held is why the local branch was left where it is; it is
// empty both when the branch advanced and when it was already there, and
// Advanced is what tells those two apart.
type Catchup struct {
	TargetBranch string `json:"target_branch"`
	// LocalCommit is where the local branch was before this attempt, and
	// RemoteCommit where the remote has it. An empty RemoteCommit means the
	// remote has no such branch, which is the ordinary state of a repository
	// that has never published.
	LocalCommit  string `json:"local_commit,omitempty"`
	RemoteCommit string `json:"remote_commit,omitempty"`
	Advanced     bool   `json:"advanced"`
	// Discarded names the export files whose uncommitted churn was thrown away
	// to let the fast-forward through. Only paths the project declared as
	// control-plane exports are ever discarded, and only when the incoming
	// commits actually rewrite them.
	Discarded []string `json:"discarded,omitempty"`
	Held      string   `json:"held,omitempty"`
}

// Removal is what one attempt to delete a merged run branch found. A branch
// that is already gone reports an empty Commit, which is how a caller tells
// "there was nothing to remove" from "it was kept" without inspecting the text.
type Removal struct {
	Branch  string `json:"branch"`
	Commit  string `json:"commit,omitempty"`
	Removed bool   `json:"removed"`
	Kept    string `json:"kept,omitempty"`
}

// CatchUpTarget fast-forwards one local target branch onto wherever the remote
// has it, and refuses anything that is not a fast-forward. It is the local half
// of a merge the forge performed: the promoted commit is already on both sides,
// and what the local branch is missing is the merge commit the forge made above
// it.
//
// Nothing here decides anything. A remote branch that does not contain the
// local one has work the harness never promoted, or a history somebody rewrote,
// and either way which of the two is right is a person's answer rather than a
// sweep's — so it is reported and the local branch is left exactly where it is.
//
// The update is not a compare-and-swap when the primary checkout is on the
// target, because a fast-forward-only merge is what keeps that checkout's
// working tree consistent with the branch it is on and Git offers no expected
// value for one. Every caller holds the target branch's promotion lease, which
// is what makes the read-then-move below safe against a promotion running
// beside it.
func (m *Manager) CatchUpTarget(ctx context.Context, targetBranch string) (Catchup, error) {
	catchup := Catchup{TargetBranch: targetBranch}
	if err := validateTargetBranch(targetBranch); err != nil {
		return catchup, err
	}
	if err := m.validateRepository(ctx); err != nil {
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
	if err != nil {
		return catchup, err
	}
	if !onRemote {
		catchup.Held = fmt.Sprintf("%s does not exist on %s, so there is nothing to catch up to", targetBranch, m.remote)
		return catchup, nil
	}
	catchup.RemoteCommit = published
	if published == local {
		return catchup, nil
	}
	// The remote tip is a commit this repository does not have — a forge merge
	// commit is one by construction — so it is brought in before anything is
	// asked about it. A branch that moved between being resolved and being
	// fetched is refused there rather than answered about.
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
		return catchup, nil
	}
	inPrimary, err := m.targetCheckout(ctx, targetBranch)
	if err != nil {
		return catchup, err
	}
	if !inPrimary {
		if err := m.advanceRef(ctx, targetBranch, published, local); err != nil {
			return catchup, err
		}
		catchup.Advanced = true
		return catchup, nil
	}
	discarded, held, err := m.clearCatchUpPath(ctx, local, published)
	if err != nil {
		return catchup, err
	}
	catchup.Discarded = discarded
	if held != "" {
		catchup.Held = held
		return catchup, nil
	}
	merged, err := m.run(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"merge", "--ff-only", published)
	if err != nil {
		return catchup, err
	}
	if merged.Status != execution.ProcessSucceeded {
		return catchup, fmt.Errorf("%w: catch %s up to %s from %s failed with exit code %d: %s",
			ErrNotFastForward, targetBranch, published, m.remote, merged.ExitCode, strings.TrimSpace(merged.Stderr))
	}
	arrived, err := m.resolveBranchCommit(ctx, targetBranch)
	if err != nil {
		return catchup, err
	}
	if arrived != published {
		return catchup, fmt.Errorf("%w: %s is at %s after the catch-up, want %s", ErrNotFastForward, targetBranch, arrived, published)
	}
	catchup.Advanced = true
	return catchup, nil
}

// advanceRef moves a branch no checkout is on, as a compare-and-swap on where
// it was read. It is the same discipline the promotion uses for a target the
// primary checkout is not sitting on: a branch that moved since it was resolved
// loses the race rather than being overwritten.
func (m *Manager) advanceRef(ctx context.Context, branch, commit, previous string) error {
	updated, err := m.run(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", "refs/heads/"+branch, commit, previous)
	if err != nil {
		return err
	}
	if updated.Status != execution.ProcessSucceeded {
		return fmt.Errorf("%w: catch %s up to %s failed with exit code %d: %s",
			ErrNotFastForward, branch, commit, updated.ExitCode, strings.TrimSpace(updated.Stderr))
	}
	return nil
}

// clearCatchUpPath makes room in the primary checkout for the incoming commits,
// and reports what still stands in the way when it cannot.
//
// Only two kinds of uncommitted change can block a fast-forward, and they are
// not the same thing. A control-plane export the project declared — a work
// tracker's passive JSONL dump is the one this exists for — is derived from a
// store that is authoritative elsewhere, so churn in it is thrown away rather
// than allowed to hold the checkout behind the forge indefinitely. Anything
// else is somebody's unsaved work, and the catch-up is held for them: a branch
// one commit behind costs nothing, and an overwritten edit cannot be undone.
func (m *Manager) clearCatchUpPath(ctx context.Context, local, published string) ([]string, string, error) {
	incoming, err := m.changedBetween(ctx, local, published)
	if err != nil {
		return nil, "", err
	}
	if len(incoming) == 0 {
		return nil, "", nil
	}
	status, err := m.primaryChanges(ctx)
	if err != nil {
		return nil, "", err
	}
	// An export that exists only in the working tree has no committed content to
	// be restored to, so it is in the way exactly as an unsaved edit is however
	// the project declared it.
	inTheWay := append(append([]string{}, status.unexpected...), status.untrackedExports...)
	if blocked := intersect(incoming, inTheWay); len(blocked) > 0 {
		return nil, fmt.Sprintf("uncommitted changes in the primary checkout would be overwritten by the catch-up: %s", strings.Join(blocked, ", ")), nil
	}
	churn := intersect(incoming, status.exports)
	if len(churn) == 0 {
		return nil, "", nil
	}
	// The index is restored alongside the working tree, so a staged export is
	// discarded as completely as an unstaged one. Every path here came from the
	// project's own declared list rather than from anything observed, so the
	// pathspec below can only ever name a control-plane export.
	args := []string{"-C", m.repositoryRoot, "-c", "core.hooksPath=" + os.DevNull, "checkout", "HEAD", "--"}
	restored, err := m.run(ctx, append(args, churn...)...)
	if err != nil {
		return nil, "", err
	}
	if restored.Status != execution.ProcessSucceeded {
		return nil, "", fmt.Errorf("discard export churn in %s failed with exit code %d: %s",
			strings.Join(churn, ", "), restored.ExitCode, strings.TrimSpace(restored.Stderr))
	}
	return churn, "", nil
}

// changedBetween lists the repository-relative paths two commits differ in,
// which is what decides whether an uncommitted change is actually in the way. A
// file nothing incoming rewrites survives a fast-forward untouched, so holding
// the catch-up for it would be holding it for nothing.
func (m *Manager) changedBetween(ctx context.Context, from, to string) ([]string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "diff", "--name-only", "--no-ext-diff", "-z", from, to, "--")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the incoming changes between %s and %s failed with exit code %d: %s",
			from, to, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var paths []string
	for _, path := range strings.Split(result.Stdout, "\x00") {
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	return paths, nil
}

// RemoveMergedBranch deletes a run branch whose work its target branch already
// carries. Containment is the whole of the evidence and it is proved here rather
// than taken from a record: a branch contained in the target has nothing on it
// that deleting it could lose, and a branch that is not is somebody's preserved
// work whatever a run once said about it.
//
// A branch a checkout still holds is kept, and one already gone is reported as
// nothing to do rather than as a removal.
func (m *Manager) RemoveMergedBranch(ctx context.Context, branch, targetBranch string) (Removal, error) {
	removal := Removal{Branch: branch}
	if err := validateRef(branch); err != nil {
		return removal, err
	}
	if err := validateTargetBranch(targetBranch); err != nil {
		return removal, err
	}
	if branch == targetBranch {
		return removal, fmt.Errorf("branch %s is its own target branch", branch)
	}
	commit, exists, err := m.optionalBranchCommit(ctx, branch)
	if err != nil {
		return removal, err
	}
	if !exists {
		return removal, nil
	}
	removal.Commit = commit
	_, targetExists, err := m.optionalBranchCommit(ctx, targetBranch)
	if err != nil {
		return removal, err
	}
	if !targetExists {
		removal.Kept = fmt.Sprintf("%s is not a branch of this repository, so nothing proves %s was promoted", targetBranch, branch)
		return removal, nil
	}
	contained, err := m.contains(ctx, commit, targetBranch)
	if err != nil {
		return removal, err
	}
	if !contained {
		removal.Kept = fmt.Sprintf("%s at %s is not contained in %s, so it still carries work nothing has promoted", branch, commit, targetBranch)
		return removal, nil
	}
	removed, err := m.deleteIntegratedBranch(ctx, branch, commit)
	if err != nil {
		// A branch a working tree still holds is kept rather than reported as a
		// failure: that checkout is what a person is using it for, and a sweep
		// that failed over it would fail on every later pass too.
		if strings.Contains(err.Error(), "still checked out in") {
			removal.Kept = err.Error()
			return removal, nil
		}
		return removal, err
	}
	removal.Removed = removed
	return removal, nil
}

// primaryStatus is the uncommitted state of the primary checkout, split by what
// the harness may do about each part. Only a declared export with committed
// content behind it is ever thrown away; a declared export that exists only in
// the working tree, and anything the project never declared, are somebody
// else's to resolve. An export's in-flight temp file is in none of the three:
// it is not a change anybody made, and it will not exist a moment from now.
type primaryStatus struct {
	exports          []string
	untrackedExports []string
	unexpected       []string
}

// primaryChanges reads that state. The split it makes is not the one
// ValidateReady makes: readiness only asks whether an uncommitted change was
// declared at all, and a declared export is as acceptable there untracked as
// tracked, so unexpected keeps exactly the meaning it always had.
func (m *Manager) primaryChanges(ctx context.Context) (primaryStatus, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return primaryStatus{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return primaryStatus{}, fmt.Errorf("inspect primary Git status failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var status primaryStatus
	for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(line) < 4 || strings.Contains(line, " -> ") {
			status.unexpected = append(status.unexpected, line)
			continue
		}
		path := filepath.ToSlash(line[3:])
		if _, allowed := m.allowedPrimaryChanges[path]; !allowed {
			// An export being rewritten right now is not a change anybody made, and
			// it is gone by the time anybody could look at it. Reading one as
			// unsaved work is how a round that had already passed every gate came
			// to be thrown away twice in one afternoon.
			if strings.HasPrefix(line, "??") && isExportWriteInProgress(m.allowedPrimaryChanges, path) {
				continue
			}
			status.unexpected = append(status.unexpected, path)
			continue
		}
		if strings.HasPrefix(line, "??") {
			status.untrackedExports = append(status.untrackedExports, path)
			continue
		}
		status.exports = append(status.exports, path)
	}
	return status, nil
}

// exportWritePrefix is what an atomic rewrite of a declared export leaves
// beside it while the write is in flight. The writer creates its temp file in
// the target's own directory, named for the target with a marker in front and a
// unique suffix behind — `.beads/.~issues.jsonl.2847119036` for
// `.beads/issues.jsonl` — and renames it over the target once the content is
// down. The window is milliseconds wide and it recurs on every export, which is
// what makes reading one as somebody's uncommitted work a race rather than a
// fault: a check landing inside the window refuses, and the same check a moment
// later passes.
//
// The suffix is deliberately not matched any more precisely than "something,
// and no path separator in it". What has actually been observed is a run of
// digits, but the exporter is somebody else's software: pinning its choice of
// randomness here would turn a version bump into the same discarded round this
// exists to prevent, and the prefix already names one declared file exactly.
func exportWritePrefix(declared string) string {
	separator := strings.LastIndex(declared, "/")
	return declared[:separator+1] + ".~" + declared[separator+1:] + "."
}

// isExportWriteInProgress reports whether an untracked path is a declared
// export's temp file rather than a file somebody put there. Only an untracked
// path is ever asked: a tracked file by that name is committed content, and
// nothing about how it got its name makes it transient.
//
// This repository also ignores them, so in this checkout they usually never
// reach Git's status at all: the root .gitignore carries a `.beads/.~*` entry.
// The two are not redundant. The entry is one line in one project's tracked
// content, which the exporter's own `bd setup` rewrites for the file beneath
// `.beads/` and which a second project managed by this harness has no reason to
// have; the check is the half that travels, and it is the only half a
// repository without that line has.
//
// Readiness has no staleness bound: it cannot tell a dead exporter from a live
// holder. Tracker maintenance makes that distinction and removes abandoned
// files; readiness only leaves the write alone, including the confined writer's
// own temporary when the harness is publishing a fresh snapshot.
func isExportWriteInProgress(declared map[string]struct{}, path string) bool {
	for export := range declared {
		if filepath.Dir(path) == filepath.Dir(export) && repowrite.IsTemporaryFile(filepath.Base(path)) {
			return true
		}
		prefix := exportWritePrefix(export)
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		suffix := path[len(prefix):]
		if suffix != "" && !strings.Contains(suffix, "/") {
			return true
		}
	}
	return false
}

func (m *Manager) unexpectedPrimaryChanges(ctx context.Context) ([]string, error) {
	status, err := m.primaryChanges(ctx)
	return status.unexpected, err
}

// intersect reports the members of want that appear in have, in want's order
// and without repeats.
func intersect(want, have []string) []string {
	if len(want) == 0 || len(have) == 0 {
		return nil
	}
	present := make(map[string]struct{}, len(have))
	for _, value := range have {
		present[value] = struct{}{}
	}
	var both []string
	for _, value := range want {
		if _, ok := present[value]; !ok {
			continue
		}
		if !slices.Contains(both, value) {
			both = append(both, value)
		}
	}
	return both
}
