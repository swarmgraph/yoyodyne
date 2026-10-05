package orchestrator

// Review of an accumulated change, which is the scope no per-work-item review
// can reach. Each work item's review sees exactly one worktree, so a defect that
// is consistent inside every change that produced it and wrong in their sum is
// structurally invisible to it. This is the same reviewer, the same contract,
// and the same structured verdict, pointed at a branch against a base commit.
//
// What a repair verdict here does is a decision this file makes deliberately,
// and it is: nothing to the work already integrated. Every commit under review
// was already checked, reviewed, approved, and promoted by a run that has since
// settled, so there is no gate left for this verdict to hold and nothing it
// could withhold that has not already been given. Reverting or reopening that
// work on a second opinion is not something the harness does behind an
// operator's back, and it is not something this reviewer is wired to do: it has
// no run store, no worktree, and no integration of its own, so a repair verdict
// is incapable of touching a run whatever anybody later asks of it.
//
// It is advisory rather than empty, though, and the difference is enforced.
// The verdict is recorded durably with the same session and model evidence a
// per-item review leaves behind, and the branch is not reviewed-and-approved
// until an approving verdict says so: Approved answers that one question, every
// other outcome — repair, a review that failed, a change too large to be shown
// in full — answers it with no, and `yoyo review` fails on all of them. What the
// findings then become is work, and admitting work to the backlog belongs to the
// product manager rather than to a reviewer.
//
// A shadow review is that same review with one thing withheld. It is asked for
// in order to measure the reviewer — the same branch state, judged again by a
// differently configured one, so the two verdicts can be held up against each
// other — and its verdict approves nothing whatever it decided. That is what
// makes measuring a cheaper reviewer risk nothing: a shadow verdict is incapable
// of becoming this branch's answer to "has an independent reviewer approved it".

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// BranchChangeReader describes an accumulated change. It is the narrow half of
// the worktree manager a branch review needs, and deliberately only the reading
// half: nothing here creates, promotes, or removes anything.
type BranchChangeReader interface {
	BranchChanges(ctx context.Context, request gitworktree.BranchRequest, limits gitworktree.DiffLimits) (gitworktree.BranchChange, error)
	repositoryReader
}

// BranchReviewRecorder keeps the durable record of a branch review: the verdict
// itself, and the event stream the provider invocation that produced it emitted.
// Both halves are one store's job because they are the same record of the same
// review — the verdict says what was decided, and the events are the only place
// what the provider reported it cost is ever written down. It is satisfied by
// runstate.BranchReviewStore.
type BranchReviewRecorder interface {
	Append(reviewed runstate.BranchReview) error
	AppendEvent(event execution.Event) error
}

// BranchReviewer runs one independent review of what a branch accumulated over
// a base commit.
type BranchReviewer struct {
	Worktrees BranchChangeReader
	Reviewer  ChangeReviewer
	// Reviews is where the verdict is recorded. A review nothing can write down
	// is still a review that happened, so a failure to record is reported on the
	// outcome rather than hiding the verdict it could not keep.
	Reviews BranchReviewRecorder
	// Reports is where what the reviewer noticed beside its verdict is
	// collected, exactly as it is for a run.
	Reports ReportCollector
	// UsageLimits is where a provider refusing this review for want of capacity
	// is written down. A branch review is a provider invocation with no run to
	// park, so without this an exhausted limit stops it at whoever's terminal
	// asked for it and leaves the durable record silent. It is optional like the
	// report collector: a review assembled without one is refused exactly as it
	// always was, and what is lost is the only account of why.
	UsageLimits UsageLimitRecorder
	Clock       execution.Clock
	NewReviewID func() (string, error)
	Repository  string
	Config      config.Config
	// StateRoot is where a pooled account's provider home is found. A branch
	// review has no run to be affined to, so it is made under the reviewer
	// agent's own account — the same one that agent's conversation is held under
	// — rather than under whichever account the rotation is at. Empty is the
	// machine's own provider home, which is what a project with one account has.
	StateRoot string
	// Limits bounds the described change. A zero value takes the branch-scope
	// defaults, which are larger than a work item's because an accumulated
	// change is many work items by construction.
	Limits gitworktree.DiffLimits
	// Build is the repository revision this harness binary was built from, which
	// is what the reviewer's reports carry: a report is a claim about the build
	// that filed it. Empty is a binary that recorded none.
	Build        string
	RedactValues []string
}

// BranchReviewRequest names the accumulated change to review.
type BranchReviewRequest struct {
	Branch  string
	BaseRef string
	// Shadow asks for a review made to measure the reviewer rather than to judge
	// the branch. It changes nothing about how the review is made — the same
	// contract, the same evidence, the same recorded verdict — and everything
	// about what the verdict is then allowed to mean: a shadow verdict approves
	// nothing, so a cheaper reviewer pointed at a branch for measurement cannot
	// leave an approval of it behind.
	Shadow bool
}

// BranchReviewOutcome is what one branch review produced, including its
// independence evidence. It is what a caller reports and exits on; the durable
// record beside it is what outlives the process.
type BranchReviewOutcome struct {
	ReviewID       string           `json:"review_id"`
	Branch         string           `json:"branch"`
	BaseRef        string           `json:"base_ref,omitempty"`
	BaseCommit     string           `json:"base_commit"`
	HeadCommit     string           `json:"head_commit"`
	Commits        int              `json:"commits"`
	CommitsOmitted int              `json:"commits_omitted,omitempty"`
	Truncated      bool             `json:"truncated,omitempty"`
	Decision       review.Decision  `json:"decision,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	Findings       []review.Finding `json:"findings,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	Model          string           `json:"model,omitempty"`
	ResolvedModel  string           `json:"resolved_model,omitempty"`
	Effort         string           `json:"effort,omitempty"`
	// ResolvedEffort is provider-reported; EffortReported is false when not reported.
	ResolvedEffort string          `json:"resolved_effort,omitempty"`
	EffortReported bool            `json:"effort_reported"`
	Invariants     []string        `json:"invariants,omitempty"`
	Reports        []report.Report `json:"reports,omitempty"`
	ReportProblem  string          `json:"report_problem,omitempty"`
	// Shadow says this review was made to measure the reviewer, and gates
	// nothing. It is carried on the outcome as well as the record so that a
	// caller reading the verdict cannot read it as an approval either.
	Shadow bool `json:"shadow,omitempty"`
	// RecordFailure names a verdict that could not be written down. The review
	// still happened and is still reported here; what is missing is the durable
	// half, which is exactly what an operator has to know to record it by hand.
	RecordFailure string `json:"record_failure,omitempty"`
}

// Approved is the single question the outcome answers, and the only one
// anything downstream should ask. A repair verdict, a review that never
// answered, an approval of a change that was truncated, and a shadow review
// that was never asked to judge the branch are all not an approval of it.
func (o BranchReviewOutcome) Approved() bool {
	return !o.Shadow && o.Decision == review.DecisionApprove
}

// Decided reports a review that reached a verdict, which is the question a
// shadow review answers instead: it was asked for a measurement rather than an
// approval, so what it produced is a verdict or nothing.
func (o BranchReviewOutcome) Decided() bool {
	return o.Decision == review.DecisionApprove || o.Decision == review.DecisionRepair
}

// Review obtains one independent verdict on an accumulated change and records
// it. The error it returns means the review could not be made; a review that
// was made and asked for repair is a completed review with a repair verdict,
// which is a different fact and is reported as one.
func (b BranchReviewer) Review(ctx context.Context, request BranchReviewRequest) (BranchReviewOutcome, error) {
	if b.Worktrees == nil {
		return BranchReviewOutcome{}, errors.New("a branch review requires a worktree manager")
	}
	if b.Reviewer == nil {
		return BranchReviewOutcome{}, errors.New("a branch review requires an independent reviewer")
	}
	change, err := b.Worktrees.BranchChanges(ctx, gitworktree.BranchRequest{
		Branch:  request.Branch,
		BaseRef: request.BaseRef,
	}, b.limits())
	if err != nil {
		return BranchReviewOutcome{}, fmt.Errorf("assemble the accumulated change: %w", err)
	}
	reviewID, err := b.newReviewID()
	if err != nil {
		return BranchReviewOutcome{}, err
	}
	outcome := BranchReviewOutcome{
		ReviewID:       reviewID,
		Branch:         change.Branch,
		BaseRef:        change.BaseRef,
		BaseCommit:     change.BaseCommit,
		HeadCommit:     change.HeadCommit,
		Commits:        len(change.Commits),
		CommitsOmitted: change.CommitsOmitted,
		Truncated:      change.Changes.Truncated,
		Shadow:         request.Shadow,
	}
	invariants, invariantErr := b.deliveredInvariants(change)
	if invariantErr != nil {
		return outcome, invariantErr
	}
	outcome.Invariants = invariants.IDs()
	intent, err := contextbundle.AssembleIntent(b.Repository, b.Config.Product.Specifications, reviewedRevision(ctx, b.Worktrees, change.BaseCommit))
	if err != nil {
		return outcome, fmt.Errorf("assemble branch review product intent: %w", err)
	}

	account := b.account()
	result, reviewErr := b.Reviewer.Review(ctx, review.Request{
		RunID:      reviewID,
		Scope:      review.ScopeBranch,
		Branch:     branchScope(change),
		Context:    branchContext(change) + intent,
		Invariants: invariants.Text(),
		// The repository holds the named commits; its current checkout need not
		// be the reviewed branch. The review contract distinguishes the two.
		WorktreePath: b.Repository,
		Changes:      change.Changes,
		Repository:   reviewedRepository(ctx, b.Worktrees, change.HeadCommit, beads.WorkItem{}, change.Changes),
		RedactValues: b.RedactValues,
		// A branch review is a provider invocation like any other the harness
		// makes, so it records one like any other: this is what lets it be
		// followed while it runs and priced afterwards. The sequence starts at
		// zero because this review is the whole of its own event stream rather
		// than a step inside a longer one.
		EventSink: b.sink,
		// And it spends like any other, so it lands in the cost log like any
		// other: the review is what it belongs to, since there is no run and no
		// work item behind it.
		Spend: b.spendAttribution(reviewID),
		// Under the reviewer agent's own account, which is what the cost line
		// above is charged to. A review invoked on one account and billed to
		// another is exactly the disagreement the alias exists to prevent.
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	})
	outcome.SessionID = result.SessionID
	outcome.Model = result.RequestedModel
	outcome.ResolvedModel = result.ResolvedModel
	outcome.Effort = result.RequestedEffort
	outcome.ResolvedEffort = result.ResolvedEffort
	outcome.EffortReported = result.EffortReported
	outcome.Summary = result.Verdict.Summary
	outcome.Findings = result.Verdict.Findings
	// What the reviewer reported is collected before its verdict is read,
	// because a report survives a review that produced no verdict at all.
	b.collectReports(&outcome, result.Reports)
	if result.ReportProblem != "" {
		b.noteReportProblem(&outcome, errors.New(result.ReportProblem))
	}
	if result.Decision == review.DecisionApprove || result.Decision == review.DecisionRepair {
		outcome.Decision = result.Decision
	}
	failure := ""
	var refusal error
	if reviewErr != nil {
		failure = reviewErr.Error()
		outcome.Decision = ""
		// A provider that declined this review for want of capacity is a fact
		// about the whole product rather than about this review, and nothing
		// else in the record would ever say it happened. It is recorded only
		// where the review actually failed: a limit reported beside a verdict
		// the provider still gave stopped nothing.
		refusal = recordUsageLimit(b.UsageLimits, b.Config.Product.ID, b.clock().Now(),
			fmt.Sprintf("the independent review %s of %s", reviewID, change.Branch), result.UsageLimit)
	}
	// The record is written whichever way the review went, because "this branch
	// has been independently reviewed" is answered from it, and a review that
	// failed answers it just as much as one that decided.
	b.record(&outcome, change, failure)
	if reviewErr != nil {
		return outcome, errors.Join(fmt.Errorf("independent branch review failed: %w", reviewErr), refusal)
	}
	return outcome, nil
}

func (b BranchReviewer) limits() gitworktree.DiffLimits {
	limits := b.Limits
	if limits.MaxTotalBytes == 0 {
		limits.MaxTotalBytes = gitworktree.DefaultMaxBranchDiffBytes
	}
	return limits
}

func (b BranchReviewer) clock() execution.Clock {
	if b.Clock == nil {
		return execution.RealClock{}
	}
	return b.Clock
}

// sink persists one event of this review's own stream. A store that cannot keep
// an event fails the review the same way it fails a run's: the event log is the
// evidence the invocation happened and what it cost, and a review nobody can
// account for afterwards is not one to report as having been made.
func (b BranchReviewer) sink(event execution.Event) error {
	if b.Reviews == nil {
		return errors.New("nothing records branch reviews for this product, so this review would leave no event stream")
	}
	if err := b.Reviews.AppendEvent(event); err != nil {
		return fmt.Errorf("persist branch review event: %w", err)
	}
	return nil
}

func (b BranchReviewer) newReviewID() (string, error) {
	mint := b.NewReviewID
	if mint == nil {
		mint = runstate.NewBranchReviewID
	}
	id, err := mint()
	if err != nil {
		return "", fmt.Errorf("mint a branch review id: %w", err)
	}
	return id, nil
}

// deliveredInvariants selects the architect's constraints for this review from
// the change itself: an accumulated change has no work item whose prose could
// name them, and what it touched is the only evidence there is. Loading them is
// a hard failure for the same reason it is for a run — delivering nothing looks
// exactly like a repository with no constraints, and a cross-cutting invariant
// is precisely what this scope exists to catch a violation of.
func (b BranchReviewer) deliveredInvariants(change gitworktree.BranchChange) (invariant.Delivery, error) {
	store := invariant.Store{RepositoryRoot: b.Repository, Directory: b.Config.Product.Invariants}
	set, err := store.Load()
	if err != nil {
		return invariant.Delivery{}, fmt.Errorf("load architectural invariants: %w", err)
	}
	evidence := []string{change.Changes.Status, change.Changes.DiffStat}
	for _, commit := range change.Commits {
		evidence = append(evidence, commit.Subject)
	}
	return set.Select(evidence...), nil
}

func branchScope(change gitworktree.BranchChange) review.BranchScope {
	return review.BranchScope{
		Name:           change.Branch,
		BaseCommit:     change.BaseCommit,
		HeadCommit:     change.HeadCommit,
		Commits:        change.Commits,
		CommitsOmitted: change.CommitsOmitted,
	}
}

// branchContext is what the harness knows about the accumulated change, and
// nothing an agent wrote. It says what the branch is, what state its commits are
// already in, and what this review is being asked for, so a reviewer that finds
// nothing new does not mistake per-item work for something it must judge again.
func branchContext(change gitworktree.BranchChange) string {
	lines := []string{
		fmt.Sprintf("Branch `%s` accumulated %d commit(s) over base commit `%s`.", change.Branch, len(change.Commits), change.BaseCommit),
		"",
		"Every one of those commits was developed for a bounded work item, verified against the project's own configured checks, reviewed independently against that item, and integrated into this branch. Each of them is therefore already known to be correct on its own terms, and none of it is waiting on this review.",
		"",
		"What is not yet known is what they add up to. That is what you are being asked for: the defects that only exist across commits, and that no review of a single work item could have been in a position to see.",
		"",
		"No deterministic checks were run for this review. The checks each work item was verified by have already run, against each of these commits in turn.",
	}
	return strings.Join(lines, "\n") + "\n"
}

// record writes the durable half of the review. A record that cannot be written
// is named on the outcome rather than turned into a failed review: the review
// happened, the caller is holding its verdict, and what is missing is the
// evidence that outlives this process.
func (b BranchReviewer) record(outcome *BranchReviewOutcome, change gitworktree.BranchChange, failure string) {
	if b.Reviews == nil {
		outcome.RecordFailure = "nothing records branch reviews for this product, so this verdict was not kept"
		return
	}
	reviewed := runstate.BranchReview{
		SchemaVersion:  runstate.BranchReviewSchemaVersion,
		ReviewID:       outcome.ReviewID,
		ProductID:      b.Config.Product.ID,
		RepositoryID:   string(b.Config.Product.RepositoryID),
		Branch:         change.Branch,
		BaseRef:        change.BaseRef,
		BaseCommit:     change.BaseCommit,
		HeadCommit:     change.HeadCommit,
		Commits:        len(change.Commits),
		CommitsOmitted: change.CommitsOmitted,
		Truncated:      change.Changes.Truncated,
		ReviewedAt:     b.clock().Now().UTC(),
		SessionID:      outcome.SessionID,
		Model:          outcome.Model,
		ResolvedModel:  outcome.ResolvedModel,
		Effort:         outcome.Effort,
		ResolvedEffort: outcome.ResolvedEffort,
		EffortReported: outcome.EffortReported,
		Decision:       string(outcome.Decision),
		Summary:        outcome.Summary,
		Findings:       durableFindings(outcome.Findings),
		Failure:        failure,
		Shadow:         outcome.Shadow,
	}
	// A review that reached no verdict must still say what stopped it, and a
	// review whose verdict the harness rejected has both a summary and a reason.
	if reviewed.Decision == "" && strings.TrimSpace(reviewed.Failure) == "" {
		reviewed.Failure = "the reviewer produced no decision"
	}
	if err := b.Reviews.Append(reviewed); err != nil {
		outcome.RecordFailure = err.Error()
	}
}

// collectReports records what the reviewer noticed beside its verdict. Every
// failure here is noted and swallowed, for the same reason it is on a run: a
// review that failed because the reviewer mentioned a risk would teach every
// reviewer to stop mentioning them.
func (b BranchReviewer) collectReports(outcome *BranchReviewOutcome, entries []report.Entry) {
	if len(entries) == 0 {
		return
	}
	if b.Reports == nil {
		b.noteReportProblem(outcome, fmt.Errorf("nothing collects reports for this review, so the %d thing(s) the %s reported were not kept", len(entries), domain.RoleReviewer))
		return
	}
	collected, err := report.Collect(entries, report.Attribution{
		Role:         domain.RoleReviewer,
		Agent:        b.agentName(),
		RunID:        outcome.ReviewID,
		Build:        b.Build,
		ProductID:    b.Config.Product.ID,
		RepositoryID: string(b.Config.Product.RepositoryID),
	}, b.clock().Now())
	if err != nil {
		b.noteReportProblem(outcome, err)
		return
	}
	for _, reported := range collected {
		if err := b.Reports.Append(reported); err != nil {
			b.noteReportProblem(outcome, err)
			continue
		}
		outcome.Reports = append(outcome.Reports, reported)
	}
}

func (b BranchReviewer) noteReportProblem(outcome *BranchReviewOutcome, cause error) {
	problem := fmt.Sprintf("a %s report was not collected: %s", domain.RoleReviewer, singleLine(cause.Error(), maxReportProblemBytes))
	if outcome.ReportProblem == "" {
		outcome.ReportProblem = problem
		return
	}
	outcome.ReportProblem += "; " + problem
}

// agentName names the configured agent that filled the reviewer role, so a
// report says which agent made it and not only which contract it worked under.
func (b BranchReviewer) agentName() string {
	return Pipeline{Config: b.Config}.agentNameForRole(domain.RoleReviewer)
}

// account is where this branch review is made. A branch review has no run to be
// affined to, so it is the reviewer agent's own account rather than a rotation's
// current position — the same account that agent's conversation is held under,
// and for the same reason: what has no run behind it has an agent behind it.
//
// An alias the configuration will not resolve leaves the review made where the
// machine is already signed in rather than refused. A branch review judges what
// is already on the branch and is not worth stopping over a mapping edited
// underneath it; the alias it records is still the one it was made under.
func (b BranchReviewer) account() config.AccountEndpoint {
	if endpoint, err := b.Config.AgentAccountEndpoint(b.StateRoot, b.agentName()); err == nil {
		return endpoint
	}
	return config.AccountEndpoint{Alias: b.Config.AgentAccountAlias(b.agentName())}
}
