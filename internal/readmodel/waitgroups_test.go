package readmodel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
)

// The not-startable line counts its work by what it waits on, names the next
// step and whose it is for each group, and says in one sentence whether any of
// it is the operator's — the six groups the operator's dashboard of 2026-09-27
// held, beside the ready work waiting for a slot, which is counted apart.
func TestTheNotStartableLineCountsItsWorkByWhatItWaitsOn(t *testing.T) {
	t.Parallel()
	entries := []struct {
		entry backlog.Entry
		kind  backlog.HoldKind
	}{
		{backlog.Entry{ID: "item-decision", Awaiting: "run run-a stopped on it"}, backlog.HeldForAPerson},
		{backlog.Entry{ID: "item-carry-out", Awaiting: "run run-b stopped on it; the decision is recorded", AwaitingCarryOut: true}, backlog.HeldForAPerson},
		{backlog.Entry{ID: "item-waiting", WaitingOn: []string{"item-other"}}, backlog.HeldWaitingOn},
		{backlog.Entry{ID: "item-conversation", Executor: domain.ConversationWith(domain.RoleArchitect)}, backlog.HeldByConversation},
		{backlog.Entry{ID: "item-parked", Parking: "off the critical path"}, backlog.HeldParked},
		{backlog.Entry{ID: "item-covered"}, backlog.HeldCovered},
		{backlog.Entry{ID: "item-parked-too", Parking: "off the critical path"}, backlog.HeldParked},
	}
	groups := newWaitGroups(Stall{}, switches{})
	refused := make([]Refused, 0, len(entries))
	for _, tried := range entries {
		if tried.entry.HoldKind() != tried.kind && tried.kind != backlog.HeldCovered {
			t.Fatalf("entry %s holds as %q, want %q", tried.entry.ID, tried.entry.HoldKind(), tried.kind)
		}
		groups.add(tried.entry, tried.kind)
		refused = append(refused, Refused{WorkItemID: tried.entry.ID, Reason: "its refusal", Kind: tried.kind})
	}
	listed := groups.list()
	standing := Standing{
		Admitted:                len(entries) + 45,
		AwaitingDecision:        1,
		AwaitingCarryOut:        1,
		NotStartable:            refused,
		NotStartableGroups:      listed,
		NotStartableForOperator: ForOperator(listed),
		WaitingForSlot:          &SlotWait{Ready: 45, Slots: 3, InFlight: 3},
	}

	rendered := standing.Render()
	want := []string{
		"Not startable (7 of 52 admitted items; 1 awaits the development manager's decision, 1 awaits the harness carrying out a decision already recorded):\n",
		"  - 45 ready, waiting for a developer slot; 3 slots, all taken — not counted as not startable: the harness starts the next one as a run in flight finishes, and nothing is asked of anybody\n",
		"  - 1 waits on the development manager's decision about a stopped run — next: she decides what becomes of each stopped run: a repair, a re-run, a wait, a re-scope, or an escalation; whose: the development manager's\n",
		"  - 1 waits on the harness carrying out a decision already recorded — next: the harness acts on the recorded decision — a repair, a re-run, or a re-armed merge — at its next pull; whose: the harness's\n",
		"  - 1 waits on other items — next: the harness pulls each once the work it waits on lands; whose: the harness's\n",
		"  - 1 is done in the architect's conversation, not by a run — next: the role does the work in its conversation, and the harness closes each item when the role's revision lands; whose: the architect's\n",
		"  - 2 are parked by the Lead Product Manager — next: she releases each once what it was parked for is settled; whose: the Lead Product Manager's\n",
		"  - 1 is covered by other work: their own unfinished children — next: the harness runs the children; nothing pulls the covering item itself; whose: the harness's\n",
		"  - nothing here is the operator's: under his rule of 2026-09-26 only a change to the fundamental goals is, and nothing here waits on one\n",
	}
	at := -1
	for _, line := range want {
		index := strings.Index(rendered, line)
		if index < 0 {
			t.Fatalf("rendered lacks %q:\n%s", line, rendered)
		}
		if index < at {
			t.Fatalf("%q is out of order:\n%s", line, rendered)
		}
		at = index
	}
	if !strings.Contains(rendered, "  title unavailable (item-decision) — its refusal\n") {
		t.Fatalf("the items themselves are no longer listed:\n%s", rendered)
	}

	// The hourly message carries the heads, and the counts by who moves them are
	// what it has to say: every tally line survives the brief rendering, and the
	// items under them do not.
	brief := standing.RenderBrief()
	for _, line := range want[1:] {
		if !strings.Contains(brief, line) {
			t.Fatalf("brief rendering lacks %q:\n%s", line, brief)
		}
	}
	if strings.Contains(brief, "item-decision") {
		t.Fatalf("brief rendering lists items:\n%s", brief)
	}

	// --json carries the groups, each with its kind, count, next step, and mover.
	encoded, err := json.Marshal(standing)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Groups []struct {
			Kind     string   `json:"kind"`
			Awaiting string   `json:"awaiting"`
			Count    int      `json:"count"`
			Next     string   `json:"next"`
			Mover    string   `json:"mover"`
			Items    []string `json:"-"`
		} `json:"not_startable_groups"`
		ForOperator string `json:"not_startable_for_operator"`
		Slot        struct {
			Ready int `json:"ready"`
			Slots int `json:"slots"`
		} `json:"waiting_for_slot"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	movers := map[string]string{}
	for _, group := range decoded.Groups {
		if group.Next == "" {
			t.Fatalf("group %+v names no next step", group)
		}
		movers[group.Kind+"/"+group.Awaiting] = group.Mover
	}
	wantMovers := map[string]string{
		"held/decision":  "development-manager",
		"held/carry-out": "harness",
		"waiting/":       "harness",
		"conversation/":  "architect",
		"parked/":        "product-manager",
		"covered/":       "harness",
	}
	for key, mover := range wantMovers {
		if movers[key] != mover {
			t.Fatalf("groups by mover = %v, want %s moved by %s", movers, key, mover)
		}
	}
	if len(decoded.Groups) != len(wantMovers) || decoded.ForOperator == "" || decoded.Slot.Ready != 45 || decoded.Slot.Slots != 3 {
		t.Fatalf("json = %s", encoded)
	}
}

// Where something on the line is the operator's, the sentence says what and
// how many rather than that nothing is.
func TestTheNotStartableLineSaysWhatIsTheOperators(t *testing.T) {
	t.Parallel()
	groups := newWaitGroups(Stall{Reason: ReasonNoWatchSession, Says: "no watch session is running, so nothing pulls the queue", Clears: "`yoyo work --watch` starts one"}, switches{})
	groups.add(backlog.Entry{ID: "item-a"}, backlog.HeldByStall)
	groups.add(backlog.Entry{ID: "item-b"}, backlog.HeldByDirective)
	listed := groups.list()
	said := ForOperator(listed)
	if !strings.HasPrefix(said, "2 items here are the operator's — ") ||
		!strings.Contains(said, "1 waits on an unresolved directive") ||
		!strings.Contains(said, "no watch session is running") {
		t.Fatalf("for the operator = %q", said)
	}
	if listed[1].Next != "`yoyo work --watch` starts one" || listed[1].Mover != MoverOperator {
		t.Fatalf("stalled group = %+v, want the command that clears it and the operator named", listed[1])
	}
}

// Whose move a stall is agrees with what the stall reason itself says: a
// reason that says it is nobody's is not the operator's here, and one that says
// it is the operator's is.
func TestAStalledGroupsMoverAgreesWithItsReason(t *testing.T) {
	t.Parallel()
	for _, reason := range Reasons() {
		if reason == ReasonIntakeHold || reason == ReasonStoreUnreadable {
			continue
		}
		mover := stallMover(Stall{Reason: reason}, switches{})
		whose := reason.Whose()
		switch {
		case strings.HasPrefix(whose, "nobody's"):
			if mover != MoverNobody {
				t.Errorf("%s says %q, and its group names %s", reason, whose, mover)
			}
		case strings.HasPrefix(whose, "the operator's"):
			if mover != MoverOperator {
				t.Errorf("%s says %q, and its group names %s", reason, whose, mover)
			}
		}
	}
}

// A step only a person can take is a group of its own, ranked with the other
// things a person moves and named as the operator's, because recording the act
// is his and nothing any role or run does passes it. Counting it among the held
// work would send whoever reads the line to the development manager for it.
func TestAGatedItemIsTheOperatorsOwnGroup(t *testing.T) {
	t.Parallel()
	gated := backlog.Entry{ID: "yoyodyne-ifd.209.7", HumanGates: humangate.Read(humangate.DeclareMarker + " soak-reviewed — the operator has judged the parity soak")}
	if kind := gated.HoldKind(); kind != backlog.HeldForAGate {
		t.Fatalf("kind = %q, want %q", kind, backlog.HeldForAGate)
	}
	groups := newWaitGroups(Stall{}, switches{})
	groups.add(backlog.Entry{ID: "item-waiting", WaitingOn: []string{"item-other"}}, backlog.HeldWaitingOn)
	groups.add(gated, gated.HoldKind())
	listed := groups.list()
	if len(listed) != 2 || listed[0].Kind != backlog.HeldForAGate || listed[0].Mover != MoverOperator {
		t.Fatalf("groups = %+v, want the gated group first and the operator's", listed)
	}
	if !strings.Contains(listed[0].Says(), "yoyo gate record") {
		t.Fatalf("says = %q, want the act that passes it named", listed[0].Says())
	}
	if sentence := ForOperator(listed); !strings.Contains(sentence, "1 item here is the operator's") || !strings.Contains(sentence, "a step only a person can take") {
		t.Fatalf("for the operator = %q", sentence)
	}
}
