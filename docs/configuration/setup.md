# Writing a project configuration

What `yoyo init` writes, where the harness looks for it, how the layers combine
and what refuses a configuration when it loads, where the harness keeps its
state on the machine, how to keep the configuration and the intent outside the
repository, the parts of the product the supervisor starts, and how to read back
what a project actually runs under. Start here if you are giving a project its
own configuration or changing one it already has.

[The configuration index](../configuration.md) lists the other guides.

## Creating a project configuration

```sh
yoyo init                              # configure the current directory
yoyo init --directory path/to/project  # configure another one
yoyo init --product example            # name the product explicitly
yoyo init --tracker-remote <url>       # sync the tracker somewhere else
yoyo init --tracker-local              # keep the tracker on this machine
yoyo init --force                      # overwrite what is already there
```

`yoyo setup` is the same thing asked rather than typed: it runs `init` for you as
one step of a walk from a binary on PATH to an installation `yoyo doctor` calls
healthy, and it will not touch a configuration that is already there — one that
does not load is handed back with the command to edit it rather than
regenerated. Everything below is what it writes on your behalf.

`init` writes what [Layout](#layout) lists, then loads what it wrote and fails if
the result is not usable. Without `--product`, the product is named after the
directory being configured; a directory name that is not a valid identifier is
refused rather than mangled, and `--product` names one instead. Nothing is overwritten without
`--force`, and a refusal happens before any file is written, so a project is
never left half-configured.

The one thing `init` derives from the project rather than from the template is
`checks`, which it proposes by reading what the repository already declares about
its toolchain. See [What `init` proposes for `checks`](runs.md#what-init-proposes-for-checks)
for what it reads and what it does with an answer it cannot settle. A run with
nothing to verify has no gate to integrate behind, so `yoyo run` refuses one
whatever `init` found; read what it proposed before running work.

`init --json` reports it. `checks` is the list that was written; `detected`
carries every proposal with the artifact it came from, in the three lists the
generated file keeps apart — `checks` written, `candidates` found and not
settled, `alternatives` read and deliberately left out.
### When the repository ignores the configuration

`init`, `yoyo setup`, and `yoyo config validate` each ask Git whether the
configuration they just wrote or just read is matched by an ignore rule, and say
so when it is. Nothing fails: the files are there and valid, the exit code is
what it would have been, and the warning goes to standard error — or, in
`setup`, into the account of the step that wrote the configuration.

`yoyo doctor` asks the same question every time it runs, as the
`configuration-ignored` finding. A rule in the repository's own `.gitignore` is
a warning, and its remedy adds the configuration directory with
`git add --force` and commits it; once committed, the rule no longer applies to
it. A rule local to the checkout is reported without a warning, for the reason
below.

It is worth saying because nothing else announces it. A project whose
`.yoyodyne` is ignored is configured on the machine that ran `init` and nowhere
else — this checkout keeps reading the configuration off disk while every clone,
every collaborator, and every dev worktree, which check out tracked files only,
get a project with no configuration at all. The warning names the rule in Git's
own `<file>:<line>:<pattern>` form, so the line is findable rather than
searchable for.

A configuration that is already tracked is not ignored however loudly a
`.gitignore` names it — Git applies ignore rules to untracked paths only — so a
project that committed its configuration and later added the rule is left alone.
A rule that is local to the checkout, in `.git/info/exclude` or a
`core.excludesFile`, is reported differently: that is the supported way to keep
tool config out of a repository that is not yours to commit it to, so it is
acknowledged rather than argued with, and what the warning names is
[`init --external`](#keeping-the-configuration-outside-the-repository) for
keeping the configuration outside the repository. Nothing is said where Git
could not be asked — a project that is not a repository, a configuration kept
outside the one it describes, a Git that would not run. An external
configuration is not asked about at all: it is outside the repository by
construction, and there is nothing there for an ignore rule to reach.

`init --json` and `config validate --json` both report it under `ignored`, with
the `path` that was asked about, the `rule` Git answered with, and the `source`
file that rule lives in.
### Where the tracker syncs

`init` also points the tracker at a remote, because a tracker that syncs
nowhere is one backlog per machine, drifting apart with nothing to say so. The
default is the project's own Git remote: Beads moves its data over an ordinary
Git remote under refs of Dolt's own, so the tracker rides beside the code it
tracks — one repository, one permission model, and nothing to stand up.

- It reads the Git remote `origin` and configures the tracker remote of the
  same name to sync there, printing what it configured.
- A tracker that already has an `origin` remote is left exactly as it is, even
  when it points somewhere other than this project's Git remote: that is a
  decision `init` must not undo. A tracker whose remotes are all named
  something else is untouched too, and gets an `origin` beside them.
- `--tracker-remote <url>` names the remote instead, and replaces whatever
  `origin` currently holds — which is what a tracker kept in a repository of its
  own needs. Beads accepts any Git URL.
- `--tracker-local` keeps the tracker on this machine, with or without
  `--external`: `init` configures no sync remote and says so. A remote the
  tracker already holds is reported rather than removed, because somebody chose
  it; `bd dolt remote remove <name>` removes it. `--tracker-local` and
  `--tracker-remote` ask for opposite things, so `init` refuses the two together
  before writing anything.
- `init --external` never points the tracker at the project's own Git remote,
  because a configuration kept outside the repository is for a repository you
  cannot or should not publish into. With no `--tracker-remote` it configures
  nothing and names `--tracker-remote` as the way to share the tracker; a
  `--tracker-remote` URL that is one of the project's own Git remotes is
  declined; any other URL is applied. A tracker that already syncs through the
  project's own remote — `bd init` can set that up by itself when the project
  has an `origin` — is reported on standard error with the
  `bd dolt remote remove` command that keeps it on this machine, and
  `yoyo doctor` keeps reporting it as its `tracker-remote` warning until it is
  removed.
- A project with no Git remote, or one whose `bd` is not initialized yet, is
  told what to run rather than failing: the configuration is written and valid
  either way, so `init` still exits 0 and `init --json` reports the outcome
  under `tracker` — `configured`, `unchanged`, `skipped`, `local`, or
  `failed`. `tracker.local` is true wherever a tracker kept on this machine was
  asked for, by `--tracker-local` or by `--external`, and
  `tracker.project_remote` is true where the remote reported is one of the
  project's own Git remotes.

A synced tracker is one operator's backlog surviving their machine, and not yet a
team sharing one: two people each running their own harness against one
repository is
[not supported](../team-mode-scope.md#what-v1-supports-meanwhile), because the
coordination around the backlog — claims, reports, directives, and the
[triage budgets](recovery.md#triage-thresholds) — stays on the machine
that made it.

Two consequences of the tracker riding your repository are worth knowing before
you adopt the default. Its history counts against the repository's size like any
other history, and grows with the backlog rather than with the code. And a push
writes `refs/dolt/data` and a `__dolt_remote_info__` branch: GitHub carries both
without complaint, but a forge that restricts which refs it accepts, or a team
that reads the branch list closely, is worth checking before you rely on it —
that is the case `--tracker-remote` and a tracker repository of its own exist
for.
## Layout

A project keeps its configuration in a `.yoyodyne` directory at its root:

```text
.yoyodyne/
  config.yaml          # the project configuration
  config.lock          # the template's values; absent in older projects
  personas/            # one Markdown file per role's persona
    product-manager.md
    architect.md
    development-manager.md
    developer.md
    reviewer.md
    program-manager.md # copied for a program manager you configure later
  roles/               # optional, protected role definitions; inert until an agent names one
    specialist.yaml
```

Everything under `.yoyodyne/` is machine-independent and belongs in version
control. Run state, provider event streams, locks, worktrees, and the reports
agents file while their work carries on live outside the repository in the
machine home, `~/.yoyodyne` by default, so nothing there depends on where the
project is checked out. Where that directory is can be set for the machine and never in
this file; see [`state_root`](#where-the-harness-keeps-its-state-state_root).

The machine home holds one directory per project, named by its `product.id`,
and at the top only what no single project owns:

```text
~/.yoyodyne/
  machine.yaml           # this machine's settings: state_root, and nothing else
  accounts/              # provider accounts, which serve every project
  operator-hold.json     # the operator's pause, one for the whole machine
  machine-identity.json  # this machine's identity, made once and never made again while it is there
  projects/<product id>/
    repository.json      # the binding: which repository this id is
    config.yaml          # the configuration, where the repository does not carry it
    personas/            # its personas, beside it
    state/               # runs, conversations, spend, the docket, memory, reports
    worktrees/           # the developer worktrees of the bound repository
    intent/              # the companion intent repository, where the project keeps one
```

`config.yaml` and `personas/` are there only for a project whose configuration
is [kept outside its repository](#keeping-the-configuration-outside-the-repository),
and `intent/` only for one that [keeps its intent there
too](#keeping-the-intent-outside-the-repository-too);
a committed `.yoyodyne/` is read where it is. `repository.json` is the
[binding](#a-product-id-names-one-repository-on-the-machine). A home the earlier
builds laid out keeps each product's records under `products/<product id>/` and
its worktrees under `worktrees/<product id>/` instead, and is read that way
until [`yoyo home migrate`](../operations.md#moving-into-the-machine-home) moves
it; the harness never moves it on its own. Nothing of the harness's is kept
anywhere else on the machine once that has run: the configurations home earlier
builds kept at `~/.config/yoyodyne` is read only for a `machine.yaml` still left
there, and the migration moves that and every external configuration out of it.

`machine-identity.json` names this machine in the factory identity the harness
puts in every pull request description it opens and, with team mode on, in every
claim it takes: the product id, this machine's identity, and the forge account
it acts as. A claim taken while the forge will not say which account that is
names the product and this machine's identity and says the account was not
read, rather than leaving the item unclaimed. The identity is made the first time it is
needed and moves with the home, so a home is one factory for as long as it
exists; a second home on the same machine, such as one `YOYODYNE_STATE_HOME`
names, is a second factory. Deleting the file makes this machine read as a new
factory to everyone working the same repository.

Committing it is the default rather than a requirement, and a contributor to a
repository they do not own has two supported ways not to, both under
[Keeping the configuration outside the repository](#keeping-the-configuration-outside-the-repository):
a `.yoyodyne` listed in `.git/info/exclude`, and a configuration kept
outside the repository entirely, which
[`yoyo init --external`](#keeping-the-configuration-outside-the-repository)
writes and discovery finds without anything being passed to it.

What `init` writes looks like this, with the explanatory comments trimmed:

```yaml
version: 1

product:
  id: example
  repository: .
  specifications: docs/product
  invariants: docs/decisions/invariants
  designs: docs/designs
  decisions: docs/decisions

execution:
  max_concurrent_developers: 1
  repair_attempts_before_replan: 2
  document_landings: 2
  integration_retries_before_reconciliation: 2
  transient_relaunches_before_blocking: 2
  worktree_root: auto
  remote: origin
  usage_limit_max_pause: 6h
  usage_limit_in_process_pause: 6h
  usage_limit_unknown_reset_pause: 30m
  server_overload_pause: 90s
  check_timeout: 30m
  check_stage_timeout: 30m
  landing_check_timeout: 2h

triage:
  stuck_merge_age: 2h
  review_rounds_cap: 4

approvals:
  brief: human
  goals: human
  designs: automatic
  work_items: human
  integration: human
  publishing: human
  releases: human   # who cuts a decided release; see Publishing through pull requests

services:           # the parts of the product, each on or off; see Services
  slack:
    enabled: false
  dashboard:
    enabled: false
    port: 8765
    bind: 127.0.0.1
    allowed_hosts: []
    token: generated
    hide_summary: false
  scheduler:
    enabled: true
  maintenance:
    enabled: true
    every: 10m
    tracker_cleanup:
      every: 24h
      grown_megabytes: 128
      max_hold: 15s
    tracker_limits:
      store_megabytes: 1024
      listing_seconds: 10

checks: []          # yours to write; a run with none is refused
landing_checks: []  # what runs whole, once per landing; see "Where the whole suite runs"
path_checks: []     # checks a change runs only when it touches what they vouch for
install: []         # what installs the project's dependencies; see configuration/runs.md
document_checks: [] # what a confirmed document's landing runs; see "The document checks"
main_checks: []     # what runs whole on the branch's head every four hours; see "The checks run on main"
merge_queue_checks: [] # which of the checks above the merge queue runs; see "The checks the merge queue runs"
merge_queue_race_check: "" # the check the queue adds, narrowed, for a change with no passing result of it
intent:
  repository: ""    # a companion intent repository, if the documents live outside the project's own
  checks: []        # what a landing into that repository runs; see "The companion intent repository's checks"

accounts:
  default: {}       # the provider account the agents below run under

agents:
  product-manager:
    role: product-manager
    backend: claude-code
    model: opus
    effort: medium
    account: default
    instances: 1
    persona:
      version: v1
      path: personas/product-manager.md
  # ... architect, development-manager, developer, and reviewer, the same shape
```

Five agents — Lead Product Manager, architect, development manager, developer, and
reviewer — each with a role, a backend, a model selector, the
[effort level](recovery.md#an-agents-effort-level) that model is asked to
think at, the [provider
account](agents.md#provider-accounts) it runs under, an instance count, and a persona file
that is in the repository beside the configuration. Change one by
editing it. Remove one by deleting its block. Nothing has to be expressed as a
deviation from something invisible.

`yoyo agent list` reports them as they actually stand, with the durable
conversation each one has, and `yoyo agent chat <name>` addresses one. The
conversation belongs to the agent rather than to the role, so configuring two
agents for one role gives you two conversations with two provider sessions —
naming one of them reaches that one. What a role may do in that conversation is
**not** configurable and is not what the persona says: the harness holds one contract and one authority table per role,
sends the contract ahead of the persona on every turn, and refuses anything
outside the table. A persona specializes how a role works; it cannot widen what
the role is allowed to do. `yoyo config show` reports each agent's
`capabilities` — everything the harness may do on that agent's behalf, named in
the vocabulary the authority is stated in rather than left to be inferred from
the role's name. It is reported and never written: the set is read off the role
in the harness's own registry — or, for an agent filling a role definition, it
is that definition's tool set: the shipped role's, plus the definition's
additions, minus its removals — there is no `capabilities` key to put in a
configuration, and a file that writes one is refused like any other key that
does not exist. The set of shipped role names is fixed for the same reason —
the capabilities and harness tools each role holds are derived
from the name — so `role` is one of the six: `product-manager`,
`architect`, `development-manager`, `developer`, `reviewer`, or
`program-manager`, or the name of an activated
[protected role definition](#protected-role-definitions), which extends one of
them. Anything else is
[refused when the configuration loads](#what-fails-closed).
The sixth is the [program manager](../designs/program-manager.md): an agent filling
it watches one outcome across the others and may change work only inside its own
lane. `yoyo init` configures none — an instance is a lane and a remit somebody
chose, written in three keys only that role's agents carry
([a program manager instance](#a-program-manager-instance)) — and every tracker
write the role holds is confined to that lane, read off each item as the action
runs ([a program manager's lane](../conversation.md#a-program-managers-lane)). It
may also record [a request that a part be restarted](../operations.md#starting-the-product-and-stopping-it),
which the supervisor's [maintenance pass](../operations.md#the-supervisors-maintenance-pass)
answers at its next pass.
[Talking to the other agents](../conversation.md#talking-to-the-other-agents) states
the table itself.

**Tools are derived from the agent's capabilities.** A tool is a registered
harness action requested through a typed reply block, with its own parameters,
bounds, scope and audit events. An agent holds the tools its shipped role is
granted, or, for an agent filling a
[role definition](#protected-role-definitions), the tools that definition's set
holds. The generated tools section follows the role's fixed authority prose and
precedes its persona and remit. `yoyo config show` reports each agent's tool
identifiers alongside its effective configuration (`tools` in the JSON report,
comments in the YAML display). This is a report, not a new configuration key:
there is no agent `tools:` narrowing key, a definition narrows only through its
own file and a person's activation of it, and a persona or remit cannot grant
anything.

The development manager's and the program manager's `log.read` grant reads this product's pass records, watch
transitions and scheduler output, docket, named run records, usage-limit log,
provider-outage record, and record of the operator's hand steps and messages
under the state root. Reads require a cursor or a time
window and a byte cap, return redacted untrusted evidence, and are audited on the
conversation before evidence is delivered. Tokens, memory, other agents'
conversation records and arbitrary filesystem paths are refused.
[The conversation guide](../conversation.md#tools-and-the-program-managers-log-reads)
states the request format and bounds. The supervisor's scheduler output is
`<state root>/projects/<product id>/state/scheduler.log`, alongside the watch's
durable transitions; it is read by naming the watch's output rather than a path.

`backend` is `claude-code` or `codex` unless your project declares one of its
own. Both built-ins serve all six roles. Codex uses worktree-write access for
developers and native read-only inspection for reviewers and management roles,
with tool network access, escalation, and external integrations disabled. Claude
Code gives a reviewer reads of the worktree under review — its file read, file
search, and text search tools, and a shell that runs only read-only Git commands
inside Claude Code's sandbox, with the worktree refused for writing and no
network — and gives the management roles no tools at all.
[Provider plugins](../provider-plugins.md#capability-validation) describes the boundary
and its limits.
For a CLI outside PATH, including a desktop-bundled Codex, set
`providers.codex.binary` or `providers.claude-code.binary` to its absolute path;
[executable setup and precedence](../provider-plugins.md#executable-setup-and-precedence)
applies to terminal chats, scheduled execution and fallback alike.
A project running a fork, a proxy, or
a variant of a provider yoyo already speaks can describe it under a top-level `providers:` key and name it here: which
compiled adapter launches it, which executable that adapter runs, which roles it
serves, which kinds of tool access it can hold them to, and how to read what it says
about rate limits, retries, and reset times. A declared provider describes and
decides nothing: whether to wait, how long, and against which budget stay the
harness's, because those are what the
[`execution.usage_limit_*` settings](recovery.md#waiting-out-a-provider-that-refuses)
mean. [Provider plugins](../provider-plugins.md) is the format and its limits — in
particular that a provider speaking a protocol no compiled adapter speaks needs
an adapter rather than a declaration.
### Protected role definitions

A person may define a named bundle in `.yoyodyne/roles/<name>.yaml`. The name
is the file's name without `.yaml`, using lowercase letters and digits in
hyphen-separated words and beginning with a letter. Each definition extends
exactly one of the six shipped roles and lists the registered capability
primitives it adds and removes:

```yaml
# .yoyodyne/roles/specialist.yaml
extends: architect
tools:
  add:
    - backlog.order
  remove:
    - repository.list
```

`tools`, `add`, and `remove` may be omitted when they change nothing. A removal
must name a primitive the shipped base role holds. An unknown primitive in
either list, a duplicate, a primitive in both lists, an unknown key, or an
`extends` that names no shipped role or more than one refuses the file. The
refusal names the file and the primitive or field that is wrong. A definition
cannot extend another definition, and one file holds one YAML document, at most
32 KiB.

**The gates cannot be added as tools.** Checks and review evidence
(`checks.execute`, `review.verdict`), publication (`forge.publish`), integration
and its lease (`target-branch.mutate`, `promotion.lease`), and the run operations
`worktree.mutate`, `provider.invoke`, and `run-state.mutate` are refused as
additions. Gate-evidence minting and recording a human gate have no registered
tool primitive, so names attempting either are refused too. The shipped base
role's existing authority is unaffected by those addition rules.

**Loading is validation, and gives no agent authority.** Configuration loading
reads the definitions even when no agent names them, and an invalid definition
refuses the configuration whole. A file no agent names is inert: it changes no
agent's capabilities, contract, or effective configuration revision.
[`yoyo role activate`](../operations.md#activating-a-role-definition-and-reading-its-history)
records a person's activation of the file's exact content digest and where the
file stood. `yoyo role list` compares the current file with that decision — an
amended file, or one that has moved to another path, is not activated as it
stands — and `yoyo role history` retains every activation. A persona grants
nothing through this file or any other.

**An agent fills a definition by naming it as its role.**

```yaml
agents:
  architect:
    role: specialist      # .yoyodyne/roles/specialist.yaml, extending architect
    backend: claude-code
    model: opus
```

The agent is then an agent of the shipped role the definition extends — that
role's contract, its conversation, its place in a run, every key only that
role's agents carry — and everything that reads what the agent may do reads the
definition's tool set instead of the role's: the shipped role's capabilities,
plus the definition's additions, minus its removals.

- `yoyo config show` reports that set as the agent's `capabilities` and the
  tools it holds as its `tools`, names the definition under `definition` with
  its digest and file, and records the capabilities' origin as
  `role-definition:<name>`. `yoyo agent list` names the
  definition beside the role it extends.
- The [conversation authority table](../conversation.md#talking-to-the-other-agents)
  derives the agent's row from the set, so a definition that removes
  `exchange.ask` takes the agent off the ask channel and one that removes
  `backlog.admit` from a Lead Product Manager takes away closing and admitting
  while leaving decomposition underneath admitted work. The instructions each
  turn is sent describe only the actions and tools the set holds, `yoyo chat`
  says under its header which definition the agent fills, and the tools the
  conversation carries out are the set's too, and so are the report
  and amendment tools a developer or reviewer run carries out. The answering
  end of an exchange reads the answering agent's set the same way.
- The tool access the provider holds the agent to is the role's own, except
  that a definition removing `worktree.mutate` from a developer holds it
  read-only: no tools on Claude Code, the read-only sandbox on Codex.
  [Capability validation](../provider-plugins.md#capability-validation) asks the
  agent's provider and its failover alternate about that narrower access.
- A project that integrates without asking a person refuses a reviewer agent
  whose definition removed `review.verdict`.

A definition can only narrow what its shipped role may not do. The additions
the loader refuses above are refused whatever a definition says, and no set
makes a read-only role a writer: a request for wider tool access than the role
has is refused before a provider is started.

**A definition supplies authority only once activated as it stands.** Every
command that reads or runs agents — `yoyo config show`, `yoyo chat`, `yoyo run`,
`yoyo work`, and the rest — reads the activation record and refuses the whole
configuration while any agent names a definition nobody has activated, one
amended since its latest activation, or one whose file is no longer the file
that was activated. The refusal names the agent, the definition, and the
`yoyo role activate` command that would make it effective, which is a person's
act. The check is part of loading itself, so nothing that reads the
configuration can give an agent a definition's tool set without asking — the
configuration a watching `yoyo work` session re-reads at each pull included,
which reads a definition amended after activation as refused rather than as
the amended set. The three `yoyo role` commands read the definitions without binding any
agent, because they are how a person activates one. A definition that no longer loads refuses the
configuration like any invalid definition, and an agent naming a name that is
neither a shipped role nor a definition is refused as an unknown role.

Definitions live beside the configuration in `roles/`, following the same
directory order as personas for an external or legacy configuration. Where
there are two possible directories, the first file for a name wins, and a bad
first file never falls back to the second. A missing directory is allowed;
dangling links, files that are not regular files, and links escaping the roles
directory are refused.

**Only a person writes these files.** The
[protected-path gate](artifacts.md#protected-paths-in-a-developers-change) refuses changes
under `.yoyodyne/roles/` from a developer run whatever its work item grants.
Loading a definition creates no exception to that boundary.

### A program manager instance

An agent on the `program-manager` role carries three keys beside the backend,
model, account, and persona every agent carries. They are what tells one
instance of the role from another, and they are refused on an agent of any other
role, naming the key and the role:

```yaml
agents:
  reliability-pm:
    role: program-manager
    backend: claude-code
    model: opus
    persona:
      version: v1
      path: personas/program-manager.md
    lane: reliability
    remit:
      version: r1
      path: remits/reliability.md
    triggers:
      every: 2h
      on: [landings, stoppages]
```

- **`lane`** is the tracker label the instance owns: one identifier-shaped word,
  held to the rule a [slot's preferred label](runs.md#a-developer-slot-that-prefers-a-label)
  is — letters, digits, dots, underscores, and hyphens, up to 64 bytes, compared
  exactly. **Two instances naming one lane are refused when the file loads**, and
  the refusal names both agents and the lane: a lane has one owner or it is not a
  lane. `yoyo agent list` prints the lane beside the agent's name —
  `reliability-pm (program-manager, lane reliability)` — and carries it as `lane`
  in `--json`.
- **`remit`** says what the lane is for, as `version` and `path`: a Markdown file
  under the configuration directory, held to every rule a [persona](agents.md#personas) is
  held to — relative, no `..`, Markdown, present, not empty, not a symlink out of
  the directory — and to the same 32 KiB bound, and refused in the persona rules'
  own words with `remit` in place of `persona`. An override replaces an inherited
  remit whole and must name both halves. It is delivered on every turn of the
  instance's conversation **after the persona**, which is after the contract: the
  contract says what the role may do, the persona how it works, and the remit what
  its lane is for, and the remit grants nothing either of the others refuses.
  Editing the file moves the [configuration revision](#inspection) as editing a
  persona does.
- **`triggers`** is what wakes the instance for a pass over its lane: `every`, a
  duration floored at the [recurring-task](agents.md#recurring-tasks) minimum of `5m` for
  the same reason — every pass is a conversation turn — and `on`, a list drawn
  from the closed set `landings`, `admissions`, and `stoppages`. An `every` under
  the floor, an `on` entry outside the set, and an entry named twice are refused
  when the file loads; leaving `every` out is no scheduled pass. Leaving the block
  out altogether is an instance that passes only when the operator or the Lead
  Product Manager asks for a pass — on no timer and on no event. A later layer's
  block replaces an inherited one whole, as the failover block does.

**The lane is enforced, and the triggers take passes.** The keys load, are
validated, are reported by `yoyo agent list` and `yoyo config show`, and the
remit is delivered. The lane is what confines the instance's tracker writes: a
creation carries the lane label in the write that admits it, and every other
write is refused on an item not carrying it at the moment of the act — the rules
are [a program manager's lane](../conversation.md#a-program-managers-lane). An
instance configured with no `lane` has nothing inside one, so all of its tracker
writes are refused. A `yoyo work --watch` session reads the triggers and takes
the instance's passes as recurring-task firings —
[a program manager instance's passes](agents.md#a-program-manager-instances-passes) says
how `every` and `on` wake one. Configuration selects which lane and what wakes
it, and never widens what the role may do.

## Discovery

Yoyodyne looks for a configuration in this order:

1. the path given to `--config`, if present;
2. otherwise the configuration `YOYODYNE_CONFIG` names;
3. otherwise `.yoyodyne/config.yaml`, searching from the current directory
   upwards to the filesystem root;
4. otherwise `.yoyodyne.yaml` in the same directories;
5. otherwise the configuration kept in the machine home's project directory
   whose [binding](#a-product-id-names-one-repository-on-the-machine)
   names the repository the current directory is in, as
   [`yoyo init --external`](#keeping-the-configuration-outside-the-repository)
   writes it.

Because the search walks upwards, `yoyo run` works from the project root or
from any directory beneath it. When both forms exist in one directory, the
directory form wins, so a half-finished migration cannot silently keep using the
old file.

The project's own configuration is looked for before this machine's, and that
order is the point rather than an implementation detail: a repository that
describes itself is what every collaborator gets, and a file one person wrote on
one machine must not quietly win over it. A project that carries a `.yoyodyne`
behaves exactly as it did before external configurations existed.

`YOYODYNE_CONFIG` is `--config` for a whole shell: export it and every command
run there reads that configuration. It must be an absolute path, and it names
either the configuration file or a directory holding a `config.yaml`. A variable
that is set and names nothing readable is a failure rather than a step that is
skipped — it is an instruction exactly as `--config` is, and falling through to a
different configuration than the one that was named is how a command does the
right thing to the wrong project.

Relative paths inside the configuration — `product.repository` and a non-`auto`
`execution.worktree_root` — resolve against the project directory, which is the
parent of `.yoyodyne`, not the `.yoyodyne` directory itself. `repository: .`
therefore keeps meaning the project root.

One case resolves further. A project whose `.yoyodyne` is checked in gives every
worktree of it a copy, so a command run from inside a worktree the harness
manages would otherwise resolve its repository to that worktree — a directory
under `execution.worktree_root`, which no command can address, since the
repository and the worktree root must not contain one another. Every command
instead addresses the checkout the worktree was added from, which is what makes
`yoyo cost`, `yoyo directive`, `yoyo pause`, `yoyo resume`, `yoyo reconcile`,
`yoyo run`, `yoyo review`, and `yoyo chat` work from inside a preserved worktree
— where inspection after a failed run happens — and from an agent's own. A
repository that is under the worktree root and is *not* a worktree of a checkout
outside it is still refused, and the refusal names both roots. The artifact directories —
`product.specifications`, `product.invariants`, `product.designs`, and
`product.decisions` — are the exceptions, and deliberately: each names a directory
*inside the repository being worked on*, so all four resolve against
`product.repository` and are refused if they leave it — or, for a project that
[keeps its intent in a companion repository](#keeping-the-intent-outside-the-repository-too),
against that repository instead.

That refusal is checked twice. When the file loads it is a check on the text: a
path that is absolute or climbs out with `..` is refused before any work is
claimed. When something reads or writes a document in one of those directories it
is checked again, against the filesystem, immediately before the bytes move —
because a directory that reads as `docs/decisions` in this file is whatever the
filesystem has put there by the time anything goes looking, and one symlink along
the way reaches outside the repository without a single `..` appearing anywhere.
So a directory that is a symlink out of the repository, or that sits below one, is
refused: nothing is written, and nothing is read. The reading half is the half
that costs more. A document from outside arrives named by a path this repository
looks like it holds, so an invariant nobody committed and nobody reviewed would be
delivered to every developer and every reviewer as a constraint this project holds
itself to, and nothing downstream could tell it from one that was. A symlink that
stays inside the repository has not left it, and the read and the write both
follow it. The same holds of the `.yoyodyne` directory `yoyo init` writes: a project
whose `.yoyodyne` leads out of the project is refused with the project untouched
rather than scaffolded somewhere nothing commits. And of the
[machine home](#keeping-the-configuration-outside-the-repository),
which is a declared root like any other: a write that resolves out of it
is refused rather than landing where nothing looks for it.
## Keeping the configuration outside the repository

A contributor to a repository they do not own has the configuration as theirs
rather than the project's, and a pull request adding a tool directory nobody
asked for is a pull request about the tool. There are two ways to keep it out,
and a third step that keeps the project's intent out with it.

**Keep it on disk and out of Git.** Discovery reads the checkout's filesystem
and never consults the index, so an untracked `.yoyodyne/` loads exactly like a
committed one. List it in `.git/info/exclude`, the per-clone ignore file that is
never committed:

```sh
printf '.yoyodyne/\n' >> .git/info/exclude
yoyo init
yoyo doctor
```

The exclude line is required rather than tidy: a run refuses to start while the
primary checkout holds anything uncommitted the project did not declare,
untracked files included, so without it the first `yoyo run` names the files
`init` wrote and stops. The same holds for the `README.md` `init` puts at the
door of each of the five artifact homes — `docs/product`,
`docs/product/goals`, `docs/designs`, `docs/decisions`, and
`docs/decisions/invariants` — which in a repository you are a guest in are five
untracked paths in somebody else's `docs/` tree. Commit them where the project
wants them; otherwise exclude them too, or delete them, which `yoyo doctor`
reports as a warning and nothing more:

```sh
printf '%s\n' docs/product/README.md docs/product/goals/README.md \
  docs/designs/README.md docs/decisions/README.md \
  docs/decisions/invariants/README.md >> .git/info/exclude
```

`bd init` writes `.beads/` and a set of agent instruction files, each another
untracked path a run would refuse over, so a fully local adoption excludes those
as well: `.beads/`, `.agents/skills/beads/`, `.claude/settings.json`,
`.codex/config.toml`, `.codex/hooks.json`, `AGENTS.md`, `CLAUDE.md`, and the
`.gitignore` it creates or adds to. `init --external`, below, adds them for you.

**Or keep it outside the repository entirely.** `yoyo init --external` writes
the configuration this machine keeps for that repository, and none of the
repository's files: the one thing it changes there is the checkout's own
`.git/info/exclude`, which is never committed:

```sh
cd ~/src/theirproject
yoyo init --external
yoyo doctor
```

It writes into the machine home's project directory,
`~/.yoyodyne/projects/<product id>/`, binds that directory to the repository
first, and puts everything `init` ordinarily writes into `.yoyodyne/` there
instead, personas included. A product id already bound to another repository
refuses before anything is written.

Five things are worth knowing about it:

- **Nothing is passed on later commands.** The configuration is found by the
  binding, which names the repository by its Git common directory, so `yoyo`
  finds it from the repository root, from any directory
  beneath it, and from a worktree Git added from it — a run's worktree, and a
  check or a hook that shells out to `yoyo` from inside one, resolve to the
  repository they came from rather than being read as projects of their own.
  That is what separates this from moving `.yoyodyne` somewhere by hand and
  passing `--config` on everything thereafter.
- **The binding is to one clone.** A second clone of the project on the same
  machine is refused rather than given a second configuration. Moving the
  checkout leaves the binding naming where it was. From where it now is, run

  ```sh
  yoyo project bind --product <product id>             # the old checkout is gone
  yoyo project bind --product <product id> --replace   # the old clone is still there
  ```

  and that is the whole of it. The bind points the configuration's
  `product.repository` at this clone in the same act, so worktrees, the target
  branch, the checks, and the tracker are all worked here from then on, and it
  removes the old clone's state-root marker where that clone is still there,
  so nothing started in it reads it as current. Nothing is edited by hand.
  **The binding decides which clone is the project's.** Where the
  configuration's `product.repository` names a different clone anyway — left by
  a bind made before bind rewrote it, or by an edit — every command uses the
  clone the binding names, for worktrees, the target branch, the checks, the
  tracker, the state-root marker, and the artifact folders alike, and
  `yoyo config show --origins` gives `product.repository` the origin
  `binding:` and the binding's path. The file is then out of date, which
  `yoyo doctor` reports as a warning under `binding`, naming both paths, with
  `yoyo project bind --product <product id> --directory <bound clone>` as the
  command that rewrites it.
- **`product.repository` is written absolute.** An external configuration has no
  project directory above it for a relative path to resolve against. The
  artifact directories are unaffected: `specifications`, `invariants`,
  `designs`, and `decisions` resolve against `product.repository` and go on
  naming directories inside the repository being worked on — unless the
  project keeps its intent in a companion repository, below.
- **It writes no artifact-home indexes.** `init` ordinarily puts a `README.md` at
  the door of each of the five artifact homes, and in a repository you are a
  guest in those are five untracked files in somebody else's `docs/` tree.
  `yoyo doctor` reports each home without an index as a warning rather than a
  problem, so an installation configured this way runs work exactly as one with
  them does.
- **It keeps the tracker to this machine and out of the checkout.** It never
  points the tracker at the project's own Git remote, and it reports a tracker
  that already syncs there, so the backlog is not published into the project
  unnoticed ([Where the tracker syncs](#where-the-tracker-syncs) says what it
  does instead). And it adds `.beads/` and every file `bd init` writes into the
  checkout to `.git/info/exclude`, anchored to the checkout's root, and prints
  the entries it added; `init --json` reports them under `excluded`, as
  `added`, `present` (already there), and `tracked`. A file the project already
  tracks, such as its own `AGENTS.md`, is not excluded, because an exclude line
  does not reach a tracked file, and `init` names it so you can put back
  whatever `bd init` changed in it. Nothing is written to the project's
  `.gitignore`.

Either way, the project stops describing itself, which in this scenario is the
intent: another clone, another machine, and anybody else working on it get no
configuration at all, and `yoyo` there reports that it found none.

### Keeping the intent outside the repository too

The configuration is not the only thing of Yoyodyne's a project carries. Its
intent — the brief, the goals, and the other specifications under
`product.specifications`, the designs under `product.designs`, and the decision
records and invariants under `product.decisions` and `product.invariants` — is
ordinarily committed in the project's own repository. A project with an
external configuration may keep all of it in a **companion intent
repository** instead: an ordinary Git repository of its own, in the project's
directory in the machine home, pushed and cloned wherever the team chooses.

```sh
cd ~/src/theirproject
yoyo init --external --intent                    # create it, empty but for the indexes
yoyo init --external --intent-from <its URL>     # or clone the one a teammate shares
```

Either writes the `intent` block into the configuration and makes the
repository at `~/.yoyodyne/projects/<product id>/intent/`:

```yaml
intent:
  repository: intent   # relative to the directory this configuration is in
```

`--intent` runs `git init` there and writes the index at the door of each
artifact home into it rather than into the project's `docs/`; `--intent-from`
clones it. Nothing is committed for you: commit what is there and push it where
your team shares it. `yoyo setup` offers the same step — for a project with an
external configuration it asks whether to keep the intent outside too, and
offers to create a companion repository the configuration names and that is not
there yet — and says what the repository holds either way.

**Every reader of intent reads it there.** The homes keep meaning the same
directories; what moves is the repository they are relative to. The artifact
commands, the goals and their attributions, `yoyo stale`, `yoyo conformance`,
the invariants delivered to a run, the specifications every developer and
reviewer is handed, a document a work item cites, and the management
conversations' picture of the product all resolve their root through one
function, `config.Product.IntentRoot`, which answers the companion repository
where the configuration names one and the project's repository otherwise. Intent
read from the companion repository is read at the commit that repository's target
branch holds, never from its checkout, and the context names that commit. A run
records the commit it read, so its developer and its reviewer read the same one,
and a change in the companion checkout that is not committed is not intent. A test drives a whole run against a project repository holding none of
the harness's files.

Four things are worth knowing about it:

- **Only a configuration kept outside the repository may name one.** An
  `intent` block in a committed `.yoyodyne/config.yaml` is refused when the
  configuration loads: a committed configuration is already something of
  Yoyodyne's in the repository, and every clone reads it while the companion
  repository is a path on one machine. A path that is absolute or climbs out of
  the project's directory is refused too.
- **A missing companion repository is a problem `yoyo doctor` names**, with
  `yoyo setup` as the remedy, because nothing can read the project's intent
  until it is there.
- **A confirmed document lands in the companion repository through a reviewed
  run.** A design confirmed under `approvals.designs: automatic`, and a document
  you approve, are written by a run cut from that repository's target branch,
  checked with [the companion intent repository's
  checks](runs.md#the-companion-intent-repositorys-checks), reviewed by the independent
  reviewer — who is also shown the project's code at a commit the run records —
  and promoted into that repository's target branch under the one-at-a-time
  promotion rule. No pull request is opened, because the forge access is the
  project's; the change is pushed to that repository's own remote where it has
  one. The run's record names the repository, and the commit it merged. A
  session built without access to the companion repository cannot cut that run,
  so the document stays confirmed by its policy and saved in its conversation,
  the owning role is told once what holds it, and it is tried again at each later
  message. It is not put to you: confirming a design is not a decision that
  needs a person. What your policy already puts to you — the brief and the goals
  under `human` — still is. `yoyo artifact identify` and `yoyo invariant` write
  into the companion repository's checkout, for you to commit there, and say the
  write is an uncommitted change in that repository, which is not intent until
  it is committed on its target branch.
- **The tracker stays where it was.** The tracker's export under `.beads/` and
  the developer-session instruction files are still the project's, so a fully
  local adoption excludes them as above.

## Where the harness keeps its state: `state_root`

Run state, provider event streams, locks, worktrees, the operator's pause, and
every durable record the harness keeps live under one directory outside the
repository, the **state root**. Where it is can be set for the machine, in a
file that describes the machine rather than any project:

```yaml
# ~/.yoyodyne/machine.yaml
state_root: /Volumes/work/yoyodyne-state
```

`machine.yaml` is always at `~/.yoyodyne/machine.yaml`, wherever `state_root`
moves the rest of the home, because it is the file that says where that is. A
`~/.yoyodyne` holding nothing but it does not count as a home in use, so
writing one on a machine still running from the earlier builds' home moves
nothing by itself. A `machine.yaml` left in `~/.config/yoyodyne`, where earlier
builds read it, is read in its place while `~/.yoyodyne/machine.yaml` does not
exist, so a machine whose `state_root` is set there keeps its home until
[`yoyo home migrate`](../operations.md#moving-into-the-machine-home) moves the file;
once `~/.yoyodyne/machine.yaml` exists it is the one read. `state_root` is its only key; it must be
an absolute path, and a key it does not have is refused rather than ignored. A
missing file, an empty one, and an empty `state_root` all leave the root where
the layers below put it.

**The root is resolved in this order**, the first that says anything winning:

1. `YOYODYNE_STATE_HOME`, the explicit instruction for one shell;
2. `state_root` in `machine.yaml`;
3. `$XDG_STATE_HOME/yoyodyne`;
4. the machine home, `~/.yoyodyne`, on every platform — except that while
   `~/.yoyodyne` does not exist, or holds only `machine.yaml`, and the earlier
   builds' default home does
   (`~/Library/Application Support/Yoyodyne/state` on macOS,
   `%LOCALAPPDATA%\Yoyodyne\state` on Windows, and `~/.local/state/yoyodyne`
   elsewhere), the earlier home is kept, because that is where the state is.
   Nothing moves it on its own.

Every process the harness starts — the watch, the Slack sink, the dashboard,
the supervisor, conversations, and runs — resolves the root through that one
order.

**It is never a project setting.** A project configuration is committed and
read on every machine that checks it out, and where state lives is true of one
machine, so a project file carrying `state_root` — at the top level or under
`execution` — is refused when it loads, naming the key and where it belongs.

**One product's state is never split across two roots.** The first process that
opens the root for a product records it in `.git/yoyodyne/state-root` of the
product's checkout, and a later process that resolved a different root — a
shell exporting another `YOYODYNE_STATE_HOME`, a launch job carrying an old
environment, an edited `machine.yaml` — refuses to start, naming both roots,
the layer that set its own, and the marker. It records nothing and writes
nothing under the root it resolved. A root reached through a symlink agrees
with the directory it links to. A worktree Git added from the checkout shares
the checkout's marker, and a repository that is not a Git checkout keeps none.

**Moving the state is four steps**, in this order: stop the product
(`yoyo stop`), move the directory, change the setting, and run
`yoyo state-root rebind` in the product's checkout, which records the new root
in the marker's place. It does so only because the old root is gone by then: a
marker naming a root still on disk is refused by rebind as by every other
command. The same command clears a marker left naming a root somebody deleted,
which is what the refusal and `yoyo doctor` name as the remedy
([operations](../operations.md#where-the-state-is-and-moving-it)).

**Two products on one machine share the root unless one of them is moved.** Each
product keeps its records under `projects/<product id>/state/` inside it — under
`products/<product id>/` in a root the earlier builds laid out, which is read
that way until it is moved — and each
product's checkout carries its own marker, so two products that resolve the same
root agree with each other as well as with themselves. The
[operator's pause](../operations.md#pausing-everything-and-resuming-it) lives at the
root rather than under a product, so on a shared root one `yoyo pause` stops
both. A product moved to a root of its own is also out of reach of the pause
placed at the other one.

`yoyo config show` prints the resolved root and the layer it came from on its
`# state root:` line, `--origins` lists it as `state_root` with the same origin,
and `--json` carries both under `state_root`. The origin is
`environment:YOYODYNE_STATE_HOME`, `machine:<path of machine.yaml>`,
`environment:XDG_STATE_HOME`, `default` for `~/.yoyodyne`, or `earlier-default`
for the earlier builds' home. [`yoyo
doctor`](../operations.md#checking-the-installation) reports the same two, and
whether the checkout's marker agrees. Neither of them records a marker.

### A product id names one repository on the machine

The first start against an id creates `projects/<product id>/` and records in
its `repository.json` which repository the id is, by the repository's Git common
directory, so every worktree of that clone is the same project; the start says
so. After that a start from a second clone of the same project, from another
product using the same id, or against a bound repository that is no longer at
its path refuses, naming both paths and the one command that settles it. A root
still laid out the earlier way writes no binding at a start; `yoyo project bind`
and `yoyo init --external` write one there too, and a binding once written is
held to in either layout. The [machine home design](../designs/machine-home.md) is
the whole of it.

```sh
yoyo project list                          # every project, its binding, and whether the repository is there
yoyo project bind                          # bind this repository to the project for its id
yoyo project bind --replace                # ... taking it off another clone that is still there
yoyo project bind --product <id>           # ... where the configuration cannot be found until it is bound
yoyo project rename <old> <new>            # move a project directory, and everything in it, to a new id
```

- **`bind`** binds the repository it is run from. Where the id is already bound
  to a repository still at its path it refuses unless `--replace` is given, and
  says what it unbound; where the bound repository is gone it binds without
  being asked twice, which is the remedy for a moved or re-cloned repository.
  It refuses while any run of the project is in flight, and `--replace` refuses
  while worktrees kept from stopped runs under `projects/<id>/worktrees/` are
  registered with the clone it would unbind, naming each one, because a later
  repair of one of those runs would work in that clone. Where the project keeps
  its configuration in the project directory, the bind rewrites that file's
  `product.repository` to the repository it binds, leaving every other line as
  it was; run against the repository already bound, it does the same for a file
  that names another, and otherwise says there is nothing to bind. Where the
  clone it unbinds, or the clone an out-of-date file named, is still there, it
  removes that clone's `.git/yoyodyne/state-root` marker, if the marker names
  this home. A configuration committed in its repository is not touched.
- **`rename`** moves `projects/<old>/` to `projects/<new>/`. A configuration kept
  in the project directory has its `product.id` rewritten; a committed one must
  already read `<new>`, so the id in the repository and the directory never
  disagree, and the rename names the file to change where it does not. It
  refuses while a run is in flight, while the project still holds worktrees
  (the repository has them registered at their paths), where the new id is
  taken, and in a home still laid out the earlier way.
- **`list`** names the home and where it came from — `--home` prints its path
  and nothing else, for a script — then each project
  directory, what it is bound to, whether that repository is there, and where
  it keeps a configuration. In a home laid out the earlier way it names each
  product under `products/` that has no binding yet.

`bind` and `rename` each append a line to `project-acts.jsonl` in the project's
state, naming the act, what it moved from and to, and the process and person
that ran it, because a re-binding is the one thing that can point a project's
history at another tree. All three take `--json`.

## Precedence

A configuration `init` wrote has one layer: itself. Every configured value comes
from the project file, and nothing is inherited from a bundle. Two values are
still reported as computed rather than written. `product.repository_id` has the
origin `derived:product.id`, because the generated file states the product id
and lets the repository id follow from it, and
`triage.repair_grant_attempts` has the origin
`derived:execution.repair_attempts_before_replan` for the same reason: the
generated file states the repair budget and lets the grant follow it, so raising
one raises the other. Both are values derived from something in the same file,
not something arriving from outside it.

The rest of this section describes what happens when a project uses `extends`,
and what the harness still fills in when a file leaves something out.

Up to three layers produce the effective configuration, later ones winning:

1. **Harness defaults.** Values the harness fills in when nothing else supplies
   them: `product.specifications` (`docs/product`), `product.invariants`
   (`docs/decisions/invariants`), `product.designs` (`docs/designs`),
   `product.decisions` (`docs/decisions`), `execution.max_concurrent_developers` (1),
   `execution.repair_attempts_before_replan` (2),
   `execution.integration_retries_before_reconciliation` (2),
   `execution.transient_relaunches_before_blocking` (2),
   `execution.worktree_root`
   (`auto`), `execution.remote` (`origin`),
   `execution.usage_limit_max_pause` and
   `execution.usage_limit_in_process_pause` (`6h` each),
   `execution.usage_limit_unknown_reset_pause` (`30m`),
   `execution.server_overload_pause` (`90s`),
   `execution.check_timeout` (`30m`),
   `execution.check_stage_timeout` (`30m`),
   `execution.landing_check_timeout` (`2h`),
   `execution.redeploy_drain_limit` (`15m`),
   `execution.build_move_interval` (`3h`),
   `execution.factory_stall_after` (`2h`),
   `execution.could_not_run_before_status` (3),
   `execution.usage_window_degraded_percent` (90),
   `triage.stuck_merge_age` (`2h`),
   `triage.review_rounds_cap` (4),
   `approvals.publishing` (`human`), `approvals.work_items` (`human`),
   `approvals.releases` (`human`), an
   agent's `instances` (1), and every value under [`services`](#services):
   `services.slack.enabled` (`false`), `services.dashboard.enabled` (`false`),
   `services.dashboard.port` (8765), `services.dashboard.bind` (`127.0.0.1`),
   `services.dashboard.allowed_hosts` (empty), `services.dashboard.token`
   (`generated`), `services.scheduler.enabled` (`true`),
   `services.maintenance.enabled` (`true`), `services.maintenance.every`
   (`10m`), `services.maintenance.tracker_cleanup.every` (`24h`),
   `services.maintenance.tracker_cleanup.grown_megabytes` (128),
   `services.maintenance.tracker_cleanup.max_hold` (`15s`),
   `services.maintenance.tracker_limits.store_megabytes` (1024), and
   `services.maintenance.tracker_limits.listing_seconds` (10).
   `triage.repair_grant_attempts` is filled in too, but as a derivation rather
   than a fixed default: it takes the size of the effective
   `execution.repair_attempts_before_replan`, read after every layer has been
   applied, and is floored at 1 for a project that repairs nothing routinely.
   `approvals.publishing`, `approvals.work_items`, and `approvals.releases` are
   the only approvals with a harness default, because they are the ones added
   after configurations existed, and a file that mentions none of them loads
   rather than failing over a key that did not exist when it was written. The
   bundle states all three at the same value the default holds, so extending it
   inherits none of them and upgrading the executable moves none of them. All
   three are opt-ins, and an opt-in that arrived by inheritance would not be one.
   `releases: human` is exactly what a file written before it got: a person cuts
   every release.

   **`work_items` is the one of the three that changes an existing project's
   behavior**, and it is worth being plain about rather than leaving to be
   discovered. `publishing: human` is exactly what a file written before it got:
   the harness publishes nothing. `work_items: human` is not, because before this
   key existed the Lead Product Manager could admit work to the backlog **directly**,
   through its `create` action, and you were told afterwards rather than asked.
   That direct admission is now refused at `human`, so a project that upgrades
   and leaves the key alone has a Lead Product Manager that proposes work instead of
   admitting it. Nothing is lost when it does — the proposal is put to you and
   approving it creates the item — and the trade is deliberate: a `human` setting
   that left this door open would be a gate the Lead Product Manager could walk around
   by choosing the other one. An operator who wants the old behavior back sets
   `work_items: automatic`, which admits directly again against goals they have
   approved. See [what reaches the queue](goals.md#what-reaches-the-queue).
2. **The built-in bundle**, named by `extends`, and present only if a project
   asks for it. Today the only bundle is `builtin:v1`. It supplies `execution`,
   `approvals`, and the five default agents. It deliberately supplies no
   `product`, no `checks`, and no `landing_checks`, because those describe the
   project rather than the harness.
3. **The project configuration**, which overlays whatever it names.

A configuration with no `extends` key — which is what `yoyo init` writes — is a
complete standalone file: it inherits nothing but the harness defaults, and must
declare everything it needs.

`version` is the one field a project never inherits. It must be declared even
when `extends` names a bundle that declares its own, because a version taken
from the bundle would let a file written against a different schema load as
whatever the bundle happened to say — which is what the version exists to
prevent.
## Merge and removal semantics

These describe how a project that uses `extends` combines with the bundle
beneath it. A configuration `init` wrote has no layer beneath it, so it is read
as written: an agent is present because it is in the file, and absent because it
is not.

- A field a layer does not mention is **inherited** from the layer beneath it.
- A field a layer does mention **replaces** the inherited value. This includes an
  explicit zero, such as `repair_attempts_before_replan: 0`.
- `checks` is replaced as a whole list rather than concatenated. Checks gate
  integration, and a silently merged list is not the gate either layer described.
- `agents` is merged by agent name. An override names only the fields it changes:

  ```yaml
  agents:
    developer:
      model: claude-opus-5-20260514
  ```

  The developer keeps its inherited role, backend, instance count, and persona.
- A `persona` override **replaces the inherited persona completely** and must
  supply both `version` and `path`. Half of one persona and half of another is
  guidance nobody wrote.
- An agent name the bundle does not define creates a new agent, which must then
  supply everything an agent requires: role, backend, and model selector.
- `disabled: true` removes an inherited agent:

  ```yaml
  agents:
    architect:
      disabled: true
  ```

  Removal is explicit, so an agent is never lost by being accidentally omitted.
  Validation still enforces the roles the invoked workflow executes: at least one
  developer agent always, and a reviewer agent whenever `approvals.integration`
  is `automatic`. Disabling either is a validation failure, not a way to skip
  review.

### What fails closed

These are all errors, reported before any work is claimed:

- a missing `version`, or a `version` this executable does not implement;
- an unknown key anywhere in the file, including a misspelled agent field;
- a `state_root`, at the top level or under `execution`, which is refused by
  name because it belongs in the machine's own
  [`machine.yaml`](#where-the-harness-keeps-its-state-state_root);
- an unknown bundle in `extends`;
- a `disabled: true` entry that also configures fields, or that names an agent no
  layer defined;
- a persona override missing `version` or `path`;
- a usage-limit pause bound that is not a duration, or that is negative — `0`
  is accepted, because "never wait" is a choice somebody can mean;
- an `execution.check_timeout`, `execution.check_stage_timeout`, or
  `execution.landing_check_timeout` that is zero or negative, since a check or
  a stage with no bound holds a worktree, a claim, and a developer seat open —
  or a landing checkout — for as long as it runs; and an empty entry in
  `checks` or `landing_checks`, which is a line the shell would run as nothing
  and report as passed;
- an `execution.redeploy_drain_limit` that is zero or negative, since a drain
  with no bound is the two-hour wait on a deploy that the bound exists to end;
- an `execution.build_move_interval` under one hour or over twenty-four hours:
  under an hour is a move on nearly every landing again, and over a day leaves
  fixes waiting in a build nobody runs;
- an `execution.factory_stall_after` that is negative, which describes no
  limit anybody could mean; zero reads the two-hour default;
- an `execution.could_not_run_before_status` that is negative, which describes
  no number of changes; zero reads the default of three;
- an `execution.usage_window_degraded_percent` that is negative or over 100,
  which describes no share of a window; zero reads the default of 90;
- a `triage.stuck_merge_age` that is not a duration, or that is zero or
  negative — unlike the usage-limit pauses, "no time at all" is not a choice
  anybody can mean here;
- a negative `triage.review_rounds_cap`, or a `triage.repair_grant_attempts`
  below 1 — a cap of `0` is a choice and is accepted, a grant of `0` is not;
- an `execution.remote` that is empty or is not a plain remote name, since it
  reaches a `git push` command line; and an `execution.push_remote` that is set
  and is not one, for the same reason — leaving it out is what says your run
  branches go to `execution.remote`;
- a `product.specifications` that is empty, absolute, or climbs out of the
  repository, since it decides what every role reads as the product's intent; and the same of
  `product.invariants`, `product.designs`, and `product.decisions`, since they
  decide which documents the harness treats as canonical artifacts and which
  paths a developer's change may not touch. This is the check on the text; the
  same four are checked again against the filesystem when something writes into
  them, which is where a symlink out of the repository is caught, and which is a
  refusal at the point of the write rather than at load;
- an `intent.repository` that is absolute or climbs out of the directory the
  configuration is in, or that is written in a configuration committed with the
  repository, since a [companion intent
  repository](#keeping-the-intent-outside-the-repository-too) exists to keep
  everything of Yoyodyne's out of it;
- a `product.shipped_documentation` entry that is empty, absolute, climbs out of
  the repository, or is not a Markdown file, since every entry is read into the
  Lead Product Manager's context as a description of what the product ships;
- a program manager's `remit` path that fails any of the persona rules below, in
  the same words; a `lane`, `remit`, or `triggers` on an agent of any other role;
  a `lane` the tracker would not carry, or one two agents name; a
  `triggers.every` under `5m`; a `triggers.full_pass_every` under `1h` or over
  `12h`; and a `triggers.on` entry outside `landings`,
  `admissions`, and `stoppages`, or named twice; and a `recurring_tasks` entry
  named for a program manager instance, since an instance's passes are recorded
  and paced under its own name;
- a `recurring_tasks` entry whose `full_pass_every` is under `1h` or over
  `12h`, since it is the longest a role goes without a full pass while passes
  with nothing new are skipped;
- a `recurring_tasks` entry named `maintenance`, which is the name the
  supervisor's maintenance pass records its passes and paces its cadence under;
- a persona path that is absolute, traverses upward, is not Markdown, is missing,
  is empty, or resolves through a symlink to somewhere outside `.yoyodyne`;
- a `role` that is neither one of the harness's six nor the name of a
  [role definition](#protected-role-definitions), which is how a typo in an
  agents block is caught: the message names what was written and lists what could
  have been meant. Adding a shipped role is a change to the harness, not to this
  file;
- an agent whose `role` names a role definition nobody has activated, one
  amended since its latest activation, or one whose file has moved since — refused
  by every command that reads or runs agents rather than by `yoyo role`;
- a [role definition](#protected-role-definitions) with no single shipped base
  role, an unknown primitive or key, duplicate or conflicting tool lists, a
  removal the base role does not hold, or an addition of checks, review evidence,
  publication, integration, its lease, or a run operation; and a definition
  larger than 32 KiB, with more than one YAML document, or reached through a
  broken or escaping link;
- a role and backend combination the provider does not support, including a
  project-declared provider asked to serve a role it did not declare;
- a provider asked to hold tool access it did not declare, such as a developer on
  a provider that declared only `read-only`, or a reviewer on one that declared
  only `worktree-write`. Both built-ins declare both kinds of access.
  [Provider plugins](../provider-plugins.md#capability-validation) describes how
  their adapters enforce read-only roles;
- a `providers:` entry that names no adapter or one this build ships none for,
  serves no role, holds no tool access, names a role or kind of tool access the harness does
  not have, reads nothing its provider says, or tries to replace a backend this
  build ships;
- an `execution.developer_slots` list longer than `max_concurrent_developers`,
  since a preference for a slot the capacity does not have is one nothing would
  act on; and a slot preferring a label the tracker would not carry — anything
  but one identifier-shaped word — or naming one label twice;
- an `execution.developer_models` entry naming a label the tracker would not
  carry, naming no usable model selector, or naming a label an earlier entry
  already mapped — since the first match in the mapping's order is what an item
  takes, so a second entry for one label is one nothing would ever reach;
- a `recurring_tasks` entry whose `model` is written and is not a usable model
  selector, by the rule and with the reason an agent's `model` is refused;
- an agent's `effort` that its provider does not accept — anything but `low`,
  `medium`, `high`, `xhigh`, or `max` on Claude Code, or a level absent from
  the configured Codex model's catalog — with the refusal naming accepted levels;
- any effective configuration that fails validation, even when every individual
  layer looked reasonable — for example `max_concurrent_developers` above the
  configured developer instances, or automatic integration with no checks;
- `slack.enabled` with no `slack.channel`, a channel that is not a channel id
  or name, or an entry under `slack.avatars` keyed by something that is not a
  role or `harness` or valued as something that is neither an emoji shortcode
  nor an https image URL — all checked whether or not reporting is switched on,
  so a typo is found now rather than on the day somebody turns it on;
- a `services` entry that is not one of the four the product has, a
  `services.maintenance.every` under a minute, a
  `services.dashboard.port` outside 1–65535, a `services.dashboard.bind` that
  is not an IP address, a `services.dashboard.token` that is not `generated`,
  `keychain`, or `file`, an entry under `services.dashboard.allowed_hosts` that
  is empty, carries a scheme, a port, or a path, or is named twice; and two
  combinations: `services.slack` enabled while `slack.enabled` is off, since a
  sink for a project that reports nothing has nowhere to post, and a
  `services.dashboard.bind` outside loopback with a `generated` token, since a
  token printed to one terminal is unusable from the other device the bind
  exists for — all checked whether or not the service is enabled, so a typo is
  found now rather than on the day the product is started;
- an `operators` entry that binds no namespace at all, binds one that is not an
  address, a forge account, or a Slack member id, names a grant the harness does
  not have, or binds an identifier a second human already bound — and two humans
  holding `own-intent`, since intent has one owner;
- an `accounts` alias that is not an identifier, a description longer than 200
  bytes, an agent whose `account` names an alias the mapping does not declare, a
  `pool` that is neither `active` nor `reserved`, a negative
  `weekly_budget_usd`, or a mapping whose every account is reserved — a pool
  with an empty active half is one every run falls out of;
- an `accounts` entry whose `provider` is not a provider this project names, and
  an agent no configured account could authenticate — a project whose accounts
  hold one provider's logins and whose developer runs on another is a project no
  run can ever be served for.

## Services

Slack, the dashboard, the scheduler, and the maintenance pass are parts of one
product rather than independent small tools, and `services` is where a product
declares which of them it runs. It is the
[management-and-supervision design's](../designs/management-and-supervision.md)
supervision tree written down: one supervisor per product, and these are its
children, started together by
[`yoyo start`](../operations.md#starting-the-product-and-stopping-it) and stopped
together by `yoyo stop`.

```yaml
services:
  slack:
    enabled: false
  dashboard:
    enabled: false
    port: 8765
    bind: 127.0.0.1
    allowed_hosts: []
    token: generated
    hide_summary: false
  scheduler:
    enabled: true
  maintenance:
    enabled: true
    every: 10m
    tracker_cleanup:
      every: 24h
      grown_megabytes: 128
      max_hold: 15s
    tracker_limits:
      store_megabytes: 1024
      listing_seconds: 10
```

**Every service is present whether or not a project mentions it.** The section is
the product's shape rather than a list a project appends to: a service a file
leaves out is at its harness default, and `yoyo config show --origins` names
`harness-default` for each value the file did not write. `yoyo init` writes the
whole section, live rather than commented, so a generated file shows every part
there is and the state each starts in. The four names are the four the product
has; a fifth is refused when the file loads, and the refusal names the four.

| Service | What it is | Default |
| --- | --- | --- |
| `slack` | the reporting sink, the `yoyo slack` process that holds this product's two tokens | off |
| `dashboard` | the read model served to a browser, which also takes the operator's own approve, decline, and ask from a person's own token ([deciding from the page](../operations.md#deciding-from-the-page)) | off |
| `scheduler` | the watch loop — `yoyo work --watch` — that reads the queue and starts what is ready | on |
| `maintenance` | the periodic pass that keeps the installation converged: reconciling interrupted runs, catching the checkout up, restarting what stopped | on |

The two that are on need nothing that is not already in the file: they are the
harness's own loop and its self-maintenance, and a product started with neither
starts nothing. The two that are off each need something arranged outside it
first — Slack a workspace, an app, and two stored tokens; the dashboard a port
somebody means to open — and each is switched on by the operator who arranged
it, as reporting itself is.

**The section declares and never widens.** There is no key here for a
capability, a tool, an account, or an authority. A part started from this
section holds exactly what it holds when started by hand: the sink still reads
its tokens from the store only its own launch looks at, the dashboard still
refuses every request without its bearer token, the scheduler still passes every
gate a `yoyo work --watch` you started yourself would pass.

**`services.slack` requires reporting to be on.** A sink started for a project
whose [`slack`](agents.md#reporting-to-slack) section is off reads a stream and then
discovers it has nowhere to post, so enabling the service over reporting that is
off is refused when the file loads, naming both ways out. Whether the two tokens
are actually stored cannot be read from the file; `yoyo doctor` asks, under
`service:slack`, and a service enabled with its tokens missing is a warning
carrying the command that stores them — the same command its `slack-secrets`
finding carries, because both are one question asked of one keychain.

### Saying when the factory has stalled

The supervisor reads, once a minute, whether the factory as a whole has
stopped: **no work pulled and no recurring pass succeeding for longer than
`execution.factory_stall_after`** (two hours by default), while passes are
still being attempted. It reads the run records and the sweep log and nothing
else — no tracker, no provider — so it still answers when the tracker is what
stopped everything, and it runs in the supervisor rather than in the watch
because the watch's passes are the ones a stall means are failing.

```yaml
execution:
  factory_stall_after: 2h   # the default
```

When a stall begins the harness files a critical report in its own voice,
naming how long nothing has happened, when work was last pulled and a pass
last succeeded, and what each recurring task's latest attempt failed on. The
report goes into the pile every report goes into, so it is put in front of the
operator wherever critical reports reach them and delivered to the Lead Product
Manager as a turn of its own. It is filed once per stall: the stall is recorded
in `factory-stalls.jsonl` under the product's state directory, and readings
that agree with a standing stall write nothing. The first pull or successful
pass closes it and files a note saying what cleared it. While it stands,
`yoyo status` names it on the attention line as the harness's move.

A pass counts as successful when it took a turn and the role gave an account
of it. The operator's pause is never a stall, and neither is a product whose
recurring tasks are switched off over an empty queue: with no pass attempted
there is nothing failing to report.

Only a pull or a successful pass that started after the stall began closes
it. Pausing the harness during a stall, raising the limit past it, or a sweep
log that no longer holds the failures each stop the reading calling it a stall,
but none of them is a recovery: the record stays open, no note is filed, and
when the pause lifts the same stall is still the one standing rather than a
new one reported again. While the pause is on, `yoyo status` names the pause
rather than the stall.

### The dashboard's entry

The dashboard is the one service with more to say than a switch, because it
listens. Its entry is the
[observability-and-dashboard design's](../designs/observability-and-dashboard.md#web-security-the-repositorys-web-service-conventions-established-here)
web-security conventions made configuration:

- **`port`** is the TCP port it serves on, a fixed number rather than one the
  operating system chooses, because a supervised child that came up on a
  different port after every restart is one nobody can bookmark or reach from
  another device. It must be between 1 and 65535; `8765` is the default, the
  same port the operations guide has used as its example of one worth
  bookmarking.
- **`bind`** is the address the product's dashboard listens on, as an IP
  literal, and it must be a loopback address: the dashboard listens only on
  this machine. `127.0.0.1` is the default — the IPv4 loopback address by
  number rather than as `localhost`, which on some machines resolves to the
  IPv6 loopback first — and `::1` is the other address most machines have. An
  address that is not loopback, an interface address or a wildcard, loads,
  and the dashboard refuses it: the supervisor starts no dashboard on it and
  says why on `yoyo status` and `yoyo doctor`. A change to it is applied by the
  supervisor replacing the dashboard, as a change to `port` is. A dashboard
  started by hand binds `127.0.0.1` whatever this says.
- **`allowed_hosts`** are the names, beyond the bound address itself, a request
  to the product's dashboard may carry as its Host and Origin — a name in
  `/etc/hosts` that points at the loopback address, say. Empty is the bound
  address alone, with `localhost` beside it. Each entry is a host name or an IP
  address written without a scheme, a port, or a path, because a request's
  Host header is compared against it with the dashboard's port added, and an
  entry with a port in it would match nothing a browser sends. The list is
  replaced wholesale rather than merged, as `checks` is. The running dashboard
  reads it again every ten seconds, so a name added or taken out is answered or
  refused with no restart. A dashboard started by hand answers to `127.0.0.1`
  and `localhost` alone.
- **`token`** is where the bearer token every request has to carry comes from.
  A change to which source it names is applied by the supervisor replacing the
  dashboard; a change to the token inside the store is read by the running
  dashboard every ten seconds, and a token taken out of its store is refused
  until it is stored again.
  It is a reference and never the token: this file is committed, and a secret
  in it would be a secret in the repository. `generated` is the default and the
  loopback arrangement — a token made at each start and printed once where you
  can read it. `keychain` and `file` are the two stores the Slack tokens already
  use, under names that carry the product: the keychain item
  `yoyo-dashboard.<product id>` under the account `yoyo`, or the file
  `<state root>/projects/<product id>/state/dashboard.token`. `yoyo dashboard`
  reads the one named and serves under it, printing where it was read from
  and never the value, so a stored token outlives a restart; a store that does
  not hold it refuses to start with the command that stores it.
- **`hide_summary`**, when `true`, leaves the Summary section that opens the
  page off it entirely, and the other sections keep their order; unset or
  `false`, the page shows it. It hides the section only: `/api/summary` still
  answers and `yoyo status` still opens with the same summary. A change to it is
  applied by the supervisor replacing the dashboard.

**A bind outside loopback with a generated token is refused when the file
loads**, with the reason, because a token printed to this terminal is exactly
what another device cannot read. With a stored token such a bind loads, and the
dashboard refuses to listen on it, since it listens only on this machine.
Every other rule holds at every address: the token is required on every
request, Host and Origin are checked against the bound address and the allowed
hosts, and the shared token reads and never acts. The shared token acts on nothing at
any address: the only acts the page takes are [the operator's own approve,
decline, and ask](../operations.md#deciding-from-the-page), and each still needs
a person's own bound token and the grant the decision needs, wherever the page
is reached from. Transport is plain HTTP in V1, so the opt-in is for a network
you trust.

`yoyo doctor` reports the dashboard under `service:dashboard`: off, on with a
generated token, or on with a supplied token that it looks for in the store the
entry names — the keychain item by name, the file by its existence and mode —
without ever reading the token. A supplied token that is not there is a warning
carrying the command that stores it.

It reports Node separately, under `node`, and asks about it whether or not the
service is enabled — what it goes by is the repository carrying
`internal/dashboard/testdata/render.js`, which is what says a product ships the
dashboard at all. The page is drawn by a script only Node can run, so a machine
without Node produces none of the page's evidence and its render check fails
there: an absence is a problem carrying the install command, an absence that
`YOYODYNE_NODE_UNAVAILABLE` declares is a warning quoting the declaration, and
every product that ships no dashboard is asked nothing. Nothing in the harness
sets that variable — it is set by whoever built an environment deliberately
without Node, so a declared absence is a decision somebody made rather than a
tool nobody installed.
[Working on yoyo itself](../developing-yoyo.md#what-a-checkout-needs-besides-go)
names Node as the development dependency this is about.

**[`yoyo start`](../operations.md#starting-the-product-and-stopping-it) is what
acts on this section.** It starts the product's supervisor, which reads the
section and starts every enabled part: the Slack sink as `yoyo slack ensure`
starts it, the dashboard as `yoyo dashboard` on this entry's `port`, and the
scheduler as `yoyo work --watch` under its own watch lease
([the dashboard under the product](../operations.md#the-dashboard-under-the-product)
says how the dashboard is confirmed, where its token is, and which of these
settings it applies while it runs). The product's dashboard listens on this
entry's `bind` and answers to its `allowed_hosts`; one started by hand binds
`127.0.0.1` and reads neither. `token` it reads whether or not the entry is
enabled, so that
[a stored token outlives a restart](../operations.md#watching-from-a-browser-the-dashboard).
Declaring the whole section now is what lets that command and the resident that
starts with the machine read one statement rather than two.

**The maintenance pass is the supervisor's own**, not a process it starts:
every `services.maintenance.every` — ten minutes by default, a minute at the
shortest, measured from the last pass as a recurring task's cadence is — it
runs `yoyo reconcile`, answers the restart requests program managers made, and
takes up a build deployed over the supervisor, restarting nothing while the
provider cannot be reached or is not logged in. Each pass is recorded in the
sweep log under the name `maintenance`, which is why a
[recurring task](agents.md#recurring-tasks) may not take that name.
[The maintenance pass](../operations.md#the-supervisors-maintenance-pass) says what
each step does and what it never does. With the part off, `yoyo reconcile` is
yours to schedule.

**The pass also cleans up the work tracker's store** in the primary checkout,
under `services.maintenance.tracker_cleanup`. A cleanup is due once the store
has grown `grown_megabytes` (128 by default, between 1 and 16384) since the
last cleanup left it, or `every` (a day by default, an hour at the shortest)
has passed since then, whichever is first. The growth is measured from what
the last cleanup left rather than as a size the store may not pass, because a
cleanup keeps the whole history, and a store whose history alone outgrew a
fixed size would be cleaned up on every pass and shrink by nothing. A store
past `tracker_limits.store_megabytes` is cleaned up every four hours as well,
and a due cleanup that has waited an hour for other processes to let the
store go starts beside them, so a store past its bound is cleaned up within
six hours however busy the developer slots keep it; neither is a setting.
`max_hold` (15 seconds by default, between one second and 20) is the longest
one cleanup holds the store; every tracker call waits behind it, so it is
stopped when this runs out and the next pass finishes the work, and its
ceiling leaves a call that arrives the moment a cleanup starts most of its own
time limit (two minutes) to answer in. Each bound is read on its own, so a
file may set one and keep the defaults for the others.

**Every pass also holds the store to `services.maintenance.tracker_limits`.**
`store_megabytes` (1024 by default, between 1 and 1048576) is the largest the
store may be on disk, and `listing_seconds` (10 by default, between 1 and 120)
the longest one of the harness's own listings of open or ready work may take,
its wait for its turn behind the harness's other tracker calls included,
which is the limit [the design on keeping the store small](../designs/tracker-store-size.md)
sets. The pass reads the size from the disk and the listing times from the log
of tracker calls every harness process keeps, over the hour before the pass,
so the check asks the store nothing. Within both, the pass's `limits` step says
the figures; past either, it says which and by how much, with the setting that
bounds it, and warns the development manager once, and again only when what is
past its bound changes. Each bound is read on its own.

## Extending a built-in bundle

Inheritance is a supported capability, and a project that wants it writes
`extends` instead of the agents:

```yaml
version: 1
extends: builtin:v1

product:
  id: example
  repository: .

checks:
  - go test ./...

agents:
  developer:
    model: claude-opus-5-20260514
```

That file inherits the five agents and their personas from the bundle, overlays
the one field it names, and is subject to the [precedence](#precedence) and
[merge rules](#merge-and-removal-semantics).

**What it buys, and what it costs.** Upgrading the executable upgrades the
defaults and the personas the project did not override — which is exactly what
an explicit configuration gives up. What the project pins, it keeps, because a
project value always wins over the bundle. New bundle versions are added under
new names rather than by changing an existing one, so `builtin:v1` keeps meaning
what it meant when a project adopted it. Neither shape depends on where Yoyodyne
lives: both travel with the repository, and neither needs the Yoyodyne source.

Yoyodyne ships the explicit shape because its operator edits agent properties
often and wants the effect of an edit obvious. A fleet of projects that should
improve together is the case `extends` is for.
[Portable agent configuration](../designs/portable-agent-configuration.md) is the
governed design that answers what a project owns versus inherits, how the two
shapes convert into each other, and how a bundle improvement reaches a project
that materialized its defaults; the architect ratified it on 2026-09-01. Its
baseline and report exist: `config.lock` records what the template supplied,
`yoyo config drift` sorts each value into `unchanged`, `yours`, `available`, or
`conflicting`, and `doctor` and `config validate` speak the `available` ones
unprompted on stderr, silently when none, without changing exit codes. Where the
[Slack sink](../reporting.md#what-arrives-as-a-direct-message) is running it says
the same `available` values without anybody running a command: one direct
message per reading that finds something new, each improvement said once and
never repeated. A persona the template ships with no agent bound to it — the
program manager's — is recorded by its file, as `personas.program-manager.text`,
and it is the one value compared where the baseline has no record of it: a
baseline taken before the template shipped the file says the template supplied
nothing there, so a project that has no such file is offered it as `available`,
and one that wrote its own by hand reads as `conflicting` and is not spoken
about unprompted. A project without a
baseline hears nothing about its values until `yoyo config baseline` writes it,
which touches nothing else and starts level.

One comment is compared as well, because it can go wrong the way a value can:
the one above `product.specifications`, which said until 2026-09-27 that only the
product manager reads that directory. It is compared against the template's own
record of every wording it has written there rather than against a baseline, so
`yoyo config drift` names it — as `product.specifications.comment`, with both
wordings — in a project with no baseline too. A copy still carrying an earlier
wording is `available`; one somebody rewrote, or deleted, is `yours` and is never
offered anything. It is not in the unprompted notice. Nothing is adopted for you; `materialize`,
`extract`, and `adopt` do not exist yet.
### Converting an inheriting configuration to an explicit one

1. Record what you have now:
   `yoyo config show --effective --origins > before.txt`.
2. Run `yoyo init --force`. It overwrites everything `init` writes, so commit or
   stash first.
3. Re-apply what was yours: `checks`, your approval policy, and any agent field
   you had overridden. The generated file states each of them in place, so this
   is editing values rather than re-expressing deviations.
4. Run `yoyo config show --effective --origins` again and diff it against
   `before.txt`. Every origin should now be the project file, apart from the few
   a generated file leaves derived — the repository id, the triage repair grant,
   and each agent's capability set, which is the harness's registry, or the
   role definition the agent fills where it names one —
   and no effective value should have moved except the persona sources, which are
   now paths inside your repository.
## Migrating from `.yoyodyne.yaml`

A `.yoyodyne.yaml` file still loads, so migration is optional. The simplest
route is to run `yoyo init` and re-apply what the old file said:

1. Run `yoyo init`, which writes `.yoyodyne/config.yaml` and the personas.
2. Copy your `product`, `checks`, `approvals`, and any agent deviations from
   `.yoyodyne.yaml` into the generated file, editing values in place.
3. Run `yoyo config show --effective --origins` and confirm the effective values
   match what the old file produced.
4. `git rm .yoyodyne.yaml`. While both exist in one directory the directory form
   wins, so a half-finished migration cannot silently keep using the old file.

Personas move to `.yoyodyne/personas/` and are referenced relative to the
`.yoyodyne` directory.
## Inspection

```sh
yoyo config validate                      # validate the discovered configuration
yoyo config show --effective              # the values that actually apply
yoyo config show --origins                # where each value came from
yoyo config show --effective --origins    # both
yoyo config show --effective --json       # machine-readable
yoyo config drift                         # what the template improved
yoyo config baseline                      # record one where there is none
```

`config show` prints the layers it applied, the revision of the configuration in
force, the effective configuration as YAML, and, with `--origins`, one line per
value. Persona bodies are reported as a source and a byte count rather than
inlined, so the output stays readable.

The revision — `cfg-` and a digest, printed by `config validate` as well — is
what a run record names when it says which configuration set it up. It is derived
from the effective values rather than declared, so nobody has to remember to bump
it: two configurations whose effective values agree share a revision however
differently their files are written, a bundle upgrade that moves a default moves
the revision with it, and a changed persona moves it too, because a persona is
what every prompt is written against.

Origins use these values:

| Origin | Meaning |
| --- | --- |
| `harness-default` | No layer supplied the value; the harness filled it in. |
| `builtin:v1` | Inherited from the built-in bundle, by a project that uses `extends`. |
| a file path | Supplied by that project configuration file. |
| `derived:product.id` | Computed from another configured value. |
| `derived:execution.repair_attempts_before_replan` | A triage repair grant no layer stated, which follows the effective repair budget. |
| `derived:accounts` | An agent's `account` no layer stated, which follows the single account the mapping declares. |
| `registry:role-capabilities` | An agent's `capabilities`, read off its role in the harness's registry. No layer states it and none may. |
| `role-definition:<name>` | An agent's `capabilities` and `definition`, read off the activated [role definition](#protected-role-definitions) its `role` names. No layer states either and none may. |

An unexpected effective value is therefore a two-command diagnosis: `--effective`
says what the value is, and `--origins` says which layer is responsible for it.

In a project `init` wrote, the answer is the project file for every configured
value. The exceptions are the values the generated file leaves to follow
something else: `derived:product.id` for `product.repository_id`,
`derived:execution.repair_attempts_before_replan` for
`triage.repair_grant_attempts`, and `registry:role-capabilities` for every
agent's capability set — or `role-definition:<name>` for an agent filling a role
definition — which is the harness's rather than any file's. Nothing
reports `builtin:v1`, and nothing reports `harness-default`, because the
generated file writes down every value the harness would otherwise have filled
in. So an origin that is none of those means the configuration is inheriting
something, which is worth looking at.
