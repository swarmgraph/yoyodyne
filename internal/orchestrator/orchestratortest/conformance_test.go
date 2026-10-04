package orchestratortest_test

import (
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
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
	_ orchestrator.ScheduleProvider      = (*orchestratortest.LoginProbe)(nil)
	_ orchestrator.LandingTracker        = (*orchestratortest.LandingTracker)(nil)
	_ orchestrator.WorkFiler             = (*orchestratortest.RecordingFiler)(nil)
	_ orchestrator.RerunItems            = orchestratortest.OpenWorkItem("")
	_ orchestrator.ClaimTracker          = (*orchestratortest.ClaimState)(nil)
	_ orchestrator.ClaimRuns             = (*orchestratortest.ClaimState)(nil)
	_ orchestrator.ClaimReleases         = (*orchestratortest.ClaimState)(nil)
	_ orchestrator.ScheduleTracker       = (*orchestratortest.ScheduleTracker)(nil)
	_ orchestrator.WorkTracker           = (*orchestratortest.PipelineTracker)(nil)
	_ orchestrator.RerunItems            = (*orchestratortest.RerunServices)(nil)
	_ orchestrator.PreservedRetirer      = (*orchestratortest.RerunServices)(nil)
	_ orchestrator.ReconcilePullRequests = (*orchestratortest.AnsweringForge)(nil)
	_ orchestrator.PublicationStates     = (*orchestratortest.BatchingForge)(nil)
	_ orchestrator.RearmForge            = (*orchestratortest.RearmForge)(nil)
	_ orchestrator.RecurringForge        = (*orchestratortest.NoticingForge)(nil)
	_ orchestrator.RearmChecks           = (*orchestratortest.RequestChecks)(nil)
	_ orchestrator.ReconcileJobLogs      = (*orchestratortest.JobLogs)(nil)
	_ orchestrator.ReconcileJobLogs      = (*orchestratortest.RefusingJobLogs)(nil)
	_ orchestrator.ReconcileTargetChecks = (*orchestratortest.TargetChecks)(nil)
	_ orchestrator.RearmWorktrees        = (*orchestratortest.RemoteTarget)(nil)
	_ orchestrator.RepairWorktrees       = (*orchestratortest.Ownership)(nil)
	_ orchestrator.ResumeWorktrees       = (*orchestratortest.ResumeOwnership)(nil)
	_ orchestrator.RestorableWorktrees   = (*orchestratortest.RecoveryCheckout)(nil)
)
