package readmodel

// How the queue of proposed changes stands, and which of them the owning role
// has already argued, derived once for every surface.
//
// Two things about the queue are waiting on a person and were said by nothing.
// The first is the queue itself getting old: forty-four proposals stood
// undecided against the architect's documents with the oldest weeks old, and
// the attention line named each one and folded most of them into "and 34
// things not named here", so a reader saw a list and never a queue that had
// stopped draining. The second is the owner's recommendations: a recurring pass
// puts the undecided proposals to the owning role and records what it argued,
// and that batch is the operator's to decide from — which nothing said either,
// because the sweep log is read by `yoyo sweeps` and nothing else.
//
// The owning role now decides its own proposals from its conversation, so the
// batch the operator is put is only what is his: the owner's recommendations
// on changes to the brief or the goals that change their intent
// or do not say. A recommendation on anything else is the owner arguing with
// itself, and the decision it argues for is the owner's to record.
//
// So both are derived here, from the amendment log and the sweep log and
// nowhere else. The count-and-age line is the sibling of the report pile's, on
// the same threshold and for the same reason; the recommendations are read
// off the recorded passes, latest first per proposal, and a proposal the
// operator has decided since is dropped from the batch wherever it is read —
// the account on the sweep record is the role's own words and is never
// rewritten, so dropping happens here rather than there.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// Sweeps is the recurring tasks' durable reports, read for the recommendations
// an owning role recorded on the proposed changes put to it. It is satisfied by
// *runstate.SweepStore.
type Sweeps interface {
	List() ([]runstate.Sweep, []runstate.UnreadableSweep, error)
}

// maxUndecidedAmendmentAge is how long the oldest undecided proposal may go
// unanswered before the queue is something waiting on a person rather than
// something a cadence is working through. It is the pile's threshold, because
// it catches the same failure: a cadence not running or not keeping up, which
// is invisible in any one reading and shows only as the oldest one's age
// climbing past anything a working cadence would leave.
const maxUndecidedAmendmentAge = maxUndecidedReportAge

// RecommendedAmendment is one undecided proposal the owning role has argued on
// a recurring pass: what it recommended, why, and where that is recorded. It
// is the unit of the batch the operator decides from, so a surface that puts
// the batch to him reads these rather than the sweep log.
type RecommendedAmendment struct {
	Proposal string           `json:"proposal"`
	Artifact string           `json:"artifact"`
	Owner    domain.AgentRole `json:"owner"`
	// Verdict, Reason, and Into are the recommendation as the role gave it.
	Verdict sweep.Recommended `json:"verdict"`
	Reason  string            `json:"reason"`
	Into    string            `json:"into,omitempty"`
	// Task is the recurring task whose pass recorded it and RecommendedAt when
	// that pass started, so `yoyo sweeps --task` finds the whole account.
	Task          string    `json:"task"`
	RecommendedAt time.Time `json:"recommended_at"`
}

// RecommendedAmendments is every undecided proposal the owning role has argued,
// oldest proposal first, each carrying the latest recommendation recorded on
// it. A recommendation on a proposal that is decided, or that nothing raised,
// names nothing waiting and is left out: the batch is what the operator still
// has to decide, not what the role once said. And a recommendation recorded on
// a pass of any role but the proposal's owner is left out too, whatever it
// says: the batch is the owner's argument, and a line that said "the architect
// recommends" over something another role wrote would be putting words in the
// one mouth the record says decides.
func RecommendedAmendments(sweeps []runstate.Sweep, records []amendment.Record) []RecommendedAmendment {
	pending := amendment.Pending(records)
	byID := make(map[string]amendment.Proposal, len(pending))
	for _, proposal := range pending {
		byID[proposal.ID] = proposal
	}
	latest := map[string]RecommendedAmendment{}
	for _, recorded := range sweeps {
		if recorded.Result == nil {
			continue
		}
		for _, recommendation := range recorded.Result.Recommendations {
			proposal, waiting := byID[recommendation.Proposal]
			if !waiting || recorded.Role != proposal.Owner {
				continue
			}
			// A later pass's recommendation on the same proposal stands over an
			// earlier one: the role may have changed its mind, and what the operator
			// acts on is its latest argument.
			if earlier, argued := latest[proposal.ID]; argued && !recorded.StartedAt.After(earlier.RecommendedAt) {
				continue
			}
			latest[proposal.ID] = RecommendedAmendment{
				Proposal:      proposal.ID,
				Artifact:      proposal.Artifact,
				Owner:         proposal.Owner,
				Verdict:       recommendation.Verdict,
				Reason:        strings.Join(strings.Fields(recommendation.Reason), " "),
				Into:          recommendation.Into,
				Task:          recorded.Task,
				RecommendedAt: recorded.StartedAt,
			}
		}
	}
	recommended := make([]RecommendedAmendment, 0, len(latest))
	for _, proposal := range pending {
		if entry, argued := latest[proposal.ID]; argued {
			recommended = append(recommended, entry)
		}
	}
	sort.SliceStable(recommended, func(i, j int) bool {
		return byID[recommended[i].Proposal].RaisedAt.Before(byID[recommended[j].Proposal].RaisedAt)
	})
	return recommended
}

// amendmentReading is the queue as one standing reading has it: the records,
// the summary, the batch the owners have argued, and what could not be read.
type amendmentReading struct {
	records     []amendment.Record
	queue       amendment.Queue
	recommended []RecommendedAmendment
	// problem is what stopped the queue being read, and sweepProblem what
	// stopped the recommendations: the two are said apart because the queue is
	// readable without the passes and a reader told neither concludes there is
	// nothing waiting.
	problem      string
	sweepProblem string
}

// readAmendments reads the proposed changes once for both lines that carry
// them. A log that could not be read says so and reports no queue at all: a
// zero here would read as nothing proposed, which is the one thing a broken
// read of it must never look like.
func readAmendments(sources Sources, now time.Time) amendmentReading {
	if sources.Amendments == nil {
		return amendmentReading{problem: "nothing was wired to read the proposed changes"}
	}
	records, err := sources.Amendments.List()
	if err != nil {
		return amendmentReading{problem: fmt.Sprintf("the proposed changes could not be read: %v", err)}
	}
	reading := amendmentReading{records: records, queue: amendment.Summarize(records, now)}
	if sources.Sweeps == nil {
		// Nothing wired to read the passes is the ordinary case for a fixture and
		// a surface older than the cadence, and it is not a problem to name: what
		// is lost is the owner's recommendations, which no surface without the
		// sweep log ever showed.
		return reading
	}
	sweeps, _, err := sources.Sweeps.List()
	if err != nil {
		// A scan that failed part way through returns what it read before the
		// failure; those passes are real and their recommendations stand. The
		// failure is said beside them rather than in place of them.
		reading.sweepProblem = fmt.Sprintf("the recurring passes could not be read to the end, so the owners' recommendations may be incomplete: %v", err)
	}
	reading.recommended = RecommendedAmendments(sweeps, records)
	return reading
}

// attention is each undecided proposal as the attention line names it, oldest
// first by the log's order. What the owners argued about them is not repeated
// here: it is the operator-action finding each pass's batch makes, which the
// line names ahead of these and the channel says once.
func (r amendmentReading) attention() []Attention {
	var attention []Attention
	for _, proposal := range amendment.Pending(r.records) {
		attention = append(attention, amendmentAttention(proposal))
	}
	return attention
}

// batchAttention is the owners' recommendations as the attention line names
// them: one operator-action finding per pass that argued something still
// undecided.
func (r amendmentReading) batchAttention() []Attention {
	actions := AmendmentBatchActions(OperatorsRecommendations(r.recommended, r.records))
	attention := make([]Attention, 0, len(actions))
	for _, action := range actions {
		attention = append(attention, operatorActionAttention(action))
	}
	return attention
}

// ageAttention is the count-and-age line, where the queue has stopped
// draining: nothing has decided the oldest undecided proposal for longer than
// any working cadence would leave it. Nothing else says so — the per-proposal
// entries name each and fold the rest, and a list is not an age.
func (r amendmentReading) ageAttention() (Attention, bool) {
	if r.queue.OldestAge <= maxUndecidedAmendmentAge {
		return Attention{}, false
	}
	var oldest amendment.Proposal
	for _, proposal := range amendment.Pending(r.records) {
		if oldest.ID == "" || proposal.RaisedAt.Before(oldest.RaisedAt) {
			oldest = proposal
		}
	}
	return amendmentQueueAttention(r.queue, oldest), true
}

// OperatorsRecommendations narrows the owners' recommendations to the
// proposals the operator decides: changes to the brief or the goals that change their intent or do not say. Every other proposal is its
// owner's to decide from its conversation, so a recommendation on one is not a
// decision anybody puts to him — listing it as needing his hand would be an
// approval routed to him that a role can make.
func OperatorsRecommendations(recommended []RecommendedAmendment, records []amendment.Record) []RecommendedAmendment {
	var kept []RecommendedAmendment
	for _, entry := range recommended {
		if proposal, found := amendment.Find(records, entry.Proposal); found && proposal.ReservedForOperator() {
			kept = append(kept, entry)
		}
	}
	return kept
}

// AmendmentBatchKeyPrefix opens the key of every batch finding, so a surface
// remembering which findings it has said can tell a batch's mark from the rest.
const AmendmentBatchKeyPrefix = "amendments:"

// amendmentBatchKey names one pass's batch durably, by the task and the moment
// the pass started, which is what identifies a pass in the sweep log. A batch is
// one finding however many of its proposals are decided before the rest, so the
// channel says it once and the key survives the batch shrinking.
func amendmentBatchKey(task string, at time.Time) string {
	return AmendmentBatchKeyPrefix + task + "@" + at.UTC().Format(time.RFC3339Nano)
}

// amendmentBatchEnds is what ends a batch finding: the last proposal in it
// being decided.
const amendmentBatchEnds = "it ends once every proposal in it is decided with `yoyo amendment approve|decline <id> --reason ...`"

// maxBatchReasonBytes bounds each recommendation's reason as the batch finding
// quotes it. A pass argues at most sweep.MaxRecommendations proposals, so ten
// reasons at this bound keep the whole list a message rather than a document;
// the reason whole is on the pass's record, which the finding names.
const maxBatchReasonBytes = 400

// AmendmentBatchActions is the owners' recommendations as findings that need
// the operator: one per recurring pass that argued proposals still undecided,
// oldest pass first, each carrying that pass's recommendations as one decision
// list. It is the one derivation of the batch every surface puts to him — the
// attention line names each, and the channel says each once, through the same
// operator-action message every other finding reaches him by.
//
// A proposal decided since the pass is not in the list, because the
// recommendations are read from RecommendedAmendments, which reads only the
// undecided; and a pass whose every proposal is decided makes no finding, so
// the finding ends when the last of them is decided. A proposal a later pass
// argued again is in the later pass's batch and not the earlier one's, because
// what the operator acts on is the owner's latest argument.
func AmendmentBatchActions(recommended []RecommendedAmendment) []OperatorAction {
	type pass struct {
		task    string
		at      time.Time
		owner   domain.AgentRole
		entries []RecommendedAmendment
	}
	byKey := map[string]*pass{}
	var keys []string
	for _, entry := range recommended {
		key := amendmentBatchKey(entry.Task, entry.RecommendedAt)
		batch, seen := byKey[key]
		if !seen {
			batch = &pass{task: entry.Task, at: entry.RecommendedAt, owner: entry.Owner}
			byKey[key] = batch
			keys = append(keys, key)
		}
		batch.entries = append(batch.entries, entry)
	}
	actions := make([]OperatorAction, 0, len(keys))
	for _, key := range keys {
		batch := byKey[key]
		// The decision list leads, one id and verdict each, so the line that cuts
		// the finding short still names what to decide; the reasons follow.
		decisions := make([]string, 0, len(batch.entries))
		reasons := make([]string, 0, len(batch.entries))
		for _, entry := range batch.entries {
			decisions = append(decisions, entry.Proposal+" "+entry.says())
			reasons = append(reasons, fmt.Sprintf("%s (a change to %s): %s",
				entry.Proposal, entry.Artifact, singleLine(entry.Reason, maxBatchReasonBytes)))
		}
		owner := batch.owner.Title()
		actions = append(actions, OperatorAction{
			Key:     key,
			Subject: fmt.Sprintf("the %s's batch of %s", owner, count(len(batch.entries), "proposed change")),
			Needs: fmt.Sprintf("decide %s. The reasons: %s.",
				strings.Join(decisions, "; "), strings.Join(reasons, "; ")),
			RecordedIn: fmt.Sprintf("the pass of %s at %s, which `yoyo sweeps --task %s` shows whole",
				batch.task, batch.at.UTC().Format(time.RFC3339), batch.task),
			FoundBy: fmt.Sprintf("the %s, arguing the undecided changes proposed to its documents on a recurring pass", owner),
			Ends:    amendmentBatchEnds,
			// Only proposals reserved for the operator reach a batch
			// (OperatorsRecommendations): changes of fundamental intent.
			Reason: ownership.OperatorFundamentalIntent,
			Since:  batch.at,
		})
	}
	sortOperatorActions(actions)
	return actions
}

// says is the recommendation in a clause: the verdict, and for a merge what it
// merges into.
func (r RecommendedAmendment) says() string {
	if r.Verdict == sweep.RecommendMerge && r.Into != "" {
		return fmt.Sprintf("%s into %s", r.Verdict, r.Into)
	}
	return string(r.Verdict)
}
