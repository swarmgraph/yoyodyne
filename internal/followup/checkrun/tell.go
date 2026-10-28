package checkrun

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Reports is where the runner tells whoever waits that a follow-up's check
// passed. It is satisfied by *runstate.ReportStore.
type Reports interface {
	Append(report.Report) error
}

// Teller tells the role that waits on a follow-up that its check passed, as a
// report addressed to that role: a report addressed to a role is put on that
// role's list and in front of its next pass, which is how a role is told
// anything between its turns. It is filed in the harness's name, because no
// role found it.
//
// A follow-up the operator waits on is not reported here, because a report is
// never addressed to him; he is told in the channel, as the hold's release is.
type Teller struct {
	Reports      Reports
	ProductID    domain.ProductID
	RepositoryID string
	Build        string
}

// tell files the report for one passed follow-up, and reports false where
// nobody is told because the one who waits is the operator.
func (t Teller) tell(passed followup.FollowUp) (bool, error) {
	role := domain.AgentRole(passed.Waits)
	if passed.Waits.IsOperator() || !role.Valid() {
		return false, nil
	}
	message := fmt.Sprintf("Follow-up %s, about %s, ended because its check passed at %s: %s. You were recorded as waiting on it, so nothing more is needed unless you were waiting to act on it.",
		passed.ID, about(passed), passed.Passed.At.Local().Format("Jan 2 15:04 MST"), strings.TrimSuffix(passed.Passed.Result, "."))
	collected, err := report.Collect([]report.Entry{{Severity: report.SeverityNote, Message: message, For: role}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "follow-up@" + passed.ID,
		Build:        t.Build,
		ProductID:    t.ProductID,
		RepositoryID: t.RepositoryID,
	}, passed.Passed.At)
	if err != nil {
		return false, err
	}
	for _, reported := range collected {
		if err := t.Reports.Append(reported); err != nil {
			return false, err
		}
	}
	return true, nil
}

func about(f followup.FollowUp) string {
	said := make([]string, 0, len(f.About))
	for _, subject := range f.About {
		said = append(said, string(subject.Kind)+" "+subject.ID)
	}
	return strings.Join(said, ", ")
}
