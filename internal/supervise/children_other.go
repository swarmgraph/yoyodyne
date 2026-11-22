//go:build !(darwin || linux)

package supervise

import "errors"

// process is one row of the process table: a process, its parent, and the
// group it is in.
type process struct {
	pid, parent, group int
}

// childrenOf is implemented for the hosts Yoyodyne's supervisor runs on.
func childrenOf(int) ([]process, error) {
	return nil, errors.New("listing the processes a process started is not supported on this platform")
}
