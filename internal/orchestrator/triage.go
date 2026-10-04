package orchestrator

// Docketing the work that has stopped moving.
//
// Two things stop work in a way no further attempt of the harness resolves. A
// run stops with its change still there — on a durable blocker the harness
// recorded, or by dying before anything could record one — and an approved
// change is published to a forge that then never merges it. The first is an
// event the harness is present for, so it is docketed where it happens. The
// second is the absence of an event, which nothing can be present for, so it is
// found by a scan: the reconciling sweep, and the build that runs when the
// development manager opens a conversation.
//
// The two shapes of the first are one class here because they are one fact to
// whoever reads them: the work is still there and nothing is going to pick it up.
// What separates them is only whether the harness got as far as saying so, and
// stoppedRun below says why that must not decide who hears about it.
//
// The scan is deliberately not a scheduled process, because there is none to
// hang it on. What that costs is stated rather than hidden: the configured
// stuck-merge age is a floor and not a promise. A publication becomes
// docketable at that age and is docketed the next time something scans, which
// on a quiet system is the next sweep or the next conversation.
//
// Both paths converge on one idempotent write keyed to the event, so a run that
// dockets its own stoppage and a sweep that settles the same run afterwards
// produce one entry between them rather than two accounts of one stoppage.
//
// A third thing stops work before it moves at all, and it is docketed here for
// the same reason as the other two rather than for a new one. An item whose own
// statement asks for something the tree does not have is work nothing is going
// to pick up either, and the only way anybody found that out was to spend a run
// on it — four times in a fortnight. RecordUnreadyItem is that finding made
// where it costs a read, and it is the one entry on this docket with no run
// behind it.
//
// A fourth stops before that: a dispatch that died before it could take its item.
// It is the one failure that leaves nothing at all — no blocker on the item, no
// worktree, no branch — so every rule above reads it as nothing having happened,
// and until RecordUnstartedRun existed it reached no surface anybody looks at.
// That is how one item was dispatched twenty-nine times in twenty hours, dying at
// the claim each time, with the harness's next-mover line saying only that
// nothing was recorded for anybody to decide.
//
// A fifth is not a thing that stopped but somebody saying it should. A developer
// or a reviewer that finds the work item unmeetable as written escalates in the
// round it reached, and RecordEscalation dockets that judgement the moment the
// run ends on it. It is docketed rather than only recorded because what it needs
// is the same thing the other three need — the development manager deciding — and
// because the alternative it replaces is the expensive one: a role with no cheap
// way to say "this cannot work" spends repair rounds against a wall, or spends
// the item's whole budget, before the stoppage reaches her at all.
//
// # What takes an entry off again
//
// A triage decision does, by closing it: the docket's lifecycle is
// create-on-death and close-on-decision. Nothing used to close one, and because
// the docket is rebuilt from durable records at every scan, a stoppage decided
// last week came back on every docket after it — three of the six decisions that
// settle a stoppage spend no counter, so nothing the harness reads could tell a settled stoppage from a
// fresh one, and the only guard against deciding it twice was prose telling the
// development manager to go and read the item's notes. The listing she is given
// is bounded, so the settled ones crowded out the ones nobody had looked at.
//
// A closure is joined where the docket is read and the entry stays on the log,
// which is what keeps the two halves from fighting: the scan goes on finding the
// same stoppage in the same records and goes on recording nothing, because the
// key is already there.
//
// Its item closing does too. An entry asks something about a work item, and a
// closed or retired item asks nobody anything, so SettleClosedItems closes the
// entries standing for one: where the item is closed, and on every reconcile
// sweep over the tracker's closed items, which catches whatever closed it
// elsewhere. An unfinished publication is the exception, because it asks about a
// merge the forge holds rather than about the item, which closes on integration.
//
// # What puts one back
//
// Two things, and neither is the scan changing its mind. The same work stopping
// again is the first: a key names a run rather than one moment of it, a repair
// continues the run that stopped, and a run that dies again after being repaired
// derives the key its settled entry carries — so a stoppage that happened after
// the decision about the last one is docketed, and one nothing has happened to
// since is not. The run's own ending is what that is measured by rather than this
// build's clock, because every scan re-derives the same stoppages and only the
// run says whether anything has happened.
//
// The second is a decision that only held for a while. Waiting says the forge
// still has the merge, and nothing about a merge that is not happening ever
// changes, so a wait that settled the entry for good would be a stuck publication
// disappearing on the strength of a decision to look at it again. It comes back
// when the decision lapses, carrying what was decided, and nothing is docketed
// twice for it.
//
// # One live entry per stopped run
//
// A key names a class of event as well as the run, so one run can stand on the
// log under more than one: it stops on a blocker, and a later scan finds the
// publication it left sitting unmerged behind the same stoppage. Those are one
// question, and on 2026-09-25 they were listed as several — yoyodyne-ifd.362 six
// times. So the docket a build hands over folds every open entry of one run into
// the one recorded last, with the rest beneath it, and a decision about that
// entry closes all of them (cli's conversationDocketLog). The log is not
// rewritten; the fold is a join, like the closure.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Docket is the durable docket entries are recorded into and read back from.
// It is satisfied by runstate.DocketStore.
//
// Close is here for the closures the harness makes rather than a role: a
// publication entry whose publication a later sweep finished, an unready item
// the pull found ready, and an entry whose item was closed. Every other closure
// is a triage decision, recorded in the conversation that made it.
type Docket interface {
	RecordOnce(entry triage.Entry) (bool, error)
	List() ([]triage.Entry, error)
	Close(closure triage.Closure) (bool, error)
}

// DocketRuns is the run evidence a docket is built from: every record the
// harness holds. Reading them decides nothing about any run, which is what lets
// a build run beside whatever else is happening.
type DocketRuns interface {
	Recorded() ([]runstate.State, error)
}

// DocketDecisions is the durable per-item record of what triage has decided and
// what the item has cost, which is the record the triage guards spend and refuse
// against. The docket reads it rather than counting the same things again from
// the runs: a second count is a second answer, and an entry that reports one
// while the guard enforces the other is a decision made against numbers that do
// not exist.
//
// It is satisfied by runstate.TriageStore.
type DocketDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// DocketReruns is what the harness has carried out of those decisions: the
// re-runs claimed for one work item, one per docketed stoppage. It is the other
// half of the re-run gate — a decision authorizes one re-run and a claim is what
// says it was acted on — so an entry that showed the decisions without the claims
// would say a stoppage may be run again where the guard refuses it.
//
// It is satisfied by runstate.RerunStore.
type DocketReruns interface {
	Claimed(workItemID string) ([]runstate.Rerun, error)
}

// DocketStops is the stops asked of runs, which a stopped run's entry carries
// where one was asked and the run stopped for some other reason before reading
// it. It is satisfied by *runstate.Store.
type DocketStops interface {
	StopRequested(runID string) (runstate.StopRequest, bool, error)
}

// Docketer makes and reads the triage docket. It has no tracker, no worktree
// access, and no forge access, and that is the point: docketing is a statement
// that work stopped, assembled from evidence somebody already recorded. What to
// do about a docketed entry is the development manager's, and nothing here can
// claim, repair, escalate, or retire anything.
type Docketer struct {
	Docket Docket
	Runs   DocketRuns
	// Decisions is the item's durable triage record. Required: every entry
	// reports what triage has already decided about its item, and an entry built
	// without that record would report an item nobody had decided anything about,
	// which is indistinguishable from one whose recovery is already authorized.
	Decisions DocketDecisions
	// Reruns is what has been carried out of those decisions. Required to read the
	// docket, for the same reason: a stoppage whose re-run has been claimed and
	// one whose decision is still waiting are opposite answers to the question the
	// development manager is about to ask.
	Reruns DocketReruns
	// Stops is where a stop asked of a run is read, so a run that stopped for
	// another reason after one was asked says so on its entry. Optional: a
	// docketer with none dockets such a stoppage without the stop beside it, which
	// is still an undecided stoppage, only one missing half of its account.
	Stops DocketStops
	// Caps are the ceilings the guards refuse against, as the caller assembled
	// them for every other reader of the same record. They are reported beside
	// what has been spent, because a count with no ceiling beside it says nothing
	// about whether the next decision will be refused.
	Caps runstate.TriageCaps
	// Triage is what the docket measures against: the age past which an unmerged
	// publication is stuck, and the budgets every entry reports beside what the
	// item has already spent.
	Triage config.Triage
	// ProductID is which product an entry belongs to, and is required only by the
	// one entry that is not made from a run record. Every other entry takes it
	// from the run, which is the more reliable source and stays the source: this
	// is here because an item dispatch declined to start has no run to take it
	// from, not because the product is a thing this decides.
	ProductID domain.ProductID
	// Remains is the repository an entry asks what the stopped run left, as the
	// entry is written and again every time the docket is built for somebody to
	// read. Nil answers from the run's own record, and the entry says nothing
	// looked rather than passing the record off as a check.
	Remains readmodel.Remains
	// Reports is where an item the tree is not ready for is also said to the
	// product manager, as a report her conversation is given. Optional: a
	// docketer with none dockets exactly as before, and the refusal reaches her
	// only through whoever reads the development manager's docket. RepositoryID
	// and Harness — the revision this binary was built from — are what that
	// report is attributed with, as every report is.
	Reports      ReportCollector
	RepositoryID string
	Harness      string
	Clock        execution.Clock
}

// DocketBuild is what one build found: the docket as it now stands, and how
// many entries this build is what created. The count is reported rather than
// the entries themselves because a build is not a notification — an entry
// created by this build and one created by last week's sweep are the same
// standing fact to whoever reads the docket.
type DocketBuild struct {
	// Entries are the stoppages nobody has decided about, which is what a docket
	// is for. An entry a triage decision closed is not among them — unless the
	// harness has since tried to carry that decision out and a gate stopped it,
	// which puts the entry back carrying both the decision and the gate; see
	// openDocket.
	Entries []triage.Entry `json:"entries"`
	// Waiting are the stoppages a decision to wait still holds over, folded one per
	// run as Entries are. They are closed and are not among Entries, which is what
	// every reader that counts open questions reads; they are carried so the
	// development manager's docket can list them after everything else rather
	// than lose sight of them until the wait runs out (triage.Stoppage.Waiting).
	// A run docketed again since the wait is a question again, and is in Entries
	// instead.
	Waiting []triage.Entry `json:"waiting,omitempty"`
	Added   int            `json:"added"`
	// Closed is how many of the docket's entries have been decided and are
	// therefore not among Entries; the waits in Waiting are counted in it. It is reported rather than dropped because a docket
	// that silently shows a subset is one a reader takes for the whole: the number
	// says the rest were settled rather than never noticed.
	Closed int `json:"closed"`
	// Folded is how many open entries are listed beneath a later docketing of the
	// same run rather than beside it, which is what one live entry per stopped run
	// comes to: they are still open, and a decision about the entry above them
	// settles them with it.
	Folded int `json:"folded,omitempty"`
}

// Listed is what the development manager's docket is rendered from: the open
// stoppages and, after them, the ones she is waiting on. triage.Live tells the
// two apart by the decision each carries, so they are handed over as one list.
func (b DocketBuild) Listed() []triage.Entry {
	if len(b.Waiting) == 0 {
		return b.Entries
	}
	return append(slices.Clone(b.Entries), b.Waiting...)
}

// Build scans every recorded run, dockets what has stopped and is not docketed
// yet, and returns the stoppages nobody has decided about. It is safe to repeat
// and safe to run concurrently with anything: every write is keyed to the event
// it describes, so a build that races another build records the same entries and
// the docket collapses them.
//
// A run whose record cannot supply an entry is skipped rather than failing the
// build: a docket that refuses to be read because one run is odd is a docket
// nobody sees, and the runs beside it are exactly the ones somebody needs.
//
// What comes back is joined to the triage record as it stands now, not as it
// stood when each entry was written. An entry is recorded once, as the work
// stops, and every decision about it is made afterwards — so a build that
// returned the entries as recorded would show every decision as absent, which is
// what let one authorized re-run be about to be decided a second time.
func (d Docketer) Build() (DocketBuild, error) {
	if err := d.validate(); err != nil {
		return DocketBuild{}, err
	}
	if d.Reruns == nil {
		return DocketBuild{}, errors.New("reading the docket requires the re-runs already carried out, so an entry never shows a stoppage as re-runnable that the harness would refuse")
	}
	recorded, err := d.Runs.Recorded()
	if err != nil {
		return DocketBuild{}, fmt.Errorf("read the recorded runs to build the triage docket: %w", err)
	}
	// What is already docketed is read once rather than once per candidate. The
	// store refuses a repeated key anyway; this is what stops a build over a long
	// history from re-reading the whole docket for every run in it.
	docketed, err := d.Docket.List()
	if err != nil {
		return DocketBuild{}, fmt.Errorf("read the triage docket: %w", err)
	}
	already := docketStanding(docketed)
	now := d.now()
	added := 0
	var problems []error
	for _, state := range recorded {
		entries, err := d.entriesFor(state, now, already)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, entry := range entries {
			created, err := d.Docket.RecordOnce(entry)
			if err != nil {
				problems = append(problems, fmt.Errorf("docket %s of run %s: %w", entry.Class, state.RunID, err))
				continue
			}
			if created {
				added++
			}
		}
	}
	byID := make(map[string]runstate.State, len(recorded))
	for _, state := range recorded {
		byID[state.RunID] = state
	}
	// A product decision whose run has ended with nothing else docketed about it
	// has no run left to stop, so it is settled before the docket is read for
	// somebody to decide from. See productdecision.go.
	if _, err := d.settleEndedProductDecisions(byID, now); err != nil {
		problems = append(problems, err)
	}
	entries, err := d.Docket.List()
	if err != nil {
		problems = append(problems, fmt.Errorf("read the triage docket: %w", err))
		return DocketBuild{Added: added}, errors.Join(problems...)
	}
	// A decided entry leaves the docket here rather than in each reader of it. The
	// entry stays on the log, which is what stops the same stoppage being docketed
	// again from the same durable records the next time anything scans; what a
	// decision ends is its being a question, and this is where the questions are
	// handed over.
	//
	// The record is joined before the questions are separated, because one kind
	// of settled entry is a question again: a decision the harness carries out
	// itself, tried, and stopped by a gate. Which entries those are is on the
	// item's record rather than on the entry, so the join has to be read to find
	// them. It is read for the settled entries whose decision the harness carries
	// out and for no other settled entry, since a re-scope, a wait, and an
	// escalation are never attempted and have nothing to be stopped by.
	listable, waiting, unlisted := listableDocket(entries, now)
	problems = append(problems, d.joinDecisions(listable, docketedRunsOf(entries), publicationsOf(recorded), byID)...)
	open, closed := openDocket(listable, now)
	// One live entry per stopped run. The repeats are folded here, where every
	// docket anybody reads is built, rather than rewritten on the log — so the
	// sweep folds the ones already standing the first time it builds, and a run
	// docketed again tomorrow folds the same way.
	live := triage.Fold(open)
	d.lookAgain(live, recorded)
	// Where a run a product decision is about stands now, which is what she
	// decides between stopping it and letting it finish from.
	joinProductDecisions(live, byID, now)
	return DocketBuild{Entries: live, Waiting: waitingDocket(waiting, live), Added: added, Closed: closed + unlisted, Folded: len(open) - len(live)}, errors.Join(problems...)
}

// lookAgain puts what the repository holds now onto every open entry whose run
// left a branch or a worktree. An entry is written once, as the work stops, and
// read by the development manager hours or days later; what it said about the
// branch then is not what she decides on, so the build she reads is the moment
// it is looked for again. Nothing is written back: the entry on the log keeps
// what was found when it was recorded, and the reading carries what is there.
func (d Docketer) lookAgain(entries []triage.Entry, recorded []runstate.State) {
	byID := make(map[string]runstate.State, len(recorded))
	for _, state := range recorded {
		byID[state.RunID] = state
	}
	for index := range entries {
		d.lookAt(&entries[index], byID)
		// What was folded beneath the entry is decided from too, so it is looked at
		// again as well.
		for earlier := range entries[index].Earlier {
			d.lookAt(&entries[index].Earlier[earlier], byID)
		}
	}
}

func (d Docketer) lookAt(entry *triage.Entry, byID map[string]runstate.State) {
	if entry.Artifacts.Branch == "" && entry.Artifacts.WorktreePath == "" {
		return
	}
	state, known := byID[entry.RunID]
	if !known {
		return
	}
	found := d.look(state)
	entry.Artifacts.Found = &found
	if entry.Class == triage.ClassStoppedRun {
		entry.HarnessContinuesChecks = state.HarnessContinuesCheckStage()
		entry.CheckStageStop = singleLine(state.CheckStageStopSays(), triage.MaxMessageBytes)
	}
}

// look is what the repository holds of one run's change, asked now.
func (d Docketer) look(state runstate.State) triage.Found {
	return readmodel.Looking(context.Background(), d.Remains, d.now)(state)
}

// listableDocket is every entry the docket might list, and how many it will
// not whatever the record says: the entries nobody has decided about, and the
// settled ones whose decision is one the harness carries out — which are listed
// again only where the harness has tried and a gate stopped it, and that is
// read off the item's record by the join. The rest were settled by a decision
// nothing attempts, so nothing about them changes after the closure. Of those,
// the ones a wait holds over are handed back apart, still counted as unlisted.
func listableDocket(entries []triage.Entry, now time.Time) (listable, waiting []triage.Entry, unlisted int) {
	listable = make([]triage.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Closed != nil && entry.Closed.Holds(now) && !harnessCarriesOut(entry.Closed.Decision) {
			unlisted++
			if entry.WaitStands(now) {
				waiting = append(waiting, entry)
			}
			continue
		}
		listable = append(listable, entry)
	}
	return listable, waiting, unlisted
}

// waitingDocket is the waited stoppages folded one per run, less any run that
// has an open entry: a run docketed again since somebody decided to wait on it
// is a question again, and the open entry speaks for it.
func waitingDocket(waiting, open []triage.Entry) []triage.Entry {
	if len(waiting) == 0 {
		return nil
	}
	asked := make(map[string]bool, len(open))
	for _, entry := range open {
		asked[strings.TrimSpace(entry.RunID)] = true
	}
	kept := make([]triage.Entry, 0, len(waiting))
	for _, entry := range waiting {
		if run := strings.TrimSpace(entry.RunID); run == "" || !asked[run] {
			kept = append(kept, entry)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return triage.Fold(kept)
}

// harnessCarriesOut reports a decision the scheduling pass fires itself: the two
// that ask for a run, and a re-arm, which the watch has made as a merge request
// since yoyodyne-ifd.429.31 and yoyodyne-ifd.428.46. The other three ask for
// nothing.
//
// A re-arm was left out after the watch began carrying it out, so every refusal
// of one was written onto the item's triage record and then dropped here with
// its settled entry: the re-arm of the maintenance-duties item's merge
// (yoyodyne-ifd.434.10) was refused fifty-five times on 2026-09-28 and 29 and the
// development manager was shown none of them (yoyodyne-edi).
func harnessCarriesOut(decision string) bool {
	switch strings.TrimSpace(decision) {
	case runstate.TriageDecisionRepair, runstate.TriageDecisionRerun, runstate.TriageDecisionRearm:
		return true
	default:
		return false
	}
}

// openDocket separates the stoppages nobody has decided about from the ones a
// standing decision settled, and reports how many were settled.
//
// A decision that has lapsed leaves its entry open, which is what waiting means:
// the forge still had the merge when somebody looked, and the entry is a
// question again once it has been sitting there as long as it took to become one
// in the first place. The entry still carries what was decided, so the reader
// who gets it back is told they have seen it before.
//
// So does a decision the harness tried to carry out and a gate stopped, for the
// reason the whole carry-out exists: a decision recorded and never fired, with
// nothing anywhere saying why, is the thirty-three-item silence of 2026-09-07.
// The entry comes back carrying the decision and the gate, so the development
// manager reads it as a decision she made that is not happening — and what it
// asks of her is the gate, since deciding the same stoppage again is what the
// budgets refuse. A gate that clears on its own is listed too, worded as
// waiting: a decision waiting on the operator's hold is still a decision that is
// not happening, and the docket saying so is what the operator's own reading of
// it is built on.
func openDocket(entries []triage.Entry, now time.Time) ([]triage.Entry, int) {
	open := make([]triage.Entry, 0, len(entries))
	closed := 0
	for _, entry := range entries {
		if !entry.Undecided(now) {
			closed++
			continue
		}
		open = append(open, entry)
	}
	if len(open) == 0 {
		// Nothing open reads as nothing to decide wherever a docket is rendered, and
		// an empty slice and no slice at all must not be two different answers to it.
		return nil, closed
	}
	return open, closed
}

// standingDocket is what the docket already holds for each key: an entry nobody
// has decided about, or the decision that settled the one it holds.
//
// The two are kept apart because they answer differently. A key with an entry
// standing is one this build leaves alone. A key whose entry was settled is one
// this build dockets again only if the work stopped again since the decision —
// re-deriving the same settled stoppage is exactly the phantom that closing the
// entry was for, and refusing every later stoppage under that key is how a fresh
// death on a repaired run reaches nobody.
// A key with no entry is absent from it; a key with one maps to the decision
// that settled that entry, or to nothing where nobody has decided about it.
type standingDocket map[string]*triage.Closure

func docketStanding(entries []triage.Entry) standingDocket {
	standing := make(standingDocket, len(entries))
	for _, entry := range entries {
		standing[entry.Key] = entry.Closed
	}
	return standing
}

// dockets reports a stoppage this build records under one key: one the docket
// holds no entry for, or one whose entry was decided before the work stopped
// again. The stoppage's own moment is what decides it rather than this build's,
// because every scan re-derives the same stoppages from the same records and it
// is the run that says whether anything has happened since.
func (s standingDocket) dockets(key string, stoppedAt time.Time) bool {
	decided, docketed := s[key]
	if !docketed {
		return true
	}
	if decided == nil {
		return false
	}
	return stoppedAt.After(decided.ClosedAt)
}

// joinDecisions puts the triage record as it now stands onto every entry: what
// has been decided about the item, what the caps will refuse the next one
// against, and what has already been claimed against this entry's own stoppage.
//
// The record is read once per work item rather than once per entry, because one
// item's stoppages share one record and a docket over a long history would
// otherwise read the same file repeatedly.
//
// An item whose record cannot be read says so on its entries and is reported as
// a problem, and its counters are left as the entry recorded them rather than
// blanked: a zero nobody could distinguish from a fresh item is the reading that
// spends a decision twice.
//
// published is what each run's record says about the publication it made, which
// is where the re-arms the harness has actually repeated are written. It is
// indexed once for the same reason the item records are read once: a docket over
// a long history would otherwise walk the runs per entry.
//
// docketedRuns is every run the whole docket holds an entry for, which is not
// the same as the runs of the entries handed here: those are the listable ones,
// and a run whose only entry was left out of them is still a run the docket
// holds, so a finding about it must not be shown as though it held none.
func (d Docketer) joinDecisions(entries []triage.Entry, docketedRuns map[string]bool, published map[string]publicationRearms, runs map[string]runstate.State) []error {
	var problems []error
	read := make(map[string]itemDecisions, len(entries))
	for index := range entries {
		entry := &entries[index]
		decisions, seen := read[entry.WorkItemID]
		if !seen {
			decisions = d.decisionsFor(entry.WorkItemID)
			read[entry.WorkItemID] = decisions
			if decisions.problem != nil {
				problems = append(problems, decisions.problem)
			}
		}
		if decisions.problem != nil {
			entry.CountersProblem = decisions.problem.Error()
			continue
		}
		// Whatever a previous reader could not do is not this reader's answer: the
		// record was read, so the entry carries what it says and nothing about the
		// reading of it.
		entry.CountersProblem = ""
		// The repair attempts stay as the entry recorded them: they are what the
		// stopped run spent of its own budget, which is evidence about that run
		// rather than a figure any guard reads.
		// A publication entry's re-arm figures are its own publication's; a stopped
		// run is about none, and the zero value says exactly that.
		publication := publicationRearms{}
		if entry.Class == triage.ClassPublication {
			publication = published[entry.RunID]
		}
		// The run's own record is read beside the ledger, because it is what says a
		// granted repair was handed back (TriageCounters.StandingOf); a run the
		// listing does not hold is asked of the ledger alone.
		run, recorded := runs[entry.RunID]
		if !recorded {
			run = runstate.State{RunID: entry.RunID}
		}
		entry.Counters = d.counters(decisions.counters, run, entry.Counters.RepairAttempts, len(decisions.claimed), publication)
		entry.Rerun = rerunOf(*entry, decisions.claimed)
		// Joined here and never written, exactly as the re-run above is: an override
		// answers the escalation this entry produced, so it is always made after the
		// entry exists.
		entry.Overrides = docketedOverrides(decisions.counters.Overrides)
		// And what became of the harness's own attempt to carry this entry's
		// decision out, where a gate stopped it. Joined here for the sharpest
		// version of the reason the two above are: the attempt is made after the
		// decision, which is made after the entry, so one frozen into the entry
		// could only ever be absent — and an absent one reads as a decision the
		// harness is about to act on, which is precisely what a refused carry-out
		// is not.
		entry.CarryOut = docketedCarryOut(*entry, decisions.counters)
		// A finding about the item's latest decision is shown on the item's entries
		// where that decision names a run this docket holds no entry for, because
		// otherwise nothing she reads would carry it: yoyodyne-ifd.187's re-run of
		// an undocketed run was refused thirty-nine times onto the item's record
		// while every entry she was shown said nothing (yoyodyne-ifd.428.52).
		if latest := undocketedCarryOut(*entry, decisions.counters, docketedRuns); latest != nil &&
			(entry.CarryOut == nil || entry.CarryOut.RefusedAt.Before(latest.RefusedAt)) {
			entry.CarryOut = latest
		}
	}
	return problems
}

// docketedRunsOf is every run the docket holds an entry for, of any class.
func docketedRunsOf(entries []triage.Entry) map[string]bool {
	runs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		runs[entry.RunID] = true
	}
	return runs
}

// undocketedCarryOut is the carry-out finding about the item's latest decision
// where that decision names a run the docket holds no entry for, in the shape an
// entry of the same item carries it. It is taken only while the decision is the
// item's latest and only where the finding was written since the decision, so a
// finding about a decision she has since decided past is never shown as though
// it stood, and it names its run so it is not read as a finding about the
// entry's own.
func undocketedCarryOut(entry triage.Entry, counters runstate.TriageCounters, docketedRuns map[string]bool) *triage.CarryOut {
	latest, found := counters.LatestDecision()
	if !found || docketedRuns[latest.RunID] {
		return nil
	}
	recorded, found := counters.CarryOutOf(latest.RunID)
	if !found || !recorded.AboutDecision(latest.Decision, latest.DecidedAt) || recorded.RefusedAt.Before(entry.RecordedAt) {
		return nil
	}
	return &triage.CarryOut{
		RunID:       latest.RunID,
		Cause:       recorded.Cause,
		Decision:    recorded.Decision,
		Gate:        recorded.Gate,
		Refusal:     recorded.Refusal,
		Clears:      recorded.Clears,
		Waiting:     recorded.Waiting,
		Attempts:    recorded.Attempts,
		RefusedAt:   recorded.RefusedAt,
		Unattempted: recorded.Unattempted,
	}
}

// docketedCarryOut is the carry-out finding standing about one entry's own
// stoppage, in the shape the entry carries it. It is matched on the run rather
// than on the item, because an item with several stoppages has a decision and an
// attempt for each of them and a finding shown against the wrong one is a finding
// about a change the reader cannot see.
//
// A finding older than the entry is about a stoppage this one replaced: a
// repaired run that died again is docketed afresh under the same run, and what
// the harness was stopped doing about the last stoppage says nothing about this
// one.
func docketedCarryOut(entry triage.Entry, counters runstate.TriageCounters) *triage.CarryOut {
	recorded, found := counters.CarryOutOf(entry.RunID)
	if !found || recorded.RefusedAt.Before(entry.RecordedAt) {
		return nil
	}
	if decision, decided := counters.DecisionOf(entry.RunID); decided && !recorded.AboutDecision(decision.Decision, decision.DecidedAt) {
		return nil
	}
	return &triage.CarryOut{
		Cause:       recorded.Cause,
		Decision:    recorded.Decision,
		Gate:        recorded.Gate,
		Refusal:     recorded.Refusal,
		Clears:      recorded.Clears,
		Waiting:     recorded.Waiting,
		Attempts:    recorded.Attempts,
		RefusedAt:   recorded.RefusedAt,
		Unattempted: recorded.Unattempted,
	}
}

// itemDecisions is one work item's triage record as the guards read it: what has
// been decided, what has been carried out, and what stopped either being read.
type itemDecisions struct {
	counters runstate.TriageCounters
	claimed  []runstate.Rerun
	problem  error
}

func (d Docketer) decisionsFor(workItemID string) itemDecisions {
	counters, err := d.Decisions.Counters(workItemID)
	if err != nil {
		return itemDecisions{problem: fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)}
	}
	claimed, err := d.Reruns.Claimed(workItemID)
	if err != nil {
		return itemDecisions{problem: fmt.Errorf("read the re-runs already carried out for %s: %w", workItemID, err)}
	}
	return itemDecisions{counters: counters, claimed: claimed}
}

// rerunOf is the re-run claimed against one entry's own stoppage, which is what
// the once-per-stoppage guard refuses a second of. It is matched on the docket
// key rather than on the run or the item, because the key is what the claim was
// taken under.
func rerunOf(entry triage.Entry, claimed []runstate.Rerun) *triage.Rerun {
	for _, existing := range claimed {
		if existing.DocketKey == entry.Key {
			return &triage.Rerun{ClaimedAt: existing.ClaimedAt, RunID: existing.RunID}
		}
	}
	return nil
}

// RecordStoppedRun dockets one run that stopped with its change still there, at
// the moment it ended. It reports whether this call is what created the entry, so
// a caller can tell docketing a stoppage from finding it already docketed.
//
// It is the wider of the two questions asked in this file. A run that ended on a
// durable blocker is docketed here and re-derived by the scan; a run that died
// before anything could record one is docketed here and nowhere else, for the
// reason preservedDeath states. This is the only place both are asked, and it is
// called wherever a run becomes terminal — by the pipeline as it fails one, and
// by the sweep as it settles one — which is what makes "at the moment it ended"
// true of either.
//
// A run that neither dockets anything and is not an error: that is every run that
// ended for a reason nobody has to decide about, which is most of them.
func (d Docketer) RecordStoppedRun(state runstate.State) (bool, error) {
	if err := d.validate(); err != nil {
		return false, err
	}
	found := d.look(state)
	if !stoppedRun(state) && !diedHolding(state, found) && !stageBoundHolding(state, found) {
		return false, nil
	}
	entry, err := d.stoppedRunEntry(state, d.now(), found)
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// RecordDecidedStop dockets a run the development manager decided to stop in
// flight, and closes the entry with her decision in the same call.
//
// A run stopped this way ends cancelled with no blocker, which is a shape the
// docket otherwise passes over — an operator's stop hands nobody a decision. This
// one is a stoppage somebody decided, and the change it leaves on its branch is
// still hers to account for, so it is put on the docket where she and anybody
// reading after her find it — as a settled entry, carrying who stopped it and
// why, rather than as a question put back to the person who already answered
// it. The failure the run ended on is carried on the entry, because it is the
// whole of what says why this run is not going on.
func (d Docketer) RecordDecidedStop(state runstate.State, request runstate.StopRequest) error {
	if err := d.validate(); err != nil {
		return err
	}
	// The entry and its closure carry one moment. A closure dated before the
	// entry it settles reads as a decision about some earlier stoppage, and the
	// decision itself was made before the run stopped, so its own time cannot be
	// the closure's: when she decided is on the item's triage record and in the
	// stop request, and this is when the docket took the decided stoppage in.
	at := d.now()
	found := d.look(state)
	entry, err := d.stoppedRunEntryCarrying(state, at, found, runstate.RecordFailure(state.Failure))
	if err != nil {
		return err
	}
	// The stop that ended the run, with the item doing its work instead where she
	// named one: that is what says the item is superseded and by what, which is
	// why the pull holds it while this run's change is preserved.
	entry.StopRequested = &triage.RequestedStop{
		By:           singleLine(request.StoppedBy(), triage.MaxMessageBytes),
		At:           request.RequestedAt.UTC(),
		Reason:       singleLine(strings.TrimSpace(request.Reason), triage.MaxMessageBytes),
		Decision:     strings.TrimSpace(request.Decision),
		Landed:       true,
		SupersededBy: d.supersededBy(state),
	}
	if _, err := d.Docket.RecordOnce(entry); err != nil {
		return err
	}
	_, err = d.Docket.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entry.Key,
		ProductID:     entry.ProductID,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		Decision:      strings.TrimSpace(request.Decision),
		Reason:        singleLine(strings.TrimSpace(request.Reason), triage.MaxMessageBytes),
		DecidedBy:     request.StoppedBy(),
		ClosedAt:      at.UTC(),
	})
	return err
}

// supersededBy is the item a stop decision about this run named as doing its
// work instead, read off the item's triage record, and empty where there is no
// such decision or the record cannot be read: the stop is docketed either way,
// and the reason it carries still names the superseding item where she gave one.
func (d Docketer) supersededBy(state runstate.State) string {
	counters, err := d.Decisions.Counters(state.WorkItemID)
	if err != nil {
		return ""
	}
	decision, decided := counters.DecisionOf(state.RunID)
	if !decided || decision.Decision != runstate.TriageDecisionStop {
		return ""
	}
	return strings.TrimSpace(decision.SupersededBy)
}

// settledPublicationDecision is the word a closure the harness makes carries, so
// a reader of a closed publication entry can tell a stoppage that stopped being
// one from a stoppage somebody decided about.
const settledPublicationDecision = "settled"

// SettlePublication closes the docket entries a run's publication had open, once
// a sweep has finished that publication. It reports how many it closed.
//
// This is the one closure the harness makes rather than a role, and it is not a
// decision: a publication entry is the absence of an event, and the merge being
// confirmed on the remote is the event. Left open, the entry would say a
// publication needs a person while the record beside it says nothing about it
// is outstanding — and a docket rebuilt from the records would never re-derive
// it, so it would stand there until somebody decided a question that had already
// been answered. Eight did, after PR 497 merged ten held requests on
// 2026-09-13.
//
// The stopped-run entry is closed with it only where the settlement cleared the
// run's blocker, which it does for a blocker about this publication and for
// nothing else: a run that stopped on something other than its publication is
// still stopped.
func (d Docketer) SettlePublication(state runstate.State, reason string) (int, error) {
	if err := d.validate(); err != nil {
		return 0, err
	}
	if state.PullRequest == nil {
		return 0, nil
	}
	entries, err := d.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to settle the publication of run %s: %w", state.RunID, err)
	}
	classes := []triage.Class{triage.ClassPublication}
	if strings.TrimSpace(state.Blocker) == "" {
		classes = append(classes, triage.ClassStoppedRun)
	}
	now := d.now().UTC()
	closed := 0
	var problems []error
	for _, entry := range entries {
		if entry.RunID != state.RunID || !slices.Contains(classes, entry.Class) {
			continue
		}
		if entry.Closed != nil && entry.Closed.Holds(now) {
			continue
		}
		took, err := d.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			RunID:         entry.RunID,
			WorkItemID:    entry.WorkItemID,
			Decision:      settledPublicationDecision,
			Reason:        reason,
			DecidedBy:     "the harness, settling the publication in a reconcile sweep",
			ClosedAt:      now,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("close the %s entry of run %s: %w", entry.Class, entry.RunID, err))
			continue
		}
		if took {
			closed++
		}
	}
	return closed, errors.Join(problems...)
}

// RecordUnstartedRun dockets one run that died before it claimed its work item,
// at the moment it ended. It reports whether this call is what created the entry,
// so a caller can tell docketing a death from finding it already docketed.
//
// It is separate from RecordStoppedRun because the two describe opposite
// situations. A stopped run left a change on a branch and holds its item; this
// one took nothing, cut nothing, and left the item exactly as it found it, so
// what a development manager decides about it is about the dispatch. Deciding
// them from one entry would mean an entry that says neither.
//
// A run that got as far as claiming dockets nothing here and is not an error,
// which is nearly every run.
func (d Docketer) RecordUnstartedRun(state runstate.State) (bool, error) {
	if err := d.validate(); err != nil {
		return false, err
	}
	if !unstartedRun(state) {
		return false, nil
	}
	entry, err := d.unstartedRunEntry(state, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// RecordEscalation dockets one run that ended with a role saying the work item
// cannot be met as it stands, at the moment it ended. It reports whether this
// call is what created the entry, so a caller can tell docketing an escalation
// from finding it already docketed.
//
// It is separate from RecordStoppedRun because the two are separate facts and
// only one of them is a stoppage. A stopped run spent its budget failing and
// carries a durable blocker; an escalated run ended in the review it was raised
// in and charged the item no round for it, integrated nothing, carries no
// blocker, and left its item parked. A single
// entry that had to describe both would say neither.
//
// A run nobody escalated dockets nothing and is not an error, which is nearly
// every run.
func (d Docketer) RecordEscalation(state runstate.State) (bool, error) {
	if err := d.validate(); err != nil {
		return false, err
	}
	if !state.Escalated() {
		return false, nil
	}
	entry, err := d.escalationEntry(state, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// RecordUnreadyItem dockets one item dispatch declined to start because the tree
// does not meet a prerequisite it states. It reports whether this call is what
// created the entry, so a caller can tell docketing a finding from finding it
// already docketed — which for a watching session is nearly every pull, since
// the queue is re-read every interval and the item is still unready.
//
// It is the one entry here made about work that never ran, and it takes the item
// and the reading rather than a run record because there is no run: the whole
// value of catching this is that it cost a read. What that means for the entry's
// other halves is stated in the entry rather than left to be noticed — nothing
// was preserved, nothing failed, and the counters are the item's own history
// rather than anything this finding spent.
//
// An item that meets everything it states dockets nothing and is not an error,
// which is nearly every item.
func (d Docketer) RecordUnreadyItem(item beads.WorkItem, unmet []readiness.Unmet) (bool, error) {
	if d.Docket == nil {
		return false, errors.New("a triage docket is required to route an unready item to it")
	}
	if len(unmet) == 0 {
		return false, nil
	}
	entry, err := d.unreadyEntry(item, unmet, d.now())
	if err != nil {
		return false, err
	}
	created, err := d.Docket.RecordOnce(entry)
	if err != nil || !created {
		return created, err
	}
	// Said to the product manager once per entry, which is once per refusal: a
	// pull that finds the same entry standing records nothing and reports nothing,
	// and one the pull took off and then found again is a new entry and news.
	if err := d.reportUnready(entry, unmet); err != nil {
		return true, fmt.Errorf("docketed %s as unready and could not report it to the Lead Product Manager: %w", item.ID, err)
	}
	return true, nil
}

// reportUnready files the refusal as a report, which is how it reaches the
// product manager's conversation: the report pile is delivered to the role that
// decides what becomes of a report, and she is that role.
//
// It exists because the docket alone put the refusal in front of the wrong
// person. yoyodyne-ifd.298 sat passed over at priority 1 for a sentence in its
// own description that only the product manager could remove, and the only
// surface that said so was the development manager's docket; it reached the
// product manager when the operator's assistant relayed it. The docket entry
// stays, because recording the dependency the sentence names is the development
// manager's; this is the other half of who can release it.
func (d Docketer) reportUnready(entry triage.Entry, unmet []readiness.Unmet) error {
	if d.Reports == nil {
		return nil
	}
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		Message:  unreadyReportMessage(entry.WorkItemID, unmet),
	}}, report.Attribution{
		Role: report.HarnessReporter,
		// The docket entry is the record this leads back to, as an exchange is for
		// the report an unresolved one files: there is no run, because being
		// refused before one was reserved is what the finding is.
		RunID:        entry.Key,
		WorkItemID:   entry.WorkItemID,
		Build:        d.Harness,
		ProductID:    d.ProductID,
		RepositoryID: d.RepositoryID,
	}, entry.RecordedAt)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := d.Reports.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

// unreadyReportMessage is the two sentences the product manager is told: which
// item is passed over and for which words, and what releases it. The words are
// the readiness package's own description, so the report, the docket entry, and
// the pass's refusal quote one sentence one way.
func unreadyReportMessage(workItemID string, unmet []readiness.Unmet) string {
	return fmt.Sprintf("%s is passed over at every pull, and dispatched to nobody, because of what it states: %s. "+
		"Amend the item where that no longer holds, or have the development manager record the dependency it names; every pull reads the item again as the tracker holds it, so it is taken at the first pull after the words are gone.",
		workItemID, singleLine(readiness.Describe(unmet), report.MaxMessageBytes/2))
}

// UnreadyReading is what one pull found of an item the docket holds as unready.
type UnreadyReading struct {
	// Present is the item being in the backlog the pull read. An item that has
	// left it — closed, most often — is asking nothing of anybody.
	Present bool
	// Unmet is what the item states that the tree does not meet, read from the
	// item as the tracker held it at this pull.
	Unmet []readiness.Unmet
	// Unreadable is a reading of the tree that failed. It settles nothing: a tree
	// that could not be read says nothing about the item, in either direction.
	Unreadable bool
}

// SettleUnreadyItems takes off the docket every unready entry the pull's own
// reading no longer supports, and reports how many it took off.
//
// Nothing did this before, and the docket entry outlived the sentence it quoted.
// yoyodyne-ifd.298's entry went on quoting "this item does not start before
// 282's design lands" after the product manager had removed the words, after a
// pull had dispatched the item, and after the item had closed: the pull read the
// item afresh every time, and the docket, which is what anybody reading about
// the item was shown, was never told. So each pull asks the question again of
// every entry standing, and an entry is taken off where the item now asks for
// nothing the tree lacks, where it has left the backlog, or where what it asks
// for is no longer what the entry quotes — the last so that an item amended to a
// different sentence is docketed again in the words it now carries, rather than
// standing under the words it used to.
func (d Docketer) SettleUnreadyItems(reread func(workItemID string) UnreadyReading) (int, error) {
	if d.Docket == nil {
		return 0, errors.New("a triage docket is required to settle the unready items on it")
	}
	entries, err := d.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to settle its unready items: %w", err)
	}
	now := d.now().UTC()
	settled := 0
	var problems []error
	for _, entry := range entries {
		if entry.Class != triage.ClassUnreadyItem || entry.Unready == nil {
			continue
		}
		if entry.Closed != nil && entry.Closed.Holds(now) {
			continue
		}
		reading := reread(entry.WorkItemID)
		if reading.Unreadable {
			continue
		}
		reason := ""
		switch {
		case !reading.Present:
			reason = "the item is no longer in the backlog the pull reads, so it is asking nothing of anybody"
		case len(reading.Unmet) == 0:
			reason = "the item was read again at a pull, as the tracker then held it, and asks for nothing the tree does not have"
		case !samePrerequisites(entry.Unready.Prerequisites, reading.Unmet):
			reason = "the item was read again at a pull and what it asks for is no longer what this entry quotes: " +
				singleLine(readiness.Describe(reading.Unmet), triage.MaxMessageBytes/2)
		default:
			continue
		}
		// Closed a moment before the pull's own, because the same pull goes on to
		// docket the item again where it is restated, and the docket opens a key
		// again only for an entry recorded after the closure that settled it. It is
		// never before the entry itself, which the docket would refuse as a decision
		// about some earlier entry.
		closedAt := now.Add(-time.Nanosecond)
		if closedAt.Before(entry.RecordedAt) {
			closedAt = entry.RecordedAt
		}
		took, err := d.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			WorkItemID:    entry.WorkItemID,
			Decision:      clearedUnreadyDecision,
			Reason:        reason,
			DecidedBy:     "the harness, reading the item again at a pull",
			ClosedAt:      closedAt,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("take %s off the docket as no longer unready: %w", entry.WorkItemID, err))
			continue
		}
		if took {
			settled++
		}
	}
	return settled, errors.Join(problems...)
}

// clearedUnreadyDecision is the word a closure made by SettleUnreadyItems
// carries. It is not one of the development manager's decisions, and is worded
// so nobody reads it as one.
const clearedUnreadyDecision = "no-longer-unready"

// closedItemDecision is the word a closure made by SettleClosedItems carries.
// Like the two beside it it is the harness's and not a triage decision, and it
// says what settled the entry: the item it is about left the backlog.
const closedItemDecision = "item-closed"

// SettleClosedItem closes every entry standing on the docket for one work item,
// because the item has been closed or retired. It reports how many it closed.
// The reason is what the entries are closed with, and says who closed the item
// and how, in the words of whatever closed it.
func (d Docketer) SettleClosedItem(workItemID, reason string) (int, error) {
	return d.SettleClosedItems(map[string]string{workItemID: reason})
}

// SettleClosedItems closes every entry standing on the docket whose work item is
// among those given, and reports how many it closed. The map is the items the
// tracker holds as closed, each with the reason its entries are closed with.
//
// Nothing did this before, and an entry outlived its item for good. A stoppage
// settled by a re-run that landed, by the product manager closing the item, or by
// the item being retired stayed on the docket, because the only things that
// closed an entry were a decision about that entry and the two settlements above.
// On 2026-09-25 125 of the 187 open entries belonged to closed items, and the
// development manager's bounded listing showed her 11 of them: the dead ones
// crowded out the stoppages that were still somebody's to decide.
//
// Every class that asks a question about the item is closed, the unready item
// included, because a closed item asks nothing. An unfinished publication is not
// one of them and is never closed here: it asks about a merge the forge holds,
// and an item is closed as its change is integrated while that merge can still be
// dropped or stuck afterwards — which is what `rearm` is for. Its entry closes
// when the publication settles, or on a decision about it. A decision standing
// over an entry is left as it is, since that entry is already off the docket; one
// that has lapsed is not, and the entry is closed here.
func (d Docketer) SettleClosedItems(closed map[string]string) (int, error) {
	if d.Docket == nil {
		return 0, errors.New("a triage docket is required to close the entries of closed items")
	}
	if len(closed) == 0 {
		return 0, nil
	}
	entries, err := d.Docket.List()
	if err != nil {
		return 0, fmt.Errorf("read the triage docket to close the entries of closed items: %w", err)
	}
	now := d.now().UTC()
	settled := 0
	var problems []error
	for _, entry := range entries {
		reason, isClosed := closed[entry.WorkItemID]
		if !isClosed || !closesWithItem(entry.Class) {
			continue
		}
		if entry.Closed != nil && entry.Closed.Holds(now) {
			continue
		}
		// Never before the entry itself, which the docket would refuse as a decision
		// about some earlier stoppage.
		closedAt := now
		if closedAt.Before(entry.RecordedAt) {
			closedAt = entry.RecordedAt
		}
		took, err := d.Docket.Close(triage.Closure{
			SchemaVersion: triage.ClosureSchemaVersion,
			Key:           entry.Key,
			ProductID:     entry.ProductID,
			RunID:         entry.RunID,
			WorkItemID:    entry.WorkItemID,
			Decision:      closedItemDecision,
			Reason:        singleLine(reason, triage.MaxMessageBytes),
			DecidedBy:     "the harness, closing the entry with its item",
			ClosedAt:      closedAt,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("close the %s entry of %s with its item: %w", entry.Class, entry.WorkItemID, err))
			continue
		}
		if took {
			settled++
		}
	}
	return settled, errors.Join(problems...)
}

// closesWithItem reports an entry class whose question is answered by its item
// closing. The unfinished publication is the one that is not: see
// SettleClosedItems.
func closesWithItem(class triage.Class) bool {
	return class != triage.ClassPublication
}

// ClosedItemReasons is the reason each item the tracker holds as closed has its
// docket entries closed with, as a sweep that read the tracker states it.
func ClosedItemReasons(items []beads.WorkItem, readBy string) map[string]string {
	reasons := make(map[string]string, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		reasons[item.ID] = fmt.Sprintf("the tracker holds %s as closed, as %s read it, so nothing about this stoppage is anybody's to decide", item.ID, readBy)
	}
	return reasons
}

// samePrerequisites reports an entry quoting exactly what a reading found, in
// the same order: the same kinds and the same words. The entry's copy is bounded
// to what a docket entry carries, so the reading is compared up to that bound.
func samePrerequisites(recorded []triage.Prerequisite, unmet []readiness.Unmet) bool {
	if len(unmet) > triage.MaxPrerequisites {
		unmet = unmet[:triage.MaxPrerequisites]
	}
	if len(recorded) != len(unmet) {
		return false
	}
	for index, one := range unmet {
		if recorded[index].Kind != string(one.Kind) || recorded[index].Missing != one.Missing {
			return false
		}
	}
	return true
}

// UnstartedAttempt is one dispatch that never became a run: the item that was
// chosen, why it was chosen, what stopped the dispatch, and whether the session
// that made it has excluded the item for the rest of its life.
//
// It is assembled by the caller rather than read from a run record, because the
// whole of what the class describes is a dispatch for which no run record was
// ever written. Everything here is held by the process that made the attempt and
// by nothing else, which is exactly why it has to be written down where it
// happens.
type UnstartedAttempt struct {
	WorkItemID string
	// WorkItemTitle is what the item is called. It is carried for the reason the
	// run record carries it: an entry outlives whatever could look the title up,
	// and one holding only an identifier says nothing about what was being tried.
	WorkItemTitle string
	// SelectedBecause is the reason the scheduler recorded for choosing the item,
	// which would have gone onto the run record had one been written.
	SelectedBecause string
	// Failure is what stopped the dispatch, in the words of whatever stopped it.
	Failure string
	// ExcludedForTheSession reports the session refusing to try the item again
	// until the item changes, which is the state that turns one failed dispatch
	// into a queue nothing pulls from.
	ExcludedForTheSession bool
}

// RecordUnstartedAttempt dockets one dispatch that failed before any run record
// existed, at the moment it failed. It reports whether this call is what created
// the entry, so a caller can tell docketing an attempt from finding it already
// docketed — which for a session meeting the same failure twice is the second
// one.
//
// It is separate from RecordUnstartedRun because the two are made from opposite
// evidence. That one is made from a run record whose run never claimed; this one
// exists precisely because there is no record to make anything from, so it takes
// what the dispatching process holds and is the only thing that can write it
// down. An attempt that reached a reservation dockets nothing here and is not an
// error, which is nearly every attempt.
func (d Docketer) RecordUnstartedAttempt(attempt UnstartedAttempt) (bool, error) {
	if d.Docket == nil {
		return false, errors.New("a triage docket is required to record an attempt that never became a run")
	}
	if strings.TrimSpace(attempt.Failure) == "" {
		return false, nil
	}
	entry, err := d.attemptEntry(attempt, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// attemptEntry is one dispatch that produced no run record, as the development
// manager reads it.
//
// Like the unready entry it names no run and carries no change evidence, because
// there is neither. What it carries instead is the selection — the one thing a
// run record would have held that nothing else in the harness wrote down.
func (d Docketer) attemptEntry(attempt UnstartedAttempt, now time.Time) (triage.Entry, error) {
	// Bounded to what an entry may carry, for the reason the unstarted run's
	// failure is: an entry refused for its length is a failure the development
	// manager never hears about, which is the silence this class exists to end.
	failure := runstate.RecordFailure(attempt.Failure)
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.AttemptKey(attempt.WorkItemID, failure),
		Class:         triage.ClassUnstartedAttempt,
		ProductID:     d.ProductID,
		WorkItemID:    attempt.WorkItemID,
		WorkItemTitle: attempt.WorkItemTitle,
		RecordedAt:    now.UTC(),
		Failure:       failure,
		// The selection is carried whole rather than bounded again. It is the same
		// string a reservation would have validated at runstate.MaxSelectionReasonBytes,
		// which is a quarter of what an entry holds, so cutting it here would only
		// ever cut something already inside the bound.
		Attempt: &triage.Attempt{
			SelectedBecause:       attempt.SelectedBecause,
			ExcludedForTheSession: attempt.ExcludedForTheSession,
		},
	}
	// The counters are read for the reason the unready entry reads them: what a
	// development manager may still decide about this item is the same question
	// whether the item stopped, never started, or never got as far as a run, and an
	// entry showing zeros is indistinguishable from an item nobody has decided
	// anything about.
	entry.Counters, entry.CountersProblem = d.unreadyCounters(attempt.WorkItemID)
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the attempt at %s that never became a run: %w", attempt.WorkItemID, err)
	}
	return entry, nil
}

func (d Docketer) unreadyEntry(item beads.WorkItem, unmet []readiness.Unmet, now time.Time) (triage.Entry, error) {
	read := now.UTC()
	prerequisites := make([]triage.Prerequisite, 0, len(unmet))
	for _, one := range unmet {
		prerequisites = append(prerequisites, triage.Prerequisite{
			Kind:     string(one.Kind),
			Missing:  one.Missing,
			Evidence: one.Evidence,
			Decides:  one.Decides,
		})
	}
	if len(prerequisites) > triage.MaxPrerequisites {
		prerequisites = prerequisites[:triage.MaxPrerequisites]
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.UnreadyKey(item.ID, readiness.Kinds(unmet)),
		Class:         triage.ClassUnreadyItem,
		ProductID:     d.ProductID,
		WorkItemID:    item.ID,
		WorkItemTitle: item.Title,
		RecordedAt:    read,
		Unready:       &triage.Unready{Prerequisites: prerequisites, ReadAt: read},
	}
	// The counters are read for the reason every other entry reads them: what a
	// development manager may still decide about this item is the same question
	// whether the item stopped or never started, and an entry showing zeros is
	// indistinguishable from an item nobody has decided anything about. A record
	// that will not read says so on the entry rather than failing the finding,
	// because a finding nobody is told at all is worse than one whose budget line
	// is missing.
	entry.Counters, entry.CountersProblem = d.unreadyCounters(item.ID)
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket %s as unready: %w", item.ID, err)
	}
	return entry, nil
}

// entriesFor is what one run record contributes to the docket, leaving out
// what is already on it. A run can contribute more than one: a stoppage is about
// the run, an escalation is about the item, and a stuck publication is about what
// the forge did with its work.
//
// The escalation is re-derived by this scan where a preserved death is not, and
// the difference is what each is read from. An escalation is a word a role wrote
// into the run's own record, so it stands on that record forever and re-deriving
// it costs nothing: a run that carried one always did, and no record written
// before the verb existed can carry it. So an escalation whose docket write
// failed as the run ended is picked up by the next scan, exactly as a blocker is.
func (d Docketer) entriesFor(state runstate.State, now time.Time, already standingDocket) ([]triage.Entry, error) {
	var entries []triage.Entry
	var problems []error
	if stoppedRun(state) && already.dockets(triage.Key(triage.ClassStoppedRun, state.RunID), stoppedAt(state)) {
		entry, err := d.stoppedRunEntry(state, now, d.look(state))
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	// A settled escalation counts as docketed whatever was decided, as a
	// publication does: the word stands on the run's record forever, so there is
	// no later moment for a second escalation of the same run to be measured by.
	if _, docketed := already[triage.Key(triage.ClassEscalation, state.RunID)]; state.Escalated() && !docketed {
		entry, err := d.escalationEntry(state, now)
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	if publicationDocketed(state, already) {
		return entries, errors.Join(problems...)
	}
	if stuckPublication(state, now, d.Triage.StuckMergeAge.Duration()) {
		entry, err := d.publicationEntry(state, now)
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	return entries, errors.Join(problems...)
}

// publicationDocketed reports this run's publication already being on the
// docket. Both keys it can carry are asked: an entry made before the pull
// request joined the key names the run alone, the log is append-only and nothing
// rewrites it, so a build that asked only the current key would docket every one
// of those a second time.
//
// A settled entry counts as docketed here whatever was decided, and unlike a
// stopped run it is never docketed again: a publication is the absence of an
// event, so there is no later moment to compare a decision against — nothing
// happens to a merge that is not happening. What puts a waited publication back
// in front of somebody is the decision lapsing rather than a fresh entry.
func publicationDocketed(state runstate.State, already standingDocket) bool {
	if _, docketed := already[triage.Key(triage.ClassPublication, state.RunID)]; docketed {
		return true
	}
	published := state.PullRequest
	if published == nil {
		return false
	}
	_, docketed := already[triage.PublicationKey(state.RunID, published.Number)]
	return docketed
}

// stoppedRun reports a run that ended on a durable blocker. Both halves matter.
// The blocker is what says a person has to decide something, and the terminal
// status is what says nobody is going to: a run that is still going, or parked
// waiting out a provider, an operator, or a directive, is owed a continuation
// and is not stopped work at all — every one of those pauses is recorded on a
// run that is still running, which is exactly what this excludes.
//
// It is the whole of what the scan below re-derives, and deliberately not the
// whole of what is docketed: preservedDeath is the other half, and is recorded
// where the death happens rather than found by walking the history.
func stoppedRun(state runstate.State) bool {
	return state.Blocker != "" && state.Status.Terminal()
}

// preservedDeath reports a run that died holding its change, with nothing having
// classified the death as a stoppage.
//
// The harness has two routes to a terminal record and they disagreed about these.
// A run whose process was killed is settled by a sweep, which blocks the item
// when the artifacts survive and dockets the stoppage off that blocker. A run
// that fails inside its own process records an outcome note instead — no blocker,
// and so no entry — because the harness may yet resume it, and a blocked item is
// one it would refuse to resume. That is the right call for the record and the
// wrong one for the docket: once the run is terminal nothing resumes it, and the
// change is still sitting on a branch with no recorded way to decide about it.
// `yoyo triage rerun` and `yoyo triage repair` both act on a docketed stoppage,
// so a push the remote refused (yoyodyne-ifd.242, yoyodyne-ifd.267) or a backend
// that broke mid-attempt (yoyodyne-ifd.209.6) left the development manager's
// recorded decision unexecutable, and the exit each time was a person dispatching
// the item by name — which starts a run and records no decision at all.
//
// Three conditions narrow it, and each excludes a run that is not this. A status
// other than failed is a run something stopped rather than one that died: an
// operator's stop and a deadline are both deliberate, and neither hands anybody a
// decision. A run that integrated its work is reconciliation's to finish, and an
// unfinished publication of it is docketed as the publication it is. And a run
// that left nothing behind is the harness having failed to carry it, which is a
// failure to read rather than work to decide about.
//
// # Why only where the death happens
//
// This is asked as a run ends and never by the scan that walks the recorded
// history, which is the one asymmetry here worth stating because it looks like an
// oversight. A blocker is a durable classification that stands on a record
// forever, so re-deriving it costs nothing: a run that carried one always did. A
// death that preserved its change is not — it is a shape every terminal failed run
// with a surviving branch has, including every record written before the harness
// carried a blocker at all, which the run schema says is most of them. Re-deriving
// it over the history would have docketed 48 of the 374 runs this repository had
// recorded on 2026-09-04, nearly all of them long-closed work, in one build; the
// development manager's context lists 25 entries newest first, so that one build
// would have hidden every stoppage she actually had to decide about behind months
// of settled ones. A docket read as complete when it is not is worse than one that
// says what it could not show, and this is the version of that failure the docket
// itself would have caused.
//
// What it costs is that a death whose docket write fails is not picked up later,
// where a blocker would be. That is reported by the run that could not write it,
// and is the same trade the scan makes everywhere else: the run's record still
// says what happened, and nothing about the change is lost.
//
// The three conditions the death itself carries are runstate.DiedInItsOwnProcess,
// asked there rather than restated here because the hold the pull reads asks the
// same question of the same record; what is added here is the artifacts test.
// diedHolding answers it from the repository, for both the docket and the
// guards that carry out a decision about that stoppage.
func diedHolding(state runstate.State, found triage.Found) bool {
	return state.DiedInItsOwnProcess() && found.Holds()
}

// stageBoundHolding reports a run the check stage bound stopped with its change
// still there. It is the one deliberate stop that is docketed: a deadline
// otherwise hands nobody anything, but this one ends a run whose change is
// finished and sitting on its branch, and until yoyodyne-ifd.429.25 the claim
// audit gave its item back half an hour later to a fresh run that redid the
// development. The entry is what the harness's own continuation of it is
// fired from, and what the development manager decides from once those
// continuations are spent.
func stageBoundHolding(state runstate.State, found triage.Found) bool {
	return state.StoppedAtStageBound() && found.Holds()
}

// stageBoundFailure is where the bound stopped the stage, in the words the run
// ended on, for a run the bound stopped and for no other.
func stageBoundFailure(state runstate.State) string {
	if !state.StoppedAtStageBound() {
		return ""
	}
	return runstate.RecordFailure(state.Failure)
}

// unstartedRun reports a run that died before it took its work item.
//
// This was the one way a run could fail and reach nobody. Every other stoppage is
// docketed off something the failure left behind — a durable blocker on the item,
// or a change preserved on a branch — and a dispatch that dies at the claim has
// neither: the item is untouched, so nothing wrote a blocker on it, and no
// worktree was cut, so preservedDeath's artifacts test is false. The run's own
// record said what happened and no surface the development manager reads did.
// yoyodyne-ifd.285 was dispatched twenty-nine times between 2026-09-06 18:43 and
// 2026-09-07 14:44 that way, each run dying on the same three words.
//
// What narrows it is the record's own reading of the same fact, which is asked
// rather than derived again here: the two rules that say the claim was never
// made, the status that says the run died rather than being stopped, and the
// failure without which an entry says nothing anybody can act on. See
// runstate.State.DiedBeforeClaiming.
//
// # Why only where the death happens
//
// This is asked as a run ends and never by the scan that walks the recorded
// history, for preservedDeath's reason and for a sharper one of its own. The
// claim time is a field yoyodyne-ifd.338 added, so every run recorded before it
// reads as unclaimed however far it actually got; a scan that re-derived this
// would docket a decade of settled failures in one build and bury the entries the
// development manager is there to decide about. What that costs is the same trade
// preservedDeath makes: a death whose docket write fails is reported by the run
// that could not write it, and nothing about the item is lost, because there was
// never anything to lose.
func unstartedRun(state runstate.State) bool {
	return state.DiedBeforeClaiming()
}

// stuckPublication reports an approved publication that did not finish and is
// not going to without somebody looking at it. There are two kinds, and they
// are one class because they need the same thing from the same reader.
//
// The first is a publication the harness already recorded as outstanding: a
// merge the forge dropped because a requirement of the base branch went unmet,
// which the harness never merges past, or one the forge performed that the
// harness could not then confirm. Nothing about either changes with time, so
// both are docketable the moment they are recorded.
//
// The second is a publication nothing has recorded anything about, which is
// what a merge that is simply not happening looks like. Nothing happened to it,
// so there is no event to hang a deadline on, and it is docketed on its age
// instead — measured from when the run that made it ended, which is when it
// became something waiting on the forge.
func stuckPublication(state runstate.State, now time.Time, stuckMergeAge time.Duration) bool {
	published := state.PullRequest
	if published == nil {
		// A promotion whose record holds no request is docketed from the record's
		// own account of that, before anything has asked the forge: it is the one
		// publication the harness has no request to finish, and an entry that
		// waited for the request would wait for exactly the sweep whose failure
		// leaves this standing.
		return state.PublicationUnrecorded()
	}
	// A run still in flight owns its own publication, and a parked one is owed
	// the rest of its own step. Neither is work that has stopped.
	if !state.Status.Terminal() {
		return false
	}
	// A publication handed back for a fresh run has been decided and carried
	// out: the change is the fresh run's, and docketing the request again on its
	// age would put a question to the development manager she has answered.
	if published.HandedBack != nil {
		return false
	}
	// A publication the harness retired as superseded is finished with, for the
	// opposite reason a merged one is: its work landed by another vehicle and
	// the request itself is closed. Docketing it would send the development
	// manager to a pull request the harness has already dealt with.
	if strings.TrimSpace(published.Superseded) != "" {
		return false
	}
	// Only an approved publication is stuck. A pull request from a run that was
	// never approved is a branch nobody authorized merging, and the run's own
	// blocker is what says so.
	if state.ReviewDecision != runstate.ReviewApprove {
		return false
	}
	// An outstanding publication the harness recorded is docketable whichever
	// way the merge went, and there are two of those: a merge the forge dropped,
	// and one it performed that the harness could not confirm. The second is the
	// reason this is asked before the merged publication below is dismissed —
	// the merge happened and the publication still did not finish.
	if state.PublishFailure != "" {
		return true
	}
	// Past that, a merged publication is finished work.
	if published.Merged {
		return false
	}
	// A request nothing ever asked the forge to merge is docketed at once, like
	// the recorded ones above rather than on its age: nothing is waiting for the
	// forge, so no amount of time changes it, and until yoyodyne-ifd.429.31 its
	// only exit was a person merging it by hand. The development manager decides
	// it — a re-arm the harness carries out, or a re-run.
	if state.PublicationUnasked() {
		return true
	}
	// A threshold of no time at all would docket every publication the instant
	// it was made, which the configuration refuses; this refuses to act on one
	// anyway, because a docket built from a configuration nobody validated must
	// not be a docket of everything.
	if stuckMergeAge <= 0 {
		return false
	}
	return now.Sub(publicationApprovedAt(state)) >= stuckMergeAge
}

// publicationApprovedAt is when the publication became something waiting on the
// forge. It is the moment the run ended rather than the moment the record was
// last touched: a sweep that walks past a stuck publication and writes nothing
// must not be able to reset its age, and one that does write must not either.
func publicationApprovedAt(state runstate.State) time.Time { return stoppedAt(state) }

// stoppedAt is when this run's work stopped moving, which is what a build
// compares against a decision already made about it: a run that was repaired and
// died again ended after the decision that settled its last stoppage, and a run
// nothing has touched since ended before it.
//
// The moment the run ended rather than the moment its record was last written,
// for the reason the publication's age is measured from there: a sweep that
// writes to a settled record must not be able to make an old stoppage look new.
func stoppedAt(state runstate.State) time.Time {
	if state.CompletedAt != nil {
		return *state.CompletedAt
	}
	return state.UpdatedAt
}

func (d Docketer) stoppedRunEntry(state runstate.State, now time.Time, found triage.Found) (triage.Entry, error) {
	entry, err := d.stoppedRunEntryCarrying(state, now, found, docketFailure(state, found))
	if err != nil {
		return triage.Entry{}, err
	}
	entry.StopRequested = d.requestedStop(state)
	return entry, nil
}

// requestedStop is a stop asked of a run whose stoppage this entry records, and
// whether that stop is what ended it.
//
// Two stoppages carry one. A run that honored the stop ends cancelled — an
// operator's `/stop` does, and is then settled into a durable blocker and
// docketed here — and the stop landed. A run that passed its last boundary
// before the request was read ended some other way, a failed review or a check,
// and the stop never reached it: the entry carries it beside what actually
// stopped the run, so whoever decides the stoppage knows the stop was asked, and
// knows it decides nothing here. Cancelled is what a stop that landed leaves, and
// a run that failed or was blocked on its own account is not cancelled, so the
// status is what tells the two apart.
//
// A request that cannot be read is left off rather than failing the entry: the
// stoppage is undecided either way, and an entry refused over the half of its
// account that could not be read is a stoppage nobody is shown.
func (d Docketer) requestedStop(state runstate.State) *triage.RequestedStop {
	if d.Stops == nil {
		return nil
	}
	request, requested, err := d.Stops.StopRequested(state.RunID)
	if err != nil || !requested {
		return nil
	}
	return &triage.RequestedStop{
		By:       singleLine(request.StoppedBy(), triage.MaxMessageBytes),
		At:       request.RequestedAt.UTC(),
		Reason:   singleLine(strings.TrimSpace(request.Reason), triage.MaxMessageBytes),
		Decision: strings.TrimSpace(request.Decision),
		Landed:   state.Status == runstate.StatusCancelled,
	}
}

// stoppedRunEntryCarrying is stoppedRunEntry with the failure the entry carries
// named by the caller. A stop somebody decided is the one stoppage whose reason
// is its failure while it carries no blocker and did not die, which is not a
// shape docketFailure reads, so the caller that dockets one says so itself.
func (d Docketer) stoppedRunEntryCarrying(state runstate.State, now time.Time, found triage.Found, failure string) (triage.Entry, error) {
	counters, err := d.recordedCounters(state, publicationRearms{})
	if err != nil {
		return triage.Entry{}, err
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, state.RunID),
		Class:         triage.ClassStoppedRun,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		StopClass:     state.RecordedStopClass().Name(),
		RecordedAt:    now.UTC(),
		Blocker:       state.Blocker,
		// The reason the run gave for dying is carried only where it is what makes
		// this a stoppage. Every other entry has a blocker that says the same thing
		// in the words the work item carries, and printing the failure beside it
		// would be the same fact twice on every ordinary stoppage.
		Failure:         failure,
		Summary:         docketSummary(state),
		Findings:        docketFindings(state.ReviewFindingDetails),
		Check:           docketCheck(state.CheckFailure),
		Artifacts:       docketArtifacts(state, found),
		Environmental:   docketEnvironmental(state.Environmental),
		IntegrationStop: docketIntegrationStop(state.IntegrationStop),
		// Whether the session this run stopped in can simply be carried on is read
		// from the same predicate the repair carry-out admits a stall by, so the
		// entry cannot offer a continuation the verb then refuses.
		SessionResumable: continuableStall(state) && !found.Unknown && found.BranchThere && found.WorktreeThere,
		// And where it is carried on, from the predicate that decides the phase the
		// carry-out puts the run back at, for the same reason.
		ResumesAt: resumesAtOf(state),
		// A stage the bound stopped says so in the run's own sentence, which the
		// channel says too, and whether the harness is the one that continues it.
		CheckStageStop:         singleLine(state.CheckStageStopSays(), triage.MaxMessageBytes),
		CheckStageFailure:      stageBoundFailure(state),
		HarnessContinuesChecks: state.HarnessContinuesCheckStage(),
		// A silent-stream stall says so in the run's own sentence, whether it began
		// in a session a re-adoption resumed, and whether the harness still
		// continues it itself.
		StallStop:             singleLine(state.StallStopSays(), triage.MaxMessageBytes),
		HarnessContinuesStall: state.HarnessContinuesStall(),
		ReplayConflict:        docketReplayConflict(state.ReplayConflict),
		Counters:              counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the stoppage of run %s: %w", state.RunID, err)
	}
	return entry, nil
}

// resumesAtOf is the step a resumable stall past its developer attempt is
// continued at, as the docket carries it, and empty for every other stoppage.
func resumesAtOf(state runstate.State) string {
	if !stallResumesPastTheAttempt(state) {
		return ""
	}
	return string(state.Phase)
}

// docketReplayConflict carries the run's record of its approved change
// conflicting on replay onto the entry, in the docket's own shape. It is what
// tells the development manager the stoppage is hers or a person's, and not
// the resume verb's — which, on the one run where the blocker write failed, the
// entry could otherwise only have said from the failure's prose.
func docketReplayConflict(conflicted *runstate.ReplayConflict) *triage.ReplayConflict {
	if conflicted == nil {
		return nil
	}
	return &triage.ReplayConflict{
		TargetBranch: conflicted.TargetBranch,
		Detail:       singleLine(conflicted.Detail, triage.MaxMessageBytes),
		Phase:        string(conflicted.Phase),
	}
}

// docketIntegrationStop carries the run's record of the environment stopping
// its approved change onto the entry, in the docket's own shape. It is the whole
// of what tells the development manager this stoppage asks her for nothing.
func docketIntegrationStop(stopped *runstate.IntegrationStop) *triage.IntegrationStop {
	if stopped == nil {
		return nil
	}
	return &triage.IntegrationStop{
		Cause:  string(stopped.Cause),
		Detail: singleLine(stopped.Detail, triage.MaxMessageBytes),
		Phase:  string(stopped.Phase),
		Title:  stopped.Cause.Title(),
	}
}

// unstartedRunEntry is one dispatch that died before it took its item, as the
// development manager reads it.
//
// It carries none of the change evidence a stopped run carries, and that is the
// entry rather than an omission: nothing was claimed, cut, written, reviewed or
// checked, so a findings list or an artifacts block on it would send somebody
// after a change that was never made. What it carries is the failure — the whole
// of what there is to decide from — and the counters, which say what the item can
// still afford once somebody works out why the dispatch could not start.
func (d Docketer) unstartedRunEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.recordedCounters(state, publicationRearms{})
	if err != nil {
		return triage.Entry{}, err
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassUnstartedRun, state.RunID),
		Class:         triage.ClassUnstartedRun,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		// The title is what makes the entry readable, and it is the one thing a run
		// that never claimed still has: it is written onto the record when the run is
		// reserved, off the item the dispatch was made for.
		WorkItemTitle: state.WorkItemTitle,
		StopClass:     state.RecordedStopClass().Name(),
		RecordedAt:    now.UTC(),
		// Bounded to what an entry may carry, for the reason a preserved death's
		// failure is: an entry refused for its length is a failure the development
		// manager never hears about, which is the silence this class exists to end.
		Failure:  runstate.RecordFailure(state.Failure),
		Counters: counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the death of run %s before it claimed %s: %w", state.RunID, state.WorkItemID, err)
	}
	return entry, nil
}

// escalationEntry is one role's judgement that the item cannot be met, as the
// development manager reads it.
//
// It carries none of the change evidence the stopped-run entry carries, and that
// is the entry rather than an omission: an escalated run integrated nothing,
// failed no check, and touched no protected path, so a findings list or a check
// block on it would be evidence about a change nobody is deciding about. What it
// does carry is what was preserved — the worktree and the branch are still there,
// and whatever the developer had written is in them — and the counters, which are
// what says the item can still afford whatever she decides.
func (d Docketer) escalationEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.recordedCounters(state, publicationRearms{})
	if err != nil {
		return triage.Entry{}, err
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassEscalation, state.RunID),
		Class:         triage.ClassEscalation,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		StopClass:     state.RecordedStopClass().Name(),
		RecordedAt:    now.UTC(),
		Escalation: &triage.Escalation{
			RaisedBy: state.EscalatedBy(),
			// Bounded to what an entry may carry, for the reason a preserved death's
			// failure is: an entry refused for its length is one she never hears about,
			// which is the silence the whole verb exists to end.
			Reason: runstate.RecordEscalationReason(state.EscalationReason()),
		},
		Artifacts:     docketArtifacts(state, d.look(state)),
		Environmental: docketEnvironmental(state.Environmental),
		Counters:      counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the escalation raised by run %s: %w", state.RunID, err)
	}
	return entry, nil
}

func (d Docketer) publicationEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.recordedCounters(state, rearmsOf(state))
	if err != nil {
		return triage.Entry{}, err
	}
	// The publication is keyed to the run and the pull request together, so what
	// an entry is about is two durable facts a reader can check rather than
	// something anybody has to infer from the state of the item. A record that
	// holds no request is keyed to the run alone, which is the shape the re-arm
	// and the docketed check both already read; the request the sweep recovers
	// afterwards joins the same entry rather than opening a second.
	var published runstate.PullRequest
	key := triage.Key(triage.ClassPublication, state.RunID)
	if state.PullRequest != nil {
		published = *state.PullRequest
		key = triage.PublicationKey(state.RunID, published.Number)
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           key,
		Class:         triage.ClassPublication,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		StopClass:     state.RecordedStopClass().Name(),
		RecordedAt:    now.UTC(),
		Summary:       docketSummary(state),
		Findings:      docketFindings(state.ReviewFindingDetails),
		Artifacts:     docketArtifacts(state, d.look(state)),
		Publication: &triage.Publication{
			Number: published.Number,
			URL:    published.URL,
			// The branch is the run's own where no request is recorded: it is
			// what the forge is asked by, and the one handle the entry can name.
			Branch:      nonEmpty(published.Branch, state.Branch),
			HeadCommit:  published.HeadCommit,
			State:       published.State,
			Merged:      published.Merged,
			MergeQueued: published.MergeQueued,
			MergeMethod: published.MergeMethod,
			MergeCommit: published.MergeCommit,
			Message:     publicationMessage(state),
			Checks:      publicationChecks(published, state),
			WaitingOn:   publicationWaitingOn(published),
			ApprovedAt:  publicationApprovedAt(state).UTC(),
		},
		Counters: counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the unmerged publication of run %s: %w", state.RunID, err)
	}
	return entry, nil
}

// publicationMessage is what the entry says went wrong with the publication:
// the run's own account where it wrote one, and for a request nothing ever asked
// the forge to merge — which carries no account, because nothing failed — the
// two decisions that answer it, so the entry never reads as a publication with
// nothing to say about it.
func publicationMessage(state runstate.State) string {
	if !state.PublicationUnasked() {
		return state.PublishFailure
	}
	target := state.TargetBranch
	if state.Integration != nil {
		target = state.Integration.TargetBranch
	}
	if state.PullRequest.Closed() {
		return oneline.Bound(closedUnaskedPublication(state.PullRequest.Number, target), triage.MaxBlockerBytes)
	}
	return oneline.Bound(unarmedPublication(state.PullRequest.Number, target), triage.MaxBlockerBytes)
}

// closedUnaskedPublication is the account of a request nothing ever asked the
// forge to merge that the forge has since closed: there is nothing left to arm,
// so the one decision that answers it is a re-run.
func closedUnaskedPublication(number int, target string) string {
	return fmt.Sprintf("nothing ever asked the forge to merge pull request %d into %s, and the forge has closed it unmerged, so there is nothing left to arm: a re-run hands the change back for a fresh run",
		number, target)
}

// unarmedPublication is the account of a request nothing ever asked the forge
// to merge, in the words the docket entry and the arming's refusals share.
func unarmedPublication(number int, target string) string {
	return fmt.Sprintf("nothing ever asked the forge to merge pull request %d into %s: a re-arm decision has the harness arm it as the run would have, under the same landing checks, and a re-run hands the change back for a fresh run",
		number, target)
}

// publicationWaitingOn is the entry's account of a merge withdrawn for its
// target's red check, in the sentence every surface says of it, or nothing.
func publicationWaitingOn(published runstate.PullRequest) string {
	if published.TargetRed == nil || published.MergeQueued || published.Merged {
		return ""
	}
	return oneline.Bound(published.TargetRed.Describe(), triage.MaxBlockerBytes)
}

// publicationChecks is the entry's account of the request's checks, in the
// sentence every surface says of them, or nothing where no sweep has read them.
func publicationChecks(published runstate.PullRequest, state runstate.State) string {
	if published.Checks == nil {
		return ""
	}
	target := state.TargetBranch
	if state.Integration != nil {
		target = state.Integration.TargetBranch
	}
	return oneline.Bound(published.Checks.Describe(target), triage.MaxBlockerBytes)
}

// counters are what the item has already spent and been given, beside what it is
// allowed to spend, and what has been carried out of what triage decided.
//
// Every figure but the repair attempts comes from the item's own triage record
// and the caps the guards refuse against, rather than being counted again here.
// The rounds are the case that shows why: the record counts a round per
// developer attempt judged, and summing the rounds each run recorded counts a
// replayed change's re-review as well — so a docket that added them up could
// report an item at its cap that the guard would grant, and refuse in a reader's
// head what nothing refuses in the record.
//
// It is every figure the record holds rather than a chosen few. A field the
// ledger keeps and the view drops is a decision the guards can see and the
// development manager cannot, which reads on the docket as a decision nobody
// took: the rounds a grant came to, whether the cap cut it, and what it stands
// committed to are all of them detail the harness reported as it recorded the
// grant, and all of them were missing from the entry it was recorded against.
//
// The caps it reports are the configured ones as the item's own recorded
// overrides leave them, which is what the guards refuse against. An operator has
// crossed a cap precisely when a development manager was refused past it, so an
// entry stating the configured figure would refuse in its reader's head exactly
// the decision the operator sat down to make possible. The overrides themselves
// are on the entry rather than in here — a budget larger than the project
// configured and no account of why is a number a reader has to decide whether to
// trust, and they are joined where the docket is read for the reason the re-run
// beside them is.
// publication, when the entry is about one, is what the re-arm figures are read
// per: that budget is the publication's rather than the item's, so the entry
// carries what has been decided and made about the one publication it describes
// beside the item's total.
//
// run is the stoppage the entry is about, and is what makes the standing below
// this entry's own rather than the item's; its record is what says a granted
// repair was handed back to it. An entry that names no run — an
// unready item, a dispatch that never became one — carries no standing, which is
// the truth about it: nothing can have been decided about a stoppage that never
// happened, however much the item has been decided about elsewhere.
func (d Docketer) counters(ledger runstate.TriageCounters, run runstate.State, repairAttempts, rerunsCarriedOut int, publication publicationRearms) triage.Counters {
	permitted := d.Caps.Overridden(ledger.Overrides)
	return triage.Counters{
		ReviewRounds:        ledger.ReviewRounds,
		ReviewRoundsCap:     permitted.ReviewRounds,
		RepairAttempts:      repairAttempts,
		RepairGrantAttempts: d.Triage.RepairGrantAttempts,
		RepairGrants:        ledger.RepairGrants,
		RepairGrantsCap:     permitted.RepairGrants,
		GrantedRounds:       ledger.GrantedRounds,
		TruncatedGrants:     ledger.TruncatedGrants,
		CommittedRounds:     ledger.CommittedRounds,
		Reruns:              ledger.Reruns,
		RerunsCap:           permitted.Reruns,
		MergeRearms:         ledger.MergeRearms,
		MergeRearmsCap:      permitted.MergeRearms,
		// Decided is read off the item's record under this publication's key, and
		// made off the publication's own record, which is where the harness writes
		// what it has actually repeated. The two are separate facts for the reason
		// the re-run's decision and claim are: a decision waiting to be carried out
		// and one already acted on are opposite answers to what the development
		// manager is about to ask.
		PublicationRearms:     ledger.RearmsOf(publication.key),
		PublicationRearmsMade: publication.made,
		RerunsCarriedOut:      rerunsCarriedOut,
		// What the development manager has already crossed on his own authority,
		// beside the bound the store refuses the next one against. Both are read from
		// the same record the guard reads, so the figure on the entry and the figure
		// that refuses can never be two different counts.
		Crossings:      ledger.DelegatedCrossings(),
		CrossingsBound: runstate.MaxDelegatedCapCrossings,
		// What stands decided about this entry's own stoppage, reduced by the
		// ledger itself to the shape the shared carry-out rule reads. It is the
		// one figure here that is not an item total, and it is read from the same
		// record the status surfaces read it from, by the same rule: an item that
		// read as the harness's on the docket and as the development manager's on
		// the status head would be one piece of work with two next movers.
		Standing: ledger.StandingOf(run),
	}
}

// publicationRearms is the publication an entry is about, for the figures that
// are read per publication: the key its decisions are recorded under, and how
// many re-arms of it the harness has made. A stopped-run entry carries the zero
// value, which names no publication and reads as nothing decided about one.
type publicationRearms struct {
	key  string
	made int
}

// rearmsOf is the publication one run's record describes, or nothing where that
// run published nothing. It is read from the record rather than from the entry
// for the reason every other figure here is: the entry says what was true when
// it was written, and what the guard reads is what is true now.
func rearmsOf(state runstate.State) publicationRearms {
	if state.PullRequest == nil {
		return publicationRearms{}
	}
	return publicationRearms{
		key:  triage.PublicationKey(state.RunID, state.PullRequest.Number),
		made: state.PullRequest.MergeRearms,
	}
}

// docketedOverrides carries the recorded cap decisions onto an entry. It is a
// copy rather than the record's own type for the reason every other figure here
// is copied: what reaches a development manager must not change shape because the
// durable schema was refactored.
func docketedOverrides(recorded []runstate.TriageOverride) []triage.Override {
	if len(recorded) == 0 {
		return nil
	}
	carried := make([]triage.Override, 0, len(recorded))
	for _, override := range recorded {
		carried = append(carried, triage.Override{
			Budget:    override.Budget,
			Cap:       override.Cap,
			Cleared:   override.Cleared,
			DecidedBy: override.DecidedBy,
			DecidedAt: override.DecidedAt,
			Reason:    override.Reason,
			// The role is carried as the title a reader reads rather than as the
			// marker the record keys it by, for the reason the entry copies every
			// other figure: the entry is prose a development manager reads, and a
			// slug there is one more thing to resolve.
			CrossedBy: crossedBy(override),
		})
	}
	return carried
}

// crossedBy names the role that crossed a cap on its own authority, and is empty
// for the operator's own override — which every one of them was before the
// delegation existed.
func crossedBy(override runstate.TriageOverride) string {
	if !override.Delegated() {
		return ""
	}
	return override.CrossedBy.Title()
}

// recordedCounters are the counters an entry is written with: the item's triage
// record as it stands at the moment the work stopped. What is written is a
// snapshot and is refreshed wherever the docket is read, because the decisions
// that matter to a reader are all made after the entry exists.
//
// What has been carried out is not read here. A stoppage being docketed has had
// nothing carried out about it yet, and reading the claims to write a zero would
// buy nothing the read side does not do again properly.
func (d Docketer) recordedCounters(state runstate.State, publication publicationRearms) (triage.Counters, error) {
	ledger, err := d.Decisions.Counters(state.WorkItemID)
	if err != nil {
		return triage.Counters{}, fmt.Errorf("read what triage has recorded about %s: %w", state.WorkItemID, err)
	}
	return d.counters(ledger, state, state.RepairAttempts, 0, publication), nil
}

// publicationsOf indexes what each run's record says about the publication it
// made, by run. A run that published nothing is absent, which reads as the zero
// value: no publication, and nothing re-armed about one.
func publicationsOf(recorded []runstate.State) map[string]publicationRearms {
	published := make(map[string]publicationRearms, len(recorded))
	for _, state := range recorded {
		if state.PullRequest == nil {
			continue
		}
		published[state.RunID] = rearmsOf(state)
	}
	return published
}

// unreadyCounters is what the item has spent and what it may still spend, and
// the reason it could not be read where that is the answer. Nothing this finding
// did costs the item anything — no run was made — so the repair attempts and the
// re-runs carried out are zero rather than counted from anywhere. Neither entry
// names a run, so neither carries a standing: there is no stoppage for a decision
// to have been made about.
func (d Docketer) unreadyCounters(workItemID string) (triage.Counters, string) {
	if d.Decisions == nil {
		return d.counters(runstate.TriageCounters{}, runstate.State{}, 0, 0, publicationRearms{}),
			"nothing was wired to read what triage has recorded about " + workItemID + ", so the figures beside this entry are the configured caps and no spend at all"
	}
	ledger, err := d.Decisions.Counters(workItemID)
	if err != nil {
		return d.counters(runstate.TriageCounters{}, runstate.State{}, 0, 0, publicationRearms{}),
			fmt.Sprintf("read what triage has recorded about %s: %v", workItemID, err)
	}
	return d.counters(ledger, runstate.State{}, 0, 0, publicationRearms{}), ""
}

func docketFindings(findings []runstate.Finding) []triage.Finding {
	if len(findings) == 0 {
		return nil
	}
	// The reviewer's words are copied rather than summarized: what makes a
	// finding worth carrying to a development manager is the reviewer's own
	// account of it, and a paraphrase is a second opinion nobody asked for.
	docketed := make([]triage.Finding, 0, len(findings))
	for _, finding := range findings {
		docketed = append(docketed, triage.Finding{
			Severity: finding.Severity,
			Message:  finding.Message,
			File:     finding.File,
			Line:     finding.Line,
		})
	}
	return docketed
}

// docketEnvironmental carries the run's account of a round the environment
// refused onto the entry. It is written into the entry rather than joined where
// the docket is read, unlike the decisions beside it, because it is settled as
// the run ends: what a development manager needs from it is what the counters on
// the entry already do or do not include, and that was decided before the entry
// was made.
func docketEnvironmental(refused *runstate.EnvironmentalRefusal) *triage.Environmental {
	if refused == nil {
		return nil
	}
	return &triage.Environmental{
		Cause:         string(refused.Cause),
		Detail:        refused.Detail,
		Settled:       refused.Settled,
		Refused:       refused.Refused,
		RoundReturned: refused.RoundReturned,
		GrantReturned: refused.GrantReturned,
		Problem:       refused.Problem,
		// The accounting sentence is carried rather than re-derived from the flags
		// beside it, so the docket, the thread, and the run's own notes say the same
		// words about what this round cost.
		Account: refused.Describe(),
	}
}

// docketFailure is the reason a preserved death gives for having died, bounded to
// what an entry may carry. The run record is bounded to the same size at the
// write that makes it, so this is a no-op over anything the harness records
// today; it stays because the entry must not depend on that having happened, and
// a stoppage refused at the docket is one the development manager never hears
// about, which is the silence this whole path exists to end.
func docketFailure(state runstate.State, found triage.Found) string {
	if state.Blocker != "" || !diedHolding(state, found) {
		return ""
	}
	return runstate.RecordFailure(state.Failure)
}

// docketSummary is what the reviewer said about the change, bounded to what an
// entry may carry. The run record is bounded to the same size at the write that
// makes it, so this is a no-op over anything the harness records today; it stays
// for the reason the failure's cut does — the entry must not depend on a bound
// somebody else was supposed to have applied, and an entry refused for its
// length is a stopped run the development manager never hears about.
func docketSummary(state runstate.State) string {
	return runstate.RecordReviewSummary(state.ReviewSummary)
}

func docketCheck(failure *runstate.CheckFailure) *triage.Check {
	if failure == nil {
		return nil
	}
	return &triage.Check{Command: failure.Command, ExitCode: failure.ExitCode, Output: failure.Output, ForgeHeadCommit: failure.ForgeHeadCommit}
}

func docketArtifacts(state runstate.State, found triage.Found) triage.Artifacts {
	artifacts := triage.Artifacts{
		// What the repository held as the entry was written, which is what the
		// entry says; the flags beside it are kept for what reads them as a record.
		Found:           &found,
		Branch:          state.Branch,
		WorktreePath:    state.WorktreePath,
		TargetBranch:    state.TargetBranch,
		BaseCommit:      state.BaseCommit,
		BranchRemoved:   state.BranchRemoved,
		WorktreeRemoved: state.WorktreeRemoved,
		// The session the developer was working in is an artifact of the run in
		// the way the branch is: a repair continues it, and a re-run discards it
		// along with whatever it had not committed. Which of those two to decide
		// is what the entry is for.
		DeveloperSession: state.ProviderSessionID,
	}
	// The request the run published through is an artifact of the run the way
	// its branch is: a stopped or escalated run leaves it open on the forge, and
	// the entry is where the development manager learns that without going to
	// the forge for it.
	if state.PullRequest != nil {
		artifacts.PullRequest = state.PullRequest.Number
		artifacts.PullRequestURL = state.PullRequest.URL
		artifacts.PullRequestMerged = state.PullRequest.Merged
		artifacts.PullRequestMergeQueued = state.PullRequest.MergeQueued
	}
	return artifacts
}

func (d Docketer) validate() error {
	var problems []error
	if d.Docket == nil {
		problems = append(problems, errors.New("a triage docket is required"))
	}
	if d.Runs == nil {
		problems = append(problems, errors.New("the recorded runs are required to build a triage docket"))
	}
	if d.Decisions == nil {
		problems = append(problems, errors.New("the item's durable triage record is required, because every entry reports what triage has already decided about its item"))
	}
	return errors.Join(problems...)
}

func (d Docketer) now() time.Time {
	if d.Clock == nil {
		return execution.RealClock{}.Now()
	}
	return d.Clock.Now()
}
