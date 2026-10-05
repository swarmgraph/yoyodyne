// Package orchestratortest holds the orchestrator tests' fakes of other
// packages' interfaces: the tracker, the provider backend, the forge, the
// pricing ledger, and worktree managers, including the specialized readings
// and request records used by conversations, schedulers, and recovery.
//
// They live here rather than in the orchestrator's own test files so that a
// test package outside orchestrator can use them too. An in-package test
// cannot import a package that imports orchestrator, so this package must
// never import it; that is what lets the orchestrator's tests be moved out in
// groups while the ones that reach its internals stay behind
// (docs/diagnoses/yoyodyne-ifd-429-12-orchestrator-binary-under-load.md).
//
// Nothing outside tests imports it.
package orchestratortest

// DeveloperResolved and ReviewerResolved are the models the fake provider
// reports serving the developer and the reviewer, beside the selector each
// configured agent declares.
const (
	DeveloperResolved = "claude-opus-5-developer"
	ReviewerResolved  = "claude-opus-5-reviewer"
)
