package orchestrator

// A program manager instance's pass: a recurring-task firing over one lane,
// woken by the instance's schedule or by what happened in the product.
//
// "Triggers and passes" in docs/designs/program-manager.md is what this builds,
// and it is built on the recurring-task machinery rather than beside it, so
// every rule that file states holds here unchanged: the operator's pause stops
// a pass and the intake hold does not, the claim is taken before the first turn
// is asked, its turns are taken beside the pull and beside other roles'
// firings (recurringfirings.go), a provider answering nobody is
// recorded as the wait rather than as a turn that failed, and every pass ends in
// the same durable record `yoyo sweeps` reads. What is added is three things.
//
// # A pass is one instance's
//
// The role has as many agents as it has lanes, so a pass wakes the instance by
// name rather than the role, and is recorded and paced under the instance's
// name. A turn already in flight on that instance's conversation skips the pass
// rather than queueing it: an operator talking to the instance is already doing
// what the pass would, and the events wait past the cursor for the pass after.
//
// # A burst wakes an instance once
//
// Each instance keeps a position in each event stream it reads — the run
// records, for landings and stoppages, and the tracker, for admissions. An event
// past the position arms one wake. The wake is taken at the next pull once the
// stream has been quiet for the settle window, or at once where the schedule is
// due, whichever comes first; the pass is handed everything between the
// position and the moment it was taken; and the position moves to that moment
// only once the pass has completed. So thirty items admitted in one turn are
// one pass carrying thirty admissions, and a pass that failed leaves the same
// events for the next one rather than losing them.
//
// An entry can appear in its stream after a pass whose window already covers
// the moment it says it happened: the tracker's export is written after the
// tracker's own write. So each stream is read again from runstate.PassLateness
// behind the position, and what a completed pass carried from inside that
// reach is kept on the cursor by key, so the late entry is handed to the next
// pass and nothing is handed twice.
//
// Two bounds keep an armed wake from becoming a turn every pull. It is not
// taken sooner than the recurring-task minimum after the instance's last pass,
// for the reason that minimum exists — every pass is a conversation turn — and
// which also paces the retry of a pass that failed. And a provider answering
// nobody leaves it armed and untaken, since a wake made into the outage asks
// nothing and would be recorded once a pull; the schedule, which the claim
// paces, is what records the wait.
//
// # The model
//
// A pass asks for the agent's own model: an instance's triggers name none, and
// the mechanism a recurring task uses to name one adds no key here.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// PassSettleWindow is how long an instance's streams must have been quiet
// before an armed wake is taken without the schedule being due. It is what
// turns a burst into one pass: a product manager admitting thirty items takes a
// turn, and the thirty are written inside it.
const PassSettleWindow = 2 * time.Minute

// maxPassEventsListed bounds how many events of one stream a pass's message
// lists. The counts beside them are whole; what the bound keeps is the message
// a message rather than a dump of a busy day.
const maxPassEventsListed = 100

// maxPassEventLineBytes bounds one listed event, cut on a rune boundary.
const maxPassEventLineBytes = 240

// PassEvent is one thing that happened in the product that an instance's
// triggers watch for.
type PassEvent struct {
	// Stream is where it was read from: runstate.PassStreamRuns or
	// runstate.PassStreamTracker.
	Stream string
	// Class is which trigger it is: a landing, an admission, or a stoppage.
	Class config.TriggerEvent
	// At is when it happened, as the stream records it, and is what the cursor
	// is compared against.
	At time.Time
	// Key names this event within its stream, so an entry read again inside
	// the reach behind the cursor is known to have been carried already.
	Key string
	// Subject is the work item it is about, and Detail one line saying what
	// happened to it.
	Subject string
	Detail  string
}

// PassEvents reads one stream's events recorded after one moment and at or
// before another. It is satisfied in the CLI over the run store and the
// tracker's export; it never fails over one unreadable entry, and fails over a
// stream that could not be read at all.
type PassEvents interface {
	Events(ctx context.Context, stream string, after, until time.Time) ([]PassEvent, error)
}

// PassCursors is where each instance has read its streams up to. It is
// satisfied by *runstate.PassCursorStore.
type PassCursors interface {
	Load(agent string) (runstate.PassCursor, bool, error)
	Advance(ctx context.Context, agent string, positions map[string]time.Time, carried map[string]map[string]time.Time, now time.Time) (runstate.PassCursor, error)
}

// InstanceConversations says whether a turn is in flight on an instance's
// conversation, without taking it.
type InstanceConversations interface {
	InFlight(agent string) (bool, error)
}

// passStreamOf is the stream a trigger class is read from.
func passStreamOf(class config.TriggerEvent) string {
	if class == config.TriggerAdmissions {
		return runstate.PassStreamTracker
	}
	return runstate.PassStreamRuns
}

// passWake is what an instance's streams hold past its cursor at one pull.
type passWake struct {
	// cursor is where each stream read stood before this pull, so the message
	// can say what the events are after.
	cursor map[string]time.Time
	// events are the events past the cursor, of the classes the instance
	// watches, in the order they happened.
	events []PassEvent
	// positions are where each stream that was read moves to once a pass taken
	// now completes: the moment it was read up to. A stream that could not be
	// read is absent, so its cursor stays where it was.
	positions map[string]time.Time
	// carried is the events, by stream and key, the cursor records once a pass
	// taken now completes.
	carried map[string]map[string]time.Time
	// problems are the streams that could not be read.
	problems []string
}

func (w passWake) armed() bool { return len(w.events) > 0 }

// settled reports streams that have been quiet for the settle window: nothing
// past the cursor happened inside it.
func (w passWake) settled(now time.Time) bool {
	for _, event := range w.events {
		if now.Sub(event.At) < PassSettleWindow {
			return false
		}
	}
	return true
}

// counts is the events by class, which is what the record keeps.
func (w passWake) counts() map[string]int {
	if len(w.events) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, event := range w.events {
		counts[string(event.Class)]++
	}
	return counts
}

// instanceNames lists the instances in a stable order, so which one a pull
// considers first is decided by the name rather than by map iteration.
func (t Trigger) instanceNames() []string {
	names := make([]string, 0, len(t.Instances))
	for name := range t.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// pass considers one instance for a pass at this pull, and claims it where the
// schedule is due or an armed wake has settled. It reports whether a pass was
// claimed, with the turns to take where there are any; an error is something
// that stopped the instance being considered at all, said beside the pull rather
// than recorded as a pass. The due time is when the pass fell due, where the
// caller knows, and it rides the pass's record.
func (t Trigger) pass(ctx context.Context, agent string, instance config.AgentConfig, outage runstate.ProviderOutage, away bool, dueAt time.Time) (claimedFiring, bool, error) {
	triggers := instance.Triggers
	if !triggers.Defined() {
		return claimedFiring{}, false, nil
	}
	// A pass that was claimed and never ended is recorded as missed before the
	// next one is claimed over it.
	unfinished := t.unfinished(ctx, agent)
	now := t.now()
	wake, err := t.readWake(ctx, agent, triggers, now)
	if err != nil {
		return claimedFiring{}, false, errors.Join(unfinished, err)
	}
	readProblem := unfinished
	if len(wake.problems) > 0 {
		readProblem = errors.Join(readProblem, errors.New(strings.Join(wake.problems, "; ")))
	}

	var claimed runstate.SweepClaim
	due := false
	if every := triggers.Every.Duration(); every > 0 {
		claimed, err = t.Claims.Claim(ctx, agent, every, now)
		switch {
		case err == nil:
			due = true
		case errors.Is(err, runstate.ErrSweepNotDue):
		default:
			return claimedFiring{}, false, errors.Join(readProblem, fmt.Errorf("claim the scheduled pass of the program manager instance %s: %w", agent, err))
		}
	}
	if !due {
		if !wake.armed() || !wake.settled(now) || away {
			return claimedFiring{}, false, readProblem
		}
		last, fired, err := t.Claims.Find(agent)
		if err != nil {
			return claimedFiring{}, false, errors.Join(readProblem, fmt.Errorf("read when the program manager instance %s last passed: %w", agent, err))
		}
		if fired && now.Before(last.FiredAt.Add(config.MinRecurringInterval)) {
			return claimedFiring{}, false, readProblem
		}
		claimed, err = t.Claims.Summon(ctx, agent, now)
		if err != nil {
			return claimedFiring{}, false, errors.Join(readProblem, fmt.Errorf("claim the pass of the program manager instance %s: %w", agent, err))
		}
	}

	task := config.RecurringTask{
		Role:    domain.RoleProgramManager,
		Every:   triggers.Every,
		Enabled: true,
	}
	// A pass taken is reported rather than an error, so what stopped the last
	// pass's miss being recorded rides on this one's problem.
	var unrecorded string
	if unfinished != nil {
		unrecorded = unfinished.Error()
	}
	if away {
		fired := t.refuse(ctx, agent, task, outage)
		fired.Problem = appendProblem(fired.Problem, unrecorded)
		return claimedFiring{settled: &fired}, true, nil
	}
	trigger := runstate.PassTriggerSchedule
	if !due {
		trigger = runstate.PassTriggerEvents
	}
	return claimedFiring{take: func(ctx context.Context) Fired {
		fired := t.run(ctx, firing{
			name:    agent,
			pass:    passName(claimed),
			task:    task,
			trigger: trigger,
			message: instanceMessage(agent, instance, due, wake),
			agent:   agent,
			events:  wake.counts(),
			due:     dueAt,
			finish: func(answered bool) string {
				problems := append([]string(nil), wake.problems...)
				problems = append(problems, t.advance(ctx, agent, wake, answered, now))
				return boundedProblem(problems)
			},
		})
		fired.Problem = appendProblem(fired.Problem, unrecorded)
		return fired
	}}, true, nil
}

// readWake reads what each stream the instance watches holds past its cursor.
// An instance seen for the first time, or a stream it has only now begun to
// watch, is positioned at this moment rather than at the start of the stream:
// the first pass is handed what happened since the instance was watched, not
// every run and item the product has ever recorded.
func (t Trigger) readWake(ctx context.Context, agent string, triggers config.Triggers, now time.Time) (passWake, error) {
	wake := passWake{cursor: map[string]time.Time{}, positions: map[string]time.Time{}, carried: map[string]map[string]time.Time{}}
	watched := watchedStreams(triggers)
	if len(watched) == 0 {
		return wake, nil
	}
	if t.Cursors == nil || t.Events == nil {
		wake.problems = append(wake.problems, fmt.Sprintf("the program manager instance %s watches %s, and nothing is wired to read its streams, so only its schedule wakes it", agent, describeClasses(triggers.On)))
		return wake, nil
	}
	cursor, _, err := t.Cursors.Load(agent)
	if err != nil {
		return passWake{}, fmt.Errorf("read where the program manager instance %s has read its streams up to, so no pass over it is taken until it can be: %w", agent, err)
	}
	unpositioned := map[string]time.Time{}
	for _, stream := range runstate.PassStreams {
		if watched[stream] == nil {
			continue
		}
		after, positioned := cursor.Streams[stream]
		if !positioned {
			unpositioned[stream] = now
			continue
		}
		wake.cursor[stream] = after
		events, err := t.Events.Events(ctx, stream, cursor.ReadFrom(stream), now)
		if err != nil {
			wake.problems = append(wake.problems, fmt.Sprintf("the %s stream could not be read for the program manager instance %s, so what it holds waits past the cursor: %v", stream, agent, err))
			continue
		}
		carried := map[string]time.Time{}
		for _, event := range unreadEvents(cursor, stream, after, watched[stream], events) {
			wake.events = append(wake.events, event)
			if event.Key != "" {
				carried[event.Key] = event.At
			}
		}
		wake.positions[stream] = now
		wake.carried[stream] = carried
	}
	if len(unpositioned) > 0 {
		if _, err := t.Cursors.Advance(ctx, agent, unpositioned, nil, now); err != nil {
			return passWake{}, fmt.Errorf("begin watching the streams of the program manager instance %s: %w", agent, err)
		}
	}
	sort.SliceStable(wake.events, func(first, second int) bool {
		return wake.events[first].At.Before(wake.events[second].At)
	})
	return wake, nil
}

// watchedStreams is the classes an instance watches, by the stream each is read
// from.
func watchedStreams(triggers config.Triggers) map[string]map[config.TriggerEvent]bool {
	watched := map[string]map[config.TriggerEvent]bool{}
	for _, class := range triggers.On {
		stream := passStreamOf(class)
		if watched[stream] == nil {
			watched[stream] = map[config.TriggerEvent]bool{}
		}
		watched[stream][class] = true
	}
	return watched
}

// unreadEvents is what of one stream's events a pass taken now would carry:
// the classes watched, past the cursor, and not already carried by a completed
// pass. The stream is read again from inside the reach behind the cursor, so
// what a completed pass carried is not handed again and what arrived late is.
func unreadEvents(cursor runstate.PassCursor, stream string, after time.Time, watched map[config.TriggerEvent]bool, events []PassEvent) []PassEvent {
	var unread []PassEvent
	for _, event := range events {
		if !watched[event.Class] {
			continue
		}
		if event.Key != "" && cursor.WasCarried(stream, event.Key) {
			continue
		}
		if !event.At.After(after) && event.Key == "" {
			continue
		}
		unread = append(unread, event)
	}
	return unread
}

// UnfinishedPassGrace is how long a pass may stand claimed with nothing
// recording its ending, and no turn in flight on the instance's conversation,
// before it is taken to have been cancelled. It is the recurring minimum,
// because no later pass of the instance is claimed sooner than that after the
// last, so the pass that would overwrite the claim always meets it first.
const UnfinishedPassGrace = config.MinRecurringInterval

// unfinished records the instance's last pass as missed where it was claimed
// and nothing ever recorded how it ended: the process carrying it stopped
// mid-pass — a watch session killed under it — and wrote neither its account
// nor its failure. Nothing else would say so: the next pass is claimed over the
// same record and the cadence runs on, which is how the factory-flow
// instance's first pass on 2026-09-26 was lost with nothing saying it.
//
// It needs the instance's conversation to be readable, because a claim with no
// ending is also a pass another process is carrying; this must find no turn in
// flight there. It reports what stopped it recording, and
// never stops the pass being considered.
func (t Trigger) unfinished(ctx context.Context, agent string) error {
	if t.Conversations == nil {
		return nil
	}
	busy, err := t.Conversations.InFlight(agent)
	if err != nil {
		return fmt.Errorf("read whether a turn is in flight on the program manager instance %s's conversation: %w", agent, err)
	}
	if busy {
		return nil
	}
	claimed, found, err := t.Claims.Find(agent)
	if err != nil || !found || claimed.Settled() {
		return nil
	}
	now := t.now()
	if now.Sub(claimed.FiredAt) < UnfinishedPassGrace {
		return nil
	}
	recorded, _, err := t.Reports.List()
	if err != nil {
		return fmt.Errorf("read whether the last pass of the program manager instance %s recorded its ending: %w", agent, err)
	}
	for _, earlier := range recorded {
		if earlier.Task == agent && !earlier.StartedAt.Before(claimed.FiredAt) {
			return nil
		}
	}
	trigger := runstate.PassTriggerSchedule
	if claimed.Summoned {
		trigger = runstate.PassTriggerEvents
	}
	problem := boundedProblem([]string{fmt.Sprintf(
		"the %s of the program manager instance %s, taken at %s, was cancelled before it completed: nothing recorded how it ended, and no turn is in flight on its conversation, so the process carrying it stopped mid-pass — a watch session stopped or killed under it is the usual cause; nothing it would have looked at was looked at, and its events wait past its cursor for the next pass",
		trigger.Describe(), agent, claimed.FiredAt.UTC().Format(time.RFC3339))})
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	var problems []error
	if err := t.Reports.Append(runstate.Sweep{
		Task:      agent,
		Role:      domain.RoleProgramManager,
		StartedAt: claimed.FiredAt,
		EndedAt:   now,
		Problem:   problem,
		Missed:    &runstate.MissedPass{Trigger: trigger, How: runstate.MissCancelled},
	}); err != nil {
		problems = append(problems, fmt.Errorf("record the cancelled pass of the program manager instance %s: %w", agent, err))
	}
	// Settled, so the claim reads as ended and the next pull does not look again.
	if _, err := t.Claims.Settle(write, agent, problem); err != nil {
		problems = append(problems, fmt.Errorf("settle the cancelled pass of the program manager instance %s: %w", agent, err))
	}
	return errors.Join(problems...)
}

// advance moves the instance's cursor once its pass is over: to the moment the
// pass was taken, for every stream that was read, and only where every turn the
// pass asked for was answered. A pass that failed leaves it where it was, so
// the next pass is handed the same events, and the record says so.
func (t Trigger) advance(ctx context.Context, agent string, wake passWake, answered bool, taken time.Time) string {
	if len(wake.positions) == 0 {
		return ""
	}
	if !answered {
		if !wake.armed() {
			return ""
		}
		return fmt.Sprintf("the pass did not complete, so its cursor was not moved and the next pass of %s carries the same %d event(s)", agent, len(wake.events))
	}
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if _, err := t.Cursors.Advance(write, agent, wake.positions, wake.carried, taken); err != nil {
		return fmt.Sprintf("the cursor of %s could not be moved past this pass, so the next pass carries its events again: %v", agent, err)
	}
	return ""
}

// instanceMessage is what the harness says when it wakes an instance for a
// pass: who woke it and why, the standing constraints every recurring turn
// carries, the events since its cursor grouped by stream, and the account
// contract.
func instanceMessage(agent string, instance config.AgentConfig, due bool, wake passWake) string {
	lane := strings.TrimSpace(instance.Lane)
	who := fmt.Sprintf("The harness woke you, the program manager instance %q", agent)
	if lane != "" {
		who += fmt.Sprintf(", for a pass over your lane %q", lane)
	} else {
		who += " for a pass"
	}
	why := "."
	switch {
	case due:
		why = fmt.Sprintf(": your schedule is due, and you pass every %s.", instance.Triggers.Every)
	case wake.armed():
		why = fmt.Sprintf(": events you watch arrived, and the streams have been quiet for %s since.", PassSettleWindow)
	}
	lines := []string{
		who + why + " Nobody is waiting at a terminal for this: what you produce is recorded and read later.",
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra, and nothing about being woken on a schedule or by an event widens what you may decide or change.",
		"Before you file anything, check it against the work already admitted. A duplicate admission costs a whole run and the reviews after it, and a pass that runs on a cadence files the same duplicate on every cadence.",
		"",
	}
	lines = append(lines, describeWake(instance.Triggers, wake)...)
	lines = append(lines, "", sweep.Contract())
	return strings.Join(lines, "\n")
}

// describeWake lists the events since the cursor, grouped by stream in the
// order the streams are read.
func describeWake(triggers config.Triggers, wake passWake) []string {
	if len(triggers.On) == 0 {
		return []string{"You are woken on your schedule alone; your triggers watch no events."}
	}
	if !wake.armed() {
		lines := []string{fmt.Sprintf("Nothing you watch (%s) has happened since your last pass.", describeClasses(triggers.On))}
		return append(lines, wake.problems...)
	}
	lines := []string{"What happened since your last pass, by stream:"}
	for _, stream := range runstate.PassStreams {
		var events []PassEvent
		for _, event := range wake.events {
			if event.Stream == stream {
				events = append(events, event)
			}
		}
		if len(events) == 0 {
			continue
		}
		lines = append(lines, "", fmt.Sprintf("%s — %s after %s:", describeStream(stream), describeCounts(events), wake.cursor[stream].UTC().Format(time.RFC3339)))
		for index, event := range events {
			if index == maxPassEventsListed {
				lines = append(lines, fmt.Sprintf("- and %d more not listed here; the counts above are whole", len(events)-maxPassEventsListed))
				break
			}
			lines = append(lines, "- "+describeEvent(event))
		}
	}
	if len(wake.problems) > 0 {
		lines = append(lines, "")
		lines = append(lines, wake.problems...)
	}
	return lines
}

func describeStream(stream string) string {
	switch stream {
	case runstate.PassStreamRuns:
		return "Run records"
	case runstate.PassStreamTracker:
		return "Tracker"
	default:
		return stream
	}
}

// describeCounts says how many events of each class a stream holds, in the
// order the classes are defined.
func describeCounts(events []PassEvent) string {
	counts := map[config.TriggerEvent]int{}
	for _, event := range events {
		counts[event.Class]++
	}
	var parts []string
	for _, class := range config.TriggerEvents {
		if counts[class] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[class], describeClass(class, counts[class])))
		}
	}
	return strings.Join(parts, ", ")
}

// describeClass names a class in the singular or the plural.
func describeClass(class config.TriggerEvent, count int) string {
	word := strings.TrimSuffix(string(class), "s")
	if count == 1 {
		return word
	}
	return string(class)
}

func describeClasses(classes []config.TriggerEvent) string {
	names := make([]string, 0, len(classes))
	for _, class := range classes {
		names = append(names, string(class))
	}
	return strings.Join(names, ", ")
}

func describeEvent(event PassEvent) string {
	line := fmt.Sprintf("%s %s: %s", event.At.UTC().Format(time.RFC3339), describeClass(event.Class, 1), event.Subject)
	if detail := strings.TrimSpace(event.Detail); detail != "" {
		line += " — " + detail
	}
	line = strings.Join(strings.Fields(line), " ")
	if len(line) <= maxPassEventLineBytes {
		return line
	}
	const marker = " […]"
	cut := maxPassEventLineBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + marker
}

// describeEventCounts renders what a pass carried, for the line a session
// prints and the record `yoyo sweeps` shows.
func describeEventCounts(counts map[string]int) string {
	var parts []string
	for _, class := range config.TriggerEvents {
		if count := counts[string(class)]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, describeClass(class, count)))
		}
	}
	return strings.Join(parts, ", ")
}
