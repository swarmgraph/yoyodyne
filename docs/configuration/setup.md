<!--
Landed by yoyodyne-ifd.117.1, tranche 1 of the configuration.md split, with
docs/configuration.md left intact. The links below into ../configuration.md
resolve today and point at sections a later tranche moves; the tranche that
moves a section retargets the link:

  #provider-accounts                  -> configuration/agents.md (117.4; the
                                         map's row, left in configuration.md
                                         this tranche on the DM's direction)
  #what-fails-closed, #merge-and-removal-semantics
                                      -> this guide (117.4; docs/docs-map.md
                                         moved the row here from publishing.md
                                         after tranche 1 was scoped, so the
                                         section is not lifted yet)
  #keeping-the-configuration-outside-the-repository (three uses), #services
                                      -> no row in docs/docs-map.md; they stay
                                         in configuration.md until the map
                                         gives them a home

117.2 retargeted #what-reaches-the-queue to goals.md when it landed that guide.
117.3 retargeted #what-init-proposes-for-checks to runs.md, and
#triage-thresholds and #waiting-out-a-provider-that-refuses to recovery.md,
when it landed those guides.

"The configuration index ... lists the other guides" below is a forward claim:
configuration.md becomes the index in 117.4.

Size: 545 lines against the map's 524-line budget for this guide, covering
exactly the sections the map assigns it minus the merge-semantics row above;
the sections themselves grew after the map's counts were taken.
-->
# Writing a project configuration

What `yoyo init` writes, where the harness looks for it, how the layers combine,
and how to read back what a project actually runs under. Start here if you are
giving a project its own configuration or changing one it already has.

[The configuration index](../configuration.md) lists the other guides.

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
its toolchain. See [What `init` proposes for `checks`](runs.md#what-init-proposes-for-checks)
for what it reads and what it does with an answer it cannot settle. A run with
nothing to verify has no gate to integrate behind, so `yoyo run` refuses one
whatever `init` found; read what it proposed before running work.

`init --json` reports it. `checks` is the list that was written; `detected`
carries every proposal with the artifact it came from, in the three lists the
generated file keeps apart — `checks` written, `candidates` found and not
settled, `alternatives` read and deliberately left out.

### When the repository ignores the configuration

`init` and `yoyo config validate` both ask Git whether the configuration they
just wrote or just read is matched by an ignore rule, and say so when it is.
Nothing fails: the files are there and valid, the exit code is what it would have
been, and the warning goes to standard error.

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
[`init --external`](../configuration.md#keeping-the-configuration-outside-the-repository) for
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
  personas/            # one Markdown file per agent persona
    product-manager.md
    architect.md
    development-manager.md
    developer.md
    reviewer.md
```

Everything under `.yoyodyne/` is machine-independent and belongs in version
control. Run state, provider event streams, locks, worktrees, and the reports
agents file while their work carries on live outside the repository, in the
machine home `~/.yoyodyne`, one directory per project named by its
`product.id`, so nothing there depends on where the project is checked out. The
home's own layout is under
[Layout in the configuration index](../configuration.md#layout), and how a
project is tied to its repository under
[the binding](../configuration.md#one-id-one-repository-the-binding).

Committing it is the default rather than a requirement, and a contributor to a
repository they do not own has two supported ways not to, both under
[Keeping the configuration outside the repository](../configuration.md#keeping-the-configuration-outside-the-repository):
a `.yoyodyne` listed in `.git/info/exclude`, and a configuration kept
outside the repository entirely, which
[`yoyo init --external`](../configuration.md#keeping-the-configuration-outside-the-repository)
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

checks: []          # yours to write; a run with none is refused

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
[effort level](../configuration.md#an-agents-effort-level) that model is asked to
think at, the [provider
account](../configuration.md#provider-accounts) it runs under, an instance count, and a persona file
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
from the name — so `role` must be one of `product-manager`, `architect`,
`development-manager`, `developer`, or `reviewer`, and anything else is
[refused when the configuration loads](../configuration.md#what-fails-closed).
[Talking to the other agents](../conversation.md#talking-to-the-other-agents) states
the table itself.

`backend` is `claude-code` or `codex` unless your project declares one of its
own — and `codex` only for a `developer` agent, since its sandbox cannot hold
the tool access every other role requires. A project running a fork, a proxy, or
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

## Discovery

Yoyodyne looks for a configuration in this order:

1. the path given to `--config`, if present;
2. otherwise the configuration `YOYODYNE_CONFIG` names;
3. otherwise `.yoyodyne/config.yaml`, searching from the current directory
   upwards to the filesystem root;
4. otherwise `.yoyodyne.yaml` in the same directories;
5. otherwise the `config.yaml` in the machine home's project directory whose
   [binding](../configuration.md#one-id-one-repository-the-binding) names the
   repository the current directory is in — a configuration
   [kept outside the repository](../configuration.md#keeping-the-configuration-outside-the-repository).

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
rather than scaffolded somewhere nothing commits. And of the
[machine home](../configuration.md#keeping-the-configuration-outside-the-repository),
which is a declared root like any other: a write that resolves out of it
is refused rather than landing where nothing looks for it.

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
   `triage.stuck_merge_age` (`2h`),
   `triage.review_rounds_cap` (4),
   `approvals.publishing` (`human`), `approvals.work_items` (`human`), an
   agent's `instances` (1), and every value under [`services`](../configuration.md#services):
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
   approved. See [what reaches the queue](goals.md#what-reaches-the-queue).
2. **The built-in bundle**, named by `extends`, and present only if a project
   asks for it. Today the only bundle is `builtin:v1`. It supplies `execution`,
   `approvals`, and the five default agents. It deliberately supplies no
   `product` and no `checks`, because those describe the project rather than the
   harness.
3. **The project configuration**, which overlays whatever it names.

A configuration with no `extends` key — which is what `yoyo init` writes — is a
complete standalone file: it inherits nothing but the harness defaults, and must
declare everything it needs.

`version` is the one field a project never inherits. It must be declared even
when `extends` names a bundle that declares its own, because a version taken
from the bundle would let a file written against a different schema load as
whatever the bundle happened to say — which is what the version exists to
prevent.

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
[merge rules](../configuration.md#merge-and-removal-semantics).

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
never repeated. A project without a
baseline hears nothing until `yoyo config baseline` writes it, which touches
nothing else and starts level. Nothing is adopted for you; `materialize`,
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
