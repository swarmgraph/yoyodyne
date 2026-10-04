package cli

// What shipped lately and what it took: the shipped ledger, `yoyo status
// --shipped`.
//
// The operator asked for it as `yoyo status` — "a list of the n most recently
// shipped beads, their cost, and wall clock time to implement" — and the product
// manager settled where it lives on yoyodyne-ifd.51, in two steps. First the
// ledger was to extend `yoyo cost`, because ifd.41 built the per-item join in
// Go with its discipline settled — provider-reported only, every run counted
// including the failed and repair attempts, unknown stated as unknown rather
// than zero — and the ledger is that same join sorted by promotion time; a
// second implementation of item aggregation would put one load-bearing number
// in two places that can drift. Then bin/yoyo-status folded into this verb
// (yoyodyne-ifd.63), the naming collision that had argued against the spelling
// `yoyo status` went with it, and the decision moved the ledger back to the
// operator's original ask: a section of the consolidated verb. What stood
// through both is what this file does: the join is runstate's and exists once
// (runstate.Store.Shipped reads it through runstate.Store.price), and the
// rendering is the shape `--spend` established — a header, a row a line, a
// rule, a TOTAL row, and what the figures mean under it — which the operator
// preferred over `yoyo cost`'s and asked both Go outputs to converge on.
//
// It is a mode rather than lines under the four, because the four lines and the
// run history answer "what is happening" and this answers "what got done", and
// a screen that put both in front of an operator who asked one of them would
// bury the answer under the other. The conversation's /status is untouched: it
// covers in-flight work, and this covers finished work.
//
// The wall clock is two numbers. Elapsed is first claim to promotion, and it
// includes every hour the item spent parked; paused is those hours on their
// own. They are beside each other rather than netted because a four-hour item
// that spent three of them waiting on a provider's usage window is slow in a
// different way from one that spent four working, and an undifferentiated
// figure would report waiting as slowness — which is the misreading the
// product manager held the requirement firm against, since concurrent runs
// (yoyodyne-ifd.49) pause on shared windows.

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// defaultShippedItems is how many shipped items are listed when nobody says.
// The question is what shipped lately; ten is a glance, and --limit or the
// argument reaches further back.
const defaultShippedItems = 10

type shippedOutput struct {
	Ledger runstate.ShippedLedger `json:"shipped"`
	Error  string                 `json:"error,omitempty"`
}

// shippedCount reads how many items were asked for, from the one argument the
// mode takes or from --limit — the argument for the reason `--spend 30` takes
// one, and --limit because this is a listing and every listing of the verb is
// bounded by it. Both given is refused rather than resolved by a precedence: an
// operator who typed two counts meant one of them. Zero reports every shipped
// item, as --limit 0 does everywhere else.
func shippedCount(named string, limit int, limitGiven bool) (int, error) {
	if named == "" {
		if limitGiven {
			return limit, nil
		}
		return defaultShippedItems, nil
	}
	count, err := strconv.Atoi(named)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("%q is not a count of shipped items to list; 0 lists every one", named)
	}
	if limitGiven && limit != count {
		return 0, fmt.Errorf("--limit %d and %d are two counts; give one of them", limit, count)
	}
	return count, nil
}

// reportShipped lists the most recently shipped items. It reads the same
// product-scoped run records the recorded mode reads, through the same
// resolution, so the two cannot disagree about which machine's work is being
// reported.
func reportShipped(configPath string, count int, jsonOutput bool, stdout, stderr io.Writer) int {
	store, _, err := recordedRunStore(configPath)
	if err != nil {
		return reportShippedFailure(stdout, stderr, jsonOutput, err)
	}
	ledger, err := store.Shipped(count)
	if err != nil {
		return reportShippedFailure(stdout, stderr, jsonOutput, err)
	}
	if jsonOutput {
		return writeJSON(stdout, stderr, shippedOutput{Ledger: ledger})
	}
	printShipped(stdout, ledger, reportTitles(configPath))
	return 0
}

// The columns: when it shipped, what it cost across every run, the wall clock
// from first claim to promotion, how much of that was spent parked, how many
// runs it took, and the complete item name last because it is the column with
// no width of its own. One shape for the header, every row, and the total, so
// the columns cannot drift apart between them; the money column is wide enough
// for a floor marker.
const shippedRow = "%-17s %11s %9s %9s %5s  %s\n"

var shippedRule = strings.Repeat("-", 82)

// printShipped renders the ledger in the shape the spend report established:
// header, a row a line, a rule, a TOTAL row, then what the figures mean.
func printShipped(writer io.Writer, ledger runstate.ShippedLedger, names ...*readmodel.WorkItemTitles) {
	if ledger.Shipped == 0 {
		fmt.Fprintln(writer, "the harness has no recorded run that promoted its work, so nothing has shipped")
		return
	}
	fmt.Fprintf(writer, shippedRow, "shipped", "cost", "elapsed", "paused", "runs", "item")
	var (
		total          float64
		elapsed        time.Duration
		paused         time.Duration
		runs, unpriced int
		elapsedUnknown int
	)
	for _, item := range ledger.Items {
		var titles *readmodel.WorkItemTitles
		if len(names) > 0 {
			titles = names[0]
		}
		fmt.Fprintf(writer, shippedRow,
			renderSpendMoment(item.ShippedAt),
			renderTotal(item.Price.TotalUSD, item.Price.UnknownRuns),
			renderElapsed(item),
			renderPaused(item.Paused()),
			strconv.Itoa(len(item.Price.Runs)),
			titles.Name(item.WorkItemID),
		)
		total += item.Price.TotalUSD
		unpriced += item.Price.UnknownRuns
		runs += len(item.Price.Runs)
		paused += item.Paused()
		if took, known := item.Elapsed(); known {
			elapsed += took
		} else {
			elapsedUnknown++
		}
	}
	fmt.Fprintln(writer, shippedRule)
	// The total's elapsed is a floor once any item's is unknown, marked the way
	// every other floor in this project is marked: a sum over nine of ten items
	// that read as the sum over ten would be exactly the understatement the
	// unknown exists to prevent.
	fmt.Fprintln(writer, strings.TrimRight(fmt.Sprintf(shippedRow,
		fmt.Sprintf("TOTAL (%d of %d)", len(ledger.Items), ledger.Shipped),
		renderTotal(total, unpriced),
		renderFloorDuration(elapsed, elapsedUnknown > 0),
		renderPaused(paused),
		strconv.Itoa(runs),
		"",
	), " \n"))
	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "%d of %d shipped item(s) listed, most recent promotion first; a number after --shipped lists that many, and 0 lists every one\n",
		len(ledger.Items), ledger.Shipped)
	fmt.Fprintln(writer, "cost is what the provider reported across every run made for the item, the failed and repair attempts included")
	fmt.Fprintln(writer, "elapsed is wall clock from the first claim to the promotion and includes the paused time beside it;")
	fmt.Fprintln(writer, "paused is what the item spent parked on a provider usage limit or the operator's hold, summed across its runs")
	if unpriced > 0 {
		fmt.Fprintf(writer, "%d run(s) have no surviving record to price and are left out of the cost, so every cost marked ≥ is a floor rather than a price\n", unpriced)
	}
	if elapsedUnknown > 0 {
		fmt.Fprintf(writer, "%d item(s) say elapsed is unknown: their promoting run has not recorded completing, and the total elapsed is a floor without them\n", elapsedUnknown)
	}
}

// renderElapsed is the elapsed column: the wall clock, or the word unknown
// where the record cannot say. The word rather than a dash, because a dash in a
// column of durations reads as none, and no time at all is the one thing an
// item that shipped certainly did not take.
func renderElapsed(item runstate.ShippedItem) string {
	took, known := item.Elapsed()
	if !known {
		return "unknown"
	}
	return renderDuration(took)
}

// renderPaused is the paused column. Nothing parked says so in a word rather
// than leaving the cell blank, as the ledger's waited column does: this column
// is read across a row with the elapsed beside it, and a blank there reads as a
// figure the row forgot.
func renderPaused(waited time.Duration) string {
	if waited <= 0 {
		return "none"
	}
	return renderDuration(waited)
}

// renderDuration is a wall-clock figure in the units somebody reads one in,
// including nought — which renderWait leaves blank, because a wait of nothing
// is the ordinary case there and a duration of nothing is a figure here — and
// including days, which a wait never reaches and an elapsed time does: an item
// first claimed one week and shipped the next is a fortnight's number, and
// "457h13m" is one a reader has to divide.
func renderDuration(took time.Duration) string {
	took = took.Round(time.Second)
	if took >= 24*time.Hour {
		return fmt.Sprintf("%dd%02dh", int(took/(24*time.Hour)), int(took%(24*time.Hour)/time.Hour))
	}
	if rendered := renderWait(took); rendered != "" {
		return rendered
	}
	return "0s"
}

// renderFloorDuration marks a summed duration some of whose parts are unknown,
// the way renderFloor marks money.
func renderFloorDuration(took time.Duration, floor bool) string {
	if floor {
		return "≥ " + renderDuration(took)
	}
	return renderDuration(took)
}

func reportShippedFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, shippedOutput{Ledger: runstate.ShippedLedger{Items: []runstate.ShippedItem{}}, Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "status failed: %v\n", err)
	return 1
}
