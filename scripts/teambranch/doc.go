// Package teambranch is the validation for integrating to a local branch: one
// project with a team's branch set, taken from the start of an item to its
// closing against a scratch Git repository, with every step the design's piece 6
// names asserted in order (docs/designs/integrating-to-a-local-branch.md, "What
// a reviewer can check"), except the development manager's decision to repair
// an item whose run has ended, which the harness does not have yet and which is
// reported as not yet built.
//
// It is run by validate.sh beside it, which makes the temporary root the
// scratch repository and the run state live in and removes it afterwards. Run
// any other way, its test skips, so `make test` compiles it and keeps it
// building without running it.
//
// The package holds nothing but its test.
package teambranch
