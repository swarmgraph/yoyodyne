package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

func TestAProposedChangeOutlivesTheRunThatRaisedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proposal := testProposal("amendment-0123456789abcdef0123456789abcdef")
	if err := newTestAmendmentStore(t, root).Append(proposal); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	// A second process reads what the first wrote. This is the whole point of the
	// store: the run that argued the design was wrong is finished and cleaned up
	// long before anybody decides what to do about it.
	records, err := newTestAmendmentStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	pending := amendment.Pending(records)
	if len(pending) != 1 {
		t.Fatalf("Pending() = %#v", pending)
	}
	if pending[0].ID != proposal.ID || pending[0].Change != proposal.Change || pending[0].Owner != proposal.Owner {
		t.Fatalf("the proposal did not survive intact: %#v", pending[0])
	}
	if !pending[0].RaisedAt.Equal(proposal.RaisedAt) {
		t.Fatalf("raised at %s, want %s", pending[0].RaisedAt, proposal.RaisedAt)
	}
}

func TestADecisionSettlesTheProposalItNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestAmendmentStore(t, root)
	proposal := testProposal("amendment-0123456789abcdef0123456789abcdef")
	if err := store.Append(proposal); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	decision, err := proposal.Decide(amendment.VerdictApproved, amendment.DeciderOperator, "the ordering was never settled", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if err := store.Decide(decision); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	records, err := newTestAmendmentStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if pending := amendment.Pending(records); len(pending) != 0 {
		t.Fatalf("Pending() = %#v, want nothing waiting", pending)
	}
	settled, decided := amendment.DecisionOn(records, proposal.ID)
	if !decided || settled.Verdict != amendment.VerdictApproved {
		t.Fatalf("DecisionOn() = %#v, %v", settled, decided)
	}
	// The proposal itself is still there. The record of what was argued is what
	// makes the change that followed traceable.
	if _, found := amendment.Find(records, proposal.ID); !found {
		t.Fatal("the decided proposal left the log")
	}
}

func TestADecisionMustSettleSomethingThatIsStillOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestAmendmentStore(t, root)
	proposal := testProposal("amendment-0123456789abcdef0123456789abcdef")
	if err := store.Append(proposal); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	approved, err := proposal.Decide(amendment.VerdictApproved, amendment.DeciderOperator, "", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if err := store.Decide(approved); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	// A proposal somebody has already acted on is not decided again, so a second
	// answer cannot quietly replace the first.
	if err := store.Decide(approved); err == nil {
		t.Fatal("Decide() settled a proposal that was already settled")
	}

	// A decision naming nothing settles nothing, and is refused rather than
	// written where it would sit forever describing a proposal nobody raised.
	unknown := approved
	unknown.ProposalID = "amendment-fedcba9876543210fedcba9876543210"
	if err := store.Decide(unknown); err == nil {
		t.Fatal("Decide() accepted a decision on a proposal nobody raised")
	}

	// And a decision claiming an authority the proposal was not addressed to is
	// the boundary crossed by the mechanism that exists to hold it.
	second := testProposal("amendment-fedcba9876543210fedcba9876543210")
	if err := store.Append(second); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	wrongAuthority, err := second.Decide(amendment.VerdictDeclined, amendment.DeciderOwner, "no", time.Now())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	wrongAuthority.Authority = domain.RoleProductManager
	if err := store.Decide(wrongAuthority); err == nil {
		t.Fatal("Decide() accepted a decision taken under another role's authority")
	}
}

func TestProposalsAreRefusedWhenTheyCannotBeReadBackOrDoNotBelongHere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestAmendmentStore(t, root)
	// A record that fails its own contract never reaches the log, so anything in
	// the log is something an owner can be asked to decide.
	invalid := testProposal("amendment-0123456789abcdef0123456789abcdef")
	invalid.Why = "  "
	if err := store.Append(invalid); err == nil {
		t.Fatal("Append() accepted a proposal with no reasoning")
	}
	foreign := testProposal("amendment-0123456789abcdef0123456789abcdef")
	foreign.ProductID = "elsewhere"
	if err := store.Append(foreign); err == nil {
		t.Fatal("Append() accepted another product's proposal")
	}

	// A log line that cannot be decoded fails the read rather than being skipped:
	// a queue that quietly drops what it cannot parse is one nobody can trust to
	// be complete.
	if err := store.Append(testProposal("amendment-0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	file, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.WriteString("{\"proposal\":{\"schema_version\":9}}\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List() read past a record it could not decode")
	}
}

func TestListingProposalsBeforeAnybodyProposedIsNotAFailure(t *testing.T) {
	t.Parallel()

	records, err := newTestAmendmentStore(t, t.TempDir()).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("List() = %#v, want nothing", records)
	}
}

func TestTheProposalLogSitsBesideTheRunsRatherThanAmongThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestAmendmentStore(t, root)
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// A proposal outlives every run for the product, so it must not live where a
	// settled run is cleaned up.
	if strings.HasPrefix(store.Path(), runs.Root()+string(filepath.Separator)) {
		t.Fatalf("the amendment log is inside the run directory: %s", store.Path())
	}
	if filepath.Dir(store.Path()) != home.ProductDirectory(root, "yoyodyne") {
		t.Fatalf("amendment log path = %s", store.Path())
	}
}

func newTestAmendmentStore(t *testing.T, root string) *AmendmentStore {
	t.Helper()

	store, err := NewAmendmentStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	return store
}

func testProposal(id string) amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
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

// A proposal on the run's record is raised or dropped and never both or neither:
// a drop has to name what it was folded into, because that is the whole of what
// somebody checking the fold needs, and a raised one has to name the proposal the
// owner will decide.
func TestARunsRecordOfAProposalSaysWhichOfRaisedOrDroppedItIs(t *testing.T) {
	t.Parallel()

	const id = "amendment-0123456789abcdef0123456789abcdef"
	for name, testCase := range map[string]struct {
		proposed RunAmendment
		valid    bool
	}{
		"raised":               {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which ordering holds", ID: id}, true},
		"dropped":              {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which holds", FoldedInto: id, Likeness: 0.5}, true},
		"neither":              {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which holds"}, false},
		"both":                 {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which holds", ID: id, FoldedInto: id, Likeness: 0.5}, false},
		"a raised one's score": {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which holds", ID: id, Likeness: 0.5}, false},
		"a score past one":     {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which holds", FoldedInto: id, Likeness: 1.5}, false},
		"no change":            {RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", ID: id}, false},
	} {
		err := testCase.proposed.Validate()
		if (err == nil) != testCase.valid {
			t.Fatalf("%s: Validate() error = %v, want valid %t", name, err, testCase.valid)
		}
	}

	var state State
	for range MaxRunAmendments + 1 {
		state.Amendments = append(state.Amendments, RunAmendment{Role: domain.RoleDeveloper, Artifact: "v1-design", Change: "say which ordering holds", ID: id})
	}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "exceeds the bound") {
		t.Fatalf("Validate() error = %v, want the record's bound named", err)
	}
}
