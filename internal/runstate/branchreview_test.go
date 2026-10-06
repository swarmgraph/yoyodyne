package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const branchReviewID = "review-0123456789abcdef0123456789abcdef"

func newBranchReview() BranchReview {
	return BranchReview{
		SchemaVersion: BranchReviewSchemaVersion,
		ReviewID:      branchReviewID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Branch:        "milestone",
		BaseRef:       "main",
		BaseCommit:    "1111111111111111111111111111111111111111",
		HeadCommit:    "2222222222222222222222222222222222222222",
		Commits:       3,
		ReviewedAt:    time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		SessionID:     "review-session",
		Model:         "opus",
		ResolvedModel: "claude-opus-5",
		Decision:      ReviewApprove,
		Summary:       "the commits agree with one another",
	}
}

func TestBranchReviewStoreKeepsEveryVerdictItWasGiven(t *testing.T) {
	t.Parallel()

	store, err := NewBranchReviewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	if reviews, err := store.List(); err != nil || len(reviews) != 0 {
		// A product whose branches have never been reviewed is not a read failure.
		t.Fatalf("List() over an empty store = %v, %v", reviews, err)
	}

	approved := newBranchReview()
	repaired := newBranchReview()
	repaired.Decision = ReviewRepair
	repaired.Summary = "the durable evidence is written in two places"
	repaired.Findings = []Finding{{Severity: SeverityMajor, Message: "one commit writes the record and the next reads another key", File: "reader.go", Line: 8}}
	repaired.Truncated = true
	repaired.CommitsOmitted = 4
	for _, reviewed := range []BranchReview{approved, repaired} {
		if err := store.Append(reviewed); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	reviews, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("List() = %#v", reviews)
	}
	// The session and model evidence a per-item review records survives here too:
	// a recorded verdict nobody can attribute is not audit evidence.
	if reviews[0].SessionID != "review-session" || reviews[0].Model != "opus" || reviews[0].ResolvedModel != "claude-opus-5" {
		t.Errorf("recorded evidence = %#v", reviews[0])
	}
	if !reviews[0].Approved() {
		t.Error("an approving verdict did not read back as approved")
	}
	if reviews[1].Approved() {
		t.Error("a repair verdict read back as an approval")
	}
	if len(reviews[1].Findings) != 1 || reviews[1].Findings[0].File != "reader.go" || reviews[1].CommitsOmitted != 4 {
		t.Errorf("recorded repair = %#v", reviews[1])
	}
	if !strings.HasSuffix(store.Path(), filepath.Join("projects", "yoyodyne", "state", "branch-reviews", "reviews.jsonl")) {
		t.Errorf("Path() = %q", store.Path())
	}
}

// A branch review is a provider invocation, so it records the event stream every
// other one records: it can be followed while it runs, and what it cost can be
// read back afterwards. The log is its own, named for the review rather than for
// a run, which is what keeps a verdict on settled work out of a run's evidence.
func TestBranchReviewStoreKeepsTheEventStreamOfItsInvocation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewBranchReviewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	if events, err := store.LoadEvents(branchReviewID); err != nil || events != nil {
		t.Fatalf("LoadEvents() of an unreviewed branch = %#v, %v", events, err)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		event, err := execution.NewEvent(branchReviewID, sequence, time.Now(), execution.EventReviewStarted, "harness.review", map[string]any{"scope": "branch"})
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := store.AppendEvent(event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	events, err := store.LoadEvents(branchReviewID)
	if err != nil || len(events) != 3 {
		t.Fatalf("LoadEvents() = %#v, %v", events, err)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) || event.RunID != branchReviewID {
			t.Errorf("events[%d] = %#v", index, event)
		}
	}
	// The stream is where `yoyo status --follow` looks for it: this store's own directory,
	// named for the review, beside the runs rather than among them.
	expected := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "branch-reviews", branchReviewID+".events.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("event log is not at %q: %v", expected, err)
	}
	// An identifier that is not a branch review's cannot name a log here, so
	// nothing can aim this store at a run's event stream.
	if err := store.AppendEvent(execution.Event{
		SchemaVersion: execution.EventSchemaVersion,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		Sequence:      1,
		Timestamp:     time.Now().UTC(),
		Type:          execution.EventReviewStarted,
		Source:        "harness.review",
		Payload:       []byte(`{}`),
	}); err == nil || !strings.Contains(err.Error(), "branch review id is invalid") {
		t.Errorf("AppendEvent() for a run id error = %v", err)
	}
}

func TestBranchReviewStoreRefusesWhatCannotDescribeAReview(t *testing.T) {
	t.Parallel()

	store, err := NewBranchReviewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	for name, mutate := range map[string]func(*BranchReview){
		"another product's review":   func(b *BranchReview) { b.ProductID = "elsewhere" },
		"an unnamed branch":          func(b *BranchReview) { b.Branch = "refs/heads/milestone" },
		"an invalid base commit":     func(b *BranchReview) { b.BaseCommit = "main" },
		"a base that is the head":    func(b *BranchReview) { b.BaseCommit = b.HeadCommit },
		"a branch with no commits":   func(b *BranchReview) { b.Commits = 0 },
		"a decision nothing decided": func(b *BranchReview) { b.Decision = "maybe" },
		"a verdict with no summary":  func(b *BranchReview) { b.Summary = "" },
		"a repair with no finding": func(b *BranchReview) {
			b.Decision = ReviewRepair
			b.Findings = nil
		},
		// A review that answered nothing has to say what stopped it, or it is
		// indistinguishable from a branch nobody reviewed.
		"a silent absent verdict": func(b *BranchReview) {
			b.Decision = ""
			b.Summary = ""
		},
	} {
		reviewed := newBranchReview()
		mutate(&reviewed)
		if err := store.Append(reviewed); err == nil {
			t.Errorf("Append() accepted %s", name)
		}
	}

	// A review that failed is recorded, because "this branch was reviewed and
	// the review did not answer" is itself what an operator needs.
	failed := newBranchReview()
	failed.Decision = ""
	failed.Summary = ""
	failed.Failure = "the reviewer backend failed: claude is not installed"
	if err := store.Append(failed); err != nil {
		t.Fatalf("Append() of a failed review error = %v", err)
	}
	reviews, err := store.List()
	if err != nil || len(reviews) != 1 || reviews[0].Approved() {
		t.Fatalf("List() = %#v, %v", reviews, err)
	}
}

// A shadow review is recorded in the same log as every other, because it is a
// provider invocation that happened and cost money. What separates it is what
// its verdict is allowed to mean, and that has to survive the round trip: the
// record outlives the process that made it, and is what a later reader asks.
func TestAShadowVerdictReadsBackAsApprovingNothing(t *testing.T) {
	t.Parallel()

	store, err := NewBranchReviewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	shadowed := newBranchReview()
	shadowed.Shadow = true
	shadowed.Model = "sonnet"
	if shadowed.Approved() {
		t.Fatal("a shadow verdict approved the branch before it was even recorded")
	}
	if err := store.Append(shadowed); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	reviews, err := store.List()
	if err != nil || len(reviews) != 1 {
		t.Fatalf("List() = %#v, %v", reviews, err)
	}
	if !reviews[0].Shadow || reviews[0].Decision != ReviewApprove {
		t.Fatalf("recorded shadow review = %#v", reviews[0])
	}
	if reviews[0].Approved() {
		t.Error("a recorded shadow verdict read back as an approval of the branch")
	}
}

// A branch review belongs to no run, so what it cost is read from the event log
// this store keeps rather than a run's. It is the same evidence and the same
// answer to a review whose log is gone: unknown rather than free.
func TestBranchReviewStorePricesAReviewFromItsOwnEventLog(t *testing.T) {
	t.Parallel()

	store, err := NewBranchReviewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	if price := store.Price(branchReviewID); price.Known() {
		t.Errorf("a review with no event log was priced: %#v", price)
	}
	for _, event := range []struct {
		sequence  uint64
		eventType execution.EventType
		payload   any
	}{
		{1, execution.EventReviewStarted, map[string]any{"scope": "branch"}},
		{2, execution.EventRunCompleted, map[string]any{"session_id": "review-session", "total_cost_usd": 1.25}},
	} {
		created, err := execution.NewEvent(branchReviewID, event.sequence, time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC), event.eventType, "claude-code", event.payload)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := store.AppendEvent(created); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	price := store.Price(branchReviewID)
	if !price.Known() {
		t.Fatalf("Price() = %#v", price)
	}
	if price.CostUSD != 1.25 || price.Invocations != 1 || price.ReviewID != branchReviewID {
		t.Errorf("Price() = %#v", price)
	}
	// An identifier that is not a branch review's cannot reach a log here, so
	// pricing cannot be aimed at a run's event stream.
	if priced := store.Price("run-0123456789abcdef0123456789abcdef"); priced.Known() {
		t.Errorf("Price() of a run id = %#v", priced)
	}
}
