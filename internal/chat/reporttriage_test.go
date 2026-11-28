package chat

// What becomes of a report once somebody has filed one.
//
// The pile used to reach triage only through a person: the product manager
// could not read it, so a report was acted on when an operator happened to read
// the channel and repeated one into a conversation. These tests hold the two
// halves that replaced that — the unhandled pile arriving in the turn of the
// role that decides about it, and the decision being written down where the
// next reader can see it.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAProductPassHandlingRequiresAPermittedPersonOnlyRemedy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, extra string
		applied     bool
	}{
		{name: "ordinary repair", extra: `,"needs":"operator"`},
		{name: "unregistered reason", extra: `,"needs":"operator","person_only":{"reason":"repair","target":"maintenance","step":"repair the harness"}`},
		{name: "role-owned file", extra: `,"needs":"operator","person_only":{"reason":"protected-file","target":"internal/maintain/maintain.go","step":"repair the harness"}`},
		{name: "protected file", extra: `,"needs":"operator","person_only":{"reason":"protected-file","target":".claude/settings.json","step":"add the notes-writer hook to .claude/settings.json by hand"}`, applied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reports := &fakeReports{}
			subject := collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "the maintenance pass needs repairing", 2)
			subject.Role, subject.PassFailureTask = report.HarnessReporter, "maintenance"
			seedReports(t, reports, subject)
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Dealing with the pass.", `{"action":"handle","report":"`+subject.ID+`","reason":"the development manager is resolving the cause"`+tc.extra+`}`)},
				{SessionID: "session-1", FinalText: "Recorded."},
			}}
			options := testOptions(t, provider)
			options.Reports, options.Tracker = reports, &fakeTracker{}
			writeRepositoryFile(t, options.Repository, ".claude/settings.json")
			session, err := Open(options)
			if err != nil {
				t.Fatal(err)
			}
			reply, err := session.Send(context.Background(), "deal with the maintenance finding")
			if err != nil {
				t.Fatal(err)
			}
			if !tc.applied {
				if len(reports.handled) != 0 || len(reply.Actions) > 0 && reply.Actions[0].Applied {
					t.Fatalf("ordinary repair was handed to the operator: %+v, %+v", reports.handled, reply.Actions)
				}
				if len(reply.HandedBack) == 0 && (len(reply.Actions) == 0 || reply.Actions[0].Failure == "") {
					t.Fatalf("handling was not explicitly refused: %+v", reply)
				}
				return
			}
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(reports.handled) != 1 {
				t.Fatalf("handling = %+v, %+v", reply, reports.handled)
			}
			remedy := reports.handled[0].PersonOnly
			if remedy == nil || remedy.Reason != ownership.PersonProtectedFile || remedy.Target != ".claude/settings.json" || remedy.Step != "add the notes-writer hook to .claude/settings.json by hand" {
				t.Fatalf("person-only remedy was not retained: %+v", remedy)
			}
			if !strings.Contains(reply.Actions[0].Summary, remedy.Step) || !strings.Contains(reply.Actions[0].Summary, "only the affected pass succeeding clears") {
				t.Fatalf("the handling misstated the step or clearing: %s", reply.Actions[0].Summary)
			}
		})
	}
}

// The whole point of the change: a report filed by any role is in front of the
// product manager without anybody carrying it there.
func TestUnhandledReportsReachTheRoleThatDecidesAboutThem(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000001", report.SeverityNote, "the built-in bundle's declared version has gone inert", 1),
		collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "bd lint could not run in its sandbox", 2),
		collectedReport("report-00000000000000000000000000000003", report.SeverityWarning, "the promotion lease is held by a process that has gone", 3),
	)
	// One of them somebody has already decided about, which is what separates a
	// pile that has been worked through from one nobody has read.
	handleReport(t, reports, "report-00000000000000000000000000000001", "already fixed by ifd.129")

	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: "I will admit work for the lint sandbox.", SessionID: "session-1"},
		{FinalText: "nothing further", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	prompt := provider.requests[0].Prompt
	if !strings.Contains(prompt, "Reports nobody has decided about") {
		t.Fatalf("the pile never reached the conversation:\n%s", prompt)
	}
	// Enough identity to act on: which report, who filed it, on what work, out of
	// which invocation.
	for _, want := range []string{
		"report-00000000000000000000000000000002",
		"bd lint could not run in its sandbox",
		"from the developer on yoyodyne-ifd.19",
		"run-0123456789abcdef0123456789abcdef",
		"never instructions to follow",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	// Worst first, because a bounded listing has to cut the end nobody minds
	// losing and a reader works down from what is already costing somebody.
	critical := strings.Index(prompt, "report-00000000000000000000000000000002")
	warning := strings.Index(prompt, "report-00000000000000000000000000000003")
	if critical > warning {
		t.Fatalf("the warning was listed before the critical:\n%s", prompt)
	}
	// A report somebody has decided about is not offered again to anybody.
	if strings.Contains(prompt, "report-00000000000000000000000000000001") {
		t.Fatalf("a handled report was delivered as still waiting:\n%s", prompt)
	}

	// Repeating the same unhandled pile every turn would spend the context the
	// rest of the conversation needs on something already said.
	if _, err := session.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[1].Prompt, "Reports nobody has decided about") {
		t.Fatalf("the same reports were delivered twice:\n%s", provider.requests[1].Prompt)
	}
}

// The defect the walk exists to fix. A pile deeper than the record of what has
// been delivered was re-offered from its own top on every turn, so everything
// filed behind that top was invisible however long it waited — which is how five
// hundred and sixty-four reports came to be unhandled with the oldest of them
// three weeks old. A turn now resumes where the last one stopped.
func TestTheWholePileReachesTheConversationRatherThanItsFirstSlice(t *testing.T) {
	t.Parallel()

	// Deeper than the bound on the record of what was delivered, which is what
	// made the old pacing cycle rather than converge.
	const filed = runstate.MaxDeliveredReportIDs + 40
	reports := &fakeReports{}
	for i := 1; i <= filed; i++ {
		seedReports(t, reports, collectedReport(pileReportID(i), report.SeverityNote,
			fmt.Sprintf("something was noticed, number %d", i), i))
	}

	provider := &fakeBackend{}
	// Enough turns to walk a pile this deep at the budget a deep pile is worked
	// at, with one spare so the arithmetic does not have to be exact.
	turns := filed/maxDrainedReports + 2
	for i := 0; i < turns; i++ {
		provider.results = append(provider.results, backendapi.RunResult{FinalText: "noted", SessionID: "session-1"})
	}
	options := testOptions(t, provider)
	options.Reports = reports
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	offered := map[string]bool{}
	for i := 0; i < turns; i++ {
		if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		for number := 1; number <= filed; number++ {
			if strings.Contains(provider.requests[i].Prompt, pileReportID(number)) {
				offered[pileReportID(number)] = true
			}
		}
	}
	if len(offered) != filed {
		t.Fatalf("%d of %d reports were ever offered; the walk does not reach the whole pile", len(offered), filed)
	}
	// The oldest is offered first, which is what makes the age of the oldest thing
	// nobody has decided about fall rather than climb.
	if !strings.Contains(provider.requests[0].Prompt, pileReportID(1)) {
		t.Fatalf("the oldest report was not in the first turn:\n%s", provider.requests[0].Prompt)
	}
	// And the pile's own depth is stated rather than left to be inferred from what
	// one turn happened to carry.
	if !strings.Contains(provider.requests[0].Prompt, fmt.Sprintf("%d further report(s) are unhandled", filed-maxDrainedReports)) {
		t.Fatalf("the turn did not say how deep the pile is:\n%s", provider.requests[0].Prompt)
	}
}

// pileReportID names one report in a seeded pile by where it sits in it.
func pileReportID(number int) string {
	return fmt.Sprintf("report-%032x", number)
}

// Delivering the pile to a role that cannot record what became of a report gives
// it something to read past every turn, which is how a channel stops being read.
func TestThePileIsDeliveredOnlyToTheRoleThatCanDecideAboutIt(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "bd lint could not run in its sandbox", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "it looks bounded to me", SessionID: "session-1"}}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDeveloper
	options.Agent = string(domain.RoleDeveloper)
	options.Reports = reports
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "is ifd.19 bounded enough?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, "Reports nobody has decided about") {
		t.Fatalf("the pile reached a role that cannot decide about it:\n%s", provider.requests[0].Prompt)
	}
}

// A conversation outlives the process holding it, so what it has already shown
// has to outlive that process too — otherwise every restart re-delivers the
// whole unhandled pile.
func TestAResumedConversationDoesNotOfferReportsItAlreadyShowed(t *testing.T) {
	t.Parallel()

	store := newTestStore(t, t.TempDir())
	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "bd lint could not run in its sandbox", 2),
	)

	first := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	options := testOptions(t, first)
	options.Store = store
	options.Reports = reports
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(first.requests[0].Prompt, "Reports nobody has decided about") {
		t.Fatalf("the pile never reached the conversation:\n%s", first.requests[0].Prompt)
	}
	recorded, err := store.Load(runstate.ConversationIdentity{Agent: string(domain.RoleProductManager), Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.DeliveredReportIDs) != 1 {
		t.Fatalf("what was delivered was not recorded: %#v", recorded.DeliveredReportIDs)
	}

	second := &fakeBackend{results: []backendapi.RunResult{{FinalText: "still nothing", SessionID: "session-1"}}}
	resumedOptions := testOptions(t, second)
	resumedOptions.Store = store
	resumedOptions.Reports = reports
	resumed, err := Open(resumedOptions)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := resumed.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(second.requests[0].Prompt, "Reports nobody has decided about") {
		t.Fatalf("a resumed process delivered what an earlier one already said:\n%s", second.requests[0].Prompt)
	}
}

// One long-lived conversation is shown a report and leaves it undecided. Its
// ordinary turns do not offer it again, its closing line says so and names it
// rather than promising it on later turns, and the start of its next recurring
// pass offers it again.
func TestAReportShownAndLeftUndecidedIsOfferedAgainAtTheNextPass(t *testing.T) {
	t.Parallel()

	// Two more than a shallow pile's budget, so the first turn has more to offer
	// and the second has only what it was already shown behind it.
	const filed = maxDeliveredReports + 2
	reports := &fakeReports{}
	for i := 1; i <= filed; i++ {
		seedReports(t, reports, collectedReport(pileReportID(i), report.SeverityNote,
			fmt.Sprintf("something was noticed, number %d", i), i))
	}
	provider := &fakeBackend{}
	for i := 0; i < 5; i++ {
		provider.results = append(provider.results, backendapi.RunResult{FinalText: "noted", SessionID: "session-1"})
	}
	options := testOptions(t, provider)
	options.Reports = reports
	memories, err := runstate.NewMemoryStore(t.TempDir(), options.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	options.Memories = memories
	session := openTestSession(t, options)
	ctx := context.Background()

	session.ForPass("product-manager-pass#1")
	if _, err := session.Send(ctx, "work the pile"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	first := provider.requests[0].Prompt
	if !strings.Contains(first, pileReportID(1)) || strings.Contains(first, pileReportID(filed)) {
		t.Fatalf("the pass's first turn did not carry the oldest slice:\n%s", first)
	}
	if !strings.Contains(first, "2 further report(s) are unhandled and have not been shown to this conversation yet") {
		t.Fatalf("the first turn did not count what is still to be offered:\n%s", first)
	}

	// The pass's next turn carries the rest, and its closing line says the ten
	// shown before are undecided, by identifier, and promises nothing more.
	if _, err := session.Send(ctx, "carry on"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	second := provider.requests[1].Prompt
	if !strings.Contains(second, pileReportID(filed)) {
		t.Fatalf("the second turn did not carry the rest of the pile:\n%s", second)
	}
	if strings.Contains(second, "further report(s)") {
		t.Fatalf("the second turn said more is coming when nothing is left to offer:\n%s", second)
	}
	if !strings.Contains(second, fmt.Sprintf("%d report(s) shown to this conversation before are still undecided", maxDeliveredReports)) ||
		!strings.Contains(second, pileReportID(1)+", ") {
		t.Fatalf("the second turn did not count and name the reports left undecided:\n%s", second)
	}

	// The turn after that carries no reports, which is what the line above told it.
	if _, err := session.Send(ctx, "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[2].Prompt, reportSectionHeading) {
		t.Fatalf("an ordinary turn offered again what it was already shown:\n%s", provider.requests[2].Prompt)
	}
	if err := session.EndPass(); err != nil {
		t.Fatal(err)
	}

	// One report is decided between passes; the next pass offers every other one again.
	handleReport(t, reports, pileReportID(3), "already fixed")
	session.ForPass("product-manager-pass#2")
	if _, err := session.Send(ctx, "work the pile"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	next := provider.requests[3].Prompt
	if strings.Contains(next, pileReportID(3)) {
		t.Fatalf("the next pass offered a report somebody decided about:\n%s", next)
	}
	for _, number := range []int{1, 2, 4, maxDeliveredReports + 1} {
		if !strings.Contains(next, pileReportID(number)) {
			t.Fatalf("the next pass did not offer %s again:\n%s", pileReportID(number), next)
		}
	}
	if !strings.Contains(next, "1 report(s) shown to this conversation before are still undecided and are not listed here: "+pileReportID(filed)+". ") {
		t.Fatalf("the next pass did not name the one that did not fit:\n%s", next)
	}
	if err := session.EndPass(); err != nil {
		t.Fatal(err)
	}
}

// Recording what became of a report is what takes it out of the pile, and it is
// the only thing that does: a report that was merely read comes back.
func TestHandlingARecordedReportIsWrittenDownBesideThePile(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "bd lint could not run in its sandbox", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{
			SessionID: "session-1",
			FinalText: trackerReply("I have admitted work for it.",
				`{"action":"handle","report":"report-00000000000000000000000000000002","reason":"admitted as yoyodyne-ifd.150"}`),
		},
		{SessionID: "session-1", FinalText: "Recorded."},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "deal with the lint report")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "report-00000000000000000000000000000002") ||
		!strings.Contains(reply.Actions[0].Summary, "developer") {
		t.Fatalf("summary = %q", reply.Actions[0].Summary)
	}
	if len(reports.handled) != 1 {
		t.Fatalf("handlings = %#v", reports.handled)
	}
	handling := reports.handled[0]
	if handling.ReportID != "report-00000000000000000000000000000002" ||
		handling.Role != domain.RoleProductManager ||
		handling.Reason != "admitted as yoyodyne-ifd.150" ||
		handling.RunID != session.state.ConversationID {
		t.Fatalf("handling = %#v", handling)
	}
	// The report itself is untouched. What makes the pile evidence is that
	// nothing written later can edit it.
	if len(reports.appended) != 1 || reports.appended[0].Message != "bd lint could not run in its sandbox" {
		t.Fatalf("the pile was changed: %#v", reports.appended)
	}
}

// A handling that says the report needs the operator is recorded as a finding
// for him rather than as a closing: the record carries it as a field the read
// model can act on, and the summary says what that means. Any other value of
// "needs" is refused whole, and nothing is recorded.
func TestHandlingAReportAsNeedingTheOperatorIsRecordedAsAFinding(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "the goals guard hook has to be added to .claude/settings.json by hand", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{
			SessionID: "session-1",
			FinalText: trackerReply("Only Mason can add that hook.",
				`{"action":"handle","report":"report-00000000000000000000000000000002","needs":"operator","operator_reason":"protected-file","reason":"add the PreToolUse hook to .claude/settings.json; the harness may not write that file"}`),
		},
		{SessionID: "session-1", FinalText: "Recorded."},
		{SessionID: "session-1", FinalText: "Nothing more."},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	writeRepositoryFile(t, options.Repository, ".claude/settings.json")
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "deal with the hook report")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "needs the operator's hand") ||
		!strings.Contains(reply.Actions[0].Summary, "yoyo status") {
		t.Fatalf("summary = %q, want the handling said as a finding for the operator", reply.Actions[0].Summary)
	}
	if len(reports.handled) != 1 || !reports.handled[0].NeedsOperator ||
		reports.handled[0].Reason != "add the PreToolUse hook to .claude/settings.json; the harness may not write that file" {
		t.Fatalf("handlings = %#v, want one recorded as needing the operator", reports.handled)
	}

	// A handled report is not offered again, so the next turn lists the finding
	// still standing with its identifier: that is what lets the role record the
	// change made without being handed the id by a person.
	if _, err := session.Send(context.Background(), "anything else?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	next := provider.requests[len(provider.requests)-1].Prompt
	if strings.Contains(next, "Reports nobody has decided about") {
		t.Fatalf("a handled report was offered again as unhandled:\n%s", next)
	}
	for _, want := range []string{
		"Reports you handled as needing the operator's hand",
		"report-00000000000000000000000000000002 on yoyodyne-ifd.19: add the PreToolUse hook to .claude/settings.json; the harness may not write that file",
	} {
		if !strings.Contains(next, want) {
			t.Fatalf("the standing finding was not listed with its identifier; want %q in:\n%s", want, next)
		}
	}
}

// A handling this role recorded before the closed list was required, naming the
// operator with no reason from it, is listed to the role as its own to finish,
// and not as a finding standing with the operator.
func TestAHandlingNamingTheOperatorWithNoReasonIsListedAsTheRolesToFinish(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000002", report.SeverityWarning, "the flaky test needs looking at", 2),
	)
	reports.handled = []report.Handling{{
		SchemaVersion: report.HandlingSchemaVersion, ReportID: "report-00000000000000000000000000000002",
		Role: domain.RoleProductManager, RunID: "chat-old", Reason: "Mason should look at the flaky test",
		RecordedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), NeedsOperator: true,
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "anything waiting?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	prompt := provider.requests[len(provider.requests)-1].Prompt
	if !strings.Contains(prompt, "Reports you handled as the operator's that are yours to finish") ||
		!strings.Contains(prompt, "- report-00000000000000000000000000000002: Mason should look at the flaky test") {
		t.Fatalf("the returned handling was not listed as the role's to finish:\n%s", prompt)
	}
	if strings.Contains(prompt, "Reports you handled as needing the operator's hand") {
		t.Fatalf("the returned handling was listed as standing with the operator:\n%s", prompt)
	}
}

// An identifier is 32 hex characters copied out of a listing by a provider, so
// one that names nothing is a plausible mistake. A handling recorded against it
// would take no report out of anybody's view while reading as though it had.
func TestHandlingAReportThatIsNotInThePileRecordsNothing(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "bd lint could not run in its sandbox", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{
			SessionID: "session-1",
			FinalText: trackerReply("Dealt with.",
				`{"action":"handle","report":"report-0000000000000000000000000000dead","reason":"nothing to do"}`),
		},
		{SessionID: "session-1", FinalText: "It was refused, then."},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "deal with it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "no report in the pile is") {
		t.Fatalf("failure = %q", reply.Actions[0].Failure)
	}
	if len(reports.handled) != 0 {
		t.Fatalf("a handling was recorded for a report that does not exist: %#v", reports.handled)
	}
}

// The action's subject is a report rather than an item, and the two identifier
// spaces must not be confusable: an action naming both was misunderstood.
func TestHandleNamesAReportAndNeverAWorkItem(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action TrackerAction
		want   string
	}{
		{
			name:   "no report named",
			action: TrackerAction{Action: actionHandle, Reason: "dealt with"},
			want:   `handle requires "report"`,
		},
		{
			name:   "an identifier that is not a report",
			action: TrackerAction{Action: actionHandle, Report: "yoyodyne-ifd.19", Reason: "dealt with"},
			want:   "is not a report identifier",
		},
		{
			name:   "a work item as well",
			action: TrackerAction{Action: actionHandle, ID: "yoyodyne-ifd.19", Report: "report-00000000000000000000000000000002", Reason: "dealt with"},
			want:   "handle does not take an id",
		},
		{
			name:   "no reason",
			action: TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002"},
			want:   "reason is required",
		},
		{
			// "needs" names the one person a report can be handed to rather than
			// closed for, and any other value was misunderstood.
			name:   "needs somebody other than the operator",
			action: TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002", Needs: "architect", Reason: "dealt with"},
			want:   `"needs" is "architect"`,
		},
		{
			name:   "needs on an action that is not a handling",
			action: TrackerAction{Action: actionClose, ID: "yoyodyne-ifd.19", Needs: "operator", Reason: "done"},
			want:   `close does not take "needs"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.action.Validate()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
	valid := TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "admitted as yoyodyne-ifd.150"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() on a well-formed handling error = %v", err)
	}
}

// Deciding what becomes of any report is a product decision, so only the Lead
// Product Manager holds handling over the whole pile. The architect and the
// development manager hold it only over the reports addressed to them by name,
// and the other roles not at all.
func TestOnlyTheProductManagerMayRecordWhatBecameOfAnyReport(t *testing.T) {
	t.Parallel()

	addressedOnly := map[domain.AgentRole]bool{domain.RoleArchitect: true, domain.RoleDevelopmentManager: true}
	for _, role := range ConversationalRoles() {
		authority, known := AuthorityFor(role)
		if !known {
			t.Fatalf("no authority is recorded for %s", role)
		}
		wantHandle := role == domain.RoleProductManager || addressedOnly[role]
		if mayHandle := authority.MayAct(actionHandle); mayHandle != wantHandle {
			t.Fatalf("the %s may act on reports = %v, want %v", role, mayHandle, wantHandle)
		}
		if scoped := authority.AddressedOnly(actionHandle); scoped != addressedOnly[role] {
			t.Fatalf("the %s handles only reports addressed to it = %v, want %v", role, scoped, addressedOnly[role])
		}
	}

	provider := &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: trackerReply("Dealt with.",
			`{"action":"handle","report":"report-00000000000000000000000000000002","reason":"nothing to do"}`),
	}}}
	reports := &fakeReports{}
	options := testOptions(t, provider)
	options.Role = domain.RoleDeveloper
	options.Agent = string(domain.RoleDeveloper)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	_, err = session.Send(context.Background(), "deal with it")
	var refusal *AuthorityError
	if !errors.As(err, &refusal) {
		t.Fatalf("Send() error = %v, want an authority refusal", err)
	}
	if len(reports.handled) != 0 {
		t.Fatalf("a refused action still recorded a handling: %#v", reports.handled)
	}
}

// A role that handles the reports addressed to it records what became of one
// from its own conversation, with the requests mapped exactly as the Lead
// Product Manager's handling maps them, and the handling names that role. The
// report then leaves the role's list and the Lead Product Manager's pile. A
// report addressed to another role, or to none, is refused with nothing written,
// and the unaddressed pile is never delivered to such a role.
func TestARoleRecordsWhatBecameOfAReportAddressedToIt(t *testing.T) {
	t.Parallel()

	const (
		mine      = "report-00000000000000000000000000000011"
		theirs    = "report-00000000000000000000000000000012"
		nobodys   = "report-00000000000000000000000000000013"
		covering  = "yoyodyne-ifd.150"
		requested = "say which still need a move"
	)
	addressed := func(id string, to domain.AgentRole, minute int) report.Report {
		reported := collectedReport(id, report.SeverityWarning, "a report asking for a move", minute)
		reported.For = to
		return reported
	}
	reports := &fakeReports{}
	seedReports(t, reports,
		addressed(mine, domain.RoleDevelopmentManager, 1),
		addressed(theirs, domain.RoleArchitect, 2),
		collectedReport(nobodys, report.SeverityNote, "a report addressed to nobody", 3),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording what I did.",
			`{"action":"handle","report":"`+mine+`","requests":[{"request":"`+requested+`","covered_by":"`+covering+`"}],"reason":"answered on the pass; the item covers it"}`,
			`{"action":"handle","report":"`+theirs+`","reason":"not mine"}`,
			`{"action":"handle","report":"`+nobodys+`","reason":"not mine either"}`)},
		{SessionID: "session-1", FinalText: "Recorded."},
		{SessionID: "session-1", FinalText: "Nothing more."},
	}}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{covering: {ID: covering, Title: "say which reports still need a move", Status: "open"}}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Reports = reports
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "deal with what is addressed to you")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, reportSectionHeading) {
		t.Fatalf("the development manager was delivered the whole unhandled pile:\n%s", provider.requests[0].Prompt)
	}
	if len(reply.Actions) != 3 {
		t.Fatalf("actions = %#v, want three handlings", reply.Actions)
	}
	if !reply.Actions[0].Applied {
		t.Fatalf("the handling of a report addressed to the role = %#v, want it recorded", reply.Actions[0])
	}
	for i, want := range []string{"addressed to the architect", "addressed to no role"} {
		got := reply.Actions[i+1]
		if got.Applied || !strings.Contains(got.Failure, want) {
			t.Fatalf("handling %d = %#v, want it refused saying %q", i+1, got, want)
		}
	}
	if len(reports.handled) != 1 {
		t.Fatalf("handlings = %#v, want only the addressed one recorded", reports.handled)
	}
	recorded := reports.handled[0]
	if recorded.ReportID != mine || recorded.Role != domain.RoleDevelopmentManager {
		t.Fatalf("handling = %#v, want %s recorded under the development manager", recorded, mine)
	}
	if len(recorded.Requests) != 1 || recorded.Requests[0].CoveredBy != covering || recorded.Requests[0].Request != requested {
		t.Fatalf("handling requests = %#v, want the request mapped to %s", recorded.Requests, covering)
	}
	if !strings.Contains(recorded.Render(), "by the development-manager") {
		t.Fatalf("the handling does not name the role that recorded it:\n%s", recorded.Render())
	}
	pile, _ := reports.List()
	for _, waiting := range report.Unhandled(pile, reports.handled) {
		if waiting.ID == mine {
			t.Fatalf("%s is still unhandled after the role handled it", mine)
		}
	}
	if left := readmodel.ReportsAddressedTo(readmodel.MoverOf(domain.RoleDevelopmentManager), pile, reports.handled); len(left) != 0 {
		t.Fatalf("the development manager's list still holds %#v", left)
	}
}

// A missing capability is a defect for the Lead Product Manager to admit, and a
// report another role handles is never offered to her, so such a handling from
// another role is refused, and the report stays for her and for its addressee.
func TestARoleMayNotHandleAReportAddressedToItAsAMissingCapability(t *testing.T) {
	t.Parallel()

	const mine = "report-00000000000000000000000000000021"
	reported := collectedReport(mine, report.SeverityWarning, "somebody must read the ticket tracker", 1)
	reported.For = domain.RoleArchitect
	reports := &fakeReports{}
	seedReports(t, reports, reported)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording it.",
			`{"action":"handle","report":"`+mine+`","missing_capability":{"role":"architect","system":"the ticket tracker"},"reason":"no role can read it"}`)},
		{SessionID: "session-1", FinalText: "Understood."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleArchitect
	options.Agent = string(domain.RoleArchitect)
	options.Reports = reports
	options.Tracker = &fakeTracker{}
	reply, err := openTestSession(t, options).Send(context.Background(), "deal with what is addressed to you")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "addressed to the product-manager") {
		t.Fatalf("actions = %#v, want the handling refused naming the report to file", reply.Actions)
	}
	if len(reports.handled) != 0 {
		t.Fatalf("handlings = %#v, want none recorded", reports.handled)
	}
}

// The one thing the architect may do that touches a work item is the note a
// handling leaves on an item it names as covering a request. That note is all
// the handling writes there: no label, status, or other field, so no handling
// can take an item's needs-design label off.
func TestTheArchitectsHandlingWritesOnlyANoteOnACoveringItem(t *testing.T) {
	t.Parallel()

	authority, _ := AuthorityFor(domain.RoleArchitect)
	if len(authority.AddressedActions) != 1 || authority.AddressedActions[0] != actionHandle {
		t.Fatalf("the architect's actions on a report addressed to her = %v, want handling alone", authority.AddressedActions)
	}
	const (
		mine     = "report-00000000000000000000000000000031"
		covering = "yoyodyne-ifd.150"
	)
	reported := collectedReport(mine, report.SeverityWarning, "a report asking for a design", 1)
	reported.For = domain.RoleArchitect
	reports := &fakeReports{}
	seedReports(t, reports, reported)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Recording it.",
			`{"action":"handle","report":"`+mine+`","requests":[{"request":"a design","covered_by":"`+covering+`"}],"reason":"the item carries the design"}`)},
		{SessionID: "session-1", FinalText: "Recorded."},
	}}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{covering: {ID: covering, Title: "the design", Status: "open"}}}
	options := testOptions(t, provider)
	options.Role = domain.RoleArchitect
	options.Agent = string(domain.RoleArchitect)
	options.Reports = reports
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "deal with what is addressed to you")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v, want the handling recorded and one note", reply.Actions, tracker.updates)
	}
	change := tracker.updates[0].change
	if change.AppendNotes == "" || !reflect.DeepEqual(change, beads.WorkItemChange{AppendNotes: change.AppendNotes}) {
		t.Fatalf("change = %#v, want a note and nothing else", change)
	}
}

// The contract has to describe the pile and the action, or the role is handed
// evidence it has no idea what to do with.
func TestTheContractTellsTheProductManagerWhatToDoWithReports(t *testing.T) {
	t.Parallel()

	prompt := SystemPrompt(domain.RoleProductManager, Admission{}, nil, hostilePersona)
	for _, required := range []string{
		"Reports the other roles have filed",
		`{"action":"handle"`,
		`"report" is required on "handle"`,
		"offered again at the start of your next recurring pass",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the product-manager contract is missing %q", required)
		}
	}
	// A handling that maps requests notes on each item that answers one, so the
	// contract must not tell the role that handling changes nothing in the backlog.
	if strings.Contains(prompt, "nothing in the backlog changes") {
		t.Fatalf("the product-manager contract still says handling a report changes nothing in the backlog")
	}
}

// collectedReport is one report in the pile, distinguished only by what a test
// needs to tell them apart.
// A recurring pass reads which criticals its turn was shown, so it can refuse an
// account that ends complete over one: the turn that carried a critical ahead of
// the walk says so, and a later turn that carried none says nothing.
func TestATurnSaysWhichCriticalReportsItCarried(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport("report-00000000000000000000000000000001", report.SeverityNote, "a stale document", 1),
		collectedReport("report-00000000000000000000000000000002", report.SeverityCritical, "nothing is landing on main", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{FinalText: "looking", SessionID: "session-1"},
		{FinalText: "still looking", SessionID: "session-1"},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "work the pile"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if shown := session.CriticalReportsShown(); len(shown) != 1 || shown[0] != "report-00000000000000000000000000000002" {
		t.Fatalf("CriticalReportsShown() = %v, want the critical the turn carried", shown)
	}
	if _, err := session.Send(context.Background(), "carry on"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if shown := session.CriticalReportsShown(); len(shown) != 0 {
		t.Fatalf("CriticalReportsShown() = %v after a turn that carried none", shown)
	}
}

func collectedReport(id string, severity report.Severity, message string, minute int) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         string(domain.RoleDeveloper),
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.19",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       message,
		RecordedAt:    time.Date(2026, 8, 22, 9, minute, 0, 0, time.UTC),
	}
}

func seedReports(t *testing.T, pile *fakeReports, reports ...report.Report) {
	t.Helper()

	for _, reported := range reports {
		if err := pile.Append(reported); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
}

func handleReport(t *testing.T, pile *fakeReports, id, reason string) {
	t.Helper()

	if err := pile.Handle(report.Handling{
		SchemaVersion: report.HandlingSchemaVersion,
		ReportID:      id,
		Role:          domain.RoleProductManager,
		Agent:         string(domain.RoleProductManager),
		RunID:         "chat-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Reason:        reason,
		RecordedAt:    time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

// The hole this closes. A report asking for two things was handled as covered by
// the one item that did the first, and the second lapsed with nothing anybody
// could audit. So a handling that maps a request to nothing is refused, quoting
// the request, and one that answers every request — one covered by an existing
// item, one admitted in the same block — is recorded with the mapping on the
// handling and on the covering item.
func TestAReportHandledAsCoveredMapsEveryRequestToWhatAnswersIt(t *testing.T) {
	t.Parallel()

	const (
		reportID  = "report-00000000000000000000000000000002"
		decisions = "the docket consumes recorded decisions"
		closed    = "the docket consumes closed status"
		admitted  = "The docket retires entries whose item has closed"
	)
	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport(reportID, report.SeverityWarning, "The docket should consume recorded decisions and closed status rather than re-presenting them.", 2),
	)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.269": {ID: "yoyodyne-ifd.269", Title: "The docket consumes recorded decisions", Status: "closed"},
	}}

	// Covered by one item, with the second request answered by nothing.
	refusedProvider := &fakeBackend{results: []backendapi.RunResult{
		{
			SessionID: "session-1",
			FinalText: trackerReply("Covered by ifd.269.",
				`{"action":"handle","report":"`+reportID+`","requests":[{"request":"`+decisions+`","covered_by":"yoyodyne-ifd.269"},{"request":"`+closed+`"}],"reason":"covered by yoyodyne-ifd.269"}`),
		},
		// The round the refusal is handed back in.
		{SessionID: "session-1", FinalText: "I will map the second request."},
	}}
	refusedOptions := testOptions(t, refusedProvider)
	refusedOptions.Reports = reports
	refusedOptions.Tracker = tracker
	_, _ = openTestSession(t, refusedOptions).Send(context.Background(), "handle the docket report")
	if len(refusedProvider.requests) < 2 {
		t.Fatalf("the refused block was not handed back: %d provider call(s)", len(refusedProvider.requests))
	}
	if handedBack := refusedProvider.requests[1].Prompt; !strings.Contains(handedBack, "was refused") ||
		!strings.Contains(handedBack, `request \"`+closed+`\" is covered by no item`) &&
			!strings.Contains(handedBack, `request "`+closed+`" is covered by no item`) {
		t.Fatalf("the refusal does not quote the request nothing covers:\n%s", handedBack)
	}
	if len(reports.handled) != 0 || len(tracker.updates) != 0 {
		t.Fatalf("a refused handling wrote something: handlings %#v, updates %#v", reports.handled, tracker.updates)
	}

	// The same item for the first request, and the second admitted in the block.
	provider := &fakeBackend{results: []backendapi.RunResult{
		{
			SessionID: "session-1",
			FinalText: trackerReply("Covered in part; admitting the rest.",
				`{"action":"create","kind":"feature","title":"`+admitted+`","description":"Closed items leave the docket.","goal":"Run development nearly autonomously.","report":"`+reportID+`","reason":"the closed-status half of the report is covered by nothing"}`,
				`{"action":"handle","report":"`+reportID+`","requests":[{"request":"`+decisions+`","covered_by":"yoyodyne-ifd.269"},{"request":"`+closed+`","admitted":"`+admitted+`"}],"reason":"covered in part by yoyodyne-ifd.269; the rest admitted"}`),
		},
		{SessionID: "session-1", FinalText: "Recorded."},
	}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.Tracker = tracker
	options.Goals = recordedGoals("Run development nearly autonomously.")
	reply, err := openTestSession(t, options).Send(context.Background(), "handle the docket report")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || !reply.Actions[0].Applied || !reply.Actions[1].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	created := reply.Actions[0].WorkItemID

	// The handling record carries the mapping, the admission by the identifier it
	// was assigned rather than by its title.
	if len(reports.handled) != 1 {
		t.Fatalf("handlings = %#v", reports.handled)
	}
	want := []report.Request{
		{Request: decisions, CoveredBy: "yoyodyne-ifd.269"},
		{Request: closed, Admitted: created},
	}
	if got := reports.handled[0].Requests; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("recorded mapping = %#v, want %#v", got, want)
	}
	rendered := reports.handled[0].Render()
	for _, line := range []string{
		`request "` + decisions + `": covered by yoyodyne-ifd.269`,
		`request "` + closed + `": admitted as ` + created,
	} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("the listing does not print %q under the handled report:\n%s", line, rendered)
		}
	}

	// And the covering item's notes say which of the report's requests it covers,
	// and only those.
	var covering string
	for _, update := range tracker.updates {
		if update.id == "yoyodyne-ifd.269" {
			covering += update.change.AppendNotes
		}
	}
	if !strings.Contains(covering, reportID) || !strings.Contains(covering, `"`+decisions+`"`) {
		t.Fatalf("the covering item's notes do not carry the mapping:\n%s", covering)
	}
	if strings.Contains(covering, closed) {
		t.Fatalf("the covering item was noted as covering a request it does not:\n%s", covering)
	}
}

// A request said to be admitted names a creation the block carries before the
// handling, and a creation that did not happen leaves the report in the pile.
func TestARequestSaidToBeAdmittedNeedsTheAdmissionInTheSameBlock(t *testing.T) {
	t.Parallel()

	handle := TrackerAction{
		Action: actionHandle,
		Report: "report-00000000000000000000000000000002",
		Requests: []report.Request{
			{Request: "the docket consumes closed status", Admitted: "The docket retires closed entries"},
		},
		Reason: "admitted",
	}
	if problems := admissionsInBlockProblems([]TrackerAction{handle}); len(problems) != 1 ||
		!strings.Contains(problems[0].Error(), `no "create" before this handle`) {
		t.Fatalf("problems = %v, want the missing admission refused", problems)
	}
	create := TrackerAction{Action: actionCreate, Title: "The docket retires closed entries"}
	if problems := admissionsInBlockProblems([]TrackerAction{handle, create}); len(problems) != 1 {
		t.Fatalf("problems = %v, want an admission after the handling refused", problems)
	}
	if problems := admissionsInBlockProblems([]TrackerAction{create, handle}); len(problems) != 0 {
		t.Fatalf("problems = %v, want an admission before the handling accepted", problems)
	}
}

// The one-sentence handling is what lost the request, so a reason that says the
// report is covered by an item, with no mapping beside it, is refused.
func TestAHandlingThatSaysCoveredByInProseAloneIsRefused(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action TrackerAction
		want   string
	}{
		{
			name:   "covered by in prose only",
			action: TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "Covered by yoyodyne-ifd.269."},
			want:   "maps none of the report's requests",
		},
		{
			name: "two answers to one request",
			action: TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "mapped",
				Requests: []report.Request{{Request: "one thing", CoveredBy: "yoyodyne-ifd.269", Declined: "and also not"}}},
			want: `takes exactly one of`,
		},
		{
			name: "a covering item that is not an item",
			action: TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "mapped",
				Requests: []report.Request{{Request: "one thing", CoveredBy: "the docket work"}}},
			want: "covered_by",
		},
		{
			name: "a request mapped twice",
			action: TrackerAction{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "mapped",
				Requests: []report.Request{{Request: "one thing", CoveredBy: "yoyodyne-ifd.269"}, {Request: "one thing", Declined: "no"}}},
			want: "mapped twice",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.action.Validate()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
	// A handling that is not about coverage needs no mapping, and one that
	// declines every request it names is complete.
	for _, valid := range []TrackerAction{
		{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "already fixed by ifd.314"},
		{Action: actionHandle, Report: "report-00000000000000000000000000000002", Reason: "declined",
			Requests: []report.Request{{Request: "one thing", Declined: "not worth a run"}}},
	} {
		if err := valid.Validate(); err != nil {
			t.Fatalf("Validate() on %#v error = %v", valid, err)
		}
	}
}

// fakeEpicAnswers is the questions about scope a judgment made once more answered.
type fakeEpicAnswers struct {
	answered map[string]string
	err      error
}

func (f fakeEpicAnswers) AnsweredByRereading() (map[string]string, error) { return f.answered, f.err }

// A question about scope that a judgment made once more already answered is
// marked beside its report, so the Lead Product Manager records that nothing
// more is needed rather than asking the operator to add what he put in.
func TestAQuestionAboutScopeAnsweredByAJudgmentMadeOnceMoreIsMarked(t *testing.T) {
	t.Parallel()

	const settled, open = "report-00000000000000000000000000000004", "report-00000000000000000000000000000005"
	reports := &fakeReports{}
	seedReports(t, reports,
		collectedReport(settled, report.SeverityWarning, "A question about scope: yoyodyne-ep.1.1, made from yoyodyne-ep.1", 1),
		collectedReport(open, report.SeverityWarning, "A question about scope: yoyodyne-ep.1.2, made from yoyodyne-ep.1", 2),
	)
	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.EpicAnswers = fakeEpicAnswers{answered: map[string]string{settled: "This question about scope is already answered: yoyodyne-ep.1.1 joined."}}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	prompt := provider.requests[0].Prompt
	mark := strings.Index(prompt, "already answered: yoyodyne-ep.1.1 joined")
	if mark < 0 || mark < strings.Index(prompt, settled) || mark > strings.Index(prompt, open) || strings.Count(prompt, "already answered") != 1 {
		t.Fatalf("the answered question is not marked beside its own report alone:\n%s", prompt)
	}
}

func TestAnswersThatCannotBeReadAreSaidAndMarkNothing(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	seedReports(t, reports, collectedReport("report-00000000000000000000000000000006", report.SeverityWarning, "A question about scope", 1))
	provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
	options := testOptions(t, provider)
	options.Reports = reports
	options.EpicAnswers = fakeEpicAnswers{err: errors.New("the epics' progress is not whole")}
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if prompt := provider.requests[0].Prompt; !strings.Contains(prompt, "could not be read, so none is marked: the epics' progress is not whole") {
		t.Fatalf("the unread answers were not said:\n%s", prompt)
	}
}
