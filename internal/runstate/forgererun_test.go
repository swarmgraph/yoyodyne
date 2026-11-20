package runstate

import (
	"strings"
	"testing"
	"time"
)

const forgeRerunHead = "0123456789abcdef0123456789abcdef01234567"

func TestAFurtherRunOfGitHubsChecksIsFixedOnceRecordedAndItsAnswerNeverChanges(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 10, 17, 17, 0, 0, time.UTC)
	recorded := ForgeChecksRerun{HeadCommit: forgeRerunHead, RecordedAt: at, Checks: []string{"build"}, CheckRuns: []int64{7},
		Tests: ForgeTestJudgment{Tests: []ForgeTest{{Name: "TestA", Package: "example.com/m/a"}}}}
	if err := recorded.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	asked := recorded
	answered := at.Add(time.Second)
	asked.AskedAt = &answered
	if refusal := recorded.Extends(&asked); refusal != "" {
		t.Fatalf("recording GitHub's answer was refused: %s", refusal)
	}
	if refusal := asked.Extends(nil); refusal == "" {
		t.Error("a recorded further run could be removed")
	}
	unasked := asked
	unasked.AskedAt = nil
	if refusal := asked.Extends(&unasked); refusal == "" {
		t.Error("GitHub's answer could be taken back")
	}
	other := asked
	other.CheckRuns = []int64{8}
	if refusal := asked.Extends(&other); refusal == "" {
		t.Error("what was asked for could be changed")
	}

	if !asked.Awaiting(forgeRerunHead, []int64{7}) || asked.Awaiting(forgeRerunHead, []int64{9}) || recorded.Awaiting(forgeRerunHead, []int64{7}) {
		t.Error("only a reading of the same commit naming only the asked runs, of a request GitHub accepted, is awaiting the second run")
	}
}

func TestAReadingOfTheSameHeadCarriesItsFurtherRunAndANewHeadStartsAgain(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 10, 17, 17, 0, 0, time.UTC)
	prior := PullRequestChecks{HeadCommit: forgeRerunHead, ReadAt: at,
		UntouchedRerun: &ForgeChecksRerun{HeadCommit: forgeRerunHead, RecordedAt: at, Checks: []string{"build"}, CheckRuns: []int64{7}, AskedAt: &at}}
	same := PullRequestChecks{HeadCommit: forgeRerunHead, ReadAt: at, Failing: []FailingCheck{{Name: "build", CheckRun: 8}},
		Tests: &ForgeTestJudgment{Tests: []ForgeTest{{Name: "TestA", Package: "example.com/m/a"}}}}
	same.CarryFrom(&prior)
	if same.UntouchedRerun == nil {
		t.Fatal("the further run was not carried to a reading of the same head")
	}
	said := same.Describe("main")
	for _, want := range []string{"TestA in example.com/m/a", "the failing tests the change does not touch", "and it failed again"} {
		if !strings.Contains(said, want) {
			t.Errorf("Describe() does not say %q: %s", want, said)
		}
	}
	other := PullRequestChecks{HeadCommit: strings.Repeat("f", 40), ReadAt: at}
	other.CarryFrom(&prior)
	if other.UntouchedRerun != nil {
		t.Error("a new head was given the old head's further run")
	}
}
