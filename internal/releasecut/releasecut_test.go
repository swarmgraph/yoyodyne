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
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
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
	// named is the commit the notes name: the first commit, which the notes
	// landed after.
	named   string
	tracker *fakeTracker
	reports *fakeReports
	cuts    *runstate.ReleaseCutStore
	runner  *countingRunner
	now     time.Time
}

const stamped = `# v0.6.0

Everything up to and including commit ` + "`NAMED`" + `.

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

// newFixture builds a repository whose main is on a bare origin, with a stub
// gate script whose answer the test sets: the real scripts/cut-release.sh is
// its own suite's claim, and what is claimed here is what the harness does
// with each way it can end. With notes, a second commit lands notes for v0.6.0
// naming the first, as a release's notes land after the commit they name.
func newFixture(t *testing.T, notes bool) *fixture {
	t.Helper()
	return buildFixture(t, notes, true)
}

// publication writes the release workflow and release-body script a commit
// carries; reading notes from main is what lets a tag there publish notes that
// landed after it.
func publication(t *testing.T, repository string, fromMain bool) {
	t.Helper()
	workflow, body := "run: bash scripts/release-body.sh \"$TAG\" body.md\n", "notes=\"docs/releases/$1.md\"\n"
	if fromMain {
		workflow = "run: RELEASE_NOTES_FROM=\"origin/$DEFAULT_BRANCH\" bash scripts/release-body.sh \"$TAG\" body.md\n"
		body += "notes_from=\"${RELEASE_NOTES_FROM:-origin/main}\"\n"
	}
	for path, text := range map[string]string{releaseWorkflow: workflow, releaseBodyScript: body} {
		if err := os.MkdirAll(filepath.Join(repository, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repository, path), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// buildFixture is newFixture, with the commit the notes name carrying a
// release workflow that reads notes from main, or one from before it did.
func buildFixture(t *testing.T, notes, publishes bool) *fixture {
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
grep -q "up to and including commit" "$YOYODYNE_RELEASE_NOTES" || { echo "cut-release: the harness handed over no notes from main" >&2; exit 9; }
echo "=== gates green"
echo "checked commit: $(git -C "$(dirname "$0")/.." rev-parse HEAD)"
`
	if err := os.WriteFile(filepath.Join(f.repository, "scripts", "cut-release.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	publication(t, f.repository, publishes)
	git(t, f.repository, "add", "-A")
	git(t, f.repository, "commit", "-qm", "the commit a release would name")
	f.named = f.head(t)
	if !publishes {
		// The fix that reads notes from main lands after the named commit, as
		// the release-step fix landed after abb18900.
		publication(t, f.repository, true)
		git(t, f.repository, "add", "-A")
		git(t, f.repository, "commit", "-qm", "the release reads its notes from main")
	}
	if notes {
		f.landNotes(t, strings.ReplaceAll(stamped, "NAMED", f.named))
	}
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

// landNotes commits the release's notes on main, after whatever they name.
func (f *fixture) landNotes(t *testing.T, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.repository, "docs", "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repository, "docs", "releases", "v0.6.0.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, f.repository, "add", "-A")
	git(t, f.repository, "commit", "-qm", "the notes land after the commit they name")
}

// renameNotes lands notes naming commit on origin's main.
func (f *fixture) renameNotes(t *testing.T, commit string) {
	t.Helper()
	f.landNotes(t, strings.ReplaceAll(stamped, "NAMED", commit))
	git(t, f.repository, "push", "-q", "origin", "main")
}

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
	commit, main := f.named, f.head(t)
	if commit == main {
		t.Fatalf("the fixture's notes name main's tip, so nothing here tells the named commit from it")
	}

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
	if len(records) != 1 || records[0].State != runstate.ReleaseCutPushed || records[0].Commit != commit || records[0].MainCommit != main ||
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
	if cut.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.named {
		t.Errorf("after main moved = %+v, want it cut on the commit the notes name", cut)
	}
}

// intent records a cut whose gates passed, as an interrupted pass leaves it.
func (f *fixture) intent(t *testing.T, commit string) runstate.ReleaseCut {
	t.Helper()
	main := f.head(t)
	digest := git(t, f.repository, "rev-parse", main+":docs/releases/v0.6.0.md")
	recorded := runstate.ReleaseCut{
		SchemaVersion: runstate.ReleaseCutSchemaVersion, ProductID: "calc", Version: "v0.6.0",
		WorkItemID: "calc-fvn", Commit: commit, MainCommit: main, State: runstate.ReleaseCutIntent,
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
	commit := f.named
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
	commit := f.named
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
	commit := f.named
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

// No tag anywhere and the notes on main now name another commit: the gates
// describe nothing that would be tagged, so the intent is stale and the next
// cut runs them again on the commit the notes name now.
func TestAnIntentWithNoTagGoesStaleOnceTheNotesNameAnotherCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.intent(t, f.named)
	f.moveMain(t)
	renamed := f.head(t)
	f.renameNotes(t, renamed)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "cuts it afresh") || !strings.Contains(step.Detail, "now names") {
		t.Fatalf("step = %+v, want the intent stale on the renamed commit", step)
	}
	if f.originTag(t) != "" || len(f.reports.all()) != 0 {
		t.Errorf("a stale intent tagged or reported")
	}
	next := f.cutter.Pass(context.Background())
	if next.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+renamed || f.runner.gateRuns() != 1 {
		t.Errorf("next pass = %+v, want a fresh cut with the gates run on the commit the notes name now", next)
	}
}

// No tag anywhere and main moved on with the notes still naming the intent's
// commit: the gates still describe what would be tagged, so it is tagged
// without running them again.
func TestAnIntentWithNoTagIsTriedAgainAfterMainMovesWhileTheNotesStillNameIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.intent(t, f.named)
	f.moveMain(t)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.named || f.runner.gateRuns() != 0 {
		t.Fatalf("step = %+v, origin %q, want the same intent tagged without the gates", step, f.originTag(t))
	}
}

// Notes that name no commit say nothing about where to tag: the release goes
// back to the Lead Product Manager with that said plainly, and no gate runs.
func TestNotesNamingNoCommitAreHandedBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t, false)
	f.landNotes(t, strings.ReplaceAll(stamped, "Everything up to and including commit `NAMED`.\n", ""))
	git(t, f.repository, "push", "-q", "origin", "main")
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "name no commit for the tag") ||
		!strings.Contains(step.Detail, "up to and including commit") {
		t.Fatalf("step = %+v, want the notes refused for naming no commit, saying how to name one", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" || git(t, f.repository, "tag", "--list") != "" {
		t.Errorf("notes naming no commit ran the gates or wrote a tag")
	}
	if records := f.records(t); len(records) != 1 || records[0].State != runstate.ReleaseCutRefused || !records[0].Preparation {
		t.Errorf("records = %+v, want one refusal over preparation", records)
	}
	if filed := f.reports.all(); len(filed) != 1 || !strings.Contains(filed[0].Message, "name no commit") {
		t.Errorf("reports = %+v, want the release handed back once, saying why", filed)
	}
}

// Notes that name a commit origin's main does not hold would tag work the
// product does not have, whether the commit is somewhere else here or nowhere.
func TestNotesNamingACommitNotOnMainAreHandedBack(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		commit func(t *testing.T, f *fixture) string
	}{
		{"a commit on another branch", func(t *testing.T, f *fixture) string {
			git(t, f.repository, "checkout", "-q", "-b", "elsewhere")
			git(t, f.repository, "commit", "-q", "--allow-empty", "-m", "work main never took")
			commit := f.head(t)
			git(t, f.repository, "checkout", "-q", "main")
			return commit
		}},
		{"a commit nobody has", func(*testing.T, *fixture) string { return strings.Repeat("ab", 20) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, false)
			commit := test.commit(t, f)
			f.renameNotes(t, commit)
			step := f.cutter.Pass(context.Background())
			if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "names commit "+commit[:12]+", which is not on origin/main") {
				t.Fatalf("step = %+v, want the notes refused for naming a commit main does not hold", step)
			}
			if f.runner.gateRuns() != 0 || f.originTag(t) != "" || git(t, f.repository, "tag", "--list") != "" {
				t.Errorf("notes naming a commit off main ran the gates or wrote a tag")
			}
			if filed := f.reports.all(); len(filed) != 1 || !strings.Contains(filed[0].Message, "which is not on origin/main") {
				t.Errorf("reports = %+v, want the release handed back once, saying why", filed)
			}
		})
	}
}

// Notes naming a commit from before the release workflow read notes from main
// would publish a release with no notes, since the notes land after the commit
// they name: the cut is refused before any release check runs, the item is told
// why once, and nothing is tagged however often main moves.
func TestNotesNamingACommitThatCannotPublishThemAreHandedBack(t *testing.T) {
	t.Parallel()
	f := buildFixture(t, true, false)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "names commit "+f.named[:12]) ||
		!strings.Contains(step.Detail, "published without its notes") || !strings.Contains(step.Detail, "does not read notes from main") {
		t.Fatalf("step = %+v, want the cut refused because the named commit cannot publish its notes", step)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" || git(t, f.repository, "tag", "--list") != "" {
		t.Fatalf("a commit that cannot publish its notes ran the release checks or was tagged")
	}
	notes := f.tracker.noted()
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "calc-fvn: The harness did not cut v0.6.0") || !strings.Contains(notes[0], "published without its notes") {
		t.Errorf("notes = %q, want the release item told plainly why", notes)
	}
	if filed := f.reports.all(); len(filed) != 1 || !strings.Contains(filed[0].Message, "published without its notes") {
		t.Errorf("reports = %+v, want the release handed back once", filed)
	}
	f.moveMain(t)
	f.cutter.Pass(context.Background())
	if f.originTag(t) != "" || len(f.tracker.noted()) != 1 || len(f.reports.all()) != 1 {
		t.Errorf("after main moved: tag %q, %d notes, %d reports, want no tag and nothing said again", f.originTag(t), len(f.tracker.noted()), len(f.reports.all()))
	}

	// Notes naming a commit that carries the fix are cut there.
	fixed := strings.SplitN(git(t, f.repository, "rev-list", "--reverse", f.named+"..HEAD"), "\n", 2)[0]
	f.renameNotes(t, fixed)
	cut := f.cutter.Pass(context.Background())
	if cut.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+fixed {
		t.Errorf("notes naming a commit that publishes them = %+v, origin %q, want it cut there", cut, f.originTag(t))
	}
}

// An intent recorded for a commit that cannot publish its notes, as one
// recorded before that was checked, writes no tag and goes stale.
func TestAnIntentOnACommitThatCannotPublishItsNotesGoesStale(t *testing.T) {
	t.Parallel()
	f := buildFixture(t, true, false)
	f.intent(t, f.named)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "published without its notes") {
		t.Fatalf("step = %+v, want the intent refused", step)
	}
	if f.originTag(t) != "" || git(t, f.repository, "tag", "--list") != "" {
		t.Fatalf("an intent on a commit that cannot publish its notes was tagged")
	}
	if records := f.records(t); len(records) != 1 || records[0].State != runstate.ReleaseCutStale {
		t.Errorf("records = %+v, want the intent stale", records)
	}
}

// A parked release item is the Lead Product Manager's to release: the pass
// tags nothing for it, finishes no cut recorded for it, and writes nothing on
// it, not even what an earlier pass owed it.
func TestAParkedReleaseIsNeitherCutNorWrittenOn(t *testing.T) {
	t.Parallel()
	const reason = "Lead PM: the release would lack its notes; release once the release step is fixed"
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, f *fixture)
	}{
		{"notes ready to cut", func(*testing.T, *fixture) {}},
		{"a cut recorded and not yet tagged", func(t *testing.T, f *fixture) { f.intent(t, f.named) }},
		{"a refusal not yet said", func(t *testing.T, f *fixture) {
			if err := f.cuts.Put(runstate.ReleaseCut{
				SchemaVersion: runstate.ReleaseCutSchemaVersion, ProductID: "calc", Version: "v0.6.0",
				WorkItemID: "calc-fvn", Commit: f.named, MainCommit: f.head(t), State: runstate.ReleaseCutRefused,
				Detail: "make check was red", CutBy: "an earlier pass", RecordedAt: f.now, UpdatedAt: f.now,
			}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, true)
			f.tracker.items[0].Parking = domain.WorkItemParking(reason)
			test.setup(t, f)
			for turn := 0; turn < 2; turn++ {
				step := f.cutter.Pass(context.Background())
				if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "v0.6.0 is parked") || !strings.Contains(step.Detail, reason) {
					t.Fatalf("pass %d = %+v, want it skipped naming the parking", turn, step)
				}
				f.moveMain(t)
			}
			if f.runner.gateRuns() != 0 || f.originTag(t) != "" || git(t, f.repository, "tag", "--list") != "" {
				t.Errorf("a parked release ran the release checks or was tagged")
			}
			if notes, filed := f.tracker.noted(), f.reports.all(); len(notes) != 0 || len(filed) != 0 {
				t.Errorf("a parked release was written on: notes %q, reports %+v", notes, filed)
			}

			f.tracker.items[0].Parking = ""
			if released := f.cutter.Pass(context.Background()); f.originTag(t) != "refs/tags/v0.6.0 "+f.named {
				t.Errorf("once released = %+v, want the pass to act on it again", released)
			}
		})
	}
}

func TestNamedCommitReadsTheOneCommitTheNotesName(t *testing.T) {
	t.Parallel()
	whole := strings.Repeat("0123456789", 4)
	if got, err := NamedCommit("covering everything up to and including commit `" + whole + "`, of today"); err != nil || got != whole {
		t.Errorf("NamedCommit = %q, %v, want %s", got, err, whole)
	}
	twice := "up to and including commit `" + whole + "` ... again up to and including commit `" + whole + "`"
	if got, err := NamedCommit(twice); err != nil || got != whole {
		t.Errorf("the same commit named twice = %q, %v, want it read", got, err)
	}
	if _, err := NamedCommit("up to and including commit `" + whole + "` or up to and including commit `" + strings.Repeat("f", 40) + "`"); err == nil {
		t.Errorf("notes naming two commits were read as naming one")
	}
	if _, err := NamedCommit("up to and including commit `abb18900`"); err == nil {
		t.Errorf("an abbreviated commit name was read as the commit to tag")
	}
}

// No tag anywhere and main where it was: the same intent is tried again.
func TestAnIntentWithNoTagIsTriedAgainWhileMainHolds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	commit := f.named
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
	f.intent(t, f.named)
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

// timingOutCheckout stops "git worktree add" part way, as a checkout that ran
// past its bound does: the folder is there with some of the tree in it, and
// Git records no worktree for it.
type timingOutCheckout struct{ *countingRunner }

func (r timingOutCheckout) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if command.Name == "git" && len(command.Args) > 2 && command.Args[0] == "worktree" && command.Args[1] == "add" {
		path := command.Args[len(command.Args)-2]
		if err := os.MkdirAll(filepath.Join(path, "scripts"), 0o755); err != nil {
			return execution.ProcessResult{}, err
		}
		if err := os.WriteFile(filepath.Join(path, "scripts", "half-written"), []byte("x"), 0o644); err != nil {
			return execution.ProcessResult{}, err
		}
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1, Stderr: "the checkout ran past its time"}, nil
	}
	return r.countingRunner.Run(ctx, command, observer)
}

// leftover puts a plain folder with something in it where the cut's checkout
// goes, as a checkout that stopped part way leaves one.
func (f *fixture) leftover(t *testing.T) string {
	t.Helper()
	path := filepath.Join(f.cutter.WorkRoot, "v0.6.0")
	if err := os.MkdirAll(filepath.Join(path, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "scripts", "half-written"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A folder Git does not know as a worktree, left where the checkout goes, is
// removed by the next cut, which goes on and cuts.
func TestALeftoverFolderIsRemovedAndTheCutGoesOn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.leftover(t)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.named {
		t.Fatalf("step = %+v, origin %q, want the leftover cleared and the release cut", step, f.originTag(t))
	}
	if entries, _ := os.ReadDir(f.cutter.WorkRoot); len(entries) != 0 {
		t.Errorf("the folder release checkouts are made in still holds %v", entries)
	}
}

// A worktree an interrupted checkout left, still locked by Git, is removed
// through Git and the cut goes on.
func TestALockedWorktreeLeftBehindIsRemovedThroughGit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	path := filepath.Join(f.cutter.WorkRoot, "v0.6.0")
	git(t, f.repository, "worktree", "add", "-q", "--detach", path, f.named)
	git(t, f.repository, "worktree", "lock", path)
	step := f.cutter.Pass(context.Background())
	if step.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.named {
		t.Fatalf("step = %+v, want the locked worktree removed and the release cut", step)
	}
	if strings.Contains(git(t, f.repository, "worktree", "list", "--porcelain"), "v0.6.0") {
		t.Errorf("Git still lists the cut's worktree")
	}
}

// A checkout that times out leaves no folder behind, the failure says what was
// removed and asks nobody to remove anything, and the next pass cuts.
func TestACheckoutThatTimesOutLeavesNoFolderBehind(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.cutter.Runner = timingOutCheckout{f.runner}
	step := f.cutter.Pass(context.Background())
	if step.Outcome == runstate.StepRan || !strings.Contains(step.Detail, "timed_out") {
		t.Fatalf("step = %+v, want the cut failed on the checkout's time", step)
	}
	path := filepath.Join(f.cutter.WorkRoot, "v0.6.0")
	if !strings.Contains(step.Detail, "the harness removed the leftover folder "+path) || strings.Contains(step.Detail, "remove it") {
		t.Errorf("detail = %q, want it to name the folder removed and ask nobody to remove it", step.Detail)
	}
	if entries, _ := os.ReadDir(f.cutter.WorkRoot); len(entries) != 0 {
		t.Fatalf("a checkout that timed out left %v behind", entries)
	}

	f.cutter.Runner = f.runner
	if again := f.cutter.Pass(context.Background()); f.originTag(t) != "refs/tags/v0.6.0 "+f.named {
		t.Errorf("the pass after the timeout = %+v, want it cut", again)
	}
}

// A link where the checkout goes is refused and named, and neither it nor what
// it points at is deleted.
func TestALinkWhereTheCheckoutGoesIsRefusedAndKept(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	outside := t.TempDir()
	kept := filepath.Join(outside, "keep")
	if err := os.WriteFile(kept, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.cutter.WorkRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.cutter.WorkRoot, "v0.6.0")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	step := f.cutter.Pass(context.Background())
	if step.Outcome == runstate.StepRan || !strings.Contains(step.Detail, link+" is a link") || !strings.Contains(step.Detail, "removed nothing") {
		t.Fatalf("step = %+v, want the link refused and named", step)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the link was removed: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("what the link points at was removed: %v", err)
	}
	if f.runner.gateRuns() != 0 || f.originTag(t) != "" {
		t.Errorf("a cut ran past a link where its checkout goes")
	}
}

// A name that is not one entry of the folder release checkouts are made in is refused and named,
// and nothing outside the folder is deleted.
func TestAPathOutsideTheReleaseCutFolderIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	if err := os.MkdirAll(f.cutter.WorkRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(f.cutter.WorkRoot), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(f.cutter.WorkRoot)
	if err != nil {
		t.Fatal(err)
	}
	root, err := repowrite.OpenPinnedRoot(resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"../outside", "..", "", "a/b"} {
		cleared := f.cutter.clearCheckout(context.Background(), root, name)
		if cleared.err == nil || !strings.Contains(cleared.err.Error(), "the folder release checkouts are made in") || len(cleared.removed) != 0 {
			t.Errorf("clearing %q = %+v, want it refused and named", name, cleared)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a folder outside the folder release checkouts are made in was removed: %v", err)
	}
}

// A worktree Git still holds locked, as a killed checkout leaves it, is cleared
// though its .git file, or the whole folder, is already gone.
func TestALockedWorktreeHalfRemovedIsClearedAndTheCutGoesOn(t *testing.T) {
	t.Parallel()
	for _, gone := range []string{".git", "."} {
		t.Run(gone, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, true)
			path := filepath.Join(f.cutter.WorkRoot, "v0.6.0")
			git(t, f.repository, "worktree", "add", "-q", "--detach", path, f.named)
			git(t, f.repository, "worktree", "lock", "--reason", "initializing", path)
			if err := os.RemoveAll(filepath.Join(path, gone)); err != nil {
				t.Fatal(err)
			}
			step := f.cutter.Pass(context.Background())
			if step.Outcome != runstate.StepRan || f.originTag(t) != "refs/tags/v0.6.0 "+f.named {
				t.Fatalf("step = %+v, want what was left cleared and the release cut", step)
			}
			if entries, _ := os.ReadDir(f.cutter.WorkRoot); len(entries) != 0 {
				t.Errorf("the folder release checkouts are made in still holds %v", entries)
			}
		})
	}
}

// timingOutGates answers the release script as one that ran past its bound,
// after during has had the checkout.
type timingOutGates struct {
	*countingRunner
	during func(path string) error
}

func (r timingOutGates) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if command.Name != "bash" {
		return r.countingRunner.Run(ctx, command, observer)
	}
	if err := r.during(command.Dir); err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
}

// Release checks that time out leave no folder behind, and the recorded refusal
// says what was removed.
func TestReleaseChecksThatTimeOutLeaveNoFolderAndSayWhatWasRemoved(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.cutter.Runner = timingOutGates{f.runner, func(path string) error {
		return os.WriteFile(filepath.Join(path, "built"), []byte("x"), 0o644)
	}}
	step := f.cutter.Pass(context.Background())
	path := filepath.Join(f.cutter.WorkRoot, "v0.6.0")
	records := f.records(t)
	if len(records) != 1 || !strings.Contains(records[0].Detail, "timed_out") ||
		!strings.Contains(records[0].Detail, "the harness removed the checkout "+path+", so the next pass starts clean") || !strings.Contains(step.Detail, records[0].Detail) {
		t.Errorf("step = %+v, records = %+v, want the timeout and what was removed said", step, records)
	}
	if entries, _ := os.ReadDir(f.cutter.WorkRoot); len(entries) != 0 {
		t.Errorf("release checks that timed out left %v behind", entries)
	}
}

// A checkout that cannot be cleared after the release checks fail is said in
// the refusal, which does not then claim the next pass starts clean.
func TestACheckoutThatCannotBeClearedIsSaidInTheRefusal(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	leftover := f.leftover(t)
	outside := t.TempDir()
	f.cutter.Runner = timingOutGates{f.runner, func(path string) error {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return os.Symlink(outside, path)
	}}
	f.cutter.Pass(context.Background())
	records := f.records(t)
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one refusal", records)
	}
	detail := records[0].Detail
	if !strings.Contains(detail, "the harness removed the leftover folder "+leftover) || !strings.Contains(detail, leftover+" is a link") ||
		strings.Contains(detail, "starts clean") {
		t.Errorf("detail = %q, want what was removed and what could not be, and no claim the path is clear", detail)
	}
	if _, err := os.Lstat(leftover); err != nil {
		t.Errorf("the link was removed: %v", err)
	}
}

// The same red check is said on the item once, though one pass had more to
// clear than the other.
func TestTheSameRedCheckIsSaidOnceWhateverWasCleared(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true)
	f.gate(t, "red")
	f.leftover(t)
	f.cutter.Pass(context.Background())
	f.moveMain(t)
	f.cutter.Pass(context.Background())
	if f.runner.gateRuns() != 2 || len(f.tracker.noted()) != 1 {
		t.Errorf("%d runs of the release checks said %q, want two runs said once", f.runner.gateRuns(), f.tracker.noted())
	}
}
