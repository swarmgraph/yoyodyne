// Package pipelinetests holds the orchestrator's tests of one run through the
// pipeline: claiming the item, what the run records about itself, the
// developer's checks, the reviewer's verdict, and the repair loop between them.
// They reach nothing unexported in package orchestrator, so they run as a test
// binary of their own rather than inside the package's
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// The package has no code of its own; everything here is a test.
package pipelinetests
