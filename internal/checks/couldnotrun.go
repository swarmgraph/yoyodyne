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
// stopped on time, the line with no reason after it is not the convention, and
// a check whose output also names a failing test or package (the lines
// failureCapture reads names from) ran something that failed — so each of
// those is judged as it always was, and the last is a failure.
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

// machineRefusals are the lines the machine prints when it refuses to start the
// tool a check drives at all, before the check has looked at anything. Each is
// read as the check saying it could not run, because that is what it is: the
// command never reached the project, and the same refusal meets every check and
// every change until somebody clears it on the machine. They are the messages
// macOS's developer tools print in front of make, cc, git and the rest while
// their licence is unaccepted or they are not installed — make then exits 69
// with the same line on every target, whatever the project builds with.
//
// The list is closed and matched by what each line begins with, so a refusal
// is recognised only where it is certain; a message nothing here names is judged
// as the failure it looks like, which is how every check was judged before.
var machineRefusals = []string{
	"You have not agreed to the Xcode license agreements",
	"Agreeing to the Xcode/iOS license requires admin privileges",
	"xcrun: error: invalid active developer path",
}

// machineRefusalReason is the reason a line of check output gives where the
// line is one the machine prints refusing to start the check's tool.
func machineRefusalReason(line string) (string, bool) {
	line = strings.TrimSpace(line)
	for _, refusal := range machineRefusals {
		if strings.HasPrefix(line, refusal) {
			if len(line) > maxCouldNotRunReason {
				line = strings.ToValidUTF8(line[:maxCouldNotRunReason], "") + "…"
			}
			return "the machine refused to start it: " + line, true
		}
	}
	return "", false
}
