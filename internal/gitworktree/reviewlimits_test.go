package gitworktree

import "testing"

// The defaults sit inside the bounds a project is held to, and the diff
// ceiling is no higher than the default (reviewlimits.go says why).
func TestTheReviewLimitDefaultsSitInsideTheirBounds(t *testing.T) {
	t.Parallel()

	if problems := (ReviewLimits{DiffKiB: DefaultMaxDiffBytes >> 10, FileKiB: DefaultMaxDiffFileBytes >> 10, Files: DefaultMaxDiffFiles}).Problems("review_limits"); len(problems) != 0 {
		t.Fatalf("the defaults are refused by their own bounds: %v", problems)
	}
	if ceiling := ReviewDiffCeiling(); ceiling > MaxReviewDiffBytes || ceiling > DefaultMaxDiffBytes {
		t.Fatalf("the diff ceiling %d is above the %d-byte default, which the measured review evidence leaves no room beyond", ceiling, DefaultMaxDiffBytes)
	}
	if ReviewDiffCeiling() < MinReviewDiffBytes {
		t.Fatalf("the diff ceiling %d is below the lowest diff limit %d, so no diff limit could be set", ReviewDiffCeiling(), MinReviewDiffBytes)
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
