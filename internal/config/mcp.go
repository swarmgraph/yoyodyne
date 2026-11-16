package config

// The MCP servers a project declares, the tools of each it will ever use, and
// the exact tools each agent may ask the harness to call
// (docs/designs/mcp-tools-through-the-harness.md, section 1).
//
// Nothing here makes a call, and nothing here hands a server to an agent: every
// provider adapter launches every role with no MCP server whatever this section
// says, and the only package that will speak MCP is internal/mcpcall. What this
// file settles is what loads. A project that says nothing has no servers and no
// agent lists, which is every project written before the key existed.
//
// Configuration names and narrows here and never grants. Which class of tool a
// role may hold at all is fixed in Go below; a list naming a tool whose class
// the agent's role does not hold is refused as the file loads, so no line of
// configuration can give a developer or a reviewer a tool that acts.

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// MCP is the project's MCP section: the servers it declares, by name.
type MCP struct {
	Servers map[string]MCPServer `yaml:"servers,omitempty" json:"servers,omitempty"`
}

// Declared reports whether the project declares any MCP server at all.
func (m MCP) Declared() bool { return len(m.Servers) > 0 }

// MCPServer is one server: a local program the harness starts, or a remote
// https address, and every tool of it the project will use.
type MCPServer struct {
	// Command is the program and its arguments, for a server the harness starts
	// on this machine. Exactly one of Command and URL is set.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// URL is the https address of a remote server.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// Login is the program `yoyo mcp login <server>` runs at the operator's
	// terminal, in the server's own directory under the state directory. It is
	// optional: a server whose credential is stored by hand needs none.
	Login []string `yaml:"login,omitempty" json:"login,omitempty"`
	// Credential names the store the server's credential is read from, and never
	// a path: see MCPCredentialStore.
	Credential MCPCredentialStore `yaml:"credential,omitempty" json:"credential,omitempty"`
	// CredentialEnv is the environment variable a command server is handed its
	// credential in. It is a command server's alone, and required where one names
	// a credential, because a credential the program cannot find is one nobody
	// can use.
	CredentialEnv string `yaml:"credential_env,omitempty" json:"credential_env,omitempty"`
	// Tools is every tool of this server the project will ever use, each marked
	// read or act by the project. The class is the project's statement rather
	// than the server's, because what a server says about its own tools is
	// untrusted, and a tool this list does not name cannot be called by anyone.
	Tools map[string]MCPToolClass `yaml:"tools,omitempty" json:"tools,omitempty"`
	// Connector is set in place of Command and URL for the one server the
	// harness does not call itself: Slack, reached through the Slack connection
	// on the operator's own Claude account. See MCPConnector.
	Connector *MCPConnector `yaml:"connector,omitempty" json:"connector,omitempty"`
}

// MCPConnectorSlack is the one service a connector may be for.
const MCPConnectorSlack = "slack"

// MCPConnectorDefaultTimeLimit is how long a carrying session may run when the
// configuration names no limit, and MCPConnectorMaxTimeLimit the longest it may
// be given.
const (
	MCPConnectorDefaultTimeLimit = 2 * time.Minute
	MCPConnectorMaxTimeLimit     = 10 * time.Minute
)

// MCPConnector is how the harness reaches a service through the Claude account
// the operator signs in with, when it holds no token of its own: for each call
// it starts one Claude session that has only the one connector tool the call
// names (docs/designs/mcp-tools-through-the-harness.md, section 8). It carries
// what the project chooses and never what a session may do: the session's
// limits are fixed in internal/mcpcall and no key here loosens one.
type MCPConnector struct {
	// Service is the service reached. Only "slack" is accepted.
	Service string `yaml:"service" json:"service"`
	// ToolPrefix is the front of the name the Claude session gives a tool of
	// this connector; the tool's own name is added to it. Empty is the name
	// Claude gives the account's Slack connection.
	ToolPrefix string `yaml:"tool_prefix,omitempty" json:"tool_prefix,omitempty"`
	// TimeLimit is how long one carrying session may run before it is ended and
	// the call is recorded as failed. Empty is two minutes; at most ten.
	TimeLimit Duration `yaml:"time_limit,omitempty" json:"time_limit,omitempty"`
	// Messages are the posts a role may ask for, by name. A post is the
	// message's fixed text and the links the role supplies, and nothing else.
	Messages map[string]MCPConnectorMessage `yaml:"messages,omitempty" json:"messages,omitempty"`
	// LinkForms are the forms a link in a post may take: each an https address
	// ending where a link must begin, such as "https://github.com/acme/". A link
	// is allowed when it starts with one of them.
	LinkForms []string `yaml:"link_forms,omitempty" json:"link_forms,omitempty"`
	// ChannelArgument and TextArgument are the names the post tool gives the
	// channel and the text in its arguments. Empty is channel_id and message.
	ChannelArgument string `yaml:"channel_argument,omitempty" json:"channel_argument,omitempty"`
	TextArgument    string `yaml:"text_argument,omitempty" json:"text_argument,omitempty"`
}

// MCPConnectorMessage is one post the project allows: where it goes and the
// words it says. The harness, not a role and not the session, puts the final
// text together from these words and the checked links.
type MCPConnectorMessage struct {
	Channel string `yaml:"channel" json:"channel"`
	Text    string `yaml:"text" json:"text"`
}

// TimeLimitOrDefault is the limit one carrying session runs under.
func (c MCPConnector) TimeLimitOrDefault() time.Duration {
	if c.TimeLimit.Duration() > 0 {
		return c.TimeLimit.Duration()
	}
	return MCPConnectorDefaultTimeLimit
}

// ChannelKey and TextKey are the argument names a post's channel and text go
// under.
func (c MCPConnector) ChannelKey() string {
	if c.ChannelArgument != "" {
		return c.ChannelArgument
	}
	return "channel_id"
}

func (c MCPConnector) TextKey() string {
	if c.TextArgument != "" {
		return c.TextArgument
	}
	return "message"
}

// SessionToolName is the name the carrying session knows a tool of this
// connector by.
func (c MCPConnector) SessionToolName(tool string) string {
	prefix := c.ToolPrefix
	if prefix == "" {
		prefix = "mcp__claude_ai_Slack__"
	}
	return prefix + tool
}

// MCPToolClass is whether a tool changes anything outside the harness.
type MCPToolClass string

const (
	// MCPRead is a tool whose call changes nothing outside the harness.
	MCPRead MCPToolClass = "read"
	// MCPAct is a tool whose call may change something outside the harness.
	MCPAct MCPToolClass = "act"
)

// MCPCredentialStore is where a server's credential is kept on this machine.
// It is a closed choice of two stores rather than a path, because one line of
// configuration naming a free path could send any file on the machine to a
// remote server. The names the two stores use are derived from the product and
// the server's name alone, in internal/mcpcredential.
type MCPCredentialStore string

const (
	// MCPCredentialNone is a server that needs no credential.
	MCPCredentialNone MCPCredentialStore = ""
	// MCPCredentialKeychain is the macOS keychain.
	MCPCredentialKeychain MCPCredentialStore = "keychain"
	// MCPCredentialFile is a file under the product's state directory, readable
	// only by the user the harness runs as.
	MCPCredentialFile MCPCredentialStore = "file"
)

// mcpRoleClasses is which class of tool each role may hold, held in Go so no
// configuration can widen it. A developer holds read tools only, because an act
// taken during a run happens before review and could not be undone if review
// refused the change; a reviewer holds none, so its judgement stays its own and
// repeats nothing. A role missing from this table holds neither. An agent
// filling a role definition is held to the shipped role it extends.
var mcpRoleClasses = map[domain.AgentRole][]MCPToolClass{
	domain.RoleProductManager:     {MCPRead, MCPAct},
	domain.RoleArchitect:          {MCPRead, MCPAct},
	domain.RoleDevelopmentManager: {MCPRead, MCPAct},
	domain.RoleProgramManager:     {MCPRead},
	domain.RoleDeveloper:          {MCPRead},
}

// MCPRoleHolds reports whether an agent of role may hold a tool of class.
func MCPRoleHolds(role domain.AgentRole, class MCPToolClass) bool {
	for _, held := range mcpRoleClasses[role] {
		if held == class {
			return true
		}
	}
	return false
}

// MCPTool is one tool an agent may ask for, resolved against the declaration.
type MCPTool struct {
	Server string
	Tool   string
	Class  MCPToolClass
}

// Name is the tool as an agent's list writes it: server and tool.
func (t MCPTool) Name() string { return t.Server + "." + t.Tool }

// MCPToolsFor is the tools one agent's list names, resolved against the mcp
// section, in the order the list gives them. A name the section does not
// declare is left out; a configuration that loaded has none.
func (c Config) MCPToolsFor(agent string) []MCPTool {
	configured, ok := c.Agents[agent]
	if !ok {
		return nil
	}
	var tools []MCPTool
	for _, name := range configured.MCPTools {
		server, tool, _ := strings.Cut(name, ".")
		class, declared := c.MCP.Servers[server].Tools[tool]
		if !declared {
			continue
		}
		tools = append(tools, MCPTool{Server: server, Tool: tool, Class: class})
	}
	return tools
}

// mcpToolNamePattern is the shape of a tool's name as MCP writes one: letters,
// digits, underscores, hyphens and dots, at most 128 of them. It admits no
// wildcard, so a list can only ever name tools one by one.
var mcpToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// mcpEnvironmentPattern is the shape of a variable a credential is handed in.
var mcpEnvironmentPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// mcpReservedEnvironment is the variables a credential may not be handed in,
// because the harness sets them itself for every server it starts.
var mcpReservedEnvironment = map[string]bool{"HOME": true, "PATH": true}

// MCPMaxRedirects is how many redirects a remote server's address may follow,
// all on its own host. It is stated here, beside the rule that the address is
// https, because both are what the configuration's url is held to.
const MCPMaxRedirects = 5

// mcpProblems reports everything wrong with the mcp section and with each
// agent's list against it, at once.
func (c Config) mcpProblems() []string {
	var problems []string
	for _, name := range sortedServerNames(c.MCP.Servers) {
		problems = append(problems, c.MCP.Servers[name].problems(name)...)
	}
	for _, name := range sortedNames(c.Agents) {
		problems = append(problems, c.mcpToolProblems(name, c.Agents[name])...)
	}
	return problems
}

func (s MCPServer) problems(name string) []string {
	var problems []string
	if err := domain.ValidateIdentifier("mcp server name", name); err != nil {
		// The name becomes a keychain item and a file name, so a name that could
		// carry a separator is refused before anything is derived from it.
		problems = append(problems, err.Error())
	}
	key := "mcp.servers." + name
	hasCommand, hasURL := len(s.Command) > 0, strings.TrimSpace(s.URL) != ""
	switch {
	case s.Connector != nil:
		problems = append(problems, s.connectorProblems(key, hasCommand, hasURL)...)
	case hasCommand && hasURL:
		problems = append(problems, fmt.Sprintf("%s sets both command and url; a server is one or the other", key))
	case !hasCommand && !hasURL:
		problems = append(problems, fmt.Sprintf("%s sets neither command nor url; a server is a program the harness starts or an https address", key))
	case hasCommand:
		problems = append(problems, mcpCommandProblems(key+".command", s.Command)...)
	default:
		problems = append(problems, mcpURLProblems(key+".url", s.URL)...)
	}
	if s.Login != nil {
		problems = append(problems, mcpCommandProblems(key+".login", s.Login)...)
	}
	switch s.Credential {
	case MCPCredentialNone, MCPCredentialKeychain, MCPCredentialFile:
	default:
		problems = append(problems, fmt.Sprintf("%s.credential is %q; it names a store, %q or %q, and never a path", key, s.Credential, MCPCredentialKeychain, MCPCredentialFile))
	}
	switch env := s.CredentialEnv; {
	case env == "" && hasCommand && s.Credential != MCPCredentialNone:
		problems = append(problems, fmt.Sprintf("%s names a credential and no credential_env, so the program it starts could not be handed it", key))
	case env == "":
	case hasURL:
		problems = append(problems, fmt.Sprintf("%s.credential_env is set on a server with a url; only a server the harness starts is handed its credential in a variable", key))
	case s.Credential == MCPCredentialNone:
		problems = append(problems, fmt.Sprintf("%s.credential_env is set and the server names no credential to hand over in it", key))
	case !mcpEnvironmentPattern.MatchString(env):
		problems = append(problems, fmt.Sprintf("%s.credential_env %q must be an environment variable name in capitals, digits and underscores", key, env))
	case mcpReservedEnvironment[env] || strings.HasPrefix(env, "YOYODYNE_"):
		problems = append(problems, fmt.Sprintf("%s.credential_env %q is a variable the harness sets itself", key, env))
	}
	if s.Connector != nil && s.Connector.Service == MCPConnectorSlack {
		problems = append(problems, s.Connector.messageProblems(key, s.Tools)...)
	}
	if len(s.Tools) == 0 {
		problems = append(problems, fmt.Sprintf("%s names no tools, so nothing on it could ever be called; name each tool the project uses as %s or %s", key, MCPRead, MCPAct))
	}
	tools := make([]string, 0, len(s.Tools))
	for tool := range s.Tools {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		if !mcpToolNamePattern.MatchString(tool) {
			problems = append(problems, fmt.Sprintf("%s.tools names %q, which is not a tool name; a tool is named exactly, in letters, digits, underscores, hyphens and dots, with no wildcard", key, tool))
		}
		if class := s.Tools[tool]; class != MCPRead && class != MCPAct {
			problems = append(problems, fmt.Sprintf("%s.tools.%s is %q; each tool is %q, a call that changes nothing outside the harness, or %q, one that may", key, tool, class, MCPRead, MCPAct))
		}
	}
	return problems
}

// connectorProblems is what is wrong with a server reached through the
// operator's Claude account: it is Slack's alone, and it names nothing the
// harness would start, dial or keep a credential for.
func (s MCPServer) connectorProblems(key string, hasCommand, hasURL bool) []string {
	var problems []string
	connector := s.Connector
	if hasCommand || hasURL || s.Login != nil || s.Credential != MCPCredentialNone || s.CredentialEnv != "" {
		problems = append(problems, fmt.Sprintf("%s.connector is set beside a command, url, login, credential or credential_env; a connector is reached through the operator's Claude account and holds none of them", key))
	}
	if connector.Service != MCPConnectorSlack {
		problems = append(problems, fmt.Sprintf("%s.connector.service is %q; the only service a connector may be for is %q", key, connector.Service, MCPConnectorSlack))
	}
	if connector.TimeLimit.Duration() < 0 || connector.TimeLimit.Duration() > MCPConnectorMaxTimeLimit {
		problems = append(problems, fmt.Sprintf("%s.connector.time_limit is %s; it is at most %s", key, connector.TimeLimit, MCPConnectorMaxTimeLimit))
	}
	for field, value := range map[string]string{"tool_prefix": connector.ToolPrefix, "channel_argument": connector.ChannelArgument, "text_argument": connector.TextArgument} {
		if value != "" && !mcpToolNamePattern.MatchString(value) {
			problems = append(problems, fmt.Sprintf("%s.connector.%s %q is not a name in letters, digits, underscores, hyphens and dots", key, field, value))
		}
	}
	for _, form := range connector.LinkForms {
		if parsed, err := url.Parse(form); err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(form, " \t\r\n") {
			problems = append(problems, fmt.Sprintf("%s.connector.link_forms names %q, which is not an https address with a host", key, form))
		}
	}
	return problems
}

// messageProblems is what is wrong with the posts a connector allows: a post
// needs a channel and words, and a server with a tool that acts needs at least
// one post and one link form, because a role may post nothing else.
func (c MCPConnector) messageProblems(key string, tools map[string]MCPToolClass) []string {
	var problems []string
	names := make([]string, 0, len(c.Messages))
	for name := range c.Messages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		message := c.Messages[name]
		if domain.ValidateIdentifier("message name", name) != nil {
			problems = append(problems, fmt.Sprintf("%s.connector.messages names %q, which is not a plain name", key, name))
		}
		if strings.TrimSpace(message.Channel) == "" || strings.TrimSpace(message.Text) == "" {
			problems = append(problems, fmt.Sprintf("%s.connector.messages.%s needs a channel and text", key, name))
		}
	}
	acts := false
	for _, class := range tools {
		acts = acts || class == MCPAct
	}
	if acts && len(c.Messages) == 0 {
		problems = append(problems, fmt.Sprintf("%s has a tool that acts and no connector.messages, so nothing could be posted; a role posts only a configured message", key))
	}
	return problems
}

func mcpCommandProblems(key string, command []string) []string {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return []string{fmt.Sprintf("%s must name a program first", key)}
	}
	for index, argument := range command {
		if strings.ContainsRune(argument, 0) {
			return []string{fmt.Sprintf("%s argument %d holds a NUL byte", key, index)}
		}
	}
	return nil
}

func mcpURLProblems(key, raw string) []string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	switch {
	case err != nil:
		return []string{fmt.Sprintf("%s %q is not an address: %v", key, raw, err)}
	case parsed.Scheme != "https":
		return []string{fmt.Sprintf("%s %q must be an https address", key, raw)}
	case parsed.Host == "" || parsed.Hostname() == "":
		return []string{fmt.Sprintf("%s %q names no host", key, raw)}
	case parsed.User != nil:
		return []string{fmt.Sprintf("%s %q carries a user or password; a credential is named by its store, never written into the address", key, raw)}
	case parsed.Fragment != "":
		return []string{fmt.Sprintf("%s %q carries a fragment, which no request sends", key, raw)}
	}
	return nil
}

// mcpToolProblems refuses an agent's list naming a tool the section does not
// declare, a wildcard, a name twice, or a tool whose class the agent's role
// does not hold, naming the agent, the tool, and why.
func (c Config) mcpToolProblems(agent string, configured AgentConfig) []string {
	var problems []string
	seen := map[string]bool{}
	for _, name := range configured.MCPTools {
		said := fmt.Sprintf("agent %q mcp_tools names %q", agent, name)
		if strings.Contains(name, "*") {
			problems = append(problems, said+", and a list names each tool exactly, with no wildcard")
			continue
		}
		server, tool, written := strings.Cut(name, ".")
		if !written || server == "" || tool == "" {
			problems = append(problems, said+", which is not written as server.tool")
			continue
		}
		if seen[name] {
			problems = append(problems, said+" twice")
			continue
		}
		seen[name] = true
		declared, ok := c.MCP.Servers[server]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s, and the mcp section declares no server %q", said, server))
			continue
		}
		class, ok := declared.Tools[tool]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s, and mcp.servers.%s.tools does not name %q, so it cannot be called by anyone", said, server, tool))
			continue
		}
		if (class == MCPRead || class == MCPAct) && !MCPRoleHolds(configured.Role, class) {
			problems = append(problems, fmt.Sprintf("%s, %s tool, and an agent of the %s role %s", said, mcpClassPhrase(class), configured.Role, mcpRoleRefusal(configured.Role)))
		}
	}
	return problems
}

// mcpClassPhrase is a class with its article, as a refusal says it.
func mcpClassPhrase(class MCPToolClass) string {
	if class == MCPAct {
		return "an act"
	}
	return "a read"
}

// mcpRoleRefusal says why a role may not hold the class it was refused.
func mcpRoleRefusal(role domain.AgentRole) string {
	switch role {
	case domain.RoleDeveloper:
		return "may hold read tools only, because an act taken during a run happens before review and could not be undone if review refused the change"
	case domain.RoleProgramManager:
		return "may hold read tools only"
	case domain.RoleReviewer:
		return "holds no MCP tools, so its review rests on the record of what was called rather than on calls of its own"
	}
	return "holds no MCP tools"
}

func sortedServerNames(servers map[string]MCPServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// copyMCP is a layer's section copied rather than aliased, so a later layer or
// a caller holding the resolved configuration never edits what a document holds.
func copyMCP(source MCP) MCP {
	// An empty section is no section, so `servers: {}` digests and shows as a
	// file that never wrote the key.
	if len(source.Servers) == 0 {
		return MCP{}
	}
	copied := MCP{Servers: make(map[string]MCPServer, len(source.Servers))}
	for name, server := range source.Servers {
		server.Command = append([]string(nil), server.Command...)
		server.Login = append([]string(nil), server.Login...)
		tools := make(map[string]MCPToolClass, len(server.Tools))
		for tool, class := range server.Tools {
			tools[tool] = class
		}
		server.Tools = tools
		if server.Connector != nil {
			connector := *server.Connector
			connector.LinkForms = append([]string(nil), connector.LinkForms...)
			messages := make(map[string]MCPConnectorMessage, len(connector.Messages))
			for name, message := range connector.Messages {
				messages[name] = message
			}
			connector.Messages = messages
			server.Connector = &connector
		}
		copied.Servers[name] = server
	}
	return copied
}
