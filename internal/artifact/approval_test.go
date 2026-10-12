package artifact

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestApprovalIsRecordedAgainstTheRevisionItWasGivenFor(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	created, err := store.Create(domain.RoleProductManager, Draft{
		ID:        "v1-goals",
		Kind:      KindGoals,
		Title:     "V1 goals",
		Supports:  []string{"brief"},
		Directory: productHome + "/goals",
		Body:      "## Goals\n\n- Run development nearly autonomously.",
		Reason:    "the goals the brief becomes",
	}, moment())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ApprovalState() != ApprovalUnapproved {
		t.Fatalf("a document nobody approved reads as %q", created.ApprovalState())
	}

	approved, err := store.Approve("v1-goals", "approved by the operator in conversation on 2026-08-17", moment().Add(time.Hour))
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if approved.ApprovalState() != ApprovalApproved || approved.RevisionsSinceApproval() != 0 {
		t.Fatalf("approved = %#v", approved)
	}
	latest, recorded := approved.LatestApproval()
	if !recorded || latest.Revision != 0 || latest.By != ApproverOperator {
		t.Fatalf("approval = %#v", latest)
	}
	// Approving records the approval and nothing else. A record that could also
	// change what the document says would be an edit under another name.
	if approved.Status != created.Status || approved.Title != created.Title {
		t.Fatalf("approving changed the document: %#v", approved)
	}
	if body := documentBody(t, store, approved.Path); !strings.Contains(body, "Run development nearly autonomously.") {
		t.Fatalf("body = %q", body)
	}

	// An approval is durable exactly insofar as the loader that reads a
	// hand-written document reads it back.
	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	reloaded, found := set.Find("v1-goals")
	if !found || len(set.Problems) != 0 {
		t.Fatalf("reloaded = %#v, problems = %v", reloaded, set.Problems)
	}
	if reloaded.ApprovalState() != ApprovalApproved || len(reloaded.Approvals) != 1 {
		t.Fatalf("reloaded = %#v", reloaded)
	}
	if reloaded.Approvals[0].Reason != "approved by the operator in conversation on 2026-08-17" {
		t.Fatalf("approval = %#v", reloaded.Approvals[0])
	}

	// The whole point: a document amended after it was approved is not the
	// document that was approved, and says so.
	title := "V1 goals, restated"
	amended, err := store.Amend(domain.RoleProductManager, "v1-goals", Amendment{
		Title:  &title,
		Reason: "named the version these goals are for",
	}, moment().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if amended.ApprovalState() != ApprovalAmended || amended.RevisionsSinceApproval() != 1 {
		t.Fatalf("amended = %#v", amended)
	}
	// The approval still stands for what it was given for. Nothing was withdrawn,
	// and nothing about the document was refused for being amended.
	if latest, recorded := amended.LatestApproval(); !recorded || latest.Revision != 0 {
		t.Fatalf("approval = %#v", latest)
	}

	reapproved, err := store.Approve("v1-goals", "approved again by the operator after the restatement", moment().Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if reapproved.ApprovalState() != ApprovalApproved || len(reapproved.Approvals) != 2 {
		t.Fatalf("reapproved = %#v", reapproved)
	}
	if latest, _ := reapproved.LatestApproval(); latest.Revision != 1 {
		t.Fatalf("approval = %#v", latest)
	}
}

func TestAnApprovalThatWouldRecordNothingIsRefused(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	if _, err := store.Create(domain.RoleProductManager, Draft{
		ID: "brief", Kind: KindBrief, Title: "Product brief", Directory: productHome,
		Body: "Intent in, software out.", Reason: "the first statement of what this product is for",
	}, moment()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	before := snapshot(t, store.RepositoryRoot)

	// An approval nobody can trace back to how it was given is a claim made on
	// the operator's behalf rather than a record of what they did.
	if _, err := store.Approve("brief", "  ", moment()); err == nil {
		t.Fatal("Approve() recorded an approval with no reason")
	}
	if _, err := store.Approve("nothing-by-this-name", "because", moment()); err == nil {
		t.Fatal("Approve() approved an artifact nobody recorded")
	}
	assertUnchanged(t, before, snapshot(t, store.RepositoryRoot))

	if _, err := store.Approve("brief", "approved in conversation", moment().Add(time.Hour)); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	// A second approval of the same revision would record a decision nobody took
	// a second time, and two entries about one version that could disagree.
	if _, err := store.Approve("brief", "approved again", moment().Add(2*time.Hour)); err == nil {
		t.Fatal("Approve() approved one revision twice")
	}

	if _, err := store.Retire(domain.RoleProductManager, "brief", "the product it stated intent for was abandoned", moment().Add(3*time.Hour)); err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	// Approving what has stopped applying would record agreement to intent that
	// is no longer anybody's.
	if _, err := store.Approve("brief", "approved after the fact", moment().Add(4*time.Hour)); err == nil {
		t.Fatal("Approve() approved a retired artifact")
	}
}

func TestAnApprovalThatNamesNoRevisionIsRefusedWithTheDocument(t *testing.T) {
	t.Parallel()

	// The store is not the only thing that writes these files, so what a
	// hand-written approval says is held to the same contract. Unlike a revision
	// recorded under the wrong role, a broken approval is refused rather than
	// reported: it says nothing about the document at all, and reading it as an
	// approval would be worse than reading it as missing.
	for name, approvals := range map[string]string{
		"a revision the document does not have": "approvals:\n    - revision: 4\n      by: operator\n      at: 2026-08-17T12:00:00Z\n      reason: approved in conversation\n",
		"a revision that is not an index":       "approvals:\n    - revision: -1\n      by: operator\n      at: 2026-08-17T12:00:00Z\n      reason: approved in conversation\n",
		"an approver who is not the operator":   "approvals:\n    - revision: 0\n      by: product-manager\n      at: 2026-08-17T12:00:00Z\n      reason: the role that wrote it approved it\n",
		"no reason at all":                      "approvals:\n    - revision: 0\n      by: operator\n      at: 2026-08-17T12:00:00Z\n      reason: \"\"\n",
		"no time it was given":                  "approvals:\n    - revision: 0\n      by: operator\n      reason: approved in conversation\n",
		"approvals recorded out of order":       "approvals:\n    - revision: 0\n      by: operator\n      at: 2026-08-17T12:00:00Z\n      reason: approved in conversation\n    - revision: 0\n      by: operator\n      at: 2026-08-18T12:00:00Z\n      reason: approved again\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newStore(t)
			write(t, store, productHome+"/brief.md",
				strings.TrimSuffix(document("brief", "brief", "Product brief", nil, "active"), "---\n")+approvals+"---\n\nIntent in, software out.\n")

			set, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if _, found := set.Find("brief"); found {
				t.Fatalf("an artifact with %s was loaded as approved", name)
			}
			if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Reason, "approvals[") {
				t.Fatalf("problems = %v", set.Problems)
			}
		})
	}
}

func TestWhatRequiresApprovalIsTheProjectsToSay(t *testing.T) {
	t.Parallel()

	// The shipped default: the operator approves the brief and the goals that
	// bound it, and the designs derived from them follow without asking.
	policy := Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic}
	for kind, want := range map[Kind]bool{
		KindBrief:         true,
		KindGoals:         true,
		KindNonGoals:      true,
		KindRules:         true,
		KindDesign:        false,
		KindSpecification: false,
		KindDecision:      false,
	} {
		if requires := policy.Requires(kind); requires != want {
			t.Errorf("Requires(%q) = %v, want %v", kind, requires, want)
		}
	}
	if setting, mode, governed := policy.Setting(KindNonGoals); !governed || setting != "approvals.goals" || mode != domain.ApprovalHuman {
		t.Errorf("Setting(non-goals) = %q, %q, %v", setting, mode, governed)
	}
	// A decision record is the architect's, and is governed as the architect's
	// designs are.
	if setting, mode, governed := policy.Setting(KindDecision); !governed || setting != "approvals.designs" || mode != domain.ApprovalAutomatic {
		t.Errorf("Setting(decision) = %q, %q, %v", setting, mode, governed)
	}

	// A project that decided otherwise gets what it decided, in both directions.
	relaxed := Policy{Brief: domain.ApprovalAutomatic, Goals: domain.ApprovalAutomatic, Designs: domain.ApprovalHuman}
	if relaxed.Requires(KindGoals) || !relaxed.Requires(KindDesign) || !relaxed.Requires(KindDecision) {
		t.Errorf("relaxed = %#v", relaxed)
	}
}

// A document filed in the specification home is product intent by where it is
// filed, so one that is neither the brief nor the goals is governed by the goals'
// approval rather than by the designs', and the same kind filed elsewhere is not.
func TestADocumentInTheSpecificationHomeIsGovernedAsTheGoalsAre(t *testing.T) {
	policy := Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic, SpecificationsHome: "docs/product"}

	inHome := Artifact{ID: "pricing-principles", Kind: KindSpecification, Path: "docs/product/pricing-principles.md"}
	if setting, mode, governed := policy.SettingFor(inHome); !governed || setting != "approvals.goals" || mode != domain.ApprovalHuman {
		t.Fatalf("SettingFor(in the home) = %q, %q, %t; want approvals.goals, human, true", setting, mode, governed)
	}
	if !policy.RequiresFor(inHome) {
		t.Fatal("a document in the specification home must ask for the operator's approval as the goals do")
	}

	elsewhere := Artifact{ID: "harness", Kind: KindSpecification, Path: "docs/designs/harness.md"}
	if setting, _, _ := policy.SettingFor(elsewhere); setting != "approvals.designs" {
		t.Fatalf("SettingFor(outside the home) = %q, want approvals.designs", setting)
	}
	// A home that merely shares a prefix with the path is not the home.
	lookalike := Artifact{ID: "x", Kind: KindDesign, Path: "docs/productive/x.md"}
	if setting, _, _ := policy.SettingFor(lookalike); setting != "approvals.designs" {
		t.Fatalf("SettingFor(a lookalike directory) = %q, want approvals.designs", setting)
	}
	brief := Artifact{ID: "brief", Kind: KindBrief, Path: "docs/product/brief.md"}
	if setting, _, _ := policy.SettingFor(brief); setting != "approvals.brief" {
		t.Fatalf("SettingFor(the brief) = %q, want approvals.brief", setting)
	}
}
