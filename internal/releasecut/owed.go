package releasecut

// A release item is finished when its tag is on the remote, not when its notes
// land. Its own reviewed run lands the notes, and the tag is cut after them: by
// this pass under approvals.releases: automatic, and by a person under human.
// So the run that lands the notes does not close the item. It leaves it open,
// parked under TagOwedParking, which keeps any other run from being started on
// it and says what it waits for (internal/orchestrator, landing.go). This
// pass, under either setting, closes each item parked that way once the
// remote holds its tag, recording the tag and the commit it names; under human
// it also says once, on the item and to the Lead Product Manager, that cutting
// the tag is a step only the operator can take, naming the commands.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// ItemVersion is the version a decided release item is for, and false for an
// item that is not one: finished, not admitted by the Lead Product Manager, or
// not titled for a version.
func ItemVersion(item beads.WorkItem) (string, bool) { return decidedVersion(item) }

// tagOwedLead is how a parking that holds a release open for its tag begins.
// The rest of the reason is free to change; this is what OwesTag reads.
func tagOwedLead(version string) string {
	return "The notes for " + version + " landed through run "
}

// TagOwedParking is the parking reason a release item is left under once the
// run that lands its notes has integrated, until its tag is on the remote.
func TagOwedParking(version, runID string) domain.WorkItemParking {
	return domain.WorkItemParking(fmt.Sprintf("%s%s, and this item stays open until the %s tag is on the remote; the release step of the maintenance pass closes it then, recording the tag and the commit it names",
		tagOwedLead(version), runID, version))
}

// OwesTag reports a decided release that is parked only because its tag is
// still owed, and the version. A release the Lead Product Manager parked for
// any other reason carries her reason instead, and is not one.
func OwesTag(item beads.WorkItem) (string, bool) {
	version, ok := decidedVersion(item)
	if !ok || !item.Parking.Parked() {
		return "", false
	}
	return version, strings.HasPrefix(item.Parking.Reason(), tagOwedLead(version))
}

// heldBack reports a release somebody parked: one parked only for its tag is
// not, because the tag is what this pass is for.
func heldBack(item beads.WorkItem) bool {
	_, owed := OwesTag(item)
	return item.Parking.Parked() && !owed
}

// operatorStepLead is how the note saying the operator owes the cut begins, so
// it is written once on each item.
func operatorStepLead(version string) string {
	return "Cutting " + version + " is a step only the operator can take"
}

// PersonCutCommands is what a person runs to cut and publish a release: the
// cut, which runs every check and writes the tag, and the push it prints.
func PersonCutCommands(version, remote string) string {
	return fmt.Sprintf("`make release VERSION=%s` in the primary checkout, then `git push %s %s`", version, remote, version)
}

// settleOwed closes every release held open only for its tag once the remote
// holds the tag, and under human says once on each still owed that the cut is
// the operator's. It says what it did, and what it could not.
func (c *Cutter) settleOwed(ctx context.Context, items []beads.WorkItem) (said, problems []string) {
	for _, item := range Decided(items) {
		version, owed := OwesTag(item)
		if !owed {
			continue
		}
		commit, present, err := c.remoteTag(ctx, version)
		if err != nil {
			problems = append(problems, fmt.Sprintf("whether %s holds %s could not be read, so %s stays open: %v", c.remote(), version, item.ID, err))
			continue
		}
		if present {
			reason := fmt.Sprintf("The %s tag is on %s, naming commit %s, read back at %s. This release's notes landed through its reviewed run, so the release is finished.",
				version, c.remote(), commit, local(c.now()))
			if _, err := c.Tracker.Complete(ctx, item.ID, reason); err != nil {
				problems = append(problems, fmt.Sprintf("%s is on %s but %s could not be closed, and is tried again on the next pass: %v", version, c.remote(), item.ID, err))
				continue
			}
			said = append(said, fmt.Sprintf("closed %s, because %s is on %s at %s", item.ID, version, c.remote(), short(commit)))
			continue
		}
		if c.Mode == domain.ApprovalAutomatic {
			continue
		}
		if problem := c.askOperator(ctx, item, version); problem != "" {
			problems = append(problems, problem)
		}
	}
	return said, problems
}

// askOperator says once, on the item and in a report the Lead Product Manager
// records as the operator's, that the tag is his to cut. The report is filed
// first, so a note that could not be written is tried again with it rather
// than leaving the report unfiled behind a note that stops the asking.
func (c *Cutter) askOperator(ctx context.Context, item beads.WorkItem, version string) string {
	shown, err := c.Tracker.Show(ctx, item.ID)
	if err != nil {
		return fmt.Sprintf("%s could not be read to see whether the operator was told its tag is his to cut: %v", item.ID, err)
	}
	if strings.Contains(shown.Notes, operatorStepLead(version)) {
		return ""
	}
	commands := PersonCutCommands(version, c.remote())
	if c.Reports == nil {
		return "no report pile is wired, so the operator was not asked to cut " + version
	}
	id, err := report.NewID()
	if err != nil {
		return err.Error()
	}
	message := fmt.Sprintf("%s: cut and push the %s tag. Its notes landed on main through %s, and approvals.releases is human, so the harness does not cut it: run %s. The harness closes %s once %s holds the tag.",
		ownership.OperatorHumanGate, version, item.ID, commands, item.ID, c.remote())
	if err := c.Reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion, ID: id, Role: report.HarnessReporter,
		RunID: "release-cut:" + version + "@owed", WorkItemID: item.ID, Build: c.Build,
		ProductID: c.Product, RepositoryID: c.RepositoryID, Severity: report.SeverityWarning,
		Message: oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)), RecordedAt: c.now().UTC(),
	}); err != nil {
		return fmt.Sprintf("the operator could not be asked to cut %s, and is asked again on the next pass: %v", version, err)
	}
	note := fmt.Sprintf("%s, because approvals.releases is human: run %s. This item stays open until %s holds the %s tag, and the release step of the maintenance pass closes it then, recording the tag and the commit it names.",
		operatorStepLead(version), commands, c.remote(), version)
	if _, err := c.Tracker.RecordOutcome(ctx, item.ID, note); err != nil {
		return fmt.Sprintf("the note asking the operator to cut %s could not be written on %s, and is tried again on the next pass: %v", version, item.ID, err)
	}
	return ""
}
