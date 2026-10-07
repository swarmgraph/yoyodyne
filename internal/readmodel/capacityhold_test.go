package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The five days this reading exists for, as the record actually holds them:
// five agents on opus, no alternates, and refusals that name no model, each
// carrying the seven-day reset.
var (
	holdOpened = time.Date(2026, 9, 8, 7, 38, 40, 0, time.UTC)
	holdResets = time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
)

func fiveAgentsOnOpus() []AgentEndpoint {
	return []AgentEndpoint{
		{Name: "architect", Provider: "claude-code", Model: "opus"},
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "development-manager", Provider: "claude-code", Model: "opus"},
		{Name: "product-manager", Provider: "claude-code", Model: "opus"},
		{Name: "reviewer", Provider: "claude-code", Model: "opus"},
	}
}

// refusal is one turn the provider stopped, as the log recorded it. An empty
// model is what every entry before this change carried.
func refusal(at time.Time, model string, resetsAt *time.Time) runstate.UsageLimitExhaustion {
	return runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            at,
		Waiting:       "the development manager conversation chat-419cedb4a013b063f477e322a2a60466",
		Kind:          "seven_day",
		ResetsAt:      resetsAt,
		Model:         model,
	}
}

// substitution is a refusal something served through: the named model was
// refused and the alternate took the turn, so nothing stopped.
func substitution(at time.Time, model, servedBy string, resetsAt *time.Time) runstate.UsageLimitExhaustion {
	served := refusal(at, model, resetsAt)
	served.ServedBy = servedBy
	served.Substitution = runstate.SubstitutedForCapacity
	return served
}

func septemberLog(count int) []runstate.UsageLimitExhaustion {
	resets := holdResets
	log := make([]runstate.UsageLimitExhaustion, 0, count)
	for index := 0; index < count; index++ {
		log = append(log, refusal(holdOpened.Add(time.Duration(index)*time.Hour), "", &resets))
	}
	return log
}

type fakeUsageLimits struct {
	refusals []runstate.UsageLimitExhaustion
	fail     error
}

func (f fakeUsageLimits) List() ([]runstate.UsageLimitExhaustion, error) { return f.refusals, f.fail }

// The 2026-09-08 shape, replayed: every agent held, the reset named, and the
// count of what the provider stopped.
func TestTheSeptemberStoppageReadsAsAHoldOverEveryRole(t *testing.T) {
	t.Parallel()

	now := holdOpened.Add(26 * time.Hour)
	hold := ReadCapacityHold(fiveAgentsOnOpus(), nil, septemberLog(20), now, 30*time.Minute, CapacityEvidence{})
	if !hold.Holding {
		t.Fatal("twenty refusals on a reset five days off, over five agents on one model, read as no hold")
	}
	if !hold.Since.Equal(holdOpened) {
		t.Fatalf("since = %s, want the first refusal %s", hold.Since, holdOpened)
	}
	if !hold.ResetsAt.Equal(holdResets) {
		t.Fatalf("resets at = %s, want the provider's %s", hold.ResetsAt, holdResets)
	}
	if hold.Refusals != 20 || hold.Kind != "seven_day" || len(hold.Agents) != 5 {
		t.Fatalf("hold = %+v, want every refusal counted, the limit named, and every agent held", hold)
	}
	said := hold.Says()
	for _, want := range []string{
		"Every role is paused on the provider's usage window until 2026-09-13T03:00:00Z",
		"all 5 agents run on opus and none names an alternate, so nothing fails over",
		"20 turns refused since 2026-09-08T07:38:40Z",
	} {
		if !strings.Contains(said, want) {
			t.Fatalf("says %q, want %q in it", said, want)
		}
	}
	if !strings.Contains(hold.Whose(), "failover") {
		t.Fatalf("whose = %q, want the remedy named", hold.Whose())
	}
	if mark := hold.Mark(); mark != "capacity:2026-09-13T03:00:00Z" {
		t.Fatalf("mark = %q, want the reset the provider named", mark)
	}
}

// A hold ends when the provider said it would. The moment the reset passes,
// the same log accounts for nothing.
func TestAHoldLiftsAtTheReset(t *testing.T) {
	t.Parallel()

	if hold := ReadCapacityHold(fiveAgentsOnOpus(), nil, septemberLog(20), holdResets.Add(time.Second), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing once the reset has passed", hold)
	}
}

// A refusal that named no reset stands for the configured probe interval and no
// longer, which is the same reading failover takes of the same log.
func TestAnUntimedRefusalStandsForTheProbeInterval(t *testing.T) {
	t.Parallel()

	log := []runstate.UsageLimitExhaustion{refusal(holdOpened, "", nil)}
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), nil, log, holdOpened.Add(10*time.Minute), 30*time.Minute, CapacityEvidence{}); !hold.Holding {
		t.Fatal("an untimed refusal ten minutes old reads as no hold inside a thirty-minute interval")
	} else if !hold.ResetsAt.IsZero() || !strings.Contains(hold.Says(), "named no time it lifts") {
		t.Fatalf("hold = %+v says %q, want the absence of a reset stated rather than invented", hold, hold.Says())
	}
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), nil, log, holdOpened.Add(31*time.Minute), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing once the interval has passed", hold)
	}
}

// Failover working is the opposite of a hold. A window that closed and was
// served through by an alternate stopped nothing, however many times it was.
func TestASubstitutionThatServedIsNotAHold(t *testing.T) {
	t.Parallel()

	agents := fiveAgentsOnOpus()
	for index := range agents {
		agents[index].Alternate, agents[index].AlternateProvider = "sonnet", "claude-code"
	}
	resets := holdResets
	log := []runstate.UsageLimitExhaustion{
		substitution(holdOpened, "opus", "sonnet", &resets),
		substitution(holdOpened.Add(time.Hour), "opus", "sonnet", &resets),
	}
	if hold := ReadCapacityHold(agents, nil, log, holdOpened.Add(2*time.Hour), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing while the alternate is serving every turn", hold)
	}
}

// The alternate refused too is the hold again, and the sentence says so: what
// the agents fail over to is named, and both are refused.
func TestTheAlternateRefusedTooIsAHoldThatNamesBoth(t *testing.T) {
	t.Parallel()

	agents := fiveAgentsOnOpus()
	for index := range agents {
		agents[index].Alternate, agents[index].AlternateProvider = "sonnet", "claude-code"
	}
	resets := holdResets
	log := []runstate.UsageLimitExhaustion{
		substitution(holdOpened, "opus", "sonnet", &resets),
		// The turn that found sonnet closed as well, recorded against the model it
		// was actually refused on.
		refusal(holdOpened.Add(time.Hour), "sonnet", &resets),
	}
	hold := ReadCapacityHold(agents, nil, log, holdOpened.Add(2*time.Hour), 30*time.Minute, CapacityEvidence{})
	if !hold.Holding {
		t.Fatal("every agent's alternate refused reads as no hold")
	}
	if hold.Refusals != 1 {
		t.Fatalf("refusals = %d, want only the turn that stopped counted, not the one that was served", hold.Refusals)
	}
	if said := hold.Says(); !strings.Contains(said, "run on opus and fail over to sonnet, and the provider is refusing both") {
		t.Fatalf("says %q, want both models named as refused", said)
	}
}

// One agent still being served is a machine whose work is moving, whatever the
// others are waiting on. That is the stall alarm's business, not this one's.
func TestOneAgentStillServedIsNotAHold(t *testing.T) {
	t.Parallel()

	agents := []AgentEndpoint{
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "reviewer", Provider: "claude-code", Model: "sonnet"},
	}
	resets := holdResets
	log := []runstate.UsageLimitExhaustion{refusal(holdOpened, "opus", &resets)}
	if hold := ReadCapacityHold(agents, nil, log, holdOpened.Add(time.Hour), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing while the reviewer's model is unrefused", hold)
	}
}

// A refusal that names no model can only be attributed where every agent asks
// for the same thing. Where they differ it names nobody, because a hold invented
// over it would send somebody to look at roles that are being served.
func TestAnUnnamedRefusalHoldsNobodyWhereTheAgentsDiffer(t *testing.T) {
	t.Parallel()

	agents := []AgentEndpoint{
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "reviewer", Provider: "claude-code", Model: "sonnet"},
	}
	resets := holdResets
	log := []runstate.UsageLimitExhaustion{refusal(holdOpened, "", &resets)}
	if hold := ReadCapacityHold(agents, nil, log, holdOpened.Add(time.Hour), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing from a refusal nobody can attribute", hold)
	}
}

// An unnamed refusal is a refusal of the model the shared chain asks for first,
// never of the alternate behind it: the alternate is asked only once the model
// has refused, and a record that names no model names no alternate either. So
// a project that enabled failover after such refusals were written — which is
// this project's situation — reads them as holding nobody while the alternate
// is being served, rather than as the provider refusing both.
func TestAnUnnamedRefusalNeverHoldsAnAgentOnItsAlternate(t *testing.T) {
	t.Parallel()

	agents := fiveAgentsOnOpus()
	for index := range agents {
		agents[index].Alternate, agents[index].AlternateProvider = "sonnet", "claude-code"
	}
	now := holdOpened.Add(26 * time.Hour)
	if hold := ReadCapacityHold(agents, nil, septemberLog(20), now, 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing from unnamed refusals over agents whose alternate nothing has refused", hold)
	}
	// The same log with the alternate refused by name is the hold again.
	resets := holdResets
	log := append(septemberLog(20), refusal(holdOpened.Add(21*time.Hour), "sonnet", &resets))
	if hold := ReadCapacityHold(agents, nil, log, now, 30*time.Minute, CapacityEvidence{}); !hold.Holding {
		t.Fatal("unnamed refusals of opus beside a named refusal of sonnet read as no hold")
	}
}

// A pinned version the provider has not got is not a window, and a reading that
// took it for one would hold every role over a model nobody is waiting for.
func TestAnAvailabilitySubstitutionIsNotARefusal(t *testing.T) {
	t.Parallel()

	log := []runstate.UsageLimitExhaustion{{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            holdOpened,
		Waiting:       "the architect conversation chat-1",
		Model:         "opus",
		ServedBy:      "opus-4-1",
		Substitution:  runstate.SubstitutedForAvailability,
	}}
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), nil, log, holdOpened.Add(time.Minute), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing from a version the provider has not got", hold)
	}
}

// runParkedOnTheLimit is one developer run asleep on the September reset, as the
// pipeline leaves it: in flight, the deadline the provider's own, the pause's
// start beside it, the model it was refused on recorded, and the probe it is
// sleeping recorded as the last time the record moved.
func runParkedOnTheLimit(runID, workItemID string, since time.Time) runstate.State {
	resets := holdResets
	return runstate.State{
		RunID:                   runID,
		ProductID:               "yoyodyne",
		WorkItemID:              workItemID,
		Status:                  runstate.StatusRunning,
		Phase:                   runstate.PhaseDeveloping,
		StartedAt:               since.Add(-time.Hour),
		UpdatedAt:               since.Add(20 * time.Hour),
		ProviderModel:           "opus",
		UsageLimitResetsAt:      &resets,
		UsageLimitPausedSince:   &since,
		UsageLimitKind:          "seven_day",
		UsageLimitModel:         "opus",
		UsageLimitPausedSeconds: 72000,
		PauseCause:              runstate.PauseUsageLimit,
	}
}

// The case the reviewer on 355 named: a product with no recurring task and no
// open conversation, whose only refusals are runs parking. The log is empty and
// the hold stands all the same, read from the runs' own records, naming the
// reset they are parked on and when the first of them parked.
func TestRunsParkedOnTheLimitAreAHoldWithNoConversationRefused(t *testing.T) {
	t.Parallel()

	runs := []runstate.State{
		runParkedOnTheLimit("run-0000000000000000000000000000000a", "yoyodyne-ifd.140", holdOpened.Add(time.Hour)),
		runParkedOnTheLimit("run-0000000000000000000000000000000b", "yoyodyne-ifd.141", holdOpened),
	}
	now := holdOpened.Add(26 * time.Hour)
	hold := ReadCapacityHold(fiveAgentsOnOpus(), runs, nil, now, 30*time.Minute, CapacityEvidence{})
	if !hold.Holding {
		t.Fatal("two runs parked on a reset five days off, over five agents on one model and no refusal in the log, read as no hold")
	}
	if !hold.Since.Equal(holdOpened) {
		t.Fatalf("since = %s, want the earliest park %s rather than the latest probe", hold.Since, holdOpened)
	}
	if !hold.ResetsAt.Equal(holdResets) {
		t.Fatalf("resets at = %s, want the reset the runs are parked on, %s", hold.ResetsAt, holdResets)
	}
	if hold.ParkedRuns != 2 || hold.Refusals != 0 || hold.Kind != "seven_day" || len(hold.Agents) != 5 {
		t.Fatalf("hold = %+v, want both runs counted as parked, no turn refused, the limit named, and every agent held", hold)
	}
	said := hold.Says()
	for _, want := range []string{
		"Every role is paused on the provider's usage window until 2026-09-13T03:00:00Z",
		"all 5 agents run on opus and none names an alternate, so nothing fails over",
		"2 runs parked since 2026-09-08T07:38:40Z",
	} {
		if !strings.Contains(said, want) {
			t.Fatalf("says %q, want %q in it", said, want)
		}
	}
	// Marked by the reset, so the probes the runs make while they stand do not
	// make it a fresh hold each time.
	if mark := hold.Mark(); mark != "capacity:2026-09-13T03:00:00Z" {
		t.Fatalf("mark = %q, want the reset the runs are parked on", mark)
	}
	// And it lifts when they would: at the reset, the same runs account for
	// nothing.
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), runs, nil, holdResets.Add(time.Second), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing once the reset the runs are parked on has passed", hold)
	}
}

// Both records at once are one hold: the runs and the turns are each counted as
// what they are, and the hold began with whichever of them came first.
func TestParkedRunsAndRefusedTurnsAreOneHold(t *testing.T) {
	t.Parallel()

	runs := []runstate.State{runParkedOnTheLimit("run-0000000000000000000000000000000a", "yoyodyne-ifd.140", holdOpened.Add(-time.Hour))}
	hold := ReadCapacityHold(fiveAgentsOnOpus(), runs, septemberLog(20), holdOpened.Add(26*time.Hour), 30*time.Minute, CapacityEvidence{})
	if !hold.Holding || hold.ParkedRuns != 1 || hold.Refusals != 20 {
		t.Fatalf("hold = %+v, want the run and the twenty turns each counted", hold)
	}
	if !hold.Since.Equal(holdOpened.Add(-time.Hour)) {
		t.Fatalf("since = %s, want the run's park, which came before the first refused turn", hold.Since)
	}
	if said := hold.Says(); !strings.Contains(said, "1 run parked and 20 turns refused since 2026-09-08T06:38:40Z") {
		t.Fatalf("says %q, want both records counted in it", said)
	}
}

// A run parked on a limit the provider named no reset for recorded the
// harness's own next probe as its deadline, and the hold must not say that
// probe as the provider's reset: it stands for the probe interval from the
// park, as an untimed refusal in the log does, and says that no time was named.
func TestARunParkedOnAnUntimedLimitStandsForTheProbeInterval(t *testing.T) {
	t.Parallel()

	run := runParkedOnTheLimit("run-0000000000000000000000000000000a", "yoyodyne-ifd.140", holdOpened)
	probe := holdOpened.Add(30 * time.Minute)
	run.UsageLimitResetsAt = &probe
	run.UsageLimitResetUnknown = true
	run.UpdatedAt = holdOpened
	runs := []runstate.State{run}
	hold := ReadCapacityHold(fiveAgentsOnOpus(), runs, nil, holdOpened.Add(10*time.Minute), 30*time.Minute, CapacityEvidence{})
	if !hold.Holding {
		t.Fatal("a run parked ten minutes ago on an untimed limit reads as no hold inside a thirty-minute interval")
	}
	if !hold.ResetsAt.IsZero() || !strings.Contains(hold.Says(), "named no time it lifts") {
		t.Fatalf("hold = %+v says %q, want the probe deadline read as no reset named rather than as the provider's", hold, hold.Says())
	}
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), runs, nil, holdOpened.Add(31*time.Minute), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing once the interval has passed", hold)
	}
}

// A server overload shares the run's deadline field and is not a usage window;
// a run waiting one out is not held by the provider's capacity and must not
// make a hold over it.
func TestARunWaitingOutAnOverloadIsNotAHold(t *testing.T) {
	t.Parallel()

	run := runParkedOnTheLimit("run-0000000000000000000000000000000a", "yoyodyne-ifd.140", holdOpened)
	deadline := holdOpened.Add(90 * time.Second)
	run.UsageLimitResetsAt = &deadline
	run.UsageLimitResetUnknown = true
	run.UsageLimitKind, run.UsageLimitModel = "", ""
	run.PauseCause = runstate.PauseServerOverload
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), []runstate.State{run}, nil, holdOpened.Add(time.Minute), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing from a run waiting out an overload", hold)
	}
}

// A record written before the park carried its model and its start is read
// from what it does carry: the developer's model is on the record already, and
// the probe being slept stands in for the start. A refused review recorded no
// model at all, so on a project whose agents differ it holds nobody.
func TestAParkWrittenBeforeItsModelWasCarriedIsReadFromTheRecordItHas(t *testing.T) {
	t.Parallel()

	older := runParkedOnTheLimit("run-0000000000000000000000000000000a", "yoyodyne-ifd.140", holdOpened)
	older.UsageLimitPausedSince = nil
	older.UsageLimitModel = ""
	hold := ReadCapacityHold(fiveAgentsOnOpus(), []runstate.State{older}, nil, holdOpened.Add(26*time.Hour), 30*time.Minute, CapacityEvidence{})
	if !hold.Holding || !hold.Since.Equal(older.UpdatedAt) {
		t.Fatalf("hold = %+v, want the developer's own model read as refused and the probe as the start", hold)
	}

	agents := []AgentEndpoint{
		{Name: "developer", Provider: "claude-code", Model: "opus"},
		{Name: "reviewer", Provider: "claude-code", Model: "opus"},
		{Name: "product-manager", Provider: "claude-code", Model: "sonnet"},
	}
	review := older
	review.Phase = runstate.PhaseReviewing
	if hold := ReadCapacityHold(agents, []runstate.State{review}, nil, holdOpened.Add(26*time.Hour), 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want nothing from a refused review that recorded no model, over agents that differ", hold)
	}
}

// A run's park is read from the runs alone where nothing was wired to read the
// log, and beside the problem where the log could not be read: the two records
// only add refusals, so a hold the runs show is a hold whatever the log holds,
// and a broken log must not hide a product whose every run is parked.
func TestParkedRunsAreTheHoldWhereTheLogIsMissingOrUnreadable(t *testing.T) {
	t.Parallel()

	runs := []runstate.State{runParkedOnTheLimit("run-0000000000000000000000000000000a", "yoyodyne-ifd.140", holdOpened)}
	sources := quietSources()
	sources.Now = func() time.Time { return holdOpened.Add(26 * time.Hour) }
	sources.Agents = fiveAgentsOnOpus()
	sources.Runs = fakeRuns{incomplete: runs, prices: map[string]runstate.ItemPrice{}}
	sources.UnknownResetPause = 30 * time.Minute

	standing := ReadStanding(context.Background(), sources)
	if standing.CapacityHold == nil || standing.CapacityHold.ParkedRuns != 1 {
		t.Fatalf("standing = %+v, want the hold read from the runs with no log wired", standing.CapacityHold)
	}
	if !strings.HasPrefix(standing.Paused, "Every role is paused on the provider's usage window until 2026-09-13T03:00:00Z") {
		t.Fatalf("paused = %q, want the hold as the banner", standing.Paused)
	}

	sources.UsageLimits = fakeUsageLimits{fail: errors.New("permission denied")}
	standing = ReadStanding(context.Background(), sources)
	if standing.CapacityHold == nil || standing.CapacityHold.ParkedRuns != 1 {
		t.Fatalf("standing = %+v, want the hold the runs show said beside the unreadable log", standing.CapacityHold)
	}
	if !strings.Contains(standing.NeedsHumanProblem, "what the provider has refused could not be read") {
		t.Fatalf("problem = %q, want the unreadable log named all the same", standing.NeedsHumanProblem)
	}
}

// The hold is the banner above the four lines and an entry on the attention
// line, said from the same reading `yoyo status` prints — where the session
// choosing work recorded no window of its own, which is what September looked
// like: the session was idle over items awaiting a decision, and the role that
// would have decided was the one being refused.
func TestAHoldIsTheBannerAndAnAttentionEntry(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Now = func() time.Time { return holdOpened.Add(26 * time.Hour) }
	sources.Agents = fiveAgentsOnOpus()
	sources.UsageLimits = fakeUsageLimits{refusals: septemberLog(20)}
	sources.UnknownResetPause = 30 * time.Minute

	standing := ReadStanding(context.Background(), sources)
	if standing.CapacityHold == nil || !standing.CapacityHold.Holding {
		t.Fatalf("standing = %+v, want the hold carried", standing.CapacityHold)
	}
	if !strings.HasPrefix(standing.Paused, "Every role is paused on the provider's usage window until 2026-09-13T03:00:00Z") {
		t.Fatalf("paused = %q, want the hold as the banner", standing.Paused)
	}
	if !strings.HasPrefix(standing.Render(), standing.Paused) {
		t.Fatalf("render = %q, want the banner first", standing.Render())
	}
	found := false
	for _, attention := range standing.NeedsHuman {
		if strings.Contains(attention.What(), "every role is held by the provider's usage window") &&
			strings.Contains(attention.Whose(), "the operator's") {
			found = true
		}
	}
	if !found {
		t.Fatalf("needs a human = %+v, want the hold as something waiting on the operator", standing.NeedsHuman)
	}
}

// A log that could not be read is said, on the line whose absence would
// otherwise read as nothing being held.
func TestAnUnreadableRefusalLogIsSaidRatherThanReadAsNoHold(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Agents = fiveAgentsOnOpus()
	sources.UsageLimits = fakeUsageLimits{fail: errors.New("permission denied")}

	standing := ReadStanding(context.Background(), sources)
	if standing.CapacityHold != nil || standing.Paused != "" {
		t.Fatalf("standing = %+v, want no hold invented over a log nobody could read", standing)
	}
	if !strings.Contains(standing.NeedsHumanProblem, "what the provider has refused could not be read") {
		t.Fatalf("problem = %q, want the unreadable log named", standing.NeedsHumanProblem)
	}
}

// usageLimitStop is a run blockOnUsageLimit stopped, in the shape the
// pipeline's ending leaves on disk: terminal, a blocker on its item, the stop
// cause usage-pause, the pause cause kept, and no deadline because the run
// refused the wait. The orchestrator's TestAUsageLimitStopIsListedAsCapacityBlocked
// checks this shape against the record the pipeline itself writes.
func usageLimitStop(runID, workItemID string, stopped time.Time) runstate.State {
	return runstate.State{
		RunID:          runID,
		ProductID:      "yoyodyne",
		WorkItemID:     workItemID,
		Status:         runstate.StatusFailed,
		Phase:          runstate.PhaseDeveloping,
		StartedAt:      stopped.Add(-time.Hour),
		UpdatedAt:      stopped,
		CompletedAt:    &stopped,
		Branch:         "yoyodyne/" + workItemID + "/" + runID,
		ProviderModel:  "opus",
		UsageLimitKind: "five_hour",
		PauseCause:     runstate.PauseUsageLimit,
		StopClass:      runstate.StopUsagePause,
		Blocker:        "Yoyodyne stopped this item: the provider refused it in a way this run could not wait out.",
		Failure:        "this run was refused by an exhausted five_hour usage limit and cannot wait for it: it reports resetting at 2026-09-08T07:00:00Z, which is not in the future",
	}
}

// A run stopped on a usage limit is one run the provider refused, not every
// role held: it parked on nothing and waits on no reset, and its item is the
// development manager's to decide. So on the attention line it is the stopped
// item waiting on her, and never the capacity entry, which is the hold over
// every role; the pause cause its record keeps must not make the hold count it
// as a parked run, even with every agent on the model it was refused on. It is
// listed among the runs blocked on capacity, where each refused run is named.
func TestAUsageLimitStopIsListedOnCapacityAndWaitsOnTheDevelopmentManagerRatherThanHoldingEveryRole(t *testing.T) {
	t.Parallel()

	stopped := usageLimitStop("run-0000000000000000000000000000000c", "yoyodyne-ifd.142", moment.Add(-2*time.Hour))
	if refusals := ParkedRunRefusals([]runstate.State{stopped}); len(refusals) != 0 {
		t.Fatalf("parked-run refusals = %+v, want none: a finished run parked on nothing", refusals)
	}
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), []runstate.State{stopped}, nil, moment, 30*time.Minute, CapacityEvidence{}); hold.Holding {
		t.Fatalf("hold = %+v, want no hold over every role from one stopped run", hold)
	}

	sources := quietSources()
	sources.Agents = fiveAgentsOnOpus()
	sources.UsageLimits = fakeUsageLimits{}
	sources.Runs = fakeRuns{recorded: []runstate.State{stopped}, prices: map[string]runstate.ItemPrice{}}
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{
		"blocked": {{ID: "yoyodyne-ifd.142", Title: "stopped on a usage limit", Status: "blocked"}},
	}}}
	sources.Stoppages = fakeStoppages{runs: []runstate.State{stopped}}
	sources.Decisions = recordedDecisions{}
	sources.Remains = &remainsOf{survives: map[string]gitworktree.Survival{stopped.RunID: {BranchExists: true}}}

	standing := ReadStanding(context.Background(), sources)
	if standing.CapacityHold != nil {
		t.Fatalf("capacity hold = %+v, want none", standing.CapacityHold)
	}
	runs := standing.CapacityBlocked.Runs
	if len(runs) != 1 || runs[0].RunID != stopped.RunID || runs[0].State != CapacityStateBlocked ||
		runs[0].RefusedBy != "an exhausted five_hour usage limit" || runs[0].ResetsAt != nil {
		t.Fatalf("capacity-blocked runs = %+v, want the stopped run, blocked on the five_hour limit with no reset", runs)
	}
	var held bool
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionHold && entry.ID == HoldCapacity {
			t.Fatalf("attention line carries the every-role hold %+v for one stopped run", entry)
		}
		if entry.Kind == AttentionHeldWork && entry.Mover == MoverDevelopmentManager {
			held = true
		}
	}
	if !held {
		t.Fatalf("needs human = %+v, want the stopped item waiting on the development manager", standing.NeedsHuman)
	}
}
