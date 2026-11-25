package maystart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	product    = "yoyodyne"
	openEpicID = "yoyodyne-9k1y"
	member     = "yoyodyne-9k1y.2"
	endedEpic  = "yoyodyne-7abc"
	endedItem  = "yoyodyne-7abc.1"
	outside    = "yoyodyne-9k1y.99" // below the open epic by name, and not on its list
)

var at = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

func operatorAt(t *testing.T, root string) *epicscope.Operator {
	t.Helper()
	operator, err := epicscope.ForOperator(root, product)
	if err != nil {
		t.Fatal(err)
	}
	return operator
}

func must(t *testing.T, _ epicscope.Record, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func name(t *testing.T, root, epic, item string) {
	t.Helper()
	started, err := operatorAt(t, root).Start(epicscope.Naming{Epic: epic, From: "terminal", Members: []string{item}}, at)
	must(t, started.Record, err)
}

func end(t *testing.T, root, epic string) {
	t.Helper()
	record, err := operatorAt(t, root).End(epic, "", at)
	must(t, record, err)
}

// The states of the record the rules are driven against.
func never(*testing.T, string) {}

// on has one open epic and one that has ended.
func on(t *testing.T, root string) {
	name(t, root, endedEpic, endedItem)
	end(t, root, endedEpic)
	name(t, root, openEpicID, member)
}

func off(t *testing.T, root string) {
	on(t, root)
	record, err := operatorAt(t, root).TurnOff()
	must(t, record, err)
}

func allEnded(t *testing.T, root string) {
	on(t, root)
	end(t, root, openEpicID)
}

func unreadable(t *testing.T, root string) {
	on(t, root)
	// The only file beside the lock in the product's directory is the record.
	found, err := filepath.Glob(filepath.Join(home.ProductDirectory(root, product), "*.json"))
	if err != nil || len(found) != 1 {
		t.Fatalf("record files = %v, %v; want one", found, err)
	}
	if err := os.WriteFile(found[0], []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEveryRule(t *testing.T) {
	t.Parallel()
	type answers struct{ on, allEnded Answer }
	states := []struct {
		name  string
		setUp func(*testing.T, string)
		want  func(answers, bool) Answer
	}{
		{"no file", never, func(answers, bool) Answer { return May }},
		{"turned off", off, func(answers, bool) Answer { return May }},
		{"on", on, func(a answers, _ bool) Answer { return a.on }},
		{"every epic ended", allEnded, func(a answers, _ bool) Answer { return a.allEnded }},
		{"file cannot be read", unreadable, func(_ answers, always bool) Answer {
			if always {
				return May
			}
			return CouldNotTell
		}},
	}

	type driven struct {
		name   string
		start  Start
		always bool // may start whatever the record says
		want   answers
	}
	in := answers{May, MayNot}
	out := answers{MayNot, MayNot}
	unknown := answers{CouldNotTell, CouldNotTell}
	cases := []driven{
		{"unmet goal review", GoalReview(), false, out},
		{"message reading", MessageReading(), false, out},
		{"team branch update", UpdateFromTeam("staging"), false, in},
		{"team branch update of no branch", UpdateFromTeam(""), false, unknown},
		{"operator's conversation", OperatorConversation(), true, answers{May, May}},
		{"role's question", RoleQuestion(), true, answers{May, May}},
		{"nothing said", Start{}, false, unknown},
		{"run by an unknown path", Run("elsewhere", member), false, unknown},
		{"document by an unknown path", DocumentRun("elsewhere", member), false, unknown},
		{"pass by an unknown path", Pass("elsewhere", domain.RoleArchitect), false, unknown},
	}
	for _, entry := range Paths() {
		path := string(entry.Path) + ": "
		runs, documents, passes := unknown, unknown, unknown
		documentsOut, runsOut := unknown, unknown
		switch entry.starts {
		case startsRun:
			runs, runsOut = in, out
			documents, documentsOut = in, out
		case startsDocument:
			documents, documentsOut = in, out
		case startsPass:
			passes = in
		case startsGoalReview, startsMessageReading, startsTeamBranchUpdate, startsByAPerson:
		default:
			t.Fatalf("%s starts nothing this test knows", entry.Path)
		}
		cases = append(cases,
			driven{path + "run of a member", Run(entry.Path, member), false, runs},
			driven{path + "run of an item outside", Run(entry.Path, outside), false, runsOut},
			driven{path + "run of an ended epic's member", Run(entry.Path, endedItem), false, runsOut},
			driven{path + "run of the epic itself", Run(entry.Path, openEpicID), false, runsOut},
			driven{path + "run of no item", Run(entry.Path, ""), false, unknown},

			driven{path + "document for a member among others", DocumentRun(entry.Path, outside, member), false, documents},
			driven{path + "document for the open epic", DocumentRun(entry.Path, openEpicID), false, documents},
			driven{path + "document for an item outside", DocumentRun(entry.Path, outside), false, documentsOut},
			driven{path + "document for an ended epic", DocumentRun(entry.Path, endedEpic, endedItem), false, documentsOut},
			driven{path + "document for nothing", DocumentRun(entry.Path), false, documentsOut},

			driven{path + "Lead Product Manager's pass", Pass(entry.Path, domain.RoleProductManager), false, passes},
			driven{path + "development manager's pass", Pass(entry.Path, domain.RoleDevelopmentManager), false, passes},
			driven{path + "architect's pass", Pass(entry.Path, domain.RoleArchitect), false, passes},
			driven{path + "pass of a role with no rule", Pass(entry.Path, domain.RoleDeveloper), false, unknown},
			driven{path + "pass of no role", Pass(entry.Path, ""), false, unknown},
		)
		programManagers := unknown
		if entry.starts == startsPass {
			programManagers = out
		}
		cases = append(cases, driven{path + "program manager's pass", Pass(entry.Path, domain.RoleProgramManager), false, programManagers})
	}

	for _, state := range states {
		root := t.TempDir()
		state.setUp(t, root)
		for _, test := range cases {
			got := Decide(root, product, test.start)
			want := state.want(test.want, test.always)
			if got.Answer != want {
				t.Errorf("%s, %s: %+v, want %s", state.name, test.name, got, want)
			}
			if got.MayStart() != (want == May) {
				t.Errorf("%s, %s: MayStart() = %t with answer %s", state.name, test.name, got.MayStart(), got.Answer)
			}
			if (got.Why == "") != (got.Answer == May) {
				t.Errorf("%s, %s: a reason belongs to every answer but May, got %+v", state.name, test.name, got)
			}
		}
	}
}

func TestAnItemTheOperatorAddsMayThenStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	on(t, root)
	start := Run(ReadyWork, outside)
	if got := Decide(root, product, start); got.Answer != MayNot || !strings.Contains(got.Why, outside) {
		t.Fatalf("before it is added: %+v, want a refusal naming it", got)
	}
	record, err := operatorAt(t, root).Add(openEpicID, outside, at)
	must(t, record, err)
	if got := Decide(root, product, start); !got.MayStart() {
		t.Fatalf("after it is added: %+v", got)
	}
}

func TestARecordThatCannotBeReadSaysWhy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	unreadable(t, root)
	_, readErr := epicscope.Read(root, product)
	if readErr == nil {
		t.Fatal("the record was read")
	}
	if got := Decide(root, product, Run(ReadyWork, member)); !strings.Contains(got.Why, readErr.Error()) {
		t.Fatalf("Why = %q, want it to carry %q", got.Why, readErr)
	}
	// What cannot even be looked for starts nothing either.
	if got := Decide("not/absolute", product, Run(ReadyWork, member)); got.Answer != CouldNotTell {
		t.Fatalf("a state root that is no place: %+v", got)
	}
}

func TestADecisionNobodyMadeStartsNothing(t *testing.T) {
	t.Parallel()
	if (Decision{}).MayStart() {
		t.Fatal("an empty decision starts something")
	}
}

// The list is written out again here so that a path dropped or added shows as a
// change to the list, and one marked wired as a change to the code that asks.
func TestTheClosedListOfPaths(t *testing.T) {
	t.Parallel()
	want := []Path{
		ReadyWork, NamedRun, HoldTest, DecidedRerun, DecidedRepair, FinishMerge, ContinueRun, PickUpRun,
		FeedbackRepair, PublishDocument, BringUpToDate, ResolveConflict, BringInFile,
		UpdateQueued, UpdateForPerson, AfterLogin, RecurringPass, PromptedPass, UnmetGoalReview, MessagesForWork,
		TeamBranchUpdate, OperatorReview,
	}
	wired := map[Path]bool{
		ReadyWork: true, NamedRun: true, HoldTest: true, DecidedRerun: true, DecidedRepair: true, FeedbackRepair: true,
		ResolveConflict: true, BringInFile: true, PublishDocument: true,
		UpdateQueued: true, UpdateForPerson: true, AfterLogin: true,
		BringUpToDate: true,
		RecurringPass: true, PromptedPass: true, UnmetGoalReview: true, MessagesForWork: true,
		FinishMerge: true, ContinueRun: true, PickUpRun: true,
		TeamBranchUpdate: true, OperatorReview: true,
	}

	got := Paths()
	if len(got) != len(want) {
		t.Fatalf("%d paths, want %d", len(got), len(want))
	}
	for i, entry := range got {
		if entry.Path != want[i] {
			t.Errorf("path %d is %q, want %q", i, entry.Path, want[i])
		}
		if entry.Says == "" {
			t.Errorf("%s has no words for a person", entry.Path)
		}
		if entry.Wired != wired[entry.Path] {
			t.Errorf("%s is marked wired %t, want %t", entry.Path, entry.Wired, wired[entry.Path])
		}
	}
	got[0].Wired = !wired[want[0]]
	if Paths()[0].Wired != wired[want[0]] {
		t.Error("a caller can change the list")
	}
}
