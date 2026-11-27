package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var repeatClock = time.Date(2026, 11, 27, 22, 2, 0, 0, time.UTC)

func standingReport(id string, severity Severity, message string, at time.Time) Report {
	return Report{
		SchemaVersion: SchemaVersion, ID: id, Role: domain.RoleDeveloper, Agent: "developer",
		RunID: "run-aaaa", WorkItemID: "yoyodyne-one", ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		Severity: severity, Message: message, For: domain.RoleDevelopmentManager, RecordedAt: at,
	}
}

const (
	standingID = "report-00000000000000000000000000000001"
	otherID    = "report-00000000000000000000000000000002"
)

func TestPlainFormTakesOutWhatDiffersBetweenRunsAndNothingElse(t *testing.T) {
	t.Parallel()
	first := "Run run-58bac51fa3 left a test server on port 8080 at commit 6ab5a03f3dd4  see report-00000000000000000000000000000009 at 14:02 PST on 2026-11-27."
	second := "run RUN-0123abcd LEFT a test server on port 9090 at commit b8f3ef41a3\tsee report-0000000000000000000000000000000a at 9:15 PDT on 2026-11-28."
	if PlainForm(first) != PlainForm(second) {
		t.Fatalf("plain forms differ:\n%q\n%q", PlainForm(first), PlainForm(second))
	}
	if PlainForm("the test server was left running") == PlainForm("the test server was not left running") {
		t.Fatal("plain form merged two messages that differ in a word")
	}
	if got := PlainForm("  A   B\nC "); got != "a b c" {
		t.Fatalf("PlainForm whitespace = %q", got)
	}
}

func TestMatchFindsAStandingReportByPlainFormOrByName(t *testing.T) {
	t.Parallel()
	standing := standingReport(standingID, SeverityWarning, "run run-1111 left a server on port 8080 at 6ab5a03f", repeatClock)
	filing := standingReport(otherID, SeverityWarning, "run run-2222 left a server on port 9090 at b8f3ef41", repeatClock.Add(time.Hour))
	// The filing role and the work item are not compared.
	filing.Role, filing.WorkItemID = domain.RoleReviewer, "yoyodyne-two"
	in := Matching{Reports: []Report{standing}}

	if id, named, refused := Match(filing, "", in); id != standingID || named || refused != "" {
		t.Fatalf("Match by plain form = %q, %v, %q", id, named, refused)
	}

	higher := filing
	higher.Severity = SeverityCritical
	if id, _, _ := Match(higher, "", in); id != "" {
		t.Fatalf("a more severe report was counted as a repeat of %s", id)
	}
	lower := filing
	lower.Severity = SeverityNote
	if id, _, _ := Match(lower, "", in); id != standingID {
		t.Fatal("a less severe report was not counted as a repeat")
	}
	elsewhere := filing
	elsewhere.For = ""
	if id, _, _ := Match(elsewhere, "", in); id != "" {
		t.Fatal("a report for another addressee was counted as a repeat")
	}

	handled := Matching{Reports: []Report{standing}, Handlings: []Handling{{ReportID: standingID, RecordedAt: repeatClock}}}
	if id, _, _ := Match(filing, "", handled); id != "" {
		t.Fatal("a report handled since was counted as repeated")
	}

	worded := standingReport(otherID, SeverityNote, "a different wording of the same fact", repeatClock)
	if id, named, refused := Match(worded, standingID, Matching{Reports: []Report{standing}, Offered: []string{standingID}}); id != standingID || !named || refused != "" {
		t.Fatalf("Match by name = %q, %v, %q", id, named, refused)
	}
	if id, _, refused := Match(worded, standingID, in); id != "" || !strings.Contains(refused, "was not on the list") {
		t.Fatalf("a name not on the run's list = %q, %q", id, refused)
	}
	if id, _, refused := Match(higher, standingID, Matching{Reports: []Report{standing}, Offered: []string{standingID}}); id != "" || !strings.Contains(refused, "more severe") {
		t.Fatalf("a named repeat at a higher severity = %q, %q", id, refused)
	}
	if id, _, refused := Match(elsewhere, standingID, Matching{Reports: []Report{standing}, Offered: []string{standingID}}); id != "" || !strings.Contains(refused, "no role named") {
		t.Fatalf("a named repeat for another addressee = %q, %q", id, refused)
	}
	handled.Offered = []string{standingID}
	if id, _, refused := Match(worded, standingID, handled); id != "" || !strings.Contains(refused, "handled since") {
		t.Fatalf("a named repeat of a handled report = %q, %q", id, refused)
	}
}

func TestOfferStandingListsTheMostRepeatedFirstThenTheNewestWithinItsBounds(t *testing.T) {
	t.Parallel()
	var reports []Report
	for i := 0; i < 14; i++ {
		reports = append(reports, standingReport(fmt.Sprintf("report-%032x", i+1), SeverityNote, strings.Repeat("long message ", 30), repeatClock.Add(time.Duration(i)*time.Minute)))
	}
	repeats := []Repeat{{ReportID: reports[0].ID}, {ReportID: reports[0].ID}, {ReportID: reports[1].ID}}
	handlings := []Handling{{ReportID: reports[13].ID, RecordedAt: repeatClock}}
	offered := OfferStanding(reports, handlings, repeats)
	if len(offered.IDs) == 0 || offered.IDs[0] != reports[0].ID || offered.IDs[1] != reports[1].ID || offered.IDs[2] != reports[12].ID {
		t.Fatalf("offered order = %v", offered.IDs)
	}
	if len(offered.IDs) > MaxOfferedReports || len(offered.Text) > MaxOfferedListBytes {
		t.Fatalf("offered %d reports in %d bytes", len(offered.IDs), len(offered.Text))
	}
	if strings.Contains(offered.Text, reports[13].ID) {
		t.Fatal("a handled report was offered")
	}
	if want := fmt.Sprintf("%d more reports stand", 13-len(offered.IDs)); !strings.Contains(offered.Text, want) {
		t.Fatalf("offered text does not say %q:\n%s", want, offered.Text)
	}
	for _, line := range strings.Split(offered.Text, "\n") {
		if strings.HasPrefix(line, "- report-") {
			if !strings.Contains(line, ", for the development manager, note: ") {
				t.Fatalf("line does not carry addressee and severity: %q", line)
			}
			_, message, _ := strings.Cut(line, "note: ")
			if len(strings.TrimSuffix(message, "…")) > OfferedMessageBytes {
				t.Fatalf("message is %d bytes", len(message))
			}
		}
	}
	if OfferStanding(nil, nil, nil).Text != "" {
		t.Fatal("an empty pile offered a list")
	}
}

func TestRepeatLineSaysTheCountAndTheLatestInLocalTime(t *testing.T) {
	t.Parallel()
	pacific := time.FixedZone("PST", -8*3600)
	repeats := []Repeat{
		{ReportID: standingID, RunID: "run-later", WorkItemID: "yoyodyne-two", RecordedAt: repeatClock},
		{ReportID: standingID, RunID: "run-earlier", WorkItemID: "yoyodyne-one", RecordedAt: repeatClock.Add(-time.Hour)},
	}
	if got, want := RepeatLine(repeats, pacific), "      repeated 2 times, the latest by run run-later on yoyodyne-two at 14:02 PST on Nov 27\n"; got != want {
		t.Fatalf("RepeatLine = %q, want %q", got, want)
	}
	if got := RepeatLine(repeats[:1], pacific); !strings.Contains(got, "repeated once") {
		t.Fatalf("RepeatLine(one) = %q", got)
	}
	if RepeatLine(nil, pacific) != "" {
		t.Fatal("a report nothing repeated got a line")
	}
	if says := (Counted{Repeat: Repeat{ReportID: standingID, Severity: SeverityWarning}, Count: 3}).Says(); !strings.Contains(says, "repeated report "+standingID) || !strings.Contains(says, "3 times") {
		t.Fatalf("Says = %q", says)
	}
}

func TestARepeatCarriesWhatTheReportWouldHave(t *testing.T) {
	t.Parallel()
	filing := standingReport(otherID, SeverityNote, "the whole new message", repeatClock)
	filing.Build = "0123456789abcdef"
	repeat := RepeatOf(filing, standingID, true)
	if err := repeat.Validate(); err != nil {
		t.Fatal(err)
	}
	if repeat.ReportID != standingID || repeat.RunID != filing.RunID || repeat.WorkItemID != filing.WorkItemID || repeat.Role != filing.Role ||
		repeat.Agent != filing.Agent || repeat.Build != filing.Build || repeat.Severity != filing.Severity || repeat.Message != filing.Message || !repeat.RecordedAt.Equal(filing.RecordedAt) {
		t.Fatalf("repeat = %#v", repeat)
	}
	repeat.ReportID = "nonsense"
	if repeat.Validate() == nil {
		t.Fatal("a repeat naming no report validated")
	}
}
