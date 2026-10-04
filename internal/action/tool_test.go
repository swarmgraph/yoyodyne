package action

import (
	"context"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

func TestRegistryRejectsInvalidToolDeclarations(t *testing.T) {
	t.Parallel()
	valid := Tool{ID: capability.WorkItemRead, Block: "yoyodyne-tracker", Class: ToolRead, Parameters: "typed read", Scope: "named item", Gates: "grant", Framing: "untrusted evidence", Bounds: ToolBounds{RequestsPerReply: 1, RoundsPerMessage: 2, BytesPerRequest: 100, BytesPerReply: 200}}
	for _, change := range []func(*Tool){
		func(t *Tool) { t.ID = "unknown" },
		func(t *Tool) { t.Block = "shell" },
		func(t *Tool) { t.Class = "command" },
		func(t *Tool) { t.Bounds.RequestsPerReply = 0 },
		func(t *Tool) { t.Bounds.BytesPerReply = 50 },
		func(t *Tool) { t.Parameters = "" },
		func(t *Tool) { t.Framing = "" },
	} {
		descriptor := valid
		change(&descriptor)
		_, err := New(Action[int]{Name: string(capability.WorkItemRead), Capabilities: []capability.Capability{capability.WorkItemRead}, Summary: "read", Wraps: "trustedRead", Perform: func(context.Context, int) error { return nil }, Tool: &descriptor})
		if err == nil {
			t.Fatalf("accepted invalid descriptor %#v", descriptor)
		}
	}
	_, err := New(Action[int]{Name: string(capability.WorkItemRead), Capabilities: []capability.Capability{capability.WorkItemRead}, Wraps: "trustedRead", Perform: func(context.Context, int) error { return nil }, Tool: &valid})
	if err != nil {
		t.Fatal(err)
	}
}

func TestToolDeclarationsCannotBeChangedThroughRegistryResults(t *testing.T) {
	t.Parallel()
	descriptor := Tool{ID: capability.LogRead, Block: "yoyodyne-log", Class: ToolRead, Parameters: "typed read", Scope: "state root", Gates: "grant", Framing: "evidence", Bounds: ToolBounds{RequestsPerReply: 1, RoundsPerMessage: 2, BytesPerRequest: 100, BytesPerReply: 100}}
	requires := []capability.Capability{capability.LogRead}
	registry, err := New(Action[int]{Name: "log.read", Capabilities: requires, Wraps: "trustedRead", Perform: func(context.Context, int) error { return nil }, Tool: &descriptor})
	if err != nil {
		t.Fatal(err)
	}
	descriptor.Bounds.BytesPerRequest = 0
	requires[0] = capability.WorktreeMutate
	for _, entry := range registry.Actions() {
		entry.Tool.Block = "shell"
		entry.Capabilities[0] = capability.WorktreeMutate
	}
	entry, _ := registry.Lookup("log.read")
	entry.Tool.Bounds.BytesPerReply = 0
	entry, _ = registry.Lookup("log.read")
	if entry.Tool.Block != "yoyodyne-log" || entry.Tool.Bounds.BytesPerRequest != 100 || entry.Tool.Bounds.BytesPerReply != 100 || entry.Capabilities[0] != capability.LogRead {
		t.Fatalf("registry declaration changed: %#v", entry)
	}
}
