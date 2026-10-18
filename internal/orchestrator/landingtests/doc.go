// Package landingtests holds the orchestrator's tests of landing a change on
// the target branch: the paths a change may not touch, a protected target
// reached through its pull request, a target whose checks are red, the checks
// a landing runs, the line a stopped landing leaves, and what becomes of the
// work item once the change lands or does not.
// They reach nothing unexported in package orchestrator, so they run as a test
// binary of their own rather than inside the package's
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// The package has no code of its own; everything here is a test.
package landingtests
