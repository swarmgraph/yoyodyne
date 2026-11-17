package readmodel

// The MCP calls the harness made, counted for every surface
// (docs/designs/mcp-tools-through-the-harness.md, section 4, and build piece 7).
//
// The call record keeps up to 256 KiB of each result, and the arguments each
// call was made with. None of that reaches a surface: what is projected here is
// each tool by name, how many of its calls ended each way, and when the latest
// one started. The record's own reader, `yoyo mcp calls`, is the one place a
// person reads a call's content, at their own terminal. MCPCalls and
// MCPToolCalls therefore carry no field a result, an argument, or the reason an
// agent gave could be put in, and a test holds them to that.
//
// A call refused because the tool is not on the caller's list names, in the
// record, whatever the agent wrote as the tool's name. That text is the agent's,
// not the configuration's, so it is counted here without its name.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
)

// MCPCallRecord is the record of MCP calls. It is satisfied by
// *mcpcall.Records.
type MCPCallRecord interface {
	Read() ([]mcpcall.Record, int, error)
}

// mcpCallWindow is how far back the counts reach: the same rolling day the
// spend box reckons from the moment of the reading.
const mcpCallWindow = 24 * time.Hour

// MCPCalls is the MCP calls of the last day, counted, with no content.
type MCPCalls struct {
	// Since is the start of the window the counts cover.
	Since time.Time `json:"since"`
	// Calls is every call that started in the window, and the four counts
	// beside it split it by how each ended.
	Calls     int `json:"calls"`
	Performed int `json:"performed"`
	Refused   int `json:"refused"`
	Failed    int `json:"failed"`
	Unknown   int `json:"unknown"`
	// Tools is each declared tool called in the window, by name.
	Tools []MCPToolCalls `json:"tools,omitempty"`
	// NotOnList is how many calls in the window asked for a tool not on the
	// caller's list; they are refused, and counted here without the name the
	// agent wrote.
	NotOnList int `json:"not_on_list,omitempty"`
	// Recorded is every call in the record, whatever its age.
	Recorded int `json:"recorded"`
	// UnknownActs is every act call in the record, whatever its age, whose
	// outcome nobody knows: it may have happened.
	UnknownActs int `json:"unknown_acts,omitempty"`
	// Unreadable is how many lines of the record could not be read.
	Unreadable int `json:"unreadable,omitempty"`
}

// MCPToolCalls is one tool's calls in the window.
type MCPToolCalls struct {
	// Tool is the tool as the configuration declares it, server and tool.
	Tool      string              `json:"tool"`
	Class     config.MCPToolClass `json:"class,omitempty"`
	Calls     int                 `json:"calls"`
	Performed int                 `json:"performed"`
	Refused   int                 `json:"refused"`
	Failed    int                 `json:"failed"`
	Unknown   int                 `json:"unknown"`
	// Latest is when the newest of them started.
	Latest time.Time `json:"latest"`
}

// ReadMCPCalls counts the record's calls for the day before now. A product
// with no record wired, or one whose record holds no call, reads nil and says
// nothing, so a project that declares no MCP server shows exactly what it
// showed before the record existed. A record that cannot be read says so
// rather than reading as no calls.
func ReadMCPCalls(record MCPCallRecord, now time.Time) (*MCPCalls, string) {
	if record == nil {
		return nil, ""
	}
	records, unreadable, err := record.Read()
	if err != nil {
		return nil, fmt.Sprintf("the record of MCP calls could not be read: %v", err)
	}
	if len(records) == 0 && unreadable == 0 {
		return nil, ""
	}
	calls := &MCPCalls{Since: now.Add(-mcpCallWindow).UTC(), Recorded: len(records), Unreadable: unreadable}
	tools := map[string]*MCPToolCalls{}
	for _, call := range records {
		if call.Outcome == mcpcall.OutcomeUnknown && call.Class == config.MCPAct {
			calls.UnknownActs++
		}
		if call.StartedAt.Before(calls.Since) || call.StartedAt.After(now) {
			continue
		}
		calls.Calls++
		countMCPOutcome(&calls.Performed, &calls.Refused, &calls.Failed, &calls.Unknown, call.Outcome)
		if call.Server == "" {
			// Only a call refused as not on the caller's list is recorded with
			// no server: its name is the agent's own text.
			calls.NotOnList++
			continue
		}
		name := call.Server + "." + strings.TrimPrefix(call.Tool, call.Server+".")
		tool := tools[name]
		if tool == nil {
			tool = &MCPToolCalls{Tool: name, Class: call.Class}
			tools[name] = tool
		}
		tool.Calls++
		countMCPOutcome(&tool.Performed, &tool.Refused, &tool.Failed, &tool.Unknown, call.Outcome)
		if call.StartedAt.After(tool.Latest) {
			tool.Latest = call.StartedAt.UTC()
		}
	}
	for _, tool := range tools {
		calls.Tools = append(calls.Tools, *tool)
	}
	sort.Slice(calls.Tools, func(i, j int) bool { return calls.Tools[i].Tool < calls.Tools[j].Tool })
	return calls, ""
}

func countMCPOutcome(performed, refused, failed, unknown *int, outcome mcpcall.Outcome) {
	switch outcome {
	case mcpcall.OutcomePerformed:
		*performed++
	case mcpcall.OutcomeRefused:
		*refused++
	case mcpcall.OutcomeUnknown:
		*unknown++
	default:
		*failed++
	}
}

// Outcomes is the counts in words, leaving out the ways no call ended.
func (c MCPToolCalls) Outcomes() string {
	return mcpOutcomeWords(c.Performed, c.Refused, c.Failed, c.Unknown)
}

// Outcomes is the counts in words, leaving out the ways no call ended.
func (c MCPCalls) Outcomes() string {
	return mcpOutcomeWords(c.Performed, c.Refused, c.Failed, c.Unknown)
}

func mcpOutcomeWords(performed, refused, failed, unknown int) string {
	var parts []string
	for _, part := range []struct {
		n    int
		word string
	}{{performed, "made"}, {refused, "refused"}, {failed, "failed"}, {unknown, "outcome not known"}} {
		if part.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.n, part.word))
		}
	}
	return strings.Join(parts, ", ")
}

// RenderMCPCalls is the MCP calls as `yoyo status` prints them under the four
// lines: nothing where the product has made none, and otherwise one line for
// the day, one per tool, and the act calls whose outcome is not known.
func (s Standing) RenderMCPCalls() string {
	if s.MCPCallsProblem != "" {
		return "\nMCP calls: could not be read: " + s.MCPCallsProblem + "\n"
	}
	if s.MCPCalls == nil {
		return ""
	}
	calls := *s.MCPCalls
	var text strings.Builder
	if calls.Calls == 0 {
		fmt.Fprintf(&text, "\nMCP calls in the last 24 hours: none; %d in the record; `yoyo mcp calls` lists them\n", calls.Recorded)
	} else {
		fmt.Fprintf(&text, "\nMCP calls in the last 24 hours: %d (%s); `yoyo mcp calls` lists them\n", calls.Calls, calls.Outcomes())
	}
	for _, tool := range calls.Tools {
		fmt.Fprintf(&text, "  %s (%s): %s\n", tool.Tool, tool.Class, tool.Outcomes())
	}
	if calls.NotOnList > 0 {
		fmt.Fprintf(&text, "  asked for a tool not on the caller's list: %d refused\n", calls.NotOnList)
	}
	if calls.UnknownActs > 0 {
		fmt.Fprintf(&text, "  %d act call(s) in the record may or may not have happened; check each before it is asked again (`yoyo mcp calls --unknown`)\n", calls.UnknownActs)
	}
	if calls.Unreadable > 0 {
		fmt.Fprintf(&text, "  %d line(s) of the record could not be read\n", calls.Unreadable)
	}
	return text.String()
}
