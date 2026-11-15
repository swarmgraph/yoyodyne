package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MergeWait is GitHub's own account of a pull request whose merge it holds and
// has not made: whether it can merge, the merge state it reports, whether it is
// a draft, what its reviews come to, where it stands in GitHub's merge queue,
// and how each check the target's branch rules require ended on its head. It
// is read while the harness waits on a merge it asked for, so that a merge
// GitHub never makes is withdrawn naming what GitHub said it was waiting on
// (orchestrator's mergequeuepromotion.go, awaitForge). It grants nothing and
// asks GitHub to do nothing.
type MergeWait struct {
	Number int `json:"number"`
	// Mergeable is GitHub's word for whether the head merges cleanly:
	// MERGEABLE, CONFLICTING, or UNKNOWN while it has not worked it out.
	Mergeable string `json:"mergeable,omitempty"`
	// MergeState is GitHub's merge state for the request (CLEAN, BLOCKED,
	// BEHIND, DIRTY, UNSTABLE, DRAFT, HAS_HOOKS, UNKNOWN).
	MergeState string `json:"merge_state,omitempty"`
	Draft      bool   `json:"draft,omitempty"`
	// ReviewDecision is what the reviews come to where a branch rule asks for
	// one: APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or empty.
	ReviewDecision string `json:"review_decision,omitempty"`
	// QueueState is where the request stands in GitHub's own merge queue, and
	// empty where it is not in one.
	QueueState string `json:"queue_state,omitempty"`
	// Required is each check the branch rules require, as it stood on the head.
	Required []RequiredCheck `json:"required,omitempty"`
}

// RequiredCheck is one check a branch rule requires before GitHub merges, and
// how it stood on the pull request's head.
type RequiredCheck struct {
	Name string `json:"name"`
	// State is one of the RequiredCheck* states.
	State string `json:"state"`
	// Conclusion is GitHub's own word for how a finished check ended, lower
	// case, such as failure or cancelled.
	Conclusion string `json:"conclusion,omitempty"`
	// ID is the check run, which for an Actions job is the job a re-run names;
	// zero for a status a third-party service posts.
	ID int64 `json:"id,omitempty"`
}

// The states a required check can stand in.
const (
	RequiredCheckPending = "pending"
	RequiredCheckPassed  = "passed"
	RequiredCheckFailed  = "failed"
)

// FailedRequired is every required check that ended without passing.
func (w MergeWait) FailedRequired() []RequiredCheck {
	var failed []RequiredCheck
	for _, check := range w.Required {
		if check.State == RequiredCheckFailed {
			failed = append(failed, check)
		}
	}
	return failed
}

// PendingRequired names every required check that has not finished.
func (w MergeWait) PendingRequired() []string {
	var pending []string
	for _, check := range w.Required {
		if check.State == RequiredCheckPending {
			pending = append(pending, check.Name)
		}
	}
	return pending
}

// WaitingOn says in plain words what GitHub's account says the merge is
// waiting on, most specific first, with GitHub's own state words after it so a
// person can look the same thing up on GitHub. It never says nothing: an
// account naming no obstacle says that.
func (w MergeWait) WaitingOn() string {
	var waits []string
	for _, check := range w.FailedRequired() {
		ended := check.Conclusion
		if ended == "" {
			ended = "failure"
		}
		waits = append(waits, fmt.Sprintf("the required check %q ended %s", check.Name, describeConclusion(ended)))
	}
	if pending := w.PendingRequired(); len(pending) > 0 {
		waits = append(waits, fmt.Sprintf("the required %s %s %s not finished", plural(len(pending), "check", "checks"), quotedList(pending), plural(len(pending), "has", "have")))
	}
	if strings.EqualFold(w.Mergeable, "CONFLICTING") {
		waits = append(waits, "the pull request conflicts with its target branch")
	}
	if w.Draft {
		waits = append(waits, "the pull request is still a draft")
	}
	switch strings.ToUpper(w.ReviewDecision) {
	case "REVIEW_REQUIRED":
		waits = append(waits, "a branch rule requires an approving review, and the pull request has none")
	case "CHANGES_REQUESTED":
		waits = append(waits, "a reviewer has asked for changes")
	}
	state := strings.ToUpper(strings.TrimSpace(w.MergeState))
	if len(waits) == 0 {
		switch state {
		case "BLOCKED":
			waits = append(waits, "a branch rule of the target is not met, and every required check has passed, so the rule is one GitHub does not name here")
		case "BEHIND":
			waits = append(waits, "the target branch has moved ahead, and a branch rule requires the pull request to be up to date with it")
		case "DIRTY":
			waits = append(waits, "the pull request conflicts with its target branch")
		case "UNKNOWN", "":
			waits = append(waits, "GitHub has not yet worked out whether the pull request can merge")
		case "CLEAN", "HAS_HOOKS", "UNSTABLE":
			waits = append(waits, "nothing GitHub names: it reports the pull request can merge, and it has not merged it")
		default:
			waits = append(waits, "nothing this build recognizes")
		}
	}
	says := strings.Join(waits, "; ")
	var own []string
	if state != "" {
		own = append(own, "merge state "+state)
	}
	if mergeable := strings.ToUpper(strings.TrimSpace(w.Mergeable)); mergeable != "" {
		own = append(own, "mergeable "+mergeable)
	}
	if queue := strings.ToUpper(strings.TrimSpace(w.QueueState)); queue != "" {
		own = append(own, "merge queue "+queue)
	}
	if len(own) > 0 {
		says += " (GitHub's own words: " + strings.Join(own, ", ") + ")"
	}
	return says
}

func describeConclusion(conclusion string) string {
	switch strings.ToLower(conclusion) {
	case "failure", "error":
		return "in failure"
	case "cancelled":
		return "cancelled"
	case "timed_out":
		return "out of time"
	case "startup_failure":
		return "without starting"
	case "action_required":
		return "waiting for somebody to act on it"
	}
	return strings.ToLower(conclusion)
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = strconv.Quote(name)
	}
	return strings.Join(quoted, ", ")
}

// mergeWaitQuery reads, in one call, everything MergeWait carries. Whether a
// check is required is asked of each check for this pull request, because the
// branch rules decide it and the check listing does not say.
const mergeWaitQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) {
    mergeable mergeStateStatus isDraft reviewDecision
    mergeQueueEntry { state }
    commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
      __typename
      ... on CheckRun { name status conclusion databaseId isRequired(pullRequestNumber: $number) }
      ... on StatusContext { context state isRequired(pullRequestNumber: $number) }
    } } } } } }
  } }
}`

// MergeWait reads GitHub's account of why it has not yet made the merge of a
// pull request. Any failure fails the whole reading: a caller records a
// reading that could not be made as exactly that, and never as GitHub holding
// the merge for a reason.
func (g GitHub) MergeWait(ctx context.Context, number int) (MergeWait, error) {
	if number <= 0 {
		return MergeWait{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	stdout, err := g.graphql(ctx, fmt.Sprintf("ask GitHub what the merge of pull request %d is waiting on", number),
		"-f", "query="+mergeWaitQuery,
		"-F", "owner={owner}",
		"-F", "name={repo}",
		"-F", "number="+strconv.Itoa(number))
	if err != nil {
		return MergeWait{}, err
	}
	return decodeMergeWait(number, stdout)
}

func decodeMergeWait(number int, stdout string) (MergeWait, error) {
	var answered struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					Mergeable        string `json:"mergeable"`
					MergeStateStatus string `json:"mergeStateStatus"`
					IsDraft          bool   `json:"isDraft"`
					ReviewDecision   string `json:"reviewDecision"`
					MergeQueueEntry  *struct {
						State string `json:"state"`
					} `json:"mergeQueueEntry"`
					Commits struct {
						Nodes []struct {
							Commit struct {
								StatusCheckRollup *struct {
									Contexts struct {
										Nodes []struct {
											Typename   string `json:"__typename"`
											Name       string `json:"name"`
											Status     string `json:"status"`
											Conclusion string `json:"conclusion"`
											DatabaseID int64  `json:"databaseId"`
											Context    string `json:"context"`
											State      string `json:"state"`
											IsRequired bool   `json:"isRequired"`
										} `json:"nodes"`
									} `json:"contexts"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answered); err != nil {
		return MergeWait{}, fmt.Errorf("decode GitHub's account of pull request %d: %w", number, err)
	}
	if len(answered.Errors) > 0 {
		return MergeWait{}, fmt.Errorf("GitHub answered the question about pull request %d with an error: %s", number, firstLine(answered.Errors[0].Message))
	}
	pull := answered.Data.Repository.PullRequest
	if pull == nil {
		return MergeWait{}, fmt.Errorf("GitHub's answer names no pull request %d", number)
	}
	wait := MergeWait{
		Number:         number,
		Mergeable:      strings.TrimSpace(pull.Mergeable),
		MergeState:     strings.TrimSpace(pull.MergeStateStatus),
		Draft:          pull.IsDraft,
		ReviewDecision: strings.TrimSpace(pull.ReviewDecision),
	}
	if pull.MergeQueueEntry != nil {
		wait.QueueState = strings.TrimSpace(pull.MergeQueueEntry.State)
	}
	for _, node := range pull.Commits.Nodes {
		rollup := node.Commit.StatusCheckRollup
		if rollup == nil {
			continue
		}
		for _, context := range rollup.Contexts.Nodes {
			if !context.IsRequired {
				continue
			}
			switch context.Typename {
			case "CheckRun":
				check := RequiredCheck{Name: strings.TrimSpace(context.Name), ID: context.DatabaseID}
				switch conclusion := strings.ToLower(strings.TrimSpace(context.Conclusion)); {
				case !strings.EqualFold(context.Status, "COMPLETED"):
					check.State = RequiredCheckPending
				case conclusion == "success" || conclusion == "neutral" || conclusion == "skipped":
					check.State, check.Conclusion = RequiredCheckPassed, conclusion
				default:
					check.State, check.Conclusion = RequiredCheckFailed, conclusion
				}
				wait.Required = append(wait.Required, check)
			case "StatusContext":
				check := RequiredCheck{Name: strings.TrimSpace(context.Context)}
				switch state := strings.ToLower(strings.TrimSpace(context.State)); state {
				case "success":
					check.State, check.Conclusion = RequiredCheckPassed, state
				case "pending", "expected":
					check.State = RequiredCheckPending
				default:
					check.State, check.Conclusion = RequiredCheckFailed, state
				}
				wait.Required = append(wait.Required, check)
			}
		}
	}
	sort.SliceStable(wait.Required, func(i, j int) bool { return wait.Required[i].Name < wait.Required[j].Name })
	return wait, nil
}
