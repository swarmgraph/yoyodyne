package maystart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	otherEpic, otherItem = "yoyodyne-5def", "yoyodyne-5def.1"
	piece                = "yoyodyne-piece"
	progressFile         = "epic-progress.json"
	begunFile            = "epic-progress-begun.json"
)

// joined has two open epics, the first with a piece that joined it.
func joined(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	name(t, root, openEpicID, member)
	name(t, root, otherEpic, otherItem)
	store, err := epicprogress.Open(root, product)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Join(openEpicID, epicprogress.Joined{WorkItemID: piece, From: member, Judgment: "inside", Session: "session-1", At: at}); err != nil {
		t.Fatal(err)
	}
	return root
}

func fileOf(root, file string) string {
	return filepath.Join(home.ProductDirectory(root, product), file)
}

func put(t *testing.T, root, file, content string) {
	t.Helper()
	if err := os.WriteFile(fileOf(root, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func wantAnswer(t *testing.T, root string, start Start, want Answer, says ...string) Decision {
	t.Helper()
	decision := Decide(root, product, start)
	if decision.Answer != want {
		t.Errorf("the answer is %+v, want %s", decision, want)
	}
	for _, text := range says {
		if !strings.Contains(decision.Why+" "+decision.Clears, text) {
			t.Errorf("%+v does not say %q", decision, text)
		}
	}
	return decision
}

func TestAPieceThatJoinedIsAMember(t *testing.T) {
	t.Parallel()
	root := joined(t)
	wantAnswer(t, root, Run(ReadyWork, piece), May)
	wantAnswer(t, root, DocumentRun(PublishDocument, piece), May)
	end(t, root, openEpicID)
	wantAnswer(t, root, Run(ReadyWork, piece), MayNot)
}

// A file that is there and not whole stops every start that asks, and is not
// read while epic mode is off.
func TestEitherFileNotWholeMeansTheDecisionCannotBeMade(t *testing.T) {
	t.Parallel()
	for _, file := range []string{progressFile, begunFile} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			root := joined(t)
			put(t, root, file, "{")
			for _, start := range []Start{Run(ReadyWork, member), Run(ReadyWork, otherItem), Run(ReadyWork, outside), Pass(RecurringPass, "architect"), DocumentRun(PublishDocument, otherItem), UpdateFromTeam("staging"), GoalReview()} {
				wantAnswer(t, root, start, CouldNotTell, file)
			}
			wantAnswer(t, root, OperatorConversation(), May)
			record, err := operatorAt(t, root).TurnOff()
			must(t, record, err)
			wantAnswer(t, root, Run(ReadyWork, outside), May)
		})
	}
}

func TestAnUnreadableEpicStartsNothingOfItsOwnAndOthersCarryOn(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{progressFile, begunFile} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			root := joined(t)
			if err := os.Remove(fileOf(root, missing)); err != nil {
				t.Fatal(err)
			}
			for _, start := range []Start{Run(ReadyWork, member), DocumentRun(PublishDocument, member), DocumentRun(PublishDocument, openEpicID)} {
				wantAnswer(t, root, start, CouldNotTell, openEpicID, "`yoyo epic end "+openEpicID+"`")
			}
			for _, start := range []Start{Run(ReadyWork, otherItem), DocumentRun(PublishDocument, member, otherItem), Pass(RecurringPass, "architect"), UpdateFromTeam("staging")} {
				wantAnswer(t, root, start, May)
			}
			end(t, root, otherEpic)
			for _, start := range []Start{Pass(RecurringPass, "architect"), UpdateFromTeam("staging")} {
				wantAnswer(t, root, start, CouldNotTell, openEpicID)
			}
			// Ending it and naming it again mends it.
			end(t, root, openEpicID)
			started, err := operatorAt(t, root).Start(epicscope.Naming{Epic: openEpicID, From: "terminal", Members: []string{member}}, at.Add(1))
			must(t, started.Record, err)
			wantAnswer(t, root, Run(ReadyWork, member), May)
		})
	}
}

// Both files missing reads as not begun: the piece that joined is outside again.
func TestBothFilesMissingReadsAsNotBegun(t *testing.T) {
	t.Parallel()
	root := joined(t)
	for _, file := range []string{progressFile, begunFile} {
		if err := os.Remove(fileOf(root, file)); err != nil {
			t.Fatal(err)
		}
	}
	wantAnswer(t, root, Run(ReadyWork, member), May)
	wantAnswer(t, root, Run(ReadyWork, piece), MayNot)
	if found, err := filepath.Glob(fileOf(root, "epic-progress*.json")); err != nil || len(found) != 0 {
		t.Fatalf("asking wrote %v, %v", found, err)
	}
}

func TestAnItemInTwoOpenEpicsDoesNotStart(t *testing.T) {
	t.Parallel()
	root := joined(t)
	// The operator's commands refuse both; a hand changes the record.
	content, err := os.ReadFile(fileOf(root, "epic-scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "epic-scope.json", strings.Replace(string(content), `"`+otherItem+`"`, `"`+piece+`"`, 1))
	wantAnswer(t, root, Run(ReadyWork, piece), MayNot, openEpicID, otherEpic, "`yoyo epic end "+otherEpic+"`")
	wantAnswer(t, root, DocumentRun(PublishDocument, piece), MayNot, openEpicID, otherEpic)
	wantAnswer(t, root, Run(ReadyWork, member), May)
	end(t, root, otherEpic)
	wantAnswer(t, root, Run(ReadyWork, piece), May)
}
