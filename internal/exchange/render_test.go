package exchange

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// costedThread is an exchange whose rounds are as given, for reading how it
// renders.
func costedThread(rounds ...Round) Exchange {
	opened := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	for index := range rounds {
		rounds[index].Number = index + 1
		rounds[index].Question = "and then?"
		rounds[index].Answer = "this much"
		rounds[index].AskedAt = opened
	}
	return Exchange{
		ID:        "exchange-" + strings.Repeat("d", 32),
		Asker:     Party{Role: domain.RoleProductManager},
		Answerer:  Party{Role: domain.RoleArchitect},
		MaxRounds: 5,
		OpenedAt:  opened,
		Rounds:    rounds,
	}
}

func unpricedRound(usage string) Round {
	reported := false
	round := Round{CostReported: &reported}
	if usage != "" {
		round.Usage = json.RawMessage(usage)
	}
	return round
}

// A round whose provider reported no dollar cost is shown by its tokens with no
// cost reported, in the summary line and in the thread, never as $0.0000.
func TestAnUnpricedExchangeRendersItsTokensAndNoCostReported(t *testing.T) {
	t.Parallel()
	thread := costedThread(unpricedRound(`{"input_tokens":3120,"cache_read_input_tokens":13184,"output_tokens":5}`))
	want := "16304 input tokens (13184 cached), 5 output tokens; no cost reported"
	if summary := thread.Summary(); !strings.HasSuffix(summary, want) {
		t.Fatalf("Summary() = %q, want it to end %q", summary, want)
	}
	rendered := thread.RenderThread()
	if !strings.Contains(rendered, "round 1 of 5 ("+want+")") || strings.Contains(rendered, "$0.0000") {
		t.Fatalf("RenderThread() = %q, want the round shown by its tokens", rendered)
	}
	// A round whose stream carried no usage says so rather than counting nothing.
	bare := costedThread(unpricedRound(""))
	if summary := bare.Summary(); !strings.HasSuffix(summary, "token usage not reported; no cost reported") {
		t.Fatalf("Summary() = %q, want usage named as not reported", summary)
	}
}

// An exchange mixing priced and unpriced rounds gives the dollars that were
// reported and says how many rounds carry no cost, and each round of the thread
// reads as its own provider reported it.
func TestAMixedExchangeNamesHowManyRoundsCarryNoCost(t *testing.T) {
	t.Parallel()
	reported := true
	priced := Round{CostUSD: 0.25, CostReported: &reported, Usage: json.RawMessage(`{"input_tokens":10,"output_tokens":2}`)}
	thread := costedThread(priced, unpricedRound(`{"input_tokens":20,"cache_read_input_tokens":30,"output_tokens":4}`), Round{CostUSD: 0.5})
	want := "$0.7500 reported; 60 input tokens (30 cached), 6 output tokens; no cost reported for 1 of 3 round(s)"
	if summary := thread.Summary(); !strings.HasSuffix(summary, want) {
		t.Fatalf("Summary() = %q, want it to end %q", summary, want)
	}
	rendered := thread.RenderThread()
	for _, round := range []string{
		"round 1 of 5 ($0.2500)",
		"round 2 of 5 (50 input tokens (30 cached), 4 output tokens; no cost reported)",
		"round 3 of 5 ($0.5000)",
	} {
		if !strings.Contains(rendered, round) {
			t.Fatalf("RenderThread() = %q, want %q", rendered, round)
		}
	}
	if total := TotalCostText([]Exchange{thread, costedThread(Round{CostUSD: 1})}); total != "$1.7500 reported; 60 input tokens (30 cached), 6 output tokens; no cost reported for 1 of 4 round(s)" {
		t.Fatalf("TotalCostText() = %q", total)
	}
}
