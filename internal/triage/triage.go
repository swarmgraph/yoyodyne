// Package triage collects the work that has stopped moving and hands it to the
// development manager.
//
// The product manager has the backlog: what is admitted reaches it without
// anybody carrying it there. Nothing carried the other half. A run that ends on
// a durable blocker, and a publication the forge never merged, are both work
// that has stopped and both were only discoverable by an operator who went
// looking — in the tracker for one, on the forge for the other. That is the
// operator standing in as the development manager's eyes, which is exactly what
// the goal this serves says the normal loop must not need.
//
// So a docket entry is the same kind of thing a backlog item is: durable,
// created where the fact was established rather than where somebody happened to
// look, and delivered into the conversation of the role that decides about it.
// What it is not is a decision. An entry says a piece of work stopped and
// carries the evidence of how; whether it is repaired, escalated, or re-scoped
// is the development manager's, and nothing here has an opinion about it.
//
// An entry carries the evidence rather than a summary of it, because the
// decision it feeds turns on the detail: the reviewer's findings in the words
// the reviewer wrote them, the check that failed and what it printed, the
// branch and worktree that were preserved so somebody can still look at the
// change, what the forge says about the merge, and the counters that say how
// much budget the item has already spent. A development manager deciding a
// repair grant without the last of those writes reasoning the configured cap
// then contradicts.
//
// One entry is not an observation but a judgement, and it is here rather than in
// a channel of its own because its destination is the same. A developer or a
// reviewer that finds the work item unmeetable as written says so in the round it
// reached, and what that produces is a decision only the development manager can
// take — so it is docketed like everything else that has stopped moving, and
// reaches her the same way.
package triage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// CapCleared is the ceiling a budget an operator cleared stands at. It is the
// same figure the durable record uses and is declared here for the reason the
// vocabulary beside it is: an entry's shape is what a development manager reads,
// and it must not change because the harness's own schema was refactored.
const CapCleared = math.MaxInt

// SchemaVersion is versioned independently of run state, and of the collected
// reports beside it, for the same reason each of those is: an entry outlives
// the run that produced it, it has no phase and nothing to integrate, and it is
// written once and never revised.
const SchemaVersion = 1

// ClosureSchemaVersion is versioned apart from the entry it settles, because the
// two are written at different moments by different things: an entry is made
// where the work stopped, and a closure where somebody decided about it.
const ClosureSchemaVersion = 1

// Class is what stopped. The six are kept apart because they are found
// differently and read differently: a stopped run is an event the harness was
// present for, a stuck publication is a thing that has not happened, which
// nothing can be present for and only a scan can notice, an unready item is
// work that never started because the tree does not meet what it asks for, an
// unstarted run is a dispatch that died before it could take its item, an
// unstarted attempt is a dispatch that never became a run at all, and an
// escalation is a role saying out loud that the item cannot be met at all.
type Class string

const (
	// ClassStoppedRun is a run that ended on a durable blocker.
	ClassStoppedRun Class = "stopped_run"
	// ClassUnstartedRun is a run that died before it claimed its work item.
	//
	// It is apart from the stopped run for the reason the two shapes inside that
	// class are together: what makes them one fact is that the work is still there
	// and nothing is going to pick it up, and here there is no work — the item was
	// never taken, no worktree was cut, and nothing was preserved. What a
	// development manager decides about it is about the dispatch rather than about
	// a change, because there is no change.
	//
	// It exists because this was the one way a run could fail and reach nobody.
	// Every other stoppage carries either a blocker on the item or artifacts on
	// disk, and each of those is what a docket entry is derived from; a dispatch
	// that died at the claim has neither, so it was recorded on the run and then
	// in no surface anybody reads. yoyodyne-ifd.285 was dispatched twenty-nine
	// times in twenty hours that way.
	ClassUnstartedRun Class = "unstarted_run"
	// ClassUnstartedAttempt is a selection that never became a run at all: the
	// scheduler chose the item and the dispatch failed before anything wrote a run
	// record for it.
	//
	// It is one layer earlier than the class above, and the layer is the whole
	// distinction. An unstarted run exists — it has an identifier, a record, and a
	// state somebody can read afterwards — and what it did not do is claim. This
	// one has none of that: nothing was reserved, so there is no run to name, and
	// every surface the harness has is built on the run record.
	//
	// It exists because that made the failure invisible rather than quiet. On
	// 2026-09-13 a watch session tried two items four hours into a returned
	// capacity window, both dispatches failed before either wrote a record, and
	// the session then excluded both for the rest of its life. Nothing anywhere
	// said the two had been tried: the runs directory had not been written to in
	// five days, and the queue behind them was seventy-four items deep.
	ClassUnstartedAttempt Class = "unstarted_attempt"
	// ClassEscalation is a developer or a reviewer having said, in the round it
	// reached, that the work item cannot be met as it stands. It is the one class
	// that is a judgement rather than an observation, and it is here because the
	// judgement has the same destination as the other three: the development
	// manager decides, and the docket is how work reaches her without anybody
	// carrying it.
	//
	// What separates it from a stopped run is what it cost. A stoppage is what is
	// left after a run spent its budget failing; an escalation is raised in the
	// round the role saw the problem, before any of that, which is the whole point
	// of the verb.
	ClassEscalation Class = "escalation"
	// ClassPublication is an approved publication that did not finish: one the
	// forge has not merged past the configured stuck-merge age, or one the
	// harness already recorded as outstanding — a merge the forge dropped, or one
	// it performed that could not be confirmed.
	ClassPublication Class = "publication"
	// ClassUnreadyItem is an item dispatch declined to start because a
	// prerequisite it states is not met by the tree. It is the one class with no
	// run behind it, and that is the point of it: the whole value of catching this
	// is that it costs a read instead of a run.
	ClassUnreadyItem Class = "unready_item"
	// ClassProductDecision is the Lead Product Manager having decided, while a
	// run for the item was still in flight, that the item is superseded,
	// narrowed, or to be retired. It is the one class about a run that has not
	// stopped, and what it asks the development manager is whether the run stops
	// or finishes. See productdecision.go.
	ClassProductDecision Class = "product_decision"
)

func (c Class) Valid() bool {
	switch c {
	case ClassStoppedRun, ClassUnstartedRun, ClassUnstartedAttempt, ClassEscalation, ClassPublication, ClassUnreadyItem, ClassProductDecision:
		return true
	default:
		return false
	}
}

// Classes is the whole taxonomy, in the order a reader meets them. A caller that
// has to cover every class reads it from here rather than repeating the list.
func Classes() []Class {
	return []Class{
		ClassStoppedRun,
		ClassUnstartedRun,
		ClassUnstartedAttempt,
		ClassEscalation,
		ClassPublication,
		ClassUnreadyItem,
		ClassProductDecision,
	}
}

// Runless reports a class describing work no run record was ever written for.
// Both of them are made about a dispatch rather than about a change: an item the
// tree is not ready for was refused before anything was reserved, and an attempt
// that never became a run died before the reservation it would have been
// recorded by.
func (c Class) Runless() bool {
	return c == ClassUnreadyItem || c == ClassUnstartedAttempt
}

// Title names a class the way the development manager reads it.
func (c Class) Title() string {
	switch c {
	case ClassStoppedRun:
		return "stopped run"
	case ClassUnstartedRun:
		return "run that died before it started"
	case ClassUnstartedAttempt:
		return "attempt that never became a run"
	case ClassEscalation:
		return "item raised as unmeetable"
	case ClassPublication:
		return "unfinished publication"
	case ClassUnreadyItem:
		return "item the tree is not ready for"
	case ClassProductDecision:
		return "product decision about a run in flight"
	default:
		return string(c)
	}
}

// The bounds one entry is held to. They are the same order as the bounds the
// durable run record holds the same evidence to, deliberately: an entry that
// could not carry what the run recorded would be an entry a reader has to go
// back to the run for, which is the errand this exists to remove.
const (
	MaxBlockerBytes     = 16 << 10
	MaxMessageBytes     = 4 << 10
	MaxCheckOutputBytes = 8 << 10
	MaxFindings         = 50
	MaxKeyBytes         = 256
	// MaxPrerequisites bounds what one unready entry names. The readiness check
	// bounds its own reading well below this; the entry states the ceiling anyway,
	// because a durable record must not be able to take an unbounded list from a
	// caller that stopped bounding it.
	MaxPrerequisites = 10
)

// Finding is one reviewer finding as the reviewer wrote it. It is declared here
// rather than imported from the durable run schema for the reason that schema
// declares its own copy of the review vocabulary: what reaches a development
// manager must not change shape because the run record was refactored.
type Finding struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Check is the local or forge check that failed when the work stopped, with
// the bounded output the run captured of it.
type Check struct {
	Command         string `json:"command"`
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output,omitempty"`
	ForgeHeadCommit string `json:"forge_head_commit,omitempty"`
}

// Artifacts are the identifiers of what the stopped work left behind. They are
// recorded even when the branch or the worktree has since been removed, because
// naming what was preserved is half of what makes an entry actionable and
// naming what is gone is the other half: a development manager sent after a
// worktree that no longer exists learns that only by going there.
type Artifacts struct {
	Branch       string `json:"branch,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	BaseCommit   string `json:"base_commit,omitempty"`
	// BranchRemoved and WorktreeRemoved are the harness's own record of the two
	// cleanup steps. They are kept for the entries written before Found existed,
	// and an entry that carries Found is never rendered from them: a flag is what
	// a cleanup remembered to write, which is how run-838ffc48 came to be decided
	// about as having preserved nothing while its branch held the approved change.
	BranchRemoved   bool `json:"branch_removed,omitempty"`
	WorktreeRemoved bool `json:"worktree_removed,omitempty"`
	// Found is what the repository held of the branch and the worktree when the
	// entry was last written — as the run stopped, and again every time the
	// docket is built for somebody to read — and it is what the entry says.
	Found *Found `json:"found,omitempty"`
	// DeveloperSession is the provider session the stopped run's developer was
	// working in. It is an artifact of the run in the way the branch and the
	// worktree are — a continuation resumes it rather than starting a developer
	// from nothing — and it is on the entry because the decision it feeds turns
	// on whether there is one: a repair continues the session, and a re-run
	// discards it along with whatever that session had not committed.
	DeveloperSession string `json:"developer_session,omitempty"`
	// PullRequest and PullRequestURL name the request the run published its
	// branch through, where the project publishes, and PullRequestMerged and
	// PullRequestMergeQueued what the run's record last said the forge did with
	// it: merged, or holding a merge for it. They are on every entry about a run
	// that published rather than only on a publication entry, because a stopped
	// or escalated run leaves its request open on the forge exactly as it leaves
	// its branch, and an entry that named the branch and not the request sent the
	// development manager after the one and left the other for a person to find:
	// yoyodyne-ifd.402 is what that cost. Whether the request is mergeable and
	// what its checks say are not here, because the docket asks the forge nothing
	// when it builds; the record is what it carries.
	PullRequest            int    `json:"pull_request,omitempty"`
	PullRequestURL         string `json:"pull_request_url,omitempty"`
	PullRequestMerged      bool   `json:"pull_request_merged,omitempty"`
	PullRequestMergeQueued bool   `json:"pull_request_merge_queued,omitempty"`
}

// Publication is an unfinished publication as the harness recorded it. There
// are four of those and they are one thing here, because each is a change
// whose promotion is local and whose publication is not: a merge the forge is
// sitting on, a merge it dropped because a requirement of the base branch went
// unmet, a merge it performed that the harness could not then confirm, and a
// promotion whose record holds no request at all — which names no number,
// carries the branch the forge is asked by, and is keyed to the run alone.
//
// Nothing here is asked of the forge when an entry is made: it is what the run
// or the reconciling sweep already observed, so building a docket costs no forge
// calls and an entry describes what was true when it was docketed.
type Publication struct {
	Number      int    `json:"number"`
	URL         string `json:"url,omitempty"`
	Branch      string `json:"branch,omitempty"`
	HeadCommit  string `json:"head_commit,omitempty"`
	State       string `json:"state,omitempty"`
	Merged      bool   `json:"merged,omitempty"`
	MergeQueued bool   `json:"merge_queued,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	// Message is the harness's recorded account of how the merge ended: the
	// outstanding-publication text a dropped or unconfirmed merge leaves behind.
	// It is empty on a publication nobody has recorded anything about, which is
	// what a merge that is simply sitting there looks like — and required on a
	// merged one, which is otherwise finished work rather than something
	// somebody has to look at.
	Message string `json:"message,omitempty"`
	// Checks is the harness's account of the request's checks as the reconciling
	// sweep last read them from the forge — which failed and on what files, how
	// far behind its target the head is, and when that was read — and is empty
	// where no sweep has read them. It is carried because a merge the forge is
	// holding says nothing about whether it will land: two queued merges sat
	// against red builds for a day and more while the docket said only that they
	// were queued (yoyodyne-ifd.429.16).
	Checks string `json:"checks,omitempty"`
	// WaitingOn is the account of a merge the reconciling sweep withdrew because
	// its checks failed on the target itself rather than on the change — which
	// checks, and the item filed for each — and is empty on every other
	// publication. Such a publication waits on those items, and the harness is
	// its next mover (yoyodyne-m5p).
	WaitingOn string `json:"waiting_on,omitempty"`
	// ApprovedAt is when the publication was approved and left unmerged, which
	// is when the run that made it ended. The age an entry reports is measured
	// from here to when it was docketed, so it is an age rather than a countdown
	// to a deadline nothing set.
	ApprovedAt time.Time `json:"approved_at"`
}

// Environmental is a round the environment refused rather than the work failing,
// as the run recorded it. It is declared here rather than imported from the
// durable run schema for the reason Finding is: what reaches a development
// manager must not change shape because the harness's own schema was refactored.
//
// It is the one thing on an entry that changes what the counters beside it mean.
// A stoppage whose last round was environmentally refused is usually an item
// that spent nothing on that round, so a development manager reading the
// counters alone would see an item one round closer to its cap than it is — and
// would decide an escalation the item never earned. Usually rather than always:
// a refusal whose return could not be written, and one asking for a round
// another process charged, both leave the round spent. Which of them this is, is
// what Account says.
type Environmental struct {
	Cause  string `json:"cause"`
	Detail string `json:"detail,omitempty"`
	// Settled says the round the cause belongs to has ended and the class was
	// decided on it; Refused says the class applied. False for Refused is a cause
	// recorded on a round that delivered a change anyway, which spent exactly as
	// any round does, or on one whose settle could not tell.
	Settled       bool `json:"settled,omitempty"`
	Refused       bool `json:"refused,omitempty"`
	RoundReturned bool `json:"round_returned,omitempty"`
	GrantReturned bool `json:"grant_returned,omitempty"`
	// Account is the harness's own sentence about what this round cost the item,
	// carried onto the entry rather than derived here from the flags above. The
	// accounting has several states, and the ones that leave the round spent — a
	// return the settle decided on and could not write, and a round another
	// process is credited with — are the ones a reader must not be told the
	// opposite of, so it is derived once where the round settles and every surface
	// says the same words. An entry written before this was carried has none, and
	// the rendering says so rather than inventing an accounting for it.
	Account string `json:"account,omitempty"`
	// Problem is a return the settle decided on and could not write, which is the
	// one case where the counters above are higher than what the item actually
	// cost and nothing has corrected them.
	Problem string `json:"problem,omitempty"`
}

// RequestedStop is a stop somebody asked of a run, as the entry about that
// run's stoppage carries it.
//
// It is on the entry for two stoppages that read alike and are not. One is the
// stop landing: the run honored it at its next boundary, and where the
// development manager decided it the entry is settled by that decision, with the
// item doing the work instead named. The other is the stop never landing: the
// run passed its last boundary before the request was read and stopped for some
// other reason, a failed review say. That stoppage is a question of its own, and
// the stop decides nothing about it. A development manager who reads that she
// asked for a stop owes the entry a decision all the same, and one who is not
// told she asked would decide it without knowing she had already said the work
// should not go on.
//
// It is declared here rather than imported from the run record for the reason
// Finding is.
type RequestedStop struct {
	// By is who asked, in the words the run's record and the item's notes use.
	By string    `json:"by"`
	At time.Time `json:"at"`
	// Reason is the stop's own reasoning, with the superseding item named where
	// there is one.
	Reason string `json:"reason,omitempty"`
	// Decision is the triage decision the stop carried out, and is set only on a
	// stop the development manager decided.
	Decision string `json:"decision,omitempty"`
	// Landed reports the stop being what ended the run. It is false on the entry
	// about a stoppage the run reached some other way after the stop was asked.
	Landed bool `json:"landed,omitempty"`
	// SupersededBy is the work item the development manager named as doing this
	// run's work instead, on a stop she decided.
	SupersededBy string `json:"superseded_by,omitempty"`
}

// Says is the stop in one sentence: who asked, when, and why.
func (s RequestedStop) Says() string {
	said := fmt.Sprintf("%s asked this run to stop at %s", strings.TrimSpace(s.By), s.At.UTC().Format(time.RFC3339))
	if reason := strings.TrimSpace(s.Reason); reason != "" {
		said += ": " + reason
	}
	return said
}

// IntegrationStop is the environment having stopped an approved change short
// of its promotion: the reviewer approved it, nothing was integrated, and what
// ended the run is a cause the environment answers for. It is the one stoppage
// on this docket that asks the development manager for no decision — the
// reviewer decided, and `yoyo triage resume` carries the change the rest of the
// way at no cost to the item — so the entry says so rather than leaving her to
// choose among verbs that each spend something for it.
//
// It is declared here rather than imported from the run record for the reason
// Finding is: what reaches a development manager must not change shape because
// the run record was refactored.
type IntegrationStop struct {
	Cause  string `json:"cause"`
	Detail string `json:"detail,omitempty"`
	// Phase is the phase the run stopped in, which is the step a resumption
	// re-enters.
	Phase string `json:"phase"`
	// Title says what the cause is, in the words the run record gives it.
	Title string `json:"title,omitempty"`
}

// ResumeIntegrationSays is the one sentence every surface says of an approved
// change the environment stopped short of its promotion: that it is approved,
// what stopped it, and the verb that resumes it. The repair verb refuses in it,
// the docket entry carries it, and the channel line ends on it, so a development
// manager sent from any one of them arrives at the same command.
//
// It is one sentence here rather than one per surface because of what the
// surfaces said before it. On yoyodyne-ifd.309 the repair verb refused with a
// sentence that was true — the run recorded no findings, failing check, or
// refused paths — and pointed nowhere, and what that bought was a re-run and
// four overrides for a change nobody disputed. A refusal that names what the
// run needs is what turns the same stop into one command.
//
// It lives here rather than beside the run record because this is the package
// both can reach: the record imports the docket, and the docket must not import
// the record.
func ResumeIntegrationSays(runID, phase, cause, title string) string {
	return fmt.Sprintf(
		"run %s's change is approved and the environment stopped it at the %s phase — %s (%s) — so what it needs is `yoyo triage resume %s` once the cause has cleared, which resumes the promotion with the approval standing and charges no review round, repair grant, or re-run",
		runID, phase, cause, title, runID)
}

// IntegrationResumable reports whether an approved change the environment
// stopped can still be resumed, from what the repository held of its run. The
// resume restores the checkout from the branch and promotes the reviewed commit
// on it, so the branch is the whole of the answer: a worktree without it is
// nothing a resume can promote, and a branch without a worktree is one it puts
// the checkout back from. A look that could not be made answers yes, for the
// reason Found.Holds holds such a run — the other direction abandons a change
// that may still be there.
//
// found is nil on an entry or a listing written before the look existed, which
// answers from the run's own removal flag and nothing else.
//
// It is the one rule the docket's next mover, the hold the pull reads, and `yoyo
// status` all ask. Before it the docket named the harness and the resume on
// every integration stop while the hold had stopped doing so for a stop whose
// branch was gone, and one stoppage with two next movers is a disagreement only
// the operator can settle.
func IntegrationResumable(found *Found, branchRemoved bool) bool {
	if found == nil {
		return !branchRemoved
	}
	return found.Unknown || found.BranchThere
}

// IntegrationGoneSays is the one sentence every surface says of an approved
// change the environment stopped and whose branch is gone since: that there is
// nothing left for a resume to promote, and that a re-run is the way on. what is
// what was found of the run's change, in the words the surface already says it
// in. The re-run is said before the reason for it, because a listing folds this
// to a line and the way on is the part that must survive the fold. It names no
// verb that would refuse, and it does not say whose move the
// re-run is: that is the hold's and the docket's to close on, from whatever
// triage has already decided about the stoppage.
func IntegrationGoneSays(runID, what string) string {
	return fmt.Sprintf(
		"run %s's branch is gone (%s), so a re-run is the way on: its approved change has nothing left for a resume to promote",
		runID, what)
}

// ReplayConflict is an approved change having conflicted when it was replayed
// onto what its target branch had become. It is the one stoppage after an
// approval that is a decision rather than weather: the environment did not
// stop the change, the target moved under it, and somebody has to say what the
// target is supposed to look like. It is on the entry as its own fact rather
// than left to the blocker, because on yoyodyne-ifd.441 the blocker write
// timed out and the entry named the harness's resume verb as the next mover
// of a conflict that verb would only have met again.
//
// It is declared here rather than imported from the run record for the reason
// IntegrationStop is.
type ReplayConflict struct {
	// TargetBranch is the branch the replay was onto, which is what the conflict
	// is with.
	TargetBranch string `json:"target_branch,omitempty"`
	Detail       string `json:"detail,omitempty"`
	// Phase is the phase the run stopped in.
	Phase string `json:"phase,omitempty"`
}

// ReplayConflictSays is the one sentence every surface says of an approved
// change whose replay conflicted: that it is approved, what it conflicted with,
// and who moves next — the repair-continue, which hands the conflict back to
// the developer that wrote the change (yoyodyne-ifd.132), or a person, and not
// the resume verb, which replays onto the same target and meets the same
// conflict. It is one sentence here for the reason
// ResumeIntegrationSays is: a docket entry and a run record that say different
// things about one conflict send the development manager to different verbs.
func ReplayConflictSays(runID, targetBranch string) string {
	return fmt.Sprintf(
		"run %s's change is approved and its replay onto %s conflicted, so what it needs is `yoyo triage repair %s`, which continues the developer that wrote it with the conflict handed back to reconcile against the target, or a person to settle the conflict — and not `yoyo triage resume`, which would replay onto the same target and meet the same conflict",
		runID, nonEmpty(targetBranch, "its target branch"), runID)
}

// Prerequisite is one thing an item's own statement asks of the tree that the
// tree does not have. It is declared here rather than imported from the package
// that reads it for the reason Finding is: what reaches a development manager
// must not change shape because the reading was refactored.
type Prerequisite struct {
	// Kind is the shape of the prerequisite, in the closed vocabulary the
	// readiness check names them by.
	Kind string `json:"kind"`
	// Missing is what the item needs and the tree does not have, in the item's
	// own words where the item supplied them.
	Missing string `json:"missing"`
	// Evidence is the read that says so. An entry whose refusal nobody can check
	// is one nobody can overrule, which for work that never started is the whole
	// of what the development manager has to go on.
	Evidence string `json:"evidence,omitempty"`
	// Decides is who releases it. It is on the entry rather than inferred,
	// because an item held back by nobody in particular is held back forever.
	Decides string `json:"decides,omitempty"`
}

// Unready is why dispatch declined to start an item: every prerequisite it
// states that the tree does not meet, as they were read.
//
// It describes a reading rather than an event, so unlike every other entry here
// it says what was true when it was written and may since have become false —
// the code the item pointed at can land, and then the item is ready. That is
// said out loud on the entry rather than left for a reader to work out, because
// a docket entry that reads like a standing fact is one somebody acts on months
// later.
type Unready struct {
	Prerequisites []Prerequisite `json:"prerequisites"`
	// ReadAt is when the tree was read. The prerequisites are that reading, and
	// the reading is what may have gone out of date.
	ReadAt time.Time `json:"read_at"`
}

// Kinds are the kinds this reading found, in the order they were recorded. It is
// what the entry's key is derived from: two readings that found the same kinds of
// thing about one item are the same fact however the wording moved.
func (u Unready) Kinds() []string {
	kinds := make([]string, 0, len(u.Prerequisites))
	for _, prerequisite := range u.Prerequisites {
		kinds = append(kinds, strings.TrimSpace(prerequisite.Kind))
	}
	return kinds
}

// Attempt is what the harness was doing when a dispatch failed before any run
// record existed: which item it had chosen, and why it chose it. The failure
// itself is on the entry, in the field every other death records it in.
//
// Why it was chosen is the half that would otherwise be lost outright. Every
// other selection the harness makes is written onto the run record it starts, so
// an attempt that never reserved one is the only place in the harness where the
// reason a thing was picked has nowhere to live — and it is exactly what somebody
// asking "why was this tried at all" goes looking for.
type Attempt struct {
	// SelectedBecause is the selection reason the scheduler recorded, in the same
	// words it would have written onto the run.
	SelectedBecause string `json:"selected_because"`
	// ExcludedForTheSession says the session that made this attempt will not try
	// the item again until the item changes. It is on the entry because it is the
	// other half of what the operator needs: an attempt that failed is one fact,
	// and a queue that will not be pulled again behind it is the one that idles a
	// line.
	ExcludedForTheSession bool `json:"excluded_for_the_session,omitempty"`
}

// Escalation is a role's judgement that the work item cannot be met as it
// stands, as the run recorded it. It is the whole content of the one class that
// carries a judgement: what it asks the development manager for is a decision
// about the item — replan, park, resequence, or redirect — rather than anything
// about the change, of which there is none to speak of.
type Escalation struct {
	// RaisedBy is the role that said it. It is on the entry because the two are
	// read differently: a developer saw the item from inside the work, and a
	// reviewer saw a change made for it and judged that no change would do.
	RaisedBy domain.AgentRole `json:"raised_by"`
	// Reason is that role's own account, in its own words. It is the whole of what
	// the decision is made from, so it is carried verbatim rather than summarized
	// for the reason a reviewer's findings are.
	Reason string `json:"reason"`
}

// Counters are what the item has already spent, beside what the project
// configured it may spend. Both halves travel together on purpose: a
// development manager that sees five rounds without seeing the cap of four
// cannot tell whether granting another is a decision or a contradiction.
//
// Every figure here is read from the durable per-item triage record the guards
// spend and refuse against, rather than counted again from somewhere else. That
// is the whole of what stops the two disagreeing: a view working from its own
// count can show a decision as unrecorded that the guard would refuse a second
// of, which is how one authorized recovery nearly got spent twice.
type Counters struct {
	// ReviewRounds is how many reviews this work item has accumulated across
	// every run made for it, and ReviewRoundsCap is the configured total past
	// which triage may no longer hand it back for another repair.
	ReviewRounds    int `json:"review_rounds"`
	ReviewRoundsCap int `json:"review_rounds_cap"`
	// RepairAttempts is what the stopped run spent of its own repair budget, and
	// RepairGrantAttempts is what a grant would hand the item.
	RepairAttempts      int `json:"repair_attempts"`
	RepairGrantAttempts int `json:"repair_grant_attempts"`
	// The three decisions triage records against a durable budget, each beside
	// the cap that refuses the next one. A decision is spent as it is recorded
	// and long before anything acts on it, so an entry that showed none of them
	// would describe an item nobody had decided anything about — which is exactly
	// what an item with a decision already standing looks like from the outside.
	RepairGrants    int `json:"repair_grants"`
	RepairGrantsCap int `json:"repair_grants_cap"`
	// GrantedRounds is what those grants came to, and TruncatedGrants how many of
	// them the round cap cut down on the way. They are the detail the harness
	// reports back as it records a grant — cut from two rounds to the one the cap
	// still had room for — and the count alone cannot carry it: a grant that was
	// halved and one given in full are different facts about how close this item
	// is to the end of what it will be given, and the entry that shows only "1 of
	// 1" is the entry that sends a development manager to `yoyo status` for the
	// rest.
	GrantedRounds   int `json:"granted_rounds"`
	TruncatedGrants int `json:"truncated_grants"`
	// CommittedRounds is what this item's grants have committed it to, which is
	// the figure the round budget is actually refused against. It exceeds the
	// rounds counted exactly while a grant is recorded and not yet spent, and that
	// window is where a view reporting the rounds alone shows room the guard does
	// not have.
	CommittedRounds int `json:"committed_rounds"`
	Reruns          int `json:"reruns"`
	RerunsCap       int `json:"reruns_cap"`
	// MergeRearms is what triage has decided across every publication of this
	// item, and PublicationRearms what it has decided about the one this entry is
	// about. The cap beside them is read per publication, so the second is the
	// figure that says whether another re-arm may be decided and the first says
	// only how often this item has needed one. A publication entry carries both; a
	// stopped-run entry is about no publication and carries the total alone.
	MergeRearms       int `json:"merge_rearms"`
	PublicationRearms int `json:"publication_rearms,omitempty"`
	MergeRearmsCap    int `json:"merge_rearms_cap"`
	// PublicationRearmsMade is how many of this publication's decided re-arms the
	// harness has actually made. It is the other half of the re-arm gate, exactly
	// as the re-runs carried out are the re-run's: a decision authorizes one
	// repeat of the merge request, so a publication with as many made as decided
	// has had everything triage decided about it carried out, and a further drop
	// of it is an escalation rather than another re-arm.
	PublicationRearmsMade int `json:"publication_rearms_made,omitempty"`
	// RerunsCarriedOut is how many of the recorded re-runs the harness has
	// actually claimed. It is the other half of the re-run gate: a decision
	// authorizes one re-run, so an item with as many claims as decisions has had
	// everything triage decided about it carried out, and the counter alone
	// cannot tell that from a decision still waiting to be acted on.
	RerunsCarriedOut int `json:"reruns_carried_out"`
	// Crossings is how many of this item's caps the development manager has
	// crossed on his own delegated authority, and CrossingsBound how many he gets.
	// They travel together for the reason every count here travels with its
	// ceiling: a development manager who sees four crossings without seeing the
	// bound of five cannot tell whether crossing again is a decision or the
	// escalation the bound exists to force.
	//
	// An entry written before the delegation existed carries zero of both, which
	// reads as an item nobody has crossed anything on — which is what every item
	// was.
	Crossings      int `json:"crossings"`
	CrossingsBound int `json:"crossings_bound"`
	// Standing is what triage has decided about the one stoppage this entry is
	// about, which is the only figure here that is not the item's. Every count
	// above is an item total, and a total cannot say whether the decision it
	// records was made about this run or about some other run of the same item —
	// so the next mover read off one names the harness for a stoppage nobody has
	// decided a thing about. It is joined wherever the docket is read, from the
	// same record and by the same rule the status surfaces read.
	Standing Standing `json:"standing,omitempty"`
}

// CrossingsSpent reports an item whose delegated crossings are gone, which is the
// counter that hands the item back to the operator: past it, more room is not the
// development manager's to give.
func (c Counters) CrossingsSpent() bool {
	return c.CrossingsBound > 0 && c.Crossings >= c.CrossingsBound
}

// Override is one recorded decision to cross this item's caps, as the durable
// triage record has it. It is declared here rather than imported from that record
// for the reason Finding is: what reaches a development manager must not change
// shape because the harness's own schema was refactored.
type Override struct {
	Budget    string    `json:"budget"`
	Cap       int       `json:"cap,omitempty"`
	Cleared   bool      `json:"cleared,omitempty"`
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	Reason    string    `json:"reason"`
	// CrossedBy is the role that crossed the cap on its own delegated authority,
	// and is empty for the operator's own hand. It is carried because a development
	// manager reading an item's budgets has to be able to tell room the operator
	// gave it from room he gave it himself: the first is an answered escalation and
	// the second counts against the crossings he has left.
	CrossedBy string `json:"crossed_by,omitempty"`
}

// Delegated reports an override a role recorded on its own authority rather than
// the operator's.
func (o Override) Delegated() bool { return strings.TrimSpace(o.CrossedBy) != "" }

// Describe says what one override did, the way the entry that carries it reads.
func (o Override) Describe() string {
	crossed := fmt.Sprintf("raised the %s cap to %d", o.Budget, o.Cap)
	if o.Cleared {
		crossed = fmt.Sprintf("cleared the %s cap", o.Budget)
	}
	decided := fmt.Sprintf("decided by %s", strings.TrimSpace(o.DecidedBy))
	if o.Delegated() {
		decided = fmt.Sprintf("crossed by the %s on delegated authority", strings.TrimSpace(o.CrossedBy))
	}
	return fmt.Sprintf("%s, %s at %s: %s",
		crossed, decided, o.DecidedAt.UTC().Format(time.RFC3339), strings.TrimSpace(o.Reason))
}

// Decided reports triage having recorded a re-run of this item that the harness
// has not carried out. It is the state that most needs saying out loud: the
// decision is already spent, so deciding a second is a second decision rather
// than a repeat of this one.
func (c Counters) Decided() bool { return c.Reruns > c.RerunsCarriedOut }

// Standing is what triage has decided about one stoppage, reduced to the facts
// the rule below turns on. It is a shape rather than a record because the two
// readers that ask the question hold the decision in two different forms — the
// docket carries a copy of the item's counters, and the read model has the
// durable ledger itself — and what must not be copied is the rule.
type Standing struct {
	// Decided is a decision recorded about this stoppage at all. An item's budget
	// having been spent is not one: the spend may have been for another run of the
	// same item.
	Decided bool `json:"decided,omitempty"`
	// Spends is that decision being one of the three that buy another attempt,
	// which are the only three the harness carries out.
	Spends bool `json:"spends,omitempty"`
	// Repair is the decision being a repair grant, which is the one kind whose
	// carrying out the decision itself cannot report.
	Repair bool `json:"repair,omitempty"`
	// GrantOutstanding is a granted repair not handed back to its run yet: the
	// item standing committed to rounds it has not spent, where the run's own
	// record shows no continuation since the decision.
	GrantOutstanding bool `json:"grant_outstanding,omitempty"`
	// Refused is the harness having tried to carry this decision out since it was
	// made and been refused by a gate, with the refusal standing on the item's
	// record. A decision waiting on the operator's pause or hold, or one no pass
	// has attempted yet, is not refused.
	Refused bool `json:"refused,omitempty"`
}

// AwaitingCarryOut reports a decision standing about one stoppage that the
// harness has still to act on. It is the one rule that answers it, and both the
// docket's next-mover line and the status surfaces' held-work wait read from
// here: an item given two answers is given two next movers, which is a
// disagreement only the operator can adjudicate.
//
// It is the question the whole docket was failing to answer separately. An entry
// says a stoppage happened, and until the distinction existed nothing on it said
// whether what it was waiting for was a decision or the carrying out of one — so
// a docket of already-decided stoppages read as a decision backlog, which on
// 2026-09-07 it did for days.
//
// Only the three decisions that buy another attempt are ones the harness carries
// out. A wait, a re-scope and an escalation are decided and leave the harness
// nothing to do, so an item still held under one of them is held by what the
// development manager decided rather than by anything outstanding, and naming the
// harness as its next mover would send an operator to watch for a run nothing is
// going to start.
//
// A granted repair is asked of the grant rather than of the decision, because a
// repair continues the run it was granted for: the same run stops again carrying
// the same decision, so the decision alone would go on claiming a carry-out that
// has already happened. The grant standing unspent is what actually says it has
// not.
//
// A re-run and a merge re-arm are answered from the decision itself, which is
// sufficient because carrying either one out changes what the reading is about.
// A re-run produces a fresh run, and once that run stops it is the latest one the
// item has, so the hold names it instead and nothing stands recorded about it. A
// re-arm the forge then honours settles the publication and lifts the hold.
//
// A decision the harness has tried to carry out and been refused is not one it
// still has to act on: what the refusal asks for is hers, which is why the docket
// leads with it (Entry.Critical). Naming the harness over it is how the re-arms
// of the supervisor's periodic pass (yoyodyne-ifd.413) and the maintenance-duties
// item (yoyodyne-ifd.434.10) were refused onto their records from 2026-09-29 while
// every line read about either still said the harness was carrying them out, and
// nobody was placed to record the re-run each needed (yoyodyne-8ff).
func AwaitingCarryOut(standing Standing) bool {
	if !standing.Decided || !standing.Spends || standing.Refused {
		return false
	}
	if standing.Repair {
		return standing.GrantOutstanding
	}
	return true
}

// AwaitingCarryOut reports a decision standing about this entry's own stoppage
// that the harness has still to act on, by the shared rule above.
func (c Counters) AwaitingCarryOut() bool { return AwaitingCarryOut(c.Standing) }

// Rerun is the re-run the harness has already claimed against one docketed
// stoppage: what a guard refuses a second of, named on the entry it is about.
// RunID is the fresh run it started, and is absent on a claim whose run never
// got as far as being reserved.
type Rerun struct {
	ClaimedAt time.Time `json:"claimed_at"`
	RunID     string    `json:"run_id,omitempty"`
}

// Closure is the triage decision that settled one docketed stoppage: the moment
// the entry stopped being something anybody has to decide about.
//
// It exists because an entry is created where work stops and nothing ever took
// one off again. The docket is rebuilt from durable records at every scan, and
// three of the six decisions that settle a stoppage — waiting, re-scoping,
// escalating — spend no counter at all, so a stoppage decided last week came back on every docket after it with
// nothing but the item's notes to say it had been settled. On a long-lived
// product that is the section that exists to surface unhandled work filling up
// with handled work, and the development manager's context lists only the newest
// entries: the decided ones crowd out the ones nobody has looked at.
//
// So a decision closes its entry, and the closure is the record of that. It
// carries the decision and the reasoning rather than only the fact, because what
// a closed entry has to answer for the next reader is why nobody needs to decide
// this again — and it names who decided, so a closure is attributable to the
// conversation that made it rather than to the harness that wrote it down.
type Closure struct {
	SchemaVersion int `json:"schema_version"`
	// Key names the docket entry this settles, which is what makes a closure one
	// per stoppage rather than one per item or one per run: a run can stop and
	// have a publication nobody merged, and deciding about one of those is not
	// deciding about the other.
	Key        string           `json:"key"`
	ProductID  domain.ProductID `json:"product_id"`
	RunID      string           `json:"run_id"`
	WorkItemID string           `json:"work_item_id"`
	// Decision is the development manager's decision in the vocabulary the
	// conversation records it in. It is carried as the word rather than checked
	// against a list here, because that vocabulary belongs to the role's contract
	// and a second copy of it is a second thing to keep in step.
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	// DecidedBy is who settled it, in words a reader recognises: the role and the
	// conversation the decision was recorded in.
	DecidedBy string    `json:"decided_by"`
	ClosedAt  time.Time `json:"closed_at"`
	// RevisitAfter is when a decision stops holding, on the one decision that does
	// not settle anything: waiting says the forge still has the merge, which is
	// "not yet" rather than "decided". A closure carrying it takes the entry off
	// the docket until the moment it names and no longer, so a merge that is still
	// sitting there is put back in front of somebody instead of disappearing on
	// the strength of a decision to look again.
	//
	// Absent on every other decision, which is what a settled stoppage looks like:
	// a repair, a re-run, a re-scope, a re-arm and an escalation each answer the
	// entry, and what happens next is somebody else's or the harness's.
	RevisitAfter time.Time `json:"revisit_after,omitempty"`
}

// Holds reports the decision still standing at a moment. A closure with no
// revisit time holds forever, which is what settling a stoppage means; one with
// a revisit time holds until it, and after that the entry is a question again.
func (c Closure) Holds(at time.Time) bool {
	if c.RevisitAfter.IsZero() {
		return true
	}
	return at.Before(c.RevisitAfter)
}

// MaxDecisionBytes bounds the decision word a closure carries. The vocabulary is
// six short words, so anything near this is prose that arrived where a decision
// was expected.
const MaxDecisionBytes = 64

// Validate reports every contract violation in the closure at once.
func (c Closure) Validate() error {
	var problems []error
	if c.SchemaVersion != ClosureSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", ClosureSchemaVersion))
	}
	switch key := strings.TrimSpace(c.Key); {
	case key == "":
		problems = append(problems, errors.New("key is required: a closure settles one docket entry"))
	case len(key) > MaxKeyBytes:
		problems = append(problems, fmt.Errorf("key is %d bytes, limit is %d", len(key), MaxKeyBytes))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	// An entry no run was ever behind is settled without one: an item the tree was
	// not ready for is taken off by the pull that finds it ready, and there is no
	// run to name because being refused before one was reserved is what the
	// entry is.
	if strings.TrimSpace(c.RunID) == "" && !runlessKey(c.Key) {
		problems = append(problems, errors.New("run id is required"))
	}
	if strings.TrimSpace(c.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	switch decision := strings.TrimSpace(c.Decision); {
	case decision == "":
		// A closure with no decision on it is an entry taken off the docket with
		// nothing saying why, which is worse than one that reappears: the next
		// reader cannot tell it from work nobody ever looked at.
		problems = append(problems, errors.New("the decision that settled this stoppage is required"))
	case len(decision) > MaxDecisionBytes:
		problems = append(problems, fmt.Errorf("decision is %d bytes, limit is %d", len(decision), MaxDecisionBytes))
	}
	if len(c.Reason) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(c.Reason), MaxMessageBytes))
	}
	if strings.TrimSpace(c.DecidedBy) == "" {
		problems = append(problems, errors.New("who decided this is required"))
	}
	if len(c.DecidedBy) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("decided_by is %d bytes, limit is %d", len(c.DecidedBy), MaxMessageBytes))
	}
	if c.ClosedAt.IsZero() {
		problems = append(problems, errors.New("closed_at is required"))
	}
	// A decision to look again at a moment already past is a decision that closed
	// nothing, which nobody could tell from one that settled the stoppage.
	if !c.RevisitAfter.IsZero() && !c.RevisitAfter.After(c.ClosedAt) {
		problems = append(problems, errors.New("a decision to look again does so after it was made"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage docket closure: %w", err)
	}
	return nil
}

// Describe says what was decided about one entry, the way a reader of the entry
// reads it. A decision that only holds for a while says so, because an entry
// carrying one is on the docket again the moment it lapses and a reader told
// only that it was decided would take it for settled.
func (c Closure) Describe() string {
	described := fmt.Sprintf("%q by %s at %s",
		strings.TrimSpace(c.Decision), strings.TrimSpace(c.DecidedBy), c.ClosedAt.UTC().Format(time.RFC3339))
	if !c.RevisitAfter.IsZero() {
		described += ", to be looked at again after " + c.RevisitAfter.UTC().Format(time.RFC3339)
	}
	if reason := strings.TrimSpace(c.Reason); reason != "" {
		described += ": " + reason
	}
	return described
}

// CarryOutCause names a refusal no later attempt of the same decision can clear.
// Empty means the refusal may be retried at the ordinary paced interval.
type CarryOutCause string

const (
	CarryOutWorktreeGone          CarryOutCause = "worktree-gone"
	CarryOutBranchGone            CarryOutCause = "branch-gone"
	CarryOutHeadMoved             CarryOutCause = "head-moved"
	CarryOutDecisionSuperseded    CarryOutCause = "decision-superseded"
	CarryOutDecisionMissing       CarryOutCause = "decision-missing"
	CarryOutStoppageMissing       CarryOutCause = "stoppage-missing"
	CarryOutPublicationUnmakeable CarryOutCause = "publication-unmakeable"
)

// CarryOutCauses is the closed list the record and the inventory share.
func CarryOutCauses() []CarryOutCause {
	return []CarryOutCause{CarryOutWorktreeGone, CarryOutBranchGone, CarryOutHeadMoved,
		CarryOutDecisionSuperseded, CarryOutDecisionMissing, CarryOutStoppageMissing,
		CarryOutPublicationUnmakeable}
}

func (c CarryOutCause) Valid() bool {
	return slices.Contains(CarryOutCauses(), c)
}

// CarryOut is the harness's own last attempt to carry this entry's standing
// decision out, and the gate that stopped it. It is declared here rather than
// imported from the durable record for the reason Finding is: what reaches a
// development manager must not change shape because the harness's own schema was
// refactored.
//
// It exists on an entry only where an attempt failed, which is the whole of what
// makes it worth reading. A decision the harness carried out clears it as it goes,
// so an entry carrying one is a decision that is not going to happen until
// something changes — and saying which thing is the difference between this and
// the thirty-three decided items that sat unfired with nothing anywhere saying so.
type CarryOut struct {
	// Cause marks a gate that will not clear until the decision changes.
	Cause CarryOutCause `json:"cause,omitempty"`
	// RunID names the run the decision is about where that is not the entry's own
	// run: the item's latest decision named a run the docket holds no entry for,
	// and the finding about it is shown on the item's entries so it is seen at
	// all. Empty on a finding about the entry's own stoppage.
	RunID string `json:"run_id,omitempty"`
	// Decision is the word from the triage vocabulary that was being carried out.
	Decision string `json:"decision"`
	// Gate is which gate stopped it, in the closed vocabulary the durable record
	// names them by, and Refusal is what that gate said in its own words.
	Gate    string `json:"gate"`
	Refusal string `json:"refusal"`
	// Clears is what would make the same carry-out succeed. It is the half a
	// refusal alone does not always carry, and it is the whole of what a
	// development manager reading this can act on.
	Clears string `json:"clears"`
	// Waiting marks a gate that clears without anybody doing anything, which is not
	// a refusal of the decision and must not be read as one: nothing was spent, the
	// decision stands, and the next pass carries it out.
	Waiting bool `json:"waiting,omitempty"`
	// Attempts is how many times the harness has been stopped carrying this
	// decision out, which is what tells a gate about to open from one that has been
	// shut for days.
	Attempts  int       `json:"attempts,omitempty"`
	RefusedAt time.Time `json:"refused_at"`
	// Unattempted marks a decision no pass has attempted at all, a poll interval
	// or more after it was recorded: Refusal is then why the pass did not reach it
	// rather than what a gate said, Gate is what kept it from the attempt, and
	// Attempts is zero.
	Unattempted bool `json:"unattempted,omitempty"`
}

// Committed is the round figure the budget is measured against: what this item
// has cost, or what a recorded grant has promised it, whichever is greater. The
// two are not added, for the reason the record that keeps them does not add
// them — a grant's rounds turn into counted rounds as the attempts it bought are
// judged, so a sum would charge a carried-out grant twice.
func (c Counters) Committed() int {
	if c.CommittedRounds > c.ReviewRounds {
		return c.CommittedRounds
	}
	return c.ReviewRounds
}

// RoundsUncommitted is the room the round cap has left for a decision that buys
// rounds. It is the arithmetic the guards refuse against rather than a second
// reading of the same numbers: a view measuring against the rounds counted would
// show room a recorded grant has already spoken for, which is a development
// manager deciding a repair the guard then refuses.
func (c Counters) RoundsUncommitted() int {
	if remaining := c.ReviewRoundsCap - c.Committed(); remaining > 0 {
		return remaining
	}
	return 0
}

// GrantOutstanding reports a repair grant recorded whose rounds the item has not
// spent yet. It is what Decided reports for a re-run: the decision is already
// spent, so deciding a second is a second decision rather than a repeat of this
// one.
func (c Counters) GrantOutstanding() bool { return c.CommittedRounds > c.ReviewRounds }

// Exhausted reports an item with no round left for a decision that buys one,
// which is the counter that decides something on its own: past it, another
// repair is not triage's to grant. It asks what the item is committed to rather
// than only what it has cost, because that is what the guard asks.
func (c Counters) Exhausted() bool { return c.RoundsUncommitted() == 0 }

// Entry is one durable docket entry. It is keyed rather than only identified:
// what makes two entries the same is the event they describe, not when
// something noticed it, so the run that stopped and the sweep that settles it
// afterwards docket one stoppage between them rather than two.
type Entry struct {
	SchemaVersion int              `json:"schema_version"`
	Key           string           `json:"key"`
	Class         Class            `json:"class"`
	ProductID     domain.ProductID `json:"product_id"`
	RunID         string           `json:"run_id"`
	WorkItemID    string           `json:"work_item_id"`
	// WorkItemTitle is what the item is called, carried from the run record that
	// wrote it down at claim time. An entry outlives that run, so a reader who
	// finds it has the identifier and nothing else to say what stopped unless the
	// entry says it in words. Absent means the run recorded no title, which is
	// what every run docketed before titles were carried did.
	WorkItemTitle string    `json:"work_item_title,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
	// Blocker is the durable blocker exactly as it was recorded on the work
	// item. On a publication entry it is empty: nothing blocked the item, the
	// change is integrated, and only its publication is unfinished.
	Blocker string `json:"blocker,omitempty"`
	// Failure is why the run ended, in the words of whatever ended it. It is what
	// a stopped-run entry says instead of a blocker where the death came before
	// anything could record one — a push the remote refused, a backend that broke
	// mid-attempt, a step the machine would not carry — and the run left the change
	// behind. Those deaths hand a person exactly what a blocker does: work that is
	// still there and nothing that will pick it up on its own.
	//
	// It is a field of its own rather than a blocker written after the fact,
	// because a blocker is what the work item carries and these items carry none.
	// A reader is owed that distinction: the item's own status is where they would
	// go next, and an entry that dressed a failure as a blocker would send them
	// looking for something nobody recorded.
	Failure string `json:"failure,omitempty"`
	// StopClass is the runstate-owned wire name, converted at the owning type.
	// This package declares no vocabulary of its own for it.
	StopClass string `json:"stop_class,omitempty"`
	// Findings are the reviewer's own words about the change, and Check is the
	// deterministic check that was failing. Both are absent from work that
	// stopped before either had anything to say.
	Findings []Finding `json:"findings,omitempty"`
	Check    *Check    `json:"check,omitempty"`
	// Summary is the reviewer's summary of the last review, kept beside the
	// findings because a finding list with no verdict prose reads as a set of
	// complaints rather than as a judgement.
	Summary     string       `json:"summary,omitempty"`
	Artifacts   Artifacts    `json:"artifacts"`
	Publication *Publication `json:"publication,omitempty"`
	// SessionResumable says this stoppage is the harness having stopped a
	// provider that had judged nothing — a stream gone silent, or a total budget
	// run out — with the developer session, branch, and worktree it stopped in
	// all still there and no failure ever returned to the developer. A repair
	// decided about it is carried out as a continuation of that session at the
	// point it stalled, spending neither a review round nor a repair attempt,
	// because a stall judges nothing.
	//
	// It is on the entry because it is what separates the two decisions a reader
	// would otherwise weigh from the same evidence. From 2026-09-23 such a
	// stoppage read as a stopped run with no findings and no failing check, which
	// the repair verb then refused for want of a repair input — so the only
	// decision that could be carried out was a re-run, and a re-run starts over
	// from the target branch with the session gone and the uncommitted work in
	// that worktree gone with it.
	SessionResumable bool `json:"session_resumable,omitempty"`
	// CheckStageStop is the run's own account of a stoppage made by the check
	// stage reaching execution.check_stage_timeout: that load stopped it rather
	// than the change, and what happens to it next. It is the sentence the
	// channel says too, so the two cannot come to say different things about one
	// run. Empty on every other stoppage.
	CheckStageStop string `json:"check_stage_stop,omitempty"`
	// CheckStageFailure is the bound's own account of where it stopped the stage,
	// in the words the run ended on.
	CheckStageFailure string `json:"check_stage_failure,omitempty"`
	// HarnessContinuesChecks reports a stoppage the harness continues itself at
	// its checks, on the change the run already has, with no decision asked of
	// anybody: the stage bound stopped it, and the harness's own continuations
	// of it are not yet spent. Validate holds it to an entry that says so.
	HarnessContinuesChecks bool `json:"harness_continues_checks,omitempty"`
	// StallStop is the run's own account of a stoppage made by the harness
	// stopping a provider stream that had gone silent, including in a repair
	// already underway: that the stop judged nothing,
	// whether the stall began in a session a re-adoption resumed, and what
	// happens to it next — the harness's own continuation, or the development
	// manager's decision once that is spent. It is the sentence the channel says
	// too. Empty on every other stoppage.
	StallStop string `json:"stall_stop,omitempty"`
	// HarnessContinuesStall reports a stall the harness continues itself, in the
	// same worktree and session at the phase it stalled in, with no decision asked
	// of anybody: its one continuation by the harness is not yet spent. Validate
	// holds it to an entry that says so.
	HarnessContinuesStall bool `json:"harness_continues_stall,omitempty"`
	// ResumesAt is the phase a resumable stall is continued at, where that is not
	// a developer attempt: the checks or the review, which a run reaches only once
	// its developer attempt is complete. It is empty for a stall mid-attempt,
	// which is continued in the developer session it stalled in. It is on the
	// entry because the verb is the same and what it does is not — a reviewer that
	// stalled is owed the review asked again on the change it has, with no
	// developer invoked, and an entry that said the session is carried on would
	// describe an attempt that never happens.
	// It carries a run phase as a plain string, as IntegrationStop.Phase does,
	// because this package sits beneath the run state that owns phases; the
	// docket store holds it to the checks or the review, through
	// runstate.StallResumeStep, wherever an entry is written or read back.
	ResumesAt string `json:"resumes_at,omitempty"`
	// Unready is why dispatch declined to start this item, on the one class that
	// describes work which never ran. It carries the whole of what a development
	// manager has to decide about: what the item asks for, what the read found,
	// and who releases it.
	Unready *Unready `json:"unready,omitempty"`
	// Attempt is what was being attempted, on the one class made about a dispatch
	// that produced no run record. It carries what the run record would have
	// carried and nothing else: which item, and why it was chosen.
	Attempt *Attempt `json:"attempt,omitempty"`
	// Escalation is a role's judgement that the item cannot be met as it stands,
	// on the one class that carries one. It is written into the entry rather than
	// joined where the docket is read, for the reason the environmental refusal
	// beside it is: it is settled as the run ends, which is before the entry
	// exists.
	Escalation *Escalation `json:"escalation,omitempty"`
	// Environmental is the environment having refused this stoppage's last round,
	// when it did. It is written into the entry rather than joined where the docket
	// is read, unlike the re-run and the overrides beside it, because it is not a
	// decision made afterwards: it is settled as the run ends, which is before the
	// entry exists.
	Environmental *Environmental `json:"environmental,omitempty"`
	// IntegrationStop is the environment having stopped this run's approved
	// change short of its promotion, when that is what stopped it. Like the
	// environmental refusal beside it, it is written into the entry rather than
	// joined where the docket is read: it is settled as the run ends, which is
	// before the entry exists.
	IntegrationStop *IntegrationStop `json:"integration_stop,omitempty"`
	// ReplayConflict is this run's approved change having conflicted when it
	// was replayed onto its target, when that is what stopped it. It is written
	// into the entry for the reason the integration stop is, and the two never
	// stand together: they are the two classifications of one stop after an
	// approval, and an entry carrying both would name two next movers.
	ReplayConflict *ReplayConflict `json:"replay_conflict,omitempty"`
	// StopRequested is a stop somebody asked of this run before it stopped:
	// the one that ended it, or one it never reached. See RequestedStop. Like the
	// integration stop it is written into the entry, because the request is made
	// before the run stops and so before the entry exists.
	StopRequested *RequestedStop `json:"stop_requested,omitempty"`
	// ProductDecision is what the Lead Product Manager decided about the item
	// while this run was in flight, on the one class that carries it.
	ProductDecision *ProductDecision `json:"product_decision,omitempty"`
	Counters        Counters         `json:"counters"`
	// Rerun is the re-run already claimed against this entry's own stoppage, when
	// there is one. It is joined to the entry where the docket is read rather
	// than written into the log: an entry is recorded once as the work stops, and
	// every decision about it is made afterwards, so a claim frozen into the
	// entry could only ever be absent.
	Rerun *Rerun `json:"rerun,omitempty"`
	// Overrides are the operator's own recorded decisions to cross this item's
	// caps. Every cap in Counters is already the configured one as these leave it,
	// so they are not arithmetic a reader has to do — they are the account of why a
	// budget is larger than the project configured, and who is answerable for it. A
	// development manager that saw the room and not the decision would read an
	// override as a cap they had misremembered.
	//
	// Like the re-run beside it, it is joined to the entry where the docket is read
	// rather than written into the log, and for a sharper version of the same
	// reason: an override answers the escalation this entry produced, so one frozen
	// into the entry could only ever be absent.
	Overrides []Override `json:"overrides,omitempty"`
	// Closed is the decision that settled this stoppage, when one has been
	// recorded. Like the re-run and the overrides above it is joined where the
	// docket is read rather than written into the log, and for the same reason
	// sharpened: a decision is made after the work stopped, so a closure frozen
	// into the entry could only ever be absent.
	//
	// An entry carrying one is off the docket: it is not listed for the
	// development manager and not delivered to her, because the question it asks
	// has been answered. It is still on the log, which is what stops the same
	// stoppage being docketed again from the same durable records.
	Closed *Closure `json:"closed,omitempty"`
	// CarryOut is the harness's own last attempt to carry this entry's standing
	// decision out, where a gate stopped it. Like the re-run and the overrides
	// beside it, it is joined where the docket is read rather than written into the
	// log, and for the sharpest version of the same reason: the attempt is made
	// after the decision, which is made after the entry.
	CarryOut *CarryOut `json:"carry_out,omitempty"`
	// Earlier is every other open docketing of the same stopped run, oldest first,
	// each whole: its blocker or failure, findings, failing check, branch and
	// worktree, session, integration stop, publication, counters, decision, and
	// carry-out, exactly as it would read listed on its own. Like the closure it is
	// joined where the docket is read rather than written into the log: the docket
	// holds one live entry per stopped run, and a run docketed again — a stoppage a
	// sweep then found its publication stuck behind, a role's escalation beside the
	// stoppage it ended the run on — is that entry with the newer account on top and
	// the earlier evidence beneath it, rather than a second entry beside it. See
	// Fold.
	Earlier []Entry `json:"earlier,omitempty"`
	// CountersProblem is why the item's durable triage record could not be read
	// for this entry. It is stated rather than left to zeros, which would read as
	// an item nothing has been decided about — the one reading that turns an
	// unreadable record into a second decision nobody meant to make.
	CountersProblem string `json:"counters_problem,omitempty"`
}

// Fold makes the open docket one live entry per stopped run. Entries of one run
// — a stopped run, its escalation, its unfinished publication — are one question
// to whoever decides it: the work is still there and nothing is going to pick it
// up. Listed apart, the same stoppage is put to the development manager once per
// class, and before this a window of sixty-two entries held thirty-eight items.
//
// The entry recorded last stands, in its own place in the order, with the
// others beneath it as Earlier, each carrying all of its own evidence: the
// fold changes what is listed, never what can be read. Entries that name no run are left as they are:
// an unready item and an attempt that never became a run are keyed to the item,
// and each is already one entry per finding.
//
// Fold is asked of open entries only. A run that stops again after a decision
// settled its last stoppage is docketed afresh, and the settled one is not open
// to fold beneath it.
func Fold(entries []Entry) []Entry {
	latest := make(map[string]int, len(entries))
	for index, entry := range entries {
		run := strings.TrimSpace(entry.RunID)
		if run == "" {
			continue
		}
		held, found := latest[run]
		if !found || !entry.RecordedAt.Before(entries[held].RecordedAt) {
			latest[run] = index
		}
	}
	earlier := make(map[string][]Entry, len(latest))
	for index, entry := range entries {
		run := strings.TrimSpace(entry.RunID)
		if run == "" || latest[run] == index {
			continue
		}
		earlier[run] = append(earlier[run], entry)
	}
	if len(earlier) == 0 {
		return entries
	}
	folded := make([]Entry, 0, len(latest))
	for index, entry := range entries {
		run := strings.TrimSpace(entry.RunID)
		if run != "" && latest[run] != index {
			continue
		}
		if beneath := earlier[run]; run != "" && len(beneath) > 0 {
			slices.SortStableFunc(beneath, func(a, b Entry) int { return a.RecordedAt.Compare(b.RecordedAt) })
			entry.Earlier = append(slices.Clone(entry.Earlier), beneath...)
		}
		folded = append(folded, entry)
	}
	return folded
}

// Key names the durable event an entry is about. It is derived rather than
// generated so that the same event yields the same key in every process that
// notices it, which is the whole of what makes docketing idempotent: a run stop
// is one event whether the run recorded it or a later sweep found it, and a
// publication is one event however many sweeps walk past it.
func Key(class Class, runID string) string {
	return string(class) + ":" + strings.TrimSpace(runID)
}

// PublicationKey names the publication event: the run that made the publication
// and the pull request it made. The pull request is part of the identity rather
// than only evidence carried on the entry, because "the publication of this run"
// is not by itself something anybody can point at — one run can publish more
// than once, and a reader that has to work out which publication an entry is
// about from the item's state is a reader that can work it out wrongly. A key
// built from the two durable facts is what nothing has to interpret.
func PublicationKey(runID string, number int) string {
	return Key(ClassPublication, runID) + "#" + strconv.Itoa(number)
}

// UnreadyKey names the event an unready item is: this item found unready for
// these kinds of prerequisite. There is no run in it because there is no run —
// that is what the class is — so the item and what was found are what identify
// it.
//
// The kinds are part of the identity rather than only evidence on the entry. A
// pull re-reads the queue every interval, so keying on the item alone would
// docket one item once and never say a word again when a different prerequisite
// went unmet months later; keying on the reading's wording would docket the same
// finding afresh every time somebody reworded the item. The kinds are the closed
// vocabulary in between, and they are sorted so that two readings that found the
// same things in a different order are one entry.
func UnreadyKey(workItemID string, kinds []string) string {
	sorted := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if trimmed := strings.TrimSpace(kind); trimmed != "" {
			sorted = append(sorted, trimmed)
		}
	}
	slices.Sort(sorted)
	return string(ClassUnreadyItem) + ":" + strings.TrimSpace(workItemID) + ":" + strings.Join(slices.Compact(sorted), "+")
}

// runlessKey reports a key naming an entry of a class no run record is behind,
// which is the keys UnreadyKey and AttemptKey derive.
func runlessKey(key string) bool {
	class, _, found := strings.Cut(strings.TrimSpace(key), ":")
	return found && Class(class).Runless()
}

// AttemptKey names the event an attempt that never became a run is: this item
// tried, and this failure met. There is no run in it because there is no run —
// that is what the class is — so the item and what stopped it are what identify
// it.
//
// The failure is part of the identity rather than only evidence on the entry,
// and it is a digest of the failure rather than the failure itself so that the
// key stays a key. Keying on the item alone would say a word the first time an
// item was ever tried and never again, which is the shape that made this
// invisible; keying on the moment would docket the same dead dispatch afresh
// every session. The failure in between is what actually distinguishes them: a
// dispatch that fails the same way twice is the same standing fact, and one that
// fails a new way is news.
func AttemptKey(workItemID, failure string) string {
	digest := sha256.Sum256([]byte(strings.Join(strings.Fields(failure), " ")))
	return string(ClassUnstartedAttempt) + ":" + strings.TrimSpace(workItemID) + ":" + hex.EncodeToString(digest[:])[:16]
}

// keys are the keys one entry may legitimately carry. There are two only for a
// publication, and only for what is already on disk: entries recorded before the
// pull request joined the key name the run alone, and the docket is an
// append-only log that nothing rewrites. Both are accepted where an entry is
// read and one is written where an entry is made.
func (e Entry) keys() []string {
	if e.Class == ClassUnreadyItem {
		if e.Unready == nil {
			return nil
		}
		return []string{UnreadyKey(e.WorkItemID, e.Unready.Kinds())}
	}
	if e.Class == ClassUnstartedAttempt {
		return []string{AttemptKey(e.WorkItemID, e.Failure)}
	}
	if e.Class == ClassProductDecision {
		if e.ProductDecision == nil {
			return nil
		}
		return []string{ProductDecisionKey(e.RunID, e.ProductDecision.Decision)}
	}
	derived := []string{Key(e.Class, e.RunID)}
	// A publication whose request is unrecorded has no number to key to, and is
	// keyed to the run alone — the first shape above, which is also what an entry
	// made before the request joined the key carries.
	if e.Class == ClassPublication && e.Publication != nil && e.Publication.Number > 0 {
		derived = append(derived, PublicationKey(e.RunID, e.Publication.Number))
	}
	return derived
}

// Validate reports every contract violation in the entry at once.
func (e Entry) Validate() error {
	var problems []error
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !e.Class.Valid() {
		named := make([]string, 0, len(Classes()))
		for _, class := range Classes() {
			named = append(named, strconv.Quote(string(class)))
		}
		problems = append(problems, fmt.Errorf("class %q must be one of %s", e.Class, strings.Join(named, ", ")))
	}
	switch key := strings.TrimSpace(e.Key); {
	case key == "":
		problems = append(problems, errors.New("key is required"))
	case len(key) > MaxKeyBytes:
		problems = append(problems, fmt.Errorf("key is %d bytes, limit is %d", len(key), MaxKeyBytes))
	case e.Class.Valid() && !slices.Contains(e.keys(), key):
		// A key that does not derive from the event it claims to describe would
		// make two records of one stoppage, which is the one thing the key exists
		// to prevent.
		problems = append(problems, fmt.Errorf("key %q does not name the %s event of run %s", e.Key, e.Class, e.RunID))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	// Most classes describe something a run did, so the run is what the entry is
	// about. The two runless ones are exceptions by construction: an unready item
	// is caught before dispatch, which is the whole point, and an attempt that
	// never became a run died before the reservation that would have named one.
	// Either entry, made to name a run, could only be written by the very thing
	// that did not happen.
	if strings.TrimSpace(e.RunID) == "" && !e.Class.Runless() {
		problems = append(problems, errors.New("run id is required"))
	}
	if strings.TrimSpace(e.RunID) != "" && e.Class.Runless() {
		problems = append(problems, fmt.Errorf("a %s entry names no run: none was ever recorded, which is what the class says", e.Class))
	}
	if strings.TrimSpace(e.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	if e.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if len(e.Blocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("blocker is %d bytes, limit is %d", len(e.Blocker), MaxBlockerBytes))
	}
	if len(e.Failure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("failure is %d bytes, limit is %d", len(e.Failure), MaxBlockerBytes))
	}
	if len(e.CheckStageStop) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("check_stage_stop is %d bytes, limit is %d", len(e.CheckStageStop), MaxMessageBytes))
	}
	if len(e.CheckStageFailure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("check_stage_failure is %d bytes, limit is %d", len(e.CheckStageFailure), MaxBlockerBytes))
	}
	if e.HarnessContinuesChecks && strings.TrimSpace(e.CheckStageStop) == "" {
		problems = append(problems, errors.New("harness_continues_checks: only a stoppage the check stage bound made is continued at its checks, so it requires check_stage_stop"))
	}
	if len(e.StallStop) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("stall_stop is %d bytes, limit is %d", len(e.StallStop), MaxMessageBytes))
	}
	if e.HarnessContinuesStall && strings.TrimSpace(e.StallStop) == "" {
		problems = append(problems, errors.New("harness_continues_stall: only a stall the harness stopped is continued by the harness, so it requires stall_stop"))
	}
	if len(e.Summary) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("summary is %d bytes, limit is %d", len(e.Summary), MaxMessageBytes))
	}
	if len(e.Findings) > MaxFindings {
		problems = append(problems, fmt.Errorf("%d findings are recorded, which exceeds the bound of %d", len(e.Findings), MaxFindings))
	}
	for index, finding := range e.Findings {
		if strings.TrimSpace(finding.Message) == "" {
			problems = append(problems, fmt.Errorf("findings[%d]: message is required", index))
		}
		if len(finding.Message) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("findings[%d]: message is %d bytes, limit is %d", index, len(finding.Message), MaxMessageBytes))
		}
		if finding.Line < 0 {
			problems = append(problems, fmt.Errorf("findings[%d]: line %d cannot be negative", index, finding.Line))
		}
	}
	if e.Check != nil {
		if strings.TrimSpace(e.Check.Command) == "" {
			problems = append(problems, errors.New("check: command is required"))
		}
		if len(e.Check.Output) > MaxCheckOutputBytes {
			problems = append(problems, fmt.Errorf("check: output is %d bytes, limit is %d", len(e.Check.Output), MaxCheckOutputBytes))
		}
	}
	if e.Environmental != nil {
		if strings.TrimSpace(e.Environmental.Cause) == "" {
			problems = append(problems, errors.New("stop outside the work: the cause is required, because what it excuses the item is decided by which one it was"))
		}
		if len(e.Environmental.Detail) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("stop outside the work: detail is %d bytes, limit is %d", len(e.Environmental.Detail), MaxMessageBytes))
		}
		if len(e.Environmental.Problem) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("stop outside the work: problem is %d bytes, limit is %d", len(e.Environmental.Problem), MaxMessageBytes))
		}
		if len(e.Environmental.Account) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("stop outside the work: account is %d bytes, limit is %d", len(e.Environmental.Account), MaxMessageBytes))
		}
	}
	if e.SessionResumable {
		// The whole of what the claim is worth is the session it names. An entry
		// that said a session was resumable and named none would send a development
		// manager to record a repair the carry-out then refuses, which is exactly
		// the round trip this field exists to remove.
		if strings.TrimSpace(e.Artifacts.DeveloperSession) == "" {
			problems = append(problems, errors.New("session_resumable: the developer session is required, because it is what a continuation resumes"))
		}
		if strings.TrimSpace(e.Artifacts.WorktreePath) == "" || e.Artifacts.WorktreeRemoved {
			problems = append(problems, errors.New("session_resumable: a preserved worktree is required, because it is what a continued developer carries on in"))
		}
	}
	if e.ResumesAt != "" && !e.SessionResumable {
		problems = append(problems, errors.New("resumes_at: only a resumable stall is continued at a step, so it requires session_resumable"))
	}
	if e.Artifacts.Found != nil {
		if err := e.Artifacts.Found.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("artifacts: found: %w", err))
		}
	}
	if e.IntegrationStop != nil {
		if strings.TrimSpace(e.IntegrationStop.Cause) == "" {
			problems = append(problems, errors.New("integration_stop: the cause is required, because it is what says the environment stopped the change rather than a verdict"))
		}
		if strings.TrimSpace(e.IntegrationStop.Phase) == "" {
			problems = append(problems, errors.New("integration_stop: the phase is required, because it is the step a resumption re-enters"))
		}
		if len(e.IntegrationStop.Detail) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("integration_stop: detail is %d bytes, limit is %d", len(e.IntegrationStop.Detail), MaxMessageBytes))
		}
		if e.Class != ClassStoppedRun {
			problems = append(problems, fmt.Errorf("integration_stop: only a stopped run is an approved change the environment stopped, and this entry is a %s", e.Class))
		}
	}
	if e.ReplayConflict != nil {
		if len(e.ReplayConflict.Detail) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("replay_conflict: detail is %d bytes, limit is %d", len(e.ReplayConflict.Detail), MaxMessageBytes))
		}
		if e.Class != ClassStoppedRun {
			problems = append(problems, fmt.Errorf("replay_conflict: only a stopped run is an approved change whose replay conflicted, and this entry is a %s", e.Class))
		}
		if e.IntegrationStop != nil {
			problems = append(problems, errors.New("replay_conflict: a conflict is a person's and an integration stop is the harness's, so one stop is never both"))
		}
	}
	if e.ProductDecision != nil && e.Class != ClassProductDecision {
		problems = append(problems, fmt.Errorf("product_decision: only a product decision entry carries one, and this entry is a %s", e.Class))
	}
	if e.StopRequested != nil {
		if strings.TrimSpace(e.StopRequested.By) == "" {
			problems = append(problems, errors.New("stop_requested: who asked is required, because a stop nobody is named for is one nobody can answer for"))
		}
		if e.StopRequested.At.IsZero() {
			problems = append(problems, errors.New("stop_requested: when it was asked is required"))
		}
		if len(e.StopRequested.By) > MaxMessageBytes || len(e.StopRequested.Reason) > MaxMessageBytes || len(e.StopRequested.SupersededBy) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("stop_requested: each field is limited to %d bytes", MaxMessageBytes))
		}
		if e.Class != ClassStoppedRun {
			problems = append(problems, fmt.Errorf("stop_requested: only a stopped run was asked to stop, and this entry is a %s", e.Class))
		}
	}
	// Each class is held to the evidence that makes it the thing it claims to
	// be. An entry that cannot say what stopped is an entry nobody can act on,
	// which is worse than no entry: it looks like coverage.
	switch e.Class {
	case ClassStoppedRun:
		// Either says what stopped the run, and one of them has to. A stoppage the
		// harness classified carries the blocker the work item carries; a run that
		// died before anything could classify it carries the reason it gave for
		// dying; a run the check stage bound stopped carries where the bound
		// stopped it.
		if strings.TrimSpace(e.Blocker) == "" && strings.TrimSpace(e.Failure) == "" && strings.TrimSpace(e.CheckStageFailure) == "" {
			problems = append(problems, errors.New("a stopped run entry carries the durable blocker that stopped it, the failure of a death that recorded none, or where the check stage bound stopped it"))
		}
		if e.Publication != nil {
			problems = append(problems, errors.New("a stopped run entry describes a run rather than a publication"))
		}
	case ClassUnstartedRun:
		// The failure is the whole of the entry. There is no blocker because the
		// item was never claimed and nothing could write one on it, and there are
		// no findings, no check, and no publication because the run reached none of
		// them — an entry carrying any of those would be describing a different
		// run.
		if strings.TrimSpace(e.Failure) == "" {
			problems = append(problems, errors.New("an unstarted run entry carries the failure that stopped it before it claimed its item"))
		}
		if strings.TrimSpace(e.Blocker) != "" {
			problems = append(problems, errors.New("an unstarted run entry names no blocker: the item was never claimed, so nothing recorded one on it"))
		}
		if e.Publication != nil || len(e.Findings) > 0 || e.Check != nil {
			problems = append(problems, errors.New("an unstarted run entry describes a run that reached no change: there is nothing reviewed, checked or published to carry"))
		}
	case ClassUnstartedAttempt:
		// The failure and what was attempted are the whole of the entry, and both are
		// required: an entry saying only that something was tried is the silence this
		// class exists to end, wearing a docket entry's clothes.
		if strings.TrimSpace(e.Failure) == "" {
			problems = append(problems, errors.New("an attempt entry carries the failure that stopped it before any run record existed"))
		}
		switch {
		case e.Attempt == nil:
			problems = append(problems, errors.New("an attempt entry carries what was being attempted"))
		case strings.TrimSpace(e.Attempt.SelectedBecause) == "":
			problems = append(problems, errors.New("attempt: why the item was selected is required, because no run record was written to carry it"))
		case len(e.Attempt.SelectedBecause) > MaxBlockerBytes:
			problems = append(problems, fmt.Errorf("attempt: why the item was selected is %d bytes, limit is %d", len(e.Attempt.SelectedBecause), MaxBlockerBytes))
		}
		if strings.TrimSpace(e.Blocker) != "" {
			problems = append(problems, errors.New("an attempt entry names no blocker: the item was never claimed, so nothing recorded one on it"))
		}
		if e.Publication != nil || len(e.Findings) > 0 || e.Check != nil {
			problems = append(problems, errors.New("an attempt entry describes a dispatch that produced no run: there is nothing reviewed, checked or published to carry"))
		}
	case ClassEscalation:
		// The judgement is the whole of the entry, so an entry that cannot carry it
		// is one the development manager can read and not decide from.
		switch {
		case e.Escalation == nil:
			problems = append(problems, errors.New("an escalation entry carries the judgement that was raised"))
		default:
			// The two roles inside a run and no others. Every other role decides about
			// work rather than doing it, and an entry naming one would describe an
			// escalation nothing in a run could have raised.
			if raised := e.Escalation.RaisedBy; raised != domain.RoleDeveloper && raised != domain.RoleReviewer {
				problems = append(problems, fmt.Errorf("escalation: %q is not one of the roles that raises one, which are %q and %q",
					raised, domain.RoleDeveloper, domain.RoleReviewer))
			}
			switch reason := strings.TrimSpace(e.Escalation.Reason); {
			case reason == "":
				problems = append(problems, errors.New("escalation: the reason is required, because it is the whole of what the decision is made from"))
			case len(reason) > MaxBlockerBytes:
				problems = append(problems, fmt.Errorf("escalation: the reason is %d bytes, limit is %d", len(reason), MaxBlockerBytes))
			}
		}
		if e.Publication != nil {
			problems = append(problems, errors.New("an escalation entry describes a judgement about the item rather than a publication"))
		}
	case ClassUnreadyItem:
		switch {
		case e.Unready == nil:
			problems = append(problems, errors.New("an unready item entry carries the prerequisites the tree does not meet"))
		case len(e.Unready.Prerequisites) == 0:
			problems = append(problems, errors.New("an unready item entry names at least one unmet prerequisite: an item nothing is wrong with is one that was dispatched"))
		case len(e.Unready.Prerequisites) > MaxPrerequisites:
			problems = append(problems, fmt.Errorf("%d prerequisites are recorded, which exceeds the bound of %d", len(e.Unready.Prerequisites), MaxPrerequisites))
		}
		if e.Unready != nil {
			if e.Unready.ReadAt.IsZero() {
				problems = append(problems, errors.New("unready: read_at is required, because what the entry says is a reading of the tree at a moment rather than a standing fact"))
			}
			for index, prerequisite := range e.Unready.Prerequisites {
				if strings.TrimSpace(prerequisite.Kind) == "" {
					problems = append(problems, fmt.Errorf("prerequisites[%d]: kind is required, because the entry's key is derived from it", index))
				}
				if strings.TrimSpace(prerequisite.Missing) == "" {
					problems = append(problems, fmt.Errorf("prerequisites[%d]: what is missing is required", index))
				}
				if len(prerequisite.Missing) > MaxMessageBytes {
					problems = append(problems, fmt.Errorf("prerequisites[%d]: what is missing is %d bytes, limit is %d", index, len(prerequisite.Missing), MaxMessageBytes))
				}
			}
		}
		if e.Publication != nil || strings.TrimSpace(e.Blocker) != "" || strings.TrimSpace(e.Failure) != "" {
			problems = append(problems, errors.New("an unready item entry describes work that never started: there is no blocker, no failure and no publication to carry"))
		}
	case ClassProductDecision:
		// The decision is the whole of the entry. Nothing stopped, so there is no
		// blocker, no failure, and nothing reviewed, checked, or published to carry.
		if e.ProductDecision == nil {
			problems = append(problems, errors.New("a product decision entry carries the decision"))
		} else {
			problems = append(problems, e.ProductDecision.validate()...)
		}
		if strings.TrimSpace(e.Blocker) != "" || strings.TrimSpace(e.Failure) != "" || e.Publication != nil || len(e.Findings) > 0 || e.Check != nil {
			problems = append(problems, errors.New("a product decision entry is about a run in flight: there is no blocker, failure, review, check, or publication to carry"))
		}
	case ClassPublication:
		if e.Publication == nil {
			problems = append(problems, errors.New("a publication entry carries the publication it is about"))
		} else {
			// A publication whose request the record does not hold names no
			// number; what it has to name instead is the branch the forge is asked
			// by and the account of the loss, or the entry describes nothing a
			// reader could go and look at.
			if e.Publication.Number <= 0 {
				if strings.TrimSpace(e.Publication.Branch) == "" {
					problems = append(problems, errors.New("publication: a publication with no recorded request names the branch the forge is asked by"))
				}
				if strings.TrimSpace(e.Publication.Message) == "" {
					problems = append(problems, errors.New("publication: a publication with no recorded request carries the account of why none is recorded"))
				}
			}
			// A merged publication is finished work unless the harness recorded
			// that finishing it did not complete — a merge it could not confirm
			// is the one merged publication that still needs a person.
			if e.Publication.Merged && strings.TrimSpace(e.Publication.Message) == "" {
				problems = append(problems, errors.New("publication: a merged publication with nothing outstanding recorded is not stuck"))
			}
			if e.Publication.ApprovedAt.IsZero() {
				problems = append(problems, errors.New("publication: approved_at is required, because the age is measured from it"))
			}
			if len(e.Publication.Message) > MaxBlockerBytes {
				problems = append(problems, fmt.Errorf("publication: message is %d bytes, limit is %d", len(e.Publication.Message), MaxBlockerBytes))
			}
			if len(e.Publication.WaitingOn) > MaxBlockerBytes {
				problems = append(problems, fmt.Errorf("publication: waiting_on is %d bytes, limit is %d", len(e.Publication.WaitingOn), MaxBlockerBytes))
			}
			if len(e.Publication.Checks) > MaxBlockerBytes {
				problems = append(problems, fmt.Errorf("publication: checks is %d bytes, limit is %d", len(e.Publication.Checks), MaxBlockerBytes))
			}
		}
		if strings.TrimSpace(e.Blocker) != "" {
			problems = append(problems, errors.New("a publication entry names no blocker: the change is integrated and only its publication is unfinished"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage docket entry: %w", err)
	}
	return nil
}

// Render describes one entry for whoever is reading the docket. Everything a
// provider or a forge produced is indented under the harness's own line and
// never printed at the margin, exactly as a collected report is.
func (e Entry) Render() string {
	var rendered strings.Builder
	// The run is named where there is one. An unready item has none, and printing
	// an empty pair of brackets for it would read as a run whose identifier
	// nobody recorded — which is a different thing entirely, and one somebody
	// would go looking for.
	if strings.TrimSpace(e.RunID) == "" {
		fmt.Fprintf(&rendered, "  [%s] %s on %s (nothing ran)\n",
			e.Class.Title(), e.RecordedAt.UTC().Format(time.RFC3339), e.item())
	} else {
		fmt.Fprintf(&rendered, "  [%s] %s on %s (%s)\n",
			e.Class.Title(), e.RecordedAt.UTC().Format(time.RFC3339), e.item(), e.RunID)
	}
	// Said first, because it is the one thing that changes what everything below
	// it is for: the evidence is still worth reading and the question it was
	// gathered to answer has been answered. An entry a decision still holds over
	// is not listed on the development manager's docket at all, so this is for
	// wherever else one is shown — and for the entry a lapsed decision has put
	// back, where what was decided last time is the first thing a reader needs.
	if e.Closed != nil {
		rendered.WriteString(indented("Decided", e.Closed.Describe()))
	}
	if e.StopClass != "" {
		rendered.WriteString(indented("Stop cause", e.StopClass))
	}
	rendered.WriteString(e.renderUnready())
	rendered.WriteString(e.renderProductDecision())
	rendered.WriteString(e.renderEscalation())
	rendered.WriteString(e.renderUnstarted())
	rendered.WriteString(e.renderAttempt())
	if e.Blocker != "" {
		rendered.WriteString(indented("Blocker", e.Blocker))
	}
	// Said only where there is no blocker, and labelled as what it is. A death
	// that recorded no blocker left the item carrying none, so a reader told
	// "blocker" would go to the item for words nobody wrote there; and the same
	// reason printed twice beside a blocker that already says it would be noise on
	// every ordinary stoppage.
	//
	// The two unstarted classes say the same field in their own words above,
	// because "died holding its change" is exactly what did not happen to either.
	stoppedByRequest := e.StopRequested != nil && e.StopRequested.Landed
	if e.Blocker == "" && e.Failure != "" && e.Class != ClassUnstartedRun && e.Class != ClassUnstartedAttempt && !stoppedByRequest {
		rendered.WriteString(indented("Died holding its change; the work item carries no blocker for it", e.Failure))
	}
	rendered.WriteString(e.renderStopRequested())
	if e.CheckStageStop != "" {
		rendered.WriteString(indented("Check stage stopped by load", e.CheckStageStop))
		if e.CheckStageFailure != "" {
			rendered.WriteString(indented("Where the bound stopped it", e.CheckStageFailure))
		}
	}
	if e.StallStop != "" {
		rendered.WriteString(indented("Stopped by the harness for a silent provider stream", e.StallStop))
	}
	if e.Summary != "" {
		rendered.WriteString(indented("Review summary", e.Summary))
	}
	for _, finding := range e.Findings {
		location := ""
		if finding.File != "" {
			location = fmt.Sprintf(" (%s:%d)", finding.File, finding.Line)
		}
		rendered.WriteString(indented(fmt.Sprintf("Finding [%s]%s", finding.Severity, location), finding.Message))
	}
	if e.Check != nil {
		label := fmt.Sprintf("Failing check: %s (exit %d)", e.Check.Command, e.Check.ExitCode)
		if e.Check.ForgeHeadCommit != "" {
			label = fmt.Sprintf("Failing forge check: %s (commit %s)", e.Check.Command, e.Check.ForgeHeadCommit)
			rendered.WriteString("      This change failed its forge checks. A repair continues the preserved change in its developer session, with fresh checks and independent review; the unchanged revision cannot be re-armed.\n")
			fmt.Fprintf(&rendered, "      If its developer session or preserved change cannot be recovered, the supported alternative is a re-run decided by the development manager through `yoyo triage rerun %s`.\n", e.RunID)
		}
		rendered.WriteString(indented(
			label, e.Check.Output))
	}
	rendered.WriteString(e.renderArtifacts())
	if e.Publication != nil {
		rendered.WriteString(e.renderPublication())
	}
	// Said before the counters rather than after them, because it is what the
	// counters mean rather than a remark about them: a development manager who
	// read the figures first has already decided how close this item is to its cap.
	rendered.WriteString(e.renderEnvironmental())
	rendered.WriteString(e.renderResumableSession())
	rendered.WriteString(e.renderIntegrationStop())
	rendered.WriteString(e.renderReplayConflict())
	// A product decision names its own next mover, because the question it asks
	// is about a run in flight rather than a stoppage.
	if e.Class != ClassProductDecision {
		rendered.WriteString(e.renderNextMover())
	}
	fmt.Fprintf(&rendered, "      Triage counters: %d of %s review round(s) used%s; %d repair attempt(s) spent in this run; a grant would hand it %d\n",
		e.Counters.ReviewRounds, capFigure(e.Counters.ReviewRoundsCap), roundsNote(e.Counters),
		e.Counters.RepairAttempts, e.Counters.RepairGrantAttempts)
	rendered.WriteString(e.renderDecisions())
	rendered.WriteString(e.renderEarlier())
	return rendered.String()
}

// renderStopRequested says what became of a stop asked of this run. A stop that
// landed is what ended the run, and where it was decided it names the item that
// supersedes this one. A stop that did not land is said beside what actually
// stopped the run, and says outright that it decides nothing about this
// stoppage, because the stop was recorded against a run in flight and this is a
// stoppage it never reached.
func (e Entry) renderStopRequested() string {
	stop := e.StopRequested
	if stop == nil {
		return ""
	}
	if stop.Landed {
		var rendered strings.Builder
		rendered.WriteString(indented("Stopped in flight", stop.Says()))
		if superseded := strings.TrimSpace(stop.SupersededBy); superseded != "" {
			rendered.WriteString(indented("Superseded", fmt.Sprintf(
				"%s is superseded by %s, so it is not pulled again while this run's change is preserved", e.item(), superseded)))
		}
		return rendered.String()
	}
	return indented("A stop was asked for and never reached", stop.Says()+
		"; the run passed its last boundary before the request was read and stopped for the reason above instead, so the stop decides nothing about this stoppage and it is a question of its own")
}

// renderEarlier puts every earlier docketing of the same run beneath everything
// this entry says now, each rendered whole and indented under it, so what the
// decision is made from is all of it and not only the newest account. A decision
// about this entry settles these too, so nothing below is a question of its own.
func (e Entry) renderEarlier() string {
	if len(e.Earlier) == 0 {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Docketed %d time(s) before for this run; one decision here settles all of it, and each earlier docketing follows in full:\n", len(e.Earlier))
	for _, earlier := range e.Earlier {
		for _, line := range strings.SplitAfter(earlier.Render(), "\n") {
			if line == "" {
				continue
			}
			rendered.WriteString("      " + line)
		}
	}
	return rendered.String()
}

// renderUnready says what the item asks of the tree that the tree does not have,
// and who releases each of them. It is silent on every entry that describes a
// run, which is nearly all of them.
//
// When the tree was read is said with it, and said as a reading rather than as a
// fact: everything else on a docket is an event that happened and stays
// happened, and this is the one entry whose subject can become false without
// anybody touching the item.
func (e Entry) renderUnready() string {
	if e.Unready == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Nothing was dispatched: the tree was read at %s and does not meet what this item asks for.\n",
		e.Unready.ReadAt.UTC().Format(time.RFC3339))
	for _, prerequisite := range e.Unready.Prerequisites {
		rendered.WriteString(indented("Unmet ["+prerequisite.Kind+"]", prerequisite.Missing))
		if evidence := strings.TrimSpace(prerequisite.Evidence); evidence != "" {
			rendered.WriteString(indented("The read that says so", evidence))
		}
		if decides := strings.TrimSpace(prerequisite.Decides); decides != "" {
			rendered.WriteString(indented("Who releases it", decides))
		}
	}
	rendered.WriteString("      Every pull reads the item again as the tracker then holds it, and takes this entry off the docket at the first pull that finds the item asking for nothing the tree lacks, or finds it gone from the backlog.\n")
	return rendered.String()
}

// renderEscalation says which role judged the item unmeetable and what it said,
// and it says what the entry is asking for: a decision about the item rather
// than about a change, because there is no change to decide about.
//
// It names what the escalation cost as well, and that is the half a reader would
// otherwise supply wrongly. Every other entry on this docket is work that spent
// its budget before anybody heard about it, so the counters below read as a
// nearly-spent item by default; an escalation is raised in the round it was
// reached, which is what makes replanning it still affordable.
//
// It is silent on every entry that is not one, which is nearly all of them.
func (e Entry) renderEscalation() string {
	raised := e.Escalation
	if raised == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Nothing was integrated: the %s judged this item cannot be met as it stands, in the round it reached, and raised it for your decision — replan, park, resequence, or redirect.\n",
		raised.RaisedBy.Title())
	rendered.WriteString("      This is a raise rather than a stopped run, so a repair is refused on it. Once the item's owner has amended it and released the raise's parking, \"rerun\" starts it again from this run's preserved change where its branch still stands, and \"retire-raise\" ends the raise where the amendment makes that change moot.\n")
	rendered.WriteString(indented("Why the "+raised.RaisedBy.Title()+" says it cannot be met", raised.Reason))
	return rendered.String()
}

// renderUnstarted says the run never got as far as taking its item, and what
// stopped it. It says what that means for the item in the same breath, because
// that is the half a reader would otherwise supply from every other entry on this
// docket: those are all work sitting on a branch somebody has to decide about,
// and this one left the item exactly as it found it.
//
// It is silent on every entry that is not one, which is nearly all of them.
func (e Entry) renderUnstarted() string {
	if e.Class != ClassUnstartedRun {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Nothing was started: this run died before it claimed %s, so the item is untouched, no worktree was cut and nothing was preserved.\n", e.WorkItemID)
	rendered.WriteString(indented("Why it never started", e.Failure))
	return rendered.String()
}

// renderAttempt says that the dispatch never became a run at all, what it was
// for, and what stopped it. It says the first of those out loud because a reader
// arriving at an entry with no run named would otherwise assume the identifier
// went missing rather than that there was never one to lose.
//
// It says the exclusion in the same breath where the session recorded one. That
// is the half nobody could see on 2026-09-13: the failure is one fact, and a
// session that will not try the item again until somebody edits it is the fact
// that turns one failed dispatch into a queue standing still.
//
// It is silent on every entry that is not one, which is nearly all of them.
func (e Entry) renderAttempt() string {
	if e.Class != ClassUnstartedAttempt {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Nothing was started and no run was recorded: the dispatch of %s failed before anything reserved a run, so there is no run to look up and the item is untouched.\n", e.WorkItemID)
	if e.Attempt != nil {
		rendered.WriteString(indented("Why it was selected", e.Attempt.SelectedBecause))
		if e.Attempt.ExcludedForTheSession {
			rendered.WriteString("      The session that tried it will not try it again until the item changes, so nothing pulls it in the meantime.\n")
		}
	}
	rendered.WriteString(indented("Why it never started", e.Failure))
	return rendered.String()
}

// renderResumableSession says this stoppage judged nothing and its developer
// session is still there, so a repair continues that session at the point it
// stalled rather than starting the item over.
//
// It names what the continuation costs as well, because that is the half a
// reader would otherwise supply from every other entry here: a repair elsewhere
// on this docket buys attempts at a change a reviewer or a check complained
// about, and the counters below say how few of those the item has left. Nothing
// complained here, so the continuation spends neither a round nor an attempt,
// and a reader weighing this against the cap would otherwise decide a re-run for
// want of budget it is not being asked for.
//
// It is said under the environmental account rather than over it, because the
// account is what happened and this is what to do about it. It is silent on
// every other stoppage, which is nearly all of them.
//
// A stall at the checks or the review names that step instead of the session,
// because that is what the verb does there: the developer attempt is complete,
// so the step is asked again on the change it left, with no developer invoked.
// It says the branch is kept for the reason it names the session otherwise — it
// is the half a re-run discards.
func (e Entry) renderResumableSession() string {
	if !e.SessionResumable {
		return ""
	}
	if e.ResumesAt != "" {
		return fmt.Sprintf(
			"      Nothing was judged: the harness stopped this run's provider at the %s phase, after its developer attempt was complete, and no failure was ever returned to its developer. `yoyo triage repair %s` continues the run at the %s phase, asking that step again on the change the attempt left, on the branch and in the worktree above, with no developer attempt; the continuation spends no review round and no repair attempt, because a stall judges nothing. A re-run starts over from the target branch instead, discarding the branch the completed attempt produced.\n",
			e.ResumesAt, e.RunID, e.ResumesAt)
	}
	return fmt.Sprintf(
		"      Nothing was judged: the harness stopped this run's provider and no failure was ever returned to its developer, and the session it stopped in is preserved along with whatever that attempt had written. `yoyo triage repair %s` continues that session at the point it stalled, in the worktree above; it spends no review round and no repair attempt, because a stall judges nothing. A re-run starts over from the target branch instead, with the session and whatever that worktree holds uncommitted both discarded.\n",
		e.RunID)
}

// renderIntegrationStop says the change was approved and the environment is
// what stopped it short of the target branch, and it says what that means for
// the reader in the same breath: nothing here is theirs to decide. It is silent
// on every other stoppage, which is nearly all of them.
//
// It names the verb and what the verb costs, because the verbs a reader would
// otherwise reach for each spend something for this stoppage — a repair grant
// for a run with no findings, or a fresh run and a fresh review for a change
// nobody disputed — and that is what four operator overrides on one approved
// change were paying for before this existed. It says so in the sentence the
// repair verb refuses in, so the entry and the refusal a reader gets for
// ignoring it read the same.
func (e Entry) renderIntegrationStop() string {
	stopped := e.IntegrationStop
	if stopped == nil {
		return ""
	}
	var rendered strings.Builder
	title := nonEmpty(stopped.Title, "the environment rather than the work")
	if e.integrationResumable() {
		fmt.Fprintf(&rendered, "      %s. Nothing here is a verdict on the change, and this item's counters stay where the review left them.\n",
			ResumeIntegrationSays(e.RunID, stopped.Phase, stopped.Cause, title))
	} else {
		fmt.Fprintf(&rendered, "      %s. It was stopped at the %s phase by %s (%s), which is no verdict on the change.\n",
			IntegrationGoneSays(e.RunID, e.remains()), stopped.Phase, stopped.Cause, title)
	}
	if detail := strings.TrimSpace(stopped.Detail); detail != "" {
		rendered.WriteString(indented("What the harness found", detail))
	}
	return rendered.String()
}

// renderReplayConflict says the change was approved and its replay onto the
// target conflicted, and it says in the same breath which verb does not answer
// that: the resume, which replays onto the same target. It is silent on every
// other stoppage, which is nearly all of them.
//
// It is said even where the blocker above already describes the conflict,
// because the case it exists for is the one where there is no blocker: the
// write that would have carried it to the work item is what timed out on
// yoyodyne-ifd.441, and the entry was then the only surface that could name the
// conflict at all.
func (e Entry) renderReplayConflict() string {
	conflicted := e.ReplayConflict
	if conflicted == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      %s. The conflict is a decision about the change rather than the environment, and nothing was force-merged.\n",
		ReplayConflictSays(e.RunID, conflicted.TargetBranch))
	if detail := strings.TrimSpace(conflicted.Detail); detail != "" {
		rendered.WriteString(indented("What the replay found", detail))
	}
	return rendered.String()
}

// renderNextMover says which of the two waits this entry is in and who has to
// move next: a stoppage with nothing outstanding about it is yours, and one
// whose recorded decision the harness has still to act on is the harness's.
//
// Which of the two it is comes from AwaitingCarryOut, the same rule the status
// surfaces' held-work wait reads, over this entry's own stoppage rather than
// over the item's totals. An item's budget having been spent says a decision was
// made about some run of it; it does not say this run, and it does not say the
// decision left the harness anything to do — a wait, a re-scope and an
// escalation leave it nothing, and are the development manager's answer rather
// than something to watch for.
//
// It is never silent, because the state it names is the one the docket could not
// say before: an entry describing a decided stoppage read exactly like one
// describing an undecided stoppage, so a docket of work already decided about
// read as work waiting on the development manager. It is said above the counters
// for the reason the environmental account is — it is what the figures under it
// mean rather than a remark about them.
//
// A record nobody could read says that instead of guessing, for the reason the
// decisions below it do: an unreadable record read as an item nobody has decided
// about is how one authorized recovery is nearly spent twice.
//
// An approved change the environment stopped is the one stoppage whose next
// mover is neither: the harness resumes it, and what it waits on is the cause
// clearing and somebody asking — while its branch is there. Once the branch is
// gone there is nothing for the resume to promote, and the stop is answered as
// every other stoppage is, with the re-run named as the way on: that is what the
// hold the pull reads says of the same run, by the same rule.
//
// An approved change whose replay conflicted is yours like any other undecided
// stoppage, and is named as the conflict it is rather than as a bare wait,
// because the verb a reader reaches for after an approval is the resume and the
// resume cannot help it.
func (e Entry) renderNextMover() string {
	gone := ""
	if e.IntegrationStop != nil {
		if e.integrationResumable() {
			return "      Next mover: the harness — this change is approved and the environment stopped it, so what it needs is `yoyo triage resume` once the cause has cleared, not a decision.\n"
		}
		gone = "this approved change's branch is gone, so a re-run is the way on; "
	}
	// A merge withdrawn for its target's red check is the harness's to take up
	// once the items filed for that check close, with nothing to decide.
	if e.Publication != nil && e.Publication.WaitingOn != "" {
		return "      Next mover: the harness — the checks fail on the target itself rather than on this change, and the merge waits on the item filed for that; once it closes the watch re-arms the merge on a level head that passes, or `yoyo reconcile` brings a head the fix left behind up to date, so nothing here needs your decision.\n"
	}
	if e.CountersProblem != "" {
		return "      Next mover: unknown — " + gone + "this item's triage record could not be read, so whether anything is already decided about it cannot be said here.\n"
	}
	// A decision the harness was refused carrying out is hers again, whatever it
	// was: the refusal below says what it is waiting for, and a re-arm refused for a
	// run whose record cannot describe the merge is answered only by a re-run.
	if e.Counters.Standing.Refused {
		return "      Next mover: you — " + gone + "the harness tried to carry out the decision recorded about this stoppage and was refused, as the carry-out below says, so it is not the harness's to act on: what the refusal asks for, or a different decision in its place, is yours.\n"
	}
	if e.Counters.AwaitingCarryOut() {
		return "      Next mover: the harness — " + gone + "a decision about this stoppage is already recorded and has not been carried out, so what is outstanding is the carry-out rather than a decision.\n"
	}
	// A stage the bound stopped is continued by the harness at its checks, with
	// nobody deciding anything, until its continuations are spent. It is said
	// after a recorded decision, because a decision she made about it is what the
	// harness carries out instead.
	if e.HarnessContinuesChecks {
		return "      Next mover: the harness — load stopped this run's check stage rather than the change, so the harness re-runs the checks on the change it already has at the next pull with a slot free and the load low enough; nothing here needs a decision unless you want it to go some other way.\n"
	}
	// A first silent-stream stall is continued by the harness itself, once, with
	// nobody deciding anything; a second is the development manager's, below.
	if e.HarnessContinuesStall {
		step := "the developer attempt it stalled in, in the same session"
		if e.ResumesAt != "" {
			step = fmt.Sprintf("the %s phase it stalled in, on the change it has", e.ResumesAt)
		}
		return fmt.Sprintf("      Next mover: the harness — the harness stopped this run's provider for a silent stream and nothing was judged, so it continues the run itself at %s, at the next pull with a developer slot free; nothing here needs a decision unless you want it to go some other way.\n", step)
	}
	// Nothing of the change left and nothing decided is the one case the pull's
	// hold lets go of: it holds a stop only while a worktree survives or a
	// decision stands. So the item is not waiting on a triage decision — a re-run
	// verb refuses a run that stands on neither a blocker nor a surviving change —
	// and what starts it over is the next pull, which is what this says.
	if gone != "" && !e.changeHeld() {
		return "      Next mover: the next pull — this approved change's branch and worktree are both gone and nothing about this stoppage is decided, so nothing holds the item and the next pull starts it over from the target branch; nothing here needs your decision unless you want it held back.\n"
	}
	// A resumable stall names the verb that continues it, and at which step,
	// because it is the one undecided stoppage whose cheapest answer is not the
	// one every other entry's evidence points at: nothing was judged, so the
	// evidence a reader would weigh a repair against is absent.
	if e.SessionResumable {
		step := "the developer attempt it stalled in, in the same session"
		if e.ResumesAt != "" {
			step = fmt.Sprintf("the %s phase it stalled in, on the change it has, with no developer attempt", e.ResumesAt)
		}
		return fmt.Sprintf("      Next mover: you — nothing the harness has still to carry out is recorded about this stoppage, so what happens to it next is your decision; a repair (`yoyo triage repair %s`) continues it at %s, keeping its branch.\n",
			e.RunID, step)
	}
	if e.ReplayConflict != nil {
		return fmt.Sprintf("      Next mover: you — this change is approved and its replay conflicted, so it is waiting on your decision: a repair (`yoyo triage repair %s`) continues the developer that wrote it with the conflict to reconcile, and `yoyo triage resume` is not the answer.\n", e.RunID)
	}
	return "      Next mover: you — " + gone + "nothing the harness has still to carry out is recorded about this stoppage, so what happens to it next is your decision.\n"
}

// integrationResumable is IntegrationResumable over what this entry last found
// of its run's change.
func (e Entry) integrationResumable() bool {
	return IntegrationResumable(e.Artifacts.Found, e.Artifacts.BranchRemoved)
}

// changeHeld is Found.Holds over what this entry last found of its run's
// change: the branch or the worktree there, or a look that could not be made.
// An entry written before the look existed answers from the run's own removal
// flags.
func (e Entry) changeHeld() bool {
	if found := e.Artifacts.Found; found != nil {
		return found.Holds()
	}
	return (e.Artifacts.Branch != "" && !e.Artifacts.BranchRemoved) ||
		(e.Artifacts.WorktreePath != "" && !e.Artifacts.WorktreeRemoved)
}

// remains is what this entry last found of its run's change, as one clause.
func (e Entry) remains() string {
	if found := e.Artifacts.Found; found != nil {
		return found.Describe()
	}
	if e.Artifacts.BranchRemoved {
		return fmt.Sprintf("branch %s removed as the run's record says, not checked", e.Artifacts.Branch)
	}
	return fmt.Sprintf("branch %s preserved as the run's record says, not checked", e.Artifacts.Branch)
}

// renderDecisions says what triage has already decided about this item, in the
// figures the guards will be read against. It is never silent: a decision the
// guard would refuse a second of and an entry that shows nothing recorded are
// how one authorized recovery is nearly spent twice, once by the development
// manager and once by whoever is helping them.
func (e Entry) renderDecisions() string {
	if e.CountersProblem != "" {
		return indented("Triage decisions could not be read",
			e.CountersProblem+"\nThis says nothing about what has been decided: read the record before deciding anything that spends a budget.")
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Triage decisions recorded: %d of %s repair grant(s)%s; %d of %s re-run(s), %d carried out; %d merge re-arm(s) across every publication%s\n",
		e.Counters.RepairGrants, capFigure(e.Counters.RepairGrantsCap), grantedNote(e.Counters),
		e.Counters.Reruns, capFigure(e.Counters.RerunsCap), e.Counters.RerunsCarriedOut,
		e.Counters.MergeRearms, e.rearmNote())
	rendered.WriteString(e.renderOverrides())
	rendered.WriteString(e.renderCrossingStanding())
	rendered.WriteString(e.renderCarryOut())
	rendered.WriteString(e.renderGrantStanding())
	rendered.WriteString(e.renderRerunStanding())
	rendered.WriteString(e.renderRearmStanding())
	return rendered.String()
}

// renderEnvironmental says the environment refused this stoppage's last round,
// and what the item was therefore not charged for it. It is silent on every
// ordinary stoppage, which is nearly all of them.
//
// A refusal whose return could not be written says so in the same breath. That
// is the one state where the counters below it are higher than what the item
// actually cost and nothing has corrected them, and a reader who is not told
// will decide an escalation against a figure the harness knows is wrong.
func (e Entry) renderEnvironmental() string {
	refused := e.Environmental
	if refused == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Round: %s\n", nonEmpty(refused.Account,
		fmt.Sprintf("a cause outside the work was recorded: %s; what it cost this item is not recorded on this entry", refused.Cause)))
	if detail := strings.TrimSpace(refused.Detail); detail != "" {
		rendered.WriteString(indented("What the harness found", detail))
	}
	if problem := strings.TrimSpace(refused.Problem); problem != "" {
		rendered.WriteString(indented("This round could not be paid back in full",
			problem+"\nThe counters below therefore include a round this item did not cost: read them as higher than the item stands at."))
	}
	return rendered.String()
}

// renderOverrides says which of this item's caps an operator crossed, and who
// crossed them. The budgets above are already stated as the overrides leave them,
// which is exactly why this cannot be left out: a development manager reading
// room the configured cap does not have, with nothing saying where it came from,
// is a development manager deciding whether to trust the number.
//
// It is silent on the ordinary item, which is every item. An override is an
// operator answering an escalation by hand, and a line announcing that nobody has
// had to is one every reader learns to skip.
func (e Entry) renderOverrides() string {
	var rendered strings.Builder
	for _, override := range e.Overrides {
		// The two are labelled apart because they answer different questions. An
		// operator override is somebody else having answered an escalation, and a
		// delegated crossing is the development manager's own earlier decision about
		// this item — which is the best evidence there is about whether crossing it
		// again will help, and it is one of five.
		label := "Operator override"
		if override.Delegated() {
			label = "Cap crossed on delegated authority"
		}
		fmt.Fprintf(&rendered, "      %s: %s\n", label, override.Describe())
	}
	return rendered.String()
}

// renderCrossingStanding says where this item stands with the caps the
// development manager may cross himself. It is silent until one has been
// crossed, for the reason the re-arm standing is: an untouched delegation
// announced on every ordinary stoppage is a line every reader learns to skip.
//
// Once one has been crossed it is never silent again, and the exhausted case says
// what happens instead rather than only that the room is gone: an item past the
// bound is the operator's, and a development manager who learns that by being
// refused has spent a turn finding out something the entry could have told them.
func (e Entry) renderCrossingStanding() string {
	counters := e.Counters
	if counters.Crossings == 0 {
		return ""
	}
	if counters.CrossingsSpent() {
		return fmt.Sprintf("      A further cap crossing for %s is not yours: %d of %d crossing(s) on your own authority are recorded, and past the bound the caps are the operator's again. Escalate, and say which cap and why.\n",
			e.WorkItemID, counters.Crossings, counters.CrossingsBound)
	}
	return fmt.Sprintf("      %d of %s cap crossing(s) on your own authority are recorded against %s; each further one is reported to the operator as you record it.\n",
		counters.Crossings, capFigure(counters.CrossingsBound), e.WorkItemID)
}

// renderCarryOut says that the harness tried to carry this entry's decision out
// and was stopped, which gate stopped it, and what would clear that gate.
//
// It is silent on every entry the harness has not been stopped on, which is
// nearly all of them: a carry-out that succeeded clears the finding as it goes,
// and one nothing has reached yet has nothing to report. So a line here is always
// a decision that is not going to happen on its own, which is exactly the state
// that used to be reported nowhere at all.
//
// A gate that clears without anybody doing anything is worded as waiting rather
// than as a refusal. The difference is the whole of what a development manager
// does about it: a full harness and a pause somebody will lift need no decision
// from her, and reading either as a refusal is how a decision gets made twice.
func (e Entry) renderCarryOut() string {
	stopped := e.CarryOut
	if stopped == nil {
		return ""
	}
	var rendered strings.Builder
	if stopped.RunID != "" && stopped.RunID != e.RunID {
		fmt.Fprintf(&rendered, "      Your latest decision on %s is about run %s, which this docket holds no entry for, so it is said here:\n",
			e.WorkItemID, stopped.RunID)
	}
	if stopped.Unattempted {
		fmt.Fprintf(&rendered, "      No pass has attempted the %q you decided, as of %s; %s kept it back: %s\n",
			stopped.Decision, stopped.RefusedAt.UTC().Format(time.RFC3339), stopped.Gate, strings.TrimSpace(stopped.Refusal))
		rendered.WriteString(indented("What would clear it", stopped.Clears))
		rendered.WriteString("      Nothing was spent and the decision still stands; the first pass that reaches it attempts it, and what that attempt comes to replaces this.\n")
		return rendered.String()
	}
	if stopped.Waiting {
		fmt.Fprintf(&rendered, "      The harness is carrying out the %q you decided and is waiting on %s (%s, last at %s): %s\n",
			stopped.Decision, stopped.Gate, plural(stopped.Attempts, "attempt", "attempts"),
			stopped.RefusedAt.UTC().Format(time.RFC3339), strings.TrimSpace(stopped.Refusal))
		rendered.WriteString(indented("What it is waiting for", stopped.Clears))
		rendered.WriteString("      Nothing was spent and the decision still stands, so the pass that finds it open carries out the same one.\n")
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "      The harness tried to carry out the %q you decided and %s refused it (%s, last at %s): %s\n",
		stopped.Decision, stopped.Gate, plural(stopped.Attempts, "attempt", "attempts"),
		stopped.RefusedAt.UTC().Format(time.RFC3339), strings.TrimSpace(stopped.Refusal))
	rendered.WriteString(indented("What would clear it", stopped.Clears))
	if stopped.Cause != "" {
		rendered.WriteString("      This gate will not clear on its own; no further attempt is made until the development manager changes the decision, by recording a re-run or an escalation.\n")
	} else {
		rendered.WriteString("      Nothing was spent, so this decision is carried out by the first pass after that is no longer so; until then the harness keeps trying it at a paced interval rather than every pass.\n")
	}
	return rendered.String()
}

// plural says a count with the noun it counts, so a line reads as a sentence
// rather than as a figure with a bracketed suffix.
func plural(count int, singular, many string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, many)
}

// capFigure is one ceiling as a reader reads it. A cleared cap stands at a number
// nothing this harness counts comes near, so printing the number would be a line
// a development manager has to decode before they can act on it. Who cleared it
// is not repeated here: the override line beside these figures says so, and every
// budget on the entry would otherwise carry the same clause.
func capFigure(limit int) string {
	if limit == CapCleared {
		return "no cap"
	}
	return strconv.Itoa(limit)
}

// renderGrantStanding says where this item stands with the repair grants triage
// may still give it, in the figures the grant guard reads. The counts above
// cannot answer it: the grant counter is a total nothing clears, and the round
// budget refuses against what the item is committed to rather than what it has
// cost, so an entry that stopped at the counts leaves a development manager to
// find out by deciding a repair and being refused. Which is the round-trip this
// says out loud instead.
func (e Entry) renderGrantStanding() string {
	counters := e.Counters
	if counters.RepairGrants == 0 {
		return ""
	}
	spentGrants := counters.RepairGrants >= counters.RepairGrantsCap
	spentRounds := counters.RoundsUncommitted() == 0
	// Both at once is said as both, rather than as the first of them. A grant
	// stands behind two budgets, and an entry that named one sent an operator to
	// cross it and the same decision back to be refused by the other — which cost
	// two override ceremonies minutes apart on each of two items on 2026-09-05.
	if spentGrants && spentRounds {
		return fmt.Sprintf("      A further repair grant for %s is refused by both of its budgets: %d of %d permitted grant(s) are already recorded, and %d of %d round(s) are spent or committed. Crossing either one alone leaves the other refusing it.\n",
			e.WorkItemID, counters.RepairGrants, counters.RepairGrantsCap, counters.Committed(), counters.ReviewRoundsCap)
	}
	if spentGrants {
		return fmt.Sprintf("      A further repair grant for %s is refused: %d of %d permitted grant(s) are already recorded, so deciding another spends nothing and is an escalation rather than a larger budget.\n",
			e.WorkItemID, counters.RepairGrants, counters.RepairGrantsCap)
	}
	if spentRounds {
		return fmt.Sprintf("      A further repair grant for %s is refused by the review round budget: %d of %d round(s) are spent or committed, so there is nothing left to grant.\n",
			e.WorkItemID, counters.Committed(), counters.ReviewRoundsCap)
	}
	if counters.GrantOutstanding() {
		return fmt.Sprintf("      A repair grant of %s is recorded and its rounds are not spent, so this stoppage may be handed back on the decision that stands; deciding another would spend a further one rather than repeat it.\n",
			e.WorkItemID)
	}
	return ""
}

// rearmNote states this publication's own re-arm budget beside the item's total,
// which is the only one of the three budgets the total does not answer for: a
// re-arm is bounded per publication, so the cap belongs beside the publication's
// figure and printing it beside the total would state a ceiling for a count it
// does not bound. A stopped-run entry is about no publication and gets none.
func (e Entry) rearmNote() string {
	if e.Class != ClassPublication {
		return ""
	}
	return fmt.Sprintf(", %d of %s for this publication, %d made",
		e.Counters.PublicationRearms, capFigure(e.Counters.MergeRearmsCap), e.Counters.PublicationRearmsMade)
}

// renderRearmStanding says where this entry's publication stands with the
// re-arms it may still be given, in the figures the re-arm guard reads. The
// budget is the publication's rather than the item's, so the item's total cannot
// answer it: an item that published three times has three separate merges the
// forge could drop, and the total would report the third publication as spent
// for what the first cost.
//
// It is silent until one has been recorded, because an entry that announced an
// untouched budget on every publication would be a line every reader learns to
// skip — and silent on a stopped run, which is about no publication at all.
func (e Entry) renderRearmStanding() string {
	if e.Check != nil && e.Check.ForgeHeadCommit != "" {
		return ""
	}
	if e.Class != ClassPublication || e.Counters.PublicationRearms == 0 {
		return ""
	}
	// A decision nothing has acted on is the more particular answer and is given
	// first, exactly as the re-run's is: at a cap of one those two are the same
	// arithmetic, and "refused" would be the wrong half of it — the decision that
	// spent the budget is still there to be carried out.
	if e.Counters.PublicationRearms > e.Counters.PublicationRearmsMade {
		return "      A merge re-arm of this publication is recorded and the harness has not made it, so its merge request may be repeated on the decision that stands; deciding another would spend a further one rather than repeat it.\n"
	}
	if e.Counters.PublicationRearms >= e.Counters.MergeRearmsCap {
		return fmt.Sprintf("      A further merge re-arm of this publication is refused: %d of %d permitted re-arm(s) are already recorded against it, and a merge the forge keeps dropping is a repository somebody has to look at.\n",
			e.Counters.PublicationRearms, e.Counters.MergeRearmsCap)
	}
	return ""
}

// renderRerunStanding says where this stoppage stands with the one re-run it
// gets, which is the question the counters above cannot answer on their own: the
// re-run counter is a total nothing clears, so what it means for this entry
// depends on what has been claimed against it.
func (e Entry) renderRerunStanding() string {
	if e.Rerun != nil {
		if e.Rerun.RunID == "" {
			return fmt.Sprintf("      This stoppage was already re-run at %s and no fresh run was recorded for it; triage re-runs a docketed stoppage once, so another is refused.\n",
				e.Rerun.ClaimedAt.UTC().Format(time.RFC3339))
		}
		return fmt.Sprintf("      This stoppage was already re-run as run %s; triage re-runs a docketed stoppage once, so another is refused.\n", e.Rerun.RunID)
	}
	if e.Counters.Decided() {
		return fmt.Sprintf("      A re-run of %s is already recorded and not yet carried out, so this stoppage may be run again on the decision that stands; deciding another would spend a further one rather than repeat it.\n",
			e.WorkItemID)
	}
	if e.Counters.Reruns > 0 {
		return fmt.Sprintf("      Every recorded re-run of %s has been carried out, so this stoppage has no decision of its own to act on; a further one is refused past the cap and is an escalation rather than a larger budget.\n",
			e.WorkItemID)
	}
	return ""
}

// item names the work an entry is about: the identifier, which is what the
// development manager acts on, and what the item is called, which is what tells
// them what stopped without their going to the tracker for it. An entry whose
// run recorded no title names the identifier alone.
func (e Entry) item() string {
	if title := strings.TrimSpace(e.WorkItemTitle); title != "" {
		return e.WorkItemID + " — " + title
	}
	return e.WorkItemID
}

func (e Entry) renderArtifacts() string {
	var rendered strings.Builder
	found := e.Artifacts.Found
	worktreeGone := e.Artifacts.WorktreeRemoved
	if found != nil {
		worktreeGone = !found.Unknown && !found.WorktreeThere
	}
	if e.Artifacts.Branch != "" {
		// What the repository held when this entry was last written, where it was
		// looked for. An entry written before the look existed says what its
		// run's record said and that nothing looked, so it is never read as a check.
		state := "preserved as the run's record says, not checked"
		switch {
		case found != nil:
			state = found.BranchState()
		case e.Artifacts.BranchRemoved:
			state = "removed as the run's record says, not checked"
		}
		fmt.Fprintf(&rendered, "      Branch (%s): %s\n", state, e.Artifacts.Branch)
	}
	if e.Artifacts.WorktreePath != "" {
		state := "preserved as the run's record says, not checked"
		switch {
		case found != nil:
			state = found.WorktreeState()
		case e.Artifacts.WorktreeRemoved:
			state = "removed as the run's record says, not checked"
		}
		fmt.Fprintf(&rendered, "      Worktree (%s): %s\n", state, e.Artifacts.WorktreePath)
	}
	if e.Artifacts.DeveloperSession != "" {
		state := "preserved"
		if worktreeGone {
			// The session is the provider's and nothing here removes one, but there
			// is nowhere left to continue it: what a continued developer works in is
			// the checkout, and a retired one is what a resumption has nothing to
			// hand back.
			state = "preserved, with no checkout left to continue it in"
		}
		fmt.Fprintf(&rendered, "      Developer session (%s): %s\n", state, e.Artifacts.DeveloperSession)
	}
	if e.Artifacts.TargetBranch != "" {
		fmt.Fprintf(&rendered, "      Integration target: %s\n", e.Artifacts.TargetBranch)
	}
	// A publication entry says everything about its request below, so the line
	// here is for the other classes: the request a stopped or escalated run left
	// open on the forge, which a decision about the run has to account for.
	if e.Artifacts.PullRequest > 0 && e.Class != ClassPublication {
		state := "open on the forge, unmerged, no merge armed"
		switch {
		case e.Artifacts.PullRequestMerged:
			state = "merged"
		case e.Artifacts.PullRequestMergeQueued:
			state = "open on the forge, merge armed and queued"
		}
		fmt.Fprintf(&rendered, "      Pull request (%s): #%d %s\n", state, e.Artifacts.PullRequest, e.Artifacts.PullRequestURL)
	}
	return rendered.String()
}

func (e Entry) renderPublication() string {
	published := *e.Publication
	var rendered strings.Builder
	// No number is the one publication the harness has no request to finish: the
	// entry says so, names the branch the forge is asked by, and carries the
	// account below rather than a forge state nothing has read.
	if published.Number <= 0 {
		fmt.Fprintf(&rendered, "      Pull request: none recorded for branch %s; `yoyo reconcile` looks it up on the forge by that branch, records it, and arms its merge\n", published.Branch)
		if published.Message != "" {
			rendered.WriteString(indented("Publication outstanding", published.Message))
		}
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "      Pull request #%d %s\n", published.Number, published.URL)
	merge := "the forge reports it unmerged"
	switch {
	case published.Merged:
		merge = "the forge merged it and the harness could not finish the publication"
	case published.MergeQueued:
		merge = "the forge has its merge queued"
	}
	fmt.Fprintf(&rendered, "      Forge state: %s, %s\n", nonEmpty(published.State, "unreported"), merge)
	// A queued merge is never shown without its checks beside it, because a merge
	// the forge is holding for checks that will not pass is not going to land.
	switch {
	case published.Checks != "":
		fmt.Fprintf(&rendered, "      Checks: %s\n", published.Checks)
	case published.MergeQueued:
		rendered.WriteString("      Checks: not yet read by a reconcile sweep, so whether the queued merge can land is unknown\n")
	}
	if published.WaitingOn != "" {
		fmt.Fprintf(&rendered, "      Waiting on the target: the merge %s\n", published.WaitingOn)
	}
	if published.MergeCommit != "" {
		fmt.Fprintf(&rendered, "      Forge merge commit: %s\n", published.MergeCommit)
	}
	// The age is what made this an entry, so it is stated as an age rather than
	// left to be worked out from two timestamps.
	if !published.ApprovedAt.IsZero() {
		fmt.Fprintf(&rendered, "      Approved at %s, unmerged for %s when docketed\n",
			published.ApprovedAt.UTC().Format(time.RFC3339), describeAge(e.RecordedAt.Sub(published.ApprovedAt)))
	}
	if published.Message != "" {
		rendered.WriteString(indented("Forge merge message", published.Message))
	}
	return rendered.String()
}

// roundsNote qualifies the rounds an item has used with what it stands
// committed to, where the two differ. The rounds counted are what the item has
// cost and the commitment is what the cap refuses against, so an entry that
// stated the first alone reports room the guard does not have for exactly as
// long as a grant is waiting to be spent — which is the whole of the window a
// development manager reads a docket in.
func roundsNote(counters Counters) string {
	committed := counters.Committed()
	switch {
	case counters.Exhausted() && counters.GrantOutstanding():
		return fmt.Sprintf(" (%d of %s are committed by a grant not yet spent, so the cap is reached: another repair is not triage's to grant)",
			committed, capFigure(counters.ReviewRoundsCap))
	case counters.Exhausted():
		return " (the cap is reached: another repair is not triage's to grant)"
	case counters.GrantOutstanding():
		return fmt.Sprintf(" (%d of %s are committed by a grant not yet spent)", committed, capFigure(counters.ReviewRoundsCap))
	default:
		return ""
	}
}

// grantedNote is what the recorded grants actually came to, which the count of
// them does not say. A grant the round cap cut is the fact that says this item
// is at the end of what it will be given, and it is reported to whoever records
// the decision at the moment they record it — so an entry that dropped it is an
// entry that disagrees with what the harness already told them.
func grantedNote(counters Counters) string {
	if counters.GrantedRounds == 0 {
		return ""
	}
	if counters.TruncatedGrants > 0 {
		return fmt.Sprintf(" worth %d review round(s), %d of them cut down to the room the cap still had",
			counters.GrantedRounds, counters.TruncatedGrants)
	}
	return fmt.Sprintf(" worth %d review round(s)", counters.GrantedRounds)
}

// indented writes one labelled block of provider or forge prose under the
// entry's own line, so multi-line evidence never reads as harness output.
func indented(label, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "      " + label + "\n"
	}
	lines := strings.Split(trimmed, "\n")
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      %s: %s\n", label, strings.TrimSpace(lines[0]))
	for _, line := range lines[1:] {
		fmt.Fprintf(&rendered, "        %s\n", strings.TrimRight(line, " \t"))
	}
	return rendered.String()
}

// describeAge states a duration the way somebody reads it rather than the way
// Go prints it: a publication that has been sitting for two days is not
// "48h0m0s" to anybody deciding what to do about it.
func describeAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	if age < time.Minute {
		return strconv.Itoa(int(age.Seconds())) + "s"
	}
	if age < time.Hour {
		return strconv.Itoa(int(age.Minutes())) + "m"
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(age.Hours()), int(age.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(age.Hours())/24, int(age.Hours())%24)
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
