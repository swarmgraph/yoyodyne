package backend

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Sub-agents are a developer session handing bounded pieces of its own work to
// agents on another model through the provider's own sub-agent tool
// (docs/designs/developer-sub-agents.md). The session stays the one invocation
// the harness launched: one process, one sandbox, one worktree, one event
// stream, and one line in the cost log.
//
// Whether a provider offers them is the adapter's to say and never a
// project's. It is on the descriptor beside the effort levels rather than in
// Capabilities, because Capabilities is what a declared provider writes for
// itself, and a configuration key that could switch sub-agents on for an
// adapter that cannot contain them would be configuration granting authority.
// A provider declared on an adapter inherits that adapter's answer.

// SubagentSupport is what an adapter version says about sub-agents. The three
// facts beside Offered are the conditions the design makes for relying on them:
// a sub-agent runs inside the invocation's own process and sandbox, its tool
// use and messages are on the invocation's event stream, and usage is reported
// for each model or only as a total. They are stated per adapter version, read
// from the provider executable the version was written against; an adapter
// version for which any of the first two does not hold is not eligible.
//
// What is stated here is not relied on alone. Before every developer
// invocation given sub-agents, the installed provider is asked whether it
// applies what they are launched with (SubagentLaunchChecker), and a provider
// that does not confirm it serves the run without them.
type SubagentSupport struct {
	Offered       bool
	InProcess     bool
	Streamed      bool
	UsagePerModel bool
	// EffortLevels are the levels the provider accepts for a sub-agent, and nil
	// where it accepts none, so an effort configured for one is refused when
	// the configuration loads rather than dropped.
	EffortLevels []string
}

// Eligible reports an adapter version a run may hand work to sub-agents on.
func (s SubagentSupport) Eligible() bool {
	return s.Offered && s.InProcess && s.Streamed
}

// AcceptsEffort reports whether the provider can apply the level to a
// sub-agent.
func (s SubagentSupport) AcceptsEffort(level string) bool {
	for _, accepted := range s.EffortLevels {
		if accepted == strings.TrimSpace(level) {
			return true
		}
	}
	return false
}

// SubagentsUnavailable says, in words a person reads on a run, why a provider
// serves a run without sub-agents, and nothing where it can serve them.
func (d Descriptor) SubagentsUnavailable() string {
	switch {
	case d.Subagents.Eligible():
		return ""
	case !d.Subagents.Offered:
		return fmt.Sprintf("%s does not offer them", providerName(d))
	case !d.Subagents.InProcess:
		return fmt.Sprintf("%s would run them outside the run's own process and sandbox", providerName(d))
	default:
		return fmt.Sprintf("%s does not report what they do on the run's event stream", providerName(d))
	}
}

func providerName(d Descriptor) string {
	switch d.ID {
	case domain.BackendClaudeCode:
		return "Claude Code"
	case domain.BackendCodex:
		return "Codex"
	}
	return string(d.ID)
}

// claudeCodeSubagents is what Claude Code 2.1.286 does with sub-agents, read
// from its help and the agent schema in the shipped executable with no
// provider call: a sub-agent defined with --agents runs as a tool call inside
// the session's own process, under the session's settings and sandbox; its
// tool calls are written to the stream with parent_tool_use_id set, and its
// text and thinking too with --forward-subagent-text; and the terminal result
// carries modelUsage, the session's usage and cost for each model. An agent
// definition accepts the same effort levels as --effort.
//
// The launch check in internal/backend/claudecode/subagentcheck.go confirms on
// the installed CLI, whatever its version, what can be confirmed without a
// model call: that it names both flags, loads the run's own sub-agent on the
// configured model, rejects none of the settings, and will run the hook that
// refuses every other sub-agent and any in the background. That a sub-agent
// still runs in the session's own process and that the terminal still reports
// usage for each model can only be seen by running one, so those two rest on
// the version recorded above; a terminal without per-model usage is recorded
// as an unknown split rather than guessed at.
var claudeCodeSubagents = SubagentSupport{
	Offered:       true,
	InProcess:     true,
	Streamed:      true,
	UsagePerModel: true,
	EffortLevels:  claudeCodeEffortLevels,
}

// SubagentRequest is a developer invocation allowed to hand work to
// sub-agents, and the model and effort they run on. A request carrying none is
// an invocation with no sub-agents, which is every invocation unless the run's
// configuration turned them on and its endpoint offers them.
type SubagentRequest struct {
	Model string
	// Effort is empty where none was configured, which leaves the provider's
	// own default for a sub-agent.
	Effort string
}

// ModelUsage is what the provider reported one model doing in an invocation.
// Claude Code reports the session's running figures, as it does for the
// invocation's total cost, so on a resumed session these are what the session
// has used since it began rather than what this invocation added.
type ModelUsage struct {
	Model                    string  `json:"model"`
	CostUSD                  float64 `json:"cost_usd"`
	InputTokens              int64   `json:"input_tokens,omitempty"`
	OutputTokens             int64   `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens,omitempty"`
}

// SubagentCall is one call to a sub-agent the developer session made, as
// the invocation's stream showed it.
type SubagentCall struct {
	// ID is the provider's identifier for the tool call that started it.
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	// EndedAt is zero for a call the invocation ended without finishing, and
	// Interrupted says so: a call nothing finished is never resumed, and the
	// record shows it was cut off.
	EndedAt     time.Time `json:"ended_at,omitzero"`
	Interrupted bool      `json:"interrupted,omitempty"`
	// Agent is the sub-agent type the session asked for, and Model the model
	// that definition runs on.
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
	// Description is the short description of the task the session gave.
	Description string `json:"description,omitempty"`
	// ToolUses is how many tools the sub-agent used, counted off the stream.
	ToolUses int  `json:"tool_uses"`
	Failed   bool `json:"failed,omitempty"`
	// TotalTokens is the sub-agent's usage where the provider reported it, and
	// UsageReported false where it did not.
	TotalTokens   int64 `json:"total_tokens,omitempty"`
	UsageReported bool  `json:"usage_reported,omitempty"`
}

// SubagentLaunchChecker is an adapter that can establish, before a developer
// invocation is given sub-agents, that the installed provider applies what
// sub-agents are launched with: the run's own sub-agent definition on its
// model, the flags that carry it and put what it does on the event stream, and
// whatever refuses every other sub-agent. It makes no provider call and spends
// nothing.
//
// A check that found something missing is not an error: it names what did not
// take, and the run goes without sub-agents and says why. An error is a check
// that could not be made, and the run goes without them for that reason.
// Nothing about the run is refused either way: sub-agents are a way of doing
// the work cheaper, and a run without them does it as it always did.
type SubagentLaunchChecker interface {
	CheckSubagentLaunch(ctx context.Context, directory, runID string, subagents SubagentRequest) (LaunchSettingsCheck, error)
}

// limitFamilies are the model families a provider can name a usage limit for
// on its own, as Claude Code does with seven_day_opus and seven_day_sonnet.
var limitFamilies = []string{"opus", "sonnet", "haiku"}

// LimitFamily is the model family a usage limit's kind names, and empty for a
// limit on the account as a whole, such as five_hour or seven_day.
func LimitFamily(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, family := range limitFamilies {
		if strings.HasSuffix(kind, "_"+family) {
			return family
		}
	}
	return ""
}

// SubagentsOnlyLimit reports a usage limit of kind that stops only an
// invocation's sub-agents: it names a model family the sub-agent model is in
// and the invocation's own model is not. A limit on the whole account, one on
// the invocation's own family, and one where the invocation's model is not
// known are the run's own, and are answered as every limit is.
func SubagentsOnlyLimit(kind, model, subagentModel string) bool {
	family := LimitFamily(kind)
	if family == "" || strings.TrimSpace(model) == "" || strings.TrimSpace(subagentModel) == "" {
		return false
	}
	return ModelInFamily(subagentModel, family) && !ModelInFamily(model, family)
}
