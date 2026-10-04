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
type DocketDelivery struct {
	Delivered   int                     `json:"delivered"`
	Undelivered []triage.WindowPosition `json:"undelivered,omitempty"`
	Oldest      *UndeliveredDocketEntry `json:"oldest,omitempty"`
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
