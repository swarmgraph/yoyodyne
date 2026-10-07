package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// hierarchyConfig configures the whole default role hierarchy, which is what an
// operator addressing "any configured agent" actually has in front of them.
const hierarchyConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  product-manager:
    role: product-manager
    backend: claude-code
    model: opus
  architect:
    role: architect
    backend: claude-code
    model: opus
  development-manager:
    role: development-manager
    backend: claude-code
    model: opus
  developer:
    role: developer
    backend: claude-code
    model: opus
  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
`

// The operator can see who is configured and what each one decides, without a
// provider being started for any of it: this reads durable state, and an agent
// nobody has spoken to says so rather than being missing from the list.
func TestAgentListReportsEveryConfiguredAgentAndItsDurableState(t *testing.T) {
	// Not parallel: the state root the command reads is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, hierarchyConfig)

	stdout, stderr, code := runCLI(t, "agent", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"5 configured agent(s) for yoyodyne",
		"architect (architect) claude-code, model opus",
		"owns the designs, the decision records, and the architectural invariants",
		"development-manager (development-manager)",
		"owns decomposition, dependency structure, and triage of work that has stopped moving",
		"no conversation recorded",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A conversation recorded by an earlier process is what the agent's identity
	// is: the process that held it is gone, and the operator is still told what
	// the architect has been told and when.
	store, err := runstate.NewConversationStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	spokenAt := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	if err := store.Save(runstate.Conversation{
		SchemaVersion:         runstate.ConversationSchemaVersion,
		ConversationID:        "chat-0123456789abcdef0123456789abcdef",
		ProductID:             "yoyodyne",
		RepositoryID:          "yoyodyne",
		Role:                  domain.RoleArchitect,
		Backend:               domain.BackendClaudeCode,
		ProviderSessionID:     "session-architect",
		ProviderModel:         "opus",
		ProviderResolvedModel: "claude-opus-5-20260514",
		Turns:                 3,
		StartedAt:             spokenAt.Add(-time.Hour),
		UpdatedAt:             spokenAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "agent", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"conversation chat-0123456789abcdef0123456789abcdef: 3 turn(s), last spoken to 2026-08-18T09:00:00Z",
		"last answered by claude-opus-5-20260514 (asked for opus)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// The same state, for anything that is not a terminal.
	stdout, stderr, code = runCLI(t, "agent", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("agent list --json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Product string        `json:"product"`
		Agents  []agentReport `json:"agents"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Product != "yoyodyne" || len(decoded.Agents) != 5 {
		t.Fatalf("decoded = %#v", decoded)
	}
	byName := map[string]agentReport{}
	for _, agent := range decoded.Agents {
		byName[agent.Name] = agent
		if !agent.Addressable {
			t.Fatalf("%s is reported as unaddressable", agent.Name)
		}
	}
	architect := byName["architect"]
	if architect.Conversation == nil || architect.Conversation.Turns != 3 || !architect.Conversation.Resumable {
		t.Fatalf("architect = %#v", architect)
	}
	if byName["developer"].Conversation != nil {
		t.Fatal("the developer reports a conversation nobody had with it")
	}
}

// One agent in full, including the work its role is executing. A developer that
// is in the middle of a run is the case where "what is this agent doing" has an
// answer that is not a conversation.
func TestAgentShowReportsOneAgentAndTheWorkItsRoleIsExecuting(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, hierarchyConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.4",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     started,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "agent", "show", "--config", configPath, "developer")
	if code != 0 {
		t.Fatalf("agent show code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"developer (developer) claude-code, model opus",
		"run run-0123456789abcdef0123456789abcdef on yoyodyne-ifd.4: running (developing)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A role that does not execute inside a run is not reported as idle work:
	// the architect has no runs, and saying "no run is in flight" about it would
	// describe a queue it has nothing to do with.
	stdout, stderr, code = runCLI(t, "agent", "show", "--config", configPath, "architect")
	if code != 0 {
		t.Fatalf("agent show architect code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "no run is in flight") || strings.Contains(stdout, "run run-") {
		t.Fatalf("the architect was reported against runs: %q", stdout)
	}

	// An agent nobody configured is refused by name rather than by failing
	// somewhere further in.
	_, stderr, code = runCLI(t, "agent", "show", "--config", configPath, "release-manager")
	if code == 0 || !strings.Contains(stderr, `no agent named "release-manager" is configured`) {
		t.Fatalf("agent show code = %d, stderr = %q", code, stderr)
	}
}

// What the operator types is either an agent's configured name or the role it
// fills, because those are the two things they are likely to remember. A role
// filled by more than one agent is a question rather than a request.
func TestResolveAgentAcceptsANameOrARoleAndRefusesAnAmbiguousOne(t *testing.T) {
	t.Parallel()

	resolved, err := loadConfiguration(writeConfig(t, hierarchyConfig))
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	name, role, err := resolveAgent(resolved.Config, "architect")
	if err != nil || name != "architect" || role != domain.RoleArchitect {
		t.Fatalf("resolveAgent(architect) = %q, %q, %v", name, role, err)
	}

	// A project may name its agents whatever it likes; the role still addresses
	// the one filling it.
	renamed := resolved.Config
	renamed.Agents = map[string]config.AgentConfig{
		"house-architect": resolved.Config.Agents["architect"],
	}
	name, role, err = resolveAgent(renamed, "architect")
	if err != nil || name != "house-architect" || role != domain.RoleArchitect {
		t.Fatalf("resolveAgent(role) = %q, %q, %v", name, role, err)
	}

	twoDevelopers := resolved.Config
	twoDevelopers.Agents = map[string]config.AgentConfig{
		"first-developer":  resolved.Config.Agents["developer"],
		"second-developer": resolved.Config.Agents["developer"],
	}
	if _, _, err := resolveAgent(twoDevelopers, "developer"); err == nil {
		t.Fatal("resolveAgent() resolved a name that two agents answer to")
	}
	if _, _, err := resolveAgent(resolved.Config, "  "); err == nil {
		t.Fatal("resolveAgent() accepted an empty name")
	}
}

// Each role is given the documents it answers for, and the product manager is
// given none of them. That asymmetry is the point: intent is what the product
// manager reasons from, and everything downstream of intent needs the documents
// that record how the product is built.
func TestEachRoleIsGivenTheDocumentsItAnswersFor(t *testing.T) {
	t.Parallel()

	product := config.Product{
		Designs:    "docs/designs",
		Invariants: "docs/decisions/invariants",
		Decisions:  "docs/decisions",
	}
	if sets := roleDocumentSets(domain.RoleProductManager, product); len(sets) != 0 {
		t.Fatalf("the product manager was given %#v", sets)
	}
	architect := roleDocumentSets(domain.RoleArchitect, product)
	if len(architect) != 3 {
		t.Fatalf("architect sets = %#v", architect)
	}
	// The invariants come before the decision records they are extracted from,
	// so the nested directory is read under the label that says what it is.
	if architect[0].Directory != "docs/designs" ||
		architect[1].Directory != "docs/decisions/invariants" ||
		architect[2].Directory != "docs/decisions" {
		t.Fatalf("architect sets are out of order: %#v", architect)
	}
	if architect[1].Label != "Architectural invariant" {
		t.Fatalf("the invariants are labelled %q", architect[1].Label)
	}
	for _, role := range []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		sets := roleDocumentSets(role, product)
		if len(sets) != 2 || sets[0].Directory != "docs/designs" || sets[1].Directory != "docs/decisions/invariants" {
			t.Fatalf("%s sets = %#v", role, sets)
		}
	}
}

// twoArchitectsConfig is the case the whole name-versus-role distinction exists
// for: one role, two agents, different personas and different models. Addressing
// one of them by name has to reach that one.
const twoArchitectsConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  house-architect:
    role: architect
    backend: claude-code
    model: fable
  visiting-architect:
    role: architect
    backend: claude-code
    model: claude-opus-5-20260514
  developer:
    role: developer
    backend: claude-code
    model: opus
`

// The agent an operator named is the agent that answers. The role decides the
// contract and the authority; the name decides the persona and the model, and a
// command that resolved the name to a role and then looked the role up again
// would reach whichever sibling sorted first with no sign that it had.
func TestNamingAnAgentAddressesThatAgentRatherThanItsRole(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, twoArchitectsConfig)
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	cfg := resolved.Config

	// The sibling that is not named is the one the role alone resolves to, so a
	// conversation that reached it would be indistinguishable from one that
	// ignored the name. That is what makes the assertions below discriminating.
	if agentNameForRole(cfg, domain.RoleArchitect) != "house-architect" {
		t.Fatalf("the role resolves to %q, so this test proves nothing", agentNameForRole(cfg, domain.RoleArchitect))
	}

	// What the command line resolved travels on to the conversation: the name as
	// well as the role, because the role alone cannot say which of the two.
	var stderr strings.Builder
	role, request, code := agentConversationRequest(
		[]string{"--config", configPath, "--message", "which one are you?", "visiting-architect"}, &stderr)
	if code != 0 {
		t.Fatalf("agentConversationRequest() code = %d, stderr = %q", code, stderr.String())
	}
	if role != domain.RoleArchitect || request.agentName != "visiting-architect" {
		t.Fatalf("resolved role %q and agent %q", role, request.agentName)
	}
	if request.message != "which one are you?" || request.configPath != configPath {
		t.Fatalf("request = %#v", request)
	}

	// And the conversation is built from that agent: its model, and its persona.
	name, agent, err := conversationAgent(cfg, role, request.agentName)
	if err != nil {
		t.Fatalf("conversationAgent() error = %v", err)
	}
	if name != "visiting-architect" || agent.Model != "claude-opus-5-20260514" {
		t.Fatalf("conversationAgent() = %q, model %q", name, agent.Model)
	}

	// A conversation that names no agent — `yoyo chat`, and every role with one
	// agent — still takes the one filling the role.
	name, agent, err = conversationAgent(cfg, domain.RoleArchitect, "")
	if err != nil || name != "house-architect" || agent.Model != "fable" {
		t.Fatalf("conversationAgent(unnamed) = %q, model %q, err = %v", name, agent.Model, err)
	}

	// A name and a role that disagree are refused rather than resolved to one of
	// them: the role decides the contract, the name decides the persona, and a
	// conversation whose two halves came from different agents is not one
	// anybody asked for.
	if _, _, err := conversationAgent(cfg, domain.RoleDeveloper, "house-architect"); err == nil {
		t.Fatal("conversationAgent() accepted an agent that fills another role")
	}
	if _, _, err := conversationAgent(cfg, domain.RoleArchitect, "nobody"); err == nil {
		t.Fatal("conversationAgent() accepted an agent nobody configured")
	}
	if _, _, err := conversationAgent(cfg, domain.RoleReviewer, ""); err == nil {
		t.Fatal("conversationAgent() invented an agent for an unfilled role")
	}
}

// Failing to ask whether a conversation is held is not the same as being told
// that it is. An unreadable state directory reported as "in use" would tell the
// operator every agent was mid-conversation at once, which is both wrong and the
// opposite of what they would act on.
func TestAConversationLeaseThatCannotBeAskedIsReportedAsAProblem(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	architectIdentity := runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect}
	inUse, problem := readmodel.InFlight(store, architectIdentity)
	if inUse || problem != "" {
		t.Fatalf("a free conversation reported in use = %v, problem = %q", inUse, problem)
	}

	// Held by this process, which is what another process holding it looks like
	// from outside.
	lease, err := store.Hold(architectIdentity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer lease.Release()
	if inUse, problem = readmodel.InFlight(store, architectIdentity); !inUse || problem != "" {
		t.Fatalf("a held conversation reported in use = %v, problem = %q", inUse, problem)
	}

	// A role whose name could never be a path is the failure to ask: it is
	// refused before any lock is taken, so it is a problem to report rather than
	// a conversation to claim.
	if inUse, problem = readmodel.InFlight(store, runstate.ConversationIdentity{Agent: "Not An Agent", Role: domain.RoleArchitect}); inUse || problem == "" {
		t.Fatalf("an unaskable lease reported in use = %v, problem = %q", inUse, problem)
	}
}

// Asking whether a conversation is in use must leave it exactly as free as it
// found it, and asking takes nothing to do it. The listing asks once per agent
// and the status asks on every reading, so an ask that acquired anything would
// answer "held by another process" — to the next agent in the same listing, to
// the next listing, and to the operator's own chat — about a conversation
// nobody is in.
func TestAskingWhetherAConversationIsInUseDoesNotKeepIt(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	identity := runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect}
	for ask := 1; ask <= 3; ask++ {
		if inUse, problem := readmodel.InFlight(store, identity); inUse || problem != "" {
			t.Fatalf("ask %d reported in use = %v, problem = %q, want a conversation nobody holds", ask, inUse, problem)
		}
	}
	// Having been asked about is not having been taken: a conversation somebody
	// now wants to have must still be there to take.
	lease, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() after asking error = %v, want the conversation still free", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// Two agents on one role are two identities in the listing as well as in the
// store: the conversation one of them has had is reported against that one, and
// the sibling is reported as never spoken to. Printing one record twice would
// tell the operator that both had been in the conversation only one of them was.
func TestAgentListReportsSiblingAgentsSeparately(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, twoArchitectsConfig)

	store, err := runstate.NewConversationStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	spokenAt := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	if err := store.Save(runstate.Conversation{
		SchemaVersion:     runstate.ConversationSchemaVersion,
		ConversationID:    "chat-0123456789abcdef0123456789abcdef",
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		Agent:             "visiting-architect",
		Role:              domain.RoleArchitect,
		Backend:           domain.BackendClaudeCode,
		ProviderSessionID: "session-visiting",
		ProviderModel:     "claude-opus-5-20260514",
		Turns:             2,
		StartedAt:         spokenAt.Add(-time.Hour),
		UpdatedAt:         spokenAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "agent", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Agents []agentReport `json:"agents"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	byName := map[string]agentReport{}
	for _, agent := range decoded.Agents {
		byName[agent.Name] = agent
	}
	visiting := byName["visiting-architect"]
	if visiting.Conversation == nil || visiting.Conversation.Turns != 2 {
		t.Fatalf("the conversation was not reported against the agent that had it: %#v", visiting)
	}
	if house := byName["house-architect"]; house.Conversation != nil {
		t.Fatalf("the sibling was reported as having had a conversation: %#v", house.Conversation)
	}
}

// What an operator is told about an alternate that leaves the provider. Both
// halves have to be there: a crossing rebuilds the conversation from the record
// rather than resuming a session, and it covers conversation turns and exchange
// rounds only — a side turn is answered on the endpoint the agent is configured
// for and has no crossing to take, so an unqualified line would be a promise that
// path does not keep.
func TestAgentSaysWhatACrossingCoversAndWhatItCosts(t *testing.T) {
	crossing := renderAgent(agentReport{
		Name: "development-manager", Role: domain.RoleDevelopmentManager,
		Backend: domain.BackendClaudeCode, Model: "fable", Instances: 1,
		FailoverModel: "gpt-5-codex", FailoverProvider: domain.BackendCodex,
	})
	for _, want := range []string{
		"conversation turns and exchange rounds served by gpt-5-codex on codex",
		"rebuilding context from the record rather than resuming a session",
		"side threads and run invocations wait it out",
	} {
		if !strings.Contains(crossing, want) {
			t.Fatalf("agent line = %q, which does not say %q", crossing, want)
		}
	}

	// An alternate on the agent's own provider is the promise it always was, and
	// still covers the rounds another role asks it.
	within := renderAgent(agentReport{
		Name: "development-manager", Role: domain.RoleDevelopmentManager,
		Backend: domain.BackendClaudeCode, Model: "fable", Instances: 1,
		FailoverModel: "opus",
	})
	if !strings.Contains(within, "turns and exchange rounds served by opus while fable has no capacity") {
		t.Fatalf("agent line = %q, want the within-provider alternate said as it always was", within)
	}
	if strings.Contains(within, "rebuilding context") {
		t.Fatalf("agent line = %q, which claims a rebuild for a substitution that resumes its own session", within)
	}
}

// A program manager instance is told apart from another by its lane, so the
// listing prints the lane beside the agent's name, and says where its remit is
// and what wakes it.
func TestAgentListNamesAProgramManagersLaneBesideItsName(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, twoArchitectsConfig+`  reliability-pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: reliability
    remit:
      version: r1
      path: remits/reliability.md
    triggers:
      every: 2h
      on: [landings, stoppages]
`)
	remits := filepath.Join(filepath.Dir(configPath), ".yoyodyne", "remits")
	if err := os.MkdirAll(remits, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remits, "reliability.md"), []byte("# Reliability\n\nWatch the line.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "agent", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"reliability-pm (program-manager, lane reliability) claude-code",
		"  remit remits/reliability.md (r1)\n",
		"  passes over its lane every 2h0m0s and on landings, stoppages\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("agent list = %q, want it to say %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "house-architect (architect, lane") {
		t.Errorf("agent list = %q, which gives an architect a lane", stdout)
	}

	stdout, stderr, code = runCLI(t, "agent", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("agent list --json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Agents []agentReport `json:"agents"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	for _, agent := range decoded.Agents {
		if agent.Name == "reliability-pm" {
			if agent.Lane != "reliability" || agent.RemitPath != "remits/reliability.md" || agent.Triggers == nil || len(agent.Triggers.On) != 2 {
				t.Fatalf("reliability-pm = %#v, want its lane, remit, and triggers", agent)
			}
			return
		}
	}
	t.Fatal("agent list --json did not report the program manager instance")
}
