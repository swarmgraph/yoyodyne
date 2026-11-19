package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// The target branch does not require a pull request to be up to date with it,
// so the forge can merge a candidate onto a base nobody checked. After a
// landing through a pull request the harness therefore confirms that the merge
// commit's first parent is the base the candidate was built and checked on, and
// that the merge changed exactly what the candidate changed
// (gitworktree.CheckMergeCommit). Either difference is one critical report
// naming the merge. A landing the check could not be made for is one warning
// saying so, and never holds the landing up: the merge has already happened.

// MergeConfirmer is the part of the repository access the check reads.
type MergeConfirmer interface {
	CheckMergeCommit(ctx context.Context, mergeCommit, recordedBase, candidate string) (gitworktree.MergeCheck, error)
}

// mergeCheckRun is what a merge check's report is attributed to.
func mergeCheckRun(mergeCommit string) string { return "merge-check@" + mergeCommit }

// confirmMergeCommit checks one landed merge and files what it finds. It
// returns the check, and whether the merge matched.
func (p Pipeline) confirmMergeCommit(ctx context.Context, lander MergeConfirmer, workItemID, mergeCommit, base, candidate string, pullRequest int) (gitworktree.MergeCheck, bool, error) {
	check, err := lander.CheckMergeCommit(ctx, mergeCommit, base, candidate)
	if err != nil {
		return check, false, p.fileMergeReport(report.SeverityWarning, workItemID, mergeCommit, fmt.Sprintf(
			"The merge %s of %s could not be checked against the candidate it landed, so whether it landed the revision that was checked is not known: %v. A person has to compare the merge's first parent with %s and its files with the candidate %s.",
			mergeCommit, describePullRequest(pullRequest), err, base, candidate))
	}
	if check.Matches() {
		return check, true, nil
	}
	return check, false, p.fileMergeReport(report.SeverityCritical, workItemID, mergeCommit, fmt.Sprintf(
		"The merge %s of %s is not the revision that was checked: %s. "+
			"The review and the checks judged candidate %s built on %s, so what landed on the target branch was not judged; somebody has to read the merge and decide whether to revert or accept it.",
		mergeCommit, describePullRequest(pullRequest), check.Says(), candidate, base))
}

func describePullRequest(number int) string {
	if number > 0 {
		return fmt.Sprintf("pull request %d", number)
	}
	return "a pull request"
}

// fileMergeReport files one report from the harness, and says why where it
// could not be kept.
func (p Pipeline) fileMergeReport(severity report.Severity, workItemID, mergeCommit, message string) error {
	if p.Reports == nil {
		return fmt.Errorf("this pipeline has nowhere to keep a report")
	}
	collected, err := report.Collect([]report.Entry{{
		Severity: severity,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        mergeCheckRun(mergeCommit),
		WorkItemID:   workItemID,
		Build:        p.Build,
		ProductID:    p.Config.Product.ID,
		RepositoryID: string(p.Config.Product.RepositoryID),
	}, p.clock().Now())
	if err != nil {
		return fmt.Errorf("file the report on merge %s: %w", mergeCommit, err)
	}
	for _, reported := range collected {
		if err := p.Reports.Append(reported); err != nil {
			return fmt.Errorf("file the report on merge %s: %w", mergeCommit, err)
		}
	}
	return nil
}

// LandedMerge is one landing through a pull request to be checked after the
// fact: the merge on the target, the candidate it merged, and the base that
// candidate was built on.
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
		return fmt.Sprintf("%s %s: matches (first parent is the recorded base %s; files are the candidate's)", e.Merge, e.Subject, e.Check.RecordedBase)
	default:
		return fmt.Sprintf("%s %s: DIFFERS: %s", e.Merge, e.Subject, e.Check.Says())
	}
}

// MergeAuditor reads the landings on a target branch and checks merges.
type MergeAuditor interface {
	MergeConfirmer
	LandedPullRequests(ctx context.Context, ref string, since time.Time) ([]gitworktree.LandedPullRequest, error)
}

// AuditLandedMerges checks every landing on the target ref since a time, as the
// check made after each landing would have, and files each difference as that
// check does. A landing the repository cannot show a candidate for, such as a
// squash merge, is listed as not checked with the reason. Every landing is
// checked and listed even where a report could not be kept; those failures come
// back joined beside the full list.
func (p Pipeline) AuditLandedMerges(ctx context.Context, auditor MergeAuditor, ref string, since time.Time) ([]MergeAuditEntry, error) {
	landings, err := auditor.LandedPullRequests(ctx, ref, since)
	if err != nil {
		return nil, err
	}
	var entries []MergeAuditEntry
	var unfiled error
	for _, landed := range landings {
		entry := MergeAuditEntry{LandedMerge: LandedMerge{Merge: landed.Merge, Candidate: landed.Candidate, Base: landed.Base, Subject: landed.Subject}, Unchecked: landed.Unchecked}
		if landed.Unchecked == "" {
			var filed error
			entry.Check, entry.Matches, filed = p.confirmMergeCommit(ctx, auditor, "", landed.Merge, landed.Base, landed.Candidate, pullRequestNumber(landed.Subject))
			switch {
			case filed != nil:
				unfiled = errors.Join(unfiled, filed)
			case entry.Check.FirstParent == "":
				entry.Unchecked = "the check could not be made; a warning was filed"
			}
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
