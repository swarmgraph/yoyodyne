package runstate

// A document publication's review made a second time on the cheaper model
// (execution.reviewer_models.compare_documents;
// docs/designs/reducing-what-the-factory-costs.md, section 5).
//
// The second review is recorded beside the deciding one and decides nothing:
// the stronger reviewer's verdict alone is what the run acts on. The record is
// what the trial is read from, so the development manager can see whether the
// cheaper reviewer refused what the stronger refused before moving document
// reviews onto it.

import "time"

// MaxReviewComparisons bounds how many second reviews one run keeps. A
// document run is reviewed once per attempt, and its attempts are few; past
// the bound the oldest is dropped, because the trial compares verdicts and the
// latest rounds are the ones the run landed or stopped on.
const MaxReviewComparisons = 16

// ReviewComparison is one deciding review of a document publication and the
// second review of the same change made on the cheaper model.
type ReviewComparison struct {
	At time.Time `json:"at"`
	// HeadCommit is the commit both reviews read.
	HeadCommit string `json:"head_commit,omitempty"`
	// DecidingModel and DecidingDecision are the review the run acted on.
	DecidingModel    string `json:"deciding_model"`
	DecidingDecision string `json:"deciding_decision"`
	// CheaperModel is the model the second review was made on, and
	// CheaperDecision what it decided; empty where it reached no verdict.
	CheaperModel    string `json:"cheaper_model"`
	CheaperDecision string `json:"cheaper_decision,omitempty"`
	// CheaperSummary is what the second reviewer said, cut to the bound a
	// review summary is held to.
	CheaperSummary  string `json:"cheaper_summary,omitempty"`
	CheaperFindings int    `json:"cheaper_findings,omitempty"`
	// Problem says why the second review reached no verdict.
	Problem string `json:"problem,omitempty"`
}

// Compared reports a pair in which both reviews reached a verdict.
func (c ReviewComparison) Compared() bool {
	return c.DecidingDecision != "" && c.CheaperDecision != ""
}

// Agree reports a pair whose two verdicts are the same decision.
func (c ReviewComparison) Agree() bool {
	return c.Compared() && c.DecidingDecision == c.CheaperDecision
}

// CheaperApprovedWhatStrongerRefused reports the pair the trial is held to:
// the cheaper reviewer approved a change the stronger one did not.
func (c ReviewComparison) CheaperApprovedWhatStrongerRefused() bool {
	return c.Compared() && c.CheaperDecision == "approve" && c.DecidingDecision != "approve"
}

// RecordReviewComparison adds a second review to the run's record, keeping the
// latest MaxReviewComparisons.
func (s *State) RecordReviewComparison(comparison ReviewComparison) {
	comparison.CheaperSummary = RecordReviewSummary(comparison.CheaperSummary)
	s.ReviewComparisons = append(s.ReviewComparisons, comparison)
	if extra := len(s.ReviewComparisons) - MaxReviewComparisons; extra > 0 {
		s.ReviewComparisons = append([]ReviewComparison(nil), s.ReviewComparisons[extra:]...)
	}
}

// ReviewComparisonRuns lists every document run whose review was made a second
// time on the cheaper model, read through the run index's record of which runs
// publish a document. A record that would not read is left out rather than
// refusing the listing, as the developer trial's listing does.
func (s *Store) ReviewComparisonRuns() ([]State, error) {
	return s.indexed("whose document review was made a second time on the cheaper model",
		func(entry runIndexEntry) bool { return entry.Document != "" },
		func(unreadableRun) bool { return false },
		func(state State) bool { return len(state.ReviewComparisons) > 0 })
}
