package intakerules

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A document that cannot be read stops the readers and is reported once to the
// Lead Product Manager; while it stays that way nothing more is filed, and once
// it reads again a later break is reported afresh.
func TestAnUnreadableDocumentStopsTheReadersAndIsReportedOnce(t *testing.T) {
	t.Parallel()

	broken := Rules{Query: "slack-bugs", Path: "setup/rules.md", Branch: "main", Commit: "0123456789abcdef0123456789abcdef01234567",
		Parsed: Parse([]byte("## Rules\n\n"+strings.Replace(goodRule, "priority: 1", "priority: 0", 1)), testGoals())}
	first := Judge(broken, Standing{})
	if first.ReadersMayRun {
		t.Fatal("readers may run on a document that cannot be read as rules")
	}
	if first.Report == nil {
		t.Fatal("no report filed for a document that cannot be read")
	}
	if first.Report.For != domain.RoleProductManager || first.Report.Severity != report.SeverityWarning {
		t.Fatalf("report = %#v, want a warning for the Lead Product Manager", first.Report)
	}
	if err := first.Report.Validate(); err != nil {
		t.Fatalf("report does not validate: %v", err)
	}
	for _, want := range []string{"setup/rules.md", "intake query slack-bugs", "main holds it at 0123456789ab", "none of its 1 rules can be used", "sets priority 0", "the messages wait and none is lost"} {
		if !strings.Contains(first.Report.Message, want) {
			t.Errorf("report = %q, want it to say %q", first.Report.Message, want)
		}
	}

	again := Judge(broken, first.Standing)
	if again.ReadersMayRun || again.Report != nil || again.Standing != first.Standing {
		t.Fatalf("second run on the same broken document = %#v, want readers still stopped and nothing filed", again)
	}

	fixed := Rules{Query: "slack-bugs", Path: "setup/rules.md", Branch: "main", Commit: "fedcba", Parsed: Parse([]byte("## Rules\n\n"+goodRule), testGoals())}
	mended := Judge(fixed, again.Standing)
	if !mended.ReadersMayRun || mended.Report != nil || mended.Standing != (Standing{}) {
		t.Fatalf("a mended document = %#v, want readers running and the standing report cleared", mended)
	}

	if broke := Judge(broken, mended.Standing); broke.Report == nil || broke.ReadersMayRun {
		t.Fatalf("a document broken again = %#v, want a new report", broke)
	}
}

// A refused rule beside good ones is not a document to report: the readers run
// on the rules that remain.
func TestARefusedRuleBesideGoodOnesDoesNotStopTheReaders(t *testing.T) {
	t.Parallel()

	parsed := Parse([]byte("## Rules\n\n"+goodRule+strings.Replace(goodRule, "[slack-bug-report]", "[urgent]", 1)+"  - extra: x\n"), testGoals())
	verdict := Judge(Rules{Query: "q", Path: "r.md", Branch: "main", Commit: "c", Parsed: parsed}, Standing{})
	if !verdict.ReadersMayRun || verdict.Report != nil {
		t.Fatalf("Judge() = %#v, want readers running and nothing filed", verdict)
	}
	if len(parsed.Refused) != 1 {
		t.Fatalf("refused = %#v", parsed.Refused)
	}
}

func TestAReportListingManyRefusalsStaysOneReadableReport(t *testing.T) {
	t.Parallel()

	var rules []string
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		rules = append(rules, strings.Replace(strings.Replace(goodRule, "[slack-bug-report]", "["+id+"]", 1), "workflow: delivery", "workflow: other", 1))
	}
	parsed := Parse([]byte("## Rules\n\n"+strings.Join(rules, "")), testGoals())
	verdict := Judge(Rules{Query: "q", Path: "r.md", Branch: "main", Commit: "c", Parsed: parsed}, Standing{})
	if verdict.Report == nil || !strings.Contains(verdict.Report.Message, "and 2 more") || verdict.Report.Validate() != nil {
		t.Fatalf("report = %#v", verdict.Report)
	}
}
