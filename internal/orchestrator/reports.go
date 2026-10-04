package orchestrator

// What an agent noticed while its own work succeeded reaches the operator only
// through here. Nothing in this file may change what a run did: a report is
// collected beside the run rather than on it, and a report the harness cannot
// read or cannot store is recorded as that on the outcome rather than failing
// the attempt it arrived with.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReportCollector is where collected reports are kept. It is satisfied by
// runstate.ReportStore.
type ReportCollector interface {
	Append(reported report.Report) error
}

// collectFromReply takes what an agent reported and what it proposed out of what
// it said, records both, and returns the rest — which is the agent's own account,
// without the blocks the operator was never meant to read as prose. A block that
// could not be read leaves the text exactly as it arrived, because the summary is
// the run's evidence and neither channel must cost it.
//
// Reports are taken first and the proposals out of what is left, so a reply
// carrying both loses neither, and an unreadable report block still has the
// proposals in front of it read rather than swallowed with it.
func (a *activeRun) collectFromReply(role domain.AgentRole, text string) string {
	rest, entries, err := report.Extract(text)
	if err != nil {
		a.auditUnreadableTool(role, capability.ReportFile)
		a.noteReportProblem(role, err)
	} else {
		a.collectReports(role, entries)
	}
	rest, proposed, err := amendment.Extract(rest)
	if err != nil {
		a.auditUnreadableTool(role, capability.AmendmentPropose)
		a.noteAmendmentProblem(role, err)
		return rest
	}
	a.collectAmendments(role, proposed)
	return rest
}

// collectReports records what one agent invocation reported. Every failure here
// is noted and swallowed: a run that failed because an agent mentioned a risk
// would teach every agent to stop mentioning them.
func (a *activeRun) collectReportsWithoutToolAudit(role domain.AgentRole, entries []report.Entry) {
	if len(entries) == 0 {
		return
	}
	// A pipeline with nowhere to collect loses all of them at once, and says so
	// once: one note per lost report would describe the same missing store
	// several times over.
	if a.pipeline.Reports == nil {
		a.noteReportProblem(role, fmt.Errorf("nothing collects reports for this run, so the %d thing(s) the %s reported were not kept", len(entries), role))
		return
	}
	collected, err := report.Collect(entries, report.Attribution{
		Role:       role,
		Agent:      a.pipeline.agentNameForRole(role),
		RunID:      a.state.RunID,
		WorkItemID: a.state.WorkItemID,
		// The build the run's record pins rather than this process's own: a run
		// resumed or repaired by a later binary was still reserved, and its
		// decisions made, by the one it records.
		Build:        a.state.Build,
		ProductID:    a.pipeline.Config.Product.ID,
		RepositoryID: string(a.pipeline.Config.Product.RepositoryID),
	}, a.pipeline.clock().Now())
	if err != nil {
		a.noteReportProblem(role, err)
		return
	}
	for _, reported := range collected {
		if err := a.pipeline.Reports.Append(reported); err != nil {
			a.noteReportProblem(role, err)
			continue
		}
		a.outcome.Reports = append(a.outcome.Reports, reported)
	}
}

// maxReportProblemBytes keeps one lost report to a readable line of the outcome.
const maxReportProblemBytes = 512

// noteReportProblem records a report that did not reach the collected pile. It
// accumulates rather than replaces, because losing the first report and then
// losing a second is two facts.
//
// It is written to the run's state as well as to its outcome. The outcome is
// what `yoyo run` prints and is gone with the process; the record is what
// anybody reads afterwards, and a report lost only on the outcome was, after the
// fact, a report nobody ever filed.
func (a *activeRun) noteReportProblem(role domain.AgentRole, cause error) {
	problem := fmt.Sprintf("a %s report was not collected: %s", role, singleLine(cause.Error(), maxReportProblemBytes))
	if a.outcome.ReportProblem == "" {
		a.outcome.ReportProblem = problem
	} else {
		a.outcome.ReportProblem += "; " + problem
	}
	a.state.ReportProblem = runstate.RecordChannelProblem(a.outcome.ReportProblem)
}

// agentNameForRole names the configured agent that fills a role, so a report
// says which agent made it and not only which contract it was working under.
func (p Pipeline) agentNameForRole(role domain.AgentRole) string {
	for _, name := range p.agentNames() {
		if p.Config.Agents[name].Role == role {
			return name
		}
	}
	return ""
}
