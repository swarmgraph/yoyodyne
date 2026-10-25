package readmodel

// What the spend accomplished: each management pass and conversation turn by
// what it led to, each review by whether it stopped a defect, and each
// completed work item by everything it cost.
//
// The spend log already says what every provider invocation cost and which role
// made it. What it cannot say is whether the money moved anything, and the
// management roles spend about half of it. This reading joins the spend log to
// the harness's own records of what each invocation did, and nothing else: the
// recurring passes' records, the tracker actions each conversation applied, the
// run records, and the review verdicts in each run's event log. No figure is
// estimated and no prose is judged.
//
// A management pass is one recurring pass's turns taken together, as its record
// bounds them; every other management invocation — a turn of a conversation
// with the operator, a side thread, an answer to another role's ask — is a pass
// of its own. Each is classified once, in this order:
//
//   - unblocked delivery: it acted on a work item in the tracker — admitted,
//     released, repaired, decided, reprioritized — and the item then
//     progressed: a developer or reviewer spent on it afterwards, or a run for
//     it started afterwards;
//   - changed records only: it acted on the tracker, wrote a memory or a lane
//     report, filed a report, recorded a document, proposal, directive or
//     concern, or answered an ask, and nothing it acted on progressed after;
//   - repeated an earlier pass: it changed nothing, and an earlier pass of the
//     same recurring task was handed the same inputs — the events, the health
//     conditions, the criticals, the work delivered to it, the request — and
//     gave the same account: status, and each finding's disposition and filed
//     work. A conversation turn's input is the operator's message, which this
//     does not compare, so a turn is never counted as a repeat;
//   - changed nothing: it left the tracker and the records as they were.
//
// A review is one verdict a reviewer recorded on a run. It prevented a defect
// where it asked for a repair, the developer then repaired the change, and the
// change was then approved or merged; it approved unchanged where its verdict
// was an approval; the findings were not fixed where it asked for a repair and
// no approval followed; it escalated where it sent the item to the development
// manager; and it has no verdict where the reviewer was paid and recorded none.
//
// A completed item is one whose change merged in the range, read exactly as the
// delivery measures read a merge. Its cost is every invocation that named it,
// at any time: the developer's first attempt, every retry — a repair attempt,
// or any later run's first attempt — every review, and a share of each
// management pass that named it, split evenly among the items that pass named.
// A management pass names the items it acted on and, for a recurring pass, the
// items its account names.
//
// Every day and week is counted in Pacific time, as the delivery measures are.
// A source that could not be read is named under not_read and makes the figures
// a floor; an invocation whose cost the provider never reported is counted as
// unpriced rather than as free.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/workref"
)

// PassOutcome is what one management pass led to.
type PassOutcome string

const (
	PassUnblocked      PassOutcome = "unblocked-delivery"
	PassChangedRecords PassOutcome = "changed-records-only"
	PassRepeated       PassOutcome = "repeated-earlier"
	PassChangedNothing PassOutcome = "changed-nothing"
)

// PassOutcomes lists every outcome, in the order a report states them.
func PassOutcomes() []PassOutcome {
	return []PassOutcome{PassUnblocked, PassChangedRecords, PassRepeated, PassChangedNothing}
}

// Describe is the outcome in the words a report prints.
func (o PassOutcome) Describe() string {
	switch o {
	case PassUnblocked:
		return "unblocked delivery"
	case PassChangedRecords:
		return "changed records only"
	case PassRepeated:
		return "repeated an earlier pass"
	case PassChangedNothing:
		return "changed nothing"
	}
	return string(o)
}

// ReviewOutcome is what one review led to.
type ReviewOutcome string

const (
	ReviewPreventedDefect   ReviewOutcome = "prevented-defect"
	ReviewApprovedUnchanged ReviewOutcome = "approved-unchanged"
	ReviewFindingsNotFixed  ReviewOutcome = "findings-not-fixed"
	ReviewEscalated         ReviewOutcome = "escalated"
	ReviewNoVerdict         ReviewOutcome = "no-verdict"
)

// ReviewOutcomes lists every outcome, in the order a report states them.
func ReviewOutcomes() []ReviewOutcome {
	return []ReviewOutcome{ReviewPreventedDefect, ReviewApprovedUnchanged, ReviewFindingsNotFixed, ReviewEscalated, ReviewNoVerdict}
}

// Describe is the outcome in the words a report prints.
func (o ReviewOutcome) Describe() string {
	switch o {
	case ReviewPreventedDefect:
		return "prevented a defect"
	case ReviewApprovedUnchanged:
		return "approved unchanged"
	case ReviewFindingsNotFixed:
		return "findings not fixed"
	case ReviewEscalated:
		return "escalated"
	case ReviewNoVerdict:
		return "no verdict"
	}
	return string(o)
}

// AccomplishmentSpend is every recorded provider invocation. It is satisfied by
// *runstate.SpendStore.
type AccomplishmentSpend interface {
	List() ([]runstate.Spend, error)
}

// AccomplishmentRuns is the run records and the events of the named types in
// each run's log. It is satisfied by *runstate.Store.
type AccomplishmentRuns interface {
	Recorded() ([]runstate.State, error)
	EventsOfType(runID string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error)
}

// AccomplishmentConversations is the events of the named types in each
// conversation's log. It is satisfied by *runstate.ConversationStore.
type AccomplishmentConversations interface {
	EventsOfType(conversationID string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error)
}

// AccomplishmentSources are the records one reading is assembled from. Each is
// optional and carries the reason its caller could not open it.
type AccomplishmentSources struct {
	Spend                AccomplishmentSpend
	SpendProblem         string
	Passes               Passes
	PassesProblem        string
	Runs                 AccomplishmentRuns
	RunsProblem          string
	Conversations        AccomplishmentConversations
	ConversationsProblem string
	// Zone is the zone days and weeks are counted in. Nil is DeliveryZoneName.
	Zone *time.Location
	// Now stamps the reading and anchors the default range. It defaults to the
	// wall clock.
	Now func() time.Time
}

// AccomplishmentRange is the local days a reading covers, first and last
// included, as YYYY-MM-DD. An empty From is the Monday three weeks before the
// current week's, and an empty To is today: the current week and the three
// full weeks before it.
type AccomplishmentRange struct {
	From string
	To   string
}

// accomplishmentWeeks is how many weeks the default range covers, the current
// one included.
const accomplishmentWeeks = 4

// OutcomeShare is one outcome's part of a role's passes or of the reviews.
type OutcomeShare struct {
	Outcome string  `json:"outcome"`
	Says    string  `json:"says"`
	Count   int     `json:"count"`
	CostUSD float64 `json:"cost_usd"`
	// Share is Count over every pass or review counted beside it, from 0 to 1.
	Share float64 `json:"share"`
}

// RoleAccomplishment is one role's spend over a period, and for a management
// role what its passes led to.
type RoleAccomplishment struct {
	Role        domain.AgentRole `json:"role"`
	CostUSD     float64          `json:"cost_usd"`
	Invocations int              `json:"invocations"`
	Unpriced    int              `json:"unpriced"`
	// Management says the role's invocations are passes classified below. A
	// developer's and a reviewer's are not, and carry no passes.
	Management bool           `json:"management"`
	Passes     int            `json:"passes"`
	Outcomes   []OutcomeShare `json:"outcomes,omitempty"`
}

// WeekAccomplishment is one Monday-to-Sunday week of the range, clipped to it.
type WeekAccomplishment struct {
	Week  string               `json:"week"`
	From  string               `json:"from"`
	To    string               `json:"to"`
	Roles []RoleAccomplishment `json:"roles"`
	// CostUSD is everything spent in the week, and Completed the items whose
	// change merged in it, with their full cost and its mean.
	CostUSD              float64 `json:"cost_usd"`
	Completed            int     `json:"completed"`
	CompletedCostUSD     float64 `json:"completed_cost_usd"`
	CostPerCompletedItem float64 `json:"cost_per_completed_item"`
}

// ManagementPass is one management pass and what it led to.
type ManagementPass struct {
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// Task is the recurring task whose pass this was; it is empty on a
	// conversation turn, a side thread's turn, and an answer to an ask.
	Task           string      `json:"task,omitempty"`
	ConversationID string      `json:"conversation_id,omitempty"`
	SideStreamID   string      `json:"side_stream_id,omitempty"`
	ExchangeID     string      `json:"exchange_id,omitempty"`
	StartedAt      time.Time   `json:"started_at"`
	EndedAt        time.Time   `json:"ended_at"`
	Invocations    int         `json:"invocations"`
	CostUSD        float64     `json:"cost_usd"`
	Unpriced       int         `json:"unpriced,omitempty"`
	Outcome        PassOutcome `json:"outcome"`
	// ActedOn are the work items it acted on in the tracker, Progressed those
	// of them that progressed after, and Named every item its cost is shared
	// among.
	ActedOn    []string `json:"acted_on,omitempty"`
	Progressed []string `json:"progressed,omitempty"`
	Named      []string `json:"named,omitempty"`
	// Changes says what it changed, in words, where it changed anything.
	Changes []string `json:"changes,omitempty"`
	// RepeatOf is when the earlier pass it repeated started.
	RepeatOf *time.Time `json:"repeat_of,omitempty"`
}

// ReviewAccomplishment is one review of a run and what it led to.
type ReviewAccomplishment struct {
	RunID       string        `json:"run_id"`
	WorkItemID  string        `json:"work_item_id"`
	Title       string        `json:"title,omitempty"`
	At          time.Time     `json:"at"`
	Decision    string        `json:"decision,omitempty"`
	Findings    int           `json:"findings"`
	CostUSD     float64       `json:"cost_usd"`
	Invocations int           `json:"invocations"`
	Outcome     ReviewOutcome `json:"outcome"`
}

// CompletedItemCost is one item whose change merged in the range and what it
// cost, broken down.
type CompletedItemCost struct {
	WorkItemID     string    `json:"work_item_id"`
	Title          string    `json:"title,omitempty"`
	MergedAt       time.Time `json:"merged_at"`
	Runs           int       `json:"runs"`
	DevelopmentUSD float64   `json:"development_usd"`
	ReviewUSD      float64   `json:"review_usd"`
	RetriesUSD     float64   `json:"retries_usd"`
	ManagementUSD  float64   `json:"management_usd"`
	TotalUSD       float64   `json:"total_usd"`
	// ManagementPasses is how many management passes named it.
	ManagementPasses int `json:"management_passes"`
	// Unpriced counts its invocations whose cost was never reported, which
	// make its total a floor.
	Unpriced int `json:"unpriced,omitempty"`
}

// Accomplishment is the reading.
type Accomplishment struct {
	ObservedAt time.Time `json:"observed_at"`
	Zone       string    `json:"zone"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	CostUSD    float64   `json:"cost_usd"`
	Unpriced   int       `json:"unpriced"`
	// Roles are the range's spend by role, management roles first.
	Roles []RoleAccomplishment `json:"roles"`
	Weeks []WeekAccomplishment `json:"weeks"`
	// Passes are every management pass in the range, oldest first.
	Passes []ManagementPass `json:"passes"`
	// Reviews are every review in the range, oldest first, and ReviewOutcomes
	// their totals.
	Reviews        []ReviewAccomplishment `json:"reviews"`
	ReviewOutcomes []OutcomeShare         `json:"review_outcomes"`
	// Completed are the items whose change merged in the range, newest first,
	// with their mean full cost.
	Completed            []CompletedItemCost `json:"completed"`
	CompletedCostUSD     float64             `json:"completed_cost_usd"`
	CostPerCompletedItem float64             `json:"cost_per_completed_item"`
	// UnnamedManagementUSD is what the range's management passes cost that
	// named no work item, and so is in no item's cost, and UnnamedPasses how
	// many passes that was.
	UnnamedManagementUSD float64 `json:"unnamed_management_usd"`
	UnnamedPasses        int     `json:"unnamed_passes"`
	// NotRead names every source that could not be read whole, and Floor says
	// the figures are a lower bound because of it.
	NotRead []string `json:"not_read,omitempty"`
	Floor   bool     `json:"floor"`
	// Problem is set where the spend could not be read at all, and every
	// figure is then absent.
	Problem string `json:"problem,omitempty"`
}

// The conversation events that say a turn changed something, and the tracker
// actions that read rather than change.
var (
	changeEvents = map[execution.EventType]string{
		execution.EventTrackerActionApplied: "acted on the tracker",
		execution.EventMemoryRecorded:       "wrote a memory",
		execution.EventLaneReportRecorded:   "changed its lane report",
		execution.EventReportRecorded:       "filed a report",
		execution.EventDocumentDrafted:      "drafted a document",
		execution.EventDocumentWritten:      "wrote a document",
		execution.EventProposalRecorded:     "recorded a proposal",
		execution.EventProposalCreated:      "proposed work",
		execution.EventDirectiveRecorded:    "recorded a directive",
		execution.EventConcernRaised:        "raised a concern",
	}
	readingActions = map[string]bool{"read": true, "survey": true}
)

func changeEventTypes() []execution.EventType {
	types := make([]execution.EventType, 0, len(changeEvents))
	for eventType := range changeEvents {
		types = append(types, eventType)
	}
	sort.Slice(types, func(a, b int) bool { return types[a] < types[b] })
	return types
}

// ManagementRole reports a role whose invocations are management passes: every
// role but the developer and the reviewer, whose work is the runs.
func ManagementRole(role domain.AgentRole) bool {
	return role != domain.RoleDeveloper && role != domain.RoleReviewer
}

// ReadAccomplishment assembles the reading for the range.
func ReadAccomplishment(ctx context.Context, sources AccomplishmentSources, span AccomplishmentRange) Accomplishment {
	zone := sources.Zone
	if zone == nil {
		zone = DeliveryZone()
	}
	now := time.Now()
	if sources.Now != nil {
		now = sources.Now()
	}
	reading := Accomplishment{ObservedAt: now, Zone: zone.String(), Roles: []RoleAccomplishment{}, Weeks: []WeekAccomplishment{},
		Passes: []ManagementPass{}, Reviews: []ReviewAccomplishment{}, Completed: []CompletedItemCost{}}
	from, to, err := accomplishmentSpan(span, now, zone)
	if err != nil {
		reading.Problem = err.Error()
		return reading
	}
	reading.From, reading.To = from.Format(time.DateOnly), to.AddDate(0, 0, -1).Format(time.DateOnly)

	var lines []runstate.Spend
	switch {
	case ctx.Err() != nil:
		reading.Problem = fmt.Sprintf("the spend was not read: %v", ctx.Err())
	case sources.Spend == nil:
		reading.Problem = absent("the spend log", sources.SpendProblem)
	default:
		lines, err = sources.Spend.List()
		if err != nil {
			reading.Problem = fmt.Sprintf("the spend log could not be read: %v", err)
		}
	}
	if reading.Problem != "" {
		return reading
	}

	read := &accomplishmentRead{}
	sweeps := read.passes(sources)
	runs := read.runs(sources)
	passes := buildPasses(lines, sweeps)
	read.classify(ctx, sources.Conversations, sources.ConversationsProblem, passes, lines, runs)
	reviews := read.reviews(ctx, sources.Runs, lines, runs, from, to)
	completed := completedItems(lines, runs, passes, from, to)

	inRange := func(at time.Time) bool { return !at.Before(from) && at.Before(to) }
	var rangeLines []runstate.Spend
	for _, line := range lines {
		if inRange(line.At) {
			rangeLines = append(rangeLines, line)
		}
	}
	var rangePasses []*managementPass
	for _, pass := range passes {
		if inRange(pass.StartedAt) {
			rangePasses = append(rangePasses, pass)
			reading.Passes = append(reading.Passes, pass.ManagementPass)
			if len(pass.Named) == 0 {
				reading.UnnamedManagementUSD += pass.CostUSD
				reading.UnnamedPasses++
			}
		}
	}
	reading.Roles = roleAccomplishments(rangeLines, rangePasses)
	for _, role := range reading.Roles {
		reading.CostUSD += role.CostUSD
		reading.Unpriced += role.Unpriced
	}
	reading.Reviews = append(reading.Reviews, reviews...)
	reading.ReviewOutcomes = reviewShares(reviews)
	reading.Completed = append(reading.Completed, completed...)
	reading.CompletedCostUSD, reading.CostPerCompletedItem = completedMean(completed)

	for start := weekStart(from); start.Before(to); start = start.AddDate(0, 0, 7) {
		end := start.AddDate(0, 0, 7)
		clippedFrom, clippedTo := start, end
		if clippedFrom.Before(from) {
			clippedFrom = from
		}
		if clippedTo.After(to) {
			clippedTo = to
		}
		within := func(at time.Time) bool { return !at.Before(clippedFrom) && at.Before(clippedTo) }
		var weekLines []runstate.Spend
		for _, line := range rangeLines {
			if within(line.At) {
				weekLines = append(weekLines, line)
			}
		}
		var weekPasses []*managementPass
		for _, pass := range rangePasses {
			if within(pass.StartedAt) {
				weekPasses = append(weekPasses, pass)
			}
		}
		week := WeekAccomplishment{
			Week:  start.Format(time.DateOnly),
			From:  clippedFrom.Format(time.DateOnly),
			To:    clippedTo.AddDate(0, 0, -1).Format(time.DateOnly),
			Roles: roleAccomplishments(weekLines, weekPasses),
		}
		for _, role := range week.Roles {
			week.CostUSD += role.CostUSD
		}
		var weekCompleted []CompletedItemCost
		for _, item := range completed {
			if within(item.MergedAt) {
				weekCompleted = append(weekCompleted, item)
			}
		}
		week.Completed = len(weekCompleted)
		week.CompletedCostUSD, week.CostPerCompletedItem = completedMean(weekCompleted)
		reading.Weeks = append(reading.Weeks, week)
	}

	reading.NotRead = read.notRead
	reading.Floor = len(read.notRead) > 0 || reading.Unpriced > 0
	return reading
}

// accomplishmentSpan is the range as instants: the first moment of its first
// day and the first moment after its last.
func accomplishmentSpan(span AccomplishmentRange, now time.Time, zone *time.Location) (time.Time, time.Time, error) {
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	from := weekStart(today).AddDate(0, 0, -7*(accomplishmentWeeks-1))
	to := today
	if span.From != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, span.From, zone)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%q is not a day written as YYYY-MM-DD", span.From)
		}
		from = parsed
	}
	if span.To != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, span.To, zone)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%q is not a day written as YYYY-MM-DD", span.To)
		}
		to = parsed
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("the range ends on %s, before it starts on %s", to.Format(time.DateOnly), from.Format(time.DateOnly))
	}
	return from, to.AddDate(0, 0, 1), nil
}

// weekStart is the Monday on or before the day.
func weekStart(day time.Time) time.Time {
	back := (int(day.Weekday()) + 6) % 7
	return time.Date(day.Year(), day.Month(), day.Day()-back, 0, 0, 0, 0, day.Location())
}

// accomplishmentRead carries what could not be read across the reading.
type accomplishmentRead struct {
	notRead []string
}

func (r *accomplishmentRead) missing(format string, args ...any) {
	r.notRead = append(r.notRead, fmt.Sprintf(format, args...))
}

func (r *accomplishmentRead) passes(sources AccomplishmentSources) []runstate.Sweep {
	if sources.Passes == nil {
		r.missing("%s, so no recurring pass is told from the conversation turns around it", absent("the recurring passes", sources.PassesProblem))
		return nil
	}
	recorded, unreadable, err := sources.Passes.List()
	if err != nil {
		r.missing("the recurring passes could not be read to the end: %v", err)
	}
	if len(unreadable) > 0 {
		r.missing("%d line(s) of the recurring passes' log could not be read", len(unreadable))
	}
	return recorded
}

func (r *accomplishmentRead) runs(sources AccomplishmentSources) []runstate.State {
	if sources.Runs == nil {
		r.missing("%s, so no item is known to have progressed or completed", absent("the run records", sources.RunsProblem))
		return nil
	}
	recorded, err := sources.Runs.Recorded()
	if err != nil {
		r.missing("the run records could not be read: %v", err)
		return nil
	}
	return recorded
}

// managementPass is a pass with what its classification needs beside it.
type managementPass struct {
	ManagementPass
	sweep  *runstate.Sweep
	acts   []act
	change map[string]bool
}

// act is one thing a pass did to a work item, and when.
type act struct {
	item string
	at   time.Time
}

// buildPasses groups the management invocations into passes: the lines of one
// conversation inside a recurring pass's bounds are that pass, and every other
// management line is a pass of its own. Passes come back oldest first.
func buildPasses(lines []runstate.Spend, sweeps []runstate.Sweep) []*managementPass {
	byConversation := map[string][]int{}
	for index, recorded := range sweeps {
		if recorded.Role == "" || recorded.ConversationID == "" || recorded.StartedAt.IsZero() {
			continue
		}
		byConversation[recorded.ConversationID] = append(byConversation[recorded.ConversationID], index)
	}
	var passes []*managementPass
	bySweep := map[int]*managementPass{}
	for _, line := range lines {
		if !ManagementRole(line.Role) {
			continue
		}
		owner := -1
		if line.SideStreamID == "" && line.ConversationID != "" {
			for _, index := range byConversation[line.ConversationID] {
				recorded := sweeps[index]
				if !line.At.Before(recorded.StartedAt) && !line.At.After(recorded.EndedAt) {
					owner = index
					break
				}
			}
		}
		pass := bySweep[owner]
		if owner < 0 || pass == nil {
			pass = &managementPass{change: map[string]bool{}, ManagementPass: ManagementPass{
				Role:           line.Role,
				Agent:          line.Agent,
				ConversationID: line.ConversationID,
				SideStreamID:   line.SideStreamID,
				ExchangeID:     line.ExchangeID,
				StartedAt:      line.At,
				EndedAt:        line.At,
			}}
			if owner >= 0 {
				recorded := &sweeps[owner]
				pass.sweep = recorded
				pass.Task = recorded.Task
				pass.StartedAt, pass.EndedAt = recorded.StartedAt, recorded.EndedAt
				bySweep[owner] = pass
			}
			passes = append(passes, pass)
		}
		pass.Invocations++
		if line.Known() {
			pass.CostUSD += line.AmountUSD
		} else {
			pass.Unpriced++
		}
	}
	sort.SliceStable(passes, func(a, b int) bool { return passes[a].StartedAt.Before(passes[b].StartedAt) })
	return passes
}

// classify reads what each pass changed and decides what it led to.
func (r *accomplishmentRead) classify(ctx context.Context, conversations AccomplishmentConversations, problem string, passes []*managementPass, lines []runstate.Spend, runs []runstate.State) {
	// What a pass recorded of its own: the writes, the reports, the admissions,
	// the items its account named.
	for _, pass := range passes {
		if pass.ExchangeID != "" {
			pass.change["answered an ask"] = true
		}
		if pass.sweep == nil {
			continue
		}
		recorded := pass.sweep
		for _, saved := range recorded.Saved {
			if saved.Kind == runstate.SavedLaneReport {
				pass.change["changed its lane report"] = true
			} else {
				pass.change["wrote a memory"] = true
			}
		}
		if recorded.ReportsFiled > 0 {
			pass.change["filed a report"] = true
		}
		for _, admitted := range recorded.Admitted {
			pass.change["acted on the tracker"] = true
			pass.acts = append(pass.acts, act{item: admitted, at: recorded.StartedAt})
		}
		if recorded.References != nil {
			pass.Named = append(pass.Named, referenceIDs(recorded.References.Summary)...)
			for _, each := range recorded.References.Issues {
				pass.Named = append(pass.Named, referenceIDs(each)...)
			}
			for _, each := range recorded.References.Details {
				pass.Named = append(pass.Named, referenceIDs(each)...)
			}
		}
	}

	// What each conversation's log says its turns did. An event belongs to the
	// pass whose bounds hold it, and otherwise to the latest turn of that
	// conversation that had ended by then, because a turn's actions are applied
	// from its reply.
	turns := map[string][]*managementPass{}
	for _, pass := range passes {
		if pass.ConversationID != "" && pass.SideStreamID == "" {
			turns[pass.ConversationID] = append(turns[pass.ConversationID], pass)
		}
	}
	switch {
	case len(turns) == 0:
	case conversations == nil:
		r.missing("%s, so no conversation turn is known to have changed anything", absent("the conversations' event logs", problem))
	default:
		types := changeEventTypes()
		for _, conversationID := range deliveryKeys(turns) {
			if ctx.Err() != nil {
				r.missing("the conversations' event logs were not all read: %v", ctx.Err())
				break
			}
			events, skipped, err := conversations.EventsOfType(conversationID, types...)
			if err != nil {
				r.missing("the event log of %s could not be read: %v", conversationID, err)
				continue
			}
			if len(skipped) > 0 {
				r.missing("%d line(s) of the event log of %s could not be read", len(skipped), conversationID)
			}
			owned := turns[conversationID]
			for _, event := range events {
				pass := owningPass(owned, event.Timestamp)
				if pass == nil {
					continue
				}
				if event.Type == execution.EventTrackerActionApplied {
					action, item := trackerActionOf(event)
					if readingActions[action] {
						continue
					}
					if item != "" {
						pass.acts = append(pass.acts, act{item: item, at: event.Timestamp})
					}
				}
				pass.change[changeEvents[event.Type]] = true
			}
		}
	}

	// When each item progressed: every developer or reviewer invocation on it,
	// and every run started for it.
	progress := map[string][]time.Time{}
	for _, line := range lines {
		if !ManagementRole(line.Role) && line.WorkItemID != "" {
			progress[line.WorkItemID] = append(progress[line.WorkItemID], line.At)
		}
	}
	for _, state := range runs {
		progress[state.WorkItemID] = append(progress[state.WorkItemID], state.StartedAt)
	}
	progressedAfter := func(item string, at time.Time) bool {
		for _, moment := range progress[item] {
			if moment.After(at) {
				return true
			}
		}
		return false
	}

	seen := map[string]map[string]time.Time{}
	for _, pass := range passes {
		acted := map[string]bool{}
		progressed := map[string]bool{}
		for _, each := range pass.acts {
			acted[each.item] = true
			if progressedAfter(each.item, each.at) {
				progressed[each.item] = true
			}
		}
		pass.ActedOn = setKeys(acted)
		pass.Progressed = setKeys(progressed)
		named := map[string]bool{}
		for _, item := range append(pass.Named, pass.ActedOn...) {
			if strings.TrimSpace(item) != "" {
				named[item] = true
			}
		}
		pass.Named = setKeys(named)
		pass.Changes = setKeys(pass.change)

		switch {
		case len(pass.Progressed) > 0:
			pass.Outcome = PassUnblocked
		case len(pass.Changes) > 0:
			pass.Outcome = PassChangedRecords
		default:
			pass.Outcome = PassChangedNothing
		}
		if pass.sweep == nil {
			continue
		}
		key := passFingerprint(*pass.sweep)
		earlier := seen[pass.Task]
		if earlier == nil {
			earlier = map[string]time.Time{}
			seen[pass.Task] = earlier
		}
		if first, ok := earlier[key]; ok && pass.Outcome == PassChangedNothing {
			pass.Outcome = PassRepeated
			repeated := first
			pass.RepeatOf = &repeated
		} else if !ok && pass.Outcome == PassChangedNothing {
			earlier[key] = pass.StartedAt
		}
	}
}

// referenceIDs is the ids a list of references resolved to.
func referenceIDs(references []workref.Reference) []string {
	ids := make([]string, 0, len(references))
	for _, reference := range references {
		ids = append(ids, reference.ID)
	}
	return ids
}

// owningPass is the pass an event at that moment belongs to.
func owningPass(passes []*managementPass, at time.Time) *managementPass {
	var latest *managementPass
	for _, pass := range passes {
		if pass.sweep != nil && !at.Before(pass.StartedAt) && !at.After(pass.EndedAt) {
			return pass
		}
		if !pass.EndedAt.After(at) {
			latest = pass
		}
	}
	return latest
}

// trackerActionOf is the action an applied tracker event records, and the item
// it was applied to.
func trackerActionOf(event execution.Event) (string, string) {
	var recorded struct {
		Action struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		} `json:"action"`
		WorkItemID string `json:"work_item_id"`
	}
	if err := json.Unmarshal(event.Payload, &recorded); err != nil {
		return "", ""
	}
	item := strings.TrimSpace(recorded.WorkItemID)
	if item == "" {
		item = strings.TrimSpace(recorded.Action.ID)
	}
	return recorded.Action.Action, item
}

// passFingerprint is a recurring pass's inputs and account, as its record states
// them: two passes of one task with the same fingerprint were handed the same
// things and said the same about them.
func passFingerprint(recorded runstate.Sweep) string {
	var parts []string
	add := func(label string, values ...string) {
		sorted := append([]string(nil), values...)
		sort.Strings(sorted)
		parts = append(parts, label+"="+strings.Join(sorted, ","))
	}
	add("agent", recorded.Agent)
	add("summoned", recorded.Summoned)
	add("request", recorded.Request)
	var events []string
	for class, count := range recorded.Events {
		events = append(events, fmt.Sprintf("%s:%d", class, count))
	}
	add("events", events...)
	if recorded.Health != nil {
		add("health", recorded.Health.Delivered...)
	}
	add("criticals", recorded.Criticals...)
	var delivered []string
	for _, work := range recorded.Delivered {
		delivered = append(delivered, work.ID)
	}
	add("delivered", delivered...)
	if recorded.Docket != nil {
		var undelivered []string
		for _, at := range recorded.Docket.Undelivered {
			undelivered = append(undelivered, at.Key)
		}
		add("docket", undelivered...)
	}
	add("failed", fmt.Sprint(recorded.Failed || recorded.Problem != ""))
	if recorded.Result != nil {
		add("status", string(recorded.Result.Status))
		var findings []string
		for _, finding := range recorded.Result.Findings {
			filed := append([]string(nil), finding.Filed...)
			sort.Strings(filed)
			findings = append(findings, string(finding.Disposition)+":"+strings.Join(filed, "+"))
		}
		add("findings", findings...)
		add("questions", fmt.Sprint(len(recorded.Result.Questions)))
	}
	return strings.Join(parts, ";")
}

// reviews classifies every review made in the range.
func (r *accomplishmentRead) reviews(ctx context.Context, runs AccomplishmentRuns, lines []runstate.Spend, states []runstate.State, from, to time.Time) []ReviewAccomplishment {
	byRun := map[string][]runstate.Spend{}
	for _, line := range lines {
		if line.RunID != "" {
			byRun[line.RunID] = append(byRun[line.RunID], line)
		}
	}
	reviewed := map[string]bool{}
	for runID, runLines := range byRun {
		for _, line := range runLines {
			if line.Phase == runstate.SpendPhaseReview && !line.At.Before(from) && line.At.Before(to) {
				reviewed[runID] = true
			}
		}
	}
	if len(reviewed) == 0 {
		return nil
	}
	if runs == nil {
		r.missing("the runs' event logs could not be read, so no review's verdict is known")
		return nil
	}
	stateOf := map[string]runstate.State{}
	merged := map[string]time.Time{}
	for _, state := range states {
		stateOf[state.RunID] = state
		if state.Integration != nil {
			if at := EndedAt(state); merged[state.WorkItemID].IsZero() || at.Before(merged[state.WorkItemID]) {
				merged[state.WorkItemID] = at
			}
		}
	}
	var reviews []ReviewAccomplishment
	for _, runID := range deliveryKeys(reviewed) {
		if ctx.Err() != nil {
			r.missing("the runs' event logs were not all read: %v", ctx.Err())
			break
		}
		events, skipped, err := runs.EventsOfType(runID, execution.EventReviewCompleted)
		if err != nil {
			r.missing("the event log of %s could not be read: %v", runID, err)
			continue
		}
		if len(skipped) > 0 {
			r.missing("%d line(s) of the event log of %s could not be read", len(skipped), runID)
		}
		reviews = append(reviews, classifyReviews(runID, stateOf[runID], byRun[runID], events, merged, from, to)...)
	}
	sort.SliceStable(reviews, func(a, b int) bool { return reviews[a].At.Before(reviews[b].At) })
	return reviews
}

// classifyReviews is one run's reviews: each recorded verdict with the reviewer
// invocations that led to it, and any reviewer invocations after the last
// verdict as a review that recorded none.
func classifyReviews(runID string, state runstate.State, lines []runstate.Spend, events []execution.Event, merged map[string]time.Time, from, to time.Time) []ReviewAccomplishment {
	sort.SliceStable(events, func(a, b int) bool { return events[a].Sequence < events[b].Sequence })
	sort.SliceStable(lines, func(a, b int) bool { return lines[a].At.Before(lines[b].At) })
	type verdict struct {
		at       time.Time
		decision string
		findings int
	}
	var verdicts []verdict
	for _, event := range events {
		var payload struct {
			Decision string `json:"decision"`
			Findings int    `json:"findings"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		verdicts = append(verdicts, verdict{at: event.Timestamp, decision: payload.Decision, findings: payload.Findings})
	}
	workItem := state.WorkItemID
	repairedAfter := func(at time.Time) bool {
		for _, line := range lines {
			if line.Phase == runstate.SpendPhaseRepair && line.At.After(at) {
				return true
			}
		}
		return false
	}
	approvedAfter := func(at time.Time) bool {
		for _, later := range verdicts {
			if later.at.After(at) && later.decision == reviewApproved {
				return true
			}
		}
		mergedAt := merged[workItem]
		return !mergedAt.IsZero() && mergedAt.After(at)
	}

	var reviews []ReviewAccomplishment
	next := 0
	for _, each := range verdicts {
		review := ReviewAccomplishment{RunID: runID, WorkItemID: workItem, Title: state.WorkItemTitle, At: each.at, Decision: each.decision, Findings: each.findings}
		for next < len(lines) && !lines[next].At.After(each.at) {
			if lines[next].Phase == runstate.SpendPhaseReview {
				review.Invocations++
				if lines[next].Known() {
					review.CostUSD += lines[next].AmountUSD
				}
			}
			next++
		}
		switch {
		case each.decision == reviewApproved:
			review.Outcome = ReviewApprovedUnchanged
		case each.decision == "escalate":
			review.Outcome = ReviewEscalated
		case repairedAfter(each.at) && approvedAfter(each.at):
			review.Outcome = ReviewPreventedDefect
		default:
			review.Outcome = ReviewFindingsNotFixed
		}
		reviews = append(reviews, review)
	}
	unanswered := ReviewAccomplishment{RunID: runID, WorkItemID: workItem, Title: state.WorkItemTitle, Outcome: ReviewNoVerdict}
	for ; next < len(lines); next++ {
		if lines[next].Phase != runstate.SpendPhaseReview {
			continue
		}
		unanswered.Invocations++
		unanswered.At = lines[next].At
		if lines[next].Known() {
			unanswered.CostUSD += lines[next].AmountUSD
		}
	}
	if unanswered.Invocations > 0 {
		reviews = append(reviews, unanswered)
	}
	var inRange []ReviewAccomplishment
	for _, review := range reviews {
		if !review.At.Before(from) && review.At.Before(to) {
			inRange = append(inRange, review)
		}
	}
	return inRange
}

func reviewShares(reviews []ReviewAccomplishment) []OutcomeShare {
	shares := make([]OutcomeShare, 0, len(ReviewOutcomes()))
	for _, outcome := range ReviewOutcomes() {
		share := OutcomeShare{Outcome: string(outcome), Says: outcome.Describe()}
		for _, review := range reviews {
			if review.Outcome == outcome {
				share.Count++
				share.CostUSD += review.CostUSD
			}
		}
		if len(reviews) > 0 {
			share.Share = float64(share.Count) / float64(len(reviews))
		}
		shares = append(shares, share)
	}
	return shares
}

// completedItems is every item whose change merged in the range, with its
// full cost, newest first.
func completedItems(lines []runstate.Spend, runs []runstate.State, passes []*managementPass, from, to time.Time) []CompletedItemCost {
	byItem := map[string][]runstate.State{}
	for _, state := range runs {
		// A run that published a document served no work item: the identifier it
		// carries in the item's place is its own.
		if state.WorkItemID != "" && state.Document == nil {
			byItem[state.WorkItemID] = append(byItem[state.WorkItemID], state)
		}
	}
	management := map[string]float64{}
	managementPasses := map[string]int{}
	for _, pass := range passes {
		if len(pass.Named) == 0 {
			continue
		}
		share := pass.CostUSD / float64(len(pass.Named))
		for _, item := range pass.Named {
			management[item] += share
			managementPasses[item]++
		}
	}
	itemLines := map[string][]runstate.Spend{}
	for _, line := range lines {
		if !ManagementRole(line.Role) && line.WorkItemID != "" {
			itemLines[line.WorkItemID] = append(itemLines[line.WorkItemID], line)
		}
	}
	var completed []CompletedItemCost
	for _, itemID := range deliveryKeys(byItem) {
		states := byItem[itemID]
		promoting, found := runstate.State{}, false
		for _, state := range states {
			if state.Integration == nil {
				continue
			}
			if !found || EndedAt(state).After(EndedAt(promoting)) {
				promoting, found = state, true
			}
		}
		if !found {
			continue
		}
		mergedAt := EndedAt(promoting)
		if mergedAt.Before(from) || !mergedAt.Before(to) {
			continue
		}
		sort.SliceStable(states, func(a, b int) bool { return states[a].StartedAt.Before(states[b].StartedAt) })
		first := states[0].RunID
		item := CompletedItemCost{
			WorkItemID:       itemID,
			Title:            promoting.WorkItemTitle,
			MergedAt:         mergedAt,
			Runs:             len(states),
			ManagementUSD:    management[itemID],
			ManagementPasses: managementPasses[itemID],
		}
		for _, line := range itemLines[itemID] {
			if !line.Known() {
				item.Unpriced++
				continue
			}
			switch {
			case line.Phase == runstate.SpendPhaseReview:
				item.ReviewUSD += line.AmountUSD
			case line.Phase == runstate.SpendPhaseDevelopment && line.RunID == first:
				item.DevelopmentUSD += line.AmountUSD
			default:
				item.RetriesUSD += line.AmountUSD
			}
		}
		item.TotalUSD = item.DevelopmentUSD + item.ReviewUSD + item.RetriesUSD + item.ManagementUSD
		completed = append(completed, item)
	}
	sort.SliceStable(completed, func(a, b int) bool { return completed[a].MergedAt.After(completed[b].MergedAt) })
	return completed
}

func completedMean(items []CompletedItemCost) (float64, float64) {
	total := 0.0
	for _, item := range items {
		total += item.TotalUSD
	}
	if len(items) == 0 {
		return 0, 0
	}
	return total, total / float64(len(items))
}

// roleAccomplishments is the spend of the lines by role, management roles
// first in the order the hierarchy names them, with each management role's
// passes classified.
func roleAccomplishments(lines []runstate.Spend, passes []*managementPass) []RoleAccomplishment {
	byRole := map[domain.AgentRole]*RoleAccomplishment{}
	role := func(name domain.AgentRole) *RoleAccomplishment {
		if byRole[name] == nil {
			byRole[name] = &RoleAccomplishment{Role: name, Management: ManagementRole(name)}
		}
		return byRole[name]
	}
	for _, line := range lines {
		entry := role(line.Role)
		entry.Invocations++
		if line.Known() {
			entry.CostUSD += line.AmountUSD
		} else {
			entry.Unpriced++
		}
	}
	for _, pass := range passes {
		role(pass.Role).Passes++
	}
	for name, entry := range byRole {
		if !entry.Management {
			continue
		}
		for _, outcome := range PassOutcomes() {
			share := OutcomeShare{Outcome: string(outcome), Says: outcome.Describe()}
			for _, pass := range passes {
				if pass.Role == name && pass.Outcome == outcome {
					share.Count++
					share.CostUSD += pass.CostUSD
				}
			}
			if entry.Passes > 0 {
				share.Share = float64(share.Count) / float64(entry.Passes)
			}
			entry.Outcomes = append(entry.Outcomes, share)
		}
	}
	order := map[domain.AgentRole]int{
		domain.RoleProductManager:     0,
		domain.RoleDevelopmentManager: 1,
		domain.RoleProgramManager:     2,
		domain.RoleArchitect:          3,
		domain.RoleDeveloper:          5,
		domain.RoleReviewer:           6,
	}
	rank := func(name domain.AgentRole) int {
		if at, ok := order[name]; ok {
			return at
		}
		return 4
	}
	roles := make([]RoleAccomplishment, 0, len(byRole))
	for _, entry := range byRole {
		roles = append(roles, *entry)
	}
	sort.SliceStable(roles, func(a, b int) bool {
		if rank(roles[a].Role) != rank(roles[b].Role) {
			return rank(roles[a].Role) < rank(roles[b].Role)
		}
		return roles[a].Role < roles[b].Role
	})
	return roles
}

func setKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
