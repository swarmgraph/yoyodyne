package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// mcpProject is the bootstrap configuration with every role an MCP list can be
// refused for, and the design's example section.
const mcpProject = validBootstrapConfig + `  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
    instances: 1
  pm:
    role: product-manager
    backend: claude-code
    model: opus
    instances: 1
mcp:
  servers:
    mail:
      command: [mail-mcp, --stdio]
      login: [mail-mcp, login]
      tools:
        search: read
        fetch: read
        send: act
    tickets:
      url: https://mcp.example.com/
      credential: file
      tools:
        list_open: read
`

func withTools(agent, tools string) string {
	return strings.Replace(mcpProject, "  "+agent+":\n", "  "+agent+":\n    mcp_tools: "+tools+"\n", 1)
}

// A project with no mcp section has no servers, no agent lists, and a
// configuration that serializes and digests exactly as it did before the keys
// existed.
func TestAProjectWithNoMCPSectionIsUnchanged(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.MCP.Declared() || len(cfg.Agents["developers"].MCPTools) != 0 || cfg.MCPToolsFor("developers") != nil {
		t.Fatalf("a file with no mcp section has %#v and %v", cfg.MCP, cfg.Agents["developers"].MCPTools)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The capability names mcp.read and mcp.act are in a resolved agent's
	// capabilities whatever the file says; what must not appear is a key.
	if strings.Contains(string(encoded), `"mcp"`) || strings.Contains(string(encoded), `"mcp_tools"`) {
		t.Fatalf("a configuration with no mcp section serializes an mcp key: %s", encoded)
	}
	empty, err := Decode(strings.NewReader(validBootstrapConfig + "mcp:\n  servers: {}\n"))
	if err != nil {
		t.Fatalf("Decode() of an empty section error = %v", err)
	}
	if empty.Revision() != cfg.Revision() {
		t.Fatalf("an empty mcp section moved the revision from %s to %s", cfg.Revision(), empty.Revision())
	}
}

func TestTheMCPSectionAndAnAgentsListLoad(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(withTools("pm", "[mail.search, mail.send, tickets.list_open]")))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	tools := cfg.MCPToolsFor("pm")
	if len(tools) != 3 || tools[1].Name() != "mail.send" || tools[1].Class != MCPAct || tools[2].Server != "tickets" {
		t.Fatalf("MCPToolsFor(pm) = %#v", tools)
	}
	if cfg.MCP.Servers["tickets"].Credential != MCPCredentialFile || cfg.MCP.Servers["mail"].Login[1] != "login" {
		t.Fatalf("servers = %#v", cfg.MCP.Servers)
	}
	if got := cfg.MCPToolsFor("developers"); got != nil {
		t.Fatalf("an agent naming no tools holds %v, want none", got)
	}
}

// A developer may hold read tools.
func TestADeveloperMayHoldReadTools(t *testing.T) {
	t.Parallel()
	if _, err := Decode(strings.NewReader(withTools("developers", "[mail.search, tickets.list_open]"))); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
}

// Every list that names something undeclared, a wildcard, or a class the
// agent's role does not hold is refused at load, naming the agent, the tool and
// why.
func TestAnAgentsListIsRefusedNamingTheAgentTheToolAndWhy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, agent, tools string
		want               []string
	}{
		{"a developer given an act tool", "developers", "[mail.send]", []string{`agent "developers"`, `"mail.send"`, "an act tool", "developer role", "before review"}},
		{"a reviewer given an act tool", "reviewer", "[mail.send]", []string{`agent "reviewer"`, `"mail.send"`, "reviewer role holds no MCP tools"}},
		{"a reviewer given a read tool", "reviewer", "[mail.search]", []string{`agent "reviewer"`, `"mail.search"`, "a read tool", "holds no MCP tools"}},
		{"an undeclared server", "pm", "[calendar.list]", []string{`agent "pm"`, `"calendar.list"`, `declares no server "calendar"`}},
		{"an undeclared tool", "pm", "[mail.delete]", []string{`agent "pm"`, `"mail.delete"`, `does not name "delete"`}},
		{"a wildcard tool", "pm", `["mail.*"]`, []string{`agent "pm"`, `"mail.*"`, "no wildcard"}},
		{"a wildcard server", "pm", `["*.search"]`, []string{`"*.search"`, "no wildcard"}},
		{"a name with no server", "pm", "[search]", []string{`"search"`, "server.tool"}},
		{"a name twice", "pm", "[mail.search, mail.search]", []string{`"mail.search" twice`}},
	} {
		_, err := Decode(strings.NewReader(withTools(test.agent, test.tools)))
		if err == nil {
			t.Errorf("%s: Decode() = nil, want it refused", test.name)
			continue
		}
		for _, want := range test.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: Decode() error = %v, want it to say %q", test.name, err, want)
			}
		}
	}
}

func TestTheRolesThatMayHoldEachClassAreFixedInGo(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		role      domain.AgentRole
		read, act bool
	}{
		{domain.RoleProductManager, true, true},
		{domain.RoleArchitect, true, true},
		{domain.RoleDevelopmentManager, true, true},
		{domain.RoleProgramManager, true, false},
		{domain.RoleDeveloper, true, false},
		{domain.RoleReviewer, false, false},
	} {
		if MCPRoleHolds(test.role, MCPRead) != test.read || MCPRoleHolds(test.role, MCPAct) != test.act {
			t.Errorf("%s holds read=%v act=%v, want read=%v act=%v", test.role, MCPRoleHolds(test.role, MCPRead), MCPRoleHolds(test.role, MCPAct), test.read, test.act)
		}
	}
}

func TestAServerDeclarationIsHeldToItsRules(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ input, want string }{
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      url: https://h/\n      tools: {t: read}\n", "sets both command and url"},
		{"mcp:\n  servers:\n    x:\n      tools: {t: read}\n", "sets neither command nor url"},
		{"mcp:\n  servers:\n    x:\n      url: http://h/\n      tools: {t: read}\n", "must be an https address"},
		{"mcp:\n  servers:\n    x:\n      url: https://user:pw@h/\n      tools: {t: read}\n", "carries a user or password"},
		{"mcp:\n  servers:\n    x:\n      url: https:///path\n      tools: {t: read}\n", "names no host"},
		{"mcp:\n  servers:\n    x:\n      url: https://h/\n      credential: /etc/passwd\n      tools: {t: read}\n", "never a path"},
		{"mcp:\n  servers:\n    x:\n      url: https://h/\n      credential: file\n      credential_env: TOKEN\n      tools: {t: read}\n", "only a server the harness starts"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      credential: keychain\n      tools: {t: read}\n", "no credential_env"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      credential_env: TOKEN\n      tools: {t: read}\n", "names no credential"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      credential: file\n      credential_env: HOME\n      tools: {t: read}\n", "the harness sets itself"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      credential: file\n      credential_env: lower\n      tools: {t: read}\n", "in capitals"},
		{"mcp:\n  servers:\n    x:\n      command: ['']\n      tools: {t: read}\n", "must name a program first"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      login: []\n      tools: {t: read}\n", ""},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n", "names no tools"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      tools: {t: write}\n", `each tool is "read"`},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      tools: {'*': read}\n", "no wildcard"},
		{"mcp:\n  servers:\n    Bad_Name:\n      command: [a]\n      tools: {t: read}\n", "mcp server name"},
		{"mcp:\n  servers:\n    x:\n      command: [a]\n      path: /tmp\n      tools: {t: read}\n", "field path not found"},
	} {
		_, err := Decode(strings.NewReader(validBootstrapConfig + test.input))
		if test.want == "" {
			if err != nil {
				t.Errorf("Decode(%q) error = %v, want it to load", test.input, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("Decode(%q) error = %v, want one saying %q", test.input, err, test.want)
		}
	}
}

// The mcp section and an agent's list each record the layer they came from, as
// every other effective value does, so `config show --origins` can say so.
func TestTheMCPSectionAndAnAgentsListRecordTheirOrigin(t *testing.T) {
	t.Parallel()

	base := withTools("pm", "[mail.search, mail.fetch]")
	resolved, err := DecodeResolved(strings.NewReader(base))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if got := resolved.Origins["mcp"]; got == "" {
		t.Fatalf("the mcp section has no origin: %v", resolved.Origins)
	}
	if got := resolved.Origins["agents.pm.mcp_tools"]; got == "" {
		t.Fatalf("the agent's list has no origin: %v", resolved.Origins)
	}
}

// Neither key is a role's to write.
func TestARoleMayNotApplyTheMCPSectionOrAnAgentsList(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ key, want string }{
		{"mcp", "program the harness runs"},
		{"mcp.servers.mail.tools.send", "program the harness runs"},
		{"agents.pm.mcp_tools", "which MCP tools an agent may ask"},
		{"agents.developer.mcp_tools.[]", "which MCP tools an agent may ask"},
	} {
		err := RoleMayApply(test.key)
		if err == nil || !strings.Contains(err.Error(), "stays the operator's") || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), test.key) {
			t.Errorf("RoleMayApply(%q) = %v, want it refused as the operator's saying %q", test.key, err, test.want)
		}
	}
}
