package backend

import (
	"strings"
)

// Tool calls are what an invocation's stream showed the agent actually doing,
// in one shape whichever provider ran it, so the harness can compare them with
// the tool access the invocation was launched under (internal/tooluse). The
// adapter that reads the stream is the only thing that can say what a call was,
// and the policy it launched the session under is the adapter's own decision,
// so both are on the result rather than worked out by anybody reading it later.
//
// What is recorded is evidence and nothing else. A call is recorded because the
// stream carried it, never because the reply said one was made, and a stream the
// adapter could not fully read says so in Gaps rather than reading as a session
// that used no tools.

// ToolKind is what a call does, in the harness's words rather than the
// provider's tool name.
type ToolKind string

const (
	// ToolKindRead reads files or searches them and changes nothing.
	ToolKindRead ToolKind = "read"
	// ToolKindShell runs a command. What the command could do is bounded by the
	// sandbox the session runs in, not by the call.
	ToolKindShell ToolKind = "shell"
	// ToolKindWrite edits, writes, or patches files.
	ToolKindWrite ToolKind = "write"
	// ToolKindWeb reaches the network: a fetch or a search.
	ToolKindWeb ToolKind = "web"
	// ToolKindExternal is a tool served from outside the provider, such as an
	// MCP server or a connector.
	ToolKindExternal ToolKind = "external"
	// ToolKindAgent hands work to another agent.
	ToolKindAgent ToolKind = "agent"
	// ToolKindSession is bookkeeping inside the session, such as a to-do list,
	// that touches nothing outside it.
	ToolKindSession ToolKind = "session"
	// ToolKindUnknown is a tool the harness has no description of.
	ToolKindUnknown ToolKind = "unknown"
)

// ToolOutcome is what the stream showed became of a call.
type ToolOutcome string

const (
	// ToolAttempted is a call the stream showed starting and never showed
	// finishing, so nothing says whether it ran.
	ToolAttempted ToolOutcome = "attempted"
	// ToolDenied is a call the provider refused to run.
	ToolDenied ToolOutcome = "denied"
	// ToolExecuted is a call that ran.
	ToolExecuted ToolOutcome = "executed"
	// ToolFailed is a call that ran and reported a failure that was not a
	// refusal, such as a command exiting non-zero.
	ToolFailed ToolOutcome = "failed"
)

// ToolCall is one call the stream showed.
type ToolCall struct {
	// ID is the provider's identifier for the call, redacted, and empty where
	// the provider gave none.
	ID string
	// Tool is the provider's name for the tool, redacted. Arguments are never
	// kept: what a call was handed can carry anything the agent read.
	Tool string
	Kind ToolKind
	// Parent is the call this one was made inside, such as the sub-agent call a
	// sub-agent's own calls belong to, and empty for a call the session made
	// itself.
	Parent  string
	Outcome ToolOutcome
	// InvocationID says the provider's identifier names the call only within
	// this invocation, as Codex numbers its items afresh in every process, so a
	// resumed session's calls are not the same calls as the last turn's.
	InvocationID bool
}

// ToolPolicy is the tool access an adapter launched a session under, as the
// harness compares calls against it.
type ToolPolicy string

const (
	// ToolPolicyNone is a session given no tools at all.
	ToolPolicyNone ToolPolicy = "no-tools"
	// ToolPolicyInspection is a session that may read and run commands under a
	// read-only sandbox, and nothing else.
	ToolPolicyInspection ToolPolicy = "read-only-inspection"
	// ToolPolicyDeveloper is a session whose work is editing its worktree.
	ToolPolicyDeveloper ToolPolicy = "developer-tools"
)

// Permits reports whether a call of the kind is consistent with the policy.
// agents says whether the invocation was given sub-agents, which only a
// developer can be.
func (p ToolPolicy) Permits(kind ToolKind, agents bool) bool {
	switch p {
	case ToolPolicyInspection:
		return kind == ToolKindRead || kind == ToolKindShell || kind == ToolKindSession
	case ToolPolicyDeveloper:
		switch kind {
		case ToolKindRead, ToolKindShell, ToolKindWrite, ToolKindSession:
			return true
		case ToolKindAgent:
			return agents
		}
		return false
	default:
		return false
	}
}

// Describe says the policy in words a person reads.
func (p ToolPolicy) Describe() string {
	switch p {
	case ToolPolicyNone:
		return "no tools"
	case ToolPolicyInspection:
		return "read-only inspection"
	case ToolPolicyDeveloper:
		return "the developer's tools"
	}
	return "an unrecorded tool policy"
}

// ToolVisibility is what an adapter says about how much of the session's tool
// use it could see.
type ToolVisibility struct {
	// Read says the adapter read the stream for tool calls at all. A result
	// without it is from an adapter that does not, and nothing on it is evidence
	// that no tool was used.
	Read bool
	// Policy is the tool access the adapter launched the session under.
	Policy ToolPolicy
	// Subagents says the session was given sub-agents.
	Subagents bool
	// Gaps say, in words a person reads, what the stream carried that the
	// adapter could not read as a tool call, each once.
	Gaps []string
}

// ToolCallLog collects one invocation's calls as a stream reports them. A
// provider can report a call more than once -- a start and a completion, a
// replayed event -- so a call is one entry per identifier, and what is learned
// later only moves its outcome forward.
type ToolCallLog struct {
	calls []ToolCall
	index map[string]int
	gaps  []string
}

// Start records a call the stream showed starting. A start for a call already
// recorded changes nothing.
func (l *ToolCallLog) Start(id, tool string, kind ToolKind, parent string) {
	if kind == "" {
		kind = ClassifyTool(tool)
	}
	if id != "" {
		if _, seen := l.index[id]; seen {
			return
		}
		if l.index == nil {
			l.index = map[string]int{}
		}
		l.index[id] = len(l.calls)
	}
	l.calls = append(l.calls, ToolCall{ID: id, Tool: tool, Kind: kind, Parent: parent, Outcome: ToolAttempted})
}

// StartInInvocation records a call whose identifier is unique only within
// this invocation (ToolCall.InvocationID).
func (l *ToolCallLog) StartInInvocation(id, tool string, kind ToolKind) {
	_, seen := l.index[id]
	l.Start(id, tool, kind, "")
	if !seen {
		l.calls[len(l.calls)-1].InvocationID = true
	}
}

// Finish records what became of a call. A denial is never overwritten: a
// provider that says a call was refused and later reports its result has still
// refused it. A finish for a call whose start the stream never showed is a gap
// rather than a guess at what the call was.
func (l *ToolCallLog) Finish(id string, outcome ToolOutcome) {
	at, seen := l.index[id]
	if !seen {
		l.Gap("a tool result arrived for a call whose start the stream did not show, so which tool ran is not known")
		return
	}
	if l.calls[at].Outcome == ToolDenied {
		return
	}
	l.calls[at].Outcome = outcome
}

// Deny records a call the provider says it refused, whether or not its start
// was seen.
func (l *ToolCallLog) Deny(id, tool string) {
	if _, seen := l.index[id]; !seen || id == "" {
		l.Start(id, tool, "", "")
	}
	if at, seen := l.index[id]; seen {
		l.calls[at].Outcome = ToolDenied
		return
	}
	l.calls[len(l.calls)-1].Outcome = ToolDenied
}

// Gap records something the stream carried that could not be read as a tool
// call, once however often it arrives.
func (l *ToolCallLog) Gap(limitation string) {
	for _, known := range l.gaps {
		if known == limitation {
			return
		}
	}
	l.gaps = append(l.gaps, limitation)
}

// Calls is every call recorded, in the order each first appeared.
func (l *ToolCallLog) Calls() []ToolCall {
	return append([]ToolCall(nil), l.calls...)
}

// Gaps is every limitation recorded, in the order each first appeared.
func (l *ToolCallLog) Gaps() []string {
	return append([]string(nil), l.gaps...)
}

// ClassifyTool says what a tool does from the name a provider gives it, for
// the providers the harness ships adapters for. A name nobody described is
// unknown rather than guessed at.
func ClassifyTool(name string) ToolKind {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "mcp__") {
		return ToolKindExternal
	}
	if kind, known := toolKinds[strings.ToLower(trimmed)]; known {
		return kind
	}
	return ToolKindUnknown
}

// toolKinds are the tool names Claude Code and Codex use, lower-cased.
var toolKinds = map[string]ToolKind{
	"read":            ToolKindRead,
	"glob":            ToolKindRead,
	"grep":            ToolKindRead,
	"ls":              ToolKindRead,
	"notebookread":    ToolKindRead,
	"view_image":      ToolKindRead,
	"bash":            ToolKindShell,
	"shell":           ToolKindShell,
	"local_shell":     ToolKindShell,
	"exec_command":    ToolKindShell,
	"edit":            ToolKindWrite,
	"multiedit":       ToolKindWrite,
	"write":           ToolKindWrite,
	"notebookedit":    ToolKindWrite,
	"apply_patch":     ToolKindWrite,
	"webfetch":        ToolKindWeb,
	"websearch":       ToolKindWeb,
	"web_search":      ToolKindWeb,
	"web_fetch":       ToolKindWeb,
	"task":            ToolKindAgent,
	"agent":           ToolKindAgent,
	"todowrite":       ToolKindSession,
	"todoread":        ToolKindSession,
	"update_plan":     ToolKindSession,
	"exitplanmode":    ToolKindSession,
	"toolsearch":      ToolKindSession,
	"enterplanmode":   ToolKindSession,
	"askuserquestion": ToolKindSession,
}
