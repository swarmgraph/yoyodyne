package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type costOutput struct {
	Prices []runstate.ItemPrice `json:"prices,omitempty"`
	// Exchanges is what the roles spent asking each other things. It is beside
	// the item prices rather than in one of them because the exchange record
	// carries nothing to join an item on -- a product, a repository, the two
	// roles, and the asker's conversation -- and it is here at all because it is
	// money the harness spent: a total that skipped it would be wrong rather than
	// merely unattributed.
	Exchanges *runstate.ExchangeSpend `json:"exchanges,omitempty"`
	// SideStreams is what the agents' side conversations spent, split by the
	// conversation each was opened beside. It is beside the item prices for the
	// reason a conversation turn is: it belongs to a conversation, not an item.
	SideStreams *runstate.SideStreamSpend `json:"side_streams,omitempty"`
	// Recorded is what was written onto the tracker, and is absent from a report
	// that only read the records.
	Recorded []recordedCost `json:"recorded,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// recordedCost is one item's price as it was written, or the reason it was not.
type recordedCost struct {
	WorkItemID string     `json:"work_item_id"`
	Cost       beads.Cost `json:"cost"`
	Failure    string     `json:"failure,omitempty"`
}

// costTrackerTimeout bounds one bd command taken to record a price. It is
// carried by the tracker the ledger writes through rather than applied at a call
// site, so every write is bounded the same way whether it is one item or a
// backfill of all of them: a tracker that has gone slow costs a backfill an item
// rather than the whole ledger.
const costTrackerTimeout = 30 * time.Second

// reportCosts prices work items from the runs the harness recorded for them.
// Reading is the default and recording is asked for, because writing a price
// onto every item in a tracker is a change somebody should have to mean.
func reportCosts(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cost", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	record := flags.Bool("record", false, "write each price onto its work item in the tracker")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "cost accepts at most one Beads work item id")
		printCostUsage(stderr)
		return 2
	}
	// The item is optional, so it is read through argumentAt rather than indexed:
	// `yoyo cost` with nothing named prices every item the runs cover.
	workItemID := argumentAt(positional, 0)

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportCostFailure(stdout, stderr, *jsonOutput, err)
	}
	prices, err := readPrices(parts, workItemID)
	if err != nil {
		return reportCostFailure(stdout, stderr, *jsonOutput, err)
	}
	readmodel.LookForPrices(ctx, parts.worktrees, parts.store, prices)
	output := costOutput{Prices: prices}
	// What the roles spent asking each other is read alongside the runs whenever
	// the whole ledger is. Naming an item asks what that item's runs cost, and an
	// exchange is not one of them: adding it to an item's own price would be
	// attributing to that item money that names no item at all.
	if workItemID == "" {
		exchanges := parts.store.ExchangeSpend()
		output.Exchanges = &exchanges
		sides := parts.store.SideStreamSpend()
		output.SideStreams = &sides
	}
	failed := false
	if *record {
		recorded, err := recordPrices(ctx, parts, workItemID)
		if err != nil {
			return reportCostFailure(stdout, stderr, *jsonOutput, err)
		}
		output.Recorded = recorded
		for _, entry := range recorded {
			failed = failed || entry.Failure != ""
		}
	}

	if *jsonOutput {
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		printPrices(stdout, prices, output.Exchanges, output.SideStreams, workItemID != "")
		printRecordedPrices(stdout, stderr, output.Recorded)
	}
	if failed {
		return 1
	}
	return 0
}

func readPrices(parts components, workItemID string) ([]runstate.ItemPrice, error) {
	if workItemID != "" {
		price, err := parts.store.Price(workItemID)
		if err != nil {
			return nil, err
		}
		return []runstate.ItemPrice{price}, nil
	}
	prices, err := parts.store.Prices()
	if err != nil {
		return nil, err
	}
	return prices, nil
}

// recordPrices writes what was read onto the tracker. An item the harness has
// never run is skipped rather than priced at nothing, so a backfill leaves
// untouched exactly the items it has no evidence about.
func recordPrices(ctx context.Context, parts components, workItemID string) ([]recordedCost, error) {
	ledger := ledgerFrom(parts)
	if workItemID != "" {
		recorded, err := ledger.Record(ctx, workItemID)
		if recorded == nil {
			// Nothing was priced, so nothing was written; the failure, if there was
			// one, is the caller's to report.
			return nil, err
		}
		entry := recordedCost{WorkItemID: workItemID, Cost: *recorded}
		if err != nil {
			entry.Failure = err.Error()
		}
		return []recordedCost{entry}, nil
	}
	results, err := ledger.RecordAll(ctx)
	if err != nil {
		return nil, err
	}
	recorded := make([]recordedCost, 0, len(results))
	for _, result := range results {
		recorded = append(recorded, recordedCost{WorkItemID: result.WorkItemID, Cost: result.Cost, Failure: result.Failure})
	}
	return recorded, nil
}

// printPrices reports what was read. Asking about one item breaks its price down
// by the runs it took, because a total for one item invites the question of
// which attempt spent it; asking about everything reports a line per item, which
// is the ledger.
func printPrices(writer io.Writer, prices []runstate.ItemPrice, exchanges *runstate.ExchangeSpend, sides *runstate.SideStreamSpend, single bool) {
	if len(prices) == 0 && !recordedExchanges(exchanges) && !recordedSideStreams(sides) {
		fmt.Fprintln(writer, "the harness has no recorded runs, so there is nothing to price")
		return
	}
	if single {
		printPriceBreakdown(writer, prices[0])
		return
	}
	fmt.Fprintf(writer, ledgerRow, "item", "runs", "unpriced", "develop", "review", "repair", "cost", "cached", "waited")
	total := 0.0
	runs, unpriced := 0, 0
	var phases runstate.PhaseSpend
	var tokens runstate.TokenUsage
	for _, price := range prices {
		if !price.Recorded() {
			continue
		}
		printLedgerRow(writer, price.WorkItemID, len(price.Runs), price.UnknownRuns, price.TotalUSD, price.Phases, price.Tokens, false)
		total += price.TotalUSD
		runs += len(price.Runs)
		unpriced += price.UnknownRuns
		phases.Merge(price.Phases)
		tokens.Merge(price.Tokens)
	}
	// The asks are a row rather than a note under the table, because the total
	// below has to add up: what one role spent asking another is a provider
	// invocation the harness made, and a reader who can see it in the total but
	// not in a row above it is being asked to take the difference on trust.
	floor := false
	if recordedExchanges(exchanges) {
		printAskRow(writer, *exchanges)
		total += exchanges.CostUSD
		tokens.Merge(exchanges.Tokens)
		floor = !exchanges.Known()
	}
	// The side threads are a row for the same reason: money the harness spent,
	// belonging to a conversation rather than to an item.
	if recordedSideStreams(sides) {
		printSideRow(writer, *sides)
		total += sides.CostUSD
		floor = floor || !sides.Known()
	}
	// The rule above the total is the shape `yoyo status --spend` closes its
	// table with, and this ledger closes the same way on purpose: the operator
	// reads both and asked for one shape across them (yoyodyne-ifd.51).
	fmt.Fprintln(writer, ledgerRule)
	printLedgerRow(writer, "TOTAL", runs, unpriced, total, phases, tokens, floor)
	if unpriced > 0 {
		fmt.Fprintln(writer, "a run with no surviving record is counted as unpriced and left out of the total,")
		fmt.Fprintln(writer, "so every total it touches is a floor rather than a price")
	}
	printAskNote(writer, exchanges)
	printSideNote(writer, sides)
	printSplitNote(writer, phases)
	printCacheNote(writer, tokens)
	printPhaseCacheNote(writer, phases)
	fmt.Fprintln(writer, "this prices runs; conversation turns are recorded but not attributed to an item")
}

// recordedExchanges reports exchange spend worth a row: some to price, or a
// reason there is no figure for it.
func recordedExchanges(exchanges *runstate.ExchangeSpend) bool {
	return exchanges != nil && exchanges.Recorded()
}

// recordedSideStreams reports side thread spend worth a row: some to price, or
// a reason there is no figure for it.
func recordedSideStreams(sides *runstate.SideStreamSpend) bool {
	return sides != nil && sides.Recorded()
}

// sideLedgerLabel names the row the side threads land on, a summary line like
// the asks rather than an item.
const sideLedgerLabel = "SIDE THREADS"

// printSideRow writes what the side threads spent. The phase columns are "-"
// for the reason they are on the asks row: a side thread is not development,
// review, or repair. Its cached column is filled, because its log carries the
// provider's usage as a run's does.
func printSideRow(writer io.Writer, sides runstate.SideStreamSpend) {
	unreadable, cost := "-", "unknown"
	if sides.Enumerated() {
		unreadable = strconv.Itoa(sides.Unreadable)
		cost = renderTokenCost(sides.Tokens, sides.CostUSD, !sides.Known())
	}
	fmt.Fprintf(writer, ledgerRow, sideLedgerLabel, "-", unreadable, "-", "-", "-", cost, renderCacheShare(sides.Tokens), "")
}

// printSideNote says what the side thread row is made of and whose it was: the
// conversation each thread was opened beside, with what the threads beside it
// cost, so the row's money is attributed rather than merely counted.
func printSideNote(writer io.Writer, sides *runstate.SideStreamSpend) {
	if !recordedSideStreams(sides) {
		return
	}
	if !sides.Enumerated() {
		fmt.Fprintf(writer, "the side threads could not be listed (%s), so what they spent\n", sides.Unknown)
		fmt.Fprintln(writer, "is missing from the total entirely rather than counted as nothing")
		return
	}
	fmt.Fprintf(writer, "side threads is %d side conversation(s) over %d invocation(s), each opened beside a conversation:\n",
		sides.Streams, sides.Invocations)
	for _, part := range sides.Conversations {
		name := part.Conversation
		if name == "" {
			name = "(record unreadable, conversation unknown)"
		}
		fmt.Fprintf(writer, "  %s  %s from %d side conversation(s) over %d invocation(s)\n",
			name, renderFloor(part.CostUSD, false), part.Streams, part.Invocations)
	}
	if sides.Unreadable > 0 {
		fmt.Fprintf(writer, "%d side conversation log(s) could not be read and are left out of that figure (%s),\n",
			sides.Unreadable, sides.Unknown)
		fmt.Fprintln(writer, "so the side threads and the total are floors")
	}
}

// askLedgerLabel names the row the asks land on. It reads as a summary line
// rather than as an item because that is what it is: money the harness spent
// that belongs to no work item, sitting above the total it is part of.
const askLedgerLabel = "ASKS BETWEEN ROLES"

// printAskRow writes what the roles spent asking each other. The columns that
// split a run's price are left as "-" rather than filled with zeros: an ask is
// not development, review, or repair, and a zero under one of them would say it
// was one of the three and cost nothing.
//
// The unpriced column is the one an ask does have, and it holds exactly what it
// holds on an item's row: the records that could not be read, left out of the
// figure beside them and making it a floor. Records nothing could even
// enumerate leave no floor to state, so that row says unknown instead.
//
// The cached column is empty for a reason of its own: an exchange record carries
// what its rounds cost and no token counts at all, so there is no share to state
// rather than a share of nothing.
func printAskRow(writer io.Writer, exchanges runstate.ExchangeSpend) {
	unreadable, cost := "-", "unknown"
	if exchanges.Enumerated() {
		unreadable = strconv.Itoa(exchanges.Unreadable)
		cost = renderTokenCost(exchanges.Tokens, exchanges.CostUSD, !exchanges.Known())
	}
	fmt.Fprintf(writer, ledgerRow, askLedgerLabel, "-", unreadable, "-", "-", "-", cost, "-", "")
}

// printAskNote says what the ask row is made of, and what is missing from it.
// An exchange nobody can read is counted and said out loud rather than counted
// as nothing, which is the same discipline a run with no surviving log is held
// to — and, like that run, it never takes the records beside it down with it.
func printAskNote(writer io.Writer, exchanges *runstate.ExchangeSpend) {
	if !recordedExchanges(exchanges) {
		return
	}
	if !exchanges.Enumerated() {
		fmt.Fprintf(writer, "the recorded exchanges could not be listed (%s), so what the roles spent\n", exchanges.Unknown)
		fmt.Fprintln(writer, "asking each other is missing from the total entirely rather than counted as nothing")
		return
	}
	fmt.Fprintf(writer, "asks between roles is %d exchange(s) over %d round(s): a question one role put to another,\n",
		exchanges.Exchanges, exchanges.Rounds)
	fmt.Fprintln(writer, "recorded naming no work item to charge it to, and in the total because the harness spent it")
	if exchanges.Unreadable > 0 {
		fmt.Fprintf(writer, "%d exchange record(s) could not be read and are left out of that figure (%s),\n",
			exchanges.Unreadable, exchanges.Unknown)
		fmt.Fprintln(writer, "so the asks and the total are floors; every exchange beside them is priced as usual")
	}
}

// ledgerRow is the shape of every line of the ledger, header and total
// included, so the columns cannot drift apart between them.
const ledgerRow = "%-38s %6s %9s %12s %12s %12s %12s %7s %9s\n"

// ledgerRule closes the rows above the total, as wide as the row shape above
// it comes to.
var ledgerRule = strings.Repeat("-", 125)

// printLedgerRow writes one item's line. Each phase carries the same floor
// marker the total does when it is the runs that went unpriced, for the reason
// the total carries it: a column that read as exact because the count saying
// otherwise is elsewhere on the line is the mistake the marker exists to stop.
//
// The extra floor is for the total column alone, and is what an unread exchange
// makes of it. That money belongs to no phase — an ask is not development,
// review, or repair — so marking the phase columns for it would claim
// uncertainty in three figures that have none.
//
// The cached column carries no floor marker of any kind. It is a share rather
// than a sum, so an unpriced run does not leave it short: what a run nobody can
// read would have contributed is a share of its own, not tokens missing from
// this one. What is outside it is said under the table instead.
func printLedgerRow(writer io.Writer, label string, runs, unpriced int, total float64, phases runstate.PhaseSpend, tokens runstate.TokenUsage, floor bool) {
	fmt.Fprintf(writer, ledgerRow,
		label,
		strconv.Itoa(runs),
		strconv.Itoa(unpriced),
		renderTokenCost(phases.Development.Tokens, phases.Development.CostUSD, unpriced > 0),
		renderTokenCost(phases.Review.Tokens, phases.Review.CostUSD, unpriced > 0),
		renderTokenCost(phases.Repair.Tokens, phases.Repair.CostUSD, unpriced > 0),
		renderTokenCost(tokens, total, floor || unpriced > 0),
		renderCacheShare(tokens),
		renderWait(phases.Waits.Total()),
	)
}

// renderCacheShare is the cached column: how much of the input the provider
// served from its own cache. Usage nobody reported is a dash rather than a
// nought, because a run measured at nothing and a run nothing measured are the
// same figure and opposite facts, and the second is the one that would make a
// caching change look like it failed. A run the provider really did report as
// reading nothing keeps its nought, which is a reading and says so.
func renderCacheShare(tokens runstate.TokenUsage) string {
	if !tokens.Reported() {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", tokens.CacheReadShare()*100)
}

func printPriceBreakdown(writer io.Writer, price runstate.ItemPrice) {
	if !price.Recorded() {
		fmt.Fprintf(writer, "%s: the harness has no recorded run of it, so it has no price rather than a price of nothing\n", price.WorkItemID)
		return
	}
	fmt.Fprintf(writer, "%s: %s across %d run(s)\n", price.WorkItemID, renderTokenCost(price.Tokens, price.TotalUSD, price.UnknownRuns > 0), len(price.Runs))
	fmt.Fprintf(writer, "  %s\n", renderPhaseSplit(price.Phases, price.UnknownRuns))
	if tokens := renderTokenSplit(price.Tokens); tokens != "" {
		fmt.Fprintf(writer, "  %s\n", tokens)
	}
	if shares := renderPhaseCacheShares(price.Phases); shares != "" {
		fmt.Fprintf(writer, "  %s\n", shares)
	}
	for _, run := range price.Runs {
		fmt.Fprintf(writer, "  %s started %s [%s] %s\n", run.RunID, run.StartedAt.UTC().Format(time.RFC3339), renderRunOutcome(run), renderRunPrice(run))
		if run.Found != nil && run.Status.Terminal() {
			fmt.Fprintf(writer, "    %s\n", run.Found.Describe())
		}
		// A run nothing survives to price has no split to show, but it still
		// waited for as long as it waited: that came from the run's own record
		// rather than from the log that is gone.
		if run.Known() {
			fmt.Fprintf(writer, "    %s\n", renderPhaseSplit(run.Phases, 0))
			if tokens := renderTokenSplit(run.Tokens); tokens != "" {
				fmt.Fprintf(writer, "    %s\n", tokens)
			}
			if shares := renderPhaseCacheShares(run.Phases); shares != "" {
				fmt.Fprintf(writer, "    %s\n", shares)
			}
		} else if wait := renderWaits(run.Phases.Waits); wait != "" {
			fmt.Fprintf(writer, "    %s\n", wait)
		}
	}
	fmt.Fprintln(writer, "this prices runs; conversation turns are recorded but not attributed to an item")
}

// renderPhaseSplit says where a price went. Every phase is named even when it
// cost nothing, because a review that never happened and a review that was free
// read identically in a line that leaves the empty ones out, and telling those
// two apart is what splitting the price up is for.
//
// The unattributed money is the exception, and is named only when there is some.
// It is not a phase — it is invocations the record does not account for — so a
// zero beside the three would read as a fourth part of the work that happened to
// be free, which is the opposite of what it says.
func renderPhaseSplit(phases runstate.PhaseSpend, unpriced int) string {
	split := fmt.Sprintf("development %s from %d invocation(s), review %s from %d, repair %s from %d",
		renderTokenCost(phases.Development.Tokens, phases.Development.CostUSD, unpriced > 0), phases.Development.Invocations,
		renderTokenCost(phases.Review.Tokens, phases.Review.CostUSD, unpriced > 0), phases.Review.Invocations,
		renderTokenCost(phases.Repair.Tokens, phases.Repair.CostUSD, unpriced > 0), phases.Repair.Invocations)
	if phases.Unattributed.Invocations > 0 {
		split += fmt.Sprintf(", unattributed %s from %d",
			renderTokenCost(phases.Unattributed.Tokens, phases.Unattributed.CostUSD, unpriced > 0), phases.Unattributed.Invocations)
	}
	if waits := renderWaits(phases.Waits); waits != "" {
		split += "; " + waits
	}
	return split
}

// renderTokenSplit says how much of an input the provider served from its own
// cache, and out of how much. The three parts are spelled out beside the share
// for the reason the phase split spells out its three: a share on its own says a
// prompt is cached well and not whether that is because the prefix is shared or
// because the prompt is short.
//
// Invocations whose terminal carried no usage object are named rather than
// folded in. A share computed over a subset and a share computed over everything
// read identically, and telling them apart is what stops a measurement being
// taken against invocations nobody measured.
func renderTokenSplit(tokens runstate.TokenUsage) string {
	if !tokens.Reported() {
		if tokens.Unreported > 0 {
			return fmt.Sprintf("cache-read share unknown: %d priced invocation(s) reported no token usage", tokens.Unreported)
		}
		return ""
	}
	split := fmt.Sprintf("cache-read share %.1f%% of %d input token(s) over %d invocation(s): %d cached, %d fresh, %d written to the cache; %d output",
		tokens.CacheReadShare()*100, tokens.InputTotal(), tokens.Measured,
		tokens.CacheReadTokens, tokens.InputTokens, tokens.CacheCreationTokens, tokens.OutputTokens)
	if tokens.Unreported > 0 {
		split += fmt.Sprintf("; %d priced invocation(s) reported no usage and are outside it", tokens.Unreported)
	}
	return split
}

// printCacheNote says what the cached column is a share of and what is outside
// it. It is the instrument a change to what the harness sends is kept or
// reverted on -- a prompt reordered to share a longer prefix is worth keeping
// exactly insofar as this rises -- so it says what the figure means rather than
// leaving a percentage to be read for whatever the reader assumes.
//
// A record where nothing reported usage says so instead of printing a share of
// nothing. Every run recorded before the harness kept the provider's usage
// object is one of those, and a nought there would read as a caching change that
// achieved nothing rather than as a window that cannot answer.
func printCacheNote(writer io.Writer, tokens runstate.TokenUsage) {
	if !tokens.Reported() {
		if tokens.Unreported == 0 {
			return
		}
		fmt.Fprintf(writer, "%d priced invocation(s) reported no token usage and none reported any, so there is no\n", tokens.Unreported)
		fmt.Fprintln(writer, "cache-read share to report for this window rather than a share of nothing")
		return
	}
	fmt.Fprintf(writer, "cached is the cache-read share of input tokens: %d of %d input token(s) over %d priced invocation(s)\n",
		tokens.CacheReadTokens, tokens.InputTotal(), tokens.Measured)
	fmt.Fprintln(writer, "came from the provider's cache, the rest fresh or written to it, and a change made to share a")
	fmt.Fprintln(writer, "longer prompt prefix is kept or reverted on whether this rises")
	if tokens.Unreported > 0 {
		fmt.Fprintf(writer, "%d priced invocation(s) reported no token usage at all and are outside that share entirely,\n", tokens.Unreported)
		fmt.Fprintln(writer, "counted apart rather than added in as nothing")
	}
}

// printPhaseCacheNote says the same share once per phase, under the one above.
// It is a separate line because the aggregate cannot answer the question the
// phases can: a developer session re-reading its own conversation is tens of
// millions of cached tokens beside a review's tens of thousands, so a review
// that reads nothing at all leaves the column above at ninety-seven per cent and
// says so nowhere. That is not hypothetical -- it is how yoyodyne-ifd.84 came to
// be measured against a figure its own effect could not move, and this line is
// what a later prefix change is kept or reverted on instead.
func printPhaseCacheNote(writer io.Writer, phases runstate.PhaseSpend) {
	shares := renderPhaseCacheShares(phases)
	if shares == "" {
		return
	}
	fmt.Fprintf(writer, "%s;\n", shares)
	fmt.Fprintln(writer, "the phases neither assemble their prompts alike nor cache alike, so a change to what one of")
	fmt.Fprintln(writer, "them sends is read here rather than in the column above, which the largest of them decides")
}

// renderPhaseCacheShares says which part of the work read its prompt back from
// the provider's cache and which paid to write one. The three phases are named
// even where nothing measured them, for the reason the money split names a phase
// that cost nothing: a review that read none of its prefix and a review that
// never happened are opposite facts, and a line that omitted the second would
// read as the first. What separates them is the invocation count beside each
// share, which is why every phase carries one.
//
// A phase nothing measured is a dash rather than a nought, exactly as the ledger
// column is: a phase measured at nothing is a reading, and a phase nobody
// measured is the absence of one.
func renderPhaseCacheShares(phases runstate.PhaseSpend) string {
	type named struct {
		label string
		phase runstate.PhaseCost
	}
	phaseList := []named{
		{"development", phases.Development},
		{"review", phases.Review},
		{"repair", phases.Repair},
	}
	// The unattributed bucket is named only when there is something in it, for
	// the reason the money split names it only then: it is not a phase, and a
	// share beside the three would read as a fourth part of the work.
	if phases.Unattributed.Invocations > 0 {
		phaseList = append(phaseList, named{"unattributed", phases.Unattributed})
	}
	measured := false
	parts := make([]string, 0, len(phaseList))
	for _, entry := range phaseList {
		measured = measured || entry.phase.Tokens.Reported()
		parts = append(parts, fmt.Sprintf("%s %s over %d invocation(s)",
			entry.label, renderCacheShare(entry.phase.Tokens), entry.phase.Tokens.Measured))
	}
	// Nothing measured anywhere is the window that cannot answer, and the line
	// above it says so already; four dashes would be repeating that in a shape
	// that reads like a measurement.
	if !measured {
		return ""
	}
	return "cache-read share by phase: " + strings.Join(parts, ", ")
}

// printSplitNote says what the three phase columns come to as a whole. The
// split is said to be exhaustive rather than said to add up, because each column
// is rounded to the cent on its own and three of them can land a penny away from
// the total they came from. What matters is that nothing is missing from them,
// which is the claim the rounding cannot make false — and the one case where it
// is false, it is not made: an invocation that reached a run's log without saying
// which part of the run it served is money in the total and in none of the three,
// and the reader is told how much rather than left to take the difference.
//
// That case is a defect in whatever wrote the log rather than a cost of the
// work, and it is expected never to arise, which is why the line is absent
// instead of reading nought: a figure printed every time is one nobody reads on
// the day it is not nought.
func printSplitNote(writer io.Writer, phases runstate.PhaseSpend) {
	if phases.Unattributed.Invocations == 0 {
		fmt.Fprintln(writer, "develop, review and repair account for every priced run invocation; waited is time rather than money")
		return
	}
	fmt.Fprintln(writer, "waited is time rather than money")
	fmt.Fprintf(writer, "%d priced invocation(s) worth %s named no phase: they are in the total and in none of develop,\n",
		phases.Unattributed.Invocations, renderFloor(phases.Unattributed.CostUSD, false))
	fmt.Fprintln(writer, "review or repair, which is money the harness spent that nothing in the run record accounts for")
}

// renderWaits says how long work was held up and by what. The two waits are
// named apart because they are somebody's decision and nobody's respectively: a
// provider that would not serve the account is a thing to spend money on, and an
// operator's hold is a thing the operator already knows about.
func renderWaits(waits runstate.Waits) string {
	provider := time.Duration(waits.UsageLimitSeconds) * time.Second
	operator := time.Duration(waits.OperatorHoldSeconds) * time.Second
	switch {
	case provider > 0 && operator > 0:
		return fmt.Sprintf("waited %s (%s for the provider, %s on the operator's hold)",
			renderWait(waits.Total()), renderWait(provider), renderWait(operator))
	case provider > 0:
		return "waited " + renderWait(provider) + " for the provider"
	case operator > 0:
		return "waited " + renderWait(operator) + " on the operator's hold"
	}
	return ""
}

// renderWait puts a wait in the units somebody reads it in. A run that waited
// four hours and a run that waited four seconds are different problems, and
// neither is helped by six digits of precision.
func renderWait(waited time.Duration) string {
	waited = waited.Round(time.Second)
	if waited <= 0 {
		return ""
	}
	switch {
	case waited >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(waited/time.Hour), int(waited%time.Hour/time.Minute))
	case waited >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(waited/time.Minute), int(waited%time.Minute/time.Second))
	default:
		return fmt.Sprintf("%ds", int(waited/time.Second))
	}
}

// printRecordedPrices says what reached the tracker. A price that could not be
// written is named rather than swallowed: the ledger is only worth reading if it
// says which items it failed to reach.
func printRecordedPrices(stdout, stderr io.Writer, recorded []recordedCost) {
	if len(recorded) == 0 {
		return
	}
	written := 0
	for _, entry := range recorded {
		if entry.Failure == "" {
			written++
			continue
		}
		fmt.Fprintf(stderr, "%s was priced but not recorded: %s\n", entry.WorkItemID, entry.Failure)
	}
	fmt.Fprintf(stdout, "recorded the price of %d of %d work item(s) on the tracker\n", written, len(recorded))
}

// renderTotal marks a figure the unpriced runs behind it make a floor rather
// than a price. Every surface that shows money uses it, including each row of
// the ledger: a row whose own runs went unpriced must not read as exact merely
// because the count that says so is in the next column.
func renderTotal(total float64, unpriced int) string {
	return renderFloor(total, unpriced > 0)
}

// renderFloor is the same marker where what makes a figure a lower bound is
// something other than an unpriced run — exchanges that could not be read leave
// a total short with no run count to say so.
func renderFloor(total float64, floor bool) string {
	if floor {
		return fmt.Sprintf("≥ $%.2f", total)
	}
	return fmt.Sprintf("$%.2f", total)
}

// renderRunOutcome says how one priced attempt went, in the same fixed
// vocabulary the run listing uses. It reads the outcome the record derives
// rather than the durable status, because a reader meeting the same run in both
// places must not be told two different things about it.
func renderRunOutcome(run runstate.RunPrice) string {
	outcome := string(run.Outcome)
	if run.Phase != "" {
		outcome += ", " + string(run.Phase)
	}
	if run.Integrated {
		outcome += ", integrated"
	}
	if run.Status.Terminal() && run.Remains != "" {
		outcome += ", " + run.Remains
	}
	return outcome
}

// renderRunPrice says what one attempt cost. A run that is still going reports
// what it has spent so far rather than a figure that reads as final.
func renderRunPrice(run runstate.RunPrice) string {
	if !run.Known() {
		return "unknown: " + run.Unknown
	}
	if !run.Status.Terminal() {
		return fmt.Sprintf("%s so far from %d invocation(s)", run.Tokens.CostText(run.CostUSD), run.Invocations)
	}
	return fmt.Sprintf("%s from %d invocation(s)", run.Tokens.CostText(run.CostUSD), run.Invocations)
}

func reportCostFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, costOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "cost failed: %v\n", err)
	return 1
}

func printCostUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo cost [options] [<beads-id>]

Prices work items from the runs the harness recorded for them: every run made
for an item, the failed attempts and the reviewer's invocations included, at the
cost the provider itself reported. Every price is split by where it went --
making the change, reviewing it, and repairing it -- with the time the work
spent waiting on a provider or on the operator beside it. Naming an item breaks
its price down by run.

Beside the money is what the provider was billed tokens for: the cache-read
share of input tokens, per run and over everything, which is the measure a
change made to share a longer prompt prefix is kept or reverted on. An
invocation whose terminal reported no usage is counted apart rather than added
in as nothing.

What the roles spent asking each other is a row of its own above the total. The
exchange record names no piece of work to charge it to, so it reaches no item's
price; it is a provider invocation the harness made, so a total without it would
be wrong rather than unattributed. What the agents' side conversations spent is
a row beside it for the same reason, with each side conversation's cost named
under the table against the conversation it was opened beside.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --record          write each price onto its work item in the tracker
  --json            emit machine-readable JSON`)
}

func renderTokenCost(tokens runstate.TokenUsage, cost float64, floor bool) string {
	if tokens.NoCost == 0 {
		return renderFloor(cost, floor)
	}
	return tokens.CostText(cost)
}
