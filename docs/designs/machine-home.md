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
    - action: amended
      by: architect
      at: 2026-10-17T10:12:04.075342Z
      reason: 'yoyodyne-ifd.434.28: the product moves its own state to the new home. The supervisor stops new work across the machine, lets what is running finish (and, once yoyodyne-ifd.428.99.6 lands, waits runs at a step boundary with their work kept), gives up after a bound rather than starve the factory, moves by the same steps yoyo home migrate runs, finishes an interrupted move from a record of it, and undoes a move whose step keeps failing. yoyo home migrate stays for override. Validation check 6 and the enforced list follow; the build work is listed for the development manager.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T03:32:17.478925Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 873, for document-873.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-17T10:12:04.075342Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1143, for document-1143.1 under the automatic approval policy
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

The new build never moves state while anything is running. Where the
platform's old default home exists, `~/.yoyodyne` does not, and the old home was
reached as the platform default rather than because the state-root key,
`YOYODYNE_STATE_HOME`, or `XDG_STATE_HOME` named it, the harness keeps using
the old home until the move below has finished. So a build deployed over a
watching session restarts into a harness that finds its state where it left
it, and nothing breaks on the deploy.

Until the move finishes, every command says once, on standard error, and
`yoyo doctor` says, that the state is still in the old home, and either when
the product will next try to move it or why it last could not. The warning is
not repeated in `yoyo status` or in any pass's report: which home the machine
uses is a fact about the machine, every command can say it, and `yoyo doctor`
is where a person looks for a fault in setup.

A home that any setting names is never moved by the product. Setting
`YOYODYNE_STATE_HOME` to the old home is how a person keeps it there for as
long as they like; precedence is unchanged.

### The product moves its own state

The supervisor of
[management and supervision](management-and-supervision.md) moves it, in its
periodic pass, starting with the first pass after it has moved onto a build
that carries this design. No person runs a command for it, and nothing moves it
because a surface noticed the old home. It goes in this order.

1. **Check that it can be a move.** Before stopping anything, the supervisor
   checks that the old home and the directory `~/.yoyodyne` would be made in
   are on the same file system, so that every step below is a rename that is
   either done or not. Where they are not, nothing is moved, the harness stays
   on the old home, every command's warning says why, and one note report goes
   to the development manager naming both paths. The product does not try
   again until the build changes.
2. **Start no new work.** The supervisor creates `moving.json` at the top of
   the old home, creating it only if it does not already exist, naming itself,
   the time, and the drain limit. Every harness process that resolves the old
   home reads it before starting anything. While it is there: the scheduler
   starts no run, re-run, pass, or delivery; the dispatchers start no wakeup; a
   message sent to a conversation is kept and answered after the move, and the
   person who sent it is told so in one line; an act from the dashboard or the
   command line that would write state is refused with a message saying the
   state is being moved and to try again after; and the dashboard and Slack go
   on showing what the state holds and say a move is waiting. Where more than
   one product's supervisor runs on the machine, each one, seeing
   `moving.json`, starts nothing new for its own product, and the supervisor
   that created the file is the only one that moves anything.
3. **Let what is running finish.** A run in flight goes on and ends as it
   would have; a conversation turn in flight finishes. Once the single
   function that decides whether and from which step a run resumes
   (yoyodyne-ifd.428.99.6) is on main, a run that reaches the boundary between
   two of its steps while the move is waiting also stops there with its branch,
   worktree, and record kept, and is resumed from that step after the move by
   that same function. That wait is not a stop: it spends no repair and no
   re-run, it is not put on the development manager's list of stopped runs,
   and the run's record says it waited for the move and for how long. A run is
   never ended to make room for the move.
4. **Give up rather than hold the factory.** If anything is still in flight
   when the drain limit has passed since `moving.json` was created, the
   supervisor removes the file, every product starts work again on the old
   home, and the attempt is recorded with what was still running. The drain
   limit is two hours by default and is one of the numeric limits the
   development manager decides under
   [portable agent configuration](portable-agent-configuration.md). The next
   attempt waits six hours. After three attempts in a row that gave up, one
   warning report goes to the development manager naming what was still
   running each time, so that she can change the limit or the work.
5. **Move.** When nothing is in flight for any product whose state is in the
   old home, the supervisor of each product stops its children as it does when
   moving onto a new build, and records in `moving.json` that it has. When every
   product with a running supervisor has, the supervisor that created the file
   takes the watch lease, writes `home-move.json` in the new home listing every
   step of the move below, and carries the steps out in order, marking each one
   done in that record as soon as it is done.
6. **Start again.** It removes `home-move.json`, so the marker the last step
   left in the old home is what an older build finds there. Each supervisor
   then starts its children on the new home. The kept messages are answered,
   the passes that fell due run, and the runs that waited at a step boundary
   resume. The move is recorded in each project's state as an act by the
   supervisor, naming when it started and finished and what it moved, and
   `yoyo doctor` names when it happened.

### The steps of the move

The supervisor and `yoyo home migrate` run these same steps, through one piece
of code. Each step first checks whether it is already done, so running it
again after any interruption changes nothing that is done.

1. Create the home.
2. Move each `products/<id>/` to `projects/<id>/state/`, and write the
   project's binding from the checkout the state names.
3. Move each hashed external configuration to `projects/<id>/config.yaml`,
   reading the id from the file.
4. Move the worktrees under their project, and run `git worktree repair`
   inside each moved worktree, so both the worktree's own link to the
   repository and the repository's list of worktrees name its new path.
5. Rewrite the recorded worktree path on every run record that names one, as
   a migration note on the record with the old path kept beside it. This is
   the one write to a run record the harness makes outside a run.
6. Move `operator-hold.json`, `accounts/`, and the logs to the top.
7. Leave a marker in the old home naming the new one, so an older `yoyo`
   started there says where the state went rather than making an empty root,
   and the two-roots guard refuses it as it refuses any second home.

Nothing is deleted by any step: the move renames, and the old home is left
holding only `moving.json` and the marker.

### If the move is interrupted or fails

A machine that sleeps, a process that crashes, or a power loss can stop the
move between steps. Any harness process that starts and finds `home-move.json`
with a step not marked done does no work in either home. The supervisor, on its
next start, finishes the remaining steps before it starts any child, and then
starts again as above.

A step that fails is tried again after one, five, and fifteen minutes. If it
fails a fourth time, the supervisor undoes the steps already done, in reverse
order, each of them also a rename: moved worktrees are repaired again at their
old paths, and each rewritten run record gains a second migration note
restoring its old path. It then removes `home-move.json` and `moving.json`,
every product starts work again on the old home, and one warning report goes to
the development manager naming the step, the error it gave, and both homes. The
product does not try again until the build changes.

If undoing fails too, the factory stays stopped, and one critical report goes
to the development manager naming the step that failed, both homes, and what
each one now holds. A factory running across two half-moved homes would lose
work, so a stopped one is the safe state. Once the cause is mended, starting
the harness again finishes the move from its record, and so does
`yoyo home migrate`.

### Moving it by hand

`yoyo home migrate` stays, for inspection, recovery, and override, as directing
a role individually does. It runs the same steps, refuses before touching
anything while any run is in flight, any session holds the watch lease, any
conversation has a turn in flight, or the supervisor's move is under way,
naming each, and finishes a move that `home-move.json` shows was interrupted.

### What is built

This section asks for code, for the development manager to admit as build work
against this item:

1. `moving.json`: created only if absent, read by the scheduler, the
   dispatchers, the conversations, and every act that writes state before each
   starts anything, with the one-line answer to a kept message and the refusal
   for an act.
2. The supervisor's move: the check that both homes are on one file system,
   starting the drain, waiting for every product, the drain limit, the
   six-hour wait, the warning after three attempts in a row that gave up,
   stopping and starting children, and the act recorded in each project's
   state.
3. One piece of code for the steps of the move, each step checking whether it
   is already done, shared by the supervisor and `yoyo home migrate`, with
   `home-move.json`, the refusal to do work while it shows a step not done,
   finishing from it on start, the retries, the undoing, and the two reports.
4. A run waiting at a step boundary while a move is waiting, resumed by the
   single function that decides where a run resumes, spending no repair and no
   re-run and not docketed; this waits on yoyodyne-ifd.428.99.6.
5. The warning on each command and in `yoyo doctor` naming the next attempt
   or why the last one could not go ahead.
6. Validation check 6 below, as the validation project's test.

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
   one preserved worktree, a run record naming that worktree, and one run in
   flight, with the supervisor running a build that carries this design.
   Observed: on the deploy nothing moves and each command warns once on
   standard error and in `yoyo doctor`; `moving.json` is written, no new run
   or pass starts, and a message sent to a conversation is answered after the
   move; once the run in flight has ended the supervisor moves everything to
   the places this design names with no command run by a person; a git command
   inside the moved worktree succeeds and `git worktree list` from the bound
   repository shows its new path; the run record carries a migration note with
   the old path beside the new; the old home holds a marker naming the new one;
   an older build started on the old home says where the state went; and
   running `yoyo home migrate` afterwards changes nothing. Three further cases:
   with the drain limit set to one minute and a run that outlasts it, the
   attempt gives up and work starts again on the old home; with the supervisor
   killed after two steps of the move, its next start finishes the move before
   starting any child; and with one step made to fail four times, the steps
   already done are undone and work starts again on the old home with a
   warning report filed. Evidence: listings of both homes before and after
   each case, the outputs, `moving.json` and `home-move.json` as each case
   left them, the run record, the reports, and the output of
   `git worktree list`.
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
- State is never moved while anything is in flight: the supervisor moves it
  only once every product has stopped its children with nothing in flight, and
  `yoyo home migrate` refuses while anything is in flight.
- No run is ended to make room for the move, and a run that waited at a step
  boundary for it spends no repair and no re-run.
- A home named by any setting is never moved by the product.
- While `home-move.json` shows a step not done, no process does work in either
  home except finishing or undoing the move.
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
- **A person running `yoyo home migrate`**, as this design first said. In a
  factory that is never idle the command always finds something in flight, so
  the move needed a person to stop the factory by hand, and the operating rules
  say the product, not a person, moves its own parts.
- **Waiting for a moment when nothing happens to be running.** This factory
  has work in flight at almost every moment, so the move would wait for ever;
  it has to stop new work itself.
- **Ending runs in flight to make room.** The operating rules say a run is
  ended only by a person or a bound on the work itself, and a move is neither.
- **Copying across file systems.** A copy cut off part way leaves a step
  neither done nor not, which is what makes an interrupted move safe to finish
  or undo.
- **Copying intent into the project repository at run time** instead of a
  companion repository. It would make the harness the source of truth, which
  the decision record forbids.
- **Warning about the old home in `yoyo status` and in every pass's report**,
  as this design first said. The build warns on each command and in
  `yoyo doctor` instead, which reaches the person at every use without putting
  a fact about the machine into reports read for other things.
