// Package recoverytests holds the orchestrator's tests of reconciling a run
// with what the target branch and the run's own records hold, and of
// recovering a stopped run: a run a killed process left behind, a stalled run a
// repair continues, an approved change resumed at its promotion after the
// environment stopped it, a forge or tracker call retried across a dropped
// connection or a restart, a provider death carried past its relaunch budget,
// and the settled checkouts and branches the sweep retires.
// They reach nothing unexported in package orchestrator, so they run as a test
// binary of their own rather than inside the package's
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// The package has no code of its own; everything here is a test.
package recoverytests
