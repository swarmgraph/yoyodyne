package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func stopCauseBlock(payload string) string {
	return stopCauseFence + "\n" + payload + "\n```\n"
}

var checkLimit = runstate.StopCauseKey{Cause: runstate.CauseCheckTimeLimit.StopClass(), Side: runstate.StopSideBound, Step: runstate.PhaseChecking}

func handedStop(run string, at time.Time) runstate.StopEvent {
	return runstate.StopEvent{Run: run, StoppedAt: at, Item: "item-" + run, Cause: checkLimit}
}

func stopEntry(stop runstate.StopEvent) string {
	return `{"run":"` + stop.Run + `","stopped_at":"` + stop.StoppedAt.UTC().Format(time.RFC3339) + `"}`
}

// postMortemSession opens a program manager's conversation over a stop cause
// record and a report pile, answering with the replies given in order.
func postMortemSession(t *testing.T, root string, pile *fakeReports, replies ...string) (*Session, *runstate.StopCauseStore) {
	t.Helper()
	store, err := runstate.NewStopCauseStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStopCauseStore() error = %v", err)
	}
	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	options := testOptions(t, &fakeBackend{results: results})
	options.Role = domain.RoleProgramManager
	options.Agent = "factory-flow-pm"
	options.Store = newTestStore(t, root)
	options.Reports = pile
	options.StopCauses = store
	return openTestSession(t, options), store
}

// Only the stops the pass was handed are counted, each against the cause the
// harness read off its run record; one it was not handed is refused, and one
// already counted adds nothing when it is named again.
func TestAPostMortemCountsOnlyTheStopsItsPassWasHanded(t *testing.T) {
	t.Parallel()

	now := fixedClock{}.Now()
	a, b := handedStop("run-a", now.Add(-2*time.Hour)), handedStop("run-b", now.Add(-time.Hour))
	block := stopCauseBlock(`{"stops":[` + stopEntry(a) + `,` + stopEntry(b) + `,{"run":"run-z","stopped_at":"2026-08-15T09:00:00Z"}]}`)
	session, store := postMortemSession(t, t.TempDir(), &fakeReports{}, "Two runs stopped at their checks.\n\n"+block, block)
	session.ForPass("pass-1")
	session.HandStops([]runstate.StopEvent{a, b})

	reply, err := session.Send(context.Background(), "pass")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	outcome := reply.StopCauses
	if outcome == nil || outcome.Failure != "" || outcome.Counted != 2 {
		t.Fatalf("reply.StopCauses = %+v, want the two handed stops counted", outcome)
	}
	if len(outcome.Refused) != 1 || !strings.Contains(outcome.Refused[0], "run-z") {
		t.Errorf("refused = %v, want the stop the pass was not handed refused", outcome.Refused)
	}
	account, err := store.Account("factory-flow-pm")
	if err != nil {
		t.Fatal(err)
	}
	if tally, found := account.Cause(checkLimit.ID()); !found || tally.Count() != 2 {
		t.Fatalf("account = %+v, want one cause counted twice", account.Causes)
	}
	if !strings.Contains(strings.Join(outcome.Digest, "\n"), checkLimit.ID()+" — 2 stop(s)") {
		t.Errorf("digest = %v, want the cause's count", outcome.Digest)
	}

	// The same pass run again, handed the same stops, counts nothing more.
	session.ForPass("pass-1")
	session.HandStops([]runstate.StopEvent{a, b})
	again, err := session.Send(context.Background(), "pass again")
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if again.StopCauses == nil || again.StopCauses.Counted != 0 || again.StopCauses.Already != 2 {
		t.Errorf("replayed pass = %+v, want nothing newly counted", again.StopCauses)
	}
	if account, _ := store.Account("factory-flow-pm"); account.Causes[0].Count() != 2 {
		t.Errorf("count after the replay = %d, want 2", account.Causes[0].Count())
	}
}

// A cause's first report is filed by the harness at warning with the count and
// the runs, and attached to the cause; a second while it stands is refused and
// the digest carries the count against the first.
func TestACauseGetsOneReportAndRecurrencesAreCarriedInTheDigest(t *testing.T) {
	t.Parallel()

	now := fixedClock{}.Now()
	a, b, c := handedStop("run-a", now.Add(-3*time.Hour)), handedStop("run-b", now.Add(-2*time.Hour)), handedStop("run-c", now.Add(-time.Hour))
	report1 := `{"cause":"` + checkLimit.ID() + `","fault":"bound","remedy":"Raise the check time limit to twenty minutes.","account":"The full test suite takes eleven minutes and the limit is ten."}`
	pile := &fakeReports{}
	session, store := postMortemSession(t, t.TempDir(), pile,
		stopCauseBlock(`{"stops":[`+stopEntry(a)+`,`+stopEntry(b)+`],"reports":[`+report1+`]}`),
		stopCauseBlock(`{"stops":[`+stopEntry(c)+`],"reports":[`+report1+`]}`),
	)
	session.ForPass("pass-1")
	session.HandStops([]runstate.StopEvent{a, b})
	first, err := session.Send(context.Background(), "pass one")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if first.StopCauses == nil || len(first.StopCauses.Filed) != 1 {
		t.Fatalf("pass one = %+v, want one report filed", first.StopCauses)
	}
	if len(pile.appended) != 1 {
		t.Fatalf("pile = %d reports, want 1", len(pile.appended))
	}
	filed := pile.appended[0]
	if filed.Severity != report.SeverityWarning || filed.ID != first.StopCauses.Filed[0].Report {
		t.Errorf("filed = %+v, want the cause's report at warning", filed)
	}
	for _, want := range []string{"2 run(s) stopped because a check reached its own time limit", "at the checks step, which is a bound on the work", "run-a, run-b", "The bound was at fault", "eleven minutes", "Raise the check time limit"} {
		if !strings.Contains(filed.Message, want) {
			t.Errorf("the report does not say %q:\n%s", want, filed.Message)
		}
	}
	// The report is read by a person and names the cause in words only; the
	// cause's identifier stays in what the program manager is told.
	if strings.Contains(filed.Message, checkLimit.ID()) || strings.Contains(filed.Message, "recorded as") {
		t.Errorf("the report names the cause's identifier %q:\n%s", checkLimit.ID(), filed.Message)
	}
	if told := renderStopCauseResult(*first.StopCauses); !strings.Contains(told, "about "+checkLimit.ID()) {
		t.Errorf("the program manager's result does not name the cause %q:\n%s", checkLimit.ID(), told)
	}
	if !strings.Contains(strings.Join(first.StopCauses.Digest, "\n"), checkLimit.ID()+" — 2 stop(s)") {
		t.Errorf("digest = %v, want the cause's identifier with its count", first.StopCauses.Digest)
	}
	if len(first.Reports) != 1 {
		t.Errorf("reply.Reports = %d, want the post-mortem carried as the reply's report", len(first.Reports))
	}
	original := filed.Message

	session.ForPass("pass-2")
	session.HandStops([]runstate.StopEvent{c})
	second, err := session.Send(context.Background(), "pass two")
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if second.StopCauses == nil || len(second.StopCauses.Filed) != 0 || len(second.StopCauses.Refused) != 1 {
		t.Fatalf("pass two = %+v, want the second report refused", second.StopCauses)
	}
	if len(pile.appended) != 1 || pile.appended[0].Message != original {
		t.Errorf("the pile changed: %+v", pile.appended)
	}
	digest := strings.Join(second.StopCauses.Digest, "\n")
	if !strings.Contains(digest, "3 stop(s)") || !strings.Contains(digest, filed.ID) {
		t.Errorf("digest = %q, want the count of three against the first report", digest)
	}
	account, _ := store.Account("factory-flow-pm")
	tally, _ := account.Cause(checkLimit.ID())
	if current, _ := tally.Current(); current.Report != filed.ID || len(tally.Reports) != 1 {
		t.Errorf("reports attached = %+v, want only the first", tally.Reports)
	}
}

// Once the Lead Product Manager has handled the report, the cause counts on;
// a new report naming the handled one is taken at the recurrence count, and at
// once for a stop of priority 0 work.
func TestAHandledCauseIsReportedAgainAtTheRecurrenceCountOrOnTopPriorityWork(t *testing.T) {
	t.Parallel()

	now := fixedClock{}.Now()
	handledAt := now.Add(-5 * time.Hour)
	earlier := handedStop("run-a", handledAt.Add(-time.Hour))
	after := []runstate.StopEvent{handedStop("run-b", handledAt.Add(time.Hour)), handedStop("run-c", handledAt.Add(2*time.Hour)), handedStop("run-d", handledAt.Add(3*time.Hour))}
	zero := 0
	urgent := handedStop("run-p0", handledAt.Add(4*time.Hour))
	urgent.Priority = &zero
	again := `{"cause":"` + checkLimit.ID() + `","fault":"bound","remedy":"Raise the limit further.","account":"It keeps happening."}`

	for _, test := range []struct {
		name      string
		stops     []runstate.StopEvent
		threshold int
		filed     bool
	}{
		{name: "two since handling, below the count", stops: after[:2], filed: false},
		{name: "three since handling, at the count", stops: after, filed: true},
		{name: "three since handling against a count of four", stops: after, threshold: 4, filed: false},
		{name: "one stop of priority 0 work", stops: []runstate.StopEvent{urgent}, filed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			pile := &fakeReports{}
			names := make([]string, 0, len(test.stops))
			for _, stop := range test.stops {
				names = append(names, stopEntry(stop))
			}
			session, store := postMortemSession(t, root, pile,
				stopCauseBlock(`{"stops":[`+strings.Join(names, ",")+`],"reports":[`+again+`]}`))
			session.options.StopCauseRecurrence = test.threshold
			if _, err := store.Record(context.Background(), "factory-flow-pm", handledAt, func(account runstate.StopCauseAccount) ([]runstate.StopCauseEntry, error) {
				entries := account.StopEntries([]runstate.StopEvent{earlier})
				return append(entries, runstate.StopCauseEntry{Kind: runstate.StopCauseReport, Cause: checkLimit, Report: "report-00000000000000000000000000000001", Fault: runstate.StopFaultBound, Remedy: "raise the limit"}), nil
			}); err != nil {
				t.Fatal(err)
			}
			pile.handled = []report.Handling{{ReportID: "report-00000000000000000000000000000001", RecordedAt: handledAt}}
			session.HandStops(test.stops)

			reply, err := session.Send(context.Background(), "pass")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if got := len(reply.StopCauses.Filed) == 1; got != test.filed {
				t.Fatalf("filed = %v (%+v), want %v", got, reply.StopCauses, test.filed)
			}
			if !test.filed {
				return
			}
			if reply.StopCauses.Filed[0].Follows != "report-00000000000000000000000000000001" {
				t.Errorf("the new report follows %q, want the handled one", reply.StopCauses.Filed[0].Follows)
			}
			if !strings.Contains(pile.appended[0].Message, "reported before as report-00000000000000000000000000000001") {
				t.Errorf("the new report does not name the handled one:\n%s", pile.appended[0].Message)
			}
		})
	}
}

// A correction is a note kept beside the report it corrects; nothing filed is
// changed, and a note against a report no cause carries is refused.
func TestACorrectionToAPostMortemIsANote(t *testing.T) {
	t.Parallel()

	now := fixedClock{}.Now()
	pile := &fakeReports{}
	session, store := postMortemSession(t, t.TempDir(), pile,
		stopCauseBlock(`{"notes":[{"report":"report-00000000000000000000000000000001","note":"The tracker was slow too."},{"report":"report-00000000000000000000000000000009","note":"not attached"}]}`))
	if _, err := store.Record(context.Background(), "factory-flow-pm", now, func(account runstate.StopCauseAccount) ([]runstate.StopCauseEntry, error) {
		entries := account.StopEntries([]runstate.StopEvent{handedStop("run-a", now.Add(-time.Hour))})
		return append(entries, runstate.StopCauseEntry{Kind: runstate.StopCauseReport, Cause: checkLimit, Report: "report-00000000000000000000000000000001", Fault: runstate.StopFaultBound, Remedy: "raise the limit"}), nil
	}); err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "correct it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.StopCauses.Noted != 1 || len(reply.StopCauses.Refused) != 1 || len(pile.appended) != 0 {
		t.Fatalf("outcome = %+v, pile %d, want one note, one refusal, and nothing filed", reply.StopCauses, len(pile.appended))
	}
	account, _ := store.Account("factory-flow-pm")
	tally, _ := account.Cause(checkLimit.ID())
	if current, _ := tally.Current(); len(current.Notes) != 1 || current.Remedy != "raise the limit" {
		t.Errorf("current = %+v, want the report unchanged with the note beside it", current)
	}
}

// A role that does not post-mortem stopped runs has the block refused whole.
func TestAStopCausesBlockFromAnotherRoleIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := runstate.NewStopCauseStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: stopCauseBlock(`{"stops":[{"run":"run-a","stopped_at":"2026-08-15T09:00:00Z"}]}`)}}})
	options.Store = newTestStore(t, root)
	options.StopCauses = store
	session := openTestSession(t, options)
	_, _ = session.Send(context.Background(), "count it")
	if entries, err := store.List(options.Agent); err != nil || len(entries) != 0 {
		t.Errorf("entries = %v, %v; want nothing recorded for a role without the grant", entries, err)
	}
}
