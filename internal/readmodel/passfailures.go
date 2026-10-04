package readmodel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReadPassFailures projects the sweep log, with ownership from the same report
// handling that can record a person-only remedy. It never closes a finding
// because its watcher completed a pass or somebody handled its report.
func ReadPassFailures(sources Sources) ([]Attention, string) {
	if sources.Passes == nil {
		return nil, ""
	}
	passes, unreadable, err := sources.Passes.List()
	if err != nil || len(unreadable) > 0 {
		return nil, fmt.Sprintf("product pass failures could not be read whole: %v; %d unreadable sweep line(s)", err, len(unreadable))
	}
	var handlings []report.Handling
	if sources.Reports != nil {
		handlings, err = sources.Reports.Handlings()
		if err != nil {
			return nil, fmt.Sprintf("product pass failure remedies could not be read: %v", err)
		}
	}
	handled := report.Handled(handlings)
	watcher := FactoryFlowAgent(sources.ProgramManagers)
	var entries []Attention
	for _, f := range runstate.PassFailuresOf(passes) {
		if !f.ClearedAt.IsZero() {
			continue
		}
		id := f.ReportID(f.ProductID)
		var remedy *ownership.PersonOnlyRemedy
		if h := handled[id]; h.NeedsOperator {
			remedy = h.PersonOnly
		}
		owner := ownership.ResolvePassFailure(watcher, remedy)
		failure := FailingTask{Task: f.Task, Failures: f.Failures, FirstAt: f.FirstAt, RaisedAt: f.RaisedAt,
			LatestAt: f.LatestAt, Problem: f.Problem, ProductPass: true, Ownership: &owner, ReportID: id}
		entries = append(entries, failingTaskAttention(failure))
	}
	return entries, ""
}

func FactoryFlowAgent(instances []ProgramManagerInstance) string {
	var agents []string
	for _, instance := range instances {
		if instance.Lane == "factory-flow" {
			agents = append(agents, instance.Agent)
		}
	}
	sort.Strings(agents)
	if len(agents) == 0 {
		return ""
	}
	return agents[0]
}

// RenderPassFailures is carried into the watcher's existing pass and digest,
// and into the development manager's pass that resolves the cause.
func RenderPassFailures(sources Sources, role domain.AgentRole, agent string) string {
	entries, problem := ReadPassFailures(sources)
	if problem != "" {
		return "## Product passes failing\n\n" + problem
	}
	var rendered strings.Builder
	for _, entry := range entries {
		owner := entry.FailingTask.Ownership
		if role != domain.RoleDevelopmentManager && (role != owner.Watcher || agent != owner.Agent) {
			continue
		}
		if rendered.Len() == 0 {
			rendered.WriteString("## Product passes failing\n\nAnswer each finding in this pass's account. The factory-flow program manager also carries it in her existing digest and lane report. The development manager resolves the cause; only the affected pass succeeding clears it.\n\n")
		}
		fmt.Fprintf(&rendered, "- %s: %s; %s\n", entry.FailingTask.ReportID, entry.What(), runstate.PassFailureOwnersSays(*owner))
	}
	return rendered.String()
}
