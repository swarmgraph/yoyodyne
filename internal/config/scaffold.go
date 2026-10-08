package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ScaffoldOptions are the project facts a bundle cannot supply, because they
// describe the project being worked on rather than the harness working on it.
type ScaffoldOptions struct {
	ProductID  string
	Repository string
	// Detection is what reading the project's own files proposed as checks. Its
	// confident proposals become the checks list; its candidates are written
	// beside them, commented out and marked, for the operator to choose from.
	Detection Detection
}

// ScaffoldFile is one generated file, named relative to the project's
// .yoyodyne directory so a caller decides where that directory lives.
type ScaffoldFile struct {
	Path    string
	Content []byte
}

// Scaffold is a complete project configuration together with the persona files
// it references. Nothing in it is inherited at load time: the bundle is the
// template these files were generated from, not a layer underneath them.
type Scaffold struct {
	// Bundle names the template the files were generated from, for reporting.
	Bundle   string
	Config   ScaffoldFile
	Personas []ScaffoldFile
	// Lock records what the template supplied, so a later executable that
	// improves one of these values can say so rather than being invisible to a
	// project that owns its file outright. It is generated and committed, and
	// nothing reads it when the configuration loads.
	Lock ScaffoldFile
}

// Files lists everything the scaffold writes, configuration first and the
// baseline last: the configuration is what a caller reports and loads back, and
// the baseline is the one file here that decides nothing.
func (s Scaffold) Files() []ScaffoldFile {
	files := make([]ScaffoldFile, 0, len(s.Personas)+2)
	files = append(files, s.Config)
	files = append(files, s.Personas...)
	return append(files, s.Lock)
}

// NewScaffold renders a complete project configuration from a built-in bundle.
// The bundle supplies the agents, their selectors, and their personas; the
// options supply what only the project knows. The result states every value
// explicitly, so a project written this way loads without consulting the bundle
// again — and, deliberately, without receiving later changes to it.
func NewScaffold(bundleName string, options ScaffoldOptions) (Scaffold, error) {
	template, err := loadBuiltinBundle(bundleName)
	if err != nil {
		return Scaffold{}, err
	}
	// Resolving the bundle on its own fills in the harness defaults it does not
	// state and loads its persona text, so the rendered file can state both
	// rather than leaving them to be supplied again at load time.
	resolved, err := resolveLayers([]layer{{
		origin:   template.name,
		document: template.document,
		personas: template.personas,
	}})
	if err != nil {
		return Scaffold{}, err
	}

	effective := resolved.Config
	effective.Extends = ""
	effective.Product.ID = domain.ProductID(strings.TrimSpace(options.ProductID))
	effective.Product.Repository = strings.TrimSpace(options.Repository)
	effective.Checks = options.Detection.Commands()
	// Validate what loading the rendered file will see rather than what the
	// struct happens to hold, so the repository id is derived from the product
	// id here exactly as it is derived there. It stays out of the rendered file
	// for the same reason: it comes from a value the reader can already see.
	validated := effective
	validated.Product.RepositoryID = domain.RepositoryID(validated.Product.ID)
	if err := validated.Validate(); err != nil {
		return Scaffold{}, err
	}

	personas, err := scaffoldPersonas(template, effective)
	if err != nil {
		return Scaffold{}, err
	}
	// The baseline is taken from the same template, at the same moment, so what
	// it records is exactly what the file beside it was generated from. Taking it
	// later -- from a rerun, or from a project's own values -- would record a
	// guess about where those values came from.
	lock, err := NewLock(template.name)
	if err != nil {
		return Scaffold{}, err
	}
	return Scaffold{
		Bundle:   template.name,
		Config:   ScaffoldFile{Path: FileName, Content: renderScaffoldConfig(effective, template.name, options.Detection)},
		Personas: personas,
		Lock:     ScaffoldFile{Path: LockFileName, Content: lock.Render()},
	}, nil
}

// scaffoldPersonas collects the persona text every agent refers to, at the path
// the rendered configuration refers to it by. Agents that share a persona share
// one file, because two copies of one persona is two things to edit.
//
// It also copies every persona the template ships that no generated agent binds.
// The program manager is the case: init configures no instance of it, because an
// instance is a lane and a remit somebody chooses, and the project that later
// writes one needs the persona there to bind rather than to write by hand.
func scaffoldPersonas(template bundle, effective Config) ([]ScaffoldFile, error) {
	byPath := map[string]string{}
	for _, name := range sortedNames(effective.Agents) {
		persona := effective.Agents[name].Persona
		if !persona.Defined() {
			continue
		}
		existing, seen := byPath[persona.Path]
		if seen && existing != persona.Text {
			return nil, fmt.Errorf("persona %q has two different texts in the template", persona.Path)
		}
		byPath[persona.Path] = persona.Text
	}
	unbound, err := template.unboundPersonas(effective)
	if err != nil {
		return nil, err
	}
	for _, personaPath := range unbound {
		text, _, err := template.personas.load("persona", personaPath)
		if err != nil {
			return nil, err
		}
		byPath[personaPath] = text
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	files := make([]ScaffoldFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, ScaffoldFile{Path: path, Content: []byte(byPath[path])})
	}
	return files, nil
}

// renderScaffoldConfig writes the configuration as a file meant to be read and
// edited rather than only parsed: every value the harness uses, in a stable
// order, with the reasoning an operator needs to change one safely.
func renderScaffoldConfig(effective Config, bundleName string, detection Detection) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, `# Yoyodyne project configuration.
#
# This file is complete. Every value the harness uses is written here, so what
# the file says is what runs and there is nothing to look up somewhere else.
# It was generated from the %s template inside the executable, and
# the personas beside it were copied from there at the same time; both are this
# project's to edit now.
#
# That ownership has a price worth knowing up front: a later Yoyodyne that
# improves a persona or corrects a model selector does not change this project,
# because nothing here is inherited. What it does instead is tell you. The
# config.lock beside this file records what the template supplied when this was
# generated, "yoyo doctor" and "yoyo config validate" say so on any run where
# something you never edited has improved, and "yoyo config drift" shows what
# each value was and is so you can take the ones you want.
#
# "yoyo config show --effective --origins" reports the resolved values and
# where each one came from.
version: %d

product:
  id: %s
  repository: %s
`, bundleName, effective.Version, effective.Product.ID, effective.Product.Repository)

	// The product block is written directly under the version, ahead of
	// everything else, because what it names is what every role reads as the
	// product's intent. The comment above the specifications directory is one the
	// template tracks, so a project carrying an earlier wording of it is told by
	// `yoyo config drift`; see comments.go.
	builder.WriteString(renderSpecificationsComment())
	fmt.Fprintf(&builder, `  specifications: %s
  # The architect's durable architectural invariants: one Markdown file per
  # constraint, named by its id. The harness delivers the ones relevant to a
  # work item into the developer's context and the reviewer's evidence. A
  # project with no such directory simply has no invariants yet.
  invariants: %s
  # The architect's two artifact homes: designs and specifications, and the
  # decision records the invariants above are extracted from. Every Markdown
  # file in these directories and in the specifications directory carries its
  # identity and metadata in frontmatter -- id, kind, status, what it supports,
  # and a revision log -- so downstream work can refer to one durably. A
  # directory that does not exist simply records no artifacts of that kind yet.
  designs: %s
  decisions: %s

execution:
  max_concurrent_developers: %d
  # Each unit of that capacity is a developer slot. A slot may prefer a label --
  # the tracker's own labels, which the Lead Product Manager and the development
  # manager put on work items -- and then pulls that label's ready work first,
  # wherever it sits in the order, and the rest of the backlog only when none of
  # it is ready. One entry per slot, in slot order; the list may be shorter than
  # the capacity, and the slots it does not name prefer nothing. The example
  # dedicates the first slot to a "reliability" label -- one a project might
  # put on bugs, on anything that keeps the system from stalling, and on
  # anything that keeps it from making mistakes, so that work is never queued
  # behind features. Delete the leading "# " to give slot 1 that preference,
  # or name a label of your own.
  # developer_slots:
  #   - prefer: [reliability]   # developer slot 1 pulls reliability-labelled work first
  repair_attempts_before_replan: %d
  # A promotion that loses a race -- to another run, or to whoever moved the
  # target branch while this one was working -- is replayed onto where the
  # target went and tried again. Every replay re-runs the checks and asks for a
  # fresh independent review, because the change that would now be promoted is
  # not the one that was approved. Losing the race costs nothing and never stops
  # a run: this bounds the replays that stop on the change -- one that fails its
  # checks or draws a repair verdict -- and the one past it stops the run there;
  # at 0 no replay may stop on the change. A replay that conflicts is never
  # resolved automatically: it stops the run and is left for a person.
  integration_retries_before_reconciliation: %d
  # A provider invocation that dies without judging the work -- an API error its
  # own retries did not outlast, or a response cut off mid-flight -- is reissued
  # in the same worktree and the same session, up to this many times, rather than
  # failing the run. Nothing here is a fault in the change, so a relaunch spends
  # no repair attempt. A run that spends this budget stops and records a blocker.
  transient_relaunches_before_blocking: %d
  worktree_root: %s
  # The remote publishing opens pull requests against, and pushes run branches
  # to unless push_remote names another. It is only consulted when
  # approvals.publishing is automatic; a repository with no remote by this name
  # publishes nothing and stays purely local.
  remote: %s
  # Set push_remote to your fork's remote when you cannot push to the remote
  # above. Run branches then go to the fork and pull requests are opened across
  # the two, which is how a contributor without push access publishes at all.
  # Leaving it out pushes run branches to the remote above.
  # push_remote: fork
  # A provider usage limit that is exhausted pauses a run rather than failing
  # it. The maximum covers the five-hour limit and deliberately stops short of
  # the seven-day one, so a capacity problem that needs a person reaches one
  # instead of a timer. The in-process bound is how much of that a run spends
  # sleeping here rather than exiting with its deadline recorded. The unknown
  # reset pause is the interval between probes, whether or not the provider
  # named a reset: a quoted reset bounds the wait rather than gating it, because
  # capacity can be bought and windows can roll before the quoted edge.
  usage_limit_max_pause: %s
  usage_limit_in_process_pause: %s
  usage_limit_unknown_reset_pause: %s
  # A provider whose own servers are transiently overloaded refuses without
  # naming any reset at all, so a run waits this much shorter interval and asks
  # again. It spends the same maximum above, so an overload that never lifts
  # reaches that bound rather than reissuing forever.
  server_overload_pause: %s
  # The total budget one check below gets: the whole time it may run, not the
  # time it may stay quiet. Raise it as the suite grows, and raise it again for
  # concurrency -- N runs at once multiply the wall clock of every suite without
  # multiplying the cores it runs on, so either this scales with
  # max_concurrent_developers or the checks are left to serialize themselves.
  check_timeout: %s
  # The budget the whole check stage of one run gets -- every check below
  # together, from the first starting to the last ending. A stage that reaches
  # it ends as a stoppage naming the bound and the check it stopped, and "yoyo
  # status" shows how much of it a run has spent while its checks run. Keep it
  # in minutes: a stage that can take hours holds a developer seat for hours.
  check_stage_timeout: %s
  # The budget each landing check gets -- the list under "landing_checks" below,
  # run once per landing over the integrated commit. It is its own budget with
  # no stage bound, because what goes there is the suite too long for the gate.
  landing_check_timeout: %s
  # "yoyo work --watch" stays open instead of returning when the queue is empty,
  # and this is how long it waits before reading the queue again. Nothing is
  # cached between readings, so this is also the delay on work you admit or
  # reorder while it is running. An idle watch costs one local tracker read per
  # interval and asks no provider anything.
  work_poll: %s
  # A watching session that finds a build deployed over it waits out the runs it
  # hosts before restarting into the new build -- for at most this long. Past it
  # the session restarts anyway and the hosted runs are stopped and preserved
  # for the session that comes back to re-adopt, with every counter as it was.
  # The session keeps pulling into free seats and firing its recurring tasks
  # while it drains; only the runs it hosts are what the drain is about.
  redeploy_drain_limit: %s
  # No work pulled and no recurring pass succeeding for this long is a factory
  # stall. The supervisor files it as a critical report once when it begins,
  # files a note when passes succeed or work is pulled again, and "yoyo status"
  # names it while it stands.
  factory_stall_after: %s
  # Replace a recurring role conversation after this many missing closing reports.
  missing_reports_before_fresh_conversation: %d
  # The failure-storm brake for a session left running unattended: this many runs
  # blocking in a row, with nothing landing between them, holds intake and
  # summons the development manager at once to decide what happens to it. It is
  # aimed at a broken machine rather than a broken item -- an item that keeps
  # failing is left alone until something about it changes, and a stop the
  # environment made counts toward nothing -- and "0" turns it off, leaving you
  # as the only thing that holds intake.
  blocked_runs_before_intake_hold: %d
  # How long a tripped brake waits for her decision before it probes the line by
  # itself: one run under the hold, whose landing reopens intake and whose
  # blocking keeps it held and asks her again. A hold she escalates to you
  # waits on a person; so does one the harness escalates itself, below.
  brake_cooldown: %s
  # How many of those summons-and-probe cycles the harness goes round before it
  # escalates the hold to you itself -- one direct message naming the cycles
  # spent and what stopped the last probe, and no further probe until somebody
  # releases it. The default is two hours at the default cooldown. "0" never
  # escalates on its own, and the loop stands until she does or the line mends.
  brake_escalation_cycles: %d
  # Every new run compiles the built-in delivery definition and records where it
  # sent the run, beside the run's own record. The delivery is the same delivery
  # either way -- the definition's steps perform nothing -- so what this buys is
  # the account. Setting it to "false" is the rollback to the legacy path, and it
  # reaches new runs only: a run already in flight finishes on whatever it
  # started on.
  declarative_delivery: %t

# When work that has stopped moving is looked at, and what looking at it may
# spend. An approved publication nobody has merged is docketed once it has sat
# this long -- an age rather than a deadline, because what makes it stuck is
# that nothing happened to it. The rounds cap is the total review rounds one
# work item may accumulate before triage stops handing it back for repair;
# past it triage may still escalate the item, re-scope it, or cross the cap on
# a recorded reason, and "0" means an item that reaches triage is never
# repaired again without a crossing.
#
# There is a third setting, "repair_grant_attempts": how many repair attempts
# triage hands an item worth another go. It is left out deliberately, the way
# the repository id above is, because unstated it follows
# execution.repair_attempts_before_replan and keeps following it when you change
# that. State it here to size a grant differently; it may not be zero, since a
# grant of nothing leaves the item exactly where it was.
triage:
  stuck_merge_age: %s
  review_rounds_cap: %d

# How long one role may go on asking another one something. Roles can put a
# question to each other through the harness -- the Lead Product Manager asking
# the architect what a goal costs, the architect asking the Lead Product Manager
# whether a trade-off is one a user would accept -- and every exchange is
# recorded where you can read it. This is the hard limit on rounds in one
# thread. Reaching it closes the exchange as unresolved and tells you about it,
# because two judgement models can defer to each other politely for ever and the
# only thing that ends that is a number. It may not be zero: the way to leave
# the channel unused is to leave it unused.
exchange:
  max_rounds: %d

# How far behind the target branch a management conversation's picture of the
# repository may fall before the harness re-reads it. The Lead Product Manager,
# the architect, and the development manager are briefed once, when a
# conversation opens, and every later turn resumes a session that already holds
# that briefing; before each reply the harness counts the landings on the target
# branch since it was taken and, past this many, re-reads the repository and the
# tracker before answering. Where the re-read cannot be made the reply says in
# its own text how many landings old its picture is. This times the re-read and
# does not switch it off: it may not be zero, and it may not be more than %d.
conversation:
  refresh_after_landings: %d

# What you approve, and what runs without asking. The brief and the goals are
# "human" deliberately: they are what you state, and everything else traces back
# to them. "yoyo artifact approve <id>" records your approval in the document's
# frontmatter, against the revision it was given for, so a document amended
# afterwards reads as approved-and-amended-since rather than as approved. An
# unapproved document still loads and still governs what is downstream of it;
# what your approval of the goals decides is what reaches the work queue, under
# work_items below. Designs are "automatic" because a design serving an approved
# goal is the architect's judgement about how.
#
# work_items is the one approval that gates rather than records, and it is where
# you say how much of this you want to watch. "human" puts every work item to
# you before it is admitted to the queue, which is what you get until you say
# otherwise; it refuses the Lead Product Manager's direct "create" as well as
# the automatic admission, so the work is proposed to you instead of arriving
# through a door the setting left open. "automatic" moves that approval up to
# your goals: work that traces to a goal you approved is then admitted without
# asking you, and you are told afterwards what went in. Work that traces to no
# goal, work that would cut against one, and a change to the goals themselves
# still stop and ask either way. Turning it on gets you a second ramp for
# free -- nothing is admitted without asking until you have actually approved
# a goal, so it still asks about everything until your first
# "yoyo artifact approve".
#
# Integration and publishing are opted in to separately. Automatic integration is
# refused unless it is actually gated by the checks below and a reviewer agent,
# and publishing is what pushes a branch and opens a pull request where other
# people can see it.
approvals:
  brief: %s
  goals: %s
  designs: %s
  work_items: %s
  integration: %s
  publishing: %s
`,
		effective.Product.Specifications,
		effective.Product.Invariants,
		effective.Product.Designs,
		effective.Product.Decisions,
		effective.Execution.MaxConcurrentDevelopers,
		effective.Execution.RepairAttemptsBeforeReplan,
		effective.Execution.IntegrationRetriesBeforeReconciliation,
		effective.Execution.TransientRelaunchesBeforeBlocking,
		effective.Execution.WorktreeRoot,
		effective.Execution.Remote,
		renderScaffoldDuration(effective.Execution.UsageLimitMaxPause),
		renderScaffoldDuration(effective.Execution.UsageLimitInProcessPause),
		renderScaffoldDuration(effective.Execution.UsageLimitUnknownResetPause),
		renderScaffoldDuration(effective.Execution.ServerOverloadPause),
		renderScaffoldDuration(effective.Execution.CheckTimeout),
		renderScaffoldDuration(effective.Execution.CheckStageTimeout),
		renderScaffoldDuration(effective.Execution.LandingCheckTimeout),
		renderScaffoldDuration(effective.Execution.WorkPoll),
		renderScaffoldDuration(effective.Execution.RedeployDrainLimit),
		renderScaffoldDuration(effective.Execution.FactoryStallAfter),
		effective.Execution.MissingReportLimit(),
		effective.Execution.BlockedRunsBeforeIntakeHold,
		renderScaffoldDuration(effective.Execution.BrakeCooldown),
		effective.Execution.BrakeEscalationCycles,
		effective.Execution.DeclarativeDelivery,
		renderScaffoldDuration(effective.Triage.StuckMergeAge),
		effective.Triage.ReviewRoundsCap,
		effective.Exchange.MaxRounds,
		MaxRefreshAfterLandings,
		effective.Conversation.RefreshAfterLandings,
		effective.Approvals.Brief,
		effective.Approvals.Goals,
		effective.Approvals.Designs,
		effective.Approvals.WorkItems,
		effective.Approvals.Integration,
		effective.Approvals.Publishing,
	)

	renderScaffoldReporting(&builder)

	renderScaffoldServices(&builder, effective.Services)

	renderScaffoldRecurring(&builder)

	renderScaffoldChecks(&builder, detection)

	renderScaffoldAccounts(&builder, effective)

	builder.WriteString(`
# Every agent is stated in full: nothing about them is inherited. A model
# selector that names a family, such as "opus", floats to that family's current
# default; an exact identifier such as claude-opus-5 pins a version. Persona
# paths are relative to the directory this file is in and must be Markdown
# inside it.
# Each agent names the account it runs under, from the mapping above. Delete an
# agent to remove it, edit one to change it.
agents:
`)
	for _, name := range sortedNames(effective.Agents) {
		renderScaffoldAgent(&builder, name, effective.Agents[name])
	}
	return []byte(builder.String())
}

// renderScaffoldDuration writes a duration the way an operator would type it.
// Go's own rendering spells six hours "6h0m0s", and a file people are meant to
// edit should not teach them a longer spelling than the one it accepts.
func renderScaffoldDuration(duration Duration) string {
	text := duration.String()
	switch {
	case strings.HasSuffix(text, "h0m0s"):
		return strings.TrimSuffix(text, "0m0s")
	case strings.HasSuffix(text, "m0s"):
		return strings.TrimSuffix(text, "0s")
	default:
		return text
	}
}

// The headings a generated configuration puts over commented commands. They are
// deliberately unmissable and deliberately stable, and they are three rather
// than one because they ask three different things: a demand to choose belongs
// only where a run cannot happen until somebody does, and putting it anywhere
// else teaches an operator to skip past it.
const (
	// CandidateMarker heads candidates when nothing was written into "checks":
	// a run is refused until one of them is chosen, so a decision is owed now.
	CandidateMarker = "# YOU MUST CHOOSE"
	// UndecidedMarker heads the same candidates when a checks list was written
	// anyway. The question is still open, but the file already runs, so nothing
	// waits on the answer.
	UndecidedMarker = "# ALSO FOUND, AND NOT DECIDED"
	// AlternativeMarker heads commands detection read and decided against,
	// because what it wrote already covers them. Nothing is owed here at all;
	// they are shown so an operator can swap one in.
	AlternativeMarker = "# ALSO FOUND, AND NOT NEEDED"
)

// checksGuide points at the per-language examples and the reasoning behind them,
// for a project that has no checkout of Yoyodyne to look them up in.
const checksGuide = "https://github.com/mason-bryant/yoyodyne/blob/main/docs/configuration.md#checks"

// slackGuide points at the whole Slack recipe -- the app, the invitation, and
// where the two tokens go -- which is the part of it that does not happen in
// this file. It is a URL for the reason checksGuide is one: a generated project
// has no checkout of Yoyodyne to read docs/slack/setup.md out of.
const slackGuide = "https://github.com/mason-bryant/yoyodyne/blob/main/docs/slack/setup.md"

// renderScaffoldReporting writes the two optional top-level sections a project
// is otherwise given no sign of: where it reports, and which humans it
// recognizes. Both are written commented out, because both are off by default
// and a project that says nothing about either reports nothing and recognizes
// nobody -- which stays the default, since uncommenting is the whole gesture
// asked for here.
//
// They are written at all because the alternative is what an operator following
// the setup recipe actually met: a step saying to add a section to a file that
// gives no hint of its shape, or that the capability exists. Every line below
// is written so that deleting its leading "# " leaves a valid entry, the way
// the commented checks further down are.
func renderScaffoldReporting(builder *strings.Builder) {
	fmt.Fprintf(builder, `
# Reporting into Slack, which is optional and off. A project that says nothing
# about it reports nothing -- which is every project until it opts in -- and an
# installation without reporting runs work exactly as one with it does. Switched
# on, it is one thread per work item and one message per milestone, posted by a
# separate "yoyo slack" process that holds this project's two tokens. No run and
# no agent ever holds one, which is why no token belongs in this file. Uncomment
# the block below and name this workspace's channel, by id from the channel's
# About panel or by #name; an id is worth preferring because a rename does not
# break it. "yoyo setup" writes these two values for you if you would rather.
# Creating the app, inviting it, and storing its tokens are the rest of it:
# %s
#
# slack:
#   enabled: true
#   channel: C0123456789

# The humans this project recognizes, and what each of them may do. A project
# that names nobody -- which is every project until it names somebody -- is
# closed rather than open: it recognizes nobody, not everybody. An entry binds
# one person's identifier namespaces, because an act carries an identifier and
# never a person -- a commit carries an address, a push carries a forge account,
# a thread reply carries a member id -- and binding them together is what lets an
# authority check resolve whichever one arrived to the same human. "own-intent"
# is authority over what the product is for, and at most one human may hold it;
# "direct-work" is authority to steer work already in flight, including the
# thread replies the Slack sink records as directives. Who may steer from Slack
# is derived from this rather than authored beside it -- the humans granted
# direct-work who have bound a member id, and nobody else -- so until somebody is
# named here, every reply in a thread is answered saying it was not acted on.
#
# operators:
#   your-name:
#     git_email: you@example.com
#     forge_account: your-account
#     slack_member_id: U0123456789   # your Slack profile -> "Copy member ID"
#     grants:
#       - own-intent
#       - direct-work
`, slackGuide)
}

// renderScaffoldServices writes the parts of the product, every one of them
// present and at its default, live rather than commented: the section is the
// product's shape rather than an option, and a file that showed only the parts
// that happened to be on would be a file whose reader could not see what else
// there is. Which parts are on is the one thing here worth deciding, and the
// comment says why each default is what it is.
func renderScaffoldServices(builder *strings.Builder, services Services) {
	fmt.Fprintf(builder, `
# The parts of this product, and whether each one runs. Slack, the dashboard,
# the scheduler, and the maintenance pass are declared here so that a person
# starts the product once and its enabled parts start and stop together, rather
# than each being a separate tool started by hand. Every part is listed whether
# or not it is on; this is the product's shape, and "enabled" is the decision.
#
# The scheduler and the maintenance pass are on, because they need nothing that
# is not already in this file -- they are the harness's own loop and its
# self-maintenance, and a product started with neither starts nothing. Slack and
# the dashboard are off, because each needs something arranged outside it first:
# Slack needs the "slack" section above switched on and this project's two
# tokens stored, and the dashboard needs a port you mean to open. Nothing here
# widens what any part may do; a part started from this section holds exactly
# what it holds when started by hand.
#
# The dashboard binds loopback and generates its token at each start, printed
# once where you can read it; set token to "keychain" or "file" and it reads
# the stored one instead, which outlives a restart and is never printed.
# Binding an interface address instead lets another device on your network
# open the page, and is an opt-in with a condition: a token printed to one
# terminal is unusable from another device, so a bind outside loopback has to
# name where its token is stored -- "keychain" or "file", the two stores the
# Slack tokens use, under names that carry the product -- and is refused at
# load until it does. allowed_hosts are the names, beyond the bound address, a
# request may carry as its Host; write them without a port. The token itself
# is never written in this file.
#
# The maintenance pass is the supervisor's own: every "every", it runs
# yoyo reconcile, which settles what interrupted runs left behind and catches
# the checkout up, and answers the restart requests program managers made. It
# restarts nothing while the provider cannot be reached or is not logged in,
# and each pass is recorded where "yoyo sweeps" reads it.
services:
  slack:
    enabled: %t
  dashboard:
    enabled: %t
    port: %d
    bind: %s
    allowed_hosts: %s
    token: %s
  scheduler:
    enabled: %t
  maintenance:
    enabled: %t
    every: %s
`,
		services.Slack.Enabled,
		services.Dashboard.Enabled,
		services.Dashboard.Port,
		services.Dashboard.Bind,
		renderScaffoldList(services.Dashboard.AllowedHosts),
		services.Dashboard.Token,
		services.Scheduler.Enabled,
		services.Maintenance.Enabled,
		renderScaffoldDuration(services.Maintenance.Every),
	)
}

// renderScaffoldList writes a list of plain words inline, which for the empty
// list a generated file carries is `[]` -- the spelling the checks placeholder
// already teaches, and one the operator replaces whole.
func renderScaffoldList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	return "[" + strings.Join(values, ", ") + "]"
}

// renderScaffoldRecurring writes the recurring-task section, commented out and
// off, for the reason the two sections above are written that way: a project
// given no sign of the shape is one whose operator has to be told the schema by
// somebody, and uncommenting is the whole gesture asked for.
//
// The examples are the standing loops this harness actually needs rather than
// made-up ones: the development manager's sweep over work that has stopped
// moving, the product manager's pass over the collected reports, her sweep over
// coherence, releases, and the work closed since its last pass, and the
// architect's passes over what waits on her and the changes proposed to her
// documents. The sweep's third job is the audit of closed work against the
// standing goals: the reviewer judges each change against them before it lands,
// and a goal enforced only where the reviewer happened to look is not enforced,
// so the harness hands the sweep what closed and it admits a correction for a
// violation. The pass over the reports is here because the pile has no other
// standing reader — every role files into it, only the product manager can
// record what became of a report, and a project that schedules nothing works
// the pile only when somebody opens a conversation. That is not a hypothetical:
// this project reached 564 unhandled reports with the oldest three weeks old
// before the pass existed to be configured. The pass over the proposals is here
// for the same shape of reason: proposals against the designs reach the
// architect only when somebody opens her conversation, and this project stood
// at forty-four undecided with the oldest weeks old before the pass existed.
//
// They are still commented out and still off, because what is woken and how
// often is the project's decision and not this file's. Every line is written so
// that deleting its leading "# " leaves a valid entry, and a test in this package
// uncomments the block and loads every task in it to keep that true.
//
// What is deliberately absent from the schema is said in the comment rather than
// left to be discovered: there is no key here for a capability, a tool, or an
// authority of any kind. A role woken on a cadence holds exactly what its role
// already holds, and an operator who reads this file has to be able to see that
// the schedule is not where that is decided.
func renderScaffoldRecurring(builder *strings.Builder) {
	builder.WriteString(`
# Work the harness does on a schedule rather than because something happened.
# Optional and off: a project that schedules nothing runs exactly as it did
# before this existed. Each entry names a role to wake, how often, and what to
# say to it -- and nothing else. There is no key here for a capability, a tool,
# an account, or an authority: a role woken on a schedule holds exactly what its
# role already holds, and a scheduled turn is authorized identically to a
# conversation you open by hand.
#
# A firing is conversation turns and costs what conversation turns cost, so the
# interval is the spending decision: "1h" is a turn an hour for as long as the
# session watches. Passes of different roles are taken side by side, beside the
# work the session pulls rather than in its way, and a pass with more to do
# than one turn holds says so and is given another, up to max_turns. Reports are
# durable -- "yoyo sweeps" reads them -- and "yoyo pause" stops firings exactly
# as it stops everything else the harness spends.
#
# recurring_tasks:
#   development-manager-sweep:
#     role: development-manager
#     every: 1h
#     enabled: true
#     max_turns: 4
#     prompt: |
#       Apply the standing goals to everything you write and every decision
#       you make, whichever goal the work item or your lane serves. Read
#       the standing set in the goals documents' Standing goals section
#       under product.specifications, the configured product specification
#       home delivered as authoritative product intent (docs/product by
#       default). In Yoyodyne, docs/product/goals/v1-goals.md names the
#       plain-language and autonomy goals as that set. An output or
#       decision that breaks a standing goal is a defect to report: name
#       the goal and where it was broken.
#       Sweep for unresolved issues: stoppages nobody has decided, claims on
#       work nothing is running, deliveries that have stopped moving, anything
#       waiting on a decision your own recorded authority covers. Batch broad
#       classes rather than working item by item.
#       Fix what your authority allows. Ask the architect where a ruling is
#       needed rather than deciding it yourself. File root-cause work with the
#       Lead Product Manager for every fix you make -- a repair that leaves the
#       cause in place is a repair you will make again next hour.
#       When the harness is healthy this finds nothing, and that is the report.
#       A sweep that keeps finding things is itself the signal: say so.
#       Then check everything that appears to wait on the operator: the
#       needs-a-human entries waiting on him, which the pass carries beside
#       the docket with how long each has waited. Only a change to the
#       fundamental goals is truly his. Settle each other one if it is yours,
#       send it to the role that owns it if it is not, and file a defect with
#       the Lead Product Manager saying why it reached him. Record what you did
#       on the record the entry is about.
#       Name every work item by what it is, with its identifier after it:
#       "retiring the maintenance job (434.9)", never "434.9" on its own. An
#       identifier alone is a defect -- nobody reading later knows the item.
#       Write anything a person reads in ordinary words, and say what
#       happened, not the harness's category or mechanism for it: "the AI
#       session running the developer produced no output for five minutes,
#       so the harness ended the run; the cause was outside the work, so no
#       repair attempt was spent and the change was kept." Coin no terms,
#       and do not pass on the words the harness uses for itself: if a
#       person would have to look a word up, write the plain words it
#       stands for. Give every time in the operator's local time with the
#       zone named, such as 08:20 PDT, not UTC.
#       A decision your authority covers is yours: make it, and report it
#       afterwards. Never ask the operator to approve it; an approval routed to
#       them is a defect to report. Only a change of fundamental intent is
#       theirs -- one after which the goals would admit any work they refused
#       before, or refuse any work they admitted.
#   report-triage:
#     role: product-manager
#     every: 1h
#     enabled: true
#     max_turns: 4
#     prompt: |
#       Apply the standing goals to everything you write and every decision
#       you make, whichever goal the work item or your lane serves. Read
#       the standing set in the goals documents' Standing goals section
#       under product.specifications, the configured product specification
#       home delivered as authoritative product intent (docs/product by
#       default). In Yoyodyne, docs/product/goals/v1-goals.md names the
#       plain-language and autonomy goals as that set. An output or
#       decision that breaks a standing goal is a defect to report: name
#       the goal and where it was broken.
#       Work the collected reports. Every role files what it noticed into one
#       pile and you are the only role that can record what became of one, so
#       a pile nobody wakes you for is a pile nothing drains.
#       The unhandled ones are carried into this turn already, oldest first
#       with anything critical ahead of them, resuming where the last turn
#       stopped. Decide about every one you are shown and record each decision
#       with the "handle" action -- work to admit, a proposal to make, a
#       question to raise, or that it needs nothing. Check anything you would
#       admit against the work already admitted first.
#       Say in your summary how many you decided and how many are still behind
#       them, and keep the findings for what was worth more than a handling.
#       A pass with more of the pile than one turn holds says so and takes
#       another. When nothing is unhandled, that is the report.
#       Name every work item by what it is, with its identifier after it:
#       "retiring the maintenance job (434.9)", never "434.9" on its own. An
#       identifier alone is a defect -- nobody reading later knows the item.
#       Write anything a person reads in ordinary words, and say what
#       happened, not the harness's category or mechanism for it: "the AI
#       session running the developer produced no output for five minutes,
#       so the harness ended the run; the cause was outside the work, so no
#       repair attempt was spent and the change was kept." Coin no terms,
#       and do not pass on the words the harness uses for itself: if a
#       person would have to look a word up, write the plain words it
#       stands for. Give every time in the operator's local time with the
#       zone named, such as 08:20 PDT, not UTC.
#       A decision your authority covers is yours: make it, and report it
#       afterwards. Never ask the operator to approve it; an approval routed to
#       them is a defect to report. Only a change of fundamental intent is
#       theirs -- one after which the goals would admit any work they refused
#       before, or refuse any work they admitted.
#   product-manager-sweep:
#     role: product-manager
#     every: 12h
#     enabled: true
#     max_turns: 6
#     prompt: |
#       Apply the standing goals to everything you write and every decision
#       you make, whichever goal the work item or your lane serves. Read
#       the standing set in the goals documents' Standing goals section
#       under product.specifications, the configured product specification
#       home delivered as authoritative product intent (docs/product by
#       default). In Yoyodyne, docs/product/goals/v1-goals.md names the
#       plain-language and autonomy goals as that set. An output or
#       decision that breaks a standing goal is a defect to report: name
#       the goal and where it was broken.
#       Three jobs this pass.
#       First, read across the goals, the designs, the decisions, and the
#       admitted work, and look for places where they disagree: a goal
#       nothing serves, two items that are the same work, an item citing
#       wording a document no longer states, a design ruling that never
#       landed, work that contradicts a decision already recorded. Fix what
#       your own authority covers and elevate the rest.
#       Second, decide whether a release should be cut: worth it when a
#       substantial set of features has landed since the last one, not when
#       what landed is small or one unfinished thread. Never more than one a
#       day. When the answer is yes, admit the release item in this pass.
#       Third, audit the work closed since your last pass, which the pass
#       carries with what landed for each, against every standing goal.
#       Record each item in your block's audits: the goals you checked and
#       what you found. For an item that breaks one, admit a correction at
#       priority 0 naming the closed items it corrects in "corrects", or
#       widen the open correction the duplicate check names. At most three
#       corrections a pass, taking the violation shared by the most closed
#       items first; mark the rest deferred. Where a program manager whose
#       lane is a standing goal already audited an item, the pass carries
#       its findings: read them, and do not audit that item against that
#       goal again.
#       End with a durable report. Put any questions for the operator at
#       the top; when nothing needs them, say so and stop.
#       Name every work item by what it is, with its identifier after it:
#       "retiring the maintenance job (434.9)", never "434.9" on its own. An
#       identifier alone is a defect -- nobody reading later knows the item.
#       Write anything a person reads in ordinary words, and say what
#       happened, not the harness's category or mechanism for it: "the AI
#       session running the developer produced no output for five minutes,
#       so the harness ended the run; the cause was outside the work, so no
#       repair attempt was spent and the change was kept." Coin no terms,
#       and do not pass on the words the harness uses for itself: if a
#       person would have to look a word up, write the plain words it
#       stands for. Give every time in the operator's local time with the
#       zone named, such as 08:20 PDT, not UTC.
#       A decision your authority covers is yours: make it, and report it
#       afterwards. Never ask the operator to approve it; an approval routed to
#       them is a defect to report. Only a change of fundamental intent is
#       theirs -- one after which the goals would admit any work they refused
#       before, or refuse any work they admitted.
#   architect-pass:
#     role: architect
#     every: 1h
#     enabled: true
#     max_turns: 4
#     prompt: |
#       Apply the standing goals to everything you write and every decision
#       you make, whichever goal the work item or your lane serves. Read
#       the standing set in the goals documents' Standing goals section
#       under product.specifications, the configured product specification
#       home delivered as authoritative product intent (docs/product by
#       default). In Yoyodyne, docs/product/goals/v1-goals.md names the
#       plain-language and autonomy goals as that set. An output or
#       decision that breaks a standing goal is a defect to report: name
#       the goal and where it was broken.
#       Your recurring pass over what is waiting on you. Survey the work
#       carried in your conversation and the undecided proposals to your
#       documents. Take work by priority first, then age within each priority.
#       Rule on as many as this pass can do properly: read each item or
#       proposal in full and the documents it rests on before deciding.
#       End with a durable report: what you ruled, what needs landing and
#       where, what is still waiting, and any question for the operator.
#       When nothing is waiting, say NOTHING WAITING and stop.
#       Name every work item by what it is, with its identifier after it:
#       "retiring the maintenance job (434.9)", never "434.9" on its own. An
#       identifier alone is a defect -- nobody reading later knows the item.
#       Write anything a person reads in ordinary words, and say what
#       happened, not the harness's category or mechanism for it: "the AI
#       session running the developer produced no output for five minutes,
#       so the harness ended the run; the cause was outside the work, so no
#       repair attempt was spent and the change was kept." Coin no terms,
#       and do not pass on the words the harness uses for itself: if a
#       person would have to look a word up, write the plain words it
#       stands for. Give every time in the operator's local time with the
#       zone named, such as 08:20 PDT, not UTC.
#       A decision your authority covers is yours: make it, and report it
#       afterwards. Never ask the operator to approve it; an approval routed to
#       them is a defect to report. Only a change of fundamental intent is
#       theirs -- one after which the goals would admit any work they refused
#       before, or refuse any work they admitted.
#   architect-amendments:
#     role: architect
#     every: 6h
#     enabled: true
#     max_turns: 4
#     prompt: |
#       Apply the standing goals to everything you write and every decision
#       you make, whichever goal the work item or your lane serves. Read
#       the standing set in the goals documents' Standing goals section
#       under product.specifications, the configured product specification
#       home delivered as authoritative product intent (docs/product by
#       default). In Yoyodyne, docs/product/goals/v1-goals.md names the
#       plain-language and autonomy goals as that set. An output or
#       decision that breaks a standing goal is a defect to report: name
#       the goal and where it was broken.
#       Work the changes other roles have proposed to your documents. The
#       undecided ones are carried into this turn already, oldest first and at
#       most ten a pass, and nothing else wakes you to argue them, so a queue
#       nobody wakes you for is a queue nothing drains.
#       Argue every one you are shown: read the document it names, and
#       recommend approve, decline, or merge with another, with the reason, in
#       the "recommendations" of your block. You decide nothing and edit
#       nothing here -- the operator records each decision under your
#       authority, and an approved change is then yours to make as a revision.
#       Say in your summary how many you argued and how many wait behind them.
#       When nothing is undecided, that is the report.
#       Name every work item by what it is, with its identifier after it:
#       "retiring the maintenance job (434.9)", never "434.9" on its own. An
#       identifier alone is a defect -- nobody reading later knows the item.
#       Write anything a person reads in ordinary words, and say what
#       happened, not the harness's category or mechanism for it: "the AI
#       session running the developer produced no output for five minutes,
#       so the harness ended the run; the cause was outside the work, so no
#       repair attempt was spent and the change was kept." Coin no terms,
#       and do not pass on the words the harness uses for itself: if a
#       person would have to look a word up, write the plain words it
#       stands for. Give every time in the operator's local time with the
#       zone named, such as 08:20 PDT, not UTC.
`)
}

// renderScaffoldChecks writes the checks section: what detection proposed, where
// each proposal came from, and what it could not decide. A proposal is only
// worth having if the reader can see what it was derived from, so provenance is
// written beside the commands rather than left to be reconstructed.
func renderScaffoldChecks(builder *strings.Builder, detection Detection) {
	builder.WriteString(`
# Checks are this project's own. Each entry is run through "/bin/sh -c" in the
# run's worktree, so shell syntax works. A check must be non-interactive and
# must exit non-zero on failure -- a run stops at a failing check and never
# reaches review or integration. A run with no checks at all is refused.
`)
	if len(detection.Checks) > 0 {
		fmt.Fprintf(builder, `#
# "yoyo init" proposed the list below from files this project already has, named
# against each entry. It executed nothing to find them: they are what this
# repository announces about itself rather than what it is known to need. Read
# them before the first run and edit or delete whatever does not belong. The
# configuration guide has per-language examples and the reasoning behind them:
# %s
checks:
`, checksGuide)
		lastSource := ""
		for _, proposal := range detection.Checks {
			if proposal.Source != lastSource {
				fmt.Fprintf(builder, "  # from %s\n", proposal.Source)
				lastSource = proposal.Source
			}
			fmt.Fprintf(builder, "  - %s\n", proposal.Command)
		}
	} else {
		found := `# This list is what makes the file usable rather than merely valid, and "yoyo
# init" found nothing in this project to propose for it. The configuration guide
# has these examples with the reasoning behind them:`
		if len(detection.Candidates) > 0 {
			found = `# This list is what makes the file usable rather than merely valid, and "yoyo
# init" found nothing here it could settle on its own -- what it did find is
# below, marked, and waiting on you. The configuration guide has these examples
# with the reasoning behind them:`
		}
		fmt.Fprintf(builder, `#
%s
# %s
#
#   # Go
#   checks:
#     - go test ./...
#     - go vet ./...
#     - gofmt -l . | (! grep .)
#
#   # TypeScript / Node
#   checks:
#     - npm ci
#     - npx tsc --noEmit
#     - npm test -- --run
#     - npx eslint .
#
#   # Python
#   checks:
#     - python -m pytest -q
#     - python -m ruff check .
#     - python -m mypy .
#
#   # Java (Maven)
#   checks:
#     - mvn --batch-mode --quiet verify
#
#   # Java (Gradle)
#   checks:
#     - ./gradlew --no-daemon check
checks: []
`, found, checksGuide)
	}
	listed := len(detection.Checks) > 0
	renderScaffoldCandidates(builder, detection.Candidates, listed)
	renderScaffoldAlternatives(builder, detection.Alternatives)
	renderScaffoldLandingChecks(builder)
}

// renderScaffoldLandingChecks writes the landing checks commented out: what to
// run once per landing on the target branch, over the integrated commit, after
// a run has integrated. Nothing is proposed for it, because what belongs here is
// the suite too expensive for every attempt -- a race detector, a long
// integration suite -- and detection cannot tell which of a project's commands
// that is. The narrowing variable is named beside it, because the two are
// halves of one arrangement: the per-run gate runs the suite over what the
// change touches, and this runs it whole over what landed.
func renderScaffoldLandingChecks(builder *strings.Builder) {
	builder.WriteString(`
# Landing checks run once per landing on the target branch, over the integrated
# commit, after a run has integrated and closed its item. A failure is reported
# as a red landing that files its own work item; it never blocks the run that
# landed the change. Each check gets landing_check_timeout above and the list
# has no stage bound; a check stopped at its budget leaves the landing
# unverified rather than red. This is where a suite too expensive for every
# attempt goes whole -- a race detector, a long integration suite -- while the per-run gate
# above runs it narrowed: every check is given YOYODYNE_CHANGED_GO_PACKAGES,
# the Go packages the change touches as "./dir" patterns, "./..." where the
# harness cannot narrow, and empty where the change touches no package. Read it
# with the shell's unset-only default, so the same line run outside the harness
# -- by a developer for its own evidence, or by hand -- tests the whole module:
#
#   checks:
#     - 'set -- ${YOYODYNE_CHANGED_GO_PACKAGES-./...}; [ $# -eq 0 ] || go test -race "$@"'
#   landing_checks:
#     - go test -race ./...
landing_checks: []
`)
}

// renderScaffoldCandidates writes what detection found and would not choose
// between. Which heading it writes turns on whether a checks list was written at
// all: with an empty list a run is refused until somebody chooses, and with a
// written one the file already works and the question is merely open. Demanding
// a choice in both cases would make the demand mean nothing in either.
func renderScaffoldCandidates(builder *strings.Builder, candidates []CheckProposal, listed bool) {
	if len(candidates) == 0 {
		return
	}
	if listed {
		builder.WriteString("\n" + UndecidedMarker + ` -- nothing below runs, and the list above stands
# without it. "yoyo init" read these out of this project too and could not tell
# whether or which of them belongs, so it wrote none of them into "checks"
# above. Uncomment what does belong -- delete the leading "#" and nothing else
# -- and delete the rest.
`)
	} else {
		builder.WriteString("\n" + CandidateMarker + ` -- nothing below runs, and the list above is empty, so a
# run is refused until this is settled. "yoyo init" found these and could not
# tell which one is this project's gate, so it wrote none of them into "checks"
# above. Uncomment what belongs here -- delete the leading "#" and nothing else
# -- then delete the rest, or write your own instead. Replace "checks: []"
# above with "checks:" first, so the list can take the entry.
`)
	}
	renderScaffoldCommented(builder, candidates)
}

// renderScaffoldAlternatives writes what detection read and decided against.
// Nothing here is owed an answer: the list above already covers it, and this
// exists so an operator who would rather have one of these can see it and swap.
func renderScaffoldAlternatives(builder *strings.Builder, alternatives []CheckProposal) {
	if len(alternatives) == 0 {
		return
	}
	builder.WriteString("\n" + AlternativeMarker + ` -- nothing below runs, and nothing below has to be
# chosen. "yoyo init" read these out of this project as well and left them out
# for the reason given against each, because what is in "checks" above already
# covers them. Swap one in only if you would rather have it.
`)
	renderScaffoldCommented(builder, alternatives)
}

// renderScaffoldCommented writes commands commented out beneath their
// provenance, grouped by the reason they were not written rather than by
// artifact: one question is one paragraph, however many files went into it.
// Every command line is written so that deleting its leading "#" leaves a valid
// entry of the checks list above, because that is the whole gesture the headings
// above ask for.
func renderScaffoldCommented(builder *strings.Builder, proposals []CheckProposal) {
	for start := 0; start < len(proposals); {
		end := start
		for end < len(proposals) && proposals[end].Reason == proposals[start].Reason {
			end++
		}
		group := proposals[start:end]
		builder.WriteString("#\n")
		header := "#  # from " + strings.Join(ProposalSources(group), ", ")
		if group[0].Reason == "" {
			builder.WriteString(header + "\n")
		} else {
			wrapScaffoldComment(builder, header+" -- ", "#  # ", group[0].Reason)
		}
		for _, proposal := range group {
			fmt.Fprintf(builder, "#  - %s\n", proposal.Command)
		}
		start = end
	}
}

// wrapScaffoldComment writes one comment across as many lines as it takes,
// keeping the generated file inside the width the rest of it is written to.
func wrapScaffoldComment(builder *strings.Builder, first, continued, text string) {
	const width = 79
	line, prefix := first, first
	for _, word := range strings.Fields(text) {
		if len(line) > len(prefix) {
			if len(line)+1+len(word) > width {
				builder.WriteString(line + "\n")
				line, prefix = continued, continued
			} else {
				line += " "
			}
		}
		line += word
	}
	builder.WriteString(line + "\n")
}

// renderScaffoldAccounts writes the provider accounts the agents below run
// under. A new project has one, and it is written out rather than left to the
// harness default for the reason everything else here is: the file states what
// runs.
func renderScaffoldAccounts(builder *strings.Builder, effective Config) {
	builder.WriteString(`
# The provider accounts this project runs agents under, keyed by the alias each
# one is known by here. A new project has one: the agents below are each assigned
# to it, every run records the alias it ran under, and every surface that reports
# a run says it back. The alias is the whole of what an entry is for -- an entry
# never holds a credential, because authentication is the provider's own and
# lives on this machine. Rename the alias to whatever you call this account.
#
# A second entry pools the work: active accounts are round-robined a run at a
# time, a reserved one is served from only when no active account can be, and
# weekly_budget_usd stands an account down once the runs that named it have cost
# that much over seven days. A pooled account has a provider home of its own, so
# an entry whose home is not a Claude Code login says whose it is with
# provider: -- an agent is only ever served by an account that can sign its
# provider in. bin/yoyo-account signs a second account in and prints the entry
# for it; docs/configuration.md#provider-accounts has the rest.
accounts:
`)
	for _, alias := range effective.AccountAliases() {
		description := strings.TrimSpace(effective.Accounts[alias].Description)
		if description == "" {
			// An entry with nothing under it is written as an empty mapping rather
			// than as a bare key: the file is meant to be read and edited, and a key
			// with nothing after the colon reads as something somebody deleted.
			// Describing the account here would be words the harness made up about
			// whose account it is.
			fmt.Fprintf(builder, "  %s: {}\n", alias)
			continue
		}
		fmt.Fprintf(builder, "  %s:\n", alias)
		fmt.Fprintf(builder, "    description: %s\n", description)
	}
}

func renderScaffoldAgent(builder *strings.Builder, name string, agent AgentConfig) {
	fmt.Fprintf(builder, "  %s:\n", name)
	fmt.Fprintf(builder, "    role: %s\n", agent.Role)
	fmt.Fprintf(builder, "    backend: %s\n", agent.Backend)
	fmt.Fprintf(builder, "    model: %s\n", agent.Model)
	if agent.Effort != "" {
		fmt.Fprintf(builder, "    effort: %s\n", agent.Effort)
	}
	fmt.Fprintf(builder, "    account: %s\n", agent.Account)
	fmt.Fprintf(builder, "    instances: %d\n", agent.Instances)
	if !agent.Persona.Defined() {
		return
	}
	builder.WriteString("    persona:\n")
	fmt.Fprintf(builder, "      version: %s\n", agent.Persona.Version)
	fmt.Fprintf(builder, "      path: %s\n", agent.Persona.Path)
}

func sortedNames(agents map[string]AgentConfig) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
