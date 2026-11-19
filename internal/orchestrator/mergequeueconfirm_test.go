package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

type keptMergeReports struct {
	mu   sync.Mutex
	kept []report.Report
}

func (k *keptMergeReports) Append(r report.Report) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.kept = append(k.kept, r)
	return nil
}

type answeringConfirmer struct {
	check gitworktree.MergeCheck
	err   error
}

func (a answeringConfirmer) CheckMergeCommit(_ context.Context, merge, base, candidate string) (gitworktree.MergeCheck, error) {
	check := a.check
	check.MergeCommit, check.RecordedBase, check.Candidate = merge, base, candidate
	return check, a.err
}

func TestAMergeCheckFilesOneCriticalReportForEachKindOfDifference(t *testing.T) {
	t.Parallel()
	const base, other = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	const merge, candidate = "3333333333333333333333333333333333333333", "4444444444444444444444444444444444444444"
	cases := []struct {
		name     string
		answer   answeringConfirmer
		matches  bool
		severity report.Severity
		says     []string
	}{
		{name: "a merge on the recorded base with the candidate's files", answer: answeringConfirmer{check: gitworktree.MergeCheck{FirstParent: base}}, matches: true},
		{name: "a merge on another base", answer: answeringConfirmer{check: gitworktree.MergeCheck{FirstParent: other}}, severity: report.SeverityCritical, says: []string{merge, "first parent is " + other, base}},
		{name: "a merge with other files", answer: answeringConfirmer{check: gitworktree.MergeCheck{FirstParent: base, FilesDiffer: []string{"extra.txt"}}}, severity: report.SeverityCritical, says: []string{merge, "extra.txt", "not the candidate's"}},
		{name: "a merge that could not be checked", answer: answeringConfirmer{err: errors.New("the object is missing")}, severity: report.SeverityWarning, says: []string{merge, "could not be checked", "the object is missing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reports := &keptMergeReports{}
			pipeline := Pipeline{Reports: reports, Config: config.Config{Product: config.Product{ID: "example", RepositoryID: "example"}}}
			_, matched, filed := pipeline.confirmMergeCommit(context.Background(), tc.answer, "item-1", merge, base, candidate, 7)
			if filed != nil {
				t.Fatalf("filing error = %v", filed)
			}
			if matched != tc.matches {
				t.Fatalf("matched = %v, want %v", matched, tc.matches)
			}
			if tc.matches {
				if len(reports.kept) != 0 {
					t.Fatalf("reports = %#v, want none for a matching merge", reports.kept)
				}
				return
			}
			if len(reports.kept) != 1 || reports.kept[0].Severity != tc.severity || reports.kept[0].Role != report.HarnessReporter {
				t.Fatalf("reports = %#v, want one %s report from the harness", reports.kept, tc.severity)
			}
			for _, want := range tc.says {
				if !strings.Contains(reports.kept[0].Message, want) {
					t.Errorf("message %q does not say %q", reports.kept[0].Message, want)
				}
			}
		})
	}
}

func TestAMergeAuditChecksEveryLandingAndListsEach(t *testing.T) {
	t.Parallel()
	reports := &keptMergeReports{}
	auditor := fakeAuditor{
		answeringConfirmer: answeringConfirmer{check: gitworktree.MergeCheck{FirstParent: "1111111111111111111111111111111111111111"}},
		landings: []gitworktree.LandedPullRequest{
			{Merge: "3333333333333333333333333333333333333333", Candidate: "4444444444444444444444444444444444444444", Base: "1111111111111111111111111111111111111111", Subject: "Merge pull request #7 from a/b"},
			{Merge: "5555555555555555555555555555555555555555", Subject: "squashed", Unchecked: "it has 1 parents, so it is not a merge of a candidate"},
		},
	}
	entries, err := Pipeline{Reports: reports}.AuditLandedMerges(context.Background(), auditor, "origin/main", time.Time{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("AuditLandedMerges() = %#v, %v; want both landings listed", entries, err)
	}
	if !entries[0].Matches || !strings.Contains(entries[0].Says(), "matches") {
		t.Errorf("first entry = %q, want a match", entries[0].Says())
	}
	if !strings.Contains(entries[1].Says(), "not checked") {
		t.Errorf("second entry = %q, want it listed as not checked", entries[1].Says())
	}
	if len(reports.kept) != 0 {
		t.Errorf("reports = %#v, want none", reports.kept)
	}
	if pullRequestNumber("Merge pull request #2201 from x") != 2201 {
		t.Error("pullRequestNumber did not read #2201")
	}
}

type fakeAuditor struct {
	answeringConfirmer
	landings []gitworktree.LandedPullRequest
}

func (f fakeAuditor) LandedPullRequests(context.Context, string, time.Time) ([]gitworktree.LandedPullRequest, error) {
	return f.landings, nil
}
