package recovery

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"
)

// The series is what the operator asked for, and a test states it whole rather
// than checking that it grows: what makes Fibonacci the right shape is the first
// few intervals being cheap, and an assertion about monotonicity would pass for
// an exponential that spends the window in four attempts.
func TestIntervalIsFibonacciSecondsCappedAtHalfAnHour(t *testing.T) {
	t.Parallel()

	for attempt, want := range map[int]time.Duration{
		0:  time.Second,
		1:  time.Second,
		2:  time.Second,
		3:  2 * time.Second,
		4:  3 * time.Second,
		5:  5 * time.Second,
		6:  8 * time.Second,
		7:  13 * time.Second,
		8:  21 * time.Second,
		9:  34 * time.Second,
		10: 55 * time.Second,
	} {
		if got := Interval(attempt); got != want {
			t.Errorf("Interval(%d) = %s, want %s", attempt, got, want)
		}
	}
	// Past the cap every wait is the cap, so an outage nobody is watching costs a
	// probe every half hour rather than a series that keeps doubling.
	for _, attempt := range []int{18, 25, 100, 1000} {
		if got := Interval(attempt); got != MaxInterval {
			t.Errorf("Interval(%d) = %s, want the cap %s", attempt, got, MaxInterval)
		}
	}
}

// The cap has to be reached rather than merely respected: a series that stopped
// growing early would keep a run polling a dead network far more often than the
// operator's rule asks for.
func TestIntervalReachesTheCapWithinTheWindow(t *testing.T) {
	t.Parallel()

	var waited time.Duration
	reached := 0
	for attempt := 1; attempt <= 64; attempt++ {
		interval := Interval(attempt)
		if interval == MaxInterval && reached == 0 {
			reached = attempt
		}
		waited += interval
		if waited > Window {
			break
		}
	}
	if reached == 0 {
		t.Fatalf("the series never reached the %s cap inside the %s window", MaxInterval, Window)
	}
	if reached < 15 || reached > 20 {
		t.Errorf("the series reached the cap at attempt %d; the Fibonacci shape puts it around 17, so this is a different series", reached)
	}
}

func TestRecoverableRecognizesTheThreeClassesAndNothingElse(t *testing.T) {
	t.Parallel()

	// Each of these is what one of git, ssh, gh, or a provider CLI actually
	// prints. The list is the specimens rather than categories, because what the
	// harness reads is the words.
	for _, recoverable := range []string{
		"fatal: unable to access 'https://github.com/x/y.git/': OpenSSL SSL_read: Connection reset by peer, errno 54",
		"Connection reset by peer",
		"send-pack: unexpected disconnect while reading sideband packet\nfatal: the remote end hung up unexpectedly",
		"error: RPC failed; curl 56 Recv failure: Connection was reset",
		"API Error: Connection closed mid-response. The response above may be incomplete.",
		"kex_exchange_identification: Connection closed by remote host",
		"fatal: early EOF",
		"unexpected EOF",
		"Your computer went to sleep mid-response. The response above may be incomplete.",
		"ssh: connect to host github.com port 22: Network is unreachable",
		"ssh: connect to host github.com port 22: No route to host",
		"ssh: Could not resolve hostname github.com: Temporary failure in name resolution",
		"dial tcp: lookup api.github.com: no such host: name or service not known",
		"dial tcp 140.82.114.4:443: connect: connection refused",
		"dial tcp 140.82.114.4:443: connect: connection timed out",
		"Post \"https://api.anthropic.com/v1/messages\": net/http: TLS handshake timeout",
		"read tcp 10.0.0.2:52134->140.82.114.4:443: i/o timeout",
		"HTTP 502: Bad Gateway (https://api.github.com/graphql)",
		"HTTP 503: Service Unavailable",
		"HTTP 504: Gateway Timeout",
		// The tracker's own client, reporting a `bd` that produced no verdict.
		// These are the strings internal/beads formats, and the first is the
		// specimen that admitted yoyodyne-ifd.232.
		"bd list failed with status cancelled and exit code -1: ",
		"close integrated work item: bd close failed with status cancelled and exit code -1: signal: killed",
	} {
		if !RecoverableDetail(recoverable) {
			t.Errorf("RecoverableDetail(%q) = false, want the class recognized", recoverable)
		}
	}

	// Everything an operator has to see promptly, and everything that would earn
	// the identical answer on the next attempt. A wrong answer here is the
	// expensive one: it turns a judgment about the work into a wait.
	for _, terminal := range []string{
		"",
		"gh: Not Found (HTTP 404)",
		"HTTP 403: Resource not accessible by integration",
		"error: failed to push some refs to 'origin' (non-fast-forward)",
		"CONFLICT (content): Merge conflict in internal/orchestrator/pipeline.go",
		"the forge refused to merge pull request 12 with the merge method: the pull request conflicts with the base branch (DIRTY)",
		"gh auth status: You are not logged into any GitHub hosts",
		"API Error: 500 {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"Internal server error\"}}",
		"check `make test` timed out after 30m0s",
		"exit status 1: 503 files changed",
		"commit 502f3c9de0a1c7b45023ad9915de5024f0a1c7b4 is not on the target branch",
		// A tracker that answered. The store said no; asking again earns the same
		// no, and the exit codes here must not be read as the -1 that means a
		// process produced nothing.
		"bd close failed with status failed and exit code 1: issue yoyodyne-ifd.9 not found",
		"bd update failed with status failed and exit code -12: killed by the operator",
		"work item yoyodyne-ifd.9 carries no cost after being priced",
		"the operator cancelled this run",
	} {
		if RecoverableDetail(terminal) {
			t.Errorf("RecoverableDetail(%q) = true, want an answer nobody should wait on", terminal)
		}
	}
}

// A refused credential is never waited out, even where SSH's closing words
// alone would read as a dropped connection, and it is its own class: the three
// approved changes that stopped on "Permission denied (publickey)" were waiting
// on a key, not on a network.
func TestAnAuthenticationRefusalIsNeverRecoverable(t *testing.T) {
	t.Parallel()

	for _, refused := range []string{
		"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.",
		"git@github.com: Permission denied (publickey).\nConnection closed by 140.82.112.3 port 22\nfatal: Could not read from remote repository.",
		"Host key verification failed.\nfatal: Could not read from remote repository.",
		"remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/x/y.git/'",
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"remote: Permission to x/y.git denied to somebody.\nfatal: unable to access 'https://github.com/x/y.git/': The requested URL returned error: 403",
		// The machine could not name its own user, so SSH never connected.
		"No user exists for uid 501\nfatal: Could not read from remote repository.",
		// The GitHub command line tool with no login, in its words and the harness's.
		"gh auth status: You are not logged into any GitHub hosts. To log in, run: gh auth login",
		"the GitHub CLI is not authenticated; run `gh auth login` before handing published work to Yoyodyne",
	} {
		if !AuthenticationRefusedDetail(refused) {
			t.Errorf("AuthenticationRefusedDetail(%q) = false, want the refusal recognized", refused)
		}
		if RecoverableDetail(refused) || Recoverable(errors.New(refused)) {
			t.Errorf("Recoverable(%q) = true, want a refused credential never waited out", refused)
		}
	}
	for _, other := range []string{
		"Connection reset by peer",
		"error: failed to push some refs to 'origin' (non-fast-forward)",
		"remote: error: GH006: Protected branch update failed for refs/heads/main.",
		"HTTP 403: Resource not accessible by integration",
	} {
		if AuthenticationRefusedDetail(other) {
			t.Errorf("AuthenticationRefusedDetail(%q) = true, want only a refused credential read as one", other)
		}
	}
}

func TestRecoverableReadsWrappedErrorsAndTransportErrnos(t *testing.T) {
	t.Parallel()

	if Recoverable(nil) {
		t.Error("Recoverable(nil) = true, want false")
	}
	// The boundaries wrap what they were handed, so the class has to survive the
	// wrapping the harness itself does.
	wrapped := fmt.Errorf("publish the developer branch: %w", errors.New("fatal: Connection reset by peer"))
	if !Recoverable(wrapped) {
		t.Errorf("Recoverable(%v) = false, want the wrapped class recognized", wrapped)
	}
	// The few boundaries that speak to a network in-process answer with an errno
	// rather than with words, and the errno is the exact answer.
	for _, transport := range []error{syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.EPIPE, syscall.ETIMEDOUT} {
		if !Recoverable(fmt.Errorf("reach the forge: %w", transport)) {
			t.Errorf("Recoverable(%v) = false, want the errno recognized", transport)
		}
	}
	if Recoverable(fmt.Errorf("read the record: %w", syscall.ENOENT)) {
		t.Error("a missing file is not a transport failure")
	}
}
