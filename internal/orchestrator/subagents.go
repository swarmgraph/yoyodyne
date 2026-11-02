package orchestrator

// Sub-agents on a developer run (docs/designs/developer-sub-agents.md).
//
// The setting a run uses is settled on its record before its first launch,
// from its developer slot and its agent, and every invocation after reads it
// back, so a configuration reload reaches only runs that start later. Whether
// an invocation gets them is then a question about the endpoint serving it:
// one whose provider does not offer them serves the run without them, and the
// record says why in words a person reads. Falling back to an alternate never
// depends on them; it takes the setting with it and the same question is
// asked of the alternate.
//
// A usage limit the provider reports on the sub-agents' model alone — a
// weekly Sonnet limit on a run whose developer is on Opus — does not stop the
// invocation, and does not move the run to its alternate: the adapter records
// it apart from the invocation's own limit (backend.SubagentsOnlyLimit), the
// developer carries on itself, and the run goes without sub-agents until the
// limit resets, its record saying why.

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// settleSubagents records the run's sub-agent setting where it has none yet.
//
// A run whose configuration says nothing about sub-agents records nothing,
// which is the record every run carried before they existed. A run that has
// already made a developer invocation without a setting on its record keeps
// running without them, so turning them on mid-run reaches the next run
// rather than this one.
//
// A run moved to another developer slot when it was taken up again (runstate's
// RunRouting.MoveSlot) settles its setting afresh from the slot it now records,
// whatever it settled before, because the setting is the slot's and the run is
// no longer in the old one.
func (a *activeRun) settleSubagents() {
	if !a.movedSinceSubagentsSettled() && (a.state.Subagents != nil || a.state.LastSequence > 0 || a.state.ProviderSessionID != "") {
		return
	}
	a.state.Subagents = nil
	p := a.pipeline
	setting := p.Config.DeveloperSubagents(p.developerName(), a.state.RecordedSlot())
	stated := p.Config.Agents[p.developerName()].Subagents.Stated()
	if slot := a.state.RecordedSlot(); slot >= 1 && slot <= len(p.Config.Execution.DeveloperSlots) {
		stated = stated || p.Config.Execution.DeveloperSlots[slot-1].Subagents != nil
	}
	if !stated {
		return
	}
	a.state.Subagents = &runstate.SubagentRecord{
		Enabled: setting.Enabled,
		Model:   setting.Model,
		Effort:  setting.Effort,
		Origin:  setting.Origin,
		Slot:    a.state.RecordedSlot(),
	}
}

// movedSinceSubagentsSettled reports a run moved to another developer slot
// whose sub-agent setting was not settled for the slot it records now.
func (a *activeRun) movedSinceSubagentsSettled() bool {
	routing := a.state.Routing
	if routing == nil || routing.Slot == nil || routing.Slot.MovedFrom == 0 {
		return false
	}
	return a.state.Subagents == nil || a.state.Subagents.Slot != routing.Slot.Number
}

// subagentsFor is what one developer invocation on the named provider is
// allowed: sub-agents on the recorded model and effort where the setting is
// on, the provider offers them, and the installed provider confirms it applies
// what they are launched with; none otherwise. Where the setting is on and the
// run goes without them, the record says why.
//
// The confirmation is asked before every invocation that would have them,
// because the provider can be upgraded under a running harness, and it is
// what keeps a later version that no longer contains or shows them from being
// given them unremarked. An adapter that cannot be asked gives none: what an
// adapter says it offers is not relied on alone.
func (a *activeRun) subagentsFor(ctx context.Context, adapter backend.Backend, named domain.Backend) *backend.SubagentRequest {
	record := a.state.Subagents
	if record == nil || !record.Enabled {
		return nil
	}
	request := &backend.SubagentRequest{Model: record.Model, Effort: record.Effort}
	if record.Limit.Stands(a.pipeline.clock().Now()) {
		record.Unavailable = record.Limit.Says()
		return nil
	}
	record.Limit = nil
	record.Unavailable = a.pipeline.subagentsUnavailable(named)
	if record.Unavailable == "" {
		record.Unavailable = a.subagentsUnconfirmed(ctx, adapter, named, *request)
	}
	if record.Unavailable != "" {
		return nil
	}
	return request
}

// subagentsUnconfirmed says why the installed provider did not confirm it
// applies what sub-agents are launched with, and nothing where it did.
func (a *activeRun) subagentsUnconfirmed(ctx context.Context, adapter backend.Backend, named domain.Backend, request backend.SubagentRequest) string {
	checker, checks := adapter.(backend.SubagentLaunchChecker)
	if !checks {
		return fmt.Sprintf("nothing in this build can confirm that the installed %s applies what sub-agents are launched with", named)
	}
	directory := a.pipeline.Repository
	if a.worktree.Path != "" {
		directory = a.worktree.Path
	}
	check, err := checker.CheckSubagentLaunch(ctx, directory, a.state.RunID, request)
	switch {
	case err != nil:
		return fmt.Sprintf("whether the installed %s applies what sub-agents are launched with could not be checked: %v", named, err)
	case !check.InForce():
		return fmt.Sprintf("the installed %s (%s) did not confirm what sub-agents are launched with: %s", named, check.Version, check.Says())
	}
	return ""
}

// subagentsUnavailable says why a developer invocation on the provider cannot
// be given sub-agents, and nothing where it can.
func (p Pipeline) subagentsUnavailable(named domain.Backend) string {
	if p.developer().Posture() == backend.PostureReadOnly {
		return "the developer agent is held to read-only tool access, so it has nothing to hand a sub-agent"
	}
	providers, err := p.Config.ProviderRegistry()
	if err != nil {
		return fmt.Sprintf("the configured providers could not be read: %v", err)
	}
	descriptor, known := providers.Lookup(named)
	if !known {
		return fmt.Sprintf("%s is not a provider this project has", named)
	}
	return descriptor.SubagentsUnavailable()
}

// noteSubagentLimit records a usage limit the provider reported reached on the
// sub-agents' model alone, so the run's later invocations go without them
// until it resets and its record says why. The invocation that met it carried
// on: the developer did the work itself.
func (a *activeRun) noteSubagentLimit(result backend.RunResult) {
	record := a.state.Subagents
	if result.SubagentLimit == nil || record == nil || !record.Enabled {
		return
	}
	limit := &runstate.SubagentLimit{Kind: result.SubagentLimit.Kind, At: a.pipeline.clock().Now()}
	if resets := result.SubagentLimit.ResetsAt; !resets.IsZero() {
		limit.ResetsAt = &resets
	}
	record.Limit = limit
	record.Unavailable = limit.Says()
}
