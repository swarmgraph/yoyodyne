package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

type fakeRestartRequests struct {
	requests []runstate.RestartRequest
	err      error
}

func (f fakeRestartRequests) List() ([]runstate.RestartRequest, error) { return f.requests, f.err }

type fakeLaneReports struct {
	reports map[string]runstate.LaneReport
	err     error
}

func (f fakeLaneReports) Current(agent string) (runstate.LaneReport, bool, error) {
	if f.err != nil {
		return runstate.LaneReport{}, false, f.err
	}
	current, written := f.reports[agent]
	return current, written, nil
}

func (f fakeLaneReports) ReportPath(agent string) string {
	return "/state/products/yoyodyne/program-managers/" + agent + "/report.json"
}

type fakePasses struct {
	passes     []runstate.Sweep
	unreadable []runstate.UnreadableSweep
	err        error
}

func (f fakePasses) List() ([]runstate.Sweep, []runstate.UnreadableSweep, error) {
	return f.passes, f.unreadable, f.err
}

type fakeExchanges struct {
	exchanges []exchange.Exchange
	err       error
}

func (f fakeExchanges) List() ([]exchange.Exchange, error) { return f.exchanges, f.err }

type fakeFirstSeen struct {
	seen map[string]time.Time
	err  error
}

func (f fakeFirstSeen) FirstSeen() (map[string]time.Time, error) { return f.seen, f.err }

const (
	factoryConversation = "chat-00000000000000000000000000000001"
	writingConversation = "chat-00000000000000000000000000000002"
)

// programManagerSources is a quiet harness with two configured instances, each
// with an hourly schedule and a conversation opened three hours before the
// reading, and every record their status is derived from wired and empty. Each
// test moves one record.
func programManagerSources() Sources {
	sources := quietSources()
	sources.ProgramManagers = []ProgramManagerInstance{
		{Agent: "factory-pgm", Lane: "reliability", Every: time.Hour},
		{Agent: "writing-pgm", Lane: "writing", Every: time.Hour},
	}
	sources.Conversations = fakeConversations{recorded: []runstate.Conversation{
		{ConversationID: factoryConversation, Agent: "factory-pgm", Role: domain.RoleProgramManager, StartedAt: moment.Add(-3 * time.Hour)},
		{ConversationID: writingConversation, Agent: "writing-pgm", Role: domain.RoleProgramManager, StartedAt: moment.Add(-3 * time.Hour)},
	}}
	sources.RestartRequests = fakeRestartRequests{}
	sources.LaneReports = fakeLaneReports{}
	sources.Exchanges = fakeExchanges{}
	sources.Passes = fakePasses{passes: []runstate.Sweep{
		completedPass(factoryConversation, moment.Add(-30*time.Minute)),
		completedPass(writingConversation, moment.Add(-30*time.Minute)),
	}}
	return sources
}

// completedPass is a pass that ended in an account, in one conversation.
func completedPass(conversation string, ended time.Time) runstate.Sweep {
	return runstate.Sweep{
		Task: "pgm-pass", Role: domain.RoleProgramManager, ConversationID: conversation,
		StartedAt: ended.Add(-time.Minute), EndedAt: ended, Turns: 1,
		Result: &sweep.Result{Summary: "the lane is moving"},
	}
}

// blockedBy is a lane report naming one blocker per citation.
func blockedBy(agent string, cites ...string) runstate.LaneReport {
	blockers := make([]runstate.LaneReportBlocker, 0, len(cites))
	for _, cited := range cites {
		blockers = append(blockers, runstate.LaneReportBlocker{What: "waiting on " + cited, WaitingOn: string(MoverProductManager), Cites: cited})
	}
	return runstate.LaneReport{
		SchemaVersion: runstate.LaneReportSchemaVersion, ProductID: "yoyodyne", Agent: agent, Version: 3,
		Report:     runstate.LaneReportContent{Summary: "moving", Remaining: []string{}, Blockers: blockers},
		Stamp:      runstate.LaneReportStamp{ConversationID: factoryConversation, Turn: 4},
		RecordedAt: moment.Add(-20 * time.Minute),
	}
}

func instanceNamed(t *testing.T, sources Sources, agent string) ProgramManager {
	t.Helper()
	instance, known, problem := ProgramManagerOf(sources, agent)
	if !known {
		t.Fatalf("ProgramManagerOf(%s) knows no such instance", agent)
	}
	if problem != "" {
		t.Fatalf("ProgramManagerOf(%s) problem = %q, want none", agent, problem)
	}
	return instance
}

func filedBy(id, agent string) report.Report {
	filed := filedReport(id, report.SeverityNote, moment.Add(-time.Hour))
	filed.Role, filed.Agent = domain.RoleProgramManager, agent
	return filed
}

func restartRequest(id, agent string) runstate.RestartRequest {
	return runstate.RestartRequest{
		SchemaVersion: runstate.RestartRequestSchemaVersion, ProductID: "yoyodyne", ID: id,
		Agent: agent, Part: config.ServiceScheduler, Reason: "died four times in an hour", RequestedAt: moment.Add(-time.Hour),
	}
}

func proposalBy(id, agent string) amendment.Record {
	return amendment.Record{Proposal: &amendment.Proposal{ID: id, Role: domain.RoleProgramManager, Agent: agent, RaisedAt: moment.Add(-time.Hour)}}
}

func askedBy(id, agent string, outcome exchange.Outcome) exchange.Exchange {
	return exchange.Exchange{ID: id, Asker: exchange.Party{Role: domain.RoleProgramManager, Agent: agent}, Outcome: outcome}
}

// An instance's open restart requests are carried by its query and by the
// standing under program_managers, beside every configured instance with none,
// and are not a line waiting on a person.
func TestAProgramManagersOpenRestartRequestsAreInItsQueryAndTheStanding(t *testing.T) {
	t.Parallel()

	request := restartRequest("restart-0123456789abcdef", "factory-pgm")
	answered := restartRequest("restart-fedcba9876543210", "factory-pgm")
	at := moment.Add(-10 * time.Minute)
	answered.AnsweredAt, answered.Answer = &at, "restarted"
	sources := programManagerSources()
	sources.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{request, answered}}

	instance := instanceNamed(t, sources, "factory-pgm")
	if len(instance.RestartRequests) != 1 || instance.RestartRequests[0].ID != request.ID {
		t.Fatalf("RestartRequests = %+v; want the one open request and not the answered one", instance.RestartRequests)
	}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.ProgramManagers) != 2 || standing.ProgramManagers[0].Agent != "factory-pgm" || standing.ProgramManagers[1].Agent != "writing-pgm" {
		t.Fatalf("ProgramManagers = %+v, want both instances by name", standing.ProgramManagers)
	}
	if len(standing.ProgramManagers[1].RestartRequests) != 0 {
		t.Errorf("writing-pgm carries %+v, want none", standing.ProgramManagers[1].RestartRequests)
	}
	if len(standing.NeedsHuman) != 0 {
		t.Errorf("NeedsHuman = %+v; a request waits on the supervisor's pass, not on a person", standing.NeedsHuman)
	}
	encoded, err := json.Marshal(standing)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{
		`"program_managers":[{"agent":"factory-pgm","lane":"reliability","status":"working"`,
		`"restart_requests":[{`,
		`"restart_requests":[]`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("standing JSON does not carry %s:\n%s", want, encoded)
		}
	}
}

// A request log that cannot be read is said, and the configured instances are
// still listed rather than the reading reporting that nobody asked anything.
func TestAnUnreadableRestartRequestLogIsSaidRatherThanReadAsNone(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.RestartRequests = fakeRestartRequests{err: errors.New("torn line")}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.ProgramManagers) != 2 || !strings.Contains(standing.ProgramManagersProblem, "torn line") {
		t.Errorf("ProgramManagers = %+v, problem %q; want the instances and the reason", standing.ProgramManagers, standing.ProgramManagersProblem)
	}
}

// Each kind of record an instance can raise blocks it while it is its own and
// still open: a report nobody has handled, an amendment nobody has decided, an
// exchange still open, and a restart request nothing has answered. Each
// blocker carries its mover and its citation, and the query carries the
// report's path and when it was written.
func TestAnOpenAskOfTheInstancesOwnBlocksIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cites string
		kind  CitedRecord
		wire  func(*Sources)
	}{
		{"a report the product manager has not handled", "report-00000000000000000000000000000001", CitedReport, func(s *Sources) {
			s.Reports = fakeReports{reports: []report.Report{filedBy("report-00000000000000000000000000000001", "factory-pgm")}}
		}},
		{"an amendment nobody has decided", "97e14527", CitedAmendment, func(s *Sources) {
			s.Amendments = fakeAmendments{records: []amendment.Record{proposalBy("97e14527", "factory-pgm")}}
		}},
		{"an exchange still open", "exchange-0123456789abcdef", CitedExchange, func(s *Sources) {
			s.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{askedBy("exchange-0123456789abcdef", "factory-pgm", "")}}
		}},
		{"a restart request nothing has answered", "restart-0123456789abcdef", CitedRestartRequest, func(s *Sources) {
			s.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "factory-pgm")}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sources := programManagerSources()
			sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": blockedBy("factory-pgm", tc.cites)}}
			tc.wire(&sources)

			instance := instanceNamed(t, sources, "factory-pgm")
			if instance.Status != ProgramManagerBlocked || !instance.Blocked || instance.Stale {
				t.Fatalf("status = %s (blocked %t, stale %t), want blocked", instance.Status, instance.Blocked, instance.Stale)
			}
			want := ProgramManagerBlocker{What: "waiting on " + tc.cites, WaitingOn: MoverProductManager, Cites: tc.cites, Record: tc.kind}
			if len(instance.Blockers) != 1 || instance.Blockers[0] != want || len(instance.Claims) != 0 {
				t.Fatalf("blockers = %+v, claims = %+v; want exactly %+v", instance.Blockers, instance.Claims, want)
			}
			if instance.ReportPath != "/state/products/yoyodyne/program-managers/factory-pgm/report.json" ||
				instance.ReportWrittenAt == nil || !instance.ReportWrittenAt.Equal(moment.Add(-20*time.Minute)) {
				t.Errorf("report path %q written at %v, want the store's path and the version's moment", instance.ReportPath, instance.ReportWrittenAt)
			}
		})
	}
}

// A citation the record does not bear out blocks nothing and is carried as a
// claim with the reason: one that resolves to nothing, one that resolves to
// another instance's record, and one that resolves to a record already
// decided — a handled report, a decided amendment, a closed exchange, an
// answered restart request.
func TestACitationTheRecordDoesNotBearOutIsAClaimWithItsReason(t *testing.T) {
	t.Parallel()

	answered := restartRequest("restart-fedcba9876543210", "factory-pgm")
	at := moment.Add(-10 * time.Minute)
	answered.AnsweredAt = &at
	cases := []struct {
		name   string
		cites  string
		reason string
		wire   func(*Sources)
	}{
		{"a citation that resolves to nothing", "report-99999999999999999999999999999999",
			"which is no request, report, amendment, or exchange on record", func(*Sources) {}},
		{"a citation to another instance's report", "report-00000000000000000000000000000002",
			"which writing-pgm filed rather than this instance", func(s *Sources) {
				s.Reports = fakeReports{reports: []report.Report{filedBy("report-00000000000000000000000000000002", "writing-pgm")}}
			}},
		{"a citation to another instance's restart request", "restart-0123456789abcdef",
			"which writing-pgm made rather than this instance", func(s *Sources) {
				s.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "writing-pgm")}}
			}},
		{"a citation to a handled report", "report-00000000000000000000000000000003",
			"which the Lead Product Manager handled at 2026-08-30T11:30:00Z", func(s *Sources) {
				s.Reports = fakeReports{
					reports: []report.Report{filedBy("report-00000000000000000000000000000003", "factory-pgm")},
					handlings: []report.Handling{{ReportID: "report-00000000000000000000000000000003", Role: domain.RoleProductManager,
						Reason: "admitted", RecordedAt: moment.Add(-30 * time.Minute)}},
				}
			}},
		{"a citation to a decided amendment", "97e14527",
			"which was decided (declined)", func(s *Sources) {
				s.Amendments = fakeAmendments{records: []amendment.Record{
					proposalBy("97e14527", "factory-pgm"),
					{Decision: &amendment.Decision{ProposalID: "97e14527", Verdict: amendment.VerdictDeclined, DecidedAt: moment.Add(-5 * time.Minute)}},
				}}
			}},
		{"a citation to a closed exchange", "exchange-0123456789abcdef",
			"which closed resolved", func(s *Sources) {
				s.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{askedBy("exchange-0123456789abcdef", "factory-pgm", exchange.OutcomeResolved)}}
			}},
		{"a citation to an answered restart request", "restart-fedcba9876543210",
			"which was answered at 2026-08-30T11:50:00Z", func(s *Sources) {
				s.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{answered}}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sources := programManagerSources()
			sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": blockedBy("factory-pgm", tc.cites)}}
			tc.wire(&sources)

			instance := instanceNamed(t, sources, "factory-pgm")
			if instance.Status != ProgramManagerWorking || instance.Blocked || len(instance.Blockers) != 0 {
				t.Fatalf("status = %s, blockers = %+v; a claim the record does not bear out blocks nothing", instance.Status, instance.Blockers)
			}
			if len(instance.Claims) != 1 || instance.Claims[0].Cites != tc.cites || instance.Claims[0].WaitingOn != MoverProductManager ||
				!strings.Contains(instance.Claims[0].Reason, tc.reason) {
				t.Fatalf("claims = %+v, want one citing %s with a reason saying %q", instance.Claims, tc.cites, tc.reason)
			}
		})
	}
}

// A citation that resolves to none of what could be read, while some of it
// could not be, is not said to resolve to nothing: the reason names what could
// not be read.
func TestACitationNothingReadableHoldsSaysWhatCouldNotBeRead(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": blockedBy("factory-pgm", "exchange-0123456789abcdef")}}
	sources.Exchanges = fakeExchanges{err: errors.New("disk gone")}

	instance, _, problem := ProgramManagerOf(sources, "factory-pgm")
	if instance.Blocked || len(instance.Claims) != 1 || !strings.Contains(instance.Claims[0].Reason, "the exchanges could not be read") {
		t.Fatalf("claims = %+v, want the one claim saying the exchanges could not be read", instance.Claims)
	}
	if !strings.Contains(problem, "disk gone") {
		t.Errorf("problem = %q, want the exchanges' own failure", problem)
	}
}

// Stale is no completed pass within twice the schedule, from the last one that
// completed; a pass that produced no account is not a completed pass.
func TestAnInstanceWithNoCompletedPassWithinTwiceItsScheduleIsStale(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	refused := completedPass(factoryConversation, moment.Add(-10*time.Minute))
	refused.Result, refused.Problem = nil, "the provider refused the turn"
	sources.Passes = fakePasses{passes: []runstate.Sweep{
		completedPass(factoryConversation, moment.Add(-150*time.Minute)),
		refused,
		completedPass(writingConversation, moment.Add(-110*time.Minute)),
	}}

	factory := instanceNamed(t, sources, "factory-pgm")
	if factory.Status != ProgramManagerStale || !factory.Stale ||
		!strings.Contains(factory.StaleSays, "no pass has completed since 2026-08-30T09:30:00Z, and its schedule is every 1h0m0s") {
		t.Fatalf("factory-pgm = %s, %q; want stale from its last completed pass, the refused one not counted", factory.Status, factory.StaleSays)
	}
	if factory.LastCompletedPassAt == nil || !factory.LastCompletedPassAt.Equal(moment.Add(-150*time.Minute)) {
		t.Errorf("LastCompletedPassAt = %v, want the completed pass", factory.LastCompletedPassAt)
	}
	if writing := instanceNamed(t, sources, "writing-pgm"); writing.Status != ProgramManagerWorking || writing.Stale {
		t.Errorf("writing-pgm = %s; a pass inside twice the schedule is working", writing.Status)
	}
}

// An instance that has never completed a pass is stale from twice its schedule
// after it was first woken, and not before.
func TestAnInstanceWithNoCompletedPassIsStaleFromTwiceItsScheduleAfterActivation(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Conversations = fakeConversations{recorded: []runstate.Conversation{
		{ConversationID: factoryConversation, Agent: "factory-pgm", Role: domain.RoleProgramManager, StartedAt: moment.Add(-3 * time.Hour)},
		{ConversationID: writingConversation, Agent: "writing-pgm", Role: domain.RoleProgramManager, StartedAt: moment.Add(-90 * time.Minute)},
	}}
	sources.Passes = fakePasses{}

	if factory := instanceNamed(t, sources, "factory-pgm"); factory.Status != ProgramManagerStale ||
		!strings.Contains(factory.StaleSays, "no pass has ever completed, and it was first woken at 2026-08-30T09:00:00Z") {
		t.Errorf("factory-pgm = %s, %q; want stale three hours after activation on an hourly schedule", factory.Status, factory.StaleSays)
	}
	if writing := instanceNamed(t, sources, "writing-pgm"); writing.Status != ProgramManagerWorking {
		t.Errorf("writing-pgm = %s; ninety minutes after activation is inside twice an hourly schedule", writing.Status)
	}
}

// An instance the scheduler has never woken has no conversation to be measured
// from, and is measured from when it was first seen in the configuration: stale
// past twice its schedule after that, and working inside it.
func TestAnInstanceNothingHasWokenIsStaleFromTwiceItsScheduleAfterItWasFirstSeen(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Conversations = fakeConversations{}
	sources.Passes = fakePasses{}
	sources.FirstSeen = fakeFirstSeen{seen: map[string]time.Time{
		"factory-pgm": moment.Add(-150 * time.Minute),
		"writing-pgm": moment.Add(-90 * time.Minute),
	}}

	if factory := instanceNamed(t, sources, "factory-pgm"); factory.Status != ProgramManagerStale ||
		factory.StaleSays != "no pass has ever completed, and it was first seen in the configuration at 2026-08-30T09:30:00Z on a schedule of every 1h0m0s" {
		t.Errorf("factory-pgm = %s, %q; want stale past twice its schedule after it was first seen", factory.Status, factory.StaleSays)
	}
	if writing := instanceNamed(t, sources, "writing-pgm"); writing.Status != ProgramManagerWorking {
		t.Errorf("writing-pgm = %s; ninety minutes after it was first seen is inside twice an hourly schedule", writing.Status)
	}

	// With no first-seen record, the same instances have nothing to be measured
	// from, which is the blind spot the record closes.
	sources.FirstSeen = nil
	if factory := instanceNamed(t, sources, "factory-pgm"); factory.Stale {
		t.Errorf("factory-pgm = %+v with no trace at all; want not stale", factory)
	}
}

// The earlier of the two traces is the one measured from: an instance configured
// before the first-seen record existed keeps its first conversation.
func TestAnInstancesFirstConversationCountsWhereItIsEarlierThanItWasFirstSeen(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Passes = fakePasses{}
	sources.FirstSeen = fakeFirstSeen{seen: map[string]time.Time{"factory-pgm": moment.Add(-10 * time.Minute)}}

	if factory := instanceNamed(t, sources, "factory-pgm"); factory.Status != ProgramManagerStale ||
		!strings.Contains(factory.StaleSays, "first woken at 2026-08-30T09:00:00Z") {
		t.Errorf("factory-pgm = %s, %q; want stale from its conversation three hours ago", factory.Status, factory.StaleSays)
	}
}

// A first-seen record that could not be read is said, and not read as none.
func TestAnUnreadableFirstSeenRecordIsSaid(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.FirstSeen = fakeFirstSeen{err: errors.New("permission denied")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.ProgramManagersProblem, "first seen in the configuration could not be read") ||
		!strings.Contains(standing.ProgramManagersProblem, "permission denied") {
		t.Errorf("problem = %q, want the first-seen record's failure", standing.ProgramManagersProblem)
	}
}

// Stale outranks blocked in the one word shown, and both are carried.
func TestStaleOutranksBlockedAndBothAreCarried(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Passes = fakePasses{passes: []runstate.Sweep{completedPass(factoryConversation, moment.Add(-5*time.Hour))}}
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": blockedBy("factory-pgm", "restart-0123456789abcdef")}}
	sources.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "factory-pgm")}}

	instance := instanceNamed(t, sources, "factory-pgm")
	if instance.Status != ProgramManagerStale || !instance.Stale || !instance.Blocked || len(instance.Blockers) != 1 {
		t.Fatalf("instance = %+v; want the word stale with the blocker still carried", instance)
	}
}

// A pass log that could not be read calls nobody stale and says why, rather
// than deriving staleness from a file nobody could open.
func TestAnUnreadablePassLogCallsNobodyStaleAndSaysSo(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Passes = fakePasses{err: errors.New("permission denied")}
	standing := ReadStanding(context.Background(), sources)
	for _, instance := range standing.ProgramManagers {
		if instance.Stale {
			t.Errorf("%s is stale over a pass log nobody could read", instance.Agent)
		}
	}
	if !strings.Contains(standing.ProgramManagersProblem, "permission denied") {
		t.Errorf("problem = %q, want the pass log's failure", standing.ProgramManagersProblem)
	}
}

// An instance whose schedule is events alone has nothing to be stale against.
func TestAnInstanceWithNoScheduleIsNeverStale(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.ProgramManagers = []ProgramManagerInstance{{Agent: "factory-pgm", Lane: "reliability"}}
	sources.Passes = fakePasses{}
	if instance := instanceNamed(t, sources, "factory-pgm"); instance.Stale {
		t.Errorf("instance = %+v; an instance with no every is never stale", instance)
	}
}

// `yoyo status` prints a line per instance under the four lines, with its
// status word and why; the hourly line's count names the stale ones and is
// empty where none is.
func TestTheInstancesAreRenderedOneLineEachAndTheStaleOnesCounted(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Passes = fakePasses{passes: []runstate.Sweep{
		completedPass(factoryConversation, moment.Add(-5*time.Hour)),
		completedPass(writingConversation, moment.Add(-10*time.Minute)),
	}}
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{
		"writing-pgm": blockedBy("writing-pgm", "exchange-0123456789abcdef", "report-99999999999999999999999999999999"),
	}}
	sources.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{askedBy("exchange-0123456789abcdef", "writing-pgm", "")}}
	standing := ReadStanding(context.Background(), sources)

	want := "Program managers (2):\n" +
		"  factory-pgm — lane reliability — stale: no pass has completed since 2026-08-30T07:00:00Z, and its schedule is every 1h0m0s\n" +
		"  writing-pgm — lane writing — blocked: blocked on 1 open ask (exchange-0123456789abcdef); 1 blocker its report names that the record does not bear out\n"
	if got := standing.RenderProgramManagers(); got != want {
		t.Errorf("RenderProgramManagers() =\n%s\nwant\n%s", got, want)
	}
	if got := standing.StaleProgramManagersLine(); got != "Program managers stale: 1 of 2 (factory-pgm)\n" {
		t.Errorf("StaleProgramManagersLine() = %q", got)
	}
	if got := ReadStanding(context.Background(), programManagerSources()).StaleProgramManagersLine(); got != "" {
		t.Errorf("StaleProgramManagersLine() with nothing stale = %q, want nothing", got)
	}
	if got := ReadStanding(context.Background(), quietSources()).RenderProgramManagers(); got != "" {
		t.Errorf("RenderProgramManagers() with no instance = %q, want nothing", got)
	}
}

// rewritingLaneReports is a report store whose report is rewritten each time it
// is read: each read of its one instance's report answers the next version in
// turn, and the last once the versions run out. Every other instance has none.
type rewritingLaneReports struct {
	versions []runstate.LaneReport
	reads    *int
}

func (f rewritingLaneReports) Current(agent string) (runstate.LaneReport, bool, error) {
	if agent != f.versions[0].Agent {
		return runstate.LaneReport{}, false, nil
	}
	at := *f.reads
	*f.reads++
	if at >= len(f.versions) {
		at = len(f.versions) - 1
	}
	return f.versions[at], true, nil
}

func (f rewritingLaneReports) ReportPath(agent string) string {
	return fakeLaneReports{}.ReportPath(agent)
}

// One instance's report query carries the instance as the standing carries it
// and the report its blockers were read from — the summary, what remains, and
// which version and turn wrote it — so the page's report card and the standing
// cannot disagree about the instance.
func TestAProgramManagersReportQueryCarriesTheInstanceAndItsReport(t *testing.T) {
	t.Parallel()

	current := blockedBy("factory-pgm", "restart-0123456789abcdef")
	current.Report.Summary = "Two stoppages this week had one cause."
	current.Report.Remaining = []string{"a root-cause item for the checkout timeout"}
	current.Stamp.Pass = "pgm-pass#12"
	sources := programManagerSources()
	sources.RestartRequests = fakeRestartRequests{requests: []runstate.RestartRequest{restartRequest("restart-0123456789abcdef", "factory-pgm")}}
	sources.LaneReports = fakeLaneReports{reports: map[string]runstate.LaneReport{"factory-pgm": current}}

	answer, err := ReadProgramManagerReport(sources, "factory-pgm")
	if err != nil {
		t.Fatalf("ReadProgramManagerReport() error = %v", err)
	}
	if answer.Instance.Status != ProgramManagerBlocked || len(answer.Instance.Blockers) != 1 || answer.Problem != "" {
		t.Fatalf("Instance = %+v, problem %q; want the blocked instance the standing carries", answer.Instance, answer.Problem)
	}
	standing := ReadStanding(context.Background(), sources)
	if standing.ProgramManagers[0].Status != answer.Instance.Status || standing.ProgramManagers[0].ReportPath != answer.Instance.ReportPath {
		t.Errorf("the standing carries %+v and the query %+v", standing.ProgramManagers[0], answer.Instance)
	}
	want := LaneReportText{
		Summary: "Two stoppages this week had one cause.", Remaining: []string{"a root-cause item for the checkout timeout"},
		Version: 3, Pass: "pgm-pass#12", ConversationID: factoryConversation, Turn: 4, WrittenAt: current.RecordedAt,
	}
	if answer.Report == nil || answer.Report.Summary != want.Summary || strings.Join(answer.Report.Remaining, "|") != strings.Join(want.Remaining, "|") ||
		answer.Report.Version != want.Version || answer.Report.Pass != want.Pass || answer.Report.ConversationID != want.ConversationID ||
		answer.Report.Turn != want.Turn || !answer.Report.WrittenAt.Equal(want.WrittenAt) {
		t.Errorf("Report = %+v, want %+v", answer.Report, want)
	}
	if !answer.ObservedAt.Equal(moment) {
		t.Errorf("ObservedAt = %v, want the reading's moment %v", answer.ObservedAt, moment)
	}

	// An instance that has written no report is carried with no report, rather
	// than refused: the card still has its status and its requests to show.
	quiet, err := ReadProgramManagerReport(sources, "writing-pgm")
	if err != nil || quiet.Report != nil || quiet.Instance.Agent != "writing-pgm" {
		t.Errorf("ReadProgramManagerReport(writing-pgm) = %+v, %v; want the instance and no report", quiet, err)
	}
}

// A name the read model knows no instance under, and a name that is not an
// agent's name at all, are both the instance not being recorded — a different
// answer from a record that could not be read.
func TestAReportQueryForNoSuchInstanceIsNotFound(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"nobody-pgm", "../factory-pgm", ""} {
		if _, err := ReadProgramManagerReport(programManagerSources(), name); !errors.Is(err, ErrNoSuchProgramManager) {
			t.Errorf("ReadProgramManagerReport(%q) error = %v, want ErrNoSuchProgramManager", name, err)
		}
	}
}

// A report rewritten between the derivation and the read of its text is read
// again, so the summary shown is the version the blockers beside it came from;
// one that keeps moving is said rather than shown against the wrong blockers.
func TestAReportRewrittenWhileItIsReadIsReadAgain(t *testing.T) {
	t.Parallel()

	first := blockedBy("factory-pgm")
	first.Report.Summary = "first"
	second := blockedBy("factory-pgm")
	second.Report.Summary, second.Version, second.RecordedAt = "second", 4, first.RecordedAt.Add(time.Minute)

	sources := programManagerSources()
	reads := 0
	// The derivation reads the first, the text the second, and the retry reads
	// the second twice.
	sources.LaneReports = rewritingLaneReports{versions: []runstate.LaneReport{first, second}, reads: &reads}
	answer, err := ReadProgramManagerReport(sources, "factory-pgm")
	if err != nil || answer.Report == nil || answer.Report.Summary != "second" || !answer.Instance.ReportWrittenAt.Equal(second.RecordedAt) {
		t.Fatalf("ReadProgramManagerReport() = %+v, %v; want the second version read whole", answer, err)
	}

	third := blockedBy("factory-pgm")
	third.Version, third.RecordedAt = 5, second.RecordedAt.Add(time.Minute)
	fourth := blockedBy("factory-pgm")
	fourth.Version, fourth.RecordedAt = 6, third.RecordedAt.Add(time.Minute)
	reads = 0
	sources.LaneReports = rewritingLaneReports{versions: []runstate.LaneReport{first, second, third, fourth}, reads: &reads}
	answer, err = ReadProgramManagerReport(sources, "factory-pgm")
	if err != nil || answer.Report != nil || !strings.Contains(answer.Problem, "rewritten twice while it was being read") {
		t.Fatalf("ReadProgramManagerReport() = %+v, %v; want no text and the reason", answer, err)
	}
}

// A missed pass recorded since the instance's last completed pass is carried
// on the instance and said on its line, naming the trigger, the cause, and the
// harness as the mover, under whatever word the instance reads; a pass that
// completes after it clears it.
func TestAnInstancesMissedPassIsOnItsLineUntilAPassCompletes(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	cancelled := runstate.Sweep{
		Task: "factory-pgm", Role: domain.RoleProgramManager,
		StartedAt: moment.Add(-20 * time.Minute), EndedAt: moment.Add(-5 * time.Minute),
		Problem: "the scheduled pass of the program manager instance factory-pgm was cancelled before it completed: the watch session carrying it stopped",
		Missed:  &runstate.MissedPass{Trigger: runstate.PassTriggerSchedule, How: runstate.MissCancelled},
	}
	// A recurring task's miss is not an instance's.
	task := runstate.Sweep{
		Task: "writing-pgm", Role: domain.RoleDevelopmentManager,
		StartedAt: moment.Add(-20 * time.Minute), EndedAt: moment, Problem: "missed",
		Missed: &runstate.MissedPass{Trigger: runstate.PassTriggerSchedule, How: runstate.MissUnfired},
	}
	sources.Passes = fakePasses{passes: []runstate.Sweep{
		completedPass(factoryConversation, moment.Add(-30*time.Minute)),
		completedPass(writingConversation, moment.Add(-30*time.Minute)),
		cancelled, task,
	}}

	factory := instanceNamed(t, sources, "factory-pgm")
	if factory.MissedPass == nil || factory.MissedPass.How != runstate.MissCancelled || factory.MissedPass.Trigger != runstate.PassTriggerSchedule ||
		factory.MissedPass.WaitingOn != MoverHarness || !factory.MissedPass.At.Equal(moment.Add(-20*time.Minute)) {
		t.Fatalf("MissedPass = %+v, want the cancelled scheduled pass, the harness's to clear", factory.MissedPass)
	}
	if factory.Status != ProgramManagerWorking {
		t.Errorf("status = %s, want the word left to staleness", factory.Status)
	}
	if writing := instanceNamed(t, sources, "writing-pgm"); writing.MissedPass != nil {
		t.Errorf("writing-pgm MissedPass = %+v, want a recurring task's miss not read as an instance's", writing.MissedPass)
	}
	rendered := Standing{ProgramManagers: []ProgramManager{factory}}.RenderProgramManagers()
	for _, want := range []string{"factory-pgm — lane reliability — working: its scheduled pass was cancelled before it completed at 2026-08-30T11:40:00Z — the harness's — ", "the watch session carrying it stopped"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered = %q, want %q", rendered, want)
		}
	}
	encoded, err := json.Marshal(factory)
	// The words the line opens the miss with are carried too, so the dashboard
	// says the miss as the terminal does rather than wording the fields again.
	if err != nil || !strings.Contains(string(encoded), `"missed_pass":{"trigger":"schedule","how":"cancelled","what":"its scheduled pass was cancelled before it completed"`) {
		t.Errorf("json = %s, %v; want the miss carried under missed_pass with its words", encoded, err)
	}
	for _, how := range []struct {
		kind runstate.MissKind
		want string
	}{
		{runstate.MissUnfired, "missed its pass its events woke"},
		{runstate.MissConversationHeld, "missed its pass its events woke"},
		{runstate.MissCancelled, "its pass its events woke was cancelled before it completed"},
	} {
		if got := missedWhat(runstate.PassTriggerEvents, how.kind); got != how.want {
			t.Errorf("missedWhat(events, %s) = %q, want %q", how.kind, got, how.want)
		}
	}

	sources.Passes = fakePasses{passes: []runstate.Sweep{cancelled, completedPass(factoryConversation, moment.Add(-time.Minute))}}
	if cleared := instanceNamed(t, sources, "factory-pgm"); cleared.MissedPass != nil {
		t.Errorf("MissedPass = %+v, want a completed pass to clear it", cleared.MissedPass)
	}
}
