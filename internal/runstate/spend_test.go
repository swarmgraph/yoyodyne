package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

func TestRecordedSpendsSurviveTheProcessThatMadeThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestSpendStore(t, root)
	development := testSpend(SpendPhaseDevelopment, 4.25)
	review := testSpend(SpendPhaseReview, 0.96)
	review.Role = "reviewer"
	review.Agent = "reviewer"
	for _, line := range []Spend{development, review} {
		if err := store.Append(line); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	// A second process reads what the first wrote, in the order it was written: a
	// spend outlives the run that made it, so the log is what somebody adds up
	// long afterwards rather than anything held in memory.
	reloaded, err := newTestSpendStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 2 {
		t.Fatalf("List() = %#v", reloaded)
	}
	if reloaded[0].Phase != SpendPhaseDevelopment || reloaded[1].Phase != SpendPhaseReview {
		t.Fatalf("spends came back out of order: %s then %s", reloaded[0].Phase, reloaded[1].Phase)
	}
	if reloaded[0].AmountUSD != 4.25 || !reloaded[0].Known() {
		t.Fatalf("the amount did not survive intact: %#v", reloaded[0])
	}
	if reloaded[0].AccountAlias != development.AccountAlias || reloaded[0].ConfigRevision != development.ConfigRevision {
		t.Fatalf("the attribution did not survive intact: %#v", reloaded[0])
	}
	// Which harness made the invocation travels with the rest of it. It is the
	// field that separates a defect in the code from a deployment that never
	// happened, and a log that could not answer it is what left a week of stale
	// dispatches undiagnosable.
	if reloaded[0].Build != development.Build {
		t.Fatalf("recorded build = %q, want %q", reloaded[0].Build, development.Build)
	}
	if !reloaded[0].At.Equal(development.At) {
		t.Fatalf("recorded time = %s, want %s", reloaded[0].At, development.At)
	}
}

func TestAnUnknownSpendIsRecordedAsUnknownRatherThanAsNothing(t *testing.T) {
	t.Parallel()

	store := newTestSpendStore(t, t.TempDir())
	unknown := testSpend(SpendPhaseDevelopment, 0)
	unknown.Classification = SpendUnknown
	unknown.AmountUSD = 0
	unknown.Unknown = "the provider ended the invocation without reporting what it cost"
	if err := store.Append(unknown); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	// The amount is encoded even on an unknown line. A key left out reads to
	// whatever consumes the log as an amount of nothing, which is the one thing an
	// unknown spend must never be mistaken for.
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), `"amount_usd":0`) {
		t.Fatalf("the encoded line omits the amount: %s", raw)
	}
	if !strings.Contains(string(raw), `"classification":"unknown"`) {
		t.Fatalf("the encoded line does not classify the amount: %s", raw)
	}

	reloaded, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].Known() {
		t.Fatalf("List() = %#v, want one unknown spend", reloaded)
	}
}

func TestSpendsAreRefusedWhenNobodyCouldAttributeThem(t *testing.T) {
	t.Parallel()

	store := newTestSpendStore(t, t.TempDir())
	// Each of these spoils one thing about a line the store would otherwise
	// accept. The base line carries a known amount of nothing, which is legal --
	// a provider may report an invocation as free -- so what each case breaks is
	// the only thing wrong with it.
	refused := map[string]func(*Spend){
		"unknown with no reason":          func(s *Spend) { s.Classification = SpendUnknown },
		"a known amount below zero":       func(s *Spend) { s.AmountUSD = -1 },
		"a classification nothing writes": func(s *Spend) { s.Classification = "estimated" },
		"an account nothing configured":   func(s *Spend) { s.AccountAlias = "Work Account" },
		"a revision of no digest":         func(s *Spend) { s.ConfigRevision = "yesterday's" },
		"a build that is not a revision":  func(s *Spend) { s.Build = "the one from Tuesday" },
		"a phase nothing runs":            func(s *Spend) { s.Phase = "integrating" },
		"a role nobody fills":             func(s *Spend) { s.Role = "auditor" },
		"a backend nothing could name":    func(s *Spend) { s.Backend = "hand written" },
		"nothing to charge it to":         func(s *Spend) { s.RunID = "" },
		"two things to charge it to":      func(s *Spend) { s.ConversationID = "conversation-1" },
		"another product's spend":         func(s *Spend) { s.ProductID = "elsewhere" },
	}
	for name, spoil := range refused {
		line := testSpend(SpendPhaseDevelopment, 0)
		spoil(&line)
		if err := store.Append(line); err == nil {
			t.Errorf("Append() accepted %s", name)
		}
	}

	// An amount nobody knows never carries a number, whatever it says about why:
	// a total that added it in would be wrong by however much was really spent.
	numbered := testSpend(SpendPhaseDevelopment, 1.5)
	numbered.Classification = SpendUnknown
	numbered.Unknown = "the provider said nothing"
	if err := store.Append(numbered); err == nil {
		t.Error("Append() accepted an unknown amount carrying a number")
	}

	// A work item belongs to a run and to nothing else, so an exchange round that
	// claimed to have served one is refused rather than putting money on an item
	// nothing was ever run for.
	misattributed := testSpend(SpendPhaseExchange, 0.25)
	misattributed.RunID = ""
	misattributed.ExchangeID = "exchange-0123456789abcdef0123456789abcdef"
	if err := store.Append(misattributed); err == nil {
		t.Error("Append() accepted a work item with no run behind it")
	}
}

// A branch review is charged to the review itself rather than to a run. The
// store takes that as one subject like any other, and refuses a line naming a
// branch review and a run both — a reader joining these lines to run records
// would otherwise have to decide which of the two the line meant.
func TestABranchReviewIsItsOwnSubjectRatherThanARun(t *testing.T) {
	t.Parallel()

	store := newTestSpendStore(t, t.TempDir())
	line := testSpend(SpendPhaseReview, 1.25)
	line.Role = "reviewer"
	line.Agent = "reviewer"
	line.RunID = ""
	line.WorkItemID = ""
	line.BranchReviewID = "review-0123456789abcdef0123456789abcdef"
	if err := store.Append(line); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	reloaded, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].BranchReviewID != line.BranchReviewID || reloaded[0].RunID != "" {
		t.Fatalf("List() = %#v, want one line naming the branch review and no run", reloaded)
	}

	both := line
	both.RunID = "run-0123456789abcdef0123456789abcdef"
	if err := store.Append(both); err == nil {
		t.Error("Append() accepted a line naming both a run and a branch review")
	}
	// A branch review serves no assigned work either, so it may not name one.
	claiming := line
	claiming.WorkItemID = "yoyodyne-ifd.182"
	if err := store.Append(claiming); err == nil {
		t.Error("Append() accepted a branch review claiming a work item")
	}
}

func TestListingSpendsBeforeAnythingWasSpentIsNotAFailure(t *testing.T) {
	t.Parallel()

	lines, err := newTestSpendStore(t, t.TempDir()).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("List() = %#v, want nothing", lines)
	}
}

func TestASpendLogLineThatCannotBeReadFailsTheReadRatherThanBeingSkipped(t *testing.T) {
	t.Parallel()

	store := newTestSpendStore(t, t.TempDir())
	if err := store.Append(testSpend(SpendPhaseConversation, 0.0125)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{\"schema_version\":1}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	// A log that quietly drops what it cannot parse is one nobody can trust to be
	// complete, which for money is the whole of its value.
	if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "decode spend log") {
		t.Fatalf("List() error = %v, want a decode failure", err)
	}
}

func TestTheSpendLogSitsBesideTheRunsRatherThanAmongThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestSpendStore(t, root)
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if strings.HasPrefix(store.Path(), runs.Root()+string(filepath.Separator)) {
		t.Fatalf("the spend log is inside the run directory: %s", store.Path())
	}
	if filepath.Dir(store.Path()) != home.ProductDirectory(root, "yoyodyne") {
		t.Fatalf("spend log path = %s", store.Path())
	}
}

func newTestSpendStore(t *testing.T, root string) *SpendStore {
	t.Helper()

	store, err := NewSpendStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSpendStore() error = %v", err)
	}
	return store
}

// testSpend is one line the store accepts, which the tests above break one way
// at a time.
func testSpend(phase SpendPhase, amount float64) Spend {
	line := Spend{
		SchemaVersion:  SpendSchemaVersion,
		ProductID:      "yoyodyne",
		At:             time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC),
		Role:           "developer",
		Agent:          "developer",
		Phase:          phase,
		Classification: SpendKnown,
		AmountUSD:      amount,
		AccountAlias:   "default",
		ConfigRevision: "cfg-0123456789ab",
		RunID:          "run-0123456789abcdef0123456789abcdef",
		WorkItemID:     "yoyodyne-ifd.182",
		Backend:        "claude-code",
		Model:          "opus",
		ResolvedModel:  "claude-opus-4-1-20250805",
		SessionID:      "session-1",
		Build:          "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900",
	}
	if phase == SpendPhaseConversation {
		line.Role = "product-manager"
		line.Agent = "product-manager"
		line.RunID = ""
		line.WorkItemID = ""
		line.ConversationID = "conversation-0123456789abcdef0123456789abcdef"
	}
	return line
}

// The log outlived the defect, so it has to be readable across it. Lines
// written before an amount was an invocation's own cost carry the figure the
// provider reported, which on a resumed session is the session's running total;
// they are re-derived on the way out by the rule a line is now written by, from
// the session identifier every line has always carried, and the figure as
// recorded is kept beside the correction so it can be checked.
//
// Nothing on disk changes. The file is the evidence of what was reported, and a
// log whose lines were edited afterwards would be a worse record of the money
// than one that was wrong in a way every reader corrects.
func TestListRederivesLinesRecordedBeforeAnAmountWasAnInvocationsOwnCost(t *testing.T) {
	t.Parallel()

	store, err := NewSpendStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewSpendStore() error = %v", err)
	}
	// Three turns of one resumed session, recorded the way the harness recorded
	// them before this: each line carrying what the provider reported.
	for _, reported := range []float64{2.50, 6.25, 9.00} {
		line := testSpend(SpendPhaseConversation, reported)
		line.SessionID = "session-resumed"
		line.RunID = ""
		line.WorkItemID = ""
		line.ConversationID = "chat-0123456789abcdef0123456789abcdef"
		if err := store.Append(line); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	// An invocation nobody was told the price of, in the same session. It must not
	// advance the session: a zero standing for "nobody knows" read as a total
	// would charge the next turn the whole session over again.
	unknown := testSpend(SpendPhaseConversation, 0)
	unknown.SessionID = "session-resumed"
	unknown.RunID = ""
	unknown.WorkItemID = ""
	unknown.ConversationID = "chat-0123456789abcdef0123456789abcdef"
	unknown.Classification = SpendUnknown
	unknown.Unknown = "the provider ended the invocation without reporting what it cost"
	if err := store.Append(unknown); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	final := testSpend(SpendPhaseConversation, 10.00)
	final.SessionID = "session-resumed"
	final.RunID = ""
	final.WorkItemID = ""
	final.ConversationID = "chat-0123456789abcdef0123456789abcdef"
	if err := store.Append(final); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	lines, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lines) != 5 {
		t.Fatalf("read %d line(s), want every line recorded", len(lines))
	}
	var total float64
	for _, line := range lines {
		total += line.AmountUSD
	}
	if total != 10.00 {
		t.Fatalf("the log reads as %v, want the session's final total of 10; "+
			"summing what each line recorded would have made it 27.75", total)
	}
	for index, want := range []float64{2.50, 3.75, 2.75, 0, 1.00} {
		if lines[index].AmountUSD != want {
			t.Fatalf("line %d reads as %v, want %v", index, lines[index].AmountUSD, want)
		}
	}
	// What was recorded is kept beside what it now reads as, on every line the
	// correction touched.
	if lines[1].ReportedTotalUSD != 6.25 || lines[2].ReportedTotalUSD != 9.00 || lines[4].ReportedTotalUSD != 10.00 {
		t.Fatalf("the figures as recorded were not kept: %#v", lines)
	}
	if lines[0].ReportedTotalUSD != 0 || lines[3].ReportedTotalUSD != 0 {
		t.Fatalf("a line nothing corrected reports a total it did not have: %#v", lines)
	}
	// And the file itself still says what was written, which is what makes the
	// correction auditable rather than something a reader has to trust.
	recorded, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(recorded), `"amount_usd":9`) {
		t.Fatalf("the log on disk was rewritten rather than read through the correction:\n%s", recorded)
	}
}

// A log written across the change is one log. A line corrected when it was
// written says what the provider reported, and a line written before this did
// not exist says it in its amount; the session's next invocation is priced
// against whichever of the two the line carries, so the two halves compose
// rather than each starting the session again.
func TestASessionRecordedAcrossTheCorrectionIsPricedAsOneSession(t *testing.T) {
	t.Parallel()

	store, err := NewSpendStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewSpendStore() error = %v", err)
	}
	before := testSpend(SpendPhaseDevelopment, 4.00)
	before.SessionID = "session-developer"
	if err := store.Append(before); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	// What the meter writes now: the increment, with the reported total beside it.
	after := testSpend(SpendPhaseRepair, 2.50)
	after.SessionID = "session-developer"
	after.ReportedTotalUSD = 6.50
	if err := store.Append(after); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	total, found, err := store.ReportedSessionTotal("session-developer")
	if err != nil || !found || total != 6.50 {
		t.Fatalf("ReportedSessionTotal() = %v, %v, %v; want the session last reported at 6.50", total, found, err)
	}
	unseen, found, err := store.ReportedSessionTotal("session-nothing-recorded")
	if err != nil || found || unseen != 0 {
		t.Fatalf("ReportedSessionTotal() = %v, %v, %v; want a session nothing has recorded", unseen, found, err)
	}

	lines, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if lines[0].AmountUSD != 4.00 || lines[1].AmountUSD != 2.50 {
		t.Fatalf("the two halves did not compose: %#v", lines)
	}
}

// The rule itself, stated over the cases the recorded history actually holds. A
// figure that rose is a running total and the amount is what it moved by; a
// figure that did not is a beginning, which is the session's first invocation
// and also a provider whose total restarted or that reports each invocation's
// own cost. Nothing it produces is ever negative.
func TestOwnCostIsTheIncrementOverATotalThatRose(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		reported float64
		previous float64
		seen     bool
		want     float64
	}{
		{"a session's first invocation records the whole", 2.5, 0, false, 2.5},
		{"a resumed session records what the total moved by", 6.25, 2.5, true, 3.75},
		{"a total that did not move records nothing", 6.25, 6.25, true, 0},
		{"a figure below the one before it is a beginning", 0.6, 8.51, true, 0.6},
	} {
		if got := OwnCostUSD(tc.reported, tc.previous, tc.seen); got != tc.want {
			t.Fatalf("%s: OwnCostUSD(%v, %v, %v) = %v, want %v", tc.name, tc.reported, tc.previous, tc.seen, got, tc.want)
		}
	}
}
