package exchange

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The question that had to be relayed by hand: the architect asked the Lead
// Product Manager something that named a work item, and work items carry no
// revision. Such a question is put, and the harness records when it read the
// item in place of a revision, so an answer given against an older state of
// the item can be found later.
func TestAQuestionNamingAWorkItemIsPutWithoutARevisionAndRecordsWhenItWasRead(t *testing.T) {
	t.Parallel()

	ask := Ask{
		Role:     domain.RoleProductManager,
		Question: "is this trade-off acceptable from the user's standpoint?",
		Refers:   []Reference{{What: "work-item", ID: incidentItem}},
	}
	if err := ask.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a work item named without a revision accepted", err)
	}

	voice := &scriptedVoice{answers: []string{"Acceptable, if the operator is told."}}
	items := EvidenceSource{Store: "the work tracker", Read: func(context.Context, string) (EvidenceRecord, error) {
		return EvidenceRecord{Revision: "content-1", Text: "the item as it reads now"}, nil
	}}
	conductor := incidentConductor(voice, map[EvidenceKind]EvidenceSource{EvidenceWorkItem: items}, &memoryReports{})
	architect := Party{Role: domain.RoleArchitect, Agent: "architect", Conversation: "chat-architect"}
	// An asker cannot supply the moment itself: whatever it put there is replaced
	// by the harness's own read.
	claimed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	ask.Refers[0].ReadAt = &claimed

	recorded, err := conductor.Put(context.Background(), ask, architect)
	if err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if len(voice.answered) != 1 {
		t.Fatalf("the question was not delivered: %#v", voice.answered)
	}
	for name, refers := range map[string][]Reference{"exchange": recorded.Refers, "round": recorded.Rounds[0].Refers} {
		if len(refers) != 1 || refers[0].ReadAt == nil || !refers[0].ReadAt.Equal(observedAt) {
			t.Fatalf("%s refers = %#v, want the work item stamped with when the harness read it", name, refers)
		}
	}
	if err := recorded.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want the recorded exchange valid", err)
	}
}

// A goal or a design is versioned, so a question naming one still has to say
// which revision was read; one that does is put as before.
func TestAQuestionNamingAGoalOrADesignRequiresTheRevisionItWasReadAt(t *testing.T) {
	t.Parallel()

	for _, what := range []string{"document", "goal", "design", " Document "} {
		without := Ask{Role: domain.RoleProductManager, Question: "is this still the goal?",
			Refers: []Reference{{What: what, ID: "v1-goals"}}}
		err := without.Validate()
		if err == nil || !strings.Contains(err.Error(), "refers[0] revision is required") || !strings.Contains(err.Error(), "a work item") {
			t.Fatalf("Validate(%q without a revision) = %v, want it refused naming what to put there", what, err)
		}
		with := without
		with.Refers = []Reference{{What: what, ID: "v1-goals", Revision: "revision 8"}}
		if err := with.Validate(); err != nil {
			t.Fatalf("Validate(%q with a revision) = %v", what, err)
		}
	}
	for _, what := range []string{"work-item", "run", "report", "amendment", "turn"} {
		ask := Ask{Role: domain.RoleProductManager, Question: "what does this say?", Refers: []Reference{{What: what, ID: "x-1"}}}
		if err := ask.Validate(); err != nil {
			t.Fatalf("Validate(%q without a revision) = %v, want it accepted", what, err)
		}
	}
}

// The rule is held of the question being asked, not of the record: an
// exchange written while a document could be named without a revision still
// loads.
func TestAnExchangeRecordedBeforeTheRuleStillLoads(t *testing.T) {
	t.Parallel()

	opened := observedAt
	recorded := Exchange{
		SchemaVersion: SchemaVersion,
		ID:            incidentExchange,
		ProductID:     "yoyodyne",
		Asker:         Party{Role: domain.RoleArchitect},
		Answerer:      Party{Role: domain.RoleProductManager},
		Question:      "is this still the goal?",
		Refers:        []Reference{{What: "document", ID: "v1-goals"}},
		MaxRounds:     4,
		OpenedAt:      opened,
		UpdatedAt:     opened,
	}
	if err := recorded.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want an older record naming a document without a revision to load", err)
	}
}

// A document is read as "revision 3 at <commit>", and an asker names what it
// read as "revision 3" or "3"; neither is a move, and a different number is.
func TestARevisionNamedTheWayARoleSaysItIsNotReadAsAMove(t *testing.T) {
	t.Parallel()

	cases := []struct {
		named, current string
		same           bool
	}{
		{"revision 3", "revision 3 at abc1234", true},
		{"3", "revision 3 at abc1234", true},
		{"revision 3", "revision 3, with uncommitted changes on abc1234", true},
		{"revision 3", "revision 3", true},
		{"revision 1", "revision 10 at abc1234", false},
		{"revision 3", "revision 4 at abc1234", false},
		{"content-1", "content-2", false},
	}
	for _, tc := range cases {
		if got := sameRevision(tc.named, tc.current); got != tc.same {
			t.Errorf("sameRevision(%q, %q) = %v, want %v", tc.named, tc.current, got, tc.same)
		}
	}
}

// An ask the harness cannot read is lifted out of the reply rather than
// cutting the reply off at it, so the blocks after it are still read.
func TestAnUnreadableAskLeavesTheRestOfTheReply(t *testing.T) {
	t.Parallel()

	after := "\n```yoyodyne-memory\n{}\n```\nand the rest of what was said."
	for name, block := range map[string]string{
		"refused":   Fence + "\n" + `{"ask":{"role":"product-manager","question":"is it?","refers":[{"what":"document","id":"v1-goals"}]}}` + "\n```",
		"malformed": Fence + "\n" + `{"ask":` + "\n```",
		"two":       Fence + "\n" + `{"ask":{"role":"architect","question":"one?"}}` + "\n```\n" + Fence + "\n" + `{"ask":{"role":"architect","question":"two?"}}` + "\n```",
	} {
		prose, ask, err := Extract("Before.\n\n" + block + after)
		if err == nil || ask != nil {
			t.Fatalf("%s: Extract() = %v, %v; want it refused", name, ask, err)
		}
		if !strings.Contains(prose, "Before.") || !strings.Contains(prose, "```yoyodyne-memory") || !strings.Contains(prose, "and the rest of what was said.") {
			t.Fatalf("%s: prose = %q, want the reply kept whole around the ask", name, prose)
		}
		if strings.Contains(prose, Fence) {
			t.Fatalf("%s: prose = %q, want the ask lifted out", name, prose)
		}
	}
}
