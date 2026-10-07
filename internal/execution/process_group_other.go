//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package execution

import "os/exec"

// gateSupported is false where a launch cannot be held back and its tree
// cannot be observed afterwards, so a gated launch is refused rather than run
// ungated.
const gateSupported = false

// Process-group cancellation is implemented for Yoyodyne's supported Unix
// hosts. Other platforms retain os/exec's immediate-process cancellation.
func configureProcessTree(_ *exec.Cmd) {}

// Reaping what a command left running is process-group work, so there is
// nothing to do where no group was made. A descendant on such a host outlives
// the command that spawned it, bounded only by whatever timeout that descendant
// carries.
func reapProcessTree(_ *exec.Cmd) {}

// processGroupOf is zero where no group was made.
func processGroupOf(_ *exec.Cmd) int { return 0 }
