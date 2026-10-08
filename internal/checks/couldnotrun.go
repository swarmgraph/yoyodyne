package checks

import "strings"

// CouldNotRunPrefix opens the line a project's own check prints to say it could
// not run at all — the tool it drives is not installed, or the service it talks
// to cannot be reached from where the harness runs it — rather than that it ran
// and found something wrong. The rest of the line is the reason. A check says it
// by printing the line, on either stream, and exiting non-zero:
//
//	echo "yoyo check could not run: codex is not installed on this machine" >&2
//	exit 1
//
// It is a line of output rather than a reserved exit status because a check is
// usually a make target, and make replaces whatever status its recipe exited
// with by its own: the line is the one thing that reaches the harness through
// every wrapper a project puts around its checks.
//
// Such a check judged nothing about the change, so it is neither a pass nor a
// failure: it spends no repair attempt and stops nothing, the checks after it
// still run, and the run records it as could not run, naming the reason. A check
// that exits zero passed whatever it printed, a check stopped on time was
// stopped on time, and the line with no reason after it is not the convention,
// so each of those is judged as it always was.
const CouldNotRunPrefix = "yoyo check could not run:"

// maxCouldNotRunReason bounds the reason one check's line carries onto the
// run's record and every surface that shows it: a sentence, not a log.
const maxCouldNotRunReason = 500

// couldNotRunReason is the reason a line of check output gives, and whether the
// line is one. Leading space is ignored so an indented line still counts; a
// line whose reason is empty is not one.
func couldNotRunReason(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, CouldNotRunPrefix) {
		return "", false
	}
	reason := strings.TrimSpace(strings.TrimPrefix(line, CouldNotRunPrefix))
	if reason == "" {
		return "", false
	}
	if len(reason) > maxCouldNotRunReason {
		reason = strings.ToValidUTF8(reason[:maxCouldNotRunReason], "") + "…"
	}
	return reason, true
}
