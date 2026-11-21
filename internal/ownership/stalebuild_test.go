package ownership

import "testing"

// A stale build is never the operator's: a move the session or the supervisor
// makes by itself is the harness's, a move that did not happen and a main line
// that does not build are the development manager's, and uncommitted changes in
// the checkout, which the closed list has no reason for, are the Lead Product
// Manager's to classify.
func TestAStaleBuildIsNeverTheOperators(t *testing.T) {
	t.Parallel()
	want := map[StaleBuildCause]Mover{
		StaleBuildMoveWaiting:  MoverHarness,
		StaleBuildMoveDue:      MoverHarness,
		StaleBuildMoveStuck:    MoverDevelopmentManager,
		StaleBuildNotBuilt:     MoverDevelopmentManager,
		StaleBuildUncommitted:  MoverProductManager,
		StaleBuildUnsupervised: MoverHarness,
	}
	if len(want) != len(StaleBuildCauses()) {
		t.Fatalf("the test covers %d causes and the vocabulary has %d", len(want), len(StaleBuildCauses()))
	}
	for _, cause := range StaleBuildCauses() {
		got := ResolveStaleBuild(cause)
		if got.Owner != want[cause] || got.Owner.IsOperator() || got.Remedy == "" {
			t.Errorf("%s: ResolveStaleBuild = %+v, want owner %s with a remedy", cause, got, want[cause])
		}
	}
	if got := ResolveStaleBuild("something-nobody-named"); got.Owner != MoverProductManager {
		t.Errorf("an unknown cause resolved to %+v, want the Lead Product Manager to classify it", got)
	}
}
