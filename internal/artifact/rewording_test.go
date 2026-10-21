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

// A change keeps the approval standing only on the record that makes it the
// Lead Product Manager's delegated decision. The same label recorded under
// another role, or on a document that is not the product's intent, is the
// operator's again, and says why. Every document of the product's intent is
// covered, a creation as well as an amendment.
func TestAChangeIsDelegatedOnlyToTheLeadProductManagerOverTheProductsIntent(t *testing.T) {
	t.Parallel()

	consistent := Revision{Action: ActionAmended, By: domain.RoleProductManager, At: moment(),
		Reason: "yoyodyne-ifd.437.11 - reworded", Intent: IntentConsistent}
	for _, kind := range Kinds() {
		for _, action := range []Action{ActionAmended, ActionCreated} {
			revision := consistent
			revision.Action = action
			delegated, why := Artifact{Kind: kind}.Rewording(revision)
			if delegated != IntentKind(kind) {
				t.Fatalf("%s of a %s document: %v, %q", action, kind, delegated, why)
			}
		}
	}
	goals := Artifact{ID: "v1-goals", Kind: KindGoals}
	if delegated, why := (Artifact{ID: "a-design", Kind: KindDesign}).Rewording(consistent); delegated || !strings.Contains(why, "design") {
		t.Fatalf("a rewording of a design: %v, %q", delegated, why)
	}

	byArchitect := consistent
	byArchitect.By = domain.RoleArchitect
	if delegated, why := goals.Rewording(byArchitect); delegated || !strings.Contains(why, "Lead Product Manager") {
		t.Fatalf("a rewording by the architect: %v, %q", delegated, why)
	}
	identified := consistent
	identified.Action = ActionIdentified
	if delegated, _ := goals.Rewording(identified); delegated {
		t.Fatal("an identity revision read as a delegated change")
	}
	for _, reason := range []string{"reworded for yoyodyne-ifd.437.11", "", "rename: the Lead PM"} {
		unnamed := consistent
		unnamed.Reason = reason
		if delegated, why := goals.Rewording(unnamed); delegated || !strings.Contains(why, "work item") {
			t.Fatalf("reason %q: %v, %q", reason, delegated, why)
		}
	}
}

// Intent is said of a creation or an amendment, in one of two words. Anything
// else is a record that means nothing and is refused rather than read as either.
func TestIntentIsRecordedOnlyOnACreationOrAnAmendmentAndInOneOfTwoWords(t *testing.T) {
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
		elsewhere := valid
		elsewhere.Action = action
		if err := elsewhere.Validate(); err == nil || !strings.Contains(err.Error(), "intent is recorded on a creation or an amendment") {
			t.Fatalf("intent on %s: error = %v", action, err)
		}
	}
	unknown := valid
	unknown.Intent = "cosmetic"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), `intent "cosmetic"`) {
		t.Fatalf("an unknown intent: error = %v", err)
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
