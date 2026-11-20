package readmodel

// What is broken, stalled, blocked, or degraded, as one list.
//
// The observability design (docs/designs/observability-and-dashboard.md, the
// factory problems entry of the read model's queries) asks for one list of
// everything the line is not doing that the goals say it should, where nothing
// in the ordinary flow fixes it by itself. This is that list, derived from
// entries the standing has already built and from nothing a role writes:
// every entry on the needs-a-human line, every factory problem, and every run
// and conversation held on the provider's capacity. Each entry keeps the
// attention entry it came from, so its owner is the ownership registry's
// answer carried on that entry and is decided nowhere here.
//
// The needs-a-human line is the slice of this list whose owner is the
// operator. The list is built from that line, so everything the line shows is
// here; and the standing puts any entry here whose owner is the operator and
// which the line does not carry onto the line (Standing.reconcileOperatorSlice),
// so the two can never show the operator different things.

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ProblemKind is which of the four kinds of problem an entry is, from a closed
// set. It is owned here so no surface redeclares it.
type ProblemKind string

const (
	// ProblemBroken is the harness failing at its own doing: a pass refused or
	// failing over and over, a part the supervisor left down, a record that
	// will not read, the tracker or the forge not answering.
	ProblemBroken ProblemKind = "broken"
	// ProblemStalled is nothing moving that should: a queue nothing pulls, the
	// factory completing nothing, a run that ended owing a step, a merge whose
	// checks cannot be read.
	ProblemStalled ProblemKind = "stalled"
	// ProblemBlocked is a decision somebody owes that is unmade: stopped runs
	// waiting on the development manager, proposed changes waiting on their
	// owner, reports nobody has handled, a step only a person can take.
	ProblemBlocked ProblemKind = "blocked"
	// ProblemDegraded is running but worse: a provider usage limit past its
	// threshold or holding work, the provider answering nobody.
	ProblemDegraded ProblemKind = "degraded"
)

// ProblemKinds is the closed set, in the order the section lists them: broken
// first.
func ProblemKinds() []ProblemKind {
	return []ProblemKind{ProblemBroken, ProblemStalled, ProblemBlocked, ProblemDegraded}
}

// Valid reports whether a token is one of the four kinds.
func (k ProblemKind) Valid() bool { return slices.Contains(ProblemKinds(), k) }

// MoneyNotRecorded is what a problem's money figure says while no record ties
// what was spent to the problem. It is said rather than shown as zero.
const MoneyNotRecorded = "not recorded"

// ProblemCost is what one problem has cost so far, as far as the records hold
// it. Runs and Waiting are counts the entry's own record carries, zero where it
// counts none; StoodSeconds is how long the problem has stood, absent where
// nothing recorded when it began; and Money is MoneyNotRecorded until a record
// prices problems.
type ProblemCost struct {
	Runs         int    `json:"runs"`
	Waiting      int    `json:"waiting"`
	StoodSeconds *int64 `json:"stood_seconds,omitempty"`
	Money        string `json:"money"`
}

// Problem is one entry of the list: its kind, the identity every occurrence of
// it folds onto, the attention entry it came from (whose owner, reason, and
// remedy are the ownership registry's), since when and the latest occurrence
// where the record says, how many it covers, what it has cost, and the open
// work item that cites its source record as the one fixing it, if any.
type Problem struct {
	Kind     ProblemKind `json:"kind"`
	Identity string      `json:"identity"`
	Entry    Attention   `json:"entry"`
	Since    *time.Time  `json:"since,omitempty"`
	Latest   *time.Time  `json:"latest,omitempty"`
	// Count is how many occurrences or items the entry covers: the failures in
	// a row of a failing pass, the items in a group of held work, the refused
	// turns of a conversation, and one where the record counts nothing.
	Count int         `json:"count"`
	Cost  ProblemCost `json:"cost"`
	// FixedBy is the admitted, unfinished work item whose own text cites this
	// problem's source record, or the item the registry names as closing a gap
	// in the harness, and empty where none is admitted yet. FixedBySays is that
	// item as a person reads it, with its priority, labels, and title.
	FixedBy     string `json:"fixed_by,omitempty"`
	FixedBySays string `json:"fixed_by_says,omitempty"`
	// Sources is the identifiers of the records the problem was derived from.
	Sources []string `json:"sources"`
}

// OperatorsMove reports a problem whose owner is the operator.
func (p Problem) OperatorsMove() bool { return p.Entry.Mover.IsOperator() }

// problemKindOf is the kind of problem an attention entry is.
func problemKindOf(a Attention) ProblemKind {
	switch a.Kind {
	case AttentionDegradedService, AttentionFailingTask, AttentionConfigMismatch, AttentionServiceCopies,
		AttentionTrackerUnanswered, AttentionForgeCertificate, AttentionUnrunCheck, AttentionUnreadableRun,
		AttentionUntracedPass:
		return ProblemBroken
	case AttentionStall, AttentionFactoryStall, AttentionOwedStep, AttentionPublication, AttentionKeptHeavyCheck:
		return ProblemStalled
	case AttentionOutage, AttentionCapacityDegraded, AttentionCapacityBlocked:
		return ProblemDegraded
	case AttentionHold:
		if a.CapacityHold != nil {
			return ProblemDegraded
		}
	}
	return ProblemBlocked
}

// greenQueuedMerge reports a merge the forge has queued whose checks read and
// are not failing: the design names it as no problem, so it is left off the
// list unless the operator is its owner, which keeps the operator's slice whole.
func greenQueuedMerge(a Attention) bool {
	var pr *runstate.PullRequest
	switch {
	case a.Kind == AttentionPublication && a.Publication != nil:
		if a.Publication.MergeDrop != nil {
			return false
		}
		pr = a.Publication.PullRequest
	case a.Kind == AttentionOwedStep:
		pr = a.OwedStep.queuedMerge()
	}
	if pr == nil || !pr.MergeQueued || a.Mover.IsOperator() {
		return false
	}
	return pr.Checks == nil || (pr.Checks.ReadError == "" && !pr.Checks.Red())
}

// problemFacts is what an entry's own record says of when the problem began,
// its latest occurrence, how many it covers, the runs spent on it, and what
// waits behind it. A record that says none of these leaves them zero.
func problemFacts(a Attention) (since, latest time.Time, count, runs, waiting int) {
	switch {
	case a.FailingTask != nil:
		return a.FailingTask.FirstAt, a.FailingTask.LatestAt, a.FailingTask.Failures, a.FailingTask.Failures, 0
	case a.UnrunCheck != nil:
		return a.UnrunCheck.Since, a.UnrunCheck.LatestAt, a.UnrunCheck.Changes, a.UnrunCheck.Changes, 0
	case a.TrackerListings != nil:
		return a.TrackerListings.FailingSince, a.TrackerListings.LatestAt, a.TrackerListings.Failures, 0, 0
	case a.ForgeCertificate != nil:
		return a.ForgeCertificate.Since, a.ForgeCertificate.LatestAt, a.ForgeCertificate.Checks, 0, 0
	case a.Outage != nil:
		return a.Outage.Since, a.Outage.LastSeen, a.Outage.Refusals, 0, 0
	case a.OperatorHold != nil:
		return a.OperatorHold.HeldAt, time.Time{}, 0, 0, 0
	case a.IntakeHold != nil:
		return a.IntakeHold.HeldAt, time.Time{}, 0, 0, 0
	case a.CapacityHold != nil:
		return a.CapacityHold.Since, time.Time{}, a.CapacityHold.Refusals, a.CapacityHold.ParkedRuns, 0
	case a.OperatorAction != nil:
		return a.OperatorAction.Since, time.Time{}, 0, 0, 0
	case a.Directive != nil:
		return a.Directive.ReceivedAt, time.Time{}, 0, 0, 0
	case a.Amendment != nil:
		return a.Amendment.RaisedAt, time.Time{}, 0, 0, 0
	case a.AmendmentQueue != nil:
		return a.AmendmentQueue.Oldest, time.Time{}, a.AmendmentQueue.Undecided, 0, a.AmendmentQueue.Undecided
	case a.Reports != nil:
		return a.Reports.Oldest, time.Time{}, a.Reports.Unhandled, 0, a.Reports.Unhandled
	case a.Report != nil:
		return a.Report.RecordedAt, time.Time{}, 0, 0, 0
	case a.Stall != nil:
		return a.Stall.Since, time.Time{}, 0, 0, 0
	case a.FactoryStall != nil:
		return a.FactoryStall.Since, a.FactoryStall.At, len(a.FactoryStall.Failures), 0, 0
	case a.HeldWork != nil:
		var since time.Time
		if a.HeldWork.Since != nil {
			since = *a.HeldWork.Since
		}
		return since, time.Time{}, a.HeldWork.Count, 0, a.HeldWork.Count
	case a.Service != nil:
		return a.Service.DiedAt, time.Time{}, a.Service.Failures, 0, 0
	case a.ServiceCopies != nil:
		return time.Time{}, time.Time{}, a.ServiceCopies.Count(), 0, 0
	case a.UntracedPass != nil:
		return a.UntracedPass.StartedAt, time.Time{}, 0, 0, 0
	case a.DocumentApproval != nil:
		return a.DocumentApproval.WrittenAt, time.Time{}, 0, 0, 0
	case a.LaneHumanBlock != nil:
		return a.LaneHumanBlock.Entry.Since, time.Time{}, 0, 0, 0
	case a.KeptHeavyCheck != nil:
		return a.KeptHeavyCheck.Since(), time.Time{}, 0, 1, 0
	case a.OwedStep != nil:
		return a.OwedStep.EndedAt, time.Time{}, 0, 1, 0
	case a.Publication != nil:
		return a.Publication.EndedAt, time.Time{}, 0, 1, 0
	case a.CapacityBlocked != nil && a.CapacityBlocked.Run != nil:
		return a.CapacityBlocked.Run.Since, time.Time{}, 0, 1, 0
	case a.CapacityBlocked != nil && a.CapacityBlocked.Conversation != nil:
		return a.CapacityBlocked.Conversation.Since, time.Time{}, a.CapacityBlocked.Conversation.Refusals, 0, 0
	}
	return time.Time{}, time.Time{}, 0, 0, 0
}

// problemSources is the identifiers of the records an entry was derived from,
// each once, in a fixed order.
func problemSources(a Attention) []string {
	sources := []string{a.ID, a.WorkItemID}
	if action := a.OperatorAction; action != nil {
		sources = append(sources, action.ReportID, action.RunID)
	}
	if a.OldestProposal != nil {
		sources = append(sources, a.OldestProposal.ID)
	}
	var kept []string
	for _, source := range sources {
		if source = strings.TrimSpace(source); source != "" && !slices.Contains(kept, source) {
			kept = append(kept, source)
		}
	}
	if kept == nil {
		kept = []string{}
	}
	return kept
}

// attentionIdentity is what makes two attention entries the same entry: its
// kind and the record it is about.
func attentionIdentity(a Attention) string {
	return string(a.Kind) + ":" + a.ID + ":" + a.WorkItemID
}

// citableSource reports a source identifier specific enough that an item's text
// naming it is a citation of that record and not a coincidence: a run's,
// report's, amendment's, or conversation's identifier carries a digit and runs
// to some length, where a pass's name or a hold's switch does not.
func citableSource(source string) bool {
	return len(source) >= 8 && strings.ContainsAny(source, "0123456789") && !strings.ContainsAny(source, " \t\n")
}

// fixingItem is the open work item that fixes a problem: the item the
// registry names as closing the gap where the harness cannot carry out a
// role's decision, and otherwise the admitted, unfinished item whose own text
// cites one of the problem's source records, the most urgent first. It never
// names the item the problem is about, which cites itself.
func fixingItem(p Problem, items []beads.WorkItem) string {
	if hand := p.Entry.Ownership.Hand; hand != nil && hand.ClosedBy != "" {
		return hand.ClosedBy
	}
	var cited []string
	for _, source := range p.Sources {
		if citableSource(source) && source != p.Entry.WorkItemID {
			cited = append(cited, source)
		}
	}
	if len(cited) == 0 {
		return ""
	}
	var found *beads.WorkItem
	for index := range items {
		item := &items[index]
		if item.Status == "closed" || item.ID == p.Entry.WorkItemID || slices.Contains(cited, item.ID) {
			continue
		}
		text := strings.Join([]string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n")
		names := false
		for _, source := range cited {
			names = names || strings.Contains(text, source)
		}
		if !names {
			continue
		}
		if found == nil || item.Priority < found.Priority || (item.Priority == found.Priority && item.ID < found.ID) {
			found = item
		}
	}
	if found == nil {
		return ""
	}
	return found.ID
}

// DeriveProblems is the problems list for a standing that has built its
// needs-a-human line, its factory problems, and its capacity-blocked state.
// Items is the tracker listing the standing read, used only to find the item
// fixing each problem; nil finds none.
func DeriveProblems(s Standing, items []beads.WorkItem) []Problem {
	entries := slices.Clone(s.NeedsHuman)
	entries = append(entries, s.FactoryProblems...)
	entries = append(entries, s.CapacityBlocked.Attention()...)
	problems := []Problem{}
	at := map[string]int{}
	for _, entry := range entries {
		if greenQueuedMerge(entry) {
			continue
		}
		kind := problemKindOf(entry)
		identity := string(kind) + ":" + attentionIdentity(entry)
		since, latest, count, runs, waiting := problemFacts(entry)
		if count < 1 {
			count = 1
		}
		if index, seen := at[identity]; seen {
			// The same entry reached from two lines is one problem said once.
			// Only an occurrence the record counts apart adds to its count.
			folded := &problems[index]
			if !sameRecordFacts(folded.Entry, entry) {
				folded.Count += count
				folded.Cost.Runs += runs
				folded.Cost.Waiting += waiting
				folded.Since = earlier(folded.Since, since)
				folded.Latest = later(folded.Latest, latest)
			}
			continue
		}
		problem := Problem{
			Kind:     kind,
			Identity: identity,
			Entry:    entry,
			Since:    problemMoment(since),
			Latest:   problemMoment(latest),
			Count:    count,
			Cost:     ProblemCost{Runs: runs, Waiting: waiting, Money: MoneyNotRecorded},
			Sources:  problemSources(entry),
		}
		at[identity] = len(problems)
		problems = append(problems, problem)
	}
	for index := range problems {
		problem := &problems[index]
		if problem.Since != nil && !s.ObservedAt.IsZero() && s.ObservedAt.After(*problem.Since) {
			stood := int64(s.ObservedAt.Sub(*problem.Since) / time.Second)
			problem.Cost.StoodSeconds = &stood
		}
		if problem.FixedBy = fixingItem(*problem, items); problem.FixedBy != "" {
			problem.FixedBySays = s.Titles.Name(problem.FixedBy)
		}
	}
	order := map[ProblemKind]int{}
	for index, kind := range ProblemKinds() {
		order[kind] = index
	}
	sort.SliceStable(problems, func(i, j int) bool {
		a, b := problems[i], problems[j]
		if order[a.Kind] != order[b.Kind] {
			return order[a.Kind] < order[b.Kind]
		}
		switch {
		case a.Since != nil && b.Since != nil && !a.Since.Equal(*b.Since):
			return a.Since.Before(*b.Since)
		case (a.Since == nil) != (b.Since == nil):
			return a.Since != nil
		}
		return a.Identity < b.Identity
	})
	return problems
}

// sameRecordFacts reports two entries that say the same thing of the same
// record, which is the one entry reached from two lines rather than two
// occurrences.
func sameRecordFacts(a, b Attention) bool { return a.What() == b.What() }

func problemMoment(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}

func earlier(current *time.Time, other time.Time) *time.Time {
	if other.IsZero() || (current != nil && !other.Before(*current)) {
		return current
	}
	return problemMoment(other)
}

func later(current *time.Time, other time.Time) *time.Time {
	if other.IsZero() || (current != nil && !other.After(*current)) {
		return current
	}
	return problemMoment(other)
}

// OperatorsProblems is the problems whose owner is the operator: the slice the
// needs-a-human line shows.
func OperatorsProblems(problems []Problem) []Problem {
	var mine []Problem
	for _, problem := range problems {
		if problem.OperatorsMove() {
			mine = append(mine, problem)
		}
	}
	return mine
}

// SetProblems derives the standing's problems from the entries it already
// carries, names every source the list could not read whole, and puts on the
// needs-a-human line any problem of the operator's it does not carry yet.
// Items is the tracker listing the standing read, or nil.
func (s *Standing) SetProblems(items []beads.WorkItem) {
	s.Problems = DeriveProblems(*s, items)
	s.ProblemsProblem = joinProblems(joinProblems(s.NeedsHumanProblem, s.FactoryProblemsProblem),
		joinProblems(s.CapacityBlocked.RunsProblem, s.CapacityBlocked.ConversationsProblem))
	s.reconcileOperatorSlice()
}

// reconcileOperatorSlice puts on the needs-a-human line every problem whose
// owner is the operator and which the line does not already carry, so the
// line and the problems' operator slice are one set. Every problem's entry
// came from the line or from a list beside it, so this adds only an entry a
// list beside the line resolved to the operator.
func (s *Standing) reconcileOperatorSlice() {
	carried := map[string]bool{}
	for _, entry := range s.NeedsHuman {
		carried[attentionIdentity(entry)] = true
	}
	for _, problem := range s.Problems {
		if identity := attentionIdentity(problem.Entry); problem.OperatorsMove() && !carried[identity] {
			s.NeedsHuman = append(s.NeedsHuman, problem.Entry)
			carried[identity] = true
		}
	}
}
