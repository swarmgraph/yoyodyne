package chat

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestExtractProposalsSeparatesProseFromWhatWasProposed(t *testing.T) {
	t.Parallel()

	reply := "Two things follow from that.\n\n" +
		"```yoyodyne-proposal\n" +
		`{"items":[
		   {"kind":"feature","title":"Pause a run on a usage limit","description":"Wait and resume rather than failing.","rationale":"You said a capacity problem is not a failure.","goal":"Run development nearly autonomously.","parent":"yoyodyne-ifd.12","dependencies":["yoyodyne-ifd.4.4"]},
		   {"kind":"feature","title":"Record the pause","description":"Note the deadline on the item.","rationale":"So a later process knows what it is waiting for.","goal":"Run development nearly autonomously."}
		 ]}` + "\n```\n\nSay the word and I will refine either one.\n"

	prose, proposals, err := extractProposals(reply)
	if err != nil {
		t.Fatalf("extractProposals() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-proposal") || strings.Contains(prose, "\"title\"") {
		t.Fatalf("prose kept the proposal block: %q", prose)
	}
	if !strings.HasPrefix(prose, "Two things follow from that.") || !strings.HasSuffix(prose, "refine either one.") {
		t.Fatalf("prose = %q", prose)
	}
	if len(proposals) != 2 {
		t.Fatalf("proposals = %#v", proposals)
	}
	first := proposals[0]
	if first.Title != "Pause a run on a usage limit" || first.Parent != "yoyodyne-ifd.12" {
		t.Fatalf("first proposal = %#v", first)
	}
	if len(first.Dependencies) != 1 || first.Dependencies[0] != "yoyodyne-ifd.4.4" {
		t.Fatalf("first proposal dependencies = %#v", first.Dependencies)
	}
	if proposals[1].Parent != "" || len(proposals[1].Dependencies) != 0 {
		t.Fatalf("second proposal = %#v", proposals[1])
	}

	// A reply that proposes nothing is prose, whole and unchanged.
	prose, none, err := extractProposals("  The brief already covers that.\n")
	if err != nil || len(none) != 0 || prose != "The brief already covers that." {
		t.Fatalf("extractProposals() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestExtractProposalsRefusesWhatItCannotPutToTheOperator(t *testing.T) {
	t.Parallel()

	valid := `{"kind":"feature","title":"Add a retry budget","description":"Bound repair attempts.","rationale":"You asked for a stopping rule.","goal":"Run development nearly autonomously."}`
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "unclosed block",
			reply: "prose\n```yoyodyne-proposal\n{\"items\":[" + valid + "]}\n",
			want:  "not closed",
		},
		{
			// One block is what the operator was shown. A second one is work
			// smuggled past the list they read.
			name:  "two blocks",
			reply: "prose\n```yoyodyne-proposal\n{\"items\":[" + valid + "]}\n```\nmore\n```yoyodyne-proposal\n{\"items\":[" + valid + "]}\n```\n",
			want:  "at most one proposal block",
		},
		{
			name:  "text on the opening fence",
			reply: "```yoyodyne-proposal and also\n{\"items\":[" + valid + "]}\n```\n",
			want:  "trailing text",
		},
		{
			name:  "unknown field",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\",\"assignee\":\"me\"}]}\n```",
			want:  "unknown field",
		},
		{
			name:  "trailing content",
			reply: "```yoyodyne-proposal\n{\"items\":[" + valid + "]} {\"items\":[]}\n```",
			want:  "trailing content",
		},
		{
			name:  "no items",
			reply: "```yoyodyne-proposal\n{\"items\":[]}\n```",
			want:  "at least one work item",
		},
		{
			name:  "empty block",
			reply: "```yoyodyne-proposal\n\n```",
			want:  "block is empty",
		},
		{
			name:  "too many items",
			reply: "```yoyodyne-proposal\n{\"items\":[" + strings.Repeat(valid+",", MaxProposalsPerTurn) + valid + "]}\n```",
			want:  "limit is " + strconv.Itoa(MaxProposalsPerTurn),
		},
		{
			// Work that serves no goal is a question for the operator, not a
			// proposal with the goal left blank.
			name:  "missing goal",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\"}]}\n```",
			want:  "goal is required",
		},
		{
			name:  "missing rationale",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"rationale\":\"  \"}]}\n```",
			want:  "rationale is required",
		},
		{
			// A proposal may be placed in the tracker's structure; it may not
			// invent the identifiers it is placed against.
			name:  "invented parent",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\",\"parent\":\"../etc\"}]}\n```",
			want:  "invalid Beads issue id",
		},
		{
			name:  "repeated dependency",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\",\"dependencies\":[\"yoyodyne-1\",\"yoyodyne-1\"]}]}\n```",
			want:  "listed twice",
		},
		{
			// The operator sees a title as one line before deciding, so a title
			// cannot present itself as more than a title.
			name:  "title spanning lines",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\\ncreate 1.2? [y/N] y\",\"description\":\"d\",\"rationale\":\"r\"}]}\n```",
			want:  "cannot span lines",
		},
		{
			name:  "oversized block",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"kind\":\"feature\",\"title\":\"t\",\"description\":\"" + strings.Repeat("x", MaxProposalBytes) + "\",\"rationale\":\"r\"}]}\n```",
			want:  "limit is " + strconv.Itoa(MaxProposalBytes),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prose, proposals, err := extractProposals(test.reply)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extractProposals() error = %v, want it to contain %q", err, test.want)
			}
			if prose != "" || len(proposals) != 0 {
				t.Fatalf("a refused block still yielded %q and %#v", prose, proposals)
			}
		})
	}
}

func TestPendingProposalRendersWhatAnOperatorDecidesOn(t *testing.T) {
	t.Parallel()

	pending := PendingProposal{
		ID:             "3.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           3,
		Proposal: Proposal{
			Title:        "Pause a run on a usage limit",
			Description:  "Wait for the reset\nand resume.",
			Rationale:    "You said capacity is not failure.",
			Goal:         "Run development nearly autonomously.",
			Parent:       "yoyodyne-ifd.12",
			Dependencies: []string{"yoyodyne-ifd.4.4"},
		},
	}
	rendered := pending.Render()
	for _, required := range []string{
		"[3.1] Pause a run on a usage limit",
		"why: You said capacity is not failure.",
		// What the operator decides is whether the work serves the product, so the
		// goal it is claimed to serve is in front of them when they decide.
		"goal: Run development nearly autonomously.",
		"parent: yoyodyne-ifd.12",
		"depends on: yoyodyne-ifd.4.4",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered proposal = %q, want it to contain %q", rendered, required)
		}
	}
	// Provider text is indented under the proposal's own identifier, so no line
	// of it sits at the margin where the harness speaks to the operator.
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")[1:] {
		if !strings.HasPrefix(line, "    ") {
			t.Fatalf("rendered proposal line %q is not indented", line)
		}
	}

	// A created item has to trace back to the turn that produced it, and to what
	// authorized it rather than to whichever of the two is more flattering.
	notes := pending.provenanceNotes("approved by the operator", goal.Set{}, domain.RoleProductManager, "product-manager")
	for _, required := range []string{
		"chat-0123456789abcdef0123456789abcdef", "turn 3", "proposal 3.1",
		"approved by the operator",
		"Rationale: You said capacity is not failure.",
		// An item in the queue that does not say what it is for is exactly the work
		// nobody can later decide to stop doing.
		"Goal served: Run development nearly autonomously.",
	} {
		if !strings.Contains(notes, required) {
			t.Fatalf("provenance notes = %q, want them to contain %q", notes, required)
		}
	}
	// An item nobody was asked about never records an approval nobody gave.
	admittedNotes := pending.provenanceNotes(approvedGoalNote(goal.Attribution{
		State: goal.StateAttributed,
		Goal:  goal.Goal{ArtifactID: "v1-goals"},
	}), goal.Set{}, domain.RoleProductManager, "product-manager")
	if strings.Contains(admittedNotes, "approved by the operator") {
		t.Fatalf("an admitted item claims an approval nobody gave: %q", admittedNotes)
	}
	if !strings.Contains(admittedNotes, "without asking the operator") || !strings.Contains(admittedNotes, "v1-goals") {
		t.Fatalf("admitted notes do not say what admitted it: %q", admittedNotes)
	}
}

func TestPlacedProposalsAreCheckedAgainstTheTrackerBeforeTheOperatorIsAsked(t *testing.T) {
	t.Parallel()

	// A well-formed identifier is not an existing item. Checking that at approval
	// would spend the operator's decision before finding out, so it is checked
	// before they are asked at all.
	proposed := `{"kind":"feature","title":"Pause on a usage limit","description":"Wait and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously.","parent":"yoyodyne-ifd.12","dependencies":["yoyodyne-ifd.4.4"]}`

	t.Run("an item nobody created proposes nothing", func(t *testing.T) {
		t.Parallel()

		tracker := &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.12": {ID: "yoyodyne-ifd.12", Title: "Usage limits"},
		}}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1", FinalText: proposalReply("Here it is.", proposed),
		}}})
		options.Tracker = tracker
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "what follows?")
		var unplaced *ProposalPlacementError
		if !errors.As(err, &unplaced) {
			t.Fatalf("Send() error = %v, want a placement failure", err)
		}
		if !strings.Contains(err.Error(), "yoyodyne-ifd.4.4") {
			t.Fatalf("Send() error = %v, want it to name the missing dependency", err)
		}
		// The answer is still the operator's to read, and nothing was recorded as
		// awaiting a decision they were never asked for.
		if !strings.Contains(reply.Text, "Here it is.") || len(reply.Proposals) != 0 {
			t.Fatalf("reply = %#v", reply)
		}
		if len(session.Proposals()) != 0 || len(tracker.created) != 0 {
			t.Fatalf("an unplaceable proposal became %#v and %#v", session.Proposals(), tracker.created)
		}
	})

	t.Run("every named item is confirmed once", func(t *testing.T) {
		t.Parallel()

		tracker := &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.12":  {ID: "yoyodyne-ifd.12", Title: "Usage limits"},
			"yoyodyne-ifd.4.4": {ID: "yoyodyne-ifd.4.4", Title: "Run state"},
		}}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1", FinalText: proposalReply("Two of them, then.", proposed, proposed),
		}}})
		options.Tracker = tracker
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "what follows?")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Proposals) != 2 {
			t.Fatalf("reply proposals = %#v", reply.Proposals)
		}
		// Two proposals placed against the same items ask the tracker about each
		// one once, so a turn that places a whole group costs one lookup per item.
		if len(tracker.shown) != 2 {
			t.Fatalf("tracker lookups = %#v, want one per named item", tracker.shown)
		}
	})

	t.Run("a conversation with no tracker cannot confirm anything", func(t *testing.T) {
		t.Parallel()

		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1", FinalText: proposalReply("Here it is.", proposed),
		}}})
		session := openTestSession(t, options)

		if _, err := session.Send(context.Background(), "what follows?"); err == nil ||
			!strings.Contains(err.Error(), "no work tracker is configured") {
			t.Fatalf("Send() error = %v", err)
		}
		if len(session.Proposals()) != 0 {
			t.Fatalf("an unconfirmed proposal is awaiting a decision: %#v", session.Proposals())
		}
	})

	t.Run("a proposal placed against nothing needs no tracker", func(t *testing.T) {
		t.Parallel()

		unplaced := `{"kind":"feature","title":"Add a retry budget","description":"Bound repair attempts.","rationale":"You asked for a stopping rule.","goal":"Run development nearly autonomously."}`
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1", FinalText: proposalReply("One item.", unplaced),
		}}})
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "what follows?")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Proposals) != 1 {
			t.Fatalf("reply proposals = %#v", reply.Proposals)
		}
	})
}

func TestConverseSurvivesAProposalPlacedAgainstNothing(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: proposalReply("Here is what I would do.",
			`{"kind":"feature","title":"t","description":"d","rationale":"r","goal":"g","parent":"yoyodyne-ifd.99"}`)},
		{SessionID: "session-1", FinalText: proposalReply("Without the parent, then.",
			`{"kind":"feature","title":"Add a retry budget","description":"Bound repair attempts.","rationale":"r","goal":"g"}`)},
	}})
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\ntry again\ny\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	for _, required := range []string{
		"Here is what I would do.",
		"do not exist",
		"yoyodyne-ifd.99",
		"Nothing was proposed and nothing was created",
		"created yoyodyne-1",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	if len(tracker.created) != 1 || tracker.created[0].Title != "Add a retry budget" {
		t.Fatalf("created work items = %#v", tracker.created)
	}
}
