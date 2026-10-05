package cli

// The provider side of a side conversation: one read-only invocation that puts a
// question to a role on its own side thread and brings back what it said.
//
// It is here rather than in `internal/sidestream` for the reason the exchange's
// answering voice is here: starting a provider needs the project's
// configuration — which agent fills the role, which model it is served under,
// which account pays for it — and the package that holds a side conversation
// must not depend on any of that. What that package holds is the record, the
// lease, the turn cap, and the boundary the reply is read against; what this
// holds is how one invocation is made.
//
// It is the harness's own hand, like every other voice here. No role reaches it,
// nothing configures its way into it, and the authority it carries is none:
// `harness-is-the-only-role-invoker` is satisfied because the only thing that
// calls this is the runner, and the only thing that calls the runner is the
// harness.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/agentcontext"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// sideTurnTimeout bounds one side turn. It is the exchange round's bound rather
// than a conversation turn's: a side thread answers a question in prose with
// optional read-only inspection. A turn still running past this leaves the asker
// waiting for no reason they can see.
const sideTurnTimeout = 5 * time.Minute

// sideVoice answers a side thread's turns under the agent configured for the
// role, which is the same resolution `yoyo agent chat` makes — so the architect
// that answers beside its main thread is the architect an operator would have
// addressed. A role nobody configured is not an empty answer: it is refused, and
// the turn records that there was nobody to ask.
type sideVoice struct {
	config     config.Config
	provider   chat.Backend // injected by tests that do not exercise adapter construction
	runner     execution.ProcessRunner
	repository string
	// usageLimits is where a provider refusing this turn for want of capacity is
	// written down. A side turn has no run to park and no conversation of its own
	// to fail at somebody's terminal, so without this an exhausted limit met here
	// would stop a side thread and leave no trace anywhere.
	usageLimits *runstate.UsageLimitStore
	// spend is where what a turn costs is written down. A side turn is a provider
	// invocation like any other and the design prices it beside the
	// conversations, so it is charged to the side stream — its own subject on the
	// cost line, because a line naming the main thread would put a side turn's
	// cost on a conversation that never took it.
	spend     spend.Log
	productID domain.ProductID
	// stateRoot is where the answering account's provider home is found. A voice
	// built without one answers where the machine is already signed in, which is
	// the single-account arrangement and is what a test wiring the voice directly
	// gets.
	stateRoot    string
	redactValues []string
	// clock is what the turn reads the time from, so the failover seam is
	// testable on a fixed clock. A voice built without one reads the wall clock.
	clock func() time.Time
}

func (v sideVoice) now() time.Time {
	if v.clock == nil {
		return time.Now().UTC()
	}
	return v.clock().UTC()
}

// Answer takes one side turn. It satisfies sidestream.Voice.
func (v sideVoice) Answer(ctx context.Context, question sidestream.Question) (sidestream.Spoken, error) {
	name := strings.TrimSpace(question.Agent)
	if name == "" {
		name = agentNameForRole(v.config, question.Role)
	}
	if name == "" {
		return sidestream.Spoken{}, fmt.Errorf("no %s agent is configured, so there is nobody to hold a side thread", question.Role)
	}
	agent, configured := v.config.Agents[name]
	if !configured {
		return sidestream.Spoken{}, fmt.Errorf("no agent named %s is configured, so %s names nobody", name, question.StreamID)
	}
	// Whether this agent holds side threads at all is the per-agent knob, and it
	// is read here because this is where a side turn becomes a provider
	// invocation. An agent left at the default queues, which is what every agent
	// did before the knob existed, so a project acquires side threads by writing
	// the key rather than by upgrading. What the knob never decides is what the
	// answer may do: that is `sidestream.Permitted` and the role's own authority
	// narrowed by it, neither of which reads configuration.
	if !v.config.AgentHoldsSideThreads(name) {
		return sidestream.Spoken{}, fmt.Errorf("the %s agent %s is configured for %q conversations, so it holds no side threads; %s has nobody to answer it",
			question.Role, name, v.config.AgentConversationMode(name), question.StreamID)
	}

	prompt := execution.NewRedactor(v.redactValues...).Redact(renderSideQuestion(question))
	// The turn is answered on the endpoint the agent is configured for, under the
	// account its main conversation is held under: a side thread is that role
	// speaking, and what it costs belongs on that role's subscription. The
	// endpoint names the provider, the adapter that reaches it, and the model
	// beside that account, which is what a substitution is checked against below.
	providers, err := v.config.ProviderRegistry()
	if err != nil {
		return sidestream.Spoken{}, fmt.Errorf("resolve the providers the %s agent %s may be served by: %w", question.Role, name, err)
	}
	choice, err := v.config.AgentEndpoint(providers, v.stateRoot, name)
	if err != nil {
		return sidestream.Spoken{}, fmt.Errorf("resolve the endpoint the %s agent %s answers on: %w", question.Role, name, err)
	}
	if err := providers.EligibleFor(choice.Endpoint, question.Role); err != nil {
		return sidestream.Spoken{}, fmt.Errorf("the %s agent %s cannot answer on its configured endpoint: %w", question.Role, name, err)
	}
	account := choice.Account
	if question.SessionID != "" && (question.SessionBackend != choice.Endpoint.Provider || question.SessionAccountAlias != account.Alias) {
		return sidestream.Spoken{}, fmt.Errorf("side thread %s has a session from another or unknown provider/account; start a new side thread because its prior questions cannot be rebuilt from the durable record", question.StreamID)
	}
	answeringProvider := v.provider
	if v.runner != nil {
		answeringProvider = providerBackendIn(v.config, choice.Endpoint.Provider, v.runner, account.Directory)
	}
	// The turn goes through the meter, so what it spends is one line in the cost
	// log beside every other provider invocation the harness makes, charged to the
	// side stream because that is the record it belongs to. The phase is the
	// conversation's, which is where the design says a side thread is listed.
	provider := spend.Metered{
		Provider: answeringProvider,
		Log:      v.spend,
		Attribution: spend.Attribution{
			ProductID:      v.productID,
			Agent:          name,
			Phase:          runstate.SpendPhaseConversation,
			AccountAlias:   account.Alias,
			ConfigRevision: v.config.Revision(),
			Backend:        agent.Backend,
			SideStreamID:   question.StreamID,
		},
	}
	// The turn is served by the agent's permitted alternate where its own model
	// has no capacity, exactly as its main conversation's turn is: a role that can
	// still speak on its main thread and not beside it would be the same stall
	// moved one seam along. The failover sits outside the meter so each attempt is
	// priced against the model that attempt asked for.
	result, served, err := modelfailover.Serve(ctx, provider, backend.RunRequest{
		// The side stream is the record this invocation belongs to, so it is what
		// the provider is told the invocation is. Its events are named for it and
		// land in its own log, which is what keeps the two transcripts apart.
		RunID:            question.StreamID,
		Role:             question.Role,
		WorkingDirectory: v.repository,
		Prompt:           prompt,
		SystemPrompt:     chat.SidePrompt(question.Role, agent.Persona.Text),
		SessionID:        question.SessionID,
		Model:            agent.Model,
		// The agent's effort level, kept by whichever model serves the turn.
		Effort: v.config.InvocationEffort(agent, agent.Model),
		// The adapter enforces the role's read-only access. A side reply
		// carries advice rather than validation results or authority to act.
		AllowedTools:     []string{},
		Timeout:          sideTurnTimeout,
		LastSequence:     question.LastSequence,
		RedactValues:     v.redactValues,
		EventSink:        question.Events,
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	}, v.failoverPolicy(question, name, choice.Endpoint, providers))
	// What served the turn travels back whether or not there was an answer,
	// because it is a fact about the invocation rather than about what came back,
	// and the stream is pinned to it either way. The build is this process's own:
	// a resident holding side threads goes on running the binary it was started
	// with.
	spoken := sidestream.Spoken{
		SessionID: result.SessionID,
		CostUSD:   result.CostUSD,
		Backend:   agent.Backend,
		// The model that actually asked, which is the configured one unless the
		// permitted alternate served the turn. Recording the configured selector
		// would leave the stream naming a model that refused it.
		Model:             served.Model,
		ResolvedModel:     result.ResolvedModel,
		Effort:            served.Effort,
		ResolvedEffort:    result.ResolvedEffort,
		EffortDescription: result.EffortDescription,
		EffortReported:    result.EffortReported,
		AccountAlias:      account.Alias,
		ConfigRevision:    v.config.Revision(),
		Build:             buildinfo.Commit(),
		LastEvent:         result.LastEvent,
	}
	// The refusal is recorded before the turn is failed, because it is a fact
	// about the whole product rather than about this side thread. Failing to
	// record it never replaces the refusal in what the turn reports: the turn is
	// spent either way, and the stream says so.
	refusal := v.noteSideUsageLimit(question, result, err, served.Model)
	switch {
	case err != nil:
		return spoken, errors.Join(fmt.Errorf("the %s could not be reached on %s: %w",
			chat.RoleTitle(question.Role), question.StreamID, err), refusal)
	case result.IsError:
		return spoken, errors.Join(fmt.Errorf("the %s reported failure on %s: %s",
			chat.RoleTitle(question.Role), question.StreamID, result.DescribeFailure()), refusal)
	}
	spoken.Answer = result.FinalText
	return spoken, nil
}

// noteSideUsageLimit records a provider refusal this turn met, exactly as a
// conversation turn records one — the model it was refused on included, so the
// refusal can be read back as part of a hold over every role — and reports only
// what went wrong recording it.
func (v sideVoice) noteSideUsageLimit(question sidestream.Question, result backend.RunResult, err error, model string) error {
	if result.UsageLimit == nil || (err == nil && !result.IsError) || v.usageLimits == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     v.productID,
		At:            v.now(),
		Waiting:       v.waitingOn(question),
		Kind:          result.UsageLimit.Kind,
		Model:         strings.TrimSpace(model),
	}
	if !result.UsageLimit.ResetsAt.IsZero() {
		resetsAt := result.UsageLimit.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := v.usageLimits.Record(exhaustion); err != nil {
		return fmt.Errorf("record the provider's refusal: %w", err)
	}
	return nil
}

// failoverPolicy is what a side turn may be served by when the model the agent
// is configured for will not take it, and where the substitution is written
// down. An agent that has not enabled failover produces the zero policy, which
// is failover off: one invocation, under the configured model.
//
// The endpoint the turn is on and the providers this project names travel with
// it, so a substitution is checked against the role's tool posture before it is
// made rather than after the turn has already moved.
func (v sideVoice) failoverPolicy(question sidestream.Question, name string, endpoint backend.Endpoint, providers *backend.Registry) modelfailover.Policy {
	// The alternate only where it stays on the provider this side thread is held
	// on, for the reason an exchange round reads the same answer: a side turn is
	// made on the agent's own endpoint and has no way to cross, and asking that
	// provider for another provider's model would fail on a selector nobody there
	// has heard of.
	alternate := v.config.AgentFailoverModelWithinProvider(name)
	if alternate == "" {
		return modelfailover.Policy{}
	}
	policy := modelfailover.Policy{
		Alternate: alternate,
		Now:       v.now,
		// How long a refusal that named no reset time stands before the agent's own
		// model is asked again, which is the same interval a run probes one on.
		UnknownResetPause: v.config.Execution.UsageLimitUnknownResetPause.Duration(),
		ProductID:         v.productID,
		Waiting:           v.waitingOn(question),
		Endpoint:          endpoint,
		Role:              question.Role,
		Eligibility:       providers,
	}
	if v.usageLimits != nil {
		policy.Windows = v.usageLimits
	}
	return policy
}

// waitingOn is the one sentence a refusal and a substitution both say about this
// turn. It is written once because the two are halves of the same fact — what
// would have stopped, and that something served it anyway — and a reader of
// either should not have to reconcile two descriptions of one side thread.
func (v sideVoice) waitingOn(question sidestream.Question) string {
	return fmt.Sprintf("the %s on side thread %s, held beside conversation %s",
		chat.RoleTitle(question.Role), question.StreamID, question.Conversation)
}

// sideThreads assembles the runner that holds this agent's side conversations,
// over the same durable stores every other process addresses: the side stream
// store is the record and the lease, the voice above is the turn, and the merge
// is the agent-context write — so a thread concluded here is one the main
// conversation's next turn reads, in this process or in any other.
//
// It is the one place a runner is built outside a test, and it is the harness's
// own hand: no role asks for a side thread, and the runner it is handed carries
// no authority the role does not already have on its main thread — less, since
// `sidestream.Permits` takes every action away.
func (p preparedChat) sideThreads() (sidestream.Runner, error) {
	cfg := p.parts.config
	streams, err := runstate.NewSideStreamStore(p.parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return sidestream.Runner{}, err
	}
	return sidestream.Runner{
		Store: streams,
		// The stream's own lease and never the main thread's, which is what lets a
		// side turn be taken while the main conversation is held.
		Leases: streams,
		Voice: sideVoice{
			config:       cfg,
			provider:     p.provider,
			runner:       p.runner,
			repository:   p.parts.repository,
			usageLimits:  p.parts.usageLimits,
			spend:        p.parts.spend,
			productID:    cfg.Product.ID,
			stateRoot:    p.parts.stateRoot,
			redactValues: p.parts.redactValues,
		},
		// The merge is the memory write and nothing beside it: what a concluded
		// thread worked out goes through `agentcontext`, where it is refused for a
		// role that keeps no memory, redacted, budgeted, and numbered, and the
		// stream is recorded as ended only once that write is durable.
		Merge:        agentcontext.Merger{Streams: streams, Memory: p.memories},
		ProductID:    cfg.Product.ID,
		RepositoryID: string(cfg.Product.RepositoryID),
	}, nil
}

// sideThreadFlagProblems is what `--side-thread` may be combined with: a
// message, and nothing that replaces or converses with the main conversation.
func sideThreadFlagProblems(command, sideThread, message string, fresh bool) error {
	if strings.TrimSpace(sideThread) == "" {
		return nil
	}
	switch {
	case !sidestream.ValidID(strings.TrimSpace(sideThread)):
		return fmt.Errorf("%s --side-thread %q does not name a side thread; one is `side-` and thirty-two hex digits, as the answer that opened it printed", command, sideThread)
	case message == "":
		return fmt.Errorf("%s --side-thread requires --message: a side thread is a bounded number of turns, not a prompt to sit at", command)
	case fresh:
		return fmt.Errorf("%s --side-thread cannot be combined with --new: a side thread is held beside the recorded conversation, and --new replaces it", command)
	default:
		return nil
	}
}

// mayGoAside reports a message that is answered on a side thread if the main
// conversation turns out to be held. It is the agent's own knob read here, and
// the knob is the whole of the choice: an agent that queues never has a side
// thread opened for it however busy it is.
//
// Three things never go aside whatever the knob says, because each has to reach
// the main thread to mean anything. A command is the harness's to carry out
// against the conversation. A decision or an answer settles something the main
// conversation is waiting on, and a side thread holds none of it. And a message
// with `--new` is about to replace the recorded conversation, which is not a
// thread anything can be held beside.
func (r conversationRequest) mayGoAside(p preparedChat) bool {
	if r.message == "" || r.fresh || chat.IsCommand(r.message) || chat.IsDecision(r.message) {
		return false
	}
	return p.parts.config.AgentHoldsSideThreads(p.name)
}

// sideThreadOutput is what a message answered on a side thread reports about the
// thread beside the reply itself, in the machine-readable form.
type sideThreadOutput struct {
	// Stream is the side thread's own identifier, which is what a later message
	// continues it by and what its merge is named for.
	Stream string `json:"stream"`
	// Conversation is the main thread it is held beside.
	Conversation string `json:"conversation"`
	Turn         int    `json:"turn"`
	MaxTurns     int    `json:"max_turns"`
	// Outcome is how the thread ended, and is empty while it is still open. A
	// thread that ended has merged what it reached into the agent's memory.
	Outcome sidestream.Outcome `json:"outcome,omitempty"`
	// Tentative reports an answer that promised something, and Commitments are the
	// promises. Every one is best effort until the main conversation ratifies it,
	// which is the design's requirement of any surface carrying a side thread's
	// answer rather than a nicety.
	Tentative   bool     `json:"tentative"`
	Commitments []string `json:"commitments,omitempty"`
	CostUSD     float64  `json:"cost_usd"`
}

// askAside puts one message to the role on a side thread — a new one beside the
// main conversation named, or the one the request continues — and reports what
// came back, saying that it came from a side thread and what that means.
//
// The operator's pause covers this exactly as it covers a turn on the main
// thread, and it is read here rather than in the runner because the runner is
// the harness's own and knows nothing of the operator's switches. Nothing else
// about the turn is decided here: the record, the lease, the cap, and the merge
// are the runner's, and what the reply may carry is the contract's.
func askAside(ctx context.Context, p preparedChat, request conversationRequest, conversation string, stdout, stderr io.Writer) int {
	role := p.identity.Role
	if hold, held, err := p.parts.holds.Held(); err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil,
			fmt.Errorf("read whether the operator has paused harness activity: %w", err))
	} else if held {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, &chat.OperatorHoldError{Hold: hold})
	}
	runner, err := p.sideThreads()
	if err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, err)
	}
	// Who is asked travels on every question, a continuation included: the agent
	// this command addressed, under whose account and provider the turn is served.
	// A stream another agent holds is refused by the runner on that, before a turn
	// is spent, rather than continued on the wrong account and merged into the
	// wrong memory.
	ask := sidestream.Ask{Stream: request.sideThread, Agent: p.name, Role: role, Question: request.message}
	if request.sideThread == "" {
		ask.Conversation = conversation
		ask.Topic = sideTopic(request.message)
		fmt.Fprintf(stderr, "the %s is mid-turn, so this is answered beside that turn on a side thread\n", chat.RoleTitle(role))
	}
	answer, err := runner.Put(ctx, ask)
	if err != nil {
		// The prose travels with the failure where there was any: a reply whose
		// block the harness refused is still an answer somebody paid for, and the
		// turn is spent either way. So does the thread's state, where the failure
		// left one — a thread the refusal concluded, or one still open to continue.
		if answer.Stream.ID != "" {
			err = fmt.Errorf("side thread %s: %w", answer.Stream.ID, err)
		}
		if request.jsonOutput {
			output := chatOutput{Reply: answer.Prose, Error: err.Error()}
			if answer.Stream.ID != "" {
				aside := sideThreadOf(answer)
				output.SideThread = &aside
			}
			if code := writeJSON(stdout, stderr, output); code != 0 {
				return code
			}
			return 1
		}
		if answer.Prose != "" {
			fmt.Fprintln(stdout, answer.Prose)
		}
		return reportChatFailure(stdout, stderr, false, role, nil, err)
	}
	aside := sideThreadOf(answer)
	if request.jsonOutput {
		return writeJSON(stdout, stderr, chatOutput{Reply: answer.Prose, SideThread: &aside})
	}
	fmt.Fprintln(stdout, answer.Prose)
	printSideThread(stdout, role, p.name, aside)
	return 0
}

// sideThreadOf is what one side turn's answer reports about its thread.
func sideThreadOf(answer sidestream.Answer) sideThreadOutput {
	stream := answer.Stream
	return sideThreadOutput{
		Stream:       stream.ID,
		Conversation: stream.Conversation,
		Turn:         stream.Turns,
		MaxTurns:     stream.MaxTurns,
		Outcome:      stream.Outcome,
		Tentative:    answer.Tentative(),
		Commitments:  answer.Commitments,
		CostUSD:      stream.CostUSD,
	}
}

// printSideThread says where an answer came from and what it is worth, which the
// design makes the carrying surface's job: a side thread's answer is judgment
// with no action behind it, and what it promised is tentative until the main
// conversation ratifies it.
func printSideThread(writer io.Writer, role domain.AgentRole, agent string, aside sideThreadOutput) {
	title := chat.RoleTitle(role)
	fmt.Fprintf(writer, "\nAnswered on side thread %s, beside the %s's conversation %s while that was mid-turn. This is the %s's judgment and not an action: nothing was created, changed, admitted, or decided by it.\n",
		aside.Stream, title, aside.Conversation, title)
	if aside.Tentative {
		fmt.Fprintf(writer, "It tentatively committed to the following. Each is best effort until the %s's main conversation ratifies or adjusts it, which is the only path that acts:\n", title)
		for _, commitment := range aside.Commitments {
			fmt.Fprintf(writer, "  - %s\n", commitment)
		}
	}
	switch aside.Outcome {
	case sidestream.OutcomeConcluded:
		fmt.Fprintf(writer, "The thread concluded after %d of %d turn(s), and what it worked out is merged into the %s's memory, where the main conversation reads it on its next turn.\n",
			aside.Turn, aside.MaxTurns, title)
	case sidestream.OutcomeSpent:
		fmt.Fprintf(writer, "The thread reached its limit of %d turn(s) without saying it had finished; what it had reached is merged into the %s's memory anyway, where the main conversation reads it on its next turn.\n",
			aside.MaxTurns, title)
	default:
		fmt.Fprintf(writer, "The thread is still open, with %d of %d turn(s) left; continue it with `%s --side-thread %s --message ...`.\n",
			aside.MaxTurns-aside.Turn, aside.MaxTurns, continueSideCommand(role, agent), aside.Stream)
	}
	fmt.Fprintf(writer, "side thread cost so far: $%.4f\n", aside.CostUSD)
}

// continueSideCommand is the command that reaches this agent again, in the form
// the operator would type: the product manager's conversation is `yoyo chat`,
// and every other agent is reached by name.
func continueSideCommand(role domain.AgentRole, agent string) string {
	if role == domain.RoleProductManager && agent == string(domain.RoleProductManager) {
		return "yoyo chat"
	}
	return "yoyo agent chat " + agent
}

// sideTopic is what a side thread opened for one message is recorded as being
// about: the message's first line, held to what a topic may be. The topic is a
// label on the record and in the merge's subject, so a long one is cut rather
// than refused — a question lost over the length of its own first line would be
// the thread never opened.
func sideTopic(message string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	first = strings.TrimSpace(first)
	if len(first) <= sidestream.MaxTopicBytes {
		return first
	}
	// Cut on a rune boundary rather than a byte one, for the reason the merge's
	// subject is: a label a person reads with half a character on the end is not a
	// shorter label but a broken one.
	const ellipsis = "..."
	room := sidestream.MaxTopicBytes - len(ellipsis)
	kept := 0
	for index := range first {
		if index > room {
			break
		}
		kept = index
	}
	return strings.TrimSpace(first[:kept]) + ellipsis
}

// renderSideQuestion is what the role holding a side thread is sent.
//
// It says where the thread has got to against its cap out of the stream's own
// durable record, which is why the contract does not state the cap as a number:
// a role being asked to land something and a role with turns to spare are being
// asked different things, and the difference is a fact about this thread rather
// than about the configuration.
func renderSideQuestion(question sidestream.Question) string {
	var rendered strings.Builder
	rendered.WriteString("# A question on your side thread\n\n")
	fmt.Fprintf(&rendered, "This is side thread %s, turn %d of the %d it is allowed, held beside your main conversation %s while that conversation is busy.\n\n",
		question.StreamID, question.Turn, question.MaxTurns, question.Conversation)
	fmt.Fprintf(&rendered, "It was opened about: %s\n\n", strings.TrimSpace(question.Topic))
	rendered.WriteString("## The question\n\n")
	rendered.WriteString(strings.TrimSpace(question.Question) + "\n")
	rendered.WriteString("\nAnswer it in prose. Nothing else you write is carried out here.\n")
	return rendered.String()
}
