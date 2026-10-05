package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/cost"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type runOutput struct {
	Outcome *orchestrator.Outcome `json:"outcome,omitempty"`
	Error   string                `json:"error,omitempty"`
}

func runWorkItem(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "run requires exactly one Beads work item id")
		printRunUsage(stderr)
		return 2
	}

	pipeline, err := buildPipeline(*configPath)
	if err != nil {
		return reportRunResult(stdout, stderr, *jsonOutput, orchestrator.Outcome{}, err)
	}
	// The operator typed the identifier, so the run records that as why it exists.
	// It is worth recording even though it is obvious to whoever typed it: what an
	// operator reads later is a list of runs, and one that says nothing about why
	// it was chosen is indistinguishable from one the harness chose unaccountably.
	pipeline.Selection = runstate.OperatorSelection(
		"the operator ran this item by name from the command line", time.Now())
	outcome, err := pipeline.Run(ctx, positional[0])
	return reportRunResult(stdout, stderr, *jsonOutput, outcome, err)
}

// components are the durable and repository-facing parts every command that
// acts on runs shares. They are built once here so a pipeline and a reconciler
// always address the same state root, worktree root, and repository.
type components struct {
	config config.Config
	// configPath is the configuration file these parts were built from, kept so
	// a command that reads something else the project keeps beside it — a
	// workflow definition of its own — looks in the directory this configuration
	// actually came from rather than guessing at one.
	configPath string
	repository string
	// stateRoot is where everything durable that is not the repository lives.
	// It is kept here so a command that needs another store built on it — the
	// conversation record and the collected reports beside the run state —
	// addresses the same root.
	stateRoot string
	runner    execution.OSProcessRunner
	store     *runstate.Store
	// reports is the collected pile of what agents noticed while their work
	// carried on. It is built beside the run store because it is durable in the
	// same way and outlives the runs that fill it.
	reports *runstate.ReportStore
	// amendments is where changes proposed to documents their proposer does not
	// own are kept, with what the owner or the operator decided about each. It is
	// built beside the reports because it is durable in the same way and for the
	// same reason: the argument outlives the run that made it.
	amendments *runstate.AmendmentStore
	// evaluations is where the product manager's recommendations about the
	// operator's ideas are kept. It is built beside the amendments for the same
	// reason: the reasoning outlives the conversation that reached it, and a
	// decision taken weeks later is the one that most needs it.
	evaluations *runstate.EvaluationStore
	// restartRequests is where a program manager's requests that the supervisor
	// restart a part of the product are kept, until the supervisor's pass acts on
	// them.
	restartRequests *runstate.RestartRequestStore
	// docket is the work that has stopped moving, waiting for the development
	// manager to decide what becomes of it. It is built beside the reports for
	// the same reason: an entry outlives the run that produced it, and a run
	// whose artifacts have been cleaned up leaves a stoppage that is still
	// somebody's to decide.
	docket *runstate.DocketStore
	// branchReviews is where verdicts on accumulated changes are recorded. It is
	// its own store for the same reason: a branch review outlives every run whose
	// work it judged, and it belongs to no one of them.
	branchReviews *runstate.BranchReviewStore
	// directives is what the operator has told the harness. It is built beside
	// the run store rather than inside it because that is what makes a directive
	// reach work regardless of which agent received it: every command here
	// addresses the same product-scoped records.
	directives *runstate.DirectiveStore
	// holds is the operator's switch over everything the harness would spend on a
	// provider. It is built on the state root itself rather than under the
	// product, because one switch pauses the machine: what makes an operator pause
	// is an account or an afternoon, neither of which belongs to a product.
	holds *runstate.OperatorHoldStore
	// intake is the operator's switch over the work the harness chooses for
	// itself. It is built under the product rather than on the state root,
	// because unlike the hold above it is about one backlog: holding what a
	// development manager may pull from this product must leave another alone.
	intake *runstate.IntakeHoldStore
	// watch is what a session that stays open says it is doing. It is built
	// under the product beside the intake hold, because what it watches is one
	// backlog and because the two are read together: a session that is choosing
	// nothing and a hold that says why are one story to whoever reads them.
	watch *runstate.WatchStore
	// releasedClaims is where a claim the harness gave back is written down. It is
	// built under the product beside the watch log, and read together with it for
	// the same reason: a claim outlives its run only when the process holding it
	// died, which is the same silence the session log is read for.
	releasedClaims *runstate.ClaimStore
	// usageLimits is where a provider refusing the harness outside a run is
	// recorded. It is built under the product beside the watch log, because a
	// limit is exhausted for an account and read as a fact about one product's
	// line stopping: the processes that meet one — a conversation, a review —
	// have no run between them to write it on.
	usageLimits *runstate.UsageLimitStore
	// outages is the product's record of the provider answering nobody — a login
	// nobody has renewed, an API nothing reaches. It is built under the product
	// beside the usage limits and for the same reason: the processes that meet
	// one have no run between them to write it on, and every surface that names
	// the wait reads it from here.
	outages *runstate.ProviderOutageStore
	// capacityServed is the latest moment the provider served each account and
	// model, written by every served invocation and read against the usage
	// limits: a refusal recorded before it on that account and model is lifted.
	capacityServed *runstate.CapacityServedStore
	// divergences is the product's record of the target branches the harness
	// will not catch up to the remote's: written by the run whose promotion is
	// refused on one, read by a watching session that chooses nothing while one
	// stands, and lifted by the convergence sweep that finds the branches
	// converged.
	divergences *runstate.DivergedTargetStore
	// spend is the cost log every provider invocation this process makes lands
	// in. It is built under the product beside the usage limits, and for the
	// mirror-image reason: that log says when the harness could not spend, and
	// this one says what it did spend, one line per invocation, whether the
	// invocation was a run's, a review's, a conversation's, or an exchange's.
	spend *runstate.SpendStore
	// trackerListings is how the tracker's listings stand: answering, or failing
	// since a moment. Every listing the harness's tracker client makes writes its
	// outcome there; see tracker.
	trackerListings *runstate.TrackerListingStore
	worktrees       *gitworktree.Manager
	redactValues    []string
}

// roots is where a product's three durable places are: the checkout the runs
// are cut from, the directory their worktrees are cut into, and the state root
// everything that is not the repository lives under.
type roots struct {
	repository   string
	worktreeRoot string
	stateRoot    string
}

// resolveRoots resolves the three from the configuration. It is separate from
// the components because a surface that reads without acting — `yoyo status`,
// the Slack sink — builds no components and still has to ask the repository
// whether a stopped run's change is there, which needs the same two roots the
// worktree manager is built on.
func resolveRoots(resolved config.Resolved) (roots, error) {
	cfg := resolved.Config
	// Relative paths resolve against the project, not against the .yoyodyne
	// directory the configuration happens to live in.
	projectDirectory := config.ProjectDirectory(resolved.Path)
	repository, err := resolvePath(projectDirectory, cfg.Product.Repository)
	if err != nil {
		return roots{}, fmt.Errorf("resolve product repository: %w", err)
	}

	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return roots{}, err
	}
	worktreeRoot := cfg.Execution.WorktreeRoot
	if worktreeRoot == "auto" {
		worktreeRoot = filepath.Join(stateRoot, "worktrees", string(cfg.Product.ID), string(cfg.Product.RepositoryID))
	} else {
		worktreeRoot, err = resolvePath(projectDirectory, worktreeRoot)
		if err != nil {
			return roots{}, fmt.Errorf("resolve worktree root: %w", err)
		}
	}
	// A verb run from inside one of those worktrees resolves its repository to the
	// worktree itself — the project's configuration is checked in, so the nearest
	// one is the copy the worktree carries — and a repository beneath the worktree
	// root is what the containment check refuses. That is exactly where inspection
	// and recovery happen: a preserved worktree after a failed run, and an agent
	// running yoyo from its own. So the repository is resolved to the checkout the
	// worktree was added from, which is what every command here means by "the
	// repository" anyway, and is what `yoyo reports` gets for free by never
	// building a worktree manager at all.
	repository, err = primaryCheckout(repository, worktreeRoot)
	if err != nil {
		return roots{}, err
	}
	return roots{repository: repository, worktreeRoot: worktreeRoot, stateRoot: stateRoot}, nil
}

// productStateRoot is the state root every command opens a product's records
// under, and the only way a command in this package reaches one. It resolves the
// root the one way runstate.ResolveRoot does, and then agrees it with the marker
// in the configured repository's Git directory: the first process records the
// root there, and a process that resolved a different one refuses to start
// naming both, so one product's state is never split between two roots by two
// layers of configuration. The watch, the sink, the dashboard, the supervisor,
// conversations, and runs all open their stores through here.
func productStateRoot(resolved config.Resolved) (string, error) {
	return productStateRootFrom(resolved, os.Getenv, os.UserHomeDir, runtime.GOOS)
}

// productStateRootFrom is productStateRoot read through the seams a caller
// holds rather than the process's own, so a walk that injects its environment
// resolves the root it would name.
func productStateRootFrom(resolved config.Resolved, getenv func(string) string, homeDir func() (string, error), goos string) (string, error) {
	root, err := runstate.ResolveRoot(getenv, homeDir, goos)
	if err != nil {
		return "", err
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return "", fmt.Errorf("resolve product repository: %w", err)
	}
	if err := runstate.AgreeRoot(repository, root); err != nil {
		return "", err
	}
	return root.Path, nil
}

// standingRemains is the repository a read-only surface asks whether a stopped
// run's change is still there. It is the same manager the components build,
// stripped to what a look needs, and a surface that cannot build one reads the
// holds from the record instead: a status line is owed the answer it can give
// rather than a failure over the one it cannot.
func standingRemains(resolved config.Resolved) readmodel.Remains {
	roots, err := resolveRoots(resolved)
	if err != nil {
		return nil
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: roots.repository,
		WorktreeRoot:   roots.worktreeRoot,
	})
	if err != nil {
		return nil
	}
	return worktrees
}

func buildComponents(configPath string) (components, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return components{}, err
	}
	cfg := resolved.Config
	roots, err := resolveRoots(resolved)
	if err != nil {
		return components{}, err
	}
	repository, worktreeRoot, stateRoot := roots.repository, roots.worktreeRoot, roots.stateRoot
	cfg.Product.Repository = repository

	processRunner := execution.OSProcessRunner{}
	store, err := runstate.NewStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	reports, err := runstate.NewReportStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	amendments, err := runstate.NewAmendmentStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	evaluations, err := runstate.NewEvaluationStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	restartRequests, err := runstate.NewRestartRequestStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	observeProgramManagers(cfg, stateRoot, time.Now())
	docket, err := runstate.NewDocketStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	branchReviews, err := runstate.NewBranchReviewStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	directives, err := runstate.NewDirectiveStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		return components{}, err
	}
	intake, err := runstate.NewIntakeHoldStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	watch, err := runstate.NewWatchStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	releasedClaims, err := runstate.NewClaimStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	usageLimits, err := runstate.NewUsageLimitStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	outages, err := runstate.NewProviderOutageStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	capacityServed, err := runstate.NewCapacityServedStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	divergences, err := runstate.NewDivergedTargetStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	spendLog, err := runstate.NewSpendStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	trackerListings, err := runstate.NewTrackerListingStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                processRunner,
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		Remote:                cfg.Execution.Remote,
		PushRemote:            cfg.Execution.PushRemote,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
		// The tracker's export of the work items themselves is what a run reads to
		// see the work around its own, so a worktree is given the primary
		// checkout's copy rather than the one its base commit carried. The
		// interactions export is not here because nothing in a run reads it.
		CurrentExports: []string{beads.ExportPath},
		PrepareExports: (beads.Client{Runner: processRunner, Dir: repository}).RefreshExport,
		// A listing that described this repository without a registration another
		// run had not finished writing is the one thing the manager works around,
		// and nothing else would ever say so. It goes to standard error rather than
		// into a command's answer, which is on standard output and which a command
		// asked for JSON has to keep parseable.
		Note: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	})
	if err != nil {
		return components{}, err
	}
	return components{
		config:          cfg,
		configPath:      resolved.Path,
		repository:      repository,
		stateRoot:       stateRoot,
		runner:          processRunner,
		store:           store,
		reports:         reports,
		amendments:      amendments,
		evaluations:     evaluations,
		restartRequests: restartRequests,
		docket:          docket,
		branchReviews:   branchReviews,
		directives:      directives,
		holds:           holds,
		intake:          intake,
		watch:           watch,
		releasedClaims:  releasedClaims,
		usageLimits:     usageLimits,
		outages:         outages,
		capacityServed:  capacityServed,
		divergences:     divergences,
		spend:           spendLog,
		trackerListings: trackerListings,
		worktrees:       worktrees,
		redactValues:    execution.SensitiveEnvironmentValues(os.Environ()),
	}, nil
}

// primaryCheckout is the repository a command addresses, given the one its
// configuration resolved to and the root the harness keeps its worktrees under.
// Ordinarily that is the repository itself. Where the repository is one of the
// managed worktrees it is the checkout that worktree was added from, which is the
// same resolution configuration discovery already makes when it looks for a
// worktree's external configuration: a worktree is the project it came from
// rather than a project of its own.
func primaryCheckout(repository, worktreeRoot string) (string, error) {
	managed, err := gitworktree.WithinWorktreeRoot(worktreeRoot, repository)
	if err != nil {
		return "", fmt.Errorf("compare the repository with the worktree root: %w", err)
	}
	if !managed {
		return repository, nil
	}
	// What the refusal has to say is what to do instead, because whoever reads it
	// is standing in the directory it is refusing.
	unresolved := fmt.Errorf("the repository %s is inside the worktree root %s and is not a worktree of a checkout outside it;"+
		" run yoyo from the checkout the worktrees were added from, or set execution.worktree_root to a directory outside the repository",
		repository, worktreeRoot)
	primary, err := config.RepositoryRoot(repository)
	if err != nil {
		return "", errors.Join(unresolved, err)
	}
	if primary == "" {
		return "", unresolved
	}
	managed, err = gitworktree.WithinWorktreeRoot(worktreeRoot, primary)
	if err != nil {
		return "", fmt.Errorf("compare the checkout %s was added from with the worktree root: %w", repository, err)
	}
	if managed {
		return "", unresolved
	}
	return primary, nil
}

// tracker is the harness's own tracker client. Every listing it makes writes
// how it ended to the product's listing record, which is what `yoyo status`
// reads to say the tracker is not answering listings and since when.
func (c components) tracker() beads.Client {
	return withListingRecord(beads.Client{Runner: c.runner, Dir: c.repository}, c.trackerListings)
}

// withListingRecord has a client write how its listings end to the record,
// where there is one. The nil check is on the store rather than left to the
// interface, because a nil *TrackerListingStore in the interface is not nil.
func withListingRecord(client beads.Client, listings *runstate.TrackerListingStore) beads.Client {
	if listings != nil {
		client.Listings = listings
	}
	return client
}

func buildPipeline(configPath string) (orchestrator.Pipeline, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Pipeline{}, err
	}
	return pipelineFrom(parts), nil
}

// pipelineFrom wires the run pipeline over parts that are already built, so a
// command that needs more than one of them — a conversation that also steers
// work — builds the repository and state boundaries exactly once.
func pipelineFrom(parts components) orchestrator.Pipeline {
	cfg := parts.config
	processRunner := parts.runner
	redactValues := parts.redactValues

	// Each role's provider is resolved from what this project declares rather
	// than assumed to be the one this build ships: a project that declared a
	// provider of its own gets that provider's dialect and executable, so its
	// rules read the stream its invocation actually produces. A built-in
	// declares neither and runs exactly as it always has.
	developerProvider := providerBackend(cfg, agentForRole(cfg, domain.RoleDeveloper).Backend, processRunner)
	reviewerProvider := providerBackend(cfg, agentForRole(cfg, domain.RoleReviewer).Backend, processRunner)

	return orchestrator.Pipeline{
		Availability: machineAvailability(parts),
		Tracker:      parts.tracker(),
		Worktrees:    parts.worktrees,
		Store:        parts.store,
		// The same store, named again because a workflow instance is written
		// through it rather than through the interface a run's record goes
		// through. It is wired unconditionally, and a project that rolled back to
		// the legacy path records nothing through it.
		Instances: parts.store,
		Backend:   developerProvider,
		Checks: checks.Runner{
			Process: processRunner,
			// The budget every check gets is configured rather than fixed,
			// because it is a property of the project's suite and of how many
			// runs share the machine with it, neither of which the harness knows.
			Timeout: cfg.Execution.CheckTimeout.Duration(),
			// And the budget the whole stage gets, for the reason it is
			// configured at all: a stage bounded only check by check is bounded
			// at the sum of the list, which is hours.
			StageTimeout: cfg.Execution.CheckStageTimeout.Duration(),
			RedactValues: redactValues,
		},
		// The stage's bound is that figure scaled for the machine's load, by the
		// same reading and cap a local Git command's budget is scaled by, because
		// a flat figure stops working suites when three runs share the machine.
		Load: gitworktree.MachineLoad,
		// The landing checks run in a checkout of the integrated commit the
		// worktree manager cuts for them, and a red landing files its item through
		// the tracker. Both are the harness's own access, wired here so that no
		// agent is ever asked to perform either.
		Landings: parts.worktrees,
		Filer:    parts.tracker(),
		// And each running part's record of the configuration keys its build
		// reads, so a landing names the parts that cannot read what landed.
		ConfigReaders: landingConfigReaders(parts.stateRoot, cfg.Product.ID),
		// The reviewer runs its own provider invocation, so it is built from a
		// separate backend value rather than sharing the developer's, and with
		// the reviewer agent's own required model selector and effective
		// persona.
		Reviewer: review.Reviewer{
			Backend: reviewerProvider,
			Model:   agentModel(cfg, domain.RoleReviewer),
			Effort:  cfg.InvocationEffort(agentForRole(cfg, domain.RoleReviewer), agentModel(cfg, domain.RoleReviewer)),
			Persona: agentForRole(cfg, domain.RoleReviewer).Persona.Text,
			// The reviewer's invocation is its own spend and lands in the same log
			// the developer's does, charged to the review rather than to the change.
			Spend: parts.spend,
		},
		// The publisher is the harness's own forge access, wired here so that no
		// agent is ever asked to invoke it and nothing about it reaches a prompt
		// or a context bundle. It is only consulted when the project opted in to
		// publishing.
		Publisher: publish.GitHub{
			Runner: processRunner,
			Dir:    parts.repository,
			// The forge client speaks about the same remotes the Git side does,
			// so a checkout with several remotes publishes to the configured
			// one rather than to whichever the CLI would infer — and a project
			// publishing from a fork opens its request across the same two
			// repositories its branches are pushed between.
			Remote:       cfg.Execution.Remote,
			PushRemote:   cfg.Execution.PushRemote,
			RedactValues: redactValues,
		},
		// What the developer and the reviewer report while their work carries on
		// is collected here. It is wired as its own store rather than through the
		// run state, because a report outlives the run that made it: the run is
		// settled and its artifacts are removed, and what it reported is still
		// waiting for somebody to read.
		Reports: parts.reports,
		// What the operator has directed is read from the product's own records
		// every time a run is about to commit to work. It is wired here rather
		// than delivered into a prompt because a directive that pauses work is not
		// something an agent weighs: it is a reason the work must not proceed, and
		// the harness is what enforces it.
		Directives: parts.directives,
		// The operator's hold over provider spending, read at every boundary where
		// this pipeline would spend. It is wired here rather than delivered into a
		// prompt for the same reason a directive is: a paused harness is not
		// something an agent weighs.
		Holds: parts.holds,
		// The operator's hold on the work the harness chooses for itself, read
		// where a run would be started for a reason other than the operator naming
		// the item. It stops nothing already under way, which is the whole reason
		// it is a second switch rather than part of the first.
		Intake: parts.intake,
		// The product's record of the provider answering nobody, written by the run
		// that meets it and cleared by the first the provider answers again. It is
		// wired here so a run's wait is one every surface can name, and so the
		// operator is told what ends it rather than sent to release a hold.
		ProviderOutages: parts.outages,
		// Where an invocation the provider served records the account and model it
		// was served on, which is what reads a refusal of them as lifted before the
		// reset it quoted.
		CapacityServed: parts.capacityServed,
		// A promotion refused because the target branch will not catch up to the
		// remote's is recorded against the product, so a watching session stops
		// pulling items into the same refusal until the branches are settled.
		DivergedTargets: parts.divergences,
		// A change an agent proposes to a document it may not edit is recorded
		// here, for the same reason and in the same way: the run that argued the
		// design was wrong is over long before anybody decides what to do about it,
		// and the proposal has to still be there when they do.
		Amendments: parts.amendments,
		// What a run costs is already recorded in its event log, and the item it
		// served is already recorded beside it. This is the join: as a run ends,
		// the item it was for is priced across every run ever made for it, and the
		// price is put where the tracker carries it.
		Prices: ledgerFrom(parts),
		// Where every provider invocation this run makes says what it spent, as it
		// spends it. It is the same log the conversations and the exchanges write
		// to, because what the operator asked to see is what the harness spends on
		// their behalf rather than what any one of its parts does.
		Spend: parts.spend,
		// Where a run that ends on a durable blocker is put in front of the
		// development manager. It is wired here rather than delivered into a
		// prompt for the same reason the directives are: what has stopped moving
		// is a fact the harness records, and the role that decides about it is not
		// the one that stopped.
		Docket:   docketerFrom(parts),
		NewRunID: runstate.NewRunID,
		// Which of the configured accounts serves a fresh run. It is wired from
		// here rather than held by the pipeline because choosing needs the run
		// records as well as the configuration, and this is where both are in
		// hand. A project with one account has nothing to choose between and gets
		// the same answer the configuration alone would have given.
		Accounts:   accountPool{config: cfg, stateRoot: parts.stateRoot, runs: parts.store},
		StateRoot:  parts.stateRoot,
		Repository: parts.repository,
		Config:     cfg,
		// Where the configuration was read from, which is where this project keeps
		// the files it owns: a run executes the project's own copy of the delivery
		// definition where there is one under here, and the built-in otherwise.
		ConfigPath: parts.configPath,
		// Which harness is dispatching, read once here for the reason the watch
		// session reads its own the same way: a process does not change binary
		// while it lives, and that is exactly what has to be written down — a
		// resident goes on dispatching from what it was started with while the
		// harness moves on underneath it. A binary that recorded no revision leaves
		// this empty, which reads as a comparison nobody can make.
		Build:        buildinfo.Commit(),
		RedactValues: redactValues,
	}
}

// ledgerFrom wires the ledger that prices work items over parts that are
// already built, so the price a run records and the price `yoyo cost` reports
// are read from one set of records and written to one tracker.
//
// Recording a price is one bd command per item, and the tracker carries the
// bound on it, so a backfill of a hundred items is a hundred separately bounded
// writes rather than one long one nothing can interrupt.
func ledgerFrom(parts components) cost.Ledger {
	tracker := parts.tracker()
	tracker.Timeout = costTrackerTimeout
	return cost.Ledger{Prices: parts.store, Tracker: tracker}
}

// docketerFrom wires the triage docket over parts that are already built, so
// the entries a run makes, the ones a sweep finds, and the docket a development
// manager reads are one log rather than three.
func docketerFrom(parts components) *orchestrator.Docketer {
	return &orchestrator.Docketer{
		Docket: parts.docket,
		Runs:   parts.store,
		// The two records the triage guards spend and refuse against: what has
		// been decided about the item, and what the harness has carried out of it.
		// The docket reads those rather than a count of its own, so a decision a
		// guard would enforce is never one the docket shows as absent.
		Decisions: parts.store.Triage(),
		Reruns:    parts.store.Reruns(),
		// The stops asked of runs, so a run that stopped for another reason after
		// one was asked is docketed saying so, as a stoppage the stop did not decide.
		Stops: parts.store,
		// The same caps the conversation's budgets spend against, assembled once,
		// so what an entry says an item has left and what refuses the next
		// decision about it are one set of numbers.
		Caps:   orchestrator.TriageCaps(parts.config.Execution, parts.config.Triage),
		Triage: parts.config.Triage,
		// Which product an entry belongs to, for the one entry that is not made
		// from a run record and so has no run to read it off.
		ProductID: parts.config.Product.ID,
		// The repository, asked what a stopped run left as its entry is written
		// and as the docket is built for the development manager to read, so an
		// entry never states preservation off the run's removal flags.
		Remains: remainsOf(parts),
		// Where an item the tree is not ready for is also said to the product
		// manager, in the pile her conversation is given, attributed the way
		// every report is.
		Reports:      parts.reports,
		RepositoryID: string(parts.config.Product.RepositoryID),
		Harness:      buildinfo.Commit(),
	}
}

func buildReconciler(configPath string) (orchestrator.Reconciler, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Reconciler{}, err
	}
	return reconcilerFrom(parts), nil
}

// reconcilerFrom wires reconciliation over parts that are already built. It is
// deliberately given no backend: settling an interrupted run is never a reason
// to invoke a provider. The forge client it does get can only ask what became
// of a merge the forge queued, which is the one thing a finished run can still
// be waiting on. The continuations the sweep makes — a run that exited on
// its in-process usage-limit bound, continued once its deadline has passed, and
// a queued merge put back at its promotion to bring its head up to date — are
// wired by the sweep verb alone, in reconcile.go, because whatever wires it
// hosts the continued run to its end and the conversation's settle must not.
func reconcilerFrom(parts components) orchestrator.Reconciler {
	forge := publish.GitHub{
		Runner:       parts.runner,
		Dir:          parts.repository,
		Remote:       parts.config.Execution.Remote,
		PushRemote:   parts.config.Execution.PushRemote,
		RedactValues: parts.redactValues,
	}
	return orchestrator.Reconciler{
		Tracker:       parts.tracker(),
		Worktrees:     parts.worktrees,
		Store:         parts.store,
		Repository:    parts.repository,
		ConfigFiles:   parts.worktrees,
		ConfigReaders: landingConfigReaders(parts.stateRoot, parts.config.Product.ID),
		Publisher:     forge,
		// A merge the forge still holds is read with its checks, and a red one is
		// withdrawn before it is handed back or brought up to date.
		Checks: forge,
		// A queued merge whose checks fail on the target itself is filed as the
		// target's, through the same tracker a red landing files through, with the
		// failing job's log read through the same forge access.
		Filer:   parts.tracker(),
		JobLogs: forge,
		// Before a check is filed as the target's, the forge is asked whether it
		// is red on the target's own head too.
		TargetChecks: forge,
		// Bringing a queued head up to date is a replay, and it makes a finished
		// run live again, so it reads the hold and the slots a resumption reads.
		// Only the sweep verb hosts the run it makes live; a pass without Continue
		// leaves the merge queued.
		Intake:   parts.intake,
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		// The operator's pause, read before a run parked on it is settled: while
		// it stands the park is theirs, and only a lifted one nothing continued is
		// settled as a run with no process behind it.
		Holds: parts.holds,
		// A run this sweep stops is docketed as it is settled, so a stoppage the
		// process that made it never got to record still reaches the development
		// manager.
		Docket: docketerFrom(parts),
		// The claims the audit gave back, so a release that did not say its run's
		// change was still on a branch is corrected on the item by the sweep.
		Releases: releasesOf(parts),
		// A target this sweep finds converged lifts the divergence recorded on it,
		// which is what lets a line held on that divergence choose again.
		Divergences: parts.divergences,
	}
}

// releasesOf is the released-claim log where these parts have one, kept as no
// reader rather than an interface holding nil.
func releasesOf(parts components) orchestrator.ReconcileReleases {
	if parts.releasedClaims == nil {
		return nil
	}
	return parts.releasedClaims
}

// supervisionLoopFrom wires one pass of the management loop over parts that are
// already built.
//
// It is given no voice, for the same reason reconciliation is given no backend:
// recovering from a lost process is a question about recorded evidence, and
// never a reason to put a question in front of a role that nobody asked. So this
// pass reclaims the rounds whose carrier is gone, closes the threads that ran
// out of rounds, and asks nothing. Asking is the wakeup half of the loop and
// arrives with the thing that wakes roles.
func supervisionLoopFrom(parts components) (orchestrator.SupervisionLoop, error) {
	store, err := runstate.NewExchangeStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return orchestrator.SupervisionLoop{}, err
	}
	return orchestrator.SupervisionLoop{
		Store: store,
		Conductor: exchange.Conductor{
			Store: store,
			// A thread that ran out of rounds reaches the operator through the same
			// pile every role's reports land in.
			Reports:      parts.reports,
			MaxRounds:    parts.config.Exchange.MaxRounds,
			ProductID:    parts.config.Product.ID,
			RepositoryID: string(parts.config.Product.RepositoryID),
			Holder:       supervisionHolder(),
		},
	}, nil
}

// supervisionHolder names this process on every round it takes. The process
// identifier is what makes it useful: a later pass reading a round nobody
// answered says which process was carrying it, and an operator can tell a
// harness that is still running from one that is not.
func supervisionHolder() string {
	return fmt.Sprintf("yoyo pid %d", os.Getpid())
}

// agentModel returns the configured selector for a role. Configuration
// validation already requires one for every agent, so an empty result means the
// role is not configured at all and the pipeline refuses the run.
func agentModel(cfg config.Config, role domain.AgentRole) string {
	return agentForRole(cfg, role).Model
}

// agentForRole returns the effective agent that fills a role, chosen by name so
// the same configuration always wires the same agent.
func agentForRole(cfg config.Config, role domain.AgentRole) config.AgentConfig {
	if name := agentNameForRole(cfg, role); name != "" {
		return cfg.Agents[name]
	}
	return config.AgentConfig{}
}

// agentNameForRole names that agent, which is what a record attributed to it
// says: a project may configure more than one agent for a role, and the role
// alone would not say which of them acted.
func agentNameForRole(cfg config.Config, role domain.AgentRole) string {
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if cfg.Agents[name].Role == role {
			return name
		}
	}
	return ""
}

func resolvePath(base, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(path)
}

func reportRunResult(stdout, stderr io.Writer, jsonOutput bool, outcome orchestrator.Outcome, err error) int {
	if jsonOutput {
		result := runOutput{}
		if outcome.RunID != "" || outcome.WorkItemID != "" || outcome.Status != "" {
			result.Outcome = &outcome
		}
		if err != nil {
			result.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, result); code != 0 {
			return code
		}
	} else if outcome.Paused {
		// A paused run is neither a success nor a failure: it is in flight and
		// owed a continuation. Reporting it as either would tell an operator to do
		// something about a run that only needs to be started again.
		//
		// A directive pause is the one that can have no run behind it at all,
		// because a directive stops work before it is claimed as readily as during
		// it. So it is reported on its own terms: naming a run that was never
		// started, or a preserved worktree that never existed, would send an
		// operator looking for artifacts nothing made.
		// The operator's hold is the other pause that can have no run behind it,
		// and it is reported on its own terms for the same reason: what lifts it is
		// one command, and nothing about this work item has anything to do with it.
		// A held intake is the third pause with no run behind it, and the one that
		// never applies to work an operator named: it is reported on its own terms
		// so nothing describes it as a run waiting on a provider.
		if outcome.PausedByIntake != nil {
			reportIntakeHold(stdout, outcome)
		} else if outcome.PausedByOperator != nil {
			reportOperatorHold(stdout, stderr, outcome, err)
		} else if outcome.PausedByDirective != nil {
			reportDirectivePause(stdout, outcome)
			if err != nil {
				fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
			}
		} else if outcome.PausedByDependency != nil {
			// A dependency pause is the fourth that can have no run behind it, and it
			// is reported on its own terms for the same reason: what lifts it is other
			// work finishing, and nothing about it is a wait on a provider.
			reportDependencyPause(stdout, outcome)
			if err != nil {
				fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
			}
		} else if outcome.PausedByTracker != nil {
			// A tracker park always has a run behind it, and is reported on its own
			// terms because what lifts it is the store answering rather than a
			// provider, a person, or other work.
			reportTrackerPause(stdout, outcome)
			if err != nil {
				fmt.Fprintf(stderr, "the park is recorded, but reporting it failed: %v\n", err)
			}
		} else {
			reportRunPause(stdout, stderr, outcome, err)
		}
	} else if err != nil {
		fmt.Fprintf(stderr, "run failed: %v\n", err)
		if outcome.RunID != "" {
			fmt.Fprintf(stderr, "run: %s\n", outcome.RunID)
		}
		if outcome.RepairAttempts > 0 {
			fmt.Fprintf(stderr, "repair attempts: %d\n", outcome.RepairAttempts)
		}
		if outcome.TransientRelaunches > 0 {
			fmt.Fprintf(stderr, "relaunches after a provider death: %d\n", outcome.TransientRelaunches)
		}
		// A blocked item is not waiting on this process: what stopped it is
		// recorded where the work is tracked and needs a person. Several different
		// things block a run — unresolved findings, a failing check, a target
		// branch that kept moving, a provider that kept killing it — so this says
		// where to look rather than naming one of them and being wrong.
		if outcome.Blocked {
			fmt.Fprintf(stderr, "blocker recorded on %s; the failure above says what stopped it\n", outcome.WorkItemID)
		}
		// An artifact recorded as removed is never described as preserved.
		if outcome.Branch != "" && !outcome.BranchRemoved {
			fmt.Fprintf(stderr, "preserved branch: %s\n", outcome.Branch)
		}
		if outcome.WorktreePath != "" && !outcome.WorktreeRemoved {
			fmt.Fprintf(stderr, "preserved worktree: %s\n", outcome.WorktreePath)
		}
		if outcome.BranchRemoved {
			fmt.Fprintf(stderr, "branch was already removed: %s\n", outcome.Branch)
		}
		if outcome.WorktreeRemoved {
			fmt.Fprintf(stderr, "worktree was already removed: %s\n", outcome.WorktreePath)
		}
		if outcome.Integration != nil {
			fmt.Fprintf(stderr, "already integrated into %s: %s\n", outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
		}
		reportPullRequest(stderr, outcome)
	} else {
		fmt.Fprintf(stdout, "run succeeded: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		reportPullRequest(stdout, outcome)
		if outcome.RepairAttempts > 0 {
			fmt.Fprintf(stdout, "repair attempts: %d\n", outcome.RepairAttempts)
		}
		if outcome.TransientRelaunches > 0 {
			fmt.Fprintf(stdout, "relaunches after a provider death: %d\n", outcome.TransientRelaunches)
		}
		if outcome.Integration == nil {
			fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		} else {
			fmt.Fprintf(stdout, "review: %s (session %s, model %s)\n", outcome.ReviewDecision, outcome.ReviewSessionID, outcome.ReviewModel)
			fmt.Fprintf(stdout, "integrated into %s: %s\n", outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
			if outcome.WorktreeRemoved {
				fmt.Fprintf(stdout, "worktree removed: %s\n", outcome.WorktreePath)
			} else {
				fmt.Fprintf(stdout, "worktree NOT removed: %s\n", outcome.WorktreePath)
			}
			if outcome.BranchRemoved {
				fmt.Fprintf(stdout, "branch removed: %s\n", outcome.Branch)
			} else {
				fmt.Fprintf(stdout, "branch NOT removed: %s\n", outcome.Branch)
			}
			// What the landing checks made of the integrated commit is said last
			// among the facts of the landing, because it is the one that is not
			// about this run: the run succeeded whichever way it went, and a red
			// one is the target branch's news and the item it filed.
			if outcome.LandingChecks != nil {
				fmt.Fprintf(stdout, "landing checks: %s\n", outcome.LandingChecks.Describe())
			}
		}
		if outcome.CleanupFailure != "" {
			// The run succeeded; only the artifacts that actually survive still
			// need an operator, and cleanup can simply be retried. A failure
			// with nothing left is a failed confirmation, not leftover work.
			if outcome.WorktreeRemoved && outcome.BranchRemoved {
				fmt.Fprintf(stderr, "cleanup could not be confirmed after a successful run: %s\n", outcome.CleanupFailure)
				fmt.Fprintln(stderr, "both artifacts were removed; nothing is known to remain")
			} else {
				fmt.Fprintf(stderr, "cleanup incomplete after a successful run: %s\n", outcome.CleanupFailure)
				if !outcome.BranchRemoved {
					fmt.Fprintf(stderr, "remaining branch: %s\n", outcome.Branch)
				}
				if !outcome.WorktreeRemoved {
					fmt.Fprintf(stderr, "remaining worktree: %s\n", outcome.WorktreePath)
				}
			}
		}
		if outcome.PublishFailure != "" {
			// The promotion itself succeeded: the local target branch is the
			// authoritative one and it already moved. Only the publication of it
			// is unfinished, so this is reported as outstanding rather than as a
			// change that did not land.
			fmt.Fprintf(stderr, "publication incomplete after a successful run: %s\n", outcome.PublishFailure)
			fmt.Fprintln(stderr, "the change is integrated locally; the pull request still needs to be reconciled by hand")
		}
		if outcome.CompletionRecordingFailure != "" {
			// Cleanup finished here; only writing it down did not. Saying
			// anything about remaining artifacts would send an operator after
			// files that are gone.
			fmt.Fprintf(stderr, "completion recording failed after a successful run: %s\n", outcome.CompletionRecordingFailure)
			fmt.Fprintln(stderr, "cleanup completed: the worktree and branch were both removed and nothing remains to clean up")
		}
		fmt.Fprintf(stdout, "base commit: %s\n", outcome.BaseCommit)
		if outcome.Changes.Status != "" {
			fmt.Fprintf(stdout, "changes:\n%s\n", outcome.Changes.Status)
		}
		if outcome.Changes.DiffStat != "" {
			fmt.Fprintf(stdout, "diff stat:\n%s\n", outcome.Changes.DiffStat)
		}
	}
	if !jsonOutput {
		// What the run's agents reported is collected whichever way the run went,
		// so it is named whichever way this reports.
		reportCollectedReports(stdout, console.ThemeFor(stdout, os.Getenv), outcome)
		// And so is what they proposed changing in a document they do not own: it
		// is waiting on a person either way, and a proposal nobody is told about is
		// one nobody decides.
		reportProposedAmendments(stdout, outcome)
		// So is what the work item has cost: a failed attempt spent money too, and
		// the price is of the item rather than of this run.
		reportItemCost(stdout, stderr, outcome)
	}
	if err != nil {
		return 1
	}
	return 0
}

// reportRunPause describes a run that is waiting on the provider: an exhausted
// usage limit, or an invocation the harness stopped on time. Both leave a run in
// flight with its artifacts preserved, and both are continued by starting the
// item again.
func reportRunPause(stdout, stderr io.Writer, outcome orchestrator.Outcome, err error) {
	fmt.Fprintf(stdout, "run paused: %s\n", outcome.RunID)
	if outcome.ProviderStop != "" {
		// A stall and an exhausted budget are different facts about the run, and
		// only one of them is worth investigating.
		if outcome.ProviderStop == runstate.ProviderStopStalled {
			fmt.Fprintln(stdout, "the AI session running it produced no output for longer than the harness allows, so the harness stopped it; the cause was outside the work, and it reported no failure")
		} else {
			fmt.Fprintln(stdout, "the provider was still working when its total budget ran out; it reported no failure")
		}
	} else {
		fmt.Fprintf(stdout, "waiting out %s\n", runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind))
		if outcome.UsageLimitResetsAt != nil {
			fmt.Fprintf(stdout, "asks again by: %s\n", outcome.UsageLimitResetsAt.Format(time.RFC3339))
		}
	}
	fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
	fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
	if outcome.ProviderStop != "" {
		fmt.Fprintf(stdout, "run yoyodyne on %s again to continue this run\n", outcome.WorkItemID)
	} else {
		// The recorded time bounds the wait rather than gating it, so continuing is
		// not something to hold off until it passes. What does need saying is the
		// one verb that overrides it, for when the refusal has stopped being true.
		fmt.Fprintf(stdout, "run yoyodyne on %s again to continue this run; it asks the provider again at its probe interval rather than only at that time\n", outcome.WorkItemID)
		fmt.Fprintf(stdout, "`yoyo resume %s` releases the wait now if the provider would serve it already\n", outcome.WorkItemID)
	}
	if err != nil {
		fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
	}
}

// reportDirectivePause describes work held up by an unresolved user directive.
// It names the directive in full, because what lifts the pause is somebody
// reading it and deciding, and it says which of the two cases this is: a run
// that was under way and is preserved, or work that was never started.
func reportDirectivePause(stdout io.Writer, outcome orchestrator.Outcome) {
	held := outcome.PausedByDirective
	fmt.Fprintf(stdout, "%s is paused for an unresolved directive\n", outcome.WorkItemID)
	fmt.Fprint(stdout, held.Render())
	if outcome.RunID != "" {
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintln(stdout, "the item stays claimed and its artifacts are preserved; nothing was cancelled")
	} else {
		fmt.Fprintln(stdout, "nothing was started for it, so there is nothing to clean up")
	}
	fmt.Fprintf(stdout, "`yoyo directive resolve %s` settles it, and running yoyodyne on %s after that carries on\n",
		held.ID, outcome.WorkItemID)
}

// reportDependencyPause describes work held up by unfinished work its item was
// made to wait on. It names that work, because closing it or unlinking it is what
// lifts the pause, and it says which of the two cases this is: a run that was
// under way and is preserved, or work that was never started.
func reportDependencyPause(stdout io.Writer, outcome orchestrator.Outcome) {
	fmt.Fprintf(stdout, "%s waits on unfinished work and is paused\n", outcome.WorkItemID)
	fmt.Fprintf(stdout, "waiting on: %s\n", outcome.PausedByDependency.Summary())
	if outcome.RunID != "" {
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintln(stdout, "the item stays claimed and its artifacts are preserved; nothing was cancelled")
		fmt.Fprintln(stdout, "closing that work, or removing the dependency link, lifts the pause; a watching `yoyo work` session then continues this run at its next pull")
		return
	}
	fmt.Fprintln(stdout, "nothing was started for it, so there is nothing to clean up")
	fmt.Fprintf(stdout, "closing that work, or removing the dependency link, lifts the pause; running yoyodyne on %s after that carries on\n",
		outcome.WorkItemID)
}

// reportTrackerPause describes a run parked because the tracker would not answer
// the read it makes at a gate boundary. It names the read and what the window
// was spent on, because a store that was contended and one that is broken are
// different things to look at, and it says that nothing was judged: a run parked
// here has its change exactly where it left it.
func reportTrackerPause(stdout io.Writer, outcome orchestrator.Outcome) {
	fmt.Fprintf(stdout, "%s is parked: the tracker did not answer a read this run makes before it may take its next step\n", outcome.WorkItemID)
	fmt.Fprintf(stdout, "waiting on: %s\n", outcome.PausedByTracker.Summary())
	fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
	fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
	fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
	fmt.Fprintln(stdout, "nothing about the change was judged; the item stays claimed and its artifacts are preserved")
	fmt.Fprintf(stdout, "the tracker answering lifts the park; running yoyodyne on %s continues the same run from where it stopped\n",
		outcome.WorkItemID)
}

// reportOperatorHold describes work the operator's own pause is holding. It
// leads with the pause rather than with the item, because the operator paused
// everything rather than this: whatever they were asking for here, what they
// need to be told is that the harness is where they left it. It says when the
// hold was placed for the same reason, since a system somebody paused and forgot
// looks exactly like a system that died.
//
// What it will not do is say nothing was started when something was. A run this
// item already has is named, whether it parked at a boundary or is still working
// its way to one — both keep everything they hold, and both carry on when the
// pause lifts. A lookup that failed says so instead of guessing either way: an
// operator told nothing was started for a run that is sitting in a worktree with
// hours of work in it would be misled about the one thing this verb exists to
// make legible.
func reportOperatorHold(stdout, stderr io.Writer, outcome orchestrator.Outcome, err error) {
	fmt.Fprintf(stdout, "PAUSED: all harness activity is paused, since %s\n",
		outcome.PausedByOperator.HeldAt.Format(time.RFC3339))
	switch {
	case outcome.RunID != "":
		fmt.Fprintf(stdout, "%s has a run in flight; it parks at its next provider call\n", outcome.WorkItemID)
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintln(stdout, "the item stays claimed and its artifacts are preserved; nothing was cancelled")
	case err != nil:
		fmt.Fprintf(stdout, "what is in flight for %s could not be read, so nothing here says whether anything was started for it\n", outcome.WorkItemID)
	default:
		fmt.Fprintf(stdout, "nothing is in flight for %s, so nothing was started and there is nothing to clean up\n", outcome.WorkItemID)
	}
	fmt.Fprintf(stdout, "`yoyo resume` lifts the pause, and running yoyodyne on %s after that carries on\n", outcome.WorkItemID)
	if err != nil {
		// The pause itself is not in doubt — it is a flag this command just read —
		// so what failed is named beside it rather than in place of it.
		fmt.Fprintf(stderr, "the pause is active; this could not be fully reported: %v\n", err)
	}
}

// reportIntakeHold names work the harness declined to choose because intake is
// held. Nothing was claimed and nothing developed, so it names no run, no
// branch, and no worktree: sending an operator to look for artifacts nothing
// made is the failure every pause report here avoids.
//
// Who is holding it is read off the record and never assumed to be the operator.
// The harness's own failure-storm brake places the same hold, and an operator
// told they placed one they did not goes looking for a decision of their own
// that was never made.
//
// It always says the two ways out, because they are genuinely different
// decisions: lift the hold and let the harness choose again, or leave it in force
// and name this item yourself, which the hold was never over.
func reportIntakeHold(stdout io.Writer, outcome orchestrator.Outcome) {
	fmt.Fprintf(stdout, "INTAKE HELD since %s: %s\n",
		outcome.PausedByIntake.HeldAt.Format(time.RFC3339), outcome.PausedByIntake.Says())
	fmt.Fprintf(stdout, "the harness starts nothing on its own; nothing was started for %s and nothing was claimed; work already running carries on\n", outcome.WorkItemID)
	fmt.Fprintf(stdout, "`yoyo release` lets the harness choose work again, as does /release in a conversation, and `yoyo run %s` runs this item now regardless\n", outcome.WorkItemID)
}

// reportCollectedReports names what this run's agents reported without it
// stopping their work. The reports themselves are read from the pile, either
// from a command line or from the conversation the operator may already be in;
// what this owes them is to say there is something new to read, and where.
//
// It says how many of what rather than only how many, and is marked and dressed
// by the worst of them. This is the last line of a run that has just printed a
// screen of branches, worktrees and prices, and an operator who is only going to
// read one of those lines has to be told from it whether anything in that pile
// is already costing them.
func reportCollectedReports(writer io.Writer, theme console.Theme, outcome orchestrator.Outcome) {
	if len(outcome.Reports) > 0 {
		worst := report.Worst(outcome.Reports)
		fmt.Fprint(writer, theme.Severity(console.Severity(worst), fmt.Sprintf(
			"%sreported %d thing(s) without stopping the run (%s); `yoyo reports` shows them, as does /reports in `yoyo chat`\n",
			worst.Prefix(), len(outcome.Reports), report.Tally(outcome.Reports))))
	}
	if outcome.ReportProblem != "" {
		fmt.Fprintln(writer, outcome.ReportProblem)
	}
}

// reportProposedAmendments names what this run's agents proposed changing in a
// document they do not own. Nothing was written to any document, and nothing
// will be until somebody decides, so what this owes the operator is to say a
// decision is waiting and where to make it.
func reportProposedAmendments(writer io.Writer, outcome orchestrator.Outcome) {
	for _, proposal := range outcome.Amendments {
		fmt.Fprintf(writer, "proposed a change to %s for the %s to decide; nothing was written to it (%s)\n",
			proposal.Artifact, proposal.Owner, proposal.ID)
	}
	if len(outcome.Amendments) > 0 {
		fmt.Fprintln(writer, "`yoyo amendment list` shows what is waiting, and approve or decline decides one")
	}
	if outcome.AmendmentProblem != "" {
		fmt.Fprintln(writer, outcome.AmendmentProblem)
	}
}

// reportItemCost names what the work item has cost across every run made for
// it, which is what the run just added to. A price that could not be recorded is
// named too: the spending happened either way, and an operator reading a ledger
// has to know where it stopped being written down.
func reportItemCost(stdout, stderr io.Writer, outcome orchestrator.Outcome) {
	if outcome.Cost != nil {
		total := fmt.Sprintf("$%.2f", outcome.Cost.TotalUSD)
		if !outcome.Cost.Complete() {
			// A run nothing survives to price is left out of the total rather than
			// added as a zero, so the number is the least the item can have cost.
			total = fmt.Sprintf("at least $%.2f (%d run(s) left no record to price)", outcome.Cost.TotalUSD, outcome.Cost.UnknownRuns)
		}
		fmt.Fprintf(stdout, "%s has cost %s across %d run(s)\n", outcome.WorkItemID, total, outcome.Cost.Runs)
	}
	if outcome.CostProblem != "" {
		fmt.Fprintf(stderr, "the price of %s was not recorded: %s\n", outcome.WorkItemID, outcome.CostProblem)
	}
}

// reportPullRequest names the published work. A run that asked to publish and
// did not is reported too, because "no pull request appeared" is otherwise
// indistinguishable from a harness that quietly stopped publishing.
func reportPullRequest(writer io.Writer, outcome orchestrator.Outcome) {
	if outcome.PullRequest != nil {
		state := "open"
		switch {
		case outcome.PullRequest.Merged:
			state = "merged"
		// A queued merge is not the same fact as an open request nobody has acted
		// on: the forge has accepted the merge and performs it once the base
		// branch's requirements are met, after this run is over.
		case outcome.PullRequest.MergeQueued:
			state = "merge queued"
		}
		fmt.Fprintf(writer, "pull request #%d (%s): %s\n", outcome.PullRequest.Number, state, outcome.PullRequest.URL)
		if outcome.PullRequest.MergeQueued {
			fmt.Fprintln(writer, "the forge merges it once the required checks pass; `yoyo reconcile` settles the run when it does")
			fmt.Fprintln(writer, "the work item stays open until that merge is confirmed, and is handed back with a blocker if the forge drops it")
		}
	}
	if outcome.PublishSkipped != "" {
		fmt.Fprintf(writer, "publishing skipped: %s\n", outcome.PublishSkipped)
	}
}

// nonEmptyValue falls back to a stated placeholder rather than printing a blank
// where a name belongs: the provider does not always name the limit it refused
// on, and "the  usage limit" reads as a bug.
func nonEmptyValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func printRunUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo run [options] <beads-id>

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
