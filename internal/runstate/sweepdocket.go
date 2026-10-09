package runstate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// DocketDelivery is the harness's account of a pass's docket delivery, rather
// than the role's claim about what it read. Undelivered keeps the order for the
// next pass; Oldest names the longest wait among them for someone reading the
// pass's record. A provider refusal leaves its attempted slice undelivered.
//
// Whole and Cut are how many entries the pass showed whole and how many it
// showed only cut: with their evidence cut short of its end, or named in one
// line and never shown with their evidence. An entry named in one line on one
// turn and shown whole on a later one is counted whole. Both are absent on
// records written before they were kept.
type DocketDelivery struct {
	Delivered   int                     `json:"delivered"`
	Whole       int                     `json:"whole,omitempty"`
	Cut         int                     `json:"cut,omitempty"`
	Undelivered []triage.WindowPosition `json:"undelivered,omitempty"`
	Oldest      *UndeliveredDocketEntry `json:"oldest,omitempty"`
}

// Shown says how many entries the pass showed whole and how many cut, or
// nothing on a record that kept neither count.
func (d DocketDelivery) Shown() string {
	if d.Whole == 0 && d.Cut == 0 {
		return ""
	}
	return fmt.Sprintf("showed %d docket entry(s) whole and %d cut — cut short of their evidence, or named in one line", d.Whole, d.Cut)
}

type UndeliveredDocketEntry struct {
	Position      triage.WindowPosition `json:"position"`
	WorkItemID    string                `json:"work_item_id,omitempty"`
	WorkItemTitle string                `json:"work_item_title,omitempty"`
	RunID         string                `json:"run_id,omitempty"`
}

func (d DocketDelivery) Validate() error {
	var problems []error
	if d.Delivered < 0 {
		problems = append(problems, errors.New("docket delivered count cannot be negative"))
	}
	if d.Whole < 0 || d.Cut < 0 {
		problems = append(problems, errors.New("the counts of docket entries shown whole and cut cannot be negative"))
	}
	if d.Whole > d.Delivered {
		problems = append(problems, errors.New("a pass cannot have shown more docket entries whole than it delivered"))
	}
	seen := map[triage.WindowPosition]bool{}
	for _, at := range d.Undelivered {
		if strings.TrimSpace(at.Key) == "" || len(at.Key) > triage.MaxKeyBytes || at.Since.IsZero() {
			problems = append(problems, errors.New("an undelivered docket entry requires its key and stoppage time"))
		}
		if seen[at] {
			problems = append(problems, errors.New("an undelivered docket entry is listed twice"))
		}
		seen[at] = true
	}
	if len(d.Undelivered) == 0 && d.Oldest != nil {
		problems = append(problems, errors.New("a fully delivered docket has no oldest undelivered entry"))
	}
	if len(d.Undelivered) > 0 {
		if d.Oldest == nil || !seen[d.Oldest.Position] {
			problems = append(problems, errors.New("the oldest undelivered docket entry must be among those not delivered"))
		} else {
			for _, at := range d.Undelivered {
				if at.Since.Before(d.Oldest.Position.Since) {
					problems = append(problems, errors.New("the oldest undelivered docket entry must have the earliest stoppage time"))
					break
				}
			}
		}
	}
	return errors.Join(problems...)
}

// Says is the one description every surface reads from the pass's problem.
func (d DocketDelivery) Says() string {
	if len(d.Undelivered) == 0 || d.Oldest == nil {
		return ""
	}
	item := d.Oldest.WorkItemID
	if item == "" {
		item = "an entry whose work item could not be read"
	} else if title := oneline.Fold(d.Oldest.WorkItemTitle, 160); title != "" {
		item = title + " (" + item + ")"
	} else {
		item = "a work item with no recorded title (" + item + ")"
	}
	return fmt.Sprintf("%d live docket entry(s) were never delivered on this pass; the oldest is %s, docket entry %s, stopped at %s. The next pass puts these entries ahead of entries already shown.",
		len(d.Undelivered), item, d.Oldest.Position.Key,
		d.Oldest.Position.Since.Local().Format("2006-01-02 15:04 MST"))
}
