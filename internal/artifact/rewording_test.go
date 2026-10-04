package artifact

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const rewordingGoals = "## Goals\n\n- [autonomy] The human's routine interface is the product manager.\n"

func approvedGoalsDocument(t *testing.T) Store {
	t.Helper()
	store := newStore(t)
	if _, err := store.Create(domain.RoleProductManager, Draft{
		ID: "v1-goals", Kind: KindGoals, Title: "V1 goals", Supports: []string{"brief"},
		Directory: productHome + "/goals", Body: rewordingGoals, Reason: "the goals the brief becomes",
	}, moment()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Approve("v1-goals", "approved by the operator in conversation", moment().Add(time.Hour)); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	return store
}

// The operator's ruling of 2026-09-26: a change to what the goals admit is the
// operator's, and a rewording consistent with them is delegated to the Lead Product Manager.
// So the approval stands through a rewording recorded as consistent, and does
// not stand through one recorded as fundamental or one that does not say which
// it is — the default stays with the operator.
func TestOnlyARewordingRecordedAsConsistentLeavesTheGoalsApproved(t *testing.T) {
	t.Parallel()

	reworded := strings.Replace(rewordingGoals, "the product manager", "the lead product manager", 1)
	for _, testCase := range []struct {
		name     string
		intent   Intent
		reason   string
		approved bool
	}{
		{name: "a rewording recorded as consistent, naming the item that directed it", intent: IntentConsistent,
			reason: "yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager", approved: true},
		{name: "the same, with the reason quoted", intent: IntentConsistent,
			reason: `"yoyodyne-ifd.437.11: the autonomy goal names the Lead Product Manager"`, approved: true},
		{name: "a change recorded as fundamental", intent: IntentFundamental,
			reason: "yoyodyne-ifd.500 - the autonomy goal drops the product manager"},
		{name: "an amendment that does not say which it is",
			reason: "yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager"},
		{name: "a rewording recorded as consistent that names no item", intent: IntentConsistent,
			reason: "the autonomy goal names the Lead Product Manager"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := approvedGoalsDocument(t)
			body := reworded
			amended, err := store.Amend(domain.RoleProductManager, "v1-goals", Amendment{
				Body: &body, Reason: testCase.reason, Intent: testCase.intent,
			}, moment().Add(2*time.Hour))
			if err != nil {
				t.Fatalf("Amend() error = %v", err)
			}

			// What decides it is what is written, so it is read back from the file.
			set, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			reloaded, found := set.Find("v1-goals")
			if !found || len(set.Problems) != 0 {
				t.Fatalf("reloaded = %#v, problems = %v", reloaded, set.Problems)
			}
			if last := reloaded.Revisions[len(reloaded.Revisions)-1]; last.Intent != testCase.intent {
				t.Fatalf("intent read back = %q, want %q", last.Intent, testCase.intent)
			}
			for _, recorded := range []Artifact{amended, reloaded} {
				if testCase.approved {
					if recorded.ApprovalState() != ApprovalApproved || recorded.RevisionsSinceApproval() != 0 || len(recorded.RewordingsSinceApproval()) != 1 {
						t.Fatalf("state %q, %d since, %d rewordings; want approved through one rewording",
							recorded.ApprovalState(), recorded.RevisionsSinceApproval(), len(recorded.RewordingsSinceApproval()))
					}
					continue
				}
				if recorded.ApprovalState() != ApprovalAmended || recorded.RevisionsSinceApproval() != 1 || len(recorded.RewordingsSinceApproval()) != 0 {
					t.Fatalf("state %q, %d since, %d rewordings; want amended since the approval",
						recorded.ApprovalState(), recorded.RevisionsSinceApproval(), len(recorded.RewordingsSinceApproval()))
				}
			}
		})
	}
}

// A rewording keeps the approval standing only on the record that makes it the
// Lead Product Manager's delegated decision. The same label recorded under
// another role, or on a document the Lead Product Manager does not own, is the
// operator's again, and says why.
func TestARewordingIsDelegatedOnlyToTheLeadProductManagerOverProductDocuments(t *testing.T) {
	t.Parallel()

	consistent := Revision{Action: ActionAmended, By: domain.RoleProductManager, At: moment(),
		Reason: "yoyodyne-ifd.437.11 - reworded", Intent: IntentConsistent}
	goals := Artifact{ID: "v1-goals", Kind: KindGoals}
	if delegated, why := goals.Rewording(consistent); !delegated {
		t.Fatalf("Rewording() = false, %q", why)
	}

	byArchitect := consistent
	byArchitect.By = domain.RoleArchitect
	if delegated, why := goals.Rewording(byArchitect); delegated || !strings.Contains(why, "Lead Product Manager") {
		t.Fatalf("a rewording by the architect: %v, %q", delegated, why)
	}
	for _, kind := range Kinds() {
		document := Artifact{Kind: kind}
		owner, _ := Owner(kind)
		delegated, why := document.Rewording(consistent)
		if want := owner == domain.RoleProductManager; delegated != want {
			t.Fatalf("a rewording of %s: %v, %q; want delegated %v", kind, delegated, why, want)
		}
	}
	for _, reason := range []string{"reworded for yoyodyne-ifd.437.11", "", "rename: the Lead PM"} {
		unnamed := consistent
		unnamed.Reason = reason
		if delegated, why := goals.Rewording(unnamed); delegated || !strings.Contains(why, "work item") {
			t.Fatalf("reason %q: %v, %q", reason, delegated, why)
		}
	}
}

// Intent is said of a creation or amendment, in one of two words. Anything else
// is a record that means nothing and is refused rather than read as either.
func TestIntentIsRecordedOnlyOnACreationOrAmendmentAndInOneOfTwoWords(t *testing.T) {
	t.Parallel()

	valid := Revision{Action: ActionAmended, By: domain.RoleProductManager, At: moment(), Reason: "x", Intent: IntentFundamental}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	onCreation := valid
	onCreation.Action = ActionCreated
	if err := onCreation.Validate(); err != nil {
		t.Fatalf("intent on a creation: error = %v", err)
	}
	for _, action := range []Action{ActionIdentified, ActionSuperseded, ActionRetired} {
		invalid := valid
		invalid.Action = action
		if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "intent is recorded on a creation or amendment") {
			t.Fatalf("intent on %s: error = %v", action, err)
		}
	}
	unknown := valid
	unknown.Intent = "cosmetic"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), `intent "cosmetic"`) {
		t.Fatalf("an unknown intent: error = %v", err)
	}
}

func TestEveryProductDocumentKeepsApprovalThroughOnlyConsistentRevisions(t *testing.T) {
	t.Parallel()

	for _, kind := range Owned(domain.RoleProductManager) {
		for _, intent := range []Intent{IntentConsistent, IntentFundamental, ""} {
			t.Run(string(kind)+"/"+string(intent), func(t *testing.T) {
				t.Parallel()
				store := newStore(t)
				id := "product-document"
				if _, err := store.Create(domain.RoleProductManager, Draft{
					ID: id, Kind: kind, Title: "Product document", Directory: productHome,
					Body: "The product manager records the intent.", Reason: "recorded",
				}, moment()); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Approve(id, "approved in conversation", moment().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				body := "The Lead Product Manager records the intent."
				if _, err := store.Amend(domain.RoleProductManager, id, Amendment{
					Body: &body, Intent: intent, Reason: "yoyodyne-ifd.433.19 - the owning role is named consistently",
				}, moment().Add(2*time.Hour)); err != nil {
					t.Fatal(err)
				}
				set, err := store.Load()
				if err != nil || len(set.Problems) != 0 {
					t.Fatalf("Load() error = %v, problems = %v", err, set.Problems)
				}
				recorded, found := set.Find(id)
				want := ApprovalAmended
				if intent == IntentConsistent {
					want = ApprovalApproved
				}
				if !found || recorded.ApprovalState() != want {
					t.Fatalf("found %v, state %q; want %q", found, recorded.ApprovalState(), want)
				}
			})
		}
	}
}

func TestRulesRecordingExistingIntentStartApprovedUnderTheirCreationReason(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		intent Intent
		reason string
	}{
		{"consistent", IntentConsistent, "yoyodyne-ifd.433.17 - records rules the operator already gave"},
		{"fundamental", IntentFundamental, "yoyodyne-ifd.433.17 - adds new intent"},
		{"unmarked", "", "yoyodyne-ifd.433.17 - records rules"},
		{"no directing item", IntentConsistent, "records rules the operator already gave"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := newStore(t)
			created, err := store.Create(domain.RoleProductManager, Draft{
				ID: "operating-rules", Kind: KindRules, Title: "Operating rules", Directory: productHome,
				Body: "## Rules\n\n- Notes are append-only.", Reason: testCase.reason, Intent: testCase.intent,
			}, moment())
			if err != nil {
				t.Fatal(err)
			}
			set, err := store.Load()
			if err != nil || len(set.Problems) != 0 {
				t.Fatalf("Load() error = %v, problems = %v", err, set.Problems)
			}
			reloaded, found := set.Find(created.ID)
			if !found {
				t.Fatal("the created rules document was not loaded")
			}
			for _, recorded := range []Artifact{created, reloaded} {
				if len(recorded.Approvals) != 0 || recorded.Revisions[0].Intent != testCase.intent || recorded.Revisions[0].Reason != testCase.reason {
					t.Fatalf("creation lost its reason or invented an operator approval: %#v", recorded)
				}
				want := ApprovalUnapproved
				if testCase.name == "consistent" {
					want = ApprovalApproved
				}
				if recorded.ApprovalState() != want {
					t.Fatalf("state %q; want %q", recorded.ApprovalState(), want)
				}
			}
			if testCase.name != "consistent" {
				return
			}
			// Delegation does not erase a later change of fundamental intent.
			bodies := []string{
				"## Rules\n\n- Work item notes are append-only.",
				"## Rules\n\n- Work item notes may be replaced.",
				"## Rules\n\n- The notes of a work item may be replaced.",
			}
			for index, intent := range []Intent{IntentConsistent, IntentFundamental, IntentConsistent} {
				body := bodies[index]
				amended, err := store.Amend(domain.RoleProductManager, created.ID, Amendment{
					Body: &body, Intent: intent, Reason: "yoyodyne-ifd.433.19 - rules revised",
				}, moment().Add(time.Duration(index+1)*time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				want := ApprovalApproved
				if index > 0 {
					want = ApprovalAmended
				}
				if amended.ApprovalState() != want {
					t.Fatalf("revision %d: state %q; want %q", index+1, amended.ApprovalState(), want)
				}
			}
			approved, err := store.Approve(created.ID, "operator approved the new intent", moment().Add(4*time.Hour))
			if err != nil || approved.ApprovalState() != ApprovalApproved || len(approved.Approvals) != 1 {
				t.Fatalf("Approve() = %#v, %v", approved, err)
			}
		})
	}
}

func TestDirectingItemIsTheIdentifierAReasonOpensWith(t *testing.T) {
	t.Parallel()

	for reason, want := range map[string]string{
		"yoyodyne-ifd.437.11 - reworded":   "yoyodyne-ifd.437.11",
		`"yoyodyne-ifd.437.11: reworded"`:  "yoyodyne-ifd.437.11",
		"yoyodyne-77x":                     "yoyodyne-77x",
		"(yoyodyne-ifd.12) reworded":       "yoyodyne-ifd.12",
		"reworded under yoyodyne-ifd.12":   "",
		"reworded":                         "",
		"yoyodyne-ifd.12x reworded":        "",
		"yoyodyne-ifd.437.11-bis reworded": "",
		"re-worded for clarity":            "",
		"follow-up: tightened the wording": "",
		`"re-worded"`:                      "",
	} {
		got, named := DirectingItem(reason)
		if got != want || named != (want != "") {
			t.Errorf("DirectingItem(%q) = %q, %v; want %q", reason, got, named, want)
		}
	}
}
