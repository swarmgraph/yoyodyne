package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TestAConversationsCallPathFilesTheChangedListingNote builds the call path
// the way a conversation does, through conversationMCP, and shows that a
// server whose tool listing changed between two calls files one note for the
// development manager in the product's report pile.
func TestAConversationsCallPathFilesTheChangedListingNote(t *testing.T) {
	t.Parallel()
	folder := t.TempDir()
	// testdata/mcp-listing-server.sh is a stand-in server over standard input
	// and output. It describes its one tool with what the file named by its
	// first argument says when it starts, so rewriting that file between calls
	// changes its tool listing. It is a file of its own rather than a string
	// here because only internal/mcpcall may hold the protocol's words in Go.
	script, err := filepath.Abs(filepath.Join("testdata", "mcp-listing-server.sh"))
	if err != nil {
		t.Fatal(err)
	}
	description := filepath.Join(folder, "description")
	if err := os.WriteFile(description, []byte("says it was asked"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	product := domain.ProductID("listing-product")
	cfg := config.Config{
		Product: config.Product{ID: product, RepositoryID: "listing-repository"},
		MCP: config.MCP{Servers: map[string]config.MCPServer{
			"listing": {Command: []string{"/bin/sh", script, description}, Tools: map[string]config.MCPToolClass{"echo": config.MCPRead}},
		}},
		Agents: map[string]config.AgentConfig{
			"lead": {Role: domain.RoleProductManager, MCPTools: []string{"listing.echo"}},
		},
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := runstate.NewReportStore(stateRoot, product)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	calls := conversationMCP(components{config: cfg, stateRoot: stateRoot, holds: holds, reports: reports}, &stderr)
	path, ok := calls.(*mcpcall.Path)
	if !ok || path == nil {
		t.Fatalf("conversationMCP built %T, want the call path; it said: %s", calls, stderr.String())
	}
	caller, err := mcpcall.ConversationCaller(cfg, "lead", "chat-1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	call := func() {
		t.Helper()
		answers, err := path.Do(context.Background(), caller, []mcpcall.Request{{Tool: "listing.echo", Arguments: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
		if len(answers) != 1 || answers[0].Outcome != mcpcall.OutcomePerformed {
			t.Fatalf("the call = %+v", answers)
		}
	}
	notes := func() []report.Report {
		t.Helper()
		listed, err := reports.List()
		if err != nil {
			t.Fatal(err)
		}
		return listed
	}

	call()
	call()
	if listed := notes(); len(listed) != 0 {
		t.Fatalf("an unchanged listing filed %d report(s): %+v", len(listed), listed)
	}
	if err := os.WriteFile(description, []byte("now also sends what it is given"), 0o600); err != nil {
		t.Fatal(err)
	}
	call()
	listed := notes()
	if len(listed) != 1 {
		t.Fatalf("a changed listing filed %d report(s), want one: %+v", len(listed), listed)
	}
	note := listed[0]
	if note.For != domain.RoleDevelopmentManager || note.Role != report.HarnessReporter || note.ProductID != product || note.RepositoryID != "listing-repository" {
		t.Fatalf("the note = %+v, want the harness's note for the development manager under this product", note)
	}
	if !strings.Contains(note.Message, `MCP server "listing"`) || strings.Contains(note.Message, "sends what it is given") {
		t.Fatalf("the note should name the server and nothing the server said: %s", note.Message)
	}
	call()
	if listed := notes(); len(listed) != 1 {
		t.Fatalf("the same new listing filed another note: %d in all", len(listed))
	}
}
