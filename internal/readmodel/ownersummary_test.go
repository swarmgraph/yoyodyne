package readmodel

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// summaryNow is a moment late enough in the local day that every moment the
// fixtures stamp an hour or two before it is still today.
func summaryNow() time.Time {
	local := time.Now().Local()
	return time.Date(local.Year(), local.Month(), local.Day(), 20, 0, 0, 0, time.Local)
}

// summaryFixture is one of each thing the five answers are made from: two
// code changes and a document landed today, runs in progress at each kind of
// evidence, an owed step seen three times, a group of items held on the
// development manager, a lapsed login that only the operator can fix, three
// runs stopped at the checks, and what today cost.
func summaryFixture(now time.Time) SummaryInputs {
	at := func(hours int) time.Time { return now.Add(-time.Duration(hours) * time.Hour) }
	checksPassedAt := at(1)

	running := []runstate.State{
		{RunID: "run-aaaaaaaa11111111", WorkItemID: "ex-1", WorkItemTitle: "Retry the merge", Status: runstate.StatusRunning, Phase: runstate.PhaseReviewing, StartedAt: at(3), ChecksPassed: &runstate.ChecksPassed{Content: "c", At: checksPassedAt}},
		{RunID: "run-bbbbbbbb22222222", WorkItemID: "ex-2", WorkItemTitle: "Name the stop", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: at(2), ReviewDecision: runstate.ReviewRepair, ReviewFindings: 2},
		{RunID: "run-cccccccc33333333", WorkItemID: "ex-3", WorkItemTitle: "Count the slots", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: at(1)},
	}
	standing := Standing{ObservedAt: now, Running: []RunningRun{}, NotStartable: []Refused{}}
	for _, state := range running {
		evidence, evidenceAt := evidenceOf(state)
		standing.Running = append(standing.Running, RunningRun{RunID: state.RunID, WorkItemID: state.WorkItemID, Title: state.WorkItemTitle, Phase: state.Phase, Stage: StageOf(state.Phase), StartedAt: state.StartedAt, Evidence: evidence, EvidenceAt: evidenceAt})
	}
	for index, run := range []string{"run-dddddddd44444444", "run-eeeeeeee55555555", "run-ffffffff66666666"} {
		standing.NeedsHuman = append(standing.NeedsHuman, resolved(Attention{Kind: AttentionOwedStep, ID: run, WorkItemID: "ex-4", OwedStep: &OwedStep{Phase: runstate.PhaseCleaningUp, EndedAt: at(4 - index)}}))
	}
	standing.NeedsHuman = append(standing.NeedsHuman, resolved(Attention{Kind: AttentionOutage, ID: string(domain.ProviderUnauthenticated), Outage: &runstate.ProviderOutage{Cause: domain.ProviderUnauthenticated, Since: at(2)}}))
	standing.NotStartableGroups = []WaitGroup{{Kind: backlog.HoldKind("held"), Count: 2, WaitsOn: "wait on the development manager's decision about a stopped run", Next: "she decides what becomes of each stopped run", Mover: MoverDevelopmentManager, Items: []WorkItemRef{{WorkItemID: "ex-5", Title: "Keep the branch"}, {WorkItemID: "ex-6", Title: "Read the queue"}}}}

	throughput := Throughput{ObservedAt: now, Windows: []Window{
		{Label: "today", Days: 1, Since: firstLocalDay(now, 1),
			Landed: 3,
			LandedItems: []LandedRun{
				{RunID: "run-1111111111111111", WorkItemID: "ex-7", Title: "Fix the tile", LandedAt: at(1)},
				{RunID: "run-2222222222222222", WorkItemID: "doc-1-1", Title: "Publish the goals", LandedAt: at(2), Document: "V1 goals"},
				{RunID: "run-3333333333333333", WorkItemID: "ex-8", Title: "Name the slot", LandedAt: at(3)},
			},
			StopsByCause:      map[runstate.StopClass]int{runstate.StopChecks: 3, runstate.StopCheckTimeout: 1},
			LatestStopByCause: map[runstate.StopClass]time.Time{runstate.StopChecks: at(1), runstate.StopCheckTimeout: at(5)},
		},
		{Label: "last 7 days", Days: 7, Since: firstLocalDay(now, 7), LandedItems: []LandedRun{}, StopsByCause: map[runstate.StopClass]int{}},
	}}

	spend := Spend{ObservedAt: now,
		Windows: []SpendWindow{{Label: "last 24 hours", Rolling: true}, {Label: "last 7 days", Days: 7, CostUSD: 81.5, Invocations: 300}},
		Days:    []SpendDay{{Day: firstLocalDay(now, 1), Reached: true, CostUSD: 12.3, Invocations: 40, Kinds: []KindSpend{{Kind: runstate.StreamKind("run"), Invocations: 30, CostUSD: 10}}}},
	}
	return SummaryInputs{Standing: &standing, Throughput: &throughput, Spend: &spend, Period: SummaryToday, Now: now}
}

func entryWith(t *testing.T, answer SummaryAnswer, words string) SummaryEntry {
	t.Helper()
	for _, entry := range answer.Entries {
		if strings.Contains(entry.Says, words) {
			return entry
		}
	}
	t.Fatalf("the %s answer has no line saying %q: %+v", answer.Heading, words, answer.Entries)
	return SummaryEntry{}
}

// What landed is said with code changes and documents apart, each with its
// time in the local zone with the zone named.
func TestTheSummarySaysWhatLandedWithCodeApartFromDocuments(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	summary := Summarize(summaryFixture(now))

	if summary.Landed.Says != "2 code changes" || len(summary.Landed.Entries) != 2 {
		t.Fatalf("landed = %+v", summary.Landed)
	}
	if summary.Published.Says != "1 document" || summary.Published.Entries[0].Says != "V1 goals" {
		t.Fatalf("published = %+v", summary.Published)
	}
	tile := entryWith(t, summary.Landed, "Fix the tile (ex-7)")
	if want := now.Add(-time.Hour).Local().Format("15:04 MST"); tile.LatestSays != want {
		t.Fatalf("landed at %q, want %q", tile.LatestSays, want)
	}
	for _, entry := range summary.Landed.Entries {
		if strings.Contains(entry.Says, "goals") {
			t.Fatalf("a document is counted among the code changes: %+v", entry)
		}
	}
	if !strings.HasPrefix(summary.SinceSays, "today, since 00:00 ") {
		t.Fatalf("since = %q", summary.SinceSays)
	}
}

// Each run in progress carries the newest result about its change.
func TestTheSummarySaysEachRunInProgressWithItsLatestEvidence(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	summary := Summarize(summaryFixture(now))

	if summary.Moving.Says != "3 runs" {
		t.Fatalf("moving = %+v", summary.Moving)
	}
	reviewed := entryWith(t, summary.Moving, "Retry the merge (ex-1)")
	if !strings.Contains(reviewed.Says, "in review; latest: the checks passed") || reviewed.LatestSays != now.Add(-time.Hour).Local().Format("15:04 MST") {
		t.Fatalf("reviewed run = %+v", reviewed)
	}
	repaired := entryWith(t, summary.Moving, "Name the stop (ex-2)")
	if !strings.Contains(repaired.Says, "the review returned 2 findings") {
		t.Fatalf("repaired run = %+v", repaired)
	}
	fresh := entryWith(t, summary.Moving, "Count the slots (ex-3)")
	if !strings.Contains(fresh.Says, "no check or review result yet") {
		t.Fatalf("fresh run = %+v", fresh)
	}

	for _, test := range []struct {
		state runstate.State
		want  string
	}{
		{runstate.State{ReviewDecision: runstate.ReviewApprove, ChecksPassed: &runstate.ChecksPassed{}}, "the review approved the change"},
		{runstate.State{CheckFailure: &runstate.CheckFailure{Command: "make test"}}, "a check failed"},
		{runstate.State{PathRefusal: &runstate.PathRefusal{}}, "touched a file it may not"},
		{runstate.State{ChecksPassed: &runstate.ChecksPassed{}}, "the checks passed"},
		{runstate.State{}, ""},
	} {
		got, _ := evidenceOf(test.state)
		if (test.want == "") != (got == "") || !strings.Contains(got, test.want) {
			t.Fatalf("evidence of %+v = %q, want %q", test.state, got, test.want)
		}
	}
}

// What is blocked says whose move it is and the next step, and the same thing
// seen three times is one line with its count and the latest time.
func TestTheSummarySaysWhatIsBlockedWithItsOwnerAndGroupsRepeats(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	summary := Summarize(summaryFixture(now))

	owed := entryWith(t, summary.Blocked, "Run not finished")
	if owed.Count != 3 || !strings.Contains(owed.Says, "3 times") || owed.WaitingOn != "the harness" || owed.Next != "settle the run" {
		t.Fatalf("owed step line = %+v", owed)
	}
	if want := now.Add(-2 * time.Hour).Local().Format("15:04 MST"); owed.LatestSays != want {
		t.Fatalf("owed step latest %q, want %q", owed.LatestSays, want)
	}
	if len(owed.Detail) != 3 {
		t.Fatalf("each occurrence is kept behind the line: %+v", owed.Detail)
	}
	held := entryWith(t, summary.Blocked, "2 wait on the development manager's decision")
	if held.WaitingOn != "the development manager" || held.Next == "" {
		t.Fatalf("held group = %+v", held)
	}
	for _, entry := range summary.Blocked.Entries {
		if strings.Contains(entry.Says, "provider") {
			t.Fatalf("the owner's own step is said as blocked on somebody else: %+v", entry)
		}
	}

	stopped := entryWith(t, summary.Failures, runstate.StopSays(runstate.StopChecks))
	if stopped.Count != 3 || stopped.Says != "3 runs: "+runstate.StopSays(runstate.StopChecks) || stopped.LatestSays != now.Add(-time.Hour).Local().Format("15:04 MST") {
		t.Fatalf("stopped at the checks = %+v", stopped)
	}
	if summary.Failures.Says != "4 runs" || summary.Failures.Entries[0].Count != 3 {
		t.Fatalf("failures = %+v", summary.Failures)
	}
}

// Only what is the owner's own — a reason on the closed list, or a role's
// decision that needs his hand — is said as needing his decision.
func TestTheSummarySaysOnlyTheOwnersOwnDecisions(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	summary := Summarize(summaryFixture(now))

	if summary.Decisions.Says != "1 thing only you can do" || len(summary.Decisions.Entries) != 1 {
		t.Fatalf("decisions = %+v", summary.Decisions)
	}
	login := summary.Decisions.Entries[0]
	if !strings.Contains(login.Says, "Provider unavailable") || login.Next != "log in to the provider again" || !strings.HasPrefix(login.WaitingOn, "you, because it is") {
		t.Fatalf("login = %+v", login)
	}

	in := summaryFixture(now)
	in.Standing.NeedsHuman = in.Standing.NeedsHuman[:3]
	if says := Summarize(in).Decisions.Says; says != "nothing" {
		t.Fatalf("with nothing of his, decisions say %q", says)
	}
}

// A period with nothing in any answer says so in one sentence, and one with
// anything in it does not.
func TestTheSummarySaysAQuietPeriodInOneSentence(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	in := summaryFixture(now)
	if quiet := Summarize(in).Quiet; quiet != "" {
		t.Fatalf("a busy period is said to be quiet: %q", quiet)
	}
	in.Standing.Running, in.Standing.NeedsHuman, in.Standing.NotStartableGroups = []RunningRun{}, nil, nil
	in.Throughput.Windows[0].LandedItems, in.Throughput.Windows[0].StopsByCause = []LandedRun{}, nil
	in.Spend.Days = nil
	if quiet := Summarize(in).Quiet; !strings.HasPrefix(quiet, "Nothing landed, nothing is in progress or blocked") || !strings.Contains(quiet, "today, since 00:00 ") {
		t.Fatalf("a quiet period is said as %q", quiet)
	}
}

// The cost is the spend reading's figure for the period.
func TestTheSummarySaysWhatThePeriodCost(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	in := summaryFixture(now)
	if says := Summarize(in).Cost.Says; says != "$12.30 across 40 provider calls" {
		t.Fatalf("today cost %q", says)
	}
	in.Period = SummaryWeek
	week := Summarize(in)
	if week.Cost.Says != "$81.50 across 300 provider calls" || !strings.HasPrefix(week.SinceSays, "over the last 7 days") {
		t.Fatalf("week = %q, %q", week.Cost.Says, week.SinceSays)
	}
	in.Spend.Windows[1].Floor = true
	if says := Summarize(in).Cost.Says; !strings.HasPrefix(says, "at least $81.50") {
		t.Fatalf("a floor is said as one: %q", says)
	}
}

// An answer whose reading is missing says so rather than nothing, and keeps
// the reason behind the line.
func TestTheSummarySaysWhatItCouldNotRead(t *testing.T) {
	t.Parallel()
	summary := Summarize(SummaryInputs{ThroughputProblem: "the run records could not be read", SpendProblem: "the spend could not be read", Now: summaryNow()})
	for _, answer := range []SummaryAnswer{summary.Landed, summary.Published, summary.Moving, summary.Blocked, summary.Failures, summary.Decisions, summary.Cost} {
		if !answer.Unread || answer.Says != "could not be read just now" || len(answer.Detail) == 0 {
			t.Fatalf("answer %s = %+v", answer.Heading, answer)
		}
	}
}

// The summary as printed carries no run identifier, no stop cause's code, and
// no configuration key, and passes the project's terms check; the detail
// behind it keeps what the lines leave out.
func TestTheSummaryReadsInOrdinaryWordsAndKeepsTheDetailBehindIt(t *testing.T) {
	t.Parallel()
	summary := Summarize(summaryFixture(summaryNow()))
	plain := summary.Render(false)
	for _, heading := range []string{"Landed: 2 code changes", "Documents published: 1 document", "In progress: 3 runs", "Blocked: 2 things", "Ended without landing: 4 runs", "Needs your decision: 1 thing", "Cost: $12.30"} {
		if !strings.Contains(plain, heading) {
			t.Fatalf("the summary lacks %q:\n%s", heading, plain)
		}
	}
	if found := regexp.MustCompile(`run-[0-9a-f]{6,}`).FindString(plain); found != "" {
		t.Fatalf("the summary names run %s:\n%s", found, plain)
	}
	for _, class := range runstate.StopClasses() {
		if strings.Contains(string(class), "-") && strings.Contains(plain, string(class)) {
			t.Fatalf("the summary carries the stop cause code %s:\n%s", class, plain)
		}
	}
	if found := regexp.MustCompile(`\b[a-z]+_[a-z_]+\b|\b[a-z]+\.[a-z]+\.[a-z_]+\b`).FindString(plain); found != "" {
		t.Fatalf("the summary carries what reads as a configuration key, %q:\n%s", found, plain)
	}
	if strings.Contains(plain, "detail:") {
		t.Fatalf("the plain summary carries detail:\n%s", plain)
	}

	detailed := summary.Render(true)
	for _, kept := range []string{"run-dddddddd44444444: cleanup", "recorded stop cause check-timeout: 1"} {
		if !strings.Contains(detailed, kept) {
			t.Fatalf("the detail lacks %q:\n%s", kept, detailed)
		}
	}

	checker, err := terms.ReadTextChecker("../..")
	if err != nil {
		t.Fatal(err)
	}
	if findings := checker.Find(plain); len(findings) > 0 {
		t.Fatalf("the summary fails the terms check: %+v\n%s", findings, plain)
	}
}

// Every line about runs that ended without landing says its cause in the
// words runstate.StopSays owns for it, so the summary and every other surface
// that names a stop cause say the same thing.
func TestTheSummarySaysEachStopCauseInTheWordsRunstateOwns(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	for _, class := range runstate.StopClasses() {
		in := summaryFixture(now)
		in.Throughput.Windows[0].StopsByCause = map[runstate.StopClass]int{class: 2}
		in.Throughput.Windows[0].LatestStopByCause = map[runstate.StopClass]time.Time{class: now.Add(-time.Hour)}
		failures := Summarize(in).Failures
		if len(failures.Entries) != 1 || failures.Entries[0].Says != "2 runs: "+runstate.StopSays(class) {
			t.Fatalf("stop cause %s is said as %+v, want the words %q", class, failures.Entries, runstate.StopSays(class))
		}
	}
}

// A document's run is named by its document's title in the throughput, so the
// summary can tell it from a code change.
func TestTheThroughputMarksADocumentsLanding(t *testing.T) {
	t.Parallel()
	state := runstate.State{Document: &runstate.DocumentPublication{Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{Title: "V1 goals"}}}}
	if got := documentTitle(state); got != "V1 goals" {
		t.Fatalf("document title = %q", got)
	}
	if got := documentTitle(runstate.State{}); got != "" {
		t.Fatalf("a code change is named as a document: %q", got)
	}
}
