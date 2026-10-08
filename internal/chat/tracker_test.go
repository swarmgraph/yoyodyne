package chat

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestExtractTrackerActionsSeparatesProseFromWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	reply := "I read ifd.22 and it already covers the separation.\n\n" +
		"```yoyodyne-tracker\n" +
		`{"actions":[
		   {"action":"read","id":"yoyodyne-ifd.22"},
		   {"action":"reparent","id":"yoyodyne-ifd.24","parent":"","reason":"it is not part of ifd.22 after all"},
		   {"action":"reprioritize","id":"yoyodyne-ifd.24","priority":0,"reason":"the operator is blocked on it"}
		 ]}` + "\n```\n\nSay so if you would rather I left it where it was.\n"

	prose, actions, _, err := extractTrackerActions(reply)
	if err != nil {
		t.Fatalf("extractTrackerActions() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-tracker") || strings.Contains(prose, "\"action\"") {
		t.Fatalf("prose kept the tracker block: %q", prose)
	}
	if !strings.HasPrefix(prose, "I read ifd.22") || !strings.HasSuffix(prose, "left it where it was.") {
		t.Fatalf("prose = %q", prose)
	}
	if len(actions) != 3 {
		t.Fatalf("actions = %#v", actions)
	}
	// An empty parent is a detachment, which is why it is a pointer: it has to be
	// distinguishable from an action that says nothing about the parent at all.
	if actions[1].Parent == nil || *actions[1].Parent != "" || actions[0].Parent != nil {
		t.Fatalf("parents = %#v", actions)
	}
	// Zero is the highest priority, not an unstated one.
	if actions[2].Priority == nil || *actions[2].Priority != 0 {
		t.Fatalf("priority = %#v", actions[2].Priority)
	}

	// A reply that asks for nothing is prose, whole and unchanged.
	prose, none, _, err := extractTrackerActions("  The queue is fine as it stands.\n")
	if err != nil || len(none) != 0 || prose != "The queue is fine as it stands." {
		t.Fatalf("extractTrackerActions() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestTrackerActionsRefuseWhatTheHarnessWillNotRun(t *testing.T) {
	t.Parallel()

	valid := `{"action":"close","id":"yoyodyne-1","reason":"it is done"}`
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "unclosed block",
			reply: "prose\n```yoyodyne-tracker\n{\"actions\":[" + valid + "]}\n",
			want:  "tracker block is not closed",
		},
		{
			name:  "two blocks",
			reply: "prose\n```yoyodyne-tracker\n{\"actions\":[" + valid + "]}\n```\nmore\n```yoyodyne-tracker\n{\"actions\":[" + valid + "]}\n```\n",
			want:  "at most one tracker block",
		},
		{
			name:  "unknown field",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-1\",\"reason\":\"r\",\"assignee\":\"me\"}]}\n```",
			want:  "unknown field",
		},
		{
			name:  "no actions",
			reply: "```yoyodyne-tracker\n{\"actions\":[]}\n```",
			want:  "at least one action",
		},
		{
			name:  "too many actions",
			reply: "```yoyodyne-tracker\n{\"actions\":[" + strings.Repeat(valid+",", MaxTrackerActionsPerTurn) + valid + "]}\n```",
			want:  "limit is " + strconv.Itoa(MaxTrackerActionsPerTurn),
		},
		{
			name:  "invented operation",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"delete\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "is not an action",
		},
		{
			// An argument the operation has no use for means the action was
			// misunderstood, so none of it is run.
			name:  "argument the operation does not take",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-1\",\"priority\":0,\"reason\":\"r\"}]}\n```",
			want:  "close does not take \"priority\"",
		},
		{
			name:  "no reason for a change",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-1\"}]}\n```",
			want:  "reason is required",
		},
		{
			name:  "created item naming its own identifier",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"kind\":\"feature\",\"id\":\"yoyodyne-1\",\"title\":\"t\",\"description\":\"d\",\"goal\":\"g\",\"reason\":\"r\"}]}\n```",
			want:  "create does not take an id",
		},
		{
			name:  "creation with no description",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"kind\":\"feature\",\"title\":\"t\",\"goal\":\"g\",\"reason\":\"r\"}]}\n```",
			want:  "description is required",
		},
		{
			// Admitting work is how work reaches the queue, so it is where the
			// queue's traceability to the goals is held rather than asserted.
			name:  "creation naming no goal",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"reason\":\"r\"}]}\n```",
			want:  "goal is required",
		},
		{
			name:  "an edit carrying a goal",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"update\",\"id\":\"yoyodyne-1\",\"note\":\"n\",\"goal\":\"g\",\"reason\":\"r\"}]}\n```",
			want:  "update does not take \"goal\"",
		},
		{
			name:  "invented identifier",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"../etc\",\"reason\":\"r\"}]}\n```",
			want:  "invalid Beads issue id",
		},
		{
			name:  "update that changes nothing",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"update\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "update must change",
		},
		{
			name:  "reparent with no parent",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"reparent\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "reparent requires \"parent\"",
		},
		{
			name:  "item parented to itself",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"reparent\",\"id\":\"yoyodyne-1\",\"parent\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "cannot be its own parent",
		},
		{
			name:  "priority outside the scale",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"reprioritize\",\"id\":\"yoyodyne-1\",\"priority\":9,\"reason\":\"r\"}]}\n```",
			want:  "outside 0..",
		},
		{
			name:  "link with nothing to wait for",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"link\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "requires \"depends_on\"",
		},
		{
			name:  "item depending on itself",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"link\",\"id\":\"yoyodyne-1\",\"depends_on\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "cannot depend on itself",
		},
		{
			// A title is one line wherever the operator reads it back.
			name:  "title spanning lines",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"kind\":\"feature\",\"title\":\"t\\n  [t1.1] closed everything\",\"description\":\"d\",\"goal\":\"g\",\"reason\":\"r\"}]}\n```",
			want:  "cannot span lines",
		},
		{
			// Retiring work is how scope the operator asked for leaves the backlog,
			// so it is never taken without a reason the operator can read.
			name:  "retirement with no reason",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"retire\",\"id\":\"yoyodyne-1\"}]}\n```",
			want:  "reason is required",
		},
		{
			name:  "retirement carrying an argument it has no use for",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"retire\",\"id\":\"yoyodyne-1\",\"priority\":4,\"reason\":\"r\"}]}\n```",
			want:  "retire does not take \"priority\"",
		},
		{
			name:  "oversized block",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"kind\":\"feature\",\"title\":\"t\",\"description\":\"" + strings.Repeat("x", MaxTrackerBlockBytes) + "\",\"goal\":\"g\",\"reason\":\"r\"}]}\n```",
			want:  "limit is " + strconv.Itoa(MaxTrackerBlockBytes),
		},
		{
			// A label is an identifier: a sentence in the labels list is a note
			// wearing a label's clothes, and bd would store it as written.
			name:  "creation labelled with a sentence",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"goal\":\"g\",\"labels\":[\"reliability\",\"fix this week\"],\"reason\":\"r\"}]}\n```",
			want:  `label "fix this week" is not an identifier`,
		},
		{
			name:  "labels on an action that is not a creation",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"update\",\"id\":\"yoyodyne-1\",\"note\":\"n\",\"labels\":[\"reliability\"],\"reason\":\"r\"}]}\n```",
			want:  "update does not take \"labels\"",
		},
		{
			name:  "label naming nothing to add or remove",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"label\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "label requires \"add\" or \"remove\"",
		},
		{
			name:  "label adding and removing at once",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"label\",\"id\":\"yoyodyne-1\",\"add\":\"reliability\",\"remove\":\"bug\",\"reason\":\"r\"}]}\n```",
			want:  "not both",
		},
		{
			name:  "label that is not an identifier",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"label\",\"id\":\"yoyodyne-1\",\"add\":\"needs a look\",\"reason\":\"r\"}]}\n```",
			want:  `add: label "needs a look" is not an identifier`,
		},
		{
			name:  "label with no reason",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"label\",\"id\":\"yoyodyne-1\",\"add\":\"reliability\"}]}\n```",
			want:  "reason is required",
		},
		{
			name:  "label with no item",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"label\",\"add\":\"reliability\",\"reason\":\"r\"}]}\n```",
			want:  "label requires the id",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prose, actions, _, err := extractTrackerActions(test.reply)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extractTrackerActions() error = %v, want it to contain %q", err, test.want)
			}
			// A refused block yields nothing to run: the whole of it is refused,
			// not the part of it that happened to parse.
			if prose != "" || len(actions) != 0 {
				t.Fatalf("a refused block still yielded %q and %#v", prose, actions)
			}
		})
	}
}

func TestSplitReplySeparatesActingFromProposing(t *testing.T) {
	t.Parallel()

	// One reply may do all three: act where the queue is the product manager's to
	// keep, propose where the decision is the operator's, and stop and ask where
	// it will not put the work in front of them at all.
	answer := "I closed the duplicate, the rewrite is yours to decide, and the marketplace I cannot place.\n\n" +
		trackerFence + "\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-2\",\"reason\":\"yoyodyne-1 already covers it\"}]}\n```\n\n" +
		proposalFence + "\n{\"items\":[{\"kind\":\"feature\",\"title\":\"Rewrite the CLI\",\"description\":\"Port everything.\",\"rationale\":\"You raised it.\",\"goal\":\"Support development in any language.\"}]}\n```\n\n" +
		concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"A plugin marketplace\",\"detail\":\"No goal covers third-party extensions.\",\"question\":\"Which goal should it serve?\"}]}\n```\n"

	parsed, err := splitReply(domain.RoleProductManager, answer)
	if err != nil {
		t.Fatalf("splitReply() error = %v", err)
	}
	if len(parsed.Actions) != 1 || parsed.Actions[0].Action != actionClose || len(parsed.Proposals) != 1 {
		t.Fatalf("splitReply() = %#v, %#v", parsed.Actions, parsed.Proposals)
	}
	if len(parsed.Concerns) != 1 || parsed.Concerns[0].Kind != ConcernUnplaceable {
		t.Fatalf("splitReply() concerns = %#v", parsed.Concerns)
	}
	if strings.Contains(parsed.Prose, "yoyodyne-tracker") || strings.Contains(parsed.Prose, "yoyodyne-proposal") ||
		strings.Contains(parsed.Prose, "yoyodyne-concern") {
		t.Fatalf("prose kept a block: %q", parsed.Prose)
	}

	// A block the harness cannot read leaves the answer whole and reports a typed
	// failure, so the caller can say what was lost and nothing is run from it.
	broken := "Closing it.\n\n" + trackerFence + "\n{\"actions\":[{\"action\":\"close\"}]}\n```\n"
	parsed, err = splitReply(domain.RoleProductManager, broken)
	var unreadable *TrackerError
	if !errors.As(err, &unreadable) {
		t.Fatalf("splitReply() error = %v, want a TrackerError", err)
	}
	if parsed.Prose != strings.TrimSpace(broken) || len(parsed.Actions) != 0 || len(parsed.Proposals) != 0 {
		t.Fatalf("a refused block yielded %q, %#v, %#v", parsed.Prose, parsed.Actions, parsed.Proposals)
	}
}

func TestReadingAnItemReturnsItInFull(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID:                 "yoyodyne-ifd.22",
		Title:              "Make the conversation readable",
		Description:        "Separate the operator's words from the answers.",
		Design:             "Prefix every line with its speaker.",
		AcceptanceCriteria: "A transcript says who said what.",
		Notes:              "Filed after a confusing session.",
		Status:             "open",
		Priority:           1,
		IssueType:          "task",
		Assignee:           "operator",
		Parent:             "yoyodyne-ifd.4",
		Dependencies:       []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: "parent-child", Status: "closed"}},
	}
	rendered := renderWorkItemEvidence(item, goal.Set{})
	// Everything the survey cannot show is what reading is for, so all of it is
	// here rather than a longer title.
	for _, required := range []string{
		"id: yoyodyne-ifd.22",
		"status: open",
		"priority: 1",
		"parent: yoyodyne-ifd.4",
		"dependency: yoyodyne-ifd.4 (parent-child, closed)",
		"Separate the operator's words from the answers.",
		"Prefix every line with its speaker.",
		"A transcript says who said what.",
		"Filed after a confusing session.",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered item = %q, want it to contain %q", rendered, required)
		}
	}

	// An item too large to carry is cut with the cut declared, because a product
	// manager reading part of an item has to know that is what it read.
	huge := item
	huge.Description = strings.Repeat("x", maxTrackerItemBytes*2)
	cut := renderWorkItemEvidence(huge, goal.Set{})
	if len(cut) > maxTrackerItemBytes+len("\n\n[cut at 8192 bytes; treat the rest as unread rather than absent]") {
		t.Fatalf("cut item is %d bytes", len(cut))
	}
	if !strings.Contains(cut, "bytes remain") {
		t.Fatalf("a cut item did not say so: %q", cut[len(cut)-200:])
	}
}

// The shape yoyodyne-ifd.283 was in when two operator directions were written
// onto it and read back as missing: a long description, sixty kilobytes of notes
// accumulated over a fortnight, and the thing just written at the very end of
// them.
//
// The writes were durable. What said otherwise was this rendering, which is what
// both readers of an item use — the product manager's own read and the operator's
// `/show` — and which cut the notes from the front, answering "was this just
// recorded?" with the admission lines from a fortnight earlier. So the notes are
// cut from the other end: the recent writing survives, and what is dropped is the
// standing text a reader can ask for again.
func TestReadingAnItemKeepsTheNotesJustWrittenRatherThanTheOldest(t *testing.T) {
	t.Parallel()

	const admitted = "Admitted to the backlog by the product manager in conversation chat-91253e0e."
	const direction = "FORGE HYGIENE joins the sweep's findings, operator-directed 2026-09-07."
	var notes strings.Builder
	notes.WriteString(admitted + "\n\n")
	for notes.Len() < 8*maxTrackerItemBytes {
		notes.WriteString("Yoyodyne stopped this item: a configured check still failed after every permitted attempt.\n")
	}
	notes.WriteString("\n" + direction)

	rendered := renderWorkItemEvidence(beads.WorkItem{
		ID:          "yoyodyne-ifd.283",
		Title:       "The development manager's hourly sweep",
		Description: strings.Repeat("the sweep reports what it finds and files root-cause work. ", 60),
		Notes:       notes.String(),
		Status:      "open",
		Priority:    1,
		IssueType:   "task",
	}, goal.Set{})

	if !strings.Contains(rendered, direction) {
		t.Fatalf("the note just written is not in what a reader is shown, so a durable write reads as lost:\n%s", rendered)
	}
	// The cut is declared at the end that was cut, so what is missing reads as
	// unread rather than as absent.
	if !strings.Contains(rendered, "are cut; treat them as unread rather than absent") {
		t.Fatalf("the rendering dropped the front of the notes without saying so:\n%s", rendered)
	}
	// The item is still bounded: keeping the recent notes is not licence to carry a
	// sixty-kilobyte item into a turn.
	if len(rendered) > maxTrackerItemBytes+maxTrackerFailureBytes {
		t.Fatalf("the rendered item is %d bytes, want it bounded", len(rendered))
	}
	// What an item says about itself is still shown, and the guarantee is which one
	// gives way: the notes keep their floor, and the standing text is what is cut
	// to make room.
	if !strings.Contains(rendered, "id: yoyodyne-ifd.283") || !strings.Contains(rendered, "the sweep reports what it finds") {
		t.Fatalf("the rendering lost the item itself:\n%s", rendered)
	}
}

func TestRetiringWorkIsRecordedAsWithdrawnRatherThanFinished(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Make the conversation readable", Status: "open"},
		"yoyodyne-ifd.23": {ID: "yoyodyne-ifd.23", Title: "Support many repositories", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("The first landed; the second is not worth doing.",
			`{"action":"close","id":"yoyodyne-ifd.22","reason":"the work landed"}`,
			`{"action":"retire","id":"yoyodyne-ifd.23","reason":"the operator dropped multi-repository support"}`)},
		{SessionID: "session-1", FinalText: "Both are out of the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Is ifd.23 still worth doing?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// The tracker holds finished and retired work in the same closed state, so
	// what separates them is what each item records about itself.
	if len(tracker.closed) != 2 {
		t.Fatalf("closed items = %#v", tracker.closed)
	}
	finished, retired := tracker.closed[0], tracker.closed[1]
	if finished[0] != "yoyodyne-ifd.22" || !strings.Contains(finished[1], "Closed as done") {
		t.Fatalf("completed item recorded %#v", finished)
	}
	if retired[0] != "yoyodyne-ifd.23" {
		t.Fatalf("retired item = %#v", retired)
	}
	for _, required := range []string{
		retiredWithoutBeingDone,
		"by the Lead Product Manager in conversation",
		"the operator dropped multi-repository support",
	} {
		if !strings.Contains(retired[1], required) {
			t.Fatalf("retired item recorded %q, want it to contain %q", retired[1], required)
		}
	}
	if strings.Contains(retired[1], "Closed as done") {
		t.Fatalf("retiring work was recorded as finishing it: %q", retired[1])
	}

	// The operator reads what the retirement actually was. Scope that was
	// dropped never appears as scope that was delivered.
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	if !strings.Contains(rendered, "retired yoyodyne-ifd.23 from the backlog without it being done") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
	if !strings.Contains(rendered, "closed yoyodyne-ifd.22 as done") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
	if !strings.Contains(rendered, "why: the operator dropped multi-repository support") {
		t.Fatalf("the reason for a retirement did not reach the operator: %q", rendered)
	}
}

// closedItemDocket records which items had their docket entries closed and why.
type closedItemDocket struct {
	closed map[string]string
}

func (d *closedItemDocket) CloseForItem(_ context.Context, workItemID, reason string) (int, error) {
	if d.closed == nil {
		d.closed = make(map[string]string)
	}
	d.closed[workItemID] = reason
	return 1, nil
}

// A closed or retired item asks nobody anything, so the entries standing for it
// on the triage docket are closed with it, saying who closed it and how.
func TestClosingOrRetiringAnItemClosesItsDocketEntries(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Make the conversation readable", Status: "open"},
		"yoyodyne-ifd.23": {ID: "yoyodyne-ifd.23", Title: "Support many repositories", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("The first landed; the second is not worth doing.",
			`{"action":"close","id":"yoyodyne-ifd.22","reason":"the work landed"}`,
			`{"action":"retire","id":"yoyodyne-ifd.23","reason":"the operator dropped multi-repository support"}`)},
		{SessionID: "session-1", FinalText: "Both are out of the backlog."},
	}}
	docket := &closedItemDocket{}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.ClosedItems = docket
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Is ifd.23 still worth doing?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reason := docket.closed["yoyodyne-ifd.22"]; !strings.Contains(reason, "closed as done by the Lead Product Manager in conversation") {
		t.Fatalf("closed item's entries closed with %q", reason)
	}
	if reason := docket.closed["yoyodyne-ifd.23"]; !strings.Contains(reason, "retired without being done by the Lead Product Manager in conversation") {
		t.Fatalf("retired item's entries closed with %q", reason)
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	if !strings.Contains(rendered, "closed yoyodyne-ifd.22 as done; 1 docket entry(s) standing for it are closed with it") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
}

// Parking is how work the product manager still wants stops being pulled without
// leaving the backlog, and it is neither a retirement nor a priority. The
// priority is what it replaces: putting deferred work at the bottom of the order
// meant "parked" to the product manager and "last" to everything that pulls, and
// a queue that drained to the bottom spent $34.38 on a run of it.
func TestParkingTakesWorkOutOfReachWithoutTakingItOutOfTheBacklog(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.6": {ID: "yoyodyne-ifd.6", Title: "The thin Codex backend", Status: "open", Priority: 4},
		"yoyodyne-ifd.77": {
			ID: "yoyodyne-ifd.77", Title: "External configuration", Status: "open", Priority: 4,
			Parking: "deferred until team mode is scoped",
		},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Parking the one and releasing the other.",
			`{"action":"park","id":"yoyodyne-ifd.6","reason":"off the critical path by the scope decision; released when a second backend is wanted"}`,
			`{"action":"unpark","id":"yoyodyne-ifd.77","reason":"team mode is scoped, so it can be pulled again"}`)},
		{SessionID: "session-1", FinalText: "One is out of reach and the other is back."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Is the Codex backend still meant to be picked up?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if len(tracker.updates) != 2 {
		t.Fatalf("updates = %#v, want the park and the release", tracker.updates)
	}
	// The parking reason is the marker selection reads, and it is appended to the
	// notes as well: a decision that only exists in a conversation transcript is
	// what parking-by-priority already was.
	parked := tracker.updates[0]
	if parked.id != "yoyodyne-ifd.6" || parked.change.Parking == nil || !parked.change.Parking.Parked() {
		t.Fatalf("park recorded %#v", parked)
	}
	if !strings.Contains(parked.change.Parking.Reason(), "off the critical path by the scope decision") {
		t.Fatalf("park reason = %q, want the action's reason stored as the parking", parked.change.Parking.Reason())
	}
	for _, required := range []string{"Parked", "by the Lead Product Manager in conversation", "off the critical path"} {
		if !strings.Contains(parked.change.AppendNotes, required) {
			t.Fatalf("park notes = %q, want them to contain %q", parked.change.AppendNotes, required)
		}
	}
	// Parking never closes anything: the work is still admitted, in the order the
	// product manager put it in.
	if len(tracker.closed) != 0 {
		t.Fatalf("parking took work out of the backlog: %#v", tracker.closed)
	}

	released := tracker.updates[1]
	if released.id != "yoyodyne-ifd.77" || released.change.Parking == nil || released.change.Parking.Parked() {
		t.Fatalf("unpark recorded %#v, want the parking cleared", released)
	}

	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	for _, required := range []string{
		"parked yoyodyne-ifd.6, so nothing selects it until it is released",
		"released yoyodyne-ifd.77 back into the queue",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered outcomes = %q, want it to contain %q", rendered, required)
		}
	}
}

// Neither action means anything on work that has left the backlog, and the
// release is the more misleading of the two to carry out: it would report work
// put back into a queue it is not in, and the product manager would go on
// expecting it to be pulled.
func TestParkingAndReleasingClosedWorkIsRefusedRatherThanRecorded(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.6": {ID: "yoyodyne-ifd.6", Title: "The thin Codex backend", Status: "closed"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Parking it.",
			`{"action":"park","id":"yoyodyne-ifd.6","reason":"off the critical path"}`,
			`{"action":"unpark","id":"yoyodyne-ifd.6","reason":"back on it"}`)},
		{SessionID: "session-1", FinalText: "It is closed."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Park the Codex backend.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("a closed item was parked or released: %#v", tracker.updates)
	}
	for _, outcome := range reply.Actions {
		if outcome.Applied {
			t.Fatalf("an action on closed work was reported as applied: %#v", outcome)
		}
		if !strings.Contains(outcome.Failure, "closed") {
			t.Fatalf("failure = %q, want the closure named", outcome.Failure)
		}
	}
}

// Work can be admitted already parked, because the identifier a creation assigns
// does not reach the product manager until the next turn: admitting it now and
// parking it then leaves the item pullable across the whole gap between them,
// which is exactly the window the marker exists to close.
func TestWorkCanBeAdmittedAlreadyParked(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it parked.",
			`{"action":"create","kind":"feature","title":"Fork-based publishing","description":"Push run branches to a fork.","goal":"Run development nearly autonomously.","priority":2,"parked":"deferred until somebody needs a fork","reason":"the operator asked for it to be recorded, not started"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog and parked."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals("Run development nearly autonomously.")
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Record fork publishing but do not start it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 || !tracker.created[0].Parking.Parked() {
		t.Fatalf("created work items = %#v, want the parking carried by the admission itself", tracker.created)
	}
	if tracker.created[0].Parking.Reason() != "deferred until somebody needs a fork" {
		t.Fatalf("created parking = %q", tracker.created[0].Parking)
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	if !strings.Contains(rendered, "parked so nothing selects it until it is released: deferred until somebody needs a fork") {
		t.Fatalf("rendered outcomes = %q, want the admission to say the work will not be pulled", rendered)
	}
}

func TestAdmittingWorkIsRecordedAsAdmissionToTheBacklog(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.26": {ID: "yoyodyne-ifd.26", Title: "Order the queue", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Filing it at the top, and moving the old one down.",
			`{"action":"create","kind":"feature","title":"Order the backlog","description":"Priority is the order.","goal":"Run development nearly autonomously.","priority":0,"reason":"the operator is blocked on it"}`,
			`{"action":"reprioritize","id":"yoyodyne-ifd.26","priority":3,"reason":"it can wait until the queue exists"}`)},
		{SessionID: "session-1", FinalText: "It is first in the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals("Run development nearly autonomously.")
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "This one comes first.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Admission is an act with an owner, so the item records that it was admitted
	// rather than merely that a row appeared in the tracker.
	if len(tracker.created) != 1 || !strings.Contains(tracker.created[0].Notes, "Admitted to the backlog by the Lead Product Manager") {
		t.Fatalf("created work items = %#v", tracker.created)
	}
	// Where the work is admitted travels with the admission. The identifier does
	// not exist until the tracker answers, so an order left to a later action is
	// an item sitting at the tracker's default in the meantime.
	if tracker.created[0].Priority == nil || *tracker.created[0].Priority != 0 {
		t.Fatalf("created work item priority = %#v", tracker.created[0].Priority)
	}
	// What the work is for is written onto the item and told to the operator, so
	// admitted work that nobody can attribute to a goal cannot exist quietly.
	if !strings.Contains(tracker.created[0].Notes, "Goal served: Run development nearly autonomously.") {
		t.Fatalf("created work item notes = %q", tracker.created[0].Notes)
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	if !strings.Contains(rendered, "admitted yoyodyne-1 to the backlog at priority 0") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
	if !strings.Contains(rendered, "goal: Run development nearly autonomously.") {
		t.Fatalf("rendered outcomes did not name the goal: %q", rendered)
	}
	// Reordering what is already admitted is the priority the tracker holds, and
	// nothing else.
	if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-ifd.26" ||
		tracker.updates[0].change.Priority == nil || *tracker.updates[0].change.Priority != 3 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
}

// What the item is called travels with what was done to it, whether or not the
// action named a title itself. An action that names none — a reordering, an
// attribution — would otherwise leave a record that is an identifier and
// nothing else, and whatever reports it later has only the record to go on.
func TestATrackerActionRecordsWhatTheItemItActedOnIsCalled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.6": {ID: "yoyodyne-ifd.6", Title: "Park the Codex adapter until the provider answers", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Moving it down; nothing is waiting on it.",
			`{"action":"reprioritize","id":"yoyodyne-ifd.6","priority":2,"reason":"the adapter is parked"}`)},
		{SessionID: "session-1", FinalText: "It sits at 2 now."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Move the Codex adapter down.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if reply.Actions[0].WorkItemTitle != "Park the Codex adapter until the provider answers" {
		t.Fatalf("outcome title = %q, want what the tracker calls the item", reply.Actions[0].WorkItemTitle)
	}
	payload := onlyEventPayload(t, root, session, execution.EventTrackerActionApplied)
	if !strings.Contains(payload, `"work_item_title":"Park the Codex adapter until the provider answers"`) {
		t.Fatalf("recorded action = %s, want it to carry what the item is called", payload)
	}
}

// What already carried the item travels with what was done to it, for the same
// reason its title does: a role acting on work no run can execute is that role
// carrying the work out, and the marker is on the item rather than in anything
// the action itself says. Without it a note the architect writes on work routed
// to the architect is indistinguishable from the product manager tidying the
// queue, and nothing ever reports the one thing the thread is missing.
func TestATrackerActionRecordsWhatAlreadyCarriedTheItemItActedOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.138": {
			ID:       "yoyodyne-ifd.138",
			Title:    "Promote the brief",
			Status:   "open",
			Executor: domain.WorkItemExecutorConversation,
		},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording what the promotion settled.",
			`{"action":"update","id":"yoyodyne-ifd.138","note":"the brief is promoted","reason":"the architect carried it out"}`)},
		{SessionID: "session-1", FinalText: "Noted on the item."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Record what the promotion settled.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if reply.Actions[0].WorkItemExecutor != domain.WorkItemExecutorConversation {
		t.Fatalf("outcome executor = %q, want what the tracker says carries the item", reply.Actions[0].WorkItemExecutor)
	}
	payload := onlyEventPayload(t, root, session, execution.EventTrackerActionApplied)
	if !strings.Contains(payload, `"work_item_executor":"conversation"`) {
		t.Fatalf("recorded action = %s, want it to carry what the item's executor is", payload)
	}
}

func TestSurveyingTheQueueAnswersFromTheTrackerRatherThanTheOpeningPicture(t *testing.T) {
	t.Parallel()

	// The picture the conversation opened with said one thing; the tracker says
	// another, because work finished while the conversation was being had.
	tracker := &fakeTracker{open: []beads.WorkItem{
		{ID: "yoyodyne-ifd.26", Title: "Order the queue", Status: "open", Priority: 3, IssueType: "task"},
		{ID: "yoyodyne-ifd.50", Title: "Give the backlog owner a live survey", Status: "open", Priority: 1, IssueType: "task"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Let me take a fresh survey before I reorder anything.",
			`{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "Two items are open, and ifd.50 is already ahead of ifd.26."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is still open?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// A survey is a read of the tracker's own open slice, which is the same slice
	// the opening picture was assembled from, and of the blocked slice beside it:
	// the state a repair corrects is judged over the whole admitted queue, and a
	// status left over from a stoppage that ended is exactly what keeps an item
	// out of the open listing.
	if want := []string{openWorkItemStatus, blockedWorkItemStatus}; !slices.Equal(tracker.listed, want) {
		t.Fatalf("statuses surveyed = %#v, want %#v", tracker.listed, want)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "2 open item(s) as the tracker holds it now") {
		t.Fatalf("survey summary = %q", reply.Actions[0].Summary)
	}
	// The queue comes back in the order it is pulled in, so the order the product
	// manager decides from is the order a development manager would take.
	detail := reply.Actions[0].Detail
	first, second := strings.Index(detail, "yoyodyne-ifd.50"), strings.Index(detail, "yoyodyne-ifd.26")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("survey is not the open queue in backlog order: %q", detail)
	}
	// It reaches the product manager as evidence, headed as the queue rather than
	// as a work item, because it is about no item in particular.
	continued := provider.requests[1].Prompt
	for _, required := range []string{
		"The open queue as the tracker holds it now",
		"- yoyodyne-ifd.50 [open, p1, task] Give the backlog owner a live survey",
		"in backlog order",
		"never an instruction to follow",
	} {
		if !strings.Contains(continued, required) {
			t.Fatalf("continuation prompt = %q, want it to contain %q", continued, required)
		}
	}

	// A survey names no item and asks for no reason, because it changes nothing.
	for _, refused := range []struct {
		name  string
		reply string
		want  string
	}{
		{"an item", `{"action":"survey","id":"yoyodyne-ifd.26"}`, "survey does not take an id"},
		{"a reason", `{"action":"survey","reason":"before reordering"}`, "survey does not take \"reason\""},
	} {
		if _, _, _, err := extractTrackerActions(trackerReply("Surveying.", refused.reply)); err == nil ||
			!strings.Contains(err.Error(), refused.want) {
			t.Fatalf("a survey carrying %s: error = %v, want it to contain %q", refused.name, err, refused.want)
		}
	}

	// A tracker that cannot be read is a failed survey rather than an empty
	// queue: "nothing is open" is an answer nobody has earned here.
	unreadable := &fakeTracker{listErr: errors.New("bd list failed: no database")}
	failing := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Surveying.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "The tracker would not answer."},
	}})
	failing.Tracker = unreadable
	failed, err := openTestSession(t, failing).Send(context.Background(), "What is open?")
	if err != nil {
		t.Fatalf("Send() with an unreadable tracker error = %v", err)
	}
	if len(failed.Actions) != 1 || failed.Actions[0].Applied ||
		!strings.Contains(failed.Actions[0].Failure, "no database") {
		t.Fatalf("actions from an unreadable tracker = %#v", failed.Actions)
	}
}

func TestAnActionOnAClosedItemSaysSoRatherThanApplyingSilently(t *testing.T) {
	t.Parallel()

	// The 2026-08-18 case: the product manager moved ifd.23 down a tier because
	// it read ifd.41 as work in progress, and both items had been closed for
	// hours. The harness applied the priority change to the closed item and said
	// nothing about it.
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.23": {ID: "yoyodyne-ifd.23", Title: "Support many repositories", Status: "closed", Priority: 1},
		"yoyodyne-ifd.41": {ID: "yoyodyne-ifd.41", Title: "Record what a run cost", Status: "closed", Priority: 1},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("ifd.41 is still in progress, so ifd.23 goes down a tier.",
			`{"action":"reprioritize","id":"yoyodyne-ifd.23","priority":3,"reason":"it waits on yoyodyne-ifd.41, which is in progress"}`,
			`{"action":"read","id":"yoyodyne-ifd.41"}`)},
		{SessionID: "session-1", FinalText: "Both are closed, so I was wrong: nothing was reordered, and ifd.23 needs no place in the queue."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Where should ifd.23 sit?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Ordering a closed item is not a decision about what happens next, so it is
	// refused rather than applied, and the refusal says the item is closed.
	reordering := reply.Actions[0]
	if reordering.Applied || !strings.Contains(reordering.Failure, "yoyodyne-ifd.23 is closed") {
		t.Fatalf("reprioritizing a closed item = %#v", reordering)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("a closed item was reprioritized anyway: %#v", tracker.updates)
	}
	if reordering.TargetStatus != "closed" {
		t.Fatalf("recorded target status = %q, want closed", reordering.TargetStatus)
	}
	// Reading the item it based that on says the same thing, in the summary the
	// product manager and the operator both read.
	read := reply.Actions[1]
	if !read.Applied || !strings.Contains(read.Summary, "yoyodyne-ifd.41 is closed as the tracker holds it now") {
		t.Fatalf("reading a closed item = %#v", read)
	}

	// The premise is corrected where the reasoning built on it happens: the next
	// round is told, and the answer the operator reads learns it too.
	continued := provider.requests[1].Prompt
	if !strings.Contains(continued, "yoyodyne-ifd.23 is closed") ||
		!strings.Contains(continued, "yoyodyne-ifd.41 is closed as the tracker holds it now") {
		t.Fatalf("continuation prompt = %q", continued)
	}
	if !strings.Contains(reply.Text, "Both are closed") {
		t.Fatalf("reply text = %q", reply.Text)
	}
	rendered := renderTrackerOutcomes(domain.RoleProductManager, reply.Actions)
	if !strings.Contains(rendered, "failed, and changed nothing: yoyodyne-ifd.23 is closed") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
}

func TestWhatIsRefusedOnClosedWorkIsWhatWouldMeanNothing(t *testing.T) {
	t.Parallel()

	closedItem := func() *fakeTracker {
		return &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.23": {ID: "yoyodyne-ifd.23", Title: "Support many repositories", Status: "closed"},
		}}
	}
	for _, test := range []struct {
		name    string
		action  string
		refused string
	}{
		// Work that has left the backlog cannot be ordered within it or taken out
		// of it again.
		{"reordering", `{"action":"reprioritize","id":"yoyodyne-ifd.23","priority":0,"reason":"r"}`, "is closed, so where it sits in the queue"},
		{"closing it again", `{"action":"close","id":"yoyodyne-ifd.23","reason":"r"}`, "is already closed, so there was nothing to close"},
		{"retiring it", `{"action":"retire","id":"yoyodyne-ifd.23","reason":"r"}`, "has left the backlog, so there was nothing to retire"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Doing it.", test.action)},
				{SessionID: "session-1", FinalText: "It was refused."},
			}})
			options.Tracker = closedItem()
			reply, err := openTestSession(t, options).Send(context.Background(), "act on ifd.23")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 || reply.Actions[0].Applied ||
				!strings.Contains(reply.Actions[0].Failure, test.refused) {
				t.Fatalf("actions = %#v", reply.Actions)
			}
		})
	}

	// Recording what was learned on finished work still means something, so it is
	// carried out — with the closure stated, because the product manager wrote it
	// believing the item was open.
	tracker := closedItem()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.23","note":"the operator dropped multi-repository support","reason":"so the item says why"}`)},
		{SessionID: "session-1", FinalText: "The note is on it, and it is already closed."},
	}})
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied ||
		!strings.Contains(reply.Actions[0].Summary, "yoyodyne-ifd.23 is closed as the tracker holds it now") {
		t.Fatalf("noting a closed item = %#v", reply.Actions)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v", tracker.updates)
	}

	// An item the tracker will not describe is neither refused nor reported as
	// open: the action is attempted and what could not be read is stated.
	silent := &fakeTracker{}
	unreadable := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Closing it.", `{"action":"close","id":"yoyodyne-ifd.99","reason":"the work landed"}`)},
		{SessionID: "session-1", FinalText: "It closed, but its state could not be read first."},
	}})
	unreadable.Tracker = silent
	reply, err = openTestSession(t, unreadable).Send(context.Background(), "close ifd.99")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied ||
		!strings.Contains(reply.Actions[0].Summary, "the tracker would not say what state yoyodyne-ifd.99 is in") {
		t.Fatalf("acting on an item the tracker would not describe = %#v", reply.Actions)
	}
	if len(silent.closed) != 1 {
		t.Fatalf("closed items = %#v", silent.closed)
	}
}

func TestTheContractOffersEveryActionAndSaysWhoOwnsTheBacklog(t *testing.T) {
	t.Parallel()

	contract := SystemPrompt(domain.RoleProductManager, Admission{}, nil, "")
	// An action a role may ask for but is never shown is one it will not use, and
	// one shown to a role that may not ask for it is one it will ask for and be
	// refused. Both are ways for a contract and the authority table to disagree,
	// so every role is held to its own list rather than to the whole vocabulary:
	// the actions are no longer one role's since triage became the development
	// manager's alone.
	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		offered := SystemPrompt(role, Admission{}, nil, "")
		for _, action := range trackerActionNames {
			shown := strings.Contains(offered, `{"action":"`+action+`"`)
			if authority.MayAct(action) && !shown {
				t.Fatalf("the %s contract does not offer the %q action that role can carry out", role, action)
			}
			if !authority.MayAct(action) && shown {
				t.Fatalf("the %s contract offers the %q action that role is refused", role, action)
			}
		}
	}
	for _, required := range []string{
		// Ordering is the product manager's, and it is what is actually pulled.
		"That queue is a backlog with an order, and the order is yours",
		"a development manager pulls from the order you set",
		"No role but you admits work or orders it",
		// Withdrawing scope is explicit and recorded, and there is no third way
		// to take work out of the queue.
		`"retire" says it will not be done`,
		"There is no delete",
		// Work reaches the queue through admission, so what admits it says what it
		// is for.
		`"goal" is required on "create"`,
		// The listing it was given is a snapshot, and the one decision it must not
		// make from a snapshot is the order.
		"That state is also a snapshot",
		"Take one before you decide what comes before what",
		// An action aimed at work that has moved on says so where the reasoning
		// that aimed it happens.
		"It also reads the item an action names as it acts on it",
		"the refusal names the closure",
	} {
		if !strings.Contains(contract, required) {
			t.Fatalf("the contract does not state %q", required)
		}
	}
}

func TestTrackerOutcomeRendersWhatHappenedRatherThanWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	applied := TrackerOutcome{
		ID:         "t2.1",
		Turn:       2,
		Action:     TrackerAction{Action: actionClose, ID: "yoyodyne-1", Reason: "the work landed"},
		Applied:    true,
		WorkItemID: "yoyodyne-1",
		Summary:    "closed yoyodyne-1",
	}
	rendered := applied.Render()
	if !strings.Contains(rendered, "[t2.1] closed yoyodyne-1") || !strings.Contains(rendered, "why: the work landed") {
		t.Fatalf("rendered outcome = %q", rendered)
	}

	// A failure is reported as one. Nothing about it reads as a change.
	failed := applied
	failed.Applied = false
	failed.Summary = ""
	failed.Failure = "bd close failed: item is claimed"
	rendered = failed.Render()
	if !strings.Contains(rendered, "failed, and changed nothing") || !strings.Contains(rendered, "item is claimed") {
		t.Fatalf("rendered failure = %q", rendered)
	}
	if strings.Contains(rendered, "closed yoyodyne-1") {
		t.Fatalf("a failed action was rendered as a change: %q", rendered)
	}
	// Provider text is indented under the action's identifier, so no line of it
	// sits at the margin where the harness speaks to the operator.
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("rendered line %q is not indented", line)
		}
	}
}

// The proof case, replayed: on 2026-09-01 the product manager asked for twelve
// tracker actions in one reply, the block was refused whole for the bound of ten,
// and three admissions and seven report dispositions went nowhere. Nothing
// recorded the refusal and nothing told the role, so the loss was found by a
// person reading the tracker afterwards and put right by hand.
//
// So the refusal is a durable event, and it reaches the role that sent it at the
// start of its next turn, verbatim. The second half of the test is what the first
// half is for: a later process, holding nothing but the record, re-issues the
// actions from the refusal it was given rather than from anybody carrying it in.
//
// The refusal is handed back within the same message first, and here the round it
// is handed back in never comes back — the provider fails it — which is the case
// the record still has to cover.
func TestARefusedTrackerBlockIsRecordedAndReachesTheRoleThatSentIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	tooMany := make([]string, 0, MaxTrackerActionsPerTurn+2)
	for i := 0; i <= MaxTrackerActionsPerTurn+1; i++ {
		tooMany = append(tooMany, `{"action":"close","id":"yoyodyne-`+strconv.Itoa(i)+`","reason":"the work landed"}`)
	}
	refusing := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Clearing the queue.", tooMany...)},
	}})
	refusing.Store = newTestStore(t, root)
	refusing.Tracker = tracker
	refused := openTestSession(t, refusing)

	handing, err := refused.Send(context.Background(), "deal with the pile")
	if err == nil || len(handing.HandedBack) != 1 {
		t.Fatalf("Send() = %v with %d hand-back(s), want the refusal handed back and the round after it failed", err, len(handing.HandedBack))
	}
	refusal := handing.HandedBack[0]
	// The whole block was refused, so nothing reached the tracker.
	if len(tracker.closed) != 0 {
		t.Fatalf("closed = %#v, want a refused block to have changed nothing", tracker.closed)
	}
	// How much was lost is part of the refusal rather than something a reader has
	// to count from a reply nobody kept; the payload below carries the count.
	payload := onlyEventPayload(t, root, refused, execution.EventTrackerBlockRefused)
	for _, wanted := range []string{
		`"role":"product-manager"`,
		`"actions":` + strconv.Itoa(len(tooMany)),
		"limit is " + strconv.Itoa(MaxTrackerActionsPerTurn),
	} {
		if !strings.Contains(payload, wanted) {
			t.Fatalf("the recorded refusal does not carry %q: %s", wanted, payload)
		}
	}

	// A second process, holding nothing but the record the first one left. Its
	// turn opens with the refusal in the harness's own words.
	backend := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Re-issuing what was refused.",
			`{"action":"close","id":"yoyodyne-1","reason":"the work landed"}`)},
		{SessionID: "session-1", FinalText: "That is the last of them."},
	}}
	continuing := testOptions(t, backend)
	continuing.Store = newTestStore(t, root)
	continuing.Tracker = tracker
	resumed := openTestSession(t, continuing)
	reply, err := resumed.Send(context.Background(), "anything else?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(backend.requests) == 0 {
		t.Fatalf("the resumed conversation made no provider call")
	}
	prompt := backend.requests[0].Prompt
	if !strings.Contains(prompt, refusal) {
		t.Fatalf("the next turn does not carry the refusal verbatim:\n%s", prompt)
	}
	if !strings.Contains(prompt, "refused whole") || !strings.Contains(prompt, "Issue the actions you still want again") {
		t.Fatalf("the next turn does not say what became of the block:\n%s", prompt)
	}

	// And the role puts it right itself: the action it re-issued landed, with
	// nobody having carried the refusal to it.
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the re-issued action to have been applied", reply.Actions)
	}
	if len(tracker.closed) != 1 || tracker.closed[0][0] != "yoyodyne-1" {
		t.Fatalf("closed = %#v, want the re-issued close", tracker.closed)
	}

	// The refusal is owed once. A turn that has been told about it does not
	// open with it again.
	if len(backend.requests) < 2 {
		t.Fatalf("the resumed conversation made %d provider call(s), want the round after the actions too", len(backend.requests))
	}
	third := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Nothing further."}}}
	later := testOptions(t, third)
	later.Store = newTestStore(t, root)
	later.Tracker = tracker
	if _, err := openTestSession(t, later).Send(context.Background(), "still nothing?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(third.requests[0].Prompt, "refused whole") {
		t.Fatalf("a later turn was told about the refusal a second time:\n%s", third.requests[0].Prompt)
	}
}

// A block the harness could not decode at all says nothing about how much was in
// it, so the count is absent rather than invented — and the refusal still reaches
// the role, which is the half that matters.
func TestARefusedTrackerBlockNobodyCanCountIsStillHandedBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Tidying.\n\n" + trackerFence + "\n{\"actions\":[{\"action\":\n```\n"},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	session := openTestSession(t, options)

	handing, err := session.Send(context.Background(), "tidy the queue")
	if err == nil || len(handing.HandedBack) != 1 {
		t.Fatalf("Send() = %v with %d hand-back(s), want the refusal handed back and the round after it failed", err, len(handing.HandedBack))
	}
	refusal := handing.HandedBack[0]
	if payload := onlyEventPayload(t, root, session, execution.EventTrackerBlockRefused); !strings.Contains(payload, `"actions":0`) {
		t.Fatalf("the recorded refusal invented a count: %s", payload)
	}

	backend := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Understood."}}}
	continuing := testOptions(t, backend)
	continuing.Store = newTestStore(t, root)
	continuing.Tracker = options.Tracker
	if _, err := openTestSession(t, continuing).Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	prompt := backend.requests[0].Prompt
	if !strings.Contains(prompt, refusal) {
		t.Fatalf("the next turn does not carry the refusal verbatim:\n%s", prompt)
	}
	// Nothing counted is said by saying nothing about a count, rather than by
	// telling the role none of its actions were asked for.
	if strings.Contains(prompt, "It asked for 0 action(s)") {
		t.Fatalf("the next turn reported an uncounted block as an empty one:\n%s", prompt)
	}
}

// A refused block leaves the record saying a turn is owed for it.
//
// The refusal reaching the role's next turn was never the missing half; what was
// missing was the turn. So the refusal is written onto the conversation's own
// record as a wakeup nobody has made, which is what the harness's schedule reads
// to start one — and a reply the harness can read clears it, because the role has
// answered and a wakeup owed forever is one that fires forever.
func TestARefusedTrackerBlockRecordsTheWakeupItIsOwed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Parking it.",
			`{"action":"park","id":"yoyodyne-ifd.7","reason":"`+strings.Repeat("x", domain.MaxWorkItemParkingBytes+1)+`"}`)},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	session := openTestSession(t, options)

	// The round the refusal is handed back in fails at the provider, so nothing
	// answered it and the wakeup is still owed.
	handing, err := session.Send(context.Background(), "park ifd.7")
	if err == nil || len(handing.HandedBack) != 1 {
		t.Fatalf("Send() = %v with %d hand-back(s), want the refusal handed back and the round after it failed", err, len(handing.HandedBack))
	}
	refusal := handing.HandedBack[0]
	recorded := loadTestConversation(t, root, options)
	if recorded.RefusedBlock == nil {
		t.Fatalf("the conversation records no refused block, so nothing can wake the role for it")
	}
	if !recorded.RefusedBlock.AwaitingWakeup(fixedClock{}.Now()) {
		t.Fatalf("refused block = %#v, want one still owed a wakeup", *recorded.RefusedBlock)
	}
	// The record carries the refusal in the harness's own words, so a wakeup made
	// from it and the turn the role reads are about the same thing.
	if !strings.Contains(recorded.RefusedBlock.Problem, refusal) {
		t.Fatalf("recorded refusal = %q, want the refusal itself", recorded.RefusedBlock.Problem)
	}
	if recorded.RefusedBlock.Turn != 1 {
		t.Fatalf("recorded refusal names turn %d, want the turn whose block was refused", recorded.RefusedBlock.Turn)
	}

	// The role answers with a block the harness can read, which is the whole of
	// what the wakeup was for. Nothing is owed after it.
	corrected := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Shorter reason.",
			`{"action":"park","id":"yoyodyne-ifd.7","reason":"waiting on the design"}`)},
		{SessionID: "session-1", FinalText: "That is the parking done."},
	}})
	corrected.Store = newTestStore(t, root)
	corrected.Tracker = options.Tracker
	if _, err := openTestSession(t, corrected).Send(context.Background(), "carry on"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if answered := loadTestConversation(t, root, corrected); answered.RefusedBlock != nil {
		t.Fatalf("refused block = %#v, want it cleared by the reply that answered it", *answered.RefusedBlock)
	}
}

// A second refused block with the first still unanswered goes to the operator
// rather than earning a second wakeup.
//
// The harness wakes a role once per refusal. A role handed its refusal back that
// sends another block the harness cannot read has shown that another copy of the
// same message will not help, and waking it again would be a turn a pass spent on
// a conversation that cannot answer. So the second refusal records why it will
// not be woken for, and is said to the operator as its own event.
func TestASecondRefusedTrackerBlockGoesToTheOperatorRatherThanLooping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Settling the report.",
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)},
	}})
	first.Store = newTestStore(t, root)
	first.Tracker = &fakeTracker{}
	if _, err := openTestSession(t, first).Send(context.Background(), "settle the reports"); err == nil {
		t.Fatalf("Send() error = nil, want the malformed handle id refused")
	}

	// The harness woke the conversation for that refusal, which is what a claim on
	// the record says.
	store := newTestStore(t, root)
	if _, err := store.ClaimRefusalWakeup(first.identity(), fixedClock{}.Now()); err != nil {
		t.Fatalf("ClaimRefusalWakeup() error = %v", err)
	}

	second := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Trying again.",
			`{"action":"handle","report":"still-not-a-report-id","reason":"already fixed"}`)},
	}})
	second.Store = newTestStore(t, root)
	second.Tracker = first.Tracker
	woken := openTestSession(t, second)
	if _, err := woken.Send(context.Background(), "re-issue what was refused"); err == nil {
		t.Fatalf("Send() error = nil, want the second block refused too")
	}

	after := loadTestConversation(t, root, second)
	if after.RefusedBlock == nil {
		t.Fatalf("the conversation records no refused block after the second refusal")
	}
	if after.RefusedBlock.AwaitingWakeup(fixedClock{}.Now()) {
		t.Fatalf("refused block = %#v, want one the harness will not wake for again", *after.RefusedBlock)
	}
	if !strings.Contains(after.RefusedBlock.Escalated, "woke this conversation") {
		t.Fatalf("escalation reason = %q, want it to say the harness had already woken the conversation", after.RefusedBlock.Escalated)
	}
	// And the operator is told, as its own event: the refusal above is a loss the
	// harness was about to repair itself, and this is the same loss with the
	// repair spent.
	payload := onlyEventPayload(t, root, woken, execution.EventTrackerRefusalUnresolved)
	for _, wanted := range []string{`"woken":true`, "still-not-a-report-id", "not-a-report-id"} {
		if !strings.Contains(payload, wanted) {
			t.Fatalf("the recorded escalation does not carry %q: %s", wanted, payload)
		}
	}
}

// A woken turn that answers in prose and re-issues nothing says so, because that
// is the same loss as a block refused twice and nothing else would mention it.
//
// The wakeup is spent, no second refusal is coming, and the record clears — so
// without this the actions vanish exactly as silently as they did before any of
// this existed, which is the failure the whole item is against.
func TestAWokenTurnThatReIssuedNothingReachesTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	losing := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Closing them out.",
			`{"action":"close","id":"yoyodyne-ifd.9","reason":"done"}`,
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)},
	}})
	losing.Store = newTestStore(t, root)
	losing.Tracker = &fakeTracker{}
	if _, err := openTestSession(t, losing).Send(context.Background(), "clear the pile"); err == nil {
		t.Fatalf("Send() error = nil, want the malformed handle id refused")
	}

	// The harness wakes the conversation, which is what the claim on the record
	// says, and the woken turn answers without asking for anything.
	store := newTestStore(t, root)
	if _, err := store.ClaimRefusalWakeup(losing.identity(), fixedClock{}.Now()); err != nil {
		t.Fatalf("ClaimRefusalWakeup() error = %v", err)
	}
	woken := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Understood — I'll leave those for now."},
	}})
	woken.Store = newTestStore(t, root)
	woken.Tracker = losing.Tracker
	session := openTestSession(t, woken)
	if _, err := session.Send(context.Background(), "re-issue what was refused"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// The record clears, because a record that never clears is a conversation
	// whose every later refusal is escalated without ever being woken for.
	if after := loadTestConversation(t, root, woken); after.RefusedBlock != nil {
		t.Fatalf("refused block = %#v, want it cleared by the turn that answered", *after.RefusedBlock)
	}
	// And the loss is said out loud, as what it is rather than as a second refusal.
	payload := onlyEventPayload(t, root, session, execution.EventTrackerRefusalUnresolved)
	for _, wanted := range []string{`"woken":true`, `"refused_again":false`, `"actions":2`, "not a report identifier"} {
		if !strings.Contains(payload, wanted) {
			t.Fatalf("the recorded loss does not carry %q: %s", wanted, payload)
		}
	}
}

// A turn somebody else drove that answers in prose is left alone: the refusal
// opened it in the harness's own words with a person reading, and the wakeup it
// is owed has not been spent.
func TestATurnNobodyWokeThatReIssuedNothingIsNotEscalated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	losing := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Settling it.",
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)},
	}})
	losing.Store = newTestStore(t, root)
	losing.Tracker = &fakeTracker{}
	if _, err := openTestSession(t, losing).Send(context.Background(), "settle the reports"); err == nil {
		t.Fatalf("Send() error = nil, want the block refused")
	}

	answering := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "It was the identifier — I read it off the wrong column."},
	}})
	answering.Store = newTestStore(t, root)
	answering.Tracker = losing.Tracker
	session := openTestSession(t, answering)
	if _, err := session.Send(context.Background(), "what happened there?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	for _, event := range loadTestEvents(t, root, session) {
		if event.Type == execution.EventTrackerRefusalUnresolved {
			t.Fatalf("a turn nobody woke was escalated to the operator: %s", event.Payload)
		}
	}
}

// refusalOf is what a message says about a tracker block the harness refused
// whole: the error where the refusal ended the message, and the hand-back where
// the harness gave it back to the role within the message instead.
func refusalOf(reply Reply, err error) string {
	refusals := append([]string(nil), reply.HandedBack...)
	if err != nil {
		refusals = append(refusals, err.Error())
	}
	return strings.Join(refusals, "\n")
}

// A refused block is handed back to the role as a further round of the same
// message, and the corrected block lands before the reply ends.
//
// Four times in the week to 2026-09-25 the product manager's block was refused
// whole and nothing landed until the operator's assistant relayed the refusal.
// The wakeup covered it at the next pull, a turn later; a tracker block's results
// already came back within the message, and a refusal is a result. So nobody
// relays it, and nothing is left owing a wakeup.
func TestARefusedTrackerBlockIsCorrectedWithinTheSameMessage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	backend := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Closing it and settling the report.",
			`{"action":"close","id":"yoyodyne-1","reason":"the work landed"}`,
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)},
		{SessionID: "session-1", FinalText: trackerReply("The report identifier was wrong; re-issuing the close alone.",
			`{"action":"close","id":"yoyodyne-1","reason":"the work landed"}`)},
		{SessionID: "session-1", FinalText: "Closed."},
	}}
	options := testOptions(t, backend)
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "close yoyodyne-1 and settle the report")
	if err != nil {
		t.Fatalf("Send() error = %v, want the corrected block to have ended the message well", err)
	}
	if len(reply.HandedBack) != 1 || !strings.Contains(reply.HandedBack[0], "not a report identifier") {
		t.Fatalf("handed back = %#v, want the refusal said to the operator", reply.HandedBack)
	}
	// The corrected block landed within the message, on the round after the one
	// that was refused.
	if len(tracker.closed) != 1 || tracker.closed[0][0] != "yoyodyne-1" {
		t.Fatalf("closed = %#v, want the re-issued close", tracker.closed)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the re-issued action applied", reply.Actions)
	}
	// The round it was handed back in is the harness's own words, verbatim, with
	// the rounds this message has left.
	if len(backend.requests) != 3 {
		t.Fatalf("provider calls = %d, want the refused round, the hand-back, and the round after the actions", len(backend.requests))
	}
	handBack := backend.requests[1].Prompt
	for _, wanted := range []string{
		reply.HandedBack[0],
		"refused whole",
		"further round of the same message",
		"This message has " + strconv.Itoa(maxTrackerRounds-1) + " round(s) of tracker actions left",
		"handed to the operator",
	} {
		if !strings.Contains(handBack, wanted) {
			t.Fatalf("the hand-back round does not say %q:\n%s", wanted, handBack)
		}
	}
	// And nothing is owed afterwards: no wakeup for a pass to make, no refusal for
	// the next turn to open with, and nothing said to the operator as lost.
	recorded := loadTestConversation(t, root, options)
	if recorded.RefusedBlock != nil {
		t.Fatalf("refused block = %#v, want it cleared by the correction within the message", *recorded.RefusedBlock)
	}
	if strings.Contains(recorded.PendingTrackerResults, "refused whole") {
		t.Fatalf("the refusal is still carried for the next turn: %q", recorded.PendingTrackerResults)
	}
	onlyEventPayload(t, root, session, execution.EventTrackerBlockRefused)
	for _, event := range loadTestEvents(t, root, session) {
		if event.Type == execution.EventTrackerRefusalUnresolved {
			t.Fatalf("a refusal corrected within the message was escalated: %s", event.Payload)
		}
	}
}

// A block refused again on the round it was handed back in goes to the operator,
// exactly as a woken turn's does, rather than being handed back a second time or
// left owing a wakeup.
func TestABlockRefusedAgainWithinTheMessageGoesToTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Settling the report.",
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)},
		{SessionID: "session-1", FinalText: trackerReply("Trying again.",
			`{"action":"handle","report":"still-not-a-report-id","reason":"already fixed"}`)},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "settle the report")
	var unreadable *TrackerError
	if !errors.As(err, &unreadable) {
		t.Fatalf("Send() error = %v, want the second refusal to end the message", err)
	}
	if len(reply.HandedBack) != 1 {
		t.Fatalf("handed back = %#v, want the first refusal handed back once", reply.HandedBack)
	}
	recorded := loadTestConversation(t, root, options)
	if recorded.RefusedBlock == nil || recorded.RefusedBlock.AwaitingWakeup(fixedClock{}.Now()) {
		t.Fatalf("refused block = %#v, want one the harness will not wake for", recorded.RefusedBlock)
	}
	if !strings.Contains(recorded.RefusedBlock.Escalated, "handed the refusal of turn 1 back within the same message") {
		t.Fatalf("escalation reason = %q, want it to say the refusal was handed back", recorded.RefusedBlock.Escalated)
	}
	payload := onlyEventPayload(t, root, session, execution.EventTrackerRefusalUnresolved)
	for _, wanted := range []string{`"handed_back":true`, `"woken":false`, `"refused_again":true`, "still-not-a-report-id"} {
		if !strings.Contains(payload, wanted) {
			t.Fatalf("the recorded escalation does not carry %q: %s", wanted, payload)
		}
	}
}

// A hand-back round that answers in prose and re-issues nothing has ended the
// correction with the actions still lost, so the operator is told, as with a
// woken turn that did the same.
func TestAHandBackAnsweredWithNoActionsReachesTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Settling the report.",
			`{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)},
		{SessionID: "session-1", FinalText: "I will leave the report for now."},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "settle the report"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if recorded := loadTestConversation(t, root, options); recorded.RefusedBlock != nil {
		t.Fatalf("refused block = %#v, want it cleared by the round that answered", *recorded.RefusedBlock)
	}
	payload := onlyEventPayload(t, root, session, execution.EventTrackerRefusalUnresolved)
	for _, wanted := range []string{`"handed_back":true`, `"refused_again":false`, "not a report identifier"} {
		if !strings.Contains(payload, wanted) {
			t.Fatalf("the recorded loss does not carry %q: %s", wanted, payload)
		}
	}
}

// A refusal on the message's last round has no round to be handed back in, so it
// is left to the role's next turn and the wakeup the harness owes it.
func TestARefusalOnTheLastRoundIsLeftToTheWakeup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	results := make([]backendapi.RunResult, 0, maxTrackerRounds)
	for round := 1; round < maxTrackerRounds; round++ {
		results = append(results, backendapi.RunResult{SessionID: "session-1",
			FinalText: trackerReply("Reading.", `{"action":"read","id":"yoyodyne-ifd.404"}`)})
	}
	results = append(results, backendapi.RunResult{SessionID: "session-1",
		FinalText: trackerReply("Settling the report.", `{"action":"handle","report":"not-a-report-id","reason":"already fixed"}`)})
	backend := &fakeBackend{results: results}
	options := testOptions(t, backend)
	options.Store = newTestStore(t, root)
	options.Tracker = &fakeTracker{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "look into it and settle the report")
	var unreadable *TrackerError
	if !errors.As(err, &unreadable) {
		t.Fatalf("Send() error = %v, want the last round's refusal to end the message", err)
	}
	if len(reply.HandedBack) != 0 || len(backend.requests) != maxTrackerRounds {
		t.Fatalf("handed back %#v over %d call(s), want nothing handed back past the last round", reply.HandedBack, len(backend.requests))
	}
	recorded := loadTestConversation(t, root, options)
	if recorded.RefusedBlock == nil || !recorded.RefusedBlock.AwaitingWakeup(fixedClock{}.Now()) {
		t.Fatalf("refused block = %#v, want one still owed its wakeup", recorded.RefusedBlock)
	}
	if !strings.Contains(recorded.PendingTrackerResults, unreadable.Error()) {
		t.Fatalf("the refusal is not carried for the next turn: %q", recorded.PendingTrackerResults)
	}
}
