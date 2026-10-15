package amendment

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestExtractTakesTheBlockOutAndLeavesWhatWasSaid(t *testing.T) {
	t.Parallel()

	reply := "I implemented the item, and the design contradicts the goal it serves.\n\n" +
		Fence + "\n" +
		`{"proposals":[{"artifact":"v1-design","change":"say which of the two orderings holds","why":"the item cannot be implemented against both"}]}` +
		"\n```\n"

	rest, entries, err := Extract(reply)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Artifact != "v1-design" {
		t.Fatalf("Extract() entries = %#v", entries)
	}
	if !strings.Contains(entries[0].Why, "cannot be implemented") {
		t.Fatalf("why = %q", entries[0].Why)
	}
	if strings.Contains(rest, "yoyodyne-amendment") || strings.Contains(rest, "\"proposals\"") {
		t.Fatalf("the block stayed in what was said: %q", rest)
	}
	if rest != "I implemented the item, and the design contradicts the goal it serves." {
		t.Fatalf("rest = %q", rest)
	}
}

func TestProseAboutADocumentProposesNothing(t *testing.T) {
	t.Parallel()

	// The block is the whole channel. A developer that says in its summary that
	// the design is wrong has told nobody who can decide it, which is exactly the
	// failure this exists to end — and treating prose as a proposal would make
	// every mention of a document into a decision somebody owes an answer to.
	rest, entries, err := Extract("The design is wrong and should say something else.\n")
	if err != nil || entries != nil {
		t.Fatalf("Extract() = %#v, %v", entries, err)
	}
	if rest != "The design is wrong and should say something else." {
		t.Fatalf("rest = %q", rest)
	}
}

func TestAnUnreadableBlockIsRefusedAndKeepsWhatCameBeforeIt(t *testing.T) {
	t.Parallel()

	// The reply is the run's evidence and a proposal decides nothing about it, so
	// a block that could not be read costs the block and never the summary.
	for name, reply := range map[string]string{
		"unclosed":     "Done.\n\n" + Fence + "\n{\"proposals\":[]}\n",
		"unknownField": "Done.\n\n" + Fence + "\n{\"proposals\":[{\"artifact\":\"v1-design\",\"change\":\"x\",\"why\":\"y\",\"patch\":\"...\"}]}\n```\n",
		"noArtifact":   "Done.\n\n" + Fence + "\n{\"proposals\":[{\"artifact\":\"  \",\"change\":\"x\",\"why\":\"y\"}]}\n```\n",
		"noWhy":        "Done.\n\n" + Fence + "\n{\"proposals\":[{\"artifact\":\"v1-design\",\"change\":\"x\",\"why\":\"  \"}]}\n```\n",
		"empty":        "Done.\n\n" + Fence + "\n{\"proposals\":[]}\n```\n",
		"second":       "Done.\n\n" + Fence + "\n{\"proposals\":[{\"artifact\":\"a\",\"change\":\"x\",\"why\":\"y\"}]}\n```\n\n" + Fence + "\n{\"proposals\":[]}\n```\n",
	} {
		rest, entries, err := Extract(reply)
		if err == nil {
			t.Errorf("%s: Extract() accepted %q", name, reply)
		}
		if entries != nil {
			t.Errorf("%s: Extract() entries = %#v, want none", name, entries)
		}
		if rest != "Done." {
			t.Errorf("%s: what was said did not survive: %q", name, rest)
		}
	}
}

func TestOneReplyCannotProposeAnUpstreamRewrite(t *testing.T) {
	t.Parallel()

	// Every proposal costs somebody a decision, so a reply that proposes changes
	// to the whole upstream at once is refused rather than turned into a queue
	// nobody works through.
	var block strings.Builder
	block.WriteString(`{"proposals":[`)
	for i := 0; i <= MaxProposalsPerReply; i++ {
		if i > 0 {
			block.WriteString(",")
		}
		block.WriteString(`{"artifact":"v1-design","change":"x","why":"y"}`)
	}
	block.WriteString("]}")
	if _, err := Decode(block.String()); err == nil {
		t.Fatalf("Decode() accepted %d proposals, limit is %d", MaxProposalsPerReply+1, MaxProposalsPerReply)
	}
}

func TestAProposalCannotCarryAReplacementDocument(t *testing.T) {
	t.Parallel()

	// A proposal is an argument for a change, never the text of one. The bound is
	// what keeps it that way: prose long enough to be pasted into the document is
	// refused, so an approval can never become an edit somebody wrote in advance.
	oversized := `{"proposals":[{"artifact":"v1-design","change":"` + strings.Repeat("a", MaxTextBytes+1) + `","why":"y"}]}`
	if _, err := Decode(oversized); err == nil {
		t.Fatal("Decode() accepted a proposal carrying a document")
	}
}

func TestTheContractStatesTheBoundItIsHeldTo(t *testing.T) {
	t.Parallel()

	// An agent told one number and held to another files a proposal it believes
	// is within the contract and loses it.
	if !strings.Contains(Contract, "at most "+maxProposalsPerReplyText+" proposals") {
		t.Fatalf("the contract does not state the enforced bound of %d:\n%s", MaxProposalsPerReply, Contract)
	}
	if !strings.Contains(Contract, Fence) {
		t.Fatalf("the contract does not show the fence agents must use:\n%s", Contract)
	}
}

func TestCollectResolvesTheDocumentToTheRoleThatOwnsIt(t *testing.T) {
	t.Parallel()

	raised := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	collected, err := Collect(
		[]Entry{{Artifact: "v1-design", Change: "say which ordering holds", Why: "the item cannot satisfy both"}},
		developerAttribution(),
		testArtifacts(),
		raised,
	)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(collected) != 1 {
		t.Fatalf("Collect() = %#v", collected)
	}
	proposal := collected[0]
	// The owner is what makes this a proposal rather than a complaint: the
	// harness resolves it from the document, so no agent chooses who is asked.
	if proposal.Owner != domain.RoleArchitect || proposal.Kind != artifact.KindDesign {
		t.Fatalf("proposal addressed to %q about a %q", proposal.Owner, proposal.Kind)
	}
	if proposal.Role != domain.RoleDeveloper || proposal.RunID != "run-0123456789abcdef0123456789abcdef" {
		t.Fatalf("attribution did not come from the harness: %#v", proposal)
	}
	if !proposal.RaisedAt.Equal(raised) {
		t.Fatalf("raised at %s, want %s", proposal.RaisedAt, raised)
	}
	if err := proposal.Validate(); err != nil {
		t.Fatalf("a collected proposal fails its own contract: %v", err)
	}
}

func TestAProposalToADocumentNobodyRecordsReachesNobody(t *testing.T) {
	t.Parallel()

	// There is no owner to decide a change to a document that does not exist, so
	// it is refused rather than recorded as something waiting on a role nobody can
	// name. The refusal is reported to the operator on the run's outcome; nothing
	// carries it back to the agent that misnamed the document.
	collected, err := Collect(
		[]Entry{{Artifact: "invented-design", Change: "x", Why: "y"}},
		developerAttribution(),
		testArtifacts(),
		time.Now(),
	)
	if err == nil {
		t.Fatal("Collect() accepted a proposal about a document nobody records")
	}
	if len(collected) != 0 {
		t.Fatalf("Collect() = %#v, want nothing recorded", collected)
	}
	if !strings.Contains(err.Error(), "no owner to decide") {
		t.Fatalf("error = %v", err)
	}
}

func TestTheOwnerOfADocumentAmendsItRatherThanProposing(t *testing.T) {
	t.Parallel()

	// The boundary runs one way. An architect proposing a change to its own
	// design would be asking permission for its own work, and recording it would
	// put a decision in front of the operator that nobody needed to make.
	collected, err := Collect(
		[]Entry{{Artifact: "v1-design", Change: "x", Why: "y"}},
		Attribution{
			Role: domain.RoleArchitect, RunID: "run-0123456789abcdef0123456789abcdef",
			ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		},
		testArtifacts(),
		time.Now(),
	)
	if err == nil || len(collected) != 0 {
		t.Fatalf("Collect() = %#v, %v", collected, err)
	}
	if !strings.Contains(err.Error(), "amends one rather than proposing") {
		t.Fatalf("error = %v", err)
	}
}

func TestOneUnusableProposalDoesNotLoseTheOneBesideIt(t *testing.T) {
	t.Parallel()

	// Two proposals in one reply are two arguments. Losing a good one because a
	// bad one shared its block would cost exactly what this channel exists to
	// protect.
	collected, err := Collect(
		[]Entry{
			{Artifact: "invented-design", Change: "x", Why: "y"},
			{Artifact: "v1-goals", Change: "say what happens when the queue is empty", Why: "the work item has no answer"},
		},
		developerAttribution(),
		testArtifacts(),
		time.Now(),
	)
	if err == nil {
		t.Fatal("Collect() did not report the proposal it could not record")
	}
	if len(collected) != 1 || collected[0].Artifact != "v1-goals" {
		t.Fatalf("Collect() = %#v", collected)
	}
	if collected[0].Owner != domain.RoleProductManager {
		t.Fatalf("goals proposal addressed to %q", collected[0].Owner)
	}
}

func TestARecordedProposalCannotClaimTheWrongOwner(t *testing.T) {
	t.Parallel()

	// The record is read long after the run that wrote it, so it has to be
	// checkable on its own: a proposal that addressed a design change to the
	// product manager would send the decision to a role that was never entitled
	// to make it.
	proposal := validProposal()
	proposal.Owner = domain.RoleProductManager
	if err := proposal.Validate(); err == nil {
		t.Fatal("Validate() accepted a design proposal addressed to the product manager")
	}

	// And a proposal from the role that owns the document is not a proposal at
	// all, however it reached the log.
	proposal = validProposal()
	proposal.Role = domain.RoleArchitect
	if err := proposal.Validate(); err == nil {
		t.Fatal("Validate() accepted a proposal from the role that owns the document")
	}
}

func TestRenderSaysWhatIsProposedAndWhoseAuthorityDecidesIt(t *testing.T) {
	t.Parallel()

	rendered := validProposal().Render()
	for _, want := range []string{"v1-design", "decided by the architect, whose document it is", "change:", "why:", "developer"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render() = %q, want it to contain %q", rendered, want)
		}
	}
	// A design is the architect's to decide, so nothing here sends it to the
	// operator.
	if strings.Contains(rendered, "operator") {
		t.Fatalf("Render() sends a design change to the operator: %q", rendered)
	}
	// Everything but the harness's own first line came from a provider, so none
	// of it is printed where the harness speaks.
	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("provider text was printed at the margin: %q", line)
		}
	}
}

func developerAttribution() Attribution {
	return Attribution{
		Role:         domain.RoleDeveloper,
		Agent:        "developer",
		RunID:        "run-0123456789abcdef0123456789abcdef",
		WorkItemID:   "yoyodyne-ifd.1.5",
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
	}
}

func validProposal() Proposal {
	return Proposal{
		SchemaVersion: SchemaVersion,
		ID:            "amendment-0123456789abcdef0123456789abcdef",
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.1.5",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "v1-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "say which of the two orderings holds",
		Why:           "the work item cannot be implemented against both",
		RaisedAt:      time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}
}

// testArtifacts is a recorded set with one document of each ownership, so a
// proposal can be resolved to either owner.
type testSet []artifact.Artifact

func (s testSet) Find(id string) (artifact.Artifact, bool) {
	for _, candidate := range s {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return artifact.Artifact{}, false
}

func testArtifacts() testSet {
	return testSet{
		{ID: "v1-design", Kind: artifact.KindDesign, Title: "V1 design", Status: artifact.StatusActive},
		{ID: "v1-goals", Kind: artifact.KindGoals, Title: "V1 goals", Status: artifact.StatusActive},
	}
}
