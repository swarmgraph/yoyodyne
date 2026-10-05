package runstate

// What became of a run is written down carefully and read back by almost
// nobody. A terminal record keeps the status, the phase the run died in, and
// the reason it gave, with the bookkeeping failures in fields of their own so a
// cleanup that could not finish never reads as a run that failed. Until this
// existed, reaching any of it meant the work item's notes one item at a time,
// or the run's own JSON.
//
// So this is the read side of exactly those records, and it decides nothing
// about them: reading a run is not acting on it, so a run a live process owns is
// summarized here as readily as one that ended months ago. What it costs comes
// from the same event logs the ledger prices from, and for the same reason it is
// unknown rather than zero when the evidence is gone.

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// RunOutcome is what became of a run, in the fixed vocabulary every listing
// says it in. It exists because the durable status alone answers a different
// question from the one a listing is read for: "failed" is accurate about the
// attempt and says nothing about the work, so a run whose change is preserved
// and whose item is back with a person printed the same word as one that was
// cancelled and one that broke with nothing to show for it.
//
// The vocabulary is small and closed on purpose. A word per failure message
// would be a vocabulary nobody can learn, and the whole point of these is that a
// reader who has seen them once knows what the next line means without reading
// the reason under it.
type RunOutcome string

const (
	// OutcomeSucceeded is the run whose work landed.
	OutcomeSucceeded RunOutcome = "succeeded"
	// OutcomeStopped is the run that ended on a durable blocker: the item carries
	// it, a person decides what happens next, and nothing about the change was
	// discarded. Every stoppage the harness hands to somebody lands here — an
	// unrepaired review, a check that kept failing, refused paths, a replay the
	// target branch outran, a provider that would not carry the run — because what
	// they have in common is the fact a reader is asking about: the work is still
	// there and it is waiting on a decision. Which of them it was is the reason
	// printed under the run and the phase printed beside this word.
	//
	// A run either role escalated is deliberately not one of these, and the reason
	// is what this word is wired to rather than what it means. The item is back in
	// a person's hands there too, but the surfaces that read this treat a stoppage
	// as a blocker recorded — the operator's line for one says the item is blocked
	// and the harness could not finish it, and an escalated run is neither. It
	// reports as the succeeded run it is, and what it raised is said on the item's
	// notes, in its parking reason, and on the triage docket entry it made.
	OutcomeStopped RunOutcome = "stopped"
	// OutcomeCancelled is the run something stopped rather than judged: the
	// operator, or a killed process. Nothing here is a verdict on the change.
	OutcomeCancelled RunOutcome = "cancelled"
	// OutcomeTimedOut is the run the harness stopped on time, with nothing
	// recorded for anybody to decide.
	OutcomeTimedOut RunOutcome = "timed out"
	// OutcomeFailed is what is left: a run that ended without succeeding and
	// without leaving anybody a blocker to act on. It is the harness itself
	// having failed to carry the run, which is why it keeps the bare word.
	OutcomeFailed RunOutcome = "failed"
)

// Outcome reports what became of this run in that vocabulary. It is derived
// here, in the read model, rather than in each surface, so `yoyo status` and
// `yoyo cost` cannot come to say different words about one run.
//
// It draws only on distinctions the record already holds, so a run recorded long
// before this existed classifies exactly as a run recorded today: a recorded
// blocker is what says the item was handed back, and it outranks the status
// because a run blocked at a deadline is still a stoppage somebody owns. A run
// that has not reached a terminal status keeps its own status word, since
// "pending" and "running" are already the two facts a reader wants there.
func (s State) Outcome() RunOutcome {
	return Ending(s.Status, strings.TrimSpace(s.Blocker) != "")
}

// Ending is the derivation itself, from the two facts that decide it: the status
// the run reached, and whether it left somebody a blocker to act on. It is
// exported because the run's durable record is not the only thing that holds
// those two — the outcome a finished run returns to whatever started it carries
// them as Status and Blocked — and a caller working them out again is a caller
// that can come to a different word than `yoyo status` does for one run.
//
// A run that has not reached a terminal status keeps its own status word, since
// "pending" and "running" are already the two facts a reader wants there.
//
// # The precedence rule
//
// A recorded blocker outranks every terminal status, "succeeded" included. That
// is the one rule here worth stating, because two decisions that are each right
// on their own meet at it, and this is where one of them yields:
//
//   - Reconciliation deliberately leaves a status alone once a record is
//     terminal. A run that wrote down how it ended said something no later
//     sweep saw, and settling it must not overwrite that account.
//   - A blocker on the record means the work item is back in somebody's hands.
//
// Both hold at once on the run that recorded a promotion the target turns out
// not to carry: the sweep puts the item back in a person's hands and clears the
// promotion, and the status stays the "succeeded" the run wrote for itself. A
// derivation reading the status first prints "succeeded" over a run a person now
// owns, which is the one word that is false about it. So the status yields to the
// blocker here, in the derivation, and the durable record is not moved: the
// sweep's rule keeps everything it was for, and the surfaces say the true word.
// Reconciler.saveTerminalFailure cites this from the other side.
func Ending(status Status, handedToAPerson bool) RunOutcome {
	if !status.Terminal() {
		return RunOutcome(status)
	}
	switch {
	case handedToAPerson:
		return OutcomeStopped
	case status == StatusSucceeded:
		return OutcomeSucceeded
	case status == StatusCancelled:
		return OutcomeCancelled
	case status == StatusTimedOut:
		return OutcomeTimedOut
	default:
		return OutcomeFailed
	}
}

// NoReasonSays is what every surface says as the reason of a run that ended
// without succeeding and whose record gives none. It is one phrase here rather
// than one per surface for the reason ResumingIntegrationSays is: the channel
// line and `yoyo status` must not say different words about one run. It states
// the absence as itself, because a run can honestly end with no reason — a
// cancellation owes nobody a sentence — and a surface that printed nothing
// instead reads as a run whose reason the operator had already seen.
const NoReasonSays = "the record names no reason"

// Reason is why the run stopped, with the class that stopped it as the first
// word, exactly as RunSummary.Reason says it: the channel line and `yoyo status`
// must not give two answers about one run.
func (s State) Reason() string {
	return stopReason(s.Status, s.Outcome(), s.StopClass, s.Failure, s.Blocker,
		s.PublishFailure, s.CleanupFailure, s.CompletionRecordingFailure)
}

// stopReason is the one derivation behind State.Reason and RunSummary.Reason.
//
// The words are the run's own failure first, then the account of whatever its
// class names that a succeeded run stopped short of, and then the blocker: a
// stoppage settled onto a record some killed process had already left terminal
// can carry the words the item was blocked in and no failure, and saying the
// record names no reason over it would be false. Only where the record gives
// none of those, names no class, and carries no outstanding publication — which
// each surface says under its own label — is the absence said, and only of a
// run that ended without succeeding.
func stopReason(status Status, outcome RunOutcome, class StopClass, failure, blocker, publish, cleanup, recording string) string {
	words := strings.TrimSpace(failure)
	if words == "" {
		switch class {
		case StopPublish:
			words = strings.TrimSpace(publish)
		case StopCleanup:
			words = strings.TrimSpace(cleanup)
		case StopRecording:
			words = strings.TrimSpace(recording)
		}
	}
	if words == "" {
		words = strings.TrimSpace(blocker)
	}
	if words == "" && (class == "" || class == StopUnknown) && endedBadly(status, outcome) && strings.TrimSpace(publish) == "" {
		words = NoReasonSays
	}
	return StopReason(class, words)
}

// Artifacts is what a run's record says survives of its change: the branch and
// the worktree it made, and the harness's own record of which of them it
// removed. It is a type rather than four fields read in place because the
// question a reader asks of a run that did not succeed is "is my work gone", and
// an answer assembled separately by each surface is an answer two surfaces can
// give differently.
type Artifacts struct {
	Branch          string
	BranchRemoved   bool
	WorktreePath    string
	WorktreeRemoved bool
}

// Preserved reports the run's change surviving the run: a branch or a worktree
// the harness has not recorded as removed. It asks about both because either one
// on its own still holds the work — a removed worktree whose branch stands is
// work somebody can still check out.
//
// It is false for a run whose record names neither, which is not the same claim
// as work thrown away and must not be rendered as one. Describe below is what
// keeps those apart, and is what a surface says rather than this.
func (a Artifacts) Preserved() bool {
	return (a.Branch != "" && !a.BranchRemoved) || (a.WorktreePath != "" && !a.WorktreeRemoved)
}

// Describe is what remains of a run's change, in the three fixed phrases every
// surface says it in. Two of them are facts the record holds; the third says the
// record holds neither, rather than turning two empty fields into a claim that
// the run made nothing — which is exactly the false reassurance the outcome
// vocabulary exists to stop.
//
// It is derived here, in the read model, rather than in each surface, for the
// reason Outcome is: `yoyo status`, `yoyo cost`, and the channel must not be
// able to say different words about one run's remains.
func (a Artifacts) Describe() string {
	switch {
	case a.Preserved():
		return "work preserved"
	case a.Branch != "" || a.WorktreePath != "":
		// Something was made and the harness recorded removing it. That is a
		// different answer from never having made anything, and an operator asking
		// whether their work is gone is owed the one that is true.
		return "work removed"
	default:
		return "no artifacts recorded"
	}
}

// Artifacts is what this run's durable record says survives of its change.
func (s State) Artifacts() Artifacts {
	return Artifacts{
		Branch:          s.Branch,
		BranchRemoved:   s.BranchRemoved,
		WorktreePath:    s.WorktreePath,
		WorktreeRemoved: s.WorktreeRemoved,
	}
}

// RunSummary is what one recorded run says about itself: how it ended, where it
// was when it did, what it gave as the reason, and what it spent getting there.
type RunSummary struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Status     Status `json:"status"`
	// Outcome is what became of the run in the fixed vocabulary above. It is
	// carried beside the status rather than instead of it: the status is the
	// durable value and stays what it always was, and this is the reading of it a
	// listing shows.
	Outcome     RunOutcome `json:"outcome"`
	Phase       Phase      `json:"phase,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// The artifacts the run leaves behind, and the harness's own record of which
	// of them it removed. They are here because "is my work gone" is the question
	// a listing of runs that did not succeed is actually read for, and a listing
	// that named neither the branch nor the worktree left it to be answered by
	// going and looking.
	Branch          string `json:"branch,omitempty"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	BranchRemoved   bool   `json:"branch_removed,omitempty"`
	WorktreeRemoved bool   `json:"worktree_removed,omitempty"`
	// Found is what the repository held of the branch and the worktree when the
	// listing was made, where the listing looked. It is what a listing says about
	// preservation in place of the removal flags above, which are what a cleanup
	// remembered to write rather than what is there.
	Found *triage.Found `json:"found,omitempty"`
	// ProviderSessionID is the developer session preserved with the change, which
	// is what a continuation resumes in rather than deriving the work a second
	// time. It is named for the same reason the artifacts are.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// ReviewFindings is how many findings the last review of this run recorded.
	// It says there is something written down to read rather than carrying the
	// findings themselves, which are the docket's to render in full.
	ReviewFindings int `json:"review_findings,omitempty"`
	// Integrated reports a run that promoted its work, which is what separates
	// the attempt that finished a piece of work from the ones that did not.
	Integrated bool `json:"integrated,omitempty"`
	// Outstanding reports a run that still owes somebody a step: one a process
	// was killed part-way through, or a finished one whose cleanup or queued
	// merge is unsettled. It is what `yoyo reconcile` acts on, and it is here
	// because a run that owes something and a run that is over look identical
	// from the status alone.
	Outstanding bool `json:"outstanding,omitempty"`
	// MergeQueued reports a merge the forge accepted but has not performed. It
	// is one of exactly two things a finished run can owe -- the other is the
	// cleanup that an integrated run short of the complete phase still owes --
	// so carrying it is what lets a reader say which of the two a run marked
	// outstanding is waiting on, rather than only that it is waiting.
	MergeQueued bool `json:"merge_queued,omitempty"`
	// ResumingIntegration reports a run at its promotion again after the
	// environment stopped it there, with its approval standing. It is carried so
	// a listing says ResumingIntegrationSays in place of the bare phase: a run
	// "integrating" after a stop and a run integrating for the first time are the
	// same phase and different facts, and the difference is what an operator who
	// signed overrides for the first kind is reading the listing for.
	ResumingIntegration bool `json:"resuming_integration,omitempty"`
	// IntegrationStop is the environment having stopped this run's approved
	// change short of its promotion, when that is what stopped it. It is on the
	// summary because it changes what the reason under the run means: the reason
	// says what failed, and this says it was not the work and names the one verb
	// that resumes it at no cost.
	IntegrationStop *IntegrationStop `json:"integration_stop,omitempty"`
	// Failure is the run's own reason for ending, and it is the only one of the
	// four recorded reasons that is about the work itself. The three below
	// happened around the work rather than to it, and are kept apart here for the
	// reason they are kept apart in the record: a publication or a cleanup that
	// could not finish must never be read as a failed piece of work.
	//
	// It is the reason and never the verdict, exactly as State.Failure is: what
	// became of the run is Outcome above, and a reader deciding from this field
	// that a run failed or stopped is a second classification the listing it
	// prints beside will contradict.
	Failure string `json:"failure,omitempty"`
	// StopClass is which gate stopped the run, exactly as State.StopClass records
	// it. Reason is how a surface prints it, as the first word of the reason.
	StopClass StopClass `json:"stop_class,omitempty"`
	// Blocker is the words the run's work item was handed back in, where it was.
	// It is carried so the reason a surface prints can fall back to it on a run
	// that recorded a blocker and no failure, which is what Reason reads.
	// What became of the run is still Outcome's to say.
	Blocker string `json:"blocker,omitempty"`
	// FailingCheck is the deterministic check that was still failing when the
	// record was last written. It is what a repair attempt was handed, so on a
	// failed run it is usually the thing behind the reason rather than a second
	// reason.
	FailingCheck *CheckFailure `json:"failing_check,omitempty"`
	// RefusedPaths is the protected-path refusal that was still standing when the
	// record was last written. It sits beside the failing check because it is the
	// same kind of fact — what a repair attempt was handed, and usually the thing
	// behind the reason rather than a second reason — and because it is the one
	// the reader cannot go and reproduce: the worktree it describes is gone.
	RefusedPaths *PathRefusal `json:"refused_paths,omitempty"`
	// CheckStage is the current attempt's check stage as the record last saw
	// it: its bound, what it has spent, and which check it is on. It is here so
	// a surface reading a running run can say how much of the bound is gone,
	// and so a stage the bound stopped is read afterwards as that rather than as
	// a check that failed.
	CheckStage *CheckStage `json:"check_stage,omitempty"`
	// LandingChecks is what the landing checks made of the commit this run
	// integrated. It says nothing about the run — the run succeeded and its
	// item closed — and it is here because a red landing is the one fact about
	// a landed change that its run's ending does not carry.
	LandingChecks *LandingChecks `json:"landing_checks,omitempty"`
	// ContextTruncation is what the work item's notes lost to the context budget
	// when this run's context was assembled. It says nothing about the work — a
	// run whose item was truncated delivered exactly as it would have — and it is
	// here because it is otherwise legible only inside the text an agent was
	// handed: an item's notes only ever grow, so the loss arrives without anybody
	// deciding it and stays until somebody sees it.
	ContextTruncation *ContextTruncation `json:"context_truncation,omitempty"`
	// StaleBlockClear is what became of the stale blocked status the claim
	// cleared on its way to the item, where it met one. It is here because the
	// ending that matters is the one the run's failure is otherwise the only
	// word on: a clear no read confirmed is a run dead at the claim with the
	// item left for the next pull, and this says so where the run is read.
	StaleBlockClear *StaleBlockClear `json:"stale_block_clear,omitempty"`
	// ReportProblem and AmendmentProblem are what the run's agents reported or
	// proposed that the harness could not read or could not keep. Neither says
	// anything about the work — the run delivered exactly as it would have — and
	// they are here because the outcome that used to be their only home is
	// printed once and gone: without them a refused proposal reads afterwards as
	// a proposal never made.
	SchedulingWaitClearFailure string `json:"scheduling_wait_clear_failure,omitempty"`
	ReportProblem              string `json:"report_problem,omitempty"`
	AmendmentProblem           string `json:"amendment_problem,omitempty"`
	// Amendments is every proposal the run's agents made, raised or dropped as a
	// restatement of one already raised. A drop is on no other record, so this is
	// where a fold the comparison got wrong is found.
	Amendments     []RunAmendment `json:"amendments,omitempty"`
	PublishFailure string         `json:"publish_failure,omitempty"`
	CleanupFailure string         `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure is on the summary for the reason it is on the
	// state: the run record is the one durable home this failure class has.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
	// WorkflowInstanceID and WorkflowDivergence are what the delivery definition
	// observed of this run: which instance watched it, and why the watching
	// stopped. Neither says anything about the work — a run that diverged
	// delivered exactly as it would have — and they are here because the
	// definition is what a run executes by default, so where it sent a run is a
	// fact about that run. One nobody can read without opening the state
	// directory is one nobody reads.
	//
	// WorkflowUnobserved is the third of them and is why there was no watching at
	// all: a run whose project asked to be observed and whose instance could not
	// be created. It is on the summary for the same reason the other two are, and
	// with more of it: an unobserved run is the one that reads as a clean run to
	// anything counting divergences, so it is exactly the run that has to be
	// visible without opening the state directory.
	WorkflowInstanceID string `json:"workflow_instance_id,omitempty"`
	WorkflowDivergence string `json:"workflow_divergence,omitempty"`
	WorkflowUnobserved string `json:"workflow_unobserved,omitempty"`
	// Selection is why the harness was running this item: who chose it and on
	// what grounds. It matters most for the runs nobody typed an identifier for,
	// which is why its absence is worth reporting rather than omitting — a run
	// nothing accounts for is indistinguishable from work happening behind the
	// operator's back, and a missing line reads as neither.
	Selection *Selection `json:"selection,omitempty"`
	// AccountAlias and ConfigRevision are which provider account the run spent
	// and which configuration set it up. They are here for the reason the
	// selection is: a run nobody can attribute to an account or to a
	// configuration is one whose cost and whose behaviour cannot be explained
	// afterwards, and there is one of each today only until there is not.
	AccountAlias   string `json:"account_alias,omitempty"`
	ConfigRevision string `json:"config_revision,omitempty"`
	// Build is the harness revision that reserved the run. It is here for a
	// sharper version of the same reason: a run whose behaviour cannot be
	// attributed to a binary is one whose defects cannot be told apart from a
	// deployment that never happened, which is how a week of code defects turns
	// out to have been one stale process.
	Build string `json:"build,omitempty"`
	// CostUSD is what the provider reported for every invocation in this run's
	// log, and UnknownCost says why there is no figure rather than reporting one
	// of zero: a run whose evidence is gone did not cost nothing.
	CostUSD     float64 `json:"cost_usd"`
	Invocations int     `json:"invocations,omitempty"`
	UnknownCost string  `json:"unknown_cost,omitempty"`
}

// Failed reports a run that reached a terminal status without succeeding.
// Cancelled and timed out belong here beside failed: what they have in common is
// that the work did not land, which is what somebody looking for the runs that
// went wrong is asking about, and each of them records why it ended.
func (r RunSummary) Failed() bool { return endedBadly(r.Status, r.Outcome) }

// Reason is why the run stopped, with the class that stopped it as the first
// word. The words are the run's own failure where it recorded one; otherwise the
// account of whatever its class names that a succeeded run stopped short of —
// the publication, the cleanup, or the completion record — so a run whose only
// account is one of those still says why it stopped; and otherwise the blocker
// its item was handed back in. A run that ended without succeeding and gives
// none of those says NoReasonSays; a run still going, or one that succeeded
// with nothing outstanding, gives nothing.
func (r RunSummary) Reason() string {
	return stopReason(r.Status, r.Outcome, r.StopClass, r.Failure, r.Blocker,
		r.PublishFailure, r.CleanupFailure, r.CompletionRecordingFailure)
}

// Artifacts is what this summary says survives of the run's change.
func (r RunSummary) Artifacts() Artifacts {
	return Artifacts{
		Branch:          r.Branch,
		BranchRemoved:   r.BranchRemoved,
		WorktreePath:    r.WorktreePath,
		WorktreeRemoved: r.WorktreeRemoved,
	}
}

// Preserved reports the run's change surviving the run: what the repository
// held where the listing looked, and the artifacts' own answer where it did not.
func (r RunSummary) Preserved() bool {
	if r.Found != nil {
		return r.Found.Holds()
	}
	return r.Artifacts().Preserved()
}

// DescribeRemains is what remains of the run's change in the words a listing
// says it in. Where the repository was asked it says so, and what it found;
// where it was not, it says the record's answer and that nothing looked, so a
// flag is never read out as a check.
func (r RunSummary) DescribeRemains() string {
	if r.Found != nil {
		return r.Found.DescribeRemains()
	}
	return r.Artifacts().Describe()
}

// CostKnown reports a run the recorded evidence could actually price.
func (r RunSummary) CostKnown() bool { return r.UnknownCost == "" }

// endedBadly reports a terminal run whose work did not land. It asks the derived
// outcome rather than the status alone, because the status alone cannot answer
// it: by the precedence rule on Ending, a stoppage can be settled onto a record
// that still says "succeeded", and a selection reading the status would leave
// that run out of the one listing it most needs to be in.
func endedBadly(status Status, outcome RunOutcome) bool {
	return status.Terminal() && outcome != OutcomeSucceeded
}

// RunQuery narrows which recorded runs a history covers.
type RunQuery struct {
	// WorkItemID keeps only the runs made for one work item, when it is set.
	WorkItemID string
	// FailedOnly keeps only the runs that ended without succeeding.
	FailedOnly bool
	// Limit is how many of the selected runs to report, newest first. Zero
	// reports all of them.
	Limit int
}

// RunHistory is the runs a query selected, and enough about what it selected
// them from that a limited listing never reads as the whole record.
type RunHistory struct {
	Runs []RunSummary `json:"runs"`
	// Matched is how many runs the query selected before the limit cut it down
	// to the runs above.
	Matched int `json:"matched"`
	// Recorded is how many runs the harness holds for this product at all, which
	// is what tells "nothing failed" from "nothing ran".
	Recorded int `json:"recorded"`
}

// History reports what became of the runs a query selects, newest first.
// Reading the record decides nothing about the runs, so a run another process is
// executing is summarized here exactly as a finished one is, with whatever its
// record and log hold so far.
func (s *Store) History(query RunQuery) (RunHistory, error) {
	states, err := s.scan("recorded", func(State) bool { return true })
	if err != nil {
		return RunHistory{}, err
	}
	history := RunHistory{Recorded: len(states), Runs: []RunSummary{}}
	workItemID := strings.TrimSpace(query.WorkItemID)
	matched := make([]State, 0, len(states))
	for _, state := range states {
		if workItemID != "" && state.WorkItemID != workItemID {
			continue
		}
		if query.FailedOnly && !endedBadly(state.Status, state.Outcome()) {
			continue
		}
		matched = append(matched, state)
	}
	// Newest first, unlike the price breakdown, which reads oldest first because
	// it is a history of one item. This is asked when something has gone wrong,
	// and what went wrong most recently is what is being asked about.
	sort.SliceStable(matched, func(first, second int) bool {
		if !matched[first].StartedAt.Equal(matched[second].StartedAt) {
			return matched[first].StartedAt.After(matched[second].StartedAt)
		}
		return matched[first].RunID < matched[second].RunID
	})
	history.Matched = len(matched)
	if query.Limit > 0 && len(matched) > query.Limit {
		matched = matched[:query.Limit]
	}
	// Only the runs actually reported are priced, because pricing one reads its
	// whole event log: a listing of the twenty most recent runs must not cost a
	// scan of every log the product has ever written. A limit of zero is the one
	// exception, and it is the caller's to ask for: it reports every run, so it
	// prices every run, and `yoyo status --limit 0` reads every event log there is.
	for _, state := range matched {
		history.Runs = append(history.Runs, s.summarize(state))
	}
	return history, nil
}

func (s *Store) summarize(state State) RunSummary {
	summary := RunSummary{
		RunID:                      state.RunID,
		WorkItemID:                 state.WorkItemID,
		Status:                     state.Status,
		Outcome:                    state.Outcome(),
		Phase:                      state.Phase,
		StartedAt:                  state.StartedAt,
		CompletedAt:                state.CompletedAt,
		Branch:                     state.Branch,
		WorktreePath:               state.WorktreePath,
		BranchRemoved:              state.BranchRemoved,
		WorktreeRemoved:            state.WorktreeRemoved,
		ProviderSessionID:          state.ProviderSessionID,
		ReviewFindings:             state.ReviewFindings,
		Integrated:                 state.Integration != nil,
		Outstanding:                state.Outstanding(),
		MergeQueued:                state.PullRequest != nil && state.PullRequest.MergeQueued,
		ResumingIntegration:        state.ResumingIntegration(),
		AccountAlias:               state.AccountAlias,
		ConfigRevision:             state.ConfigRevision,
		Build:                      state.Build,
		Failure:                    state.Failure,
		StopClass:                  state.RecordedStopClass(),
		Blocker:                    state.Blocker,
		SchedulingWaitClearFailure: state.SchedulingWaitClearFailure,
		ReportProblem:              state.ReportProblem,
		AmendmentProblem:           state.AmendmentProblem,
		Amendments:                 slices.Clone(state.Amendments),
		PublishFailure:             state.PublishFailure,
		CleanupFailure:             state.CleanupFailure,

		WorkflowInstanceID: state.WorkflowInstanceID,
		WorkflowDivergence: state.WorkflowDivergence,
		WorkflowUnobserved: state.WorkflowUnobserved,

		CompletionRecordingFailure: state.CompletionRecordingFailure,
	}
	if state.CheckFailure != nil {
		failing := *state.CheckFailure
		summary.FailingCheck = &failing
	}
	if state.PathRefusal != nil {
		refused := *state.PathRefusal
		summary.RefusedPaths = &refused
	}
	if state.CheckStage != nil {
		stage := *state.CheckStage
		summary.CheckStage = &stage
	}
	if state.LandingChecks != nil {
		landed := *state.LandingChecks
		summary.LandingChecks = &landed
	}
	if state.IntegrationStop != nil {
		stopped := *state.IntegrationStop
		summary.IntegrationStop = &stopped
	}
	if state.ContextTruncation != nil {
		truncated := *state.ContextTruncation
		summary.ContextTruncation = &truncated
	}
	if state.StaleBlockClear != nil {
		cleared := *state.StaleBlockClear
		summary.StaleBlockClear = &cleared
	}
	if state.Selection != nil {
		selection := *state.Selection
		summary.Selection = &selection
	}
	price := s.priceRun(state)
	summary.CostUSD = price.CostUSD
	summary.Invocations = price.Invocations
	summary.UnknownCost = price.Unknown
	return summary
}
