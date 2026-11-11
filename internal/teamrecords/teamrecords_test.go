package teamrecords_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamrecords"
)

var epoch = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

type factory struct {
	name    string
	clone   string
	records *gitworktree.RecordsRepository
	reports *runstate.ReportStore
	dirs    *runstate.DirectiveStore
	state   *runstate.SharedRecordsStore
	sharer  teamrecords.Sharer
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	var out, errs bytes.Buffer
	command.Stdout, command.Stderr = &out, &errs
	if err := command.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, errs.String())
	}
	return strings.TrimSpace(out.String())
}

// newFactories makes a bare remote and two clones of it, each a factory with
// stores and a records repository of its own.
func newFactories(t *testing.T) (a, b *factory, remote string) {
	t.Helper()
	base := t.TempDir()
	remote = filepath.Join(base, "remote.git")
	git(t, base, "init", "--bare", "--quiet", remote)
	seed := filepath.Join(base, "seed")
	git(t, base, "init", "--quiet", seed)
	git(t, seed, "commit", "--allow-empty", "--quiet", "-m", "seed")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	var factories []*factory
	for _, name := range []string{"factory A", "factory B"} {
		clone := filepath.Join(base, strings.ReplaceAll(name, " ", "-"))
		git(t, base, "clone", "--quiet", remote, clone)
		git(t, clone, "config", "maintenance.auto", "false")
		git(t, clone, "config", "gc.auto", "0")
		factories = append(factories, newFactory(t, name, clone))
	}
	return factories[0], factories[1], remote
}

func newFactory(t *testing.T, name, clone string) *factory {
	t.Helper()
	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: clone,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Remote:         "origin",
		Timeout:        2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	root := t.TempDir()
	state, err := runstate.NewSharedRecordsStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	records, err := manager.RecordsRepository(context.Background(), state.RepositoryDirectory())
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := runstate.NewDirectiveStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	f := &factory{name: name, clone: clone, records: records, reports: reports, dirs: dirs, state: state}
	f.sharer = teamrecords.Sharer{Remote: records, Reports: reports, Directives: dirs, State: state, Factory: name, Now: func() time.Time { return epoch }}
	return f
}

func (f *factory) sync(t *testing.T) teamrecords.Result {
	t.Helper()
	result, err := f.sharer.Sync(context.Background())
	if err != nil {
		t.Fatalf("%s Sync() error = %v", f.name, err)
	}
	return result
}

// snapshot is every path of the remote's records now, with its content.
func (f *factory) snapshot(t *testing.T) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	commit, err := f.records.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := f.records.Paths(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	var list []string
	for path := range paths {
		list = append(list, path)
	}
	contents, err := f.records.Read(ctx, commit, list)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func keepsEarlierRecords(t *testing.T, earlier, later map[string][]byte) {
	t.Helper()
	for path, content := range earlier {
		if got, ok := later[path]; !ok || !bytes.Equal(got, content) {
			t.Errorf("record %s was rewritten or removed", path)
		}
	}
}

func testReport(n int) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            fmt.Sprintf("report-%032x", n),
		Role:          "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityNote,
		Message:       fmt.Sprintf("something noticed, number %d", n),
		RecordedAt:    epoch.Add(time.Duration(n) * time.Minute),
	}
}

func testDirective(id string, receivedAt time.Time) directive.Directive {
	return directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    receivedAt,
		Text:          "hold the publishing change until I have decided",
		Unresolved:    "which of the two approaches",
	}
}

func reportIDs(t *testing.T, f *factory) map[string]int {
	t.Helper()
	listed, err := f.reports.List()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range listed {
		counts[r.ID]++
	}
	return counts
}

func TestTwoFactoriesSharingAtOnceEndWithEveryReportOnEachMachineOnce(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	for n := 1; n <= 3; n++ {
		if err := a.reports.Append(testReport(n)); err != nil {
			t.Fatal(err)
		}
		if err := b.reports.Append(testReport(n + 10)); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, f := range []*factory{a, b} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if _, err := f.sharer.Sync(context.Background()); err != nil {
				t.Errorf("%s Sync() error = %v", f.name, err)
			}
		}()
	}
	close(start)
	wait.Wait()
	a.sync(t)
	b.sync(t)
	for _, f := range []*factory{a, b} {
		ids := reportIDs(t, f)
		if len(ids) != 6 {
			t.Errorf("%s holds %d reports, want 6", f.name, len(ids))
		}
		for id, count := range ids {
			if count != 1 {
				t.Errorf("%s holds %s %d times", f.name, id, count)
			}
		}
	}
	snapshot := a.snapshot(t)
	if len(snapshot) != 6 {
		t.Errorf("the remote holds %d records, want 6", len(snapshot))
	}
	if result := b.sync(t); result.Copied != 0 || result.Shared != 0 || result.Waiting != 0 {
		t.Errorf("a settled check = %+v, want nothing to do", result)
	}
}

func TestOneSlackReplySeenByTwoFactoriesIsOneDirective(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	id := directive.ForSlackMessage("C0123", "1700000000.000100")
	type outcome struct {
		directive directive.Directive
		sharing   teamrecords.Sharing
		err       error
	}
	outcomes := make([]outcome, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, f := range []*factory{a, b} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			d, sharing, err := f.sharer.ShareDirective(context.Background(), testDirective(id, epoch.Add(time.Duration(index+1)*time.Second)))
			outcomes[index] = outcome{d, sharing, err}
		}()
	}
	close(start)
	wait.Wait()
	for index, got := range outcomes {
		if got.err != nil {
			t.Fatalf("factory %d ShareDirective() error = %v", index, got.err)
		}
		if got.directive.ID != id {
			t.Errorf("factory %d returned directive %s, want %s", index, got.directive.ID, id)
		}
	}
	shared, already := 0, 0
	for _, got := range outcomes {
		switch got.sharing {
		case teamrecords.SharingShared:
			shared++
		case teamrecords.SharingAlreadyRecorded:
			already++
		}
	}
	if shared != 1 || already != 1 {
		t.Errorf("outcomes = %q and %q, want one shared and one already-recorded", outcomes[0].sharing, outcomes[1].sharing)
	}
	files := 0
	for path := range a.snapshot(t) {
		if strings.HasPrefix(path, "directives/") {
			files++
		}
	}
	if files != 1 {
		t.Errorf("the remote holds %d directive files, want 1", files)
	}
	localA, err := a.dirs.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	localB, err := b.dirs.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one copy names the other factory: the loser's names the winner.
	switch {
	case localA.Factory == "" && localB.Factory == "factory A", localB.Factory == "" && localA.Factory == "factory B":
	default:
		t.Fatalf("factories = %q (A) and %q (B), want the loser's to name the winner and the winner's empty", localA.Factory, localB.Factory)
	}
	localA.Factory, localB.Factory = "", ""
	if !reflect.DeepEqual(localA, localB) {
		t.Errorf("copies differ beyond the factory:\n%+v\n%+v", localA, localB)
	}
}

func TestAnUnreachableRemoteKeepsRecordsHereAndTheNextTurnSharesThem(t *testing.T) {
	t.Parallel()
	a, b, remote := newFactories(t)
	git(t, a.clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	if err := a.reports.Append(testReport(1)); err != nil {
		t.Fatal(err)
	}
	result, err := a.sharer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Problem == "" || result.Waiting != 1 || result.Shared != 0 {
		t.Fatalf("Sync() = %+v, want a problem and one waiting", result)
	}
	if !strings.Contains(result.Says(), "1 filed here is not yet shared") {
		t.Errorf("Says() = %q", result.Says())
	}
	state, found, err := a.state.Load()
	if err != nil || !found || state.Waiting != 1 || state.Problem == "" || state.SharedAt != nil {
		t.Fatalf("state = %+v, %v, %v, want one waiting with the problem", state, found, err)
	}
	git(t, a.clone, "remote", "set-url", "origin", remote)
	result = a.sync(t)
	if result.Shared != 1 || result.Waiting != 0 || result.Problem != "" {
		t.Fatalf("Sync() after the remote returned = %+v, want one shared", result)
	}
	if state, _, _ := a.state.Load(); state.Waiting != 0 || state.Problem != "" || state.SharedAt == nil {
		t.Errorf("state = %+v, want nothing waiting", state)
	}
	if result := b.sync(t); result.Copied != 1 {
		t.Errorf("B Sync() = %+v, want one copied", result)
	}
}

func TestAReportTravelsToAnotherFactoryAndItsHandlingTravelsBack(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	filed := testReport(1)
	if err := a.reports.Append(filed); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	if result := b.sync(t); result.Copied != 1 {
		t.Fatalf("B Sync() = %+v, want one copied", result)
	}
	listed, err := b.reports.List()
	if err != nil || len(listed) != 1 {
		t.Fatalf("B List() = %v, %v", listed, err)
	}
	if listed[0].Factory != "factory A" || !strings.Contains(listed[0].Render(), "filed by another factory: factory A") {
		t.Errorf("B's report = %q (factory %q)", listed[0].Render(), listed[0].Factory)
	}
	if own, _ := a.reports.List(); len(own) != 1 || own[0].Factory != "" {
		t.Errorf("A's own report was marked as another's: %+v", own)
	}
	handling := report.Handling{
		SchemaVersion: report.HandlingSchemaVersion,
		ReportID:      filed.ID,
		Role:          "product-manager",
		RunID:         "conversation-0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Reason:        "nothing to do",
		RecordedAt:    epoch.Add(time.Hour),
	}
	if err := b.reports.Handle(handling); err != nil {
		t.Fatal(err)
	}
	b.sync(t)
	if result := a.sync(t); result.Copied != 1 {
		t.Fatalf("A Sync() = %+v, want one copied", result)
	}
	handlings, err := a.reports.Handlings()
	if err != nil || len(handlings) != 1 || handlings[0].Factory != "factory B" {
		t.Fatalf("A Handlings() = %+v, %v, want one from factory B", handlings, err)
	}
	// B's own handling is not shared again, and A's copy does not travel back.
	if result := b.sync(t); result.Copied != 0 || result.Shared != 0 {
		t.Errorf("B Sync() = %+v, want nothing", result)
	}
	if result := a.sync(t); result.Copied != 0 || result.Shared != 0 {
		t.Errorf("A Sync() = %+v, want nothing", result)
	}
}

func TestADirectiveResolvedAfterItWasSharedReachesTheOtherFactoryWithoutARewrite(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	id := "directive-" + strings.Repeat("ab", 16)
	if err := a.dirs.Record(testDirective(id, epoch)); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	b.sync(t)
	before := a.snapshot(t)
	file := "directives/" + id + ".json"
	if _, ok := before[file]; !ok {
		t.Fatalf("the remote holds %v, want %s", before, file)
	}

	if _, err := a.dirs.Resolve(id, "go with the first approach", epoch.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if result := a.sync(t); result.Shared != 1 {
		t.Fatalf("A Sync() = %+v, want the change shared", result)
	}
	after := a.snapshot(t)
	changes := 0
	for path := range after {
		if strings.HasPrefix(path, "directive-changes/") {
			changes++
		}
	}
	if changes != 1 {
		t.Errorf("the remote holds %d directive changes, want 1", changes)
	}
	keepsEarlierRecords(t, before, after)
	if !bytes.Equal(before[file], after[file]) {
		t.Errorf("the directive file was rewritten")
	}

	if result := b.sync(t); result.Copied != 1 {
		t.Fatalf("B Sync() = %+v, want the change copied", result)
	}
	got, err := b.dirs.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Resolved() || got.Resolution != "go with the first approach" || got.Factory != "factory A" {
		t.Errorf("B's copy = %+v, want it resolved with A's resolution", got)
	}
	// Nothing travels again: B's copy has no change of its own to share.
	if result := b.sync(t); result.Shared != 0 || result.Copied != 0 {
		t.Errorf("B Sync() = %+v, want nothing", result)
	}
	keepsEarlierRecords(t, after, b.snapshot(t))
}

func TestAnUnreadableRecordIsNamedAndTheRestStillArrive(t *testing.T) {
	t.Parallel()
	a, b, _ := newFactories(t)
	if err := a.reports.Append(testReport(1)); err != nil {
		t.Fatal(err)
	}
	// A file of the right place but the wrong content, written the way the
	// harness writes (the records repository), and a path nobody recognises.
	ctx := context.Background()
	commit, err := a.records.Build(ctx, "", map[string][]byte{
		"reports/" + testReport(2).ID + ".json": []byte("not json"),
		"spends/item/one.json":                  []byte("{}"),
	}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.records.Push(ctx, commit, ""); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	result := b.sync(t)
	if result.Copied != 1 || len(result.Unreadable) != 1 || !strings.HasPrefix(result.Unreadable[0], "reports/"+testReport(2).ID+".json: ") {
		t.Errorf("B Sync() = %+v, want one copied and the bad file named", result)
	}
	if _, err := os.Stat(b.reports.Path()); err != nil {
		t.Error(err)
	}
}
