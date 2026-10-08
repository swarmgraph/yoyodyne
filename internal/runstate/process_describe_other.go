//go:build dragonfly || freebsd || illumos || netbsd || openbsd

package runstate

import "time"

// describeProcess says nothing more than that the process exists on the Unix
// hosts with no reading here of their process tables, so a look there is the
// null signal's answer alone.
func describeProcess(int) (exited bool, started time.Time, known bool) {
	return false, time.Time{}, false
}
