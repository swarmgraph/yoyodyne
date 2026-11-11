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
	if got := conflict.Says(); len(got) != 1 || got[0] != want+"; waiting for the development manager to name an item to resolve it." {
		t.Errorf("unreported conflict Says() = %q", got)
	}
	c.ReportedAt = &at
	if got := conflict.Says(); len(got) != 1 || got[0] != want+"; the development manager has been told; waiting for the development manager to name an item to resolve it." {
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

func conflictedRecord(t *testing.T, store *TeamBranchUpdateStore) TeamBranchUpdate {
	t.Helper()
	record := tbuRecord("staging")
	pending := tbuAttempt()
	pending.Conflict = &TeamBranchUpdateConflict{
		Paths: []string{"app.txt"}, FoundAt: time.Date(2026, 11, 10, 9, 5, 0, 0, time.UTC),
		Items: []TeamBranchConflictItem{{ID: "yoyodyne-abc.1", Title: "Adds the export button"}, {ID: "yoyodyne-abc.2"}},
	}
	record.Pending = &pending
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return record
}

func tbuDecision() TeamBranchResolutionDecision {
	return TeamBranchResolutionDecision{
		TargetBranch: "staging", WorkItemID: "yoyodyne-abc.1", WorkItemTitle: "Adds the export button", Reason: "it touched the file last",
		DecidedBy: "development-manager", Conversation: "dm-1", Turn: 3, At: time.Date(2026, 11, 10, 9, 10, 0, 0, time.UTC),
	}
}

func TestAConflictItemIsSaidByTitleWithItsIdentifierAfterIt(t *testing.T) {
	if got := (TeamBranchConflictItem{ID: "yoyodyne-abc.1", Title: "  Adds the\n export button "}).Says(); got != "Adds the export button (yoyodyne-abc.1)" {
		t.Fatalf("Says() = %q", got)
	}
	if got := (TeamBranchConflictItem{ID: "yoyodyne-abc.2"}).Says(); got != "yoyodyne-abc.2" {
		t.Fatalf("Says() with no title = %q, want the identifier alone", got)
	}
}

func TestDecideResolutionRecordsTheDecisionOnTheConflictAndKeepsTheRoundsSpent(t *testing.T) {
	store := newTBUStore(t, t.TempDir())
	conflictedRecord(t, store)
	ctx := context.Background()

	decided, err := store.DecideResolution(ctx, tbuDecision())
	if err != nil {
		t.Fatalf("DecideResolution() error = %v", err)
	}
	resolution := decided.Pending.Resolution
	if resolution == nil || resolution.WorkItemID != "yoyodyne-abc.1" || resolution.DecidedBy != "development-manager" || resolution.Conversation != "dm-1" || resolution.Turn != 3 || resolution.RoundsLeft() != 3 {
		t.Fatalf("resolution = %#v, want the decision as made", resolution)
	}
	loaded, _, _ := store.Load("staging")
	if loaded.Pending.Resolution == nil || loaded.Pending.Resolution.Reason != "it touched the file last" {
		t.Fatalf("loaded = %#v, want the decision saved", loaded.Pending)
	}
	says := strings.Join(loaded.Says(), "\n")
	if !strings.Contains(says, "the development manager decided that Adds the export button (yoyodyne-abc.1) resolves it (round 1 of 3 starts at the next check)") {
		t.Fatalf("Says() = %q", says)
	}

	// A round ended, then a new decision naming another item: the item changes,
	// the round spent stays counted.
	ended := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	loaded.Pending.Resolution.Rounds = []TeamBranchResolutionRound{{RunID: "run-" + strings.Repeat("1", 32), StartedAt: ended.Add(-time.Hour), EndedAt: &ended, Why: "the reviewer refused it"}}
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	again := tbuDecision()
	again.WorkItemID, again.WorkItemTitle, again.Reason = "yoyodyne-abc.2", "", "the other item is closer"
	decided, err = store.DecideResolution(ctx, again)
	if err != nil {
		t.Fatalf("DecideResolution(again) error = %v", err)
	}
	if r := decided.Pending.Resolution; r.WorkItemID != "yoyodyne-abc.2" || r.Reason != "the other item is closer" || len(r.Rounds) != 1 || r.RoundsLeft() != 2 {
		t.Fatalf("resolution after a second decision = %#v, want the new item with the round spent kept", r)
	}
	if says := strings.Join(decided.Says(), "\n"); !strings.Contains(says, "decided that yoyodyne-abc.2 resolves it (1 of 3 rounds used; the next starts at the next check)") {
		t.Fatalf("Says() = %q", says)
	}
}

func TestDecideResolutionRefusesInPlainWords(t *testing.T) {
	ctx := context.Background()
	store := newTBUStore(t, t.TempDir())
	if _, err := store.DecideResolution(ctx, tbuDecision()); err == nil || !strings.Contains(err.Error(), "no update of staging is waiting on a conflict") {
		t.Fatalf("DecideResolution() with no record = %v", err)
	}
	record := tbuRecord("staging")
	pending := tbuAttempt()
	pending.Refused = "the checks failed"
	record.Pending = &pending
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecideResolution(ctx, tbuDecision()); err == nil || !strings.Contains(err.Error(), "no update of staging is waiting on a conflict") {
		t.Fatalf("DecideResolution() on a refused update = %v", err)
	}

	conflicted := conflictedRecord(t, store)
	for name, change := range map[string]func(*TeamBranchResolutionDecision){
		"empty reason": func(d *TeamBranchResolutionDecision) { d.Reason = "  " },
		"no item":      func(d *TeamBranchResolutionDecision) { d.WorkItemID = "" },
		"no decider":   func(d *TeamBranchResolutionDecision) { d.DecidedBy = "" },
	} {
		decision := tbuDecision()
		change(&decision)
		if _, err := store.DecideResolution(ctx, decision); err == nil {
			t.Errorf("%s: DecideResolution() accepted it", name)
		}
	}
	if loaded, _, _ := store.Load("staging"); loaded.Pending.Resolution != nil {
		t.Fatalf("a refused decision was recorded: %#v", loaded.Pending.Resolution)
	}

	// A round in progress blocks a change of decision.
	running := conflicted
	running.Pending.Resolution = &TeamBranchResolution{WorkItemID: "yoyodyne-abc.1", Reason: "r", DecidedBy: "development-manager", DecidedAt: time.Now(),
		Rounds: []TeamBranchResolutionRound{{RunID: "run-" + strings.Repeat("2", 32), StartedAt: time.Now()}}}
	if err := store.Save(running); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecideResolution(ctx, tbuDecision()); err == nil || !strings.Contains(err.Error(), "a round of resolving this conflict is running now") {
		t.Fatalf("DecideResolution() during a round = %v", err)
	}
	if says := strings.Join(running.Says(), "\n"); !strings.Contains(says, "(round 1 of 3 in progress)") {
		t.Fatalf("Says() = %q", says)
	}

	// Every round used: refused, and the line says so.
	spent := running
	ended := time.Now()
	rounds := make([]TeamBranchResolutionRound, MaxTeamBranchResolutionRounds)
	for i := range rounds {
		rounds[i] = TeamBranchResolutionRound{RunID: "run-" + strings.Repeat(string(rune('3'+i)), 32), StartedAt: ended, EndedAt: &ended, Why: "no"}
	}
	spent.Pending.Resolution.Rounds = rounds
	if err := store.Save(spent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecideResolution(ctx, tbuDecision()); err == nil || !strings.Contains(err.Error(), "all 3 rounds of resolving this conflict have been used") {
		t.Fatalf("DecideResolution() with the rounds spent = %v", err)
	}
	if says := strings.Join(spent.Says(), "\n"); !strings.Contains(says, "rounds of resolving it are spent (3 of 3)") {
		t.Fatalf("Says() = %q", says)
	}
	if !spent.Pending.Resolution.Spent() || spent.Pending.Resolution.RoundsLeft() != 0 {
		t.Fatal("three rounds are not spent")
	}
}

func TestDecideResolutionLeavesTheRecordToTheUpdaterThatHoldsTheLease(t *testing.T) {
	root := t.TempDir()
	store := newTBUStore(t, root)
	conflictedRecord(t, store)
	lease, held, err := newTBUStore(t, root).LeaseUpdater(context.Background(), "staging")
	if err != nil || !held {
		t.Fatalf("LeaseUpdater() = %t, %v", held, err)
	}
	defer lease.Release()
	if _, err := store.DecideResolution(context.Background(), tbuDecision()); err == nil || !strings.Contains(err.Error(), "is being worked on right now") {
		t.Fatalf("DecideResolution() while the update is worked = %v, want it refused to be recorded again shortly", err)
	}
}

func TestResolutionValidationRefusesWhatCannotBeKept(t *testing.T) {
	good := TeamBranchResolution{WorkItemID: "x", Reason: "r", DecidedBy: "person", DecidedAt: time.Now()}
	tooMany := good
	tooMany.Rounds = make([]TeamBranchResolutionRound, MaxTeamBranchResolutionRounds+1)
	for i := range tooMany.Rounds {
		ended := time.Now()
		tooMany.Rounds[i] = TeamBranchResolutionRound{RunID: "run-" + strings.Repeat("a", 32), StartedAt: ended, EndedAt: &ended}
	}
	twoOpen := good
	twoOpen.Rounds = []TeamBranchResolutionRound{{RunID: "run-" + strings.Repeat("a", 32), StartedAt: time.Now()}, {RunID: "run-" + strings.Repeat("b", 32), StartedAt: time.Now()}}
	badRun := good
	badRun.Rounds = []TeamBranchResolutionRound{{RunID: "nope", StartedAt: time.Now()}}
	noReason := good
	noReason.Reason = ""
	for name, resolution := range map[string]TeamBranchResolution{"too many rounds": tooMany, "two rounds in progress": twoOpen, "bad run id": badRun, "no reason": noReason} {
		record := tbuRecord("staging")
		pending := tbuAttempt()
		pending.Resolution = &resolution
		record.Pending = &pending
		if err := record.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted it", name)
		}
	}
	record := tbuRecord("staging")
	pending := tbuAttempt()
	pending.Resolution = &good
	record.Pending = &pending
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() of a good decision = %v", err)
	}
	// Free text is bounded on save.
	long := good
	long.Reason = strings.Repeat("y", 3*MaxRecordedTextBytes)
	pending.Resolution = &long
	record.Pending = &pending
	store := newTBUStore(t, t.TempDir())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := store.Load("staging"); len(got.Pending.Resolution.Reason) > MaxRecordedTextBytes {
		t.Fatalf("reason is %d bytes after save, want it bounded", len(got.Pending.Resolution.Reason))
	}
}

func TestTeamBranchResolutionRunNamesItsRunAndItsBookkeepingItem(t *testing.T) {
	run := TeamBranchResolutionRun{TargetBranch: "staging", UpstreamBranch: "main", TeamCommit: tbuCommit("a"), TargetBase: tbuCommit("b"), ConflictedBase: tbuCommit("c"),
		Paths: []string{"app.txt"}, WorkItemID: "yoyodyne-abc.1", Round: 1}
	if err := run.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	next := run
	next.Round = 2
	if run.RunID() == next.RunID() || !ValidRunID(run.RunID()) {
		t.Fatalf("run ids %s and %s, want valid and distinct per round", run.RunID(), next.RunID())
	}
	if got, ok := TeamBranchResolutionRunOfItem(run.WorkItemBookkeepingID()); !ok || got != run.RunID() {
		t.Fatalf("TeamBranchResolutionRunOfItem(%q) = %q, %t", run.WorkItemBookkeepingID(), got, ok)
	}
	if _, ok := TeamBranchResolutionRunOfItem("yoyodyne-abc.1"); ok {
		t.Fatal("a tracker item was taken for a bookkeeping item")
	}
	for name, change := range map[string]func(*TeamBranchResolutionRun){
		"round zero": func(r *TeamBranchResolutionRun) { r.Round = 0 },
		"round four": func(r *TeamBranchResolutionRun) { r.Round = 4 },
		"no paths":   func(r *TeamBranchResolutionRun) { r.Paths = nil },
		"bad commit": func(r *TeamBranchResolutionRun) { r.ConflictedBase = "x" },
		"no item":    func(r *TeamBranchResolutionRun) { r.WorkItemID = "" },
	} {
		bad := run
		change(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s: Validate() accepted it", name)
		}
	}
}
