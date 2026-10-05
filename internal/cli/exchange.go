package cli

// The operator's view of what the roles have asked each other, and the harness's
// hand that carries a question from one to the other.
//
// Both halves are here because both are the harness's rather than any role's.
// The voice below starts the provider that answers, under a prompt that gives it
// read-only access and no action authority; the command above reads the durable
// threads. What makes the channel safe to have at all is that neither role reaches either one.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// exchangeAnswerTimeout bounds one answering invocation. It is shorter than a
// conversation turn's because an exchange round is one question answered in
// prose with optional read-only inspection. A round still running past this leaves
// the asking conversation waiting for no reason it can see.
const exchangeAnswerTimeout = 5 * time.Minute

func runExchange(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printExchangeUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listExchanges(args[1:], stdout, stderr)
	case "show":
		return showExchange(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown exchange command %q\n\n", args[0])
		printExchangeUsage(stderr)
		return 2
	}
}

func listExchanges(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("exchange list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "exchange list does not accept positional arguments; use `yoyo exchange show <id>` for one exchange")
		return 2
	}

	store, productID, err := exchangeStore(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	recorded, err := store.List()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product":   productID,
			"exchanges": recorded,
		})
	}
	if len(recorded) == 0 {
		fmt.Fprintln(stdout, "exchanges: no role has asked another one anything.")
		return 0
	}
	var spent float64
	for _, one := range recorded {
		spent += one.CostUSD()
	}
	fmt.Fprintf(stdout, "%d exchange(s) for %s, costing $%.4f in total:\n", len(recorded), productID, spent)
	for _, one := range recorded {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, one.Render())
	}
	return 0
}

func showExchange(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("exchange show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "name the exchange to show, as `yoyo exchange show <id>`; `yoyo exchange list` names them")
		return 2
	}

	store, _, err := exchangeStore(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// A prefix names one exchange the way it names one directive, because nobody
	// types thirty-two hex digits out of a listing.
	found, err := store.Find(positional[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, found)
	}
	fmt.Fprint(stdout, found.RenderThread())
	return 0
}

func exchangeStore(configPath string) (*runstate.ExchangeStore, string, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, "", err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, "", err
	}
	store, err := runstate.NewExchangeStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, "", err
	}
	return store, string(resolved.Config.Product.ID), nil
}

// exchangeVoice is the answering half of the channel: one read-only provider
// invocation per round, under the answering role's identity and the harness's
// own boundary.
//
// It answers under the agent configured for the role, which is the same
// resolution `yoyo agent chat` makes, so the architect that answers an exchange
// is the architect an operator would have addressed. A role nobody configured is
// not an empty answer: it is refused, and the round records that there was
// nobody to ask.
type exchangeVoice struct {
	config     config.Config
	provider   chat.Backend // injected by tests that do not exercise adapter construction
	runner     execution.ProcessRunner
	repository string
	// usageLimits is where a provider refusing this round for want of capacity is
	// written down. An answering round has no run to park and no conversation of
	// its own to fail at somebody's terminal, so without this an exhausted limit
	// met here would stop an exchange and leave no trace anywhere — and an
	// exhausted limit is hours in which nothing happens anywhere, which is exactly
	// what somebody not watching this conversation needs to be told.
	usageLimits *runstate.UsageLimitStore
	// spend is where what a round costs is written down. An answering round is a
	// provider invocation with neither a run nor a conversation behind it, so
	// without this it would be the one invocation the harness makes whose cost
	// nothing durable records at all.
	//
	// It is the log's interface rather than the store itself, as it is everywhere
	// else the harness wires one. A store built and found wanting is a build
	// failure rather than a nil handed on, so the type is the interface for
	// uniformity rather than for safety: one hazard guarded at three sites and not
	// the fourth is a hazard nobody can reason about.
	spend     spend.Log
	productID domain.ProductID
	// stateRoot is where the answering account's provider home is found. A voice
	// built without one answers where the machine is already signed in, which is
	// the single-account arrangement and is what a test wiring the voice directly
	// gets.
	stateRoot    string
	redactValues []string
	// clock is what the round reads the time from: when a refusal happened, and
	// whether a window an earlier refusal described still stands. It is a field
	// rather than a call to time.Now so the failover seam is testable on a fixed
	// clock, which is how the rest of this behaviour is tested. A voice built
	// without one reads the wall clock.
	clock func() time.Time
}

func (v exchangeVoice) now() time.Time {
	if v.clock == nil {
		return time.Now().UTC()
	}
	return v.clock().UTC()
}

func (v exchangeVoice) Answer(ctx context.Context, question exchange.Question) (exchange.Spoken, error) {
	name := agentNameForRole(v.config, question.Role)
	if name == "" {
		return exchange.Spoken{}, fmt.Errorf("no %s agent is configured, so there is nobody to ask", question.Role)
	}
	agent := v.config.Agents[name]

	prompt := execution.NewRedactor(v.redactValues...).Redact(renderQuestion(question))
	// The round is answered on the endpoint the answering agent is configured for,
	// under the account its conversation is held under: an exchange is that role
	// speaking, and what it costs belongs on that role's subscription. The
	// endpoint names the provider, the adapter that reaches it, and the model
	// beside that account, which is what a substitution is checked against below.
	providers, err := v.config.ProviderRegistry()
	if err != nil {
		return exchange.Spoken{}, fmt.Errorf("resolve the providers the %s agent %s may be served by: %w", question.Role, name, err)
	}
	choice, err := v.config.AgentEndpoint(providers, v.stateRoot, name)
	if err != nil {
		return exchange.Spoken{}, fmt.Errorf("resolve the endpoint the %s agent %s answers on: %w", question.Role, name, err)
	}
	if err := providers.EligibleFor(choice.Endpoint, question.Role); err != nil {
		return exchange.Spoken{}, fmt.Errorf("the %s agent %s cannot answer on its configured endpoint: %w", question.Role, name, err)
	}
	account := choice.Account
	if question.SessionBackend != choice.Endpoint.Provider || question.SessionAccountAlias != account.Alias {
		// Every prompt carries the earlier rounds, so a new endpoint rebuilds
		// from the record instead of receiving another endpoint's session ID.
		question.SessionID = ""
	}
	answeringProvider := v.provider
	if v.runner != nil {
		answeringProvider = providerBackendIn(v.config, choice.Endpoint.Provider, v.runner, account.Directory)
	}
	// The round goes through the meter, so what it spends is one line in the cost
	// log beside every other provider invocation the harness makes, charged to
	// the exchange because that is the only record it belongs to.
	//
	// The alias the line is charged to is the one the round was actually answered
	// under, rather than the configuration's single account: under a pool there is
	// no single account, and a cost line naming none is a line nothing can
	// attribute.
	provider := spend.Metered{
		Provider: answeringProvider,
		Log:      v.spend,
		Attribution: spend.Attribution{
			ProductID:      v.productID,
			Agent:          name,
			Phase:          runstate.SpendPhaseExchange,
			AccountAlias:   account.Alias,
			ConfigRevision: v.config.Revision(),
			Backend:        agent.Backend,
			ExchangeID:     question.ExchangeID,
		},
	}
	// The round is served by the answering agent's permitted alternate where its
	// own model has no capacity, exactly as that agent's conversation turn is. An
	// exchange is that role speaking, so a role that can still speak in its own
	// conversation and not when another role asks it something would be the same
	// stall moved one seam along. The failover sits outside the meter so each
	// attempt is priced against the model that attempt asked for.
	result, served, err := modelfailover.Serve(ctx, provider, backend.RunRequest{
		// The exchange is the record this invocation belongs to, so it is what the
		// provider is told the invocation is: an answering round has no run and no
		// conversation of its own.
		RunID:            question.ExchangeID,
		Role:             question.Role,
		WorkingDirectory: v.repository,
		Prompt:           prompt,
		SystemPrompt:     chat.AnsweringPrompt(question.Role, agent.Persona.Text),
		SessionID:        question.SessionID,
		Model:            agent.Model,
		// The agent's effort level, kept by whichever model serves the turn.
		Effort: v.config.InvocationEffort(agent, agent.Model),
		// The adapter enforces the role's read-only access, as on its main
		// conversation. The answering reply carries no authority to act.
		AllowedTools:     []string{},
		Timeout:          exchangeAnswerTimeout,
		RedactValues:     v.redactValues,
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	}, v.failoverPolicy(question, name, choice.Endpoint, providers))
	// What served the round travels back with what it cost, so the exchange record
	// pins the invocation to a backend, a model, an account, a configuration, and
	// the harness that made the call rather than to a provider session that
	// outlives none of them. The alias is the one the round was actually answered
	// under, which under a pool is the answering agent's own rather than a
	// configuration-wide account there is none of — the same alias its cost line is
	// charged to. The build is this process's own, because a resident conducting an
	// exchange goes on running the binary it was started with.
	spoken := exchange.Spoken{
		Agent:     name,
		SessionID: result.SessionID,
		CostUSD:   result.CostUSD,
		Backend:   agent.Backend,
		// The model that actually asked, which is the configured one unless the
		// permitted alternate served the round. Recording the configured selector
		// would leave the exchange record naming a model that refused it.
		Model:          served.Model,
		ResolvedModel:  result.ResolvedModel,
		Effort:         served.Effort,
		ResolvedEffort: result.ResolvedEffort,
		EffortReported: result.EffortReported,
		AccountAlias:   account.Alias,
		ConfigRevision: v.config.Revision(),
		Build:          buildinfo.Commit(),
	}
	// The refusal is recorded before the round is failed, because it is a fact
	// about the whole product rather than about this exchange. Failing to record
	// it never replaces the refusal itself in what the round reports: the round is
	// spent either way, and the exchange says so.
	refusal := v.noteUsageLimit(question, result, err, served.Model)
	switch {
	case err != nil:
		return spoken, errors.Join(fmt.Errorf("the %s could not be reached: %w", chat.RoleTitle(question.Role), err), refusal)
	case result.IsError:
		return spoken, errors.Join(
			fmt.Errorf("the %s reported failure: %s", chat.RoleTitle(question.Role), result.DescribeFailure()),
			refusal)
	}
	spoken.Answer = result.FinalText
	return spoken, nil
}

// noteUsageLimit records a provider refusal this round met, exactly as a
// conversation turn records one, and reports only what went wrong recording it.
// A round that was not refused, and a voice with nowhere to record one, both
// record nothing and say nothing. The model is the one the round was refused
// on, for the reason a conversation turn records it: a refusal that names its
// model can be read back as part of a hold over every role, and one that names
// none cannot.
func (v exchangeVoice) noteUsageLimit(question exchange.Question, result backend.RunResult, err error, model string) error {
	if result.UsageLimit == nil || (err == nil && !result.IsError) || v.usageLimits == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     v.productID,
		At:            v.now(),
		Waiting: fmt.Sprintf("the %s answering exchange %s, asked by the %s",
			chat.RoleTitle(question.Role), question.ExchangeID, chat.RoleTitle(question.Asker)),
		Kind:  result.UsageLimit.Kind,
		Model: strings.TrimSpace(model),
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

// failoverPolicy is what an answering round may be served by when the model it
// would ask for will not take it — the pinned version the provider has not got,
// or the configured model whose window is closed — and where either substitution
// is written down. An agent that has pinned no version and enabled no failover
// produces the zero policy, which is both mechanisms off: one invocation, under
// the configured model, exactly as before.
//
// The endpoint the round is on and the providers this project names travel with
// it, so a substitution is checked against the role's tool posture before it is
// made rather than after the round has already moved.
func (v exchangeVoice) failoverPolicy(question exchange.Question, name string, endpoint backend.Endpoint, providers *backend.Registry) modelfailover.Policy {
	// The alternate only where it stays on the provider this round is answered
	// on. An exchange has no way to cross — it is answered on the agent's own
	// endpoint, and asking that provider for another provider's model would fail on
	// a selector nobody there has heard of, at the moment the fallback was meant to
	// save the round. An agent whose alternate crosses therefore answers rounds
	// exactly as it did before failover existed.
	alternate := v.config.AgentFailoverModelWithinProvider(name)
	version := v.config.AgentModelVersion(name)
	if alternate == "" && version == "" {
		return modelfailover.Policy{}
	}
	policy := modelfailover.Policy{
		Alternate: alternate,
		// The exact version this agent's rounds ask for, empty for every agent that
		// pins none. An exchange is that role speaking, so a round is answered by the
		// same version its conversation is held on rather than by whatever the family
		// alias floats to at the moment somebody asks it something.
		Version: version,
		Now:     v.now,
		// How long a refusal that named no reset time stands before the answering
		// agent's own model is asked again, which is the same interval a run probes
		// one on. Without it every round would re-ask an exhausted model and
		// announce the substitution again with it.
		UnknownResetPause: v.config.Execution.UsageLimitUnknownResetPause.Duration(),
		ProductID:         v.productID,
		// The same sentence a refusal here writes, because it is the same thing
		// that would have stopped — and what makes this the other half of that fact
		// is that something served it anyway. It carries no work item and no
		// conversation for the reason the refusal beside it carries none: an
		// answering round belongs to an exchange, and an exchange is not one of the
		// two references this record holds. Both are therefore addressed to the
		// product line, which is where a reader of either already looks.
		Waiting: fmt.Sprintf("the %s answering exchange %s, asked by the %s",
			chat.RoleTitle(question.Role), question.ExchangeID, chat.RoleTitle(question.Asker)),
		Endpoint:    endpoint,
		Role:        question.Role,
		Eligibility: providers,
	}
	if v.usageLimits != nil {
		policy.Windows = v.usageLimits
	}
	return policy
}

// renderQuestion is what the answering role is sent. The thread before this
// round is included on the first invocation of a session and left out afterwards
// only in the sense that the provider already holds it: it is sent every time,
// because a session the provider dropped would otherwise answer round four with
// no idea what rounds one to three said.
func renderQuestion(question exchange.Question) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "# The %s is asking you something\n\n", chat.RoleTitle(question.Asker))
	fmt.Fprintf(&rendered, "This is exchange %s, round %d of the %d it is allowed.\n\n",
		question.ExchangeID, question.Round, question.MaxRounds)
	for _, earlier := range question.Earlier {
		fmt.Fprintf(&rendered, "## Round %d\n\n%s asked: %s\n\n",
			earlier.Number, chat.RoleTitle(question.Asker), strings.TrimSpace(earlier.Question))
		if context := strings.TrimSpace(earlier.Context); context != "" {
			fmt.Fprintf(&rendered, "Their framing: %s\n\n", context)
		}
		switch {
		case strings.TrimSpace(earlier.Answer) != "":
			fmt.Fprintf(&rendered, "You answered: %s\n\n", strings.TrimSpace(earlier.Answer))
		default:
			rendered.WriteString("That round produced no answer.\n\n")
		}
	}
	rendered.WriteString("## The question\n\n")
	rendered.WriteString(strings.TrimSpace(question.Question) + "\n")
	if context := strings.TrimSpace(question.Context); context != "" {
		fmt.Fprintf(&rendered, "\nTheir framing, which is what they think rather than evidence: %s\n", context)
	}
	rendered.WriteString("\nAnswer it in prose. Nothing else you write will be carried out.\n")
	return rendered.String()
}

// conversationExchanges is the channel as a conversation reaches it, or nothing
// where the role holding that conversation is not on the channel. Wiring it only
// for the roles that may ask is the same decision the triage budgets are wired
// by: a capability a role has no authority for is not one its conversation
// should be able to reach at all.
func conversationExchanges(parts components, role domain.AgentRole, provider chat.Backend, runner execution.ProcessRunner) chat.Exchanges {
	authority, known := chat.AuthorityFor(role)
	if !known || !authority.Asks {
		return nil
	}
	store, err := runstate.NewExchangeStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		// A store that cannot be built is a conversation with no channel, which
		// refuses an ask plainly. It is not a conversation that fails to open: the
		// operator came here to talk about the product.
		return nil
	}
	return exchange.Conductor{
		Store: store,
		// The lease is what holds one exchange to one round at a time. Two
		// conversations open at once are two processes, and without it the second
		// would write over the first one's round.
		Leases: store,
		Voice: exchangeVoice{
			config:       parts.config,
			provider:     provider,
			runner:       runner,
			repository:   parts.repository,
			usageLimits:  parts.usageLimits,
			spend:        parts.spend,
			productID:    parts.config.Product.ID,
			stateRoot:    parts.stateRoot,
			redactValues: parts.redactValues,
		},
		Reports:      parts.reports,
		MaxRounds:    parts.config.Exchange.MaxRounds,
		ProductID:    parts.config.Product.ID,
		RepositoryID: string(parts.config.Product.RepositoryID),
		// Every round this conductor takes says which process took it, so a round
		// this process dies in the middle of leaves a record naming what to go and
		// look for rather than only that somebody was carrying it.
		Holder: supervisionHolder(),
	}
}

func printExchangeUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo exchange <list|show> [options] [<id>]

  list             every exchange the roles have had, open ones first
  show <id>        one exchange in full, every question and every answer

Options:
  --config <path>  configuration file (default: the nearest .yoyodyne/config.yaml)
  --json           emit machine-readable JSON

An exchange is one role asking another something through the harness: the
Lead Product Manager asking the architect what a goal costs, the architect
asking the Lead Product Manager whether a trade-off is one a user would accept.
It carries advice, not validation results, and grants no authority, so nothing
in one admits work, orders a backlog, or edits a
document. It is recorded so that two roles can never say anything to each other
that you cannot read afterwards, with what each one cost beside the rounds it
took.

An exchange that reaches its round limit closes as unresolved and is reported to
you, because two roles deferring to each other for ever is the one way this can
fail and a silent limit would hide it.`)
}
