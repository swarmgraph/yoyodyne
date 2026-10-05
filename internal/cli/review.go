package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

type branchReviewOutput struct {
	Review *orchestrator.BranchReviewOutcome `json:"review,omitempty"`
	Error  string                            `json:"error,omitempty"`
}

// reviewBranch runs one independent review of what a branch accumulated over a
// base commit. It is the review scope no per-work-item run can reach: each run
// judges one worktree, and a defect that only exists across the commits of
// several of them is invisible to every one of those reviews.
//
// It never touches an in-flight or settled run. The work it covers is already
// integrated, so this reports on the branch rather than gating anything that has
// already happened — and it fails unless an independent reviewer approved the
// whole accumulated change, so nothing downstream can read anything else as an
// approval of it.
func reviewBranch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	base := flags.String("base", "", "the base ref the branch accumulated over")
	branch := flags.String("branch", "", "the branch to review (default: the repository's current branch)")
	shadow := flags.Bool("shadow", false, "review to measure the reviewer; the verdict approves nothing")
	model := flags.String("model", "", "the model selector to review with, instead of the configured one (requires --shadow)")
	compare := flags.Bool("compare", false, "compare the recorded shadow reviews with the reviews they shadow, and review nothing")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "review does not accept positional arguments")
		printReviewUsage(stderr)
		return 2
	}
	if *compare {
		// Comparing reads what is already recorded, so everything that describes
		// a review to make is refused rather than quietly ignored: an operator who
		// passed --base meant a review to happen.
		if *base != "" || *shadow || *model != "" {
			fmt.Fprintln(stderr, "review --compare reads the recorded reviews and makes none, so it takes neither --base, --shadow, nor --model")
			printReviewUsage(stderr)
			return 2
		}
		return compareShadowReviews(*configPath, *branch, *jsonOutput, stdout, stderr)
	}
	if *base == "" {
		fmt.Fprintln(stderr, "review requires --base: an accumulated change is measured against the commit it grew from")
		printReviewUsage(stderr)
		return 2
	}
	// A model chosen at the terminal is only ever a measurement. Letting one
	// gate a branch would put the choice of what a verdict is worth in a command
	// line rather than in the configuration, and the cheapest reviewer anybody
	// happened to type would leave an approval behind it.
	if *model != "" && !*shadow {
		fmt.Fprintln(stderr, "review --model only makes sense with --shadow: a review the configuration did not choose the reviewer for cannot approve a branch")
		printReviewUsage(stderr)
		return 2
	}
	if *model != "" {
		if err := config.ValidateModelSelector(*model); err != nil {
			fmt.Fprintf(stderr, "review --model is invalid: %v\n", err)
			return 2
		}
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportBranchReview(stdout, stderr, *jsonOutput, orchestrator.BranchReviewOutcome{}, err)
	}
	// A branch review is one provider invocation and nothing else — there is no
	// run to park, no claim to keep, and nothing durable it could resume from — so
	// a held harness refuses it here rather than parking it. Asking again after
	// `yoyo resume` costs nothing that was already spent.
	if hold, held, err := parts.holds.Held(); err != nil || held {
		if err == nil {
			err = fmt.Errorf("all harness activity is paused, since %s; `yoyo resume` lifts it and this review can be asked for again",
				hold.HeldAt.Format(time.RFC3339))
		}
		return reportBranchReview(stdout, stderr, *jsonOutput, orchestrator.BranchReviewOutcome{}, err)
	}
	reviewed := *branch
	if reviewed == "" {
		// A branch nobody named is the one the repository is on, which is the
		// branch an operator asking about "this work" means.
		reviewed, err = parts.worktrees.CurrentBranch(ctx)
		if err != nil {
			return reportBranchReview(stdout, stderr, *jsonOutput, orchestrator.BranchReviewOutcome{}, err)
		}
	}
	outcome, err := branchReviewerFrom(parts, *model).Review(ctx, orchestrator.BranchReviewRequest{
		Branch:  reviewed,
		BaseRef: *base,
		Shadow:  *shadow,
	})
	return reportBranchReview(stdout, stderr, *jsonOutput, outcome, err)
}

// branchReviewerFrom wires a branch review over parts that are already built. It
// is given the reviewer and nothing else that acts: no run store, no tracker,
// and no integration, because a verdict on already-integrated work must not be
// able to reach the runs that produced it.
//
// An overriding model is what a shadow review is made with. Nothing else about
// the reviewer changes with it — the same persona, the same contract, the same
// evidence — because a measurement of what a model is worth is only a
// measurement if the model is the only thing that differed.
func branchReviewerFrom(parts components, model string) orchestrator.BranchReviewer {
	cfg := parts.config
	reviewerModel := agentModel(cfg, domain.RoleReviewer)
	if model != "" {
		reviewerModel = model
	}
	// A branch review is the reviewer's own provider invocation, so it runs on
	// whatever provider that agent names — a built-in, or one this project
	// declared, read by the dialect the declaration supplied.
	reviewer := agentForRole(cfg, domain.RoleReviewer)
	return orchestrator.BranchReviewer{
		Worktrees: parts.worktrees,
		Reviewer: review.Reviewer{
			Backend: providerBackend(cfg, reviewer.Backend, parts.runner),
			Model:   reviewerModel,
			Effort:  cfg.InvocationEffort(reviewer, reviewerModel),
			Persona: reviewer.Persona.Text,
			// A branch review spends like every other provider invocation and lands
			// in the same log, which is also what makes a shadow review's price
			// comparable to the review it was measured against.
			Spend: parts.spend,
		},
		Reviews: parts.branchReviews,
		Reports: parts.reports,
		// Where a provider refusing this review for want of capacity is written
		// down. It records nothing about the branch and reaches no run: it is the
		// account of why nothing happened, for somebody who is not at this
		// terminal.
		UsageLimits:  parts.usageLimits,
		Repository:   parts.repository,
		Config:       cfg,
		StateRoot:    parts.stateRoot,
		Build:        buildinfo.Commit(),
		RedactValues: parts.redactValues,
	}
}

// reportBranchReview says what the review decided and exits on whether the
// branch was approved. A repair verdict is not a failed review — the review
// worked, and it asked for repairs — but it is not an approval either, and the
// exit code answers that question rather than the other one.
//
// A shadow review was never asked that question, so the exit code answers the
// one it was asked instead: whether a verdict was obtained at all. Failing a
// shadow review for asking about repairs would make measuring a reviewer look
// like judging the branch, which is exactly what a shadow review is not.
func reportBranchReview(stdout, stderr io.Writer, jsonOutput bool, outcome orchestrator.BranchReviewOutcome, err error) int {
	if jsonOutput {
		result := branchReviewOutput{}
		if outcome.ReviewID != "" {
			result.Review = &outcome
		}
		if err != nil {
			result.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, result); code != 0 {
			return code
		}
		return branchReviewCode(outcome, err)
	}

	if err != nil {
		fmt.Fprintf(stderr, "branch review failed: %v\n", err)
	}
	if outcome.ReviewID != "" {
		theme := console.ThemeFor(stdout, os.Getenv)
		reviewed := "reviewed"
		if outcome.Shadow {
			reviewed = "shadow-reviewed"
		}
		fmt.Fprintf(stdout, "%s %s against %s: %d commit(s) from %s to %s\n",
			reviewed, outcome.Branch, outcome.BaseRef, outcome.Commits, outcome.BaseCommit, outcome.HeadCommit)
		if outcome.Decision != "" {
			fmt.Fprintf(stdout, "review: %s (session %s, model %s)\n", outcome.Decision, outcome.SessionID, outcome.Model)
		}
		if outcome.Summary != "" {
			fmt.Fprintf(stdout, "summary: %s\n", outcome.Summary)
		}
		for _, finding := range outcome.Findings {
			location := ""
			if finding.Location != nil {
				location = fmt.Sprintf(" [%s:%d]", finding.Location.File, finding.Location.Line)
			}
			fmt.Fprintf(stdout, "- %s%s: %s\n", finding.Severity, location, finding.Message)
		}
		// A change the bounds cut is one the reviewer could not see the whole of,
		// so what it kept out is said here whatever the verdict was. It is not a
		// refusal and never has been: the exit code below follows the verdict
		// alone, and a range whose test data alone outgrew the bound is one the
		// reviewer may approve — which is the whole of yoyodyne-ifd.425 at this
		// scope, and an approval of one exits zero as any other does.
		if outcome.Truncated {
			fmt.Fprintln(stderr, "the accumulated change was too large to describe in full; what was not shown is unreviewed")
			if outcome.CommitsOmitted > 0 {
				fmt.Fprintf(stderr, "%d older commit(s) were not described\n", outcome.CommitsOmitted)
			}
		}
		if outcome.RecordFailure != "" {
			fmt.Fprintf(stderr, "the verdict above was not recorded: %s\n", outcome.RecordFailure)
		}
		// Marked and dressed by the worst of them, for the reason a run's own
		// closing line is: this is one line under a verdict and a list of findings,
		// and a count alone reads the same whether the reviewer noticed three
		// routine things or one that is already costing somebody.
		if len(outcome.Reports) > 0 {
			worst := report.Worst(outcome.Reports)
			fmt.Fprint(stdout, theme.Severity(console.Severity(worst), fmt.Sprintf(
				"%sreported %d thing(s) beside the verdict (%s); `yoyo reports` shows them, as does /reports in `yoyo chat`\n",
				worst.Prefix(), len(outcome.Reports), report.Tally(outcome.Reports))))
		}
		if outcome.ReportProblem != "" {
			fmt.Fprintln(stdout, outcome.ReportProblem)
		}
		// A shadow review decided nothing about the branch, and says so where a
		// gating review would say what the branch now needs: what it produced is a
		// measurement of the reviewer, and `yoyo review --compare` is what reads it.
		if outcome.Shadow {
			fmt.Fprintf(stdout, "this verdict measures the reviewer and approves nothing; `yoyo review --compare` holds it up against the review it shadows\n")
		} else if !outcome.Approved() && err == nil {
			// The per-item work is integrated already, so this says what the branch
			// now needs rather than holding anything back from it.
			fmt.Fprintf(stderr, "%s is not approved as an accumulated change; the work on it stays integrated and the findings above are work to be admitted\n", outcome.Branch)
		}
	}
	return branchReviewCode(outcome, err)
}

// branchReviewCode is what the command exits on, which depends on what it was
// asked. A gating review is asked whether the branch is approved; a shadow
// review is asked for a verdict to measure, and produced one or did not.
func branchReviewCode(outcome orchestrator.BranchReviewOutcome, err error) int {
	if err != nil {
		return 1
	}
	if outcome.Shadow {
		if !outcome.Decided() {
			return 1
		}
		return 0
	}
	if !outcome.Approved() {
		return 1
	}
	return 0
}

func printReviewUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo review --base <ref> [options]
       yoyo review --compare [options]

Reviews what a branch accumulated over a base commit, as one change, with the
same independent reviewer that judges a single work item. The work it covers is
already integrated: this reports on the branch and never revises a run.

A shadow review is the same review made to measure the reviewer rather than to
judge the branch: its verdict approves nothing, so a different model can be
pointed at a branch state something else already decided without its answer ever
gating anything. --compare holds those verdicts up against the ones they shadow.

Options:
  --base <ref>      the base the branch accumulated over (required unless --compare)
  --branch <name>   the branch to review (default: the current branch), or with
                    --compare the only branch to compare (default: every branch)
  --shadow          measure the reviewer; the verdict approves nothing
  --model <name>    review with this model instead of the configured one (requires --shadow)
  --compare         compare the recorded shadow reviews with the reviews they shadow
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
