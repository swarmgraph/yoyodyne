package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The brief rendering is what a message nobody asked for carries: the same four
// lines, with the queues counted rather than listed. Every label, every count and
// every stated absence is the one the terminal prints, because it is read off
// that rendering rather than derived a second time — so the two can differ in how
// much they say and never in what they say.
func TestTheBriefRenderingCountsTheQueuesAndListsNone(t *testing.T) {
	t.Parallel()

	standing := Standing{
		Running: []RunningRun{
			{WorkItemID: "yoyodyne-ifd.194", Phase: "developing"},
			{WorkItemID: "yoyodyne-ifd.201", Phase: "reviewing"},
		},
		Working:      []WorkingTurn{{Agent: "product-manager", Role: "product-manager", Turns: 270}},
		Admitted:     3,
		NotStartable: []Refused{{WorkItemID: "yoyodyne-ifd.200", Reason: "waiting on yoyodyne-ifd.199"}},
		NeedsHuman:   []Attention{operatorHoldAttention(runstate.OperatorHold{HeldAt: moment})},
	}
	brief := standing.RenderBrief()
	for _, want := range []string{
		"Running (2 developer runs)\n",
		"Working (1 conversation)\n",
		"Not startable (1 of 3 admitted items)\n",
		"Needs a human (1)\n",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief:\n%s\nmissing: %q", brief, want)
		}
	}
	for _, unwanted := range []string{"yoyodyne-ifd.194", "yoyodyne-ifd.200", "product-manager", "the operator's"} {
		if strings.Contains(brief, unwanted) {
			t.Fatalf("brief:\n%s\nenumerates %q, want the queues counted and not listed", brief, unwanted)
		}
	}
	// Four lines and nothing else, so a reader knows a short message is the whole
	// answer rather than a truncated one.
	if lines := strings.Count(brief, "\n"); lines != 4 {
		t.Fatalf("brief:\n%s\nhas %d lines, want the four", brief, lines)
	}
	// Every count is the terminal's own, so a reader who runs `yoyo status` after
	// reading this is not told a different number.
	full := standing.Render()
	for _, line := range strings.Split(strings.TrimSuffix(brief, "\n"), "\n") {
		if !strings.Contains(full, line) {
			t.Fatalf("full rendering:\n%s\ndoes not carry the brief line %q", full, line)
		}
	}
}

// A count assembled from a source that answered in part still says so. It is the
// one indented line the brief rendering keeps: dropping the caveat and keeping
// the number is the confident emptiness this format exists to refuse.
func TestTheBriefRenderingKeepsWhatCouldNotBeFullyRead(t *testing.T) {
	t.Parallel()

	standing := Standing{
		Working:        []WorkingTurn{{Agent: "product-manager", Role: "product-manager"}},
		WorkingProblem: "chat-2 could not be read",
	}
	brief := standing.RenderBrief()
	if !strings.Contains(brief, "not fully read: chat-2 could not be read") {
		t.Fatalf("brief:\n%s\ndrops the caveat under a partial count", brief)
	}
	if strings.Contains(brief, "a turn in flight") {
		t.Fatalf("brief:\n%s\nlists the conversations it counted", brief)
	}
}

// The banner is carried for the reason the full rendering carries it: the
// operator asked that a pause name its cause in the first words of any message
// that reaches him, and this is the rendering those messages use.
func TestTheBriefRenderingKeepsThePausedBanner(t *testing.T) {
	t.Parallel()

	standing := Standing{Paused: "Paused on the provider's usage limit until 13:43Z"}
	brief := standing.RenderBrief()
	if !strings.HasPrefix(brief, "Paused on the provider's usage limit until 13:43Z\n") {
		t.Fatalf("brief:\n%s\ndoes not open with the cause", brief)
	}
	if strings.HasPrefix(standing.RenderBriefLines(), "Paused") {
		t.Fatalf("the lines alone open with the banner, want it left to the caller that says it once")
	}
}

// The 2026-09-07 shape, in the message that arrives on its own. The head is the
// whole of what a brief rendering carries, so a distinction only the entries
// made would be invisible in exactly the message that woke somebody — and one
// figure covering both is what sent his attention to a development manager who
// had decided every one of them.
func TestTheNotStartableHeadSplitsHeldWorkByWhoseMoveItIs(t *testing.T) {
	t.Parallel()

	standing := Standing{
		Admitted:         47,
		AwaitingDecision: 18,
		AwaitingCarryOut: 15,
		NotStartable:     make([]Refused, 33),
	}
	head := "Not startable (33 of 47 admitted items; 18 await the development manager's decision, 15 await the harness carrying out a decision already recorded)"
	if brief := standing.RenderBrief(); !strings.Contains(brief, head+"\n") {
		t.Fatalf("brief:\n%s\nmissing: %q", brief, head)
	}
	if full := standing.Render(); !strings.Contains(full, head+":\n") {
		t.Fatalf("rendered:\n%s\nmissing: %q", full, head)
	}
}

// A queue held by dependencies and directives says nothing about triage at all.
// A clause announcing that neither kind is present is one every reader learns to
// skip, and the head is the line that has to stay a line.
func TestTheNotStartableHeadSaysNothingAboutHeldWorkWhereThereIsNone(t *testing.T) {
	t.Parallel()

	standing := Standing{Admitted: 3, NotStartable: []Refused{{WorkItemID: "yoyodyne-ifd.200", Reason: "waiting on yoyodyne-ifd.199"}}}
	if rendered := standing.Render(); !strings.Contains(rendered, "Not startable (1 of 3 admitted items):\n") {
		t.Fatalf("rendered:\n%s\nwant the plain head", rendered)
	}
}
