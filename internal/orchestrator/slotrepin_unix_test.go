//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package orchestrator

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run moved to another developer slot is pinned to that slot's own
// endpoints as the configuration resolves them now: the slot's explicit pair
// where it configures one, and the developer's own endpoint where it does not,
// as slot 1 here, which is how a run kept on slot 2's pair comes to run in a
// slot with none without carrying slot 2's pair there.
func TestAMovedRunIsPinnedToTheNewSlotsOwnEndpoints(t *testing.T) {
	t.Parallel()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := routedTracker()
	claude := newRoutedProvider(domain.BackendClaudeCode, "claude-session", refuseNothing, writeRoutedFeature)
	codex := newRoutedProvider(domain.BackendCodex, "codex-session", refuseNothing, writeRoutedFeature)
	slots := routedSlots([2]*domain.EndpointSpec{nil, nil}, [2]*domain.EndpointSpec{codexSol, claudeOpus})
	pipeline := routedPipeline(t, repository, worktreeRoot, store, tracker, claude, codex, slots)
	run := runstate.State{RunID: "run-e63fbad1", WorkItemID: tracker.Item.ID}

	unpaired, err := pipeline.slotSnapshot(run, 1)
	if err != nil {
		t.Fatalf("slotSnapshot(1) error = %v", err)
	}
	if unpaired.Slot != 1 || unpaired.Explicit || unpaired.Primary.Provider != domain.BackendClaudeCode || unpaired.Origin != runstate.RoutingReconfigured || unpaired.Digest == "" {
		t.Fatalf("slot 1's endpoints = %#v, want the developer's own Claude Code endpoint, recorded as a reconfiguration", unpaired)
	}
	paired, err := pipeline.slotSnapshot(run, 2)
	if err != nil {
		t.Fatalf("slotSnapshot(2) error = %v", err)
	}
	if paired.Slot != 2 || !paired.Explicit || paired.Primary.Provider != domain.BackendCodex || paired.Alternate == nil || paired.Digest == unpaired.Digest {
		t.Fatalf("slot 2's endpoints = %#v, want its explicit Codex-then-Claude pair", paired)
	}
}
