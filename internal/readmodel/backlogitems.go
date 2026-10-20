package readmodel

// Every work item the tracker holds, open or closed, for the page that charts
// the backlog over time.
//
// The standing answers where the queue stands now; this answers how it got
// there: when each item was filed and closed, at what priority, under which
// labels, who does it, who asked for it, and whether it is ready or waiting.
// The page draws filed, open, and closed series from it and splits them by any
// of those fields, so every field is a value a chart can group by and none is
// a sentence it would have to parse.
//
// Nothing here is a second derivation. Whether an admitted item is ready, and
// what it waits on where it is not, is the queue's own answer — readQueue and
// backlog.Order, the reading the standing's not-startable line is worded from —
// with the two per-item refusals the standing adds on top of it: children that
// already carry the work, and an unresolved directive pausing it. The times are
// the item's own record, never its dependency records. The origin is the one
// the admission recorded, and an item admitted before origins were recorded
// says so rather than having one guessed for it.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// BacklogState is where one work item stands, in the closed set the page
// splits by.
type BacklogState string

const (
	// BacklogReady is admitted work nothing is holding back: the next pull can
	// take it.
	BacklogReady BacklogState = "ready"
	// BacklogWaiting is admitted work something holds back, and the item says
	// what in WaitingKind and WaitingOn.
	BacklogWaiting BacklogState = "waiting"
	// BacklogInProgress is work somebody has claimed and is carrying.
	BacklogInProgress BacklogState = "in progress"
	// BacklogClosed is work that has left the backlog.
	BacklogClosed BacklogState = "closed"
	// BacklogNotRead is admitted work whose readiness could not be read; the
	// reading's ReadinessProblem says why. It is never reported as ready or as
	// waiting, because either would be a guess.
	BacklogNotRead BacklogState = "not read"
)

// OriginNotRecorded is the filed-by value of an item whose admission recorded
// no origin, which is every item admitted before origins were recorded.
const OriginNotRecorded = "not recorded"

// DoneByDeveloperRun is the done-by value of ordinary work, which a developer
// run carries out.
const DoneByDeveloperRun = "developer run"

// DoneByUnnamedConversation is the done-by value of work a conversation
// carries whose marker names no role, as work marked before markers named one
// does.
const DoneByUnnamedConversation = "conversation, role not named"

// Backlog is the reading: every work item the tracker holds, and what could
// not be read. It never fails as a whole: a tracker that cannot be listed
// costs the items, saying so in TrackerProblem, and readiness that cannot be
// read costs each admitted item its state, saying so in ReadinessProblem.
type Backlog struct {
	// ObservedAt is when the reading was taken.
	ObservedAt time.Time `json:"observed_at"`
	// Items is every work item the tracker holds, open and closed, oldest filed
	// first. It is nil where the tracker could not be listed, and empty rather
	// than absent where it holds nothing.
	Items []BacklogItem `json:"items"`
	// TrackerProblem is why the tracker could not be listed, and empty where it
	// was.
	TrackerProblem string `json:"tracker_problem,omitempty"`
	// ReadinessProblem is what could not be read about whether admitted work is
	// ready: the tracker's own ready listing, the holds after stopped runs, the
	// human gates a person has passed, or the recorded directives. Empty where
	// all of it was read.
	ReadinessProblem string `json:"readiness_problem,omitempty"`
}

// BacklogItem is one work item as the backlog page charts it.
type BacklogItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Status is the tracker's own status word.
	Status string `json:"status"`
	// FiledAt is when the tracker recorded the item, and ClosedAt when it
	// recorded it closed. Each is null where the tracker did not say, and
	// ClosedAt is null on work that is not closed.
	FiledAt  *time.Time `json:"filed_at"`
	ClosedAt *time.Time `json:"closed_at"`
	Priority int        `json:"priority"`
	// Labels are the tracker's own labels, empty rather than absent on an item
	// carrying none.
	Labels []string `json:"labels"`
	// Kind is the tracker's kind for the item — feature, bug, task — and empty
	// where it records none.
	Kind string `json:"kind"`
	// Parent is the item this one was split from, and empty where it was filed
	// on its own.
	Parent string `json:"parent,omitempty"`
	// DoneBy is who carries the work: DoneByDeveloperRun, the role whose
	// conversation carries it, or DoneByUnnamedConversation. Executor is the
	// marker it is read from, and empty for a developer run.
	DoneBy   string                  `json:"done_by"`
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
	// State is where the item stands. WaitingKind and WaitingOn say what holds
	// a waiting item back — the pile, from the queue's own vocabulary, and the
	// refusal in the words the standing uses for it — and are empty otherwise.
	State       BacklogState     `json:"state"`
	WaitingKind backlog.HoldKind `json:"waiting_kind,omitempty"`
	WaitingOn   string           `json:"waiting_on,omitempty"`
	// Parked is why the item is deliberately not to be pulled, and empty for
	// work that is not parked.
	Parked string `json:"parked,omitempty"`
	// Origin is who filed the item, as the admission recorded it.
	Origin BacklogOrigin `json:"origin"`
}

// BacklogOrigin is who asked for an item, as its admission recorded it.
type BacklogOrigin struct {
	// Recorded says whether the admission recorded an origin at all.
	Recorded bool `json:"recorded"`
	// FiledBy is who asked, as one value a chart splits by — the operator, the
	// harness, or the role that asked — and OriginNotRecorded where nothing was
	// recorded. It is never guessed from the tracker's author field, which names
	// whoever's identity the harness runs under.
	FiledBy string `json:"filed_by"`
	// OnBehalfOf is whom the work was admitted for, and empty where nothing was
	// recorded.
	OnBehalfOf string `json:"on_behalf_of,omitempty"`
	// AdmittedBy is the role that put the item in the backlog, and Directive
	// the operator's directive the admission answers, each empty where none.
	AdmittedBy domain.AgentRole `json:"admitted_by,omitempty"`
	Directive  string           `json:"directive,omitempty"`
	// Words is the origin in a sentence, as the item's own reading says it.
	Words string `json:"words"`
}

// ReadBacklog reads every work item the tracker holds, with the standing's own
// account of which admitted ones are ready. It writes nothing.
func ReadBacklog(ctx context.Context, sources Sources) Backlog {
	now := sources.now()
	reading := Backlog{ObservedAt: now}
	if sources.Tracker == nil {
		reading.TrackerProblem = "nothing was wired to read the tracker"
		return reading
	}
	// One listing of every item and one readiness query, shared with whatever
	// else is reading the tracker (sharedlisting.go).
	sources, shared := sources.sharing()
	shared.start(ctx)
	defer shared.finish()

	every, err := sources.list(ctx, "")
	if err != nil {
		reading.TrackerProblem = fmt.Sprintf("the work items could not be listed: %v", err)
		return reading
	}

	entries, readinessProblem := readBacklogReadiness(ctx, sources)
	reading.ReadinessProblem = readinessProblem

	reading.Items = make([]BacklogItem, 0, len(every))
	for _, item := range every {
		reading.Items = append(reading.Items, backlogItem(item, entries))
	}
	sort.SliceStable(reading.Items, func(i, j int) bool {
		left, right := reading.Items[i].FiledAt, reading.Items[j].FiledAt
		switch {
		case left == nil || right == nil:
			// An item whose filing time the tracker did not give goes after the
			// ones it did, rather than being read as the oldest of all.
			return left != nil && right == nil
		case !left.Equal(*right):
			return left.Before(*right)
		default:
			return reading.Items[i].ID < reading.Items[j].ID
		}
	})
	return reading
}

// backlogReadiness is what the queue says of one admitted item.
type backlogReadiness struct {
	state  BacklogState
	kind   backlog.HoldKind
	reason string
}

// readBacklogReadiness reads the queue and says, per admitted item, whether it
// is ready and what holds it where it is not. A nil map is the queue not
// having been read, and the problem says why.
func readBacklogReadiness(ctx context.Context, sources Sources) (map[string]backlogReadiness, string) {
	queue, coverage, gateProblem, err := readQueue(ctx, sources)
	if err != nil {
		return nil, fmt.Sprintf("the admitted work's readiness could not be read: %v", err)
	}
	pausing, directiveProblem := readPausingDirectives(sources)
	problem := joinProblems(gateProblem, directiveProblem)

	entries := make(map[string]backlogReadiness, len(queue.Entries))
	for _, entry := range queue.Entries {
		covering := coverage.Covering(entry.ID)
		paused := pausedBy(pausing, entry.ID)
		switch {
		case !entry.Ready:
			entries[entry.ID] = backlogReadiness{state: BacklogWaiting, kind: entry.HoldKind(), reason: entry.Hold()}
		case len(covering) > 0:
			entries[entry.ID] = backlogReadiness{state: BacklogWaiting, kind: backlog.HeldCovered, reason: backlog.CoveredReason(covering)}
		case paused != nil:
			entries[entry.ID] = backlogReadiness{state: BacklogWaiting, kind: backlog.HeldByDirective,
				reason: fmt.Sprintf("paused for unresolved directive %s: %s", paused.ID, singleLine(paused.Unresolved, maxRefusalBytes))}
		default:
			entries[entry.ID] = backlogReadiness{state: BacklogReady}
		}
	}
	return entries, problem
}

// readPausingDirectives is the unresolved directives that pause work, read as
// the standing's switches read them.
func readPausingDirectives(sources Sources) ([]directive.Directive, string) {
	if sources.Directives == nil {
		return nil, "nothing was wired to read the recorded directives, so no item is shown as paused by one"
	}
	recorded, err := sources.Directives.List()
	if err != nil {
		return nil, fmt.Sprintf("the recorded directives could not be read, so no item is shown as paused by one: %v", err)
	}
	var pausing []directive.Directive
	for _, given := range recorded {
		if given.Pauses() {
			pausing = append(pausing, given)
		}
	}
	return pausing, ""
}

// backlogItem is one tracker item as the page charts it.
func backlogItem(item beads.WorkItem, entries map[string]backlogReadiness) BacklogItem {
	labels := append([]string{}, item.Labels...)
	read := BacklogItem{
		ID:       item.ID,
		Title:    item.Title,
		Status:   item.Status,
		FiledAt:  recordedTime(item.CreatedAt),
		Priority: item.Priority,
		Labels:   labels,
		Kind:     item.IssueType,
		Parent:   item.Parent,
		DoneBy:   doneBy(item.Executor),
		Executor: item.Executor,
		Parked:   item.Parking.Reason(),
		Origin:   backlogOrigin(item.Origin),
	}
	switch {
	case item.Status == "closed":
		read.State = BacklogClosed
		read.ClosedAt = recordedTime(item.ClosedAt)
	case item.Status == backlog.StatusClaimed:
		read.State = BacklogInProgress
	case isAdmittedStatus(item.Status):
		if entries == nil {
			read.State = BacklogNotRead
			break
		}
		readiness, found := entries[item.ID]
		if !found {
			// The queue is read out of the same listing, so this is an item the
			// listing changed under; say so rather than guess.
			read.State = BacklogNotRead
			break
		}
		read.State, read.WaitingKind, read.WaitingOn = readiness.state, readiness.kind, readiness.reason
	default:
		read.State = BacklogWaiting
		read.WaitingOn = fmt.Sprintf("its tracker status is %q, which the backlog does not pull from", item.Status)
	}
	return read
}

func isAdmittedStatus(status string) bool {
	for _, admitted := range backlogStatuses {
		if status == admitted {
			return true
		}
	}
	return false
}

// recordedTime is a time the tracker recorded, or nil where it recorded none.
func recordedTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

// doneBy is who carries an item's work, from its executor marker.
func doneBy(executor domain.WorkItemExecutor) string {
	if executor.DeveloperRun() {
		return DoneByDeveloperRun
	}
	if role := executor.Role(); role != "" {
		return string(role)
	}
	return DoneByUnnamedConversation
}

// backlogOrigin is an item's recorded origin, with an unrecorded one said as
// such.
func backlogOrigin(origin domain.WorkItemOrigin) BacklogOrigin {
	origin = origin.Trimmed()
	if !origin.Known() {
		return BacklogOrigin{FiledBy: OriginNotRecorded, Words: origin.Describe()}
	}
	filedBy := origin.AskedBy()
	if strings.TrimSpace(filedBy) == "" {
		// An asker the harness does not recognise is still what was recorded.
		filedBy = string(origin.Asker)
	}
	return BacklogOrigin{
		Recorded:   true,
		FiledBy:    filedBy,
		OnBehalfOf: origin.OnBehalfOf(),
		AdmittedBy: origin.AdmittedBy,
		Directive:  origin.Directive,
		Words:      origin.Describe(),
	}
}
