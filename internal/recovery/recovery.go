// Package recovery says which failures the harness waits out and asks again
// rather than records as terminal, and how long it waits between attempts.
//
// The rule it implements is the operator's, stated on 2026-09-03: the harness
// never fails outright on anything that can recover. What produced it is four
// runs killed in one day, each at its final publish or integrate step, each by a
// single connection reset the next attempt would have survived — completed and
// sometimes reviewed work recorded as failed, with the intake brake then holding
// the whole line because three runs had blocked in a row.
//
// Two things are deliberately narrow here. The failure classes are the ones
// nobody has to argue about — a reset connection, a network that went away, a
// transport that refused before anything read the request — because the full
// recoverable-versus-terminal taxonomy is the architect's and this does not wait
// on it. And there is no configuration: the intervals are the harness's and the
// same for every product, exactly as the tracker-read retry the scheduler takes
// is, because what they measure is how long a connection that comes back takes
// rather than anything about a project.
package recovery

import (
	"errors"
	"io"
	"strings"
	"syscall"
	"time"
)

const (
	// MaxInterval caps the wait between two attempts. Past it the backoff has
	// stopped being a backoff and become a poll, and a poll every half hour is
	// what an outage nobody is watching costs.
	MaxInterval = 30 * time.Minute
	// Window bounds the whole of one boundary's recovery. Something has to end
	// it — a rule that never gave up would hold a claim, a worktree, and a
	// promotion lease against a network that is not coming back — and two hours
	// is long enough that every transport failure on record has resolved inside
	// it, and short enough that a person hears about one that has not on the same
	// afternoon it happened.
	Window = 2 * time.Hour
)

// Interval is the wait before a boundary's nth attempt, counted from 1. The
// series is Fibonacci in seconds — 1, 1, 2, 3, 5, 8, 13 — which is the shape the
// operator asked for: it stays cheap while a reset connection is still the most
// likely explanation, and reaches the cap in about seventy minutes rather than
// in the four an exponential would take.
func Interval(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	previous, current := 0, 1
	for at := 1; at < attempt; at++ {
		previous, current = current, previous+current
		if time.Duration(current)*time.Second >= MaxInterval {
			return MaxInterval
		}
	}
	if wait := time.Duration(current) * time.Second; wait < MaxInterval {
		return wait
	}
	return MaxInterval
}

// Recoverable reports a failure whose class says the next attempt may well
// succeed. It is deliberately a closed answer about a small set rather than an
// opinion about everything: anything it does not recognize keeps the behavior it
// already had, so a wrong answer here can only cost a retry that was never taken
// and never a judgment on the work turned into a wait.
func Recoverable(err error) bool {
	if err == nil {
		return false
	}
	// The in-process forms, for the few boundaries that speak to a network
	// themselves rather than through a subprocess. They are checked first because
	// they are the exact answer, where the text below is a reading of one.
	for _, transport := range []error{
		syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED,
		syscall.EPIPE, syscall.EHOSTUNREACH, syscall.ENETUNREACH,
		syscall.ENETDOWN, syscall.ETIMEDOUT, io.ErrUnexpectedEOF,
	} {
		if errors.Is(err, transport) {
			return true
		}
	}
	return RecoverableDetail(err.Error())
}

// RecoverableDetail answers the same question about text rather than an error.
// Almost everything the harness meets at these boundaries arrives that way: `gh`
// and `git` are subprocesses whose failure is what they printed, and a provider
// death is the detail its adapter carried out of the stream.
func RecoverableDetail(detail string) bool {
	// A refusal of the credential is an answer, whatever else the transport said
	// on its way out. SSH follows "Permission denied (publickey)" with "Connection
	// closed by <host>", and reading the second line alone would wait two hours on
	// a key nobody is going to load.
	if AuthenticationRefusedDetail(detail) {
		return false
	}
	normalized := strings.ToLower(detail)
	for _, phrase := range recoverablePhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// recoverablePhrases are how the three classes this item covers actually read
// where the harness meets them. Each is what one of `git`, `ssh`, `gh`, or a
// provider CLI writes, kept as the words it writes rather than as a category
// nobody printed.
//
// What is deliberately absent is as much of the decision as what is here. An
// authentication failure, a refused merge, a protected branch, a conflict, and a
// 4xx of any kind all earn the identical answer on the next attempt, so none of
// them is recoverable however transport-shaped its wording. Nor is a bare
// "timeout" or a bare "500": a check that timed out and a provider that answered
// with an error are both real answers about the work, and folding them in here
// would turn a verdict into a wait.
var recoverablePhrases = []string{
	// A connection reset: the class that killed all four runs.
	"connection reset",
	"connection aborted",
	"broken pipe",
	// A connection that went away mid-flight. The provider's own wording for it
	// is "Connection closed mid-response", which is the specimen the relaunch
	// budget was built from.
	"connection closed",
	"computer went to sleep mid-response",
	"closed by remote host",
	"remote end hung up unexpectedly",
	"unexpected eof",
	"early eof",
	"rpc failed",
	// A network that is not there. A name that does not resolve is in this class
	// rather than in a wrong-address one: the addresses the harness speaks to are
	// the forge and the provider, and both resolve except when resolution itself
	// is down.
	"network is unreachable",
	"network is down",
	"no route to host",
	"could not resolve host",
	"could not resolve hostname",
	"temporary failure in name resolution",
	"name or service not known",
	// A transport that refused or timed out before anything read the request.
	"connection refused",
	"connection timed out",
	"operation timed out",
	"i/o timeout",
	"tls handshake timeout",
	"handshake failed",
	// A forge or a proxy in front of one refusing to carry the request at all.
	// These are the gateway statuses said in words rather than as numbers: a bare
	// "502" appears in commit identifiers, byte counts, and exit codes, and a
	// phrase that matched one of those would turn an answer about the work into a
	// wait.
	"bad gateway",
	"service unavailable",
	"gateway timeout",
	// A subprocess that produced no verdict at all, which is how the tracker's
	// own client reports a `bd` that was killed or never completed:
	// `bd close failed with status cancelled and exit code -1: ...`. It is the
	// same class as a dropped connection and not a different one — nothing judged
	// the work, and the store being contended is far more often the reason than
	// the store being broken, which is the reading yoyodyne-ifd.232 already
	// applied to tracker *reads*.
	//
	// Both are the exact strings internal/beads formats rather than categories,
	// which is what keeps them narrow. "exit code -1" carries its colon so it
	// cannot match the "-1" that opens "-12", and the status is matched with the
	// words in front of it so a work item whose own text says "cancelled" is not
	// read as a process that was.
	"failed with status cancelled",
	"exit code -1:",
}

// AuthenticationRefusedDetail reports text in which the harness could not reach
// a remote as itself: an SSH key the server would not take, a forge that turned
// an HTTPS login away, a machine that could not name the user the harness runs
// as (so SSH never started), or a GitHub command line tool that is not logged
// in. It is the other closed reading this
// package makes, and the opposite verdict: asking again earns the identical
// answer until somebody loads the key or renews the login, so it is never waited
// out, and it is never a verdict on the work either.
//
// The phrases are what `ssh`, `git`, and GitHub's own remote write, kept as
// their words for the reason recoverablePhrases keeps them. A bare "denied" or
// "403" is deliberately absent: a protected branch refusing a push is also a
// denial, and that one is the forge applying a rule rather than refusing a key.
func AuthenticationRefusedDetail(detail string) bool {
	normalized := strings.ToLower(detail)
	for _, phrase := range authenticationPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

var authenticationPhrases = []string{
	// The SSH refusal three approved changes stopped on: yoyodyne-ifd.428.16,
	// 429.3, and 384.
	"permission denied (publickey",
	"host key verification failed",
	// An HTTPS remote turning the login away, or having none to ask for.
	"authentication failed for",
	"invalid username or password",
	"could not read username",
	"could not read password",
	// GitHub's own words for a credential with no write access to the repository.
	"remote: permission to ",
	// The machine unable to look up the user the harness runs as, which SSH
	// reports before it opens any connection: there is no identity to present
	// until the machine can name its user again.
	"no user exists for uid",
	// The GitHub command line tool with no login, in its own words and in the
	// harness's when it found the tool unauthenticated before publishing.
	"not logged into any github hosts",
	"gh auth login",
}
