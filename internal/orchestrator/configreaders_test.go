package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A landing that leaves the configuration carrying a key a running part's
// build cannot read names that part — its build, its process, and the key — on
// the item and in the outcome, the way the effort lines should have been named
// against the dashboard on 2026-09-28. A part that reads the key, and a part
// whose process has gone, are not named.
func TestALandingNamesTheRunningPartsThatCannotReadTheConfiguration(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})

	stateRoot := t.TempDir()
	configPath := filepath.Join(stateRoot, "config.yaml")
	if err := os.WriteFile(configPath, []byte("agents:\n  developer:\n    role: developer\n    effort: medium\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := runstate.NewConfigReaderStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	alive := map[int]bool{4242: true, 4343: true, 4444: false, 4243: true}
	store = store.WithProcessCheck(func(pid int) (bool, error) { return alive[pid], nil })
	older := slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
	started := time.Date(2026, 9, 26, 23, 36, 0, 0, time.UTC)
	for _, reader := range []runstate.ConfigReader{
		{Service: "dashboard", PID: 4242, Build: "0364141b2c3d4e5f", ConfigPath: configPath, StartedAt: started, Keys: older},
		{Service: "dashboard", PID: 4243, Build: "9870df6a1b2c3d4e", ConfigPath: configPath, StartedAt: started, Keys: config.SchemaKeys()},
		{Service: "scheduler", PID: 4343, Build: "9870df6a1b2c3d4e", ConfigPath: configPath, StartedAt: started, Keys: config.SchemaKeys()},
		{Service: "slack", PID: 4444, Build: "0364141b2c3d4e5f", ConfigPath: configPath, StartedAt: started, Keys: older},
	} {
		if err := store.Record(reader); err != nil {
			t.Fatalf("Record(%s) error = %v", reader.Service, err)
		}
	}
	pipeline.ConfigReaders = store

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the run landed", outcome)
	}
	if len(outcome.ConfigMismatches) != 1 || outcome.ConfigMismatches[0].Service != "dashboard" {
		t.Fatalf("ConfigMismatches = %+v, want the dashboard alone", outcome.ConfigMismatches)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	for _, want := range []string{"Running parts that cannot read the configuration this landing left", "the dashboard service", "build 0364141b2c3d", "pid 4242", "agents.developer.effort", "`yoyo dashboard`"} {
		if !strings.Contains(notes, want) {
			t.Errorf("item notes do not name %q:\n%s", want, notes)
		}
	}
	for _, unwanted := range []string{"the scheduler service", "the slack service"} {
		if strings.Contains(notes, unwanted) {
			t.Errorf("item notes name %q, which reads the key or is not running:\n%s", unwanted, notes)
		}
	}
}

// A landing where every running part reads the configuration says nothing.
func TestALandingEveryRunningPartReadsSaysNothingOfThem(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	store, err := runstate.NewConfigReaderStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.ConfigReaders = store

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(outcome.ConfigMismatches) != 0 || strings.Contains(strings.Join(tracker.NoteRecords, "\n"), "cannot read the configuration") {
		t.Fatalf("outcome names %+v and notes %v, want nothing said", outcome.ConfigMismatches, tracker.NoteRecords)
	}
}

// unmovedCheckout stands in for a primary checkout the landing has not moved —
// a target the forge protects, where nothing is moved locally until the forge
// merges: when the landing asks, the working file does not carry what landed.
type unmovedCheckout struct {
	store   *runstate.ConfigReaderStore
	path    string
	content []byte
}

func (u unmovedCheckout) MismatchesIn(read func(string) ([]byte, error)) ([]runstate.ConfigMismatch, error) {
	if err := os.WriteFile(u.path, u.content, 0o600); err != nil {
		return nil, err
	}
	return u.store.MismatchesIn(read)
}

func (u unmovedCheckout) TemplateMismatches(path string, added []string) ([]runstate.ConfigTemplateMismatch, error) {
	return u.store.TemplateMismatches(path, added)
}

// The landed change is what adds the key, and the primary checkout's file does
// not carry it when the landing asks. The landing reads the file as the
// integrated commit holds it, so the dashboard on a build from before the key
// is still named.
func TestALandingThatAddsAKeyNamesThePartsFromTheLandedCommit(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		directory := filepath.Join(request.WorkingDirectory, "deploy")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(directory, "config.yaml"), []byte("agents:\n  developer:\n    role: developer\n    effort: medium\n"), 0o644)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})

	configPath := filepath.Join(repository, "deploy", "config.yaml")
	store, err := runstate.NewConfigReaderStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithProcessCheck(func(pid int) (bool, error) { return pid == 4242, nil })
	older := slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
	if err := store.Record(runstate.ConfigReader{
		Service: "dashboard", PID: 4242, Build: "0364141b2c3d4e5f", ConfigPath: configPath,
		StartedAt: time.Date(2026, 9, 26, 23, 36, 0, 0, time.UTC), Keys: older,
	}); err != nil {
		t.Fatal(err)
	}
	pipeline.ConfigReaders = unmovedCheckout{store: store, path: configPath, content: []byte("agents:\n  developer:\n    role: developer\n")}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the run landed", outcome)
	}
	if len(outcome.ConfigMismatches) != 1 || outcome.ConfigMismatches[0].Service != "dashboard" ||
		!slices.Equal(outcome.ConfigMismatches[0].Keys, []string{"agents.developer.effort"}) {
		t.Fatalf("ConfigMismatches = %+v, want the dashboard named for the landed key", outcome.ConfigMismatches)
	}
	if notes := strings.Join(tracker.NoteRecords, "\n"); !strings.Contains(notes, "the dashboard service") || !strings.Contains(notes, "agents.developer.effort") {
		t.Fatalf("item notes do not name the dashboard and the key:\n%s", notes)
	}
}

func TestALandingAddingOnlyTemplateKeysNamesIncompatibleRunningBuilds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, before, after string
		wantMismatch        bool
	}{
		{
			name:         "new merged template key",
			before:       "agents: {developer: {role: developer, model: opus}}\n",
			after:        "agents: {developer: {<<: {role: developer, model: opus, effort: medium}}}\n",
			wantMismatch: true,
		},
		{
			name:   "existing key with a new value",
			before: "agents: {developer: {role: developer, effort: medium}}\n",
			after:  "agents: {developer: {role: developer, effort: high}}\n",
		},
		{
			name:         "new template",
			after:        "agents: {developer: {role: developer, effort: medium}}\n",
			wantMismatch: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := pipelineRepository(t)
			template := "internal/config/builtin/v1/bundle.yaml"
			templatePath := filepath.Join(repository, filepath.FromSlash(template))
			if test.before != "" {
				if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(templatePath, []byte(test.before), 0o644); err != nil {
					t.Fatal(err)
				}
				runPipelineGit(t, repository, "add", template)
				runPipelineGit(t, repository, "commit", "-m", "initial shipped template")
			}
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				path := filepath.Join(request.WorkingDirectory, filepath.FromSlash(template))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				return os.WriteFile(path, []byte(test.after), 0o644)
			}, approveVerdict)
			pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
			stateRoot := t.TempDir()
			configPath := filepath.Join(stateRoot, "config.yaml")
			if err := os.WriteFile(configPath, []byte("agents: {developer: {role: developer}}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := runstate.NewConfigReaderStore(stateRoot, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
			older := slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
			services := []string{"dashboard", "scheduler", "slack", runstate.ConfigReaderSupervisor}
			for index, service := range services {
				if err := store.Record(runstate.ConfigReader{
					Service: service, PID: 4200 + index, Build: "0364141b2c3d4e5f",
					ConfigPath: configPath, StartedAt: time.Now(), Keys: older,
				}); err != nil {
					t.Fatal(err)
				}
			}
			// Move the checkout's template back before the landing comparison. Both
			// the introduced keys and their previous version must come from Git.
			pipeline.ConfigReaders = unmovedCheckout{store: store, path: templatePath, content: []byte(test.before)}
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil || outcome.Status != runstate.StatusSucceeded {
				t.Fatalf("Run() = %s, %v", outcome.Status, err)
			}
			if len(outcome.ConfigMismatches) != 0 {
				t.Fatalf("healthy active files named as broken: %+v", outcome.ConfigMismatches)
			}
			notes := strings.Join(tracker.NoteRecords, "\n")
			if !test.wantMismatch {
				if len(outcome.TemplateConfigMismatches) != 0 || strings.Contains(notes, "new keys in shipped templates") {
					t.Fatalf("an existing key was reported newly introduced: %+v; %s", outcome.TemplateConfigMismatches, notes)
				}
				return
			}
			if len(outcome.TemplateConfigMismatches) != len(services) {
				t.Fatalf("template mismatches = %+v, want all four running services", outcome.TemplateConfigMismatches)
			}
			for _, service := range services {
				if !strings.Contains(notes, "the "+service+" service") {
					t.Errorf("template-only landing does not name %s: %s", service, notes)
				}
			}
			for _, want := range []string{"new keys in shipped templates", "build 0364141b2c3d", "agents.*.effort", template, "adopting those keys", "would make"} {
				if !strings.Contains(notes, want) {
					t.Errorf("prospective landing note lacks %q: %s", want, notes)
				}
			}
			if strings.Contains(notes, "Running parts that cannot read the configuration this landing left") {
				t.Fatalf("a prospective warning claims the active file is unreadable: %s", notes)
			}
		})
	}
}

func TestAQueuedLandingComparesRunningBuildsWhenItsMergeIsConfirmed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		activeKey  bool
		refuseNote bool
	}{
		{name: "template only"},
		{name: "active file and template", activeKey: true},
		{name: "a refused finding is delivered on the next sweep", refuseNote: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newQueuedFixture(t)
			template := "internal/config/builtin/v1/bundle.yaml"
			activePath := "deploy/config.yaml"
			before := []byte("agents: {developer: {role: developer}}\n")
			after := []byte("agents: {developer: {role: developer, effort: medium}}\n")
			for _, relative := range []string{template, activePath} {
				path := filepath.Join(fixture.repository, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, before, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			runPipelineGit(t, fixture.repository, "add", template, activePath)
			runPipelineGit(t, fixture.repository, "commit", "-m", "configuration before the new key")
			runPipelineGit(t, fixture.repository, "push", "origin", "main")
			previous := publishedCommit(t, fixture.repository, "main")
			fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				if err := os.WriteFile(filepath.Join(request.WorkingDirectory, filepath.FromSlash(template)), after, 0o644); err != nil {
					return err
				}
				if test.activeKey {
					return os.WriteFile(filepath.Join(request.WorkingDirectory, filepath.FromSlash(activePath)), after, 0o644)
				}
				return nil
			}, approveVerdict)
			pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"true"}), provider), fixture.forge)
			store, err := runstate.NewConfigReaderStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
			older := slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
			if err := store.Record(runstate.ConfigReader{
				Service: "dashboard", PID: 4242, Build: "0364141b2c3d4e5f",
				ConfigPath: filepath.Join(fixture.repository, filepath.FromSlash(activePath)),
				StartedAt:  time.Now(), Keys: older,
			}); err != nil {
				t.Fatal(err)
			}
			pipeline.ConfigReaders = store
			outcome, err := pipeline.Run(context.Background(), fixture.tracker.Record().Item.ID)
			if err != nil || outcome.PullRequest == nil || !outcome.PullRequest.MergeQueued {
				t.Fatalf("queued run = %+v, %v", outcome, err)
			}
			if len(outcome.TemplateConfigMismatches) != 0 || strings.Contains(fixture.tracker.Record().Notes, "new keys in shipped templates") {
				t.Fatal("a queued change was compared before its merge landed")
			}
			fixture.forge.PerformQueuedMerge(t)
			merge := publishedCommit(t, fixture.remote, "main")
			// Another landing advances the tip before the sweep. The comparison
			// must use this request's confirmed merge, not that later tip.
			landAnotherChange(t, fixture.remote)
			files := &configComparisonReadLog{ConfigComparisonFiles: pipeline.Worktrees}
			reconciler := fixture.reconciler(t)
			reconciler.Repository = fixture.repository
			reconciler.ConfigFiles = files
			reconciler.ConfigReaders = store
			readsBeforeRetry := 0
			if test.refuseNote {
				reconciler.Tracker = &refuseConfigFindingOnce{WorkTracker: fixture.tracker}
				first, err := reconciler.Reconcile(context.Background())
				if err != nil || len(first) != 1 || !strings.Contains(first[0].Failure, "configuration finding refused") {
					t.Fatalf("refused finding = %+v, %v", first, err)
				}
				state := loadRun(t, fixture.store, outcome.RunID)
				if !state.PullRequest.MergeQueued || !state.PullRequest.Merged || state.PullRequest.MergeCommit != merge || fixture.tracker.Record().Closed {
					t.Fatalf("settlement forgot the undelivered finding: pull request %+v, closed %t", state.PullRequest, fixture.tracker.Record().Closed)
				}
				if !state.Outstanding() || state.ConfigComparison == nil || !state.ConfigComparison.Pending || state.ConfigComparison.DeliveryFailure != "configuration finding refused" {
					t.Fatalf("pending comparison was lost: %+v", state.ConfigComparison)
				}
				if state.ConfigComparison.TargetCommit != merge || state.ConfigComparison.PreviousTargetCommit != previous {
					t.Fatalf("saved comparison names the wrong revisions: %+v", state.ConfigComparison)
				}
				if _, err := os.Stat(outcome.WorktreePath); err != nil {
					t.Fatalf("artifacts were removed before finding delivery: %v", err)
				}
				// Delivery retries the saved comparison without reading newer files
				// or requiring the older service still to be running.
				reconciler.ConfigReaders = nil
				reconciler.ConfigFiles = nil
				readsBeforeRetry = len(files.reads)
			}
			results, err := reconciler.Reconcile(context.Background())
			if err != nil || len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
				t.Fatalf("confirmed landing = %+v, %v", results, err)
			}
			result := results[0]
			if len(result.TemplateConfigMismatches) != 1 || result.TemplateConfigMismatches[0].Service != "dashboard" {
				t.Fatalf("template findings = %+v, want the older dashboard", result.TemplateConfigMismatches)
			}
			wantActive := 0
			if test.activeKey {
				wantActive = 1
			}
			if len(result.ConfigMismatches) != wantActive {
				t.Fatalf("active findings = %+v, want %d", result.ConfigMismatches, wantActive)
			}
			notes := fixture.tracker.Record().Notes
			for _, want := range []string{"the dashboard service", "build 0364141b2c3d", "agents.*.effort", "new keys in shipped templates"} {
				if !strings.Contains(notes, want) {
					t.Errorf("queued landing note lacks %q: %s", want, notes)
				}
			}
			if !slices.Contains(files.reads, configComparisonRead{commit: merge, path: template}) ||
				!slices.Contains(files.reads, configComparisonRead{commit: previous, path: template}) ||
				!slices.Contains(files.reads, configComparisonRead{commit: merge, path: activePath}) {
				t.Fatalf("comparison reads = %+v, want previous %s and confirmed merge %s", files.reads, previous, merge)
			}
			settled := loadRun(t, fixture.store, outcome.RunID)
			if settled.Outstanding() || settled.PullRequest.MergeQueued || settled.ConfigComparison.Pending || !fixture.tracker.Record().Closed {
				t.Fatalf("delivered comparison did not finish settlement: %+v", settled)
			}
			if test.refuseNote && len(files.reads) != readsBeforeRetry {
				t.Fatal("retry replaced the saved comparison by rereading the configuration")
			}
			if branch := publishedCommit(t, fixture.remote, outcome.Branch); branch != "" {
				t.Fatalf("finding delivery skipped removal of the merged remote branch: %s", branch)
			}
			if settled.Integration.PreviousTargetCommit != previous || settled.Integration.TargetCommit != outcome.Integration.TargetCommit {
				t.Fatal("comparison changed the promotion's recorded revisions")
			}
			noteCount := len(fixture.tracker.Record().NoteRecords)
			if again, err := reconciler.Reconcile(context.Background()); err != nil || len(again) != 0 || len(fixture.tracker.Record().NoteRecords) != noteCount {
				t.Fatalf("settled comparison repeated: %+v, %v", again, err)
			}
		})
	}
}

type configComparisonRead struct{ commit, path string }

type configComparisonReadLog struct {
	ConfigComparisonFiles
	reads []configComparisonRead
}

func (r *configComparisonReadLog) FileAtCommit(ctx context.Context, commit, path string, bound int64) (gitworktree.FileAt, error) {
	r.reads = append(r.reads, configComparisonRead{commit: commit, path: path})
	return r.ConfigComparisonFiles.FileAtCommit(ctx, commit, path, bound)
}

type refuseConfigFindingOnce struct {
	WorkTracker
	refused bool
}

func (r *refuseConfigFindingOnce) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	if !r.refused && strings.HasPrefix(note, "Running parts that cannot read") {
		r.refused = true
		return beads.WorkItem{}, errors.New("configuration finding refused")
	}
	return r.WorkTracker.RecordOutcome(ctx, id, note)
}

// An immediate landing can finish while its finding write is refused. Its
// account must survive in the run and be delivered after cleanup, without
// inspecting today's services or configuration in place of what it found.
func TestAnImmediateLandingRetriesItsSavedConfigurationFinding(t *testing.T) {
	t.Parallel()
	for _, unreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "mismatch", true: "comparison error"}[unreadable], func(t *testing.T) {
			t.Parallel()
			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			refusal := &refuseConfigFindingOnce{WorkTracker: tracker}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			pipeline, runs := newAutomaticPipeline(t, repository, refusal, provider, []string{"true"})
			stateRoot := t.TempDir()
			configPath := filepath.Join(stateRoot, "config.yaml")
			if !unreadable {
				if err := os.WriteFile(configPath, []byte("agents: {developer: {role: developer, effort: medium}}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			readers, err := runstate.NewConfigReaderStore(stateRoot, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			readers = readers.WithProcessCheck(func(int) (bool, error) { return true, nil })
			if err := readers.Record(runstate.ConfigReader{
				Service: "dashboard", PID: 4242, Build: "0364141b2c3d4e5f", ConfigPath: configPath, StartedAt: time.Now(),
				Keys: slices.DeleteFunc(config.SchemaKeys(), func(key string) bool { return key == "agents.*.effort" }),
			}); err != nil {
				t.Fatal(err)
			}
			pipeline.ConfigReaders = readers
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil || outcome.Status != runstate.StatusSucceeded || !outcome.WorkItemClosed || !outcome.WorktreeRemoved || !outcome.BranchRemoved {
				t.Fatalf("immediate landing = %+v, %v", outcome, err)
			}
			state := loadRun(t, runs, outcome.RunID)
			if state.Phase != runstate.PhaseComplete || !state.Outstanding() || state.ConfigComparison == nil || !state.ConfigComparison.Pending || state.ConfigComparison.DeliveryFailure != "configuration finding refused" {
				t.Fatalf("completed run lost its delivery obligation: %+v", state.ConfigComparison)
			}
			if outcome.ConfigComparison == nil || !outcome.ConfigComparison.Pending {
				t.Fatal("the outcome hides the outstanding delivery")
			}
			if state.ConfigComparison.TargetCommit != outcome.Integration.TargetCommit || state.ConfigComparison.PreviousTargetCommit != outcome.Integration.PreviousTargetCommit {
				t.Fatal("the saved comparison is not bound to the landed revisions")
			}
			if unreadable && !strings.Contains(state.ConfigComparison.ActiveProblem, "stale configuration reader record") {
				t.Fatalf("the comparison's read failure was lost: %+v", state.ConfigComparison)
			}
			if !unreadable && (len(state.ConfigComparison.Mismatches) != 1 || state.ConfigComparison.Mismatches[0].PID != 4242) {
				t.Fatalf("the older dashboard was lost: %+v", state.ConfigComparison)
			}
			// The later process has no service records or configuration reader.
			// Only the durable account can supply the comparison it must deliver.
			reconciler := Reconciler{Tracker: refusal, Store: runs, Worktrees: newObserver(t, repository, filepath.Dir(outcome.WorktreePath))}
			results, err := reconciler.Reconcile(context.Background())
			if err != nil || len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
				t.Fatalf("retry = %+v, %v", results, err)
			}
			notes := strings.Join(tracker.NoteRecords, "\n")
			want := "agents.developer.effort"
			if unreadable {
				want = "Configuration reader records and comparison problems"
			}
			if !strings.Contains(notes, want) || !strings.Contains(notes, configPath) {
				t.Fatalf("saved finding was not delivered: %s", notes)
			}
			settled := loadRun(t, runs, outcome.RunID)
			if settled.Outstanding() || settled.ConfigComparison.Pending || settled.ConfigComparison.DeliveryFailure != "" {
				t.Fatalf("successful delivery remained outstanding: %+v", settled.ConfigComparison)
			}
			noteCount := len(tracker.NoteRecords)
			if again, err := reconciler.Reconcile(context.Background()); err != nil || len(again) != 0 || len(tracker.NoteRecords) != noteCount {
				t.Fatalf("delivered comparison repeated: %+v, %v", again, err)
			}
		})
	}
}

func TestALandingSeparatesAStaleLegacySupervisorFromRunningParts(t *testing.T) {
	root := t.TempDir()
	store, err := runstate.NewConfigReaderStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	// The leaked test PID has been reused. Starting the installed supervisor
	// with a different PID does not supersede that legacy record.
	store = store.WithProcessCheck(func(int) (bool, error) { return true, nil })
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("agents: {developer: {effort: medium}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := runstate.ConfigReader{Service: runstate.ConfigReaderSupervisor, PID: 101, ConfigPath: filepath.Join(root, "gone.yaml"), StartedAt: time.Now(), Keys: []string{"version"}}
	if err := store.Record(stale); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "products", "yoyodyne", "config-readers")
	legacy := filepath.Join(directory, "supervisor.json")
	if err := os.Rename(filepath.Join(directory, stale.InstanceID()+".json"), legacy); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []runstate.ConfigReader{
		{Service: runstate.ConfigReaderSupervisor, PID: 103, ConfigPath: path, StartedAt: time.Now(), Keys: config.SchemaKeys()},
		{Service: "dashboard", PID: 102, ConfigPath: path, StartedAt: time.Now(), Keys: []string{"version"}},
	} {
		if err := store.Record(reader); err != nil {
			t.Fatal(err)
		}
	}
	active, problem := store.MismatchesIn(os.ReadFile)
	if len(active) != 1 || active[0].Service != "dashboard" {
		t.Fatalf("valid reader was not compared: %+v, %v", active, problem)
	}
	notes := (configComparison{active: active, activeError: problem}).notes()
	sections := strings.Split(notes, "\n")
	if len(sections) != 2 || !strings.HasPrefix(sections[0], "Running parts that cannot read") || !strings.Contains(sections[0], "the dashboard service") || strings.Contains(sections[0], "supervisor") {
		t.Fatalf("stale record presented as a running failure: %s", notes)
	}
	for _, want := range []string{"Configuration reader records and comparison problems:", "stale configuration reader record", "supervisor.json", stale.ConfigPath, "remains stale even after a different process starts", "other parts were still checked"} {
		if !strings.Contains(sections[1], want) {
			t.Errorf("stale record explanation lacks %q: %s", want, notes)
		}
	}
	if strings.Contains(notes, "next supervisor start") {
		t.Fatal("promises a restart will repair the old record")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("comparison unexpectedly modified live evidence: %v", err)
	}
}
