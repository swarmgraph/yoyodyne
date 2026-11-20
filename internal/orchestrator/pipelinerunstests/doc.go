// Package pipelinerunstests holds the orchestrator's tests of what becomes of a
// run when something outside the change interferes: a failed run's preserved
// work and its cleanup, the models and sessions a run records, the relaunch
// budget, a provider that is overloaded, limited, or stopped for a redeploy,
// and a promotion whose target moved so the change is replayed.
// They reach nothing unexported in package orchestrator, so they run as a test
// binary of their own rather than inside the package's
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// The package has no code of its own; everything here is a test.
package pipelinerunstests
