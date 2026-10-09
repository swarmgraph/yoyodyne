package orchestrator

// Carrying out what the development manager decided, with nobody typing a verb.
//
// The decision has been durable since yoyodyne-ifd.311: the development manager
// records a repair or a re-run in her own conversation, it spends the item's
// budget as it is recorded, and the two actions that act on one read it back
// rather than taking words from whoever ran the command. What that landed was a
// carry-out that carries her decision. What it did not land was one that fires.
//
// Between the decision and the firing sat a person. Thirty-three items stood
// decided and unfired, some for days, because the only thing that executed one
// was the operator's assistant typing `yoyo triage repair`; the development
// manager decided within the hour and then nothing happened, and her own sweeps
// reported it as a standing finding from the day they began. That is the autonomy
// goal failing at the one place the machinery was otherwise complete.
//
// So the harness fires it, on the same pass that chooses everything else it runs.
// What changes is the hand and nothing else: the decision is hers, the gates are
// the ones that already refused a carry-out typed by hand, and the attribution the
// fresh run carries is still read from the record she wrote.
//
// # The gates are the ones that were already there
//
// Nothing here re-implements a gate and nothing here relaxes one. A re-run goes
// through Rerunner and a repair through RepairContinuer, exactly as the verbs do,
// so the intake hold, the item's triage budgets, the once-per-stoppage claim, the
// preserved-work rules, and developer capacity all refuse precisely what they
// refused before. The one gate this reads itself is the operator's pause on
// harness spending, and it is read here rather than left to the pipeline for a
// reason that is specific to the repair: a repair supersedes the stopped run's
// blocker and puts the item back before it starts anything, so a pause met after
// those writes is a paused harness that nonetheless unblocked an item. Reading it
// first is what keeps a paused harness from touching anything at all.
//
// # Every refusal is a finding, because silence is what this replaces
//
// A carry-out that failed quietly would reproduce the condition one item at a
// time: a decision recorded, nothing happening, and nothing anywhere saying why.
// So every gate that stops one is written onto the item's own triage record — the
// gate, what it said, and what would clear it — and the docket entry the
// development manager reads joins it. A decision that cannot be carried out says
// so where she is already looking, and the action that carries the decision out
// clears the finding as it starts — whichever hand fired it.
//
// # As many as there are slots for, and paced when it is refused
//
// A carry-out is a run, so it takes a developer slot and the pass starts it
// exactly as it starts a chosen item: in a goroutine, counted against capacity,
// waited out with everything else. A pull fires every decision it has a free
// slot for, because a decision left behind another for want of nothing but its
// place on the docket is one nobody attempts and nothing refuses — which is how
// two re-runs sat unfired and unrecorded for a week (yoyodyne-ifd.428.39). What
// the slots do not stretch to is written onto the item by RecordUnattempted once
// it has stood a poll interval, so no decision is ever silently passed over.
//
// A permanent refusal is left alone until its decision changes. Other
// refusals are paced according to who the gate is shut for. The
// pass reads the docket every poll interval and attempts every decision it can,
// so an unpaced retry of a gate shut for one item — a directive pausing it, work
// it waits on, a worktree somebody has been in — would take a developer slot
// several times a minute for as long as the gate stood, and crowd out every
// decided stoppage and queued item behind it. A gate shut for everything at once —
// the operator's pause, the intake hold, a full harness — is not paced, because
// nothing is behind it to starve and pacing it would leave a decision uncarried
// for a quarter of an hour after the switch was already open, which is the
// latency this exists to remove.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// CarryOutDocket is the docket the decisions were made against. It is read and
// never written: an entry stands as the record that work stopped however many
// times a decision about it is carried out.
type CarryOutDocket interface {
	List() ([]triage.Entry, error)
}

// CarryOutDecisions is the item's durable triage record: what the development
// manager decided, and what became of the harness's attempts to carry it out.
// The first is read and the second is written, which is the whole of what this
// package adds to that record — nothing here decides anything or spends anything.
//
// Clearing a finding is not here, deliberately. The action that carries the
// decision out clears it as it starts, whichever hand fired the action, so a
// carry-out typed at a terminal takes the finding back exactly as this does; see
// RerunDecisions.
//
// It is satisfied by *runstate.TriageStore.
type CarryOutDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
	RecordCarryOutRefusal(ctx context.Context, workItemID string, refusal runstate.TriageCarryOut, at time.Time) (runstate.TriageCounters, error)
	RecordCarryOutUnattempted(ctx context.Context, workItemID string, unattempted runstate.TriageCarryOut, at time.Time) (runstate.TriageCounters, error)
	ClearCarryOut(ctx context.Context, workItemID, runID string, at time.Time) (runstate.TriageCounters, error)
	DeliverCarryOutNotes(ctx context.Context, workItemID string, at time.Time, deliver func(context.Context, string) error) error
}

// CarryOutReruns is what the harness has already claimed of the re-run decisions.
// It is the other half of what says a decision is outstanding: a decision
// authorizes one re-run, and a claim is what says it was acted on.
//
// It is satisfied by *runstate.RerunStore.
type CarryOutReruns interface {
	Claimed(workItemID string) ([]runstate.Rerun, error)
}

// CarryOutRuns is the durable run state this sweep reads. Its readings answer
// the same question in the two shapes it takes: whether an item has a run in
// flight, which makes it not stopped work at all, and how much of a repair grant
// the item's runs have already been handed, which is what says a repair decision
// still has something left to carry out.
// Held distinguishes a pending dispatch from a live process carrying that run.
//
// It reads and never writes. What becomes of a run is the run's own to record.
//
// It is satisfied by *runstate.Store.
type CarryOutRuns interface {
	Incomplete() ([]runstate.State, error)
	Recorded() ([]runstate.State, error)
	Held(runID string) (bool, error)
}

// CarryOutRerunner starts a fresh run of an item whose stoppage was decided a
// re-run. It is satisfied by Rerunner.
type CarryOutRerunner interface {
	Rerun(ctx context.Context, request RerunRequest) (RerunResult, error)
}

// CarryOutRepairer re-enters the repair loop of a stopped run whose stoppage was
// decided a repair. It is satisfied by RepairContinuer.
type CarryOutRepairer interface {
	Continue(ctx context.Context, request RepairContinueRequest) (RepairContinueResult, error)
}

// CarryOutRearmer arms the merge of a publication nothing ever asked the forge
// to merge, on a re-arm decision. It is satisfied by Rearmer.
type CarryOutRearmer interface {
	Rearm(ctx context.Context, request RearmRequest) (RearmResult, error)
}

// CarryOutCheckStages continues a run the check stage bound stopped, at its
// checks. It is the one thing this fires that nobody decided: the harness
// stopped the stage, so carrying it on is the harness's own act rather than a
// decision of the development manager's. It is satisfied by CheckStageContinuer.
type CarryOutCheckStages interface {
	Due(runID string) (bool, error)
	Continue(ctx context.Context, request CheckStageContinueRequest) (CheckStageContinueResult, error)
}

// checkStageWaitNoter writes an overdue continuation's remaining gate onto
// its item. It is separate from the action interface so other continuers need
// not own durable wait accounting.
type checkStageWaitNoter interface {
	NoteWaiting(ctx context.Context, runID, why, clears string) error
}

// DecisionContinueChecks is the task a carry-out fires for a check stage the
// bound stopped. It is not a word from the development manager's vocabulary and
// is never written onto an item's triage record: no refusal of it is recorded
// there, because nobody's decision is waiting on it.
const DecisionContinueChecks = "continue-checks"

// CarryOutStalls continues a run the harness stopped for a silent provider
// stream, once, in the session and at the phase it stalled in. Like a check
// stage the bound stopped, it is fired with nobody having decided it. It is
// satisfied by StallContinuer.
type CarryOutStalls interface {
	Due(runID string) (bool, error)
	Continue(ctx context.Context, request StallContinueRequest) (StallContinueResult, error)
}

// DecisionContinueStall is the task a carry-out fires for a first silent-stream
// stall. Like DecisionContinueChecks it is not a word from the development
// manager's vocabulary and is never written onto an item's triage record.
const DecisionContinueStall = "continue-stall"

// harnessOwnTask reports a task the harness fires with nobody having decided
// it, which is never recorded against the item's triage record as a decision.
func harnessOwnTask(decision string) bool {
	return decision == DecisionContinueChecks || decision == DecisionContinueStall
}

// CarryOut fires the decisions the development manager recorded. It decides
// nothing, spends nothing, and grants nothing: what it does is find a decision
// somebody else made that the harness has not acted on, hand it to the action
// that already knows how to act on it, and write down what became of the attempt.
type CarryOut struct {
	Docket CarryOutDocket
	// Decisions is where the decisions are read and where a refused attempt is
	// recorded. Required: a carry-out that could not read the record would be
	// firing on nobody's decision, and one that could not write it would be the
	// silence this exists to end.
	Decisions CarryOutDecisions
	// Notes appends permanent refusals to the tracker item as well as its
	// durable triage record. The production harness wires its tracker here.
	Notes interface {
		Show(context.Context, string) (beads.WorkItem, error)
		RecordOutcome(context.Context, string, string) (beads.WorkItem, error)
	}
	// Reruns and Runs are what has already been carried out of those decisions.
	// Both required: a decision already acted on is not one to act on again, and
	// the two records are the only things that say so.
	Reruns CarryOutReruns
	Runs   CarryOutRuns
	// Rerunner and Repairer are the two actions that carry a decision out. Each is
	// optional on its own — a harness wired with one fires that half and leaves the
	// other for a person, which is what it had before this existed — and a
	// carry-out with neither fires nothing.
	Rerunner CarryOutRerunner
	Repairer CarryOutRepairer
	// Rearmer makes the merge request of a publication the development manager
	// decided a re-arm of: one nothing ever asked the forge to merge, or one whose
	// merge the forge dropped. Optional: a carry-out wired without it leaves that
	// decision for somebody typing `yoyo triage rearm`, which is what it was before
	// yoyodyne-ifd.429.31 for the first and yoyodyne-ifd.428.46 for the second.
	Rearmer CarryOutRearmer
	// Items reads whether the items a publication waiting on its target's red
	// check waits on are closed, so the harness's own arming of it is attempted
	// only once they are. Optional: a carry-out wired without it attempts that
	// arming every time its pacing allows, and the Rearmer's own gate refuses it.
	Items RearmItems
	// CheckStages continues a run the check stage bound stopped, where she has
	// decided nothing about it. Optional: a carry-out wired without it leaves such
	// a stoppage on the docket for her, which is what it was before.
	CheckStages CarryOutCheckStages
	// Stalls continues a run the harness stopped for a silent provider stream,
	// once, where she has decided nothing about it. Optional: a carry-out wired
	// without it leaves such a stoppage on the docket for her, which is what it
	// was before yoyodyne-a0s.
	Stalls CarryOutStalls
	// Holds is the operator's pause over everything the harness spends. Optional,
	// and a carry-out wired without one is one nothing can pause, which is what
	// every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// CarryOutTask is one recorded decision the harness has not acted on: which
// stoppage, what was decided, and the reasoning it was decided on.
//
// The reasoning travels with it so whoever reads the pass can see what is about
// to be carried out. It is not what either action records: both read the
// decision again from the durable record as they carry it out, so the words a
// run attributes to the development manager are always words she wrote, however
// the task reached the action.
type CarryOutTask struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	// DecidedAt is when the decision was recorded, which is what a decision no
	// pass has attempted is measured from. It is zero on the harness's own
	// continuation of a check stage, which nobody decided.
	DecidedAt time.Time `json:"decided_at,omitempty"`
	// Harness says nobody decided this: it is the harness arming a merge it
	// withdrew for its target's red check, once the items that check was filed
	// as have closed. DecidedAt is then when the merge was withdrawn.
	Harness bool `json:"harness,omitempty"`
	// Recover continues a recorded repair whose dispatch is still pending. It
	// reuses that run's slot and grant; the action re-reads it under its lease.
	Recover bool `json:"recover,omitempty"`
	// Preserved says the pass found the stopped run's branch or worktree still
	// there when it chose this decision, which is what puts it ahead of fresh
	// pulls of any priority; AheadOf is the ready work of higher priority it was
	// put ahead of, each by its title with its identifier after it, which the
	// run's reason and the item's notes name. Both are the pass's to set and
	// empty on a decision carried out any other way.
	Preserved bool     `json:"preserved,omitempty"`
	AheadOf   []string `json:"ahead_of,omitempty"`
}

// CarriedOut is what one attempt came to. It reports an attempt that was stopped
// as carefully as one that fired: a decision that cannot be carried out is the
// thing this whole mechanism exists to stop being silent.
type CarriedOut struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Decision   string `json:"decision"`
	// Carried reports a run actually started or continued. An attempt a gate
	// stopped is never reported as one that fired.
	Carried bool `json:"carried"`
	// Reason is what the run records as why it is going, which is the development
	// manager's decision cited to the record she wrote it on. It is empty on an
	// attempt that started nothing.
	Reason string `json:"reason,omitempty"`
	// Gate is which gate stopped it, in the durable record's own vocabulary, and
	// Waiting says that gate clears without anybody doing anything. Both are empty
	// on an attempt that fired.
	Cause   triage.CarryOutCause `json:"cause,omitempty"`
	Gate    string               `json:"gate,omitempty"`
	Waiting bool                 `json:"waiting,omitempty"`
	// ReleasedHold says the refusal was of a re-run, will not clear, and was
	// about a run that left no branch or worktree, so the decision no longer
	// holds its item and the next pull may start it like any other ready item.
	// The note written onto the item says so beside the refusal; see
	// readmodel's releasesRerun, which is the hold that lets go.
	ReleasedHold bool `json:"released_hold,omitempty"`
	// Problem is the whole account of a stopped attempt: what the gate said and
	// what would clear it. It is what a pass prints, and it is the same sentence
	// the item's own record now carries.
	Problem string `json:"problem,omitempty"`
	// RecordProblem is a finding this attempt could not write down. It is reported
	// beside the attempt rather than in place of it, and never left unsaid: a
	// refusal nobody recorded is a decision that reads as never attempted, which is
	// exactly the state this exists to end.
	RecordProblem string `json:"record_problem,omitempty"`
}

// Outstanding is every decision recorded and not carried out, in the docket's own
// order — oldest stoppage first, which is the order the entries were recorded in
// rather than the order the decisions were made — with the ones nothing can act
// on now left out.
//
// Three things take an entry out. An item with a run in flight is not stopped
// work whatever the docket said when the entry was written, except for a pending
// repair dispatch with no live lease holder; a decision the harness has already
// carried out as far as it goes is not outstanding at all; and one whose last
// attempt a gate refused is left to its pacing, because the finding recorded then is what says so and
// repeating it every poll would starve every decision behind it.
//
// It reads and writes nothing. What it produces is a list somebody else acts on,
// which is what lets the pass take a slot for each before anything is attempted.
func (c CarryOut) Outstanding() ([]CarryOutTask, error) {
	reading, err := c.read()
	return reading.tasks, err
}

// carryOutReading is one sweep of the recorded decisions: the ones a pass may
// attempt now, and the ones standing that it may not, each with why. The second
// list is what nothing wrote down before yoyodyne-ifd.428.39 — a decision the
// sweep passed over was simply absent from the first, and a decision nobody
// attempts is one nobody refuses, so the item carried no word of it for a week.
type carryOutReading struct {
	tasks []CarryOutTask
	held  []heldDecision
}

// heldDecision is a standing decision the sweep did not offer, and why: the gate
// that kept it from being attempted, in the record's own vocabulary, what that
// gate is, and what would let it through.
type heldDecision struct {
	task   CarryOutTask
	gate   string
	why    string
	clears string
}

// read is the sweep behind Outstanding and RecordUnattempted, so what a pass
// attempts and what it writes down as unattempted are one reading's two halves.
//
// It walks the docket's stopped runs and then, for every item the docket names,
// the item's latest decision where that decision is about a run no stopped-run
// entry stands for. The second walk is the half the entry-by-entry reading could
// never reach: a decision is recorded by run, and an item whose latest stoppage
// was never docketed as a stopped run — a re-run cancelled on its way out, say —
// has a decision no entry leads to. Only the latest is taken there, because an
// earlier decision about another of the item's runs is one she has since decided
// past.
func (c CarryOut) read() (carryOutReading, error) {
	if err := c.validate(); err != nil {
		return carryOutReading{}, err
	}
	entries, err := c.Docket.List()
	if err != nil {
		return carryOutReading{}, fmt.Errorf("read the triage docket: %w", err)
	}
	inFlight, err := c.itemsInFlight()
	if err != nil {
		return carryOutReading{}, err
	}
	// What the repair grants have already bought is counted from every run the
	// product has had, which is the one reading here that grows with the history.
	// It is taken only where a repair decision is actually standing, and once: the
	// ordinary pass has no repair to fire and pays nothing for the accuracy, and a
	// pass that does reads the runs once rather than once per item.
	history := onceRecorded(c.Runs)
	now := c.now()
	read := make(map[string]outstandingItem, len(entries))
	var items []string
	docketed := make(map[string]bool, len(entries))
	var reading carryOutReading
	var problems []error
	itemFor := func(workItemID string) outstandingItem {
		item, seen := read[workItemID]
		if !seen {
			item = c.outstandingFor(workItemID)
			read[workItemID] = item
			items = append(items, workItemID)
			if item.problem != nil {
				problems = append(problems, item.problem)
			}
		}
		return item
	}
	consider := func(entry triage.Entry, item outstandingItem) {
		task, outstanding, held, err := item.taskFor(entry, now, history)
		if err != nil {
			problems = append(problems, err)
			return
		}
		if held != nil {
			reading.held = append(reading.held, *held)
			return
		}
		if !outstanding {
			task, outstanding, err = c.checkStageTask(entry, item)
			if err != nil {
				problems = append(problems, err)
				return
			}
		}
		if !outstanding {
			task, outstanding, err = c.stallTask(entry, item)
			if err != nil {
				problems = append(problems, err)
				return
			}
		}
		if !outstanding {
			return
		}
		if task.Recover {
			held, err := c.Runs.Held(task.RunID)
			if err != nil {
				problems = append(problems, fmt.Errorf("read whether pending repair run %s has a live holder: %w", task.RunID, err))
				return
			}
			if held {
				return
			}
		}
		if running, busy := inFlight[entry.WorkItemID]; busy {
			// A repair continues the run it was granted for, so that run going again
			// is the decision being carried out rather than something keeping it back.
			if (running == entry.RunID && !task.Recover) || task.Decision == DecisionContinueStall {
				return
			}
			if running != entry.RunID {
				reading.held = append(reading.held, heldDecision{
					task:   task,
					gate:   runstate.TriageGateWorkItem,
					why:    fmt.Sprintf("run %s of %s is in flight, and a decision about an item something is already running is not attempted until that run ends", running, entry.WorkItemID),
					clears: fmt.Sprintf("run %s ending, which needs nobody; the pass after it attempts the decision", running),
				})
				return
			}
		}
		reading.tasks = append(reading.tasks, task)
	}
	// A publication entry is considered where its run has no stopped-run entry,
	// which is a request nothing ever asked the forge to merge: a re-run decided
	// about it is fired off that entry, so the claim it makes and the claim this
	// reads back are keyed alike. Only a re-run is ever offered from one, because
	// taskFor offers nothing else a publication can be decided.
	//
	// A run docketed as a stoppage and as a raise is one run with one decision,
	// and it is offered once, under the stoppage's entry, which is the one the
	// carry-out itself takes where a run has both.
	stoppedRuns := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Class == triage.ClassStoppedRun {
			stoppedRuns[entry.RunID] = true
		}
	}
	for _, entry := range entries {
		if entry.WorkItemID == "" {
			continue
		}
		if entry.Class == triage.ClassEscalation && stoppedRuns[entry.RunID] {
			itemFor(entry.WorkItemID)
			continue
		}
		// A raise is walked as a stoppage is: a re-run is one of the two decisions
		// that answer it, and the key a carry-out claims against is the raise's
		// own, so the decision has to be offered under that entry rather than under
		// one synthesized for a stopped run it never was.
		if entry.Class != triage.ClassStoppedRun && entry.Class != triage.ClassEscalation && (entry.Class != triage.ClassPublication || stoppedRuns[entry.RunID]) {
			itemFor(entry.WorkItemID)
			continue
		}
		docketed[entry.RunID] = true
		item := itemFor(entry.WorkItemID)
		if item.problem != nil {
			continue
		}
		consider(entry, item)
	}
	for _, workItemID := range items {
		item := read[workItemID]
		if item.problem != nil {
			continue
		}
		latest, found := item.latestDecision()
		if !found || docketed[latest.RunID] {
			continue
		}
		consider(triage.Entry{
			Key:        triage.Key(triage.ClassStoppedRun, latest.RunID),
			Class:      triage.ClassStoppedRun,
			RunID:      latest.RunID,
			WorkItemID: workItemID,
		}, item)
	}
	// A re-arm is fired on its own path, and what that path holds back is written
	// down here with everything else the sweep holds back, so a re-arm no pass
	// attempts is as visible on the item as a re-run no pass attempts.
	if c.Rearmer != nil {
		// The reading is a pass's listing, which takes no context of its own; the
		// one call in it that can reach outside the harness is the tracker's own
		// bounded read of the items a red target's publication waits on.
		_, held, err := c.readRearms(context.Background(), entries, inFlight, history, now, "")
		if err != nil {
			problems = append(problems, err)
		}
		reading.held = append(reading.held, held...)
	}
	return reading, errors.Join(problems...)
}

// RecordUnattempted writes onto each item's triage record every decision that
// stands a poll interval or more after it was recorded with no pass having
// attempted it, and why, and returns an account of each one it wrote.
//
// A decision is attempted where a pass handed it to the action that carries it
// out: the action fires it, or a gate refuses it and the refusal is written. So
// what this finds is every other ending — a decision the sweep held back, and
// one the sweep offered that the pass then did not take — and passed is the
// pass's own account of the second: the offered decisions it did not attempt,
// by run, with why. An offered decision the pass does not name was attempted.
//
// What is already written about the decision since it was made is left to
// stand: a refusal is the attempt this looks for, and an unattempted record
// saying the same thing is not written twice. poll is the pass's own interval,
// which is the whole of the grace a decision is given before its not having been
// attempted is a finding.
func (c CarryOut) RecordUnattempted(ctx context.Context, poll time.Duration, passed map[string]string) ([]CarriedOut, error) {
	reading, err := c.read()
	var candidates []heldDecision
	candidates = append(candidates, reading.held...)
	for _, task := range reading.tasks {
		why, notTaken := passed[task.RunID]
		if !notTaken {
			continue
		}
		candidates = append(candidates, heldDecision{
			task:   task,
			gate:   runstate.TriageGateCapacity,
			why:    why,
			clears: "the condition named above clearing so a pass can give it a developer slot; the continuation or decision still stands",
		})
	}
	now := c.now()
	counters := make(map[string]runstate.TriageCounters)
	var written []CarriedOut
	var problems []error
	if err != nil {
		problems = append(problems, err)
	}
	for _, held := range candidates {
		task := held.task
		if task.Decision == DecisionContinueChecks {
			if err := c.noteCheckStageWait(ctx, task.RunID, held.why, held.clears); err != nil {
				problems = append(problems, err)
			}
			continue
		}
		if harnessOwnTask(task.Decision) || task.DecidedAt.IsZero() || now.Sub(task.DecidedAt) < poll {
			continue
		}
		record, seen := counters[task.WorkItemID]
		if !seen {
			read, err := c.Decisions.Counters(task.WorkItemID)
			if err != nil {
				problems = append(problems, fmt.Errorf("read what triage has recorded about %s: %w", task.WorkItemID, err))
				continue
			}
			counters[task.WorkItemID], record = read, read
		}
		if standing, found := record.CarryOutOf(task.RunID); found && !standing.RefusedAt.Before(task.DecidedAt) {
			if !standing.Unattempted || (standing.Gate == held.gate && standing.Refusal == strings.TrimSpace(held.why)) {
				continue
			}
		}
		write, stopWriting := recordContext(ctx)
		_, err := c.Decisions.RecordCarryOutUnattempted(write, task.WorkItemID, runstate.TriageCarryOut{
			RunID:    task.RunID,
			Decision: task.Decision,
			Gate:     held.gate,
			Refusal:  held.why,
			Clears:   held.clears,
		}, now)
		stopWriting()
		account := CarriedOut{
			WorkItemID: task.WorkItemID,
			RunID:      task.RunID,
			DocketKey:  task.DocketKey,
			Decision:   task.Decision,
			Gate:       held.gate,
			Problem: fmt.Sprintf("the %q the development manager decided about the stoppage of run %s at %s has not been attempted by any pass: %s. What clears it: %s",
				task.Decision, task.RunID, task.DecidedAt.UTC().Format(time.RFC3339), strings.TrimSpace(held.why), strings.TrimSpace(held.clears)),
		}
		if err != nil {
			account.RecordProblem = fmt.Sprintf(
				"and that could not be written onto %s's triage record, so the docket the development manager reads does not carry it and this pass is the only thing that says it: %v",
				task.WorkItemID, err)
		}
		written = append(written, account)
	}
	return written, errors.Join(problems...)
}

// outstandingItem is one work item's record as this sweep reads it: what has been
// decided, what has been claimed of the re-runs, and what stopped either being
// read.
type outstandingItem struct {
	counters runstate.TriageCounters
	claimed  []runstate.Rerun
	problem  error
}

func (c CarryOut) outstandingFor(workItemID string) outstandingItem {
	counters, err := c.Decisions.Counters(workItemID)
	if err != nil {
		return outstandingItem{problem: fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)}
	}
	claimed, err := c.Reruns.Claimed(workItemID)
	if err != nil {
		return outstandingItem{problem: fmt.Errorf("read the re-runs already carried out for %s: %w", workItemID, err)}
	}
	return outstandingItem{counters: counters, claimed: claimed}
}

// latestDecision is the item's most recently recorded decision, of whatever
// kind, and whether it has one. A wait or an escalation about a later stoppage
// is her deciding past an earlier one as surely as a re-run is, so every kind
// counts; where the latest is one the harness does not carry out, taking it
// offers nothing.
func (i outstandingItem) latestDecision() (runstate.TriageDecision, bool) {
	return i.counters.LatestDecision()
}

// onceRecorded reads every run the product has had, the first time somebody asks
// and not before. A pass with no repair decision standing never asks, which is
// nearly every pass; one that does asks once however many items it walks.
func onceRecorded(runs CarryOutRuns) func() ([]runstate.State, error) {
	var recorded []runstate.State
	var problem error
	read := false
	return func() ([]runstate.State, error) {
		if !read {
			recorded, problem = runs.Recorded()
			if problem != nil {
				problem = fmt.Errorf("read the recorded runs, to count what the repair grants have already bought: %w", problem)
			}
			read = true
		}
		return recorded, problem
	}
}

// repairOutstanding reports a repair grant with rounds the harness has not handed
// to a run yet. It asks the same two records the repair action asks and in the
// same order: what was granted, against what the item's own runs record having
// been continued on. The counters alone cannot answer it — the grant counter is a
// total nothing clears — and a cheaper reading of them would offer a grant the
// action then refuses, which is a finding nobody asked for every pass.
func (i outstandingItem) repairOutstanding(workItemID string, history func() ([]runstate.State, error)) (bool, error) {
	if i.counters.GrantedRounds < 1 {
		return false, nil
	}
	recorded, err := history()
	if err != nil {
		return false, err
	}
	carried := 0
	for _, state := range recorded {
		if state.WorkItemID == workItemID {
			carried += state.CarriedOutRepairAttempts()
		}
	}
	return i.counters.GrantedRounds-carried > 0, nil
}

// taskFor reports the decision standing about one entry's stoppage that the
// harness has not acted on, and whether there is one — or, where a decision
// stands that the sweep will not offer, why not.
//
// It asks the same two questions of each decision that the action carrying it out
// asks, and asks them the same way round: what was decided, and how much of that
// decision the harness has already spent. The counters alone cannot answer the
// second — they are totals nothing clears — which is why the claims and the
// continuations recorded on the item's runs are read beside them.
func (i outstandingItem) taskFor(entry triage.Entry, now time.Time, history func() ([]runstate.State, error)) (CarryOutTask, bool, *heldDecision, error) {
	decision, found := i.counters.DecisionOf(entry.RunID)
	if !found {
		// Old budgets predate durable decisions. Offer them only to record the
		// missing authorization, never to execute on the budget alone. Carry
		// refuses before asking either action to do anything.
		if len(i.counters.Decisions) != 0 {
			return CarryOutTask{}, false, nil, nil
		}
		if outstanding, err := i.repairOutstanding(entry.WorkItemID, history); err != nil {
			return CarryOutTask{}, false, nil, err
		} else if outstanding {
			decision.Decision = runstate.TriageDecisionRepair
		} else if i.counters.Reruns > len(i.claimed) {
			decision.Decision = runstate.TriageDecisionRerun
		} else {
			return CarryOutTask{}, false, nil, nil
		}
	}
	task := CarryOutTask{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Decision:   decision.Decision,
		Reason:     decision.Reason,
		DecidedAt:  decision.DecidedAt,
	}
	switch decision.Decision {
	case runstate.TriageDecisionRerun:
		outstanding, held := i.rerunOutstanding(entry, decision)
		if held != nil {
			held.task = task
			return CarryOutTask{}, false, held, nil
		}
		if !outstanding {
			return CarryOutTask{}, false, nil, nil
		}
	case runstate.TriageDecisionRepair:
		// A raise has no stopped run for a repair to continue, which is why
		// recording one on it is refused; a repair recorded on one before that
		// refusal existed is not offered, since carrying it out could only refuse.
		if entry.Class == triage.ClassEscalation {
			return CarryOutTask{}, false, nil, nil
		}
		outstanding, err := i.repairOutstanding(entry.WorkItemID, history)
		if err != nil {
			return CarryOutTask{}, false, nil, err
		}
		recorded, err := history()
		if err != nil {
			return CarryOutTask{}, false, nil, err
		}
		for _, state := range recorded {
			if state.RunID != entry.RunID || state.WorkItemID != entry.WorkItemID || !state.RepairDispatchPending() {
				continue
			}
			continuation := state.RepairContinuations[len(state.RepairContinuations)-1]
			if !continuation.ContinuedAt.Before(decision.DecidedAt) && strings.Contains(continuation.Reason, decision.Cite()) {
				task.Recover = true
			}
		}
		if !outstanding && !task.Recover {
			return CarryOutTask{}, false, nil, nil
		}
	default:
		// A re-scope, a wait, an escalation, and a merge re-arm are decisions this
		// sweep does not offer: the first three ask for no run at all, and a re-arm
		// is an integration retry rather than work, which takes no developer slot.
		// A re-arm is fired on its own path, CarryRearms, whether the merge it
		// makes is one nothing ever asked the forge for or one the forge dropped.
		return CarryOutTask{}, false, nil, nil
	}
	if stopped, refused := i.counters.CarryOutOf(entry.RunID); refused && stopped.BlocksDecision(task.Decision, task.DecidedAt, now) {
		return CarryOutTask{}, false, nil, nil
	}
	return task, true, nil, nil
}

// checkStageTask reports a stoppage the check stage bound made that the harness
// continues itself now, and whether there is one. A decision the development
// manager recorded about the stoppage is hers to have carried out instead, so
// only an entry nobody decided anything about is taken. Machine load does not
// withhold it, just as it does not withhold fresh work.
func (c CarryOut) checkStageTask(entry triage.Entry, item outstandingItem) (CarryOutTask, bool, error) {
	if c.CheckStages == nil || (!entry.HarnessContinuesChecks && strings.TrimSpace(entry.CheckStageStop) == "") {
		return CarryOutTask{}, false, nil
	}
	// A stop or a decision to let the run finish was made about the run in
	// flight, and decides nothing about the stoppage the bound later made.
	if decision, decided := item.counters.DecisionOf(entry.RunID); decided && !decision.InFlight() {
		return CarryOutTask{}, false, nil
	}
	// The run's current obligation is authoritative. An old docket entry may
	// have recorded removal flags before checkout restoration was supported.
	due, err := c.CheckStages.Due(entry.RunID)
	if err != nil || !due {
		return CarryOutTask{}, false, err
	}
	return CarryOutTask{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Decision:   DecisionContinueChecks,
		Reason:     "the check stage bound stopped this run under load, and the harness continues it at its checks",
	}, true, nil
}

// stallTask reports a first stall — a silent stream or a spent session budget —
// the harness continues itself now, and whether there is one. A decision the development manager recorded
// about the stoppage is hers to have carried out instead, so only an entry
// nobody decided anything about is taken.
func (c CarryOut) stallTask(entry triage.Entry, item outstandingItem) (CarryOutTask, bool, error) {
	if c.Stalls == nil || !entry.HarnessContinuesStall {
		return CarryOutTask{}, false, nil
	}
	// A decision about an earlier stoppage may already have put this run back
	// to work. It does not decide a later silent session in that repair; only a
	// decision made about the current stoppage takes precedence over the harness.
	if decision, decided := item.counters.DecisionOf(entry.RunID); decided && !decision.InFlight() && !decision.DecidedAt.Before(entry.RecordedAt) {
		return CarryOutTask{}, false, nil
	}
	due, err := c.Stalls.Due(entry.RunID)
	if err != nil || !due {
		return CarryOutTask{}, false, err
	}
	return CarryOutTask{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Decision:   DecisionContinueStall,
		Reason:     "the harness stopped this run's AI session itself, because it went silent or its total budget ran out, and continues the run itself once",
	}, true, nil
}

// rerunOutstanding reports a re-run decision the harness has not acted on. Both
// halves are the question, and they are the ones the re-run action itself asks:
// this stoppage's own claim is what makes the once-per-stoppage bound, and the
// count of claims against the count of decisions is what stops one decision
// authorizing a re-run of every stoppage the item ever has.
//
// A claim on this stoppage answers the decision only where the claim came after
// it. A re-run decided again about a stoppage whose one re-run was already
// claimed — what the development manager recorded for yoyodyne-ifd.192 and .187
// on 2026-09-19 — is a decision nothing has acted on, and it is offered so the
// action refuses it and the refusal is written onto the item, rather than being
// read as carried out and passed over in silence. It is offered only while it
// is the item's latest decision: one she has since decided past is not hers to
// have carried out any more.
//
// Where no claim stands on this stoppage and the item's claims already match
// its re-runs, the decision is held back rather than dropped, with why: the
// record disagrees with itself, and the item is where that is said.
func (i outstandingItem) rerunOutstanding(entry triage.Entry, decision runstate.TriageDecision) (bool, *heldDecision) {
	for _, existing := range i.claimed {
		if existing.DocketKey != entry.Key {
			continue
		}
		if !decision.DecidedAt.After(existing.ClaimedAt) {
			return false, nil
		}
		latest, found := i.latestDecision()
		return found && latest.RunID == decision.RunID, nil
	}
	if i.counters.Reruns > len(i.claimed) {
		return true, nil
	}
	return false, &heldDecision{
		gate: runstate.TriageGateBudget,
		why: fmt.Sprintf("the item's record counts %d re-run(s) decided and %d already claimed, so by its own arithmetic this re-run has nothing left to carry it out, though no claim stands on this stoppage",
			i.counters.Reruns, len(i.claimed)),
		clears: "the development manager recording the re-run again, which spends a further re-run of the item and past the cap is `yoyo triage override`'s to permit",
	}
}

// Carry carries one recorded decision out and writes down what became of the
// attempt. It reports the run's own outcome and failure beside its account, so a
// caller that started this the way it starts any other run settles it the same way.
//
// The order is the order the guarantees need. The operator's pause is read before
// anything is attempted, because the repair writes to the item and the run before
// it starts anything and a paused harness must touch neither; the action is then
// asked, and every other gate refuses inside it exactly as it refuses a carry-out
// somebody typed; and the finding is written last, because until the attempt has
// ended there is nothing to record about it.
func (c CarryOut) Carry(ctx context.Context, task CarryOutTask) (account CarriedOut, outcome Outcome, err error) {
	// What a run fired ahead of the queue went ahead of is said wherever its reason
	// is, the pass's account included, whichever action carried it out.
	defer func() {
		if ahead := aheadOfQueue(task); account.Carried && ahead != "" && !strings.Contains(account.Reason, ahead) {
			account.Reason = withAheadOf(account.Reason, ahead)
		}
	}()
	if err := c.validate(); err != nil {
		return CarriedOut{}, Outcome{}, err
	}
	carried := CarriedOut{
		WorkItemID: task.WorkItemID,
		RunID:      task.RunID,
		DocketKey:  task.DocketKey,
		Decision:   task.Decision,
	}
	if task.Decision == DecisionContinueChecks {
		return c.continueChecks(ctx, task, carried)
	}
	if task.Decision == DecisionContinueStall {
		return c.continueStall(ctx, task, carried)
	}
	hold, held, err := c.paused()
	if err != nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false, err.Error(),
			"the operator's pause becoming readable again"), Outcome{}, nil
	}
	if held {
		return c.stopped(ctx, task, carried, runstate.TriageGateSpendingPause, true,
			fmt.Sprintf("the operator has paused everything the harness spends on a provider, since %s", hold.HeldAt.UTC().Format(time.RFC3339)),
			"`yoyo resume` lifting the pause; nothing was spent and the decision still stands"), Outcome{}, nil
	}
	counters, err := c.Decisions.Counters(task.WorkItemID)
	if err != nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false, err.Error(),
			"the item's triage record becoming readable again"), Outcome{}, nil
	}
	if _, decided := counters.DecisionOf(task.RunID); !decided {
		carried.Cause = triage.CarryOutDecisionMissing
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false,
			fmt.Sprintf("the item's budget records a %s but the development manager has recorded no durable triage decision about run %s on %s, so there is nothing authorized to carry out; the decision must be recorded in her conversation, with an override where its budget requires it", task.Decision, task.RunID, task.WorkItemID),
			"the development manager recording the missing decision again, or escalating the inconsistent record"), Outcome{}, nil
	}
	switch task.Decision {
	case runstate.TriageDecisionRerun:
		return c.rerun(ctx, task, carried)
	case runstate.TriageDecisionRepair:
		return c.repair(ctx, task, carried)
	default:
		return CarriedOut{}, Outcome{}, fmt.Errorf(
			"%q is not a decision the harness carries out; it carries out %q and %q, and every other decision asks for no run at all",
			task.Decision, runstate.TriageDecisionRepair, runstate.TriageDecisionRerun)
	}
}

// rerun starts the fresh run one re-run decision authorizes, and reports the
// three states the action distinguishes: a run that started, a gate that is
// waiting, and a refusal.
func (c CarryOut) rerun(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	if c.Rerunner == nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false,
			"nothing is wired to this harness to start a fresh run, so the re-run recorded against this stoppage waits on somebody running `yoyo triage rerun`",
			"a harness wired to start re-runs itself"), Outcome{}, nil
	}
	result, runErr := c.Rerunner.Rerun(ctx, RerunRequest{Run: task.RunID, AheadOf: aheadOfQueue(task)})
	switch {
	case result.IntakeHeld != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateIntakeHold, true,
			fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)),
			"`yoyo release` lifting the hold; nothing was claimed, so the stoppage keeps its re-run"), Outcome{}, nil
	case result.CapacityFull != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateCapacity, true,
			fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit),
			"a developer slot freeing, which needs nobody"+nextSlot(task)+"; nothing was claimed, so the stoppage keeps its re-run"), Outcome{}, nil
	case result.PausedBeforeStarting != nil:
		gate, clears, waiting := pausedGate(*result.PausedBeforeStarting)
		return c.stopped(ctx, task, carried, gate, waiting,
			fmt.Sprintf("the fresh run met %s where it would have started", pauseMet(*result.PausedBeforeStarting)), clears), Outcome{}, nil
	case !result.Started:
		if refusal, clears, undocketed := c.undocketed(task, runErr); undocketed {
			carried.Cause = carryOutCause(runErr)
			return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false, refusal, clears), Outcome{}, nil
		}
		gate, clears := carryOutGate(runErr)
		carried.Cause = carryOutCause(runErr)
		carried.ReleasedHold = carried.Cause != "" && result.Preserved.Disposition == runstate.PreservedGone
		return c.stopped(ctx, task, carried, gate, false, refusalText(runErr), clears), Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	carried.RecordProblem = result.RecordProblem
	return carried, result.Outcome, runErr
}

// repair re-enters the stopped run's own repair loop on the grant one repair
// decision recorded. It hands the action the run and nothing else, exactly as a
// re-run is handed: the action reads the decision and its reasoning from the
// record the development manager wrote, so a carry-out fired here and one typed
// at a terminal learn what she decided the one same way.
func (c CarryOut) repair(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	if c.Repairer == nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false,
			"nothing is wired to this harness to continue a stopped run, so the repair granted against this stoppage waits on somebody running `yoyo triage repair`",
			"a harness wired to continue stopped runs itself"), Outcome{}, nil
	}
	result, runErr := c.Repairer.Continue(ctx, RepairContinueRequest{Run: task.RunID, AheadOf: aheadOfQueue(task)})
	switch {
	case result.IntakeHeld != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateIntakeHold, true,
			fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)),
			"`yoyo release` lifting the hold; nothing was spent, so the item keeps its grant"), Outcome{}, nil
	case result.CapacityFull != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateCapacity, true,
			fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit),
			"a developer slot freeing, which needs nobody"+nextSlot(task)+"; nothing was spent, so the item keeps its grant"), Outcome{}, nil
	case !result.Continued:
		if refusal, clears, undocketed := c.undocketed(task, runErr); undocketed {
			carried.Cause = carryOutCause(runErr)
			return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false, refusal, clears), Outcome{}, nil
		}
		gate, clears := carryOutGate(runErr)
		carried.Cause = carryOutCause(runErr)
		return c.stopped(ctx, task, carried, gate, false, refusalText(runErr), clears), Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	carried.RecordProblem = result.RecordProblem
	return carried, result.Outcome, runErr
}

// aheadOfQueue is what a run fired ahead of higher-priority ready work records
// about it: the work it was put ahead of, and the rule that put it there. It is
// empty for every other decision, whose reason is what it always was.
func aheadOfQueue(task CarryOutTask) string {
	if !task.Preserved || len(task.AheadOf) == 0 {
		return ""
	}
	named := task.AheadOf
	if len(named) > readmodel.MaxPassedOverNamed {
		named = named[:readmodel.MaxPassedOverNamed]
	}
	listed := strings.Join(named, ", ")
	if further := len(task.AheadOf) - len(named); further > 0 {
		listed += fmt.Sprintf(", and %d further", further)
	}
	return fmt.Sprintf("It went ahead of %s of higher priority in the Lead Product Manager's order that stood ready (%s), because the stopped run's change is still there: a decided repair or re-run of preserved work takes the first free developer slot ahead of fresh pulls of any priority.",
		plural(len(task.AheadOf), "item", "items"), listed)
}

// withAheadOf ends a run's recorded reason with the sentence aheadOfQueue wrote,
// shortening the reason before it rather than the sentence where the two outgrow
// the bound a selection reason is held to, so what the run went ahead of is never
// the part cut.
func withAheadOf(reason, ahead string) string {
	ahead = strings.TrimSpace(ahead)
	if ahead == "" {
		return reason
	}
	room := runstate.MaxSelectionReasonBytes - len(ahead) - 1
	if room < 0 {
		return singleLine(ahead, runstate.MaxSelectionReasonBytes)
	}
	return singleLine(reason, room) + " " + ahead
}

// nextSlot is what a decision about preserved work that found every slot taken
// adds to what clears it: that no fresh pull goes ahead of it for the next one.
func nextSlot(task CarryOutTask) string {
	if !task.Preserved {
		return ""
	}
	return "; the stopped run's change is still there, so it is next: the first developer slot that frees is its, ahead of fresh pulls of any priority"
}

// continueChecks continues a run the check stage bound stopped, at its checks.
// A wait is said on the pass and, after thirty minutes, on the item. A refusal
// is written onto the run by the action itself, which hands the stoppage to
// the development manager.
func (c CarryOut) continueChecks(ctx context.Context, task CarryOutTask, carried CarriedOut) (account CarriedOut, outcome Outcome, err error) {
	defer func() {
		if !account.Carried && account.Problem != "" {
			clears := "the named gate being resolved; the next pull attempts the same preserved run"
			switch account.Gate {
			case runstate.TriageGateIntakeHold:
				clears = "`yoyo release` lifting the intake hold"
			case runstate.TriageGateSpendingPause:
				clears = "the spending pause being lifted"
			case runstate.TriageGateCapacity:
				clears = "a developer slot becoming free"
			}
			if noteErr := c.noteCheckStageWait(ctx, task.RunID, account.Problem, clears); noteErr != nil {
				account.RecordProblem = strings.TrimSpace(account.RecordProblem + " " + noteErr.Error())
			}
		}
	}()
	waiting := func(gate, what string) (CarriedOut, Outcome, error) {
		carried.Gate = gate
		carried.Waiting = true
		carried.Problem = fmt.Sprintf("the harness's continuation of the check stage the bound stopped on run %s is waiting on %s: %s; nothing was spent, and the next pull asks again", task.RunID, gate, what)
		return carried, Outcome{}, nil
	}
	if c.CheckStages == nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("nothing is wired to this harness to continue the check stage of run %s", task.RunID)
		return carried, Outcome{}, nil
	}
	hold, held, err := c.paused()
	if err != nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("the harness's continuation of the check stage of run %s was not attempted: %v", task.RunID, err)
		return carried, Outcome{}, nil
	}
	if held {
		return waiting(runstate.TriageGateSpendingPause, fmt.Sprintf("the operator has paused everything the harness spends, since %s", hold.HeldAt.UTC().Format(time.RFC3339)))
	}
	result, runErr := c.CheckStages.Continue(ctx, CheckStageContinueRequest{Run: task.RunID, AheadOf: aheadOfQueue(task)})
	carried.RecordProblem = result.RecordProblem
	switch {
	case result.IntakeHeld != nil:
		return waiting(runstate.TriageGateIntakeHold, fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)))
	case result.CapacityFull != nil:
		return waiting(runstate.TriageGateCapacity, fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit))
	case !result.Continued:
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("the harness did not continue the check stage the bound stopped on run %s: %s", task.RunID, strings.TrimSpace(refusalText(runErr)))
		return carried, Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	return carried, result.Outcome, runErr
}

func (c CarryOut) noteCheckStageWait(ctx context.Context, runID, why, clears string) error {
	if noter, ok := c.CheckStages.(checkStageWaitNoter); ok {
		return noter.NoteWaiting(ctx, runID, why, clears)
	}
	return nil
}

// continueStall continues a run the harness stopped for a silent provider
// stream. As with a check stage, nothing it meets is written onto the item's
// triage record, because nobody's decision is waiting on it: a wait is said on
// the pass and asked again at the next pull, and a refusal is written onto the
// run by the action itself, which hands the stoppage to the development manager.
// The operator's pause and the intake hold stop it exactly as they stop a
// recorded decision's carry-out.
func (c CarryOut) continueStall(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	waiting := func(gate, what string) (CarriedOut, Outcome, error) {
		carried.Gate = gate
		carried.Waiting = true
		carried.Problem = fmt.Sprintf("the harness's continuation of run %s after its stall is waiting on %s: %s; nothing was spent, and the next pull asks again", task.RunID, gate, what)
		return carried, Outcome{}, nil
	}
	if c.Stalls == nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("nothing is wired to this harness to continue run %s after its stall", task.RunID)
		return carried, Outcome{}, nil
	}
	hold, held, err := c.paused()
	if err != nil {
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("the harness's continuation of run %s after its stall was not attempted: %v", task.RunID, err)
		return carried, Outcome{}, nil
	}
	if held {
		return waiting(runstate.TriageGateSpendingPause, fmt.Sprintf("the operator has paused everything the harness spends, since %s", hold.HeldAt.UTC().Format(time.RFC3339)))
	}
	result, runErr := c.Stalls.Continue(ctx, StallContinueRequest{Run: task.RunID})
	carried.RecordProblem = result.RecordProblem
	switch {
	case result.IntakeHeld != nil:
		return waiting(runstate.TriageGateIntakeHold, fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)))
	case result.CapacityFull != nil:
		return waiting(runstate.TriageGateCapacity, fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit))
	case !result.Continued:
		carried.Gate = runstate.TriageGateHarness
		carried.Problem = fmt.Sprintf("the harness did not continue run %s after its stall: %s", task.RunID, strings.TrimSpace(refusalText(runErr)))
		return carried, Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	return carried, result.Outcome, runErr
}

// pausedGate names the pause a fresh run met, what lifts it, and whether it is
// one this decision waits on rather than one somebody has to open for it. Each of
// the four is lifted by a different person doing a different thing, so which one
// it was is the whole of what the finding is worth — and the four are not one kind
// of gate.
//
// The operator's pause and the intake hold stop everything the harness would do.
// They clear for every recorded decision at once, and asking again at the next
// pull starves nothing, because every decision behind this one is standing at the
// same switch. So they are the waiting kind, and the pass notices the moment
// either is lifted.
//
// A directive and a dependency stop this item and no other. Left as waiting they
// would be unpaced, and an unpaced refusal on a directive-paused item takes a
// developer slot on every poll for as long as the directive stands — which is
// the decided stoppages and queued work behind it crowded out by one that cannot
// fire. They are refusals somebody has to open, and
// they cool like every other one.
func pausedGate(outcome Outcome) (gate, clears string, waiting bool) {
	switch {
	case outcome.PausedByOperator != nil:
		return runstate.TriageGateSpendingPause, "`yoyo resume` lifting the pause; nothing was reserved, so the stoppage keeps its re-run", true
	case outcome.PausedByIntake != nil:
		return runstate.TriageGateIntakeHold, "`yoyo release` lifting the hold; nothing was reserved, so the stoppage keeps its re-run", true
	case outcome.PausedByDirective != nil:
		return runstate.TriageGateDirective, "the operator resolving the directive that pauses this item; nothing was reserved, so the stoppage keeps its re-run", false
	default:
		return runstate.TriageGateWorkItem, "the work this item waits on finishing; nothing was reserved, so the stoppage keeps its re-run", false
	}
}

// carryOutGate names which gate a refusal came from and what would clear it.
//
// It is read from the sentinels the actions already export rather than from the
// words of the refusal, for the reason every other classification in this package
// reads a record rather than prose: a gate matched on wording is one that changes
// when somebody rewrites a sentence, and this one is written into a durable record
// somebody acts on.
//
// Everything the sentinels do not cover is the harness's own records, and its
// refusal is carried verbatim rather than summarized. Those refusals already say
// what has to become true — this package's contribution would be a paraphrase of
// one, which is the last thing a finding should carry.
func carryOutGate(err error) (gate, clears string) {
	switch {
	case err == nil:
		// A carry-out that started nothing and refused nothing is a contradiction
		// rather than a state, and it is recorded as one: silence is what this
		// mechanism exists to end, so an ending nobody accounted for is said out loud.
		return runstate.TriageGateHarness, "somebody looking at the harness: it neither started the run nor said why"
	case carryOutCause(err) == triage.CarryOutWorktreeGone, carryOutCause(err) == triage.CarryOutBranchGone:
		return runstate.TriageGatePreservedWork, "the development manager recording a re-run from the target branch or escalating what became of the preserved change"
	case errors.Is(err, ErrRecordedBackendUnavailable):
		return runstate.TriageGateHarness,
			"the development manager recording a re-run, which starts the item again on the backend the developer is configured for now; a repair has to carry on the developer's session, and only the backend that opened it can. The refused repair spent nothing"
	case errors.Is(err, ErrWorktreeNotAsLeft), errors.Is(err, ErrPreservedChangeMissing):
		return runstate.TriageGatePreservedWork,
			"somebody saying what became of the worktree the stopped run preserved; what is in it is what a continued developer would be handed back, so this is a person's to look at"
	case errors.Is(err, ErrItemNotStartable):
		return runstate.TriageGateWorkItem,
			"the work item being put back in a state a run may start on; nothing was spent, so the same decision is carried out once it is"
	case errors.Is(err, runstate.ErrTriageCapReached):
		return runstate.TriageGateBudget,
			"`yoyo triage override` crossing the budget that refused, which is the operator's and nobody else's"
	case errors.Is(err, runstate.ErrRerunTaken):
		return runstate.TriageGateBudget,
			"the decision being recorded against the stoppage of the item's latest run instead — a run the docket never held, such as a re-run cancelled on its way out, is re-run from the target branch like any other: triage re-runs one stoppage once, so this one's re-run is spent, and a further attempt at it is an escalation rather than a larger budget"
	default:
		return runstate.TriageGateHarness, "what the refusal itself names"
	}
}

// refusalText is what a gate said, in words a record can carry. A refusal that
// arrived with no error at all still has to say something: a finding recording an
// empty refusal is the silence this exists to end, written down.
func refusalText(err error) string {
	if err == nil {
		return "the carry-out started nothing and gave no reason, which is a state the harness should not be able to reach"
	}
	return err.Error()
}

// undocketed is the finding for a decision recorded against a run the docket
// holds no stoppage of that the action could not carry out anyway, and whether
// err is that refusal at all.
//
// A re-run of such a run is carried out: it starts the item again from the
// target branch, claimed under the key the docket would have given the run. What
// is left refused is a repair, which re-enters a stopped run's own worktree and
// so needs a docketed stoppage, and a re-run of a run the harness holds no record
// of. Neither will clear on its own, so the finding names the decision that would
// apply instead. yoyodyne-ifd.187's re-run of run-04e578ce, a re-run the harness
// had cancelled on its way out and never docketed, was refused thirty-nine times
// over two days with "what the refusal itself names" as the only thing said about
// what would clear it
// (docs/diagnoses/yoyodyne-ifd-428-52-decision-on-an-undocketed-run.md).
func (c CarryOut) undocketed(task CarryOutTask, err error) (refusal, clears string, undocketed bool) {
	var missing NoDocketedStoppageError
	if !errors.As(err, &missing) {
		return "", "", false
	}
	refusal = fmt.Sprintf("run %s %s, and the triage docket holds no stoppage of it, so a %s recorded against it has nothing to start from: a %s acts on a stoppage, and recording a decision about a run does not docket it",
		task.RunID, c.howRunEnded(task.RunID), task.Decision, task.Decision)
	return refusal, c.applicableDecision(task), true
}

// howRunEnded says what the run's own record says became of it, which is what
// tells a reader why the docket never held it: a run cancelled or refused before
// it reached a stoppage is not one.
func (c CarryOut) howRunEnded(runID string) string {
	recorded, err := c.Runs.Recorded()
	if err != nil {
		return fmt.Sprintf("has a record this pass could not read (%v)", err)
	}
	for _, state := range recorded {
		if state.RunID != runID {
			continue
		}
		if state.CompletedAt != nil {
			return fmt.Sprintf("ended %s at %s", state.Status, state.CompletedAt.UTC().Format(time.RFC3339))
		}
		return fmt.Sprintf("stands %s", state.Status)
	}
	return "is not a run the harness holds a record of"
}

// applicableDecision names the decision the harness would carry out where the
// one recorded names a run it cannot act on. It names only decisions the
// development manager records and the harness then fires, so what the finding
// hands her is hers to decide and nobody else's to carry out.
func (c CarryOut) applicableDecision(task CarryOutTask) string {
	if task.Decision == runstate.TriageDecisionRepair {
		if recorded, err := c.Runs.Recorded(); err == nil {
			for _, state := range recorded {
				if state.RunID == task.RunID && state.WorkItemID != "" {
					return fmt.Sprintf("a re-run recorded against run %s instead, which the harness carries out by starting %s again from the target branch: a repair re-enters the worktree of a docketed stoppage, and the docket holds none of this run",
						task.RunID, task.WorkItemID)
				}
			}
		}
	}
	entries, err := c.Docket.List()
	if err != nil {
		return fmt.Sprintf("the decision recorded against a run of %s the harness holds a record of, which this pass could not list from the docket (%v)", task.WorkItemID, err)
	}
	claimed, err := c.Reruns.Claimed(task.WorkItemID)
	if err != nil {
		return fmt.Sprintf("the decision recorded against a run of %s the harness holds a record of, whose re-runs this pass could not read (%v)", task.WorkItemID, err)
	}
	taken := make(map[string]bool, len(claimed))
	for _, claim := range claimed {
		taken[claim.DocketKey] = true
	}
	var open []string
	for _, entry := range entries {
		if entry.WorkItemID != task.WorkItemID || entry.RunID == task.RunID {
			continue
		}
		stoppage := entry.Class == triage.ClassStoppedRun
		if task.Decision == runstate.TriageDecisionRerun {
			stoppage = stoppage || entry.Class == triage.ClassEscalation
			if taken[entry.Key] {
				continue
			}
		}
		if stoppage {
			open = append(open, fmt.Sprintf("run %s, docketed at %s", entry.RunID, entry.RecordedAt.UTC().Format(time.RFC3339)))
		}
	}
	if len(open) > 0 {
		return fmt.Sprintf("the %s recorded instead against a stoppage the docket holds for %s: %s. Recording it spends the item's budget as any decision does, and past a cap that is `yoyo triage override`'s to permit",
			task.Decision, task.WorkItemID, strings.Join(open, "; "))
	}
	return fmt.Sprintf("a re-run recorded instead against the latest run of %s the harness holds a record of, which it carries out by starting the item again from the target branch whether or not the docket holds that run",
		task.WorkItemID)
}

// stopped writes the finding for one attempt a gate stopped, and returns the
// account of it.
//
// The write is made under a context detached from the attempt's own, for the
// reason the stopped-work delivery detaches its records: a shutdown cancels the
// very context the attempt ran under, and it lands between the gate refusing and
// this write — which is the case a finding is most needed for and the one an
// attached context would lose.
func (c CarryOut) stopped(ctx context.Context, task CarryOutTask, carried CarriedOut, gate string, waiting bool, refusal, clears string) CarriedOut {
	carried.Gate = gate
	switch {
	case carried.ReleasedHold:
		clears += fmt.Sprintf("; this gate will not clear on its own, so no later pull retries this decision. Run %s left no branch or worktree, so a fresh run of %s would start from the target branch exactly as this re-run would have: the hold this decision placed on the item is released, and the next pull may start it like any other ready item",
			task.RunID, task.WorkItemID)
	case carried.Cause != "":
		clears += "; this gate will not clear on its own, so no later pull retries this decision. The development manager records a re-run or an escalation instead; a missing decision must be recorded again, with an override where the budget requires it"
	}
	carried.Waiting = waiting
	held := "was refused by"
	if waiting {
		held = "is waiting on"
	}
	carried.Problem = fmt.Sprintf("the %q the development manager decided about the stoppage of run %s %s %s: %s. What clears it: %s",
		task.Decision, task.RunID, held, gate, strings.TrimSpace(refusal), strings.TrimSpace(clears))
	if task.Harness {
		carried.Problem = fmt.Sprintf("the harness's own re-arm of the merge of run %s, withdrawn for its target's red check, %s %s: %s. What clears it: %s",
			task.RunID, held, gate, strings.TrimSpace(refusal), strings.TrimSpace(clears))
	}
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	_, err := c.Decisions.RecordCarryOutRefusal(write, task.WorkItemID, runstate.TriageCarryOut{
		Cause:     carried.Cause,
		DecidedAt: task.DecidedAt,
		RunID:     task.RunID,
		Decision:  task.Decision,
		Gate:      gate,
		Refusal:   refusal,
		Clears:    clears,
		Waiting:   waiting,
	}, c.now())
	if err != nil {
		carried.RecordProblem = fmt.Sprintf(
			"and the finding could not be written onto %s's triage record, so the docket the development manager reads does not carry it and this pass is the only thing that says it: %v",
			task.WorkItemID, err)
	}
	if err == nil && carried.Cause != "" && c.Notes != nil {
		if noteErr := c.deliverItemNotes(write, task.WorkItemID); noteErr != nil {
			carried.RecordProblem = fmt.Sprintf("the permanent refusal is on the triage record but could not be appended to the item's notes; its note remains pending for a later pull: %v", noteErr)
		}
	}
	return carried
}

// DeliverNotes retries tracker notes independently of the refused actions and
// accepted repair dispatches. It includes closed docket entries and completed
// repair runs, so finishing work or making a new decision loses no earlier note.
func (c CarryOut) DeliverNotes(ctx context.Context) error {
	var problems []error
	if repairs, delivers := c.Repairer.(interface{ DeliverNotes(context.Context) error }); delivers {
		if err := repairs.DeliverNotes(ctx); err != nil {
			problems = append(problems, err)
		}
	}
	if c.Notes == nil {
		return errors.Join(problems...)
	}
	entries, err := c.Docket.List()
	if err != nil {
		return errors.Join(append(problems, fmt.Errorf("read the docket for pending carry-out notes: %w", err))...)
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.WorkItemID == "" || seen[entry.WorkItemID] {
			continue
		}
		seen[entry.WorkItemID] = true
		if err := c.deliverItemNotes(ctx, entry.WorkItemID); err != nil {
			problems = append(problems, fmt.Errorf("deliver pending carry-out notes on %s: %w", entry.WorkItemID, err))
		}
	}
	return errors.Join(problems...)
}

func (c CarryOut) deliverItemNotes(ctx context.Context, workItemID string) error {
	return c.Decisions.DeliverCarryOutNotes(ctx, workItemID, c.now(), func(ctx context.Context, note string) error {
		item, err := c.Notes.Show(ctx, workItemID)
		if err != nil {
			return err
		}
		if strings.Contains(item.Notes, note) {
			return nil
		}
		_, err = c.Notes.RecordOutcome(ctx, workItemID, note)
		return err
	})
}

// itemsInFlight names the work items with a run going. A decision about an item
// something is already running is not one to act on: the item is not stopped work,
// and both actions refuse it anyway — asking here is what keeps the pass from
// spending a slot finding out. It names the run, so a decision held back for it
// is written down naming what it waits on.
func (c CarryOut) itemsInFlight() (map[string]string, error) {
	incomplete, err := c.Runs.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("read what is already in flight: %w", err)
	}
	busy := make(map[string]string, len(incomplete))
	for _, state := range incomplete {
		busy[state.WorkItemID] = state.RunID
	}
	return busy, nil
}

// paused reports the operator's pause over everything the harness spends. A pause
// that cannot be read stops the attempt rather than being spent through, exactly
// as it does everywhere else it is read.
func (c CarryOut) paused() (runstate.OperatorHold, bool, error) {
	if c.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := c.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (c CarryOut) validate() error {
	var problems []error
	if c.Docket == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the docket the decision was made against"))
	}
	if c.Decisions == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the item's durable triage record, which is what says the decision was made and where a refusal is recorded"))
	}
	if c.Reruns == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the re-runs already claimed, which is what says a decision has been acted on"))
	}
	if c.Runs == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the durable run state, because an item with a run in flight is not stopped work"))
	}
	return errors.Join(problems...)
}

func (c CarryOut) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Render describes what one attempt came to, for whoever asked. An attempt that
// fired says what it started; one a gate stopped says the gate and what clears it,
// which is the same sentence the item's own record now carries.
func (carried CarriedOut) Render() string {
	var rendered strings.Builder
	switch {
	case carried.Carried && carried.Decision == DecisionContinueChecks:
		fmt.Fprintf(&rendered, "continued the check stage the bound stopped on run %s of %s, at its checks on the change it already has\n",
			carried.RunID, carried.WorkItemID)
	case carried.Carried && carried.Decision == DecisionContinueStall:
		fmt.Fprintf(&rendered, "continued run %s of %s after the harness stopped its AI session for going silent or running out of its total budget, in its own session and at the phase it stopped in\n",
			carried.RunID, carried.WorkItemID)
	case carried.Carried:
		fmt.Fprintf(&rendered, "carried out the %q the development manager decided about %s, on the stopped work of run %s\n",
			carried.Decision, carried.WorkItemID, carried.RunID)
	default:
		fmt.Fprintf(&rendered, "%s\n", carried.Problem)
	}
	if carried.RecordProblem != "" {
		fmt.Fprintf(&rendered, "  %s\n", carried.RecordProblem)
	}
	return rendered.String()
}
