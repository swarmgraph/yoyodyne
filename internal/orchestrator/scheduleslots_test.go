package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A developer slot that prefers a label: it pulls the label's ready work first
// and the rest only when none is ready, a slot with no preference leaves labelled
// work to it while it is free, and the report says which slot took what and what
// was left for another slot.

// labelled builds one open item at one priority carrying labels.
func labelled(id string, priority int, labels ...string) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: id, Status: "open", Priority: priority, Labels: labels}
}

// slotReplay holds every run this harness starts until the test releases it, so
// a pull's choice is observed against exactly the runs in flight the test put
// there rather than against whatever happened to finish first.
type slotReplay struct {
	harness  *scheduleHarness
	releases map[string]chan struct{}
}

func newSlotReplay(items ...beads.WorkItem) *slotReplay {
	replay := &slotReplay{harness: newScheduleHarness(items...), releases: map[string]chan struct{}{}}
	for _, item := range items {
		replay.releases[item.ID] = make(chan struct{})
	}
	replay.harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-replay.releases[id]
		return h.complete(id), nil
	}
	return replay
}

// release lets one run finish, once the pull that started it has been observed
// to have started as many runs as the test expects so far.
//
// What the runs were started in is read from the schedule afterwards rather
// than from the harness's order of starts: two runs one pull starts are two
// goroutines, and which of them reaches the harness first is nothing the
// scheduler decides.
//
// The wait is on the harness's own announcement of a start and on nothing
// else: a scheduler that never starts the run is reported by the binary's own
// timeout with this goroutine named, rather than by a bound here that a loaded
// machine reaches with the scheduler working.
func (r *slotReplay) release(t *testing.T, id string, startedSoFar int) {
	t.Helper()
	for len(r.harness.pullOrder()) < startedSoFar {
		<-r.harness.started
	}
	close(r.releases[id])
}

// startedOrder is the order the scheduler chose items in, as its own record
// says it.
func startedOrder(schedule Schedule) []string {
	order := make([]string, 0, len(schedule.Started))
	for _, started := range schedule.Started {
		order = append(order, started.WorkItemID)
	}
	return order
}

// The acceptance criterion as a replay: two slots, one preferring the dashboard
// label, over a backlog of labelled and unlabelled ready items in the product
// manager's order. The preferring slot pulls dashboard work ahead of higher
// priority unlabelled items, the other slot takes the unlabelled work in order,
// and once the label's work is exhausted the preferring slot falls back to the
// rest rather than idling.
func TestAPreferringSlotPullsItsLabelFirstAndFallsBackOnceItIsExhausted(t *testing.T) {
	t.Parallel()

	replay := newSlotReplay(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-dash-1", 2, "dashboard"),
		labelled("yoyodyne-plain-2", 3),
		labelled("yoyodyne-dash-2", 4, "dashboard"),
		labelled("yoyodyne-plain-3", 5),
	)
	harness := replay.harness
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}

	go func() {
		// Pull 1 fills both slots: dashboard work into slot 1, the top unlabelled
		// item into slot 2. Freeing slot 1 pulls the next dashboard item; freeing
		// slot 2 pulls the next unlabelled one; freeing slot 1 again finds no
		// dashboard work and falls back to what is left.
		replay.release(t, "yoyodyne-dash-1", 2)
		replay.release(t, "yoyodyne-plain-1", 3)
		replay.release(t, "yoyodyne-dash-2", 4)
		replay.release(t, "yoyodyne-plain-2", 5)
		replay.release(t, "yoyodyne-plain-3", 5)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-dash-1", "yoyodyne-plain-1", "yoyodyne-dash-2", "yoyodyne-plain-2", "yoyodyne-plain-3"}
	if got := startedOrder(schedule); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v\n%s", got, want, schedule.Render())
	}
	slots := map[string]int{}
	for _, started := range schedule.Started {
		slots[started.WorkItemID] = started.Slot
	}
	// One item was left for another slot along the way: at the pull that freed
	// slot 1 while plain-1 still held slot 2, slot 1 pulled dash-2 ahead of
	// plain-2, and plain-2 says so — and nothing else does. An item in flight
	// that a preferring slot walked past is not reported as left for anyone.
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID != "yoyodyne-plain-2" || !strings.Contains(deferred.Reason, "pulled yoyodyne-dash-2 ahead of it") {
			t.Errorf("%s was reported as not pulled: %s", deferred.WorkItemID, deferred.Reason)
		}
	}
	if len(schedule.Deferred) != 1 {
		t.Errorf("deferred = %+v, want plain-2 alone reported as left for another slot", schedule.Deferred)
	}
	for id, want := range map[string]int{
		"yoyodyne-dash-1": 1, "yoyodyne-plain-1": 2, "yoyodyne-dash-2": 1, "yoyodyne-plain-2": 2, "yoyodyne-plain-3": 1,
	} {
		if slots[id] != want {
			t.Errorf("%s was pulled into slot %d, want slot %d", id, slots[id], want)
		}
	}
	// Each run's recorded reason says which slot took it and on what footing.
	for id, want := range map[string]string{
		"yoyodyne-dash-1":  "pulled into developer slot 1, which prefers the dashboard label this item carries",
		"yoyodyne-plain-1": "pulled into developer slot 2, which prefers no label",
		"yoyodyne-plain-3": "pulled into developer slot 1, which prefers the dashboard label and found none of that work ready, so it fell back to the rest of the backlog",
	} {
		if reason := harness.selectionFor(id).Reason; !strings.Contains(reason, want) {
			t.Errorf("%s reason = %q, want it to say %q", id, reason, want)
		}
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the queue drained", schedule.Stopped)
	}
}

// configurationGuide is the operator document whose developer-slot example is
// this project's own configuration: one slot preferring the reliability label.
const configurationGuide = "../../docs/configuration.md"

// configurationGuideSlotsHeading opens the section that example sits in. The
// test below reads the first fenced YAML block under it as data, so renaming the
// heading or moving the block is a change to the test as much as to the guide.
const configurationGuideSlotsHeading = "### A developer slot that prefers a label"

// guideDeveloperSlots reads the guide's developer-slot example and loads it as a
// project configuration, so what the replay drives is the block the operator is
// told to paste rather than a copy of it kept here. The block states only the
// execution section; the rest of a loadable project is wrapped around it, with
// as many developer instances as the block's capacity asks for.
func guideDeveloperSlots(t *testing.T) (capacity int, slots []domain.DeveloperSlot) {
	t.Helper()
	guide, err := os.ReadFile(configurationGuide)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", configurationGuide, err)
	}
	_, section, found := strings.Cut(string(guide), configurationGuideSlotsHeading+"\n")
	if !found {
		t.Fatalf("%s has no %q section", configurationGuide, configurationGuideSlotsHeading)
	}
	_, fenced, found := strings.Cut(section, "```yaml\n")
	if !found {
		t.Fatalf("the %q section of %s has no yaml block", configurationGuideSlotsHeading, configurationGuide)
	}
	block, _, found := strings.Cut(fenced, "```")
	if !found {
		t.Fatalf("the yaml block under %q in %s is not closed", configurationGuideSlotsHeading, configurationGuide)
	}
	if !strings.HasPrefix(block, "execution:\n") {
		t.Fatalf("the block under %q is not an execution section:\n%s", configurationGuideSlotsHeading, block)
	}

	project := t.TempDir()
	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	contents := "version: 1\nextends: builtin:v1\nproduct:\n  id: example\n  repository: .\n" + block +
		"agents:\n  developer:\n    instances: 3\n"
	if err := os.WriteFile(filepath.Join(directory, config.FileName), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.Load(filepath.Join(directory, config.FileName))
	if err != nil {
		t.Fatalf("the guide's developer-slot block does not load as a configuration: %v\n%s", err, block)
	}
	return cfg.Execution.MaxConcurrentDevelopers, cfg.Execution.DeveloperSlots
}

// The reliability seat as a replay, over the block the guide shows: three slots,
// the first preferring the reliability label. A reliability-labelled bug at
// priority 2 is pulled by slot 1 ahead of the unlabelled item at priority 1 while
// the other two slots take the unlabelled work in the product manager's order,
// the next reliability item goes to slot 1 the moment it frees, and once no
// reliability work remains slot 1 falls back to the rest of the backlog rather
// than idling.
func TestTheReliabilitySlotPullsReliabilityWorkFirstAndFallsBackWhenNoneRemains(t *testing.T) {
	t.Parallel()

	capacity, slots := guideDeveloperSlots(t)
	if capacity != 3 || len(slots) != 1 || !slots[0].Prefers([]string{"reliability"}) || slots[0].Prefers([]string{"dashboard"}) {
		t.Fatalf("the guide's block gives capacity %d and slots %+v, want three slots with the first preferring reliability alone", capacity, slots)
	}

	replay := newSlotReplay(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-reliability-1", 2, "reliability", "bug"),
		labelled("yoyodyne-plain-2", 3),
		labelled("yoyodyne-plain-3", 4),
		labelled("yoyodyne-reliability-2", 5, "reliability"),
	)
	harness := replay.harness
	harness.capacity = capacity
	harness.slots = slots

	go func() {
		// Pull 1 fills all three slots: reliability-1 into slot 1 ahead of the
		// higher-priority plain-1, which slot 2 takes, and plain-2 into slot 3.
		// Freeing slot 1 pulls reliability-2 ahead of the higher-priority plain-3;
		// freeing it again finds no reliability work and falls back to plain-3.
		replay.release(t, "yoyodyne-reliability-1", 3)
		replay.release(t, "yoyodyne-reliability-2", 4)
		replay.release(t, "yoyodyne-plain-1", 5)
		replay.release(t, "yoyodyne-plain-2", 5)
		replay.release(t, "yoyodyne-plain-3", 5)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-reliability-1", "yoyodyne-plain-1", "yoyodyne-plain-2", "yoyodyne-reliability-2", "yoyodyne-plain-3"}
	if got := startedOrder(schedule); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v\n%s", got, want, schedule.Render())
	}
	for _, started := range schedule.Started {
		want := 1
		switch started.WorkItemID {
		case "yoyodyne-plain-1":
			want = 2
		case "yoyodyne-plain-2":
			want = 3
		}
		if started.Slot != want {
			t.Errorf("%s was pulled into slot %d, want slot %d", started.WorkItemID, started.Slot, want)
		}
	}
	// plain-3 is the one item slot 1 walked past for its label with no other slot
	// free, and the report says which slot pulled what ahead of it.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != "yoyodyne-plain-3" ||
		!strings.Contains(schedule.Deferred[0].Reason, "developer slot 1 prefers the reliability label and pulled yoyodyne-reliability-2 ahead of it") {
		t.Errorf("deferred = %+v, want plain-3 alone reported as left for another slot", schedule.Deferred)
	}
	for id, want := range map[string]string{
		"yoyodyne-reliability-1": "pulled into developer slot 1, which prefers the reliability label this item carries",
		"yoyodyne-reliability-2": "pulled into developer slot 1, which prefers the reliability label this item carries",
		"yoyodyne-plain-1":       "pulled into developer slot 2, which prefers no label",
		"yoyodyne-plain-3":       "pulled into developer slot 1, which prefers the reliability label and found none of that work ready, so it fell back to the rest of the backlog",
	} {
		if reason := harness.selectionFor(id).Reason; !strings.Contains(reason, want) {
			t.Errorf("%s reason = %q, want it to say %q", id, reason, want)
		}
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the queue drained", schedule.Stopped)
	}
}

// A slot with no preference leaves labelled work to a preferring slot that is
// free to take it: with both slots free and only one dashboard item ready, the
// dashboard item goes to slot 1 however the queue ranks it, and slot 2 takes the
// unlabelled work. Where no preferring slot is free, a label is only a
// preference: the unpreferring slot takes labelled work in the order.
func TestASlotWithNoPreferenceLeavesLabelledWorkToAFreePreferringSlot(t *testing.T) {
	t.Parallel()

	replay := newSlotReplay(
		labelled("yoyodyne-dash-1", 1, "dashboard"),
		labelled("yoyodyne-dash-2", 2, "dashboard"),
		labelled("yoyodyne-plain-1", 3),
	)
	harness := replay.harness
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}

	go func() {
		// Pull 1: dash-1 into slot 1; slot 2, with no preferring slot free, takes
		// dash-2 rather than idling over labelled work. Then plain-1 fills
		// whichever frees first.
		replay.release(t, "yoyodyne-dash-1", 2)
		replay.release(t, "yoyodyne-dash-2", 3)
		replay.release(t, "yoyodyne-plain-1", 3)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-dash-1", "yoyodyne-dash-2", "yoyodyne-plain-1"}
	if got := startedOrder(schedule); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v\n%s", got, want, schedule.Render())
	}
	if schedule.Started[0].Slot != 1 || schedule.Started[1].Slot != 2 {
		t.Fatalf("slots = %d, %d, want dash-1 in slot 1 and dash-2 in slot 2: %s", schedule.Started[0].Slot, schedule.Started[1].Slot, schedule.Render())
	}
}

// An item the only free slot walked past for its label is reported as left for
// another slot rather than as deferred: it waits on nothing about itself, and
// the line says which slot pulled what ahead of it. It is also what the idle
// account carries, in its own class, so the alarm can say whose move it is.
func TestTheReportNamesWorkLeftForAnotherSlotRatherThanDeferred(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-dash-1", 2, "dashboard"),
	)
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}
	// Slot 2 is taken by a run another process started, so the only free slot is
	// the one preferring dashboard.
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// The dashboard run ends with the other slot still taken, so the pull
		// that follows finds slot 1 free again and falls back to plain-1.
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := harness.pullOrder(); strings.Join(got, ",") != "yoyodyne-dash-1" {
		t.Fatalf("pull order = %v, want the dashboard item alone: %s", got, schedule.Render())
	}
	var left *Deferred
	for index := range schedule.Deferred {
		if schedule.Deferred[index].WorkItemID == "yoyodyne-plain-1" {
			left = &schedule.Deferred[index]
		}
	}
	if left == nil {
		t.Fatalf("plain-1 is not on the report as passed over: %s", schedule.Render())
	}
	for _, want := range []string{
		string(runstate.PassedOverLeftForAnotherSlot),
		"developer slot 1 prefers the dashboard label and pulled yoyodyne-dash-1 ahead of it",
		"a slot with no preference takes this item in the Lead Product Manager's order",
	} {
		if !strings.Contains(left.Reason, want) {
			t.Errorf("reason = %q, want it to say %q", left.Reason, want)
		}
	}
}

// A project whose slots prefer nothing pulls exactly as it did before slots
// could prefer anything, and its runs record exactly the reason they always did.
func TestSlotsPreferringNothingChangeNothing(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-dash-1", 2, "dashboard"),
	)
	harness.capacity = 1
	harness.slots = []domain.DeveloperSlot{{}}

	if _, err := (Scheduler{Open: harness.open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := harness.pullOrder(); strings.Join(got, ",") != "yoyodyne-plain-1,yoyodyne-dash-1" {
		t.Fatalf("pull order = %v, want the product manager's order", got)
	}
	if reason := harness.selectionFor("yoyodyne-plain-1").Reason; strings.Contains(reason, "developer slot 1") {
		t.Fatalf("reason = %q, want no slot named where no slot prefers anything", reason)
	}
}

// A slot marked dedicated is kept for its label: with its label's work and
// priority-0 work elsewhere both ready, it takes its label's work, and a slot
// with no preference takes the priority-0 work.
func TestADedicatedSlotTakesItsLabelAheadOfPriorityZeroWorkElsewhere(t *testing.T) {
	t.Parallel()

	replay := newSlotReplay(
		beads.WorkItem{ID: "yoyodyne-urgent", Title: "Main's build is red", Status: "open", Priority: 0},
		beads.WorkItem{ID: "yoyodyne-team", Title: "Collaborators share one tracker", Status: "open", Priority: 2, Labels: []string{"team-development"}},
	)
	harness := replay.harness
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{}, {Prefer: []string{"team-development"}, Dedicated: true}}

	go func() {
		replay.release(t, "yoyodyne-team", 2)
		replay.release(t, "yoyodyne-urgent", 2)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	slots := map[string]int{}
	for _, started := range schedule.Started {
		slots[started.WorkItemID] = started.Slot
	}
	if slots["yoyodyne-team"] != 2 || slots["yoyodyne-urgent"] != 1 {
		t.Fatalf("slots = %v, want the team-development item in dedicated slot 2 and the priority-0 item in slot 1\n%s", slots, schedule.Render())
	}
	if reason := harness.selectionFor("yoyodyne-team").Reason; !strings.Contains(reason, "pulled into developer slot 2, which is kept for the team-development label this item carries") {
		t.Errorf("team reason = %q, want it to say the dedicated slot took its label's work", reason)
	}
	if len(schedule.Deferred) != 0 {
		t.Errorf("deferred = %+v, want nothing left waiting with a slot free for it", schedule.Deferred)
	}
}

// With only one slot free, a dedicated slot and a slot that only prefers the
// label part ways: the dedicated one takes its label's work and the report says
// the priority-0 item waits for one of the other slots, naming both items by
// title with the identifier after it; the preferring one takes the priority-0
// work, as it always has.
func TestOneFreeSlotDedicatedOrNotDecidesWhetherPriorityZeroWaits(t *testing.T) {
	t.Parallel()

	for _, dedicated := range []bool{true, false} {
		t.Run(map[bool]string{true: "dedicated", false: "preferring"}[dedicated], func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(
				beads.WorkItem{ID: "yoyodyne-urgent", Title: "Main's build is red", Status: "open", Priority: 0},
				beads.WorkItem{ID: "yoyodyne-team", Title: "Collaborators share one tracker", Status: "open", Priority: 2, Labels: []string{"team-development"}},
			)
			harness.capacity = 2
			harness.slots = []domain.DeveloperSlot{{Prefer: []string{"team-development"}, Dedicated: dedicated}, {}}
			// Slot 2 is taken by a run another process started, so the only free
			// slot is slot 1.
			harness.inFlight["yoyodyne-elsewhere"] = runstate.State{RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning}

			schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			want := "yoyodyne-urgent"
			if dedicated {
				want = "yoyodyne-team"
			}
			if got := strings.Join(harness.pullOrder(), ","); got != want {
				t.Fatalf("pull order = %v, want %s: %s", got, want, schedule.Render())
			}
			var waiting *Deferred
			for index := range schedule.Deferred {
				if schedule.Deferred[index].WorkItemID == "yoyodyne-urgent" {
					waiting = &schedule.Deferred[index]
				}
			}
			if !dedicated {
				if reason := harness.selectionFor("yoyodyne-urgent").Reason; !strings.Contains(reason, "is taken ahead of that work because it is at priority 0") {
					t.Errorf("urgent reason = %q, want the preferring slot to say it took priority-0 work", reason)
				}
				return
			}
			if waiting == nil {
				t.Fatalf("the priority-0 item is not on the report as waiting: %s", schedule.Render())
			}
			for _, want := range []string{
				string(runstate.PassedOverLeftForAnotherSlot),
				"Main's build is red (yoyodyne-urgent) is at priority 0 and is waiting for one of the other developer slots, because developer slot 1 is kept for the team-development label and took Collaborators share one tracker (yoyodyne-team)",
			} {
				if !strings.Contains(waiting.Reason, want) {
					t.Errorf("reason = %q, want it to say %q", waiting.Reason, want)
				}
			}
		})
	}
}

// A dedicated slot with none of its label's work ready takes the ordinary
// order rather than sitting idle, and says so.
func TestADedicatedSlotWithNoLabelWorkReadyTakesTheOrdinaryOrder(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-plain-2", 2),
	)
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"team-development"}, Dedicated: true}, {}}
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning}

	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := strings.Join(harness.pullOrder(), ","); got != "yoyodyne-plain-1" {
		t.Fatalf("pull order = %v, want the top of the ordinary order: %s", got, schedule.Render())
	}
	if reason := harness.selectionFor("yoyodyne-plain-1").Reason; !strings.Contains(reason, "pulled into developer slot 1, which is kept for the team-development label and found none of that work ready, so it took the ordinary order") {
		t.Errorf("reason = %q, want it to say the dedicated slot took the ordinary order", reason)
	}
}

// Marking no slot dedicated leaves every pull exactly as it was: the replay of
// a preferring slot over priority-0 work is unchanged by an explicit false.
func TestNoSlotDedicatedTakesPriorityZeroAsBefore(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		labelled("yoyodyne-team", 1, "team-development"),
		labelled("yoyodyne-urgent", 0),
	)
	harness.capacity = 1
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"team-development"}, Dedicated: false}}

	if _, err := (Scheduler{Open: harness.open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := harness.pullOrder(); strings.Join(got, ",") != "yoyodyne-urgent,yoyodyne-team" {
		t.Fatalf("pull order = %v, want the priority-0 item first as before", got)
	}
}
