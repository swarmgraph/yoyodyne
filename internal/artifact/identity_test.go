package artifact

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const unidentifiedGoals = "## Goals\n\n- Maintain a traceable chain.\n  *Supports: every change traces to intent.*\n- Run development nearly autonomously.\n"

const identifiedGoals = "## Goals\n\n- [traceable-chain] Maintain a traceable chain.\n  *Supports: every change traces to intent.*\n- [run-development-nearly-autonomously] Run development nearly autonomously.\n"

func approvedGoals(t *testing.T) Store {
	t.Helper()
	store := newStore(t)
	if _, err := store.Create(domain.RoleProductManager, Draft{
		ID: "v1-goals", Kind: KindGoals, Title: "V1 goals", Supports: []string{"brief"},
		Directory: productHome + "/goals", Body: unidentifiedGoals, Reason: "the goals the brief becomes",
	}, moment()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Approve("v1-goals", "approved by the operator in conversation", moment().Add(time.Hour)); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	return store
}

// Giving an approved goals document's goals identifiers changes no goal's words,
// so it is not an amendment: the approval stands through it, and nothing the
// operator agreed to has to be agreed to again.
func TestAnIdentityRevisionLeavesTheApprovalStanding(t *testing.T) {
	t.Parallel()

	store := approvedGoals(t)
	identified, err := store.Identify(domain.RoleProductManager, "v1-goals", identifiedGoals,
		"yoyodyne-ifd.344 - goal identifiers recorded", moment().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Identify() error = %v", err)
	}
	if last := identified.Revisions[len(identified.Revisions)-1]; last.Action != ActionIdentified || last.By != domain.RoleProductManager {
		t.Fatalf("revision = %#v", last)
	}
	if identified.ApprovalState() != ApprovalApproved || identified.RevisionsSinceApproval() != 0 {
		t.Fatalf("an identity revision moved the approval: state %q, %d since", identified.ApprovalState(), identified.RevisionsSinceApproval())
	}
	if body := documentBody(t, store, identified.Path); !strings.Contains(body, "- [traceable-chain] Maintain a traceable chain.") {
		t.Fatalf("body = %q", body)
	}

	// What is written is what the next load reads back.
	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	reloaded, found := set.Find("v1-goals")
	if !found || reloaded.ApprovalState() != ApprovalApproved {
		t.Fatalf("reloaded = %#v, problems = %v", reloaded, set.Problems)
	}
	// The approval stands through the identity revision without being asked for
	// again, and nothing above approved anything: the one approval still names
	// the revision the operator was asked about.
	if len(identified.Approvals) != 1 || identified.Approvals[0].Revision != 0 {
		t.Fatalf("approvals = %#v, want only the operator's of revision 0", identified.Approvals)
	}

	// An amendment afterwards is still an amendment, and counts once: the identity
	// revision between the approval and it is not a second one.
	title := "V1 goals, restated"
	amended, err := store.Amend(domain.RoleProductManager, "v1-goals", Amendment{Title: &title, Reason: "restated"}, moment().Add(4*time.Hour))
	if err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if amended.ApprovalState() != ApprovalAmended || amended.RevisionsSinceApproval() != 1 {
		t.Fatalf("amended: state %q, %d since", amended.ApprovalState(), amended.RevisionsSinceApproval())
	}
}

// The action is held to the claim it makes. A body that changed a word, a line,
// or nothing at all is not an identity revision, and recording it as one would
// be an amendment that left the approval standing.
func TestAnIdentityRevisionThatChangesAnythingElseIsRefused(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"a goal's words changed alongside its identifier": strings.Replace(identifiedGoals, "Maintain a traceable chain.", "Maintain an auditable chain.", 1),
		"a goal's words changed with no identifier":       strings.Replace(unidentifiedGoals, "Maintain a traceable chain.", "Maintain an auditable chain.", 1),
		"a goal added":      identifiedGoals + "- [review] Review every change.\n",
		"a trailer changed": strings.Replace(identifiedGoals, "every change traces to intent.", "most changes trace to intent.", 1),
		"nothing changed":   unidentifiedGoals,
		"a bracketed run of prose rather than an identifier": strings.Replace(unidentifiedGoals, "- Maintain", "- [see the brief] Maintain", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := approvedGoals(t)
			before := snapshot(t, store.RepositoryRoot)
			if _, err := store.Identify(domain.RoleProductManager, "v1-goals", body, "identifiers", moment().Add(2*time.Hour)); err == nil {
				t.Fatalf("Identify() recorded %s as an identity revision", name)
			}
			assertUnchanged(t, before, snapshot(t, store.RepositoryRoot))
		})
	}
}

// Recording identifiers is a change to the document, so it is the owning role's
// like any other, and it is a goals document's: nothing else states goal
// identities.
func TestAnIdentityRevisionIsTheOwningRolesAndOnlyOfAGoalsDocument(t *testing.T) {
	t.Parallel()

	store := approvedGoals(t)
	if _, err := store.Identify(domain.RoleArchitect, "v1-goals", identifiedGoals, "identifiers", moment().Add(2*time.Hour)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Identify() by the architect error = %v, want ErrUnauthorized", err)
	}
	if _, err := store.Create(domain.RoleProductManager, Draft{
		ID: "brief", Kind: KindBrief, Title: "Product brief", Directory: productHome,
		Body: unidentifiedGoals, Reason: "the brief",
	}, moment()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Identify(domain.RoleProductManager, "brief", identifiedGoals, "identifiers", moment().Add(2*time.Hour)); err == nil {
		t.Fatal("Identify() recorded an identity revision against a brief")
	}
}
