package cli

// The operator's view of the agents themselves: who is configured, what each
// one is currently in the middle of, and how to address one directly.
//
// An agent here is a durable logical identity rather than a process. The
// provider process that answers a turn is ephemeral and is gone by the time
// this command runs; what survives it is the configuration that says who the
// agent is and the conversation record that says what it has been told. So this
// reads both and says what it found, rather than looking for anything running.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// agentReport is one configured agent as the operator reads it: what it is,
// what it decides, and what durable state it has.
type agentReport struct {
	Name string           `json:"name"`
	Role domain.AgentRole `json:"role"`
	// Definition is the role definition the agent fills, and absent for an
	// agent on its shipped role. Role above is then the role it extends.
	Definition string         `json:"definition,omitempty"`
	Backend    domain.Backend `json:"backend"`
	Model      string         `json:"model"`
	// ModelVersion is the exact version of that family this agent's turns ask
	// for, and absent for every agent that pins none — which is the alias above
	// floating to the family's current best. It is read here beside the model
	// because an operator asking what an agent is has to be able to tell a pinned
	// agent from a floating one without opening the configuration.
	ModelVersion string `json:"model_version,omitempty"`
	// Effort is the effort level every invocation of this agent asks for, and
	// absent for an agent that configured none, whose provider resolves its own.
	Effort string `json:"effort,omitempty"`
	// Account is the provider account this agent runs under. Which role runs
	// where is the operator's and it is fixed, so it is read here beside the
	// model rather than reconstructed from the configuration by hand.
	Account string `json:"account,omitempty"`
	// FailoverModel is the permitted alternate this agent's turn may be served by
	// while the model above has no capacity, and is absent for every agent that
	// has not enabled failover. It is read here beside the model for the reason
	// the account is: what an agent is includes what answers for it when its own
	// model will not.
	//
	// What it covers is the turns the agent takes rather than every invocation
	// made on its behalf: a run's developer and reviewer invocations still wait
	// their window out. The rendering says so, because a developer agent with an
	// alternate named and a run that parks anyway is exactly the pair somebody
	// would otherwise read as broken.
	FailoverModel string `json:"failover_model,omitempty"`
	// FailoverProvider is the provider that alternate is served by, and is absent
	// where the alternate stays on the provider above — which is every agent that
	// names none. It is read here because a crossing is a different promise from a
	// substitution within one provider, and a narrower one: the turn is served by a
	// provider holding no session for it, so its context is rebuilt from the durable
	// record, and only this agent's conversation turns cross at all — an exchange
	// round and a side turn are answered on the endpoint the agent is configured
	// for. The rendering says both, because an operator reading the alternate as
	// covering everything the within-provider one covers would be reading a promise
	// that is not kept.
	FailoverProvider domain.Backend `json:"failover_provider,omitempty"`
	// Conversations is what this agent does with a question that arrives while
	// its main thread is busy — queueing it, or holding it on a side thread. It is
	// read here for the reason the alternate is: an operator asking what an agent
	// is cannot otherwise tell which of the two a question will get, and the
	// answer is a fact about the agent rather than about any one question. It says
	// nothing about what a side thread may do, which is the role's own authority
	// narrowed in Go and is not this key's to move.
	Conversations  config.ConversationMode `json:"conversations"`
	Instances      int                     `json:"instances"`
	PersonaPath    string                  `json:"persona_path,omitempty"`
	PersonaVersion string                  `json:"persona_version,omitempty"`
	// Lane is the tracker label a program manager instance owns, and absent for
	// every other agent. It is printed beside the agent's name because it is what
	// tells one instance of the role from another.
	Lane         string `json:"lane,omitempty"`
	RemitPath    string `json:"remit_path,omitempty"`
	RemitVersion string `json:"remit_version,omitempty"`
	// Triggers is what wakes the instance for a pass, as configured; absent where
	// nothing is configured.
	Triggers *config.Triggers `json:"triggers,omitempty"`
	// Owns is what this role decides, from the authority table rather than from
	// the persona: a project can rewrite the persona and cannot rewrite this.
	Owns string `json:"owns,omitempty"`
	// Addressable reports whether the harness holds conversations with this
	// role at all. A role with no contract has no conversation, and saying so is
	// better than a command that fails when it is tried.
	Addressable bool `json:"addressable"`
	// Conversation is the durable record, absent when this agent has never been
	// spoken to.
	Conversation *conversationReport `json:"conversation,omitempty"`
	// InUse reports that another process has this agent's conversation right
	// now, which is what anything else wanting a turn waits behind. An
	// interactive conversation puts it down between turns, so an operator sitting
	// at a prompt is not what this reports: what it reports is somebody mid-turn.
	InUse bool `json:"in_use,omitempty"`
	// Problem is why the durable state could not be read, when it could not.
	Problem string `json:"problem,omitempty"`
}

type conversationReport struct {
	ID                string    `json:"id"`
	Turns             int       `json:"turns"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ProviderSessionID string    `json:"provider_session_id,omitempty"`
	RequestedModel    string    `json:"requested_model,omitempty"`
	ResolvedModel     string    `json:"resolved_model,omitempty"`
	ContextGatheredAt time.Time `json:"context_gathered_at,omitempty"`
	ContextCommit     string    `json:"context_commit,omitempty"`
	// ContextShippedDocumentationBytes is what the shipped documentation in
	// that picture added up to on disk, recorded on every pass so the set's
	// growth toward its ceiling is readable here rather than only from the
	// test that fails once it is reached.
	ContextShippedDocumentationBytes int `json:"context_shipped_documentation_bytes,omitempty"`
	// ContextWaiting says that picture was read and has not reached the agent
	// yet: the record moves to a re-read as it is taken, and the agent is told
	// what moved with the next thing said to it.
	ContextWaiting    bool   `json:"context_waiting,omitempty"`
	LastRunWorkItemID string `json:"last_run_work_item_id,omitempty"`
	// Resumable says whether a later process can continue this conversation. A
	// record whose first turn never completed has no provider session, and
	// speaking to it starts again rather than carrying on.
	Resumable bool `json:"resumable"`
}

// runReport is one run this agent's work is recorded in. Runs are not owned by
// a role — a run is a developer attempt, its checks, and a reviewer verdict —
// so they are reported against the roles that actually execute inside one
// rather than against every configured agent.
type runReport struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	Status     string    `json:"status"`
	Phase      string    `json:"phase,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func runAgentCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printAgentUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listAgents(args[1:], stdout, stderr)
	case "show":
		return showAgent(args[1:], stdout, stderr)
	case "chat":
		return chatWithAgent(ctx, args[1:], stdin, stdout, stderr)
	case "memory":
		return showAgentMemory(args[1:], stdout, stderr)
	case "cut-replies":
		return auditCutReplies(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown agent command %q\n\n", args[0])
		printAgentUsage(stderr)
		return 2
	}
}

func listAgents(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "agent list does not accept positional arguments; use `yoyo agent show <name>` for one agent")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reports, err := readAgents(parts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product": parts.config.Product.ID,
			"agents":  reports,
		})
	}
	fmt.Fprintf(stdout, "%d configured agent(s) for %s:\n", len(reports), parts.config.Product.ID)
	for _, report := range reports {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, renderAgent(report))
	}
	return 0
}

func showAgent(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "name the agent to show, as `yoyo agent show <name>`; `yoyo agent list` names them")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	name, _, err := resolveAgent(parts.config, positional[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reports, err := readAgents(parts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var found agentReport
	for _, report := range reports {
		if report.Name == name {
			found = report
			break
		}
	}
	// The runs are the other half of durable state: what the harness is in the
	// middle of doing, as opposed to what it has been told. They are reported
	// for the roles that execute inside a run and left out for the roles that do
	// not, rather than shown empty for every agent as though the developer were
	// idle.
	var runs []runReport
	if found.Role == domain.RoleDeveloper || found.Role == domain.RoleReviewer {
		runs, err = readRunsInFlight(parts.store)
		if err != nil {
			found.Problem = appendProblem(found.Problem, err.Error())
		}
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product": parts.config.Product.ID,
			"agent":   found,
			"runs":    runs,
		})
	}
	fmt.Fprint(stdout, renderAgent(found))
	if found.Role == domain.RoleDeveloper || found.Role == domain.RoleReviewer {
		if len(runs) == 0 {
			fmt.Fprintln(stdout, "  no run is in flight")
		}
		for _, run := range runs {
			fmt.Fprintf(stdout, "  run %s on %s: %s", run.RunID, run.WorkItemID, run.Status)
			if run.Phase != "" {
				fmt.Fprintf(stdout, " (%s)", run.Phase)
			}
			fmt.Fprintf(stdout, ", last moved %s\n", run.UpdatedAt.UTC().Format(time.RFC3339))
		}
	}
	return 0
}

func chatWithAgent(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	role, request, code := agentConversationRequest(args, stderr)
	if code != 0 {
		return code
	}
	return converse(ctx, role, request, stdin, stdout, stderr)
}

// agentConversationRequest turns the command line into the conversation it asks
// for: which role answers, and which configured agent fills it. Both travel on,
// because the role decides the contract and the agent decides the persona and
// the model — resolving the name here and looking the role up again later would
// address whichever agent sorted first in a project that configured two.
//
// It is separate from holding the conversation so that what the operator named
// is checked before a lease is taken or a provider is started, and so that what
// it resolved can be tested without either.
func agentConversationRequest(args []string, stderr io.Writer) (domain.AgentRole, conversationRequest, int) {
	flags := flag.NewFlagSet("agent chat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	message := flags.String("message", "", "send one message and print the reply instead of opening an interactive conversation")
	fresh := flags.Bool("new", false, "start a new conversation instead of resuming the recorded one")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON (requires --message)")
	sideThread := flags.String("side-thread", "", "continue the named side thread with --message instead of reaching the main conversation")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return "", conversationRequest{}, 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "name the agent to talk to, as `yoyo agent chat <name>`; `yoyo agent list` names them")
		return "", conversationRequest{}, 2
	}
	if *jsonOutput && *message == "" {
		fmt.Fprintln(stderr, "agent chat --json requires --message: an interactive conversation has no single result to encode")
		return "", conversationRequest{}, 2
	}
	if err := sideThreadFlagProblems("agent chat", *sideThread, *message, *fresh); err != nil {
		fmt.Fprintln(stderr, err)
		return "", conversationRequest{}, 2
	}

	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", conversationRequest{}, 1
	}
	name, role, err := resolveAgent(resolved.Config, positional[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", conversationRequest{}, 1
	}
	if _, known := chat.AuthorityFor(role); !known {
		fmt.Fprintf(stderr, "the harness holds no conversation contract for role %q, so there is nothing to say to it\n", role)
		return "", conversationRequest{}, 1
	}
	return role, conversationRequest{
		agentName:  name,
		configPath: *configPath,
		message:    *message,
		fresh:      *fresh,
		jsonOutput: *jsonOutput,
		sideThread: *sideThread,
	}, 0
}

// readAgents reports every configured agent with whatever durable state it has.
// A conversation that cannot be read is reported against the agent it belongs
// to rather than failing the listing: an operator asking who is configured is
// owed the answer even when one record is unreadable.
func readAgents(parts components) ([]agentReport, error) {
	store, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parts.config.Agents))
	for name := range parts.config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)

	reports := make([]agentReport, 0, len(names))
	for _, name := range names {
		agent := parts.config.Agents[name]
		report := agentReport{
			Name:           name,
			Role:           agent.Role,
			Definition:     agentDefinitionName(agent),
			Backend:        agent.Backend,
			Model:          agent.Model,
			ModelVersion:   parts.config.AgentModelVersion(name),
			Effort:         parts.config.AgentEffort(name),
			Account:        agent.Account,
			FailoverModel:  parts.config.AgentFailoverModel(name),
			Conversations:  parts.config.AgentConversationMode(name),
			Instances:      agent.Instances,
			PersonaPath:    agent.Persona.Path,
			PersonaVersion: agent.Persona.Version,
			Lane:           parts.config.AgentLane(name),
			RemitPath:      agent.Remit.Path,
			RemitVersion:   agent.Remit.Version,
		}
		if agent.Triggers.Defined() {
			triggers := agent.Triggers
			report.Triggers = &triggers
		}
		// Named only where the alternate actually leaves the provider, so an agent
		// that fails over within its own reads exactly as it always has.
		if failover := parts.config.AgentFailover(name); failover.Alternate() != "" && failover.CrossesProviders(agent.Backend) {
			report.FailoverProvider = failover.AlternateProvider(agent.Backend)
		}
		if authority, known := chat.AuthorityFor(agent.Role); known {
			report.Addressable = true
			report.Owns = authority.Owns
		}
		identity := runstate.ConversationIdentity{Agent: name, Role: agent.Role}
		recorded, err := store.Read(identity)
		switch {
		case err == nil:
			report.Conversation = &conversationReport{
				ID:                               recorded.ConversationID,
				Turns:                            recorded.Turns,
				StartedAt:                        recorded.StartedAt,
				UpdatedAt:                        recorded.UpdatedAt,
				ProviderSessionID:                recorded.ProviderSessionID,
				RequestedModel:                   recorded.ProviderModel,
				ResolvedModel:                    recorded.ProviderResolvedModel,
				ContextGatheredAt:                recorded.ContextGatheredAt,
				ContextCommit:                    recorded.ContextCommit,
				ContextShippedDocumentationBytes: recorded.ContextShippedDocumentationBytes,
				ContextWaiting:                   recorded.PendingPicture != nil,
				LastRunWorkItemID:                recorded.LastRunWorkItemID,
				Resumable:                        recorded.ProviderSessionID != "",
			}
		case errors.Is(err, runstate.ErrNoConversation):
		default:
			report.Problem = err.Error()
		}
		// Whether a turn is in flight is asked of the read model rather than
		// answered again here: the standing status counts the same conversations
		// from the same hold, and two surfaces asking that question their own way
		// is how they come to give an operator two answers.
		inUse, problem := readmodel.InFlight(store, identity)
		report.InUse = inUse
		if problem != "" {
			report.Problem = appendProblem(report.Problem, problem)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// readRunsInFlight reports the runs that have not finished, which is what the
// roles that execute inside a run are currently in the middle of.
func readRunsInFlight(store *runstate.Store) ([]runReport, error) {
	states, err := store.Incomplete()
	if err != nil {
		return nil, err
	}
	reports := make([]runReport, 0, len(states))
	for _, state := range states {
		reports = append(reports, runReport{
			RunID:      state.RunID,
			WorkItemID: state.WorkItemID,
			Status:     string(state.Status),
			Phase:      string(state.Phase),
			Branch:     state.Branch,
			UpdatedAt:  state.UpdatedAt,
		})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].UpdatedAt.After(reports[j].UpdatedAt) })
	return reports, nil
}

// resolveAgent turns what the operator typed into the agent it names. An agent
// name is what a project actually configured, and a role name is what an
// operator is more likely to remember, so both work — and a role filled by more
// than one agent is refused rather than resolved to whichever sorted first,
// because addressing "the developer" when there are three of them is a question
// rather than a request.
func resolveAgent(cfg config.Config, requested string) (string, domain.AgentRole, error) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return "", "", errors.New("name the agent")
	}
	if agent, ok := cfg.Agents[trimmed]; ok {
		return trimmed, agent.Role, nil
	}
	var matches []string
	for name, agent := range cfg.Agents {
		if string(agent.Role) == trimmed {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 1:
		return matches[0], cfg.Agents[matches[0]].Role, nil
	case 0:
		return "", "", fmt.Errorf("no agent named %q is configured; `yoyo agent list` names them", requested)
	default:
		return "", "", fmt.Errorf("%d agents fill the %s role (%s); name the one you mean",
			len(matches), trimmed, strings.Join(matches, ", "))
	}
}

// agentDefinitionName is the role definition an agent fills, or empty.
func agentDefinitionName(agent config.AgentConfig) string {
	if agent.Definition == nil {
		return ""
	}
	return agent.Definition.Name
}

func renderAgent(report agentReport) string {
	var rendered strings.Builder
	// A lane is named inside the parentheses beside the role, because it is what
	// the instance is: two program managers are told apart by their lanes.
	identity := string(report.Role)
	if report.Definition != "" {
		identity = "role definition " + report.Definition + ", extending " + identity
	}
	if report.Lane != "" {
		identity += ", lane " + report.Lane
	}
	// The effort level is said beside the model it is asked of, and only where
	// the agent names one: an agent that names none passes none.
	model := report.Model
	if report.Effort != "" {
		model += " at " + report.Effort + " effort"
	}
	fmt.Fprintf(&rendered, "%s (%s) %s, model %s, account %s, %d instance(s)\n",
		report.Name, identity, report.Backend, model,
		recorded(report.Account, "none the configuration names"), report.Instances)
	if report.ModelVersion != "" {
		// The scope is named for the reason the alternate's below is: a pin covers
		// the turns this agent takes and not the invocations a run makes on its
		// behalf, which ask for the family alias. And the fallback is said out loud,
		// because a pin an operator reads as a guarantee is one they would take a
		// version they never saw served as evidence against.
		fmt.Fprintf(&rendered, "  turns and exchange rounds pinned to %s, falling back to %s where the provider has not got it; run invocations ask for %s\n",
			report.ModelVersion, report.Model, report.Model)
	}
	if report.FailoverModel != "" {
		// The scope is named rather than left to be assumed. Failover covers the
		// turns this agent takes — its conversation and the rounds where another
		// role asks it something — and not the invocations a run makes on its
		// behalf, which wait their window out on execution's usage-limit settings.
		// An unqualified line here would read as a promise the run path does not
		// keep, for a developer or reviewer agent most of all.
		if report.FailoverProvider != "" {
			// A crossing is a narrower promise as well as a different one, and both
			// halves are said. It covers this agent's conversation turns and nothing
			// else: an exchange round and a side turn are answered on the endpoint the
			// agent is configured for and have no way to cross, so an alternate that
			// leaves the provider is no alternate to them and they wait the window out
			// with the run invocations. And what the crossing costs is named, because
			// the provider taking the turn holds no session for it: what it is handed
			// is assembled from the durable record rather than resumed.
			fmt.Fprintf(&rendered, "  conversation turns served by %s on %s while %s has no capacity, rebuilding context from the record rather than resuming a session; exchange rounds, side threads, and run invocations wait it out\n",
				report.FailoverModel, report.FailoverProvider, report.Model)
		} else {
			fmt.Fprintf(&rendered, "  turns and exchange rounds served by %s while %s has no capacity; run invocations wait it out\n",
				report.FailoverModel, report.Model)
		}
	}
	if report.Conversations == config.ConversationSideThreads {
		// Said only for an agent that holds them, because queueing is what every
		// agent does until one says otherwise and a line on every agent saying so
		// would be a listing describing the default five times. What a side thread
		// may do is named rather than left to be assumed: it is the role's own
		// authority narrowed, and the main thread ratifies whatever it drafts.
		fmt.Fprintln(&rendered, "  holds side threads beside its conversation; each judges and drafts, and the main thread ratifies")
	}
	if report.Owns != "" {
		fmt.Fprintf(&rendered, "  owns %s\n", report.Owns)
	}
	if report.PersonaPath != "" {
		fmt.Fprintf(&rendered, "  persona %s (%s)\n", report.PersonaPath, report.PersonaVersion)
	}
	if report.RemitPath != "" {
		fmt.Fprintf(&rendered, "  remit %s (%s)\n", report.RemitPath, report.RemitVersion)
	}
	if report.Triggers != nil {
		fmt.Fprintf(&rendered, "  %s\n", describeTriggers(*report.Triggers))
	}
	if !report.Addressable {
		fmt.Fprintln(&rendered, "  the harness holds no conversation with this role")
	}
	switch {
	case report.Conversation == nil:
		fmt.Fprintln(&rendered, "  no conversation recorded")
	default:
		conversation := report.Conversation
		fmt.Fprintf(&rendered, "  conversation %s: %d turn(s), last spoken to %s\n",
			conversation.ID, conversation.Turns, conversation.UpdatedAt.UTC().Format(time.RFC3339))
		if conversation.ResolvedModel != "" && conversation.ResolvedModel != conversation.RequestedModel {
			fmt.Fprintf(&rendered, "  last answered by %s (asked for %s)\n", conversation.ResolvedModel, conversation.RequestedModel)
		}
		if !conversation.Resumable {
			fmt.Fprintln(&rendered, "  no provider session recorded, so saying something starts it again")
		}
		if !conversation.ContextGatheredAt.IsZero() {
			verb := "working from"
			if conversation.ContextWaiting {
				verb = "about to be given"
			}
			fmt.Fprintf(&rendered, "  %s a picture taken %s", verb, conversation.ContextGatheredAt.UTC().Format(time.RFC3339))
			if conversation.ContextCommit != "" {
				fmt.Fprintf(&rendered, " at %s", conversation.ContextCommit)
			}
			if conversation.ContextShippedDocumentationBytes > 0 {
				fmt.Fprintf(&rendered, ", carrying %d bytes of shipped documentation", conversation.ContextShippedDocumentationBytes)
			}
			fmt.Fprintln(&rendered)
		}
		if conversation.LastRunWorkItemID != "" {
			fmt.Fprintf(&rendered, "  last started work on %s\n", conversation.LastRunWorkItemID)
		}
	}
	if report.InUse {
		fmt.Fprintln(&rendered, "  another process is mid-turn with it right now")
	}
	if report.Problem != "" {
		fmt.Fprintf(&rendered, "  durable state could not be read: %s\n", report.Problem)
	}
	return rendered.String()
}

// appendProblem joins what could not be read, so a second failure does not
// replace the first.
func appendProblem(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + "; " + addition
}

func printAgentUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo agent <list|show|memory|chat|cut-replies> [options] [<name>]

  list                       the configured agents, and what each is in the middle of
  show [options] <name>      one agent in full, with the work its role is executing
  memory [options] <name>    what one agent remembers, each memory with its history
  chat [options] <name>      talk to one agent; <name> is its name or its role
  cut-replies [options]      every recorded reply the conversation logs hold only
                             the beginning of, across every conversation

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON

agent chat options:
  --message <text>  send one message and print the reply instead of conversing
  --new             start a new conversation instead of resuming the recorded one
  --side-thread <id>
                    continue the named side thread with --message

A --message that finds the agent mid-turn waits for it, unless the agent is
configured with "conversations: side-threads": then it is answered beside the
busy turn on a side thread, which judges and answers and takes no action, and the
main conversation's next turn reads what it concluded. "yoyo chat" documents it.

An agent is a durable logical identity: the provider process that answers is
started for a turn and gone afterwards, and what survives it is the conversation
recorded here. Talking to the Lead Product Manager is what "yoyo chat" does, and
"yoyo agent chat product-manager" is the same conversation reached the long way.

What each role may do is fixed by the harness rather than by its persona: the
Lead Product Manager owns the backlog, the development manager decomposes
admitted work underneath it and cannot admit or reorder any, the architect owns
the designs and invariants and edits nothing from a conversation, and the
developer and reviewer do their real work inside runs.`)
}

// describeTriggers says what wakes an instance in one line, as configured. It
// does not say a pass has been taken: reading the triggers is the pass
// machinery's.
func describeTriggers(triggers config.Triggers) string {
	var parts []string
	if triggers.Every != 0 {
		parts = append(parts, "every "+triggers.Every.String())
	}
	if len(triggers.On) > 0 {
		events := make([]string, 0, len(triggers.On))
		for _, event := range triggers.On {
			events = append(events, string(event))
		}
		parts = append(parts, "on "+strings.Join(events, ", "))
	}
	return "passes over its lane " + strings.Join(parts, " and ")
}
