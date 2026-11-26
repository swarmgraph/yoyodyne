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
// said is never presented as one. An entry admitted on the approval of the head
// it resolves shows that approval, of the head before the resolution, and then
// the conflicts and how each was resolved, which no reviewer has seen
// (resolutionReview).
func candidateApprovingReview(entry runstate.MergeQueueEntry, run runstate.State) string {
	if entry.ResolvedHead != "" {
		approval := fmt.Sprintf("The run's record no longer holds the review that approved head %s.", entry.ApprovedHead)
		if r := run.ConflictResolution; r != nil && r.ResolvedHead == entry.ResolvedHead && r.Approved.HeadCommit == entry.ApprovedHead {
			approval = fmt.Sprintf("The review of run %s approved head %s, before the resolution", run.RunID, entry.ApprovedHead)
			if r.Approved.Approves != "" {
				approval += ", as " + r.Approved.Approves
			}
			if summary := strings.TrimSpace(r.Approved.Summary); summary != "" {
				approval += ". Its summary:\n\n" + summary
			} else {
				approval += ", and no summary of that review is on the run's record."
			}
		}
		return approval + "\n\n" + resolutionReview(entry, run)
	}
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

// headNamed names the head an entry's candidate is built from, without calling
// a resolved head approved: only the candidate's own review approves that.
func headNamed(entry runstate.MergeQueueEntry) string {
	if entry.ResolvedHead == "" {
		return "the approved head " + entry.ApprovedHead
	}
	return fmt.Sprintf("the resolved head %s, which resolves a conflict of the approved head %s", entry.ResolvedHead, entry.ApprovedHead)
}

// headLines are the head lines of a candidate's commit message.
func headLines(entry runstate.MergeQueueEntry) string {
	lines := "Approved head: " + entry.ApprovedHead + "\n"
	if entry.ResolvedHead != "" {
		lines += "Resolved head: " + entry.ResolvedHead + "\n"
	}
	return lines
}

// candidateAdmittedSays is how a candidate's reviewer is told what was
// admitted and what the candidate is.
func candidateAdmittedSays(entry runstate.MergeQueueEntry, generation runstate.MergeQueueGeneration) string {
	admitted := fmt.Sprintf("The change for this work item was approved at head `%s` and admitted to the merge queue for `%s`.", entry.ApprovedHead, generation.TargetBranch)
	if entry.ResolvedHead != "" {
		admitted = fmt.Sprintf("The change for this work item was approved at head `%s`, handed back because it no longer merged onto `%s`, and resolved at head `%s`, which no reviewer has approved; it was admitted to the merge queue again on the approval of the head it resolves.", entry.ApprovedHead, generation.TargetBranch, entry.ResolvedHead)
	}
	return admitted + fmt.Sprintf(" The candidate under review is commit `%s`: the target branch at `%s` with head `%s` merged onto it. It is the exact revision that would land, and nothing else is.",
		generation.Candidate, generation.TargetBase, entry.Head())
}
