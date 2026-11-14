package factoryflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeForge struct {
	open []publish.OpenPullRequestStatus
	err  error
}

func (f fakeForge) ListOpenStatus(context.Context) ([]publish.OpenPullRequestStatus, error) {
	return f.open, f.err
}

type fakeRuns struct {
	states   []runstate.State
	waits    []runstate.DocumentWait
	err      error
	waitsErr error
}

func (f fakeRuns) Recorded() ([]runstate.State, error)             { return f.states, f.err }
func (f fakeRuns) DocumentWaits() ([]runstate.DocumentWait, error) { return f.waits, f.waitsErr }

var now = time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)

func document(id, title string, owner domain.AgentRole) *runstate.DocumentPublication {
	return &runstate.DocumentPublication{
		Candidate:      artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: id, Title: title, Path: "docs/designs/" + id + ".md"}},
		ConversationID: "chat-1",
		WriteID:        "document-" + id,
		Owner:          owner,
	}
}

func request(number int, branch string, opened time.Time) publish.OpenPullRequestStatus {
	return publish.OpenPullRequestStatus{
		PullRequest: publish.PullRequest{Number: number, HeadBranch: branch, BaseBranch: "main"},
		CreatedAt:   opened,
		Mergeable:   "MERGEABLE",
		MergeState:  "BLOCKED",
		Checks:      publish.ChecksSummary{Passed: 3},
	}
}

// A forge that could not be read is said to the pass as not known, and never
// as a list with nothing on it.
func TestAFailedForgeReadIsSaidAndNeverAnEmptyList(t *testing.T) {
	reading := Reader{
		Forge:    fakeForge{err: errors.New("gh: HTTP 502")},
		Runs:     fakeRuns{},
		Landings: 2,
		Now:      func() time.Time { return now },
	}.Read(context.Background())
	if !strings.Contains(reading.Section, "NOT KNOWN") || !strings.Contains(reading.Section, "this is not an empty list") || !strings.Contains(reading.Section, "HTTP 502") {
		t.Fatalf("section = %s", reading.Section)
	}
	if strings.Contains(reading.Section, "None: the forge holds") {
		t.Fatalf("a failed read was rendered as an empty list: %s", reading.Section)
	}
	if len(reading.Problems) != 1 || !strings.Contains(reading.Problems[0], "HTTP 502") {
		t.Fatalf("problems = %v", reading.Problems)
	}
}

// Each open request the harness opened is listed with what it carries, how old
// it is, whether it can merge, and its checks; a document with more than one
// open publication request is a finding of its own; a request somebody else
// opened is left out.
func TestOpenRequestsAreListedAndADuplicatedDocumentIsAFinding(t *testing.T) {
	design := document("machine-home", "Machine home", domain.RoleArchitect)
	states := []runstate.State{
		{RunID: "run-a", Status: runstate.StatusFailed, StartedAt: now.Add(-20 * time.Hour), Branch: "yoyodyne/document-machine-home/a", Document: design,
			PullRequest: &runstate.PullRequest{Number: 101}},
		{RunID: "run-b", Status: runstate.StatusSucceeded, StartedAt: now.Add(-10 * time.Hour), Document: design,
			MergeQueue: &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-b", TargetBranch: "main", AdmittedAt: now.Add(-9 * time.Hour)}},
		{RunID: "run-c", Status: runstate.StatusRunning, StartedAt: now.Add(-time.Hour), Branch: "yoyodyne/yoyodyne-7fl/c", WorkItemID: "yoyodyne-7fl", WorkItemTitle: "factory flow"},
	}
	open := []publish.OpenPullRequestStatus{
		request(101, "yoyodyne/document-machine-home/a", now.Add(-19*time.Hour)),
		request(102, runstate.MergeQueueCandidateBranch("mqe-b"), now.Add(-9*time.Hour)),
		request(103, "yoyodyne/yoyodyne-7fl/c", now.Add(-30*time.Minute)),
		request(104, "feature/by-hand", now.Add(-time.Hour)),
	}
	open[1].Mergeable, open[1].Checks = "CONFLICTING", publish.ChecksSummary{Passed: 1, Failed: []string{"test"}}
	reading := Reader{Forge: fakeForge{open: open}, Runs: fakeRuns{states: states}, Landings: 2, Now: func() time.Time { return now }}.Read(context.Background())

	for _, want := range []string{
		"3 open, oldest first",
		`#101 carries document "Machine home" (machine-home), the architect's, run run-a; open 19h 0m`,
		"#102 carries document \"Machine home\" (machine-home), the architect's, run run-b; open 9h 0m; CANNOT merge",
		"checks: 1 of 2 failed (test)",
		"#103 carries work item yoyodyne-7fl (factory flow), run run-c; open 30m; can merge without conflict",
		"held from merging because the base branch's protection rules are not satisfied",
		"approved document(s) waiting in the merge queue",
	} {
		if !strings.Contains(reading.Section, want) {
			t.Errorf("section is missing %q:\n%s", want, reading.Section)
		}
	}
	if strings.Contains(reading.Section, "#104") {
		t.Errorf("a request the harness did not open was listed:\n%s", reading.Section)
	}
	if len(reading.Findings) != 1 || !strings.Contains(reading.Findings[0].Issue, `document "Machine home" (machine-home), the architect's, has 2 open publication pull requests at once: #101, #102`) {
		t.Fatalf("findings = %#v", reading.Findings)
	}
	if err := reading.Findings[0].Validate(); err != nil {
		t.Fatalf("the finding is not one an account accepts: %v", err)
	}
}

// Every document landing held for longer than the set time is a finding
// naming the role whose documents hold them; the same holds a little younger
// is not, and the documents waiting are listed with how long they waited.
func TestEveryLandingHeldTooLongIsAFindingNamingTheRole(t *testing.T) {
	held := func(age time.Duration) []runstate.State {
		return []runstate.State{
			{RunID: "run-1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: now.Add(-age), Document: document("one", "One", domain.RoleArchitect)},
			{RunID: "run-2", Status: runstate.StatusRunning, StartedAt: now.Add(-age - time.Hour), Document: document("two", "Two", domain.RoleArchitect)},
		}
	}
	waits := []runstate.DocumentWait{{Document: *document("three", "Three", domain.RoleProductManager), Since: now.Add(-5 * time.Hour)}}
	reading := Reader{Runs: fakeRuns{states: held(LandingHeldTooLong + time.Minute), waits: waits}, Landings: 2, Now: func() time.Time { return now }}.Read(context.Background())
	for _, want := range []string{
		"this project's publishing is not automatic",
		"2 in all): 2 held",
		`held by document "One" (one), the architect's, run run-1`,
		`document "Three" (three), the Lead Product Manager's, from conversation chat-1, waiting since`,
		"(5h 0m)",
	} {
		if !strings.Contains(strings.ToLower(reading.Section), strings.ToLower(want)) {
			t.Errorf("section is missing %q:\n%s", want, reading.Section)
		}
	}
	if len(reading.Findings) != 1 {
		t.Fatalf("findings = %#v, want the stalled landings", reading.Findings)
	}
	issue := reading.Findings[0].Issue
	if !strings.Contains(issue, "every document landing (2 of 2) has been held for longer than 3h0m0s") || !strings.Contains(issue, "the architect's") || !strings.Contains(issue, "1 document(s) wait behind them") {
		t.Fatalf("finding = %q", issue)
	}

	young := Reader{Runs: fakeRuns{states: held(LandingHeldTooLong - 2*time.Hour)}, Landings: 2, Now: func() time.Time { return now }}.Read(context.Background())
	if len(young.Findings) != 0 || !strings.Contains(young.Section, "None: no document") {
		t.Fatalf("a landing held less than the set time was named: %#v", young.Findings)
	}
	spare := Reader{Runs: fakeRuns{states: held(10 * time.Hour)}, Landings: 3, Now: func() time.Time { return now }}.Read(context.Background())
	if len(spare.Findings) != 0 || !strings.Contains(spare.Section, "- free") {
		t.Fatalf("with a landing free, nothing is stalled: %#v\n%s", spare.Findings, spare.Section)
	}
}

// Run records or waits that could not be read are said as unknown.
func TestUnreadableRunRecordsAreSaidAsUnknown(t *testing.T) {
	reading := Reader{Runs: fakeRuns{err: errors.New("disk"), waitsErr: errors.New("torn")}, Landings: 2, Now: func() time.Time { return now }}.Read(context.Background())
	if !strings.Contains(reading.Section, "NOT KNOWN: the run records could not be read") || !strings.Contains(reading.Section, "NOT KNOWN rather than empty") {
		t.Fatalf("section = %s", reading.Section)
	}
	if len(reading.Problems) != 2 {
		t.Fatalf("problems = %v", reading.Problems)
	}
}
