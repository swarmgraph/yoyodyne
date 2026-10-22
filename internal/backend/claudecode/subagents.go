package claudecode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// How a developer invocation is allowed sub-agents on Claude Code
// (docs/designs/developer-sub-agents.md). Every part of it is on the command
// line, the settings and the environment the harness builds, and none is left
// to what the session is told:
//
//   - The only sub-agent the session may start is one the harness defines on
//     the command line (--agents), named for the run so nothing in the
//     repository, the account's home, or the provider's own configuration
//     names it in advance. The allow rule names that agent alone, and a hook
//     refuses every other sub-agent type, the built-in ones and any the
//     repository defines, whatever another settings source allows.
//   - Its tools are the run's own allowed tools without the sub-agent tool, so
//     it cannot start a sub-agent; the spawn depth is held to one as well.
//   - Background work is switched off in the session, so a sub-agent cannot be
//     started in the background, and the hook refuses one asked for. A
//     sub-agent is a tool call inside the invocation's own process, so it ends
//     when the invocation does, by whatever ends it.
//   - It runs in the session's process, in the worktree, under the session's
//     settings: the same sandbox, the same scoped write rules, the same guard.
//   - Its text and thinking are forwarded onto the stream beside its tool
//     calls, so a working sub-agent is output for the limit that ends a run
//     that has gone quiet, which is not lengthened for it.

// subagentToolNames are the names Claude Code gives the tool that starts a
// sub-agent: Agent, and Task, the name earlier versions used and this one
// still answers to.
var subagentToolNames = []string{"Agent", "Task"}

func isSubagentTool(name string) bool {
	for _, tool := range subagentToolNames {
		if name == tool {
			return true
		}
	}
	return false
}

// subagentEnvironment is what an invocation allowed sub-agents is started
// with, replacing whatever the harness's own environment carried: one level
// of sub-agent and no deeper, no background work, no forked sub-agent carrying
// the session's whole context, and no built-in Explore or Plan agents.
var subagentEnvironment = [][2]string{
	{"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH", "1"},
	{"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "1"},
	{"CLAUDE_CODE_FORK_SUBAGENT", "false"},
	{"CLAUDE_CODE_DISABLE_EXPLORE_PLAN_AGENTS", "1"},
}

// subagentModelOverrides are the variables an operator's shell could carry
// through that would put sub-agents on a model the configuration did not
// choose. They are removed from an invocation allowed sub-agents, and an
// invocation with none never starts one for them to reach.
var subagentModelOverrides = []string{"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE"}

// subagentName is the one sub-agent type an invocation may start. It carries
// the run so no definition found anywhere else can have been written for it.
func subagentName(runID string) string {
	return "yoyodyne-developer-" + shortRunID(runID)
}

// subagentPrompt is the definition's system prompt: what the sub-agent is and
// what it is not, beside the task the developer hands it.
const subagentPrompt = "You are a sub-agent of a Yoyodyne developer run. The developer session that started you owns the change and will check what you did. " +
	"Do the bounded piece of work it gave you, inside the run's worktree, with the tools you have. Do not commit, push, create branches, or run the work tracker's command. " +
	"Finish by saying exactly what you changed, what you ran, and what it reported, including anything that failed."

// subagentTools are the tools a sub-agent is given: the run's own allowed
// tools by name, without the sub-agent tool. A rule's scope stays the
// session's to apply, because a sub-agent's tool use is decided under the
// session's permissions.
func subagentTools(allowed []string) []string {
	seen := map[string]bool{}
	var tools []string
	for _, rule := range allowed {
		name := rule
		if open := strings.IndexByte(rule, '('); open >= 0 {
			name = rule[:open]
		}
		if name == "" || isSubagentTool(name) || seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, name)
	}
	return tools
}

// subagentDefinition is the --agents value: the one sub-agent the run may
// start, with its tools, model and effort.
func subagentDefinition(name string, request backend.SubagentRequest, allowed []string) (string, error) {
	definition := map[string]any{
		"description":     "Does a bounded piece of coding the developer hands it, in the run's worktree, and reports exactly what it changed.",
		"prompt":          subagentPrompt,
		"tools":           subagentTools(allowed),
		"disallowedTools": subagentToolNames,
		"model":           request.Model,
		"background":      false,
	}
	if effort := strings.TrimSpace(request.Effort); effort != "" {
		definition["effort"] = effort
	}
	encoded, err := json.Marshal(map[string]any{name: definition})
	if err != nil {
		return "", fmt.Errorf("encode the sub-agent a developer run may start: %w", err)
	}
	return string(encoded), nil
}

// subagentAllowRules are the allow rules naming the run's own sub-agent and no
// other, under both of the tool's names.
func subagentAllowRules(name string) []string {
	rules := make([]string, 0, len(subagentToolNames))
	for _, tool := range subagentToolNames {
		rules = append(rules, tool+"("+name+")")
	}
	return rules
}

// subagentGuard is the hook command that stands in front of the sub-agent
// tool. It refuses a call naming any sub-agent type but the run's own, and one
// asked to run in the background, by exiting 2 with the reason, which Claude
// Code reads as a refusal it shows the session.
//
// It reads the tool call's JSON with the shell and grep, which every system
// the sandbox runs on has, so it rests on nothing the harness puts on the
// run's PATH. A quote inside any string value is escaped, so the key and value
// can match only as the call's own subagent_type and run_in_background; space
// around the colon is allowed, so a provider that writes the call less
// compactly is read the same. It fails closed: where grep cannot run, the call
// is refused rather than let through.
func subagentGuard(name string) string {
	return `input=$(cat); ` +
		`printf '%s' "$input" | grep -Eq '"run_in_background"[[:space:]]*:[[:space:]]*true'; found=$?; ` +
		`case $found in 1) ;; 0) echo "Yoyodyne: a sub-agent cannot run in the background in a developer run." >&2; exit 2;; ` +
		`*) echo "Yoyodyne: this sub-agent call could not be checked, so it is refused." >&2; exit 2;; esac; ` +
		`printf '%s' "$input" | grep -Eq '"subagent_type"[[:space:]]*:[[:space:]]*"` + name + `"' && exit 0; ` +
		`echo "Yoyodyne: the only sub-agent this developer run may start is ` + name + `." >&2; exit 2`
}

// withSubagentSettings is a developer's settings with the sub-agent guard
// added to its hooks.
func withSubagentSettings(base, name string) (string, error) {
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(base), &settings); err != nil {
		return "", fmt.Errorf("decode the settings a developer run is given: %w", err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	before, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(before, map[string]any{
		"matcher": strings.Join(subagentToolNames, "|"),
		"hooks":   []any{map[string]any{"type": "command", "command": subagentGuard(name)}},
	})
	settings["hooks"] = hooks
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encode the settings a developer run is given: %w", err)
	}
	return string(encoded), nil
}

// withSubagentEnvironment is an invocation's environment with sub-agents
// confined as subagentEnvironment says and no model override carried through.
func withSubagentEnvironment(environment []string) []string {
	kept := make([]string, 0, len(environment)+len(subagentEnvironment))
	for _, entry := range environment {
		dropped := false
		for _, name := range subagentModelOverrides {
			if strings.HasPrefix(entry, name+"=") {
				dropped = true
			}
		}
		if !dropped {
			kept = append(kept, entry)
		}
	}
	for _, variable := range subagentEnvironment {
		kept = withEnvironmentValue(kept, variable[0], variable[1])
	}
	return kept
}

// subagentCalls follows the calls to sub-agents an invocation's stream
// shows: one opened by the session's own call of the sub-agent tool, a tool
// use counted for each call the sub-agent makes, and closed by the tool's
// result.
type subagentCalls struct {
	// models is the model each sub-agent type the invocation defined runs on.
	models map[string]string
	calls  []backend.SubagentCall
	open   map[string]int
}

func (s *subagentCalls) start(id string, input json.RawMessage, at time.Time) {
	var asked struct {
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	}
	_ = json.Unmarshal(input, &asked)
	call := backend.SubagentCall{
		ID:          id,
		StartedAt:   at,
		Agent:       asked.SubagentType,
		Description: asked.Description,
		Model:       s.models[asked.SubagentType],
	}
	if s.open == nil {
		s.open = map[string]int{}
	}
	s.open[id] = len(s.calls)
	s.calls = append(s.calls, call)
}

func (s *subagentCalls) toolUse(parent string) {
	if index, ok := s.open[parent]; ok {
		s.calls[index].ToolUses++
	}
}

// finish closes the call a tool result answers, reading the usage the
// provider reported with it where there is any.
func (s *subagentCalls) finish(id string, failed bool, reported json.RawMessage, at time.Time) (backend.SubagentCall, bool) {
	index, ok := s.open[id]
	if !ok {
		return backend.SubagentCall{}, false
	}
	delete(s.open, id)
	s.calls[index].EndedAt = at
	s.calls[index].Failed = failed
	var usage struct {
		TotalTokens       *int64 `json:"totalTokens"`
		TotalToolUseCount *int   `json:"totalToolUseCount"`
	}
	if len(reported) > 0 && json.Unmarshal(reported, &usage) == nil {
		if usage.TotalTokens != nil {
			s.calls[index].TotalTokens, s.calls[index].UsageReported = *usage.TotalTokens, true
		}
		if usage.TotalToolUseCount != nil && *usage.TotalToolUseCount > s.calls[index].ToolUses {
			s.calls[index].ToolUses = *usage.TotalToolUseCount
		}
	}
	return s.calls[index], true
}

// recorded is every call, with any the invocation ended before finishing
// marked interrupted: nothing resumes one.
func (s *subagentCalls) recorded() []backend.SubagentCall {
	if len(s.calls) == 0 {
		return nil
	}
	calls := append([]backend.SubagentCall(nil), s.calls...)
	for _, index := range s.open {
		calls[index].Interrupted = true
	}
	return calls
}

// providerModelUsage is the modelUsage a Claude Code terminal reports, keyed
// by model.
type providerModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

// modelUsageOf is the terminal's usage for each model, ordered by model, and
// false where the terminal reported none.
func modelUsageOf(reported map[string]providerModelUsage) ([]backend.ModelUsage, bool) {
	if len(reported) == 0 {
		return nil, false
	}
	models := make([]string, 0, len(reported))
	for model := range reported {
		models = append(models, model)
	}
	sort.Strings(models)
	usage := make([]backend.ModelUsage, 0, len(models))
	for _, model := range models {
		figures := reported[model]
		usage = append(usage, backend.ModelUsage{
			Model:                    model,
			CostUSD:                  figures.CostUSD,
			InputTokens:              figures.InputTokens,
			OutputTokens:             figures.OutputTokens,
			CacheReadInputTokens:     figures.CacheReadInputTokens,
			CacheCreationInputTokens: figures.CacheCreationInputTokens,
		})
	}
	return usage, true
}
