package readmodel

// What each second review of a document publication cost, in the reading of
// what the spend accomplished (execution.reviewer_models.compare_documents;
// runstate/reviewcomparison.go).
//
// A second review decides nothing, so its cost belongs to no verdict. It is
// matched to the review by identity rather than by time: each of its lines in
// the cost log is marked as a comparison and names the provider session it ran
// in, and the run's record of the second review names the same sessions. A
// marked line whose session no recorded second review names — a pair recorded
// before the sessions were kept, one dropped past the record's bound, or a run
// whose record could not be read — is still the comparison's cost, and is
// shown as that run's, unattributed to any one review, rather than guessed at.
//
// The reviewer's spend is unchanged by any of this: every one of these lines
// is in it, as it always was.

import (
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// comparisonReviews is every second document review in the range, each with
// the cost of the lines that name its sessions, followed in time order by any
// run's comparison cost that names no recorded review.
func comparisonReviews(lines []runstate.Spend, states []runstate.State, from, to time.Time) []SecondReviewCost {
	byRun := map[string][]runstate.Spend{}
	for _, line := range lines {
		if line.Comparison && line.RunID != "" {
			byRun[line.RunID] = append(byRun[line.RunID], line)
		}
	}
	stateOf := map[string]runstate.State{}
	for _, state := range states {
		if len(state.ReviewComparisons) > 0 || len(byRun[state.RunID]) > 0 {
			stateOf[state.RunID] = state
		}
	}
	inRange := func(at time.Time) bool { return !at.Before(from) && at.Before(to) }
	add := func(entry *SecondReviewCost, line runstate.Spend) {
		entry.Invocations++
		if !line.Known() {
			entry.Unpriced++
			return
		}
		entry.CostUSD += line.AmountUSD
	}

	var reviews []SecondReviewCost
	for _, runID := range deliveryKeys(stateOf) {
		state := stateOf[runID]
		owner := map[string]int{}
		var entries []SecondReviewCost
		for _, comparison := range state.ReviewComparisons {
			entries = append(entries, SecondReviewCost{RunID: runID, WorkItemID: state.WorkItemID, Title: state.WorkItemTitle,
				At: comparison.At, Model: comparison.CheaperModel, Decision: comparison.CheaperDecision})
			for _, session := range comparison.Sessions {
				owner[session] = len(entries) - 1
			}
		}
		unattributed := SecondReviewCost{RunID: runID, WorkItemID: state.WorkItemID, Title: state.WorkItemTitle, Unattributed: true}
		for _, line := range byRun[runID] {
			if index, ok := owner[line.SessionID]; ok && line.SessionID != "" {
				add(&entries[index], line)
				continue
			}
			if inRange(line.At) {
				add(&unattributed, line)
				if line.At.After(unattributed.At) {
					unattributed.At = line.At
				}
			}
		}
		for _, entry := range entries {
			// A second review the harness withheld, or one recorded before its
			// sessions were kept, has no line of its own: it cost nothing here, and
			// any money it did cost is in the unattributed entry.
			if entry.Invocations > 0 && inRange(entry.At) {
				reviews = append(reviews, entry)
			}
		}
		if unattributed.Invocations > 0 {
			reviews = append(reviews, unattributed)
		}
	}
	// A run whose record could not be read still has comparison lines.
	for runID, runLines := range byRun {
		if _, ok := stateOf[runID]; ok {
			continue
		}
		unattributed := SecondReviewCost{RunID: runID, WorkItemID: runLines[0].WorkItemID, Unattributed: true}
		for _, line := range runLines {
			if inRange(line.At) {
				add(&unattributed, line)
				if line.At.After(unattributed.At) {
					unattributed.At = line.At
				}
			}
		}
		if unattributed.Invocations > 0 {
			reviews = append(reviews, unattributed)
		}
	}
	sort.SliceStable(reviews, func(a, b int) bool {
		if !reviews[a].At.Equal(reviews[b].At) {
			return reviews[a].At.Before(reviews[b].At)
		}
		return reviews[a].RunID < reviews[b].RunID
	})
	return reviews
}

// comparisonsSaid is what the second reviews cost together, and a sentence
// saying so with what of it could not be tied to one review; the sentence is
// empty where there were none.
func comparisonsSaid(reviews []SecondReviewCost) (float64, string) {
	if len(reviews) == 0 {
		return 0, ""
	}
	var total, unattributedUSD float64
	made, unattributedCalls, unpriced := 0, 0, 0
	for _, review := range reviews {
		total += review.CostUSD
		unpriced += review.Unpriced
		if review.Unattributed {
			unattributedUSD += review.CostUSD
			unattributedCalls += review.Invocations
			continue
		}
		made++
	}
	cost := fmt.Sprintf("$%.2f", total)
	if unpriced > 0 {
		cost = "at least " + cost
	}
	says := fmt.Sprintf("second reviews of documents on the cheaper model cost %s; it is in the reviewer's spend and in no review's cost above", cost)
	if made > 0 {
		says = fmt.Sprintf("%s of documents on the cheaper model cost %s; it is in the reviewer's spend and in no review's cost above", count(made, "second review"), cost)
	}
	if unattributedCalls > 0 {
		says += fmt.Sprintf(". %s costing $%.2f could not be tied to one second review, because the run's record names no session they ran in", count(unattributedCalls, "provider call"), unattributedUSD)
	}
	return total, says
}
