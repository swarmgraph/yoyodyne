package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// defaultTimeout is the total budget for one provider invocation: how long a
// run may go on at all, whether or not it is getting anywhere. It is generous
// because it answers a different question from whether the run is stuck --
// defaultIdleTimeout answers that, far sooner -- and because a run stopped for
// either reason is left resumable rather than discarded. What it exists to catch
// is an invocation that stays live and unproductive, retrying or looping, which
// no liveness signal will ever catch.
const defaultTimeout = 4 * time.Hour

// RequestSize bounds the supplied text against the API's 32 MiB request ceiling.
// JSON escaping is included; native history, reasoning and tool results are not
// visible here and remain covered by conversation session compaction and refusal
// recovery. The caller leaves room for the API's other fields. The skills and
// instruction files the project names are sent with the system prompt, so they
// are counted; a named file that cannot be read counts as nothing here, because
// Run refuses the invocation for it before anything is sent.
func (b Backend) RequestSize(request backend.RunRequest) (int, int) {
	named, _, _, err := backend.ReadNamedContext(b.Context, request.Role, backend.NamedRoot(request))
	if err != nil {
		named = ""
	}
	prompt, _ := json.Marshal(request.Prompt)
	system, _ := json.Marshal(appendNamed(request.SystemPrompt, named))
	return len(prompt) + len(system), 32 << 20
}

// defaultIdleTimeout bounds the gap between one event and the next. A working
// agent emits events continuously -- a thought, a tool call, its result -- so
// silence for this long means nothing is happening, and it can be far shorter
// than the total budget for exactly that reason.
const defaultIdleTimeout = 5 * time.Minute

// defaultAfterReplyTimeout bounds how long a session that has written its final
// reply is waited for when it does not exit, which is a session kept alive by
// work it started in the background. The reply is the end of the turn, so this
// is not a stall and is not bounded as one: it is a wait for the background
// work, and at the bound that work is ended rather than the turn.
const defaultAfterReplyTimeout = 5 * time.Minute

// developerSettings is what a developer run is given beyond its tools: the
// OS-level sandbox that confines Bash, and the guard that stands in front of it.
//
// The guard is here rather than only in the repository's own agent settings
// because this is the settings source the harness owns. A developer run has
// Bash and is told to record its work with the tracker, so it is the most
// routine path to `bd update <id> --notes`, which replaces an item's notes and
// takes the goal recorded in them with it, and to `bd update <id> --status`
// with no note saying what moved the status; `yoyo goals guard` reads the
// command and refuses both. It rests on `yoyo` being on the PATH of the run, which
// is where the harness itself was invoked from -- and where it is not, Claude
// Code reports the hook as failed and runs the command, which is the behaviour
// there was before this. The guard can therefore be missing, but not wrong.
//
// The sandbox lets Bash bind local ports and nothing more on the network:
// `sandbox.network.allowLocalBinding` is the one network grant, so a test that
// starts a server on 127.0.0.1 runs in a developer's own `make test` instead of
// failing with "bind: operation not permitted". In Claude Code 2.1.286 it adds
// three rules to the macOS sandbox profile -- bind to any local address, accept
// connections on it, and connect out to localhost -- and every other outbound
// connection still goes through the CLI's filtering proxy. It names no domain,
// no Unix socket, and no write path, so what a run may write and which hosts it
// may reach are unchanged. docs/configuration/runs.md says what this permits.
const developerSettings = `{"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false,` +
	`"network":{"allowLocalBinding":true}},` +
	`"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"yoyo goals guard"}]}]}}`

// developerTools scopes built-in writes to the worktree project root. Bash is
// separately confined by Claude Code's OS-level sandbox settings below.
var developerTools = []string{"Bash", "Read", "Edit(/**)", "Write(/**)", "Glob", "Grep"}

// The session mode a role's invocation runs under, one per posture. Which mode
// a role gets is settled here and nowhere else: the request carries no mode, so
// there is no caller who can name one and no path by which a role receives a
// session somebody else chose for it.
//
// Neither is "plan", and that is the point of them. Plan mode is the
// interactive layer's workflow rather than a permission: Claude Code puts its
// own planning instructions into the session's system prompt -- do not execute
// yet, write a plan file, launch Explore and Plan agents, finish by calling
// ExitPlanMode -- and a harness-invoked role receives them on top of a role
// contract that says the opposite. The reviewer on run
// run-fe0ad8461100ca399c4d2dee371afd53 read them beside a contract forbidding it
// tools and requiring one JSON verdict, followed its contract, and reported the
// injection; nothing but its own judgement made that the outcome. The same
// instructions reaching a developer forbid the edits the run exists to make.
//
// worktreeWriteSessionMode lets an agent whose work is editing a worktree apply
// its edits without a prompt nobody is there to answer.
//
// readOnlySessionMode is the mode that grants nothing: every tool use asks for
// approval, and a non-interactive invocation has nobody to give it. It stands
// behind the empty tool list rather than instead of it -- a role with no tools
// has nothing to ask about -- so what it adds is that the mode itself grants no
// standing permission if a tool ever reached one of these roles.
const (
	worktreeWriteSessionMode = "acceptEdits"
	readOnlySessionMode      = "manual"
)

// readOnlyTools is intentionally empty. A reviewer receives a bounded context,
// patch, and check results, and the product manager, the architect, and the
// development manager receive bounded repository and tracker evidence;
// disabling tools prevents injected evidence from reading outside that evidence
// and exfiltrating unrelated local files, and is what makes "this role runs
// nothing" enforced rather than asked for. A product manager does change the
// work tracker and an architect does decide what a design says, and none of
// that happens here: the harness carries out validated actions on their behalf,
// so the authority never takes the form of a tool this process could be talked
// into using.
var readOnlyTools = []string{}

// stableSystemPromptFlag keeps the per-machine facts out of the part of the
// prompt a provider caches, so the harness's one-shot invocations share a prefix
// instead of each paying to write its own.
//
// Claude Code assembles its own system prompt above whatever the harness
// appends, and one of its sections states the working directory and whether that
// directory is a git worktree. A review runs in the developer's worktree, whose
// path carries the work item and a per-run suffix, so that one section makes the
// whole cached block unique to a single review — and the review contract, the
// reviewer persona, and everything else identical across every review sits
// behind it, off the shared prefix. This flag moves those sections into the
// first user message, where they still reach the role and no longer decide what
// the cache key is. Moved rather than dropped is the provider's documented
// behaviour and is what its shipped code does: the excluded sections are
// rebuilt into a record and prepended to the messages as one user message before
// the model is called. It applies only where the default system prompt is
// appended to rather than replaced, which is what this backend does and what
// TestAOneShotInvocationSharesItsPrefixAndTheDevelopersDoesNot holds it to.
//
// It is the read-only roles' and not the developer's. Their invocation is a
// single short turn with no session to resume, so the cached prefix is the only
// thing they could ever read back; the developer's session re-reads its own
// conversation on every turn and already reads almost all of its input from the
// cache, and moving what a tool-using agent is told about its own working
// directory is a change to that role rather than to what it is charged.
//
// What it is worth is measured rather than assumed: before this, the reviewer's
// invocations reported a cache read of exactly nothing across every recorded
// run, while writing their whole prompt into the cache at the write rate. See
// docs/experiments/yoyodyne-ifd-205-review-prompt-cache.md.
const stableSystemPromptFlag = "--exclude-dynamic-system-prompt-sections"

// promptCacheLifetimeVariable is how Claude Code is told how long what an
// invocation writes into the provider's cache is kept: "5m" or "1h". Unset, the
// provider chooses an hour on a subscription, and an hour is billed at double
// the fresh rate against a quarter over for five minutes.
//
// singleTurnCacheLifetime is what the reviewer's invocations are given. A
// review is one turn nobody resumes, and what it writes into the cache is
// almost all the patch it is judging — unique to that review and never read
// back by anything. The after-window measurement of yoyodyne-ifd.205 found the
// flag above doing what it was meant to: every review reads its shared prefix,
// which is the provider's own static system prompt plus the appended contract
// and persona, some six thousand tokens. Everything past that, tens of
// thousands to a hundred and thirty thousand tokens a review, was written at
// the hour's premium for nobody. The shorter lifetime cuts that premium from
// double to a quarter over and keeps the prefix read where the next review
// follows inside five minutes, which one in five does; modelled over the 638
// reviews of that window it is a seventh of the review phase's cost
// (docs/diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md).
//
// It is the reviewer's alone. The conversation roles resume a session whose
// whole transcript is the cached prefix, and an hour is what keeps that warm
// between an operator's messages; the developer's session is the same. Nothing
// here sets a lifetime for them, so they keep the provider's choice.
const (
	promptCacheLifetimeVariable = "CLAUDE_CODE_PROMPT_CACHE_TTL"
	singleTurnCacheLifetime     = "5m"
)

// singleTurnRole reports a role whose every invocation is one turn nobody
// resumes, which is the reviewer: a review is a separate provider invocation
// with no session to resume, by the contract in internal/review.
func singleTurnRole(role domain.AgentRole) bool {
	return role == domain.RoleReviewer
}

// withPromptCacheLifetime sets the lifetime on an invocation's environment,
// replacing one the harness's own environment carried through: the lifetime is
// this adapter's decision about the invocation's shape, and an operator's
// setting for their own interactive sessions is not a decision about the
// harness's reviews.
func withPromptCacheLifetime(environment []string, lifetime string) []string {
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, promptCacheLifetimeVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, promptCacheLifetimeVariable+"="+lifetime)
}

// readOnlyRole reports whether a role reasons over supplied evidence rather than
// reaching outside it. Such a role gets no tools and cannot be given them.
//
// Which roles those are is the contract's to say rather than this adapter's: it
// is the same statement a backend makes when it declares which postures it can
// hold, and a provider validated against one table and run against another is a
// provider whose configuration says nothing. A role nobody has decided a posture
// for has no posture at all, so it is neither read-only here nor supported by
// this backend, and it reaches the refusal in Run.
func readOnlyRole(role domain.AgentRole) bool {
	return backend.PostureFor(role) == backend.PostureReadOnly
}

// sessionModeFor is the mode a role's session runs under, read off the posture
// the contract holds for that role rather than decided again here. A role whose
// posture nobody has decided never reaches this: Run refuses it first.
func sessionModeFor(role domain.AgentRole) string {
	if readOnlyRole(role) {
		return readOnlySessionMode
	}
	return worktreeWriteSessionMode
}

// supportedRole reports whether this backend knows how to assemble an
// invocation for a role: which tools it gets and which permission mode it may
// run under. A role nobody has decided that for is refused rather than
// defaulted, because the only default available would be the developer's, and
// silently granting a shell to a role meant to have none is the failure this
// guard exists to prevent.
func supportedRole(role domain.AgentRole) bool {
	return role == domain.RoleDeveloper || readOnlyRole(role)
}

type Backend struct {
	Runner execution.ProcessRunner
	Binary string
	Clock  execution.Clock
	// Provider is the backend identifier this invocation is recorded under, and
	// is empty for Claude Code itself. A project that declared a provider running
	// on this adapter is a different backend from the one that ships here, and
	// what a run, a conversation, and a line of spend record has to be the
	// backend the agent named rather than the adapter that happened to launch it.
	Provider domain.Backend
	// Dialect is how what the provider says is read: which reports are limits,
	// which are retries the provider is taking itself, and when a limit lifts.
	// Empty is this provider's own dialect, which is every invocation until a
	// project declares one; a declared provider supplies its own as data.
	//
	// It decides nothing. Whether to wait, how long, and against which budget
	// stay above this adapter, which is what keeps a declared provider from being
	// able to spend an account.
	Dialect backend.Dialect
	// ConfigDir is the provider configuration directory this backend value asks
	// about and, where a request names none of its own, invokes under. It is what
	// lets a diagnosis ask one configured account whether it is authenticated:
	// CheckAvailability takes no request, so the account it is asking about has to
	// be on the value being asked. Empty is the machine's own provider home.
	ConfigDir string
	// Context is the skills and instruction files the project's configuration
	// names. Nothing else the CLI could find is given to a role: see context.go.
	Context backend.NamedContext
}

// ProviderHomeVariable is how Claude Code is told which provider home to
// read. It is the provider's own variable rather than anything the harness
// invented, which is the whole of why an account is a directory here: the
// provider already keeps one account's authentication per home, so pooling is
// naming the homes rather than handling anybody's credentials.
//
// It is this adapter's rather than the contract's for the same reason the
// dialect is: how a provider is pointed at one account's credentials is that
// provider's own vocabulary, and a contract that named this variable would be
// naming Claude Code's spelling of an answer. What generalizes is the request's
// AccountConfigDir; what does not is that this provider reads it from here.
const ProviderHomeVariable = "CLAUDE_CONFIG_DIR"

// providerEnvironmentPrefixes are the families this provider reads its own
// settings from, and so the ones an invocation's environment carries through
// from the harness's beside the allowlist every run gets. A credential under
// either -- an API key, an OAuth token -- is dropped all the same: provider
// authentication is the login held in the provider home, never a variable.
var providerEnvironmentPrefixes = []string{"CLAUDE_", "ANTHROPIC_"}

// environmentFor is the environment one invocation is made in: built from the
// allowlist rather than inherited, so nothing the harness's own environment
// happened to carry -- a Slack token exported in a shell profile, say -- reaches
// the provider or anything it goes on to start. Naming a directory adds the
// provider home on top; naming none names no provider home at all, so an
// installation with one account still authenticates where the machine is signed
// in, and the account plumbing costs it nothing. What the invocation is finally
// given is this plus the run's build cache, which every run gets whether or not
// it named an account.
func environmentFor(configDir string) []string {
	environment := execution.ExplicitEnvironment(nil, providerEnvironmentPrefixes...)
	if strings.TrimSpace(configDir) == "" {
		return environment
	}
	return append(environment, ProviderHomeVariable+"="+configDir)
}

// dialect is what reads this invocation's stream: whatever the caller resolved
// for the backend the agent named, and this provider's own when nothing did.
func (b Backend) dialect() backend.Dialect {
	if b.Dialect == nil {
		return Dialect{}
	}
	return b.Dialect
}

// provider is the backend a result is recorded under.
func (b Backend) provider() domain.Backend {
	if b.Provider == "" {
		return domain.BackendClaudeCode
	}
	return b.Provider
}

// adapterVersion is the compiled adapter a result is recorded as having been
// read by, taken from the one description of this backend the harness holds
// rather than restated here. A provider a project declared is served by this
// adapter too, so its results say this version beside the provider's own name —
// which is the pair a record needs to say which harness code read what.
func adapterVersion() string {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendClaudeCode)
	return descriptor.AdapterVersion
}

// versionCheckTimeout is how long the executable is given to say its version.
const versionCheckTimeout = 10 * time.Second

func (b Backend) CheckAvailability(ctx context.Context) (backend.Availability, error) {
	if b.Runner == nil {
		return backend.Availability{}, errors.New("Claude Code process runner is required")
	}
	binary := b.binary()
	// Both questions are asked in the home this value was built for, so a
	// diagnosis asking about one pooled account cannot be answered by another
	// account's login. Naming no directory asks where the machine is signed in,
	// which is what a single-account installation has always done.
	environment := environmentFor(b.ConfigDir)
	versionResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"--version"}, Env: environment, Timeout: versionCheckTimeout}, nil)
	if err != nil {
		if availability, unavailable := backend.ExecutableUnavailable(err); unavailable {
			return availability, nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return backend.NotFound(binary), nil
		}
		return backend.Availability{}, fmt.Errorf("check Claude Code version: %w", err)
	}
	// An executable that was found and did not answer is not a missing one. A
	// check that timed out, was cancelled with whatever asked for it, or exited
	// nonzero says which, rather than reading as "not installed": see
	// internal/backend/availability.go for the firing that read it that way.
	if versionResult.Status != execution.ProcessSucceeded {
		return backend.Availability{}, fmt.Errorf("check Claude Code version: %w", backend.VersionCheckFailed(ctx, binary, versionCheckTimeout, versionResult))
	}
	availability := backend.Availability{Installed: true, Version: strings.TrimSpace(versionResult.Stdout)}

	authResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"auth", "status", "--json"}, Env: environment, Timeout: 10 * time.Second}, nil)
	if err != nil {
		if availability, unavailable := backend.ExecutableUnavailable(err); unavailable {
			return availability, nil
		}
		return availability, fmt.Errorf("check Claude Code authentication: %w", err)
	}
	var status struct {
		LoggedIn    bool   `json:"loggedIn"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	if err := json.Unmarshal([]byte(authResult.Stdout), &status); err != nil {
		return availability, fmt.Errorf("decode Claude Code authentication status: %w", err)
	}
	availability.Authenticated = status.LoggedIn
	availability.AuthMethod = status.AuthMethod
	availability.APIProvider = status.APIProvider
	return availability, nil
}

// Capabilities is what this adapter can do, read from the one description of
// this backend the harness holds rather than restated here. A configuration is
// validated against that description, so an adapter that answered differently
// would be one whose capability check meant nothing.
func (Backend) Capabilities() backend.Capabilities {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendClaudeCode)
	return descriptor.Capabilities
}

func (b Backend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	if b.Runner == nil {
		return backend.RunResult{}, errors.New("Claude Code process runner is required")
	}
	if strings.TrimSpace(request.RunID) == "" {
		return backend.RunResult{}, errors.New("run id is required")
	}
	if strings.TrimSpace(request.WorkingDirectory) == "" {
		return backend.RunResult{}, errors.New("working directory is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return backend.RunResult{}, errors.New("prompt is required")
	}
	if err := backend.CheckRequestSize(b, request); err != nil {
		return backend.RunResult{}, err
	}
	if !supportedRole(request.Role) {
		return backend.RunResult{}, fmt.Errorf("Claude Code backend does not support role %q", request.Role)
	}

	sessionMode := sessionModeFor(request.Role)
	allowedTools := request.AllowedTools
	if allowedTools == nil {
		if readOnlyRole(request.Role) {
			allowedTools = readOnlyTools
		} else {
			allowedTools = developerTools
		}
	}
	for _, tool := range allowedTools {
		if tool == "" || strings.Contains(tool, ",") || strings.IndexFunc(tool, unicode.IsSpace) >= 0 {
			return backend.RunResult{}, fmt.Errorf("allowed tool rule %q cannot contain list delimiters", tool)
		}
	}
	// Advisory roles consume only the bounded supplied evidence. Refuse every
	// tool, including nominally read-only tools that could inspect outside that
	// evidence and send unrelated local data to the provider.
	if readOnlyRole(request.Role) && len(allowedTools) > 0 {
		return backend.RunResult{}, fmt.Errorf("%s runs cannot be granted tools; the role reasons over bounded supplied evidence", request.Role)
	}
	if request.Role == domain.RoleDeveloper {
		for _, tool := range allowedTools {
			if !developerWriteToolIsScoped(tool) {
				return backend.RunResult{}, fmt.Errorf("developer write tool %q must be scoped to the worktree", tool)
			}
		}
	}

	// What the role is given beside its prompt is what the project named, plus,
	// for a developer, what the CLI reads from its own worktree. Nothing from the
	// account's home is read by any role. See context.go.
	named, skills, instructions, err := backend.ReadNamedContext(b.Context, request.Role, backend.NamedRoot(request))
	if err != nil {
		return backend.RunResult{}, err
	}
	var settingsSources, plugins []backend.LoadedItem
	baseSettings := ""
	if request.Role == domain.RoleDeveloper {
		baseSettings = developerSettings
		var repositoryInstructions []backend.LoadedItem
		settingsSources, plugins, repositoryInstructions = repositoryContext(request.WorkingDirectory)
		instructions = append(repositoryInstructions, instructions...)
	}
	settings, err := settingsFor(baseSettings, request.Role, request.WorkingDirectory)
	if err != nil {
		return backend.RunResult{}, err
	}
	loaded := backend.NewLoaded(skills, plugins, instructions).WithSettingsAndConnectors(settingsSources, nil)

	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", sessionMode,
		"--name", "yoyodyne-" + shortRunID(request.RunID),
		"--settings", settings,
	}
	args = append(args, contextArgs(request.Role)...)
	if request.Role != domain.RoleDeveloper {
		// Repository instruction files are evidence, not harness policy. Safe
		// mode prevents a checked-in CLAUDE.md from entering the provider's
		// system context alongside an immutable harness contract.
		args = append(args, "--safe-mode")
		args = append(args, stableSystemPromptFlag)
	}
	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, allowedTools...)
	} else {
		args = append(args, "--tools", "")
	}
	if systemPrompt := appendNamed(request.SystemPrompt, named); systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	if request.SessionID != "" {
		args = append(args, "--resume", request.SessionID)
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	// The level is passed explicitly rather than left to the provider, which
	// would otherwise resolve one from this machine's environment and settings
	// or from the model's own default -- a level nobody configured and nothing
	// records.
	if effort := strings.TrimSpace(request.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	timeout := request.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	idleTimeout := request.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = defaultIdleTimeout
	}
	afterReplyTimeout := request.AfterReplyTimeout
	if afterReplyTimeout == 0 {
		afterReplyTimeout = defaultAfterReplyTimeout
	}

	clock := b.Clock
	if clock == nil {
		clock = execution.RealClock{}
	}
	redactor := execution.NewRedactor(request.RedactValues...)
	parser := newStreamParser(request.RunID, request.Role, request.LastSequence, clock, redactor, request.EventSink, request.ReplySink, b.dialect())
	parser.loaded = loaded
	var parseErrors []error
	// The account this invocation is made under is the request's, falling back to
	// the one this backend value was built for. A request that names neither runs
	// where the machine is already signed in, which is what a single-account
	// installation has always done.
	configDir := request.AccountConfigDir
	if strings.TrimSpace(configDir) == "" {
		configDir = b.ConfigDir
	}
	// The invocation carries the role it is made for, so the verbs that record
	// a person's decision -- pause, resume, release, approve -- can refuse a
	// shell this agent opens. It is under the harness's own family, which the
	// allowlist carries through to everything the agent goes on to start.
	environment := execution.WithAgentRole(withContextEnvironment(environmentFor(configDir)), request.Role)
	if singleTurnRole(request.Role) {
		environment = withPromptCacheLifetime(environment, singleTurnCacheLifetime)
	}
	processResult, err := b.Runner.Run(ctx, execution.Command{
		Name: b.binary(),
		Args: args,
		Dir:  request.WorkingDirectory,
		// Beside the account, the invocation carries the Go build cache pointed
		// somewhere this run may write. A developer's first act is to execute
		// the project's checks, and the default cache is under the user's home,
		// which the sandbox this run is confined to does not grant: without the
		// redirect the probe dies at setup and reads as a broken toolchain.
		Env:   execution.WithGoBuildCache(environment, request.WorkingDirectory),
		Stdin: strings.NewReader(request.Prompt),
		// The stream this invocation is asked for is the liveness signal: every
		// line the process writes is an event, so the gap between lines is
		// exactly the gap between events.
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
		// The invocation's own result is the end of its turn. A session still
		// alive after it is kept there by work it backgrounded, not by a provider
		// gone silent, so the stall bound stops applying at it and that work is
		// waited out instead: before this, run-008b0e25 was stopped as a stall on
		// 2026-09-28 five minutes after writing its final reply, over the make
		// test and make race it had left running.
		Replied:           parser.SawResult,
		AfterReplyTimeout: afterReplyTimeout,
		AfterReplyWaiting: func(account execution.AfterReply) {
			if recordErr := parser.RecordAfterReply(account); recordErr != nil {
				parseErrors = append(parseErrors, recordErr)
			}
			if request.AfterReplyWaiting != nil {
				request.AfterReplyWaiting(account)
			}
		},
		// Every line of that stream is parsed into a normalized event below, so
		// the run's event log is where an invocation too verbose to retain is
		// held whole. What the runner keeps is a diagnostic beside it, and the
		// marker in a cut copy says which of the two a reader has.
		OutputRecord: execution.EventLogOf(request.RunID),
		// A routed attempt is started behind its launch gate, so its record
		// names the process before the provider can begin; nil is an
		// invocation nothing reserved, started as it always was.
		Gate:     request.LaunchGate,
		Redactor: redactor,
	}, func(output execution.Output) {
		if output.Stream == execution.StreamStdout {
			// A line the runner cut is not an envelope any more, and nothing
			// recovers the rest of it. It is recorded as the anomaly it is and the
			// stream carries on: one line the harness could not hold is not a
			// stream it could not read, and failing the invocation over it is the
			// same self-inflicted death that used to arrive as a killed process.
			if output.LineTruncated {
				if recordErr := parser.RecordTruncatedLine(output); recordErr != nil {
					parseErrors = append(parseErrors, recordErr)
				}
				return
			}
			if parseErr := parser.ParseLine(output.Text); parseErr != nil {
				parseErrors = append(parseErrors, parseErr)
			}
			return
		}
		if sinkErr := parser.EmitProcessOutput(output); sinkErr != nil {
			parseErrors = append(parseErrors, sinkErr)
		}
	})
	if err != nil {
		return backend.RunResult{}, fmt.Errorf("run Claude Code: %w", err)
	}
	// A process that failed without writing a terminal has said whatever it had
	// to say as prose — on stderr, or on stdout in place of the envelopes it was
	// asked for — so that is what the dialect is handed, and only then. A
	// terminal the provider did write is the provider's own account of the
	// ending and is not second-guessed by its diagnostics; a process the harness
	// stopped on time is a stop the harness already names; and a stream that
	// reported a limit and then died has been answered by the limit. What is
	// left is the CLI that refused before it wrote anything structured — an
	// expired login, an API nothing reaches — which used to end the attempt as
	// a process failure nobody classified.
	//
	// It is asked before the stream's decode errors are judged, because a
	// refusal written to stdout as plain text is exactly a line that failed to
	// decode: read first, it is the invocation's answer and the errors are what
	// it looked like on the way in; judged first, it failed the invocation as an
	// unreadable stream before stderr was read at all, which is the gap
	// yoyodyne-ifd.393 reported after closing the stderr one.
	if processResult.Status == execution.ProcessFailed && !parser.SawResult() && !parser.SawUsageLimit() {
		parser.ObservePlainOutput()
	}
	if len(parseErrors) > 0 && !parser.ReadRefusalOffPlainOutput() {
		return backend.RunResult{}, fmt.Errorf("parse Claude Code stream: %w", errors.Join(parseErrors...))
	}
	// An invocation that outran what the runner retains is not an invocation
	// that failed: every line still reached the parser, so the stream was read
	// in full and the result event this run is priced from is in the events
	// below. The truncation is said in the same stream rather than left to a
	// field nobody opens, because a run that died of its own verbosity is what
	// this is here to stop looking like silence.
	if processResult.OutputTruncation != "" {
		if truncationErr := parser.EmitProcessOutput(execution.Output{
			Stream: execution.StreamStderr,
			Text:   processResult.OutputTruncation,
		}); truncationErr != nil {
			return backend.RunResult{}, fmt.Errorf("record Claude Code output truncation: %w", truncationErr)
		}
	}
	// Which way the wait after the reply ended is said in the same stream as
	// the wait itself, so the log reads as a turn that ended and then its
	// background work, never as a silence.
	if processResult.AfterReply != nil {
		if recordErr := parser.RecordAfterReply(*processResult.AfterReply); recordErr != nil {
			return backend.RunResult{}, fmt.Errorf("record what outlived Claude Code's final reply: %w", recordErr)
		}
	}
	// The normalized result and events are the durable provider output. Do not
	// return the raw JSON stream as a second, potentially escape-obfuscated copy.
	processResult.Stdout = ""
	result := parser.Result()
	result.Backend = b.provider()
	result.AdapterVersion = adapterVersion()
	result.Loaded = loaded
	result.Process = processResult
	if processResult.Status == execution.ProcessCancelled || processResult.Status == execution.ProcessTimedOut || processResult.Status == execution.ProcessStalled {
		result.IsError = true
		if result.StopReason == "" {
			result.StopReason = string(processResult.Status)
		}
	}
	if !parser.SawResult() && processResult.Status == execution.ProcessSucceeded {
		return result, errors.New("Claude Code stream ended without a result event")
	}
	if processResult.Status == execution.ProcessFailed {
		result.IsError = true
		if result.StopReason == "" {
			result.StopReason = fmt.Sprintf("process_exit_%d", processResult.ExitCode)
		}
	}
	return result, nil
}

func (b Backend) binary() string {
	if b.Binary == "" {
		return "claude"
	}
	return b.Binary
}

func developerWriteToolIsScoped(tool string) bool {
	for _, name := range []string{"Edit", "Write"} {
		if tool == name {
			return false
		}
		prefix := name + "("
		if !strings.HasPrefix(tool, prefix) {
			continue
		}
		if !strings.HasSuffix(tool, ")") {
			return false
		}
		pattern := strings.TrimSuffix(strings.TrimPrefix(tool, prefix), ")")
		return strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "//") && !strings.Contains(pattern, "..")
	}
	return true
}

func shortRunID(runID string) string {
	value := strings.TrimPrefix(runID, "run-")
	if len(value) > 8 {
		return value[:8]
	}
	return value
}
