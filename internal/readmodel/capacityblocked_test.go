package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The moment the fixtures below are read at: the reset the parked run is
// waiting out is still an hour off, and the conversation's refusal still
// stands.
var capacityReadAt = time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)

func TestCapacityPreservationFollowsTheRepositoryRatherThanRemovalFlags(t *testing.T) {
	t.Parallel()
	for _, there := range []bool{true, false} {
		run := blockedRun("run-1111aaaa", "yoyodyne-ifd.150")
		run.BranchRemoved, run.WorktreeRemoved = there, there
		look := func(run runstate.State) triage.Found {
			return triage.Found{Branch: run.Branch, WorktreePath: run.WorktreePath, BranchThere: there}
		}
		blocked := ReadCapacityBlocked([]runstate.State{run}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{}, look)
		if len(blocked.Runs) != 1 || blocked.Runs[0].Preserved != there {
			t.Fatalf("capacity-blocked runs = %#v, want preserved %t", blocked.Runs, there)
		}
	}
}

// parkedRun is a run asleep on a reset the provider named, as the pipeline
// leaves one: in flight, the deadline durable, the cause beside it, and the
// probe it is sleeping recorded as the last time the record moved.
func parkedRun(runID, workItemID string) runstate.State {
	resetsAt := capacityReadAt.Add(time.Hour)
	return runstate.State{
		RunID:                   runID,
		WorkItemID:              workItemID,
		Status:                  runstate.StatusRunning,
		Phase:                   runstate.PhaseDeveloping,
		StartedAt:               capacityReadAt.Add(-3 * time.Hour),
		UpdatedAt:               capacityReadAt.Add(-20 * time.Minute),
		Branch:                  "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath:            "/state/worktrees/" + runID,
		BaseCommit:              strings.Repeat("a", 40),
		UsageLimitResetsAt:      &resetsAt,
		UsageLimitKind:          "five_hour",
		UsageLimitPausedSeconds: 5400,
		PauseCause:              runstate.PauseUsageLimit,
	}
}

// blockedRun is a run the provider refused and the harness would not wait for,
// as the pipeline leaves one: terminal, the blocker on the item, the cause
// kept by the run's ending, and no deadline because none was ever taken. The
// orchestrator's TestAUsageLimitStopIsListedAsCapacityBlocked reads the record
// the pipeline itself writes for such a run, so this shape is checked against
// what is actually on disk rather than only assumed here.
func blockedRun(runID, workItemID string) runstate.State {
	stopped := capacityReadAt.Add(-2 * time.Hour)
	return runstate.State{
		RunID:                   runID,
		WorkItemID:              workItemID,
		Status:                  runstate.StatusFailed,
		Phase:                   runstate.PhaseReviewing,
		StartedAt:               stopped.Add(-4 * time.Hour),
		UpdatedAt:               stopped,
		CompletedAt:             &stopped,
		Branch:                  "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath:            "/state/worktrees/" + runID,
		UsageLimitKind:          "seven_day",
		UsageLimitPausedSeconds: 21600,
		PauseCause:              runstate.PauseUsageLimit,
		Blocker:                 "Yoyodyne stopped this item: the provider refused it in a way this run could not wait out.",
		Failure:                 "this run was refused by an exhausted seven_day usage limit and cannot wait for it",
	}
}

// conversationRefusal is one turn of a conversation the provider stopped, as
// the conversation records it: what was stopped, the limit, the reset, and the
// conversation it belongs to.
func conversationRefusal(at time.Time, conversationID string, resetsAt *time.Time) runstate.UsageLimitExhaustion {
	stopped := refusal(at, "opus", resetsAt)
	stopped.Waiting = "the product manager conversation " + conversationID
	stopped.ConversationID = conversationID
	stopped.Kind = "five_hour"
	return stopped
}

// The shape is a contract: a script and the capacity panel read these keys, so
// they are pinned as text against the three fixtures the work item names — one
// run parked on a reset, one conversation the provider is refusing, and
// nothing at all — rather than asserted field by field, where a renamed key
// would pass.
func TestTheCapacityBlockedStateHasOneShape(t *testing.T) {
	t.Parallel()

	resets := capacityReadAt.Add(45 * time.Minute)
	blocked := ReadCapacityBlocked(
		[]runstate.State{parkedRun("run-0a1b2c3d", "yoyodyne-ifd.140")},
		[]runstate.UsageLimitExhaustion{
			conversationRefusal(capacityReadAt.Add(-40*time.Minute), "chat-91253e0e070c17b0663651cc48602122", &resets),
		},
		capacityReadAt, 30*time.Minute, CapacityEvidence{},
	)
	encoded, err := json.MarshalIndent(blocked, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	want := `{
  "runs": [
    {
      "run_id": "run-0a1b2c3d",
      "work_item_id": "yoyodyne-ifd.140",
      "phase": "developing",
      "state": "waiting",
      "refused_by": "an exhausted five_hour usage limit",
      "since": "2026-09-18T09:40:00Z",
      "resets_at": "2026-09-18T11:00:00Z",
      "waited_seconds": 5400,
      "preserved": true,
      "remedy": "nothing needs doing: the run asks the provider again by itself at its next probe and carries on once it is served; ` + "`yoyo resume`" + ` with the work item named asks now instead of at the probe"
    }
  ],
  "conversations": [
    {
      "conversation_id": "chat-91253e0e070c17b0663651cc48602122",
      "state": "capacity-blocked",
      "waiting": "the product manager conversation chat-91253e0e070c17b0663651cc48602122",
      "refused_by": "an exhausted five_hour usage limit",
      "model": "opus",
      "since": "2026-09-18T09:20:00Z",
      "resets_at": "2026-09-18T10:45:00Z",
      "refusals": 1,
      "remedy": "nothing waits on it: the turn failed where it was asked for, and asking again once the window lifts is what serves it; enabling failover on the agent is what would move the next turn onto another model before then"
    }
  ]
}`
	if string(encoded) != want {
		t.Fatalf("capacity-blocked state encodes as\n%s\nwant\n%s", encoded, want)
	}

	// Nothing held is two empty lists, never two absent ones: a script has to
	// tell "nothing is held" from "the key was left out".
	none, err := json.Marshal(ReadCapacityBlocked(nil, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{}))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(none) != `{"runs":[],"conversations":[]}` {
		t.Fatalf("nothing held encodes as %s", none)
	}
}

// A run past its wait budget stops with a blocker like every other stoppage,
// and this is what makes it a queryable capacity state rather than a generic
// one: the cause it still records, the moment it stopped, and what survives.
func TestARunTheHarnessWouldNotWaitForIsCapacityBlocked(t *testing.T) {
	t.Parallel()

	blocked := ReadCapacityBlocked([]runstate.State{blockedRun("run-9f8e7d6c", "yoyodyne-ifd.141")}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{})
	if len(blocked.Runs) != 1 {
		t.Fatalf("runs = %+v, want the stopped run", blocked.Runs)
	}
	run := blocked.Runs[0]
	if run.State != CapacityStateBlocked {
		t.Fatalf("state = %q, want %q", run.State, CapacityStateBlocked)
	}
	if run.ResetsAt != nil {
		t.Fatalf("resets at = %s, want none: the run took no wait", run.ResetsAt)
	}
	if !run.Since.Equal(capacityReadAt.Add(-2 * time.Hour)) {
		t.Fatalf("since = %s, want the moment it stopped", run.Since)
	}
	if run.WaitedSeconds != 21600 || !run.Preserved || run.RefusedBy != "an exhausted seven_day usage limit" {
		t.Fatalf("run = %+v, want the budget it spent, its change preserved, and the limit named", run)
	}
	if !strings.Contains(run.Remedy, "usage_limit_max_pause") || !strings.Contains(run.Remedy, "development manager") {
		t.Fatalf("remedy = %q, want the configured maximum and whose hands the item is in", run.Remedy)
	}
}

// A run the provider's usage window stopped is capacity-blocked too, read from
// the refusal it ended on, because its ending cleared the pause the reading
// above keys on. What it says is the reset and that nothing needs doing: the
// item went back to the queue rather than to a person.
func TestARunTheUsageWindowStoppedIsCapacityBlockedUntilItsReset(t *testing.T) {
	t.Parallel()

	stopped := capacityReadAt.Add(-2 * time.Hour)
	resetsAt := capacityReadAt.Add(89 * time.Hour)
	run := runstate.State{
		RunID:          "run-7a6b5c4d",
		WorkItemID:     "yoyodyne-ifd.428.12",
		Status:         runstate.StatusCancelled,
		Phase:          runstate.PhaseDeveloping,
		StartedAt:      stopped.Add(-time.Minute),
		UpdatedAt:      stopped,
		CompletedAt:    &stopped,
		Branch:         "yoyodyne/yoyodyne-ifd-428-12/run-7a6b5c4d",
		WorktreePath:   "/state/worktrees/run-7a6b5c4d",
		UsageLimitKind: "seven_day",
		Failure:        "this run was refused by an exhausted seven_day usage limit and the harness will not wait for it",
		Environmental: &runstate.EnvironmentalRefusal{
			Cause: runstate.CauseUsageWindow, RecordedAt: stopped, ResetsAt: &resetsAt, Settled: true, Refused: true,
		},
	}
	blocked := ReadCapacityBlocked([]runstate.State{run}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{})
	if len(blocked.Runs) != 1 {
		t.Fatalf("runs = %+v, want the stopped run", blocked.Runs)
	}
	entry := blocked.Runs[0]
	if entry.State != CapacityStateBlocked || entry.RefusedBy != "an exhausted seven_day usage limit" {
		t.Fatalf("run = %+v, want it capacity-blocked on the seven_day limit", entry)
	}
	if entry.ResetsAt == nil || !entry.ResetsAt.Equal(resetsAt) || !entry.Since.Equal(stopped) || !entry.Preserved {
		t.Fatalf("run = %+v, want the reset, the moment it stopped, and its change preserved", entry)
	}
	if !strings.HasPrefix(entry.Remedy, "nothing needs doing") || strings.Contains(entry.Remedy, "development manager") {
		t.Fatalf("remedy = %q, want it to say the item went back to the queue rather than to a person", entry.Remedy)
	}
}

// A run waiting out a transient overload is parked on the provider's capacity
// as much as one waiting out a limit, on a shorter clock; a run waiting on a
// login nobody renewed shares the deadline field and is not capacity at all.
func TestAnOverloadIsCapacityAndAnOutageIsNot(t *testing.T) {
	t.Parallel()

	overloaded := parkedRun("run-1111aaaa", "yoyodyne-ifd.150")
	overloaded.PauseCause = runstate.PauseServerOverload
	overloaded.UsageLimitKind = ""
	loggedOut := parkedRun("run-2222bbbb", "yoyodyne-ifd.151")
	loggedOut.PauseCause = runstate.PauseProviderUnauthenticated
	held := parkedRun("run-3333cccc", "yoyodyne-ifd.152")
	held.PauseCause = runstate.PauseOperatorHold

	blocked := ReadCapacityBlocked([]runstate.State{overloaded, loggedOut, held}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{})
	if len(blocked.Runs) != 1 || blocked.Runs[0].RunID != "run-1111aaaa" {
		t.Fatalf("runs = %+v, want only the overloaded run", blocked.Runs)
	}
	if blocked.Runs[0].RefusedBy != "a transient provider server overload" {
		t.Fatalf("refused by %q, want the overload named", blocked.Runs[0].RefusedBy)
	}
}

// A run that resumed has its cause cleared with its deadline, so a run that
// paused once and later stopped on a review is the review's stoppage and not
// this one's; and a cancelled run mid-wait ended on nothing anybody has to
// decide, so it is not held on capacity either. A record written before the
// cause was carried reads as a usage limit, as it does everywhere.
func TestOnlyARunStillRecordingACapacityCauseIsListed(t *testing.T) {
	t.Parallel()

	resumedThenStopped := blockedRun("run-4444dddd", "yoyodyne-ifd.160")
	resumedThenStopped.PauseCause = ""
	resumedThenStopped.UsageLimitKind = ""
	resumedThenStopped.UsageLimitPausedSeconds = 0
	resumedThenStopped.Blocker = "Yoyodyne stopped this item: independent review requires repair"
	cancelledMidWait := parkedRun("run-5555eeee", "yoyodyne-ifd.161")
	cancelledMidWait.Status = runstate.StatusCancelled
	cancelledMidWait.CompletedAt = &capacityReadAt
	vintage := parkedRun("run-6666ffff", "yoyodyne-ifd.162")
	vintage.PauseCause = ""

	blocked := ReadCapacityBlocked([]runstate.State{resumedThenStopped, cancelledMidWait, vintage}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{})
	if len(blocked.Runs) != 1 || blocked.Runs[0].RunID != "run-6666ffff" {
		t.Fatalf("runs = %+v, want only the vintage parked run: a review stoppage with its cause cleared and a cancelled wait are not held on capacity", blocked.Runs)
	}
	if blocked.Runs[0].RefusedBy != "an exhausted five_hour usage limit" {
		t.Fatalf("refused by %q, want the empty cause read as a usage limit beside its deadline", blocked.Runs[0].RefusedBy)
	}
}

// One run per item, the latest: a run a later run superseded is history
// whatever it stopped on, and an item whose latest run is not on capacity is
// not held on it.
func TestALaterRunOfTheSameItemSupersedesAParkedOne(t *testing.T) {
	t.Parallel()

	earlier := blockedRun("run-7777aaaa", "yoyodyne-ifd.170")
	later := runstate.State{
		RunID:      "run-8888bbbb",
		WorkItemID: "yoyodyne-ifd.170",
		Status:     runstate.StatusRunning,
		Phase:      runstate.PhaseDeveloping,
		StartedAt:  capacityReadAt.Add(-time.Hour),
		UpdatedAt:  capacityReadAt.Add(-time.Minute),
	}
	if blocked := ReadCapacityBlocked([]runstate.State{later, earlier}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{}); len(blocked.Runs) != 0 {
		t.Fatalf("runs = %+v, want nothing: the item's latest run is not on capacity", blocked.Runs)
	}
}

// A conversation is held while a refusal of it stands, and one entry says so
// however many turns were stopped: the earliest is when it began, the latest
// reset is when it lifts, and a turn an alternate served through is not a
// refusal of the conversation at all.
func TestAConversationIsOneEntryHoweverManyTurnsWereRefused(t *testing.T) {
	t.Parallel()

	first := capacityReadAt.Add(-3 * time.Hour)
	firstReset := capacityReadAt.Add(30 * time.Minute)
	secondReset := capacityReadAt.Add(2 * time.Hour)
	served := substitution(capacityReadAt.Add(-time.Hour), "opus", "sonnet", &secondReset)
	served.ConversationID = "chat-served"
	lifted := capacityReadAt.Add(-time.Minute)
	blocked := ReadCapacityBlocked(nil, []runstate.UsageLimitExhaustion{
		conversationRefusal(first, "chat-held", &firstReset),
		conversationRefusal(capacityReadAt.Add(-time.Hour), "chat-held", &secondReset),
		served,
		// A refusal whose reset has passed holds nothing any more.
		conversationRefusal(capacityReadAt.Add(-2*time.Hour), "chat-lifted", &lifted),
		// A refusal with no conversation — a branch review at a terminal — is not
		// a conversation.
		refusal(capacityReadAt.Add(-time.Minute), "opus", &secondReset),
	}, capacityReadAt, 30*time.Minute, CapacityEvidence{})

	if len(blocked.Conversations) != 1 {
		t.Fatalf("conversations = %+v, want only the held one", blocked.Conversations)
	}
	held := blocked.Conversations[0]
	if held.ConversationID != "chat-held" || held.Refusals != 2 {
		t.Fatalf("held = %+v, want chat-held with both refusals counted", held)
	}
	if !held.Since.Equal(first) || held.ResetsAt == nil || !held.ResetsAt.Equal(secondReset) {
		t.Fatalf("held = %+v, want since the first refusal and until the latest reset", held)
	}
}

// A refusal that named no reset stands for the probe interval, and a
// conversation held on one says it has no reset rather than inventing one.
func TestAnUntimedConversationRefusalStandsForTheProbeInterval(t *testing.T) {
	t.Parallel()

	log := []runstate.UsageLimitExhaustion{conversationRefusal(capacityReadAt.Add(-10*time.Minute), "chat-untimed", nil)}
	blocked := ReadCapacityBlocked(nil, log, capacityReadAt, 30*time.Minute, CapacityEvidence{})
	if len(blocked.Conversations) != 1 || blocked.Conversations[0].ResetsAt != nil {
		t.Fatalf("conversations = %+v, want the conversation held with no reset named", blocked.Conversations)
	}
	if blocked := ReadCapacityBlocked(nil, log, capacityReadAt.Add(25*time.Minute), 30*time.Minute, CapacityEvidence{}); len(blocked.Conversations) != 0 {
		t.Fatalf("conversations = %+v, want nothing once the probe interval has passed", blocked.Conversations)
	}
}

// The standing status carries the state whole and always, and each half says
// so where its records could not be read rather than reporting an empty list:
// "nothing is held" and "nothing could be read" are opposite answers.
func TestTheStandingCarriesTheCapacityBlockedStateAndNamesWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{parkedRun("run-0a1b2c3d", "yoyodyne-ifd.140")},
		recorded:   []runstate.State{parkedRun("run-0a1b2c3d", "yoyodyne-ifd.140")},
		prices:     map[string]runstate.ItemPrice{},
	}
	sources.UsageLimits = fakeUsageLimits{fail: errors.New("usage-limits.jsonl: permission denied")}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.CapacityBlocked.Runs) != 1 || standing.CapacityBlocked.Runs[0].State != CapacityStateWaiting {
		t.Fatalf("capacity blocked = %+v, want the parked run", standing.CapacityBlocked)
	}
	if standing.CapacityBlocked.Conversations == nil || !strings.Contains(standing.CapacityBlocked.ConversationsProblem, "permission denied") {
		t.Fatalf("capacity blocked = %+v, want an empty list and the log's failure named", standing.CapacityBlocked)
	}
	// The parked run is still on the running line: this says it is asleep, and
	// takes nothing off the four lines.
	if len(standing.Running) != 1 {
		t.Fatalf("running = %+v, want the parked run still counted", standing.Running)
	}

	unwired := quietSources()
	unwired.Runs = nil
	blocked := ReadStanding(context.Background(), unwired).CapacityBlocked
	if blocked.Runs == nil || !strings.Contains(blocked.RunsProblem, "nothing was wired") ||
		blocked.Conversations == nil || !strings.Contains(blocked.ConversationsProblem, "nothing was wired") {
		t.Fatalf("capacity blocked = %+v, want both halves saying nothing was wired to read them", blocked)
	}
}
