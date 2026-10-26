package readmodel

// The owner summary: where things stand, in five answers a person can read
// without knowing how the harness works.
//
// The four lines, the throughput, and the spend each answer part of what the
// owner asks, in the harness's own terms: run records, stop causes, and the
// entries of the attention line. Reading them to find out where things stand
// meant reading the failure chains behind them. This puts five answers ahead of
// all of it, on the dashboard and as the opening of `yoyo status`:
//
//  1. what landed since a chosen time, code changes apart from documents
//     published;
//  2. what is in progress, each with the newest result about its change;
//  3. what is blocked, each with whose move it is and what happens next, and
//     the runs that ended without landing, one line per cause;
//  4. what needs the owner's own decision, and only steps only a person can
//     take;
//  5. what it cost.
//
// It derives nothing the three readings do not already say. Who owns an entry
// is the ownership registry's answer the attention line carries, what landed is
// the throughput's landed runs, and the cost is the spend reading's figure, so
// the summary and the sections under it cannot disagree. A stop cause is said
// in the words runstate.StopSays owns for it, as every surface says one. What it adds is
// wording and grouping: an entry that recurs — the same kind of thing, owned by
// the same mover, ended by the same step — is one line with its count and its
// latest time, and a run identifier, a stop cause's code, or a configuration
// key is never in a line. Those are kept on each entry as its detail, which the
// dashboard opens on a click and `yoyo status --detail` prints.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// SummaryPeriod is the time the summary counts what landed and what it cost
// from: today since local midnight, or the last seven local days with today
// the first of them. They are the throughput's and the spend's own windows of
// local days, so a figure in the summary is a figure the sections under it
// show.
type SummaryPeriod string

const (
	SummaryToday SummaryPeriod = "today"
	SummaryWeek  SummaryPeriod = "week"
)

// SummaryPeriods is every period the summary can be asked for, in the order a
// surface offers them.
func SummaryPeriods() []SummaryPeriod { return []SummaryPeriod{SummaryToday, SummaryWeek} }

// ParseSummaryPeriod reads a period as a person types it. Empty is today.
func ParseSummaryPeriod(text string) (SummaryPeriod, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "", "today":
		return SummaryToday, nil
	case "week", "7d", "7 days", "last 7 days":
		return SummaryWeek, nil
	}
	return "", fmt.Errorf("%q is not a time the summary counts from; say today or week", text)
}

// days is how many local days the period covers, today counting as the first.
func (p SummaryPeriod) days() int {
	if p == SummaryWeek {
		return 7
	}
	return 1
}

// OwnerSummary is the five answers. Every answer is present: one with nothing
// in it says so in words, and one whose source could not be read says that
// rather than nothing.
type OwnerSummary struct {
	ObservedAt time.Time     `json:"observed_at"`
	Period     SummaryPeriod `json:"period"`
	// Since is the first moment the period covers, and SinceSays the period as
	// the summary's heading says it, in the operator's local zone with the zone
	// named.
	Since     time.Time `json:"since"`
	SinceSays string    `json:"since_says"`

	// Landed is the code changes that reached the target branch in the period,
	// and Published the confirmed documents that did. They are apart because a
	// document published is the owning role's words landing, and a code change
	// is the product changing.
	Landed    SummaryAnswer `json:"landed"`
	Published SummaryAnswer `json:"published"`
	// Moving is each run in progress with the newest result about its change.
	Moving SummaryAnswer `json:"moving"`
	// Blocked is what waits on somebody other than the owner, each with whose
	// move it is and the next step, and Failures the runs that ended in the
	// period without landing, one line per cause with its count and latest time.
	Blocked  SummaryAnswer `json:"blocked"`
	Failures SummaryAnswer `json:"failures"`
	// Decisions is what only the owner can do: the entries the ownership
	// registry gives him for a reason on its closed list, and the decisions a
	// role made that need his hand to carry out.
	Decisions SummaryAnswer `json:"decisions"`
	// Cost is what the period cost, as the spend reading prices it.
	Cost SummaryAnswer `json:"cost"`
	// Quiet says, in one sentence, that every answer is nothing: nothing
	// landed, moving, blocked, or ended without landing, nothing needs the
	// owner, and nothing was spent. It is empty whenever any answer has
	// something in it or could not be read, and a surface with room for one
	// sentence shows it in place of the five.
	Quiet string `json:"quiet,omitempty"`

	wording *TextTerms
}

// SummaryAnswer is one answer: its heading, the answer in one sentence, and the
// lines under it.
type SummaryAnswer struct {
	Heading string         `json:"heading"`
	Says    string         `json:"says"`
	Entries []SummaryEntry `json:"entries"`
	// Unread says the answer's source could not be read, and Detail why, in the
	// source's own words.
	Unread bool     `json:"unread,omitempty"`
	Detail []string `json:"detail,omitempty"`
}

// SummaryEntry is one line of an answer. Says is the line in ordinary words;
// WaitingOn and Next are who has to act and what happens next where the answer
// carries them; Count is how many occurrences the line stands for, and Latest
// the newest of them. Detail is everything technical behind the line — run
// identifiers, stop causes, commands — which a surface shows only when asked.
type SummaryEntry struct {
	// Line is the whole line as every surface prints it: Says, the time, the
	// next step, and who it waits on, worded once here.
	Line       string    `json:"line"`
	Says       string    `json:"says"`
	WorkItemID string    `json:"work_item_id,omitempty"`
	WaitingOn  string    `json:"waiting_on,omitempty"`
	Next       string    `json:"next,omitempty"`
	Count      int       `json:"count"`
	Latest     time.Time `json:"latest,omitzero"`
	LatestSays string    `json:"latest_says,omitempty"`
	Detail     []string  `json:"detail,omitempty"`
}

// SummaryInputs are the three readings the summary is made from, each with
// what stopped it being read where it could not be. A nil reading with no
// problem is one nothing was wired to read.
type SummaryInputs struct {
	Standing          *Standing
	StandingProblem   string
	Throughput        *Throughput
	ThroughputProblem string
	Spend             *Spend
	SpendProblem      string
	Period            SummaryPeriod
	// Now anchors the period; it defaults to the standing's observation time,
	// and to the wall clock where there is none.
	Now time.Time
}

// Summarize makes the five answers from the three readings. It reads nothing
// itself and never fails: an answer whose reading is missing says so.
func Summarize(in SummaryInputs) OwnerSummary {
	now := in.Now
	if now.IsZero() && in.Standing != nil {
		now = in.Standing.ObservedAt
	}
	if now.IsZero() {
		now = time.Now()
	}
	period := in.Period
	if period != SummaryWeek {
		period = SummaryToday
	}
	since := startOfLocalDay(now, period.days())
	summary := OwnerSummary{
		ObservedAt: now,
		Period:     period,
		Since:      since,
		SinceSays:  periodSays(period, since),
	}
	if in.Standing != nil {
		summary.wording = in.Standing.wording
	}
	names := summaryNames{standing: in.Standing, throughput: in.Throughput}

	summary.Landed, summary.Published, summary.Failures = summarizeEndings(in, period, now, names)
	summary.Moving = summarizeMoving(in, now, names)
	summary.Blocked, summary.Decisions = summarizeWaiting(in, now, names)
	summary.Cost = summarizeCost(in, period, now)
	if summary.nothing() {
		summary.Quiet = "Nothing landed, nothing is in progress or blocked, no run ended without landing, nothing needs you, and nothing was spent, " + summary.SinceSays + "."
	}
	for _, answer := range summary.answers() {
		for index := range answer.Entries {
			answer.Entries[index].Line = answer.Entries[index].line()
		}
	}
	return summary
}

// nothing reports every answer read and empty, and nothing spent.
func (s *OwnerSummary) nothing() bool {
	for _, answer := range s.answers() {
		if answer.Unread || len(answer.Entries) > 0 {
			return false
		}
	}
	return s.Cost.Says == "$0.00 across "+count(0, "provider call")
}

// answers is every answer in the order the summary prints them.
func (s *OwnerSummary) answers() []*SummaryAnswer {
	return []*SummaryAnswer{&s.Landed, &s.Published, &s.Moving, &s.Blocked, &s.Failures, &s.Decisions, &s.Cost}
}

// periodSays is the period as the heading says it.
func periodSays(period SummaryPeriod, since time.Time) string {
	if period == SummaryWeek {
		return "over the last 7 days, since " + since.Format("Monday January 2") + " at " + since.Format("15:04 MST")
	}
	return "today, since " + since.Format("15:04 MST") + " on " + since.Format("Monday January 2")
}

// summaryMoment is a moment as a summary line says it: the minute where it is
// today, and the day as well where it is not, always in the operator's local
// zone with the zone named.
func summaryMoment(moment, now time.Time) string {
	if moment.IsZero() {
		return ""
	}
	local, today := moment.Local(), now.Local()
	if local.Year() == today.Year() && local.YearDay() == today.YearDay() {
		return local.Format("15:04 MST")
	}
	return local.Format("Mon Jan 2 15:04 MST")
}

// summaryNames names a work item as the readings name it: the standing's one
// tracker listing first, the throughput's names next, and the title a run
// recorded at its claim where neither holds it.
type summaryNames struct {
	standing   *Standing
	throughput *Throughput
}

func (n summaryNames) name(id, recorded string) string {
	id = strings.TrimSpace(id)
	if n.standing != nil && n.standing.Titles != nil {
		if _, known := n.standing.Titles.Title(id); known && n.standing.Titles.Readable() {
			return n.standing.Titles.Name(id)
		}
	}
	if n.throughput != nil {
		if named, ok := n.throughput.WorkItemNames[id]; ok && named != "" {
			return named
		}
	}
	if n.standing != nil {
		if named, ok := n.standing.WorkItemNames[id]; ok && named != "" {
			return named
		}
	}
	if title := strings.TrimSpace(recorded); title != "" {
		return title + " (" + id + ")"
	}
	// Nothing holds a title: the reading's tracker listing still says why,
	// beside the id — not in the tracker, or the tracker unreadable — as every
	// list the harness builds does.
	if n.standing != nil && n.standing.Titles != nil {
		return n.standing.Titles.Name(id)
	}
	return id
}

// unreadAnswer is an answer whose reading could not be made.
func unreadAnswer(heading, problem string) SummaryAnswer {
	if strings.TrimSpace(problem) == "" {
		problem = "nothing was wired to read it"
	}
	return SummaryAnswer{Heading: heading, Says: "could not be read just now", Entries: []SummaryEntry{}, Unread: true, Detail: []string{problem}}
}

// windowFor is the throughput's window covering the period.
func windowFor(throughput *Throughput, period SummaryPeriod) (Window, bool) {
	for _, window := range throughput.Windows {
		if window.Days == period.days() {
			return window, true
		}
	}
	return Window{}, false
}

// summarizeEndings is the landed code changes, the documents published, and
// the runs that ended without landing, from the throughput's window for the
// period.
func summarizeEndings(in SummaryInputs, period SummaryPeriod, now time.Time, names summaryNames) (SummaryAnswer, SummaryAnswer, SummaryAnswer) {
	const (
		landedHeading    = "Landed"
		publishedHeading = "Documents published"
		failuresHeading  = "Ended without landing"
	)
	problem := in.ThroughputProblem
	if problem == "" && in.Throughput != nil {
		problem = in.Throughput.RunsProblem
	}
	if in.Throughput == nil || problem != "" {
		return unreadAnswer(landedHeading, problem), unreadAnswer(publishedHeading, problem), unreadAnswer(failuresHeading, problem)
	}
	window, ok := windowFor(in.Throughput, period)
	if !ok {
		missing := fmt.Sprintf("the throughput carries no window of %d days", period.days())
		return unreadAnswer(landedHeading, missing), unreadAnswer(publishedHeading, missing), unreadAnswer(failuresHeading, missing)
	}
	landed := SummaryAnswer{Heading: landedHeading, Entries: []SummaryEntry{}}
	published := SummaryAnswer{Heading: publishedHeading, Entries: []SummaryEntry{}}
	for _, run := range window.LandedItems {
		entry := SummaryEntry{Count: 1, Latest: run.LandedAt, LatestSays: summaryMoment(run.LandedAt, now), Detail: []string{"run " + run.RunID}}
		if run.Document != "" {
			entry.Says = run.Document
			published.Entries = append(published.Entries, entry)
			continue
		}
		entry.WorkItemID = run.WorkItemID
		entry.Says = names.name(run.WorkItemID, run.Title)
		landed.Entries = append(landed.Entries, entry)
	}
	landed.Says = count(len(landed.Entries), "code change")
	published.Says = count(len(published.Entries), "document")

	failures := SummaryAnswer{Heading: failuresHeading, Entries: []SummaryEntry{}}
	grouped := map[string]*SummaryEntry{}
	var order []string
	for class, number := range window.StopsByCause {
		if number == 0 {
			continue
		}
		cause := runstate.StopSays(class)
		entry, seen := grouped[cause]
		if !seen {
			entry = &SummaryEntry{}
			grouped[cause] = entry
			order = append(order, cause)
		}
		entry.Count += number
		if latest := window.LatestStopByCause[class]; latest.After(entry.Latest) {
			entry.Latest = latest
		}
		entry.Detail = append(entry.Detail, fmt.Sprintf("recorded stop cause %s: %d", class, number))
	}
	total := 0
	for _, cause := range order {
		entry := grouped[cause]
		sort.Strings(entry.Detail)
		entry.Says = count(entry.Count, "run") + ": " + cause
		entry.LatestSays = summaryMoment(entry.Latest, now)
		total += entry.Count
		failures.Entries = append(failures.Entries, *entry)
	}
	sortEntries(failures.Entries)
	failures.Says = count(total, "run")
	return landed, published, failures
}

// sortEntries puts the most occurrences first, then the newest, then the
// line's words, so two readings of one state list it in one order.
func sortEntries(entries []SummaryEntry) {
	sort.SliceStable(entries, func(first, second int) bool {
		a, b := entries[first], entries[second]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if !a.Latest.Equal(b.Latest) {
			return a.Latest.After(b.Latest)
		}
		return a.Says < b.Says
	})
}

// summarizeMoving is each run in progress with the newest result about its
// change.
func summarizeMoving(in SummaryInputs, now time.Time, names summaryNames) SummaryAnswer {
	const heading = "In progress"
	if in.Standing == nil || in.StandingProblem != "" {
		return unreadAnswer(heading, in.StandingProblem)
	}
	if in.Standing.RunningProblem != "" {
		return unreadAnswer(heading, in.Standing.RunningProblem)
	}
	moving := SummaryAnswer{Heading: heading, Entries: []SummaryEntry{}}
	for _, run := range in.Standing.Running {
		entry := SummaryEntry{Says: names.name(run.WorkItemID, run.Title) + " — " + stageSays(run), WorkItemID: run.WorkItemID, Count: 1, Detail: runDetail(run)}
		switch {
		case run.NoProcess != "":
			entry.Says += "; nothing is running it any more"
			entry.Detail = append(entry.Detail, run.NoProcess)
			if run.NoProcessRemedy != "" {
				entry.Detail = append(entry.Detail, run.NoProcessRemedy)
			}
		case run.Evidence != "":
			entry.Says += "; latest: " + run.Evidence
			entry.Latest = run.EvidenceAt
			entry.LatestSays = summaryMoment(run.EvidenceAt, now)
		default:
			entry.Says += "; no check or review result yet"
		}
		moving.Entries = append(moving.Entries, entry)
	}
	for _, run := range in.Standing.PausedRuns {
		entry := SummaryEntry{Says: names.name(run.WorkItemID, run.Title) + " — paused until the work it waits on lands", WorkItemID: run.WorkItemID, Count: 1, Detail: runDetail(run)}
		if run.WaitingOn != "" {
			entry.Detail = append(entry.Detail, "waiting on "+run.WaitingOn)
		}
		moving.Entries = append(moving.Entries, entry)
	}
	moving.Says = count(len(moving.Entries), "run")
	return moving
}

// stageSays is where a run is, in words.
func stageSays(run RunningRun) string {
	if run.documentLanding {
		return "publishing a document"
	}
	switch run.Stage {
	case StageReviewing:
		return "in review"
	case StageIntegrating:
		return "landing"
	}
	if run.Checks != "" {
		return "in its checks"
	}
	return "being developed"
}

// runDetail is what the summary keeps behind a run's line.
func runDetail(run RunningRun) []string {
	detail := []string{"run " + run.RunID}
	if run.Phase != "" {
		detail = append(detail, "phase "+string(run.Phase))
	}
	if run.Checks != "" {
		detail = append(detail, run.Checks)
	}
	if run.Model != "" {
		detail = append(detail, "model "+run.Model)
	}
	return detail
}

// evidenceOf is the newest result a run's record holds about its current
// change. The review is read first because it comes after the checks; the
// checks' three answers — passed, failed, a protected file refused — clear one
// another, so at most one of them is about the current attempt.
func evidenceOf(state runstate.State) (string, time.Time) {
	switch state.ReviewDecision {
	case runstate.ReviewApprove:
		return "the review approved the change", time.Time{}
	case runstate.ReviewRepair:
		return fmt.Sprintf("the review returned %s, and the developer is answering them", count(max(state.ReviewFindings, len(state.ReviewFindingDetails)), "finding")), time.Time{}
	case runstate.ReviewEscalate:
		return "the review said the work cannot be met as written", time.Time{}
	}
	switch {
	case state.PathRefusal != nil:
		return "the change touched a file it may not, and the developer is undoing that", time.Time{}
	case state.CheckFailure != nil:
		return "a check failed, and the developer is repairing it", time.Time{}
	case state.ChecksPassed != nil:
		return "the checks passed", state.ChecksPassed.At
	}
	return "", time.Time{}
}

// summarizeWaiting is what waits on somebody: the owner's own decisions, and
// everything else as blocked, each with whose move it is and the next step.
func summarizeWaiting(in SummaryInputs, now time.Time, names summaryNames) (SummaryAnswer, SummaryAnswer) {
	const (
		blockedHeading   = "Blocked"
		decisionsHeading = "Needs your decision"
	)
	if in.Standing == nil || in.StandingProblem != "" {
		return unreadAnswer(blockedHeading, in.StandingProblem), unreadAnswer(decisionsHeading, in.StandingProblem)
	}
	standing := in.Standing
	blocked := SummaryAnswer{Heading: blockedHeading, Entries: []SummaryEntry{}}
	decisions := SummaryAnswer{Heading: decisionsHeading, Entries: []SummaryEntry{}}

	// The held work the attention line counts is the same items the groups
	// below name with their next step, so it is said once, by the groups,
	// wherever the groups could be read.
	groupsRead := standing.NotStartableProblem == ""
	var mine, theirs []Attention
	for _, waiting := range standing.NeedsHuman {
		switch {
		case waiting.Kind == AttentionHeldWork && groupsRead:
			continue
		case waiting.Mover.IsOperator(), waiting.Ownership.NeedsHand():
			mine = append(mine, waiting)
		default:
			theirs = append(theirs, waiting)
		}
	}
	decisions.Entries = append(decisions.Entries, groupAttention(mine, now, names)...)
	blocked.Entries = append(blocked.Entries, groupAttention(theirs, now, names)...)
	if groupsRead {
		for _, group := range standing.NotStartableGroups {
			entry := SummaryEntry{
				Says:      group.counted(),
				WaitingOn: waitingOn(group.Mover),
				Next:      group.Next,
				Count:     group.Count,
			}
			for _, item := range group.Items {
				entry.Detail = append(entry.Detail, names.name(item.WorkItemID, item.Title))
			}
			if group.Mover.IsOperator() {
				decisions.Entries = append(decisions.Entries, entry)
				continue
			}
			blocked.Entries = append(blocked.Entries, entry)
		}
	}
	sortEntries(blocked.Entries)
	sortEntries(decisions.Entries)

	blocked.Says = "nothing"
	if len(blocked.Entries) > 0 {
		blocked.Says = count(len(blocked.Entries), "thing") + " waiting on somebody other than you"
	}
	decisions.Says = "nothing"
	if len(decisions.Entries) > 0 {
		decisions.Says = count(len(decisions.Entries), "thing") + " only you can do"
	}
	for _, answer := range []*SummaryAnswer{&blocked, &decisions} {
		if standing.NeedsHumanProblem != "" {
			answer.Detail = append(answer.Detail, standing.NeedsHumanProblem)
		}
		if standing.NotStartableProblem != "" {
			answer.Detail = append(answer.Detail, standing.NotStartableProblem)
		}
		if len(answer.Detail) > 0 {
			answer.Says += "; not everything could be read"
		}
	}
	return blocked, decisions
}

// groupAttention folds the entries that are the same thing — one kind, one
// mover, one next step — into one line with how many there are and the newest
// of them, and keeps each entry's own sentence as the line's detail, since that
// sentence names runs and records the line does not.
func groupAttention(entries []Attention, now time.Time, names summaryNames) []SummaryEntry {
	type key struct {
		label string
		mover Mover
		next  string
	}
	grouped := map[key]*SummaryEntry{}
	var order []key
	for _, waiting := range entries {
		k := key{label: waiting.Label(), mover: waiting.Mover, next: waiting.Ownership.Remedy}
		entry, seen := grouped[k]
		if !seen {
			entry = &SummaryEntry{WaitingOn: waitingOn(waiting.Mover), Next: waiting.Ownership.Remedy}
			if waiting.Ownership.NeedsHand() {
				entry.WaitingOn = "you, to carry out " + waiting.Mover.Possessive() + " decision"
			}
			if because := waiting.Ownership.ReasonSays(); because != "" {
				entry.WaitingOn += ", because it is " + because
			}
			grouped[k] = entry
			order = append(order, k)
		}
		entry.Count++
		if since := waiting.Since(); since.After(entry.Latest) {
			entry.Latest = since
		}
		if entry.Count == 1 {
			entry.WorkItemID = waiting.WorkItemID
		} else if entry.WorkItemID != waiting.WorkItemID {
			entry.WorkItemID = ""
		}
		detail := waiting.CitedWhat() + " — " + waiting.CitedWhose()
		if waiting.ID != "" {
			detail = waiting.ID + ": " + detail
		}
		if hand := waiting.CitedHand(); hand != "" {
			detail += " — " + hand
		}
		if capability := waiting.Ownership.Capability; capability != "" {
			detail += " (needs " + capability + ")"
		}
		entry.Detail = append(entry.Detail, detail)
	}
	lines := make([]SummaryEntry, 0, len(order))
	for _, k := range order {
		entry := grouped[k]
		says := capitalized(k.label)
		switch {
		case entry.Count > 1 && entry.WorkItemID != "":
			says += ", " + count(entry.Count, "time") + ", on " + names.name(entry.WorkItemID, "")
		case entry.Count > 1:
			says += ", " + count(entry.Count, "time")
		case entry.WorkItemID != "":
			says += ": " + names.name(entry.WorkItemID, "")
		}
		entry.Says = says
		entry.LatestSays = summaryMoment(entry.Latest, now)
		lines = append(lines, *entry)
	}
	return lines
}

// waitingOn is who an entry waits on, as the summary says it to the operator:
// "you" where it is his, and the mover the fourth line names otherwise.
func waitingOn(mover Mover) string {
	if mover.IsOperator() {
		return "you"
	}
	return strings.TrimPrefix(mover.WaitingOn(), "Waiting on ")
}

// capitalized opens a line with a capital letter.
func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// summarizeCost is what the period cost, by the spend reading's own figures:
// today's line of the month for today, and the week's window for the week.
func summarizeCost(in SummaryInputs, period SummaryPeriod, now time.Time) SummaryAnswer {
	const heading = "Cost"
	problem := in.SpendProblem
	if problem == "" && in.Spend != nil {
		problem = in.Spend.Problem
	}
	if in.Spend == nil || problem != "" {
		return unreadAnswer(heading, problem)
	}
	spend := in.Spend
	var (
		cost     float64
		tokens   runstate.TokenUsage
		calls    int
		kinds    []KindSpend
		costText string
		floor    = spend.Floor
	)
	if period == SummaryWeek {
		found := false
		for _, window := range spend.Windows {
			if !window.Rolling && window.Days == 7 {
				cost, tokens, calls, kinds, costText, floor, found = window.CostUSD, window.Tokens, window.Invocations, window.Kinds, window.CostText, window.Floor, true
			}
		}
		if !found {
			return unreadAnswer(heading, "the spend carries no window of 7 days")
		}
	} else {
		// Today's line of the month. A day no priced record reaches carries no
		// figures, and as far as the records go nothing was spent on it.
		today := firstLocalDay(now, 1)
		for _, day := range spend.Days {
			if day.Day == today && day.Reached {
				cost, tokens, calls, kinds, costText = day.CostUSD, day.Tokens, day.Invocations, day.Kinds, day.CostText
			}
		}
	}
	says := fmt.Sprintf("$%.2f", cost)
	if tokens.NoCost > 0 {
		says += fmt.Sprintf(" reported, and %s whose cost the provider did not report", count(tokens.NoCost, "call"))
	}
	says += " across " + count(calls, "provider call")
	if floor {
		says = "at least " + says + ", because some records could not be read"
	}
	answer := SummaryAnswer{Heading: heading, Says: says, Entries: []SummaryEntry{}}
	for _, kind := range kinds {
		text := kind.CostText
		if text == "" {
			text = fmt.Sprintf("$%.2f", kind.CostUSD)
		}
		answer.Detail = append(answer.Detail, fmt.Sprintf("%s: %s over %s", kind.Kind, text, count(kind.Invocations, "call")))
	}
	if costText != "" && tokens.NoCost > 0 {
		answer.Detail = append(answer.Detail, "as priced: "+costText)
	}
	return answer
}

// Render is the summary as `yoyo status` opens with it: a heading, then each
// answer with its lines under it. With detail, each line is followed by what
// it keeps behind it. It is deterministic, so the same summary always prints
// the same text.
func (s OwnerSummary) Render(detail bool) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Summary, %s (read at %s):\n", s.SinceSays, summaryMoment(s.ObservedAt, s.ObservedAt))
	for _, answer := range s.answers() {
		fmt.Fprintf(&rendered, "  %s: %s\n", answer.Heading, answer.Says)
		if detail {
			for _, line := range answer.Detail {
				fmt.Fprintf(&rendered, "      detail: %s\n", line)
			}
		}
		listed := 0
		for _, entry := range answer.Entries {
			if listed == maxListed {
				fmt.Fprintf(&rendered, "    - and %d more\n", len(answer.Entries)-listed)
				break
			}
			listed++
			rendered.WriteString("    - " + entry.Line + "\n")
			if detail {
				for _, line := range entry.Detail {
					fmt.Fprintf(&rendered, "      detail: %s\n", line)
				}
			}
		}
	}
	return s.wording.Render(rendered.String())
}

// line is one entry as the summary prints it.
func (e SummaryEntry) line() string {
	line := e.Says
	if e.LatestSays != "" {
		if e.Count > 1 {
			line += " (latest " + e.LatestSays + ")"
		} else {
			line += " (" + e.LatestSays + ")"
		}
	}
	if e.Next != "" {
		line += " — next: " + e.Next
	}
	if e.WaitingOn != "" {
		line += "; waiting on " + e.WaitingOn
	}
	return line
}
