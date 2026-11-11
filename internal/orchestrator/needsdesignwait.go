package orchestrator

// How long each item has waited on the architect's design, and telling the
// Lead Product Manager about a P0 one that has waited more than a day.
//
// "Showing the wait" in docs/designs/needs-design-gate.md is what this keeps
// the record for: every pull of a watching session reads which open items carry
// the label, as the architect's wake does, and records each one the first time
// it is seen (runstate.NeedsDesignLabelled). The standing surfaces read that
// time to say "waiting on the architect's design since", and the scheduler's
// refusal says it too.
//
// A P0 item still waiting more than a day is reported to the Lead Product
// Manager once, as the harness's own report. The record keeps that it was, so
// the report is not filed again while the item stays labelled; the item's
// entry among the factory problems carries it on every reading after that,
// until the label comes off. Taking the label off and putting it back starts a
// new wait, which is reported again if it too runs past a day.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// NeedsDesignReportAfter is how long a P0 item waits on the architect's design
// before the Lead Product Manager is told.
const NeedsDesignReportAfter = 24 * time.Hour

// NeedsDesignLabelledRecord is since when each labelled item has waited. It is
// satisfied by *runstate.NeedsDesignLabelledStore.
type NeedsDesignLabelledRecord interface {
	Observe(ctx context.Context, labelled []string, at time.Time) (runstate.NeedsDesignLabelled, error)
	MarkReported(ctx context.Context, item string, at time.Time) (runstate.NeedsDesignLabelled, error)
}

// NeedsDesignLabelledReader reads the same record without writing it, for a
// pull and a surface that only say the wait.
type NeedsDesignLabelledReader interface {
	Load() (runstate.NeedsDesignLabelled, bool, error)
}

// needsDesignWaitWired reports a trigger that records the wait.
func (t Trigger) needsDesignWaitWired() bool {
	return t.NeedsDesign != nil && t.NeedsDesignLabelled != nil
}

// watchNeedsDesign records the labelled items as this pull reads them and
// reports each P0 one past a day that has not been reported. An error is said
// beside the pull, and the next pull tries again.
func (t Trigger) watchNeedsDesign(ctx context.Context) error {
	labelled, err := t.NeedsDesign.Labelled(ctx)
	if err != nil {
		return fmt.Errorf("read which work items carry the %s label, so how long each has waited is not recorded until it can be: %w", domain.NeedsDesignLabel, err)
	}
	ids := make([]string, 0, len(labelled))
	for _, item := range labelled {
		ids = append(ids, item.ID)
	}
	now := t.now()
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	record, err := t.NeedsDesignLabelled.Observe(write, ids, now)
	if err != nil {
		return fmt.Errorf("record since when each work item has carried the %s label: %w", domain.NeedsDesignLabel, err)
	}
	if t.Breakage == nil {
		return nil
	}
	var problems []string
	for _, item := range labelled {
		since, recorded := record.Since(item.ID)
		if !recorded || record.Reported(item.ID) || item.Priority == nil || *item.Priority != 0 || now.Sub(since) <= NeedsDesignReportAfter {
			continue
		}
		if err := t.reportNeedsDesignWait(item, since, now); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if _, err := t.NeedsDesignLabelled.MarkReported(write, item.ID, now); err != nil {
			problems = append(problems, fmt.Sprintf("record that the wait of %s was reported, so it may be reported again: %v", item.ID, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("report P0 work items waiting on the architect's design: %s", strings.Join(problems, "; "))
	}
	return nil
}

// reportNeedsDesignWait files the harness's own report to the Lead Product
// Manager about one P0 item waiting more than a day.
func (t Trigger) reportNeedsDesignWait(item NeedsDesignItem, since, now time.Time) error {
	attribution := t.Attribution
	attribution.Role = report.HarnessReporter
	attribution.Agent = ""
	attribution.RunID = fmt.Sprintf("%s-wait@%s", NeedsDesignWakeTask, since.UTC().Format(time.RFC3339))
	attribution.WorkItemID = item.ID
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleProductManager,
		Message:  needsDesignWaitMessage(item, since, now),
	}}, attribution, now)
	if err != nil {
		return fmt.Errorf("compose the report on %s: %w", item.ID, err)
	}
	for _, reported := range collected {
		if err := t.Breakage.Append(reported); err != nil {
			return fmt.Errorf("file the report on %s: %w", item.ID, err)
		}
	}
	return nil
}

// needsDesignWaitMessage is the two sentences the report is said in: what is
// waiting and since when, and what that costs.
func needsDesignWaitMessage(item NeedsDesignItem, since, now time.Time) string {
	named := item.ID
	if title := strings.Join(strings.Fields(item.Title), " "); title != "" {
		named += " (" + title + ")"
	}
	return fmt.Sprintf("The P0 work item %s has waited on the architect's design since %s, %s ago, and is still labelled %s. "+
		"No developer run starts it until she makes its design readable and removes the label, so look at whether she has what she needs or whether the item should be reordered; it stays among the factory problems until the label comes off, and this report is not filed again for this wait.",
		named, since.Local().Format("2006-01-02 15:04 MST"), waitedFor(now.Sub(since)), domain.NeedsDesignLabel)
}
