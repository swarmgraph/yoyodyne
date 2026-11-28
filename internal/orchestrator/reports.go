package orchestrator

// What an agent noticed while its own work succeeded reaches the operator only
// through here. Nothing in this file may change what a run did: a report is
// collected beside the run rather than on it, and a report the harness cannot
// read or cannot store is recorded as that on the outcome rather than failing
// the attempt it arrived with.

import (
	"fmt"
	"slices"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/workref"
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
		References:   findReferences(a.pipeline.ReferenceFinder),
	}, a.pipeline.clock().Now())
	if err != nil {
		a.noteReportProblem(role, err)
		return
	}
	for index, reported := range collected {
		filed, err := fileReport(a.pipeline.Reports, reported, entries[index].Repeats, a.state.StandingReportsOffered)
		if err != nil {
			a.noteReportProblem(role, err)
			continue
		}
		if filed.Repeat != nil {
			a.outcome.Repeats = append(a.outcome.Repeats, report.Counted{Repeat: *filed.Repeat, Count: filed.Count})
			continue
		}
		if filed.NotUsed != "" {
			note := fmt.Sprintf("report %s was filed as a new report because %s", reported.ID, filed.NotUsed)
			if a.outcome.ReportNotes != "" {
				note = a.outcome.ReportNotes + "; " + note
			}
			a.outcome.ReportNotes = note
		}
		a.outcome.Reports = append(a.outcome.Reports, reported)
	}
}

// RepeatFiler is a collector that can keep a report as a repeat of a standing
// one. It is satisfied by runstate.ReportStore; a collector that is not one
// keeps every report as a new one.
type RepeatFiler interface {
	File(reported report.Report, named string, offered []string) (runstate.Filed, error)
}

// fileReport keeps one report a run's agent filed, as a repeat where the
// collector can keep one and the report repeats a standing one.
func fileReport(collector ReportCollector, reported report.Report, named string, offered []string) (runstate.Filed, error) {
	if filer, ok := collector.(RepeatFiler); ok {
		return filer.File(reported, named, offered)
	}
	return runstate.Filed{Report: reported}, collector.Append(reported)
}

// StandingReports is a collector that can say what stands in the project, for
// the list a developer or reviewer is handed at its start.
type StandingReports interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
	Repeats() ([]report.Repeat, error)
}

// standingReportsSection is the list of standing reports handed to the
// developer or the reviewer at its start, recording on the run which reports
// were on it, since those are the only ones its reports may name. It is empty
// where nothing stands or nothing could be read: a run is never held up for
// the list.
func (a *activeRun) standingReportsSection() string {
	pile, ok := a.pipeline.Reports.(StandingReports)
	if !ok {
		return ""
	}
	offered := offerStanding(pile)
	for _, id := range offered.IDs {
		if len(a.state.StandingReportsOffered) < runstate.MaxStandingReportsOffered && !slices.Contains(a.state.StandingReportsOffered, id) {
			a.state.StandingReportsOffered = append(a.state.StandingReportsOffered, id)
		}
	}
	if offered.Text == "" {
		return ""
	}
	return offered.Text + "\n"
}

func offerStanding(pile StandingReports) report.Offered {
	reports, err := pile.List()
	if err != nil {
		return report.Offered{}
	}
	handlings, err := pile.Handlings()
	if err != nil {
		return report.Offered{}
	}
	repeats, err := pile.Repeats()
	if err != nil {
		return report.Offered{}
	}
	return report.OfferStanding(reports, handlings, repeats)
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

// findReferences is what finds the work items a report's message names as it
// is collected, from one listing of the tracker, or nil where nothing was wired
// to list it or it could not be listed.
func findReferences(finder func() func(text string) []workref.Reference) func(text string) []workref.Reference {
	if finder == nil {
		return nil
	}
	return finder()
}
