package cli

// Reading what became of the runs the harness made, and why one of them failed.
//
// The write side of a failure is thorough: a terminal run's record keeps the
// status, the phase it died in, and the reason, with the bookkeeping failures
// beside it in fields of their own, and the same reason reaches the work item's
// notes. Reading it back was the gap — an operator asking "what has been
// failing?" went through the tracker item by item, or read the run JSON out of
// the state directory by hand.
//
// So this reports the recorded runs newest first, which is the order the
// question is asked in, with each recorded reason on a line of its own and named
// for what it is: a publication or a cleanup that could not finish is not a
// failed piece of work, and a listing that ran them together would undo the
// separation the records take care to keep.
//
// What each run's own line says is what became of the work rather than what
// became of the attempt, in a small fixed vocabulary the read model derives.
// "failed" was one word for four different things — a review nobody repaired, a
// target branch the replay could not catch, a provider that kept killing the
// run, and an operator stopping it — and three of those leave the change intact
// and the item back in somebody's hands. An operator read three of those lines
// and asked whether the runs had been discarded, which is the one question this
// listing exists to answer. So the outcome says which of them it was, the line
// beside it says whether anything survives, and the lines under it name the
// branch, the worktree, and the session that do.
//
// It is read-only in the strongest sense. Reading a run is not acting on it, so
// this holds nothing, adopts nothing, and settles nothing — a run another
// process is executing is listed exactly as a finished one is. Settling what a
// run left behind is `yoyo reconcile`; this is the record afterwards.
//
// Watching one happen is the same verb under `--follow`, `--events`, `--list`,
// and `--spend`, which live in statusstream.go: the record afterwards and the
// stream as it arrives are the same question asked at two moments, and an
// operator should not have to install a second thing to ask the other half of
// it.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type statusOutput struct {
	// Standing is where the harness stands right now, in the four lines the
	// operator ratified. It comes first because it is the question this verb is
	// actually reached for: the run history says what became of attempts that are
	// over, and until this existed there was nowhere at all that said what is
	// happening. Standing is absent when one item was named, because the four
	// lines are about the product. Throughput always covers the product's history.
	Throughput *readmodel.Throughput `json:"throughput,omitempty"`
	Standing   *readmodel.Standing   `json:"standing,omitempty"`
	Runs       []runstate.RunSummary `json:"runs"`
	// Matched and Recorded are what keep a limited listing honest: how many runs
	// the query selected, and how many the harness holds at all.
	Matched  int `json:"matched"`
	Recorded int `json:"recorded"`
	// Triage is what triage has spent on the named item and what the item has
	// cost in review rounds, with the caps those are measured against. It is
	// present only when an item was named, because it is a fact about one piece of
	// work rather than about the listing: a run's record says what became of that
	// run, and this says what the item has been given across all of them.
	Triage     *runstate.TriageCounters `json:"triage,omitempty"`
	TriageCaps *runstate.TriageCaps     `json:"triage_caps,omitempty"`
	// TriageError accompanies a successful listing: a named item whose triage
	// record could not be read reports that here while the runs it found are
	// still returned. It is its own key so error keeps meaning what it always
	// meant — the command failed, and the exit status agrees.
	TriageError string `json:"triage_error,omitempty"`
	// Watch is where the session that chooses work got to, when one has ever run
	// for this product. It is the one fact here that is not about a run: a
	// session choosing nothing has no run to say so with, and its silence and a
	// dead process read identically without it.
	Watch *runstate.WatchTransition `json:"watch,omitempty"`
	// WatchError accompanies a successful listing, for the reason TriageError
	// does: an unreadable watch log costs this answer a line rather than the runs
	// it found.
	WatchError string `json:"watch_error,omitempty"`
	// Stalls is the product's record of having gone quiet: stretches where nothing
	// started at all while the tracker reported work ready and nothing accounted
	// for it. It is the one history here that is not about a run, and it is here
	// because it is the history nothing else keeps — the process that would have
	// recorded a stall is the process a stall means has died, so the record
	// outlives it or there is no answer afterwards to how long it was dead.
	//
	// It is absent when one item was named, for the reason the four lines are: a
	// stall is about the product rather than about any one piece of work.
	Stalls []runstate.StallEvent `json:"stalls,omitempty"`
	// StallError accompanies a successful listing, for the reason the two above
	// do.
	StallError string `json:"stall_error,omitempty"`
	Error      string `json:"error,omitempty"`
}

// defaultStatusStalls is how many recorded stalls are printed. It is a handful
// rather than the whole history because the question a stall listing answers is
// whether this has been happening lately; --json carries every one of them.
const defaultStatusStalls = 5

// defaultStatusRuns is how many runs are reported when nobody says. It is a
// screenful rather than the whole history, because the question this answers is
// about what has happened lately; --limit 0 reports everything.
const defaultStatusRuns = 20

func reportRunStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	failedOnly := flags.Bool("failed", false, "only the runs that ended without succeeding")
	limit := flags.Int("limit", defaultStatusRuns, "report at most this many, newest first (0 reports all of them)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	follow := flags.Bool("follow", false, "follow a run, conversation, or branch review as its events arrive")
	events := flags.Bool("events", false, "print a stream's recent events and exit, without following")
	list := flags.Bool("list", false, "list the recent runs, conversations, and branch reviews")
	spend := flags.Bool("spend", false, "report what was spent, grouped by the local day it was spent on")
	shipped := flags.Bool("shipped", false, "list the most recently shipped work items with their price and wall clock")
	latest := flags.Bool("latest", false, "with --follow, move to a later stream when one starts")
	lines := flags.Int("lines", defaultStreamLines, "replay this many recorded events first (0 replays the whole log)")
	kind := flags.String("kind", "", "narrow to one kind: runs, chats, reviews, sides, exchanges, or all (default all)")
	includeAll := flags.Bool("all", false, "include the thinking-token events the default leaves out")
	raw := flags.Bool("raw", false, "emit each event exactly as it was recorded")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "status accepts at most one id")
		printStatusUsage(stderr)
		return 2
	}
	// The argument is optional, so it is read through argumentAt rather than
	// indexed: `yoyo status` with nothing named reports the whole recent history.
	// Which id it is depends on the mode — a work item for the run records, a
	// stream for the live ones — because those are different collections and an
	// argument that meant the same thing in both would name nothing in one.
	named := argumentAt(positional, 0)
	if *limit < 0 {
		fmt.Fprintln(stderr, "limit cannot be negative; 0 reports everything")
		return 2
	}
	if *lines < 0 {
		fmt.Fprintln(stderr, "lines cannot be negative; 0 replays the whole log")
		return 2
	}
	kinds, err := resolveStreamKinds(*kind)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	mode, err := selectedStatusMode(*follow, *events, *list, *spend, *shipped)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printStatusUsage(stderr)
		return 2
	}
	if *latest && mode != statusFollows {
		fmt.Fprintln(stderr, "--latest moves a follow to a later stream, so it needs --follow")
		return 2
	}
	// An option that belongs to the other half of the verb is refused rather than
	// ignored: an operator who narrowed a listing and was silently given the
	// unnarrowed one reads a true answer as the answer to their question. Two of
	// these carry a default that is not their zero, so whether they were given at
	// all is read off the flag set rather than off their value.
	if mode != statusReadsRecords && *failedOnly {
		fmt.Fprintln(stderr, "--failed selects among the recorded runs, so it cannot narrow a stream")
		return 2
	}
	if flagGiven(flags, "limit") && mode != statusReadsRecords && mode != statusListsStreams && mode != statusListsShipped {
		fmt.Fprintln(stderr, "--limit bounds a listing, so it needs --list, --shipped, or the recorded runs")
		return 2
	}
	if flagGiven(flags, "lines") && mode != statusFollows && mode != statusShowsEvents {
		fmt.Fprintln(stderr, "--lines replays a stream's recorded events, so it needs --follow or --events")
		return 2
	}
	// The shipped ledger reads the run records rather than the event streams, so
	// it is refused every stream-shaping option the recorded mode is refused below
	// and answered from the same store the recorded mode reads.
	if mode == statusListsShipped {
		if *raw || *includeAll {
			fmt.Fprintln(stderr, "--raw and --all shape a followed event stream, so they need --follow or --events")
			return 2
		}
		if *kind != "" {
			fmt.Fprintln(stderr, "--kind narrows which event streams are read, so it needs --follow, --events, --list, or --spend")
			return 2
		}
		count, err := shippedCount(named, *limit, flagGiven(flags, "limit"))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		return reportShipped(*configPath, count, *jsonOutput, stdout, stderr)
	}
	if !followableKinds(kinds) && mode != statusPricesStreams {
		fmt.Fprintln(stderr, "an exchange has no event stream to follow or list; --kind exchanges needs --spend")
		return 2
	}
	// Following emits the recorded events themselves, which are already the
	// machine-readable form: --raw is what asks for them untouched. Like every
	// other refusal here it is decided from the flags alone, so a request that
	// cannot be honored is refused before any configuration is loaded or any
	// state directory is opened to answer it.
	if *jsonOutput && (mode == statusFollows || mode == statusShowsEvents) {
		fmt.Fprintln(stderr, "a followed stream emits its own recorded events; --raw is what asks for them untouched")
		return 2
	}
	if mode != statusReadsRecords {
		options := streamOptions{
			kinds:  kinds,
			match:  named,
			lines:  *lines,
			limit:  *limit,
			follow: mode == statusFollows,
			latest: *latest,
			raw:    *raw,
			all:    *includeAll,
		}
		if mode == statusPricesStreams {
			days, match, err := spendWindow(named)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			options.days, options.match = days, match
		}
		return reportStreamStatus(ctx, mode, options, *configPath, *jsonOutput, stdout, stderr)
	}
	if *raw || *includeAll {
		fmt.Fprintln(stderr, "--raw and --all shape a followed event stream, so they need --follow or --events")
		return 2
	}
	if *kind != "" {
		fmt.Fprintln(stderr, "--kind narrows which event streams are read, so it needs --follow, --events, --list, or --spend")
		return 2
	}
	workItemID := named

	store, caps, err := recordedRunStore(*configPath)
	if err != nil {
		return reportStatusFailure(stdout, stderr, *jsonOutput, err)
	}
	history, err := store.History(runstate.RunQuery{
		WorkItemID: workItemID,
		FailedOnly: *failedOnly,
		Limit:      *limit,
	})
	if err != nil {
		return reportStatusFailure(stdout, stderr, *jsonOutput, err)
	}
	// What each run that stopped left is looked for in the repository rather than
	// read off its removal flags, so this listing says what is there — and says
	// it looked — in the words the docket and the hold use for the same run.
	readmodel.LookForSummaries(context.Background(), statusRemains(*configPath), store, history.Runs)
	// The item's triage record is read only when an item was named, and it is
	// read whatever the listing found: an item whose runs were all cleaned up
	// still has a record of what triage gave it, and that is exactly the reader
	// this answers.
	// An unreadable triage record does not replace the listing: the
	// never-spend-an-unreadable-budget rule is about spending, and this is a
	// read-only answer that still holds the runs it found. The failure is
	// reported beside them instead.
	var counters *runstate.TriageCounters
	var triageFailure string
	if workItemID != "" {
		read, err := store.Triage().Counters(workItemID)
		if err != nil {
			triageFailure = fmt.Sprintf("the item's triage record could not be read: %v", err)
		} else {
			counters = &read
		}
	}

	// Where the session that chooses work got to is read whatever the listing
	// found, and it is read for the whole product rather than for a named item:
	// a session is about the queue, and the question it answers — is anything
	// still choosing work — is the one an operator asks before any question
	// about a particular run.
	watched, watchFailure := latestWatch(*configPath)

	// Where the harness stands is read only when nothing was named, because the
	// four lines are about the product: an operator asking about one item is
	// asking a different question, and answering both would put a screen of
	// product-wide state in front of the run they came here to read.
	var standing *readmodel.Standing
	var stalls []runstate.StallEvent
	var stallFailure string
	if workItemID == "" {
		// A status is one of the loads that records when each program manager
		// instance was first seen, so a scheduler that never woke a new one is
		// still caught; the reading below then measures from that record.
		if resolved, err := loadConfiguration(*configPath); err == nil {
			if stateRoot, err := productStateRoot(resolved); err == nil {
				observeProgramManagers(resolved.Config, stateRoot, time.Now())
			}
		}
		read := readmodel.ReadStanding(context.Background(), standingSources(*configPath))
		standing = &read
		// What the product recorded about having gone quiet, read for the whole
		// product for the reason the four lines are: a stall is a fact about the
		// line rather than about any item, and the item that was not started during
		// one has no record of the stall on it.
		stalls, stallFailure = recordedStalls(*configPath)
	}

	if *jsonOutput {
		throughput := readmodel.ReadThroughput(ctx, readmodel.ThroughputSources{Runs: store})
		output := statusOutput{
			Throughput: &throughput,
			Standing:   standing,
			Runs:       history.Runs,
			Matched:    history.Matched,
			Recorded:   history.Recorded,
			Triage:     counters,
			Watch:      watched,
			Stalls:     stalls,
		}
		if counters != nil {
			// The caps as this item's own recorded overrides leave them, which is what
			// the guards refuse against. Reporting the configured pair instead would
			// tell an operator who crossed a cap that they had not.
			recorded := caps.Overridden(counters.Overrides)
			output.TriageCaps = &recorded
		}
		output.TriageError = triageFailure
		output.WatchError = watchFailure
		output.StallError = stallFailure
		return writeJSON(stdout, stderr, output)
	}
	// The four lines come first and are separated from the history by a blank
	// line, because they answer opposite questions: everything above is what is
	// true now, and everything below is what became of attempts that are over.
	if standing != nil {
		fmt.Fprint(stdout, standing.Render())
		fmt.Fprint(stdout, standing.RenderProgramManagers())
		fmt.Fprint(stdout, standing.RenderMergeQueues())
		fmt.Fprint(stdout, standing.RenderServices())
		fmt.Fprintln(stdout)
	}
	printWatch(stdout, watched)
	printStalls(stdout, stalls)
	printRunHistory(stdout, history, workItemID, *failedOnly)
	if counters != nil {
		printItemTriage(stdout, *counters, caps.Overridden(counters.Overrides))
	}
	if triageFailure != "" {
		fmt.Fprintln(stderr, triageFailure)
	}
	if watchFailure != "" {
		fmt.Fprintln(stderr, watchFailure)
	}
	if stallFailure != "" {
		fmt.Fprintln(stderr, stallFailure)
	}
	// A failed run is what this exists to report, so reporting one is this
	// command working. An exit status that treated the answer as a failure would
	// make the surface something a script has to guard against reading.
	return 0
}

// recordedRunStore resolves the same product-scoped run records every run
// writes, from the configuration and the state root alone. It deliberately does
// not go through buildComponents, for the reason the reports verb does not:
// reading what became of a run needs no repository, no worktree manager, and no
// process runner, and a verb an operator reaches for when something has gone
// wrong must not refuse to answer because of where their checkout happens to sit
// — inside a harness-managed worktree, for one.
//
// The state root is productStateRoot for every command that has one,
// so the product id is the whole of what decides which records these are, and a
// test pins this path to the one buildComponents builds.
func recordedRunStore(configPath string) (*runstate.Store, runstate.TriageCaps, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, runstate.TriageCaps{}, err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, runstate.TriageCaps{}, err
	}
	store, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, runstate.TriageCaps{}, err
	}
	// The caps come back with the store because the counters are only legible
	// beside them: "three review rounds" says nothing about whether this item is
	// nearly out of them.
	return store, orchestrator.TriageCaps(resolved.Config.Execution, resolved.Config.Triage), nil
}

// statusRemains is the repository the listing asks what a stopped run left,
// where the configuration names one. Nil is kept as no observer, which the look
// answers from the record and says so.
func statusRemains(configPath string) readmodel.Remains {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil
	}
	return standingRemains(resolved)
}

// flagGiven reports whether an option was actually on the command line, which
// is the only way to tell a default from a choice for an option whose default
// is not its zero value.
func flagGiven(flags *flag.FlagSet, name string) bool {
	given := false
	flags.Visit(func(visited *flag.Flag) {
		if visited.Name == name {
			given = true
		}
	})
	return given
}

// statusRoots is what the live modes of this verb read from: the product the
// configuration names, and the state root the harness keeps its records under.
// They are resolved the way the recorded mode resolves them, which is what makes
// the live modes agree with the recorded one about which machine's work is being
// reported.
type statusRoots struct {
	productID domain.ProductID
	stateRoot string
}

func statusStateRoots(configPath string) (statusRoots, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return statusRoots{}, err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return statusRoots{}, err
	}
	return statusRoots{productID: resolved.Config.Product.ID, stateRoot: stateRoot}, nil
}

// statusMode is which question of the verb was asked. The record afterwards is
// the default because it is the one that needs no argument and refuses nothing;
// four are the live surface this verb absorbed, and the last is the shipped
// ledger, which reads the run records the default reads and answers a
// different question of them: not what became of each attempt, but what
// shipped and what it took.
type statusMode int

const (
	statusReadsRecords statusMode = iota
	statusFollows
	statusShowsEvents
	statusListsStreams
	statusPricesStreams
	statusListsShipped
)

// selectedStatusMode reads which mode the flags asked for. They are exclusive
// because they are different answers rather than different amounts of one:
// combining them would have to invent a precedence, and an operator who typed
// two of them meant one of them.
func selectedStatusMode(follow, events, list, spend, shipped bool) (statusMode, error) {
	selected := statusReadsRecords
	named := 0
	for _, mode := range []struct {
		asked bool
		mode  statusMode
	}{
		{follow, statusFollows},
		{events, statusShowsEvents},
		{list, statusListsStreams},
		{spend, statusPricesStreams},
		{shipped, statusListsShipped},
	} {
		if mode.asked {
			selected = mode.mode
			named++
		}
	}
	if named > 1 {
		return statusReadsRecords, errors.New("--follow, --events, --list, --spend, and --shipped are different questions; ask one of them")
	}
	return selected, nil
}

// reportStreamStatus answers the modes that read the event streams rather than
// the run records. They share the state root with the recorded listing and
// nothing else: what they read is being written right now, and the holds that
// the recorded mode carries on its attention line are said here as a banner,
// because these modes print no four lines for the switch to appear in.
func reportStreamStatus(ctx context.Context, mode statusMode, options streamOptions, configPath string, jsonOutput bool, stdout, stderr io.Writer) int {
	roots, err := statusStateRoots(configPath)
	if err != nil {
		return reportStatusFailure(stdout, stderr, jsonOutput, err)
	}
	store, err := runstate.NewStreamStore(roots.stateRoot, roots.productID)
	if err != nil {
		return reportStatusFailure(stdout, stderr, jsonOutput, err)
	}
	holds := readStatusHolds(roots)
	announceHolds(stderr, holds)
	switch mode {
	case statusListsStreams:
		return listStreams(store, options, holds, jsonOutput, stdout, stderr)
	case statusPricesStreams:
		// The recurring tasks' own records, which are what say which of a
		// conversation's turns a schedule took and on which model. A store that
		// cannot be opened costs the report that attribution and nothing else.
		sweeps, err := runstate.NewSweepStore(roots.stateRoot, roots.productID)
		if err != nil {
			fmt.Fprintf(stderr, "warning: the recurring tasks' records could not be opened, so their spend is not attributed: %v\n", err)
			sweeps = nil
		}
		return reportSpend(store, sweeps, options, holds, time.Now(), jsonOutput, stdout, stderr)
	default:
		// --json is refused for these two before anything is resolved, so by here
		// there is nothing left to decide about the shape of what they emit.
		return followStreams(ctx, store, options, stdout, stderr)
	}
}

// spendWindow reads the one argument a spend report takes, which is either the
// number of local days to cover or the thing to price. A purely numeric one is
// the count, because an operator asking for a fortnight would not otherwise have
// a way to say so. An id prefix can be all digits too — ids are hex — so one
// that is has to be given with its `run-`, `chat-`, `review-`, `side-`, or `exchange-`
// prefix to be read as an id rather than as days. Naming something prices it
// whatever day it ran on: the window is for a report that has to choose what to
// show, and an id has already chosen.
func spendWindow(named string) (int, string, error) {
	if named == "" {
		return defaultSpendDays, "", nil
	}
	if strings.TrimLeft(named, "0123456789") != "" {
		return 0, named, nil
	}
	days, err := strconv.Atoi(named)
	if err != nil || days <= 0 {
		return 0, "", fmt.Errorf("%q is neither a positive number of days nor the id of a run, conversation, branch review, side thread, or exchange", named)
	}
	return days, "", nil
}

// standingSources wires the four lines over the same durable records every run
// writes. It goes through the configuration and the state root alone, for the
// reason the run store does: a verb an operator reaches for when something has
// gone wrong must not refuse to answer because of where their checkout happens
// to sit, and none of these stores needs a worktree or a process runner.
//
// A source that cannot be built is left out rather than failing the answer, and
// the line it belongs to says it could not be read. That is the whole discipline
// of this format: three quarters of an answer with the missing quarter named
// beats no answer, and beats an answer that quietly reports the missing quarter
// as empty.
//
// The tracker is the one source that needs the repository, so it is the one that
// can be missing on a checkout the configuration does not resolve against. It is
// wired as a tracker that reports that failure rather than left nil, so the line
// says what actually went wrong instead of reporting a wiring gap.
func standingSources(configPath string) readmodel.Sources {
	sources := readmodel.Sources{TrackerTimeout: chatTrackerTimeout}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		sources.Tracker = unreadableTracker{err}
		return sources
	}
	cfg := resolved.Config
	sources.Capacity = cfg.Execution.MaxConcurrentDevelopers
	sources.Slots = cfg.Execution.DeveloperSlots
	// What each agent asks for and may be served by instead, read against the
	// refusal log below for the one thing the two say together: whether the
	// provider is holding every role at once.
	sources.Agents = agentEndpoints(cfg)
	sources.UnknownResetPause = cfg.Execution.UsageLimitUnknownResetPause.Duration()
	sources.FactoryStallAfter = cfg.Execution.FactoryStallAfter.Duration()
	sources.CouldNotRunBeforeStatus = cfg.Execution.CouldNotRunBeforeStatus
	sources.GateChecks = cfg.GateCheckCommands()
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		sources.Tracker = unreadableTracker{err}
		return sources
	}
	if store, err := runstate.NewStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Runs = store
		sources.Stoppages = store
		// The item's own triage record, from the store that already holds it, so a
		// held item says whether it waits on a decision or on the harness carrying
		// one out rather than on both at once.
		sources.Decisions = store.Triage()
		// And the repository, asked whether each stopped run's change is still
		// there, so this surface holds the same items the scheduler holds and for
		// the same reason rather than reading the run's flags where it looks.
		sources.Remains = standingRemains(resolved)
		// The same store answers both, and it is set twice rather than once
		// because the two are different questions about different records: what
		// the runs are doing, and what a person has recorded doing.
		sources.Gates = store
		// The recurring passes, from the same state root, for what an owning
		// role recommended on the proposed changes put to it.
		sources.Sweeps = store.Sweeps()
	}
	if store, err := runstate.NewConversationStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Conversations = store
	}
	if store, err := runstate.NewDirectiveStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Directives = store
	}
	if store, err := runstate.NewAmendmentStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Amendments = store
	}
	if store, err := runstate.NewOperatorHoldStore(stateRoot); err == nil {
		sources.OperatorHolds = store
	}
	if store, err := runstate.NewIntakeHoldStore(stateRoot, cfg.Product.ID); err == nil {
		sources.IntakeHolds = store
	}
	if store, err := runstate.NewWatchStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Sessions = store
	}
	if store, err := runstate.NewReportStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Reports = store
	}
	// The triage docket, for the Lead Product Manager's decisions about runs in
	// flight that the development manager has not yet answered.
	if store, err := runstate.NewDocketStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Docket = store
	}
	if store, err := runstate.NewUsageLimitStore(stateRoot, cfg.Product.ID); err == nil {
		sources.UsageLimits = store
	}
	// What the provider has served since, which reads a refusal of the same
	// account and model as lifted before the reset it quoted.
	if store, err := runstate.NewCapacityServedStore(stateRoot, cfg.Product.ID); err == nil {
		sources.CapacityServed = store
	}
	if store, err := runstate.NewProviderOutageStore(stateRoot, cfg.Product.ID); err == nil {
		sources.ProviderOutages = store
	}
	if store, err := runstate.NewDivergedTargetStore(stateRoot, cfg.Product.ID); err == nil {
		sources.DivergedTargets = store
	}
	// The product's supervisor and its record of the parts, so a part the
	// supervisor has left down is said here with its reason.
	if store, err := runstate.NewConfigReaderStore(stateRoot, cfg.Product.ID); err == nil {
		sources.ConfigReaders = store
	}
	if store, err := runstate.NewSupervisionStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Supervision = store
		sources.Machine = store
	}
	// The program manager instances, and everything their status is derived
	// from.
	programManagerSources(&sources, cfg, stateRoot)
	// How the tracker's listings stand, so a tracker that is not answering them
	// is said with since when. The status's own listings write to it too: a
	// reading that could not list the tracker is evidence of the same thing.
	listings, err := runstate.NewTrackerListingStore(stateRoot, cfg.Product.ID)
	if err == nil {
		sources.TrackerListings = listings
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), cfg.Product.Repository)
	if err != nil {
		sources.Tracker = unreadableTracker{fmt.Errorf("resolve product repository: %w", err)}
		return sources
	}
	sources.Repository = repository
	sources.Tracker = withListingRecord(beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository}, listings)
	return sources
}

// agentEndpoints is every configured agent as the read model needs it: what
// each asks for, and what each fails over to where it fails over at all. The
// alternate is read through the same answer failover itself reads — an
// alternate an operator switched off is not one — so what the hold says about
// the configuration is what the configuration would actually do. Sorted by
// name, so two readings of one configuration say the agents in one order.
func agentEndpoints(cfg config.Config) []readmodel.AgentEndpoint {
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	endpoints := make([]readmodel.AgentEndpoint, 0, len(names))
	for _, name := range names {
		agent := cfg.Agents[name]
		endpoint := readmodel.AgentEndpoint{Name: name, Provider: agent.Backend, Model: agent.Model}
		if alternate := agent.Failover.Alternate(); alternate != "" {
			endpoint.Alternate = alternate
			endpoint.AlternateProvider = agent.Failover.AlternateProvider(agent.Backend)
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

// programManagerSources wires what the read model derives each program manager
// instance from: the configured instances, their restart requests, their lane
// reports, the pass records, and the exchanges. The reports, the amendments,
// and the conversations a status also reads are wired by the caller, because
// the four lines read them too. A store that cannot be built is left unwired,
// which the reading says rather than reporting what it could not read as none.
func programManagerSources(sources *readmodel.Sources, cfg config.Config, stateRoot string) {
	sources.ProgramManagers = programManagerInstances(cfg)
	if store, err := runstate.NewMergeQueueStore(stateRoot, cfg.Product.ID); err == nil {
		sources.MergeQueues = store
	}
	if store, err := runstate.NewRestartRequestStore(stateRoot, cfg.Product.ID); err == nil {
		sources.RestartRequests = store
	}
	if store, err := runstate.NewLaneReportStore(stateRoot, cfg.Product.ID, readmodel.CheckLaneReportMover); err == nil {
		sources.LaneReports = store
	}
	if store, err := runstate.NewSweepStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Passes = store
	}
	if store, err := runstate.NewExchangeStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Exchanges = store
	}
	// Read and never written here: this is the path every request the dashboard
	// answers goes through, and no request writes anything. The loads that
	// record an instance call observeProgramManagers themselves.
	if store, err := runstate.NewFirstSeenStore(stateRoot, cfg.Product.ID); err == nil {
		sources.FirstSeen = store
	}
}

// observeProgramManagers records now as the moment each configured program
// manager instance was first seen, for every one this load carries that the
// record does not hold yet, and returns the store. Every command that builds
// the harness makes it, and so do the loads that read the standing without a
// scheduler behind them — `yoyo status`, the Slack sink as it starts, and the
// dashboard as it starts, though none of the dashboard's requests — so an
// instance is recorded at the first load that carries it whether or not the
// scheduler ever wakes it, which is what lets a scheduler that never does be
// read as stale. A record that cannot be written is not a reason to refuse the
// load: the reading reads the record back and says when it cannot, and the
// next load tries the write again.
func observeProgramManagers(cfg config.Config, stateRoot string, now time.Time) *runstate.FirstSeenStore {
	store, err := runstate.NewFirstSeenStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return nil
	}
	instances := programManagerInstances(cfg)
	if len(instances) == 0 {
		return store
	}
	agents := make([]string, 0, len(instances))
	for _, instance := range instances {
		agents = append(agents, instance.Agent)
	}
	_, _ = store.Observe(agents, now)
	return store
}

// programManagerInstances is every configured agent on the program manager
// role, with its lane and its schedule, sorted by name.
func programManagerInstances(cfg config.Config) []readmodel.ProgramManagerInstance {
	var instances []readmodel.ProgramManagerInstance
	for name, agent := range cfg.Agents {
		if agent.Role == domain.RoleProgramManager {
			instances = append(instances, readmodel.ProgramManagerInstance{
				Agent: name,
				Lane:  cfg.AgentLane(name),
				Every: agent.Triggers.Every.Duration(),
			})
		}
	}
	sort.Slice(instances, func(first, second int) bool { return instances[first].Agent < instances[second].Agent })
	return instances
}

// pullConversations is the product's conversation records, for a watching pull
// to tell a role's current conversation from one it has replaced. A store that
// cannot be opened is none rather than a typed nil, so the pull reads every
// conversation as current — which holds intake on its refusals rather than
// releasing it on a guess.
func pullConversations(parts components) readmodel.Conversations {
	store, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil
	}
	return store
}

// developerEndpoints is every endpoint a developer run's turn can be asked of:
// the developer agent as configured, and the same agent on each model
// execution.developer_models maps a label to, each failing over exactly as the
// developer does. A usage window holds intake only where it closes every one of
// them, because a mapped model the provider still serves is work that can run.
func developerEndpoints(cfg config.Config) []readmodel.AgentEndpoint {
	name := agentNameForRole(cfg, domain.RoleDeveloper)
	if name == "" {
		return nil
	}
	var developer readmodel.AgentEndpoint
	for _, endpoint := range agentEndpoints(cfg) {
		if endpoint.Name == name {
			developer = endpoint
		}
	}
	endpoints := []readmodel.AgentEndpoint{developer}
	for _, rule := range cfg.Execution.DeveloperModels {
		mapped := developer
		mapped.Name = name + " (" + strings.TrimSpace(rule.Label) + ")"
		mapped.Model = strings.TrimSpace(rule.Model)
		endpoints = append(endpoints, mapped)
	}
	return endpoints
}

// unreadableTracker is the tracker a reading gets when the harness could not be
// resolved far enough to build one. It answers every question with the reason,
// so the queue's line says what went wrong rather than reporting an empty
// backlog assembled from nothing.
type unreadableTracker struct{ err error }

func (t unreadableTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, t.err
}

func (t unreadableTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	return nil, t.err
}

// latestWatch reads where the session that chooses work got to. A product
// nobody has watched has no session rather than an idle one, which is why the
// absence is carried as a nil rather than as a state: never having watched and
// having stopped watching are different answers to the question being asked.
//
// It resolves its own store for the reason the run records do: reading what a
// session said needs no repository and no worktree, and a verb reached for when
// something looks wrong must not refuse over where a checkout happens to sit.
func latestWatch(configPath string) (*runstate.WatchTransition, string) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	store, err := runstate.NewWatchStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	latest, watched, err := store.Latest()
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	if !watched {
		return nil, ""
	}
	return &latest, ""
}

// printWatch says where the session that chooses work got to, in one line above
// the runs. A product nobody has ever watched says nothing at all rather than
// asserting that nothing is running: this command has never known that, and a
// line claiming it would be the confident emptiness the rest of this file
// avoids.
func printWatch(writer io.Writer, watched *runstate.WatchTransition) {
	if watched == nil {
		return
	}
	// The one stop that is not an ending says so, and so does the one idle poll
	// that never read the queue. Both are the transition's own mark rather than a
	// reading taken here, for the reason everything else on this line is: a reader
	// told a session is stopped when it is on its way back looks for somebody to
	// start it, and one told it is idle through a store outage looks for work to
	// admit.
	fmt.Fprintf(writer, "the session choosing work is %s as of %s",
		readmodel.SessionSays(*watched), watched.At.UTC().Format(time.RFC3339))
	if reason := strings.TrimSpace(watched.Reason); reason != "" {
		fmt.Fprintf(writer, ": %s", reason)
	}
	fmt.Fprintln(writer)
}

// recordedStalls reads the product's record of having gone quiet. It resolves
// its own store for the reason the run records and the watch log do: reading
// what happened needs no repository and no worktree, and a verb reached for when
// something looks wrong must not refuse to answer over where a checkout happens
// to sit.
func recordedStalls(configPath string) ([]runstate.StallEvent, string) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, fmt.Sprintf("what this product recorded about going quiet could not be read: %v", err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, fmt.Sprintf("what this product recorded about going quiet could not be read: %v", err)
	}
	store, err := runstate.NewStallStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, fmt.Sprintf("what this product recorded about going quiet could not be read: %v", err)
	}
	events, err := store.List()
	if err != nil {
		return nil, fmt.Sprintf("what this product recorded about going quiet could not be read: %v", err)
	}
	return events, ""
}

// printStalls says what the product recorded about having gone quiet, newest
// first, above the runs.
//
// A product that has never gone quiet says nothing at all rather than saying so:
// the absence is the ordinary state, and a line asserting it on every reading is
// a line every reader learns to skip. A stall that is still standing leads,
// because it is the one thing on this whole listing that is happening now.
func printStalls(writer io.Writer, events []runstate.StallEvent) {
	if len(events) == 0 {
		return
	}
	shown := 0
	for index := len(events) - 1; index >= 0 && shown < defaultStatusStalls; index-- {
		event := events[index]
		// The age is said as well as the moments, because the moments are what a
		// reader would have to subtract and the age is the fact they are after.
		if event.Open() {
			fmt.Fprintf(writer, "nothing has started on this product since %s (%s ago), noticed %s, with %s ready\n",
				event.Since.UTC().Format(time.RFC3339), event.For().Round(time.Minute),
				event.OpenedAt.UTC().Format(time.RFC3339), stalledItems(event.Ready))
		} else {
			fmt.Fprintf(writer, "nothing started on this product for %s from %s, with %s ready; it cleared at %s\n",
				event.For().Round(time.Minute), event.Since.UTC().Format(time.RFC3339),
				stalledItems(event.Ready), event.ClosedAt.UTC().Format(time.RFC3339))
		}
		// What the thing that chooses work last said is the whole of what tells a
		// dead scheduler from a wedged one, so it is under the stall rather than
		// left to --json.
		if chooser := strings.TrimSpace(event.Chooser); chooser != "" {
			fmt.Fprintf(writer, "  %s\n", chooser)
		}
		if cleared := strings.TrimSpace(event.Cleared); cleared != "" {
			fmt.Fprintf(writer, "  cleared by: %s\n", cleared)
		}
		shown++
	}
	if remaining := len(events) - shown; remaining > 0 {
		fmt.Fprintf(writer, "%d further recorded stall(s) are not listed here; --json carries all of them\n", remaining)
	}
}

// stalledItems says how much work waited through a stall, and states an
// uncounted queue as uncounted: a stall recorded before the count was carried is
// not a stall over an empty queue, and the two are opposite news.
func stalledItems(ready int) string {
	switch {
	case ready <= 0:
		return "a number of items the record does not carry"
	case ready == 1:
		return "1 item"
	default:
		return fmt.Sprintf("%d items", ready)
	}
}

// printItemTriage says what triage has spent on the named item and what the
// item has cost in review rounds. It is printed under the runs because it is a
// different kind of fact from any of them: every line above belongs to one run,
// and every figure here spans all of them.
//
// The rounds come first because they are the budget two of the three actions are
// refused against, and the one figure that moves without anybody deciding
// anything. An item triage has spent something on more than once says that in
// the first clause, because that is the thing a reader is looking for: work that
// came back is work where something other than the change may be wrong.
//
// Every figure here is a budget, and the last line says so. Three of the
// development manager's seven decisions spend one — a repair grant, a re-run, a
// merge re-arm — and the other four cost nothing and reach no counter, so an
// item that was escalated or told to wait shows zeroes; a crossing moves a cap
// rather than a count, and the crossing lines are where it shows. A reader looking for
// whether anybody has looked at stopped work is looking at the item, and the
// line points them there rather than letting these zeroes answer a question they
// were never counting.
// The caps it is given are the effective ones — the configured ceilings as this
// item's own recorded overrides leave them — because they are what refuses the
// next decision. An operator who crossed a cap and is then shown the configured
// figure has been told their decision did not take.
func printItemTriage(writer io.Writer, counters runstate.TriageCounters, caps runstate.TriageCaps) {
	fmt.Fprintf(writer, "triage of %s: %s\n", counters.WorkItemID, describeTriagePasses(counters))
	// The rounds line states the rounds and nothing else. Every conclusion it
	// used to assert acquired a second predicate sooner or later — the grant
	// budget refuses repairs the rounds would allow, and a re-arm ignores the
	// rounds entirely — so what may still happen is said by the budget lines
	// below, each beside the numbers that decide it.
	// A cleared cap is its own line rather than a figure in either of the other
	// two: neither of them reads as a sentence with "no cap" substituted into the
	// place a number goes, and this is the line an operator checks first.
	switch {
	case caps.ReviewRounds == runstate.TriageCapCleared:
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item, under no cap at all — the operator cleared it\n",
			counters.ReviewRounds)
	case counters.RoundsRemaining(caps.ReviewRounds) == 0:
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item — at or past the cap of %d, so no decision that buys a round remains\n",
			counters.ReviewRounds, caps.ReviewRounds)
		// What changes that, said where the operator meets the dead end rather than
		// left to be found. This is the command that crosses it by any ceiling; the
		// development manager crosses it by one himself, which is said here because
		// an operator who reads a cap at its limit is entitled to know the item may
		// move without them. An override recorded anywhere else — the item's own
		// notes included, which is where two of them went — reaches no guard.
		fmt.Fprintf(writer, "    `yoyo triage override --budget %q --cap <n> --by \"<you>\" --reason \"<why>\" %s` crosses it to any ceiling; the development manager may also cross it far enough for one more decision, %d times per item, and each of those reaches you in the channel as it happens\n",
			runstate.TriageReviewRoundBudget, counters.WorkItemID, runstate.MaxDelegatedCapCrossings)
	default:
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item, under the cap of %d\n",
			counters.ReviewRounds, caps.ReviewRounds)
	}
	// Each of these is refused by two budgets rather than one, and the line says
	// both: the rounds above, which bound what the item may cost, and its own,
	// which bounds how often triage may decide the same thing about it. An
	// operator told only about the rounds would read an item refused a second
	// re-run with rounds to spare as a bug.
	fmt.Fprintf(writer, "  repair grants: %d of %s permitted; re-runs: %d of %s; each is refused by its own budget or once no round remains\n",
		counters.RepairGrants, triageCapFigure(caps.RepairGrants), counters.Reruns, triageCapFigure(caps.Reruns))
	// The re-arm's ceiling is the only one here that is not the item's. A re-arm
	// repeats one merge request the reviewer's verdict already authorized, so it
	// is bounded per publication, and an item that published three times has three
	// separate budgets. So the count is stated as the total it is, the cap beside
	// what it actually bounds, and every publication that has spent any of it is
	// named — an operator reading "3 of 1 permitted" against one figure would read
	// a working budget as a broken one.
	fmt.Fprintf(writer, "  merge re-arms: %d across every publication of this item, %s permitted per publication\n",
		counters.MergeRearms, triageCapFigure(caps.MergeRearms))
	for _, publication := range slices.Sorted(maps.Keys(counters.RearmedPublications)) {
		fmt.Fprintf(writer, "    %s: %d of %s permitted\n",
			publication, counters.RearmedPublications[publication], triageCapFigure(caps.MergeRearms))
	}
	// A grant that was cut is said out loud, because it is the fact that says the
	// item is at the end of what it will be given: the next grant has nothing left
	// to truncate to and is refused outright.
	if counters.TruncatedGrants > 0 {
		fmt.Fprintf(writer, "  %d grant(s) were cut down to the rounds the cap still had room for; %d round(s) were granted in total\n",
			counters.TruncatedGrants, counters.GrantedRounds)
	}
	// A cap somebody crossed is said out loud, with who crossed it and why. Every
	// figure above is one of these caps, so an operator reading a budget larger
	// than the project configured and finding no account of it here would have to
	// go looking in the state directory for the reason.
	//
	// Whose crossing each one was is the label rather than a detail inside the
	// sentence, because they are read for opposite reasons: the operator's own
	// override is something they did and can remember doing, and a delegated
	// crossing is something that happened while they were not asked. The count of
	// the second is said under them, so an operator scanning an item can see how
	// much of the delegation it has used without counting lines.
	for _, override := range counters.Overrides {
		label := "operator override"
		if override.Delegated() {
			label = "cap crossed on delegated authority"
		}
		fmt.Fprintf(writer, "  %s: %s\n", label, override.Describe())
	}
	if crossings := counters.DelegatedCrossings(); crossings > 0 {
		fmt.Fprintf(writer, "  %d of %d cap crossing(s) the development manager may take himself are recorded; past that the caps are yours again\n",
			crossings, runstate.MaxDelegatedCapCrossings)
	}
	fmt.Fprintln(writer, "  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say")
}

// triageCapFigure is one ceiling as a reader reads it. A cleared cap is a number
// no count comes near rather than a number anybody means, so printing it would be
// a line an operator has to decode. Who cleared it is not repeated on every
// budget: the override line beneath these figures says so once.
func triageCapFigure(limit int) string {
	if limit == runstate.TriageCapCleared {
		return "no cap"
	}
	return strconv.Itoa(limit)
}

// describeTriagePasses says how many times triage has spent something on an
// item, in the three cases that read differently: never, once, and again.
//
// It counts spending rather than deciding, and says so, because those stopped
// being the same thing when triage acquired decisions that cost nothing. An item
// the development manager escalated has been triaged and has been given nothing,
// and a line reading "triage has not acted on it" would tell an operator looking
// for evidence somebody looked that nobody had.
func describeTriagePasses(counters runstate.TriageCounters) string {
	switch passes := counters.Passes(); passes {
	case 0:
		return "triage has spent nothing on it"
	case 1:
		return "triage has spent one pass on it"
	default:
		return fmt.Sprintf("triage has spent %d passes on it", passes)
	}
}

// printRunHistory reports the selected runs, one block each. The reasons are
// each named for what they are and printed under the run they belong to rather
// than in a column, because a reason is a sentence somebody wrote and a column
// wide enough for one would leave no room for anything else.
func printRunHistory(writer io.Writer, history runstate.RunHistory, workItemID string, failedOnly bool) {
	if history.Recorded == 0 {
		fmt.Fprintln(writer, "the harness has no recorded runs, so there is nothing to report")
		return
	}
	if len(history.Runs) == 0 {
		fmt.Fprintf(writer, "no %s, of the %d run(s) recorded\n", describeRunSelection(workItemID, failedOnly), history.Recorded)
		return
	}
	fmt.Fprintf(writer, "%s, %d of %d shown (%d run(s) recorded):\n",
		describeRunSelection(workItemID, failedOnly), len(history.Runs), history.Matched, history.Recorded)
	reasoned := false
	for _, run := range history.Runs {
		fmt.Fprintf(writer, "%s %s started %s [%s] %s\n",
			run.RunID, run.WorkItemID, run.StartedAt.UTC().Format(time.RFC3339),
			renderRunState(run), renderSummaryCost(run))
		if printRunReasons(writer, run) {
			reasoned = true
		}
		printRunArtifacts(writer, run)
		printOutstandingSteps(writer, run)
	}
	if remaining := history.Matched - len(history.Runs); remaining > 0 {
		fmt.Fprintf(writer, "%d further run(s) are not listed here; --limit reports more, and 0 reports all of them\n", remaining)
	}
	if reasoned {
		fmt.Fprintln(writer, "each reason is shown as one line; --json carries what the record holds in full")
	}
}

// printRunReasons prints what one run recorded about how it went, and reports
// whether it recorded anything at all. Each reason is labelled, because the
// record keeps them apart on purpose: only the first says the work failed, and
// the others are things that happened around work that may well have landed —
// including a completion record that took a late write to land, the one class
// whose work-item note is itself unreliable.
// Each is folded and bounded by singleLine, so a reviewer's verdict is one row
// of the listing rather than a page of it; --json carries the whole of it.
func printRunReasons(writer io.Writer, run runstate.RunSummary) bool {
	// Why the harness was running this item at all comes first, because it is the
	// only one of these that is about the choice rather than the outcome. It is
	// printed for every run rather than only for the ones that recorded it: a run
	// with no reason is exactly what an operator most needs to see, and omitting
	// the line would make it look like a run whose reason they had already read.
	if run.Selection != nil {
		fmt.Fprintf(writer, "  selected by the %s: %s\n", run.Selection.By, singleLine(run.Selection.Reason))
	} else {
		fmt.Fprintln(writer, "  selected: no reason recorded")
	}
	// What the run was spent on, what set it up, and which harness dispatched it
	// are printed for every run, and for the reason the selection line is: there
	// is one account today, so a line that appeared only where several existed
	// would be a line nobody was reading on the day the second one arrived. A
	// record that names none of them is a record written before they were carried,
	// and says so.
	//
	// The build is on the same line because it answers the same class of question
	// and is read at the same moment: an operator asking why a run behaved the way
	// it did needs to know whether the code it ran was the code that was merged,
	// and a run that cannot say is a defect nobody can classify.
	fmt.Fprintf(writer, "  ran under %s, configuration %s, harness %s\n",
		recorded(run.AccountAlias, "an account the record does not name"),
		recorded(run.ConfigRevision, "a configuration the record does not name"),
		recorded(shortBuild(run.Build), "a build the record does not name"))
	printed := true
	if class := run.RecordedStopClass(); class != "" {
		fmt.Fprintf(writer, "  stop cause: %s\n", class)
	}
	// The reason leads with the class that stopped the run, so which gate stopped
	// it is read off the record rather than inferred from whichever evidence is
	// printed below it. Where the reason borrowed its words from one of the three
	// accounts of a succeeded run, that account is not printed a second time.
	reason := run.Reason()
	if reason != "" {
		fmt.Fprintf(writer, "  reason: %s\n", singleLine(reason))
	}
	for _, account := range []struct {
		label string
		text  string
	}{
		{label: "outstanding publication", text: run.PublishFailure},
		{label: "outstanding cleanup", text: run.CleanupFailure},
		{label: "completion recorded late", text: run.CompletionRecordingFailure},
	} {
		if account.text == "" || reason == runstate.StopReason(run.StopClass, account.text) {
			continue
		}
		fmt.Fprintf(writer, "  %s: %s\n", account.label, singleLine(account.text))
	}
	// An approved change the environment stopped is said beside its reason,
	// because the reason alone reads as a failed piece of work and sends an
	// operator to the verbs that each spend something for it. This names the one
	// that spends nothing — while the branch is there, by the rule the docket and
	// the pull's hold ask it by. Once it is gone the resume would refuse, so the
	// line says what is gone and that a re-run is the way on instead.
	if run.IntegrationStop != nil {
		if triage.IntegrationResumable(run.Found, run.BranchRemoved) {
			fmt.Fprintf(writer, "  integration stop: %s; `yoyo triage resume %s` resumes it at no cost once the cause has cleared\n",
				singleLine(run.IntegrationStop.Describe()), run.RunID)
		} else {
			// Each half is folded on its own, so the re-run is never the part a
			// fold cuts; the branch and worktree lines below say what was found
			// in full, so the clause here is the listing's short one.
			fmt.Fprintf(writer, "  integration stop: %s; %s\n",
				singleLine(run.IntegrationStop.Describe()), singleLine(triage.IntegrationGoneSays(run.RunID, run.DescribeRemains())))
		}
		printed = true
	}
	if run.FailingCheck != nil {
		fmt.Fprintf(writer, "  failing check: %s exited %d\n", singleLine(run.FailingCheck.Command), run.FailingCheck.ExitCode)
		printed = true
	}
	// The check stage is said where it is what the run is doing now or what
	// stopped it: how much of its bound it spent, and the check it was on. A
	// stage that ended inside its bound is the ordinary case and says nothing
	// here — the reason line above already answers for a run the bound stopped,
	// and this is the figure beside it.
	//
	// A stage still running is only said of a run still in flight: a run that
	// ended with its stage open is one whose process died inside it, and the
	// sweep closes that stage as interrupted when it settles the run.
	if stage := run.CheckStage; stage != nil && ((stage.Running() && run.Status.InFlight()) || stage.StoppedAtBound || stage.Interrupted) {
		fmt.Fprintf(writer, "  %s\n", singleLine(stage.Describe(time.Now())))
		printed = true
	}
	// A check that said it could not run is said on every run it happened in,
	// with its reason, because nothing else about the run shows it: it stopped
	// nothing and spent nothing, so the run reads exactly as one whose checks
	// all ran.
	if stage := run.CheckStage; stage != nil {
		for _, unrun := range stage.CouldNotRun {
			fmt.Fprintf(writer, "  check %s\n", singleLine(unrun.Says()))
			printed = true
		}
	}
	// What the landing checks made of the integrated commit is said on every run
	// that has one, because a red landing is the one fact about a landed change
	// that its run's ending does not carry: the run succeeded, and the target
	// branch is red.
	if run.LandingChecks != nil {
		fmt.Fprintf(writer, "  %s\n", singleLine(run.LandingChecks.Describe()))
		printed = true
	}
	// A refused path is said here rather than left to the run's JSON for the
	// reason the failing check is: it is what stopped the run, and the worktree
	// that would have shown it is removed when the run is cleaned up.
	if run.RefusedPaths != nil {
		refused := strings.Join(run.RefusedPaths.Paths, ", ")
		if run.RefusedPaths.Omitted > 0 {
			refused = fmt.Sprintf("%s, and %d more", refused, run.RefusedPaths.Omitted)
		}
		fmt.Fprintf(writer, "  refused paths: %s\n", singleLine(refused))
		printed = true
	}
	// Nor is a truncated item, and for the same reason: the run delivered exactly
	// as it would have, on an item saying slightly less than the tracker holds. It
	// is printed because nothing else shows it — an item's notes only ever grow,
	// so this arrives on some ordinary run nobody decided anything about, and stays
	// on every run of that item afterwards until somebody sees it.
	if run.ContextTruncation != nil {
		fmt.Fprintf(writer, "  work item truncated for context: the oldest %d note(s), %d bytes, were not delivered\n",
			run.ContextTruncation.DroppedNotes, run.ContextTruncation.DroppedBytes)
		printed = true
	}
	// A stale blocked status the claim met is said whichever way its clear
	// ended, because the ending that matters is the one the reason line alone
	// reads as a run that died at the claim: no read confirmed the clear, and the
	// item was left for the next pull rather than claimed. The label names the
	// status and not the clear, because an unconfirmed clear is reported never
	// as cleared and the read model's own words are what say which it was.
	if run.StaleBlockClear != nil {
		fmt.Fprintf(writer, "  stale blocked status at the claim: %s\n", singleLine(run.StaleBlockClear.Describe()))
		printed = true
	}
	// Nor is a report or a proposal the harness could not keep: the run delivered
	// exactly as it would have, and what was lost is beside it. They are printed
	// because this record is the only place they survive the run — a refused
	// proposal that reached only the run's printed outcome was, afterwards, one
	// nobody could tell from a proposal never made.
	if run.ReportProblem != "" {
		fmt.Fprintf(writer, "  report not kept: %s\n", singleLine(run.ReportProblem))
		printed = true
	}
	if run.AmendmentProblem != "" {
		fmt.Fprintf(writer, "  proposal not kept: %s\n", singleLine(run.AmendmentProblem))
		printed = true
	}
	// A restatement dropped in favour of a proposal already raised is kept on
	// this record and nowhere else, so it is printed with what it was folded into
	// and how alike the two read: a fold the comparison got wrong has cost the
	// second argument its decision, and this is where somebody finds that.
	for _, proposed := range run.Amendments {
		if !proposed.Dropped() {
			continue
		}
		fmt.Fprintf(writer, "  restatement dropped: the %s's change to %s was folded into %s (likeness %.2f): %s\n",
			proposed.Role, proposed.Artifact, proposed.FoldedInto, proposed.Likeness, singleLine(proposed.Change))
		printed = true
	}
	// A divergence is not a reason the run ended and is never read as one: the run
	// delivered exactly as it would have, and what diverged is the observation
	// beside it. It is printed because every run executes the definition by
	// default now, and a disagreement nobody is shown is one nobody acts on.
	if run.WorkflowDivergence != "" {
		fmt.Fprintf(writer, "  workflow divergence: %s\n", singleLine(run.WorkflowDivergence))
		printed = true
	}
	// A run nothing observed is printed for the same reason and more loudly than
	// the field's absence would be: it is not a reason the run ended either, and
	// it is the one shape that reads as an agreeing run to anybody counting
	// divergences, because it has none and never could have had one.
	if run.WorkflowUnobserved != "" {
		fmt.Fprintf(writer, "  workflow unobserved: %s\n", singleLine(run.WorkflowUnobserved))
		printed = true
	}
	return printed
}

// printRunArtifacts names what a run that did not succeed left behind, which is
// the question a listing of those runs is actually read for: whether the work is
// gone. The brackets above answer that in a word; these lines say where it is,
// so an operator who wants to look at the change is not sent to the run's JSON
// for the path.
//
// It is silent on a run that succeeded and on one still in flight. A successful
// run removes its worktree and branch by design, so naming them would report the
// harness working as a loss; a run still going holds everything it has by
// definition, so saying so of every one of them would make "preserved" mean
// nothing on the records where it means preserved work nobody has looked at.
//
// An artifact the harness recorded as removed is named as removed rather than
// left out. Sending somebody to a worktree that is gone and telling them nothing
// was preserved are the same failure in opposite directions, and the record
// distinguishes them. A run whose record names neither says nothing here at all:
// the brackets above already say the record names no artifact, and a line
// repeating it on every run that broke before it made anything is a line every
// reader learns to skip.
func printRunArtifacts(writer io.Writer, run runstate.RunSummary) {
	if !run.Status.Terminal() || run.Outcome == runstate.OutcomeSucceeded {
		return
	}
	// Where the repository was asked, what it said is what is printed, and how
	// it was established beside it: a branch checked and there and a branch a
	// record says nothing removed are different claims, and run-838ffc48 was
	// decided about on the second while its branch held the approved change.
	if found := run.Found; found != nil && found.Recorded() {
		if run.Branch != "" {
			fmt.Fprintf(writer, "  branch (%s): %s\n", found.BranchState(), run.Branch)
		}
		if run.WorktreePath != "" {
			fmt.Fprintf(writer, "  worktree (%s): %s\n", found.WorktreeState(), run.WorktreePath)
		}
		printPreservedDetail(writer, run)
		return
	}
	if run.Branch != "" {
		if run.BranchRemoved {
			fmt.Fprintf(writer, "  branch already removed: %s\n", run.Branch)
		} else {
			fmt.Fprintf(writer, "  preserved branch: %s\n", run.Branch)
		}
	}
	if run.WorktreePath != "" {
		if run.WorktreeRemoved {
			fmt.Fprintf(writer, "  worktree already removed: %s\n", run.WorktreePath)
		} else {
			fmt.Fprintf(writer, "  preserved worktree: %s\n", run.WorktreePath)
		}
	}
	printPreservedDetail(writer, run)
}

// printPreservedDetail names the session and the findings, which are said only
// where the change survives. A session that continues work nothing holds any
// more continues nothing, and findings about a change that is gone are a
// reading list rather than something to act on.
func printPreservedDetail(writer io.Writer, run runstate.RunSummary) {
	if !run.Preserved() {
		return
	}
	if run.ProviderSessionID != "" {
		fmt.Fprintf(writer, "  preserved developer session: %s\n", run.ProviderSessionID)
	}
	if run.ReviewFindings > 0 {
		fmt.Fprintf(writer, "  %d review finding(s) recorded against the preserved change\n", run.ReviewFindings)
	}
}

// recorded says what a record holds for one field, or states the absence in
// words. A blank in a listing reads as a bug in the listing rather than as a
// record that was written before the field existed, which is what an absence
// here actually is.
func recorded(value, absence string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return absence
}

// shortBuild names a revision the way somebody quoting one does. A run's record
// keeps the whole object name, which --json carries; a listing wants the prefix
// a person would type into `git show`.
func shortBuild(build string) string {
	if trimmed := strings.TrimSpace(build); len(trimmed) > 12 {
		return trimmed[:12]
	}
	return build
}

// printOutstandingSteps says what a finished run still owes, so a run marked
// outstanding is never marked and then left unexplained — which would be the
// "go and read the run's JSON" case this verb exists to remove.
//
// Nothing here is a recorded reason, and that is why it is derived rather than
// read out of a field: what a finished run owes is decided by the state it is
// in, and there are exactly two things it can be. The last branch is not dead
// code but the honest answer if that ever stops being true: a run the record
// says owes something, whose state does not say what, is reported as owing
// something rather than silently as owing nothing.
func printOutstandingSteps(writer io.Writer, run runstate.RunSummary) {
	if !run.Outstanding || !run.Status.Terminal() {
		return
	}
	printed := false
	if run.Integrated && run.Phase != runstate.PhaseComplete {
		fmt.Fprintln(writer, "  outstanding: its work is promoted, and cleaning up after it is not recorded as finished")
		printed = true
	}
	if run.MergeQueued {
		fmt.Fprintln(writer, "  outstanding: the forge queued the merge of its pull request and nothing has settled it since")
		printed = true
	}
	if !printed {
		fmt.Fprintln(writer, "  outstanding: it still owes a step; `yoyo reconcile` reports which and settles it")
	}
}

// describeRunSelection names what was asked for, so an empty answer says which
// question it is empty about: no failed run and no run at all are different
// answers, and so are the whole record and one item's part of it.
func describeRunSelection(workItemID string, failedOnly bool) string {
	selection := "runs"
	if failedOnly {
		selection = "runs that ended without succeeding"
	}
	if workItemID != "" {
		selection += " of " + workItemID
	}
	return selection
}

// renderRunState says what became of a run and what remains of it: the outcome,
// the phase it reached, whether it promoted anything, whether its change
// survives, and — on a run that is over — whether it still owes somebody a step.
//
// It leads with the outcome rather than the durable status, because the status
// answers a question nobody is asking here. "failed" is true of a run whose
// reviewer blocked it, of one the target branch outran, of one the provider kept
// killing, and of one that broke before it made anything — and the first three
// leave a branch, a worktree, a session, and an item back in somebody's hands.
// An operator reading that one word has been told the attempt is over and
// nothing about whether the work is.
//
// Preservation is stated on every run that did not succeed, in the three fixed
// phrases the read model renders, and on no others: a successful run removes
// what it made on purpose, and a run still in flight holds everything by
// definition. The phrases are the read model's rather than this file's for the
// reason the outcome word is — the channel says the same three about the same
// run — and the same discipline `recorded` below keeps holds inside them: an
// absence is stated as an absence rather than read as work thrown away. The
// outstanding marker keeps the same rule and for the same reason it always had.
func renderRunState(run runstate.RunSummary) string {
	state := string(run.Outcome)
	switch {
	case run.ResumingIntegration:
		// The read model's own phrase for a promotion going again after the
		// environment stopped it, in place of the bare phase: the same words the
		// Running line says about the same run.
		state += ", " + runstate.ResumingIntegrationSays
	case run.Phase != "":
		state += ", " + string(run.Phase)
	}
	if run.Integrated {
		state += ", integrated"
	}
	if run.Status.Terminal() && run.Outcome != runstate.OutcomeSucceeded {
		state += ", " + run.DescribeRemains()
	}
	if run.Outstanding && run.Status.Terminal() {
		state += ", outstanding"
	}
	return state
}

// renderSummaryCost says what a run spent, in the terms the ledger uses: a run
// still going reports what it has spent so far rather than a figure that reads
// as final, and a run whose evidence is gone is stated as unpriceable rather
// than as free.
func renderSummaryCost(run runstate.RunSummary) string {
	if !run.CostKnown() {
		return "cost unknown"
	}
	if !run.Status.Terminal() {
		return run.Tokens.CostText(run.CostUSD) + " so far"
	}
	return run.Tokens.CostText(run.CostUSD)
}

func reportStatusFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, statusOutput{Runs: []runstate.RunSummary{}, Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "status failed: %v\n", err)
	return 1
}

func printStatusUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo status [options] [<beads-id>]
       yoyo status --follow [options] [<id>]
       yoyo status --events [options] [<id>]
       yoyo status --list [options]
       yoyo status --spend [options] [<id>|<days>]
       yoyo status --shipped [options] [<n>]

Where the harness stands, then what became of the runs it made.

Naming no item prints four lines first, and prints all four every time: what is
Running, with each run's item, phase, elapsed time and spend; what is Working,
which is the persona conversations with a turn in flight; what is Not startable,
which is each admitted item nothing will pull with the refusal that stops it; and
what Needs a human, which is either "nothing" or the list with who each one is
waiting on. A line with nothing in it says so in words, and a line whose records
could not be read says that instead of saying nothing. Naming an item leaves them
out: they are about the product, and a question about one item is a different
question.

Under them, what this product recorded about having gone quiet: each stretch where
nothing started at all while the tracker reported work ready and no hold, no full
machine and no run in flight accounted for it, with what the thing that chooses
work last said before it went silent. A product that has never gone quiet says
nothing here. It is the one history nothing else keeps: the process that would
have recorded a stall is the process a stall means has died.

Under that, what became of the runs the harness made, newest first: the work item, the
outcome it reached, the phase it was in, what it cost, and the reasons its record
kept. Naming an item reports only its runs, and under them what triage has spent
on that item: the review rounds it has cost across every run of it, against the
cap that bounds them, and the repair grants, re-runs, and merge re-arms it has
been given. An item triaged more than once says so there.

The outcome in the brackets is one of a small fixed set. "succeeded" landed its
work. "stopped" ended on a durable blocker: the item carries it, a person decides
what happens next, and nothing was discarded — an unrepaired review, a check that
kept failing, refused paths, a replay the target branch outran, and a provider
that would not carry the run all end this way, and the reason under the run says
which. "cancelled" was stopped rather than judged. "timed out" was stopped on
time. "failed" is what is left: a run that ended leaving nobody anything to act
on. A run still going says "pending" or "running" instead.

Beside it, every run that did not succeed says what remains of it: "work
preserved", "work removed" for artifacts the harness recorded removing, or "no
artifacts recorded" where the record names neither. The last states an absence
rather than claiming the run made nothing, and in practice it is a run that broke
before it got a worktree: a run that reached any phase has one. The preserved
branch, worktree, and developer session are named under the run. A successful run
removes what it made on purpose, so it says nothing about preservation; a run
still in flight holds everything it has.

Each recorded reason is printed under the run and named for what it is. Only
"reason" is the run's own account of why it ended; an outstanding publication, an
outstanding cleanup, a failing check, and a completion recorded late are things
recorded around the work, and a run can carry one of those with its change
already promoted. The last is the class whose work-item note is itself unreliable —
recording that note is part of what was failing — so this listing is its
authoritative home.

Reading a run decides nothing about it, so this holds nothing and settles
nothing, and reporting a failure is not itself a failure: the exit status says
whether the records could be read. Settling what an interrupted run left behind
is `+"`yoyo reconcile`"+`.

--follow, --events, --list, and --spend read the event stream a run, a
conversation, a branch review, and a side thread each record, rather than the
run records. It is the same question asked of all four -- is this alive, what
is it doing, and what did it cost -- so every one of them covers all four and
the default never
asks which kind you meant; --kind narrows it when that is what you want. There,
the id names a stream or a unique prefix of one rather than a work item.

--spend groups what was spent by the local-timezone day it was spent on, each
day's group closing with that day's spend and today's coming last: what an
operator budgets against is what today cost, and the day they mean is the one
their own clock is keeping. What counts on a day is each invocation rather than
the log it was recorded in, so a conversation open for a fortnight appears under
every day it spent on. A side thread's rows name the conversation it was opened
beside, which is whose the money was. The exchanges the roles conducted are priced beside the
streams, each round on the day it was answered, and narrowed out with the
streams when --kind names one of them. A number asks for a different count of
days; naming a stream or an exchange prices that one whatever day it ran on.
Under the total, a table per role says what each paid to write the provider's
cache and what it paid to read it, apportioned from the provider's own figure
at its rate multiples: the one cache-read share above it is decided by whichever
role reads the most, and hides a role that writes its whole prompt into the cache
and reads none of it back.
`+"`yoyo cost`"+` is the same run spending grouped by the work item the runs were
for.

--shipped lists the n most recently shipped work items, most recent promotion
first, ten unless a number says otherwise and 0 for every one: each with what it
cost the provider across every run made for it, the failed and repair attempts
included; the wall clock from the first claim to the promotion; how much of that
it spent parked on a provider usage limit or the operator's pause; how many runs
it took; and its title as the run that shipped it recorded it. Elapsed and
paused are two figures on purpose, so an item that spent three of its four hours
waiting reads as what it was. Totals across the listing close it. An item is
shipped when a run of it recorded promoting its work; a run with no surviving
record to price makes its cost a floor marked ≥, an elapsed the record cannot
compute says unknown, and neither is ever reported as nothing. The join it prices
is `+"`yoyo cost`"+`'s: the ledger is that join read for the shipped items and
sorted by when each shipped.

Every live mode leads with a banner while activity is paused or intake is held:
a machine somebody paused and a machine that died look identical otherwise. The
recorded mode says the same on its "Needs a human" line.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --failed          only the runs that ended without succeeding
  --limit <n>       report at most this many, newest first (default 20, or 10 with --shipped; 0 reports all)
  --json            emit machine-readable JSON

Live options:
  --follow          follow a stream's events as they arrive
  --events          print a stream's recent events and exit, without following
  --list            list the recent runs, conversations, and branch reviews
  --spend           report what was spent, by the local day it was spent on
  --shipped         list the most recently shipped items with cost, elapsed, and paused time
  --latest          with --follow, move to a later stream when one starts
  --lines <n>       replay this many recorded events first (default 50; 0 the whole log)
  --kind <kind>     runs, chats, reviews, sides, exchanges (--spend only), or all (default all)
  --all             include the thinking-token events the default leaves out
  --raw             emit each event exactly as it was recorded`)
}
