package maystart

import (
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epiccost"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
)

// spent is a cost reader that says the epic has spent the amount.
func spent(amount float64) costReader {
	return func(_ string, _ domain.ProductID, _ epicprogress.View, epic epicscope.Epic) (epiccost.Standing, error) {
		return epiccost.Standing{Spend: epiccost.Spend{KnownUSD: amount, Runs: 1}, Budget: epic.Budget}, nil
	}
}

func budgeted(t *testing.T, limit float64) string {
	t.Helper()
	root := t.TempDir()
	name(t, root, openEpicID, member)
	record, err := operatorAt(t, root).SetBudget(openEpicID, limit, at)
	must(t, record, err)
	return root
}

// stillGoesOn is each way a run already under way takes its next step: its
// repairs, its review and its landing.
var stillGoesOn = []Path{DecidedRepair, FinishMerge, ContinueRun, PickUpRun, FeedbackRepair, BringUpToDate, ResolveConflict, UpdateQueued, UpdateForPerson, AfterLogin}

func TestPastItsBudgetAnEpicStartsNoMemberAndWorkUnderWayGoesOn(t *testing.T) {
	t.Parallel()
	root := budgeted(t, 10)
	for _, path := range []Path{ReadyWork, NamedRun, HoldTest, DecidedRerun} {
		decision := decideWith(root, product, Run(path, member), spent(10))
		if decision.Answer != MayNot || !decision.ByBudget {
			t.Errorf("a new start by %s at the limit is %+v, want refused by the budget", path, decision)
		}
		for _, says := range []string{"spent $10.00 of its limit of $10.00 in provider reported cost", "`yoyo epic budget " + openEpicID + " <dollars>`"} {
			if !strings.Contains(decision.Why+" "+decision.Clears, says) {
				t.Errorf("%+v does not say %q", decision, says)
			}
		}
		if below := decideWith(root, product, Run(path, member), spent(9.99)); !below.MayStart() {
			t.Errorf("a new start by %s below the limit is %+v", path, below)
		}
	}
	for _, path := range stillGoesOn {
		if decision := decideWith(root, product, Run(path, member), spent(25)); !decision.MayStart() {
			t.Errorf("a run under way taking its next step by %s, past the limit, is %+v", path, decision)
		}
	}
	// Nothing else the decision answers is changed by the budget.
	for _, start := range []Start{Pass(PromptedPass, domain.RoleArchitect), JudgmentOfAPiece(named(openEpicID)), DocumentRun(PublishDocument, member)} {
		if decision := decideWith(root, product, start, spent(25)); !decision.MayStart() {
			t.Errorf("%+v past the limit is %+v", start, decision)
		}
	}
}

// Raising the limit lets the next start go ahead.
func TestRaisingTheLimitLetsTheNextStartGo(t *testing.T) {
	t.Parallel()
	root := budgeted(t, 10)
	if decideWith(root, product, Run(ReadyWork, member), spent(12)).MayStart() {
		t.Fatal("a start past the limit went ahead")
	}
	record, err := operatorAt(t, root).SetBudget(openEpicID, 20, at)
	must(t, record, err)
	if decision := decideWith(root, product, Run(ReadyWork, member), spent(12)); !decision.MayStart() {
		t.Fatalf("after the limit was raised the start is %+v", decision)
	}
}

// With no budget set, and with epic mode off, the cost is never read.
func TestWithNoBudgetTheCostIsNeverRead(t *testing.T) {
	t.Parallel()
	unread := func(string, domain.ProductID, epicprogress.View, epicscope.Epic) (epiccost.Standing, error) {
		t.Fatal("the cost was read")
		return epiccost.Standing{}, nil
	}
	root := t.TempDir()
	name(t, root, openEpicID, member)
	if decision := decideWith(root, product, Run(ReadyWork, member), unread); !decision.MayStart() {
		t.Fatalf("a member with no budget is %+v", decision)
	}
	root = budgeted(t, 1)
	record, err := operatorAt(t, root).TurnOff()
	must(t, record, err)
	if decision := decideWith(root, product, Run(ReadyWork, member), unread); !decision.MayStart() {
		t.Fatalf("with epic mode off a member is %+v", decision)
	}
}

// A cost that cannot be read beside a budget starts nothing new.
func TestACostThatCannotBeReadStartsNothingNew(t *testing.T) {
	t.Parallel()
	root := budgeted(t, 10)
	broken := func(string, domain.ProductID, epicprogress.View, epicscope.Epic) (epiccost.Standing, error) {
		return epiccost.Standing{}, errors.New("the run records cannot be read")
	}
	if decision := decideWith(root, product, Run(ReadyWork, member), broken); decision.Answer != CouldNotTell {
		t.Fatalf("the start is %+v, want could not tell", decision)
	}
	if decision := decideWith(root, product, Run(PickUpRun, member), broken); !decision.MayStart() {
		t.Fatalf("a run under way is %+v", decision)
	}
}

// A refusal by the budget is not kept among the starts refused for being outside
// the named epics: the epic's own line in status says it.
func TestARefusalByTheBudgetIsNotKeptAsARefusedStart(t *testing.T) {
	t.Parallel()
	root := budgeted(t, 10)
	decision := decideWith(root, product, Run(ReadyWork, member), spent(10))
	if err := Note(root, product, Run(ReadyWork, member), decision, at); err != nil {
		t.Fatal(err)
	}
	refusals, err := ReadRefusals(root, product)
	if err != nil {
		t.Fatal(err)
	}
	if len(refusals.Items) != 0 {
		t.Fatalf("the budget's refusal was kept: %+v", refusals.Items)
	}
}

// Decide reads the real cost: an epic with no runs has spent nothing.
func TestDecideReadsWhatTheEpicSpent(t *testing.T) {
	t.Parallel()
	root := budgeted(t, 0.01)
	wantAnswer(t, root, Run(ReadyWork, member), May)
}
