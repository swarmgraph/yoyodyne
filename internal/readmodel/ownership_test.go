package readmodel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every kind the attention line names has a rule in the ownership registry, so
// no entry the read model builds is owned by default.
func TestEveryAttentionKindHasARuleInTheRegistry(t *testing.T) {
	t.Parallel()
	for _, kind := range AttentionKinds() {
		if !ownership.HasRule(kind) {
			t.Errorf("attention kind %q has no rule in the ownership registry", kind)
		}
	}
}

// Every entry carries the registry's whole answer for its record — the owner,
// the reason where it is the operator's, the remedy, and what the remedy needs
// — its mover is that owner, and the JSON every surface reads carries both.
func TestEveryEntryCarriesTheRegistrysAnswer(t *testing.T) {
	t.Parallel()
	for kind, fixture := range attentionOfEveryKind(t) {
		entry := fixture.entry
		want := ownership.Resolve(entry.Kind, entry.ownershipRecord())
		if entry.Ownership != want || entry.Mover != want.Owner {
			t.Errorf("%s: carries %+v with mover %s, want the registry's %+v", kind, entry.Ownership, entry.Mover, want)
		}
		if strings.TrimSpace(entry.Ownership.Remedy) == "" {
			t.Errorf("%s: carries no remedy", kind)
		}
		if entry.Mover.IsOperator() && !entry.Ownership.Reason.Valid() {
			t.Errorf("%s: names the operator for %q, off the closed list", kind, entry.Ownership.Reason)
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		var wire struct {
			Mover       string           `json:"mover"`
			Ownership   ownership.Answer `json:"ownership"`
			OwnerReason string           `json:"owner_reason"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if wire.Ownership != want || wire.Mover != string(want.Owner) || wire.OwnerReason != want.ReasonSays() {
			t.Errorf("%s: JSON = %s, want the registry's answer carried", kind, encoded)
		}
	}
}

// The intake brake's hold, once escalated, is the Lead Product Manager's, with
// the operator told: never on the operator's head of the line.
func TestAnEscalatedBrakeHoldIsTheLeadProductManagersOnTheLine(t *testing.T) {
	t.Parallel()
	for name, brake := range map[string]runstate.IntakeBrake{
		"by her":         {Decision: runstate.BrakeDecisionEscalate},
		"by the harness": {Escalation: &runstate.BrakeEscalation{At: moment, Cycles: 4, Reason: "checks failed"}},
	} {
		hold := runstate.IntakeHold{HeldAt: moment, HeldBy: runstate.IntakeHolderBrake, Brake: &brake}
		entry := intakeHoldAttention(hold)
		if entry.Mover != MoverProductManager || !strings.Contains(entry.Whose(), "the operator is told") {
			t.Errorf("%s: mover %s, whose %q, want the Lead Product Manager's with the operator told", name, entry.Mover, entry.Whose())
		}
		if rendered := (Standing{NeedsHuman: []Attention{entry}}).Render(); !strings.Contains(rendered, "Needs a human: nothing") {
			t.Errorf("%s: rendered:\n%s\nwant nothing on the operator's head", name, rendered)
		}
	}
}
