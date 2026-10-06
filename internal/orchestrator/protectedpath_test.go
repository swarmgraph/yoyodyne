package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// trackerExport is the path `yoyo run` declares as the export a worktree is
// given the primary checkout's copy of, which the fixtures here configure too.
const trackerExport = ".beads/issues.jsonl"

// writeTrackerExport puts content where the tracker's export lives, in whichever
// checkout it is given: the primary one, whose copy a new worktree is handed, or
// a worktree, which is where a developer would find it.
func writeTrackerExport(t *testing.T, checkout, content string) {
	t.Helper()
	full := filepath.Join(checkout, filepath.FromSlash(trackerExport))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// exportingRepository is a checkout whose branch carries the tracker's export
// and whose working copy of it is newer, which is the ordinary state of a
// checkout between release cuts: the store moves every time an item is admitted,
// and the export is committed on a cadence of its own.
func exportingRepository(t *testing.T) string {
	t.Helper()
	repository := pipelineRepository(t)
	writeTrackerExport(t, repository, committedExport)
	runPipelineGit(t, repository, "add", trackerExport)
	runPipelineGit(t, repository, "commit", "-m", "export the tracker's items")
	writeTrackerExport(t, repository, refreshedExport)
	return repository
}

const (
	committedExport = "{\"id\":\"yoyodyne-1\"}\n"
	refreshedExport = "{\"id\":\"yoyodyne-1\"}\n{\"id\":\"yoyodyne-2\"}\n"
)

// writeUpstream puts a file where an upstream artifact lives, which is what a
// developer with an editor in its worktree does when it decides the intent it
// was given should say something else.
func writeUpstream(t *testing.T, worktree, relative, content string) error {
	t.Helper()
	full := filepath.Join(worktree, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o600)
}

// A grant of one of these paths is refused at admission, and a run refuses to
// start on an item carrying one however it got there. What neither gate catches
// is the item that describes the work and grants nothing — no marker, so nothing
// to refuse — whose developer would otherwise spend attempts looking for a way
// into a file the provider was never going to allow. The contract is what tells
// that developer, before its first attempt rather than after its last.
func TestTheDeveloperContractNamesEveryPathBeyondAGrant(t *testing.T) {
	t.Parallel()

	for _, entry := range protectedpath.ProviderPaths {
		if !strings.Contains(developerContract(scratchForTest, nil), entry.Path) {
			t.Fatalf("the developer contract never names %q, which no grant reaches", entry.Path)
		}
		if !strings.Contains(developerContract(scratchForTest, nil), entry.Provider) {
			t.Fatalf("the developer contract never names %q, which is what refuses %q", entry.Provider, entry.Path)
		}
	}
}

// The role definitions are the harness's own path beyond a grant, and the
// developer is told so before its first attempt for the reason it is told about
// the provider's.
func TestTheDeveloperContractNamesTheRoleDefinitionsAsBeyondAGrant(t *testing.T) {
	t.Parallel()

	if !strings.Contains(developerContract(scratchForTest, nil), protectedpath.RoleDefinitions+"/") {
		t.Fatalf("the developer contract never names %q, which no grant reaches", protectedpath.RoleDefinitions)
	}
}

// An item whose acceptance criteria grant a role definition reached no admission
// door, so the run is what refuses it — before it claims the item or spends an
// attempt.
func TestARunRefusesToStartOnAnItemGrantingARoleDefinition(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Give the developer a new capability",
		Status:             "open",
		AcceptanceCriteria: "The developer's definition carries it.\n" + protectedpath.GrantMarker + " " + protectedpath.RoleDefinitions + "/developer.yaml\n",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() on an item granting a role definition = nil error, want it refused before it started")
	}
	for _, want := range []string{protectedpath.RoleDefinitions + "/developer.yaml", "no grant reaches", "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q never names %q", err, want)
		}
	}
	if developers := len(provider.RequestsForRole(domain.RoleDeveloper)); developers != 0 {
		t.Fatalf("developer invocations = %d, want none", developers)
	}
	if tracker.Claimed {
		t.Fatal("the item was claimed by a run that could never be given the path")
	}
}

// A grant of the configuration directory admits the configuration and never the
// role definitions inside it: a change touching one is refused before any check
// runs, and the refusal says no grant would have admitted it.
func TestAChangeTouchingARoleDefinitionIsRefusedThoughTheItemGrantsTheConfiguration(t *testing.T) {
	t.Parallel()

	const roleDefinition = protectedpath.RoleDefinitions + "/developer.yaml"
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:          "yoyodyne-task",
		Title:       "Adjust the project configuration",
		Description: "The configuration needs a new check.\n\n" + protectedpath.GrantMarker + " .yoyodyne\n",
		Status:      "open",
	}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		if attempts == 1 {
			return writeUpstream(t, request.WorkingDirectory, roleDefinition, "capabilities: [everything]\n")
		}
		return os.RemoveAll(filepath.Join(request.WorkingDirectory, filepath.FromSlash(protectedpath.RoleDefinitions)))
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("the repaired change was not integrated on one refusal: %#v", outcome)
	}
	// No reviewer and no check judged the attempt that wrote a role definition.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want only the attempt that left the role definitions alone", reviews)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
	}
	for _, want := range []string{roleDefinition, "Granted by this work item: .yoyodyne", protectedpath.RoleInstruction} {
		if !strings.Contains(developerRequests[1].Prompt, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, developerRequests[1].Prompt)
		}
	}
	if _, err := attemptPipelineGit(repository, "show", "main:"+roleDefinition); err == nil {
		t.Fatal("the role definition reached the target branch")
	}
}

// A grant is honoured from an item's design guidance and acceptance criteria as
// well as from its title and description, and the two doors admission holds — a
// proposal and a tracker action — carry neither of those two: no action takes
// them, no creation sets them, and they reach an item through the tracker's own
// command. So the run reads all four itself and refuses to start, which is the
// same refusal one step later and still before anything is claimed or spent.
func TestARunRefusesToStartOnAnItemWhoseDesignGuidanceGrantsAPathNoProviderHonours(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Wire the goal guard into the developer's hook",
		Status: "open",
		Design: protectedpath.GrantMarker + " .claude/settings.json\n",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() on an item granting a path no provider honours = nil error, want it refused before it started")
	}
	// The refusal is worth having only if it says whose boundary this is and what
	// to do about it, exactly as the one admission gives does.
	for _, want := range []string{".claude/settings.json", "Claude Code", "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q never names %q", err, want)
		}
	}
	// The point of refusing here is that nothing was spent: this is the budget
	// ifd.153 burned three rounds of against the same wall.
	if developers := len(provider.RequestsForRole(domain.RoleDeveloper)); developers != 0 {
		t.Fatalf("developer invocations = %d, want none", developers)
	}
	if tracker.Claimed {
		t.Fatal("the item was claimed by a run that could never finish it")
	}
}

// The item whose done-condition lives in a document the run may not write is
// refused before it is claimed, however the condition reached it: the acceptance
// criteria are written with the tracker's own command and pass no admission
// door, which is the case admission cannot cover and this has to. The document
// is named as the incidents named it — by the design's name rather than its
// path — so the run has to read the homes it is about to cut from.
func TestARunRefusesToStartOnAnItemWhoseAcceptanceCriteriaNameAnUngrantedDesign(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	if err := writeUpstream(t, repository, "docs/designs/observability-and-dashboard.md",
		"---\nid: observability-and-dashboard\nkind: design\nstatus: in-force\nowner: architect\nsupports: [v1-goals]\nrevisions: []\n---\n# Observability\n"); err != nil {
		t.Fatalf("writeUpstream() error = %v", err)
	}
	runPipelineGit(t, repository, "add", "docs/designs")
	runPipelineGit(t, repository, "commit", "-m", "record the design")
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Add the capacity-blocked state to the read model",
		Description:        "The observability-and-dashboard design requires a query that does not exist.",
		AcceptanceCriteria: "The read model exposes the state; the observability-and-dashboard design's query list marks the query as existing.",
		Status:             "open",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() on an item whose criteria name an ungranted design = nil error, want it refused before it started")
	}
	// The refusal quotes the clause, names the document, and names both fixes,
	// exactly as admission's does: the run is what catches an item admission
	// never saw, so it has to say the same thing.
	for _, want := range []string{
		"docs/designs/observability-and-dashboard.md",
		"query list marks the query as existing",
		"governed path",
		protectedpath.GrantMarker,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q never says %q", err, want)
		}
	}
	// Nothing was spent and nothing was claimed, which is the whole point of
	// refusing here rather than parking after a run.
	if developers := len(provider.RequestsForRole(domain.RoleDeveloper)); developers != 0 {
		t.Fatalf("developer invocations = %d, want none", developers)
	}
	if tracker.Claimed {
		t.Fatal("the item was claimed by a run that could never discharge it")
	}

	// The same item granted the design is the ordinary case the grant exists for,
	// and it starts.
	tracker.Item.Design = protectedpath.GrantMarker + " docs/designs/observability-and-dashboard.md\n"
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() on the granted item error = %v", err)
	}
	if developers := len(provider.RequestsForRole(domain.RoleDeveloper)); developers != 1 {
		t.Fatalf("developer invocations = %d, want the granted item run once", developers)
	}
}

// yoyodyne-ifd.330 as run-f9e67240 was handed it: the architect's design work by
// its title and its done-means, naming no document and no executor, in a queue
// the tracker called ready. The run spent itself finding the design already
// landed. Now it is refused before it is claimed, with the marker named as the
// fix — and the same item carrying the marker is not refused on that account,
// because it is then the architect's work stated correctly.
func TestARunRefusesToStartOnConversationShapedWorkThatNamesNoExecutor(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:          "yoyodyne-ifd.330",
		Title:       "The architect designs side conversations with merge-back",
		Description: "Operator capability direction, 2026-09-07, design routed to the architect as directed. Done means the design is recorded in the governed documents - the stream shape, the merge write, the action-authority answer, the config knob - and implementation items can cite it.",
		Status:      "open",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() on 330's shape = nil error, want it refused before it started")
	}
	for _, want := range []string{
		"the design is recorded in the governed documents",
		"The architect designs side conversations",
		`executor "conversation:architect"`,
		"never selected for a developer run",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q never says %q", err, want)
		}
	}
	if developers := len(provider.RequestsForRole(domain.RoleDeveloper)); developers != 0 {
		t.Fatalf("developer invocations = %d, want none", developers)
	}
	if tracker.Claimed {
		t.Fatal("the item was claimed by a run that could never discharge it")
	}

	// Marked, the operator naming it is the operator deciding, and the condition
	// gate has nothing to refuse: whatever else stops such a run, it is not this.
	tracker.Item.Executor = domain.ConversationWith(domain.RoleArchitect)
	homes := protectedpath.ArtifactHomes(pipeline.Config)
	if problems := homes.ConditionProblems(protectedpath.Subject{Title: tracker.Item.Title, Description: tracker.Item.Description, Executor: tracker.Item.Executor}); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on the marked item = %v, want nothing", problems)
	}
}

// goalsRepository is a checkout whose branch already carries the product's
// goals, which is the state every real one is in. It matters here because what
// the gate has to catch is then a modification of a tracked document rather than
// a file the run invented, and an approval forged into one is written that way.
func goalsRepository(t *testing.T) string {
	t.Helper()
	repository := pipelineRepository(t)
	if err := writeUpstream(t, repository, goalsDocument, committedGoals); err != nil {
		t.Fatalf("writeUpstream() error = %v", err)
	}
	runPipelineGit(t, repository, "add", goalsDocument)
	runPipelineGit(t, repository, "commit", "-m", "record the product's goals")
	return repository
}

const (
	goalsDocument  = "docs/product/goals/v1-goals.md"
	committedGoals = "---\nid: v1-goals\nkind: goals\napprovals: []\n---\n\n# V1 goals\n"
	// The forgery in the shape it would actually take: a goal the run wants its
	// work to trace to, and an operator approval of it that no operator gave.
	forgedGoals = "---\nid: v1-goals\nkind: goals\napprovals:\n" +
		"    - revision: 1\n      by: operator\n      reason: approved\n---\n\n# V1 goals\n\n- Whatever this run is doing.\n"
)

// The whole admission policy rests on goal-level approval: work that traces to a
// goal the operator approved is admitted without asking them, and `yoyo artifact
// approve` records that approval in the goals document itself. A developer run is
// the one agent with a shell and a worktree, so this gate is what stands between
// it and an approval it wrote for itself — and it has to say so, because a
// developer that only learns the rule by tripping it spends an attempt on it.
func TestAChangeRewritingTheProductsGoalsIsRefusedWithTheGoalsDocumentNamed(t *testing.T) {
	t.Parallel()

	repository := goalsRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, goalsDocument, forgedGoals)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "protected paths refused") {
		t.Fatalf("Run() error = %v, want the goals refused", err)
	}
	// No reviewer was asked to judge a change that rewrote what it would have
	// judged the change against, and nothing reached the target branch.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatalf("reviews = %d, want none while the change rewrites the product's goals", reviews)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a refused change reached integration: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if promoted := gitOutput(t, repository, "show", "main:"+goalsDocument); promoted != committedGoals {
		t.Fatalf("the promoted goals = %q, want the approved copy %q", promoted, committedGoals)
	}
	// The refusal names the document it caught and how an exception is made, on
	// every attempt after the first.
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 3 {
		t.Fatalf("developer invocations = %d, want the first attempt and both repairs", len(developerRequests))
	}
	for _, repair := range developerRequests[1:] {
		for _, want := range []string{goalsDocument, "Granted by this work item: nothing", protectedpath.GrantMarker} {
			if !strings.Contains(repair.Prompt, want) {
				t.Fatalf("the refusal is missing %q:\n%s", want, repair.Prompt)
			}
		}
	}
	// And it survives the run, in the durable state and in what the operator is
	// left reading on the item.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal == nil || len(state.PathRefusal.Paths) != 1 || state.PathRefusal.Paths[0] != goalsDocument {
		t.Fatalf("durable refusal = %#v, want %q named", state.PathRefusal, goalsDocument)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, goalsDocument) {
		t.Fatalf("blocked = %t, blocker does not name %q:\n%s", tracker.Blocked, goalsDocument, tracker.BlockReason)
	}
}

func TestAChangeTouchingAnUngrantedProtectedPathIsRefusedBeforeAnythingJudgesIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		// The first attempt also rewrites the product's own brief; the repair
		// attempt takes it back out.
		if attempts == 1 {
			return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the product is whatever this run needed it to be\n")
		}
		return os.RemoveAll(filepath.Join(request.WorkingDirectory, "docs", "product"))
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.Closed || tracker.Blocked {
		t.Fatalf("the repaired change was not integrated: %#v, closed = %t, blocked = %t", outcome.Integration, tracker.Closed, tracker.Blocked)
	}
	// The refusal spent one attempt from the same budget the other two kinds of
	// repair draw on.
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the refusal to have spent one", outcome.RepairAttempts)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
	}
	// This is the whole point of the gate: no reviewer was asked about the
	// attempt that reached outside its item, so the class costs nothing to catch.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want only the attempt that stayed inside its scope", reviews)
	}
	// The rule is declared before the first attempt as well as in the refusal.
	// A gate a developer only meets by tripping it costs an attempt to learn.
	if !strings.Contains(developerRequests[0].Prompt, protectedpath.GrantMarker) {
		t.Fatalf("the first attempt was not told the rule:\n%s", developerRequests[0].Prompt)
	}
	repair := developerRequests[1]
	if repair.SessionID != provider.DeveloperSession || repair.WorkingDirectory != outcome.WorktreePath {
		t.Fatalf("the refusal did not go back to the same developer in the same worktree: %#v", repair)
	}
	for _, want := range []string{
		"repair attempt 1 of 2",
		"docs/product/brief.md",
		"Granted by this work item: nothing",
		// A developer that genuinely needs the path has to be told how one is
		// granted, or the gate is something to work around rather than to raise.
		protectedpath.GrantMarker,
	} {
		if !strings.Contains(repair.Prompt, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, repair.Prompt)
		}
	}
	// An integrated run carries no outstanding refusal: the change that was
	// promoted is the one that touched nothing it was not granted.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("integrated run kept the refusal it repaired: %#v", state.PathRefusal)
	}
	if _, err := attemptPipelineGit(repository, "show", "main:docs/product/brief.md"); err == nil {
		t.Fatal("the refused edit reached the target branch")
	}
}

// The refreshed export is kept out of a run's change by Git's skip-worktree bit,
// which lives in the worktree's index under `.git` — a directory a developer's
// sandbox grants writes to. So the bit is where the hold is kept rather than what
// enforces it: one `git update-index --no-skip-worktree` turns the copy the
// harness put there into a modification the harness would otherwise commit,
// promote, and conflict every other run against. What refuses it is this gate,
// which reads the change rather than the index, and it is the case the read-only
// posture already takes seriously — an agent following injected instructions is
// exactly who would flip the bit.
func TestARefreshedExportInTheChangeIsRefusedHoweverTheIndexBitEnded(t *testing.T) {
	t.Parallel()

	repository := exportingRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		if attempts == 1 {
			// The whole of the attack: the file's content is the harness's own
			// refresh, and lifting the hold is what makes it this run's change.
			if output, err := attemptPipelineGit(request.WorkingDirectory, "update-index", "--no-skip-worktree", "--", trackerExport); err != nil {
				return fmt.Errorf("lift the hold on %s: %v: %s", trackerExport, err, output)
			}
			return nil
		}
		// The repair takes it back out by putting the file at what the base commit
		// carries, which is what the refusal asks for.
		writeTrackerExport(t, request.WorkingDirectory, committedExport)
		return nil
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("the repaired change was not integrated on one refusal: %#v", outcome)
	}
	// No reviewer was asked about the attempt carrying the export, and no
	// promotion carried it: the export on the target is the one the branch had.
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want only the attempt that carried no export", reviews)
	}
	if promoted := gitOutput(t, repository, "show", "main:"+trackerExport); promoted != committedExport {
		t.Fatalf("the promoted export = %q, want the committed copy %q", promoted, committedExport)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
	}
	// A developer refused for a file it never wrote can act on the refusal only
	// if the refusal says what the file is and how it got there.
	for _, want := range []string{
		trackerExport,
		"Held out of every run's change by the harness: " + trackerExport,
		"derived from a store outside Git",
		"the hold was lifted",
	} {
		if !strings.Contains(developerRequests[1].Prompt, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, developerRequests[1].Prompt)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("integrated run kept the refusal it repaired: %#v", state.PathRefusal)
	}
}

// Lifting the hold is the obvious way to smuggle the export in and not the only
// one, and the other one is what says why the guard is this gate rather than a
// re-reading of the bit. A developer that stages the refreshed copy and then
// puts the bit back leaves an index Git still reports as `S`: a check that
// re-verified the hold at submission would find it intact and pass, while the
// commit the harness makes carries the staged blob anyway, because `git add
// --all` skipping a held path does not unstage what is already in the index. The
// gate reads the change against the run's base commit instead, and for a held
// path that comparison is against the index — so the state that defeats a bit
// check is the same one this refuses.
func TestAStagedExportIsRefusedThoughTheHoldLooksIntact(t *testing.T) {
	t.Parallel()

	repository := exportingRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	var heldAtAttack string
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		if attempts == 1 {
			// Stage the harness's own refresh, then hand the index back looking
			// exactly as it did before.
			for _, args := range [][]string{
				{"update-index", "--no-skip-worktree", "--", trackerExport},
				{"add", "--", trackerExport},
				{"update-index", "--skip-worktree", "--", trackerExport},
			} {
				if output, err := attemptPipelineGit(request.WorkingDirectory, args...); err != nil {
					return fmt.Errorf("git %v on %s: %v: %s", args, trackerExport, err, output)
				}
			}
			output, err := attemptPipelineGit(request.WorkingDirectory, "ls-files", "-v", "--", trackerExport)
			if err != nil {
				return fmt.Errorf("read the index state of %s: %v: %s", trackerExport, err, output)
			}
			heldAtAttack = strings.TrimSpace(output)
			return nil
		}
		// The repair the refusal asks for: let Git compare the path again, and put
		// it at the content the base commit carries so the change no longer has it.
		if output, err := attemptPipelineGit(request.WorkingDirectory, "update-index", "--no-skip-worktree", "--", trackerExport); err != nil {
			return fmt.Errorf("compare %s again: %v: %s", trackerExport, err, output)
		}
		writeTrackerExport(t, request.WorkingDirectory, committedExport)
		return nil
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The premise of the test: at the moment the gate ran, the hold was still on,
	// so nothing that only asked the index about it would have caught this.
	if !strings.HasPrefix(heldAtAttack, "S ") {
		t.Fatalf("the index state at the attack = %q, want the hold still set", heldAtAttack)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("the repaired change was not integrated on one refusal: %#v", outcome)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want only the attempt that carried no export", reviews)
	}
	if promoted := gitOutput(t, repository, "show", "main:"+trackerExport); promoted != committedExport {
		t.Fatalf("the promoted export = %q, want the committed copy %q", promoted, committedExport)
	}
	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
	}
	if !strings.Contains(developerRequests[1].Prompt, "Held out of every run's change by the harness: "+trackerExport) {
		t.Fatalf("the refusal does not name the export it caught:\n%s", developerRequests[1].Prompt)
	}
}

// The other half of the same rule: the export a run leaves held is not part of
// its change, so a run that reads it and writes its own work costs nothing. A
// gate that refused the ordinary case would refuse every run this project makes.
func TestAnExportLeftHeldIsNotRefusedThoughTheWorktreeCarriesTheRefreshedCopy(t *testing.T) {
	t.Parallel()

	repository := exportingRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	var read string
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		content, err := os.ReadFile(filepath.Join(request.WorkingDirectory, filepath.FromSlash(trackerExport)))
		if err != nil {
			return err
		}
		read = string(content)
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 0 {
		t.Fatalf("a run that only read the export was refused: %#v", outcome)
	}
	// It read the current export rather than the copy its base commit carried,
	// which is the reason the refresh exists at all.
	if read != refreshedExport {
		t.Fatalf("the developer read %q, want the refreshed export %q", read, refreshedExport)
	}
	if promoted := gitOutput(t, repository, "show", "main:"+trackerExport); promoted != committedExport {
		t.Fatalf("the promoted export = %q, want the committed copy %q", promoted, committedExport)
	}
}

func TestAGrantInTheWorkItemAdmitsThePathForEveryAttemptTheItemMakes(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	// The precedent this has to cover: an item whose own work is to move a
	// design document. The grant is in the item, written and reviewed before the
	// run started, which is what makes it somebody's decision rather than the
	// developer's.
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:          "yoyodyne-task",
		Title:       "Move the design document into its home",
		Description: "The design has to move.\n\nProtected-path grant: docs/designs/v1-harness-design.md\n",
		Status:      "open",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, "docs/designs/v1-harness-design.md", "the design, moved\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f docs/designs/v1-harness-design.md"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 0 {
		t.Fatalf("a granted path did not pass the gate: %#v", outcome)
	}
	if invocations := len(provider.RequestsForRole(domain.RoleDeveloper)); invocations != 1 {
		t.Fatalf("developer invocations = %d, want the granted change to stand on its first attempt", invocations)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("a granted path was refused: %#v", state.PathRefusal)
	}
	// The gate admitted the path; whether anybody decided what went into it is
	// the other half of the same mechanism, and that half is the reviewer's. So
	// the review this run asked for carries both the instruction to look for the
	// decision behind the grant and the item text the grant is written in.
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want the granted change judged once", len(reviews))
	}
	if !strings.Contains(reviews[0].SystemPrompt, "read the item for the decided change named behind each grant") {
		t.Fatalf("the reviewer was not asked what decided the granted edit:\n%s", reviews[0].SystemPrompt)
	}
	if !strings.Contains(strings.ToLower(reviews[0].Prompt), protectedpath.GrantMarker) {
		t.Fatalf("the granting item text never reached the reviewer:\n%s", reviews[0].Prompt)
	}
	// The grant covers the file it names and nothing around it.
	if refused := protectedpath.Protect(pipeline.Config).Refused(
		[]string{"docs/designs/another-design.md"},
		protectedpath.Grants(tracker.Item.Description),
	); len(refused) != 1 {
		t.Fatalf("the grant admitted more than the path it named: %v", refused)
	}
}

// The gate cannot ask who typed a grant — the tracker records no authorship it
// could check — so it relies on when instead: the fields a grant is read from
// exist before the run and the harness never writes to them. The notes are the
// one field it does write to, and what it appends there includes the reviewer's
// own summary and findings. A grant read from the notes could therefore be an
// agent's prose admitting a path to the next run of the same item, which is the
// thing this gate exists to stop.
func TestAGrantInTheItemsNotesDoesNotAdmitAPath(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Task",
		Status: "open",
		// Exactly the shape a previous run's recorded outcome leaves behind: the
		// reviewer's own words, appended to the item by the harness.
		Notes: "Review summary: the change is fine.\nProtected-path grant: docs/product\n",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "a brief the run preferred\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "protected paths refused") {
		t.Fatalf("Run() error = %v, want the notes to have granted nothing", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal == nil || len(state.PathRefusal.Grants) != 0 {
		t.Fatalf("durable refusal = %#v, want the notes to have granted nothing", state.PathRefusal)
	}
	// The same words in a field somebody authored do admit the path, so what is
	// being tested is where the grant was read from rather than how it was
	// written.
	tracker.Item.Design = tracker.Item.Notes
	if granted := protectedpath.Grants(grantEvidence(tracker.Item)...); len(granted) != 1 || granted[0] != "docs/product" {
		t.Fatalf("Grants() from an authored field = %v, want the path admitted", granted)
	}
}

func TestARefusedChangeBlocksTheItemWhenTheRepairBudgetIsSpent(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, "docs/decisions/invariants/new-invariant.md", "an invariant this run wrote for itself\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	before := gitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	wantFailure := "protected paths refused after 2 of 2 permitted attempt(s)"
	if err == nil || !strings.Contains(err.Error(), wantFailure) {
		t.Fatalf("Run() error = %v, want %q", err, wantFailure)
	}
	if invocations := len(provider.RequestsForRole(domain.RoleDeveloper)); invocations != 3 {
		t.Fatalf("developer invocations = %d, want the first attempt and both repairs", invocations)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatalf("reviews = %d, want none while the change reaches outside its item", reviews)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a refused change reached integration: %#v, closed = %t", outcome.Integration, tracker.Closed)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
		t.Fatalf("main moved with a refused change: %q, want %q", head, before)
	}
	if !tracker.Blocked || !outcome.Blocked {
		t.Fatalf("the spent budget did not block the item: tracker = %t, outcome = %t", tracker.Blocked, outcome.Blocked)
	}
	// What a person has to decide is which of the two is wrong, so the note says
	// both and settles neither.
	for _, want := range []string{
		"Repair attempts: 2 of 2 permitted",
		"Refused paths: docs/decisions/invariants/new-invariant.md",
		"Granted by this work item: nothing",
		"missing a grant it should have had",
		outcome.WorktreePath,
		outcome.Branch,
	} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Fatalf("blocker is missing %q:\n%s", want, tracker.BlockReason)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.RepairAttempts != 2 || state.PathRefusal == nil {
		t.Fatalf("state = %#v", state)
	}
	if len(state.PathRefusal.Paths) != 1 || state.PathRefusal.Paths[0] != "docs/decisions/invariants/new-invariant.md" {
		t.Fatalf("durable refusal = %#v", state.PathRefusal)
	}
}

// A refusal is the gate's decision about the change in front of it, so the
// evidence an earlier attempt collected must not survive beside it: a check that
// passed on a change this one has moved past would otherwise read as a gate this
// attempt cleared.
func TestARefusalReplacesWhateverEarlierEvidenceTheRunWasCarrying(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt fails its check; the second passes it and reaches
		// outside the item instead.
		if attempts == 1 {
			return nil
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		return writeUpstream(t, request.WorkingDirectory, "docs/product/goals/v1-goals.md", "goals this run preferred\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "protected paths refused after 1 of 1 permitted attempt(s)") {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal == nil {
		t.Fatal("no refusal was recorded")
	}
	if state.CheckFailure != nil {
		t.Fatalf("the refusal left an earlier attempt's failing check to compete with it: %#v", state.CheckFailure)
	}
}

func TestAResumedRunIsHandedBackTheRefusalItWasRecordedWith(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The first process keeps rewriting the configuration and is interrupted
	// once its second attempt is already recorded. What survives is an attempt
	// counted against the budget together with the refusal that triggered it.
	interrupted := &interruptedStore{StateStore: store, atAttempt: 2, allowSaves: 1}
	first := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, ".yoyodyne/config.yaml", "checks: []\n")
	}, approveVerdict)
	firstPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first)
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !interrupted.stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.stopped)
	}
	interruptedState, err := store.Load(firstOutcome.RunID)
	if err != nil {
		t.Fatalf("Load() interrupted state error = %v", err)
	}
	if interruptedState.Status.Terminal() || interruptedState.RepairAttempts != 2 || interruptedState.Phase != runstate.PhaseDeveloping {
		t.Fatalf("interrupted state = %#v, want 2 attempts in the developing phase", interruptedState)
	}
	if interruptedState.PathRefusal == nil || interruptedState.PathRefusal.Paths[0] != ".yoyodyne/config.yaml" {
		t.Fatalf("interrupted state lost the refusal: %#v", interruptedState.PathRefusal)
	}

	// The second process rebuilds the interrupted attempt from durable state
	// rather than from a gate it re-ran to discover, and this attempt takes the
	// path back out.
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.RemoveAll(filepath.Join(request.WorkingDirectory, ".yoyodyne")); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	resumed := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := resumed.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != firstOutcome.RunID || outcome.RepairAttempts != 2 || outcome.Integration == nil {
		t.Fatalf("resumed run did not finish at the recorded attempt: %#v", outcome)
	}
	developerRequests := second.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("resumed developer invocations = %d, want the one recorded attempt reissued", len(developerRequests))
	}
	for _, want := range []string{"repair attempt 2 of 2", ".yoyodyne/config.yaml", protectedpath.GrantMarker} {
		if !strings.Contains(developerRequests[0].Prompt, want) {
			t.Fatalf("resumed refusal is missing %q:\n%s", want, developerRequests[0].Prompt)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("integrated run kept the refusal it repaired: %#v", state.PathRefusal)
	}
}

// The developer run's own contract carries the rule that a decision the role's
// authority covers is made and reported afterwards rather than put to the
// operator for approval: it is the contract a developer actually works under,
// and a project's persona can drop what the persona says.
func TestTheDeveloperContractDecidesAndReportsRatherThanRoutingApprovals(t *testing.T) {
	t.Parallel()

	if !strings.Contains(developerContract(scratchForTest, nil), terms.DecideAndReport) {
		t.Fatal("the developer contract does not carry the rule against routing approvals to the operator")
	}
}

func TestTheDeveloperContractAppliesStandingGoals(t *testing.T) {
	t.Parallel()

	if !strings.Contains(developerContract(scratchForTest, nil), terms.StandingGoals) {
		t.Fatal("the developer run contract does not apply standing goals to its own output and decisions")
	}
	if !strings.Contains(developerContract(scratchForTest, nil), terms.PersonWriting) {
		t.Fatal("the developer run contract does not hold what it writes for a person to ordinary words and local time")
	}
}
