package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
)

// Every part that begins a run for a work item is wired the one decision on this
// product's own record. A part wired without it refuses nothing, which no test
// that supplies its own decision would show.
func TestEveryPartThatBeginsARunIsWiredTheDecisionOnTheNamedEpics(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := epicscope.ForOperator(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: "yoyodyne-9k1y", From: "terminal", Members: []string{"yoyodyne-9k1y.1"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	pull, err := openPull(configPath, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for name, decide := range map[string]orchestrator.StartDecision{
		"pipeline":                            pipelineFrom(parts).MayStart,
		"scheduler's pull":                    pull.MayStart,
		"decided fresh run":                   rerunnerFrom(parts).MayStart,
		"decided repair":                      repairContinuerFrom(parts).MayStart,
		"carry-out of both":                   carryOutFrom(parts).MayStart,
		"repair decided from feedback":        feedbackRepairerFrom(parts).MayStart,
		"repair round asked for on a request": reconcilerFrom(parts).MayStart,
		"taking up after GitHub's refusal":    gitHubIdentityStopsFrom(parts).MayStart,
	} {
		if decide == nil {
			t.Errorf("the %s is wired no decision, so it would start anything", name)
			continue
		}
		if decide(maystart.Run(maystart.ReadyWork, "yoyodyne-outside")).MayStart() || !decide(maystart.Run(maystart.ReadyWork, "yoyodyne-9k1y.1")).MayStart() {
			t.Errorf("the %s does not read this product's named epics", name)
		}
	}

	// What is refused is kept for yoyo status by the decision the part that starts
	// work each wired way is wired, until it may start.
	carrying := carryOutFrom(parts).MayStart
	wiredTo := map[maystart.Path][]orchestrator.StartDecision{
		maystart.ReadyWork:       {pull.MayStart, pipelineFrom(parts).MayStart},
		maystart.HoldTest:        {pull.MayStart, pipelineFrom(parts).MayStart},
		maystart.NamedRun:        {pipelineFrom(parts).MayStart},
		maystart.DecidedRerun:    {carrying, rerunnerFrom(parts).MayStart, pipelineFrom(parts).MayStart},
		maystart.DecidedRepair:   {carrying, repairContinuerFrom(parts).MayStart},
		maystart.FeedbackRepair:  {feedbackRepairerFrom(parts).MayStart, reconcilerFrom(parts).MayStart, pipelineFrom(parts).MayStart},
		maystart.ResolveConflict: {pipelineFrom(parts).MayStart},
		maystart.BringInFile:     {pipelineFrom(parts).MayStart},
		maystart.UpdateQueued:    {reconcilerFrom(parts).MayStart},
		maystart.UpdateForPerson: {reconcilerFrom(parts).MayStart},
		maystart.AfterLogin:      {gitHubIdentityStopsFrom(parts).MayStart},
	}
	parts.config.RecurringTasks = map[string]config.RecurringTask{"look": {Role: domain.RoleArchitect, Every: config.Duration(time.Hour), Enabled: true, Prompt: "look", MaxTurns: 1}}
	trigger := recurringTrigger(parts, configPath, io.Discard).(*orchestrator.Trigger)
	corrections, err := correctorFrom(parts, configPath, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	handback := mergeQueueDriverFrom(parts).Promoter.Repair.(orchestrator.MergeQueueRunHandback)
	wiredTo[maystart.BringUpToDate] = []orchestrator.StartDecision{handback.MayStart}
	wiredTo[maystart.RecurringPass] = []orchestrator.StartDecision{trigger.MayStart}
	wiredTo[maystart.PromptedPass] = []orchestrator.StartDecision{trigger.MayStart, corrections.(*orchestrator.Corrector).MayStart}
	wiredTo[maystart.UnmetGoalReview] = []orchestrator.StartDecision{trigger.MayStart}
	wiredTo[maystart.MessagesForWork] = []orchestrator.StartDecision{pull.MayStart}
	notRuns := map[maystart.Path]maystart.Start{
		maystart.RecurringPass:   maystart.Pass(maystart.RecurringPass, domain.RoleProgramManager),
		maystart.PromptedPass:    maystart.Pass(maystart.PromptedPass, domain.RoleProgramManager),
		maystart.UnmetGoalReview: maystart.GoalReview(),
		maystart.MessagesForWork: maystart.MessageReading(),
	}
	if trigger.WouldStart(notRuns[maystart.RecurringPass]).MayStart() || trigger.EpicsChanged().IsZero() {
		t.Error("the check for missed passes does not read this product's named epics")
	}
	items := func() []maystart.RefusedItem {
		t.Helper()
		refusals, err := maystart.ReadRefusals(parts.stateRoot, parts.config.Product.ID)
		if err != nil {
			t.Fatal(err)
		}
		return refusals.Items
	}
	decide := pipelineFrom(parts).MayStart
	for _, path := range maystart.Paths() {
		if !path.Wired || path.Path == maystart.PublishDocument {
			continue // a document is kept by its own entry, below
		}
		if len(wiredTo[path.Path]) == 0 {
			t.Errorf("%s is marked wired, and no part here is known to start work that way", path.Path)
		}
		for part, asking := range wiredTo[path.Path] {
			if start, other := notRuns[path.Path]; other {
				if asking == nil || asking(start).MayStart() {
					t.Errorf("%s, part %d does not read this product's named epics", path.Path, part)
				}
				continue
			}
			item := fmt.Sprintf("yoyodyne-other%d", part)
			before, began := len(items()), time.Now().UTC()
			refused := asking(maystart.Run(path.Path, item))
			got := items()
			if refused.MayStart() || len(got) != before+1 {
				t.Fatalf("%s, part %d: %+v and %d entries, want it refused and one more than %d", path.Path, part, refused, len(got), before)
			}
			if entry := got[before]; entry.WorkItemID != item || entry.Path != path.Path || entry.Why != refused.Why || entry.Since.Before(began) || entry.Last.Before(began) {
				t.Errorf("%s, part %d: %+v, want its item, path, reason and time", path.Path, part, entry)
			}
			if asking(maystart.Run(path.Path, item)); len(items()) != before+1 {
				t.Errorf("%s, part %d: refused again, it has %d entries, want %d", path.Path, part, len(items()), before+1)
			}
		}
	}
	document := maystart.DocumentRun(maystart.PublishDocument, "yoyodyne-other0").Of(maystart.Document{ID: "a-design", WriteID: "document-1.1"})
	documents := func() int {
		t.Helper()
		refusals, err := maystart.ReadRefusals(parts.stateRoot, parts.config.Product.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(refusals.Documents)
	}
	for _, asking := range []orchestrator.StartDecision{pipelineFrom(parts).MayStart, pull.MayStart} {
		if asking(document).MayStart() || documents() != 1 {
			t.Fatalf("%d documents are recorded as refused, want the one", documents())
		}
	}
	pipelineFrom(parts).DocumentEnded("a-design")
	if documents() != 0 {
		t.Fatal("a document that ended is still recorded as refused")
	}
	decide(document)
	before := len(items())
	if _, err := operator.Add("yoyodyne-9k1y", "yoyodyne-other1", time.Now()); err != nil {
		t.Fatal(err)
	}
	decide(maystart.Run(maystart.NamedRun, "yoyodyne-other1"))
	if got := items(); len(got) == 0 || len(got) >= before || slices.ContainsFunc(got, func(item maystart.RefusedItem) bool { return item.WorkItemID == "yoyodyne-other1" }) {
		t.Fatalf("refusals = %+v; want only the added item's taken away", got)
	}
	// A refused start of an item that closed is taken away by the sweep, which
	// asks the tracker nothing where none is recorded.
	tracker := &listedItems{{ID: "yoyodyne-other0", Title: "Other", Status: "closed"}}
	if err := clearRefusalsOfClosedItems(context.Background(), parts, tracker); err != nil {
		t.Fatal(err)
	}
	if got := items(); len(got) == 0 || slices.ContainsFunc(got, func(item maystart.RefusedItem) bool { return item.WorkItemID == "yoyodyne-other0" }) {
		t.Fatalf("refusals = %+v; want only the closed item's taken away", got)
	}
	if documents() != 1 {
		t.Fatal("an item it answers closing took away a refused document")
	}
	blocking := filepath.Join(home.ProductDirectory(parts.stateRoot, string(parts.config.Product.ID)), "not-started.json")
	if err := os.WriteFile(blocking, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused := decide(maystart.Run(maystart.NamedRun, "yoyodyne-outside")); refused.MayStart() || !strings.Contains(refused.Why, "could not be recorded") {
		t.Fatalf("refused = %+v, want a refusal saying it could not be recorded", refused)
	}
}
