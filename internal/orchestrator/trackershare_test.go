package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// shareMachine is one machine's tracker: rows by item, written as `bd import`
// writes them, keeping its own row on anything not strictly later.
type shareMachine struct {
	rows     map[string]string
	writes   [][]byte
	refuse   error
	beatBy   map[string]bool
	exported beads.ExportedVersions
}

func (m *shareMachine) ExportRows(context.Context) ([]byte, error) {
	if m.refuse != nil {
		return nil, m.refuse
	}
	return shareExport(m.rows), nil
}

func (m *shareMachine) ExportedVersions() beads.ExportedVersions { return m.exported }

func (m *shareMachine) ImportRows(_ context.Context, rows [][]byte, _ []byte) (beads.ImportAnswer, error) {
	m.writes = append(m.writes, rows...)
	var answer beads.ImportAnswer
	for _, row := range rows {
		id := shareID(string(row))
		if m.beatBy[id] {
			answer.Kept = append(answer.Kept, id)
			continue
		}
		m.rows[id] = string(row)
	}
	return answer, nil
}

type sharedBranch struct {
	content string
	missing bool
	refuse  error
	reads   int
}

func (b *sharedBranch) CurrentBranch(context.Context) (string, error) { return "main", nil }

func (b *sharedBranch) SharedFile(context.Context, string, string) ([]byte, bool, error) {
	b.reads++
	if b.refuse != nil {
		return nil, false, b.refuse
	}
	if b.missing {
		return nil, false, nil
	}
	return []byte(b.content), true, nil
}

type shareReports struct{ reports []report.Report }

func (r *shareReports) Append(reported report.Report) error {
	r.reports = append(r.reports, reported)
	return nil
}

func shareRow(id, title, status, updated string) string {
	return `{"_type":"issue","id":"` + id + `","title":"` + title + `","status":"` + status + `","updated_at":"` + updated + `"}`
}

func shareID(row string) string {
	_, rest, _ := strings.Cut(row, `"id":"`)
	id, _, _ := strings.Cut(rest, `"`)
	return id
}

func shareExport(rows map[string]string) []byte {
	var out strings.Builder
	for _, row := range rows {
		out.WriteString(row + "\n")
	}
	return []byte(out.String())
}

type shareFixture struct {
	machine *shareMachine
	branch  *sharedBranch
	reports *shareReports
	store   *runstate.TrackerMergeStore
	runs    *runstate.Store
	share   TrackerShare
}

// newShareFixture is a machine and a shared branch that both start from rows,
// with one merge already made so the two have something they last agreed on.
func newShareFixture(t *testing.T, rows map[string]string) shareFixture {
	t.Helper()
	root := t.TempDir()
	store, err := runstate.NewTrackerMergeStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	mine := map[string]string{}
	for id, row := range rows {
		mine[id] = row
	}
	fixture := shareFixture{
		machine: &shareMachine{rows: mine, beatBy: map[string]bool{}},
		branch:  &sharedBranch{content: string(shareExport(rows))},
		reports: &shareReports{},
		store:   store,
		runs:    runs,
	}
	fixture.share = TrackerShare{
		Remote: fixture.branch, Tracker: fixture.machine, Store: store, Reports: fixture.reports,
		ProductID: "yoyodyne", RepositoryID: "repository",
		Now: func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) },
	}
	if _, err := fixture.share.Check(context.Background()); err != nil {
		t.Fatalf("the first merge: %v", err)
	}
	fixture.machine.writes = nil
	return fixture
}

const (
	shareEarlier = "2026-11-01T10:00:00Z"
	shareHere    = "2026-11-02T10:00:00Z"
	shareThere   = "2026-11-03T10:00:00Z"
)

func TestDifferentItemsEditedOnTwoMachinesAreBothKept(t *testing.T) {
	t.Parallel()

	fixture := newShareFixture(t, map[string]string{
		"t-a": shareRow("t-a", "a", "open", shareEarlier),
		"t-b": shareRow("t-b", "b", "open", shareEarlier),
	})
	fixture.machine.rows["t-a"] = shareRow("t-a", "a edited here", "open", shareHere)
	fixture.branch.content = string(shareExport(map[string]string{
		"t-a": shareRow("t-a", "a", "open", shareEarlier),
		"t-b": shareRow("t-b", "b edited there", "open", shareThere),
	}))

	result, err := fixture.share.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Held) != 0 || len(fixture.reports.reports) != 0 {
		t.Fatalf("held %v, reports %+v; want neither", result.Held, fixture.reports.reports)
	}
	if !strings.Contains(fixture.machine.rows["t-a"], "a edited here") {
		t.Errorf("this machine's edit was lost: %s", fixture.machine.rows["t-a"])
	}
	if !strings.Contains(fixture.machine.rows["t-b"], "b edited there") {
		t.Errorf("the other machine's edit did not arrive: %s", fixture.machine.rows["t-b"])
	}
}

func heldFixture(t *testing.T) shareFixture {
	t.Helper()
	fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
	fixture.machine.rows["t-a"] = shareRow("t-a", "a", "in_progress", shareHere)
	fixture.branch.content = shareRow("t-a", "a", "closed", shareThere) + "\n"
	return fixture
}

func TestOneItemChangedTwoWaysIsHeldWithBothNamedAndNeitherEdited(t *testing.T) {
	t.Parallel()

	fixture := heldFixture(t)
	ours, theirs := fixture.machine.rows["t-a"], fixture.branch.content
	ctx := context.Background()

	result, err := fixture.share.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Held) != 1 || result.Held[0] != "t-a" {
		t.Fatalf("held = %v, want the item", result.Held)
	}
	if fixture.machine.rows["t-a"] != ours || fixture.branch.content != theirs || len(fixture.machine.writes) != 0 {
		t.Errorf("a version was edited: machine %s, shared %s, writes %s", fixture.machine.rows["t-a"], fixture.branch.content, fixture.machine.writes)
	}

	// The development manager is told, once, in words naming both.
	if len(fixture.reports.reports) != 1 {
		t.Fatalf("reports = %+v, want one", fixture.reports.reports)
	}
	told := fixture.reports.reports[0]
	if told.For != domain.RoleDevelopmentManager || told.WorkItemID != "t-a" {
		t.Errorf("report for %s about %s", told.For, told.WorkItemID)
	}
	for _, want := range []string{"cannot be merged", "neither version has been changed", "On this machine", `status is "in_progress"`, "shared on main", `status is "closed"`, "keep_version"} {
		if !strings.Contains(told.Message, want) {
			t.Errorf("report %q lacks %q", told.Message, want)
		}
	}
	if _, err := fixture.share.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fixture.reports.reports) != 1 {
		t.Errorf("the same two versions were reported %d times", len(fixture.reports.reports))
	}

	// And nothing the harness chooses starts it.
	holds, err := readmodel.HeldForAPerson(ctx, fixture.runs, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reason, held := holds.Reason("t-a")
	if !held || !strings.Contains(reason, `status is "in_progress"`) || !strings.Contains(reason, `status is "closed"`) {
		t.Errorf("hold = %q, %v; want both versions named", reason, held)
	}
	if holds.Decided("t-a") {
		t.Error("the hold reads as decided before anybody decided")
	}
}

func TestADecisionIsCarriedOutByTheNextCheckAndEndsTheHold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	at := time.Date(2026, 11, 6, 13, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		keep runstate.TrackerVersion
		want string
	}{
		{runstate.TrackerVersionShared, `"status":"closed"`},
		{runstate.TrackerVersionThisMachine, `"status":"in_progress"`},
	} {
		fixture := heldFixture(t)
		if _, err := fixture.share.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.DecideHold(ctx, "t-a", tt.keep, "why", "the development manager", at); err != nil {
			t.Fatal(err)
		}
		holds, err := readmodel.HeldForAPerson(ctx, fixture.runs, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !holds.Decided("t-a") {
			t.Errorf("keeping %s: the hold does not read as decided until it is carried out", tt.keep)
		}
		result, err := fixture.share.Check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Held) != 0 || len(result.Ended) != 1 {
			t.Errorf("keeping %s: held %v, ended %v", tt.keep, result.Held, result.Ended)
		}
		if !strings.Contains(fixture.machine.rows["t-a"], tt.want) {
			t.Errorf("keeping %s: this machine has %s", tt.keep, fixture.machine.rows["t-a"])
		}
		remaining, err := fixture.store.Holds()
		if err != nil || len(remaining) != 0 {
			t.Errorf("keeping %s: holds %v, error %v", tt.keep, remaining, err)
		}
		if _, err := fixture.store.DecideHold(ctx, "t-a", tt.keep, "why", "her", at); !errors.Is(err, runstate.ErrNoTrackerMergeHold) {
			t.Errorf("a decision with no hold: error = %v", err)
		}
	}
}

func TestAHoldEndsOnItsOwnOnceTheTwoVersionsAgree(t *testing.T) {
	t.Parallel()

	fixture := heldFixture(t)
	ctx := context.Background()
	if _, err := fixture.share.Check(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.machine.rows["t-a"] = shareRow("t-a", "a", "closed", "2026-11-04T10:00:00Z")
	result, err := fixture.share.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ended) != 1 || len(result.Held) != 0 {
		t.Errorf("ended %v, held %v", result.Ended, result.Held)
	}
}

func TestVersionsThatMoveAfterADecisionAreHeldAndToldAgain(t *testing.T) {
	t.Parallel()

	fixture := heldFixture(t)
	ctx := context.Background()
	if _, err := fixture.share.Check(ctx); err != nil {
		t.Fatal(err)
	}
	fixture.machine.rows["t-a"] = shareRow("t-a", "a", "blocked", "2026-11-04T10:00:00Z")
	if _, err := fixture.share.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fixture.reports.reports) != 2 || !strings.Contains(fixture.reports.reports[1].Message, `status is "blocked"`) {
		t.Errorf("reports = %+v, want the new version told", fixture.reports.reports)
	}
}

// A remote or a tracker that cannot answer changes nothing, and the next check
// reads the same change again.
func TestAMergeThatCannotFinishChangesNothingAndIsReadAgain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
	before, _, err := fixture.store.Base()
	if err != nil {
		t.Fatal(err)
	}
	moved := shareRow("t-a", "a edited there", "open", shareThere) + "\n"

	fixture.branch.refuse = errors.New("remote refused")
	if _, err := fixture.share.Check(ctx); err == nil {
		t.Error("an unreachable remote was not said")
	}
	fixture.branch.refuse = nil
	fixture.branch.content = moved
	fixture.machine.refuse = errors.New("store busy")
	if _, err := fixture.share.Check(ctx); err == nil {
		t.Error("a tracker that did not answer was not said")
	}
	after, _, err := fixture.store.Base()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("what was last read moved from %s to %s", before, after)
	}

	fixture.machine.refuse = nil
	if _, err := fixture.share.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fixture.machine.rows["t-a"], "a edited there") {
		t.Errorf("the next check did not take it: %s", fixture.machine.rows["t-a"])
	}
}

// A write landing here during the import beats the row, as it should, and the
// shared change is not lost: the next check still has it to take.
func TestARowANewerWriteBeatIsReadAgain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
	fixture.branch.content = shareRow("t-a", "a edited there", "open", shareThere) + "\n"
	fixture.machine.beatBy["t-a"] = true
	result, err := fixture.share.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Kept) != 1 {
		t.Fatalf("kept = %v", result.Kept)
	}
	fixture.machine.beatBy["t-a"] = false
	if _, err := fixture.share.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fixture.machine.rows["t-a"], "a edited there") {
		t.Errorf("the shared change was lost: %s", fixture.machine.rows["t-a"])
	}
}

func TestAnUnchangedSharedExportAsksTheTrackerNothing(t *testing.T) {
	t.Parallel()

	fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
	fixture.machine.refuse = errors.New("asked")
	result, err := fixture.share.Check(context.Background())
	if err != nil || !result.Unchanged {
		t.Errorf("result %+v, error %v; want unchanged without asking", result, err)
	}
}

func TestABranchWithNoExportHasNothingToMerge(t *testing.T) {
	t.Parallel()

	fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
	fixture.branch.missing = true
	result, err := fixture.share.Check(context.Background())
	if err != nil || !result.NoSharedExport {
		t.Errorf("result %+v, error %v", result, err)
	}
}

func TestAStoreThatCouldNotBeOpenedIsSaidAtEachCheck(t *testing.T) {
	t.Parallel()

	problem := errors.New("no state root")
	if _, err := (TrackerShare{StoreProblem: problem}).Check(context.Background()); !errors.Is(err, problem) {
		t.Errorf("error = %v", err)
	}
}

func TestAForgeCheckWithTeamModeOffDoesNotReadTheSharedExport(t *testing.T) {
	t.Parallel()

	check, err := Reconciler{}.CheckForge(context.Background())
	if check.Tracker != nil {
		t.Errorf("tracker = %+v, error %v; want nothing read", check.Tracker, err)
	}
}

// With the real versions record behind it, a commit of this machine's own export
// holds nothing when the item has moved on here since.
func TestThisMachinesOwnExportOnTheBranchHoldsNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
	committed := shareRow("t-a", "a", "in_progress", shareHere)
	fixture.branch.content = committed + "\n"
	fixture.machine.rows["t-a"] = shareRow("t-a", "a", "closed", shareThere)
	fixture.machine.exported = exportedVersionsOf(t, committed)

	result, err := fixture.share.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Held) != 0 || len(fixture.reports.reports) != 0 {
		t.Errorf("held %v, reports %+v; want neither", result.Held, fixture.reports.reports)
	}
	if !strings.Contains(fixture.machine.rows["t-a"], `"status":"closed"`) {
		t.Errorf("this machine's version changed: %s", fixture.machine.rows["t-a"])
	}
}

// exportedVersionsOf is the record a real export of these rows leaves.
func exportedVersionsOf(t *testing.T, rows ...string) beads.ExportedVersions {
	t.Helper()
	versions, err := beads.ExportedVersionsOf([]byte(strings.Join(rows, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return versions
}

// A removal cannot be carried into the tracker, so keeping it is refused and the
// hold stands. Every other way of settling an edit against a removal is carried out.
func TestARemovalMeetingAnEditIsSettledOnlyInWaysTheHarnessCanCarryOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	at := time.Date(2026, 11, 6, 13, 0, 0, 0, time.UTC)
	removedThere := func(t *testing.T) shareFixture {
		fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
		fixture.machine.rows["t-a"] = shareRow("t-a", "a edited here", "open", shareHere)
		fixture.branch.content = ""
		if _, err := fixture.share.Check(ctx); err != nil {
			t.Fatal(err)
		}
		return fixture
	}
	removedHere := func(t *testing.T) shareFixture {
		fixture := newShareFixture(t, map[string]string{"t-a": shareRow("t-a", "a", "open", shareEarlier)})
		delete(fixture.machine.rows, "t-a")
		fixture.branch.content = shareRow("t-a", "a edited there", "open", shareThere) + "\n"
		if _, err := fixture.share.Check(ctx); err != nil {
			t.Fatal(err)
		}
		return fixture
	}

	t.Run("keeping the shared removal is refused and the hold stands", func(t *testing.T) {
		t.Parallel()
		fixture := removedThere(t)
		if !strings.Contains(fixture.reports.reports[0].Message, "the only version it can keep is this machine's") {
			t.Errorf("report = %q, want the one decision open to her", fixture.reports.reports[0].Message)
		}
		_, err := fixture.store.DecideHold(ctx, "t-a", runstate.TrackerVersionShared, "why", "her", at)
		if !errors.Is(err, runstate.ErrCannotKeepRemoval) {
			t.Fatalf("error = %v", err)
		}
		result, err := fixture.share.Check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Held) != 1 || len(result.Ended) != 0 {
			t.Errorf("held %v, ended %v; want it still held", result.Held, result.Ended)
		}
	})
	t.Run("keeping this machine's edit over the shared removal", func(t *testing.T) {
		t.Parallel()
		fixture := removedThere(t)
		if _, err := fixture.store.DecideHold(ctx, "t-a", runstate.TrackerVersionThisMachine, "why", "her", at); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.share.Check(ctx)
		if err != nil || len(result.Ended) != 1 || len(result.Held) != 0 {
			t.Fatalf("result %+v, error %v", result, err)
		}
		if !strings.Contains(fixture.machine.rows["t-a"], "a edited here") {
			t.Errorf("this machine has %s", fixture.machine.rows["t-a"])
		}
	})
	t.Run("keeping the shared edit over this machine's removal brings it back", func(t *testing.T) {
		t.Parallel()
		fixture := removedHere(t)
		if _, err := fixture.store.DecideHold(ctx, "t-a", runstate.TrackerVersionShared, "why", "her", at); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.share.Check(ctx)
		if err != nil || len(result.Ended) != 1 {
			t.Fatalf("result %+v, error %v", result, err)
		}
		if !strings.Contains(fixture.machine.rows["t-a"], "a edited there") {
			t.Errorf("this machine has %q", fixture.machine.rows["t-a"])
		}
	})
	t.Run("keeping this machine's removal leaves it removed", func(t *testing.T) {
		t.Parallel()
		fixture := removedHere(t)
		if _, err := fixture.store.DecideHold(ctx, "t-a", runstate.TrackerVersionThisMachine, "why", "her", at); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.share.Check(ctx)
		if err != nil || len(result.Ended) != 1 || len(fixture.machine.writes) != 0 {
			t.Fatalf("result %+v, writes %s, error %v", result, fixture.machine.writes, err)
		}
		if _, present := fixture.machine.rows["t-a"]; present {
			t.Error("the item came back")
		}
	})
}
