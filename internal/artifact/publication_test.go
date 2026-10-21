package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestAutomaticConfirmationPreservesBodyAndDoesNotWritePrimary(t *testing.T) {
	store := generatedStore(t.TempDir())
	body := "\n\n# Owner's words  \n\nKeep this spacing.\n\n"
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, Write{Action: WriteCreate, ID: "saved-design", Kind: KindDesign, Title: "Saved design", Directory: designsHome, Body: body, Reason: "owner decision"}, Policy{Designs: domain.ApprovalAutomatic}, "confirmed under policy", moment())
	if err != nil || !automatic {
		t.Fatalf("confirmation: %v, %v", automatic, err)
	}
	if !strings.HasSuffix(candidate.Content, body) {
		t.Fatalf("body changed: %q", candidate.Content)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.RepositoryRoot, candidate.Artifact.Path)); !os.IsNotExist(err) {
		t.Fatalf("primary was written: %v", err)
	}
}

func TestIntentRequiresConsistentOwnerRevisionBeforeAutomaticConfirmation(t *testing.T) {
	// Every kind governed as product intent is held to the claim, not only the
	// brief and the goals: the non-goals and the operating rules bound what the
	// goals admit just as much.
	for _, kind := range []Kind{KindBrief, KindGoals, KindNonGoals, KindRules} {
		for _, intent := range []Intent{"", IntentFundamental, IntentConsistent} {
			t.Run(string(kind)+"/"+string(intent), func(t *testing.T) {
				store := generatedStore(t.TempDir())
				id := "intent-document"
				if _, err := store.Create(domain.RoleProductManager, Draft{ID: id, Kind: kind, Title: "Product intent", Directory: productHome, Body: "# Goals\n\n- Keep the boundary.", Reason: "created by owner"}, moment()); err != nil {
					t.Fatal(err)
				}
				candidate, automatic, err := store.PrepareConfirmation(domain.RoleProductManager, Write{Action: WriteRevise, ID: id, Body: "# Goals\n\n- Keep the same boundary.", Reason: "yoyodyne-ifd.500 - consistent wording", Intent: intent}, Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic}, "confirmed under policy", moment().Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				if automatic != (intent == IntentConsistent) {
					t.Fatalf("intent %q automatic = %v", intent, automatic)
				}
				if automatic {
					if err := candidate.Validate(); err != nil {
						t.Fatal(err)
					}
				}
				// A creation that does not say it records intent already given is
				// new product intent, and the operator's whatever the setting says.
				fresh := Write{Action: WriteCreate, ID: id + "-new", Kind: kind, Title: "New product intent", Directory: productHome, Body: "# Goals\n\n- A new boundary.", Reason: "yoyodyne-ifd.500 - new intent"}
				if _, automatic, err := store.PrepareConfirmation(domain.RoleProductManager, fresh, Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic}, "confirmed under policy", moment().Add(time.Hour)); err != nil || automatic {
					t.Fatalf("new %s confirmed by policy: %v, %v", kind, automatic, err)
				}
			})
		}
	}
}

func TestWaitingCreationAlreadyTranscribedOnlyConfirmsMatchingContent(t *testing.T) {
	store := generatedStore(t.TempDir())
	draft := Draft{ID: "waiting-design", Kind: KindDesign, Title: "Saved design", Directory: designsHome, Body: "# Saved\n\nOwner's words.", Reason: "owner decision"}
	if _, err := store.Create(domain.RoleArchitect, draft, moment()); err != nil {
		t.Fatal(err)
	}
	proposal := Write{Action: WriteCreate, ID: draft.ID, Kind: draft.Kind, Title: draft.Title, Directory: draft.Directory, Body: draft.Body, Reason: draft.Reason}
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, proposal, Policy{Designs: domain.ApprovalAutomatic}, "confirmed under policy", moment().Add(time.Hour))
	if err != nil || !automatic {
		t.Fatalf("confirmation: %v, %v", automatic, err)
	}
	if len(candidate.Artifact.Revisions) != 2 || candidate.Before == "" {
		t.Fatal("existing document was not safely revised")
	}
	proposal.Body = "# Different words"
	if _, _, err := store.PrepareConfirmation(domain.RoleArchitect, proposal, Policy{Designs: domain.ApprovalAutomatic}, "confirmed under policy", moment().Add(time.Hour)); err == nil {
		t.Fatal("different existing content overwritten")
	}
}

func TestIntentKindCannotBeConfirmedUnderTheDesignsSetting(t *testing.T) {
	store := generatedStore(t.TempDir())
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, Write{Action: WriteCreate, ID: "saved-design", Kind: KindDesign, Title: "Saved design", Directory: designsHome, Body: "# Design", Reason: "owner decision"}, Policy{Designs: domain.ApprovalAutomatic}, "confirmed under policy", moment())
	if err != nil || !automatic {
		t.Fatalf("confirmation: %v, %v", automatic, err)
	}
	for _, kind := range []Kind{KindNonGoals, KindRules} {
		relabelled := candidate
		relabelled.Artifact.Kind = kind
		relabelled.Content = strings.Replace(candidate.Content, "kind: design", "kind: "+string(kind), 1)
		if err := relabelled.Validate(); err == nil || !strings.Contains(err.Error(), "product intent") {
			t.Fatalf("%s confirmed under approvals.designs: %v", kind, err)
		}
	}
}

// A decision record is the architect's, and is confirmed or put to the operator
// under approvals.designs exactly as a design is.
func TestADecisionRecordIsConfirmedUnderTheDesignsSetting(t *testing.T) {
	store := generatedStore(t.TempDir())
	record := Write{Action: WriteCreate, ID: "authority-by-capability", Kind: KindDecision, Title: "Authority by capability", Directory: decisionsHome, Body: "# Authority by capability", Reason: "the architect's ruling"}
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, record, Policy{Designs: domain.ApprovalAutomatic}, "confirmed under policy", moment())
	if err != nil || !automatic {
		t.Fatalf("confirmation: %v, %v", automatic, err)
	}
	if approval, _ := candidate.Artifact.LatestApproval(); approval.By != ApproverHarness || approval.Policy != "approvals.designs" {
		t.Fatalf("approval = %+v", approval)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, record, Policy{Designs: domain.ApprovalHuman}, "confirmed under policy", moment()); err != nil || automatic {
		t.Fatalf("confirmed under a human designs setting: %v, %v", automatic, err)
	}
}

// The operator may approve any revision its owner may write, a change of
// fundamental intent included, and the candidate carries their approval
// rather than the harness's, without anything reaching the primary checkout.
func TestOperatorApprovalPreparesACandidateForAnyRevisionWithoutWritingPrimary(t *testing.T) {
	for _, kind := range []Kind{KindGoals, KindNonGoals} {
		t.Run(string(kind), func(t *testing.T) {
			store := generatedStore(t.TempDir())
			id := "intent-document"
			if _, err := store.Create(domain.RoleProductManager, Draft{ID: id, Kind: kind, Title: "Product intent", Directory: productHome, Body: "# Goals\n\n- Keep the boundary.", Reason: "created by owner"}, moment()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.RepositoryRoot, productHome, id+".md")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := store.PrepareApproval(domain.RoleProductManager, Write{Action: WriteRevise, ID: id, Body: "# Goals\n\n- A wider boundary.", Reason: "the operator widened the boundary", Intent: IntentFundamental}, "approved by the operator in conversation chat-1, turn 2", moment().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := candidate.Validate(); err != nil {
				t.Fatal(err)
			}
			approval, _ := candidate.Artifact.LatestApproval()
			if !candidate.OperatorApproved() || approval.By != ApproverOperator || approval.Policy != "" || approval.Revision != len(candidate.Artifact.Revisions)-1 {
				t.Fatalf("approval = %+v", approval)
			}
			if candidate.Before != DocumentIdentity(before) || !strings.Contains(candidate.Content, "A wider boundary") {
				t.Fatalf("candidate = %+v", candidate)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("primary was written: %v", err)
			}
			if _, err := store.PrepareApproval(domain.RoleArchitect, Write{Action: WriteRevise, ID: id, Body: "# Goals", Reason: "not the owner"}, "approved", moment().Add(time.Hour)); err == nil {
				t.Fatal("an approval let a role write a document it does not own")
			}
		})
	}
}

// Recording rules the operator already gave is a change consistent with intent,
// and the Lead Product Manager's to approve: a rules document she creates marked
// consistent, naming the item that directed it, is not put to the operator
// whatever approvals.goals says, and its approval is recorded as hers, naming her
// and that item. Created unmarked or marked fundamental, it waits for him.
func TestARulesDocumentCreatedConsistentIsApprovedByTheLeadProductManager(t *testing.T) {
	for _, mode := range []domain.ApprovalMode{domain.ApprovalHuman, domain.ApprovalAutomatic} {
		for _, intent := range []Intent{IntentConsistent, IntentFundamental, ""} {
			t.Run(string(mode)+"/"+string(intent), func(t *testing.T) {
				store := generatedStore(t.TempDir())
				rules := Write{Action: WriteCreate, ID: "operating-rules", Kind: KindRules, Title: "Operating rules", Supports: []string{"brief"}, Directory: productHome,
					Body: "# Operating rules\n\n## Rules\n\n- A work item's notes are append-only.", Reason: "yoyodyne-ifd.433.17 - the standing rules the operator already gave", Intent: intent}
				candidate, approved, err := store.PrepareConfirmation(domain.RoleProductManager, rules, Policy{Brief: mode, Goals: mode, SpecificationsHome: productHome}, "confirmed under policy", moment())
				if err != nil {
					t.Fatal(err)
				}
				if approved != (intent == IntentConsistent) {
					t.Fatalf("intent %q under %s: approved without the operator = %v", intent, mode, approved)
				}
				if !approved {
					return
				}
				if err := candidate.Validate(); err != nil {
					t.Fatal(err)
				}
				approval, _ := candidate.Artifact.LatestApproval()
				if !candidate.ProductManagerApproved() || candidate.OperatorApproved() || approval.By != ApproverProductManager || approval.Policy != "" || approval.Revision != 0 {
					t.Fatalf("approval = %+v", approval)
				}
				if !strings.HasPrefix(approval.Reason, "yoyodyne-ifd.433.17 - ") || !strings.Contains(approval.Reason, domain.RoleProductManager.Title()) {
					t.Fatalf("the approval does not name the Lead Product Manager and the directing item: %q", approval.Reason)
				}
				// What it reads as once landed is what the file says.
				if err := os.MkdirAll(filepath.Join(store.RepositoryRoot, productHome), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store.RepositoryRoot, candidate.Artifact.Path), []byte(candidate.Content), 0o644); err != nil {
					t.Fatal(err)
				}
				set, err := store.Load()
				if err != nil || len(set.Problems) != 0 {
					t.Fatalf("Load() error = %v, problems = %v", err, set.Problems)
				}
				landed, found := set.Find("operating-rules")
				if !found || landed.ApprovalState() != ApprovalApproved || landed.Revisions[0].Intent != IntentConsistent {
					t.Fatalf("landed = %+v, state %q", landed, landed.ApprovalState())
				}
				if (Policy{Goals: mode, SpecificationsHome: productHome}).RequiresFor(landed) != true {
					t.Fatal("a document of the product's intent stopped being the operator's to approve")
				}
			})
		}
	}
}

// A change of fundamental intent is the operator's whatever the setting for
// its kind says, and so is a change that does not say what it is: a goals
// document under an automatic approvals.goals, revised and marked fundamental,
// waits for him rather than being confirmed by the setting. The same revision
// marked consistent is the Lead Product Manager's, under a human setting too.
func TestARevisionMarkedFundamentalWaitsForTheOperatorUnderAnAutomaticSetting(t *testing.T) {
	for _, kind := range []Kind{KindBrief, KindGoals, KindNonGoals, KindRules} {
		t.Run(string(kind), func(t *testing.T) {
			store := generatedStore(t.TempDir())
			id := "intent-document"
			if _, err := store.Create(domain.RoleProductManager, Draft{ID: id, Kind: kind, Title: "Product intent", Directory: productHome, Body: "# Goals\n\n- Keep the boundary.", Reason: "created by owner"}, moment()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Approve(id, "approved by the operator in conversation", moment().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			automatic := Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic, Designs: domain.ApprovalAutomatic, SpecificationsHome: productHome}
			for _, intent := range []Intent{IntentFundamental, ""} {
				widened := Write{Action: WriteRevise, ID: id, Body: "# Goals\n\n- A wider boundary.", Reason: "yoyodyne-ifd.500 - the boundary widens", Intent: intent}
				if _, confirmed, err := store.PrepareConfirmation(domain.RoleProductManager, widened, automatic, "confirmed under policy", moment().Add(time.Hour)); err != nil || confirmed {
					t.Fatalf("intent %q confirmed without the operator under an automatic setting: %v, %v", intent, confirmed, err)
				}
			}
			reworded := Write{Action: WriteRevise, ID: id, Body: "# Goals\n\n- Keep the same boundary.", Reason: "yoyodyne-ifd.500 - reworded", Intent: IntentConsistent}
			candidate, confirmed, err := store.PrepareConfirmation(domain.RoleProductManager, reworded, Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, SpecificationsHome: productHome}, "confirmed under policy", moment().Add(time.Hour))
			if err != nil || !confirmed || !candidate.ProductManagerApproved() {
				t.Fatalf("a consistent revision under a human setting: %v, %v, %+v", confirmed, err, candidate.Artifact.Approvals)
			}
			if approval, _ := candidate.Artifact.LatestApproval(); approval.Revision != 1 || !strings.HasPrefix(approval.Reason, "yoyodyne-ifd.500 - ") {
				t.Fatalf("approval = %+v", approval)
			}
		})
	}
}

// The Lead Product Manager's approval is recorded only for the change the
// operator delegated to her. One written by hand onto any other revision, one
// naming a policy, and one whose reason names no directing item are refused.
func TestTheLeadProductManagersApprovalIsRefusedOffTheChangeDelegatedToHer(t *testing.T) {
	store := generatedStore(t.TempDir())
	candidate, confirmed, err := store.PrepareConfirmation(domain.RoleProductManager, Write{Action: WriteCreate, ID: "operating-rules", Kind: KindRules, Title: "Operating rules", Directory: productHome, Body: "# Rules", Reason: "yoyodyne-ifd.433.17 - rules already given", Intent: IntentConsistent}, Policy{Goals: domain.ApprovalHuman}, "", moment())
	if err != nil || !confirmed {
		t.Fatalf("%v, %v", confirmed, err)
	}
	for name, mutate := range map[string]func(*Artifact){
		"a fundamental revision": func(a *Artifact) { a.Revisions[0].Intent = IntentFundamental },
		"an unmarked revision":   func(a *Artifact) { a.Revisions[0].Intent = "" },
		"a policy":               func(a *Artifact) { a.Approvals[0].Policy = "approvals.goals" },
		"no directing item":      func(a *Artifact) { a.Approvals[0].Reason = "approved by the Lead Product Manager" },
	} {
		changed := candidate.Artifact
		changed.Revisions = append([]Revision(nil), candidate.Artifact.Revisions...)
		changed.Approvals = append([]Approval(nil), candidate.Artifact.Approvals...)
		mutate(&changed)
		if err := changed.Validate(); err == nil {
			t.Fatalf("%s: the Lead Product Manager's approval was accepted", name)
		}
	}
}
