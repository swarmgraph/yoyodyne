package runstate

// What the merge queue did to land an entry, kept beside its generations:
// docs/designs/integration-through-a-merge-queue.md, "Harness-run queue" from
// promotion on, and "Crash recovery and disabling". Every mutation a promotion
// makes — moving the local target, pushing the candidate, opening the pull
// request that carries it, asking the forge to merge, and fast-forwarding the
// local target onto what the forge landed — is written here, with the commit
// it is pinned to and a key that names it, before it is requested, and settled
// here once its result is known. A process that dies in between leaves a
// mutation requested and never settled, and the next promotion observes the
// target, the publication and the forge before it does anything else: what it
// finds settles the mutation, and what it cannot find out leaves it standing
// rather than asked for again.
//
// The record is one file per entry, written whole by the holder of the queue's
// worker lease and by nothing else, for the reason the generations record is
// (mergequeuegeneration.go). A save that fails is read back before it is
// answered.
//
// Completion is three writes in three places — this record, the run, and the
// work item — and no order makes them one transaction. So each is recorded
// here as it is made, and a promotion that finds a landing whose completion is
// partial makes only the parts still missing.
//
// A merge the forge holds queued can be taken back: the withdrawal is written
// on its attempt before the forge is asked and settled only once the forge's
// answer and the pull request say what happened, and an attempt whose merge
// was asked for is set aside only once that withdrawal is confirmed. An entry
// whose candidate is a defect of its change is handed back, which ends its
// turn in the queue as a completion does; HeadRewriteRefusal is what anything
// that would rewrite or re-admit the change asks first.
//
// Nothing here moves a branch, asks a forge anything, or decides whether a
// promotion may happen; internal/orchestrator's merge queue promoter does those
// and records them here.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// MergeQueueLandingSchemaVersion is 1 and has never changed.
const MergeQueueLandingSchemaVersion = 1

// maxMergeQueueAttempts bounds the promotion attempts one entry keeps. An
// attempt is set aside only when the target moved under it or it stopped
// before anything was asked of anybody, so an entry past this is one the target
// never stops moving under.
const maxMergeQueueAttempts = 200

// maxMergeQueueRecoveries bounds the recoveries one entry records.
const maxMergeQueueRecoveries = 200

// maxMergeQueueMutations bounds the mutations one attempt records: each of
// its path's once, and each asked for again after it was found not made.
const maxMergeQueueMutations = 50

// MergeQueueCandidateBranchPrefix is the prefix of the branch a candidate is
// published on for a pull request to carry it. The branch is the queue's own:
// the change's branch and its pull request are never rewritten to carry a
// candidate, so the approved head stays where its approval found it.
const MergeQueueCandidateBranchPrefix = "yoyodyne/merge-queue/"

// MergeQueueCandidateBranch names the branch an entry's candidates are
// published on.
func MergeQueueCandidateBranch(entryID string) string {
	return MergeQueueCandidateBranchPrefix + entryID
}

// MergeQueueLandingPath is how an entry's candidate reaches its target.
type MergeQueueLandingPath string

const (
	// MergeQueueLandLocally is an unprotected target in a project that does not
	// publish: moving the local target onto the candidate is the landing.
	MergeQueueLandLocally MergeQueueLandingPath = "local"
	// MergeQueueLandLocallyThenPullRequest is an unprotected target in a
	// project that publishes: the local target is moved onto the candidate, and
	// the candidate's pull request is merged after it, as a run's promotion is.
	MergeQueueLandLocallyThenPullRequest MergeQueueLandingPath = "local-then-pull-request"
	// MergeQueueLandThroughPullRequest is a protected target: the forge merges
	// the candidate's pull request, and the local target follows by
	// fast-forward only once that landing is confirmed.
	MergeQueueLandThroughPullRequest MergeQueueLandingPath = "pull-request"
	// MergeQueueLandThroughForgeQueue is an entry admitted to the forge's own
	// queue, which builds, checks and lands its own combined commit; the local
	// target follows by fast-forward once the landing is confirmed.
	MergeQueueLandThroughForgeQueue MergeQueueLandingPath = "forge-queue"
)

func (p MergeQueueLandingPath) valid() bool {
	switch p {
	case MergeQueueLandLocally, MergeQueueLandLocallyThenPullRequest, MergeQueueLandThroughPullRequest, MergeQueueLandThroughForgeQueue:
		return true
	}
	return false
}

// MovesLocalTargetFirst reports a path on which the local target is moved
// before anything is asked of the forge.
func (p MergeQueueLandingPath) MovesLocalTargetFirst() bool {
	return p == MergeQueueLandLocally || p == MergeQueueLandLocallyThenPullRequest
}

// ThroughForge reports a path whose landing is the forge's merge.
func (p MergeQueueLandingPath) ThroughForge() bool {
	return p != MergeQueueLandLocally
}

// Mutations is every mutation the path makes, in the order it makes them, up
// to and including the one that lands the change. Following the landing onto
// the local target comes after, and is not among them.
func (p MergeQueueLandingPath) Mutations() []MergeQueueMutation {
	switch p {
	case MergeQueueLandLocally:
		return []MergeQueueMutation{MergeQueueMoveTarget}
	case MergeQueueLandLocallyThenPullRequest:
		return []MergeQueueMutation{MergeQueueMoveTarget, MergeQueuePushCandidate, MergeQueueOpenPullRequest, MergeQueueRequestMerge}
	case MergeQueueLandThroughPullRequest:
		return []MergeQueueMutation{MergeQueuePushCandidate, MergeQueueOpenPullRequest, MergeQueueRequestMerge}
	case MergeQueueLandThroughForgeQueue:
		return []MergeQueueMutation{MergeQueueOpenPullRequest, MergeQueueRequestMerge}
	}
	return nil
}

// MergeQueueMutation names one thing a promotion asks of the repository or the
// forge.
type MergeQueueMutation string

const (
	// MergeQueueMoveTarget moves the local target from the generation's base to
	// its candidate, as a compare-and-swap on the base.
	MergeQueueMoveTarget MergeQueueMutation = "move-target"
	// MergeQueuePushCandidate puts the candidate on the entry's candidate branch
	// on the push remote, as a compare-and-swap on what that branch held.
	MergeQueuePushCandidate MergeQueueMutation = "push-candidate"
	// MergeQueueOpenPullRequest opens, or finds open, the pull request that
	// carries the candidate.
	MergeQueueOpenPullRequest MergeQueueMutation = "open-pull-request"
	// MergeQueueRequestMerge asks the forge to merge that pull request, pinned
	// to the candidate it carries.
	MergeQueueRequestMerge MergeQueueMutation = "request-merge"
	// MergeQueueFollowTarget fast-forwards the local target onto the remote
	// target once the forge's landing is confirmed.
	MergeQueueFollowTarget MergeQueueMutation = "follow-target"
)

func (m MergeQueueMutation) valid() bool {
	switch m {
	case MergeQueueMoveTarget, MergeQueuePushCandidate, MergeQueueOpenPullRequest, MergeQueueRequestMerge, MergeQueueFollowTarget:
		return true
	}
	return false
}

// MergeQueueMutationResult is how a mutation settled.
type MergeQueueMutationResult string

const (
	// MergeQueueMutationDone is the mutation having happened, whether this
	// process saw it happen or a later one observed it had.
	MergeQueueMutationDone MergeQueueMutationResult = "done"
	// MergeQueueMutationQueued is a merge the forge accepted and holds until
	// its own requirements are met.
	MergeQueueMutationQueued MergeQueueMutationResult = "queued"
	// MergeQueueMutationNotMade is a mutation observed not to have happened:
	// refused, or never reaching what it was asked of.
	MergeQueueMutationNotMade MergeQueueMutationResult = "not-made"
	// MergeQueueMutationHeld is a follow the local checkout's own unsaved work
	// stood in the way of; the landing stands, and reconciliation catches the
	// local target up later.
	MergeQueueMutationHeld MergeQueueMutationResult = "held"
)

func (r MergeQueueMutationResult) valid() bool {
	switch r {
	case MergeQueueMutationDone, MergeQueueMutationQueued, MergeQueueMutationNotMade, MergeQueueMutationHeld:
		return true
	}
	return false
}

// MergeQueueMutationRecord is one mutation, written before it is requested.
// Everything but the settlement is fixed when it is written.
type MergeQueueMutationRecord struct {
	Mutation MergeQueueMutation `json:"mutation"`
	// Key names this mutation of this attempt, and is what a forge or a retry
	// recognises the same request by.
	Key string `json:"key"`
	// Commit is what the mutation is pinned to — the candidate a target or a
	// branch is moved to, or the head a merge is pinned to — and Expected is
	// what it is to replace, empty where it replaces nothing.
	Commit      string    `json:"commit,omitempty"`
	Expected    string    `json:"expected,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
	// Settled is set once the mutation's result is known, and never cleared.
	Settled *MergeQueueSettlement `json:"settled,omitempty"`
}

// MergeQueueSettlement is how one mutation turned out, and how that was
// learned.
type MergeQueueSettlement struct {
	Result MergeQueueMutationResult `json:"result"`
	At     time.Time                `json:"at"`
	// Observed is set where a later process, rather than the one that made the
	// request, established the result.
	Observed bool   `json:"observed,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// MergeQueueLanded is a landing confirmed: the candidate on the target.
type MergeQueueLanded struct {
	// Commit is the commit that landed: the attempt's candidate, or, in the
	// forge's queue, the combined commit the forge built, checked, had
	// reviewed, and landed, which is then also RemoteMerge. The head handed
	// to the forge's queue is the attempt's Candidate and is never recorded
	// as what landed.
	Commit string `json:"commit"`
	// RemoteMerge is the commit the forge made of the merge, where it made one.
	RemoteMerge string    `json:"remote_merge,omitempty"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

// MaxMergeQueueWithdrawalAnswers bounds the answers one withdrawal keeps: each
// is the forge refusing or not answering a request to withdraw, asked again on
// a later call. A withdrawal past it keeps asking and stops writing answers
// down.
const MaxMergeQueueWithdrawalAnswers = 50

// MergeQueueWithdrawalResult is how a withdrawal of a queued merge settled.
type MergeQueueWithdrawalResult string

const (
	// MergeQueueWithdrawalConfirmed is the forge having turned the merge off and
	// taken the request out of its queue, and the request still unmerged after
	// it did.
	MergeQueueWithdrawalConfirmed MergeQueueWithdrawalResult = "withdrawn"
	// MergeQueueWithdrawalLanded is the merge found made: the forge landed it
	// before the withdrawal reached it. The landing is completed the way any
	// confirmed landing is, and is never asked for again.
	MergeQueueWithdrawalLanded MergeQueueWithdrawalResult = "landed"
)

func (r MergeQueueWithdrawalResult) valid() bool {
	return r == MergeQueueWithdrawalConfirmed || r == MergeQueueWithdrawalLanded
}

// MergeQueueWithdrawal is the merge an attempt asked the forge for, taken
// back: written down before the forge is asked to withdraw it, and settled
// only once the forge's answer and the pull request's state together say what
// happened. Until it settles the merge may still land, so nothing rewrites
// the change's head, hands it to repair, moves the entry to another mode, or
// admits it again (MergeQueueLanding.MergeArmed).
type MergeQueueWithdrawal struct {
	// Key names the withdrawal, as a mutation's key names the mutation.
	Key string `json:"key"`
	// PullRequest and Pinned are the request and the commit its merge was
	// pinned to: what is being withdrawn, fixed when it is written down.
	PullRequest int       `json:"pull_request"`
	Pinned      string    `json:"pinned"`
	Reason      string    `json:"reason"`
	IntendedAt  time.Time `json:"intended_at"`
	// Answers are the requests to withdraw that did not settle it: the forge
	// refused, or its answer did not say. Each is kept so a reader can see why
	// the merge is still armed.
	Answers []MergeQueueWithdrawalAnswer `json:"answers,omitempty"`
	// Settled is set once the withdrawal's result is known, and never cleared.
	Settled *MergeQueueWithdrawalSettlement `json:"settled,omitempty"`
}

// MergeQueueWithdrawalAnswer is one request to withdraw that left the merge
// possibly armed.
type MergeQueueWithdrawalAnswer struct {
	At time.Time `json:"at"`
	// Rejected is the forge refusing the request outright; otherwise its answer
	// did not say whether the merge was withdrawn.
	Rejected bool   `json:"rejected,omitempty"`
	Detail   string `json:"detail"`
}

// MergeQueueWithdrawalSettlement is how a withdrawal turned out.
type MergeQueueWithdrawalSettlement struct {
	Result MergeQueueWithdrawalResult `json:"result"`
	At     time.Time                  `json:"at"`
	Detail string                     `json:"detail,omitempty"`
}

// MergeQueueWithdrawalKey is the key an attempt's withdrawal is written under.
func (a MergeQueuePromotionAttempt) MergeQueueWithdrawalKey() string {
	return a.Key + "/withdraw"
}

// Withdrawn reports an attempt whose queued merge the forge confirmed
// withdrawn.
func (a MergeQueuePromotionAttempt) Withdrawn() bool {
	return a.Withdrawal != nil && a.Withdrawal.Settled != nil && a.Withdrawal.Settled.Result == MergeQueueWithdrawalConfirmed
}

// MergeQueueSetAside is an attempt given up on before it landed, and why.
type MergeQueueSetAside struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	// Drift is the target having moved, which is charged to nothing.
	Drift bool `json:"drift,omitempty"`
}

// MergeQueuePromotionAttempt is one promotion of one generation by one path.
type MergeQueuePromotionAttempt struct {
	Number uint64 `json:"number"`
	// Key is the attempt's idempotency key, which every mutation's key extends.
	Key        string                `json:"key"`
	Path       MergeQueueLandingPath `json:"path"`
	Generation uint64                `json:"generation,omitempty"`
	Binding    string                `json:"binding,omitempty"`
	TargetBase string                `json:"target_base"`
	// Candidate is what the attempt promotes: the generation's candidate, which
	// is what lands, or, in the forge's queue, the approved head handed to the
	// forge, which combines it into a commit of its own (see Landed).
	Candidate string `json:"candidate"`
	// CandidateBranch is the branch whose pull request carries Candidate, and
	// is empty for a local landing.
	CandidateBranch string    `json:"candidate_branch,omitempty"`
	IntendedAt      time.Time `json:"intended_at"`
	// PullRequest and PullRequestURL are the pull request that carries the
	// candidate, once one is open.
	PullRequest    int                        `json:"pull_request,omitempty"`
	PullRequestURL string                     `json:"pull_request_url,omitempty"`
	Mutations      []MergeQueueMutationRecord `json:"mutations,omitempty"`
	// Withdrawal is the attempt's queued merge being taken back, once that is
	// intended.
	Withdrawal *MergeQueueWithdrawal `json:"withdrawal,omitempty"`
	Landed     *MergeQueueLanded     `json:"landed,omitempty"`
	SetAside   *MergeQueueSetAside   `json:"set_aside,omitempty"`
}

// Mutation is the attempt's newest record of one mutation, if it has one.
func (a MergeQueuePromotionAttempt) Mutation(mutation MergeQueueMutation) (MergeQueueMutationRecord, bool) {
	for index := len(a.Mutations) - 1; index >= 0; index-- {
		if a.Mutations[index].Mutation == mutation {
			return a.Mutations[index], true
		}
	}
	return MergeQueueMutationRecord{}, false
}

// Next is the mutation the attempt makes after the ones it recorded, along its
// path, or "" where it has made all of them. Following the landing is not
// among them.
func (a MergeQueuePromotionAttempt) Next() MergeQueueMutation {
	order := a.Path.Mutations()
	if len(a.Mutations) == 0 {
		return order[0]
	}
	last := a.Mutations[len(a.Mutations)-1].Mutation
	for index, mutation := range order {
		if mutation == last && index+1 < len(order) {
			return order[index+1]
		}
	}
	return ""
}

// Open reports an attempt neither landed nor set aside.
func (a MergeQueuePromotionAttempt) Open() bool {
	return a.Landed == nil && a.SetAside == nil
}

// Unsettled is the attempt's first mutation requested and never settled.
func (a MergeQueuePromotionAttempt) Unsettled() (MergeQueueMutationRecord, bool) {
	for _, record := range a.Mutations {
		if record.Settled == nil {
			return record, true
		}
	}
	return MergeQueueMutationRecord{}, false
}

// MutationKey is the key one mutation of the attempt is requested under.
func (a MergeQueuePromotionAttempt) MutationKey(mutation MergeQueueMutation) string {
	return a.Key + "/" + string(mutation)
}

// MergeQueuePromotionKey is the idempotency key of a promotion attempt.
func MergeQueuePromotionKey(entryID string, number uint64, path MergeQueueLandingPath, binding, candidate string) string {
	sum := sha256.New()
	writeBound(sum, "merge-queue-promotion/1")
	writeBound(sum, entryID)
	writeBound(sum, fmt.Sprint(number))
	writeBound(sum, string(path))
	writeBound(sum, binding)
	writeBound(sum, candidate)
	return "mqp-" + hex.EncodeToString(sum.Sum(nil))[:32]
}

// MergeQueueRecovery is one boundary a promotion crossed after a process
// stopped: a mutation found requested and never settled, and what was found.
type MergeQueueRecovery struct {
	At       time.Time          `json:"at"`
	Attempt  uint64             `json:"attempt"`
	Mutation MergeQueueMutation `json:"mutation"`
	Found    string             `json:"found"`
}

// MergeQueueCompletion is a landed entry's completion, part by part. The
// queue's part is this record existing; the run's and the work item's are
// each recorded as they are made.
type MergeQueueCompletion struct {
	Attempt     uint64                `json:"attempt"`
	Path        MergeQueueLandingPath `json:"path"`
	Generation  uint64                `json:"generation,omitempty"`
	Binding     string                `json:"binding,omitempty"`
	TargetBase  string                `json:"target_base"`
	Landed      string                `json:"landed"`
	RemoteMerge string                `json:"remote_merge,omitempty"`
	PullRequest string                `json:"pull_request,omitempty"`
	QueueAt     time.Time             `json:"queue_at"`
	RunAt       *time.Time            `json:"run_at,omitempty"`
	WorkItemAt  *time.Time            `json:"work_item_at,omitempty"`
}

// Whole reports every part of the completion made.
func (c MergeQueueCompletion) Whole() bool {
	return c.RunAt != nil && c.WorkItemAt != nil
}

// MergeQueueFailureClass is what a candidate that did not earn its gate is
// taken to say: docs/designs/integration-through-a-merge-queue.md, "Failure,
// withdrawal and continuation". Only a defect of the change is charged to the
// change; every other class is somebody else's, or nobody's, and costs the
// run nothing.
type MergeQueueFailureClass string

const (
	// MergeQueueCandidateDefect is the candidate's own checks or its reviewer
	// finding against it, on evidence complete enough to say so.
	MergeQueueCandidateDefect MergeQueueFailureClass = "candidate-defect"
	// MergeQueueTargetFailure is a check that fails on the target as well,
	// which existing triage has already filed against the target.
	MergeQueueTargetFailure MergeQueueFailureClass = "target-failure"
	// MergeQueueTargetDrift is the target having moved off the candidate's base.
	MergeQueueTargetDrift MergeQueueFailureClass = "target-drift"
	// MergeQueueInfrastructureFailure is a check or a review that judged
	// nothing: a runner that failed, a process stopped on time, a provider that
	// made no review.
	MergeQueueInfrastructureFailure MergeQueueFailureClass = "infrastructure"
	// MergeQueueUnreadableEvidence is evidence that could not be read or is not
	// whole, which says nothing either way.
	MergeQueueUnreadableEvidence MergeQueueFailureClass = "unreadable-evidence"
)

// Charged reports a class the change itself answers for.
func (c MergeQueueFailureClass) Charged() bool {
	return c == MergeQueueCandidateDefect
}

func (c MergeQueueFailureClass) valid() bool {
	switch c {
	case MergeQueueCandidateDefect, MergeQueueTargetFailure, MergeQueueTargetDrift, MergeQueueInfrastructureFailure, MergeQueueUnreadableEvidence:
		return true
	}
	return false
}

// MergeQueueContinuation is what follows an entry handed back from the queue.
type MergeQueueContinuation string

const (
	// MergeQueueContinueRepair is the same run taking the failure back into its
	// own repair loop, with its saved change, under the repair budget it already
	// has.
	MergeQueueContinueRepair MergeQueueContinuation = "repair"
	// MergeQueueBudgetExhausted is a run that has already spent every repair
	// attempt its budget allows; the work stays where it is for whoever
	// replans it.
	MergeQueueBudgetExhausted MergeQueueContinuation = "exhausted"
	// MergeQueueMissingPrerequisite is a run that no longer has what a repair
	// continues from: its record, its branch, or the developer session that
	// wrote the change.
	MergeQueueMissingPrerequisite MergeQueueContinuation = "missing-prerequisite"
	// MergeQueueUnattributed is a candidate combining more than one head, whose
	// failure no evidence attributes to one of them.
	MergeQueueUnattributed MergeQueueContinuation = "unattributed"
	// MergeQueueReleased is an entry whose queued merge was withdrawn for
	// something other than a defect — its head to be rewritten, or the entry to
	// move to the other queue mode — and that leaves the queue for whatever asked
	// for the withdrawal to admit again. It names no failed candidate and charges
	// nothing.
	MergeQueueReleased MergeQueueContinuation = "released"
)

// HandsBackToRun reports a continuation whose failure is recorded on the run
// and the run docketed: a repair, which the run's repair loop takes up, and an
// exhausted budget, which the development manager decides on from the same
// docket. The others have no single run to give it to, or nothing failed.
func (c MergeQueueContinuation) HandsBackToRun() bool {
	return c == MergeQueueContinueRepair || c == MergeQueueBudgetExhausted
}

func (c MergeQueueContinuation) valid() bool {
	switch c {
	case MergeQueueContinueRepair, MergeQueueBudgetExhausted, MergeQueueMissingPrerequisite, MergeQueueUnattributed, MergeQueueReleased:
		return true
	}
	return false
}

// MergeQueueHandback is an entry the queue has stopped trying to land because
// its candidate is defective, and what follows for the work. It is written
// once its merge, if one was asked for, is confirmed withdrawn, and ends the
// entry's turn in the queue: neither the worker nor the promotion takes the
// entry up again. Everything but HandedBackAt is fixed when it is written.
//
// The failed candidate's evidence stays on its generation, which is never
// removed; this names it, and the run, branch, and approved head that are the
// preserved work.
type MergeQueueHandback struct {
	At           time.Time              `json:"at"`
	Class        MergeQueueFailureClass `json:"class"`
	Continuation MergeQueueContinuation `json:"continuation"`
	// Mover is who moves next, in ownership's words. A handback never names
	// the operator: nothing in it is a change of intent or an act only a person
	// can perform.
	Mover  ownership.Mover `json:"mover"`
	Reason string          `json:"reason"`
	// Generation through Candidate are the failed candidate, as its generation
	// records it.
	Generation uint64   `json:"generation"`
	Binding    string   `json:"binding"`
	TargetBase string   `json:"target_base"`
	Heads      []string `json:"heads"`
	Candidate  string   `json:"candidate"`
	// Branch and ApprovedHead are the run's saved change.
	Branch       string `json:"branch,omitempty"`
	ApprovedHead string `json:"approved_head"`
	// RepairAttempts and RepairBudget are the run's own repair counters as they
	// stood when this was decided. They are read, never granted: the run's
	// record is what bounds its repairs.
	RepairAttempts int `json:"repair_attempts"`
	RepairBudget   int `json:"repair_budget"`
	// HandedBackAt is when the failure was recorded on the run and the run
	// docketed, and is set only for a continuation that HandsBackToRun.
	HandedBackAt *time.Time `json:"handed_back_at,omitempty"`
}

func (h MergeQueueHandback) validate() []error {
	var problems []error
	if h.At.IsZero() {
		problems = append(problems, errors.New("the handback records no time"))
	}
	released := h.Continuation == MergeQueueReleased
	switch {
	case released && h.Class != "":
		problems = append(problems, fmt.Errorf("a released entry was withdrawn, not failed, and names no failure class (%s)", h.Class))
	case !released && !h.Class.Charged():
		problems = append(problems, fmt.Errorf("a %s is never handed back: only a defect of the change ends its turn in the queue", h.Class))
	}
	if !h.Continuation.valid() {
		problems = append(problems, fmt.Errorf("continuation %q is not one this build knows", h.Continuation))
	}
	if !h.Mover.Valid() || h.Mover == ownership.MoverOperator {
		problems = append(problems, fmt.Errorf("a handback names mover %q, and it names a role, never the operator", h.Mover))
	}
	if err := mergeQueueText("handback reason", h.Reason, true); err != nil {
		problems = append(problems, err)
	}
	if !released && (h.Generation == 0 || len(h.Binding) != 64 || len(h.Heads) == 0) {
		problems = append(problems, errors.New("the handback names no failed generation"))
	}
	for field, value := range map[string]string{"target base": h.TargetBase, "candidate": h.Candidate, "approved head": h.ApprovedHead} {
		if released && value == "" && field != "approved head" {
			continue
		}
		if !commitPattern.MatchString(value) {
			problems = append(problems, fmt.Errorf("handback %s %q is not a full commit id", field, value))
		}
	}
	for _, head := range h.Heads {
		if !commitPattern.MatchString(head) {
			problems = append(problems, fmt.Errorf("handback head %q is not a full commit id", head))
		}
	}
	if h.Branch != "" && !validLocalBranch(h.Branch) {
		problems = append(problems, fmt.Errorf("handback branch %q is not a branch name", h.Branch))
	}
	if h.RepairAttempts < 0 || h.RepairBudget < 0 {
		problems = append(problems, errors.New("repair counters are never negative"))
	}
	switch h.Continuation {
	case MergeQueueContinueRepair:
		if h.RepairAttempts >= h.RepairBudget {
			problems = append(problems, fmt.Errorf("a repair is handed back with %d of %d attempts spent", h.RepairAttempts, h.RepairBudget))
		}
	case MergeQueueBudgetExhausted:
		if h.RepairAttempts < h.RepairBudget {
			problems = append(problems, fmt.Errorf("a budget recorded exhausted has %d of %d attempts spent", h.RepairAttempts, h.RepairBudget))
		}
	}
	if released && (h.RepairAttempts != 0 || h.RepairBudget != 0) {
		problems = append(problems, errors.New("a released entry reads no repair budget"))
	}
	if h.HandedBackAt != nil && (!h.Continuation.HandsBackToRun() || h.HandedBackAt.IsZero()) {
		problems = append(problems, fmt.Errorf("a %s is not handed back to its run", h.Continuation))
	}
	return problems
}

// MergeQueueLanding is one entry's landing record.
type MergeQueueLanding struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Repository    string           `json:"repository"`
	TargetBranch  string           `json:"target_branch"`
	EntryID       string           `json:"entry_id"`
	// RunID and Publication are the entry's own, carried so completion names
	// the run and the publication the change was approved under however many
	// attempts it took.
	RunID       string                       `json:"run_id"`
	Publication string                       `json:"publication,omitempty"`
	Attempts    []MergeQueuePromotionAttempt `json:"attempts,omitempty"`
	Recoveries  []MergeQueueRecovery         `json:"recoveries,omitempty"`
	Completion  *MergeQueueCompletion        `json:"completion,omitempty"`
	// Handback is the entry handed back for its defective candidate, or
	// released after its merge was withdrawn for something else; either ends its
	// turn in the queue as a completion does.
	Handback *MergeQueueHandback `json:"handback,omitempty"`
}

// NewMergeQueueLanding is the empty landing record of an entry.
func NewMergeQueueLanding(entry MergeQueueEntry) MergeQueueLanding {
	return MergeQueueLanding{
		SchemaVersion: MergeQueueLandingSchemaVersion, ProductID: entry.ProductID,
		Repository: entry.Repository, TargetBranch: entry.TargetBranch, EntryID: entry.EntryID,
		RunID: entry.RunID, Publication: entry.Publication,
	}
}

// Waiting reports an entry the queue has not finished with: nothing has
// recorded its completion, and it has not been handed back.
func (l MergeQueueLanding) Waiting() bool {
	return l.Completion == nil && l.Handback == nil
}

// HeadRewriteRefusal says why the entry's change may not have its head
// rewritten, be handed to repair, be moved to another queue mode, or be
// admitted again, and is empty where nothing the queue asked for can still
// land it. A merge the forge was asked for, or a target already moved, is
// refused until a withdrawal of it is confirmed; a withdrawal still unsettled
// is the forge's answer not yet known, and is refused the same way.
func (l MergeQueueLanding) HeadRewriteRefusal() string {
	if l.Completion != nil {
		return "the change has landed through the merge queue"
	}
	attempt, ok := l.Current()
	switch {
	case !ok || attempt.SetAside != nil:
		return ""
	case attempt.Landed != nil:
		return fmt.Sprintf("promotion attempt %d landed the change", attempt.Number)
	case attempt.Withdrawal != nil && attempt.Withdrawal.Settled == nil:
		return fmt.Sprintf("the withdrawal of pull request %d's merge is not confirmed, so the forge may still land it", attempt.Withdrawal.PullRequest)
	}
	for _, record := range attempt.Mutations {
		if record.Mutation != MergeQueueMoveTarget && record.Mutation != MergeQueueRequestMerge {
			continue
		}
		if record.Settled == nil || record.Settled.Result != MergeQueueMutationNotMade {
			return fmt.Sprintf("promotion attempt %d asked for the %s, and nothing has confirmed it withdrawn", attempt.Number, record.Mutation)
		}
	}
	return ""
}

// Current is the newest attempt, if there is one.
func (l MergeQueueLanding) Current() (MergeQueuePromotionAttempt, bool) {
	if len(l.Attempts) == 0 {
		return MergeQueuePromotionAttempt{}, false
	}
	return l.Attempts[len(l.Attempts)-1], true
}

func (l MergeQueueLanding) validate(productID domain.ProductID, key MergeQueueKey, entryID string) error {
	var problems []error
	if l.SchemaVersion != MergeQueueLandingSchemaVersion {
		problems = append(problems, fmt.Errorf("merge queue landing schema version %d is not supported", l.SchemaVersion))
	}
	if l.ProductID != productID || l.Repository != key.Repository || l.TargetBranch != key.TargetBranch || l.EntryID != entryID {
		problems = append(problems, fmt.Errorf("the landing record is for entry %s of %s on %q, not entry %s of %s on %q",
			l.EntryID, l.TargetBranch, l.Repository, entryID, key.TargetBranch, key.Repository))
	}
	if !ValidRunID(l.RunID) {
		problems = append(problems, fmt.Errorf("run id %q is not a run id", l.RunID))
	}
	if err := mergeQueueText("publication", l.Publication, false); err != nil {
		problems = append(problems, err)
	}
	if len(l.Attempts) > maxMergeQueueAttempts {
		problems = append(problems, fmt.Errorf("%d promotion attempts exceeds the %d an entry keeps", len(l.Attempts), maxMergeQueueAttempts))
	}
	if len(l.Recoveries) > maxMergeQueueRecoveries {
		problems = append(problems, fmt.Errorf("%d recoveries exceeds the %d an entry keeps", len(l.Recoveries), maxMergeQueueRecoveries))
	}
	landed := 0
	for index, attempt := range l.Attempts {
		if err := attempt.validate(entryID); err != nil {
			problems = append(problems, err)
			continue
		}
		if attempt.Number != uint64(index+1) {
			problems = append(problems, fmt.Errorf("promotion attempt %d is recorded in place %d", attempt.Number, index+1))
		}
		// Only the newest attempt may be open or landed: each one before it was
		// set aside, so at most one promotion is ever in flight for an entry.
		if index < len(l.Attempts)-1 && attempt.SetAside == nil {
			problems = append(problems, fmt.Errorf("promotion attempt %d was followed by another without being set aside", attempt.Number))
		}
		if attempt.Landed != nil {
			landed++
		}
	}
	if landed > 1 {
		problems = append(problems, errors.New("more than one promotion attempt landed"))
	}
	for _, recovery := range l.Recoveries {
		if recovery.At.IsZero() || recovery.Attempt == 0 || recovery.Attempt > uint64(len(l.Attempts)) || !recovery.Mutation.valid() ||
			mergeQueueText("recovery", recovery.Found, true) != nil {
			problems = append(problems, fmt.Errorf("a recovery of attempt %d is not a whole record", recovery.Attempt))
		}
	}
	if c := l.Completion; c != nil {
		attempt, ok := l.Current()
		switch {
		case !ok || attempt.Landed == nil:
			problems = append(problems, errors.New("a completion is recorded without a landing"))
		case c.Attempt != attempt.Number || c.Path != attempt.Path || c.Generation != attempt.Generation || c.Binding != attempt.Binding ||
			c.TargetBase != attempt.TargetBase || c.Landed != attempt.Landed.Commit || c.RemoteMerge != attempt.Landed.RemoteMerge:
			problems = append(problems, errors.New("the completion does not describe the attempt that landed"))
		}
		if c.QueueAt.IsZero() {
			problems = append(problems, errors.New("the completion records no time"))
		}
		if err := mergeQueueText("completion pull request", c.PullRequest, false); err != nil {
			problems = append(problems, err)
		}
	}
	if h := l.Handback; h != nil {
		problems = append(problems, h.validate()...)
		if l.Completion != nil {
			problems = append(problems, errors.New("an entry that landed is never handed back"))
		}
		// An entry is handed back only once nothing it asked for can still land.
		if attempt, ok := l.Current(); ok && attempt.SetAside == nil {
			problems = append(problems, fmt.Errorf("it was handed back with promotion attempt %d still standing", attempt.Number))
		}
	}
	return errors.Join(problems...)
}

func (a MergeQueuePromotionAttempt) validate(entryID string) error {
	var problems []error
	if a.Number == 0 {
		problems = append(problems, errors.New("attempt numbers start at 1"))
	}
	if !a.Path.valid() {
		problems = append(problems, fmt.Errorf("landing path %q is not one this build knows", a.Path))
	}
	if a.Key != MergeQueuePromotionKey(entryID, a.Number, a.Path, a.Binding, a.Candidate) {
		problems = append(problems, errors.New("its key is not the key of what it promotes"))
	}
	if a.Path == MergeQueueLandThroughForgeQueue {
		if a.Generation != 0 || a.Binding != "" {
			problems = append(problems, errors.New("the forge's queue builds its own candidate, so an attempt there names no generation"))
		}
	} else if a.Generation == 0 || len(a.Binding) != 64 {
		problems = append(problems, errors.New("it names no verified generation"))
	}
	for field, value := range map[string]string{"target base": a.TargetBase, "candidate": a.Candidate} {
		if !commitPattern.MatchString(value) {
			problems = append(problems, fmt.Errorf("%s %q is not a full commit id", field, value))
		}
	}
	switch {
	case a.Path == MergeQueueLandLocally && a.CandidateBranch != "":
		problems = append(problems, errors.New("a local landing publishes no candidate branch"))
	case a.Path != MergeQueueLandLocally && a.CandidateBranch == "":
		problems = append(problems, errors.New("a landing through the forge names the branch its pull request carries"))
	case a.CandidateBranch != "" && !validLocalBranch(a.CandidateBranch):
		problems = append(problems, fmt.Errorf("candidate branch %q is not a branch name", a.CandidateBranch))
	}
	if a.IntendedAt.IsZero() {
		problems = append(problems, errors.New("the time it was intended is required"))
	}
	if a.PullRequest < 0 {
		problems = append(problems, errors.New("pull request numbers are positive"))
	}
	if err := mergeQueueText("pull request address", a.PullRequestURL, false); err != nil {
		problems = append(problems, err)
	}
	// Mutations are written in the path's order and only ever after the one
	// before them settled: a mutation is requested only once everything ahead
	// of it is known to have happened. The one repetition is a mutation asked
	// for again, under the same key, after it was found not made.
	order := append(a.Path.Mutations(), MergeQueueFollowTarget)
	if len(a.Mutations) > maxMergeQueueMutations {
		problems = append(problems, fmt.Errorf("%d mutations exceeds the %d an attempt keeps", len(a.Mutations), maxMergeQueueMutations))
	}
	place := -1
	for index, record := range a.Mutations {
		var previous *MergeQueueMutationRecord
		if index > 0 {
			previous = &a.Mutations[index-1]
		}
		notMade := previous != nil && previous.Settled != nil && previous.Settled.Result == MergeQueueMutationNotMade
		switch {
		case notMade && record.Mutation == previous.Mutation:
		case notMade:
			problems = append(problems, fmt.Errorf("the %s was requested after the %s before it was not made", record.Mutation, previous.Mutation))
		default:
			place++
			if place >= len(order) || record.Mutation != order[place] {
				problems = append(problems, fmt.Errorf("mutation %d is %q, out of its path's order", index+1, record.Mutation))
				continue
			}
		}
		if record.Key != a.MutationKey(record.Mutation) {
			problems = append(problems, fmt.Errorf("the %s is keyed for another attempt", record.Mutation))
		}
		if record.RequestedAt.IsZero() {
			problems = append(problems, fmt.Errorf("the %s records no request time", record.Mutation))
		}
		for field, value := range map[string]string{"commit": record.Commit, "expected": record.Expected} {
			if value != "" && !commitPattern.MatchString(value) {
				problems = append(problems, fmt.Errorf("the %s's %s %q is not a full commit id", record.Mutation, field, value))
			}
		}
		if settled := record.Settled; settled != nil {
			if !settled.Result.valid() || settled.At.IsZero() || len(settled.Detail) > maxMergeQueueEvidenceText {
				problems = append(problems, fmt.Errorf("the %s's settlement is not a whole record", record.Mutation))
			}
		} else if index < len(a.Mutations)-1 {
			problems = append(problems, fmt.Errorf("the %s was followed by another before it settled", record.Mutation))
		}
	}
	if landed := a.Landed; landed != nil {
		own := landed.Commit == a.Candidate
		if a.Path == MergeQueueLandThroughForgeQueue {
			// The forge's queue lands a combined commit of its own, which is
			// what landed, and it is the merge the forge recorded.
			own = commitPattern.MatchString(landed.Commit) && landed.Commit == landed.RemoteMerge
		}
		if !own || landed.ConfirmedAt.IsZero() || (landed.RemoteMerge != "" && !commitPattern.MatchString(landed.RemoteMerge)) {
			problems = append(problems, errors.New("its landing is not of what it promoted"))
		}
		if a.SetAside != nil {
			problems = append(problems, errors.New("an attempt that landed is never set aside"))
		}
	}
	if aside := a.SetAside; aside != nil {
		if aside.At.IsZero() || mergeQueueText("the reason it was set aside", aside.Reason, true) != nil {
			problems = append(problems, errors.New("its setting aside is not a whole record"))
		}
		// An attempt is set aside only when nothing it asked for is still
		// unknown: a request whose result nobody established may yet land, and
		// a later attempt beside it would be a second promotion of the entry.
		if record, unsettled := a.Unsettled(); unsettled {
			problems = append(problems, fmt.Errorf("it was set aside with its %s still unsettled", record.Mutation))
		}
		if a.MovedTarget() && !a.Withdrawn() {
			problems = append(problems, errors.New("it was set aside after it had moved the target"))
		}
	}
	problems = append(problems, a.validateWithdrawal()...)
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("promotion attempt %d: %w", a.Number, err)
	}
	return nil
}

// validateWithdrawal checks the attempt's withdrawal: one is written only for a
// merge the forge holds queued and the local target has not been moved for,
// names that merge, and settles once, consistently with the attempt.
func (a MergeQueuePromotionAttempt) validateWithdrawal() []error {
	w := a.Withdrawal
	if w == nil {
		return nil
	}
	var problems []error
	requested, found := a.Mutation(MergeQueueRequestMerge)
	switch {
	case a.Path.MovesLocalTargetFirst():
		problems = append(problems, errors.New("a withdrawal is written for an attempt that moved the local target, which no withdrawal takes back"))
	case !found || requested.Settled == nil || requested.Settled.Result != MergeQueueMutationQueued:
		problems = append(problems, errors.New("a withdrawal is written for a merge the forge does not hold queued"))
	}
	if w.Key != a.MergeQueueWithdrawalKey() || w.PullRequest <= 0 || w.PullRequest != a.PullRequest || w.Pinned != a.Candidate || w.IntendedAt.IsZero() {
		problems = append(problems, errors.New("its withdrawal does not name the merge it asked for"))
	}
	if err := mergeQueueText("withdrawal reason", w.Reason, true); err != nil {
		problems = append(problems, err)
	}
	if len(w.Answers) > MaxMergeQueueWithdrawalAnswers {
		problems = append(problems, fmt.Errorf("%d withdrawal answers exceeds the %d a withdrawal keeps", len(w.Answers), MaxMergeQueueWithdrawalAnswers))
	}
	for _, answer := range w.Answers {
		if answer.At.IsZero() || len(answer.Detail) > maxMergeQueueEvidenceText || strings.TrimSpace(answer.Detail) == "" {
			problems = append(problems, errors.New("a withdrawal answer is not a whole record"))
		}
	}
	if settled := w.Settled; settled != nil {
		if !settled.Result.valid() || settled.At.IsZero() || len(settled.Detail) > maxMergeQueueEvidenceText {
			problems = append(problems, errors.New("its withdrawal's settlement is not a whole record"))
		}
		if settled.Result == MergeQueueWithdrawalConfirmed && a.Landed != nil {
			problems = append(problems, errors.New("a merge confirmed withdrawn is recorded as landed"))
		}
		if settled.Result == MergeQueueWithdrawalLanded && a.SetAside != nil {
			problems = append(problems, errors.New("a merge found landed is set aside"))
		}
	} else if a.SetAside != nil {
		problems = append(problems, errors.New("it was set aside with its withdrawal unsettled"))
	}
	return problems
}

// MovedTarget reports an attempt that has changed the target or asked the
// forge to: such an attempt can only land, wait, or be kept as it is.
func (a MergeQueuePromotionAttempt) MovedTarget() bool {
	for _, record := range a.Mutations {
		if record.Settled == nil {
			continue
		}
		switch record.Mutation {
		case MergeQueueMoveTarget, MergeQueueRequestMerge:
			if record.Settled.Result == MergeQueueMutationDone || record.Settled.Result == MergeQueueMutationQueued {
				return true
			}
		}
	}
	return false
}

// MergeQueueLandingConflictError is a landing write that would change what the
// record already says rather than add to it.
type MergeQueueLandingConflictError struct {
	EntryID string
	Reason  string
}

func (e MergeQueueLandingConflictError) Error() string {
	return fmt.Sprintf("the landing record of merge queue entry %s cannot be written: %s", e.EntryID, e.Reason)
}

// ErrMergeQueueLandingSaveUncertain is a landing write that failed in a way
// that does not say whether it landed, and whose readback could not settle it
// either. The promoter settles it by reading the record again before it does
// anything that depends on it, and requests nothing the record does not show
// was written down first.
var ErrMergeQueueLandingSaveUncertain = errors.New("the merge queue landing record may or may not have been saved, and reading it back did not say")

// Landing reports an entry's landing record, and false where nothing has been
// recorded for it. A record that cannot be read is an error, never an empty
// one.
func (s *MergeQueueStore) Landing(key MergeQueueKey, entryID string) (MergeQueueLanding, bool, error) {
	if err := key.validate(); err != nil {
		return MergeQueueLanding{}, false, fmt.Errorf("merge queue: %w", err)
	}
	if !mergeQueueEntryIDPattern.MatchString(entryID) {
		return MergeQueueLanding{}, false, fmt.Errorf("entry id %q is not a merge queue entry id", entryID)
	}
	queueRoot, err := s.openQueue(key)
	if err != nil {
		return MergeQueueLanding{}, false, err
	}
	defer queueRoot.Close()
	return s.loadLanding(queueRoot, key, entryID, false)
}

// RecordLanding writes an entry's landing record whole. A write may add an
// attempt once every earlier one is set aside, add a mutation, settle one,
// record a landing, a setting aside, a recovery, or a part of the completion;
// it may never remove, reorder or rewrite any of those once written, and a
// write that would is refused with MergeQueueLandingConflictError.
//
// The worker lease for the queue is what a caller presents to write, for the
// reason RecordGeneration's is.
func (s *MergeQueueStore) RecordLanding(worker *Lease, key MergeQueueKey, landing MergeQueueLanding) error {
	if err := key.validate(); err != nil {
		return fmt.Errorf("merge queue: %w", err)
	}
	if worker == nil || worker.file == nil || worker.label != mergeQueueWorkerLabel || worker.scope != key.directory() {
		return ErrMergeQueueWorkerLeaseRequired
	}
	if err := landing.validate(s.productID, key, landing.EntryID); err != nil {
		return err
	}
	queueRoot, err := s.openQueue(key)
	if err != nil {
		return err
	}
	defer queueRoot.Close()
	recorded, found, err := s.loadLanding(queueRoot, key, landing.EntryID, true)
	if err != nil {
		return err
	}
	if found {
		if err := extendsLanding(recorded, landing); err != nil {
			return err
		}
	}
	encoded, err := encodeRecord("merge queue landing", landing)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return StopError{Class: StopStateBound, Cause: fmt.Errorf("the landing record of merge queue entry %s would be %d bytes, limit is %d", landing.EntryID, len(encoded), maxEncodedStateBytes)}
	}
	name := landingRecordName(landing.EntryID)
	saveErr := s.saveLandingRecord(queueRoot, name, encoded)
	if saveErr == nil {
		return nil
	}
	landed, readErr := s.readLandingRecordBack(queueRoot, name)
	if errors.Is(readErr, fs.ErrNotExist) && !found {
		return fmt.Errorf("save the landing record of merge queue entry %s: %w; reading it back found no record", landing.EntryID, saveErr)
	}
	if readErr != nil {
		return fmt.Errorf("%w: save: %w; readback: %w", ErrMergeQueueLandingSaveUncertain, saveErr, readErr)
	}
	if string(landed) == string(encoded) {
		return nil
	}
	return fmt.Errorf("save the landing record of merge queue entry %s: %w; reading it back found it was not saved", landing.EntryID, saveErr)
}

// extendsLanding refuses a write that changes what the record says rather
// than adding to it.
func extendsLanding(recorded, revised MergeQueueLanding) error {
	conflict := func(format string, args ...any) error {
		return MergeQueueLandingConflictError{EntryID: recorded.EntryID, Reason: fmt.Sprintf(format, args...)}
	}
	if recorded.RunID != revised.RunID || recorded.Publication != revised.Publication {
		return conflict("the run and publication it was admitted under never change")
	}
	if len(revised.Attempts) < len(recorded.Attempts) {
		return conflict("a promotion attempt is never removed")
	}
	for index, was := range recorded.Attempts {
		if err := extendsAttempt(was, revised.Attempts[index]); err != "" {
			return conflict("promotion attempt %d: %s", was.Number, err)
		}
	}
	if len(revised.Recoveries) < len(recorded.Recoveries) {
		return conflict("a recovery is never removed")
	}
	for index, was := range recorded.Recoveries {
		if now := revised.Recoveries[index]; !now.At.Equal(was.At) || now.Attempt != was.Attempt || now.Mutation != was.Mutation || now.Found != was.Found {
			return conflict("a recovery is never rewritten")
		}
	}
	if was := recorded.Completion; was != nil {
		now := revised.Completion
		switch {
		case now == nil:
			return conflict("a completion is never withdrawn")
		case now.Attempt != was.Attempt || now.Landed != was.Landed || now.Binding != was.Binding || !now.QueueAt.Equal(was.QueueAt) || now.PullRequest != was.PullRequest:
			return conflict("a completion is never rewritten")
		case (was.RunAt != nil && !sameTime(was.RunAt, now.RunAt)) || (was.WorkItemAt != nil && !sameTime(was.WorkItemAt, now.WorkItemAt)):
			return conflict("a part of a completion, once made, is never withdrawn")
		}
	}
	if was := recorded.Handback; was != nil {
		now := revised.Handback
		switch {
		case now == nil:
			return conflict("a handback is never withdrawn")
		case was.HandedBackAt != nil && !sameTime(was.HandedBackAt, now.HandedBackAt):
			return conflict("a handback, once given to its run, is never withdrawn")
		case !sameHandback(*was, *now):
			return conflict("a handback is never rewritten")
		}
	}
	return nil
}

// sameHandback compares everything a handback fixes when it is written, which
// is all of it but when it was given to its run.
func sameHandback(a, b MergeQueueHandback) bool {
	if len(a.Heads) != len(b.Heads) || !a.At.Equal(b.At) {
		return false
	}
	for index := range a.Heads {
		if a.Heads[index] != b.Heads[index] {
			return false
		}
	}
	a.Heads, b.Heads, a.HandedBackAt, b.HandedBackAt, a.At, b.At = nil, nil, nil, nil, time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func extendsAttempt(was, now MergeQueuePromotionAttempt) string {
	if now.Key != was.Key || now.Number != was.Number || now.Path != was.Path || now.Generation != was.Generation ||
		now.Binding != was.Binding || now.TargetBase != was.TargetBase || now.Candidate != was.Candidate ||
		now.CandidateBranch != was.CandidateBranch || !now.IntendedAt.Equal(was.IntendedAt) {
		return "what it promotes is fixed when it is intended"
	}
	if was.PullRequest != 0 && (now.PullRequest != was.PullRequest || now.PullRequestURL != was.PullRequestURL) {
		return "the pull request that carries it, once open, never changes"
	}
	if len(now.Mutations) < len(was.Mutations) {
		return "a mutation is never removed"
	}
	for index, before := range was.Mutations {
		after := now.Mutations[index]
		if after.Mutation != before.Mutation || after.Key != before.Key || after.Commit != before.Commit ||
			after.Expected != before.Expected || !after.RequestedAt.Equal(before.RequestedAt) {
			return "a mutation is never rewritten"
		}
		if before.Settled != nil && (after.Settled == nil || *after.Settled != *before.Settled) {
			return "a settled mutation is never unsettled or settled again"
		}
	}
	if was.Landed != nil && (now.Landed == nil || *now.Landed != *was.Landed) {
		return "a landing is never withdrawn or rewritten"
	}
	if was.SetAside != nil && (now.SetAside == nil || *now.SetAside != *was.SetAside) {
		return "a setting aside is never withdrawn or rewritten"
	}
	if before := was.Withdrawal; before != nil {
		after := now.Withdrawal
		switch {
		case after == nil:
			return "a withdrawal, once intended, is never removed"
		case after.Key != before.Key || after.PullRequest != before.PullRequest || after.Pinned != before.Pinned ||
			after.Reason != before.Reason || !after.IntendedAt.Equal(before.IntendedAt):
			return "what a withdrawal takes back is fixed when it is intended"
		case len(after.Answers) < len(before.Answers):
			return "a withdrawal answer is never removed"
		case before.Settled != nil && (after.Settled == nil || *after.Settled != *before.Settled):
			return "a settled withdrawal is never unsettled or settled again"
		}
		for index, answer := range before.Answers {
			if after.Answers[index] != answer {
				return "a withdrawal answer is never rewritten"
			}
		}
	}
	return ""
}

func landingRecordName(entryID string) string {
	return "landing-" + entryID + ".json"
}

func (s *MergeQueueStore) loadLanding(queueRoot *repowrite.PinnedRoot, key MergeQueueKey, entryID string, strict bool) (MergeQueueLanding, bool, error) {
	encoded, err := readLandingRecord(queueRoot, landingRecordName(entryID))
	if errors.Is(err, fs.ErrNotExist) {
		return MergeQueueLanding{}, false, nil
	}
	if err != nil {
		return MergeQueueLanding{}, false, fmt.Errorf("read the landing record of merge queue entry %s: %w", entryID, err)
	}
	landing, err := s.decodeLanding(encoded, key, entryID, strict)
	if err != nil {
		return MergeQueueLanding{}, false, err
	}
	return landing, true, nil
}

func (s *MergeQueueStore) decodeLanding(encoded []byte, key MergeQueueKey, entryID string, strict bool) (MergeQueueLanding, error) {
	var landing MergeQueueLanding
	var err error
	if strict {
		err = decodeStrictly(encoded, &landing)
	} else {
		var unknown []string
		unknown, err = decodeTolerating(encoded, &landing)
		noteUnknownFields("merge queue landing", unknown)
	}
	if err != nil {
		return MergeQueueLanding{}, fmt.Errorf("decode the landing record of merge queue entry %s: %w", entryID, err)
	}
	if err := landing.validate(s.productID, key, entryID); err != nil {
		return MergeQueueLanding{}, fmt.Errorf("the landing record of merge queue entry %s: %w", entryID, err)
	}
	return landing, nil
}

func (s *MergeQueueStore) saveLandingRecord(queueRoot *repowrite.PinnedRoot, name string, encoded []byte) error {
	if s.saveLanding != nil {
		return s.saveLanding(queueRoot, name, encoded)
	}
	if err := queueRoot.WriteFile(name, encoded, 0o600, false); err != nil {
		return err
	}
	return queueRoot.Sync()
}

func (s *MergeQueueStore) readLandingRecordBack(queueRoot *repowrite.PinnedRoot, name string) ([]byte, error) {
	if s.readLandingBack != nil {
		return s.readLandingBack(queueRoot, name)
	}
	return readLandingRecord(queueRoot, name)
}

func readLandingRecord(queueRoot *repowrite.PinnedRoot, name string) ([]byte, error) {
	encoded, err := queueRoot.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return nil, fmt.Errorf("the merge queue landing record is %d bytes, which exceeds the %d byte bound", len(encoded), maxEncodedStateBytes)
	}
	return encoded, nil
}

// BoundedMergeQueueText cuts free text to what a settlement's detail carries.
func BoundedMergeQueueText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxMergeQueueEvidenceText {
		return text
	}
	cut := maxMergeQueueEvidenceText
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}
