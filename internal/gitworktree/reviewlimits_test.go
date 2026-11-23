package gitworktree

import "testing"

// The defaults sit inside the bounds a project is held to, alone and together.
func TestTheReviewLimitDefaultsSitInsideTheirBounds(t *testing.T) {
	t.Parallel()

	if problems := (ReviewLimits{DiffKiB: DefaultMaxDiffBytes >> 10, FileKiB: DefaultMaxDiffFileBytes >> 10, Files: DefaultMaxDiffFiles}).Problems("review_limits"); len(problems) != 0 {
		t.Fatalf("the defaults are refused by their own bounds: %v", problems)
	}
	if problems := (ReviewLimits{}).Problems("review_limits"); len(problems) != 0 {
		t.Fatalf("limits left unset are refused: %v", problems)
	}
}

// The diff limit and the work item context limit may come to 512 KiB together
// and no more, whichever of the two is the large one.
func TestTheDiffAndContextLimitsTogetherAreHeld(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		diff, context int
		refused       bool
	}{{256, 256, false}, {257, 256, true}, {256, 257, true}, {384, 128, false}, {512, 128, true}} {
		problem := ReviewDiffAndContextProblem("review_limits", test.diff<<10, test.context<<10)
		if (problem != "") != test.refused {
			t.Errorf("diff %d KiB with context %d KiB: problem = %q, want refused %t", test.diff, test.context, problem, test.refused)
		}
	}
}

// A limit left unset takes its default where the rendering resolves it.
func TestAnUnsetReviewLimitTakesItsDefault(t *testing.T) {
	t.Parallel()

	resolved, err := (ReviewLimits{Files: 300}).DiffLimits().resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.MaxTotalBytes != DefaultMaxDiffBytes || resolved.MaxFileBytes != DefaultMaxDiffFileBytes || resolved.MaxFiles != 300 {
		t.Fatalf("resolved = %+v, want the two unset limits at their defaults and 300 files", resolved)
	}
}
