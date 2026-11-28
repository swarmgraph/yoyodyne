package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A change that lands while one environment its item names is still to be
// checked leaves the item open: it is put back parked by the run, with a note
// naming the environment, its criteria and the commit the change landed at,
// and the run is put on the development manager's list.
func TestAChangeThatLandsWithAnEnvironmentStillToBeCheckedLeavesItsItemOpen(t *testing.T) {
	t.Parallel()

	tracker := environmentItem("environments-left-1", "Environments: android client (criteria 1, 2); ios client (criterion 3)")
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}, approveVerdict)
	pipeline, store, _ := environmentPipeline(t, tracker, provider, []config.Environment{
		{Name: "android client", Where: config.EnvironmentInHarness, Check: "true", Test: "true"},
		{Name: "ios client", Where: config.EnvironmentInHarness, Check: "echo 'no simulator runtime'; exit 1", Test: "true"},
	})
	docket, err := runstate.NewDocketStore(t.TempDir(), pipeline.Config.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the change integrated", outcome)
	}
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatal("the item was closed while an environment it names was still to be checked")
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Discharges() || !state.LeavesEnvironmentsToCheck() {
		t.Fatalf("discharges = %v, leaves environments = %v; want the item left open", state.Discharges(), state.LeavesEnvironmentsToCheck())
	}
	landed := recordedLandingCommit(state)
	if landed == "" {
		t.Fatal("the run recorded no landed commit")
	}
	if !tracker.Reopened || tracker.Item.Status != "open" || !tracker.Item.Parking.Parked() {
		t.Fatalf("reopened %v, status %q, parking %q; want the item open and parked", tracker.Reopened, tracker.Item.Status, tracker.Item.Parking.Reason())
	}
	if by, byRun := runstate.ParkingRun(tracker.Item.Parking.Reason()); !byRun || by != outcome.RunID {
		t.Fatalf("parking %q is not the run's own, so a re-run of it would be held", tracker.Item.Parking.Reason())
	}
	for _, want := range []string{`"ios client"`, "criterion 3", "commit " + landed, "development manager"} {
		if !strings.Contains(tracker.ReopenReason, want) {
			t.Errorf("note = %q, want it to say %q", tracker.ReopenReason, want)
		}
	}
	if strings.Contains(tracker.ReopenReason, "android client") {
		t.Errorf("note = %q names the environment that passed", tracker.ReopenReason)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatal(err)
	}
	var listed *triage.Entry
	for index := range entries {
		if entries[index].RunID == outcome.RunID && entries[index].Class == triage.ClassStoppedRun {
			listed = &entries[index]
		}
	}
	if listed == nil {
		t.Fatalf("docket = %#v, want the run on the development manager's list", entries)
	}
	if !strings.Contains(listed.Failure, `"ios client"`) || !strings.Contains(listed.Failure, landed) {
		t.Fatalf("entry failure = %q, want the environment and the landed commit", listed.Failure)
	}

	recheck := state.LandedEnvironmentsRecheck()
	if recheck == nil || recheck.RunID != outcome.RunID || !slices.Equal(recheck.Names, []string{"ios client"}) || recheck.Landed != landed {
		t.Fatalf("recheck = %#v, want ios client from run %s at %s", recheck, outcome.RunID, landed)
	}
}

// recheckPipeline is the fixture for a fresh run the development manager
// decided for an item whose earlier change left "ios client" still to be
// checked.
func recheckPipeline(t *testing.T, id string, provider *orchestratortest.Backend, iosTest string) (Pipeline, *runstate.Store, *environmentChecksSeen, *orchestratortest.Tracker) {
	t.Helper()
	tracker := environmentItem(id, "Environments: android client (criteria 1, 2); ios client (criterion 3)")
	pipeline, store, recorded := environmentPipeline(t, tracker, provider, []config.Environment{
		{Name: "android client", Where: config.EnvironmentInHarness, Check: "echo 'android check ran'", Test: "echo 'android test ran'"},
		{Name: "ios client", Where: config.EnvironmentInHarness, Check: "echo 'simulator booted'", Test: iosTest, TimeLimit: config.Duration(time.Minute)},
	})
	earlier, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Selection = runstate.Selection{
		By:           runstate.SelectedByDevelopmentManager,
		Reason:       "the development manager decided a fresh run once the simulator was installed",
		At:           time.Now().UTC(),
		Environments: &runstate.EnvironmentsRecheck{RunID: earlier, Landed: strings.Repeat("d", 40), Names: []string{"ios client"}},
	}
	return pipeline, store, recorded, tracker
}

// The fresh run checks only the environment still to be checked, runs the
// configured checks and its test at the target's head, records the result
// against that commit, and closes the item with no developer turn.
func TestARerunWhosePendingEnvironmentsPassClosesTheItemWithNoDeveloperTurn(t *testing.T) {
	t.Parallel()

	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store, recorded, tracker := recheckPipeline(t, "environments-left-2", provider, "echo 'the app opened on the simulator'")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("developer invocations = %d, want none", got)
	}
	if got := len(provider.RequestsForRole(domain.RoleReviewer)); got != 0 {
		t.Fatalf("reviewer invocations = %d, want none: there is no change", got)
	}
	if !outcome.WorkItemClosed || !tracker.Closed || !outcome.EnvironmentsConfirmed || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, closed %v; want the item closed", outcome, tracker.Closed)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.EnvironmentsConfirmed || state.Status != runstate.StatusSucceeded || state.Phase != runstate.PhaseComplete || state.Integration != nil {
		t.Fatalf("state: confirmed %v, %s/%s, integration %#v", state.EnvironmentsConfirmed, state.Status, state.Phase, state.Integration)
	}
	if len(state.Environments) != 1 || state.Environments[0].Name != "ios client" {
		t.Fatalf("environments checked = %#v, want only the one still to be checked", state.Environments)
	}
	at := state.EnvironmentTestsAt(state.BaseCommit)
	if state.BaseCommit == "" || len(at) != 1 || at[0].Name != "ios client" || !at[0].Passed() {
		t.Fatalf("tests = %#v, want ios client passing at the target's head %s", state.EnvironmentTests, state.BaseCommit)
	}
	ranChecks := false
	for _, request := range recorded.requests {
		for _, command := range request.Commands {
			if strings.Contains(command, "android") {
				t.Fatalf("the run ran %q, an environment that was not still to be checked", command)
			}
			if command == npmTestCheck {
				ranChecks = true
			}
		}
	}
	if !ranChecks {
		t.Fatal("the configured checks were not run at the target's head")
	}
	if !strings.Contains(tracker.Notes, "ios client") || !strings.Contains(tracker.Notes, state.BaseCommit) {
		t.Fatalf("notes = %q, want the test and the commit it ran on", tracker.Notes)
	}
}

// Where a pending environment's test fails at the target's head, the run goes
// on as an ordinary run to repair it, and the developer is told what failed.
func TestARerunWhosePendingEnvironmentFailsGoesOnAsAnOrdinaryRun(t *testing.T) {
	t.Parallel()

	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}, approveVerdict)
	pipeline, store, _, tracker := recheckPipeline(t, "environments-left-3", provider, "test -f src/farewell.ts || { echo 'the app has no farewell screen'; exit 5; }")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	developers := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developers) == 0 {
		t.Fatal("the developer was never invoked, want the run to go on to repair the failure")
	}
	first := developers[0].Prompt
	for _, want := range []string{"# What failed at the target's head before your turn", "the app has no farewell screen", `"ios client"`} {
		if !strings.Contains(first, want) {
			t.Errorf("first prompt does not say %q", want)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.EnvironmentsConfirmed {
		t.Fatal("the run was recorded as confirmed although a test failed")
	}
	if outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the repair integrated and the item closed", outcome)
	}
}

// A re-run of a run whose change landed with environments still to be checked
// is given them on its selection, and starts from the target rather than the
// landed run's branch.
func TestOnlyALandedRunLeavingEnvironmentsGivesAReRunEnvironmentsToCheck(t *testing.T) {
	t.Parallel()

	landed := strings.Repeat("e", 40)
	state := runstate.State{
		RunID: "run-0123456789abcdef0123456789abcdef", Status: runstate.StatusSucceeded,
		Integration: &runstate.Integration{TargetCommit: landed},
		Environments: []runstate.EnvironmentCheck{
			{Name: "android client", Result: runstate.EnvironmentPassed, Where: "harness", Criteria: []int{1}},
			{Name: "ios client", Result: runstate.EnvironmentFailed, Where: "harness", Criteria: []int{3}},
		},
	}
	if !landedLeavingEnvironments(state) {
		t.Fatal("a landed run with ios client still to be checked was not recognised")
	}
	if recheck := state.LandedEnvironmentsRecheck(); recheck == nil || !slices.Equal(recheck.Names, []string{"ios client"}) || recheck.Landed != landed {
		t.Fatalf("recheck = %#v", recheck)
	}
	if parking := environmentsLeftParking(state); !strings.HasPrefix(parking, "yoyodyne run "+state.RunID) || !strings.Contains(parking, `"ios client"`) {
		t.Fatalf("parking = %q", parking)
	}

	selection := rerunSelection(state, runstate.PreservedArtifacts{Branch: "yoyodyne/item/landed"}, "decided", time.Now())
	if selection.Lift != nil || selection.Environments == nil || !slices.Equal(selection.Environments.Names, []string{"ios client"}) || selection.Environments.RunID != state.RunID {
		t.Fatalf("re-run selection = %#v, want ios client to check and no lift", selection)
	}
	if err := selection.Validate(); err != nil {
		t.Fatalf("re-run selection is refused: %v", err)
	}
	if stamped, _ := selection.Stamped(time.Now()); stamped.Environments == nil {
		t.Fatal("the environments to check were lost when the selection was recorded")
	}

	failed := state
	failed.Status = runstate.StatusFailed
	if failed.LandedEnvironmentsRecheck() != nil || landedLeavingEnvironments(failed) {
		t.Fatal("a failed run was treated as a landed one")
	}
	none := state
	none.Environments = none.Environments[:1]
	if none.LandedEnvironmentsRecheck() != nil || landedLeavingEnvironments(none) || !none.Discharges() {
		t.Fatal("a run with every environment checked was treated as leaving some")
	}
	if selection := rerunSelection(none, runstate.PreservedArtifacts{}, "decided", time.Now()); selection.Environments != nil {
		t.Fatalf("re-run selection = %#v, want nothing to check", selection)
	}
	evidence := state
	evidence.LandingOutcome = "evidence"
	if landedLeavingEnvironments(evidence) {
		t.Fatal("an evidence landing was put on the list for its environments rather than parked as evidence")
	}
}
