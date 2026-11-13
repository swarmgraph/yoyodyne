package config

// How long each persona's turns, messages, and provider calls may take.
//
// Every one of these limits was a figure fixed in code, and the right figure is
// a judgement about the work a persona does: a program manager whose pass reads
// a large lane needs longer than one that reads a small one. So each is stated
// in the agent's own block, under `time_limits`, and an agent that states
// nothing keeps exactly the figure the harness used before the key existed.
//
// What no value here reaches is the five-minute limit on a provider session
// that produces no output, which each provider adapter holds whatever the
// invocation's total time is, or the single automatic continuation of a run
// that went silent (runstate.MaxHarnessStallContinuations). A longer limit lets
// a working session run longer; it never lets a silent one sit.
//
// The limits select how long work may take and never what it may do, which is
// what `configuration-never-grants-authority` requires.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/budget"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The figures an agent that states nothing is held to. Each is the figure the
// place that applies it used before it was configurable, and a test in that
// place's package holds the two equal: chat.DefaultTurnTimeout,
// slack.DefaultConversationDeadline, the exchange and side-thread bounds in
// internal/cli, review's defaultReviewTimeout, and the provider adapters'
// defaultTimeout. The message limits are the budget package's own defaults.
const (
	DefaultTurnTimeLimit           = 15 * time.Minute
	DefaultMessageTimeLimit        = budget.DefaultMessageElapsed
	DefaultMessageTurnLimit        = budget.DefaultMessageTurns
	DefaultSlackTurnTimeLimit      = 10 * time.Minute
	DefaultExchangeAnswerTimeLimit = 5 * time.Minute
	DefaultSideThreadTurnTimeLimit = 5 * time.Minute
	DefaultReviewTimeLimit         = 15 * time.Minute
	DefaultProviderCallTimeLimit   = 4 * time.Hour
)

// The ranges a stated limit is checked against when the configuration is read.
// The floors keep a limit long enough for a provider to answer at all; the
// ceilings keep a typo from turning a bound into none. The provider call's floor
// is above the five-minute no-output limit, so a developer's total time can
// never be what stops a session that is still working before silence would.
const (
	MinTurnTimeLimit         = time.Minute
	MaxTurnTimeLimit         = 4 * time.Hour
	MinMessageTimeLimit      = 2 * time.Minute
	MaxMessageTimeLimit      = 8 * time.Hour
	MinMessageTurnLimit      = 1
	MaxMessageTurnLimit      = 100
	MaxMessageSpendUSD       = 1000.0
	MinAnswerTimeLimit       = time.Minute
	MaxAnswerTimeLimit       = time.Hour
	MinReviewTimeLimit       = time.Minute
	MaxReviewTimeLimit       = 4 * time.Hour
	MinProviderCallTimeLimit = 10 * time.Minute
	MaxProviderCallTimeLimit = 24 * time.Hour
)

// TimeLimits is one agent's `time_limits` block. A zero field is a limit the
// agent did not state, which takes the default above. The conversation limits
// are for an agent that holds a conversation, Review is the reviewer's, and
// ProviderCall is the developer's; a limit stated on an agent of another role is
// refused when the configuration is read, since nothing would apply it.
type TimeLimits struct {
	// Turn bounds one provider turn in the agent's conversation, and is the
	// provider invocation's own total time. A recurring pass's turn starts
	// counting it once the turn holds the conversation, so time spent waiting
	// for it to be free is not counted.
	Turn Duration `yaml:"turn,omitempty" json:"turn,omitempty"`
	// Message bounds the elapsed time of one message, however many turns
	// answering it takes, and how long a recurring pass's turn may wait for the
	// conversation to be free. It has to be longer than Turn.
	Message Duration `yaml:"message,omitempty" json:"message,omitempty"`
	// MessageTurns bounds how many provider turns one message may take.
	MessageTurns int `yaml:"message_turns,omitempty" json:"message_turns,omitempty"`
	// MessageSpendUSD bounds what one message's turns may cost, where the
	// provider reports a cost. Unset, a message has no spend limit.
	MessageSpendUSD float64 `yaml:"message_spend_usd,omitempty" json:"message_spend_usd,omitempty"`
	// SlackTurn bounds one turn taken from the Slack channel, from the message
	// arriving to the answer being posted.
	SlackTurn Duration `yaml:"slack_turn,omitempty" json:"slack_turn,omitempty"`
	// ExchangeAnswer bounds the agent's answer to a question another role asked
	// it.
	ExchangeAnswer Duration `yaml:"exchange_answer,omitempty" json:"exchange_answer,omitempty"`
	// SideThreadTurn bounds one turn on a side thread beside the agent's main
	// conversation.
	SideThreadTurn Duration `yaml:"side_thread_turn,omitempty" json:"side_thread_turn,omitempty"`
	// Review bounds one reviewer invocation.
	Review Duration `yaml:"review,omitempty" json:"review,omitempty"`
	// ProviderCall bounds one developer provider invocation, tool work included.
	ProviderCall Duration `yaml:"provider_call,omitempty" json:"provider_call,omitempty"`
}

func orDefault(stated Duration, fallback time.Duration) time.Duration {
	if stated <= 0 {
		return fallback
	}
	return stated.Duration()
}

// TurnLimit is the agent's conversation turn limit, or the default.
func (l TimeLimits) TurnLimit() time.Duration { return orDefault(l.Turn, DefaultTurnTimeLimit) }

// MessageLimit is the elapsed time one message may take, or the default.
func (l TimeLimits) MessageLimit() time.Duration {
	return orDefault(l.Message, DefaultMessageTimeLimit)
}

// MessageTurnLimit is how many provider turns one message may take, or the
// default.
func (l TimeLimits) MessageTurnLimit() int {
	if l.MessageTurns <= 0 {
		return DefaultMessageTurnLimit
	}
	return l.MessageTurns
}

// MessageBudget is the three message limits as the budget package decides them.
func (l TimeLimits) MessageBudget() budget.Limits {
	return budget.Limits{Turns: l.MessageTurnLimit(), Elapsed: l.MessageLimit(), SpendUSD: max(l.MessageSpendUSD, 0)}
}

// SlackTurnLimit is how long a turn started from Slack may take, or the default.
func (l TimeLimits) SlackTurnLimit() time.Duration {
	return orDefault(l.SlackTurn, DefaultSlackTurnTimeLimit)
}

// ExchangeAnswerLimit is how long the agent's answer in an exchange may take,
// or the default.
func (l TimeLimits) ExchangeAnswerLimit() time.Duration {
	return orDefault(l.ExchangeAnswer, DefaultExchangeAnswerTimeLimit)
}

// SideThreadTurnLimit is how long one side-thread turn may take, or the
// default.
func (l TimeLimits) SideThreadTurnLimit() time.Duration {
	return orDefault(l.SideThreadTurn, DefaultSideThreadTurnTimeLimit)
}

// ReviewLimit is how long one reviewer invocation may take, or the default.
func (l TimeLimits) ReviewLimit() time.Duration { return orDefault(l.Review, DefaultReviewTimeLimit) }

// ProviderCallLimit is how long one developer provider invocation may take, or
// the default.
func (l TimeLimits) ProviderCallLimit() time.Duration {
	return orDefault(l.ProviderCall, DefaultProviderCallTimeLimit)
}

// holdsConversation reports a role whose agent holds a conversation: every
// shipped role but the developer and the reviewer, which are invoked for a run
// and answer nobody.
func holdsConversation(role domain.AgentRole) bool {
	return role.Valid() && role != domain.RoleDeveloper && role != domain.RoleReviewer
}

// timeLimitProblems reports every stated limit outside its range or on an agent
// whose role nothing would apply it to, and a turn limit not shorter than the
// message limit that holds it. Each names the agent, the key, and the value.
func timeLimitProblems(name string, agent AgentConfig) []string {
	limits := agent.TimeLimits
	var problems []string
	duration := func(key string, stated Duration, low, high time.Duration) {
		if stated == 0 {
			return
		}
		if stated.Duration() < low || stated.Duration() > high {
			problems = append(problems, fmt.Sprintf("agent %q time_limits.%s is %s, which is outside its range of %s to %s",
				name, key, stated, Duration(low), Duration(high)))
		}
	}
	misplaced := func(key, holder string, stated bool) {
		if stated {
			problems = append(problems, fmt.Sprintf("agent %q time_limits.%s is set, but only %s uses it and this agent's role is %s",
				name, key, holder, agent.Role))
		}
	}

	duration("turn", limits.Turn, MinTurnTimeLimit, MaxTurnTimeLimit)
	duration("message", limits.Message, MinMessageTimeLimit, MaxMessageTimeLimit)
	if limits.MessageTurns != 0 && (limits.MessageTurns < MinMessageTurnLimit || limits.MessageTurns > MaxMessageTurnLimit) {
		problems = append(problems, fmt.Sprintf("agent %q time_limits.message_turns is %d, which is outside its range of %d to %d",
			name, limits.MessageTurns, MinMessageTurnLimit, MaxMessageTurnLimit))
	}
	if limits.MessageSpendUSD < 0 || limits.MessageSpendUSD > MaxMessageSpendUSD {
		problems = append(problems, fmt.Sprintf("agent %q time_limits.message_spend_usd is %g, which is outside its range of above 0 to %g",
			name, limits.MessageSpendUSD, MaxMessageSpendUSD))
	}
	duration("slack_turn", limits.SlackTurn, MinTurnTimeLimit, MaxTurnTimeLimit)
	duration("exchange_answer", limits.ExchangeAnswer, MinAnswerTimeLimit, MaxAnswerTimeLimit)
	duration("side_thread_turn", limits.SideThreadTurn, MinAnswerTimeLimit, MaxAnswerTimeLimit)
	duration("review", limits.Review, MinReviewTimeLimit, MaxReviewTimeLimit)
	duration("provider_call", limits.ProviderCall, MinProviderCallTimeLimit, MaxProviderCallTimeLimit)

	if !agent.Role.Valid() {
		// An unknown role is reported on its own, and which limits it may carry
		// cannot be said of a name that is not a role.
		return problems
	}
	converses := holdsConversation(agent.Role)
	const conversing = "an agent that holds a conversation"
	misplaced("turn", conversing, !converses && limits.Turn != 0)
	misplaced("message", conversing, !converses && limits.Message != 0)
	misplaced("message_turns", conversing, !converses && limits.MessageTurns != 0)
	misplaced("message_spend_usd", conversing, !converses && limits.MessageSpendUSD != 0)
	misplaced("slack_turn", conversing, !converses && limits.SlackTurn != 0)
	misplaced("exchange_answer", conversing, !converses && limits.ExchangeAnswer != 0)
	misplaced("side_thread_turn", conversing, !converses && limits.SideThreadTurn != 0)
	misplaced("review", "the reviewer", agent.Role != domain.RoleReviewer && limits.Review != 0)
	misplaced("provider_call", "the developer", agent.Role != domain.RoleDeveloper && limits.ProviderCall != 0)

	// A turn as long as its message would be cut off by the message limit at the
	// moment its own limit fell due, so which of the two stopped it would be a
	// race; the turn has to be the shorter. Defaults count, so a turn raised past
	// the default message limit has to raise the message limit too.
	if converses && limits.TurnLimit() >= limits.MessageLimit() {
		problems = append(problems, fmt.Sprintf("agent %q time_limits.turn is %s, which is not shorter than its message limit of %s; a turn has to be shorter than the message that holds it, so raise time_limits.message as well",
			name, Duration(limits.TurnLimit()), Duration(limits.MessageLimit())))
	}
	return problems
}
