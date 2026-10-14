---
id: machine-home
kind: design
title: 'The machine home: where the harness keeps everything that is not the repository'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-29T02:00:00Z
      reason: 'yoyodyne-ifd.434 - Mason''s direction of 2026-09-28: one home under ~/.yoyodyne, project directories named by product id and bound to a repository, a companion intent repository allowed and not required, and a migration from the platform default with a harness running'
    - action: amended
      by: architect
      at: 2026-10-14T03:32:17.478925Z
      reason: 'yoyodyne-ifd.434.23 and yoyodyne-ifd.434.24: the move section now states what the landed move command does (the old-home warning appears once on each command''s error output and in yoyo doctor, and each moved worktree is repaired from inside it), and a new Validation section states seven checks derived from this design''s requirements, each with its requirement, setup, observed result and kept evidence, plus the first-use measurements; the earlier reference to seven checks named a list this design never held. No requirement or safety boundary is changed.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T03:32:17.478925Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 873, for document-873.1 under the automatic approval policy
---
# The machine home

Serves the configurability goal (roles, policies, and provider selection
configurable without making safety invariants optional) and the autonomy goal.
It extends the state root that
[the v1 harness design](v1-harness-design.md) rules: the key, its precedence,
the one resolver, and the two-roots guard stand, and this design says what the
root's default is and what lies under it.

## One home

The state root's default is `~/.yoyodyne`, on every platform. The state-root
key and `YOYODYNE_STATE_HOME` keep their precedence exactly as ruled, so a
person who set either has asked for another home and gets it; `XDG_STATE_HOME`
keeps its place in that order too, because whoever set it asked for it. The
external configuration home at `~/.config/yoyodyne/projects/<hash>/` ends: a
project's configuration that is not in its repository lives under its project
directory below. Nothing of the harness's is anywhere else on the machine.

The home holds one directory per project and, at the top, only what no single
project owns:

- `projects/<product.id>/` — one per project, below.
- `accounts/` — provider accounts, which serve every project.
- `operator-hold.json` — the operator's hold, which is machine-wide by decision:
  one hold stops every project on the machine, as it does today.
- `logs/` — the harness's own logs.

Anything that today sits beside `products/` under the state root and belongs to
one product moves under that product's project directory; the build classifies
each record by whether one product or the machine owns it, and reports the
classification in its change.

## The project directory

`projects/<product.id>/` holds:

- `config.yaml` — the project's configuration, when it is not committed in its
  repository. A committed `.yoyodyne/config.yaml` is still read where it is.
- `repository.json` — the binding, below.
- `state/` — everything that today sits under `products/<id>/`: runs,
  conversations, spend, the docket, memory, reports, the read model's inputs.
- `worktrees/` — the developer worktrees, which are worktrees of the bound
  repository.
- `intent/` — the companion intent repository, only where the project uses one.

`product.id` names the directory, so it has to be one safe path segment:
lowercase letters, digits, and hyphens, as every id in use already is.
Configuration with any other id is refused at load, naming the id.

## Identity: an id is bound to one repository

A project directory is bound to a repository by the repository's git common
directory (`git rev-parse --git-common-dir`, resolved absolute), recorded in
`repository.json` with the remote's URL where there is one, when it was bound,
and by what. Every worktree of one clone shares that common directory, so a
`yoyo` run from a developer worktree, or from a worktree a person made, is the
same project and never a duplicate.

The binding is written once, by the harness, at the first start against an id
that has no project directory: the directory is created, the binding written,
and the start says so. After that, at every start:

- **The bound repository is this one:** proceed.
- **The bound repository is elsewhere and still exists:** refuse to start,
  naming both paths. Where the two remotes match it is a second clone of one
  project, and the message says this machine runs one clone and names it. Where
  they differ it is two products sharing an id, and the message says which id
  and names renaming. Either way the remedy named is one command.
- **The bound repository is no longer at its path:** refuse to start, naming
  the recorded path and `yoyo project bind`. The harness never re-binds on
  that alone, because a moved clone and an unmounted volume look the same.

The two-roots guard of the state-root design keeps its marker where that design
puts it, and the marker records the home the repository is bound into, so two
processes resolving two homes for one repository refuse as they do today.

Teammates on separate machines each have their own home and their own binding,
so nothing clashes; state is per machine, and what a team shares is the
repository, or the companion intent repository below.

## The commands

- `yoyo project bind` binds the current repository to the project directory for
  its id. It refuses while the recorded repository still exists at its path,
  unless `--replace` is given, which unbinds the earlier one and says so; it
  refuses while any run is in flight for the project. It is the remedy for a
  moved or re-cloned repository.
- `yoyo project rename <old> <new>` moves the project directory and everything
  under it, refusing while a run is in flight or the new name is taken. Where
  the configuration is under the project directory it rewrites `product.id`;
  where it is committed, it requires `product.id` to already read `<new>` and
  refuses otherwise, so the id in the repository and the directory never
  disagree.
- `yoyo project list` names every project directory, its binding, and whether
  the bound repository is present.

Each is recorded in the project's state as an act with who ran it, because a
re-binding is the one thing that can point a project's history at another tree.

## The companion intent repository

A project may keep its intent outside its own repository, so that nothing of
Yoyodyne's is committed there. It is chosen in the project's configuration under
the project directory, never in a committed file, because a committed file is
already something of Yoyodyne's in the repository:

```yaml
intent:
  repository: intent   # relative to the project directory; absent means the project's own repository
```

What moves is exactly the governed set and the tracker: `docs/product`,
`docs/designs`, `docs/decisions` with its invariants, `docs/terms.md`, and the
`.beads/` export. The artifact homes are already configuration, so each home
resolves to a path in the intent repository and every other path resolves in the
project's repository. The intent repository is an ordinary git repository with a
target branch of its own, pushed to and cloned from whatever remote the team
chooses, and `yoyo init --intent` creates it with the product home the
executable ships. Team sharing is git's: a teammate clones both repositories,
and the harness binds their project directory to the first and its `intent` to
the second.

What stays the same is everything the decision record protects. A revision
lands in the intent repository as it lands in the project's today: on a branch,
promoted into the intent repository's target under the one-promotion-per-target
lease, with the revision log's reason as the commit's. The protected-path gate
holds as it holds now: a run's write into a governed home is refused unless the
item grants it, and `.yoyodyne/roles/` stays beyond any grant. What changes is
evidence and reads. A change is evidenced by two recorded commits, the code's
and the intent's, and every record that today carries one repository commit
carries both, named. The repository read a role makes resolves a governed path
in the intent repository and anything else in the project's, and says which it
read from and at which commit. Staleness and the doclink checks follow the same
resolution, so a design that cites code cites the project's commit and a check
that follows the citation reads it there.

Configuration, personas, and the developer-session instructions are then not in
the project's repository. The harness materializes them into every worktree it
cuts, untracked, listed in the worktree's own `.git/info/exclude`, and the diff
gate reads them as not part of the change; a run that changes one of them
changes nothing that lands. A repository with no `.yoyodyne/` at all is the
whole point, so a missing directory there is not a fault.

The build of this is the contributor-mode epic's (yoyodyne-ifd.78), decomposed
once the Lead Product Manager records the goal it serves and releases it.

## The migration, with a harness running

The new build moves nothing on its own. Where the platform's old default home
exists and `~/.yoyodyne` does not, the harness keeps using the old home and
says so once on every command it runs, on standard error, and in `yoyo doctor`,
naming the one command that moves it. The warning is not repeated in
`yoyo status` or in any pass's report: which home the machine uses is a fact
about the machine, every command can say it, and `yoyo doctor` is where a
person looks for a fault in setup. So a build deployed over a watching session
restarts into a harness that finds its state where it left it, and nothing
breaks on the deploy.

`yoyo home migrate` moves it, and refuses before touching anything while any
run is in flight, any session holds the watch lease, or any conversation has a
turn in flight, naming each. It then, in order: creates the home; moves each
`products/<id>/` to `projects/<id>/state/` and writes the project's binding
from the checkout the state names; moves each hashed external configuration to
`projects/<id>/config.yaml`, reading the id from the file; moves the worktrees
under their project and runs `git worktree repair` inside each moved worktree,
so both the worktree's own link to the repository and the repository's list of
worktrees name its new path; rewrites the recorded worktree path on every run
record that names one, as a migration note on the record with the old path kept
beside it, which is the one write to a run record the harness makes outside a
run; moves `operator-hold.json`, `accounts/`, and the logs to the top; and
leaves a marker in the old home naming the new one, so an older `yoyo` started
there says where the state went rather than making an empty root, and the
two-roots guard refuses it as it refuses any second home. A migration that
fails part way says what moved and what did not and is safe to run again,
because every step is a move that is either done or not. Setting
`YOYODYNE_STATE_HOME` to the old home defers the migration for as long as
anybody likes; precedence is unchanged.

## Validation

The validation project (yoyodyne-ifd.434.14) proves this design on a machine
that has never run Yoyodyne. An earlier reference to seven checks in this
design named a list it never held; the seven below are derived from the
requirements above and are the list from this revision on.

Every check starts from the same setup: a temporary directory as the home
directory, with `YOYODYNE_STATE_HOME` and `XDG_STATE_HOME` unset, and a fresh
git repository holding none of the harness's files. A check whose part of this
design has not been built is recorded as not taken, naming the build it waits
on, and never as passed. Evidence is kept with the validation project's record.

1. **One home.** Requirement: One home. Setup: `yoyo init` and a first start in
   the fresh repository; then a second start with `YOYODYNE_STATE_HOME` set to
   another temporary directory. Observed: after the first start, everything the
   harness wrote outside the repository is under `~/.yoyodyne`, and nothing is
   under `~/.config` or the platform's application folder; after the second,
   state is under the directory the variable named. Evidence: a listing of the
   temporary home before and after each start.
2. **A safe product id.** Requirement: The project directory. Setup: a
   configuration whose `product.id` contains a capital letter or a slash.
   Observed: the load is refused, naming the id, and no project directory is
   made. Evidence: the command's output and exit status, and a listing of
   `~/.yoyodyne/projects/`.
3. **The first start binds.** Requirement: Identity. Setup: a first start, then
   a start from a second worktree of the same clone. Observed: the first start
   says it bound the project, and `repository.json` holds the absolute git
   common directory, the remote's URL where there is one, the time, and what
   bound it; the second start uses the same project directory and makes no
   other. Evidence: both outputs, `repository.json`, and `yoyo project list`.
4. **The binding refusals.** Requirement: Identity. Setup: three cases against
   a bound project: a second clone with the same remote, a different
   repository whose configuration carries the same id, and the bound
   repository moved away from its recorded path. Observed: each start is
   refused with the message this design gives for its case, naming the paths
   and the one command that remedies it, and `repository.json` is unchanged in
   all three. Evidence: the three outputs and `repository.json` before and
   after.
5. **The project commands.** Requirement: The commands. Setup: `yoyo project
   bind` while the recorded repository still exists, with and without
   `--replace`, and while a run is in flight; `yoyo project rename` with the
   configuration under the project directory and again with a committed id
   that does not read the new name. Observed: each refusal this design names
   happens and changes nothing; each command that succeeds says what it did;
   and each is recorded in the project's state as an act naming who ran it.
   Evidence: the outputs, the project directory before and after, and the
   recorded acts.
6. **The move.** Requirement: The migration, with a harness running. Setup: an
   old platform home holding one product's state, an external configuration,
   one preserved worktree, and a run record naming that worktree. Observed: the
   new build moves nothing and warns once on each command's standard error and
   in `yoyo doctor`; `yoyo home migrate` refuses while a run is in flight; once
   nothing is in flight it moves everything to the places this design names; a
   git command inside the moved worktree succeeds and `git worktree list` from
   the bound repository shows its new path; the run record carries a migration
   note with the old path beside the new; the old home holds a marker naming
   the new one; an older build started on the old home says where the state
   went; and running the move a second time changes nothing. Evidence:
   listings of both homes before and after, the outputs, the run record, and
   the output of `git worktree list`.
7. **The companion intent repository.** Requirement: The companion intent
   repository. Setup: `yoyo init --intent` and the intent repository chosen in
   the configuration under the project directory. Observed: the governed
   homes and the tracker export are in the intent repository and the project's
   repository holds no `.yoyodyne/`; a document revision lands in the intent
   repository's target branch under the promotion lease; a run's write into a
   governed home without a grant is refused; the configuration the harness puts
   in a worktree is untracked and listed in that worktree's exclude file; and
   the record of a landed change names both commits. Evidence: `git status` of
   both repositories, the landed commit in the intent repository, the refusal,
   and the record naming both commits. Until the contributor-mode epic
   (yoyodyne-ifd.78) builds this, the check is recorded as not taken.

The validation also records three measurements of first use, with no target set
for any of them:

- the time from the empty repository to its first change that an independent
  reviewer accepted and the harness integrated;
- how many decisions were asked of the person on the way, each named;
- every setup failure, with the step it happened at and the message shown.

A measurement that was not taken is recorded as not taken, never as zero.

## What is enforced in code

- One resolver for the home, which every process uses, and the guard against
  two homes for one repository, from the state-root design.
- A `product.id` that is not one safe path segment is refused at load.
- The binding refusals above: second clone, shared id, and missing repository,
  each with its remedy named; a first start binds and says so.
- `yoyo home migrate` refuses while anything is in flight; the new build never
  moves state on its own.
- The intent repository's target is a promotion target under the lease, and the
  protected-path gate applies to its homes as to the project's.
- A record that evidences a change carries both commits where there are two.

## Alternatives rejected

- **The platform's application-support folder**, today's default. Its path is
  opaque and hard to type, and it put configuration and state in two homes,
  which is what made the layout hard to find.
- **Splitting across `~/.config`, `~/.local/state`, and a cache.** Correct by
  the XDG convention and three places to look; the operator's constraint is one.
- **Naming the project directory by a path hash**, today's external
  configuration. A hash survives nothing: a moved clone is a new project. The
  id is what a person knows the project by, and the binding is what makes the
  id safe.
- **Re-binding automatically when the recorded repository is gone.** A moved
  clone and an unmounted volume are indistinguishable at that moment, and
  pointing a project's history at the wrong tree is not recoverable by a
  command.
- **Moving state on the first start of the new build.** A deploy restarts a
  watching session into the new build with runs preserved at recorded paths;
  moving under it would break every re-adoption.
- **Copying intent into the project repository at run time** instead of a
  companion repository. It would make the harness the source of truth, which
  the decision record forbids.
- **Warning about the old home in `yoyo status` and in every pass's report**,
  as this design first said. The build warns on each command and in
  `yoyo doctor` instead, which reaches the person at every use without putting
  a fact about the machine into reports read for other things.
