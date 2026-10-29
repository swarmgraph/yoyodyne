package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	pinnedRecord  = "assets/chart.umd.js.yoyodyne-source.yaml"
	pinnedFile    = "assets/chart.umd.js"
	pinnedFor     = "yoyodyne-ifd.432.33"
	pinnedContent = "/*! Chart.js v4.4.1 | MIT */\nvar Chart = {};\n"
	// licenseContent is what the test server serves as the library's license.
	licenseContent = "The MIT License (MIT)\n\nCopyright (c) 2014-2022 Chart.js Contributors\n"
)

// collectedReports keeps what the harness reported.
type collectedReports struct {
	mu       sync.Mutex
	reported []report.Report
}

func (c *collectedReports) Append(reported report.Report) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reported = append(c.reported, reported)
	return nil
}

// noSuchItemTracker answers for the items it holds and refuses every other
// identifier the way the real client does, with beads.ErrNoSuchWorkItem.
type noSuchItemTracker struct {
	*orchestratortest.Tracker
}

func (t noSuchItemTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	item, err := t.Tracker.Show(ctx, id)
	if err != nil {
		return beads.WorkItem{}, fmt.Errorf("%w: %s", beads.ErrNoSuchWorkItem, id)
	}
	return item, nil
}

type movableClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *movableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *movableClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(by)
}

// chartServer serves the pinned file at a versioned path, and answers with a
// server error where down is set.
func chartServer(t *testing.T, down *bool) (*httptest.Server, string) {
	t.Helper()
	var requests sync.Mutex
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Lock()
		defer requests.Unlock()
		if down != nil && *down {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/npm/chart.js@4.4.1/dist/chart.umd.js":
			_, _ = w.Write([]byte(pinnedContent))
		case "/npm/chart.js@4.4.1/LICENSE.md":
			_, _ = w.Write([]byte(licenseContent))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "https://")
}

func pinnedRecordText(address string) string {
	return fmt.Sprintf("# Chart.js for the dashboard\nfile: chart.umd.js\nurl: %s/npm/chart.js@4.4.1/dist/chart.umd.js\nversion: 4.4.1\nlicense: MIT\nlicense_file: chart.js-LICENSE.md\nfor: %s\n", address, pinnedFor)
}

func writePinnedFixtureFile(t *testing.T, root, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// outsideFileFixture is an automatically integrating pipeline over a
// repository whose main carries a record for a file a test server serves, and
// a project listing that server's host.
func outsideFileFixture(t *testing.T, down *bool) (string, *orchestratortest.Tracker, *orchestratortest.Backend, Pipeline, *runstate.Store, *collectedReports, *movableClock) {
	t.Helper()
	repository, tracker, provider, pipeline, store := automaticFixture(t)
	tracker.Holds(pinnedFor)
	srv, host := chartServer(t, down)
	writePinnedFixtureFile(t, repository, pinnedRecord, pinnedRecordText(srv.URL))
	writePinnedFixtureFile(t, repository, "assets/chart.js-LICENSE.md", "The MIT License\n")
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "declare the chart library")
	pipeline.Config.OutsideFiles.Hosts = []string{host}
	clock := &movableClock{at: time.Date(2026, 10, 23, 16, 0, 0, 0, time.UTC)}
	pipeline.Clock = clock
	pipeline.OutsideFiles = outsidefile.Fetcher{Transport: srv.Client().Transport, Now: clock.Now}
	reports := &collectedReports{}
	pipeline.Reports = reports
	pipeline.Tracker = noSuchItemTracker{tracker}
	return repository, tracker, provider, pipeline, store, reports, clock
}

// A record on main from a listed host is fetched by the harness in its own
// process and lands through a reviewed run of its own: no developer is
// invoked, no developer slot is held, the change is the file and the record's
// harness fields, and the item the record is for is told.
func TestARecordOnMainIsBroughtInThroughAReviewedRunOfTheHarnessesOwn(t *testing.T) {
	t.Parallel()

	repository, tracker, provider, pipeline, store, _, _ := outsideFileFixture(t, nil)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %+v, %v, %v; want the record due", landing, due, err)
	}
	if landing.Record != pinnedRecord || landing.For != pinnedFor {
		t.Fatalf("NextOutsideFile() = %+v", landing)
	}
	outcome, err := pipeline.BringInOutsideFile(ctx, landing)
	if err != nil {
		t.Fatalf("BringInOutsideFile() error = %v (%+v)", err, outcome)
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("a developer was invoked %d times", got)
	}
	if got := len(provider.RequestsForRole(domain.RoleReviewer)); got != 1 {
		t.Fatalf("the reviewer was invoked %d times, want once", got)
	}
	review := provider.RequestsForRole(domain.RoleReviewer)[0].Prompt
	for _, want := range []string{"Final address after any redirects", "SHA-256: " + outsidefile.Checksum([]byte(pinnedContent)), "trusted on its first fetch", "chart.js-LICENSE.md, which is in the repository", "Chart.js v4.4.1"} {
		if !strings.Contains(review, want) {
			t.Errorf("the reviewer was not shown %q", want)
		}
	}
	saved, err := store.Load(landing.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != runstate.StatusSucceeded || saved.Integration == nil || saved.HoldsDeveloperSlot() || saved.PricedAgainst() != pinnedFor {
		t.Fatalf("run = %s, integration %+v; want it landed, holding no slot and priced against %s", saved.Status, saved.Integration, pinnedFor)
	}
	if err := saved.Validate(); err != nil {
		t.Fatalf("the run's record is not valid: %v", err)
	}
	landed := publicationGit(t, repository, "show", "main:"+pinnedFile)
	if strings.TrimSpace(landed) != strings.TrimSpace(pinnedContent) {
		t.Fatalf("main holds %q", landed)
	}
	record, err := outsidefile.ParseRecord(pinnedRecord, []byte(publicationGit(t, repository, "show", "main:"+pinnedRecord)))
	if err != nil {
		t.Fatal(err)
	}
	if record.SHA256 != outsidefile.Checksum([]byte(pinnedContent)) || record.Size != int64(len(pinnedContent)) || record.FetchedAt != "2026-10-23T16:00:00Z" || !strings.HasSuffix(record.FetchedFrom, "/npm/chart.js@4.4.1/dist/chart.umd.js") {
		t.Fatalf("main's record = %+v", record)
	}
	changed := strings.Fields(publicationGit(t, repository, "diff", "--name-only", "main~1", "main"))
	if strings.Join(changed, " ") != pinnedRecord+" "+pinnedFile && strings.Join(changed, " ") != pinnedFile+" "+pinnedRecord {
		t.Fatalf("the landing changed %v", changed)
	}
	if !strings.Contains(tracker.Notes, "brought in "+pinnedFile) {
		t.Fatalf("the item the record is for was told %q", tracker.Notes)
	}
	// Brought in, the record is no longer due.
	if _, due, err := pipeline.NextOutsideFile(ctx); err != nil || due {
		t.Fatalf("NextOutsideFile() after landing = %v, %v; want nothing due", due, err)
	}
}

// Nothing is due from a record that is not on main, or from a project that
// lists no host, and nothing is fetched for either.
func TestARecordNotOnMainAndAnEmptyHostListAreNeverFetched(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, _, _, _ := outsideFileFixture(t, nil)
	ctx := context.Background()
	empty := pipeline
	empty.Config.OutsideFiles.Hosts = nil
	if _, due, err := empty.NextOutsideFile(ctx); err != nil || due {
		t.Fatalf("NextOutsideFile() with no host listed = %v, %v; want nothing due", due, err)
	}
	// A second record written in the checkout and on a branch, but not on main.
	other := strings.Replace(pinnedRecordText("https://example.invalid"), "chart.umd.js", "other.js", 1)
	writePinnedFixtureFile(t, repository, "assets/other.js.yoyodyne-source.yaml", other)
	runPipelineGit(t, repository, "checkout", "-b", "proposal")
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "a record not yet on main")
	runPipelineGit(t, repository, "checkout", "main")
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || landing.Record != pinnedRecord {
		t.Fatalf("NextOutsideFile() = %+v, %v, %v; want only main's record", landing, due, err)
	}
}

// A record the rules refuse is stopped before any request, and reported once
// to the development manager rather than the operator.
func TestARefusedRecordIsStoppedAndReportedOnceToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	for name, edit := range map[string]func(string) string{
		"plain http": func(text string) string { return strings.Replace(text, "url: https://", "url: http://", 1) },
		"host not listed": func(text string) string {
			return strings.Replace(text, "url: https://", "url: https://cdn.example.invalid/", 1)
		},
		"query string": func(text string) string {
			return strings.Replace(text, "chart.umd.js\nversion", "chart.umd.js?x=1\nversion", 1)
		},
		"version not in path":  func(text string) string { return strings.Replace(text, "version: 4.4.1", "version: 4.4.2", 1) },
		"for names no item":    func(text string) string { return strings.Replace(text, "for: "+pinnedFor, "for: yoyodyne-ifd.0", 1) },
		"field does not parse": func(text string) string { return text + "\ncolour: blue\n" },
	} {
		t.Run(name, func(t *testing.T) {
			repository, _, _, pipeline, store, reports, _ := outsideFileFixture(t, nil)
			text := publicationGit(t, repository, "show", "main:"+pinnedRecord)
			writePinnedFixtureFile(t, repository, pinnedRecord, edit(text))
			runPipelineGit(t, repository, "commit", "-am", "a record the rules refuse")
			ctx := context.Background()
			for range 2 {
				if _, due, err := pipeline.NextOutsideFile(ctx); err != nil || due {
					t.Fatalf("NextOutsideFile() = %v, %v; want nothing due", due, err)
				}
			}
			attempt, found, err := store.OutsideFileAttempt(pinnedRecord)
			if err != nil || !found || attempt.Stopped == "" || !attempt.Reported {
				t.Fatalf("attempt = %+v, %v, %v; want the record stopped and reported", attempt, found, err)
			}
			if len(reports.reported) != 1 {
				t.Fatalf("reports = %d, want one", len(reports.reported))
			}
			filed := reports.reported[0]
			if filed.Severity != report.SeverityWarning || filed.Addressee() != domain.RoleDevelopmentManager || !strings.Contains(filed.Message, pinnedRecord) {
				t.Fatalf("report = %+v", filed)
			}
		})
	}
}

// A fetch whose file is over the size limit, or does not match the checksum
// the record gives, is refused and not written; the record is stopped.
func TestAnOversizedFileAndAChecksumMismatchAreRefusedAndNotWritten(t *testing.T) {
	t.Parallel()

	for name, set := range map[string]func(*Pipeline, string) string{
		"over the size limit": func(p *Pipeline, text string) string { p.Config.OutsideFiles.MaxBytes = 10; return text },
		"checksum mismatch": func(_ *Pipeline, text string) string {
			return text + "\nsha256: " + strings.Repeat("0", 64) + "\n"
		},
	} {
		t.Run(name, func(t *testing.T) {
			repository, _, provider, pipeline, store, reports, _ := outsideFileFixture(t, nil)
			text := publicationGit(t, repository, "show", "main:"+pinnedRecord)
			writePinnedFixtureFile(t, repository, pinnedRecord, set(&pipeline, text))
			runPipelineGit(t, repository, "commit", "-am", "the record as the test sets it")
			ctx := context.Background()
			landing, due, err := pipeline.NextOutsideFile(ctx)
			if err != nil || !due {
				t.Fatalf("NextOutsideFile() = %v, %v", due, err)
			}
			if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
				t.Fatalf("BringInOutsideFile() error = %v", err)
			}
			if _, err := store.Load(landing.RunID()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a run was recorded for a refused fetch: %v", err)
			}
			if len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
				t.Fatal("a refused fetch was reviewed")
			}
			if _, err := os.Stat(filepath.Join(repository, pinnedFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the refused file was written: %v", err)
			}
			attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
			if attempt.Stopped == "" || len(reports.reported) != 1 {
				t.Fatalf("attempt = %+v, reports %d; want the record stopped and one report", attempt, len(reports.reported))
			}
		})
	}
}

// A network that fails is waited out: no run is recorded, nothing is counted
// against the record, and it is due again once the wait has passed.
func TestANetworkFailureIsRetriedLaterWithoutBeingCounted(t *testing.T) {
	t.Parallel()

	down := true
	_, _, _, pipeline, store, reports, clock := outsideFileFixture(t, &down)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %v, %v", due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Stopped != "" || attempt.Attempt != 0 || attempt.RetryFrom == nil || len(reports.reported) != 0 {
		t.Fatalf("attempt = %+v, reports %d; want a wait that counts nothing and reports nothing", attempt, len(reports.reported))
	}
	if _, due, _ := pipeline.NextOutsideFile(ctx); due {
		t.Fatal("the record was due again before the wait had passed")
	}
	clock.advance(outsidefile.RetryAfter + time.Minute)
	down = false
	again, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || again.RunID() != landing.RunID() {
		t.Fatalf("NextOutsideFile() after the wait = %+v, %v, %v; want the same attempt due", again, due, err)
	}
}

// committedPinnedFixture is a repository whose main carries a pinned file
// already brought in, with its record, and a pipeline whose developer runs
// develop to change.
func committedPinnedFixture(t *testing.T, develop func(directory string) error) (*runstate.Store, Pipeline) {
	t.Helper()
	repository := pipelineRepository(t)
	fetched := outsidefile.Fetched{SHA256: outsidefile.Checksum([]byte(pinnedContent)), Size: int64(len(pinnedContent)), FinalURL: "https://cdn.example/npm/chart.js@4.4.1/dist/chart.umd.js", At: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	written, err := outsidefile.WithHarnessFields(pinnedRecord, []byte(pinnedRecordText("https://cdn.example")), fetched)
	if err != nil {
		t.Fatal(err)
	}
	writePinnedFixtureFile(t, repository, pinnedRecord, string(written))
	writePinnedFixtureFile(t, repository, pinnedFile, pinnedContent)
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "the chart library, brought in")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return develop(request.WorkingDirectory)
	}, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	return store, pipeline
}

// A pinned file changed by hand, and the harness's fields written by a change
// that is not the run that brings the file in, each fail that change's checks
// naming the file or the record.
func TestAPinnedFileChangedByHandAndHarnessFieldsWrittenByAnotherChangeFailTheChecks(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		develop func(string) error
		names   string
	}{
		"pinned file changed by hand": {
			develop: func(directory string) error {
				return os.WriteFile(filepath.Join(directory, pinnedFile), []byte(pinnedContent+"// edited\n"), 0o600)
			},
			names: pinnedFile + ": the pinned file's checksum",
		},
		"harness fields written": {
			develop: func(directory string) error {
				path := filepath.Join(directory, pinnedRecord)
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return os.WriteFile(path, []byte(strings.Replace(string(content), "fetched_at: \"2026-10-01T00:00:00Z\"", "fetched_at: \"2026-10-02T00:00:00Z\"", 1)), 0o600)
			},
			names: pinnedRecord + ": this change writes the harness's fields (fetched_at)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, pipeline := committedPinnedFixture(t, test.develop)
			outcome, _ := pipeline.Run(context.Background(), "yoyodyne-task")
			saved, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if len(outcome.Checks) != 1 || outcome.Checks[0].Command != PinnedFilesCheck || !strings.Contains(outcome.Checks[0].FailureOutput, test.names) || saved.Status != runstate.StatusFailed {
				t.Fatalf("run %s: %s; checks = %+v; want the pinned-file comparison failing and naming %q", saved.Status, saved.Failure, outcome.Checks, test.names)
			}
		})
	}
	// A change that leaves the pinned file and its record alone passes the
	// comparison.
	store, pipeline := committedPinnedFixture(t, func(directory string) error {
		return os.WriteFile(filepath.Join(directory, "feature.txt"), []byte("implemented\n"), 0o600)
	})
	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if saved, _ := store.Load(outcome.RunID); saved.Status != runstate.StatusSucceeded {
		t.Fatalf("an unrelated change ended %s: %s", saved.Status, saved.Failure)
	}
}

// queuedOutsideFile is outsideFileFixture with the merge queue switched on,
// and the driver that works the queue reading the run's bookkeeping item as
// the command line wires it.
func queuedOutsideFile(t *testing.T) (string, *orchestratortest.Tracker, *orchestratortest.Backend, Pipeline, *runstate.Store, *collectedReports, *runstate.MergeQueueStore, func() MergeQueueDriver) {
	t.Helper()
	repository, tracker, provider, pipeline, store, reports, _ := outsideFileFixture(t, nil)
	queue, err := runstate.NewMergeQueueStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.MergeQueue = queue
	pipeline.Config.Execution.MergeQueue = true
	driver := func() MergeQueueDriver {
		queued := pipeline
		items := OutsideFileItems{WorkTracker: pipeline.Tracker, Runs: store}
		queued.Tracker = items
		worktrees := queued.Worktrees.(*gitworktree.Manager)
		return MergeQueueDriver{
			Worker: MergeQueueWorker{Pipeline: &queued, Queue: queue, Candidates: worktrees, Events: queue.AppendEvent, Waiting: MergeQueueWaiting(queue)},
			Promoter: MergeQueuePromoter{
				Pipeline: &queued, Queue: queue, Lander: worktrees,
				Completion: MergeQueueRunCompletion{Store: store, Tracker: items},
				Repair:     MergeQueueRunHandback{Store: store, Tracker: items},
			},
			Queues: queue,
		}
	}
	return repository, tracker, provider, pipeline, store, reports, queue, driver
}

// With the merge queue on, the run that brings a file in is admitted to it like
// any approved change, and the queue lands it and tells the item the record is
// for.
func TestWithTheQueueOnARunThatBringsAFileInIsAdmittedAndLandedByTheQueue(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, store, _, _, driver := queuedOutsideFile(t)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %v, %v", due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	saved, err := store.Load(landing.RunID())
	if err != nil || !saved.AwaitingMergeQueue() || saved.HoldsDeveloperSlot() {
		t.Fatalf("run = %s %+v, %v; want it waiting in the merge queue and holding no slot", saved.Status, saved.MergeQueue, err)
	}
	if !strings.Contains(tracker.Notes, "admitted to the merge queue") {
		t.Fatalf("the item the record is for was told %q", tracker.Notes)
	}
	// Waiting in the queue, the record is not started again.
	if again, err := pipeline.BringInOutsideFile(ctx, landing); err != nil || again.RunID != landing.RunID() {
		t.Fatalf("BringInOutsideFile() while queued = %+v, %v", again, err)
	}
	pass := driver().Pass(ctx, saved.MergeQueue.Key())
	if pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the candidate landed", pass)
	}
	if strings.TrimSpace(publicationGit(t, repository, "show", "main:"+pinnedFile)) != strings.TrimSpace(pinnedContent) {
		t.Fatal("main does not carry the file the queue landed")
	}
	if !strings.Contains(tracker.Notes, "the file is brought in") {
		t.Fatalf("the item the record is for was not told the file landed: %q", tracker.Notes)
	}
}

// A combined change the queue refuses ends the run with its reason on the item
// the record is for, hands nothing to a developer, and a later pull starts a
// fresh run.
func TestAQueueRefusalEndsTheRunAndALaterPullStartsAFreshOne(t *testing.T) {
	t.Parallel()

	_, tracker, provider, pipeline, store, reports, _, driver := queuedOutsideFile(t)
	approve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := approve(request)
		if request.Role == domain.RoleReviewer && len(provider.RequestsForRole(domain.RoleReviewer)) > 1 {
			result.FinalText = repairVerdict
		}
		return result, err
	}
	ctx := context.Background()
	landing, _, _ := pipeline.NextOutsideFile(ctx)
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	saved, _ := store.Load(landing.RunID())
	pass := driver().Pass(ctx, saved.MergeQueue.Key())
	if pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the candidate refused", pass)
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("a developer was invoked %d times", got)
	}
	if handed, _ := store.Load(landing.RunID()); !queueRefused(handed) {
		t.Fatalf("handed back run = %s, queue %v, integration %v; want it ended by the queue", handed.Status, handed.MergeQueue != nil, handed.Integration != nil)
	}
	if !strings.Contains(tracker.Notes, "The merge queue handed this change back") {
		t.Fatalf("the item the record is for was not told why: %q", tracker.Notes)
	}
	// The next pull settles the refused run and counts it; the one after starts
	// a fresh run.
	next, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || next.RunID() != landing.RunID() {
		t.Fatalf("NextOutsideFile() = %+v, %v, %v", next, due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, next); err != nil {
		t.Fatalf("BringInOutsideFile() settling the refusal error = %v", err)
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Attempt != 1 || len(attempt.QueueFailures) != 1 || attempt.Stopped != "" || len(reports.reported) != 0 {
		t.Fatalf("attempt = %+v, reports %d; want one refusal counted and nothing stopped", attempt, len(reports.reported))
	}
	fresh, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || fresh.Attempt != 1 || fresh.RunID() == landing.RunID() {
		t.Fatalf("NextOutsideFile() after the refusal = %+v, %v, %v; want a fresh run", fresh, due, err)
	}
}

// Three refusals in a row for one record stop it and file one report naming
// the record and the three reasons.
func TestThreeQueueRefusalsInARowStopTheRecordAndReportOnce(t *testing.T) {
	t.Parallel()

	_, _, _, pipeline, store, reports, _, _ := queuedOutsideFile(t)
	landing, _, _ := pipeline.NextOutsideFile(context.Background())
	for attempt, why := range []string{"the first reason", "the second reason", "the third reason"} {
		landing.Attempt = attempt
		if err := pipeline.queueRefusedOutsideFile(store, landing, why); err != nil {
			t.Fatal(err)
		}
		// The same run's refusal read twice is counted once.
		if err := pipeline.queueRefusedOutsideFile(store, landing, why); err != nil {
			t.Fatal(err)
		}
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Stopped == "" || !attempt.Reported || len(reports.reported) != 1 {
		t.Fatalf("attempt = %+v, reports %d; want the record stopped and one report", attempt, len(reports.reported))
	}
	message := reports.reported[0].Message
	for _, want := range []string{pinnedRecord, "the first reason", "the second reason", "the third reason"} {
		if !strings.Contains(message, want) {
			t.Errorf("the report does not name %q: %s", want, message)
		}
	}
	if reports.reported[0].Addressee() != domain.RoleDevelopmentManager {
		t.Fatalf("the report is for %s", reports.reported[0].Addressee())
	}
	if _, due, _ := pipeline.NextOutsideFile(context.Background()); due {
		t.Fatal("a stopped record was due again")
	}
}

// asGoModule makes the fixture's repository a Go module whose assets directory
// is a package, and commits it, so a change to the pinned file narrows the
// checks to that package where anything lets it.
func asGoModule(t *testing.T, repository string) {
	t.Helper()
	writePinnedFixtureFile(t, repository, "go.mod", "module example.invalid/charts\n\ngo 1.22\n")
	writePinnedFixtureFile(t, repository, "assets/assets.go", "package assets\n")
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "make the repository a Go module")
}

// A run that brings a file in, in a Go module where its change touches one
// package, runs its checks told the whole module, so its checks and review
// passing is what lets it through the merge step rather than a refusal for
// checks that were narrowed.
func TestARunThatBringsAFileInIsCheckedWholeAndGetsThroughTheMergeStep(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, store, reports, _ := outsideFileFixture(t, nil)
	asGoModule(t, repository)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %+v, %v, %v; want the record due", landing, due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	saved, err := store.Load(landing.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != runstate.StatusSucceeded || saved.Integration == nil {
		t.Fatalf("run = %s: %s; want it through the merge step", saved.Status, saved.Failure)
	}
	if saved.ChecksPassed == nil || saved.ChecksPassed.Narrowed != "" {
		t.Fatalf("checks passed = %+v; want them told the whole module", saved.ChecksPassed)
	}
	if strings.TrimSpace(publicationGit(t, repository, "show", "main:"+pinnedFile)) != strings.TrimSpace(pinnedContent) {
		t.Fatal("main does not carry the file")
	}
	if len(reports.reported) != 0 {
		t.Fatalf("reports = %+v; want none", reports.reported)
	}
}

// pinnedLicenseFixture is outsideFileFixture with the license the record names
// pinned too rather than committed: its record is on main, its file is not,
// and the test server serves it at licensePath. The pipeline's check refuses
// the library in the tree without its license beside it, as the dashboard's own
// test does.
func pinnedLicenseFixture(t *testing.T, licenseName, licensePath string) (string, *orchestratortest.Tracker, *orchestratortest.Backend, Pipeline, *runstate.Store, *collectedReports, *movableClock) {
	t.Helper()
	repository, tracker, provider, pipeline, store, reports, clock := outsideFileFixture(t, nil)
	license := "assets/" + licenseName
	// The test server's address, as the fixture's record gives it.
	declared, err := outsidefile.ParseRecord(pinnedRecord, []byte(publicationGit(t, repository, "show", "main:"+pinnedRecord)))
	if err != nil {
		t.Fatal(err)
	}
	address, _, _ := strings.Cut(declared.URL, "/npm/")
	runPipelineGit(t, repository, "rm", "-q", "assets/chart.js-LICENSE.md")
	writePinnedFixtureFile(t, repository, pinnedRecord, strings.Replace(pinnedRecordText(address), "license_file: chart.js-LICENSE.md", "license_file: "+licenseName, 1))
	writePinnedFixtureFile(t, repository, license+outsidefile.RecordSuffix, fmt.Sprintf("file: %s\nurl: %s%s\nversion: 4.4.1\nlicense: MIT\nlicense_file: %s\nfor: %s\n", licenseName, address, licensePath, licenseName, pinnedFor))
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "the license is pinned too")
	pipeline.Config.Checks = []string{"test ! -e " + pinnedFile + " || test -e " + license}
	return repository, tracker, provider, pipeline, store, reports, clock
}

// A file whose record names a license file that is itself pinned and not yet
// on main is brought in by one run together with that license, so the file's
// checks, which refuse the file without its license beside it, see both. The
// license's record starts no run of its own meanwhile, whichever of the two
// records comes first: the real Chart.js license's record sorts before the
// library's.
func TestAFileIsBroughtInTogetherWithTheLicenseItsRecordNames(t *testing.T) {
	t.Parallel()

	for name, licenseName := range map[string]string{
		"the license's record sorts first, as Chart.js's does": "chart.js-LICENSE.md",
		"the file's record sorts first":                        "vendored-LICENSE.md",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository, tracker, provider, pipeline, store, _, _ := pinnedLicenseFixture(t, licenseName, "/npm/chart.js@4.4.1/LICENSE.md")
			license := "assets/" + licenseName
			licenseRecord := license + outsidefile.RecordSuffix
			ctx := context.Background()
			landing, due, err := pipeline.NextOutsideFile(ctx)
			if err != nil || !due {
				t.Fatalf("NextOutsideFile() = %+v, %v, %v; want the record due", landing, due, err)
			}
			if landing.Record != pinnedRecord || landing.License == nil || landing.License.Record != licenseRecord {
				t.Fatalf("NextOutsideFile() = %+v; want %s brought in with the license %s", landing, pinnedRecord, licenseRecord)
			}
			if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
				t.Fatalf("BringInOutsideFile() error = %v", err)
			}
			saved, err := store.Load(landing.RunID())
			if err != nil {
				t.Fatal(err)
			}
			if saved.Status != runstate.StatusSucceeded || saved.Integration == nil {
				t.Fatalf("run = %s: %s; want both files landed", saved.Status, saved.Failure)
			}
			if err := saved.Validate(); err != nil {
				t.Fatalf("the run's record is not valid: %v", err)
			}
			if got := len(provider.RequestsForRole(domain.RoleReviewer)); got != 1 {
				t.Fatalf("the reviewer was invoked %d times, want once", got)
			}
			review := provider.RequestsForRole(domain.RoleReviewer)[0].Prompt
			for _, want := range []string{"brings in the license file " + license, "SHA-256: " + outsidefile.Checksum([]byte(licenseContent)), licenseName + ", which is in the repository"} {
				if !strings.Contains(review, want) {
					t.Errorf("the reviewer was not shown %q", want)
				}
			}
			if strings.TrimSpace(publicationGit(t, repository, "show", "main:"+license)) != strings.TrimSpace(licenseContent) {
				t.Fatal("main does not carry the license")
			}
			for path, content := range map[string]string{pinnedRecord: pinnedContent, licenseRecord: licenseContent} {
				record, err := outsidefile.ParseRecord(path, []byte(publicationGit(t, repository, "show", "main:"+path)))
				if err != nil {
					t.Fatal(err)
				}
				if record.SHA256 != outsidefile.Checksum([]byte(content)) {
					t.Fatalf("main's record %s = %+v; want the harness's fields written", path, record)
				}
			}
			changed := strings.Fields(publicationGit(t, repository, "diff", "--name-only", "main~1", "main"))
			if len(changed) != 4 {
				t.Fatalf("the landing changed %v; want the file, the license, and their records", changed)
			}
			if !strings.Contains(tracker.Notes, "with its license file "+license) {
				t.Fatalf("the item the record is for was told %q", tracker.Notes)
			}
			if _, due, err := pipeline.NextOutsideFile(ctx); err != nil || due {
				t.Fatalf("NextOutsideFile() after landing = %v, %v; want nothing due", due, err)
			}
		})
	}
}

// Where the license a run brings in with its file cannot be fetched for a
// reason about its record, neither file is brought in, both records are
// stopped, and one report names both.
func TestALicenseThatCannotBeFetchedStopsBothRecordsAndBringsInNeither(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, store, reports, clock := pinnedLicenseFixture(t, "chart.js-LICENSE.md", "/npm/chart.js@4.4.1/NO-LICENSE.md")
	licenseRecord := "assets/chart.js-LICENSE.md" + outsidefile.RecordSuffix
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || landing.License == nil {
		t.Fatalf("NextOutsideFile() = %+v, %v, %v; want the file due with its license", landing, due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatal("a run was reviewed although its license could not be fetched")
	}
	for _, path := range []string{pinnedFile, "assets/chart.js-LICENSE.md"} {
		if _, err := os.Stat(filepath.Join(repository, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s was written: %v", path, err)
		}
	}
	for _, record := range []string{pinnedRecord, licenseRecord} {
		attempt, found, err := store.OutsideFileAttempt(record)
		if err != nil || !found || attempt.Stopped == "" || !attempt.Reported {
			t.Fatalf("%s = %+v, %v, %v; want it stopped", record, attempt, found, err)
		}
	}
	if len(reports.reported) != 1 || !strings.Contains(reports.reported[0].Message, licenseRecord) {
		t.Fatalf("reports = %+v; want one naming the license's record too", reports.reported)
	}
	clock.advance(outsidefile.RetryAfter + time.Hour)
	if _, due, err := pipeline.NextOutsideFile(ctx); err != nil || due {
		t.Fatalf("NextOutsideFile() = %v, %v; want neither record due", due, err)
	}
}

// movedUnderReview makes every review of a run that brings a file in move the
// pinned file in that run's checkout, so what is about to be promoted is not
// what the checks passed over, and the merge step refuses it.
func movedUnderReview(provider *orchestratortest.Backend, store *runstate.Store) {
	approve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			inFlight, err := store.Incomplete()
			if err != nil {
				return backend.RunResult{}, err
			}
			for _, state := range inFlight {
				if state.OutsideFile != nil && state.WorktreePath != "" {
					if err := os.WriteFile(filepath.Join(state.WorktreePath, pinnedFile), []byte(pinnedContent+"// moved\n"), 0o600); err != nil {
						return backend.RunResult{}, err
					}
				}
			}
		}
		return approve(request)
	}
}

// A run the merge step refuses for a cause another run would meet again — here
// checks that ran over only part of the module — is not picked up again while
// the record is unchanged: the record is stopped and one report says which
// check the merge step found missing.
func TestARunRefusedAtTheMergeStepForALastingCauseIsNotPickedUpAgainUnchanged(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, store, reports, clock := outsideFileFixture(t, nil)
	pipeline.Build = strings.Repeat("a", 40)
	movedUnderReview(provider, store)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %v, %v", due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	// The run ended at the merge step; the cause it is settled on is the one
	// narrowed checks give, read from the promotion's own gate.
	narrowed := (&activeRun{state: runstate.State{
		ReviewDecision: runstate.ReviewApprove,
		ChecksPassed:   &runstate.ChecksPassed{Content: "content", Narrowed: "./internal/dashboard"},
	}}).integrationEarned(ctx)
	// The way the run's ending carries it back: classified at the promotion and
	// joined with whatever ending the run also failed to record.
	ended := errors.Join(stoppedBy(runstate.StopIntegration, narrowed), errors.New("record failed run outcome"))
	if !refusedForGood(ended) {
		t.Fatalf("narrowed checks = %v; want a lasting refusal", ended)
	}
	if _, err := pipeline.settleOutsideFile(ctx, store, landing.RunID(), outsideFileRecorder{}, ended); err != nil {
		t.Fatalf("settleOutsideFile() error = %v", err)
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Stopped == "" || !attempt.Reported || len(reports.reported) != 1 {
		t.Fatalf("attempt = %+v, reports %d; want the record stopped and one report", attempt, len(reports.reported))
	}
	if message := reports.reported[0].Message; !strings.Contains(message, "merge step refused it because the checks passed only over ./internal/dashboard, and the full checks were never run") {
		t.Fatalf("the report does not say which check was missing: %s", message)
	}
	clock.advance(outsidefile.RetryAfter + time.Hour)
	if _, due, err := pipeline.NextOutsideFile(ctx); err != nil || due {
		t.Fatalf("NextOutsideFile() after the refusal = %v, %v; want the unchanged record not picked up again", due, err)
	}
	if got := len(provider.RequestsForRole(domain.RoleReviewer)); got != 1 {
		t.Fatalf("the reviewer was invoked %d times, want once", got)
	}
	if message := reports.reported[0].Message; strings.Contains(message, "has to be corrected") || !strings.Contains(message, "build of the harness other than "+strings.Repeat("a", 40)) {
		t.Fatalf("the report asks for the record to be edited, or does not say a new build starts it again: %s", message)
	}
	// A new build is running: the record starts again, unedited, as a fresh run.
	pipeline.Build = strings.Repeat("b", 40)
	again, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || again.Attempt != landing.Attempt+1 || again.RunID() == landing.RunID() {
		t.Fatalf("NextOutsideFile() on a new build = %+v, %v, %v; want a fresh run of the unchanged record", again, due, err)
	}
}

// A run the merge step refuses for a cause that passes — the change moved
// under its evidence, or could not be named — is tried again after the wait
// rather than stopping the record, and nothing is reported.
func TestARunRefusedAtTheMergeStepForAPassingCauseIsTriedAgainAfterTheWait(t *testing.T) {
	t.Parallel()

	_, _, provider, pipeline, store, reports, clock := outsideFileFixture(t, nil)
	movedUnderReview(provider, store)
	ctx := context.Background()
	landing, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due {
		t.Fatalf("NextOutsideFile() = %v, %v", due, err)
	}
	if _, err := pipeline.BringInOutsideFile(ctx, landing); err != nil {
		t.Fatalf("BringInOutsideFile() error = %v", err)
	}
	saved, err := store.Load(landing.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status == runstate.StatusSucceeded || !strings.HasPrefix(saved.Failure, ErrIntegrationUnearned.Error()) {
		t.Fatalf("run = %s: %s; want it refused at the merge step", saved.Status, saved.Failure)
	}
	attempt, _, _ := store.OutsideFileAttempt(pinnedRecord)
	if attempt.Stopped != "" || attempt.RetryFrom == nil || attempt.Attempt != 1 || len(reports.reported) != 0 {
		t.Fatalf("attempt = %+v, reports %d; want a wait, the next attempt, and no report", attempt, len(reports.reported))
	}
	// A change that could not be named is the same kind of refusal.
	unnamed := fmt.Errorf("%w: the change about to be promoted could not be named: %v", ErrIntegrationUnearned, errors.New("git timed out"))
	if refusedForGood(unnamed) {
		t.Fatal("a change that could not be named was read as a lasting refusal")
	}
	if _, due, _ := pipeline.NextOutsideFile(ctx); due {
		t.Fatal("the record was due again before the wait had passed")
	}
	clock.advance(outsidefile.RetryAfter + time.Minute)
	again, due, err := pipeline.NextOutsideFile(ctx)
	if err != nil || !due || again.Attempt != 1 {
		t.Fatalf("NextOutsideFile() after the wait = %+v, %v, %v; want the next attempt due", again, due, err)
	}
}
