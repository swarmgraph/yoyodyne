package runstate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// publishRefusedState is a run stopped while publishing because GitHub would
// not take the harness's identity.
func publishRefusedState(t *testing.T) State {
	t.Helper()
	state := testState(t, StatusFailed)
	state.Phase = PhaseDeveloping
	state.Failure = "publish: git push exited 255: No user exists for uid 501"
	state.IdentityStop = &IdentityStop{Detail: state.Failure, RecordedAt: state.UpdatedAt}
	return state
}

// The identity stop and its take-ups are held to the bounds the record states:
// a stop says what refused and when, and a take-up says why and when.
func TestAnIdentityStopAndItsTakeUpsAreHeldToTheirBounds(t *testing.T) {
	t.Parallel()

	stopped := publishRefusedState(t)
	if err := stopped.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the stopped run valid", err)
	}
	takenUp := func(count int) []IdentityTakeUp {
		var all []IdentityTakeUp
		for range count {
			all = append(all, IdentityTakeUp{Reason: "GitHub answers again", TakenUpAt: stopped.UpdatedAt})
		}
		return all
	}
	for name, shape := range map[string]func(*State){
		"a stop without detail":    func(s *State) { s.IdentityStop.Detail = " " },
		"a stop with long detail":  func(s *State) { s.IdentityStop.Detail = strings.Repeat("x", MaxEnvironmentalDetailBytes+1) },
		"a stop without a time":    func(s *State) { s.IdentityStop.RecordedAt = time.Time{} },
		"a take-up without reason": func(s *State) { s.IdentityTakeUps = []IdentityTakeUp{{TakenUpAt: s.UpdatedAt}} },
		"a take-up without a time": func(s *State) { s.IdentityTakeUps = []IdentityTakeUp{{Reason: "r"}} },
		"a take-up with long reason": func(s *State) {
			s.IdentityTakeUps = []IdentityTakeUp{{Reason: strings.Repeat("r", MaxSelectionReasonBytes+1), TakenUpAt: s.UpdatedAt}}
		},
		"too many take-ups": func(s *State) { s.IdentityTakeUps = takenUp(MaxIdentityTakeUps + 1) },
	} {
		state := publishRefusedState(t)
		shape(&state)
		if err := state.Validate(); err == nil {
			t.Errorf("%s: Validate() error = nil, want it refused", name)
		}
	}

	stopped.IdentityTakeUps = takenUp(MaxIdentityTakeUps)
	if err := stopped.Validate(); err != nil {
		t.Fatalf("Validate() at the bound error = %v, want it valid", err)
	}
	if left := stopped.IdentityTakeUpsLeft(); left != 0 {
		t.Fatalf("IdentityTakeUpsLeft() = %d at the bound, want 0", left)
	}
	stopped.IdentityTakeUps = takenUp(3)
	if left := stopped.IdentityTakeUpsLeft(); left != MaxIdentityTakeUps-3 {
		t.Fatalf("IdentityTakeUpsLeft() = %d, want %d", left, MaxIdentityTakeUps-3)
	}
}

// The identity stop, the owed push, and the take-ups survive the file.
func TestAnIdentityStopRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	state := publishRefusedState(t)
	state.PublishOwed = true
	state.IdentityTakeUps = []IdentityTakeUp{{Reason: "GitHub answers again", TakenUpAt: state.UpdatedAt, SupersededFailure: state.Failure}}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded State
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(decoded.IdentityStop, state.IdentityStop) || !decoded.PublishOwed || !reflect.DeepEqual(decoded.IdentityTakeUps, state.IdentityTakeUps) {
		t.Fatalf("round trip = %#v, want %#v", decoded, state)
	}
}
