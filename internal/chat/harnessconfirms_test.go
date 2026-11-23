package chat

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A surface reading a saved document asks the question the conversation asks
// before it confirms one, and gets the same answer: a design under an automatic
// design setting is the harness's to confirm, in a project that integrates
// automatically; a design whose setting asks a person, a change to the goals
// marked fundamental, and anything at all in a project that does not integrate
// automatically are not, and stay with the operator.
func TestHarnessConfirmsAnswersAsTheConversationWouldConfirm(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	store := artifact.StoreFor(repository, config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	})
	if _, err := store.Create(domain.RoleArchitect, artifact.Draft{ID: "intake-design", Kind: artifact.KindDesign, Title: "Intake", Directory: "docs/designs", Body: "# Intake\n\nThe first text.\n", Reason: "owner decision"}, fixedClock{}.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(domain.RoleProductManager, artifact.Draft{ID: "v2-goals", Kind: artifact.KindGoals, Title: "V2 goals", Directory: "docs/product", Body: "# V2 goals\n\n## Goals\n\n- Ship the thing.\n", Reason: "owner decision"}, fixedClock{}.Now()); err != nil {
		t.Fatal(err)
	}
	design := runstate.PendingWrite{ID: "document-7.1", Turn: 7, Action: "revise", Artifact: "intake-design", Body: "# Intake\n\nA sharper text.\n", Reason: "the work item asks for it"}
	goals := runstate.PendingWrite{ID: "document-4.1", Turn: 4, Action: "revise", Artifact: "v2-goals", Intent: string(artifact.IntentFundamental), Body: "# V2 goals\n\n## Goals\n\n- Ship the other thing.\n", Reason: "yoyodyne-x: the operator changed what v2 is for"}
	automatic := artifact.Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic, Designs: domain.ApprovalAutomatic}
	human := artifact.Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic, Designs: domain.ApprovalHuman}

	for _, tc := range []struct {
		name       string
		role       domain.AgentRole
		write      runstate.PendingWrite
		policy     artifact.Policy
		integrates bool
		want       bool
	}{
		{"a design under the automatic design setting", domain.RoleArchitect, design, automatic, true, true},
		{"a design whose setting asks a person", domain.RoleArchitect, design, human, true, false},
		{"a design in a project that does not integrate automatically", domain.RoleArchitect, design, automatic, false, false},
		{"a change to the goals marked fundamental", domain.RoleProductManager, goals, automatic, true, false},
	} {
		got, err := HarnessConfirms(store, tc.policy, tc.integrates, tc.role, "chat-1", tc.write, fixedClock{}.Now())
		if err != nil {
			t.Fatalf("%s: HarnessConfirms() error = %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: HarnessConfirms() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
