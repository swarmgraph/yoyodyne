package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The one call the operator asked for: what shipped lately and what it took,
// most recent first, with the price, the wall clock, and the paused time each
// stated, totals under them, and an unknown said to be unknown.
func TestStatusListsWhatShippedWithItsPriceAndWallClock(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// records it reads are written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing shipped is an answer rather than an empty table.
	stdout, stderr, code := runCLI(t, "status", "--shipped", "--config", configPath)
	if code != 0 {
		t.Fatalf("status --shipped code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "nothing has shipped") {
		t.Fatalf("stdout = %q", stdout)
	}

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	claimed := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	// A rejected attempt that paused for an hour of the night, then the promotion
	// three hours after the first claim: the item's price is both runs and its
	// elapsed time spans both.
	rejected := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", claimed)
	rejected.Phase = runstate.PhaseReviewing
	rejected.UsageLimitPausedSeconds = 3600
	rejected.ProviderSessionID = "session-rejected"
	saveRun(t, store, rejected)
	appendRunCost(t, store, rejected.RunID, 8.91)
	shipping := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.2.7", claimed.Add(2*time.Hour))
	shipping.WorkItemTitle = "Resume an interrupted run"
	shipped := claimed.Add(3 * time.Hour)
	shipping.CompletedAt = &shipped
	shippedRun(&shipping)
	saveRun(t, store, shipping)
	appendRunCost(t, store, shipping.RunID, 19.02)
	// An item that shipped a day later leads the listing, and its one run has no
	// surviving log, so its price is a floor rather than nothing.
	unpriced := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.41", claimed.Add(24*time.Hour))
	laterShipped := claimed.Add(24*time.Hour + 20*time.Minute)
	unpriced.CompletedAt = &laterShipped
	shippedRun(&unpriced)
	saveRun(t, store, unpriced)
	// A run that succeeded without promoting anything is not a shipment.
	evidence := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.99", claimed.Add(48*time.Hour))
	evidence.Phase = runstate.PhaseComplete
	saveRun(t, store, evidence)

	stdout, stderr, code = runCLI(t, "status", "--shipped", "--config", configPath)
	if code != 0 {
		t.Fatalf("status --shipped code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"item",
		"elapsed",
		"paused",
		"yoyodyne-ifd.41",
		"yoyodyne-ifd.2.7",
		"title unavailable (yoyodyne-ifd.2.7)",
		// Both runs priced together, and the hour parked stated beside the three
		// hours elapsed rather than netted out of it.
		"$27.93",
		"3h00m",
		"1h00m",
		// The floor on the item nothing survives to price, and on the total.
		"≥ $0.00",
		"≥ $27.93",
		"TOTAL (2 of 2)",
		"title unavailable (yoyodyne-ifd.41)",
		"1 run(s) have no surviving record to price",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "yoyodyne-ifd.99") {
		t.Fatalf("an item that promoted nothing was listed as shipped: %q", stdout)
	}
	// Most recent promotion first.
	if strings.Index(stdout, "yoyodyne-ifd.41") > strings.Index(stdout, "yoyodyne-ifd.2.7") {
		t.Fatalf("the ledger is not most recent first: %q", stdout)
	}

	// The count bounds the listing and the listing says what it was bounded from.
	stdout, stderr, code = runCLI(t, "status", "--shipped", "1", "--config", configPath)
	if code != 0 {
		t.Fatalf("status --shipped 1 code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "TOTAL (1 of 2)") || strings.Contains(stdout, "yoyodyne-ifd.2.7") {
		t.Fatalf("stdout = %q, want one of two listed", stdout)
	}

	// A script reads the same ledger, with the price join whole under each item.
	stdout, stderr, code = runCLI(t, "status", "--shipped", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("status --shipped --json code = %d, stderr = %q", code, stderr)
	}
	var decoded shippedOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Ledger.Shipped != 2 || len(decoded.Ledger.Items) != 2 {
		t.Fatalf("decoded = %#v", decoded)
	}
	item := decoded.Ledger.Items[1]
	if item.WorkItemID != "yoyodyne-ifd.2.7" || item.ElapsedSeconds != 3*3600 || item.Price.TotalUSD != 27.93 {
		t.Fatalf("decoded item = %#v", item)
	}
	if item.Price.Phases.Waits.UsageLimitSeconds != 3600 {
		t.Fatalf("decoded waits = %#v, want the rejected attempt's hour", item.Price.Phases.Waits)
	}
	if decoded.Ledger.Items[0].Price.UnknownRuns != 1 {
		t.Fatalf("decoded unpriced item = %#v, want its run counted as unknown", decoded.Ledger.Items[0])
	}
}

// The ledger is a listing of the run records, so it takes the options a listing
// takes and refuses the ones that shape a stream — and it is one question,
// asked apart from the others.
func TestStatusShippedRefusesWhatItCannotHonor(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--shipped", "--spend"}, "different questions"},
		{[]string{"status", "--shipped", "--failed"}, "--failed selects among the recorded runs"},
		{[]string{"status", "--shipped", "--lines", "5"}, "--lines replays"},
		{[]string{"status", "--shipped", "--kind", "runs"}, "--kind narrows"},
		{[]string{"status", "--shipped", "--raw"}, "--raw and --all"},
		{[]string{"status", "--shipped", "ten"}, "not a count of shipped items"},
		{[]string{"status", "--shipped", "--limit", "3", "5"}, "two counts"},
	} {
		_, stderr, code := runCLI(t, refused.args...)
		if code != 2 {
			t.Fatalf("%v: code = %d, want 2; stderr = %q", refused.args, code, stderr)
		}
		if !strings.Contains(stderr, refused.want) {
			t.Fatalf("%v: stderr = %q, want it to contain %q", refused.args, stderr, refused.want)
		}
	}
	for _, accepted := range []struct {
		named      string
		limit      int
		limitGiven bool
		want       int
	}{
		{"", 20, false, defaultShippedItems},
		{"", 3, true, 3},
		{"0", 20, false, 0},
		{"5", 5, true, 5},
	} {
		count, err := shippedCount(accepted.named, accepted.limit, accepted.limitGiven)
		if err != nil || count != accepted.want {
			t.Fatalf("shippedCount(%q, %d, %v) = %d, %v; want %d", accepted.named, accepted.limit, accepted.limitGiven, count, err, accepted.want)
		}
	}
}

// shippedRun makes a recorded run one that promoted its work, with the
// approval and the two independent invocations the record holds a promotion
// to, so the fixture is a run the harness could actually have written.
func shippedRun(state *runstate.State) {
	state.Phase = runstate.PhaseCleaningUp
	state.WorktreePath = "/state/worktrees/" + state.RunID
	state.Branch = "yoyodyne/" + state.WorkItemID + "/" + state.RunID
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.ProviderSessionID = "session-developer"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "session-reviewer"
	state.ReviewModel = "opus"
	state.ReviewDecision = runstate.ReviewApprove
	state.Integration = &runstate.Integration{
		TargetBranch:         "main",
		SourceCommit:         strings.Repeat("b", 40),
		TargetCommit:         strings.Repeat("b", 40),
		PreviousTargetCommit: strings.Repeat("a", 40),
	}
}
