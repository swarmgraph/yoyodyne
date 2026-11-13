package orchestrator

import "github.com/mason-bryant/yoyodyne/internal/runstate"

// resumeShapes is every shape of in-flight run Pipeline.Run takes up again
// when it adopts one, each a predicate on the run's record alone. A run that
// matches none is reported as already existing rather than resumed.
//
// A new way for a run to pause or stop and be continued later is added here,
// and nowhere else in Pipeline.Run. TestEveryPauseMarkerIsConsideredByResumable
// fails while a pause marker on runstate.State, or a pausedFor… predicate in
// this package, is neither listed here nor named as deliberately taken up
// elsewhere, and TestResumableTakesUpEveryShape fails while a shape listed here
// has no run of its own that the test resumes.
var resumeShapes = []func(runstate.State) bool{
	// The first five are resumable whatever the approval policy, because none
	// of them depends on the repair loop: a usage-limit pause has not had its
	// attempt served yet, and the rest are owed the rest of the step they
	// stopped short of. Nothing reaches the adoption while the hold or the
	// dependency is still in force, so a run carrying one is a run whose reason
	// to wait has gone.
	pausedForUsageLimit,
	pausedForDirective,
	pausedForDependency,
	pausedForTracker,
	pausedForOperatorHold,
	// A provider the harness stopped on time is owed the rest of the attempt
	// it was making, and a run its hosting session stopped for a redeploy is
	// owed what it was doing when the drain stopped it.
	stoppedProviderIsResumable,
	stoppedForRedeployIsResumable,
	// A run put back at its checks after the check stage's time limit, and a
	// run inside its repair loop, are continued at the step their record names.
	continuedAtCheckStage,
	resumableRepair,
	// A run the development manager asked to have continued through
	// run.recover is resumable on the same terms that request was accepted on
	// (recoveryrequest.go).
	recoveryContinuationDue,
	// A run brought back from its promotion to update its head, whose hosting
	// process ended without stopping it (queuedchecks.go).
	updateLostItsHost,
}

// resumable reports whether Pipeline.Run may take up this in-flight run from
// its record: whether it matches one of resumeShapes.
func resumable(state runstate.State) bool {
	for _, shape := range resumeShapes {
		if shape(state) {
			return true
		}
	}
	return false
}
