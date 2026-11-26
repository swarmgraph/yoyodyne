// Package epiccost says what a named epic has cost and whether its budget stops
// its next new start: docs/designs/epic-mode-second-part.md, piece 12.
//
// An epic's cost is the provider reported cost of every run whose start was
// reserved for that naming of it (epicprogress.Entry.RunsReserved). A run's price
// already holds its developer, its reviewer and each repair inside it, so a
// member's runs and reviews are counted by counting its runs. The reservation is
// kept when an item is taken out, so what such an item cost stays counted, and a
// run reserved for two namings, a document answering two epics, is shared
// equally between them, so no figure is counted twice.
//
// A joining judgment and a check of the whole epic are paid calls reserved for the
// epic, each priced from the provider reported cost of the calls recorded under
// its identifier. A run of the project's declared checks for a check of the whole
// epic is charged to the epic too, and is counted, because it calls no provider.
//
// Nothing is counted as costing nothing because its cost is unknown: a run or a
// call that cannot be priced, in whole or in part, is counted beside the figure.
// Those are never added to the figure and never count toward the limit.
package epiccost

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// NoticeShare is the share of the limit at which status says the epic is near it.
const NoticeShare = 0.8

// Prices prices one recorded run. It is satisfied by *runstate.Store.
type Prices interface {
	PriceRun(runID string) (runstate.RunPrice, error)
}

// CallPrices prices one paid call that is not a run, from the cost lines recorded
// under its identifier: the provider reported cost, and whether every line had
// one. Prices that also satisfy it price the calls; others leave them unpriced.
type CallPrices interface {
	PriceCall(id string) (reportedUSD float64, known bool, err error)
}

// SpendPrices prices runs from their records and calls from the cost log.
type SpendPrices struct {
	*runstate.Store
	Lines []runstate.Spend
}

// PriceCall is known where at least one line is recorded under id and every one
// carries a provider reported cost.
func (p SpendPrices) PriceCall(id string) (float64, bool, error) {
	kinds := runstate.KindsOf(p.Lines, func(line runstate.Spend) bool { return line.BranchReviewID == id })
	return kinds.ReportedUSD, kinds.Calls > 0 && kinds.ReportedCalls == kinds.Calls, nil
}

// Spend is what one naming of an epic has cost. KnownUSD and the counts beside it
// are different kinds of cost and are never added together.
type Spend struct {
	// KnownUSD is the provider reported cost of the runs that could be priced,
	// with a shared run's share, in US dollars.
	KnownUSD float64 `json:"known_usd"`
	// Runs is how many runs were made for it.
	Runs int `json:"runs"`
	// UnknownRuns is how many of them could not be priced from all their evidence.
	// One priced from part of it adds that part to KnownUSD and is counted here too.
	UnknownRuns int `json:"unknown_runs,omitempty"`
	// Calls is how many other paid calls, joining judgments and checks of the
	// whole epic, were reserved for it, and UnpricedCalls how many of them could
	// not be priced in whole. One priced in part adds that part to KnownUSD.
	Calls         int `json:"calls,omitempty"`
	UnpricedCalls int `json:"unpriced_calls,omitempty"`
	// CheckRuns is how many runs of the project's declared checks were made for
	// its checks of the whole epic. They call no provider.
	CheckRuns int `json:"check_runs,omitempty"`
}

// Of is what a naming has cost, from what is kept of its progress.
func Of(view epicprogress.View, epic epicscope.Epic, prices Prices) Spend {
	entry, _ := view.Entry(epicprogress.Of(epic))
	var spend Spend
	for _, runID := range entry.RunsReserved() {
		spend.Runs++
		price, err := prices.PriceRun(runID)
		if err != nil || !price.Known() {
			spend.UnknownRuns++
			continue
		}
		if !price.Complete() {
			spend.UnknownRuns++
		}
		spend.KnownUSD += price.CostUSD / float64(max(1, view.RunSharedBy(runID)))
	}
	calls, priced := prices.(CallPrices)
	for _, call := range entry.CallsReserved() {
		spend.Calls++
		if !priced {
			spend.UnpricedCalls++
			continue
		}
		usd, known, err := calls.PriceCall(call.ID)
		if err != nil || !known {
			spend.UnpricedCalls++
		}
		if err == nil {
			spend.KnownUSD += usd
		}
	}
	spend.CheckRuns = len(entry.ChecksRunsReserved())
	return spend
}

// Standing is an epic's cost beside its limit, where one is set.
type Standing struct {
	Spend
	Budget *epicscope.Budget `json:"budget,omitempty"`
}

// Read is what an open or ended naming has cost and its limit, read from the run
// records under the state root.
func Read(stateRoot string, product domain.ProductID, view epicprogress.View, epic epicscope.Epic) (Standing, error) {
	store, err := runstate.NewStore(stateRoot, product)
	if err != nil {
		return Standing{}, fmt.Errorf("open the run records: %w", err)
	}
	spends, err := runstate.NewSpendStore(stateRoot, product)
	if err != nil {
		return Standing{}, fmt.Errorf("open the cost log: %w", err)
	}
	lines, err := spends.List()
	if err != nil {
		return Standing{}, fmt.Errorf("read the cost log: %w", err)
	}
	return Standing{Spend: Of(view, epic, SpendPrices{store, lines}), Budget: epic.Budget}, nil
}

// Refuses reports a limit the known cost has reached, so that the epic's next
// new start is refused. Work already started is not asked about it.
func (s Standing) Refuses() bool {
	return s.Budget != nil && s.KnownUSD >= s.Budget.LimitUSD
}

// Near reports a known cost at four fifths of the limit or more, below it.
func (s Standing) Near() bool {
	return s.Budget != nil && !s.Refuses() && s.KnownUSD >= NoticeShare*s.Budget.LimitUSD
}

// Above is by how much the known cost is over the limit, and 0 where it is not.
func (s Standing) Above() float64 {
	if s.Budget == nil || s.KnownUSD <= s.Budget.LimitUSD {
		return 0
	}
	return s.KnownUSD - s.Budget.LimitUSD
}

// Why is why the next new start is refused, in words, and empty where it is not.
func (s Standing) Why() string {
	if !s.Refuses() {
		return ""
	}
	return fmt.Sprintf("spent %s of its limit of %s in provider reported cost", Dollars(s.KnownUSD), Dollars(s.Budget.LimitUSD))
}

// Says is the cost of an epic in one line: the figure, the limit and where it
// stands, what is not in the figure, and the command that sets the limit.
func (s Standing) Says(epic string) string {
	said := fmt.Sprintf("Cost: %s in provider reported cost, over %s", Dollars(s.KnownUSD), count(s.Runs, "run"))
	switch {
	case s.Budget == nil:
		said += "; no budget is set"
	case s.Above() > 0:
		said += fmt.Sprintf(", which is %s above its limit of %s; nothing new starts in it, and what is under way finishes", Dollars(s.Above()), Dollars(s.Budget.LimitUSD))
	case s.Refuses():
		said += fmt.Sprintf(", which has reached its limit of %s; nothing new starts in it, and what is under way finishes", Dollars(s.Budget.LimitUSD))
	case s.Near():
		said += fmt.Sprintf(", which is four fifths or more of its limit of %s", Dollars(s.Budget.LimitUSD))
	default:
		said += fmt.Sprintf(", of a limit of %s", Dollars(s.Budget.LimitUSD))
	}
	var unknown []string
	if s.UnknownRuns > 0 {
		unknown = append(unknown, count(s.UnknownRuns, "run")+" whose cost is unknown in whole or in part")
	}
	if s.UnpricedCalls > 0 {
		unknown = append(unknown, count(s.UnpricedCalls, "other paid call")+" whose cost is unknown in whole or in part")
	}
	if len(unknown) > 0 {
		said += ". Not in that figure, and not counted toward a limit: " + strings.Join(unknown, ", and ")
	}
	if s.CheckRuns > 0 {
		said += fmt.Sprintf(". It ran the project's checks %s for the check of the whole epic, which calls no provider", times(s.CheckRuns))
	}
	verb := "change"
	if s.Budget == nil {
		verb = "set"
	}
	return said + fmt.Sprintf(". To %s the limit: yoyo epic budget %s <dollars>", verb, epic)
}

// Dollars is an amount of US dollars as status says it.
func Dollars(amount float64) string { return fmt.Sprintf("$%.2f", amount) }

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}
