package readmodel

// The trial of reviewing document publications on the cheaper model
// (execution.reviewer_models.compare_documents;
// docs/designs/reducing-what-the-factory-costs.md, section 5), as
// `yoyo model-trial reviews` reads it: each document publication whose review
// was made a second time on the cheaper model, both verdicts, and whether they
// agree, with the two counts the development manager decides on.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReviewComparisonTarget is how many document publications the design asks to
// be reviewed both ways before document reviews may move to the cheaper model.
const ReviewComparisonTarget = 30

// ReviewComparisonRuns is the part of the run store the listing is read from,
// an optional capability of Runs satisfied by *runstate.Store.
type ReviewComparisonRuns interface {
	ReviewComparisonRuns() ([]runstate.State, error)
}

// ReviewComparisonEntry is one deciding review of a document publication and
// its second review on the cheaper model.
type ReviewComparisonEntry struct {
	RunID      string                    `json:"run_id"`
	WorkItemID string                    `json:"work_item_id,omitempty"`
	Document   string                    `json:"document,omitempty"`
	Comparison runstate.ReviewComparison `json:"comparison"`
	Agree      bool                      `json:"agree"`
}

// ReviewComparison is the listing as the read model carries it.
type ReviewComparison struct {
	ObservedAt time.Time `json:"observed_at"`
	Enabled    bool      `json:"enabled"`
	// Publications is how many document publications had both reviews reach a
	// verdict at least once, which is what the design's 30 counts.
	Publications int `json:"publications"`
	Target       int `json:"target"`
	// Compared is how many pairs reached two verdicts, Agreements how many of
	// those agree, and CheaperApprovedRefused how many had the cheaper reviewer
	// approve what the stronger did not.
	Compared               int                     `json:"compared"`
	Agreements             int                     `json:"agreements"`
	CheaperApprovedRefused int                     `json:"cheaper_approved_refused"`
	NoSecondVerdict        int                     `json:"no_second_verdict"`
	Entries                []ReviewComparisonEntry `json:"entries"`
	Problem                string                  `json:"problem,omitempty"`
}

// ReadReviewComparison reads every recorded second review off the run records.
func ReadReviewComparison(sources Sources) ReviewComparison {
	listing := ReviewComparison{ObservedAt: sources.now(), Enabled: sources.CompareDocumentReviews, Target: ReviewComparisonTarget, Entries: []ReviewComparisonEntry{}}
	runs, ok := sources.Runs.(ReviewComparisonRuns)
	if !ok {
		listing.Problem = "nothing was wired to read the document runs"
		return listing
	}
	states, err := runs.ReviewComparisonRuns()
	if err != nil {
		listing.Problem = fmt.Sprintf("the document runs could not be read: %v", err)
		return listing
	}
	return summarizeReviewComparisons(listing, states)
}

func summarizeReviewComparisons(listing ReviewComparison, states []runstate.State) ReviewComparison {
	for _, state := range states {
		document := ""
		if state.Document != nil {
			document = state.Document.Candidate.Artifact.ID
		}
		compared := false
		for _, comparison := range state.ReviewComparisons {
			listing.Entries = append(listing.Entries, ReviewComparisonEntry{RunID: state.RunID, WorkItemID: state.WorkItemID, Document: document, Comparison: comparison, Agree: comparison.Agree()})
			if !comparison.Compared() {
				listing.NoSecondVerdict++
				continue
			}
			compared = true
			listing.Compared++
			if comparison.Agree() {
				listing.Agreements++
			}
			if comparison.CheaperApprovedWhatStrongerRefused() {
				listing.CheaperApprovedRefused++
			}
		}
		if compared {
			listing.Publications++
		}
	}
	sort.SliceStable(listing.Entries, func(a, b int) bool {
		return listing.Entries[a].Comparison.At.Before(listing.Entries[b].Comparison.At)
	})
	return listing
}

// Render is the listing in ordinary words: the counts, then one line for each
// pair of reviews.
func (l ReviewComparison) Render() string {
	var out strings.Builder
	state := "off"
	if l.Enabled {
		state = "on"
	}
	fmt.Fprintf(&out, "Document publications reviewed a second time on the cheaper model: %s. %d of at least %d publications reviewed both ways so far.\n", state, l.Publications, l.Target)
	if l.Problem != "" {
		fmt.Fprintf(&out, "Not everything could be read: %s.\n", l.Problem)
	}
	fmt.Fprintf(&out, "%d pair(s) of verdicts: %d agree, %d disagree; the cheaper reviewer approved what the stronger refused %d time(s).\n", l.Compared, l.Agreements, l.Compared-l.Agreements, l.CheaperApprovedRefused)
	if l.NoSecondVerdict > 0 {
		fmt.Fprintf(&out, "%d second review(s) reached no verdict and are left out of the counts.\n", l.NoSecondVerdict)
	}
	if len(l.Entries) == 0 {
		return strings.TrimRight(out.String(), "\n")
	}
	out.WriteString("\n")
	for _, entry := range l.Entries {
		c := entry.Comparison
		cheaper := c.CheaperDecision
		agreement := "disagree"
		switch {
		case cheaper == "":
			cheaper, agreement = "no verdict", "not compared ("+c.Problem+")"
		case entry.Agree:
			agreement = "agree"
		}
		fmt.Fprintf(&out, "%s  %s (%s, run %s): stronger %s %s, cheaper %s %s: %s\n",
			c.At.Local().Format("2006-01-02 15:04 MST"), entry.Document, entry.WorkItemID, entry.RunID,
			c.DecidingModel, c.DecidingDecision, c.CheaperModel, cheaper, agreement)
	}
	return strings.TrimRight(out.String(), "\n")
}
