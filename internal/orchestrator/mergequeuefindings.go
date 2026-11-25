package orchestrator

// A change the merge queue's reviewer sent back, as its repair is handed it:
// every finding of that review, kept with the candidate's generation, carried on
// the hand-back, and recorded on the run beside the bounded reason
// (runstate.CheckFailure.QueueReview).

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// queueReviewCommand is what a run records as having failed where no check did.
const queueReviewCommand = "the merge queue candidate's review"

// queueReviewSummaryBound is what a generation's record takes of a summary.
const queueReviewSummaryBound = 4 << 10

const queueReviewSummaryCut = "\n[cut; the rest of this summary was not recorded]"

// boundedReviewSummary is a review's summary within the generation's bound,
// saying where it was cut.
func boundedReviewSummary(summary string) string {
	if len(summary) <= queueReviewSummaryBound {
		return summary
	}
	cut := queueReviewSummaryBound - len(queueReviewSummaryCut)
	for cut > 0 && (summary[cut]&0xC0) == 0x80 {
		cut--
	}
	return summary[:cut] + queueReviewSummaryCut
}

// queueReviewWithoutFindings reports a hand-back over the queue's review made by
// a build that kept only the review's summary.
func queueReviewWithoutFindings(failure runstate.CheckFailure) bool {
	return failure.QueueReview == nil && failure.Command == queueReviewCommand && strings.Contains(failure.Output, "the independent reviewer decided ")
}

// writeQueueReview writes the repair a merge queue's review asks for, after the
// contract and the invariants every repair prompt opens with.
func writeQueueReview(prompt *strings.Builder, review runstate.ReviewBefore, failure runstate.CheckFailure, attempt, limit int) {
	prompt.WriteString("# The merge queue's review: repair required\n\n")
	fmt.Fprintf(prompt, "Your change was approved and queued to land, and the independent reviewer of the merge queue's candidate, commit %s, which is the target branch with your change merged onto it, decided %q. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and resolve every finding below.\n\n", review.ReviewedCommit, review.Decision, attempt, limit)
	if summary := strings.TrimSpace(review.Summary); summary != "" {
		prompt.WriteString("Reviewer summary: " + summary + "\n\n")
	}
	if len(review.Findings) == 0 {
		prompt.WriteString("No findings are recorded beside that review.\n\n")
	} else if encoded, err := json.MarshalIndent(review.Findings, "", "  "); err == nil {
		prompt.WriteString("Every finding of that review:\n\n")
		prompt.Write(encoded)
		prompt.WriteString("\n\n")
	}
	if failure.Output != "" {
		prompt.WriteString("The queue's own account, which repeats the summary above within a limit of its own:\n\n```\n" + failure.Output + "\n```\n\n")
	}
	prompt.WriteString("Fix each finding, re-run the relevant checks, and finish with a concise summary of the whole change that stands on its own rather than pointing to an earlier attempt's summary. If a finding is wrong, say why in your summary rather than leaving it unaddressed. The harness checks and reviews the repaired change again before it is queued.")
}
