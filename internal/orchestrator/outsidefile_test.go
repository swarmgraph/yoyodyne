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
		if r.URL.Path != "/npm/chart.js@4.4.1/dist/chart.umd.js" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(pinnedContent))
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
