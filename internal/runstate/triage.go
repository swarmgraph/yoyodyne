package runstate

// What triage has already spent on one piece of work, and what that work has
// cost in review rounds across every run of it.
//
// Triage decides what to do about a run that did not land: hand the developer
// another repair grant, cause the item to be run again, re-arm a merge the
// forge dropped. Each of those buys another attempt at the same work, and none
// of them is bounded by anything the run itself records — a run's repair budget
// is spent inside that run and starts again at zero in the next one. So an item
// triaged twice, and then twice more, is an item nothing was ever going to stop,
// and the record of what it had already been given lived in whichever
// conversation happened to be open at the time.
//
// This is the durable home for that: one record per work item, beside the runs
// rather than inside any of them, because what triage has spent on a piece of
// work outlives every run of it. Every counter is written before the action it
// counts takes effect, so a process that dies between the two has recorded a
// grant it did not give rather than given one it did not record — the direction
// that costs an unspent attempt rather than a duplicated one.
//
// It is one home per machine. Two collaborators running their own harnesses
// against one repository each hold a full set of budgets for the same item,
// which is a limit rather than a design: the coordination that would make them
// one is the team-mode epic's, and `docs/team-mode-scope.md` states it where
// that epic's designer will find it.
//
// All four counters have callers. Every run records its review rounds here,
// because a round is something the run itself produces, and the development
// manager's triage decisions spend the other three as they are recorded: a
// repair grant, a re-run, and a merge re-arm each go through the operation
// below that bounds it, and are refused by the cap they find.
//
// Spending the budget is not carrying the decision out, and the two are
// deliberately apart. Recording the decision is the role's; starting the run,
// continuing one, or asking a forge for anything, is the harness's own hand
// afterwards — the re-run and repair-continue actions are the two that exist,
// each bounded again by a record of its own: one re-run per docketed stoppage,
// and a grant spent no further than the continuations the item's runs record. So these operations stay the gate rather
// than the decision: an action records itself through one of them and is
// refused by the cap it finds, instead of arriving with a budget of its own
// invention.
//
// What each of those three writes beside the counter it spends is the decision
// that authorized it — which stoppage, whose reasoning, and where it was
// recorded — in the same update and under the same lock, so an item's record
// cannot say a budget was spent without saying what was decided. That is what
// the hand afterwards reads instead of taking words from whoever typed the
// command. See triagedecision.go.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// TriageCountersSchemaVersion is 1 and has never changed.
const TriageCountersSchemaVersion = 1

// maxTriageKeyPrefix bounds the readable half of a counter file's name. It
// exists so a directory listing says which item each file belongs to; the digest
// after it is what makes the name unique.
const maxTriageKeyPrefix = 60

// The triage actions a cap can refuse, named here because a refusal has to say
// which action it refused. They are the actions themselves rather than the
// counters: what an operator reads is "no more re-runs", not "the re-run
// counter".
const (
	TriageRepairGrant = "repair grant"
	TriageRerun       = "re-run"
	TriageMergeRearm  = "merge re-arm"
)

// The budgets an action is refused against, which are not the same vocabulary as
// the actions and must not be printed as though they were. A refusal that named
// only the action would leave an operator raising the wrong number.
//
// Two of the three actions buy review rounds and are refused by the round budget
// as well as by their own. Each also has a budget of its own, and it is not the
// same question: the round budget bounds what an item may cost, and an action's
// own budget bounds how many times triage may take that action on one item. An
// item whose runs stopped before any reviewer verdict has spent no round at all,
// so the round budget alone would bound nothing.
const (
	TriageReviewRoundBudget = "review round"
	TriageRepairGrantBudget = "repair grant"
	TriageRerunBudget       = "re-run"
	TriageMergeRearmBudget  = "merge re-arm"
)

// TriageCounters is what one work item has been given and what it has cost.
// Every field is a total across every run of the item, which is the whole
// reason the record is not kept on a run.
type TriageCounters struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	WorkItemID    string           `json:"work_item_id"`
	// RepairGrants is how many times triage has handed this item another go at
	// its own change, and GrantedRounds how many review rounds those grants added
	// up to. The two differ whenever a grant was cut down to what the round cap
	// still had room for, and TruncatedGrants is how often that happened: a grant
	// that was quietly halved and one that was given in full are different facts
	// about how close this item is to the end of its budget.
	RepairGrants    int `json:"repair_grants,omitempty"`
	GrantedRounds   int `json:"granted_rounds,omitempty"`
	TruncatedGrants int `json:"truncated_grants,omitempty"`
	// CommittedRounds is the round count this item's grants have committed it to:
	// what it had already cost when the most recent grant was recorded, plus what
	// that grant gave. It is what a further action that buys rounds is truncated
	// and refused against, and neither figure above can stand in for it. The
	// rounds counted cannot see a grant no attempt has spent yet, so two grants
	// taken before either produced a verdict would each be cut against the same
	// room and together promise more than the cap has; and the rounds granted
	// cannot say what the item had already cost before them.
	//
	// It is a reservation and not a spend, and it is released where the rounds it
	// promised are not going to be produced: by a granted attempt whose verdict
	// charged nothing, one round at a time, and by a decision that supersedes the
	// repair before its attempts are made, for what that repair reserved. A
	// commitment that outlived the rounds it stood for was the cap counting an
	// approving round and a round that never ran, which yoyodyne-ifd.349 and
	// yoyodyne-ifd.309 each paid an operator override for.
	//
	// A record written before this was counted carries zero, which is the
	// accounting that was in force when it was written rather than a record that
	// is wrong: it reads as an item nothing is outstanding on, which is what the
	// grant that wrote it was treated as at the time.
	CommittedRounds int `json:"committed_rounds,omitempty"`
	// Reruns is how many times triage has caused this item to be run again from
	// the start, and MergeRearms how many times it has re-armed a merge the forge
	// accepted and then dropped, across every publication of the item.
	Reruns      int `json:"reruns,omitempty"`
	MergeRearms int `json:"merge_rearms,omitempty"`
	// RearmedPublications is how much of that total each publication has had, keyed
	// by the publication the re-arm repeats. It is what the re-arm is actually
	// refused against, because a re-arm repeats one already-authorized merge
	// request rather than buying the item another attempt at anything: an item that
	// published three times has three separate merges the forge could drop, and a
	// budget spent across them would refuse the third publication its first re-arm
	// on the strength of the first publication's.
	//
	// A record written while the counter was kept per item names no publication and
	// carries the total alone, which is the accounting that was in force when it
	// was written rather than a record that is wrong. It reads as a publication
	// nothing has been re-armed about, which is what the per-item counter could
	// never tell anybody.
	RearmedPublications map[string]int `json:"rearmed_publications,omitempty"`
	// ReviewRounds is how many times a reviewer verdict has sent this item's work
	// back, across every run of it. It is the figure a repair grant is truncated
	// against, because it is the one that says what the item has actually cost:
	// repair budgets reset with each run and this does not.
	//
	// A re-review that no developer attempt produced is not a round. The
	// integration replay is the case: a promotion that loses its race replays the
	// same work onto where the target went and asks for a fresh verdict on it,
	// and counting that would charge the item for losing a race it did not cause.
	//
	// Neither is a verdict that approved the change, and for a version of the same
	// reason: the cap this feeds stops an item buying the same argument another
	// round, and an approval ends that argument. Neither is a repair whose whole
	// residue is one trivial finding, which is that same ending with a note
	// attached rather than another turn of the argument. Nor is any verdict on a
	// run with no change present, because a reviewer shown an empty diff was not
	// arguing with the change. What is counted is decided by whoever calls
	// RecordReviewRound, which is the run that obtained the verdict; this record
	// counts what it is handed.
	ReviewRounds int `json:"review_rounds,omitempty"`
	// LastJudged identifies the developer attempt a reviewer has most recently
	// answered about, whichever way the verdict went, and it is what makes the
	// second answer about one attempt free. That is a separate fact from the round
	// at the head below, and it has to be: an approval is not a round, so it
	// leaves no head, and the attempt an approved change was judged under would
	// otherwise be judged from scratch when a lost promotion replays it. The
	// replay's fresh verdict can be a repair — the ground moved under the change —
	// and charging that is charging the item for a race it did not cause, which is
	// exactly the round this record exists to keep off its bill.
	//
	// Consecutive is the whole of what has to be deduplicated, because it is the
	// whole of what can happen. A repeat is always a re-review of the attempt the
	// run has most recently been judged on, and nothing else can slip a round for
	// this item in between: a run reserves the item exclusively, so a second run
	// of the same item is refused while the first is in flight. An attempt
	// repeated after some other verdict of the same item had been obtained would
	// be a second run judging an attempt of the first, which is not a thing the
	// harness can produce.
	//
	// A record written before this was kept names nobody here and names its
	// counted round below, which is why the deduplication asks both: the item was
	// mid-flight when the executable changed under it, and a resumed review of the
	// attempt at that head must still be free.
	LastJudged string `json:"last_judged,omitempty"`
	// LastRound identifies the developer attempt whose verdict was counted last,
	// and only that one: it is the most recent round rather than a set of every
	// round counted. It is what a return of that round is matched against, and it
	// is not what deduplicates a re-review — LastJudged above is, because a verdict
	// that charged nothing still has to be remembered.
	//
	// It is cleared by a round given back, which is what makes the return honest:
	// a record still naming an attempt whose round is no longer counted would make
	// the next review of that attempt free.
	LastRound string `json:"last_round,omitempty"`
	// LastRoundCharger is the process that charged the round at the head, and it
	// is what a return of that round has to match. The attempt alone is not
	// enough: a run re-entered after its process died is the same run at the same
	// attempt number, so an attempt key names a round the process before this one
	// may have been the one to spend, and a later refusal at that attempt would
	// otherwise give back a round the item genuinely cost.
	//
	// It travels with the round rather than beside it. A charge and its credit are
	// one write under the item's lock, so a process that died between producing a
	// verdict and recording who produced it is not a state this can be in — which
	// is the whole reason it is here rather than on the run.
	//
	// A record written before rounds carried this names nobody, and a return
	// against it is refused for the same reason any mismatch is: nothing in the
	// record says this process is the one that charged it, and leaving a round
	// spent costs an item a round it should have kept, while crediting one it did
	// spend is a budget nothing bounds.
	LastRoundCharger string `json:"last_round_charger,omitempty"`
	// Overrides are the operator's recorded decisions to cross this item's caps,
	// in the order they were recorded. They are the one thing that gets past a cap
	// and they are kept here rather than beside the counters, so a guard reading
	// what an item has spent reads what it is permitted in the same breath and
	// under the same lock. An item nobody has decided anything like this about
	// carries none, which is every item. See triageoverride.go.
	Overrides []TriageOverride `json:"overrides,omitempty"`
	// Decisions are the development manager's own decisions about this item's
	// stoppages, one standing per stopped run. They are kept here for the reason
	// the overrides are, and for a sharper version of it: the counters say a
	// budget was spent and the decision says what was decided and by whom, so a
	// carry-out that read them from two places could act on a decision the spend
	// beside it never authorized. See triagedecision.go.
	Decisions []TriageDecision `json:"decisions,omitempty"`
	// CarryOuts are what became of the harness's own attempts to carry those
	// decisions out, one standing per stoppage that a gate stopped. They are kept
	// here for the reason the decisions are, and for the half the decisions cannot
	// answer: a decision recorded and a decision carried out look identical from
	// this record until something says which, and an item whose decision a gate has
	// been refusing for days is exactly the silence that put thirty-three of them
	// here. An item nothing has refused carries none, which is nearly every item.
	// See triagecarryout.go.
	CarryOuts []TriageCarryOut `json:"carry_outs,omitempty"`
	// PendingCarryOutNotes are the exact permanent-refusal notes still owed to
	// the tracker item. They survive a finding being cleared or superseded.
	PendingCarryOutNotes []string  `json:"pending_carry_out_notes,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Validate reports every contract violation in the record at once.
func (c TriageCounters) Validate() error {
	var problems []error
	if c.SchemaVersion != TriageCountersSchemaVersion {
		problems = append(problems, fmt.Errorf("triage counter schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(c.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	for _, counter := range []struct {
		field string
		value int
	}{
		{"repair_grants", c.RepairGrants},
		{"granted_rounds", c.GrantedRounds},
		{"truncated_grants", c.TruncatedGrants},
		{"committed_rounds", c.CommittedRounds},
		{"reruns", c.Reruns},
		{"merge_rearms", c.MergeRearms},
		{"review_rounds", c.ReviewRounds},
	} {
		if counter.value < 0 {
			problems = append(problems, fmt.Errorf("%s cannot be negative", counter.field))
		}
	}
	// A grant that was cut is a grant that was given, and rounds are only ever
	// granted by one. Either the other way round describes a record that cannot
	// have been written by the only thing that writes them.
	if c.TruncatedGrants > c.RepairGrants {
		problems = append(problems, fmt.Errorf("%d grants are recorded as truncated of %d given", c.TruncatedGrants, c.RepairGrants))
	}
	if c.GrantedRounds > 0 && c.RepairGrants == 0 {
		problems = append(problems, errors.New("granted rounds require the grant that gave them"))
	}
	// Committed rounds are written by a grant and by nothing else, so a record
	// carrying them without one could not have been written by the only thing
	// that writes it. The other way round is ordinary rather than a violation: a
	// record written before committed rounds were counted carries a grant and no
	// commitment.
	if c.CommittedRounds > 0 && c.RepairGrants == 0 {
		problems = append(problems, errors.New("committed rounds require the grant that committed them"))
	}
	// The per-publication re-arms are a breakdown of the total rather than a
	// second count of the same thing, so a breakdown larger than the total could
	// not have been written by the only thing that writes both. The other way
	// round is ordinary: it is every record written while the counter was per
	// item, and every one where an operator's override was crossed on a
	// publication some earlier record already counted.
	rearmed := 0
	for publication, count := range c.RearmedPublications {
		if strings.TrimSpace(publication) == "" {
			problems = append(problems, errors.New("a re-armed publication is required to name the publication it re-armed"))
		}
		if count < 1 {
			problems = append(problems, fmt.Errorf("publication %q is recorded with %d re-arm(s), which is not a re-arm", publication, count))
			continue
		}
		rearmed += count
	}
	if rearmed > c.MergeRearms {
		problems = append(problems, fmt.Errorf("%d re-arm(s) are recorded against publications of %d recorded in total", rearmed, c.MergeRearms))
	}
	if c.LastRound != "" && c.ReviewRounds == 0 {
		problems = append(problems, errors.New("a counted round requires the round count that includes it"))
	}
	// A charger is written with the round it charged and cleared with it, so one
	// standing alone could not have been written by the only thing that writes it.
	// The other way round is ordinary: a record written before rounds carried the
	// process that charged them names an attempt and nobody.
	if c.LastRoundCharger != "" && c.LastRound == "" {
		problems = append(problems, errors.New("a charging process requires the round it charged"))
	}
	problems = append(problems, validateTriageOverrides(c.Overrides)...)
	problems = append(problems, validateTriageDecisions(c.Decisions)...)
	problems = append(problems, validateTriageCarryOuts(c.CarryOuts)...)
	problems = append(problems, validatePendingCarryOutNotes(c.PendingCarryOutNotes)...)
	if c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage counters: %w", err)
	}
	return nil
}

// Passes is how many times triage has acted on this item. Each of the three
// actions is one pass, because each is one decision somebody made about work
// that did not land; a pass that decided to do nothing spends nothing and is not
// one of these. Two or more is what a reader is looking for — an item nobody
// triaged and an item triaged repeatedly are the two ends of what this record
// exists to tell apart — and it is answerable from the record alone.
func (c TriageCounters) Passes() int {
	return c.RepairGrants + c.Reruns + c.MergeRearms
}

// RearmsOf is how many re-arms triage has recorded against one publication of
// this item. It is the figure the re-arm guard refuses against, and a
// publication nothing has been decided about answers zero, which is where every
// publication starts.
func (c TriageCounters) RearmsOf(publicationKey string) int {
	return c.RearmedPublications[strings.TrimSpace(publicationKey)]
}

// TriagedAgain reports an item triage has come back to. It is the question a
// reader of one item's record asks first: everything is worth one look, and a
// second says the first look did not settle it.
func (c TriageCounters) TriagedAgain() bool { return c.Passes() > 1 }

// RoundsRemaining is how many further review rounds this item may still cost
// under a cap. It is never negative: an item already past its cap has nothing
// remaining rather than a debt.
//
// It is what the item has cost measured against the cap, and it is the figure a
// reader is shown. What may still be granted is RoundsUncommitted, which is the
// same question asked of a budget part of which may already be promised.
func (c TriageCounters) RoundsRemaining(limit int) int {
	if remaining := limit - c.ReviewRounds; remaining > 0 {
		return remaining
	}
	return 0
}

// RoundsUncommitted is how many further rounds a triage action may still buy
// under a cap: the room left beyond both what this item has cost and what a
// grant already recorded has promised it. It is what the actions that buy rounds
// are truncated and refused against, because a grant's rounds are spendable from
// the moment the grant is recorded, and the rounds counted cannot see them until
// an attempt the grant bought is judged.
//
// On an item with nothing outstanding it is the rounds remaining, so the two
// differ only while a grant is waiting to be carried out.
func (c TriageCounters) RoundsUncommitted(limit int) int {
	if remaining := limit - c.committed(); remaining > 0 {
		return remaining
	}
	return 0
}

// GrantOutstanding reports a repair grant recorded whose rounds the item has not
// spent yet, which is what says a granted repair has not been carried out: a
// grant is spendable from the moment it is written and turns into counted rounds
// as the attempts it bought are judged, so the two figures differ exactly while
// the grant is waiting to be acted on.
func (c TriageCounters) GrantOutstanding() bool { return c.CommittedRounds > c.ReviewRounds }

// committed is what this item has cost or what it stands committed to,
// whichever is greater. The two are not added: a grant's rounds turn into
// counted rounds as the attempts it bought are judged, so a sum would charge a
// carried-out grant twice and shrink the budget of an item that spent exactly
// what it was given.
func (c TriageCounters) committed() int {
	if c.CommittedRounds > c.ReviewRounds {
		return c.CommittedRounds
	}
	return c.ReviewRounds
}

// reserved is how many rounds a grant has committed this item to beyond what it
// has spent: the reservation still standing. It is never negative, because an
// item whose rounds have caught up with its commitment has nothing reserved.
func (c TriageCounters) reserved() int {
	if outstanding := c.CommittedRounds - c.ReviewRounds; outstanding > 0 {
		return outstanding
	}
	return 0
}

// releaseReservedRounds gives back rounds a grant reserved and the item is not
// going to spend, and it is the only thing besides a grant that moves the
// commitment. A reservation is a promise that attempts the harness will hand a
// run may each cost a round; a round that ended in a verdict the cap does not
// count, and a round the harness never ran, were reserved and are not spent, and
// a commitment that went on holding them was the cap counting rounds nothing
// produced. That is what refused yoyodyne-ifd.349's re-run at 4 of 4 with three
// rounds spent, and yoyodyne-ifd.309's at 6 of 6 with three.
//
// It releases at most what is reserved, whatever it is asked for, so a
// reservation already spent through — the item's rounds caught up with its
// commitment — releases nothing. Zero asks for everything outstanding, which is
// what a repair recorded before decisions carried their reservation has to be
// released by: such a record can be holding one grant's rounds and nothing says
// how many.
func (c *TriageCounters) releaseReservedRounds(rounds int) {
	outstanding := c.reserved()
	if outstanding == 0 {
		return
	}
	if rounds <= 0 || rounds > outstanding {
		rounds = outstanding
	}
	c.CommittedRounds -= rounds
}

// TriageCaps bounds each triage action for one work item. They are per item and
// per machine, so they bound what one harness will spend on one piece of work
// however many runs it takes.
//
// Every one of them is supplied rather than decided here: this is deliberately
// not the place that invents a number, and a caller assembling them is where
// each one's size is argued for. `triage.review_rounds_cap` is the ceiling on
// what an item may cost, and both of the actions that buy rounds — another
// repair, another whole run — are refused against it as well as against their
// own. Each action also has a bound of its own, because the rounds bound what an
// item costs rather than how often triage may decide the same thing about it,
// and an item whose runs never reach a reviewer costs no rounds at all.
//
// What is passed in is the configured ceiling, and every guard below refuses
// against these as one item's recorded overrides leave them — Overridden is what
// applies them. An override is the operator's own decision to cross a cap, and it
// is the only thing that does; nothing an agent records reaches one. See
// triageoverride.go.
type TriageCaps struct {
	// ReviewRounds is the ceiling on the rounds one item may accumulate across
	// every run of it: `triage.review_rounds_cap`. It is what a repair grant is
	// truncated against and what refuses a re-run, and it is not an action
	// anybody asks permission for — nothing asks to be reviewed, and the rounds
	// an item has spent are a fact rather than a request.
	ReviewRounds int `json:"review_rounds"`
	// RepairGrants and Reruns bound how many times triage may take each of those
	// actions on one item, whatever the rounds say. They are not the same bound
	// as the rounds and neither replaces it: the rounds bound what an item may
	// cost, and these bound how often triage may decide the same thing about it.
	//
	// Without them the round budget is the only bound, and it bounds nothing at
	// all for the work that most needs bounding: a run that stopped before any
	// reviewer verdict — a provider that kept refusing, a replay that conflicts,
	// integration retries spent — has produced no round, so an item whose runs
	// all stop that way could be handed back and re-run for ever with every
	// counter reading zero.
	RepairGrants int `json:"repair_grants"`
	Reruns       int `json:"reruns"`
	// MergeRearms bounds the one action that buys no round, and it bounds it per
	// publication rather than per item: what a re-arm repeats is one merge request
	// the reviewer's verdict already authorized, so an item that published three
	// times has three of them and a budget spent across them would refuse the
	// third publication for what the first cost. An action that costs no provider
	// invocation is the one that can be taken forever, and a merge the forge keeps
	// dropping is a repository somebody has to look at.
	MergeRearms int `json:"merge_rearms"`
}

// Validate refuses a cap set nothing could be spent against. A cap of zero is a
// deliberate choice — never grant this — and a negative one is not a choice at
// all.
func (c TriageCaps) Validate() error {
	var problems []error
	for _, limit := range []struct {
		field string
		value int
	}{
		{"review round cap", c.ReviewRounds},
		{"repair grant cap", c.RepairGrants},
		{"re-run cap", c.Reruns},
		{"merge re-arm cap", c.MergeRearms},
	} {
		if limit.value < 0 {
			problems = append(problems, fmt.Errorf("%s cannot be negative", limit.field))
		}
	}
	return errors.Join(problems...)
}

// ErrTriageCapReached is what every refusal past a cap unwraps to, so a caller
// can tell "this item has had its budget" from a store that could not be read
// without matching on the words of either.
var ErrTriageCapReached = errors.New("triage cap reached")

// TriageCapRefusal is one budget refusing one action: what it bounds, what the
// item has spent against it, and what it stands at.
//
// It carries the spend because an operator answering the refusal has to state a
// new ceiling, and a refusal that named only the budget left them reconstructing
// the figure from the item's record before they could type a number. What they
// need is arithmetic they can read off the sentence.
type TriageCapRefusal struct {
	Budget string
	Spent  int
	Cap    int
}

// Permits is the smallest cap that would let the refused action through: one
// more than what is already spent. It is what an operator's override states, so
// it is computed here rather than left to whoever renders the refusal — two
// places deriving it is two places to get it wrong by one.
func (r TriageCapRefusal) Permits() int { return r.Spent + 1 }

// Describe says what one budget refused, in the words an operator reads.
func (r TriageCapRefusal) Describe() string {
	if r.Cap == 0 {
		return fmt.Sprintf("the %s cap is 0, so none is permitted", r.Budget)
	}
	return fmt.Sprintf("%d of %d permitted %s(s) are spent", r.Spent, r.Cap, r.Budget)
}

// triageBudgetSpend is what one item has spent against one budget, in the figure
// that budget's own refusal reports.
//
// It is one function rather than a figure written out at each refusal because two
// things read it and they must not disagree: the refusal, which tells a reader
// what would permit the decision, and the delegated crossing, which puts that
// ceiling in force. A crossing measured from a different figure than the refusal
// quoted is a crossing that does not answer the refusal that produced it.
//
// The round budget answers with what the item is committed to rather than what it
// has cost, for the reason the refusal states: a grant recorded and not yet spent
// has already promised its rounds, so the rounds counted would name room the cap
// does not have.
//
// The re-arm budget is the one whose refusal does not read this, because that
// refusal is made per publication and names the one publication in front of it.
// A crossing has no publication in hand — it raises the ceiling every publication
// of the item is held to — so it measures from the publication that has spent the
// most, which is the one that refused whenever any did: one more than that is
// exactly the ceiling the refusal quoted, and never less.
func triageBudgetSpend(counters TriageCounters, budget string) int {
	switch budget {
	case TriageReviewRoundBudget:
		return counters.committed()
	case TriageRepairGrantBudget:
		return counters.RepairGrants
	case TriageRerunBudget:
		return counters.Reruns
	default:
		most := 0
		for _, rearms := range counters.RearmedPublications {
			most = larger(most, rearms)
		}
		return most
	}
}

// TriageCapError names the action that was refused and every budget that refused
// it, each with what was already spent against it. The action and the budgets are
// separate because they are frequently not the same thing: a re-run is refused
// by the review round budget, and an operator told only "re-run refused" would
// go looking for a re-run cap that does not exist.
//
// Every budget rather than the first, because two of the three actions stand
// behind two budgets and an item can be at the end of both. Refusing on the
// first and staying silent about the second is a refusal that is true and
// incomplete: the operator crosses the cap it named, the same decision is asked
// for again, and the second budget refuses it in turn. That is not hypothetical
// — on 2026-09-05 it cost two override ceremonies two minutes apart on each of
// yoyodyne-ifd.272 and yoyodyne-ifd.209.20, four recorded overrides for two
// decisions. One refusal naming both is one sitting.
type TriageCapError struct {
	Action     string
	WorkItemID string
	// Publication names the publication a budget spent per publication rather
	// than per item was refused against. It is empty on every other budget, and
	// saying it is what keeps the refusal readable: an operator told only that a
	// re-arm was refused for the item would go looking for a per-item figure that
	// no longer decides anything.
	Publication string
	// Refusals are the budgets that refused, in the order the action asks them:
	// the action's own budget first, then the rounds it would buy. There is always
	// at least one, and a refusal carrying none could not have been built by the
	// only thing that builds them.
	Refusals []TriageCapRefusal
}

func (e TriageCapError) Error() string {
	against := e.WorkItemID
	if publication := strings.TrimSpace(e.Publication); publication != "" {
		against = fmt.Sprintf("publication %s of %s", publication, e.WorkItemID)
	}
	spends := make([]string, 0, len(e.Refusals))
	permits := make([]string, 0, len(e.Refusals))
	for _, refusal := range e.Refusals {
		spends = append(spends, refusal.Describe())
		permits = append(permits, fmt.Sprintf("a %s cap of %d", refusal.Budget, refusal.Permits()))
	}
	return fmt.Sprintf("%s is refused for %s: %s. What permits it is %s",
		e.Action, against, strings.Join(spends, ", and "), joinWithAnd(permits))
}

func (e TriageCapError) Unwrap() error { return ErrTriageCapReached }

// Budgets are the budgets that refused, in the order the refusal names them.
// It is what a caller turning a refusal into commands for the operator walks.
func (e TriageCapError) Budgets() []string {
	budgets := make([]string, 0, len(e.Refusals))
	for _, refusal := range e.Refusals {
		budgets = append(budgets, refusal.Budget)
	}
	return budgets
}

// RefusedBy is what one named budget refused, and whether it was one of the
// budgets that refused at all.
func (e TriageCapError) RefusedBy(budget string) (TriageCapRefusal, bool) {
	for _, refusal := range e.Refusals {
		if refusal.Budget == budget {
			return refusal, true
		}
	}
	return TriageCapRefusal{}, false
}

// joinWithAnd reads a list out the way a sentence does, because this one is read
// by an operator rather than parsed: two budgets joined by a comma read as a list
// that might continue.
func joinWithAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// RepairGrant is what a granted repair actually came to. Rounds is what may be
// spent, which is what the caller acts on; Requested and Truncated are what was
// asked for and whether the round cap cut it, because a grant of one where two
// were asked for is a different thing to tell somebody than a grant of one.
type RepairGrant struct {
	Requested int
	Rounds    int
	Truncated bool
	Counters  TriageCounters
}

// MergeRearmDecision is what recording a re-arm came to: the publication whose
// budget it spent, and the item's counters with it on them. The publication is
// reported rather than left to be worked out, because what a caller names is the
// run whose merge was dropped and what the budget is keyed to is the publication
// that run made — so the record and whatever the caller says about it afterwards
// name one thing rather than two.
type MergeRearmDecision struct {
	Publication string
	Counters    TriageCounters
}

// Rearms is what the publication this decision was recorded against now stands
// at, which is the figure that refuses the next one.
func (d MergeRearmDecision) Rearms() int { return d.Counters.RearmsOf(d.Publication) }

// TriageStore is where the counters live: one directory under the product,
// beside the runs rather than among them, and one file per work item inside it.
// A file each rather than one shared record is what keeps concurrent runs of
// different items out of each other's way — the harness runs several at once,
// and every one of them records its review rounds.
type TriageStore struct {
	root      string
	productID domain.ProductID
}

func NewTriageStore(root string, productID domain.ProductID) (*TriageStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &TriageStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "triage"),
		productID: productID,
	}, nil
}

// Triage is the counter store for this run store's product. It is reached from
// here because the two are one product's durable record: whoever can read what
// became of an item's runs can read what triage has spent on the item, without
// having to be told the state root a second time.
func (s *Store) Triage() *TriageStore {
	return &TriageStore{
		root:      filepath.Join(filepath.Dir(s.root), "triage"),
		productID: s.productID,
	}
}

func (s *TriageStore) Root() string { return s.root }

// Counters reports what one work item has been given and what it has cost. An
// item nothing has recorded anything about carries zero of everything, which is
// the ordinary answer rather than a failure to look: every item starts there.
// A record that cannot be read is not that, and is an error, because an
// unreadable budget must never be spent through as though it were empty.
func (s *TriageStore) Counters(workItemID string) (TriageCounters, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return TriageCounters{}, errors.New("a work item is required to read its triage counters")
	}
	return s.load(id)
}

// LockCounters reads a decision while preventing another process from replacing
// it until release is called. Artifact retirement holds this lock through its
// repository mutation so a recovery grant cannot arrive between the decision
// check and the removal it was meant to prevent. The caller must not update the
// same item's triage record while holding it.
func (s *TriageStore) LockCounters(ctx context.Context, workItemID string) (TriageCounters, func(), error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return TriageCounters{}, nil, errors.New("a work item is required to lock its triage counters")
	}
	release, err := s.lock(ctx, id)
	if err != nil {
		return TriageCounters{}, nil, err
	}
	counters, err := s.load(id)
	if err != nil {
		release()
		return TriageCounters{}, nil, err
	}
	return counters, release, nil
}

// RecordReviewRound counts one round against a work item and reports what the
// item now stands at. It never refuses: a round is something that happened
// rather than something being asked for, and a cap that could stop it being
// written down would only make the record disagree with the world.
//
// Which verdicts are rounds is the caller's, not this record's — an approval is
// not one, nor is a repair whose whole residue is one trivial finding, and the
// run that obtained the verdict is what knows. What those are instead is
// RecordUnchargedVerdict below, which every caller obtaining a verdict has to
// reach for, because the deduplication here is what keeps a re-review of a judged
// attempt free and a verdict that reached neither of these is an attempt the
// record has never heard of.
//
// attemptID identifies the developer attempt whose change was judged, and an
// attempt this item has already been answered about is recorded once. That is
// what excludes the integration replay: the replayed change is re-reviewed under
// the attempt that produced it, so the second verdict names an attempt already
// judged — whether the first verdict cost a round or approved and cost nothing.
// It is also what makes an interrupted review safe to resume.
//
// chargedBy is the process counting it, kept with the round so that only that
// process can give it back. An attempt already judged keeps whatever the record
// holds rather than acquiring the process asking: the item was charged at most
// once, by whoever produced the verdict, and a re-review that counted nothing has
// bought nothing to give back.
//
// Only the most recent verdict is compared against, which is sufficient rather
// than approximate; LastJudged says why.
func (s *TriageStore) RecordReviewRound(ctx context.Context, workItemID, attemptID, chargedBy string, at time.Time) (TriageCounters, error) {
	if strings.TrimSpace(attemptID) == "" {
		return TriageCounters{}, errors.New("a developer attempt is required to count the round its review produced")
	}
	if strings.TrimSpace(chargedBy) == "" {
		return TriageCounters{}, errors.New("a charging process is required to count a review round")
	}
	return s.update(ctx, workItemID, at, func(counters *TriageCounters) error {
		if counters.alreadyJudged(attemptID) {
			return errNoTriageChange
		}
		counters.ReviewRounds++
		counters.LastJudged = attemptID
		counters.LastRound = attemptID
		counters.LastRoundCharger = chargedBy
		return nil
	})
}

// RecordUnchargedVerdict records that a reviewer answered about one developer
// attempt and the answer costs the item nothing, which is still something the
// record has to hold.
//
// Two verdicts reach it, and which ones is the caller's judgement rather than
// this record's. An approval is one: the cap this feeds exists to stop an item
// buying the same argument another round, and an approval ends that argument.
// The other is a repair whose whole residue is one trivial finding — the
// reviewer said the work is right and named one small thing beside it, which is
// the same end of the same argument with a note attached. Charging either walks
// an item toward its cap on its own success, which is what four escalations and
// two override ceremonies in a week were.
//
// It exists because the exclusion and the deduplication are the same mechanism.
// A verdict that went unrecorded would leave the attempt looking unjudged, and
// the one thing that re-reviews an already-judged attempt is the integration
// replay: a promotion that lost its race replays the change onto where the target
// went and asks for a fresh verdict on it. That verdict can be a repair — the
// ground moved — and with nothing saying the attempt had already been answered
// about, the item would be charged a round for losing a race it did not cause.
// That is the round the replay exclusion has always promised to keep off the
// bill, and this is what keeps the promise now that not every verdict is a round.
//
// The third is a verdict of any kind on a run with no change present: the
// reviewer was shown an empty diff, and whatever it said about nothing is not the
// change disputing with it. The development manager reported rounds of that
// shape counting identically to real repair rounds, and yoyodyne-ifd.391 rules
// them out with the other two: the cap counts only rounds that ended in a
// verdict requiring repair against a change that was present.
//
// No charging process is asked for, because nothing is charged. An uncharged
// verdict leaves the round at the head, and whatever process holds it, exactly as
// it found them: a later round given back is still that process's to give.
//
// What it does move is the reservation, where a grant is holding one for this
// attempt. A granted attempt is judged exactly once, and the round the grant
// reserved for it is resolved by that judgement: a repair verdict spends it, and
// a verdict that charges nothing releases it. Leaving it committed is the
// reservation counting the round the cap does not — which is how
// yoyodyne-ifd.349, its granted round approved and its promotion then
// conflicted, was refused a re-run at 4 of 4 with three rounds spent.
//
// Whether the reservation is this attempt's is read from the decisions: a grant
// is carried out by continuing the run it was decided about, so a granted
// attempt is one of that run's, and a standing repair naming the attempt's run is
// what says the round was reserved for it. An uncharged verdict in some other
// run — a re-run started beside a grant standing on another stoppage of the item
// — releases nothing, because the round it judged was never reserved. A
// reservation with no repair decision standing anywhere on the record is a grant
// recorded before decisions were durable, and that one is released by whichever
// attempt is judged, since nothing on the record can say whose it is.
func (s *TriageStore) RecordUnchargedVerdict(ctx context.Context, workItemID, attemptID string, at time.Time) (TriageCounters, error) {
	if strings.TrimSpace(attemptID) == "" {
		return TriageCounters{}, errors.New("a developer attempt is required to record the verdict that cost it nothing")
	}
	return s.update(ctx, workItemID, at, func(counters *TriageCounters) error {
		if counters.alreadyJudged(attemptID) {
			return errNoTriageChange
		}
		counters.LastJudged = attemptID
		if counters.reservedFor(attemptID) {
			counters.releaseReservedRounds(1)
		}
		return nil
	})
}

// reservedFor reports a standing reservation being one made for the run the
// attempt belongs to. See RecordUnchargedVerdict for the two readings.
func (c TriageCounters) reservedFor(attemptID string) bool {
	if c.reserved() == 0 {
		return false
	}
	runID, _, _ := strings.Cut(attemptID, "#")
	repairStanding := false
	for _, decision := range c.Decisions {
		if decision.Decision != TriageDecisionRepair {
			continue
		}
		if decision.RunID == runID {
			return true
		}
		repairStanding = true
	}
	return !repairStanding
}

// alreadyJudged reports an attempt a reviewer has already answered about, which
// is what makes the second answer free.
//
// It asks the counted round as well as the judged attempt, and that is a
// compatibility clause rather than a second rule: a record written before
// approvals were remembered names its attempt only at the round it charged, and
// an item that was mid-flight when the executable changed under it must not have
// its resumed review charged twice.
func (c TriageCounters) alreadyJudged(attemptID string) bool {
	return c.LastJudged == attemptID || c.LastRound == attemptID
}

// ReturnReviewRound gives back the round one developer attempt's review was
// counted as, and reports whether there was one to give back. It is the only
// thing that ever lowers the count, and it exists for one reason: a round whose
// diff was empty because the environment handed it nothing is a round the item
// did not cost. Charging it walks the item toward a cap on the harness's
// failures rather than on its own, which is an escalation that reads afterwards
// as work nobody could finish.
//
// It is deliberately not the mirror of RecordReviewRound. That one never
// refuses, because a round is something that happened; this one refuses
// everything except the round at the head of the record charged by the process
// asking, so it can only ever give back a round this caller is the one that
// counted. A caller naming an attempt the record does not stand at gets its
// counters back unchanged and is told nothing was returned, rather than a
// decrement against somebody else's round.
//
// The charger is asked as well as the attempt, because the attempt alone does
// not say whose round it is. An attempt is keyed to a run and how many repairs
// into it the round is, and a run re-entered after its process died is that same
// run at that same attempt: a refusal in the new process would otherwise find
// its own key at the head and give back a round the process before it spent on a
// verdict the item really got. A mismatch is reported rather than decremented,
// so the settle that asked can say the round was left spent instead of claiming
// the item stands where it did.
//
// What it does not touch is the grant. A grant is a decision the development
// manager recorded, and it still stands after an environmental refusal — what
// was never spent is the run's carrying it out, which the run's own record says.
// The commitment a grant wrote stays for the same reason: the rounds it promised
// are still promised.
func (s *TriageStore) ReturnReviewRound(ctx context.Context, workItemID, attemptID, chargedBy string, at time.Time) (TriageCounters, RoundReturn, error) {
	if strings.TrimSpace(attemptID) == "" {
		return TriageCounters{}, RoundReturn{}, errors.New("a developer attempt is required to return the round its review was counted as")
	}
	if strings.TrimSpace(chargedBy) == "" {
		return TriageCounters{}, RoundReturn{}, errors.New("a charging process is required to return a review round")
	}
	var outcome RoundReturn
	counters, err := s.update(ctx, workItemID, at, func(counters *TriageCounters) error {
		if counters.LastRound != attemptID || counters.ReviewRounds < 1 {
			return errNoTriageChange
		}
		if counters.LastRoundCharger != chargedBy {
			outcome.Mismatched = true
			outcome.ChargedBy = counters.LastRoundCharger
			return errNoTriageChange
		}
		counters.ReviewRounds--
		// The head is cleared with the round it named, and its charger with it.
		// Leaving either would leave the record saying a round was counted for an
		// attempt whose round has been given back, which is the one reading that
		// makes the next review of the same attempt free.
		counters.LastRound = ""
		counters.LastRoundCharger = ""
		// And the judged attempt with them, where it is this one. A returned round
		// is a round the environment refused, so the attempt is going to be judged
		// again and has to be chargeable when it is; leaving it named here is the
		// same free review by the other door.
		if counters.LastJudged == attemptID {
			counters.LastJudged = ""
		}
		outcome.Returned = true
		return nil
	})
	if err != nil {
		return TriageCounters{}, RoundReturn{}, err
	}
	return counters, outcome, nil
}

// RoundReturn is what asking for a review round back came to.
//
// Returned and Mismatched are different answers and a caller must not read one
// as the other. Nothing returned can mean the record stands at some other round
// entirely, which says nothing about this one; Mismatched says the round asked
// for is exactly the one at the head and another process is credited with it, so
// it stays spent and the item does not stand where it did before the round.
type RoundReturn struct {
	Returned   bool
	Mismatched bool
	// ChargedBy is the process holding the round, on a mismatch. It is empty on a
	// record written before rounds carried the process that charged them, which is
	// a mismatch all the same: a round nothing attributes is not one this caller
	// can prove it spent.
	ChargedBy string
}

// NewChargingProcess mints the identity one process charges review rounds under.
//
// It is minted rather than derived from the run, because what it has to tell
// apart is two processes carrying the same run: a run re-entered after an
// interrupted process is the same run identifier at the same attempt number, and
// a derivation from either would give the second process the first one's
// credit. It carries this process's number so a record naming it says something
// to whoever reads it, and the random half is what makes it unique — process
// numbers are reused, and a reused one matching would be the whole bug back.
func NewChargingProcess() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate charging process id: %w", err)
	}
	return fmt.Sprintf("pid-%d-%s", os.Getpid(), hex.EncodeToString(bytes)), nil
}

// GrantRepair records a repair grant and reports what it came to. The grant is
// truncated to the review rounds the cap still has room for, because a grant
// larger than that is unspendable: at the harness defaults an item that has been
// through its repair budget once has spent three rounds of four, so a grant of
// the configured two attempts would promise a round nothing would ever let it
// take, and a grant that simply overshot the cap would make the cap decorative.
// The truncation is recorded rather than applied silently — a grant that was cut
// is what says this item is at the end of what it will be given.
//
// The room it is truncated to counts a grant already recorded against the item,
// not only the rounds the item has produced. A grant is spendable from the
// moment it is written, so two taken before either is carried out would
// otherwise be cut against the same room and promise between them more than the
// cap has, with neither one overshooting it.
//
// rounds is what `triage.repair_grant_attempts` states, which is a count of
// repair attempts and is therefore also a count of rounds: every repair attempt
// is judged, so each one an item is granted is one more verdict it will produce.
//
// It is written before the developer is handed anything, so a process that dies
// between the two has spent a grant it did not give. That is the direction this
// record exists to fail in: the other one is a grant given twice.
//
// The decision that bought it is written in the same update, because a spend is
// only ever authorized by one: what the counters would say on their own is that
// somebody was given a grant, and what a later reader needs is which stoppage it
// was about and who decided it. See triagedecision.go.
func (s *TriageStore) GrantRepair(ctx context.Context, workItemID string, decision TriageDecision, rounds int, at time.Time, caps TriageCaps) (RepairGrant, error) {
	if rounds < 1 {
		return RepairGrant{}, errors.New("a repair grant gives at least one round")
	}
	if err := caps.Validate(); err != nil {
		return RepairGrant{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	decided, err := spendingDecision(decision, TriageDecisionRepair, when)
	if err != nil {
		return RepairGrant{}, err
	}
	granted := RepairGrant{Requested: rounds}
	counters, err := s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		// The caps as the operator's own recorded decisions leave them. Every
		// refusal below is against these rather than against what the project
		// configured, which is the whole of what makes an override executable: the
		// operator crosses the cap once, in a record, and the guard that refused
		// their decision permits it without anybody going round it.
		permitted := caps.Overridden(counters.Overrides)
		// Both budgets are asked before either refuses, so an item at the end of
		// both is told so once. The grant's own budget is asked first because it is
		// the bound that answers on an item the rounds say nothing about — a run
		// that stopped before it was ever reviewed spent no round, and the round
		// budget would hand that item back for ever — and because it is the order
		// an operator reads the refusal in.
		var refusals []TriageCapRefusal
		if counters.RepairGrants >= permitted.RepairGrants {
			refusals = append(refusals, TriageCapRefusal{
				Budget: TriageRepairGrantBudget,
				Spent:  triageBudgetSpend(*counters, TriageRepairGrantBudget),
				Cap:    permitted.RepairGrants,
			})
		}
		// The room is what the cap has beyond what the item stands committed to,
		// rather than beyond what it has cost. A grant recorded and not yet carried
		// out has already promised its rounds, and a second one truncated against
		// the same room is how the rounds granted overshoot the cap without any
		// single grant doing so.
		remaining := counters.RoundsUncommitted(permitted.ReviewRounds)
		if remaining == 0 {
			refusals = append(refusals, TriageCapRefusal{
				Budget: TriageReviewRoundBudget,
				// What refused it is what it is committed to, so that is the figure
				// reported: naming the rounds counted would leave a reader looking for
				// room the cap does not have.
				Spent: triageBudgetSpend(*counters, TriageReviewRoundBudget),
				Cap:   permitted.ReviewRounds,
			})
		}
		if len(refusals) > 0 {
			return TriageCapError{
				Action:     TriageRepairGrant,
				WorkItemID: counters.WorkItemID,
				Refusals:   refusals,
			}
		}
		granted.Rounds = rounds
		if granted.Rounds > remaining {
			granted.Rounds = remaining
			granted.Truncated = true
			counters.TruncatedGrants++
		}
		counters.RepairGrants++
		counters.GrantedRounds += granted.Rounds
		counters.CommittedRounds = counters.committed() + granted.Rounds
		// The decision carries what it reserved, so a decision that supersedes it
		// can release exactly that rather than everything the item stands committed
		// to.
		decided.Rounds = granted.Rounds
		return counters.recordDecision(decided)
	})
	if err != nil {
		return RepairGrant{}, err
	}
	granted.Counters = counters
	return granted, nil
}

// RecordRerun records that triage caused this item to be run again, and refuses
// once the item has no review rounds left. A re-run buys a whole fresh run,
// repair budget and all, so what it costs is not one round but every round the
// new run goes on to spend — and an item with none remaining would produce its
// first verdict already past the cap.
//
// Like a grant it is written before the run it causes is started, so a crash
// between the two costs a re-run nobody took rather than a re-run nobody
// counted.
//
// The round cap this spends is one precondition among several, not the gate
// entire: the action that calls this claims a work item the operator did not
// name, so `selected-work-passes-intake-and-records-why` also requires the
// product's intake hold read and found clear before the claim, and the triage
// reason recorded as the run's selection reason. Both belong to the caller —
// this store counts budgets, it does not start runs — and an action that
// reached here without them has gone around the invariant, not through it.
//
// The decision itself is written in the same update, and for the re-run that is
// the half the invariant turns on: the selection reason a fresh run records
// names the development manager, and what makes that attribution evidence rather
// than an assertion is that the words in it are read back from what the role
// recorded here. See triagedecision.go.
func (s *TriageStore) RecordRerun(ctx context.Context, workItemID string, decision TriageDecision, at time.Time, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	decided, err := spendingDecision(decision, TriageDecisionRerun, when)
	if err != nil {
		return TriageCounters{}, err
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		// The decision is put on the record ahead of the budget it is measured
		// against, because recording it is what releases a repair it supersedes: a
		// re-run decided in place of a repair the harness could not carry out has
		// to be measured against the rounds that repair reserved and nothing will
		// spend, or it is refused for them — which is what yoyodyne-ifd.309 was.
		// Nothing is saved on a refusal, so a decision recorded here and then
		// refused below was never recorded.
		if err := counters.recordDecision(decided); err != nil {
			return err
		}
		// The caps as the operator's own recorded decisions leave them, for the
		// reason a grant's are: this refusal is the one that deadlocked the
		// escalation protocol, and an override is what crosses it.
		permitted := caps.Overridden(counters.Overrides)
		// Both budgets are asked before either refuses, exactly as a grant's are, so
		// an item at the end of both hears it once. Its own budget is named first,
		// and for the same reason a grant's is: a re-run buys a whole fresh run and
		// spends no round itself, so on an item whose runs keep stopping before
		// review the round budget refuses nothing.
		var refusals []TriageCapRefusal
		if counters.Reruns >= permitted.Reruns {
			refusals = append(refusals, TriageCapRefusal{
				Budget: TriageRerunBudget,
				Spent:  triageBudgetSpend(*counters, TriageRerunBudget),
				Cap:    permitted.Reruns,
			})
		}
		// Against what the item is committed to, for the reason a grant is: a re-run
		// whose only remaining round is one an outstanding grant has already
		// promised overshoots the cap exactly as a second grant would.
		if counters.RoundsUncommitted(permitted.ReviewRounds) == 0 {
			refusals = append(refusals, TriageCapRefusal{
				Budget: TriageReviewRoundBudget,
				Spent:  triageBudgetSpend(*counters, TriageReviewRoundBudget),
				Cap:    permitted.ReviewRounds,
			})
		}
		if len(refusals) > 0 {
			return TriageCapError{
				Action:     TriageRerun,
				WorkItemID: counters.WorkItemID,
				Refusals:   refusals,
			}
		}
		counters.Reruns++
		return nil
	})
}

// RecordMergeRearm records that triage re-armed a merge the forge accepted and
// then dropped, and refuses once that publication has had the re-arms its cap
// permits. It spends no provider invocation at all, which is exactly why it is
// bounded separately: an action that costs nothing to take is the one that can
// be taken forever, and a merge that keeps being dropped is a repository
// somebody has to look at.
//
// publicationKey names the publication the re-arm repeats — the run that made it
// and the pull request it made — and it is what the budget is keyed to. That is
// the whole of the bound: a re-arm buys the item no further attempt at anything,
// it repeats one merge request the reviewer's verdict already authorized, so what
// may happen once is one publication being re-armed. Keyed to the item instead,
// the counter refused the second publication of an item its own first re-arm,
// and — at the integration-retry cap it was sized by — granted one publication a
// second re-arm that the governed design says is an escalation.
//
// The re-arm cap this spends is one precondition among several, exactly as the
// re-run's is: a re-arm is an integration retry against the target branch, so
// `one-promotion-per-target-branch` binds the caller — the promotion is the
// harness's to take under the runstate lease, no agent performs one or touches
// that lease, and the re-arm repeats only the identical, already-authorized
// forge request against a head and target the original gate's checks still
// pass. Those belong to the action; this store counts what it has been given.
//
// The decision that bought it is written in the same update, as every other
// spend's is. See triagedecision.go.
func (s *TriageStore) RecordMergeRearm(ctx context.Context, workItemID, publicationKey string, decision TriageDecision, at time.Time, caps TriageCaps) (TriageCounters, error) {
	publication := strings.TrimSpace(publicationKey)
	if publication == "" {
		return TriageCounters{}, errors.New("a publication is required to record its merge re-arm: a re-arm repeats one already-authorized merge request, and the budget it spends is that publication's")
	}
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	decided, err := spendingDecision(decision, TriageDecisionRearm, when)
	if err != nil {
		return TriageCounters{}, err
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		permitted := caps.Overridden(counters.Overrides)
		// One budget, because a re-arm buys no round: there is no second bound for it
		// to stand behind and nothing else to say in the same breath. It is the
		// publication's rather than the item's, which the refusal says.
		if spent := counters.RearmsOf(publication); spent >= permitted.MergeRearms {
			return TriageCapError{
				Action:      TriageMergeRearm,
				WorkItemID:  counters.WorkItemID,
				Publication: publication,
				Refusals: []TriageCapRefusal{{
					Budget: TriageMergeRearmBudget,
					Spent:  spent,
					Cap:    permitted.MergeRearms,
				}},
			}
		}
		if counters.RearmedPublications == nil {
			counters.RearmedPublications = make(map[string]int, 1)
		}
		counters.RearmedPublications[publication]++
		// The item's own total is kept beside the breakdown rather than derived from
		// it, because it is what every reading of the item reports as what triage has
		// done to it — and because a record written while the counter was per item
		// carries a total with no breakdown behind it, which the total must go on
		// including.
		counters.MergeRearms++
		return counters.recordDecision(decided)
	})
}

// spendingDecision is the decision a spend is authorized by, checked against the
// operation being asked for. The word and the budget have to agree: an operation
// that took whatever decision it was handed would let a wait pay for a re-run,
// which is the attribution the whole record exists to make impossible.
func spendingDecision(decision TriageDecision, expected string, at time.Time) (TriageDecision, error) {
	prepared, err := prepareTriageDecision(decision, at)
	if err != nil {
		return TriageDecision{}, err
	}
	if prepared.Decision != expected {
		return TriageDecision{}, fmt.Errorf(
			"a %q spend is authorized by a %q decision, and this one is %q; nothing was recorded and nothing was spent",
			expected, expected, prepared.Decision)
	}
	return prepared, nil
}

// RoundKey names the developer attempt one review judged: the run it was made
// in and how many repairs into that run it is. It is the identity a counted
// round is recorded under, and it is derived rather than minted so that the same
// attempt asked about twice always answers with the same key.
func RoundKey(runID string, repairAttempts int) string {
	return fmt.Sprintf("%s#%d", runID, repairAttempts)
}

// errNoTriageChange reports an update that turned out to have nothing to write.
// It never reaches a caller: the record is already what the caller asked for, so
// it is returned as it stands rather than rewritten.
var errNoTriageChange = errors.New("triage counters are already what this update would make them")

// update takes one item's counters, applies a change to them under the item's
// own lock, and makes the result durable before returning it. The lock is per
// item and held only for the read and the write, so two runs recording rounds
// for two items never wait on each other, and two processes recording against
// the same item cannot lose one of the two increments to a read that predated
// the other's write.
func (s *TriageStore) update(ctx context.Context, workItemID string, at time.Time, change func(*TriageCounters) error) (TriageCounters, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return TriageCounters{}, errors.New("a work item is required to record its triage counters")
	}
	release, err := s.lock(ctx, id)
	if err != nil {
		return TriageCounters{}, err
	}
	defer release()

	counters, err := s.load(id)
	if err != nil {
		return TriageCounters{}, err
	}
	if err := change(&counters); err != nil {
		if errors.Is(err, errNoTriageChange) {
			return counters, nil
		}
		return TriageCounters{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	counters.UpdatedAt = when.UTC()
	if err := s.save(id, counters); err != nil {
		return TriageCounters{}, err
	}
	return counters, nil
}

// load reads one item's counters, or the empty set an item nothing has recorded
// anything about stands at.
func (s *TriageStore) load(workItemID string) (TriageCounters, error) {
	file, err := os.Open(s.path(workItemID))
	if errors.Is(err, os.ErrNotExist) {
		return TriageCounters{
			SchemaVersion: TriageCountersSchemaVersion,
			ProductID:     s.productID,
			WorkItemID:    workItemID,
		}, nil
	}
	if err != nil {
		return TriageCounters{}, fmt.Errorf("open triage counters: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var counters TriageCounters
	if err := decoder.Decode(&counters); err != nil {
		return TriageCounters{}, fmt.Errorf("decode triage counters for %s: %w", workItemID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return TriageCounters{}, fmt.Errorf("decode triage counters for %s: %w", workItemID, err)
	}
	if err := counters.Validate(); err != nil {
		return TriageCounters{}, err
	}
	if counters.ProductID != s.productID {
		return TriageCounters{}, fmt.Errorf("triage counters belong to product %q, not %q", counters.ProductID, s.productID)
	}
	// The file is named for a digest of the work item rather than for the item
	// itself, so this is what catches two items that were ever to land on one
	// name: the record says whose it is, and a record that is not this item's is
	// refused rather than spent.
	if counters.WorkItemID != workItemID {
		return TriageCounters{}, fmt.Errorf("triage counters at %s belong to work item %q, not %q", s.path(workItemID), counters.WorkItemID, workItemID)
	}
	return counters, nil
}

// save replaces one item's counters durably. The write is a temporary file and a
// rename, as every other revised record here is, so a process that dies mid-write
// leaves the previous counters rather than a truncated file nothing can read.
func (s *TriageStore) save(workItemID string, counters TriageCounters) error {
	counters.SchemaVersion = TriageCountersSchemaVersion
	counters.ProductID = s.productID
	counters.WorkItemID = workItemID
	if err := counters.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create triage counter directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".triage-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary triage counters: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary triage counters: %w", err)
	}
	if err := writeJSONFile(temporary, "triage counters", counters); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary triage counters: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(workItemID)); err != nil {
		return fmt.Errorf("replace triage counters: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the read-modify-write on one item's counters across every
// Yoyodyne process. The lock file outlives the update for the reason a run's
// lease file does: removing it while another process held it would let a third
// take a lock on a file nobody else can see.
func (s *TriageStore) lock(ctx context.Context, workItemID string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create triage counter directory: %w", err)
	}
	file, err := os.OpenFile(s.path(workItemID)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open triage counter lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock triage counters for %s: %w", workItemID, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

func (s *TriageStore) path(workItemID string) string {
	return filepath.Join(s.root, triageKey(workItemID)+".json")
}

// triageKey names one work item's counter file. A tracker identifier is not a
// file name — nothing stops one carrying a slash — so the name is a bounded,
// lowercased rendering of the identifier for whoever reads the directory, with a
// digest of the exact identifier after it so two that render alike still get
// their own file.
func triageKey(workItemID string) string {
	digest := sha256.Sum256([]byte(workItemID))
	var rendered strings.Builder
	for _, character := range strings.ToLower(workItemID) {
		if rendered.Len() >= maxTriageKeyPrefix {
			break
		}
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			rendered.WriteRune(character)
		default:
			rendered.WriteByte('-')
		}
	}
	return rendered.String() + "-" + hex.EncodeToString(digest[:8])
}
