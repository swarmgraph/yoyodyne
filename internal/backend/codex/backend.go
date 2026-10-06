// Package codex runs agents through the locally installed Codex CLI.
//
// It is deliberately thin. Codex is not required to match every Claude Code
// feature; a role or a posture this adapter cannot hold is refused where the
// configuration is validated, before any work is assigned. Credentials stay
// where the provider keeps them: the harness reports whether the CLI is
// installed and logged in and manages no account of its own.
//
// The adapter's history is worth a line, because it explains why this arrives
// whole rather than as a first draft. It was written under yoyodyne-ifd.6 on a
// branch that was never merged, and the line moved 492 commits underneath it
// while it sat there; yoyodyne-ifd.347 is the operator's decision to land the
// work rather than leave a second endpoint the registry expresses and nothing
// can launch. What is here is that adapter brought forward onto the contract as
// it stands now — no permission mode on a request, an adapter version on every
// result, an account that is a provider home, and the seventh answer the
// contract gained for a model a provider has not got.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// defaultTimeout is the total budget for one provider invocation and
// defaultIdleTimeout the gap it may go without saying anything. They answer
// different questions — is this run worth continuing, and is it doing anything
// at all — so both are applied rather than either standing in for the other,
// and they are the same bounds the other adapter uses because they are
// properties of how the harness runs agents rather than of which agent it runs.
//
// defaultAfterReplyTimeout is how long a process that has written its terminal
// is waited for when it does not exit: the terminal ends the turn, so what keeps
// the process alive after it is work it started in the background, waited out
// to this bound and then ended rather than read as a stall.
const (
	defaultTimeout           = 4 * time.Hour
	defaultIdleTimeout       = 5 * time.Minute
	defaultAfterReplyTimeout = 5 * time.Minute
)

// The Codex sandbox setting this adapter will ask for. `danger-full-access` is
// deliberately absent: it is the one setting that would let an agent write
// anywhere on the machine, and nothing in the harness has a reason to ask for
// it, so it is unreachable from here rather than merely unused.
const (
	sandboxWorkspaceWrite = "workspace-write"
	sandboxReadOnly       = "read-only"
)

// sandboxForPosture maps role-derived access onto the native sandbox. Read-only
// permits repository inspection but does not isolate reads to that repository.
func sandboxForPosture(posture backend.Posture) string {
	switch posture {
	case backend.PostureReadOnly:
		return sandboxReadOnly
	case backend.PostureWorktreeWrite:
		return sandboxWorkspaceWrite
	default:
		return ""
	}
}

// sandboxFor decides what this invocation runs under: the sandbox the role's
// tool posture requires, and a refusal for a role that has no posture at all.
//
// The posture decides and nothing else may. There is no permission mode on a
// request for a caller to name one with — which role gets which session is a
// function of the role, settled by the contract — so this is the whole of the
// policy check, and a role nobody has decided a posture for is refused rather
// than defaulted, because the only default available would be the developer's
// and silently widening a role meant to have none is the failure this guard
// exists to prevent.
//
// The request may name a narrower posture than its role's — an agent filling a
// role definition that removed the worktree write — and never a wider one.
func sandboxFor(request backend.RunRequest) (string, error) {
	posture, err := backend.RequestPosture(request)
	if err != nil {
		return "", err
	}
	sandbox := sandboxForPosture(posture)
	if sandbox == "" {
		return "", fmt.Errorf("Codex backend does not support role %q", request.Role)
	}
	return sandbox, nil
}

type Backend struct {
	Runner execution.ProcessRunner
	Binary string
	Clock  execution.Clock
	// Provider is the backend identifier this invocation is recorded under, and
	// is empty for Codex itself. A project that declared a provider running on
	// this adapter is a different backend from the one that ships here, and what
	// a run, a conversation, and a line of spend record has to be the backend the
	// agent named rather than the adapter that happened to launch it.
	Provider domain.Backend
	// Dialect is how what the provider says is read. Empty is this provider's
	// own, which is every invocation until a project declares one. It decides
	// nothing: whether to wait, how long, and against which budget stay above
	// this adapter.
	Dialect backend.Dialect
	// ConfigDir is the provider home this backend value asks about and, where a
	// request names none of its own, invokes under. It is what lets a diagnosis
	// ask one configured account whether it is authenticated: CheckAvailability
	// takes no request, so the account it is asking about has to be on the value
	// being asked. Empty is the machine's own provider home.
	ConfigDir string
	// Context is the skills and instruction files the project's configuration
	// names. Nothing else the CLI could find is given to a role: see context.go.
	Context backend.NamedContext
}

// ProviderHomeVariable is how Codex is told which provider home to read. It is
// the provider's own variable rather than anything the harness invented, which
// is the whole of why an account is a directory here as it is for the other
// adapter: the provider already keeps one account's authentication per home, so
// pooling is naming the homes rather than handling anybody's credentials.
//
// Provenance, stated because it is weaker than the other adapter's. No recorded
// Codex run in this repository sets it; it is read from the provider's own
// documented configuration home. What it costs to be wrong is bounded and
// visible: an invocation that named a home the CLI ignored would authenticate
// where the machine is signed in, which is exactly what naming no home does, and
// `yoyo doctor` asks each account through this same variable, so a home the CLI
// does not read shows up there as an account that will not authenticate rather
// than as a run charged to somebody else's subscription.
const ProviderHomeVariable = "CODEX_HOME"

// providerEnvironmentPrefixes are the families this provider reads its own
// settings from, and so the ones an invocation's environment carries through
// from the harness's beside the allowlist every run gets. A credential under
// either -- an API key -- is dropped all the same: provider authentication is
// the login held in the provider home, never a variable.
var providerEnvironmentPrefixes = []string{"CODEX_", "OPENAI_"}

// environmentFor is the environment one invocation is made in: built from the
// allowlist rather than inherited, so nothing the harness's own environment
// happened to carry -- a Slack token exported in a shell profile, say -- reaches
// the provider or anything it goes on to start. Naming a directory adds the
// provider home on top; naming none names no provider home at all, so an
// installation with one account still authenticates where the machine is signed
// in, and the account plumbing costs it nothing.
func environmentFor(configDir string) []string {
	environment := execution.ExplicitEnvironment(nil, providerEnvironmentPrefixes...)
	if strings.TrimSpace(configDir) == "" {
		return environment
	}
	return append(environment, ProviderHomeVariable+"="+configDir)
}

// readOnlyEnvironment keeps provider authentication but drops inherited Codex
// control channels and settings. The developer's environment remains unchanged.
func readOnlyEnvironment(configDir string) []string {
	environment := execution.ExplicitEnvironment(nil)
	kept := environment[:0]
	for _, entry := range environment {
		if strings.HasPrefix(entry, "SSH_AUTH_SOCK=") {
			continue
		}
		kept = append(kept, entry)
	}
	if strings.TrimSpace(configDir) == "" {
		configDir = os.Getenv(ProviderHomeVariable)
	}
	if configDir != "" {
		kept = append(kept, ProviderHomeVariable+"="+configDir)
	}
	return kept
}

// readOnlyArgs fixes the native sandbox's companion policy on every turn,
// including resume. User settings and exec rules cannot enable integrations or
// escalation. The directory is an empty launch directory outside the repository,
// so repository configuration cannot inject MCP servers or other integrations.
// Native reads remain available, and are not limited to the repository.
func readOnlyArgs(directory string) []string {
	path, _ := json.Marshal(directory)
	args := []string{"--ignore-user-config", "--ignore-rules", "--strict-config"}
	for _, setting := range []string{
		`approval_policy="never"`,
		`web_search="disabled"`,
		`orchestrator.mcp.enabled=false`,
		`cloud.skills.enabled=false`,
		`agents.enabled=false`,
		`allow_login_shell=false`,
		`notify=[]`,
		`project_doc_max_bytes=0`,
		`tools.experimental_request_user_input.enabled=false`,
		`projects={` + string(path) + `={trust_level="untrusted"}}`,
	} {
		args = append(args, "--config", setting)
	}
	// Skills, plugins, and apps are kept out of every role by contextArgs.
	for _, feature := range []string{
		"hooks", "browser_use",
		"browser_use_external", "browser_use_full_cdp_access", "computer_use",
		"in_app_browser", "image_generation", "workspace_dependencies",
		"goals", "shell_snapshot", "multi_agent", "multi_agent_v2",
	} {
		args = append(args, "--disable", feature)
	}
	return args
}

// prepareReadOnlyLaunch resolves the repository before changing directories.
// The CLI discovers project configuration from its cwd, so read-only roles run
// from an empty directory and inspect the repository by its absolute path.
func prepareReadOnlyLaunch(directory string) (repository, launch string, err error) {
	repository, err = filepath.Abs(directory)
	if err != nil {
		return "", "", fmt.Errorf("resolve read-only repository: %w", err)
	}
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		return "", "", fmt.Errorf("resolve read-only repository: %w", err)
	}
	info, err := os.Stat(repository)
	if err != nil {
		return "", "", fmt.Errorf("stat read-only repository: %w", err)
	}
	if !info.IsDir() {
		return "", "", errors.New("read-only repository must be a directory")
	}
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", "", fmt.Errorf("resolve temporary directory: %w", err)
	}
	relative, relErr := filepath.Rel(repository, temporary)
	if relErr != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return "", "", errors.New("read-only launch directory must be outside the repository; configure a temporary directory outside it")
	}
	// A sibling of a nested inspection directory can still be inside the same
	// Git checkout. Check the temporary directory's ancestors before creating
	// anything, including linked worktrees whose .git is a file.
	for ancestor := temporary; ; ancestor = filepath.Dir(ancestor) {
		for _, name := range []string{".git", ".codex"} {
			if _, err := os.Stat(filepath.Join(ancestor, name)); err == nil {
				return "", "", fmt.Errorf("temporary directory has a %s ancestor; configure a temporary directory outside project configuration", name)
			} else if !os.IsNotExist(err) {
				return "", "", fmt.Errorf("inspect temporary directory ancestors: %w", err)
			}
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	launch, err = os.MkdirTemp(temporary, "yoyodyne-codex-readonly-")
	if err != nil {
		return "", "", fmt.Errorf("create read-only launch directory: %w", err)
	}
	return repository, launch, nil
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
		return domain.BackendCodex
	}
	return b.Provider
}

// adapterVersion is the compiled adapter a result is recorded as having been
// read by, taken from the one description of this backend the harness holds
// rather than restated here. A provider a project declared may be served by this
// adapter too, so its results say this version beside the provider's own name —
// which is the pair a record needs to say which harness code read what.
func adapterVersion() string {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	return descriptor.AdapterVersion
}

func (b Backend) binary() string {
	if b.Binary == "" {
		return "codex"
	}
	return b.Binary
}

// versionCheckTimeout is how long the executable is given to say its version.
const versionCheckTimeout = 10 * time.Second

// CheckAvailability asks the installed CLI whether it is there and whether it is
// logged in. Both answers are the provider's own: Codex holds the credentials,
// whether they came from a ChatGPT subscription or an API key, and the harness
// reports the state without managing any of it.
//
// The login state is read from the command's exit status rather than from its
// prose, because the status is the one part of the answer that does not move
// between versions. The prose is read only to say which credential is in use,
// and a sentence this adapter does not recognize leaves that unnamed rather than
// leaving the account misreported.
func (b Backend) CheckAvailability(ctx context.Context) (backend.Availability, error) {
	if b.Runner == nil {
		return backend.Availability{}, errors.New("Codex process runner is required")
	}
	binary := b.binary()
	// Both questions are asked in the home this value was built for, so a
	// diagnosis asking about one pooled account cannot be answered by another
	// account's login. Naming no directory asks where the machine is signed in,
	// which is what a single-account installation has always done.
	environment := environmentFor(b.ConfigDir)
	versionResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"--version"}, Env: environment, Timeout: versionCheckTimeout}, nil)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return backend.NotFound(binary), nil
		}
		return backend.Availability{}, fmt.Errorf("check Codex version: %w", err)
	}
	// An executable that was found and did not answer is not a missing one. A
	// check that timed out, was cancelled with whatever asked for it, or exited
	// nonzero says which, rather than reading as "not installed": see
	// internal/backend/availability.go for the firing that read it that way.
	if versionResult.Status != execution.ProcessSucceeded {
		return backend.Availability{}, fmt.Errorf("check Codex version: %w", backend.VersionCheckFailed(ctx, binary, versionCheckTimeout, versionResult))
	}
	availability := backend.Availability{
		Installed:   true,
		Version:     strings.TrimSpace(versionResult.Stdout),
		APIProvider: "openai",
	}

	loginResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"login", "status"}, Env: environment, Timeout: 10 * time.Second}, nil)
	if err != nil {
		return availability, fmt.Errorf("check Codex authentication: %w", err)
	}
	availability.Authenticated = loginResult.Status == execution.ProcessSucceeded
	availability.AuthMethod = authMethod(loginResult.Stdout)
	return availability, nil
}

// authMethod names the credential the CLI said it is using. It reads the two
// the provider documents and says nothing about a sentence it does not
// recognize, because a guessed credential in an availability report is worse
// than an unnamed one.
func authMethod(status string) string {
	lowered := strings.ToLower(status)
	switch {
	case strings.Contains(lowered, "api key"):
		return "api-key"
	case strings.Contains(lowered, "chatgpt"):
		return "chatgpt"
	default:
		return ""
	}
}

// Capabilities is what this adapter can do, read from the one description of
// this backend the harness holds rather than restated here. A configuration is
// validated against that description, so an adapter that answered differently
// would be one whose capability check meant nothing.
func (Backend) Capabilities() backend.Capabilities {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	return descriptor.Capabilities
}

// invocationArgs is the command line one invocation is made with, each option
// placed on the command level whose own help lists it.
//
// That placement is the whole difficulty, because the CLI refuses an option on a
// level that does not take it and refuses it before anything starts. `exec`
// takes all four of the options this adapter passes; `exec resume` takes
// `--json`, `--skip-git-repo-check`, and `--model` but not `--sandbox`, so a
// resumed invocation that put the sandbox after `resume` was refused outright
// ("unexpected argument '--sandbox' found") and no resumed session ever started.
// The sandbox is given to `exec`, ahead of `resume`, and that is not a
// formality: asked without a provider call, codex-cli 0.159.2 recorded the
// resumed turn under the sandbox given there rather than the one the session
// was started under. It is never dropped to let a resume start — a session that
// could not be given its sandbox would run under whatever it was started with,
// or none. testdata/cli-help holds the help this was read from, and
// conformance_test.go checks every invocation against it.
func invocationArgs(request backend.RunRequest, sandbox string, directories []string) []string {
	args := []string{"exec", "--sandbox", sandbox}
	args = append(args, "--config", "model_reasoning_effort="+fmt.Sprintf("%q", request.Effort))
	args = append(args, contextArgs()...)
	if sandbox == sandboxWorkspaceWrite {
		// exec resume has no --add-dir option. A config override ahead of resume
		// applies the same confined roots on every turn and replaces user roots.
		if directories == nil {
			directories = []string{}
		}
		roots, _ := json.Marshal(directories)
		args = append(args,
			"--config", `approval_policy="never"`,
			"--config", "sandbox_workspace_write.writable_roots="+string(roots),
			"--config", "sandbox_workspace_write.network_access=false",
		)
	}
	if sandbox == sandboxReadOnly {
		args = append(args, readOnlyArgs(request.WorkingDirectory)...)
	}
	args = append(args, "--cd", request.WorkingDirectory)
	// Resuming continues the provider's own session, which is an acceleration
	// and never the record: what the harness knows about this work is in its own
	// durable state, and a session the provider has forgotten costs context
	// rather than work.
	if request.SessionID != "" {
		args = append(args, "resume", request.SessionID)
	}
	args = append(args,
		"--json",
		// The worktree is a Git checkout, so this changes nothing for a real run;
		// it is what lets the harness invoke the provider somewhere that is not
		// one, which the conformance check does.
		"--skip-git-repo-check",
	)
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	// The prompt is read from standard input rather than put on the command line.
	// It carries the role's contract and the evidence the role was given, both of
	// which are long and neither of which belongs in a process listing.
	return append(args, "-")
}

func (b Backend) Run(ctx context.Context, request backend.RunRequest) (returned backend.RunResult, runErr error) {
	if b.Runner == nil {
		return backend.RunResult{}, errors.New("Codex process runner is required")
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
	sandbox, err := sandboxFor(request)
	if err != nil {
		return backend.RunResult{}, err
	}
	// Codex has no per-tool control: what an agent may do is decided by the
	// sandbox and by nothing else. A request naming tools is therefore refused
	// rather than run with the list quietly dropped, because a caller that asked
	// for a narrower set than the sandbox gives would otherwise get a wider one
	// and be told nothing.
	if len(request.AllowedTools) > 0 {
		return backend.RunResult{}, errors.New("Codex runs cannot be granted a tool list; the sandbox is what scopes what an agent may do")
	}

	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	descriptor = descriptor.ForModel(request.Model)
	request.Effort = descriptor.InvocationEffort(request.Model, request.Effort)
	if !descriptor.AcceptsEffort(request.Effort) {
		return backend.RunResult{}, fmt.Errorf("Codex model %q does not accept effort %q; accepted values: %s", request.Model, request.Effort, descriptor.DescribeEffortLevels())
	}

	invocation := request
	if sandbox == sandboxReadOnly {
		repository, launch, err := prepareReadOnlyLaunch(request.WorkingDirectory)
		if err != nil {
			return backend.RunResult{}, err
		}
		defer func() {
			if err := os.RemoveAll(launch); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("remove read-only launch directory: %w", err))
			}
		}()
		request.WorkingDirectory = repository
		invocation.WorkingDirectory = launch
	}
	// Canonicalizing the inspection path can lengthen the composed prompt.
	if err := backend.CheckRequestSize(b, request); err != nil {
		return backend.RunResult{}, err
	}
	var directories []string
	repository := request.RepositoryRoot
	if repository == "" {
		repository = request.WorkingDirectory
	}
	if sandbox == sandboxWorkspaceWrite {
		directories, err = execution.PrepareDeveloperDirectories(repository, request.WorkingDirectory, request.RunID)
		if err != nil {
			return backend.RunResult{}, fmt.Errorf("prepare Codex developer sandbox: %w", err)
		}
	}
	args := invocationArgs(invocation, sandbox, directories)

	// What the role is given beside its prompt is what the project named, plus,
	// for a developer, the repository's own instruction file the CLI reads from
	// the worktree. See context.go.
	named, skills, instructions, err := backend.ReadNamedContext(b.Context, request.Role, backend.NamedRoot(request))
	if err != nil {
		return backend.RunResult{}, err
	}
	if sandbox == sandboxWorkspaceWrite {
		instructions = append(repositoryInstructions(invocation.WorkingDirectory), instructions...)
	}
	loaded := backend.NewLoaded(skills, nil, instructions)

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
	environment := environmentFor(configDir)
	if sandbox == sandboxReadOnly {
		// Relative provider homes previously resolved from the repository. Keep
		// that account mapping when the CLI runs from the isolated directory.
		if strings.TrimSpace(configDir) == "" {
			configDir = os.Getenv(ProviderHomeVariable)
		}
		if configDir != "" && !filepath.IsAbs(configDir) {
			configDir = filepath.Join(request.WorkingDirectory, configDir)
		}
		environment = readOnlyEnvironment(configDir)
	}
	// The provider home's own instruction files are kept out by running under a
	// home that leaves them out, where it has any.
	home, err := providerHome(configDir, request.WorkingDirectory)
	if err != nil {
		return backend.RunResult{}, err
	}
	home, madeHome, err := prepareProviderHome(home)
	if err != nil {
		return backend.RunResult{}, err
	}
	if madeHome {
		defer func() {
			if err := os.RemoveAll(home); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("remove the Codex provider home made for this invocation: %w", err))
			}
		}()
		environment = withProviderHome(environment, home)
	}
	environment = execution.WithAgentRole(environment, request.Role)
	if sandbox == sandboxWorkspaceWrite {
		environment = execution.WithGoBuildCache(environment, repository)
		if len(directories) > 0 {
			for index, entry := range environment {
				if strings.HasPrefix(entry, "GOCACHE=") {
					environment[index] = "GOCACHE=" + directories[0]
				}
			}
		}
	}
	processResult, err := b.Runner.Run(ctx, execution.Command{
		Name: b.binary(),
		Args: args,
		Dir:  invocation.WorkingDirectory,
		// Beside the account, the invocation carries the Go build cache pointed
		// somewhere this run may write. A developer's first act is to execute the
		// project's checks, and the default cache is under the user's home, which
		// the sandbox this run is confined to does not grant: without the redirect
		// the probe dies at setup and reads as a broken toolchain. And it carries
		// the role it is made for, so the verbs that record a person's decision
		// -- pause, resume, release, approve -- can refuse a shell this agent
		// opens.
		Env:   environment,
		Stdin: strings.NewReader(composePrompt(request, named)),
		// The stream this invocation is asked for is the liveness signal: every
		// line the process writes is an event, so the gap between lines is
		// exactly the gap between events.
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
		// The invocation's own terminal is the end of its turn, so the stall
		// bound stops applying at it and a process still alive after it is
		// waited out as background work instead.
		Replied:           parser.SawTerminal,
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
		Redactor:     redactor,
	}, func(output execution.Output) {
		if output.Stream == execution.StreamStdout {
			// A line the runner cut is not an envelope any more, and nothing
			// recovers the rest of it. It is recorded as the anomaly it is and the
			// stream carries on: one line the harness could not hold is not a
			// stream it could not read, and failing the invocation over it is a
			// self-inflicted death.
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
		return backend.RunResult{}, fmt.Errorf("run Codex: %w", err)
	}
	if len(parseErrors) > 0 {
		return backend.RunResult{}, fmt.Errorf("parse Codex stream: %w", errors.Join(parseErrors...))
	}
	// An invocation that outran what the runner retains is not an invocation that
	// failed: every line still reached the parser, so the stream was read in full.
	// The truncation is said in the same stream rather than left to a field
	// nobody opens, because a run that died of its own verbosity is what this is
	// here to stop looking like silence.
	if processResult.OutputTruncation != "" {
		if truncationErr := parser.EmitProcessOutput(execution.Output{
			Stream: execution.StreamStderr,
			Text:   processResult.OutputTruncation,
		}); truncationErr != nil {
			return backend.RunResult{}, fmt.Errorf("record Codex output truncation: %w", truncationErr)
		}
	}
	// Which way the wait after the terminal ended is said in the same stream as
	// the wait itself, so the log reads as a turn that ended and then its
	// background work, never as a silence.
	if processResult.AfterReply != nil {
		if recordErr := parser.RecordAfterReply(*processResult.AfterReply); recordErr != nil {
			return backend.RunResult{}, fmt.Errorf("record what outlived Codex's terminal: %w", recordErr)
		}
	}
	// A process that failed without writing a terminal has said whatever it had
	// to say as prose — on stderr, or on stdout in place of the events it was
	// asked for — so that is what the dialect is handed, and only then. A
	// terminal the provider did write is the provider's own account of the
	// ending and is not second-guessed by its diagnostics; a process the harness
	// stopped on time is a stop the harness already names; and a stream that
	// reported a limit and then died has been answered by the limit. What is
	// left is the CLI that refused before it wrote anything structured — an
	// expired login, an API nothing reaches — which until yoyodyne-ifd.400 ended
	// the attempt as a process failure nobody classified, on this adapter by
	// decision pending a recorded occurrence.
	if processResult.Status == execution.ProcessFailed && !parser.SawTerminal() && !parser.SawUsageLimit() {
		parser.ObservePlainOutput()
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
	if !parser.SawTerminal() && processResult.Status == execution.ProcessSucceeded {
		if unrecognized := parser.FirstUnrecognized(); unrecognized != "" {
			return result, fmt.Errorf("Codex %s wrote a stream this adapter cannot read: the first event it did not recognize was %q, and no terminal it recognizes arrived", b.installedVersion(ctx, configDir), unrecognized)
		}
		return result, errors.New("Codex stream ended without a terminal event")
	}
	if processResult.Status == execution.ProcessFailed {
		result.IsError = true
		if result.StopReason == "" {
			result.StopReason = fmt.Sprintf("process_exit_%d", processResult.ExitCode)
		}
	}
	return result, nil
}

// installedVersion is what the CLI says its version is, for an error that has
// to name it. It is asked only once a stream has already failed to read, so an
// invocation that went well spends nothing on it, and a CLI that will not say is
// named as such rather than failing the report of the fault that mattered.
func (b Backend) installedVersion(ctx context.Context, configDir string) string {
	versionResult, err := b.Runner.Run(ctx, execution.Command{Name: b.binary(), Args: []string{"--version"}, Env: environmentFor(configDir), Timeout: versionCheckTimeout}, nil)
	if err != nil || versionResult.Status != execution.ProcessSucceeded || strings.TrimSpace(versionResult.Stdout) == "" {
		return "(version unknown)"
	}
	return strings.TrimSpace(versionResult.Stdout)
}

// composePrompt is what the provider is actually sent. Codex has no separate
// channel for a system prompt, so the role's contract is prepended to the
// prompt rather than dropped: a role invoked without the contract it was given
// is a role doing some other job. It is worth being plain that this is weaker
// than the other adapter, where the contract enters the provider's system
// context and the evidence cannot reach it — here the two arrive as one message,
// so evidence that tried to talk its way past the contract is arguing with text
// in the same message rather than with something above it.
//
// What the project named for the role goes between the contract and the prompt,
// so it reads as part of the role's standing instructions rather than the task.
func composePrompt(request backend.RunRequest, named string) string {
	prompt := request.Prompt
	if strings.TrimSpace(named) != "" {
		prompt = named + "\n\n" + prompt
	}
	if strings.TrimSpace(request.SystemPrompt) != "" {
		prompt = request.SystemPrompt + "\n\n" + prompt
	}
	if posture, _ := backend.RequestPosture(request); posture == backend.PostureReadOnly {
		directory, _ := json.Marshal(request.WorkingDirectory)
		prompt = "Repository available for read-only inspection: " + string(directory) + ".\nThe current directory is an empty launch directory, not the repository. Inspect and plan only; do not implement changes or request escalation.\n\n" + prompt
	}
	return prompt
}

// RequestSize includes everything composePrompt adds. Codex turn/start refused
// input past 1,048,576 characters in the development manager's October 5 record
// (events 13747–13749). Counting UTF-8 bytes is conservative for that character
// bound, and covers the CLI's fresh and resumed turns alike. The skills and
// instruction files the project names are part of what is sent, so they are
// counted; a named file that cannot be read counts as nothing here, because Run
// refuses the invocation for it before anything is sent.
func (b Backend) RequestSize(request backend.RunRequest) (int, int) {
	named, _, _, err := backend.ReadNamedContext(b.Context, request.Role, backend.NamedRoot(request))
	if err != nil {
		named = ""
	}
	return len(composePrompt(request, named)), 1 << 20
}
