// Package config loads Yoyodyne's effective configuration from a project's
// .yoyodyne directory. A project owns its configuration outright: `yoyo init`
// generates a complete file from the versioned bundle shipped inside the
// executable and copies that bundle's personas into the project, so what runs
// is what the project can read. A project may still inherit the bundle by name
// with "extends" and overlay only what it changes, which trades the legibility
// of an explicit file for defaults that improve when the executable does.
// Either shape is usable from a repository with no access to the Yoyodyne
// source checkout.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

const CurrentVersion = 1

// DefaultSpecifications is where the product manager reads product intent from
// when nothing names another directory. It is the layout the design recommends
// for human-readable product artifacts, so a project that followed that layout
// needs no setting at all.
const DefaultSpecifications = "docs/product"

// DefaultInvariants is where the architect's durable invariants live when
// nothing names another directory. It is the recommended layout for the same
// reason the specifications default is: a project that followed it writes
// nothing down, and a project with no such directory simply has no invariants
// yet rather than a broken configuration.
const DefaultInvariants = "docs/decisions/invariants"

// DefaultDesigns and DefaultDecisions are the other two homes canonical
// artifacts live in when nothing names another directory. Together with the
// specifications directory they are what the harness reads artifact identity
// and metadata from, and they follow the recommended layout for the same reason
// the invariants default does: a project that followed it writes nothing down,
// and a project with no such directory simply records no artifacts of that kind
// yet. The decisions home contains the invariants one by default, and the
// invariants keep their own identity scheme rather than being read twice.
const (
	DefaultDesigns   = "docs/designs"
	DefaultDecisions = "docs/decisions"
)

type Config struct {
	Version int `yaml:"version" json:"version"`
	// Extends names the built-in bundle this configuration inherits from, and
	// is empty for a complete standalone configuration.
	Extends   string    `yaml:"extends,omitempty" json:"extends,omitempty"`
	Product   Product   `yaml:"product" json:"product"`
	Execution Execution `yaml:"execution" json:"execution"`
	Triage    Triage    `yaml:"triage" json:"triage"`
	Exchange  Exchange  `yaml:"exchange" json:"exchange"`
	// Conversation is what a management conversation's picture of the
	// repository is held to. It is always present, at the harness default where
	// a project writes nothing, because the measurement it times is not
	// something a project opts into.
	Conversation Conversation `yaml:"conversation" json:"conversation"`
	Research     Research     `yaml:"research,omitempty" json:"research,omitempty"`
	Approvals    Approvals    `yaml:"approvals" json:"approvals"`
	Checks       []string     `yaml:"checks" json:"checks"`
	// LandingChecks are the commands run once per landing on the target branch,
	// over the integrated commit, after a run has integrated and closed its item.
	// They are the other half of a per-run gate narrowed to what a change
	// touches: the suite the gate no longer runs whole is run whole here, once
	// per landing rather than once per attempt, and a failure is reported as a
	// red landing that files its own work item and blocks nothing. A project
	// that names none runs nothing after a landing, which is what every project
	// did before this existed.
	LandingChecks []string `yaml:"landing_checks,omitempty" json:"landing_checks,omitempty"`
	// PathChecks are commands the per-run gate adds after Checks for a change
	// that touches what they vouch for, and leaves out for one that touches none
	// of it. Each names a file in the repository listing the paths it vouches
	// for, so the list is kept beside the thing it describes rather than here;
	// a change that edits that list runs the check whatever the list now says.
	// A project that names none adds nothing, which is what every project did
	// before this existed.
	PathChecks []PathCheck            `yaml:"path_checks,omitempty" json:"path_checks,omitempty"`
	Agents     map[string]AgentConfig `yaml:"agents" json:"agents"`
	// Accounts are the provider accounts this project runs agents under, keyed by
	// the alias each one is named by. It is top level rather than under `agents`
	// because an account is a thing several agents share: which roles run on which
	// account is stated on the agents, and what accounts exist is stated once
	// here. A project that names none runs under the default alias, which is what
	// makes a single-account project write nothing and still record what it ran
	// under.
	Accounts map[string]Account `yaml:"accounts,omitempty" json:"accounts,omitempty"`
	// Operators are the humans the project recognizes, keyed by a short name for
	// each one. It is top level rather than under any one surface because a human
	// is known by several, and the authority is the human's: an act is authorized
	// by resolving whichever namespace it arrived through to a person. It is
	// absent from a project that has named nobody, which recognizes nobody rather
	// than everybody.
	Operators map[string]Operator `yaml:"operators,omitempty" json:"operators,omitempty"`
	// Providers are the provider plugins this project declares, keyed by the
	// backend identifier an agent names to run on one. It is absent from a
	// project that runs on the backends this build ships, which is every project
	// until one reaches a harness or an API yoyo has never heard of.
	//
	// A declared provider describes and decides nothing: it says which roles it
	// serves, which tool postures it can hold them to, what it can do, and how to
	// read what it says about rate limits, retries, and reset times. Whether to
	// wait, how long, and against which budget stay the harness's, because those
	// are what the usage_limit settings and a run's safety properties rest on.
	// See docs/provider-plugins.md.
	Providers map[string]backend.ProviderPlugin `yaml:"providers,omitempty" json:"providers,omitempty"`
	// Codex is the skills and instruction files a Codex invocation is given beside
	// its prompt. It is absent from a project that names none, and then a Codex
	// role is given none: the skills, plugins, and instruction files in the
	// account's own provider home are kept out either way. See
	// internal/backend/codex/context.go and docs/provider-plugins.md.
	Codex backend.NamedContext `yaml:"codex,omitempty" json:"codex,omitempty"`
	// ClaudeCode is the same for a Claude Code invocation. A Claude Code role is
	// given none of the account's own settings, memory, skills, plugins,
	// connectors, or instruction files either way. See
	// internal/backend/claudecode/context.go and docs/provider-plugins.md.
	ClaudeCode backend.NamedContext `yaml:"claude_code,omitempty" json:"claude_code,omitempty"`
	// Slack configures the reporting sink. It is absent from a project that does
	// not report to a workspace, which is every project until one opts in.
	Slack Slack `yaml:"slack,omitempty" json:"slack,omitempty"`
	// Services are the parts of the product — the Slack sink, the dashboard,
	// the scheduler, and the maintenance pass — and whether each runs. It is
	// always present, at harness defaults where a project writes nothing,
	// because the set of parts is the product's shape rather than something a
	// project opts into; what a project decides is which of them are on. See
	// services.go for what the section declares and what deliberately reads it.
	Services Services `yaml:"services" json:"services"`
	// RecurringTasks are the things the harness does on a cadence rather than
	// because something happened, keyed by the name each one is reported and
	// recorded under. It is absent from a project that has scheduled nothing,
	// which is every project until one opts in. See recurring.go for what
	// configuration decides here and what it deliberately cannot.
	RecurringTasks map[string]RecurringTask `yaml:"recurring_tasks,omitempty" json:"recurring_tasks,omitempty"`
}

// ProviderRegistry is every provider this project may name: the backends this
// build ships, plus whatever the project declared for itself. It reports
// everything wrong with the declarations at once and nothing at all when there
// are none, which is the state of every project that runs on a built-in.
func (c Config) ProviderRegistry() (*backend.Registry, error) {
	plugins := make(map[domain.Backend]backend.ProviderPlugin, len(c.Providers))
	for name, plugin := range c.Providers {
		plugins[domain.Backend(name)] = plugin
	}
	return backend.NewRegistry(plugins)
}

// Slack is what a project says about reporting into a chat workspace. What it
// deliberately does not hold is a credential: the sink's two tokens live in the
// sink process's environment and nowhere else, so nothing that is read into a
// prompt, a context bundle, or a run's environment can carry one. What is here
// is identity and addressing, which is configuration in the ordinary sense —
// checked in, reviewed with the code, and readable by anybody who can read the
// repository.
//
// What it deliberately no longer holds either is the allow-list of who may steer
// the harness from the workspace. Who counts as an operator is a fact about
// humans rather than about Slack, so it is stated once in the top-level
// operators mapping and read back through Config.SlackOperators: the humans
// granted direct-work who have bound a member id. An allow-list authored beside
// those grants is one that disagrees with them — silently, and about authority.
type Slack struct {
	// Enabled is the switch. A project that has not set it reports nothing, and
	// the sink refuses to start rather than posting into a workspace nobody
	// configured.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Channel is where the threads are opened, as a channel id (the stable
	// thing, which a rename does not break) or a #name. A project that enables
	// Slack without one is refused at load, before any work is claimed, because
	// the alternative is a sink that starts, reads a stream, and then discovers
	// it has nowhere to post.
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
	// Avatars overrides the picture beside a speaker's name, keyed by role —
	// `developer`, `reviewer`, and the rest — or by `harness` for what no persona
	// did. A value is an emoji shortcode, including one this workspace added
	// itself, or the https URL of an image. A speaker with no entry keeps the avatar
	// the harness ships, so a project that names one names one.
	//
	// Only the picture is here. The name a message appears under, and whose
	// account it is, are deliberately not configurable: who speaks is a claim
	// about who did the work, and a project that could rewrite it could attribute
	// a promotion to a developer. The avatar carries none of that — everything it
	// distinguishes is already distinguished by the name beside it — which is what
	// makes it the part that can be a preference.
	Avatars map[string]string `yaml:"avatars,omitempty" json:"avatars,omitempty"`
}

type Product struct {
	ID           domain.ProductID    `yaml:"id" json:"id"`
	RepositoryID domain.RepositoryID `yaml:"repository_id,omitempty" json:"repository_id,omitempty"`
	Repository   string              `yaml:"repository" json:"repository"`
	// Specifications is the directory the product manager reads product intent
	// from, relative to the repository root. It is the whole of what that role
	// is shown about the product, so it is confined to the repository: a path
	// that escapes it would put arbitrary text in front of the role that decides
	// what the product is for.
	Specifications string `yaml:"specifications" json:"specifications"`
	// Invariants is the directory of durable architectural invariants the
	// architect owns, relative to the repository root. The harness reads it to
	// deliver the ones relevant to a work item into the developer's context and
	// the reviewer's evidence, so it is confined to the repository for the same
	// reason the specifications are: a path that escaped it would put arbitrary
	// text in front of every developer as a constraint on their change.
	Invariants string `yaml:"invariants" json:"invariants"`
	// Designs and Decisions are the architect's two artifact homes, relative to
	// the repository root: the designs and specifications that serve the goals,
	// and the decision records the invariants are extracted from. With
	// Specifications they are the directories the harness reads canonical
	// artifact identity and metadata from, so they are confined to the
	// repository for the same reason the others are.
	Designs   string `yaml:"designs" json:"designs"`
	Decisions string `yaml:"decisions" json:"decisions"`
	// ShippedDocumentation is the operator-facing documentation this project
	// ships, as repository-relative file paths. It is what the product manager
	// is given as a description of what the product ships today, labeled as
	// description and never as authority about intent.
	//
	// It is a named list rather than a directory for the reason the harness's own
	// set is: a walk would sweep in the design document and the decision records,
	// which say how the product is built and are what made description reachable
	// as intent in the first place. Each entry is confined to the repository like
	// every other product path, and a path naming nothing is simply not carried.
	//
	// A project that names none is shown none, and told so. The harness ships a
	// set of its own for the repository it describes — see
	// contextbundle.HarnessShippedDocumentation — and that set is deliberately
	// not a default for anybody else: it is eight generic paths, and feeding an
	// adopting project's unrelated docs/work.md to its product manager labeled
	// "what the product ships" is exactly the mistake a default would make.
	ShippedDocumentation []string `yaml:"shipped_documentation,omitempty" json:"shipped_documentation,omitempty"`
}

type Execution struct {
	MaxConcurrentDevelopers    int `yaml:"max_concurrent_developers" json:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan int `yaml:"repair_attempts_before_replan" json:"repair_attempts_before_replan"`
	// IntegrationRetriesBeforeReconciliation bounds the replays of a run whose
	// promotion lost a race — to another run, or to whoever moved the target
	// branch mid-run — that stopped on the change: the replay conflicted, or the
	// replayed change failed its checks or drew a repair verdict. Each replay
	// re-runs the deterministic checks and obtains a fresh independent review,
	// because the reviewed change is not the change that would now be promoted,
	// and a replay that passes both is charged nothing, so a run whose replays
	// keep passing keeps replaying until it lands. The replay that takes the
	// count past the bound stops the run there, on the change; zero means no
	// replay may stop on the change. A lost race itself never stops a run.
	IntegrationRetriesBeforeReconciliation int `yaml:"integration_retries_before_reconciliation" json:"integration_retries_before_reconciliation"`
	// TransientRelaunchesBeforeBlocking bounds how many times a run reissues a
	// provider invocation that died without judging the work — an API error the
	// provider's own retries did not outlast, or a response cut off mid-flight.
	// The relaunch continues the same worktree and the same session, so nothing
	// the dead attempt built is redone. One budget covers the developer and the
	// reviewer, because what it bounds is how much of the provider's weather one
	// run absorbs. Zero never relaunches, which is the behavior a run had before
	// this bound existed: the first transient death ends it.
	TransientRelaunchesBeforeBlocking int    `yaml:"transient_relaunches_before_blocking" json:"transient_relaunches_before_blocking"`
	WorktreeRoot                      string `yaml:"worktree_root" json:"worktree_root"`
	// Remote names the Git remote publishing opens pull requests against, and —
	// unless PushRemote names another — pushes run branches to. It is only
	// consulted when `approvals.publishing` is automatic, and a repository that
	// has no remote by this name publishes nothing rather than failing.
	Remote string `yaml:"remote" json:"remote"`
	// PushRemote names the Git remote run branches are pushed to when that is not
	// the repository the work is published into: a contributor's fork. Setting it
	// is what makes the pull requests cross-repository — the branch goes to the
	// fork and the request is opened against Remote — which is how somebody
	// without push access to a project publishes to it at all. Empty means run
	// branches go to Remote, which is every project that can push to what it
	// publishes into. A repository that has no remote by this name publishes
	// nothing rather than failing, exactly as an absent Remote does.
	PushRemote string `yaml:"push_remote,omitempty" json:"push_remote,omitempty"`
	// UsageLimitMaxPause bounds how long a run may wait for an exhausted
	// provider usage limit to reset. A reset further away than this is treated
	// as no usable reset time at all: the run stops and records a blocker
	// instead of sleeping on it. Zero disables waiting entirely, so every
	// exhausted limit blocks immediately.
	UsageLimitMaxPause Duration `yaml:"usage_limit_max_pause" json:"usage_limit_max_pause"`
	// UsageLimitInProcessPause is how much of that bound a run will spend
	// sleeping inside this process. The process sleeps probes until it has spent
	// this much on one run and then exits with the run still in flight and its
	// deadline recorded, so a later invocation resumes it rather than holding a
	// process open for hours. It is measured against every probe this process has
	// already slept, across phases, rather than against each probe separately: a
	// bound applied per probe would not bound how long the process stays open,
	// because a probe interval that fits under it fits however many times it is
	// taken. It is never larger than UsageLimitMaxPause in effect, because a
	// pause beyond that bound is refused before either path is chosen.
	UsageLimitInProcessPause Duration `yaml:"usage_limit_in_process_pause" json:"usage_limit_in_process_pause"`
	// UsageLimitUnknownResetPause is the interval between probes: how long a run
	// sleeps before reissuing the attempt to find out whether the provider will
	// serve it now. It applies whether or not a reset time was named, which is
	// the whole of the polling discipline. A limit reported without one is not
	// the same as having no capacity — the monthly overage allowance reports this
	// way while the ordinary rolling window keeps resetting on its usual schedule
	// — so it is waitable and simply carries no deadline. A limit reported with
	// one carries a deadline that bounds the wait rather than gating it, because
	// a reset time is a claim about the provider and claims go stale in both
	// directions. Either way a run sleeps this interval or the time left to the
	// deadline, whichever is shorter, and asks again. Every probe spends
	// UsageLimitMaxPause, so a provider that keeps refusing walks into that bound
	// rather than polling forever.
	UsageLimitUnknownResetPause Duration `yaml:"usage_limit_unknown_reset_pause" json:"usage_limit_unknown_reset_pause"`
	// CheckTimeout is the total budget one configured check gets: the whole time
	// it may run, not the time it may stay quiet. It scales with the work rather
	// than with the machine, so it is configured rather than fixed — a suite
	// grows, and N runs at once multiply its wall clock without multiplying the
	// cores it runs on, so the budget has to be raised or the runs serialized.
	// A check killed at this bound is not a check that failed: it is work that
	// may have been passing the whole time, which is why every check reports
	// what it spent against this budget rather than only the one that ran out.
	CheckTimeout Duration `yaml:"check_timeout" json:"check_timeout"`
	// CheckStageTimeout is the total budget the whole check stage of one run
	// gets: every configured check together, from the first one starting to the
	// last one ending. CheckTimeout above bounds one check and says nothing about
	// the list, so four checks each inside their budget could hold a run — and
	// the seat it occupies — for hours; on 2026-09-19 one did, for over two, with
	// the race suite alone past ninety minutes under the load the concurrent
	// suites were themselves creating. A stage that reaches this bound ends as a
	// stoppage naming the bound and the check it stopped, and the bound is on the
	// run's record while the stage runs, so `yoyo status` says how much of it has
	// been spent rather than only how long the run has been going.
	CheckStageTimeout Duration `yaml:"check_stage_timeout" json:"check_stage_timeout"`
	// LandingCheckTimeout is the budget each landing check gets. It is its own
	// budget rather than the per-run gate's, and the landing has no stage bound,
	// because what is moved to the landing is exactly the suite too long for the
	// gate — bounding it the same way would stop it on every landing. A landing
	// check stopped at this budget makes the landing unverified rather than
	// red, since a stopped check judged nothing.
	LandingCheckTimeout Duration `yaml:"landing_check_timeout" json:"landing_check_timeout"`
	// ServerOverloadPause is how long a run waits before reissuing an attempt the
	// provider refused because its own servers were transiently overloaded. It is
	// the same polling discipline as an exhausted limit with a different clock:
	// an overload quotes no reset time and lifts in seconds rather than hours, so
	// waiting the usage-limit probe interval would park a run for half an hour on
	// a condition that had already passed. It spends UsageLimitMaxPause like every
	// other wait, so a provider that stays overloaded walks into that bound rather
	// than reissuing forever.
	ServerOverloadPause Duration `yaml:"server_overload_pause" json:"server_overload_pause"`
	// WorkPoll is how long a watch session waits before reading the queue again
	// when it found nothing to start. It is the whole cost of an idle watch: one
	// local tracker read per interval, no provider anywhere near it. Nothing is
	// cached between polls, so it is also the whole of the latency on a change —
	// work admitted, reprioritized, or unblocked is picked up at the next poll
	// rather than by anything detecting it.
	WorkPoll Duration `yaml:"work_poll" json:"work_poll"`
	// RedeployDrainLimit bounds how long a watch session that has found a build
	// deployed over it waits out the runs it hosts before it restarts anyway.
	// Past it the hosted runs are stopped and preserved — worktree, branch,
	// claim, developer session, and every counter — for the session that comes
	// back to re-adopt, so a deploy over a session hosting a long check suite
	// costs minutes rather than the length of the suite. A draining session
	// keeps polling, pulling into free seats, and firing its recurring tasks
	// right up to the restart; only the runs it hosts are what the drain is
	// about.
	RedeployDrainLimit Duration `yaml:"redeploy_drain_limit" json:"redeploy_drain_limit"`
	// FactoryStallAfter is how long the product may go with no work pulled and
	// no recurring pass succeeding before the harness says the factory has
	// stalled: a critical report filed once when it begins, a note when passes
	// succeed or work is pulled again, and an entry on the attention line while
	// it stands. It is read by the supervisor rather than by any role's pass,
	// because the roles that would notice are the ones failing; on 2026-09-29
	// every pass failed for twelve hours and the one trace was a log nobody read.
	FactoryStallAfter Duration `yaml:"factory_stall_after" json:"factory_stall_after"`
	// MissingReportsBeforeFreshConversation bounds consecutive recurring passes
	// that omit their closing report even after one request for it. Zero uses
	// the default of three; the next pass starts a new role conversation.
	MissingReportsBeforeFreshConversation int `yaml:"missing_reports_before_fresh_conversation" json:"missing_reports_before_fresh_conversation"`
	// BlockedRunsBeforeIntakeHold is the failure-storm brake: this many runs
	// blocking in a row, with nothing landing between them, holds intake and
	// summons the development manager to decide what happens to it. It is a
	// bound on systemic breakage rather than on any one item — an item that
	// keeps failing is the per-item cooldown's business — and it exists because
	// a watch session left overnight against a broken machine would otherwise
	// put the whole queue through a run each and dock every one of them. An
	// environmental stop — a dirty checkout, a transport that did not answer, a
	// provider nobody could reach — counts toward nothing, because no run can
	// fix one. Zero never brakes, which is the behaviour a pass had before this
	// bound existed.
	BlockedRunsBeforeIntakeHold int `yaml:"blocked_runs_before_intake_hold" json:"blocked_runs_before_intake_hold"`
	// BrakeCooldown is how long a tripped brake waits for the development
	// manager's decision before it probes the line by itself: one run started
	// under the hold, whose landing reopens intake and whose blocking keeps it
	// held and puts the question to her again. It is what makes the brake
	// self-releasing — a hold nobody decides about is probed rather than left —
	// and a probe that blocks restarts it, so a broken machine is probed once
	// per cooldown rather than continuously. It bounds a wait on a turn, not on
	// a person: she is summoned the moment the brake trips, and a decision she
	// records ends the wait wherever the cooldown stands. Zero waits for her
	// summoned turn and no longer.
	BrakeCooldown Duration `yaml:"brake_cooldown" json:"brake_cooldown"`
	// BrakeEscalationCycles bounds the loop the cooldown makes. A probe that
	// blocks summons her again and restarts the cooldown, so on a machine that
	// stays broken the brake goes round — one of her turns and one probe run
	// per cooldown — for as long as nobody happens to look, and nothing about
	// it gets louder unless she escalates. After this many such cycles with no
	// escalation of hers, the harness escalates the hold to the operator itself:
	// one direct message naming the cycles spent and what stopped the last
	// probe, and no further probe until somebody releases it. It is a count of
	// cycles rather than a length of time because the loop is what it bounds,
	// and what a count of cycles comes to in hours is the cooldown times it.
	// Zero never escalates on its own, which is the loop as it stood before the
	// bound existed.
	BrakeEscalationCycles int `yaml:"brake_escalation_cycles" json:"brake_escalation_cycles"`
	// DeveloperSlots is what each developer slot prefers, one entry per slot in
	// slot order, and empty for a project whose every slot pulls in the product
	// manager's order. A slot is one unit of MaxConcurrentDevelopers, so the list
	// may be shorter than that — the slots it does not name prefer nothing — and
	// never longer, because a preference for a slot the capacity does not have is
	// a preference nothing will ever act on. A slot that prefers a label pulls the
	// backlog's ready work carrying it first, and the rest of the backlog only
	// when none of its label's work is ready; a slot with no preference passes
	// labelled work over while a slot preferring its label is free to take it.
	//
	// It selects what a slot pulls first and nothing else. A slot with a
	// preference runs what any slot runs, under the same contract, checks, and
	// reviewer, so there is nothing here a persona or a role could be widened by.
	DeveloperSlots []domain.DeveloperSlot `yaml:"developer_slots,omitempty" json:"developer_slots,omitempty"`
	// DeveloperModels is the model a run takes for the work it is over, keyed by
	// the item's labels and read in this list's own order: the first entry any of
	// an item's labels matches is the model that item's developer invocations ask
	// for, and an item carrying no label named here takes the developer's
	// configured model. Empty is every run on the developer's own model, which is
	// what every project did before the mapping existed.
	//
	// It selects a model and never anything else. A run on a mapped model is
	// developed, checked, and reviewed exactly as any run is, and the reviewer's
	// model is not nameable from here at all — its posture is a safety property
	// rather than a spend decision, which is why the mapping has no key for it.
	DeveloperModels []DeveloperModelRule `yaml:"developer_models,omitempty" json:"developer_models,omitempty"`
	// DeclarativeDelivery is how a new run is executed, and it defaults on: each
	// run compiles the built-in delivery definition, records a workflow instance
	// of it, and steps that instance beside the run, so the sequence the
	// definition chooses is recorded against the sequence the run actually took.
	// It changes nothing about how work is delivered — the pipeline performs
	// every step exactly as it always has, and the definition's doors perform
	// nothing — so what the default buys is the record: every run says where the
	// definition sent it, and any disagreement is on the run.
	//
	// Setting it to `false` is the rollback to the legacy path, which is the
	// same delivery with nothing observing it. It is one key and it is the whole
	// of the rollback; docs/configuration.md says so where the default is
	// described.
	//
	// It is read once per run, when the run is created. A run already in flight
	// keeps whatever it started under, so a rollback never strands a run that is
	// mid-flight and undoing one never migrates a legacy run into the
	// definition.
	//
	// It carries no `omitempty`: a project that rolled back would otherwise
	// serialize exactly like one running the default, so both `config show
	// --effective` and the configuration revision would be unable to tell the
	// two apart.
	DeclarativeDelivery bool `yaml:"declarative_delivery" json:"declarative_delivery"`
}

const (
	// defaultRemote is the remote publishing uses when nothing names another. A
	// repository with no remote by this name is not an error: it simply publishes
	// nothing and behaves exactly as a local-only project does.
	defaultRemote = "origin"
	// defaultUsageLimitMaxPause covers the provider's five-hour limit with slack
	// and stops short of its seven-day one. A run that would have to sleep for
	// days is not a run that should be sleeping: it stops and records a blocker,
	// so the capacity problem reaches a person instead of a timer.
	defaultUsageLimitMaxPause = Duration(6 * time.Hour)
	// defaultUsageLimitInProcessPause equals the maximum pause, so by default
	// every wait the harness will take at all is taken in this process and the
	// run continues on its own — which is what waiting out a usage limit is for.
	// Lowering it trades that for a run that exits with its deadline recorded
	// and is resumed by a later invocation.
	defaultUsageLimitInProcessPause = defaultUsageLimitMaxPause
	// defaultUsageLimitUnknownResetPause is how long to wait between probes,
	// whether or not a reset time was named. Short enough to resume soon after a
	// window rolls, long enough not to hammer a provider that is refusing — and
	// deliberately checked at least this often even under a multi-hour quoted
	// reset, because a quoted reset can be overtaken by a window that rolls or by
	// capacity somebody bought.
	defaultUsageLimitUnknownResetPause = Duration(30 * time.Minute)
	// defaultCheckTimeout has room for a suite several times the size of the one
	// that provoked it. The flat ten minutes it replaces killed a run whose
	// tests were passing package by package under the contention of two
	// concurrent runs, so the default is set against the contended case rather
	// than the single-run one: this repository's own suite takes well under two
	// minutes with two of them running at once, and a project that outgrows
	// thirty minutes raises this rather than meeting it as a failed run.
	defaultCheckTimeout = Duration(30 * time.Minute)
	// defaultCheckStageTimeout is stated in minutes on purpose, because the bound
	// it replaces was the sum of the per-check budgets, which for a four-check
	// list is two hours — and a check stage that can take hours is what held a
	// watch session's drain for two of them and starved the seat beside it. It
	// equals the per-check default rather than exceeding it: with the race suite
	// narrowed to the packages a change touches, this repository's whole stage
	// fits it with two runs contending, and a project whose stage does not is
	// told which check the bound stopped and what moves it.
	defaultCheckStageTimeout = Duration(30 * time.Minute)
	// defaultLandingCheckTimeout is what the whole race suite took under load
	// on 2026-09-19 with room to spare: a landing runs once per landing rather
	// than once per attempt, and holds no seat, claim, or place in the queue
	// while it runs — only the process that landed — so it can be given what
	// the suite actually takes rather than what a developer seat can spare.
	defaultLandingCheckTimeout = Duration(2 * time.Hour)
	// defaultServerOverloadPause is long enough to be worth waiting — the
	// provider CLI has already spent its own ten retries on the condition before
	// the harness ever sees it — and short enough that a run resumes within a
	// minute or two of the overload lifting, which is the timescale the provider's
	// own message describes.
	defaultServerOverloadPause = Duration(90 * time.Second)
	// defaultWorkPoll is a minute, which is the granularity a person steering a
	// backlog actually works at: work admitted or reordered is picked up within
	// a minute, and an idle session costs one tracker read a minute to be that
	// responsive. Shorter buys latency nobody is waiting on; much longer makes
	// reordering the queue feel like it did nothing.
	defaultWorkPoll = Duration(60 * time.Second)
	// defaultRedeployDrainLimit is measured in minutes, deliberately. On
	// 2026-09-19 a session found a build deployed over it at 07:35Z and then did
	// nothing for two hours, waiting out one hosted run whose race suite alone
	// took over ninety minutes under load; two recurring passes were missed and
	// the second seat stayed empty the whole time. Fifteen minutes is long enough
	// for a run at its checks or its review to finish on an ordinary machine, and
	// short enough that a deploy is taken up within the hour it landed in. A run
	// the bound cuts off loses nothing but the minutes its current phase had
	// spent: it is re-adopted from durable state by the session that comes back.
	defaultRedeployDrainLimit = Duration(15 * time.Minute)
	// defaultFactoryStallAfter is two hours: twice the development manager's
	// hourly sweep, so one failed pass is not a stall and two in a row with
	// nothing pulled between them is.
	defaultFactoryStallAfter = Duration(2 * time.Hour)
	// defaultBlockedRunsBeforeIntakeHold is three, which is the same shape of
	// bound as the repair and relaunch budgets: enough that one bad item and the
	// unlucky item after it do not stop the line, and short of a session that
	// spends the whole backlog finding out the machine is broken.
	defaultBlockedRunsBeforeIntakeHold = 3
	// defaultBrakeCooldown is thirty minutes: long enough for the development
	// manager's summoned turn to be taken and answered several times over — a
	// turn is minutes — and short enough that a summons the provider refused, or
	// a conversation nothing could open, costs the line half an hour rather than
	// the two hours the 2026-09-19 trip cost it.
	defaultBrakeCooldown = Duration(30 * time.Minute)
	// defaultBrakeEscalationCycles is four, which at the default cooldown is two
	// hours: the same bar the stall alarm and the heartbeat raise a stopped line
	// to critical at, and the length of the 2026-09-19 hold that nobody knew
	// about. Four cycles is four of her turns and four probe runs spent finding
	// out the same thing, which is enough evidence that the machine is broken
	// and that she is not going to say so.
	defaultBrakeEscalationCycles = 4
)

// Triage is what the triage workflow measures against: when work that has
// stopped moving is docketed to be looked at, and what looking at it may spend.
// The numbers are configuration rather than constants in the workflow because
// each one is a judgement about a project's pace — how long a merge may take
// before nobody merging it is news, how many times one change may go round with
// a reviewer before going round again is the problem — and a project sets that
// differently from the next one.
type Triage struct {
	// StuckMergeAge is how long an approved publication may sit unmerged before
	// it is docketed. It is an age rather than a deadline because what makes a
	// publication stuck is that nothing has happened to it, and nothing
	// happening offers no event to hang a deadline on. Zero is refused rather
	// than read as a choice: a threshold of no time at all dockets every
	// publication the instant it is made, which is a docket of everything and a
	// triage of nothing.
	StuckMergeAge Duration `yaml:"stuck_merge_age" json:"stuck_merge_age"`
	// ReviewRoundsCap bounds the review rounds one work item may accumulate in
	// total — across repairs, across runs — past which triage may no longer hand
	// it back for another repair. Past the cap triage still has three things it
	// may do: escalate the item, re-scope it, or cross the cap — the development
	// manager's own crossing, one step and five times per item, or the operator's
	// override. What it may not do is buy the same argument another round without
	// one of those. Zero is a deliberate choice rather than an error, which is why
	// it is accepted: it says an item that reaches triage is never repaired again
	// unless a crossing says so.
	ReviewRoundsCap int `yaml:"review_rounds_cap" json:"review_rounds_cap"`
	// RepairGrantAttempts is how many repair attempts triage hands an item when
	// it decides the work is worth another go. A project that states nothing
	// gets its configured execution.repair_attempts_before_replan, because a
	// grant is the same kind of budget a run starts with and a project that
	// tuned one has said what it thinks that budget is worth. It is never zero:
	// a grant of nothing changes nothing about the item it was granted to, so it
	// is refused where it is stated and floored at one where it is derived from
	// a repair budget of zero.
	RepairGrantAttempts int `yaml:"repair_grant_attempts" json:"repair_grant_attempts"`
}

const (
	// defaultStuckMergeAge is long enough that an ordinary merge lands well
	// inside it — including one waiting on a person who stepped away from the
	// keyboard — and short enough that a publication nobody is going to merge is
	// looked at within the working session that produced it.
	defaultStuckMergeAge = Duration(2 * time.Hour)
	// defaultReviewRoundsCap is two rounds past the repair budget a run starts
	// with, so an item reaches the cap only after triage has already bought it
	// more rounds than the run itself would have taken. A change still arguing
	// with its reviewer that far in is not a change another round fixes.
	defaultReviewRoundsCap = 4
	// minimumRepairGrant is the floor under a derived grant. A project that
	// configured no routine repair attempts at all still gets a grant of one,
	// because the grant is triage's deliberate exception to that budget rather
	// than another helping of it, and a grant of nothing is not an exception.
	minimumRepairGrant = 1
)

// Exchange is what the inter-role ask channel is bounded by. Two roles talking
// to each other is the one thing here with no natural end: each of them is a
// judgement model, each can always find something further worth saying, and
// neither is the operator. So an exchange is opened with a hard limit on rounds,
// and the limit is a project's judgement about how long a question between two
// of its roles is worth going on for.
type Exchange struct {
	// MaxRounds is the most rounds one exchange thread may take. Reaching it
	// closes the exchange as unresolved and escalates it to the operator, which
	// is what turns the pathological case — two roles deferring to each other for
	// ever — into a rare, legible question somebody can answer. It is copied onto
	// each exchange as it opens, so changing it never lengthens a thread already
	// running long.
	//
	// Zero is not a choice here, unlike the triage caps: an exchange allowed no
	// round at all is a channel that is off, and turning the channel off is
	// leaving the block out of a persona rather than configuring a limit nothing
	// can be spent against. It is refused, and one is the floor.
	MaxRounds int `yaml:"max_rounds" json:"max_rounds"`
}

// defaultExchangeMaxRounds is far more rounds than a question between two roles
// has ever needed and few enough that the loop this bounds costs a knowable
// amount before it reaches the operator.
const defaultExchangeMaxRounds = exchange.DefaultMaxRounds

// Conversation is what a management conversation's picture of the repository
// is held to. The picture — specifications, tracker, and the rest of the
// briefing — is gathered when a conversation opens and sent on its first turn,
// and every later turn resumes a session that already holds it. Before each
// reply the harness measures how far that picture has fallen behind the target
// branch, in landings rather than hours, and past the threshold here it
// re-reads the repository and the tracker before the turn is answered. Where
// the re-read cannot be made, the reply says in its own text how many landings
// old its picture is.
//
// The threshold is a project's judgement about its own pace: how many landings
// a conversation may reason across before what it does not know it does not
// know is worth the cost of re-briefing it. What it is not is a switch. No value
// turns the measurement, the re-read, or the statement off, which is what the
// upper bound below is for.
type Conversation struct {
	// RefreshAfterLandings is how many landings on the target branch the picture
	// may fall behind before a turn re-reads it. A project that states nothing
	// gets the harness default. Zero is refused rather than read as a choice,
	// like the exchange rounds: a picture that may fall no landings behind is
	// re-read on every turn, which is a threshold of nothing. A number above
	// MaxRefreshAfterLandings is refused rather than accepted as a way of never
	// refreshing.
	RefreshAfterLandings int `yaml:"refresh_after_landings" json:"refresh_after_landings"`
}

// DefaultRefreshAfterLandings is a day or so of landings on a busy branch: a
// conversation left open overnight is re-briefed before its first reply the
// next morning, and one held over an afternoon is not re-briefed at all, since a
// refresh carries the whole briefing into the turn again and is the most
// expensive thing a conversation does short of a run.
//
// MaxRefreshAfterLandings bounds what a project may set. The case this exists
// for was a picture roughly five hundred landings behind, and a threshold that
// let one be advised from unrefreshed and unlabelled would be the configuration
// disabling the statement it is only meant to time.
const (
	DefaultRefreshAfterLandings = 20
	MaxRefreshAfterLandings     = 200
)

// Research is what this project permits the product manager to find out from
// outside the repository, and what it is bounded by. Every part of it is a
// judgement an operator makes about their own money, their own privacy, and
// their own patience, which is why it is configuration and why its default is
// nothing at all.
//
// A project that states no source has the capability off. That is the important
// default and it is deliberate: a conversational role reaching the network is
// something an operator turns on, naming what it may reach, rather than
// something they acquire by extending a bundle or upgrading the executable. It
// is the same rule work_items, integration, and publishing are held to.
type Research struct {
	// Sources are the evidence sources the operator permits, each a command the
	// harness runs with the question on standard input and whose standard output
	// is the evidence. It is a command rather than a built-in integration for the
	// reason the checks are commands: the operator decides what runs, in the file
	// they decide everything else in, and the harness reaches nothing they did not
	// name.
	//
	// It replaces an inherited list wholesale rather than adding to it, like the
	// check list: what the harness may reach is one statement, and half of one
	// operator's answer joined to half of another's is a policy nobody decided.
	Sources []research.Source `yaml:"sources,omitempty" json:"sources,omitempty"`
	// MaxQueriesPerTurn is the cost bound: how many questions one reply may set
	// off. Zero takes the harness default, and it is capped at what the protocol
	// itself permits, so a project cannot configure its way past a limit the block
	// enforces.
	MaxQueriesPerTurn int `yaml:"max_queries_per_turn,omitempty" json:"max_queries_per_turn,omitempty"`
	// Timeout is the time bound, per question. Zero takes the harness default. It
	// is never zero in effect: a source with no budget holds a conversation open
	// for as long as it keeps not answering, with the operator sitting in front of
	// it.
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Policy is this configuration as the research capability reads it. It is
// derived rather than stored so the two can never disagree about what a project
// permitted.
func (r Research) Policy() research.Policy {
	return research.Policy{
		Sources:           r.Sources,
		MaxQueriesPerTurn: r.MaxQueriesPerTurn,
		Timeout:           time.Duration(r.Timeout),
	}
}

type Approvals struct {
	// Brief, Goals, and Designs decide which canonical documents the operator's
	// approval is asked for. `human` means the operator approves the document and
	// the approval is recorded against it, in its frontmatter and against the
	// revision it was given for, so an approved document and a draft one are
	// distinguishable and a document amended after approval is distinguishable
	// from both. Neither setting gates anything: an unapproved document still
	// loads and still governs what is downstream of it, and what is reported is
	// what is recorded. Goals governs the non-goals with the goals, because a
	// bound on intent nobody approved is as much unapproved intent as a goal is.
	//
	// The shipped bundle says `human` for the brief and the goals deliberately
	// rather than by inheritance: they are what the operator states and the whole
	// of what the design asks a person to approve routinely, and everything
	// downstream traces to them. Designs are `automatic` for the same reason read
	// the other way — a design that serves an approved goal is the architect's
	// judgement about how, and asking a person to approve each one is the
	// per-change gate autonomy is the absence of.
	Brief   domain.ApprovalMode `yaml:"brief" json:"brief"`
	Goals   domain.ApprovalMode `yaml:"goals" json:"goals"`
	Designs domain.ApprovalMode `yaml:"designs" json:"designs"`
	// WorkItems decides whether the operator is asked about every work item
	// before it reaches the queue. It is the one approval that gates rather than
	// records: `human` is the per-item gate the harness started with, where the
	// product manager proposes and nothing exists until the operator says so,
	// and `automatic` moves that decision up to the goals — work that traces to
	// a goal the operator approved is admitted without a further prompt, and
	// everything else is still put to them.
	//
	// "Everything else" is not a residue. Work that traces to no goal, work that
	// would cut against one, and work the product manager judges to be against
	// what the product is for are exactly the cases it escalates rather than
	// proposes, and a change to the goals themselves is the operator's and
	// reaches the queue through nothing. Approval moved up a level; it did not
	// disappear.
	//
	// `automatic` requires the operator to be approving goals at all, which is
	// checked below: the whole weight of admitting work without asking rests on
	// the goal it serves having been approved, and a project recording no goal
	// approvals has nothing for it to rest on. That refusal only ever names a key
	// its author wrote, because `automatic` is never inherited — see below.
	//
	// Both the harness default and the shipped bundle say `human`, deliberately
	// and at the same value: this is the setting that lets work reach the queue
	// with no person in the loop, so a project turns it on rather than acquiring
	// it by extending a bundle or by upgrading the executable. That is the same
	// rule integration and publishing are held to, and for the same reason.
	WorkItems domain.ApprovalMode `yaml:"work_items" json:"work_items"`
	// WorkItemExemptions are the classes of work this project admits without
	// asking, whatever WorkItems says. It exists because the per-item gate turned
	// out to be coarser than the operators who keep it actually mean: an operator
	// who wants to be asked about every change to the product does not
	// necessarily want to be asked before something reads the repository and
	// writes down what it found, and with no way to say so the policy they
	// recorded stays a sentence nothing enforces.
	//
	// It is empty by default, which is the gate exactly as it stands: an
	// exemption is an operator handing over a decision, and one that arrived by
	// inheritance or by upgrading the executable would not be that. Each class is
	// stated rather than derived, and the harness recognizes only the classes it
	// names — a project naming anything else is refused rather than quietly
	// exempting nothing.
	//
	// What it does not move is the goal. Work admitted under an exemption still
	// names a goal the repository records, because an exemption is about who is
	// asked and never about whether the work is for anything.
	WorkItemExemptions []domain.WorkItemClass `yaml:"work_item_exemptions" json:"work_item_exemptions,omitempty"`
	Integration        domain.ApprovalMode    `yaml:"integration" json:"integration"`
	// Publishing decides whether the harness pushes a run's branch and opens the
	// pull request its reviewer's verdict merges. It sits beside integration
	// because publishing has the wider blast radius of the two: integration moves
	// a local branch, publishing puts the work somewhere other people see it.
	// `human` leaves pushing and pull requests to the operator, which is what a
	// project gets until it opts in.
	Publishing domain.ApprovalMode `yaml:"publishing" json:"publishing"`
}

type AgentConfig struct {
	Role    domain.AgentRole `yaml:"role" json:"role"`
	Backend domain.Backend   `yaml:"backend" json:"backend"`
	// Model is the required provider model selector for every instance of this
	// agent. There is no implicit harness default: a family alias such as
	// "opus" intentionally floats to the backend's current default for that
	// family, while an exact provider identifier pins a version.
	Model string `yaml:"model" json:"model"`
	// ModelVersion is the exact version of that family this agent's turns ask
	// for, and empty for every agent that pins none — which is the floating alias
	// above behaving exactly as it always has. Where it is named and the provider
	// has not got it, the turn is served by the alias, which is the family's
	// latest by definition; see modelversion.go for why a pin is a preference
	// rather than a requirement.
	ModelVersion string `yaml:"model_version,omitempty" json:"model_version,omitempty"`
	// Effort is the effort level every invocation of this agent asks its
	// provider for, validated against the levels that provider accepts. Empty is
	// an agent that names none; Codex gets its explicit model default, while
	// Claude retains its own resolution of an omitted level; see effort.go.
	Effort string `yaml:"effort,omitempty" json:"effort,omitempty"`
	// Account is the alias of the provider account this agent runs under, from
	// the top-level accounts mapping. The assignment is the operator's and it is
	// fixed: an agent runs where the configuration says it runs, and nothing
	// chooses at run time. A project that states nothing has every agent assigned
	// to its single account, which is the only arrangement v1 executes.
	Account   string `yaml:"account,omitempty" json:"account,omitempty"`
	Instances int    `yaml:"instances,omitempty" json:"instances"`
	// Persona is the resolved role guidance handed to this agent's prompt. It
	// may specialize how an agent works; it can never remove a harness
	// invariant, which is why the immutable contracts stay in Go.
	Persona Persona `yaml:"persona,omitempty" json:"persona,omitempty"`
	// Failover is what serves this agent's turn while the model above has no
	// capacity. It is off unless this agent says otherwise, and what it may reach
	// is the one alternate this agent names — see failover.go for why both halves
	// are the agent's own rather than the harness's.
	Failover Failover `yaml:"failover,omitempty" json:"failover,omitempty"`
	// Conversations is what this agent does with a question that arrives while
	// its main thread is busy: queue it, which is what every agent did before this
	// key existed and what an agent naming nothing still does, or hold it on a
	// side thread. It sits beside the persona because it is a choice about how
	// this agent works rather than about what it may do — see sidethreads.go for
	// why no value of it reaches a side thread's authority.
	Conversations ConversationMode `yaml:"conversations,omitempty" json:"conversations,omitempty"`
	// Lane is the tracker label a program manager instance owns, and empty for
	// every agent of any other role, which is refused a lane when the file loads.
	// Two instances naming one lane are refused too: a lane has one owner. See
	// lane.go for why none of the three instance keys grants anything.
	Lane string `yaml:"lane,omitempty" json:"lane,omitempty"`
	// Remit says what a program manager instance's lane is for. It is read by
	// the persona's loader and held to every rule a persona is, and it follows
	// the persona in the prompt on every turn of the instance's conversation.
	Remit Persona `yaml:"remit,omitempty" json:"remit,omitempty"`
	// Triggers is what wakes a program manager instance for a pass over its lane:
	// a cadence and the events from a closed set. Reading them is the pass
	// machinery's; the configuration loads, validates, and reports them.
	Triggers Triggers `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	// Capabilities is everything the harness may do on this agent's behalf,
	// stated in the closed vocabulary rather than implied by the role's name. It
	// is read from `internal/rolecapability` as the configuration resolves, so
	// what a shipped agent holds and what the registry says it holds are one
	// statement that cannot come apart.
	//
	// No layer supplies it and no layer may: there is no `capabilities` key in a
	// configuration document, so a project naming one is refused by the decoder
	// exactly as any other unknown key is. That is the design's law rather than
	// an omission — a role definition says what may be performed at all, and a
	// file that could widen it would be a project granting itself authority.
	// Operator-authored bundles are a later step of the same workstream, and they
	// arrive protected and activated by digest rather than as a key here.
	//
	// It is reported like the derived halves of a persona are, because an
	// operator asking what an agent is has a better answer for seeing it, and it
	// is part of the configuration's revision for the same reason a bundle
	// default is: an executable that shipped a role a different capability set
	// shipped a different configuration.
	Capabilities []capability.Capability `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// MaxPersonaBytes bounds a persona so role guidance stays guidance rather than
// an unbounded document smuggled into every prompt.
const MaxPersonaBytes = 32 << 10

// Persona is one resolved persona: the declared revision, the path it was
// declared with, where the text was actually read from, and the text itself.
// Text is excluded from serialized diagnostics, which report its size instead,
// so `config show` stays readable.
type Persona struct {
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
	Source  string `yaml:"source,omitempty" json:"source,omitempty"`
	Bytes   int    `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	Text    string `yaml:"-" json:"-"`
}

// Defined reports whether an agent declares a persona at all. Agents in legacy
// complete configurations may declare none, and prompt construction then falls
// back to the immutable harness contract alone.
func (p Persona) Defined() bool {
	return strings.TrimSpace(p.Version) != "" || strings.TrimSpace(p.Path) != "" || strings.TrimSpace(p.Text) != ""
}

func (c Config) Validate() error {
	var problems []string

	if c.Version != CurrentVersion {
		problems = append(problems, fmt.Sprintf("version must be %d", CurrentVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.Product.ID)); err != nil {
		problems = append(problems, err.Error())
	}
	if err := domain.ValidateIdentifier("repository id", string(c.Product.RepositoryID)); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Product.Repository) == "" {
		problems = append(problems, "product repository is required")
	}
	if err := validateSpecificationsDirectory(c.Product.Specifications); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product invariants", c.Product.Invariants); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product designs", c.Product.Designs); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product decisions", c.Product.Decisions); err != nil {
		problems = append(problems, err.Error())
	}
	for _, documentPath := range c.Product.ShippedDocumentation {
		if err := validateShippedDocument(documentPath); err != nil {
			problems = append(problems, err.Error())
		}
	}
	problems = append(problems, namedContextProblems("codex", c.Codex)...)
	problems = append(problems, namedContextProblems("claude_code", c.ClaudeCode)...)
	if c.Execution.MaxConcurrentDevelopers < 1 {
		problems = append(problems, "max_concurrent_developers must be at least 1")
	}
	problems = append(problems, developerSlotProblems(c.Execution)...)
	problems = append(problems, developerModelProblems(c.Execution)...)
	if c.Execution.RepairAttemptsBeforeReplan < 0 {
		problems = append(problems, "repair_attempts_before_replan cannot be negative")
	}
	// Zero is a deliberate choice here too — never retry a refused promotion —
	// so only a negative bound, which describes no retry anybody could take, is
	// refused.
	if c.Execution.IntegrationRetriesBeforeReconciliation < 0 {
		problems = append(problems, "integration_retries_before_reconciliation cannot be negative")
	}
	// And here, for the same reason: zero says fail on the first transient death,
	// which is a choice, and a negative bound is not one.
	if c.Execution.TransientRelaunchesBeforeBlocking < 0 {
		problems = append(problems, "transient_relaunches_before_blocking cannot be negative")
	}
	if strings.TrimSpace(c.Execution.WorktreeRoot) == "" {
		problems = append(problems, "worktree_root is required")
	}
	if err := validateRemoteName(c.Execution.Remote); err != nil {
		problems = append(problems, err.Error())
	}
	// An unset push remote is the ordinary arrangement — run branches go to the
	// remote above — so only a value that names no remote is refused. It is
	// checked as a name rather than against the repository for the same reason
	// the remote above is: whether the checkout has it is a question about a
	// repository, answered where publishing is resolved.
	if pushRemote := strings.TrimSpace(c.Execution.PushRemote); pushRemote != "" && !remoteNamePattern.MatchString(pushRemote) {
		problems = append(problems, fmt.Sprintf("push_remote %q must be a plain Git remote name", c.Execution.PushRemote))
	}
	// Zero is a deliberate choice — never wait, block on every exhausted limit —
	// so only a negative bound, which describes no wait anybody could take, is
	// refused.
	if c.Execution.UsageLimitMaxPause < 0 {
		problems = append(problems, "usage_limit_max_pause cannot be negative")
	}
	if c.Execution.UsageLimitUnknownResetPause <= 0 {
		problems = append(problems, "execution.usage_limit_unknown_reset_pause must be positive")
	}
	if c.Execution.UsageLimitInProcessPause < 0 {
		problems = append(problems, "usage_limit_in_process_pause cannot be negative")
	}
	// Zero is not a deliberate choice here, unlike the pauses above: a check with
	// no budget at all holds a worktree, a claim, and a run open for as long as
	// it keeps running, and nothing else bounds it.
	if c.Execution.CheckTimeout <= 0 {
		problems = append(problems, "execution.check_timeout must be positive")
	}
	// The stage bound is what a per-check budget cannot be — a bound on the whole
	// list — so a stage with none would be every check's budget added up again,
	// which is the two-hour stage this exists to end.
	if c.Execution.CheckStageTimeout <= 0 {
		problems = append(problems, "execution.check_stage_timeout must be positive")
	}
	if c.Execution.LandingCheckTimeout <= 0 {
		problems = append(problems, "execution.landing_check_timeout must be positive")
	}
	// An overload names no reset time, so this interval is the whole of the wait
	// rather than a bound on it. Zero would mean reissuing straight back into the
	// same overloaded server with nothing between the attempts.
	if c.Execution.ServerOverloadPause <= 0 {
		problems = append(problems, "execution.server_overload_pause must be positive")
	}
	// Zero is not a choice here either: a watch session that polled with nothing
	// between the readings would read the tracker as fast as the machine allows
	// for as long as the queue stayed empty, which is a spin rather than a watch.
	if c.Execution.WorkPoll <= 0 {
		problems = append(problems, "execution.work_poll must be positive")
	}
	// Zero is not a choice here: a drain with no bound is a session that waits
	// on whatever its hosted run happens to be doing, which on 2026-09-19 was two
	// hours of a race suite under load. A session that must not stop its runs
	// for a deploy sets this long rather than to nothing.
	if c.Execution.RedeployDrainLimit <= 0 {
		problems = append(problems, "execution.redeploy_drain_limit must be positive")
	}
	// Zero is a configuration assembled without the key, which reads the
	// default; only a negative limit describes nothing anybody could mean.
	if c.Execution.FactoryStallAfter < 0 {
		problems = append(problems, "execution.factory_stall_after cannot be negative")
	}
	if c.Execution.MissingReportsBeforeFreshConversation < 0 {
		problems = append(problems, "execution.missing_reports_before_fresh_conversation cannot be negative")
	}
	// Zero is a choice here — never brake, let the operator be the only thing
	// that holds intake — so only a negative bound, which describes no run
	// anybody could count, is refused.
	if c.Execution.BlockedRunsBeforeIntakeHold < 0 {
		problems = append(problems, "blocked_runs_before_intake_hold cannot be negative")
	}
	// Zero is a choice here too: the summons is taken before the cooldown is
	// read, so a cooldown of no time at all waits for her summoned turn and no
	// longer, and probes at the poll after it. Only a negative one, which
	// describes no wait anybody could take, is refused.
	if c.Execution.BrakeCooldown < 0 {
		problems = append(problems, "execution.brake_cooldown cannot be negative")
	}
	// Zero is a choice — never escalate the loop on its own — so only a negative
	// bound, which describes no number of cycles anybody could count, is refused.
	if c.Execution.BrakeEscalationCycles < 0 {
		problems = append(problems, "execution.brake_escalation_cycles cannot be negative")
	}
	// An age of zero is not the choice the pauses above make with theirs: it
	// dockets every approved publication the instant it is made, which is not a
	// stricter triage but the end of triage meaning anything.
	if c.Triage.StuckMergeAge <= 0 {
		problems = append(problems, "triage.stuck_merge_age must be positive")
	}
	// Zero is a choice here — an item that reaches triage is escalated or
	// re-scoped rather than repaired again — so only a negative cap, which
	// describes no round anybody could take, is refused.
	if c.Triage.ReviewRoundsCap < 0 {
		problems = append(problems, "triage.review_rounds_cap cannot be negative")
	}
	// And zero is not a choice here: triage granting no attempts leaves the item
	// exactly where granting nothing would have, so it is refused rather than
	// silently spent.
	if c.Triage.RepairGrantAttempts < minimumRepairGrant {
		problems = append(problems, "triage.repair_grant_attempts must be at least 1")
	}
	// And nor is zero a choice here: an exchange that may take no round is a
	// question nobody can put, which is what leaving the channel unused already
	// is.
	if c.Exchange.MaxRounds < 1 {
		problems = append(problems, "exchange.max_rounds must be at least 1")
	}
	// Zero is not a choice here either — a picture that may fall no landings
	// behind is re-read on every turn, which is a threshold of nothing rather
	// than a choice about pace — and neither is a number past the bound: the
	// threshold times the re-read and does not switch it off.
	if c.Conversation.RefreshAfterLandings < 1 {
		problems = append(problems, "conversation.refresh_after_landings must be at least 1")
	}
	if c.Conversation.RefreshAfterLandings > MaxRefreshAfterLandings {
		problems = append(problems, fmt.Sprintf("conversation.refresh_after_landings must be at most %d; the threshold times the re-read and cannot turn it off", MaxRefreshAfterLandings))
	}
	problems = append(problems, c.Research.problems()...)

	approvalValues := []struct {
		name string
		mode domain.ApprovalMode
	}{
		{name: "brief", mode: c.Approvals.Brief},
		{name: "goals", mode: c.Approvals.Goals},
		{name: "designs", mode: c.Approvals.Designs},
		{name: "work_items", mode: c.Approvals.WorkItems},
		{name: "integration", mode: c.Approvals.Integration},
		{name: "publishing", mode: c.Approvals.Publishing},
	}
	for _, approval := range approvalValues {
		if !approval.mode.Valid() {
			problems = append(problems, fmt.Sprintf("approval %s must be %q or %q", approval.name, domain.ApprovalHuman, domain.ApprovalAutomatic))
		}
	}
	// A class the harness does not recognize exempts nothing, so a file naming one
	// is refused rather than loaded with a policy its author believes is in force.
	// A duplicate is refused for the same reason: it is a file saying something
	// twice, which is the shape a merge that went wrong leaves behind.
	exempted := make(map[domain.WorkItemClass]struct{}, len(c.Approvals.WorkItemExemptions))
	for _, class := range c.Approvals.WorkItemExemptions {
		if !class.Valid() {
			problems = append(problems, fmt.Sprintf("approvals.work_item_exemptions names %q, which is not a class the harness recognizes; the classes there are: %s", class, namedWorkItemClasses()))
			continue
		}
		if _, duplicate := exempted[class]; duplicate {
			problems = append(problems, fmt.Sprintf("approvals.work_item_exemptions names %q twice", class))
			continue
		}
		exempted[class] = struct{}{}
	}

	// The providers this project may name are resolved before the agents are
	// checked, because every agent names one. A declaration that will not build
	// is reported here and the agents are then checked against the built-ins
	// alone, so a typo in one plugin does not turn every agent in the file into a
	// second complaint about a backend nobody can find.
	providers, providerErr := c.ProviderRegistry()
	if providerErr != nil {
		problems = append(problems, providerErr.Error())
		providers, _ = backend.NewRegistry(nil)
	}

	if len(c.Agents) == 0 {
		problems = append(problems, "at least one agent is required")
	}

	agentNames := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	developers := 0
	reviewers := 0
	for _, name := range agentNames {
		agent := c.Agents[name]
		if err := domain.ValidateIdentifier("agent name", name); err != nil {
			problems = append(problems, err.Error())
		}
		// The set of roles is fixed in the harness, so a name outside it is
		// refused here rather than reaching the backend that assembles the
		// invocation: everything downstream — the role's authority, its tool
		// posture, whether it counts as a developer — is derived from the name,
		// and a typo in an agents block would otherwise load and fail only once
		// work had been claimed. The known roles are named because the whole
		// point of the refusal is that the operator can see which one was meant.
		roleKnown := agent.Role.Valid()
		if strings.TrimSpace(string(agent.Role)) == "" {
			problems = append(problems, fmt.Sprintf("agent %q role is required", name))
		} else if !roleKnown {
			problems = append(problems, fmt.Sprintf("agent %q has unknown role %q; roles are %s", name, agent.Role, describeRoles()))
		}
		// Which backends exist, and what each of them serves, is one question
		// asked of the registry rather than two asked of a switch: a provider this
		// project declared is refused for an unsupported role or an unsupported
		// tool posture exactly as a backend this build ships is, and both are
		// refused here, before any work is assigned to an agent that names one.
		//
		// Why a provider may not serve a role is the descriptor's own answer
		// rather than a second reading of its declaration taken here, so what
		// refuses a configuration and what refuses a substitution at the moment a
		// window closes are one derivation. It is eligibility that is shared —
		// the roles a provider declares and the postures it can be held to.
		// Whether this build ships an adapter that could launch the provider is
		// not asked here and never has been: a project may name a backend the
		// vocabulary has and this build cannot run, and what refuses it is the
		// dispatch of a run, before anything is claimed.
		descriptor, known := providers.Lookup(agent.Backend)
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("agent %q has unsupported backend %q", name, agent.Backend))
		case !roleKnown:
			// The role is already reported above, and a backend cannot be said to
			// support or refuse a name that is not a role at all.
		default:
			if refusal := descriptor.RoleRefusal(agent.Role); refusal != "" {
				problems = append(problems, fmt.Sprintf("%s, for agent %q", refusal, name))
			}
		}
		// Every executable agent declares its own selector; the harness never
		// falls back to a provider default nobody chose or recorded.
		if err := validateModelSelector(agent.Model); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q %s", name, err))
		}
		problems = append(problems, modelVersionProblems(name, agent.ModelVersion, agent.Model, agent.Failover.Model)...)
		problems = append(problems, effortProblems(providers, name, agent)...)
		problems = append(problems, c.otherEffortProblems(providers, name, agent)...)
		if agent.Instances < 1 {
			problems = append(problems, fmt.Sprintf("agent %q instances must be at least 1", name))
		}
		problems = append(problems, agent.Persona.problems(name)...)
		problems = append(problems, agent.Failover.problems(name, agent)...)
		problems = append(problems, c.failoverEndpointProblems(providers, name, agent)...)
		problems = append(problems, conversationModeProblems(name, agent.Conversations)...)
		problems = append(problems, instanceProblems(name, agent)...)
		if agent.Role == domain.RoleDeveloper {
			developers += agent.Instances
		}
		if agent.Role == domain.RoleReviewer {
			reviewers += agent.Instances
		}
	}
	problems = append(problems, laneProblems(c.Agents)...)
	problems = append(problems, c.slotRoutingProblems(providers)...)
	if developers == 0 {
		problems = append(problems, "at least one developer agent is required")
	}
	if developers > 0 && c.Execution.MaxConcurrentDevelopers > developers {
		problems = append(problems, "max_concurrent_developers cannot exceed configured developer instances")
	}

	for index, check := range c.Checks {
		if strings.TrimSpace(check) == "" {
			problems = append(problems, fmt.Sprintf("check %d cannot be empty", index))
		}
	}
	for index, check := range c.LandingChecks {
		if strings.TrimSpace(check) == "" {
			problems = append(problems, fmt.Sprintf("landing check %d cannot be empty", index))
		}
	}
	for index, check := range c.PathChecks {
		problems = append(problems, check.problems(index)...)
	}
	// Admitting work without asking rests entirely on the operator's approval of
	// the goal that work serves, so a project that records no goal approvals has
	// nothing for it to rest on: the setting would read as autonomy and mean
	// nothing, because no goal would ever be approved for work to trace to. It is
	// refused here rather than left to be discovered as a queue that never fills,
	// which is the same choice automatic integration makes about its own gates.
	if c.Approvals.WorkItems == domain.ApprovalAutomatic && c.Approvals.Goals != domain.ApprovalHuman {
		problems = append(problems, fmt.Sprintf("automatic work_items requires approvals.goals to be %q; work is admitted without asking on the strength of the goal it serves having been approved", domain.ApprovalHuman))
	}
	if c.Approvals.Integration == domain.ApprovalAutomatic {
		if len(c.Checks) == 0 {
			problems = append(problems, "automatic integration requires at least one check")
		}
		if reviewers == 0 {
			problems = append(problems, "automatic integration requires at least one reviewer agent")
		}
	}

	problems = append(problems, c.accountProblems()...)
	problems = append(problems, c.accountProviderProblems(providers)...)
	problems = append(problems, c.operatorProblems()...)
	problems = append(problems, c.Slack.problems()...)
	problems = append(problems, c.Services.problems(c.Slack)...)
	problems = append(problems, validateRecurringTasks(c)...)
	problems = append(problems, passNameProblems(c)...)

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

// developerSlotProblems refuses a slot list the capacity cannot carry out and a
// preference no item could ever satisfy. A list longer than the capacity names
// a slot that does not exist, and a preference on it is one nothing will ever
// act on — refused here rather than left to read as a slot the operator
// believes is dedicated. A label the tracker would not carry is refused for the
// same reason: a slot preferring one would pull nothing first, forever, and
// nothing downstream would say so.
func developerSlotProblems(execution Execution) []string {
	var problems []string
	if len(execution.DeveloperSlots) > execution.MaxConcurrentDevelopers && execution.MaxConcurrentDevelopers >= 1 {
		problems = append(problems, fmt.Sprintf("execution.developer_slots names %d slot(s) and max_concurrent_developers is %d; a developer slot is one unit of that capacity, so the list may be shorter than it and never longer",
			len(execution.DeveloperSlots), execution.MaxConcurrentDevelopers))
	}
	seen := make(map[int]bool)
	for index, slot := range execution.DeveloperSlots {
		number := index + 1
		if slot.Number != nil {
			number = *slot.Number
			if number < 1 || number > execution.MaxConcurrentDevelopers {
				problems = append(problems, fmt.Sprintf("execution.developer_slots slot %d.number must be between 1 and max_concurrent_developers (%d)", index+1, execution.MaxConcurrentDevelopers))
			}
			if number != index+1 {
				problems = append(problems, fmt.Sprintf("execution.developer_slots slot %d.number is %d; slot numbers must follow list order", index+1, number))
			}
		}
		if seen[number] {
			problems = append(problems, fmt.Sprintf("execution.developer_slots slot %d.number repeats slot %d", index+1, number))
		}
		seen[number] = true
		for _, err := range slot.Problems() {
			problems = append(problems, fmt.Sprintf("execution.developer_slots slot %d: %v", index+1, err))
		}
	}
	return problems
}

// namedWorkItemClasses lists the classes a project may exempt, so a refusal
// names what was actually available rather than only what was wrong.
func namedWorkItemClasses() string {
	named := make([]string, 0, len(domain.WorkItemClasses))
	for _, class := range domain.WorkItemClasses {
		named = append(named, fmt.Sprintf("%q", class))
	}
	return strings.Join(named, ", ")
}

// slackChannelPattern keeps a configured channel a channel: an id, or a name
// with or without its leading hash. Anything else names nothing the workspace
// has, and is refused here rather than becoming a posting failure every few
// seconds for as long as the sink runs.
var slackChannelPattern = regexp.MustCompile(`^#?[A-Za-z0-9][A-Za-z0-9._-]*$`)

// slackUserPattern is the shape of a Slack member id, which an operator binds in
// the top-level mapping. Names are deliberately not accepted: a display name is
// not identity — two people can carry one, and one person can change theirs —
// and a binding keyed on something that moves is one that quietly stops matching
// the person it was written for.
var slackUserPattern = regexp.MustCompile(`^[UW][A-Z0-9]{6,}$`)

// slackEmojiPattern is the shape of an emoji shortcode: a name between colons,
// optionally with a skin-tone variant after it. It checks the shape and
// deliberately not the name, because a workspace's own custom emoji are the
// workspace's — a list of accepted names checked in here would refuse
// `:ship-it:` and go stale besides.
var slackEmojiPattern = regexp.MustCompile(`^:[a-z0-9][a-z0-9_+-]*:(:skin-tone-[2-6]:)?$`)

// MaxSlackChannelBytes bounds a configured channel so it stays a channel rather
// than a request smuggled onto the Slack API.
const MaxSlackChannelBytes = 80

// MaxSlackAvatarBytes bounds one configured avatar. It is generous enough for
// an image URL with a path on it and far short of anything that is no longer an
// avatar.
const MaxSlackAvatarBytes = 500

// SlackHarnessAvatar is the key an avatar override uses for what no persona did.
// It is the notifier's own speaker key, spelled here so checking a key does not
// make the configuration package depend on the notifier.
const SlackHarnessAvatar = "harness"

// problems reports what makes a Slack section unusable. Everything is checked
// whether or not reporting is enabled, so a project that has configured the
// section and not switched it on yet learns about a typo now rather than on the
// day it turns reporting on.
// problems reports everything wrong with the research settings at once. A
// project that permits no source has nothing to be wrong: the capability is off,
// which is what most projects are, and the bounds beneath it bound nothing.
func (r Research) problems() []string {
	if len(r.Sources) == 0 {
		return nil
	}
	var problems []string
	named := make(map[string]struct{}, len(r.Sources))
	for _, source := range r.Sources {
		if err := source.Validate(); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		// Two sources of one name is a question whose destination depends on which
		// entry the harness happened to find first, which is not a destination the
		// operator chose.
		if _, duplicate := named[source.Name]; duplicate {
			problems = append(problems, fmt.Sprintf("research source %q is named twice", source.Name))
			continue
		}
		named[source.Name] = struct{}{}
	}
	// Zero is a choice here — take the harness default — so only a negative
	// number, which describes no question anybody could ask, is refused.
	if r.MaxQueriesPerTurn < 0 {
		problems = append(problems, "research.max_queries_per_turn cannot be negative")
	}
	if r.Timeout < 0 {
		problems = append(problems, "research.timeout cannot be negative")
	}
	return problems
}

func (s Slack) problems() []string {
	var problems []string
	channel := strings.TrimSpace(s.Channel)
	switch {
	case channel == "":
		if s.Enabled {
			problems = append(problems, "slack.channel is required when slack is enabled")
		}
	case len(channel) > MaxSlackChannelBytes:
		problems = append(problems, fmt.Sprintf("slack.channel is %d bytes, limit is %d", len(channel), MaxSlackChannelBytes))
	case !slackChannelPattern.MatchString(channel):
		problems = append(problems, fmt.Sprintf("slack.channel %q must be a channel id or name", s.Channel))
	}
	return append(problems, s.avatarProblems()...)
}

// avatarProblems reports what makes a configured avatar unusable. A typo here
// is worth refusing at load rather than posting: Slack takes an unknown
// shortcode or an unreachable image without complaint and simply shows the
// app's own icon, so the failure would be a picture nobody notices is the wrong
// one rather than anything that says so.
//
// The keys are reported in a stable order, because a validation error that
// names three problems in a different order on every load is one nobody can
// diff.
func (s Slack) avatarProblems() []string {
	speakers := make([]string, 0, len(s.Avatars))
	for speaker := range s.Avatars {
		speakers = append(speakers, speaker)
	}
	sort.Strings(speakers)

	var problems []string
	for _, speaker := range speakers {
		if speaker != SlackHarnessAvatar && !domain.AgentRole(speaker).Valid() {
			problems = append(problems, fmt.Sprintf("slack.avatars %q is not a role or %q", speaker, SlackHarnessAvatar))
			continue
		}
		avatar := strings.TrimSpace(s.Avatars[speaker])
		switch {
		case avatar == "":
			problems = append(problems, fmt.Sprintf("slack.avatars.%s is empty; leave it out to keep the one the harness ships", speaker))
		case len(avatar) > MaxSlackAvatarBytes:
			problems = append(problems, fmt.Sprintf("slack.avatars.%s is %d bytes, limit is %d", speaker, len(avatar), MaxSlackAvatarBytes))
		case !slackEmojiPattern.MatchString(avatar) && !slackImageURL(avatar):
			problems = append(problems, fmt.Sprintf("slack.avatars.%s %q must be an emoji shortcode like %q or an https image URL", speaker, avatar, ":robot_face:"))
		}
	}
	return problems
}

// slackImageURL reports the other shape an avatar may take. It is https only:
// Slack fetches the image itself and an avatar is not worth a plaintext hop,
// and a project that meant a shortcode and wrote something else gets a refusal
// naming both shapes rather than a URL nothing will load.
func slackImageURL(avatar string) bool {
	parsed, err := url.Parse(avatar)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// problems reports what makes a declared persona unusable. A persona that
// resolved to nothing is a configuration error rather than a silent fallback:
// an agent whose configured guidance vanished is not the agent that was
// configured.
func (p Persona) problems(agentName string) []string {
	return p.problemsAs(agentName, "persona", MaxPersonaBytes)
}

// problemsAs is the persona rules stated for any document held to them, naming
// the document by kind — which is how a program manager's remit is refused in
// the persona's own words.
func (p Persona) problemsAs(agentName, kind string, limit int) []string {
	if !p.Defined() {
		return nil
	}
	var problems []string
	if strings.TrimSpace(p.Version) == "" {
		problems = append(problems, fmt.Sprintf("agent %q %s version is required", agentName, kind))
	}
	if strings.TrimSpace(p.Path) == "" {
		problems = append(problems, fmt.Sprintf("agent %q %s path is required", agentName, kind))
	}
	if strings.TrimSpace(p.Text) == "" {
		problems = append(problems, fmt.Sprintf("agent %q %s %q is empty", agentName, kind, p.Path))
	}
	if len(p.Text) > limit {
		problems = append(problems, fmt.Sprintf("agent %q %s %q is %d bytes, limit is %d", agentName, kind, p.Path, len(p.Text), limit))
	}
	return problems
}

// describeRoles lists the harness's roles the way an operator would read them
// back into the file they mistyped.
func describeRoles() string {
	names := make([]string, 0, len(domain.Roles()))
	for _, role := range domain.Roles() {
		names = append(names, strconv.Quote(string(role)))
	}
	return strings.Join(names, ", ")
}

// validateSpecificationsDirectory keeps the product manager's inputs inside the
// repository. The path is resolved against the repository root, so an absolute
// path or one that climbs out of it names something the repository does not
// contain and is refused rather than read.
func validateSpecificationsDirectory(directory string) error {
	return validateRepositoryDirectory("product specifications", directory)
}

// validateRepositoryDirectory holds one configured directory inside the
// repository. Every such setting names artifacts an agent is shown, so the rule
// is stated once here rather than once per setting: a path that climbs out of
// the repository names text nobody reviewed with the code.
func validateRepositoryDirectory(setting, directory string) error {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return fmt.Errorf("%s directory is required", setting)
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(trimmed) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must be a directory inside the repository", setting, directory)
	}
	return nil
}

// validateShippedDocument holds one configured document inside the repository,
// and holds it to being Markdown. The confinement is validateRepositoryDirectory's
// rule said of a file, and for the same reason: a path that climbs out of the
// repository names text nobody reviewed with the code. The Markdown part is what
// the context bundle will actually read, so a project naming something else is
// refused here rather than discovering it as a conversation that will not open.
//
// It judges the trimmed path, and the bundle reads the trimmed path too, so what
// was validated is what is opened. A rule made on one and a read made on the
// other is a document this refuses to fault and nothing then carries.
func validateShippedDocument(documentPath string) error {
	const setting = "product shipped documentation"
	trimmed := strings.TrimSpace(documentPath)
	if trimmed == "" {
		return fmt.Errorf("%s cannot name an empty path", setting)
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(trimmed) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must be a file inside the repository", setting, documentPath)
	}
	if !strings.EqualFold(filepath.Ext(clean), ".md") {
		return fmt.Errorf("%s %q must be a Markdown file", setting, documentPath)
	}
	return nil
}

// remoteNamePattern keeps a configured remote a plain remote name. The harness
// puts it on a `git push` command line, so anything that could read as an
// option, a path, or a refspec is refused rather than passed along.
var remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateRemoteName(remote string) error {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return errors.New("remote is required")
	}
	if !remoteNamePattern.MatchString(trimmed) {
		return fmt.Errorf("remote %q must be a plain Git remote name", remote)
	}
	return nil
}

// MaxModelSelectorBytes bounds a configured selector so it stays a model name
// rather than an argument smuggled onto a provider command line.
const MaxModelSelectorBytes = 128

// ValidateModelSelector reports whether a configured model selector is usable.
// It deliberately accepts both floating family aliases and pinned identifiers,
// and rejects only what cannot name a model.
func ValidateModelSelector(model string) error {
	return validateModelSelector(model)
}

func validateModelSelector(model string) error {
	trimmed := strings.TrimSpace(model)
	switch {
	case trimmed == "":
		return errors.New("model selector is required; there is no implicit harness default")
	case len(trimmed) > MaxModelSelectorBytes:
		return fmt.Errorf("model selector is %d bytes, limit is %d", len(trimmed), MaxModelSelectorBytes)
	case strings.IndexFunc(trimmed, unicode.IsSpace) >= 0 || strings.HasPrefix(trimmed, "-"):
		return fmt.Errorf("model selector %q must be a single model name", model)
	}
	return nil
}

type ValidationError struct {
	Problems []string
}

func (e ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}

// namedContextProblems refuses a skill or instruction file named with no path or
// for a role that does not exist. Whether the file is there is asked when an
// invocation reads it, in the repository that invocation works in.
func namedContextProblems(section string, named backend.NamedContext) []string {
	var problems []string
	for kind, files := range map[string][]backend.ContextFile{"skills": named.Skills, "instructions": named.Instructions} {
		for index, file := range files {
			if strings.TrimSpace(file.Path) == "" {
				problems = append(problems, fmt.Sprintf("%s.%s[%d] names no path", section, kind, index))
			}
			for _, role := range file.Roles {
				if !role.Valid() {
					problems = append(problems, fmt.Sprintf("%s.%s[%d] names unknown role %q", section, kind, index, role))
				}
			}
		}
	}
	sort.Strings(problems)
	return problems
}
