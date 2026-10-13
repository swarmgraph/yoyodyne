package releasecut

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeTracker holds the release items and remembers every note written.
type fakeTracker struct {
	mu    sync.Mutex
	items []beads.WorkItem
	notes []string
}

func (t *fakeTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]beads.WorkItem(nil), t.items...), nil
}

func (t *fakeTracker) RecordOutcome(_ context.Context, id, notes string) (beads.WorkItem, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notes = append(t.notes, id+": "+notes)
	return beads.WorkItem{ID: id}, nil
}

func (t *fakeTracker) noted() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.notes...)
}

type fakeReports struct {
	mu    sync.Mutex
	filed []report.Report
}

func (r *fakeReports) Append(filed report.Report) error {
	if err := filed.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filed = append(r.filed, filed)
	return nil
}

func (r *fakeReports) all() []report.Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]report.Report(nil), r.filed...)
}

type fakeHolds struct{ held bool }

func (h fakeHolds) Held() (runstate.OperatorHold, bool, error) {
	if !h.held {
		return runstate.OperatorHold{}, false, nil
	}
	return runstate.OperatorHold{SchemaVersion: 1, HeldAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}, true, nil
}

// countingRunner runs every command for real and counts the gate scripts.
type countingRunner struct {
	execution.OSProcessRunner
	mu    sync.Mutex
	gates int
}

func (r *countingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if command.Name == "bash" {
		r.mu.Lock()
		r.gates++
		r.mu.Unlock()
	}
	return r.OSProcessRunner.Run(ctx, command, observer)
}

func (r *countingRunner) gateRuns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gates
}

type fixture struct {
	cutter     *Cutter
	repository string
	origin     string
	mode       string
	tracker    *fakeTracker
	reports    *fakeReports
	cuts       *runstate.ReleaseCutStore
	runner     *countingRunner
	now        time.Time
}

const stamped = `# v0.6.0

## Key functionality

- **Something** (` + "`yoyodyne-x`" + `)

<!-- yoyodyne:release-readiness -->
## Release readiness

The ` + "`release-readiness`" + ` workflow ended in **ready**.

Pinned to ` + "`release-readiness@abc123`" + `.
<!-- /yoyodyne:release-readiness -->
`

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_CONFIG_NOSYSTEM=1")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newFixture builds a repository whose main is on a bare origin, with notes
// for v0.6.0 committed and a stub gate script whose answer the test sets: the
// real scripts/cut-release.sh is its own suite's claim, and what is claimed
// here is what the harness does with each way it can end.
func newFixture(t *testing.T, notes bool) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		repository: filepath.Join(base, "repository"),
		origin:     filepath.Join(base, "origin.git"),
		mode:       filepath.Join(base, "gate-mode"),
		tracker:    &fakeTracker{},
		reports:    &fakeReports{},
		runner:     &countingRunner{OSProcessRunner: execution.OSProcessRunner{Clock: execution.RealClock{}}},
		now:        time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC),
	}
	git(t, base, "init", "-q", "--bare", f.origin)
	git(t, f.origin, "symbolic-ref", "HEAD", "refs/heads/main")
	git(t, base, "init", "-q", f.repository)
	git(t, f.repository, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.MkdirAll(filepath.Join(f.repository, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
mode="$(cat '` + f.mode + `' 2>/dev/null || echo green)"
[ "$YOYODYNE_RELEASE_CUT" = "automatic" ] || { echo "cut-release: not run in the harness's mode" >&2; exit 9; }
case "$mode" in
  preparation) echo "cut-release: docs/releases/$1.md does not carry a readiness result matching this reading" >&2; exit 3 ;;
  red) echo "cut-release: make check is red, so $1 was not cut and nothing was written" >&2; exit 1 ;;
esac
echo "=== gates green"
echo "checked commit: $(git -C "$(dirname "$0")/.." rev-parse HEAD)"
`
	if err := os.WriteFile(filepath.Join(f.repository, "scripts", "cut-release.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if notes {
		if err := os.MkdirAll(filepath.Join(f.repository, "docs", "releases"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.repository, "docs", "releases", "v0.6.0.md"), []byte(stamped), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, f.repository, "add", "-A")
	git(t, f.repository, "commit", "-qm", "the commit a release would name")
	git(t, f.repository, "remote", "add", "origin", f.origin)
	git(t, f.repository, "push", "-q", "origin", "main")

	cuts, err := runstate.NewReleaseCutStore(filepath.Join(base, "state"), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.cuts = cuts
	f.tracker.items = []beads.WorkItem{{
		ID: "calc-fvn", Title: "Cut v0.6.0: the release since v0.5.0", Status: "open",
		CreatedAt: time.Now().Add(-48 * time.Hour),
		Origin:    domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager},
	}}
	f.cutter = &Cutter{
		Product: "calc", RepositoryID: "calc", Mode: domain.ApprovalAutomatic,
		Repository: f.repository, Remote: "origin", WorkRoot: filepath.Join(base, "release-cut"),
		CutBy: "the test", Runner: f.runner, Tracker: f.tracker, Cuts: cuts, Reports: f.reports,
		Holds: fakeHolds{}, Now: func() time.Time { return f.now },
	}
	return f
}

func (f *fixture) gate(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(f.mode, []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) head(t *testing.T) string { return git(t, f.repository, "rev-parse", "HEAD") }

func (f *fixture) originTag(t *testing.T) string {
	t.Helper()
	return git(t, f.origin, "for-each-ref", "--format=%(refname) %(*objectname)", "refs/tags")
}

// moveMain lands a commit on origin's main, as integration does all day.
func (f *fixture) moveMain(t *testing.T) {
	t.Helper()
	git(t, f.repository, "commit", "-q", "--allow-empty", "-m", "another change lands")
	git(t, f.repository, "push", "-q", "origin", "main")
}

func (f *fixture) records(t *testing.T) []runstate.ReleaseCut {
	t.Helper()
	cuts, err := f.cuts.List()
	if err != nil {
		t.Fatal(err)
	}
	return cuts
}

func TestAPersonCutsUnderHumanAndNothingIsRun(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.cutter.Mode = domain.ApprovalHuman
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "make release") {
		t.Fatalf("step = %+v, want it skipped naming the person's verb", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" {
		t.Errorf("a human-mode pass ran the gates or pushed a tag")
	}
}

func TestTheOperatorsPauseStopsTheCut(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.cutter.Holds = fakeHolds{held: true}
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "operator's pause") {
		t.Fatalf("step = %+v, want it skipped on the pause", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" {
		t.Errorf("a paused pass ran the gates or pushed a tag")
	}
}

// The whole of it: a decided release with its preparation landed is gated,
// tagged under the harness's identity on the commit origin's main holds,
// pushed, read back, recorded on the item and the pass, and reported at
// warning severity. The next pass cuts nothing.
func TestADecidedReleaseIsCutPushedAndRecorded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	commit := f.head(t)

	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "pushed v0.6.0") {
		t.Fatalf("step = %+v, want the tag pushed", step)
	}
	if got := f.originTag(t); got != "refs/tags/v0.6.0 "+commit {
		t.Fatalf("origin's tags = %q, want v0.6.0 on %s", got, commit)
	}
	name, email := gitworktree.HarnessIdentity()
	if tagger := git(t, f.repository, "for-each-ref", "--format=%(taggername) %(taggeremail)", "refs/tags/v0.6.0"); tagger != name+" <"+email+">" {
		t.Errorf("tagger = %q, want the harness's identity", tagger)
	}
	records := f.records(t)
	if len(records) != 1 || records[0].State != runstate.ReleaseCutPushed || records[0].Commit != commit ||
		records[0].ReadinessVerdict != "ready" || records[0].ReadinessDefinition != "release-readiness@abc123" ||
		records[0].NotesDigest == "" || len(records[0].Gates) != len(gates) || !records[0].Noted || !records[0].Reported {
		t.Fatalf("records = %+v, want one pushed cut carrying its intent", records)
	}
	notes := f.tracker.noted()
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "calc-fvn: The harness cut v0.6.0 and pushed its tag") || !strings.Contains(notes[0], commit) {
		t.Errorf("notes = %q, want one saying the tag was pushed on its commit", notes)
	}
	filed := f.reports.all()
	if len(filed) != 1 || filed[0].Severity != report.SeverityWarning || filed[0].Role != report.HarnessReporter || filed[0].WorkItemID != "calc-fvn" {
		t.Errorf("reports = %+v, want one warning from the harness on the item", filed)
	}
	if entries, _ := os.ReadDir(f.cutter.WorkRoot); len(entries) != 0 {
		t.Errorf("the cut's worktree was left behind: %v", entries)
	}

	f.now = f.now.Add(10 * time.Minute)
	again := f.cutter.Pass(context.Background())
	if again.Outcome != runstate.StepSkipped || !strings.Contains(again.Detail, "no more than one release is cut a day") {
		t.Errorf("second pass = %+v, want nothing cut the same day", again)
	}
	f.now = f.now.Add(48 * time.Hour)
	later := f.cutter.Pass(context.Background())
	if later.Outcome != runstate.StepSkipped || !strings.Contains(later.Detail, "waits on the Lead Product Manager to close its item") {
		t.Errorf("a later pass = %+v, want the pushed release left for its item to close", later)
	}
	if f.runner.gateRuns() != 1 || len(f.tracker.noted()) != 1 || len(f.reports.all()) != 1 {
		t.Errorf("later passes ran the gates %d times and said %d notes and %d reports, want nothing more", f.runner.gateRuns(), len(f.tracker.noted()), len(f.reports.all()))
	}
}

// Missing notes hand the release back to the Lead Product Manager without
// running a gate or drafting anything, and the same notes are handed back once
// however often main moves.
func TestMissingNotesHandTheReleaseBackOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, false)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "docs/releases/v0.6.0.md is not on origin/main") {
		t.Fatalf("step = %+v, want the cut refused on the missing notes", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" {
		t.Errorf("missing notes ran the gates or pushed a tag")
	}
	if filed := f.reports.all(); len(filed) != 1 || !strings.Contains(filed[0].Message, "needs its reviewed run") {
		t.Errorf("reports = %+v, want the release handed back once", filed)
	}
	if notes := f.tracker.noted(); len(notes) != 1 || !strings.Contains(notes[0], "handed back to the Lead Product Manager") {
		t.Errorf("notes = %q, want the item told it is handed back", notes)
	}
	if _, err := os.Stat(filepath.Join(f.repository, "docs", "releases", "v0.6.0.md")); !os.IsNotExist(err) {
		t.Errorf("the harness drafted notes: %v", err)
	}

	f.moveMain(t)
	f.cutter.Pass(context.Background())
	if len(f.reports.all()) != 1 || len(f.tracker.noted()) != 1 {
		t.Errorf("the same missing notes were handed back again after main moved")
	}
}

// Notes older than the decision are not this release's preparation.
func TestNotesThatPredateTheDecisionAreHandedBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.tracker.items[0].CreatedAt = time.Now().Add(time.Hour)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "before the release was decided") {
		t.Fatalf("step = %+v, want the old notes refused", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" {
		t.Errorf("old notes ran the gates or pushed a tag")
	}
}

// A stale stamp is the gate script's to find, and it hands the release back
// rather than opening anything.
func TestAStaleStampHandsTheReleaseBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.gate(t, "preparation")
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "readiness result") {
		t.Fatalf("step = %+v, want the stale stamp refused", step)
	}
	records := f.records(t)
	if len(records) != 1 || records[0].State != runstate.ReleaseCutRefused || !records[0].Preparation {
		t.Fatalf("records = %+v, want one refusal over preparation", records)
	}
	if len(f.reports.all()) != 1 || f.originTag(t) != "" {
		t.Errorf("want the release handed back with no tag")
	}
}

// A red gate writes no tag, goes on the item and not to the channel, and the
// same commit is not gated again; once main moves it is.
func TestARedGateWritesNoTagAndWaitsForMainToMove(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.gate(t, "red")
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "make check is red") {
		t.Fatalf("step = %+v, want the red gate said", step)
	}
	if f.originTag(t) != "" || git(t, f.repository, "tag", "--list") != "" {
		t.Fatalf("a red gate left a tag")
	}
	if len(f.reports.all()) != 0 || len(f.tracker.noted()) != 1 {
		t.Errorf("reports %d notes %d, want the item told and the channel not", len(f.reports.all()), len(f.tracker.noted()))
	}
	again := f.cutter.Pass(context.Background())
	if again.Outcome != runstate.StepSkipped || f.runner.gateRuns() != 1 {
		t.Errorf("second pass = %+v after %d gate runs, want the same commit not gated again", again, f.runner.gateRuns())
	}

	f.moveMain(t)
	f.gate(t, "green")
	cut := f.cutter.Pass(context.Background())
	if cut.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.head(t) {
		t.Errorf("after main moved = %+v, want it cut on the new commit", cut)
	}
}

// intent records a cut whose gates passed, as an interrupted pass leaves it.
func (f *fixture) intent(t *testing.T, commit string) runstate.ReleaseCut {
	t.Helper()
	digest := git(t, f.repository, "rev-parse", commit+":docs/releases/v0.6.0.md")
	recorded := runstate.ReleaseCut{
		SchemaVersion: runstate.ReleaseCutSchemaVersion, ProductID: "calc", Version: "v0.6.0",
		WorkItemID: "calc-fvn", Commit: commit, State: runstate.ReleaseCutIntent,
		ReadinessVerdict: "ready", ReadinessDefinition: "release-readiness@abc123", NotesDigest: digest,
		Gates: gates, CutBy: "an earlier pass", RecordedAt: f.now, UpdatedAt: f.now,
	}
	if err := f.cuts.Put(recorded); err != nil {
		t.Fatal(err)
	}
	return recorded
}

// A local tag origin does not have is pushed, without the gates again.
func TestALocalTagNotOnOriginIsPushed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	commit := f.head(t)
	f.intent(t, commit)
	git(t, f.repository, "tag", "-a", "v0.6.0", "-m", "v0.6.0", commit)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+commit {
		t.Fatalf("step = %+v, origin %q, want the tag pushed", step, f.originTag(t))
	}
	if f.runner.gateRuns() != 0 {
		t.Errorf("recovering a recorded intent ran the gates again")
	}
}

// Origin already holding the tag on the intent's commit is a push that landed
// unseen, and is adopted.
func TestAMatchingTagOnOriginIsAdopted(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	commit := f.head(t)
	f.intent(t, commit)
	git(t, f.repository, "tag", "-a", "v0.6.0", "-m", "v0.6.0", commit)
	git(t, f.repository, "push", "-q", "origin", "refs/tags/v0.6.0")
	git(t, f.repository, "tag", "-d", "v0.6.0")
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "already holds v0.6.0") {
		t.Fatalf("step = %+v, want the tag adopted", step)
	}
	if records := f.records(t); records[0].State != runstate.ReleaseCutPushed {
		t.Errorf("record = %+v, want it pushed", records[0])
	}
}

// A tag of the version on another commit is somebody's: the cut is held for
// the Lead Product Manager and nothing is moved or deleted.
func TestAConflictingTagHoldsTheCutAndMovesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	commit := f.head(t)
	f.intent(t, commit)
	f.moveMain(t)
	other := f.head(t)
	git(t, f.repository, "tag", "-a", "v0.6.0", "-m", "v0.6.0", other)
	git(t, f.repository, "push", "-q", "origin", "refs/tags/v0.6.0")
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "choose a new version") {
		t.Fatalf("step = %+v, want the cut held", step)
	}
	if got := f.originTag(t); got != "refs/tags/v0.6.0 "+other {
		t.Errorf("origin's tag = %q, want it left on %s", got, other)
	}
	if records := f.records(t); records[0].State != runstate.ReleaseCutBlocked {
		t.Errorf("record = %+v, want it blocked", records[0])
	}
	if filed := f.reports.all(); len(filed) != 1 || !strings.Contains(filed[0].Message, "Lead Product Manager has to choose") {
		t.Errorf("reports = %+v, want the hold reported", filed)
	}
	later := f.cutter.Pass(context.Background())
	if later.Outcome != runstate.StepSkipped || len(f.reports.all()) != 1 {
		t.Errorf("a later pass = %+v, want the held release left alone and not reported again", later)
	}
}

// No tag anywhere and main moved: the gates describe nothing that would be
// tagged, so the intent is stale and the next cut runs them again.
func TestAnIntentWithNoTagGoesStaleOnceMainMoves(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.intent(t, f.head(t))
	f.moveMain(t)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "cuts it afresh") {
		t.Fatalf("step = %+v, want the intent stale", step)
	}
	if f.originTag(t) != "" || len(f.reports.all()) != 0 {
		t.Errorf("a stale intent tagged or reported")
	}
	next := f.cutter.Pass(context.Background())
	if next.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.head(t) || f.runner.gateRuns() != 1 {
		t.Errorf("next pass = %+v, want a fresh cut with the gates run on the new commit", next)
	}
}

// No tag anywhere and main where it was: the same intent is tried again.
func TestAnIntentWithNoTagIsTriedAgainWhileMainHolds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	commit := f.head(t)
	f.intent(t, commit)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+commit || f.runner.gateRuns() != 0 {
		t.Fatalf("step = %+v, want the same intent tagged and pushed without the gates", step)
	}
}

// An origin that cannot be read is waited out, counted, and the third pass
// holds the cut for the Lead Product Manager.
func TestAnUnreadableOriginIsCountedAndTheThirdPassHoldsTheCut(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.intent(t, f.head(t))
	git(t, f.repository, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	for turn := 1; turn < MaxUnconfirmedTurns; turn++ {
		step := f.cutter.Pass(context.Background())
		if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "tried again on the next pass") {
			t.Fatalf("turn %d = %+v, want it waited out", turn, step)
		}
	}
	if len(f.reports.all()) != 0 {
		t.Fatalf("an unreadable origin was reported before the bound")
	}
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "3 passes could not confirm") {
		t.Fatalf("third turn = %+v, want the cut held", step)
	}
	if records := f.records(t); records[0].State != runstate.ReleaseCutBlocked {
		t.Errorf("record = %+v, want it blocked", records[0])
	}
}

func TestDecidedReadsOnlyTheLeadProductManagersOpenReleases(t *testing.T) {
	t.Parallel()
	pm := domain.WorkItemOrigin{Asker: domain.AskerSweep, AdmittedBy: domain.RoleProductManager}
	items := []beads.WorkItem{
		{ID: "a", Title: "Cut v0.10.0: later", Status: "open", Origin: pm},
		{ID: "b", Title: "Cut v0.6.0: earlier", Status: "blocked", Origin: pm},
		{ID: "c", Title: "Cut v0.5.0: done", Status: "closed", Origin: pm},
		{ID: "d", Title: "Cut v0.7.0: from a report", Status: "open", Origin: domain.WorkItemOrigin{Asker: domain.AskerReport, AdmittedBy: domain.RoleDevelopmentManager}},
		{ID: "e", Title: "Release notes carry descriptions", Status: "open", Origin: pm},
	}
	var ids []string
	for _, item := range Decided(items) {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "b,a" {
		t.Errorf("decided = %v, want the open releases the Lead Product Manager admitted, lowest version first", ids)
	}
}

func TestStampReadsTheVerdictAndDefinition(t *testing.T) {
	t.Parallel()
	verdict, definition, err := Stamp(stamped)
	if err != nil || verdict != "ready" || definition != "release-readiness@abc123" {
		t.Errorf("Stamp = %q, %q, %v", verdict, definition, err)
	}
	if _, _, err := Stamp("# notes with no section\n"); err == nil {
		t.Errorf("Stamp read a verdict from notes with none")
	}
}
