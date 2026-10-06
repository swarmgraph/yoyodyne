package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An answering round lands in the cost log like every other provider invocation
// the harness makes. It is the one with neither a run nor a conversation behind
// it, so without this it would be the one invocation whose cost nothing durable
// ever recorded.
func TestAnAnsweringRoundRecordsWhatItSpentAgainstTheExchange(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{result: backend.RunResult{
		SessionID:    "session-2",
		FinalText:    "More than the ordering assumes.",
		CostUSD:      0.25,
		CostReported: true,
	}}
	log := &recordingSpendLog{}
	voice := exchangeVoice{
		config:     answeringConfig(),
		provider:   provider,
		repository: t.TempDir(),
		spend:      log,
		productID:  "yoyodyne",
	}
	question := exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("a", 32),
		Role:       domain.RoleArchitect,
		Asker:      domain.RoleProductManager,
		Round:      1,
		MaxRounds:  10,
		Question:   "what does this goal cost, and what am I missing?",
	}

	if _, err := voice.Answer(context.Background(), question); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want one for the round's one invocation: %#v", len(log.lines), log.lines)
	}

	line := log.lines[0]
	if line.Phase != runstate.SpendPhaseExchange || line.ExchangeID != question.ExchangeID {
		t.Errorf("line = %#v, want the round charged to the exchange", line)
	}
	// An answering round belongs to no run and no conversation, and serves no
	// assigned work, so it names none of them.
	if line.RunID != "" || line.ConversationID != "" || line.WorkItemID != "" {
		t.Errorf("line = %#v, want nothing but the exchange", line)
	}
	if line.Role != domain.RoleArchitect || line.Agent != "architect" {
		t.Errorf("line = %#v, want the answering role and the agent that filled it", line)
	}
	if line.Backend != domain.BackendClaudeCode || line.Model != "opus-architect" {
		t.Errorf("line = %#v, want what served the round", line)
	}
	if !line.Known() || line.AmountUSD != 0.25 {
		t.Errorf("line = %#v, want the provider's own figure", line)
	}
	if err := line.Validate(); err != nil {
		t.Errorf("recorded line does not satisfy the durable contract: %v", err)
	}
}

// A voice with nowhere to record what a round cost answers exactly as it would
// have. That is what keeps the log optional for a caller that has not built one
// without making its absence silent anywhere the harness itself wires it.
func TestAnAnsweringRoundWithNowhereToRecordSpendsAsItWould(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{result: backend.RunResult{SessionID: "session-3", FinalText: "Answered."}}
	voice := exchangeVoice{config: answeringConfig(), provider: provider, repository: t.TempDir(), productID: "yoyodyne"}
	spoken, err := voice.Answer(context.Background(), exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("b", 32),
		Role:       domain.RoleArchitect,
		Asker:      domain.RoleProductManager,
		Round:      1,
		MaxRounds:  10,
		Question:   "and this one?",
	})
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if spoken.Answer != "Answered." || provider.calls != 1 {
		t.Fatalf("spoken = %+v after %d call(s)", spoken, provider.calls)
	}
}

// recordingSpendLog is the cost log as a test reads it back.
type recordingSpendLog struct {
	lines []runstate.Spend
}

func (l *recordingSpendLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return nil
}

// ReportedSessionTotal answers from the lines this log has already taken, the
// way the durable store answers from the lines it has already written.
func (l *recordingSpendLog) ReportedSessionTotal(sessionID string) (float64, bool, error) {
	total, found := 0.0, false
	for _, line := range l.lines {
		if line.SessionID == sessionID && line.Known() {
			total, found = line.ReportedTotal(), true
		}
	}
	return total, found, nil
}

func TestCodexSpendNamesTokensAndMissingPrices(t *testing.T) {
	usage := runstate.TokenUsage{InputTokens: 3120, CacheReadTokens: 13184, OutputTokens: 5, Measured: 1, NoCost: 1}
	row := runstate.SpendRow{Calls: 1, Usage: &usage}
	if text := spendCostText(row); strings.Contains(text, "$0") || !strings.Contains(text, "16304 input tokens (13184 cached), 5 output tokens") || !strings.Contains(text, "no cost reported") {
		t.Fatal(text)
	}
	usage.Merge(runstate.TokenUsage{InputTokens: 20, OutputTokens: 3, Measured: 1})
	row.CostUSD = 2
	if text := spendCostText(row); !strings.Contains(text, "$2.00 reported") || !strings.Contains(text, "no cost reported for 1 turn") {
		t.Fatal(text)
	}
}
