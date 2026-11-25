package orchestrator

// What a merge queue candidate's reviewer is given from the record of the run
// whose change the candidate carries: the developer's summaries, and the
// summary of the review that approved the change. A candidate carries one run's
// change. Where the record holds none of them the reviewer is told that, so it
// never has to guess whether one exists.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// candidateDeveloperSummary is the developer's latest account on the run's
// record and what the candidate's reviewer is told of it.
func candidateDeveloperSummary(run runstate.State) (string, string) {
	switch {
	case run.Document != nil:
		return "", "The owning role wrote this document in its conversation. The harness confirmed and copied it unchanged; there is no developer."
	case run.OutsideFile != nil:
		return "", "The harness fetched this file and wrote its record's harness fields; there is no developer."
	}
	summary := run.DeveloperSummary
	if summary == nil || strings.TrimSpace(summary.Text) == "" {
		return "", fmt.Sprintf("Run %s, whose change this candidate carries, has no developer summary on its record. None was withheld from this review.", run.RunID)
	}
	context := fmt.Sprintf("The developer's account recorded on run %s, at attempt %d, of the change before the queue merged it onto the target. Its verification claims do not establish verification of this candidate.", run.RunID, summary.Attempt)
	if cut := summary.CutStatement(); cut != "" {
		context += " " + cut
	}
	return summary.Text, context
}

// recordedEarlierSummaries is a run's record of each earlier attempt's account,
// as a review request carries it. A change the harness wrote has no developer,
// so it has none.
func recordedEarlierSummaries(run runstate.State) []review.AttemptSummary {
	if run.HarnessAuthored() {
		return nil
	}
	var earlier []review.AttemptSummary
	for _, summary := range run.EarlierDeveloperSummaries {
		earlier = append(earlier, review.AttemptSummary{Attempt: summary.Attempt, Text: summary.Text, Cut: summary.CutStatement()})
	}
	return earlier
}

// candidateApprovingReview is the summary of the run's own review that approved
// the change. Only an approval is shown: what a review that did not approve
// said is never presented as one.
func candidateApprovingReview(run runstate.State) string {
	if run.ReviewDecision != runstate.ReviewApprove {
		return fmt.Sprintf("Run %s has no approving review on its record.", run.RunID)
	}
	said := fmt.Sprintf("The review of run %s approved the change", run.RunID)
	if run.ReviewApproves != "" {
		said += " as " + run.ReviewApproves
	}
	if summary := strings.TrimSpace(run.ReviewSummary); summary != "" {
		return said + ". Its summary:\n\n" + summary
	}
	return said + ", and no summary of that review is on the run's record."
}
