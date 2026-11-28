package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// namedEpic is one open epic the test names, with what is inside it.
type namedEpic struct {
	epic  string
	items []string
	err   error
}

func (n namedEpic) EpicHolding(item string) (string, func(string) bool, error) {
	if n.err != nil {
		return "", nil, n.err
	}
	inside := func(other string) bool { return other == n.epic || slices.Contains(n.items, other) }
	if !inside(item) {
		return "", nil, nil
	}
	return n.epic, inside, nil
}

func epicLinkTracker() placingTracker {
	return placingTracker{&fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-e":   {ID: "yoyodyne-e", Title: "The epic", Status: "open", IssueType: "epic"},
		"yoyodyne-e.1": {ID: "yoyodyne-e.1", Title: "A member", Status: "open", Parent: "yoyodyne-e"},
		"yoyodyne-e.2": {ID: "yoyodyne-e.2", Title: "Another member", Status: "open", Parent: "yoyodyne-e"},
		"yoyodyne-out": {ID: "yoyodyne-out", Title: "Outside work", Status: "open"},
		"yoyodyne-old": {ID: "yoyodyne-old", Title: "Finished outside work", Status: "closed"},
	}}}
}

// While epic mode is on, a role's dependency of a member on open work outside
// its epic, and a move of open outside work under a member, are refused when
// written, naming the member, the item and the add that brings it in. A link to
// closed work, to another member, or between items outside every epic is carried
// out, and so is the development manager's split of a member, which is judged
// for joining in the same act.
func TestALinkFromANamedEpicToOpenWorkOutsideItIsRefused(t *testing.T) {
	t.Parallel()

	tracker := epicLinkTracker()
	session := developmentManagerSession(t, tracker,
		trackerReply("Sequencing.",
			`{"action":"link","id":"yoyodyne-e.1","depends_on":"yoyodyne-out","reason":"it needs the outside work"}`,
			`{"action":"reparent","id":"yoyodyne-out","parent":"yoyodyne-e.1","reason":"it belongs to the member"}`,
			`{"action":"link","id":"yoyodyne-e.1","depends_on":"yoyodyne-old","reason":"it built on finished work"}`,
			`{"action":"link","id":"yoyodyne-e.1","depends_on":"yoyodyne-e.2","reason":"members in order"}`,
			`{"action":"link","id":"yoyodyne-out","depends_on":"yoyodyne-old","reason":"outside work in order"}`,
			`{"action":"create","kind":"feature","title":"Half of the member","description":"The first half.","goal":"`+recordedGoal+`","parent":"yoyodyne-e.1","reason":"too large for one change"}`),
		"Sequenced.")
	session.options.EpicLinks = namedEpic{epic: "yoyodyne-e", items: []string{"yoyodyne-e.1", "yoyodyne-e.2"}}

	reply, err := session.Send(context.Background(), "Sequence the epic.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 6 {
		t.Fatalf("actions = %#v, want six: %v", reply.Actions, reply.HandedBack)
	}
	for at, want := range map[int][]string{
		0: {"refused, and nothing was changed", "yoyodyne-e.1 is in epic yoyodyne-e", "make it wait on yoyodyne-out, while yoyodyne-out is open and outside that epic", "`yoyo epic add yoyodyne-e yoyodyne-out`"},
		1: {"refused, and nothing was changed", "yoyodyne-e.1 is in epic yoyodyne-e", "put yoyodyne-out under it as a child, while yoyodyne-out is open and outside that epic", "`yoyo epic add yoyodyne-e yoyodyne-out`"},
	} {
		if refused := reply.Actions[at]; refused.Applied {
			t.Errorf("%s was carried out, want it refused", refused.Action.Action)
		} else {
			for _, phrase := range want {
				if !strings.Contains(refused.Failure, phrase) {
					t.Errorf("the refusal of %s does not say %q:\n%s", refused.Action.Action, phrase, refused.Failure)
				}
			}
		}
	}
	for _, at := range []int{2, 3, 4, 5} {
		if outcome := reply.Actions[at]; !outcome.Applied {
			t.Errorf("%s %s = %q, want it carried out", outcome.Action.Action, outcome.Action.ID, outcome.Failure)
		}
	}
	want := [][2]string{{"yoyodyne-e.1", "yoyodyne-old"}, {"yoyodyne-e.1", "yoyodyne-e.2"}, {"yoyodyne-out", "yoyodyne-old"}}
	if !slices.Equal(tracker.links, want) {
		t.Fatalf("links = %v, want only %v", tracker.links, want)
	}
	if moved := tracker.items["yoyodyne-out"].Parent; moved != "" {
		t.Fatalf("the outside work was moved under %s", moved)
	}

	// Anything else created under work in the epic is refused, and the
	// development manager's split is the act that is not.
	if refusal := session.outsideEpicRefusal(context.Background(), "yoyodyne-e", "", true); !strings.Contains(refusal, "a new item under it would be open work outside that epic") {
		t.Errorf("a new child of the epic's own item = %q, want it refused", refusal)
	}
}

// Where which epic an item is in cannot be read, a role's link is refused saying
// so, which is the safe side; with no epic named nothing is checked.
func TestALinkWhoseEpicCannotBeReadIsRefused(t *testing.T) {
	t.Parallel()

	link := trackerReply("Sequencing.", `{"action":"link","id":"yoyodyne-e.1","depends_on":"yoyodyne-out","reason":"it needs the outside work"}`)
	session := developmentManagerSession(t, epicLinkTracker(), link, "Done.")
	session.options.EpicLinks = namedEpic{err: errors.New("the record of the named epics is not a regular file")}
	reply, err := session.Send(context.Background(), "Sequence it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "could not be read: the record of the named epics is not a regular file") {
		t.Fatalf("the link = %#v; want it refused because the epics cannot be read", reply.Actions)
	}

	session = developmentManagerSession(t, epicLinkTracker(), link, "Done.")
	session.options.EpicLinks = namedEpic{epic: "yoyodyne-other"}
	reply, err = session.Send(context.Background(), "Sequence it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("the link outside every epic = %#v, want it carried out", reply.Actions)
	}
}

// A proposal placed under work in a named epic would be admitted without asking
// the operator as open work outside the epic, held as a child of a member. It is
// refused before it is recorded: nothing is created, nothing waits on the
// operator, and the refusal goes back to the Lead Product Manager. A proposal
// placed elsewhere is admitted as before.
func TestAProposalUnderWorkInANamedEpicIsRefusedBeforeItIsAdmitted(t *testing.T) {
	t.Parallel()

	for _, parent := range []string{"yoyodyne-e.1", "yoyodyne-e", "yoyodyne-out"} {
		tracker := epicLinkTracker().fakeTracker
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: proposalReply("One follows it.",
				`{"kind":"feature","title":"A child of `+parent+`","description":"What done means.","rationale":"You asked for it.","goal":"`+recordedGoal+`","parent":"`+parent+`"}`)},
			{SessionID: "session-1", FinalText: "Understood."},
		}})
		options.Tracker = tracker
		options.Goals = recordedGoals(recordedGoal)
		options.EpicLinks = namedEpic{epic: "yoyodyne-e", items: []string{"yoyodyne-e.1", "yoyodyne-e.2"}}
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "what follows from that")
		if err != nil {
			t.Fatalf("under %s: Send() error = %v", parent, err)
		}
		if parent == "yoyodyne-out" {
			if len(reply.Admitted) != 1 || len(tracker.created) != 1 {
				t.Fatalf("under outside work: admitted %#v, created %d; want it admitted", reply.Admitted, len(tracker.created))
			}
			continue
		}
		if len(reply.Admitted) != 0 || len(tracker.created) != 0 || len(reply.Proposals) != 0 || len(session.Proposals()) != 0 {
			t.Fatalf("under %s: admitted %#v, created %d, waiting %d; want nothing", parent, reply.Admitted, len(tracker.created), len(session.Proposals()))
		}
		if len(reply.RefusedBlocks) != 1 || !strings.Contains(reply.RefusedBlocks[0], parent+" is in epic yoyodyne-e") ||
			!strings.Contains(reply.RefusedBlocks[0], "a new item under it would be open work outside that epic") {
			t.Fatalf("under %s: refused blocks = %q, want the proposal refused naming the epic", parent, reply.RefusedBlocks)
		}
	}
}

// namedLater is an epic named between the check of a proposal and its carrying
// out: the first calls answer that no epic is named.
type namedLater struct {
	before *int
	epic   namedEpic
}

func (n namedLater) EpicHolding(item string) (string, func(string) bool, error) {
	if *n.before > 0 {
		*n.before--
		return "", nil, nil
	}
	return n.epic.EpicHolding(item)
}

func underMember() string {
	return `{"kind":"feature","title":"A child of the member","description":"What done means.","rationale":"You asked for it.","goal":"` + recordedGoal + `","parent":"yoyodyne-e.1"}`
}

// A proposal is checked again when it is carried out, against the epics as they
// stand then, also where the operator decided it: one whose parent came into a
// named epic since it was made is refused whole, creates nothing, no longer waits
// on anybody, records the refusal, and tells the conversation the role reads.
func TestADecidedProposalUnderWorkInANamedEpicIsRefusedWhenCarriedOut(t *testing.T) {
	t.Parallel()

	t.Run("approved by the operator", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		tracker := epicLinkTracker().fakeTracker
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: proposalReply("One follows.", underMember())}}})
		options.Store = newTestStore(t, root)
		options.Tracker = tracker
		options.Goals = unapprovedGoals(recordedGoal)
		session := openTestSession(t, options)
		reply, err := session.Send(context.Background(), "what follows")
		if err != nil || len(reply.Proposals) != 1 {
			t.Fatalf("Send() = %#v, %v; want one proposal waiting", reply.Proposals, err)
		}
		session.options.EpicLinks = namedEpic{epic: "yoyodyne-e", items: []string{"yoyodyne-e.1"}}

		_, err = session.Approve(context.Background(), reply.Proposals[0].ID)
		var refused *ProposalRefusedError
		if !errors.As(err, &refused) || !strings.Contains(err.Error(), "yoyodyne-e.1 is in epic yoyodyne-e") {
			t.Fatalf("Approve() error = %v, want the proposal refused naming the epic", err)
		}
		if len(tracker.created) != 0 || len(session.Proposals()) != 0 {
			t.Fatalf("created %d, waiting %d; want nothing", len(tracker.created), len(session.Proposals()))
		}
		if payload := onlyEventPayload(t, root, session, execution.EventProposalRefused); !strings.Contains(payload, "yoyodyne-e.1 is in epic yoyodyne-e") {
			t.Fatalf("the refusal recorded is %s", payload)
		}
	})

	t.Run("admitted without the operator", func(t *testing.T) {
		t.Parallel()
		tracker := epicLinkTracker().fakeTracker
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: proposalReply("One follows.", underMember())}}})
		options.Tracker = tracker
		options.Goals = recordedGoals(recordedGoal)
		before := 1
		options.EpicLinks = namedLater{before: &before, epic: namedEpic{epic: "yoyodyne-e", items: []string{"yoyodyne-e.1"}}}
		session := openTestSession(t, options)
		reply, err := session.Send(context.Background(), "what follows")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Admitted) != 0 || len(reply.Proposals) != 0 || len(session.Proposals()) != 0 || len(tracker.created) != 0 {
			t.Fatalf("admitted %#v, waiting %d, created %d; want nothing, and nothing put to the operator", reply.Admitted, len(session.Proposals()), len(tracker.created))
		}
	})
}

// The development manager's creation under the epic's own item is not a split of
// a member, so nothing would judge it for joining: it is refused through the same
// action path, and nothing is created. Her split of a member goes ahead.
func TestADevelopmentManagersCreationUnderTheEpicItselfIsRefused(t *testing.T) {
	t.Parallel()

	tracker := epicLinkTracker()
	session := developmentManagerSession(t, tracker,
		trackerReply("Filing it.",
			`{"action":"create","kind":"feature","title":"A fix under the epic","description":"The fix.","goal":"`+recordedGoal+`","parent":"yoyodyne-e","reason":"it was found during the epic"}`,
			`{"action":"create","kind":"feature","title":"Half of the member","description":"The first half.","goal":"`+recordedGoal+`","parent":"yoyodyne-e.1","reason":"too large for one change"}`),
		"Filed.")
	session.options.EpicLinks = namedEpic{epic: "yoyodyne-e", items: []string{"yoyodyne-e.1", "yoyodyne-e.2"}}

	reply, err := session.Send(context.Background(), "File the fix.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 {
		t.Fatalf("actions = %#v, want two", reply.Actions)
	}
	if refused := reply.Actions[0]; refused.Applied || !strings.Contains(refused.Failure, "yoyodyne-e is in epic yoyodyne-e") ||
		!strings.Contains(refused.Failure, "a new item under it would be open work outside that epic") {
		t.Fatalf("the creation under the epic itself = applied %v, %q; want it refused", refused.Applied, refused.Failure)
	}
	if split := reply.Actions[1]; !split.Applied {
		t.Fatalf("the split of a member = %q, want it carried out", split.Failure)
	}
	if len(tracker.created) != 1 || tracker.created[0].Parent != "yoyodyne-e.1" {
		t.Fatalf("created %+v, want only the split of the member", tracker.created)
	}
}
