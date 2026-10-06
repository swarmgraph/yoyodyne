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
				// A creation records no amendment at all, so it is never confirmed
				// by policy: new product intent is the operator's.
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
