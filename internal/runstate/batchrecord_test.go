package runstate

import (
	"strings"
	"testing"
	"time"
)

func batchCommit(c string) string { return strings.Repeat(c, 40) }

var batchMoment = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func openBatch() BatchRecord {
	return BatchRecord{
		TargetBranch: "staging", UpstreamBranch: "main", BatchBranch: "yoyodyne/batch/staging",
		PullRequest: &BatchPullRequestRef{Number: 12, OpenedAt: batchMoment},
		Head:        batchCommit("a"),
		Items:       []BatchRecordItem{{WorkItemID: "ex-1", LandingCommit: batchCommit("1")}},
		UpdatedAt:   batchMoment,
	}
}

func TestBatchRecordRoundTripsAndBoundsItsText(t *testing.T) {
	store, err := NewBatchRecordStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load("staging"); err != nil || found {
		t.Fatalf("Load() before any save = %t, %v; want none", found, err)
	}
	record := openBatch()
	sent := batchMoment
	record.Requests = []BatchRequestSent{{Head: record.Head, Why: "opened", SentAt: &sent, LastErr: strings.Repeat("x", 3*MaxRecordedTextBytes)}}
	record.Description = []BatchDescriptionTry{{Head: record.Head, At: batchMoment, Posted: true}}
	record.Reviewer = &BatchReviewer{Account: "reviewbot[bot]", Head: record.Head, State: BatchReviewerWaiting, At: batchMoment}
	record.Approval = &BatchApproval{By: "pat", Commit: record.Head, At: batchMoment}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, found, err := store.Load("staging")
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v", found, err)
	}
	if got.PullRequest.Number != 12 || got.Head != record.Head || len(got.Items) != 1 || !got.Approved() || !got.RequestSentFor(record.Head) || len(got.Description) != 1 || got.Reviewer.State != BatchReviewerWaiting {
		t.Fatalf("Load() = %#v; want what was saved", got)
	}
	if len(got.Requests[0].LastErr) > MaxRecordedTextBytes {
		t.Fatalf("a request error is %d bytes; want it bounded", len(got.Requests[0].LastErr))
	}
	all, err := store.All()
	if err != nil || len(all) != 1 || all[0].TargetBranch != "staging" {
		t.Fatalf("All() = %v, %v", all, err)
	}
}

func TestBatchRecordRefusesWhatCannotBeTrue(t *testing.T) {
	for name, change := range map[string]func(*BatchRecord){
		"a pull request with no head": func(b *BatchRecord) { b.Head = "" },
		"a bad head":                  func(b *BatchRecord) { b.Head = "nope" },
		"merged and closed":           func(b *BatchRecord) { b.Merged = &BatchEnded{At: batchMoment}; b.Closed = &BatchEnded{At: batchMoment} },
		"an unknown reviewer state": func(b *BatchRecord) {
			b.Reviewer = &BatchReviewer{Account: "a", Head: batchCommit("a"), State: "thinking", At: batchMoment}
		},
		"an item with no commit": func(b *BatchRecord) { b.Items = []BatchRecordItem{{WorkItemID: "ex-1"}} },
	} {
		record := openBatch()
		change(&record)
		if err := record.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want a refusal", name)
		}
	}
}

func TestAnItemsStateOnTheBatchRoute(t *testing.T) {
	record := openBatch()
	carried, other := "ex-1", "ex-2"
	if got := record.StateOf(other, batchCommit("2")); got != BatchUnpublished {
		t.Errorf("an item the batch does not carry is %q, want unpublished", got)
	}
	if got := record.StateOf(carried, batchCommit("1")); got != BatchInOpenBatch {
		t.Errorf("a carried item is %q, want in the open batch", got)
	}
	record.Approval = &BatchApproval{Commit: batchCommit("9"), At: batchMoment}
	if got := record.StateOf(carried, batchCommit("1")); got != BatchInOpenBatch {
		t.Errorf("an approval of an older head leaves the item %q, want in the open batch", got)
	}
	record.Approval.Commit = record.Head
	if got := record.StateOf(carried, batchCommit("1")); got != BatchApproved {
		t.Errorf("an approval on the head leaves the item %q, want approved", got)
	}
	record.Merged = &BatchEnded{At: batchMoment}
	if got := record.StateOf(carried, batchCommit("1")); got != BatchMerged {
		t.Errorf("after the merge the item is %q, want merged", got)
	}
	record.Merged, record.Closed = nil, &BatchEnded{At: batchMoment}
	if got := record.StateOf(carried, batchCommit("1")); got != BatchUnpublished {
		t.Errorf("after a close without merging the item is %q, want unpublished", got)
	}
}

func TestWhatTheBatchWaitsOn(t *testing.T) {
	in := BatchWaitInput{Now: batchMoment, WindowOpen: true, MinItems: 5, MaxWait: 24 * time.Hour, Unpublished: 3, OldestSince: batchMoment.Add(-2 * time.Hour)}
	for _, test := range []struct {
		name   string
		record func() *BatchRecord
		change func(*BatchWaitInput)
		kind   BatchWaitKind
		says   string
	}{
		{"nothing to publish", func() *BatchRecord { return nil }, func(i *BatchWaitInput) { i.Unpublished = 0 }, BatchWaitsNothing, "no item is waiting to be published"},
		{"window closed", func() *BatchRecord { return nil }, func(i *BatchWaitInput) { i.WindowOpen, i.WindowOpensAt = false, "on Monday 09:00 PST" }, BatchWaitsReviewWindow, "waiting on the review window; it opens on Monday 09:00 PST"},
		{"below the threshold", func() *BatchRecord { return nil }, func(*BatchWaitInput) {}, BatchWaitsThreshold, "3 items are unpublished and waiting on the threshold: 5 items are needed or the oldest has to have waited 1 day, and it has waited 2 hours"},
		{"threshold met by count", func() *BatchRecord { return nil }, func(i *BatchWaitInput) { i.Unpublished = 5 }, BatchWaitsNothing, "the threshold is met"},
		{"threshold met by age", func() *BatchRecord { return nil }, func(i *BatchWaitInput) { i.OldestSince = batchMoment.Add(-25 * time.Hour) }, BatchWaitsNothing, "the threshold is met"},
		{"the automated reviewer", func() *BatchRecord { r := openBatch(); return &r }, func(i *BatchWaitInput) { i.AutomatedReviewer = "reviewbot[bot]" }, BatchWaitsAutomatedReviewer, "#12 is waiting on reviewbot[bot]"},
		{"the reviewer finished", func() *BatchRecord {
			r := openBatch()
			r.Reviewer = &BatchReviewer{Account: "reviewbot[bot]", Head: r.Head, State: BatchReviewerReviewed, At: batchMoment}
			return &r
		}, func(i *BatchWaitInput) { i.AutomatedReviewer = "reviewbot[bot]" }, BatchWaitsApproval, "waiting on a person's approval"},
		{"the reviewer reviewed an older head", func() *BatchRecord {
			r := openBatch()
			r.Reviewer = &BatchReviewer{Account: "reviewbot[bot]", Head: batchCommit("f"), State: BatchReviewerReviewed, At: batchMoment}
			return &r
		}, func(i *BatchWaitInput) { i.AutomatedReviewer = "reviewbot[bot]" }, BatchWaitsAutomatedReviewer, "reviewbot[bot]"},
		{"the window before a request", func() *BatchRecord { r := openBatch(); return &r }, func(i *BatchWaitInput) { i.RequestConfigured, i.WindowOpen = true, false }, BatchWaitsReviewWindow, "to ask a person to review it"},
		{"a person", func() *BatchRecord { r := openBatch(); return &r }, func(*BatchWaitInput) {}, BatchWaitsApproval, "waiting on a person's approval of its head commit"},
		{"approved, not yet mergeable", func() *BatchRecord {
			r := openBatch()
			r.Approval = &BatchApproval{Commit: r.Head, At: batchMoment}
			r.Mergeable = &BatchMergeable{Head: r.Head, Mergeable: false, Missing: "the required check test has not passed.", ReadAt: batchMoment}
			return &r
		}, func(*BatchWaitInput) {}, BatchWaitsMergeable, "the forge reporting it mergeable: the required check test has not passed."},
		{"approved and mergeable", func() *BatchRecord {
			r := openBatch()
			r.Approval = &BatchApproval{Commit: r.Head, At: batchMoment}
			r.Mergeable = &BatchMergeable{Head: r.Head, Mergeable: true, ReadAt: batchMoment}
			return &r
		}, func(*BatchWaitInput) {}, BatchWaitsNothing, "the next check of the forge merges it"},
	} {
		input := in
		test.change(&input)
		got := WaitingOn(test.record(), input)
		if got.Kind != test.kind || !strings.Contains(got.Says, test.says) {
			t.Errorf("%s: WaitingOn() = %q %q, want kind %q and %q in it", test.name, got.Kind, got.Says, test.kind, test.says)
		}
	}
}

func TestBatchSpanIsOrdinaryWords(t *testing.T) {
	for in, want := range map[time.Duration]string{
		10 * time.Second: "under a minute", 45 * time.Minute: "45 minutes", time.Hour: "1 hour", 26 * time.Hour: "1 day 2 hours", 3 * 24 * time.Hour: "3 days",
	} {
		if got := BatchSpan(in); got != want {
			t.Errorf("BatchSpan(%s) = %q, want %q", in, got, want)
		}
	}
}
