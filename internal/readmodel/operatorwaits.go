package readmodel

import (
	"fmt"
	"strings"
	"time"
)

// Since is when the thing an entry is about began waiting, read off the record
// the entry carries, and zero where that record holds no moment: a carried
// item and a gate are admitted work items whose record here carries no time,
// and the count of held work is a count rather than one record. It
// is derived like What and Whose rather than stored, so an entry and its age
// cannot disagree.
func (a Attention) Since() time.Time {
	switch a.Kind {
	case AttentionHold:
		switch {
		case a.OperatorHold != nil:
			return a.OperatorHold.HeldAt
		case a.IntakeHold != nil:
			return a.IntakeHold.HeldAt
		case a.CapacityHold != nil:
			return a.CapacityHold.Since
		}
	case AttentionDirective:
		if a.Directive != nil {
			return a.Directive.ReceivedAt
		}
	case AttentionAmendment:
		if a.Amendment != nil {
			return a.Amendment.RaisedAt
		}
	case AttentionOwedStep:
		if a.OwedStep != nil {
			return a.OwedStep.EndedAt
		}
	case AttentionPublication:
		// A dropped merge dates from the drop, since that is when it stopped
		// waiting on the forge; every other publication from the run's ending,
		// which is when it was left outstanding.
		if a.Publication != nil {
			if a.Publication.MergeDrop != nil && !a.Publication.MergeDrop.At.IsZero() {
				return a.Publication.MergeDrop.At
			}
			return a.Publication.EndedAt
		}
	case AttentionOutage:
		if a.Outage != nil {
			return a.Outage.Since
		}
	case AttentionReports:
		if a.Reports != nil {
			return a.Reports.Oldest
		}
	case AttentionAmendmentQueue:
		if a.AmendmentQueue != nil {
			return a.AmendmentQueue.Oldest
		}
	case AttentionStall:
		if a.Stall != nil {
			return a.Stall.Since
		}
	case AttentionDegradedService:
		if a.Service != nil {
			return a.Service.DiedAt
		}
	case AttentionFailingTask:
		if a.FailingTask != nil {
			return a.FailingTask.FirstAt
		}
	case AttentionServiceCopies:
		// Since the latest copy started, which is when there came to be more than
		// one.
		if a.ServiceCopies != nil {
			var latest time.Time
			for _, running := range a.ServiceCopies.Copies {
				if running.StartedAt.After(latest) {
					latest = running.StartedAt
				}
			}
			return latest
		}
	case AttentionOperatorAction:
		if a.OperatorAction != nil {
			return a.OperatorAction.Since
		}
	case AttentionUntracedPass:
		if a.UntracedPass != nil {
			return a.UntracedPass.StartedAt
		}
	case AttentionFactoryStall:
		if a.FactoryStall != nil {
			return a.FactoryStall.Since
		}
	case AttentionTrackerUnanswered:
		if a.TrackerListings != nil {
			return a.TrackerListings.FailingSince
		}
	case AttentionUnrunCheck:
		if a.UnrunCheck != nil {
			return a.UnrunCheck.Since
		}
	}
	return time.Time{}
}

// maxOperatorWaits bounds how many entries the development manager's sweep is
// shown. It is wider than a head of the attention line, because this list is
// the one she is asked to work through, and bounded all the same, named
// entries included, because a wake message over the harness's size limit is a
// firing refused before its first turn — which asks her nothing at all.
const maxOperatorWaits = 20

// maxOperatorWaitBytes bounds what one entry says on the sweep, for the same
// reason: a finding's own words can run long, and the whole of it is on
// `yoyo status --json` and on the record it names.
const maxOperatorWaitBytes = 600

// RenderOperatorWaits is the needs-a-human entries whose move is the
// operator's, as a scheduled pass of the development manager is shown them:
// each with its kind and the record it is about, what it says, since when in
// this machine's zone, and how long ago that was. It is the same derivation the
// "Needs a human" line prints from, so what she is asked to check is what the
// operator is shown as his. A reading that could not be made whole says so,
// and an empty one says so in words: a pass shown nothing could not tell
// nothing waiting on him from nothing read.
func (s Standing) RenderOperatorWaits(now time.Time) string {
	var operator []Attention
	for _, waiting := range s.NeedsHuman {
		if waiting.Mover == MoverOperator {
			operator = append(operator, waiting)
		}
	}
	var rendered strings.Builder
	rendered.WriteString("## Waiting on the operator\n\n")
	if len(operator) == 0 {
		rendered.WriteString("Nothing on the needs-a-human line is the operator's.\n")
	} else {
		fmt.Fprintf(&rendered, "%s on the needs-a-human line %s the operator's, as `yoyo status` derives %s:\n\n",
			entries(len(operator)), isAre(len(operator)), themIt(len(operator)))
		for index, waiting := range operator {
			if index == maxOperatorWaits {
				fmt.Fprintf(&rendered, "- and %s more not listed here; `yoyo status --json` carries every one under standing.needs_human\n",
					entries(len(operator)-maxOperatorWaits))
				break
			}
			fmt.Fprintf(&rendered, "- [%s] %s — %s\n", operatorWaitLabel(waiting),
				singleLine(waiting.CitedWhat(), maxOperatorWaitBytes), operatorWaitSince(waiting.Since(), now))
		}
		rendered.WriteString("\n" + operatorWaitsAsk)
	}
	if s.NeedsHumanProblem != "" {
		fmt.Fprintf(&rendered, "\n%s%s\n", partialRead, s.NeedsHumanProblem)
	}
	return rendered.String()
}

// operatorWaitsAsk is what the list asks of her, said with it so it reaches
// her whatever her sweep's prompt says: only a change to the fundamental goals
// is truly the operator's, and each entry that is not is settled, sent to its
// owner, and explained, with what she did written where the entry's record is.
const operatorWaitsAsk = "Only a change to the fundamental goals is truly the operator's. For each other entry: settle it if it is yours, send it to the role that owns it if it is not, and file a defect with the Lead Product Manager saying why it reached him. Record what you did on the record the entry is about — the work item it names, as a note, or the run's stoppage, as your decision on the docket — and where you can write to neither, name the entry's kind and record in your sweep's finding.\n"

// operatorWaitLabel is the entry's kind and, where it has one, the identifier
// of the record it is about, which is where her handling of it is recorded.
func operatorWaitLabel(waiting Attention) string {
	label := string(waiting.Kind)
	if waiting.ID != "" {
		label += " " + waiting.ID
	}
	if waiting.WorkItemID != "" && waiting.WorkItemID != waiting.ID {
		label += ", item " + waiting.WorkItemID
	}
	return label
}

// operatorWaitSince says since when, in this machine's zone with the zone
// named, and how long ago that was — or that the record holds no moment,
// rather than leaving the age out and letting it read as new.
func operatorWaitSince(since, now time.Time) string {
	if since.IsZero() {
		return "since when is not recorded on it"
	}
	return fmt.Sprintf("since %s, %s", localMoment(since), agoSaid(now.Sub(since)))
}

// entries counts entries, which count cannot: its plural is "entrys".
func entries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

func themIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
