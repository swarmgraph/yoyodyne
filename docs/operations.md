# Operations and recovery

*For an operator recovering from a stall, a crash, or a provider refusal. Part
of [yoyo's documentation](../README.md#further-reading).*

## Starting the product, and stopping it

A person starts the product once, with one verb, and stops it with one:

```sh
yoyo start    # the supervisor, and through it every part the configuration enables
yoyo stop     # the supervisor, then every part, in order
```

Slack, the scheduler, the dashboard, and the maintenance pass are parts of one
product rather than tools each started by hand, and the configuration's
[`services`](configuration.md#services) section is where a product says which
of them it runs. `yoyo start` starts the product's supervisor — one process per
product, detached into a session of its own so it outlives the terminal — and
the supervisor reads that section and starts every enabled part the way that
part is started alone: the Slack sink exactly as
[`yoyo slack ensure`](#checking-the-installation) starts it, from this product's
own stored tokens into that one process and nowhere else; the scheduler as
[`yoyo work --watch`](work.md#letting-the-harness-choose-the-work) under its own
watch lease. Each start is lease-checked, so a part that is already running is
taken as it is rather than started twice, and a part started here holds exactly
what it holds started by hand. The verb waits for the supervisor to record what
came up and says so, one line per part:

```text
started the supervisor for yoyodyne as pid 48211, logging to …/projects/yoyodyne/state/supervisor/supervisor.log
  slack: running as pid 48214, logging to …/projects/yoyodyne/state/slack/sink.log
  dashboard: enabled, and not yet a child of the supervisor: its adoption is yoyodyne-ifd.414; until that lands, start it with `yoyo dashboard`
  scheduler: running as pid 48215, logging to …/projects/yoyodyne/state/scheduler.log
  maintenance: the supervisor's own pass, every 10m0s; next at 2026-09-29T17:10:00Z
stop it with `yoyo stop`; `yoyo status` says how each part stands
```

The dashboard is declared and not yet started by the supervisor, and the line
says which work adopts it — adopting the dashboard as a supervised part
(yoyodyne-ifd.414); until that lands `yoyo dashboard` is yours, and the supervisor says so rather than starting a part it does not
know how to. The maintenance pass is no process at all: it is
[the supervisor's own periodic pass](#the-supervisors-maintenance-pass), and its
line says its cadence and what its last pass came to.

**A second start while the product is running says so and does nothing.**
Whether a supervisor is running is its lease's answer, an advisory lock the
operating system drops when its holder dies, so a supervisor that was killed
leaves nothing to clean up and the next `yoyo start` simply starts one.

**The supervisor keeps each part running, within bounds.** It looks at every
part every few seconds. A part that dies is started again after a backoff that
doubles from a second and is capped at thirty; a part that dies within two
minutes of a start five times in a row is, on the sixth, left down and shown as
**degraded** — on `yoyo status`'s "Needs a human" line, with the reason the
supervisor recorded and who it is waiting on, and in the channel's hourly lines,
which read the same model:

```text
Needs a human (1):
  the scheduler service is degraded: died 6 times within 2m0s of being started, most recently at 2026-09-19T12:03:00Z, so it is left down — the operator's — the supervisor has stopped restarting it; fix the cause, then `yoyo stop` and `yoyo start` bring it back, or start the part by hand and the supervisor takes it back
```

A part that cannot be started at all — the Slack service with this product's
tokens not stored — is degraded at once with that reason rather than the bound
being spent finding out five times; `yoyo doctor` names what to store. A part
that ran longer than two minutes and then died is not a part that cannot
start, so its count begins again. `yoyo status --json` carries the whole record
under `standing.services`: whether a supervisor is running, and each part's
state, process, log, and reason.

**The supervisor's own death leaves the parts running.** They are processes of
their own with recorded presence — the sink's presence record, the watch
session's holder stamp — and the next `yoyo start` finds each through its lease
and takes it back rather than starting it again; the line says `running,
reattached`. A part somebody starts by hand while the product is up is taken
back the same way, which is also what brings a degraded part back once its
cause is fixed.

**A program manager may ask for a part to be restarted, and the supervisor's
maintenance pass is what acts on it.** A
[program manager](designs/program-manager.md) that thinks one of the four parts
should be restarted ends its reply with one `yoyodyne-restart` block naming the
part and why. The harness writes that down as a durable request under the state
root — at `projects/<product>/state/program-managers/restart-requests.jsonl`,
recording the instance, the part, when, and the conversation turn that asked —
and does nothing else: no instance restarts, stops, or signals a process. A part
the section does not declare is refused naming the four, and each instance has
at most one open request per part: a second while the first is unanswered is
refused naming the first. While it is open the request is carried in
`yoyo status --json` under `standing.program_managers` as the instance's open
`restart_requests`.

The [maintenance pass](#the-supervisors-maintenance-pass) answers every open
request at its next pass, and appends what it did to the request as its
`answer`. It treats a requested restart as a death: the part is stopped,
counted against the same five-in-two-minutes bound, and started again after the
backoff its failures earned, so a stream of requests leaves a part degraded
rather than bouncing it without end. Four things are answered with nothing
done, saying why: a part that is down or degraded is left as it stands; the
scheduler is never stopped, because stopping it cancels the runs it hosts; a
part that is not a process the supervisor starts — the dashboard until it is
adopted, the maintenance pass itself, a part that is off — is named as such;
and nothing is restarted while the provider cannot be reached or is not logged
in. The program manager is told at the time it asks that a request is recorded
and not yet acted on, so it never reports a part as restarted. A request waits
on the pass rather than on you, so it is not on the "Needs a human" line; to
restart a part yourself, `yoyo stop` and `yoyo start` are still the verbs.

**`yoyo stop` stops the supervisor first**, so nothing restarts a part on its
way down, and then the parts in the reverse of the order they were started in,
waiting for each to let go of its lease. A part that is not running is reported
so, and the parts are stopped whether or not a supervisor was running — a
supervisor that died left them running, and this is what stops them. One thing
to know before typing it: stopping the scheduler cancels the runs it is hosting,
as stopping a watch session always has, and [`yoyo reconcile`](#recovering-interrupted-runs)
settles what that leaves. When what you want is for the runs to keep what they
have and carry on later, [`yoyo pause`](#pausing-everything-and-resuming-it) is
the verb and the product stays up.

**The product starts with the machine through a launch agent.** On macOS,
[`yoyo setup`](#setting-up-with-yoyo-setup) ends by offering to install one: a
per-user launchd job, `com.yoyodyne.supervisor.<product>`, under
`~/Library/LaunchAgents`, whose program is the supervisor verb itself —
`yoyo start --foreground --config <this checkout's configuration>`, from the
binary that installed it — rather than a script. It is loaded at once and at
every login, so a machine restart brings the product up with every part the
services section enables, and nothing is typed by hand. Its settings are chosen
for the history of the job it replaces:

- **`AbandonProcessGroup` is true.** The parts the supervisor starts are sessions
  of their own and survive it by design, to be reattached by the next one; a
  launchd job without this setting tears down its whole process group when it
  exits, which on 2026-09-03 killed every watch session and Slack sink the old
  maintenance job had started, 77 times.
- **`KeepAlive` restarts it only after an unsuccessful exit.** `yoyo stop` stops
  the supervisor cleanly and it stays stopped; a crash is restarted by launchd.
  A supervisor refused because another already holds the product's lease exits
  cleanly too, so an agent loaded beside a supervisor started by hand does not
  restart every few seconds for as long as that one runs.
- **Its environment is the installing shell's `PATH`**, any override of where the state is kept
  (`YOYODYNE_STATE_HOME`, `XDG_STATE_HOME`), and `XDG_CONFIG_HOME`, which the
  tools it runs read their own settings by, so the parts find `git`, `bd`,
  `make`, and the provider, and read the same state your own commands do.
  Nothing else is carried, a Slack token least of all. Its output goes to the
  supervisor log `yoyo start` names.

Where the agent is loaded for this checkout, `yoyo start` asks launchd to start
it (`launchctl kickstart`) rather than detaching a supervisor beside it, so the
machine has one way the product is launched. Running `yoyo setup` again reports
the agent already installed; an agent that reads differently — a moved binary or
checkout — is replaced and reloaded after asking, which restarts the supervisor
and leaves its parts running to be reattached. A difference in the `PATH` alone
is not a replacement, so a walk from another shell cannot swap a working agent's
tools out; to change the `PATH` it carries, remove the agent and run
`yoyo setup` from the shell whose `PATH` it should have. A walk answering itself
with `--yes` installs the agent only where `--launch-agent` asks for it. The
step names the command that removes it:

```sh
launchctl bootout gui/$(id -u)/com.yoyodyne.supervisor.<product>
rm ~/Library/LaunchAgents/com.yoyodyne.supervisor.<product>.plist
```

On a platform without launchd, the step says so and `yoyo start` is typed after
each restart.

**Starting the supervisor retires the operator's old maintenance job.** Before
the supervisor, a launchd job of the operator's own, `com.yoyodyne.maintenance`,
ran a script every ten minutes that started and restarted the scheduler,
restarted the Slack sink, started the dashboard, ran `yoyo reconcile`, and
rebuilt `bin/yoyo` after a landing. Beside a supervisor it is a second manager of
the same processes, and the two start, kill, and restart them against each
other: on 2026-09-26 its bounce-when-idle step killed the watch 32 times,
cancelling the pulls and recurring passes in it. So `yoyo start` — and the
foreground supervisor, whichever starts first — boots that job out of launchd
and removes its property list from `~/Library/LaunchAgents`, and says so:

```text
retired the operator's maintenance job com.yoyodyne.maintenance, a second manager of the product's parts: booted it out of launchd and removed …/Library/LaunchAgents/com.yoyodyne.maintenance.plist; it starts, kills, and restarts the scheduler …; the job's script …/yoyodyne-maintenance.sh is the operator's file and is left where it is; nothing runs it any more; recorded in …/projects/yoyodyne/state/supervisor/retired-jobs.jsonl
```

Nothing about it is typed by hand. The record keeps the job's property list
whole, what it ran, and what of the product it duplicated, so what was removed
is written down rather than only gone; the script it ran is left where it is.
Only the job this user's `LaunchAgents` installed is retired: one of the same
label that launchd loaded from anywhere else is left loaded and named, and
[`yoyo doctor`](#checking-the-installation) gives the command for it. A job
that could not be retired does not stop the product starting; the line says
why, and the doctor goes on naming the job until it is gone.

**The one duty of that job the product still needed is the supervisor's own:
rebuilding the binary when the target branch lands.** Where the binary the
supervisor was started from lives inside the checkout that builds it — the
harness developing itself, with `bin/yoyo` in its own checkout — the supervisor
looks every thirty seconds at where the checkout's branch stands and at the
revision the binary was built from, and where the branch has landed something
under `cmd`, `internal`, `go.mod`, or `go.sum` since, it runs `make build` into
that binary and says so in its log. A landing of only documentation builds
nothing, a checkout with uncommitted changes to those paths is not built over
until they are gone, and a build that fails is tried again when the branch next
moves. What takes the build up is the paragraph below: a deploy reaches every
part the supervisor hosts. The job's other steps are not carried: its
bounce-when-idle compared a process's start time with the binary's, which is
wrong for a watch that re-executes itself in place. What the job did for
reconcile is [the supervisor's maintenance pass](#the-supervisors-maintenance-pass);
until adopting the dashboard as a supervised part (yoyodyne-ifd.414) lands, the
dashboard is still started by hand.

**A deploy reaches every part, with nobody restarting anything.** Every thirty
seconds the supervisor reads which build the binary it starts the parts from is
— the revision Go stamped into the file — and asks each part which build it is
running: the sink from its presence record, the scheduler from the watch
session's holder stamp. Where the binary has moved past a part, the supervisor
moves the part onto it, one part at a time, and never in the middle of what
the part is doing:

- **The scheduler restarts itself**, as it always has: the watch session sees
  the binary replaced, stops pulling, waits out the runs it hosts under
  `execution.redeploy_drain_limit`, and re-executes in place
  ([a session draining to restart into a deployed build](#a-session-draining-to-restart-into-a-deployed-build)).
  The supervisor waits for it rather than stopping it, since stopping it would
  cancel those runs. The moment the session lets its lease go to re-execute is
  read as that restart rather than as a death, and a session that has not taken
  its lease back thirty seconds later is started from the binary.
- **The Slack sink is restarted by the supervisor**, between two passes over
  the records and never while it is answering a product manager turn in a
  thread. The sink makes each pass under a lease of its own; the supervisor
  waits while a pass holds it, then takes it itself for the length of the stop,
  so the sink starts no new pass while it is being stopped, and lets it go
  before the sink is started again from the binary. A stop landing inside a
  pass could post a message whose cursor was never written, and the sink in its
  place would post it again; waiting the pass out is what rules that out.
- **The dashboard** is moved the same way as soon as it is a child of the
  supervisor, which is adopting the dashboard as a supervised part
  (yoyodyne-ifd.414); until then it is started by hand and
  restarted by hand. A part can only become a child by saying which build it
  runs and what it is in the middle of — a test over the parts the supervisor
  starts fails for one that does not — so the dashboard cannot be adopted
  without a deploy reaching it.

A part somebody started by hand, and the supervisor took back, is moved exactly
as one the supervisor started. Every move is recorded as a restart and not as
a death — the part's `restarts` and `restarted_at` in the record, apart from
the `failures` the backoff and the five-in-two-minutes bound count — so a day
of deploys never leaves a part degraded, and a part that dies on its own with
no deploy behind it is still a death.

`yoyo status` prints the parts under the four lines, one line each in the words
`yoyo start` uses, saying which build each is on and since when, and where a
deploy is moving one:

```text
Services (supervisor running as pid 48211; the binary on disk is build 3d3d367a1b2c):
  slack: running as pid 48214, logging to …/projects/yoyodyne/state/slack/sink.log, on build 3d3d367a1b2c since 2026-09-28 09:40 PDT (restarted into a deployed build once)
  dashboard: enabled, and not yet a child of the supervisor: its adoption is yoyodyne-ifd.414; until that lands, start it with `yoyo dashboard`
  scheduler: running as pid 48215, logging to …/projects/yoyodyne/state/scheduler.log, on build 1a2b3c4d5e6f since 2026-09-27 20:56 PDT; on build 1a2b3c4d5e6f, behind the deployed 3d3d367a1b2c; the watch restarts itself into it between runs, waiting out the runs it hosts under execution.redeploy_drain_limit
  maintenance: the supervisor's own pass, every 10m0s; last pass at 2026-09-28T16:30:00Z (4 step(s) ran, 2 skipped, 0 failed); next at 2026-09-28T16:40:00Z
```

The services section also shows the last machine sleep and wake, in local time
with the zone named, and the last recorded interval without the harness
watching. On macOS the supervisor reads `pmset -g log` about once a minute and keeps
the OS transitions in `projects/<product>/state/supervisor/machine.jsonl`, including
sleep and wake while it was down. Dark wakes count as wakes because processes
can run during them. The same log records whether the scheduler held its lease
when the supervisor looked. Sampled downtime is counted only between consecutive
looks no more than two minutes apart. Collection waits at least a minute between
looks; the shared two-minute observation window allows the supervisor's
five-second poll after each tick, processing time and timer delays. A down sample
without a following look, or a gap beyond that window, leaves the unsampled
interval unknown; absence of a look is not proof that the harness was down. The
services section names the last such gap separately from recorded downtime.
An unavailable OS history or a product with no observations says so. On other
platforms OS sleep history is currently unavailable. A sleep without a matching
wake is shown with its duration unknown, rather than counted through the present.

The JSON reading carries these values under `standing.services.availability`:
`last_sleep`, `last_wake`, `last_gap` (a duration in nanoseconds), and `problem`
where the history could not be read whole. `observation_problem` describes the
last gap in scheduler observations or an unrecorded restart. Watch-session stop
and start records give precise downtime boundaries when they agree with the
supervisor's looks. A stop without a recorded opening establishes that the
scheduler was down at the stop, rather than continuously down through the
present. Later looks that find it watching also override a stop whose opening
was recorded late. Bounded consecutive observations still count as downtime;
unsampled portions remain unknown, and an unrecorded restart's time is reported
as uncertain. A missed pass after the scheduler was observed watching is not
attributed to that earlier stop.

A product no supervisor has run for prints no such line. `--json` carries the
same under `standing.services`: the binary's build as the record's `deployed`,
and each part's `build`, `build_since`, `restarts`, `restarted_at`, and, while a
move is under way, `restarting_into` and `redeploy`. A part the supervisor found
already running is named on its build with no date, because it moved there
before the supervisor looked; the date appears once the supervisor has seen it
move. A part whose build cannot
be read — one that recorded none, such as a binary built without Go's stamp —
is compared with nothing and moved by nothing, and its line names no build.

### The supervisor's maintenance pass

The maintenance part of the services section is the supervisor's own periodic
pass rather than a process it starts. It is what the operator's hand-rolled
maintenance job did every ten minutes, taken by the resident instead, on the
cadence the configuration sets:

```yaml
services:
  maintenance:
    enabled: true
    every: 10m     # the default; a minute is the shortest
```

Each pass takes six steps, in this order, and each is recorded:

- **provider** — whether the provider is answering, read from the product's
  [outage record](#waiting-out-a-provider-nobody-can-reach). While it stands,
  the supervisor restarts nothing it would choose to restart: no part is moved
  onto a deployed build, no requested restart is carried out, and the
  supervisor does not take a build up itself. A restart cannot renew a login or
  bring a network back, and it can kill a process that is waiting one of them
  out; on 2026-09-17 the old job restarted the watch 158 times over an expired
  login. A part that dies is still started again, because a part left dead
  through an outage is one nothing is left to notice the outage ending.
- **reconcile** — [`yoyo reconcile`](#recovering-interrupted-runs), run from the
  supervisor's own binary: it settles what interrupted runs left behind,
  converges the checkout and the worktrees, and takes the
  [stall reading](#when-nothing-happened-at-all). It runs beside the
  supervisor's looks at the parts rather than holding them up, and is marked
  as the pass's so it is not [counted as a hand step](#counting-your-hand-steps).
- **rebuild** — what the supervisor's rebuilder last came to. It builds the
  binary on its own thirty-second look when the branch lands something the
  binary is made of (above), so a deploy has a build to take up.
- **restart-requests** — the restarts program managers asked for, each answered
  on the request with what was done (above).
- **redeploy** — the deployed build taken up. The parts are moved onto it by the
  supervisor's own looks, one at a time; what this step adds is the supervisor
  itself, which re-executes into the new build once the pass is recorded,
  leaving every part running to be reattached. It waits a pass where a part is
  still being moved.
- **slack** — where the sink stands: `yoyo slack ensure`, which the supervisor's
  own look at the sink already takes every few seconds.

The pass writes no tracker status of its own. The one step that touches the
tracker is `yoyo reconcile`, whose every write is a settlement the harness
records with a note on the item; nothing here runs `bd`, so there is no
`bd update --status` for anybody to miss, which is what an operator's script
beside the old job did to four items on 2026-09-18. And the scheduler is never
stopped by it: a watch session takes a deployed build up itself, between the
runs it hosts, under [`execution.redeploy_drain_limit`](#a-session-draining-to-restart-into-a-deployed-build),
exactly as it does with no supervisor at all. The old job's bounce-when-idle
step, which cancelled a live run, is not carried.

Every pass is a record in the sweep log beside the recurring tasks', under the
name `maintenance`, and [`yoyo sweeps`](#reading-what-the-recurring-tasks-found)
shows it as the supervisor's own, one line per step, with a step that was
skipped or failed in capitals and saying why:

```text
2026-09-29T17:00:00Z  maintenance (the supervisor's own pass, no role woken)
  3 step(s) ran, 3 skipped, 0 failed
  - provider: ran, answering
  - reconcile: ran, yoyo reconcile exited 0: settled 0 runs
  - rebuild: ran, /Users/you/github/yoyodyne/bin/yoyo is built from main's tip 3d3d367a1b2c
  - restart-requests: SKIPPED, no program manager has an open restart request
  - redeploy: SKIPPED, every part is on the deployed build 3d3d367a1b2c; nothing to take up
  - slack: SKIPPED, the sink is not enabled in the services section
```

`yoyo sweeps --task maintenance` reads them alone. A pass with a failed step
carries the failures as its problem and on its cadence's claim, so a pass that
runs and achieves nothing is findable rather than quiet. The cadence is kept
under the same claim a recurring task's is, so two supervisors cannot take one
pass twice, and a recurring task may not be named `maintenance`. A product
whose maintenance part is off gets none of this: `yoyo reconcile` is then yours
to schedule, and the supervisor stays on the build it was started from until it
is restarted.

**Retiring the hand-rolled scripts.** With the launch agent installed and the
maintenance part on, nothing the operator's two scripts did is left to them.
`~/.local/yoyodyne/yoyodyne-maintenance.sh` was what the retired
`com.yoyodyne.maintenance` job ran: its reconcile, its rebuild, and its
`yoyo slack ensure` are the steps above, and its restarts are the supervisor's.
`~/.local/yoyodyne/carry-out-queue.sh` opened items with a bare
`bd update --status=open` and then ran `yoyo triage rerun` on decisions already
recorded; a watching session carries out the development manager's recorded
decisions itself, and a re-run claims its item and clears a stale blocked status
with a note as it does, so nothing has to open an item ahead of it. Both can be
deleted, along with whatever schedules the second — a launchd job or a crontab
line of your own:

```sh
launchctl list | grep -i yoyodyne    # the supervisor's agent is com.yoyodyne.supervisor.<product>; anything else is yours
crontab -l | grep -i yoyodyne
rm ~/.local/yoyodyne/yoyodyne-maintenance.sh ~/.local/yoyodyne/carry-out-queue.sh
```

**Three failures in a row tell the roles without anyone reading this log.**
After a third failed maintenance pass, the harness files one warning in the
existing report pile. The finding names the pass, how many failures have
followed one another, when they began in the operator's local time with the
zone named, and the latest error on one line. The factory-flow program manager
receives it in her next pass and must answer it in her account and existing
digest and lane report; the development manager receives it too and owns
resolving the cause. Where no factory-flow program manager is configured, the
development manager watches it as well, and the finding says why.

The dashboard's **Factory problems** section and `yoyo status` carry the same
finding from the shared read model. Further failures raise its count without
filing another report, and the status line names it even after reaching its
ordinary listing limit. Completing the watching role's pass or handling the
report does not clear it: only a later maintenance pass that carries out its work does. The
harness records the clearing in the report's handling log with the total
number of consecutive failures. A later run of failures is a new finding.
If filing or recording the clearing fails, a later pass retries both from the
sweep log, even if the affected pass succeeded before the finding was filed.
This applies to the recurring role passes recorded in the same sweep log too,
including failures before their first turn and failures during a turn. A role's
pass counts as failed only when a turn's actions were not carried out, it never
started, it was stopped under it, or it gave no account at all. A pass that
carried out its actions and says more work is waiting is partial, not failed:
it ends a run of failures and clears an existing finding as a finished pass
does, because a role whose queue is never empty would otherwise hold a finding
that can never clear. A reply that wrote its report block more than once is
read by its last block and is not a failure either. A held conversation or a
cadence that never fired neither counts nor clears; missed cadences and
provider waits do not count as failed executions.

Each line about a failing pass, on the dashboard and in `yoyo status`, starts
with what went wrong in ordinary words — for example "the architect asked the
developer a question, which it may not do, so the actions of turn 4 were not
carried out" — and then gives the count, the start time, and the record's own
text.

An error does not by itself make the finding the operator's. If the Lead
Product Manager records a report handling as needing a step only a person can
take, that handling must supply `person_only` with a permitted reason, its
target, and the exact step. The reasons are `credential`, `repository-setting`,
and `protected-file`; the last accepts only provider-refused settings files
and files under `.yoyodyne/roles/`. An operator flag or ordinary repair prose
alone cannot transfer ownership to the operator. The factory-flow program
manager still watches, and the development manager still resolves the cause.

## Setting up with `yoyo setup`

`yoyo setup` walks a project to an installation that can run work, as
questions: the tracker, the configuration, the checks read from what the
repository already declares, the tracker's sync remote, the index at the door of
each artifact home, the optional offer of
[reporting into Slack](reporting.md#reporting-into-slack), and, on macOS, the
[launch agent](#starting-the-product-and-stopping-it) that starts the product
with the machine, ending with `yoyo doctor`. Everything it does is something you could have typed, and it
asks before each step.

It changes nothing that is already there — a configuration that does not load is
handed back rather than regenerated, a sync remote the tracker already holds
keeps pointing where it points, and a Slack token already stored is left alone —
and it keeps no record of its own, which is what makes running it again safe:
every step looks at the installation first, says what was already true, and
resumes an interrupted setup where it actually got to rather than where a record
claims. `--yes` answers every question with the answer it proposes; the one
prompt it cannot answer for you is the keychain's own, which waits for each
Slack token to be typed. `--json` on its own asks nothing and changes nothing: it
reports the same steps machine-readably, saying what is already true and what
would still have to be done. `yoyo setup --yes --json` carries a walk out with
nobody at the terminal, and leaves the keychain step, and only that step, to a
walk somebody is watching.

## Checking the installation

`yoyo doctor` answers one question — can work actually run here — and answers it
before anything is spent rather than at the point a run discovers it cannot:

```sh
yoyo doctor            # everything it looked at, healthy or not
yoyo doctor --quiet    # only what is wrong
yoyo doctor --json     # the same findings, for something automating the repair
```

It looks at the `yoyo` on your `PATH` and whether it is the build you think it
is, Git and whether this project is a repository with something to branch from,
the tracker and whether it answers *here*, the configuration — and whether the
repository's own `.gitignore` keeps it from every clone and worktree (see
[when the repository ignores the configuration](configuration.md#when-the-repository-ignores-the-configuration)) — the deterministic
checks and whether this machine can run the programs they name, Node where the
product ships the dashboard — whose page only Node can draw, so a machine
without it fails the page's render check rather than passing quietly — each provider
your agents name — installed always, and authenticated where the harness has an
adapter that can ask, which today is Claude Code — whether a provider key is
exported in this shell, which is an authentication the harness no longer
carries, whether every agent runs on
one model with nothing to fail over to, forge access when the project publishes,
when reporting is on, this project's own Slack secrets and the sink that is
supposed to be using them, and each part the [`services`](configuration.md#services)
section declares — off, on with what it needs stored, or on with it missing.

**Every finding that is not healthy carries a remedy, and a remedy is a
command.** That is the whole difference between this and a status listing: what
it prints under a problem is what to run. `--json` carries the same findings with
the same remedies, which is what [the setup and repair
prompt](../skills/yoyo-setup/SKILL.md) has your own agent session act on rather
than parsing any of this.

```text
yoyodyne cannot run work: 2 problems, and 1 warning worth knowing about

problem  tracker                bd is installed but could not read this project's issues
                                fix: bd init
ok       checks                 4 checks configured, and every command resolves here
problem  provider:claude-code   claude is installed but not authenticated, so every agent invocation would be refused
                                fix: claude auth login
warning  slack-sink             no sink is running for this product, so nothing is being reported
                                fix: SLACK_BOT_TOKEN="$(security find-generic-password …
```

Findings come in the order you would fix them in — the tools, then the project,
then what the project turns on — rather than worst first, because the first
problem in the list is usually why the ones under it are problems too. `--quiet`
drops the healthy ones and changes nothing else.

The configuration finding is one of two the same way. A project with no
configuration is told to run `yoyo init`. A configuration that is there and does
not load is somebody's edit, which `yoyo init` refuses to write over and
`yoyo init --force` would delete, so its remedy opens that file in your editor
instead, and the detail says why it does not load. Where `YOYODYNE_CONFIG` names
a file that is not there, the remedy is to unset it.

The tracker finding above is the initialized-here half of two. A machine with no
`bd` on it at all gets the other, and its remedy is the tracker's own installer,
fetched from [the one home Beads has](https://github.com/gastownhall/beads):

```text
problem  tracker                bd is not installed, and every role reads and writes the tracker
                                fix: curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash
```

That is the same repository the README, the install script, and the adoption
walkthrough send you to, and it is deliberately not a `go install` line: the
tracker moved to that home from `steveyegge/beads` and its released modules
still declare the old path, so `go install` of a path under the new home fails
on the mismatch, and a bare `go install` of the old one takes a build its own
documentation calls unsupported — which is what turned every pull request here
red on 2026-09-05. [The diagnosis](diagnoses/yoyodyne-ifd-125-6-beads-home.md)
has the evidence. `yoyo setup` hands you the same command, since setup does not
install tools.

A healthy installation says so in as many words, because an empty list of
complaints and a check that never ran read the same. It exits 1 when something
would stop work running and 0 otherwise.

**A warning is not a small problem — it is something about an installation that
works.** The `yoyo` on your `PATH` having drifted from the one you are running is
one. **Every agent on one model, and none naming an alternate** is another, under
`failover`:

```text
warning  failover               every agent runs on one model, opus on claude-code, and none names an alternate
                                a capacity window closing on that model stops every role at once until it lifts, and nothing fails over; set failover.enabled: true and failover.model on each agent …
                                fix: ${EDITOR:-vi} .yoyodyne/config.yaml
```

Nothing about it stops a run today. What it costs is paid the day that model's
window closes: on 2026-09-08 the seven-day limit on the one model all five
agents ran on closed with a reset five days off, no agent named an alternate,
and the harness waited the whole window out. [Failover](configuration.md#serving-a-turn-from-a-permitted-alternate-model)
had shipped, off by default so that each agent's alternate is a choice somebody
made, and this project had never turned it on — a condition that was in the
configuration the whole time and is one line to state. The finding is healthy
once any agent names an alternate, or the agents run on more than one model,
and the healthy line says how far the cover goes.

**Every reporting finding is a warning too**, and deliberately so: reporting is an
observation and never a gate, so a sink you never started, a workspace that is
down, and a token nobody stored all leave an installation that runs work exactly
as it would have. They are still named, in full, with the command that ends each
one — what the exit status refuses to do is fail a machine that works.

**So is every service finding.** Each part the configuration's
[`services`](configuration.md#services) section declares gets a line of its
own — `service:slack`, `service:dashboard`, `service:scheduler`,
`service:maintenance` — saying it is off, or on with what it needs in place, or
on with what it needs missing: the Slack service without this project's two
tokens stored, the dashboard with a `keychain` or `file` token that is not in
the store its entry names. The remedy is the command that stores it, and a
part that cannot start reports nothing or serves nothing rather than stopping a
run, which is why none of these is a problem.

**A launchd job outside the product that manages its parts is a warning too**,
under `maintenance-job`, on macOS: the operator's old
`com.yoyodyne.maintenance` job, loaded or installed to load at the next login,
named with its property list and each part it duplicates, read from the script
it runs:

```text
warning  maintenance-job        the launchd job com.yoyodyne.maintenance is loaded, a second manager of the product's parts beside the supervisor
                                it starts, kills, and restarts the scheduler (`yoyo work --watch`), which the supervisor's scheduler child does; …
                                fix: yoyo start
```

The remedy is the verb because the verb is what retires it
([starting the product](#starting-the-product-and-stopping-it)); the doctor
itself only asks `launchctl` and reads the files.

**A running part whose build cannot read the configuration is named, under
`config-readers`.** The configuration finding says the build making the
diagnosis reads the file; a part of the product started days ago runs the build
it was started from, and a key a landing has added since is one that build
refuses — the whole file, on every read. On 2026-09-28 the effort lines reached
the main checkout at about 11:10 AM Pacific while the dashboard ran a build
from 4:36 PM two days earlier, and the dashboard showed nothing but `field
effort not found in type config.agentDocument` until it was restarted at 1:28
PM; the watch and the installed binary had been checked, and the dashboard had
not. So each long-running part — the supervisor, the Slack sink, the scheduler
(`yoyo work --watch`), and the dashboard — records as it starts which build it
is, which file it reads, and every configuration key its build reads, under
`projects/<product>/state/config-readers/` in the state root, with one record per
process and start time. For the same service and process id, only the latest
startup record is compared: the watch and supervisor keep their process ids
when they restart into a deployed build, and the new account supersedes the
previous build's record. Starting another dashboard keeps the first dashboard
in the comparison while both processes remain running. The doctor reads the
records of every live instance, reads the file each one reads as it stands
now, and names every key in it that part's build would
refuse, with the part, its build, and its process:

```text
warning  config-readers:dashboard  the dashboard service, running build 0364141b2c3d as pid 4242 since 2026-09-26 16:36 PDT, cannot read agents.developer.effort in …/.yoyodyne/config.yaml, so every read it makes of the configuration fails
                                nothing restarts the dashboard onto a newer build until the supervisor adopts it (the dashboard's adoption, yoyodyne-ifd.414), so stop it and start it again with `yoyo dashboard`
                                fix: kill 4242 && yoyo dashboard
```

The comparison needs nothing of the older build but the list it recorded, so a
newer doctor answers it for a part whose types it no longer has. A key it
cannot read is named once, as the key the decoder stops on, and nothing under
it is looked at. A mismatch on the scheduler is a problem, because a watch that
cannot read the configuration chooses nothing; on any other part it is a
warning, for the reason every service finding is. The remedy restarts the part
now; the watch restarts itself into a deployed build between runs, and the
supervisor restarts the sink between its passes and takes up the deployed
build itself after its maintenance pass is recorded, so on those parts the
command is for not waiting. With nothing mismatched the finding is healthy and names
each running part and its build. A part started from a build older than the
record writes none, and the comparison says nothing about it rather than
calling it current. `yoyo config validate` says the same on its standard error
and under `unreadable_by_running` in `--json`, without moving its exit code,
and `yoyo status` carries each one on its fourth line (below), as the
harness's move. A landing makes the same comparison against the configuration
as the integrated commit holds it, read from Git rather than from the primary
checkout, which can still be behind the forge's merge. Each running part that
cannot read it is named on the landed item, in a note that opens
`Running parts that cannot read the configuration this landing left:`, and
under `config_mismatches` in the run's
outcome or the sweep's result, so a landing that adds a key names the parts it
leaves behind when the harness records the landing, rather than when somebody
next opens the dashboard. A part that reads a file outside the repository is
compared against that file as it stands, since no landing changed it. The
landing also compares
the shipped templates at the previous and integrated commits. Keys newly
introduced there are compared against every running part's recorded schema,
even when its active configuration has not adopted them. Incompatible parts
are named separately, with their builds and the new keys, in a note opening
`Running parts that cannot read new keys in shipped templates:` and in the
outcome or sweep's result under `template_config_mismatches`. This warns that
adopting the keys would make those builds fail to read the configuration; it
does not say the file they read now is broken. Changing a value or repeating an
existing key on another agent introduces no key. Both comparisons include keys supplied
by YAML merges, with explicit values and earlier merge sources taking precedence.
When the forge queues a merge, both comparisons wait for confirmation that it
landed. Reconciliation then makes them before settling the item, using the
run's recorded previous revision and the confirmed merge commit, even if
another change has since advanced the target branch. Every comparison is
saved on the run before attempting to record its finding on the item, including
any failure reading the configuration or template. If that write fails, the
saved comparison remains an outstanding delivery for the next sweep, even if
an immediate landing has already settled its item and removed its worktree.
`yoyo status` names that delivery under **Waiting on the harness**, with the
recording failure; `yoyo reconcile` retries the saved account without replacing
it with a comparison of services or files that changed afterwards. A queued
merge keeps its item settlement outstanding until delivery succeeds. A merge
whose confirmation failed is compared when a later sweep confirms its
publication.

It changes nothing. Nothing here installs, authenticates, restarts, or edits a
configuration, and no credential is ever read: whether a secret is stored is
asked in the form that answers without producing the value.

The two checks worth calling out are the ones that catch an installation that
was working and stopped. **A long-running sink is started from a binary that
keeps moving underneath it**, so the build that is reporting and the build that
is installed drift apart with no event between them — nothing fails, nothing is
logged, and the milestones added since it started are simply never posted, which
in a channel reads as a quiet week. The version is asked first, because that is
what you installed by; the revision behind it is asked second, because on a
harness developing itself the version cannot answer at all. Every unreleased
binary reports the same version, so a sink started last week and the binary
diagnosing it compare as identical — a clean report over exactly the drift being
looked for. Two revisions are two places in one history, so where the versions
agree and the revisions do not, the revisions settle it; and where there is no
pair of revisions to compare, the finding says the comparison was not made rather
than reporting the versions agreeing as if it had been. And **on a machine running more than one
harness, "a Slack token exists" is true for all of them and right for at most
one**, so what is checked is this project's own pair under names that carry the
product, and whether the sink that is running was launched with them. See
[Reporting into Slack](reporting.md#reporting-into-slack).

A stopped sink is the one finding here you need not act on by hand. On macOS,
`yoyo slack ensure` starts one if nothing is reporting for this product, from
this product's own keychain items, and does nothing when a sink is already
running. With the Slack service enabled in the
[`services`](configuration.md#services) section, that is what
[`yoyo start`](#starting-the-product-and-stopping-it)'s supervisor does for the
sink — the same lease-checked start, made again whenever the sink dies, within
the supervisor's bounds — so on a product that has been started the finding
clears itself. The drift above clears itself the same way there: the supervisor
[moves the sink onto each build deployed over it](#starting-the-product-and-stopping-it). The verb is still there for a product nobody has started, and
for a pass of your own. `yoyo doctor` only diagnoses — it changes nothing, and
starting the sink is the other command's job.

### Which provider authentication is supported

**A provider authenticates by its own login, held in its provider home.** That
is the one authentication an invocation the harness makes receives: `claude auth
login`, `codex login`, or — for a pooled account, which has a home of its own —
the same login with that home named, which is the command the `account:` finding
hands you.

**A key exported in a shell is not it.** `ANTHROPIC_API_KEY`,
`CLAUDE_CODE_OAUTH_TOKEN`, and `OPENAI_API_KEY` each read as a credential, and
every process the harness launches is given an environment built from an
allowlist with credentials dropped from it — so none of the three reaches a
provider invocation, a check, or a Git command. It used to: before
`yoyodyne-ifd.408` an invocation inherited the harness's whole environment, so a
key in a shell profile authenticated every one of them. An installation relying
on that does not degrade when it stops working; the provider refuses its next
run.

So the state is named before that run happens, under `provider-authentication`:

```text
warning  provider-authentication  ANTHROPIC_API_KEY is exported here and reaches no invocation the harness makes
                                  every invocation is built from an allowlist and a name that reads as a credential is dropped from it, so an installation authenticating a provider this way is refused by that provider rather than degrading; provider authentication is the provider's own login, held in its provider home
                                  fix: claude auth login
```

It is a **warning** rather than a problem, by the question this command separates
the two with. A key exported beside a provider that is signed in stops nothing —
the login is what authenticates and the key is dead weight. A key exported
*instead* of one stops everything, and that is already the `provider:` finding's
problem; naming it twice would count one broken installation twice. Read the pair
together: `provider:claude-code` saying the provider is not authenticated, with
this beside it, is the whole story — you can run `claude` in your own shell, and
the harness cannot.

Two other surfaces say the same thing, so it is not only found by somebody who
thought to run a diagnosis. `yoyo config validate` says it beside the validity
answer, on standard error, without changing the exit code, and carries the
variable names under `provider_keys` in its `--json`. And `yoyo slack` says it
once when the sink starts — the shell that starts a sink is usually the shell the
harness was started from, and that process is the one an operator leaves running.
All three name the variables and never their values.
[The environment a check runs in](configuration.md#the-environment-a-check-runs-in)
is where the allowlist itself is stated.

### Where the state is, and moving it

Everything the harness records — runs, conversations, worktrees, reports, the
pause below — lives under one directory outside the repository, the state root.
It is `YOYODYNE_STATE_HOME` where a shell exports it, otherwise the
`state_root` in this machine's `~/.yoyodyne/machine.yaml`, otherwise
`$XDG_STATE_HOME/yoyodyne`, otherwise the machine home, `~/.yoyodyne`. Where
`~/.yoyodyne` does not exist, or holds only `machine.yaml`, and the earlier
builds' default does (`~/Library/Application Support/Yoyodyne/state` on macOS),
the earlier one is kept, because that is where the state is; nothing moves it on
its own. A `machine.yaml` left in `~/.config/yoyodyne`, where earlier builds
read it, is not read.

Inside it, each project has a directory of its own, `projects/<product id>/`,
holding `repository.json` — which repository the id is bound to — and the
project's `state/` and `worktrees/`, with the configuration and its personas
beside them where the repository does not carry its own. The operator's pause
and the provider accounts are at the top, because they serve every project.
`yoyo project list` names every project directory, what it is bound to, and
whether that repository is still there; a start from a second clone, from
another product using the same id, or against a bound repository that has gone
refuses and names `yoyo project bind` or `yoyo project rename`, which
[the configuration guide](configuration.md#a-product-id-names-one-repository-on-the-machine)
describes. Every part of the
product resolves it the same way, and it is never set in the project's own
configuration; [`state_root`](configuration.md#where-the-harness-keeps-its-state-state_root)
is the whole of the setting. `yoyo doctor` says which directory it is and which
of those four put it there, under `state`:

```text
ok       state                  the durable records live in /Users/you/.yoyodyne, from default; the marker in /Users/you/src/example/.git/yoyodyne/state-root agrees
```

The first process that opens the root for a product records it in
`.git/yoyodyne/state-root` of the product's checkout, and from then on a
process that resolved a different root refuses to start rather than keeping a
second, divergent copy of the product's state. The refusal names both roots,
the layer the new one came from, and the marker. The usual cause is a shell
or a launch job carrying a `YOYODYNE_STATE_HOME` the rest of the product does
not have. Unset it, or correct the setting, and the refused command runs.
`yoyo doctor` reports the same disagreement as a problem before anything
refuses. The refusal and the finding both say which process recorded the
marker and when — its process id, its command line, and the moment, kept
beside the marker in `.git/yoyodyne/state-root.writer` — so a marker you did
not expect can be traced to what wrote it. A marker recorded before that was
kept says it names no writer, and gives the marker file's own time.

**A marker naming a root that no longer exists is stale, and one command
clears it.** It happens when whatever recorded the marker was working on a
temporary state root that has since been deleted, or when the state was moved
and the marker left behind. There is no state at the root it names, so nothing
is split, but every command from the checkout still refuses to start until the
marker is replaced. The refusal says the root is gone and names the command,
and `yoyo doctor` gives it as the remedy under `state`:

```text
problem  state                  this checkout's state-root marker names /private/var/folders/…/deleted-root, which no longer exists, so every command from here refuses to start
                                fix: yoyo state-root rebind
```

`yoyo state-root rebind`, run from the product's checkout, records the root
this shell resolves in the marker's place, and says what it replaced and who
had recorded it. It replaces only a marker whose root is gone: a marker naming
a root that is still on disk is the product's state, and rebind refuses it with
the same refusal as every other command, because moving off state that is there
is the deliberate move below. A marker that already agrees is left as it is.

No test writes a marker into a real checkout. Inside a test binary the harness
refuses to record one anywhere outside the temporary directory, so a test that
ran a command against this repository's own configuration fails instead of
leaving the primary checkout's `.git`, which every worktree of it shares,
holding a marker that names a deleted temporary directory.

Moving the state on purpose is four steps, in this order:

```sh
yoyo stop                                   # nothing may be writing while it moves
mv "$HOME/.yoyodyne" /Volumes/work/yoyodyne-state
mkdir -p ~/.yoyodyne && printf 'state_root: /Volumes/work/yoyodyne-state\n' > ~/.yoyodyne/machine.yaml
yoyo state-root rebind                      # in the product's checkout: the old root is gone now
yoyo start
```

Another product on this machine shares the root unless it is moved too. Each
product keeps its records under `projects/<product id>/state/` (under
`products/<product id>/` in a root the earlier builds laid out, which is read
that way until it is moved), each product's checkout has its own marker, and the pause is kept at the root, so on a shared
root one pause stops every product on it. Moving one product's state gives it
a pause of its own. Moving every product's state together keeps one pause, and
that means following the steps above in each product's checkout.

## Pausing everything, and resuming it

`yoyo pause` stops everything the harness would spend on a provider, and
`yoyo resume` starts it again:

```sh
./bin/yoyo pause      # to conserve tokens, or for any other reason of your own
./bin/yoyo resume     # everything parked on it carries on
```

It is one durable switch over the whole machine rather than one item. Every
provider-call boundary reads it before it spends — a developer attempt, each
reissue of one after a refusal, a reviewer invocation, a conversation turn — so
a pause placed while a developer is working reaches that run at its next
attempt rather than only reaching the runs that had not started. The flag lives
at the state root rather than under a product, because what makes you pause is
an account or an afternoon rather than any one project — so it stops every
product whose state is kept at that root, which is every product on the machine
unless one of them was [moved](#where-the-state-is-and-moving-it).

A run that meets the pause parks exactly as one waiting out a
[usage limit](#waiting-out-a-provider-usage-limit) does, on the same machinery
with you as its reset instead of a clock: the park is durable before any waiting
starts, and the item stays claimed with its branch, worktree, and developer
session all preserved. A process already parked acts on `yoyo resume` within
seconds and carries on unaided; one that exited while the pause stood is
continued by `yoyo run <beads-id>`. While the pause stands nothing settles such
a run, however long it stands. Once it is lifted, one that nothing continues
within half an hour is settled by the next
[`yoyo reconcile`](#recovering-interrupted-runs) as a run with no process behind
it — its change kept and its stoppage docketed for the development manager —
rather than holding a developer slot until somebody types the command. Nothing is cancelled, so nothing has to be
reconciled afterwards — which is the whole difference between this and killing
processes, where the run lands cancelled and the work has to be developed again
from scratch once
[the claim it left behind](#claims-with-nothing-working-on-them) is given back. A
conversation turn is refused
rather than parked, because there is a person in front of it: saying the same
thing again once the pause is lifted takes the turn that was refused. `yoyo
review` is refused for the same reason, having no run to park.

The honest boundary is that a provider call already in flight is not
interrupted. The flag is read before a call, so a generation that is already
streaming finishes and is charged for, and the pause takes effect at the next
boundary — which for a developer attempt can be minutes away. Stopping a
generation mid-flight would throw away what it had already cost and leave the
run needing the same work again, which is the cost that makes a kill the wrong
verb in the first place.

Time a run spends held is accounted under its own kind, separately from what a
provider's refusals are allowed to spend: a hold never eats a run's
`execution.usage_limit_max_pause` budget, and nothing bounds it, because the
thing that lifts it is you. `yoyo status` — on its "Needs a human" line, and as
a banner over every one of [its live modes](#following-a-run-a-conversation-or-a-branch-review)
— and the conversation's `/status` all say when the pause was placed, because a
system somebody paused and forgot looks exactly like a system that died.

This is the broad switch, and there is a narrow one beside it. `yoyo pause` stops
everything including the runs already under way, which park keeping everything
they have; the conversation's [`/hold`](conversation.md#steering-the-work-from-the-conversation)
stops only the harness choosing new work and lets what is running finish. Reach
for the first when the reason is your account or your afternoon, and the second
when the reason is the queue.

`yoyo release` lifts that narrow hold from a terminal:

```bash
./bin/yoyo release   # the harness may choose work from this backlog again
```

It is the same record `/release` lifts — one file under the product — so it does
not matter which surface placed the hold or which lifts it. It is here because a
hold you did not place is the one you are most likely to meet with no
conversation open: the failure-storm brake holds intake itself when runs keep
blocking, and every report of a held intake at a terminal names this command
beside `/release`. Releasing what is not held is not an error, an item you name
with `yoyo run` was never subject to the hold, and a watching `yoyo work` session
starts choosing again at its next poll. Placing a hold stays in the conversation,
where the reason for it can be recorded with it. Who lifted a hold is recorded
beside the absence — `intake-release.json` under the product, naming the hold it
lifted, when, and by whom: this command, the conversation and its turn, or the
harness and what moved it — because the channel owes you who ended a hold it
announced: [the release is said once, by whom](reporting.md#a-finding-that-needs-your-hand).

## Where a finding that needs your hand goes

Some of what the roles find can be acted on only by a person: a hook that has to
go into `.claude/settings.json`, a credential to renew, a setting in a workspace,
a change to a file the harness may not write. Until 2026-09-19 such a finding
went where every other report went — into [the collected pile](reporting.md#what-agents-report-and-where-it-reaches-you),
worked through on the Lead Product Manager's cadence, and from there onto a checklist
in her conversation that you saw when you asked. Six developer reports of that
class sat in the pile from 2026-08-17 until the sweep of 2026-09-14 reached
them, and the finding it produced reached you a month after the first of them
was filed, because you asked why.

**A finding that needs your hand is a class the harness reads, and it goes to
three places the moment it is recorded.** Four things make one, and the
brake's hold becomes a fifth once it is yours:

- **The Lead Product Manager handling a report as yours.** Her `handle` action takes
  `"needs": "operator"` for a report whose answer is a change only you can make,
  with the reason saying what you have to do. That handling does not close the
  report; it records the finding, and a later handling of the same report
  without `needs` is what records the change made. She is not handed the report
  again as unhandled, so her turns list the findings she has handed you, with
  their identifiers, until she records each one done — tell her when you have
  made the change, or she will see it.
- **A report filed at critical severity** by any role, until somebody handles it.
  Critical is the severity that means action, in the reporting contract's own
  words; a critical report the Lead Product Manager then handles as yours is the same
  finding, and one she handles any other way ends it.
- **A run stopping on a condition only a person can clear.** The harness does
  not judge a stoppage itself; the development manager does, on
  [her docket](conversation.md#deciding-what-becomes-of-stopped-work), and her
  `escalate` decision is the one typed record that says a stopped run needs a
  person rather than a repair, a re-run, or a wait — a target branch that
  diverged from the forge, a stop no budget answers. A publication nothing
  asked the forge to merge is not one of these: it is hers to decide, by a
  re-arm the harness carries out or a re-run
  ([below](#recovering-interrupted-runs)). That decision is the finding, standing while it is
  the decision on the item's latest stopped run and nothing has settled what it
  handed you. A later decision on the run or a later run ends it. So does the
  item being **parked**, **retired**, or **closed** by a role, or the escalated
  run's **branch and worktree both being gone**, since then there is nothing
  left for a person to decide. `yoyo status` and the dashboard drop it as
  soon as they read that. The branch and worktree are looked for in the
  repository, never read off the run's removal flags. The next
  [`yoyo reconcile`](#recovering-interrupted-runs) writes what ended it
  on the item once — `The development manager's escalation of run … to the
  operator has ended: <item> was parked, so nothing about it waits on the
  operator: <the parking reason>` — and on the run's record as
  `escalation_ended`. The channel reads that record, and the sweep says
  what it ended under `escalations_ended` in `--json`. The development
  manager's docket already counts her escalation as settled and lists no
  escalated stoppage, so nothing about the ending returns one to her. Until
  yoyodyne-ifd.428.54, parking the item and losing the change ended nothing:
  on 2026-09-28 the escalation of run-95b34031 still named you over
  yoyodyne-ifd.78 two days after the Lead Product Manager parked it, with
  that run's branch and worktree long gone. (Her escalation already carries
  a warning-or-above report into the pile; that report is what she may
  handle as yours, and the finding is the decision rather than the report,
  so it is named once.)
- **An owning role's batch of recommendations on the changes proposed to its
  documents.** A [recurring pass](configuration.md#working-the-amendment-queue-on-a-cadence)
  of the architect, or of the Lead Product Manager, argues the undecided
  proposals it is put — approve, decline, or merge with another, with the
  reason — and decides none of them: only `yoyo amendment` does. Each pass that
  argued something still undecided is one finding, keyed by the pass, whose
  message is the pass's decision list — each proposal with what its owner
  recommends, then the reasons. A proposal you decide drops out of it, and the
  finding ends when the last one in it is decided.
- **The failure-storm brake's hold, once it is yours.** The hold names the runs
  it counted — each with its item and what stopped it. The trip is sent to you
  directly once, tagged, the moment it is recorded, naming those runs and
  `yoyo release`. While the development manager and the harness are working it,
  it is theirs to move and the message says so; once she escalates it to you,
  or the harness does at the bound on its loop, that is said to you directly
  once more — once for the hold, whichever of the two escalated it and however
  many times — and its one line on `yoyo status` names you as the one to move.
  [The configuration guide](configuration.md#watching-instead-of-draining) says
  what the brake counts, what it does not, and how it is worked.

Two stops a run makes on its own are named to you elsewhere and are not
findings of this class: a run the provider refused and the harness would not
wait for is listed among the runs blocked on provider capacity
(`standing.capacity_blocked`) with its remedy, and on the attention line as a
stopped item waiting on the development manager — the line's capacity entry
is the provider holding every role at once, which one stopped run is not — and a
promotion the forge has not published is on the line as awaiting the forge, each
with whose move it is.

Where each goes:

1. **One Slack direct message, tagged to you by member id, the moment it is
   recorded** — saying what is needed, who found it, and where it is recorded,
   so you can go and read the whole of it. It is said once and never again
   while it stands: a second pass sends nothing more, and a message repeated
   about something you have been told is the nagging that gets a channel muted.
   The brake's message names the runs it counted; the release of any intake
   hold is said once, naming who lifted it. See
   [reporting](reporting.md#a-finding-that-needs-your-hand).
2. **A named line under `Needs a human` on `yoyo status`**, printed wherever it
   falls among the operator's entries and never folded into `and N things not
   named here`:
   `report-… needs your hand: <what> (found by …; recorded in …)`,
   `yoyodyne-ifd.272 (<its title>) needs your hand: <the development manager's reason> (found
   by the development manager, escalating the stopped run to the operator;
   recorded in …)`, `the architect's batch of 3 proposed changes needs your
   hand: decide amendment-… approve; …`, and for the brake `intake is held, since <trip time>: …;
   tripped by run … of …: …`. It stays until the finding ends — the change
   recorded made, the report handled, the run decided again or its escalation
   ended, the batch's
   last proposal decided, the hold lifted.
   `--json` carries each finding as an entry of kind `operator-action`, with the
   finding whole under `operator_action`.
3. **The durable records that made it** — the report and its handling in the
   pile, read with `yoyo reports`; the triage decision on the item's record and
   the blocker on the item; the pass's account in `yoyo sweeps` and the
   amendment log `yoyo amendment` decides into; the hold in `intake-hold.json`
   under the product,
   which the status line is read from. A finding is derived from those and
   stored nowhere else, so there is nothing to clear by hand: what ends it is
   the record that says it is done.

What is deliberately not here is a list of your own. The Lead Product Manager keeps
none, and the harness keeps none apart from the records above: a checklist is
what reaches you when you ask, and this is what reaches you when it happens.

**These three verbs, `yoyo artifact approve`, `yoyo role activate`, and
`yoyo gate record` are a person's, and a process an agent started is refused
them.** Every process the harness launches for a role carries the role it was
launched for in its environment, as
`YOYODYNE_AGENT_ROLE`, on top of [the explicit environment](configuration.md#the-environment-a-check-runs-in)
every invocation is built from; the variable is under the harness's own
prefix, so a shell the agent opens and every `yoyo` that shell runs carry it
too. `yoyo pause`, `yoyo resume` in both its forms, `yoyo release`,
`yoyo artifact approve`, `yoyo role activate`, and `yoyo gate record` read it
before they read anything else, and a process that carries it is told, in a
sentence rather than a permission error, whose act this is:

```text
yoyo release is refused from a process the harness launched for the developer: a person releases intake, and an agent's process is not one
```

Nothing is written by a refused verb, and `--json` carries the same sentence
under `error`. What that stops is a mistake the system could make on its own —
a developer told to unblock the queue and reaching for `yoyo release`, or
approving the goals it was about to admit work against — which until it
existed was stopped by nothing but this page saying nobody does that. The
honest boundary is that an environment variable identifies an agent that is
being one: a shell can strip its own environment, and the refusal is then
gone. What stands behind it is different for the two kinds of record. An
approval is written into the goals document, and that document is a protected
path: a run's change carrying it is refused before any check runs or any
reviewer sees it, whatever wrote it, and that is tested end to end
(`TestAChangeRewritingTheProductsGoalsIsRefusedWithTheGoalsDocumentNamed` in
`internal/orchestrator`, with the join to the path `approve` writes pinned
beside the refusal tests in `internal/cli`). The holds and gate acts live under
the state root, outside any worktree, and nothing the harness tests stands between a
stripped environment and a write there: a developer run enables Claude Code's
OS-level sandbox over its shell (`sandbox.enabled`, failing if it is
unavailable), and that sandbox's write policy is the provider's own rather
than anything the harness declares or verifies, so it is not counted on here.
A person at a shell an agent opened clears the variable and the verbs are
theirs again.

**A hold the brake placed asks a person for nothing until it is escalated —
by the development manager, or by the harness at the bound on its own loop.**
That was not always so: the brake tripped on
2026-09-02, 09-05, 09-13, 09-17, and 09-19, and each time the line sat held
until somebody noticed — on the last of them for about two hours, with a free
developer slot idle. The operator's decision that day, recorded as a directive,
was that the brake may trip so long as the development manager is always
invoked at once to sort it out and nothing waits. So the poll that trips the
brake also summons her [sweep](configuration.md#recurring-tasks) out of its
cadence, with the runs that blocked and the reason each blocked in the message
that wakes her, and she decides what happens to the hold: release it, keep it
and probe the line, or keep it and escalate it to you. The watching session
acts on her decision at its next poll. Where she records none by
`execution.brake_cooldown` — thirty minutes by default — the session decides on
evidence instead: it starts one probe run under the hold, and the probe landing
reopens intake while the probe blocking keeps it held, restarts the cooldown,
and puts the question to her again with the probe's own stoppage. A broken
machine is therefore probed once per cooldown and put to her each time, and a
machine that was fine is choosing again within a cooldown of the trip whether or
not anybody answered. That loop is bounded: after
`execution.brake_escalation_cycles` of those summons-and-probe cycles — four by
default, which is two hours at the default cooldown — with her not escalating
the hold, the harness escalates it to you itself, sends you one direct message
naming the cycles spent and what stopped the last probe, and starts no further
probe. So the brake hold that waits on you is one she escalated, which she
does by recording the decision and reporting it at `warning` severity so it
reaches you, or one the harness escalated at that bound. Only verdicts and
check failures against a change that was present count toward the trip — a run
ended by something outside the work, a dirty checkout or a transport that did
not answer, is a verdict on nothing and counts toward nothing, and neither does a provider
answering nobody. A
promotion refused because the target branch
[diverged from the forge](#unwedging-a-target-branch-that-diverged-from-the-forge)
is in the same class: a catch-up the harness will not make is a stop the
harness made, every run that reaches integration meets the same one until a
person settles the branches, and the brake does not trip on any number of them.
What tells you is the run's own blocker — the item is stopped with both branch
positions and the recovery steps, said in its thread at `critical` and put on
the development manager's docket — and, since `yoyodyne-ifd.428.26`, a record of
the divergence against the product that
[holds the line by itself](#unwedging-a-target-branch-that-diverged-from-the-forge)
until the branches converge, so the one refusal is the only run it costs. On
2026-09-21 three identical diverged-target refusals tripped the brake, which
held the line a second time over the one cause the first blocker had already
put in front of a person.

The hold's own record says where it stands, and every surface reads it from
there: `yoyo status` names the hold on its fourth line with who it is
waiting on, under the head of whoever that is — `Waiting on the development
manager` while she decides, with when the probe
starts if she has not; the harness's while a probe runs, naming the probe;
either of those with which summons-and-probe cycle it is and at what cycle the
harness stops asking; and yours only once it is escalated, saying whether she
did or the harness did, and only then under `Needs a human` — the watch log and the channel say the same,
`yoyo sweeps` shows the summoned pass as summoned, and the run the probe made
records the brake as what chose it. `yoyo release` still lifts a brake
hold sooner, and says what the harness was in the middle of when it did.

## Waiting out a provider usage limit

None of what follows is specific to one provider. What a provider said is read by
that provider's dialect and reduced to one of nine answers — served, retrying,
limit-reached, unavailable, interrupted, model-unavailable, unauthenticated,
unreachable, refused — and every wait below is driven
by those and by nothing provider-specific. A project that declares a provider of
its own gets exactly this behaviour, including the two reset-time rules, without
restating any of it: see [provider plugins](provider-plugins.md).

When the provider reports that a usage limit is exhausted, the run pauses
instead of failing — for either provider invocation a run makes, the developer
attempt or the review. The reset time the provider named is recorded in durable
run state before any waiting starts, and nothing is cleaned up: the worktree,
the branch, the claimed Beads item, and the developer session are all kept, so
the reissued attempt continues the same change rather than starting it over. A
review that was declined is simply asked for again once the limit resets,
without redeveloping the change or spending a repair attempt.

The recorded reset is an upper bound on the wait rather than a gate on it. A run
sleeps `execution.usage_limit_unknown_reset_pause` — thirty minutes by default —
or the time left to the deadline, whichever is shorter, and then reissues the
attempt: the reissue *is* the probe. A reset time is a claim about the provider,
and claims go stale in both directions — capacity gets bought mid-wait, and a
rolling window can free room before the quoted edge — so a probe into a window
that is still closed costs one refused request and re-parks on whatever the
provider now reports. A run sleeps probes inside this process until it has spent
`execution.usage_limit_in_process_pause` on this run, and then exits with the
run still in flight instead of sleeping the next one. Two things continue it,
each with the whole bound available to its process again: running `yoyo run`
on the same item, and [`yoyo reconcile`](#recovering-interrupted-runs), which
continues such a run itself once its recorded deadline has passed and no
process holds its lease — in the same worktree and developer session, through
the same continuation `yoyo run` makes, with the run's record saying the sweep
did it. Until yoyodyne-ifd.428.5 the verb was the only continuation, and a run
nobody typed it for had exactly the shape of
[a stopped provider nothing continued](#when-a-provider-stalls-or-runs-out-of-budget):
no live process, no ending, a developer slot held and the in-flight guard
refusing every item beside it — indefinitely, because neither that settlement
nor the claim audit touches a run that is only waiting. A run whose deadline
has not passed is left as the wait it is, whether a process is asleep on it or
not, and one a live process holds is left to that process whatever the clock
says. The sweep hosts the continued run to its end exactly as `yoyo run` would,
so `yoyo reconcile` typed by hand with such a run waiting stays open for the
length of a developer attempt, saying which run it is continuing and why on
standard error before it does; `--json` writes its one document once the
continued runs have finished, with what each came to under `continuations`.
That bound counts every probe this process has already slept rather than each
one on its own, because a bound applied per probe would stop bounding how long
the process stays open at all.
`execution.usage_limit_max_pause` bounds what one run may spend waiting in total
rather than each wait separately, so a provider that keeps refusing cannot walk
a run past it, and what it records is what was actually waited rather than the
span to a deadline the run never reached. A limit reported without a reset time
polls under exactly the same rule, because it is unknown rather than
unwaitable: the monthly overage allowance reports this way while the ordinary
rolling window keeps resetting on its usual schedule, so it waits the same
interval and asks again. Unifying the two was the point — one polling
discipline, whether or not a deadline was quoted. A reset that is not in the
future is one the harness genuinely cannot wait for, so it stops the run and
records a blocker rather than guessing a wait. A reset that no longer fits the
run's remaining budget is different: the wait is well defined, only longer than
the harness will take, so nobody has anything to decide. The run ends cancelled,
recorded as ended by something outside the work (cause `usage-window`) and
naming the reset; it gives its
claim back, and keeps its branch and worktree. It spends nothing — no brake
count, no review round, repair grant, or re-run — and a watching session holds
the item under "waiting on the provider's usage window" until the reset passes,
then pulls it again by itself. An exhausted limit is not the only thing a run waits out:
[an overloaded provider](#waiting-out-an-overloaded-provider) below takes the
same machinery on a much shorter clock.

### A provider refusal outside a run

An exhausted limit is not only a run's problem, either. The harness asks a
provider for work in three places: inside a run, which parks as above; a
conversation turn; and an independent `yoyo review`, which uses the same reviewer
with no run around it. All three record the refusal — what was stopped, the limit
the provider named, when it lifts, and the model the turn was refused on, which
is the alternate where failover had already moved the turn there — and the last
two have no run to park in. What the record buys is that
[reporting into Slack](reporting.md#reporting-into-slack) says it as a `warning`
without you there, and a run that parks on the same limit is said at that weight
too. Hours in which nothing will happen is the one message a channel nobody is
watching most needs to carry, and it must not weigh the same as checks passing.

**A turn you typed waits anyway**, under the bounds above and on the same polling
discipline: it sleeps the probe interval or the time left to the quoted reset,
whichever is shorter, and asks the refused invocation again. It releases the
conversation while it waits, then takes it back and reloads its record before
asking again, using the latest session if another turn has been taken. Nothing
the turn had already done is done twice — tracker actions
applied by a round that finished stay applied, because what is reissued is the
single invocation the provider declined, with its results carried into the
conversation as it now stands.
In an interactive conversation the wait is on the activity line, saying what is
being waited out and when the turn will ask again — the end of this probe, which
is the quoted reset only where that comes sooner; `yoyo chat --message` waits the
same way, because an unattended caller is exactly the one who cannot retry by
hand. The budget covers the message you are waiting for rather than each wait
inside it, so a provider that keeps refusing reaches the configured maximum
instead of walking one message past it a wait at a time. The refusal is written
down once for as long as one message is waiting it out, so a wait is one warning
in the channel rather than one per probe — a limit whose quoted reset moves is
written down again, because that is the provider saying something new about it.
A `yoyo pause` placed while the turn is waiting is read before it asks again, so
the wait ends on your hold rather than spending through it.

What a conversation cannot do is what a run does when the wait is one the harness
will not take — a reset already behind us, or a wait past
`execution.usage_limit_max_pause` or `execution.usage_limit_in_process_pause`. A
run leaves its deadline in durable state and a later invocation continues it; a
turn has no such record to be continued from, so it fails at your terminal with
the limit and its reset time stated, the conversation stays open, and saying the
same thing again takes the turn that was refused. Setting
`execution.usage_limit_max_pause` to `0` turns the waiting off for conversations
exactly as it does for runs.

The turns the harness takes for itself do not wait: a stopped run delivered to
the development manager, a recurring task's firing, a correction, and the Slack
sink's turn each fail on the refusal exactly as before, because every one of
those already paces itself on it — the next pull, the next cadence, the thread
told what happened — and a turn that slept through the window would hold the
`yoyo work` session that took it for hours. An independent `yoyo review` still
fails on a refusal exactly as it did before too.

What those refusals add up to is read as well as each one on its own, and the
runs parked on a limit are read with them — a run's park is in its own record
rather than in the log above, and a product whose only refusals are runs
parking is held all the same. When the refusals standing cover the model every
agent's turn ends on, and at least one of them stopped a turn or parked a run
rather than being served through by an alternate, **the provider is holding
every role**, and that is said as a state rather than as one more refusal: it
heads [the four lines](#where-the-harness-stands-the-four-lines)
with the reset the provider named, it is on the attention line as your move,
and the channel [says it again while it stands](reporting.md#the-provider-holding-every-role).
It is the message that was missing between 2026-09-08 and 09-13, when 134
refusals were each said once and nothing said that all five agents were on the
one model being refused, with nothing to fail over to, for five days. A
refusal a later served turn on the same account and model has disproved is not
standing, and neither is one of a conversation its role has replaced; the next
section says what clears a refusal early.

### A watch session inside a recorded window

The same two records hold intake by themselves. At every pull a watching
`yoyo work` session reads the usage-limit log and the runs parked on a limit,
and while a refusal the provider named a future reset for covers the model
every developer's turn ends on — the developer's alternate where failover names
one, and each model `execution.developer_models` maps a label to as well — it
chooses nothing: no item is pulled and no recorded triage decision is carried
out. It reads the record rather than remembering it, so a session started inside
a window honours it from its first poll. That is what was missing on
2026-09-23, when the maintenance pass restarted the watch repeatedly inside a
`seven_day` window and each fresh session pulled new items into the same
refusal: one limit, twelve failed runs.

The session records the poll as one made inside the provider's window, so
`yoyo status` opens with `Paused on the provider's usage window until …`, the
channel says the same, and the watch log's idle line carries the window and its
reset — which is what a maintenance script reads to stand its idle check down.
It is never reported as a hold: nothing needs releasing, and the first poll past
the reset pulls again. A limit with no reset named does not hold intake this
way; a dispatch is how that one is asked about. A developer model the record
does not refuse — a label mapped to a model the provider still serves — holds
nothing, because work can run on it. A pass that is not watching stops on the window
instead of waiting it out.

**A served turn clears the window before its reset.** A quoted reset is a claim
about the provider, and capacity bought mid-window makes it stale: on 2026-09-24
the operator added capacity a day into a `seven_day` window, every turn after
that was served, and the record went on quoting 09-27. So every invocation the
provider serves — a developer attempt, a review with a verdict, a conversation
turn — writes the account and model it was served on to
`projects/<product>/state/capacity-served.json` under the state root, and every
reading of the refusals treats each one recorded before that moment, on that
account and model, as lifted. Only an invocation that genuinely served counts:
it ended without error, its process succeeded, and nothing on it reported a
refusal. One that ended in error — a provider death, a malformed stream, an
`api_error` the dialect could not classify, which may be a limit being
enforced — writes nothing, and neither does an answer with a limit, an
overload, or an outage reported beside it. The model recorded is the one the
invocation asked for, and for a conversation turn failover moved, the
alternate that answered. The first poll after such a turn pulls again, with
nothing released. A refusal written before refusals carried the account is
lifted by its model being served on any account, and one written before they
carried the model by anything served at all, because neither can be told apart
any more finely. A refusal of a conversation its role has since replaced holds
nothing either: nothing will be asked in that conversation again. Where either
record cannot be read — the served record or the conversation records —
nothing is cleared early by either of them, the one that could be read
included: every refusal stands until its quoted reset, which costs time and
never a refused run, since clearing on half the evidence would be guessing
about the other half.

Selection is not a fourth place. A watching `yoyo work` session reads the tracker
and starts runs, so a limit it meets is met by a run it started, bar the turn it
takes delivering a stopped run to the development manager. That the three above
are all of them is checked rather than asserted —
`TestEveryProviderInvocationAccountsForAnExhaustedLimit` sweeps the tree and
fails on a provider invocation with no account of what an exhausted limit does to
it.

## Waiting out an overloaded provider

A provider whose own servers are transiently overloaded refuses the same way an
exhausted limit does — the work is never judged, only declined — so it takes the
same machinery rather than a second one of its own. The difference is the clock.
An overload quotes no reset time and lifts in seconds rather than hours, so a run
waits `execution.server_overload_pause` — ninety seconds by default — and
reissues, instead of parking for the half-hour probe interval a usage limit uses.
Everything else is shared: the deadline is durable before the wait starts, the
wait spends the same `execution.usage_limit_max_pause` budget, and an overload
that never lifts therefore walks into that maximum and stops with a blocker
rather than reissuing forever. [Releasing a wait early](#releasing-a-wait-early)
below covers one of these exactly as it covers a usage-limit wait.

Ordinary transient throttling still never reaches any of this: the provider CLI
retries that on its own, and the harness does not duplicate the wait. What it
does act on is the terminal result the CLI ends on once its own retries are
spent — an `api_error` reporting HTTP 529 — because at that point the provider
has stopped retrying and somebody has to. An overload is the only terminal
`api_error` that becomes a wait;
[the rest of them](#when-the-provider-dies-mid-run) become a relaunch.

## Releasing a wait early

Everything above honors the recorded deadline as an upper bound, and a restart
mid-wait serves the rest of it rather than asking again, which is what keeps a
crash from retrying straight back into a window that is still closed.
`yoyo resume` with a work item named is the one thing that overrides that
deadline, and it overrides nothing else:

```sh
./bin/yoyo resume yoyodyne-ifd.53
```

(With no work item named it is the other half of
[`yoyo pause`](#pausing-everything-and-resuming-it) and lifts the operator's
hold over everything instead. Both are the same act — stop waiting and carry on
— and what the argument says is whose decision is being withdrawn: the
provider's refusal of one run, or your own hold over all of them.)

It exists because the deadline is a claim about the provider and you are the one
who can change what it is a claim about. Raise the account's capacity while runs
are asleep against an 18:50 reset and that reset has stopped being true; a run
waiting out a limit its owner has already lifted is autonomy working against
them. The command moves the next probe to now and does nothing else. In
particular it does not stop anything: killing a waiting run leaves a cancelled
run whose item stays claimed, and recovering from that means reconciling,
reopening the item, and developing it again from scratch. Released, the run
keeps its claim, its branch, its worktree, and its developer session, and a
process already asleep on the wait acts on the release within seconds. If the
provider still refuses, the run records the new report and waits again, so the
worst a premature release costs is one refused request. It is refused when the
named item has no run in flight, or has one that is not waiting on the provider
at all, because a release recorded against a run that is not waiting would be
acted on by whatever pause that run took next.

## Activating a role definition, and reading its history

A role definition is a file a person writes beside the configuration, under
`roles/`; [the configuration guide](configuration.md#protected-role-definitions)
states its format and the tools it may compose. Loading validates the file.
Activation records a person's decision about its exact content:

```sh
yoyo role activate specialist           # records USER as the person
yoyo role activate specialist --by Ada  # names the person explicitly
yoyo role list                          # each definition and its activation
yoyo role history                       # every activation, newest first
```

All three verbs accept `--config <path>` and `--json`. Activation refuses a
missing or invalid definition and records nothing. It records the name, source
path, SHA-256 content digest, person, and time in a separate immutable record
under `<state root>/projects/<product>/state/role-activations/`. Repeating activation
adds a record and retains every earlier one; the latest activation for a name
is what listing compares with its file.

`list` says **not activated**, **activated**, or **amended since activation**.
An amended file's current digest differs from the latest activated digest, even
when the edit only changed a comment. Its listing names both digests, who last
activated it, and when. Read the edited definition and activate it again to
record a decision about that content. An older matching activation does not
stand in for the latest one. `history` keeps the records when a definition is
removed or malformed. Neither inspection changes activation records. Both
agree the state root with the configured repository's Git marker before opening
the records; if that marker is absent, they create `.git/yoyodyne/state-root`
and its `.git/yoyodyne/state-root.writer` record. A root that disagrees with an
existing marker is refused.

**Activation is a person's verb.** A process the harness launched for a role
is refused before the configuration or state is read, with the same sentence
as `yoyo release`:

```text
yoyo role activate is refused from a process the harness launched for the developer: a person activates a role definition, and an agent's process is not one
```

`--json` carries that sentence under `error`. The refusal uses
`YOYODYNE_AGENT_ROLE`, with the same environment boundary described
[above](#where-a-finding-that-needs-your-hand-goes): stripping that variable
removes this check. The activation records live outside the worktree, under the
state root, like the holds. A definition remains a protected path whatever
its activation says.

Activation records the decision; binding an agent to that definition is
subsequent work. An agent's `role` still accepts only a shipped role, and these
verbs change no agent's capabilities or contract.

## Recording a step only you can take

Some work must not start until you have actually done something — read a soak,
signed a release off, checked a migration against production. Work that reserves
such a step declares it as a gate, by naming it after `human-gate:` on a line of
its own, and a workflow definition declares one on a state as `gate:`. Until the
act is on the record, the item is never pulled and the executor performs nothing
at that state.

What the gate holds is every route by which the harness chooses the work: the
pull `yoyo work` makes, and a re-run the development manager decides, which
`yoyo triage rerun` refuses before it claims anything, saying the stoppage keeps
its re-run for after the act is recorded. What it does not hold is you naming the
item: `yoyo run <id>` starts it as it starts a parked item, because the step the
gate reserves is yours and naming the item is you deciding to take it or to waive
it. Naming it waives the gate for that run, and the waiver leaves no trace: the
gate is listed only while its item is admitted, so it leaves `yoyo gate list` and
the needs-a-human line the moment the run claims the item, and a run that lands
closes the item with no act ever recorded and nothing afterwards saying the step
was passed without one. If the step matters enough to be on the record, record
the act first and then name the item — a gate you run past by hand is one only
you remember.

The two are not equally visible yet, and it is worth knowing which you are
looking at. A gated work item says it is waiting on a person wherever the queue
is shown and on the needs-a-human line of `yoyo status`, with the step named. A
workflow instance standing at a gated state says so only in the refusal raised
when something tries to step it — no status surface lists it — so a gated
definition is something to watch for rather than something the four lines will
tell you about. Nor does it hold a run today: under the delivery trial the
definition observes the pipeline rather than performing it, so a `gate:` on a
delivery state stops the observation at that state and records a
`workflow_divergence`, while the run itself delivers. A gate on a state holds
what the executor performs, and until the executor is what delivers, that is not
the run. Nothing shipped declares one today.

```sh
./bin/yoyo gate list
./bin/yoyo gate record soak-reviewed --for yoyodyne-ifd.209.7 --by mason --did "read a week of soak runs; they diverge nowhere"
```

`yoyo gate record` is the only thing that passes a gate. No run passes one, no
check passes one, and closing a work item does not pass one — which is the whole
reason gates exist. Before them, the only way to write down "a person has to sign
this off first" was an item somebody closes, and on 2026-09-04 machinery closed
exactly such an item and the work behind it became pullable with the reserved
step untaken.

A process the harness launched for a role is refused `yoyo gate record` before
the configuration or state is opened. Naming a person with `--by`, or accepting
the default from `$USER`, does not lift that refusal:

```text
yoyo gate record is refused from a process the harness launched for the developer: a person records gates, and an agent's process is not one
```

`--json` carries the same sentence under `error`, and no act is recorded. This
uses `YOYODYNE_AGENT_ROLE` and has the same environment boundary described
[above](#where-a-finding-that-needs-your-hand-goes): stripping the variable
removes the check.

The record names who took the step and what they say they did, because a gate
passed by nobody in particular and described by nothing is the flag that failed.
A gate already passed on that subject is refused rather than overwritten, so the
record keeps saying whose act it was. A gate whose record cannot be read is never
treated as open.

`--for` is required, and names the work item or workflow instance that declared
the gate. The act passes it there and nowhere else, which is what makes a name
reusable: `release-signed` is a step taken once per release, not once ever, so
declaring it on the next release holds that release until you sign that one off,
whatever you signed before. Were the name alone the identity, your first
signature would pass every later declaration of the word, and you could not
record the new act at all, because the gate would already read as passed. For a
workflow the reason is sharper still — every instance of one definition reaches
the same gated state, so an act against the name would approve one run's step and
every run made after it.

## Counting your hand steps

Every step you take by hand to keep the work moving is recorded as one event,
so how many each merged change cost you can be counted. The harness records the
ones it carries out for you as it does them:

- running a work item by name — `yoyo run <id>`, or `/work <id>` in a
  conversation;
- starting a re-run, a repair, a resume, or a re-arm by hand with `yoyo triage`,
  and crossing a cap with `yoyo triage override`;
- stopping a run — `/stop`, `/stop-everything`, or `/redirect` on a running item;
- settling or reconciling by hand — `yoyo reconcile` typed at a terminal;
- approving or declining a proposal or a document in a conversation, and a
  proposed change with `yoyo amendment approve` or `decline`;
- recording a directive — `yoyo directive record`, or `/directive` in a
  conversation.

Each event says when, which kind of step it was, the work items and run it
touched, anything else it touched (a directive, a proposal, a document), and
where it came in: the command line, or the conversation it was typed into.
Nothing is recorded for what the system does on its own account. A `yoyo` an
agent's process runs carries `YOYODYNE_AGENT_ROLE` and records nothing, and the
`yoyo reconcile` the [maintenance pass](#the-supervisors-maintenance-pass) runs
every few minutes is marked with `YOYODYNE_STARTED_BY` so it is not counted as
you settling runs.

**A step you take outside the harness is counted only if somebody writes it
down.** Restarting the scheduler, resetting the target branch, editing a
tracker status by hand: none of those passes through anything that could
record it. Record one afterwards with `yoyo intervention record`, saying who
took it, and naming the item or run it was for wherever there was one, because
that is what counts it against that change:

```sh
./bin/yoyo intervention record --kind restart --by mason --subject "the scheduler" \
  --at 2026-10-05T09:30:00-07:00 "restarted the scheduler after it stopped pulling work"
./bin/yoyo intervention record --kind reset --by mason --item yoyodyne-ifd.432 \
  "reset main by hand to drop a commit that broke the build"
```

The kinds that only happen outside the harness are `restart`, `reset`,
`tracker`, and `other`; any kind the harness records can be written down
too, such as a run stopped by killing its process. The event is marked as
observed, with who took the step and who recorded it (`--recorded-by`, by
default whoever took it). A process an agent's run started is refused, as the
other verbs that record a person's act are. A program manager that notices a
step can record it from its own conversation with a `yoyodyne-intervention`
block, under its own name; no other role can.

The events are kept in `interventions.jsonl` in the product's state directory
(`projects/<product>/state/` under the state root). The file is only ever
appended to, so a step once recorded is never rewritten, and it is read afresh
by every process, so a restart loses nothing. `yoyo intervention list` shows
every step in the order they were taken, in your local time, with the count
beside them:

```text
2026-10-05 09:30 PDT  mason restarted a part of the product by hand (observed, recorded by mason)
  touched: the scheduler
  restarted the scheduler after it stopped pulling work
6 hand step(s) named 4 merged change(s): 1.50 per change, and 2 named no merged change; the count is a floor: a hand step taken outside the harness is counted only if somebody recorded it afterwards, so the true number can be higher and never lower
```

A merged change is a work item the harness promoted. A step counts toward it
when it names the item or any run made for it, and once however many of them
it names; a step that names no merged change — a reconcile, a restart, a
decision on a document — is counted apart rather than spread across changes.
`--json` carries the events and the count, and the count is the read model's
(`readmodel.CountInterventions`), so a surface that shows it per day or per
week reads the same number. Steps taken before this record existed are not in
it.

## When the provider dies mid-run

Not every way a provider ends an invocation is a refusal it names in advance.
Sometimes it simply dies: the API answers with an error its own retry ladder did
not outlast, or the connection carrying the response goes away before the reply
is finished — `API Error: Connection closed mid-response`, which quotes no HTTP
status because nothing answered. Nothing was judged and nothing is wrong with the
change; the run just stops existing. That used to fail the run outright and leave
a person to reconcile it, reopen the item, and launch it again — twice in the
week before this was built.

The run relaunches itself now. The dead invocation is reissued in the same
worktree and the same developer session, up to
`execution.transient_relaunches_before_blocking` times — two by default — and
then the run carries on as if nothing had happened. Continuing the session is
what makes this cheap rather than merely automatic: an attempt that died
mid-response had already made part of the change, and the relaunch picks that up
instead of asking a developer to derive it a second time. There is no wait
attached, because there is no condition to wait out: a dropped connection is
already gone, and the provider's own retries are spent before the harness sees
the terminal.

The provider contradicting itself is in the same class. A stream that ends one
invocation twice — two terminal results, where there was only ever one ending —
judges nothing either, and the second of them is quite often the real one: a
subagent's completion carrying a terminal's marks is read as the invocation's,
so the run's own ending arrives looking like the duplicate. Because neither
ending can be told apart from the other, the invocation is not trusted to have
produced an answer at all, and it is asked again in the same session rather than
published. That used to fail the run outright as a malformed stream, which is
how a change that was all but finished came to be recovered by a triage rerun.
Both endings stay in the run's event log, so what the provider's dialect drifted
into is diagnosable afterwards. A stream the harness genuinely cannot read still
fails the run.

One budget covers both provider invocations a run makes. A review the provider
killed is asked for again on the same count, without redeveloping the change,
because what the budget bounds is how much of the provider's weather one run
absorbs rather than how often either role is asked. Nothing is handed back to the
developer either way, so a relaunch spends no repair attempt — the change is not
what went wrong.

Relaunches are counted in durable run state before each one begins, so a process
that dies mid-relaunch resumes against the budget it had rather than a fresh one.
Setting the bound to `0` buys no relaunches at all: the first provider death is
the last. It does not turn off
[waiting a dropped connection out](#waiting-out-a-network-that-dropped) — that
is a different rule, it is not configured, and it applies at `0` exactly as it
applies at `2`.

**What happens once the budget is spent depends on what killed the invocation.**
A death nothing can classify stops the run there and records a blocker on the
work item naming the provider's own last message. A death that is plainly a
dropped connection does not: it is
[waited out and asked again](#waiting-out-a-network-that-dropped) past the
budget, on the same backoff every other transport failure gets, and only a run
that spends that whole window stops. The budget is the right bound for weather
nobody has classified; a reset connection is not that, and stopping on one is
what cost four runs their finished work.

What else that blocker says depends on what the run was carrying, because a
provider dies during a repair attempt as readily as during the first one. A run
nothing had judged yet says so plainly — no check failed, no reviewer asked for
repair, nothing here says the change is wrong — which is what tells you to pick
the work up rather than replan it. A run killed inside its repair loop names the
repair attempts it had spent, the check that was failing, and the findings it was
answering, and says the provider is what stopped it rather than that verdict:
the evidence is unresolved rather than dismissed.

A refusal that *would* stand is not relaunched. A terminal `api_error` quoting a
4xx status — a malformed request, a key that is not permitted, a limit the
provider is enforcing — would earn the identical answer on the next attempt, so
it fails the run exactly as it always did. So does a 529, which is
[a wait](#waiting-out-an-overloaded-provider) rather than a relaunch, and so does
any terminal the API did not report at all. Two of the API's own errors are
neither: a login the provider will not accept (`Not logged in`, a 401) and an
API nothing reaches (`Can't reach the API server`, a name that does not resolve)
are [a wait that spends nothing](#waiting-out-a-provider-nobody-can-reach)
rather than a relaunch or a refusal. The invocation ended twice is the one
thing outside the API's own errors that still relaunches, because it is not a
verdict on anything — it is the provider failing to say what its verdict was.

## Waiting out a provider nobody can reach

The operator's directive of 2026-09-18, verbatim: *"I don't want a run killed
just because the network is flaky, the laptop is asleep, or I need to re-auth a
session."* Until then a run whose provider invocation failed on an expired login
or an unreachable API was read as a transient death: it spent its two relaunches
on an answer no relaunch could change, was recorded as blocked with its work
preserved, and went on the development manager's docket. A dispatch refused at
the availability check failed outright and counted toward the intake brake.
From 2026-09-17 18:17 local the Claude Code login on the operator's machine had
expired; three runs blocked in a row, the brake tripped, every recurring pass
recorded 0 turns, and the maintenance job restarted the watch 158 times. Nothing
told him. He learned by asking, three days later.

**A provider that is not authenticated or cannot be reached is a named wait
that spends nothing.** Two conditions earn it, and they are told apart only by
what you do about them:

- **Not authenticated** — the provider will not accept the account the harness
  asks under. `claude auth status` says so before a dispatch; inside a run the
  terminal says `Not logged in`, `Login expired`, `OAuth token revoked`, or
  `Please run /login`, or quotes a 401. The remedy is you logging in.
- **Unreachable** — nothing answers at the provider's API: the machine is
  offline or asleep, or a name does not resolve. The terminal says `Can't reach
  the API server` or carries the transport's own error. The remedy is the
  network coming back, which the harness finds by asking again.

Both are read off the terminal the provider ends its stream with, and off the
process's prose when there is no terminal — its stderr, or plain text on stdout
where the stream should have been: a CLI that refuses an expired login before
writing any envelope says so there, and an attempt that died so used to end as
a process failure nobody classified — relaunched, spending the budget, and
blocked, the 2026-09-17 stall replayed. Prose is read by both adapters, only
for those two refusals, and only when the stream ended without a terminal,
stderr first; a terminal the provider did write is never second-guessed by its
diagnostics, and a process that died any other way stays the failure it was.
The channel the refusal came on is recorded on the run
(`provider_outage_channel`: `envelope`, `stderr`, or `stdout`, kept as evidence
after the wait, like the limit's kind) and on the product's outage record
(`channel`), so a run that waited says whether its provider wrote an ending or
died first.

What the wait costs is nothing, and that is the whole of the rule:

- **A run keeps everything and waits.** Its claim, its branch, its worktree, and
  its developer session are all kept. No relaunch is counted, no repair attempt
  is charged, the usage-limit pause budget is untouched, and nothing is docketed
  or blocked. The run asks again every `execution.usage_limit_unknown_reset_pause`
  — the one interval the configuration already states for "ask again rather
  than being told when" — and carries on from exactly where it stopped when the
  provider answers, with its relaunch and repair counters exactly as they were.
  There is no in-process bound and no maximum: a login you renew in an hour is a
  run that waited an hour. The next probe is durable on the run, so a process
  that dies mid-wait leaves a run `yoyo run <beads-id>` resumes rather than one
  that failed, and `yoyo resume <beads-id>` asks again now rather than at the
  next probe, exactly as it does for a limit.

  All of that is the wait a live process is serving, and it costs nothing
  however long it lasts. A wait whose process died is different, because
  nothing but `yoyo run` ever continues it: once half an hour has passed since
  its recorded probe with no process holding it, the next
  [`yoyo reconcile`](#recovering-interrupted-runs) settles it as a run with no
  process behind it. The item is blocked with that account, the branch,
  worktree, and developer session are kept, and the stoppage is docketed for
  the development manager, whose repair-continue picks it up in its own
  worktree and session. Until then `yoyo run <beads-id>` resumes it as above.
  Before the sweep settled every park nothing continues (yoyodyne-ifd.428.49), such a run held its developer slot until somebody
  typed the command.
- **The scheduler dispatches nothing into it.** A dispatch the provider turned
  away counts toward nothing — not the brake, not the docket, not the session's
  exclusion of the item — and `yoyo work --watch` chooses nothing while the wait
  stands, saying why. It asks the provider at every poll whether the login has
  been renewed, and the poll that finds it renewed resumes the line by itself:
  **nothing is released and nothing is restarted.** A provider nobody can reach
  is asked about by pulling into it again once the probe interval has passed,
  because nothing cheaper says whether the network is back. A draining pass
  (`yoyo work` without `--watch` — the pass that returns when the queue empties,
  not the [redeploy drain](#a-session-draining-to-restart-into-a-deployed-build)
  below) stops on the wait instead, since it is a command you are waiting on
  the return of.
- **A recurring task records the wait rather than a failed turn.** A firing due
  while it stands moves its cadence, asks the role nothing, and its sweep record
  says the provider is not authenticated (or cannot be reached) — so `yoyo
  sweeps` over the outage reads as the outage rather than as a column of zero
  turns. Once the probe interval has passed since the provider was last met
  refusing, a due firing is made into it anyway: the firing is the one probe
  this path has, so a machine with nothing in its backlog and no watch running
  still finds the network back on its own. A served turn ends the wait; a
  refused one re-records it, and the next firing waits the interval again.
- **A conversation you start waits within its message's budget.** It probes on
  `execution.usage_limit_unknown_reset_pause`, sharing the same total waiting
  budget as a usage window. It releases its conversation for each wait, records
  the cause for `yoyo status`, and takes the hold back and reloads the record
  before asking again. A cancelled wait writes no stale conversation state.
- **The brake does not trip.** The failure-storm brake counts runs that blocked
  with nothing landing between them, and a dispatch or a run the provider turned
  away is neither. A brake tripped on this would summon the development manager
  over a change nobody judged, and prescribe a probe into a provider that is
  still away, which is why tripping it on this turned one hand step into two.

Where it stands is one record under the product, `provider-outage.json`,
written by whatever meets the provider refusing everybody — a dispatch, a run,
a conversation turn — and cleared by the first thing the provider serves again:
a developer attempt, a review, a conversation turn, or the watch's own login
check finding the machine signed in.
It is what `yoyo status` names the wait from: the banner above the four lines,
and an entry on the attention line that says who it is waiting on.

```text
The provider is not authenticated; the operator must log in: every role is waiting on it, and the harness asks again on its own until it answers; 3 turns refused since 2026-09-17T15:17:00Z (claude-code, account default)
Running: nothing
...
Needs a human (1):
  The provider is not authenticated; the operator must log in: … — the operator's — log in to the provider, or wait for the network; the harness resumes on its own once it answers, and nothing is released or restarted
```

The channel says it once the moment it is seen, tagged to the operators by
member id, and once more when the provider answers again: see
[reporting](reporting.md#a-provider-nobody-can-reach). The stall alarm does not
fire over it — a line of runs each waiting on the same login is not a machine
that died — and it does not repeat while it stands, because the banner carries
it and a message repeated about a wait you have been told about is the nagging
that gets a channel muted.

## Waiting out a network that dropped

A run touches somebody else's network at its most expensive moments: it pushes
the run branch, opens and updates the pull request, reads where the remote target
branch stands, asks the forge to merge, confirms the merge, deletes the merged
branch, catches the local branch up, and makes every provider invocation over
it. It ends by writing to the tracker, which is not a network but is a store
other processes are writing to, and a `bd` too busy to run judges the work no
more than a reset connection does. **A run in flight reads that same store at
each of its gate boundaries**, to find out what its work item waits on: at the
start of every repair round, and once more before the promotion. A `bd show`
killed under load there is the same non-answer as the write, and it reaches the
run at the moments it has most to lose — yoyodyne-ifd.436.4's change was already
approved and yoyodyne-ifd.117.1's files already lifted when the read that ended
each of them timed out. One read sits before all of those and is
[a different thing](#the-read-a-dispatch-makes-before-there-is-a-run): the one a
dispatch makes to load the item at all, before it claims anything or adopts a
run in flight.
On 2026-09-03 four runs died at those boundaries in one day, each on a single
connection reset the next attempt would have survived — completed and sometimes
already reviewed work recorded as failed — and the intake brake then held the
whole line three times because the blocked runs came one after another.

**The harness no longer fails outright on anything that can recover.** A failure
whose class says the next attempt may well succeed — a connection reset, a
network drop, a transport-level refusal — is waited out and asked again, at every
one of the boundaries above.

- **The waits are Fibonacci seconds, capped at half an hour**: 1s, 1s, 2s, 3s,
  5s, 8s, 13s and so on, reaching the cap after about seventy minutes. Cheap
  while a reset connection is still the likeliest explanation, and a probe every
  half hour after that.
- **Each boundary gets its own two-hour window**, because a network that dropped
  a push says nothing about a merge. Roughly twenty attempts fit in one.
- **None of it is configured.** The intervals are the harness's and the same for
  every product, exactly as the watching session's retry of an unreadable tracker
  is: what they measure is how long a connection that comes back takes, rather
  than anything about a project.
- **Nothing that is an answer is waited on.** An authentication failure, a merge
  the forge refused, a protected branch, a conflict, and any 4xx earn the
  identical answer on the next attempt, so they are reported as promptly as they
  always were. So is a failure whose class the harness does not recognize: the
  set is deliberately small, and anything outside it keeps the behavior it had.
  The full recoverable-versus-terminal taxonomy is the architect's, and this does
  not wait on it.

**Every wait a run takes is recorded before it is taken**, on the run itself,
with the boundary, which attempt it was, the interval, and the failure it waited
out. Two things follow. A process that dies mid-wait comes back to the window it had
already spent rather than to a fresh one. And a run that waited a network out and
finished says so on the work item — `Waited out a recoverable failure while
merging the pull request: 3 retr(ies) over 4s, waiting 1s, 1s, 2s` — which is the
only sign that anything happened at all, and the thing to read when a machine's
network is degrading before it starts costing runs.

**A window that runs out escalates rather than going quiet.** What the boundary
would have produced is produced — an outstanding publication, a blocker on the
item — with the attempts and the time in front of it, so a run handed to a person
says the network was retried and for how long instead of reporting the last reset
as though it were the first.

**The gate-boundary dependency read is the one that parks instead.** What the
other boundaries would have produced is a blocker, because a push that never
landed or a merge the forge never made is a step somebody has to decide about; a
store that was busy for two hours is not, and the run has nothing wrong with it.
So a read that spends its whole window leaves the run in flight, parked, keeping
its claim, its branch, its worktree, and its developer session — exactly as a
run waiting on an unresolved directive or on work its item depends on does. It
says so on the item, naming the read, the attempts, the time, and the last thing
the store said; `yoyo status` reads it as a parked run, `yoyo reconcile` leaves
it resumable for half an hour and then, with nothing having continued it,
[settles it](#recovering-interrupted-runs) as a run with no process behind it,
the claim audit leaves its claim alone, and
the channel says it as a `warning`, since nobody chose it. The store answering is
what lifts it: `yoyo run <beads-id>` continues the same run from the boundary it
stopped at, and the window goes with the park, so the re-entered gate asks again
rather than finding its window already spent. Before yoyodyne-ifd.428.6 this
boundary ran under a flat deadline and ended the run on the first timeout, which
in two days killed three runs — two of them holding an approved change and a
lifted worktree.

### The read a dispatch makes before there is a run

Both of the reads above are a run's own, and everything this section says about
recording and parking follows from there being a run to record on. **The read a
dispatch makes before either — `yoyo run` and `yoyo triage`'s continuation
loading the work item, before anything is claimed and before a run in flight is
adopted — has neither.** It is waited out on the same series and the same
two-hour window, because it is the same store contended by the same processes
and a `bd` killed there turned away a dispatch that had nothing wrong with it.
What it does not do is the other two halves:

- **Nothing is recorded on a run**, because there is no run to record it on. No
  run has been reserved, and the run a resume is about belongs to whichever
  process holds its lease rather than to the one asking. So a dispatch that dies
  mid-window comes back to a whole window rather than to the one it had spent —
  the opposite of the rule a run's own boundaries follow, and it costs nothing,
  because a dispatch that died claimed nothing and left nothing behind.
- **The wait is recorded on the watch log instead**, where a watch session
  started the dispatch — an item it pulled, or a triage decision it carried out.
  Each wait is written as it is taken, naming the item, the boundary (`reading
  what this item waits on`), which retry it is, when it asks again, and the
  failure it is waiting out. Such a dispatch holds a developer slot with no run
  record for up to the whole window, and until yoyodyne-ifd.428.14 every surface
  read that as a hung process. Now `yoyo status`'s running line says it —
  `Running: no run yet, and 1 dispatch waiting out a tracker failure before
  claiming anything:`, with the wait under it — and so does the brief form the
  channel carries. The line the channel repeats while nothing is chosen names the
  wait rather than an idle session, as nobody's move, and the stall alarm reads it
  as an account of the quiet rather than paging. A wait accounts for the quiet
  until a minute past its own end, time enough for the retry it precedes, so a
  dispatch that died mid-wait stops accounting for it within minutes and the
  alarm is free to fire. These entries are notes about a dispatch rather than
  changes of the session's state, so `yoyo status`'s session line still names
  what the session itself last did. A dispatch started by `yoyo run` or `yoyo
  triage` at a terminal records nothing; whoever typed the command is watching it.
- **Nothing is parked**, for the same reason. A window that runs out there
  refuses the dispatch with `load work item: the tracker kept failing on
  something a later attempt could have survived`, naming the attempts and the
  time, and writes nothing on the work item. Where the dispatch was a resume, the
  run in flight is left exactly as it was — still claimed, still preserved, still
  resumable — rather than given a park of its own.

The practical difference is what you go and look at. A run parked on this says so
on its item and on every surface; a dispatch refused by it says so to whoever
typed the command, and to the
[docket entry](#recovering-interrupted-runs) for an attempt that never became a
run where the scheduler made it.

**A conversation's tracker calls are under the same rule.** Every decision a
role makes in conversation — a triage decision, an admission, a note, a closure
— is a write to the same store, and the reads that gate those writes go to it
too; until yoyodyne-ifd.366 not one of them was retried, which is how a triage
re-run of yoyodyne-ifd.142 came to be reported as unrecorded on a single `bd
update` that timed out. Now a call that fails the way a killed or contended
`bd` does is waited out on the same series and asked again, and only a call
that has spent the window is reported the way it always was, with the attempts
and the time in front of it and then the account of what a timed-out write left
behind. The window is per operator message rather than per boundary, and every
call in the message shares it — forty calls each waiting a whole window is a
message nobody gets an answer from. Each wait is recorded on the conversation
before it is taken, as a `tracker.retried` event, and an interactive turn shows
it on screen — `waiting out a tracker failure a later attempt may survive; asking
again at 3:04PM (attempt 3, after 2s)` — so a turn waiting out a contended store
is distinguishable from one that has hung. Stopping the turn ends the waiting,
all of it: the call is left as it failed, the window is closed for every later
call in the message — including the read that settles what a timed-out write
left behind, which runs under a context nothing can cancel — and what is
reported says the turn was stopped rather than that the window ran out. An
action that landed after waiting says so on its own line. One guard changed
with it: the duplicate check an admission makes used to let the admission
through when its listing failed, on the argument that the tracker was briefly
unavailable. Now that the listing is retried, a listing that still fails refuses
the creation with the reason, and puts a proposal the goals would have admitted
unasked to the operator instead, rather than admitting work on the strength of a
guard that never ran. A write that is not safe to repeat is not asked again
blindly, because repeating it is a second thing rather than the same write
twice: on 2026-09-24 a `bd create` killed at its timeout had already made
yoyodyne-ifd.428.21, and the retry made 428.22, and an append killed the same
way was appended twice. Reads, and linking or unlinking a dependency, are asked
again as they stand, as is an update that appends no note, since it only sets
values. Everything else is checked for having landed first. Before a creation is
asked for again, the tracker is listed for an item carrying the creation's
title, parent, and notes — which name the conversation and the turn. Before an
update, a block, or the clearing of a blocked status is asked for again, the
item is read for the note it was appending at the end of its notes. Before a
close, the item is read for being closed. What the check finds is the write's
answer, reported as applied. A check that cannot be read leaves the write
failed rather than asked again.

**One consequence is worth knowing before you raise
`execution.max_concurrent_developers`, and it is not free.** Five of these
boundaries run under the target branch's promotion lease, which is what keeps
promotions serial: re-reading the remote target, the merge, confirming it,
deleting the merged remote branch, and catching the local branch up. A run
waiting a forge out holds that lease while it waits, and each of those boundaries
has a two-hour window of its own — so the worst case is not two hours but the sum
of them, and a forge that is down for a day holds the lease for as long as the
windows last rather than for an hour.

**Other runs promoting into that branch do not wait it out.** The promotion queue
is bounded at fifteen minutes, so a run that reaches integration while the lease
is held waits that long and then stops, saying that another promotion held the
lease for the whole wait. Before these waits existed the holder failed fast and
the queue drained behind it; now a forge outage longer than fifteen minutes can
stop the runs queued behind the one that is waiting. The trade is deliberate at
one developer, where there is no queue at all — waiting is what stops reviewed
work being recorded as failed — but above one it converts a long outage into
stopped runs on the branch rather than one slow one, and the fix while it lasts
is `yoyo pause` rather than waiting for the windows to run out.

### Tracker export snapshots and abandoned temporary files

The harness sets `BD_EXPORT_AUTO=false` on its bd calls, even where the project
file enables automatic exports. A tracker write therefore no longer rewrites
the full `.beads/issues.jsonl` under its store lock. The harness requests an
explicit export before preparing a worktree, and admission-trigger readers and
`yoyo reconcile` refresh the primary snapshot when it is at least a minute old.
The tracker still has to read the whole store for an export; it does so at these
reader boundaries rather than on every write. A failed or incomplete export
leaves the previous snapshot intact and is reported by the reader.

`yoyo reconcile`, including the supervisor's maintenance pass, removes bd export
temporaries named `.beads/.~issues.jsonl.<digits>` or
`.beads/.~interactions.jsonl.<digits>` only after their modification time is more
than 24 hours old. The confined snapshot writer's `.beads/.yoyo-write-*.tmp`
temporaries are cleaned by the same rules. It checks open files with `lsof`,
matching device and inode
so a holder using another path or a hard link is still recognized. Open files,
symlinks, unrelated names, and files changed during inspection are kept. If
`lsof` is unavailable, times out, or cannot give a complete answer, nothing is
removed and the pass reports why. bd creates a new temporary exclusively for
each export; it never reopens an abandoned one.

Each pass that removes files records their paths, sizes, and modification times
under `projects/<product-id>/state/tracker-export-cleanups/` in the product's state
directory. The command also names each removal, and `--json` carries it under
`tracker_exports`. The developer's sandbox cannot clean the primary checkout;
the harness performs this on its next maintenance pass after moving to the new
build. Calls to bd outside the harness keep their own export settings.

### A tracker that does not answer a listing

A listing — `bd list`, which the development manager's docket, the forge
reading on her pass, the claim audit, the admission guard, and `yoyo status`
all make — is bounded at thirty seconds like every tracker call. bd takes an
exclusive lock on its store for every command, reads included. Harness writes
set `BD_EXPORT_AUTO=false` and do not automatically rewrite the export.
Explicit snapshot exports still read the whole store under that lock; the
harness publishes the snapshot beside the store after bd exits. Calls outside
the harness retain their own export settings and, when automatic exports are
enabled, can still rewrite the whole export while holding the lock. A listing
queued behind these commands can be killed at its bound seconds before it would
have answered.
[The diagnosis](diagnoses/yoyodyne-ifd-433-20-tracker-listing-timeouts.md)
records the earlier behavior, when every harness write also exported; the
[snapshot section](#tracker-export-snapshots-and-abandoned-temporary-files)
describes the current behavior.

**A listing its bound killed is asked again, twice**, two seconds and then
eight seconds later. Only a timeout is asked again: bd refusing, or answering
something that does not decode, would say the same thing the next time. A
listing that still fails is refused with how many attempts it made and over how
long — `bd list did not answer within its 30s bound on any of 3 attempts over
1m40s: …` — so what reads it says the tracker did not answer rather than that
one listing timed out.

**A pass carries on with what it could read and names what it could not.** The
development manager's docket still lists every stoppage; it says that whether
each entry's item is still open could not be read, that this is the harness's
to retry rather than hers to wait on, and that an entry on closed work is closed
by the next `yoyo reconcile`. The forge reading on her pass still compares every
open pull request with its target and reports the ones whose branches are
carried, and its problem on the pass says that none was judged on its item being
closed and that the tracker is read again on the next pass. Nothing about a
listing that did not answer fails a pass. The watching session's own reads of
the queue keep the retry they already had — read again for up to five minutes,
[with the session line saying so](#where-the-harness-stands-the-four-lines) —
and a conversation's calls keep
[theirs](#the-read-a-dispatch-makes-before-there-is-a-run).

**`yoyo status` says since when.** Every listing the harness's own tracker
client makes writes how it ended to `tracker-listings.json` under the product's
state: the moment the first listing failed after the last one that answered,
how many have failed since, and what the latest said. While listings are
failing, the fourth line carries it under `Waiting on the harness`:

```text
Waiting on the harness (1):
  the tracker has not answered a listing since 2026-09-30T18:13:00Z: 2 listing(s) failed after their retries, the latest at 2026-09-30T18:23:00Z: bd list did not answer within its 30s bound on any of 3 attempts over 1m40s: … — the harness's — each listing its thirty-second bound killed is asked again, twice, before it is given up on, a pass carries on with what it could read and names what it could not, and the first listing that answers clears this
```

`--json` carries it as an entry of kind `tracker-unanswered`, with the record
under `tracker_listings`. The first listing that answers clears it and records
when, as `answered_at`. It is not the operator's: nothing about a contended
store is a person's to settle while it lasts. A store that goes on refusing for
hours is a different thing, and the item-level remedies above are still where it
shows first.

**What this does not cover** is a machine that sleeps. The twelve-hour stall
from 21:53 PDT on 2026-09-29 was the laptop's lid being closed on battery, not
the tracker: a recurring pass that was in flight when the machine went to sleep
held the watching session's poll until it ended three and a half hours later,
and the passes behind it ran only in the minute-long maintenance wakes the
machine took overnight. A machine that runs the product unattended has to be
kept awake — on power, with system sleep disabled; on a Mac laptop whose lid is
closed that takes `sudo pmset -a disablesleep 1`, which `caffeinate` does not
do — and nothing the harness does replaces that.

**The watch log says which pass the session is in.** A watching session used
to fire its recurring passes inside its poll, one at a time, so while a pass ran
the session pulled nothing. Until yoyodyne-ifd.433.20 it also wrote nothing: the
log went from its last line before that pass to 09:48 PDT the next morning with
not a word. Now each pass the session begins is a line in `watch.jsonl` as it
starts. Since the fix for one role's pass holding another's (yoyodyne-ifd.428.56)
a session takes its passes beside its poll, so the line says the poll goes on
pulling:

```text
taking the recurring pass of development-manager-sweep since 2026-09-30T04:52:08Z, fired by its schedule; it is taken beside the session's poll, which goes on pulling while it runs
```

A schedule that can only fire inside the poll still does, and its line still
ends "the session fires its passes inside its poll, so it pulls nothing more
until this pass ends".

carried as `recurring_pass` — the task or instance, its role, what fired it,
and when it began. It is a note about what the session is doing inside its
poll rather than a change of the session's state, like a dispatch's wait: the
session line on `yoyo status` and the stall reading still name the session's
own last word, so a session idle over an empty queue that begins a pass is not
read as one choosing work. The session's next line after the pass is the
account of the poll that pass was part of. A pass that runs for hours is
therefore said as the pass, with its start, rather than left as silence; and
since it runs beside the poll it holds only its own role's conversation, so the
session goes on pulling and firing other roles' passes meanwhile.

## When a provider stalls or runs out of budget

A provider invocation is bounded by two separate questions, because one deadline
cannot answer both. Whether it is stuck is answered by activity: the harness
already stamps every event it parses, so a gap of five minutes with no event at
all means nothing is happening, and the invocation is stopped as stalled.
Whether it is worth continuing is answered by a total budget of four hours,
because an agent can stay live and unproductive — retrying, looping, thrashing —
and no liveness signal will ever catch that. An agent that emitted a tool result
seconds ago is demonstrably working, so elapsed time alone never stops it. Both
stops leave the run in flight rather than failing it, exactly as a usage-limit
pause does: the worktree, the branch, the claimed Beads item, and the developer
session are all preserved, and running `yoyo run` on the same item continues
that run — the developer resumes its session, and a stopped review is simply
asked for again without redeveloping the change or spending a repair attempt.
The reason is reported as what it was, a stall or an exhausted budget, and
neither is ever described as the agent having reported a failure, because it
reported nothing. Only a stop with nothing to continue from — no session, no
worktree — ends the run, and it still says the harness stopped the provider.

**The total budget and the wait after a reply hold until the process exits.**
A process that closes both its output streams is still waited for under those
bounds, and reaching either ends its process tree just as it would while the
streams were open. The limit on waiting without output stops applying once both
streams close, because there is no output left to wait for. The process result
records when the streams closed as `OutputClosedAt`, separately from when the process
finished, so a process that closed its output and kept running says so.
These clocks use Go's runtime timers: on systems whose monotonic clock pauses
during machine sleep, their budgets pause too. The recorded start and finish
times are wall time and can include that sleep; a sleeping laptop is waited out.

**A session that has written its final reply has ended its turn, and is never
stopped as stalled.** Both adapters read the provider's own result — Claude
Code's `result`, Codex's terminal — as the end of the turn, and from that line
on the five-minute silence bound no longer applies. A process still running
after it is being kept alive by work the agent started in the background, a
`make test` or `make race` left running, and not by a provider gone quiet. The
harness waits that work out for up to five minutes, with anything it prints
still going into the run's event log, and then ends it. The run's record
(`after_reply`) and the log say which of the two happened: the work finished on
its own, or it was still running at the bound and was ended. While the wait
lasts, `yoyo status` and the dashboard show the run as `reply written, waiting
for background processes: 2m of 5m` instead of `developing`. The run then goes
on to its checks with the reply it wrote. Nothing is recorded as a stall, so
the harness does not spend its one resumption of a stalled run on it, and no
decision is asked of the development manager. Until yoyodyne-ifd.435.6 that state was read as silence: on
2026-09-28 run-008b0e25 wrote its final reply at 11:58:33 PDT and was stopped as
a stall five minutes later, over the `make test` and `make race` it had
backgrounded.

Short Git commands keep a deadline of their own rather than a liveness signal,
which is the right bound for a command whose duration is known — known on an
idle machine, that is. A local Git command is given thirty seconds, scaled by
how far the machine's one-minute load average exceeds its cores, read for each
command and capped at ten times: at a load average near forty, a flat thirty
seconds was killing `git worktree list` under two race suites and the provider
processes beside them, and the run that asked failed on the machine rather than
on its work.

One local Git command is budgeted to the work rather than to the command, and it
is the one that does work: `git worktree add` writes the whole tree out, so its
budget is the figure above plus fifty milliseconds for every file in the commit
being checked out, load-scaled like the rest. A checkout bounded by the
command's own figure is what killed three runs of yoyodyne-ifd.441 in three
hours on 2026-09-22, each of them with no Git error at all — the runner's exit
code, and a stderr holding nothing but the checkout's progress meter, one
stopped at 87% of 1099 files — each leaving a half-written registration, holding
a developer slot until the claim audit gave the item back half an hour later,
and one of them spending a recorded re-run on a run no developer ever saw. A
creation that is ended anyway is [a round refused from outside the work](work.md): the run's
record says so in one sentence naming the tree and the budget rather than
reprinting the progress, the item is charged neither a round nor the re-run that
started it, and the stoppage is docketed like any other.

Sizing that budget costs one `git ls-tree` the creation did not used to run, and
it cannot become a new way for a creation to die. A count that fails for its own
reasons never stops the creation: the add is budgeted for a stand-in tree of two
thousand files instead, and the run says on standard error that its checkout was
bounded as an uncounted tree — so an operator reading a creation that dies later
knows the bound was a stand-in rather than the tree's own size. A count the
harness itself ended is the one that does stop it, and it stops it in exactly the
class above: a count killed by the load is the same machine-too-busy death as an
add killed by it, refused as a cause outside the work and charged nothing.

**The run is left in flight for half an hour, and then it is settled.** Before
automatic continuation, the scheduler chose only what the tracker called ready,
and a claimed item was not. A stop nobody typed `yoyo run` for therefore left a
run that read as running for good, with no live process behind it and no ending
ever recorded. Each one held a developer slot,
the in-flight guard refused every item beside it as a race, the claim audit left
it alone as a wait, `yoyo reconcile` reported it resumable on every pass, and the
development manager's repair-continue about it was refused for want of a
docketed stoppage, because the run never recorded one. Two runs stood that way
from 2026-09-20 07:20 until somebody asked, a day and a half later. So a sweep
that finds a stopped run nothing has continued for thirty minutes — measured
from the stop, which is the last thing the record wrote — settles it as
[a run whose process vanished](#recovering-interrupted-runs): the item is
blocked with what the sweep observed, the stoppage is docketed, and the slot is
free. Inside the half hour the sweep still reports the run resumable and says
when the grace ends.

**What the settled stoppage is owed is the attempt it was stopped in, and the
docket entry says so.** A silent session itself judges nothing. When a first
developer attempt goes silent before any check or review, nothing has been
returned to the developer, so the entry the development manager reads carries
the fact that decides between her two verbs.
It names the developer session the run stalled in, says the session is preserved
with whatever that attempt had written still in the worktree, and prints
`yoyo triage repair <run-id>` as what continues it. `yoyo triage repair` then
does exactly that: the same run goes on, in the same worktree, branch, and
session, resuming at the point the provider was stopped. It counts no review
round and no repair attempt, because a stall returned no failure for an attempt
to answer — what the item's budget records is the grant the decision spent, and
what the run records is a continuation with no attempt against it, marked as the
stall it carried on. The one condition a repair of a change is held to and this
is not is that the worktree hold a change already: a first attempt stopped early
may never have written anything, and an empty worktree is exactly what the
attempt it is owed starts from.

**A stall at the review is owed the review, not another attempt.** A run whose
developer attempt finished and whose reviewer the harness then stopped — or
whose process went at its checks — with nothing yet handed back to its
developer is settled and docketed the same way, and its entry says the repair
continues it at that step rather than in the developer session. `yoyo triage repair` then puts the run back at the step it
stalled in: the review is asked again (or the checks re-run) on the change the
attempt left, on the same branch and in the same worktree, with no developer
invoked. That continuation counts no repair attempt either, and it is held to
the checks a repair is — the worktree has to be as the harness left it and has
to still hold the change, because that change is what the step judges. Before
2026-09-24 only a stall in the developing phase was continuable, so a stalled
review left a re-run as the only decision, and a re-run discards the branch the
finished attempt produced.
A run already in its repair loop that stalls at its review or checks is not
this: a failure was returned to it, so it is re-entered as a repair.

Until 2026-09-23 the repair was refused for a stoppage like that, for want of a
repair input, which left a re-run as the only decision that could be carried
out — and a re-run starts the item over from the target branch, discarding the
session and whatever the stalled attempt had left uncommitted in the preserved
worktree. `yoyo triage rerun` is still the right verb where the ground has moved
and the work is to be done again; it is no longer the only one available.

**The harness continues a first silent-stream stall itself, once.** A run whose
provider stream went silent during development or review does not wait on her
decision after its first silent session, including when it was already repairing
a failing check or reviewer findings. The interrupted repair keeps its original
input and the attempt already counted; continuing it adds no attempt or grant.
Once the sweep has settled it, its docket entry says the harness stopped the provider and nothing was judged, names the
harness as the next mover, and a watching `yoyo work` session's next pull with
a developer slot free continues it itself — in the same worktree and developer
session, at the phase it stalled in, with no decision recorded and no review round, repair attempt, repair grant, or
re-run spent. The continuation is recorded on the run (a repair continuation
marked `by_harness`, granting nothing) and noted on the item, the entry is
closed in the harness's name, and while it stands `yoyo status` holds the item
as the harness's move rather than as a decision waiting on anybody. The
operator's pause and the intake hold stop it exactly as they stop a recorded
decision's carry-out; a decision she records about the current stoppage first is
carried out instead. A decision already carried out for an earlier stoppage of
the same run does not prevent this continuation. If the watching session restarts
between the silence and the continuation, its replacement reads the run and
docket records and carries on the same run under the same bounds. Maintenance
keeps its checkout and branch while the automatic continuation is outstanding, including when intake or capacity delays
it beyond the retained tail. An interrupted developer's uncommitted work stays
in that checkout; recreating committed files from a branch cannot recover it.
It is held to what her repair of a stall is held to: the
worktree has to be as the harness left it, and a stall at the checks, review,
or a repair already underway must still hold the change. One that fails either is written onto the run
(`stall_continuation_refused`), the item is told, and the stoppage is docketed
again for her. **The harness does this at most once for one run.** A run that
stalls again after the harness carried it on is settled and docketed as before,
and its entry says the harness's continuation is spent and what happens next is
her decision. A provider stopped for running out of its total budget rather
than for going silent is not continued this way. Where the run had been stopped
for a [redeploy](#a-session-draining-to-restart-into-a-deployed-build) and
re-adopted before it stalled, the entry and the item's note say so. Where it
stalled at the phase it was re-adopted at, they say the stall began in the
session that re-adoption resumed; where it stalled later, they say only that
the re-adoption came first. That was the case for the second of the two stalls
this was built on:
[the account of run-008b0e25](diagnoses/yoyodyne-a0s-stall-after-readoption.md)
shows it stalled in the session its re-adoption resumed, after that session had
already written its final reply and was kept alive by background checks it had
started. Until the harness continued first stalls itself (yoyodyne-a0s),
every such stall waited on her decision after the half hour — two runs in two
days, on the unmeetable-item-returns item (yoyodyne-ifd.428.44) and the program
manager pass-and-query item (yoyodyne-ifd.430.13.8), each for a continuation
its own docket entry said cost nothing.

## When a run says more than the harness keeps

There is a third bound beside those two, and it is not a deadline: how much of a
process's output the harness holds in memory. It is 8 MiB, and what it bounds is
the copy — never the process, and never the run. A run that bursts its output
diagnosing something is a run doing its work, and a parity harness that diffs
execution traces is exactly that workload.

Output past the bound is truncated with a marker, and the process carries on to
its own end:

```text
[output truncated at 8388608 bytes; the whole of it is in the event log of run-32e3f059…]
```

The marker follows the rule a cut Slack message already follows: it names the
durable record holding the whole, so nobody reads a cut copy as everything the
process said. For a check and for a provider invocation that record is the run's
own event log, because every line goes into it on the way past — the retained
copy is the diagnostic beside it rather than the original. A command that keeps
no such record says the rest was not retained instead, in those words, rather
than sending you after a file nobody wrote.

The truncation is said out loud in the same event stream `yoyo status --follow`
follows, and it is on the result the record keeps, so it is visible both to
somebody watching and to somebody reading back months later.

This used to end the run. The output bound was a read error, so a verbose run
failed with its provider's own result event never parsed — no cost recorded, no
verdict, and a claimed work item left sitting behind a process that was not
coming back. `run-32e3f059` died that way on 2026-09-03, with zero dollars
recorded and six hours of silence after it. Keeping traces in files was the
workaround; nothing needs it now.

### One line that is too long

There is a second bound underneath that one: how much of a single line the
harness holds, which is 1 MiB. It exists because output is read a line at a time
and a line has to be complete before anything can be done with it, and it is hit
by different output than the 8 MiB total — a provider stream puts one tool result
on one line, so a single large file read can reach it while the invocation as a
whole is nowhere near verbose.

It follows the same rule. The line is cut, the rest of it is read and thrown away
so the process is never blocked writing it, and the cut line ends with a marker:

```text
…[line truncated at 1048576 bytes; 3407872 further bytes were not retained]
```

The marker names no record holding the rest, because there is none — unlike the
8 MiB bound, which cuts a copy while every line still reaches the event log, this
one drops what it cuts. What follows the long line is read normally.

For a provider stream a cut line is no longer an envelope, so nothing is read off
it: it is recorded as a `truncated_stream_line` anomaly in the run's event log
and the stream carries on to its own result. A stream the harness genuinely
cannot read still fails the run with a decode error, which is why the runner
marks the line rather than leaving that to be guessed from the failure. If the
line that was cut happened to be the invocation's terminal, the invocation ends
without one and is answered the way any other lost terminal is — the anomaly in
the log is what says why it is missing.

This used to end the run too, in the harder way: the process was killed on the
spot with `token too long`, so the work in the worktree and the invocation's cost
went with it.

## What a check stage may cost, and where the whole suite runs

A run's checks are bounded twice, and the two bounds answer different
questions. `execution.check_timeout` is what one check may spend, thirty
minutes by default. `execution.check_stage_timeout` is what the whole list may
spend, from the first check starting to the last one ending — thirty minutes
by default, and in minutes on purpose. That figure is for an idle machine: the
bound in force is it scaled for the machine's one-minute load average exactly
as a local Git command's budget is, multiplied by how far the load is above the
number of cores and capped at ten times, read again as each check begins and
never lowered. [What a whole check stage may
cost](configuration.md#what-a-whole-check-stage-may-cost) says why. The second exists because the first says
nothing about the list: on 2026-09-19 a run on this repository sat in its
checks for over two hours under load, every check inside its own budget and
`make race` alone past ninety minutes, holding a developer seat and the watch
session's drain for the whole of it. Twenty-one timing-flake reports in the
same fortnight were the same suite failing under the load it was creating.

**A stage that reaches its bound ends the run as a stoppage**, `timed_out`,
with the change preserved and no repair attempt spent — a stage the bound
stopped never judged the change, so there is nothing to hand a developer. It is
recorded on the run as a stop from outside the work, of cause
`check-stage-bound`, and counts toward nothing: not the intake brake, and not
the item's review rounds, repair grant, or re-run. The reason names the bound,
the configured figure and the load that scaled it, the check the bound stopped
and how long it had run, what the stage had spent across how many checks, and
what moves it — here on a machine at three times its cores:

```text
the check stage reached its 1h30m0s execution.check_stage_timeout bound (the configured 30m0s scaled for a one-minute load average of 48.0 on 16 cores) during make race, which had run for 1h12m0s; the stage had spent 1h30m0s across 3 check(s) (gate narrowed to: the whole module (the repository root is not a Go module)); narrow the per-run gate to what the change touches with $YOYODYNE_CHANGED_GO_PACKAGES, move the whole suite to landing_checks, or raise the bound
```

It is a different stoppage from a check reaching its own budget, and it is
worded as one, because raising `check_timeout` does nothing for a check the
stage stopped. Both leave the branch and the worktree where they were.

**The harness continues a stage the bound stopped, at its checks, by itself.**
What stops a stage at its bound is nearly always the machine — three runs' race
suites beside each other — rather than the change, and until
yoyodyne-ifd.429.25 the only thing that fired for one was a re-run from the
target branch: repair was refused because nothing was handed back, resumption
covers only approved changes, and the claim audit gave the item back half an
hour later to a fresh run that redid the development while the finished change
sat on its branch. Now the stopped run is docketed as it ends, and the watching
session's pull continues it itself — no development manager decision — on the
first pull where a developer slot is free, ahead of fresh work at equal or
lower priority. Machine load does not gate a continuation, just as it does
not gate fresh work. The run is made live again at its checks, on the same
branch and in the same worktree, and the checks are re-run
on the change it already has; no developer is invoked, and no review round,
repair grant, or re-run is spent. It is held to the conditions a repair is: the
worktree has to be as the harness left it and still hold the change, and the
item has to be one a run may continue on. The operator's pause and the intake
hold stop it exactly as they stop a recorded decision's carry-out. The claim
audit leaves such an item's claim alone. After 30 minutes from the durable
record of the run ending, any remaining gate and what clears it are noted on
the item. Restarting the watcher does not reset that wait or repeat the same
note. Intake holds, the spending pause, capacity, item eligibility, and the
worktree checks still apply; elapsed waiting never counts a check as passed.

The checkout and branch are kept for that outstanding continuation, even after
the run falls outside the maintenance sweep's retained tail. If the checkout
is already missing, continuation can restore it from the surviving recorded
branch at the completed commit the harness recorded. Ownership and the recovered
revision are verified; verification credit is cleared durably before writing.
The same run, developer session, and consumed budgets continue at the checks.
A restart verifies an unfinished restoration before continuing it. Conflicting
paths, missing or changed branches, unverifiable state, and separately captured
uncommitted work refuse restoration and return the stoppage to the development
manager without spending a continuation. Missing uncommitted work is never
claimed recovered.

The docket entry and the run's line in the channel say it in one sentence:

```text
the check stage was stopped by load at its execution.check_stage_timeout bound, not by the change: nothing was judged and nothing was handed back to the developer; the harness continues it itself, re-running the checks on the change the run already has, on the same branch and worktree, at the next pull with a developer slot free, ahead of fresh work at equal or lower priority, without waiting on machine load; after 30 minutes waiting, the item names any remaining gate and what clears it — no developer is invoked and no review round, repair grant, or re-run is spent (continuation 1 of 2)
```

and the entry's next mover is the harness. The continuation is recorded on the
run (`check_stage_continuations`) and noted on the item, and the entry is closed
in the harness's name. **The harness does this at most twice for one run.** A
stage the bound stops a third time is a suite that does not fit its bound —
a decision about the gate or the bound,
not something another try settles — so the entry then says the harness's
continuations are spent and the stoppage is the development manager's, as it
was before. So is one whose worktree somebody has been in, or whose change is
gone: the refusal is written onto the run
(`check_stage_continuation_refused`), the item is told, and the stoppage is
docketed again for her, and the harness does not ask again. A stage stopped by
a check reaching its own `check_timeout` is not continued this way.

**While the checks run, the bound is what `yoyo status` shows.** A run in its
checks says where the stage stands in place of the bare phase — how much of the
bound it has spent, with the configured figure and the load beside a bound the
load raised, and which check it is on:

```text
Running (2 developer runs):
  yoyodyne-ifd.389 (Timing-bound tests do not fail the gate under machine load) — checks: 14m of 30m, on make race, 1h02m elapsed, $4.10 so far
  yoyodyne-ifd.432.13 (…) — checks: 41m of 90m (30m configured, scaled for a one-minute load average of 48.0 on 16 cores), on make race, 1h20m elapsed, $6.75 so far
```

`yoyo status <beads-id>` prints the same line under a run that is in its checks
or that the bound stopped, and the item's notes carry `Check stage: 14m0s of
the 30m0s execution.check_stage_timeout bound` above the per-check lines, with
what the gate was narrowed to. The run's Slack thread says what the stage spent
of its bound when the checks pass, so the thread reads "checks passed in 14m0s
of the 30m0s bound" rather than only "passed".

**Where the whole suite runs is once per landing.** The stage fits its bound
by running the expensive suite narrowed per run and whole per landing. Every
check is handed `YOYODYNE_CHANGED_GO_PACKAGES`, the Go packages the change
touches, and a check written to read it — `make race
RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"` here, with the shell's
unset-only default so the same line tests the whole module wherever the
harness did not set the variable — runs over those alone.
`landing_checks` is then what runs whole, once per landing on the target
branch: after a run has integrated, closed its item, and removed its worktree,
the harness cuts a detached checkout of the integrated commit under the
worktree root, runs the list there, and removes the checkout. The landing has
a budget of its own, `execution.landing_check_timeout` (two hours by default,
per check, with no stage bound over the list), because what is moved there is
the suite the gate's stage bound cannot hold. The run is over before they
start, so they hold no seat, no claim, and no place in the run queue, and the
next developer run starts beside them; what they do hold is the process that
ran the landing — `yoyo run` reports only once they end, a `yoyo work` drain or
`--limit` returns only once every run it started has landed, and a deploy's
restart waits them out with the runs — for up to the budget times the number
of landing checks, plus any wait for its turn. The landing checkout compiles
against the repository's shared build cache, the one under the common Git
directory every run's worktree uses, rather than a cold cache of its own.

**Landings on one target branch run one at a time.** A second landing does not
start beside the first: landings queue on a lease per target branch — an
advisory file lock beside the branch's promotion lease, dropped by the
operating system when its holder dies, and separate from it, so a landing
holds nobody out of integration. Without it, two runs landing back to back
would run two whole race suites at once beside the next runs' gates, which is
the load the suite was moved to the landing to escape. A landing that
finds another running records when it began waiting before it waits, and its
checkout is not cut until the first landing's is removed. While it waits,
`yoyo status` says so under the run, where it would otherwise say the checks
are running:

```text
  landing checks waiting over 3d3d367a1b2c behind another landing on main, since 2026-09-19T14:02:10Z
```

The wait is bounded by what the landing ahead may take — the budget times the
number of landing checks, and a fifteen-minute margin for its checkout —
and a landing that waits it out runs nothing and is unverified, saying which
queue it waited on. The bound covers one landing ahead: a third landing queued
behind two that each run their whole budget waits it out and is unverified,
which is the case to look for when several land back to back. A landing that did wait says how long beside its result.
What the landing made of the commit is recorded on the run and said on the
item and in the thread:

```text
green landing: 1 landing check passed over 3d3d367a1b2c in 18m
green landing: 1 landing check passed over 3d3d367a1b2c in 18m, after waiting 12m behind another landing on main
red landing: make race exited 1 over 3d3d367a1b2c; filed as yoyodyne-ifd.402
unverified landing: the landing checks did not run to the end over 3d3d367a1b2c (make race was stopped at its 2h0m0s execution.landing_check_timeout budget after 2h0m0s and judged nothing)
unverified landing: the landing checks did not run to the end over 3d3d367a1b2c (the landing checks never started: wait to land on main: another landing held the lease for the whole 2h15m0s wait)
```

A landing check stopped at its budget judged nothing, exactly as a gate check
the harness stops on time judged nothing, so the landing is unverified rather
than red and files nothing: the record and the channel say which check was
stopped and at what budget, and raising `landing_check_timeout` is the remedy.

**A red landing files its own item and blocks nothing.** The run that landed
the change passed its gate and was approved, so the run stays succeeded and its
item stays closed. What the harness does is admit a bug at priority 0 — the
front of the queue — naming the target branch, the commit, the failing check,
and the run and the item that landed it, with the check's bounded output in
the item's notes, under the goal the landed item served, because every run
after it is cut from that commit. A branch that stays red is one such item:
a later red landing of the same check finds it open, notes the later commit on
it, and files nothing. The red landing reaches the channel as a warning naming
the item it filed; a green one stays in the thread; an unverified one reaches
the channel too, because a landing nobody verified reads as green to anybody
who was not told.

**A process that dies inside the landing checks is settled by the sweep.** It
leaves a run that is over with a landing the record says is still running, and
the checkout the checks ran in — `landing-<run>` under the worktree root —
still registered. Such a run owes a step, so `yoyo reconcile` takes it up:
where the process is really gone (a live one still holds the run's lease and is
left alone) the landing is settled as unverified, saying the process died —
and, for one that was still waiting its turn behind another landing, that it
died waiting before its checks started — and the checkout is removed. A checkout the sweep could not remove is named on the
run for somebody to remove by hand. A run killed inside its per-run checks is
settled the same way: the sweep closes the stage as interrupted, naming the
check it was on, so `yoyo status` stops saying the checks are running under a
run that has ended.

[What a whole check stage may cost](configuration.md#what-a-whole-check-stage-may-cost)
and [where the whole suite runs](configuration.md#where-the-whole-suite-runs)
in the configuration guide are the settings and the arithmetic.

## A session draining to restart into a deployed build

A watching `yoyo work` session runs the binary it was started from, so when you
install or rebuild `yoyo` over it, the session restarts itself into the new
build — [how work flows](work.md#letting-the-harness-choose-the-work) says why
nothing outside the process can. Between finding the deploy and restarting, the
session is **draining**, and it is worth being exact about what that does and
does not stop, because on 2026-09-19 it stopped everything: at 07:35Z the
session logged that it was restarting, then did nothing for over two hours
while the one run it hosted sat in `make race` under load — no pull into the
second seat, no recurring task, two hourly passes missed, and the operator
learned by looking.

**A drain stops nothing but the wait on the runs it hosts.** A draining session
still polls, still pulls ready work into any free seat, still puts stopped work
in front of the development manager, and still fires every recurring task as
its cadence comes due for as long as it hosts a run. Once it hosts none it
starts no new pass, since a pass begun then would only hold the restart; the
session that comes back takes it. The one thing it declines is a pull made with the bound less
than one poll away, which would start a run only to stop it — a development
manager's decision it would otherwise carry out included, which the session
that comes back carries out instead; that pull is skipped, and the skip is said in the watch log and in `yoyo status` — which
names it as the session restarting, not as an idle session over a queue with
work in it — rather than looking like a poll that found nothing.

**The drain is bounded.** It restarts the moment it hosts no run and no recurring
pass is taking its turns, and otherwise
waits at most `execution.redeploy_drain_limit` — fifteen minutes by default,
minutes rather than hours on purpose. Past that it restarts anyway, and at
once: in the same step that stops the runs it hosts, before any further pull
or recurring pass. A run it has stopped is not a run it hosts, because its
process is gone; the session waits only the seconds each stopped run takes to
record its stop, up to two minutes for one that never reports back, which is
then named in the restart's line and left to its own record. A recurring pass
still taking its turns is stopped in the same step and waited for only while it
records itself as a missed pass. Only a run at its
promotion, or a check stage a moment from recording its verdict, is still
hosted past the bound:

- Each run it still hosts at its developer attempt, its checks, or its review is
  stopped where it is and **preserved whole** — worktree, branch, claim,
  developer session, repair attempts, relaunches, review rounds, every counter.
  The run's record carries a `redeploy_stop` naming the phase, the bound, and
  the session that stopped it; the work item gets a note saying the run was
  paused for a redeploy, not failed.
- The session that comes back **re-adopts** each of them at its first pull,
  ahead of anything new and into the seat the run already holds, and continues
  it from the recorded phase: a developer attempt resumes in the same session,
  and a run stopped at its checks or its review re-earns the gate from the
  checks. Its selection reason says it was handed over rather than chosen. A
  re-adoption the pipeline could not take at that moment — a lease another
  process held, a tracker that did not answer — is made again at the next
  pull for as long as the run's record carries its stop, reported on one line
  of the pass with the count of tries, and counts toward nothing.
  `yoyo run <beads-id>` continues one the same way if no session does, and
  `yoyo reconcile` leaves it alone as a run its own pipeline can continue.
  The session that stopped a run never re-adopts it, and a draining session
  re-adopts nothing: while it waits out a promotion past the bound, the run it
  stopped stays stopped, holding its seat, rather than being resumed only for
  the next look to stop it again.
- A run at its promotion is waited out: it holds the target branch's
  lease, and a promotion cancelled part-way is the one boundary durable state
  cannot describe, so the session waits it out past the bound and restarts
  after it. The wait is the session's ordinary loop and not a silence — the
  pull is still opened every poll and every recurring task still fires on its
  cadence; only new starts are declined, and each declined pull says so. A
  forge outage can hold a promotion for hours, and those hours cost the
  scheduler nothing but the seat the promotion holds.
- A run still running its checks is stopped at `execution.redeploy_drain_limit`,
  independently of the check stage's own bound. Load can scale
  `execution.check_stage_timeout` to hours, and waiting for that would leave
  the session on the old build for hours. The session that comes back runs the
  interrupted stage again from the start. A stage that has already finished
  gets up to a minute's grace to record its verdict and move the run on to its
  review or a repair; the next look stops it there. This brief wait uses the
  ordinary loop too, so pulls and recurring tasks continue.
- A run that has already landed and is running its
  [landing checks](configuration.md#where-the-whole-suite-runs) is stopped at
  the bound too. The run is over and its item settled, so nothing is preserved
  for re-adoption: the stopped check judged nothing, the landing is recorded as
  unverified with its reason naming the redeploy, and nothing is filed. The
  landing budget allows hours a check, which is exactly the wait the bound
  refuses.
- A recurring pass falling due during the drain is not skipped. One due while
  the session still hosts a run — a promotion or a check stage waited out
  included — is made then, and one due once it hosts none is made by the
  session that comes back at its first pull, because the cadence is claimed
  durably and the restart takes a minute. A pass already taking its turns is
  waited out like a run until the bound. The pass the drain costs is one still
  taking its turns when the session restarts past the bound: it is stopped, and
  recorded in `yoyo sweeps` as a missed pass cancelled before it completed, so
  what it would have looked at waits for that task's next pass rather than
  holding the restart past the bound.
- A run the bound stops **before it recorded anything a continuation could pick
  up** — no developer session yet — is not held with a marker nothing can act
  on. It is recorded as cancelled, with its branch and worktree preserved and
  its reason naming the redeploy, exactly as a run a killed process leaves:
  `yoyo status --failed` lists it, the item's note says why, and the item stays
  claimed until `yoyo triage rerun` starts it over or somebody releases it.
  Nothing is ever silently held.

What a stopped run loses is the minutes its current phase had spent: a
developer attempt resumes in the session it was making, and a review is asked
for again. An interrupted check stage starts again from its first check when
the run is re-adopted. The bound stops a developer attempt, running checks,
and a review; a promotion is waited out past it. An already-finished check
stage gets up to a minute's grace to record its verdict and move to the next
phase, where the next look stops the run. Set the bound longer if that trade
is wrong for your project, and never to nothing — the configuration refuses
a drain with no bound.

**`yoyo status` names the drain throughout.** The session's line says it is
draining, since when, under what bound, and until when, on every transition it
writes while the drain lasts; once the bound has run out, or a pull has been
declined for being within a poll of it, the not-startable line names the
restart as its own state — whose move is nobody's, because the session comes
back on its own — rather than reporting an idle session or no session, either
of which would send you to start one that is already on its way back. Running
check stages are stopped at the drain limit and preserved for the next session
to run again from the start. A run at its promotion is never stopped: it holds
the target branch's lease, and the session waits it out however long it takes
and restarts the moment it ends. While it does, its line says how many runs it
is waiting out at their promotion and since when, and the not-startable line
names the restart that way, dated from when that wait began. The session says
it again every ten minutes while the wait lasts, so a promotion that runs for
an hour is never reported as a problem; a line saying so that has gone thirty
minutes without being said again is a session that died waiting. A session
giving an already-finished check stage its brief grace to record its verdict
writes nothing while that wait is unchanged. The bound having run out reads
that way for ten minutes past the bound. A session still draining after that,
and not saying it is waiting out a promotion, has stopped restarting — stuck,
or killed while it waited — and is reported as a factory problem rather than as a session on its way back: `yoyo status`, the
dashboard's **Factory problems** section, and the channel's heartbeat name the
session and say since when it has been draining past its bound, whose move is
the harness's, and that restarting the watch session takes up the deployed
build with the runs it stopped picked up by the session that comes back. It is
said even when every developer slot reads as taken, because the runs it
stopped keep their seats. The stop
recorded as a restart reads that way for two minutes, which is the minute the
re-execution is given plus slack: a new build that dies in its own startup
after the exec writes nothing, and past that it reads as the ending it was —
no session running, and yours to start. The pass's own report, when the
session returns, says the same: when the deploy was found, the bound, which
runs it stopped and preserved, and any landing checks it interrupted.

Older watch records can still name a running check stage being waited out and
its latest deadline. For those records, status reads the session as restarting
until two minutes past that deadline if that is later than thirty minutes past
its latest line, and an older restart report can list the stages it waited out.
New records omit `checking`, `checks_until`, and `checks_waited`: running stages
are interrupted at the drain limit, while the brief grace applies only to
stages that have already finished.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got
to. `yoyo reconcile` settles what it left behind, and then converges your local
state onto what the forge has:

```sh
./bin/yoyo reconcile --json
```

**Run it from wherever you are standing, the preserved worktree included.**
Recovery and inspection happen inside the worktree a failed run left behind,
and a project whose `.yoyodyne` is checked in gives that worktree a copy of the
configuration — so every verb here, run from inside it, used to resolve the
repository to the worktree itself, a directory under `execution.worktree_root`,
and refuse with `repository and worktree roots must not contain one another`
(reported twice on 2026-08-19 and fixed in `yoyodyne-ifd.335`). Now `yoyo
reconcile`, `yoyo cost`, `yoyo directive`, `yoyo pause`, `yoyo resume`, `yoyo
run`, `yoyo review`, and `yoyo chat` run from inside a worktree the harness
manages address the checkout that worktree was added from — the primary
checkout, which is what every one of them means by "the repository" — exactly
as they do run from that checkout; `yoyo status` and `yoyo reports` never
built the manager that refused and were never caught by it. The one thing
still refused is a repository that sits under the worktree root and is not a
worktree of a checkout outside it, and that refusal says what to do: run
`yoyo` from the checkout the worktrees were added from, or point
`execution.worktree_root` outside the repository.
`TestVerbsRunFromInsideAManagedWorktree` in `internal/cli` drives the verbs
from a worktree it creates, and [the configuration
guide](configuration.md#discovery) says how the resolution is made.

It compares the recorded run against the repository and Beads, and then finishes
the run's own remaining step or records what still needs doing on that item.
A settlement refused for one item, including a remote branch deletion or an
unreadable forge answer, leaves a finding on that item's run record and notes.
The finding names the refusal, the remedy, and who moves it: the harness retries
the settlement, and the development manager decides what to preserve when a
moved remote branch carries work outside its target. Reconcile finishes the rest
of the pass and reports each item's settlement or remaining finding; these
findings alone do not make the command or the supervisor's maintenance pass
fail. A failure to discover the pass's state still fails the command. Repeating
the same refusal retries the settlement without adding another finding note;
an undelivered note is retried too. Every pass revisits saved findings under
their run's lease, including publications and checkouts whose settlement has
finished and that no operation sweep selects any more. A completed settlement
whose finding note or clearing save is refused remains recorded for retry;
attention names that delivery or clearing obligation rather than a settlement
still refused. The item is checked for a note already delivered before another
is appended. If a branch was removed elsewhere, the next sweep confirms its
absence and clears the saved refusal under the run's lease.

**A run whose item already merged is retired before it is continued.** When
the item is closed and another run has a later, settled publication with its
merge confirmed into the same target, reconcile records that merge and the run
it supersedes on the old run and on the item. Publication order comes from the
forge's pull request numbers, and the confirming request must have a higher
number than the old run's own request. Progress recorded afterwards on an older
run does not make its request newer or hide the confirmed merge. For a run
without a request, the confirming run must have completed after it began;
completion before or at its start does not authorize retirement. The old run ends cancelled,
releasing its developer slot, integration reservation, and the files it held
for scheduling. Its branch,
worktree, developer session, publication record, and execution history are kept;
retirement does not reopen the item or replay its change. This check also runs
before withdrawing a queued merge to update its head, before selecting an
already waiting update, and again when the pipeline takes up the selected run,
so a merge confirmed between selection and execution ends the old work too.
Later passes announce nothing more. A retirement note that could not be
delivered, or whose delivery marker could not be saved, is retried from the run
record without appending the same note twice.

Closed status alone proves no merge. A closed item with no confirmed later
publication keeps its run and leaves a finding for the development manager; a
later publication still being settled keeps the run for the harness to retry.
An explicitly reopened item follows the existing continuation rules. Update
and expired provider-wait refusals, like other settlement findings, name their
next mover and do not fail the reconcile command or the maintenance pass or
stop other items being settled. Retirement ends any recorded wait, keeping its
account in the retirement history rather than promising another continuation.

A run it settled into an ending that is not success is reported twice over: what the sweep did with it,
and — in the same words `yoyo status` uses — what became of the run and what
remains of its change. Those are different facts, and only the second answers
whether your work is still there. A run whose work landed says only what the
sweep did, because a successful run removes its branch and worktree on purpose
and there is nothing preserved to report. It also builds the triage
docket on the way past, so a run it stopped and a publication the forge quietly
never merged reach the development manager rather than waiting for somebody to
go looking. A run that died holding its change — a push the remote refused, a
backend that broke mid-attempt — is docketed too, but by the run itself as it
ends rather than by this sweep: the sweep re-derives blockers from the whole
recorded history, and every terminal failed run with a surviving branch has the
shape of a death, so re-deriving those would put months of settled work on the
docket at once. A death from before this existed is therefore not on the docket
and will not appear on one. **A run that died before it claimed its item** — a
dispatch the tracker refused, anything that failed before the first thing a run
changes outside itself — is docketed the same way and for the same reason, as a
*run that died before it started*. It is the one failure that leaves nothing at
all: no blocker on the item, because the item was never taken, and no branch,
because no worktree was cut. Every other rule here reads that as nothing having
happened, which is exactly how one item was dispatched twenty-nine times in
twenty hours with no surface saying a word. Its entry names the run and the item
it tried to claim, says the item is untouched, and carries the failure. Like the
death above it is recorded where it happens and never re-derived by the sweep,
so a pre-claim death from before this existed is not on the docket either.
**A dispatch that failed before any run was reserved** is one layer earlier
still, and is docketed as an *attempt that never became a run* by the
[watching session](work.md#letting-the-harness-choose-the-work) that made it. There is no run
to name — nothing wrote a record, which is why nothing else could ever find it —
so the entry carries what the record would have: the item, why the scheduler
selected it, what stopped the dispatch, and that the session will not try it
again until the item changes. It is keyed to the item and the failure rather
than to a run, so the same dead dispatch is one entry however many sessions meet
it. **The sweep also closes the entries of closed items**: every entry still
standing for an item the tracker holds as closed or retired is closed with it,
with the reason, and the sweep says how many (`closed_with_item` in `--json`).
It also tells the item of each escalation to you that has ended — the item
parked, retired, or closed, or the escalated run's branch and worktree both
gone — once, and records the ending on the run (`escalations_ended` in
`--json`; see [where a finding that needs your hand
goes](#where-a-finding-that-needs-your-hand-goes)).
An unfinished publication's entry is the exception and stays, because an item
closes on integration while its merge can still be dropped or stuck at the
forge.
The places that close an item close its entries as they do; this is what
catches the rest ([an entry closes with its
item](conversation.md#deciding-what-becomes-of-stopped-work)). A run
whose work reached
the target branch is completed — its item closed where the run's landing
discharged it, put back in the backlog parked or waiting where it did not — and
its worktree and branch removed, including when
the run died before it could record the promotion. A run stopped anywhere
earlier becomes a durable blocker naming the branch and worktree that were
preserved. A run that finished with its merge queued at the forge is settled
here too: reconcile asks the forge and, once the merge has landed, finishes the
publication — merge commit recorded and your local target branch caught up onto
the remote target, which carries the forge's merge and whatever landed after it
— and settles the work item, which the run
deliberately left open because a queued merge is a
publication nothing has confirmed. On a target the forge protects, the run
moved nothing locally, so it also left its branch and worktree exactly where
they were: nothing proves the change is on the target until the forge's merge
is confirmed, and the kept branch is what a head that falls behind is brought
up to date from, below. They are removed by the settlement that confirms the
merge and by nothing earlier, and a blocker written while they stand names them
as preserved. The run's notes on such a target say the change is to land on the
target by the forge's merge and that the local target is not moved until then —
never that it is integrated into the local branch. Where it goes is what the run's own landing
says: closed where the landing discharged the item, back in the backlog parked
or waiting where it did not. Settling a merge
is complete on its own that way rather than leaning on the sweep below, so a
checkout is never left behind by which command somebody happened to run.
The branch the merge consumed is deleted **after** the item is settled, and its
removal cannot hold the settlement up: it is hygiene on the forge rather than
part of the publication, so a connection that drops at that last step leaves a
dead branch and a settled item rather than an item that reads as unfinished
work. It
is asked again on the recoverable-failure backoff before it gives up, and what
it leaves if it does is recorded below.

**A merge the forge still holds is read with its checks.** "Queued" says the
forge will merge the request once the base branch's requirements are met, and
nothing about whether they ever will be: pull request 609 sat queued for 33
hours against a red build, and 713 sat 31 commits behind main failing two tests
its change never touched while the development manager waited on it because the
record said the merge was queued. So every sweep that finds a merge still queued
also asks the forge for the head's checks — which failed, the files each failing
check's annotations name, and how many commits the target has that the head
does not — writes that onto the run's publication record, and decides on it.
A request the forge's merge queue has taken counts as a merge the forge still
holds: the queue consumes the request's auto-merge as it takes it, and the
request stays open until the queue lands it, so the harness asks the forge
whether the request is in the queue before it reads an open request with no
auto-merge as dropped, and a forge that does not answer that leaves the merge
queued for the next sweep. On 2026-09-27, the morning after main gained a merge
queue, pull requests 832 and 834 were each handed to a person as dropped while
the queue was landing them.

- **Checks passing, or still running.** The merge stays queued, and the reading
  goes with it everywhere the merge is named: the sweep's line, the
  publication's entry on `yoyo status`'s fourth line — under `Waiting on the
  forge`, since a queued merge is the forge's to land — and the docket entry once the request has
  sat past `triage.stuck_merge_age`.
- **A head behind its target whose failing checks name no file the change
  touches.** The failure is one the change met on a target that has moved on,
  not one it brought, so the harness does what a promotion that lost its race
  does. The sweep withdraws the queued merge first — a merge left armed would
  land the rewritten head the moment its checks passed, before any reviewer
  saw it — tells the item, and puts the run back at its promotion (an
  integration resumption of cause `queued-head-behind`). The sweep's last step
  hosts it, beside the usage-limit continuations: the promotion finds the target
  moved, replays the change onto it, runs the checks again, gets a fresh
  independent review, and queues the merge again. That is a lost race, and like
  every lost race it is never handed back for being one: the replay's own gate
  charges `execution.integration_retries_before_reconciliation` if the replayed
  change stops on the change. Only a run that cannot be replayed at all is
  handed back. `yoyo reconcile
  --json` carries what each came to under `updates`. Where the moment is wrong
  rather than the run — intake held, every developer slot taken, or a pass that
  hosts no runs, such as the settle a conversation makes — the merge is left
  queued with the checks beside it and the reason, for the next `yoyo reconcile`.
- **A job the forge ended itself.** A check the forge reports as cancelled,
  timed out, or never started (`startup_failure`), whose only annotation is the
  forge's own on `.github` or that has none, was ended before any step decided
  anything, so before anything is decided on it the sweep asks the forge to run
  that job again on the same head
  (`POST /repos/{owner}/{repo}/actions/jobs/{id}/rerun`) and leaves the merge
  queued. A re-run that passes is the forge landing the merge. Each head gets at
  most two re-runs, counted on the publication's check reading with the check
  runs sent back; the forge gives a re-run a check run of its own, so a sweep
  that reads the old one again before the re-run has started spends nothing. A
  job ended again after both re-runs, or one the forge will not run again (a
  check no Actions job ran, or a `gh` token without the right to re-run jobs),
  is handed back as below, saying the forge ended it. A job waiting on a
  person's approval is not re-run. Neither is a step that failed: GitHub files
  "Process completed with exit code 2" on `.github` too, and that is what a
  genuine red test looks like, so a failed step annotated only there is not
  re-run. It names no file the change touches, so on a head level with its
  target it is confirmed as the target's failure or found to be the change's,
  and filed and waited on or handed back, as the next bullet says, with the forge's account of the job carried on the filed item; on a
  head behind its target it is brought up to date as the bullet above says.
- **A head level with its target whose failing checks name no file the change
  touches.** Nothing but the change differs from the target, so bringing the
  head up to date would change nothing, and the failure is the target's own — a
  required check red on main itself, or flaky there, which every merge queued
  behind this one meets too. **It is filed as the target's only once the harness
  has confirmed it is.** A check that names no file can still be the change's:
  a test runner that reports failures per package annotates nothing, and on
  2026-09-29 pull request 907 failed the build on a test in a package its own
  change added, was filed as main's failure, and had its approved merge
  withdrawn while `make test` passed on main (yoyodyne-c02). So before filing,
  the harness asks the forge how the same check ended on the commit the target
  branch points at. Red there too, it is the target's. Passing there, it is the
  change's. Where the forge cannot say — the read fails, or the check has not
  run or not finished on the target's head — the harness reads the check's
  annotations and the last 400 lines of its job's log, and counts any file the
  change adds or modifies, or the directory one sits in, named there as the
  change's; a directory at the top of the repository counts only where it is
  named as part of a path. A failure that is the change's is handed back to be
  repaired exactly as the **Anything else red** bullet below says, with the
  reason naming what made it the change's, and nothing is filed against the
  target. The filed item says in its notes how the check was confirmed: red on
  the target's own head, or not confirmed there with nothing of the change in
  its log. Once confirmed, it is filed as the target's, exactly as a
  [red landing](#what-a-check-stage-may-cost-and-where-the-whole-suite-runs) is:
  one bug at priority 0 per target branch and failing check, naming the branch,
  the head, the check, and the run and item that met it, under the goal the item
  served, with the forge's account of the check (below) in the item's notes.
  A later request meeting the same check while that item is open finds it by
  the `Red forge check: <check> on <branch>` line in its notes and is noted on
  it rather than filing again. The queued merge is withdrawn, the
  work item is told and made to wait on the filed item in the tracker, and the
  publication is recorded as waiting on it (`target_red` on the pull request)
  with **the harness as the one to move**. Nothing is handed to a person: no
  blocker, no dropped merge, and no decision on the development manager's
  docket. The docket entry says `Waiting on the target` with the check and the
  item and names the harness as next mover; `yoyo status` carries it under
  `Waiting on the harness`, saying the request waits on the target's red check
  and naming the item; and the channel is told once, as a warning, naming both.
  A filing the tracker refuses leaves the merge queued, writes nothing, and the
  next sweep tries again. **What ends the wait is the harness's too.** Once
  every item it waits on is closed, `yoyo reconcile` reads the head's checks
  again: a head the fix left behind the target is brought up to date from the
  kept branch, checked, reviewed, and queued again, exactly as a queued head
  behind its target is; a head still level and failing the same way is filed
  again, since the items closed with the check still red; and a head still level
  whose checks now pass is armed again by a watching `yoyo work` session's
  re-arm carry-out at its next pull, with nobody deciding anything and no re-arm
  spent. `yoyo reconcile` says what each waiting publication came to on every
  pass, and `--json` carries it under `red_targets`. Until that arming, `yoyo triage rearm` refuses the publication while an
  item it waits on is open, naming the item. A head still level whose only
  failures are jobs the forge ended itself is neither of those: the jobs are
  run again on the same head, at most twice, with the wait standing meanwhile,
  and a job ended again past that, or one the forge will not run again, is
  handed back as below, saying the forge ended it. Until filing a red forge
  check as the target's own failure (yoyodyne-m5p) this case was handed back
  like the one below, and on 2026-09-28 a red adoption check on main made pull
  request 863 a hand step.
- **Anything else red** — a failing check whose annotations name a file the
  change touches, or a check on a level head found to be the change's as the
  bullet above says — is handed back for repair. The harness withdraws the
  queued merge and records a failing check on the run, naming the forge check,
  the checked commit, its conclusion, annotations, and the captured job log
  where available. It keeps the branch, worktree and developer session, but
  clears the promotion and review credit. The docket says a repair is the way
  on and offers no re-arm of the unchanged red revision. A repair the
  development manager grants is carried out by `yoyo triage repair` on that
  same change and session, under the existing repair budget; fresh configured
  checks and independent review must pass before it is published again.
  This failure is recorded even if the forge already dropped the merge and
  the run cannot replay its head. On a level head, a check passing on the target
  or a job log naming changed files or their directories attributes the failure
  before replay eligibility is considered, even without annotations naming
  those files: replay limits or missing artifacts never
  authorize re-arming the red revision. If the developer session or preserved
  change cannot be recovered, the repair refusal and docket name a re-run
  decided by the development manager, through `yoyo triage rerun`, as the
  supported alternative. A change already promoted locally keeps that
  promotion as cleanup history with the failing check, without current
  promotion or review credit. When a repair continues its preserved change,
  the old promotion and failing check stay in the continuation's history;
  the repaired change must earn its own checks, review and cleanup evidence.
  An older stopped run that kept its failing forge checks only on the
  publication cannot supply this repair input: the repair refusal names a
  development-manager-decided re-run, carried out by `yoyo triage rerun`, as
  the supported alternative.
  Other red merges keep the dropped-merge recovery below: a head level with
  its target where nothing is wired to file the target's failure, or a head
  that fell behind and cannot be replayed because it was promoted onto the
  local target first or its worktree or branch is gone.

**The forge's account of a red check is carried onto the item.** Whenever the
sweep withdraws a queued merge over a failing check, or hands one back — to
bring its head up to date, to wait on the target's red check, or to a person —
the note it writes on the work item carries, for each failing check, what the
forge says of it, read under the harness's own forge access: the check's name
and how the forge ended it, the commit it ran on, the forge's own annotations
(file, line, level, and message), the failing step's lines from the forge's log
of the job — up to sixty, ending at the last error the log marks rather than at
the clean-up steps after it — and a link to that log. The item filed for a
check red on the target carries the same account. A developer run does not
reach the forge at all — its sandbox refuses `github.com` and `api.github.com` —
so this record is what a run given the item works from, and nothing on the item
asks it to fetch anything; until yoyodyne-ifd.429.35 the hand-back said the
forge's log would say why, and on 2026-09-28 the two logs behind pull requests
863 and 866 had to be read by a person. Where the forge will not let the
harness's token read a job's log, or run a job again, the item says so in the
forge's words and names granting that access as the operator's: reading a log
needs the token to read the repository's Actions, and a re-run needs it to
write them. A log the forge no longer holds, or a check no Actions job ran, is
said as that instead. The link and the annotations are kept on the run's record
beside the reading, as `url` and `annotations` on each failing check.

**Withdrawing a queued merge takes the request out of the merge queue too.**
Turning the request's auto-merge off is not enough on a target with a merge
queue: the queue consumes the auto-merge as it takes the request, so a request
already in the queue stays there with nothing armed, and the queue could land
the head the harness withdrew it to rewrite — the queue usually drops a request
whose checks go red itself, and "usually" is the hole. So the withdrawal turns
the auto-merge off first, so that nothing can put the request back, and then
asks the forge whether the queue holds it and, where it does, takes it out
(`dequeuePullRequest`). A forge with no merge queue answers that nothing is
queued, and there is nothing more to do. A withdrawal whose dequeue the forge
refuses, or that cannot learn whether the queue holds the request, is refused
rather than reported done: nothing is written, the merge stays recorded as
queued, and the next sweep asks again, so the harness never rewrites or hands
back a head the queue may still land.

A merge the forge has stopped holding is read the same way before it is handed
to anybody. Where the change landed through its pull request, the request is
still open, and its head is behind the target with no failing check naming a
file the change touches, the drop is the race a replay answers: the sweep puts
the run back at its promotion exactly as above — with nothing to withdraw — and
the change is brought up to date from the kept branch, checked, reviewed, and
queued again. Where its head is level with the target and failing only checks
that name no file the change touches, the drop is the target's red check once
the harness has confirmed it as above, and is filed and waited on exactly as
above, with nothing to withdraw; one found to be the change's is handed back. A drop is
handed back, to the development manager's docket, only when the change cannot
be replayed: a local promotion, a run whose branch, worktree, approval, or
sessions are gone, a request the forge closed, a head level with its target
failing some other way, or checks failing on the change itself. A reading of
the checks the forge could not give records the unread state and its error on
the publication and leaves the merge's disposition as it stands for the next
sweep.

A check that annotates no file says nothing by its annotations about whose
failure it is: a head behind its target failing only such checks is brought up
to date, and if it still fails once level with its target it is confirmed as
above — against the target's own head, or its log — and filed as the target's
failure and waited on, or handed back as the change's. A reading the forge could not give leaves the merge queued and records its
check state as unread, with the error and when the reading was attempted, on
the publication (`read_error` under `checks` in the run record). A check state
nobody read is not a red one, and an earlier reading is not reported as the
current state. The publication appears under `Waiting on the harness` on
`yoyo status`, saying `checks unread` with the error and naming the next
`yoyo reconcile` sweep as what reads the checks again. Nothing about a failed
read asks the operator to decide anything. A later successful reading replaces
the unread state; a merge nobody has attempted to read the checks of yet is
docketed saying exactly that rather than as approved and queued with nothing
beside it.

Three settle-path outcomes leave a publication outstanding, each
with its own line on the work item. A merge the forge **dropped**, where the
change cannot be replayed onto its target, is the
first: something the base branch required went unmet, the harness does not
merge past a requirement, and nothing about that publication is confirmed — so
the item is handed back to you with a blocker rather than closed as integrated,
which is also what puts it where a bounded re-arm of the dropped merge can be
decided, unless the harness withdrew it over the change's own failing check:
that is the repair handback above. A re-arm is once per publication, carried
out by `yoyo triage rearm`, after which a
further drop of the same publication is recorded as an escalation rather than
re-armed again. The moment the drop is found out is recorded on the run as well,
so it is announced in the item's thread as a `warning` rather than waiting for
the next person who runs a status command — and until the publication is
settled, it is counted in the [heartbeat](reporting.md) as a promotion awaiting
the forge. A
merge that **landed but could not be confirmed** is the second: the forge
performed it, and the steps that confirm it — verifying the remote carries the
promotion and recording the merge commit — failed,
so the record honestly says the publication is not settled even though the
merge is real. In those two, your local branch is deliberately left where it is
rather than moved on a publication nothing verified. A **merged branch that
could not be deleted** is the third, and is the mildest: the item is already
settled — closed, or back in the backlog, as its landing said — and your local
branch already caught up, and what is left is a branch on the forge. It says so
in a second line on the item naming the branch, followed by a settlement finding
with the remedy and who moves it. A remote branch at the recorded
published commit is removed as before. If the branch has moved, reconcile reads
the remote target and checks whether it contains the moved tip. A tip already
in that target is left alone and the publication settles with nothing
outstanding. A tip outside the target keeps the deletion refusal as a finding
on that item: preserve the extra work before removing the leftover branch, then
reconcile clears the outstanding publication. Neither case fails the rest of
the maintenance pass.

All three are on that docket, and all three hold their item out of the pull for
as long as they stand — which is the point: the change is already reviewed and
either on your local target branch (a target the forge does not protect, where
the local branch is the authoritative one) or on its kept branch and pull
request (a protected target, whose local branch was never moved), so an item
whose only outstanding state is a publication is not implementable work and a
run started against one can only rediscover that. What holds it says which of
the three it is, because they are not the same thing to act on: a confirmed
merge means nothing at all is left to do about the work, and a merge the forge
never made means the merge itself is still to be decided. Neither ever claims
the other, and where a run also left a branch behind, an unconfirmed merge reads
as that branch — the thing somebody can still act on — rather than as its
publication. A catch-up the
settle could not make is none of these: it is ordinary, the run settles, and the
convergence sweep below finishes it on the next pass. Other reports on this page
still reach you when the evidence demands it — a preserved blocker, a diverged
remote, a catch-up that could not finish — but none of them asks reconcile to
exercise judgement: it reports and leaves the decision where it belongs.
Settling never invokes a provider either: a lost process handle is not a
reason to start a second developer for an item. The provider invocations the
sweep makes are the last thing it does, and none of them starts a second
developer: a run
[paused on a usage limit](#waiting-out-a-provider-usage-limit) whose process
exited on the in-process bound is continued by the sweep itself once its
deadline has passed, in the run's own worktree and developer session, which is
the same run's own attempt reissued rather than a new one; and a queued merge
the settlement put back at its promotion to bring its head up to date is
carried through its replay, its checks, and its review by the same run.

**A publication nothing asked the forge to merge is decided, not merged by
hand.** A promoted, approved run whose record holds its pull request with no
merge queued, none dropped, and no account of anything going wrong is a merge
nobody made — every merge a run asks for leaves one of those marks, whichever
way the forge answers. Until yoyodyne-ifd.429.31 that state was named as the
operator's, and its only exit was a person merging the request on the forge.
Now it is put on the development manager's docket the moment it is recorded,
rather than after `triage.stuck_merge_age`, since nothing is waiting on the
forge and no amount of time changes it, and she decides it one of two ways:

- **A re-arm**, which the harness carries out as the merge request the run's
  own merge would have made — the same method, pinned to the promoted commit,
  after the same pre-merge check on the remote target, under the target
  branch's promotion lease — and records as a queued merge, which the next
  sweep settles like any other. It spends the publication's one re-arm, so a
  later drop of the same request is an escalation. It is refused, naming the
  gate and spending nothing, where the request's head is behind its target, a
  check on its head is failing, the forge's merge state names something only a
  person can supply, or the checks could not be read.
- **A re-run**, which hands the change back for a fresh run from the target
  branch. Once it is carried out, the prior run's record marks its publication
  handed back (`handed_back` on the pull request), and from then on nothing
  names it: it is not docketed again, on its age or otherwise, `yoyo status`'s
  fourth line drops it from under `Waiting on the development manager`, and the heartbeat stops counting it as awaiting the
  forge. The old request stays open on the forge until the fresh run lands,
  and nothing reads it as work meanwhile; once the fresh run has integrated,
  the re-run closes it with a comment naming the vehicle, as the convergence
  sweep below does for every superseded publication.

A request the forge has closed unmerged is docketed the same way, with nothing
left to arm: a re-run is the one decision offered for it, and the watch never
arms it.

Neither waits on anybody typing a verb. A watching `yoyo work` session carries
the re-arm out itself on its next pull — outside the developer slots, since it
is one merge request rather than a run, and not while the operator's pause or
the intake hold stands — and fires the re-run as it fires any other. A refused
arming is written onto the item's triage record naming the gate, and the
publication's docket entry comes back onto the development manager's docket
carrying it, ahead of the rest, even though her re-arm decision had settled that
entry. A temporary refusal is attempted again only once it has cooled, and a
later arming that goes through takes it back off. A permanent refusal is delivered
once as a new stoppage and is not retried until she changes the decision. `yoyo triage rearm <run-id>` makes
the request now rather than at the next pull, for a merge the forge dropped as
for one nothing ever asked for.

**A re-arm the harness cannot make is refused, not skipped.** A re-arm
decided about a run whose record cannot describe the merge — most often a run
that stopped before it promoted, such as one refused at a
[diverged target](#unwedging-a-target-branch-that-diverged-from-the-forge) —
is attempted at the next pull like any other. It is refused with the reason,
which names the re-run or an escalation as the decision that applies. This is
a permanent gate: no later pull retries it until she records a new decision.
A re-arm never brings a
head up to date: it makes the merge request the reviewer's verdict authorized,
on the head that verdict saw. A head behind its target, or in conflict with it,
is refused, and the fallback is a re-run. Until yoyodyne-edi the watch passed
this case over without a word, and it hid every re-arm refusal from the
docket. On 2026-09-28 that is how two re-arm decisions sat unexplained for a
day. [The account](diagnoses/yoyodyne-edi-rearms-never-carried-out.md) covers
both.

**None of the three stands forever.** Every sweep asks the remote again about
each publication the record says is merged and unfinished, and finishes the ones
the remote now confirms — the promoted commit on the remote target, unrewritten.
The merge commit recorded is the one the forge names for the request where it is
the merge of that promotion, or otherwise the one the sweep finds in the remote
history with the promoted commit as a parent; the forge's record never decides
the confirmation. Finishing is
the settle path's own work in the settle path's order: the merge commit recorded,
your local branch caught up, the item settled by its own landing where the drop
had handed it back, the consumed branch deleted, and the docket entry closed as
`settled` by the harness — so the hold, the heartbeat's count, and the
`Publication outstanding` line on the item all clear together, and the item gets
one note saying what was settled and which line it replaces. That is the lever
behind the sentence in [how work flows](work.md#letting-the-harness-choose-the-work)
that a hold lifts by the publication being settled, which until yoyodyne-ifd.357
had nothing behind it. A publication the remote still refuses stays exactly
where it was — the publication record keeps the account the run wrote, which
is the line on the item. Its settlement finding records what the remote refuses
now and the next move; the same finding is delivered to the item once, rather
than announced as new on every pass. The sweep still reports what remains on
each pass, and continues settling the other items. Once publication settlement
succeeds, its saved finding is revisited independently of publication selection:
failed finding delivery or a failed clearing save is retried under the run's
lease, without keeping the publication outstanding or delivering the note twice.
The eight held requests PR
497 merged on 2026-09-13 are
the case this was built on: confirmation then required the remote tip to carry
exactly the promotion's content, which only the last merge of a batch does, so
all eight settled as unconfirmed and stayed that way until this could re-ask.

Every other publication is re-asked about on the same sweep, before that. A run
that ended without its publication settled — one that failed before it
integrated anything, or one whose request the forge merged after the harness had
stopped watching — used to keep whatever the forge last said at the moment the
run ended, for good: a pull request somebody merged days later stayed recorded
open and unmerged, and the triage docket and the status surfaces read that rather
than the truth. Reconcile asks the forge about each of those and records the
answer — merged, closed, or still open. The refresh updates the publication
record and records a refused answer as a finding on that run and its work item.
What the sweep does close, one step later, is decided on the
harness's own promotion record and described below; the refresh is what makes
that record true first. A request that turns out to have merged outside the harness — a
dropped merge you made by hand on the forge — is recorded as merged here, and the
finishing above then confirms it on the remote and settles the item, so a hand
merge is settled by the sweep that finds it rather than staying handed back for
good. A record the forge agrees with is left exactly as it is, and a merged one
is never asked about again by this half — merged is the one answer a forge does
not take back. A record left alone for a reason, such as a branch the forge
answers about with some other request, is reported and is not a failure. An
unreachable forge or an unreadable answer leaves a finding on the affected item,
naming the refusal, the remedy, and who moves it. Reconcile continues settling
the other items, and that finding alone does not fail the maintenance pass. The
next sweep retries the unanswered request without adding another note for the
same refusal; undelivered finding notes are retried too. Once the answer is
recorded, the saved finding is delivered and cleared under the run's lease even
if the publication no longer needs refreshing.

**Which publications a pass asks about.** Only the unsettled ones. A request
the record already holds as merged, as closed, as superseded by the
convergence sweep below, or as handed back for a fresh run by the development
manager's re-run is never asked about again: none of those answers can change
what the harness does with it. That leaves the requests still recorded open
with nothing settled about them, the merged publications still unfinished, the
promotions whose record names no request, and the queued merges and
interrupted landings the run settlement owes an answer. The first three are
asked in batches — one forge query covers up to 50 branches, each read exactly
as asking about that branch alone would read it, merge queue included — and a
batch the forge does not answer leaves every record in it as it stands for the
next sweep. The runs whose settlement asks the forge nothing — a run whose
process died, one a redeploy drain preserved, one stopped on time — are settled
before any queued merge or interrupted landing is asked about, so a slow forge
never holds the developer slots those runs keep. The pass's last line says how
many publications it asked about, in how many requests, and how long the forge
took — `asked the forge about 12 unsettled publication(s) in 3 request(s),
which took 4.2s` — and `--json` carries the same under `forge`, with the time
in nanoseconds as `took_ns`; the
[maintenance pass](#the-supervisors-maintenance-pass) records the end of what
the sweep prints, so its record carries the line too. Until yoyodyne-ifd.429.38
every recorded request that was not merged was asked about on every pass, one
listing and one merge-queue question at a time: on 2026-09-29, over 798
recorded publications, one pass ran past an hour while the two runs a redeploy
drain had preserved held both developer slots with 49 items ready.

Before either of those, the sweep looks for the one publication neither can see:
**a promoted run whose record names no pull request at all.** Everything above
starts from the request on the record — the docket keys a publication entry to
it, the heartbeat counts what awaits the forge from it, the re-arm repeats it —
so a publishing run that promoted a change and recorded no request would be a
change the forge holds that no surface reports. Three things close that. The run
itself refuses to be that record: a publishing run that reaches its promotion
with no request on its record writes a `Publication outstanding` line naming the
branch and saying nothing was asked of the forge, rather than finishing quietly,
and a run whose summary names a request its durable record does not hold — or
holds in a different arming state — is refused completion outright and recorded
as failed. That line is what the docket and the status line then read, before
any sweep has asked the forge: the promotion is docketed as a publication keyed
to the run alone, since there is no number, naming the branch and carrying the
account, and it is counted as awaiting the forge with the harness named as the
mover — so a forge that turns out to hold no request for the branch leaves a
promotion every surface still names, not one only a sweep's stderr does. And the
sweep asks the forge by the run's branch, which is the one durable handle it has
left, writes the request the forge holds onto the record — number, state, and
whether a merge is queued for it — and then makes the merge request the run
itself never made. That is the run's own merge made late, on the run's own
evidence and through the run's own gate: the record has to carry the promotion
and the approving verdict, and the verdict is read off the record by the sweep
before it asks rather than inferred from the promotion beside it; the request's
head has to be the promoted commit; the remote target has to pass the same
pre-merge check the run's merge makes; and the request is pinned to that commit,
made by the same method, under the target branch's promotion lease.
The forge's answer is recorded as a queued merge on either answer, exactly as a
re-arm records one — and the record is written only with that answer, so a
sweep interrupted between finding the request and arming it leaves the record
as the run wrote it for the next sweep to ask again, rather than a request
beside a line saying none is held — and the next sweep settles the run on what
the forge does with it — confirms the merge, records the merge commit, catches your local
branch up, deletes the consumed branch, and closes the docket entry the
promotion had open. A request the forge has already merged, or already holds a
merge for, needs no arming: something has asked the forge, so the account of
the loss is replaced in the same write that records the request — with nothing,
for a merge the forge holds, since settling it writes what became of it; and for
a merge the forge has performed, with the line every unconfirmed merge carries,
so the finishing above confirms it on the remote and records the merge commit
exactly as it finishes a merge the run itself could not confirm; a request
whose head has moved, a remote target that no longer passes the check, or a
merge the forge refuses is recorded as the dropped merge it is, which puts it on
the docket for triage and holds the item exactly as a drop the run itself met.
The sweep never repeats a merge the forge dropped: that is still `yoyo triage
rearm`, a decision, once. A forge that holds no request for the branch leaves the
record as the run wrote it, and the sweep says so on every pass it stands. What
the sweep, the docket, and the status line select on is that account and only
that account — a promoted run that recorded it, with the approving verdict
beside it — and not the bare shape of a promotion with no request on its
record: the record carries nothing else that tells a local promotion from a
publishing one, and every promotion of that bare shape the store held when this
was built was a local one from before publishing existed. A record that lost its
request by some path that wrote neither the account nor went through the run's
own completion is therefore not recovered by this, and is not claimed to be.
docs/diagnoses/yoyodyne-ifd-402-publication-record-not-lost.md is the account
of the two runs this was built on, neither of which turned out to have lost
anything.

The same sweep recovers the [exchanges the roles have put to each
other](conversation.md#roles-asking-each-other-things), for the reason it settles
the runs: a process died holding something, and this is what finds out. Each
exchange is taken under its own lease, so one a live process is carrying is left
to that process — the lease is also what says the carrier is gone, since the
operating system drops it when a process exits. A round a dead process asked and
never got an answer to is closed saying which process was carrying it, with the
round still spent and the thread still open; a thread that spent every round it
was given and was never asked again is closed as unresolved and reported to you
at warning severity, which is the ending the round cap would otherwise never
reach on a thread nobody came back to. Nothing is put in front of a role by this:
recovering from a lost process is never a reason to start a round nobody asked
for, so a sweep that finds a thread simply waiting its turn leaves it waiting.

Those two endings are the whole of what it prints, because they are the whole of
what it changed. Everything else it comes back with is a description of what it
found rather than something it did — a thread another process is carrying, one
waiting its turn, one the [in-flight bound](conversation.md#roles-asking-each-other-things)
would hold back, one whose references have moved — and those are in `--json`
under `supervision`, as the branches and publications it leaves unprinted are.
That division is worth knowing before you go looking: a product with several open
threads has a line about each of them on every sweep, and printing those would
bury the one or two that say a record was changed.

Once the runs are settled it converges local state, which is the rest of the
post-merge hygiene you would otherwise do by hand. Every target branch the
harness knows about is caught up onto its remote counterpart — the same
fast-forward the settle paths make, for a target left behind by something no run
is going to finish, or a catch-up that was held at the time — and every settled
run's leftover branch whose work the target already carries is deleted. Both
refuse on evidence rather than on a record: a remote that has diverged from
your local branch is reported for you to decide rather than reconciled — the
steps for deciding it are
[here](#unwedging-a-target-branch-that-diverged-from-the-forge) — a
branch carrying work nothing promoted is
kept, and a branch a checkout still holds is left alone. Catching a branch up
takes that branch's promotion lease, so it never races a run promoting into it.
A deletion is written onto the run it belonged to, under that run's own lease, as
a retired checkout is: `yoyo status` and the triage docket read the run's record
for whether its change survived, so a branch deleted with nothing written down
leaves the run advertising one that is not there.

Between the catch-up and the checkouts it closes the pull requests whose work
landed by another vehicle. A run branch carries the run that published it, so an
item attempted again — after a killed process, as the loser of a duplicate
selection, or as a re-run triage decided — publishes a new branch and opens a new
request, and nothing revisited the first one: it sat open with a green build and
no queued merge, indistinguishable from pending work until somebody asked why.
Thirty of them had accumulated on this project's forge by 2026-09-07. The sweep
pairs each run that ended without integrating with the latest run of the same
item that did integrate, and where that landing came after the dead run began,
closes the dead run's pull request with a comment naming the vehicle — the
superseding pull request where the forge merged one, otherwise the commit that
reached the target branch — deletes the remote branch it published, and records
the supersession on the run's own `pull_request` so no later sweep asks the forge
about it again:

```
pull request #109 of yoyodyne-ifd.113 closed: pull request #111, which the forge merged for run run-813384f1… superseded it
```

It refuses on evidence here too. A run that integrated something of its own is
left alone, because its publication is outstanding rather than superseded and
the [triage docket](configuration.md#triage-thresholds) is where that goes —
except one whose publication the development manager handed back for a fresh
run, which is superseded once that fresh run lands and is closed in its name,
while its own promotion is never counted as a landing of the item; a request the forge
reports merged is never touched; a request opened after the landing is pending
work rather than an orphan; and a request somebody already closed collects no
second comment — and settles all the same, its remote branch read as already
deleted where a hand-closer took it too. What it never does is guess: a request
whose item no run of this harness ever landed is not closed on the strength of a
merge it cannot see. Those are what is left open, and they are named rather than
left in silence, on one line:

```
3 open pull request(s) belong to runs that ended without landing, and no later run of their item has landed to supersede them; each is a person's to merge or close: #125 (yoyodyne-ifd.121), #244 (yoyodyne-ifd.158), #342 (yoyodyne-ifd.241)
```

Each of those is one of two things, and the two call for opposite actions:
work still pending on the preserved branch, waiting on a repair or a re-run, or
work that landed by a vehicle the harness did not record — a hand merge, or
another item's change that made this one moot. `yoyo reconcile --json` carries
the reason for each under `convergence.unsuperseded`. Once you have decided one,
closing or merging it at the forge is all it takes: the next sweep's refresh
records the answer and the request leaves the list. A request a run left open
because your integration policy has a person approve the merge is not on it —
that is the run's deliverable, open because you said so, and a project under
`integration: human` would otherwise be shown every pull request it has.

What the dead run kept locally is not this step's. Its checkout is retired by the
checkout sweep below on the same terms as every other settled run's, and its
local branch is judged by the branch sweep above — **a branch carrying work
nothing promoted is still kept**, and once the published copy has gone it is the
only copy of that work left, so deleting one is your decision rather than the
sweep's. A re-run closes the same request itself at the moment its fresh run
integrates, so the forge's open list stays honest between sweeps.

The same sweep retires the leftover checkouts, which is what makes the worktree
registrations a machine carries live runs, outstanding recovery, and a bounded tail rather than
something that grows with the harness's history. That growth is not cosmetic: an
agent's sandbox profile denies every registered worktree path on every command it
spawns, so a machine that keeps them all eventually cannot spawn a command in its
next worktree at all — no `make check`, no `go test`, nothing. Settled runs past
the most recent few have their checkout unregistered unless a recovery decision
still needs it. A standing repair or re-run keeps the checkout and branch,
including when a gate refused to carry the decision out; a stopped integration
keeps them too. An outstanding automatic continuation at checks or after a silent
provider stall also keeps them, including while intake or capacity delays it.
A run retired because another run confirmed its item's merge keeps its branch
and checkout too, regardless of the cleanup tail. A recovery record that cannot
be read keeps the artifacts rather than granting
retirement. Registrations whose
checkout is no longer on disk are pruned, whichever run or person left them
behind. A registration a killed `git worktree add` never finished filling in is
cleared on the same pass, and named — see
[a registration a run never finished writing](#a-registration-a-run-never-finished-writing)
for what that shape is and why nothing else clears it. A run still in flight is
never a candidate — that is a live developer's checkout. Neither is a run whose
publication records a merge the forge still holds, for its checkout or for its
branch, however far past the tail it is: until the forge's merge is confirmed
or dropped, the kept branch and worktree are what a head that falls behind is
replayed from, and retiring them would turn that replay into a drop handed to
a person. They are removed by the settlement that confirms the merge, as
[above](#recovering-interrupted-runs), and by nothing earlier —
`TestConvergeNeverRetiresTheCheckoutOrBranchOfAnUnsettledQueuedMerge` in
`internal/orchestrator` holds the sweep to that. Each retirement is taken under the run's own lease and written onto its
record, and so is a checkout the sweep finds already gone — removed by you, or by
an external `git worktree prune` — so `yoyo status` and the triage docket stop
advertising a directory that is not there rather than sending you after it.
If delivery of a preservation or finding note fails after retirement, later
passes retry the note under the run's lease even though the checkout is gone.
The item is checked for a note already delivered before appending it again,
and the finding clears once those delivery obligations are settled.
Because the sweep is part of `yoyo reconcile`, this is owned and recurring rather
than something anybody has to remember.

Nothing is lost by it, including the case that made this worth doing carefully.
Most preserved checkouts belong to runs that stopped without promoting anything,
which is the population most likely to have a half-finished change sitting in the
working tree — and that change is the one thing no branch, commit, or record
holds a copy of. So the sweep moves it rather than declining to act: the tree is
recorded on `refs/yoyodyne/preserved-work/<run-id>` and proven to be there, and
only then does the directory go.

```
/…/worktrees/yoyodyne-ifd-140-a1b2c3d4 retired: run run-4f2a…9c1b is settled
  uncommitted work preserved at refs/yoyodyne/preserved-work/run-4f2a…9c1b
```

That ref is on the run's own record too, and the sweep writes it onto the work
item as well, naming the checkout it retired and the command that opens the ref.
The item is where somebody picking the work up actually reads, and what it
already carries is that run's own failure note — written while the checkout was
still there and naming it. Without the correction, that note goes on pointing at
a directory that is gone: run-48216ea9's 23 files were reported destroyed on
exactly that gap, while the ref holding them was two commands away. A note the
tracker refuses is reported on the sweep rather than failing it, because the
work is on the ref either way.

Open it as a checkout again with `git worktree add --detach <path> <ref>`, or
read it with `git show` and `git diff`. It is deliberately not a branch: a branch
would be swept by the branch sweep above, listed by `git branch`, and answer the
containment proofs the harness makes about run branches. A capture that cannot be
written leaves the checkout exactly where it was, reported as kept with the
reason — as are the other things the sweep will not touch, a directory Git is not
managing and a registration on a branch its run never recorded. Those are
anomalies rather than a category: a `yoyo reconcile` printing one is telling you
about something that should not be there.

A recorded repair can restore a missing checkout from a surviving branch at the
exact commit the harness recorded. It continues the same run and developer
session, with consumed budgets retained and check approval cleared before the
checkout is restored. A conflicting path, a missing or changed branch, or an
unfinished developer attempt refuses restoration and leaves the decision
standing. Captured uncommitted work remains on its recorded ref; checking out
the branch does not recover it. The development manager decides what follows
such a refusal.

The restoration writer holds the directories open while creating files and
registration data; replacing a root with a symlink cannot redirect its writes.
Existing index and export files are replaced with new files, preserving the
contents of files hard-linked elsewhere.
Restoration refuses checkout filters selected by the recorded tree's attributes
because committed objects alone cannot prove those filters' output was recovered;
unused filter definitions do not refuse restoration.

The last reading the sweep takes — after every settlement above and before the
runs it continues, below — is whether anything is happening at all. When
no developer run has started or ended for `--stall-after` — ten minutes by
default — the tracker reports work ready, and no hold, no still-moving run and no provider usage window
accounts for it, that is recorded against the product as a stall and said here:

```
nothing has started on this product for 2h14m0s, with 3 item(s) ready
  the session choosing work last recorded watching at 2026-09-01T06:05:00Z, and has said nothing since
```

The second line is the one to act on: a session whose last word was `stopped`
wants starting, and one still claiming to be `watching` wants killing first. A
machine that is behaving says nothing here at all. Where the record goes and why
this is the sweep that writes it is
[when nothing happened at all](#when-nothing-happened-at-all); what it costs is
one tracker read per sweep, and only on a sweep where nothing else already
accounts for the quiet.

Repeating the whole thing is safe — a settled run is no longer outstanding, a
branch already level with the remote has nothing to catch up to, cleanup over
artifacts that are already gone does nothing, and a stall already standing is not
recorded twice. A run another process still holds
is left to that process, and a run `yoyo run` can continue on its own — one
inside its repair loop, one paused for a provider usage limit whose deadline
has not passed, one parked on an
[operator pause](#pausing-everything-and-resuming-it) that still stands, and,
one paused for work its item depends on, for as long as that work is open,
one a watch session
[stopped for its own redeploy](#a-session-draining-to-restart-into-a-deployed-build),
and, for the half hour below, one whose provider the harness stopped on time,
one paused for an [unresolved
directive](conversation.md#directives-and-the-work-they-pause), one parked on a
tracker that would not answer, one waiting out a provider nobody could reach,
and one parked on an operator pause since lifted — is left exactly as it is for
that command to pick up. A run paused for a usage limit whose deadline has
passed with no process serving the wait is the one the sweep
[continues itself](#waiting-out-a-provider-usage-limit), as the last thing it
does, and a run paused for work its item depends on is continued by a watching
`yoyo work` session at the first pull after that work closes
([how work flows](work.md#how-work-flows-once-you-approve-it)).

**A run whose process vanished is settled here, and nobody edits its record by
hand.** A run whose provider [the harness stopped on time](#when-a-provider-stalls-or-runs-out-of-budget)
is left in flight to be continued, and so is a run parked on anything else its
process returns from and exits over — an unresolved directive, a tracker that
would not answer, a provider nobody could reach, an operator pause since
lifted. Nothing continues any of those on its own; a run
nobody typed `yoyo run` for therefore goes on reading as running with no live
process behind it and no ending ever recorded. The sweep settles one of those,
whatever it was parked on, once thirty minutes have passed since its record last
moved with nothing continuing it — measured, for a provider nobody could reach,
from the probe it recorded — and the lease it takes to settle a run is what says
no process holds it, since a continuation somebody did start would be holding
that lease. It ends the run and records the cause as outside the work, rather than
as a verdict on anything. The run's record
and the work item both carry what the sweep observed and nothing more: that no
live process held the run, that no ending was recorded, when the record last
moved, and what the run was parked on. Three parks are left out, because each
has something else that ends it: a usage limit or an overloaded provider, which
the sweep's own last step continues once its deadline passes; work its item
depends on, which a watching `yoyo work` session continues at the first pull
after that work closes, however long it stays open; and the operator's pause
while it still stands. Until the sweep settled every park nothing continues (yoyodyne-ifd.428.49), only a provider stop was
settled here, and on 2026-09-26 a run parked on a dependency held developer
slot 1 for twenty hours while every sweep reported it resumable
([the diagnosis](diagnoses/yoyodyne-ifd-428-49-dead-run-held-its-slot.md)).
A dependency park was settled here too from then until the watch came to
continue it (yoyodyne-ifd.428.51); a stop recorded on such a run is still
honoured here at once. The branch and worktree are left
exactly as a stopped run's are, the item is blocked with that account, and the
stoppage goes on the triage docket, so a repair-continue the development
manager decides about it carries out as it does for any stopped run — on the
change the run already has, in its own worktree and developer session. Both
shapes of stoppage are carried out: a run stopped inside its repair loop, with a
failing check or the reviewer's findings handed back to it, is continued on that
failure; one stopped in its first attempt, with nothing handed back, is
continued at the attempt it was stopped in, and counts no review round and no
repair attempt because a stall judges nothing; one stopped at its review or its
checks after the attempt finished is continued at that step, with no developer
invoked — the entry says which, and
[what a stall is owed](#when-a-provider-stalls-or-runs-out-of-budget) is the
whole of it. A first stall of a provider stream that went silent, including
during a repair already underway, is the exception to waiting on her: the
harness continues it
itself at a watching session's next pull, once per run, and only a second
stall is hers — [the same section](#when-a-provider-stalls-or-runs-out-of-budget)
says how. The
slot the run was holding and the in-flight guard's hold over the items beside it
release with the record going terminal. `yoyo status <item>` reads the run as
`stopped` with that reason under it. Whether the round it ends spent anything
is decided as every round ended from outside the work is: one that left a change behind spent
what it spent, and one that left nothing gives back the repair grant that bought
it. What you never do is open a run's JSON and change `status` yourself: a
record edited by hand carries no account of who ended the run or why, the
docket and every status surface are derived from the record rather than from
the edit, and the sweep already writes the whole of it on the next
`yoyo reconcile`.

**A stop asked for on a run with no process behind it is honoured by the sweep,
at once.** A stop — yours, or one the development manager decided — is read by
the run's own process at its next provider-call boundary, and a run whose
process is gone reaches no boundary. So the sweep reads it too, before anything
else it decides about an in-flight run: a run whose lease it can take and that
somebody asked to stop is ended there and then, without waiting out the half
hour, exactly as the run would have ended itself — `cancelled`, its branch and
worktree left where they are, the stop and who asked for it on the item, and, for
a stop she decided, the stoppage docketed as settled by her decision. The slot
is free as the record goes terminal. `yoyo reconcile` says `cancelled` against
the run, with the stop's own words and that the sweep ended it in the dead
process's place.

## Git maintenance, and the one prune that is still yours

Git prunes worktree registrations as part of its automatic maintenance, and it
judges one stale by whether its administrative files are there — which is
exactly what a `git worktree add` has not written yet while it is still filling
the entry in. A prune reaching that window deletes the registration out from
under the add, the add fails with

```text
fatal: could not open '.git/worktrees/yoyodyne-ifd-334-0db8dc56/locked' for writing
```

and the run is lost to nothing but timing. Every worktree the harness cuts
shares the repository's common Git directory, so the prune does not have to
start anywhere near the run it takes down.

The harness holds this off in the two places it can. It never asks for
maintenance in the Git commands it composes itself, and every process it
launches — the agent, each configured check, and anything those go on to
start — carries `gc.auto=0` and `maintenance.auto=false` in its environment. So
a Git command an agent runs, or one a project's own build tooling runs inside a
worktree, cannot start a maintenance run either, without either of them having
to know that.

Nothing is written into your repository's config for this. The repository is
yours, its maintenance is yours to configure, and object GC turned off for good
in a repository that keeps growing is a cost the harness would be imposing on
your machine rather than on a run. The fence lasts exactly as long as the
process it was given to.

What that leaves is a Git command nobody here launched: `git gc` or
`git maintenance run` typed in the checkout, or a tool you started yourself.
That one is yours. Run it when nothing is in flight — `yoyo status` says what is
running — and a run cannot be caught mid-creation by it.

If runs are still being lost this way, the full fence is available and is one
command in the managed repository:

```sh
git config maintenance.auto false
git config gc.auto 0
```

That closes the residual for every command in the repository, at the price of
packing and pruning objects becoming something you run by hand.

## A registration a run never finished writing

`git worktree add` registers an entry under the common Git directory's
`worktrees/` and then fills it in, one file at a time: a `locked` file saying
`initializing` first, then `gitdir`, `commondir` and `HEAD`, then the checkout
that writes the `index`, and last of all it removes the lock. Anything that
walks the bookkeeping in between reads a file that has been created and not yet
written, and Git refuses the whole command rather than skipping the one entry:

```text
fatal: failed to read .git/worktrees/yoyodyne-ifd-334-0db8dc56/commondir: Result too large
```

The listing is the obvious walker — every inspection, cleanup and sweep the
harness makes is one — but it is not the only one. A rebase, a checkout, a
branch deletion and `git worktree add` itself each walk the registrations to
check that a branch is not checked out somewhere else, and each dies over the
same entry with the same words.

**Within one harness, such a command does not cross that instant at all.** Every
Git command the harness runs that walks the registrations takes the same lease
its own creations take, in a shared mode every other reader may hold at once and
no creation may hold beside — so a rebase, a checkout or a listing waits for a
creation in flight instead of meeting the entry it has not filled in yet, and
readers never queue behind each other. A creation reads what it is writing under
the lease it already holds, so it never waits for itself. Commands that open no
registrations — the great majority, every `diff`, `status`, `log` and `rev-parse`
— take nothing and are not held back by a creation.

What the lease cannot cover, the harness runs *any* Git command again over: a
second harness on an older binary, a Git command somebody ran by hand, and a
platform with no advisory lock to take at all. The instant passes in the time
Git takes to write a handful of small files, so running the command again is
usually enough. The same instant has a second face at the other end, when the
add finishes: Git sees the entry's `locked` file, the add removes it, and Git
dies reading it — `failed to read '.git/worktrees/<entry>/locked': No such file
or directory` — so that is run again too. A removal crosses a walk the same
way, from the other side, and Git has two words for that. The entry itself can
go between Git reading it and resolving the repository through it — `Invalid
path '.git/worktrees/<entry>': No such file or directory` — and when the
removal takes the last entry, Git deletes `worktrees/` with it, so an add that
had just made the directory finds nowhere to make its own entry: `could not
create directory of '.git/worktrees/<entry>': No such file or directory`. Both
pass once the removal beside them returns, and the add has made nothing when it
dies of either, so both are run again. Those four refusals are the only ones
run again, within the same three attempts: every other answer Git gives is
believed the first time.

A command run again that way leaves nothing behind once it succeeds, so where a
watch session started the run, each re-run is written on the watch log as it is
taken: the item the run is for, the Git command (`git worktree add`, `git
rebase`, …), which attempt Git refused out of how many, and Git's own words.
It is a note on the log rather than a change in what the session is doing, so
`yoyo status` and the channel read past it; it is what tells a repository whose
runs cross each other more and more from one where they never do.

What neither can cover is the entry that stays that way. An add whose
process was killed leaves the entry exactly as it stood, and nothing Git has
clears it: `git worktree prune` skips an entry that is locked and judges an
unlocked one by its `gitdir` file, which such an entry usually has, and
`git worktree remove` finds its entry through the listing the entry breaks.
Where the file it was killed writing was `commondir`, every walk on the
repository then fails, creation included — which is how one killed add used to
stop every later run on the repository until a person removed a directory by
hand.

**The harness clears such an entry itself now, and says so.** Two things do it:
every worktree creation, just before its own `git worktree add`, and the
convergence sweep [`yoyo reconcile`](#recovering-interrupted-runs) runs. Both
hold the registry lease the harness's own creations take, so under it an
unfinished entry cannot be a creation of the harness's own still writing; and
both leave alone an entry younger than a minute, because an add somebody else
started — a person's, or one an agent ran inside a checkout — takes no lease and
is told from a dead one only by having stopped writing. A creation waits such an
entry out rather than failing over it, and the minute is the bound on that wait
rather than its length: what it is really waiting for is the other add getting
past the one file a walk dies on, which takes milliseconds, so a creation that
meets a live neighbour is held up for about as long as that neighbour takes to
register. Only an add that has genuinely stopped costs the whole
minute, once, and is then cleared. What is cleared is the
registration alone: the branch the add made first is a branch like any other,
and a directory it left on disk is left where it is, named in the line so you
know it is not a worktree any more.

```text
cleared the worktree registration yoyodyne-ifd-334-0db8dc56, left by a git worktree add that never finished: its lock still says initializing, which the add removes only once it has checked the worktree out; the directory it named at /…/worktrees/yoyodyne-ifd-334-0db8dc56 is not a worktree any more, and is left where it is
```

A creation says it on standard error; the sweep prints it and carries it in
`--json` under `convergence.registrations.unfinished`, with an entry it kept and
why beside any it cleared. An entry is judged a killed add by two things
together: it has no `index`, because the checkout is what writes one, and it
either still carries Git's own `initializing` lock or is missing one of the
files the add writes before the checkout. A worktree added with `--no-checkout`
has no index either and is neither of those, so it is never touched.

A listing that meets an unfinished entry before it has been cleared — inside
the minute, or one Git refused over that was never a killed add — still
describes the repository without it rather than failing, saying so on standard
error:

```text
the worktree listing left out yoyodyne-ifd-334-0db8dc56, registered and not yet filled in, which Git refused the whole listing over: list worktrees failed with exit code 128: fatal: failed to read .git/worktrees/yoyodyne-ifd-334-0db8dc56/commondir: Result too large
```

That line means runs are not being lost to the entry, not that it has gone. If
it keeps appearing across sweeps, the entry is the one shape nothing clears: a
registration that *does* have an index — a finished worktree whose `commondir`
was emptied by something other than a killed add, a crash that zeroed the file,
say — and that one is yours to look at, because the checkout it names may hold
somebody's work. `yoyo status` says what is running; when nothing is:

```sh
rm -r .git/worktrees/yoyodyne-ifd-334-0db8dc56
git worktree list --porcelain   # describes the repository again
```

## Unwedging a target branch that diverged from the forge

Every catch-up and every promotion here is fast-forward-or-nothing, so a local
target branch and the remote's having both moved is the one repository state the
harness will not decide. You see it as the same line on every sweep:

```
main not caught up: main on origin is at 9f1c2ab, which does not contain the local main at 4d7e805; only a person can say which history is right
```

and the run that reached integration for that target stops with both branch
positions named rather than promoting into it. That refusal is deliberate — the
alternative is a promotion nobody can publish and an item settled as integrated
against it — but it does mean the branch does no more work until you say which
history is right. Nothing sweeps it away in the meantime, and no later
`yoyo reconcile` resolves it.

**The line holds itself while the wedge stands.** The refused promotion records
the divergence against the product — the branch, both positions, the catch-up's
own words, and the run it stopped — and a watching `yoyo work` session reads that
record at every pull and chooses nothing while it stands; a drain stops on it.
Before `yoyodyne-ifd.428.26` the line went on pulling, and every item it pulled
spent a whole development and review before stopping on the same divergence. It
is neither [the brake](#pausing-everything-and-resuming-it) nor an intake hold
somebody placed: it counts nothing, nobody placed it, and `yoyo release` does not
lift it. `yoyo status` names it on its "Needs a human" line as yours, with this
section as what settles it:

```text
Needs a human (1):
  the target branch main will not catch up to the remote's, so the harness chooses no work for it: main on origin is at 9f1c2ab, which does not contain the local main at 4d7e805; only a person can say which history is right; 1 promotion refused since 2026-09-21T08:00:00Z — the operator's — follow "Unwedging a target branch that diverged from the forge" in docs/operations.md, and the next `yoyo reconcile` that finds the branches converged lifts it; nothing needs releasing
```

Every ready item on the "Not startable" line is refused for it in the same words,
the channel repeats it every `--heartbeat` at `warning`, tagged to the operators,
and `yoyo status --json` carries the record under `standing.diverged_targets`.
**What lifts it is the branches converging**, and nothing else: the next
`yoyo reconcile` whose catch-up finds the local target level with the remote's,
or brought onto it, removes the record and says so, and the watching session
chooses again at its next poll with nothing to release. The record lives at
`projects/<product>/state/diverged-targets.json` under the state root. The target is
the branch the primary checkout is on, which is what every run promotes into, so
while one stands the session chooses nothing at all.

Runs that predate the fix in `yoyodyne-ifd.177` could produce this by losing a
cross-machine race after promoting, and a repository still standing in that state
is what this section is for. A run today cannot produce it that way: it settles
where the remote target stands before promoting, and stops without closing
anything if the remote moves afterwards. Runs before `yoyodyne-ifd.429.5` could
also produce it on a target the forge protects, by promoting onto the local
branch and then having the forge refuse or hold the merge: the local branch was
left ahead of the remote with the run's commits, which is how the product stalled
on 2026-09-20 and again on 2026-09-24. A run today lands a protected target
through its pull request and moves the local branch only by a fast-forward onto
the remote ([configuration](configuration.md#a-protected-target-lands-through-its-pull-request)).
Reaching this state now takes somebody pushing to the target directly. The
recovery is the same either way, and it is yours to run. (A queued merge landing among others used to reach this page too — as a
publication reported unconfirmable for good rather than as a wedge — until
confirmation asked whether the remote contains the promotion rather than whether
its tip carries exactly the promotion's content.)

**Which side is which.** The remote is the shared truth: the forge has it, and so
does every other checkout of the project. The commits your local branch has that
the remote does not are promotions this repository made and never published —
reviewed and integrated here, and nowhere else. Keeping the remote's history and
preserving those commits on a branch of their own is the only resolution that
discards nothing, and it is the one below. Do not resolve it the other way by
force-pushing your local branch over the remote: that throws away whatever the
remote gained, which is by definition work this repository has never seen.

1. **Stop the harness spending, and check nothing is mid-promotion.**

   ```sh
   ./bin/yoyo pause
   ./bin/yoyo status
   ```

   `pause` keeps new attempts from starting. `status` is what tells you no run is
   in the `integrating` phase: a promotion already under way holds that target's
   promotion lease, and moving the branch underneath it is exactly the race the
   lease exists to prevent. Wait for anything integrating to finish.

2. **See what each side has that the other does not**, so you are deciding about
   named commits rather than two hashes:

   ```sh
   git -C <repository> fetch origin main
   git -C <repository> log --oneline origin/main..main   # promotions the remote never received
   git -C <repository> log --oneline main..origin/main   # what the remote gained meanwhile
   ```

3. **Preserve the local-only commits on their own branch**, so nothing you are
   about to move away from becomes unreachable. Naming it after the commit makes
   the step safe to repeat:

   ```sh
   git -C <repository> branch diverged/main-$(git -C <repository> rev-parse --short main) main
   ```

4. **Put the target back onto the shared truth.** When the primary checkout is not
   on the branch, move the ref as a compare-and-swap on the commit you read in
   step 2, so a branch that moved since loses the race rather than being
   overwritten:

   ```sh
   git -C <repository> update-ref refs/heads/main <remote-commit> <local-commit>
   ```

   When the checkout is on the branch, confirm there is nothing uncommitted first,
   because the move discards changes to tracked files:

   ```sh
   git -C <repository> status --porcelain    # empty, or only your declared exports
   git -C <repository> reset --hard origin/main
   ```

5. **Let the harness go again, and confirm the wedge is gone.**

   ```sh
   ./bin/yoyo resume
   ./bin/yoyo reconcile
   ```

   The held catch-up should be absent from the sweep, and in its place the sweep
   says the divergence recorded on the branch is lifted — `main has converged
   with the remote's, and the divergence recorded on it since … is lifted` — so a
   watching session chooses work again at its next poll and runs for that target
   promote again. That is the state this recovery is for: resolvable, and back
   under the harness.

6. **Resume the approved changes the divergence stopped.** Each one is recorded
   as an integration stop of cause `diverged-target`, and its docket entry and
   blocker name the command:

   ```sh
   ./bin/yoyo triage resume <run-id>
   ```

   It carries the change on to its promotion with its approval standing and
   spends no review round, repair grant, or re-run. Asked before the branches
   are settled, it refuses, writes nothing, and names what is still diverged.

7. **Decide what happens to the preserved branch.** Its commits carry work a
   reviewer approved and this repository integrated, which the shared remote never
   received; the work items behind them carry a `Publication outstanding` line
   naming the pull request that was never merged. Open a pull request from the
   branch yourself, or file work to redo it, and delete the branch once you have.
   Nothing sweeps it for you: it is preserved work, and the convergence sweep only
   ever removes a branch whose work the target provably carries.

## Where the harness stands: the four lines

`yoyo status` opens with four lines, and prints all four every time:

```text
Running (2 developer runs):
  yoyodyne-ifd.194 (The four-line status: running, working, not-startable-with-reasons, needs-a-human) — developing, on claude-opus-5 at medium effort, 12m elapsed, $3.41 so far
  yoyodyne-ifd.201 (The invariant loader skips the directory README, as everything else already documents) — reviewing, 3m elapsed, cost unknown (its event log is gone)
Working (1 conversation):
  product-manager — product-manager, on claude-opus-5 at medium effort, a turn in flight for 40s after 270 recorded turns
Not startable (4 of 7 admitted items; 1 awaits the development manager's decision, 1 awaits the harness carrying out a decision already recorded):
  - 1 ready, waiting for a developer slot; 2 slots, all taken — not counted as not startable: the harness starts the next one as a run in flight finishes, and nothing is asked of anybody
  - 1 waits on the development manager's decision about a stopped run — next: she decides what becomes of each stopped run: a repair, a re-run, a wait, a re-scope, or an escalation; whose: the development manager's
  - 1 waits on the harness carrying out a decision already recorded — next: the harness acts on the recorded decision — a repair, a re-run, or a re-armed merge — at its next pull; whose: the harness's
  - 1 waits on other items — next: the harness pulls each once the work it waits on lands; whose: the harness's
  - 1 is parked by the Lead Product Manager — next: she releases each once what it was parked for is settled; whose: the Lead Product Manager's
  - nothing here is the operator's: under his rule of 2026-09-26 only a change to the fundamental goals is, and nothing here waits on one
  yoyodyne-ifd.200 (The status probe observes leases without acquiring them) — waiting on yoyodyne-ifd.199 (Harness-invoked sessions carry no plan-mode workflow: session mode is set per role)
  yoyodyne-ifd.212 (The architect rules whether bin/yoyo-status is bound by the one-read-model invariant) — parked, so no pull selects it however far the queue drains: the design is being reworked
  yoyodyne-ifd.153 (Interactive sessions get the notes-writer guard: the uncovered loss population) — held since 2026-09-12 09:40 PDT, 3 days ago; run run-5035c832 stopped on it and its change is preserved (branch and worktree checked and there), so a fresh run would start over on top of work that is still there; the development manager decides what happens to it, and nothing pulls it until she has
  yoyodyne-ifd.150 (The release gate commits the tracker's derived exports instead of refusing on them) — held since 2026-09-15 07:05 PDT, 5 hours ago; run run-a17c9b40 stopped on it and its change is preserved (branch checked and there), so a fresh run would start over on top of work that is still there; the development manager has already decided what happens to it, so what is outstanding is the harness carrying that decision out rather than a decision
Needs a human (1):
  directive directive-4f2c… is unresolved: which branch does this land on? — the operator's — the work it affects waits until `yoyo directive resolve` settles it
Waiting on the development manager (1):
  1 admitted item awaits the development manager's decision — the development manager's — nothing pulls a stopped item until she decides what happens to it
Waiting on the harness (1):
  1 admitted item awaits carry-out of a decision already recorded — the harness's — the decision is made, and what is outstanding is the harness acting on it
```

Every work item the lines name is shown beside its title, the first time the
lines name it, including an item named inside a reason or a directive; one the
tracker does not hold says so. [Reporting](reporting.md#every-work-item-beside-its-title)
has the rule.

- **Running** is the developer runs in flight, each with its item, the phase it
  reached, how long it has been going, and what it has spent so far. A run whose
  evidence cannot be priced says so; it is never reported as free. Where any
  [developer slot prefers a label](configuration.md#a-developer-slot-that-prefers-a-label),
  each run also says which slot it is in and what that slot prefers — `, in
  developer slot 1 (prefers the dashboard label)` — and each free slot is named
  under the runs with its preference, `developer slot 3 is free and prefers no
  label`, so the next pull's first choice is readable before it is made. Which
  slot a run is in is the slot its record names where it names one, and is
  otherwise read off the labels the run recorded at its claim, by the same
  derivation the scheduler fills the free slots from, so the slot this line
  calls free is the slot the scheduler will fill. Where no slot prefers a
  label the line reads exactly as above.

  **A run with no process behind it is named as one rather than as running.**
  Its record says `running` until something writes its ending, and a process
  that dies writes nothing, so it stays on this line — it still holds its
  slot — but in place of the phase the line says so, with what ends it, and
  the head counts it:

  ```text
  Running (2 developer runs, 1 with no process behind it):
    yoyodyne-ifd.428.34 (…) — no process can be found behind it: no process holds it, and nothing has been written to it since 2026-09-27T01:05:29Z; recorded as checking; `yoyo reconcile` settles it — a parked run once its record has not moved for 30m0s — and `yoyo run yoyodyne-ifd.428.34` continues it before then, 20h02m elapsed, $41.20 so far
  ```

  What ends it depends on what it was parked on, because the
  [sweep](#recovering-interrupted-runs) does not settle every park. A run
  parked on your pause says the sweep leaves it alone while the pause stands,
  that `yoyo run` continues it, and that the sweep settles it once the pause is
  lifted. A run waiting out a usage limit or an overloaded provider says the
  sweep continues it once its deadline passes, and names the deadline. A run
  paused on work its item depends on names that work and says a watching
  `yoyo work` session continues it at the first pull after it closes. Every
  other run says the sweep settles it.

  Whether a process is behind a run is observed rather than taken, the way the
  Working line below observes a conversation: a process that takes a run's
  lease writes down which process it is beside it, and a reading checks that
  process is still there. A holder killed outright leaves that stamp behind,
  and the run reads as having no process at once. A run with no stamp at all —
  a park whose process let go of it and exited — reads so once neither its
  record nor its event log has been written to for half an hour, which is also
  what keeps a run from a build older than the stamp, still working, from being
  called dead. `--json` carries the first sentence as the run's `no_process` and what ends it
  as `no_process_remedy`, which the dashboard's card prints.
- **Working** is the persona conversations with a turn in flight, which nothing
  counted before this: a conversation is not a run, so a machine spending money
  on six persona turns used to report nothing running at all. The advisory hold
  is what decides, because it is the only thing that actually knows. It is
  observed rather than taken: the process holding a conversation writes down
  which process it is, and a reading checks that the process is still there. A
  status that took the hold to find out — which is how this was first built —
  would refuse a chat that asked for its own conversation in the same instant.
  A turn waiting out a provider usage window or an unreachable provider releases
  that hold. Working lists it as waiting, naming the conversation, process, and
  cause, separately from turns the provider is answering. The turn takes the
  hold back and reloads the conversation before asking again.
- **Not startable** is each admitted item that cannot be started now, with the
  refusal that stops it — the queue's own account where the queue has one, the children
  where an item's unfinished children already carry its execution, the directive
  where a directive pauses the work, and otherwise what has stopped the harness
  choosing at all. The coverage is the scheduling pass's own derivation rather
  than a second reading of it, so a decomposed epic is refused here in the same
  words the pass passes it over in, with the covering children named: the
  tracker reports an epic and the child doing its work as equally pullable, and
  a status that only asked the tracker showed the epic as work about to be
  started and merely stalled, which sent whoever read it after a stall that was
  not one. That last one comes from a closed set of named reasons, each
  of which says who it is waiting on: the operator's hold, a held intake, a
  target branch the harness will not catch up to the remote's, a provider
  nobody can reach, a session waiting out the provider's usage window, a live
  watch session retrying a read of the harness's store that failed, a live watch
  session that has found nothing it can start, a session
  [restarting into a build deployed over it](#a-session-draining-to-restart-into-a-deployed-build),
  no watch session running any more, and a product no session has ever watched.
  A retried read is the harness's move and is never said as idle: the queue was
  not read, so nothing is known about it, and the session line above the runs
  says `retrying a failed read of the harness's store` rather than `idle` for as
  long as the read goes on failing. An idle session, a restarting one, and no
  session are named apart on purpose — telling you to start a session you are
  already running, or one that is on its way back, sends you to the wrong place.
  A provider window is named apart from all of them for the same reason and says
  `Paused on the provider's usage window until 13:43Z`: nobody has a move, the
  window lifts on the provider's clock, and
  reporting it as a session finding nothing to start sends you to look at a queue
  that is fine.
  It never comes from a watch session's memory of what it has already tried,
  which is a fact about one process rather than about the product. Work that is
  admitted and would be started next is not listed here at all; the count of
  admitted items beside the heading is where it shows.

  **Every developer slot being taken refuses nothing.** It is the harness
  working, and an item ready behind it is the next one started as a run
  finishes — so it is never counted in the heading's figure. It has a line of
  its own under the heading, the first of the lines opened by `-`:
  `45 ready, waiting for a developer slot; 3 slots, all taken`, saying nothing
  is asked of anybody. On 2026-09-27 forty-five such items were counted among
  "140 admitted items nothing will pull", filed under the same kind as a
  session choosing nothing, and the operator asked what he was meant to do
  about a line that said nothing will pull work that was next in line.

  **Under it, the refused work is counted by what it waits on**, one `-` line
  per group, each saying how many, what they wait on, the next step, and whose
  move that is: the development manager's decision about a stopped run (hers);
  the harness carrying out a decision already recorded (the harness's); an
  unresolved directive (the operator's, by `yoyo directive resolve` — or the
  Lead Product Manager's, where she has carried it into a document or an item
  and [resolves it into that](conversation.md#directives-and-the-work-they-pause)
  from her conversation, which ends it and lifts the pause); a step
  only a person can take (the operator's, by `yoyo gate record`); ready work
  a switch or a missing session stops (whoever the reason names — the operator
  for his hold or a session that is not running, the development manager or the
  harness for a hold the brake placed, nobody for a usage window); other items
  (the harness's, as they land); a role's conversation, one group per role (that
  role's); parked by the Lead Product Manager (hers); covered by other work, its
  own unfinished children (the harness's); and not offered by the tracker for a
  reason nothing here can read. **The last `-` line says in one sentence whether
  anything on the line is the operator's** — under his rule of 2026-09-26 only a
  change to the fundamental goals is — naming what is where something is, and
  saying `nothing here is the operator's` where nothing is. The items themselves
  follow, as before. The brief rendering the channel's hourly message carries
  keeps every `-` line with the heading, because who moves the work is what an
  unasked-for message has to say. `--json` carries the groups under
  `standing.not_startable_groups` — each with its `kind` (the queue's pile),
  `awaiting` for the two held waits, `count`, `waits_on`, `next`, `mover`, and
  its `items` — the sentence as `standing.not_startable_for_operator`, and the
  slot wait as `standing.waiting_for_slot`, with `ready`, `slots`, `in_flight`,
  its `items`, and the line it `says`.

  One of the queue's own accounts is an item **held**, which is the third and
  fourth not-startable lines in the example above: a run stopped on it and its
  change is still on a branch or in a checkout, its stoppage is in front of the
  development manager and nobody has decided about it, a decision about its
  stoppage is recorded and not yet carried out, an approved change the
  environment stopped short of its promotion is waiting on `yoyo triage resume`,
  or a run promoted its change and could not finish publishing it. A run that
  ended `failed` holding its change is the first of those exactly as a run that
  ended `stopped` is: a run that fails inside its own process hands nobody a
  blocker, so its record ends `failed` while its branch sits there — which is
  what left yoyodyne-ifd.436.4 reading as an item with nothing holding it while
  its approved change waited on a branch. The first is stated from the repository rather
  than from the run's record — the parenthesis says what was found, `branch and
  worktree checked and there` or only one of them — because the record's removal
  flags are what a sweep remembered to write, and on 2026-09-19 a hold read off
  them released yoyodyne-ifd.372 as no longer preserved. A look that could not
  be made holds the item as preserved and says why. The last of those five
  accounts is not a stoppage at all and is held for the opposite reason: the
  change is on the target branch already, so there is nothing left to implement
  and a run started against it can only find that out again — which is what
  yoyodyne-ifd.295 cost, three developer runs and three reviews deep. It says
  whether the forge merged the publication or not, and never guesses: the two
  are different things to settle. Every one of the five names the run and what
  has to be decided rather than leaving the item to its `blocked` status, because
  a status says the same word about a stoppage nobody has answered and about work
  whose every blocker closed months ago — and reading that word as a refusal is
  what hid two-thirds of the backlog on 2026-09-04.

  **Each held item says since when it has been held, and the held items are
  listed oldest hold first.** The entry opens with the moment, in this
  machine's local zone with the zone named, and how long before the reading
  that was in words — `held since 2026-09-12 09:40 PDT, 3 days ago;` — read
  from the record that holds it: the run's stop, the moment its stoppage was
  put on the development manager's docket, or her decision that stopped it.
  The held items take the places in the list they already had, reordered among
  themselves so the one that has waited longest comes first; every other entry
  keeps its place in the Lead Product Manager's order. Until 2026-09-27 the
  list said nothing of when, and a stoppage from yesterday read exactly like
  one from three weeks ago. `--json` carries the moment on each held entry as
  `held_since`, and an entry whose record names no moment carries none and says
  none.

  A held item says which of two waits it is in, because they are two different
  people to go to. **Awaiting a decision** is a stoppage the development manager
  has still to settle. **Awaiting carry-out** is one she has settled into
  something the harness then has to do — a repair handed back, a re-run, a merge
  re-armed — and has not yet acted on. The other three decisions she can make
  settle the stoppage and leave the harness nothing: an item told to wait, to be
  re-scoped, or escalated is held by what she decided rather than by anything
  outstanding, so it is not in the carry-out wait and nobody is watching for a
  run that is not coming. An approved change the environment stopped is in the
  carry-out wait with her having decided nothing, because the wait is named for
  whose move it is rather than for what was decided: the reviewer decided, and
  what is outstanding is the harness resuming the promotion. One rule answers that for every surface — the status
  head, the attention line, the alarm, and the next mover on the development
  manager's own docket — so one piece of work cannot be given two next movers.
  The counts are in
  the head of the line as well as against each item, so the hourly channel
  message, which prints the heads and drops the entries, still says which of the
  two the queue is full of. Reporting both as one thing is what cost 2026-09-07:
  thirty-three items read as a decision backlog for days while the development
  manager had decided every one of them and the gap was the carry-out.
- **Needs a human** is always present, and says either `nothing` or the list of
  what waits on the operator, the one human the line is named for. Everything
  else that is waiting is printed under it, one line per mover — `Waiting on the
  development manager (2):`, `Waiting on the harness (1):`, `Waiting on the
  forge (1):` — in the order the Lead Product Manager, the architect, the
  development manager, the developer, the reviewer, the harness, the forge, the
  provider, nobody's move, then a role the harness cannot name, each head
  counting its own entries and printed only where it has any. Those are not a
  fifth line: they are the fourth line split by whose move it is, so that
  nothing a role or the harness moves is said to need a human. Until
  2026-09-27 every entry was listed under `Needs a human`, and on the dashboard
  a pile of thirty-four items the development manager and the harness were
  moving read as "held for a person". The brief rendering the channel's hourly
  message carries keeps every one of those heads and drops the entries. Taken
  together, the line and the heads under it list who each thing is waiting on:
  the operator's two switches — a held intake with who
  it waits on, which for [a hold the brake placed](#pausing-everything-and-resuming-it)
  is the development manager's or the harness's rather than yours until she
  escalates it, and names the probe run while one is in flight — an unresolved
  directive, each [finding that needs your hand](#where-a-finding-that-needs-your-hand-goes)
  by name — among them each batch of recommendations an owning role argued on a
  [recurring pass](configuration.md#working-the-amendment-queue-on-a-cadence),
  one per pass, as one decision list that is yours — a
  proposed change nobody has decided, a run that ended still owing a step, a
  promotion the forge has not published, work
  marked for a conversation rather than for a run, work held by a step only a
  person can take — named with the step and the `yoyo gate record` that takes
  it — a target branch the harness
  will not catch up to the remote's, with
  [the recovery](#unwedging-a-target-branch-that-diverged-from-the-forge), a
  queue nothing is pulling from — a session sitting idle over it, or no session at all — while admitted
  work waits behind that, the provider holding every role at once (below), a
  part of the product [its supervisor has left down](#starting-the-product-and-stopping-it)
  as degraded, with the reason, a running part whose build
  [cannot read the configuration](#checking-the-installation) — the part, its
  build, and the keys, as the harness's move whichever part it is, with what
  brings the part onto a build that reads the file — a
  [recurring task whose firings keep failing before their first turn](#reading-what-the-recurring-tasks-found),
  with the failure and how many in a row, and a
  [pile of collected reports](reporting.md#whether-the-pile-is-draining) whose
  oldest undecided entry has been waiting more than a week, and its sibling: a
  queue of proposed changes whose oldest undecided one has been waiting more
  than a week, said as a count and an age — `44 of 51 proposed change(s) are
  undecided, the oldest raised 23d ago, against the architect's documents` —
  because the proposals are each named on the line already and a list is not
  an age. A stall over an empty
  queue is not listed: it is a state of the machine rather than something waiting
  on you, and neither is a report pile or an amendment queue that is being worked through — what is
  listed is one that is not. Each head names ten entries and counts the rest,
  except a finding that needs your hand, a repeatedly failing product pass,
  and a hold the brake placed: those are
  named wherever they fall and never counted into `and N things not named
  here`, because a finding folded into a count is one that did not reach you. The unpublished promotions are the same set the
  channel's hourly line counts as awaiting the forge, read by the same
  derivation, and each says who it is waiting on: the forge's while it holds the
  merge queued with passing or running checks; the harness's when the last
  reading has failed checks to settle — with the checks the last sweep read
  beside it, since a merge
  held for checks that will not pass is [not left queued](#recovering-interrupted-runs)
  — the harness's while a queued merge's check state is unread, with the error
  and the next sweep named; a finished run that still owes a reconcile step is
  also the harness's — the development manager's once it has dropped one, the development
  manager's too for a request nothing ever asked it to merge, which is on her
  docket to arm or re-run rather than yours to merge by hand, and the harness's
  for a promotion whose record holds no request at all — the next
  [`yoyo reconcile`](#recovering-interrupted-runs) looks the request up by the
  run's branch and arms its merge. A merge withdrawn because its checks failed on
  the target itself is the harness's as well, saying it waits on the target's red
  check and naming the item filed for it, which the harness takes up once that
  closes ([above](#recovering-interrupted-runs)). An unmerged request whose record carries some
  other account and no drop is still named as the operator's. All of them leave
  the line the moment the forge records the merge and `yoyo reconcile` settles
  it.

An `owed-step` entry is only for a finished run with no live process holding
it. A live run in its checks, including landing checks after integration, never
appears as an ended run here. The entry carries the recorded ending and phase,
the cleanup failure, unfinished landing checks, or undelivered configuration
comparison, and the pull request with the forge's last check reading when a merge still needs settlement. Its words say
which step remains and what moves it: finishing cleanup, recording completion,
delivering a saved configuration comparison, confirming a queued merge, rerunning jobs the forge ended, waiting for a rerun
already requested, or withdrawing a merge over failed checks. A job cancelled,
timed out, or never started is rerun within the head's two-rerun limit, with the
merge left queued; a reading still awaiting that rerun spends nothing more,
even at the limit. Withdrawal follows a refusal, an exhausted limit, or a step
that failed, as [merge recovery](#recovering-interrupted-runs) describes. These are
the harness's steps, under **Waiting on the harness**, and `yoyo reconcile`
settles them. A merge withdrawn over the change's own failing check is a
stopped-run entry under **Waiting on the development manager**, with the
forge failure carried for repair; its unchanged revision cannot be re-armed.
Other dropped merges already handed back are separate publication entries
under **Waiting on the development manager**, for her to decide a re-run or
re-arm. If the same run still owes cleanup of a local promotion, its
`owed-step` entry names only that cleanup, with **the harness** as its mover;
the dropped-merge decision stays on the publication entry. A superseded
publication or one handed back for a fresh run asks for nothing and is absent,
but its run's unfinished landing checks or local cleanup still appear as the
harness's steps. Something only a person can do is named separately, with what
that person has to do; a run having ended
never makes its remaining step the operator's.

The dashboard list's grey tag and the card's heading and kind field all read
the same `label` from the model. Code identifiers remain in JSON as `kind`;
they are not the labels a person reads:

| JSON kind | Dashboard label |
| --- | --- |
| `amendment` | proposed document change |
| `conversation-carried-item` | work in conversation |
| `report` | reports waiting |
| `amendment-queue` | document changes waiting |
| `owed-step` | run not finished or merge waiting or merge stuck |
| `publication` | merge waiting or merge stuck |
| `degraded-service` | service down |
| `failing-task` | scheduled task failing |
| `config-mismatch` | service cannot read configuration |
| `hold` | work paused |
| `directive` | direction unresolved |
| `outage` | provider unavailable |
| `stall` | work not starting |
| `held-work` | work waiting |
| `operator-action` | person needed |
| `product-decision` | work decision waiting |
| `human-gate` | person's step waiting |
| `untraced-pass` | findings not recorded |
| `factory-stall` | nothing completing |
| `tracker-unanswered` | tracker not answering |

A line with nothing in it says `nothing` in words, and a line whose records could
not be read says that instead — never `nothing`, which would be a confident
emptiness assembled from a file nobody could open. There is no fifth line and no
residual bucket: a state that will not render into these four is a bug in the
state.

**One thing is printed above them, and only one.** While the harness is waiting
out the provider's usage window, the reading opens with that and nothing else:

```text
Paused on the provider's usage window until 13:43Z
Running: nothing
Working: nothing
Not startable (3 of 7 admitted items):
  ...
```

It is a banner rather than a fifth line — the four are unchanged and are all
still printed — and it is there because a reading is one of the messages that
reaches you when you want to know why nothing is happening, and the reason for it
should be the first thing you read rather than the third line down beside one
item. It is the same sentence the channel says and the same one the refusals
carry, from the one derivation, and it is off the moment the window lifts.

The same place carries the other capacity state, which the session choosing
work never records because it is not the thing being refused: **the provider
holding every role at once**.

```text
Every role is paused on the provider's usage window until 2026-09-13T03:00:00Z: all 5 agents run on opus and none names an alternate, so nothing fails over; 134 turns refused since 2026-09-08T07:38:40Z
Running: nothing
...
Needs a human (1):
  every role is held by the provider's usage window, since 2026-09-08T07:38:40Z, until 2026-09-13T03:00:00Z — the operator's — the window lifts on the provider's clock, and enabling failover on the agents is what would move the work onto another model before it does
```

It is read from the [refusals the harness records outside a run](#a-provider-refusal-outside-a-run)
and from the runs parked on a limit — each run's own record, since a park is
written there and not in the log — against what each agent is configured to
ask for and to fail over to: a hold stands while a refusal the provider has
not said lifts yet covers the model every agent's turn ends on — its alternate
where it names one, its own model otherwise — and at least one of those
refusals was a turn that actually stopped, or a run that actually parked,
rather than one an alternate served through. A refusal is not standing once
the provider has served the same account and model since it was recorded, or
once its conversation is no longer its role's, so a window lifted early by
added capacity ends the hold at the first served turn rather than at the
quoted reset — see [a watch session inside a recorded
window](#a-watch-session-inside-a-recorded-window). The sentence counts the two as
what they are, `2 runs parked and 20 turns refused since …`, and a run is one
refusal however many probes it makes while it waits. A refusal that names no model,
which is every one recorded before 2026-09-13, counts only where every agent
asks for the same thing, because on a project whose agents differ it cannot be
attributed — and it counts as a refusal of the model they ask for first, never
of an alternate, so enabling failover after such refusals were written ends
the hold they made rather than turning it into one over an alternate that is
being served. Unlike the session's window it is on the attention line as well,
because it is the one capacity state with a move in it: the window is the
provider's, and the configuration that let one window hold every role is yours.
Between 2026-09-08 and 09-13 the harness recorded 134 of these refusals and
said nothing about what they added up to; this is what says it. Where the
session's own window and this are both true, the session's is the banner —
it is the same fact with less inference — and the hold is still on the
attention line. Nothing else is ever put above the four lines: every other
reason the harness is choosing nothing is inside them.

**Under the four lines, one line per [program manager](designs/program-manager.md)
instance**, where any is configured or any has a restart request open:

```text
Program managers (2):
  factory-pgm — lane reliability — stale: no pass has completed since 2026-09-25T07:00:00Z, and its schedule is every 1h0m0s
  writing-pgm — lane writing — blocked: blocked on 1 open ask (exchange-0123456789abcdef); 1 blocker its report names that the record does not bear out
```

It is not a fifth line, and nothing on it waits on you: you read an instance
when you choose. Each instance says its name, its lane, and one word — **blocked**,
**stale**, or **working** — and, where the word is not `working` or a pass was
missed, why. The word
is derived by the read model, and nothing an instance writes sets it:

- **Blocked** is a blocker in the instance's latest
  [lane report](conversation.md#a-program-managers-lane-report) whose `cites`
  names a record of the instance's own that is still open: a report the Lead Product
  Manager has not handled, an amendment nobody has decided, an exchange still
  open, or a restart request nothing has answered. A citation that names
  nothing, names another instance's record, or names one already decided blocks
  nothing; it is carried as a claim, with the reason, and the line counts them.
- **Stale** is no completed pass within twice the instance's `triggers.every`,
  measured from the last pass that ended in an account. A pass the provider
  refused, a turn the size backstop rejected, and a scheduler that died all look
  the same here, which is the point: it is the sign that the watcher is not
  watching, and it is plain Go over the pass records with no provider call on
  the path, held there by the sweep in `internal/watchdog` that holds the stall
  reading to the same. An instance that has never completed a pass is measured
  from when the harness first saw it in the loaded configuration: the first load
  that carries an instance — `yoyo status`, the dashboard or the Slack sink as
  it starts, or any verb that builds the harness — records that moment under the
  state root at `projects/<product>/state/program-managers/first-seen.json`, and no
  later load moves it. The dashboard and Slack sink also record their own
  build and the configuration keys it reads as they start, under
  `projects/<product>/state/config-readers/`. These records direct no work; the
  surfaces otherwise only read. The first-seen record is on them on purpose:
  the dead scheduler this word is for is
  exactly the case in which nothing else loads the configuration. So an
  instance the scheduler has never woken reads stale twice its schedule after
  it was first seen; the line says `first seen in the configuration at …`.
  Where its first conversation is earlier — an instance configured before the
  record existed — that is the moment instead, said as `first woken at …`. The
  moment is kept when an instance is taken out of the configuration, so one put
  back under the same name long afterwards, with no pass completed in between,
  reads stale at once until its first pass completes. One with no `every` is
  never stale.
- **Working** is neither. Stale outranks blocked in the word, and both are
  carried.

**A pass the instance missed is said on its line under any of the three
words**, since its last completed pass: the
[missed pass](#reading-what-the-recurring-tasks-found) its sweep record holds —
which trigger owed it, whether no pass followed or the pass was cancelled
before it completed, when, and the record's own account of the cause — as the
harness's move, which the next completed pass ends:

```text
  factory-pgm — lane reliability — working: its scheduled pass was cancelled before it completed at 2026-09-26T19:04:00Z — the harness's — the scheduled pass of the program manager instance factory-pgm, taken at 2026-09-26T19:04:00Z, was cancelled before it completed: nothing recorded how it ended, …
```

It does not change the word: a missed pass is the stall before the instance
reads stale, named while it stands rather than two schedules later.

`--json` carries each instance under `standing.program_managers`: its `agent`,
`lane`, `status`, `stale` and `blocked` separately, `stale_says`,
`last_completed_pass_at`, the `missed_pass` where one stands — its `trigger`,
`how`, the `what` the line opens the miss with, `at`, `recorded_at`, what it `says`, and its mover under `waiting_on`,
always `harness` — the `blockers` each with its mover (`waiting_on`),
its citation, and the kind of record it resolved to, the `claims` each with its
`reason`, the report's `report_path` and `report_written_at`, and its open
`restart_requests`. A record behind them that could not be read is said under
the line and in `program_managers_problem`, and never read as none: a pass log
nobody could open calls nobody stale. The channel's hourly line counts the
stale instances — `Program managers stale: 1 of 2 (factory-pgm)` — inside the
message it already posts for a stopped line, and posts nothing for a stale
instance alone. The dashboard's
[program managers section](#what-the-page-presents) lists the same instances
from the same field, and opens each one's report.

Naming an item leaves the four lines out. They are about the product, and a
question about one piece of work is a different question. `--json` carries the
same derivation under `standing`, so a second surface reads the answer rather
than parsing the rendering. Two things it carries are not printed, because the
lines say them by omission: `standing.startable` is how many admitted items
nothing refuses — the work the harness pulls next, counted over the same
entries as the refusals, and zero whenever the pass-level stall stands, except
a full machine, whose ready items are counted here because they are next — and
each running run's `stage` is its phase folded onto `developing`, `reviewing`,
or `integrating`. Both are there for the dashboard's pipeline, so it reads the
model's count and the model's fold rather than making its own. Beside the
counts, `standing.admitted_items` and `standing.startable_items` name the
items each count counts, by id and title, in the Lead Product Manager's order — so
the page's pop-up on a grouping lists what the figure counted rather than a
list assembled from the other lines — and both are absent, like the refusals,
where the queue could not be read.

Each entry under `standing.needs_human` is the thing waiting rather than a
sentence about it: its `kind`, from a closed set — `amendment`,
`conversation-carried-item`, `report`, `amendment-queue`, `owed-step`,
`publication`, `degraded-service`, `failing-task`, `config-mismatch`, `hold`, `directive`,
`outage`, `stall`, `held-work`, `operator-action`, `product-decision`,
`human-gate`, `untraced-pass`, `factory-stall`, `tracker-unanswered` — the `id`
of the record it is about (an amendment's, a
directive's, a run's, a work item's, a service's name, a recurring task's
name, a human gate's name, which switch a hold is: `operator`, `intake`, or
`capacity`, and for a `factory-stall` the moment the factory last did anything
and for a `tracker-unanswered` the moment listings began failing), the
`mover` whose move it is, in the same closed vocabulary the
page counts by (`operator`, a role such as `architect`,
`development-manager`, or `program-manager`, `harness`, `forge`, `provider`, `nobody`, or
`unnamed-role`), and
the record itself, whole, under a field named for the kind — `amendment`,
`directive`, `outage`, `stall`, `reports`, `service`, `failing_task`, `config_mismatch`,
`owed_step`, `publication`, `held_work`, `amendment_queue`, `operator_action`,
`product_decision`, `human_gate`, `untraced_pass`, `factory_stall`,
`tracker_listings`, and for a hold `operator_hold`, `intake_hold`, or
`capacity_hold`, whichever switch the `id` names. An `amendment` carries the
target document, the proposer's role, agent, run, and work item, the proposed
change, and why, none of it cut to a line. An entry about one admitted work
item — the carried item, the item a run was carrying, the item an amendment's
proposer was working on — names it under `work_item_id` as well; a carried
item carries its `executor` marker rather than a record of its own. A
`human-gate` entry names the item that declared the gate under `work_item_id`,
and its `human_gate` carries the gate's name and what the person has to do — or,
for a declaration nothing could read, what is wrong with it and no name. Two kinds
carry no `id`, because each is about a set rather than a record: `report` is
the pile, and `held-work` is how many items are in one of the two waits. The
`label` and the `what` and `whose` sentences are there beside them, derived
from those fields at the moment the answer is written rather than
stored, so a record and the line about it cannot disagree; a document whose
`what` says something its fields do not, or whose `kind` or `mover` is outside
its vocabulary, is refused when the model reads it back. `said_what` and
`said_whose` are the same two sentences as the terminal prints them, with
[every work item beside its title](reporting.md#every-work-item-beside-its-title),
and are present only where that changes something; the dashboard shows those.

One thing is carried there that the four lines do not print: what is parked or
held on provider capacity, one run and one conversation at a time, under
`standing.capacity_blocked`. The hold above is every role refused at once; this
is each thing the provider has stopped on its own. `runs` lists each work item's
latest run that is either `waiting` — in flight and asleep on a recorded
deadline, still counted on the running line — or `capacity-blocked`, which is a
run the provider refused and the harness would not wait for. One stopped by a
usage window resetting past the maximum pause gave its item back to the queue
and names the reset it is pulled again after. A usage-limit stop is listed too:
a run the provider refused on a usage limit naming a reset that is not in the
future, or on an overload that outlasted the pause budget, stopped with a
blocker on its item and its stop cause `usage-pause`, and its record keeps
which of the two refused it so this list can name it. It names no reset,
because the run took no wait. On the attention line it is the stopped item
waiting on the development manager; it does not count toward the hold over
every role, which reads only runs still asleep on a deadline. Each says what refused it, since when, the reset it is
waiting out or none, how much of `execution.usage_limit_max_pause` it has spent
(`waited_seconds`), whether its change is preserved, and what a person can do about it — for a
waiting run, that nothing needs doing. `conversations` lists each conversation
the provider is still refusing, read from
[the refusals recorded outside a run](#a-provider-refusal-outside-a-run): one
entry per conversation however many turns were stopped, since the earliest
standing refusal, until the latest reset any of them named, with the turns an
alternate served through not counted. **A block clears before its reset on
evidence the window lifted.** A turn or a run the provider served on the same
account and model after a refusal was recorded clears that refusal, so a
conversation whose every refusal came before such a turn is not listed, and a
run the provider stopped is not listed once its account and model have been
served since it stopped; a run still asleep on its deadline stays listed as
`waiting`, because it is, and `yoyo resume <item>` asks now. A conversation that
is no longer its role's current one — replaced, as the development manager's
`chat-419cedb4…` was on 2026-09-24 — is never listed, and its refusals count
toward no hold. [A watch session inside a recorded
window](#a-watch-session-inside-a-recorded-window) says how the served turn is
recorded; the dashboard's capacity section, `yoyo status`, the channel's
provider-hold message, and the watch session's hold on intake all read this one
derivation. Where either the record of what was served or the conversation
records could not be read, nothing is cleared early by either of them, and the
failure is named in `runs_problem` and `conversations_problem` alike — the
evidence clears stopped runs as well as conversations, so both lists may be
longer than they would have been — and on the "Needs a human" line's
`needs_human_problem`, since the hold over every role reads the same evidence.
A run waiting on a login or a network is
not capacity and is not here; the outage banner says it. Both lists are always
present, and each says under `runs_problem` or `conversations_problem` when
its records could not be read rather than reporting an empty list. It is not
printed as a fifth line: it is the read model's capacity query, carried for the
capacity panel and for scripts.

## When nothing happened at all

Under the four lines, `yoyo status` reads back every stretch in which this
product went quiet: nothing started at all, while the tracker reported work
ready, and no hold, no full machine, no still-moving run and no provider usage
window accounted for it.

```text
nothing started on this product for 7h30m0s from 2026-09-01T06:05:00Z, with 3 items ready; it cleared at 2026-09-01T13:35:00Z
  the session choosing work last recorded watching at 2026-09-01T06:05:00Z, and has said nothing since
  cleared by: 1 developer run(s) are in flight and still moving
```

The second line is the one to act on. A stall cannot say why it happened —
it is precisely the absence of anything having been written down — so what is
recorded beside it is the last thing the watch log holds, and that is what tells a
scheduler that died from one that is wedged: a session whose last word was
`stopped` wants starting, and one still claiming to be `watching` wants killing
first.

**The silence is measured from the last moment anything held a developer
slot**: the later of the last run start and the last run end. That is the
`from` in the first line. A slot is held for the whole of a run, so a run
ending moves this moment on just as a run starting does. Until
yoyodyne-ifd.428.19 it was measured from the last start alone, and that was
wrong in one repeating way. The watch fills every free slot in one pull, and
runs take more than an hour. So when a batch ended there was an instant with
nothing in flight and a last start over an hour old. The watch read a stall at
exactly that instant, a second before the pull that refilled the slots. On
2026-09-24 the last twenty alarms were all that shape. A slot free for seconds
before a pull is not a stall now. Slots free for the whole threshold still are.
One kind of end does not count. A run whose process was already gone is ended
by the harness: `yoyo reconcile` settles it, or the claim audit cancels its
claim. That end is when the harness noticed, not when the run let its slot go.
So such a run counts as holding its slot only until its record last moved,
which the settlement keeps on the run as `settled_quiet_since`. A sweep
therefore still opens the stall right after it settles a dead line, dated from
when the line went quiet rather than from the settlement.

What the message that wakes somebody says beside that is the last poll's own
account of the queue — "33 of the 47 admitted items are awaiting carry-out of
decisions already recorded", and the next mover with it — which it reads from the
watch log rather than from the stall, and only where that poll was made after the
silence began.

What that bound refuses is an account a start overtook: something ran after the
poll and the line then went quiet, so the queue has not been read since it moved.
A session that died carrying a run is the usual way that happens, and there the
message names no cause and points at the chooser. It does not refuse the account
of a session that died while idle, which polled after the last start — that
message names the cause, because the items really are held, and a named cause is
therefore no evidence that the session is alive. The chooser's last word is what
says that, in the message exactly as in this listing: a session that last recorded
something and has said nothing since wants looking at whatever the queue is
holding. This listing keeps that last word and no cause, because the stall record
is the absence and the account of what was in the way of the queue belongs to the
session that read it.

This is the one history in the harness that nothing else keeps, and the reason it
exists is that the process which would have recorded a stall is the process a
stall means has died. A session that crashes writes no stop, so every other
surface reads a dead machine as a quiet one — which is exactly what happened on
2026-09-01, for seven and a half hours, until a person noticed.

**Two things notice, and between them they cover the two ways it happens.**
[`yoyo work --watch`](work.md#letting-the-harness-choose-the-work) takes the
reading as it polls, at most once per `--stall-after`: that is the harness's own
loop, and it catches the session that is alive and has stopped starting anything —
a queue whose ready items are all claimed by runs that died, say. A session that
died itself writes nothing at all, so [`yoyo reconcile`](#recovering-interrupted-runs)
takes the same reading as the last reading of the sweep that settles what a dead
process left behind — after every settlement, and before the two steps that
follow it and host runs: [continuing a usage-limit wait](#waiting-out-a-provider-usage-limit)
whose deadline has passed, and carrying a queued merge the settlement
[put back at its promotion](#recovering-interrupted-runs) through its update.
That ordering is why it is that sweep and not another: a
killed run goes on saying it is in flight until the settling, and a phantom run
counted as activity would silence this for exactly the crash it exists to catch.
The two readings can land at the same moment. Each one reads the stall log and
appends to it under one lock shared across processes, so a stall is opened by
exactly one of them. The log then holds one line when a stall opens and one
more when it closes, with the same event id. `yoyo status` and the channel read
that pair as one stall.

Reporting has nothing to do with either. A product that never turned Slack on
records its stalls and reads them back here, and a product that did gets the same
record [taken to the operators](reporting.md#reporting-into-slack) — again every
heartbeat and louder as it stands — by a sink that reads it rather than produces
it. That was the other way round until
`yoyodyne-ifd.295`, and it meant the products least able to notice a stopped
harness were the ones with no stall history at all.

How promptly a stall is noticed is `--stall-after` — ten minutes by default, and
the same flag on both commands — and, for the sweep, the cadence of whatever runs
it. The product's supervisor runs it on its
[maintenance pass](#the-supervisors-maintenance-pass), every
`services.maintenance.every`; a product with that part off, or with no
supervisor running, schedules `yoyo reconcile` itself. A machine
running neither a watch session nor a sweep records no stalls, so this listing is
empty on one; the sink says so when it starts, because that is the state nobody
would think to check for.

A product that has never gone quiet says nothing here at all. The five most
recent stalls are printed, newest first, and `--json` carries every one of them
under `stalls`; naming an item leaves them out, because a stall is about the
product rather than about any piece of work.

## Claims with nothing working on them

The reading above catches a line that has stopped over a queue with work in it,
and it is blind by construction to the case beside it. The harness claims a work
item at the start of every run, and nothing gives the claim back when the process
holding it dies — so what a killed run leaves is an item the tracker calls in
progress, a run record nobody is writing to, and a scheduler that will never
choose the item again, because it chooses from what the tracker calls ready and a
claimed item is not ready. An item under a dead claim has left the ready queue,
so a machine whose only startable work is sitting under one reads from every
surface here as a drained queue: nothing held, nothing running, nothing ready,
nothing wrong.

That cost four nights of throughput in the week of 2026-09-01, twice inside an
hour on the last of them, and each time the line was idle until somebody looked
in the morning.

A watching session now audits the claims against the runs on every pull, before
it consults the intake hold or its own capacity, and before it sits out
[a provider nobody can reach](#waiting-out-a-provider-nobody-can-reach) — which
is the placement that matters, because a held queue is often the session's own
brake, placed exactly when runs are failing one after another, a machine whose
every slot is held by a run that died never gets as far as reading the queue at
all, and a run killed before a login expired is exactly as dead through the wait.
A claim with no run alive behind it for half an hour is given back:

```text
yoyodyne-ifd.264 was claimed with nothing working on it and was given back to the queue: its run run-264 ended failed at 2026-09-03T22:14:00Z and the claim outlived it
```

Giving the claim back is only half of it, and the other half is what makes the
item actually startable. A killed process leaves a run record that still says it
is in flight, so it goes on filling a developer slot and the scheduler goes on
passing the item over as already running however open the tracker says it is. So
the audit ends that record too, under the run's own lease — which is also the
only test of liveness that is not a guess, because a lease belongs to a live
process and the operating system drops it when that process dies, so a lease the
audit can take is a process that is gone. The ending is recorded as **cancelled**:
nothing here judged the change, and the branch and worktree the killed run left
are untouched, exactly where they were. The item is then pulled again on the same
pass, with nobody having typed anything.

This settles a much narrower set than [`yoyo reconcile`](#recovering-interrupted-runs)
does and never overlaps it: it moves no refs, removes nothing, closes nothing,
and touches only runs that have gone quiet past the threshold with no wait
recorded on them.

A tracker that will not answer costs the audit and nothing else. It is the one
reading in the pass that is reported rather than retried by the session — the
listing itself is asked again, twice, when its bound kills it, as
[every listing is](#a-tracker-that-does-not-answer-a-listing) — because the pass has
answers that need no tracker at all — a held intake is read from a switch — and
an operator running `yoyo work` on a machine whose tracker is down should still
be told what is holding it.

Alive means the run's own record still moving, not the status field saying it is
in flight: a killed process leaves a record that goes on claiming to be running,
so the test is whether anything has been written to it within the hour — and then
whether its lease can be taken, which is the answer the timestamps were standing
in for.

Four kinds of claim are never given back. A run that stopped short and is owed a
continuation keeps its claim however quiet it has gone — one waiting out a
[usage limit](#waiting-out-a-provider-usage-limit) or an
[overloaded provider](#waiting-out-an-overloaded-provider), which the
reconciling sweep continues itself once the deadline has passed and no process
is serving the wait, one waiting out
[a provider nobody can reach](#waiting-out-a-provider-nobody-can-reach), one parked by
[`yoyo pause`](#pausing-everything-and-resuming-it), one held up by an
unresolved directive or by work its item depends on, one parked because
[the tracker would not answer](#waiting-out-a-network-that-dropped) the read a
gate boundary makes, one [put back at its promotion](#recovering-interrupted-runs)
to bring a queued head up to date, which the reconciling sweep hosts as its last
step, and one whose provider
[the harness stopped on time](#when-a-provider-stalls-or-runs-out-of-budget).
The audit leaves each of those as a wait, and the reconciling sweep, not the
audit, [settles](#recovering-interrupted-runs) every one of them but the usage
limit, the overload, work its item depends on, and a pause that still stands
once nothing has continued it for half an hour. Each of those returns and
lets its process exit, so its record goes as still as a killed one's, and its
item is claimed on purpose with the worktree and developer session that
continuation needs. Every one of those is a wait that is still pending, which is
the whole rule: how many repair rounds a run has already been through is history
rather than a wait, so it says nothing about whether the run is coming back and a
run killed after one is a dead claim like any other. The question is asked again
under the run's lease before anything is written, so a run that parks between the
two keeps its claim.

And a claim with no recorded run behind it at all is left alone: the harness
reserves a run before it claims anything, so such a claim is somebody's own and
not the harness's to take back.

An item whose latest run already promoted its change is left alone too, and that
one is left for `yoyo reconcile` rather than for a person: the change is on the
target branch and the item wants closing rather than developing a second time,
which is what the sweep does from the promotion the record holds.

**And an item whose latest run ended holding its change is left alone, whatever
the claim says.** A claim there is not a claim nothing is working on: it is the
one thing saying an item whose change is sitting on a branch is spoken for, and
giving it back buys a fresh run started over the top of that change. Four
endings are in the class, and they are the same four the pull's own
[hold](work.md#letting-the-harness-choose-the-work) reads, so the two cannot
disagree about one run:

- **An approved change the environment stopped** short of its promotion, which
  `yoyo triage resume` finishes. The audit leaves it for that verb, as it leaves
  a wait for the sweep.
- **A run that left somebody a blocker** with its change still on its branch — a
  replay that conflicted against a target that moved, first among them.
- **A run that died inside its own process** with its change still on its
  branch. This one is the ending nothing announced: such a run deliberately
  hands nobody a blocker, so its record ends `failed` rather than `stopped` and
  every rule that looked for a blocker read it as an item with nothing holding
  it.
- **A run whose check stage its bound stopped**, which ends `timed out` with
  its finished change on the branch. The harness
  [continues it at its checks](#what-a-check-stage-may-cost-and-where-the-whole-suite-runs)
  once the load allows, and the audit leaves it for that.

The death inside a run's own process is what this rule was written for. On 2026-09-22 run-b0b6d18d's
change on yoyodyne-ifd.436.4 was approved and then stopped at the promotion by a
tracker read that timed out; the run ended `failed` at 04:04Z with its branch
preserved and its pull request open, and the docket named the harness and `yoyo
triage resume`. At 05:00Z the audit read the item as a claim with nothing
working on it and gave it back, the next pull started a run from scratch, and
that run spent a developer and a reviewer re-deriving the change that was
already on the branch and integrated it a second time.

A run whose process was killed is none of these, and is a dead claim exactly as
it was however much of its half-finished change is on disk: nothing ended it and
nothing owes it a move, which is the whole failure the audit exists for. What
tells them apart is the ending on the record rather than what is in the
worktree.

Whether the change is still there is asked of the repository as the audit reads
the claim — the run's branch and its checkout, looked for — and never of the
run's removal flags, which are what a cleanup remembered to write. A look that
could not be made keeps the claim. And every release says on the item what the
look found, because "nothing was working on it" is a sentence about processes
and was read as one about the change: on 2026-09-23 the development manager
crossed yoyodyne-ifd.432.10's re-run cap reasoning that run-838ffc48 had left no
preserved change, from a release that said nothing either way, while the run's
branch held the approved change. A release now reads:

```text
The harness gave this item back to the queue at 2026-09-23T01:43:04Z: its run run-838ffc48… ended cancelled at 2026-09-23T01:01:32Z and the claim outlived it. Nothing was working on it, so it was released to be pulled again.
What the run left: branch yoyodyne/yoyodyne-ifd-432-10/838ffc48 (checked and there at 2026-09-23T01:43:04Z); worktree …/yoyodyne-ifd-432-10-838ffc48 (checked and NOT there at 2026-09-23T01:43:04Z). That change is still there; a run started for this item should pick it up rather than derive it again.
```

A release written before the audit looked says none of that, so
[the convergence sweep](#recovering-interrupted-runs) corrects it: where a
released claim's run still has its branch standing and the release did not say
so, the sweep appends a correction to the item naming the branch, the commit it
is at, and why it is kept, and records on the run that it did, so the item is
told once. `yoyo reconcile` prints the branch as kept with its item corrected.

Each release is
[sent to the operators once](reporting.md#reporting-into-slack), in the item's
own thread, as the degraded harness it is. It is said once and never repeated:
what follows a release is the item being pulled again, which says so itself.

The two deaths are worded apart because they are two different histories and a
reader acts on the difference: a run that ended left the claim behind by
finishing without giving it back, which is a hole in the pipeline, and a run
still recorded as in flight is a process something killed. What is done about
them is the same either way, so neither line hands you a chore.

## Conversation-carried work that has landed

The other thing a pull closes for you is work no run ever carried. An item
admitted for a role's conversation — `executor: conversation:architect` — lands
as a revision in a document that role owns, and until 2026-09-20 nothing read
that revision back to the tracker: the Lead Product Manager closed such items on
evidence some turns later, and one of them had a developer run spent on it
first. Now every pull reads the documents each marked role owns, and an item
whose identifier opens the reason of a revision in one of them, made by that
role, is closed at that pull:

```text
yoyodyne-ifd.330 was closed: its architect landed as the 2026-09-07 05:30:00Z revision of docs/designs/management-and-supervision.md
```

The close reason on the item names the document, the revision, the convention
it was read by, and the revision's own words, so a close you disagree with is
one you can read and reopen with a note — and the reopen holds, because the
revision is recorded on the item ahead of the close and a pull that finds the
item open and still carrying it leaves it alone until a later revision lands.
A revision that mentions an item further into its reason closes nothing, which
is what keeps an item with two deliverables open on a revision that carries
one; a landing the tracker will not close is reported on the pass and left for
you, and so is the one case a person has to clear by hand — a close the tracker
refused after the revision was recorded and the record could not be taken back
off, which the pass names with the metadata key to clear. [How work
flows](work.md#letting-the-harness-choose-the-work) says what is read and why,
and [the artifacts manual](artifacts.md#artifact-identity) says how to write
the revision so it is read.

## What became of the runs, and what remains of them

Under the stalls, `yoyo status` reads back what the runs themselves recorded
— newest first, the work item, the outcome and the phase the run reached, what
remains of it, what it cost, why the item was chosen, and the reasons its record
kept:

```sh
./bin/yoyo status                    # the four lines, then the twenty most recent runs
./bin/yoyo status --failed           # only the ones that did not succeed
./bin/yoyo status yoyodyne-ifd.90    # one item's runs, without the four lines
./bin/yoyo status --limit 0 --json   # every recorded run, for a script
```

A run's cost is read from its whole event log, so the listing prices only the
runs it shows: the default twenty cost twenty log reads however many runs the
product holds. `--limit 0` is the exception you ask for — it shows every run,
so it reads every event log the product has written, and its cost grows with
the product's history.

The listing below is `./bin/yoyo status --failed --limit 2`:

```text
runs that ended without succeeding, 2 of 9 shown (137 run(s) recorded):
run-19dc9dff153e1eb89a2470f78f02f240 yoyodyne-ifd.1.7 started 2026-09-26T18:02:11Z [stopped, developing, work preserved, checked] $4.62
  selected by the operator: the operator ran this item by name from the command line
  ran under default, configuration cfg-9f2c41ab7e05, harness 9870df6a1b2c
  reason: provider: the provider ended this run without judging the work after 3 of 3 permitted relaunch(es)
  branch (checked and there at 2026-09-26T19:40:02Z): yoyodyne/yoyodyne-ifd.1.7/19dc9dff
  worktree (checked and there at 2026-09-26T19:40:02Z): /Users/you/.yoyodyne/projects/yoyodyne/worktrees/yoyodyne-ifd-1-7-19dc9dff
  preserved developer session: 0f2c41ab-7e05-4c3d-9a1b-6e8f0d2a4c71
run-c81f0a4d7c2b41e6a0f9d3b5e7104c22 yoyodyne-ifd.63 started 2026-08-15T11:47:03Z [failed, no artifacts recorded] $12.80
  selected: no reason recorded
  ran under an account the record does not name, configuration a configuration the record does not name, harness a build the record does not name
  reason: create isolated worktree: primary checkout is not ready for integration
7 further run(s) are not listed here; --limit reports more, and 0 reports all of them
each reason is shown as one line; --json carries what the record holds in full
```

The word in the brackets is what became of the *work*, not of the attempt, and it
comes from a small fixed set:

| word | what it means |
| --- | --- |
| `succeeded` | the work landed |
| `stopped` | it ended on a durable blocker: the item carries it and nothing was discarded; the development manager decides what happens next, except for a first stall of a silent provider stream, including during a repair, which the harness [continues once itself](#when-a-provider-stalls-or-runs-out-of-budget) and which becomes hers only if it stalls again |
| `cancelled` | something stopped it rather than judged it — the operator, a killed process, or retirement after another run confirmed the item's merge |
| `timed out` | the harness stopped it on time; nothing judged the change, and only a check stage its bound stopped is acted on afterwards — [continued at its checks by the harness](#what-a-check-stage-may-cost-and-where-the-whole-suite-runs), then the development manager's once those continuations are spent |
| `failed` | it ended without succeeding and without leaving anybody a blocker |
| `pending`, `running` | it has not finished |

Beside the status and blocker, `stop_class` names the one cause that ended a run
without landing. It uses the existing closed list, extended with the bounds in
[the run-stop inventory](run-stops.md): `check-timeout`, `check-stage-bound`,
`provider-idle`, `provider-budget`, `repair-budget`, `integration-budget`,
`relaunch-budget`, `promotion-wait`, `usage-pause`, `operator-stop`, `manager-stop`,
`redeploy-drain`, `dead-claim`, `developer-account`, `review-account`,
`work-item-escalated`, `integration-policy`, `recovery-window`, `context-bound`,
`state-bound`, `event-bound`, and the
inventory's causes outside the work. Other refusals retain their gate names.
The refusal record still controls refunds; a stop class changes no budget
or recovery rule. An older record with no class reads as `unknown`, without
inferring a cause from its prose or its leftover check findings. Status prints
`stop cause`, the docket names it, and the run's Slack reason carries its name.
`yoyo status --json` carries the shared read model's `throughput.windows`, whose
`stops_by_cause` counts endings today and in the last seven local days, including
successful escalations that landed nothing. These counts cover the whole run
history even when the displayed history is narrowed or limited. The dashboard's
Throughput section prints the same counts. In-flight waits and successful
landings count as no stop.

`stopped` covers every ending the harness hands to somebody: an unrepaired
review, a check that kept failing, refused protected paths, a replay the target
branch outran, a provider that would not carry the run, and a promotion the
target branch turns out not to carry. The phase beside the word says where it
stopped and the `reason` under it says what stopped it, so the one word never has
to carry all six. This used to be one word — `failed` — for all of them and for
the two below it, which is how three preserved runs came to read as three
discarded ones.

A blocker outranks the run's own status, `succeeded` included. The last of those
endings is the one where that shows: a run promotes its work, records it, and
`yoyo reconcile` then finds the target does not carry the promotion, so the item
goes back to the development manager's docket while the run's record keeps the status it wrote
for itself before anything contradicted it. `--json` shows both — a `status` of
`succeeded` beside an `outcome` of `stopped` — and the outcome is what became of
the work.

Beside it, every run that did not succeed says what remains, and it says it
from the repository rather than from the run's removal flags: the listing looks
for the run's branch and its checkout as it is printed. `work preserved, checked`
is either of them found there, `work gone, checked` is neither, and `no
artifacts recorded` is a record that names neither. Where the repository could
not be asked, the listing says so — `work possibly preserved, not checked`, with
the reason on the artifact lines — rather than reading the flags out as though
they were a look; a flag is what a cleanup remembered to write, and on
2026-09-23 run-838ffc48's flags said removed while its branch held the approved
change. The branch, the worktree, and the developer session are then named under
the run, each with what the look found and when, so looking at the change is not
a trip through the run's JSON for a path. `--json` carries the same answer as
each run's `found`. A successful run removes what it made by design, so it says
nothing about preservation at all; a run still in flight holds everything it
has.

The third phrase states an absence rather than claiming the run made nothing —
the same discipline as the `selected: no reason recorded` and `an account the
record does not name` lines below, and for the same reason: a listing that turns
an empty field into a reassurance is the failure this one exists to remove. In
practice it is a run that broke before it got a worktree, which is also why the
second run above has no phase between the two words: the phase is only recorded
once the worktree exists, so any run carrying one has a branch and a worktree and
reports what the look found of them with the paths underneath.

The `selected` line is on every run, including — in those words — a run that
recorded no reason at all. That is deliberate: work the harness chose and cannot
account for is exactly what you most need to see, and a line left out would read
as a reason you had already looked at rather than as one nobody wrote.

The `ran under` line beneath it is the same shape of fact and is printed for the
same reason: which provider account the run spent, the revision of the
configuration that set it up, and the revision of the harness that dispatched it.
A project with one account reads `ran under
default`; a pooled one reads whichever account the pool served that run, which
is what makes a rotation something you can see rather than infer. The revision is
a digest of the effective configuration, so two
runs carrying the same one were configured identically and a run whose
configuration was edited under it is distinguishable from one that was not;
`yoyo config show` prints the active revision. A run recorded before any of the
three was carried says so, in those words, rather than showing a blank.

The `harness` on the end is a Git object name, shortened here and carried whole
by `--json`. It is there because a process runs whatever binary it was started
with while the harness moves on underneath it, so a run that behaved like a build
from before the fix is otherwise indistinguishable from a fix that does not work —
which is how a week of deployment defects came to be read as code defects. A
binary installed without the stamping records none, and the line says so rather
than inventing one: a comparison nobody can make is an answer, and a comparison
made against the wrong commit is not.

The first word of the `reason` names the recorded gate or bound from the
run-stop inventory. It is read off the record rather than worked out from the
evidence printed under it: a `failing check` is what the last repair was handed,
and can still be there when the provider later stops the run. A run recorded
before the class existed prints its reason as recorded and names its stop cause
`unknown`, as the second run above does.

The words after the class are the run's own failure where it recorded one, and
otherwise the blocker its item was handed back in: a stoppage `yoyo reconcile`
settles onto a run some killed process had already ended can carry the blocker
and no failure, and the line says the blocker rather than nothing. A run that
ended without succeeding and whose record gives neither says `the record names
no reason`, in the same words the channel's line uses for it. The reason is one
derivation in the run's record, so this line and the channel's give one reason
for one run.

Each of the other reasons is printed under the run it belongs to and named for
what it is, because the records keep them apart deliberately. Only `reason` is the
run's own account of why it ended. An `outstanding publication`, an `outstanding
cleanup`, a `failing check`, and a `completion recorded late` are recorded around the work,
and a run can carry one of them with its change already promoted. The last of
those is the class whose work-item note is itself unreliable — recording that
note is part of what was failing — so the run record this verb reads is its
authoritative home. A succeeded run that stopped short of one of the first,
second, or last of these carries it as its `reason` too, under `publish`,
`cleanup`, or `recording`, and the line it came from is not printed a second
time.

`outstanding` in the brackets marks a finished run that still owes somebody a
step, and the `outstanding:` line under it says which — cleanup that is not
recorded as finished, or a merge the forge queued and nothing has settled — so
the marker is never left for you to go and interpret out of the run's JSON.
[`yoyo reconcile`](#recovering-interrupted-runs) is what settles either. The
marker is said only of finished runs: one still in flight owes its own remaining
steps by definition.

Naming an item reports one more thing under its runs, because it is the one
question no run can answer: what that item has cost and what it has been given.
Every budget a run spends starts again at zero in the next run, so an item handed
back, run again, and handed back again is an item nothing bounds. The per-item
counters are what bound it:

```text
triage of yoyodyne-ifd.90: triage has spent 2 passes on it
  review rounds: 3 spent across every run of this item, under the cap of 4
  repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains
  merge re-arms: 1 across every publication of this item, 1 permitted per publication
    publication:run-a#92: 1 of 1 permitted
  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say
```

Every figure here is a budget, and the first line counts what has been spent
rather than how many times somebody looked. Only three of the development
manager's eight decisions spend anything — a repair grant, a re-run, a merge
re-arm — so an item it escalated or told to wait shows `triage has spent nothing
on it` and zeroes across the rest; a cap it crossed moves a ceiling rather than
a count, and is reported on the crossing lines beside these. **That is not evidence nobody looked.** The
decision itself is recorded on the work item, which is where to read whether
stopped work has been decided and what was decided; an escalated item is blocked
there as well.

A **round** is a reviewer verdict that sent a developer attempt back, counted
across every run of the item. A re-review no developer attempt produced is not
one, so a promotion that [loses its race](configuration.md#losing-a-race-for-the-target-branch)
and gets a fresh verdict on the replayed change is not charged for it, whichever
way that verdict goes. Neither is a verdict that approved the change: the cap
stops an item buying the same argument another round, and an approval ends the
argument. Neither is a repair whose whole residue is one finding the reviewer
disposed of as `out_of_scope` — the reviewer said the work is right and named one
thing beside it that is not this change's to do, which is the same ending with a
note attached; the work still goes back to the developer and still spends one of
the run's own repair attempts. The disposition decides this, not the severity: a
single `minor` finding with no disposition is a round like any other. An uncharged verdict is still
recorded rather than passed over, because the exclusions are one mechanism — an
attempt already answered about is charged at most once — and a promotion only
ever follows an approval. Rounds are what runs actually spend, and every run
records them.

The lines under it are the budget for what triage can decide about work that did
not land — another go at the change, a re-run, a re-armed merge — and they move
when [the development manager decides one](conversation.md#deciding-what-becomes-of-stopped-work).
Each is recorded before the action it counts takes effect, so a crash cannot
double-grant, and each is refused once its budget is spent. A grant and a re-run
are each once per item and are also refused by the rounds — the grant truncated
to what the cap still has room for, the re-run refused outright once none
remain — and a merge re-arm is bounded on its own because it buys no round at
all. It is the one budget here that is not the item's: what a re-arm repeats is
one merge request the reviewer's verdict already authorized, so it is bounded
once per publication, and the line above names each publication that has spent
any of it. An item that published three times has three separate budgets, and a
second drop of one publication is an escalation rather than another re-arm. The
rounds alone would bound neither of the first two on an item whose runs
keep stopping before a reviewer ever sees them. The
numbers are the `triage` keys in [the configuration
guide](configuration.md#what-one-work-item-has-been-given). An item triage
has spent more than one pass on says so in the first line, which is the fact
worth looking for: work that keeps coming back is usually work where something
other than the change is wrong.

The listing folds each reason onto one line and bounds it at 160 bytes with
an ellipsis, never cutting mid-character, so a reviewer's whole verdict does not become the listing;
`--json` carries what the record holds in full, along with the same figures.

Cost comes from the same recorded evidence [`yoyo cost`](reporting.md#what-the-work-cost)
prices from, so a run still going reports what it has spent so far, and one
whose event log no longer survives reads as `cost unknown` rather than as free.

Reading a run decides nothing about it, so this holds nothing and settles
nothing: a run another process is executing is listed exactly as a finished one
is. Reporting a failure is not itself a failure either — the exit status says
whether the records could be read, so a script can read this without guarding
against the answer.

## Watching from a browser: the dashboard

`yoyo dashboard` serves what `yoyo status` reads — the four lines, the
capacity state carried under them, what the harness is spending, and what
landed — to a browser on this machine, as one page of eight sections, and keeps
serving it until you stop it:

```sh
./bin/yoyo dashboard              # a port the operating system chooses
./bin/yoyo dashboard --port 8765  # one you can bookmark
```

The configuration's [`services.dashboard`](configuration.md#services) entry
declares the dashboard as a part of the product — its port, the address it
binds, the hosts a request may name, and where a supplied token comes from —
for the supervisor that will start it with the rest. The supervisor is here
([`yoyo start`](#starting-the-product-and-stopping-it)) and the dashboard is
not yet its child: with the entry enabled, `yoyo start` says so and names the
work that adopts it, adopting the dashboard as a supervised part
(yoyodyne-ifd.414). Until that lands, this command is
started by hand and still binds loopback and serves on `--port`, exactly as
below; the one thing it reads from the entry is `token`, and it reads that
whether or not the entry is enabled.

With `services.dashboard.token` at its `generated` default it prints two things
when it starts, and the second of them once:

```text
dashboard for yoyodyne serving at http://127.0.0.1:52341/
token: 9f2c41ab7e05…
the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing, /api/throughput, /api/spend, /api/items/<work-item-id>, and /api/program-managers/<agent>
it is printed here and nowhere else, and a restarted dashboard prints a new one; stop with ctrl-c
```

**A configured token outlives a restart.** Set `services.dashboard.token` to
`keychain` or `file` and the command reads the token from the store the
setting names — the keychain item `yoyo-dashboard.<product id>` under the
account `yoyo`, or the file `<state root>/projects/<product id>/state/dashboard.token`
— and serves under it, so a restart serves under the same token and nobody is
handed a fresh one to paste. It prints where the token was read from and never
the value:

```text
dashboard for yoyodyne serving at http://127.0.0.1:8765/
the token was read from the keychain item yoyo-dashboard.yoyodyne under the account yoyo, as services.dashboard.token names, and is not printed
the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing, /api/throughput, /api/spend, /api/items/<work-item-id>, and /api/program-managers/<agent>
it outlives a restart: a restarted dashboard reads the same one; stop with ctrl-c
```

A store that does not hold the token refuses to start, before anything is
bound, with the command that stores it — the same one
[`yoyo doctor`](#checking-the-installation) prints under `service:dashboard`:
`security add-generic-password -s yoyo-dashboard.<product id> -a yoyo -w` for
the keychain, which prompts for the token so it never reaches a shell history,
and `mkdir -p … && (umask 077 && openssl rand -hex 32 > …/dashboard.token)` for
the file. A stored token is accepted from the bearer header alone, exactly as a
generated one is. The keychain is macOS's; on another platform the `keychain`
source refuses by name and says the file is the store that platform has.

Open the URL, paste the token into the page, and the page shows where the
harness stands and asks again every ten seconds. The same answers are served as
JSON to anything that sends the token as a bearer header: at `/api/standing`,
the `standing` object `yoyo status --json` carries, from the same derivation,
so the page and the terminal cannot disagree about a number; at
`/api/throughput`, what landed over today and the last seven days, counted from
the run records `yoyo status` derives each run's outcome from; and at
`/api/spend`, what the harness spent over the last twenty-four hours and the
last seven local days, with a line for each of the last thirty local days,
priced by the same reading `yoyo status --spend 30` prints. That third reading
prices every event log a month holds, which is seconds of work, so the page asks
for it once a minute rather than every ten seconds — and it is one read of the
logs rather than two, because pricing a stream reads the whole of its log
whatever window is asked for, so the rolling day and the days around it come
off the same pass.

**Those three answers are snapshots, built in the background and never per
request.** The dashboard reads the standing every ten seconds and the
throughput and the spend every minute — each interval counted from the end of
the last build, so a build slower than its interval never runs back to back —
and answers every request with the latest reading it has, however many tabs
are asking: two tabs cost one build. Each answer carries, under `snapshot`,
when the reading was taken (`taken_at`), how old it was when served
(`age_seconds`), its interval (`interval_seconds`), and `stale` where it is
older than two intervals; the reading's own fields are beside it exactly as
before. A build that fails leaves the last reading served, with what the
failure said and when under `snapshot.failure` and `snapshot.failed_at`,
rather than a refusal; only a reading no build has ever produced is refused,
with the failure. The first request after the dashboard starts waits for the
first build, and a reading nobody has asked for in five minutes stops being
built until somebody asks again, when it is served with its age while the next
one is taken. Until yoyodyne-ifd.432.16 every poll of every tab rebuilt the
standing from the whole tracker listing and every run record: at a load average
of 117 one build took 6.4 seconds, and under more load it passed the tracker's
thirty-second timeout and the page hung or showed errors exactly when the
harness was busiest.

A fourth answer is
served one item at a time, at `/api/items/<work-item-id>`: the work item whole
— the tracker's own fields, and the run the harness last made for it as
`yoyo status <item>` lists it — which is what the page's
[card on an item](#opening-a-work-item) reads. It costs a tracker command, so
the page asks for it when a card is opened and not before, and never reads the
tracker itself. An id the tracker holds nothing under is refused as not found,
in fixed words that do not name the id back; an id that is not the tracker's
shape is refused before anything is asked. A fifth is served the same way, one
[program manager](designs/program-manager.md) instance at a time, at
`/api/program-managers/<agent>`: the instance exactly as the standing carries it
under `standing.program_managers`, and its current lane report whole beside it,
which is what the page's [card on a report](#opening-a-program-managers-report)
reads. A name the read model knows no instance under is refused as not found,
in fixed words that do not name it back, and a name that is not an agent's
shape is refused before anything is read.

### What the page presents

Eight sections, top to bottom, each drawn from the read model and from nothing
else. Above them, one banner and only one, while it stands: the same sentence
the terminal prints above the four lines when the harness is paused on the
provider's usage window, when every role is held by one, or when the provider
is answering nobody. Beside the product's name the page says when the reading
was observed, how old the dashboard's snapshot of it was when it was served —
`taken 4s ago; asks again every 10 s` — and when it asks again. It marks the
page **stale** when a reading is older than two of its intervals. For the
standing, routine lag shows only that small marker and the observed time; its
red age warning appears once the reading is five minutes old or more. The
throughput and spend age warnings still appear after two intervals. An age
warning says plainly what is old — `The reading
of the standing is 5m old, older than two of its 10s intervals: the dashboard
is taking longer than that to read it, so what is shown may not be what the
harness is doing now.` Failed builds and failed polls still get an explanation
immediately. A reading whose latest build failed shows the failure beside its
age — `The dashboard's last reading of the standing failed — bd list timed out
after 30s — so what is shown is the reading taken 34s ago.` A poll that fails
after one that succeeded, of any of the three readings, says which reading
failed and which it is still showing. The throughput and spend
sections say the same under their own figures, rather than going blank on one
dropped request.

**The page asks less often while the dashboard is slow or failing.** An answer
that fails, or takes longer than five seconds, doubles the wait before that
reading is asked for again — up to a minute for the standing and three minutes
for the throughput and the spend — and the first quick answer puts it back on
its ordinary clock. While it is backed off the page says so where it says when
it asks again: `asks again in 20s, less often while the dashboard answers
slowly or not at all`.

1. **Where the harness stands** — a tile for each of the four lines: running
   developer runs, conversations with a turn in flight, admitted items not
   startable now (out of how many are admitted, how many await a decision or
   the carrying out of one, and the ready work waiting for a developer slot),
   and what waits on the operator, which says `nothing` in words when nothing
   waits on the operator or anybody else. That last tile counts per mover, in the read
   model's own vocabulary for who each entry is waiting on: its figure is what
   waits on the operator, and beside it, out of the line's whole count — the
   figure the terminal prints — what waits on the Lead Product Manager, the
   architect, the development manager, and the harness, in the model's order
   and each named only where it is not zero. The tile asks for attention when
   something waits on the operator, not when sixty things wait on a role. Its
   label is a button that opens [the list of what is waiting](#opening-what-is-waiting-and-on-whom),
   each entry of which opens a card. Two
   more tiles carry what landed today and in the last seven days, and what was
   spent in the last twenty-four hours and the last seven days, the latter
   prefixed `≥` or `at least` where a record that should be in it could not be
   read and its label opening the same listing the spend box's does.
2. **What the harness is spending** — the box above Running now, and the answer
   to what the machine is costing right now: two columns, the last twenty-four
   hours and the last seven local days, each with what every priced invocation
   in it cost, how many there were, and the split by kind — runs, conversations,
   branch reviews, side threads, and exchanges — with the count of records that could not be
   priced named beside the figure whenever there is one, because a cost with a
   hole in it is a floor rather than a total. The first window is **rolling**:
   it is reckoned from the moment the reading was taken rather than from
   midnight, so a figure read at ten past midnight is a figure about the day
   behind you and not about ten minutes; the column says which moment. The
   second is whole local days, today counting as the first, so it is exactly the
   sum of the seven newest lines of the listing behind it. Under the two, a
   label — *Every day for the past 30 days* — opens
   [that listing](#opening-the-month-of-spend).
3. **Running now** — a card for each developer run and each conversation with a
   turn in flight: the work item's title and id, the phase (or `approved,
   resuming integration` where that is what the run is doing), how long it has
   been going, what it has spent so far or `cost unknown` and why, and the
   provider, model, and account alias it is spending. A run with
   [no process behind it](#where-the-harness-stands-the-four-lines) says `no
   process behind it` in place of the phase, with why under it, and its card
   carries a dashed rule a running card does not. The title and the id
   each open [the item's card](#opening-a-work-item). A conversation card says
   the agent, its role, how long the turn has been in flight, and how many turns
   are recorded before it.
4. **Where the work stands** — the pipeline, read left to right: admitted items;
   how many are held back — not startable now — split into the read model's
   groups by what each waits on, exactly as the terminal's `-` lines under
   [the not-startable line](#where-the-harness-stands-the-four-lines) count
   them: the development manager's decision, the harness carrying a decision
   out, a directive, a
   [step only a person can take](#recording-a-step-only-you-can-take), which is
   the operator's through `yoyo gate record`, ready work a switch or a missing session stops, other
   items, a role's conversation, parked by the Lead Product Manager, covered by
   other work, and not offered for a reason nothing here can read — each with
   its next step and whose move that is, and the largest marked `(most)`; how
   many are startable and next to be pulled — while every developer slot is
   taken, said as `45 ready, waiting for a developer slot; 3 slots, all taken`
   and never counted as held back, or, while a stall holds every pullable item,
   that the harness is choosing nothing and why; how many are running, by
   stage; and how many landed today and this week. Under it, in words, whether
   anything held back is the operator's — the model's one sentence — then how
   many things wait on the operator, and then how many wait on each other mover
   — `Needs a human: 1 thing waiting on the operator; waiting on others: the
   development manager's: 30, the harness's: 4.` Nothing a role or the harness
   moves is said to need a person. The Not startable tile counts the held-back work and names
   the slot wait beside the figure rather than in it. Every stage's label
   and every pile's label is a button that opens [the list of the items in it](#opening-a-work-item).
5. **Throughput** — two columns, today and the last seven days, each labeled
   with the local days it covers: how many runs landed their work on the target
   branch; the other endings, in the run history's own words (stopped on a
   blocker, cancelled, timed out, failed, and succeeded without promoting
   anything) — what each run ended as, never a wait it is still in, since a run
   that stopped days ago may have been decided, re-run, or landed since; and how many runs started. What those days cost is in the spend
   box above rather than here: a page carrying "today" in one section and "the
   last 24 hours" in another is a page with two cost figures a reader has to
   reconcile, and with the money moved out this section needs no pricing at all,
   which is what keeps the page at one read of the event logs rather than two.
6. **Provider capacity** — the capacity-blocked state under
   `standing.capacity_blocked`: each run parked or held on provider capacity,
   with what refused it, since when, the reset it is waiting out or that none
   was named, how much of its pause budget it has spent, whether its change is
   preserved, and what to do about it — `nothing needs doing` for a run that is
   only asleep, and the remedy for one that stopped; each conversation the
   provider is still refusing, with its model, its refusals, and its reset; and,
   when every role is held at once, a line saying so with the agents, the
   models, the alternates or the lack of them, the refusals, and the reset.
7. **Factory problems** — repeatedly failing product passes from
   `standing.factory_problems`, with the pass, its current consecutive failure
   count, the local time the failures began, the latest error, who watches it,
   and who resolves its cause. An unreadable sweep or handling log says so in
   this section. The finding ends when its own pass next carries out its work,
   finished or with more waiting, as described
   [under maintenance](#the-supervisors-maintenance-pass). A watch session
   that has been draining past its bound for ten minutes without restarting
   is listed here too, naming the session and since when, until a session
   running the deployed build takes over. One waiting out a run at its
   promotion is not listed, however long the promotion takes.
8. **Program managers** — each [program manager](designs/program-manager.md)
   instance `standing.program_managers` carries, which is the list `yoyo
   status` prints [under the four lines](#where-the-harness-stands-the-four-lines),
   by name: its lane; its status as a word in a badge — **blocked**, **stale**,
   or **working**, derived by the read model and never by the page — and,
   where the word is not `working`, why, in the terminal's words, both halves
   where an instance is stale and blocked at once; under any word, the pass it
   missed since its last completed one, in the words its `yoyo status` line
   uses, with the time in the reader's zone; when its last pass
   completed; when its report was written, or that it has written none; how
   many restart requests it has open; and a button that
   [opens its current report](#opening-a-program-managers-report).

Every section has four states and shows exactly one. **Loading** says it is
reading, and for the spend box that it is pricing the last thirty days, with one
slow pulse that stops for a reader who asked for reduced motion. **Empty** says
in a sentence that there is nothing — the harness is idle, nothing is running,
the backlog is empty, nothing ran in the last seven days, nothing was spent in
the last thirty days (and, where anything has ever been priced, how far back the
oldest priced record goes),
no run or conversation is waiting on capacity, no instance of the program
manager role is configured and none has a restart request open — because a panel with nothing in
it and a panel nobody filled look the same. **Error** says what could not be
read, in the read model's own words, and what to do: which command says the
same thing with more room, and that the page keeps asking. **Ready** is the
content above. A section whose sources could only partly be read stays ready
and lists each unreadable source under its content, and a tile or a stage
whose source could not be read shows a dash and the words `could not be read`
in the figure's place — the pipeline with the tracker unreadable still draws
what is running and what landed, and its first three stages say they could not
be read; nothing on the page ever shows a zero for a line the model did not
answer.

Every distinction survives without colour. A state is a word in a badge as
well as a tint, a problem is `Could not be read` as well as a red rule, a
waiting run and a blocked one differ in the word and in a solid against a
dashed rule, a program manager that is working, blocked, or stale differs in
the word and in a solid, a dashed, or a dotted rule, the largest pile says `(most)` as well as being bold, and the
stages are joined by an arrow character rather than by a coloured bar. The page
follows the reader's light or dark setting and their reduced-motion setting,
and holds its badges' edges under forced colours.

The words are the terminal's wherever the terminal has them — `no developer
runs`, `12m elapsed`, `$3.41 so far`, `cost unknown (its event log is gone)`,
the refusal each item carries, the remedy each parked run carries — because the
page and `yoyo status` are two projections of one model and a reader moving
between them should not have to translate.

### Opening the month of spend

The spend box's label — *Every day for the past 30 days* — and the band's
**Cost** tile each open one listing, in the same pop-up the groupings below
open in and with the same four states: one line per local day, newest first,
with what that day cost, how many invocations it was, and the split by kind.
Its empty state is a month in which nothing was spent, said in the box's own
sentence rather than as thirty lines of `nothing spent`; its error state is the
reason the spend could not be read; and it is loading while the month is still
being priced.

Three things it says rather than leaving to be read off a zero. **A day no
priced record goes back to says so** — `no priced record reaches this far back`
— because a day nobody measured and a day nothing was spent on are different
answers and only one of them is zero; a day inside the reach with nothing on it
reads `nothing spent`. **Spend whose moment could not be read** is on a line of
its own at the foot, saying it is counted in every window of the box above and
on no day here, which is the only reason the days and the windows can differ.
And **records that could not be priced at all** are counted on a last line
saying every figure here and above is a floor.

Nothing in the listing opens anything: a day is not a record this page can show
more of. It is drawn again on every poll while it is open, from the reading the
page already holds, so it stays as live as the box it was opened from.

### Opening a work item

The sections show counts and names; the items behind them open in two pop-ups,
each a dialog over the page that closes on its **Close** button, on a click
outside it, or on Escape, and puts focus back on what opened it — on the
button now carrying that item or grouping where a poll has redrawn the page
in between.

**A grouping's items.** In *Where the work stands*, the label of each stage —
Admitted, Held back, Startable, Running, Landed — and of each pile under one
(`held after a stopped run`, `developing`, and the rest; the week's landed line is a
grouping of its own beside today's) ends in a chevron and opens a list of the
work items in it, by title, with the id under each and the pipeline's own word
for it beside: the refusal for a held-back item — for one held after a stopped
run, opened by since when it has been held and how long ago that was, in the
reader's zone, as `yoyo status` says it, and listed oldest hold first — the
phase and elapsed time for
a running one, when it landed for a landed one. The list is read from the
readings the page already holds — the standing names the admitted, startable,
and refused items and the throughput names the landed runs, so it is what the
figure counted rather than a list assembled on the page — and is drawn again
on every poll while it is open. It has the four states a section has:
**loading** while the landed figure is still being read, **empty** saying in
a sentence that no item is in the grouping, **error** with the reason the
grouping's source could not be read — a stage showing a dash still opens, so
the reason is readable in full — and **ready**. Each title in it opens the
item's card, over the list.

**One item's card.** Opened from a running item's title or id, or from any
entry of a grouping, the card shows the item whole under plain labels: **Id**,
**Title**, **Status**, **Priority** (bd's `P0` to `P4`, 0 the most urgent),
**Labels**, **Parent**, **Description**, **Design**, **Acceptance criteria**,
**Notes**, and **Run**. A field the item has nothing in says `none`, so a blank
is never mistaken for a field the page did not read, and prose keeps its line
breaks. **Run** is the run the harness last made for the item, in the words
`yoyo status <item>` lists it in: `in flight — developing, 12m elapsed, $3.41
so far` for one still going; `preserved: stopped, reviewing — work preserved,
checked` for one that ended with its change still on a branch or in a checkout;
and otherwise that nothing is in flight or preserved and what the latest run
came to, `work gone, checked` or `no artifacts recorded`. What survives is
looked for in the repository as the card is read, exactly as `yoyo status` looks
for it, rather than read off the run's removal flags; a look that could not be
made says `not checked` and why. Under the line are the run's id
and when it started and ended, its cost or `cost unknown` and why, the reason it
gave for ending, and the branch, worktree, and developer session it preserved.
An item never run says `none is recorded`; run records that could not be
opened say why in the run's place rather than the card showing an item nothing
ever touched. The card is read once, when it is opened, from
`/api/items/<id>`; it has the same four states, and its **empty** state is the
tracker holding nothing under the id — an item closed or removed since the
page last read the standing — which is a different answer from the item not
being readable, and is said as one. Every value on it reaches the page as JSON
and is written as text, under the same policy as the rest of the page.

### Opening what is waiting, and on whom

The **Needs a human** tile counts; its label opens the list of everything the
fourth line carries, the operator's and every other mover's, headed *What is
waiting, and on whom* — not *Needs a human*, because most of what it lists is
a role's or the harness's to move, and each entry of the list opens a card, in the same two pop-ups as
above. Both are drawn from [the structured entries the standing
carries](#where-the-harness-stands-the-four-lines) under
`standing.needs_human` and from nothing else — the page never reads the
tracker or the amendment store for them — and both are drawn again on every
poll while they are open, so they stay as live as the tile.

**The list.** Each entry is the thing waiting, in the sentence the terminal
prints for it, with its `kind` under it and the terminal's second sentence
beside — who it is waiting on and what settles it, `the architect's — nothing
reaches the document until they or the operator decide it` — in the movers'
order, the operator's first, so the list opens on what the tile's figure
counted; within one mover the entries are in the terminal's order. It has the
four states the grouping list has: **empty**
saying `Nothing waits on the operator or anybody else.`, **error** with the reason the line could
not be read — the tile showing a dash still opens it, so the reason is
readable in full — **loading** while the standing has not arrived, and
**ready**. Each sentence is a button that opens the entry's card.

**An entry's card.** Headed by what sort of thing it is, in plain words — *A
change proposed to a document*, *A run that still owes a step*, *A work item
carried by a conversation* — with the entry's key and when the standing was
read under the heading. Every card opens with the terminal's two sentences,
**What** and **Waiting on**, then the entry's **Kind** and **Mover**, and
then the record the kind names, whole, under plain labels:

- an amendment's card shows the **Document**, its **Document kind** and
  **Owner**, who it was **Proposed by** (the role, and the agent), **In run**
  which run, **Working on** which item, the **Proposed change** and **Why** in
  full with their line breaks kept, when it was **Raised**, and its **Id**;
- an owed step's card shows the **Run**, its **Work item**, and where it
  stopped — **Ended** with the run's recorded status, and its **Phase** — and
  the command that settles it is in the *Waiting on* sentence above them,
  `yoyo reconcile`;
- a conversation-carried item's card shows the **Work item**, its **Executor**
  marker, and the **Role** the marker names;
- the other kinds — a publication, a degraded service, a hold, a directive, an
  outage, a stall, the report pile, held work, a step reserved for a person — show their own record's fields
  the same way.

A work item the entry names is a button on the card that opens the item's own
card in its place, over the list; closing that puts focus back on the list
entry that opened the entry card. The card has the four states: **ready**;
**empty** when a poll finds the entry gone from the line — settled since the
card was opened — which it says in a sentence with the key it was opened on;
**error** when a poll finds the line unreadable; and **loading** while the
standing has not arrived. Every value on it reaches the page as JSON and is
written as text, and no button on it does anything but open or close a pop-up:
acting on an entry from the page is its own item (`yoyodyne-ifd.432.8`),
behind the architect's ruling on whose identity the page would act as.

### Opening a program manager's report

Each row of the program managers section ends in a button that opens the
instance's current [lane report](conversation.md#a-program-managers-lane-report)
on a card of its own, in a pop-up that closes like the others and puts focus
back on the button that opened it. The card is read once, when it is opened,
from `/api/program-managers/<agent>`, and shows, as text under plain labels:
**Status**, the badge with why beside it; **Lane**; the **Missed pass**, where
the instance has one since its last completed pass — which trigger owed it and
whether no pass followed or it was cancelled before it completed, when it fell
due or was taken and when the harness recorded it missed, the record's account
of the cause, and that it is the harness's move, which the next completed pass
ends — or that there is none; the report's **Summary**
with its line breaks kept, and what it says is **Remaining**; the
**Blockers** the record bears out, each with what is blocked, whose move it is,
what it cites, and which open record of the instance's own that citation
resolved to; the blockers it names that the record does not bear out, under
**Not blockers**, each with the reason it blocks nothing; when the report was
**Written**, which version it is, and the pass — or the operator's own turn —
and the conversation turn that wrote it; the **Last pass** that completed; and
the open **Restart requests**, each with the part, the reason, and when it was
asked, and that the supervisor's
[maintenance pass](#the-supervisors-maintenance-pass) answers each at its next
pass. That is everything the report holds, so the card ends there and names
no file: where the report is kept is in the answer's `instance.report_path`,
for a program that reads it. An instance that has written no report says so in
the summary's place. The instance is the one the
standing carries, from the same derivation, and the summary shown is the
version its blockers were read from, so the card, the row, and `yoyo status`
cannot disagree about it. The card has the four states: **loading** while it is
read; **empty** when the read model knows no instance by the name — one taken
out of the configuration since the page last read the standing; **error** with
the reason the state could not be read; and **ready**. What the model could not
read behind a ready card is said on it under **Could not be read**.

**Seeing every state without a harness behind it.** `internal/dashboard/testdata/renders`
holds the page as its own script renders it from the fixtures under
`internal/dashboard/testdata/fixtures` — the document as the script left it,
keeping the one page state and the one state per section a browser would show
and dropping the hidden ones — one file per scenario — `quiet`, `busy`,
`held`, `roles` (nothing waiting on the operator, everything on a role or the harness), `degraded`, `unreadable`, `loading`, `throughput-pending`,
`throughput-refused`, `throughput-stale`, `spend-pending`, `spend-stale`,
`refused`, `unreachable`,
`wrong-token`, `stale`, `snapshot`, `snapshot-old`, `snapshot-failed`, and
`signin` for the page, `spend-days`,
`spend-days-empty`, `spend-days-error`, and `spend-days-loading` for the month
behind the spend box, and `card`, `card-loading`,
`card-missing`, `card-refused`, `grouping`, `grouping-landed`,
`grouping-empty`, `grouping-error`, `grouping-loading`, `grouping-card`,
`closed`, and `closed-after-poll` for the pop-ups on work items, and
`attention`, `attention-empty`, `attention-error`, `attention-amendment`,
`attention-owed-step`, `attention-carried-item`,
`attention-carried-item-card`, `attention-settled`, `attention-unreadable`,
and `attention-closed` for the list of what is waiting, and on whom, and the cards
opened from it, and `report`, `report-blocked`, `report-unwritten`,
`report-loading`, `report-missing`, `report-refused`, and `report-closed` for
a program manager's report card, each opened by clicking what a reader would click on
one of the pages and holding the pop-ups alone, over the page render it names
— which together show every section and each pop-up in each of its four
states. The `busy` page lists a program manager in each of the three
statuses, `quiet` shows that section empty, `unreadable` in its error, and
`degraded` ready with what could not be read listed under it. They are golden files:
`TestThePageRendersEverySectionInEveryState` runs the page's script under Node
against the fixtures, checks that each section and each pop-up reaches each
state and that the fixtures' words land on the page as text, and fails when a
render differs from what is recorded; `go test ./internal/dashboard -run TestThePageRendersEverySectionInEveryState -update-renders`
rewrites them after a deliberate change. Each render opens in a browser beside
the real stylesheet. To look at the live page in each state, with the real
server and the real policy in front of it, `go run ./internal/dashboard/fixtureserver`
serves one dashboard per scenario on a loopback port of its own and prints each
URL with its token.

**It is a projection and nothing else.** It reads the same durable records the
terminal reads and no request writes any of them. It writes two records as it
starts: when each program manager instance was
[first seen in the configuration](#where-the-harness-stands-the-four-lines),
which `yoyo status` and the Slack sink write too, so an instance the scheduler
never woke can read stale; and its own build and the configuration keys that
build reads, under `projects/<product>/state/config-readers/`, so a later key it
cannot read is named against the dashboard. Both records direct no work. The
only form is the one that takes the token, every button on the page opens or
closes one of its own pop-ups and
nothing else, and nothing but `GET` and `HEAD` is answered at all. Restarting it
changes nothing about the harness and loses nothing, because the history it
shows lives in the records rather than in the page. It is not a second control
plane, and work is still directed from the conversation and the commands above.

**What it will not do** is the part worth reading before leaving it running:

- **It answers only on this machine.** It binds `127.0.0.1` and nothing else,
  so nothing off the machine can reach it, and being on the machine is not
  enough on its own: every request for the read model has to carry the token.
  What is served without one is the page shell and its own script and
  stylesheet — static text compiled into the binary, with nothing of the read
  model in it, which a browser needs before it can present a token at all.
  Everything that reads state is behind the token.
- **The token is never in a URL, and never in a cookie.** A token in a URL
  reaches the browser's history, the referrer of every link on the page, and
  every log a proxy keeps, which is why the URL it prints carries none and the
  page asks for it instead. A cookie would be worse than it looks: browsers key
  cookies on the host and not the port, so a cookie on `127.0.0.1` is sent to
  every other service on every other port of `127.0.0.1` the browser visits,
  and two dashboards for two products would overwrite each other's. So the page
  keeps the token in the tab's session storage, which is scoped to the origin
  with its port, and sends it as `Authorization: Bearer` on each fetch; the
  server accepts it from that header and from nowhere else. Session storage
  ends with the tab, so a new tab asks again. A restarted dashboard under the
  `generated` source generates a new token, so a bookmark outlives the token
  and the page simply asks again; under `keychain` or `file` it reads the same
  one, and what the page asks for after a restart is the token it already had.
- **It refuses anything that did not come from its own address.** A request
  whose `Host` is not the address it bound — a name somebody pointed at
  loopback — and a request whose `Origin` is some other page scripting calls
  at the port are both refused before anything is served, the shell included.
- **It loads nothing from anywhere else.** Every response carries a
  content-security policy that allows script and style from this origin only:
  no CDN, no inline script, and no framing by another page. A value that reached
  the page unescaped would have nowhere to run, and none does: the product's own
  name is the one value the server writes into the page, escaped, and everything
  the read model says — work-item titles, an item's description and notes,
  refusals, the reason a source could not be read — reaches the page as JSON
  and is written by the page as text.
- **Every failure is a refusal or said beside the last good answer, never a
  partial one.** No token, the wrong token, a foreign `Host` or `Origin`, a
  standing, throughput, or spend no build has ever produced, and a work item or
  program manager report that cannot be read each get a status and a one-line
  reason, and nothing of the read model beside it; the page shows that reason
  in its error state and keeps asking. A build of the standing, the throughput,
  or the spend that fails after one succeeded is not a refusal: the answer is
  the last good reading, whole, with the failure and when it happened under
  `snapshot.failure` and `snapshot.failed_at` beside that reading's age, and
  the page marks itself **stale** and names the failure. What the
  read model could read with one source missing is a different thing, and is
  said inside the answer line by line — the page says that line could not be
  read in place of its count, as the terminal does, and lists the reason under
  the counts, rather than counting an unreadable line as empty.

`internal/dashboard`'s tests drive each of those refusals from the outside and
are the evidence a reviewer is handed for the conventions; the
[observability-and-dashboard design](designs/observability-and-dashboard.md)
is where they are established, as the repository's first web-service
conventions. The same tests hold the page to them: the shell, the script, and
the stylesheet carry no inline script, no inline style, and nothing loaded from
anywhere but this origin, and the renders above are made by a driver that
refuses a render in which the script set a style or sent the token anywhere but
as a bearer to this origin.

## Following a run, a conversation, or a branch review

`yoyo status --follow` follows the normalized event stream a run, a
conversation, or a [branch review](work.md#reviewing-what-a-branch-adds-up-to)
records, which is the closest thing there is to watching an agent work. It is
the other half of the verb above: the recorded mode reads back what the records
hold now — a run still in flight as readily as one that finished — and these
modes follow a run's events as they arrive, list what has been recorded lately,
and price it. They ship with the binary, so `go install` and a release download
carry them; there is nothing to clone. Until yoyodyne-ifd.63 this was
`bin/yoyo-status`, a shell script that lived only in a checkout of this
repository, which for anybody who had never seen the internals meant the surface
did not exist. The script is retired rather than kept as a wrapper: a wrapper
would have to be installed to be useful, which is the gap the fold closes, and
kept in the checkout it would be a second copy of every sentence the verb says,
drifting from it — its banner had already drifted from the harness's own wording
once. Nothing it did is missing from the verb, and two things it could not do are
here: it needs no `jq`, and it prices a failed invocation, which cost money like
any other. There is nothing at the old path either, on purpose: the script's
flags were its own — `-L` for following the newest, `-l` to list, `-c` for
spend, `-n` for the replay — and a one-line `exec yoyo status "$@"` under the
old name would have turned every one of them into an unknown-flag refusal and a
bare `bin/yoyo-status` into the recorded report instead of the follow it always
was, which is the old name answering a different question rather than muscle
memory kept. So `bin/yoyo-status -L` is `yoyo status --follow --latest` now,
`-l` is `--list`, `-c` is `--spend`, `-n` is `--lines`, and `--runs`, `--chats`,
and `--reviews` are `--kind runs`, `--kind chats`, and `--kind reviews`.
[The retirement's own record](diagnoses/yoyodyne-ifd-239-yoyo-status-already-retired.md)
says where each of those is pinned in the binary.

```sh
yoyo status --follow             # follow the newest of any kind
yoyo status --follow --latest    # follow the newest, and move on when a later one starts
yoyo status --follow 40b68275    # follow one by id or unique id prefix
yoyo status --events             # print the newest stream's recent events and exit
yoyo status --list               # list recent runs, conversations, and reviews and exit
yoyo status --spend              # report the last 7 days of spend, by day and in total
yoyo status --spend 30           # report that many days instead of 7
yoyo status --spend 40b68275     # report spend for one run, conversation, review, or exchange, any day
yoyo status --shipped            # the 10 most recently shipped items: cost, elapsed, paused, runs
yoyo status --shipped 25         # that many instead of 10; 0 lists every item that shipped
```

A conversation and a branch review each record the same kind of event stream a
run does, and "is this alive" is the same question asked of all three, so every
mode covers all of them and the default never asks which kind you meant.
Selecting one by id or by a unique id prefix works the same for each. `--kind
runs`, `--kind chats`, `--kind reviews`, and `--kind sides` narrow it to one kind
when that is what you want. `--lines` says how many recorded events to replay before
following, fifty by default and `0` for the whole log; `--raw` emits each event
exactly as it was recorded, and `--all` keeps the thinking-token pings the
default leaves out. An option that belongs to the other half of the verb —
`--failed`, `--limit` outside a listing, `--lines` outside a follow — is refused
rather than ignored, because a narrowing silently dropped reads as an answer to
the question that was asked.

A side thread — a bounded conversation an agent holds beside its main one —
records an event stream of its own under `sidestreams/`, so it is listed,
followed, and priced like the other three. Its terminals are priced by the same
reader as a run's and a conversation's, and every spend row it contributes names
the conversation it was opened beside (`open, beside chat-…`), which is whose the
money was; `yoyo cost` carries the side threads into its total on a `SIDE
THREADS` row, with each one's cost listed under the table against that
conversation.

A [recurring task](configuration.md#recurring-tasks)'s passes are turns of the
role's own conversation, so their cost is already in the conversations figure.
What the spend report adds under its totals, whenever it covers conversations
and names nothing in particular, is that figure's recurring part: each task's
passes, turns, and cost by the model the passes ran on, read from the passes'
own records over the same window. It is a split rather than an addition, and it
is how a sweep moved onto a cheaper model with
[`model`](configuration.md#a-tasks-own-model) is told from the decisions beside
it on the role's own. The JSON carries it as `sweeps`.

An [exchange](conversation.md#roles-asking-each-other-things) is priced beside the
streams and is the only thing that is never followed: its record is the thread itself,
revised as it goes, rather than a stream of events, so it appears in the spend
report and in no other mode. Naming one by id prices it like anything else,
`--kind exchanges` prices them alone, and narrowing to any of the followed
kinds narrows the exchanges out along with the kinds it excludes — somebody who
asked what the runs cost is asking about the runs.

A run's listed status is the status it recorded. A conversation has no such
record of its own, so its status is derived and says what an operator is
actually asking: `answering` while an agent is working on a turn, `waiting`
between turns, and `ended` once the role has moved on to a later conversation.
Whether a turn is in flight is read from the same observed hold the four lines'
Working line reads — the process holding the conversation writes down which
process it is, and the listing checks that it is still there — rather than from
the event log, which cannot tell a turn in flight from one whose process died
before it wrote a terminal; so `yoyo status` and `yoyo status --list` cannot
disagree about the same conversation. A
branch review has no state file either — its verdicts share one log rather than
having a record each — so its status comes from its own events: `reviewing`
while the verdict is being made, and `reviewed` once it has been. A side
thread's status is read from its own record: `open` while its agent holds it,
and the outcome it ended with — `concluded` or `spent-its-budget` — once it has
ended.

Every live mode leads with a PAUSED banner while
[activity is paused](#pausing-everything-and-resuming-it), naming when the pause
was placed, and an INTAKE HELD banner while intake is held, naming who held it
and why: a quiet machine somebody paused and a quiet machine that died look
identical, and this is the one place an operator is already looking. The banners
go to standard error, so `--json` on standard output stays machine-readable and
carries both holds as fields instead. The recorded mode carries the same two
switches on its "Needs a human" line.

A listing chooses from the directory and opens only the logs it prints — the
newest twenty by default, one for `--follow --latest`'s look every few seconds —
so a state directory holding hundreds of streams is not read through to print
a screenful. It resolves the state directory the same way every other verb
does, so it keeps working under `YOYODYNE_STATE_HOME`, a machine's
`state_root`, or `XDG_STATE_HOME`, and
an empty answer
names the directory it read and the kinds it was asked about — a machine with
fifty runs and no branch reviews is told no branch reviews are recorded, never
that nothing is. `yoyo status --help` lists the rest of the options. What
`--spend` prices is every run, every conversation, every branch review, and
every exchange, and a mixed total says how much of it was each — a conversation
turn, a branch review, and a round of one role asking another something are each
a provider invocation like any other, and leaving any of them out understated
every total it belonged in. An exchange counts each round on the day it was
answered, so a thread that ran over two days lands in both of their totals; its
record keeps what the provider charged and not what it used, so its rows say
nothing in the token columns rather than saying none. An exchange record that
cannot be read is counted and named under the report rather than dropped: every
exchange beside it is still priced, and the total it is missing from is marked
`≥`. [`yoyo cost`](reporting.md#what-the-work-cost) answers the same way for the
same record — it counts it in the ask row's `unpriced` column, prices the rest,
and marks its own total — because two surfaces disagreeing about what an unknown
figure is would be a disagreement only you could settle.

The rows are grouped by the local-timezone day the money was spent on, each
day's group closing with that day's spend and today's group coming last: what an
operator budgets against is what today cost, and the day they mean is the one
their own clock is keeping. What counts on a day is each invocation rather than
the log it was recorded in, so a conversation that has been open for a fortnight
appears under today for the turn it was asked this morning and under each
earlier day it spent on — one row per day it spent, each with the shape a row
has always had. A report covers the last seven such days, today counting as the
first of them. A number asks for a different count — `--spend 30` — and naming a
run, a conversation, a review, or an exchange prices that one whatever day it
ran on, because an id has already chosen what to show; an id prefix that is all
digits has to carry its `run-`, `chat-`, `review-`, or `exchange-` prefix to be
read as an id rather than as a count of days. A window with nothing in it says so
and says since when, rather than reading like a machine that spent nothing.
`--json` carries the same rows and the same window, so a script reads the figures
rather than the columns.

Under the total, a table per role says what each role's invocations paid to
write the provider's cache and what they paid to read it, beside the tokens
each way and the role's cache-read share. The one share on the `tokens:` line
above it is decided by whichever role reads the most — a developer session
re-reading its own conversation — and hides a role that writes its whole prompt
into the cache at the write premium and reads none of it back, which is what
every review did before yoyodyne-ifd.205 and what the table exists to show. The
dollars are the report's own apportioning of each invocation's reported cost
across what it was billed for, at the provider's rate multiples (fresh 1×,
cache read 0.1×, five-minute write 1.25×, one-hour write 2×, output 5×), so a
role's parts add up to the provider's figure and no price of the harness's
enters it; a model priced off those multiples shifts a role's split and never
its total. `--json` carries the same split on every row, under `roles`.
[`docs/diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md`](diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md)
is the measurement the table was built for.

[`yoyo cost`](reporting.md#what-the-work-cost) is the same run spending grouped by the work
item the runs were for, which is what answers "what did that piece of work
cost"; it leaves conversations and branch reviews out of the *items*, deliberately
and for the same reason — a conversation that discussed five items, and a review
of a branch that carried a dozen, cannot be attributed to any one of them. What it
does not leave out of its total is what the roles spent asking each other, which
sits on a row of its own above it.

### What shipped lately, and what it took

`yoyo status --shipped` is the same per-item join read for the items whose work
the harness promoted, most recent promotion first, with the wall clock beside
the money:

```text
item                     shipped                  cost   elapsed    paused  runs  title
yoyodyne-ifd.239         09 20 2026 14:26       $26.00    19d01h      none     2  bin/yoyo-status retires: deleted or a one-line wrapper for muscle memory
yoyodyne-ifd.366         09 20 2026 11:33       $42.05    51m08s      none     1  Conversation-side tracker writes retry like the pipeline's
yoyodyne-ifd.12          08 17 2026 23:58     ≥ $4.50     4h02m     3h01m     3  Pause on a provider usage limit
----------------------------------------------------------------------------------
TOTAL (3 of 402)                             ≥ $72.55    19d05h     3h01m     6
```

Ten items unless a number says otherwise, and `0` lists every one; `--limit`
bounds it too, because it is a listing. An item is shipped when a run of it
recorded promoting its work — the durable evidence a
[promotion](work.md#publishing-and-the-merge-that-follows-it) leaves on the
run — so an item closed as evidence, or closed by
hand, is not in it, and the tracker is not consulted at all: the ledger answers
from the run records wherever they are. `--json` carries the same rows, each
with the whole of its price join under `price`.

**The cost is the item's, not the run's.** It is what `yoyo cost` reports for
the same item: every run made for it at the provider's own figure, the
rejected attempt and the repair attempts included, and a run with no surviving
record to price makes it a floor marked `≥` rather than a lower number. There is
one join, in `internal/runstate`, and both verbs read it; the product manager
settled that on yoyodyne-ifd.51 so one load-bearing number could not exist in
two implementations that drift.

**The wall clock is two numbers, on purpose.** `elapsed` is first claim to
promotion — the earliest run's recorded claim, or its start where the run
predates claims being recorded, to when the promoting run recorded completing —
and it includes every hour of rework and every hour parked, because that is how
long the item took. `paused` is the parked part on its own: what the item's
runs spent waiting out a provider usage limit or the operator's hold, summed
across every run including the ones nothing survives to price, since a wait is
read from a run's own record rather than from its log. They are beside each
other rather than netted because a four-hour item that spent three of them
parked on the night's usage window is slow in a different way from one that
worked for four, and with concurrent runs pausing on shared windows an
undifferentiated figure would report waiting as slowness. An item first
attempted one week and shipped the next reads as a fortnight, which is the
honest answer to how long it took.

**An unknown is said to be unknown.** A promoting run that has not recorded
completing — still cleaning up, or dead in it — says `unknown` under elapsed and
makes the total elapsed a floor; a run recorded before titles were carried
leaves the title as "(no run recorded the title)"; neither is ever reported as
nothing. Every moment here is one the records already keep — the runs' started,
claimed, and completed times, and the wait seconds each run commits as it
waits — so nothing is bookkept for this ledger alone.

The table closes the way `--spend`'s does — a rule, a TOTAL row, and what the
figures mean under it — and `yoyo cost`'s ledger closes the same way, because
the operator reads all three and asked for one shape across them. The
conversation's `/status` is untouched: it covers work in flight, and this covers
work that is done.

The Go tests in `internal/cli` and `internal/runstate` check these claims against
a fabricated state directory holding runs, conversations, branch reviews, and
exchanges, without a provider or a repository and without reading your real
state, so the verb is held to them by `make test` like everything else in the
repository.

## Reading what the recurring tasks found

A [recurring task](configuration.md#recurring-tasks) wakes a role on a cadence to
look at its own domain. Nobody is watching those turns, so each firing ends in a
durable report, and `yoyo sweeps` is where they are read:

```sh
yoyo sweeps                                  # the 20 most recent passes, newest first
yoyo sweeps --limit 200                      # read further back
yoyo sweeps --limit 0                        # every pass recorded
yoyo sweeps --task development-manager-sweep # one task's passes
yoyo sweeps --json                           # the whole log, machine-readable
```

**The default is twenty passes, which for an hourly task is under a day.** That
is a bound on what fits a terminal rather than on what the log holds, and it is
worth knowing which you are looking at: the question these reports exist to
answer — whether a week of fixes filed root-cause work, or quietly repaired the
same thing seven times — needs the week. `--limit` widens it, `--limit 0` reads
all of it, and a listing showing part of the pile says so and says how to see the
rest. `--json` is never bounded and always carries the whole log.

It is read-only. A sweep is written once and never revised, and nothing here
fires one, retires one, or decides anything about what a pass found.

**A reply without its closing report is asked once for the block alone.** The
harness quotes the block's shape in the same conversation and on the same pass,
without asking the role to repeat its work. A valid second reply records one
ordinary pass with its findings. Both replies' costs and every action and write
already recorded remain part of that pass; the request spends no work-item
budget and is outside the pass's work-turn bound. A reply that already carries
the block is never asked again, and a pass gets at most one such request, even
when it takes several work turns.

If the report is still missing, the listing marks the pass **FAILED PASS**:
its findings remain unrecorded, its writes stand, and it counts toward the
repeated-failure finding watched by the factory-flow program manager and
resolved by the development manager. A program manager's failed pass also keeps
its event cursor where it was. After
`execution.missing_reports_before_fresh_conversation` consecutive passes still
omit the block — three by default — the next pass opens a fresh conversation
for that role or program manager instance, with its normal memory and briefing.
The pass records the previous and new conversation and the reason for replacing
it, and the listing says so. The earlier conversation's durable records remain
available. A recovered report ends the count; a conversation already replaced
by another task or by the operator is reused.

Each pass's header names the model its turns ran on — the task's own
[`model`](configuration.md#a-tasks-own-model) where it names one, the role's
configured model where it does not, and the alternate where a failover answered
— and `--json` carries it as `model`. A pass recorded before passes named their
model, or one that took no turn, has none.

Under the header a pass says how long it stood due before it was taken —
`fell due at 2026-09-29T16:43:47Z and waited 1h1m0s before it was taken` — and
`--json` carries the due time as `due_at`. A pass waits while its role's
conversation is taking another pass's turns, or while every firing a session
takes at once is in flight; firings of different roles are taken side by side,
so a wait of more than a poll or two on a busy cadence is the thing to look at.
A task's first pass, a summoned pass, and one recorded before passes carried it
say nothing about waiting.

Each entry leads with **the questions the pass could not settle itself**, because
that is the one part of a report that asks for anything: a report with no
questions needs no attention, which is what makes reading these at leisure
possible. Below them come the pass's summary and what it found, each finding with
what the role did about it — `fixed`, `filed`, `consulted`, or `left` — and the
work filed for its root cause.

**A fix that filed nothing is named as one.** That is the whole of what a week of
these reports is read for: a repair that leaves its cause in place is a repair
the next pass makes again, and a listing that could not tell the two apart could
not show it either way.

**A program manager instance's passes are here too**, under the instance's
name — `yoyo sweeps --task reliability-pm` — because each is a recurring-task
firing. A pass its events woke says what it was handed under its header,
`carried 1 landing, 30 admissions since its last pass`, and `--json` carries the
counts by class as `events`. A pass that failed says its cursor was not moved,
and the next pass of the instance carries the same events.

**A pass that failed keeps every memory and lane-report write it made before
it failed.** Each one is saved in its store the moment it is made, and a turn
failing afterwards undoes none of them. The pass's record says which writes
stood and what failed after them — `turn 2 of the recurring task factory-watch
failed, …; before that failure the pass of factory-watch saved 2 write(s),
which stand and were not undone: memory "line-stalls-at-review" (remember,
revision 1), lane report version 1` — and `--json` carries the record as
`failed`, with the writes under `saved`, each as its `kind`, `action`, `memory`,
and `revision`. The pass run after it, which is handed the same events, is told
in its message which memories and which lane report versions the failed pass
already saved, so it does not write them twice. That holds for every role that
keeps a memory, on a recurring task's pass as on a program manager instance's.
It is told of every pass since the last one that completed, so two failures in a
row are both named, and once a pass completes the one after it is told nothing
of them.
[A program manager instance's passes](configuration.md#a-program-manager-instances-passes)
says what wakes one.

**A pass whose findings left no trace is marked `UNTRACED`.** A finding has to
leave a memory written, a lane report changed, a report filed, or work admitted
on the same pass, because the account here is not something the role reads back
and its conversation is compacted. A pass that reported findings of the role's
own and left none of the four says so under its findings, and `--json` carries
`untraced` with the traces the harness counted: `saved`, `reports_filed`, and
`admitted`. The task's next pass that takes a turn is told the findings in its
message and asked to leave the trace then; until that pass the untraced pass is
an `untraced-pass` entry on [the "Needs a human"
line](#where-the-harness-stands-the-four-lines), under the head for the role
whose pass it was, and never among the operator's.
[What comes back to you](reporting.md#whether-the-pile-is-draining) says why.

**Some findings on a development manager's pass are the harness's own.** Beside
what the role reported, the harness lists the forge's open pull requests on every
firing of that role's task and states each one held open for nothing: a request
whose work item is closed, or whose head branch its target branch already
contains. Those findings are always `left`, because noticing is all the harness
does — it closes nothing — and each request is stated once, on the first pass
that finds it, rather than once an hour. The `--json` form carries them a second
time as `pull_requests` on the record, by number, which is what the next pass
reads to know what was already said. [Recurring
tasks](configuration.md#recurring-tasks) says when the reading is taken.

**A development manager's pass carries what waits on you, beside her docket.**
Every firing of her task, scheduled or summoned by the brake, has two sections
in the message that wakes her: the triage docket, and `Waiting on the operator`
— every entry on [the "Needs a human" line](#where-the-harness-stands-the-four-lines)
whose move is yours, read from the same standing `yoyo status` reads. Each entry
gives its kind and the record it is about, what it says, since when in this
machine's zone, and how long ago — `[owed-step run-…, item …] … — since
2026-09-28 09:40 PDT, 3 hours ago`. Each is dated from its own record: a hold
from when it was placed, a finding from when it was recorded, an owed step from
when its run ended, a publication from the drop where the forge dropped its
merge and otherwise from when its run ended, and so on. Two kinds carry no
moment on this line, and say `since when is not recorded on it`: work marked
for a conversation and work waiting on a step only a person can take, because
what the line carries of each is the work item and its marker, which hold no
time. The
section lists at most twenty and counts the rest, and says so when the line
could not be read whole. Until yoyodyne-ifd.430.31 her sweep was told to check
what appears to wait on you without needing you, and was shown only her own
docket: owed steps, other roles' escalations, unresolved directives, and
publications named as yours were out of her sight. The section asks what her
prompt asks: only a change to the fundamental goals is truly yours; each other
entry she settles if it is hers or sends to the role that owns it, files a
defect with the Lead Product Manager saying why it reached you, and records
what she did on the record the entry is about — a note on the work item, her
decision on the run's stoppage, or, where she can write to neither, a finding
on her pass naming the entry.

**A pass of a role that owns documents carries what it recommended on the
changes proposed to them.** The harness puts the undecided proposals against
the role's documents in front of it on every firing — oldest first, at most ten
a pass, and never one the role already argued on an earlier pass while the
operator has not decided it — and the account carries a recommendation for each:
`approve`, `decline`, or `merge` with another, with the reason. The listing
shows them after the findings, as `> recommends approve for <id>` with the
reason under it. They are recommendations and never decisions: nothing on a
sweep record changes a document or settles a proposal, `yoyo amendment approve`
and `yoyo amendment decline` record each decision under the owner's authority,
and each pass's batch is [a finding that needs your
hand](#where-a-finding-that-needs-your-hand-goes): sent to you once and named by
[`yoyo status`](#where-the-harness-stands-the-four-lines) while it waits on you —
derived from these records, and dropping any proposal you have decided since. [Working the amendment queue on a
cadence](configuration.md#working-the-amendment-queue-on-a-cadence) says how
the pass is configured.

These outcomes look similar in a listing and are not the same thing:

- **A pass that found nothing** shows its own summary and no findings. On a
  healthy harness that is most of them, and a run of passes that keeps finding
  things is itself a signal about the harness rather than about the sweep.
- **A pass that produced no account** says so and names what stopped it — a
  turn the provider failed, or a role that still omitted the closing block after
  the harness's one request for it. It is recorded as failed and never shown as
  a quiet or completed pass.
- **A missed cadence** is shown the same way: a pass that took no turn, starting
  when the task fell due and ending when the miss was noticed, and naming what
  kept the task from firing, under a `MISSED PASS` line naming the trigger that
  owed it. It is recorded once a task has gone a whole interval
  unfired; see [recurring tasks](configuration.md#recurring-tasks) for which
  causes are also reported to the operator. **A program manager instance's
  passes are covered by the same detection**, keyed by the instance and the
  trigger: its schedule is missed once it has gone a whole `every` unfired, as a
  task's is, and its events once a wake past its cursor has stood takeable for
  half an hour with no pass taken — both recorded under the instance's name,
  with the same causes, reported the same way. So is a pass that was **taken and
  cancelled before it completed**: one whose session was stopped under its turn
  is recorded as a missed pass at once, and one whose process died carrying it —
  a watch session killed mid-pass, which writes nothing at all, as the
  factory-flow instance's first pass was on 2026-09-26 — is found by the next
  pull that considers the instance, once the claim has stood five minutes with
  no ending and no turn in flight, and recorded then, starting when the pass was
  taken. `--json` marks each with `missed`, carrying the `trigger` (`schedule`,
  `events`, or `summons`) and `how` (`unfired`, `cancelled`, or
  `conversation-held`). The last is a pass whose bounded wait behind a turn in
  flight ended before its first turn, naming the holder rather than recording
  a failed firing. The latest
  missed pass since an instance's last completed one is also on its line in
  `yoyo status`, as the harness's move — see [the program managers'
  lines](#where-the-harness-stands-the-four-lines).
- **A pass stopped by its turn bound** is recorded as partial, naming the bound,
  so a truncated pass is never mistaken for a finished one.
- **A development manager's pass that leaves docket entries unread** records
  how many live entries were never delivered and names the oldest, including
  when a provider refusal ended the pass. Its continuation turns carry each
  next slice, without repeating entries already delivered on that pass. The
  next pass starts with the unread entries. `--json` carries this evidence in
  `docket`: the delivered count, the ordered `undelivered` entries, and `oldest`.
- **A firing that failed before its first turn** is recorded as a failed firing,
  not a partial pass, and its header says `FAILED FIRING` with the cause:
  the harness refused the message it composed for the pass, the role's
  conversation could not be opened, or what the turn would carry could not be
  assembled. Nothing was asked of the role and nothing was spent, and the record
  keeps the refusal's own words — `scheduled pass's message is 47768 bytes,
  limit is 32768`, say. `--json` carries the cause as `not_started`
  (`message-refused`, `conversation-unopened`, or `context-unassembled`). A
  firing the provider refused, or one the
  [outage wait](#waiting-out-a-provider-nobody-can-reach) recorded, is not one
  of these: waiting ends those, and nothing waiting does ends this.
- **A pass whose reply carried more than one block** shows the last block as its
  account and says beside it that more than one was sent. It is an account, not
  a lost pass: the role slipped on the one-block contract, and the decisions it
  took are on the record rather than thrown away over the shape of the reply.

**A task that fails before its first turn twice in a row is raised rather than
left in this log.** From 06:39Z on 2026-09-26 every development manager sweep
was refused before its first turn, six times in a row, and every triage
decision those sweeps would have made waited a day; the only account was one
line per firing here, and it was found by somebody reading the log. So from the
second such firing in a row — counted back to the last firing that took a turn,
with firings the provider refused neither counting nor resetting the count —
the task is an entry on `yoyo status`'s fourth line naming the task,
the cause, the latest refusal, and how many in a row — under `Needs a human`
where the operator's move ends it, and under `Waiting on the harness` where the
harness's does:

```text
Needs a human: nothing
Waiting on the harness (1):
  the recurring task development-manager-sweep has failed before its first turn 2 times in a row since 2026-09-26T06:39:00Z: the harness refused the message it composed for the pass; latest: scheduled pass's message is 47768 bytes, limit is 32768; … — the harness's — the harness refuses what it composed for the pass, which is a defect in the harness rather than anything waiting it out will end; every firing meets the same refusal until the harness is fixed, and the first firing that takes a turn clears this
```

Whose move it is follows the cause. A message the harness refused, or a turn it
could not assemble, is **the harness's**: it composed what it then refused. A
conversation that would not open is **the operator's**: what stops it is a role
no agent fills, a conversation record that will not load, or a session somebody
else is holding. `--json` carries the entry with kind `failing-task`, the task
as its `id`, and the record under `failing_task`: the task, role, cause, latest
problem, the count as `failures`, and `first_at`, `raised_at` (the second
failure), and `latest_at`. The dashboard's list of what is waiting, and on whom, opens
the same record.

[The channel](reporting.md#a-recurring-task-failing-before-its-first-turn) says
it once as a `warning` when it becomes an entry, and once more as `critical`,
sent to the operators directly as well, once it has stood two hours. It is not
said again beyond that. The first firing that takes a turn ends these pre-turn
messages. Once three executions have failed, the broader
[product pass finding](#the-supervisors-maintenance-pass) also stands, with the
factory-flow program manager watching and the development manager resolving the
cause; only a successful pass clears that finding.

One turn may report at most twenty findings and five questions, and a whole
firing holds what its turns come to. A pass that ran past even that says so in
its own summary, naming how many entries are not listed — a shortened list that
said nothing would read as a pass that found less than it did.

The reports live beside the run state, under
`<state root>/projects/<product id>/state/sweeps/`, with each task's cadence recorded
in its own file there. Nothing in the repository holds them: like the collected
reports, a sweep outlives the session that produced it.

The log is appended to once per firing and never rewritten, and a write of a
whole pass is not atomic, so a process killed partway through one can leave a
torn line behind. A listing names that line and carries on rather than failing:
one interrupted write must not cost every report around it, on the only surface
those reports are read from. What it will not do is drop the line quietly — a
listing short by a record it never mentioned is a worse answer than the failure
it replaced.

A log that cannot be read **to the end** is the same rule one step further. A
line too long for the reader stops the reading dead rather than being set aside,
so nothing past it is seen at all — and what was read before it is still shown,
with a line after the listing saying it stops where the reading stopped rather
than where the log does. The command still exits non-zero, and `--json` carries
the same sentence in its `error` field beside the passes it did read. The reports
before such a line are ordinary records and worth having; what a reader must not
be left with is a listing that looks complete and is not.
