<!--
Landed by yoyodyne-ifd.117.3, tranche 3 of the configuration.md split, with
docs/configuration.md left intact. The link below into ../configuration.md
resolves today and points at a section no tranche has a home for:

  #waiting-out-a-network-that-dropped -> no row in docs/docs-map.md; it stays
                                         in configuration.md until the map
                                         gives it a home, which is why this
                                         guide links out to a section that
                                         sits between two of its own

117.3 retargeted setup.md's #triage-thresholds and
#waiting-out-a-provider-that-refuses to this guide when it landed.

"The configuration index ... lists the other guides" below is a forward claim:
configuration.md becomes the index in 117.4.

Scope against docs/docs-map.md: the three sections the map's disposition table
assigns this guide — Waiting out a provider that refuses, Relaunching a run
the provider killed, and Triage thresholds — with their children. Two of those
children have no row of their own, because the table was last reconciled on
2026-08-24 and the file has grown since: Serving a turn from a permitted
alternate model and Pinning an agent to a model version both sit under Waiting
out a provider that refuses, so both go where their parent goes.

Extracted from the docs/configuration.md beside this file, section for section,
with the prose left word for word. The only edits are to links: a relative path
out of docs/configuration/ gains a ../ prefix, and a link into a section the
split has moved into a guide — one of this tranche's siblings or an earlier
tranche's — is retargeted at that guide. Diffing
this guide's body against lines 2948-3433 and 3795-4577 of that file shows those
link lines and nothing else.

Size: 1312 lines against the map's 513-line budget; the sections themselves
grew after the map's counts were taken, Triage thresholds most of all.
-->
# Configuring triage thresholds and provider waits

What the harness does when a provider refuses, serves a turn from another
model, or dies mid-run, and the thresholds that decide when a stalled run, a
stuck merge, or a work item that has been given enough is escalated to you.

[The configuration index](../configuration.md) lists the other guides.

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
[not authenticated or cannot be reached](../operations.md#waiting-out-a-provider-nobody-can-reach),
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
item, or [`yoyo reconcile`](../operations.md#waiting-out-a-provider-usage-limit),
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
[Waiting out a provider usage limit](../operations.md#waiting-out-a-provider-usage-limit).

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
[A provider refusal outside a run](../operations.md#a-provider-refusal-outside-a-run)
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
[waited out on the recovery window](../configuration.md#waiting-out-a-network-that-dropped) rather
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
alternate named is a [`yoyo doctor`](../operations.md#checking-the-installation)
warning under `failover`, before any window closes; and while a window is
holding every role, the channel and `yoyo status`
[say so](../reporting.md#the-provider-holding-every-role), naming the reset and
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

**A crossing covers conversation turns and nothing else.** An alternate on the
agent's own provider serves its exchange rounds and its side threads as well; one
that leaves the provider does not, because those are answered on the endpoint the
agent is configured for and there is no crossing for them to take. An agent whose
alternate names a provider therefore has its conversation carried through a window
and its exchange rounds and side turns waiting the window out, alongside the run
invocations. `yoyo agent` says which of the two an agent has.

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
  that can refuse every tool, and a developer one that can scope writes to a
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
run's developer and reviewer invocations ask for `model`. `yoyo agent list` says
so for every pinned agent rather than leaving it to be assumed.

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
[waiting a dropped connection out](../configuration.md#waiting-out-a-network-that-dropped), which
is a different rule and is not configured — a death that is plainly a reset
connection is waited out on the recovery window at `0` exactly as it is at `2`,
because what the operator ruled is that the harness never fails outright on
anything that can recover. What `0` decides is how much of the provider's
unclassifiable weather one run absorbs before it stops.

What happens once the budget is spent depends on what killed the invocation. A
death nothing can classify stops the run and records a blocker on the work item
naming the provider's own last message. A death that is plainly a dropped
connection does not: it is
[waited out and asked again](../configuration.md#waiting-out-a-network-that-dropped) past the
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

The docket is built when something scans: `yoyo reconcile`, and the moment a
development manager conversation opens. There is no scheduled process behind it,
so `stuck_merge_age` is a floor rather than a promise — a publication becomes
docketable at that age and is docketed the next time one of those happens.

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
[the conversation guide](../conversation.md#resuming-an-approved-change-the-environment-stopped)
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
[step only a person can take](../work.md#letting-the-harness-choose-the-work) that
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
[convergence sweep](../operations.md#recovering-interrupted-runs) when it retires
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

An outstanding recovery decision or automatic continuation at checks or after a
silent provider stall keeps that checkout and branch out of the maintenance
sweep. Intake or capacity delays leave them intact, as does a gate refusing a
recorded recovery decision. An interrupted developer's uncommitted work stays in
its checkout for the continuation. If the checkout is already missing, a recorded
repair or automatic check continuation may restore it at its recorded path from
the surviving branch. The branch must
still point to the exact completed commit the harness recorded and pass its
ownership checks. Restoration keeps the run identifier, developer session, and
consumed budgets. Check approval
is cleared durably before restoration writes the checkout; a run stopped at review
returns through checks before review can proceed.
An unfinished restoration is also recorded before writing. After a restart,
the harness requires a complete, clean checkout at the recorded revision before
it clears that record and continues the same decision or check stage. Removal
flags do not cancel an automatic check continuation: its action checks the
repository and restores a recoverable checkout before spending a continuation. A refused
restoration spends none and returns the stoppage to the development manager.

Restoration never overwrites an existing path or forces a branch out of another
checkout. A missing or changed branch, unverifiable ownership, an unfinished
developer attempt, or work captured separately on a preserved-work ref refuses
branch-only restoration. Missing uncommitted work is never reported recovered.
The decision remains standing, no continuation is spent, and the development
manager decides what follows.

Restoration writes through opened directory handles, so replacing the checkout
root with a symlink cannot redirect the files outside it. Git supplies the
committed objects without creating the checkout. Existing index and export files
are replaced with new files, preserving the contents of files hard-linked
elsewhere. Checkout filters selected by the recorded tree's attributes refuse
restoration because committed objects cannot prove those filters' output was
recovered; unused filter definitions do not refuse restoration. Such a refusal leaves
the branch and recovery decision available for the development manager.

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
recorded on the run before it stops, so the continuation hands the same
developer the same conflict to reconcile on top of the target
(yoyodyne-ifd.132); a run whose provider kept refusing, or that died before
anything judged its work, never had a failure returned to its developer, so
there is no repair loop to re-enter, and a re-run is what those need. The one exception is a stall — a provider
the harness stopped on time, settled by the sweep with the developer session
preserved — which is continued at the step it stalled in: the developer attempt
in that session, or, for a run stopped at its checks or its review, that step
asked again on the change it has with no developer invoked. The preserved worktree has
to be as the harness left it: what a continued developer is handed back is
whatever is in that worktree, so a HEAD that moved — an operator mid-surgery, an
agent that committed — is a person's to decide about, and the refusal leaves the
item blocked and says so. And that worktree has to still hold the change: a
checkout the harness would call its own and that holds nothing passes the gate
above and fails this one, and a developer handed the reviewer's findings and an
empty directory delivers an empty repair or reinvents the change from them, with
nothing in the run's record afterwards to tell either from a repair that went
well. And the decision standing about the stoppage has to still be the repair:
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
it has stopped. The item first receives a preparation note citing the decision,
and its claim for the repair is confirmed. The run's blocker is then cleared onto
the continuation that supersedes it, which keeps the words it was recorded in and
the grant that bought the attempt. The order is the item first, because a run
recorded as running behind an item that still says it is blocked is the one
half-finished state nothing else here would notice. Preconditions are checked
before the item claim, but later failures can leave the item claimed or the
continuation and its expenditure already recorded. Success is reported, and its
note appended, only after the continuation has been saved and read back. A refused
claim or an unconfirmed save reports that outcome and dispatches no developer.
A save that replaced the record but failed to confirm its durability is reported
as uncertain, even when the continuation can be read back.

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
was lost, then records its confirmation. If execution has completed but its
dispatch acknowledgement was not saved, the terminal record and completion time
confirm that the latest continuation has ended; note recovery records both
acknowledgements without dispatching again. It leaves live leased runs alone.
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
else ([a protected target lands through its pull request](publishing.md#a-protected-target-lands-through-its-pull-request)), and the re-arm
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
one, so a promotion that [loses its race](publishing.md#losing-a-race-for-the-target-branch)
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
([the counters of the first such run](../diagnoses/yoyodyne-ifd-399-empty-diff-rule-is-base-relative.md)
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
[`docs/team-mode-scope.md`](../team-mode-scope.md#a-recorded-gap-per-item-budgets-are-per-machine)
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
