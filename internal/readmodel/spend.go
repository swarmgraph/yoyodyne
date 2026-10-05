package readmodel

// What the harness is spending, and what it has spent each day for a month.
//
// The throughput beside this answers what got done; this answers the question
// the operator opens the page for, which is what the machine is costing him
// right now. The two windows are the ones he asks in: the last twenty-four
// hours, reckoned from this moment rather than from midnight — a figure taken
// at ten past midnight is otherwise a figure about ten minutes — and the last
// seven local days, which is the week the spend report already groups by and
// the sum of the seven most recent lines of the month below it.
//
// It derives nothing of its own about money. Every figure is
// (*runstate.StreamStore).Spend's, summed by runstate.SpendTotals — the one
// summation `yoyo status --spend` prints its own total and by-kind split from —
// so a figure here is a figure the terminal prints and the two cannot disagree
// about what the last week cost. The kinds are the spend report's own, in the
// order it prices them: runs, conversations, branch reviews, side threads, and
// exchanges.
//
// Three things are said rather than left to be inferred from a zero. A cost is
// a floor wherever an exchange record that should be in it could not be read,
// and the count of those rides beside the figure. Spend whose moment could not
// be read belongs to no day, so it is counted in every window and stated on its
// own rather than quietly making the days disagree with the windows. And a day
// no priced record goes back to is said to be out of reach, because a day
// nobody measured and a day nothing was spent on are different answers and only
// one of them is zero.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Ledger is the recorded spend as `yoyo status --spend` prices it: every run,
// conversation, branch review, side thread, and exchange, by the local day the money was
// spent on and from one moment for the rolling window. It is satisfied by
// *runstate.StreamStore.
type Ledger interface {
	Spend(runstate.SpendQuery) (runstate.SpendReport, error)
}

const (
	// rollingSpendWindow is the short window, reckoned from the moment the
	// reading was taken rather than from a day boundary.
	rollingSpendWindow = 24 * time.Hour
	// weeklySpendDays is the long window, in local days with today the first of
	// them, so it is exactly the sum of the seven newest lines of the listing.
	weeklySpendDays = 7
	// listedSpendDays is how many local days the listing behind the box covers.
	listedSpendDays = 30
)

// SpendSources are the records one spend reading is assembled from. The ledger
// is an interface so the derivation can be exercised without a state directory,
// which is the only way a figure nobody may recompute per surface gets a
// fixture that holds it.
type SpendSources struct {
	// Ledger is the spend, and LedgerProblem the reason it could not be opened
	// where the caller has one. A reading without one says so rather than
	// reporting nothing spent.
	Ledger        Ledger
	LedgerProblem string
	// Now stamps the reading and anchors both windows. It defaults to the wall
	// clock and is injected so a test can pin a moment.
	Now func() time.Time
}

func (s SpendSources) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// SpendWindow is what one window cost, labeled with what it covers.
type SpendWindow struct {
	// Label is the window in words, as the page and the terminal both name it.
	Label string `json:"label"`
	// Rolling says the window is reckoned from Since rather than from the first
	// midnight of Days local days, which is what makes "the last 24 hours" a
	// different question from "today".
	Rolling bool `json:"rolling"`
	// Since is the first instant the window covers, and SinceDay the first local
	// day it covers where it is a window of whole days. Days is how many, today
	// counting as the first, and is zero on a rolling window.
	Since    time.Time `json:"since"`
	SinceDay string    `json:"since_day,omitempty"`
	Days     int       `json:"days,omitempty"`

	// CostUSD is what every priced invocation in the window cost, across runs,
	// conversations, branch reviews, side threads, and exchanges alike, and
	// Invocations how many there were. Kinds splits both by what was invoked.
	Tokens      runstate.TokenUsage `json:"tokens"`
	CostText    string              `json:"cost_text"`
	CostUSD     float64             `json:"cost_usd"`
	Invocations int                 `json:"invocations"`
	Kinds       []KindSpend         `json:"kinds"`
	// Unpriced counts the records the window should cover and could not read,
	// and Floor says in one word that the cost is therefore a lower bound, so a
	// surface does not have to know why.
	Unpriced int  `json:"unpriced"`
	Floor    bool `json:"floor"`
}

// SpendDay is one local day of the listing: what that day cost and on what.
type SpendDay struct {
	Day string `json:"day"`
	// Reached says a priced record goes back at least this far. A day none does
	// carries no figures at all rather than zeroes, because nothing measured it.
	Reached     bool                `json:"reached"`
	Tokens      runstate.TokenUsage `json:"tokens"`
	CostText    string              `json:"cost_text"`
	CostUSD     float64             `json:"cost_usd"`
	Invocations int                 `json:"invocations"`
	Kinds       []KindSpend         `json:"kinds"`
}

// KindSpend is one kind's share of a window's or a day's cost.
type KindSpend struct {
	Kind        runstate.StreamKind `json:"kind"`
	Invocations int                 `json:"invocations"`
	Tokens      runstate.TokenUsage `json:"tokens"`
	CostText    string              `json:"cost_text"`
	CostUSD     float64             `json:"cost_usd"`
}

// Spend is the reading: the two windows the box shows, the month of local days
// the listing behind it shows, and what could not be read.
type Spend struct {
	ObservedAt time.Time     `json:"observed_at"`
	Windows    []SpendWindow `json:"windows"`
	// Days is the listing, newest first, and is absent rather than empty where
	// the spend could not be read at all.
	Days []SpendDay `json:"days"`
	// Reaches is the earliest local day a priced record falls on, and is empty
	// where nothing dated was priced.
	Reaches string `json:"reaches,omitempty"`
	// Undated is the spend whose moment could not be read. It is counted in
	// every window above and on no day of the listing, so it is stated on its
	// own rather than being the reason the two do not add up.
	Undated SpendDay `json:"undated"`
	// Unpriced counts the exchange records that could not be read, and Floor
	// says that every figure in this reading is therefore a lower bound.
	Unpriced int  `json:"unpriced"`
	Floor    bool `json:"floor"`
	// Problem is set where the spend could not be read; every figure is then
	// nothing rather than zero, and this says why.
	Problem string `json:"problem,omitempty"`
}

// ReadSpend assembles the windows and the month from the recorded spend. One
// read of the logs answers all three: pricing a stream reads the whole of its
// log whatever window is asked for, so the query names the month it covers and
// the moment the rolling window is reckoned from together, and the report comes
// back carrying both.
func ReadSpend(ctx context.Context, sources SpendSources) Spend {
	now := sources.now()
	reading := Spend{ObservedAt: now, Windows: spendWindows(now)}

	var report runstate.SpendReport
	switch {
	case ctx.Err() != nil:
		reading.Problem = fmt.Sprintf("the spend was not read: %v", ctx.Err())
	case sources.Ledger == nil:
		reading.Problem = absent("the spend", sources.LedgerProblem)
	default:
		priced, err := sources.Ledger.Spend(runstate.SpendQuery{
			Days:  listedSpendDays,
			Since: now.Add(-rollingSpendWindow),
			Now:   now,
		})
		if err != nil {
			reading.Problem = fmt.Sprintf("the spend could not be read: %v", err)
		} else {
			report = priced
		}
	}
	if reading.Problem != "" {
		return reading
	}

	reading.Reaches = report.Reaches
	reading.Unpriced = len(report.UnreadableExchanges)
	reading.Floor = report.Floor()
	for index := range reading.Windows {
		window := &reading.Windows[index]
		if window.Rolling {
			// The moment the report says it reckoned from, rather than the one
			// this asked for, so the label on the page describes what was
			// measured.
			if !report.RollingSince.IsZero() {
				window.Since = report.RollingSince
			}
			sumInto(window, report.RollingWindow())
			continue
		}
		sumInto(window, report.Since(window.SinceDay))
	}

	reading.Days = make([]SpendDay, 0, listedSpendDays)
	for back := 0; back < listedSpendDays; back++ {
		day := runstate.LocalDay(startOfLocalDay(now, back+1))
		listed := SpendDay{Day: day, Reached: report.Reaches != "" && day >= report.Reaches, Kinds: []KindSpend{}}
		if listed.Reached {
			listed.CostUSD, listed.Invocations, listed.Kinds, listed.Tokens, listed.CostText = onlyDay(report, day)
		}
		reading.Days = append(reading.Days, listed)
	}
	reading.Undated = SpendDay{Day: runstate.UndatedDay, Reached: true, Kinds: []KindSpend{}}
	reading.Undated.CostUSD, reading.Undated.Invocations, reading.Undated.Kinds, reading.Undated.Tokens, reading.Undated.CostText = onlyDay(report, runstate.UndatedDay)
	return reading
}

// spendWindows are the two windows every reading covers, with what each covers
// stated on it: the rolling day, and the week of local days.
func spendWindows(now time.Time) []SpendWindow {
	return []SpendWindow{
		{Label: "last 24 hours", Rolling: true, Since: now.Add(-rollingSpendWindow), Kinds: []KindSpend{}},
		{
			Label:    "last 7 days",
			Since:    startOfLocalDay(now, weeklySpendDays),
			SinceDay: firstLocalDay(now, weeklySpendDays),
			Days:     weeklySpendDays,
			Kinds:    []KindSpend{},
		},
	}
}

// sumInto takes one window's figures off the report narrowed to it, by the
// report's own summation.
func sumInto(window *SpendWindow, narrowed runstate.SpendReport) {
	totals := narrowed.Totals()
	window.CostUSD = totals.CostUSD
	window.Tokens = totals.Usage
	window.CostText = totals.Usage.CostText(totals.CostUSD)
	window.Invocations = totals.Calls
	window.Kinds = kindShares(totals)
	window.Unpriced = len(narrowed.UnreadableExchanges)
	window.Floor = narrowed.Floor()
}

// onlyDay is what one local day cost, added up by the same summation the
// windows are: the report's rows are already one per stream per day, so a day
// is the rows carrying it and nothing else.
func onlyDay(report runstate.SpendReport, day string) (float64, int, []KindSpend, runstate.TokenUsage, string) {
	only := report
	only.Rows = nil
	for _, row := range report.Rows {
		if row.Day == day {
			only.Rows = append(only.Rows, row)
		}
	}
	totals := only.Totals()
	return totals.CostUSD, totals.Calls, kindShares(totals), totals.Usage, totals.Usage.CostText(totals.CostUSD)
}

func kindShares(totals runstate.SpendTotals) []KindSpend {
	shares := make([]KindSpend, 0, len(totals.ByKind))
	for _, share := range totals.ByKind {
		part := KindSpend{Kind: share.Kind, Invocations: share.Calls, CostUSD: share.CostUSD}
		if share.Usage.NoCost > 0 {
			part.Tokens = share.Usage
			part.CostText = share.Usage.CostText(share.CostUSD)
		}
		shares = append(shares, part)
	}
	return shares
}
