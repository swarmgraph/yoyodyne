package chat

// The operator's narrowing of their own gate. A project that asks about every
// work item may still say that one class of work is not what it is asking
// about, and until it could say so the policy an operator had recorded was a
// sentence nothing enforced: the harness refused the admission, and implementing
// the exemption was itself change-work the same gate held.

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// diagnosisProposal is a proposal for work that only reads and reports, claiming
// the class the operator carved out.
func diagnosisProposal(title string) string {
	return `{"kind":"feature","title":"` + title + `","description":"Read the attribution notes and report what is there.","rationale":"Nobody knows how many items carry one.","goal":"` + recordedGoal + `","class":"diagnosis"}`
}

// Under the per-item gate, work of an exempted class reaches the queue and the
// operator is told rather than asked. Everything else under that gate is still
// put to them, which is what makes this an exemption rather than the gate coming
// down.
func TestExemptedWorkIsAdmittedUnderThePerItemGateAndEverythingElseIsStillAsked(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("One of these only looks.",
			diagnosisProposal("Count the items carrying an attribution"),
			proposeRecordedGoal("Rewrite the attribution resolver")),
	}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 1 || reply.Admitted[0].Title != "Count the items carrying an attribution" {
		t.Fatalf("admitted = %#v, want only the diagnosis-class work", reply.Admitted)
	}
	if len(reply.Proposals) != 1 || reply.Proposals[0].Proposal.Title != "Rewrite the attribution resolver" {
		t.Fatalf("proposals = %#v, want the change still put to the operator", reply.Proposals)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the exempted item alone", tracker.created)
	}
	// The item says what let it in, and it does not say the operator approved it:
	// nobody asked them about this item, and no approved goal is what carried it
	// past a gate that asks about every item whatever its goal.
	notes := tracker.created[0].Notes
	if !strings.Contains(notes, "diagnosis-class work") || !strings.Contains(notes, "work_item_exemptions") {
		t.Fatalf("notes = %q, want them to name the carve-out that admitted it", notes)
	}
	if strings.Contains(notes, "approved by the operator") {
		t.Fatalf("notes = %q, want no claim of an approval nobody gave", notes)
	}
	// And what the operator reads says the same thing as the item. An account
	// naming the approved goal here while the notes named the carve-out would be
	// two records of one decision that disagree.
	if !strings.Contains(reply.Admitted[0].Basis, "diagnosis-class work") {
		t.Fatalf("basis = %q, want the carve-out that admitted it", reply.Admitted[0].Basis)
	}
	if !strings.Contains(reply.Admitted[0].Render(), "admitted because") {
		t.Fatalf("rendered = %q, want the operator told why nobody asked them", reply.Admitted[0].Render())
	}
}

// A project that has carved out nothing is unmoved by an item claiming a class.
// The exemption is the operator's decision, so a claim without one changes
// nothing at all.
func TestAClaimedClassChangesNothingWhereTheProjectExemptsNothing(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This only looks.", diagnosisProposal("Count the items carrying an attribution")),
	}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(reply.Proposals) != 1 {
		t.Fatalf("admitted = %#v and proposals = %#v; want the operator still asked", reply.Admitted, reply.Proposals)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing admitted on a class nobody exempted", tracker.created)
	}
}

// The exemption moves who is asked and never whether the work is for anything.
// Work of an exempted class that names a goal the repository does not record is
// refused exactly as any other work naming one is.
func TestExemptedWorkStillNamesAGoalTheRepositoryRecords(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This only looks.",
			`{"kind":"feature","title":"Count the plugins","description":"Read and report.","rationale":"Nobody knows.","goal":"Grow the ecosystem.","class":"diagnosis"}`),
	}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	_, err := session.Send(context.Background(), "how many plugins are there?")
	var unresolved *ProposalGoalError
	if !errors.As(err, &unresolved) {
		t.Fatalf("Send() error = %v, want the goal refused", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want an exemption that does not exempt the goal", tracker.created)
	}
}

// The direct "create" action honours the exemption the same way, because the
// product manager reaches both doors: a carve-out that opened one and not the
// other would be work arriving through whichever asked less.
func TestTheCreateActionHonoursTheExemptionUnderThePerItemGate(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the survey.",
			`{"action":"create","kind":"feature","title":"Count the items carrying an attribution","description":"Read and report.","goal":"`+recordedGoal+`","class":"diagnosis","reason":"the operator asked how many"}`)},
		{SessionID: "session-1", FinalText: "It is in the queue."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation carried out", reply.Actions)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the exempted item", tracker.created)
	}
	if !strings.Contains(tracker.created[0].Notes, "diagnosis-class work") {
		t.Fatalf("notes = %q, want them to name the carve-out", tracker.created[0].Notes)
	}
}

// A creation of ordinary work is refused under the same gate, which is the half
// of the carve-out that keeps it one.
func TestTheCreateActionIsStillRefusedForWorkOfNoExemptedClass(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the rewrite.",
			`{"action":"create","kind":"feature","title":"Rewrite the attribution resolver","description":"Change it.","goal":"`+recordedGoal+`","reason":"the operator asked"}`)},
		{SessionID: "session-1", FinalText: "It was refused; I will propose it."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "rewrite the resolver")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want the gate still standing for ordinary work", tracker.created)
	}
}

// What an exemption narrows is the per-item gate, so where a project has already
// moved that approval up to its goals there is nothing left for it to narrow. An
// exempted class under `work_items: automatic` clears exactly the gate every
// other item clears — the approved goal — and work naming a goal nobody approved
// is put to the operator whatever class it claims. An exemption that reached past
// the per-item question here would be the carve-out admitting work under a goal
// nobody agreed to, which is the opposite of a narrowing.
func TestAnExemptionDoesNotAdmitWorkUnderAGoalNobodyApproved(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This only looks.", diagnosisProposal("Count the items carrying an attribution")),
	}}})
	options.Tracker = tracker
	options.Goals = unapprovedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalAutomatic,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("admitted = %#v and created = %#v, want nothing admitted under an unapproved goal", reply.Admitted, tracker.created)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want the work put to the operator", reply.Proposals)
	}
	// And the card says why they are being asked, which under this policy is the
	// goal rather than the per-item gate the exemption would have stood down.
	if asking := reply.Proposals[0].Asking; !strings.Contains(asking, "records no approval") {
		t.Fatalf("asking = %q, want the unapproved goal named", asking)
	}
}

// The same policy with the goal approved admits the work, and says the approved
// goal is what did it. The class changed nothing here, so an item claiming the
// carve-out as its authority would be naming a decision that did not admit it.
func TestUnderAutomaticAnExemptedClassIsAdmittedOnTheApprovedGoal(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This only looks.", diagnosisProposal("Count the items carrying an attribution")),
	}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalAutomatic,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 1 || len(tracker.created) != 1 {
		t.Fatalf("admitted = %#v and created = %#v, want the work admitted on the approved goal", reply.Admitted, tracker.created)
	}
	if basis := reply.Admitted[0].Basis; !strings.Contains(basis, "approved goal") {
		t.Fatalf("basis = %q, want the approved goal rather than the carve-out", basis)
	}
	if notes := tracker.created[0].Notes; !strings.Contains(notes, "a goal the operator approved") {
		t.Fatalf("notes = %q, want the approved goal named as what admitted it", notes)
	}
}

// The direct "create" is the other door, and it narrows by exactly as much. An
// exempt class under `work_items: automatic` is held to the approved goal that
// admits every other direct creation there, because a carve-out that opened one
// door wider than the other would be work arriving through whichever asked less
// — which is the failure the exemption was written not to be.
func TestAnExemptCreateUnderAutomaticIsHeldToTheApprovedGoal(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the survey.",
			`{"action":"create","kind":"feature","title":"Count the items carrying an attribution","description":"Read and report.","goal":"`+recordedGoal+`","class":"diagnosis","reason":"the operator asked how many"}`)},
		{SessionID: "session-1", FinalText: "It was refused."},
	}})
	options.Tracker = tracker
	options.Goals = unapprovedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalAutomatic,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused under an unapproved goal", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "records no approval") {
		t.Fatalf("failure = %q, want the unapproved goal named", reply.Actions[0].Failure)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing admitted under a goal nobody approved", tracker.created)
	}
}

// And where the approved goal is what admitted an exempt creation, the item says
// so. Under `work_items: automatic` the class stood nothing down, so a note
// claiming the carve-out would record an authority nobody exercised.
func TestAnExemptCreateUnderAutomaticRecordsTheApprovedGoalThatAdmittedIt(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the survey.",
			`{"action":"create","kind":"feature","title":"Count the items carrying an attribution","description":"Read and report.","goal":"`+recordedGoal+`","class":"diagnosis","reason":"the operator asked how many"}`)},
		{SessionID: "session-1", FinalText: "It is in the queue."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{
		WorkItems: domain.ApprovalAutomatic,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "how many items carry an attribution?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the creation admitted on the approved goal", tracker.created)
	}
	if notes := tracker.created[0].Notes; strings.Contains(notes, "work_item_exemptions") {
		t.Fatalf("notes = %q, want no claim of a carve-out that admitted nothing", notes)
	}
}

// An exemption moves who is asked and never whether the work is for anything, and
// a goal the repository has nothing to check against is not a goal it records. A
// claim nobody could check is exactly what the operator is asked about, whatever
// class the work claims — admitting on it would be admitting on nothing.
func TestAnExemptionDoesNotAdmitWorkWhoseGoalNobodyCouldCheck(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply("This only looks.", diagnosisProposal("Count the items carrying an attribution")),
	}}})
	options.Tracker = tracker
	// A repository with no goals in force: the attribution is neither confirmed
	// nor denied, which is the state configuration.md calls uncheckable.
	options.Goals = goal.Set{}
	options.Admission = Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("admitted = %#v and created = %#v, want nothing admitted on an unanswerable claim", reply.Admitted, tracker.created)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want the work put to the operator instead", reply.Proposals)
	}
	if asking := reply.Proposals[0].Asking; asking == "" {
		t.Fatalf("asking = %q, want the card to say why they are being asked", asking)
	}
}

// The same on the direct door: a creation of exempt-class work is refused where
// the goal it names is one nothing could check.
func TestAnExemptCreateIsRefusedWhereTheGoalsCannotBeChecked(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the survey.",
			`{"action":"create","kind":"feature","title":"Count the items carrying an attribution","description":"Read and report.","goal":"`+recordedGoal+`","class":"diagnosis","reason":"the operator asked how many"}`)},
		{SessionID: "session-1", FinalText: "It was refused."},
	}})
	options.Tracker = tracker
	options.Goals = goal.Set{}
	options.Admission = Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "how many items carry an attribution?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v, want nothing admitted on an unanswerable claim", tracker.created)
	}
}

// A class the harness does not recognize claims an exemption that does not
// exist, so nothing in the block is carried out rather than the item quietly
// arriving as ordinary work under a word nothing reads.
func TestAClassTheHarnessDoesNotRecognizeIsRefused(t *testing.T) {
	t.Parallel()

	if err := (Proposal{
		Title:       "Count the items",
		Description: "Read and report.",
		Rationale:   "Nobody knows.",
		Goal:        recordedGoal,
		Class:       "housekeeping",
	}).Validate(); err == nil || !strings.Contains(err.Error(), "housekeeping") {
		t.Fatalf("Validate() error = %v, want the unknown class named", err)
	}
	if err := (TrackerAction{
		Action:      actionCreate,
		Title:       "Count the items",
		Description: "Read and report.",
		Goal:        recordedGoal,
		Class:       "housekeeping",
		Reason:      "the operator asked",
	}).Validate(); err == nil || !strings.Contains(err.Error(), "housekeeping") {
		t.Fatalf("Validate() error = %v, want the unknown class named", err)
	}
}

// What the product manager is told about its own project. A class it is never
// told about is one it cannot claim, and a class the harness would ignore is one
// it must not be invited to claim.
func TestTheContractStatesTheExemptionsAProjectActuallyHas(t *testing.T) {
	t.Parallel()

	gated := SystemPrompt(domain.RoleProductManager, Admission{WorkItems: domain.ApprovalHuman}, nil, "")
	if strings.Contains(gated, "diagnosis") {
		t.Fatalf("a project that exempts nothing was told about a class it does not have")
	}
	exempting := SystemPrompt(domain.RoleProductManager, Admission{
		WorkItems: domain.ApprovalHuman,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}, nil, "")
	for _, required := range []string{"diagnosis", "produces findings rather than a change", `"class"`} {
		if !strings.Contains(exempting, required) {
			t.Fatalf("the contract does not say %q", required)
		}
	}
	// And a project whose per-item gate is already up is not told about the
	// classes it exempts, because there the class changes nothing: a role told
	// about one would be told it mattered.
	automatic := SystemPrompt(domain.RoleProductManager, Admission{
		WorkItems: domain.ApprovalAutomatic,
		Exempt:    []domain.WorkItemClass{domain.WorkItemClassDiagnosis},
	}, nil, "")
	if strings.Contains(automatic, "diagnosis") {
		t.Fatalf("a project with no per-item gate was told about a class that narrows one")
	}
}
