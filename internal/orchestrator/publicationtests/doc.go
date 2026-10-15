// Package publicationtests holds the orchestrator's tests of publication: a
// run pushing its branch and opening a pull request, the forge merging or
// queueing it, and reconciliation settling what the forge did after the run
// that published it ended. They reach nothing unexported in package
// orchestrator, so they run as a test binary of their own rather than inside
// the package's
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// The package has no code of its own; everything here is a test.
package publicationtests
