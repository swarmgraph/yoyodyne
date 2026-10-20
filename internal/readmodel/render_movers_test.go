package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The fourth line lists only what waits on the operator under "Needs a human",
// and every other mover's entries under a line naming that mover, each head
// counting its own. On 2026-09-27 the dashboard said "held for a person" over
// thirty-four items the development manager was moving; this holds every mover
// the vocabulary has to a label naming it, and the words "a person" and "a
// human" to the operator's part of the line.
func TestTheFourthLineNamesEachMoverAndSaysAHumanOnlyOfTheOperator(t *testing.T) {
	t.Parallel()

	wants := map[Mover]string{
		ownership.MoverOperator: "Needs a human (1):\n",
		MoverProductManager:     "Waiting on the Lead Product Manager (1):\n",
		MoverArchitect:          "Waiting on the architect (1):\n",
		MoverDevelopmentManager: "Waiting on the development manager (1):\n",
		MoverProgramManager:     "Waiting on the program manager (1):\n",
		Mover("developer"):      "Waiting on the developer (1):\n",
		Mover("reviewer"):       "Waiting on the reviewer (1):\n",
		MoverHarness:            "Waiting on the harness (1):\n",
		MoverForge:              "Waiting on the forge (1):\n",
		MoverProvider:           "Waiting on the provider (1):\n",
		MoverNobody:             "Waiting on nobody's move (1):\n",
		MoverUnnamed:            "Waiting on a role the harness cannot name (1):\n",
	}
	var entries []Attention
	for _, mover := range Movers() {
		if _, ok := wants[mover]; !ok {
			t.Fatalf("mover %q has no label this test asserts", mover)
		}
		entries = append(entries, Attention{Kind: AttentionOwedStep, ID: "run-" + string(mover), Mover: mover, WorkItemID: "item-" + string(mover), OwedStep: &OwedStep{Phase: runstate.PhaseCleaningUp}})
	}
	// In reverse, so the heads are shown to come in the vocabulary's order
	// rather than the order the entries arrived in.
	for low, high := 0, len(entries)-1; low < high; low, high = low+1, high-1 {
		entries[low], entries[high] = entries[high], entries[low]
	}
	rendered := Standing{NeedsHuman: entries}.renderNeedsHuman()

	at := -1
	for _, mover := range Movers() {
		head := wants[mover]
		found := strings.Index(rendered, head)
		if found < 0 {
			t.Fatalf("rendered:\n%s\nmissing the head %q for %s", rendered, head, mover)
		}
		if found < at {
			t.Fatalf("rendered:\n%s\nthe head for %s is out of the movers' order", rendered, mover)
		}
		at = found
		entry := "  cleanup of the branch and worktree for item-" + string(mover) + " is not finished — " + mover.Possessive()
		if !strings.Contains(rendered[found:], head+entry) {
			t.Fatalf("rendered:\n%s\nthe entry %s moves is not under its head %q", rendered, mover, head)
		}
	}
	operatorPart := rendered[:strings.Index(rendered, wants[MoverProductManager])]
	for _, word := range []string{"a person", "a human", "Needs a human"} {
		if strings.Contains(rendered[len(operatorPart):], word) {
			t.Fatalf("rendered:\n%s\nsays %q of a mover that is not the operator", rendered, word)
		}
	}

	// With nothing the operator's, the line says so, and the rest still stand
	// under their own heads.
	rendered = Standing{NeedsHuman: entries[:1]}.renderNeedsHuman()
	if !strings.HasPrefix(rendered, "Needs a human: nothing\n"+wants[MoverUnnamed]) {
		t.Fatalf("rendered:\n%s\nwant the operator's line empty and the unnamed role's head under it", rendered)
	}

	// The brief rendering keeps every head and drops the entries.
	brief := Standing{NeedsHuman: entries}.RenderBriefLines()
	for _, mover := range Movers() {
		head := strings.TrimSuffix(wants[mover], ":\n") + "\n"
		if !strings.Contains(brief, head) {
			t.Fatalf("brief:\n%s\nmissing the head %q", brief, head)
		}
	}
	if strings.Contains(brief, "cleanup of the branch and worktree") {
		t.Fatalf("brief:\n%s\nlists an entry", brief)
	}
}

// A partly unreadable fourth line says so once, under its own head and saying
// it covers every head, rather than after whichever mover's head came last.
func TestAPartialReadingOfTheFourthLineIsSaidUnderItsOwnHead(t *testing.T) {
	t.Parallel()

	rendered := Standing{
		NeedsHuman:        []Attention{{Kind: AttentionOwedStep, ID: "run-a", Mover: MoverHarness, WorkItemID: "item-a"}},
		NeedsHumanProblem: "the recorded directives could not be read",
	}.renderNeedsHuman()
	want := "Needs a human: nothing\n" + partialRead + "the recorded directives could not be read (this covers every head of this line)\nWaiting on the harness (1):\n"
	if !strings.HasPrefix(rendered, want) {
		t.Fatalf("rendered:\n%s\nwant it to open with:\n%s", rendered, want)
	}
	if strings.Count(rendered, partialRead) != 1 {
		t.Fatalf("rendered:\n%s\nsays the partial reading more than once", rendered)
	}
}
