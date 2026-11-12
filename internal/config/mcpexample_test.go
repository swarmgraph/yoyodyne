package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// mcpExample is the worked example the operator guide ships
// (docs/mcp.md): an example to copy and adapt, which nothing loads as a
// default.
var mcpExample = filepath.Join("..", "..", "docs", "examples", "mcp.yaml")

// mappingValue is the value under key in a YAML mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// The worked example loads the way a project that copied it would: its mcp
// section and each agent's mcp_tools line are put into the configuration
// `yoyo init` writes, with a program manager instance beside the five agents
// init configures, and the result is read through Load like any project's
// file. A change to the configuration that refuses the example fails here,
// and so does an example that stops naming a list for each role.
func TestTheMCPWorkedExampleLoadsIntoAnInitProject(t *testing.T) {
	t.Parallel()

	example, err := os.ReadFile(mcpExample)
	if err != nil {
		t.Fatalf("read the worked example: %v", err)
	}
	if !strings.HasPrefix(string(example), "# AN EXAMPLE TO COPY AND ADAPT. Yoyodyne never loads this file.") {
		t.Fatalf("the worked example does not open by saying it is an example to copy and adapt")
	}

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	// Not a default: what init writes declares no server and no agent's list.
	if bytes.Contains(scaffold.Config.Content, []byte("\nmcp:")) || bytes.Contains(scaffold.Config.Content, []byte("mcp_tools")) {
		t.Fatalf("the configuration init writes carries MCP settings:\n%s", scaffold.Config.Content)
	}
	directory := filepath.Join(t.TempDir(), ".yoyodyne")
	for _, file := range scaffold.Files() {
		target := filepath.Join(directory, file.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, file.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(directory, "remits"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "remits", "mail.md"), []byte(laneRemit), 0o644); err != nil {
		t.Fatal(err)
	}

	var project, copied yaml.Node
	if err := yaml.Unmarshal(scaffold.Config.Content, &project); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(example, &copied); err != nil {
		t.Fatalf("the worked example is not YAML: %v", err)
	}
	root, from := project.Content[0], copied.Content[0]
	agents := mappingValue(root, "agents")
	var instance yaml.Node
	if err := yaml.Unmarshal([]byte(strings.ReplaceAll(programManagerAgent("program-manager", "mail", "remits/mail.md"), "\n  ", "\n")[2:]), &instance); err != nil {
		t.Fatal(err)
	}
	agents.Content = append(agents.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "program-manager"}, instance.Content[0].Content[1])

	roles := map[domain.AgentRole]bool{}
	listed := mappingValue(from, "agents")
	if listed == nil {
		t.Fatal("the worked example names no agent's list")
	}
	for i := 0; i+1 < len(listed.Content); i += 2 {
		name, block := listed.Content[i].Value, listed.Content[i+1]
		agent := mappingValue(agents, name)
		if agent == nil {
			t.Fatalf("the worked example gives a list to agent %q, which the project init writes has no agent of", name)
		}
		if len(block.Content) != 2 || block.Content[0].Value != "mcp_tools" {
			t.Fatalf("the worked example's block for %q carries more than its mcp_tools line", name)
		}
		agent.Content = append(agent.Content, block.Content...)
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "mcp"}, mappingValue(from, "mcp"))
	merged, err := yaml.Marshal(&project)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, FileName)
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the worked example does not load into a project init wrote: %v", err)
	}
	if server, ok := cfg.MCP.Servers["mail"]; !ok || len(server.Tools) == 0 {
		t.Fatalf("the loaded configuration has no mail server: %+v", cfg.MCP)
	}
	for i := 0; i+1 < len(listed.Content); i += 2 {
		name := listed.Content[i].Value
		role := cfg.Agents[name].Role
		roles[role] = true
		for _, tool := range cfg.MCPToolsFor(name) {
			if !MCPRoleHolds(role, tool.Class) {
				t.Errorf("agent %q, a %s, was loaded holding %s, a tool its role may not hold", name, role, tool.Name())
			}
		}
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProgramManager} {
		if !roles[role] {
			t.Errorf("the worked example gives no list to an agent of the %s role", role)
		}
	}
	if len(cfg.MCPToolsFor("product-manager")) == 0 || len(cfg.MCPToolsFor("reviewer")) != 0 {
		t.Fatalf("the loaded lists are product-manager %v and reviewer %v", cfg.MCPToolsFor("product-manager"), cfg.MCPToolsFor("reviewer"))
	}
}
