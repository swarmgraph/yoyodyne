package exchange

// How an exchange reads: to the operator who is auditing it, and to the asking
// role that is waiting on it.
//
// Both renderings say the rounds and the cost together, always. That pairing is
// the point: rounds alone say how long a conversation went on and cost alone
// says what it came to, and the operator's question — was this worth it — is
// only answerable from the two side by side.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sort orders exchanges the way they are read: the ones still being conducted
// first, and the most recently moved first within each group. An open exchange
// is the one that might still need somebody.
func Sort(exchanges []Exchange) {
	sort.SliceStable(exchanges, func(i, j int) bool {
		if exchanges[i].Open() != exchanges[j].Open() {
			return exchanges[i].Open()
		}
		return exchanges[i].UpdatedAt.After(exchanges[j].UpdatedAt)
	})
}

// State is what an exchange is, in one word an operator reads down a column.
func (e Exchange) State() string {
	if e.Open() {
		return "open"
	}
	return string(e.Outcome)
}

// Summary is one exchange as a line: who asked whom, where it got to, and what
// it cost.
func (e Exchange) Summary() string {
	return fmt.Sprintf("%s  %s asked %s, %s, %d/%d round(s), %s",
		e.ID, e.Asker.Role.Title(), e.Answerer.Role.Title(), e.State(),
		e.Spent(), e.MaxRounds, e.CostText())
}

// CostText is what the exchange cost as it is read anywhere: the dollars the
// provider reported, or, where a round's provider reported no dollar cost, the
// tokens the rounds used and how many rounds carry no cost. A round with no
// reported cost is never shown as costing nothing, and no price is estimated
// from its tokens.
func (e Exchange) CostText() string { return costText(e.Rounds) }

// TotalCostText is CostText across several exchanges, read as one set of
// rounds so a total that mixes priced and unpriced rounds says how many carry
// no cost.
func TotalCostText(exchanges []Exchange) string {
	var rounds []Round
	for _, one := range exchanges {
		rounds = append(rounds, one.Rounds...)
	}
	return costText(rounds)
}

// costText is CostText over any set of rounds, which is what lets one round of
// a thread read the same way the exchange it belongs to does.
func costText(rounds []Round) string {
	var total float64
	var input, cached, output int64
	uncosted, measured, unmeasured := 0, 0, 0
	for _, round := range rounds {
		total += round.CostUSD
		reported := round.CostReported == nil || *round.CostReported
		if !reported {
			uncosted++
		}
		usage, ok := round.tokens()
		switch {
		case ok:
			measured++
			input += usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
			cached += usage.CacheReadTokens
			output += usage.OutputTokens
		case !reported:
			unmeasured++
		}
	}
	if uncosted == 0 {
		return money(total)
	}
	tokens := fmt.Sprintf("%d input tokens (%d cached), %d output tokens", input, cached, output)
	if measured == 0 {
		tokens = "token usage not reported"
	} else if unmeasured > 0 {
		tokens += fmt.Sprintf("; usage not reported for %d round(s)", unmeasured)
	}
	if uncosted == len(rounds) {
		return tokens + "; no cost reported"
	}
	return fmt.Sprintf("%s reported; %s; no cost reported for %d of %d round(s)", money(total), tokens, uncosted, len(rounds))
}

// roundUsage is a round's usage under the names the answering invocation's
// usage is recorded with: input is fresh input, with cache reads beside it.
type roundUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// tokens reads the round's recorded usage, and reports false where the round
// carries none, which is a round nobody measured rather than one measured at
// nothing.
func (r Round) tokens() (roundUsage, bool) {
	if len(r.Usage) == 0 || string(r.Usage) == "null" {
		return roundUsage{}, false
	}
	var usage roundUsage
	if err := json.Unmarshal(r.Usage, &usage); err != nil {
		return roundUsage{}, false
	}
	return usage, true
}

// Render is one exchange as an operator reads it in a listing: the line above,
// the question it opened with, and what closed it.
func (e Exchange) Render() string {
	var rendered strings.Builder
	rendered.WriteString(e.Summary() + "\n")
	rendered.WriteString(indent("asked: " + singleLine(e.Question)))
	switch {
	case e.Outcome == OutcomeResolved && strings.TrimSpace(e.Settled) != "":
		rendered.WriteString(indent("settled: " + singleLine(e.Settled)))
	case e.Outcome == OutcomeUnresolved:
		rendered.WriteString(indent(fmt.Sprintf(
			"nothing was settled: it reached the %d round(s) it was opened with, and was escalated to you", e.MaxRounds)))
	}
	return rendered.String()
}

// RenderThread is the whole exchange, which is what "durable and visible" comes
// to in the end: every question and every answer as the two roles wrote them,
// readable by somebody who was not there.
func (e Exchange) RenderThread() string {
	var rendered strings.Builder
	rendered.WriteString(e.Summary() + "\n")
	fmt.Fprintf(&rendered, "opened %s", e.OpenedAt.UTC().Format(time.RFC3339))
	if e.ClosedAt != nil {
		fmt.Fprintf(&rendered, ", closed %s", e.ClosedAt.UTC().Format(time.RFC3339))
	}
	rendered.WriteString("\n")
	if agent := strings.TrimSpace(e.Asker.Agent); agent != "" {
		fmt.Fprintf(&rendered, "asked by %s", agent)
		if conversation := strings.TrimSpace(e.Asker.Conversation); conversation != "" {
			fmt.Fprintf(&rendered, ", from %s", conversation)
		}
		rendered.WriteString("\n")
	}
	if agent := strings.TrimSpace(e.Answerer.Agent); agent != "" {
		fmt.Fprintf(&rendered, "answered by %s\n", agent)
	}
	for _, round := range e.Rounds {
		fmt.Fprintf(&rendered, "\nround %d of %d (%s)\n", round.Number, e.MaxRounds, costText([]Round{round}))
		rendered.WriteString(indent(e.Asker.Role.Title() + ": " + strings.TrimSpace(round.Question)))
		if context := strings.TrimSpace(round.Context); context != "" {
			rendered.WriteString(indent("context: " + context))
		}
		switch {
		case strings.TrimSpace(round.Answer) != "":
			rendered.WriteString(indent(e.Answerer.Role.Title() + ": " + strings.TrimSpace(round.Answer)))
		case strings.TrimSpace(round.Problem) != "":
			rendered.WriteString(indent("unanswered: " + round.Problem))
		default:
			rendered.WriteString(indent("unanswered: the round was recorded and nothing came back"))
		}
	}
	if strings.TrimSpace(e.Settled) != "" {
		fmt.Fprintf(&rendered, "\nsettled: %s\n", strings.TrimSpace(e.Settled))
	}
	if e.Outcome == OutcomeUnresolved {
		fmt.Fprintf(&rendered, "\nnothing was settled: it reached the %d round(s) it was opened with, and was escalated to you.\n", e.MaxRounds)
	}
	return rendered.String()
}

// Delivery is what the harness hands back to the asking role when a round comes
// in: the answer, where the exchange has got to against its cap, and what the
// asker may do next. The cap is stated as live state rather than as contract
// text, because what a role needs is not the setting but how much of it this
// thread has left.
func (e Exchange) Delivery() string {
	var rendered strings.Builder
	rendered.WriteString("# The " + e.Answerer.Role.Title() + "'s answer\n\n")
	fmt.Fprintf(&rendered, "Exchange %s, round %d of the %d it is allowed. This is the %s's judgement and nothing more: it had no action authority, so treat it as advice rather than as a validation result or a decision. It is recorded and the operator can read it.\n\n",
		e.ID, e.Spent(), e.MaxRounds, e.Answerer.Role.Title())
	if len(e.Rounds) == 0 {
		// Nothing calls this for an exchange with no rounds, and an index into an
		// empty thread is not the way to find that out.
		return rendered.String() + "No round has been taken yet.\n"
	}
	last := e.Rounds[len(e.Rounds)-1]
	switch {
	case strings.TrimSpace(last.Answer) != "":
		rendered.WriteString(strings.TrimSpace(last.Answer) + "\n\n")
	default:
		problem := strings.TrimSpace(last.Problem)
		if problem == "" {
			problem = "nothing came back"
		}
		fmt.Fprintf(&rendered, "The question went unanswered: %s. The round is spent either way. Say so to the operator rather than describing an answer you did not get.\n\n", problem)
	}
	switch remaining := e.RoundsRemaining(); {
	case remaining > 0:
		fmt.Fprintf(&rendered, "Carry on answering the operator using this. Ask again in %s if you still need something, which leaves %d round(s); close it with \"settled\" as soon as you have what you needed.\n",
			e.ID, remaining)
	default:
		fmt.Fprintf(&rendered, "That was the last round %s is allowed. Close it with \"settled\", saying what you did and did not get; asking again closes it as unresolved and puts it to the operator.\n", e.ID)
	}
	return rendered.String()
}

// indent puts provider text under the harness's own line and never at the
// margin, which is what keeps a listing readable when what an agent wrote runs
// to several lines.
func indent(text string) string {
	trimmed := strings.TrimRight(text, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return ""
	}
	var rendered strings.Builder
	for _, line := range strings.Split(trimmed, "\n") {
		rendered.WriteString("  " + line + "\n")
	}
	return rendered.String()
}
