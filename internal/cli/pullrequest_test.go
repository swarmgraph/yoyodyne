package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
)

func TestPullRequestArgumentsAreRefusedBeforeAnythingIsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no subcommand", nil, "Usage: yoyo pull-request track"},
		{"unknown subcommand", []string{"merge", "4"}, `unknown pull-request command "merge"`},
		{"track without a number", []string{"track", "--item", "ex-1"}, "name the pull request by its number"},
		{"track with a word", []string{"track", "forty", "--item", "ex-1"}, `"forty" is not a pull request number`},
		{"track with zero", []string{"track", "0", "--item", "ex-1"}, `"0" is not a pull request number`},
		{"track with two numbers", []string{"track", "4", "5", "--item", "ex-1"}, "name the pull request by its number"},
		{"track without an item", []string{"track", "4"}, "name each item the pull request carries"},
		{"untrack without a number", []string{"untrack"}, "name the pull request by its number"},
		{"untrack with a negative number", []string{"untrack", "-3"}, "flag provided but not defined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := runPullRequest(context.Background(), test.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; stderr:\n%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr does not contain %q:\n%s", test.want, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want nothing", stdout.String())
			}
		})
	}
}

func TestPullRequestIsRegisteredAsACommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"pull-request"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage: yoyo pull-request track") {
		t.Fatalf("stderr carries no pull-request usage:\n%s", stderr.String())
	}
	var usage bytes.Buffer
	printUsage(&usage)
	if !strings.Contains(usage.String(), "pull-request") {
		t.Fatalf("the command list does not name pull-request:\n%s", usage.String())
	}
}

func TestPrintTrackedPullRequestSaysWhatHappenedToEachItem(t *testing.T) {
	t.Parallel()

	track := orchestrator.TeamPullRequestTrack{Number: 42, Items: []orchestrator.TrackedItem{
		{WorkItemID: "ex-1"},
		{WorkItemID: "ex-2", MovedFrom: 40},
		{WorkItemID: "ex-3", Unverified: true},
		{WorkItemID: "ex-4", Unchanged: true},
		{WorkItemID: "ex-5", Failure: "a live process holds run r5"},
	}}
	var stdout, stderr bytes.Buffer
	code := reportTeamPullRequest(&stdout, &stderr, false, track, nil, printTrackedPullRequest)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for the item that failed", code)
	}
	for _, want := range []string{
		"ex-1: recorded as carried by pull request #42\n",
		"ex-2: recorded as carried by pull request #42 (moved from pull request #40)\n",
		"ex-3: recorded as carried by pull request #42 (recorded as unverified: its landing commit is not among the pull request's commits)\n",
		"ex-4: already recorded as carried by pull request #42\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	if want := "ex-5: not recorded: a live process holds run r5\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestPrintUntrackedPullRequestSaysWhatHappenedToEachItem(t *testing.T) {
	t.Parallel()

	track := orchestrator.TeamPullRequestTrack{Number: 42, Items: []orchestrator.TrackedItem{
		{WorkItemID: "ex-1"},
		{WorkItemID: "ex-2", Unchanged: true},
	}}
	var stdout, stderr bytes.Buffer
	if code := reportTeamPullRequest(&stdout, &stderr, false, track, nil, printUntrackedPullRequest); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if want := "ex-1: pull request #42 no longer tracked; it still waits for its landing commit to be found on the team's branch\n"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
	}
	if !strings.Contains(stdout.String(), "ex-2: pull request #42 was not tracked on it any longer\n") {
		t.Errorf("stdout does not say ex-2 was already untracked:\n%s", stdout.String())
	}
}

func TestReportTeamPullRequestRefusalIsOnStandardErrorOrInJSON(t *testing.T) {
	t.Parallel()

	refusal := context.DeadlineExceeded
	var stdout, stderr bytes.Buffer
	if code := reportTeamPullRequest(&stdout, &stderr, false, orchestrator.TeamPullRequestTrack{}, refusal, printTrackedPullRequest); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), refusal.Error()) || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q; want the refusal on stderr only", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := reportTeamPullRequest(&stdout, &stderr, true, orchestrator.TeamPullRequestTrack{}, refusal, nil); code != 1 {
		t.Fatalf("JSON exit code = %d, want 1", code)
	}
	var failed map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &failed); err != nil || failed["error"] != refusal.Error() {
		t.Fatalf("JSON = %q (%v), want an error field", stdout.String(), err)
	}

	stdout.Reset()
	if code := reportTeamPullRequest(&stdout, &stderr, true, orchestrator.TeamPullRequestTrack{Number: 7}, nil, nil); code != 0 {
		t.Fatalf("JSON exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), `"items":[]`) {
		t.Fatalf("JSON = %q, want an empty items list rather than null", stdout.String())
	}
}

func TestPrintTeamPullRequestsSaysWhatTheCheckFound(t *testing.T) {
	t.Parallel()

	pulls := []orchestrator.TeamPullRequestSettlement{
		{Number: 1, Found: orchestrator.TeamPullRequestMerged, MergeCommit: "abc123", Items: []orchestrator.TeamPullRequestItem{
			{WorkItemID: "ex-1", Settled: true},
			{WorkItemID: "ex-2", Failure: "the tracker is down"},
		}},
		{Number: 2, Found: orchestrator.TeamPullRequestClosed, ClosedBy: "alice", Items: []orchestrator.TeamPullRequestItem{
			{WorkItemID: "ex-3", Settled: true},
		}},
		{Number: 3, Found: orchestrator.TeamPullRequestOpen, Items: []orchestrator.TeamPullRequestItem{
			{WorkItemID: "ex-4", Added: true},
			{WorkItemID: "ex-5", Kept: "a live process holds run r5"},
		}},
		{Number: 4, Failure: "read pull request #4: no route"},
	}
	var stdout, stderr bytes.Buffer
	printTeamPullRequests(&stdout, &stderr, pulls)
	for _, want := range []string{
		"pull request #1 into the team's branch merged as abc123; ex-1 closed\n",
		"pull request #2 was closed without being merged by alice; ex-3 goes back to the Lead Product Manager\n",
		"ex-4's landing commit is among the commits of pull request #3; it is now tracked as carrying it\n",
		"ex-5 left as it stands: a live process holds run r5\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	for _, want := range []string{
		"ex-2 on pull request #1 not settled: the tracker is down\n",
		"pull request #4 into the team's branch not settled: read pull request #4: no route\n",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
}

// The forge check's text output, as checkForge prints it, says what it found
// for each tracked pull request, and before the closing count.
func TestTheForgeCheckSaysWhatItFoundForEachTrackedPullRequest(t *testing.T) {
	t.Parallel()

	var output forgeCheckOutput
	output.TeamPullRequests = []orchestrator.TeamPullRequestSettlement{
		{Number: 7, Found: orchestrator.TeamPullRequestMerged, MergeCommit: "abc123", Items: []orchestrator.TeamPullRequestItem{
			{WorkItemID: "ex-1", Settled: true},
			{WorkItemID: "ex-2", Failure: "the tracker is down"},
		}},
		{Number: 8, Found: orchestrator.TeamPullRequestClosed, ClosedBy: "alice", Items: []orchestrator.TeamPullRequestItem{
			{WorkItemID: "ex-3", Settled: true},
		}},
		{Number: 9, Failure: "read pull request #9: no route"},
	}
	var stdout, stderr bytes.Buffer
	printForgeCheckFindings(&stdout, &stderr, output)
	said := stdout.String()
	merged := "pull request #7 into the team's branch merged as abc123; ex-1 closed\n"
	closed := "pull request #8 was closed without being merged by alice; ex-3 goes back to the Lead Product Manager\n"
	for _, want := range []string{merged, closed} {
		if !strings.Contains(said, want) {
			t.Errorf("stdout lacks %q:\n%s", want, said)
		}
	}
	if count := strings.Index(said, "forge check:"); count < 0 || count < strings.Index(said, closed) {
		t.Errorf("the closing count is missing or comes before what was found:\n%s", said)
	}
	for _, want := range []string{
		"ex-2 on pull request #7 not settled: the tracker is down\n",
		"pull request #9 into the team's branch not settled: read pull request #9: no route\n",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
}

func TestDescribeForgeCheckCountsPullRequestsIntoTheTeamsBranch(t *testing.T) {
	t.Parallel()

	var output forgeCheckOutput
	if said := describeForgeCheck(output); strings.Contains(said, "team's branch") {
		t.Fatalf("describeForgeCheck() = %q, want no mention when none were read", said)
	}
	output.TeamPullRequests = []orchestrator.TeamPullRequestSettlement{{Number: 1}, {Number: 2}}
	if said := describeForgeCheck(output); !strings.Contains(said, ", 2 pull request(s) into the team's branch read") {
		t.Fatalf("describeForgeCheck() = %q", said)
	}
}

func TestForgeCheckJSONCarriesAnEmptyListOfTeamPullRequests(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := reportForgeCheck(&stdout, &stderr, true, forgeCheckOutput{}, nil); code != 0 {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"team_pull_requests":[]`) {
		t.Fatalf("JSON = %q, want team_pull_requests as an empty list", stdout.String())
	}
}
