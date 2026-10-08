package orchestrator

// The closed work a Lead Product Manager's pass audits against the standing
// goals.
//
// The standing goals apply to every work item whichever goal it serves, and a
// reviewer judges each change against them before it lands. A goal enforced only
// where the reviewer happened to look is not enforced, so the Lead Product
// Manager's sweep checks what closed since its last pass and admits a correction
// for a violation. The harness hands the pass that list rather than leaving the
// sweep to find it: a role asked to look back over "recent" work decides for
// itself what recent means, and the work nobody listed is the work nobody
// audits.
//
// The audit is shared by lane. A program manager instance whose lane is a
// standing goal audits the closed work in its lane, and records each audit in
// its own pass's account; the sweep is handed those audits beside the list, so
// it reads them rather than auditing the same item against the same goal again.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// RecurringClosedWork lists the work items the tracker closed after a moment,
// oldest first, at most limit of them, and how many more closed after those. It
// changes nothing in the tracker.
type RecurringClosedWork interface {
	Closed(ctx context.Context, since time.Time, limit int) (ClosedWork, error)
}

// ClosedWork is one reading of the work closed after a moment.
type ClosedWork struct {
	Items []ClosedItem
	// More is how many items closed after since and are not in Items because the
	// listing reached its limit.
	More int
}

// ClosedItem is one closed work item as the audit reads it: what it was, when
// it closed, and what landed, in the words its own record gives.
type ClosedItem struct {
	ID       string
	Title    string
	ClosedAt time.Time
	// Landed is the account of what landed: the independent reviewer's summary
	// of the approved change where the item carries one, and the reason the
	// tracker closed it with otherwise.
	Landed string
}

// closedWorkLimit is how many closed items one pass is handed. It is below the
// number of audits one turn may carry, and small enough that the listing and its
// summaries leave the pass's message room for everything else it carries.
const closedWorkLimit = 15

// closedWorkLookback is how far back a task's first pass looks, where no earlier
// pass of it says where the last listing reached.
const closedWorkLookback = 7 * 24 * time.Hour

// maxClosedTitleBytes and maxLandedBytes bound one item's lines in the listing.
const (
	maxClosedTitleBytes = 160
	maxLandedBytes      = 480
)

// maxSharedAudits bounds how many of the program managers' audits one pass is
// handed. They are already recorded where `yoyo sweeps` reads them; this is
// what the pass needs to keep from repeating them.
const maxSharedAudits = 30

// closedWorkSection is the closed work handed to a Lead Product Manager's pass,
// with the audits the program managers made beside it, and how far the listing
// reached for the record. A listing that could not be read says so, and the
// next pass is handed the same range again.
func (t Trigger) closedWorkSection(ctx context.Context, name string) (string, time.Time, string) {
	now := t.now()
	since, lastPass := now.Add(-closedWorkLookback), time.Time{}
	var earlier []runstate.Sweep
	problem := ""
	if t.Reports != nil {
		recorded, _, err := t.Reports.List()
		if err != nil {
			problem = fmt.Sprintf("the earlier passes of %s could not be read, so this pass is handed what closed in the last %s rather than since its last pass: %v", name, closedWorkLookback, err)
		}
		earlier = recorded
	}
	for _, pass := range earlier {
		if pass.Task != name || pass.Unfinished() {
			continue
		}
		lastPass = pass.StartedAt
		since = pass.StartedAt
		if !pass.ClosedThrough.IsZero() {
			since = pass.ClosedThrough
		}
	}
	reading, err := t.ClosedWork.Closed(ctx, since, closedWorkLimit)
	if err != nil {
		unread := fmt.Sprintf("the work closed since %s could not be listed, so nothing was audited on this pass and the next pass is handed the same range: %v", localStamp(since), err)
		return "## Work closed since your last pass\n\n" + unread + "\n", since, joinProblems(problem, unread)
	}
	through := now
	if reading.More > 0 && len(reading.Items) > 0 {
		// The next pass starts at the last item listed, a moment early, so an item
		// that closed in the same second as it and did not fit is not skipped. One
		// listed again is audited twice at worst, and the duplicate check keeps
		// that to one correction.
		first, last := reading.Items[0].ClosedAt, reading.Items[len(reading.Items)-1].ClosedAt
		through = last
		if last.After(first) {
			through = last.Add(-time.Nanosecond)
		}
	}
	var section strings.Builder
	section.WriteString("## Work closed since your last pass\n\n")
	if len(reading.Items) == 0 {
		fmt.Fprintf(&section, "Nothing closed since %s, so there is nothing to audit on this pass.\n", localStamp(since))
	} else {
		fmt.Fprintf(&section, "%d item(s) closed since %s, oldest first. Audit each against every standing goal: read the item where its account below is not enough, and judge what landed, not what was asked for.", len(reading.Items), localStamp(since))
		if reading.More > 0 {
			fmt.Fprintf(&section, " %d more closed after these and are handed to your next pass.", reading.More)
		}
		section.WriteString("\n\n")
		for _, item := range reading.Items {
			fmt.Fprintf(&section, "- %s (%s), closed %s: %s\n", item.ID, oneline.Fold(item.Title, maxClosedTitleBytes), localStamp(item.ClosedAt), landedAccount(item.Landed))
		}
	}
	section.WriteString(t.sharedAudits(earlier, lastPass, since))
	section.WriteString("\n" + sweep.AuditContract() + "\n")
	return section.String(), through, problem
}

// sharedAudits is what the program manager instances audited since this task's
// last pass, so the sweep reads their findings rather than repeating them. An
// instance audits only where its lane is a standing goal, so an audit on its
// record is one its lane covers.
func (t Trigger) sharedAudits(earlier []runstate.Sweep, lastPass, since time.Time) string {
	from := lastPass
	if from.IsZero() {
		from = since
	}
	var lines []string
	omitted := 0
	for i := len(earlier) - 1; i >= 0; i-- {
		pass := earlier[i]
		if pass.Role != domain.RoleProgramManager || pass.Result == nil || pass.EndedAt.Before(from) {
			continue
		}
		for _, audit := range pass.Result.Audits {
			if len(lines) >= maxSharedAudits {
				omitted++
				continue
			}
			lines = append(lines, "- "+describeAudit(pass.Agent, audit))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	said := "\n## Audits the program managers already made\n\nA program manager whose lane is a standing goal audits the closed work in its lane. These are its audits since your last pass, newest first. Do not audit these items again against the goals named; read each finding, take up a deferred violation if your pass has room for it, and record your own audits only for what they did not cover.\n\n" + strings.Join(lines, "\n") + "\n"
	if omitted > 0 {
		said += fmt.Sprintf("%d further audit(s) are not listed here; `yoyo sweeps --json` carries them.\n", omitted)
	}
	return said
}

// describeAudit is one audit on one line, in the words the listing uses.
func describeAudit(agent string, audit sweep.Audit) string {
	said := fmt.Sprintf("%s, audited by %s against %s: %s", strings.TrimSpace(audit.Item), agent,
		oneline.Fold(strings.Join(audit.Goals, "; "), maxClosedTitleBytes), audit.Finding)
	if audit.Finding != sweep.AuditBroken {
		return said
	}
	said += " — " + oneline.Fold(audit.Detail, maxLandedBytes)
	if correction := strings.TrimSpace(audit.Correction); correction != "" {
		return said + "; corrected by " + correction
	}
	return said + "; deferred, with no correction admitted yet"
}

// landedAccount is what landed, on one bounded line, and says so where the
// item's record gives no account at all.
func landedAccount(landed string) string {
	if strings.TrimSpace(landed) == "" {
		return "its record gives no account of what landed; read the item"
	}
	return oneline.Fold(landed, maxLandedBytes)
}

// localStamp is a moment in this machine's zone with the zone named.
func localStamp(at time.Time) string {
	return at.Local().Format("2006-01-02 15:04 MST")
}

func joinProblems(problems ...string) string {
	var kept []string
	for _, problem := range problems {
		if strings.TrimSpace(problem) != "" {
			kept = append(kept, problem)
		}
	}
	return strings.Join(kept, "; ")
}
