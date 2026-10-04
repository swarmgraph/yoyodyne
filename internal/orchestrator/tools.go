package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/toolcatalog"
)

func (a *activeRun) emitTool(kind execution.EventType, audit execution.ToolAudit) error {
	if a.pipeline.Store == nil {
		return errors.New("no run store is available for tool auditing")
	}
	event, err := execution.NewEvent(a.state.RunID, a.state.LastSequence+1, a.pipeline.clock().Now(), kind, "harness", audit)
	if err != nil {
		return err
	}
	return a.sink(event)
}

func (a *activeRun) runTool(role domain.AgentRole, id capability.Capability, count int, perform func(), problem func() bool) error {
	registered, found := toolcatalog.Registry().Lookup(string(id))
	if !found {
		return fmt.Errorf("no registered tool describes %s", id)
	}
	audit := execution.ToolAudit{ID: fmt.Sprintf("tool-%d", a.state.LastSequence+1), Tool: id, Role: role, Requests: count, Bounds: registered.Tool.Bounds}
	if err := a.emitTool(execution.EventToolRequested, audit); err != nil {
		return fmt.Errorf("tool request audit failed; nothing was recorded: %w", err)
	}
	allowed := false
	for _, held := range toolcatalog.Granted(role) {
		allowed = allowed || held.Name == registered.Name
	}
	var refused error
	if !allowed {
		refused = errors.New("the role holds no grant for this tool")
	} else if count > audit.Bounds.RequestsPerReply {
		refused = errors.New("tool request count exceeds its descriptor bound")
	} else {
		refused = registered.Perform(context.Background(), toolcatalog.Call{Perform: func(context.Context) error { perform(); return nil }})
	}
	event := execution.EventToolPerformed
	if refused != nil || problem() {
		event = execution.EventToolRefused
	}
	if err := a.emitTool(event, audit); err != nil {
		return fmt.Errorf("tool outcome could not be audited: %w", err)
	}
	return refused
}

func (a *activeRun) collectReports(role domain.AgentRole, entries []report.Entry) {
	if len(entries) == 0 {
		return
	}
	before := a.outcome.ReportProblem
	if err := a.runTool(role, capability.ReportFile, len(entries), func() { a.collectReportsWithoutToolAudit(role, entries) }, func() bool { return a.outcome.ReportProblem != before }); err != nil {
		a.noteReportProblem(role, err)
	}
}

func (a *activeRun) auditUnreadableTool(role domain.AgentRole, id capability.Capability) {
	if err := a.runTool(role, id, 1, func() {}, func() bool { return true }); err != nil {
		if id == capability.ReportFile {
			a.noteReportProblem(role, err)
		} else {
			a.noteAmendmentProblem(role, err)
		}
	}
}

func (a *activeRun) collectAmendments(role domain.AgentRole, entries []amendment.Entry) {
	if len(entries) == 0 {
		return
	}
	before := a.outcome.AmendmentProblem
	if err := a.runTool(role, capability.AmendmentPropose, len(entries), func() { a.collectAmendmentsWithoutToolAudit(role, entries) }, func() bool { return a.outcome.AmendmentProblem != before }); err != nil {
		a.noteAmendmentProblem(role, err)
	}
}
