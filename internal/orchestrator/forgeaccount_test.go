package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// These drive yoyodyne-ifd.429.35: a merge the harness withdraws or hands back
// over a forge check carries the forge's own account of the check onto the
// item — its name, how it ended, the commit, the forge's annotations, the
// failing step's lines from the job's log, and a link to it — read under the
// harness's forge access, because a developer run given the item may not reach
// the forge at all. Where the forge refuses the harness's token, the item says
// so and names granting it as the operator's.

// redOnTheChange is a head failing a check on the file its change touches,
// with the forge's annotations and its page for the job.
func redOnTheChange() publish.CheckReading {
	return publish.CheckReading{
		Files: []string{"feature.txt"},
		Failing: []publish.FailedCheck{{
			Name:       "lint",
			Paths:      []string{"feature.txt"},
			ID:         77,
			Conclusion: "failure",
			URL:        "https://example.invalid/acme/thing/actions/runs/5/job/77",
			Annotations: []publish.Annotation{
				{Path: "feature.txt", Line: 3, Level: "failure", Message: "line is longer than 100 characters"},
				{Path: ".github", Level: "failure", Message: "Process completed with exit code 1."},
			},
		}},
		Passing:  2,
		BehindBy: 4,
	}
}

// A merge handed back over a red check carries the forge's account onto the
// item: the check, its conclusion, the commit, the annotations in the forge's
// words, the failing step's log lines, and the link — and nothing on the item
// sends whoever works it to the forge.
func TestAMergeHandedBackOverARedCheckCarriesTheForgesAccountOntoTheItem(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.Reading = redOnTheChange()
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)
	logs := &orchestratortest.JobLogs{Tail: "##[group]Run make lint\nfeature.txt:3: line is longer than 100 characters\n##[error]Process completed with exit code 1."}
	reconciler.JobLogs = logs

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || len(forge.Withdrawn) != 1 {
		t.Fatalf("reconciliation = %#v, withdrawn = %v; want the merge withdrawn and handed back", results, forge.Withdrawn)
	}
	if len(logs.Asked) != 1 || logs.Asked[0] != 77 {
		t.Errorf("job logs asked = %v, want the failing job's log read under the harness's access", logs.Asked)
	}
	head := loadRun(t, fixture.store, pipelineRunID).PullRequest.HeadCommit
	notes := fixture.tracker.Record().Notes
	for _, want := range []string{
		"The forge's account of the failing checks, read by the harness under its own forge access",
		"nothing here asks anybody to fetch the forge",
		"How the forge ended lint: failure.",
		"Commit: " + head + ".",
		"Log: https://example.invalid/acme/thing/actions/runs/5/job/77",
		"- feature.txt:3 (failure): line is longer than 100 characters",
		"- .github (failure): Process completed with exit code 1.",
		"The failing step's lines from the forge's log of the job, at most 60 (check run 77)",
		"> feature.txt:3: line is longer than 100 characters",
		"> ##[error]Process completed with exit code 1.",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the item's notes do not carry %q:\n%s", want, notes)
		}
	}
	blocker := fixture.tracker.Record().BlockReason
	if strings.Contains(blocker, "says why") {
		t.Errorf("the blocker sends its reader to the forge's log rather than the record:\n%s", blocker)
	}
	settled := loadRun(t, fixture.store, pipelineRunID)
	recorded := settled.PullRequest.Checks.Failing[0]
	if recorded.URL == "" || len(recorded.Annotations) != 2 || recorded.Annotations[0].Line != 3 {
		t.Errorf("recorded check = %#v, want its link and annotations kept on the publication", recorded)
	}
}

// Where the forge will not let the harness's token read a job's log or run it
// again, the item says so and names the grant as the operator's, and it is
// still handed the rest of the forge's account.
func TestAForgeThatRefusesTheHarnessesTokenIsNamedOnTheItemAsTheOperatorsToGrant(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.Reading = jobFailure(4215)
	forge.RefuseRerun = fmt.Errorf("ask the forge to run check 4215 again: exit code 1: gh: Resource not accessible by integration (HTTP 403): %w", publish.ErrForgeAccessRefused)
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)
	logs := &orchestratortest.JobLogs{Err: fmt.Errorf("read the forge's log of check 4215: exit code 1: gh: Resource not accessible by integration (HTTP 403): %w", publish.ErrForgeAccessRefused)}
	reconciler.JobLogs = logs

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the merge handed back", results)
	}
	notes := fixture.tracker.Record().Notes
	for _, want := range []string{
		"How the forge ended adoption: cancelled.",
		"The forge would not let the harness's token read this job's log",
		"Granting that token read access to the repository's Actions is the operator's",
		"nobody working this item is asked to fetch it",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the item's notes do not carry %q:\n%s", want, notes)
		}
	}
	if blocker := fixture.tracker.Record().BlockReason; !strings.Contains(blocker, "the forge would not let the harness's token run the job again") || !strings.Contains(blocker, "granting it that is the operator's") {
		t.Errorf("blocker does not name the re-run refusal as the operator's to grant:\n%s", blocker)
	}
	if !errors.Is(forge.RefuseRerun, publish.ErrForgeAccessRefused) {
		t.Fatal("the fixture's refusal is not the forge refusing the token")
	}
}
