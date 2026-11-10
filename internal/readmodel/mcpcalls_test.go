package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
)

type fakeMCPCalls struct {
	records    []mcpcall.Record
	unreadable int
	err        error
}

func (f fakeMCPCalls) Read() ([]mcpcall.Record, int, error) { return f.records, f.unreadable, f.err }

// mcpCallsCarryingContent are calls whose arguments, reasons, results and
// asked-for names all carry the word PRIVATE, which nothing a surface is
// handed may hold.
func mcpCallsCarryingContent(now time.Time) []mcpcall.Record {
	caller := mcpcall.CallerRecord{Kind: mcpcall.CallerConversation, Role: domain.RoleProductManager, Agent: "pm", Conversation: "c1", Turn: 2}
	return []mcpcall.Record{
		{Entry: "1", StartedAt: now.Add(-time.Hour), Caller: caller, Server: "mail", Tool: "mail.search", Class: config.MCPRead,
			Arguments: json.RawMessage(`{"query":"PRIVATE argument"}`), Why: "PRIVATE why", Outcome: mcpcall.OutcomePerformed,
			Result: "PRIVATE result", ResultBytes: 14, ResultDigest: "sha256:PRIVATE"},
		{Entry: "2", StartedAt: now.Add(-2 * time.Hour), Caller: caller, Server: "mail", Tool: "mail.search", Class: config.MCPRead,
			Outcome: mcpcall.OutcomeFailed, Reason: "PRIVATE server error text"},
		{Entry: "3", StartedAt: now.Add(-3 * time.Hour), Caller: caller, Server: "mail", Tool: "mail.send", Class: config.MCPAct, ID: "a1",
			Arguments: json.RawMessage(`{"to":"PRIVATE address"}`), Outcome: mcpcall.OutcomeUnknown, Reason: "PRIVATE"},
		{Entry: "4", StartedAt: now.Add(-4 * time.Hour), Caller: caller, Tool: "PRIVATE-name-the-agent-wrote",
			Outcome: mcpcall.OutcomeRefused, Refusal: mcpcall.RefusedNotOnList},
		// Older than the day: counted in the record, and as an unknown act.
		{Entry: "5", StartedAt: now.Add(-48 * time.Hour), Caller: caller, Server: "mail", Tool: "mail.send", Class: config.MCPAct, ID: "a0",
			Outcome: mcpcall.OutcomeUnknown, Result: "PRIVATE old result"},
	}
}

// The day's calls are counted by tool and outcome, an older unknown act is
// still named, and a call the agent asked for by a name not on its list is
// counted without that name.
func TestMCPCallsAreCountedByToolAndOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	calls, problem := ReadMCPCalls(fakeMCPCalls{records: mcpCallsCarryingContent(now), unreadable: 1}, now)
	if problem != "" || calls == nil {
		t.Fatalf("ReadMCPCalls() = %+v, %q", calls, problem)
	}
	if calls.Calls != 4 || calls.Performed != 1 || calls.Failed != 1 || calls.Unknown != 1 || calls.Refused != 1 ||
		calls.NotOnList != 1 || calls.Recorded != 5 || calls.UnknownActs != 2 || calls.Unreadable != 1 {
		t.Fatalf("counts = %+v", calls)
	}
	if len(calls.Tools) != 2 || calls.Tools[0].Tool != "mail.search" || calls.Tools[0].Calls != 2 || calls.Tools[1].Tool != "mail.send" ||
		calls.Tools[1].Unknown != 1 || !calls.Tools[0].Latest.Equal(now.Add(-time.Hour)) {
		t.Fatalf("tools = %+v", calls.Tools)
	}
	rendered := Standing{MCPCalls: calls}.RenderMCPCalls()
	for _, want := range []string{
		"MCP calls in the last 24 hours: 4 (1 made, 1 refused, 1 failed, 1 outcome not known)",
		"  mail.search (read): 1 made, 1 failed\n",
		"  mail.send (act): 1 outcome not known\n",
		"asked for a tool not on the caller's list: 1 refused",
		"2 act call(s) in the record may or may not have happened",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderMCPCalls() does not say %q:\n%s", want, rendered)
		}
	}
}

// A product with no record, or an empty one, shows nothing at all, so a
// project that declares no MCP server reads exactly as before; a record that
// cannot be read says so.
func TestNoMCPCallsShowNothingAndAnUnreadableRecordSaysSo(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, record := range []MCPCallRecord{nil, fakeMCPCalls{}} {
		calls, problem := ReadMCPCalls(record, now)
		if calls != nil || problem != "" {
			t.Fatalf("ReadMCPCalls(%v) = %+v, %q; want nothing", record, calls, problem)
		}
	}
	if rendered := (Standing{}).RenderMCPCalls(); rendered != "" {
		t.Fatalf("a standing with no calls renders %q", rendered)
	}
	_, problem := ReadMCPCalls(fakeMCPCalls{err: errors.New("permission denied")}, now)
	if !strings.Contains(problem, "could not be read: permission denied") {
		t.Fatalf("problem = %q", problem)
	}
	if rendered := (Standing{MCPCallsProblem: problem}).RenderMCPCalls(); !strings.Contains(rendered, "MCP calls: could not be read") {
		t.Fatalf("an unreadable record renders %q", rendered)
	}
}

// No content of a call reaches a surface. The standing is what `yoyo status`
// prints, what the dashboard is served as JSON, and what the Slack heartbeat
// renders its lines from, so a call's arguments, its reasons, its result and
// the name an agent asked for appear in none of them. The projection's own
// types carry no field such text could be put in, so a later field holding it
// is a change this test is shown failing on.
func TestNoMCPCallContentReachesAnySurface(t *testing.T) {
	t.Parallel()
	now := time.Now()
	sources := quietSources()
	sources.Now = func() time.Time { return now }
	sources.MCPCalls = fakeMCPCalls{records: mcpCallsCarryingContent(now)}
	standing := ReadStanding(context.Background(), sources)
	if standing.MCPCalls == nil || standing.MCPCalls.Calls != 4 {
		t.Fatalf("the standing carries %+v, want the day's four calls", standing.MCPCalls)
	}
	encoded, err := json.Marshal(standing)
	if err != nil {
		t.Fatal(err)
	}
	for surface, text := range map[string]string{
		"the standing as the dashboard is served it": string(encoded),
		"yoyo status":                 standing.Render() + standing.RenderMCPCalls(),
		"the Slack heartbeat's lines": standing.RenderBrief(),
	} {
		if strings.Contains(text, "PRIVATE") {
			t.Errorf("%s carries an MCP call's content:\n%s", surface, text)
		}
	}
	for _, kind := range []reflect.Type{reflect.TypeOf(MCPCalls{}), reflect.TypeOf(MCPToolCalls{})} {
		for i := range kind.NumField() {
			field := kind.Field(i)
			if field.Type.Kind() == reflect.String && field.Name != "Tool" && field.Type != reflect.TypeOf(config.MCPRead) {
				t.Errorf("%s.%s is text, and the projection carries no text but the declared tool's name and its class", kind.Name(), field.Name)
			}
		}
	}
}
