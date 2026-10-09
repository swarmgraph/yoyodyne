//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package runstate

import "os"

// duplicateHold hands no copy on a platform where a launch cannot be gated, so
// the stage's processes are started the ordinary way.
func duplicateHold(*os.File) (*os.File, error) { return nil, nil }

// groupAlive establishes nothing on a platform with no process groups to look
// in, so a recorded process is never read as stopped.
func groupAlive(int) (bool, string) {
	return true, "this platform cannot establish whether a process has stopped"
}
