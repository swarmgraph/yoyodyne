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
	entries, _, problem := readPassFailures(sources)
	return entries, problem
}

// PassFailureOperatorActions projects person-only remedies for direct delivery
// from the same reading that supplies the single pass-failure status entry.
// Its actions end when the affected pass succeeds, even if clearing its report
// has not yet been recorded.
func PassFailureOperatorActions(sources Sources) ([]OperatorAction, string) {
	_, actions, problem := readPassFailures(sources)
	return actions, problem
}

// PassFailureKeyPrefix lets a delivery cursor retain these findings when their
// sweep log cannot be read, without retaining unrelated findings that ended.
const PassFailureKeyPrefix = "pass-failure:"

func readPassFailures(sources Sources) ([]Attention, []OperatorAction, string) {
	if sources.Passes == nil {
		return nil, nil, ""
	}
	passes, unreadable, err := sources.Passes.List()
	if err != nil || len(unreadable) > 0 {
		return nil, nil, fmt.Sprintf("product pass failures could not be read whole: %v; %d unreadable sweep line(s)", err, len(unreadable))
	}
	var handlings []report.Handling
	if sources.Reports != nil {
		handlings, err = sources.Reports.Handlings()
		if err != nil {
			return nil, nil, fmt.Sprintf("product pass failure remedies could not be read: %v", err)
		}
	}
	handled := report.Handled(handlings)
	watcher := FactoryFlowAgent(sources.ProgramManagers)
	var entries []Attention
	var actions []OperatorAction
	for _, f := range runstate.PassFailuresOf(passes) {
		if !f.ClearedAt.IsZero() {
			continue
		}
		id := f.ReportID(f.ProductID)
		handling := handled[id]
		var remedy *ownership.PersonOnlyRemedy
		if handling.NeedsOperator {
			remedy = handling.PersonOnly
		}
		owner := ownership.ResolvePassFailureForRole(watcher, f.Role, f.Agent, remedy)
		failure := FailingTask{Task: f.Task, Failures: f.Failures, FirstAt: f.FirstAt, RaisedAt: f.RaisedAt,
			LatestAt: f.LatestAt, Problem: f.Problem, FailureOutput: f.FailureOutput, ProductPass: true, Ownership: &owner, ReportID: id}
		entries = append(entries, failingTaskAttention(failure))
		if owner.Mover == MoverOperator {
			actions = append(actions, OperatorAction{
				Key:      PassFailureKeyPrefix + id,
				Subject:  f.Task,
				ReportID: id,
				Needs:    owner.PersonStep,
				RecordedIn: fmt.Sprintf("the handling of %s recorded in %s, for the product pass %s",
					id, handling.RunID, f.Task),
				FoundBy: fmt.Sprintf("the %s, handling the report", handling.Role.Title()),
				Ends:    fmt.Sprintf("the product pass %s next succeeding clears the finding", f.Task),
				Since:   handling.RecordedAt,
			})
		}
	}
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].Since.Before(actions[j].Since) })
	return entries, actions, ""
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
		if (role != owner.Resolver || (role == domain.RoleProgramManager && agent != owner.Agent)) && (role != owner.Watcher || agent != owner.Agent) {
			continue
		}
		if rendered.Len() == 0 {
			rendered.WriteString("## Product passes failing\n\nAnswer each finding in this pass's account. The factory-flow program manager also carries it in her existing digest and lane report. The owner named below resolves the cause; only the affected pass succeeding clears it.\n\n")
		}
		fmt.Fprintf(&rendered, "- %s: %s; %s\n", entry.FailingTask.ReportID, entry.What(), runstate.PassFailureOwnersSays(*owner))
	}
	return rendered.String()
}
