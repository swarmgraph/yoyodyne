package readmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	stateEpic  = "yoyodyne-ep"
	stateOne   = "yoyodyne-ep.1"
	stateTwo   = "yoyodyne-ep.2"
	statePiece = "yoyodyne-piece"
	stateOther = "yoyodyne-other"
)

// hours is a time after the epic of a stateBench was named.
func hours(after int) time.Time { return afterKept.Add(time.Duration(after) * time.Hour) }

// stateBench is the records of one named epic with two members.
type stateBench struct {
	t        *testing.T
	root     string
	items    map[string]beads.WorkItem
	ready    []string
	inFlight []runstate.State
	runs     map[string][]runstate.State
	entry    epicprogress.Entry
	budget   string
	sources  Sources
}

type budgetedRecords struct {
	EpicRecords
	why string
}

func (b budgetedRecords) RefusesStart(epicprogress.Naming) (string, error) { return b.why, nil }

func newStateBench(t *testing.T) *stateBench {
	t.Helper()
	b := &stateBench{t: t, root: t.TempDir(), items: map[string]beads.WorkItem{}, runs: map[string][]runstate.State{}, sources: quietSources()}
	b.sources.Gates = fakeGates{}
	b.sources.Conversations = loggedConversations{fakeConversations: fakeConversations{recorded: []runstate.Conversation{{ConversationID: "chat-1", Agent: "product-manager", Role: domain.RoleProductManager}}}}
	started, err := operatorOf(t, b.root).Start(epicscope.Naming{Epic: stateEpic, From: "terminal", Members: []string{stateOne, stateTwo}}, afterKept)
	b.must(err)
	b.entry = epicprogress.Entry{Naming: epicprogress.Of(started.Epic), BegunAt: afterKept}
	for _, id := range []string{stateEpic, stateOne, stateTwo, statePiece, stateOther} {
		b.items[id] = beads.WorkItem{ID: id, Title: "Title " + id, Status: "open"}
	}
	return b
}

func (b *stateBench) must(err error) {
	b.t.Helper()
	if err != nil {
		b.t.Fatal(err)
	}
}

func (b *stateBench) set(id string, change func(*beads.WorkItem)) {
	item := b.items[id]
	change(&item)
	b.items[id] = item
}

func (b *stateBench) status(id, status string) {
	b.set(id, func(item *beads.WorkItem) { item.Status = status })
}

// run records a run of an item that ended, as the change given left it.
func (b *stateBench) run(id string, started int, change func(*runstate.State)) {
	run := runstate.State{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusSucceeded, StartedAt: hours(started), ReviewDecision: runstate.ReviewApprove}
	change(&run)
	b.runs[id] = append(b.runs[id], run)
}

func (b *stateBench) landed(ids ...string) {
	for _, id := range ids {
		b.status(id, "closed")
		b.run(id, 1, func(run *runstate.State) { run.Integration = &runstate.Integration{} })
	}
}

// handedOver is an approved change on an open pull request a person merges.
func (b *stateBench) handedOver(id string) {
	b.status(id, "in_progress")
	b.run(id, 1, func(run *runstate.State) {
		run.PullRequest, run.PersonMerge = &runstate.PullRequest{Number: 7}, &runstate.PersonMerge{Number: 7, Since: hours(1)}
	})
}

func (b *stateBench) lastRun(id string, change func(*runstate.State)) {
	change(&b.runs[id][len(b.runs[id])-1])
}

func (b *stateBench) closedUnmerged(id string, at time.Time) {
	b.set(id, func(item *beads.WorkItem) {
		item.Status, item.Parking = "open", domain.WorkItemParking("closed by a person")
	})
	b.lastRun(id, func(run *runstate.State) { run.PersonMerge.ClosedAt, run.PersonMerge.ReturnedAt = &at, &at })
}

func (b *stateBench) check(found string, at time.Time) {
	b.entry.Checks = append(b.entry.Checks, epicprogress.Check{ID: "check-" + at.String(), Commit: "abc", Found: found, At: at})
}

func (b *stateBench) deliver(at time.Time) {
	b.check(epicprogress.FoundAllMet, at)
	b.entry.Deliveries = append(b.entry.Deliveries, epicprogress.Delivery{At: at, Evidence: "check"})
}

func (b *stateBench) end(at time.Time) {
	b.t.Helper()
	_, err := operatorOf(b.t, b.root).End(stateEpic, "", at)
	b.must(err)
}

// ask is a question between roles that is open.
func (b *stateBench) ask(question string, opened time.Time) {
	listed, _ := b.sources.Exchanges.(fakeExchanges)
	listed.exchanges = append(listed.exchanges, exchange.Exchange{ID: "exchange-" + question, Asker: exchange.Party{Role: domain.RoleDevelopmentManager},
		Answerer: exchange.Party{Role: domain.RoleArchitect}, Question: question, OpenedAt: opened})
	b.sources.Exchanges = listed
}

// loggedConversations is one conversation's saved record and its event log.
type loggedConversations struct {
	fakeConversations
	events  []execution.Event
	skipped []runstate.SkippedLine
}

func (l loggedConversations) LoggedConversations() ([]string, error) { return []string{"chat-1"}, nil }

func (l loggedConversations) EventsOfType(string, ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error) {
	return l.events, l.skipped, nil
}

// askOperator is a question to the operator, and when it was answered, if it was.
func (b *stateBench) askOperator(about string, raised time.Time, answered ...time.Time) {
	asked := b.sources.Conversations.(loggedConversations)
	id := fmt.Sprintf("c%d.1", len(asked.events)+1)
	payload, err := json.Marshal(map[string]any{"id": id, "concern": map[string]string{"subject": about, "detail": "seen", "question": "Is it right?"}})
	b.must(err)
	asked.events = append(asked.events, execution.Event{Type: execution.EventConcernRaised, Timestamp: raised, Payload: payload})
	for _, at := range answered {
		asked.events = append(asked.events, execution.Event{Type: execution.EventConcernAnswered, Timestamp: at, Payload: payload})
	}
	saved := &asked.recorded[0]
	if len(answered) == 0 {
		saved.PendingConcerns = append(saved.PendingConcerns, runstate.PendingConcern{ID: id, Kind: "judgement", Subject: about, Detail: "seen", Question: "Is it right?"})
	}
	b.sources.Conversations = asked
}

func (b *stateBench) join(id, from string, at time.Time) {
	b.entry.Joined = append(b.entry.Joined, epicprogress.Joined{WorkItemID: id, From: from, Judgment: "inside", Session: "session-1", At: at})
}

// remove takes an item out at a moment.
func (b *stateBench) remove(id string, at time.Time) {
	b.t.Helper()
	b.write()
	_, err := operatorOf(b.t, b.root).Knowing(epicprogress.Holding(b.root, "yoyodyne")).Remove(stateEpic, id, "not needed", at)
	b.must(err)
}

func (b *stateBench) write() {
	b.t.Helper()
	for name, content := range map[string]any{
		"epic-progress.json":       epicprogress.Progress{SchemaVersion: 1, ProductID: "yoyodyne", Entries: []epicprogress.Entry{b.entry}},
		"epic-progress-begun.json": epicprogress.Begun{SchemaVersion: 1, ProductID: "yoyodyne", Namings: []epicprogress.Naming{b.entry.Naming}},
	} {
		encoded, err := json.Marshal(content)
		b.must(err)
		b.must(os.WriteFile(fileOf(b.root, name), encoded, 0o600))
	}
}

func (b *stateBench) read() *Epics {
	b.t.Helper()
	b.write()
	tracker := statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{}}}
	for _, id := range []string{stateEpic, stateOne, stateTwo, statePiece, stateOther} {
		item := b.items[id]
		tracker.byStatus[item.Status] = append(tracker.byStatus[item.Status], item)
		if slices.Contains(b.ready, id) {
			tracker.ready = append(tracker.ready, item)
		}
	}
	b.sources.Tracker = tracker
	b.sources.Runs = runsOfItems{fakeRuns{incomplete: b.inFlight, recorded: slices.Concat(b.inFlight)}, b.runs}
	b.sources.Epics = budgetedRecords{NewEpicRecords(b.root, "yoyodyne"), b.budget}
	return ReadStanding(context.Background(), b.sources).Epics
}

// wants reads the state of the epic, and what is said of it.
func (b *stateBench) wants(state EpicState, says ...string) EpicStanding {
	b.t.Helper()
	epics := b.read()
	var standing EpicStanding
	if len(epics.Open) == 1 {
		standing = epics.Open[0].EpicStanding
	} else {
		standing = epics.Ended[0].EpicStanding
	}
	if standing.State != state {
		b.t.Errorf("the state is %q (%s), want %q; it says\n%s", standing.State, standing.Problem, state, standing.render())
	}
	wantSaid(b.t, standing.render(), says...)
	return standing
}

// Each state with what makes it, and the first that fits where several would.
func TestEachStateOfANamedEpic(t *testing.T) {
	t.Parallel()
	delivered := func(b *stateBench) { b.landed(stateOne, stateTwo); b.check(epicprogress.FoundAllMet, hours(2)) }
	waitsOutside := func(b *stateBench, id string) {
		b.set(id, func(item *beads.WorkItem) {
			item.Dependencies = []beads.Dependency{{ID: stateOther, Type: beads.BlocksDependency}}
		})
	}
	personsStep := func(b *stateBench, id string) {
		b.set(id, func(item *beads.WorkItem) { item.Description = "human-gate: looked-at — the operator has looked\n" })
	}
	for name, test := range map[string]struct {
		given func(*stateBench)
		want  EpicState
		says  []string
	}{
		"cancelled": {func(b *stateBench) {
			b.end(hours(3))
		}, EpicCancelled, []string{"Cancelled: you ended it before"}},
		"ended after it was delivered": {func(b *stateBench) {
			b.landed(stateOne, stateTwo)
			b.deliver(hours(2))
			b.end(hours(3))
		}, EpicIntegrated, nil},
		"delivered only after it was ended": {func(b *stateBench) {
			b.landed(stateOne, stateTwo)
			b.deliver(hours(2))
			b.end(hours(1))
		}, EpicCancelled, nil},
		"integrated": {delivered, EpicIntegrated, []string{"Delivered, integrated"}},
		"integrated, by a summary": {func(b *stateBench) {
			b.landed(stateOne)
			b.status(stateTwo, "closed")
			b.run(stateTwo, 1, func(*runstate.State) {})
			b.check(epicprogress.FoundAllMet, hours(2))
		}, EpicIntegrated, nil},
		"handed over": {func(b *stateBench) {
			b.landed(stateOne)
			b.handedOver(stateTwo)
			b.check(epicprogress.FoundAllMet, hours(2))
		}, EpicHandedOver, []string{"Delivered, handed over"}},
		"working, running before anything outside": {func(b *stateBench) {
			b.inFlight = []runstate.State{{RunID: "run-1", WorkItemID: stateOne, Status: runstate.StatusRunning, StartedAt: hours(1)}}
			waitsOutside(b, stateTwo)
		}, EpicWorking, []string{"waits for Title " + stateOther}},
		"working, ready before the operator": {func(b *stateBench) {
			b.ready = []string{stateOne}
			personsStep(b, stateTwo)
		}, EpicWorking, []string{"Waiting on the operator: Title " + stateTwo}},
		"working, a decision owed, past its budget": {func(b *stateBench) {
			b.budget, b.ready = "spent $10 of $10", []string{stateOne}
			b.set(stateTwo, func(item *beads.WorkItem) { item.Parking = "later" })
		}, EpicWorking, []string{"Waiting on the Lead Product Manager: Title " + stateTwo}},
		"working, a design owed": {func(b *stateBench) {
			b.landed(stateOne)
			b.set(stateTwo, func(item *beads.WorkItem) { item.Labels = []string{domain.NeedsDesignLabel} })
		}, EpicWorking, []string{"Waiting on the architect: Title " + stateTwo}},
		"working, the check owed": {func(b *stateBench) { b.landed(stateOne, stateTwo) }, EpicWorking,
			[]string{"Waiting on the reviewer: every member is done and the check of the whole epic is owed; that check is not built yet"}},
		"working, a correction owed, whatever the budget": {func(b *stateBench) {
			b.budget = "spent"
			b.landed(stateOne, stateTwo)
			b.check(epicprogress.FoundNotMet, hours(2))
		}, EpicWorking, []string{"Waiting on the development manager: write the piece"}},
		"working, evidence owed": {func(b *stateBench) {
			b.landed(stateOne, stateTwo)
			b.check(epicprogress.FoundCannotTell, hours(2))
		}, EpicWorking, []string{"Waiting on the Lead Product Manager: say what evidence"}},
		"working, a third failed check": {func(b *stateBench) {
			b.landed(stateOne, stateTwo)
			for at := 2; at < 5; at++ {
				b.check(epicprogress.FoundNotMet, hours(at))
			}
		}, EpicWorking, []string{"Waiting on the Lead Product Manager: the check of the whole epic found something missing a third time"}},
		"out of budget before the operator": {func(b *stateBench) {
			b.budget, b.ready = "spent $10 of $10", []string{stateOne}
			personsStep(b, stateTwo)
		}, EpicOutOfBudget, []string{"Out of budget; the next start is refused by its budget: spent $10 of $10"}},
		"waiting on the operator": {func(b *stateBench) { b.landed(stateOne); personsStep(b, stateTwo) }, EpicWaitsOnOperator,
			[]string{"Waiting on the operator: Title " + stateTwo + " (" + stateTwo + ") needs a step only a person can take"}},
		"waiting on the operator, by the check": {func(b *stateBench) {
			b.landed(stateOne, stateTwo)
			b.check(epicprogress.FoundPersonsStep, hours(2))
		}, EpicWaitsOnOperator, nil},
		"blocked outside, and on the operator": {func(b *stateBench) { personsStep(b, stateOne); waitsOutside(b, stateTwo) }, EpicBlockedOutside,
			[]string{"Waiting on the operator: Title " + stateOne, "Title " + stateTwo + " (" + stateTwo + ") waits for Title " + stateOther}},
		"blocked outside": {func(b *stateBench) { b.landed(stateOne); waitsOutside(b, stateTwo) }, EpicBlockedOutside,
			[]string{"Title " + stateTwo + " (" + stateTwo + ") waits for Title " + stateOther}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newStateBench(t)
			test.given(b)
			b.wants(test.want, test.says...)
		})
	}
}

func TestAnEpicWithACancelledMemberOrAnOpenQuestionIsNeverDelivered(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.check(epicprogress.FoundAllMet, hours(2))
	b.runs[stateTwo] = nil
	b.wants(EpicWorking, "Waiting on the Lead Product Manager: Title "+stateTwo+" ("+stateTwo+") was closed without its change having landed")
	// A piece written in its place, which joined as a split of it, stands for it.
	b.join(statePiece, stateTwo, hours(3))
	b.ready = []string{statePiece}
	if standing := b.wants(EpicWorking); len(standing.Waits) != 0 {
		t.Errorf("waits = %+v, want none but the piece's own start", standing.Waits)
	}
	b.ready = nil
	b.landed(statePiece)
	b.check(epicprogress.FoundAllMet, hours(4))
	b.wants(EpicIntegrated)

	asked := exchange.Exchange{ID: "exchange-1", Asker: exchange.Party{Role: domain.RoleDevelopmentManager}, Answerer: exchange.Party{Role: domain.RoleArchitect}, Question: "Is " + stateOne + "0 enough?"}
	b.sources.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{asked}}
	b.wants(EpicIntegrated)
	asked.Question = "Is " + stateOne + " enough?"
	b.sources.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{asked}}
	b.wants(EpicWorking, "Waiting on the architect: Title "+stateOne+" ("+stateOne+") has a question between roles that is not settled")
}

func TestAMemberIsJudgedByItsSummaryOnlyOnACompleteApprovedRunWithNoChange(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*runstate.State){
		"it did not end complete":      func(run *runstate.State) { run.Status = runstate.StatusFailed },
		"it was not approved":          func(run *runstate.State) { run.ReviewDecision = runstate.ReviewRepair },
		"it was approved as evidence":  func(run *runstate.State) { run.ReviewApproves = runstate.ApprovesEvidence },
		"it recorded a pull request":   func(run *runstate.State) { run.PullRequest = &runstate.PullRequest{Number: 7} },
		"it recorded a change to land": func(run *runstate.State) { run.Changes = &runstate.Changes{Files: "M\tmain.go"} },
	} {
		b := newStateBench(t)
		b.landed(stateOne)
		b.status(stateTwo, "closed")
		b.run(stateTwo, 1, func(*runstate.State) {})
		b.check(epicprogress.FoundAllMet, hours(2))
		b.wants(EpicIntegrated)
		b.lastRun(stateTwo, change)
		t.Log(name)
		b.wants(EpicWorking, "Waiting on the Lead Product Manager: Title "+stateTwo+" ("+stateTwo+") was closed without its change having landed")
	}
}

func TestAQuestionToTheOperatorKeepsAnOpenEpicFromDelivered(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.check(epicprogress.FoundAllMet, hours(2))
	b.askOperator(stateOne+"0", hours(1))
	b.wants(EpicIntegrated)
	b.askOperator("the scope of "+stateOne, hours(1))
	standing := b.wants(EpicWaitsOnOperator, "Waiting on the operator: Title "+stateOne+" ("+stateOne+") has a question with no answer: Is it right?")
	if len(standing.Waits) != 1 || standing.Waits[0].WorkItemID != stateOne || !standing.Waits[0].Owner.IsOperator() {
		t.Errorf("waits = %+v, want the one question to the operator", standing.Waits)
	}
}

func TestAPersonsMergeAloneMovesAnEpicFromHandedOverToIntegrated(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne)
	b.handedOver(stateTwo)
	b.check(epicprogress.FoundAllMet, hours(2))
	b.entry.Deliveries = []epicprogress.Delivery{{At: hours(2), Evidence: "check"}}
	b.wants(EpicHandedOver)
	b.status(stateTwo, "closed")
	b.wants(EpicHandedOver)
	b.lastRun(stateTwo, func(run *runstate.State) {
		merged := hours(9)
		run.PersonMerge.MergedAt, run.Integration = &merged, &runstate.Integration{ThroughPullRequest: true}
	})
	if standing := b.wants(EpicIntegrated); standing.DeliveredBefore != nil {
		t.Error("its delivery no longer counts after the merge")
	}

	closed := newStateBench(t)
	closed.handedOver(stateOne)
	closed.handedOver(stateTwo)
	closed.check(epicprogress.FoundAllMet, hours(2))
	closed.entry.Deliveries = []epicprogress.Delivery{{At: hours(2), Evidence: "check"}}
	closed.closedUnmerged(stateTwo, hours(3))
	closed.wants(EpicWorking, "Waiting on the Lead Product Manager: Title "+stateTwo+" ("+stateTwo+") had its pull request closed unmerged", "It was delivered at "+localMoment(hours(2))+" and opened again since")
	// Landed afterwards, the earlier check no longer counts.
	closed.set(stateTwo, func(item *beads.WorkItem) { item.Parking = "" })
	closed.run(stateTwo, 4, func(run *runstate.State) { run.Integration = &runstate.Integration{} })
	closed.status(stateTwo, "closed")
	closed.wants(EpicWorking, "the check of the whole epic is owed")
}

func TestAMemberAddedAfterDeliveryOpensTheEpicAgain(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, stateTwo, stateOther)
	b.check(epicprogress.FoundAllMet, hours(2))
	b.entry.Deliveries = []epicprogress.Delivery{{At: hours(2), Evidence: "check"}}
	b.wants(EpicIntegrated)
	_, err := operatorOf(t, b.root).Add(stateEpic, stateOther, hours(3))
	b.must(err)
	standing := b.wants(EpicWorking, "the check of the whole epic is owed", "opened again since")
	if standing.DeliveredBefore == nil || !standing.DeliveredBefore.Equal(hours(2)) {
		t.Errorf("DeliveredBefore = %v, want the old delivery kept as history", standing.DeliveredBefore)
	}
}

func TestTakingAnItemOutChangesWhatTheEpicOwes(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne)
	b.status(stateTwo, "closed")
	b.wants(EpicWorking, "closed without its change having landed")
	b.remove(stateTwo, hours(3))
	b.wants(EpicWorking, "the check of the whole epic is owed")
	// One that did not pass is made again after a removal, and is no round.
	b.check(epicprogress.FoundNotMet, hours(4))
	b.wants(EpicWorking, "write the piece")
	b.join(statePiece, "check", hours(5))
	b.landed(statePiece)
	b.check(epicprogress.FoundNotMet, hours(6))
	b.remove(statePiece, hours(7))
	b.wants(EpicWorking, "the check of the whole epic is owed")
	b.check(epicprogress.FoundNotMet, hours(8))
	b.wants(EpicWorking, "Waiting on the development manager: write the piece")
	b.check(epicprogress.FoundAllMet, hours(9))
	b.wants(EpicIntegrated)

	delivered := newStateBench(t)
	delivered.landed(stateOne, stateTwo)
	delivered.check(epicprogress.FoundAllMet, hours(2))
	delivered.remove(stateTwo, hours(3))
	delivered.wants(EpicIntegrated)
}

func TestASuggestionFoundDuringAnEpicIsCountedAndStartsNothing(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	filed := filedReport("report-00000000000000000000000000000001", report.SeverityNote, hours(1))
	filed.WorkItemID, filed.DuringEpic = stateOne, stateEpic
	before := filedReport("report-00000000000000000000000000000002", report.SeverityNote, hours(1))
	b.sources.Reports = fakeReports{reports: []report.Report{filed, before},
		handlings: []report.Handling{{ReportID: filed.ID, Requests: []report.Request{{Request: "admit it", Admitted: statePiece}}}}}
	b.set(stateOther, func(item *beads.WorkItem) {
		item.Origin = domain.WorkItemOrigin{Asker: domain.AskerReport, Report: before.ID}
	})
	b.ready = []string{statePiece}
	scope, err := epicscope.Read(b.root, "yoyodyne")
	b.must(err)
	if epics := b.read(); epics.Suggestions != 1 || epics.ItemsHeldOut != 1 {
		t.Errorf("suggestions = %d and held out = %d, want the one suggestion in each", epics.Suggestions, epics.ItemsHeldOut)
	}
	b.set(stateOther, func(item *beads.WorkItem) { item.Origin.Report = filed.ID })
	if epics := b.read(); epics.Suggestions != 2 {
		t.Errorf("suggestions = %d, want both", epics.Suggestions)
	}
	wantSaid(t, ReadStanding(context.Background(), b.sources).RenderEpics(), "Items suggested during an open epic, which start nothing: 2")
	for _, id := range []string{statePiece, stateOther} {
		if maystart.Decide(b.root, "yoyodyne", maystart.Run(maystart.ReadyWork, id)).MayStart() {
			t.Errorf("%s may start", id)
		}
	}
	if after, err := epicscope.Read(b.root, "yoyodyne"); err != nil || !reflect.DeepEqual(scope, after) {
		t.Errorf("the record changed: %v, %v", after, err)
	}
}

func TestAStateThatCannotBeWorkedOutIsSaid(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.check(epicprogress.FoundAllMet, hours(2))
	b.read()
	b.sources.Runs = fakeRuns{}
	if said := ReadStanding(context.Background(), b.sources).RenderEpics(); !strings.Contains(said, "where it stands could not be worked out") {
		t.Errorf("status says\n%s", said)
	}
}

// A pull request a person closed leaves its member parked and open in the
// tracker, and the piece written in its place still stands for it.
func TestAPieceInPlaceOfAClosedPullRequestStandsForItsMember(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, statePiece)
	b.handedOver(stateTwo)
	b.join(statePiece, stateTwo, hours(2))
	b.closedUnmerged(stateTwo, hours(3))
	b.check(epicprogress.FoundAllMet, hours(4))
	// This one was split off before the request was closed.
	b.wants(EpicWorking, "Waiting on the Lead Product Manager: Title "+stateTwo+" ("+stateTwo+") had its pull request closed unmerged")
	b.entry.Joined[0].At = hours(4)
	b.check(epicprogress.FoundAllMet, hours(5))
	b.wants(EpicIntegrated)
	b.runs[statePiece] = nil
	b.handedOver(statePiece)
	b.wants(EpicHandedOver)
	b.ready, b.runs[statePiece] = []string{statePiece}, nil
	b.status(statePiece, "open")
	if standing := b.wants(EpicWorking); len(standing.Waits) != 0 {
		t.Errorf("waits = %+v, want none but the piece's own start", standing.Waits)
	}
}

func TestEachStepOwedIsNamed(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.set(stateOne, func(item *beads.WorkItem) {
		item.Parking, item.Labels = "later", []string{domain.NeedsDesignLabel}
	})
	b.set(stateTwo, func(item *beads.WorkItem) {
		item.Dependencies = []beads.Dependency{{ID: stateOther, Type: beads.BlocksDependency}, {ID: statePiece, Type: beads.BlocksDependency}}
	})
	b.ask("Is "+stateOne+" enough?", hours(1))
	b.ask("Does "+stateOne+" need a lock?", hours(1))
	standing := b.wants(EpicWorking, "enough?", "a lock?", "is parked", "waits for its design", "waits for Title "+stateOther, "waits for Title "+statePiece)
	if len(standing.Waits) != 6 {
		t.Errorf("waits = %+v, want six", standing.Waits)
	}

	// One directive told by its own record and by the hold it places is one step.
	paused := newStateBench(t)
	paused.landed(stateOne)
	paused.ready = []string{stateTwo}
	paused.sources.Directives = fakeDirectives{recorded: []directive.Directive{{ID: "directive-1", Kind: directive.KindAmbiguous, ReceivedAt: hours(1), Scope: []string{stateTwo}, Unresolved: "which way"}}}
	if standing := paused.read().Open[0].EpicStanding; len(standing.Waits) != 1 || !strings.Contains(standing.Waits[0].Says, "which way") {
		t.Errorf("waits = %+v, want the directive once", standing.Waits)
	}
}

func TestAnEndedEpicKeepsTheStateItHadWhenItWasEnded(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne)
	b.handedOver(stateTwo)
	b.deliver(hours(2))
	b.end(hours(3))
	b.wants(EpicHandedOver)
	later := func() {
		b.askOperator(stateOne, hours(4))
		b.ask("Was "+stateOne+" right?", hours(4))
		b.inFlight = []runstate.State{{RunID: "run-later", WorkItemID: stateOne, Status: runstate.StatusRunning, StartedAt: hours(4)}}
		b.run(stateOne, 4, func(run *runstate.State) { run.RunID, run.Status = "run-later", runstate.StatusFailed })
		b.status(stateOne, "in_progress")
	}
	merged := hours(5)
	b.lastRun(stateTwo, func(run *runstate.State) {
		run.PersonMerge.MergedAt, run.Integration = &merged, &runstate.Integration{ThroughPullRequest: true}
	})
	b.wants(EpicHandedOver)
	later()
	b.wants(EpicHandedOver)
	// Merged before it was ended, it is integrated.
	merged = hours(2).Add(time.Minute)
	b.wants(EpicIntegrated)

	// So where a later run had taken the pull request over.
	for over, want := range map[int]EpicState{2: EpicIntegrated, 4: EpicHandedOver} {
		taken := newStateBench(t)
		taken.landed(stateOne)
		taken.handedOver(stateTwo)
		taken.deliver(hours(2))
		taken.end(hours(3))
		taken.handedOver(stateTwo)
		at, mergedAt := hours(over), hours(over).Add(time.Minute)
		taken.lastRun(stateTwo, func(run *runstate.State) {
			run.RunID, run.StartedAt = "run-taken", at
			run.PersonMerge.MergedAt, run.Integration = &mergedAt, &runstate.Integration{ThroughPullRequest: true}
		})
		first := taken.runs[stateTwo][0].PersonMerge
		first.SupersededBy, first.SupersededAt = "run-taken", &at
		taken.wants(want)
	}

	closed := newStateBench(t)
	closed.landed(stateOne)
	closed.handedOver(stateTwo)
	closed.deliver(hours(2))
	closed.end(hours(3))
	closed.closedUnmerged(stateTwo, hours(4))
	closed.wants(EpicHandedOver)

	for name, unmoved := range map[string]func(*stateBench){
		"a question to the operator raised only afterwards": func(b *stateBench) { b.askOperator(stateOne, hours(4)) },
		"one its conversation dropped before the delivery": func(b *stateBench) {
			b.askOperator(stateOne, hours(1))
			b.sources.Conversations.(loggedConversations).recorded[0].PendingConcerns = nil
		},
		"one answered before it was ended": func(b *stateBench) { b.askOperator(stateOne, hours(1), hours(2)) },
		"one about another item":           func(b *stateBench) { b.askOperator(stateOther, hours(1)) },
	} {
		b := newStateBench(t)
		b.landed(stateOne, stateTwo)
		b.deliver(hours(2))
		b.end(hours(3))
		unmoved(b)
		t.Log(name)
		b.wants(EpicIntegrated)
		// What cannot be read is left out, and the state is kept.
		unread := b.sources.Conversations.(loggedConversations)
		unread.skipped = []runstate.SkippedLine{{}}
		b.sources.Conversations = unread
		b.wants(EpicIntegrated)
		b.sources.Conversations = unread.fakeConversations
		b.wants(EpicIntegrated)
	}

	untimed := newStateBench(t)
	untimed.landed(stateOne, stateTwo)
	untimed.deliver(hours(2))
	untimed.end(hours(3))
	untimed.askOperator(stateOne, hours(1))
	asked := untimed.sources.Conversations.(loggedConversations)
	for name, events := range map[string][]execution.Event{"no event": nil, "one that will not decode": {{Type: execution.EventConcernRaised, Timestamp: hours(1), Payload: json.RawMessage("7")}}} {
		asked.events = events
		untimed.sources.Conversations = asked
		t.Log(name)
		untimed.wants(EpicIntegrated)
	}

	for name, undelivered := range map[string]func(*stateBench){
		"a question to the operator open when it was ended": func(b *stateBench) { b.askOperator(stateOne, hours(1)) },
		"one whose conversation was replaced since": func(b *stateBench) {
			b.askOperator(stateOne, hours(2).Add(time.Minute))
			b.sources.Conversations.(loggedConversations).recorded[0].PendingConcerns = nil
		},
		"one answered only since":           func(b *stateBench) { b.askOperator(stateOne, hours(1), hours(4)) },
		"a question open when it was ended": func(b *stateBench) { b.ask("Is "+stateOne+" enough?", hours(1)) },
		"a directive open when it was ended, settled since": func(b *stateBench) {
			settled := hours(4)
			b.sources.Directives = fakeDirectives{recorded: []directive.Directive{{ID: "directive-1", Kind: directive.KindAmbiguous, ReceivedAt: hours(1), ResolvedAt: &settled, Scope: []string{stateOne}, Unresolved: "which way"}}}
		},
		"a member not done when it was ended": func(b *stateBench) { b.runs[stateTwo] = nil },
		"a member landed only afterwards":     func(b *stateBench) { b.lastRun(stateTwo, func(run *runstate.State) { run.StartedAt = hours(4) }) },
		"no delivery recorded":                func(b *stateBench) { b.entry.Deliveries = nil },
	} {
		b := newStateBench(t)
		b.landed(stateOne, stateTwo)
		b.deliver(hours(2))
		b.end(hours(3))
		b.wants(EpicIntegrated)
		undelivered(b)
		t.Log(name)
		b.wants(EpicCancelled)
	}
}
