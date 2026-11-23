package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordsLikeAnAdapter makes the stand-in provider do what every adapter does
// with a request's recorder: record what it is about to send, and send nothing
// if that fails.
func recordsLikeAnAdapter(provider *orchestratortest.Backend) {
	inner := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		fallback := backend.SentPart{Name: "first message", Origin: backend.OriginHarnessBuild()}
		sent := backend.SentInvocation{Backend: "claude-code", Messages: []backend.SentMessage{
			{Role: backend.SentSystem, Parts: backend.PartsOr(request.SystemParts, request.SystemPrompt, fallback)},
			{Role: backend.SentUser, Parts: backend.PartsOr(request.PromptParts, request.Prompt, fallback)},
		}}
		if err := backend.RecordInvocation(request, sent); err != nil {
			return backend.RunResult{}, err
		}
		return inner(request)
	}
}

type unwritableInvocations struct{}

func (unwritableInvocations) Recorder(string, domain.AgentRole, string, func() time.Time) backend.InvocationRecorder {
	return refusingRecorder{}
}

type refusingRecorder struct{}

func (refusingRecorder) RecordInvocation(backend.SentInvocation) error {
	return errors.New("the state directory is full")
}

func withInvocations(pipeline Pipeline, log backend.InvocationLog) Pipeline {
	pipeline.Invocations = log
	reviewer := pipeline.Reviewer.(review.Reviewer)
	reviewer.Invocations = log
	pipeline.Reviewer = reviewer
	return pipeline
}

// Every developer attempt and every review of a run is kept before it is made:
// the first attempt, a repair that starts a new one, and each review.
func TestEveryDeveloperAndReviewerInvocationOfARunIsKept(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	recordsLikeAnAdapter(provider)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = withInvocations(pipeline, store.Invocations())

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	kept, err := store.Invocations().List(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	developers, reviewers := len(provider.RequestsForRole(domain.RoleDeveloper)), len(provider.RequestsForRole(domain.RoleReviewer))
	if developers < 2 || reviewers < 2 || len(kept) != developers+reviewers {
		t.Fatalf("%d developer and %d reviewer invocations made, %d kept", developers, reviewers, len(kept))
	}
	// What was kept is what each invocation's request carried.
	byRole := map[string][]string{}
	for _, entry := range kept {
		text, err := store.Invocations().Text(outcome.RunID, entry.Number)
		if err != nil {
			t.Fatalf("invocation %d: %v", entry.Number, err)
		}
		byRole[entry.Role] = append(byRole[entry.Role], text.Messages[len(text.Messages)-1].Text)
	}
	for index, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		if byRole["developer"][index] != request.Prompt {
			t.Fatalf("developer invocation %d kept %q, was sent %q", index+1, byRole["developer"][index], request.Prompt)
		}
	}
	// The developer's invocations share their contract, and it is kept once;
	// each part says where it came from.
	digests := map[string][]string{}
	origins := map[string]string{}
	for _, entry := range kept {
		if entry.Role != "developer" {
			continue
		}
		for _, part := range entry.Parts() {
			digests[part.Name] = append(digests[part.Name], part.Digest)
			origins[part.Name] = part.Origin
		}
	}
	contract := digests["developer contract"]
	if len(contract) < 2 || contract[0] != contract[1] {
		t.Fatalf("the developer contract was kept as %v, want one digest for every developer invocation", contract)
	}
	if !strings.HasPrefix(origins["developer contract"], "the harness build") {
		t.Fatalf("contract origin = %q", origins["developer contract"])
	}
	if !strings.HasPrefix(origins["work item and its notes"], "tracker item yoyodyne-task, read at ") {
		t.Fatalf("work item origin = %q", origins["work item and its notes"])
	}
	for index, request := range provider.RequestsForRole(domain.RoleReviewer) {
		if byRole["reviewer"][index] != request.Prompt {
			t.Fatalf("reviewer invocation %d kept a different message than it was sent", index+1)
		}
	}
}

// A developer invocation that cannot be recorded is never started, and the run
// stops on its own store without spending a repair attempt.
func TestADeveloperInvocationThatCannotBeRecordedIsNeverStarted(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	started := false
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		started = true
		return nil
	}, approveVerdict)
	recordsLikeAnAdapter(provider)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = withInvocations(pipeline, unwritableInvocations{})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("a run whose developer could not be recorded went on")
	}
	if started {
		t.Fatal("the developer was started although nothing was recorded")
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.RecordedStopClass() != runstate.StopRecording || state.RepairAttempts != 0 {
		t.Fatalf("stop class %q, repair attempts %d; want a stop on the record and no repair spent", state.RecordedStopClass(), state.RepairAttempts)
	}
}

// The pieces of a prompt the harness can point to are named where they are
// found, and the parts always join to exactly the prompt.
func TestSplitByKnownPartsNamesWhatItFindsAndJoinsToTheText(t *testing.T) {
	t.Parallel()
	known := []backend.SentPart{
		{Name: "contract", Text: "CONTRACT\n\n"},
		{Name: "persona", Text: ""},
		{Name: "invariants", Text: "INVARIANTS\n"},
		{Name: "absent", Text: "NOT HERE"},
		{Name: "work item", Text: "WORK ITEM"},
	}
	rest := backend.SentPart{Name: "rest"}
	text := "A refusal first.\n" + "CONTRACT\n\n" + "INVARIANTS\n" + "WORK ITEM" + "\nand findings"
	parts := splitByKnownParts(text, known, rest)
	var names []string
	for _, part := range parts {
		names = append(names, part.Name)
	}
	if got := strings.Join(names, ","); got != "rest,contract,invariants,work item,rest" {
		t.Fatalf("parts = %s", got)
	}
	if backend.JoinedText(parts) != text {
		t.Fatalf("parts joined to %q, want the text", backend.JoinedText(parts))
	}
	if only := splitByKnownParts("nothing known", known, rest); len(only) != 1 || only[0].Name != "rest" || only[0].Text != "nothing known" {
		t.Fatalf("a text with nothing known = %+v", only)
	}
}

// A merge-queue candidate review streams its events under its generation, which
// is no run, and is kept with the run whose change the candidate carries, at the
// candidate's commit; the reviewer is then started and reaches its verdict.
func TestAMergeQueueCandidateReviewIsKeptWithItsEntrysRunAndStarted(t *testing.T) {
	t.Parallel()
	f := newQueueFixture(t)
	reviewer := f.reviewer.reviewer.(review.Reviewer)
	recordsLikeAnAdapter(reviewer.Backend.(*orchestratortest.Backend))
	store := f.runs.(*admittedRun).StateStore.(*runstate.Store)
	reviewer.Invocations = store.Invocations()
	f.reviewer.reviewer = reviewer

	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	if !verification.Verified || verification.Generation.Review == nil || verification.Generation.Review.Decision != string(review.DecisionApprove) {
		t.Fatalf("Work() = %#v, want the candidate reviewed and approved", verification)
	}
	request := f.reviewer.last()
	if request.RunID == f.entry.RunID {
		t.Fatalf("the review's events were streamed under the run %s, want the generation's own stream", request.RunID)
	}
	kept, err := store.Invocations().List(f.entry.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Role != string(domain.RoleReviewer) || kept[0].RunID != f.entry.RunID {
		t.Fatalf("kept = %+v, want the one candidate review under the entry's run", kept)
	}
	if kept[0].Commit != verification.Generation.Candidate {
		t.Fatalf("kept at commit %q, the review was of the candidate %q", kept[0].Commit, verification.Generation.Candidate)
	}
	text, err := store.Invocations().Text(f.entry.RunID, kept[0].Number)
	if err != nil {
		t.Fatal(err)
	}
	if got := text.Messages[len(text.Messages)-1].Text; !strings.Contains(got, verification.Generation.Candidate) {
		t.Fatal("what was kept is not the candidate review's message")
	}
}
