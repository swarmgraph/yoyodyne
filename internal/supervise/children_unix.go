//go:build darwin || linux

package supervise

// process is one row of the process table: a process, its parent, and the
// group it is in.
type process struct {
	pid, parent, group int
}
