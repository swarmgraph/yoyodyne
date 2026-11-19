package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The target branch does not require a pull request to be up to date with it,
// so the forge can merge a change onto a base nobody checked. After a landing
// through a pull request the harness therefore confirms that the merge commit's
// first parent is the base the change was checked and reviewed on, and that the
// merge changed exactly what the change changed (gitworktree.CheckMergeCommit).
// Either difference is one critical report naming the merge. A landing the
// check could not be made for is one warning saying so, and never holds the
// landing up: the merge has already happened.
//
// The base and the change come from what the harness recorded for the landing:
// the merge queue's attempt for a queue candidate, and the run's own review
// (the commits it reviewed and ran its checks over) for a run's pull request.

// MergeConfirmer is the part of the repository access the check reads.
type MergeConfirmer interface {
	CheckMergeCommit(ctx context.Context, mergeCommit, recordedBase, candidate string) (gitworktree.MergeCheck, error)
}

// LandingReporter is what files the report a merge check makes. The zero value
// has nowhere to keep one, and says so rather than losing the finding.
type LandingReporter struct {
	Reports      ReportCollector
	Build        string
	ProductID    domain.ProductID
	RepositoryID string
	Clock        execution.Clock
}

func (p Pipeline) landingReporter() LandingReporter {
	return LandingReporter{Reports: p.Reports, Build: p.Build, ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID), Clock: p.Clock}
}

// confirmMergeCommit checks one landed merge and files what it finds. It
// returns the check, whether the merge matched, and why a report could not be
// kept where one could not.
func (p Pipeline) confirmMergeCommit(ctx context.Context, lander MergeConfirmer, workItemID, mergeCommit, base, candidate string, pullRequest int) (gitworktree.MergeCheck, bool, error) {
	return p.landingReporter().Confirm(ctx, lander, workItemID, mergeCommit, base, candidate, pullRequest)
}

// Confirm checks one landed merge against the base and change recorded for it.
func (l LandingReporter) Confirm(ctx context.Context, lander MergeConfirmer, workItemID, mergeCommit, base, candidate string, pullRequest int) (gitworktree.MergeCheck, bool, error) {
	check, err := lander.CheckMergeCommit(ctx, mergeCommit, base, candidate)
	if err != nil {
		return check, false, l.file(report.SeverityWarning, workItemID, mergeCommit, fmt.Sprintf(
			"The merge %s of %s could not be checked against the change it landed, so whether it landed the revision that was checked is not known: %v. The merge has to be compared by hand: its first parent with %s, and its files with the change %s.",
			mergeCommit, describePullRequest(pullRequest), err, base, candidate))
	}
	if check.Matches() {
		return check, true, nil
	}
	return check, false, l.file(report.SeverityCritical, workItemID, mergeCommit, fmt.Sprintf(
		"The merge %s of %s is not the revision that was checked: %s. "+
			"The review and the checks judged %s built on %s, so what landed on the target branch was not judged; the merge has to be read, and reverted or accepted.",
		mergeCommit, describePullRequest(pullRequest), check.Says(), candidate, base))
}

// ConfirmRun checks the merge a run's pull request landed in, against the
// commits the run's review judged. A run with no recorded review, or a merge
// the repository access cannot check, is left alone: only a landing that can be
// compared is reported on. It returns nothing the landing waits for.
func (l LandingReporter) ConfirmRun(ctx context.Context, worktrees any, state runstate.State, pullRequest int, mergeCommit string) {
	confirmer, ok := worktrees.(MergeConfirmer)
	base, candidate, recorded := RecordedRunLanding(state)
	if !ok || !recorded || mergeCommit == "" {
		return
	}
	_, _, _ = l.Confirm(ctx, confirmer, state.WorkItemID, mergeCommit, base, candidate, pullRequest)
}

// RecordedRunLanding is the base and the change a run's review and checks
// judged, which is what its pull request's merge is compared with.
func RecordedRunLanding(state runstate.State) (base, candidate string, ok bool) {
	base = nonEmpty(state.ReviewBaseCommit, state.BaseCommit)
	candidate = nonEmpty(state.ReviewHeadCommit, state.HarnessCommit)
	return base, candidate, base != "" && candidate != ""
}

func describePullRequest(number int) string {
	if number > 0 {
		return fmt.Sprintf("pull request %d", number)
	}
	return "a pull request"
}

func (l LandingReporter) clock() execution.Clock {
	if l.Clock == nil {
		return execution.RealClock{}
	}
	return l.Clock
}

// file files one report from the harness, and says why where it could not be
// kept.
func (l LandingReporter) file(severity report.Severity, workItemID, mergeCommit, message string) error {
	if l.Reports == nil {
		return errors.New("nowhere to keep a report")
	}
	collected, err := report.Collect([]report.Entry{{
		Severity: severity,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "merge-check@" + mergeCommit,
		WorkItemID:   workItemID,
		Build:        l.Build,
		ProductID:    l.ProductID,
		RepositoryID: l.RepositoryID,
	}, l.clock().Now())
	if err != nil {
		return fmt.Errorf("file the report on merge %s: %w", mergeCommit, err)
	}
	for _, reported := range collected {
		if err := l.Reports.Append(reported); err != nil {
			return fmt.Errorf("file the report on merge %s: %w", mergeCommit, err)
		}
	}
	return nil
}

// LandedMerge is one landing through a pull request to be checked after the
// fact: the merge on the target, the change it merged, and the base that change
// was checked on.
type LandedMerge struct {
	Merge     string
	Candidate string
	Base      string
	Subject   string
}

// MergeAuditEntry is the result of checking one landed merge.
type MergeAuditEntry struct {
	LandedMerge
	// Unchecked is why the merge could not be checked, where it could not.
	Unchecked string
	Check     gitworktree.MergeCheck
	Matches   bool
}

// Says is one line for the merge: what was checked and how it came out.
func (e MergeAuditEntry) Says() string {
	switch {
	case e.Unchecked != "":
		return fmt.Sprintf("%s %s: not checked: %s", e.Merge, e.Subject, e.Unchecked)
	case e.Matches:
		return fmt.Sprintf("%s %s: matches (first parent is the recorded base %s; files are the change's)", e.Merge, e.Subject, e.Check.RecordedBase)
	default:
		return fmt.Sprintf("%s %s: DIFFERS: %s", e.Merge, e.Subject, e.Check.Says())
	}
}

// MergeAuditor reads the landings on a target branch and checks merges.
type MergeAuditor interface {
	MergeConfirmer
	LandedPullRequests(ctx context.Context, ref string, since time.Time) ([]gitworktree.LandedPullRequest, error)
}

// RecordedLandings finds the base and the change the harness recorded for a
// pull request that is not a merge queue candidate, by its number, and whether
// it recorded any.
type RecordedLandings func(pullRequest int) (base, candidate string, found bool)

// RunLandings is the recorded landings of every run that has a pull request,
// read from the runs' own records. Where more than one run carries a number,
// the last one read wins; a number is one run's pull request.
func RunLandings(states []runstate.State) RecordedLandings {
	recorded := map[int]runstate.State{}
	for _, state := range states {
		if state.PullRequest != nil && state.PullRequest.Number > 0 {
			if _, _, ok := RecordedRunLanding(state); ok {
				recorded[state.PullRequest.Number] = state
			}
		}
	}
	return func(number int) (string, string, bool) {
		state, found := recorded[number]
		if !found {
			return "", "", false
		}
		base, candidate, _ := RecordedRunLanding(state)
		return base, candidate, true
	}
}

// AuditLandedMerges checks every landing on the target ref since a time, as the
// check made after each landing would have, and files each difference as that
// check does. A merge queue candidate is checked against the base it was built
// on; any other merge against what the run that made its pull request recorded
// (recorded). A landing the harness recorded nothing for, such as a pull request
// a person opened, or a squash merge, is listed as not checked with the reason.
// Every landing is checked and listed even where a report could not be kept;
// those failures come back joined beside the full list.
func (p Pipeline) AuditLandedMerges(ctx context.Context, auditor MergeAuditor, recorded RecordedLandings, ref string, since time.Time) ([]MergeAuditEntry, error) {
	landings, err := auditor.LandedPullRequests(ctx, ref, since)
	if err != nil {
		return nil, err
	}
	var entries []MergeAuditEntry
	var unfiled error
	for _, landed := range landings {
		number := pullRequestNumber(landed.Subject)
		entry := MergeAuditEntry{LandedMerge: LandedMerge{Merge: landed.Merge, Candidate: landed.Candidate, Base: landed.Base, Subject: landed.Subject}, Unchecked: landed.Unchecked}
		if entry.Unchecked == "" && !landed.QueueCandidate {
			base, candidate, found := "", "", false
			if recorded != nil {
				base, candidate, found = recorded(number)
			}
			if !found {
				entry.Unchecked = "the harness recorded no review of this pull request's change, so there is no checked base to compare the merge with"
			} else {
				entry.Base, entry.Candidate = base, candidate
			}
		}
		if entry.Unchecked == "" {
			var filed error
			entry.Check, entry.Matches, filed = p.confirmMergeCommit(ctx, auditor, "", landed.Merge, entry.Base, entry.Candidate, number)
			if entry.Check.FirstParent == "" {
				entry.Unchecked = "the check could not be made; a warning was filed"
			}
			unfiled = errors.Join(unfiled, filed)
		}
		entries = append(entries, entry)
	}
	return entries, unfiled
}

// pullRequestNumber reads the number from the subject the forge gives a merge
// ("Merge pull request #12 from ..."), and is zero where there is none.
func pullRequestNumber(subject string) int {
	_, rest, found := strings.Cut(subject, "#")
	if !found {
		return 0
	}
	number := 0
	for _, digit := range rest {
		if digit < '0' || digit > '9' {
			break
		}
		number = number*10 + int(digit-'0')
	}
	return number
}
