package epicjoin

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Reports is where the harness files the Lead Product Manager the question about
// the evidence a check of the whole epic that could not tell lacked, and reads her
// handling of it. It is satisfied by *runstate.ReportStore.
type Reports interface {
	Append(reported report.Report) error
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
}

// Evidence is what the harness asks evidence with. docs/designs/epic-mode-second-
// part.md, "How a correcting piece names the check it came from", states the rule.
type Evidence struct {
	Reports      Reports
	Build        string
	RepositoryID string
}

// asking is a check that asks for its evidence, of one open naming.
type asking struct {
	naming epicprogress.Naming
	check  epicprogress.Check
}

func (j Joiner) asking() ([]asking, error) {
	view, err := j.read()
	if err != nil || !view.Record.On() || j.Evidence.Reports == nil {
		return nil, err
	}
	var found []asking
	for _, epic := range view.Record.OpenEpics() {
		entry, _ := view.Entry(epicprogress.Of(epic))
		if check, asks := entry.Asking(); asks {
			found = append(found, asking{epicprogress.Of(epic), check})
		}
	}
	return found, nil
}

// evidenceOwed reports a question about evidence to file, or an answer to it to
// read. A record of reports that cannot be read is an error.
func (j Joiner) evidenceOwed() (bool, error) {
	found, err := j.asking()
	if err != nil || len(found) == 0 {
		return false, err
	}
	filed, handled, err := j.reportsRead()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(found, func(one asking) bool {
		asked := one.check.Asked
		return asked == nil || !filed[asked.Report] || handled[asked.Report].ReportID != ""
	}), nil
}

func (j Joiner) reportsRead() (filed map[string]bool, handled map[string]report.Handling, err error) {
	reports, err := j.Evidence.Reports.List()
	if err != nil {
		return nil, nil, fmt.Errorf("read the reports: %w", err)
	}
	handlings, err := j.Evidence.Reports.Handlings()
	if err != nil {
		return nil, nil, fmt.Errorf("read the handlings of reports: %w", err)
	}
	filed = map[string]bool{}
	for _, reported := range reports {
		filed[reported.ID] = true
	}
	return filed, report.Handled(handlings), nil
}

// evidence files the one question each check that could not tell owes the Lead
// Product Manager, writing its identifier beside the check first, and records her
// handling of it once there is one. A report whose identifier is written and that
// a stop kept from being filed is filed under that identifier.
func (j Joiner) evidence() error {
	found, err := j.asking()
	if err != nil || len(found) == 0 {
		return err
	}
	filed, handled, err := j.reportsRead()
	if err != nil {
		return err
	}
	var problems []error
	for _, one := range found {
		asked := one.check.Asked
		if asked == nil {
			question, err := j.question(one, "", j.Now())
			if err == nil {
				err = j.Progress.AskEvidence(one.naming, one.check.ID, question.ID)
			}
			if err == nil {
				err = j.Evidence.Reports.Append(question)
			}
			problems = append(problems, err)
			continue
		}
		if !filed[asked.Report] {
			question, err := j.question(one, asked.Report, asked.At)
			if err == nil {
				err = j.Evidence.Reports.Append(question)
			}
			problems = append(problems, err)
			continue
		}
		if handling, answered := handled[asked.Report]; answered {
			operator := handling.OperatorReason
			if !handling.NeedsOperator {
				operator = ""
			}
			problems = append(problems, j.Progress.EvidenceAnswered(one.naming, one.check.ID, asked.Report, handling.Reason, handling.RecordedAt, operator))
		}
	}
	return errors.Join(problems...)
}

// question is the report asking the Lead Product Manager what evidence would
// settle what a check could not tell, under the identifier given or a new one.
func (j Joiner) question(one asking, id string, at time.Time) (report.Report, error) {
	message := fmt.Sprintf("The check of the whole epic %s, %s, which read commit %s, could not tell: %s. Say in your handling of this report what evidence would settle it: its reason is what the development manager's correcting piece gives. Where only the operator can act, handle it naming his reason, and the epic waits on him.",
		one.naming.WorkItemID, one.check.ID, one.check.Commit, oneline.Fold(epicprogress.CouldNotTell(one.check), report.MaxMessageBytes/2))
	collected, err := report.Collect([]report.Entry{{Severity: report.SeverityWarning, Message: message, For: domain.RoleProductManager}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "epic-check:" + one.check.ID,
		WorkItemID:   one.naming.WorkItemID,
		Build:        j.Evidence.Build,
		ProductID:    j.Product,
		RepositoryID: j.Evidence.RepositoryID,
	}, at)
	if err != nil {
		return report.Report{}, fmt.Errorf("ask for the evidence the check %s lacked: %w", one.check.ID, err)
	}
	question := collected[0]
	if id != "" {
		question.ID = id
	}
	question.DuringEpic = one.naming.WorkItemID
	return question, nil
}
