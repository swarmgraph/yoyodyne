package spend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestEveryInvocationLandsInTheLogExactlyOnce(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend:       "claude-code",
			SessionID:     "session-1",
			ResolvedModel: "claude-opus-4-1-20250805",
			CostUSD:       4.25,
			CostReported:  true,
		}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.CostUSD != 4.25 {
		t.Fatalf("the invocation's own result did not come back: %#v", result)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want exactly one", len(log.lines))
	}

	line := log.lines[0]
	if !line.Known() || line.AmountUSD != 4.25 {
		t.Fatalf("the provider's own figure was not recorded: %#v", line)
	}
	// The role and the requested model come off the request and the resolved model
	// off the result, so no caller gets to assert either.
	if line.Role != "developer" || line.Model != "opus" || line.ResolvedModel != "claude-opus-4-1-20250805" {
		t.Fatalf("what served the invocation was not recorded: %#v", line)
	}
	if line.Phase != runstate.SpendPhaseDevelopment || line.RunID != "run-0123456789abcdef0123456789abcdef" {
		t.Fatalf("the attribution was not carried: %#v", line)
	}
	if line.AccountAlias != "default" || line.ConfigRevision != "cfg-0123456789ab" {
		t.Fatalf("the account and the configuration were not carried: %#v", line)
	}
	if err := line.Validate(); err != nil {
		t.Fatalf("the recorded line does not satisfy the durable contract: %v", err)
	}
}

// Which harness made the invocation is on every line and comes from no caller.
// It is the one thing on a line that says whether the code that spent the money
// was the code that was merged, and a field a call site supplied would be a field
// a call site could forget: the line would then say whose account paid for an
// invocation without saying what made it.
//
// This is deliberately not parallel: it stands the process's own answer aside for
// the length of the test, which is the only way to drive a fact a test binary
// does not have one of.
func TestEveryLineSaysWhichHarnessMadeTheInvocation(t *testing.T) {
	restore := processBuild
	processBuild = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
	t.Cleanup(func() { processBuild = restore })

	log := &recordingLog{}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", CostUSD: 1, CostReported: true}, nil
	})
	if _, err := metered.Run(context.Background(), testRequest()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// And an invocation that failed says it too. The money was spent either way,
	// and a failure is exactly the case somebody comes back to asking which build
	// produced it.
	failing := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("the provider was unreachable")
	})
	if _, err := failing.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("Run() accepted an invocation the provider refused")
	}
	if len(log.lines) != 2 {
		t.Fatalf("recorded %d line(s), want one per invocation", len(log.lines))
	}
	for _, line := range log.lines {
		if line.Build != processBuild {
			t.Fatalf("recorded build = %q, want the build that made the call %q", line.Build, processBuild)
		}
		if err := line.Validate(); err != nil {
			t.Fatalf("the recorded line does not satisfy the durable contract: %v", err)
		}
	}
}

func TestAnInvocationTheProviderDidNotPriceIsRecordedAsUnknown(t *testing.T) {
	t.Parallel()

	// The provider answered and said nothing about the cost. A zero here would be
	// added up as an invocation that was free, which is the opposite of the truth.
	answered := &recordingLog{}
	metered := testMetered(answered, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "codex", Usage: json.RawMessage(`{"input_tokens":3120,"cache_read_input_tokens":13184,"output_tokens":5}`), IsError: true, StopReason: "api_error"}, nil
	})
	if _, err := metered.Run(context.Background(), testRequest()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(answered.lines) != 1 || answered.lines[0].Known() || answered.lines[0].AmountUSD != 0 {
		t.Fatalf("recorded = %#v, want one unknown spend", answered.lines)
	}
	if string(answered.lines[0].Usage) != `{"input_tokens":3120,"cache_read_input_tokens":13184,"output_tokens":5}` {
		t.Fatal(answered.lines[0].Usage)
	}
	if !strings.Contains(answered.lines[0].Unknown, "without reporting what it cost") {
		t.Fatalf("the line does not say why nobody knows: %q", answered.lines[0].Unknown)
	}

	// And the invocation that died before the provider reported anything: still a
	// line, and it names what killed it, because that is what somebody
	// reconciling a bill has to know.
	died := &recordingLog{}
	metered = testMetered(died, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("Claude Code stream ended without a result event")
	})
	if _, err := metered.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("Run() hid the invocation's own failure")
	}
	if len(died.lines) != 1 || died.lines[0].Known() {
		t.Fatalf("recorded = %#v, want one unknown spend", died.lines)
	}
	if !strings.Contains(died.lines[0].Unknown, "stream ended without a result event") {
		t.Fatalf("the line does not carry what killed the invocation: %q", died.lines[0].Unknown)
	}
	// A result that never arrived names no backend, so the configured one is what
	// the line is pinned to rather than nothing at all.
	if died.lines[0].Backend != "claude-code" {
		t.Fatalf("backend = %q, want the configured one", died.lines[0].Backend)
	}
}

// A line the log will not take is reported rather than swallowed, which is a
// deliberate trade: an invocation the provider already served and charged for
// comes back as a failure. It is the same weight the harness gives an event it
// could not record, and the alternative is an operator's cost log quietly
// missing lines nothing says are missing.
func TestALineThatCannotBeMadeDurableIsReportedRatherThanLost(t *testing.T) {
	t.Parallel()

	log := &recordingLog{failure: errors.New("the disk is full")}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", CostUSD: 1, CostReported: true}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "the disk is full") {
		t.Fatalf("Run() error = %v, want the failure to record", err)
	}
	// The invocation's own result still comes back: the money was spent and what
	// the provider said about it is not lost because the log could not take it.
	if result.CostUSD != 1 {
		t.Fatalf("the result was dropped with the record: %#v", result)
	}

	// The invocation's own failure is not replaced by the failure to record it.
	// They are two things wrong, and a caller deciding what to do about the run
	// needs the first.
	metered = testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code"}, errors.New("developer backend failed")
	})
	_, err = metered.Run(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "developer backend failed") || !strings.Contains(err.Error(), "the disk is full") {
		t.Fatalf("Run() error = %v, want both failures", err)
	}

	// A caller for which that trade comes out the other way says so, and is
	// handed the failure rather than having it cost the invocation. The answer
	// the provider already gave and already charged for still comes back.
	var handed error
	metered = testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", FinalText: "an answer", CostUSD: 1, CostReported: true}, nil
	})
	metered.RecordFailure = func(err error) { handed = err }
	result, err = metered.Run(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Run() error = %v, want the invocation to survive the failure to record it", err)
	}
	if result.FinalText != "an answer" {
		t.Fatalf("the answer was dropped with the record: %#v", result)
	}
	if handed == nil || !strings.Contains(handed.Error(), "the disk is full") {
		t.Fatalf("the failure to record reached nobody: %v", handed)
	}
}

func TestAProviderWithNowhereToRecordSpendsExactlyAsItWould(t *testing.T) {
	t.Parallel()

	invoked := 0
	metered := testMetered(nil, func(backend.RunRequest) (backend.RunResult, error) {
		invoked++
		return backend.RunResult{Backend: "claude-code", CostUSD: 2, CostReported: true}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if invoked != 1 || result.CostUSD != 2 {
		t.Fatalf("the invocation did not happen unchanged: invoked %d, result %#v", invoked, result)
	}
}

func testMetered(log Log, run func(backend.RunRequest) (backend.RunResult, error)) Metered {
	return Metered{
		Provider: providerFunc(run),
		Log:      log,
		Attribution: Attribution{
			ProductID:      "yoyodyne",
			Agent:          "developer",
			Phase:          runstate.SpendPhaseDevelopment,
			AccountAlias:   "default",
			ConfigRevision: "cfg-0123456789ab",
			Backend:        "claude-code",
			RunID:          "run-0123456789abcdef0123456789abcdef",
			WorkItemID:     "yoyodyne-ifd.182",
		},
		Clock: fixedClock{},
	}
}

func testRequest() backend.RunRequest {
	return backend.RunRequest{
		RunID: "run-0123456789abcdef0123456789abcdef",
		Role:  "developer",
		Model: "opus",
	}
}

type providerFunc func(backend.RunRequest) (backend.RunResult, error)

func (f providerFunc) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	return f(request)
}

// recordingLog is a cost log that keeps what it was given, and one that refuses
// everything when a failure is set.
type recordingLog struct {
	lines []runstate.Spend
	// failure is what appending reports and totalFailure what reading a session's
	// recorded total reports. They are separate because they are separate
	// failures: one loses the line after the amount was worked out, the other
	// stops the amount being worked out at all.
	failure      error
	totalFailure error
}

func (l *recordingLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return l.failure
}

// ReportedSessionTotal answers from the lines this log has already taken, the
// way the durable store answers from the lines it has already written.
func (l *recordingLog) ReportedSessionTotal(sessionID string) (float64, bool, error) {
	if l.totalFailure != nil {
		return 0, false, l.totalFailure
	}
	total, found := 0.0, false
	for _, line := range l.lines {
		if line.SessionID == sessionID && line.Known() {
			total, found = line.ReportedTotal(), true
		}
	}
	return total, found, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC) }

// A line says which endpoint served the turn and not only which provider was
// named: the account, the model, and the backend were already there, and the
// adapter that reached the provider is what completes the identity. The
// adapter's own word is preferred, because it is what actually read the stream.
func TestALineSaysWhichEndpointServedTheTurn(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend:        "my-harness",
			AdapterVersion: backend.ClaudeCodeAdapterVersion,
			CostUSD:        1,
			CostReported:   true,
		}, nil
	})
	if _, err := metered.Run(context.Background(), testRequest()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	line := log.lines[0]
	if line.Backend != "my-harness" || line.AdapterVersion != backend.ClaudeCodeAdapterVersion {
		t.Fatalf("the endpoint was not recorded: %#v", line)
	}
	if line.AccountAlias != "default" || line.Model != "opus" {
		t.Fatalf("the rest of the endpoint identity was not carried: %#v", line)
	}
	if err := line.Validate(); err != nil {
		t.Fatalf("the recorded line does not satisfy the durable contract: %v", err)
	}
}

// An invocation that died before its adapter could say anything still names the
// adapter, where the harness knows one: the provider it was configured for is a
// backend this build ships and therefore has an adapter version of its own.
// Nothing is guessed — a provider this build has no description of records the
// provider and no adapter rather than a version nobody established.
func TestADeadInvocationStillNamesTheAdapterThisBuildKnows(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("the provider went away")
	})
	if _, err := metered.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("Run() reported no failure, want the provider's own")
	}
	if version := log.lines[0].AdapterVersion; version != backend.ClaudeCodeAdapterVersion {
		t.Fatalf("adapter version = %q, want the one this build ships for the configured backend", version)
	}

	declared := &recordingLog{}
	unknown := testMetered(declared, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("the provider went away")
	})
	unknown.Attribution.Backend = "my-harness"
	if _, err := unknown.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("Run() reported no failure, want the provider's own")
	}
	if version := declared.lines[0].AdapterVersion; version != "" {
		t.Fatalf("adapter version = %q, want nothing for a provider this build has no description of", version)
	}
}

// A provider asked to resume a session reports what that session has cost since
// it opened, so three turns of one conversation report a rising total and not
// three costs. Recording what each of them reported charges the operator for the
// whole session again on every turn: this product's management conversations
// read at thirty times what they cost, and its last seven days at $30,841
// against an actual $3,464, on exactly that arithmetic.
//
// So the log adds up to the session's final total, and the three lines are what
// each turn moved it by. The store is the durable one rather than a fake,
// because the session outlives whatever process opened it and the log is the
// only thing that remembers what it was last reported at.
func TestResumingASessionRecordsWhatEachInvocationAddedRatherThanTheSessionAgain(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewSpendStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewSpendStore() error = %v", err)
	}
	// What the provider reports at the end of each of three turns of one session.
	reported := []float64{2.50, 6.25, 9.00}
	for _, total := range reported {
		metered := testMetered(store, func(backend.RunRequest) (backend.RunResult, error) {
			return backend.RunResult{
				Backend:      "claude-code",
				SessionID:    "session-resumed",
				CostUSD:      total,
				CostReported: true,
			}, nil
		})
		if _, err := metered.Run(context.Background(), testRequest()); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}

	lines, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("recorded %d line(s), want one per invocation", len(lines))
	}
	var total float64
	for _, line := range lines {
		total += line.AmountUSD
	}
	final := reported[len(reported)-1]
	if total != final {
		t.Fatalf("the log adds up to %v, want the session's final total of %v; "+
			"summing what each invocation reported would have made it %v", total, final, 17.75)
	}
	// Each line is what its own turn cost, and the first records the whole because
	// it opened the session and had nothing to be an increment over.
	for index, want := range []float64{2.50, 3.75, 2.75} {
		if lines[index].AmountUSD != want {
			t.Fatalf("line %d is %v, want %v", index, lines[index].AmountUSD, want)
		}
	}
	// And what the provider actually said is kept beside the corrected figure, so
	// the correction can be checked rather than taken on trust. The first line has
	// nothing to keep: its amount is the reported figure.
	if lines[0].ReportedTotalUSD != 0 {
		t.Fatalf("the first line reports a total it did not correct: %#v", lines[0])
	}
	if lines[1].ReportedTotalUSD != 6.25 || lines[2].ReportedTotalUSD != 9.00 {
		t.Fatalf("the reported totals were not kept: %#v, %#v", lines[1], lines[2])
	}
	for _, line := range lines {
		if err := line.Validate(); err != nil {
			t.Fatalf("a corrected line does not satisfy the durable contract: %v", err)
		}
	}
}

// Two sessions recorded side by side are priced apart. A run's repair attempts
// resume the developer's session while the reviewer's invocations run in one of
// their own, so a rule that tracked a single running total per product would
// read every crossing between them as a total that had fallen back.
func TestSessionsArePricedApartFromEachOther(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	spend := func(session string, reportedTotal float64) {
		metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
			return backend.RunResult{
				Backend:      "claude-code",
				SessionID:    session,
				CostUSD:      reportedTotal,
				CostReported: true,
			}, nil
		})
		if _, err := metered.Run(context.Background(), testRequest()); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	spend("session-developer", 8.0)
	spend("session-reviewer", 0.5)
	spend("session-developer", 11.0)
	spend("session-reviewer", 0.9)

	for index, want := range []float64{8.0, 0.5, 3.0, 0.4} {
		if log.lines[index].AmountUSD != want {
			t.Fatalf("line %d is %v, want %v", index, log.lines[index].AmountUSD, want)
		}
	}
}

// A provider that reports what the invocation itself cost, rather than a running
// total, is left alone. Nothing here asks a provider which of the two it does;
// the rule reads it off the figures, and a figure that did not rise is not one
// this invocation is an increment over. It is also what a session whose total
// restarts mid-conversation looks like, which is what this product's provider
// did on 2026-09-19 -- in the other direction, and inside one session.
func TestAProviderReportingEachInvocationsOwnCostIsRecordedAsItReports(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	for _, cost := range []float64{3.0, 1.25, 0.5} {
		metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
			return backend.RunResult{
				Backend:      "claude-code",
				SessionID:    "session-own-costs",
				CostUSD:      cost,
				CostReported: true,
			}, nil
		})
		if _, err := metered.Run(context.Background(), testRequest()); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	for index, want := range []float64{3.0, 1.25, 0.5} {
		if log.lines[index].AmountUSD != want || log.lines[index].ReportedTotalUSD != 0 {
			t.Fatalf("line %d is %#v, want the reported figure recorded whole", index, log.lines[index])
		}
	}
}

// A log that cannot say what the session was last reported at cannot say what
// this invocation cost either, and the line is not written rather than written
// at the whole session's total. The failure is reported the way a log that
// refuses the append is, which is the same failure: the total is read from the
// file the line is about to be appended to.
func TestALogThatCannotSayWhatTheSessionCostDoesNotRecordTheWholeSessionInstead(t *testing.T) {
	t.Parallel()

	log := &recordingLog{totalFailure: errors.New("the spend log could not be read")}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend:      "claude-code",
			SessionID:    "session-resumed",
			CostUSD:      42.0,
			CostReported: true,
		}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "the spend log could not be read") {
		t.Fatalf("Run() error = %v, want the reason the session's total could not be read", err)
	}
	// The provider's own answer still comes back: what failed is the bookkeeping.
	if result.CostUSD != 42.0 {
		t.Fatalf("the invocation's own result did not come back: %#v", result)
	}
	if len(log.lines) != 0 {
		t.Fatalf("recorded %#v, want no line rather than one at the session's total", log.lines)
	}
}

// A caller counting what it spent is told what the invocation cost rather than
// what the provider reported, and is told even when the log would not take the
// line. Those are two claims about one hook and they pull the same way: the
// figure an operator is shown and the figure the log holds are one number, and
// a log that refused a line has already said so without also stopping a budget
// counting.
func TestACallerIsToldWhatTheInvocationCostRatherThanWhatTheProviderReported(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	var counted []float64
	spendTwice := func() {
		for _, reportedTotal := range []float64{0.75, 2.00} {
			metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
				return backend.RunResult{
					Backend:      "claude-code",
					SessionID:    "session-resumed",
					CostUSD:      reportedTotal,
					CostReported: true,
				}, nil
			})
			metered.Recorded = func(line runstate.Spend) { counted = append(counted, line.AmountUSD) }
			if _, err := metered.Run(context.Background(), testRequest()); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
		}
	}
	spendTwice()
	if len(counted) != 2 || counted[0] != 0.75 || counted[1] != 1.25 {
		t.Fatalf("counted %v, want the whole first figure and the $1.25 the second added", counted)
	}

	// And a log that refuses the line still tells the caller what was spent.
	refusing := &recordingLog{failure: errors.New("the disk is full")}
	var overRefusal []float64
	metered := testMetered(refusing, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", SessionID: "session-new", CostUSD: 3, CostReported: true}, nil
	})
	metered.Recorded = func(line runstate.Spend) { overRefusal = append(overRefusal, line.AmountUSD) }
	if _, err := metered.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("Run() hid the failure to record")
	}
	if len(overRefusal) != 1 || overRefusal[0] != 3 {
		t.Fatalf("counted %v over a refused line, want the spend still counted", overRefusal)
	}
}
