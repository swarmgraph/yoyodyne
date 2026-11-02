package backend

import "testing"

// A call reported more than once is one call, a refusal is never overwritten
// by a later result, a refusal for a call whose start was not seen is still a
// call, and a limit said twice is said once.
func TestToolCallLogKeepsOneCallPerIdentity(t *testing.T) {
	t.Parallel()
	var log ToolCallLog
	log.Start("a", "Bash", "", "")
	log.Start("a", "Bash", "", "")
	log.Finish("a", ToolExecuted)
	log.Start("b", "Write", "", "parent")
	log.Deny("b", "Write")
	log.Finish("b", ToolExecuted)
	log.Deny("c", "WebFetch")
	log.Finish("missing", ToolExecuted)
	log.Finish("missing", ToolExecuted)
	want := []ToolCall{
		{ID: "a", Tool: "Bash", Kind: ToolKindShell, Outcome: ToolExecuted},
		{ID: "b", Tool: "Write", Kind: ToolKindWrite, Parent: "parent", Outcome: ToolDenied},
		{ID: "c", Tool: "WebFetch", Kind: ToolKindWeb, Outcome: ToolDenied},
	}
	calls := log.Calls()
	if len(calls) != len(want) {
		t.Fatalf("Calls() = %#v, want %#v", calls, want)
	}
	for index := range want {
		if calls[index] != want[index] {
			t.Errorf("Calls()[%d] = %#v, want %#v", index, calls[index], want[index])
		}
	}
	if gaps := log.Gaps(); len(gaps) != 1 {
		t.Fatalf("Gaps() = %q, want the orphan result said once", gaps)
	}
}

func TestToolPolicyPermits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		policy ToolPolicy
		kind   ToolKind
		agents bool
		want   bool
	}{
		{ToolPolicyNone, ToolKindRead, false, false},
		{ToolPolicyInspection, ToolKindRead, false, true},
		{ToolPolicyInspection, ToolKindShell, false, true},
		{ToolPolicyInspection, ToolKindWrite, false, false},
		{ToolPolicyInspection, ToolKindSession, false, true},
		{ToolPolicyDeveloper, ToolKindWrite, false, true},
		{ToolPolicyDeveloper, ToolKindSession, false, true},
		{ToolPolicyDeveloper, ToolKindAgent, false, false},
		{ToolPolicyDeveloper, ToolKindAgent, true, true},
		{ToolPolicyDeveloper, ToolKindWeb, false, false},
		{ToolPolicyDeveloper, ToolKindExternal, false, false},
		{ToolPolicyDeveloper, ToolKindUnknown, false, false},
		{"", ToolKindRead, false, false},
	} {
		if got := tc.policy.Permits(tc.kind, tc.agents); got != tc.want {
			t.Errorf("%q.Permits(%q, %v) = %v, want %v", tc.policy, tc.kind, tc.agents, got, tc.want)
		}
	}
	if ClassifyTool("mcp__anchor__listTasks") != ToolKindExternal || ClassifyTool("SomethingNew") != ToolKindUnknown {
		t.Fatal("ClassifyTool does not separate MCP tools from tools nobody described")
	}
}
