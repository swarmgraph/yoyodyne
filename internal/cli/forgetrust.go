package cli

// A product started where GitHub's certificate cannot be verified.
//
// Every part the supervisor starts inherits the supervisor's macOS sandbox, if
// it has one, and so does every gh command those parts run; a supervisor
// started from inside an AI session's shell is one nothing in the product can
// read GitHub from (internal/forgetrust says why). So `yoyo start` does not
// detach a supervisor from such a shell: it starts the installed launch agent,
// which launchd runs outside whatever sandbox asked it to, or refuses and says
// where to start it instead (forgereach.go).
//
// A supervisor already running that way -- started before this check existed,
// or by launchd somewhere that cannot verify either -- files one report saying
// so, rather than one per pass of every check that then fails.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/forgetrust"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// forgeTrustProbe is the check a product opened from the machine is given. It
// is a variable only so the test that starts a real supervisor can run inside
// a sandbox that would refuse it; nothing else sets it.
var forgeTrustProbe = forgetrust.Probe

// forgeTrustReportRun is what a certificate report is attributed to, which is
// how a supervisor finds one already filed and not yet dealt with.
const forgeTrustReportRun = "supervisor-certificate-check"

// forgeTrustReports is where the supervisor files its certificate report. It
// is satisfied by *runstate.ReportStore.
type forgeTrustReports interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
	Append(report.Report) error
}

// reportForgeTrust files one warning that this supervisor cannot verify
// GitHub's certificate, unless one is already filed and nobody has dealt with
// it. It returns the report's id, empty where it filed none.
func reportForgeTrust(reports forgeTrustReports, product domain.ProductID, repository, build string, problem error, now time.Time) (string, error) {
	filed, err := reports.List()
	if err != nil {
		return "", err
	}
	handlings, err := reports.Handlings()
	if err != nil {
		return "", err
	}
	for _, open := range report.Unhandled(filed, handlings) {
		if open.Role == report.HarnessReporter && open.RunID == forgeTrustReportRun {
			return "", nil
		}
	}
	message := fmt.Sprintf("The supervisor cannot verify GitHub's certificate: %v. "+
		"Every check of GitHub the scheduler and the other parts make fails until the product is started again from an ordinary terminal or through the launch agent `yoyo setup` installs, among them the check for pull requests merged outside the merge queue, so those changes are not closed out and the work waiting on them does not start.",
		strings.TrimSuffix(problem.Error(), "."))
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        forgeTrustReportRun,
		Build:        build,
		ProductID:    product,
		RepositoryID: repository,
	}, now)
	if err != nil {
		return "", err
	}
	if err := reports.Append(collected[0]); err != nil {
		return "", err
	}
	return collected[0].ID, nil
}
