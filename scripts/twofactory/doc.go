// Package twofactory is the two-factory validation: two harness homes on one
// machine working one throwaway remote, with every check under "What a reviewer
// can check" in docs/designs/working-alongside-other-contributors.md that the
// validation work item names asserted one by one.
//
// It is run by scripts/validate-two-factories.sh, which makes the temporary
// root the homes and the remote live in, points HOME there, and removes it
// afterwards. Run any other way, its test skips, so `make test` compiles it and
// keeps it building without running it.
//
// The package holds nothing but its tests.
package twofactory
