package runstate

// What the open batch is waiting on, and how an item's state on the batch route
// is said, in the one set of words every surface uses: `yoyo status`, the
// dashboard and the item's notes all say what these functions return. The
// settings the answer depends on arrive as plain values in BatchWaitInput,
// because this package does not read the configuration.

import (
	"fmt"
	"strings"
	"time"
)

// BatchWaitKind is what a batch is held up by.
type BatchWaitKind string

const (
	// BatchWaitsNothing means nothing holds it: the next forge check takes the
	// next step, or there is nothing to publish.
	BatchWaitsNothing BatchWaitKind = ""
	// BatchWaitsReviewWindow is the review window being closed.
	BatchWaitsReviewWindow BatchWaitKind = "the review window"
	// BatchWaitsThreshold is too few unpublished items, none of them yet old enough.
	BatchWaitsThreshold BatchWaitKind = "the threshold"
	// BatchWaitsAutomatedReviewer is the review tool not yet having reviewed the head.
	BatchWaitsAutomatedReviewer BatchWaitKind = "the automated reviewer"
	// BatchWaitsApproval is a person's approval on the head.
	BatchWaitsApproval BatchWaitKind = "a person's approval"
	// BatchWaitsMergeable is the forge reporting the pull request mergeable.
	BatchWaitsMergeable BatchWaitKind = "the forge reporting it mergeable"
)

// BatchWaitInput is what the answer depends on besides the record.
type BatchWaitInput struct {
	Now time.Time
	// WindowOpen says the review window is open now; true where none is set.
	WindowOpen bool
	// WindowOpensAt is when it next opens, in the words a person reads it in,
	// and empty where that is not known.
	WindowOpensAt string
	// MinItems and MaxWait are the threshold: that many unpublished items, or one
	// that has waited that long.
	MinItems int
	MaxWait  time.Duration
	// Unpublished is how many items on the target branch are in no open batch,
	// and OldestSince is when the longest-waiting of them began waiting.
	Unpublished int
	OldestSince time.Time
	// AutomatedReviewer is the account whose review is waited for, empty for none.
	AutomatedReviewer string
	// RequestConfigured says a review request is to be sent for a ready batch.
	RequestConfigured bool
}

// BatchWait is what the batch for one target branch waits on.
type BatchWait struct {
	Kind BatchWaitKind `json:"kind,omitempty"`
	// Says is the whole sentence, ready for a surface.
	Says string `json:"says"`
}

// BatchSpan says a length of time in ordinary words: "3 days 2 hours",
// "45 minutes".
func BatchSpan(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	total := int(d.Round(time.Minute) / time.Minute)
	days, hours, minutes := total/(24*60), total/60%24, total%60
	var parts []string
	unit := func(n int, name string) {
		if n > 0 {
			word := name
			if n != 1 {
				word += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, word))
		}
	}
	unit(days, "day")
	unit(hours, "hour")
	if days == 0 {
		unit(minutes, "minute")
	}
	return strings.Join(parts, " ")
}

// RequestSentFor reports a review request already sent for the commit.
func (b BatchRecord) RequestSentFor(head string) bool {
	for _, request := range b.Requests {
		if request.Head == head && request.SentAt != nil {
			return true
		}
	}
	return false
}

// reviewerDone reports the automated reviewer finished with the head, or was
// passed over for it.
func (b BatchRecord) reviewerDone() bool {
	r := b.Reviewer
	return r != nil && r.Head == b.Head && (r.State == BatchReviewerReviewed || r.State == BatchReviewerPassedOver)
}

// WaitingOn says what the batch for this record's target branch waits on. A
// nil record is a target branch with no batch yet.
func WaitingOn(record *BatchRecord, in BatchWaitInput) BatchWait {
	if record == nil || !record.Open() {
		return waitingToOpen(in)
	}
	if record.Approved() {
		if m := record.Mergeable; m != nil && m.Head == record.Head && m.Mergeable {
			return BatchWait{Says: fmt.Sprintf("Pull request #%d is approved on its head commit and the forge reports it mergeable; the next check of the forge merges it.", record.PullRequest.Number)}
		}
		says := fmt.Sprintf("Pull request #%d is approved and is waiting on the forge reporting it mergeable", record.PullRequest.Number)
		if m := record.Mergeable; m != nil && m.Head == record.Head && strings.TrimSpace(m.Missing) != "" {
			says += ": " + strings.TrimRight(strings.TrimSpace(m.Missing), ".")
		}
		return BatchWait{Kind: BatchWaitsMergeable, Says: says + "."}
	}
	number := record.PullRequest.Number
	switch {
	case in.AutomatedReviewer != "" && !record.reviewerDone():
		return BatchWait{Kind: BatchWaitsAutomatedReviewer, Says: fmt.Sprintf("Pull request #%d is waiting on %s, the automated reviewer, to review its head commit; it is passed over two hours after the head was pushed.", number, in.AutomatedReviewer)}
	case in.RequestConfigured && !in.WindowOpen && !record.RequestSentFor(record.Head):
		return BatchWait{Kind: BatchWaitsReviewWindow, Says: fmt.Sprintf("Pull request #%d is waiting on the review window to ask a person to review it%s.", number, opensAt(in))}
	}
	return BatchWait{Kind: BatchWaitsApproval, Says: fmt.Sprintf("Pull request #%d is waiting on a person's approval of its head commit.", number)}
}

func opensAt(in BatchWaitInput) string {
	if in.WindowOpensAt == "" {
		return ""
	}
	return "; it opens " + in.WindowOpensAt
}

func waitingToOpen(in BatchWaitInput) BatchWait {
	switch {
	case in.Unpublished == 0:
		return BatchWait{Says: "No batch pull request is open, and no item is waiting to be published."}
	case !in.WindowOpen:
		return BatchWait{Kind: BatchWaitsReviewWindow, Says: fmt.Sprintf("No batch pull request is open; %s unpublished and waiting on the review window%s.", itemsAre(in.Unpublished), opensAt(in))}
	}
	waited := in.Now.Sub(in.OldestSince)
	if in.Unpublished < in.MinItems && waited < in.MaxWait {
		return BatchWait{Kind: BatchWaitsThreshold, Says: fmt.Sprintf("No batch pull request is open; %s unpublished and waiting on the threshold: %d items are needed or the oldest has to have waited %s, and it has waited %s.",
			itemsAre(in.Unpublished), in.MinItems, BatchSpan(in.MaxWait), BatchSpan(waited))}
	}
	return BatchWait{Says: fmt.Sprintf("No batch pull request is open; %s unpublished and the threshold is met, so the next check of the forge opens one.", itemsAre(in.Unpublished))}
}

func itemsAre(n int) string {
	if n == 1 {
		return "1 item is"
	}
	return fmt.Sprintf("%d items are", n)
}
