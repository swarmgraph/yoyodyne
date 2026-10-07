# Yoyodyne configuration

**A Yoyodyne project owns its configuration outright.** `yoyo init` writes a
complete `.yoyodyne/config.yaml` — every agent, backend, model selector,
provider account, instance count, and persona reference stated in the file — and copies the
personas themselves into `.yoyodyne/personas/`. Nothing is inherited at load
time, so what the file says is what runs, and an edit to it is an edit to the
harness's behavior with nothing in between.

The executable still contains a versioned, read-only bundle of agent definitions
and personas. It is the **template `init` generates from**, not a layer
underneath your project. A project therefore never needs access to the Yoyodyne
source checkout, and nobody reading its configuration has to be told where a
value came from.

**What owning your defaults costs.** A later Yoyodyne that improves a persona or
corrects a model selector does not change a project that already has its own
copy — nothing is inherited, so nothing arrives. It does say so, and moving a
value stays yours. That report, and inheritance for projects wanting the other
half of the trade, are both under
[Extending a built-in bundle](#extending-a-built-in-bundle).

## Creating a project configuration

```sh
yoyo init                              # configure the current directory
yoyo init --directory path/to/project  # configure another one
yoyo init --product example            # name the product explicitly
yoyo init --tracker-remote <url>       # sync the tracker somewhere else
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
its toolchain. See [What `init` proposes for `checks`](#what-init-proposes-for-checks)
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
- A project with no Git remote, or one whose `bd` is not initialized yet, is
  told what to run rather than failing: the configuration is written and valid
  either way, so `init` still exits 0 and `init --json` reports the outcome
  under `tracker` — `configured`, `unchanged`, `skipped`, or `failed`.

A synced tracker is one operator's backlog surviving their machine, and not yet a
team sharing one: two people each running their own harness against one
repository is
[not supported](team-mode-scope.md#what-v1-supports-meanwhile), because the
coordination around the backlog — claims, reports, directives, and the budgets
below — stays on the machine that made it.

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
  roles/               # optional, protected role definitions; loaded but inert
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
  projects/<product id>/
    repository.json      # the binding: which repository this id is
    config.yaml          # the configuration, where the repository does not carry it
    personas/            # its personas, beside it
    state/               # runs, conversations, spend, the docket, memory, reports
    worktrees/           # the developer worktrees of the bound repository
```

`config.yaml` and `personas/` are there only for a project whose configuration
is [kept outside its repository](#keeping-the-configuration-outside-the-repository);
a committed `.yoyodyne/` is read where it is. `repository.json` is the
[binding](#a-product-id-names-one-repository-on-the-machine). A home the earlier
builds laid out keeps each product's records under `products/<product id>/` and
its worktrees under `worktrees/<product id>/` instead, and is read that way
until it is moved; the harness never moves it on its own. Nothing of the
harness's is kept anywhere else on the machine: the configurations home earlier
builds kept at `~/.config/yoyodyne` is no longer read.

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

services:           # the parts of the product, each on or off; see Services
  slack:
    enabled: false
  dashboard:
    enabled: false
    port: 8765
    bind: 127.0.0.1
    allowed_hosts: []
    token: generated
  scheduler:
    enabled: true
  maintenance:
    enabled: true
    every: 10m

checks: []          # yours to write; a run with none is refused
landing_checks: []  # what runs whole, once per landing; see "Where the whole suite runs"
path_checks: []     # checks a change runs only when it touches what they vouch for

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
[effort level](#an-agents-effort-level) that model is asked to think at, the [provider
account](#provider-accounts) it runs under, an instance count, and a persona file
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
in the harness's own registry, there is no `capabilities` key to put in a
configuration, and a file that writes one is refused like any other key that
does not exist. The set of role names is fixed for the same reason —
the tools each role may use, a reviewer's absence of any included, are derived
from the name — so `role` must be one of the six: `product-manager`,
`architect`, `development-manager`, `developer`, `reviewer`, or
`program-manager`, and anything else is
[refused when the configuration loads](#what-fails-closed).
The sixth is the [program manager](designs/program-manager.md): an agent filling
it watches one outcome across the others and may change work only inside its own
lane. `yoyo init` configures none — an instance is a lane and a remit somebody
chose, written in three keys only that role's agents carry
([a program manager instance](#a-program-manager-instance)) — and every tracker
write the role holds is confined to that lane, read off each item as the action
runs ([a program manager's lane](conversation.md#a-program-managers-lane)). It
may also record [a request that a part be restarted](operations.md#starting-the-product-and-stopping-it),
which the supervisor's [maintenance pass](operations.md#the-supervisors-maintenance-pass)
answers at its next pass.
[Talking to the other agents](conversation.md#talking-to-the-other-agents) states
the table itself.

`backend` is `claude-code` or `codex` unless your project declares one of its
own. Both built-ins serve all six roles. Codex uses worktree-write access for
developers and native read-only inspection for reviewers and management roles,
with tool network access, escalation, and external integrations disabled.
[Provider plugins](provider-plugins.md#capability-validation) describes the boundary
and its limits. A project running a fork, a proxy, or
a variant of a provider yoyo already speaks can describe it under a top-level `providers:` key and name it here: which
compiled adapter launches it, which executable that adapter runs, which roles it
serves, which kinds of tool access it can hold them to, and how to read what it says
about rate limits, retries, and reset times. A declared provider describes and
decides nothing: whether to wait, how long, and against which budget stay the
harness's, because those are what the `execution.usage_limit_*` settings below
mean. [Provider plugins](provider-plugins.md) is the format and its limits — in
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
refuses the configuration whole. A valid file remains inert: it changes no
agent's capabilities, contract, or effective configuration revision, and an
agent's `role` still accepts only a shipped role.
[`yoyo role activate`](operations.md#activating-a-role-definition-and-reading-its-history)
records a person's activation of the file's exact content digest.
`yoyo role list` compares the current file with that decision, and
`yoyo role history` retains every activation. Binding an agent to the activated definition is
subsequent work, so activation changes no agent's authority yet. A persona
grants nothing through this file or any other.

Definitions live beside the configuration in `roles/`, following the same
directory order as personas for an external or legacy configuration. Where
there are two possible directories, the first file for a name wins, and a bad
first file never falls back to the second. A missing directory is allowed;
dangling links, files that are not regular files, and links escaping the roles
directory are refused.

**Only a person writes these files.** The
[protected-path gate](#protected-paths-in-a-developers-change) refuses changes
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
  held to the rule a [slot's preferred label](#a-developer-slot-that-prefers-a-label)
  is — letters, digits, dots, underscores, and hyphens, up to 64 bytes, compared
  exactly. **Two instances naming one lane are refused when the file loads**, and
  the refusal names both agents and the lane: a lane has one owner or it is not a
  lane. `yoyo agent list` prints the lane beside the agent's name —
  `reliability-pm (program-manager, lane reliability)` — and carries it as `lane`
  in `--json`.
- **`remit`** says what the lane is for, as `version` and `path`: a Markdown file
  under the configuration directory, held to every rule a [persona](#personas) is
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
  duration floored at the [recurring-task](#recurring-tasks) minimum of `5m` for
  the same reason — every pass is a conversation turn — and `on`, a list drawn
  from the closed set `landings`, `admissions`, and `stoppages`. An `every` under
  the floor, an `on` entry outside the set, and an entry named twice are refused
  when the file loads; leaving `every` out is no scheduled pass. A later layer's
  block replaces an inherited one whole, as the failover block does.

**The lane is enforced, and the triggers take passes.** The keys load, are
validated, are reported by `yoyo agent list` and `yoyo config show`, and the
remit is delivered. The lane is what confines the instance's tracker writes: a
creation carries the lane label in the write that admits it, and every other
write is refused on an item not carrying it at the moment of the act — the rules
are [a program manager's lane](conversation.md#a-program-managers-lane). An
instance configured with no `lane` has nothing inside one, so all of its tracker
writes are refused. A `yoyo work --watch` session reads the triggers and takes
the instance's passes as recurring-task firings —
[a program manager instance's passes](#a-program-manager-instances-passes) says
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
   whose [binding](#a-product-id-names-one-repository-on-the-machine) names the
   repository the current directory is in, as
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
`product.repository` and are refused if they leave it.

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
rather than scaffolded somewhere nothing commits. And of the machine home below,
which is a declared root like any other: a write that resolves out of it is
refused rather than landing where nothing looks for it.

## Keeping the configuration outside the repository

A contributor to a repository they do not own has the configuration as theirs
rather than the project's, and a pull request adding a tool directory nobody
asked for is a pull request about the tool. There are two ways to keep it out.

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
as well.

**Or keep it outside the repository entirely.** `yoyo init --external` writes
the configuration this machine keeps for that repository, and nothing at all
into the repository:

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

Four things are worth knowing about it:

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
  checkout leaves the binding naming where it was; run
  `yoyo project bind --product <product id>` from where it now is, which is
  how the configuration is found again.
- **`product.repository` is written absolute.** An external configuration has no
  project directory above it for a relative path to resolve against. The
  artifact directories are unaffected: `specifications`, `invariants`,
  `designs`, and `decisions` resolve against `product.repository` and go on
  naming directories inside the repository being worked on.
- **It writes no artifact-home indexes.** `init` ordinarily puts a `README.md` at
  the door of each of the five artifact homes, and in a repository you are a
  guest in those are five untracked files in somebody else's `docs/` tree.
  `yoyo doctor` reports each home without an index as a warning rather than a
  problem, so an installation configured this way runs work exactly as one with
  them does.

Either way, the project stops describing itself, which in this scenario is the
intent: another clone, another machine, and anybody else working on it get no
configuration at all, and `yoyo` there reports that it found none.

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
builds read it, is not read. `state_root` is its only key; it must be
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
([operations](operations.md#where-the-state-is-and-moving-it)).

**Two products on one machine share the root unless one of them is moved.** Each
product keeps its records under `projects/<product id>/state/` inside it — under
`products/<product id>/` in a root the earlier builds laid out, which is read
that way until it is moved — and each
product's checkout carries its own marker, so two products that resolve the same
root agree with each other as well as with themselves. The
[operator's pause](operations.md#pausing-everything-and-resuming-it) lives at the
root rather than under a product, so on a shared root one `yoyo pause` stops
both. A product moved to a root of its own is also out of reach of the pause
placed at the other one.

`yoyo config show` prints the resolved root and the layer it came from on its
`# state root:` line, `--origins` lists it as `state_root` with the same origin,
and `--json` carries both under `state_root`. The origin is
`environment:YOYODYNE_STATE_HOME`, `machine:<path of machine.yaml>`,
`environment:XDG_STATE_HOME`, `default` for `~/.yoyodyne`, or `earlier-default`
for the earlier builds' home. [`yoyo
doctor`](operations.md#checking-the-installation) reports the same two, and
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
held to in either layout. The [machine home design](designs/machine-home.md) is
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
  It refuses while any run of the project is in flight.
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
   `execution.factory_stall_after` (`2h`),
   `triage.stuck_merge_age` (`2h`),
   `triage.review_rounds_cap` (4),
   `approvals.publishing` (`human`), `approvals.work_items` (`human`), an
   agent's `instances` (1), and every value under [`services`](#services):
   `services.slack.enabled` (`false`), `services.dashboard.enabled` (`false`),
   `services.dashboard.port` (8765), `services.dashboard.bind` (`127.0.0.1`),
   `services.dashboard.allowed_hosts` (empty), `services.dashboard.token`
   (`generated`), `services.scheduler.enabled` (`true`),
   `services.maintenance.enabled` (`true`), and `services.maintenance.every`
   (`10m`).
   `triage.repair_grant_attempts` is filled in too, but as a derivation rather
   than a fixed default: it takes the size of the effective
   `execution.repair_attempts_before_replan`, read after every layer has been
   applied, and is floored at 1 for a project that repairs nothing routinely.
   `approvals.publishing` and `approvals.work_items` are the only approvals with
   a harness default, because they are the ones added after configurations
   existed, and a file that mentions neither loads rather than failing over a key
   that did not exist when it was written. The bundle states both at the same
   value the default holds, so extending it inherits neither and upgrading the
   executable moves neither. Both are opt-ins, and an opt-in that arrived by
   inheritance would not be one.

   **`work_items` is the one of the two that changes an existing project's
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
   approved. See [what reaches the queue](#what-reaches-the-queue).
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

## Product specifications

**Every role reads the specifications directory as authoritative product
intent, and reads all of it.** That is the operator's direction of 2026-09-27 —
"everything in docs/product is authoritative" — and it is what the directory is
for: one configured place the product's intent is written down in, near the top
of the configuration, directly under `version`.

```yaml
product:
  id: example
  repository: .
  specifications: docs/product   # the default; nothing to write down if you use it
```

Every document filed there is carried, not a fixed pair: the brief and the goals,
the non-goals, and anything else the owner files there. The management
conversations — the Lead Product Manager, the architect, the development manager,
and a program manager — are briefed with each of them under the heading
`Authoritative product intent`, and every developer and reviewer is handed them
the same way after the work item it is given, the reviewer reading each as the
change's base commit holds it. The work item is what a run is for and is carried
first; the home is read next and takes at most half of what the run's context
has room for, so a large home still leaves the item's own references room, and a
document that does not fit is named rather than silently missing. A document the
item names that the home already carried is not carried twice. A `README.md`
there is read as the directory index it is, and what it says about ownership —
which role owns what is filed there, and that every other role proposes rather
than edits — is delivered as a rule rather than as description.

Every document there is also [an artifact](#artifact-identity-and-metadata), with
the identity and the approval the goals have: one that is neither the brief nor
the goals is governed by `approvals.goals` rather than by `approvals.designs`,
because it is product intent by where it is filed. And two documents there that
contradict each other are reported by
[`yoyo stale`](#what-a-change-upstream-leaves-stale), naming both.

A **specification** is one Markdown file that opens with an introduction saying
what the thing is and why it exists, and states the goals that serve it after
that introduction:

```markdown
# Bounded runs

Yoyodyne runs one bounded work item at a time, because a change nobody can
review is not a change anybody can trust.

## Goals

- A run integrates only behind [protected paths](#protected-paths-in-a-developers-change),
  deterministic checks, and an independent review.
- A run that cannot finish leaves its work recoverable rather than lost.
```

The directory is walked to any depth, and every `.md` file inside it is a
specification, with one exception: a `README.md` is a directory index rather than
a document stating anything, so neither the shape above nor
[artifact identity](#artifact-identity-and-metadata) is asked of it, and it is not
counted as a specification. It is still read into the context, under a heading of
its own, because what is filed in a directory is worth knowing to whoever is about
to write the first document into it.
A non-goals document — one whose frontmatter records `kind: non-goals`, or
failing that one named `non-goals` — is held to its own shape rather than to
the specification's: an introduction saying what it bounds and why, then what
the product will not do under a `Non-goals` heading. It states no goals, and it
is not reported for that; one that states no non-goals, opens with them, or
leaves the section empty is reported exactly as a specification is. Everything
else has its prose checked for the introduction-then-goals shape above, and its
identity — the frontmatter naming its id, kind, status, and what it supports — is
checked separately, by [artifact identity](#artifact-identity-and-metadata).
The two are read by different things and reported differently, so a
specification with a malformed id is still read as intent, and one with no goals
still has an id everything downstream can refer to it by.

That shape is checked rather than merely described, because the goals are what
downstream work is kept consistent with and goals with nothing behind them are
not traceable to anything. A specification that does not follow it — no goals, no
introduction before them, or an empty goals section — is **reported and still
read**. `yoyo chat` names it on stderr when the conversation opens — and in the
conversation itself when `/refresh` reads the specifications again — and it is
listed for the Lead Product Manager alongside the specifications themselves. Refusing
to load it would silently lose intent somebody wrote down, which is worse than
loading intent in the wrong shape and saying so.

An empty or missing specifications directory is not an error either. The
conversation says that product intent is not written down, which is a true
statement about the repository rather than a reason to fail. A directory holding
nothing but the indexes `yoyo init` wrote is that same directory: an index states
no intent, so it is not counted as a specification and the conversation still
says intent is not written down.

The context also states, in so many words, what the directory records of the two
documents intent is written in: the **brief** saying what the product is and who
it is for, and the **goals** that serve it. Each is named with roughly how much
prose it carries, and one that carries almost none is called a placeholder — a
count beside the verdict, because how much a short document needs to say is a
judgment. A document's kind comes from the `kind:` in its
[frontmatter](#artifact-identity-and-metadata) when it has one, and from what it
is called when it does not, so a brief written by hand before anything was said
about identity still counts as one. Goals stated in the brief's own `Goals`
section count as the goals when there is no goals document — that is where the
shape above already puts them, and a project that wrote them there has written
them; once a goals document exists, that document is what the goals are read
from and the brief's section is not named beside it. What the Lead Product Manager
does with that signal is the [persona's](#personas) — the built-in one opens a
project with no brief or goals by asking what the product is for and offering to
draft them, and a project that wants something else replaces that guidance like
any other part of the persona.

### What the Lead Product Manager sees besides them, and what it does not

**The specifications directory, the tracker, and a description of what the
product ships today.** That last part is the documentation your project names,
and the help every command prints. It is carried in a section of its own,
labeled as description of the implementation as built and never as authority
about intent. That is the whole of what is delivered in its context: no source
and no design document arrives there, and nothing runs a command. What it may
additionally do is [read one named path at a recorded commit](#reading-the-repository-from-a-conversation)
— a source file or a design among them — which arrives under the same
description-not-intent label, one path at a time, and only when it asks.

**Which documents those are is `product.shipped_documentation`**, a list of
Markdown files relative to the repository:

```yaml
product:
  id: example
  repository: .
  shipped_documentation:
    - README.md
    - docs/handbook.md
    - docs/operating.md
```

It is a named list rather than a directory for the same reason the set is
narrow at all: a walk would sweep in the design document and the decision
records, which say how the product is built and are the half of `docs/` that
made description reachable as intent in the first place. Each entry is confined
to the repository like every other product path and must be Markdown; a path
naming nothing is simply not carried, and the section says which of them it
found. The list is replaced wholesale rather than merged, as `checks` is.

**There is no default, and that is the point.** A project that names none is
shown none, and the section says so — that what the product ships is not
written down here, rather than that the repository holds no documentation.
Yoyodyne's own documentation layout is eight generic paths (`docs/work.md`,
`docs/reporting.md`, `docs/operations.md`, and five more), and a repository
that happens to hold a file at one of them means something else by it. Handing
those to an adopting project's Lead Product Manager labeled "what the product
ships" is a stranger's prose arriving as description of your product, so the
harness does not do it: **that set applies only to Yoyodyne's own repository**,
which it identifies by the Go module that repository declares, and every other
project is shown what it names and nothing else.

**What Yoyodyne itself carries**, from that set, is `README.md`; the six
operator documents the README split put beside it — [the
conversation](conversation.md), [how work flows](work.md), [what comes back to
you](reporting.md), [artifacts, goals, and invariants](artifacts.md),
[operations and recovery](operations.md), and [working on yoyo
itself](developing-yoyo.md) — and this file. It lives in
`HarnessShippedDocumentation` in `internal/contextbundle/product.go`, and it is
deliberately narrower than the README's [further-reading
index](../README.md#further-reading): the provider-plugin format, the
coined-term register, the Slack setup, the release notes, the setup skill, and
the design are all reachable from there and none of them is carried here. So
adding a document to that index does not thereby show it to the Lead Product
Manager — the set has to name it, and a test holds the set to documents this
repository actually has, because a path that stops resolving is a surface the
Lead Product Manager silently stops being given.

**The set has a ceiling, and a margin under it that warns.** The eight
documents are carried in full — that is the Lead Product Manager's decision, taken
on yoyodyne-ifd.240 and kept on yoyodyne-ifd.403 — and what they add up to is
measured against `ShippedDocumentationCeiling` in the same file, which is set
well above what they are today and marks the point at which the briefing's
token cost is a product question again; the constant's comment says what that
cost is. Inside `ShippedDocumentationMargin` of it, `make test` prints a
`WARNING:` line after the suite — printed there deliberately, since
`go test ./...` discards what a passing test says — every conversation that
opens says so on stderr, and the briefing itself says so to the role, naming
the room left and the reduction (yoyodyne-ifd.117.4) that buys more; nothing
fails. Only reaching the ceiling fails the test. The set's size is written down on every pass, whatever
its standing: the briefing states it, the conversation's record keeps it with
the picture it was taken for (`yoyo agent list` prints it beside the picture's
commit), and a refresh records it on the event. Over the three weeks to
2026-09-19 the ceiling was a budget the set was always within bytes of, raised
four times, and every documentation edit failed the gate on a sentence
unrelated to it; a reported margin under a distant ceiling is what replaced
that.

The label is the whole of the arrangement, so it is worth reading twice. The
specifications are the only statement of what the product is for; nothing in the
shipped-surface section revises that, however emphatically it is written. Where
the two disagree, the Lead Product Manager **reports the conflict** rather than
resolving it silently or repeating either side as settled product fact. That is
what makes documentation safe to hand to the role that is authoritative about
intent: it arrives as an answer to *what exists*, never to *what is wanted*.

**This reverses half of an earlier trade, openly.** Until 2026-08-18 the Lead Product
Manager saw the specifications and the tracker and nothing else, narrowed on
2026-08-16 after a stale sentence in `README.md` reached the operator as a
statement about the product. What that bought is real and is kept: description
does not arrive labeled as intent, and it never will again while the section
carries its label. What it cost was underestimated. On 2026-08-18 the Lead Product
Manager did not know `bin/yoyo-status` or `yoyo cost` existed until the operator
described them, drafted a work item that mis-assumed which surfaces existed, and
could not evaluate a formatting question about two real outputs it had never
seen — three failures in one day of the operator's routine interface needing the
operator to stand in as its eyes.

What is still given up is also real. Reading all of `docs/` is what let the
Lead Product Manager notice a contradiction between documentation and reality, and
what it reads now is narrower than that: the design document and the decision
records are not there, because they say how the product is built and are the
half of `docs/` that made description reachable as intent in the first place.
Reconciling accumulated documentation against the code belongs to a role that
reads the code, and the harness still does not have one. What it has since
gained is narrower: a management role can [read one named path at a recorded
commit](#reading-the-repository-from-a-conversation), which lets the Lead Product
Manager check a document before it advises about it rather than sweep the tree
for contradictions. Point `specifications` at a wider directory if you would
rather have the breadth than the authority; the confinement rule is the only
limit on where it points.

The documentation is read **after** the specifications have taken what they need
of the context budget, so a repository too large for both keeps the half that is
authoritative and the section names what did not fit. A repository that holds
none of the documentation it named is told so rather than getting a section that
quietly carries less than it says it does — and a project that named none is
told *that*, in different words, because a setting nobody wrote and a document
nobody wrote are two different things to go and do something about.

## Artifact identity and metadata

The canonical documents upstream of a work item — the brief, the goals, the
designs and specifications, and the decision records — each carry a stable
identity in frontmatter, so something downstream can refer to one durably and
the relationship can be checked rather than believed. They live in three
configured homes:

```yaml
product:
  id: example
  repository: .
  specifications: docs/product     # the brief and the goals: the defaults, so
  designs: docs/designs            # nothing to write down if you use them
  decisions: docs/decisions
```

Each home is walked to any depth, and every `.md` file inside one is an
artifact. Two names are not: a `README.md`, which is a directory index rather
than intent anything refers to, and everything inside the invariants directory,
which sits inside the decisions home by default and carries
[its own identity scheme](#architectural-invariants). A home that does not exist
is not an error — a project that has not written its designs down yet records no
design artifacts.

That exemption is what makes the `README.md` the place these directories explain
themselves in, and `yoyo init` writes one for every home above — the
specifications directory, the `goals` directory under it, the designs, the
decision records, and the invariants. Each states three things and nothing else:
what is filed there, which agent owns it, and whether you may edit one of those
documents by hand. None of that is policy the file invents. A role that is not
the owner proposes an amendment and waits, per the ownership table above; your
own edit is never refused, and what it is is reported — a revision recorded under
a role that does not own the document is an unauthorized revision every load
names, and what a change leaves stale downstream is
[`yoyo stale`](#traceability-references-and-orphans)'s to report. An index that is
already there is left alone, `--force` included, because it is the project's own
prose rather than something `init` generated; `yoyo doctor` reports one that is
missing or has stopped stating the three things, and `yoyo setup` offers to write
it — asking separately, and defaulting to no, before it replaces one somebody
wrote.

The metadata is the model the invariants already use, deliberately rather than a
second scheme beside it: **the file name is the id**, a frontmatter id that
disagrees with it is refused, the status is stated rather than inferred, and
every change appends to a revision log.

```markdown
---
id: v1-goals
kind: goals
title: V1 goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema
---

# V1 goals

The document itself, unchanged by any of the above.
```

| Field | Meaning |
| --- | --- |
| `id` | The stable identity, and the file's own name: `v1-goals` lives in `v1-goals.md`. Lower-case letters, digits, and hyphens. |
| `kind` | `brief`, `goals`, `non-goals`, `design`, `specification`, or `decision`. |
| `title` | One line naming what the document is. |
| `supports` | The artifacts upstream of this one, by id: the goal a design serves, the brief a goal serves. Optional — the brief is the root and supports nothing. |
| `status` | `draft` (written, not yet active), `active` (what the product currently intends), `superseded` (replaced by a later artifact), or `retired` (stopped applying, not replaced). |
| `revisions` | Append-only: what changed (`created`, `amended`, `superseded`, `retired`), the role it was recorded under, when, and why. At least the creation is required, and the role must be the one that [owns the kind](#who-may-change-an-artifact). An amendment may also say what it did to the document's intent — `intent: consistent` or `intent: fundamental` — which on the goals decides [whether your approval stands through it](#approving-a-document). |
| `approvals` | Append-only, and optional: [your approval of the document](#approving-a-document), each entry naming the revision it was given for. |

Everything below the frontmatter is the document, and nothing about it is
prescribed here: a brief, a goals document, and a decision record have nothing in
common structurally. A specification's own prose contract is the
[introduction-then-goals shape](#product-specifications), which is checked
separately.

Status and revisions have to agree, the same way an invariant's retirement does.
A `superseded` or `retired` artifact must record the revision that ended it, and
one that still applies cannot record one — an artifact whose status says it
was replaced while nothing says when or why records no decision at all. Which
artifact superseded it is not part of the schema yet; the revision's reason says
so in prose.

A file in an artifact home that cannot be read as an artifact is **refused and
named**, which is how an invariant that cannot be read is handled and the
opposite of how a malformed specification is: a document with no usable identity
cannot be referred to, and admitting it under a guessed id would be worse than
saying it is not there. That covers a file with no frontmatter, an unknown or
mistyped field, a status or kind the harness does not know, a missing revision
log, and an id that disagrees with the file name. Two files claiming one id
refuse **both** — choosing between them would hand whatever refers to that id a
document nobody decided on — and each refusal names the other file.

```sh
yoyo artifact list                  # the recorded artifacts; what is not one goes to stderr
yoyo artifact list --kind decision  # one kind
yoyo artifact show v1-goals         # one artifact, its revisions, and your approvals
```

There is no `yoyo artifact create` or `amend`, unlike the invariant commands: an
artifact's content is written by the role that owns it — by hand, or from its
conversation as a typed write you approve, which the harness files with the
frontmatter generated ([writing a document from a
conversation](artifacts.md#writing-a-document-from-a-conversation)). What the harness owns is refusing a
document whose identity is missing, malformed, or claimed by something else,
[reporting a change recorded by a role that does not own it](#who-may-change-an-artifact),
and [recording your approval](#approving-a-document).

### Approving a document

Approving the brief and the goals is the one thing the design asks of you
routinely, and until it is written down it lives only in the conversation where
you said it — which leaves an approved goal and a draft one indistinguishable to
everything downstream. `yoyo artifact approve` records it in the document:

```sh
yoyo artifact approve brief --reason "approved in conversation on 2026-08-17"
yoyo artifact approve v1-goals --reason "approved with the adoption goal added"
```

```yaml
approvals:
    - revision: 1
      by: operator
      at: 2026-08-18T09:00:00Z
      reason: approved in conversation, with the adoption goal added
```

A document written from a conversation whose policy is automatic records
`by: harness` and `policy: approvals.designs` (or the applicable brief or goals
policy). That confirmation opens an isolated run for the exact saved document,
with configured checks and independent review before normal integration;
it does not leave a file for you to commit. It needs `approvals.integration:
automatic` as well; where integration is `human`, the document is put to you to
confirm instead. A document governed by `approvals.brief` or `approvals.goals`
— the non-goals, the operating rules, and anything filed in the specifications
directory included — remains yours to confirm unless the owner records a
consistent revision with its directing work item. [Writing a document from a conversation](artifacts.md#writing-a-document-from-a-conversation)
describes restart recovery and failures returned to the owner.

`at` is when the approval was **recorded**, which is not always when it was given
— an approval given in conversation is written down afterwards — so say when and
how you gave it in `--reason`, which is the half only you can attest to.

**The write lands in your checkout and stops there, and the command says so.**

```
docs/product/brief.md is now an uncommitted change in /home/you/src/example, and
a run against that checkout refuses to start while it is; committing it is
yours, under your own identity
```

**The checkout is named rather than assumed**, because which one the write landed
in is this configuration's answer: `--config` naming another project, a
`YOYODYNE_CONFIG` in your environment, and a linked worktree carrying its own
`.yoyodyne` each point `approve` at a different repository, and the run that
refuses to start is a run over the one that was written to. Ordinarily that is
the checkout you are standing in and the sentence tells you nothing you did not
know; where it is not, it is the difference between committing the file and
looking for it where it never landed.

Nothing commits it and nothing opens a pull request for it. The only ways into
the target branch are a reviewed promotion and your own hand, so a harness-made
commit would be a promotion by another name, carrying tree state nothing
reviewed; and stopping at your tree keeps a document to two readings — committed,
or an edit of yours that is visibly holding the runs up — rather than adding a
third that is on the branch and that nobody touched. The price is the dirty
checkout, which is why it is said as the write happens rather than left to arrive
as `primary repository has uncommitted changes` from the next command you run.
`--json` carries the same sentence as `pending_commit`. A refused approval writes
nothing, so it says nothing.

**The approval is yours, and a process an agent started cannot record one.** A
goal you approved is what lets work be admitted without asking you, so a run
that could approve the goals could admit whatever it liked against them. Every
process the harness launches for a role carries `YOYODYNE_AGENT_ROLE`, and
`approve` refuses a process that carries it before it opens the store —
`yoyo artifact approve is refused from a process the harness launched for the
developer: a person approves an artifact, and an agent's process is not one` —
as [`yoyo pause`, `yoyo resume`, and `yoyo release`](operations.md#pausing-everything-and-resuming-it)
do. Behind that, the write it would have made is to a protected path, which the
harness refuses in a run's change whatever ran the command — tested end to end
by a developer run whose change carries a forged approval in
`docs/product/goals`, refused with nothing reaching the target branch.

**The approval names the revision it was given for**, which is the index into the
revision log above it. The log is append-only, so that index means one change
forever, and the arithmetic that follows is the point: an approval of the last
revision is a document approved as it stands, and an approval of an earlier one
is a document that has been amended since you saw it. A revision that only gives
a goals document's goals identifiers — the bracketed name an entry opens with —
is not counted: it is recorded as `identified` rather than `amended`, changes no
goal's words, and leaves the document approved as it stands. `yoyo artifact list` and
`show` say which:

```
v1-goals [goals, active] V1 goals
  file: docs/product/goals/v1-goals.md
  supports: brief
  approval: approved and amended since — given by the operator 2026-08-18T09:00:00Z,
            for revision 1, and one revision was recorded after it, so the document
            as it now reads is not what was approved
```

**A rewording of the goals that is consistent with what you approved is not an
amendment you are asked about.** The test is what the goals admit: a change is of
fundamental intent if the goals would afterwards admit work they refused
before, or refuse work they admitted, and that is yours; anything else is a
consistent rewording, delegated to the Lead Product Manager. Which one a change
is, is the Lead Product Manager's judgement, and it is recorded on the amendment
rather than inferred from it:

```yaml
revisions:
    - action: amended
      by: product-manager
      at: 2026-09-26T20:00:00Z
      reason: yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager
      intent: consistent
```

An amendment of a goals document recorded by the Lead Product Manager as
`intent: consistent`, with a reason that opens with the work item that directed
it, leaves the document approved: work naming its goals is admitted exactly as
before, `yoyo artifact show` says the approval stands through that many
rewordings, and [`yoyo stale`](artifacts.md#what-a-change-upstream-leaves-stale)
lists it as a rewording rather than an amendment. Every other amendment is still
yours — one recorded as `intent: fundamental`, one that says nothing, and one
labelled consistent whose record is short of the rest: recorded by another role,
against a document other than the goals, or with no item named. The last of
those is said on `show` with what it is missing, so the label does not read as
ignored. What is checked is the shape of the identifier the reason opens with and
not that the tracker holds it; the reason is the record you follow to the
decision.

**Your `approvals` configuration decides what is asked of you.** `approvals.brief`
and `approvals.goals` are `human` by default and `approvals.designs` is
`automatic`, deliberately rather than by inheritance: the brief and the goals are
what you state and what everything else traces back to, while a design serving an
approved goal is the architect's judgement about how, and approving each one is
the per-change gate autonomy is the absence of. Approving the goals is the one
approval that then carries weight elsewhere, because it is what work is admitted
against. `approvals.goals` covers the
non-goals with the goals, because a bound on intent nobody approved is as much
unapproved intent as a goal is, and it covers every other document filed in the
specifications directory too, whatever its kind, because everything there is
authoritative product intent. A decision record is the architect's account of
how something was decided rather than a statement of what the product should do,
and no setting asks you to approve one.

**Recording an approval gates one thing: what reaches the work queue.** An
unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it. What your approval of the goals decides is whether
work serving them is admitted without asking you — see
[what reaches the queue](#what-reaches-the-queue) below. Everywhere else, an
amendment after approval changes what is reported about the document rather than
what is allowed, and what `human` buys you is that the difference is visible — in
the document, in the listings, and in `--json`, where each artifact's `state` is
`approved`, `amended`, or `unapproved`.

### What reaches the queue

**`approvals.work_items` decides whether you are asked about every work item.**
It is `human` until you say otherwise: every item is put to you before it is
admitted to the queue. Set it to `automatic` to move that approval up to your
goals, which is what it exists for — work that traces to a goal you approved is
then admitted without a further prompt, and you are told afterwards what went in.

**It is opted in to rather than inherited**, for the reason `integration` and
`publishing` are: this is the setting that lets work reach the queue with no
person in the loop, and autonomy is something you turn on once you have the gates
to justify it rather than something a repository acquires by extending a bundle
or by upgrading the executable. The bundle states `human` at the same value the
harness default holds, so `automatic` never arrives on its own.

**Upgrading does move one thing, and it moves toward asking you.** Before this
key existed the Lead Product Manager could admit work to the backlog directly, and you
were told afterwards rather than asked; `human` refuses that direct admission, so
a project that upgrades and leaves the key alone has a Lead Product Manager that
proposes work instead of admitting it. That is the whole of the change, it is in
the direction of more consent rather than less, and the work is not lost — the
proposal is put to you, and approving it creates the item. Set `work_items` to
`automatic` to have it admit directly again, against goals you approved.

**Approval moved up a level; it did not disappear.** Three things still stop and
ask, and they are exactly what the Lead Product Manager escalates rather than
proposes: work it can attach to no goal, work it says would cut against one, and
work that fits the goals and that it judges to be against what the product is
for. A change to what the goals admit is yours to decide and reaches the queue
through nothing at all: the Lead Product Manager drafts it, and it reaches the
repository only as a [typed write you
approve](artifacts.md#writing-a-document-from-a-conversation). A rewording that
leaves them admitting and refusing the same work is the Lead Product Manager's
to record, and your approval stands through it — see [approving a
document](#approving-a-document).

**Nothing is admitted without asking until a goal is actually approved.** The
attribution has to resolve to a goal an active document states, and that
document has to be approved as it now stands. A goals document nobody approved,
one amended since you approved it — a rewording the Lead Product Manager recorded
as consistent with intent is not an amendment for this — and a repository with no
goals to check against all put the work to you instead, with the reason on the proposal. So
turning it on gets you a second ramp for free: a project that has opted in still
asks about everything until its first `yoyo artifact approve`. It is also why
`work_items: automatic` requires `approvals.goals` to be `human`: admitting work
rests on the goal it serves having been approved, and a project approving no
goals has nothing for it to rest on, so the combination is refused rather than
left to be discovered as a queue that never fills. That refusal only ever names a
key you wrote, because `automatic` is never inherited.

**Both ways work reaches the queue are governed by it.** The Lead Product Manager can
admit work to the backlog directly as well as propose it, and `human` refuses the
direct admission with a pointer at the proposal it should have made instead — a
setting that governed proposals while work arrived through the other door would
say one thing and do another. Decomposition is not admission: a role that may
only create underneath work you already admitted is building structure under a
decision that was made, and it is unaffected by either setting.

**One admission is the harness's own and is governed by neither value: the
item a red landing files.** When the [landing checks](#where-the-whole-suite-runs)
fail over a commit that every gate passed, the harness files a bug for it
directly, at priority 0, under either `work_items` setting — the operator's
standing order of 2026-09-19, that a red landing files its own item. No role
asks for it and no proposal is put to you: it is the harness reporting that
the target branch is broken, in the one form that stops the next run being cut
from it unnoticed. Its notes record that basis — filed by the harness for the
red landing of the named item and run, on the operator's standing order — and
carry the `Goal served:` line of the item whose landing went red, so the
attribution check reads it as work serving that goal rather than as work nobody
attributed. Nothing else the harness does admits work on its own account.

**`approvals.work_item_exemptions` narrows the per-item gate without lifting it.**
It is a list of classes of work this project admits without asking, whatever
`work_items` says, and it is empty until you write one:

```yaml
approvals:
  work_items: human
  work_item_exemptions:
    - diagnosis
```

There is one class. `diagnosis` is work that only looks: it reads what is already
there, says up front what it will read and stops there, and produces findings
rather than a change. It exists because "ask me about every work item" turns out
to be coarser than most operators who set it mean — being asked before something
reads the repository and writes down what it found is not what the gate was put
up for, and with no way to say so the policy stays a sentence nothing enforces.

**The class is the agent's claim about its own work, and the exemption is yours.**
A proposal or a `create` may carry `class: diagnosis`, and it means nothing at all
in a project that has not exempted that class — the Lead Product Manager is told about
a class only where you have exempted it, precisely so it is never invited to claim
one that would change nothing. What keeps the claim honest is that the exempted
class is work that changes nothing: an item claiming to be diagnosis and then
doing something else is an item whose description says what it does, under a goal
that had to resolve, in a queue you read.

**An exemption moves who is asked and never whether the work is for anything.**
Work admitted under one names a goal that *resolves* — one an active goals
document actually states — and nothing weaker. Anything short of that is put to
you exactly as it would be for work claiming no class: a goal the documents do
not state is `unresolved` and refused, and a goal nothing could check against is
`uncheckable` and asked about, because an attribution nobody could check is not
one you agreed to. What an exemption does not require is that the goal be
*approved*, which is what makes it usable by the projects that keep the human
gate. The [attribution table](#goals-and-the-work-attributed-to-them) is the same
table for exempted work as for everything else.

**What it narrows is the per-item gate and only that.** Under `work_items:
human` the exemption stands the per-item question down, and the goal has only to
resolve. Under `work_items: automatic` there is no per-item question left to
narrow: the approved goal is the whole of what admits work there, so an exempt
class clears exactly the gate every other item clears, and a goal nobody
approved — or one amended since — puts the work to you as it would anything
else. An exemption that reached past the per-item question would be a carve-out
admitting work under a goal nobody approved, which is the opposite of the
narrowing it is.

**What was admitted without asking is reported where a decision would have been.**
Each item is named with the goal it traces to and with what actually admitted it
— the approved goal, or the class you exempted — in the conversation and in
`yoyo chat --message ... --json` under `admitted`, where they are the `goal` and
`basis` fields. The two are not the same answer: under an exemption the item
still names a goal, and the goal is not what let it through. The item's own notes
record the same basis, and never that you approved the item. The conversation's
event log records an admission as its own event, so work nobody was asked about
is never readable as work somebody approved.

**Approving writes nothing but the approval.** The prose, the title, what the
document supports, and its status are untouched, so an approval can never become
a way to edit a document by another name — the document itself stays the owning
role's to change. Approval is recorded as yours rather than as a role's, because
every one of these documents is drafted by the role that owns it, and an
approval a role could record would be that role approving its own document.
Refused, rather than recorded: an approval with no reason saying how you gave it,
a second approval of a revision already approved, and approving a document that
has been superseded or retired.

### Who may change an artifact

Ownership is an authorization boundary rather than a prompt convention, so it is
in code the way the invariants' is, rather than in a persona a configuration can
weaken.

| Kind | Owner | Every other role |
| --- | --- | --- |
| `brief`, `goals`, `non-goals` | Lead Product Manager | Asks questions and [proposes amendments](#proposing-a-change-to-a-document-you-do-not-own) |
| `design`, `specification`, `decision` | Architect | Identifies risks, asks questions, and [proposes amendments](#proposing-a-change-to-a-document-you-do-not-own) |

The development manager appears in neither row, because it owns no repository
document: its decomposition is Beads work rather than Markdown. Nothing here
constrains **you**. The boundary is between agent roles, and the operator directs
any of them.

It holds in two places, and both are live.

**Writing.** The package that writes an artifact refuses a role that does not own
the kind, on creating, amending, superseding, and retiring one, and records the
role that did in the revision log. That is the path a document written from a
conversation takes: the owning role emits a typed action, and the harness
confirms it under the document's approval policy or asks you where that policy
is not automatic. Either way the write is checked against this boundary under
that role's authority — see [writing a document from a
conversation](artifacts.md#writing-a-document-from-a-conversation). A role that
names a kind it does not own, or a home its kind is not filed in, is refused
before anything is confirmed and before you are asked about it. There is still no `yoyo artifact create`: a
command would need the document's prose typed at a shell, which is the
transcription the typed action exists to end.

**Reading.** A document whose revision log records a change by a role that does
not own it is **reported every time the artifacts are loaded**, as an
`unauthorized-revision` beside the [broken relationships](#traceability-references-and-orphans),
naming the file and which entries crossed. It is the half that catches a
hand-edited log, wherever it came from.

It reports rather than refuses, deliberately. The revision log is append-only, so
a past entry cannot be made lawful without rewriting history, which is the one
thing the log exists to prevent. Refusing would drop the document out of the set,
report everything that referred to it as naming something nobody wrote, and leave
a file that could neither load nor be corrected. So the document keeps loading,
keeps governing, and stays amendable by its owner, and the entry stays reported
until somebody decides what to do about it.

**A third place, and the one that catches an editor.** Both halves above are
about the document — who wrote it, and what its log says. Neither notices a
developer that simply opens the file. That is what the protected-path gate below
is for, and it is why an agent with an editor in its worktree is no longer the
open case it was: the edit is refused before anybody reviews it, whatever the
revision log does or does not say about it.

### Protected paths in a developer's change

The documents above are upstream of every change a developer makes. A developer
that edits one is redefining what its own work is measured against, and reading
the diff does not tell that from a legitimate edit — both are a file that
changed. So these paths are **default-deny for a developer's diff**:

| Protected | Setting it follows |
| --- | --- |
| `.yoyodyne/` | fixed; the configuration directory |
| `docs/product/` | `product.specifications` |
| `docs/designs/` | `product.designs` |
| `docs/decisions/` | `product.decisions` |
| `docs/decisions/invariants/` | `product.invariants` |

The set follows your configuration rather than the default layout: a project
that keeps its designs elsewhere has not thereby made them a developer's to
rewrite.

**The tracker's export is refused the same way.** A worktree is given your
checkout's own `.beads/issues.jsonl` and that copy is
[held out of the run's change](work.md) with Git's skip-worktree bit — which
lives in the worktree's index under `.git`, a directory the developer's sandbox
grants writes to. One `git update-index --no-skip-worktree` inside the run would
turn the refreshed export into a modification the harness commits, promotes, and
conflicts every other run against, so the export joins this gate: a diff
containing it is refused, and the refusal names what the file is and how it got
there rather than only the path. The paths refused are the exports the harness
refreshes, so the two never drift apart.

**How it behaves.** The gate runs in front of the deterministic checks, on every
attempt, over every path the change touches — tracked, untracked, and both sides
of a move. A change that touches one of these paths without a grant is refused
and handed back to the same developer inside the same repair loop a failing check
uses, spending from the same budget, and the refusal names how a grant is made.
Where the product [reports to Slack](slack/setup.md), each refusal handed back
is also said in the item's thread, in the developer's voice, naming the refused
paths, what the item grants, and the grant line that would admit them — so a
repair round spent on it is a round with a stated reason rather than one only
the item's notes explain. A refusal that finds the budget already spent buys no
round, and the blocker line that ends the run is what names its paths.
No reviewer is asked about it: the class of finding this replaces used to cost an
Opus review cycle to reach, and it costs a string comparison here. A run whose
repair budget is spent still refusing is blocked on the work item, with the
refused paths and the item's grants both named, because which of the two is wrong
is a person's decision.

**Granting a path.** An exception is declared in the work item's text, on a line
beginning with the marker:

```text
Protected-path grant: docs/designs/v1-harness-design.md
```

Several paths on one line are separated by commas or spaces, and several such
lines are read together. A grant naming a file admits that file alone; a grant
naming a directory admits what is inside it. A grant of the repository root is
not a grant, and prose that merely discusses these paths grants nothing — the
marker has to begin the line, which is why it is an unlovely token rather than a
phrase an item could produce by accident.

**Which fields count, and why it is not "whoever wrote it".** A grant is read
from the item's **title, description, design guidance, and acceptance criteria**,
and **not from its notes**. The gate does not ask who typed a grant — it cannot,
because the tracker records no authorship the harness could check. What it relies
on instead is *when*: those four fields exist before the run starts and no part of
the harness writes to them, so a grant in one of them predates the change it
admits. The notes are the opposite — the harness appends each run's own record
there, including the reviewer's summary and findings — so a grant read from the
notes could be an agent's own prose, admitted to the next run of the same item.
That is the case this gate exists to stop, so the notes do not count.

The practical consequence: **a grant written into the notes silently does not
count.** A run refused despite an item that plainly names the path is usually
this. Both the refusal and the blocker name the fields a grant is read from.

**The paths a grant does not reach.** A grant is an exception to *this harness's*
default-deny, and to nothing else's — and one directory of this harness's own
is beyond it too, below. Claude Code refuses an agent's writes to its own
settings files above anything yoyodyne permits — the editing tools are denied
there however the run is configured, and the shell sandbox names the file and
cannot be disabled by policy — so an item that grants one of them has admitted
work no run can do, and the run discovers that by spending its repair budget
against it. One item spent three rounds there before this was recorded anywhere.

| Beyond any grant | Refused by |
| --- | --- |
| `.claude/settings.json` | Claude Code |
| `.claude/settings.local.json` | Claude Code |
| `.yoyodyne/roles/` | Yoyodyne, absolutely |

So the harness refuses a creation, an update, or a proposal whose text grants one
of these, and the refusal names who refuses it: what is wrong is not the item's
judgement about the path, but that the change is a person's to make by hand
rather than a run's to be given. Only the marker is refused — prose that names
one of these files grants nothing and admits fine, which is what lets an item
*about* this boundary exist at all.

**The role definitions are refused by this harness itself, and for a reason of
its own.** A role definition says what a role may do, so a run that could write
one could widen its own authority — the one thing
`configuration-never-grants-authority` forbids. The rest of `.yoyodyne/` is
default-deny with the item as the way out; this directory has no way out at
all, decided change or not
([configurable workflows](designs/configurable-workflows.md#the-authority-model)).
A grant naming `.yoyodyne/roles` or anything inside it is refused at the same
three doors and by the run before it claims the item, and a change touching the
directory is refused by the diff gate **whatever the item grants** — a grant of
`.yoyodyne`, which is still how an item admits the rest of the configuration,
does not reach inside it. The comparison folds case, because on a
case-insensitive filesystem `.yoyodyne/Roles/` is the same directory. A person
changes a role definition by hand, and the operator's activation is what makes
it effective.

**And once more, where all four fields are read.** Those three doors carry an
item's title and description; a grant is honoured from its design guidance and
acceptance criteria too, and nothing in the harness writes either of those — they
reach an item through the tracker's own command. So the run asks the same
question itself, over all four fields, before the item is claimed: a run that
would start on such an item refuses to start, which is the same refusal one step
later and still before a single repair round is spent. That is also what covers
an item admitted before this gate existed. The developer contract names the same
paths for the case no gate can catch — an item that describes the work without
granting anything, whose developer would otherwise spend attempts looking for a
way in.

The provider rows are short and evidenced rather than a guess at everything a
provider's sandbox refuses: an entry refuses work at admission, so a path added on
suspicion costs items nobody needed to refuse. They grow the same way they started —
something meets the wall and reports it. The role-definitions row is not one of
them: it is this harness's own refusal, decided in the design rather than met by a
run, and it is absolute by that decision.

**A done-condition is never written against one of these homes.** The gate
above refuses a diff; what it cannot refuse is an item whose *done-condition*
lives in a document the run may not write — "the design's query list marks the
query as existing", "reconcile the design document's `yoyo status` entry", "her
ruling is recorded on the design". Such a condition is one no diff can meet, and
a run handed it lands what it can and parks on the rest. Three items did that in
one week (`yoyodyne-ifd.141.1`, `.63`, and `.68.25` before them), each caught by
the reviewer after the run had spent itself, each fixed afterwards by the
architect amending the document through the governed path. So the check is made
where the item is written: a creation, an update that rewrites the description,
or a proposal whose "Done means" clauses or acceptance criteria name a path
under the product, designs, decisions, or invariants homes — or a document one
of those homes owns, by its name — is refused unless the item grants that path,
with the clause quoted and both fixes named: take the clause out and say the
document's owner amends it through the governed path once the run's summary
names what there is to record, or, where the change behind it is already
decided, carry the grant. Only the done-conditions are read — the whole of the
acceptance criteria, and in the description the sentences from a "Done means"
(or "Done:", "done when") to the end of their paragraph — because an item cites
these documents in nearly every description, as the design it builds against or
the ruling it obeys, and a citation is not a condition. A document is named by
its path or, where its id is two words or more, by its id however the prose
joins the words ("the slack-reporting design" names
`docs/designs/slack-reporting-design.md`); a one-word id such as `brief` is left
to the path, and an invariant is named by its path only. An id that is also the
name of one of the harness's roles is read as the document only where the clause
names its path or names it as a document — the word `design` or `document`
beside the id, or its file name — and the bare phrase is the role: "no program
manager is configured" is about the role, while "the program manager design"
names `docs/designs/program-manager.md`. A done-condition saying a design is
recorded, published, or ratified is still refused on an item naming no
executor, whichever document it names. The run asks the same
question of the item it is handed, over the acceptance criteria as well, before
it claims the item, and refuses to start rather than parking on the condition
afterwards — which is what covers an item whose criteria were written with the
tracker's own command, and an item admitted before this existed. Neither check
reaches `.claude/settings.json`, `.claude/settings.local.json`, or
`.yoyodyne/roles/`, which stay beyond any grant as above. [How work flows](work.md#what-an-item-may-ask-of-a-run)
states the rule from the item's side.

**What a grant does not do.** It admits the path; it does not decide what is
written into it. The legitimate use of the exception is recording a change
somebody already decided — an approved amendment, an operator's decision — never
delegating the deciding, so a grant should name that decision, and the reviewer
is instructed to look for it: a granted path whose item names no decided change
behind the grant is a finding at major severity or higher. The gate is a string
comparison and cannot ask this question; the reviewer can, which is why the two
halves sit where they do. A branch review is not asked it at all, because it
reads commits rather than the items their grants live in.

Nothing any agent produces during a run grants a path. A developer that
genuinely needs one says so in its summary and
[proposes the change](#proposing-a-change-to-a-document-you-do-not-own); the
grant goes into the item, which is not a developer's to write. Which role
maintains an item's text is a question about the fixed set of roles rather than
about this gate, and this document does not answer it.

### Proposing a change to a document you do not own

A role that may not edit a document and has no way to say it is wrong has two
moves left, and both are bad: build against intent it believes is wrong, or edit
the document anyway. So there is a third: one block, in the contract, carried by
every role that has it. Today that is the developer, which is the role that meets
the boundary while implementing against a document — a developer that finds the
design contradicts the goal it serves ends its reply with it:

````text
```yoyodyne-amendment
{"proposals":[{"artifact":"v1-design","change":"say which of the two orderings holds","why":"the work item cannot be implemented against both"}]}
```
````

The harness resolves the document to its kind and its kind to its owner, so who
is being asked follows from the document rather than from anything the agent
claims. A proposal naming a document nobody records is refused, because there is
no owner to decide a change to a document that does not exist, and a proposal
from the role that already owns the document is refused too: that role amends
it. **The refusal reaches you, and it reaches the agent that wrote it on that
agent's next invocation in the same run, if there is one.** It is named on the
run's outcome beside the proposals that were kept, and it is carried on the run's
own state, tagged with the role that proposed it, so that role's next invocation
on the run — a repair attempt, a continuation triage granted, the re-ask an
interim reply earns — opens with it in the harness's own words: nothing was
recorded, nobody was asked, do not describe the proposal as raised, take the
claim back out of anything already written, and propose it again if it is still
worth proposing. That role's next reply spends it, so an agent is told once, and
a refusal is only ever shown to the role that earned it. Since the developer is
the only role carrying the block today, the developer is the only role that earns
one.

The condition is the whole of it, and it is worth reading plainly: **a
developer invoked once, whose only reply carried the refused block, is never
told.** The harness cannot read a proposal before the reply that carries it, and
a run whose one attempt succeeds asks that developer nothing afterwards, so the
refusal is on the run for you alone. What covers that case is the contract rather
than the carry-back — every developer is told in advance that writing the block
is not the proposal being recorded, that the harness can only answer afterwards,
and that nothing it writes may therefore assert a proposal has been raised. The
carry-back is what repairs a claim the contract did not stop; the contract is
what stops it where nothing can carry anything back. The artifact ids are what
`yoyo artifact list` prints.

**Nothing an unapproved proposal contains reaches the document, and neither does
anything an approved one contains.** A proposal carries what should become true
and why, never replacement prose — the size bound on it is what keeps it an
argument rather than an edit waiting to be pasted. Approving records that the
owner's authority came down in favour of the change; the change itself is then
made by the owner, in the document, in a revision recorded under that role.

Like a report, a proposal costs its run nothing: the run integrates exactly as it
would have, and a proposal the harness cannot read or cannot keep is named on the
outcome rather than failing the attempt it arrived with. It is durable in the
same place and for the same reason — the run that argued the design was wrong is
finished and cleaned up long before anybody decides what to do about it.

**What could not be kept is durable too**, on the run's record rather than only
on the outcome `yoyo run` prints. Every proposal the harness could not read or
record, and every report it could not read or collect, is written onto the run's
state in the harness's own words — `amendment_problem` and `report_problem`, the
same two fields the outcome carries — and `yoyo status <beads-id>` prints each
under the run as `proposal not kept:` and `report not kept:`. They were for a
long time on the outcome alone, which is printed once by the process that made
it and gone with it, so a proposal that was refused, or that was made on a run
whose process died before it reported, read afterwards exactly as one never
made: one run's three lost proposals went unnoticed for four runs on that
account, and could no longer be diagnosed when they were. Nothing spends these
— the carried refusal above is emptied by the developer's next reply, and the
record stays for whoever audits later.

```sh
yoyo amendment list                       # what is waiting to be decided
yoyo amendment list --owner architect     # one owner's queue
yoyo amendment show <id>                  # one proposal and what became of it
yoyo amendment approve <id> --reason ...  # record the change as authorized
yoyo amendment decline <id> --reason ...  # turn it down, keeping why
```

**Every decision is yours, whoever owns the document.** An owning role that runs
is shown what has been proposed against its documents and argues for or against
it — proposals against the brief and the goals are carried into the Lead Product
Manager's conversation, and proposals against the designs, the specifications,
and the decision records are carried into the architect's, each told in so many
words that it cannot decide one. An owner may write its own documents, which is
how an approved change is made: it writes the revision as a typed action,
confirmation follows the document's policy, and the harness performs the write — see [writing a document from a
conversation](artifacts.md#writing-a-document-from-a-conversation). What no
owner can do is decide the proposal from there. Both owners can now be
asked directly: `yoyo agent chat architect` is where the argument about a design
happens. And the argument is made on a cadence rather than only when you open
the conversation: a [recurring task](#working-the-amendment-queue-on-a-cadence)
wakes the owner with the undecided proposals, oldest first and bounded, and
records what it recommended on each — approve, decline, or merge with another,
with the reason — as one batch on the firing's report, which is sent to you
once as one decision list and named on `yoyo status` until you have decided it. But no agent records a decision, `yoyo
amendment` is the only thing that does, and the record says you exercised the
owner's authority rather than that the owner answered — the same override path
`yoyo invariant` documents. A decline keeps the reason it was turned down with,
because a proposal refused silently is one the same argument arrives to make
again.

An owning role recording its own decision is vocabulary the record already has
and nothing produces: what would make it real is a decision the harness carries
out for a role from its own reply, the way it carries out the Lead Product Manager's
tracker actions. Until something does that, read "under the architect's
authority" on a decision as your judgement standing in for the role, taken after
hearing it rather than instead of hearing it.

The reviewer is deliberately not given this block. What it finds wrong with a
change is a finding, which decides whether the change is repaired; a reviewer
that could also propose amendments would have two ways to say one thing. The
Lead Product Manager raises what it cannot place under a goal as a concern, which
stops and asks you, for the same reason.

A developer that could not be talked out of its argument makes it again on every
repair attempt, and the second and later copies within one run are dropped: one
disagreement is one proposal, rather than one per attempt for whoever decides to
answer several times over. Two proposals count as the same argument when they
ask for the same change to the same document; restating the reasoning does not
make a new one, and **neither does rewording what is asked for**. A developer
handed a repair writes its block again rather than copying the one before it, so
one argument arrives spelled two ways, and a comparison that read the wording
literally put both of them in front of whoever decides.

What is compared is the share of content words the two changes have in common,
with the function words dropped — those are what any two English sentences share
whatever they say, so leaving them in narrows the very gap this is reading.
Different documents are never one argument however alike the two read, because
they are decided by different owners. The boundary is measured rather than
picked: on the five proposals one run made about one design, the two pairs the
architect decided as one argument each score 0.47 and 0.97, and the closest pair
the architect decided as two — the same fact asked for in two different sections
— scores 0.28. It sits at 0.4, nearer the duplicates than the midpoint, because
the two errors do not cost the same: a restatement that gets through costs its
owner a second copy of an argument they are already reading, and two arguments
folded into one cost the second of them its decision, silently. Those five
proposals are quoted in the test beside the comparison, so moving the boundary
fails there rather than in an owner's queue.

What it reaches is one run, in whichever process continues it. Every proposal
the run's agents make is written onto the run's own state as `amendments`: each
one raised, with the id it was recorded under, and each one dropped as a
restatement, with the id of the raised proposal it was folded into and the
likeness it was folded on. That record is the memory the next proposal is
compared against, so a run continued in a second process — by a usage-limit
pause that exited on its in-process bound, or by a repair triage re-entering it —
reads it back and folds a restatement made there exactly as the first process
would have. It is also the only record a drop has: a dropped proposal reaches
neither the amendment log nor `amendment_problem`, so a pair the boundary folded
wrongly would otherwise lose its second argument with nothing anybody could
find. `yoyo status <beads-id>` prints each drop under the run as
`restatement dropped:`, naming the document, the proposal it was folded into,
the likeness, and the change as it was written, and `--json` carries the whole
list. The record holds 32 proposals; past that a restatement is raised rather
than dropped, because a drop the record cannot hold is exactly the lost argument
it exists to prevent. Nothing compares a proposal against one an earlier run
raised.

**This is a second proposal path rather than a reuse of the one the conversation
already has**, and that is worth knowing because it was not the first choice. The
Lead Product Manager's work-item proposals live in the conversation that raised them,
in memory, decided inside a turn. A proposed amendment has to survive the run
that raised it, is addressed to an owning role rather than to you alone, and is
decided from the command line days later — so what carries over is the shape
(propose, never defer an edit, decide explicitly, record the decision) rather
than the code. The cost is two vocabularies for one idea: a proposal in the
conversation is a work item, and a proposal in `yoyo amendment` is a change to a
document. Consolidating them is not done.

### Traceability: references and orphans

Identity makes a relationship expressible; it does not make it true. So the
chain is validated across the whole set every time the artifacts are loaded, and
what it finds is **reported, never refused** — the opposite of how a document
with no usable identity is handled, and for the same reason a malformed
specification is still read. A broken relationship is a thing to correct, not a
reason to lose a document somebody wrote. What each document's revision log says
about who changed it is reported in the same place and for the same reason, so
one listing says everything that is wrong with the documents that loaded.

| Reported as | What it is |
| --- | --- |
| `dangling-reference` | A `supports` entry naming an id no artifact answers to. Both ends are named: the file the reference is written in, and the id it names. If that id belongs to a file that is in an artifact home and was refused, the report says so and names it, rather than reading as a document nobody wrote. |
| `orphan` | An artifact that nothing connects back to the brief. Following `supports` upstream from it — through as many artifacts as the chain runs — arrives at no `brief`. |
| `unauthorized-revision` | A revision recorded under a role that does not [own the document](#who-may-change-an-artifact). Reported once per document, naming which entries crossed, because opening the file and deciding is one job however many there are. |

Two kinds are never orphans. The **brief** is the root, so nothing is upstream
of it. A **decision** record says how the product is built rather than what it
is for: it is taken in service of the goals without being a statement of intent
downstream of them. Everything else — goals, non-goals, designs, and
specifications — has to trace to the brief.

Only references that resolve are followed, so a reference that names nothing is
reported once as the broken name it is rather than guessed at. Nothing is
followed twice, so two artifacts that support each other are reported as
reaching nothing rather than sending the check round in a circle. A repository
with no `brief` recorded at all is told that, once per document, instead of
being told that each of its documents is separately unconnected.

An artifact that is `superseded` or `retired` still answers to its id and still
holds its place in the chain. The record of what was intended is what makes a
later change traceable, so a design that traces through a goal since replaced is
not an orphan.

```sh
yoyo artifact list   # broken relationships go to stderr beside the listing
yoyo artifact show v1-goals   # and what is wrong with one document, for that one
```

`--kind` narrows the listing and not the reporting: the chain runs between
kinds, and a listing narrowed to the goals would otherwise hide the design that
names one of them and resolves to nothing.

### Goals, and the work attributed to them

The chain's last link runs from a work item to a goal, and a work item is in the
tracker rather than in an artifact home. So the goals themselves are read out of
the goals artifacts: **every entry under a goals document's `Goals` heading is a
goal work can be attributed to**, and an attribution resolves by naming that
goal's stable identity.

**A goal's identity is written in square brackets at the start of its entry** —
`- [traceable-chain] Maintain a traceable chain ...` — and is lower-case
letters, digits, and single hyphens between them. It is assigned once, never
reused, and unchanged by every re-wording of the sentence beside it. The words
are what a reader reads and what a work item displays; they are not what the
match depends on, so amending a goal orphans no item attributed by identity and
refuses no admission that names the identity. Brackets holding anything else —
a phrase with spaces, a path, a Markdown link — are prose, and an entry carrying
them states no identity rather than a malformed one.

**A goal that states no identity is matched on its words**, with case,
surrounding and repeated whitespace, and trailing sentence punctuation folded.
That is the older arrangement, it still resolves, and it is the one a re-wording
breaks. So is an admission that quotes a goal's earlier wording and names no
identity: the words are the whole of what it gave, and they match nothing.
`yoyo goals list` says which goals carry no identity, `yoyo goals attribution`
says which work items still match that way, and `yoyo goals reattribute` moves
those items onto the identity where the goal has one.

**An identity two active goals carry picks out neither.** It is reported by
`yoyo goals list` on stderr, carried into `yoyo release`'s goals check, and work
naming it is refused until one of the documents is corrected — choosing between
them would be exactly the guess identity exists to remove.

Only a `goals` artifact is read this way. A brief or a design with a `Goals`
heading of its own states no goals work may be attributed to — the goals are the
Lead Product Manager's document, and reading intent out of anything with the right
heading is how a design comes to authorize its own work.

The `Goals` heading is the heading whose **whole text** is `Goals`, at any
level. A title that merely opens with the word — `# Goals for V1` — is a title,
and a document with no such heading states no goals and is reported as stating
none. The exactness is load-bearing rather than pedantic: a title read as the
section opens the goals at the document's top level, and nothing written below
it can then end them by level, so everything in the file becomes something work
may be admitted under.

Each goal is one top-level list entry under that heading, and its statement is
**that entry's opening paragraph, rejoined onto one line**. Markdown is normally
hard-wrapped, so a goal written across several lines is the ordinary case: the
lines that continue it are joined with a single space, and the goal is recorded
whole rather than as its first line. The statement ends at the first thing that
is not more of the same sentence — a blank line, an unindented line, a nested
list entry, or the emphasized `*Supports: ...*` trailer naming what the goal
serves upstream. A trailer is recognised by the emphasis it **opens** with
rather than by where that emphasis closes, so a trailer hard-wrapped across
lines ends the statement exactly as a one-line trailer does; a line that opens
with an emphasized phrase and then carries on in plain text is the rest of a
wrapped sentence, and continues the statement. Everything after the statement
ends describes the goal rather than being part of it or being another, and a
heading below the
`Goals` heading divides the goals rather than ending them. The section ends at
the next heading at the same level or above, **or at any heading stating what
the product will not do** — a `Non-goals` heading ends it wherever it is
written, including nested inside it, so a document that files its non-goals
under its goals rather than beside them is read as ending the goals there rather
than as stating more of them. Attributing work to a non-goal is worse than
attributing it to nothing, so that bound does not depend on how the document was
nested.

**A wrapped goal is recorded whole and reported anyway.** Rejoining is what
closed the silent truncation that recorded only a goal's first line, so nothing
is refused over a wrap and work naming the whole statement still resolves. What
`yoyo goals list` says on stderr about one is that the rejoining is a reading of
the file rather than something the file states: the words an attribution has to
match exist only once the wrap is put back together, and an indent, or a wrapped
line that reads as the `Supports:` trailer, changes the recorded goal without
changing a word of it. A goal written on one physical line cannot be changed that
way, which is why the convention is worth holding rather than merely tolerating
the wrap. Only a goal in a document that still applies is reported, for the same
reason a broken link upstream is only reported for one: a goal in a superseded
document is not one work can name.

| Reported as | What it is | What it means for the work |
| --- | --- | --- |
| `attributed` | Names a goal an active goals artifact states. | The chain holds. |
| `unresolved` | Names something no active goals artifact states. | A claim that is wrong. Admission is refused, and an item already carrying one is reported for correction. |
| `unattributed` | Names no goal at all, and the tracker witnesses none was ever written. | Work admitted before this check existed. Grandfathered: reported, never refused, and nothing stops it running. |
| `lost` | Names no goal, on an item the tracker witnesses one was written onto. | A record that was destroyed rather than never made. Reported and failed. Where the witness kept the words, they are quoted and putting them back is a restoration rather than a fresh judgement; where it kept only that a goal was written, the words have to be recovered from outside the tracker. |
| `uncheckable` | The repository records no active goal, or the goals could not be read. | Nothing was checked, and it is said so rather than reported either way. Admitting work without asking is refused here, because an attribution nobody could check is not one the operator agreed to; the work is proposed instead and they decide, which is how a repository with no goals yet files the work of writing them. |

An identity that no active goal carries is `unresolved` and says so about the
identity, rather than falling back to the wording beside it: an item names one
goal, and reading its words as a second opinion would be the prose key coming
back in through the failure path. Where the match is on wording, nothing beyond
the folding above is guessed at — a paraphrase is `unresolved` with the goals
documents named, because deciding it was near enough is the inference a resolved
attribution exists to replace.

An attribution is written on the item as a `Goal served:` line — by the creation
that admitted the work, or by an `attribute` action afterwards, appended to what
the item already records rather than replacing it. The newest such line is the
item's current claim, so the goal an item was admitted under is never rewritten
and the record of how it came to be attributed survives. The line names the
goal's identity where the goal has one, and carries the words the document
states beside it for reading:
`Goal served: [traceable-chain] Maintain a traceable chain ...`. What the
harness never does is invent an identity: a goal that carries none is written
down in the words it was named by.

Every write that puts a goal into an item's notes also records that goal in the
tracker's own metadata for the item, under `yoyodyne_goal_recorded`. It exists
because the notes are what gets destroyed: `yoyo` only ever appends to them, but
anything else with the tracker's command line can replace them wholesale, and it
has. Six items lost the goal they were created under that way and read
afterwards exactly like work admitted before the check existed, which is the one
state nothing fails on. The witness is outside the reach of the write that does
the damage, so it survives to say both that an attribution was destroyed and
which one.

The notes stay the record. What an item serves is resolved from them and only
from them, and the copy in the metadata is never read as an answer — an
attribution the notes lost and the metadata answered for would report as intact
while the item stayed empty, which is the same silence arrived at from the other
side. The copy says what to put back, and putting it back is a `Goal served:`
line written onto the item like any other. A goal longer than a goals document
may state is witnessed without its words rather than stored cut in half.

**The witness covers a goal only from the moment it is written.** An attribution
made before this existed carries none, so replacing its notes reads as work
nobody ever attributed and does not fail the audit. `yoyo goals witness` closes
that gap: it records, on every work item whose notes state a goal and which
carries no witness, the goal those notes already state. It writes no attribution
and decides nothing — the statement is the item's own, copied to where a careless
writer cannot reach it — and it is worth running once after upgrading, and again
after any bulk import of work attributed elsewhere. It sweeps every status the
tracker holds rather than the queue, because the command that destroys an
attribution reaches a claimed or closed item just as easily, and most of the
losses on record were on items that had already closed.

**The audit reads as far as the sweep does.** `attribution` walks every status
the tracker holds — `open`, `in_progress`, `blocked`, and `closed` — so a
witnessed loss is a `lost` state it reports and exits non-zero for wherever the
item sits. It read the backlog alone until yoyodyne-ifd.276, and what that cost
is the reason it does not now: nine of the twelve recorded losses were on closed
items, so the slice the audit could not see is the slice the losses were actually
in, and two diagnoses of the same destruction were made wrong against a report
that said nothing about them. `--scope=queue` reads `open` and `blocked` alone
for the narrower question, and either way the report opens with the statuses it
read and the ones it did not.

**`yoyo goals reattribute` moves an attribution off the wording.** An
attribution recorded before goals carried identities names the words, and the
words are what the next amendment changes. It resolves what each item recorded
against the goals as they now stand and appends the same goal named by its
identity, deciding nothing about what any work is for. An item whose recorded
goal resolves to nothing, or whose goal carries no identity yet, is reported and
left exactly as it was rather than guessed at — the first is a claim somebody has
to correct and the second is a document somebody has to amend — and the command
exits non-zero while any item is left behind. `--dry-run` reports the same thing
and writes nothing, which is worth reading before a run over a live backlog. Like
the sweep it walks every status the tracker holds.

**`yoyo goals origins` backfills where older work came from.** Every admission
now records who asked for the work, and on whose behalf, as fields on the item in
the same write as the admission: the operator, a role's report (with the report
and the role that filed it), a role's own recurring pass, or the harness itself,
and the directive the work answers where there is one. An item admitted before
that reads as origin unknown. This sets those fields on such an item from what its
own notes already state — the role that admitted it, and the report or directive
it was admitted from — and from nothing else. The notes never said whether the
operator or a sweep asked, so an item citing neither a report nor a directive is
left unknown rather than guessed at, as is one whose notes name more than one of
anything; an item that already records an origin is never rewritten. `--dry-run`
reports what would be set and writes nothing.

`yoyo goals guard` is the same loss stopped rather than reported. Wired as a
`PreToolUse` hook on `Bash`, it reads the command an agent session is about to
run and refuses every recognized `bd update <id> --notes` replacement, even
one carrying a `Goal served:` line. An item's notes are append-only: preserve
every earlier note and add a correction with `--append-notes`. It decides from
the command line alone and never reads the item or opens the tracker. It
also refuses `bd update <id> --status=...` with no `--append-notes` on the same
command: a status set with no note saying what moved it is the other silent
rewrite, the one that on 2026-09-18 reopened two items closed on confirmed merges
and released two escalations with nothing on any of them saying so. The
direction of a move is not readable from the line, so a note is asked for on
every status set there; `--claim` is not a status set and passes. The
harness gives it to every developer run it makes on the Claude Code backend,
which is the backend that passes the hook; any other agent session is covered
only by wiring the same command into that session's own hooks. It is passed to
the provider rather than enforced by the harness, so where the hook does not fire
the command runs as it did before and nothing reports that it did.

```sh
yoyo goals list          # the goals work may be attributed to, their identities, and where each is stated
yoyo goals attribution   # what each work item the tracker holds says it is for
yoyo goals witness       # witness the goals already recorded on work items
yoyo goals reattribute   # move an attribution off the wording and onto the goal's identity
yoyo goals origins       # record who asked for work admitted before admissions recorded it, from its notes
yoyo goals guard         # refuse wholesale notes replacement or a status set with no note
```

`attribution` exits non-zero for an item whose attribution is `unresolved` or
`lost`, and zero for one with none. That asymmetry is the decision, not an
oversight: an item admitted before goals were checked is somebody's to attribute,
and a rule that failed every one of them would stop a backlog to close a gap that
has cost nothing yet. An item that lost the goal it recorded fails for the
opposite reason — it passed the check, and what is wrong is that the record of it
was written over. Attributing one is a judgement about what the work is for, so it
is the Lead Product Manager's to make in conversation and there is no command here that
makes it.

Work that has closed is held to one half of that rule. `lost` fails there like
anywhere else, because the record was destroyed and the witness holds the words
to put back. `unresolved` is counted and named on closed work and does not fail:
the item named what the goals stated when it was admitted, the work is finished,
and the ordinary way it stops resolving is a goal reworded afterwards — which is
what `yoyo stale` reports rather than a claim anybody can now correct. Both halves
are printed in the report, so neither is a rule to be inferred from an exit code.

That leaves a pass still owed. When the check arrived, Yoyodyne's own backlog
carried one attribution across seventeen open items, and **the rest are still to
be attributed**: until they are, `yoyo goals attribution` reports most of the
queue as naming no goal, and that is the queue's real state rather than a
reporting artefact. Grandfathering is what keeps the work running while the pass
is outstanding; it is not a substitute for making it. The pass is made by the
Lead Product Manager in conversation, working from `yoyo goals attribution` and using
the `attribute` action on each item — which appends, so nothing already recorded
is lost.

### What a change upstream leaves stale

A goal can be amended while the designs that serve it and the work admitted
under its old wording carry on unchanged. The amendment is somebody exercising
authority over their own document; the silence after it is the problem, and
`yoyo stale` ends it.

```sh
yoyo stale          # what a change upstream left unanswered downstream
yoyo stale --json   # machine-readable
```

| Reported as | What it is |
| --- | --- |
| a document | An artifact something upstream of it — through `supports`, as far as the chain runs — changed after the artifact itself was last revised. |
| open work | An admitted item whose goals document, or anything upstream of it, changed after the item was admitted. |
| a contradiction | Two active documents of the product's intent that say opposite things, both named: two active briefs; one statement one document states as a goal and another rules out as a non-goal, matched by the goal's identity where both carry one and otherwise by the words; or one goal identity two documents give to different goals. |

A contradiction is reported only where the documents' own structure makes it
readable — whether two paragraphs mean opposite things is a reading for a person
or the owning role, and a guess at it would be a report nobody could trust. Like
the rest, it refuses nothing and `yoyo stale` still exits zero: which document is
right is the owner's decision. `yoyo conformance` carries each one as a note on
its staleness survey.

A change is an `amended`, `superseded`, or `retired` revision. A `created` one is
not: a document that did not exist cannot be what anybody was working from. Each
report names the document that changed, when, the role whose authority it
happened under, and the reason it recorded — a rewording and a reversal of
intent are the same event without the reason.

Two things are never reported. An artifact that is itself `superseded` or
`retired` stated what was intended and stopped, so it is not asked to answer for
what happened upstream afterwards. An item naming no goal, or one the goals do
not state, has no reference to follow at all; that is a gap in the chain, and it
is [reported where attributions are](#goals-and-the-work-attributed-to-them)
rather than restated here. The counts say how many admitted items were judged
and how many were not, so what this could not answer for is never silence.

**Nothing is stored.** Staleness is a comparison over records that already
exist — each artifact's revision log, and the tracker's record of when an item
was admitted — rather than a mark somebody writes. So a document edited by hand
counts exactly as one amended through the harness, a process that dies between
the amendment and anything else leaves nothing unmarked, and there is no second
account of staleness that can disagree with the documents. What it costs is
where it clears: an artifact stops being reported once its owner records a
revision later than the change, which is the durable record that somebody looked
at it, and a work item carries only its admission time, so a stale item stays
reported until it is closed. The tracker's own modification time is deliberately
not used for this — it moves when the harness records what a run cost, and
staleness that vanished because a price was written would be a signal nobody
could trust.

**Stale is not cancelled.** Nothing is stopped, closed, blocked, or reordered,
and the command exits zero whatever it finds. A change to a goal's wording is
frequently not a change to what the work should do, and failing a build over an
edit would teach an operator not to edit. What happens to stale work is the
operator's decision or the owning role's; this surfaces the condition.

A tracker that cannot be read costs the work half of the report rather than all
of it: the documents still report, and the report says the queue was not read
instead of rendering it as one nothing has moved under.

### The release-readiness workflow

Every check above answers for one thing, and `yoyo conformance` asks the whole
set together — which is what [cutting a
release](developing-yoyo.md#cutting-a-release) gates on.

```sh
yoyo conformance          # what a release is tagged behind
yoyo conformance --json   # machine-readable
yoyo conformance --notes  # the Markdown section a release's notes carry
```

The order the checks run in is not code. It is a **workflow definition**: a
state machine in YAML that selects actions the harness registered in Go, maps
each outcome to where the sequence goes next, and ends in one of two terminals —
`ready`, which lets a tag be cut, and `mismatch`, which refuses it. This build
ships one, and a project that wants its own writes it here:

```
.yoyodyne/workflows/release-readiness.yaml
```

Nothing is merged between the two. A project that writes one owns the whole
sequence from then on, which is the only arrangement where reading the file
tells you what actually ran; `yoyo conformance` names which of the two it read,
and the content digest it pinned, in everything it prints.

What a definition can change is the sequence and nothing else. It selects among
the actions the build registered — `conformance.artifacts`,
`conformance.references`, `conformance.invariants`, `conformance.goals` and
`conformance.staleness` — and an action nothing registers is refused rather than
run. Each action's authority is declared in Go, and the gate is compiled under
`repository.read` and `work-item.read` and nothing else, so no definition can
make it write anything. Each state must handle exactly the outcomes its check can
produce — `conforms` and `diverges` for the four that gate, `noted` for
`conformance.staleness`, which reports and refuses nothing — so an unhandled
outcome, or a transition on one the check never returns, is refused here rather
than met halfway through a cut. Validation and compilation both happen before the
first check runs, and a definition that is wrong is refused whole rather than
half adopted:

```yaml
schema: 1
id: release-readiness
summary: what this project checks before it tags
initial: artifacts
states:
    artifacts:
        action: conformance.artifacts
        on:
            conforms: references
            diverges: mismatch
    references:
        action: conformance.references
        on:
            conforms: ready
            diverges: mismatch
terminals:
    ready: {}
    mismatch: {}
```

A definition names its own states; `action:` is what selects the check. A state
called `check-artifacts` selecting `conformance.artifacts` is reported as
`check-artifacts`, with the check named beside it, so a renamed sequence reads
against both the file and this build.

A run is recorded durably, one state boundary at a time, under the harness's own
state root, so what a release was gated on can be read back afterwards. That
record is the one thing `yoyo conformance` writes — it touches neither the
repository nor the tracker — and nothing prunes them; one is written per
invocation.

## Architectural invariants

The architect's durable constraints live in a second configured directory:

```yaml
product:
  id: example
  repository: .
  invariants: docs/decisions/invariants   # the default; nothing to write down if you use it
```

An **invariant** is a cross-cutting constraint that outlives the work item that
established it — the kind a later change breaks while its own work looks
correct. One Markdown file per constraint, named by its id, with the metadata in
frontmatter and the constraint itself in two required sections:

```markdown
---
id: one-writer-per-item
title: One process at a time acts on an in-flight work item
status: active
established_by:
    - yoyodyne-ifd.2.7
scope:
    - internal/runstate
revisions:
    - action: created
      by: architect
      at: 2026-08-17T12:00:00Z
      reason: extracted from the decision that added the reservation
---

## Must hold

Every entry into an in-flight run takes the run's exclusive lease first.

## Why

The lease is the only thing keeping two processes off one in-flight item.
```

Only files directly in this directory are read, because the file name is the
identity; a `.md` filed in a subdirectory is reported rather than read. One name
is not read at all: `README.md` is the directory index this home carries like
every other, so it is skipped rather than reported as a malformed constraint, and
`yoyo invariant create readme` is refused because a constraint written there
would be one nobody is held to. `scope`
is optional: an invariant without one is repository-wide and reaches every work
item, and a scoped one is delivered when the work item's prose — or, for the
reviewer, the change itself — names a path it constrains. A missing directory is
not an error; the project simply records no invariants.

Writing these by hand works, and `yoyo invariant create|amend|retire` is the
supported path: it validates the constraint, records who changed it and why, and
refuses every role but the architect. Retirement sets `status: retired` and
records the reason. The file stays and stops being delivered, because an
invariant that vanished leaves whoever read it last month with no way to find out
it was lifted.

A file in this directory that cannot be read as an invariant is **reported and
not delivered**, which is the opposite of how a malformed specification is
handled and deliberately so: half a constraint is not one a developer can be held
to. `yoyo invariant list` names it on stderr, the gap is stated in the prompts
the harness builds, and it is recorded on the work item, so a set that is missing
something never looks complete.

## Checks

Each entry runs through `/bin/sh -c` in the run's worktree, so shell syntax is
available. A check must be non-interactive and must exit non-zero on failure: a
failing check ends the run before any reviewer is asked and before anything can
be integrated. Checks are the project's own — the bundle supplies none — and the
list is replaced wholesale rather than merged.

```yaml
# Go
checks:
  - go test ./...
  - go vet ./...
  - gofmt -l . | (! grep .)

# TypeScript / Node
checks:
  - npm ci
  - npx tsc --noEmit
  - npm test -- --run
  - npx eslint .

# Python
checks:
  - python -m pytest -q
  - python -m ruff check .
  - python -m mypy .

# Java (Maven)
checks:
  - mvn --batch-mode --quiet verify

# Java (Gradle)
checks:
  - ./gradlew --no-daemon check
```

Note the shape of the Go formatting check. `gofmt -l` exits 0 even when it
lists unformatted files, so `gofmt -l .` on its own is not a gate: it reports a
problem and then passes. A check has to turn that output into a non-zero exit,
as above or in a Makefile target. This repository learned it the ordinary way,
by integrating an unformatted file through a green check run.

Prefer the non-interactive, non-daemon, pinned-install form of each tool. A
check that prompts, starts a watcher, or resolves dependencies differently
between runs makes the integration gate nondeterministic.

### What a developer has to have run

The checks above are what the harness runs. What a developer has to have run
itself is decided from them, and it is asked for rather than assumed: every
developer's reply records the commands it executed, and the harness refuses a
change that records none before it spends a suite on it.

Two things are asked, and only the first is universal.

- **The probe.** One execution of a declared check, or of the build step
  underneath it, made in the worktree before anything is changed. Every run is
  asked for it whatever the work turns out to be, and what it answers is whether
  commands run here rather than whether they pass. A probe the developer records
  as `refused` — the command never started — ends the run naming what refused,
  because nothing a developer does to its change fixes an environment that
  cannot spawn a process. A probe recorded as `failed` is the opposite finding:
  the environment works and something else is red, usually the commit the run
  was cut from, so the run carries on and the configured checks report the
  failure with the repair loop behind them.
- **The check run.** The developer's own record of running a check against the
  change it is handing over. This is asked only of a change the declared checks
  would actually read: a change to content nothing here checks submits on the
  probe alone. Demanding a suite run for a change the suite never reads teaches
  padding rather than verification, which is why the line is drawn rather than
  the bar raised.

What belongs in the `detail` of anything but a pass is the message the command
itself printed, rather than a paraphrase of it, because a tool that refuses
often says how to stop refusing and that sentence is the whole value of the
record. The class this was written for is already closed from the other side:
the Go build cache defaults under the user's home, which a run's sandbox does
not grant, and the harness points `GOCACHE` at `.git/yoyodyne/go-build` for
every run it makes — the developer's own probe included, as
[the environment a check runs in](#the-environment-a-check-runs-in) describes.
An environment the harness did not make is the project's own to warn about, and
this repository's `make` targets refuse with the redirect named; a developer
copying that refusal into the `detail` puts the fix in the run's record rather
than leaving the next reader to rediscover it.

Which files the checks read is a mechanical question rather than a developer's
judgement, and the answer comes from the checks themselves. This repository
keeps a ledger of what it is made of — every content class, and for each one
either the declared checks that exercise it or why nothing does — and the bar is
read off that, so a class that gains or loses coverage moves what is asked of a
developer without anything else being edited. The ledger is consulted only for a
project that declares the checks it was written against; a project with checks
of its own is asked for the record on every change, which is the stricter of the
two answers and the one that costs nothing to be wrong about.

### What a check leaves running

Every command the harness runs is the leader of a process group of its own, and
that group is killed when the command ends — whether it succeeded, failed, timed
out, or was cancelled. So a check that backgrounds something and exits leaves
nothing behind: the background work dies with the check that started it, and a
cleanup step the check only reaches on its happy path is not what the machine
depends on.

That is the point rather than an inconvenience. A check is a question about the
change, asked and answered inside the run; anything still running afterwards is
spending the operator's machine on a run that is over, and every run working
beside it pays for that. A daemon a check genuinely needs is started by the
check and stopped by it, inside the one command.

The reap reaches the group and nothing further. Work that puts itself in a
session of its own — `setsid`, a launchd job, a tool that deliberately detaches
what it starts — is outside the group by the time the command ends, and nothing
here kills it. What bounds that work is the bound it carries itself, which is
why background load a check spawns should stop on its own however the check
ends.

### The environment a check runs in

A check does not inherit the harness's environment. It is given one the harness
builds from an allowlist — the same one every provider invocation is built from
— so nothing the shell that started the harness happened to export reaches the
project's own commands. What a check sees is:

- what a program needs to run at all: `PATH`, `HOME`, `USER`, `LOGNAME`,
  `SHELL`, `TMPDIR`, `TERM`, `TZ`, `LANG`, `LANGUAGE`, and `LC_*`
- `SSH_AUTH_SOCK`, so a Git command over an SSH remote — a private module the
  checks fetch — can still ask the agent that holds the keys
- the proxy and certificate settings: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`,
  `ALL_PROXY` and their lowercase forms, `SSL_CERT_FILE`, `SSL_CERT_DIR`, and
  `NODE_EXTRA_CA_CERTS`
- what the toolchains read: everything beginning `GO`, `XDG_*`, and Git's
  environment configuration (`GIT_CONFIG_*`), which is also where the
  maintenance fence every harness-launched process carries lives
- the harness's own `YOYODYNE_*`
- `GOCACHE`, pointed at `.git/yoyodyne/go-build` inside the repository being
  checked, replacing whatever the harness's own environment said. Every run the
  harness makes is given the same redirect, so a developer's own execution of
  the checks and the harness's run of them afterwards share one cache.

And whatever that list admits, a name that reads as a credential — anything
`yoyo` would redact from a process's output: `*TOKEN*`, `*PASSWORD*`,
`*API_KEY*`, `*_SECRET`, and the rest — is dropped. The list exists so that no
run's subprocess tree ever holds a Slack token, which
[`docs/slack/setup.md`](slack/setup.md#where-the-tokens-go-and-what-the-harness-guarantees-about-where-they-do-not)
states as the guarantee it is; a check is a process the harness launches for a
run, so it is held to the same rule. A check whose tooling reads a variable not
on the list does not see it, and the place to set one is the command itself —
`FOO=bar make test` — where it is versioned with the project and visible to
every reviewer rather than a fact about one operator's shell.

The build cache is there because the Go toolchain's default cache is under the
user's home, which a developer run's sandbox does not grant: without the
redirect the first Go command in a run fails at setup with `operation not
permitted`, which reads as a broken toolchain. A project whose checks are not Go
is unaffected by a variable its tools never read.

`PATH` is the harness's own with the provider CLIs left off it: `claude`,
`codex`, and the executable every entry under `providers` names in their place.
A directory on the path that holds one of them is replaced, for the length of
the check stage, by a directory in the temporary directory holding a link to
everything the original holds except those executables, so the toolchain
installed beside a provider CLI is still found. The checks run where a provider
CLI usually is not installed as well — the forge's continuous integration, a
collaborator's machine — and a test that passed only because one is installed
here would fail there, after a review has been spent on it. Under this rule it
fails in the check stage instead. A test that genuinely needs a provider CLI
skips itself, saying why, where none is found.

A provider invocation is given the same list with one thing more:
`YOYODYNE_AGENT_ROLE`, naming the role the process was launched for —
`developer`, `reviewer`, and so on. It is under the harness's own prefix so the
allowlist carries it into everything the agent starts, and it is what
[the verbs that record a person's decision](operations.md#pausing-everything-and-resuming-it)
— `yoyo pause`, `yoyo resume`, `yoyo release`, `yoyo artifact approve` — read
to refuse a shell an agent opened. A check the harness runs itself does not
carry it: a check is the project's command, launched by the harness rather
than by an agent.

### The environment the harness's own Git and forge commands run in

Every Git command the harness runs itself gets that same list, and for a reason
of its own. Git runs hooks, and a hook is a program the repository supplies and
the harness executes: `git worktree add` runs `post-checkout`, a ref update runs
`reference-transaction`, and both of those live in `.git/hooks`, which every
worktree the harness cuts shares. So a Git command that inherited the harness's
environment handed whatever that environment carried to a program the harness
never wrote — the Slack tokens included, by a path the run's own built
environment says nothing about.

**The forge credential is added to the forge commands and to nothing else.**
Those are the `gh` invocations the harness makes and the Git commands that reach
a remote — the push, the fetch, `ls-remote`, and the delete of a merged branch.
They carry, on top of the list above, whichever of `GH_TOKEN`, `GITHUB_TOKEN`,
`GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN`, `GH_HOST`, `GH_CONFIG_DIR`,
`GIT_ASKPASS`, `SSH_ASKPASS`, `GIT_SSH`, `GIT_SSH_COMMAND`, and
`GIT_TERMINAL_PROMPT` the harness's own environment holds. Every local Git
command — a diff, a ref update, a checkout, a `worktree add` — gets none of
them, so the hooks those run have no forge credential to hand out. Handing every
Git command a token so that the push would have one is exactly the arrangement
this replaces.

`SSH_AUTH_SOCK` is not on that second list and does not need to be: it is on the
standing one above, so **a project whose remote is SSH pushes through the agent
exactly as it always did** — every process the harness starts carries the socket,
and the keys stay with the agent holding them. It is worth saying because the
absence reads like an omission, and the cost of it actually being one would be
every run stopping at integration on every installation with an SSH remote.

### Which provider authentication is supported

**A provider authenticates by its own login, held in its provider home, and by
nothing else.** That is what the accounts machinery names an account by, and it
is the only authentication an invocation the harness makes receives.

A key exported in a shell — `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`,
`OPENAI_API_KEY` — reaches none of them. It reads as a credential, so the
allowlist drops it from every process the harness launches, exactly as it drops
the Slack tokens. An installation that had been authenticating that way does not
degrade: the provider refuses its next run.

Three things say so before that run happens, and none of them is a gate.
[`yoyo doctor`](operations.md#which-provider-authentication-is-supported) reports
it under `provider-authentication`, as a warning, with the login for this
project's own provider as the remedy; `yoyo config validate` says it beside the
validity answer, on standard error, and carries the variable names under
`provider_keys` in its `--json`; and `yoyo slack` says it once when the sink
starts, because the shell that starts a sink is usually the shell the harness
was started from. All three name the variables and never their values.

### What `init` proposes for `checks`

A project does not start from the empty list unless it has to. `yoyo init` reads
what the repository already announces about its own toolchain and writes the
commands that follow into `checks`, each under a comment naming the artifact it
was derived from:

| What is there | What is proposed |
| --- | --- |
| a Makefile with a `check` target, or with `test` and no `check` | `make check` / `make test` |
| `go.mod` | `go test ./...`, `go vet ./...` |
| `package.json` with a test script and exactly one lockfile | the lockfile's install, `npm`/`yarn`/`pnpm test`, and `tsc --noEmit` where there is a `tsconfig.json` |
| `pyproject.toml`, `pytest.ini`, `setup.cfg`, or `tox.ini` naming pytest | `python3 -m pytest -q` |
| `pom.xml` | `mvn --batch-mode --quiet verify` |
| a `gradlew` wrapper | `./gradlew --no-daemon check` |

**Nothing is executed.** Detection is by artifact presence and by reading those
artifacts, because running a stranger's build to discover what it is is not a
first impression worth making, and because a command that has to run to be
proposed is one that runs before anybody has reviewed it. This is a convenience
default derived from the project's own files rather than an understanding of
toolchains in the harness: what runs is still only the shell commands this list
declares, judged by their exit codes.

**Whatever is not written into `checks` is written beside it, commented out,
under a heading that says what it wants from you.** There are three, and only the
first asks for anything:

| Heading | What it means | What you owe |
| --- | --- | --- |
| `YOU MUST CHOOSE` | detection could not tell which command is the gate, and `checks` is empty | a choice: a run is refused until there is one |
| `ALSO FOUND, AND NOT DECIDED` | the same, except `checks` was written from something else and works | nothing; the question is open, not blocking |
| `ALSO FOUND, AND NOT NEEDED` | commands detection read and decided against, because what it wrote covers them | nothing |

The distinction is the point. A demand to choose is worth reading only where a
run cannot happen until somebody does; putting it over an already-runnable file
teaches an operator to scroll past it.

Taking any of them is the same gesture: delete the leading `#` and nothing else,
and open the list above with `checks:` if it is still `checks: []`. Each carries
the reason it is where it is.

**A Makefile supersedes the language-native commands**, which is the ordinary way
into the third heading. A project with a `check` target and a `go.mod` gets
`make check`, and `go test ./...` and `go vet ./...` appear under
`ALSO FOUND, AND NOT NEEDED` rather than being added, because two gates running
the same suite is the suite run twice. Nothing about that is undecided, so
nothing about it demands a decision.

**What cannot be settled is not settled**, which is the first two headings. The
cases that reach them today are:

- Python tests with no runner named anywhere. unittest discovery over
  pytest-style tests collects nothing and exits 0, which is a gate that passes
  everything, so neither runner is written.
- A `package.json` with no lockfile beside it, or with more than one, which
  leaves how the project installs unsettled.
- A `package.json` that declares no `test` script at all, or whose only one is
  npm's `exit 1` placeholder: nothing there says how the project is tested.
- A Gradle build script with no `gradlew` wrapper to pin the version a check
  would run under.

Which of the two headings they land under depends only on whether anything else
in the project produced a `checks` list to stand on.

A repository that announces none of this keeps `checks: []` and the commented
per-language examples above, which is what it always did.

### How long a check may take

Each check gets a budget, and a check that exceeds it is killed and ends the run:

```yaml
execution:
  check_timeout: 30m   # the default; per check, not for the list
```

It is the *total* time a check may run rather than the time it may stay quiet: a
suite printing a result every second is spending it just as fast as one that has
gone silent. The `30m` default is deliberately generous, because a check stopped
at this bound is not a check that judged the change — the work may have been
passing the whole way, and killing it costs a run that had nothing wrong with it.

**Concurrency multiplies what a suite takes, so this has to scale with it.**
`max_concurrent_developers: 2` does not give each run its own machine: two suites
contend for the same cores, and each one's wall clock grows accordingly — about
twofold for this repository's own suite, and further under whatever else the
machine is doing, including the provider processes the runs themselves keep busy.
The budget is spent in wall clock, so N concurrent runs need a budget set against
what the suite takes with N of them running, not against what it takes alone.
Either raise `check_timeout` to match, or lower `max_concurrent_developers` so
the suites serialize; leaving both at values chosen independently is how a
passing suite gets killed. This is the failure that produced the setting: a flat
ten minutes, a suite past forty packages with real Git integration tests, and two
concurrent runs — the tests were passing package by package when the bound
stopped them.

Every check reports what it spent against what it was allowed, whether it passed
or not. The completion event carries `elapsed` and `timeout`, and the run's notes
on the work item carry the same pair per check, so a suite growing toward its
ceiling is visible run after run rather than only in the run the ceiling finally
stops. When one does time out, the failure names both numbers and the two
settings that move them.

A budget of `0` is refused rather than read as "unbounded": nothing else bounds a
check, so one that never returns would hold a worktree, a claim, and a run open
indefinitely.

### What a whole check stage may cost

The budget above bounds one check and says nothing about the list. Four checks
each inside a thirty-minute budget are a check stage that may run for two hours,
and on 2026-09-19 one did: a run on this repository sat in its checks for over
two hours under load, with `make race` alone past ninety minutes, holding its
developer seat and the watch session's drain for the whole of it. So the stage
has a bound of its own, beside the per-check one. A session
[draining to restart into a deployed build](#watching-instead-of-draining)
waits out a running check stage only until `redeploy_drain_limit`, independently
of the stage's bound:

```yaml
execution:
  check_timeout: 30m         # per check
  check_stage_timeout: 30m   # the whole list, from the first check starting to the last ending
```

**The bound in force scales with the machine's load**, exactly as a local Git
command's budget does: `check_stage_timeout` is the figure for an idle machine,
and where the one-minute load average is above the number of cores it is
multiplied by how far above — twice the cores is twice the bound — up to ten
times the configured figure. It is the same reading and the same cap the Git
budget uses (one function, `gitworktree.ScaleForLoad`), because the two were
stopping working commands under the same load: on 2026-09-26 a stage with the
gate already narrowed to three packages was stopped in `make race` at a load
average of 40 to 55 on 16 cores. The load is read as each check begins rather
than once, because the load a stage starts under is not the load three suites
beside each other build up, and the bound only ever grows: a check already
given what the stage had left keeps it. A platform that cannot report its load
holds the stage to the configured figure.

Each check is given the smaller of its own budget and what the stage has left,
and a check the stage has nothing left for is not started. A stage that reaches
its bound **ends the run as a stoppage** — `timed_out`, no repair attempt spent,
the change preserved — and what it names is the bound, the configured figure
and the load that scaled it, the check it stopped and how long that check had
run, what the stage had spent across how many checks, and the two things that
move it: narrow the per-run gate to what the change touches, or raise the
bound. That is a different failure from a check reaching its own budget, and it
is reported as one, because raising `check_timeout` does nothing for a check the
stage stopped.

**A stage the scaled bound still stops is a stop from outside the work.** It is
recorded on the run as one, of cause `check-stage-bound`, naming the bound, the
load, and the check; the run keeps its branch, its worktree, and its developer
session; and it counts toward nothing — not the
[failure-storm brake](#watching-instead-of-draining), and not the item's review
rounds, repair grant, or re-run. Because a stage the bound stopped judged
nothing, the harness [continues it at its checks by
itself](operations.md#what-a-check-stage-may-cost-and-where-the-whole-suite-runs)
— on the change the run already has, at a pull with a slot free, ahead of
fresh work at equal or lower priority, at most twice per run, spending nothing —
rather than leaving it to a re-run that redoes the development.

**The bound is visible while the checks run, not only when it stops them.**
The run's record carries the stage — when it began, the bound in force, the
configured figure and the load reading that scaled it, and which check it is
on — so `yoyo status` says where a run in its checks stands in place of the
bare phase, with the configured figure beside a bound the load raised:

```text
Running (1 developer run):
  yoyodyne-ifd.389 (Timing-bound tests do not fail the gate under machine load) — checks: 14m of 30m, on make race, 1h02m elapsed, $4.10 so far
  yoyodyne-ifd.432.13 (…) — checks: 41m of 90m (30m configured, scaled for a one-minute load average of 48.0 on 16 cores), on make race, 1h20m elapsed, $6.75 so far
```

The same figures reach the item: the run's notes carry `Check stage: 14m0s of
the 30m0s execution.check_stage_timeout bound` above the per-check lines — with
`(the configured 30m0s scaled for a one-minute load average of …)` after the
bound where the load raised it — and what the gate was narrowed to beside it, and a stage the bound stopped says so
there in the same words `yoyo status` uses for the run. The Slack thread's
"checks passed" line says what the stage spent of its bound, for the same
reason the per-check pair is recorded on every run: a stage walking toward its
bound is visible run after run, before the run the bound stops.

The default is thirty minutes on purpose, and in minutes on purpose. It is the
per-check default rather than something above it, because the bound is what
makes the per-run gate worth narrowing: with the race suite narrowed to the
packages a change touches — [below](#where-the-whole-suite-runs) — this
repository's whole stage fits it with two runs contending, and the load scaling
is what keeps it fitting when a third suite beside them loads the machine past
its cores. A project whose
stage does not fit it is told, on the first run that reaches it, which check
the bound stopped and what moves it. Like the per-check budget it must be
positive; a stage with no bound would be every check's budget added up again.

### Where the whole suite runs

A per-run gate that runs the whole suite on every attempt spends the suite's
cost several times per change and pays it in wall clock under contention, which
is what the stage bound above then stops. The arrangement that fits inside the
bound is two halves: the per-run gate runs the expensive suite **narrowed to
what the change touches**, and the whole suite runs **once per landing** over
what actually landed.

**Narrowing.** Every check is given `YOYODYNE_CHANGED_GO_PACKAGES` in its
environment: the Go packages the change touches, as the `./dir` patterns the Go
command takes, sorted and without repeats. A changed file belongs to the nearest
directory above it that holds Go source — a package's test data and embedded
files are the package's, as the Go command itself files them — and a file above
every package, a document or the Makefile, belongs to none. The variable is
`./...` where the harness cannot narrow: a change to `go.mod` or `go.sum` or the
vendor tree reaches every package, and a repository that is no Go module has
nothing to narrow within. It is empty where the change touches no Go package at
all. A check that never mentions it runs exactly as it always has; a check
written to read it runs over that and nothing else. What the narrowing cannot
see is a package that depends on a touched one, which is what the landing half
is for. The run's record says what the gate was narrowed to, and so do the
item's notes.

**Landing checks.** `landing_checks` is a second list beside `checks`, run
once per landing on the target branch — after a run has integrated, closed its
item, and removed its worktree — in a detached checkout of the integrated commit
cut under the worktree root for the purpose and removed afterwards, and told
`YOYODYNE_CHANGED_GO_PACKAGES=./...` because a landing is where the whole
suite runs. It runs under a budget of its own:

```yaml
execution:
  landing_check_timeout: 2h   # per landing check; the list has no stage bound
```

The budget is the landing's rather than the gate's on purpose. What is moved
to the landing is exactly the suite the gate's stage bound cannot hold, so a
landing held to `check_stage_timeout` would be stopped on every landing of the
repository that needed it; each landing check gets `landing_check_timeout`
whole, and the list may take the sum. The default is two hours, which is what
the whole race suite took under load on 2026-09-19 with room to spare.

**What waits on a landing, and what does not.** The landing checks are run by
the process that made the landing, after the run is terminal, its item
settled, and its worktree removed — so the run's developer seat is free and
`yoyo work` can start the next run beside them, and the run reads as succeeded
everywhere while they run. What does wait is whatever waits on that process
returning from the run: `yoyo run` prints its result only once the landing has
ended, a `yoyo work` drain or a `--limit` returns only once every run it
started has landed, and the restart a deployed build causes waits out every run
the session started, landing included — up to
[`redeploy_drain_limit`](#watching-instead-of-draining), past which the
landing checks are stopped and the landing is recorded as unverified. So a
landing holds `yoyo run` and a drain or a `--limit` for up to
`landing_check_timeout` times the number of landing checks — two hours a check
by default — plus any time it spends waiting its turn behind another landing
(below), and a project that cannot afford that on a drain lowers the budget or
shortens the list; it does not hold a seat, a claim, or the queue.

**One landing at a time, per target branch.** Landings on one target branch
queue on a lease of their own, so at most one landing suite runs at a time
however many runs land back to back: with two developer seats, the most the
checks put on the machine is one landing suite beside two narrowed gates, not
two whole suites beside them. The lease is an advisory file lock beside the
branch's promotion lease in the run state directory, dropped by the operating
system when its holder dies, and it is not the promotion lease — a landing
holds nobody out of integration. A landing that finds another running records
when it began waiting before it waits, and `yoyo status` says so under the run
until it is let in. The wait is bounded by what the landing ahead may take —
`landing_check_timeout` times the number of landing checks, and a
fifteen-minute margin for its checkout — and a landing that waits that out
runs nothing and is unverified. The bound covers one landing ahead, so a third
landing queued behind two full-budget suites waits it out unverified. The landing checkout runs its checks against
the repository's shared build cache, the one under the common Git directory
every run's worktree compiles against, rather than a cold one of its own.

A landing whose checks all pass on their own exit is **green**; one where a
check fails on its own exit is **red**; one whose checks did not run to a
verdict — no checkout could be cut, a check was stopped at its budget, the
landing waited out its turn behind another, the process running them died — is
**unverified**. A stopped check judged nothing,
which is the rule the per-run gate already applies to a check it stops on
time, so a landing it happened in files nothing and says why instead. All
three are recorded on the run, said on the item's notes, and said in the run's
Slack thread, and a red or unverified landing reaches the channel because it is
the one fact about a landed change that the run's own ending does not carry.

**A red landing files its own item and blocks nothing.** The run that landed
the change succeeded on the gate it was given and was approved; a red landing
is news about the target branch, not a verdict on that run, so nothing is
reopened, failed, or blocked. What happens instead is that the harness admits a
bug at priority 0 — the front of the queue, where this project puts an
operator's order — naming the target branch, the commit, the check that failed,
and the run and item that landed it, under the goal the landed item served,
because every run after it is cut from that commit and a red target branch is
the thing to fix first. What the check printed goes in that item's notes and
nowhere else: the title and the description are fields the
[protected-path gate](#protected-paths-in-a-developers-change) reads grants
from, and check output is text a change can shape, so the harness keeps those
two to its own words. In the notes it is quoted, a `> ` on every line, because
the notes are read line by line for the item's `Goal served:` and for the
marker below, and a check that printed either must not be taken for it. The
item is named on the run (`filed as
yoyodyne-ifd.402`) and on the landed item's notes. A target branch that stays
red is one item rather than one per landing: a later red landing of the same
check on the same branch finds the item still open — by the
`Red-landing check:` line its notes carry — notes the later commit on it, and
files nothing (`red again on yoyodyne-ifd.402, filed by an earlier landing`).
A red landing the tracker would not take an item for is still recorded and
said as red, with the refusal beside it. This is the operator's standing order of 2026-09-19, and it
is the one place the harness admits work on its own account: it goes straight
to the tracker under either `approvals.work_items` value, which
[what reaches the queue](#what-reaches-the-queue) states as the exception it
is.

For this repository the two halves are written as, with the Makefile's `race`
target taking the packages it covers as `RACE_PACKAGES` and passing on an empty
one:

```yaml
checks:
  - make fmtcheck
  - make test
  - make race RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
  - make vet

landing_checks:
  - make race
```

**Read the variable with the shell's unset-only default, `${…-./...}`.** The
variable is set only by the harness's check runner, and the declared checks
are run in other places too: a developer executes them in its worktree for its
minute-zero probe and its submission evidence, and a person runs them by hand.
A line that read an unset variable as "nothing to test" would print that and
pass, which is a green race check nobody ran. With the unset-only default those
runs test the whole module, while inside the harness the variable is always
set — to the packages, to `./...`, or to nothing at all for a change touching
no Go package, which the Makefile's `race` target says and passes on. A check
written directly against the Go command wants the same shape:
`set -- ${YOYODYNE_CHANGED_GO_PACKAGES-./...}; [ $# -eq 0 ] || go test -race "$@"`.

A project that names no landing checks lands exactly as it did before they
existed, and a check list that never reads the variable is a gate that runs
whole on every attempt, bounded by the stage. Each entry is a shell line like
the checks above, non-interactive and non-zero on failure, and an empty one is
refused when the configuration loads. The landing checks are run by the process
that made the landing, once its run is over: a run whose process died and whose
integration `yoyo reconcile` settled afterwards lands without them, and its
record carries no landing rather than a green one. So does a run whose merge the
forge queued rather than performed: its change is on no target branch when the
run ends, and the sweep that settles the merge later runs no landing checks. On a
[protected target](#a-protected-target-lands-through-its-pull-request) whose
merge the forge performs at once, the checkout is of the promoted commit the
pull request carried, which is the tree the merge put on the target. A process that dies inside
the landing checks leaves a run that is over with a landing the record says is
running; `yoyo reconcile` settles that landing as unverified, saying the process
died, and removes the checkout it was running in — a live process running them
holds the run's lease and is left alone.

### Checks a change runs only when it touches what they vouch for

Some checks vouch for one part of the repository and cost something whatever
they are given: a walkthrough of the documented install, say, which a change to
the documentation or to the program it documents can break, and a change to a
design note cannot. `path_checks` is a list of those, each a command and the
file in the repository listing the paths it vouches for:

```yaml
path_checks:
  - command: make adoption
    paths: scripts/walk-adoption.paths
```

The per-run gate runs every entry in `checks` and then each path check whose
list covers a path the change touches, in the order written, under the same
stage bound, in the same environment, and to the same effect: a path check that
fails is a failing check, handed to the developer to repair like any other, and
one that passes is a check result the review is shown beside the rest. A change
that touches nothing on a path check's list does not run it. The run's record
and the item's notes say which path check the gate added and which changed
path added it, where they say what the gate was narrowed to.

The list lives beside the thing it describes rather than in this file, and a
change that edits it runs the check whatever the edited list says. Otherwise
the author of a change could narrow or empty the list and switch off the check
meant to hold that change; so a change to the list costs that change one run
of the check, and for every other change the list reads as the target branch
holds it. It takes the part of a `.gitignore`'s syntax everybody already
reads: one pattern a line, blank lines and `#` comments ignored; a pattern with
no `/` matches any component of a path, so `*_test.go` is every Go test file; a
`/` anchors a pattern at the repository root; a trailing `/` means a directory
and everything under it; and a leading `!` takes back what an earlier line
covered, the last matching line deciding. A list that cannot be read — missing,
or carrying a line that is not a pattern — runs its check rather than passing it
over, because the gate has no declaration to skip it on, and the record says so.

Path checks are not landing checks and are not narrowed: a landing runs
`landing_checks` alone. An entry with no command, or with a `paths` that is
absolute or leaves the repository, is refused when the configuration loads.

## Scheduling ready work

`yoyo run <id>` is you naming an item. `yoyo work` is the harness choosing:

```yaml
execution:
  max_concurrent_developers: 1   # the default
```

It reads the admitted work in the order you set — highest priority first — takes
the items the tracker itself reports as ready to pull, and starts as many of them
at once as this leaves free. Each run gets a worktree and a branch of its own,
and the command returns once every run it started has ended. `--limit <n>` stops
it after that many runs; without one it drains what is ready, and
[`--watch`](#watching-instead-of-draining) keeps it open instead.

Nothing about running several at once relaxes anything. Capacity is enforced at
the reservation rather than by the scheduler, so two schedulers, or a scheduler
and a `yoyo run` beside it, share one limit rather than getting one each — a run
that loses the race for the last slot is reported as declined, not as a failure.
Integration stays serial: at most one promotion into a given target branch
happens at a time, and a change whose target moved while it was being reviewed is
replayed onto where the target went and promoted by fast-forward — or, on a
target the forge protects, replayed the same way and then landed through its
pull request rather than by a local fast-forward
([a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request)).
A replay that conflicts is handed back to the change's own developer to
reconcile on top of the target, as a repair attempt that is checked and reviewed
again, and the run blocks only once a budget is spent. Nothing is ever forced.

Twelve things keep an item out of a pass, reported at two different grains. The
first nine are named against the item, because nothing else would report that
this item was passed over; the last three are facts about the pass rather than
about any one item.
[How work flows](work.md#letting-the-harness-choose-the-work) lists the same
twelve in the same order, and a test fails when the two lists differ:

<!-- selection-rules: the same names, in the same order, as docs/work.md and docs/configuration/runs.md; internal/doclink/selectionrules_test.go holds them together -->
1. **An unresolved directive** withholds the item until a person resolves the
   directive, and is named in the directive's own words.
2. **Unfinished children that carry its execution** withhold a container while
   any child it was broken into is queued, blocked, or claimed, and release it
   once the last of them leaves the backlog.
3. **A race with work in flight** withholds an item that shares an epic
   decomposition or files with a run in flight, and releases it at the first
   pull after that run ends.
4. **A conversation executor** withholds an item whose `executor` names a
   persona conversation from every developer run; nothing clears it, and what
   moves the item is somebody opening the conversation it names.
5. **Parking** withholds an item the Lead Product Manager parked however far the
   queue drains, and only her `unpark` releases it.
6. **A hold** withholds an item whose stopped run left its change on a branch,
   or whose publication did not finish, until the development manager's
   decision is carried out, the escalation is answered, or `yoyo reconcile`
   settles the publication.
7. **A step only a person can take** withholds an item that declares a
   `human-gate:` nobody has recorded taking, and only a person's
   `yoyo gate record <name> --for <item>` releases it; closing an item never does.
8. **A prerequisite the tree does not meet** withholds an item that pinpoints
   code the repository no longer has, or says in its own words that something
   must land first; a pinpoint releases it when the code lands, and a sentence
   when the item is amended or the dependency recorded.
9. **A label another slot prefers** withholds an item every free developer slot
   walked past for its preferred label, and the next slot with no preference to
   come free — or the preferring slot, once its label's work is exhausted —
   releases it.
10. **The tracker not calling it ready** withholds an item with unfinished
    dependencies or a status that is not open, and the tracker's own readiness
    releases it.
11. **A run already in flight for it** withholds the item while that run lasts,
    and the run ending releases it.
12. **No free developer slot** withholds everything once the slots are taken,
    and any run ending releases one.
<!-- /selection-rules -->

Parking is the one to know about if you watch the queue: it is how deferred
work stays admitted without being pulled, and it exists because on 2026-08-27 a
draining queue reached work a scope decision had put off, started it, and spent
$34.38 on a run nobody wanted. A priority cannot do that job — the bottom of the
order is the last thing pulled, not the thing never pulled. The conversation
executor is the same kind of marker for work a role does in conversation rather
than in a developer run.

The children rule is there because a decomposed epic and the child doing its
work are both reported as ready, and starting both buys the same change twice —
two developers over one file, the second of them guaranteed a conflict at
integration. A race is sequenced behind the run it would have raced rather than
started beside it, named with that run and what the two share — the epic one of
them was broken out of and the other is, or overlapping files. Two items merely
filed under one epic are not racing:
an epic is as often a heading as it is one piece of work broken into several,
nothing tells the two apart from the outside, and holding every child of a
heading behind whichever started first serializes the queue rather than
declining a race. That one
is a wait rather than a refusal: the conflicts are re-read at every pull from
what is actually in flight, so the item is pulled at the first pull where the run
it would have raced has ended, and the slot the hold freed is spent on the next
item down the order that races nothing. An item says which files it will change
by naming them after `conflict-surface:` on a line of its own, in its title,
description, design guidance, or acceptance criteria; an item that declares
nothing has those same fields read for the files it plainly names, and that
inference takes only a path with a separator and an extension on the end, because
a surface invented out of prose would hold unrelated work back. An item the tree
is not ready for — one that pinpoints a `file:line` or a package-qualified
symbol the repository no longer has, or that says in its own authored words that
something must land before it starts — is named with the unmet prerequisite and
routed to the [triage docket](#triage-thresholds) rather than
to a run; [how work flows](work.md#letting-the-harness-choose-the-work) says what
the two readings are, and what the executor, parking, and hold rules each
record. An item every free
[developer slot](#a-developer-slot-that-prefers-a-label) walked past for its
preferred label is named as left for another slot, with the slot and what it
pulled ahead of the item: it waits on nothing about itself. An item that
declares a step only a person can take — named after `human-gate:` on a line of
its own, in those same authored fields — is passed over with that step and what
records it both named, until somebody has recorded taking it with `yoyo gate
record <name> --for <item>`. That one is neither a wait nor something any run
clears: closing a work item does not pass it, which is the whole reason it
exists. The act is recorded against the item that declared the gate and passes it
there and nowhere else, so a name a later item declares again is a step somebody
still has to take. See
[a step only a person can take](work.md#letting-the-harness-choose-the-work) for
what it replaced. The last three
rules are reported as facts about the pass — the
stop reason names which of them ended the choosing, and a pass that got as far as
reading the queue prints how many items were admitted, how many the tracker
called ready to pull, and how many slots were taken. Those are counts rather than
a list on purpose: naming every unready item would print a line per backlog entry
on every pass and bury the deferrals worth reading. A pass that stopped before
reading the queue at all — held intake, or every slot already taken — says
nothing about the backlog rather than reporting zeroes it never looked up.

A decided repair or re-run is fired by the same pass, oldest decision first, and
one about a stopped run whose branch or worktree is still there takes the first
free developer slot ahead of fresh pulls of any priority, naming in its reason
the ready work it went ahead of. Every other decision waits behind ready work of
a higher priority than its item.
[How work flows](work.md#letting-the-harness-choose-the-work) has the whole
rule; it changes no gate and nothing a decision costs.

A thirteenth thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, and what changed
goes into the run's recorded reason instead. See
[what a change upstream leaves stale](#what-a-change-upstream-leaves-stale) for
why staleness reports rather than decides.

`max_concurrent_developers` cannot exceed the number of developer `instances` you
configured, and the default of `1` is deliberate: raising it is a decision about
your machine, and [how long a check may take](#how-long-a-check-may-take) is the
setting that has to move with it.

More than one run means more than one process writing to the tracker, and `bd`
does not protect two writes to one item from each other: two notes appended to
one item at the same moment can leave only one of them, with both commands
reporting success. So the harness queues its writes to any one item, across
every process using the same tracker, on a lock file per item under
`yoyodyne-writes/` in the tracker's own directory (`.beads/` in an ordinary
checkout). That directory is found the way `bd` finds it, so a process reaching
the tracker from a subdirectory, a linked worktree, or a redirect queues on the
same lock. Writes to different items do not wait for each other.
A write that waits longer than the tracker command limit for the one ahead of
it fails with an error rather than going ahead unqueued. The queue covers the
harness's own writes only: a `bd update` typed by hand is not in it.
[The diagnosis](diagnoses/yoyodyne-ifd-433-23-concurrent-writes-to-one-item.md)
has the measurements.

### A developer slot that prefers a label

Each unit of `max_concurrent_developers` is a **developer slot**: the capacity
one developer run takes. By default every slot pulls in the order you set. A
slot can instead prefer a **label** — the tracker's own labels, which the
Lead Product Manager and the development manager put on work items — and then it
pulls the ready work carrying that label first, wherever that sits in the
order, and the rest of the backlog only when none of its label's work is ready.
On 2026-09-19 the operator directed that one of Yoyodyne's own developer
[seats](terms.md#the-register) — one running developer, as against the slot,
which is the capacity it fills — be dedicated to the `reliability` label, and
this is the block that does it, by giving the slot that seat fills a preference
for the label. The block is the operator's to paste into the project's
configuration by hand, because `.yoyodyne/` is a
[protected path](#protected-paths-in-a-developers-change) no run may write, so
a project whose file does not yet carry it has a reliability seat that is
directed and not yet configured:

```yaml
execution:
  max_concurrent_developers: 3
  developer_slots:
    - prefer: [reliability]   # developer slot 1 pulls reliability-labelled work first
    # slots 2 and 3 are not named, so they prefer nothing
```

The reliability label means, in the operator's words, bugs, anything that
keeps the system from stalling, and anything that keeps the system from making
mistakes. The admission practice that goes with it, from the same day: every
item admitted under the reliability directive, every bug, and every stall or
mistake fix carries the `reliability` label from admission, put on by the
Lead Product Manager's `labels` field in the same write that admits the item, so the
item never exists unlabelled. The [conversation guide](conversation.md#backlog-state-that-has-stopped-being-true)
states the same practice where it describes the `labels` and `label` actions,
in the section on an item's tracker state.

`developer_slots` is one entry per slot, in slot order, and it may be shorter
than the capacity — the slots it does not name prefer nothing — and never
longer, because a preference for a slot the capacity does not have is one
nothing would ever act on, so a longer list is refused when the file loads. An
entry names the labels it prefers under `prefer`, any one of which on an item is
enough; an entry written as `{}` or with `prefer: []` is a slot with no
preference, which is how the first slot is left alone and the second given one.
A slot may prefer more than one label, and more than one slot may prefer the
same label. Each label is held to the rule the tracker's actions hold a label to
— one word of letters, digits, dots, underscores, and hyphens, up to 64 bytes —
and compared exactly, so `Dashboard` and `dashboard` are two labels. A list in a
later layer replaces an inherited one whole, as `checks` does.

**What a preference changes is which item a slot pulls first, and nothing
else.** The item a preferring slot starts is claimed, developed, checked,
reviewed, and promoted exactly as it would be from any slot, under the same
contract and the same authority table. Configuration selects what a slot pulls
and never widens what it may do.

Three things follow from a preference, in the order a pull applies them:

- **A preferring slot pulls its label's ready work first.** With the example
  above and a reliability-labelled bug at priority 2 under an unlabelled item
  at priority 1, slot 1 pulls the bug ahead of the unlabelled one; the run's
  recorded reason says it was pulled into developer slot 1, which prefers the
  reliability label the item carries.
- **A slot with no preference leaves labelled work to a preferring slot that is
  free to take it.** With slots 1 and 2 both free, the reliability item goes to
  slot 1 and slot 2 takes the next unlabelled item down the order. Where no
  preferring slot is free — the seat in slot 1 is working on one reliability
  item and another is ready — the label is only a preference, and slot 2 takes the
  reliability item in the order like any other. A label dedicates capacity to
  its work; it never withholds the rest of the machine from it.
- **A preferring slot never idles on an empty label.** Once none of its label's
  work is ready, slot 1 pulls from the rest of the backlog in the order like a
  slot with no preference, and its recorded reason says it fell back. The next
  reliability item admitted is pulled the next time slot 1 is free.

A replay test in `internal/orchestrator` reads the block above out of this
document, loads it as a configuration, and drives the scheduler over it, so the
example is held to doing what these three points say rather than described as
doing it.

A run's record can name the slot it occupies. Where it does, that is the run's
slot and no other: a change of labels, a different start order, a restart, or a
lowered `max_concurrent_developers` never moves it, and a run whose recorded
slot lies beyond the capacity is reported beyond the slots, keeping its number.
A run records its slot only in a project that names an endpoint pair for at
least one slot ([developer slot endpoints](#developer-slot-endpoints)): it
records the slot it was pulled into, or the lowest free one where that was
taken or it was started by name. In every other project, and for every run
recorded before slots were, which slot a run is in is read off what is in flight against what the slots prefer, the same way every
time, by the scheduler and by `yoyo status` alike. A run over labelled work is in a slot that prefers its
label while one is unassigned, and everything else is in a slot with no
preference first and in a preferring slot only once those are full — which is
that slot having fallen back. Labels are read from what each run recorded at
its claim, so a run started by `yoyo run` counts against the slots exactly as a
scheduled one does. Where any slot prefers a label, the running line of
[`yoyo status`](operations.md#where-the-harness-stands-the-four-lines) says which slot each run is in and what that slot
prefers, and names each free slot with its preference under the runs; where
none does, the line reads as it always did. An item the only free slots walked
past for their label — an unlabelled item ranked above the reliability item
slot 1 pulled, with no other slot free — is reported by the pass as **left for
another developer slot** rather than as deferred, naming the slot and what it
pulled ahead of the item: the item waits on nothing about itself, and what
takes it is the next slot with no preference to come free, or slot 1 once its
label's work is exhausted.

### Developer slot endpoints

A developer slot may name an ordered pair of endpoints under
`execution.developer_slots[].routing`: a primary the slot's runs start on, and
one alternate they move to when the primary reaches its usage limit. Developer
runs use it, as [below](#what-a-developer-run-does-with-its-slots-pair); the
reviewer's own alternate is not used by reviews yet, and the four-slot mapping
in the design is a configuration somebody has to apply. Accepting or printing a
pair does not mean a run has used it: the run's own record says what it used.

```yaml
execution:
  max_concurrent_developers: 2
  developer_slots:
    - number: 1
      prefer: [reliability]
      routing:
        enabled: true
        primary: {provider: codex, model: gpt-6.1-sol, account: codex-account}
        alternate: {provider: claude-code, model: opus, account: default}
    - number: 2
      routing:
        enabled: true
        primary: {provider: claude-code, model: opus, account: default}
        alternate: {provider: codex, model: gpt-6.1-sol, account: codex-account}
accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
agents:
  developer:
    instances: 2
```

The list still names slots in order. An optional `number` must equal the entry's
one-based position and fit within `max_concurrent_developers`; duplicates, zero,
negative numbers and reordered numbers are refused. Unlisted slots retain their
existing defaults. `prefer` continues to choose work and does not choose an
endpoint.

An explicit `routing` pair takes precedence over shared developer defaults and
`execution.developer_models` label rules. Without a pair, the first matching
label rule still wins, followed by the shared developer model. Both `primary`
and `alternate` blocks are required. The primary inherits omitted provider,
model, account, model version and effort from the developer agent. An explicit
model clears an inherited version pin; `model_version` may supply its own pin.
The alternate must name a model, inherits provider and account from the resolved
primary, and inherits effort from the developer agent rather than from the
primary's override. It may also name its own `model_version` or `effort`.

`enabled` controls fallback and defaults to false. A disabled pair still selects
its primary, retains its alternate, and validates both. A same-provider alternate
with a different supported model is allowed. Identical resolved endpoints,
unknown providers or accounts, incompatible authentication, unsupported roles
or access, and incompatible model versions or effort are refused. Validation
collects the invalid fields rather than reverting to shared defaults. Capacity
and credential availability are checked at launch, not by this resolver.

The reviewer uses its own `agents.<reviewer>.backend`, `model`, `account`,
`model_version` and `effort`, plus its own `failover` block for the alternate.
It never inherits the developer slot pair or label models. `failover.effort` is
accepted only for developer and reviewer run endpoint resolution. The alternate
retains the existing `enabled`, `provider`, `model` and `account` keys and also accepts
`effort`. For these run endpoint pairs, inherited effort is validated separately
on each endpoint; an explicit `effort: ""` requests that endpoint's model default.
An incompatible inherited level is refused rather than silently dropped, and a
reviewer alternate must support the reviewer's read-only access. Disabled
reviewer alternates are validated too.

`yoyo config show --effective --origins` prints the configured pairs and their
source layer. The run endpoint resolvers additionally return the selected
provider, adapter version, model, account alias, effective effort, field origins
and configuration revision for later persistence. They do not read authentication
files or include the path of any provider's home directory. The configuration reload API loads and
validates a complete replacement before accepting it; rejection returns an error
and preserves the last valid configuration and any previously resolved selection.
The caller serializes reload and records the error. A watching session reads
the configuration again at every pull, so a run it starts pins its pair from
the configuration that pull read, and nothing a later pull or reload reads
changes a pair a run has already pinned.

#### What a developer run does with its slot's pair

Only a slot with a `routing` pair is affected. In a project that names one for
any slot, every run records the slot it occupies; a run in a slot without a
pair records nothing more and runs exactly as it would in a project with none.

- **The pair is pinned when the run starts.** Before the run claims its work
  item, its record takes the slot, both endpoints with the configuration key
  each field came from, whether fallback is on, and the configuration's
  revision. The run starts on the primary. A repair, a reissue after a wait, a
  restart, and a later configuration change all read the pair off that record,
  so editing the slot's pair or its label rules reaches new runs and never one
  already going. A run the scheduler pulls into the slot is refused before it
  claims anything if the primary's provider is not installed or not logged in,
  as a run with no pair is for the developer's.
- **Every invocation is an attempt under a logical operation.** The first
  attempt at the item is one operation, and each repair answering a recorded
  failure is another. A reissue after a wait, a relaunch after a dropped
  connection, and a restart stay in the same operation. Each provider launch is
  a separately recorded attempt, written into the run's record before the
  provider can begin work, so a harness that dies at any point leaves a record
  it can recover from without starting a second provider beside the first.
  After a restart the harness first settles the last attempt: one that never
  began is launched again under its own identity, one that stopped without a
  result is recorded as interrupted, and one that may still be running is
  waited for, for up to two minutes, after which the run stops with the reason,
  its work kept, rather than launching beside it.
- **One switch per operation, for a usage limit only.** When the provider
  refuses the primary for its usage limit, the operation moves to the alternate
  at once, without waiting the limit out, provided `enabled` is true. Where the
  primary is already known to have reached its limit — a refusal recorded
  against that account and model that has not reset and that no served
  invocation has lifted since — the alternate serves the operation's first
  attempt, and no attempt is recorded on the primary. A login nobody has
  renewed, a provider nobody can reach, an overloaded server, a dropped
  connection, a failing check, and a reviewer's finding are answered exactly as
  on a run with no pair, and never move an operation. A same-provider alternate
  on another model switches the same way.
- **The alternate is checked before it is used.** It must still be a provider
  allowed to serve the developer, on an account the configuration declares,
  installed, logged in, and not itself known to be limited. One that fails a
  check leaves the operation on its primary, waiting the limit out as a run
  with no pair does, with the reason recorded on the operation; the switch is
  not spent, and the alternate is checked again before the next attempt.
- **Once moved, the operation stays.** Every reissue, wait, and restart of that
  operation stays on the alternate, and an alternate that is limited too is
  waited out where it is. The next operation — the next repair — starts on the
  primary again with a switch of its own. A switch spends no repair attempt and
  no relaunch, and resets neither; the usage-limit wait budget and the
  relaunch budget cover the operation across both endpoints.
- **A new endpoint starts a new session.** An attempt on the same provider,
  account and model as the run's last one resumes its session. An attempt on
  any other endpoint — the other provider, another account, or another model of
  the same provider — is given a new session and told what the run recorded:
  the run and its branch, the files the change touched, the developer's last
  summary, and the work item's context; the failure a repair answers is in its
  prompt as always. The change itself is in the worktree, committed on the
  run's branch. No session identifier or credential passes from one endpoint
  to another.

The run's record holds the pinned pair, each operation, each attempt's
endpoint, whether its process was confirmed stopped, how it ended, and which of
the run's logged events carry the usage its provider reported; the switch records what
triggered it and the attempt it came from. The model the provider reports
actually serving is in the run's event log and spend log as before. What
`yoyo status` and the dashboard say about a run's endpoint and any switch is
separate work, as is the reviewer's use of its own alternate.

These new keys require a compatible build: `execution.developer_slots[].number`,
`execution.developer_slots[].routing` and its endpoint fields, and
`agents.*.failover.effort`. Restart running parts on that build before adding them
to a project's file. `yoyo config validate` and `yoyo doctor` name running parts
whose recorded schema cannot read the keys.

### A developer model chosen by the item's label

The slot preference above says which work a seat pulls first. This says what
that work costs to do. **Model spend follows the work rather than the role**: a
documentation item and a change to the scheduler are both developer runs, and
only one of them needs the developer's own model. For slots without an explicit
[endpoint pair](#developer-slot-endpoints), `execution.developer_models`
is how a project says so — the tracker's own labels, the ones
[a slot prefers](#a-developer-slot-that-prefers-a-label), mapped to the model a
run over such an item asks for:

```yaml
execution:
  developer_models:
    - label: docs
      model: sonnet
    - label: config
      model: sonnet
    - label: tests
      model: sonnet
```

With that block, a run over an item labelled `docs` asks for `sonnet`, and an
item carrying none of the three labels asks for the developer agent's own
`model` exactly as every run did before the mapping existed. It is the
operator's direction of 2026-09-19, taken off a seven-day reading in which
developer runs on Opus were 64% of $1,431: the largest spend line is developer
runs that do not all need the developer's model, and the label already says
which do. The block is the operator's to paste into the project's own
configuration by hand, because `.yoyodyne/` is a
[protected path](#protected-paths-in-a-developers-change) no run may write, so
a project whose file does not yet carry it runs every item on the developer's
configured model.

**One label per entry, and the order is the answer.** An item can carry two
labels the mapping names, and what it takes is **the first entry in the
mapping's own order** — the file's order, not the item's — so moving an entry
up the list is how a project says which of two labels wins. That is why an
entry names one label rather than a list: an entry preferring several would
make "the first match" a question about which of *that entry's* labels matched
first, which the file does not answer.

**It is read once, when the run starts, and written onto the run.** The labels
it is read against are the ones the item carried when it was pulled, which the
run already records; the model it chose and **why it chose that one** are
recorded beside them. Every developer invocation the run goes on to make — the
first attempt, each repair, and anything a later process resumes — reads the
model back off that record rather than resolving the mapping again, for the
reason [the account](#pooling-work-across-several-accounts) is read back: a run
that resolved it per invocation would move mid-flight the first time the file
was edited under it. The reason is recorded for an unmapped item too, because
an item nothing mapped and a mapping nobody read are two accounts of one model
and only the record tells them apart.

`yoyo status` names the model each running run is on as it always did, and the
[cost log](#provider-accounts) records it per invocation, so what a kind of work
costs is read off the same surfaces as before — a mapped run simply says
`sonnet` where it used to say `opus`.

**The reviewer's model is not reachable from here.** There is no key in this
block that could name it, deliberately: which tools a reviewer may use is a safety
property rather than a spend decision, and an independent verdict bought more
cheaply is the one saving that costs the gate its meaning. The
[account pool and the failover rules](#serving-a-turn-from-a-permitted-alternate-model)
apply to a mapped run unchanged, and so does everything else — a run on a
mapped model is claimed, developed, checked, reviewed, and promoted exactly as
any run is, under the same contract and the same authority table. Configuration
selects the model and never widens what a role may do.

**What the file refuses.** A label the tracker would not carry — anything but
one identifier-shaped word, the same rule a slot's preference is held to. A
`model` that cannot name a model, held to the rule every other configured
selector is. And a label mapped twice, because the first match in the order is
what an item takes, so a second entry for one label is a mapping the operator
believes is active and that nothing will ever reach. All three are refused when
the configuration loads, before anything is claimed.

### Watching instead of draining

`yoyo work` returns when nothing more is ready. `yoyo work --watch` does not: it
waits out an interval and reads the queue again, until you stop it.

```yaml
execution:
  work_poll: 60s                       # the default
  blocked_runs_before_intake_hold: 3   # the default
  brake_cooldown: 30m                  # the default
  brake_escalation_cycles: 4           # the default: two hours at that cooldown
  redeploy_drain_limit: 15m            # the default
```

Nothing else about the pass changes, and nothing needed to. Every pull re-reads
the configuration and the intake hold, takes the queue in the order you set, and
records why it chose what it chose — so work you admit is picked up at the next
poll, a reprioritization at the next pull, and an item whose dependency landed
becomes pullable because the tracker says so. There is no change detection in it:
nothing between the readings is cached, and a run already in flight is never
preempted by any of it.

An idle session costs one local tracker read per `work_poll` and asks no provider
anything, so a queue that is empty overnight spends nothing — unless it has a
stopped run to [deliver](work.md#letting-the-harness-choose-the-work), or a
[recurring task](#recurring-tasks) that has come due. Each of those is a turn and
is charged as one, so a project with an hourly task and an empty queue spends a
turn an hour rather than nothing.

**The intake hold is the remote brake.** It does not stop a watching session; it
brakes it in place — the session keeps polling, chooses nothing, and resumes
where it was when you release it. `yoyo pause`, the wider switch, parks the runs
too, and lifting it resumes them from their own records.

**Holding intake does not stop the spend above, and that is the distinction to
have in mind.** The hold stops the session *choosing work*, and the two things
that spend without choosing any are read before it: a stopped run reaches the
development manager and a due recurring task fires under a held intake exactly as
they do under a clear one. That is deliberate — a held queue is usually waiting on
one of those judgements, and withholding them would be the hold answering a
question nobody asked it — but it means an operator who holds intake to stop
spending is still charged a turn per cadence. `yoyo pause` is the switch that
stops those too.

**Four guards, because the loop no longer ends.**

**A watching session does not start the same item twice unless the item has
changed.** The case that forces this is a run that fails *before it starts* —
unreadable acceptance criteria, a context bundle that will not assemble. Nothing
is claimed and nothing is recorded, so the item is left exactly as ready as it
was: a drain tries it once and returns, and a watch with no memory would retry it
every interval forever. A provider that is not authenticated used to be one of
these and is not any more: that dispatch is
[a wait](operations.md#waiting-out-a-provider-nobody-can-reach) the session
holds the item through rather than an attempt it remembers, so the item is
started when the login is renewed.

The rule covers every item the session has started, not only the ones that failed
that way, because the other cases that leave an item pullable with nothing
recorded — a run the intake hold or your `yoyo pause` stopped before it claimed
anything — would spin the same way. What lifts it is the item changing: what the
work says, what it is for, its priority, its status, what it depends on, and its
notes. The notes make the ordinary recovery work: a run that stops on a blocker
takes the item out of the ready queue and writes the blocker into its notes, so
when you release that item without editing anything else, the session sees an
item it has not tried and pulls it. Nothing the harness writes can clear the
cooldown of an item that stayed pullable, because it only appends to the notes of
an item it has claimed, blocked, or closed.

An item this session has already run and nothing has touched since is left alone
for the life of the session. Restarting the session, or touching the item, asks
for another attempt — and the restart it makes for itself when you deploy counts,
which is usually what you want, since a build you just installed is the likeliest
reason the attempt would go differently.

A start the environment refuses before reserving a run is eligible again after
one poll interval, without any change to the item. Uncommitted changes in the
primary checkout, a lost race for capacity, and failures to read repository
readiness, durable state, or architectural invariants leave no memory that the
work was tried and count nothing toward `blocked_runs_before_intake_hold`.
The refusing step records that the cause is outside the work. A sandbox that
will not spawn a shell is covered when that step marks its refusal that way.
A readiness read also covers an unmarked failed start while the checkout is
still refusing work.

The primary checkout's readiness is read at every pull before new work is chosen.
A watching session waits and reads again; a drain stops on the refusal. The watch
log, `yoyo status`, and the Slack heartbeat carry the cause, including
`runs cannot start: uncommitted changes in the primary checkout (<file>); commit or stash to release`.
Once you commit or stash, the next poll can start the queued work without a
session restart or an edit to the item. Other refusals carry the condition and
the beginning of the cause separately, so bounding a long cause does not spend
the detail limit repeating the condition.

`blocked_runs_before_intake_hold` is the failure-storm brake, a different thing
from that cooldown: it is aimed at a broken machine rather than a broken item.
That many runs blocking one after another, with nothing landing between them,
holds intake — the same hold you would place — and the same poll summons the
development manager's [sweep](#recurring-tasks) out of its cadence, with the
runs that blocked and the reason each blocked in the message that wakes her.
Any run that lands clears the count, and `0` turns the brake off, leaving you as
the only thing that holds intake. Only verdicts and check failures on a change
that was present count: a stop the environment made — a dirty checkout, a
tracker or a forge that did not answer, a sandbox that would not spawn, a
target branch that has
[diverged from the forge](operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
so the harness will not catch it up — is a verdict on nothing and counts
toward nothing, and neither does a dispatch or a run the provider turned away
because nobody is logged into it or nobody can reach it. That is
[a wait](operations.md#waiting-out-a-provider-nobody-can-reach) no run can end,
and a brake tripped on it prescribes a decision about a change nobody judged —
which is what happened on 2026-09-17 over an expired login, again on
2026-09-19 when two of the three stops that tripped it came from outside the
work, and
again on 2026-09-21 when all three were one diverged target.

**The brake's hold does not wait on you while the harness is still working
it.** She decides what happens to it — to
release it, to keep it and probe the line, or to escalate it to you — and the
watching session acts on the decision at its next poll. `brake_cooldown` is how
long the brake waits for that decision before it decides on evidence instead:
once it has passed with nothing recorded, the session starts one probe run under
the hold, and the probe landing reopens intake while the probe blocking keeps it
held, restarts the cooldown, and summons her again with the probe's own
stoppage beside the three. So a broken machine is probed once per cooldown and
put to her each time, and a machine that was fine is choosing again within a
cooldown of the trip whether or not anybody answered. Thirty minutes is the
default: a summoned turn is minutes, so that is several answers' worth of
slack, and a summons the provider refused costs the line half an hour rather
than the two hours the 2026-09-19 trip cost it. Zero waits for her summoned
turn and no longer.

**And the loop that makes has a bound.** On a machine that stays broken, each
blocked probe summons her again and restarts the cooldown, so the brake goes
round — one of her turns and one probe run per cooldown — and before the bound
nothing about it got louder unless she escalated it. `brake_escalation_cycles`
is how many of those summons-and-probe cycles the harness goes round before it
escalates the hold to you itself: the cycle that reaches it is not put to her
again, no further probe starts, and you are sent
[one direct message](reporting.md#a-brake-hold-the-harness-escalates), tagged
by member id, naming the cycles spent and what stopped the last probe. It is a
count of cycles rather than a length of time because the loop is what it
bounds; what it comes to in hours is the cooldown times it, and the default of
four is two hours at the default cooldown — the same bar the heartbeat raises a
stopped line to critical at. Every summons names which cycle it is and at what
cycle the harness stops asking, so she can escalate sooner herself. A hold the
harness escalated is still hers to release if the line turns out to be fine; a
probe decision on it is refused, because the bound ended the loop. Zero never
escalates on its own, which is the loop as it stood before the bound existed.

So a brake hold waits on a person only once it is escalated, by her or by the
harness at that bound, and the hold is sent to you as escalated once, directly
and tagged by member id, the moment the first escalation is recorded — one
message for the hold whichever of the two escalated it, and none more if the
other follows. It is
[a finding for you](operations.md#where-a-finding-that-needs-your-hand-goes).
The trip itself is sent to you directly too, once and tagged, the moment it is
recorded, naming the runs it counted and `yoyo release`: it is not yours to
move while the harness is working it, and the message says whose it is, but it
is the one hold you did not place. `yoyo release` and the conversation's
`/release` still lift any of them sooner.

The hold records which of you placed it, and everything that reports one says
so: "the harness's own brake placed it after 3 run(s) blocked in a row with
nothing landing between them, which is the configured brake at 3" rather than a
hold attributed to you — and, for the brake's, what is deciding it and when the
probe starts if nobody does. It matters because what you do about a stopped
line depends entirely on which of the two stopped it, and a brake that trips
over a hold you already placed leaves yours standing, still yours, and summons
nobody over it. The brake's hold also names the runs it counted — each with its
item and what stopped it — on the channel's message and under `Needs a human`
on `yoyo status`, and a release is said once, naming who lifted it.

And the session says what it is doing, because an idle session and a dead one are
otherwise the same silence. Each transition — watching, idle, braked, blocked, resumed,
stopped — is recorded once, where `yoyo status` prints it and the Slack sink
posts it. A session idling all night writes one line rather than one a minute. A
stop says whether it is an ending or a restart, so the one below reads as a
session coming back rather than a line waiting for you to start another.

**Beyond the three: a reading of the harness that fails does not end the
session.** The tracker is a database a reconcile and every settling run write to,
so a reading that fails is contention far more often than a store that is broken
— and a session that exited on one left the queue idle until an external job
noticed. A watching session waits and reads again, two seconds doubling to
thirty, and stops only once the readings have gone on failing for five minutes,
saying how long it tried. None of it is configured: the numbers are the harness's
and the same for every product. A drain still stops on the first one, and so does
either kind of pass on a pull that assembles and is unusable — a capacity of
zero, or a `--budget` with nothing to price it — because that is a decision about
the configuration rather than a reading that failed.

**Beyond the three: a watching session takes up a build deployed over it.** When
the `yoyo` it is running is written over — you rebuild it, you install it — the
session drains: it restarts into what you deployed the moment it hosts no run
and no recurring pass is taking its turns — past the bound, a run it has
stopped is not one it hosts — and until then it goes on polling, pulling into
free seats, and, while it still hosts a run, firing its recurring tasks,
because the drain is about the runs it hosts and not about the scheduler's
other duties. Once it hosts no run it starts no new pass, since a pass begun
then would only hold the restart; the session that comes back takes it at its
first pull. A pass already taking its turns is waited out like a run until the
bound below; when the session restarts past the bound, every pass still taking
its turns is stopped and recorded as a missed pass. A deploy is the whole of
the instruction; what is configured is only how long the wait may last:

```yaml
execution:
  redeploy_drain_limit: 15m            # the default
```

Past that bound the session restarts anyway. Each run it still hosts at its
developer attempt, its checks, or its review is stopped where it is and preserved whole:
worktree, branch, claim, developer session, and every counter. The session that
comes back re-adopts each one at its first pull, ahead of anything new, and
continues it from the phase it was stopped at — the session that stopped it
never does, however long it goes on pulling. A run at its promotion is waited
out past the bound rather than interrupted, because it holds the target
branch's lease. A running check stage is stopped at the drain bound, however
far the machine's load has scaled
[`check_stage_timeout`](#what-a-whole-check-stage-may-cost); the session that
comes back runs the checks again from the start. A stage that has already
finished gets up to a minute's grace to record its verdict and move the run
on to its review or a repair, where the next look stops it. A run that has
already landed and is running its
[landing checks](#where-the-whole-suite-runs) is stopped too: nothing about the
run is at stake by then, and the landing is recorded as unverified and files
nothing. The bound is minutes rather than hours on purpose: on 2026-09-19 a
session waited two hours on one run's race suite under load, with its second
seat empty and two recurring passes missed — a wait the restart drain limit
caps at fifteen minutes by default, independently of load. What a run the bound stops loses is the
minutes its current phase had spent, and a developer attempt and a review
resume where they were. Set it longer if that trade is wrong for your project, and never to
nothing — a drain with no bound is the wait this exists to end, and the
configuration refuses it. [Operations](operations.md#a-session-draining-to-restart-into-a-deployed-build)
says what a drain does and does not stop.

What a deploy costs is one restart per deploy, and the queue is re-read from
scratch on the way back in exactly as it is at every poll.

The bounds cross the restart reduced to what is left of them — `--budget` less
what the session has spent, `--limit` less what it has started — because a bound
carried whole would start again at every deploy. A session that has reached
either one stops on the bound instead of restarting: you set that number, and
taking up a build is not you raising it. There is nothing here for a drain, which
is a command you are waiting on the return of.

**`--budget <usd>`** caps what one session spends, and everything it spends
counts against it: the runs it starts, priced from the same recorded run evidence
`yoyo cost` prices items from; the turns it takes delivering stopped work; and
the turns a [recurring task](#recurring-tasks) takes when its cadence comes due.
The last two are turns the session takes rather than runs it started, and they
are counted for exactly that reason — a bound that quietly excluded what a quiet
session spends would be the cap disappearing on the nights it matters most. It is
checked between pulls, never part way: money already spent is spent, and what
stopping would lose is the work it bought.

A budget the harness cannot measure is no budget, so it fails closed at both
ends. A pass given `--budget` with no way to price itself is refused before
anything starts. A session that has started and then meets a run whose recorded
evidence will not price — the event log gone, or a record it cannot read — stops
there and says which run it was, rather than counting it as free and carrying on
inside a bound it can no longer hold. The stop is announced like every other
transition, so you find out while it matters rather than in the morning.

**The default is still the drain**, and `--until-drained` says so explicitly.
That is deliberate: watching is the shape this loop is meant to have, and turning
it on by default is a decision to make once stopped work reliably reaches
somebody, rather than a side effect of the flag existing.

What changes when you watch is what bounds the spend. A drain is bounded by the
queue emptying; a watching session is bounded by what you admit to the queue. The
backlog's order stops being a schedule and becomes the throttle.

### When a configuration change takes effect

**At the next selection.** `yoyo work` re-reads the configuration before every
pull, not once when it starts, so a capacity you raise or a priority you reorder
while it is running is picked up the next time it chooses something. That is the
same answer every other command gives — each one loads the configuration fresh —
and it is what makes reordering the backlog steer the work rather than steering
the work after a restart.

A run already in flight keeps the configuration its own pull read. Its capacity,
its check budget, and its repair budget were fixed when it was reserved, and
changing them under a running developer would mean a run judged by rules it was
never started under.

A watching session is the same answer said again: `work_poll`,
`blocked_runs_before_intake_hold`, `brake_cooldown`,
`brake_escalation_cycles`, and `redeploy_drain_limit` are re-read at every pull
too, so an interval you shorten, a brake you loosen, or a drain bound you
lengthen under a draining session takes effect at the next wait rather than at
the next restart, and a bound you tighten under a standing loop is heard at the
next probe.

### Why each run says why it was there

Every run `yoyo work` starts records, in durable state, why that item was chosen:
where it sat in the order, how much of the queue was pullable, how much of the
machine was free, and anything upstream of it that had changed since it was
admitted. `yoyo status` and a conversation's survey both read it back.

This is not bookkeeping. Work the harness chose and cannot account for looks
exactly like work happening behind your back, and holding intake — which stops
`yoyo work` choosing anything more while what is running finishes — is worth
having only if the thing that chooses actually consults it. Both halves are
enforced rather than conventional: an item you name yourself is exempt from the
hold, because naming it is you deciding it is the exception.

## Running a work item against the workflow definition

The delivery loop's sequence is also written down as data, in the two built-in
workflow definitions this build ships — `delivery.yaml` for a project whose
integration the harness takes, and `delivery-human-approval.yaml` for one where a
person still approves it. **Every new run compiles the one its integration policy
binds and executes it beside the run**, which is the default and takes no
configuration at all. A project that wants its own sequence
[writes one and owns it whole](#the-definition-is-the-projects-to-own).

What "executes" means here is exact, and it is worth being plain about: the
definition resolves where the run goes next and records it. It does not perform
anything. What runs a work item is the same Go control flow it always was — the
run claims the item, invokes the developer, runs the checks, buys the verdict and
takes the promotion lease exactly as it always has — and the conversion that
moves the performing too is a later step with its own configuration.

A new run records a **workflow instance** beside its own record, standing on the
definition's first state, and every boundary the run crosses is put to the
definition: the state the run just performed, the outcome it produced, and the
transition the definition resolves from them. The doors the definition holds are
the registered delivery steps with their bodies replaced by nothing at all, so
delivery is untouched and what the instance costs is one small file write per
boundary.

### The definition is the project's to own

The built-in is the default and not the only option. A project that wants its own
sequence writes it beside its personas, under the same configuration directory,
named for the workflow it replaces:

```
.yoyodyne/workflows/delivery.yaml                  # automatic integration
.yoyodyne/workflows/delivery-human-approval.yaml   # a person still approves
```

Which of the two a run reads is the integration policy above, exactly as it is
for the built-ins: a project whose `approvals.integration` is `automatic` binds
`delivery`, and one where it is not binds `delivery-human-approval`. A project
that keeps neither file runs what this build ships, which is what every project
did before this existed and still takes no configuration at all.

**Nothing is merged between the two.** A project that writes one owns the whole
sequence from then on — the states, the transitions, the terminals — which is the
only arrangement where reading the file tells you what its runs execute. The
other side of that is the cost: a later Yoyodyne that improves the built-in does
not reach a project that ejected a copy, so the copy is worth a header saying
where it came from. Yoyodyne's own repository keeps one — `.yoyodyne/workflows/delivery.yaml`
here is the built-in verbatim, adopted so that this project runs the arrangement
it ships, and a test holds the two to the same content digest so that editing one
and not the other fails rather than passing quietly.

**What a copy can change is the sequence and nothing else.** It selects among the
actions this build registered — `work-item.claim`, `candidate.develop`,
`candidate.check`, `candidate.review`, `candidate.integrate`, `run.complete` and
`run.clean-up` — and each action's authority is declared in Go, so no file can
make a run do anything the built-in could not. A state selecting an action
nothing registers, a transition to a destination that does not exist, an outcome
the step it selected never produces, a file answering to another workflow's name,
or a key the schema does not describe: each is refused, all of them are reported
together, and the file is refused whole.

The gate is not among the things a file can rearrange away. A definition that can
reach the promotion without a state that runs the checks and a state that buys an
independent verdict between the last write of the change and the promotion is
refused at compile, whatever order it puts its states in. Configuration selects
the sequence; it cannot make a guarantee optional.

**A copy that is wrong stops the run before it claims anything.** The refusal
names the file and the defect, and nothing falls back to the built-in — a run
that quietly executed a sequence nobody chose, under a name the project had
already used for something else, is the failure this location exists to prevent.
Refusing it costs nothing: no work item has been claimed, no worktree exists, and
no provider has been paid.

A run already in flight is treated differently, because by then the work is under
way and what is broken is only the watching. Such a run finishes exactly as it
would have and records a `workflow_divergence` naming the file, which is the same
thing it records when a definition is edited under an instance already running
it: an instance keeps the digest it pinned and is never migrated, so an edit — a
correct one included — stops the observation of the runs already going and
reaches the next one.

**The rollback is one key.** A project that wants the legacy path — the same
delivery with nothing observing it — writes:

```yaml
execution:
  declarative_delivery: false   # the rollback; the default is true
```

That is the whole of it. It reaches new runs only: everything already in flight
finishes on whatever it started on, in both directions, which is the section
below. `yoyo config show --effective` prints the value that applies and
`--origins` names the file it came from, so a rollback is something you can
confirm rather than assume.

Three fields on the run say what happened:

- `workflow_instance_id` names the instance, and a run carrying one is a run
  executing the definition. It is written when the run is created and never
  afterwards.
- `workflow_unobserved` is why a run has none although its project asked for
  one: the definition could not be built, or the instance could not be created.
  The run delivers as it would have and what is lost is the watching. Without
  it, such a run reads like a rolled-back one and is counted as one the
  definition agreed with.
- `workflow_divergence` is why the run stopped being observed: the definition
  sent it somewhere it did not go, refused an outcome it produced, could not be
  stepped at all, or had no outcome for the way the run ended. A run carrying one
  is a run to read before the definition is trusted with anything, which is still
  ahead of it.

  That last case is what keeps the record honest. A run can end by a route no
  definition expresses — a review that ended without a verdict and without the
  operator's stop, a `complete` that failed, a worktree that could not be cut
  before the first attempt — and none of those is observed, deliberately, because
  naming the nearest outcome would record the run ending somewhere it did not. So
  the instance is left standing where the two last agreed, and a run that reaches
  a terminal status with its instance still mid-graph records the gap itself as
  the divergence, naming the state it stopped in.

  That holds however the run reaches its terminal. A run its own process ends
  records it there; a run whose process died and is settled by `yoyo reconcile`
  has the same gap recorded by the sweep, in the same words, whether the
  settlement completes it, blocks it, or fails it. The completed case is the one
  worth naming: the work lands and the item is settled on it, so a run whose observation
  stopped halfway would otherwise read exactly like one that walked the
  definition to the end.

  **What is recorded is the gap, not the settlement.** A process that died can
  still have left its instance on a terminal — an ending the definition has an
  outcome for is stepped before the process stops writing — and such a run is
  settled carrying no divergence, on every one of those three settlements. That
  is the recorded baseline's blocked trace:
  `reconciliation-blocks-a-run-interrupted-while-developing` stops writing as the
  developer's attempt ends, the developer's ending sends its instance to
  `abandoned`, and the sweep blocks the run with nothing to record. An empty
  divergence there is the observation having finished rather than the sweep
  having missed it, which is why it is measured rather than left to be read off
  an absent field.

All three are on the run's summary, so `yoyo status <beads-id>` and its `--json`
carry them like every other fact about a run, and a divergence and an unobserved
run each get a line there. Neither is a reason a run ended: the run delivered
exactly as it would have, and what diverged or went unwatched is the observation.

Three divergences are already known and expected. The first two are
interrupted processes rather than anything about the work. A run interrupted while its
reviewer was being asked resumes at the checks rather than at the review, because
a resumed run re-earns the whole gate, and no definition has a transition from
the review back to the check; such a run records a divergence naming both. A
process killed inside integration is settled by the sweep as succeeded with its
instance still standing in `integrate`, and records the gap that leaves. (That
is a local promotion; a process killed while landing through a pull request is
settled on the forge's answer instead, as
[a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request)
says.) Both are
left as divergences deliberately — the definition is missing a path the pipeline
takes, and an observation that quietly agreed with itself would be worth nothing.
The third is a replay conflict handed back to its developer: `integrate` answers
`reconciling`, and neither the built-in definition nor this repository's own copy
routes that outcome back to `develop` yet, so such a run records the refusal as
its divergence. Adding the transition means changing both copies together,
because a test holds them to one digest; that follow-up is admitted as
yoyodyne-ifd.209.31.

**The default and the rollback both reach new runs only.** Whether a run is
observed is settled once, when the run is reserved, and read back off the run's
own record by every later process. So a run already in flight when you roll back
keeps being observed to its terminal, and a run started under a rollback you have
since undone carries on to its own terminal with no instance and nothing watching
it. There is no migration in either direction, which is the same rule an
in-flight instance is held to when the definition itself changes: it keeps
running the definition it was pinned to, or it stops being stepped and says so.

That is the price of the rollback, and it is worth stating plainly: writing
`declarative_delivery: false` does not stop the runs that are already going. It
decides what the next one does.

## Publishing through pull requests

By default Yoyodyne is entirely local: it creates a branch and a worktree, runs
the work, and fast-forwards your target branch. Nothing is pushed, and a
repository with no remote never notices publishing exists.

A project opts in the way it opts in to automatic integration. **Both settings
matter**: publishing opens the pull request, and integration is what merges it.

```yaml
approvals:
  publishing: automatic
  integration: automatic   # required for the harness to merge what it opened

execution:
  remote: origin   # the default; name another remote if yours is not origin
```

If you cannot push to that remote — you are contributing to somebody else's
repository — name your fork as well:

```yaml
execution:
  remote: upstream     # where pull requests are opened; you need no push access
  push_remote: fork    # where run branches go; the one you can push to
```

With both on, a run works like this:

1. **The developer phase publishes.** When a developer attempt finishes, the
   harness commits its work under its own identity, pushes the run branch to
   `execution.remote` — or to your fork, if you named one; see [Publishing from
   a fork](#publishing-from-a-fork) — and opens a pull request against the
   target branch on `execution.remote`. Each repair attempt pushes
   onto the same branch and updates the same pull request, so one change never
   ends up with two places to be reviewed. This happens *before* the checks run:
   a pull request is where work is reviewed, and work that does not pass yet is
   exactly what a reviewer should be able to see.
2. **The reviewer's verdict merges it.** An approving verdict authorizes the
   merge, and the harness asks the forge to perform it — it never pushes your
   target branch. Nothing about the gate changes: the same passing checks, the
   same independent-reviewer evidence, and the same fast-forward rule that gate
   integration also gate the merge, and the remote target is checked again right
   before the call, so a target that moved in the meantime refuses the merge
   rather than having the forge reconcile it. That holds of a merge
   [reissued after a dropped connection](#waiting-out-a-network-that-dropped)
   too: the check is made again on each attempt rather than once in front of
   them, so what authorizes a merge made after a wait is a reading of the target
   taken after that wait.
   The merge is asked for as of *when your branch protection is satisfied*
   rather than as of now, so required checks that are still running are waited
   for by the forge instead of refused seconds after the approval. Administrator
   override is never used to get past them. Waiting that way needs **"Allow
   auto-merge"** enabled in your repository settings, which is off by default;
   when it is off and nothing is holding the pull request back, the harness
   simply merges, so a repository without branch protection needs no setting
   changed at all. Only the combination of the two — something holding the
   request back and no way to queue the merge behind it — cannot be published
   to, and the run says exactly that and names the setting rather than
   reporting a merge that mysteriously fails.
3. **The merge method is a merge commit.** The harness names it rather than
   taking your repository's default, because it is the only method that puts the
   reviewed commit itself on your target branch. A squash replaces it with a
   commit nobody reviewed, and GitHub's rebase always rewrites what it merges —
   new committer, new SHA, even when the request needs no rebasing — so both
   would leave the remote carrying a copy of the work your local branch does not
   have. The method is recorded on the run and on the work item, along with the
   commit the merge produced.
4. **The merge is confirmed, then the branch is cleaned up** on both sides,
   locally and on the remote, on the same compare-and-swap evidence. The
   confirmation waits briefly and boundedly, because a forge's own record of a
   request can lag the merge it just performed. If the forge refuses outright —
   a request that conflicts with its base, a merge method the repository
   forbids — the run reports which requirement was unmet rather than a generic
   failure.
5. **A merge the forge queued ends the run rather than being waited for.** It
   lands minutes later, when your checks pass. The run reports the pull request
   as queued and finishes: your change is already in the local target branch,
   which is the authoritative one (on a
   [protected target](#a-protected-target-lands-through-its-pull-request) it is
   not, and nothing local moves until the forge merges), and the run branch stays on the remote
   because that is what the forge still has to merge. On a protected target the
   run's local branch and worktree stay too, until the forge's merge is
   confirmed, because nothing proves the change is on the target before then.
   A request the forge's merge queue has taken is read as a merge the forge
   still holds, not as one it dropped. The work item is the one
   thing the run does **not** settle — it stays open, with the queued merge named
   on it, because closing it as integrated would record a publication that has
   not happened and may not. `yoyo reconcile` settles both afterwards: it asks
   the forge, and either finishes the publication and settles the item — closed,
   or put back in the backlog parked or waiting on a named impediment where the
   run's own records say its change does not discharge it, its landing claim or
   what its reviewer approved (merge
   commit recorded and your local target branch caught up
   onto the forge's merge commit, with the branch the merge consumed deleted
   afterwards as hygiene that cannot hold the closure up) — or, if the forge dropped the queued merge
   because something it required went unmet, records an outstanding publication
   and hands the item back to you with a blocker rather than closing it. A drop
   on a protected target whose head fell behind, failing nothing the change
   touches, is not handed back: the change is brought up to date from the kept
   branch, checked and reviewed again, and queued again, as a queued head behind
   its target is. It never
   merges anything itself: a requirement that stopped the forge is yours to
   satisfy, and re-arming a dropped merge is a bounded triage decision — one per
   publication, carried out by `yoyo triage rearm` — rather than something a sweep
   does.

`gh` is invoked by the harness and never by a developer or reviewer: no role is
given a publishing credential, tool, or request to push or merge. For the reviewer that
is enforced by its adapter: Claude Code refuses every tool, while Codex permits
read-only inspection with tool network access and external integrations disabled.
The reviewer returns a verdict; the harness performs publication and merging.

For the developer it is not. A developer has a shell in its worktree and runs
under your account, so it could in principle reach a `gh` you have
authenticated; what stands in the way is its backend's sandbox and the harness
contract in its prompt, not a boundary the harness enforces. What does hold is
that your local target branch is authoritative: work an agent pushed by itself
is not integrated by having been pushed, and a pull request merged behind the
harness's back moves the remote away from the local branch, which the harness's
own check of the remote target then refuses rather than force-resolves.

### Publishing from a fork

Everything above assumes you can push to the remote you publish into. When you
cannot — the ordinary situation for a contributor to somebody else's
repository — `execution.push_remote` names the remote your run branches go to
instead, and the pull request is opened across the two repositories:

```yaml
execution:
  remote: upstream
  push_remote: fork
```

Both remotes have to exist in your checkout. Add the fork the way you would add
any remote, and check both with `yoyo doctor`, which names whichever one is
missing:

```bash
git remote add fork git@github.com:you/theirproject.git
yoyo doctor
```

Nothing else about publishing changes, and nothing about it is a second mode.
Run branches are pushed to the fork, the pull request is opened against the
remote you publish into with the head qualified by your account, and every
question about the target branch — whether it may still be merged into, where
the forge's merge left it, catching your local branch up afterwards — is asked
of the repository the work is going into. The merged run branch is deleted from
the fork, because that is the only place it ever was.

Your fork's account is read from the fork remote's URL rather than configured
separately, so there is nothing to keep in step with it. A repository that does
not have the remote you named publishes nothing and says which remote it was
looking for, the same way a repository with no remote at all does.

### Publishing without automatic integration

`approvals.publishing: automatic` with `approvals.integration: human` is
supported and does exactly half of the above: the harness pushes and opens the
pull request, and then stops. **It merges nothing.** You get an open pull
request, a run branch that stays on the remote, and a preserved worktree; you
merge, and the harness never touches any of the three afterwards.

That is deliberate rather than a gap. Merging is a promotion, promotion is what
`approvals.integration` governs, and a harness that merged under a `human`
integration policy would be taking the decision that setting reserves for you.

| `publishing` | `integration` | What you get |
| --- | --- | --- |
| `human` | `human` | Local branch and worktree, preserved for you. |
| `human` | `automatic` | Local fast-forward into the target branch, artifacts removed. Nothing pushed. |
| `automatic` | `automatic` | Pull request opened, merged on approval — or queued with the forge until your required checks pass — and the branch removed locally, then on the remote once the merge has happened. |
| `automatic` | `human` | Pull request opened and left for you. Nothing merged, nothing cleaned up. |

### Which branch is authoritative

**The local target branch.** Your work is where that branch says it is. The
exception is a target branch the forge protects, where the forge's copy leads and
the local one follows it: see
[a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request).

Merging is not a second promotion performed on the remote. The harness
fast-forwards the local target exactly as it always has, and the forge merges
the pull request carrying exactly that commit. One promotion, one reviewed
commit, the same commit on both sides.

The merge itself does not leave the two at the same commit, and no forge merge
method would: **the merge leaves the remote target at your local target plus one
merge commit**, made by the forge and identical in content. The last step of the
promotion is to catch your local branch up onto the remote, which is an ordinary
fast-forward onto a commit that already contains the promotion. Nothing is
rewritten, reset, or merged, and nothing is decided: that is the `git pull` you
used to run yourself.

A catch-up the harness cannot make cleanly is held rather than forced, and says
why:

- **Uncommitted work in your checkout that the incoming commits would
  overwrite.** The branch is left where it is and the file is named. The
  exception is the work tracker's own exports — `.beads/issues.jsonl` and
  `.beads/interactions.jsonl`, the same two a run is allowed to rewrite in your
  checkout while it works. They are derived from a store that is authoritative
  elsewhere, so their churn is discarded and the catch-up goes through.
- **A remote that has diverged from your local branch** — a history somebody
  rewrote, or work that reached the remote another way. Which of the two is
  right is your answer rather than the harness's, so it is reported and nothing
  moves. A promotion refused on one records it against the product, and the line
  [chooses no work](operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
  until a `yoyo reconcile` finds the branches converged.

A merge that landed after its run had finished, and any catch-up that was held,
are swept by `yoyo reconcile`, which also removes the leftover local branches of
settled runs whose work the target already carries. Catching a branch up takes
that branch's promotion lease, so it never races a run promoting into it.

Because the forge performs the merge, the harness checks that relationship
rather than assuming it. Before the merge, the remote target must contain the
commit your promotion was made from and carry exactly its content — that is what
tells a target another run already published into from someone else's work.
After the merge, it must contain the promoted commit itself, unrewritten. It
need not carry exactly its content: a merge that lands among others — ten held
requests merged in one sitting — leaves every promotion but the last under a
merge commit later merges have built on, and requiring equality there reported
nine confirmable publications as unconfirmable for good. What is recorded as the
merge commit is the one the forge names for the pull request, where that commit
is on the remote target with the promoted commit as a parent, or otherwise the
one found in the remote history with the promoted commit as a parent; the
forge's record never decides the confirmation, only what is recorded. A forge that rewrote the
commit is reported, not reconciled, and the run branch is left on the remote for
whoever decides which history is right.

If a promotion onto an unprotected target cannot be published — the forge is
unreachable, the remote target moved, or the forge refused the merge — the run
still succeeds and closes its item, and reports an *outstanding publication*. A forge that could not be reached
is [waited out and asked again](#waiting-out-a-network-that-dropped) first, so
an outstanding publication over a dropped connection is one that went on being
dropped rather than one reset the next attempt would have survived. The change is integrated where
it counts; only its publication is unfinished, and it is reconciled by hand.
Nothing is ever force-pushed to resolve it. On a protected target the same
failures leave nothing integrated, so the run stops rather than closing the item;
the next section says how.

### A protected target lands through its pull request

Before it promotes, a publishing run asks the forge whether the target branch is
protected, both ways GitHub protects one: per-branch protection, and a ruleset
carrying a pull-request, required-status-check, or update rule. It is the same
question `make release` asks before a cut. The answer decides the order of the
two halves above.

**An unprotected target** is promoted exactly as described above: the local
target is fast-forwarded onto the reviewed commit, and the forge merges the pull
request carrying it.

**A protected target is never moved locally ahead of the forge.** The run
commits the change, checks that the local target still stands where the change
was written against, and asks the forge to merge, with the local target left
where it was. The reviewed commit reaches the remote only through the forge's
merge, and the local target follows by the catch-up above: a fast-forward onto
the remote, taken under the branch's promotion lease. So no ending of the run
leaves your local target ahead of the remote:

- **Merged.** The catch-up brings the local target onto the forge's merge
  commit, and the item closes.
- **Queued.** Nothing moves locally. The worktree and branch are kept, because
  nothing proves the change is on the target yet; `yoyo reconcile` catches the
  local target up once the forge merges, closes the item, and cleans up.
- **Refused or dropped**: a required review, a failing check, anything the
  forge would not merge. The change is on its pull request and on no target
  branch, so the item is not closed. The run stops and hands the item back with
  the forge's answer as the blocker, keeping the record `yoyo triage rearm`
  repeats the merge from once the requirement is met.
- **The remote target moved** after the landing was prepared. Nothing was
  promoted, so this is a lost race rather than a divergence: the local target
  is fast-forwarded onto the remote and the change is replayed onto it, with
  the checks and the review re-earned, under the same
  [retry budget](#losing-a-race-for-the-target-branch).
- **The process was killed** after the landing was prepared and before the
  forge's answer was heard. The run recorded the landing before asking, and
  `yoyo reconcile` settles it on what the forge says rather than on the local
  target, which says nothing here: merged is confirmed, caught up onto, closed,
  and cleaned up; queued is recorded as a queued landing and settled like any
  other; anything else hands the item to a person, as the run itself would have.

**Why.** On a protected target, promoting locally first strands commits. A
merge the forge refuses or holds leaves the local target ahead of the remote,
and every later run that has to bring the target onto the remote collides with
it. That happened on 2026-09-20 and again on 2026-09-24, and both times every
run stalled until the checkout was reset by hand.

**A forge that cannot be asked is treated as protecting the branch**, and the
run says so on the work item. The protected path costs an unprotected target
nothing it needs, since the change still lands by the merge, while the other
reading could move a branch the forge then refuses. Every publishing run
records which path it took, and why, as the `Target branch:` line in its notes
on the work item.

### What publishing needs

- A remote by the configured name. **Without one the run is purely local**,
  reports `publishing skipped`, and behaves exactly as it did before publishing
  existed. That is a property of the repository, not an error.
- The GitHub CLI, installed and authenticated (`gh auth login`). If a project
  asked to publish and `gh` is missing or logged out, the run **fails before it
  claims anything** — a harness that quietly stopped publishing would look the
  same as one with nothing to publish.
- Permission to merge the pull request. The target branch itself is never
  pushed, so a branch protected against direct pushes — requiring a pull
  request, a build check, or a review — is merged into normally, provided the
  account `gh` is authenticated as may merge and the request satisfies whatever
  the protection requires. Only the run branch is pushed. If the protection is
  not satisfied, the run stops with the unmet requirement named, the item is
  handed back to you, and your local target is left where the forge has it —
  see [a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request).
- **Merge commits allowed** in the repository's settings, since that is the
  method the harness asks for. A repository that permits only squashing or only
  rebasing refuses the merge, and the run reports that refusal — it does not
  fall back to a method that would replace the reviewed commit with a rewritten
  copy your local branch does not have. A protection rule requiring linear
  history has the same effect.

## Waiting out a provider that refuses

When the provider reports that a usage limit is exhausted, the run pauses rather
than failing: nothing is cleaned up, the Beads item stays claimed, the worktree
and branch survive, and the developer session is kept so the reissued attempt
continues the same change. This covers both provider invocations a run makes — a
developer attempt is reissued, and a review the provider declined is asked for
again without redeveloping the change or spending a repair attempt. Two settings
bound that wait, both written in Go's duration syntax (`6h`, `90m`, `45s`):

```yaml
execution:
  usage_limit_max_pause: 6h
  usage_limit_in_process_pause: 6h
  usage_limit_unknown_reset_pause: 30m
  server_overload_pause: 90s
```

`usage_limit_unknown_reset_pause` is the interval between probes: how long a run
sleeps before reissuing the attempt and finding out whether the provider will
serve it now. It applies whether or not a reset time was named, which is the
whole of the polling discipline. A limit reported *without* one is not the same
as having no capacity — an exhausted overage allowance reports this way while
the ordinary rolling window keeps resetting on its usual schedule — so the work
is waitable and simply carries no deadline. A limit reported *with* one is
waitable and carries a deadline that is an upper bound rather than a gate: a
reset time is a claim about the provider, and claims go stale in both directions,
because capacity gets bought mid-wait and a rolling window can free room before
the quoted edge. So a run sleeps this interval or the time left to the deadline,
whichever is shorter, and then asks again; a probe into a window that is still
closed costs one refused request and re-parks on whatever the provider now
reports. Every probe spends the same budget as any other wait, so a provider
that keeps refusing reaches the maximum rather than polling forever.

The same interval is how often a run asks again when the provider is
[not authenticated or cannot be reached](operations.md#waiting-out-a-provider-nobody-can-reach),
and how long a watching `yoyo work` leaves a provider nobody can reach before
pulling into it again to find out. That wait spends none of the budget below and
has no maximum: nothing but a person logging in or the network returning ends
it, so there is no bound a run could sensibly stop on.

`usage_limit_max_pause` is the longest a single run will spend waiting **in
total**, across every pause it takes. The budget is per run, not per pause,
because a provider that keeps refusing would otherwise walk a run far past the
configured maximum one individually-acceptable wait at a time. A reset time that
does not fit in what the run has left is treated as no usable reset time: the run
stops and records a blocker naming what it already spent, instead of sleeping on
it. The `6h` default covers the provider's five-hour limit with slack and
deliberately stops short of its seven-day one, because a capacity problem that
would cost days needs a person rather than a timer. Setting it to `0` disables
waiting entirely, so every exhausted limit blocks immediately.

`usage_limit_in_process_pause` is how much of that bound a run will spend
sleeping inside the `yoyodyne` process. It defaults to the same `6h`, so by
default every probe the harness will take is taken here and the run continues on
its own once the limit resets. Lowering it — say to `1h` — makes the process
sleep probes until it has spent that hour on the run and then exit, with the run
still in flight and its deadline recorded. Two things continue that same run,
and either process gets the whole bound again: running `yoyo run` on the same
item, or [`yoyo reconcile`](operations.md#waiting-out-a-provider-usage-limit),
which continues it itself once the recorded deadline has passed and no process
is serving the wait, so a run left this way does not hold a developer slot
until somebody types the verb.

It is also what bounds a run parked on an operator pause — `yoyo pause`, which
holds everything the harness would spend at every provider-call boundary. That
pause has no configuration of its own and no maximum: what lifts it is the
operator rather than a clock, and time spent held is accounted separately from
`usage_limit_max_pause`, because the provider never refused the run. What it
shares is this bound on how long a single process will stay open waiting, and
the durable park behind it: a run held past the bound exits with the park
recorded, and `yoyo run` on the same item continues it once `yoyo resume` has
lifted the pause. See the README for the whole of that behavior.

The bound is on how long one process stays open for a run, so it counts every
probe that process has already slept rather than each probe separately. Applying
it per probe would bound nothing: a probe interval of `30m` fits under a `1h`
bound however many times it is taken, so a six-hour deadline would hold the
process open for the whole six hours in half-hour slices. It spans phases for
the same reason — a process that waited half an hour for the developer and half
an hour for the review has been open for an hour.

Both paths record the deadline in durable run state *before* any waiting begins,
so a process that dies mid-wait loses nothing and a restart serves the same
deadline rather than retrying straight back into the limit. What each probe will
spend is committed before it is spent, for the same reason, and the unspent
remainder of a probe cut short is given back — so the recorded total is what was
actually waited. `yoyo reconcile` leaves a paused run whose deadline has not
passed alone for the same reason it leaves a repair loop alone: it is not an
interrupted run, it is a run that is owed the attempt it was refused. Once the
deadline has passed with no process serving the wait — the process that was
asleep on it exited on `usage_limit_in_process_pause` — the sweep continues the
run itself, in its own worktree and developer session, and the run's record
says the sweep did; see
[Waiting out a provider usage limit](operations.md#waiting-out-a-provider-usage-limit).

A reset time that is unreadable or already in the past stops the run with a
blocker naming what refused it. A reset that is not in the future is refused
deliberately: a limit still declining work while claiming it has already reset
is not describing a wait, and honoring it would mean reissuing straight back
into the same refusal.

A reset beyond what the run has left of `usage_limit_max_pause` — including the
probe a limit with no reset time would wait for — is a wait the harness will not
take rather than anything a person has to decide, so it blocks nothing. The run
ends cancelled, recorded as ended by something outside the work (cause
`usage-window`) with the reset it is waiting for; it gives its claim back so the
item is ready again, and keeps its branch and worktree. It counts toward nothing: not the failure-storm brake, not the
item's review rounds, repair grant, or re-run, and not a watching session's
memory of what it has tried. A watching session holds the item only until the
reset passes and then pulls it again by itself. The item's notes, the run's
ending in `yoyo status` (and its `capacity_blocked` entry), and the channel all
name the window and its reset.

These three settings are not only a run's. A conversation turn you typed —
`yoyo chat`, interactive or `--message` — waits out a refusing provider under
exactly the same bounds and the same polling discipline, because an operator who
has said how long the harness may wait out a limit has said it about every
invocation they pay for. Two things differ. The budget covers the message you
are waiting for rather than one run, across however many rounds that message
takes. And a wait this configuration will not take fails the turn instead of
parking it: a run leaves its deadline in durable state for a later invocation to
continue, and a turn has no such record, so it says what refused it and when the
provider claimed it lifts, and saying the same thing again takes the turn. What
the turn had already done is unaffected either way — tracker actions applied by
a round that finished stay applied, and only the invocation the provider declined
is reissued. The turns the harness takes for itself — a stopped run delivered to
the development manager, a recurring firing, a correction, the Slack sink's —
read none of this and fail on the refusal as they always did, because each of
them already paces itself on it and a `yoyo work` session that slept through a
window inside one turn would be a session choosing nothing for hours.
[A provider refusal outside a run](operations.md#a-provider-refusal-outside-a-run)
covers what an operator sees while it happens.

`server_overload_pause` is the same discipline on a different clock, for the
other way a provider refuses without judging the work: its own servers are
transiently unable to serve the attempt. That names no reset time at all and
lifts in seconds rather than hours, so a run waits this interval — `90s` by
default — and reissues, rather than parking for the half-hour probe interval a
usage limit uses. Everything else is shared with the paragraphs above: the
deadline is durable before the wait begins, the reissue continues the same
worktree and developer session, and each wait spends `usage_limit_max_pause`, so
an overload that never lifts walks into that maximum and stops with a blocker
instead of reissuing forever. `yoyo resume` releases one of these waits exactly
as it releases a usage-limit wait.

Ordinary transient throttling still never reaches any of this: the provider CLI
retries that itself, and the harness does not duplicate the wait. What the
harness acts on is the terminal result that CLI ends on once its own retries are
spent — an `api_error` reporting HTTP 529 — because the provider has stopped
retrying by then and something has to. An overload is the only terminal
`api_error` that becomes a wait *of this kind* — one on a deadline the provider
named, spending `usage_limit_max_pause`. The rest are covered by
[the relaunch budget](#relaunching-a-run-the-provider-killed) below, and one of
them becomes a wait of the other kind once that budget is spent: a terminal
`api_error` whose detail is plainly a dropped connection —
`API Error: Connection closed mid-response` is the specimen — is then
[waited out on the recovery window](#waiting-out-a-network-that-dropped) rather
than blocking the item.

`yoyo resume <beads-id>` is the one thing that overrides a recorded deadline,
and it overrides nothing else. It moves the next probe to now, for when the
reset has stopped being true — you raised the account's capacity, say — because
the deadline is a claim about the provider and you are the one who can change
what it is a claim about. It never stops the run: a process asleep on the wait
acts on the release within seconds, and the run keeps its claim, its branch, its
worktree, and its developer session. If the provider still refuses, the run
records the new report and waits again, so a premature release costs one refused
request. See the README for the whole of that behavior.

### Serving a turn from a permitted alternate model

Waiting is the right answer for a run and the wrong one for a decision. On
2026-09-07 at 06:00 the model the management roles were configured for closed its
capacity window while the model the developers and reviewers run on still had
one: the deciders stopped, the doers did not, and every item held for a
development manager decision sat behind a role that could not take a turn until
somebody hand-edited three agents onto the other model.

An agent may therefore name one alternate model and be served by it while its own
model has no capacity. It is stated in the agent's own block, beside the model
and the account, and it is off unless the agent says otherwise:

```yaml
agents:
  development-manager:
    role: development-manager
    model: fable
    failover:
      enabled: true
      model: opus
```

`enabled` is `false` for every agent that does not write it, so nothing acquires
this by inheriting a bundle or by upgrading the executable — which agents are
worth serving from a second model is a judgement about the work, and a management
role that has to keep deciding and a developer whose work can wait out a window
are different answers to the same question. Setting `enabled: false` while
leaving `model` in place switches the behaviour off without losing the choice, so
turning it back on is one word. The two keys are read together: a layer that
supplies a `failover` block replaces whatever it inherited whole, rather than
switching failover on over an alternate some other layer named.

Off by default has a price, and it was paid once: between 2026-09-08 and 09-13
every agent of this project ran on one model, that model's seven-day window
closed with a reset five days off, no agent had turned this on, and nothing
moved for five days. So a project whose every agent runs on one model with no
alternate named is a [`yoyo doctor`](operations.md#checking-the-installation)
warning under `failover`, before any window closes; and while a window is
holding every role, the channel and `yoyo status`
[say so](reporting.md#the-provider-holding-every-role), naming the reset and
this block as the remedy.

There is exactly one alternate. A list would be a routing policy; this is a
fallback, so the second endpoint either has capacity or the turn waits as it did
before. An agent that enables failover and names no alternate is refused, as is
one that names its own endpoint — a failover to the endpoint whose window just
closed is a second refusal rather than an alternate.

The alternate may name a provider and an account as well as a model, and both
default to the ones the agent already runs on:

```yaml
agents:
  development-manager:
    role: development-manager
    backend: claude-code
    model: fable
    failover:
      enabled: true
      model: second-model
      provider: second-provider
      account: second-account
```

That is there because the window that closes is not always the model's. A whole
provider can decline — an account suspended, a subscription exhausted, the
provider down — and an alternate that could only ever name another model on the
same provider is no answer to that. An agent that names only a model fails over
within its own provider, exactly as it did before these two keys existed.

`provider` and `account` say where an alternate is served, so a block that names
either and no `model` is refused: it says where and never what, which would read
as a configured crossing that can never happen. That holds with `enabled: false`
too — switching failover off keeps a choice already made, and there is none to
keep in a block nobody finished.

A crossing is refused where the file is read if the alternate names a provider
this project does not name, one that cannot be held to the tool access the
agent's role requires, or an account that could not sign that provider in. The
account is the one the agent would actually be served under — the account it
names, or the pool's first that can sign its own provider in — rather than only
the alias the `failover` block wrote down, so an agent that named no account of
its own is refused here too. The same three are asked again at the moment of the
substitution, because which tools a role can use is not something to take on trust from a check
that ran earlier.

A crossing that cannot be resolved when a conversation opens — an account edited
away under a running harness, say — leaves that conversation with no failover at
all, and `yoyo chat` says so on stderr. It is not degraded to a substitution
within the provider: the alternate's model belongs to the other provider, so
asking this conversation's own provider for it would meet an unknown selector at
exactly the moment the fallback existed to save the turn.

**A crossing covers conversation turns and exchange rounds.** An alternate on
the agent's own provider serves its side threads as well; one that leaves the
provider does not, because a side turn is answered on the endpoint the agent is
configured for and there is no crossing for it to take. An exchange round crosses
because its prompt already carries every earlier round, so the other provider is
sent the whole thread and no session; the exchange records the provider and
account that answered. An agent whose alternate names a provider therefore has
its conversation and the questions other roles ask it carried through a window,
and its side turns waiting the window out, alongside the run invocations.
`yoyo agent` says which of the two an agent has.

**An alternate on another provider also stands in for an executable that cannot
run.** Where the agent's own provider's executable cannot be found or started in
the environment the turn is made in — the CLI is not on the PATH this process
was given, or the path `providers.<backend>.binary` names is missing or not
executable — the turn, the scheduled pass, or the exchange round is made on that
alternate instead, and a conversation still opens, saying on stderr which
installation is missing and which alternate is serving it. The substitution is
recorded with that reason and the executable's own account of what was missing,
reaches the channel as a note like any other, and stands for
`execution.usage_limit_unknown_reset_pause` before the agent's own provider is
looked for again. An alternate on the same provider is no answer to this, since
the same executable would start it, so an agent with no alternate on another
provider is never moved: its turn fails with the missing executable and the
[setup that fixes it](provider-plugins.md#executable-setup-and-precedence).
A provider that starts and reports that it is not logged in is not moved by
this: authentication is checked separately, and is handled as
[a provider that refuses](#waiting-out-a-provider-that-refuses) as before.

What happens on a refused turn:

- The configured model is asked first. If the provider declines the turn for
  want of capacity, the same invocation is made once more on the alternate
  endpoint, and the answer that comes back is the answer.
- Each attempt is priced against the endpoint that attempt actually asked, so
  the cost log says what was spent where rather than billing the alternate's turn
  to the model that refused it. A crossing is charged to the alternate's own
  account and provider, which is the subscription the money actually left.
- The endpoint the turn would move onto is checked against the tool access the
  role requires before it is moved. A substitution can never put a role on a
  provider whose sandbox cannot hold that tool access — a reviewer needs a provider
  that enforces read-only access, and a developer one that can scope writes to a
  worktree — and a substitution that would is refused with the tool access named,
  leaving the turn to take the refusal it would have taken anyway.
- **A crossing rebuilds rather than resumes.** Every turn but the first resumes a
  provider session, which is why a later turn's prompt carries so little: the
  session already holds the picture, the operator's earlier messages, and what
  the role has said. A session belongs to the provider that issued it, so a turn
  served on another provider has nothing to resume. What it is handed instead is
  assembled from the conversation's own durable record — the picture it is working
  from, and the exchange its event log holds — with no session identifier anywhere.
  The log carries both sides: each of the operator's messages is recorded by the
  harness before the role is asked to answer it, redacted and bounded exactly as
  the reply is, so the reconstruction replays the exchange in order with each side
  named. A conversation begun before the operator's side was kept has replies with
  no message before them, and the reconstruction says so and tells the role to say
  when that leaves it unsure rather than to fill the gap in. A crossing that could
  not be rebuilt is refused before it is attempted, and the turn takes the refusal
  it already met.
  The turn after a crossing crosses back the same way: the session on the record
  belongs to the provider that served the crossing, so it is not sent, and the
  context is rebuilt again for the provider the agent is configured for.
- The substitution is recorded in the same per-product usage-limit log every
  refusal outside a run is recorded in, carrying the endpoint that was refused and
  the one that served — both providers where the turn crossed, because two
  providers can spell one model name. The conversation's own record keeps the
  whole endpoint that served each turn, and `yoyo chat` says so at the prompt.
- While that refusal stands, the next turn goes straight to the alternate rather
  than paying a refused invocation to rediscover a window the harness has already
  watched close. Affinity is the configured model's: the first turn after it
  stops standing asks it again, so a substitution lasts a window rather than
  becoming a quiet permanent move.
- How long it stands is the provider's reset time where the provider named a
  usable one. Where it named none — or named one already in the past, which
  describes no wait at all — what stands in for it is
  `execution.usage_limit_unknown_reset_pause`, the same interval a run waits
  before probing an undated limit. So an undated outage is a sequence of windows
  one probe interval long rather than one window of unknown length, and the
  configured model is asked again at the top of each.
- The substitution reaches the operator's channel as a note. Nothing stopped —
  that is the whole point of it — but an agent answering on an endpoint the
  operator did not configure it for is a change to what the work was produced by.
  A crossing says which provider produced it and that the context was rebuilt. It
  is said once per window rather than again while one stands, which for an undated
  refusal means once per probe interval: a six-hour outage the provider never
  dated is said around twelve times at the `30m` default, not once per turn.

An agent that has not enabled failover behaves exactly as it did before: one
invocation, under the model it named, and a refused turn recorded as the stoppage
it is — which a turn you typed at `yoyo chat` then
[waits out on that model](#waiting-out-a-provider-that-refuses) under the
settings above, and a turn the harness took for itself fails on. So does an
agent whose alternate is refused too. A run's developer and reviewer invocations
are not covered by this and still wait their window out on the settings above;
what this covers is the turns an agent takes — the conversations, where a
decision nobody can make stops everything downstream of it.

### A conversation too long for its provider session

A conversation's provider session grows with every turn, and a long-held one can
outgrow what the provider will take: it refuses the turn as too long (Claude
Code's `Prompt is too long`, its API's `request_too_large`, Codex's
`context_length_exceeded`), or the compaction that would have shrunk the session
fails. That turn is not failed and the conversation is not replaced. The session
is set aside and the same turn is asked once more in a fresh provider session,
under the same conversation identifier, with its recent context rebuilt from the
record exactly as [a crossing](#serving-a-turn-from-a-permitted-alternate-model)
rebuilds it: the picture the conversation is working from, and the most recent
80 messages of the exchange. The reconstruction tells the role why it has no
session, so it knows the earliest of what it said is gone. Nothing else about the
conversation changes — its agent's memory, its report position, and its picture
carry over as they are.

The conversation's event log records a `session.replaced` event naming the
session set aside, the endpoint that refused it — the alternate's, where failover
had moved the turn — and what the provider said. A reply the provider did not flag
as a failure is read as this refusal only when the provider's notice is the whole
of it, so a role that merely mentions one of these errors is never mistaken for
one. The session is cleared on the record before the fresh attempt, so a fresh attempt that is refused too fails
that turn only, and the next turn rebuilds again rather than resuming the session
that was refused. A turn is given one fresh session, not a loop of them.

A rebuilt conversation is also checked against the selected adapter's input
limit before it is sent, with five percent left for framing. Oldest replayed
messages are dropped first and their omission is named to the role; current
instructions and evidence, memory, decisions and docket records are preserved.
Codex's 1,048,576-character bound includes the role contract and inspection
instructions its adapter adds. If the endpoint nevertheless refuses for request
size (`input_too_large` or `request_too_large`), the turn gets one shorter
reconstruction before it can fail. This also applies to the memory-save turn
before compaction and to the receiving endpoint on failover. A save refused on
the old session retries from the durable record; its writes are recorded before
the waiting message continues. A capacity wait retains the shortened request
and its spent size retry; an intervening turn rebuilds from the latest record
with the reduced history allowance. If that reconstruction fails, the error is
returned and the recorded replacement's event position is kept. These bounds
belong to the adapters and add no configuration key.
See [request size protection](conversation.md) for the
compaction and refusal behavior.

### Pinning an agent to a model version

A model selector is a family alias by default — `opus`, `fable` — and an alias
floats: it follows the provider's current best for that family without anybody
editing a file, and the run evidence records the exact identifier the provider
reported serving. That is the right default and it stays the default.

What an alias cannot do is hold a version still. Comparing two weeks of work,
reproducing something a particular version did, or running a persona tuned
against one release all need the selector to stop moving, and the alias's whole
virtue is that it does not. So an agent may name a version as well, in the same
block as the model, the account, the persona binding, and the failover
alternate:

```yaml
agents:
  architect:
    role: architect
    model: opus
    model_version: claude-opus-5-20260401
```

`model_version` is absent for every agent that does not write it, and an agent
without one behaves exactly as it did before this existed. Naming it is the whole
of the switch — there is no `enabled` beside it, because unlike failover there is
no judgement worth keeping while it is off. Stating it empty in a later layer
removes an inherited pin and puts the alias back to floating. A version that is
the alias itself is refused: it pins nothing, and the thing it would fall back to
is itself. So is one that is also the agent's `failover.model` — the model a turn
moves to when a family has no capacity is not the version that family was pinned
to.

**The pin is a preference, not a requirement.** Versions get retired, and an
agent whose pin the provider has stopped serving would simply stop taking turns —
the same stall failover exists to prevent, reached from the other direction. So:

- The pinned version is asked for. If the provider serves it, that is the whole
  of it: one invocation, and nothing is recorded or said.
- If the provider answers that it has not got that model, the same invocation is
  made once more under `model` — the family alias, which is by definition the
  family's latest — and the answer that comes back is the answer. There is no
  table here mapping versions to families, and nothing to keep up to date when a
  family gains one. A refusal that is *not* the provider lacking the model is
  handled where it belongs rather than here: no capacity goes to `failover.model`
  if the agent named one, and anything else fails the turn under the version that
  asked, as it would have under the alias.
- Each attempt is priced against the model that attempt actually asked for.
- The fallback is recorded in the same per-product usage-limit log a failover
  substitution is, carrying the version that was refused, the model that served,
  and `substitution: availability` — so one record answers "which model served
  this turn, and why" whichever mechanism chose it. The conversation's own record
  and `yoyo chat` say which model served.
- While that stands, the next turn goes straight to the alias rather than paying
  a refused invocation to be told the same thing again. It stands for
  `execution.usage_limit_unknown_reset_pause` and no longer: a provider that has
  not got a model quotes no deadline for getting one, so the pin is asked for
  again at the top of each interval. A version that was skipped once and then
  forever would be an alias the operator believes is a pin.
- The fallback reaches the operator's channel as a note, said once per interval
  rather than once per turn, exactly as a capacity substitution is. A pin
  silently not being honored is the one outcome that would make the evidence
  false.

**Pinning an agent never costs it failover.** The pinned version is asked for
through the same path the family alias would have been, so which mechanism
answers a refusal is decided by the refusal rather than by an order fixed in
advance:

- The provider having no capacity for the pinned version is failover's, and
  `failover.model` answers it — exactly as it would have had nothing been pinned.
  Falling back to `model` there would buy nothing, because the alias floats over
  the same family and is therefore inside the same window. The closed window is
  recorded against the pinned selector, so the next turn inside it goes straight
  to the alternate.
- The provider not having the pinned version is the fallback's, and `model`
  answers it. That attempt is then subject to failover in its turn, so a version
  the provider has retired during a capacity outage still reaches the alternate.

Each hop records itself, because one entry naming both would name a model that
refused a turn nobody asked it.

A pin covers the same invocations failover does: the turns an agent takes as
itself, its conversation and the rounds where another role asks it something. A
run's invocations are not among them, and neither of them asks for the pin: the
reviewer's asks for the reviewer agent's `model`, and the developer's asks for
whichever selector the item's own labels chose under
[`execution.developer_models`](#a-developer-model-chosen-by-the-items-label) —
which is the developer agent's `model` for an item that mapping names no label
of, and the mapped one otherwise. `yoyo agent list` says so for every pinned
agent rather than leaving it to be assumed. A recurring task's pass is the
agent's own turn and carries the pin, unless the task
[names another model](#a-tasks-own-model), which carries none.

### An agent's effort level

`effort` sits beside `model` in an agent's block and says how hard the agent's
provider is asked to think on every invocation of that agent: its developer or
reviewer invocations in a run, its conversation turns, the rounds where another
role asks it something, its side threads, and its recurring and program manager
passes. Claude Code accepts `low`, `medium`, `high`, `xhigh`, and `max` — its own
`--effort` flag, which the harness passes — and anything else is refused when the
file loads, naming those five.

The template `yoyo init` writes states `effort: medium` for every agent. That is
not a preference: it is Claude Code's own default for the model `opus` serves
today, Opus 5.5, which its model configuration documentation gives as `medium`
(every other model that takes an effort level defaults to `high`). Writing it
down pins the level where it was, so it does not move when the alias does, and
makes it something a record can say. A later change of level is an edit to this
key.

The key is optional. A Claude Code agent without one passes no level, and Claude Code
resolves its own from the environment, the machine's settings, or the model's
default — which is how every agent ran before the key existed and how a project
whose file predates it still runs. Stating it empty in a later layer removes an
inherited level. A project generated before the template carried it hears of
it from [`yoyo config drift`](#extending-a-built-in-bundle) as an available
value.

**The level belongs to the agent, not the model.** A model
[`execution.developer_models`](#a-developer-model-chosen-by-the-items-label)
maps an item to, a model a [recurring task](#a-tasks-own-model) names, a pinned
version's fallback to its alias, and a
[failover alternate](#serving-a-turn-from-a-permitted-alternate-model) all serve
the turn at the agent's level. The [run endpoint pair resolver](#developer-slot-endpoints)
also accepts an explicit alternate effort and refuses incompatible inherited
levels. For existing conversation failover, the one exception is a failover that crosses onto
a provider that does not accept the agent's level: that turn is asked with none, and its record
says none was asked rather than the level configured, so the failover still
saves the turn.

**Codex always receives an explicit level.** On fresh and resumed invocations,
including developer, reviewer, conversation, and recurring turns, the adapter
passes `--config 'model_reasoning_effort="high"'` for `effort: high`, before
`resume` when resuming. This overrides personal Codex settings. Omitting `effort`
or setting it empty explicitly passes the invoked model's advertised default.
The default is model-specific:

| Codex model | Accepted levels | Default |
| --- | --- | --- |
| `gpt-6-astra`, `gpt-6.1-sol`, `gpt-5.6-sol`, `gpt-daybreak-blue-latest` | `low`, `medium`, `high`, `xhigh`, `max`, `ultra` | `low` |
| `gpt-6-sol`, `gpt-5.6-terra`, `gpt-daybreak-red-latest` | `low`, `medium`, `high`, `xhigh`, `max`, `ultra` | `medium` |
| `gpt-6-luna`, `gpt-5.6-luna`, `codex-auto-review` | `low`, `medium`, `high`, `xhigh`, `max` | `medium` |
| `gpt-5.5` | `low`, `medium`, `high`, `xhigh` | `medium` |

Established against **codex-cli 0.159.2**: `codex exec --help` and
`codex exec resume --help` establish the config override and its placement;
`codex debug models --bundled` supplies these levels and defaults without a
provider call. Local validation in an isolated `CODEX_HOME` shows that
`model_reasoning_effort` takes a string (an integer is refused). That CLI accepts
unadvertised strings locally too, so parsing alone cannot establish model
support. Yoyo validates against the recorded bundled catalog in
`internal/backend/testdata/codex-cli-0.159.2-effort.json`: an unsupported level is
refused at configuration load with accepted values named. An unlisted selector,
including an unverified exact model version, is refused because this build has
no established levels or default for it. The same check covers Codex models in
failover, developer mappings, and recurring tasks. A provider a project
[declares](provider-plugins.md) inherits its adapter's effort policy.

For the technical-health program manager, configure `backend: codex`,
`model: gpt-6-astra`, `effort: high`, with failover disabled and no model-version
fallback. Deploy a build carrying this support before activating that live
configuration; activation and the first-pass proof belong to delivering the
technical-health program manager (yoyodyne-ifd.430.13.20). This change adds no
configuration key: it extends the existing `agents.*.effort` key.

**Every record says what was asked.** A run records the developer's level at
reservation — read back by every attempt, as its account and model are, so an
edit to the key reaches the next run and never one already in flight — and
the reviewer's with its verdict; both appear in the item's notes beside the
models. A conversation records the level of its last turn, an exchange round and
a side thread record theirs, a recurring or program manager pass records its
pass's, and a branch review records its reviewer's. Every line in the cost log
carries the level its invocation asked for, so each turn is pinned to one even
where the conversation's own record has moved on. An absent requested level on an older record means none was asked. Codex
invocations made by this build record their explicit default when none was
configured. `yoyo config show` prints the key beside the model, `yoyo agent list`
says `model opus at medium effort`, `yoyo status` says it beside the model on
the line of each running run and each conversation in flight — `developing, on
claude-opus-5 at medium effort` — and carries it under `--json`, and the
dashboard shows it beside the model on a run's card. Records keep the
provider-reported level separately as `resolved_effort` and `effort_reported`,
with corresponding `provider_*` and `review_*` fields on run records. A stream
that reports no effort records `effort_reported: false`: the served effort was
not reported, and the requested level is never copied into it. The current
Codex `exec --json` stream normally omits effort; a `session_configured` event
that includes `reasoning_effort` records that value. Every invocation's cost
line preserves both facts, even when a later turn replaces the conversation's
record. Sweep evidence uses the effort actually requested by the serving turn,
rather than re-reading the configuration after a provider substitution. A line whose record names
no level says nothing of one, and reads as it did before the key existed.

### Skills and instruction files a Codex role is given

A Codex role is given the skills and instruction files the project names in a
top-level `codex` section, and nothing else. Skills, plugins, and instruction
files in the Codex home of the account a turn runs under — `~/.codex/skills`,
`~/.codex/AGENTS.md`, the plugins its `config.toml` enables, and the like — are
not loaded, whether the role is a developer, a reviewer, a conversation, or a
recurring pass, and whether the turn starts a session or resumes one. Neither
are skills under `~/.agents/skills` or the repository's own `.agents/skills`. To
give a role one of them, name it:

```yaml
codex:
  skills:
    # A directory holding SKILL.md, or the SKILL.md itself.
    - path: .yoyodyne/skills/code-review
      roles: [reviewer]
  instructions:
    - path: docs/agent-notes.md
    # A personal file is given only when the project names it.
    - path: ~/.codex/AGENTS.md
      roles: [developer]
```

A relative path is read from the harness's own checkout of the repository,
for every role — never from a run's worktree, so a change under review cannot
rewrite the instructions its reviewer is given — and `~/` is the home
directory of whoever runs the harness. A file named for a reviewer therefore
takes effect once the change that edits it has landed. `roles` narrows an entry to those roles; without it every role gets
it. The harness reads each file and puts it in the prompt between the role's
contract and its task, with a skill's own directory named so the role can read
the files the skill refers to. A named file that cannot be read stops the turn
before the provider is asked, with the path in the error, rather than running
the role without it. An entry with no path, or a role that does not exist, is
refused when the file loads. Plugins cannot be named: none is loaded.

Two things are not personal and stay. A developer's turn still reads the
repository's own `AGENTS.md` (or `AGENTS.override.md`) from its worktree, as
Codex always has. And the account's `config.toml` still applies to a
developer's turn where nothing overrides it, so the
[effort rule](#an-agents-effort-level) is unchanged — except for the settings in
it that carry instructions: `developer_instructions` is emptied on every turn,
and `model_instructions_file`, `experimental_compact_prompt_file`, and
`compact_prompt` set at the top of the file are left out of what the turn
reads.

Every record says what was loaded, by name and source, and says `none` for
each kind where that was nothing: a run keeps the developer's as
`provider_loaded` and the review's as `review_loaded`, a conversation keeps its
last turn's as `provider_loaded`, and every Codex turn's `run.started` event
carries the same one-line account, for example
`skills: none; plugins: none; instruction files: AGENTS.md (repository, /path/to/worktree/AGENTS.md)`.
A Claude Code turn's record also names its settings sources and connectors; see
[the next section](#settings-memory-skills-connectors-and-instruction-files-a-claude-code-role-is-given).

This adds the `codex` configuration key. A part of the product still running a
build from before it refuses a file that carries it; `yoyo config validate` and
`yoyo doctor` name any such part, and the product restarts it on this build
when it moves its parts onto the build. How the CLI is
kept from loading the rest is in
[provider plugins](provider-plugins.md#codex-skills-plugins-and-instruction-files).

### Settings, memory, skills, connectors, and instruction files a Claude Code role is given

A Claude Code role is given the skills and instruction files the project names
in a top-level `claude_code` section, and nothing personal. From the Claude
Code home of the account a turn runs under it reads no settings file
(`~/.claude/settings.json`, with the hooks, permissions, and plugins it
enables), no memory (the auto-memory index and its notes), no skill, agent, or
plugin, no MCP server, no claude.ai connector — Gmail, Calendar, and the rest
attached to the account on claude.ai — and no instruction file
(`~/.claude/CLAUDE.md` and its rules). That holds whether the role is a
developer, a reviewer, a conversation, or a recurring pass, and whether the turn
starts a session or resumes one. Neither are skills in the repository's own
`.claude/skills` or the CLI's built-in ones. The section has the shape of the
[Codex one](#skills-and-instruction-files-a-codex-role-is-given), and its
entries are read the same way:

```yaml
claude_code:
  skills:
    - path: .yoyodyne/skills/code-review
      roles: [reviewer]
  instructions:
    - path: docs/agent-notes.md
    # A personal file is given only when the project names it.
    - path: ~/.claude/CLAUDE.md
      roles: [developer]
```

A relative path is read from the harness's own checkout, `~/` is the home
directory of whoever runs the harness, and `roles` narrows an entry. The harness
reads each file and adds it to the role's standing instructions after its
contract. A named file that cannot be read stops the turn before the provider
is asked. Plugins, MCP servers, and connectors cannot be named: none is loaded.
The `codex` and `claude_code` sections are separate, so a file named in one is
not given to the other provider's roles.

What a developer reads from its own worktree is the repository's and stays: its
checked-in `.claude/settings.json`, which is where its project hooks come from,
and the `CLAUDE.md` files in the worktree. A `CLAUDE.md` in a directory above
the worktree is not the repository's and is not read. Every other role reads no
settings file and no `CLAUDE.md` at all, so the repository it inspects cannot
configure it. Admin-managed policy settings installed on the machine still
apply, as they do to every Claude Code session.

Every record says what was loaded, by name and source, with `none` for each
kind that was empty, in the same places as a Codex turn's — `provider_loaded`,
`review_loaded`, and the `run.started` event — for example
`settings sources: .claude/settings.json (repository, /path/to/worktree/.claude/settings.json); skills: none; plugins: none; connectors: none; instruction files: CLAUDE.md (repository, /path/to/worktree/CLAUDE.md)`.

This adds the `claude_code` configuration key. A part of the product still
running a build from before it refuses a file that carries it; `yoyo config
validate` and `yoyo doctor` name any such part, and the product restarts it on
this build when it moves its parts onto the build. How the CLI is kept from
loading the rest is in
[provider plugins](provider-plugins.md#claude-code-settings-memory-skills-connectors-and-instruction-files).

## Relaunching a run the provider killed

Not every way a provider ends an invocation is a refusal it names in advance.
Sometimes it dies: the API answers with an error its own retry ladder did not
outlast, or the connection carrying the response goes away before the reply is
finished — `API Error: Connection closed mid-response`, which quotes no HTTP
status because nothing answered. The work was never judged and nothing is wrong
with the change. Rather than failing, the run relaunches itself:

```yaml
execution:
  transient_relaunches_before_blocking: 2
```

The dead invocation is reissued in the same worktree and the same developer
session, so an attempt that died mid-response continues the change it had already
started rather than deriving it again. No wait is attached, because there is no
condition to wait out: a dropped connection is already gone, and the provider's
own retries are spent before the harness sees the terminal.

A stream that ends one invocation twice spends the same budget. Two terminal
results where there was only ever one ending judge nothing about the work, and
neither can be told apart from the other — a subagent's completion carrying a
terminal's marks is read as the invocation's, so the run's own ending arrives
looking like the duplicate — so the invocation is asked again rather than
published. Both endings stay in the run's event log. A stream the harness
genuinely cannot read still fails the run.

One budget covers both provider invocations a run makes. A review the provider
killed is asked for again on the same count, without redeveloping the change,
because what the budget bounds is how much of the provider's weather a single run
absorbs rather than how often either role is asked. Nothing is handed back to the
developer, so a relaunch spends no repair attempt.

Relaunches are counted in durable run state before each one begins, so a process
that dies mid-relaunch resumes against the budget it had rather than a fresh one.

Setting the bound to `0` buys no relaunches at all: the first provider death is
the last, and the run stops there. It is **not** an opt-out of
[waiting a dropped connection out](#waiting-out-a-network-that-dropped), which
is a different rule and is not configured — a death that is plainly a reset
connection is waited out on the recovery window at `0` exactly as it is at `2`,
because what the operator ruled is that the harness never fails outright on
anything that can recover. What `0` decides is how much of the provider's
unclassifiable weather one run absorbs before it stops.

What happens once the budget is spent depends on what killed the invocation. A
death nothing can classify stops the run and records a blocker on the work item
naming the provider's own last message. A death that is plainly a dropped
connection does not: it is
[waited out and asked again](#waiting-out-a-network-that-dropped) past the
budget, on the backoff every other transport failure gets, and only a run that
spends that window as well stops. This budget is the right bound for provider
weather nobody has classified; a reset connection is not that, and stopping on
one is what cost four runs their finished work on 2026-09-03.

What else that blocker says depends on what the run was carrying, because a
provider dies during a repair attempt as readily as during the first one. A run
nothing had judged yet says plainly that no check failed and no reviewer asked
for repair. A run killed inside its repair loop names the repair attempts it had
spent, the failing check, and the findings it was answering, and says the
provider stopped it rather than that verdict — the evidence is unresolved rather
than dismissed.

A refusal that would stand is never relaunched. A terminal `api_error` quoting a
4xx status — a malformed request, a key that is not permitted, a limit the
provider is enforcing — earns the identical answer on the next attempt, so it
fails the run as it always did; so does a 529, which is a wait rather than a
relaunch, and so does any terminal the API did not report at all. The invocation
ended twice is the one thing outside the API's own errors that still relaunches,
because it is not a verdict on anything.

## Waiting out a network that dropped

A run touches somebody else's network at its most expensive moments: it pushes
the run branch, opens and updates the pull request, reads where the remote target
branch stands, asks the forge to merge, confirms the merge, deletes the merged
branch, catches the local branch up, and makes every provider invocation over
it. It ends by writing to the tracker, which is not a network but is a store
other processes are writing to, and a `bd` too busy to run judges the work no
more than a reset connection does.
**A failure at one of those whose class says the next attempt may well succeed —
a connection reset, a network drop, a transport-level refusal — is waited out and
asked again rather than recorded as terminal.**

The rule is the operator's, and what produced it is four runs killed in one day
on 2026-09-03, each at its final publish or integrate step, each by one
connection reset, each with the work already completed and some of it already
reviewed. The
[intake hold](#watching-instead-of-draining) then held the whole line three
times, because the blocked runs came one after another.

**There is nothing to configure.** The waits are Fibonacci seconds — 1s, 1s, 2s,
3s, 5s, 8s, 13s — capped at half an hour and reaching that cap after about
seventy minutes, and each boundary gets a two-hour window of its own, which is
about twenty attempts. The numbers are the harness's and the same for every
product, exactly as the [watching session's](#watching-instead-of-draining)
retry of a tracker it could not read is: what they measure is how long a
connection that comes back takes rather than anything about a project. Each
boundary has its own window because a network that dropped a push says nothing
about a merge.

**Nothing that is an answer is waited on.** An authentication failure, a merge
the forge refused, a protected branch whose requirements are unmet, a conflict,
and any 4xx all earn the identical answer next time, so they are reported as
promptly as they always were. So is a failure whose class the harness does not
recognize: the set is deliberately small, and anything outside it keeps exactly
the behavior it had. The full recoverable-versus-terminal taxonomy is the
architect's, and this does not wait on it.

**Every wait is recorded before it is taken**, on the run itself, with the
boundary, which attempt it was, the interval, and the failure it waited out. So a
process that dies mid-wait comes back to the window it had already spent rather
than to a fresh one, and a run that waited a network out and finished says so on
the work item rather than merely looking slow. A window that runs out escalates
rather than going quiet: what the boundary would have produced is produced — an
outstanding publication, a blocker on the item — with the attempts and the time
in front of it.

A role's conversation reaches the same store, and its calls are under the same
rule with the same numbers: a triage decision, an admission, a note, a closure,
and the reads that gate them are waited out and asked again, with one window per
operator message shared by every call in it, each wait recorded on the
conversation as a `tracker.retried` event, and only a call that spent the
window reported the way it always was. Nothing about that is configured either.

[Waiting out a network that dropped](operations.md#waiting-out-a-network-that-dropped)
in the operations guide is the same thing said for an operator reading a run,
and says what the conversation shows on screen while it waits.

## Losing a race for the target branch

A run promotes its change by fast-forwarding the branch it was written against,
which requires that branch to still be where the run started from. On a target
the forge protects the local branch is not fast-forwarded — the change lands
through its pull request ([a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request))
— but the same requirement holds, and a remote target that moves between the
landing being prepared and the forge being asked to merge is a lost race too,
answered by the same replay and the same budget. It may not
be: another run can promote into the same branch first, and an operator who
commits to it while a run is working moves it just as effectively. The
promotion fails closed in both cases — nothing is force-merged and nothing is
reset — and the run then re-prepares rather than dying on it:

```yaml
execution:
  integration_retries_before_reconciliation: 2
```

Each lost race replays the change onto wherever the target went, runs the
configured checks again, and obtains a **fresh independent review**. The earlier
approval is discarded rather than carried over: it described a diff on the base
the change no longer sits on, and an approval that survived a replay would be
authorizing a promotion nobody judged. Nothing is handed back to the developer,
so a replay spends no repair attempt — the change is not what went wrong.

**Losing the race spends nothing, and never stops the run.** The change
standing at a refused promotion passed its checks and was approved, so a lost
race always replays, and a replay that passes its checks and is approved is
charged nothing: a run whose replays keep passing keeps replaying until it
lands, however busy the target is and whatever the budget says. Every lost race
is recorded on the run before its replay begins and is said in the item's
thread at note severity; it is never docketed and never blocks the item.

The budget bounds replays that stop on the change instead: one that conflicts,
and one whose replayed change is handed back for a failing check, a refused
path, missing verification, or a repair verdict. Each replay is charged at most
once, at the first such stop after it, and the charge is enforced right there.
While the count is within the budget the replayed change is handed back to the
developer like any repair; the replay that takes it past the budget stops the
run at that point, on the change, with a blocker saying what stopped the replay,
how many replays stopped on the change, and how many races were lost.
`integration_retries` counts the races and `charged_replays` the replays that
were charged, both saved before what they count takes effect, so a process that
dies mid-replay comes back to both. At `0` no replay may stop on the change:
the first replay that does ends the run there, and a lost race whose replay
passes still lands.

Whether a replay whose diff is byte-identical to the one approved may promote
without a fresh review is a question about the gate and the architect's to
rule on. Until the architect rules, every replay is reviewed afresh as above.

A merge the forge queued can lose the same race after the run is over: the
target moves on, the queued head falls behind it, and its checks fail on files
the change does not touch. `yoyo reconcile` reads a queued merge's checks on
every sweep and, finding that, withdraws the queued merge and replays the change
onto the target through the same run — checks again, a fresh review, and the
merge queued again. The update is a lost race and is never handed back for
being one; the replay's own gate charges the budget if the replayed change
stops on the change, as above. A run that cannot be replayed at all is handed
back instead
([operations](operations.md#recovering-interrupted-runs)).

A replay that **conflicts** is never retried and never resolved automatically
— which side of a conflict is right is a decision about the product, not a Git
operation — and it goes back to the developer that wrote the change before it
goes to anybody else. The change is moved onto where the target went, with
whatever would not merge left in the worktree between Git's own conflict
markers, and the same developer session is handed the conflict to settle: the
branch it could not be replayed onto, the commit that branch is at, and the
paths the replay stopped on. That hand-back is a repair attempt, spent from
`execution.repair_attempts_before_replan` like any other, and what the
developer produces goes through the whole gate again — the protected-path
refusal, the checks, and a fresh independent review — before it is promoted by
the same fast-forward. A replay conflict on an approved change therefore costs
a continuation of the session that wrote it rather than a fresh run. The target
keeps every commit it has; what the move drops is only the run's own commits,
whose content is what the developer is handed back as uncommitted work.

A conflict is also a replay that stopped on the change, so it is charged to
the integration budget above at the same moment, and a run past either budget —
no repair attempt left, or more conflicting replays than
`integration_retries_before_reconciliation` permits — stops instead, as every
conflict did before:
the replay is abandoned, the branch and worktree are left exactly as they were,
both sides survive, and the blocker on the item names the paths and the target
commit. The conflict stays on the run's record, so a repair the development
manager grants in triage continues that same session with the same conflict,
moved onto the target first; a re-run starts the change over instead.

A replay the harness itself ended — its local Git budget ran out, its context
was cancelled, or it went silent past its liveness bound — is **not** a
conflict, although it leaves the same half-applied state behind. It is told
apart by how the rebase stopped, abandoned the same way so the worktree is back
on its branch before anything is recorded, and reported as an integration stop
by something outside the work, of cause `replay-killed`, that
`yoyo triage resume` picks up, rather than as a conflict for a person to settle.

Two more stops of an approved change before its promotion are integration stops
of the same kind, although a person clears them: a target branch the harness
will not catch up to the remote's (`diverged-target`), and a remote that refused
the harness's SSH key or forge login on a push or a fetch
(`remote-auth-refused` — "Permission denied (publickey)" is the usual one). What
the person settles is the branches or the credential, never the change, so
`yoyo triage resume <run-id>` carries the approved change on afterwards at no
cost to the item. Asked before then it refuses, writes nothing, and says what
clears the cause: the diverged-target recovery in docs/operations.md, or
loading the key (`ssh-add`) or renewing the login (`gh auth login`). A refused
credential is never retried in the meantime, because asking again earns the
same answer.

A published run's pull request follows the replay: the run branch is replaced on
the remote from exactly the commit the harness published there, so the request
carries the change that would actually be promoted. A change handed back to
reconcile a conflict is replaced the same way, once the developer's attempt is
committed — never with the target alone in between, since a request whose head
is already in its base reads as merged. That is the same
compare-and-swap every other write makes — a remote branch carrying anything
else is refused rather than overwritten — and the refusal stops the run, because
nothing has been promoted yet and there is nothing outstanding to report.

## How long one role may ask another

Roles can put a question to each other through the harness — the Lead Product Manager
asking the architect what a goal costs before it orders the backlog, the
architect asking the Lead Product Manager whether a trade-off is one a user would
accept before it settles a design. Every exchange is recorded where you can read
it with `yoyo exchange`. Both halves retain their backend-enforced read-only
access, including repository inspection where supported. Their replies are
advice, not validation results or authority to act. What is configurable is how
long a single exchange may go on:

```yaml
exchange:
  max_rounds: 10             # the hard limit on rounds in one exchange thread
```

**It is a hard limit and it is durable with the exchange.** The number is copied
onto an exchange as it opens rather than read afresh each round, so a process
dying part way through, a second process picking the thread up, and an edit to
this setting all leave a thread already in flight bounded by what it started
with. A cap a crash could reset is not a cap.

**Reaching it is not a silent cutoff.** The exchange closes as
`unresolved-after-rounds`, and it is escalated to you as a report at warning
severity naming the two roles, the question, the rounds, and what the exchange
cost — so it reaches [the pile you read](reporting.md#what-agents-report-and-where-it-reaches-you)
rather than ending in a record nobody opens. The failure this bounds is two
judgement models deferring to each other politely for ever, which is rare,
expensive, and invisible without the number.

**Zero is refused**, unlike the triage caps above. An exchange allowed no round
at all is a channel that is off, and turning the channel off is a matter of
nobody using it rather than of configuring a limit nothing can be spent against.
One is the floor.

One further bound is the harness's rather than yours: a single thing you say to a
conversation sets off at most as many rounds of asking as one exchange is
allowed, however many exchanges it spreads them over. That bounds a reply
opening thread after thread, which is a different question from how long one
thread may run.

## How far behind a conversation's picture may fall

The Lead Product Manager, the architect, and the development manager are briefed
once, when a conversation opens, and every later turn resumes a session that
already holds that briefing. Before each reply the harness counts the landings
on the target branch since the picture was taken and records the count on the
conversation; past this many it re-reads the repository and the tracker before
the turn is answered, the way [`/refresh`](conversation.md#how-fresh-the-conversations-picture-is-and-how-to-refresh-it)
does when you ask:

```yaml
conversation:
  refresh_after_landings: 20   # landings on the target branch before a turn re-reads
```

**It is measured in landings, not hours.** A branch that took fifty commits in a
morning has moved further under a conversation than one that took none in a
week, and the number a role would have to state about its picture — what the
repository holds that the picture does not — is the count, so the count is what
the threshold is in. The picture records the commit it was taken against, and
the comparison is `git rev-list --count` from that commit to `HEAD` in the
primary checkout, whose current branch is the integration target every run is
promoted into.

**It times the re-read and does not switch it off.** Zero is refused, since a
picture allowed no landings behind is re-read on every turn, and so is anything
above 200: the case that admitted this was a picture roughly five hundred
landings old advising the operator to add a section a file had opened with for
a month, and a threshold that let one be advised from unrefreshed would be this
file disabling the statement it is only meant to time. Where the re-read cannot
be made — the tracker locked, the repository not answering — the reply carries
its picture's age in its own text, and no value here reaches that either. The
number is a judgement about your project's pace: how many landings a
conversation may reason across before what it does not know it does not know
is worth the cost of re-briefing it, which is the whole briefing carried into
the turn again.

## Queueing a question, or holding it on a side thread

A conversation takes its turns one at a time. That is what stops two processes
interleaving one transcript, and the price of it is that a busy thread queues
everything behind whatever it is doing: a question worth a minute waits out a
turn worth twenty, and the roles every other role waits on are the ones whose
threads are busiest.

An agent may therefore hold **side threads** — bounded conversations beside its
main one, each with its own stream, its own lease, and its own transcript, so a
question put to it while the main thread is busy is answered rather than queued.
It is stated in the agent's own block, beside the persona and the model:

```yaml
agents:
  architect:
    role: architect
    model: opus
    conversations: side-threads
```

`conversations` is `queue` for every agent that does not write it, which is what
every agent did before this key existed. Nothing acquires side threads by
inheriting a bundle or by upgrading the executable — which roles are worth
answering two questions at once is a judgement about the work, exactly as
[failover](#serving-a-turn-from-a-permitted-alternate-model) is. Stating it empty
in a later layer removes an inherited choice and puts the agent back to queueing.
A value that is neither word is refused at load, naming the two that are.
`yoyo agent list` says which agents hold side threads.

**Where the choice is made.** A single message — `yoyo chat --message` for the
Lead Product Manager, `yoyo agent chat <name> --message` for any agent — that finds
the agent's conversation mid-turn is the moment the knob decides. An agent that
queues has the message wait for the turn, which is what every message did before
the key existed. An agent that holds side threads has it answered beside the busy
turn instead, on a side thread of its own, and the answer says so: which thread
it came from, that it is the agent's judgment and not an action, and what the
agent tentatively committed to. Three kinds of message always reach the main
conversation whatever the knob says, because each has to: a `/command`, which the
harness carries out against the conversation; a decision or an answer, which
settles something the main conversation is waiting on; and a message with
`--new`, which replaces the conversation rather than sitting beside it. An
interactive `yoyo chat` and a message from Slack queue as they always have — a
side thread is a bounded number of turns, not a prompt to sit at. A side thread
the agent left open for a further turn is continued with
`--side-thread <id> --message`, addressed to the agent that holds it — another
agent's command naming the stream is refused before a turn is spent, so a thread
is never served on one agent's account and merged into another's memory. It takes
its turns on its own record and its own lease, so it neither waits for the main
conversation nor holds it.

**The knob selects behaviour and never authority.** A side thread judges,
answers, and tentatively plans: it reads the tracker and the evidence its role
was given, and every intent it forms is a draft. It admits no work, mutates no
item, raises no proposal or concern, commissions no research, records no
evaluation, and issues no directive — whatever the role may do on its main
thread. That list is in the harness's own code rather than in any file, there is
no configuration key that names a capability, and no value of `conversations`
reaches it. Setting this key gives an agent a second thread; it gives that thread
nothing to act with.

**What a side thread promises is best effort until the main thread confirms it.**
A side thread concludes by finishing or by spending its turn budget, and what it
reached is written into the agent's own memory — budgeted, redacted, audited,
citing the side stream rather than copying its transcript. The main thread's next
turn reads that and ratifies or adjusts whatever the side thread drafted, through
its own single-threaded path, which is the only path there is. So an answer that
promised scheduling is tentative, and the surface carrying it says so.

**The bounds are the harness's, not yours.** A side thread runs to a turn cap and
an agent holds a limited number at once; both are the harness's defaults and
neither is configurable here yet. A side turn is otherwise a provider invocation
like any other: the spending pause and your holds gate it, it is priced from what
the provider reported and listed beside the conversations in the cost surfaces,
and it is served by the agent's [permitted alternate](#serving-a-turn-from-a-permitted-alternate-model)
and [pinned version](#pinning-an-agent-to-a-model-version) exactly as a main turn
is.

## Research sources

The Lead Product Manager can have the harness find something out for it, so an idea
you bring it is evaluated against evidence rather than against what a model
remembers. **The capability is off until you name a source**, and a project that
names none has a Lead Product Manager that says it could not check rather than
answering from memory as though it had.

```yaml
research:
  max_queries_per_turn: 4    # how many questions one reply may set off
  timeout: 60s               # how long one source has to answer
  sources:
    - name: web              # what the role names, and what every record cites
      command: my-search     # run with the question on standard input
      describes: public web search, no login
```

**A source is a command you wrote.** The harness runs it with the question on
standard input and reads its standard output as the evidence — nothing else is
passed, and the question is never part of a command line the shell parses. That
is the same arrangement `checks` uses, and for the same reason: what the harness
may run is a thing you write down in the file you write everything else in, so
what it can reach is exactly what you named. There is no built-in provider and no
default source, deliberately. A conversational role reaching the network is
something you turn on, not something you acquire by extending a bundle or
upgrading the executable.

**The role still has no network.** It names a question and one of these sources;
it does not choose what runs, where the command reaches, or how often. Only the
question leaves your machine, redacted with the same values every other
provider-facing path is redacted with and bounded at 512 bytes — generous for a
sentence somebody would type into a search box, and far too small to carry a
document out inside one. Which sources exist is delivered to the role with each
turn rather than written into its contract, so a source you add or remove is in
force on the next thing you say.

**What comes back is untrusted.** It is delivered framed as evidence about the
world and never as instruction, exactly as your repository documents and your
work items already are, and it is bounded at 4KB per answer with any cut
declared. A source that fails, times out, or answers with nothing produces a
finding that says so rather than silence — a role that gets silence for an answer
concludes there was nothing to find, which is the one conclusion it must never
draw from a source that broke. Every question and what it returned is printed to
you as it happens.

**The bounds are yours and the protocol has its own.** `max_queries_per_turn`
narrows how many questions one reply may ask and cannot widen it past four, which
is what the block itself permits; `timeout` is per question. Both take a harness
default when you leave them out, so naming a source is enough to have the
capability rather than something you configure twice. Zero is a choice for each —
it takes the default — and a negative number is refused. One further bound is the
harness's rather than yours: one thing you say sets off at most two rounds of
gathering, so a message cannot spend itself searching its way around a question.

What the Lead Product Manager does with the evidence is an evaluation, which is
advice and nothing else: recording one admits no work, changes no document, and
approves nothing. That path, and how to read the evaluations back, is described
in [the conversation guide](conversation.md#bringing-it-an-idea-rather-than-a-work-item).

## Reading the repository from a conversation

The three management roles — Lead Product Manager, architect, development manager —
and the program manager can have the harness read one repository path for them, or list the names one
directory holds, at a recorded commit. It is here beside research because it is
the same shape and the opposite arrangement: research is evidence from outside
the repository, run by a command you wrote and off until you name one; this is
evidence from inside it, run by the harness's own Git, and **there is nothing to
configure**. No key switches it on, none switches it off, and none moves a
bound. [The conversation guide](conversation.md#reading-the-repository-at-a-recorded-commit)
says how a role uses it; what belongs here is why the file you are reading has
no say in it.

**Which roles hold it is the role-capability registry's, in Go.** The three
management bundles and the program manager's hold `repository.read` and
`repository.list`; the developer's
and the reviewer's hold `repository.read` alone, which is the harness reading a
change or a context bundle on their behalf rather than a path they name. `yoyo
config show` reports both under each agent's `capabilities`, and — as with every
capability — the set is read off the role and never written: a `capabilities`
key in this file is refused like any other key that does not exist. A persona
cannot widen it either, because the block is refused where the reply is read
whatever the persona said.

**The bounds are the protocol's rather than yours**, for the reason
`max_queries_per_turn` cannot be raised past four: what is bounded is the size
of a prompt. One reply names at most six paths; one read returns at most 48 KiB
of a file and one reply's reads together at most 96 KiB, a file beyond that
being cut with the cut declared and its whole size named rather than split
across reads; a listing returns at most 400 names; one message reads at most
twice. A `research`-style block for it would be a bound a project could
configure past, which is the thing this section exists to say there is not.

**Every read is against the tree of the commit `HEAD` names at that moment**, in
the repository this configuration's `product.repository` resolves to — the
primary checkout, never a worktree — and never the working tree, so an edit you
have not committed is not what a role is shown. That is also what makes the read
confined without a check: a committed tree has no link to follow and no path
that leaves it. The content is redacted with the same values every other
provider-facing path is redacted with, and each read is recorded on the
conversation as the commit, the path, and the time.

**What the Lead Product Manager is handed is labelled as description, never intent**
— the same label its [shipped documentation](#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not)
carries, applied on every delivery, with the same rule: where a file contradicts
a specification, the conflict is reported rather than resolved. The
specifications remain the only statement of what the product is for.

## Triage thresholds

Triage is what looks at work that has stopped moving. Its numbers are
configuration rather than constants, because each one is a judgement about a
project's pace — how long a merge may take before nobody merging it is news, how
many times one change may go round with a reviewer before going round again is
the problem:

```yaml
triage:
  stuck_merge_age: 2h        # how long an approved publication may sit unmerged
  review_rounds_cap: 4       # total review rounds one item may accumulate
  repair_grant_attempts: 2   # what a grant is worth, when triage grants one
```

**These are read by the triage docket**, which is where work that has stopped
moving — and work that never started, because the tree does not meet a
prerequisite the item states — is collected and delivered to the development
manager: `stuck_merge_age`
decides when an unmerged publication is docketed, and the item's budgets —
rounds spent, repair grants, re-runs, merge re-arms — are carried on every
entry so a decision about one is made against what the item is allowed to
spend rather than against the evidence alone. Deciding what becomes
of a docketed item is the development manager's; the caps are what refuse a
decision that would spend more than the item is allowed.

**The docket reads the record the guards enforce, not a count of its own.** Every
figure on an entry — the rounds spent, each decision recorded, and each cap
beside it — comes from the [per-item counters](#what-one-work-item-has-been-given)
a decision spends, and the re-runs already carried out come from the per-stoppage
re-run records under `<state root>/projects/<product id>/state/reruns/`. It
is read as the docket is read rather than written into the entry: the entry is
recorded once as the work stops and every decision about it is made afterwards,
so an entry frozen at docket time could only ever show every decision as absent.
That was the defect — a re-run decided and durably recorded, a resubmission
refused as one of one re-runs spent, and a docket showing nothing decided, which
nearly had one authorized recovery spent twice. An item whose record cannot be
read says so on its entries instead of rendering as an item nobody has decided
anything about.

The docket is built when something scans: `yoyo reconcile`, the moment a
development manager conversation opens, and every firing of a
[recurring task](#recurring-tasks) of hers, which carries the docket in the
message that wakes her — and beside it the "Needs a human" entries whose move is
the operator's, each with its age
([operations](operations.md#reading-what-the-recurring-tasks-found)). Only that last is scheduled, and only where a project
configures one, so `stuck_merge_age` is a floor rather than a promise — a
publication becomes docketable at that age and is docketed the next time one of
those happens.

**All three are read.** The docket above consumes `stuck_merge_age` — an approved
publication older than it is docketed at the next scan — and `review_rounds_cap`
bounds the [per-item counters](#what-one-work-item-has-been-given) below, which
every run writes to and `yoyo status <id>` reports. The development manager's
triage decisions spend them: a decision of `repair` takes a grant of
`repair_grant_attempts` rounds truncated to what the cap has room for, and
`rerun` and `rearm` each spend a budget of their own. Every one of the three has
a budget nothing else spends, and is refused once that budget is gone; the two
that buy review rounds — a repair grant and a re-run — are bounded again by the
round cap, which they share with each other and with every run of the item. The
[table below](#what-one-work-item-has-been-given) says which bound refuses
which.

Recording a decision and carrying it out are two steps, and three of the eight
decisions have an action for the second. Two of them are the opposite answers to
a run that stopped: `yoyo triage rerun` starts the item over, and `yoyo triage
repair` continues the run that stopped on the change it already has. The third,
`yoyo triage rearm`, is about a publication rather than a run: it repeats the
merge request the forge dropped. A fourth action carries out no decision:
`yoyo triage resume` promotes an approved change the environment stopped short
of the target branch, spending nothing and asking nobody, because nothing about
such a stop is a verdict —
[the conversation guide](conversation.md#resuming-an-approved-change-the-environment-stopped)
says what it records and what refuses it.

`yoyo triage rerun <run-id>` starts a fresh run of the item whose stopped run the
docket entry names. It takes the run and nothing else: the decision it carries
out and the reasoning it records come from the item's durable triage record
rather than from a flag. It is refused unless that run is terminally recorded and
still standing on whichever of the two docketed it — its blocker, or, for a run
that died before anything recorded one, the change it left behind — read from the
run's own record rather than from the
docket entry — and one docketed stoppage is re-run once, whatever the item's
budget still says. It is also refused unless a re-run decision about this
stoppage stands on that record, and names what is missing when none does. The
decision spends the item's re-run budget in the same write and authorizes exactly
one re-run, so what has already been claimed is read back against what was
decided. A stoppage nothing was decided about is refused, and so is one decided
otherwise; an item whose decisions have all been carried out is refused too. A
second stoppage needs a second decision — the counter is a total nothing clears,
so neither it nor a decision about the first stoppage says anything about this
one — which past the once-per-item cap is an escalation rather than a larger
budget. It is refused, finally, unless the work item itself is one
a run may start on — open or blocked, with nothing it depends on outstanding.
Blocked is deliberately among them: stopping the run blocked the item, and a
status written when work stops and never rewritten when what stopped it clears is
not something to refuse a decision over, so re-entry supersedes the standing
blocker rather than waiting for somebody to remember to reopen the item. What
still refuses is unfinished work the item waits for, and an item that has left
the backlog. The intake hold applies too, because the harness is the one
choosing the work; a re-run under a hold starts nothing and claims nothing, so the
stoppage keeps its re-run for after the hold is lifted. A
[step only a person can take](work.md#letting-the-harness-choose-the-work) that
the item declares and nobody has recorded applies for the same reason and in the
same way: the re-run is refused before the claim, in the words the queue holds
the item with, and the stoppage keeps its re-run for after the act is recorded —
the development manager deciding a re-run is not the operator taking the step the
item reserved for them. The fresh run records
the development manager as having chosen it, cites the decision it read that from
— whose, which conversation, which turn — and carries the reasoning recorded with
it, which is what `selected-work-passes-intake-and-records-why` asks of anything
the harness chooses, made checkable.

**Every one of those refusals is made before the stoppage's re-run is claimed**,
and the claim is what spends it. A condition asked after the claim would spend
the budget on refusing to use it: the item's status is the one that showed this,
where a re-run of a blocked item was refused for the status and the next attempt
was then refused by the once-only guard, for a run that had never happened. So a
refused re-run leaves the stoppage its re-run and says so, along with what would
make it stop refusing, and asking again once that is true carries out the same
decision. The opposite order holds past the claim, deliberately: the claim is
taken before the run is started, so a process that dies between the two has spent
a re-run nobody took rather than taken one nobody recorded.

**A refusal the fresh run meets past the claim gives the claim back**, because
the pipeline asks the item's state, the repository's and the provider's again
where it would start, and any of them can have changed since this action asked
its own. A refusal made there reserved no run, so it claimed no work item, cut no
worktree and invoked no agent, and what says which side of the reservation it
stopped on is whether the outcome names a run at all. So the claim goes back, the
refusal says so, and asking again once it no longer refuses carries out the same
decision. A refusal that does name a run is a run that existed and did something:
its claim stands and is settled like any other.

**A full harness is a state rather than a refusal.** `execution.max_concurrent_developers`
is read before the claim, from the same runs in flight the reservation counts, and
every slot being taken neither refuses the carry-out nor fails it: nothing is
claimed, the decision stands until it is carried out or the development manager
withdraws it, and asking again once a slot frees carries out the same one. The
item is meanwhile the open work the scheduler pulls from, which is the other way
it reaches a developer, and the two agree because this claimed nothing. The last
slot can also go between that reading and the reservation; a claim taken for a
run the reservation then refused for capacity is **given back**, because that run
provably never started — a reservation refused for capacity creates no run
record, claims no work item and runs no agent. A claim carrying a run is refused
rather than removed. A withdrawal the harness could not write is reported rather
than swallowed: the stoppage has then spent its re-run on a run that never
started, which is a thing to go and correct.

**A pause the fresh run meets gives the claim back too**, and for the same
reason. The operator's hold on all activity, an unresolved directive, work the
item waits on, and a held intake are all read where the run would start, which is
past the claim, and each of them stops the pipeline before it reserves a run,
claims the item, or invokes an agent. So a paused outcome carrying no run is that
same nothing: the claim is given back, the pause is reported in place of the run,
and lifting it and asking again carries out the same decision — rather than
meeting the once-only guard for a run nobody ever made. Two of the four are read
before the claim as well, which is not the same question twice: that reading
keeps a harness already held from spending anything, and this covers one that
arrives while the claim is being taken.

**A fresh run the environment refused before any agent of it ran gives the claim
back as well**, and it is the one give-back on the far side of the reservation. A
worktree whose checkout the harness's own budget ended is the case it was built
for: the run was reserved and its record says what stopped it, but no developer
was invoked and no change was delivered, so the claim bought nothing. Both halves
come off the run's own record rather than from anybody's word for it — the
refusing site writes that nothing of the round ran, because it is the only thing
that can know, and the run's settle marks the round refused only once it has
proved the round delivered nothing. Every other claim carrying a run stands and
is settled like any other, whatever became of that run, including one whose round
the settle could not classify: a claim given back twice is one decision starting
two runs.

The re-run is recorded beside the counters, one file per docketed stoppage at
`<state root>/projects/<product id>/state/reruns/`, and it carries what the stopped
run preserved. Its branch and worktree are **kept** while the fresh run has not
integrated — that is what a development manager's guidance points at when it says
what to cherry-pick — and **retired** explicitly once it has. Anything that could
not be retired stays kept with the reason recorded: a worktree holding
uncommitted work and a branch whose work nothing promoted are both left exactly
where they are, because nothing else records what they hold. Nothing automated
deletes the record, for the reason nothing deletes a counter file — save the
withdrawals above, which remove a claim that provably bought nothing: one whose
fresh run the pipeline answered before it reserved anything, and one whose fresh
run the environment refused before any agent of it ran.

A retirement is written onto the stopped run itself as well, under that run's own
lease, because its record is what `yoyo status` and the docket read to say
whether its branch and worktree are still there. A stopped run promoted nothing,
so the removal names the run that superseded it — `artifacts_retired_by` on the
run's state — which is the second way a recorded removal is earned beside a
promotion of the run's own. The third is `worktree_swept_at`, written by the
[convergence sweep](operations.md#recovering-interrupted-runs) when it retires
an old stoppage's checkout to keep a machine's worktree registrations bounded;
it earns the checkout alone. Where that checkout held uncommitted work,
`preserved_work_ref` names the ref it was recorded on first — the run's record is
the only place that connects the ref to the item it belonged to, and the sweep
writes the ref onto the work item too, so the person picking that item up has a
route to the work rather than only the failed run's own note naming a directory
that is gone. The fourth is `branch_swept_at`, written by the same sweep when it
deletes a branch whose work the target provably carries; it earns the branch
alone, and it is recorded because `Preserved()` asks `branch_removed` and nothing
else — a deletion nothing wrote down leaves the run advertising a branch that is
not there. A
retirement the harness could not write onto that run is reported rather than
swallowed: the artifacts are gone and its record still says otherwise, which is
a thing to go and correct.

The pull request the stopped run published is the third artifact, and it is
retired at the same moment: once the fresh run has integrated, that request
carries work that landed by another vehicle and will never merge, so it is closed
with a comment naming that vehicle and the remote branch it published is
deleted. The run's own record keeps which vehicle retired it — `superseded` on
its `pull_request` — which is what stops the
[convergence sweep](operations.md#recovering-interrupted-runs) asking the forge
about a request that is already closed. That sweep closes the same requests for
every stopped run nothing triaged, so a project wired without forge access here
loses timeliness rather than the cleanup. The fresh run integrating nothing
retires nothing: the request stays open, as pending work on a preserved branch.
A promoted run's publication that nothing asked the forge to merge, handed back
by the re-run, is retired the same way once the fresh run lands — its record then
carries both `handed_back` and `superseded`.

`yoyo triage repair <run-id>` is the other half of the same pair, and it starts
nothing over. It re-enters the stopped run's
own repair loop: the same branch, the same worktree, the same developer session,
and the reviewer's findings handed back exactly as they were written. A run
whose record holds no developer session — its session's budget ran out before
the provider reported one, the provider never returned one, or the harness
carried the run on itself at a step with no developer — is not refused for it:
the repair starts a fresh developer session in the same worktree on the same
change, hands it the failure, the run's record, and the work item, and spends
the grant exactly as re-entering a session would. The run's record says the
repair started a fresh session and why. A stall or a check-stage continuation
still needs the session it stopped in. A developer attempt whose provider
reports no session keeps the session the run already held on its record, so a
run the harness continues past a silent session does not lose it.

**What it may hand the run is the grant the development manager already
recorded**, and it spends nothing of its own. Deciding `repair` is what takes the
item's grant — `repair_grant_attempts` rounds, truncated there to what the round
cap had room for — so this reads that record for how many attempts it is worth
and hands the run exactly that. Like a re-run, it takes the run and nothing else:
the decision it carries out and the reasoning the run and the item record come
from the durable triage record of the item that run was made for, and the
account it writes cites the conversation and the turn the decision was recorded
on. A stoppage with no repair decision standing about it on that record is
refused naming the record that is missing — which is also what a repair
recorded about some other run meets, so a decision is only ever carried out
against the run it names — and a run whose own record names a different item
from its docket entry is refused naming both. An item whose grant the harness
has already carried out is refused too, which it counts from the continuations
the item's runs record. Past the once-per-item cap a second is an escalation rather than a larger
budget, and an item with no rounds left never gets a grant to carry out at all.

Five more things refuse it. The stopped run has to be really over, terminal and
still standing on whichever of the two docketed it, read from the run's own
record rather than from the docket
entry. The run has to have recorded a repair input — a replay conflict is one,
recorded on the run before it stops — or be a stall; a run whose provider kept
refusing never had a failure returned to its developer and has no attempt to
carry on with, and a re-run is what it needs. A stall is continued rather than re-run: the harness is what stopped
it, before anything judged the work, so what it is owed is the attempt it was
stopped in, resumed in the session it stalled in — and the continuation counts
no review round and no repair attempt, because a stall judges nothing. A run
whose record says its approved change conflicted on replay is continued like
any other repair input (yoyodyne-ifd.132): the change is moved onto the target
first, with the conflict left in the worktree, and the same developer session is
handed it to reconcile. The
preserved worktree has
to be as the harness left it: what a continued developer is handed back is
whatever is in that worktree, so a HEAD that moved — an operator mid-surgery, an
agent that committed — is a person's to decide about, and the refusal leaves the
item blocked and says so. And that worktree has to still hold the change: a
checkout the harness would call its own and that holds nothing passes the gate
above and fails this one, and a developer handed the reviewer's findings and an
empty directory delivers an empty repair or reinvents the change from them, with
nothing in the run's record afterwards to tell either from a repair that went
well. A stall is not held to that last one, for the reason the resumed run below
exempts the same case: nothing was handed back to be about a change, and an
empty worktree is what the attempt it is owed starts from.
And the decision standing about the stoppage has to still be the repair:
one decision stands per stopped run, and a re-run, an escalation, or a wait
recorded in the repair's place released the rounds the repair had reserved (see
[what spends a round and what does not](#what-spends-a-round-and-what-does-not)),
so a repair carried out on that run afterwards would spend attempts the item's
record no longer holds room for, on a decision nobody holds any more. The intake
hold applies for the reason it applies to a re-run: this spends on a provider,
and the development manager naming the item is not the operator naming it.

**The resumed run asks the same question again**, and blocks rather than
spending where the answer has changed. That is the enforcement rather than a
second opinion: it binds every route into a run that continues a change — this
action, an interrupted process a later invocation picks up, and whatever
re-entry is built next — because a worktree that lost its change looks exactly
like a valid one and is caught by nothing else. It is asked of every resume whose
worktree is supposed to hold a change already, which is two different cases: a
run picked up inside its repair loop, which carries a failure returned about a
change it made, and a run picked up at the checks or at the review, which has
completed a developer attempt and has nothing else for those steps to judge. Only
the run owed its first attempt is exempt, because an empty worktree is what that
attempt starts from. The blocker it records names the run's branch as where the
preserved work is, and says plainly that nothing was developed, checked, or
reviewed, because the empty diff behind it is not a verdict on the change.

**A fresh run is refused where a repair is owed**, which is the same loss
arriving by the opposite route: nothing is handed back at all, and a run starts
clean on an item whose last run stopped with its change preserved. The fresh
worktree is a perfectly valid one off the target branch, so nothing downstream
notices; what makes it visible is the stopped run's own record — terminal, its
blocker standing, a failure returned to its developer, and a branch that still
carries the change. So `yoyo run` reads that record before it reserves anything,
and refuses, naming both the repair that would continue the change and the re-run
that would deliberately start over. The one fresh run of such an item that is
right is the re-run, and it says so in the record before it starts: triage claims
it against the stoppage, and a claim naming that run is what lets the fresh run
through. Nothing is reserved, claimed, or created by the refusal, so carrying out
either decision afterwards costs the stoppage nothing.

**And the repair itself is dispatched to the run it is about**, which is the
other end of the same loss. Every recorded instance of a repair round going
missing was a dispatch that started something fresh instead — the scheduler
pulling the item off the backlog, or somebody naming it with `yoyo run` — so a
carry-out that only named the item was one a fresh run satisfied. It names the
run as well, and the entry point it names it to re-enters that run or refuses:
it reserves nothing, claims nothing, and creates no worktree, whatever it finds.
A dispatch that finds no run in flight, or a different run of the same item,
says which of the two it found and stops there, leaving the stoppage and its
branch as they were. The refusal in `yoyo run` above is then the backstop for
the routes that never carried repair intent at all, rather than the only thing
standing between a repair and a clean worktree.

**A repair supersedes the blocker rather than needing somebody to remember to.**
The run that stopped blocked its item and recorded the blocker on its own state,
which `yoyo status`, `yoyo reconcile`, and the docket all read as the fact that
it has stopped. So re-entry clears both at the moment it happens: the item is put
back with the decision recorded on it first, and the run's blocker is cleared onto
the continuation that supersedes it, which keeps the words it was recorded in and
the grant that bought the attempt. The order is the item first, because a run
recorded as running behind an item that still says it is blocked is the one
half-finished state nothing else here would notice. The note before the claim
describes preparation, and the success note is appended only after the run's
continuation has been saved and read back. A refused claim or an unconfirmed save
reports that outcome and dispatches no developer. A save that replaced the record
but failed to confirm its durability is reported as uncertain, even when the
continuation can be read back.

Recovery reads the item and run before making another transition. It reuses an
item already claimed and confirms a continuation already recorded for the
standing repair decision without adding another continuation, attempt, or grant.
The recorded continuation carries `dispatch_pending` until the pipeline confirms
that it adopted and accepted the named run. A pause before adoption can name the
existing run without accepting it, so its dispatch remains pending. Later
scheduling passes offer a pending continuation whose run has no live lease
holder, even though it has consumed its grant and still occupies a developer
slot. Recovery confirms durability and reuses that slot before dispatching; it
takes no new slot or grant. A live leased run is left to finish, and an accepted
dispatch is not offered again. A repeated repair command reads and reports an
already served continuation without another continuation or dispatch. Success
notes carry a separate `success_note_pending` marker on their continuation.
Accepted dispatch and a completed run leave an unconfirmed note pending: later
pulls retry note delivery without taking a developer slot, making a new
continuation, or charging another attempt. Recovery checks the item's existing
notes before appending a missing success note or recognizing one whose response
was lost, then records its confirmation. It leaves live leased runs alone.
Once the continuation is confirmed, a failed note is reported alongside it
rather than making a recorded continuation look absent.

The continuations are recorded on the run itself, under `repair_continuations` in
its state file, and they are what the continued run's repair loop adds to
`execution.repair_attempts_before_replan` to know what it may spend. They are
also how the harness knows what a grant has already bought: summed across an
item's runs, they are what a second re-entry is refused against.

`yoyo triage rearm <run-id> --reason "<the recorded decision>"` is the third
carry-out, and it is about the other thing that stops: an approved change
published to a forge that queued its merge and then dropped it. **It repeats
exactly the request the reviewer's verdict authorized** — the same pull request,
by the method that verdict's own merge recorded rather than one this action picks,
pinned to the commit that was integrated — and it overrides nothing to do it.
Administrator privileges are never used, and repeating an identical request is
not merging past a requirement: the forge's requirement machinery runs again in
full, which is the whole reason the design permits it.

**It is bounded per publication rather than per item**, at one. The development
manager's decision spends that publication's budget as it is recorded, and what
says the decision has been acted on is the publication's own durable counter, on
the run's record, written before the request is made. A publication with as many
re-arms made as decided has had everything triage decided about it carried out,
so a second drop of it is refused here and recorded as an escalation on the item:
the blocker the sweep leaves for a second drop says so in as many words. The
decision itself is read back too, as the re-run's is: one decision stands per
stopped run, so a re-arm the development manager recorded and then decided to
escalate instead leaves the budget spent and the standing decision an escalation,
and a re-arm is refused on that record rather than carried out on a decision
nobody holds any more. The account it reports cites the conversation and the
turn the decision was recorded on; the `--reason` is the words given to this
command, attributed to that rather than to the role.

**What refuses it, all of it asked before anything is spent.** The forge's own
merge state has to name nothing only a person can satisfy — a conflict with the
base branch, a request still in draft, a base branch that has moved ahead, a
protection rule the request does not meet — and a state that could not be read
refuses too, because this is a gate. The request has to be unchanged: its head is
still the commit the run integrated, and the remote target still passes the same
pre-merge content check the original gate ran. And the work has to be settled —
the run that made the publication terminally recorded, and no run of the item in
flight — which is the precondition a live incident bought, where a publication
re-armed under a live run left a hand-written amendment stranded on a preserved
branch. The intake hold does not apply, because a re-arm chooses no work: it
repeats a merge request an approving verdict already authorized, for a change
that already passed every gate. On an unprotected target that change is already
integrated locally; on a protected one it is on its pull request and nowhere
else ([a protected target lands through its pull request](#a-protected-target-lands-through-its-pull-request)), and the re-arm
is how it lands — either way, nothing new is selected.

It takes the target branch's promotion lease before it asks the forge for
anything, so it queues behind whatever is promoting into that branch now — a
re-arm is an integration retry against the target branch, and
`one-promotion-per-target-branch` binds it as it binds the promotion itself. The
lease covers the forge reads and the pre-merge check as well as the merge, and
not the merge alone: that check on the remote target is the evidence the repeated
request stands on, and a promotion admitted between the check and the merge
invalidates it. Everything answerable from the harness's own records — nothing
docketed, a live run, no decision left to carry out — refuses in front of the
lease, so an ordinary refusal holds up no promotion. A merge the forge queues
again puts the run back where `yoyo reconcile` settles it, and clears the run's
own record of the drop — the publication failure and the blocker that said the
forge had let the merge go — so `yoyo status` stops describing a publication that
needs a person while the forge is holding the merge again. The work item's
blocker is the sweep's to settle on what the forge does next: a merge closes the
item, and a second drop writes a fresh one there.

The other four decisions still carry themselves out no further than the record:
a re-scope, a wait, and an escalation ask for no action at all, and a crossing
moves a cap and asks for nothing either — what it makes recordable is the
decision it was for, and that decision is carried out as above. The budget is
spent when the decision is recorded, which is the same order every counter here
is written in — an attempt nobody took rather than one nobody counted — so a
decision nobody acts on has still cost the item its budget.

`stuck_merge_age` is how long an approved publication may sit unmerged before it
is docketed. It is an age rather than a deadline because what makes a
publication stuck is that nothing has happened to it, and nothing happening
offers no event to hang a deadline on. It must be positive: an age of no time at
all dockets every publication the instant it is made, which is a docket of
everything and a triage of nothing.

It is also how long a decision to `wait` leaves that entry alone, whether the
entry is a publication or a stopped run. Waiting says "not yet" rather than
deciding anything, so the entry comes back once it has been sitting there this
long again — otherwise a merge or a stopped run nothing is happening to would
disappear on the strength of a decision to look at it later, since nothing about
it will ever change to bring it back. Until then the development manager's
docket still lists it, after every entry nobody has decided, saying until when.

`review_rounds_cap` bounds the review rounds one work item may accumulate in
total — across repairs, across runs — past which triage may no longer hand it
back for another repair. Past the cap triage still has three things it may do:
escalate the item, re-scope it, or cross the cap. `0` is a choice somebody can
mean and is accepted as one: an item that reaches triage at all is never
repaired again without a crossing. What crosses it for a single item is
[a recorded crossing](#crossing-a-cap-the-operator-decides-to-cross): the operator's
own override, to any ceiling, or the development manager's, far enough for the
one decision that was refused and five times per item — which is what makes an
escalation answerable rather than only
sayable, and what stops most of them being needed.

`repair_grant_attempts` is how many repair attempts triage hands an item when it
decides the work is worth another go. Leave it out and it follows
`execution.repair_attempts_before_replan`, tracking that budget rather than
copying it: raise the budget and the grant rises with it. It may not be zero,
because a grant of nothing leaves the item exactly where granting nothing would
have. A project that configured no routine repair attempts at all still gets a
derived grant of 1 rather than a configuration that fails to load — the grant is
triage's deliberate exception to that budget, not another helping of it.

### What one work item has been given

The thresholds above bound something, and what they bound is a durable record
per work item: the repair grants triage has given it, the re-runs it has caused,
the merge re-arms it has made — with what each publication of the item has had of
them — and the **review rounds** the item has cost across every run of it. It lives beside the runs under the state directory, one file per
item, and it outlives them — a run is settled and its worktree and branch are
removed, and what the item has been given is still there.

That it is not on a run is the whole point. Every budget a run spends starts
again at zero in the next run, so an item handed back, run again, and handed back
again is an item nothing was bounding. `yoyo status <id>` reports it under that
item's runs, in text and in `--json`:

```text
triage of yoyodyne-ifd.90: triage has spent 2 passes on it
  review rounds: 3 spent across every run of this item, under the cap of 4
```

At or past the cap — 4 of 4 exactly included, because a grant needs a round and
none remains — the same line reads: `review rounds: 6 spent across every run of
this item — at or past the cap of 4, so no decision that buys a round remains`,
and the line under it names the one thing that changes that, so the dead end and
the way out are read in the same breath:

```text
    `yoyo triage override --budget "review round" --cap <n> --by "<you>" --reason "<why>" yoyodyne-ifd.90` crosses it to any ceiling; the development manager may also cross it far enough for one more decision, 5 times per item, and each of those reaches you in the channel as it happens
```

What may still happen without crossing anything is what the budget lines beside
it say: waiting, re-scoping, and escalating spend nothing, and a merge re-arm
spends only its own budget, whatever the rounds say.

```text
  repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains
  merge re-arms: 1 across every publication of this item, 1 permitted per publication
    publication:run-a#92: 1 of 1 permitted
  1 grant(s) were cut down to the rounds the cap still had room for; 1 round(s) were granted in total
  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say
```

The re-arm line is the one that reads differently, because its ceiling is not
the item's. A re-arm repeats one already-authorized merge request, so it is
bounded per publication: the count is the item's total across every publication
it has made, the cap is what one publication may spend, and each publication
that has spent any of it is named beneath. An item that published three times has
three separate budgets, and one publication being out says nothing about the
others.

**The first line counts what has been spent, not how many times triage looked.**
Three of the development manager's eight decisions spend a budget here — a
repair grant, a re-run, a merge re-arm — and `wait`, `rescope`, `escalate`, and
`retire-raise` cost nothing and reach no counter, so an item that was escalated reads `triage
has spent nothing on it`. A `cross` reaches no counter here either: it moves a
cap rather than spending one, and where it is reported is the crossing lines
under these budgets. Whether stopped work has been decided, and what was
decided, is recorded on the work item itself — and on the docket entry, which
every decision about a stoppage closes, so a stoppage settled without spending
anything still leaves the docket rather than being put to the development
manager again. A `cross` closes nothing, because it settles nothing: the entry
stays until the decision it made recordable is recorded.

**This is also what the docket reports.** Every entry for an item carries these
counters and these caps, read as the docket is read, so what `yoyo status` says
about an item, what a docket entry says about it, and what refuses the next
decision about it are one record rather than three counts of it.

**A round is a reviewer verdict that sent a developer attempt back**, counted
across every run of the item. A re-review no developer attempt produced is not
one, so a promotion that [loses its race](#losing-a-race-for-the-target-branch)
and gets a fresh verdict on the replayed change is not charged for it — counting
that would charge an item for losing a race it did not cause. A review re-asked
for after an interrupted process is the same case and is counted once for the
same reason. Rounds are recorded whatever a cap says, because a round is
something that happened rather than something being asked for.

**A verdict that approved the change is not a round either.** The cap stops an
item buying the same argument another round, and an approval ends that argument
rather than taking another turn of it; what becomes of an approved change
afterwards — a promotion that conflicted, a merge the forge dropped — is not the
change disputing with its reviewer. Charging it walked items toward the cap on
their own success, and one such item reached triage with every recorded decision
about it refused.

**Neither is a repair whose whole residue is one out-of-scope finding.** The
reviewer said the work is right and named one thing beside it that is not this
change's to do — outside what the item asked for, or too trivial to hold the
change for — which is the same ending with a note attached rather than another
turn of the argument. The reviewer says so with the finding's `disposition`,
`out_of_scope`, which is separate from its severity: `minor` says how serious a
problem is, and the disposition says whether this change has to fix it. The
budget reads the disposition and never the severity, so one minor finding with
no disposition is charged like any repair; reading the severity made a real
defect labelled minor a free round (yoyodyne-ifd.359). One out-of-scope finding
is the whole of the rule: two notes is a list and a list is the reviewer still
arguing. The work still goes back to the developer and the run still spends an
attempt on it — what changes is the item's bill, not what happens to the
change. This is the operator's direction of 2026-09-05, after four items in a
week reached their caps on rounds of exactly these two shapes and each took a
person to unstick.

**It loosens no bound**, and what holds that is other budgets rather than the
rounds. An approval sends the change to promotion rather than back to the
developer, so the only thing that asks for another verdict inside the same run is
a promotion that lost its race and replayed. A replay that passes spends no
budget, so those approvals are bounded by how often other work lands on the
target rather than by a number: each one is a race another promotion caused,
and `execution.integration_retries_before_reconciliation` bounds the replays
that stop on the change instead. A trivial residue
does send the change back, and `execution.repair_attempts_before_replan` bounds
that: an attempt is spent by the attempt, whether or not the verdict that asked
for it cost a round. How many runs an item gets is bounded in turn by the repair
grant and the re-run, each once per item.
None of those budgets is the round cap and none can be spent through it,
which is what stops the exclusions turning into a budget nothing bounds.

**An uncharged verdict is recorded without being counted**, and that is what
keeps the replay exclusion above true rather than nearly true. Both exclusions are
one mechanism: an attempt a reviewer has already answered about is charged at
most once, whichever way either answer went. A promotion only follows an
approval, so an approval nothing recorded would leave the replayed attempt
looking unjudged — and the replay's fresh verdict can be a repair, because the
ground moved — which is the race charged to the item after all.

**Each counter is written before the action it counts takes effect**, so a
process that dies between the two has recorded a grant it did not give rather
than given one it did not record — an unspent attempt rather than a duplicated
one. **The decision that authorized the spend is written in the same
update** — the word, the stoppage, the reasoning verbatim, and where it was
recorded — so a record cannot say a budget went without saying what was decided.
That is what the actions carrying a decision out read, rather than words from
whoever ran the command. Decisions that spend nothing are recorded too, and one
stands per stopped run. Concurrent updates are serialized per item, so no increment is lost, and a
record that cannot be read is a refusal rather than an empty budget: an
unreadable budget read as empty is every cap in it stopping to mean anything.
Recovery from one is a decision, not a repair: the record is one JSON file per
item at `<state root>/projects/<product id>/state/triage/`, named by a slugged
rendering of the item id with a digest suffix (so a listing reads which item
each file belongs to, and two ids that render alike still get their own files
— match on the slug). Read it and fix what is malformed if the history is
worth keeping — or delete it, which resets every budget the item had spent,
and is therefore a deliberate re-budgeting to record on the work item in the
same breath, not a cleanup. Nothing automated deletes one, for exactly the
reason nothing reads one as empty.

Which threshold refuses which action:

| Action | Refused by |
| --- | --- |
| another repair grant | one per item, and `triage.review_rounds_cap`, truncated to the rounds it still has room for — one precondition among several: the decision recorded here spends the budget, and `yoyo triage repair` re-enters the stopped run's repair loop on it, which is a claim the harness makes rather than the operator, so `selected-work-passes-intake-and-records-why` also requires the intake hold consulted before the run is continued and the reasoning recorded in the run's durable state. That action is bounded again by what the grant has already bought, read back from the continuations the item's runs record, and it refuses a preserved worktree that is not as the harness left it and a run whose standing decision is no longer a repair |
| another whole run of the item | one per item, and `triage.review_rounds_cap`, refused outright once none remain — one precondition among several: the invariant `selected-work-passes-intake-and-records-why` also requires the intake hold consulted before the claim and the selection reason recorded in the run's durable state. The decision recorded here spends the budget and is written beside it, naming the stoppage and where it was recorded; `yoyo triage rerun` starts the run, is bounded again by one re-run per docketed stoppage, and reads that decision back — with this counter, against the re-runs already claimed — as the proof that one is there to carry out and as the words its attribution is built from |
| re-arming a merge the forge dropped | one per **publication**, not per item — a re-arm repeats one already-authorized merge request, so an item that published three times has three separate budgets. One precondition among several: a re-arm is an integration retry against the target branch, so `one-promotion-per-target-branch` binds `yoyo triage rearm`, which takes the target branch's promotion lease and repeats only the identical already-authorized request. The decision recorded here spends that publication's budget and is written beside it, naming the run whose publication it is about; the action reads both back — the budget against the re-arms the publication's own record says the harness has made, and the decision as the one still standing about that run — as the proof that a decision is there to carry out, and a second drop is an escalation rather than another re-arm |

The first two buy review rounds, so the round cap bounds them, and a grant is
**truncated** rather than refused where some rounds remain: at the defaults an
item that has been through its repair budget once has spent three rounds of
four, so the configured grant of two attempts is cut to the one round that is
left, and the truncation is recorded. An untruncated grant would promise a round
nothing would let it take, and one that overshot the cap would make the cap
decorative.

The room it is cut to counts what a grant already recorded has promised, not
only the rounds the item has produced. A grant is spendable from the moment it
is written, so two taken before either is carried out would otherwise be cut
against the same room and promise between them more than the cap has, with
neither one overshooting it. What a grant has promised and the item has not
spent is a reservation, and it is released where the round is not going to be
produced — see [what spends a round and what does not](#what-spends-a-round-and-what-does-not)
below — so the room a later decision is cut to is what the item may still
actually cost.

**The round cap is not their only bound, and could not be.** Each of those two
is also once per item, which is not configured because it is the workflow rather
than a judgement about pace: triage takes its own decisions about one item once,
and a second is an escalation rather than a bigger budget. The rounds cannot
stand in for it — they bound what an item costs, and an item whose runs stop
before any reviewer verdict, on a provider that kept refusing or a replay that
conflicts, costs no rounds at all. With only the round cap, that item could be
handed back and re-run without bound while every counter read zero.

A merge re-arm buys no round at all, which is exactly why it needs a bound of
its own: an action that costs nothing to take is the one that can be taken
forever. It is also the only one of the three that is not the item's. What a
re-arm repeats is one merge request the reviewer's verdict already authorized,
so the governed design bounds it at **once per publication** and calls a second
drop of the same publication an escalation.

It shipped keyed to the item and sized by
`execution.integration_retries_before_reconciliation`, which was a different
bound in both halves: it granted one publication a second re-arm the design
calls an escalation, and it refused a later publication of the same item its
first. It is now once per publication and reads no configuration at all, so an
operator raising the integration retries cannot move it back.

#### What spends a round and what does not

Stated once, because the rule above — an approval is not a round, and neither
is a trivial residue — was yoyodyne-ifd.279's and the record afterwards showed
it not holding: eleven approved overrides in five days, every one on an
undisputed change. So yoyodyne-ifd.391 completed it: **the cap counts only
rounds that ended in a verdict requiring repair against a change that was
present.** The counter is charged at verdict time and by nothing else. A round
spends when the reviewer sent the work back with anything but a single
out-of-scope note, about a change that was in the worktree to be judged. A round
spends nothing when it approved the change; when its whole residue was one
finding the reviewer disposed of as out of scope; when
the reviewer escalated the item instead of judging the change, which hands
nothing back; when it judged an empty diff, whatever it said about it — a
mis-selected run, a stale worktree, and a developer that delivered nothing all
put the same empty diff in front of a reviewer, and the development manager
reported those rounds counting identically to real repair rounds; when it was
granted and never executed; and when a promotion after an approval conflicted
on replay, which reached no verdict and leaves the approval standing on the
stopped run for the conflict path to re-enter through. What bounds a developer
that delivers nothing is the run's own repair budget, exactly as it bounds a
trivial residue. Whether a diff is empty is measured against the run's recorded
base commit, not against what happens to be uncommitted: the harness commits
each attempt before the reviewer sees it, whatever `approvals.publishing` says,
so a committed change judged with a clean status is a change that was present,
and a repair verdict on it spends a round
([the counters of the first such run](diagnoses/yoyodyne-ifd-399-empty-diff-rule-is-base-relative.md)
say so).

The last two are about the reservation a grant makes rather than about a
verdict. A repair grant reserves its rounds against the cap the moment it is
recorded, so that a second grant cannot promise the same room twice, and the
round budget refuses against what the item is committed to rather than what it
has cost. That reservation is released, not spent, where the round it promised
is not going to be produced: by a granted round whose verdict charged nothing,
one round at a time and only for a round of the run the repair was decided
about — a verdict in some other run of the item reserved nothing and releases
nothing — and by a decision recorded in the repair's place — a re-run, an
escalation, a wait — for what the repair reserved and the item never spent.
Both regression cases cost an operator override before this held. On
2026-09-15 yoyodyne-ifd.349's granted round approved the change and the
promotion stopped on a replay conflict, and the re-run was refused at 4 of 4
with three rounds spent — the approving round counted through the commitment.
On 2026-09-18 a repair on yoyodyne-ifd.309 reserved the cap's last round, the
harness found the run's worktree retired and refused to carry it out, and the
re-run recorded in its place was refused at 6 of 6 — a round that never ran
counted the same way. Each repair decision records the rounds it reserved,
which is what the decision superseding it releases; a repair superseded by a
repair keeps both reservations and records their sum; and `yoyo triage repair`
refuses a run whose standing decision is no longer a repair, because the rounds
that repair reserved have gone with it.

#### Crossing a cap the operator decides to cross

**A cap refuses the answer to an escalation as readily as it refuses a machine,
so a cap is crossable — in a record, by somebody named, and for a stated
reason.** Every cap above stops triage handing an item back again, which is what
they are for — but they stopped the recording as well as the carrying out. A
development manager past the round cap could not record a re-run of the item;
`yoyo triage rerun` refuses without that record; and escalating recorded neither.
So a cap-exhausted item was unrunnable by every path the harness keeps a record
of, and an operator who read the escalation and ruled that the work should be run
again had that ruling carried out by admitting fresh work in its place.

There are two crossings and they are not the same size. **The operator's is
`yoyo triage override`, below: any ceiling, any budget, including lifting one
entirely.** **The development manager's is one step, five times per item, and
only with a justification** — described under
[a crossing the development manager takes himself](#a-crossing-the-development-manager-takes-himself).

```bash
yoyo triage override --budget "review round" --cap 8 \
  --by "mason" --reason "REBUILD: the rounds went on a base that had moved" yoyodyne-ifd.143
yoyo triage override --budget "re-run" --clear --by "mason" --reason "driven by hand until it lands" yoyodyne-ifd.143
```

`--budget` names the budget in the words a refusal uses — `review round`,
`repair grant`, `re-run`, or `merge re-arm` — and `--cap` raises it while
`--clear` lifts it entirely. `--by` and `--reason` are required, because an
override nobody is named for is the thing this record exists to replace; the
override is kept on the item's own triage record, and `yoyo status <beads-id>`
and every docket entry for the item report it beside the budgets it changed.

**It clears or raises and never lowers.** An override that would leave a budget no
larger than it already stands is refused and nothing is recorded, so an item's
overrides are a monotonic account of who gave it more room and why. Lowering a cap
is a judgement about the project's pace rather than a decision about one item, and
`review_rounds_cap` above is where that is made.

That holds after the configuration moves, too. The ceiling that applies to an item is
the larger of the configured cap and its override, not the override — so raising
`review_rounds_cap` from 4 to 10 over an item carrying an override to 8 gives that
item 10 like every other, rather than pinning it to the 8 somebody once gave it. An
override only ever adds room.

**An unbounded raise is yours and nobody else's.** No role can produce one: a
crossing recorded by the development manager raises one budget just far enough
for the decision it was refused and can never clear one, and the actions that carry decisions out read overrides
rather than write them. It is a terminal command for the reason `yoyo release` is
one — the switch that answers an escalation has to work with no conversation
open.

**Nothing crosses a cap except a recorded crossing, and the refusal names both
kinds.** An override recorded anywhere else — in the work item's notes, in the
escalation, in the conversation that raised it — crosses nothing, because no
guard reads prose. That is not a hypothetical misreading: a refusal that said
only "the operator can record an override against the item" was twice answered in
the item's notes, exactly as those words directed, and the resubmitted decision
came back with the identical message. The development manager's refusal now
prints the crossing that is his own and the command above that is yours, each
with the budget that refused, the item, and the ceiling that would permit the
decision already in it.

**A decision two spent budgets refuse is refused by both at once.** A repair
grant and a re-run each stand behind two budgets — their own and the rounds — and
a refusal that named only the first was true and incomplete: the operator crossed
the budget it named, the same decision was asked for again, and the second budget
refused it in turn. On 2026-09-05 that cost two override ceremonies two minutes
apart on each of two items, four recorded overrides for two decisions. The
refusal now names every budget that refused, what each has spent, and the
override command for each, and says that both are needed:

```text
repair grant is refused for yoyodyne-ifd.272: 1 of 1 permitted repair grant(s)
are spent, and 4 of 4 permitted review round(s) are spent. What permits it is a
repair grant cap of 2 and a review round cap of 5
```

**It carries nothing out.** Recording an override changes what the guards will
permit and nothing else. The development manager then records the decision the
escalation was about, which spends the item's budget exactly as it always did, and
the next scheduling pass carries that decision out — or `yoyo triage rerun` or
`yoyo triage repair` fires it now — under every condition either already asks:
the intake hold, the stoppage being over, the item being one a run may start on,
a free developer slot. Crossing a cap and spending it are two decisions and stay
two.

**These budgets are per machine.** Two collaborators running their own harnesses
against one repository each hold a full set for the same item, so a cap of one is
a cap of two across the pair. That is a recorded limit rather than a design, and
[`docs/team-mode-scope.md`](team-mode-scope.md#a-recorded-gap-per-item-budgets-are-per-machine)
states it where the team-mode design will need it.

#### A crossing the development manager takes himself

**The development manager may cross a cap that refused him, far enough for the
one decision it refused, five times per item, and only with the reason
recorded.** Every override recorded in
the week to 2026-09-06 was granted, most of them within minutes of the escalation
that asked for it, under the operator's own standing direction — so the operator
step was latency rather than judgement. What replaced it keeps the judgement and
removes the wait: he crosses the cap, and the operator reads about it.

He records it as a triage decision like any other, naming the budget the refusal
named:

```json
{"action":"triage","id":"yoyodyne-ifd.143","run":"run-…","decision":"cross","budget":"review round","reason":"the change was right and the ground moved under it"}
```

**Three things bound it, and they are what the operator delegated it on.**

- **Exactly the ceiling the refusal named.** The crossing raises that one budget
  to one more than the item has spent against it, which is the figure the refusal
  already quotes as permitting the decision. It is measured from the spend rather
  than from the ceiling, and the two differ on every item that is past its cap
  rather than level with it — rounds are counted whatever a cap says, so the "6
  spent … at or past the cap of 4" above is an ordinary state, and a crossing that
  stepped from the ceiling would move it to 5, meet the same refusal, and cost him
  another crossing and you another message for every round of the gap. The
  merge re-arm is bounded per publication, and a crossing names no publication:
  it steps from the most any publication of the item has had — which is the one
  that refused, whenever any did — and the ceiling it raises is the one every
  publication of the item is held to. An
  unbounded raise, a cleared budget, and any ceiling beyond the one that permits
  the decision are operator acts and are refused to him.
- **Five per item.** A sixth crossing of the same item is refused, naming
  `yoyo triage override` as the path, because an item that has been given more
  room five times is one where something other than the budget is wrong. The
  crossings are counted across every budget together: what is bounded is how often
  he may decide the item deserves more room.
- **A justification, always.** A crossing carrying no reason is refused outright.
  The reason is recorded on the item beside the cap and the crossing number, and
  it is reported to the operator in the channel at the moment of the crossing.

**The channel message is the veto.** A crossing applies from the moment it is
recorded, so nothing waits for the operator's answer — what keeps their say is
that they are told at once, at `warning` severity, with the item, the cap, which
crossing of the five it was, and the reason. Disagreeing means undoing the work it
bought, not withholding permission. A crossing that reached nobody would be the
delegation without the condition it was granted under.

`yoyo status <beads-id>` lists each recorded crossing beside the operator's own
overrides, and says how many of the item's five are spent:

```text
  cap crossed on delegated authority: raised the review round cap to 5, crossed by the development manager on delegated authority at 2026-09-06T09:00:00Z: the change was right and the ground moved under it
  1 of 5 cap crossing(s) the development manager may take himself are recorded; past that the caps are yours again
```

**It carries nothing out and buys nothing.** A crossing spends none of the
budgets: it moves one cap, and the decision it makes recordable is still a
decision he records afterwards, refused by everything it was always refused by.

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
- an `execution.factory_stall_after` that is negative, which describes no
  limit anybody could mean; zero reads the two-hour default;
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
- a `product.shipped_documentation` entry that is empty, absolute, climbs out of
  the repository, or is not a Markdown file, since every entry is read into the
  Lead Product Manager's context as a description of what the product ships;
- a program manager's `remit` path that fails any of the persona rules below, in
  the same words; a `lane`, `remit`, or `triggers` on an agent of any other role;
  a `lane` the tracker would not carry, or one two agents name; a
  `triggers.every` under `5m`; and a `triggers.on` entry outside `landings`,
  `admissions`, and `stoppages`, or named twice; and a `recurring_tasks` entry
  named for a program manager instance its triggers wake, since an instance's
  passes are recorded and paced under its own name;
- a `recurring_tasks` entry named `maintenance`, which is the name the
  supervisor's maintenance pass records its passes and paces its cadence under;
- a persona path that is absolute, traverses upward, is not Markdown, is missing,
  is empty, or resolves through a symlink to somewhere outside `.yoyodyne`;
- a `role` that is not one of the harness's six, which is how a typo in an
  agents block is caught: the message names what was written and lists what could
  have been meant. Adding a role is a change to the harness, not to this file;
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
  [Provider plugins](provider-plugins.md#capability-validation) describes how
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

## Provider accounts

`accounts` is the provider accounts this project runs its agents under, keyed by
the alias each one is known by here. One is the ordinary case:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to

agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    account: default
```

The whole mapping is optional. A project that names none runs under the alias
`default`, every agent is assigned to it, and nothing about a single-account
project has to be written down for its runs to say what they ran under.

**An entry names an account and never a credential.** There is deliberately no
key here that holds a secret or a path: authentication is the provider's own and
lives on this machine, and the alias is what everything else refers to. Every run
record and every surface that reports one names the account it ran under — `yoyo
status` says it, and so does the message that opens a run's Slack thread.

**A project with one account authenticates where this machine already is**,
whatever that account is called. Nothing about a single-account project changes
because pooling exists: `claude` reads the home it always read, and the alias is
still only a name for the record.

**Under a pool, where an alias authenticates follows from the alias.** `default`
stays the machine's own home. Every other alias has a provider home of its own,
at `<state root>/accounts/<alias>`, which the harness sets that provider's home
variable to when it invokes under that account — `CLAUDE_CONFIG_DIR` for Claude
Code, `CODEX_HOME` for Codex. That is one rule, and the harness, `yoyo doctor`,
and `bin/yoyo-account` all read it the same way. It is a rule rather than a
setting because this file is versioned with the repository, and a directory
belonging to one machine has no business in it.

**An account names the provider whose authentication its home holds.** A
provider home is one provider's: an invocation pointed at another provider's home
authenticates as nobody and is refused. So an account that is not Claude Code's
says so:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to
  on-codex:
    description: the ChatGPT subscription
    provider: codex
```

`provider` is optional, and what leaving it out means depends on whether the
account has a home of its own:

- An account that authenticates **where the machine does** — a project's single
  account, and the `default` alias under a pool — serves whichever provider is
  asking, because each provider reads its own home there. This is every project
  that pools nothing, and nothing about provider-scoped accounts reaches one.
- An account with a **home of its own** under the state root is a Claude Code
  home when it says nothing, because that is what every one of them is:
  `bin/yoyo-account` makes them with `CLAUDE_CONFIG_DIR=… claude auth login`, and
  so does the login `yoyo doctor` hands back. A pool of Codex accounts states
  `provider: codex` on them.

Two providers reached by one adapter — Claude Code and a [declared
provider](provider-plugins.md) whose `adapter` is `claude-code` — authenticate in
the same shape of home, so an account holding either serves both.

**An account that cannot sign an agent's provider in is refused before anything
is claimed.** A run is served by an account that holds its developer's provider,
and a pool that holds none for it refuses at the point the account would have
been chosen — before a work item is claimed and before a worktree is cut — naming
what each account holds. The same project is refused when its configuration is
read, so the ordinary way to meet this is an edit rather than a run. In a mixed
pool the rotation simply skips the accounts of other providers, which is what
lets one pool serve a Claude Code developer and a Codex one.

`yoyo doctor` asks each account about its own provider: a Codex account is asked
by `codex` whether it is signed in, in `CODEX_HOME`, and the login it hands back
is that provider's own.

The consequence worth knowing is at the moment you declare the second account,
not before it. A project whose single account was aliased `work` was
authenticating in this machine's home; adding a second account gives `work` a
home of its own, which nobody has signed in to yet. `yoyo doctor` reports it as
`account:work` with the exact login to run, and `bin/yoyo-account` is the other
way to settle it. Aliasing the account you are already signed in as `default`
avoids the step entirely.

### Pooling work across several accounts

A second entry pools the work:

```yaml
accounts:
  default:
    description: the account this machine was signed in with
  second:
    description: the other subscription
    pool: active
    weekly_budget_usd: 100
  spare:
    pool: reserved
```

- **`provider`** is whose authentication this account's home holds, and defaults
  as [above](#provider-accounts): the machine's own home serves whichever
  provider asks, and a home of its own is Claude Code's unless the entry says
  otherwise. An account of another provider is skipped by the rotation for an
  agent it could not sign in, rather than handed a run that would die
  unauthenticated.
- **`pool`** is `active` or `reserved`, and defaults to `active`. The active
  accounts are round-robined, one account per run; a reserved one is served from
  only when no active account can be. A mapping whose every account is reserved
  is refused, because a pool with an empty active half is one every run falls out
  of.
- **`weekly_budget_usd`** is optional. It stands an account down once **this
  product's** runs that named it have cost that much over the seven days behind
  now, read from what those runs actually cost rather than from a price table.
  The scope is worth knowing: the figure comes from this product's run records,
  so two Yoyodyne products on one machine sharing a subscription each bound it
  separately and the account can be spent to twice the stated figure. Budget for
  the product rather than for the subscription, or state the budget in only one
  of them. Leaving it out is unbudgeted on purpose: spend on that account until
  the provider's own limit stops us.

  Writing `weekly_budget_usd: 0` is not the same as leaving it out, and means
  what it reads as — nothing may be spent on this account — so it is how an
  account is stood down while it stays in the mapping and keeps its login. A
  negative budget is refused, because it says nothing the zero does not say more
  plainly.

**The rotation's cursor is the run records.** Each run already writes down the
account it spent, so the pool takes the first active alias after the one the last
run recorded. Nothing else is kept, which is why the rotation survives a crash, a
second process, and a machine that was off for a week.

The cursor is read when a run starts and written when that run's record is
reserved, and those are one step. A start holds the pool's rotation lease across
both, so runs beginning in the same moment queue for the choosing and are served
by different accounts rather than all by the same one — which is the case pooling
exists for. The lease is a file lock in the run state directory, held for the
choosing alone and dropped the moment the record exists, so a start whose process
dies leaves nothing for anybody to clear. A start that never reaches the front of
that queue within two minutes is refused rather than held forever.

The lease is only taken by a project with more than one account. A project with
one account is not rotating anything and starts exactly as it did before pooling
existed.

**A run is affined to the account it started on.** The account is chosen once,
before the work item is claimed, and recorded on the run. Every invocation that
run goes on to make — each repair attempt, the review of the change, and anything
a later process resumes — reads the alias back off the record. A run that moved
between accounts mid-flight would leave half its spend on one subscription and
half on another with nothing saying so.

**Conversations sit still while runs rotate.** A conversation belongs to its
agent and lasts for weeks, so it is held under the account that agent's entry
names, or under the first active account where it names none. An agent that moved
between accounts each turn would have no provider session left to resume.

**A per-agent `account` governs that agent's own invocations, not the runs it
serves.** Under a pool the split is by what the invocation belongs to rather than
by which role makes it: a conversation, an exchange round, and a branch review
belong to their agent and are made under the account that agent's entry names, or
under the first active account where it names none. A run belongs to the work
item, so it is served by the rotation whichever agent's entry the developer and
reviewer invocations came from.

That is deliberate rather than an omission. `yoyo init` writes `account: default`
onto every agent it generates, so honouring the per-agent entry for runs would
mean that adding a second account to a project the harness scaffolded rotated
nothing at all — pooling would read as configured and do nothing, which is the
one failure that looks exactly like success. Taking an account out of the
rotation is what `pool: reserved` is for, and standing one down is what
`weekly_budget_usd` is for.

**A pool with nothing left to spend refuses before it claims anything.** When
every account is over its weekly budget, the run is refused at the point the
account would have been chosen — before a work item is claimed and before a
worktree is cut — and the refusal names what each account has spent against what
it was budgeted. A pool holding no account for the developer's provider is
refused in the same place and reads as the different fact it is: nothing is
exhausted, and no amount of waiting makes one of those accounts able to sign this
agent in.

**Setting the second account up** is [the multi-account quick
start](multi-account-quickstart.md), and `bin/yoyo-account`
asks the questions and runs the login. `yoyo doctor` then reports each configured
alias by name — `account:second` — saying which provider's authentication it
holds, whether it is authenticated, and which half of the pool it is in.
`bin/yoyo-account` signs an account in with Claude Code; an account on another
provider is signed in with that provider's own login, which the diagnosis prints.

## Operators

`operators` is the humans this project recognizes. Each entry binds one person's
identifier namespaces and says what that person may do:

```yaml
operators:
  mason:
    git_email: mason@example.com
    forge_account: mason-bryant
    slack_member_id: U0123456789
    grants:
      - own-intent
      - direct-work
  jordan:
    git_email: jordan@example.com
    forge_account: jordan-q
    grants:
      - direct-work
```

The whole mapping is optional, and a project that names nobody recognizes
nobody — which is every project until it names somebody, and is closed rather
than open. `yoyo init` writes an example of it commented out, beside the
[`slack`](#reporting-to-slack) block, so a generated file shows what an entry
looks like without recognizing anybody.

It is **top level rather than under any one surface**, because a human is known
by more than one. An act carries an identifier and never a person: a commit
carries an address, a push carries a forge account, a thread reply carries a
member id. Binding all three to one entry is what lets an authority check
resolve whichever namespace the act arrived through to the same person and then
ask what that person may do. Filing the whole thing under `slack` would have
made the Slack id the identity and the other two an afterthought.

**No new identity machinery, deliberately.** Git and Dolt authorship are the
assertion — the address on a commit is what the author says about themselves —
and the forge's push authentication is the proof, at the one boundary that is
shared. This mapping adds the join between namespaces that otherwise have
nothing to do with each other; it does not add a login.

Each key is a short name for a person, in the same shape as an agent name
(`mason`, `jordan-q`). Every field under it is optional except that at least one
namespace has to be bound: a human bound to nothing is authority attached to
nobody, since no act can arrive carrying an identifier that reaches them.

- `git_email` — the address their commits and tracker writes are authored with.
- `forge_account` — their account on the remote the project publishes to.
- `slack_member_id` — their member id in the reporting workspace, from their
  profile → "Copy member ID". It is identity rather than a secret, which is why
  it is checked in here with the rest.

Addresses and forge accounts are matched without regard to case, because they
are case-insensitive where they live; a member id is matched exactly, because it
is an opaque id the workspace issued rather than something a person types. One
identifier may be bound by one human: an identifier that resolves to two people
resolves to neither, so it is refused when the configuration loads.

`grants` is what the human may do, whichever namespace they arrive through, and
it defaults to empty. Recognizing somebody and authorizing them are two
decisions, so an entry with no grants records who a person is without giving
them anything — which is also how you take authority back without forgetting the
person.

| grant | what it is |
| --- | --- |
| `own-intent` | stating and approving what the product is for: the brief, the goals, and the non-goals. **At most one human may hold it** — several people amending goals concurrently is conflict machinery nobody has designed. |
| `direct-work` | steering work already in flight: the directives that reach a run, the thread replies the Slack sink acts on, and the decisions it asks for directly when the line has stopped. |

**One grant is checked today, and it is worth being exact about which.** The
Slack sink's allow-list is derived from the `direct-work` holders who bound a
member id, so a thread reply is acted on or refused by asking this mapping who
sent it. That is a grant checked where the act arrives, and it is the only one —
a thread reply is also the only act that carries an identifier the harness can
resolve, because the workspace issued that identifier and put it on the message.

**`own-intent` is checked by nothing, and `by: operator` proves only that a
command was run.** A terminal carries no identifier at all, so `yoyo artifact
approve` records `by: operator` on the strength of whoever ran it, and so do
`yoyo amendment approve`, `yoyo directive record`, and the `--by` on `yoyo triage
override`, which is a string somebody types. Nothing in any of those records
distinguishes the operator from anything else with a shell. Read an `own-intent`
grant as this project's record of who owns intent — which is what refuses a
second holder when the configuration loads, and what a person auditing the
mapping reads — rather than as a gate an act passes through.

**What keeps an agent out of the goals is two enforcements that do not depend on
the signature.** (The one item the harness admits on its own account — the bug
a [red landing](#where-the-whole-suite-runs) files — is the harness's act and
not an agent's, and it reaches the queue and never the goals.) A conversation
runs under adapter-enforced read-only access: Claude Code refuses tools, and
Codex permits inspection while denying writes, tool network access, and external
integrations. A run's change is compared
against the [protected paths](#protected-paths-in-a-developers-change) before any
check runs and before any reviewer sees it, so an approval a developer wrote is
refused with the rest of the diff and never reaches the repository the goals are
read from. [What reaches the queue](#what-reaches-the-queue) rests on those two
rather than on who an approval says gave it, which is what
`internal/chat/admission.go` says in its own words. If either is ever loosened,
this is what was resting on them.

Making an approval name the resolved human, and refusing one from anybody who
does not hold `own-intent`, is designed and not built.
[Operator identity, designed once](team-mode-coordination.md#operator-identity-designed-once)
is where it is specified, and what it would and would not close.

## Reporting to Slack

`yoyo slack` reports what the harness is doing into a Slack channel: one thread
per work item, one message per milestone, and every report an agent filed at the
severity it was filed under. The project says where to report and what each
speaker looks like; nothing else about reporting is configurable here.

```yaml
slack:
  enabled: true
  channel: C0123456789   # a channel id, or a #name
```

The whole block is optional, and a project that omits it reports nothing — which
is every project until it opts in. `channel` takes a channel id or a name;
an id is worth preferring because renaming the channel does not break it.

**`yoyo init` writes it commented out**, together with the
[`operators`](#operators) example beside it, so the generated file shows the
shape and says the capability exists rather than leaving both to be found in
[`docs/slack/setup.md`](slack/setup.md). Deleting the leading `# ` from each
line is the whole of turning it on; `yoyo setup` does the same edit for you, and
replaces the commented example rather than writing a second block under it.

### Avatars

Each speaker posts under its own name and picture, and the picture is the
project's to choose:

```yaml
slack:
  enabled: true
  channel: C0123456789
  avatars:
    harness: ":gear:"
    developer: ":ship-it:"                              # a custom emoji works
    reviewer: https://example.com/faces/reviewer.png
```

Keys are roles — `product-manager`, `architect`, `development-manager`,
`developer`, `reviewer`, `program-manager` — or `harness` for what no persona did. A value is
either an **emoji shortcode**, including a custom emoji this workspace added
itself, or the **https URL of an image** Slack fetches. Both shapes need the
`chat:write.customize` scope the [app manifest](slack/manifest.yaml) already
declares, so neither costs a reinstall.

The mapping is optional and so is every entry in it. A speaker with no entry
keeps the avatar the harness ships, so naming one persona's picture does not
blank the rest. An avatar that is neither shape is refused when the
configuration loads, whether or not reporting is switched on — Slack accepts an
unknown shortcode or an unreachable image without complaint and quietly shows
the app's own icon, so nothing downstream would ever say so.

Entries **merge across layers** rather than replacing each other, the way agents
do: a project that extends a bundle and changes the developer's picture keeps
every other one it inherited.

**Only the picture is configurable.** The name a message appears under, and
whose account it is, are not here and are not meant to be — who speaks is a
claim about who did the work, and a project that could rewrite it could
attribute a promotion to a developer. The avatar carries none of that:
everything it distinguishes is already distinguished by the name beside it and
the voice below it, so a reader whose client renders no picture loses nothing.

**Every name says which product it speaks for**, from
[`product.id`](#layout): `Development Manager (yoyodyne)`,
`Yoyodyne (yoyodyne)`, and a project that configured a second agent for a role
reads `Developer (opus) (yoyodyne)` — the product is last on every name, in the
same shape, for every speaker including the harness. It is applied by the voice
layer from the id the configuration already carries, never authored per message
and not configurable beside the avatars, because it is a fact about which
harness is talking rather than a claim about who did the work. An operator with
two products in development is running two harnesses, and where both are read in
one channel this is the only thing a message carries that tells them apart.

**Who may steer the harness from a thread is not configured here.** The
allow-list is derived from [`operators`](#operators): the humans granted
`direct-work` who have bound a `slack_member_id`, and nobody else. It is a
derivation rather than a second list because a list maintained beside those
grants is a list that disagrees with them — silently, and about authority. A
human granted `direct-work` who has bound no member id simply is not on it: they
hold the authority, and Slack is not a boundary they can reach it through.

An instruction from somebody on that list is recorded as a directive against the
item whose thread it was said in, and reaches the work exactly as one typed at a
terminal does; a question from them is answered by the Lead Product Manager in the
same thread and recorded as nothing. The same list is who the sink asks, each in
a direct message, when the line has stopped over ready work, and whose reply in
that thread is recorded as the decision — see
[deciding a stopped line from a direct message](slack/setup.md#deciding-a-stopped-line-from-a-direct-message). A reply from a human this mapping names who is not on it is
answered in the thread saying it was not acted on, naming the grant they are
missing — visibly, because a channel that silently ignores some people looks
broken rather than closed. What a reply may say is in
[`docs/slack/setup.md`](slack/setup.md#steering-the-work-from-a-thread).

**Somebody the mapping does not name at all gets a different answer**, and gets
it once: *I don't know you. Please reach out to … if you need something*, with
the humans this mapping names filling in the gap — by the names it files them
under, rather than as Slack mentions, so telling one stranger who to ask does not
notify everybody. It is said at most once per thread — in a thread this sink
opened, or under the message that @-mentioned the app — and everything the same
person says after it in that thread is written to the sink's log and answered
with nothing, so an unknown user cannot make the app talk by repeating
themselves. A project whose mapping names nobody has drawn no boundary and has
nobody to name as a contact, so it says this to nobody.

An earlier shape put this list under `slack` as `slack.operators`. It is gone,
and a file that still carries it is refused when the configuration loads, with a
message naming the entry to write instead.

**The credentials are not here and must never be.** The sink reads
`SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` from its own process environment and
from nowhere else: never from this file, never from a work item, never from a
prompt. That is what keeps the boundary structural rather than behavioral — one
separate process posts, and the harness builds every run's environment from an
allowlist rather than handing down its own, so no run process, and therefore no
agent's subprocess tree, has a Slack token in its environment at all, even on a
machine where the pair is exported in a shell profile. The Git commands the
harness runs itself are held to the same rule, and for a reason of their own —
a Git hook is a program the repository supplies and the harness executes; see
[the environment the harness's own Git and forge commands run
in](#the-environment-the-harnesss-own-git-and-forge-commands-run-in). What such an export does
still cost is the harness's own process and the sink: they are read from a
store only the sink's own launch looks at, under names that carry the product —
`yoyo-slack-bot.<product id>` and `yoyo-slack-app.<product id>`. The product is
in the name because a machine running more than one harness has more than one
pair, and a sink launched from a shell holding the wrong one connects,
authenticates, and posts this project's work into another project's channel.
`yoyo doctor` asks whether this project's pair is stored, and whether the sink
that is running was launched with it; [`docs/slack/setup.md`](slack/setup.md#5-store-the-two-tokens-under-this-projects-names)
has the launcher.

Reporting is an observation and never a gate: a workspace that is down, slow, or
misconfigured changes nothing about any run. [`docs/slack/setup.md`](slack/setup.md)
takes a workspace from nothing to live reporting, and the app manifest it asks
for is checked in beside it.

## Services

Slack, the dashboard, the scheduler, and the maintenance pass are parts of one
product rather than independent small tools, and `services` is where a product
declares which of them it runs. It is the
[management-and-supervision design's](designs/management-and-supervision.md)
supervision tree written down: one supervisor per product, and these are its
children, started together by
[`yoyo start`](operations.md#starting-the-product-and-stopping-it) and stopped
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
  scheduler:
    enabled: true
  maintenance:
    enabled: true
    every: 10m
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
| `dashboard` | the read-only projection of the read model, served to a browser | off |
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
whose [`slack`](#reporting-to-slack) section is off reads a stream and then
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
operator wherever critical reports reach him and delivered to the Lead Product
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
[observability-and-dashboard design's](designs/observability-and-dashboard.md#web-security-the-repositorys-web-service-conventions-established-here)
web-security conventions made configuration:

- **`port`** is the TCP port it serves on, a fixed number rather than one the
  operating system chooses, because a supervised child that came up on a
  different port after every restart is one nobody can bookmark or reach from
  another device. It must be between 1 and 65535; `8765` is the default, the
  same port the operations guide has used as its example of one worth
  bookmarking.
- **`bind`** is the address it binds, as an IP literal. `127.0.0.1` is the
  default — the IPv4 loopback address by number rather than as `localhost`,
  which on some machines resolves to the IPv6 loopback first — and a project
  that says nothing gets exactly the loopback-only behaviour the standalone
  command had. Writing an interface address instead is the opt-in that lets
  another device on your network open the page. A specific interface address is
  preferred over a wildcard, which reaches every interface the machine has, and
  neither is refused.
- **`allowed_hosts`** are the names, beyond the bound address itself, a request
  may carry as its Host and Origin. Empty is the bound address alone — with
  `localhost` beside it under the loopback default, which the standalone
  command already accepted. Each entry is a host name or an IP address written
  without a scheme, a port, or a path, because a request's Host header is
  compared against it exactly and an entry with a port in it would match
  nothing a browser sends. The list is replaced wholesale rather than merged,
  as `checks` is.
- **`token`** is where the bearer token every request has to carry comes from.
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

**A bind outside loopback with a generated token is refused when the file
loads**, with the reason. The opt-in exists so a browser on another device can
reach the page, and a token printed to this terminal is exactly what that
device cannot read, so a non-loopback bind has to name where its token is
stored before it is accepted at all. What the opt-in keeps is every other rule:
the token is still required on every request, Host and Origin are still checked
against the configured address and hosts, and there is still no write path at
any address — a wider bind widens who can read observability data and never who
can direct work. Transport is plain HTTP in V1, so the opt-in is for a network
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
[Working on yoyo itself](developing-yoyo.md#what-a-checkout-needs-besides-go)
names Node as the development dependency this is about.

**[`yoyo start`](operations.md#starting-the-product-and-stopping-it) is what
acts on this section.** It starts the product's supervisor, which reads the
section and starts every enabled part it knows how to: the Slack sink as
`yoyo slack ensure` starts it, and the scheduler as `yoyo work --watch` under
its own watch lease. One part is declared here ahead of the supervisor knowing
how to start it, the dashboard, and `yoyo start` says so: adopting the dashboard
as a supervised part is yoyodyne-ifd.414, and until it lands `yoyo dashboard`
is started by hand and still binds loopback on its `--port` rather than reading
this entry's `port`, `bind`, or `allowed_hosts` — `token` it does read, whether
or not the entry is enabled, so that
[a stored token outlives a restart](operations.md#watching-from-a-browser-the-dashboard).
Declaring the whole section now is what lets that command and the resident that
starts with the machine read one statement rather than two.

**The maintenance pass is the supervisor's own**, not a process it starts:
every `services.maintenance.every` — ten minutes by default, a minute at the
shortest, measured from the last pass as a recurring task's cadence is — it
runs `yoyo reconcile`, answers the restart requests program managers made, and
takes up a build deployed over the supervisor, restarting nothing while the
provider cannot be reached or is not logged in. Each pass is recorded in the
sweep log under the name `maintenance`, which is why a
[recurring task](#recurring-tasks) may not take that name.
[The maintenance pass](operations.md#the-supervisors-maintenance-pass) says what
each step does and what it never does. With the part off, `yoyo reconcile` is
yours to schedule.

## Recurring tasks

Everything else the harness starts is reactive: an item is admitted, a run stops,
somebody asks. A recurring task is the other shape — a role woken every so often
to look at its own domain and deal with what it finds — and it is configuration
because what runs and how often is a project's judgement rather than a release's:

```yaml
recurring_tasks:
  development-manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    max_turns: 4
    prompt: |
      Sweep for unresolved issues: stoppages nobody has decided, claims on work
      nothing is running, deliveries that have stopped moving. Fix what your
      authority allows, ask the architect where a ruling is needed, and file
      root-cause work with the Lead Product Manager for every fix you make.
```

**Configuration decides which role is woken, when, and on which model, and
nothing else.** There is
no key here for a capability, a tool, an account, or an authority of any kind,
and the absence is deliberate rather than an omission to be filled in later: a
role woken on a cadence holds exactly what its role already holds, resolved from
the harness's own registry the same way it is resolved for a conversation you
open by hand. A scheduled turn also reads the role's own persona, so the
personality that answers is the one the project configured and not a second
version of it. The loader is strict about keys, so a `capabilities:` or `tools:`
written under a task fails the configuration rather than being ignored.

A development manager's task is handed one thing no prompt has to ask for: the
[triage docket](conversation.md#roles-asking-each-other-things) as it stands,
built for each firing and put in the message that wakes her, ahead of the
prompt. A pass resumes her conversation rather than opening it, so the docket
that conversation opened with is not what is waiting on her now.

Every recurring task turn also receives the live work assigned to that role's
conversation, with each item's priority shown. The harness lists it in backlog
order: highest priority first (P0 before P1), then oldest admitted within each
priority, using the same order as a developer pull. Parked and finished items
are excluded. This list is read again for each turn, so an architect's pass
receives a newer P0 design before an older P2 without relying on the prompt to
reorder an old briefing. The role reads each item in full before deciding.
Reports and proposed amendments keep their own ordering, described below.

`yoyo init` includes a commented `architect-pass` example at `every: 1h`, whose
prompt says to take work by priority first, then age within each priority. A
project chooses its own cadence; this repository uses `45m` to keep passes
closer together after the operator observed more cached opening input below
an hour. That observation is a reason for the setting, not a provider guarantee.

| Key | What it says |
| --- | --- |
| `role` | which role is woken. It must be a role this project configures an agent for; a task naming a role nobody fills is refused rather than discovered as silence. |
| `every` | the cadence, measured from the last firing rather than against a wall-clock grid. The shortest accepted is `5m`, which is what keeps `1m` written where `1h` was meant from becoming sixty times the spend. |
| `enabled` | the switch. It is explicit so a task can be turned off for a week without deleting its prompt and cadence. |
| `prompt` | what the role is told. It is the task, not a personality. |
| `max_turns` | how many work turns one firing may take, defaulting to 3 and capped at 10. One request for a missing closing report is outside this bound. |
| `model` | the model this task's turns ask for. Optional: leave it out and the turns ask for the role's own configured `model`, which is what every task did before the key existed. See [a task's own model](#a-tasks-own-model). |

### A task's own model

**Model spend follows the work rather than the role.** A routine pass over a
domain and a decision about one stoppage are both the development manager's
turns, in the same conversation, and only the decision needs the role's model.
A task that names a `model` has its turns ask for that one, and every turn the
task does not cover — a message you send, a docket decision, a directive, a
summons delivered some other way — asks for the role's own model as before:

```yaml
recurring_tasks:
  development-manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    max_turns: 4
    model: sonnet
    prompt: |
      ...
  report-triage:
    role: product-manager
    every: 12h
    enabled: true
    model: sonnet
    prompt: |
      ...
```

It is the operator's direction of 2026-09-19, off the same seven-day reading as
[the developer mapping](#a-developer-model-chosen-by-the-items-label): the
management roles on their own model were $317 of $1,431, and most of it was
routine sweep passes. The lines are the operator's to paste into the project's
own configuration by hand, because `.yoyodyne/` is a
[protected path](#protected-paths-in-a-developers-change) no run may write; a
task whose block does not carry the key runs on the role's model.

**It selects a model for the task's turns and nothing more.** The turn is taken
in the role's own conversation, under the account that role's agent names,
holding the authority the role holds and reading the persona it reads — so
configuration still selects and never widens what a role may do. The
[account pool](#pooling-work-across-several-accounts) and the
[failover rules](#serving-a-turn-from-a-permitted-alternate-model) apply
unchanged: a pass whose model has no capacity is served by the agent's
alternate where it has enabled one. Two things follow from the model being
another one. A [pinned version](#pinning-an-agent-to-a-model-version) is a
version of the agent's own family, so a task naming another model carries no
pin — and a task naming the agent's own model keeps it. And an alternate that
*is* the task's model, on the agent's own provider, is dropped for that pass,
because failing over to the endpoint whose window just closed is a second
refusal rather than an alternate.

**The model is validated exactly as an agent's is**, and refused when the
configuration loads with the same reason: a selector longer than the bound, one
with whitespace in it, and one that begins with `-`. Leaving the key out, or
writing it empty, is not a refusal; it is the role's model.

**Every pass records the model it ran on**, beside its turns and its cost, as a
run's record names its developer's model. It is the model that served — the
task's own, the role's where the task names none, or the alternate where a
failover answered — and [`yoyo sweeps`](operations.md#reading-what-the-recurring-tasks-found)
prints it on each pass's header. [`yoyo status --spend`](operations.md#following-a-run-a-conversation-or-a-branch-review)
reads the same records and adds a table under its totals: each task's passes,
turns, and cost, by the model they ran on. It is a split of the conversations
figure above it rather than an addition to it, since a pass is conversation
turns. A pass recorded before passes named their model is shown as
`(not recorded)`.

**A firing costs what conversation turns cost.** The cadence is therefore the
spending decision: `every: 1h` is a turn an hour for as long as a `yoyo work
--watch` session is running.

**Firings of different roles do not wait on one another, and none holds the
pull.** A pull claims the firings that are due and takes their turns beside
itself, each in its role's own conversation — a program manager instance's in
the instance's own — so the queue is read and started from, stopped work is
delivered, and other roles are woken while a pass is still taking its turns. A
multi-turn pass holds its own role's conversation and nothing else: a second
task of the same role waits for it, because a conversation takes one turn at a
time, and a stopped run's delivery to the development manager waits while her
pass is in flight. At most four firings are in flight at once, a bound that is
the harness's and not configured. Where it is reached, the firing that has
waited longest since it fell due goes next, whatever kind it is; a [critical
report's delivery](#working-the-report-pile-on-a-cadence) waits by when the
oldest report it carries was filed, so it never takes a firing from another
role that fell due before it. [`yoyo sweeps`](operations.md#reading-what-the-recurring-tasks-found)
says how long each pass waited after it fell due. Before 2026-09-30 a pull made
one firing, tasks first, and took its turns inside the pull: on 2026-09-29 the
Lead Product Manager's several-turn sweep kept the development manager's sweep
and a program manager's pass unfired for more than an hour, four times, and the
critical reports of those misses woke her into the one firing they were waiting
for; on 2026-09-30 one pass that spanned a machine sleep held every pull for
three and a half hours.

**What bounds that spend is the session's own
[`--budget`](#watching-instead-of-draining)**, which counts a firing's turns
exactly as it counts a run it started or a stopped run it delivered — so a
session given a budget stops on it rather than sweeping past it, and a session
given none is bounded by the cadence and nothing else.

**`yoyo pause` is the switch that stops firings**, exactly as it stops runs and
conversation turns. The pause is read at the start of a pass, before anything is
claimed, so a task a held pause was in place for keeps its cadence and fires once
the pause lifts. One narrow case costs a cadence rather than keeping it: a pause
placed in the moment between a task's claim and its turn arrives after the claim
has already moved the clock, so that firing is recorded as one the role could not
be reached for and the task waits for its next cadence rather than firing when
the pause lifts. It costs one pass of one task, and only for a pause that lands
inside that window. **Holding intake does not stop them.**
The hold stops the harness choosing work, and a firing chooses none: it is read
before the hold on every pull, so a task fires under a held intake exactly as it
does under a clear one. That is deliberate — a held queue is often waiting on
exactly the kind of look a sweep takes — but it is the opposite of what an
operator reaching for the hold to stop spending expects, so it is worth saying
plainly: to stop paying for a cadence, pause rather than hold.

**A heavy pass iterates rather than truncating.** A role that has more to do than
one turn holds says so in its account, and the harness gives it another turn up
to `max_turns`. A pass that still had more to do when the bound ran out is
recorded as partial, so a truncated pass and a finished one are never the same
short report.

**A missing closing report is requested once on the same pass.** The request
quotes the report's shape and asks for the block alone, without repeating any
action. If it is still missing, the pass fails with its findings unrecorded;
earlier writes and both replies' costs stand. This extra request spends no
work-item budget and does not consume a work turn under `max_turns`. After
consecutive passes still omit the report, the next opens a fresh conversation
with the role's memory and briefing. This bound covers all recurring roles,
including program manager instances:

```yaml
execution:
  missing_reports_before_fresh_conversation: 3  # the default; zero also uses three
```

The value must be nonnegative. A recovered report resets the count, as does
replacing the conversation. The pass record and `yoyo sweeps` name the old and
new conversations and why the replacement happened. Existing conversations and
their events remain durable. See
[reading the passes](operations.md#reading-what-the-recurring-tasks-found).

The development manager's continuation turns also carry the next slice of her
live docket, through the same listing as the first turn. Entries already
delivered on that pass are not repeated, and decisions and closed work are read
again before each slice. The harness continues while live entries remain
undelivered, even if a turn's account says complete. A reply without an account
must recover it through the one block-only request; otherwise the pass fails
and continues no further work. If the turn bound or a provider refusal ends the pass first, its
record counts the entries never delivered and names the oldest; the next pass
puts them ahead of entries already shown.

**A reply with more than one account keeps the last.** The contract is one
sweep block per reply, and a role that answers with two — a `more` and then a
`complete`, which is the shape the slip takes — has slipped rather than failed.
The last block is recorded as the pass's account, and the record notes beside
it that more than one was sent, rather than the pass being thrown away over
the shape of its reply after its decisions were taken. A block that cannot be
read is still refused wherever it sits, and a reply with one block is recorded
exactly as before.

**A firing that failed waits for its next cadence.** It is not retried at once:
the next pass looks at everything this one would have, and retrying immediately
would spend turns against whatever was already failing. What stopped it is
recorded against the task, so a schedule that is running and producing nothing is
something you can find.

**A run in flight does not hold the cadence.** A watching session waiting on a
run of its own goes back round to the schedule when the next task falls due,
fires it, and returns to waiting. Before this, the wait ended only when the run
did: on 2026-09-13 one run took twenty hours and the development manager's hourly
task fired nothing in all of them. A session waiting out a redeploy goes on
firing its tasks on their cadence for as long as it still hosts a run, because
the [drain](#watching-instead-of-draining) is about the runs it hosts and not
about the schedule. Once it hosts none it starts no new pass — the session that
comes back takes it at its first pull, since the cadence is claimed durably —
and it waits for a pass still taking its turns before it restarts, until the
drain bound: a session restarting past the bound stops every pass still taking
its turns and records each as a missed pass.

**A task that goes a whole interval unfired is recorded as missed, with what
kept it.** A task is missed once it is a whole interval past the time it fell
due. Anything shorter is the ordinary shape of a cadence: a task waits while
its role's conversation is taking another pass's turns, or while every firing a
session takes at once is in flight. A miss is found at the
first pull that reaches the schedule afterwards. A gap is recorded once, even across a restart:
a session finding a gap already in the sweep log records and reports nothing.
`yoyo sweeps` shows it as a pass that took no turn, spanning the gap, and its
problem names the cause. Each cause is also reported differently:

- **The harness held its own cadence.** The schedule could not be fired, the
  harness could not be read, or the task was kept behind another firing — its
  conversation taking another pass's turns, named, or every firing a session
  takes at once in flight, named. This is filed as the
  harness's own report at `critical`, which puts it in front of the operator.
- **The firing was turned away before it reached the role.** The provider had
  no capacity, the provider was answering nobody, or the role's conversation was
  held by another process. The miss quotes the refusal, including the reset the
  provider named. Where the session's own reading of the provider says more, such
  as the usage window and when it resets, that is added. This is reported at
  `warning`, since the provider's wait already has a notice of its own. In
  practice a refused firing is itself recorded as the pass for that cadence,
  so this cause names a miss only where the cadence was held without moving.
- **No session was running** when the task fell due. This is reported at
  `warning`, since whoever stopped the harness knows. If this session did open
  late but then found the task held by one of the causes above, that cause is
  what gets named. With supervisor history available, a session opening after
  the due time alone does not prove that no earlier session was running.
- **The operator's pause** is recorded and reported to nobody.
- **Nothing recorded.** No cause was found at or after the time the task fell
  due, for example because the session spent the interval somewhere other than
  its schedule. The miss then says the session recorded nothing that kept the
  task, and is reported at `critical`. It never names a hold from before the task
  fell due, because that describes the pass before the gap and not its cause. A
  task's own firing is never named as what kept it.

When supervisor observations are available, a miss names the machine's sleep
from OS power history, an observed interval with the scheduler down, or the
recurring pass it waited behind. More than one may apply to the same gap. Sleep
and observed downtime are warnings; waiting behind another pass is reported at
critical severity. Current scheduler holds, including refusals and capacity
resets, remain named alongside these observations, with the hold's severity
retained unless an observed wait behind another pass makes it critical; an
operator's pause remains quiet. Unavailable history and gaps between supervisor
looks are described separately as incomplete evidence, never as established
causes. An unfinished pass whose ending is missing is identified as uncertain
rather than treated as proof that it held the whole gap. The last
pass's failure is not the cause of a later miss. A stopped provider response
also carries the observations available during that response; absence of sleep
evidence does not turn a transport failure into a claim about the machine.
These observations remain in the run's event log even if a later attempt
recovers and completes the run.

The harness records the interval it adopts and when it first reads a changed
one. A new cadence owes nothing before that observation: its due time is the
later of the last firing plus the new interval and the observation time.
Reading the same interval after a restart retains that time. A missed-pass
account gives the age of the last actual firing separately from time overdue
under this effective schedule. Older records with no adoption evidence say
that earlier timing is unavailable and measure lateness only from the first
observation; the age of an old firing cannot establish missed obligations under
the current interval. This applies to shortening and lengthening intervals and
to program manager instances' schedule triggers. Event triggers keep their own
settling and minimum-interval rules.

The cadence is not moved by a miss. The task is still due, and fires on its own at
the first pull that reaches it once the cause clears.

**A program manager instance's passes are covered by the same detection**,
keyed by the instance and the trigger that owed the pass. Its `triggers.every`
is missed exactly as a task's interval is; its `triggers.on` events are missed
once a wake past its cursor has stood takeable — its streams settled, and the
recurring minimum passed since its last pass — for half an hour with no pass
taken. The causes and their severities are the ones above. A pass that was
taken and cancelled before it completed is a missed pass too: one whose session
was stopped under its turn is recorded as one at once, and one whose process
died carrying it, which records nothing, is recorded by the next pull that
considers the instance once the claim has stood five minutes with no ending and
no turn in flight on its conversation. See
[reading what the recurring tasks found](operations.md#reading-what-the-recurring-tasks-found).

**The intake brake summons a development manager's task out of its cadence.**
The first enabled task whose role is `development-manager` is the one the
[failure-storm brake](#watching-instead-of-draining) fires the moment it trips,
whether or not the task is due: the same conversation, the same turn bound, the
same durable report, with the runs that blocked and the reason each blocked in
the message that wakes her ahead of the task's own prompt. It is a firing like
any other — it counts, it is charged to the session's `--budget`, and the
cadence runs on from it, so the hourly pass does not follow a summons a minute
later over the same ground — and `yoyo sweeps` shows it as summoned, naming
what tripped the brake. A project that schedules no such task gets no summons;
its brake is decided by the cooldown's probe rather than by her, and the hold
says so. The provider answering nobody refuses a summons exactly as it refuses
a scheduled firing, and the pause covers both. A trip that finds a pass of hers
still taking turns in her conversation does not summon her into it — the
summons claims her task's firing before it asks her anything, so it would spend
that claim on a turn her own pass holds the conversation against — and summons
her at the first pull after that pass ends, provided the brake's hold still
stands and has not been escalated; a stopped run's delivery to her waits for
her pass the same way. A wake for a tracker block the harness refused is
already refused by a conversation mid-turn before it claims anything, and is
made at a later pass.

**A development manager's pass also reads the forge.** On every firing of a
task whose role is `development-manager`, and only that role's, the harness
itself lists the open pull requests of the repository the project publishes into
and adds a finding for each one the forge is holding open for nothing: a request
whose work item is closed, and a request whose head branch is already contained
in the branch it targets. Which work is closed is read from the tracker whole
rather than from the first page of its listing, so a request superseded long ago
is reported as such and not passed over as live because its item fell past a
page. The finding names the request, the work item, and which
of the two holds, and it is `left` rather than `fixed` — the harness closes
nothing, and neither does the role on its account; the request is there for
somebody to decide about. Each request is reported once, keyed on its number,
however many passes find it still open afterwards: the requests a pass reported
are recorded on its report, and the next pass reads them back before it looks.
The reading is taken beside the role's turns rather than by the role, so it
happens whether or not the role could be reached, and a forge that could not be
read is a problem on the record rather than a lost pass. The reading is taken
under exactly the setting the harness opens requests under:
[`approvals.publishing: automatic`](#publishing-without-automatic-integration), which is
the only value that pushes a branch or opens a pull request. Under `human`, the
other value, the harness opens no requests and reads no forge, so a request
somebody opened by hand in such a project is not noticed here.

**A pass of a role that owns documents is put the changes proposed to them.**
On every firing of a task whose role owns artifacts — the architect the designs,
specifications, and decision records; the Lead Product Manager the brief and
the goals — the harness reads the [proposed
amendments](#proposing-a-change-to-a-document-you-do-not-own) nobody has
decided against that role's documents and puts them in the message that wakes
it, ahead of the task's own prompt: oldest first, at most ten a pass, and never
one the role already argued on an earlier pass while the operator has not
decided it, so the pass moves on to what has not been argued rather than
re-arguing the same ten every cadence. The wake says how many wait behind the
batch and how many already carry a recommendation. The role's account then
carries a `recommendations` entry for each — `approve`, `decline`, or `merge`
with another, with the reason — and that batch is on the durable report. A role
that owns no documents, or has nothing undecided against them, is told nothing
about any of this. See [working the amendment queue on a
cadence](#working-the-amendment-queue-on-a-cadence) for the task that exists for
it.

Every firing ends in a durable report, read with
[`yoyo sweeps`](operations.md#reading-what-the-recurring-tasks-found). The reports
outlive the session that produced them and are written once and never revised.

### Working the report pile on a cadence

The other standing loop worth configuring is the one that drains the
[collected reports](reporting.md#who-reads-them-and-what-became-of-each-one).
Every role files what it noticed into one pile, the Lead Product Manager is the only
role that can record what became of a report, and until something wakes it for
that the pile is worked only when you happen to open a conversation. Reports
arrive at twenty to forty-five a day in this project, which is more than that
reaches.

`yoyo init` writes this entry into the generated configuration, commented out and
beside the development manager's sweep, so a new project has it to uncomment
rather than to compose. **A project that has not uncommented it has no cadence
over the pile**,
and no part of the harness supplies one on its behalf — the schedule is where a
project says which roles are woken and how often, and a task nobody wrote is a
task that does not fire:

```yaml
recurring_tasks:
  report-triage:
    role: product-manager
    every: 1h
    enabled: true
    max_turns: 4
    prompt: |
      Work the collected reports. The unhandled ones are carried into this turn
      already, oldest first with anything critical ahead of them; decide about
      every one you are shown and record each decision with the "handle"
      action, whether that decision is work to admit, a proposal to make, a
      question to raise, or that it needs nothing. Check anything you would
      admit against the work already admitted first. Say in your pass's summary
      how many you decided and how many are still behind them, and keep the
      findings for what was worth more than a handling; a pass that has more of
      the pile to work than one turn holds says so and takes another.
```

Every decision is on the record twice, which is why the prompt does not ask for
one finding per report: the `handle` action writes what became of each report
beside the pile, and the pass's own account in `yoyo sweeps` is the summary of
the pass — bounded at twenty findings a turn, which a pass working forty reports
would otherwise spend on bookkeeping.

Nothing about that turn is special, which is the point: the same persona, the
same authority, and the same bounded delivery a conversation you open yourself
gets. What makes the loop converge is the delivery being a walk with a durable
position rather than a listing — see
[the walk](reporting.md#who-reads-them-and-what-became-of-each-one) — so each
firing takes the next slice of the pile instead of the same worst one. Whether
it is keeping up is answered by the count and the oldest undecided report's age
that every listing of the pile now leads with.

This task is also what a critical report is delivered through. The first
enabled task that wakes the product manager, in name order, is fired out of its
cadence on the pull after a critical is filed, with that report in the message
— a firing of her conversation alone, so it never holds up a pass of another
role that fell due before it; its passes are refused as complete while a critical they were shown stands
unhandled; and they name a program manager's warnings and notes left unhandled
through two passes as overdue. What each of those does is in
[the reporting guide](reporting.md#who-reads-them-and-what-became-of-each-one).
A project with no such task has none of the three.

### Working the amendment queue on a cadence

The third standing loop is the one that argues the [proposed
amendments](#proposing-a-change-to-a-document-you-do-not-own). A developer that
finds a design wrong proposes the change and carries on; the proposal waits on
the architect's argument and your decision; and until something woke her for
it, the argument happened only when somebody opened her conversation. This
project stood at forty-four undecided proposals against the designs, the oldest
weeks old, before the pass existed.

`yoyo init` writes this entry into the generated configuration, commented out
and beside the two above, so a new project has it to uncomment rather than to
compose. **A project that has not uncommented it has no cadence over the
queue**, and no part of the harness supplies one on its behalf:

```yaml
recurring_tasks:
  architect-amendments:
    role: architect
    every: 6h
    enabled: true
    max_turns: 4
    prompt: |
      Work the changes other roles have proposed to your documents. The
      undecided ones are carried into this turn already, oldest first and at
      most ten a pass, and nothing else wakes you to argue them, so a queue
      nobody wakes you for is a queue nothing drains.
      Argue every one you are shown: read the document it names, and
      recommend approve, decline, or merge with another, with the reason, in
      the "recommendations" of your block. You decide nothing and edit
      nothing here -- the operator records each decision under your
      authority, and an approved change is then yours to make as a revision.
      Say in your summary how many you argued and how many wait behind them.
      When nothing is undecided, that is the report.
      Name every work item by what it is, with its identifier after it:
      "retiring the maintenance job (434.9)", never "434.9" on its own. An
      identifier alone is a defect -- nobody reading later knows the item.
```

What the pass produces is a batch of recommendations and never a decision.
The harness puts the undecided proposals in the wake, oldest first and at most
ten a pass — a proposal the architect already argued on an earlier pass is not
put again while it waits on you, and the batch closes at the first proposal
that would take the wake past its size bound, so what she is put is always the
oldest she has not argued. She argues each back in her account, and the
firing's report in `yoyo sweeps` lists them in one batch.

**The batch reaches you once, as one decision list.** Each pass that argued
something still undecided is a [finding that needs your
hand](operations.md#where-a-finding-that-needs-your-hand-goes): it is sent to
you directly, tagged by member id, through the same operator-action message
every such finding uses, naming each proposal with what she recommends and why,
and it is not sent again. `yoyo status` names it on the needs-a-human line —
never folded into the count of things not named — until every proposal in it is
decided. **Deciding is yours**, from the same two verbs as before:

```sh
yoyo status                                     # each pass's batch waiting on you, and each undecided proposal
yoyo sweeps --task architect-amendments         # her reasons, pass by pass
yoyo amendment approve <id> --reason ...        # record the change as authorized
yoyo amendment decline <id> --reason ...        # turn it down, keeping why
```

A `merge` recommendation names the proposal it folds into; the record has no
merge of its own, so it is carried out as one approval and one decline whose
reason names the other. A proposal you decide between her pass and your reading
is dropped from the batch wherever the batch is read — `yoyo status` derives it
from the sweep records and the amendment log together — so the batch is always
what you still have to decide rather than what she once said, and a batch whose
every proposal is decided stops being named. The same cadence works for the
Lead Product Manager over the brief and the goals; nothing
about the mechanism is the architect's except that her documents are where
proposals accumulate.

Whether the queue is draining is answered the way the pile's is: `yoyo status`
carries the undecided count and the oldest undecided proposal's age on every
reading, and names the queue on the needs-a-human line once that age passes a
week, as the operator's — a queue this old says the task is not keeping up, or
is not enabled.

### A program manager instance's passes

A [program manager instance](#a-program-manager-instance) is woken the way a
recurring task is, by its own `triggers` block rather than by an entry here, and
its pass **is** a recurring-task firing: everything above holds of it unchanged.
The pause stops a pass and the intake hold does not; the claim is taken before
the first turn; an instance's pass is taken beside the pull and beside other
roles' firings, in the instance's own conversation, and where the bound on
firings in flight is reached it waits its turn by how long it has stood due like
a task's; a provider answering nobody is recorded as the wait; and every pass
ends in a durable record [`yoyo sweeps`](operations.md#reading-what-the-recurring-tasks-found)
reads, filed under the instance's name — `yoyo sweeps --task reliability-pm` —
with the model its turns ran on. A recurring task named for an instance its
triggers wake is refused when the file loads, because the two would share one
cadence and one record.

```yaml
agents:
  reliability-pm:
    role: program-manager
    # backend, model, persona, remit as any instance
    lane: reliability
    triggers:
      every: 2h
      on: [landings, stoppages]
```

- **`every`** is the instance's schedule, measured from its last pass like a
  task's, and floored at the same `5m`. An instance with `every` fires as a
  recurring task on that cadence whether or not anything happened; one without
  it is woken by its events alone.
- **`on`** is what else wakes it: `landings` (a run that landed its change),
  `stoppages` (every developer run ending `failed`, `timed_out`, or `cancelled`,
  or leaving its work item waiting on a decision), and `admissions` (work created
  in the tracker). Stoppages include runs that end during checks or without a
  durable blocker, and runs a developer or reviewer escalates for the development
  manager to decide. Each carries its work item, run, and recorded reason;
  a record giving no reason says so. A run still in progress is not a stoppage.
  The first two are read from the run records by when each run ended, and
  admissions from the tracker's item export by when each item was created.

**A burst wakes an instance once.** Each instance keeps a cursor per stream —
the run records and the tracker — under the state root, at
`projects/<product id>/state/program-managers/<agent>/cursor.json`, beside its lane
report. An event past the cursor arms one wake. The wake is taken at the next
pull once the streams have been quiet for **two minutes**, or at once where
`every` is due, whichever comes first; the pass is handed everything between the
cursor and the moment it was taken, grouped by stream in its first message; and
the cursor moves to that moment only once every turn of the pass was answered. A
product manager admitting thirty items in one turn is one pass carrying thirty
admissions, which `yoyo sweeps` shows under the pass's header as
`carried 30 admissions since its last pass`. A pass that fails leaves the cursor
where it was, says so on its record, and the next pass carries the same events.
What the failed pass wrote into its memory and its lane report before it failed
stands, and the next pass is told which of those writes are already saved so it
does not make them twice; [reading what the recurring tasks
found](operations.md#reading-what-the-recurring-tasks-found) says how the record
names them.

**An event that arrives late is still carried.** The tracker's export is written
after the tracker's own write, and a run record is readable only once it is
saved, so an entry can say it happened before a pass was taken and appear only
after that pass moved the cursor past it. Each stream is therefore read again
from fifteen minutes behind its cursor — never from before the instance began
watching it — and the cursor keeps, by item or run, what completed passes
carried from inside that reach: the late entry goes to the next pass, and
nothing a pass already carried is handed again. An entry later than fifteen
minutes is outside what a pass promises to carry.

Four more things decide whether a wake is taken, and none of them is configured.
An instance seen for the first time, or a stream it has just begun to watch, is
positioned at that moment, so its first pass is handed what happened since it was
watched rather than every run the product has recorded. A turn in flight on the
instance's conversation queues the pass behind it, within the scheduled turn's
fifteen-minute bound or the caller's earlier deadline. A wait that runs out is
recorded as a missed pass naming the holder, and the events wait past the cursor.
A wake armed by events is not taken sooner than the
`5m` minimum after the instance's last pass, which also paces the retry of a pass
that failed. And while the provider is answering nobody an armed wake waits
rather than being recorded as a refusal on every pull; the schedule, which its
cadence paces, is what records the wait. A pass taken for events moves the
cadence as a scheduled one does, so the scheduled pass is not taken a minute
later over the same ground.

**A pass asks for the agent's own model.** The triggers block names none, and the
[`model`](#a-tasks-own-model) a recurring task names is that task's key rather
than one an instance carries.

## Personas

A persona is a Markdown file describing how an agent works. Personas specialize
behavior; they never grant it. The harness invariants — agent authority,
worktree sandboxing, the protected paths a developer's change may not touch, the
review verdict contract, integration preconditions, and cleanup — are enforced in
Go and are not configurable, so a persona cannot weaken them:

- the developer prompt starts with the harness contract verbatim, and the
  persona follows it as subordinate guidance;
- the reviewer's system prompt starts with the immutable review contract, and
  the persona follows it; the decision vocabulary and the JSON response format
  are not negotiable, and a persona cannot authorize approving a change the
  reviewer cannot see;
- untrusted developer output is never treated as configuration, and configured
  text never replaces harness policy.

Persona rules:

- `version` is a free-form revision label recorded in the effective
  configuration, so a change of guidance is visible in diagnostics.
- `path` is relative to the directory the configuration file is in, and must name
  a Markdown file inside it. Absolute paths, `..` traversal, and symlinks that
  escape the directory are rejected. For the ordinary project that is the
  `.yoyodyne` directory, and for a configuration
  [kept outside the repository](#keeping-the-configuration-outside-the-repository)
  it is the directory `init --external` wrote, which is what lets that directory
  be moved as one. A `.yoyodyne.yaml` uses the `.yoyodyne` directory beside it,
  which is where migrating it would put the personas; and a `config.yaml` placed
  by hand somewhere of its own falls back to a `.yoyodyne` directory beside it
  when the persona is not there, so an arrangement that predates this goes on
  loading.
- A persona is limited to 32 KiB. It is role guidance, not a document to paste
  into every prompt.

The template ships six personas, one per role: `product-manager.md`,
`architect.md`, `development-manager.md`, `developer.md`, `reviewer.md`, and
`program-manager.md`. A test holds it to exactly one per role the harness knows,
so a role added without its persona fails there rather than in a project. `init`
copies all six, although it configures only five agents: the program manager's
persona is there for the project that later
[configures an instance](#a-program-manager-instance), which binds
`personas/program-manager.md` rather than writing its own. Like the rest of the
persona, it says how the role works and grants nothing — the role's contract and
the lane decide what an instance may do.

Every shipped persona and role contract carries the rule that the role
applies the standing goals to everything it writes and every decision it
makes, whichever goal the work item or its lane serves. The standing set is
read from the goals documents' `Standing goals` section under the configured
`product.specifications` home, delivered as authoritative product intent;
in Yoyodyne it is [the plain-language and autonomy goals](product/goals/v1-goals.md#standing-goals).
An output or decision that breaks one is a defect to report, naming the goal
and where it was broken. The pass prompts `init` writes carry the same rule,
and a program manager applies it to its lane report and post-mortems too.

Every shipped persona, every role contract, the pass prompts `init` writes,
and this repository's factory-flow remit also carry the rule for writing for a
person: anything a person reads uses the ordinary word for a thing, says what
happened rather than the harness's name for its own mechanism, coins no terms,
and gives every time in the operator's local time with the zone named. Its
model is a plain account of a stopped run: *the AI session running the
developer produced no output for five minutes, so the harness ended the run;
the cause was outside the work, so no repair attempt was spent and the change
was kept.* The shipped personas and this repository's copies under
`.yoyodyne/personas` carry the same section word for word, and a test holds
them to it.

In a project `init` wrote, every persona is already a file in
`.yoyodyne/personas/`: change how the reviewer works by editing
`personas/reviewer.md`, and bump the `version` label beside it in the
configuration so the change is visible in diagnostics.

```yaml
agents:
  reviewer:
    persona:
      version: house-1            # bumped from v1 after editing the file
      path: personas/reviewer.md
```

In a project that uses `extends`, the same block is how one inherited persona is
replaced without changing anything else.

**A persona change is done when the live copy carries it.** The copy a role
reads is the file its agent binds — under `.yoyodyne/personas/` — and never the
template the executable ships, which only `init` reads. Yoyodyne's own
repository is where the two sit side by side: `internal/config/builtin/v1/personas/`
is what a new project is given, `.yoyodyne/personas/` is what this repository's
roles read, and a change made to the first alone reaches no role here. On
2026-09-27 two rules — that no role routes an approval to the operator, and that
a work item is named by what it is — landed in the template alone and closed as
done while every role ran without them. So the developer contract says a run
whose deliverable is a persona or contract change is done only when the copy
the roles read carries it, and otherwise lands as evidence naming the gap
`yoyo config drift` reports, or the files where it reports none; the reviewer's
contract refuses a persona change that lands in the template alone without
saying so; and a test holds every passage of the shipped personas to being in
this repository's copies, which may say more than the template but never less.
A template passage this repository's copies deliberately do not carry is
declared in that test with the reason, which is where saying so is recorded;
today those are the passages the live copies were rewritten into plain words
ahead of the template, and a declaration that stops matching anything fails the
test so it is removed once the template catches up. A role contract is compiled
into the harness, so it reaches the roles with the build they run.

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
the one field it names, and is subject to the precedence and merge rules above.

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
[Portable agent configuration](designs/portable-agent-configuration.md) is the
governed design that answers what a project owns versus inherits, how the two
shapes convert into each other, and how a bundle improvement reaches a project
that materialized its defaults; the architect ratified it on 2026-09-01. Its
baseline and report exist: `config.lock` records what the template supplied,
`yoyo config drift` sorts each value into `unchanged`, `yours`, `available`, or
`conflicting`, and `doctor` and `config validate` speak the `available` ones
unprompted on stderr, silently when none, without changing exit codes. Where the
[Slack sink](reporting.md#what-arrives-as-a-direct-message) is running it says
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
   and each agent's capability set, which is the harness's registry either way —
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

An unexpected effective value is therefore a two-command diagnosis: `--effective`
says what the value is, and `--origins` says which layer is responsible for it.

In a project `init` wrote, the answer is the project file for every configured
value. The exceptions are the values the generated file leaves to follow
something else: `derived:product.id` for `product.repository_id`,
`derived:execution.repair_attempts_before_replan` for
`triage.repair_grant_attempts`, and `registry:role-capabilities` for every
agent's capability set, which is the harness's rather than any file's. Nothing
reports `builtin:v1`, and nothing reports `harness-default`, because the
generated file writes down every value the harness would otherwise have filled
in. So an origin that is none of those means the configuration is inheriting
something, which is worth looking at.
