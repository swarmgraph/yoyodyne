package sweep

import (
	"strings"
	"testing"
)

// A report entry names a report and carries exactly one of a decision or why
// it was left, and a turn's block carrying one decodes with it kept.
func TestAReportEntryCarriesADecisionOrWhyItWasLeft(t *testing.T) {
	t.Parallel()

	const id = "report-00000000000000000000000000000d01"
	for _, tc := range []struct {
		name  string
		entry ReportDecision
		want  string
	}{
		{"decided", ReportDecision{Report: id, Decision: "revised the design"}, ""},
		{"left", ReportDecision{Report: id, Left: "waiting on the review"}, ""},
		{"not a report", ReportDecision{Report: "yoyodyne-ifd.1", Decision: "revised"}, "is not a report's identifier"},
		{"neither", ReportDecision{Report: id}, "carries neither"},
		{"both", ReportDecision{Report: id, Decision: "revised", Left: "later"}, "not both"},
	} {
		err := tc.entry.Validate()
		if tc.want == "" && err != nil {
			t.Errorf("%s: Validate() = %v, want nil", tc.name, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: Validate() = %v, want it to say %q", tc.name, err, tc.want)
		}
	}

	decoded, err := Decode(`{"status":"complete","summary":"done","reports":[{"report":"` + id + `","left":"waiting on the review"}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if entry, found := decoded.ReportDecided(id); !found || entry.Left != "waiting on the review" {
		t.Fatalf("ReportDecided() = %+v, %v, want the entry kept", entry, found)
	}
	if _, err := Decode(`{"status":"complete","summary":"done","reports":[{"report":"` + id + `"}]}`); err == nil {
		t.Fatal("Decode() accepted a report entry carrying neither a decision nor a leaving")
	}
	merged := Result{Status: StatusMore, Summary: "one", Reports: []ReportDecision{{Report: id, Left: "later"}}}.
		Merge(Result{Status: StatusComplete, Summary: "two", Reports: []ReportDecision{{Report: id, Decision: "revised"}}})
	if entry, _ := merged.ReportDecided(id); entry.Decision != "revised" {
		t.Fatalf("merged entry = %+v, want the later turn's to stand", entry)
	}
}
