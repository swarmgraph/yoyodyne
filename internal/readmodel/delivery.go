package readmodel

// The factory's delivery measures, modelled on the ones software teams
// commonly use for how often and how safely they ship, day by day and week by
// week in Pacific time, for the dashboard's delivery page (the delivery page
// in docs/designs/observability-and-dashboard.md).
//
// A deployment is a change merged to main: an item whose run recorded a
// promotion, placed when that run recorded completing, exactly as the
// throughput's landed count and the shipped ledger place it (EndedAt). Every
// other measure is read against those merges, from records that already exist
// and nothing kept for this reading alone:
//
//   - lead time, from the item's filing in the tracker to its merge, split into
//     the time before the first run and what each run's event log says it was
//     doing — developing, running checks, under review, repairing, merging. No
//     stage is read from a dependency record (the design says why);
//   - change failure, from the repository's own history of reverts, the
//     landing checks each promoting run recorded on its merge commit, and the
//     bugs filed in the tracker within 48 hours that cite the change;
//   - recovery, from the runs of failed recurring passes, the scheduler's stall
//     log, and the factory stall log: how long each stayed broken, counted where
//     it cleared;
//   - rework, from the kind each merged item records;
//   - hand steps, from the recorded hand steps, counted by CountInterventions;
//   - first pass, from each change's event logs: its first check stage and its
//     first review verdict;
//   - tokens, from the usage the provider reported for each invocation.
//
// Every period is a calendar day or a week from Monday to Sunday in Pacific time,
// named rather than taken from the machine, so the same records read the same
// on a machine in any zone. A measure whose source could not be read says so in
// its own words under not_recorded and carries no figure; one read from only
// some of its sources names the others under not_read and is a floor. Nothing
// unknown is reported as zero.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the delivery zone is named, so it must load on a machine with no zone database

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DeliveryZoneName is the zone every delivery period is counted in. It is
// Pacific time because that is the operator's, and it is named rather than read
// from the machine so a reading does not change with where it is taken.
const DeliveryZoneName = "America/Los_Angeles"

const (
	// deliveryDays and deliveryWeeks are how many periods each series covers,
	// the current one included: a month of days and a quarter of weeks.
	deliveryDays  = 30
	deliveryWeeks = 13
	// defectWindow is how long after a merge a bug citing the change counts it
	// as failed.
	defectWindow = 48 * time.Hour
)

// LeadStage is one stage of a change's lead time, in the order a change passes
// through them. Queued is filing to first run; repairing is every stretch spent
// putting right what checks or a review found, and the time between one run of
// the item ending and the next starting. It is a vocabulary of its own rather
// than Stage, which folds a run's phases onto the three parts a pipeline shows.
type LeadStage string

const (
	LeadQueued     LeadStage = "queued"
	LeadDeveloping LeadStage = "developing"
	LeadChecks     LeadStage = "checks"
	LeadReview     LeadStage = "review"
	LeadRepairing  LeadStage = "repairing"
	LeadMerging    LeadStage = "merging"
)

var deliveryStages = []LeadStage{LeadQueued, LeadDeveloping, LeadChecks, LeadReview, LeadRepairing, LeadMerging}

// reviewApproved is the decision a review.completed event records for an
// approval: review.DecisionApprove, written here because that package reads
// this one.
const reviewApproved = "approve"

// DeliveryTracker is the tracker as the delivery reading reads it: every item,
// closed included. It is satisfied by beads.Client.
type DeliveryTracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// DeliveryRuns is the run records and each run's event log. It is satisfied by
// *runstate.Store.
type DeliveryRuns interface {
	Recorded() ([]runstate.State, error)
	LoadEvents(runID string) ([]execution.Event, error)
}

// DeliveryUsage is the usage the provider reported for each invocation. It is
// satisfied by *runstate.SpendStore.
type DeliveryUsage interface {
	List() ([]runstate.Spend, error)
}

// DeliveryHandSteps is the recorded hand steps. It is satisfied by
// *runstate.InterventionStore.
type DeliveryHandSteps interface {
	List() ([]intervention.Event, error)
}

// DeliveryStalls is the scheduler's stall log, satisfied by *runstate.StallStore.
type DeliveryStalls interface {
	List() ([]runstate.StallEvent, error)
}

// DeliveryFactoryStalls is the factory stall log, satisfied by
// *runstate.FactoryStallStore.
type DeliveryFactoryStalls interface {
	List() ([]runstate.FactoryStallEvent, error)
}

// DeliveryReverts is the repository's history of reverts on a branch. It is
// satisfied by *gitworktree.Manager.
type DeliveryReverts interface {
	RevertsOn(ctx context.Context, branch string, since time.Time) ([]gitworktree.Revert, error)
}

// DeliverySources are the records one delivery reading is assembled from. Each
// is optional, and each carries the reason its caller could not open it, so a
// reading without one says why rather than that nothing was wired.
type DeliverySources struct {
	Tracker        DeliveryTracker
	TrackerProblem string
	// TrackerTimeout bounds the one tracker listing the reading makes.
	TrackerTimeout time.Duration
	// TrackerReadings is the product's one shared reading of the tracker. Where
	// it is set and the tracker can answer a status reading (Tracker), the
	// listing is read out of the reading every status reading shares rather
	// than asked of the tracker (sharedlisting.go).
	TrackerReadings      TrackerReadings
	Runs                 DeliveryRuns
	RunsProblem          string
	Usage                DeliveryUsage
	UsageProblem         string
	HandSteps            DeliveryHandSteps
	HandStepsProblem     string
	Passes               Passes
	PassesProblem        string
	Stalls               DeliveryStalls
	StallsProblem        string
	FactoryStalls        DeliveryFactoryStalls
	FactoryStallsProblem string
	Reverts              DeliveryReverts
	RevertsProblem       string
	// Zone is the zone the periods are counted in. Nil is DeliveryZoneName; a
	// test names another only to show it is the zone, not the machine, that
	// decides.
	Zone *time.Location
	// Now stamps the reading and anchors the periods. It defaults to the wall
	// clock.
	Now func() time.Time
}

// Names of the sources, as the reading says them.
const (
	sourceTracker       = "the tracker"
	sourceRuns          = "the run records"
	sourceEvents        = "the runs' event logs"
	sourceUsage         = "the usage the provider reported"
	sourceHandSteps     = "the recorded hand steps"
	sourcePasses        = "the recurring passes"
	sourceStalls        = "the scheduler's stall log"
	sourceFactoryStalls = "the factory stall log"
	sourceReverts       = "the repository's history of the target branch"
)

// Delivery is the reading: the days and the weeks, oldest first, and how each
// source read.
type Delivery struct {
	ObservedAt time.Time `json:"observed_at"`
	// Zone is the zone every period is counted in, by name.
	Zone    string           `json:"zone"`
	Days    []DeliveryPeriod `json:"days"`
	Weeks   []DeliveryPeriod `json:"weeks"`
	Sources []DeliverySource `json:"sources"`
}

// DeliverySource is one source the reading was assembled from, and whether it
// could be read.
type DeliverySource struct {
	Name    string `json:"name"`
	Read    bool   `json:"read"`
	Problem string `json:"problem,omitempty"`
}

// DeliveryPeriod is every measure over one day or one week.
type DeliveryPeriod struct {
	// Label is the day, or the Monday a week starts on, as a date in the zone.
	Label string    `json:"label"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Current marks the period the reading was taken in, which is not over.
	Current bool `json:"current,omitempty"`

	Merges        DeliveryMerges        `json:"merges"`
	LeadTime      DeliveryLeadTime      `json:"lead_time"`
	ChangeFailure DeliveryChangeFailure `json:"change_failure"`
	Recovery      DeliveryRecovery      `json:"recovery"`
	Rework        DeliveryRework        `json:"rework"`
	HandSteps     DeliveryHandStepCount `json:"hand_steps"`
	FirstPass     DeliveryFirstPass     `json:"first_pass"`
	Tokens        DeliveryTokens        `json:"tokens"`
}

// DeliveryMeasure is what every measure says about where it came from. ReadFrom
// names the sources it was read from. NotRecorded is set, in place of every
// figure, where none of its sources could be read. NotRead names a source that
// could not be read while others were, which makes the figure a floor.
type DeliveryMeasure struct {
	ReadFrom    []string `json:"read_from"`
	NotRecorded string   `json:"not_recorded,omitempty"`
	NotRead     []string `json:"not_read,omitempty"`
}

// DeliveryMerges is the deployments: the changes merged in the period.
type DeliveryMerges struct {
	DeliveryMeasure
	Count   *int              `json:"count,omitempty"`
	Changes []DeliveredChange `json:"changes,omitempty"`
}

// DeliveredChange is one merged change.
type DeliveredChange struct {
	WorkItemID   string    `json:"work_item_id"`
	Title        string    `json:"title,omitempty"`
	RunID        string    `json:"run_id"`
	MergedAt     time.Time `json:"merged_at"`
	TargetBranch string    `json:"target_branch,omitempty"`
	PullRequest  int       `json:"pull_request,omitempty"`
}

// DurationSummary is a set of durations: how many, their median and 90th
// percentile, and the spread around them. Each is the nearest-rank value of
// the sorted durations, so every figure is one a change actually took.
type DurationSummary struct {
	Count         int   `json:"count"`
	MedianSeconds int64 `json:"median_seconds"`
	P90Seconds    int64 `json:"p90_seconds"`
	MinSeconds    int64 `json:"min_seconds"`
	P25Seconds    int64 `json:"p25_seconds"`
	P75Seconds    int64 `json:"p75_seconds"`
	MaxSeconds    int64 `json:"max_seconds"`
}

// CountSummary is a set of counts summarized the same way.
type CountSummary struct {
	Count  int `json:"count"`
	Median int `json:"median"`
	P90    int `json:"p90"`
	Max    int `json:"max"`
}

// DeliveryLeadTime is filing to merge, whole and by stage.
type DeliveryLeadTime struct {
	DeliveryMeasure
	// Total is filing to merge, over the changes whose filing the tracker
	// records; FilingUnknown counts the rest.
	Total         *DurationSummary `json:"total,omitempty"`
	FilingUnknown int              `json:"filing_unknown,omitempty"`
	// Stages is each stage in order. A stage no change's records could time
	// says so under its own not_recorded.
	Stages []DeliveryStage `json:"stages,omitempty"`
	// StagesUnknown counts the changes whose promoting run's event log holds
	// nothing, so no stage after queued could be timed for them.
	StagesUnknown int `json:"stages_unknown,omitempty"`
	// ReviewRounds and RepairRounds count, per change, the review verdicts and
	// the repairs before the merge.
	ReviewRounds *CountSummary `json:"review_rounds,omitempty"`
	RepairRounds *CountSummary `json:"repair_rounds,omitempty"`
}

// DeliveryStage is one stage of the lead time.
type DeliveryStage struct {
	Stage       LeadStage        `json:"stage"`
	Duration    *DurationSummary `json:"duration,omitempty"`
	NotRecorded string           `json:"not_recorded,omitempty"`
}

// DeliveryChangeFailure is the share of merged changes that failed: reverted,
// followed by main failing its checks on the merge commit, or cited by a bug
// filed within 48 hours.
type DeliveryChangeFailure struct {
	DeliveryMeasure
	Merges   int      `json:"merges"`
	Failed   *int     `json:"failed,omitempty"`
	Rate     *float64 `json:"rate,omitempty"`
	Reverted int      `json:"reverted"`
	RedAfter int      `json:"red_after_merge"`
	Defects  int      `json:"defect_within_48h"`
	// Settling counts changes merged less than 48 hours before the reading, for
	// which a bug may yet be filed.
	Settling int `json:"settling,omitempty"`
	// LandingUnchecked counts changes whose promoting run recorded no verdict of
	// the landing checks on its merge commit.
	LandingUnchecked int            `json:"landing_unchecked,omitempty"`
	FailedChanges    []FailedChange `json:"failed_changes,omitempty"`
}

// FailedChange is one merged change that failed, and every way it did.
type FailedChange struct {
	WorkItemID string   `json:"work_item_id"`
	Why        []string `json:"why"`
}

// DeliveryRecovery is how long the factory itself stayed broken: each run of
// failed recurring passes, each scheduler stall, and each factory stall that
// cleared in the period, from when it began to when it cleared.
type DeliveryRecovery struct {
	DeliveryMeasure
	Recovered *DurationSummary   `json:"recovered,omitempty"`
	Breakages []DeliveryBreakage `json:"breakages,omitempty"`
	// StillBroken is what has not cleared, on the current period alone.
	StillBroken []DeliveryBreakage `json:"still_broken,omitempty"`
}

// DeliveryBreakage is one stretch of the factory being broken.
type DeliveryBreakage struct {
	Kind      string     `json:"kind"`
	Subject   string     `json:"subject,omitempty"`
	Since     time.Time  `json:"since"`
	ClearedAt *time.Time `json:"cleared_at,omitempty"`
	Seconds   int64      `json:"seconds"`
}

// The kinds of breakage recovery is read from.
const (
	BreakageFailingPasses  = "failing recurring passes"
	BreakageSchedulerStall = "scheduler stall"
	BreakageFactoryStall   = "factory stall"
)

// DeliveryRework is the share of merged changes that were bug fixes. Every
// merge whose item records no kind is counted as not a bug fix and shown
// beside the rate, so the rate is a minimum wherever any merge has no kind.
type DeliveryRework struct {
	DeliveryMeasure
	Merges   int      `json:"merges"`
	Bugs     int      `json:"bugs"`
	Features int      `json:"features"`
	Untyped  int      `json:"untyped"`
	Rate     *float64 `json:"rate,omitempty"`
	// UntypedShare is Untyped over Merges.
	UntypedShare *float64 `json:"untyped_share,omitempty"`
	// Minimum says the rate is a minimum, and why, wherever Untyped is not zero.
	Minimum string `json:"minimum,omitempty"`
}

// DeliveryHandStepCount is the operator's hand steps per merged change.
type DeliveryHandStepCount struct {
	DeliveryMeasure
	Count *InterventionCount `json:"count,omitempty"`
}

// DeliveryFirstPass is the share of merged changes whose first check run and
// first review both passed. Unknown counts the changes whose event logs do not
// record both, which the rate is not taken over.
type DeliveryFirstPass struct {
	DeliveryMeasure
	Merges  int      `json:"merges"`
	Known   int      `json:"known"`
	Passed  int      `json:"passed"`
	Unknown int      `json:"unknown"`
	Rate    *float64 `json:"rate,omitempty"`
}

// DeliveryTokens is the tokens each merged change cost, by model, across every
// run made for it. Unrecorded counts the changes no usage was recorded for,
// which the figures per change are not taken over.
type DeliveryTokens struct {
	DeliveryMeasure
	Merges     int           `json:"merges"`
	Recorded   int           `json:"recorded"`
	Unrecorded int           `json:"unrecorded"`
	ByModel    []ModelTokens `json:"by_model,omitempty"`
}

// ModelTokens is what one model was billed across the period's merged changes,
// and that over the changes usage was recorded for.
type ModelTokens struct {
	Model string `json:"model"`
	// Changes counts the merged changes the model worked on.
	Changes   int                 `json:"changes"`
	Tokens    runstate.TokenUsage `json:"tokens"`
	PerChange TokensPerChange     `json:"per_change"`
}

// TokensPerChange is a model's tokens divided by the merged changes usage was
// recorded for.
type TokensPerChange struct {
	Input         float64 `json:"input"`
	CacheRead     float64 `json:"cache_read"`
	CacheCreation float64 `json:"cache_creation"`
	Output        float64 `json:"output"`
	Total         float64 `json:"total"`
}

// modelNotRecorded is the model an invocation is counted under where its
// record names none.
const modelNotRecorded = "model not recorded"

// deliveredWork is one merged change and what its records say, worked out once
// and read into every period it falls in.
type deliveredWork struct {
	change    DeliveredChange
	promoting runstate.State
	runs      []runstate.State // oldest first, started no later than the merge
	item      *beads.WorkItem

	// filed is the tracker's filing, zero where it is not recorded.
	filed time.Time
	// stages is each stage's time, and timed whether the event logs could time
	// them; rounds are what the logs counted.
	stages       map[LeadStage]time.Duration
	timed        bool
	reviewRounds int
	repairRounds int
	// firstChecks and firstReview are the first check stage's and the first
	// review's results, where the logs record them.
	firstChecks *bool
	firstReview *bool

	failures []string
	tokens   map[string]runstate.TokenUsage
}

// deliveryRead is every source as it was read, and what could not be.
type deliveryRead struct {
	items           map[string]beads.WorkItem
	itemList        []beads.WorkItem
	trackerErr      string
	runs            []runstate.State
	runsErr         string
	usage           map[string][]runstate.Spend
	usageErr        string
	steps           []intervention.Event
	stepsErr        string
	breakages       []DeliveryBreakage
	recoveryRead    []string
	recoveryNotRead []string
	revertsErr      string
	reverts         map[string][]gitworktree.Revert
	eventsErr       []string
}

// ReadDelivery assembles the delivery reading from the durable records.
func ReadDelivery(ctx context.Context, sources DeliverySources) Delivery {
	now := time.Now()
	if sources.Now != nil {
		now = sources.Now()
	}
	zone := sources.Zone
	if zone == nil {
		zone = DeliveryZone()
	}
	reading := Delivery{ObservedAt: now, Zone: zone.String()}
	days := deliveryDayPeriods(now, zone)
	weeks := deliveryWeekPeriods(now, zone)
	earliest := weeks[0].Start
	if days[0].Start.Before(earliest) {
		earliest = days[0].Start
	}

	read := readDeliverySources(ctx, sources, earliest)
	work := deliveredChanges(&read, sources.Runs, earliest, now)
	reading.Sources = deliverySourceHealth(read)

	for _, series := range []*[]DeliveryPeriod{&days, &weeks} {
		for index := range *series {
			fillDeliveryPeriod(&(*series)[index], work, read, now)
		}
	}
	reading.Days, reading.Weeks = days, weeks
	return reading
}

// DeliveryZone is the zone delivery is counted in. The zone database is
// compiled in, so loading it cannot fail; were it ever to, the reading would be
// in UTC and say so by its zone's name rather than fall back to the machine's.
func DeliveryZone() *time.Location {
	zone, err := time.LoadLocation(DeliveryZoneName)
	if err != nil {
		return time.UTC
	}
	return zone
}

// deliveryDayPeriods is the last deliveryDays calendar days in the zone, oldest
// first, today last. A day is stepped by the calendar rather than by
// twenty-four hours, so the days a clock change falls on are twenty-three and
// twenty-five hours long, as they are on the operator's clock.
func deliveryDayPeriods(now time.Time, zone *time.Location) []DeliveryPeriod {
	local := now.In(zone)
	periods := make([]DeliveryPeriod, 0, deliveryDays)
	for back := deliveryDays - 1; back >= 0; back-- {
		start := time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, zone)
		end := time.Date(local.Year(), local.Month(), local.Day()-back+1, 0, 0, 0, 0, zone)
		periods = append(periods, DeliveryPeriod{Label: start.Format(time.DateOnly), Start: start, End: end, Current: back == 0})
	}
	return periods
}

// deliveryWeekPeriods is the last deliveryWeeks weeks in the zone, each from a
// Monday's midnight to the next, oldest first, the week holding today last.
func deliveryWeekPeriods(now time.Time, zone *time.Location) []DeliveryPeriod {
	local := now.In(zone)
	sinceMonday := (int(local.Weekday()) + 6) % 7
	periods := make([]DeliveryPeriod, 0, deliveryWeeks)
	for back := deliveryWeeks - 1; back >= 0; back-- {
		day := local.Day() - sinceMonday - 7*back
		start := time.Date(local.Year(), local.Month(), day, 0, 0, 0, 0, zone)
		end := time.Date(local.Year(), local.Month(), day+7, 0, 0, 0, 0, zone)
		periods = append(periods, DeliveryPeriod{Label: start.Format(time.DateOnly), Start: start, End: end, Current: back == 0})
	}
	return periods
}

// readDeliverySources reads every source once.
func readDeliverySources(ctx context.Context, sources DeliverySources, since time.Time) deliveryRead {
	read := deliveryRead{items: map[string]beads.WorkItem{}, usage: map[string][]runstate.Spend{}, reverts: map[string][]gitworktree.Revert{}}

	if sources.Tracker == nil {
		read.trackerErr = absent(sourceTracker, sources.TrackerProblem)
	} else {
		listCtx, cancel := ctx, context.CancelFunc(func() {})
		if sources.TrackerTimeout > 0 {
			listCtx, cancel = context.WithTimeout(ctx, sources.TrackerTimeout)
		}
		tracker := DeliveryTracker(sources.Tracker)
		var shared *sharedTracker
		if whole, ok := sources.Tracker.(Tracker); ok && sources.TrackerReadings != nil {
			var sharing Sources
			sharing, shared = Sources{Tracker: whole, TrackerReadings: sources.TrackerReadings, TrackerTimeout: sources.TrackerTimeout, Now: sources.Now}.sharing()
			tracker = sharing.Tracker
		}
		items, err := tracker.List(listCtx, "")
		cancel()
		shared.finish()
		if err != nil {
			read.trackerErr = fmt.Sprintf("%s could not be read: %v", sourceTracker, err)
		} else {
			read.itemList = items
			for _, item := range items {
				read.items[item.ID] = item
			}
		}
	}

	if sources.Runs == nil {
		read.runsErr = absent(sourceRuns, sources.RunsProblem)
	} else if states, err := sources.Runs.Recorded(); err != nil {
		read.runsErr = fmt.Sprintf("%s could not be read: %v", sourceRuns, err)
	} else {
		read.runs = states
	}

	if sources.Usage == nil {
		read.usageErr = absent(sourceUsage, sources.UsageProblem)
	} else if rows, err := sources.Usage.List(); err != nil {
		read.usageErr = fmt.Sprintf("%s could not be read: %v", sourceUsage, err)
	} else {
		for _, row := range rows {
			if row.RunID != "" {
				read.usage[row.RunID] = append(read.usage[row.RunID], row)
			}
		}
	}

	if sources.HandSteps == nil {
		read.stepsErr = absent(sourceHandSteps, sources.HandStepsProblem)
	} else if steps, err := sources.HandSteps.List(); err != nil {
		read.stepsErr = fmt.Sprintf("%s could not be read: %v", sourceHandSteps, err)
	} else {
		read.steps = steps
	}

	readDeliveryBreakages(&read, sources)

	if sources.Reverts == nil {
		read.revertsErr = absent(sourceReverts, sources.RevertsProblem)
	} else {
		branches := map[string]bool{}
		for _, state := range read.runs {
			if state.Integration != nil && state.Integration.TargetBranch != "" {
				branches[state.Integration.TargetBranch] = true
			}
		}
		for _, branch := range deliveryKeys(branches) {
			reverts, err := sources.Reverts.RevertsOn(ctx, branch, since)
			if err != nil {
				read.revertsErr = fmt.Sprintf("%s could not be read: %v", sourceReverts, err)
				break
			}
			read.reverts[branch] = reverts
		}
	}
	return read
}

// readDeliveryBreakages reads every stretch of the factory being broken from
// the three records that keep one.
func readDeliveryBreakages(read *deliveryRead, sources DeliverySources) {
	notRead := func(name, problem string, err error) {
		if err != nil {
			read.recoveryNotRead = append(read.recoveryNotRead, fmt.Sprintf("%s could not be read: %v", name, err))
			return
		}
		read.recoveryNotRead = append(read.recoveryNotRead, absent(name, problem))
	}

	if sources.Passes == nil {
		notRead(sourcePasses, sources.PassesProblem, nil)
	} else if passes, _, err := sources.Passes.List(); err != nil {
		notRead(sourcePasses, "", err)
	} else {
		read.recoveryRead = append(read.recoveryRead, sourcePasses)
		for _, failure := range runstate.PassFailuresOf(passes) {
			breakage := DeliveryBreakage{Kind: BreakageFailingPasses, Subject: failure.Task, Since: failure.FirstAt}
			if !failure.ClearedAt.IsZero() {
				cleared := failure.ClearedAt
				breakage.ClearedAt = &cleared
			}
			read.breakages = append(read.breakages, breakage)
		}
	}

	if sources.Stalls == nil {
		notRead(sourceStalls, sources.StallsProblem, nil)
	} else if stalls, err := sources.Stalls.List(); err != nil {
		notRead(sourceStalls, "", err)
	} else {
		read.recoveryRead = append(read.recoveryRead, sourceStalls)
		for _, stall := range stalls {
			read.breakages = append(read.breakages, DeliveryBreakage{Kind: BreakageSchedulerStall, Since: stallSince(stall.Since, stall.OpenedAt), ClearedAt: stall.ClosedAt})
		}
	}

	if sources.FactoryStalls == nil {
		notRead(sourceFactoryStalls, sources.FactoryStallsProblem, nil)
	} else if stalls, err := sources.FactoryStalls.List(); err != nil {
		notRead(sourceFactoryStalls, "", err)
	} else {
		read.recoveryRead = append(read.recoveryRead, sourceFactoryStalls)
		for _, stall := range stalls {
			read.breakages = append(read.breakages, DeliveryBreakage{Kind: BreakageFactoryStall, Since: stallSince(stall.Since, stall.OpenedAt), ClearedAt: stall.ClosedAt})
		}
	}
}

// stallSince is when a stall began: when the work last moved, or where that
// was not recorded, when it was noticed.
func stallSince(since, opened time.Time) time.Time {
	if since.IsZero() {
		return opened
	}
	return since
}

// deliveredChanges finds every change merged since the earliest period began,
// and works out what its records say about it.
func deliveredChanges(read *deliveryRead, runs DeliveryRuns, since, now time.Time) []*deliveredWork {
	byItem := map[string][]runstate.State{}
	for _, state := range read.runs {
		byItem[state.WorkItemID] = append(byItem[state.WorkItemID], state)
	}
	var work []*deliveredWork
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
		merged := EndedAt(promoting)
		if merged.Before(since) || merged.After(now) {
			continue
		}
		sort.SliceStable(states, func(first, second int) bool { return states[first].StartedAt.Before(states[second].StartedAt) })
		entry := &deliveredWork{
			change: DeliveredChange{
				WorkItemID:   itemID,
				Title:        promoting.WorkItemTitle,
				RunID:        promoting.RunID,
				MergedAt:     merged,
				TargetBranch: promoting.Integration.TargetBranch,
			},
			promoting: promoting,
		}
		if promoting.PullRequest != nil {
			entry.change.PullRequest = promoting.PullRequest.Number
		}
		for _, state := range states {
			if !state.StartedAt.After(merged) {
				entry.runs = append(entry.runs, state)
			}
		}
		if item, ok := read.items[itemID]; ok {
			entry.item = &item
			entry.filed = item.CreatedAt
			if entry.change.Title == "" {
				entry.change.Title = item.Title
			}
		}
		timeDelivery(entry, runs, read)
		judgeDeliveryFailure(entry, *read, now)
		entry.tokens = deliveryTokens(entry, *read)
		work = append(work, entry)
	}
	sort.SliceStable(work, func(first, second int) bool {
		return work[first].change.MergedAt.Before(work[second].change.MergedAt)
	})
	return work
}

// deliveryEvent is the part of an event's payload the stages are read from.
type deliveryEvent struct {
	Kind        string `json:"kind"`
	Passed      bool   `json:"passed"`
	CouldNotRun string `json:"could_not_run"`
	Decision    string `json:"decision"`
}

// timeDelivery splits the change's time from its first run to its merge into
// stages, from what each run's event log says it was doing. A run begins
// developing, or repairing where it is not the item's first; a check starting
// puts it in checks; a review starting puts it under review; a failed check
// stage, or a review that did not approve, puts it to repairing until the next
// check; an approval puts it to merging. The stretch between one run ending and
// the next starting is repairing. Nothing is timed past the merge.
func timeDelivery(entry *deliveredWork, runs DeliveryRuns, read *deliveryRead) {
	entry.stages = map[LeadStage]time.Duration{}
	if len(entry.runs) == 0 || runs == nil {
		return
	}
	merged := entry.change.MergedAt
	first := entry.runs[0].StartedAt
	if entry.runs[0].WorkItemClaimedAt != nil && entry.runs[0].WorkItemClaimedAt.Before(first) {
		first = *entry.runs[0].WorkItemClaimedAt
	}
	if !entry.filed.IsZero() && first.After(entry.filed) {
		entry.stages[LeadQueued] = first.Sub(entry.filed)
	} else if !entry.filed.IsZero() {
		entry.stages[LeadQueued] = 0
	}

	logs := make([][]execution.Event, len(entry.runs))
	for index, state := range entry.runs {
		events, err := runs.LoadEvents(state.RunID)
		if err != nil {
			read.eventsErr = append(read.eventsErr, fmt.Sprintf("the event log of %s could not be read: %v", state.RunID, err))
			if state.RunID == entry.promoting.RunID {
				return
			}
			continue
		}
		logs[index] = events
		if state.RunID == entry.promoting.RunID && len(events) == 0 {
			return
		}
	}
	entry.timed = true

	var previousEnd time.Time
	seenChecks := false
	for index, state := range entry.runs {
		start := state.StartedAt
		if !previousEnd.IsZero() && start.After(previousEnd) {
			entry.stages[LeadRepairing] += clampedSpan(previousEnd, start, merged)
		}
		end := EndedAt(state)
		if state.RunID == entry.promoting.RunID || end.After(merged) {
			end = merged
		}
		current := LeadDeveloping
		if index > 0 {
			current = LeadRepairing
			entry.repairRounds++
		}
		at := start
		stageFailed := false
		inFirstChecks := false
		events := append([]execution.Event(nil), logs[index]...)
		sort.SliceStable(events, func(a, b int) bool { return events[a].Sequence < events[b].Sequence })
		move := func(next LeadStage, moment time.Time) {
			if moment.Before(at) {
				moment = at
			}
			if moment.After(end) {
				moment = end
			}
			entry.stages[current] += moment.Sub(at)
			current, at = next, moment
		}
		for _, event := range events {
			var payload deliveryEvent
			_ = json.Unmarshal(event.Payload, &payload)
			switch event.Type {
			case execution.EventCommandStarted:
				if payload.Kind != "check" {
					continue
				}
				if current != LeadChecks {
					stageFailed = false
					inFirstChecks = !seenChecks
					seenChecks = true
					move(LeadChecks, event.Timestamp)
				}
			case execution.EventCommandCompleted:
				if payload.Kind != "check" || current != LeadChecks {
					continue
				}
				if !payload.Passed && strings.TrimSpace(payload.CouldNotRun) == "" {
					stageFailed = true
					if inFirstChecks {
						passed := false
						entry.firstChecks = &passed
					}
				}
			case execution.EventRunStarted:
				// A developer invoked again after a failed check stage is the repair.
				if current == LeadChecks && stageFailed {
					entry.repairRounds++
					move(LeadRepairing, event.Timestamp)
				}
			case execution.EventReviewStarted:
				if current == LeadChecks && inFirstChecks && !stageFailed && entry.firstChecks == nil {
					passed := true
					entry.firstChecks = &passed
				}
				move(LeadReview, event.Timestamp)
			case execution.EventReviewCompleted:
				entry.reviewRounds++
				approved := payload.Decision == reviewApproved
				if entry.firstReview == nil {
					entry.firstReview = &approved
				}
				if approved {
					move(LeadMerging, event.Timestamp)
				} else {
					entry.repairRounds++
					move(LeadRepairing, event.Timestamp)
				}
			}
		}
		// A first check stage that ended with the run, failing nothing, passed.
		if current == LeadChecks && inFirstChecks && !stageFailed && entry.firstChecks == nil {
			passed := true
			entry.firstChecks = &passed
		}
		move(current, end)
		if end.After(previousEnd) {
			previousEnd = end
		}
	}
}

// clampedSpan is from start to end, ending no later than limit.
func clampedSpan(start, end, limit time.Time) time.Duration {
	if end.After(limit) {
		end = limit
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}

// judgeDeliveryFailure records every way the change failed: reverted on its
// target branch, main red on its merge commit, or a bug filed within 48 hours
// that cites it.
func judgeDeliveryFailure(entry *deliveredWork, read deliveryRead, now time.Time) {
	merged := entry.change.MergedAt
	commits := map[string]bool{}
	if integration := entry.promoting.Integration; integration != nil {
		for _, commit := range []string{integration.TargetCommit, integration.SourceCommit} {
			if commit != "" {
				commits[commit] = true
			}
		}
	}
	if published := entry.promoting.PullRequest; published != nil && published.MergeCommit != "" {
		commits[published.MergeCommit] = true
	}
	for _, revert := range read.reverts[entry.change.TargetBranch] {
		if revert.At.Before(merged) {
			continue
		}
		for _, reverted := range revert.Reverts {
			if commits[reverted] {
				entry.failures = append(entry.failures, fmt.Sprintf("reverted by %s", shortDeliveryCommit(revert.Commit)))
				break
			}
		}
	}
	if landed := entry.promoting.LandingChecks; landed != nil && landed.Red() {
		entry.failures = append(entry.failures, fmt.Sprintf("%s failed its checks on the merge commit %s", nonEmptyBranch(entry.change.TargetBranch), shortDeliveryCommit(landed.Commit)))
	}
	for _, item := range read.itemList {
		if item.ID == entry.change.WorkItemID || item.Kind() != domain.WorkItemKindBug || item.CreatedAt.IsZero() {
			continue
		}
		if !item.CreatedAt.After(merged) || item.CreatedAt.After(merged.Add(defectWindow)) {
			continue
		}
		if citesChange(item.Title+"\n"+item.Description+"\n"+item.Notes, entry.change, entry.promoting) {
			entry.failures = append(entry.failures, fmt.Sprintf("the bug %s, filed within 48 hours, cites it", item.ID))
		}
	}
}

func nonEmptyBranch(branch string) string {
	if branch == "" {
		return "the target branch"
	}
	return branch
}

func shortDeliveryCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// citesChange reports text naming the change: its work item's id standing on
// its own — not as the front of a longer id split from it — or its pull
// request, by number or by address.
func citesChange(text string, change DeliveredChange, promoting runstate.State) bool {
	if citesItem(text, change.WorkItemID) {
		return true
	}
	if change.PullRequest > 0 {
		number := strconv.Itoa(change.PullRequest)
		if regexp.MustCompile(`(?i)(pull request|PR) #?` + number + `\b|#` + number + `\b`).MatchString(text) {
			return true
		}
		if promoting.PullRequest != nil && promoting.PullRequest.URL != "" && strings.Contains(text, promoting.PullRequest.URL) {
			return true
		}
	}
	return false
}

// citesItem reports the id appearing in the text as a whole id: not inside a
// longer word, and not followed by a dot and more of an id, which is a part
// split from it rather than the item.
func citesItem(text, id string) bool {
	if id == "" {
		return false
	}
	idChar := func(b byte) bool {
		return b == '-' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for from := 0; ; {
		found := strings.Index(text[from:], id)
		if found < 0 {
			return false
		}
		start, end := from+found, from+found+len(id)
		before := start == 0 || !(idChar(text[start-1]) || text[start-1] == '.')
		after := end == len(text) || !(idChar(text[end]) || (text[end] == '.' && end+1 < len(text) && idChar(text[end+1])))
		if before && after {
			return true
		}
		from = start + 1
	}
}

// deliveryTokens sums what every run of the change was billed, by model.
func deliveryTokens(entry *deliveredWork, read deliveryRead) map[string]runstate.TokenUsage {
	byModel := map[string]runstate.TokenUsage{}
	for _, state := range entry.runs {
		for _, row := range read.usage[state.RunID] {
			model := strings.TrimSpace(row.ResolvedModel)
			if model == "" {
				model = strings.TrimSpace(row.Model)
			}
			if model == "" {
				model = modelNotRecorded
			}
			usage := byModel[model]
			usage.Merge(row.Tokens())
			byModel[model] = usage
		}
	}
	return byModel
}

// deliverySourceHealth says, source by source, whether it was read.
func deliverySourceHealth(read deliveryRead) []DeliverySource {
	health := []DeliverySource{
		{Name: sourceTracker, Read: read.trackerErr == "", Problem: read.trackerErr},
		{Name: sourceRuns, Read: read.runsErr == "", Problem: read.runsErr},
	}
	events := DeliverySource{Name: sourceEvents, Read: read.runsErr == "" && len(read.eventsErr) == 0}
	switch {
	case read.runsErr != "":
		events.Problem = "not read, because " + read.runsErr
	case len(read.eventsErr) == 1:
		events.Problem = read.eventsErr[0]
	case len(read.eventsErr) > 1:
		events.Problem = fmt.Sprintf("%d event logs could not be read; the first: %s", len(read.eventsErr), read.eventsErr[0])
	}
	health = append(health, events,
		DeliverySource{Name: sourceUsage, Read: read.usageErr == "", Problem: read.usageErr},
		DeliverySource{Name: sourceHandSteps, Read: read.stepsErr == "", Problem: read.stepsErr},
	)
	for _, name := range []string{sourcePasses, sourceStalls, sourceFactoryStalls} {
		source := DeliverySource{Name: name, Read: true}
		for _, problem := range read.recoveryNotRead {
			if strings.Contains(problem, name) {
				source.Read, source.Problem = false, problem
			}
		}
		health = append(health, source)
	}
	return append(health, DeliverySource{Name: sourceReverts, Read: read.revertsErr == "", Problem: read.revertsErr})
}

// fillDeliveryPeriod reads every measure over one period.
func fillDeliveryPeriod(period *DeliveryPeriod, work []*deliveredWork, read deliveryRead, now time.Time) {
	var merged []*deliveredWork
	for _, entry := range work {
		if !entry.change.MergedAt.Before(period.Start) && entry.change.MergedAt.Before(period.End) {
			merged = append(merged, entry)
		}
	}
	runsMissing := read.runsErr
	period.Merges = deliveryMergesOf(merged, runsMissing)
	period.LeadTime = deliveryLeadTimeOf(merged, read)
	period.ChangeFailure = deliveryChangeFailureOf(merged, read, now)
	period.Recovery = deliveryRecoveryOf(period, read, now)
	period.Rework = deliveryReworkOf(merged, read)
	period.HandSteps = deliveryHandStepsOf(merged, read)
	period.FirstPass = deliveryFirstPassOf(merged, read)
	period.Tokens = deliveryTokensOf(merged, read)
}

func deliveryMergesOf(merged []*deliveredWork, runsMissing string) DeliveryMerges {
	measure := DeliveryMerges{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceRuns}}}
	if runsMissing != "" {
		measure.NotRecorded = runsMissing
		return measure
	}
	count := len(merged)
	measure.Count = &count
	measure.Changes = []DeliveredChange{}
	for _, entry := range merged {
		measure.Changes = append(measure.Changes, entry.change)
	}
	return measure
}

func deliveryLeadTimeOf(merged []*deliveredWork, read deliveryRead) DeliveryLeadTime {
	measure := DeliveryLeadTime{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceTracker, sourceRuns, sourceEvents}}}
	if read.runsErr != "" {
		measure.NotRecorded = read.runsErr
		return measure
	}
	if read.trackerErr != "" {
		measure.NotRead = append(measure.NotRead, read.trackerErr)
	}
	var totals []time.Duration
	byStage := map[LeadStage][]time.Duration{}
	var reviews, repairs []int
	for _, entry := range merged {
		if entry.filed.IsZero() {
			measure.FilingUnknown++
		} else {
			totals = append(totals, clampedSpan(entry.filed, entry.change.MergedAt, entry.change.MergedAt))
		}
		if queued, ok := entry.stages[LeadQueued]; ok {
			byStage[LeadQueued] = append(byStage[LeadQueued], queued)
		}
		if !entry.timed {
			measure.StagesUnknown++
			continue
		}
		for _, stage := range deliveryStages[1:] {
			byStage[stage] = append(byStage[stage], entry.stages[stage])
		}
		reviews = append(reviews, entry.reviewRounds)
		repairs = append(repairs, entry.repairRounds)
	}
	measure.Total = summarizeDurations(totals)
	for _, stage := range deliveryStages {
		entry := DeliveryStage{Stage: stage, Duration: summarizeDurations(byStage[stage])}
		if entry.Duration == nil {
			switch {
			case len(merged) == 0:
				entry.NotRecorded = "nothing merged"
			case stage == LeadQueued && read.trackerErr != "":
				entry.NotRecorded = read.trackerErr
			case stage == LeadQueued:
				entry.NotRecorded = "the tracker records no filing time for any change merged"
			default:
				entry.NotRecorded = "no event log of a change merged records what its runs did"
			}
		}
		measure.Stages = append(measure.Stages, entry)
	}
	measure.ReviewRounds = summarizeCounts(reviews)
	measure.RepairRounds = summarizeCounts(repairs)
	return measure
}

func deliveryChangeFailureOf(merged []*deliveredWork, read deliveryRead, now time.Time) DeliveryChangeFailure {
	measure := DeliveryChangeFailure{
		DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceRuns, sourceReverts, sourceTracker}},
		Merges:          len(merged),
	}
	if read.runsErr != "" {
		measure.NotRecorded = read.runsErr
		return measure
	}
	for _, problem := range []string{read.revertsErr, read.trackerErr} {
		if problem != "" {
			measure.NotRead = append(measure.NotRead, problem)
		}
	}
	failed := 0
	for _, entry := range merged {
		if now.Sub(entry.change.MergedAt) < defectWindow {
			measure.Settling++
		}
		if landed := entry.promoting.LandingChecks; landed == nil || !landed.Finished() || landed.Unverified() {
			measure.LandingUnchecked++
		}
		if len(entry.failures) == 0 {
			continue
		}
		failed++
		measure.FailedChanges = append(measure.FailedChanges, FailedChange{WorkItemID: entry.change.WorkItemID, Why: entry.failures})
		for _, why := range entry.failures {
			switch {
			case strings.HasPrefix(why, "reverted"):
				measure.Reverted++
			case strings.HasPrefix(why, "the bug"):
				measure.Defects++
			default:
				measure.RedAfter++
			}
		}
	}
	measure.Failed = &failed
	measure.Rate = share(failed, len(merged))
	return measure
}

func deliveryRecoveryOf(period *DeliveryPeriod, read deliveryRead, now time.Time) DeliveryRecovery {
	measure := DeliveryRecovery{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourcePasses, sourceStalls, sourceFactoryStalls}}}
	if len(read.recoveryRead) == 0 {
		measure.NotRecorded = strings.Join(read.recoveryNotRead, "; ")
		return measure
	}
	measure.NotRead = read.recoveryNotRead
	var spans []time.Duration
	measure.Breakages = []DeliveryBreakage{}
	for _, breakage := range read.breakages {
		if breakage.ClearedAt == nil {
			if period.Current && !breakage.Since.After(now) {
				open := breakage
				open.Seconds = int64(now.Sub(breakage.Since) / time.Second)
				measure.StillBroken = append(measure.StillBroken, open)
			}
			continue
		}
		cleared := *breakage.ClearedAt
		if cleared.Before(period.Start) || !cleared.Before(period.End) {
			continue
		}
		span := clampedSpan(breakage.Since, cleared, cleared)
		breakage.Seconds = int64(span / time.Second)
		spans = append(spans, span)
		measure.Breakages = append(measure.Breakages, breakage)
	}
	sort.SliceStable(measure.Breakages, func(a, b int) bool { return measure.Breakages[a].ClearedAt.Before(*measure.Breakages[b].ClearedAt) })
	measure.Recovered = summarizeDurations(spans)
	return measure
}

// reworkMinimum is what the rework rate says about itself wherever a merge's
// item has no recorded kind.
const reworkMinimum = "a minimum: a merge whose item records no kind is counted as not a bug fix, and items admitted before kinds were recorded mostly have none"

func deliveryReworkOf(merged []*deliveredWork, read deliveryRead) DeliveryRework {
	measure := DeliveryRework{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceRuns, sourceTracker}}, Merges: len(merged)}
	switch {
	case read.runsErr != "":
		measure.NotRecorded = read.runsErr
		return measure
	case read.trackerErr != "":
		measure.NotRecorded = read.trackerErr
		return measure
	}
	kinds := make([]domain.WorkItemKind, 0, len(merged))
	for _, entry := range merged {
		var kind domain.WorkItemKind
		if entry.item != nil {
			kind = entry.item.Kind()
		}
		kinds = append(kinds, kind)
	}
	tally := domain.CountRework(kinds)
	measure.Bugs, measure.Features, measure.Untyped = tally.Bugs, tally.Features, tally.Untyped
	if rate, ok := tally.Rate(); ok {
		measure.Rate = &rate
	}
	measure.UntypedShare = share(tally.Untyped, tally.Merges)
	if tally.Untyped > 0 {
		measure.Minimum = reworkMinimum
	}
	return measure
}

func deliveryHandStepsOf(merged []*deliveredWork, read deliveryRead) DeliveryHandStepCount {
	measure := DeliveryHandStepCount{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceHandSteps, sourceRuns}}}
	switch {
	case read.runsErr != "":
		measure.NotRecorded = read.runsErr
		return measure
	case read.stepsErr != "":
		measure.NotRecorded = read.stepsErr
		return measure
	}
	promoted := make([]runstate.PromotedItem, 0, len(merged))
	for _, entry := range merged {
		runIDs := make([]string, 0, len(entry.runs))
		for _, state := range entry.runs {
			runIDs = append(runIDs, state.RunID)
		}
		promoted = append(promoted, runstate.PromotedItem{
			WorkItemID:   entry.change.WorkItemID,
			ShippedAt:    entry.change.MergedAt,
			ShippedRunID: entry.change.RunID,
			RunIDs:       runIDs,
		})
	}
	count := CountInterventions(read.steps, promoted)
	measure.Count = &count
	return measure
}

func deliveryFirstPassOf(merged []*deliveredWork, read deliveryRead) DeliveryFirstPass {
	measure := DeliveryFirstPass{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceRuns, sourceEvents}}, Merges: len(merged)}
	if read.runsErr != "" {
		measure.NotRecorded = read.runsErr
		return measure
	}
	for _, entry := range merged {
		if !entry.timed || entry.firstChecks == nil || entry.firstReview == nil {
			measure.Unknown++
			continue
		}
		measure.Known++
		if *entry.firstChecks && *entry.firstReview {
			measure.Passed++
		}
	}
	measure.Rate = share(measure.Passed, measure.Known)
	return measure
}

func deliveryTokensOf(merged []*deliveredWork, read deliveryRead) DeliveryTokens {
	measure := DeliveryTokens{DeliveryMeasure: DeliveryMeasure{ReadFrom: []string{sourceUsage, sourceRuns}}, Merges: len(merged)}
	switch {
	case read.runsErr != "":
		measure.NotRecorded = read.runsErr
		return measure
	case read.usageErr != "":
		measure.NotRecorded = read.usageErr
		return measure
	}
	totals := map[string]*ModelTokens{}
	for _, entry := range merged {
		if len(entry.tokens) == 0 {
			measure.Unrecorded++
			continue
		}
		measure.Recorded++
		for model, usage := range entry.tokens {
			figure := totals[model]
			if figure == nil {
				figure = &ModelTokens{Model: model}
				totals[model] = figure
			}
			figure.Changes++
			figure.Tokens.Merge(usage)
		}
	}
	measure.ByModel = []ModelTokens{}
	for _, model := range deliveryKeys(totals) {
		figure := *totals[model]
		per := float64(measure.Recorded)
		figure.PerChange = TokensPerChange{
			Input:         float64(figure.Tokens.InputTokens) / per,
			CacheRead:     float64(figure.Tokens.CacheReadTokens) / per,
			CacheCreation: float64(figure.Tokens.CacheCreationTokens) / per,
			Output:        float64(figure.Tokens.OutputTokens) / per,
			Total:         float64(figure.Tokens.InputTotal()+figure.Tokens.OutputTokens) / per,
		}
		measure.ByModel = append(measure.ByModel, figure)
	}
	return measure
}

// share is part over whole, or nothing where there is no whole to take a share
// of: a rate over no merges is not zero.
func share(part, whole int) *float64 {
	if whole == 0 {
		return nil
	}
	rate := float64(part) / float64(whole)
	return &rate
}

// summarizeDurations is the summary of a set of durations, or nothing for an
// empty set.
func summarizeDurations(spans []time.Duration) *DurationSummary {
	if len(spans) == 0 {
		return nil
	}
	sorted := append([]time.Duration(nil), spans...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	seconds := func(fraction float64) int64 { return int64(sorted[nearestRank(len(sorted), fraction)] / time.Second) }
	return &DurationSummary{
		Count:         len(sorted),
		MedianSeconds: seconds(0.5),
		P90Seconds:    seconds(0.9),
		MinSeconds:    int64(sorted[0] / time.Second),
		P25Seconds:    seconds(0.25),
		P75Seconds:    seconds(0.75),
		MaxSeconds:    int64(sorted[len(sorted)-1] / time.Second),
	}
}

// summarizeCounts is the summary of a set of counts, or nothing for an empty
// set.
func summarizeCounts(counts []int) *CountSummary {
	if len(counts) == 0 {
		return nil
	}
	sorted := append([]int(nil), counts...)
	sort.Ints(sorted)
	return &CountSummary{
		Count:  len(sorted),
		Median: sorted[nearestRank(len(sorted), 0.5)],
		P90:    sorted[nearestRank(len(sorted), 0.9)],
		Max:    sorted[len(sorted)-1],
	}
}

// nearestRank is the index of the value at a fraction of a sorted set: the
// smallest value at least that fraction of the set does not exceed.
func nearestRank(size int, fraction float64) int {
	rank := int(math.Ceil(fraction * float64(size)))
	if rank < 1 {
		rank = 1
	}
	if rank > size {
		rank = size
	}
	return rank - 1
}

// deliveryKeys is a map's keys in order, so two readings of one directory say
// things in one order.
func deliveryKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
