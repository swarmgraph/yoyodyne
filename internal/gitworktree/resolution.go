package gitworktree

// Comparing a resolved conflict with Git's own merge, for decision 4 of
// docs/designs/approved-changes-that-no-longer-merge.md: a resolution is
// confined to the conflicting files when every other file is exactly as Git's
// merge of the approved head onto the same target leaves it.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// maxResolutionDiffBytes bounds the diff from Git's merge to the resolution
// that ResolutionComparison carries for the candidate's reviewer.
const maxResolutionDiffBytes = 48 << 10

// ResolutionComparison is what comparing a resolved change with Git's merge of
// the approved head found.
type ResolutionComparison struct {
	// MergedTree is the tree Git's merge of the approved head onto the target
	// leaves, with its conflict markers in the conflicting files.
	MergedTree string
	// Conflicting are the files Git's merge could not settle, as it listed them.
	Conflicting []string
	// Outside are the files outside Conflicting whose content in the resolved
	// head differs from Git's merge. A confined resolution has none.
	Outside []string
	// Resolutions is the diff from Git's merge to the resolved head over the
	// conflicting files: how each conflict was resolved. Truncated says it was
	// cut at its bound.
	Resolutions string
	Truncated   bool
}

// Confined reports a resolution that changed nothing outside the files Git
// could not merge.
func (c ResolutionComparison) Confined() bool {
	return len(c.Outside) == 0
}

// CompareResolution merges approvedHead onto target in memory, as Git would,
// and compares the result with resolvedHead file by file. resolvedHead must be
// built on target, because the comparison is with a merge onto that target and
// no other. A merge that does not conflict at all is compared the same way: any
// file the resolution changed is then outside the (empty) conflicting set.
func (m *Manager) CompareResolution(ctx context.Context, target, approvedHead, resolvedHead string) (ResolutionComparison, error) {
	for _, commit := range []string{target, approvedHead, resolvedHead} {
		if !commitPattern.MatchString(commit) {
			return ResolutionComparison{}, fmt.Errorf("%q is not a full commit id", commit)
		}
	}
	ancestor, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", target, resolvedHead)
	if err != nil {
		return ResolutionComparison{}, err
	}
	switch {
	case ancestor.ExitCode == 1:
		return ResolutionComparison{}, fmt.Errorf("the resolved head %s is not built on %s", resolvedHead, target)
	case ancestor.Status != execution.ProcessSucceeded:
		return ResolutionComparison{}, fmt.Errorf("ask whether %s is built on %s: exit code %d: %s", resolvedHead, target, ancestor.ExitCode, strings.TrimSpace(ancestor.Stderr))
	}
	merged, err := m.run(ctx, "-C", m.repositoryRoot, "merge-tree", "--write-tree", "--no-messages", "-z", target, approvedHead)
	if err != nil {
		return ResolutionComparison{}, err
	}
	if merged.ExitCode != 0 && merged.ExitCode != 1 {
		return ResolutionComparison{}, fmt.Errorf("merge %s onto %s in memory failed with exit code %d: %s", approvedHead, target, merged.ExitCode, strings.TrimSpace(merged.Stderr))
	}
	tree, rest, _ := strings.Cut(merged.Stdout, "\x00")
	tree = strings.TrimSpace(tree)
	if !commitPattern.MatchString(tree) {
		return ResolutionComparison{}, fmt.Errorf("merge %s onto %s in memory answered no tree: %q", approvedHead, target, strings.TrimSpace(merged.Stdout))
	}
	comparison := ResolutionComparison{MergedTree: tree}
	if merged.ExitCode == 1 {
		comparison.Conflicting, _ = unmergedFiles(rest)
	}
	differing, err := m.run(ctx, "-C", m.repositoryRoot, "diff-tree", "-r", "-z", "--no-renames", "--name-only", tree, resolvedHead+"^{tree}")
	if err != nil {
		return ResolutionComparison{}, err
	}
	if differing.Status != execution.ProcessSucceeded {
		return ResolutionComparison{}, fmt.Errorf("compare %s with Git's merge: exit code %d: %s", resolvedHead, differing.ExitCode, strings.TrimSpace(differing.Stderr))
	}
	// Each path is ended by a NUL; the output's own last line ending is not one.
	for _, path := range strings.Split(strings.TrimSuffix(differing.Stdout, "\n"), "\x00") {
		if path != "" && !slices.Contains(comparison.Conflicting, path) {
			comparison.Outside = append(comparison.Outside, path)
		}
	}
	if len(comparison.Conflicting) == 0 {
		return comparison, nil
	}
	args := append([]string{"-C", m.repositoryRoot, "diff", "--no-color", "--no-ext-diff", "--no-renames", tree, resolvedHead + "^{tree}", "--"}, comparison.Conflicting...)
	resolved, err := m.run(ctx, args...)
	if err != nil {
		return ResolutionComparison{}, err
	}
	if resolved.Status != execution.ProcessSucceeded {
		return ResolutionComparison{}, fmt.Errorf("show how the conflicts were resolved: exit code %d: %s", resolved.ExitCode, strings.TrimSpace(resolved.Stderr))
	}
	comparison.Resolutions = resolved.Stdout
	if len(comparison.Resolutions) > maxResolutionDiffBytes {
		cut := maxResolutionDiffBytes
		for cut > 0 && !utf8.RuneStart(comparison.Resolutions[cut]) {
			cut--
		}
		comparison.Resolutions, comparison.Truncated = comparison.Resolutions[:cut], true
	}
	return comparison, nil
}
