package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func tbuCommit(c string) string { return strings.Repeat(c, 40) }

func tbuAttempt() TeamBranchUpdateAttempt {
	return TeamBranchUpdateAttempt{TeamCommit: tbuCommit("a"), TargetBase: tbuCommit("b"), StartedAt: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
}

func tbuRecord(target string) TeamBranchUpdate {
	return TeamBranchUpdate{SchemaVersion: TeamBranchUpdateSchemaVersion, TargetBranch: target, UpstreamBranch: "main"}
}

func newTBUStore(t *testing.T, root string) *TeamBranchUpdateStore {
	t.Helper()
	store, err := NewTeamBranchUpdateStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTeamBranchUpdateStore() error = %v", err)
	}
	store.leaseWait = 0
	return store
}

func TestTeamBranchUpdateRoundTrip(t *testing.T) {
	store := newTBUStore(t, t.TempDir())
	if _, found, err := store.Load("staging"); err != nil || found {
		t.Fatalf("Load() before any save = %t, %v; want none", found, err)
	}
	at := time.Date(2026, 11, 10, 9, 12, 0, 0, time.UTC)
	record := tbuRecord("staging")
	record.CheckedAt = &at
	pending := tbuAttempt()
	pending.Candidate = tbuCommit("c")
	pending.Checks = []TeamBranchUpdateCheck{{Command: "make check", Passed: true}}
	pending.ChecksFinishedAt = &at
	pending.Review = &TeamBranchUpdateReview{Decision: "approve", Summary: strings.Repeat("x", 3*MaxRecordedTextBytes), FinishedAt: at}
	record.Pending = &pending
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, found, err := store.Load("staging")
	if err != nil || !found {
		t.Fatalf("Load() = %t, %v", found, err)
	}
	if got.SchemaVersion != TeamBranchUpdateSchemaVersion || got.Pending == nil || got.Pending.Candidate != tbuCommit("c") || !got.Pending.ChecksPassed() || !got.Pending.Approved() {
		t.Fatalf("Load() = %#v; want what was saved", got)
	}
	if len(got.Pending.Review.Summary) > MaxRecordedTextBytes || !strings.Contains(got.Pending.Review.Summary, "truncated") {
		t.Fatalf("review summary is %d bytes; want it bounded and marked", len(got.Pending.Review.Summary))
	}
}

func TestTeamBranchUpdateValidationRefusals(t *testing.T) {
	landedAt := time.Now()
	bad := tbuAttempt()
	bad.TeamCommit = "nope"
	noCommit := tbuAttempt()
	noCommit.LandedAt = &landedAt
	over := make([]TeamBranchUpdateAttempt, MaxTeamBranchUpdatesKept+1)
	for i := range over {
		over[i] = tbuAttempt()
		over[i].LandedAt = &landedAt
		over[i].LandedCommit = tbuCommit("d")
	}
	hasLanded := tbuAttempt()
	hasLanded.LandedAt = &landedAt
	for name, mutate := range map[string]func(*TeamBranchUpdate){
		"bad commit":            func(u *TeamBranchUpdate) { u.Pending = &bad },
		"landed without commit": func(u *TeamBranchUpdate) { u.Landed = []TeamBranchUpdateAttempt{noCommit} },
		"too many landed":       func(u *TeamBranchUpdate) { u.Landed = over },
		"pending has landed":    func(u *TeamBranchUpdate) { u.Pending = &hasLanded },
		"bad branch":            func(u *TeamBranchUpdate) { u.TargetBranch = "bad branch" },
		"schema":                func(u *TeamBranchUpdate) { u.SchemaVersion = 7 },
	} {
		record := tbuRecord("staging")
		mutate(&record)
		if err := record.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted %#v", name, record)
		}
	}
	if err := tbuRecord("staging").Validate(); err != nil {
		t.Fatalf("Validate() of an empty record = %v", err)
	}
	if err := newTBUStore(t, t.TempDir()).Save(TeamBranchUpdate{TargetBranch: "staging", UpstreamBranch: "main", Pending: &bad}); err == nil {
		t.Fatal("Save() accepted a bad commit")
	}
}

func TestTeamBranchUpdateLandBoundsHistory(t *testing.T) {
	record := tbuRecord("staging")
	for i := 0; i < MaxTeamBranchUpdatesKept+5; i++ {
		pending := tbuAttempt()
		pending.PullRequest = i + 1
		record.Pending = &pending
		record.Land(time.Date(2026, 11, 10, 9, i, 0, 0, time.UTC), tbuCommit("e"))
		if record.Pending != nil {
			t.Fatal("Land() left the update pending")
		}
	}
	if len(record.Landed) != MaxTeamBranchUpdatesKept || record.Landed[0].PullRequest != 6 || record.Landed[len(record.Landed)-1].PullRequest != MaxTeamBranchUpdatesKept+5 {
		t.Fatalf("Landed has %d updates from #%d; want the newest %d kept", len(record.Landed), record.Landed[0].PullRequest, MaxTeamBranchUpdatesKept)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestTeamBranchUpdateLease(t *testing.T) {
	root := t.TempDir()
	first, held, err := newTBUStore(t, root).LeaseUpdater(context.Background(), "staging")
	if err != nil || !held {
		t.Fatalf("LeaseUpdater() = %t, %v; want held", held, err)
	}
	if _, held, err := newTBUStore(t, root).LeaseUpdater(context.Background(), "staging"); err != nil || held {
		t.Fatalf("second LeaseUpdater() = %t, %v; want not held", held, err)
	}
	other, held, err := newTBUStore(t, root).LeaseUpdater(context.Background(), "release/1.2")
	if err != nil || !held {
		t.Fatalf("LeaseUpdater(other branch) = %t, %v; want held", held, err)
	}
	_ = other.Release()
	if err := first.Release(); err != nil {
		t.Fatalf("Release() = %v", err)
	}
	again, held, err := newTBUStore(t, root).LeaseUpdater(context.Background(), "staging")
	if err != nil || !held {
		t.Fatalf("LeaseUpdater() after release = %t, %v; want held", held, err)
	}
	_ = again.Release()
}

func TestTeamBranchUpdateSays(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 12, 0, 0, time.UTC)
	stamp := at.Local().Format("2006-01-02 15:04 MST")

	landed := tbuRecord("staging")
	pending := tbuAttempt()
	landed.Pending = &pending
	landed.Land(at, tbuCommit("c"))
	if got, want := landed.Says(), []string{"staging was last brought up to date with main at " + stamp + " (merge " + strings.Repeat("c", 12) + ")."}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("landed Says() = %q; want %q", got, want)
	}

	current := tbuRecord("staging")
	current.CurrentAt = &at
	if got := current.Says(); len(got) != 1 || got[0] != "staging already carried main as of "+stamp+"." {
		t.Errorf("current Says() = %q", got)
	}

	conflict := tbuRecord("staging")
	c := tbuAttempt()
	c.Conflict = &TeamBranchUpdateConflict{Paths: []string{"a.go", "b.go"}, FoundAt: at}
	conflict.Pending = &c
	want := "Bringing staging up to date with main is waiting: merging main at " + strings.Repeat("a", 12) + " conflicts in a.go, b.go"
	if got := conflict.Says(); len(got) != 1 || got[0] != want+"." {
		t.Errorf("unreported conflict Says() = %q", got)
	}
	c.ReportedAt = &at
	if got := conflict.Says(); len(got) != 1 || got[0] != want+"; the development manager has been told." {
		t.Errorf("reported conflict Says() = %q", got)
	}
	if got := c.Waiting(); !strings.Contains(got, "conflicts in a.go, b.go") {
		t.Errorf("conflict Waiting() = %q", got)
	}

	refused := tbuRecord("staging")
	r := tbuAttempt()
	r.Refused = "the check make check failed"
	refused.Pending = &r
	if got := refused.Says(); len(got) != 1 || got[0] != "Bringing staging up to date with main is waiting: the check make check failed." {
		t.Errorf("refused Says() = %q", got)
	}
	if r.Waiting() != r.Refused {
		t.Errorf("refused Waiting() = %q", r.Waiting())
	}

	pr := tbuRecord("staging")
	p := tbuAttempt()
	p.PullRequest = 42
	p.MergeRequestedAt = &at
	pr.Pending = &p
	if got := pr.Says(); len(got) != 1 || got[0] != "Bringing staging up to date with main: pull request #42 is waiting for the forge to merge it." {
		t.Errorf("pull request Says() = %q", got)
	}
	if p.Waiting() != "" {
		t.Errorf("pull request Waiting() = %q; want empty", p.Waiting())
	}

	busy := tbuRecord("staging")
	b := tbuAttempt()
	busy.Pending = &b
	if got := busy.Says(); len(got) != 1 || got[0] != "Bringing staging up to date with main at "+strings.Repeat("a", 12)+" is in progress." {
		t.Errorf("in progress Says() = %q", got)
	}
}

func TestTeamBranchUpdateChecksPassed(t *testing.T) {
	at := time.Now()
	a := tbuAttempt()
	if a.ChecksPassed() {
		t.Error("ChecksPassed() true with no checks")
	}
	a.Checks = []TeamBranchUpdateCheck{{Command: "x", Passed: true}}
	if a.ChecksPassed() {
		t.Error("ChecksPassed() true before the checks finished")
	}
	a.ChecksFinishedAt = &at
	if !a.ChecksPassed() {
		t.Error("ChecksPassed() false for passing checks")
	}
	a.Checks = append(a.Checks, TeamBranchUpdateCheck{Command: "y", Passed: true, CouldNotRun: "no such tool"})
	if a.ChecksPassed() {
		t.Error("ChecksPassed() true with a check that could not run")
	}
}

func TestTeamBranchUpdateStoreKeepsBranchesApart(t *testing.T) {
	store := newTBUStore(t, t.TempDir())
	if all, err := store.All(); err != nil || len(all) != 0 {
		t.Fatalf("All() of an empty store = %v, %v", all, err)
	}
	for _, branch := range []string{"staging", "release/1.2", "release-1.2"} {
		record := tbuRecord(branch)
		p := tbuAttempt()
		record.Pending = &p
		if err := store.Save(record); err != nil {
			t.Fatalf("Save(%s) error = %v", branch, err)
		}
	}
	for _, branch := range []string{"staging", "release/1.2", "release-1.2"} {
		got, found, err := store.Load(branch)
		if err != nil || !found || got.TargetBranch != branch {
			t.Fatalf("Load(%s) = %#v, %t, %v", branch, got, found, err)
		}
	}
	all, err := store.All()
	if err != nil || len(all) != 3 {
		t.Fatalf("All() = %v, %v; want three", all, err)
	}
	if all[0].TargetBranch != "release-1.2" || all[1].TargetBranch != "release/1.2" || all[2].TargetBranch != "staging" {
		t.Fatalf("All() order = %s, %s, %s", all[0].TargetBranch, all[1].TargetBranch, all[2].TargetBranch)
	}
}

func TestTeamBranchUpdateConflictItemName(t *testing.T) {
	t.Parallel()
	if got := (TeamBranchUpdateConflictItem{ID: "ex-7", Title: "Retry the merge"}).Name(); got != "Retry the merge (ex-7)" {
		t.Fatalf("Name() = %q, want the title with the identifier after it", got)
	}
	if got := (TeamBranchUpdateConflictItem{ID: "ex-7", Title: "  "}).Name(); got != "ex-7" {
		t.Fatalf("Name() with no title = %q, want the identifier", got)
	}
}

func tbuDecision() TeamBranchConflictDecision {
	return TeamBranchConflictDecision{
		Item:      TeamBranchUpdateConflictItem{ID: "yoyodyne-item", Title: "Reword the greeting"},
		Reason:    "the item changed that line",
		DecidedBy: "the development manager in conversation chat-1, turn 4",
		DecidedAt: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC),
	}
}

func tbuConflicted() TeamBranchUpdate {
	record := tbuRecord("staging")
	pending := tbuAttempt()
	pending.Conflict = &TeamBranchUpdateConflict{Paths: []string{"app.txt"}, FoundAt: time.Now()}
	record.Pending = &pending
	return record
}

func TestADecisionOnAConflictCountsARoundAndIsRefusedWhereItCannotStand(t *testing.T) {
	record := tbuConflicted()
	if err := record.DecideConflict(tbuDecision()); err != nil || record.ConflictRounds != 1 || record.Decision == nil || record.Decision.Round != 1 {
		t.Fatalf("DecideConflict() = %v, rounds %d, decision %#v; want round 1 recorded", err, record.ConflictRounds, record.Decision)
	}
	if err := record.DecideConflict(tbuDecision()); err == nil || !strings.Contains(err.Error(), "already has a decision") || record.ConflictRounds != 1 {
		t.Fatalf("second DecideConflict() = %v, rounds %d; want a refusal that spends nothing", err, record.ConflictRounds)
	}

	none := tbuRecord("staging")
	if err := none.DecideConflict(tbuDecision()); err == nil || !strings.Contains(err.Error(), "no conflict is waiting") {
		t.Fatalf("DecideConflict() with no conflict = %v; want it refused", err)
	}
	unnamed := tbuConflicted()
	noItem := tbuDecision()
	noItem.Item.ID = " "
	if err := unnamed.DecideConflict(noItem); err == nil || !strings.Contains(err.Error(), "names one work item") {
		t.Fatalf("DecideConflict() with no item = %v; want it refused", err)
	}
	noReason := tbuDecision()
	noReason.Reason = ""
	if err := unnamed.DecideConflict(noReason); err == nil || unnamed.ConflictRounds != 0 {
		t.Fatalf("DecideConflict() with no reason = %v, rounds %d; want it refused", err, unnamed.ConflictRounds)
	}
}

func TestAFourthRoundIsRefusedAndTheRoundsStartAgainOnceAnUpdateLands(t *testing.T) {
	record := tbuConflicted()
	for round := 1; round <= MaxTeamBranchConflictRounds; round++ {
		if err := record.DecideConflict(tbuDecision()); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		record.GiveUpConflictRun() // the run made no merge; the round stays spent
	}
	err := record.DecideConflict(tbuDecision())
	if err == nil || !strings.Contains(err.Error(), "a fourth round is refused") || !strings.Contains(err.Error(), "3 rounds") || !strings.Contains(err.Error(), "spent") {
		t.Fatalf("fourth DecideConflict() = %v; want a plain refusal naming the three spent rounds", err)
	}
	if record.ConflictRounds != MaxTeamBranchConflictRounds {
		t.Fatalf("rounds = %d, want the refusal not to add one", record.ConflictRounds)
	}
	record.Land(time.Now(), tbuCommit("c"))
	if record.ConflictRounds != 0 || record.Decision != nil {
		t.Fatalf("after a landing rounds = %d, decision %#v; want them started again", record.ConflictRounds, record.Decision)
	}
}

func TestARoundIsGivenBackWhereItsRunNeverStarted(t *testing.T) {
	record := tbuConflicted()
	if err := record.DecideConflict(tbuDecision()); err != nil {
		t.Fatal(err)
	}
	record.RefundUnstartedRound()
	if record.ConflictRounds != 0 || record.Decision != nil {
		t.Fatalf("rounds = %d, decision %#v; want the round given back", record.ConflictRounds, record.Decision)
	}
	if err := record.DecideConflict(tbuDecision()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	record.Decision.StartedAt = &started
	record.RefundUnstartedRound()
	if record.ConflictRounds != 1 {
		t.Fatalf("rounds = %d, want a round whose run started to stay spent", record.ConflictRounds)
	}
}

func TestTheStoreRecordsADecisionUnderTheUpdatersLeaseAndRefusesWhileAnotherHoldsIt(t *testing.T) {
	store := newTBUStore(t, t.TempDir())
	if _, err := store.DecideConflict(context.Background(), "staging", tbuDecision()); err == nil || !strings.Contains(err.Error(), "not being brought up to date") {
		t.Fatalf("DecideConflict() with no record = %v; want it refused", err)
	}
	if err := store.Save(tbuConflicted()); err != nil {
		t.Fatal(err)
	}
	lease, held, err := store.LeaseUpdater(context.Background(), "staging")
	if err != nil || !held {
		t.Fatalf("LeaseUpdater() = %t, %v", held, err)
	}
	if _, err := store.DecideConflict(context.Background(), "staging", tbuDecision()); err == nil || !strings.Contains(err.Error(), "another process is bringing") {
		t.Fatalf("DecideConflict() under another's lease = %v; want it asked to be made again", err)
	}
	_ = lease.Release()
	decided, err := store.DecideConflict(context.Background(), "staging", tbuDecision())
	if err != nil || decided.Decision == nil {
		t.Fatalf("DecideConflict() = %#v, %v", decided, err)
	}
	loaded, _, err := store.Load("staging")
	if err != nil || loaded.ConflictRounds != 1 || loaded.Decision == nil || loaded.Decision.Item.ID != "yoyodyne-item" {
		t.Fatalf("Load() = %#v, %v; want the decision and its round kept", loaded, err)
	}
	bad := tbuConflicted()
	bad.ConflictRounds = MaxTeamBranchConflictRounds + 1
	if err := store.Save(bad); err == nil {
		t.Fatal("Save() accepted more rounds than an update is given")
	}
}
