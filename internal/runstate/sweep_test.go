package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

func newSweepStore(t *testing.T) *SweepStore {
	t.Helper()
	store, err := NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	return store
}

// The cadence is the whole of what the claim is for: a task that fired ten
// minutes ago does not fire again on the next pull, and one whose interval has
// passed does.
func TestClaimPacesTheCadence(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start); err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	_, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(10*time.Minute))
	if !errors.Is(err, ErrSweepNotDue) {
		t.Fatalf("second Claim() error = %v, want ErrSweepNotDue", err)
	}
	claimed, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("third Claim() error = %v", err)
	}
	if claimed.Firings != 2 {
		t.Errorf("firings = %d, want 2", claimed.Firings)
	}
}

// A task that has never fired is due at once. A schedule turned on at nine that
// produced nothing until ten looks broken for an hour, and the first pass is the
// one most worth having.
func TestATaskThatHasNeverFiredIsDueAtOnce(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	claimed, err := store.Claim(context.Background(), "a-sweep", 24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Firings != 1 {
		t.Errorf("firings = %d, want the first firing", claimed.Firings)
	}
}

// A firing that failed waits for its next cadence rather than being retried at
// once. It is the deliberate opposite of the escalation record beside it: the
// next pass looks at everything this one would have, and retrying at once spends
// turns against whatever was already failing.
func TestAFailedFiringStillMovesTheClock(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	settled, err := store.Settle(context.Background(), "a-sweep", "the conversation could not be opened")
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if settled.Problem == "" {
		t.Error("the claim records no problem after a firing that failed")
	}
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(time.Minute)); !errors.Is(err, ErrSweepNotDue) {
		t.Fatalf("Claim() after a failed firing error = %v, want ErrSweepNotDue", err)
	}
}

// A problem is the most recent firing's or it is nothing. A claim still carrying
// last week's failure would send somebody after a fault that cleared six firings
// ago.
func TestANewFiringClearsTheLastOnesProblem(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), "a-sweep", "it failed"); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	claimed, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Problem != "" {
		t.Errorf("problem = %q, want it cleared by the firing that followed", claimed.Problem)
	}
}

// Two tasks pace independently: one that fired a minute ago must not hold back
// one that is due.
func TestTasksPaceIndependently(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "first-sweep", time.Hour, now); err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if _, err := store.Claim(context.Background(), "second-sweep", time.Hour, now); err != nil {
		t.Fatalf("Claim(second) error = %v", err)
	}
	claimed, found, err := store.Find("first-sweep")
	if err != nil || !found {
		t.Fatalf("Find() = %v, %v, %v", claimed, found, err)
	}
	if claimed.Task != "first-sweep" {
		t.Errorf("task = %q, want the one asked for", claimed.Task)
	}
}

func TestSweepsAreAppendedAndReadBack(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	recorded := Sweep{
		Task:      "a-sweep",
		Role:      "development-manager",
		StartedAt: at,
		EndedAt:   at.Add(time.Minute),
		Turns:     2,
		CostUSD:   0.42,
		Result: &sweep.Result{
			Status:   sweep.StatusComplete,
			Summary:  "two dead claims, both released",
			Findings: []sweep.Finding{{Issue: "a dead claim", Disposition: sweep.DispositionFixed, Filed: []string{"yoyodyne-ifd.300"}}},
		},
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	listed, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List() = %d sweeps, want 2", len(listed))
	}
	if listed[0].Result == nil || listed[0].Result.Summary != recorded.Result.Summary {
		t.Errorf("sweep = %+v, want the account as it was written", listed[0])
	}
	// One line per record and nothing between them. The append puts a newline in
	// front of a torn fragment, and this is what keeps that from firing on a
	// healthy log: a blank line every second record would be invisible to the
	// reader, which skips them, and would double the file for nothing.
	written, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if lines := strings.Count(string(written), "\n"); lines != 2 {
		t.Errorf("the log holds %d line(s) for 2 records:\n%s", lines, written)
	}
}

// A product nothing has swept has recorded nothing, which is not a failure to
// read: it is what every project looks like before its first firing.
func TestListOfAnUnsweptProductIsEmpty(t *testing.T) {
	t.Parallel()

	listed, _, err := newSweepStore(t).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("List() = %v, want nothing", listed)
	}
}

// A pass with neither an account nor a problem is a firing the record could say
// nothing at all about, which is the one state it must not be able to hold: a
// reader would see a sweep that happened and no way to tell whether it found
// nothing or failed.
func TestASweepMustSayWhatBecameOfIt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	silent := Sweep{Task: "a-sweep", Role: "development-manager", StartedAt: at, EndedAt: at}
	if err := newSweepStore(t).Append(silent); err == nil {
		t.Fatal("a sweep with no result and no problem was recorded")
	}
	// The quiet pass — an account that carries no findings — is the ordinary
	// result on a healthy harness and is a different fact entirely.
	quiet := silent
	quiet.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: "nothing unresolved"}
	if !quiet.FoundNothing() {
		t.Error("a pass that gave an account with no findings is not reported as having found nothing")
	}
	if err := newSweepStore(t).Append(quiet); err != nil {
		t.Fatalf("Append() of a quiet pass error = %v", err)
	}
}

// A failed pass's saved writes are kept on its record and read back as they
// were, and a write the record could not name is refused rather than kept.
func TestAFailedPassRecordsTheWritesItSaved(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	failed := Sweep{Task: "a-sweep", Role: "program-manager", StartedAt: at, EndedAt: at, Turns: 1, Failed: true,
		Problem: "turn 2 failed",
		Saved: []SavedWrite{
			{Kind: SavedMemory, Action: "remember", Memory: "line-stalls", Revision: 3},
			{Kind: SavedLaneReport, Revision: 2},
		}}
	store := newSweepStore(t)
	if err := store.Append(failed); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 || !recorded[0].Failed || len(recorded[0].Saved) != 2 || recorded[0].Saved[1] != failed.Saved[1] {
		t.Fatalf("List() = %+v, %v; want the failed pass and its two saved writes", recorded, err)
	}
	if !recorded[0].Unfinished() {
		t.Error("a pass whose turn failed does not read as unfinished")
	}
	if got := recorded[0].Saved[0].Describe(); got != `memory "line-stalls" (remember, revision 3)` {
		t.Errorf("Describe() = %q", got)
	}
	for name, write := range map[string]SavedWrite{
		"no kind":             {Revision: 1},
		"no revision":         {Kind: SavedLaneReport},
		"an unnamed memory":   {Kind: SavedMemory, Action: "remember", Revision: 1},
		"a named lane report": {Kind: SavedLaneReport, Memory: "line-stalls", Revision: 1},
	} {
		bad := failed
		bad.Saved = []SavedWrite{write}
		if err := newSweepStore(t).Append(bad); err == nil {
			t.Errorf("%s: a pass naming a write the record cannot name was recorded", name)
		}
	}
}

// The record has to hold what a whole firing can actually produce. A firing folds
// at most sweep.MaxMergedTurns turns together and each turn's block is capped at
// sweep.MaxBlockBytes, and each byte of what a block decodes to can take up to
// maxJSONGrowth bytes once the record encodes it again, so an encoded bound below
// that product is a bound that throws the busiest passes' reports away as it
// writes them.
func TestTheEncodedBoundHoldsTheLargestFiringAnAccountCanReach(t *testing.T) {
	t.Parallel()

	reachable := sweep.MaxMergedTurns * sweep.MaxBlockBytes * maxJSONGrowth
	if maxEncodedSweepBytes <= reachable {
		t.Fatalf("the encoded sweep bound is %d bytes and a firing's account can reach %d once encoded (%d turns of %d, grown up to %d times), so the heaviest passes would not store",
			maxEncodedSweepBytes, reachable, sweep.MaxMergedTurns, sweep.MaxBlockBytes, maxJSONGrowth)
	}
	if encoded, err := json.Marshal("<"); err != nil || len(encoded)-2 != maxJSONGrowth {
		t.Fatalf("json.Marshal(%q) = %s, %v; want the %d-byte escape the growth is sized on", "<", encoded, err, maxJSONGrowth)
	}
}

// And a firing whose every turn filled its block with text JSON escapes is
// stored rather than refused. Each finding here is text a block of its turn can
// carry, and there are as many of them as a firing can merge.
func TestAnAccountOfEscapedTextIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	perFinding := sweep.MaxBlockBytes/sweep.MaxFindings - 64
	account := &sweep.Result{Status: sweep.StatusComplete, Summary: "a pass that quoted a lot of markup"}
	for i := 0; i < sweep.MaxPassFindings; i++ {
		account.Findings = append(account.Findings, sweep.Finding{
			Issue:       strings.Repeat("<", min(perFinding, sweep.MaxTextBytes)),
			Disposition: sweep.DispositionLeft,
		})
	}
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	store := newSweepStore(t)
	if err := store.Append(Sweep{Task: "a-sweep", Role: "development-manager", StartedAt: at, EndedAt: at.Add(time.Minute), Turns: sweep.MaxMergedTurns, Result: account}); err != nil {
		t.Fatalf("Append() of an account of escaped text error = %v", err)
	}
	listed, unreadable, err := store.List()
	if err != nil || len(unreadable) != 0 || len(listed) != 1 || listed[0].Result == nil || len(listed[0].Result.Findings) != sweep.MaxPassFindings {
		t.Fatalf("List() = %d sweep(s), %d unreadable, %v; want the account read back whole", len(listed), len(unreadable), err)
	}
}

// And the store actually takes one. The bound above is arithmetic; this writes a
// pass-sized account and reads it back, which is what the durable report is for.
func TestAPassSizedAccountIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	account := &sweep.Result{Status: sweep.StatusComplete, Summary: "a very heavy pass"}
	for i := 0; i < sweep.MaxPassFindings; i++ {
		account.Findings = append(account.Findings, sweep.Finding{
			Issue:       strings.Repeat("a thing that was found ", 40),
			Disposition: sweep.DispositionFiled,
			Filed:       []string{"yoyodyne-ifd.300"},
		})
	}
	for i := 0; i < sweep.MaxPassQuestions; i++ {
		account.Questions = append(account.Questions, "something only a person can settle")
	}
	recorded := Sweep{
		Task:      "a-sweep",
		Role:      "development-manager",
		StartedAt: at,
		EndedAt:   at.Add(time.Minute),
		Turns:     sweep.MaxMergedTurns,
		Result:    account,
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("Append() of a pass-sized account error = %v", err)
	}
	listed, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Result == nil {
		t.Fatalf("listed = %+v, want the pass-sized account read back", listed)
	}
	if len(listed[0].Result.Findings) != sweep.MaxPassFindings {
		t.Errorf("findings = %d, want the %d that were written", len(listed[0].Result.Findings), sweep.MaxPassFindings)
	}
}

// The failure this log is actually exposed to: a write of a whole pass is not
// atomic, so a process killed partway through one leaves a torn line. Failing the
// listing on it would make one interrupted write cost every report before it,
// permanently, on the only surface those reports are read from.
func TestATornLineDoesNotCostTheReportsAroundIt(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	before := Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at.Add(time.Minute),
		Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "the pass before"},
	}
	after := before
	after.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: "the pass after"}
	if err := store.Append(before); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	// A write that stopped mid-record, in the shape a crash actually leaves it:
	// no trailing newline, because the newline is the last thing the interrupted
	// write would have put down. That is the whole difficulty — the next append
	// lands on the same line unless something closes the fragment off first, and
	// then the crash costs the record after it as well as the one it interrupted.
	torn, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := torn.WriteString(`{"schema_version":1,"product_id":"example","task":"a-sw`); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	torn.Close()
	if err := store.Append(after); err != nil {
		t.Fatalf("Append() after the torn line error = %v", err)
	}

	listed, unreadable, err := store.List()
	if err != nil {
		t.Fatalf("List() over a torn log error = %v, want the readable reports back", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %d sweeps, want the two either side of the torn line", len(listed))
	}
	if listed[0].Result.Summary != "the pass before" || listed[1].Result.Summary != "the pass after" {
		t.Errorf("listed = %+v, want both passes in the order they were written", listed)
	}
	// Named rather than dropped: a listing short by a record it never mentioned is
	// a worse answer than the failure it replaced.
	if len(unreadable) != 1 {
		t.Fatalf("unreadable = %+v, want the torn line named", unreadable)
	}
	if unreadable[0].Line != 2 {
		t.Errorf("unreadable line = %d, want the second line of the log", unreadable[0].Line)
	}
	if unreadable[0].Problem == "" {
		t.Error("the torn line is named with no reason, so nobody can tell what happened to it")
	}
}

// The sweeps a run store hands out are the ones NewSweepStore writes to, for the
// same state root and product.
//
// The two derive that directory by different arithmetic — the run store walks up
// from its own runs directory, and NewSweepStore builds the path from the state
// root — so nothing but this holds them to the same answer. Getting it wrong is
// silent in the worst way: firings would be written to one directory and
// `yoyo sweeps` would read another, and an empty listing is exactly what a
// schedule that has found nothing looks like. The whole point of the durable
// report is to be readable afterwards, so a disagreement here would make the
// feature appear to work while producing nothing anybody can find.
func TestTheSweepsOfARunStoreAreWhereNewSweepStoreWrites(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := NewStore(root, "example")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	direct, err := NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	if got, want := runs.Sweeps().Root(), direct.Root(); got != want {
		t.Errorf("run store sweeps root = %q, NewSweepStore root = %q; firings and the listing would use different directories", got, want)
	}
	if got, want := runs.Sweeps().Path(), direct.Path(); got != want {
		t.Errorf("run store sweep log = %q, NewSweepStore log = %q", got, want)
	}
}

// A scan that fails part way through is the third shape of the same rule, and
// the one the reader has to act on differently: a line too long for the scanner
// stops the reading dead, so nothing after it is seen at all. What came before it
// is still returned with the failure rather than instead of it, which is what
// lets `yoyo sweeps` render the passes it did read and say the listing is
// partial. Returning nothing would make one oversized line cost every report
// written before it, which is the failure the torn-line handling above exists to
// prevent, arriving by the other door.
func TestAScanThatFailsStillReturnsWhatItRead(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if err := store.Append(Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at.Add(time.Minute), Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "the pass before the unreadable tail"},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	// Longer than the reader's own buffer, so bufio.Scanner gives up on it rather
	// than setting it aside the way an undecodable line is set aside.
	oversized, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := oversized.WriteString(strings.Repeat("x", maxEncodedSweepBytes+1) + "\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	oversized.Close()

	listed, _, err := store.List()
	if err == nil {
		t.Fatal("List() over a log it could not scan to the end returned no error, so a caller cannot say the listing is partial")
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %d sweeps, want the one read before the scan failed", len(listed))
	}
	if listed[0].Result.Summary != "the pass before the unreadable tail" {
		t.Errorf("listed = %+v, want the pass written before the oversized line", listed)
	}
}

// A record from another product in this product's log is the same shape of
// problem and gets the same answer: named, and not fatal to the rest.
func TestARecordFromAnotherProductIsNamedRatherThanFatal(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if err := store.Append(Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at, Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "ours"},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	foreign, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := foreign.WriteString(`{"schema_version":1,"product_id":"elsewhere","task":"a-sweep","role":"development-manager","started_at":"2026-09-05T09:00:00Z","ended_at":"2026-09-05T09:00:00Z","turns":1,"problem":"theirs"}` + "\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	foreign.Close()

	listed, unreadable, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Result.Summary != "ours" {
		t.Errorf("listed = %+v, want only this product's pass", listed)
	}
	if len(unreadable) != 1 || !strings.Contains(unreadable[0].Problem, "elsewhere") {
		t.Errorf("unreadable = %+v, want the foreign record named", unreadable)
	}
}

// The pull requests a pass noticed are the harness's own record on the sweep,
// and a later pass reads them back to know which requests were already
// reported. They are written and read with the account that states them, and a
// record cannot carry one without the other.
func TestNoticedPullRequestsAreRecordedWithTheAccountThatStatesThem(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	noticed := ForgeNotice{Number: 445, URL: "https://forge.invalid/pull/445", HeadBranch: "yoyodyne/yoyodyne-ifd-283/aaaaaaaa", BaseBranch: "main", WorkItemID: "yoyodyne-ifd.283", ItemClosed: true}
	if err := store.Append(Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at.Add(time.Minute), Turns: 1,
		Result:       &sweep.Result{Status: sweep.StatusComplete, Summary: "one request held open", Findings: []sweep.Finding{noticed.Finding()}},
		PullRequests: []ForgeNotice{noticed},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	listed, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || len(listed[0].PullRequests) != 1 || listed[0].PullRequests[0] != noticed {
		t.Fatalf("listed = %+v, want the noticed request read back as written", listed)
	}

	// Without the account the notice would be reported nowhere a reader looks.
	err = store.Append(Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at.Add(time.Minute), Turns: 0,
		Problem:      "the role could not be reached",
		PullRequests: []ForgeNotice{noticed},
	})
	if err == nil || !strings.Contains(err.Error(), "carries the account") {
		t.Errorf("Append() of notices without an account error = %v, want the refusal", err)
	}
	// And a notice naming no condition reports nothing.
	if err := (ForgeNotice{Number: 1, HeadBranch: "x"}).Validate(); err == nil {
		t.Error("a notice with neither condition validated")
	}
}

// The finding a notice becomes names the request, the item, and which of the two
// conditions holds, and is left rather than fixed: noticing is the whole of what
// the pass did.
func TestANoticeStatesItselfAsAFindingLeftForSomebody(t *testing.T) {
	t.Parallel()

	both := ForgeNotice{Number: 460, URL: "https://forge.invalid/pull/460", HeadBranch: "yoyodyne/yoyodyne-ifd-300/bbbbbbbb", BaseBranch: "main", WorkItemID: "yoyodyne-ifd.300", ItemClosed: true, Contained: true}
	finding := both.Finding()
	if finding.Disposition != sweep.DispositionLeft {
		t.Errorf("disposition = %q, want left", finding.Disposition)
	}
	for _, want := range []string{"pull request #460", "https://forge.invalid/pull/460", "work item yoyodyne-ifd.300 is closed", "yoyodyne/yoyodyne-ifd-300/bbbbbbbb is already contained in main"} {
		if !strings.Contains(finding.Issue, want) {
			t.Errorf("issue = %q, want it to carry %q", finding.Issue, want)
		}
	}
	if err := finding.Validate(); err != nil {
		t.Errorf("the finding does not meet the sweep contract: %v", err)
	}
	unowned := ForgeNotice{Number: 9, HeadBranch: "feature/by-hand", BaseBranch: "main", Contained: true}.Finding()
	if !strings.Contains(unowned.Issue, "no work item") {
		t.Errorf("issue = %q, want it to say no item could be named", unowned.Issue)
	}
}

// A problem too long for the record is stored cut on a rune boundary and marked
// as cut, never with half a rune at its end.
func TestASettledProblemIsCutOnARuneBoundary(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, time.Now()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	problem := "x" + strings.Repeat("é", MaxSweepTextBytes)
	settled, err := store.Settle(context.Background(), "a-sweep", problem)
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if !utf8.ValidString(settled.Problem) || len(settled.Problem) > MaxSweepTextBytes || !strings.HasSuffix(settled.Problem, " […]") {
		t.Fatalf("settled problem is %d bytes, valid UTF-8 %t, ending %q; want valid text within %d bytes marked as cut",
			len(settled.Problem), utf8.ValidString(settled.Problem), settled.Problem[len(settled.Problem)-8:], MaxSweepTextBytes)
	}
}

// A missed pass is marked with the trigger that owed it and how it was missed,
// both from their vocabularies, and carries no role account: a pass that ended
// in one completed. A summoned claim says so until the cadence claims again, and
// a claim reads as settled only once its ending is written.
func TestAMissedPassIsMarkedAndItsClaimSaysHowItWasTaken(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	missed := Sweep{
		SchemaVersion: SweepSchemaVersion, ProductID: "example", Task: "reliability-pm", Role: "program-manager",
		StartedAt: start, EndedAt: start.Add(time.Hour), Problem: "cancelled before it completed",
		Missed: &MissedPass{Trigger: PassTriggerEvents, How: MissCancelled},
	}
	if err := missed.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for name, spoil := range map[string]func(*Sweep){
		"unknown trigger": func(s *Sweep) { s.Missed = &MissedPass{Trigger: "whim", How: MissUnfired} },
		"unknown how":     func(s *Sweep) { s.Missed = &MissedPass{Trigger: PassTriggerSchedule, How: "lost"} },
		"an account":      func(s *Sweep) { s.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: "done"} },
	} {
		spoiled := missed
		spoil(&spoiled)
		if err := spoiled.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want the missed pass refused", name)
		}
	}

	store := newSweepStore(t)
	summoned, err := store.Summon(context.Background(), "reliability-pm", start)
	if err != nil || !summoned.Summoned || summoned.Settled() {
		t.Fatalf("Summon() = %+v, %v; want an unsettled summoned claim", summoned, err)
	}
	settled, err := store.Settle(context.Background(), "reliability-pm", "")
	if err != nil || !settled.Settled() {
		t.Fatalf("Settle() = %+v, %v; want the claim settled", settled, err)
	}
	claimed, err := store.Claim(context.Background(), "reliability-pm", time.Hour, start.Add(time.Hour))
	if err != nil || claimed.Summoned {
		t.Fatalf("Claim() = %+v, %v; want the cadence's claim not summoned", claimed, err)
	}
}

func TestAConversationHeldMissMayCarryOnlyTheHarnessForgeFindings(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	notice := ForgeNotice{Number: 445, HeadBranch: "yoyodyne/yoyodyne-ifd-283/aaaaaaaa", ItemClosed: true, WorkItemID: "yoyodyne-ifd.283"}
	missed := Sweep{
		SchemaVersion: SweepSchemaVersion, ProductID: "example", Task: "a-sweep", Role: "development-manager",
		StartedAt: start, EndedAt: start.Add(time.Minute), Problem: "the conversation wait ended before its first turn",
		Missed:       &MissedPass{Trigger: PassTriggerSchedule, How: MissConversationHeld},
		Result:       &sweep.Result{Status: sweep.StatusComplete, Summary: "The harness's own reading of the forge.", Findings: []sweep.Finding{notice.Finding()}},
		PullRequests: []ForgeNotice{notice},
	}
	if err := missed.Validate(); err != nil {
		t.Fatalf("the independent forge findings could not be recorded with a conversation miss: %v", err)
	}
	for name, spoil := range map[string]func(*Sweep){
		"role took a turn":    func(s *Sweep) { s.Turns = 1 },
		"another role":        func(s *Sweep) { s.Role = "program-manager" },
		"another miss":        func(s *Sweep) { s.Missed = &MissedPass{Trigger: PassTriggerSchedule, How: MissCancelled} },
		"notice not recorded": func(s *Sweep) { s.PullRequests = nil },
		"role finding":        func(s *Sweep) { s.Result.Findings[0].Issue = "The role found another problem." },
		"role question":       func(s *Sweep) { s.Result.Questions = []string{"Should this request be closed?"} },
		"work filed":          func(s *Sweep) { s.Result.Findings[0].Filed = []string{"yoyodyne-ifd.283"} },
	} {
		t.Run(name, func(t *testing.T) {
			spoiled := missed
			account := *missed.Result
			account.Findings = append([]sweep.Finding(nil), missed.Result.Findings...)
			spoiled.Result = &account
			spoil(&spoiled)
			if err := spoiled.Validate(); err == nil {
				t.Fatal("a missed pass accepted an account beyond the independent forge reading")
			}
		})
	}
}

// A pass's traces are kept on its record and read back, and a record that
// marks a pass untraced while naming a trace it left is refused: the flag is
// one somebody acts on, and it must not be able to disagree with the record.
func TestAnUntracedPassIsRecordedAndCannotClaimATrace(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	account := &sweep.Result{Status: sweep.StatusComplete, Summary: "one", Findings: []sweep.Finding{{Issue: "x", Disposition: sweep.DispositionLeft}}}
	store := newSweepStore(t)
	untraced := Sweep{Task: "a-sweep", Role: "development-manager", StartedAt: at, EndedAt: at, Turns: 1, Result: account, Untraced: true}
	if err := store.Append(untraced); err != nil {
		t.Fatalf("Append() of an untraced pass error = %v", err)
	}
	traced := Sweep{Task: "a-sweep", Role: "development-manager", StartedAt: at, EndedAt: at, Turns: 1, Result: account,
		ReportsFiled: 1, Admitted: []string{"yoyodyne-ifd.500"}}
	if err := store.Append(traced); err != nil {
		t.Fatalf("Append() of a traced pass error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 2 || !recorded[0].Untraced || recorded[0].LeftATrace() || !recorded[1].LeftATrace() || recorded[1].Admitted[0] != "yoyodyne-ifd.500" {
		t.Fatalf("List() = %+v, %v; want the untraced pass and the traced one as written", recorded, err)
	}
	for name, bad := range map[string]Sweep{
		"untraced with a report":   {ReportsFiled: 1},
		"untraced with admissions": {Admitted: []string{"yoyodyne-ifd.500"}},
		"untraced with a memory":   {Saved: []SavedWrite{{Kind: SavedMemory, Action: "remember", Memory: "m", Revision: 1}}},
	} {
		bad.Task, bad.Role, bad.StartedAt, bad.EndedAt, bad.Turns, bad.Result, bad.Untraced = "a-sweep", "development-manager", at, at, 1, account, true
		if err := store.Append(bad); err == nil || !strings.Contains(err.Error(), "untraced") {
			t.Errorf("%s: Append() error = %v, want the record refused", name, err)
		}
	}
	if err := store.Append(Sweep{Task: "a-sweep", Role: "development-manager", StartedAt: at, EndedAt: at, Turns: 1, Result: account, Admitted: []string{" "}}); err == nil {
		t.Error("a record admitting an unnamed item was kept")
	}
}

// The harness's own maintenance pass names no role and carries its steps; a
// record naming neither, or both, says nothing coherent about whose pass it
// was, and a step that was skipped or failed has to say why.
func TestAHarnessPassCarriesStepsInsteadOfARole(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	pass := Sweep{SchemaVersion: SweepSchemaVersion, ProductID: "example", Task: "maintenance", StartedAt: at, EndedAt: at,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "1 step(s) ran, 1 skipped, 0 failed"},
		Steps: []SweepStep{
			{Name: "reconcile", Outcome: StepRan, Detail: "yoyo reconcile exited 0"},
			{Name: "redeploy", Outcome: StepSkipped, Detail: "every part is on the deployed build"},
		}}
	store := newSweepStore(t)
	if err := store.Append(pass); err != nil {
		t.Fatalf("Append() of a harness pass error = %v", err)
	}
	read, _, err := store.List()
	if err != nil || len(read) != 1 || !read[0].HarnessPass() || len(read[0].Steps) != 2 {
		t.Fatalf("List() = %+v, %v, want the harness pass with its steps", read, err)
	}

	neither := pass
	neither.Steps = nil
	both := pass
	both.Role = "development-manager"
	unexplained := pass
	unexplained.Steps = []SweepStep{{Name: "redeploy", Outcome: StepSkipped}}
	for name, invalid := range map[string]Sweep{"neither": neither, "both": both, "unexplained": unexplained} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: a harness pass record validated: %+v", name, invalid)
		}
	}
}

// A pass marks a passed-over note it did not write as a repeat or as one that
// could not be written, never both, and never on an item it took.
func TestAPassedOverNoteIsMarkedRepeatedOrUnwrittenOnlyOnALeftItem(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	pass := Sweep{
		SchemaVersion: SweepSchemaVersion, ProductID: "example", Task: "architect-pass", Role: "architect",
		StartedAt: start, EndedAt: start.Add(time.Minute), Turns: 1,
		Result:    &sweep.Result{Status: sweep.StatusComplete, Summary: "done"},
		Delivered: []DeliveredWork{{ID: "x-1", Reason: "spread over replies", NoteRepeated: true}},
	}
	if err := pass.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for name, spoil := range map[string]func(*DeliveredWork){
		"both":  func(d *DeliveredWork) { d.NoteUnwritten = true },
		"taken": func(d *DeliveredWork) { d.Taken, d.Reason = true, "" },
	} {
		spoiled := pass
		spoiled.Delivered = []DeliveredWork{pass.Delivered[0]}
		spoil(&spoiled.Delivered[0])
		if err := spoiled.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want the record refused", name)
		}
	}
}
