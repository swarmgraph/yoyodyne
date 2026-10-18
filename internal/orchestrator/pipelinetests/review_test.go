package pipelinetests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestPipelineSkipsReviewAndIntegrationWhenChecksFail(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 3"})
	before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("Run() error = %v", err)
	}
	// The check is returned to the developer for every permitted attempt, and
	// none of them makes it pass, so the reviewer is never reached at all.
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatal("a failed check reached the reviewer")
	}
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 3 {
		t.Fatalf("developer invocations = %d, want the first attempt and both repairs", runs)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a failed check reached integration: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
		t.Fatalf("main moved on a failed check: %q, want %q", head, before)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("failed worktree was not preserved: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.Phase != runstate.PhaseChecking || state.ReviewDecision != "" || state.Integration != nil {
		t.Fatalf("state = %#v", state)
	}
}

func TestPipelineNeverIntegratesWithoutAnApprovingVerdict(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		verdict  string
		want     string
		decision string
	}{
		{
			name:     "repair",
			verdict:  testharness.RepairVerdict,
			want:     "independent review requires repair",
			decision: runstate.ReviewRepair,
		},
		{
			name:    "malformed",
			verdict: "sure, looks good to me!",
			want:    "decode review verdict",
		},
		{
			name:    "approval contradicted by its own findings",
			verdict: `{"decision":"approve","summary":"fine apart from the data loss","findings":[{"severity":"blocker","message":"this drops the index"}]}`,
			want:    "contradictory review verdict",
		},
		{
			name:    "approval carrying a decision the contract does not define",
			verdict: `{"decision":"looks-good","summary":"ok","severity_note":"none"}`,
			want:    `decision "looks-good"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, test.verdict)
			pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			before := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
			if outcome.Integration != nil || tracker.Closed {
				t.Fatalf("unapproved change was integrated: %#v, closed = %t", outcome.Integration, tracker.Closed)
			}
			if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
				t.Fatalf("main moved without an approval: %q, want %q", head, before)
			}
			if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
				t.Fatalf("unapproved worktree was not preserved: %v", err)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.Status != runstate.StatusFailed || state.Phase != runstate.PhaseReviewing || state.Integration != nil {
				t.Fatalf("state = %#v", state)
			}
			if state.ReviewSessionID != "reviewer-session" || state.ReviewDecision != test.decision {
				t.Fatalf("durable review evidence = %#v", state)
			}
			if test.decision == runstate.ReviewRepair {
				if state.ReviewFindings != 1 || len(state.ReviewFindingDetails) != 1 || !strings.Contains(tracker.Notes, "Finding [blocker] (feature.txt:1): add the missing file") {
					t.Fatalf("repair findings were not preserved: state = %#v, notes = %q", state, tracker.Notes)
				}
			}
		})
	}
}

// A verdict that carries a field the schema does not name is a verbose verdict
// rather than a corrupted one. It integrates the run exactly as the same verdict
// without the extra field would, and what the reviewer invented is recorded in
// the run's event stream instead of costing the whole change. This replays the
// verdict shape that killed run-2e5102d105a1c4ad772722b30b3d2635.
func TestPipelineIntegratesAVerdictCarryingFieldsTheSchemaDoesNotName(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, `{"decision":"approve","approves":"implementation","summary":"the change matches the acceptance criteria","severity_note":"no blocking issues found"}`)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("a verbose verdict did not integrate: %#v, closed = %t", outcome, tracker.Closed)
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
		t.Fatalf("main = %q, want the integrated commit %q", head, outcome.Integration.TargetCommit)
	}
	// One review: an extra field is never a reason to ask again.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want 1", reviews)
	}
	// The drift is diagnostic gold for a prompt regression, so it survives the
	// run that shrugged it off.
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	drift := ""
	for _, event := range events {
		if event.Type == execution.EventReviewDrift {
			drift = string(event.Payload)
		}
	}
	if !strings.Contains(drift, `"fields":["severity_note"]`) {
		t.Fatalf("review.drift payload = %q, want the drifted field named", drift)
	}
	// Nothing the schema does not define reaches anything that acts on a
	// verdict, so an extra field is no more than a note in the log.
	if outcome.ReviewSummary != "the change matches the acceptance criteria" || len(outcome.ReviewFindings) != 0 {
		t.Fatalf("Run() review evidence = %#v", outcome)
	}
}

// A reply nothing could read as a verdict is a failed review invocation rather
// than a failed change, so the reviewer is asked once more before the run gives
// up on work the checks have already passed.
func TestPipelineAsksTheReviewerAgainWhenItsReplyCannotBeReadAsAVerdict(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "Sure! Here is my review.", testharness.ApproveVerdict)
	pipeline, _ := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("the re-asked review did not integrate: %#v, closed = %t", outcome, tracker.Closed)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Fatalf("reviews = %d, want the unreadable reply asked again once", reviews)
	}
	// The re-ask costs a review, never a repair attempt: the developer was told
	// nothing, because the reviewer said nothing about the change.
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
		t.Fatalf("developer invocations = %d, want 1", runs)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("Run() repair attempts = %d, want the re-ask to cost none", outcome.RepairAttempts)
	}
}

// A verdict the reviewer wrote inside a code fence is read as the verdict it
// encloses: it integrates on the one review, with nothing asked again. This is
// the reply run-f940f785 stopped on.
func TestPipelineIntegratesAVerdictWrittenInsideACodeFence(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "```json\n"+testharness.ApproveVerdict+"\n```")
	pipeline, _ := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("a fenced approval did not integrate: %#v, closed = %t", outcome, tracker.Closed)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want the fenced verdict read on the first", reviews)
	}
}

// A decode error is answered by asking once more, in the reviewer's own session
// and naming what was wrong with the reply, rather than by stopping a run that
// reached review clean. The first reply here is fenced with prose after it,
// which the decoder still cannot read.
func TestPipelineAsksAgainInTheReviewersSessionNamingTheDecodeError(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "```json\n"+testharness.ApproveVerdict+"\n```\nHope that helps.", testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the decode error answered by a re-ask", err)
	}
	if outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the re-asked review did not integrate: %#v, closed = %t", outcome, tracker.Closed)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want one re-ask", len(reviews))
	}
	if reviews[0].SessionID != "" {
		t.Fatalf("first review resumed session %q", reviews[0].SessionID)
	}
	if reviews[1].SessionID != provider.ReviewerSession {
		t.Fatalf("re-ask ran in session %q, want the reviewer's own %q", reviews[1].SessionID, provider.ReviewerSession)
	}
	for _, want := range []string{"decode review verdict", "Write it bare"} {
		if !strings.Contains(reviews[1].Prompt, want) {
			t.Fatalf("re-ask does not say %q:\n%s", want, reviews[1].Prompt)
		}
	}
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 || outcome.RepairAttempts != 0 {
		t.Fatalf("developer invocations = %d, repair attempts = %d; the re-ask costs neither", runs, outcome.RepairAttempts)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	reasked := false
	for _, event := range events {
		if event.Type == execution.EventReviewStarted && strings.Contains(string(event.Payload), `"reask":true`) {
			reasked = true
		}
	}
	if !reasked {
		t.Fatal("the run's record does not say the review was asked again")
	}
}

// Two unreadable replies in a row is a reviewer that cannot answer the contract,
// and the run ends on it rather than asking forever.
func TestPipelineFailsAfterASecondUnreadableVerdict(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "Sure! Here is my review.")
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopReviewAccount)
	if err == nil || !strings.Contains(err.Error(), "decode review verdict") {
		t.Fatalf("Run() error = %v, want the second unreadable verdict to end the run", err)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("an unreviewed change was integrated: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Fatalf("reviews = %d, want exactly one re-ask", reviews)
	}
	// The stop says in plain words that the reviewer was asked twice, beside the
	// decode error itself, on the run and on the item.
	plain := "the reviewer was asked twice and neither reply could be accepted as a verdict"
	if !strings.Contains(err.Error(), plain) {
		t.Fatalf("Run() error = %v, want it to say %q", err, plain)
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if !strings.Contains(state.Failure, plain) || !strings.Contains(state.Failure, "decode review verdict") {
		t.Fatalf("run failure = %q, want the plain account and the decode error", state.Failure)
	}
	if !strings.Contains(tracker.Notes, plain) || !strings.Contains(tracker.Notes, "decode review verdict") {
		t.Fatalf("item notes = %q, want the plain account and the decode error", tracker.Notes)
	}
}

func TestPipelineReturnsFindingsToTheSameDeveloperUntilOneAttemptIsApproved(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt leaves the reviewer something to object to; the
		// repair attempt fixes it in the same worktree.
		content := "incomplete\n"
		if attempts > 1 {
			content = "implemented\n"
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
	}, testharness.RepairVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("repaired work was not integrated: %#v, closed = %t, blocked = %t", outcome.Integration, tracker.Closed, tracker.Blocked)
	}
	if outcome.RepairAttempts != 1 || outcome.ReviewDecision != review.DecisionApprove {
		t.Fatalf("Run() outcome = %#v", outcome)
	}

	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want 2", len(developerRequests))
	}
	// The repair attempt resumes the developer's own session, in the branch and
	// worktree the first attempt used.
	repair := developerRequests[1]
	if repair.SessionID != provider.DeveloperSession {
		t.Fatalf("repair attempt session = %q, want %q", repair.SessionID, provider.DeveloperSession)
	}
	if repair.WorkingDirectory != developerRequests[0].WorkingDirectory || repair.WorkingDirectory != outcome.WorktreePath {
		t.Fatalf("repair attempt ran in %q, want %q", repair.WorkingDirectory, outcome.WorktreePath)
	}
	// The findings reach the developer as the structured verdict the reviewer
	// produced, not as a restatement of it.
	for _, want := range []string{"repair attempt 1 of 2", `"severity": "blocker"`, `"message": "add the missing file"`, `"file": "feature.txt"`} {
		if !strings.Contains(repair.Prompt, want) {
			t.Fatalf("repair prompt is missing %q:\n%s", want, repair.Prompt)
		}
	}
	// Every attempt is verified and reviewed again; nothing is inherited.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Fatalf("reviews = %d, want 2", reviews)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if completed := countEvents(events, execution.EventCommandCompleted); completed != 2 {
		t.Fatalf("completed check commands = %d, want 2", completed)
	}
	if head := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main"); head != outcome.Integration.TargetCommit {
		t.Fatalf("main = %q, want the integrated commit %q", head, outcome.Integration.TargetCommit)
	}
	// What was integrated is the repaired change, not the one the reviewer
	// rejected.
	if integrated := testharness.GitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the repaired content", integrated)
	}
	// The evidence that authorized integration belongs to the approving
	// attempt: its own approval, from a reviewer session distinct from the
	// developer's, with the rejected attempt's findings gone rather than
	// carried forward.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewDecision != runstate.ReviewApprove || len(state.ReviewFindingDetails) != 0 || state.ReviewFindings != 0 {
		t.Fatalf("integrated run kept the rejected attempt's review evidence: %#v", state)
	}
	if state.ReviewSessionID == "" || state.ReviewSessionID == state.ProviderSessionID {
		t.Fatalf("integrated run has no independent reviewer session: developer = %q, reviewer = %q", state.ProviderSessionID, state.ReviewSessionID)
	}
}
