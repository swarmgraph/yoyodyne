package orchestratortest_test

import (
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// Each fake answers for the interface the orchestrator asks of it. This is a
// test package of its own, so importing orchestrator here puts nothing into
// orchestratortest itself.
var (
	_ orchestrator.WorkTracker           = (*orchestratortest.Tracker)(nil)
	_ orchestrator.PullRequests          = (*orchestratortest.Forge)(nil)
	_ orchestrator.Pricer                = (*orchestratortest.Pricer)(nil)
	_ orchestrator.WorktreeManager       = orchestratortest.PartialWorktreeManager{}
	_ chat.Backend                       = (*orchestratortest.ReplayBackend)(nil)
	_ chat.Tracker                       = (*orchestratortest.ParkingTracker)(nil)
	_ orchestrator.WorkTracker           = (*orchestratortest.PipelineTracker)(nil)
	_ orchestrator.RearmForge            = (*orchestratortest.RearmForge)(nil)
	_ orchestrator.ResumeWorktrees       = (*orchestratortest.ResumeOwnership)(nil)
	_ orchestrator.RestorableWorktrees   = (*orchestratortest.RecoveryCheckout)(nil)
	_ orchestrator.PullRequests          = (*orchestratortest.CheckedForge)(nil)
	_ orchestrator.ReconcilePullRequests = orchestratortest.PublicationAnswers{}
	_ orchestrator.ReconcilePullRequests = (*orchestratortest.AnsweringForge)(nil)
	_ readmodel.Remains                  = (*orchestratortest.Survival)(nil)
)
