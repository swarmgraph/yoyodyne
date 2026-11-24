package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// publishComparedDesign publishes one design through a pipeline whose stronger
// reviewer approves and whose cheaper reviewer asks for a repair, with
// compare_documents set as given.
func publishComparedDesign(t *testing.T, compare bool, whileDeciding func(*testing.T, *Pipeline, *runstate.Store) func(runID string)) (*orchestratortest.Backend, *runstate.Store, runstate.State, *recordingSpendLog) {
	t.Helper()
	_, _, provider, pipeline, runs := automaticFixture(t)
	log := &recordingSpendLog{}
	pipeline.Spend = log
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: testReviewerModel, Spend: log}
	pipeline.Config.Execution.ReviewerModels.CompareDocuments = compare
	during := func(string) {}
	if whileDeciding != nil {
		during = whileDeciding(t, &pipeline, runs)
	}
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		switch {
		case request.Role == domain.RoleArchitect:
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("recovery-design", "# Recovery\n\nThe owner's exact words.")}, nil
		case request.Role == domain.RoleReviewer:
			cheaper, cost := request.Model == "sonnet", 1.0
			if cheaper {
				cost = 0.25
			} else {
				during(request.RunID)
			}
			result, err := original(request)
			if err != nil {
				return result, err
			}
			if cheaper {
				result.FinalText = repairVerdict
			}
			result.LastEvent, err = emitTerminal(request, domain.RoleReviewer, cost, false)
			return result, err
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != 1 {
		t.Fatalf("runs = %+v, want one document run", recorded)
	}
	return provider, runs, recorded[0], log
}

func TestADocumentReviewIsNotComparedWhileTheSettingIsOff(t *testing.T) {
	t.Parallel()

	provider, runs, state, log := publishComparedDesign(t, false, nil)
	if state.Status != runstate.StatusSucceeded || len(state.ReviewComparisons) != 0 {
		t.Fatalf("run = %+v, want it landed with no second review", state)
	}
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 || reviews[0].Model != testReviewerModel {
		t.Fatalf("reviewer requests = %d, want the one deciding review", len(reviews))
	}
	if len(log.lines) != 1 || log.lines[0].Comparison {
		t.Fatalf("cost lines = %+v, want the deciding review's alone, not marked a comparison", log.lines)
	}
	listed, err := runs.ReviewComparisonRuns()
	if err != nil || len(listed) != 0 {
		t.Fatalf("ReviewComparisonRuns = %d, %v; want none", len(listed), err)
	}
}

// The cheaper reviewer asks for a repair of a design the stronger approves.
// The design lands on the stronger verdict, and the disagreement is recorded
// beside it and listed.
func TestASecondDocumentReviewThatDisagreesIsRecordedAndChangesNothing(t *testing.T) {
	t.Parallel()

	provider, runs, state, log := publishComparedDesign(t, true, nil)
	if state.Status != runstate.StatusSucceeded || state.Integration == nil || state.ReviewDecision != string(review.DecisionApprove) || state.ReviewModel != testReviewerModel {
		t.Fatalf("run = %+v, want it landed on the stronger reviewer's approval", state)
	}
	if state.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want the second review not counted as a round", state.ReviewRounds)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 2 || reviews[0].Model != testReviewerModel || reviews[1].Model != "sonnet" {
		t.Fatalf("reviewer requests = %+v, want the deciding review then the second on sonnet", reviews)
	}
	// The second reviewer is the reviewer role, never the document's author,
	// and starts its own session.
	if reviews[1].SessionID != "" || reviews[1].Role != domain.RoleReviewer {
		t.Fatalf("second review request = %+v, want a fresh reviewer session", reviews[1])
	}
	if len(state.ReviewComparisons) != 1 {
		t.Fatalf("comparisons = %+v, want one", state.ReviewComparisons)
	}
	comparison := state.ReviewComparisons[0]
	if comparison.DecidingModel != testReviewerModel || comparison.DecidingDecision != "approve" || comparison.CheaperModel != "sonnet" || comparison.CheaperDecision != "repair" || comparison.Agree() || comparison.HeadCommit == "" {
		t.Fatalf("comparison = %+v", comparison)
	}

	listing := readmodel.ReadReviewComparison(readmodel.Sources{Runs: runs, CompareDocumentReviews: true})
	if listing.Problem != "" || listing.Publications != 1 || listing.Compared != 1 || listing.Agreements != 0 || listing.CheaperApprovedRefused != 0 || len(listing.Entries) != 1 || listing.Entries[0].Document != "recovery-design" {
		t.Fatalf("listing = %+v", listing)
	}

	// The second review's cost is recorded and marked, so the reading of what
	// the spend accomplished counts the one review that decided.
	if len(log.lines) != 2 || log.lines[0].Comparison || !log.lines[1].Comparison || log.lines[1].Phase != runstate.SpendPhaseReview || log.lines[1].RunID != state.RunID {
		t.Fatalf("cost lines = %+v, want the deciding review then the comparison", log.lines)
	}
	for _, line := range log.lines {
		if err := line.Validate(); err != nil {
			t.Fatalf("cost line %+v: %v", line, err)
		}
	}
	// The run's price, read from its event log, counts both reviews as review
	// spend, and the log still holds the one verdict that decided.
	price, err := runs.Price(state.PricedAgainst())
	if err != nil || len(price.Runs) != 1 {
		t.Fatalf("Price() = %+v, %v", price, err)
	}
	if priced := price.Runs[0]; money(priced.Phases.Review) != (runstate.PhaseCost{CostUSD: 1.25, Invocations: 2}) || priced.CostUSD != 1.25 || priced.Incomplete != "" {
		t.Fatalf("run price = %+v, want both reviews in it", priced)
	}
	for _, eventType := range []execution.EventType{execution.EventReviewStarted, execution.EventReviewCompleted} {
		if events, _, err := runs.EventsOfType(state.RunID, eventType); err != nil || len(events) != 1 {
			t.Fatalf("%s events = %d, %v; want the deciding review's alone", eventType, len(events), err)
		}
	}
	day := state.StartedAt.AddDate(0, 0, -1).Format("2006-01-02")
	accomplished := readmodel.ReadAccomplishment(context.Background(), readmodel.AccomplishmentSources{
		Spend: recordedSpendLines(log.lines), Runs: runs,
		Now: func() time.Time { return state.StartedAt.AddDate(0, 0, 2) },
	}, readmodel.AccomplishmentRange{From: day, To: state.StartedAt.AddDate(0, 0, 1).Format("2006-01-02")})
	if len(accomplished.Reviews) != 1 || accomplished.Reviews[0].Outcome != readmodel.ReviewApprovedUnchanged || accomplished.Reviews[0].Invocations != 1 {
		t.Fatalf("accomplished reviews = %+v, want the deciding approval alone (%s)", accomplished.Reviews, accomplished.Problem)
	}
}

// A pause or a stop that arrives while the deciding review is being made buys
// no second one, and the publication lands on the verdict it already has.
func TestASecondDocumentReviewIsNotStartedOnceThePauseOrAStopIsAsked(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		during func(*testing.T, *Pipeline, *runstate.Store) func(string)
		reason string
	}{
		"pause": {func(t *testing.T, pipeline *Pipeline, _ *runstate.Store) func(string) {
			holds := newOperatorHoldStore(t)
			pipeline.Holds = holds
			return func(string) {
				if _, err := holds.Hold(time.Now()); err != nil {
					t.Errorf("Hold() error = %v", err)
				}
			}
		}, "the operator has paused harness activity"},
		"unreadable pause": {func(_ *testing.T, pipeline *Pipeline, _ *runstate.Store) func(string) {
			holds := &changingHolds{}
			pipeline.Holds = holds
			return func(string) { holds.unreadable.Store(true) }
		}, "the hold could not be read"},
		"stop": {func(t *testing.T, _ *Pipeline, runs *runstate.Store) func(string) {
			return func(runID string) {
				if err := runs.RecordStop(runstate.StopRequest{SchemaVersion: runstate.StopSchemaVersion, ProductID: "yoyodyne", RunID: runID, WorkItemID: runID, RequestedAt: time.Now(), Reason: "no longer wanted"}); err != nil {
					t.Errorf("RecordStop() error = %v", err)
				}
			}
		}, "stopped this run"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			provider, _, state, log := publishComparedDesign(t, true, test.during)
			if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 1 || len(log.lines) != 1 {
				t.Fatalf("made %d reviews over %d cost lines, want the deciding review alone", len(reviews), len(log.lines))
			}
			if state.Status != runstate.StatusSucceeded || state.Integration == nil || state.ReviewDecision != string(review.DecisionApprove) {
				t.Fatalf("run = %+v, want it landed on the deciding approval", state)
			}
			if len(state.ReviewComparisons) != 1 || state.ReviewComparisons[0].Compared() || !strings.Contains(state.ReviewComparisons[0].Problem, test.reason) {
				t.Fatalf("comparisons = %+v, want one with no verdict because %q", state.ReviewComparisons, test.reason)
			}
		})
	}
}

// changingHolds is a hold record that stops being readable part-way.
type changingHolds struct{ unreadable atomic.Bool }

func (h *changingHolds) Held() (runstate.OperatorHold, bool, error) {
	if h.unreadable.Load() {
		return runstate.OperatorHold{}, false, errors.New("the hold could not be read")
	}
	return runstate.OperatorHold{}, false, nil
}

type recordedSpendLines []runstate.Spend

func (l recordedSpendLines) List() ([]runstate.Spend, error) { return l, nil }
