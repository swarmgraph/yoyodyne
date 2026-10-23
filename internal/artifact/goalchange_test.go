package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const twoGoals = `# The goals

These are the goals.

## Goals

- Keep the boundary where it is, and say so
  when anybody asks.
  *Supports: the boundary holds.*
- [spend-follows-the-work] Spend follows the work.
  *Supports: the operator can see.*

## Non-goals

- A hosted control plane.
`

func TestGoalStatementsReadsTheEntriesUnderTheGoalsHeading(t *testing.T) {
	got := GoalStatements(twoGoals)
	want := []string{"Keep the boundary where it is, and say so when anybody asks.", "[spend-follows-the-work] Spend follows the work."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("GoalStatements() = %q, want %q", got, want)
	}
}

func TestGoalChangeTellsARewordingFromAGoalAddedOrRemoved(t *testing.T) {
	for _, test := range []struct {
		name, after, want string
	}{
		{"unchanged", twoGoals, ""},
		{"a goal reworded in place", strings.Replace(twoGoals, "and say so", "and say plainly", 1), ""},
		{"an identified goal reworded", strings.Replace(twoGoals, "Spend follows the work.", "Spend follows the task.", 1), ""},
		{"goals reordered", "## Goals\n\n- [spend-follows-the-work] Spend follows the work.\n- Keep the boundary where it is, and say so when anybody asks.\n", ""},
		{"a goal added", strings.Replace(twoGoals, "## Non-goals", "- Ship on Fridays.\n\n## Non-goals", 1), `it adds the goal "Ship on Fridays."`},
		{"a goal removed", strings.Replace(twoGoals, "- [spend-follows-the-work] Spend follows the work.\n  *Supports: the operator can see.*\n", "", 1), `it removes the goal "[spend-follows-the-work] Spend follows the work."`},
		{"an identified goal swapped for another", strings.Replace(twoGoals, "[spend-follows-the-work] Spend follows the work.", "[ship-on-fridays] Ship on Fridays.", 1), `it adds the goal "[ship-on-fridays] Ship on Fridays.", and removes the goal "[spend-follows-the-work] Spend follows the work."`},
		{"a non-goal added", strings.Replace(twoGoals, "- A hosted control plane.", "- A hosted control plane.\n- Remote execution.", 1), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := GoalChange(twoGoals, test.after); got != test.want {
				t.Fatalf("GoalChange() = %q, want %q", got, test.want)
			}
		})
	}
}

// A goals document or a brief the Lead Product Manager creates is the
// operator's to approve however she marks it: every goal it states is new. It
// waits for him under either approval setting, and says why.
func TestACreatedGoalsDocumentMarkedConsistentWaitsForTheOperator(t *testing.T) {
	for _, kind := range []Kind{KindGoals, KindBrief} {
		for _, mode := range []domain.ApprovalMode{domain.ApprovalHuman, domain.ApprovalAutomatic} {
			t.Run(string(kind)+"/"+string(mode), func(t *testing.T) {
				store := generatedStore(t.TempDir())
				created := Write{Action: WriteCreate, ID: "new-goals", Kind: kind, Title: "New goals", Directory: productHome, Body: twoGoals, Reason: "yoyodyne-ifd.500 - goals the operator asked for", Intent: IntentConsistent}
				policy := Policy{Brief: mode, Goals: mode, SpecificationsHome: productHome}
				if _, approved, err := store.PrepareConfirmation(domain.RoleProductManager, created, policy, "confirmed under policy", moment()); err != nil || approved {
					t.Fatalf("a created %s document marked consistent was approved without the operator: %v, %v", kind, approved, err)
				}
				why, err := store.ConsistentMarkOverruled(domain.RoleProductManager, created, moment())
				if err != nil || !strings.Contains(why, "it creates") || !strings.Contains(why, "operator's to approve whatever it is marked") {
					t.Fatalf("ConsistentMarkOverruled() = %q, %v", why, err)
				}
				// The same document already transcribed by hand is confirmed as an
				// amendment of the copy on disk, and is still his.
				if _, err := store.Create(domain.RoleProductManager, created.Draft(), moment()); err != nil {
					t.Fatal(err)
				}
				if _, approved, err := store.PrepareConfirmation(domain.RoleProductManager, created, policy, "confirmed under policy", moment().Add(time.Hour)); err != nil || approved {
					t.Fatalf("a transcribed %s creation marked consistent was approved without the operator: %v, %v", kind, approved, err)
				}
			})
		}
	}
	// A rules document created consistent is still hers: it states no goals.
	store := generatedStore(t.TempDir())
	rules := Write{Action: WriteCreate, ID: "operating-rules", Kind: KindRules, Title: "Operating rules", Directory: productHome, Body: "# Rules\n\n- Notes are append-only.", Reason: "yoyodyne-ifd.433.17 - rules already given", Intent: IntentConsistent}
	if _, approved, err := store.PrepareConfirmation(domain.RoleProductManager, rules, Policy{Goals: domain.ApprovalHuman}, "", moment()); err != nil || !approved {
		t.Fatalf("a rules document created consistent: %v, %v", approved, err)
	}
	if why, err := store.ConsistentMarkOverruled(domain.RoleProductManager, rules, moment()); err != nil || why != "" {
		t.Fatalf("ConsistentMarkOverruled() = %q, %v", why, err)
	}
}

// A revision of the goals or the brief marked consistent that adds or removes a
// goal waits for the operator and names the goal; one that only rewords a goal
// is still approved as the Lead Product Manager's without him.
func TestAConsistentRevisionThatAddsOrRemovesAGoalWaitsForTheOperator(t *testing.T) {
	for _, kind := range []Kind{KindGoals, KindBrief} {
		t.Run(string(kind), func(t *testing.T) {
			store := generatedStore(t.TempDir())
			if _, err := store.Create(domain.RoleProductManager, Draft{ID: "the-goals", Kind: kind, Title: "Goals", Directory: productHome, Body: twoGoals, Reason: "created by owner"}, moment()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Approve("the-goals", "approved by the operator in conversation", moment().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			policy := Policy{Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, SpecificationsHome: productHome}
			revise := func(body, section string) Write {
				return Write{Action: WriteRevise, ID: "the-goals", Body: body, Section: section, Reason: "yoyodyne-ifd.500 - consistent wording", Intent: IntentConsistent}
			}
			for name, test := range map[string]struct {
				write Write
				names string
			}{
				"a goal added":                {revise(strings.Replace(twoGoals, "## Non-goals", "- Ship on Fridays.\n\n## Non-goals", 1), ""), `adds the goal "Ship on Fridays."`},
				"a goal removed":              {revise(strings.Replace(twoGoals, "- [spend-follows-the-work] Spend follows the work.\n  *Supports: the operator can see.*\n", "", 1), ""), `removes the goal "[spend-follows-the-work] Spend follows the work."`},
				"a goal added in its section": {revise("- Keep the boundary where it is.\n- Ship on Fridays.\n- [spend-follows-the-work] Spend follows the work.", "Goals"), `adds the goal "Ship on Fridays."`},
			} {
				if _, approved, err := store.PrepareConfirmation(domain.RoleProductManager, test.write, policy, "confirmed under policy", moment().Add(time.Hour)); err != nil || approved {
					t.Fatalf("%s: approved without the operator: %v, %v", name, approved, err)
				}
				why, err := store.ConsistentMarkOverruled(domain.RoleProductManager, test.write, moment().Add(time.Hour))
				if err != nil || !strings.Contains(why, test.names) || !strings.Contains(why, "operator's to approve whatever it is marked") {
					t.Fatalf("%s: ConsistentMarkOverruled() = %q, %v", name, why, err)
				}
			}
			reworded := revise(strings.Replace(twoGoals, "and say so", "and say plainly", 1), "")
			candidate, approved, err := store.PrepareConfirmation(domain.RoleProductManager, reworded, policy, "confirmed under policy", moment().Add(time.Hour))
			if err != nil || !approved || !candidate.ProductManagerApproved() {
				t.Fatalf("a consistent rewording of a goal: %v, %v, %+v", approved, err, candidate.Artifact.Approvals)
			}
			if why, err := store.ConsistentMarkOverruled(domain.RoleProductManager, reworded, moment().Add(time.Hour)); err != nil || why != "" {
				t.Fatalf("a rewording: ConsistentMarkOverruled() = %q, %v", why, err)
			}
			// It lands approved as it stands, the operator's approval standing
			// through the rewording.
			if err := os.WriteFile(filepath.Join(store.RepositoryRoot, candidate.Artifact.Path), []byte(candidate.Content), 0o644); err != nil {
				t.Fatal(err)
			}
			set, err := store.Load()
			if err != nil || len(set.Problems) != 0 {
				t.Fatalf("Load() error = %v, problems = %v", err, set.Problems)
			}
			if landed, _ := set.Find("the-goals"); landed.ApprovalState() != ApprovalApproved {
				t.Fatalf("landed state = %q", landed.ApprovalState())
			}
		})
	}
}

// A consistent rewording held back only because its record is short — its
// reason names no work item — is the Lead Product Manager's to put right, not
// the operator's to decide, so it is not offered as a reason the operator must
// be asked.
func TestARecordShortOfADirectingItemIsNotTheOperatorsToDecide(t *testing.T) {
	store := generatedStore(t.TempDir())
	if _, err := store.Create(domain.RoleProductManager, Draft{ID: "the-goals", Kind: KindGoals, Title: "Goals", Directory: productHome, Body: twoGoals, Reason: "created by owner"}, moment()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve("the-goals", "approved by the operator in conversation", moment().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	reworded := Write{Action: WriteRevise, ID: "the-goals", Body: strings.Replace(twoGoals, "and say so", "and say plainly", 1), Reason: "reworded for clarity", Intent: IntentConsistent}
	if _, approved, err := store.PrepareConfirmation(domain.RoleProductManager, reworded, Policy{Goals: domain.ApprovalHuman, SpecificationsHome: productHome}, "confirmed under policy", moment().Add(time.Hour)); err != nil || approved {
		t.Fatalf("a rewording naming no work item was approved as hers: %v, %v", approved, err)
	}
	if why, err := store.ConsistentMarkOverruled(domain.RoleProductManager, reworded, moment().Add(time.Hour)); err != nil || why != "" {
		t.Fatalf("ConsistentMarkOverruled() = %q, %v, want nothing for a gap in her own record", why, err)
	}
	// The same rewording that also adds a goal is the operator's however its
	// record reads.
	widened := reworded
	widened.Body = strings.Replace(reworded.Body, "## Non-goals", "- Ship on Fridays.\n\n## Non-goals", 1)
	if why, err := store.ConsistentMarkOverruled(domain.RoleProductManager, widened, moment().Add(time.Hour)); err != nil || !strings.Contains(why, `adds the goal "Ship on Fridays."`) {
		t.Fatalf("ConsistentMarkOverruled() = %q, %v", why, err)
	}
}
