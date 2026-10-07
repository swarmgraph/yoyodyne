package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

const defaultTimeout = 30 * time.Second

// MaxPriority is the lowest Beads priority, and 0 is the highest. It is exported
// so a caller can refuse a priority it was handed rather than discovering the
// problem as a bd failure.
const MaxPriority = 4

type WorkItem struct {
	// RelevantGoals names the goals this change must not break, beside the goal it serves.
	RelevantGoals []string

	ID                 string
	Title              string
	Description        string
	Design             string
	AcceptanceCriteria string
	Notes              string
	Status             string
	Priority           int
	IssueType          string
	Assignee           string
	Parent             string
	Dependencies       []Dependency
	// CreatedAt is when the tracker recorded this item, which is what says what
	// the work was admitted knowing. It is the zero time where the tracker did not
	// say, and a caller that needs it reports that it does not know rather than
	// treating an unknown admission time as the beginning of time.
	CreatedAt time.Time
	// Cost is what the runs made for this item have cost, as the tracker holds
	// it. It is absent from an item nothing has ever priced, which is a different
	// fact from an item that cost nothing.
	Cost *Cost
	// GoalWitness is what the tracker records, outside this item's notes, about a
	// goal having been written into them. It is kept there because notes are what
	// a careless writer replaces: an item whose notes lost their goal still
	// carries this, which is what tells a destroyed attribution from one nobody
	// ever made and what says which goal to put back.
	GoalWitness goal.Witness
	// Executor is what carries this item's execution, where it is not a developer
	// run. It is empty for ordinary work, which is what an item that names none
	// is; see domain.WorkItemExecutor for why the ordinary case is the silent one.
	//
	// It is read from the tracker's own metadata rather than from the notes,
	// because selection reads it: a marker in prose is a marker the next writer
	// replaces, and the whole point of this one is that nothing chooses the item
	// after it is set.
	Executor domain.WorkItemExecutor
	// Parking is why this item is deliberately not to be pulled, and is empty for
	// the queued work that is. It is metadata for the same reason the executor is,
	// and it is a different question from the executor: this one says the work is
	// not to be started now, not that no run could start it.
	Parking domain.WorkItemParking
	// Labels are the tracker's own labels on the item, in the order bd lists
	// them, and nil for an item carrying none. They are bd's native field rather
	// than harness metadata, so whatever else reads the tracker — bd's own
	// listing filters first among it — reads the same labels the harness wrote.
	Labels []string
	// Landing is the revision the harness closed this item on, where it closed
	// one that a conversation carries: the document and the revision's time, as
	// RecordLanding wrote them. It is empty for everything else. It is what
	// keeps that close from being made twice — an item somebody reopened after
	// it still carries the landing it was closed on, and the sweep that reads
	// the same revision again reads this and leaves the item open.
	Landing string
	// Origin is who asked for this item to be admitted, and on whose behalf, as
	// the admission recorded it. It is the zero origin on every item admitted
	// before origins were recorded, which reads as unknown rather than as the
	// operator the tracker's own author field would name.
	Origin domain.WorkItemOrigin
}

// Cost is the provider-reported price of every run made for one work item. It
// is carried by the tracker itself rather than assembled beside it, so the
// briefing, the conversation, and bd all read one number from one place.
//
// UnknownRuns is what keeps that number honest. A run whose evidence no longer
// survives cannot be priced, and pricing it as nothing would understate every
// total it entered; while it is non-zero, TotalUSD is a floor on what the item
// cost rather than what it cost.
type Cost struct {
	TotalUSD    float64 `json:"total_usd"`
	Runs        int     `json:"runs"`
	UnknownRuns int     `json:"unknown_runs,omitempty"`
}

// The tracker metadata keys the price is carried in. They are namespaced
// because the metadata is the project's own and Yoyodyne is a guest in it.
const (
	costTotalKey   = "yoyodyne_cost_usd"
	costRunsKey    = "yoyodyne_cost_runs"
	costUnknownKey = "yoyodyne_cost_unknown_runs"
)

// goalWitnessKey is where the tracker records the goal that was written onto an
// item. The notes remain where an attribution is made and read from; this is a
// copy kept where replacing those notes cannot reach it, so a destroyed
// attribution can be told from one nobody made and put back from the words
// rather than judged again.
const goalWitnessKey = "yoyodyne_goal_recorded"

// executorKey is where the tracker records what carries an item's execution.
// Like the goal witness it is metadata rather than notes, and for a sharper
// reason: this one is read by selection, so a marker the next writer of the
// notes could replace would be a marker that stops working exactly when
// somebody records an outcome on the item.
const executorKey = "yoyodyne_executor"

// parkedKey is where the tracker records that an item is parked, and why. It is
// metadata for the reason the executor is — selection reads it, and notes are
// what the next writer replaces — and it is the whole of the parking rather than
// a flag beside a reason kept elsewhere: the reason is what says whether
// releasing the work is right, and a marker that outlived its reason would be
// back to a parking nobody can account for.
//
// The key is absent from work that is not parked, and released work carries it
// with nothing in it. Both read as unparked, which is what lets a release be
// written the same way everything else here is written — one value set on the
// item — rather than needing the tracker to forget a key.
const parkedKey = "yoyodyne_parked"

// LandingKey is where the tracker records the revision the harness closed a
// conversation-carried item on. It is metadata for the reason the parking is:
// the sweep that closes reads it, and a reopen appends a note without touching
// it, which is exactly what lets a reopened item stay open. It is exported so
// a problem the sweep reports can name what a person would have to clear.
const LandingKey = "yoyodyne_landed"

// The tracker metadata keys an admission's origin is carried in, one fact to a
// key so a listing can split the backlog by any of them. They are written in the
// same write as the admission and never afterwards, except by the one-time
// backfill from an item's own notes; see domain.WorkItemOrigin for what each
// one means. The asked-by and on-behalf-of keys are derived from the others and
// stored anyway, because they are the values a page splits by and a reader of
// the tracker should not have to know how to derive them.
const (
	originAskerKey      = "yoyodyne_origin"
	originAskedByKey    = "yoyodyne_origin_asked_by"
	originOnBehalfOfKey = "yoyodyne_origin_on_behalf_of"
	originAdmittedByKey = "yoyodyne_origin_admitted_by"
	originReportKey     = "yoyodyne_origin_report"
	originReportedByKey = "yoyodyne_origin_reported_by"
	originDirectiveKey  = "yoyodyne_origin_directive"
)

// originEntries is the metadata an origin is written as. A key whose value is
// empty is left out rather than written blank, so an origin with no report and
// no directive carries no report or directive key at all.
func originEntries(origin domain.WorkItemOrigin) map[string]string {
	entries := map[string]string{
		originAskerKey:      string(origin.Asker),
		originAskedByKey:    origin.AskedBy(),
		originOnBehalfOfKey: origin.OnBehalfOf(),
		originAdmittedByKey: string(origin.AdmittedBy),
		originReportKey:     strings.TrimSpace(origin.Report),
		originReportedByKey: string(origin.ReportedBy),
		originDirectiveKey:  strings.TrimSpace(origin.Directive),
	}
	for key, value := range entries {
		if value == "" {
			delete(entries, key)
		}
	}
	return entries
}

// originIn reads the origin the tracker records. Only the stored facts are read;
// the derived asked-by and on-behalf-of keys are worked out again from them, so
// a hand edit to one of those cannot make the item say two things.
func originIn(metadata map[string]json.RawMessage) domain.WorkItemOrigin {
	return domain.WorkItemOrigin{
		Asker:      domain.WorkItemAsker(metadataString(metadata, originAskerKey)),
		AdmittedBy: domain.AgentRole(metadataString(metadata, originAdmittedByKey)),
		Report:     metadataString(metadata, originReportKey),
		ReportedBy: domain.AgentRole(metadataString(metadata, originReportedByKey)),
		Directive:  metadataString(metadata, originDirectiveKey),
	}
}

// witnessValue is what the witness holds for one goal: the statement itself
// where it fits, and a bare "1" where it does not. The bound is the one a goals
// document is already held to, so anything a goal can actually be stated in is
// carried whole; something longer is witnessed without its words rather than
// stored truncated, because a statement cut in half is not the goal and would
// be put back as if it were.
func witnessValue(statement string) string {
	trimmed := strings.TrimSpace(statement)
	if trimmed == "" || len(trimmed) > goal.MaxStatementBytes {
		return "1"
	}
	return trimmed
}

// creationMetadata renders the metadata a creation carries. bd takes an item's
// whole metadata as one JSON object at creation — unlike an update, which sets
// one key — so this flag owns every key the created item will have, and any
// future key has to be added to the map here rather than as a second
// --metadata that would replace this one.
func creationMetadata(entries map[string]string) (string, error) {
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("encode bd metadata: %w", err)
	}
	return string(encoded), nil
}

// costPrecision is how many decimal places a recorded price keeps. A single
// invocation can cost fractions of a cent, and a ledger that rounded each item
// to the cent would drift from the invocations it was summed from.
const costPrecision = 6

// Validate rejects a price that could not describe real spending.
func (c Cost) Validate() error {
	var problems []error
	if math.IsNaN(c.TotalUSD) || math.IsInf(c.TotalUSD, 0) {
		problems = append(problems, errors.New("total cost is not a number"))
	} else if c.TotalUSD < 0 {
		problems = append(problems, fmt.Errorf("total cost %v cannot be negative", c.TotalUSD))
	}
	if c.Runs < 1 {
		problems = append(problems, errors.New("a recorded price must cover at least one run"))
	}
	if c.UnknownRuns < 0 {
		problems = append(problems, errors.New("unknown runs cannot be negative"))
	}
	if c.UnknownRuns > c.Runs {
		problems = append(problems, fmt.Errorf("%d of %d runs cannot be unknown", c.UnknownRuns, c.Runs))
	}
	return errors.Join(problems...)
}

// Complete reports a price no unpriceable run is missing from.
func (c Cost) Complete() bool { return c.UnknownRuns == 0 }

type Dependency struct {
	// IssueID is the item the tracker attributes this edge to, where it says. It
	// is what tells an edge pointing at an item's parent from one a listing
	// carried in the other direction, so a reader that cares which way an edge
	// runs checks it and one that does not can ignore it. It is empty where the
	// tracker did not say, which a reader treats as this item's own.
	IssueID string
	ID      string
	Type    string
	Status  string
}

// parentChildDependency is how bd states decomposition when it states it as an
// edge rather than as a field. See WorkItem.DecomposedFrom for why that
// distinction is not academic.
const parentChildDependency = "parent-child"

// DecomposedFrom is the item this one was broken out of, and empty for work that
// was not broken out of anything.
//
// bd states that relationship two ways and a store may use either: the parent
// field beside the item, and a parent-child dependency on the parent. A capture
// of this project's own tracker states it both ways — testdata holds the listing
// it answered for yoyodyne-ifd.121.2 and its parent — but the tracker's own
// export states only the edge, carrying no parent field on any item in it, so a
// reading that consults only the field sees such a store as a backlog with no
// decomposition anywhere in it. That is the whole of why both are read: a reader
// handed one of these stores cannot tell which it has.
//
// It is not, despite what an earlier version of this comment said, what let
// yoyodyne-ifd.121 and the child carrying its execution be started as two
// developer runs of one scope. Nothing keyed on parentage was in the tree when
// that happened, in either direction. See
// docs/diagnoses/yoyodyne-ifd-273-121-double-run-mechanism.md.
//
// The field wins where both are stated, because that is the tracker answering
// the question directly. An edge the tracker attributes to some other item is
// skipped, so a listing that carries an item's children alongside its parent
// cannot be read as the item having been broken out of its own child.
func (w WorkItem) DecomposedFrom() string {
	if parent := strings.TrimSpace(w.Parent); parent != "" {
		return parent
	}
	id := strings.TrimSpace(w.ID)
	for _, dependency := range w.Dependencies {
		if !strings.EqualFold(strings.TrimSpace(dependency.Type), parentChildDependency) {
			continue
		}
		if issue := strings.TrimSpace(dependency.IssueID); issue != "" && issue != id {
			continue
		}
		if parent := strings.TrimSpace(dependency.ID); parent != "" && parent != id {
			return parent
		}
	}
	return ""
}

// BlocksDependency is the Beads dependency type that makes one item wait for
// another. It is the only edge that decides anything about starting work: a
// parent-child edge is decomposition, and an item is not held back by having
// been broken out of something.
//
// It is exported because more than one place has to name the same edge, and a
// second spelling of it is a second answer to whether an item may be started.
const BlocksDependency = "blocks"

// The tracker statuses admitted work that is not finished can be in. They are
// what the claim reads to judge a status it was refused on, and they are the
// same two the backlog assembles the order from.
const (
	statusOpen    = "open"
	statusBlocked = "blocked"
)

// WaitingOn names the unfinished work this item waits for, given the admitted
// work that is still unfinished. It is what says whether the item can be started
// at all, and it is here — beside the item — because two readings of it are two
// answers: the backlog decides what to pull from it, and the claim decides
// whether a status of blocked is stale, and those two disagreeing is what burned
// twenty-nine dispatches of yoyodyne-ifd.285 in twenty hours.
//
// It asks the listing in both directions, because neither half is reliable
// alone. A listing may record only that a dependency exists, reading the same
// after the blocker closed as before, so a dependency is named when the
// depended-on item is itself still unfinished. A listing that does carry a
// status is believed on it, so work the tracker reports as unfinished is named
// whether or not the caller knew it was still queued.
//
// Both halves are load-bearing, and which one answers depends on the shape the
// caller read: bd's show carries the blocker's real status and bd's list carries
// none, so an in-flight blocker — which has left the backlog and so is in no
// admitted listing — is named by the status half alone.
// TestBlockerStatusConformance pins that against the real binary.
func (w WorkItem) WaitingOn(unfinished map[string]struct{}) []string {
	var waiting []string
	for _, dependency := range w.Dependencies {
		if dependency.Type != BlocksDependency || dependency.Status == "closed" {
			continue
		}
		_, queued := unfinished[dependency.ID]
		if queued || dependency.Status != "" {
			waiting = append(waiting, dependency.ID)
		}
	}
	sort.Strings(waiting)
	return waiting
}

// NewWorkItem is a work item to create. It is deliberately narrow: the harness
// creates items on someone's explicit approval, so this carries the item and
// the provenance that explains it rather than every field bd accepts.
type NewWorkItem struct {
	RelevantGoals []string

	Title       string
	Description string
	// Type is the Beads issue type. It is required: an item created with
	// whatever type bd happened to default to is not the item that was approved.
	Type string
	// Notes records where the item came from. Beads keeps it beside the
	// description rather than inside it, so provenance stays legible as the
	// description is edited.
	Notes  string
	Parent string
	// Executor is what will carry the work, where that is not a developer run.
	// It is optional and empty for ordinary work, and it is set here rather than
	// afterwards because an item is chosen from the moment it is admitted: a
	// marker added in a later call is a window in which the harness can pull the
	// item for a run nothing can execute.
	Executor domain.WorkItemExecutor
	// Parking is why work is being admitted already parked, and is empty for the
	// work that is admitted to be pulled. It is set here rather than afterwards
	// for the reason the executor is: an item is chosen from the moment it is
	// admitted, and a marker added by a later call is a window in which a
	// draining queue can pull work somebody has just said not to do.
	Parking domain.WorkItemParking
	// Priority is where the item is admitted in the backlog's order. It is a
	// pointer because zero is the highest Beads priority rather than an absent
	// one, and because admitting work without saying where it goes is a real
	// request: the tracker's own default is then what places it.
	Priority *int
	// Labels are applied in the same write as the admission, so an item admitted
	// under a labelling practice never exists unlabelled: a label added by a
	// later call is a gap in which whatever reads the label — a filter, a seat
	// watching for it — sees the item without it.
	Labels []string
	// Origin is who asked for the item and on whose behalf. It is written in the
	// same write as the admission, for the reason the labels are: an origin added
	// by a later call is a window in which the item reads as unknown, and a call
	// that never comes leaves it unknown for good. The zero origin writes nothing,
	// which is what a creation that is not an admission — a decomposition of
	// work already admitted — leaves.
	Origin domain.WorkItemOrigin
}

// WorkItemChange is a bounded edit to an item that already exists. Each field is
// applied only when it is set, so an edit says exactly what it changes and
// leaves everything it does not name alone.
type WorkItemChange struct {
	// Nil leaves the list alone; an empty list clears it.
	RelevantGoals []string

	Title       string
	Description string
	// AppendNotes adds to the item's notes rather than replacing them, so an
	// edit never erases the provenance an earlier one recorded.
	AppendNotes string
	// Executor is what carries the item's execution, applied only when it is set.
	// It is here as well as on a creation because the marker had to arrive after
	// the queue did: work whose executor is a conversation was admitted for as
	// long as there was no way to say so, and an item that cannot acquire the
	// marker afterwards is an item that keeps being chosen for a run.
	Executor domain.WorkItemExecutor
	// Parking is why the item is not to be pulled, applied only when it is set.
	// It is a pointer because parking and releasing are different requests and
	// leaving the parking alone is a third: a nil parking changes nothing, an
	// empty one releases the item back into the queue, and a stated one parks it
	// with that as the reason.
	Parking *domain.WorkItemParking
	// Priority is a pointer because zero is the highest Beads priority rather
	// than an absent one.
	Priority *int
	// Parent is a pointer because reparenting and detaching are different
	// requests: a nil parent leaves the item where it is, and an empty one
	// removes the parent bd currently records.
	Parent *string
	// AddLabels and RemoveLabels change the item's labels one at a time, leaving
	// every label they do not name alone. They are two lists rather than one
	// replacement because bd also offers replacement, and a replacement is the
	// label-shaped version of the notes rewrite this package refuses: it takes
	// off whatever somebody else put on. Adding a label the item carries and
	// removing one it does not are both no-ops bd accepts.
	AddLabels    []string
	RemoveLabels []string
}

type Client struct {
	Runner  execution.ProcessRunner
	Binary  string
	Dir     string
	Timeout time.Duration
	// readBack is how a claim confirms the clear of a stale blocked status. The
	// zero value is the default bound; it is set only by this package's tests,
	// which drive a clear that lands late and one that never lands without
	// waiting the seconds the real bound spans.
	readBack staleBlockClearReadBack
	// Listings is told how each listing ended, where it is set: that it
	// answered, or that it failed after its retries and what it said. It is what
	// lets `yoyo status` say the tracker is not answering listings and since
	// when, rather than each reader learning it alone from the one listing it
	// made. Nil records nothing, which is every client built for a single
	// command. A record that cannot be written never fails the listing it is
	// about: the listing's answer is the caller's, and the record is a reading
	// of it.
	Listings ListingRecorder
	// listingPause is how a listing waits before it asks again. The zero value
	// sleeps; it is set only by this package's tests, which drive a listing that
	// times out on every attempt without waiting the seconds between them.
	listingPause func(ctx context.Context, wait time.Duration) bool
}

// ListingRecorder is where a client writes how its listings end. It is
// satisfied by *runstate.TrackerListingStore.
type ListingRecorder interface {
	Failed(at time.Time, failure string) error
	Answered(at time.Time) error
}

var (
	issueIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	statusPattern    = regexp.MustCompile(`^[a-z][a-z_]*$`)
	issueTypePattern = regexp.MustCompile(`^[a-z][a-z_]*$`)
)

// MaxLabelBytes bounds one label. It is the domain's bound, restated here so a
// caller of this client can state the bound it refuses on rather than only that
// it refused; the rule itself is domain.ValidateLabel, which the configuration
// holds a developer slot's preferred labels to as well.
const MaxLabelBytes = domain.MaxLabelBytes

// ErrNoSuchWorkItem reports an id the tracker holds no item under. It is told
// apart from every other way Show fails because the two are opposite things to
// do about: a surface answers "there is no such item" for one and "the tracker
// could not be read" for the rest, and reading the second as the first would
// report a tracker that is down as a backlog with a hole in it.
var ErrNoSuchWorkItem = errors.New("no work item is recorded under that id")

// ValidIssueID reports whether an id has the shape the tracker accepts, which is
// what a surface checks before it puts an id it was handed on a command line.
func ValidIssueID(id string) bool {
	return issueIDPattern.MatchString(id)
}

func (c Client) Show(ctx context.Context, id string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	data, err := c.run(ctx, "show", id, "--json")
	if err != nil {
		// bd refuses an id it holds nothing under with `Issue <id> not found` on
		// its standard error (cmd/bd/show.go), and the sentinel is read off that
		// line and its id alone: a refusal that merely contains "not found" — a
		// database file missing, a helper bd could not find — is the tracker not
		// answering, and a runner that could not start bd at all is not a
		// refusal bd wrote, so neither is read as an item that does not exist.
		var refused processFailure
		if errors.As(err, &refused) && strings.Contains(strings.ToLower(refused.message), strings.ToLower("issue "+id+" not found")) {
			return WorkItem{}, fmt.Errorf("%w: %v", ErrNoSuchWorkItem, err)
		}
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

// unboundedListing is what bd is told so that a listing is the whole of what
// matches rather than its first page. `bd ready` defaults to 100; `bd list`
// caps its output at fifty rows by default, and at twenty in what it takes for
// an agent session; zero is its
// word for no cap. Every reading here is a decision over the whole set — which
// work is closed, which is open, which is blocked — and a page of it decides
// wrongly for whatever fell past the cap, so the cap is lifted on the command
// line rather than left to bd's reading of whether its output is a terminal.
const unboundedListing = "--limit=0"

// everyStatus is what bd is told so that a listing narrowed to no status is
// every item it holds. `bd list` given no status leaves closed work out by
// default, and the readers that ask for no status ask precisely because closed
// work is what they need to see: which docket entries are on closed work, and
// whether a creation duplicates work that has already landed. Against a bare
// listing both read every item as unfinished and nothing fails.
// TestUnfilteredListingConformance pins it against bd itself.
const everyStatus = "--all"

// List reports the work items Beads currently holds, optionally narrowed to one
// status. It is read-only: nothing about listing work claims, changes, or
// closes any of it. It reads the whole set: see unboundedListing. No status is
// every status, closed included: see everyStatus.
//
// A tracker that refuses the no-cap flag is asked once without it to learn how
// many rows it returned, but that reading is refused as possibly incomplete.
// Neither a default page nor a warning that bd cut a list is a whole-set answer.
//
// A listing its bound killed is asked again, twice, after a short wait: see
// listingWaits. One that still fails is refused naming how many attempts it
// made and over how long, around the last failure, so a caller says the
// tracker did not answer rather than that one listing timed out.
func (c Client) List(ctx context.Context, status string) ([]WorkItem, error) {
	var filter []string
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		if !statusPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("invalid Beads status %q", status)
		}
		filter = append(filter, "--status="+trimmed)
	} else {
		filter = append(filter, everyStatus)
	}
	items, err := c.listPatiently(ctx, filter)
	c.recordListing(ctx, err)
	return items, err
}

// listingWaits are the pauses between the attempts one listing makes, and so
// how many it makes: one more than there are waits.
//
// A listing is retried because of what stops one. bd opens its embedded Dolt
// store under an exclusive lock, every bd process takes it — reads included —
// and a write holds it while it rewrites the whole export beside the store, so
// a listing that arrives behind a write or two waits its turn and a bound of
// thirty seconds kills some of those that would have answered a few seconds
// later. docs/diagnoses/yoyodyne-ifd-433-20-tracker-listing-timeouts.md is the
// evidence. The waits are short on purpose: a caller of a listing is a pass, a
// docket, or a conversation somebody is reading, and three attempts at the
// default bound are already a minute and a half of it. A store that is still
// not answering after that is not contention a caller can wait out, and is
// said as the tracker not answering.
var listingWaits = []time.Duration{2 * time.Second, 8 * time.Second}

// listPatiently makes one listing, asking again after each wait where the
// listing was killed by its bound and the caller has not given up.
func (c Client) listPatiently(ctx context.Context, filter []string) ([]WorkItem, error) {
	started := time.Now()
	attempts := 0
	for {
		attempts++
		items, err := c.workListing(ctx, "list", filter)
		if err == nil {
			return items, nil
		}
		if !timedOut(err) || ctx.Err() != nil {
			return nil, err
		}
		if attempts > len(listingWaits) {
			return nil, fmt.Errorf("bd list did not answer within its %s bound on any of %d attempts over %s: %w",
				c.timeout(), attempts, time.Since(started).Round(time.Second), err)
		}
		if !c.pauseListing(ctx, listingWaits[attempts-1]) {
			return nil, err
		}
	}
}

// timedOut reports a bd the runner killed at its bound, which is the one
// listing failure another attempt may well not meet: bd refusing, or answering
// something that does not decode, is the same answer the next time.
func timedOut(err error) bool {
	var failure processFailure
	return errors.As(err, &failure) && failure.status == execution.ProcessTimedOut
}

func (c Client) pauseListing(ctx context.Context, wait time.Duration) bool {
	if c.listingPause != nil {
		return c.listingPause(ctx, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// recordListing tells the recorder how a listing ended. A listing the caller
// gave up on says nothing about the tracker and is not recorded either way.
func (c Client) recordListing(ctx context.Context, err error) {
	if c.Listings == nil || ctx.Err() != nil {
		return
	}
	if err != nil {
		_ = c.Listings.Failed(time.Now(), err.Error())
		return
	}
	_ = c.Listings.Answered(time.Now())
}

func (c Client) timeout() time.Duration {
	if c.Timeout == 0 {
		return defaultTimeout
	}
	return c.Timeout
}

// refusedFlag reports whether a bd failure is bd not knowing the flag, as
// distinct from bd failing at what the flag asked. bd parses its command line
// with cobra, whose refusal names the flag without its value — `unknown flag:
// --limit` for `--limit=0` — and is issued before any store is opened, so the
// wording is the whole of what tells the two apart.
func refusedFlag(err error, flag string) bool {
	name, _, _ := strings.Cut(flag, "=")
	return strings.Contains(err.Error(), "unknown flag: "+name)
}

// Ready reports the work items the tracker itself considers ready to be worked
// on: admitted, not already claimed, and waiting on nothing unfinished. It is
// read-only and asks for the whole list with --limit=0, preserving bd's order.
// Cut output is refused with the count read and a missing-work warning.
//
// It exists because readiness is the tracker's answer to give rather than this
// client's to infer. A dependency lives in the tracker's own graph, and a status
// listing is not guaranteed to carry it, so deciding "this item lists no
// blockers, therefore it can be pulled" would report a blocked item as the next
// thing to work on wherever the listing leaves dependencies out.
func (c Client) Ready(ctx context.Context) ([]WorkItem, error) {
	items, err := c.workListing(ctx, "ready", nil)
	c.recordListing(ctx, err)
	return items, err
}

// workListing lifts the row cap on both whole-set readings. The old-tracker
// fallback is evidence about what was read, never a successful whole listing.
func (c Client) workListing(ctx context.Context, verb string, filter []string) ([]WorkItem, error) {
	data, err := c.run(ctx, append([]string{verb, "--json", unboundedListing}, filter...)...)
	if err != nil && refusedFlag(err, unboundedListing) {
		data, err = c.run(ctx, append([]string{verb, "--json"}, filter...)...)
		if err == nil {
			items, decodeErr := decodeWorkListing(data, verb)
			if decodeErr != nil {
				return nil, decodeErr
			}
			return nil, fmt.Errorf("bd %s could not lift its default limit: read %d work item(s), but the list may be cut and work may be missing", verb, len(items))
		}
	}
	if err != nil {
		return nil, err
	}
	return decodeWorkListing(data, verb)
}

// Create records a new work item. Unlike every other call here it brings work
// into existence, so the caller is responsible for having the authority to ask:
// this adapter is the mechanism, never the approval.
func (c Client) Create(ctx context.Context, item NewWorkItem) (WorkItem, error) {
	if err := item.validate(); err != nil {
		return WorkItem{}, err
	}
	args := []string{
		"create",
		"--title=" + item.Title,
		"--description=" + item.Description,
		"--type=" + item.Type,
	}
	// Every key the created item will carry is gathered before any of it is
	// rendered, because bd takes a creation's metadata as one object: a second
	// --metadata would replace the first rather than add to it.
	entries := map[string]string{}
	if item.RelevantGoals != nil {
		entries[relevantGoalsKey] = encodeRelevantGoals(item.RelevantGoals)
	}
	if notes := strings.TrimSpace(item.Notes); notes != "" {
		args = append(args, "--notes="+notes)
		if statement, records := goal.NamedIn(notes); records {
			// The witness is derived from what is about to be written rather than
			// asked of the caller. A caller that had to remember it would eventually
			// not, and an attribution written without one is exactly an attribution
			// whose loss goes unnoticed.
			entries[goalWitnessKey] = witnessValue(statement)
		}
	}
	if executor := strings.TrimSpace(string(item.Executor)); executor != "" {
		entries[executorKey] = executor
	}
	if parking := item.Parking.Reason(); parking != "" {
		entries[parkedKey] = parking
	}
	if item.Origin.Known() {
		for key, value := range originEntries(item.Origin.Trimmed()) {
			entries[key] = value
		}
	}
	if len(entries) > 0 {
		metadata, err := creationMetadata(entries)
		if err != nil {
			return WorkItem{}, err
		}
		args = append(args, "--metadata="+metadata)
	}
	if parent := strings.TrimSpace(item.Parent); parent != "" {
		args = append(args, "--parent="+parent)
	}
	if item.Priority != nil {
		args = append(args, "--priority="+strconv.Itoa(*item.Priority))
	}
	// One flag per label rather than bd's comma-joined spelling, so the command
	// line never depends on a label being free of the separator; validation
	// already holds a label to an identifier, and this is what makes that
	// redundancy rather than the thing the write rests on.
	for _, label := range item.Labels {
		args = append(args, "--labels="+strings.TrimSpace(label))
	}
	args = append(args, "--json")
	data, err := c.run(ctx, args...)
	if err != nil {
		return WorkItem{}, err
	}
	// Creation answers with the one item it made rather than a list, and the
	// created identifier is what everything downstream refers to, so an answer
	// without one is a failure however it was reported.
	var raw rawWorkItem
	if err := decodeJSON(data, &raw); err != nil {
		return WorkItem{}, fmt.Errorf("decode bd created work item: %w", err)
	}
	created, err := convertWorkItem(raw)
	if err != nil {
		return WorkItem{}, err
	}
	if item.RelevantGoals != nil && !slices.Equal(created.RelevantGoals, item.RelevantGoals) {
		return WorkItem{}, fmt.Errorf("bd created work item %s with relevant goals %v, want %v", created.ID, created.RelevantGoals, item.RelevantGoals)
	}
	if created.Title != item.Title {
		return WorkItem{}, fmt.Errorf("bd created work item %s with title %q, want %q", created.ID, created.Title, item.Title)
	}
	// An executor that was asked for and not stored is a failure, because
	// admission is the path this marker exists for: the item is in the queue and
	// pullable the moment this returns, so a caller told the marker was set would
	// have admitted exactly the item the guard does not cover. It is read back
	// where the priority beside it is not, and the difference is what an absent
	// field means. bd's creation response omits a key it was never given, so a
	// missing executor is unambiguously one that was not stored — where a missing
	// priority is indistinguishable from the default having been applied, which is
	// why refusing on that one would lose items rather than protect the order.
	if executor := strings.TrimSpace(string(item.Executor)); executor != "" && string(created.Executor) != executor {
		return WorkItem{}, fmt.Errorf("bd created work item %s with executor %q, want %q", created.ID, created.Executor, executor)
	}
	// The parking is read back for the same reason and against the same risk: an
	// item admitted as parked is in the queue and pullable the moment this
	// returns, so a caller told the work was parked would have admitted exactly
	// the item a draining queue is free to take.
	if parking := item.Parking.Reason(); parking != "" && created.Parking.Reason() != parking {
		return WorkItem{}, fmt.Errorf("bd created work item %s parked %q, want %q", created.ID, created.Parking, parking)
	}
	// The labels are read back for the same reason: the item is in the queue the
	// moment this returns, and a caller told it was admitted labelled would have
	// admitted exactly the item nothing filtering on the label sees.
	if missing := labelsMissing(created, item.Labels); len(missing) > 0 {
		return WorkItem{}, fmt.Errorf("bd created work item %s without the label(s) %s it was given", created.ID, strings.Join(missing, ", "))
	}
	// The origin is read back for the reason the labels are: it is what says who
	// filled the backlog, and a caller told it was recorded when it was not would
	// have admitted exactly the item that reads as unknown.
	if item.Origin.Known() && created.Origin != item.Origin.Trimmed() {
		return WorkItem{}, fmt.Errorf("bd created work item %s with origin %+v, want %+v", created.ID, created.Origin, item.Origin.Trimmed())
	}
	// The requested priority is deliberately not read back, for the reason the
	// parent is not read back after an update: an unset field and a field bd's
	// response does not carry are indistinguishable here, and refusing a creation
	// that actually happened would lose the item rather than protect the order.
	return created, nil
}

// Update applies a bounded edit to an item that already exists. Like Create it
// changes the tracker, so the caller is responsible for having the authority to
// ask; unlike Create it names the fields it touches, which is what lets an edit
// be validated before it is run and checked against what bd reports afterwards.
func (c Client) Update(ctx context.Context, id string, change WorkItemChange) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if err := change.validate(); err != nil {
		return WorkItem{}, err
	}
	args := []string{"update", id}
	if change.RelevantGoals != nil {
		args = append(args, "--set-metadata="+relevantGoalsKey+"="+encodeRelevantGoals(change.RelevantGoals))
	}
	if title := strings.TrimSpace(change.Title); title != "" {
		args = append(args, "--title="+title)
	}
	if description := strings.TrimSpace(change.Description); description != "" {
		args = append(args, "--description="+description)
	}
	if notes := strings.TrimSpace(change.AppendNotes); notes != "" {
		args = append(args, "--append-notes="+notes)
		if statement, records := goal.NamedIn(notes); records {
			args = append(args, "--set-metadata="+goalWitnessKey+"="+witnessValue(statement))
		}
	}
	if executor := strings.TrimSpace(string(change.Executor)); executor != "" {
		args = append(args, "--set-metadata="+executorKey+"="+executor)
	}
	// A release sets the key to nothing rather than removing it, so parking and
	// releasing are one write with one shape. What matters afterwards is what the
	// item reads as, and both an absent key and an empty one read as unparked.
	if change.Parking != nil {
		args = append(args, "--set-metadata="+parkedKey+"="+change.Parking.Reason())
	}
	if change.Priority != nil {
		args = append(args, "--priority="+strconv.Itoa(*change.Priority))
	}
	if change.Parent != nil {
		args = append(args, "--parent="+strings.TrimSpace(*change.Parent))
	}
	for _, label := range change.AddLabels {
		args = append(args, "--add-label="+strings.TrimSpace(label))
	}
	for _, label := range change.RemoveLabels {
		args = append(args, "--remove-label="+strings.TrimSpace(label))
	}
	args = append(args, "--json")
	data, err := c.write(ctx, id, args...)
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	// What bd echoes back is verified against what was asked for, so an edit that
	// did not take effect is a failure rather than a reported success. The parent
	// is the exception, and knowingly so: bd's update response does not carry it,
	// so a reparenting rests on bd's own report of success and is not read back.
	if change.RelevantGoals != nil && !slices.Equal(item.RelevantGoals, change.RelevantGoals) {
		return WorkItem{}, fmt.Errorf("work item %s relevant goals are %v after being updated, want %v", item.ID, item.RelevantGoals, change.RelevantGoals)
	}
	if title := strings.TrimSpace(change.Title); title != "" && item.Title != title {
		return WorkItem{}, fmt.Errorf("work item %s title is %q after being updated, want %q", item.ID, item.Title, title)
	}
	if change.Priority != nil && item.Priority != *change.Priority {
		return WorkItem{}, fmt.Errorf("work item %s priority is %d after being updated, want %d", item.ID, item.Priority, *change.Priority)
	}
	// The executor is read back for the reason a price is: what rests on it is
	// that nothing chooses this item for a run afterwards, and a marker reported
	// as set and not actually stored would leave the caller believing the item
	// was covered by exactly the guard it is not covered by.
	if executor := strings.TrimSpace(string(change.Executor)); executor != "" && string(item.Executor) != executor {
		return WorkItem{}, fmt.Errorf("work item %s executor is %q after being updated, want %q", item.ID, item.Executor, executor)
	}
	// The parking is read back in both directions, because both directions have
	// something resting on them. A parking that did not take leaves work the
	// operator was told is parked sitting pullable in a queue that drains; a
	// release that did not take leaves work nobody can start and no error saying
	// so, which is the harder of the two to ever notice.
	if change.Parking != nil && item.Parking.Reason() != change.Parking.Reason() {
		if change.Parking.Parked() {
			return WorkItem{}, fmt.Errorf("work item %s is parked %q after being updated, want %q", item.ID, item.Parking, change.Parking.Reason())
		}
		return WorkItem{}, fmt.Errorf("work item %s is still parked %q after being released", item.ID, item.Parking)
	}
	// The labels are read back in both directions. What rests on a label is
	// whatever filters on it — the seat that watches for one, bd's own listing
	// filters — so a label reported as added and not stored is an item that seat
	// never sees, and one reported as removed and still there is an item it keeps
	// seeing.
	if missing := labelsMissing(item, change.AddLabels); len(missing) > 0 {
		return WorkItem{}, fmt.Errorf("work item %s does not carry the label(s) %s after they were added", item.ID, strings.Join(missing, ", "))
	}
	if kept := labelsCarried(item, change.RemoveLabels); len(kept) > 0 {
		return WorkItem{}, fmt.Errorf("work item %s still carries the label(s) %s after they were removed", item.ID, strings.Join(kept, ", "))
	}
	return c.confirmWritten(ctx, item, appendedNote(change.AppendNotes),
		writtenText{field: "description", want: change.Description, stored: func(w WorkItem) string { return w.Description }})
}

// writtenText is one piece of prose a write was told to put on an item: what it
// was, and where the item carries it afterwards.
type writtenText struct {
	field  string
	want   string
	stored func(WorkItem) string
}

// appendedNote is the one every write that records something on an item shares:
// the text it added to the notes.
func appendedNote(note string) writtenText {
	return writtenText{field: "note", want: note, stored: func(w WorkItem) string { return w.Notes }}
}

// confirmWritten answers with the item only where every text a write was told to
// put on it is actually there, so a caller told the write was applied is told
// something that was checked rather than assumed.
//
// The prose fields are checked here rather than beside the title and the priority
// above because they are the ones a confirmation is worth least without: a title
// that did not take is visible in the next listing, and a decision recorded in a
// note that did not take is reasoning nobody knows is gone.
//
// bd answers an update with the item as it holds it afterwards, so the ordinary
// confirmation costs nothing beyond the write. Where that answer does not carry
// the text the item is read back separately before anything is concluded, and the
// two outcomes are kept apart deliberately: a write that landed and was echoed
// badly is not a failure, and reporting one would be the mirror of the loss this
// guards — a durable write reported as lost, which yoyodyne-ifd.327 is the record
// of. What is refused is only the case where the tracker itself, asked again,
// does not hold what was written.
//
// A read-back that cannot run refuses too, and says which of the two it is: an
// unconfirmed write is not a write that failed, and a caller that was going to
// tell somebody the note is recorded must not be told it is.
func (c Client) confirmWritten(ctx context.Context, item WorkItem, written ...writtenText) (WorkItem, error) {
	missing := unconfirmed(item, written)
	if len(missing) == 0 {
		return item, nil
	}
	reread, err := c.Show(ctx, item.ID)
	if err != nil {
		return WorkItem{}, fmt.Errorf("work item %s was reported updated and did not answer with the %s it was given, "+
			"and reading it back to say whether the write landed failed: %w", item.ID, strings.Join(missing, " or the "), err)
	}
	if still := unconfirmed(reread, written); len(still) > 0 {
		return WorkItem{}, fmt.Errorf("work item %s does not carry the %s it was reported to have been given, "+
			"so the write was confirmed and nothing was recorded", item.ID, strings.Join(still, " or the "))
	}
	return reread, nil
}

// unconfirmed names the texts a write was told to put on an item that the item
// does not carry. Text the write was not given is not missing from anything.
func unconfirmed(item WorkItem, written []writtenText) []string {
	var missing []string
	for _, text := range written {
		if wanted := strings.TrimSpace(text.want); wanted != "" && !textCarried(text.stored(item), wanted) {
			missing = append(missing, text.field)
		}
	}
	return missing
}

// textCarried reports whether a field holds text a write put there.
//
// It compares what a tracker is free to have rewritten as equal: line endings
// and trailing space on a line are not the text going missing, and refusing a
// write over one would make the guard the reason a durable note reads as lost.
// Anything else is compared verbatim, because text stored cut short or reflowed
// is exactly the loss this exists to find.
func textCarried(stored, written string) bool {
	return strings.Contains(normalizeText(stored), normalizeText(written))
}

// NotesEndWith reports whether an item's notes end with a note a write appended,
// compared as textCarried compares. It is the end rather than anywhere because
// notes are only ever appended to: a note found earlier in them is an older write
// that happens to read the same, and only one at the end can be the write that
// was just asked for.
func NotesEndWith(notes, note string) bool {
	wanted := normalizeText(note)
	return wanted != "" && strings.HasSuffix(normalizeText(notes), wanted)
}

func normalizeText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// staleBlockedRefusal is how bd refuses a claim on an item whose status field
// says blocked. It is matched on rather than parsed because it is the whole of
// what bd says: there is no exit code or JSON field that distinguishes this
// refusal from any other, so the recovery below is entered on the message and on
// nothing else, and a bd that reworded it would simply stop recovering.
//
// That the refusal happens at all, and that this pattern is what bd's own words
// match, is pinned against the real binary by TestBlockedClaimConformance rather
// than left to this comment: every other check of the recovery drives a scripted
// runner replaying a message this file wrote.
var staleBlockedRefusal = regexp.MustCompile(`(?i)not claimable: status blocked`)

// Claim takes a work item for a run.
//
// It re-reads what the item actually waits on when bd refuses the claim for a
// status of blocked, and claims anyway when the answer is nothing. That is not a
// workaround of bd's gate; it is the harness's two readings of "blocked" being
// made one, which they were not.
//
// The backlog computes a blocked item's readiness from its blocking dependencies
// rather than from its status, because the status is written when work stops and
// never rewritten when what stopped it clears — on 2026-09-04 that read
// two-thirds of this backlog as unpullable, and yoyodyne-ifd.277 released it. bd's
// claim gate reads the status field and nothing else, so from that release
// onwards every item in the released set was selectable and unclaimable at once.
// yoyodyne-ifd.285 was dispatched twenty-nine times between 2026-09-06 18:43 and
// 2026-09-07 14:44, each run dying here, each leaving nothing anybody could read.
//
// So the stale status is corrected where it is found rather than routed around.
// The item is re-read under the claim, which also closes the race the same
// symptom would have if the item's state had genuinely moved between selection
// and here: what is judged is the state that is then claimed. An item that really
// does wait on unfinished work is refused with that work named, so the record
// says which of the two it was.
//
// The correction is confirmed before it is relied on. The status is read back
// after the write and the claim is made only once a read returns open, retrying
// within a bounded wait — and a claim bd still refuses on the status is retried
// on a later read within the same wait; the account of that read-back is returned beside the
// item, and beside the error where no read confirmed it, so the run's record
// can say which of its three endings the clear had. On 2026-09-20 the claim on
// yoyodyne-ifd.415 recorded the clear as made and bd refused the claim that
// followed on the same status, so the re-run tripped on its own correction —
// the stall in the class 415 was admitted to dedicate a slot to.
//
// The account is nil on a claim that met no stale status, which is nearly all
// of them.
//
// What this deliberately does not ask is the other half of the backlog's answer:
// a governance hold — a stoppage whose change is still on a branch, an escalation
// nobody has decided. A hold is the harness's own durable record rather than
// anything the tracker holds, so it is read where work is selected and cannot be
// read from here. That is the right seam and not a gap: every caller of this has
// already answered it. The scheduler consults the holds before it chooses; a
// re-run is a decision the development manager recorded about that exact
// stoppage; and an operator naming an item is the operator deciding.
func (c Client) Claim(ctx context.Context, id string) (WorkItem, *StaleBlockClear, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, nil, err
	}
	item, err := c.claim(ctx, id)
	if err == nil || !staleBlockedRefusal.MatchString(err.Error()) {
		return item, nil, err
	}
	return c.claimPastStaleBlock(ctx, id, err)
}

// StaleBlockClear is the account of a stale blocked status a claim cleared on
// its way to the item: what the tracker said when the status was read back
// after the write, and how many reads it took to say it.
type StaleBlockClear struct {
	Outcome domain.StaleBlockClearOutcome
	// Reads is how many times the status was read back after the write, the
	// read that confirmed it included. On an unconfirmed clear it is every read
	// the bounded wait allowed.
	Reads int
	// Status is what the last read returned: open where a read confirmed the
	// clear, and whatever the tracker still held where none did.
	Status string
	// ClaimsRefused is how many claims bd refused on the status after a read had
	// returned open, each of them retried on a later read within the bound.
	ClaimsRefused int
}

// staleBlockClearReadBack bounds how the clear of a stale blocked status is
// confirmed: how many times the status is read back after the write, and how
// long the reads are spaced. Five reads a second apart is a wait a tracker that
// commits late can land inside and a claim that will not land can fail inside,
// and it is a bound rather than a retry until: a clear no read confirms within
// it is reported as unconfirmed, with what the tracker returned, and never as
// cleared.
type staleBlockClearReadBack struct {
	reads    int
	interval time.Duration
	// sleep waits between reads. Nil is time.Sleep bounded by the context; a
	// test that drives a clear landing late replaces it so the wait is a count
	// rather than a clock.
	sleep func(context.Context, time.Duration) error
}

const (
	defaultStaleBlockClearReads    = 5
	defaultStaleBlockClearInterval = time.Second
)

func (r staleBlockClearReadBack) settled() staleBlockClearReadBack {
	if r.reads < 1 {
		r.reads = defaultStaleBlockClearReads
	}
	if r.interval <= 0 {
		r.interval = defaultStaleBlockClearInterval
	}
	if r.sleep == nil {
		r.sleep = sleepWithin
	}
	return r
}

// sleepWithin waits for the interval or until the context ends, whichever is
// first, and reports the context's ending as the reason where that is what
// ended the wait.
func sleepWithin(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c Client) claim(ctx context.Context, id string) (WorkItem, error) {
	data, err := c.write(ctx, id, "update", id, "--claim", "--json")
	if err != nil {
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

// claimPastStaleBlock decides whether the status bd refused on is stale, and
// claims the item where it is.
//
// The reading is the item's own — the same WaitingOn the backlog orders the queue
// by — over the admitted work as it stands right now rather than as the caller
// last saw it. Anything that cannot be read leaves the original refusal standing:
// this widens what may be claimed, so a reading that failed must not be what
// widens it.
//
// The correction is written to the tracker, not held in this process. A status
// this call left as it found it would be refused again by the next claim, and the
// item would read as blocked to everything that opens it; and the harness owns
// tracker writes, so recording what it corrected — in the notes, appended — is the
// account of a change nobody else made.
//
// The write is not the correction; the read that returns open is. The note the
// write carries says the harness is clearing the status and that the claim
// follows once the tracker reads it back as open, and the claim is made on that
// read and on nothing else, again on a later read where bd refuses it on the
// status. Where no claim within the bound is taken, the
// clear is reported as unconfirmed with the status the tracker returned, a note
// saying so is appended, and the item is left for the next pull rather than
// claimed — a claim made on a status the tracker still holds as blocked is the
// refusal this was entered on, met a second time.
func (c Client) claimPastStaleBlock(ctx context.Context, id string, refusal error) (WorkItem, *StaleBlockClear, error) {
	item, err := c.Show(ctx, id)
	if err != nil {
		return WorkItem{}, nil, errors.Join(refusal, fmt.Errorf("re-read %s to judge whether its blocked status is stale: %w", id, err))
	}
	// Only the status bd refused on is corrected. A re-read that finds the item
	// somewhere else is the race rather than the disagreement — most sharply where
	// it now reads as claimed, which would be another run holding it — and
	// reopening that item is taking work off whoever has it.
	if item.Status != statusBlocked {
		return WorkItem{}, nil, fmt.Errorf(
			"%s is at status %q when re-read under the claim, not blocked as bd refused it, so nothing here is a stale status to correct: %w",
			id, item.Status, refusal)
	}
	unfinished, err := c.unfinished(ctx)
	if err != nil {
		return WorkItem{}, nil, errors.Join(refusal, err)
	}
	if waiting := item.WaitingOn(unfinished); len(waiting) > 0 {
		return WorkItem{}, nil, fmt.Errorf(
			"%s is blocked and waits on unfinished work (%s), so its status is not stale and the claim stands refused: %w",
			id, strings.Join(waiting, ", "), refusal)
	}
	// The refusal is quoted first and said to be the one that came before the
	// clear. Quoted after the promise of a claim, it read as bd refusing the claim
	// that followed the clear: on 2026-09-28 the reviewer of yoyodyne-ifd.428.44
	// reported exactly that from the item's notes, over a claim that had landed.
	corrected := fmt.Sprintf(
		"%s. That refusal came before anything below. The harness is clearing this item's blocked status to claim it: nothing unfinished blocks it, and the status was left over from whatever did. The claim follows once the tracker reads the status back as open.",
		singleLineNote(refusal.Error()))
	if _, err := c.write(ctx, id, "update", id, "--status=open", "--append-notes="+corrected, "--json"); err != nil {
		return WorkItem{}, nil, errors.Join(refusal, fmt.Errorf("clear the stale blocked status on %s: %w", id, err))
	}
	claimed, account, err := c.claimOnConfirmedClear(ctx, id)
	if err != nil {
		return WorkItem{}, account, errors.Join(refusal, err)
	}
	if account.Outcome == domain.StaleBlockClearUnconfirmed {
		returned := fmt.Sprintf("%d read(s) over %s returned status %q rather than open",
			account.Reads, c.readBack.settled().span(account.Reads), account.Status)
		if account.ClaimsRefused > 0 {
			returned = fmt.Sprintf("%d read(s) over %s, the last returning status %q, and bd refused the claim on the status %d time(s) after a read that returned open",
				account.Reads, c.readBack.settled().span(account.Reads), account.Status, account.ClaimsRefused)
		}
		// The order of the writes leads, and the tracker's own words follow it: a
		// refusal that quoted bd alone read as the first claim refused a second
		// time, when what it was is a clear that was written and never took.
		unconfirmed := fmt.Errorf(
			"%s, so the item is left for the next pull rather than claimed",
			clearOrder(id, account, returned))
		// The note the write carried promised a claim on a read that never came,
		// so what came instead is written beside it: the next reader of the item
		// finds the account rather than a promise, and a status the tracker still
		// holds as blocked with nothing saying why the claim never followed.
		note := fmt.Sprintf("The harness could not confirm the clear above: %s. The item is left for the next pull rather than claimed.", returned)
		if _, err := c.write(ctx, id, "update", id, "--append-notes="+note, "--json"); err != nil {
			return WorkItem{}, account, errors.Join(unconfirmed, refusal, fmt.Errorf("record the unconfirmed clear on %s: %w", id, err))
		}
		return WorkItem{}, account, errors.Join(unconfirmed, refusal)
	}
	// The claim is the second write, and it is recorded on the item beside the
	// first: without it the notes end on the promise of a claim and the refusal
	// that preceded it, and a reader cannot tell whether the claim ever came. The
	// note is written after the claim rather than riding it, because a note on a
	// claim bd refuses is a note about a claim that did not happen. A note that
	// cannot be written does not undo the claim — the item is claimed either way,
	// and failing here would strand it claimed with no run behind it; the run's
	// own record carries the same account from the value returned.
	claimedNote := fmt.Sprintf("The harness read the cleared status back as open on read %d and claimed the item.", account.Reads)
	if account.ClaimsRefused > 0 {
		claimedNote = fmt.Sprintf("The harness read the cleared status back as open, bd refused the claim on the status %d time(s) after that, and the item was claimed on read %d.", account.ClaimsRefused, account.Reads)
	}
	_, _ = c.write(ctx, id, "update", id, "--append-notes="+claimedNote, "--json")
	return claimed, account, nil
}

// clearOrder says what the claim past a stale blocked status did, in the order
// it did it: the claim bd refused on the status, the clear written after it, and
// what reading the clear back returned. A refusal that ends the claim after a
// clear leads with this, so it is never read as the first refusal repeated.
func clearOrder(id string, account *StaleBlockClear, readBack string) string {
	claims := "no read returned open, so no claim followed the clear"
	if account.ClaimsRefused > 0 {
		claims = fmt.Sprintf("each claim made after a read returned open was refused on the status (%d)", account.ClaimsRefused)
	}
	return fmt.Sprintf(
		"bd refused the claim on %s for its blocked status; the harness then wrote the status open and read it back: %s; %s; the clear of the stale blocked status on %s was never confirmed",
		id, readBack, claims, id)
}

// claimOnConfirmedClear reads the status back after the clear was written and
// claims the item on a read that returns open, within the bound, and says what
// it found. The account is returned however the reads ended: a read that could
// not be made is an error beside the reads that were, so the record still says
// how far the confirmation got.
//
// A claim bd still refuses on the status after a read returned open is retried
// on a later read within the same bound, rather than ending the claim. That is
// the ending the read-back alone did not cover: on 2026-09-22 and 2026-09-23 a
// re-run the development manager recorded cleared the status on
// yoyodyne-ifd.432.10 and on yoyodyne-ifd.117.3, and bd then refused the claim
// with "issue not claimable: status blocked", so the carry-out spent its pull
// and a later pull claimed each item. The clear is confirmed only by the claim
// the read made room for: where no claim within the bound is taken, the clear
// is reported as unconfirmed, with how many claims bd refused, and the item is
// left for the next pull.
func (c Client) claimOnConfirmedClear(ctx context.Context, id string) (WorkItem, *StaleBlockClear, error) {
	readBack := c.readBack.settled()
	account := &StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed}
	for attempt := 1; attempt <= readBack.reads; attempt++ {
		if attempt > 1 {
			if err := readBack.sleep(ctx, readBack.interval); err != nil {
				return WorkItem{}, account, fmt.Errorf("wait to read %s's status back after clearing its stale blocked status: %w", id, err)
			}
		}
		item, err := c.Show(ctx, id)
		if err != nil {
			return WorkItem{}, account, fmt.Errorf("read %s's status back after clearing its stale blocked status: %w", id, err)
		}
		account.Reads = attempt
		account.Status = item.Status
		if item.Status != statusOpen {
			continue
		}
		claimed, err := c.claim(ctx, id)
		if err == nil {
			account.Outcome = domain.StaleBlockClearConfirmed
			if attempt > 1 {
				account.Outcome = domain.StaleBlockClearConfirmedLate
			}
			return claimed, account, nil
		}
		if !staleBlockedRefusal.MatchString(err.Error()) {
			return WorkItem{}, account, fmt.Errorf("bd refused the claim on %s for its blocked status; the harness then wrote the status open, read %d returned it as open, and the claim made on that read failed: %w", id, attempt, err)
		}
		account.ClaimsRefused++
	}
	return WorkItem{}, account, nil
}

// span is how long the given number of reads took to space out: the intervals
// between them, which is what a report of an unconfirmed clear says it waited.
func (r staleBlockClearReadBack) span(reads int) time.Duration {
	if reads < 1 {
		return 0
	}
	return time.Duration(reads-1) * r.interval
}

// unfinished is the admitted work that is not finished, which is what says
// whether a dependency an item records is still somebody's wait. It is the same
// pair of slices the backlog is assembled from; a dependency on work that is in
// neither has been closed or pulled, and is nobody's wait.
func (c Client) unfinished(ctx context.Context) (map[string]struct{}, error) {
	admitted := make(map[string]struct{})
	for _, status := range []string{statusOpen, statusBlocked} {
		items, err := c.List(ctx, status)
		if err != nil {
			return nil, fmt.Errorf("list %s work items to judge what is still unfinished: %w", status, err)
		}
		for _, item := range items {
			admitted[item.ID] = struct{}{}
		}
	}
	return admitted, nil
}

// singleLineNote folds a bd message into one line for the note that records it.
// bd's refusals are short, so this is a no-op over what it says today; it is here
// because the note must not depend on that, and a note carrying a provider's
// whole output is one nobody reads.
func singleLineNote(message string) string {
	return "bd refused the claim: " + oneline.Fold(message, maxCorrectionNoteBytes)
}

// maxCorrectionNoteBytes bounds what the correction note quotes of bd's refusal.
const maxCorrectionNoteBytes = 512

func (c Client) RecordOutcome(ctx context.Context, id, notes string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(notes) == "" {
		return WorkItem{}, errors.New("outcome notes are required")
	}
	data, err := c.write(ctx, id, "update", id, "--append-notes="+notes, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	return c.confirmWritten(ctx, item, appendedNote(notes))
}

// Block records a durable blocker on a work item the harness could not finish,
// carrying the reason into the item's notes. The applied status is verified
// rather than assumed: a blocker that was not actually stored would leave the
// item looking like work still in progress.
func (c Client) Block(ctx context.Context, id, reason string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItem{}, errors.New("blocker reason is required")
	}
	data, err := c.write(ctx, id, "update", id, "--status=blocked", "--append-notes="+reason, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Status != "blocked" {
		return WorkItem{}, fmt.Errorf("work item %s status is %q after being blocked, want blocked", item.ID, item.Status)
	}
	return c.confirmWritten(ctx, item, appendedNote(reason))
}

// Unblock clears a status of blocked, carrying the account of why it was stale
// into the item's notes. It is the deliberate half of what the claim does for
// itself when bd refuses a claim on a status nothing maintains: the same write,
// made because somebody judged the status stale rather than because a run
// happened to meet it.
//
// It says nothing about whether the status was actually stale, which is the
// caller's judgement over the dependency graph and the holds. What it does hold
// to is that the write landed: a status still reading blocked afterwards would
// leave the item unclaimable and the note claiming it had been released.
func (c Client) Unblock(ctx context.Context, id, note string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(note) == "" {
		return WorkItem{}, errors.New("a note saying what made the blocked status stale is required")
	}
	data, err := c.write(ctx, id, "update", id, "--status=open", "--append-notes="+note, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Status != statusOpen {
		return WorkItem{}, fmt.Errorf("work item %s status is %q after its blocked status was cleared, want open", item.ID, item.Status)
	}
	return c.confirmWritten(ctx, item, appendedNote(note))
}

// Release gives a claimed work item back to the queue, carrying the reason into
// the item's notes. It is the opposite of Claim and the same shape as Block: the
// applied status is verified rather than assumed, because a release that did not
// take leaves work nothing will ever pull and no error saying so — which is the
// harder of the two directions to notice, exactly as it is for a parking.
//
// The caller is responsible for having established that nothing is working on
// the item. This is the mechanism and never the judgement: a claim released out
// from under a live run would put two developers on one piece of work.
func (c Client) Release(ctx context.Context, id, reason string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItem{}, errors.New("release reason is required")
	}
	data, err := c.write(ctx, id, "update", id, "--status=open", "--append-notes="+reason, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Status != statusOpen {
		return WorkItem{}, fmt.Errorf("work item %s status is %q after its claim was released, want open", item.ID, item.Status)
	}
	return c.confirmWritten(ctx, item, appendedNote(reason))
}

func (c Client) AddBlocker(ctx context.Context, id, blockerID string) error {
	return c.changeBlocker(ctx, "add", "added", id, blockerID)
}

// RemoveBlocker unlinks a dependency the tracker records. It verifies what bd
// reports for the same reason adding does: a link that is still there after
// being removed would leave work looking blocked by something nobody thinks
// blocks it.
func (c Client) RemoveBlocker(ctx context.Context, id, blockerID string) error {
	return c.changeBlocker(ctx, "remove", "removed", id, blockerID)
}

func (c Client) changeBlocker(ctx context.Context, command, applied, id, blockerID string) error {
	if err := validateIssueID(id); err != nil {
		return err
	}
	if err := validateIssueID(blockerID); err != nil {
		return fmt.Errorf("invalid blocker: %w", err)
	}
	data, err := c.write(ctx, id, "dep", command, id, blockerID, "--json")
	if err != nil {
		return err
	}
	var response dependencyResponse
	if err := decodeJSON(data, &response); err != nil {
		return fmt.Errorf("decode bd dependency response: %w", err)
	}
	if response.Status != applied || response.IssueID != id || response.DependsOnID != blockerID {
		return fmt.Errorf("unexpected bd dependency response: status=%q issue=%q blocker=%q", response.Status, response.IssueID, response.DependsOnID)
	}
	return nil
}

// RecordGoalWitness records, outside an item's notes, the goal those notes
// already state. It writes no attribution and makes no judgement: the statement
// it stores is one the caller read off the item itself, which is why this can
// run over work the product manager attributed long ago without deciding
// anything on their behalf.
//
// It exists because an attribution written before the witness did is protected
// by nothing: the notes can be replaced tomorrow and the item afterwards reads
// as work nobody ever attributed. Nothing here reaches an item whose notes state
// no goal — there is nothing to witness, and writing one would turn an
// unattributed item into a permanently lost one.
//
// What bd echoes back is verified, for the reason a price is: a witness that was
// not actually stored would leave the caller believing an item was covered.
func (c Client) RecordGoalWitness(ctx context.Context, id, statement string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(statement) == "" {
		return WorkItem{}, errors.New("the goal to witness is required")
	}
	data, err := c.write(ctx, id, "update", id, "--set-metadata="+goalWitnessKey+"="+witnessValue(statement), "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if !item.GoalWitness.Recorded {
		return WorkItem{}, fmt.Errorf("work item %s carries no goal witness after being witnessed", item.ID)
	}
	return item, nil
}

// RecordCost stores what the runs made for one work item have cost, so the
// tracker itself carries the price and everything that reads the tracker sees
// it without a second data source. Like every other write here it is the
// mechanism rather than the authority: the caller is responsible for the price
// being the provider's own report and not an estimate.
//
// What bd echoes back is verified, for the reason an edit is: a price that was
// not actually stored would leave the item looking unpriced while the caller
// believed the ledger had been written.
func (c Client) RecordCost(ctx context.Context, id string, cost Cost) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if err := cost.Validate(); err != nil {
		return WorkItem{}, fmt.Errorf("invalid work item cost: %w", err)
	}
	data, err := c.write(ctx, id, "update", id,
		"--set-metadata="+costTotalKey+"="+formatCost(cost.TotalUSD),
		"--set-metadata="+costRunsKey+"="+strconv.Itoa(cost.Runs),
		"--set-metadata="+costUnknownKey+"="+strconv.Itoa(cost.UnknownRuns),
		"--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Cost == nil {
		return WorkItem{}, fmt.Errorf("work item %s carries no cost after being priced", item.ID)
	}
	// The stored total is compared at the precision it was written with, because
	// bd stores it as a number and returns whatever that number renders as.
	if formatCost(item.Cost.TotalUSD) != formatCost(cost.TotalUSD) ||
		item.Cost.Runs != cost.Runs || item.Cost.UnknownRuns != cost.UnknownRuns {
		return WorkItem{}, fmt.Errorf("work item %s cost is %#v after being priced, want %#v", item.ID, *item.Cost, cost)
	}
	return item, nil
}

// RecordLanding stores the revision the harness is closing a conversation-carried
// item on, or clears it with an empty value. It is written before the close so
// the item carries it whatever becomes of the close, and it is read back for the
// reason a parking is: what rests on it is that a reopened item is not closed
// again on the same revision, and a marker reported as set and not stored is
// exactly the item the next pull closes a second time.
func (c Client) RecordLanding(ctx context.Context, id, landing string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	landing = strings.TrimSpace(landing)
	data, err := c.write(ctx, id, "update", id, "--set-metadata="+LandingKey+"="+landing, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Landing != landing {
		return WorkItem{}, fmt.Errorf("work item %s landing is %q after being recorded, want %q", item.ID, item.Landing, landing)
	}
	return item, nil
}

// RecordOrigin stores an origin on an item that has none. It is the one-time
// backfill's write and nothing else's: an admission records its origin in the
// write that admits it, so an item that already carries one is refused rather
// than rewritten, because an origin is what happened and nothing later changes
// what happened. The origin is read back for the reason a price is.
func (c Client) RecordOrigin(ctx context.Context, id string, origin domain.WorkItemOrigin) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	origin = origin.Trimmed()
	if err := origin.Validate(); err != nil {
		return WorkItem{}, fmt.Errorf("invalid work item origin: %w", err)
	}
	current, err := c.Show(ctx, id)
	if err != nil {
		return WorkItem{}, err
	}
	if current.Origin.Known() {
		return WorkItem{}, fmt.Errorf("work item %s already records its origin (%s); an origin is never rewritten", id, current.Origin.Describe())
	}
	entries := originEntries(origin)
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := []string{"update", id}
	for _, key := range keys {
		args = append(args, "--set-metadata="+key+"="+entries[key])
	}
	args = append(args, "--json")
	data, err := c.write(ctx, id, args...)
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Origin != origin {
		return WorkItem{}, fmt.Errorf("work item %s origin is %+v after being recorded, want %+v", item.ID, item.Origin, origin)
	}
	return item, nil
}

func formatCost(total float64) string {
	return strconv.FormatFloat(total, 'f', costPrecision, 64)
}

func (c Client) Complete(ctx context.Context, id, reason string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItem{}, errors.New("completion reason is required")
	}
	data, err := c.write(ctx, id, "close", id, "--reason="+reason, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

// Reopen returns a claimed item to the backlog, carrying into its notes the
// reason it was not discharged and into its parking whatever the caller decided
// it should sit under. It is what a run that landed evidence rather than the work
// leaves behind: the change integrated, so there is nothing to block on and
// nobody to hand it to, and what is left to decide is whether the item is to be
// pulled again.
//
// The parking is applied in the same invocation as the status rather than in a
// second call, because the window between the two is exactly when a watch
// session pulls the item: an item returned to the backlog unparked is pullable
// the moment the status lands. An empty parking releases the item into the queue
// and is a decision, not an omission — the caller that gives one is holding the
// item back some other way.
//
// A release here is unconditional: it clears whatever parking the item carried,
// including one somebody else placed for their own reasons. That is why the
// parking is the caller's whole answer rather than a change to what is there —
// this call cannot tell a parking it is superseding from one it is retiring, so a
// caller that must not retire one reads the item and passes what it finds back.
//
// The status and the parking are both read back, for the reason a blocker's
// status is. An item left claimed by a run that has ended is work nothing can
// start and nothing is watching; an item returned unparked when the caller asked
// for a parking is work the next pull selects again for another run of the same
// diagnosis.
func (c Client) Reopen(ctx context.Context, id, reason string, parking domain.WorkItemParking) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItem{}, errors.New("reopen reason is required")
	}
	if err := errors.Join(parkingProblem(parking)...); err != nil {
		return WorkItem{}, err
	}
	data, err := c.write(ctx, id, "update", id, "--status=open", "--append-notes="+reason,
		"--set-metadata="+parkedKey+"="+parking.Reason(), "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Status != "open" {
		return WorkItem{}, fmt.Errorf("work item %s status is %q after being reopened, want open", item.ID, item.Status)
	}
	if item.Parking.Reason() != parking.Reason() {
		if parking.Parked() {
			return WorkItem{}, fmt.Errorf("work item %s is parked %q after being reopened, want %q", item.ID, item.Parking, parking.Reason())
		}
		return WorkItem{}, fmt.Errorf("work item %s is still parked %q after being reopened unparked", item.ID, item.Parking)
	}
	return c.confirmWritten(ctx, item, appendedNote(reason))
}

// maxBDOutputBytes is how much of one bd invocation's output this client keeps.
// It is far above the process runner's general default of 8 MiB because a bd
// listing is one JSON document that has to be read whole, and it grows with the
// tracker: on 2026-09-26 a listing of every item including closed ones (719 of
// them, --all since yoyodyne-ifd.433.7) reached 8.7 MiB, the runner kept the
// first 8, and every listing-backed path failed at once, the admission guard
// among them, so no role could admit work. At roughly 12 KiB an item this bound
// is some twenty thousand items away; outgrowing it is refused by name below,
// never decoded as a half.
const maxBDOutputBytes = 256 << 20

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	runner := c.Runner
	if runner == nil {
		return nil, errors.New("bd process runner is required")
	}
	binary := c.Binary
	if binary == "" {
		binary = "bd"
	}
	command := execution.Command{
		Name:           binary,
		Args:           args,
		Dir:            c.Dir,
		Env:            withoutAutoExport(os.Environ()),
		Timeout:        c.timeout(),
		MaxOutputBytes: maxBDOutputBytes,
	}
	var raw boundedExportOutput
	if args[0] == "export" {
		command.RawStdout = &raw
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return nil, fmt.Errorf("run bd %s: %w", args[0], err)
	}
	// A cut copy of bd's output is not bd's answer, whatever it decodes to. The
	// runner marks a copy it had to cut, and this refuses it in those words
	// rather than handing half a listing to a JSON decoder, whose complaint
	// about a stray bracket named nothing anybody could act on.
	if result.OutputTruncation != "" {
		if args[0] == "list" || args[0] == "ready" {
			return nil, fmt.Errorf("bd %s output was cut: read %d complete work item(s); work may be missing: %s", args[0], completeWorkItems(result.Stdout), result.OutputTruncation)
		}
		return nil, fmt.Errorf("bd %s wrote more than the %d bytes this client retains, so its output was cut and is not read: %s", args[0], maxBDOutputBytes, result.OutputTruncation)
	}
	if result.Status != execution.ProcessSucceeded {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		return nil, processFailure{verb: args[0], status: result.Status, exitCode: result.ExitCode, message: message}
	}
	if args[0] == "list" || args[0] == "ready" {
		if warning := listingCutWarning(result.Stderr); warning != "" {
			return nil, fmt.Errorf("bd %s list was cut: read %d complete work item(s); work may be missing: %s", args[0], completeWorkItems(result.Stdout), warning)
		}
	}
	if command.RawStdout != nil {
		return raw.Bytes(), nil
	}
	return []byte(result.Stdout), nil
}

// processFailure is bd having run and refused: the verb, how the process ended,
// and what it wrote. It is a type rather than a formatted string so a caller can
// tell a refusal bd wrote from a bd that could not be started, which the
// formatted message alone does not say.
type processFailure struct {
	verb     string
	status   execution.ProcessStatus
	exitCode int
	message  string
}

func (f processFailure) Error() string {
	return fmt.Sprintf("bd %s failed with status %s and exit code %d: %s", f.verb, f.status, f.exitCode, f.message)
}

type rawWorkItem struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Design             string          `json:"design"`
	AcceptanceCriteria string          `json:"acceptance_criteria"`
	Notes              string          `json:"notes"`
	Status             string          `json:"status"`
	Priority           int             `json:"priority"`
	IssueType          string          `json:"issue_type"`
	Assignee           string          `json:"assignee"`
	Parent             string          `json:"parent"`
	Dependencies       []rawDependency `json:"dependencies"`
	Labels             []string        `json:"labels"`
	// CreatedAt is read as text and parsed here rather than decoded as a time,
	// because it is one field of an item and not the item: a tracker that wrote a
	// timestamp this cannot read must leave the admission time unknown, not fail
	// every read of the work it belongs to.
	CreatedAt string `json:"created_at"`
	// Metadata is the tracker's own key-value store on an item. Only the keys
	// the harness writes are read out of it; everything else in there belongs to
	// whoever put it there.
	Metadata map[string]json.RawMessage `json:"metadata"`
}

type rawDependency struct {
	ID             string `json:"id"`
	IssueID        string `json:"issue_id"`
	DependsOnID    string `json:"depends_on_id"`
	DependencyType string `json:"dependency_type"`
	Type           string `json:"type"`
	Status         string `json:"status"`
}

type dependencyResponse struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Status      string `json:"status"`
}

func decodeSingleWorkItem(data []byte) (WorkItem, error) {
	items, err := decodeWorkItems(data)
	if err != nil {
		return WorkItem{}, err
	}
	if len(items) != 1 {
		return WorkItem{}, fmt.Errorf("bd returned %d work items, want 1", len(items))
	}
	return items[0], nil
}

func decodeWorkItems(data []byte) ([]WorkItem, error) {
	var rawItems []rawWorkItem
	if err := decodeJSON(data, &rawItems); err != nil {
		return nil, fmt.Errorf("decode bd work item: %w", err)
	}
	items := make([]WorkItem, 0, len(rawItems))
	for _, raw := range rawItems {
		item, err := convertWorkItem(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func convertWorkItem(raw rawWorkItem) (WorkItem, error) {
	if err := validateIssueID(raw.ID); err != nil {
		return WorkItem{}, fmt.Errorf("bd returned invalid work item: %w", err)
	}
	item := WorkItem{
		ID:                 raw.ID,
		Title:              raw.Title,
		Description:        raw.Description,
		Design:             raw.Design,
		AcceptanceCriteria: raw.AcceptanceCriteria,
		Notes:              raw.Notes,
		Status:             raw.Status,
		Priority:           raw.Priority,
		IssueType:          raw.IssueType,
		Assignee:           raw.Assignee,
		Parent:             raw.Parent,
		CreatedAt:          admittedAt(raw.CreatedAt),
		Dependencies:       make([]Dependency, 0, len(raw.Dependencies)),
	}
	for _, dependency := range raw.Dependencies {
		id := dependency.ID
		if id == "" {
			id = dependency.DependsOnID
		}
		dependencyType := dependency.DependencyType
		if dependencyType == "" {
			dependencyType = dependency.Type
		}
		if id != "" {
			item.Dependencies = append(item.Dependencies, Dependency{
				IssueID: dependency.IssueID,
				ID:      id,
				Type:    dependencyType,
				Status:  dependency.Status,
			})
		}
	}
	// Labels are carried as bd lists them, blanks dropped: an empty label is
	// nothing the harness writes and nothing a reader could act on.
	for _, label := range raw.Labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			item.Labels = append(item.Labels, trimmed)
		}
	}
	item.Cost = costFromMetadata(raw.Metadata)
	item.GoalWitness = goalWitnessIn(raw.Metadata)
	if encoded := metadataString(raw.Metadata, relevantGoalsKey); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &item.RelevantGoals); err != nil {
			return WorkItem{}, fmt.Errorf("work item %s relevant goals: %w", item.ID, err)
		}
	}
	item.Executor = executorIn(raw.Metadata)
	item.Parking = parkingIn(raw.Metadata)
	item.Landing = metadataString(raw.Metadata, LandingKey)
	item.Origin = originIn(raw.Metadata)
	return item, nil
}

// metadataString reads one string-valued metadata key, trimmed, and nothing
// for a key that is absent or is not a string.
func metadataString(metadata map[string]json.RawMessage, key string) string {
	raw, present := metadata[key]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// HasLabel reports whether the item carries one label, compared exactly: bd
// stores a label as it was written, so "Reliability" and "reliability" are two
// labels, and a reader that folded them would report a label nobody applied.
func (w WorkItem) HasLabel(label string) bool {
	return slices.Contains(w.Labels, strings.TrimSpace(label))
}

// labelsMissing names the labels a write was told to put on an item that the
// item does not carry, and labelsCarried names the ones it was told to take off
// that it still does. Both are empty for a write that named none.
func labelsMissing(item WorkItem, wanted []string) []string {
	var missing []string
	for _, label := range wanted {
		if !item.HasLabel(label) {
			missing = append(missing, strings.TrimSpace(label))
		}
	}
	return missing
}

func labelsCarried(item WorkItem, unwanted []string) []string {
	var kept []string
	for _, label := range unwanted {
		if item.HasLabel(label) {
			kept = append(kept, strings.TrimSpace(label))
		}
	}
	return kept
}

// parkingIn reads why the tracker records an item as parked. An absent key and
// an empty value are both unparked, which is what work nobody ever parked and
// work somebody released both look like.
//
// Anything but a string is read as unparked, and that is the opposite of how the
// executor beside it is read, deliberately. An unreadable executor is carried
// through because the safe answer there is "no run may take this"; here the
// unreadable case is a value the harness never writes, and treating one as a
// parking would take an item out of the queue with nothing to say why or how to
// get it back.
func parkingIn(metadata map[string]json.RawMessage) domain.WorkItemParking {
	raw, present := metadata[parkedKey]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return domain.WorkItemParking(strings.TrimSpace(value))
}

// executorIn reads what the tracker records about who carries an item's
// execution. A value the harness does not recognize is carried through as it was
// written rather than dropped: the caller asks whether the item is a developer
// run, and something nobody can read was still put there by somebody who meant
// it was not one. Dropping it would answer "developer run" and spend the run
// this marker exists to save.
//
// Anything but a string is nothing at all, because nothing but a string is ever
// written here — the harness writes one at creation and one on an update, and
// neither can produce a number or an object.
func executorIn(metadata map[string]json.RawMessage) domain.WorkItemExecutor {
	raw, present := metadata[executorKey]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return domain.WorkItemExecutor(strings.TrimSpace(value))
}

// goalWitnessIn reads what the tracker records about a goal written onto an
// item: that one was, and the words where it kept them. Anything the key holds
// but a false or empty value counts as a witness, because the harness writes it
// two ways — a creation's JSON object and an update's key=value, which bd does
// not store as the same type — and because what is being asked first is whether
// the key is there at all. Reading it strictly would turn the tracker's own
// coercion into a destroyed attribution reported as a gap, which is the failure
// this exists to catch. A value that is a bare marker rather than a statement
// witnesses the loss without its words, which is what an item witnessed before
// the words were kept carries.
func goalWitnessIn(metadata map[string]json.RawMessage) goal.Witness {
	raw, present := metadata[goalWitnessKey]
	if !present {
		return goal.Witness{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return goal.Witness{}
	}
	switch witnessed := value.(type) {
	case nil:
		return goal.Witness{}
	case bool:
		return goal.Witness{Recorded: witnessed}
	case string:
		trimmed := strings.TrimSpace(witnessed)
		if trimmed == "" || trimmed == "0" || strings.EqualFold(trimmed, "false") {
			return goal.Witness{}
		}
		if trimmed == "1" || strings.EqualFold(trimmed, "true") {
			return goal.Witness{Recorded: true}
		}
		return goal.Witness{Recorded: true, Statement: trimmed}
	case float64:
		return goal.Witness{Recorded: witnessed != 0}
	default:
		return goal.Witness{Recorded: true}
	}
}

// admittedAt reads when the tracker says an item was recorded, and returns the
// zero time when it says nothing this can read. An unknown admission time is a
// fact a caller can report; a guessed one would date work to whenever the format
// happened to fail.
func admittedAt(recorded string) time.Time {
	admitted, err := time.Parse(time.RFC3339, strings.TrimSpace(recorded))
	if err != nil {
		return time.Time{}
	}
	return admitted.UTC()
}

// costFromMetadata reads the price the tracker carries, or nothing at all when
// it carries none. A partial record is read as no price rather than as a cheap
// one: the total alone would not say how many runs it covers or whether any of
// them went unpriced, and a floor presented as a price is worse than silence.
func costFromMetadata(metadata map[string]json.RawMessage) *Cost {
	if len(metadata) == 0 {
		return nil
	}
	total, ok := metadataNumber(metadata, costTotalKey)
	if !ok {
		return nil
	}
	runs, ok := metadataNumber(metadata, costRunsKey)
	if !ok {
		return nil
	}
	// The unknown count is absent from an item all of whose runs were priced,
	// because bd drops a key it was never given rather than storing a zero.
	unknown, _ := metadataNumber(metadata, costUnknownKey)
	cost := Cost{TotalUSD: total, Runs: int(runs), UnknownRuns: int(unknown)}
	if cost.Validate() != nil {
		return nil
	}
	return &cost
}

func metadataNumber(metadata map[string]json.RawMessage, key string) (float64, bool) {
	raw, present := metadata[key]
	if !present {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		// A metadata value that is not a number was not written by the harness,
		// or was written over by hand. Either way it is not a price to report.
		return 0, false
	}
	return value, true
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not supported")
		}
		return err
	}
	return nil
}

func (n NewWorkItem) validate() error {
	var problems []error
	if strings.TrimSpace(n.Title) == "" {
		problems = append(problems, errors.New("title is required"))
	}
	if strings.TrimSpace(n.Description) == "" {
		problems = append(problems, errors.New("description is required"))
	}
	if !issueTypePattern.MatchString(n.Type) {
		problems = append(problems, fmt.Errorf("invalid Beads issue type %q", n.Type))
	}
	if parent := strings.TrimSpace(n.Parent); parent != "" {
		if err := validateIssueID(parent); err != nil {
			problems = append(problems, fmt.Errorf("invalid parent: %w", err))
		}
	}
	if n.Priority != nil && (*n.Priority < 0 || *n.Priority > MaxPriority) {
		problems = append(problems, fmt.Errorf("priority %d is outside 0..%d", *n.Priority, MaxPriority))
	}
	problems = append(problems, executorProblem(n.Executor)...)
	problems = append(problems, parkingProblem(n.Parking)...)
	problems = append(problems, labelProblems(n.Labels)...)
	if n.Origin.Known() {
		if err := n.Origin.Trimmed().Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if err := goal.ValidateRelevant(n.RelevantGoals); err != nil {
		problems = append(problems, err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid new work item: %w", errors.Join(problems...))
	}
	return nil
}

func (c WorkItemChange) validate() error {
	if err := goal.ValidateRelevant(c.RelevantGoals); err != nil {
		return err
	}
	var problems []error
	if strings.TrimSpace(c.Title) == "" &&
		strings.TrimSpace(c.Description) == "" &&
		strings.TrimSpace(c.AppendNotes) == "" &&
		strings.TrimSpace(string(c.Executor)) == "" &&
		c.RelevantGoals == nil && c.Priority == nil && c.Parent == nil && c.Parking == nil &&
		len(c.AddLabels) == 0 && len(c.RemoveLabels) == 0 {
		problems = append(problems, errors.New("an update must change something"))
	}
	problems = append(problems, executorProblem(c.Executor)...)
	if c.Parking != nil {
		problems = append(problems, parkingProblem(*c.Parking)...)
	}
	problems = append(problems, labelProblems(c.AddLabels)...)
	problems = append(problems, labelProblems(c.RemoveLabels)...)
	// A label both added and removed in one write is a write bd would carry out
	// in whichever order it pleases, and the item afterwards says nothing about
	// which was meant.
	for _, label := range c.AddLabels {
		if slices.Contains(c.RemoveLabels, label) {
			problems = append(problems, fmt.Errorf("label %q is both added and removed", label))
		}
	}
	if strings.ContainsAny(c.Title, "\r\n") {
		problems = append(problems, errors.New("title cannot span lines"))
	}
	if c.Priority != nil && (*c.Priority < 0 || *c.Priority > MaxPriority) {
		problems = append(problems, fmt.Errorf("priority %d is outside 0..%d", *c.Priority, MaxPriority))
	}
	if c.Parent != nil {
		// An empty parent detaches the item, which is a request bd accepts; any
		// other value has to name an item that could exist.
		if parent := strings.TrimSpace(*c.Parent); parent != "" {
			if err := validateIssueID(parent); err != nil {
				problems = append(problems, fmt.Errorf("invalid parent: %w", err))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid work item update: %w", errors.Join(problems...))
	}
	return nil
}

// executorProblem refuses an executor the harness does not recognize, wherever
// it is written. It is the other half of reading an unrecognized marker as work
// no run may take: the read is deliberately permissive so a typo cannot cost a
// run, and this is what keeps that case from being how the marker is normally
// written. An absent executor is the ordinary case and never a problem.
func executorProblem(executor domain.WorkItemExecutor) []error {
	trimmed := domain.WorkItemExecutor(strings.TrimSpace(string(executor)))
	if trimmed == "" || trimmed.Valid() {
		return nil
	}
	named := make([]string, 0, len(domain.WorkItemExecutors))
	for _, known := range domain.WorkItemExecutors {
		named = append(named, fmt.Sprintf("%q", known))
	}
	// The bare marker is refused here like anything else that may not be written,
	// but it is not unrecognized — items marked before the marker named a role
	// carry it and are read exactly as they were — so what it is told is what it
	// is missing rather than that nobody knows the word.
	if trimmed == domain.WorkItemExecutorConversation {
		return []error{fmt.Errorf("executor %q does not say whose conversation carries the work; the executors that do are: %s",
			executor, strings.Join(named, ", "))}
	}
	return []error{fmt.Errorf("executor %q is not one the harness recognizes; the executors there are: %s",
		executor, strings.Join(named, ", "))}
}

// parkingProblem refuses a parking reason the tracker could not hold as one
// value on one line. An empty parking is a release and is never a problem.
//
// It is checked where it is written rather than folded on the way in, because
// what is being stored is somebody's account of a decision: a reason silently
// cut short, or one that reads as parked-for-nothing after the newlines are
// squeezed out, is worse than a refusal that says to write it shorter.
func parkingProblem(parking domain.WorkItemParking) []error {
	reason := parking.Reason()
	if reason == "" {
		return nil
	}
	var problems []error
	if strings.ContainsAny(reason, "\r\n") {
		problems = append(problems, errors.New("a parking reason cannot span lines"))
	}
	if len(reason) > domain.MaxWorkItemParkingBytes {
		problems = append(problems, fmt.Errorf("a parking reason of %d bytes exceeds the %d byte bound", len(reason), domain.MaxWorkItemParkingBytes))
	}
	return problems
}

// ValidateIssueID reports whether a string can name a Beads issue. It is
// exported so a caller can refuse an identifier it was handed before building a
// command around it, rather than discovering the problem as a bd failure.
func ValidateIssueID(id string) error {
	return validateIssueID(id)
}

// ValidateLabel refuses a label the harness will not write: anything that is
// not one identifier-shaped token within MaxLabelBytes. It is exported for the
// reason ValidateIssueID is, and for one more — bd would accept what this
// refuses, so a caller that did not ask would find out from nobody. The rule is
// the domain's, so a developer slot's preference and the label an action writes
// are held to one spelling.
func ValidateLabel(label string) error {
	return domain.ValidateLabel(label)
}

// labelProblems refuses every label in a list that ValidateLabel would, and a
// list that names one label twice: bd would store it once, and the caller was
// asking for something it had already asked for.
func labelProblems(labels []string) []error {
	var problems []error
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if err := ValidateLabel(label); err != nil {
			problems = append(problems, err)
			continue
		}
		trimmed := strings.TrimSpace(label)
		if _, repeated := seen[trimmed]; repeated {
			problems = append(problems, fmt.Errorf("label %q is named twice", trimmed))
		}
		seen[trimmed] = struct{}{}
	}
	return problems
}

func validateIssueID(id string) error {
	if !issueIDPattern.MatchString(id) {
		return fmt.Errorf("invalid Beads issue id %q", id)
	}
	return nil
}
