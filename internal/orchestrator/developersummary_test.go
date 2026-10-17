package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Compatibility evidence in the developer's final account used to be absent
// from the review request even though the run returned it in its outcome.
func TestReviewReceivesTheDeveloperSummaryOfEachAttempt(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(strings.Repeat("implemented\n", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"Implemented the configuration. Compatibility with the previous build was checked before adding the key.",
		"Repaired the configuration. Compatibility with the previous build was checked again after the repair.",
	}
	respond := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := respond(request)
		if err != nil || request.Role != domain.RoleDeveloper {
			return result, err
		}
		payload := execution.ReplyPayload(result.FinalText)
		payload["role"] = request.Role
		event, err := execution.NewEvent(request.RunID, request.LastSequence+1, time.Now(), execution.EventAgentMessage, "fake", payload)
		if err != nil {
			return result, err
		}
		if err := request.EventSink(event); err != nil {
			return result, err
		}
		result.LastEvent = event.Sequence
		return result, nil
	}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want the original and repaired change", len(reviews))
	}
	for i, request := range reviews {
		if !strings.Contains(request.Prompt, provider.DeveloperFinalTextByAttempt[i]) {
			t.Errorf("review %d lost its developer's compatibility account:\n%s", i, request.Prompt)
		}
		if i == 0 && strings.Contains(request.Prompt, provider.DeveloperFinalTextByAttempt[1]) {
			t.Errorf("review %d received a later attempt's summary", i)
		}
		if strings.Contains(request.Prompt, "yoyodyne-verification") {
			t.Errorf("review %d received the execution block as summary prose", i)
		}
		if !strings.Contains(request.Prompt, "# Check results") || !strings.Contains(request.Prompt, "test -f feature.txt") {
			t.Errorf("review %d lost the harness's check results", i)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.DeveloperSummary == nil || state.DeveloperSummary.Attempt != 1 || state.DeveloperSummary.Text != outcome.Summary {
		t.Fatalf("durable summary = %#v, want the repaired attempt's account %q", state.DeveloperSummary, outcome.Summary)
	}
	if len(state.EarlierDeveloperSummaries) != 1 || state.EarlierDeveloperSummaries[0].Attempt != 0 || state.EarlierDeveloperSummaries[0].Text != provider.DeveloperFinalTextByAttempt[0] {
		t.Fatalf("earlier summaries = %#v, want the first attempt's account kept", state.EarlierDeveloperSummaries)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, event := range events {
		if event.Type != execution.EventAgentMessage {
			continue
		}
		var reply struct{ Text string }
		if err := json.Unmarshal(event.Payload, &reply); err != nil {
			t.Fatal(err)
		}
		for _, summary := range provider.DeveloperFinalTextByAttempt {
			if reply.Text == summary {
				recorded = append(recorded, summary)
			}
		}
	}
	if len(recorded) != 2 {
		t.Fatalf("the event log retained %d developer accounts, want both accounts the review must receive", len(recorded))
	}
}

// Docket continuation delivery (yoyodyne-ifd.430.40), run-070a8c4e,
// received the diagnosis at review events 1081 and 1798, but lost it at 1408
// and 2125 after each approval's change was replayed onto a newer base.
func TestReviewKeepsTheDeveloperDiagnosisAfterReplayOntoAMovedTarget(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		writePipelineFile(t, repository, "elsewhere.txt", "concurrent work\n")
		runPipelineGit(t, repository, "add", "elsewhere.txt")
		runPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		return nil
	}, approveVerdict)
	provider.DeveloperFinalText = "October 4 diagnosis: the docket was constructed once; continuation messages never read the next slice."
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatal(err)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 || provider.DeveloperAttempts != 1 {
		t.Fatalf("reviews = %d, developer attempts = %d, want two reviews of one developer account", len(reviews), provider.DeveloperAttempts)
	}
	for i, request := range reviews {
		if !strings.Contains(request.Prompt, provider.DeveloperFinalText) {
			t.Errorf("review %d lost the completed developer diagnosis after replay", i)
		}
	}
	if !strings.Contains(reviews[1].Prompt, "candidate content or base has changed") {
		t.Fatal("replayed review did not qualify the earlier account's verification claims")
	}
}

func TestResumedReviewReceivesTheLatestDeveloperSummaryWithItsContext(t *testing.T) {
	for _, mode := range []string{"current", "missing", "earlier attempt", "changed content", "after repair"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			first.DeveloperFinalText = "Implemented the configuration and checked compatibility with the previous build."
			if mode == "after repair" {
				first.DeveloperFinalTextByAttempt = []string{"Original developer account.", "Repaired developer account."}
			}
			respond := first.Respond
			stop := stoppingReviewer(respond, first.ReviewerSession)
			first.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
				if mode == "after repair" && request.Role == domain.RoleReviewer && first.DeveloperAttempts == 1 {
					result, err := respond(request)
					result.FinalText = repairVerdict
					return result, err
				}
				return stop(request)
			}
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
			paused, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil || !paused.Paused {
				t.Fatalf("Run() = %#v, %v, want a stopped reviewer", paused, err)
			}
			state, err := store.Load(paused.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if state.DeveloperSummary == nil {
				t.Fatal("the completed developer account was not saved before review")
			}
			switch mode {
			case "missing":
				// A record written before summaries were retained has no binding.
				state.DeveloperSummary = nil
			case "earlier attempt":
				state.RepairAttempts++
			case "changed content":
				if err := os.WriteFile(filepath.Join(state.WorktreePath, "feature.txt"), []byte("changed after the account\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			second := orchestratortest.RoleBackend(func(backend.RunRequest) error {
				t.Error("resuming a stopped review invoked the developer")
				return nil
			}, approveVerdict)
			second.Respond = stoppingReviewer(second.Respond, second.ReviewerSession)
			pipeline = automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
			if resumed, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil || !resumed.Paused {
				t.Fatalf("resumed Run() = %#v, %v, want a stopped reviewer", resumed, err)
			}
			reviews := second.RequestsForRole(domain.RoleReviewer)
			if len(reviews) != 1 {
				t.Fatalf("resumed reviews = %d, want one", len(reviews))
			}
			prompt := reviews[0].Prompt
			if mode == "missing" {
				if strings.Contains(prompt, first.DeveloperFinalText) || !strings.Contains(prompt, "No final account is saved in this run's durable record.") {
					t.Fatal("resumption concealed the missing summary or its reason")
				}
				return
			}
			want := first.DeveloperFinalText
			if mode == "after repair" {
				want = first.DeveloperFinalTextByAttempt[1]
				earlier := strings.Index(prompt, "### Attempt 0: the developer's own account (untrusted)\n\n"+first.DeveloperFinalTextByAttempt[0])
				if earlier < 0 || earlier > strings.Index(prompt, want) {
					t.Fatal("re-adoption lost the account before the repair, or did not show it before the latest")
				}
			}
			if !strings.Contains(prompt, want) {
				t.Fatal("resumption lost the latest completed account")
			}
			if mode == "earlier attempt" && !strings.Contains(prompt, "This account is from an earlier attempt.") {
				t.Fatal("review was not told the account's attempt differs")
			}
			if mode == "changed content" && !strings.Contains(prompt, "candidate content or base has changed") {
				t.Fatal("review was not told the account's candidate differs")
			}
		})
	}
}

func TestADeveloperInvocationRetainsTheLatestAccountUntilItsReplacement(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	invocations := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		state, err := store.Load(request.RunID)
		if err != nil {
			return err
		}
		if invocations == 0 && state.DeveloperSummary != nil {
			t.Error("first invocation already has an account")
		}
		if invocations == 1 && (state.DeveloperSummary == nil || state.DeveloperSummary.Text != "Original account.") {
			t.Error("repair invocation lost the latest completed account before returning a replacement")
		}
		invocations++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{"Original account.", "Replacement account."}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatal(err)
	}
	if provider.DeveloperAttempts != 2 {
		t.Fatalf("developer attempts = %d, want a first invocation and a repair", provider.DeveloperAttempts)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	latest := strings.Index(reviews[1].Prompt, "## Developer's final summary (untrusted claims)")
	if latest < 0 || !strings.Contains(reviews[1].Prompt[latest:], "Replacement account.") || strings.Contains(reviews[1].Prompt[latest:], "Original account.") {
		t.Fatal("completed repair did not replace the previous account as the latest one for review")
	}
}

func TestTheDeveloperIsToldEachAttemptsSummaryStandsOnItsOwn(t *testing.T) {
	t.Parallel()
	if !strings.Contains(developerContract(scratchForTest, nil), "Each attempt's summary stands on its own.") {
		t.Fatal("the developer contract does not say each attempt's summary stands on its own")
	}
	repair, err := repairPrompt("", "", scratchForTest, nil, []runstate.Finding{{Severity: "major", Message: "fix"}}, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repair, "stands on its own rather than pointing to an earlier attempt's summary") {
		t.Fatal("the repair prompt does not ask for a summary that stands on its own")
	}
}

// On the waiting-reason work (yoyodyne-37h) a reviewer refused a sound change
// because the repair's summary said "as described in the previous attempt" and
// only the latest summary reached it. A second-attempt review carries both,
// each labelled with its attempt and as the developer's own untrusted account,
// the newest last.
func TestASecondAttemptReviewCarriesEveryAttemptsSummary(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(strings.Repeat("implemented\n", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"First attempt: the wait reason is read from the tracker because the run record cannot hold it.",
		"Second attempt: fixed the finding, as described in the previous attempt.",
	}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 {
		t.Fatalf("reviews = %d, want the original and repaired change", len(reviews))
	}
	if strings.Contains(reviews[0].Prompt, "Developer's summaries of earlier attempts") {
		t.Fatal("a first-attempt review was shown an earlier-attempts section")
	}
	prompt := reviews[1].Prompt
	untrusted := strings.Index(prompt, "# Untrusted review evidence")
	section := strings.Index(prompt, "## Developer's summaries of earlier attempts (untrusted claims)")
	first := strings.Index(prompt, "### Attempt 0: the developer's own account (untrusted)\n\n"+provider.DeveloperFinalTextByAttempt[0])
	latestHeading := strings.Index(prompt, "## Developer's final summary (untrusted claims)")
	latestLabel := strings.Index(prompt, "Latest completed developer account: recorded at attempt 1")
	second := strings.Index(prompt, provider.DeveloperFinalTextByAttempt[1])
	patch := strings.Index(prompt, "# The whole change under review")
	if untrusted < 0 || section < untrusted || first < section || latestHeading < first || latestLabel < latestHeading || second < latestLabel || patch < second {
		t.Fatalf("the second review did not carry both summaries, labelled and oldest first, inside the untrusted evidence:\n%s", prompt)
	}
}

func TestReviewReceivesAVisiblyBoundedDeveloperSummary(t *testing.T) {
	t.Parallel()
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "Implemented and checked compatibility. " + strings.Repeat("é", runstate.MaxRecordedTextBytes)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	want := runstate.RecordDeveloperSummary(provider.DeveloperFinalText)
	if state.DeveloperSummary == nil || state.DeveloperSummary.Text != want {
		t.Fatalf("recorded summary = %#v, want a bounded account", state.DeveloperSummary)
	}
	if !strings.Contains(provider.RequestsForRole(domain.RoleReviewer)[0].Prompt, want) {
		t.Fatal("review lost the summary's visible truncation marker")
	}
}
