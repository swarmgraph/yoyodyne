package orchestrator

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// addHarnessFindings adds findings the harness made itself to a pass's
// account, after the role's turns, as noticeForge adds the forge's notices: the
// role's own account is left intact, a pass that gave none is given the
// harness's, and what does not fit the account's bound is said in the problem
// rather than dropped silently. Each pass reads afresh, so a finding left out
// is found again on the next pass while its cause stands.
func addHarnessFindings(recorded *runstate.Sweep, role domain.AgentRole, findings []sweep.Finding, what string) string {
	if len(findings) == 0 {
		return ""
	}
	if recorded.Result == nil {
		recorded.Result = &sweep.Result{
			Status:  sweep.StatusComplete,
			Summary: fmt.Sprintf("No account of this pass came from the %s; the findings are %s.", role.Title(), what),
		}
	}
	room := sweep.MaxPassFindings - len(recorded.Result.Findings)
	if room < 0 {
		room = 0
	}
	kept := findings
	problem := ""
	if len(kept) > room {
		kept = kept[:room]
		problem = fmt.Sprintf("%d of %s are not listed because the pass's account is at its bound of %d findings; the next pass finds them again while they stand",
			len(findings)-room, what, sweep.MaxPassFindings)
	}
	recorded.Result.Findings = append(recorded.Result.Findings, kept...)
	return problem
}
