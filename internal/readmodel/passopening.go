package readmodel

// What a program manager's pass opens with, and the one named query a reply may
// ask for in full.
//
// "The fixed capability set" in docs/designs/program-manager.md gives the role
// `readmodel.read`, delivered two ways. Every pass opens with the standing, the
// throughput windows, the capacity state, the docket as counts, the reports
// pile as counts, and a line per other instance; and a bounded block in a reply
// asks for one named query in full. Both are read here, from the one read model
// every operator surface projects, and from nowhere else: a program manager
// that wants what the channel said reads the record the channel was rendered
// from, never the channel.
//
// The opening is counts on purpose. It is carried on every pass, and a pass
// that wants the entries behind a count asks for the query by name.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// PassOpening is the six things a program manager's pass opens with, each
// already said as the pass is told it, and the instances the last of them is
// read from. Every part says why where its records could not be read, rather
// than reporting nothing.
type PassOpening struct {
	Standing   string
	Throughput string
	Capacity   string
	Docket     string
	Reports    string
	// Instances is every program manager instance as the standing carries it;
	// the line per other instance is rendered from it for whichever instance is
	// being woken.
	Instances        []ProgramManager
	InstancesProblem string
}

// ReadPassOpening reads the opening from one standing reading, one throughput
// reading, and the docket.
func ReadPassOpening(ctx context.Context, sources Sources, throughput ThroughputSources) PassOpening {
	standing := ReadStanding(ctx, sources)
	return PassOpening{
		Standing:         strings.TrimSpace(standing.RenderBrief()),
		Throughput:       describeThroughput(ReadThroughput(ctx, throughput)),
		Capacity:         describeCapacity(CapacityStandingOf(standing, sources.Capacity)),
		Docket:           ReadDocketCounts(ctx, sources).Describe(),
		Reports:          describePile(standing),
		Instances:        standing.ProgramManagers,
		InstancesProblem: standing.ProgramManagersProblem,
	}
}

// Render is the opening as the instance named is told it: the six parts under
// their headings, with a line for every instance but that one.
func (o PassOpening) Render(agent string) string {
	var rendered strings.Builder
	rendered.WriteString("# Where the product stands, from the read model\n\n")
	rendered.WriteString("Read as this pass was taken, from the one read model every operator surface is projected from. It is counts rather than entries; ask for a query by name, below, where you need what is behind a count.\n\n")
	section := func(heading, body string) {
		fmt.Fprintf(&rendered, "## %s\n\n%s\n\n", heading, strings.TrimSpace(body))
	}
	section("Standing", o.Standing)
	section("Throughput", o.Throughput)
	section("Capacity", o.Capacity)
	section("Docket", o.Docket)
	section("Reports", o.Reports)
	section("The other program managers", o.otherInstances(agent))
	return strings.TrimSpace(rendered.String())
}

// otherInstances is one line per instance other than the one woken: its name,
// its lane, its status, and its open requests by id.
func (o PassOpening) otherInstances(agent string) string {
	var lines []string
	for _, instance := range o.Instances {
		if instance.Agent == agent {
			continue
		}
		lines = append(lines, "- "+instance.OpeningLine())
	}
	if len(lines) == 0 {
		lines = append(lines, "No other program manager instance is configured.")
	}
	if o.InstancesProblem != "" {
		lines = append(lines, "Not everything behind these lines could be read: "+o.InstancesProblem)
	}
	return strings.Join(lines, "\n")
}

// OpeningLine is the instance as another instance's pass names it.
func (p ProgramManager) OpeningLine() string {
	lane := "no lane configured"
	if p.Lane != "" {
		lane = "lane " + p.Lane
	}
	open := "no open requests"
	if len(p.OpenRequests) > 0 {
		open = "open requests: " + strings.Join(p.OpenRequests, ", ")
	}
	return fmt.Sprintf("%s — %s — %s — %s", p.Agent, lane, p.Status, open)
}

// describeThroughput is the two windows as counts.
func describeThroughput(reading Throughput) string {
	if reading.RunsProblem != "" {
		return "The run records could not be read, so nothing is counted: " + reading.RunsProblem
	}
	lines := make([]string, 0, len(reading.Windows))
	for _, window := range reading.Windows {
		lines = append(lines, fmt.Sprintf("- %s (since %s): %d started; %d landed, %d succeeded without landing, %d stopped, %d cancelled, %d timed out, %d failed",
			window.Label, window.Since, window.Started, window.Landed, window.Succeeded, window.Stopped, window.Cancelled, window.TimedOut, window.Failed))
	}
	return strings.Join(lines, "\n")
}

// CapacityStanding is what the harness has to run work on: its developer slots
// and what fills them, and everything the provider is holding. It is the
// standing's own capacity fields gathered into one query, so the pass and the
// dashboard's capacity panel read one derivation.
type CapacityStanding struct {
	ObservedAt time.Time `json:"observed_at"`
	// DeveloperSlots is execution.max_concurrent_developers, and Running how many
	// developer runs are in flight against it. RunningProblem says the runs could
	// not be read, in which case Running is not a count.
	DeveloperSlots int    `json:"developer_slots"`
	Running        int    `json:"running"`
	RunningProblem string `json:"running_problem,omitempty"`
	// WaitingForSlot is the ready work waiting only for a slot.
	WaitingForSlot *SlotWait `json:"waiting_for_slot,omitempty"`
	// Paused is the banner the standing opens with where the provider is holding
	// the harness, and the three after it are what it is said from.
	Paused         string                   `json:"paused,omitempty"`
	Hold           *CapacityHold            `json:"capacity_hold,omitempty"`
	ProviderOutage *runstate.ProviderOutage `json:"provider_outage,omitempty"`
	Blocked        CapacityBlocked          `json:"capacity_blocked"`
}

// CapacityOf is the capacity state a standing reading carries.
func CapacityStandingOf(standing Standing, slots int) CapacityStanding {
	return CapacityStanding{
		ObservedAt:     standing.ObservedAt,
		DeveloperSlots: slots,
		Running:        len(standing.Running),
		RunningProblem: standing.RunningProblem,
		WaitingForSlot: standing.WaitingForSlot,
		Paused:         standing.Paused,
		Hold:           standing.CapacityHold,
		ProviderOutage: standing.ProviderOutage,
		Blocked:        standing.CapacityBlocked,
	}
}

func describeCapacity(state CapacityStanding) string {
	var lines []string
	if state.RunningProblem != "" {
		lines = append(lines, fmt.Sprintf("- Developer slots: %d configured; the runs in flight could not be read: %s", state.DeveloperSlots, state.RunningProblem))
	} else {
		line := fmt.Sprintf("- Developer slots: %d of %d in use", state.Running, state.DeveloperSlots)
		if state.WaitingForSlot != nil {
			line += "; ready work is waiting for a slot"
		}
		lines = append(lines, line)
	}
	if state.Paused != "" {
		lines = append(lines, "- The provider is holding the harness: "+strings.Join(strings.Fields(state.Paused), " "))
	} else {
		lines = append(lines, "- The provider is holding nothing across every role, and is answering.")
	}
	blocked := fmt.Sprintf("- Parked or held on provider capacity: %s and %s", count(len(state.Blocked.Runs), "run"), count(len(state.Blocked.Conversations), "conversation turn"))
	for _, problem := range []string{state.Blocked.RunsProblem, state.Blocked.ConversationsProblem} {
		if problem != "" {
			blocked += "; not all of it could be read: " + problem
		}
	}
	lines = append(lines, blocked)
	return strings.Join(lines, "\n")
}

// describePile is the reports pile as counts, and by severity.
func describePile(standing Standing) string {
	if standing.ReportsProblem != "" {
		return "The reports pile could not be read, so it is not counted: " + standing.ReportsProblem
	}
	return standing.Reports.Describe() + "."
}

// DocketCounts is the triage docket as counts: how many stopped runs are
// waiting on the development manager, how many of them jump the walk, how long
// the oldest has waited, and how many entries are on work that has closed.
type DocketCounts struct {
	ObservedAt time.Time `json:"observed_at"`
	Live       int       `json:"live"`
	Critical   int       `json:"critical"`
	// Oldest is when the oldest live stoppage was first docketed, and zero where
	// none is live.
	Oldest time.Time `json:"oldest,omitempty"`
	// Dead is entries on work the tracker holds as closed.
	Dead int `json:"dead"`
	// Stoppages is the live docket, oldest first, and is carried by the query
	// rather than by the opening.
	Stoppages []triage.Stoppage `json:"stoppages,omitempty"`
	// Unread is a docket that could not be read at all, which Problem says why;
	// the counts are then nothing rather than zero.
	Unread  bool   `json:"unread,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// ReadDocketCounts reads the docket as the development manager's window reads
// it: the live entries, one per stopped run, with entries on closed work set
// aside where the tracker says which work is closed.
func ReadDocketCounts(ctx context.Context, sources Sources) DocketCounts {
	counts := DocketCounts{ObservedAt: sources.now()}
	if sources.Docket == nil {
		counts.Unread, counts.Problem = true, "nothing was wired to read the triage docket"
		return counts
	}
	entries, err := sources.Docket.List()
	if err != nil {
		counts.Unread, counts.Problem = true, fmt.Sprintf("the triage docket could not be read: %v", err)
		return counts
	}
	var closed func(string) bool
	if sources.Tracker != nil {
		items, err := sources.list(ctx, "closed")
		if err != nil {
			counts.Problem = fmt.Sprintf("the tracker could not say which work is closed, so no entry is set aside as dead: %v", err)
		} else {
			finished := make(map[string]bool, len(items))
			for _, item := range items {
				finished[item.ID] = true
			}
			closed = func(id string) bool { return finished[id] }
		}
	}
	live := triage.Live(entries, closed, counts.ObservedAt)
	counts.Live = len(live.Stoppages)
	counts.Dead = live.Dead
	counts.Stoppages = live.Stoppages
	for _, stoppage := range live.Stoppages {
		if stoppage.Critical() {
			counts.Critical++
		}
		if counts.Oldest.IsZero() || stoppage.Since.Before(counts.Oldest) {
			counts.Oldest = stoppage.Since
		}
	}
	return counts
}

// Describe is the counts in a sentence.
func (c DocketCounts) Describe() string {
	var said string
	switch {
	case c.Unread:
		return "The triage docket could not be read, so it is not counted: " + c.Problem
	case c.Live == 0:
		said = "No stopped run is waiting on the development manager"
	default:
		said = fmt.Sprintf("%s waiting on the development manager, %d of them critical, the oldest docketed %s",
			count(c.Live, "stopped run"), c.Critical, agoSaid(c.ObservedAt.Sub(c.Oldest)))
	}
	if c.Dead > 0 {
		said += fmt.Sprintf("; %s on work that has closed", count(c.Dead, "further entry"))
	}
	said += "."
	if c.Problem != "" {
		said += " " + c.Problem + "."
	}
	return said
}

// The queries a reply may ask for by name, each returned whole.
const (
	QueryStanding        = "standing"
	QueryThroughput      = "throughput"
	QueryCapacity        = "capacity"
	QueryDocket          = "docket"
	QueryReports         = "reports"
	QueryProgramManagers = "program-managers"
	QueryLaneReport      = "lane-report"
)

// Queries is every query a reply may name, in the order they are listed to it.
func Queries() []string {
	return []string{QueryStanding, QueryThroughput, QueryCapacity, QueryDocket, QueryReports, QueryProgramManagers, QueryLaneReport}
}

// KnownQuery reports whether a name is one of Queries.
func KnownQuery(name string) bool {
	for _, known := range Queries() {
		if known == name {
			return true
		}
	}
	return false
}

// QueryTakesAgent reports whether a query is about one instance, and so names
// the agent it is about.
func QueryTakesAgent(name string) bool {
	return name == QueryLaneReport
}

// MaxQueryResultBytes bounds what one query returns. It is the size of a
// prompt that is bounded rather than the query: a result larger than this is
// cut, with the cut declared and the whole size named.
const MaxQueryResultBytes = 96 << 10

// ErrUnknownQuery is a name that is not one of Queries.
var ErrUnknownQuery = errors.New("no read-model query is recorded under that name")

// QueryResult is one query's answer: the record as the read model carries it,
// as JSON, and whether it was cut.
type QueryResult struct {
	Query string
	Agent string
	JSON  string
	// Cut is set where the answer was longer than MaxQueryResultBytes, and
	// WholeBytes is then how long it was.
	Cut        bool
	WholeBytes int
}

// Query answers one named query in full, as the read model's own record.
func Query(ctx context.Context, sources Sources, throughput ThroughputSources, name, agent string) (QueryResult, error) {
	if !KnownQuery(name) {
		return QueryResult{}, fmt.Errorf("%w: %q", ErrUnknownQuery, name)
	}
	var answer any
	switch name {
	case QueryStanding:
		answer = ReadStanding(ctx, sources)
	case QueryThroughput:
		answer = ReadThroughput(ctx, throughput)
	case QueryCapacity:
		answer = CapacityStandingOf(ReadStanding(ctx, sources), sources.Capacity)
	case QueryDocket:
		answer = ReadDocketCounts(ctx, sources)
	case QueryReports:
		answer = readUnhandledPile(sources)
	case QueryProgramManagers:
		instances, problem := ReadProgramManagers(sources)
		answer = struct {
			ProgramManagers []ProgramManager `json:"program_managers"`
			Problem         string           `json:"problem,omitempty"`
		}{instances, problem}
	case QueryLaneReport:
		reportAnswer, err := ReadProgramManagerReport(sources, agent)
		if err != nil {
			return QueryResult{}, err
		}
		answer = reportAnswer
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return QueryResult{}, fmt.Errorf("encode the %s query: %w", name, err)
	}
	result := QueryResult{Query: name, Agent: agent, JSON: string(encoded), WholeBytes: len(encoded)}
	if len(encoded) > MaxQueryResultBytes {
		cut := MaxQueryResultBytes
		for cut > 0 && !utf8.RuneStart(encoded[cut]) {
			cut--
		}
		result.JSON = string(encoded[:cut])
		result.Cut = true
	}
	return result, nil
}

// UnhandledPile is the reports nobody has decided about, oldest filed first,
// and how the pile stands.
type UnhandledPile struct {
	Pile       report.Pile     `json:"pile"`
	Unhandled  []report.Report `json:"unhandled"`
	Problem    string          `json:"problem,omitempty"`
	ObservedAt time.Time       `json:"observed_at"`
}

func readUnhandledPile(sources Sources) UnhandledPile {
	now := sources.now()
	reports, handlings, problem := readPile(sources)
	pile, problem := summarizePile(reports, handlings, problem, now)
	answer := UnhandledPile{Pile: pile, Problem: problem, ObservedAt: now, Unhandled: []report.Report{}}
	if problem == "" {
		answer.Unhandled = report.ByFiling(report.Unhandled(reports, handlings))
	}
	return answer
}

// QueryList is every query a block may name, as a sentence lists them: each in
// quotes, and the one that is about an instance saying so.
func QueryList() string {
	quoted := make([]string, 0, len(Queries()))
	for _, name := range Queries() {
		if QueryTakesAgent(name) {
			quoted = append(quoted, fmt.Sprintf("%q (with \"agent\")", name))
			continue
		}
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return strings.Join(quoted, ", ")
}
