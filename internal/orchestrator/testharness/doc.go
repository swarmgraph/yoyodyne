// Package testharness holds the test helpers shared by the orchestrator's tests
// that have moved out of package orchestrator into packages of their own: the
// pipelines they build, the repositories and verdicts they drive them with, and
// the small stand-ins for the stores a run reads.
//
// It imports orchestrator, which orchestratortest must never do, so the
// package's own tests cannot import it. A helper that tests on both sides still
// use is therefore kept twice — unexported in the package's test files, and
// exported here — until the last test using the first copy moves out. Each file
// is named after the test file its helpers came from, which is where the other
// copy lives while there is one.
//
// Its files are not test files, so the checks that sweep the harness's own
// code read them, where the copies in the package's test files were never
// read: the terms check in internal/terms reads their string literals, and a
// failure message naming a replaced term is reworded on the way here; the
// authority sweep in internal/authority reads their names, and a helper that
// looks like an authorization site is excused in docs/authority-inventory.md.
//
// scripts/movetests moves a group of tests and writes what they need here;
// docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md says why
// the tests are moving. Nothing outside tests imports it.
package testharness
